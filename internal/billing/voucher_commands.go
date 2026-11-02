package billing

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/security"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func voucherText(s string, max int) bool {
	if len(s) < 1 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func voucherCommandValid(c VoucherCommandContext, tenant bool) bool {
	return voucherID(c.OperationID) && voucherID(c.ActorID) && ((tenant && voucherID(c.TenantID)) || (!tenant && c.TenantID == ""))
}
func (v *VoucherService) writeAllowed(runtime bool) error {
	if v.options.RecoveryMode {
		return voucherFail("commerce_unavailable")
	}
	if runtime && !v.options.RuntimeAllowed {
		return voucherFail("vouchers_runtime_disabled")
	}
	return nil
}
func voucherRetryable(e error) bool {
	var pg *pgconn.PgError
	return errors.As(e, &pg) && (pg.Code == "40P01" || pg.Code == "40001")
}

// This runner records the COMMIT boundary separately; uncertain commits never
// enter the SQL retry loop and never retain caller-visible attempt secrets.
func (v *VoucherService) transact(ctx context.Context, fn func(context.Context, pgx.Tx) error) (err error) {
	defer func() {
		if recover() != nil {
			err = voucherFail("commerce_unavailable")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		e, retry := func() (error, bool) {
			tx, e := v.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			if e != nil {
				return e, false
			}
			defer voucherRollback(ctx, tx)
			if _, e = tx.Exec(ctx, `SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)`); e != nil {
				return e, voucherRetryable(e)
			}
			if e = fn(ctx, tx); e != nil {
				return e, voucherRetryable(e)
			}
			if e = tx.Commit(ctx); e == nil {
				return nil, false
			}
			var pg *pgconn.PgError
			if errors.As(e, &pg) {
				return e, voucherRetryable(e)
			}
			if errors.Is(e, pgx.ErrTxCommitRollback) {
				return e, false
			}
			return voucherFail("commerce_outcome_unknown"), false
		}()
		if e == nil {
			return nil
		}
		if !retry || attempt == 2 || ctx.Err() != nil {
			return voucherSafeError(e)
		}
	}
	return voucherFail("commerce_unavailable")
}
func voucherOwner(ctx context.Context, tx pgx.Tx, c VoucherCommandContext) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{{`SELECT id FROM tenants WHERE id=$1 FOR SHARE`, []any{c.TenantID}}, {`SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, []any{c.TenantID, c.ActorID}}, {`SELECT id FROM users WHERE id=$1 FOR SHARE`, []any{c.ActorID}}} {
		var id string
		if e := tx.QueryRow(ctx, q.sql, q.args...).Scan(&id); errors.Is(e, pgx.ErrNoRows) {
			return voucherFail("forbidden")
		} else if e != nil {
			return e
		}
	}
	var ok bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=$1 AND u.id=$2 AND t.status='active' AND m.status='active' AND u.status='active' AND m.role='owner')`, c.TenantID, c.ActorID).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return voucherFail("forbidden")
	}
	return nil
}
func voucherFinance(ctx context.Context, tx pgx.Tx, roles map[string]string) error {
	ids := make([]string, 0, len(roles))
	for id := range roles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if e := role(ctx, tx, id, roles[id]); e != nil {
			var b *Error
			if errors.As(e, &b) && b.Code == "forbidden" {
				return voucherFail("forbidden")
			}
			return e
		}
	}
	return nil
}
func voucherBatchLock(ctx context.Context, tx pgx.Tx, id string) (VoucherBatchView, error) {
	var locked string
	if e := tx.QueryRow(ctx, `SELECT id FROM voucher_batches WHERE id=$1 FOR UPDATE`, id).Scan(&locked); errors.Is(e, pgx.ErrNoRows) {
		return VoucherBatchView{}, voucherFail("not_found")
	} else if e != nil {
		return VoucherBatchView{}, e
	}
	return voucherBatchTx(ctx, tx, id)
}
func voucherPolicyLock(ctx context.Context, tx pgx.Tx) (bool, error) {
	var enabled bool
	e := tx.QueryRow(ctx, `SELECT enabled FROM voucher_policy WHERE singleton FOR SHARE`).Scan(&enabled)
	return enabled, e
}

type voucherRejected struct {
	reason   string
	decision func(context.Context, pgx.Tx) error
}

func (r *voucherRejected) Error() string { return r.reason }
func voucherReject(reason string) error  { return &voucherRejected{reason: reason} }

type voucherExecution struct {
	ctx       context.Context
	tx        pgx.Tx
	savepoint bool
}

func (x *voucherExecution) begin() error {
	if x.savepoint {
		return nil
	}
	_, e := x.tx.Exec(x.ctx, `SAVEPOINT voucher_business`)
	if e == nil {
		x.savepoint = true
	}
	return e
}

type voucherEffect struct {
	resource         string
	amount           *Money
	funding, journal *string
	batch            *VoucherBatchView
	codes            []IssuedVoucherCode
}
type voucherRunResult struct {
	receipt VoucherOperationReceipt
	effect  voucherEffect
	fresh   bool
}
type voucherCommandSpec struct {
	command VoucherCommandContext
	kind    string
	target  *string
	body    func(string) (json.RawMessage, error)
}

func voucherBody(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
func voucherDigest(spec voucherCommandSpec, version string) (string, error) {
	body, e := spec.body(version)
	if e != nil {
		return "", e
	}
	scope := "platform"
	if spec.command.TenantID != "" {
		scope = "tenant:" + spec.command.TenantID
	}
	b, e := json.Marshal(struct {
		V      int             `json:"v"`
		Actor  string          `json:"actor"`
		Scope  string          `json:"scope"`
		Kind   string          `json:"kind"`
		Target *string         `json:"target_id"`
		Body   json.RawMessage `json:"body"`
	}{1, spec.command.ActorID, scope, spec.kind, spec.target, body})
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func (v *VoucherService) run(ctx context.Context, spec voucherCommandSpec, apply func(*voucherExecution) (voucherEffect, error)) (voucherRunResult, error) {
	var out voucherRunResult
	e := v.transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out = voucherRunResult{}
		version := ""
		var versionArg any
		if spec.kind == "voucher.redeem" {
			version = v.options.Keys.ActiveVersion()
			versionArg = version
		}
		digest, e := voucherDigest(spec, version)
		if e != nil {
			return e
		}
		scope := "platform"
		if spec.command.TenantID != "" {
			scope = "tenant:" + spec.command.TenantID
		}
		tag, e := tx.Exec(ctx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest,fingerprint_key_version) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, spec.command.OperationID, spec.command.ActorID, scope, spec.kind, digest, versionArg)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var actor, originalScope, kind, originalDigest string
			var originalVersion *string
			if e = tx.QueryRow(ctx, `SELECT actor,scope,kind,request_digest,fingerprint_key_version FROM commerce_commands WHERE operation_id=$1`, spec.command.OperationID).Scan(&actor, &originalScope, &kind, &originalDigest, &originalVersion); e != nil {
				return e
			}
			if actor != spec.command.ActorID || originalScope != scope || kind != spec.kind {
				return voucherFail("commerce_operation_conflict")
			}
			version = ""
			if originalVersion != nil {
				version = *originalVersion
			}
			digest, e = voucherDigest(spec, version)
			if e != nil {
				return e
			}
			if subtle.ConstantTimeCompare([]byte(digest), []byte(originalDigest)) != 1 {
				return voucherFail("commerce_operation_conflict")
			}
			if e = voucherReadAuth(ctx, tx, VoucherReader{ActorID: spec.command.ActorID, TenantID: spec.command.TenantID}, scope == "platform", false); e != nil {
				return e
			}
			out.receipt, e = scanVoucherReceipt(tx.QueryRow(ctx, `SELECT `+voucherReceiptColumns+` FROM commerce_operation_receipts WHERE operation_id=$1`, spec.command.OperationID))
			if e != nil {
				return e
			}
			if spec.kind == "voucher.batch.create" && out.receipt.Status == "accepted" && out.receipt.ResourceID != nil {
				b, e := voucherBatchTx(ctx, tx, *out.receipt.ResourceID)
				if e != nil {
					return e
				}
				out.effect.batch = &b
			}
			return nil
		}
		x := &voucherExecution{ctx: ctx, tx: tx}
		effect, e := apply(x)
		status, reason := "accepted", "accepted"
		var rejected *voucherRejected
		if errors.As(e, &rejected) {
			if x.savepoint {
				if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT voucher_business`); err != nil {
					return err
				}
			}
			effect = voucherEffect{}
			status, reason = "rejected", rejected.reason
			if rejected.decision != nil {
				if err := rejected.decision(ctx, tx); err != nil {
					return err
				}
			}
		} else if e != nil {
			return e
		}
		var resource any
		if status == "accepted" {
			resource = effect.resource
		}
		if _, e = tx.Exec(ctx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at) SELECT operation_id,kind,scope,actor,$2,$3,$4,$5,$6,$7,created_at FROM commerce_commands WHERE operation_id=$1`, spec.command.OperationID, status, reason, resource, effect.amount, effect.funding, effect.journal); e != nil {
			return e
		}
		out.receipt, e = scanVoucherReceipt(tx.QueryRow(ctx, `SELECT `+voucherReceiptColumns+` FROM commerce_operation_receipts WHERE operation_id=$1`, spec.command.OperationID))
		out.effect = effect
		out.fresh = true
		return e
	})
	if e != nil {
		return voucherRunResult{}, e
	}
	return out, nil
}

func (v *VoucherService) CreateVoucherBatch(ctx context.Context, in CreateVoucherBatchInput) (CreateVoucherBatchResult, error) {
	if !voucherCommandValid(in.Command, false) || in.FaceAmount <= 0 || in.Count < 1 || in.Count > 100 || !new(big.Int).Mul(big.NewInt(int64(in.FaceAmount)), big.NewInt(int64(in.Count))).IsInt64() || !voucherText(in.Reason, 200) || !voucherText(in.PurposeRef, 256) || !security.ValidReference(in.PurposeRef, 256) || in.ExpiresAt.IsZero() || in.ExpiresAt.Location() != time.UTC || in.ExpiresAt.Nanosecond()%1000 != 0 {
		return CreateVoucherBatchResult{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(true); e != nil {
		return CreateVoucherBatchResult{}, e
	}
	body := voucherBody(struct {
		Amount  Money  `json:"face_amount"`
		Count   int    `json:"count"`
		Expires string `json:"expires_at"`
		Purpose string `json:"purpose_ref"`
		Reason  string `json:"reason"`
	}{in.FaceAmount, in.Count, in.ExpiresAt.Format(voucherTimeLayout), in.PurposeRef, in.Reason})
	out, e := v.run(ctx, voucherCommandSpec{command: in.Command, kind: "voucher.batch.create", body: func(string) (json.RawMessage, error) { return body, nil }}, func(x *voucherExecution) (voucherEffect, error) {
		enabled, e := voucherPolicyLock(x.ctx, x.tx)
		if e != nil {
			return voucherEffect{}, e
		}
		if e = voucherFinance(x.ctx, x.tx, map[string]string{in.Command.ActorID: "finance_operator"}); e != nil {
			return voucherEffect{}, e
		}
		if !enabled {
			return voucherEffect{}, voucherReject("vouchers_disabled")
		}
		var now time.Time
		if e = x.tx.QueryRow(x.ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			return voucherEffect{}, e
		}
		if in.ExpiresAt.Before(now.Add(time.Hour)) || in.ExpiresAt.After(now.Add(365*24*time.Hour)) {
			return voucherEffect{}, voucherReject("voucher_state_conflict")
		}
		if e = x.begin(); e != nil {
			return voucherEffect{}, e
		}
		id := uuid.NewString()
		if _, e = x.tx.Exec(x.ctx, `INSERT INTO voucher_batches(id,face_amount,count,expires_at,purpose_ref,reason,maker,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, int64(in.FaceAmount), in.Count, in.ExpiresAt, in.PurposeRef, in.Reason, in.Command.ActorID, now); e != nil {
			return voucherEffect{}, e
		}
		codes := make([]IssuedVoucherCode, 0, in.Count)
		version := v.options.Keys.ActiveVersion()
		for i := 0; i < in.Count; i++ {
			voucher := uuid.NewString()
			secret := security.Random(32)
			digest, e := v.options.Keys.CodeDigest(version, voucher, secret)
			if e != nil {
				return voucherEffect{}, voucherFail("voucher_key_unavailable")
			}
			if _, e = x.tx.Exec(x.ctx, `INSERT INTO vouchers(id,batch_id,code_digest,digest_key_version) VALUES($1,$2,$3,$4)`, voucher, id, digest, version); e != nil {
				return voucherEffect{}, e
			}
			codes = append(codes, IssuedVoucherCode{VoucherID: voucher, Code: "mav1." + voucher + "." + secret})
		}
		if e = audit(x.ctx, x.tx, "", in.Command.ActorID, "voucher.batch.created", id, in.Command.RequestID, map[string]any{"face_amount": in.FaceAmount, "count": in.Count, "purpose_ref": in.PurposeRef}); e != nil {
			return voucherEffect{}, e
		}
		b, e := voucherBatchTx(x.ctx, x.tx, id)
		if e != nil {
			return voucherEffect{}, e
		}
		return voucherEffect{resource: id, batch: &b, codes: codes}, nil
	})
	if e != nil {
		return CreateVoucherBatchResult{}, e
	}
	secrets := out.fresh && out.receipt.Status == "accepted"
	return CreateVoucherBatchResult{Receipt: out.receipt, Batch: out.effect.batch, Codes: out.effect.codes, SecretsAvailable: secrets, Created: secrets}, nil
}
func (v *VoucherService) ApproveVoucherBatch(ctx context.Context, in ApproveVoucherBatchInput) (VoucherOperationReceipt, error) {
	if !voucherCommandValid(in.Command, false) || !voucherID(in.BatchID) || in.ExpectedRevision < 1 {
		return VoucherOperationReceipt{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(false); e != nil {
		return VoucherOperationReceipt{}, e
	}
	body := voucherBody(struct {
		Revision string `json:"expected_revision"`
	}{strconv.FormatInt(in.ExpectedRevision, 10)})
	out, e := v.run(ctx, voucherCommandSpec{in.Command, "voucher.batch.approve", &in.BatchID, func(string) (json.RawMessage, error) { return body, nil }}, func(x *voucherExecution) (voucherEffect, error) {
		b, e := voucherBatchLock(x.ctx, x.tx, in.BatchID)
		if e != nil {
			return voucherEffect{}, e
		}
		if b.Maker == in.Command.ActorID {
			return voucherEffect{}, voucherFail("forbidden")
		}
		if e = voucherFinance(x.ctx, x.tx, map[string]string{b.Maker: "finance_operator", in.Command.ActorID: "finance_approver"}); e != nil {
			return voucherEffect{}, e
		}
		if b.Revision != strconv.FormatInt(in.ExpectedRevision, 10) {
			return voucherEffect{}, voucherReject("voucher_revision_conflict")
		}
		var now time.Time
		if e = x.tx.QueryRow(x.ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			return voucherEffect{}, e
		}
		if b.State != "proposed" || !now.Before(b.ExpiresAt) {
			return voucherEffect{}, voucherReject("voucher_state_conflict")
		}
		if e = x.begin(); e != nil {
			return voucherEffect{}, e
		}
		if _, e = x.tx.Exec(x.ctx, `UPDATE voucher_batches SET state='active',approver=$2,activated_at=$3 WHERE id=$1`, b.ID, in.Command.ActorID, now); e != nil {
			return voucherEffect{}, e
		}
		e = audit(x.ctx, x.tx, "", in.Command.ActorID, "voucher.batch.approved", b.ID, in.Command.RequestID, map[string]any{"maker": b.Maker})
		return voucherEffect{resource: b.ID}, e
	})
	return out.receipt, e
}
func (v *VoucherService) VoidVoucherBatch(ctx context.Context, in VoidVoucherBatchInput) (VoucherOperationReceipt, error) {
	if !voucherCommandValid(in.Command, false) || !voucherID(in.BatchID) || in.ExpectedRevision < 1 || !voucherText(in.Reason, 200) {
		return VoucherOperationReceipt{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(false); e != nil {
		return VoucherOperationReceipt{}, e
	}
	body := voucherBody(struct {
		Revision string `json:"expected_revision"`
		Reason   string `json:"reason"`
	}{strconv.FormatInt(in.ExpectedRevision, 10), in.Reason})
	out, e := v.run(ctx, voucherCommandSpec{in.Command, "voucher.batch.void", &in.BatchID, func(string) (json.RawMessage, error) { return body, nil }}, func(x *voucherExecution) (voucherEffect, error) {
		b, e := voucherBatchLock(x.ctx, x.tx, in.BatchID)
		if e != nil {
			return voucherEffect{}, e
		}
		if e = voucherFinance(x.ctx, x.tx, map[string]string{in.Command.ActorID: "finance_operator"}); e != nil {
			return voucherEffect{}, e
		}
		if b.Maker != in.Command.ActorID {
			return voucherEffect{}, voucherFail("forbidden")
		}
		if b.Revision != strconv.FormatInt(in.ExpectedRevision, 10) {
			return voucherEffect{}, voucherReject("voucher_revision_conflict")
		}
		if b.State != "proposed" {
			return voucherEffect{}, voucherReject("voucher_state_conflict")
		}
		if e = x.begin(); e != nil {
			return voucherEffect{}, e
		}
		if _, e = x.tx.Exec(x.ctx, `UPDATE voucher_batches SET state='voided',terminal_actor=$2,terminal_at=clock_timestamp() WHERE id=$1`, b.ID, in.Command.ActorID); e != nil {
			return voucherEffect{}, e
		}
		e = audit(x.ctx, x.tx, "", in.Command.ActorID, "voucher.batch.voided", b.ID, in.Command.RequestID, map[string]any{"reason": in.Reason})
		return voucherEffect{resource: b.ID}, e
	})
	return out.receipt, e
}
func (v *VoucherService) ProposeVoucherRevocation(ctx context.Context, in ProposeVoucherRevocationInput) (VoucherOperationReceipt, error) {
	if !voucherCommandValid(in.Command, false) || !voucherID(in.BatchID) || in.ExpectedRevision < 1 || in.ExpectedRedeemedCount < 0 || !voucherText(in.Reason, 200) {
		return VoucherOperationReceipt{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(false); e != nil {
		return VoucherOperationReceipt{}, e
	}
	body := voucherBody(struct {
		Revision string `json:"expected_revision"`
		Count    string `json:"expected_redeemed_count"`
		Reason   string `json:"reason"`
	}{strconv.FormatInt(in.ExpectedRevision, 10), strconv.FormatInt(in.ExpectedRedeemedCount, 10), in.Reason})
	out, e := v.run(ctx, voucherCommandSpec{in.Command, "voucher.revocation.create", &in.BatchID, func(string) (json.RawMessage, error) { return body, nil }}, func(x *voucherExecution) (voucherEffect, error) {
		b, e := voucherBatchLock(x.ctx, x.tx, in.BatchID)
		if e != nil {
			return voucherEffect{}, e
		}
		if e = voucherFinance(x.ctx, x.tx, map[string]string{in.Command.ActorID: "finance_operator"}); e != nil {
			return voucherEffect{}, e
		}
		if b.Revision != strconv.FormatInt(in.ExpectedRevision, 10) {
			return voucherEffect{}, voucherReject("voucher_revision_conflict")
		}
		if b.State != "active" {
			return voucherEffect{}, voucherReject("voucher_state_conflict")
		}
		if b.RedeemedCount != strconv.FormatInt(in.ExpectedRedeemedCount, 10) {
			return voucherEffect{}, voucherReject("voucher_evidence_stale")
		}
		if e = x.begin(); e != nil {
			return voucherEffect{}, e
		}
		id := uuid.NewString()
		if _, e = x.tx.Exec(x.ctx, `INSERT INTO voucher_revocations(id,batch_id,operation_id,maker,expected_revision,expected_redeemed_count,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, b.ID, in.Command.OperationID, in.Command.ActorID, in.ExpectedRevision, in.ExpectedRedeemedCount, in.Reason); e != nil {
			return voucherEffect{}, e
		}
		e = audit(x.ctx, x.tx, "", in.Command.ActorID, "voucher.revocation.created", id, in.Command.RequestID, map[string]any{"batch_id": b.ID, "reason": in.Reason})
		return voucherEffect{resource: id}, e
	})
	return out.receipt, e
}
func (v *VoucherService) ApproveVoucherRevocation(ctx context.Context, in ApproveVoucherRevocationInput) (VoucherOperationReceipt, error) {
	if !voucherCommandValid(in.Command, false) || !voucherID(in.ProposalID) {
		return VoucherOperationReceipt{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(false); e != nil {
		return VoucherOperationReceipt{}, e
	}
	body := voucherBody(struct{}{})
	out, e := v.run(ctx, voucherCommandSpec{in.Command, "voucher.revocation.approve", &in.ProposalID, func(string) (json.RawMessage, error) { return body, nil }}, func(x *voucherExecution) (voucherEffect, error) {
		var batch string
		if e := x.tx.QueryRow(x.ctx, `SELECT batch_id FROM voucher_revocations WHERE id=$1`, in.ProposalID).Scan(&batch); errors.Is(e, pgx.ErrNoRows) {
			return voucherEffect{}, voucherFail("not_found")
		} else if e != nil {
			return voucherEffect{}, e
		}
		b, e := voucherBatchLock(x.ctx, x.tx, batch)
		if e != nil {
			return voucherEffect{}, e
		}
		var maker, rev, count string
		var decided bool
		if e = x.tx.QueryRow(x.ctx, `SELECT r.maker,r.expected_revision::text,r.expected_redeemed_count::text,EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.proposal_id=r.id) FROM voucher_revocations r WHERE r.id=$1`, in.ProposalID).Scan(&maker, &rev, &count, &decided); e != nil {
			return voucherEffect{}, e
		}
		if maker == in.Command.ActorID {
			return voucherEffect{}, voucherFail("forbidden")
		}
		if e = voucherFinance(x.ctx, x.tx, map[string]string{maker: "finance_operator", in.Command.ActorID: "finance_approver"}); e != nil {
			return voucherEffect{}, e
		}
		if decided {
			return voucherEffect{}, voucherReject("voucher_state_conflict")
		}
		decision := func(ctx context.Context, tx pgx.Tx, status, reason string) error {
			_, e := tx.Exec(ctx, `INSERT INTO voucher_revocation_decisions(id,proposal_id,batch_id,operation_id,approver,status,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), in.ProposalID, b.ID, in.Command.OperationID, in.Command.ActorID, status, reason)
			return e
		}
		if b.State != "active" || b.Revision != rev || b.RedeemedCount != count {
			return voucherEffect{}, &voucherRejected{reason: "voucher_evidence_stale", decision: func(ctx context.Context, tx pgx.Tx) error {
				return decision(ctx, tx, "rejected", "voucher_evidence_stale")
			}}
		}
		if e = x.begin(); e != nil {
			return voucherEffect{}, e
		}
		if _, e = x.tx.Exec(x.ctx, `UPDATE voucher_batches SET state='revoked',terminal_actor=$2,terminal_at=clock_timestamp() WHERE id=$1`, b.ID, in.Command.ActorID); e != nil {
			return voucherEffect{}, e
		}
		if e = decision(x.ctx, x.tx, "accepted", "accepted"); e != nil {
			return voucherEffect{}, e
		}
		e = audit(x.ctx, x.tx, "", in.Command.ActorID, "voucher.revocation.approved", in.ProposalID, in.Command.RequestID, map[string]any{"batch_id": b.ID})
		return voucherEffect{resource: in.ProposalID}, e
	})
	return out.receipt, e
}
func parseVoucherCode(code string) (string, string, bool) {
	if len(code) != 85 || !strings.HasPrefix(code, "mav1.") {
		return "", "", false
	}
	parts := strings.Split(code, ".")
	if len(parts) != 3 || !voucherID(parts[1]) {
		return "", "", false
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[2])
	return parts[1], parts[2], e == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == parts[2]
}
func (v *VoucherService) RedeemVoucher(ctx context.Context, in RedeemVoucherInput) (VoucherOperationReceipt, error) {
	id, secret, valid := parseVoucherCode(in.Code)
	if !voucherCommandValid(in.Command, true) || !valid {
		return VoucherOperationReceipt{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(true); e != nil {
		return VoucherOperationReceipt{}, e
	}
	out, e := v.run(ctx, voucherCommandSpec{command: in.Command, kind: "voucher.redeem", body: func(version string) (json.RawMessage, error) {
		fp, e := v.options.Keys.CommandFingerprint(version, id, secret)
		if e != nil {
			return nil, voucherFail("voucher_key_unavailable")
		}
		return voucherBody(struct {
			Fingerprint string `json:"voucher_fingerprint"`
		}{fp}), nil
	}}, func(x *voucherExecution) (voucherEffect, error) {
		var batch string
		exists := true
		if e := x.tx.QueryRow(x.ctx, `SELECT batch_id FROM vouchers WHERE id=$1`, id).Scan(&batch); errors.Is(e, pgx.ErrNoRows) {
			exists = false
		} else if e != nil {
			return voucherEffect{}, e
		}
		if exists {
			if _, e := voucherBatchLock(x.ctx, x.tx, batch); e != nil {
				return voucherEffect{}, e
			}
		}
		var digest, version string
		if exists {
			if e := x.tx.QueryRow(x.ctx, `SELECT code_digest,digest_key_version FROM vouchers WHERE id=$1`, id).Scan(&digest, &version); e != nil {
				return voucherEffect{}, e
			}
		}
		enabled, e := voucherPolicyLock(x.ctx, x.tx)
		if e != nil {
			return voucherEffect{}, e
		}
		if e = voucherOwner(x.ctx, x.tx, in.Command); e != nil {
			return voucherEffect{}, e
		}
		if !enabled {
			return voucherEffect{}, voucherReject("vouchers_disabled")
		}
		if !exists {
			return voucherEffect{}, voucherReject("voucher_unavailable")
		}
		expected, e := v.options.Keys.CodeDigest(version, id, secret)
		if e != nil {
			return voucherEffect{}, voucherFail("voucher_key_unavailable")
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(digest)) != 1 {
			return voucherEffect{}, voucherReject("voucher_unavailable")
		}
		if e = x.begin(); e != nil {
			return voucherEffect{}, e
		}
		fund, e := v.store.ApplyFundingTx(x.ctx, x.tx, FundingSourceRef{FundingVoucherGrant, id}, FundingScope{TenantID: in.Command.TenantID, ActorID: in.Command.ActorID, CommandID: in.Command.OperationID})
		if e != nil {
			var b *Error
			if errors.As(e, &b) {
				switch b.Code {
				case "amount_overflow":
					return voucherEffect{}, voucherReject("funding_amount_overflow")
				case "voucher_unavailable":
					return voucherEffect{}, voucherReject("voucher_unavailable")
				}
			}
			return voucherEffect{}, e
		}
		if e = audit(x.ctx, x.tx, in.Command.TenantID, in.Command.ActorID, "voucher.redeemed", fund.RedemptionID, in.Command.RequestID, map[string]any{"amount": fund.Receipt.Amount, "funding_receipt_id": fund.Receipt.ID}); e != nil {
			return voucherEffect{}, e
		}
		return voucherEffect{resource: fund.RedemptionID, amount: &fund.Receipt.Amount, funding: &fund.Receipt.ID, journal: &fund.Receipt.JournalID}, nil
	})
	return out.receipt, e
}
func (v *VoucherService) UpdateVoucherPolicy(ctx context.Context, in UpdateVoucherPolicyInput) (VoucherPolicyView, error) {
	if !voucherID(in.ActorID) || in.ExpectedRevision < 1 {
		return VoucherPolicyView{}, voucherFail("invalid_request")
	}
	if e := v.writeAllowed(false); e != nil {
		return VoucherPolicyView{}, e
	}
	if in.Enabled && !v.options.RuntimeAllowed {
		return VoucherPolicyView{}, voucherFail("vouchers_runtime_disabled")
	}
	var out VoucherPolicyView
	e := v.transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out = VoucherPolicyView{}
		if e := voucherFinance(ctx, tx, map[string]string{in.ActorID: "admin"}); e != nil {
			return e
		}
		var enabled bool
		var rev int64
		if e := tx.QueryRow(ctx, `SELECT enabled,revision FROM voucher_policy WHERE singleton FOR UPDATE`).Scan(&enabled, &rev); e != nil {
			return e
		}
		if rev != in.ExpectedRevision {
			return voucherFail("voucher_policy_conflict")
		}
		if enabled != in.Enabled {
			if _, e := tx.Exec(ctx, `UPDATE voucher_policy SET enabled=$1 WHERE singleton`, in.Enabled); e != nil {
				return e
			}
			if e := audit(ctx, tx, "", in.ActorID, "voucher.policy.update", "voucher_policy", in.RequestID, map[string]any{"before": enabled, "after": in.Enabled}); e != nil {
				return e
			}
		}
		if e := tx.QueryRow(ctx, `SELECT enabled,revision::text FROM voucher_policy WHERE singleton`).Scan(&out.Enabled, &out.Revision); e != nil {
			return e
		}
		out.RuntimeAllowed = v.options.RuntimeAllowed
		out.EffectiveEnabled = out.Enabled && out.RuntimeAllowed
		return nil
	})
	if e != nil {
		return VoucherPolicyView{}, e
	}
	return out, nil
}

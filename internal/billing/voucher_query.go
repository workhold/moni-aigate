package billing

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

func voucherID(id string) bool {
	u, e := uuid.Parse(id)
	return e == nil && u != uuid.Nil && u.String() == id
}
func voucherRollback(ctx context.Context, tx pgx.Tx) {
	// Stay inside the operation budget. pgx closes a connection if rollback
	// fails (including an expired context), and pgxpool then releases it.
	_ = tx.Rollback(ctx)
}
func (s *Store) VoucherKeyVersions(ctx context.Context) (VoucherKeyVersions, error) {
	result := VoucherKeyVersions{Code: []string{}, Fingerprint: []string{}}
	ctx, c := context.WithTimeout(ctx, 5*time.Second)
	defer c()
	rows, e := s.pool.Query(ctx, `SELECT kind,version FROM (
 (SELECT 'code' kind,digest_key_version version FROM vouchers GROUP BY digest_key_version ORDER BY digest_key_version COLLATE "C" LIMIT 129)
 UNION ALL (SELECT 'fingerprint' kind,fingerprint_key_version version FROM commerce_commands WHERE fingerprint_key_version IS NOT NULL GROUP BY fingerprint_key_version ORDER BY fingerprint_key_version COLLATE "C" LIMIT 129)) versions`)
	if e != nil {
		return result, voucherFail("commerce_unavailable")
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			return VoucherKeyVersions{}, voucherFail("commerce_unavailable")
		}
		seen[v] = true
		if k == "code" {
			result.Code = append(result.Code, v)
		} else {
			result.Fingerprint = append(result.Fingerprint, v)
		}
	}
	if rows.Err() != nil {
		return VoucherKeyVersions{}, voucherFail("commerce_unavailable")
	}
	if len(seen) > 128 {
		return VoucherKeyVersions{}, voucherFail("voucher_key_unavailable")
	}
	return result, nil
}

func voucherReadAuth(ctx context.Context, tx pgx.Tx, r VoucherReader, platform, admin bool) error {
	if !voucherID(r.ActorID) || (!platform && !voucherID(r.TenantID)) {
		return voucherFail("invalid_request")
	}
	var allowed bool
	var e error
	if platform {
		roles := []string{"admin", "finance_operator", "finance_approver"}
		if admin {
			roles = []string{"admin"}
		}
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role=ANY($2::text[]))`, r.ActorID, roles).Scan(&allowed)
	} else {
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN memberships m ON m.user_id=u.id JOIN tenants t ON t.id=m.tenant_id WHERE u.id=$1 AND t.id=$2 AND u.status='active' AND m.status='active' AND t.status='active' AND m.role IN ('owner','billing_viewer'))`, r.ActorID, r.TenantID).Scan(&allowed)
	}
	if e != nil {
		return e
	}
	if !allowed {
		return voucherFail("forbidden")
	}
	return nil
}
func (v *VoucherService) read(ctx context.Context, r VoucherReader, platform, admin bool, fn func(context.Context, pgx.Tx) error) error {
	ctx, c := context.WithTimeout(ctx, 5*time.Second)
	defer c()
	tx, e := v.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return voucherSafeError(e)
	}
	defer voucherRollback(ctx, tx)
	if e = voucherReadAuth(ctx, tx, r, platform, admin); e == nil {
		e = fn(ctx, tx)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return voucherFail("not_found")
	}
	if e != nil {
		return voucherSafeError(e)
	}
	return voucherSafeError(tx.Commit(ctx))
}
func (v *VoucherService) GetVoucherPolicy(ctx context.Context, actor string) (VoucherPolicyView, error) {
	var p VoucherPolicyView
	e := v.read(ctx, VoucherReader{ActorID: actor}, true, true, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT enabled,revision::text FROM voucher_policy WHERE singleton`).Scan(&p.Enabled, &p.Revision)
	})
	p.RuntimeAllowed = v.options.RuntimeAllowed
	p.EffectiveEnabled = p.Enabled && p.RuntimeAllowed
	return p, e
}
func (v *VoucherService) capabilities(ctx context.Context, r VoucherReader, platform bool) (CommerceCapabilitiesView, error) {
	p := CommerceCapabilitiesView{PaymentsMode: "off"}
	e := v.read(ctx, r, platform, false, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT enabled FROM voucher_policy WHERE singleton`).Scan(&p.VouchersEnabled)
	})
	p.VouchersEnabled = p.VouchersEnabled && v.options.RuntimeAllowed
	return p, e
}
func (v *VoucherService) PlatformCommerceCapabilities(ctx context.Context, actor string) (CommerceCapabilitiesView, error) {
	return v.capabilities(ctx, VoucherReader{ActorID: actor}, true)
}
func (v *VoucherService) WalletCommerceCapabilities(ctx context.Context, r VoucherReader) (CommerceCapabilitiesView, error) {
	return v.capabilities(ctx, r, false)
}

const voucherReceiptColumns = `operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at`

func scanVoucherReceipt(row pgx.Row) (r VoucherOperationReceipt, e error) {
	e = row.Scan(&r.OperationID, &r.Kind, &r.Scope, &r.Actor, &r.Status, &r.Reason, &r.ResourceID, &r.Amount, &r.FundingReceiptID, &r.JournalID, &r.CreatedAt)
	r.Scope = strings.TrimPrefix(r.Scope, "tenant:")
	return
}
func (v *VoucherService) getOperation(ctx context.Context, r VoucherReader, operation string, platform bool) (VoucherOperationReceipt, error) {
	var receipt VoucherOperationReceipt
	if !voucherID(operation) {
		return receipt, voucherFail("invalid_request")
	}
	scope := "platform"
	if !platform {
		scope = "tenant:" + r.TenantID
	}
	e := v.read(ctx, r, platform, false, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		receipt, e = scanVoucherReceipt(tx.QueryRow(ctx, `SELECT `+voucherReceiptColumns+` FROM commerce_operation_receipts WHERE operation_id=$1 AND scope=$2`, operation, scope))
		return e
	})
	return receipt, e
}
func (v *VoucherService) GetPlatformCommerceOperation(ctx context.Context, actor, operation string) (VoucherOperationReceipt, error) {
	return v.getOperation(ctx, VoucherReader{ActorID: actor}, operation, true)
}
func (v *VoucherService) GetWalletCommerceOperation(ctx context.Context, r VoucherReader, operation string) (VoucherOperationReceipt, error) {
	return v.getOperation(ctx, r, operation, false)
}

const voucherBatchColumns = `b.id,b.state,b.face_amount,b.count,b.expires_at,b.purpose_ref,b.reason,b.maker,b.approver,b.activated_at,b.terminal_actor,b.terminal_at,b.revision::text,b.redeemed_count::text,(b.count-b.redeemed_count)::text,b.created_at,(b.expires_at<=observed.now),observed.now`
const voucherObserved = `WITH observed AS MATERIALIZED (SELECT clock_timestamp() now) `

func scanVoucherBatch(row pgx.Row) (b VoucherBatchView, e error) {
	e = row.Scan(&b.ID, &b.State, &b.FaceAmount, &b.Count, &b.ExpiresAt, &b.PurposeRef, &b.Reason, &b.Maker, &b.Approver, &b.ActivatedAt, &b.TerminalActor, &b.TerminalAt, &b.Revision, &b.RedeemedCount, &b.RemainingCount, &b.CreatedAt, &b.Expired, &b.ObservedAt)
	return
}
func voucherBatchTx(ctx context.Context, tx pgx.Tx, id string) (VoucherBatchView, error) {
	return scanVoucherBatch(tx.QueryRow(ctx, voucherObserved+`SELECT `+voucherBatchColumns+` FROM voucher_batches b CROSS JOIN observed WHERE b.id=$1`, id))
}
func (v *VoucherService) GetVoucherBatch(ctx context.Context, actor, id string) (VoucherBatchView, error) {
	var b VoucherBatchView
	if !voucherID(id) {
		return b, voucherFail("invalid_request")
	}
	e := v.read(ctx, VoucherReader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error { var e error; b, e = voucherBatchTx(ctx, tx, id); return e })
	return b, e
}

const voucherRevocationColumns = `r.id,r.batch_id,r.expected_revision::text,r.expected_redeemed_count::text,r.reason,r.maker,r.created_at,d.id,d.operation_id,d.status,d.reason,d.approver,d.decided_at`

func scanVoucherRevocation(row pgx.Row) (r VoucherRevocationView, e error) {
	var id, op, status, reason, actor *string
	var at *time.Time
	e = row.Scan(&r.ID, &r.BatchID, &r.ExpectedRevision, &r.ExpectedRedeemedCount, &r.Reason, &r.Maker, &r.CreatedAt, &id, &op, &status, &reason, &actor, &at)
	if e == nil && id != nil {
		if op == nil || status == nil || reason == nil || actor == nil || at == nil {
			return r, voucherFail("commerce_unavailable")
		}
		r.Decision = &VoucherDecisionView{ID: *id, OperationID: *op, Status: *status, Reason: *reason, Approver: *actor, DecidedAt: *at}
	}
	return
}
func (v *VoucherService) GetVoucherRevocation(ctx context.Context, actor, id string) (VoucherRevocationView, error) {
	var r VoucherRevocationView
	if !voucherID(id) {
		return r, voucherFail("invalid_request")
	}
	e := v.read(ctx, VoucherReader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		r, e = scanVoucherRevocation(tx.QueryRow(ctx, `SELECT `+voucherRevocationColumns+` FROM voucher_revocations r LEFT JOIN voucher_revocation_decisions d ON d.proposal_id=r.id WHERE r.id=$1`, id))
		return e
	})
	return r, e
}

type voucherCursor struct {
	V         int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Filter    string `json:"filter"`
}

const voucherTimeLayout = "2006-01-02T15:04:05.000000Z"

func voucherFilter(scope, kind, filter string) string {
	b, _ := json.Marshal([]string{scope, kind, filter})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func decodeVoucherCursor(raw, filter string) (*time.Time, string, error) {
	if raw == "" {
		return nil, "", nil
	}
	if len(raw) > 1024 {
		return nil, "", voucherFail("invalid_cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || len(b) > 768 || !utf8.Valid(b) || base64.RawURLEncoding.EncodeToString(b) != raw {
		return nil, "", voucherFail("invalid_cursor")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, "", voucherFail("invalid_cursor")
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		s, ok := k.(string)
		if e != nil || !ok || seen[s] || (s != "v" && s != "created_at" && s != "id" && s != "filter") {
			return nil, "", voucherFail("invalid_cursor")
		}
		seen[s] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, "", voucherFail("invalid_cursor")
		}
	}
	if t, e = d.Token(); e != nil || t != json.Delim('}') || len(seen) != 4 {
		return nil, "", voucherFail("invalid_cursor")
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, "", voucherFail("invalid_cursor")
	}
	var cur voucherCursor
	if json.Unmarshal(b, &cur) != nil || cur.V != 1 || cur.Filter != filter || !voucherID(cur.ID) {
		return nil, "", voucherFail("invalid_cursor")
	}
	at, e := time.Parse(voucherTimeLayout, cur.CreatedAt)
	if e != nil || at.Format(voucherTimeLayout) != cur.CreatedAt {
		return nil, "", voucherFail("invalid_cursor")
	}
	return &at, cur.ID, nil
}
func encodeVoucherCursor(at time.Time, id, filter string) *string {
	b, _ := json.Marshal(voucherCursor{1, at.UTC().Format(voucherTimeLayout), id, filter})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}
func (v *VoucherService) ListVoucherBatches(ctx context.Context, actor string, page VoucherPageInput) (VoucherPage[VoucherBatchView], error) {
	out := VoucherPage[VoucherBatchView]{Data: []VoucherBatchView{}}
	filter := voucherFilter("platform", "batch", "")
	before, id, e := decodeVoucherCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = v.read(ctx, VoucherReader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, voucherObserved+`SELECT `+voucherBatchColumns+` FROM voucher_batches b CROSS JOIN observed WHERE ($1::timestamptz IS NULL OR b.created_at<$1 OR (b.created_at=$1 AND b.id COLLATE "C"<$2)) ORDER BY b.created_at DESC,b.id COLLATE "C" DESC LIMIT 51`, before, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			b, e := scanVoucherBatch(rows)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, b)
		}
		return rows.Err()
	})
	if e == nil && len(out.Data) > 50 {
		b := out.Data[49]
		out.NextCursor = encodeVoucherCursor(b.CreatedAt, b.ID, filter)
		out.Data = out.Data[:50]
	}
	return out, e
}
func (v *VoucherService) ListVoucherRevocations(ctx context.Context, actor, batch string, page VoucherPageInput) (VoucherPage[VoucherRevocationView], error) {
	out := VoucherPage[VoucherRevocationView]{Data: []VoucherRevocationView{}}
	if !voucherID(batch) {
		return out, voucherFail("invalid_request")
	}
	filter := voucherFilter("platform", "revocation", batch)
	before, id, e := decodeVoucherCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = v.read(ctx, VoucherReader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM voucher_batches WHERE id=$1)`, batch).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return voucherFail("not_found")
		}
		rows, e := tx.Query(ctx, `SELECT `+voucherRevocationColumns+` FROM voucher_revocations r LEFT JOIN voucher_revocation_decisions d ON d.proposal_id=r.id WHERE r.batch_id=$1 AND ($2::timestamptz IS NULL OR r.created_at<$2 OR (r.created_at=$2 AND r.id COLLATE "C"<$3)) ORDER BY r.created_at DESC,r.id COLLATE "C" DESC LIMIT 51`, batch, before, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanVoucherRevocation(rows)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, r)
		}
		return rows.Err()
	})
	if e == nil && len(out.Data) > 50 {
		r := out.Data[49]
		out.NextCursor = encodeVoucherCursor(r.CreatedAt, r.ID, filter)
		out.Data = out.Data[:50]
	}
	return out, e
}
func (v *VoucherService) GetVoucherRedemption(ctx context.Context, r VoucherReader, id string) (VoucherRedemptionView, error) {
	var out VoucherRedemptionView
	if !voucherID(id) {
		return out, voucherFail("invalid_request")
	}
	e := v.read(ctx, r, false, false, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,operation_id,tenant_id,voucher_id,batch_id,actor,amount,'COIN',funding_receipt_id,journal_id,created_at FROM voucher_redemptions WHERE id=$1 AND tenant_id=$2`, id, r.TenantID).Scan(&out.ID, &out.OperationID, &out.TenantID, &out.VoucherID, &out.BatchID, &out.Actor, &out.Amount, &out.Currency, &out.FundingReceiptID, &out.JournalID, &out.CreatedAt)
	})
	return out, e
}
func (v *VoucherService) ListFundingReceipts(ctx context.Context, r VoucherReader, page FundingReceiptPageInput) (VoucherPage[FundingReceiptView], error) {
	out := VoucherPage[FundingReceiptView]{Data: []FundingReceiptView{}}
	if page.SourceKind != "" && page.SourceKind != FundingManualCredit && page.SourceKind != FundingVoucherGrant && page.SourceKind != FundingSandboxCapture && page.SourceKind != FundingWalletOpening {
		return out, voucherFail("invalid_request")
	}
	filter := voucherFilter("tenant:"+r.TenantID, "funding", string(page.SourceKind))
	before, id, e := decodeVoucherCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = v.read(ctx, r, false, false, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at FROM funding_receipts WHERE tenant_id=$1 AND ($2='' OR source_kind=$2) AND ($3::timestamptz IS NULL OR created_at<$3 OR (created_at=$3 AND id COLLATE "C"<$4)) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT 51`, r.TenantID, string(page.SourceKind), before, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var f FundingReceiptView
			if e = rows.Scan(&f.ID, &f.TenantID, &f.SourceKind, &f.SourceID, &f.Amount, &f.Currency, &f.JournalID, &f.CreatedAt); e != nil {
				return e
			}
			out.Data = append(out.Data, f)
		}
		return rows.Err()
	})
	if e == nil && len(out.Data) > 50 {
		f := out.Data[49]
		out.NextCursor = encodeVoucherCursor(f.CreatedAt, f.ID, filter)
		out.Data = out.Data[:50]
	}
	return out, e
}
func (v *VoucherService) GetFundingSummary(ctx context.Context, r VoucherReader) (FundingSummaryView, error) {
	out := FundingSummaryView{Currency: "COIN"}
	e := v.read(ctx, r, false, false, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(sum(amount::numeric),0)::text,coalesce(sum(amount::numeric) FILTER(WHERE source_kind='manual_credit'),0)::text,coalesce(sum(amount::numeric) FILTER(WHERE source_kind='voucher_grant'),0)::text,coalesce(sum(amount::numeric) FILTER(WHERE source_kind='sandbox_capture'),0)::text,coalesce(sum(amount::numeric) FILTER(WHERE source_kind='wallet_opening'),0)::text,clock_timestamp() FROM funding_receipts WHERE tenant_id=$1`, r.TenantID).Scan(&out.TotalAmount, &out.ManualCreditAmount, &out.VoucherGrantAmount, &out.SandboxCaptureAmount, &out.WalletOpeningAmount, &out.AsOf)
	})
	return out, e
}

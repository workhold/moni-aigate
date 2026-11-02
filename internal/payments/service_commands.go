package payments

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type paymentRejected struct{ reason string }

func (e *paymentRejected) Error() string { return e.reason }
func paymentReject(reason string) error  { return &paymentRejected{reason} }

type paymentCommandSpec struct {
	meta         CommandMeta
	kind, target string
	body         any
	replayAuth   func(context.Context, pgx.Tx) error
}
type paymentRunResult struct {
	receipt OperationReceipt
	fresh   bool
	order   *OrderView
}

const paymentReceiptColumns = `operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at`

func scanPaymentReceipt(row pgx.Row) (r OperationReceipt, e error) {
	e = row.Scan(&r.OperationID, &r.Kind, &r.Scope, &r.Actor, &r.Status, &r.Reason, &r.ResourceID, &r.Amount, &r.FundingReceiptID, &r.JournalID, &r.CreatedAt)
	r.Scope = strings.TrimPrefix(r.Scope, "tenant:")
	return
}
func paymentCommandDigest(spec paymentCommandSpec) (string, error) {
	scope := "platform"
	if spec.meta.TenantID != "" {
		scope = "tenant:" + spec.meta.TenantID
	}
	b, e := json.Marshal(struct {
		V      int     `json:"v"`
		Actor  string  `json:"actor"`
		Scope  string  `json:"scope"`
		Kind   string  `json:"kind"`
		Target *string `json:"target_id"`
		Body   any     `json:"body"`
	}{1, spec.meta.ActorID, scope, spec.kind, &spec.target, spec.body})
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type paymentExecution struct {
	ctx       context.Context
	tx        pgx.Tx
	savepoint bool
}

// Call only after all parent locks, current authorization and semantic checks.
// Rolling back effects must preserve every authorization/serialization lock.
func (x *paymentExecution) begin() error {
	if x.savepoint {
		return nil
	}
	_, e := x.tx.Exec(x.ctx, `SAVEPOINT payment_business`)
	if e == nil {
		x.savepoint = true
	}
	return e
}
func (s *Service) run(ctx context.Context, spec paymentCommandSpec, apply func(*paymentExecution) (string, error)) (paymentRunResult, error) {
	var out paymentRunResult
	e := s.transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out = paymentRunResult{}
		digest, e := paymentCommandDigest(spec)
		if e != nil {
			return e
		}
		scope := "platform"
		if spec.meta.TenantID != "" {
			scope = "tenant:" + spec.meta.TenantID
		}
		tag, e := tx.Exec(ctx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest,fingerprint_key_version) VALUES($1,$2,$3,$4,$5,NULL) ON CONFLICT DO NOTHING`, spec.meta.OperationID, spec.meta.ActorID, scope, spec.kind, digest)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var actor, oldScope, kind, oldDigest string
			var version *string
			if e = tx.QueryRow(ctx, `SELECT actor,scope,kind,request_digest,fingerprint_key_version FROM commerce_commands WHERE operation_id=$1`, spec.meta.OperationID).Scan(&actor, &oldScope, &kind, &oldDigest, &version); e != nil {
				return e
			}
			if actor != spec.meta.ActorID || oldScope != scope || kind != spec.kind || version != nil || subtle.ConstantTimeCompare([]byte(digest), []byte(oldDigest)) != 1 {
				return paymentFail("commerce_operation_conflict")
			}
			if e = spec.replayAuth(ctx, tx); e != nil {
				return e
			}
			out.receipt, e = scanPaymentReceipt(tx.QueryRow(ctx, `SELECT `+paymentReceiptColumns+` FROM commerce_operation_receipts WHERE operation_id=$1`, spec.meta.OperationID))
			if e != nil {
				return e
			}
		} else {
			x := &paymentExecution{ctx: ctx, tx: tx}
			resource, e := apply(x)
			status, reason := "accepted", "accepted"
			var rejected *paymentRejected
			var resourceArg any
			if errors.As(e, &rejected) {
				if x.savepoint {
					if _, e = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT payment_business`); e != nil {
						return e
					}
				}
				status, reason = "rejected", rejected.reason
			} else if e != nil {
				return e
			} else {
				resourceArg = resource
				if e = paymentAudit(ctx, tx, spec.meta, spec.kind, spec.meta.OperationID, map[string]any{}); e != nil {
					return e
				}
			}
			if _, e = tx.Exec(ctx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at) SELECT operation_id,kind,scope,actor,$2,$3,$4,NULL,NULL,NULL,created_at FROM commerce_commands WHERE operation_id=$1`, spec.meta.OperationID, status, reason, resourceArg); e != nil {
				return e
			}
			out.receipt, e = scanPaymentReceipt(tx.QueryRow(ctx, `SELECT `+paymentReceiptColumns+` FROM commerce_operation_receipts WHERE operation_id=$1`, spec.meta.OperationID))
			if e != nil {
				return e
			}
			out.fresh = status == "accepted"
		}
		return nil
	})
	if e != nil {
		return paymentRunResult{}, e
	}
	return out, nil
}
func (s *Service) skuGate(ctx context.Context, tx pgx.Tx, id string) error {
	env := s.runtime.environment()
	h := sha256.Sum256([]byte(strings.Join([]string{"moni-payment-lock-v1", "application", env.ID, "sku", id}, "\x00")))
	key := int32(binary.BigEndian.Uint32(h[:4]))
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(782193416,$1::integer)`, key)
	return e
}

type paymentSKUState struct {
	revision, lastVersion int64
	current               *string
	enabled               bool
}

func (s *Service) skuLock(ctx context.Context, tx pgx.Tx, id string) (paymentSKUState, error) {
	var state paymentSKUState
	if e := s.skuGate(ctx, tx, id); e != nil {
		return state, e
	}
	e := tx.QueryRow(ctx, `SELECT revision,last_version,current_version_id,enabled FROM payment_skus WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&state.revision, &state.lastVersion, &state.current, &state.enabled)
	return state, e
}
func (s *Service) skuReplay(ctx context.Context, tx pgx.Tx, actor, id string) error {
	_, e := s.skuLock(ctx, tx, id)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	return paymentAdminLock(ctx, tx, actor)
}
func (s *Service) PublishSKU(ctx context.Context, in PublishSKUInput) (PublishSKUResult, error) {
	if !paymentMeta(in.CommandMeta, false) || !paymentID(in.SKUID) || in.ExpectedRevision < 0 || !paymentText(in.DisplayName, 100) || in.Amount <= 0 {
		return PublishSKUResult{}, paymentFail("invalid_request")
	}
	if e := s.writeAllowed(ctx, false); e != nil {
		return PublishSKUResult{}, e
	}
	spec := paymentCommandSpec{meta: in.CommandMeta, kind: "payment.sku.publish", target: in.SKUID, body: struct {
		Revision string `json:"expected_revision"`
		Name     string `json:"display_name"`
		Amount   any    `json:"amount"`
	}{strconv.FormatInt(in.ExpectedRevision, 10), in.DisplayName, in.Amount}, replayAuth: func(ctx context.Context, tx pgx.Tx) error { return s.skuReplay(ctx, tx, in.ActorID, in.SKUID) }}
	out, e := s.run(ctx, spec, func(x *paymentExecution) (string, error) {
		ctx, tx := x.ctx, x.tx
		state, e := s.skuLock(ctx, tx, in.SKUID)
		missing := errors.Is(e, pgx.ErrNoRows)
		if e != nil && !missing {
			return "", e
		}
		if e = paymentAdminLock(ctx, tx, in.ActorID); e != nil {
			return "", e
		}
		rev := state.revision
		if missing {
			rev = 0
		}
		if rev != in.ExpectedRevision {
			return "", paymentReject("payment_revision_conflict")
		}
		if rev == math.MaxInt64 || state.lastVersion == math.MaxInt64 {
			return "", paymentReject("payment_state_conflict")
		}
		if e = x.begin(); e != nil {
			return "", e
		}
		if missing {
			if _, e = tx.Exec(ctx, `INSERT INTO payment_skus(id,created_by) VALUES($1,$2)`, in.SKUID, in.ActorID); e != nil {
				return "", e
			}
		}
		id := uuid.NewString()
		env := s.runtime.environment()
		if _, e = tx.Exec(ctx, `INSERT INTO payment_sku_versions(id,sku_id,version,display_name,amount,wallet_credit_amount,environment_id,merchant_id,payee_label,publish_operation_id,created_by) VALUES($1,$2,1,$3,$4,$4,$5,$6,$7,$8,$9)`, id, in.SKUID, in.DisplayName, int64(in.Amount), env.ID, env.MerchantID, env.PayeeLabel, in.OperationID, in.ActorID); e != nil {
			return "", e
		}
		_, e = tx.Exec(ctx, `INSERT INTO payment_sku_changes(operation_id,sku_id,kind,before_revision,after_revision,published_version_id,before_enabled,after_enabled) VALUES($1,$2,'payment.sku.publish',$3,$4,$5,false,false)`, in.OperationID, in.SKUID, rev, rev+1, id)
		return id, e
	})
	return PublishSKUResult{Receipt: out.receipt, Created: out.fresh}, e
}
func (s *Service) ActivateSKU(ctx context.Context, in ActivateSKUInput) (OperationReceipt, error) {
	if !paymentMeta(in.CommandMeta, false) || !paymentID(in.SKUID) || in.ExpectedRevision < 1 || (in.VersionID != nil && !paymentID(*in.VersionID)) {
		return OperationReceipt{}, paymentFail("invalid_request")
	}
	if e := s.writeAllowed(ctx, false); e != nil {
		return OperationReceipt{}, e
	}
	spec := paymentCommandSpec{meta: in.CommandMeta, kind: "payment.sku.activate", target: in.SKUID, body: struct {
		Revision string  `json:"expected_revision"`
		Version  *string `json:"version_id"`
		Enabled  bool    `json:"enabled"`
	}{strconv.FormatInt(in.ExpectedRevision, 10), in.VersionID, in.Enabled}, replayAuth: func(ctx context.Context, tx pgx.Tx) error { return s.skuReplay(ctx, tx, in.ActorID, in.SKUID) }}
	out, e := s.run(ctx, spec, func(x *paymentExecution) (string, error) {
		ctx, tx := x.ctx, x.tx
		state, e := s.skuLock(ctx, tx, in.SKUID)
		if errors.Is(e, pgx.ErrNoRows) {
			return "", paymentFail("not_found")
		}
		if e != nil {
			return "", e
		}
		if e = paymentAdminLock(ctx, tx, in.ActorID); e != nil {
			return "", e
		}
		if state.revision != in.ExpectedRevision {
			return "", paymentReject("payment_revision_conflict")
		}
		same := state.enabled == in.Enabled && ((state.current == nil && in.VersionID == nil) || (state.current != nil && in.VersionID != nil && *state.current == *in.VersionID))
		if state.lastVersion == 0 || in.Enabled && in.VersionID == nil || !same && state.revision == math.MaxInt64 {
			return "", paymentReject("payment_state_conflict")
		}
		if in.VersionID != nil {
			var ok bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_sku_versions WHERE id=$1 AND sku_id=$2)`, *in.VersionID, in.SKUID).Scan(&ok); e != nil {
				return "", e
			}
			if !ok {
				return "", paymentReject("payment_state_conflict")
			}
		}
		if e = x.begin(); e != nil {
			return "", e
		}
		_, e = tx.Exec(ctx, `INSERT INTO payment_sku_changes(operation_id,sku_id,kind,before_revision,after_revision,before_enabled,after_enabled,after_version_id) VALUES($1,$2,'payment.sku.activate',$3,$3,false,$4,$5)`, in.OperationID, in.SKUID, state.revision, in.Enabled, in.VersionID)
		return in.SKUID, e
	})
	return out.receipt, e
}
func paymentSaleLock(ctx context.Context, tx pgx.Tx, version string) (bool, error) {
	var available bool
	e := tx.QueryRow(ctx, `SELECT s.enabled AND s.current_version_id=v.id FROM payment_sku_versions v JOIN payment_skus s ON s.id=v.sku_id WHERE v.id=$1 FOR SHARE OF s`, version).Scan(&available)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	return available, e
}
func paymentPolicyLock(ctx context.Context, tx pgx.Tx) (bool, error) {
	var enabled bool
	e := tx.QueryRow(ctx, `SELECT enabled FROM payment_policy WHERE singleton FOR SHARE`).Scan(&enabled)
	return enabled, e
}
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (OrderResult, error) {
	if !paymentMeta(in.CommandMeta, true) || !paymentID(in.SKUVersionID) {
		return OrderResult{}, paymentFail("invalid_request")
	}
	if e := s.writeAllowed(ctx, true); e != nil {
		return OrderResult{}, e
	}
	auth := func(ctx context.Context, tx pgx.Tx) error {
		if _, e := paymentSaleLock(ctx, tx, in.SKUVersionID); e != nil {
			return e
		}
		if _, e := paymentPolicyLock(ctx, tx); e != nil {
			return e
		}
		return paymentOwnerLock(ctx, tx, in.CommandMeta)
	}
	spec := paymentCommandSpec{meta: in.CommandMeta, kind: "payment.order.create", target: in.SKUVersionID, body: struct {
		Version string `json:"sku_version_id"`
	}{in.SKUVersionID}, replayAuth: auth}
	out, e := s.run(ctx, spec, func(x *paymentExecution) (string, error) {
		ctx, tx := x.ctx, x.tx
		available, e := paymentSaleLock(ctx, tx, in.SKUVersionID)
		if e != nil {
			return "", e
		}
		enabled, e := paymentPolicyLock(ctx, tx)
		if e != nil {
			return "", e
		}
		if e = paymentOwnerLock(ctx, tx, in.CommandMeta); e != nil {
			return "", e
		}
		if !enabled {
			return "", paymentReject("payments_disabled")
		}
		if !available {
			return "", paymentReject("payment_sku_unavailable")
		}
		if e = x.begin(); e != nil {
			return "", e
		}
		var o frozenPaymentOrder
		e = tx.QueryRow(ctx, `INSERT INTO payment_orders(id,tenant_id,creator,sku_version_id) VALUES($1,$2,$3,$4) RETURNING id,environment_id,merchant_id,merchant_order_id,amount::text,currency,created_at,expires_at`, in.OperationID, in.TenantID, in.ActorID, in.SKUVersionID).Scan(&o.id, &o.environment, &o.merchant, &o.merchantOrder, &o.amount, &o.currency, &o.created, &o.expires)
		if e != nil {
			return "", e
		}
		if e = paymentMutationOutbox(ctx, tx, o, "checkout"); e != nil {
			return "", e
		}
		return in.OperationID, nil
	})
	if e != nil {
		return OrderResult{}, e
	}
	var order *OrderView
	if out.receipt.Status == "accepted" {
		o, readErr := s.GetWalletOrder(ctx, Reader{ActorID: in.ActorID, TenantID: in.TenantID}, in.OperationID)
		if readErr != nil {
			// The command is already committed. A safe-view denial/failure is
			// not a precommit 4xx that permits discarding the operation journal.
			return OrderResult{}, ErrPaymentsOutcomeUnknown
		}
		order = &o
	}
	return OrderResult{Receipt: out.receipt, Order: order, Created: out.fresh}, nil
}
func paymentOrderLock(ctx context.Context, tx pgx.Tx, id, tenant string) (frozenPaymentOrder, error) {
	var o frozenPaymentOrder
	e := tx.QueryRow(ctx, `SELECT id,environment_id,merchant_id,merchant_order_id,amount::text,currency,created_at,expires_at FROM payment_orders WHERE id=$1 AND tenant_id=$2 FOR NO KEY UPDATE`, id, tenant).Scan(&o.id, &o.environment, &o.merchant, &o.merchantOrder, &o.amount, &o.currency, &o.created, &o.expires)
	if errors.Is(e, pgx.ErrNoRows) {
		return o, paymentFail("not_found")
	}
	return o, e
}
func (s *Service) CancelOrder(ctx context.Context, in CancelOrderInput) (OperationReceipt, error) {
	if !paymentMeta(in.CommandMeta, true) || !paymentID(in.OrderID) {
		return OperationReceipt{}, paymentFail("invalid_request")
	}
	if e := s.writeAllowed(ctx, false); e != nil {
		return OperationReceipt{}, e
	}
	auth := func(ctx context.Context, tx pgx.Tx) error {
		if _, e := paymentOrderLock(ctx, tx, in.OrderID, in.TenantID); e != nil {
			return e
		}
		return paymentOwnerLock(ctx, tx, in.CommandMeta)
	}
	spec := paymentCommandSpec{meta: in.CommandMeta, kind: "payment.order.cancel", target: in.OrderID, body: struct{}{}, replayAuth: auth}
	out, e := s.run(ctx, spec, func(x *paymentExecution) (string, error) {
		ctx, tx := x.ctx, x.tx
		o, e := paymentOrderLock(ctx, tx, in.OrderID, in.TenantID)
		if e != nil {
			return "", e
		}
		if e = paymentOwnerLock(ctx, tx, in.CommandMeta); e != nil {
			return "", e
		}
		var conflict bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=$1) OR EXISTS(SELECT 1 FROM payment_captures WHERE order_id=$1) OR EXISTS(SELECT 1 FROM payment_provider_revisions WHERE order_id=$1 AND kind='closed')`, in.OrderID).Scan(&conflict); e != nil {
			return "", e
		}
		if conflict {
			return "", paymentReject("payment_state_conflict")
		}
		if e = x.begin(); e != nil {
			return "", e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO payment_cancel_intents(order_id,operation_id,actor) VALUES($1,$2,$3)`, in.OrderID, in.OperationID, in.ActorID); e != nil {
			return "", e
		}
		if e = paymentMutationOutbox(ctx, tx, o, "cancel"); e != nil {
			return "", e
		}
		return in.OrderID, nil
	})
	return out.receipt, e
}
func (s *Service) UpdatePolicy(ctx context.Context, in UpdatePolicyInput) (PolicyView, error) {
	if !paymentID(in.ActorID) || in.ExpectedRevision < 1 {
		return PolicyView{}, paymentFail("invalid_request")
	}
	if e := s.runtime.check(ctx); e != nil {
		return PolicyView{}, e
	}
	if s.runtime.recovery() {
		return PolicyView{}, ErrPaymentsRecovery
	}
	if in.Enabled && string(s.runtime.mode()) != "sandbox" {
		return PolicyView{}, paymentFail("payments_runtime_disabled")
	}
	var out PolicyView
	e := s.transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out = PolicyView{}
		if e := paymentAdminLock(ctx, tx, in.ActorID); e != nil {
			return e
		}
		var enabled bool
		var rev int64
		if e := tx.QueryRow(ctx, `SELECT enabled,revision FROM payment_policy WHERE singleton FOR UPDATE`).Scan(&enabled, &rev); e != nil {
			return e
		}
		if rev != in.ExpectedRevision {
			return paymentFail("payment_policy_conflict")
		}
		if enabled != in.Enabled {
			if rev == math.MaxInt64 {
				return paymentFail("payment_policy_conflict")
			}
			if _, e := tx.Exec(ctx, `UPDATE payment_policy SET enabled=$1 WHERE singleton`, in.Enabled); e != nil {
				return e
			}
			if e := paymentAudit(ctx, tx, CommandMeta{ActorID: in.ActorID, RequestID: in.RequestID}, "payment.policy.update", "payment_policy", map[string]any{"before": enabled, "after": in.Enabled}); e != nil {
				return e
			}
		}
		return s.policyTx(ctx, tx, &out)
	})
	if e != nil {
		return PolicyView{}, e
	}
	return out, nil
}

// Outbound mutation bytes come only from the INSERT/locked immutable order.
func paymentMutationOutbox(ctx context.Context, tx pgx.Tx, o frozenPaymentOrder, kind string) error {
	command := uuid.NewString()
	var b []byte
	var e error
	if kind == "checkout" {
		b, e = p.CanonicalCheckout(p.CheckoutRequest{SchemaVersion: 1, EnvironmentID: o.environment, MerchantID: o.merchant, CommandID: command, MerchantOrderID: o.merchantOrder, Amount: o.amount, Currency: o.currency, CreatedAt: o.created.UTC().Format(paymentTimeLayout), ExpiresAt: o.expires.UTC().Format(paymentTimeLayout)})
	} else {
		b, e = p.CanonicalCancel(p.CancelRequest{SchemaVersion: 1, EnvironmentID: o.environment, MerchantID: o.merchant, CommandID: command, MerchantOrderID: o.merchantOrder})
	}
	if e != nil {
		return ErrPaymentsIntegrity
	}
	_, e = tx.Exec(ctx, `INSERT INTO payment_command_outbox(id,order_id,kind,external_command_id,canonical_parameters) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), o.id, kind, command, b)
	return e
}

package billing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const sandboxFundingActor = "payment-sandbox-worker"

type sandboxFundingSource struct {
	OrderID, TenantID string
	Amount            Money
	Receipt           *FundingReceipt
}

// A locator conveys no verification authority. Every call rebuilds the stored
// source graph through invoker proofs, in the caller's transaction. In
// particular, it never obtains an order/identity/tenant lock after accounts.
func readSandboxFundingSource(ctx context.Context, tx pgx.Tx, source FundingSourceRef, scope FundingScope, at time.Time) (sandboxFundingSource, error) {
	var out sandboxFundingSource
	if tx == nil || !voucherID(source.ID) || !voucherID(scope.TenantID) || scope.ActorID != sandboxFundingActor || scope.CommandID != "" {
		return out, fail("invalid_funding_source", "funding source unavailable")
	}
	var connectionValid bool
	if e := tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation')='read committed' AND EXISTS(
 SELECT 1 FROM payment_sandbox_environment m WHERE m.singleton
 AND current_user=m.worker_role AND session_user=m.worker_role
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_roles r WHERE r.rolname=m.worker_role AND r.rolcanlogin
  AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls))
 AND m.app_role<>m.worker_role AND m.projector_role<>m.worker_role AND m.app_role<>m.projector_role
 AND m.database_oid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database())
 AND m.schema_oid=current_schema()::regnamespace::oid)`).Scan(&connectionValid); e != nil {
		return out, e
	}
	if !connectionValid {
		return out, fail("funding_integrity", "funding source integrity failed")
	}
	var valid, claimed, hasJournal bool
	e := tx.QueryRow(ctx, `SELECT c.order_id,c.tenant_id,c.wallet_credit_amount,
 payment_capture_source_valid(c.id) AND EXISTS(SELECT 1 FROM payment_effects x
  WHERE x.capture_id=c.id AND (x.identity_id,x.environment_id,x.merchant_id,x.provider_transaction_id,x.effect_kind,x.effect_id)
   =(c.identity_id,c.environment_id,c.merchant_id,c.provider_transaction_id,'capture','capture')),
 EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=c.verified_evidence_id
  AND p.state='leased' AND p.generation=c.processing_generation AND p.lease_until>$2)
 AND EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_evidence_processing p ON p.evidence_id=i.evidence_id
  WHERE i.id=c.identity_id AND p.generation=i.processing_generation
   AND (p.state IN ('processed','processing_review') OR
    (p.state='leased' AND i.evidence_id=c.verified_evidence_id AND i.processing_generation=c.processing_generation AND p.lease_until>$2))),
 EXISTS(SELECT 1 FROM journals j WHERE j.operation_id=c.id)
 FROM payment_captures c WHERE c.id=$1`, source.ID, at).Scan(&out.OrderID, &out.TenantID, &out.Amount, &valid, &claimed, &hasJournal)
	if errors.Is(e, pgx.ErrNoRows) {
		return sandboxFundingSource{}, fail("invalid_funding_source", "funding source unavailable")
	}
	if e != nil {
		return out, e
	}
	if !valid || out.TenantID != scope.TenantID || out.Amount <= 0 || !voucherID(out.OrderID) {
		return sandboxFundingSource{}, fail("funding_integrity", "funding source integrity failed")
	}
	receipt, e := fundingReceiptRow(ctx, tx, FundingSandboxCapture, source.ID)
	if e == nil {
		var complete bool
		if e = tx.QueryRow(ctx, `SELECT funding_journal_valid($1)`, receipt.JournalID).Scan(&complete); e != nil {
			return out, e
		}
		if !complete || receipt.TenantID != out.TenantID || receipt.Amount != out.Amount || receipt.Currency != "COIN" {
			return sandboxFundingSource{}, fail("funding_integrity", "funding source integrity failed")
		}
		out.Receipt = &receipt
		return out, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	if !claimed || hasJournal {
		return sandboxFundingSource{}, fail("funding_integrity", "funding source integrity failed")
	}
	return out, nil
}

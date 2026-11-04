package billing

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type FundingCheck struct {
	Balanced                                              bool
	MissingReceipts, OrphanReceipts, SourceMismatches     int64
	JournalMismatches, OutboxMismatches                   int64
	RedemptionMismatches, BatchCountMismatches            int64
	ClearingMismatches                                    int64
	PaymentIdentityMismatches, PaymentSelectionMismatches int64
}

func (s *Store) VerifyFunding(ctx context.Context, tenantID string) (FundingCheck, error) {
	var result FundingCheck
	if tenantID != "" && !voucherID(tenantID) {
		return result, fail("invalid_tenant", "invalid scope")
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return result, e
	}
	defer tx.Rollback(ctx)
	result, e = verifyFundingTx(ctx, tx, tenantID)
	if e != nil {
		return result, e
	}
	if e = tx.Commit(ctx); e != nil {
		return FundingCheck{}, e
	}
	return result, nil
}

// One statement/snapshot checks every direction, including facts with no
// corresponding receipt. Tenant checks compare their own clearing lines,
// never their subtotal against a shared platform account's complete balance.
func verifyFundingTx(ctx context.Context, tx pgx.Tx, tenant string) (FundingCheck, error) {
	var c FundingCheck
	e := tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM credit_orders c WHERE ($1='' OR c.tenant_id=$1) AND c.state='approved' AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id))+
 (SELECT count(*) FROM voucher_redemptions r WHERE ($1='' OR r.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.id=r.funding_receipt_id AND f.source_kind='voucher_grant' AND f.source_id=r.voucher_id))+
 (SELECT count(*) FROM payment_captures c LEFT JOIN payment_orders o ON o.id=c.order_id WHERE ($1='' OR o.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='sandbox_capture' AND f.source_id=c.id))+
 (SELECT count(*) FROM wallet_openings o WHERE ($1='' OR o.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='wallet_opening' AND f.source_id=o.id)),
 (SELECT count(*) FROM funding_receipts f WHERE ($1='' OR f.tenant_id=$1) AND
  ((f.source_kind='manual_credit' AND NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.id=f.source_id AND c.state='approved')) OR
   (f.source_kind='voucher_grant' AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.voucher_id=f.source_id AND r.funding_receipt_id=f.id)) OR
   (f.source_kind='sandbox_capture' AND NOT EXISTS(SELECT 1 FROM payment_captures c JOIN payment_orders o ON o.id=c.order_id WHERE c.id=f.source_id AND c.tenant_id=f.tenant_id AND o.tenant_id=f.tenant_id)) OR
   (f.source_kind='wallet_opening' AND NOT EXISTS(SELECT 1 FROM wallet_openings o WHERE o.id=f.source_id AND o.tenant_id=f.tenant_id)) OR
   f.source_kind NOT IN ('manual_credit','voucher_grant','sandbox_capture','wallet_opening'))),
 (SELECT count(*) FROM credit_orders c WHERE ($1='' OR c.tenant_id=$1) AND
  ((c.state='approved' AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id AND f.tenant_id=c.tenant_id AND f.amount=c.amount AND f.journal_id=c.journal_id)) OR
   (c.state='pending' AND (c.journal_id IS NOT NULL OR c.approver IS NOT NULL OR EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id)))))+
 (SELECT count(*) FROM wallet_openings o WHERE ($1='' OR o.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='wallet_opening' AND f.source_id=o.id AND f.tenant_id=o.tenant_id AND f.amount=o.amount AND wallet_opening_journal_valid(f.journal_id)))+
 (SELECT count(*) FROM payment_captures c LEFT JOIN payment_orders o ON o.id=c.order_id WHERE ($1='' OR o.tenant_id=$1)
  AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='sandbox_capture' AND f.source_id=c.id AND payment_sandbox_journal_valid(f.journal_id)))+
 (SELECT count(*) FROM payment_effects x LEFT JOIN payment_captures c ON c.id=x.capture_id LEFT JOIN payment_orders o ON o.id=c.order_id
  WHERE ($1='' OR o.tenant_id=$1) AND (c.id IS NULL OR NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='sandbox_capture' AND f.source_id=c.id AND payment_sandbox_journal_valid(f.journal_id))))+
 (SELECT count(*) FROM audit_events a LEFT JOIN payment_captures c ON c.id=a.target LEFT JOIN payment_orders o ON o.id=c.order_id
  WHERE a.action='payment.capture.credited' AND ($1='' OR o.tenant_id=$1) AND (c.id IS NULL OR NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='sandbox_capture' AND f.source_id=c.id AND payment_sandbox_journal_valid(f.journal_id)))),
 (SELECT count(*) FROM journals j WHERE ($1='' OR j.tenant_id=$1) AND j.kind IN ('credit','voucher_grant','sandbox_capture','wallet_opening') AND NOT funding_journal_valid(j.id))+
 (SELECT count(*) FROM funding_receipts f JOIN journals j ON j.id=f.journal_id WHERE ($1='' OR f.tenant_id=$1) AND j.kind NOT IN ('credit','voucher_grant','sandbox_capture','wallet_opening')),
 (SELECT count(*) FROM projection_outbox o WHERE ($1='' OR o.tenant_id=$1) AND o.kind='credited' AND NOT EXISTS(
  SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id WHERE j.operation_id=o.operation_id AND j.tenant_id=o.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text AND funding_journal_valid(j.id)))+
 (SELECT count(*) FROM funding_receipts f JOIN journals j ON j.id=f.journal_id WHERE ($1='' OR f.tenant_id=$1) AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')<>1),
 (SELECT count(*) FROM voucher_redemptions r WHERE ($1='' OR r.tenant_id=$1) AND NOT funding_journal_valid(r.journal_id))+
 (SELECT count(*) FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.redeem' AND terminal.status='accepted' AND ($1='' OR terminal.scope='tenant:'||$1)
  AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.operation_id=terminal.operation_id AND r.id=terminal.resource_id AND r.actor=terminal.actor AND 'tenant:'||r.tenant_id=terminal.scope AND r.amount=terminal.amount AND r.funding_receipt_id=terminal.funding_receipt_id AND r.journal_id=terminal.journal_id)),
 (SELECT count(*) FROM voucher_batches b WHERE ($1='' OR EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.batch_id=b.id AND r.tenant_id=$1)) AND
  (b.count<>(SELECT count(*) FROM vouchers v WHERE v.batch_id=b.id) OR b.redeemed_count<>(SELECT count(*) FROM voucher_redemptions r WHERE r.batch_id=b.id))),
 (SELECT count(*) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id JOIN journals j ON j.id=l.journal_id
  WHERE ($1='' OR j.tenant_id=$1) AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING') AND NOT funding_journal_valid(j.id))+
 (SELECT count(*) FROM funding_receipts f JOIN journals j ON j.id=f.journal_id WHERE ($1='' OR f.tenant_id=$1) AND NOT EXISTS(
  SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE f.source_kind WHEN 'manual_credit' THEN 'FUNDING_CLEARING' WHEN 'voucher_grant' THEN 'OPERATING_GRANT_CLEARING' WHEN 'sandbox_capture' THEN 'SANDBOX_FUNDING_CLEARING' WHEN 'wallet_opening' THEN 'WALLET_OPENING_CLEARING' END AND l.signed_delta=-f.amount)),
 (SELECT count(*) FROM payment_transaction_identities i LEFT JOIN payment_orders o ON o.id=i.order_id WHERE ($1='' OR o.tenant_id=$1) AND
  (NOT payment_identity_valid(i.id)
   OR NOT EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=i.evidence_id AND p.generation=i.processing_generation AND p.state IN ('processed','processing_review'))
   OR (NOT EXISTS(SELECT 1 FROM payment_order_capture_selections s WHERE s.identity_id=i.id AND s.order_id=i.order_id)
    AND NOT EXISTS(SELECT 1 FROM payment_review_facts v JOIN payment_verified_evidence e ON e.id=v.evidence_id
     JOIN payment_order_capture_selections s ON s.order_id=i.order_id AND s.identity_id<>i.id
     WHERE v.identity_id=i.id AND v.order_id=i.order_id AND v.reason='duplicate_order_capture' AND payment_capture_evidence_valid(e.id)
      AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
       =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision))))),
 (SELECT count(*) FROM payment_order_capture_selections s LEFT JOIN payment_orders o ON o.id=s.order_id
  LEFT JOIN payment_transaction_identities i ON i.id=s.identity_id LEFT JOIN payment_verified_evidence e ON e.id=s.first_evidence_id
  WHERE ($1='' OR o.tenant_id=$1) AND
   (i.id IS NULL OR i.order_id IS DISTINCT FROM s.order_id OR NOT payment_identity_valid(i.id) OR NOT payment_capture_evidence_valid(e.id)
    OR (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
     IS DISTINCT FROM (i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)
    OR (EXISTS(SELECT 1 FROM payment_captures c WHERE c.order_id=s.order_id)
     AND NOT EXISTS(SELECT 1 FROM payment_captures c JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id
      WHERE c.order_id=s.order_id AND c.identity_id=s.identity_id AND payment_sandbox_journal_valid(f.journal_id)))
    OR (NOT EXISTS(SELECT 1 FROM payment_captures c WHERE c.order_id=s.order_id)
     AND NOT EXISTS(SELECT 1 FROM payment_review_facts v JOIN payment_verified_evidence ve ON ve.id=v.evidence_id
      WHERE v.selection_order_id=s.order_id AND v.order_id=s.order_id AND v.identity_id=s.identity_id AND v.reason='funding_amount_overflow'
       AND payment_capture_evidence_valid(ve.id)
       AND (ve.environment_id,ve.merchant_id,ve.order_id,ve.provider_transaction_id,ve.amount,ve.currency,ve.happened_at,ve.provider_order_revision)
        =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)))))`, tenant).Scan(&c.MissingReceipts, &c.OrphanReceipts, &c.SourceMismatches, &c.JournalMismatches, &c.OutboxMismatches, &c.RedemptionMismatches, &c.BatchCountMismatches, &c.ClearingMismatches, &c.PaymentIdentityMismatches, &c.PaymentSelectionMismatches)
	c.Balanced = e == nil && c.MissingReceipts == 0 && c.OrphanReceipts == 0 && c.SourceMismatches == 0 && c.JournalMismatches == 0 && c.OutboxMismatches == 0 && c.RedemptionMismatches == 0 && c.BatchCountMismatches == 0 && c.ClearingMismatches == 0 && c.PaymentIdentityMismatches == 0 && c.PaymentSelectionMismatches == 0
	return c, e
}

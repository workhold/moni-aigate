package funding019

// The historical 017 predicate is copied into a CTE rather than invoking any
// live function or the current billing verifier. Every base relation receives
// the explicitly quoted fixture schema; tenant remains a bound value.
const fundingSQL = `WITH valid_funding AS (
 SELECT j.id FROM @journals j JOIN @funding_receipts f ON f.journal_id=j.id
 LEFT JOIN @credit_orders c ON f.source_kind='manual_credit' AND c.id=f.source_id
 LEFT JOIN @vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 LEFT JOIN @voucher_batches b ON b.id=v.batch_id
 LEFT JOIN @voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 LEFT JOIN @commerce_commands cmd ON cmd.operation_id=r.operation_id
 LEFT JOIN @commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.tenant_id=f.tenant_id AND j.currency='CNY' AND f.currency='CNY' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.state='approved' AND c.journal_id=j.id AND c.tenant_id=f.tenant_id AND c.amount=f.amount AND c.approver<>c.requester AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND j.actor=c.approver)
 OR (f.source_kind='voucher_grant' AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
  AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
  AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
  AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
  AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id))
 AND (SELECT count(*) FROM @journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM @journal_lines l JOIN @ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM @journal_lines l JOIN @ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM @projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM @projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text)
)
SELECT
 (SELECT count(*) FROM @credit_orders c WHERE ($1='' OR c.tenant_id=$1) AND c.state='approved' AND NOT EXISTS(SELECT 1 FROM @funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id))+
 (SELECT count(*) FROM @voucher_redemptions r WHERE ($1='' OR r.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM @funding_receipts f WHERE f.id=r.funding_receipt_id AND f.source_kind='voucher_grant' AND f.source_id=r.voucher_id)),
 (SELECT count(*) FROM @funding_receipts f WHERE ($1='' OR f.tenant_id=$1) AND
  ((f.source_kind='manual_credit' AND NOT EXISTS(SELECT 1 FROM @credit_orders c WHERE c.id=f.source_id AND c.state='approved')) OR
   (f.source_kind='voucher_grant' AND NOT EXISTS(SELECT 1 FROM @voucher_redemptions r WHERE r.voucher_id=f.source_id AND r.funding_receipt_id=f.id)) OR
   f.source_kind NOT IN ('manual_credit','voucher_grant'))),
 (SELECT count(*) FROM @credit_orders c WHERE ($1='' OR c.tenant_id=$1) AND
  ((c.state='approved' AND NOT EXISTS(SELECT 1 FROM @funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id AND f.tenant_id=c.tenant_id AND f.amount=c.amount AND f.journal_id=c.journal_id)) OR
   (c.state='pending' AND (c.journal_id IS NOT NULL OR c.approver IS NOT NULL OR EXISTS(SELECT 1 FROM @funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id))))),
 (SELECT count(*) FROM @journals j WHERE ($1='' OR j.tenant_id=$1) AND j.kind IN ('credit','voucher_grant') AND NOT EXISTS(SELECT 1 FROM valid_funding v WHERE v.id=j.id))+
 (SELECT count(*) FROM @funding_receipts f JOIN @journals j ON j.id=f.journal_id WHERE ($1='' OR f.tenant_id=$1) AND j.kind NOT IN ('credit','voucher_grant')),
 (SELECT count(*) FROM @projection_outbox o WHERE ($1='' OR o.tenant_id=$1) AND o.kind='credited' AND NOT EXISTS(
  SELECT 1 FROM @journals j JOIN @funding_receipts f ON f.journal_id=j.id JOIN valid_funding v ON v.id=j.id WHERE j.operation_id=o.operation_id AND j.tenant_id=o.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))+
 (SELECT count(*) FROM @funding_receipts f JOIN @journals j ON j.id=f.journal_id WHERE ($1='' OR f.tenant_id=$1) AND (SELECT count(*) FROM @projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')<>1),
 (SELECT count(*) FROM @voucher_redemptions r WHERE ($1='' OR r.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM valid_funding v WHERE v.id=r.journal_id))+
 (SELECT count(*) FROM @commerce_operation_receipts terminal WHERE terminal.kind='voucher.redeem' AND terminal.status='accepted' AND ($1='' OR terminal.scope='tenant:'||$1)
  AND NOT EXISTS(SELECT 1 FROM @voucher_redemptions r WHERE r.operation_id=terminal.operation_id AND r.id=terminal.resource_id AND r.actor=terminal.actor AND 'tenant:'||r.tenant_id=terminal.scope AND r.amount=terminal.amount AND r.funding_receipt_id=terminal.funding_receipt_id AND r.journal_id=terminal.journal_id)),
 (SELECT count(*) FROM @voucher_batches b WHERE ($1='' OR EXISTS(SELECT 1 FROM @voucher_redemptions r WHERE r.batch_id=b.id AND r.tenant_id=$1)) AND
  (b.count<>(SELECT count(*) FROM @vouchers v WHERE v.batch_id=b.id) OR b.redeemed_count<>(SELECT count(*) FROM @voucher_redemptions r WHERE r.batch_id=b.id))),
 (SELECT count(*) FROM @journal_lines l JOIN @ledger_accounts a ON a.id=l.account_id JOIN @journals j ON j.id=l.journal_id
  WHERE ($1='' OR j.tenant_id=$1) AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING') AND NOT EXISTS(SELECT 1 FROM valid_funding v WHERE v.id=j.id))+
 (SELECT count(*) FROM @funding_receipts f JOIN @journals j ON j.id=f.journal_id WHERE ($1='' OR f.tenant_id=$1) AND NOT EXISTS(
  SELECT 1 FROM @journal_lines l JOIN @ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)),
 (SELECT count(*) FROM @ledger_accounts a WHERE ($1='' OR a.tenant_id=$1) AND a.balance::numeric<>coalesce((SELECT sum(l.signed_delta::numeric) FROM @journal_lines l WHERE l.account_id=a.id),0)),
 (SELECT count(*) FROM @journals j WHERE ($1='' OR j.tenant_id=$1) AND ((SELECT count(*) FROM @journal_lines l WHERE l.journal_id=j.id)<2 OR (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM @journal_lines l WHERE l.journal_id=j.id)<>0))`

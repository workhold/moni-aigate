-- 034.1 corrective append migration: restore immutable command and audit bytes.
-- current_004's pre_coin_input_fingerprint preserves the exact original hash,
-- allowing this transaction to restore the original CNY command/result values.
-- Readers now project COIN at query time instead of rewriting immutable facts.
ALTER TABLE pricing_operations DISABLE TRIGGER immutable_pricing_operation;
ALTER TABLE audit_events DISABLE TRIGGER immutable_audit;
UPDATE pricing_operations
SET input_fingerprint=pre_coin_input_fingerprint,
 safe_input=jsonb_set(safe_input,'{currency}','"CNY"'::jsonb,false),
 result_snapshot=jsonb_set(result_snapshot,'{currency}','"CNY"'::jsonb,false)
WHERE pre_coin_input_fingerprint IS NOT NULL;
UPDATE audit_events a SET sanitized_diff=jsonb_set(a.sanitized_diff,'{after}',p.result_snapshot,false)
FROM pricing_operations p
WHERE p.operation_id=a.sanitized_diff->>'operation_id'
 AND p.pre_coin_input_fingerprint IS NOT NULL
 AND a.action='pricing.price.publish';
ALTER TABLE pricing_operations DROP COLUMN pre_coin_input_fingerprint;
ALTER TABLE pricing_operations ENABLE TRIGGER immutable_pricing_operation;
ALTER TABLE audit_events ENABLE TRIGGER immutable_audit;

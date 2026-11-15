-- 034.3b append-only coin-rate versions and receipt buy-rate linkage.
-- No rate-adjustment API, cash-refund state machine, or runtime exchange-rate path.

CREATE TABLE coin_rate_versions (
 id text PRIMARY KEY,
 fiat_currency text NOT NULL CHECK(fiat_currency IN ('CNY','USD')),
 rate_num bigint NOT NULL CHECK(rate_num>0),
 rate_den bigint NOT NULL CHECK(rate_den BETWEEN 1 AND 1000000),
 effective_at timestamptz NOT NULL,
 reason text NOT NULL,
 maker text REFERENCES users(id),
 approver text REFERENCES users(id),
 single_operator boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(fiat_currency,effective_at),
 UNIQUE(id,fiat_currency),
 CHECK(
  (fiat_currency='CNY' AND rate_num=1 AND rate_den=1 AND effective_at='1970-01-01T00:00:00Z'
   AND maker IS NULL AND approver IS NULL AND NOT single_operator)
  OR (maker IS NOT NULL AND approver IS NOT NULL
   AND (single_operator OR approver<>maker)
   AND effective_at>=created_at)));
INSERT INTO coin_rate_versions(id,fiat_currency,rate_num,rate_den,effective_at,reason,maker,approver,single_operator)
VALUES(gen_random_uuid()::text,'CNY',1,1,'1970-01-01T00:00:00Z','034.3b CNY genesis',NULL,NULL,false);
CREATE TRIGGER immutable_coin_rate BEFORE UPDATE OR DELETE ON coin_rate_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

ALTER TABLE funding_receipts ADD COLUMN buy_rate_version_id text;
ALTER TABLE funding_receipts DISABLE TRIGGER immutable_funding_receipt;
ALTER TABLE funding_receipts DISABLE TRIGGER wallet_source_fact;
UPDATE funding_receipts SET buy_rate_version_id=(SELECT id FROM coin_rate_versions WHERE fiat_currency='CNY') WHERE basis_kind='paid';
ALTER TABLE funding_receipts ENABLE TRIGGER immutable_funding_receipt;
ALTER TABLE funding_receipts ENABLE TRIGGER wallet_source_fact;
ALTER TABLE funding_receipts
 ADD CONSTRAINT funding_receipts_buy_rate_check CHECK((basis_kind='paid')=(buy_rate_version_id IS NOT NULL)),
 ADD CONSTRAINT funding_receipts_buy_rate_fk FOREIGN KEY(buy_rate_version_id,fiat_currency) REFERENCES coin_rate_versions(id,fiat_currency);

ALTER TABLE cash_refund_orders
 ADD CONSTRAINT cash_refund_buy_rate_fk FOREIGN KEY(buy_rate_version_id) REFERENCES coin_rate_versions(id),
 ADD CONSTRAINT cash_refund_refund_rate_fk FOREIGN KEY(refund_rate_version_id) REFERENCES coin_rate_versions(id);

CREATE OR REPLACE FUNCTION payment_funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 JOIN credit_orders c ON c.id=f.source_id AND c.tenant_id=f.tenant_id AND c.id=f.parent_ref
 WHERE j.id=journal_ref AND wallet_credit_pair_valid(c.id)
 AND f.basis_kind=CASE WHEN f.source_kind='manual_bonus' THEN 'grant' ELSE c.basis_kind END AND j.currency='COIN' AND f.currency='COIN' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.basis_kind='paid' AND (f.fiat_currency,f.fiat_amount)=(c.fiat_currency,c.fiat_amount))
  OR (f.source_kind='manual_bonus' AND c.basis_kind IN ('paid','grant') AND f.fiat_currency IS NULL AND f.fiat_amount IS NULL)
  OR (f.source_kind='manual_unknown' AND c.basis_kind='unknown' AND f.fiat_currency IS NULL AND f.fiat_amount IS NULL))
 AND (f.basis_kind<>'paid' OR f.buy_rate_version_id IS NOT NULL)
 AND ((f.source_kind='manual_credit' AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND f.amount=c.amount)
  OR (f.source_kind='manual_bonus' AND j.kind='credit_bonus' AND j.operation_id<>c.id AND j.reference=c.id AND f.amount=c.bonus_amount)
  OR (f.source_kind='manual_unknown' AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND f.amount=c.amount))
 AND j.actor=c.approver
 AND ((c.approver<>c.requester AND c.approver IS DISTINCT FROM c.requested_by)
  OR (c.single_operator AND c.approver=c.requester AND c.requested_by=c.approver
   AND EXISTS(SELECT 1 FROM users a JOIN users m ON m.id=c.requester AND m.platform_role='admin' AND m.status='active' WHERE a.id=c.approver AND a.platform_role='admin' AND a.status='active')))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL
  AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;

CREATE OR REPLACE FUNCTION payment_sandbox_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='sandbox_capture'
 JOIN payment_captures c ON c.id=f.source_id JOIN payment_effects x ON x.capture_id=c.id
 WHERE j.id=journal_ref AND payment_capture_source_valid(c.id)
 AND f.basis_kind='paid' AND f.fiat_currency=c.currency AND f.fiat_amount=c.amount AND f.parent_ref=c.id
 AND f.buy_rate_version_id IS NOT NULL
 AND EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_evidence_processing p ON p.evidence_id=i.evidence_id
  WHERE i.id=c.identity_id AND p.generation=i.processing_generation AND p.state IN ('processed','processing_review'))
 AND EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=c.verified_evidence_id AND p.state='processed' AND p.generation=c.processing_generation)
 AND EXISTS(SELECT 1 FROM audit_events a WHERE a.action='payment.capture.credited' AND a.target=c.id AND a.actor='payment-sandbox-worker' AND a.tenant_scope=c.tenant_id AND a.sanitized_diff=jsonb_build_object('amount',c.wallet_credit_amount::text) AND a.request_id='')
 AND (j.kind,j.operation_id,j.reference,j.actor,j.currency)=('sandbox_capture',c.id,c.order_id,'payment-sandbox-worker','COIN')
 AND (f.tenant_id,f.amount,f.currency,j.tenant_id)=(c.tenant_id,c.wallet_credit_amount,'COIN',c.tenant_id)
 AND (x.identity_id,x.environment_id,x.merchant_id,x.provider_transaction_id,x.effect_kind,x.effect_id)=(c.identity_id,c.environment_id,c.merchant_id,c.provider_transaction_id,'capture','capture')
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=c.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=c.wallet_credit_amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='SANDBOX_FUNDING_CLEARING' AND l.signed_delta=-c.wallet_credit_amount)
 AND (SELECT count(*) FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited' AND b.tenant_id=c.tenant_id AND b.schema_version=1 AND b.sanitized_payload=jsonb_build_object('amount',c.wallet_credit_amount::text)))
$$;

DO $$ DECLARE f record; BEGIN
 FOR f IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname IN ('payment_funding_journal_valid','payment_sandbox_journal_valid') LOOP
  EXECUTE format('ALTER FUNCTION %s SECURITY INVOKER',f.oid::regprocedure::text);
  EXECUTE format('ALTER FUNCTION %s SET search_path TO %I,pg_catalog,pg_temp',f.oid::regprocedure::text,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC',f.oid::regprocedure::text);
 END LOOP;
END $$;

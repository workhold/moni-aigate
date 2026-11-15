-- 034.1 Virtual-coin units for customer wallets and retail prices.
-- Supplier costs, procurement observations and payment fiat amounts stay CNY.
-- This is a current-only append migration.

-- Backfill values without changing any amount. Immutable-history and deferred
-- source-validation triggers are disabled only for these explicit unit backfills
-- and restored before any function or constraint is validated.
ALTER TABLE journals DISABLE TRIGGER immutable_journal;
ALTER TABLE funding_receipts DISABLE TRIGGER immutable_funding_receipt;
ALTER TABLE price_versions DISABLE TRIGGER immutable_price;
ALTER TABLE journals DISABLE TRIGGER wallet_source_fact;
ALTER TABLE funding_receipts DISABLE TRIGGER wallet_source_fact;
ALTER TABLE journals DROP CONSTRAINT journals_currency_check;
ALTER TABLE funding_receipts DROP CONSTRAINT funding_receipts_currency_check;
ALTER TABLE price_versions DROP CONSTRAINT price_versions_currency_check;
UPDATE journals SET currency='COIN' WHERE currency='CNY';
UPDATE funding_receipts SET currency='COIN' WHERE currency='CNY';
UPDATE price_versions SET currency='COIN' WHERE currency='CNY';

ALTER TABLE journals ALTER COLUMN currency SET DEFAULT 'COIN';
ALTER TABLE funding_receipts ALTER COLUMN currency SET DEFAULT 'COIN';
ALTER TABLE price_versions ALTER COLUMN currency SET DEFAULT 'COIN';
ALTER TABLE journals ADD CONSTRAINT journals_currency_check CHECK (currency='COIN');
ALTER TABLE funding_receipts ADD CONSTRAINT funding_receipts_currency_check CHECK (currency='COIN');
ALTER TABLE price_versions ADD CONSTRAINT price_versions_currency_check CHECK (currency='COIN');
ALTER TABLE journals ENABLE TRIGGER immutable_journal;
ALTER TABLE funding_receipts ENABLE TRIGGER immutable_funding_receipt;
ALTER TABLE price_versions ENABLE TRIGGER immutable_price;
ALTER TABLE journals ENABLE TRIGGER wallet_source_fact;
ALTER TABLE funding_receipts ENABLE TRIGGER wallet_source_fact;

-- Existing pricing operations must remain readable and replayable under the
-- current COIN DTO. Migrate their complete command/result/audit identity and
-- retain only the old fingerprint as proof for persisted browser recovery.
ALTER TABLE pricing_operations ADD COLUMN pre_coin_input_fingerprint TEXT CHECK(pre_coin_input_fingerprint IS NULL OR pre_coin_input_fingerprint ~ '^[0-9a-f]{64}$');
ALTER TABLE pricing_operations DISABLE TRIGGER immutable_pricing_operation;
ALTER TABLE audit_events DISABLE TRIGGER immutable_audit;
UPDATE pricing_operations
SET pre_coin_input_fingerprint=input_fingerprint,
 safe_input=jsonb_set(safe_input,'{currency}','"COIN"'::jsonb,false),
 result_snapshot=jsonb_set(result_snapshot,'{currency}','"COIN"'::jsonb,false),
 input_fingerprint=pricing_fingerprint(kind,actor,scope,target,jsonb_set(safe_input,'{currency}','"COIN"'::jsonb,false))
WHERE kind IN ('group_price.publish','default_price.publish')
 AND safe_input->>'currency'='CNY' AND result_snapshot->>'currency'='CNY'
 AND pre_coin_input_fingerprint IS NULL;
UPDATE audit_events a SET sanitized_diff=jsonb_set(a.sanitized_diff,'{after}',p.result_snapshot,false)
FROM pricing_operations p
WHERE p.operation_id=a.sanitized_diff->>'operation_id'
 AND p.kind IN ('group_price.publish','default_price.publish')
 AND a.action='pricing.price.publish' AND a.sanitized_diff->'after'->>'currency'='CNY';
ALTER TABLE pricing_operations ENABLE TRIGGER immutable_pricing_operation;
ALTER TABLE audit_events ENABLE TRIGGER immutable_audit;

-- Metering canonical bytes and historical request snapshots intentionally keep
-- the frozen CNY normalization. Only the outer retail price currency changes.

-- Customer wallet/receipt guards are replaced as complete current functions.
DO $$
DECLARE fn record; original text; definition text;
BEGIN
 FOR fn IN SELECT p.oid,p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=current_schema() AND p.proname IN ('payment_funding_journal_valid','wallet_opening_journal_valid','meter_insert_token_price') LOOP
  original := pg_get_functiondef(fn.oid::regprocedure);
  definition := replace(original,'''CNY''','''COIN''');
  IF definition = original THEN RAISE EXCEPTION 'retail currency guard shape changed for %', fn.proname; END IF;
  EXECUTE definition;
  EXECUTE format('ALTER FUNCTION %s SECURITY INVOKER', fn.oid::regprocedure::text);
  EXECUTE format('ALTER FUNCTION %s SET search_path TO %I,pg_catalog,pg_temp', fn.oid::regprocedure::text, current_schema());
 END LOOP;
END $$;

-- Retail price command guards validate their own input envelopes. Change both
-- group/default branches; the adjacent supplier cost-price branch remains CNY.
DO $$
DECLARE original text; definition text; branch_start integer; branch_end integer; branch text;
BEGIN
 definition := pg_get_functiondef('assert_pricing_safe_input(text,jsonb)'::regprocedure);
 original := definition;
 branch_start := position('WHEN ''group_price.publish'' THEN' in definition);
 branch_end := position('WHEN ''cost_price.publish'' THEN' in definition);
 IF branch_start < 1 OR branch_end <= branch_start THEN RAISE EXCEPTION 'retail/cost price guard shape changed'; END IF;
 branch := substring(definition from branch_start for branch_end - branch_start);
 branch := replace(branch,'"CNY"','"COIN"');
 definition := substr(definition,1,branch_start-1) || branch || substr(definition,branch_end);
 IF definition = original THEN RAISE EXCEPTION 'retail currency guard did not change'; END IF;
 EXECUTE definition;
 ALTER FUNCTION assert_pricing_safe_input(text,jsonb) SECURITY INVOKER;
 EXECUTE format('ALTER FUNCTION assert_pricing_safe_input(text,jsonb) SET search_path TO %I,pg_catalog,pg_temp', current_schema());
END $$;

CREATE OR REPLACE FUNCTION payment_sandbox_journal_valid(journal_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='sandbox_capture'
 JOIN payment_captures c ON c.id=f.source_id JOIN payment_effects x ON x.capture_id=c.id
 WHERE j.id=journal_ref AND payment_capture_source_valid(c.id)
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
ALTER FUNCTION payment_sandbox_journal_valid(text) SECURITY INVOKER;
DO $$ BEGIN EXECUTE format('ALTER FUNCTION payment_sandbox_journal_valid(text) SET search_path TO %I,pg_catalog,pg_temp', current_schema()); END $$;

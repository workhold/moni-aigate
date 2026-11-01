-- 022-B1 isolated design WORKING DRAFT. Apply once after real 001--019 in a
-- disposable schema. NOT a production migration, NOT frozen, NO payment IO.
-- Includes human-command, source graph and lease guards. See b1-verification
-- for the actual restricted-LOGIN experiment scope, not production acceptance.
CREATE FUNCTION p1_require_rc() RETURNS void LANGUAGE plpgsql VOLATILE AS $$ BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'payment writes require read committed' USING ERRCODE='23514';END IF;
END $$;
CREATE FUNCTION p1_ref(v text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(octet_length(v) BETWEEN 1 AND 100 AND v!~'[[:cntrl:]]',false)
$$;
CREATE FUNCTION p1_time(v timestamptz) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT to_char(v AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
$$;
CREATE FUNCTION p1_frame(parts text[]) RETURNS bytea LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result bytea:=''::bytea;part text;
BEGIN FOREACH part IN ARRAY parts LOOP
 IF part IS NULL THEN result:=result||int4send(-1);ELSE result:=result||int4send(octet_length(convert_to(part,'UTF8')))||convert_to(part,'UTF8');END IF;
 END LOOP;RETURN result;
END $$;

CREATE TABLE payment_sandbox_environment(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),id text NOT NULL UNIQUE CHECK(funding_uuid(id)),
 merchant_id text NOT NULL CHECK(p1_ref(merchant_id)),payee_label text NOT NULL CHECK(p1_ref(payee_label)),
 database_oid oid NOT NULL,schema_oid oid NOT NULL,nonce_digest text NOT NULL CHECK(nonce_digest~'^[0-9a-f]{64}$'),
 app_role name NOT NULL,worker_role name NOT NULL,projector_role name NOT NULL,
 protocol_version integer NOT NULL DEFAULT 1 CHECK(protocol_version=1),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(id,merchant_id),CHECK(app_role<>worker_role AND app_role<>projector_role AND worker_role<>projector_role)
);
CREATE TRIGGER payment_environment_immutable BEFORE UPDATE OR DELETE ON payment_sandbox_environment FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE payment_policy(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),enabled boolean NOT NULL DEFAULT false,revision bigint NOT NULL DEFAULT 1 CHECK(revision>0));
INSERT INTO payment_policy DEFAULT VALUES;
CREATE FUNCTION p1_policy_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM p1_require_rc();IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment policy cannot be deleted';END IF;
 IF NEW.singleton IS DISTINCT FROM OLD.singleton THEN RAISE EXCEPTION 'payment policy identity immutable';END IF;
 NEW.revision:=OLD.revision+CASE WHEN NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END;RETURN NEW;
END $$;
CREATE TRIGGER payment_policy_guard BEFORE UPDATE OR DELETE ON payment_policy FOR EACH ROW EXECUTE FUNCTION p1_policy_guard();

-- Preserve A kind/scope/fingerprint semantics and A's five unique accepts.
ALTER TABLE commerce_commands DROP CONSTRAINT commerce_commands_kind_check;
ALTER TABLE commerce_commands ADD CHECK(kind IN ('voucher.batch.create','voucher.batch.approve','voucher.batch.void','voucher.revocation.create','voucher.revocation.approve','voucher.redeem','payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel'));
ALTER TABLE commerce_commands DROP CONSTRAINT commerce_commands_check1;
ALTER TABLE commerce_commands ADD CHECK((kind IN ('voucher.redeem','payment.order.create','payment.order.cancel'))=(scope<>'platform'));
ALTER TABLE commerce_operation_receipts DROP CONSTRAINT commerce_operation_receipts_reason_check;
ALTER TABLE commerce_operation_receipts ADD CHECK(reason IN ('accepted','voucher_unavailable','vouchers_disabled','voucher_revision_conflict','voucher_evidence_stale','voucher_state_conflict','funding_amount_overflow','payment_revision_conflict','payment_state_conflict','payments_disabled','payment_sku_unavailable'));
DROP INDEX commerce_accepted_resource;
CREATE UNIQUE INDEX commerce_accepted_resource ON commerce_operation_receipts(kind,resource_id) WHERE status='accepted' AND kind IN ('voucher.batch.create','voucher.batch.approve','voucher.batch.void','voucher.revocation.create','voucher.revocation.approve');

CREATE TABLE payment_skus(
 id text PRIMARY KEY CHECK(funding_uuid(id)),revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 last_version bigint NOT NULL DEFAULT 0 CHECK(last_version>=0),enabled boolean NOT NULL DEFAULT false,
 current_version_id text,created_by text NOT NULL REFERENCES users(id),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 CHECK(NOT enabled OR current_version_id IS NOT NULL)
);
CREATE TABLE payment_sku_versions(
 id text PRIMARY KEY CHECK(funding_uuid(id)),sku_id text NOT NULL REFERENCES payment_skus(id),version bigint NOT NULL CHECK(version>0),
 display_name text NOT NULL CHECK(p1_ref(display_name)),amount bigint NOT NULL CHECK(amount>0),wallet_credit_amount bigint NOT NULL,
 currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),environment_id text NOT NULL,merchant_id text NOT NULL,payee_label text NOT NULL CHECK(p1_ref(payee_label)),
 checkout_ttl_seconds integer NOT NULL DEFAULT 900 CHECK(checkout_ttl_seconds=900),
 publish_operation_id text NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),created_by text NOT NULL REFERENCES users(id),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(sku_id,version),UNIQUE(sku_id,id),CHECK(wallet_credit_amount=amount),FOREIGN KEY(environment_id,merchant_id) REFERENCES payment_sandbox_environment(id,merchant_id)
);
ALTER TABLE payment_skus ADD FOREIGN KEY(id,current_version_id) REFERENCES payment_sku_versions(sku_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE payment_sku_changes(
 operation_id text PRIMARY KEY REFERENCES commerce_commands(operation_id),sku_id text NOT NULL REFERENCES payment_skus(id),
 kind text NOT NULL CHECK(kind IN ('payment.sku.publish','payment.sku.activate')),
 before_revision bigint NOT NULL CHECK(before_revision>=0),after_revision bigint NOT NULL CHECK(after_revision>0),
 before_version_id text,after_version_id text,published_version_id text,
 before_enabled boolean NOT NULL,after_enabled boolean NOT NULL,created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(sku_id,before_version_id) REFERENCES payment_sku_versions(sku_id,id),FOREIGN KEY(sku_id,after_version_id) REFERENCES payment_sku_versions(sku_id,id),
 FOREIGN KEY(sku_id,published_version_id) REFERENCES payment_sku_versions(sku_id,id),
 CHECK((kind='payment.sku.publish' AND published_version_id IS NOT NULL AND after_revision=before_revision+1) OR
 (kind='payment.sku.activate' AND published_version_id IS NULL AND after_revision=before_revision+CASE WHEN (before_version_id,before_enabled) IS DISTINCT FROM (after_version_id,after_enabled) THEN 1 ELSE 0 END))
);
CREATE TABLE payment_orders(
 id text PRIMARY KEY CHECK(funding_uuid(id)) REFERENCES commerce_commands(operation_id),tenant_id text NOT NULL REFERENCES tenants(id),
 creator text NOT NULL REFERENCES users(id),sku_version_id text NOT NULL REFERENCES payment_sku_versions(id),display_name text NOT NULL CHECK(p1_ref(display_name)),
 amount bigint NOT NULL CHECK(amount>0),wallet_credit_amount bigint NOT NULL,currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 environment_id text NOT NULL,merchant_id text NOT NULL,payee_label text NOT NULL CHECK(p1_ref(payee_label)),merchant_order_id text NOT NULL UNIQUE,
 created_at timestamptz(6) NOT NULL,expires_at timestamptz(6) NOT NULL,
 UNIQUE(tenant_id,id),UNIQUE(environment_id,merchant_id,id),
 CHECK(wallet_credit_amount=amount AND expires_at=created_at+interval '900 seconds' AND isfinite(created_at) AND isfinite(expires_at)),
 CHECK(merchant_order_id='mps1.'||environment_id||'.'||id),FOREIGN KEY(environment_id,merchant_id) REFERENCES payment_sandbox_environment(id,merchant_id)
);
CREATE TABLE payment_cancel_intents(
 order_id text PRIMARY KEY REFERENCES payment_orders(id),operation_id text NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),
 actor text NOT NULL REFERENCES users(id),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION p1_order_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v payment_sku_versions%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT v FROM payment_sku_versions WHERE id=NEW.sku_version_id;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.id AND kind='payment.order.create' AND actor=NEW.creator AND scope='tenant:'||NEW.tenant_id) THEN RAISE EXCEPTION 'payment order command mismatch';END IF;
 NEW.display_name:=v.display_name;NEW.amount:=v.amount;NEW.wallet_credit_amount:=v.wallet_credit_amount;NEW.currency:=v.currency;
 NEW.environment_id:=v.environment_id;NEW.merchant_id:=v.merchant_id;NEW.payee_label:=v.payee_label;
 NEW.merchant_order_id:='mps1.'||v.environment_id||'.'||NEW.id;NEW.created_at:=clock_timestamp();NEW.expires_at:=NEW.created_at+interval '900 seconds';RETURN NEW;
END $$;
CREATE TRIGGER payment_order_insert BEFORE INSERT ON payment_orders FOR EACH ROW EXECUTE FUNCTION p1_order_insert();
CREATE TRIGGER payment_order_immutable BEFORE UPDATE OR DELETE ON payment_orders FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE payment_command_outbox(
 id text PRIMARY KEY CHECK(funding_uuid(id)),order_id text NOT NULL REFERENCES payment_orders(id),kind text NOT NULL CHECK(kind IN ('checkout','cancel','query')),
 external_command_id text CHECK(funding_uuid(external_command_id)),original_outbox_id text REFERENCES payment_command_outbox(id),
 canonical_parameters bytea,parameter_digest text GENERATED ALWAYS AS (encode(sha256(canonical_parameters),'hex')) STORED,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','retry_wait','leased','completed','submission_unknown','review_required')),
 generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
 current_query_id text,next_attempt_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),lease_until timestamptz(6),last_error_code text,
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 CHECK((kind IN ('checkout','cancel') AND external_command_id IS NOT NULL AND canonical_parameters IS NOT NULL AND octet_length(canonical_parameters) BETWEEN 1 AND 8192 AND original_outbox_id IS NULL AND attempt_count<=10 AND current_query_id IS NULL)
 OR(kind='query' AND external_command_id IS NULL AND canonical_parameters IS NULL AND original_outbox_id IS NOT NULL AND attempt_count<=20)),
 CHECK((state='leased')=(lease_until IS NOT NULL)),
 CHECK(last_error_code IS NULL OR last_error_code IN ('network','timeout','auth','protocol','environment','temporary','permanent','attempts_exhausted'))
);
CREATE UNIQUE INDEX payment_one_mutation ON payment_command_outbox(order_id,kind) WHERE kind IN ('checkout','cancel');
CREATE UNIQUE INDEX payment_external_command ON payment_command_outbox(external_command_id) WHERE external_command_id IS NOT NULL;
CREATE UNIQUE INDEX payment_one_query_supervisor ON payment_command_outbox(original_outbox_id) WHERE kind='query';
CREATE INDEX payment_outbox_claim ON payment_command_outbox(next_attempt_at,id) WHERE state IN ('pending','retry_wait','submission_unknown');
CREATE TABLE payment_command_observations(
 id text PRIMARY KEY CHECK(funding_uuid(id)),outbox_id text NOT NULL REFERENCES payment_command_outbox(id),generation bigint NOT NULL CHECK(generation>0),
 canonical_reply bytea NOT NULL CHECK(octet_length(canonical_reply) BETWEEN 1 AND 65536),
 reply_digest text GENERATED ALWAYS AS(encode(sha256(canonical_reply),'hex')) STORED,observed_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(outbox_id,generation,reply_digest)
);
CREATE TABLE payment_query_attempts(
 query_id text PRIMARY KEY CHECK(funding_uuid(query_id)),supervisor_id text NOT NULL REFERENCES payment_command_outbox(id),
 created_generation bigint NOT NULL CHECK(created_generation>0),canonical_request bytea NOT NULL CHECK(octet_length(canonical_request) BETWEEN 1 AND 8192),
 request_digest text GENERATED ALWAYS AS(encode(sha256(canonical_request),'hex')) STORED,created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(supervisor_id,query_id)
);
ALTER TABLE payment_command_outbox ADD FOREIGN KEY(id,current_query_id) REFERENCES payment_query_attempts(supervisor_id,query_id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE payment_inbox(
 id text PRIMARY KEY CHECK(funding_uuid(id)),environment_id text NOT NULL,merchant_id text NOT NULL,event_id text NOT NULL CHECK(p1_ref(event_id)),
 canonical_body bytea NOT NULL CHECK(octet_length(canonical_body) BETWEEN 1 AND 65536),
 business_digest text GENERATED ALWAYS AS(encode(sha256(canonical_body),'hex')) STORED,
 verified_key_id text NOT NULL CHECK(verified_key_id~'^[A-Za-z0-9_-]{1,32}$'),received_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(environment_id,merchant_id,event_id),FOREIGN KEY(environment_id,merchant_id) REFERENCES payment_sandbox_environment(id,merchant_id)
);
CREATE TABLE payment_event_conflicts(
 id text PRIMARY KEY CHECK(funding_uuid(id)),inbox_id text NOT NULL REFERENCES payment_inbox(id),
 conflicting_digest text NOT NULL CHECK(conflicting_digest~'^[0-9a-f]{64}$'),received_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),UNIQUE(inbox_id,conflicting_digest)
);
CREATE TABLE payment_verified_queries(
 id text PRIMARY KEY CHECK(funding_uuid(id)),query_id text NOT NULL UNIQUE REFERENCES payment_query_attempts(query_id),
 canonical_body bytea NOT NULL CHECK(octet_length(canonical_body) BETWEEN 1 AND 65536),
 snapshot_digest text GENERATED ALWAYS AS(encode(sha256(canonical_body),'hex')) STORED,
 verified_key_id text NOT NULL CHECK(verified_key_id~'^[A-Za-z0-9_-]{1,32}$'),received_at timestamptz(6) NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE payment_query_conflicts(
 id text PRIMARY KEY CHECK(funding_uuid(id)),verified_query_id text NOT NULL REFERENCES payment_verified_queries(id),
 conflicting_digest text NOT NULL CHECK(conflicting_digest~'^[0-9a-f]{64}$'),received_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),UNIQUE(verified_query_id,conflicting_digest)
);
CREATE FUNCTION p1_original_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j jsonb;q jsonb;
BEGIN
 PERFORM p1_require_rc();j:=convert_from(NEW.canonical_body,'UTF8')::jsonb;NEW.received_at:=clock_timestamp();
 IF TG_TABLE_NAME='payment_inbox' THEN
  NEW.environment_id:=j->>'environment_id';NEW.merchant_id:=j->>'merchant_id';NEW.event_id:=j->>'event_id';
 ELSE
  SELECT convert_from(canonical_request,'UTF8')::jsonb INTO STRICT q FROM payment_query_attempts WHERE query_id=NEW.query_id;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'query_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM (q->>'schema_version',q->>'environment_id',q->>'merchant_id',q->>'query_id',q->>'merchant_order_id',q->>'command_id',q->>'command_kind',q->>'parameter_digest') OR j->>'command_state' NOT IN ('not_seen','in_progress','completed') THEN RAISE EXCEPTION 'payment query original binding mismatch';END IF;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_notification_stamp BEFORE INSERT ON payment_inbox FOR EACH ROW EXECUTE FUNCTION p1_original_insert();
CREATE TRIGGER payment_query_stamp BEFORE INSERT ON payment_verified_queries FOR EACH ROW EXECUTE FUNCTION p1_original_insert();
CREATE TABLE payment_verified_evidence(
 id text PRIMARY KEY CHECK(funding_uuid(id)),inbox_id text UNIQUE REFERENCES payment_inbox(id),verified_query_id text UNIQUE REFERENCES payment_verified_queries(id),
 environment_id text NOT NULL,merchant_id text NOT NULL,order_id text NOT NULL CHECK(funding_uuid(order_id)),
 kind text NOT NULL CHECK(kind IN ('capture','closed','observation','unsupported')),
 provider_transaction_id text CHECK(provider_transaction_id IS NULL OR p1_ref(provider_transaction_id)),amount bigint CHECK(amount>0),currency text CHECK(currency='CNY'),
 happened_at timestamptz(6),provider_order_revision bigint CHECK(provider_order_revision>0),
 fact_digest text NOT NULL CHECK(fact_digest~'^[0-9a-f]{64}$'),verified_at timestamptz(6) NOT NULL,
 CHECK((inbox_id IS NOT NULL)<>(verified_query_id IS NOT NULL)),
 CHECK(kind<>'capture' OR(provider_transaction_id IS NOT NULL AND amount IS NOT NULL AND currency IS NOT NULL AND happened_at IS NOT NULL AND provider_order_revision IS NOT NULL)),
 CHECK(kind<>'closed' OR(happened_at IS NOT NULL AND provider_order_revision IS NOT NULL)),
 FOREIGN KEY(environment_id,merchant_id) REFERENCES payment_sandbox_environment(id,merchant_id)
);
CREATE INDEX payment_evidence_order ON payment_verified_evidence(order_id,verified_at DESC,id COLLATE "C" DESC);
CREATE TABLE payment_evidence_processing(
 evidence_id text PRIMARY KEY REFERENCES payment_verified_evidence(id),state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','retry_wait','leased','processed','processing_review')),
 generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 20),
 next_attempt_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),lease_until timestamptz(6),last_error_code text,
 CHECK((state='leased')=(lease_until IS NOT NULL))
);
CREATE INDEX payment_evidence_claim ON payment_evidence_processing(next_attempt_at,evidence_id) WHERE state IN ('pending','retry_wait');

-- Authentication occurs only in the P verification entrance. This normalizer
-- proves columns match the stored immutable verified original; it does NOT
-- re-implement HMAC or authenticate an arbitrary caller of the APP SQL role.
CREATE FUNCTION p1_evidence_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j jsonb;q jsonb;fact jsonb;r jsonb;received timestamptz;env text;merchant text;oid text;evtype text;failed text;
BEGIN
 PERFORM p1_require_rc();NEW.provider_transaction_id:=NULL;NEW.amount:=NULL;NEW.currency:=NULL;NEW.happened_at:=NULL;NEW.provider_order_revision:=NULL;
 IF NEW.inbox_id IS NOT NULL AND NEW.verified_query_id IS NULL THEN
  SELECT convert_from(canonical_body,'UTF8')::jsonb,received_at,environment_id,merchant_id INTO STRICT j,received,env,merchant FROM payment_inbox WHERE id=NEW.inbox_id;
  IF j->>'environment_id' IS DISTINCT FROM env OR j->>'merchant_id' IS DISTINCT FROM merchant OR (j->>'schema_version')::integer<>1 THEN RAISE EXCEPTION 'payment notification original mismatch';END IF;
  oid:=j->>'order_id';evtype:=j->>'event_type';failed:=j->>'failure_code';
  NEW.provider_transaction_id:=j->>'provider_transaction_id';NEW.amount:=(j->>'amount')::bigint;NEW.currency:=j->>'currency';
  NEW.happened_at:=(j->>'occurred_at')::timestamptz;NEW.provider_order_revision:=(j->>'provider_order_revision')::bigint;
  NEW.kind:=CASE WHEN evtype='capture.confirmed' THEN 'capture' WHEN evtype='checkout.failed' AND failed='order_closed' THEN 'closed' WHEN evtype='checkout.failed' THEN 'observation' ELSE 'unsupported' END;
 ELSIF NEW.inbox_id IS NULL AND NEW.verified_query_id IS NOT NULL THEN
  SELECT convert_from(v.canonical_body,'UTF8')::jsonb,convert_from(a.canonical_request,'UTF8')::jsonb,v.received_at INTO STRICT j,q,received
   FROM payment_verified_queries v JOIN payment_query_attempts a ON a.query_id=v.query_id WHERE v.id=NEW.verified_query_id;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'query_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest')
   IS DISTINCT FROM (q->>'schema_version',q->>'environment_id',q->>'merchant_id',q->>'query_id',q->>'merchant_order_id',q->>'command_id',q->>'command_kind',q->>'parameter_digest') THEN RAISE EXCEPTION 'payment query original mismatch';END IF;
  env:=j->>'environment_id';merchant:=j->>'merchant_id';oid:=split_part(j->>'merchant_order_id','.',3);r:=j->'result';
  NEW.kind:='observation';
  IF j->>'command_state'='completed' AND r->>'status'='captured' THEN
   fact:=r->'capture';NEW.kind:='capture';NEW.provider_transaction_id:=fact->>'provider_transaction_id';NEW.amount:=(fact->>'amount')::bigint;NEW.currency:=fact->>'currency';
   NEW.happened_at:=(fact->>'occurred_at')::timestamptz;NEW.provider_order_revision:=(fact->>'provider_order_revision')::bigint;
   IF fact->>'provider_order_revision' IS DISTINCT FROM j->>'provider_order_revision' THEN RAISE EXCEPTION 'payment query revision mismatch';END IF;
  ELSIF j->>'command_state'='completed' AND r->>'status'='closed' THEN
   NEW.kind:='closed';NEW.happened_at:=(r->>'closed_at')::timestamptz;NEW.provider_order_revision:=(j->>'provider_order_revision')::bigint;
  END IF;
 ELSE RAISE EXCEPTION 'payment evidence must have one original';END IF;
 NEW.environment_id:=env;NEW.merchant_id:=merchant;NEW.order_id:=oid;NEW.verified_at:=received;
 IF NEW.happened_at>received+interval '5 seconds' THEN RAISE EXCEPTION 'payment verified fact from future';END IF;
 IF NEW.kind='capture' THEN
  NEW.fact_digest:=encode(sha256(p1_frame(ARRAY[env,merchant,oid,'capture',NEW.provider_transaction_id,NEW.amount::text,NEW.currency,p1_time(NEW.happened_at),NEW.provider_order_revision::text])),'hex');
 ELSIF NEW.kind='closed' THEN NEW.fact_digest:=encode(sha256(p1_frame(ARRAY[env,merchant,oid,'closed',p1_time(NEW.happened_at),NEW.provider_order_revision::text])),'hex');
 ELSE NEW.fact_digest:=encode(sha256(p1_frame(ARRAY[env,merchant,oid,NEW.kind,coalesce(j->>'event_id',j->>'query_id')])),'hex');END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_evidence_insert BEFORE INSERT ON payment_verified_evidence FOR EACH ROW EXECUTE FUNCTION p1_evidence_insert();

CREATE TABLE payment_provider_revisions(
 environment_id text NOT NULL,merchant_id text NOT NULL,order_id text NOT NULL REFERENCES payment_orders(id),provider_order_revision bigint NOT NULL CHECK(provider_order_revision>0),
 kind text NOT NULL CHECK(kind IN ('capture','closed')),fact_digest text NOT NULL CHECK(fact_digest~'^[0-9a-f]{64}$'),evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 PRIMARY KEY(environment_id,merchant_id,order_id,provider_order_revision),FOREIGN KEY(environment_id,merchant_id,order_id) REFERENCES payment_orders(environment_id,merchant_id,id)
);
CREATE FUNCTION p1_revision_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e payment_verified_evidence%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=NEW.evidence_id;
 IF e.kind NOT IN ('capture','closed') THEN RAISE EXCEPTION 'payment provider revision needs authoritative fact';END IF;
 PERFORM id FROM payment_orders WHERE id=e.order_id FOR NO KEY UPDATE;
 IF e.kind='closed' AND EXISTS(SELECT 1 FROM payment_transaction_identities i WHERE i.order_id=e.order_id
   AND (i.captured_at>=e.happened_at OR i.provider_order_revision>=e.provider_order_revision)) THEN
  RAISE EXCEPTION 'payment closed conflicts with prior immutable capture identity';END IF;
 NEW.environment_id:=e.environment_id;NEW.merchant_id:=e.merchant_id;NEW.order_id:=e.order_id;NEW.provider_order_revision:=e.provider_order_revision;
 NEW.kind:=e.kind;NEW.fact_digest:=e.fact_digest;RETURN NEW;
END $$;
CREATE TRIGGER payment_revision_insert BEFORE INSERT ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION p1_revision_insert();
CREATE TABLE payment_transaction_identities(
 id text PRIMARY KEY CHECK(funding_uuid(id)),environment_id text NOT NULL,merchant_id text NOT NULL,provider_transaction_id text NOT NULL CHECK(p1_ref(provider_transaction_id)),
 order_id text NOT NULL,tenant_id text NOT NULL,amount bigint NOT NULL CHECK(amount>0),currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 captured_at timestamptz(6) NOT NULL,provider_order_revision bigint NOT NULL CHECK(provider_order_revision>0),evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),processing_generation bigint NOT NULL CHECK(processing_generation>0),UNIQUE(environment_id,merchant_id,provider_transaction_id),UNIQUE(order_id,id),
 FOREIGN KEY(tenant_id,order_id) REFERENCES payment_orders(tenant_id,id),FOREIGN KEY(environment_id,merchant_id,order_id) REFERENCES payment_orders(environment_id,merchant_id,id)
);
CREATE TABLE payment_order_capture_selections(
 order_id text PRIMARY KEY REFERENCES payment_orders(id),identity_id text NOT NULL UNIQUE,first_evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 selected_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),FOREIGN KEY(order_id,identity_id) REFERENCES payment_transaction_identities(order_id,id)
);
CREATE TABLE payment_captures(
 id text PRIMARY KEY CHECK(funding_uuid(id)),order_id text NOT NULL UNIQUE REFERENCES payment_order_capture_selections(order_id),identity_id text NOT NULL UNIQUE,
 tenant_id text NOT NULL,environment_id text NOT NULL,merchant_id text NOT NULL,provider_transaction_id text NOT NULL CHECK(p1_ref(provider_transaction_id)),
 amount bigint NOT NULL CHECK(amount>0),wallet_credit_amount bigint NOT NULL,currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 captured_at timestamptz(6) NOT NULL,provider_order_revision bigint NOT NULL CHECK(provider_order_revision>0),verified_evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),processing_generation bigint NOT NULL CHECK(processing_generation>0),CHECK(wallet_credit_amount=amount),UNIQUE(tenant_id,id),
 FOREIGN KEY(order_id,identity_id) REFERENCES payment_transaction_identities(order_id,id),FOREIGN KEY(tenant_id,order_id) REFERENCES payment_orders(tenant_id,id)
);
CREATE TABLE payment_effects(
 capture_id text PRIMARY KEY REFERENCES payment_captures(id),identity_id text NOT NULL UNIQUE REFERENCES payment_transaction_identities(id),
 environment_id text NOT NULL,merchant_id text NOT NULL,provider_transaction_id text NOT NULL CHECK(p1_ref(provider_transaction_id)),
 effect_kind text NOT NULL DEFAULT 'capture' CHECK(effect_kind='capture'),effect_id text NOT NULL DEFAULT 'capture' CHECK(effect_id='capture'),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),UNIQUE(environment_id,merchant_id,provider_transaction_id,effect_kind,effect_id)
);
CREATE TABLE payment_review_facts(
 id text PRIMARY KEY CHECK(funding_uuid(id)),order_id text NOT NULL CHECK(funding_uuid(order_id)),evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 identity_id text REFERENCES payment_transaction_identities(id),selection_order_id text REFERENCES payment_order_capture_selections(order_id),
 reason text NOT NULL CHECK(reason IN ('unknown_order','capture_outside_window','provider_time_invalid','provider_revision_conflict','amount_mismatch','currency_mismatch','transaction_conflict','duplicate_order_capture','funding_amount_overflow','unsupported_effect_review','processing_attempts_exhausted','command_attempts_exhausted','query_attempts_exhausted','provider_protocol_invalid')),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),UNIQUE(evidence_id,reason),CHECK(selection_order_id IS NULL OR selection_order_id=order_id)
);
CREATE INDEX payment_review_order ON payment_review_facts(order_id,created_at,id COLLATE "C");
CREATE FUNCTION p1_review_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e payment_verified_evidence%ROWTYPE;o payment_orders%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=NEW.evidence_id;
 IF NEW.order_id IS DISTINCT FROM e.order_id THEN RAISE EXCEPTION 'payment review order/evidence mismatch';END IF;
 SELECT * INTO o FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
 IF (NEW.reason='unknown_order' AND o.id IS NOT NULL) OR (NEW.reason='amount_mismatch' AND (o.id IS NULL OR e.amount IS NOT DISTINCT FROM o.amount)) OR (NEW.reason='currency_mismatch' AND (o.id IS NULL OR e.currency IS NOT DISTINCT FROM o.currency)) OR (NEW.reason='capture_outside_window' AND (o.id IS NULL OR (e.happened_at>=o.created_at AND e.happened_at<o.expires_at))) OR (NEW.reason='unsupported_effect_review' AND e.kind<>'unsupported') THEN RAISE EXCEPTION 'payment review reason does not match evidence';END IF;
 IF NEW.identity_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_transaction_identities WHERE id=NEW.identity_id AND order_id=NEW.order_id) THEN RAISE EXCEPTION 'payment review identity mismatch';END IF;
 NEW.created_at:=clock_timestamp();
 IF NEW.reason='funding_amount_overflow' AND (NEW.selection_order_id IS DISTINCT FROM NEW.order_id OR
  NOT EXISTS(SELECT 1 FROM payment_order_capture_selections s WHERE s.order_id=NEW.order_id AND s.identity_id=NEW.identity_id)
  OR EXISTS(SELECT 1 FROM payment_captures WHERE order_id=NEW.order_id)) THEN
  RAISE EXCEPTION 'payment overflow review requires unfunded selected identity';END IF;
 IF NEW.reason='duplicate_order_capture' AND NOT EXISTS(SELECT 1 FROM payment_order_capture_selections s
  JOIN payment_transaction_identities i ON i.id=NEW.identity_id WHERE s.order_id=NEW.order_id AND i.order_id=s.order_id AND s.identity_id<>i.id) THEN
  RAISE EXCEPTION 'payment duplicate capture review requires other selected identity';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_review_insert BEFORE INSERT ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION p1_review_insert();

CREATE FUNCTION p1_identity_valid(identity_ref text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_orders o ON o.id=i.order_id
 JOIN payment_verified_evidence e ON e.id=i.evidence_id JOIN payment_provider_revisions r ON (r.environment_id,r.merchant_id,r.order_id,r.provider_order_revision)=(i.environment_id,i.merchant_id,i.order_id,i.provider_order_revision)
 WHERE i.id=identity_ref AND e.kind='capture' AND (i.environment_id,i.merchant_id,i.order_id,i.tenant_id,i.amount,i.currency)=(o.environment_id,o.merchant_id,o.id,o.tenant_id,o.amount,o.currency)
 AND (i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)=(e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
 AND r.kind='capture' AND r.fact_digest=e.fact_digest AND i.captured_at>=o.created_at AND i.captured_at<o.expires_at
 AND NOT EXISTS(SELECT 1 FROM payment_provider_revisions closed JOIN payment_verified_evidence ce ON ce.id=closed.evidence_id WHERE closed.order_id=o.id AND closed.kind='closed' AND (i.captured_at>=ce.happened_at OR i.provider_order_revision>=closed.provider_order_revision)))
$$;
CREATE FUNCTION p1_capture_source_valid(capture_ref text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM payment_captures c JOIN payment_orders o ON o.id=c.order_id JOIN payment_order_capture_selections s ON s.order_id=o.id
 JOIN payment_transaction_identities i ON i.id=s.identity_id JOIN payment_verified_evidence e ON e.id=c.verified_evidence_id
 WHERE c.id=capture_ref AND p1_identity_valid(i.id) AND c.identity_id=i.id
 AND (c.tenant_id,c.environment_id,c.merchant_id,c.provider_transaction_id,c.amount,c.wallet_credit_amount,c.currency,c.captured_at,c.provider_order_revision)
  =(i.tenant_id,i.environment_id,i.merchant_id,i.provider_transaction_id,i.amount,o.wallet_credit_amount,i.currency,i.captured_at,i.provider_order_revision)
 AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
  =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)
 AND NOT EXISTS(SELECT 1 FROM payment_review_facts v WHERE v.selection_order_id=s.order_id AND v.reason='funding_amount_overflow'))
$$;

ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check;
ALTER TABLE ledger_accounts ADD CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING'));
ALTER TABLE ledger_accounts ADD CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR
 (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR (tenant_id IS NULL AND kind IN ('OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING') AND balance<=0));
ALTER TABLE funding_receipts DROP CONSTRAINT funding_receipts_source_kind_check;
ALTER TABLE funding_receipts ADD CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture'));
-- Prototype only: preserve the literal A predicate and compose the new branch.
-- Production migration must keep the original funding_journal_valid OID.
ALTER FUNCTION funding_journal_valid(text) RENAME TO p1_a_funding_journal_valid;
CREATE FUNCTION p1_sandbox_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='sandbox_capture'
 JOIN payment_captures c ON c.id=f.source_id JOIN payment_effects x ON x.capture_id=c.id
 WHERE j.id=journal_ref AND p1_capture_source_valid(c.id)
 AND EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=c.verified_evidence_id AND p.state='processed' AND p.generation=c.processing_generation)
 AND EXISTS(SELECT 1 FROM audit_events a WHERE a.action='payment.capture.credited' AND a.target=c.id AND a.actor='payment-sandbox-worker' AND a.tenant_scope=c.tenant_id AND a.sanitized_diff=jsonb_build_object('amount',c.wallet_credit_amount::text) AND a.request_id='')
 AND (j.kind,j.operation_id,j.reference,j.actor,j.currency)=('sandbox_capture',c.id,c.order_id,'payment-sandbox-worker','CNY')
 AND (f.tenant_id,f.amount,f.currency,j.tenant_id)=(c.tenant_id,c.wallet_credit_amount,'CNY',c.tenant_id)
 AND (x.identity_id,x.environment_id,x.merchant_id,x.provider_transaction_id,x.effect_kind,x.effect_id)=(c.identity_id,c.environment_id,c.merchant_id,c.provider_transaction_id,'capture','capture')
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=c.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=c.wallet_credit_amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='SANDBOX_FUNDING_CLEARING' AND l.signed_delta=-c.wallet_credit_amount)
 AND (SELECT count(*) FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited' AND b.tenant_id=c.tenant_id AND b.schema_version=1 AND b.sanitized_payload=jsonb_build_object('amount',c.wallet_credit_amount::text)))
$$;
CREATE FUNCTION funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
BEGIN
 -- Use a statement-level branch: the worker must not need SELECT on A's
 -- voucher/manual source tables just to validate its sandbox journal.
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='sandbox_capture') THEN RETURN p1_sandbox_journal_valid(journal_ref);END IF;
 RETURN p1_a_funding_journal_valid(journal_ref);
END
$$;

CREATE FUNCTION p1_source_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE identity_ref text;order_ref text;jid text;
BEGIN
 IF TG_TABLE_NAME='payment_transaction_identities' THEN identity_ref:=NEW.id;order_ref:=NEW.order_id;
  IF NOT p1_identity_valid(identity_ref) OR NOT EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=NEW.evidence_id AND p.generation=NEW.processing_generation AND p.state IN ('processed','processing_review')) THEN RAISE EXCEPTION 'payment transaction identity invalid' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM payment_order_capture_selections WHERE identity_id=identity_ref) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE identity_id=identity_ref AND order_id=order_ref AND reason='duplicate_order_capture') THEN RAISE EXCEPTION 'payment identity neither selected nor reviewed' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME='payment_order_capture_selections' THEN
  order_ref:=NEW.order_id;identity_ref:=NEW.identity_id;
  IF NOT p1_identity_valid(identity_ref) OR NOT EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_verified_evidence e ON e.id=NEW.first_evidence_id WHERE i.id=identity_ref AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)=(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)) THEN RAISE EXCEPTION 'payment selection identity mismatch' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM payment_captures WHERE order_id=order_ref) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE selection_order_id=order_ref AND identity_id=identity_ref AND reason='funding_amount_overflow') THEN RAISE EXCEPTION 'payment selection missing funding or review' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME IN ('payment_captures','payment_effects') THEN
  IF TG_TABLE_NAME='payment_captures' THEN order_ref:=NEW.order_id;SELECT journal_id INTO jid FROM funding_receipts WHERE source_kind='sandbox_capture' AND source_id=NEW.id;
  ELSE SELECT c.order_id,f.journal_id INTO order_ref,jid FROM payment_captures c LEFT JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id WHERE c.id=NEW.capture_id;END IF;
  IF jid IS NULL OR NOT p1_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'payment funding graph incomplete' USING ERRCODE='23514';END IF;
 END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_identity_complete AFTER INSERT ON payment_transaction_identities DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_source_complete();
CREATE CONSTRAINT TRIGGER payment_selection_complete AFTER INSERT ON payment_order_capture_selections DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_source_complete();
CREATE CONSTRAINT TRIGGER payment_capture_complete AFTER INSERT ON payment_captures DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_source_complete();
CREATE CONSTRAINT TRIGGER payment_effect_complete AFTER INSERT ON payment_effects DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_source_complete();
CREATE FUNCTION p1_extra_funding_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid text;
BEGIN
 IF TG_TABLE_NAME='journals' THEN IF NEW.kind<>'sandbox_capture' THEN RETURN NULL;END IF;jid:=NEW.id;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM ledger_accounts WHERE id=NEW.account_id AND kind='SANDBOX_FUNDING_CLEARING') THEN RETURN NULL;END IF;jid:=NEW.journal_id;
 END IF;
 IF NOT p1_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'sandbox journal or clearing missing funding source' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_sandbox_journal_complete AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_extra_funding_complete();
CREATE CONSTRAINT TRIGGER payment_sandbox_clearing_complete AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_extra_funding_complete();

-- Entry ownership is distinct from external signature authentication.
CREATE FUNCTION p1_source_writer() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE worker name;is_sandbox boolean;parent_kind text;processing payment_evidence_processing%ROWTYPE;
BEGIN
 SELECT worker_role INTO worker FROM payment_sandbox_environment WHERE singleton;
 IF TG_TABLE_NAME='funding_receipts' THEN is_sandbox:=NEW.source_kind='sandbox_capture';
 ELSIF TG_TABLE_NAME='journals' THEN is_sandbox:=NEW.kind='sandbox_capture';
 ELSIF TG_TABLE_NAME='projection_outbox' THEN
  IF NEW.kind<>'credited' THEN
   IF current_user=worker THEN RAISE EXCEPTION 'payment worker cannot emit other projection effects' USING ERRCODE='42501';END IF;RETURN NEW;
  END IF;SELECT kind INTO parent_kind FROM journals WHERE operation_id=NEW.operation_id;is_sandbox:=parent_kind='sandbox_capture';
 ELSE is_sandbox:=true;END IF;
 IF is_sandbox AND (worker IS NULL OR current_user<>worker) THEN RAISE EXCEPTION 'sandbox funding requires payment worker' USING ERRCODE='42501';END IF;
 IF current_user=worker AND NOT coalesce(is_sandbox,false) THEN RAISE EXCEPTION 'payment worker cannot write other funding' USING ERRCODE='42501';END IF;
 IF is_sandbox THEN
  PERFORM p1_require_rc();
  IF TG_TABLE_NAME IN ('payment_transaction_identities','payment_order_capture_selections','payment_captures') THEN
   PERFORM id FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
   IF TG_TABLE_NAME='payment_transaction_identities' THEN
    SELECT * INTO STRICT processing FROM payment_evidence_processing WHERE evidence_id=NEW.evidence_id;
    IF processing.state<>'leased' OR processing.lease_until<=clock_timestamp() OR NEW.processing_generation IS DISTINCT FROM processing.generation THEN RAISE EXCEPTION 'payment identity requires exact caller processing claim';END IF;
    NEW.processing_generation:=processing.generation;NEW.created_at:=clock_timestamp();
   END IF;
  END IF;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_receipt_writer BEFORE INSERT ON funding_receipts FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_journal_writer BEFORE INSERT ON journals FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_credited_writer BEFORE INSERT ON projection_outbox FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_capture_writer BEFORE INSERT ON payment_captures FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_effect_writer BEFORE INSERT ON payment_effects FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_identity_writer BEFORE INSERT ON payment_transaction_identities FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_selection_writer BEFORE INSERT ON payment_order_capture_selections FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_revision_writer BEFORE INSERT ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE TRIGGER payment_review_writer BEFORE INSERT ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION p1_source_writer();
CREATE FUNCTION p1_worker_tenant_lock_only() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF current_user=(SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment worker tenant privilege is lock-only' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_worker_tenant_lock_only BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION p1_worker_tenant_lock_only();

-- Add immutable-history guards and bounded-page indexes without owner-only
-- bypass settings. Mutable lease/root rows are closed by the later guards;
-- production application wiring remains a separate B2/B3 task.
DO $$
DECLARE t text;
BEGIN
 FOREACH t IN ARRAY ARRAY['payment_sku_versions','payment_sku_changes','payment_cancel_intents','payment_command_observations','payment_query_attempts','payment_inbox','payment_event_conflicts','payment_verified_queries','payment_query_conflicts','payment_verified_evidence','payment_provider_revisions','payment_transaction_identities','payment_order_capture_selections','payment_captures','payment_effects','payment_review_facts'] LOOP
  EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_history_mutation()',t||'_immutable',t);
 END LOOP;
END $$;
CREATE INDEX payment_sku_page ON payment_skus(created_at DESC,id COLLATE "C" DESC);
CREATE INDEX payment_order_page ON payment_orders(tenant_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX payment_order_platform_page ON payment_orders(created_at DESC,id COLLATE "C" DESC);
CREATE INDEX payment_capture_tenant ON payment_captures(tenant_id,created_at DESC,id COLLATE "C" DESC);


-- B1 human commands: immutable change -> controlled root projection -> exact
-- A-compatible terminal. These are executable guards, not HTTP authorization.
ALTER TABLE payment_skus ADD COLUMN last_change_operation_id text REFERENCES payment_sku_changes(operation_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE payment_sku_changes ADD COLUMN before_operation_id text REFERENCES payment_sku_changes(operation_id);
ALTER TABLE payment_sku_changes ADD COLUMN created_xid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE FUNCTION p1_sku_root_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c payment_sku_changes%ROWTYPE;v bigint;
BEGIN
 PERFORM p1_require_rc();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment SKU cannot be deleted';END IF;
 IF TG_OP='INSERT' THEN
  NEW.revision:=1;NEW.last_version:=0;NEW.enabled:=false;NEW.current_version_id:=NULL;NEW.last_change_operation_id:=NULL;NEW.created_at:=clock_timestamp();RETURN NEW;
 END IF;
 IF (NEW.id,NEW.created_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.created_by,OLD.created_at) THEN RAISE EXCEPTION 'payment SKU identity immutable';END IF;
 SELECT * INTO STRICT c FROM payment_sku_changes WHERE operation_id=NEW.last_change_operation_id AND sku_id=OLD.id;
 SELECT version INTO v FROM payment_sku_versions WHERE id=c.published_version_id;
 IF c.created_xid IS DISTINCT FROM pg_current_xact_id() OR c.before_operation_id IS DISTINCT FROM OLD.last_change_operation_id
  OR (c.before_version_id,c.before_enabled) IS DISTINCT FROM (OLD.current_version_id,OLD.enabled)
  OR c.before_revision IS DISTINCT FROM (CASE WHEN OLD.last_version=0 AND OLD.last_change_operation_id IS NULL THEN 0 ELSE OLD.revision END)
  OR (NEW.revision,NEW.current_version_id,NEW.enabled,NEW.last_version) IS DISTINCT FROM
   (c.after_revision,c.after_version_id,c.after_enabled,CASE WHEN c.kind='payment.sku.publish' THEN v ELSE OLD.last_version END)
 THEN RAISE EXCEPTION 'payment SKU projection requires exact new change';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_sku_root_guard BEFORE INSERT OR UPDATE OR DELETE ON payment_skus FOR EACH ROW EXECUTE FUNCTION p1_sku_root_guard();
CREATE FUNCTION p1_sku_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s payment_skus%ROWTYPE;c commerce_commands%ROWTYPE;env payment_sandbox_environment%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.sku_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=NEW.publish_operation_id;
 IF c.kind<>'payment.sku.publish' OR c.scope<>'platform' OR c.actor IS DISTINCT FROM NEW.created_by THEN RAISE EXCEPTION 'payment SKU publish command mismatch';END IF;
 PERFORM id FROM users WHERE id=c.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=c.actor AND status='active' AND platform_role='admin') THEN RAISE EXCEPTION 'payment SKU publisher forbidden';END IF;
 SELECT * INTO STRICT env FROM payment_sandbox_environment WHERE singleton;
 NEW.environment_id:=env.id;NEW.merchant_id:=env.merchant_id;NEW.payee_label:=env.payee_label;NEW.currency:='CNY';NEW.wallet_credit_amount:=NEW.amount;
 NEW.version:=s.last_version+1;NEW.checkout_ttl_seconds:=900;NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_sku_version_insert BEFORE INSERT ON payment_sku_versions FOR EACH ROW EXECUTE FUNCTION p1_sku_version_insert();
CREATE FUNCTION p1_sku_change_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s payment_skus%ROWTYPE;c commerce_commands%ROWTYPE;v payment_sku_versions%ROWTYPE;first_publish boolean;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.sku_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=NEW.operation_id;
 IF c.kind IS DISTINCT FROM NEW.kind OR c.scope<>'platform' THEN RAISE EXCEPTION 'payment SKU change command mismatch';END IF;
 PERFORM id FROM users WHERE id=c.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=c.actor AND status='active' AND platform_role='admin') THEN RAISE EXCEPTION 'payment SKU actor forbidden';END IF;
 first_publish:=s.last_version=0 AND s.last_change_operation_id IS NULL;
 IF NEW.before_revision IS DISTINCT FROM (CASE WHEN first_publish THEN 0 ELSE s.revision END) THEN RAISE EXCEPTION 'payment SKU revision conflict';END IF;
 NEW.before_operation_id:=s.last_change_operation_id;NEW.before_version_id:=s.current_version_id;NEW.before_enabled:=s.enabled;
 IF NEW.kind='payment.sku.publish' THEN
  SELECT * INTO STRICT v FROM payment_sku_versions WHERE id=NEW.published_version_id AND sku_id=s.id AND publish_operation_id=c.operation_id;
  IF v.version<>s.last_version+1 OR (first_publish AND s.created_by<>c.actor) THEN RAISE EXCEPTION 'payment published version mismatch';END IF;
  NEW.after_version_id:=s.current_version_id;NEW.after_enabled:=s.enabled;NEW.after_revision:=CASE WHEN first_publish THEN 1 ELSE s.revision+1 END;
 ELSE
  IF first_publish OR NEW.published_version_id IS NOT NULL OR (NEW.after_enabled AND NEW.after_version_id IS NULL) OR
   (NEW.after_version_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_sku_versions WHERE id=NEW.after_version_id AND sku_id=s.id)) THEN RAISE EXCEPTION 'payment activation state invalid';END IF;
  NEW.after_revision:=s.revision+CASE WHEN (NEW.after_version_id,NEW.after_enabled) IS DISTINCT FROM (s.current_version_id,s.enabled) THEN 1 ELSE 0 END;
 END IF;
 NEW.created_at:=clock_timestamp();NEW.created_xid:=pg_current_xact_id();RETURN NEW;
END $$;
CREATE TRIGGER payment_sku_change_insert BEFORE INSERT ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION p1_sku_change_insert();
CREATE FUNCTION p1_sku_change_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE payment_skus SET revision=NEW.after_revision,current_version_id=NEW.after_version_id,enabled=NEW.after_enabled,
  last_version=CASE WHEN NEW.kind='payment.sku.publish' THEN (SELECT version FROM payment_sku_versions WHERE id=NEW.published_version_id) ELSE last_version END,
  last_change_operation_id=NEW.operation_id WHERE id=NEW.sku_id;RETURN NULL;
END $$;
CREATE TRIGGER payment_sku_change_apply AFTER INSERT ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION p1_sku_change_apply();

CREATE OR REPLACE FUNCTION p1_order_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v payment_sku_versions%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();
 PERFORM s.id FROM payment_skus s JOIN payment_sku_versions pv ON pv.sku_id=s.id WHERE pv.id=NEW.sku_version_id FOR SHARE OF s;
 PERFORM singleton FROM payment_policy WHERE singleton FOR SHARE;
 PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
 PERFORM user_id FROM memberships WHERE tenant_id=NEW.tenant_id AND user_id=NEW.creator FOR SHARE;
 PERFORM id FROM users WHERE id=NEW.creator FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=NEW.tenant_id AND m.user_id=NEW.creator AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'payment order owner forbidden';END IF;
 SELECT * INTO STRICT v FROM payment_sku_versions WHERE id=NEW.sku_version_id;
 IF NOT EXISTS(SELECT 1 FROM payment_skus WHERE id=v.sku_id AND enabled AND current_version_id=v.id) OR NOT EXISTS(SELECT 1 FROM payment_policy WHERE singleton AND enabled) THEN RAISE EXCEPTION 'payment order sale unavailable';END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.id AND kind='payment.order.create' AND actor=NEW.creator AND scope='tenant:'||NEW.tenant_id) THEN RAISE EXCEPTION 'payment order command mismatch';END IF;
 NEW.display_name:=v.display_name;NEW.amount:=v.amount;NEW.wallet_credit_amount:=v.wallet_credit_amount;NEW.currency:=v.currency;
 NEW.environment_id:=v.environment_id;NEW.merchant_id:=v.merchant_id;NEW.payee_label:=v.payee_label;
 NEW.merchant_order_id:='mps1.'||v.environment_id||'.'||NEW.id;NEW.created_at:=clock_timestamp();NEW.expires_at:=NEW.created_at+interval '900 seconds';RETURN NEW;
END $$;
CREATE FUNCTION p1_cancel_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE o payment_orders%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT o FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
 PERFORM id FROM tenants WHERE id=o.tenant_id FOR SHARE;PERFORM user_id FROM memberships WHERE tenant_id=o.tenant_id AND user_id=NEW.actor FOR SHARE;PERFORM id FROM users WHERE id=NEW.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=o.tenant_id AND m.user_id=NEW.actor AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'payment cancel owner forbidden';END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.operation_id AND kind='payment.order.cancel' AND actor=NEW.actor AND scope='tenant:'||o.tenant_id) OR
  EXISTS(SELECT 1 FROM payment_captures WHERE order_id=o.id) OR EXISTS(SELECT 1 FROM payment_provider_revisions WHERE order_id=o.id AND kind='closed') THEN RAISE EXCEPTION 'payment cancel state invalid';END IF;
 NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_cancel_insert BEFORE INSERT ON payment_cancel_intents FOR EACH ROW EXECUTE FUNCTION p1_cancel_insert();

CREATE FUNCTION p1_check_human(operation_ref text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE c commerce_commands%ROWTYPE;r commerce_operation_receipts%ROWTYPE;valid boolean:=false;
BEGIN
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=operation_ref;
 IF c.kind NOT LIKE 'payment.%' THEN RETURN;END IF;
 SELECT * INTO STRICT r FROM commerce_operation_receipts WHERE operation_id=c.operation_id;
 IF r.status='rejected' THEN
  IF (c.kind IN ('payment.sku.publish','payment.sku.activate') AND r.reason NOT IN ('payment_revision_conflict','payment_state_conflict')) OR
   (c.kind='payment.order.create' AND r.reason NOT IN ('payments_disabled','payment_sku_unavailable')) OR
   (c.kind='payment.order.cancel' AND r.reason<>'payment_state_conflict') THEN RAISE EXCEPTION 'payment terminal reason invalid';END IF;
  IF EXISTS(SELECT 1 FROM payment_sku_changes WHERE operation_id=c.operation_id) OR EXISTS(SELECT 1 FROM payment_sku_versions WHERE publish_operation_id=c.operation_id) OR
   EXISTS(SELECT 1 FROM payment_orders WHERE id=c.operation_id) OR EXISTS(SELECT 1 FROM payment_cancel_intents WHERE operation_id=c.operation_id) OR EXISTS(SELECT 1 FROM audit_events WHERE action=c.kind AND target=c.operation_id) THEN RAISE EXCEPTION 'rejected payment command has effect';END IF;RETURN;
 END IF;
 IF c.kind='payment.sku.publish' THEN
  SELECT EXISTS(SELECT 1 FROM payment_sku_versions v JOIN payment_sku_changes x ON x.published_version_id=v.id WHERE v.id=r.resource_id AND v.publish_operation_id=c.operation_id AND v.created_by=c.actor AND x.operation_id=c.operation_id AND x.sku_id=v.sku_id AND x.kind=c.kind) INTO valid;
 ELSIF c.kind='payment.sku.activate' THEN
  SELECT EXISTS(SELECT 1 FROM payment_sku_changes x WHERE x.operation_id=c.operation_id AND x.sku_id=r.resource_id AND x.kind=c.kind) INTO valid;
 ELSIF c.kind='payment.order.create' THEN
  SELECT EXISTS(SELECT 1 FROM payment_orders o JOIN payment_command_outbox b ON b.order_id=o.id AND b.kind='checkout' WHERE o.id=c.operation_id AND o.id=r.resource_id AND o.creator=c.actor AND c.scope='tenant:'||o.tenant_id) INTO valid;
 ELSE
  SELECT EXISTS(SELECT 1 FROM payment_cancel_intents x JOIN payment_orders o ON o.id=x.order_id JOIN payment_command_outbox b ON b.order_id=o.id AND b.kind='cancel' WHERE x.operation_id=c.operation_id AND x.order_id=r.resource_id AND x.actor=c.actor AND c.scope='tenant:'||o.tenant_id) INTO valid;
 END IF;
 IF NOT valid OR NOT EXISTS(SELECT 1 FROM audit_events WHERE action=c.kind AND target=c.operation_id AND actor=c.actor AND tenant_scope IS NOT DISTINCT FROM (CASE WHEN c.scope='platform' THEN NULL ELSE substr(c.scope,8) END) AND sanitized_diff='{}'::jsonb) THEN RAISE EXCEPTION 'payment command resource incomplete' USING ERRCODE='23514';END IF;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'payment command terminal missing' USING ERRCODE='23514';
END $$;
CREATE FUNCTION p1_human_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE operation_ref text;s payment_skus%ROWTYPE;c payment_sku_changes%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='payment_skus' THEN
  SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.id;
  SELECT * INTO c FROM payment_sku_changes WHERE operation_id=s.last_change_operation_id;
  IF c.operation_id IS NULL OR s.last_version<1 OR (s.revision,s.current_version_id,s.enabled) IS DISTINCT FROM (c.after_revision,c.after_version_id,c.after_enabled)
   OR NOT EXISTS(SELECT 1 FROM payment_sku_versions WHERE sku_id=s.id AND version=s.last_version) THEN RAISE EXCEPTION 'payment SKU root incomplete';END IF;
  operation_ref:=s.last_change_operation_id;
 ELSIF TG_TABLE_NAME='payment_sku_versions' THEN operation_ref:=NEW.publish_operation_id;
 ELSIF TG_TABLE_NAME='payment_orders' THEN operation_ref:=NEW.id;
 ELSIF TG_TABLE_NAME='payment_command_outbox' THEN
  IF NEW.kind='query' THEN RETURN NULL;END IF;
  IF NEW.kind='checkout' THEN operation_ref:=NEW.order_id;ELSE SELECT operation_id INTO operation_ref FROM payment_cancel_intents WHERE order_id=NEW.order_id;END IF;
 ELSE operation_ref:=NEW.operation_id;END IF;
 PERFORM p1_check_human(operation_ref);RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_human_command_complete AFTER INSERT ON commerce_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_human_terminal_complete AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_sku_complete AFTER INSERT OR UPDATE ON payment_skus DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_sku_version_complete AFTER INSERT ON payment_sku_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_sku_change_complete AFTER INSERT ON payment_sku_changes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_order_complete AFTER INSERT ON payment_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_cancel_complete AFTER INSERT ON payment_cancel_intents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();
CREATE CONSTRAINT TRIGGER payment_mutation_outbox_complete AFTER INSERT ON payment_command_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_human_complete();

-- Lease machines. The online worker owns transitions; immutable evidence may
-- be retained after a stale response, but stale generations cannot ACK work.
ALTER TABLE payment_command_outbox ADD COLUMN last_submission_unknown_at timestamptz(6);
ALTER TABLE payment_command_outbox ADD CHECK(generation=attempt_count);
ALTER TABLE payment_query_attempts ADD COLUMN mutation_generation bigint NOT NULL CHECK(mutation_generation>0);
ALTER TABLE payment_query_attempts ADD UNIQUE(supervisor_id,created_generation);
ALTER TABLE payment_evidence_processing ADD CHECK(generation=attempt_count);
ALTER TABLE payment_evidence_processing ADD CHECK(last_error_code IS NULL OR last_error_code IN ('temporary','permanent','attempts_exhausted'));


CREATE FUNCTION p1_backoff(n integer) RETURNS interval LANGUAGE sql IMMUTABLE AS $$
 SELECT make_interval(secs=>CASE WHEN n<=1 THEN 1 WHEN n=2 THEN 5 WHEN n=3 THEN 30 WHEN n=4 THEN 120 ELSE 300 END)
$$;
CREATE FUNCTION p1_outbox_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE o payment_orders%ROWTYPE;b payment_command_outbox%ROWTYPE;j jsonb;worker name;
BEGIN
 PERFORM p1_require_rc();SELECT worker_role INTO STRICT worker FROM payment_sandbox_environment WHERE singleton;
 IF NEW.state<>'pending' OR NEW.generation<>0 OR NEW.attempt_count<>0 OR NEW.lease_until IS NOT NULL OR NEW.current_query_id IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.last_submission_unknown_at IS NOT NULL THEN RAISE EXCEPTION 'payment outbox initial state';END IF;
 SELECT * INTO STRICT o FROM payment_orders WHERE id=NEW.order_id;
 IF NEW.kind='query' THEN
  IF current_user<>worker THEN RAISE EXCEPTION 'payment query supervisor requires worker' USING ERRCODE='42501';END IF;
  SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=NEW.original_outbox_id AND kind IN ('checkout','cancel') AND order_id=o.id;
 ELSE
  IF current_user=worker THEN RAISE EXCEPTION 'payment mutation requires application' USING ERRCODE='42501';END IF;
  j:=convert_from(NEW.canonical_parameters,'UTF8')::jsonb;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'command_id',j->>'merchant_order_id') IS DISTINCT FROM ('1',o.environment_id,o.merchant_id,NEW.external_command_id,o.merchant_order_id) THEN RAISE EXCEPTION 'payment mutation frozen identity mismatch';END IF;
  IF NEW.kind='checkout' AND ((j->>'amount',j->>'currency',j->>'created_at',j->>'expires_at') IS DISTINCT FROM (o.amount::text,o.currency,p1_time(o.created_at),p1_time(o.expires_at)) OR (SELECT count(*) FROM jsonb_object_keys(j))<>9) THEN RAISE EXCEPTION 'payment checkout frozen tuple mismatch';END IF;
  IF NEW.kind='cancel' AND ((SELECT count(*) FROM jsonb_object_keys(j))<>5 OR NOT EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=o.id)) THEN RAISE EXCEPTION 'payment cancel command missing intent';END IF;
 END IF;
 NEW.created_at:=clock_timestamp();NEW.next_attempt_at:=NEW.created_at;RETURN NEW;
END $$;
CREATE TRIGGER payment_outbox_insert BEFORE INSERT ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION p1_outbox_insert();

CREATE FUNCTION p1_query_attempt_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s payment_command_outbox%ROWTYPE;b payment_command_outbox%ROWTYPE;o payment_orders%ROWTYPE;j jsonb;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT s FROM payment_command_outbox WHERE id=NEW.supervisor_id AND kind='query';
 SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=s.original_outbox_id;
 SELECT * INTO STRICT o FROM payment_orders WHERE id=b.order_id;j:=convert_from(NEW.canonical_request,'UTF8')::jsonb;
 IF s.state<>'leased' OR s.lease_until<=clock_timestamp() OR NEW.created_generation<>s.generation OR b.generation=0 THEN RAISE EXCEPTION 'payment query preparation requires current claim';END IF;
 IF s.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_verified_queries WHERE query_id=s.current_query_id) THEN RAISE EXCEPTION 'payment unknown query must reuse immutable child';END IF;
 IF (j->>'schema_version',j->>'query_id',j->>'environment_id',j->>'merchant_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM ('1',NEW.query_id,o.environment_id,o.merchant_id,o.merchant_order_id,b.external_command_id,b.kind,b.parameter_digest) OR (SELECT count(*) FROM jsonb_object_keys(j))<>8 THEN RAISE EXCEPTION 'payment query request binding mismatch';END IF;
 NEW.mutation_generation:=b.generation;NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_query_attempt_insert BEFORE INSERT ON payment_query_attempts FOR EACH ROW EXECUTE FUNCTION p1_query_attempt_insert();
CREATE FUNCTION p1_observation_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b payment_command_outbox%ROWTYPE;o payment_orders%ROWTYPE;j jsonb;
BEGIN
 PERFORM p1_require_rc();SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=NEW.outbox_id AND kind IN ('checkout','cancel');
 SELECT * INTO STRICT o FROM payment_orders WHERE id=b.order_id;j:=convert_from(NEW.canonical_reply,'UTF8')::jsonb;
 IF NEW.generation>b.generation OR (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM ('1',o.environment_id,o.merchant_id,o.merchant_order_id,b.external_command_id,b.kind,b.parameter_digest) OR j->>'command_state' NOT IN ('in_progress','completed') THEN RAISE EXCEPTION 'payment command observation binding mismatch';END IF;
 NEW.observed_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_observation_insert BEFORE INSERT ON payment_command_observations FOR EACH ROW EXECUTE FUNCTION p1_observation_insert();

CREATE FUNCTION p1_outbox_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp();budget integer;j jsonb;child payment_query_attempts%ROWTYPE;prior_unknown timestamptz;
BEGIN
 PERFORM p1_require_rc();
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment lease requires worker' USING ERRCODE='42501';END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment outbox cannot be deleted';END IF;
 IF (NEW.id,NEW.order_id,NEW.kind,NEW.external_command_id,NEW.original_outbox_id,NEW.canonical_parameters,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.order_id,OLD.kind,OLD.external_command_id,OLD.original_outbox_id,OLD.canonical_parameters,OLD.created_at) THEN RAISE EXCEPTION 'payment outbox identity immutable';END IF;
 budget:=CASE WHEN OLD.kind='query' THEN 20 ELSE 10 END;
 IF OLD.state IN ('completed','review_required') THEN RAISE EXCEPTION 'payment outbox terminal immutable';END IF;
 IF NEW.state='leased' AND OLD.state IN ('pending','retry_wait') THEN
  IF OLD.next_attempt_at>stamp OR OLD.attempt_count>=budget OR NEW.generation<>OLD.generation+1 OR NEW.attempt_count<>OLD.attempt_count+1 OR NEW.current_query_id IS DISTINCT FROM OLD.current_query_id THEN RAISE EXCEPTION 'payment outbox claim invalid';END IF;
  NEW.lease_until:=stamp+interval '60 seconds';NEW.last_error_code:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;NEW.last_submission_unknown_at:=OLD.last_submission_unknown_at;RETURN NEW;
 END IF;
 IF NEW.generation<>OLD.generation OR NEW.attempt_count<>OLD.attempt_count THEN RAISE EXCEPTION 'payment outbox budget immutable outside claim';END IF;
 IF OLD.kind='query' AND OLD.state='leased' AND NEW.state='leased' THEN
  IF OLD.lease_until<=stamp OR NEW.current_query_id IS NOT DISTINCT FROM OLD.current_query_id THEN RAISE EXCEPTION 'payment query pointer preparation invalid';END IF;
  SELECT * INTO STRICT child FROM payment_query_attempts WHERE query_id=NEW.current_query_id AND supervisor_id=OLD.id AND created_generation=OLD.generation;
  IF OLD.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_verified_queries WHERE query_id=OLD.current_query_id) THEN RAISE EXCEPTION 'payment unknown query pointer cannot be replaced';END IF;
  NEW.lease_until:=OLD.lease_until;NEW.next_attempt_at:=OLD.next_attempt_at;NEW.last_error_code:=OLD.last_error_code;NEW.last_submission_unknown_at:=OLD.last_submission_unknown_at;RETURN NEW;
 END IF;
 IF NEW.current_query_id IS DISTINCT FROM OLD.current_query_id THEN RAISE EXCEPTION 'payment query pointer immutable outside preparation';END IF;
 NEW.lease_until:=NULL;NEW.last_submission_unknown_at:=OLD.last_submission_unknown_at;
 IF OLD.state='leased' THEN
  IF OLD.kind='query' THEN
   IF OLD.lease_until>stamp THEN
    SELECT convert_from(canonical_body,'UTF8')::jsonb INTO j FROM payment_verified_queries WHERE query_id=OLD.current_query_id;
    IF NEW.state='completed' THEN
     IF j IS NULL OR j->>'command_state'<>'completed' THEN RAISE EXCEPTION 'payment completed query requires frozen completed snapshot';END IF;NEW.last_error_code:=NULL;
    ELSIF NEW.state='retry_wait' THEN
     IF OLD.attempt_count>=20 OR (j IS NOT NULL AND j->>'command_state'='completed') THEN RAISE EXCEPTION 'payment query retry invalid';END IF;
     IF NEW.last_error_code IS NULL AND j IS NULL THEN RAISE EXCEPTION 'payment unknown query requires fixed error';END IF;
    ELSIF NEW.state='review_required' THEN
     IF j IS NOT NULL AND j->>'command_state'='completed' THEN RAISE EXCEPTION 'payment completed query cannot become exhausted review';END IF;
     IF OLD.attempt_count<20 AND NEW.last_error_code IS DISTINCT FROM 'permanent' THEN RAISE EXCEPTION 'payment premature query review';END IF;
     IF OLD.attempt_count=20 THEN NEW.last_error_code:='attempts_exhausted';END IF;
    ELSE RAISE EXCEPTION 'payment query lease transition invalid';END IF;
   ELSE
    IF NEW.state IS DISTINCT FROM (CASE WHEN OLD.attempt_count=20 THEN 'review_required' ELSE 'retry_wait' END) THEN RAISE EXCEPTION 'payment expired query transition invalid';END IF;
    NEW.last_error_code:=CASE WHEN OLD.attempt_count=20 THEN 'attempts_exhausted' ELSE 'timeout' END;
   END IF;
  ELSE
   IF OLD.lease_until>stamp AND NEW.state='completed' THEN
    IF NOT EXISTS(SELECT 1 FROM payment_command_observations WHERE outbox_id=OLD.id AND generation=OLD.generation) THEN RAISE EXCEPTION 'payment mutation ACK requires current observation';END IF;NEW.last_error_code:=NULL;
   ELSIF NEW.state='submission_unknown' THEN
    NEW.last_submission_unknown_at:=stamp;NEW.last_error_code:=CASE WHEN OLD.attempt_count=10 THEN 'attempts_exhausted' ELSE coalesce(NEW.last_error_code,'timeout') END;
   ELSIF OLD.lease_until>stamp AND NEW.state='review_required' AND NEW.last_error_code='permanent' THEN NULL;
   ELSE RAISE EXCEPTION 'payment mutation lease transition invalid';END IF;
  END IF;
 ELSIF OLD.kind<>'query' AND OLD.state='submission_unknown' AND NEW.state IN ('retry_wait','completed') THEN
  SELECT convert_from(v.canonical_body,'UTF8')::jsonb INTO j FROM payment_command_outbox s JOIN payment_query_attempts a ON a.supervisor_id=s.id JOIN payment_verified_queries v ON v.query_id=a.query_id WHERE s.original_outbox_id=OLD.id AND (NEW.state='completed' OR (s.state IN ('leased','retry_wait') AND s.current_query_id=a.query_id)) AND a.mutation_generation=OLD.generation AND a.created_at>=OLD.last_submission_unknown_at AND convert_from(v.canonical_body,'UTF8')::jsonb->>'command_state'=CASE WHEN NEW.state='completed' THEN 'completed' ELSE 'not_seen' END ORDER BY a.created_at DESC LIMIT 1;
  IF j IS NULL OR (NEW.state='retry_wait' AND (OLD.attempt_count>=10 OR (OLD.kind='checkout' AND EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=OLD.order_id)))) THEN RAISE EXCEPTION 'payment mutation recovery requires current signed query';END IF;
  NEW.last_error_code:=NULL;
 ELSE RAISE EXCEPTION 'payment outbox transition invalid';END IF;
 NEW.next_attempt_at:=CASE WHEN NEW.state='retry_wait' THEN stamp+p1_backoff(OLD.attempt_count) ELSE OLD.next_attempt_at END;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_outbox_transition BEFORE UPDATE OR DELETE ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION p1_outbox_transition();
CREATE FUNCTION p1_outbox_complete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind<>'query' AND (NEW.state='submission_unknown' OR (NEW.state='completed' AND EXISTS(SELECT 1 FROM payment_command_observations o WHERE o.outbox_id=NEW.id AND o.generation=NEW.generation AND (convert_from(o.canonical_reply,'UTF8')::jsonb->>'command_state'='in_progress' OR convert_from(o.canonical_reply,'UTF8')::jsonb#>>'{result,status}'='captured')))) AND NOT EXISTS(SELECT 1 FROM payment_command_outbox WHERE original_outbox_id=NEW.id AND kind='query') THEN RAISE EXCEPTION 'payment unknown mutation needs query supervisor';END IF;
 IF NEW.kind='query' AND NEW.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_query_attempts WHERE supervisor_id=NEW.id AND query_id=NEW.current_query_id) THEN RAISE EXCEPTION 'payment query pointer incomplete';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_outbox_complete AFTER INSERT OR UPDATE ON payment_command_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_outbox_complete();

CREATE FUNCTION p1_processing_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp();e payment_verified_evidence%ROWTYPE;
BEGIN
 PERFORM p1_require_rc();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment processing cannot be deleted';END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.generation<>0 OR NEW.attempt_count<>0 OR NEW.lease_until IS NOT NULL OR NEW.last_error_code IS NOT NULL THEN RAISE EXCEPTION 'payment processing initial state';END IF;
  NEW.next_attempt_at:=stamp;RETURN NEW;
 END IF;
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment processing requires worker' USING ERRCODE='42501';END IF;
 IF NEW.evidence_id IS DISTINCT FROM OLD.evidence_id OR OLD.state IN ('processed','processing_review') THEN RAISE EXCEPTION 'payment processing terminal/identity immutable';END IF;
 IF OLD.state IN ('pending','retry_wait') AND NEW.state='leased' THEN
  IF OLD.next_attempt_at>stamp OR OLD.attempt_count>=20 OR NEW.generation<>OLD.generation+1 OR NEW.attempt_count<>OLD.attempt_count+1 THEN RAISE EXCEPTION 'payment processing claim invalid';END IF;
  NEW.lease_until:=stamp+interval '30 seconds';NEW.last_error_code:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;RETURN NEW;
 END IF;
 IF OLD.state<>'leased' OR NEW.generation<>OLD.generation OR NEW.attempt_count<>OLD.attempt_count THEN RAISE EXCEPTION 'payment processing transition invalid';END IF;
 IF OLD.lease_until<=stamp THEN
  IF NEW.state IS DISTINCT FROM (CASE WHEN OLD.attempt_count=20 THEN 'processing_review' ELSE 'retry_wait' END) THEN RAISE EXCEPTION 'payment expired processing transition invalid';END IF;
  NEW.last_error_code:=CASE WHEN OLD.attempt_count=20 THEN 'attempts_exhausted' ELSE 'temporary' END;
 ELSIF NEW.state='processed' THEN
  SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=OLD.evidence_id;
  IF e.kind='closed' AND NOT EXISTS(SELECT 1 FROM payment_provider_revisions r WHERE r.order_id=e.order_id AND r.provider_order_revision=e.provider_order_revision AND r.kind='closed' AND r.fact_digest=e.fact_digest) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE evidence_id=e.id) THEN RAISE EXCEPTION 'payment closed processing requires revision or review';END IF;
  IF e.kind IN ('capture','unsupported') AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE evidence_id=e.id) AND NOT EXISTS(SELECT 1 FROM payment_captures c JOIN payment_transaction_identities i ON i.id=c.identity_id WHERE c.order_id=e.order_id AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)=(i.environment_id,i.merchant_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)) THEN RAISE EXCEPTION 'payment processing has no matching capture or review';END IF;
  NEW.last_error_code:=NULL;
 ELSIF NEW.state='processing_review' THEN
  IF NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE evidence_id=OLD.evidence_id) AND NOT (OLD.attempt_count=20 AND NEW.last_error_code='attempts_exhausted') THEN RAISE EXCEPTION 'payment processing review missing fact';END IF;
 ELSIF NEW.state='retry_wait' THEN
  IF OLD.attempt_count>=20 OR NEW.last_error_code IS DISTINCT FROM 'temporary' THEN RAISE EXCEPTION 'payment processing retry invalid';END IF;
 ELSE RAISE EXCEPTION 'payment processing ACK invalid';END IF;
 NEW.lease_until:=NULL;NEW.next_attempt_at:=CASE WHEN NEW.state='retry_wait' THEN stamp+p1_backoff(OLD.attempt_count) ELSE OLD.next_attempt_at END;RETURN NEW;
END $$;
CREATE TRIGGER payment_processing_guard BEFORE INSERT OR UPDATE OR DELETE ON payment_evidence_processing FOR EACH ROW EXECUTE FUNCTION p1_processing_guard();
CREATE FUNCTION p1_evidence_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM payment_evidence_processing WHERE evidence_id=NEW.id) THEN RAISE EXCEPTION 'payment evidence requires processing root';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_evidence_complete AFTER INSERT ON payment_verified_evidence DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_evidence_complete();
CREATE FUNCTION p1_capture_generation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p payment_evidence_processing%ROWTYPE;
BEGIN
 SELECT * INTO STRICT p FROM payment_evidence_processing WHERE evidence_id=NEW.verified_evidence_id;
 IF p.state<>'leased' OR p.lease_until<=clock_timestamp() OR NEW.processing_generation<>p.generation THEN RAISE EXCEPTION 'payment source requires current evidence claim';END IF;
 NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_capture_generation BEFORE INSERT ON payment_captures FOR EACH ROW EXECUTE FUNCTION p1_capture_generation();

CREATE FUNCTION p1_worker_account_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RETURN NEW;END IF;
 IF TG_OP='DELETE' OR NEW.kind NOT IN ('AVAILABLE','SANDBOX_FUNDING_CLEARING') OR (TG_OP='INSERT' AND NEW.balance<>0) THEN RAISE EXCEPTION 'payment worker account kind or initial balance forbidden' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.kind) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.kind) THEN RAISE EXCEPTION 'payment worker account identity immutable' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_worker_account_guard BEFORE INSERT OR UPDATE OR DELETE ON ledger_accounts FOR EACH ROW EXECUTE FUNCTION p1_worker_account_guard();
CREATE FUNCTION p1_worker_line_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF current_user=(SELECT worker_role FROM payment_sandbox_environment WHERE singleton) AND NOT EXISTS(SELECT 1 FROM journals WHERE id=NEW.journal_id AND kind='sandbox_capture') THEN RAISE EXCEPTION 'payment worker cannot append other ledger effects' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_worker_line_guard BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION p1_worker_line_guard();
CREATE FUNCTION p1_worker_balance_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE aid text;
BEGIN
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RETURN NULL;END IF;
 IF TG_TABLE_NAME='ledger_accounts' THEN aid:=NEW.id;ELSE aid:=NEW.account_id;END IF;
 IF NOT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=aid AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)) THEN RAISE EXCEPTION 'payment worker balance cache mismatch' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_worker_account_complete AFTER INSERT OR UPDATE ON ledger_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_worker_balance_complete();
CREATE CONSTRAINT TRIGGER payment_worker_line_complete AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_worker_balance_complete();

CREATE UNIQUE INDEX payment_capture_audit_once ON audit_events(action,target) WHERE action='payment.capture.credited';
CREATE UNIQUE INDEX payment_human_audit_once ON audit_events(action,target) WHERE action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel');
CREATE FUNCTION p1_audit_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE worker name;c payment_captures%ROWTYPE;op commerce_commands%ROWTYPE;
BEGIN
 SELECT worker_role INTO worker FROM payment_sandbox_environment WHERE singleton;
 IF NEW.action='payment.capture.credited' THEN
  IF current_user IS DISTINCT FROM worker THEN RAISE EXCEPTION 'payment capture audit requires worker' USING ERRCODE='42501';END IF;
  SELECT * INTO STRICT c FROM payment_captures WHERE id=NEW.target;
  IF (NEW.actor,NEW.tenant_scope,NEW.request_id,NEW.sanitized_diff) IS DISTINCT FROM ('payment-sandbox-worker',c.tenant_id,'',jsonb_build_object('amount',c.wallet_credit_amount::text)) THEN RAISE EXCEPTION 'payment capture audit mismatch';END IF;
 ELSIF current_user=worker THEN RAISE EXCEPTION 'payment worker audit action forbidden' USING ERRCODE='42501';
 ELSIF NEW.action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel') THEN
  SELECT * INTO STRICT op FROM commerce_commands WHERE operation_id=NEW.target AND kind=NEW.action;
  IF NEW.actor IS DISTINCT FROM op.actor OR NEW.tenant_scope IS DISTINCT FROM (CASE WHEN op.scope='platform' THEN NULL ELSE substr(op.scope,8) END) OR NEW.sanitized_diff<>'{}'::jsonb THEN RAISE EXCEPTION 'payment human audit mismatch';END IF;
 END IF;
 IF NEW.action LIKE 'payment.%' THEN NEW.created_at:=clock_timestamp();END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_audit_guard BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION p1_audit_guard();
CREATE FUNCTION p1_audit_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid text;
BEGIN
 IF NEW.action='payment.capture.credited' THEN
  SELECT journal_id INTO jid FROM funding_receipts WHERE source_kind='sandbox_capture' AND source_id=NEW.target;
  IF jid IS NULL OR NOT p1_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'payment capture audit missing complete source';END IF;
 ELSIF NEW.action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel') THEN PERFORM p1_check_human(NEW.target);END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_audit_complete AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_audit_complete();

CREATE FUNCTION p1_original_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_TABLE_NAME='payment_inbox' THEN
  IF NOT EXISTS(SELECT 1 FROM payment_verified_evidence WHERE inbox_id=NEW.id) THEN RAISE EXCEPTION 'payment notification evidence missing';END IF;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM payment_verified_evidence WHERE verified_query_id=NEW.id) THEN RAISE EXCEPTION 'payment query evidence missing';END IF;
 END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_notification_complete AFTER INSERT ON payment_inbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_original_complete();
CREATE CONSTRAINT TRIGGER payment_query_original_complete AFTER INSERT ON payment_verified_queries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION p1_original_complete();
DO $$
DECLARE f record;
BEGIN
 FOR f IN SELECT p.oid,p.proname,pg_get_function_identity_arguments(p.oid) args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND (p.proname LIKE 'p1\_%' ESCAPE '\' OR p.proname='funding_journal_valid') LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
 END LOOP;
END $$;

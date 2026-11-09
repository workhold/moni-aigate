-- Current MVP baseline. This single install artifact is the only runtime migration.
-- BEGIN 001_core.sql
CREATE TABLE tenants (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended')),
 allowed_regions TEXT[] NOT NULL DEFAULT ARRAY['global'],
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE users (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 email TEXT NOT NULL UNIQUE CHECK(email=lower(email)),
 password_hash TEXT NOT NULL,
 platform_role TEXT NOT NULL DEFAULT 'none' CHECK(platform_role IN ('none','admin','finance_operator','finance_approver')),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended')),
 mfa_secret TEXT NOT NULL DEFAULT '', mfa_pending_secret TEXT NOT NULL DEFAULT '', mfa_enabled BOOLEAN NOT NULL DEFAULT false, mfa_last_step BIGINT NOT NULL DEFAULT -1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE memberships (
 tenant_id TEXT NOT NULL REFERENCES tenants(id), user_id TEXT NOT NULL REFERENCES users(id),
 role TEXT NOT NULL CHECK(role IN ('owner','member','billing_viewer')),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended')),
 allow_all_models BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,user_id)
);
CREATE TABLE session_families (id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,user_id TEXT NOT NULL REFERENCES users(id),revoked_at TIMESTAMPTZ);
CREATE TABLE sessions (
 digest TEXT PRIMARY KEY, family_id TEXT NOT NULL REFERENCES session_families(id), user_id TEXT NOT NULL REFERENCES users(id), tenant_id TEXT REFERENCES tenants(id),
 csrf_token TEXT NOT NULL, mfa_verified BOOLEAN NOT NULL DEFAULT false,
 reauthenticated_at TIMESTAMPTZ, expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE mfa_recovery_codes (user_id TEXT NOT NULL REFERENCES users(id), digest TEXT NOT NULL, used_at TIMESTAMPTZ, PRIMARY KEY(user_id,digest));
CREATE TABLE models (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, public_name TEXT NOT NULL UNIQUE,
 enabled BOOLEAN NOT NULL DEFAULT true, profile JSONB NOT NULL,
 profile_version TEXT NOT NULL DEFAULT gen_random_uuid()::text,
 active_revision BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE model_capability_versions (id TEXT PRIMARY KEY, model_id TEXT NOT NULL REFERENCES models(id), profile JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp());
CREATE TABLE tenant_model_grants (tenant_id TEXT NOT NULL REFERENCES tenants(id), model_id TEXT NOT NULL REFERENCES models(id), PRIMARY KEY(tenant_id,model_id));
CREATE TABLE member_model_grants (tenant_id TEXT NOT NULL, user_id TEXT NOT NULL, model_id TEXT NOT NULL REFERENCES models(id), FOREIGN KEY(tenant_id,user_id) REFERENCES memberships(tenant_id,user_id), PRIMARY KEY(tenant_id,user_id,model_id));
CREATE TABLE api_keys (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL REFERENCES tenants(id), owner_id TEXT NOT NULL,
 name TEXT NOT NULL, prefix TEXT NOT NULL, digest TEXT NOT NULL UNIQUE,
 scopes TEXT[] NOT NULL, allowed_regions TEXT[] NOT NULL DEFAULT ARRAY['global'],
 expires_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ, lifetime_limit BIGINT CHECK(lifetime_limit>=0),
 spent BIGINT NOT NULL DEFAULT 0 CHECK(spent>=0), held BIGINT NOT NULL DEFAULT 0 CHECK(held>=0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,owner_id) REFERENCES memberships(tenant_id,user_id),
 CHECK(lifetime_limit IS NULL OR (spent::numeric+held::numeric)<=lifetime_limit)
);
CREATE INDEX api_keys_tenant ON api_keys(tenant_id,created_at DESC);
CREATE TABLE channels (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, name TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true, quarantined BOOLEAN NOT NULL DEFAULT false,
 current_version TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE channel_credentials (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, channel_id TEXT NOT NULL REFERENCES channels(id),
 secret_ciphertext TEXT NOT NULL, revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE channel_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, channel_id TEXT NOT NULL REFERENCES channels(id),
 base_url TEXT NOT NULL, provider TEXT NOT NULL DEFAULT 'openai', region TEXT NOT NULL DEFAULT 'global',
 allowed_cidrs TEXT[] NOT NULL DEFAULT '{}', credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE channels ADD CONSTRAINT channels_current_version FOREIGN KEY(current_version) REFERENCES channel_versions(id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE route_policy_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, model_id TEXT NOT NULL REFERENCES models(id), revision BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(model_id,revision)
);
CREATE TABLE model_bindings (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, route_policy_version_id TEXT NOT NULL REFERENCES route_policy_versions(id),
 channel_id TEXT NOT NULL REFERENCES channels(id), channel_version_id TEXT NOT NULL REFERENCES channel_versions(id),
 upstream_model TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 0,
 weight INTEGER NOT NULL CHECK(weight>0 AND weight<=1000000), max_inflight INTEGER NOT NULL CHECK(max_inflight>0 AND max_inflight<=1000),
 UNIQUE(route_policy_version_id,channel_id,upstream_model)
);
CREATE TABLE price_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, model_id TEXT NOT NULL REFERENCES models(id),
 currency TEXT NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 input_rate BIGINT NOT NULL CHECK(input_rate>=0), output_rate BIGINT NOT NULL CHECK(output_rate>=0),
 effective_at TIMESTAMPTZ NOT NULL, created_by TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(model_id,effective_at)
);
CREATE TABLE cost_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, channel_id TEXT NOT NULL REFERENCES channels(id), upstream_model TEXT NOT NULL,
 input_rate BIGINT CHECK(input_rate>=0), output_rate BIGINT CHECK(output_rate>=0),
 basis TEXT NOT NULL DEFAULT 'unknown' CHECK(basis IN ('contract','invoice','unknown')),
 effective_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE audit_events (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, actor TEXT NOT NULL,
 tenant_scope TEXT, action TEXT NOT NULL, target TEXT NOT NULL,
 sanitized_diff JSONB NOT NULL DEFAULT '{}', request_id TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX audit_events_time ON audit_events(created_at DESC,id);
CREATE FUNCTION reject_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable history'; END $$;
CREATE TRIGGER immutable_price BEFORE UPDATE OR DELETE ON price_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_capability BEFORE UPDATE OR DELETE ON model_capability_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_channel_version BEFORE UPDATE OR DELETE ON channel_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_route_version BEFORE UPDATE OR DELETE ON route_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_binding BEFORE UPDATE OR DELETE ON model_bindings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_audit BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TRIGGER immutable_cost_version BEFORE UPDATE OR DELETE ON cost_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
-- END 001_core.sql
-- BEGIN 002_billing.sql
CREATE TABLE ledger_accounts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 tenant_id TEXT REFERENCES tenants(id),
 kind TEXT NOT NULL CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE')),
 balance BIGINT NOT NULL DEFAULT 0,
 CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')))
);
CREATE UNIQUE INDEX ledger_customer_account ON ledger_accounts(tenant_id,kind) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX ledger_platform_account ON ledger_accounts(kind) WHERE tenant_id IS NULL;
CREATE TABLE journals (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL REFERENCES tenants(id),
 operation_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, reference TEXT NOT NULL, actor TEXT NOT NULL,
 currency TEXT NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'), created_txid BIGINT NOT NULL DEFAULT txid_current(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE journal_lines (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, journal_id TEXT NOT NULL REFERENCES journals(id),
 account_id TEXT NOT NULL REFERENCES ledger_accounts(id), signed_delta BIGINT NOT NULL,
 UNIQUE(journal_id,account_id)
);
CREATE FUNCTION guard_journal_line_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM journals WHERE id=NEW.journal_id AND created_txid=txid_current()) THEN
  RAISE EXCEPTION 'cannot append to committed journal';
 END IF;
 IF EXISTS(SELECT 1 FROM ledger_accounts a JOIN journals j ON j.id=NEW.journal_id WHERE a.id=NEW.account_id AND a.tenant_id IS NOT NULL AND a.tenant_id<>j.tenant_id) THEN
  RAISE EXCEPTION 'cross-tenant journal account';
 END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION require_balanced_journal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE line_count BIGINT; total NUMERIC;
BEGIN
 SELECT count(*),coalesce(sum(signed_delta::numeric),0) INTO line_count,total FROM journal_lines WHERE journal_id=NEW.id;
 IF line_count<2 OR total<>0 THEN RAISE EXCEPTION 'journal must have balanced entries'; END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER immutable_journal BEFORE UPDATE OR DELETE ON journals FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_journal_line BEFORE UPDATE OR DELETE ON journal_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER journal_line_current_transaction BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION guard_journal_line_insert();
CREATE CONSTRAINT TRIGGER balanced_journal AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_balanced_journal();
CREATE INDEX journals_tenant_time ON journals(tenant_id,created_at DESC,id);

CREATE TABLE request_records (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL REFERENCES tenants(id),
 key_id TEXT NOT NULL, owner_id TEXT NOT NULL REFERENCES users(id), model_id TEXT NOT NULL REFERENCES models(id),
 status TEXT NOT NULL DEFAULT 'accepted', billing_status TEXT NOT NULL DEFAULT 'reserved',
 price_version_id TEXT NOT NULL REFERENCES price_versions(id), input_rate BIGINT NOT NULL CHECK(input_rate>=0), output_rate BIGINT NOT NULL CHECK(output_rate>=0),
 input_upper_bound BIGINT NOT NULL CHECK(input_upper_bound>=0), output_limit BIGINT NOT NULL CHECK(output_limit>=0),
 profile_version TEXT NOT NULL, policy_version TEXT NOT NULL, request_hmac TEXT NOT NULL, route_revision BIGINT NOT NULL DEFAULT 0 CHECK(route_revision>=0),
 request_deadline TIMESTAMPTZ NOT NULL, instance_id TEXT NOT NULL, streaming BOOLEAN NOT NULL,
 latest_finalization_id TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,key_id) REFERENCES api_keys(tenant_id,id)
);
CREATE TABLE request_idempotency (
 tenant_id TEXT NOT NULL REFERENCES tenants(id), operation TEXT NOT NULL DEFAULT 'inference', key TEXT NOT NULL,
 request_hmac TEXT NOT NULL, request_id TEXT NOT NULL REFERENCES request_records(id) DEFERRABLE INITIALLY DEFERRED,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,operation,key)
);
CREATE TRIGGER immutable_request_idempotency BEFORE UPDATE OR DELETE ON request_idempotency FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE reservations (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL,
 request_id TEXT NOT NULL UNIQUE, amount BIGINT NOT NULL CHECK(amount>=0),
 status TEXT NOT NULL DEFAULT 'reserved' CHECK(status IN ('reserved','pending_reconciliation','settled','released')),
 deadline TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id), UNIQUE(tenant_id,id)
);
CREATE INDEX reservations_unfinished ON reservations(status,deadline);
CREATE INDEX requests_tenant_time ON request_records(tenant_id,created_at DESC,id);
CREATE TABLE upstream_attempts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 1 AND 2), channel_id TEXT NOT NULL REFERENCES channels(id),
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id), credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 upstream_model TEXT NOT NULL, delivery_state TEXT NOT NULL DEFAULT 'prepared' CHECK(delivery_state IN ('prepared','sending','completed','failed','unknown')),
 cost_version_id TEXT REFERENCES cost_versions(id), cost_input_rate BIGINT, cost_output_rate BIGINT, cost_basis TEXT NOT NULL DEFAULT 'unknown',
 cost_status TEXT NOT NULL DEFAULT 'unknown', cost_amount NUMERIC(100,0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(request_id,sequence), UNIQUE(request_id,channel_id), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id)
);
CREATE TABLE metering_events (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
 attempt_id TEXT, source TEXT NOT NULL, source_event_id TEXT NOT NULL, schema_version INTEGER NOT NULL DEFAULT 1,
 normalized_usage JSONB NOT NULL, happened_at TIMESTAMPTZ NOT NULL, received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,source,source_event_id), FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id),
 FOREIGN KEY(tenant_id,attempt_id) REFERENCES upstream_attempts(tenant_id,id)
);
CREATE TABLE metering_event_conflicts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, event_id TEXT NOT NULL REFERENCES metering_events(id),
 normalized_usage JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE finalization_records (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
 event_id TEXT NOT NULL REFERENCES metering_events(id), input_digest TEXT NOT NULL, protocol_outcome TEXT NOT NULL, transport_end_reason TEXT NOT NULL,
 delivery_stage TEXT NOT NULL, content_written BOOLEAN NOT NULL, finish_reason TEXT NOT NULL DEFAULT '',
 policy_version TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id)
);
ALTER TABLE request_records ADD CONSTRAINT request_finalization_fk FOREIGN KEY(latest_finalization_id) REFERENCES finalization_records(id) DEFERRABLE INITIALLY DEFERRED;
CREATE TRIGGER immutable_metering BEFORE UPDATE OR DELETE ON metering_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_finalization BEFORE UPDATE OR DELETE ON finalization_records FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_metering_conflict BEFORE UPDATE OR DELETE ON metering_event_conflicts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE settlements (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, request_id TEXT NOT NULL UNIQUE,
 finalization_id TEXT REFERENCES finalization_records(id), charged_amount BIGINT NOT NULL CHECK(charged_amount>=0),
 raw_retail_amount NUMERIC(100,0) NOT NULL CHECK(raw_retail_amount>=0), waived_retail_amount NUMERIC(100,0) NOT NULL CHECK(waived_retail_amount>=0),
 refunded_amount BIGINT NOT NULL DEFAULT 0 CHECK(refunded_amount>=0 AND refunded_amount<=charged_amount),
 status TEXT NOT NULL CHECK(status IN ('settled','released')), reason TEXT NOT NULL, journal_id TEXT NOT NULL REFERENCES journals(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id)
);
CREATE FUNCTION guard_settlement_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable settlement'; END IF;
 IF (to_jsonb(NEW)-'refunded_amount') IS DISTINCT FROM (to_jsonb(OLD)-'refunded_amount') OR NEW.refunded_amount<OLD.refunded_amount THEN
  RAISE EXCEPTION 'immutable settlement facts';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_settlement BEFORE UPDATE OR DELETE ON settlements FOR EACH ROW EXECUTE FUNCTION guard_settlement_history();
CREATE TABLE financial_idempotency (
 tenant_id TEXT NOT NULL REFERENCES tenants(id), operation TEXT NOT NULL CHECK(operation IN ('credit','refund')),
 key TEXT NOT NULL, request_hmac TEXT NOT NULL, resource_id TEXT NOT NULL,
 created_txid BIGINT NOT NULL DEFAULT txid_current(), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,operation,key)
);
CREATE FUNCTION guard_financial_idempotency() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.created_txid<>txid_current() THEN RAISE EXCEPTION 'immutable financial idempotency'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_financial_idempotency BEFORE UPDATE OR DELETE ON financial_idempotency FOR EACH ROW EXECUTE FUNCTION guard_financial_idempotency();
CREATE TABLE credit_orders (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL REFERENCES tenants(id), business_ref TEXT NOT NULL UNIQUE,
 amount BIGINT NOT NULL CHECK(amount>0), state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved')),
 requester TEXT NOT NULL REFERENCES users(id), approver TEXT REFERENCES users(id), proof_ref TEXT NOT NULL,
 journal_id TEXT REFERENCES journals(id), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(approver IS NULL OR approver<>requester)
);
CREATE TABLE refund_orders (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, settlement_id TEXT NOT NULL,
 business_ref TEXT NOT NULL UNIQUE, amount BIGINT NOT NULL CHECK(amount>0), reason TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved')), requester TEXT NOT NULL REFERENCES users(id),
 approver TEXT REFERENCES users(id), journal_id TEXT REFERENCES journals(id), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(approver IS NULL OR approver<>requester), FOREIGN KEY(tenant_id,settlement_id) REFERENCES settlements(tenant_id,id)
);
CREATE TABLE reconciliation_cases (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, request_id TEXT NOT NULL UNIQUE,
 reason TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'open' CHECK(state IN ('open','proposed','approved','closed')),
 proposed_usage JSONB, evidence_ref TEXT, proposal_reason TEXT, requester TEXT REFERENCES users(id), approver TEXT REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id),
 CHECK(approver IS NULL OR approver<>requester)
);
CREATE TABLE recovery_jobs (
 request_id TEXT PRIMARY KEY REFERENCES request_records(id), tenant_id TEXT NOT NULL REFERENCES tenants(id),
 lease_until TIMESTAMPTZ, owner TEXT, next_run_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), attempts INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE billing_profile_blocks (
 model_id TEXT NOT NULL REFERENCES models(id), profile_version TEXT NOT NULL, reason TEXT NOT NULL,
 request_id TEXT NOT NULL REFERENCES request_records(id), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(model_id,profile_version)
);
CREATE TABLE provider_cost_corrections (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL, attempt_id TEXT NOT NULL,
 operation_id TEXT NOT NULL UNIQUE, amount BIGINT NOT NULL CHECK(amount>=0), basis TEXT NOT NULL CHECK(basis IN ('contract','invoice')),
 evidence_ref TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,attempt_id) REFERENCES upstream_attempts(tenant_id,id)
);
CREATE TRIGGER immutable_cost_correction BEFORE UPDATE OR DELETE ON provider_cost_corrections FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE projection_outbox (
 event_id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL REFERENCES tenants(id), operation_id TEXT NOT NULL,
 kind TEXT NOT NULL, schema_version INTEGER NOT NULL DEFAULT 1, sanitized_payload JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(operation_id,kind)
);
CREATE TRIGGER immutable_outbox BEFORE UPDATE OR DELETE ON projection_outbox FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE projection_receipts (
 consumer TEXT NOT NULL, event_id TEXT NOT NULL REFERENCES projection_outbox(event_id),
 processed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(consumer,event_id)
);
CREATE TABLE report_totals (
 consumer TEXT NOT NULL, tenant_id TEXT NOT NULL REFERENCES tenants(id),
 credited NUMERIC(100,0) NOT NULL DEFAULT 0, charged NUMERIC(100,0) NOT NULL DEFAULT 0, refunded NUMERIC(100,0) NOT NULL DEFAULT 0,
 released NUMERIC(100,0) NOT NULL DEFAULT 0, requests BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(consumer,tenant_id)
);

CREATE TABLE active_report_projection (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton), consumer TEXT NOT NULL,
 switched_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO active_report_projection(singleton,consumer) VALUES(true,'reports-v1');
-- END 002_billing.sql
-- BEGIN 003_operations.sql
CREATE TABLE platform_probe_attempts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, actor TEXT NOT NULL REFERENCES users(id),
 channel_id TEXT NOT NULL REFERENCES channels(id), credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id), model_id TEXT NOT NULL REFERENCES models(id),
 profile_version TEXT NOT NULL, upstream_model TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'sending' CHECK(status IN ('sending','completed','failed','unknown')),
 normalized_usage JSONB, cost_basis TEXT NOT NULL DEFAULT 'unknown', cost_amount NUMERIC(100,0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), completed_at TIMESTAMPTZ
);
CREATE TABLE delivery_observations (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL,
 request_id TEXT NOT NULL, stage TEXT NOT NULL CHECK(stage IN ('complete_local_write','write_failed','cancelled')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id)
);
CREATE INDEX delivery_observations_request ON delivery_observations(request_id,created_at DESC);
CREATE TRIGGER immutable_delivery_observation BEFORE UPDATE OR DELETE ON delivery_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
-- END 003_operations.sql
-- BEGIN 004_recovery.sql
CREATE TABLE recovery_gate (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 required BOOLEAN NOT NULL DEFAULT false,
 epoch BIGINT NOT NULL DEFAULT 0 CHECK(epoch>=0),
 proposal_id TEXT, requester TEXT REFERENCES users(id), approver TEXT REFERENCES users(id),
 evidence_ref TEXT, reason TEXT, proposed_at TIMESTAMPTZ, approved_at TIMESTAMPTZ,
 latched_at TIMESTAMPTZ,
 CHECK((proposal_id IS NULL)=(requester IS NULL)),
 CHECK(approver IS NULL OR (requester IS NOT NULL AND approver<>requester)),
 CHECK(approved_at IS NULL OR approver IS NOT NULL)
);
INSERT INTO recovery_gate(singleton,required,epoch) VALUES(true,false,0);
CREATE FUNCTION guard_recovery_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'recovery singleton cannot be deleted'; END IF;
 IF NEW.epoch<>OLD.epoch THEN
  IF NEW.epoch<>OLD.epoch+1 OR NOT NEW.required OR NEW.proposal_id IS NOT NULL OR NEW.approver IS NOT NULL THEN
   RAISE EXCEPTION 'invalid recovery epoch transition';
  END IF;
 ELSE
  IF NOT OLD.required AND NEW.required THEN RAISE EXCEPTION 'recovery latch requires a new epoch'; END IF;
  IF OLD.proposal_id IS NOT NULL AND
   (NEW.proposal_id,NEW.requester,NEW.evidence_ref,NEW.reason,NEW.proposed_at) IS DISTINCT FROM
   (OLD.proposal_id,OLD.requester,OLD.evidence_ref,OLD.reason,OLD.proposed_at) THEN
   RAISE EXCEPTION 'immutable recovery proposal';
  END IF;
  IF OLD.required AND NOT NEW.required AND (NEW.proposal_id IS NULL OR NEW.approver IS NULL OR NEW.approved_at IS NULL) THEN
   RAISE EXCEPTION 'recovery checker approval required';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER recovery_gate_guard BEFORE UPDATE OR DELETE ON recovery_gate FOR EACH ROW EXECUTE FUNCTION guard_recovery_gate();
-- END 004_recovery.sql
-- BEGIN 005_binding_isolation.sql
CREATE TABLE route_binding_isolations (
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id),
 upstream_model TEXT NOT NULL CHECK(length(upstream_model) BETWEEN 1 AND 200),
 reason TEXT NOT NULL CHECK(reason='model_configuration_error'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(channel_version_id,upstream_model)
);
CREATE TRIGGER immutable_route_binding_isolation BEFORE UPDATE OR DELETE ON route_binding_isolations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
-- END 005_binding_isolation.sql
-- BEGIN 006_route_decisions.sql
-- Additive diagnostic metadata: old attempts retain an empty explanation.
ALTER TABLE upstream_attempts ADD COLUMN route_decisions JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK(jsonb_typeof(route_decisions)='array' AND jsonb_array_length(route_decisions)<=1000 AND octet_length(route_decisions::text)<=262144);
CREATE FUNCTION protect_route_decisions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.route_decisions IS DISTINCT FROM OLD.route_decisions THEN
  RAISE EXCEPTION 'route decision snapshots are immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_route_decisions BEFORE UPDATE OF route_decisions ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION protect_route_decisions();
-- END 006_route_decisions.sql
-- BEGIN 007_model_discovery.sql
-- Directory discovery records are platform metadata, never customer metering.
ALTER TABLE channel_versions ADD CONSTRAINT channel_versions_discovery_identity UNIQUE(id,channel_id,credential_id);
ALTER TABLE channel_credentials ADD CONSTRAINT channel_credentials_discovery_identity UNIQUE(id,channel_id);
CREATE TABLE channel_model_discoveries (
 id TEXT PRIMARY KEY,
 actor TEXT NOT NULL REFERENCES users(id),
 channel_id TEXT NOT NULL REFERENCES channels(id),
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id),
 credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 format TEXT NOT NULL CHECK(format IN ('openai','anthropic','gemini')),
 bindings JSONB NOT NULL CHECK(jsonb_typeof(bindings)='array' AND jsonb_array_length(bindings)<=1000 AND octet_length(bindings::text)<=262144),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(channel_version_id,channel_id,credential_id) REFERENCES channel_versions(id,channel_id,credential_id),
 FOREIGN KEY(credential_id,channel_id) REFERENCES channel_credentials(id,channel_id)
);
CREATE INDEX channel_model_discoveries_channel_time ON channel_model_discoveries(channel_id,created_at DESC,id DESC);
CREATE TABLE channel_model_discovery_results (
 discovery_id TEXT PRIMARY KEY REFERENCES channel_model_discoveries(id),
 status TEXT NOT NULL CHECK(status IN ('completed','failed')),
 model_ids JSONB NOT NULL CHECK(jsonb_typeof(model_ids)='array' AND jsonb_array_length(model_ids)<=1000 AND octet_length(model_ids::text)<=262144),
 pages INTEGER NOT NULL CHECK(pages BETWEEN 0 AND 10),
 error_code TEXT NOT NULL DEFAULT '' CHECK(error_code='' OR error_code ~ '^[a-z][a-z0-9_]{0,79}$'),
 completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK((status='completed' AND error_code='' AND pages BETWEEN 1 AND 10) OR (status='failed' AND error_code<>'' AND pages=0 AND model_ids='[]'::jsonb))
);
CREATE TRIGGER immutable_model_discovery BEFORE UPDATE OR DELETE ON channel_model_discoveries FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_model_discovery_result BEFORE UPDATE OR DELETE ON channel_model_discovery_results FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
-- END 007_model_discovery.sql
-- BEGIN 008_credential_pools.sql
-- Freeze each version's complete credential pool. Credentials remain mutable
-- only for irreversible revocation and offline ciphertext rewrapping.
ALTER TABLE channel_versions ADD CONSTRAINT channel_versions_channel_identity UNIQUE(id,channel_id);
ALTER TABLE channel_versions ADD COLUMN credential_pool_creation_xid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE FUNCTION set_credential_pool_creation_xid() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.credential_pool_creation_xid := pg_current_xact_id();
 RETURN NEW;
END $$;
CREATE TRIGGER channel_version_pool_creation BEFORE INSERT ON channel_versions FOR EACH ROW EXECUTE FUNCTION set_credential_pool_creation_xid();
CREATE TABLE channel_version_credentials (
 channel_version_id TEXT NOT NULL,
 channel_id TEXT NOT NULL,
 credential_id TEXT NOT NULL,
 position INTEGER NOT NULL CHECK(position BETWEEN 1 AND 32),
 label TEXT NOT NULL CHECK(octet_length(label) BETWEEN 1 AND 64 AND label !~ '[[:cntrl:]]' AND label ~ '[^[:space:]]'),
 weight INTEGER NOT NULL CHECK(weight BETWEEN 1 AND 1000000),
 max_inflight INTEGER NOT NULL CHECK(max_inflight BETWEEN 1 AND 1000),
 PRIMARY KEY(channel_version_id,credential_id),
 UNIQUE(channel_version_id,position),
 UNIQUE(channel_version_id,channel_id,credential_id),
 FOREIGN KEY(channel_version_id,channel_id) REFERENCES channel_versions(id,channel_id),
 FOREIGN KEY(credential_id,channel_id) REFERENCES channel_credentials(id,channel_id)
);
CREATE INDEX channel_version_credentials_credential ON channel_version_credentials(credential_id,channel_version_id);
CREATE FUNCTION require_credential_pool_creation_transaction() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE creation_xid xid8; creation_tuple_xid xid; tuple_xid NUMERIC;
BEGIN
 SELECT credential_pool_creation_xid,xmin INTO creation_xid,creation_tuple_xid FROM channel_versions WHERE id=NEW.channel_version_id;
 IF creation_xid IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'credential pool membership must be created with its channel version';
 END IF;
 -- A matching persisted stamp alone is insufficient after a cluster restore.
 IF NOT EXISTS (
  SELECT 1 FROM pg_locks WHERE locktype='transactionid'
   AND transactionid=creation_tuple_xid AND pid=pg_backend_pid()
   AND granted AND mode='ExclusiveLock'
 ) THEN
  -- RELEASE SAVEPOINT releases the child's xid lock. A visible uncommitted
  -- child tuple still belongs to this transaction under PostgreSQL MVCC.
  -- A child is assigned after its parent; reconstruct its full xid from the
  -- parent epoch, including a child assigned immediately after xid wrap.
  tuple_xid := floor(creation_xid::text::NUMERIC/4294967296)*4294967296 + creation_tuple_xid::text::NUMERIC;
  IF tuple_xid<creation_xid::text::NUMERIC THEN tuple_xid := tuple_xid+4294967296; END IF;
  IF tuple_xid<=creation_xid::text::NUMERIC THEN
   RAISE EXCEPTION 'credential pool membership must be created with its channel version';
  END IF;
  BEGIN
   IF pg_xact_status(tuple_xid::text::xid8) IS DISTINCT FROM 'in progress' THEN
    RAISE EXCEPTION 'credential pool membership must be created with its channel version';
   END IF;
  EXCEPTION WHEN invalid_parameter_value THEN
   RAISE EXCEPTION 'credential pool membership must be created with its channel version';
  END;
 END IF;
 RETURN NEW;
END $$;
INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight)
 SELECT id,channel_id,credential_id,1,'primary',1,1000 FROM channel_versions;
CREATE TRIGGER channel_version_credential_creation BEFORE INSERT ON channel_version_credentials FOR EACH ROW EXECUTE FUNCTION require_credential_pool_creation_transaction();
ALTER TABLE channel_versions ADD CONSTRAINT channel_versions_primary_membership
 FOREIGN KEY(id,channel_id,credential_id) REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id)
 DEFERRABLE INITIALLY DEFERRED;
CREATE TRIGGER immutable_channel_version_credential BEFORE UPDATE OR DELETE ON channel_version_credentials FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

-- A directory read may use a live secondary after the primary is revoked.
-- Preserve all old records, replacing only the former primary-only identity FK.
DO $$
DECLARE old_constraint TEXT;
BEGIN
 SELECT conname INTO STRICT old_constraint FROM pg_constraint
 WHERE conrelid='channel_model_discoveries'::regclass
   AND confrelid='channel_versions'::regclass AND contype='f'
   AND array_length(conkey,1)=3;
 EXECUTE format('ALTER TABLE channel_model_discoveries DROP CONSTRAINT %I',old_constraint);
END $$;
ALTER TABLE channel_model_discoveries ADD CONSTRAINT channel_model_discoveries_pool_identity
 FOREIGN KEY(channel_version_id,channel_id,credential_id)
 REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id);
ALTER TABLE upstream_attempts ADD CONSTRAINT upstream_attempts_pool_identity
 FOREIGN KEY(channel_version_id,channel_id,credential_id)
 REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id);
ALTER TABLE platform_probe_attempts ADD CONSTRAINT platform_probe_attempts_pool_identity
 FOREIGN KEY(channel_version_id,channel_id,credential_id)
 REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id);
-- END 008_credential_pools.sql
-- BEGIN 009_channel_batch_operations.sql
-- All channel writers participate in the same monotonic management revision.
ALTER TABLE channels ADD COLUMN management_revision BIGINT NOT NULL DEFAULT 1 CHECK(management_revision>0);
CREATE FUNCTION advance_channel_management_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.management_revision := 1;
 ELSE NEW.management_revision := OLD.management_revision+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER channel_management_revision BEFORE INSERT OR UPDATE ON channels FOR EACH ROW EXECUTE FUNCTION advance_channel_management_revision();

CREATE FUNCTION valid_channel_batch_revision(value TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF value IS NULL OR value !~ '^[1-9][0-9]{0,18}$' THEN RETURN false; END IF;
 RETURN value::NUMERIC<=9223372036854775807;
END $$;
CREATE FUNCTION valid_channel_batch_state(value JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF jsonb_typeof(value) IS DISTINCT FROM 'object'
  OR NOT value ?& ARRAY['current_version','management_revision','enabled','quarantined']
  OR EXISTS(SELECT 1 FROM jsonb_object_keys(value) k WHERE k NOT IN ('current_version','management_revision','enabled','quarantined')) THEN RETURN false; END IF;
 RETURN jsonb_typeof(value->'current_version')='string'
  AND (value->>'current_version') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND jsonb_typeof(value->'management_revision')='string'
  AND valid_channel_batch_revision(value->>'management_revision')
  AND jsonb_typeof(value->'enabled')='boolean'
  AND jsonb_typeof(value->'quarantined')='boolean';
END $$;

-- A receipt stores only the fixed metadata schema, never provider payloads.
CREATE FUNCTION valid_channel_batch_items(items JSONB,operation_status TEXT,operation_action TEXT,targets INTEGER,changes INTEGER) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE item JSONB; before_state JSONB; after_state JSONB; item_status TEXT; reason TEXT;
 previous_channel TEXT := ''; channel TEXT; changed_items INTEGER := 0; blocked_items INTEGER := 0;
 metric TEXT; desired BOOLEAN := operation_action='enable';
BEGIN
 IF jsonb_typeof(items) IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 IF targets NOT BETWEEN 1 AND 100 OR jsonb_array_length(items)<>targets OR octet_length(items::text)>262144 THEN RETURN false; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(items) LOOP
  IF jsonb_typeof(item) IS DISTINCT FROM 'object'
   OR NOT item ?& ARRAY['channel_id','expected_version','expected_revision','before','live_credentials','published_bindings','older_version_bindings','status','reason']
   OR EXISTS(SELECT 1 FROM jsonb_object_keys(item) k WHERE k NOT IN ('channel_id','expected_version','expected_revision','before','after','live_credentials','published_bindings','older_version_bindings','status','reason')) THEN RETURN false; END IF;
  channel := item->>'channel_id';
  IF jsonb_typeof(item->'channel_id') IS DISTINCT FROM 'string' OR channel !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR channel<=previous_channel OR jsonb_typeof(item->'expected_version') IS DISTINCT FROM 'string'
   OR (item->>'expected_version') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR jsonb_typeof(item->'expected_revision') IS DISTINCT FROM 'string' OR NOT valid_channel_batch_revision(item->>'expected_revision') THEN RETURN false; END IF;
  previous_channel := channel;
  FOREACH metric IN ARRAY ARRAY['live_credentials','published_bindings','older_version_bindings'] LOOP
   IF jsonb_typeof(item->metric) IS DISTINCT FROM 'number' OR (item->>metric) !~ '^(0|[1-9][0-9]{0,9})$' THEN RETURN false; END IF;
   IF (item->>metric)::NUMERIC>2147483647 THEN RETURN false; END IF;
  END LOOP;
  IF (item->>'live_credentials')::INTEGER>32 OR (item->>'older_version_bindings')::INTEGER>(item->>'published_bindings')::INTEGER THEN RETURN false; END IF;
  IF jsonb_typeof(item->'status') IS DISTINCT FROM 'string' OR jsonb_typeof(item->'reason') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  before_state := item->'before'; after_state := item->'after'; item_status := item->>'status'; reason := item->>'reason';
  IF item_status='blocked' THEN
   blocked_items := blocked_items+1;
   IF operation_status<>'rejected' OR item ? 'after' OR reason NOT IN ('not_found','stale_version','stale_revision','channel_quarantined','no_live_credential') THEN RETURN false; END IF;
   IF reason='not_found' THEN
    IF before_state<>'null'::jsonb OR (item->>'live_credentials')::INTEGER<>0 OR (item->>'published_bindings')::INTEGER<>0 THEN RETURN false; END IF;
   ELSE
    IF NOT valid_channel_batch_state(before_state) THEN RETURN false; END IF;
    IF reason='stale_version' THEN
     IF before_state->>'current_version'=item->>'expected_version' THEN RETURN false; END IF;
    ELSE
     IF before_state->>'current_version'<>item->>'expected_version' THEN RETURN false; END IF;
     IF reason='stale_revision' THEN
      IF before_state->>'management_revision'=item->>'expected_revision' THEN RETURN false; END IF;
     ELSE
      IF before_state->>'management_revision'<>item->>'expected_revision' OR NOT desired THEN RETURN false; END IF;
      IF reason='channel_quarantined' AND NOT (before_state->>'quarantined')::BOOLEAN THEN RETURN false; END IF;
      IF reason='no_live_credential' AND ((before_state->>'quarantined')::BOOLEAN OR (item->>'live_credentials')::INTEGER<>0) THEN RETURN false; END IF;
     END IF;
    END IF;
   END IF;
  ELSE
   IF NOT valid_channel_batch_state(before_state) OR reason<>''
    OR before_state->>'current_version'<>item->>'expected_version'
    OR before_state->>'management_revision'<>item->>'expected_revision' THEN RETURN false; END IF;
   IF desired AND ((before_state->>'quarantined')::BOOLEAN OR (item->>'live_credentials')::INTEGER=0) THEN RETURN false; END IF;
   IF item_status='changed' THEN
    changed_items := changed_items+1;
    IF operation_status<>'applied' OR NOT valid_channel_batch_state(after_state)
     OR (before_state->>'enabled')::BOOLEAN=desired OR (after_state->>'enabled')::BOOLEAN<>desired
     OR before_state->>'current_version'<>after_state->>'current_version'
     OR before_state->>'quarantined'<>after_state->>'quarantined'
     OR (after_state->>'management_revision')::NUMERIC<>(before_state->>'management_revision')::NUMERIC+1 THEN RETURN false; END IF;
   ELSIF item_status='unchanged' THEN
    IF (before_state->>'enabled')::BOOLEAN<>desired THEN RETURN false; END IF;
    IF operation_status='applied' THEN
     IF NOT valid_channel_batch_state(after_state) OR after_state<>before_state THEN RETURN false; END IF;
    ELSIF item ? 'after' THEN RETURN false;
    END IF;
   ELSIF item_status='not_applied' THEN
    IF operation_status<>'rejected' OR item ? 'after' OR (before_state->>'enabled')::BOOLEAN=desired THEN RETURN false; END IF;
   ELSE RETURN false;
   END IF;
  END IF;
 END LOOP;
 RETURN changed_items=changes AND ((operation_status='applied' AND blocked_items=0) OR (operation_status='rejected' AND changes=0 AND blocked_items>0));
END $$;

CREATE TABLE channel_batch_operations (
 operation_id TEXT PRIMARY KEY CHECK(operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 actor TEXT NOT NULL REFERENCES users(id),
 action TEXT NOT NULL CHECK(action IN ('enable','disable')),
 input_digest TEXT NOT NULL CHECK(input_digest ~ '^[0-9a-f]{64}$'),
 status TEXT NOT NULL CHECK(status IN ('applied','rejected')),
 target_count INTEGER NOT NULL CHECK(target_count BETWEEN 1 AND 100),
 changed_count INTEGER NOT NULL CHECK(changed_count BETWEEN 0 AND target_count),
 items JSONB NOT NULL CHECK(valid_channel_batch_items(items,status,action,target_count,changed_count)),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX channel_batch_operations_time ON channel_batch_operations(created_at DESC,operation_id DESC);
CREATE TRIGGER immutable_channel_batch_operation BEFORE UPDATE OR DELETE ON channel_batch_operations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE UNIQUE INDEX channel_batch_operation_unique_audit ON audit_events(target) WHERE action IN ('channel.batch.apply','channel.batch.reject');
-- END 009_channel_batch_operations.sql
-- BEGIN 010_key_access_policy.sql
-- PostgreSQL prints IPv4-compatible IPv6 using dotted notation. Reconstruct
-- RFC 5952 hex form so database constraints agree with Go net/netip for all IPv6.
CREATE FUNCTION canonical_key_client_cidr(value TEXT) RETURNS TEXT LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE address CIDR; wire BYTEA; parts TEXT[] := '{}'; part INTEGER;
 current_start INTEGER := 0; current_length INTEGER := 0;
 best_start INTEGER := 0; best_length INTEGER := 0; position INTEGER; canonical TEXT;
BEGIN
 IF value IS NULL OR octet_length(value)>128 THEN RETURN NULL; END IF;
 address := value::CIDR;
 IF masklen(address)=0 THEN RETURN NULL; END IF;
 IF family(address)=4 THEN RETURN address::TEXT; END IF;
 wire := inet_send(address::INET);
 IF substring(wire FROM 5 FOR 12)=decode('00000000000000000000ffff','hex') THEN RETURN NULL; END IF;
 FOR position IN 0..7 LOOP
  part := get_byte(wire,4+position*2)*256+get_byte(wire,5+position*2);
  parts := array_append(parts,to_hex(part));
  IF part=0 THEN
   IF current_length=0 THEN current_start:=position+1; END IF;
   current_length:=current_length+1;
   IF current_length>best_length THEN best_start:=current_start; best_length:=current_length; END IF;
  ELSE current_length:=0;
  END IF;
 END LOOP;
 IF best_length>=2 THEN
  canonical := COALESCE(array_to_string(parts[1:best_start-1],':'),'') || '::' || COALESCE(array_to_string(parts[best_start+best_length:8],':'),'');
 ELSE canonical:=array_to_string(parts,':');
 END IF;
 RETURN canonical || '/' || masklen(address)::TEXT;
EXCEPTION WHEN OTHERS THEN RETURN NULL;
END $$;

CREATE FUNCTION valid_key_client_cidrs(items TEXT[]) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE value TEXT; canonical TEXT; previous TEXT := NULL; total INTEGER := 0;
BEGIN
 IF items IS NULL OR cardinality(items)>32 THEN RETURN false; END IF;
 IF cardinality(items)=0 THEN RETURN true; END IF;
 IF array_ndims(items)<>1 OR array_lower(items,1)<>1 THEN RETURN false; END IF;
 FOREACH value IN ARRAY items LOOP
  IF value IS NULL THEN RETURN false; END IF;
  total:=total+octet_length(value);
  IF total>4096 THEN RETURN false; END IF;
  canonical:=canonical_key_client_cidr(value);
  IF canonical IS NULL OR value<>canonical OR (previous IS NOT NULL AND (value COLLATE "C")<=(previous COLLATE "C")) THEN RETURN false; END IF;
  previous:=value;
 END LOOP;
 RETURN true;
END $$;

ALTER TABLE api_keys
 ADD COLUMN allowed_client_cidrs TEXT[] NOT NULL DEFAULT '{}' CHECK(valid_key_client_cidrs(allowed_client_cidrs)),
 ADD COLUMN access_policy_revision BIGINT NOT NULL DEFAULT 1 CHECK(access_policy_revision>0);

-- Financial counters and unrelated attributes never invalidate policy editors.
-- Every writer gets the same monotonic revision; caller supplied values are ignored.
CREATE FUNCTION advance_key_access_policy_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.access_policy_revision:=1;
 ELSIF NEW.allowed_client_cidrs IS DISTINCT FROM OLD.allowed_client_cidrs THEN NEW.access_policy_revision:=OLD.access_policy_revision+1;
 ELSE NEW.access_policy_revision:=OLD.access_policy_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_access_policy_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_access_policy_revision();

ALTER TABLE request_records ADD COLUMN client_ip INET;
CREATE FUNCTION preserve_request_client_ip() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.client_ip IS DISTINCT FROM OLD.client_ip THEN RAISE EXCEPTION 'request source IP is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_request_client_ip BEFORE UPDATE OF client_ip ON request_records FOR EACH ROW EXECUTE FUNCTION preserve_request_client_ip();
-- END 010_key_access_policy.sql
-- BEGIN 011_key_runtime_limits.sql
ALTER TABLE api_keys
 ADD COLUMN rpm_limit INTEGER NOT NULL DEFAULT 60 CHECK(rpm_limit BETWEEN 1 AND 6000),
 ADD COLUMN burst_limit INTEGER NOT NULL DEFAULT 20 CHECK(burst_limit BETWEEN 1 AND 200),
 ADD COLUMN concurrency_limit INTEGER NOT NULL DEFAULT 16 CHECK(concurrency_limit BETWEEN 1 AND 16),
 ADD COLUMN limit_revision BIGINT NOT NULL DEFAULT 1 CHECK(limit_revision>0);

-- Runtime admission and source restrictions have independent edit revisions.
-- Counters, unrelated key attributes and caller supplied revisions cannot reset
-- or advance this revision; every actual runtime tuple change advances once.
CREATE FUNCTION advance_key_runtime_limit_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.limit_revision:=1;
 ELSIF ROW(NEW.rpm_limit,NEW.burst_limit,NEW.concurrency_limit) IS DISTINCT FROM ROW(OLD.rpm_limit,OLD.burst_limit,OLD.concurrency_limit) THEN NEW.limit_revision:=OLD.limit_revision+1;
 ELSE NEW.limit_revision:=OLD.limit_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_runtime_limits_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_runtime_limit_revision();
-- END 011_key_runtime_limits.sql
-- BEGIN 012_key_configuration.sql
ALTER TABLE api_keys
 ADD COLUMN configuration_revision BIGINT NOT NULL DEFAULT 1 CHECK(configuration_revision>0);

-- This edit lane is independent of source/runtime policy and financial usage.
-- Existing restrictions remain byte-for-byte intact until explicitly edited.
CREATE FUNCTION advance_key_configuration_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.configuration_revision:=1;
 ELSIF ROW(NEW.name,NEW.scopes,NEW.allowed_regions,NEW.lifetime_limit,NEW.expires_at) IS DISTINCT FROM ROW(OLD.name,OLD.scopes,OLD.allowed_regions,OLD.lifetime_limit,OLD.expires_at) THEN NEW.configuration_revision:=OLD.configuration_revision+1;
 ELSE NEW.configuration_revision:=OLD.configuration_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_configuration_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_configuration_revision();
-- END 012_key_configuration.sql
-- BEGIN 013_cost_aware_routing.sql
ALTER TABLE route_policy_versions
 ADD COLUMN cost_mode TEXT NOT NULL DEFAULT 'manual' CHECK(cost_mode IN ('manual','cost_first')),
 ADD COLUMN unknown_cost_policy TEXT NOT NULL DEFAULT 'allow' CHECK(unknown_cost_policy IN ('allow','deny')),
 ADD CONSTRAINT cost_first_requires_known_cost CHECK(cost_mode<>'cost_first' OR unknown_cost_policy='deny');
ALTER TABLE model_bindings
 ADD COLUMN max_input_cost_rate BIGINT CHECK(max_input_cost_rate>=0),
 ADD COLUMN max_output_cost_rate BIGINT CHECK(max_output_cost_rate>=0);

-- Cost publishers synchronize with billing's channel SHARE locks. A raw INSERT
-- cannot bypass this barrier or the channel's existing management CAS revision.
-- Existing duplicate effective instants remain readable in deterministic order.
CREATE FUNCTION synchronize_cost_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE channels SET id=id WHERE id=NEW.channel_id;
 IF EXISTS(SELECT 1 FROM cost_versions WHERE channel_id=NEW.channel_id AND upstream_model=NEW.upstream_model AND effective_at=NEW.effective_at) THEN
  RAISE EXCEPTION 'cost version effective instant already exists' USING ERRCODE='23505',CONSTRAINT='cost_version_effective_instant';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER synchronize_cost_version BEFORE INSERT ON cost_versions FOR EACH ROW EXECUTE FUNCTION synchronize_cost_version_insert();
CREATE INDEX cost_versions_current_quote ON cost_versions(channel_id,upstream_model,effective_at DESC,created_at DESC,id DESC);
-- END 013_cost_aware_routing.sql
-- BEGIN 014_procurement_accounts.sql
CREATE TABLE procurement_accounts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 label TEXT NOT NULL CHECK(octet_length(label) BETWEEN 1 AND 100 AND label !~ '[[:cntrl:]]' AND label ~ '[^[:space:]]'),
 provider TEXT NOT NULL CHECK(octet_length(provider) BETWEEN 1 AND 100 AND provider !~ '[[:cntrl:]]' AND provider ~ '[^[:space:]]'),
 state TEXT NOT NULL CHECK(state IN ('active','paused')),
 limit_amount BIGINT NOT NULL CHECK(limit_amount>=0),
 held_amount NUMERIC(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount NUMERIC(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION procurement_account_configuration_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.revision := 1;
 ELSE
  IF NEW.id<>OLD.id OR NEW.created_at<>OLD.created_at THEN RAISE EXCEPTION 'procurement account identity is immutable'; END IF;
  IF (NEW.label,NEW.provider,NEW.state,NEW.limit_amount) IS DISTINCT FROM (OLD.label,OLD.provider,OLD.state,OLD.limit_amount) THEN NEW.revision := OLD.revision+1;
  ELSE NEW.revision := OLD.revision; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER procurement_account_revision BEFORE INSERT OR UPDATE ON procurement_accounts FOR EACH ROW EXECUTE FUNCTION procurement_account_configuration_revision();
CREATE TRIGGER procurement_account_no_delete BEFORE DELETE ON procurement_accounts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE credential_procurement_bindings (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 credential_id TEXT NOT NULL UNIQUE,
 channel_id TEXT NOT NULL,
 account_id TEXT NOT NULL REFERENCES procurement_accounts(id),
 operation_id TEXT NOT NULL UNIQUE CHECK(operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 bound_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(credential_id,channel_id) REFERENCES channel_credentials(id,channel_id),
 UNIQUE(id,credential_id,channel_id,account_id),
 UNIQUE(id,account_id)
);
CREATE FUNCTION synchronize_procurement_binding_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE credential_channel TEXT;
BEGIN
 -- This is also the barrier for raw SQL and for an initially absent binding.
 -- Never start from the account and return to an earlier channel lock.
 UPDATE channels SET id=id WHERE id=NEW.channel_id;
 SELECT channel_id INTO credential_channel FROM channel_credentials WHERE id=NEW.credential_id FOR SHARE;
 IF credential_channel IS DISTINCT FROM NEW.channel_id THEN RAISE EXCEPTION 'procurement binding credential identity mismatch' USING ERRCODE='23503'; END IF;
 PERFORM id FROM procurement_accounts WHERE id=NEW.account_id FOR NO KEY UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement account not found' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER procurement_binding_synchronize BEFORE INSERT ON credential_procurement_bindings FOR EACH ROW EXECUTE FUNCTION synchronize_procurement_binding_insert();
CREATE TRIGGER immutable_procurement_binding BEFORE UPDATE OR DELETE ON credential_procurement_bindings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE procurement_reservations (
 attempt_id TEXT PRIMARY KEY REFERENCES upstream_attempts(id),
 account_id TEXT NOT NULL REFERENCES procurement_accounts(id),
 binding_id TEXT NOT NULL,
 input_bound BIGINT NOT NULL CHECK(input_bound>0),
 output_bound BIGINT NOT NULL CHECK(output_bound>=0),
 upper_amount NUMERIC(100,0) NOT NULL CHECK(upper_amount>=0 AND upper_amount<=9223372036854775807),
 held_amount NUMERIC(100,0) NOT NULL CHECK(held_amount>=0),
 spent_amount NUMERIC(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 state TEXT NOT NULL CHECK(state IN ('prepared','sending','unknown','settled','released','disputed')),
 anomaly BOOLEAN NOT NULL DEFAULT false,
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(binding_id,account_id) REFERENCES credential_procurement_bindings(id,account_id),
 UNIQUE(attempt_id,account_id),
 CHECK(state NOT IN ('prepared','sending') OR (held_amount=upper_amount AND spent_amount=0 AND NOT anomaly)),
 CHECK(state NOT IN ('settled','released') OR held_amount=0),
 CHECK(state<>'released' OR spent_amount=0)
);
CREATE INDEX procurement_reservations_account ON procurement_reservations(account_id,created_at DESC,attempt_id DESC);
CREATE INDEX procurement_reservations_recovery ON procurement_reservations(state,updated_at) WHERE state IN ('prepared','sending');
CREATE FUNCTION guard_procurement_reservation_identity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual_credential TEXT; actual_channel TEXT; bound_credential TEXT; bound_channel TEXT;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.attempt_id,NEW.account_id,NEW.binding_id,NEW.input_bound,NEW.output_bound,NEW.upper_amount,NEW.created_at)
   IS DISTINCT FROM (OLD.attempt_id,OLD.account_id,OLD.binding_id,OLD.input_bound,OLD.output_bound,OLD.upper_amount,OLD.created_at) THEN
   RAISE EXCEPTION 'procurement reservation identity and bounds are immutable';
  END IF;
  IF NEW.state<>OLD.state AND NOT (
   (OLD.state='prepared' AND NEW.state IN ('sending','released','unknown','settled','disputed')) OR
   (OLD.state='sending' AND NEW.state IN ('unknown','settled','released','disputed')) OR
   (OLD.state='unknown' AND NEW.state IN ('settled','released','disputed')) OR
   (OLD.state='settled' AND NEW.state='disputed') OR
   (OLD.state='released' AND NEW.state IN ('disputed','settled')) OR
   (OLD.state='disputed' AND NEW.state='settled')
  ) THEN RAISE EXCEPTION 'invalid procurement reservation state transition'; END IF;
  IF (NEW.state,NEW.held_amount,NEW.spent_amount,NEW.anomaly) IS DISTINCT FROM (OLD.state,OLD.held_amount,OLD.spent_amount,OLD.anomaly) THEN NEW.revision := OLD.revision+1; ELSE NEW.revision := OLD.revision; END IF;
  RETURN NEW;
 END IF;
 NEW.revision := 1;
 SELECT credential_id,channel_id INTO actual_credential,actual_channel FROM upstream_attempts WHERE id=NEW.attempt_id;
 SELECT credential_id,channel_id INTO bound_credential,bound_channel FROM credential_procurement_bindings WHERE id=NEW.binding_id AND account_id=NEW.account_id;
 IF actual_credential IS NULL OR bound_credential IS NULL OR (actual_credential,actual_channel) IS DISTINCT FROM (bound_credential,bound_channel) THEN
  RAISE EXCEPTION 'procurement reservation attempt binding mismatch' USING ERRCODE='23503';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER procurement_reservation_identity BEFORE INSERT OR UPDATE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION guard_procurement_reservation_identity();
CREATE TRIGGER procurement_reservation_no_delete BEFORE DELETE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE procurement_budget_events (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 event_key TEXT NOT NULL UNIQUE CHECK(octet_length(event_key) BETWEEN 1 AND 400),
 account_id TEXT NOT NULL REFERENCES procurement_accounts(id),
 attempt_id TEXT NOT NULL,
 held_delta NUMERIC(100,0) NOT NULL,
 spent_delta NUMERIC(100,0) NOT NULL,
 type TEXT NOT NULL CHECK(octet_length(type) BETWEEN 1 AND 80 AND type !~ '[[:cntrl:]]'),
 evidence_ref TEXT NOT NULL DEFAULT '' CHECK(octet_length(evidence_ref)<=256 AND evidence_ref !~ '[[:cntrl:]]'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(attempt_id,account_id) REFERENCES procurement_reservations(attempt_id,account_id)
);
CREATE INDEX procurement_budget_events_account ON procurement_budget_events(account_id,created_at DESC,id DESC);
CREATE TRIGGER immutable_procurement_budget_event BEFORE UPDATE OR DELETE ON procurement_budget_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE procurement_evidence_identities (
 attempt_id TEXT NOT NULL REFERENCES procurement_reservations(attempt_id),
 evidence_digest TEXT NOT NULL CHECK(evidence_digest ~ '^[0-9a-f]{64}$'),
 event_id TEXT NOT NULL CHECK(event_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(attempt_id,evidence_digest)
);
CREATE TRIGGER immutable_procurement_evidence_identity BEFORE UPDATE OR DELETE ON procurement_evidence_identities FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

-- Deferred checks allow attempt/reservation/event/balance updates in one Tx,
-- while rejecting a committed balance without its immutable event history.
CREATE FUNCTION assert_procurement_budget_projection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target TEXT; actual_held NUMERIC; actual_spent NUMERIC; event_held NUMERIC; event_spent NUMERIC; reserved_held NUMERIC; reserved_spent NUMERIC;
BEGIN
 IF TG_TABLE_NAME='procurement_accounts' THEN target := NEW.id; ELSE target := NEW.account_id; END IF;
 SELECT held_amount,spent_amount INTO actual_held,actual_spent FROM procurement_accounts WHERE id=target;
 SELECT coalesce(sum(held_delta),0),coalesce(sum(spent_delta),0) INTO event_held,event_spent FROM procurement_budget_events WHERE account_id=target;
 SELECT coalesce(sum(held_amount),0),coalesce(sum(spent_amount),0) INTO reserved_held,reserved_spent FROM procurement_reservations WHERE account_id=target;
 IF actual_held IS DISTINCT FROM event_held OR actual_spent IS DISTINCT FROM event_spent OR actual_held IS DISTINCT FROM reserved_held OR actual_spent IS DISTINCT FROM reserved_spent THEN
  RAISE EXCEPTION 'procurement budget projection does not match immutable events and reservations' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER procurement_account_budget_balance AFTER INSERT OR UPDATE ON procurement_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_procurement_budget_projection();
CREATE CONSTRAINT TRIGGER procurement_event_budget_balance AFTER INSERT ON procurement_budget_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_procurement_budget_projection();
CREATE CONSTRAINT TRIGGER procurement_reservation_budget_balance AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_procurement_budget_projection();

CREATE FUNCTION valid_procurement_command_snapshot(operation_kind TEXT,snapshot JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE allowed TEXT[]; entry RECORD;
BEGIN
 IF snapshot='null'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(snapshot) IS DISTINCT FROM 'object' OR octet_length(snapshot::text)>16384 THEN RETURN false; END IF;
 IF operation_kind IN ('account_create','account_configure') THEN allowed := ARRAY['id','label','provider','state','limit_amount','held_amount','spent_amount','revision','created_at'];
 ELSIF operation_kind='credential_bind' THEN allowed := ARRAY['id','credential_id','channel_id','account_id','operation_id','bound_at','channel_revision'];
 ELSIF operation_kind='balance_observe' THEN allowed := ARRAY['id','account_id','operation_id','actor','status','amount','currency','source','semantic','observed_at','provider_as_of','evidence_ref','created_at'];
 ELSE RETURN false; END IF;
 IF NOT snapshot ?& allowed OR EXISTS(SELECT 1 FROM jsonb_object_keys(snapshot) k WHERE NOT k=ANY(allowed)) THEN RETURN false; END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(snapshot) LOOP
  IF operation_kind='balance_observe' AND entry.key IN ('amount','provider_as_of') AND entry.value='null'::jsonb THEN CONTINUE; END IF;
  IF jsonb_typeof(entry.value) IS DISTINCT FROM 'string' OR octet_length(entry.value#>>'{}')>1000 OR (entry.value#>>'{}') ~ '[[:cntrl:]]' THEN RETURN false; END IF;
 END LOOP;
 IF operation_kind IN ('account_create','account_configure') THEN
  RETURN snapshot->>'state' IN ('active','paused') AND valid_channel_batch_revision(snapshot->>'revision')
   AND snapshot->>'limit_amount' ~ '^(0|[1-9][0-9]{0,18})$' AND (snapshot->>'limit_amount')::numeric<=9223372036854775807
   AND snapshot->>'held_amount' ~ '^(0|[1-9][0-9]{0,99})$' AND snapshot->>'spent_amount' ~ '^(0|[1-9][0-9]{0,99})$';
 ELSIF operation_kind='credential_bind' THEN RETURN valid_channel_batch_revision(snapshot->>'channel_revision');
 ELSE RETURN snapshot->>'currency'='CNY' AND snapshot->>'status' IN ('known','unknown','unsupported','error')
  AND ((snapshot->>'status'='known' AND jsonb_typeof(snapshot->'amount')='string' AND snapshot->>'amount' ~ '^(0|[1-9][0-9]{0,18})$' AND (snapshot->>'amount')::numeric<=9223372036854775807)
   OR (snapshot->>'status'<>'known' AND snapshot->'amount'='null'::jsonb));
 END IF;
END $$;
CREATE TABLE procurement_commands (
 operation_id TEXT PRIMARY KEY CHECK(operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 actor TEXT NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('account_create','account_configure','credential_bind','balance_observe')),
 target_id TEXT NOT NULL,
 input_digest TEXT NOT NULL CHECK(input_digest ~ '^[0-9a-f]{64}$'),
 status TEXT NOT NULL CHECK(status IN ('applied','rejected')),
 reason TEXT NOT NULL CHECK(reason IN ('','not_found','revision_conflict','already_bound','credential_revoked')),
 before_state JSONB NOT NULL,
 after_state JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(valid_procurement_command_snapshot(kind,before_state) AND valid_procurement_command_snapshot(kind,after_state)),
 CHECK((status='applied' AND reason='' AND jsonb_typeof(after_state)='object') OR (status='rejected' AND reason<>'' AND after_state='null'::jsonb))
);
CREATE INDEX procurement_commands_time ON procurement_commands(created_at DESC,operation_id DESC);
CREATE TRIGGER immutable_procurement_command BEFORE UPDATE OR DELETE ON procurement_commands FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE UNIQUE INDEX procurement_command_unique_audit ON audit_events(target) WHERE action IN ('procurement.command.applied','procurement.command.rejected');

CREATE TABLE procurement_cost_proposals (
 id TEXT PRIMARY KEY CHECK(id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 tenant_id TEXT NOT NULL,
 attempt_id TEXT NOT NULL,
 amount BIGINT NOT NULL CHECK(amount>=0),
 basis TEXT NOT NULL CHECK(basis IN ('contract','invoice')),
 evidence_ref TEXT NOT NULL CHECK(octet_length(evidence_ref) BETWEEN 1 AND 256 AND evidence_ref !~ '[[:cntrl:]]'),
 reason TEXT NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 400 AND reason !~ '[[:cntrl:]]'),
 requester TEXT NOT NULL REFERENCES users(id),
 expected_reservation_revision BIGINT NOT NULL CHECK(expected_reservation_revision>0),
 expected_evidence_count BIGINT NOT NULL CHECK(expected_evidence_count>=0),
 state TEXT NOT NULL DEFAULT 'proposed' CHECK(state IN ('proposed','approved')),
 approver TEXT REFERENCES users(id),
 approval_operation_id TEXT UNIQUE CHECK(approval_operation_id IS NULL OR approval_operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 approved_at TIMESTAMPTZ,
 FOREIGN KEY(tenant_id,attempt_id) REFERENCES upstream_attempts(tenant_id,id),
 FOREIGN KEY(attempt_id) REFERENCES procurement_reservations(attempt_id),
 CHECK((state='proposed' AND approver IS NULL AND approval_operation_id IS NULL AND approved_at IS NULL) OR (state='approved' AND approver IS NOT NULL AND approver<>requester AND approval_operation_id IS NOT NULL AND approval_operation_id<>id AND approved_at IS NOT NULL))
);
CREATE FUNCTION guard_procurement_cost_proposal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state<>'proposed' OR NEW.state<>'approved' OR
  (NEW.id,NEW.tenant_id,NEW.attempt_id,NEW.amount,NEW.basis,NEW.evidence_ref,NEW.reason,NEW.requester,NEW.expected_reservation_revision,NEW.expected_evidence_count,NEW.created_at)
  IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.attempt_id,OLD.amount,OLD.basis,OLD.evidence_ref,OLD.reason,OLD.requester,OLD.expected_reservation_revision,OLD.expected_evidence_count,OLD.created_at) THEN
  RAISE EXCEPTION 'procurement cost proposal only permits one immutable approval';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER procurement_cost_proposal_transition BEFORE UPDATE ON procurement_cost_proposals FOR EACH ROW EXECUTE FUNCTION guard_procurement_cost_proposal();
CREATE TRIGGER procurement_cost_proposal_no_delete BEFORE DELETE ON procurement_cost_proposals FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE procurement_balance_observations (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 account_id TEXT NOT NULL REFERENCES procurement_accounts(id),
 operation_id TEXT NOT NULL UNIQUE,
 actor TEXT NOT NULL REFERENCES users(id),
 status TEXT NOT NULL CHECK(status IN ('known','unknown','unsupported','error')),
 amount BIGINT CHECK(amount>=0),
 currency TEXT NOT NULL CHECK(currency='CNY'),
 source TEXT NOT NULL CHECK(octet_length(source) BETWEEN 1 AND 100 AND source !~ '[[:cntrl:]]'),
 semantic TEXT NOT NULL CHECK(octet_length(semantic) BETWEEN 1 AND 100 AND semantic !~ '[[:cntrl:]]'),
 observed_at TIMESTAMPTZ NOT NULL,
 provider_as_of TIMESTAMPTZ,
 evidence_ref TEXT NOT NULL CHECK(octet_length(evidence_ref) BETWEEN 1 AND 256 AND evidence_ref !~ '[[:cntrl:]]'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK((status='known' AND amount IS NOT NULL) OR (status<>'known' AND amount IS NULL))
);
CREATE INDEX procurement_observations_account ON procurement_balance_observations(account_id,created_at DESC,id DESC);
CREATE TRIGGER immutable_procurement_observation BEFORE UPDATE OR DELETE ON procurement_balance_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
-- END 014_procurement_accounts.sql
-- BEGIN 015_public_catalog.sql
ALTER TABLE models
 ADD COLUMN public_visible BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN public_revision BIGINT NOT NULL DEFAULT 1 CHECK(public_revision>0);

CREATE FUNCTION guard_model_public_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  NEW.public_revision := 1;
 ELSIF NEW.public_visible IS DISTINCT FROM OLD.public_visible THEN
  NEW.public_revision := OLD.public_revision+1;
 ELSE
  NEW.public_revision := OLD.public_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER model_public_revision BEFORE INSERT OR UPDATE ON models
 FOR EACH ROW EXECUTE FUNCTION guard_model_public_revision();

CREATE INDEX models_public_catalog ON models(public_name COLLATE "C",id COLLATE "C")
 WHERE public_visible AND enabled;
-- END 015_public_catalog.sql
-- BEGIN 016_self_service_identity.sql
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN password_revision BIGINT NOT NULL DEFAULT 1 CHECK(password_revision>0);
CREATE FUNCTION identity_password_revision_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.password_revision:=1;
 ELSIF NEW.password_hash IS DISTINCT FROM OLD.password_hash THEN NEW.password_revision:=OLD.password_revision+1;
 ELSE NEW.password_revision:=OLD.password_revision; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_password_revision BEFORE INSERT OR UPDATE ON users FOR EACH ROW EXECUTE FUNCTION identity_password_revision_guard();

CREATE TABLE identity_initialization (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 initialized_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 initialized_by TEXT NOT NULL REFERENCES users(id)
);
CREATE FUNCTION identity_initialization_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'identity initialization immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initialized_by AND platform_role='admin') THEN RAISE EXCEPTION 'identity initialization requires admin'; END IF;
 IF current_user<>(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND
   (NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initialized_by AND status='active') OR (SELECT count(*) FROM users)<>1) THEN
   RAISE EXCEPTION 'identity initialization requires bootstrap';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_initialization_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_initialization FOR EACH ROW EXECUTE FUNCTION identity_initialization_guard();
INSERT INTO identity_initialization(initialized_by) SELECT id FROM users WHERE platform_role='admin' ORDER BY created_at,id LIMIT 1;

CREATE TABLE identity_mail_subjects (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 purpose TEXT NOT NULL CHECK(purpose IN ('registration','password_reset')),
 email_digest TEXT NOT NULL CHECK(email_digest ~ '^[0-9a-f]{64}$'),
 window_started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 window_count INTEGER NOT NULL DEFAULT 0 CHECK(window_count BETWEEN 0 AND 5),
 next_issue_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 last_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(purpose,email_digest), UNIQUE(id,purpose)
);
CREATE TABLE identity_challenges (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 subject_id UUID NOT NULL,
 purpose TEXT NOT NULL CHECK(purpose IN ('registration','password_reset')),
 token_digest TEXT NOT NULL UNIQUE CHECK(token_digest ~ '^[0-9a-f]{64}$'),
 user_id TEXT REFERENCES users(id),
 password_revision BIGINT CHECK(password_revision>0),
 registration_email_ciphertext TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ,
 revoked_at TIMESTAMPTZ,
 FOREIGN KEY(subject_id,purpose) REFERENCES identity_mail_subjects(id,purpose),
 CHECK(expires_at>created_at), CHECK(consumed_at IS NULL OR revoked_at IS NULL),
 CHECK((purpose='registration' AND user_id IS NULL AND password_revision IS NULL) OR
       (purpose='password_reset' AND user_id IS NOT NULL AND password_revision IS NOT NULL AND registration_email_ciphertext IS NULL))
);
CREATE UNIQUE INDEX identity_challenges_active ON identity_challenges(subject_id) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX identity_challenges_expiry ON identity_challenges(expires_at,id);
CREATE TABLE identity_mail_outbox (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 challenge_id UUID NOT NULL UNIQUE REFERENCES identity_challenges(id),
 payload_ciphertext TEXT,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','dead','cancelled')),
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 5),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 claim_generation BIGINT NOT NULL DEFAULT 0 CHECK(claim_generation=attempt_count),
 lease_until TIMESTAMPTZ,
 sent_at TIMESTAMPTZ,
 last_error_code TEXT NOT NULL DEFAULT '' CHECK(last_error_code IN ('','mail_configuration','mail_payload_invalid','mail_rejected','mail_temporary','mail_network','mail_tls','mail_tls_required','mail_decrypt','mail_attempts_exhausted','mail_proof_inactive')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK((state IN ('pending','sending'))=(payload_ciphertext IS NOT NULL)),
 CHECK((state='sending')=(lease_until IS NOT NULL)),
 CHECK((state='sent')=(sent_at IS NOT NULL)),
 CHECK(state<>'pending' OR attempt_count<5),
 CHECK(state NOT IN ('sending','sent') OR attempt_count>0)
);
CREATE INDEX identity_mail_outbox_pending ON identity_mail_outbox(next_attempt_at,id) WHERE state='pending';
CREATE INDEX identity_mail_outbox_sending ON identity_mail_outbox(lease_until,id) WHERE state='sending';

CREATE FUNCTION identity_subject_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE db_now TIMESTAMPTZ:=clock_timestamp();
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.window_count NOT IN (0,1) OR NEW.window_started_at>db_now OR NEW.last_seen_at>db_now THEN RAISE EXCEPTION 'identity subject initial state'; END IF;
  NEW.window_started_at:=db_now;
  IF NEW.window_count=1 THEN NEW.last_seen_at:=db_now; NEW.next_issue_at:=db_now+interval '60 seconds'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.last_seen_at>clock_timestamp()-interval '24 hours' OR EXISTS(SELECT 1 FROM identity_challenges WHERE subject_id=OLD.id) THEN RAISE EXCEPTION 'identity subject retention'; END IF;
  RETURN OLD;
 END IF;
 IF (NEW.id,NEW.purpose,NEW.email_digest) IS DISTINCT FROM (OLD.id,OLD.purpose,OLD.email_digest) OR NEW.last_seen_at<OLD.last_seen_at OR NEW.last_seen_at>db_now OR NEW.next_issue_at<OLD.next_issue_at THEN RAISE EXCEPTION 'identity subject immutable'; END IF;
 IF NEW.window_started_at=OLD.window_started_at THEN
  IF NEW.window_count<OLD.window_count OR NEW.window_count>OLD.window_count+1 THEN RAISE EXCEPTION 'identity subject counter'; END IF;
 ELSIF NEW.window_started_at<OLD.window_started_at+interval '1 hour' OR NEW.window_started_at>db_now OR db_now<OLD.window_started_at+interval '1 hour' OR NEW.window_count<>1 THEN RAISE EXCEPTION 'identity subject window'; END IF;
 IF NEW.window_count>OLD.window_count OR NEW.window_started_at<>OLD.window_started_at THEN
  IF OLD.next_issue_at>db_now THEN RAISE EXCEPTION 'identity subject cooldown'; END IF;
  IF NEW.window_started_at<>OLD.window_started_at THEN NEW.window_started_at:=db_now; END IF;
  NEW.last_seen_at:=db_now; NEW.next_issue_at:=db_now+interval '60 seconds';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_subject_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_mail_subjects FOR EACH ROW EXECUTE FUNCTION identity_subject_guard();

CREATE FUNCTION identity_challenge_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.consumed_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR (NEW.purpose='registration' AND NEW.registration_email_ciphertext IS NULL) THEN RAISE EXCEPTION 'identity challenge initial state'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.created_at>clock_timestamp()-interval '7 days' OR (OLD.consumed_at IS NULL AND OLD.revoked_at IS NULL AND OLD.expires_at>clock_timestamp()) THEN RAISE EXCEPTION 'identity challenge retention'; END IF;
  RETURN OLD;
 END IF;
 IF current_user=(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND OLD.registration_email_ciphertext IS NOT NULL AND NEW.registration_email_ciphertext IS NOT NULL AND
  (to_jsonb(NEW)-'registration_email_ciphertext')=(to_jsonb(OLD)-'registration_email_ciphertext') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.subject_id,NEW.purpose,NEW.token_digest,NEW.user_id,NEW.password_revision,NEW.created_at,NEW.expires_at) IS DISTINCT FROM
    (OLD.id,OLD.subject_id,OLD.purpose,OLD.token_digest,OLD.user_id,OLD.password_revision,OLD.created_at,OLD.expires_at) THEN RAISE EXCEPTION 'identity challenge immutable'; END IF;
 IF (OLD.consumed_at IS NOT NULL AND NEW.consumed_at IS DISTINCT FROM OLD.consumed_at) OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN RAISE EXCEPTION 'identity challenge terminal'; END IF;
 IF NEW.registration_email_ciphertext IS DISTINCT FROM OLD.registration_email_ciphertext AND
   (NEW.registration_email_ciphertext IS NOT NULL OR (NEW.consumed_at IS NULL AND NEW.revoked_at IS NULL AND NEW.expires_at>clock_timestamp())) THEN RAISE EXCEPTION 'identity challenge secret immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_challenge_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_challenges FOR EACH ROW EXECUTE FUNCTION identity_challenge_guard();

CREATE FUNCTION identity_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.attempt_count<>0 OR NEW.claim_generation<>0 OR NEW.lease_until IS NOT NULL OR NEW.sent_at IS NOT NULL OR NEW.payload_ciphertext IS NULL OR NEW.last_error_code<>'' THEN RAISE EXCEPTION 'identity outbox initial state'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.state IN ('pending','sending') OR OLD.created_at>clock_timestamp()-interval '7 days' THEN RAISE EXCEPTION 'identity outbox retention'; END IF;
  RETURN OLD;
 END IF;
 IF current_user=(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND OLD.payload_ciphertext IS NOT NULL AND NEW.payload_ciphertext IS NOT NULL AND
  (to_jsonb(NEW)-'payload_ciphertext')=(to_jsonb(OLD)-'payload_ciphertext') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.challenge_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.challenge_id,OLD.created_at) THEN RAISE EXCEPTION 'identity outbox immutable'; END IF;
 IF OLD.state IN ('sent','dead','cancelled') THEN
  IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'identity outbox terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.state='sending' AND (OLD.state='pending' OR (OLD.state='sending' AND OLD.lease_until<=clock_timestamp())) THEN
  IF NEW.attempt_count<>OLD.attempt_count+1 OR NEW.claim_generation<>OLD.claim_generation+1 OR OLD.attempt_count>=5 OR NEW.lease_until<=clock_timestamp() OR NEW.lease_until>clock_timestamp()+interval '60 seconds' OR NEW.payload_ciphertext IS DISTINCT FROM OLD.payload_ciphertext THEN RAISE EXCEPTION 'identity outbox claim'; END IF;
 ELSIF OLD.state='sending' AND NEW.state IN ('pending','sent','dead') OR NEW.state='cancelled' THEN
  IF (NEW.attempt_count,NEW.claim_generation) IS DISTINCT FROM (OLD.attempt_count,OLD.claim_generation) THEN RAISE EXCEPTION 'identity outbox counter'; END IF;
  IF NEW.state='pending' AND (NEW.payload_ciphertext IS DISTINCT FROM OLD.payload_ciphertext OR NEW.next_attempt_at<clock_timestamp()) THEN RAISE EXCEPTION 'identity outbox retry'; END IF;
  IF NEW.state='sent' AND NEW.last_error_code<>'' THEN RAISE EXCEPTION 'identity outbox sent error'; END IF;
 ELSE RAISE EXCEPTION 'identity outbox transition'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_outbox_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_mail_outbox FOR EACH ROW EXECUTE FUNCTION identity_outbox_guard();
-- END 016_self_service_identity.sql
-- BEGIN 017_funding_vouchers.sql
-- Refuse invalid legacy facts before creating any new source or projection.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM credit_orders c WHERE
  (c.state='pending' AND (c.approver IS NOT NULL OR c.journal_id IS NOT NULL
   OR EXISTS(SELECT 1 FROM journals j WHERE j.operation_id=c.id AND j.kind='credit')
   OR EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=c.id AND o.kind='credited')))
  OR (c.state='approved' AND (c.approver IS NULL OR c.approver=c.requester OR NOT EXISTS(
   SELECT 1 FROM journals j WHERE j.id=c.journal_id AND j.tenant_id=c.tenant_id AND j.operation_id=c.id
    AND j.kind='credit' AND j.reference=c.id AND j.actor=c.approver AND j.currency='CNY'
    AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
    AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=c.tenant_id AND l.signed_delta=c.amount)
    AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='FUNDING_CLEARING' AND a.tenant_id IS NULL AND l.signed_delta=-c.amount)
    AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=c.id AND o.kind='credited')=1
    AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=c.id AND o.kind='credited' AND o.tenant_id=c.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=c.amount::text)
  )))) OR EXISTS(SELECT 1 FROM journals j WHERE j.kind='voucher_grant' OR (j.kind='credit' AND NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.state='approved' AND c.journal_id=j.id)))
 OR EXISTS(SELECT 1 FROM projection_outbox o WHERE o.kind='credited' AND NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.state='approved' AND c.id=o.operation_id))
 OR EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id JOIN journals j ON j.id=l.journal_id WHERE a.kind='FUNDING_CLEARING' AND (j.kind<>'credit' OR NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.state='approved' AND c.journal_id=j.id)))
 THEN RAISE EXCEPTION 'funding legacy source verification failed'; END IF;
END $$;

ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check;
ALTER TABLE ledger_accounts ADD CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING'));
ALTER TABLE ledger_accounts ADD CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR
 (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR
 (tenant_id IS NULL AND kind='OPERATING_GRANT_CLEARING' AND balance<=0));
ALTER TABLE journals ADD UNIQUE(tenant_id,id);
ALTER TABLE credit_orders ADD UNIQUE(journal_id);
ALTER TABLE credit_orders ADD FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id);

CREATE FUNCTION funding_uuid(value TEXT) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT value ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND value<>'00000000-0000-0000-0000-000000000000'
$$;
CREATE TABLE funding_receipts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 source_kind TEXT NOT NULL CHECK(source_kind IN ('manual_credit','voucher_grant')),
 source_id TEXT NOT NULL,
 amount BIGINT NOT NULL CHECK(amount>0), currency TEXT NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 journal_id TEXT NOT NULL UNIQUE, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(source_kind,source_id), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
INSERT INTO funding_receipts(tenant_id,source_kind,source_id,amount,journal_id,created_at)
 SELECT c.tenant_id,'manual_credit',c.id,c.amount,c.journal_id,j.created_at FROM credit_orders c JOIN journals j ON j.id=c.journal_id WHERE c.state='approved';

CREATE TABLE voucher_policy(singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),enabled BOOLEAN NOT NULL DEFAULT false,revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0));
INSERT INTO voucher_policy DEFAULT VALUES;
CREATE TABLE commerce_commands (
 operation_id TEXT PRIMARY KEY CHECK(funding_uuid(operation_id)),actor TEXT NOT NULL REFERENCES users(id),
 scope TEXT NOT NULL CHECK(scope='platform' OR (left(scope,7)='tenant:' AND funding_uuid(substr(scope,8)))),
 kind TEXT NOT NULL CHECK(kind IN ('voucher.batch.create','voucher.batch.approve','voucher.batch.void','voucher.revocation.create','voucher.revocation.approve','voucher.redeem')),
 request_digest TEXT NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
 fingerprint_key_version TEXT CHECK(fingerprint_key_version ~ '^[A-Za-z0-9_-]{1,32}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(operation_id,kind,scope,actor),
 CHECK((kind='voucher.redeem')=(fingerprint_key_version IS NOT NULL)),
 CHECK((kind='voucher.redeem')=(scope<>'platform'))
);
CREATE TABLE commerce_operation_receipts (
 operation_id TEXT PRIMARY KEY,kind TEXT NOT NULL,scope TEXT NOT NULL,actor TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('accepted','rejected')),
 reason TEXT NOT NULL CHECK(reason IN ('accepted','voucher_unavailable','vouchers_disabled','voucher_revision_conflict','voucher_evidence_stale','voucher_state_conflict','funding_amount_overflow')),
 resource_id TEXT CHECK(funding_uuid(resource_id)),amount BIGINT CHECK(amount>0),
 funding_receipt_id TEXT REFERENCES funding_receipts(id),journal_id TEXT REFERENCES journals(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(operation_id,kind,scope,actor) REFERENCES commerce_commands(operation_id,kind,scope,actor),
 CHECK((status='accepted')=(reason='accepted')),
 CHECK((status='accepted' AND resource_id IS NOT NULL AND
  ((kind='voucher.redeem' AND amount IS NOT NULL AND funding_receipt_id IS NOT NULL AND journal_id IS NOT NULL) OR
   (kind<>'voucher.redeem' AND amount IS NULL AND funding_receipt_id IS NULL AND journal_id IS NULL))) OR
  (status='rejected' AND resource_id IS NULL AND amount IS NULL AND funding_receipt_id IS NULL AND journal_id IS NULL))
);
CREATE TABLE voucher_batches (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),
 state TEXT NOT NULL DEFAULT 'proposed' CHECK(state IN ('proposed','active','voided','revoked')),
 face_amount BIGINT NOT NULL CHECK(face_amount>0),count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 100),
 expires_at TIMESTAMPTZ NOT NULL,purpose_ref TEXT NOT NULL CHECK(octet_length(purpose_ref) BETWEEN 1 AND 256),
 reason TEXT NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),maker TEXT NOT NULL REFERENCES users(id),
 approver TEXT REFERENCES users(id),activated_at TIMESTAMPTZ,terminal_actor TEXT REFERENCES users(id),terminal_at TIMESTAMPTZ,
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),redeemed_count BIGINT NOT NULL DEFAULT 0 CHECK(redeemed_count>=0 AND redeemed_count<=count),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(face_amount::numeric*count<=9223372036854775807),
 CHECK(expires_at>=created_at+interval '1 hour' AND expires_at<=created_at+interval '365 days'),
 CHECK((state='proposed' AND approver IS NULL AND activated_at IS NULL AND terminal_actor IS NULL AND terminal_at IS NULL) OR
  (state='active' AND approver IS NOT NULL AND approver<>maker AND activated_at IS NOT NULL AND terminal_actor IS NULL AND terminal_at IS NULL) OR
  (state='voided' AND approver IS NULL AND activated_at IS NULL AND terminal_actor IS NOT NULL AND terminal_actor=maker AND terminal_at IS NOT NULL) OR
  (state='revoked' AND approver IS NOT NULL AND approver<>maker AND activated_at IS NOT NULL AND terminal_actor IS NOT NULL AND terminal_at IS NOT NULL))
);
CREATE TABLE vouchers (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),batch_id TEXT NOT NULL REFERENCES voucher_batches(id),
 code_digest TEXT NOT NULL CHECK(code_digest ~ '^[0-9a-f]{64}$'),digest_key_version TEXT NOT NULL CHECK(digest_key_version ~ '^[A-Za-z0-9_-]{1,32}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),UNIQUE(id,batch_id)
);
CREATE TABLE voucher_revocations (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),batch_id TEXT NOT NULL REFERENCES voucher_batches(id),
 operation_id TEXT NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),maker TEXT NOT NULL REFERENCES users(id),
 expected_revision BIGINT NOT NULL CHECK(expected_revision>0),expected_redeemed_count BIGINT NOT NULL CHECK(expected_redeemed_count>=0),
 reason TEXT NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),UNIQUE(id,batch_id)
);
CREATE TABLE voucher_revocation_decisions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),proposal_id TEXT NOT NULL UNIQUE,batch_id TEXT NOT NULL,
 operation_id TEXT NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),approver TEXT NOT NULL REFERENCES users(id),
 status TEXT NOT NULL CHECK(status IN ('accepted','rejected')),reason TEXT NOT NULL,
 decided_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(proposal_id,batch_id) REFERENCES voucher_revocations(id,batch_id),
 CHECK((status='accepted' AND reason='accepted') OR (status='rejected' AND reason='voucher_evidence_stale'))
);
CREATE TABLE voucher_redemptions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),tenant_id TEXT NOT NULL REFERENCES tenants(id),
 voucher_id TEXT NOT NULL UNIQUE,batch_id TEXT NOT NULL,
 operation_id TEXT NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),actor TEXT NOT NULL REFERENCES users(id),
 amount BIGINT NOT NULL CHECK(amount>0),journal_id TEXT NOT NULL UNIQUE,funding_receipt_id TEXT NOT NULL UNIQUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(voucher_id,batch_id) REFERENCES vouchers(id,batch_id),
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id),
 FOREIGN KEY(tenant_id,funding_receipt_id) REFERENCES funding_receipts(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX funding_receipts_page ON funding_receipts(tenant_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX voucher_batches_page ON voucher_batches(created_at DESC,id COLLATE "C" DESC);
CREATE INDEX voucher_revocations_page ON voucher_revocations(batch_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX voucher_redemptions_page ON voucher_redemptions(tenant_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX vouchers_batch ON vouchers(batch_id,id);
CREATE INDEX voucher_redemptions_batch ON voucher_redemptions(batch_id,id);
CREATE UNIQUE INDEX commerce_accepted_resource ON commerce_operation_receipts(kind,resource_id) WHERE status='accepted' AND kind<>'voucher.redeem';

CREATE FUNCTION funding_credit_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL THEN RAISE EXCEPTION 'credit initial state'; END IF;
 ELSE
  IF (NEW.id,NEW.tenant_id,NEW.business_ref,NEW.amount,NEW.requester,NEW.proof_ref,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.business_ref,OLD.amount,OLD.requester,OLD.proof_ref,OLD.created_at) OR OLD.state<>'pending' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 END IF;
 IF (NEW.state='pending' AND (NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL)) OR (NEW.state='approved' AND (NEW.approver IS NULL OR NEW.approver=NEW.requester OR NEW.journal_id IS NULL)) THEN RAISE EXCEPTION 'credit approval incomplete'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER funding_credit_guard BEFORE INSERT OR UPDATE OR DELETE ON credit_orders FOR EACH ROW EXECUTE FUNCTION funding_credit_guard();
CREATE FUNCTION voucher_policy_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable voucher policy'; END IF;
 IF TG_OP='INSERT' THEN NEW.revision:=1; ELSE NEW.revision:=OLD.revision+CASE WHEN NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER voucher_policy_guard BEFORE INSERT OR UPDATE OR DELETE ON voucher_policy FOR EACH ROW EXECUTE FUNCTION voucher_policy_guard();
CREATE FUNCTION voucher_batch_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable voucher batch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'proposed' OR NEW.redeemed_count<>0 THEN RAISE EXCEPTION 'voucher batch initial state'; END IF;NEW.revision:=1;RETURN NEW;
 END IF;
 IF (NEW.id,NEW.face_amount,NEW.count,NEW.expires_at,NEW.purpose_ref,NEW.reason,NEW.maker,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.face_amount,OLD.count,OLD.expires_at,OLD.purpose_ref,OLD.reason,OLD.maker,OLD.created_at) THEN RAISE EXCEPTION 'immutable voucher batch'; END IF;
 IF NEW.state=OLD.state THEN
  IF (NEW.approver,NEW.activated_at,NEW.terminal_actor,NEW.terminal_at) IS DISTINCT FROM (OLD.approver,OLD.activated_at,OLD.terminal_actor,OLD.terminal_at) THEN RAISE EXCEPTION 'immutable voucher approval'; END IF;
  IF NEW.redeemed_count<>OLD.redeemed_count AND (OLD.state<>'active' OR NEW.redeemed_count<>OLD.redeemed_count+1 OR NEW.redeemed_count<>(SELECT count(*) FROM voucher_redemptions WHERE batch_id=NEW.id)) THEN RAISE EXCEPTION 'voucher batch counter'; END IF;
  NEW.revision:=OLD.revision;
 ELSE
  IF NOT ((OLD.state='proposed' AND NEW.state IN ('active','voided')) OR (OLD.state='active' AND NEW.state='revoked')) OR NEW.redeemed_count<>OLD.redeemed_count THEN RAISE EXCEPTION 'voucher batch transition'; END IF;
  IF OLD.state='active' AND (NEW.approver,NEW.activated_at) IS DISTINCT FROM (OLD.approver,OLD.activated_at) THEN RAISE EXCEPTION 'immutable voucher approval'; END IF;
  NEW.revision:=OLD.revision+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER voucher_batch_guard BEFORE INSERT OR UPDATE OR DELETE ON voucher_batches FOR EACH ROW EXECUTE FUNCTION voucher_batch_guard();
CREATE FUNCTION voucher_redemption_count() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE voucher_batches SET redeemed_count=redeemed_count+1 WHERE id=NEW.batch_id;RETURN NEW;
END $$;
CREATE TRIGGER voucher_redemption_count AFTER INSERT ON voucher_redemptions FOR EACH ROW EXECUTE FUNCTION voucher_redemption_count();

CREATE FUNCTION funding_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 LEFT JOIN credit_orders c ON f.source_kind='manual_credit' AND c.id=f.source_id
 LEFT JOIN vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 LEFT JOIN voucher_batches b ON b.id=v.batch_id
 LEFT JOIN voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 LEFT JOIN commerce_commands cmd ON cmd.operation_id=r.operation_id
 LEFT JOIN commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id AND j.currency='CNY' AND f.currency='CNY' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.state='approved' AND c.journal_id=j.id AND c.tenant_id=f.tenant_id AND c.amount=f.amount AND c.approver<>c.requester AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND j.actor=c.approver)
 OR (f.source_kind='voucher_grant' AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
  AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
  AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
  AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
  AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;
CREATE FUNCTION funding_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid TEXT; credit_state TEXT;
BEGIN
 IF TG_TABLE_NAME='credit_orders' THEN
  SELECT state,journal_id INTO credit_state,jid FROM credit_orders WHERE id=NEW.id;
  IF credit_state='pending' THEN
   IF EXISTS(SELECT 1 FROM journals WHERE kind='credit' AND operation_id=NEW.id) OR EXISTS(SELECT 1 FROM projection_outbox WHERE kind='credited' AND operation_id=NEW.id) THEN RAISE EXCEPTION 'pending credit has funding'; END IF;
   RETURN NULL;
  END IF;
 ELSIF TG_TABLE_NAME='journals' THEN
  IF NEW.kind NOT IN ('credit','voucher_grant') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER funding_credit_fact AFTER INSERT OR UPDATE ON credit_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_receipt_fact AFTER INSERT ON funding_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_journal_fact AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_redemption_fact AFTER INSERT ON voucher_redemptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_outbox_fact AFTER INSERT ON projection_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();

CREATE FUNCTION voucher_batch_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE batch_ref TEXT; b voucher_batches%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='voucher_batches' THEN batch_ref:=NEW.id;ELSE batch_ref:=NEW.batch_id;END IF;
 SELECT * INTO b FROM voucher_batches WHERE id=batch_ref;
 IF b.id IS NULL OR b.count<>(SELECT count(*) FROM vouchers WHERE batch_id=batch_ref) OR b.redeemed_count<>(SELECT count(*) FROM voucher_redemptions WHERE batch_id=batch_ref) THEN RAISE EXCEPTION 'voucher batch facts incomplete'; END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.create' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.maker) THEN RAISE EXCEPTION 'voucher batch creation incomplete';END IF;
 IF b.state IN ('active','revoked') AND NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.approve' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.approver) THEN RAISE EXCEPTION 'voucher batch approval incomplete';END IF;
 IF b.state='voided' AND NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.void' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.maker) THEN RAISE EXCEPTION 'voucher batch void incomplete';END IF;
 IF b.state='revoked' AND NOT EXISTS(SELECT 1 FROM voucher_revocations p JOIN voucher_revocation_decisions d ON d.proposal_id=p.id
  JOIN commerce_commands cmd ON cmd.operation_id=d.operation_id JOIN commerce_operation_receipts terminal ON terminal.operation_id=cmd.operation_id
  WHERE p.batch_id=b.id AND d.batch_id=b.id AND d.status='accepted' AND d.reason='accepted' AND d.approver<>p.maker
  AND b.terminal_actor=d.approver AND b.revision=p.expected_revision+1 AND b.redeemed_count=p.expected_redeemed_count
  AND cmd.kind='voucher.revocation.approve' AND cmd.scope='platform' AND cmd.actor=d.approver
  AND terminal.status='accepted' AND terminal.resource_id=p.id) THEN RAISE EXCEPTION 'voucher revocation decision incomplete';END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER voucher_batch_complete AFTER INSERT OR UPDATE ON voucher_batches DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();
CREATE CONSTRAINT TRIGGER voucher_batch_vouchers_complete AFTER INSERT ON vouchers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();
CREATE CONSTRAINT TRIGGER voucher_batch_redemptions_complete AFTER INSERT ON voucher_redemptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();
CREATE FUNCTION commerce_command_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts r USING(operation_id) WHERE c.operation_id=NEW.operation_id AND (c.kind,c.scope,c.actor)=(r.kind,r.scope,r.actor)) THEN RAISE EXCEPTION 'commerce command incomplete'; END IF;
 IF TG_TABLE_NAME='commerce_operation_receipts' THEN
  IF NEW.status='accepted' AND NEW.kind='voucher.redeem' AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.operation_id=NEW.operation_id AND r.id=NEW.resource_id AND r.actor=NEW.actor AND 'tenant:'||r.tenant_id=NEW.scope AND r.amount=NEW.amount AND r.funding_receipt_id=NEW.funding_receipt_id AND r.journal_id=NEW.journal_id) THEN RAISE EXCEPTION 'commerce redemption incomplete';END IF;
  IF NEW.status='accepted' AND (
   (NEW.kind='voucher.batch.create' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.maker=NEW.actor)) OR
   (NEW.kind='voucher.batch.approve' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.state IN ('active','revoked') AND b.approver=NEW.actor)) OR
   (NEW.kind='voucher.batch.void' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.state='voided' AND b.terminal_actor=NEW.actor AND b.maker=NEW.actor)) OR
   (NEW.kind='voucher.revocation.create' AND NOT EXISTS(SELECT 1 FROM voucher_revocations p WHERE p.id=NEW.resource_id AND p.operation_id=NEW.operation_id AND p.maker=NEW.actor)) OR
   (NEW.kind='voucher.revocation.approve' AND NOT EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.proposal_id=NEW.resource_id AND d.operation_id=NEW.operation_id AND d.approver=NEW.actor AND d.status='accepted'))
  ) THEN RAISE EXCEPTION 'commerce resource incomplete';END IF;
  IF NEW.status='rejected' AND NEW.kind='voucher.revocation.approve' AND NEW.reason='voucher_evidence_stale' AND NOT EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.operation_id=NEW.operation_id AND d.approver=NEW.actor AND d.status='rejected' AND d.reason=NEW.reason) THEN RAISE EXCEPTION 'commerce stale decision incomplete';END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER commerce_command_complete AFTER INSERT ON commerce_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION commerce_command_complete();
CREATE CONSTRAINT TRIGGER commerce_receipt_complete AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION commerce_command_complete();

CREATE FUNCTION voucher_revocation_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p voucher_revocations%ROWTYPE; d voucher_revocation_decisions%ROWTYPE; b voucher_batches%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='voucher_revocations' THEN
  p:=NEW;
 ELSE
  d:=NEW;SELECT * INTO p FROM voucher_revocations WHERE id=d.proposal_id;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts terminal USING(operation_id)
  WHERE c.operation_id=p.operation_id AND c.kind='voucher.revocation.create' AND c.scope='platform' AND c.actor=p.maker
   AND terminal.status='accepted' AND terminal.resource_id=p.id) THEN RAISE EXCEPTION 'voucher revocation proposal incomplete';END IF;
 IF TG_TABLE_NAME='voucher_revocation_decisions' THEN
  SELECT * INTO b FROM voucher_batches WHERE id=p.batch_id;
  IF d.approver=p.maker OR d.batch_id<>p.batch_id OR NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts terminal USING(operation_id)
   WHERE c.operation_id=d.operation_id AND c.kind='voucher.revocation.approve' AND c.scope='platform' AND c.actor=d.approver
    AND terminal.status=d.status AND terminal.reason=d.reason AND (d.status='rejected' OR terminal.resource_id=p.id)) THEN RAISE EXCEPTION 'voucher revocation terminal incomplete';END IF;
  IF d.status='accepted' THEN
   IF b.state<>'revoked' OR b.terminal_actor<>d.approver OR b.revision<>p.expected_revision+1 OR b.redeemed_count<>p.expected_redeemed_count THEN RAISE EXCEPTION 'voucher revocation state incomplete';END IF;
  ELSIF b.state='active' AND b.revision=p.expected_revision AND b.redeemed_count=p.expected_redeemed_count THEN RAISE EXCEPTION 'voucher revocation evidence not stale';END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER voucher_revocation_complete AFTER INSERT ON voucher_revocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_revocation_complete();
CREATE CONSTRAINT TRIGGER voucher_decision_complete AFTER INSERT ON voucher_revocation_decisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_revocation_complete();
CREATE FUNCTION voucher_proposal_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.batch_id AND b.state='active' AND b.revision=NEW.expected_revision AND b.redeemed_count=NEW.expected_redeemed_count) THEN RAISE EXCEPTION 'voucher proposal snapshot invalid';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER voucher_proposal_snapshot BEFORE INSERT ON voucher_revocations FOR EACH ROW EXECUTE FUNCTION voucher_proposal_snapshot();

CREATE TRIGGER immutable_funding_receipt BEFORE UPDATE OR DELETE ON funding_receipts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_commerce_command BEFORE UPDATE OR DELETE ON commerce_commands FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_commerce_receipt BEFORE UPDATE OR DELETE ON commerce_operation_receipts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher BEFORE UPDATE OR DELETE ON vouchers FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher_redemption BEFORE UPDATE OR DELETE ON voucher_redemptions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher_revocation BEFORE UPDATE OR DELETE ON voucher_revocations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher_decision BEFORE UPDATE OR DELETE ON voucher_revocation_decisions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
-- END 017_funding_vouchers.sql
-- BEGIN 018_tenant_pricing.sql
-- 023-A: immutable tenant retail-pricing domains and request snapshots.
-- Run only after the serving instance is drained; retain all legacy facts.
DO $$
BEGIN
 IF current_setting('server_version_num')::integer < 150000 THEN
  RAISE EXCEPTION 'tenant pricing requires PostgreSQL 15 or later';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint
   WHERE conrelid='price_versions'::regclass
     AND conname='price_versions_model_id_effective_at_key' AND contype='u'
     AND pg_get_constraintdef(oid)='UNIQUE (model_id, effective_at)') THEN
  RAISE EXCEPTION 'original model/effective price constraint is missing or changed';
 END IF;
END $$;

-- 新命令仅供 A，不为尚未冻结的 B 抢占 policy_operations schema。
CREATE TABLE pricing_operations (
 operation_id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN
  ('price_group.create','price_group.update','group_price.publish','tenant_pricing.assign')),
 actor TEXT NOT NULL REFERENCES users(id),
 scope TEXT NOT NULL,
 scope_tenant_id TEXT REFERENCES tenants(id),
 target TEXT NOT NULL CHECK(octet_length(target) BETWEEN 1 AND 200),
 resource_id TEXT NOT NULL,
 revision BIGINT CHECK(revision>0),
 safe_input JSONB NOT NULL CHECK(jsonb_typeof(safe_input)='object'),
 input_fingerprint TEXT NOT NULL CHECK(input_fingerprint ~ '^[0-9a-f]{64}$'),
 result_schema SMALLINT NOT NULL DEFAULT 1 CHECK(result_schema=1),
 result_snapshot JSONB NOT NULL CHECK(jsonb_typeof(result_snapshot)='object'
  AND octet_length(result_snapshot::text)<=8192),
 created_txid BIGINT NOT NULL DEFAULT txid_current(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((kind='tenant_pricing.assign' AND scope_tenant_id IS NOT NULL
    AND scope=scope_tenant_id AND resource_id=scope_tenant_id)
   OR (kind<>'tenant_pricing.assign' AND scope='platform' AND scope_tenant_id IS NULL)),
 CHECK((kind='group_price.publish' AND revision IS NULL)
   OR (kind<>'group_price.publish' AND revision IS NOT NULL))
);
CREATE TRIGGER immutable_pricing_operation BEFORE UPDATE OR DELETE ON pricing_operations
 FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE price_groups (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 label TEXT NOT NULL CHECK(octet_length(label) BETWEEN 1 AND 100
  AND label=btrim(label) AND label !~ '[[:cntrl:]]'),
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','retired')),
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 last_operation_id TEXT NOT NULL REFERENCES pricing_operations(operation_id)
  DEFERRABLE INITIALLY DEFERRED,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE INDEX price_groups_time ON price_groups(created_at DESC,id DESC);
CREATE TRIGGER no_delete_price_group BEFORE DELETE ON price_groups
 FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

ALTER TABLE tenants
 ADD COLUMN price_group_id TEXT REFERENCES price_groups(id),
 ADD COLUMN pricing_revision BIGINT NOT NULL DEFAULT 1 CHECK(pricing_revision>0),
 ADD COLUMN pricing_operation_id TEXT REFERENCES pricing_operations(operation_id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE tenant_pricing_assignments (
 operation_id TEXT PRIMARY KEY REFERENCES pricing_operations(operation_id)
  DEFERRABLE INITIALLY DEFERRED,
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 before_group_id TEXT REFERENCES price_groups(id),
 after_group_id TEXT REFERENCES price_groups(id),
 before_revision BIGINT NOT NULL CHECK(before_revision>0),
 after_revision BIGINT NOT NULL CHECK(after_revision=before_revision+1),
 actor TEXT NOT NULL REFERENCES users(id),
 created_txid BIGINT NOT NULL DEFAULT txid_current(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK(before_group_id IS DISTINCT FROM after_group_id),
 UNIQUE(tenant_id,after_revision)
);
CREATE INDEX tenant_pricing_assignments_time
 ON tenant_pricing_assignments(tenant_id,created_at DESC,operation_id DESC);
CREATE TRIGGER immutable_tenant_pricing_assignment
 BEFORE UPDATE OR DELETE ON tenant_pricing_assignments
 FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

ALTER TABLE price_versions
 ADD COLUMN price_group_id TEXT REFERENCES price_groups(id),
 ADD COLUMN pricing_operation_id TEXT REFERENCES pricing_operations(operation_id)
  DEFERRABLE INITIALLY DEFERRED,
 ADD CONSTRAINT price_versions_group_operation CHECK
  ((price_group_id IS NULL AND pricing_operation_id IS NULL)
   OR (price_group_id IS NOT NULL AND pricing_operation_id IS NOT NULL)),
 DROP CONSTRAINT price_versions_model_id_effective_at_key,
 ADD CONSTRAINT price_versions_domain_effective_key
  UNIQUE NULLS NOT DISTINCT(model_id,price_group_id,effective_at);
CREATE INDEX price_versions_domain_lookup
 ON price_versions(model_id,price_group_id,effective_at DESC,created_at DESC,id DESC);
CREATE UNIQUE INDEX price_versions_pricing_operation
 ON price_versions(pricing_operation_id) WHERE pricing_operation_id IS NOT NULL;
-- 001 的 immutable_price 原封保留；不 UPDATE 原价格行。

ALTER TABLE request_records
 ADD COLUMN pricing_group_id TEXT REFERENCES price_groups(id),
 ADD COLUMN tenant_pricing_revision BIGINT,
 ADD COLUMN retail_priced_at TIMESTAMPTZ,
 ADD CONSTRAINT request_retail_snapshot_shape CHECK
  ((pricing_group_id IS NULL AND tenant_pricing_revision IS NULL AND retail_priced_at IS NULL)
   OR (tenant_pricing_revision IS NOT NULL AND tenant_pricing_revision>0
       AND retail_priced_at IS NOT NULL AND isfinite(retail_priced_at)));

CREATE FUNCTION guard_price_group_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'active' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'invalid initial price group';
  END IF;
 ELSE
  IF ROW(NEW.id,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.created_at) THEN
   RAISE EXCEPTION 'immutable price group identity';
  END IF;
  IF OLD.state='retired' AND NEW.state<>'retired' THEN
   RAISE EXCEPTION 'price group cannot be restored';
  END IF;
  IF ROW(NEW.label,NEW.state) IS DISTINCT FROM ROW(OLD.label,OLD.state) THEN
   IF OLD.revision=9223372036854775807 OR NEW.revision<>OLD.revision+1
      OR NEW.last_operation_id=OLD.last_operation_id THEN
    RAISE EXCEPTION 'invalid price group revision';
   END IF;
  ELSIF ROW(NEW.revision,NEW.last_operation_id)
        IS DISTINCT FROM ROW(OLD.revision,OLD.last_operation_id) THEN
   RAISE EXCEPTION 'price group no-op changed revision';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER price_group_change BEFORE INSERT OR UPDATE ON price_groups
 FOR EACH ROW EXECUTE FUNCTION guard_price_group_change();

CREATE FUNCTION guard_tenant_pricing_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gstate TEXT; op pricing_operations%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.price_group_id IS NOT NULL OR NEW.pricing_revision<>1
     OR NEW.pricing_operation_id IS NOT NULL THEN
   RAISE EXCEPTION 'new tenant must start with default pricing';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.price_group_id IS NOT DISTINCT FROM OLD.price_group_id THEN
  IF ROW(NEW.pricing_revision,NEW.pricing_operation_id)
    IS DISTINCT FROM ROW(OLD.pricing_revision,OLD.pricing_operation_id) THEN
   RAISE EXCEPTION 'tenant pricing no-op changed revision';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.pricing_revision=9223372036854775807
    OR NEW.pricing_revision<>OLD.pricing_revision+1
    OR NEW.pricing_operation_id IS NULL
    OR NEW.pricing_operation_id IS NOT DISTINCT FROM OLD.pricing_operation_id THEN
  RAISE EXCEPTION 'invalid tenant pricing revision';
 END IF;
 -- 在线 service 已按 tenant -> actor -> group 取得同一行锁。
 IF NEW.price_group_id IS NOT NULL THEN
  SELECT state INTO STRICT gstate FROM price_groups WHERE id=NEW.price_group_id FOR SHARE;
  IF gstate<>'active' THEN RAISE EXCEPTION 'price group retired'; END IF;
 END IF;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 IF op.created_txid<>txid_current() OR op.kind<>'tenant_pricing.assign'
    OR op.resource_id<>NEW.id OR op.revision<>NEW.pricing_revision THEN
  RAISE EXCEPTION 'tenant pricing operation mismatch';
 END IF;
 INSERT INTO tenant_pricing_assignments(operation_id,tenant_id,before_group_id,after_group_id,
  before_revision,after_revision,actor,created_txid,created_at)
 VALUES(op.operation_id,NEW.id,OLD.price_group_id,NEW.price_group_id,
  OLD.pricing_revision,NEW.pricing_revision,op.actor,txid_current(),op.created_at);
 RETURN NEW;
END $$;
CREATE TRIGGER tenant_pricing_change BEFORE INSERT OR UPDATE ON tenants
 FOR EACH ROW EXECUTE FUNCTION guard_tenant_pricing_change();

CREATE FUNCTION guard_group_price_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE protocol TEXT; gstate TEXT; op pricing_operations%ROWTYPE;
BEGIN
 -- 先 model 再 group；旧默认发布也显式取得 model SHARE。
 SELECT profile->>'protocol' INTO STRICT protocol FROM models WHERE id=NEW.model_id FOR SHARE;
 IF protocol IN ('openai_embeddings','jina_rerank') AND NEW.output_rate<>0 THEN
  RAISE EXCEPTION 'input-only price requires zero output rate';
 END IF;
 IF NEW.price_group_id IS NOT NULL THEN
  SELECT state INTO STRICT gstate FROM price_groups WHERE id=NEW.price_group_id FOR SHARE;
  IF gstate<>'active' THEN RAISE EXCEPTION 'price group retired'; END IF;
  SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
  IF op.created_txid<>txid_current() OR op.kind<>'group_price.publish'
     OR op.resource_id<>NEW.id OR op.actor<>NEW.created_by THEN
   RAISE EXCEPTION 'group price operation mismatch';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER group_price_insert BEFORE INSERT ON price_versions
 FOR EACH ROW EXECUTE FUNCTION guard_group_price_insert();

CREATE FUNCTION guard_request_retail_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gid TEXT; rev BIGINT; p price_versions%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.price_version_id,
     NEW.input_rate,NEW.output_rate,NEW.pricing_group_id,NEW.tenant_pricing_revision,NEW.retail_priced_at)
   IS DISTINCT FROM ROW(OLD.tenant_id,OLD.key_id,OLD.owner_id,OLD.model_id,OLD.price_version_id,
     OLD.input_rate,OLD.output_rate,OLD.pricing_group_id,OLD.tenant_pricing_revision,OLD.retail_priced_at) THEN
   RAISE EXCEPTION 'immutable request retail snapshot';
  END IF;
  RETURN NEW; -- legacy NULL 也不能被补写；正常 status/finalization 更新仍允许。
 END IF;
 IF NEW.tenant_pricing_revision IS NULL OR NEW.retail_priced_at IS NULL THEN
  RAISE EXCEPTION 'new request requires retail snapshot';
 END IF;
 SELECT price_group_id,pricing_revision INTO STRICT gid,rev
  FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
 IF gid IS DISTINCT FROM NEW.pricing_group_id OR rev<>NEW.tenant_pricing_revision THEN
  RAISE EXCEPTION 'request tenant pricing mismatch';
 END IF;
 SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.price_version_id;
 IF p.model_id<>NEW.model_id OR p.price_group_id IS DISTINCT FROM NEW.pricing_group_id
    OR p.input_rate<>NEW.input_rate OR p.output_rate<>NEW.output_rate
    OR p.effective_at>NEW.retail_priced_at OR NOT isfinite(NEW.retail_priced_at) THEN
  RAISE EXCEPTION 'request price version mismatch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER request_retail_snapshot BEFORE INSERT OR UPDATE ON request_records
 FOR EACH ROW EXECUTE FUNCTION guard_request_retail_snapshot();

CREATE FUNCTION pricing_time(t TIMESTAMPTZ) RETURNS TEXT LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT to_char(t AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
$$;
CREATE FUNCTION pricing_group_result(g price_groups) RETURNS JSONB LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT jsonb_build_object('id',g.id,'label',g.label,'state',g.state,
  'revision',g.revision::text,'created_at',pricing_time(g.created_at))
$$;
CREATE FUNCTION pricing_assignment_result(t tenants) RETURNS JSONB LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT jsonb_build_object('tenant_id',t.id,'price_group_id',t.price_group_id,
  'pricing_revision',t.pricing_revision::text)
$$;
CREATE FUNCTION pricing_price_result(p price_versions) RETURNS JSONB LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT jsonb_build_object('id',p.id,'model_id',p.model_id,'price_group_id',p.price_group_id,
  'currency',p.currency,'input_rate',p.input_rate::text,'output_rate',p.output_rate::text,
  'effective_at',pricing_time(p.effective_at),'created_at',pricing_time(p.created_at))
$$;

CREATE FUNCTION pricing_frame(parts TEXT[]) RETURNS BYTEA LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE v TEXT; b BYTEA; raw BYTEA;
BEGIN
 b:=convert_to('moni-aigate/pricing-command/v1','UTF8')||decode('00','hex')
    ||int4send(cardinality(parts));
 FOREACH v IN ARRAY parts LOOP
  IF v IS NULL THEN b:=b||int4send(-1);
  ELSE raw:=convert_to(v,'UTF8'); b:=b||int4send(octet_length(raw))||raw;
  END IF;
 END LOOP;
 RETURN b;
END $$;
CREATE FUNCTION pricing_fingerprint(k TEXT,a TEXT,s TEXT,t TEXT,j JSONB)
 RETURNS TEXT LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(pricing_frame(ARRAY[k,a,s,t] || CASE k
  WHEN 'price_group.create' THEN ARRAY[j->>'label']
  WHEN 'price_group.update' THEN ARRAY[j->>'expected_revision',j->>'label',j->>'state']
  WHEN 'group_price.publish' THEN ARRAY[j->>'model_id',j->>'input_rate',j->>'output_rate',j->>'effective_at']
  WHEN 'tenant_pricing.assign' THEN ARRAY[j->>'expected_revision',j->>'price_group_id']
  ELSE ARRAY[]::text[] END)),'hex')
$$;

-- 在任何 cast / 比较前验证 JSON 类型；SQL NULL 与 JSON null 均不能作为 revision。
CREATE FUNCTION pricing_json_uint(j JSONB,positive BOOLEAN) RETURNS BOOLEAN
 LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE v TEXT;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'string' THEN RETURN false; END IF;
 v:=j#>>'{}';
 IF positive THEN
  IF v !~ '^[1-9][0-9]*$' THEN RETURN false; END IF;
 ELSE
  IF v !~ '^(0|[1-9][0-9]*)$' THEN RETURN false; END IF;
 END IF;
 IF octet_length(v)>19 THEN RETURN false; END IF;
 RETURN v::numeric<=9223372036854775807::numeric;
END $$;
CREATE FUNCTION pricing_json_uuid(j JSONB) RETURNS BOOLEAN
 LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(jsonb_typeof(j)='string'
  AND (j#>>'{}') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND (j#>>'{}')<>'00000000-0000-0000-0000-000000000000',false)
$$;
CREATE FUNCTION pricing_json_time(j JSONB) RETURNS BOOLEAN
 LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE v TEXT; t TIMESTAMPTZ;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'string' THEN RETURN false; END IF;
 v:=j#>>'{}';
 IF v !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.][0-9]{6}Z$' THEN
  RETURN false;
 END IF;
 BEGIN t:=v::timestamptz;
 EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow THEN RETURN false;
 END;
 RETURN isfinite(t) AND pricing_time(t)=v;
END $$;
CREATE FUNCTION assert_pricing_safe_input(k TEXT,j JSONB) RETURNS VOID
 LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE shape JSONB; label TEXT;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'object' THEN
  RAISE EXCEPTION 'pricing safe input must be object';
 END IF;
 CASE k
 WHEN 'price_group.create' THEN
  shape:=jsonb_build_object('label',j->'label');
 WHEN 'price_group.update' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','label',j->'label','state',j->'state');
  IF NOT pricing_json_uint(j->'expected_revision',true)
     OR jsonb_typeof(j->'state') IS DISTINCT FROM 'string'
     OR (j->>'state') NOT IN ('active','retired') THEN
   RAISE EXCEPTION 'invalid group update input';
  END IF;
 WHEN 'group_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','input_rate',j->'input_rate',
   'output_rate',j->'output_rate','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id')
     OR NOT pricing_json_uint(j->'input_rate',false)
     OR NOT pricing_json_uint(j->'output_rate',false)
     OR NOT pricing_json_time(j->'effective_at') THEN
   RAISE EXCEPTION 'invalid group price input';
  END IF;
 WHEN 'tenant_pricing.assign' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','price_group_id',j->'price_group_id');
  IF NOT pricing_json_uint(j->'expected_revision',true)
     OR NOT (j ? 'price_group_id')
     OR (jsonb_typeof(j->'price_group_id') IS DISTINCT FROM 'null'
         AND NOT pricing_json_uuid(j->'price_group_id')) THEN
   RAISE EXCEPTION 'invalid tenant pricing input';
  END IF;
 ELSE RAISE EXCEPTION 'unknown pricing operation kind';
 END CASE;
 IF j IS DISTINCT FROM shape THEN RAISE EXCEPTION 'pricing input keys mismatch'; END IF;
 IF k IN ('price_group.create','price_group.update') THEN
  IF jsonb_typeof(j->'label') IS DISTINCT FROM 'string' THEN
   RAISE EXCEPTION 'pricing label must be string';
  END IF;
  label:=j->>'label';
  IF octet_length(label) NOT BETWEEN 1 AND 100 OR label<>btrim(label) OR label ~ '[[:cntrl:]]' THEN
   RAISE EXCEPTION 'invalid pricing label';
  END IF;
 END IF;
END $$;

CREATE FUNCTION guard_pricing_operation_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input);
 NEW.created_txid:=txid_current();
 NEW.created_at:=clock_timestamp();
 NEW.input_fingerprint:=pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,NEW.safe_input);
 -- 新资源的 created_at 取 receipt 插入的 DB 时间；service 用 RETURNING 后建资源。
 IF NEW.kind IN ('price_group.create','group_price.publish') THEN
  NEW.result_snapshot:=jsonb_set(NEW.result_snapshot,'{created_at}',to_jsonb(pricing_time(NEW.created_at)),true);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER pricing_operation_insert BEFORE INSERT ON pricing_operations
 FOR EACH ROW EXECUTE FUNCTION guard_pricing_operation_insert();

CREATE FUNCTION require_pricing_operation_result() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g price_groups%ROWTYPE; t tenants%ROWTYPE; p price_versions%ROWTYPE;
 e tenant_pricing_assignments%ROWTYPE; expected JSONB; prior BIGINT; has_event BOOLEAN;
BEGIN
 IF NEW.created_txid<>txid_current() THEN RAISE EXCEPTION 'pricing operation not current transaction'; END IF;
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input);
 IF NEW.kind='price_group.create' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  expected:=jsonb_build_object('label',g.label);
  IF NEW.target<>'/api/platform/price-groups' OR g.last_operation_id<>NEW.operation_id
     OR g.revision<>1 OR g.state<>'active' OR g.created_at<>NEW.created_at
     OR NEW.revision<>1 OR NEW.result_snapshot<>pricing_group_result(g) THEN
   RAISE EXCEPTION 'price group create result mismatch';
  END IF;
 ELSIF NEW.kind='price_group.update' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'label',g.label,'state',g.state);
  IF NEW.target<>('/api/platform/price-groups/'||g.id) OR NEW.revision<>g.revision
     OR NEW.result_snapshot<>pricing_group_result(g)
     OR NOT ((g.last_operation_id=NEW.operation_id AND prior<9223372036854775807 AND g.revision=prior+1)
       OR (g.last_operation_id<>NEW.operation_id AND g.revision=prior)) THEN
   RAISE EXCEPTION 'price group update result mismatch';
  END IF;
 ELSIF NEW.kind='group_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=jsonb_build_object('model_id',p.model_id,'input_rate',p.input_rate::text,
   'output_rate',p.output_rate::text,'effective_at',pricing_time(p.effective_at));
  IF p.price_group_id IS NULL OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor
     OR p.created_at<>NEW.created_at
     OR NEW.target<>('/api/platform/price-groups/'||p.price_group_id||'/price-versions')
     OR NEW.result_snapshot<>pricing_price_result(p) THEN
   RAISE EXCEPTION 'group price result mismatch';
  END IF;
 ELSE
  SELECT * INTO STRICT t FROM tenants WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'price_group_id',t.price_group_id);
  SELECT * INTO e FROM tenant_pricing_assignments WHERE operation_id=NEW.operation_id;
  has_event:=FOUND;
  IF NEW.target<>('/api/platform/tenants/'||t.id||'/pricing') OR NEW.revision<>t.pricing_revision
     OR NEW.result_snapshot<>pricing_assignment_result(t) THEN
   RAISE EXCEPTION 'tenant pricing result mismatch';
  END IF;
  IF t.pricing_operation_id=NEW.operation_id THEN
   IF NOT has_event OR e.created_txid<>txid_current() OR e.tenant_id<>t.id OR e.actor<>NEW.actor
      OR e.after_group_id IS DISTINCT FROM t.price_group_id OR e.before_revision<>prior
      OR e.after_revision<>t.pricing_revision OR e.created_at<>NEW.created_at THEN
    RAISE EXCEPTION 'tenant pricing event mismatch';
   END IF;
  ELSIF has_event OR t.pricing_revision<>prior THEN
   RAISE EXCEPTION 'tenant pricing no-op mismatch';
  END IF;
 END IF;
 IF NEW.safe_input IS DISTINCT FROM expected OR NEW.input_fingerprint IS DISTINCT FROM
    pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,expected) THEN
  RAISE EXCEPTION 'pricing command input mismatch';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER pricing_operation_result AFTER INSERT ON pricing_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_pricing_operation_result();

CREATE FUNCTION require_price_group_operation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.label,NEW.state) IS NOT DISTINCT FROM ROW(OLD.label,OLD.state) THEN
  RETURN NULL;
 END IF;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.last_operation_id;
 IF op.created_txid<>txid_current() OR op.resource_id<>NEW.id OR op.revision<>NEW.revision
    OR op.kind<>(CASE WHEN TG_OP='INSERT' THEN 'price_group.create' ELSE 'price_group.update' END)
    OR op.result_snapshot<>pricing_group_result(NEW) THEN
  RAISE EXCEPTION 'price group operation required';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER price_group_operation AFTER INSERT OR UPDATE ON price_groups
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_price_group_operation();

CREATE FUNCTION require_tenant_pricing_assignment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t tenants%ROWTYPE; op pricing_operations%ROWTYPE;
BEGIN
 SELECT * INTO STRICT t FROM tenants WHERE id=NEW.tenant_id;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.operation_id;
 IF NEW.created_txid<>txid_current() OR op.created_txid<>txid_current()
    OR op.kind<>'tenant_pricing.assign' OR op.actor<>NEW.actor OR op.created_at<>NEW.created_at
    OR op.resource_id<>NEW.tenant_id OR op.revision<>NEW.after_revision
    OR t.pricing_operation_id<>NEW.operation_id OR t.pricing_revision<>NEW.after_revision
    OR t.price_group_id IS DISTINCT FROM NEW.after_group_id THEN
  RAISE EXCEPTION 'assignment event without same transaction change';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER tenant_pricing_assignment_link AFTER INSERT ON tenant_pricing_assignments
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_tenant_pricing_assignment();
-- END 018_tenant_pricing.sql
-- BEGIN 019_operations_alerts.sql
-- 024-A: closed in-app operations alerts; observe business sources without locks or side effects.

CREATE FUNCTION alert_id(v TEXT) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND v <> '00000000-0000-0000-0000-000000000000',false)
$$;
CREATE FUNCTION alert_keys(v JSONB, names TEXT[]) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF jsonb_typeof(v) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 RETURN (v ?& names) IS TRUE AND NOT EXISTS
   (SELECT 1 FROM jsonb_object_keys(v) k WHERE NOT (k=ANY(names)));
END $$;
CREATE FUNCTION alert_uint(v TEXT, max_value NUMERIC) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF v IS NULL OR length(v)>101 OR v !~ '^(0|[1-9][0-9]*)$' THEN RETURN false; END IF;
 RETURN v::numeric<=max_value;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
CREATE FUNCTION alert_signed(v TEXT) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(length(v)<=102 AND v ~ '^(0|[1-9][0-9]{0,100}|-[1-9][0-9]{0,100})$',false)
$$;
CREATE FUNCTION alert_label(v TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE n INTEGER; i INTEGER; cp INTEGER;
BEGIN
 IF v IS NULL THEN RETURN false; END IF;
 n:=char_length(v);
 IF n<1 OR n>80 OR octet_length(v)>160 THEN RETURN false; END IF;
 FOR i IN 1..n LOOP
  cp:=ascii(substr(v,i,1)); -- 受支持数据库编码必须UTF8，ascii返回Unicode code point
  IF cp BETWEEN 0 AND 31 OR cp BETWEEN 127 AND 159 THEN RETURN false; END IF;
  IF (i=1 OR i=n) AND (cp IN (32,133,160,5760,8232,8233,8239,8287,12288)
    OR cp BETWEEN 8192 AND 8202) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
CREATE FUNCTION alert_time(v TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF v IS NULL OR v !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.][0-9]{6}Z$' THEN RETURN false; END IF;
 RETURN to_char(v::timestamptz AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')=v;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
CREATE FUNCTION alert_parameters(k TEXT,p JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF p IS NULL OR octet_length(p::text)>512 THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN RETURN p='{}'::jsonb; END IF;
 IF k='procurement_budget_low' THEN
  IF NOT alert_keys(p,ARRAY['threshold_amount','recovery_amount']) THEN RETURN false; END IF;
  IF jsonb_typeof(p->'threshold_amount') IS DISTINCT FROM 'string'
    OR jsonb_typeof(p->'recovery_amount') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'threshold_amount',9223372036854775807)
    OR NOT alert_uint(p->>'recovery_amount',9223372036854775807) THEN RETURN false; END IF;
  RETURN (p->>'recovery_amount')::numeric >= (p->>'threshold_amount')::numeric;
 END IF;
 IF k IN ('customer_reconciliation_aged','procurement_unresolved_aged') THEN
  IF NOT alert_keys(p,ARRAY['minimum_age_seconds','trigger_count','recovery_count']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(p) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'minimum_age_seconds',604800) OR NOT alert_uint(p->>'trigger_count',1000)
    OR NOT alert_uint(p->>'recovery_count',999) THEN RETURN false; END IF;
  RETURN (p->>'minimum_age_seconds')::numeric>=600 AND (p->>'trigger_count')::numeric>=1
    AND (p->>'recovery_count')::numeric<(p->>'trigger_count')::numeric;
 END IF;
 RETURN false;
END $$;
CREATE FUNCTION alert_snapshot(v JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k TEXT; s TEXT;
BEGIN
 IF v IS NULL OR octet_length(v::text)>1024 OR NOT alert_keys(v,ARRAY['kind','scope_type','scope_id','label','parameters']) THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE x.key<>'parameters' AND jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
 k:=v->>'kind'; s:=v->>'scope_type';
 RETURN alert_id(v->>'scope_id') AND alert_label(v->>'label') AND alert_parameters(k,v->'parameters')
  AND ((k='channel_quarantined' AND s='channel')
    OR (k IN ('procurement_budget_low','procurement_unresolved_aged') AND s='procurement_account')
    OR (k='customer_reconciliation_aged' AND s='tenant'));
END $$;
CREATE FUNCTION alert_sample(v JSONB,k TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE n NUMERIC; age_seconds NUMERIC;
BEGIN
 IF v IS NULL OR octet_length(v::text)>2048 OR jsonb_typeof(v) IS DISTINCT FROM 'object'
   OR jsonb_typeof(v->'kind') IS DISTINCT FROM 'string' OR (v->>'kind') IS DISTINCT FROM k
   OR jsonb_typeof(v->'as_of') IS DISTINCT FROM 'string' OR NOT alert_time(v->>'as_of') THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN
  RETURN alert_keys(v,ARRAY['kind','as_of','enabled','quarantined'])
   AND jsonb_typeof(v->'enabled')='boolean' AND jsonb_typeof(v->'quarantined')='boolean';
 ELSIF k='procurement_budget_low' THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','account_state','limit_amount','held_amount','spent_amount','available_amount','currency']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF (v->>'account_state') NOT IN ('active','paused') OR (v->>'currency') IS DISTINCT FROM 'CNY'
    OR NOT alert_uint(v->>'limit_amount',9223372036854775807)
    OR NOT alert_uint(v->>'held_amount',power(10::numeric,100)-1)
    OR NOT alert_uint(v->>'spent_amount',power(10::numeric,100)-1)
    OR NOT alert_signed(v->>'available_amount') THEN RETURN false; END IF;
  RETURN (v->>'available_amount')::numeric=(v->>'limit_amount')::numeric-(v->>'held_amount')::numeric-(v->>'spent_amount')::numeric;
 ELSIF k IN ('customer_reconciliation_aged','procurement_unresolved_aged') THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','minimum_age_seconds','observed_count','count_is_lower_bound','oldest_created_at','oldest_reference_id']) THEN RETURN false; END IF;
  IF jsonb_typeof(v->'minimum_age_seconds') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'observed_count') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'count_is_lower_bound') IS DISTINCT FROM 'boolean'
    OR NOT alert_uint(v->>'minimum_age_seconds',604800) OR NOT alert_uint(v->>'observed_count',1001) THEN RETURN false; END IF;
  n:=(v->>'observed_count')::numeric; age_seconds:=(v->>'minimum_age_seconds')::numeric;
  IF age_seconds<600 OR (v->>'count_is_lower_bound')::boolean IS DISTINCT FROM (n=1001) THEN RETURN false; END IF;
  IF n=0 THEN RETURN v->'oldest_created_at'='null'::jsonb AND v->'oldest_reference_id'='null'::jsonb; END IF;
  IF jsonb_typeof(v->'oldest_created_at') IS DISTINCT FROM 'string' OR jsonb_typeof(v->'oldest_reference_id') IS DISTINCT FROM 'string'
    OR NOT alert_time(v->>'oldest_created_at') OR NOT alert_id(v->>'oldest_reference_id') THEN RETURN false; END IF;
  RETURN (v->>'oldest_created_at')::timestamptz <= (v->>'as_of')::timestamptz-age_seconds*interval '1 second';
 END IF;
 RETURN false;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
CREATE FUNCTION alert_decision(snapshot JSONB,sample JSONB,has_episode BOOLEAN) RETURNS TEXT LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k TEXT:=snapshot->>'kind'; p JSONB:=snapshot->'parameters'; n NUMERIC;
BEGIN
 IF NOT alert_snapshot(snapshot) OR NOT alert_sample(sample,k) OR has_episode IS NULL THEN RETURN 'invalid'; END IF;
 IF k='channel_quarantined' THEN RETURN CASE WHEN (sample->>'quarantined')::boolean THEN 'breach' ELSE 'clear' END; END IF;
 IF k='procurement_budget_low' THEN
  n:=(sample->>'available_amount')::numeric;
  IF has_episode THEN RETURN CASE WHEN n>(p->>'recovery_amount')::numeric THEN 'clear' ELSE 'hold' END; END IF;
  RETURN CASE WHEN n<=(p->>'threshold_amount')::numeric THEN 'breach' ELSE 'hold' END;
 END IF;
 IF (sample->>'minimum_age_seconds') IS DISTINCT FROM (p->>'minimum_age_seconds') THEN RETURN 'invalid'; END IF;
 n:=(sample->>'observed_count')::numeric;
 IF has_episode THEN RETURN CASE WHEN n<=(p->>'recovery_count')::numeric THEN 'clear' ELSE 'hold' END; END IF;
 RETURN CASE WHEN n>=(p->>'trigger_count')::numeric THEN 'breach' ELSE 'hold' END;
END $$;

CREATE TABLE alert_control (
 singleton BOOLEAN PRIMARY KEY CHECK(singleton IS TRUE)
);
INSERT INTO alert_control(singleton) VALUES(true);
CREATE TABLE alert_commands (
 operation_id TEXT PRIMARY KEY CHECK(alert_id(operation_id) IS TRUE),
 actor TEXT NOT NULL CHECK(alert_id(actor) IS TRUE),
 kind TEXT NOT NULL CHECK(kind IN ('rule_create','rule_configure','rule_retire','acknowledge')),
 target_id TEXT CHECK(target_id IS NULL OR alert_id(target_id) IS TRUE),
 canonical_digest TEXT NOT NULL CHECK(canonical_digest ~ '^[0-9a-f]{64}$'),
 transaction_id BIGINT NOT NULL DEFAULT txid_current() UNIQUE,
 status TEXT NOT NULL CHECK(status IN ('claimed','applied','rejected')),
 reason TEXT NOT NULL DEFAULT '', resource_id TEXT, result_revision BIGINT,
 created_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((kind='rule_create' AND target_id IS NULL) OR (kind<>'rule_create' AND target_id IS NOT NULL)),
 CHECK(resource_id IS NULL OR alert_id(resource_id) IS TRUE),
 CHECK(result_revision IS NULL OR result_revision>0),
 CHECK((status='claimed' AND reason='' AND resource_id IS NULL AND result_revision IS NULL)
  OR (status='applied' AND reason='' AND resource_id IS NOT NULL AND result_revision IS NOT NULL)
  OR (status='rejected' AND reason IN ('alert_revision_stale','alert_rule_limit','alert_rule_exists','alert_rule_retired',
   'alert_source_missing','alert_not_open','alert_already_acknowledged','alert_not_found') AND result_revision IS NULL
   AND ((kind='rule_create' AND resource_id IS NULL) OR (kind<>'rule_create' AND resource_id IS NOT DISTINCT FROM target_id))))
);
CREATE TABLE alert_rules (
 id TEXT PRIMARY KEY CHECK(alert_id(id) IS TRUE),
 kind TEXT NOT NULL, scope_type TEXT NOT NULL, scope_id TEXT NOT NULL,
 label TEXT NOT NULL CHECK(alert_label(label) IS TRUE),
 state TEXT NOT NULL CHECK(state IN ('enabled','disabled','retired')),
 parameters JSONB NOT NULL,
 configuration_revision BIGINT NOT NULL CHECK(configuration_revision>0),
 created_by TEXT NOT NULL CHECK(alert_id(created_by) IS TRUE),
 updated_by TEXT NOT NULL CHECK(alert_id(updated_by) IS TRUE),
 created_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(created_at)), updated_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(updated_at)),
 retired_at TIMESTAMPTZ(6) CHECK(retired_at IS NULL OR isfinite(retired_at)),
 last_command_id TEXT NOT NULL REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(alert_snapshot(jsonb_build_object('kind',kind,'scope_type',scope_type,'scope_id',scope_id,'label',label,'parameters',parameters)) IS TRUE),
 CHECK(updated_at>=created_at),
 CHECK((state='retired' AND retired_at IS NOT NULL AND retired_at=updated_at) OR (state<>'retired' AND retired_at IS NULL))
);
CREATE UNIQUE INDEX alert_rules_live_scope ON alert_rules(kind,scope_id) WHERE state<>'retired';
CREATE INDEX alert_rules_page ON alert_rules(created_at DESC,id COLLATE "C" DESC);
CREATE INDEX alert_rules_last_command ON alert_rules(last_command_id);
CREATE TABLE alert_episodes (
 id TEXT PRIMARY KEY CHECK(alert_id(id) IS TRUE),
 rule_id TEXT NOT NULL REFERENCES alert_rules(id),
 generation BIGINT NOT NULL CHECK(generation>0),
 rule_revision BIGINT NOT NULL CHECK(rule_revision>0),
 rule_snapshot JSONB NOT NULL CHECK(alert_snapshot(rule_snapshot) IS TRUE),
 opened_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(opened_at)),
 initial_sample JSONB NOT NULL,
 last_seen_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(last_seen_at)), latest_sample JSONB NOT NULL,
 action_revision BIGINT NOT NULL CHECK(action_revision BETWEEN 1 AND 3),
 acknowledged_by TEXT, acknowledged_at TIMESTAMPTZ(6) CHECK(acknowledged_at IS NULL OR isfinite(acknowledged_at)), acknowledged_command_id TEXT REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 resolved_at TIMESTAMPTZ(6) CHECK(resolved_at IS NULL OR isfinite(resolved_at)), resolve_reason TEXT, resolved_command_id TEXT REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 UNIQUE(rule_id,generation), UNIQUE(rule_id,id),
 CHECK(acknowledged_by IS NULL OR alert_id(acknowledged_by) IS TRUE),
 CHECK((acknowledged_by IS NULL AND acknowledged_at IS NULL AND acknowledged_command_id IS NULL)
  OR (acknowledged_by IS NOT NULL AND acknowledged_at IS NOT NULL AND acknowledged_command_id IS NOT NULL AND acknowledged_at>=opened_at)),
 CHECK((resolved_at IS NULL AND resolve_reason IS NULL AND resolved_command_id IS NULL)
  OR (resolved_at IS NOT NULL AND resolved_at>=last_seen_at AND resolve_reason IN ('recovered','rule_changed','rule_disabled','rule_retired')
   AND ((resolve_reason='recovered' AND resolved_command_id IS NULL) OR (resolve_reason<>'recovered' AND resolved_command_id IS NOT NULL)))),
 CHECK(resolved_at IS NULL OR acknowledged_at IS NULL OR resolved_at>=acknowledged_at),
 CHECK(last_seen_at>=opened_at),
 CHECK(alert_sample(initial_sample,rule_snapshot->>'kind') IS TRUE),
 CHECK(alert_sample(latest_sample,rule_snapshot->>'kind') IS TRUE),
 CHECK(opened_at=(initial_sample->>'as_of')::timestamptz),
 CHECK(last_seen_at=(latest_sample->>'as_of')::timestamptz),
 CHECK(action_revision=1+(acknowledged_at IS NOT NULL)::int+(resolved_at IS NOT NULL)::int)
);
CREATE UNIQUE INDEX alert_episodes_unresolved ON alert_episodes(rule_id) WHERE resolved_at IS NULL;
CREATE INDEX alert_episodes_page ON alert_episodes(opened_at DESC,id COLLATE "C" DESC);
CREATE INDEX alert_episodes_rule_page ON alert_episodes(rule_id,opened_at DESC,id COLLATE "C" DESC);
CREATE TABLE alert_rule_runtime (
 rule_id TEXT PRIMARY KEY REFERENCES alert_rules(id),
 configured_revision BIGINT NOT NULL CHECK(configured_revision>0),
 write_revision BIGINT NOT NULL DEFAULT 1 CHECK(write_revision>0),
 episode_generation BIGINT NOT NULL DEFAULT 0 CHECK(episode_generation>=0),
 next_evaluation_at TIMESTAMPTZ(6) CHECK(next_evaluation_at IS NULL OR isfinite(next_evaluation_at)),
 last_attempt_at TIMESTAMPTZ(6) CHECK(last_attempt_at IS NULL OR isfinite(last_attempt_at)),
 last_success_at TIMESTAMPTZ(6) CHECK(last_success_at IS NULL OR isfinite(last_success_at)),
 evaluated_revision BIGINT, last_sample JSONB,
 last_error_code TEXT CHECK(last_error_code IN ('source_missing','source_query_timeout','source_unavailable','clock_regression','counter_exhausted','evaluation_failed')),
 current_episode_id TEXT,
 FOREIGN KEY(rule_id,current_episode_id) REFERENCES alert_episodes(rule_id,id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((last_success_at IS NULL AND evaluated_revision IS NULL AND last_sample IS NULL)
   OR (last_success_at IS NOT NULL AND evaluated_revision IS NOT NULL AND evaluated_revision>0 AND last_sample IS NOT NULL))
);
CREATE INDEX alert_runtime_due ON alert_rule_runtime(next_evaluation_at,rule_id COLLATE "C") WHERE next_evaluation_at IS NOT NULL;
CREATE TABLE alert_episode_events (
 id TEXT PRIMARY KEY CHECK(alert_id(id) IS TRUE),
 episode_id TEXT NOT NULL REFERENCES alert_episodes(id),
 action_revision BIGINT NOT NULL CHECK(action_revision BETWEEN 1 AND 3),
 type TEXT NOT NULL CHECK(type IN ('opened','acknowledged','recovered','rule_changed','rule_disabled','rule_retired')),
 actor TEXT NOT NULL CHECK(actor='system' OR alert_id(actor) IS TRUE),
 observed_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(observed_at)),
 safe_sample JSONB, created_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(created_at)),
 command_id TEXT REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 UNIQUE(episode_id,action_revision),
 CHECK(created_at=observed_at),
 CHECK((type IN ('opened','recovered') AND actor='system' AND command_id IS NULL AND safe_sample IS NOT NULL)
   OR (type NOT IN ('opened','recovered') AND actor<>'system' AND command_id IS NOT NULL AND safe_sample IS NULL))
);
CREATE INDEX alert_events_page ON alert_episode_events(episode_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX alert_commands_actor ON alert_commands(actor,created_at DESC,operation_id COLLATE "C" DESC);
CREATE INDEX alert_events_command ON alert_episode_events(command_id) WHERE command_id IS NOT NULL;
CREATE INDEX alert_episodes_ack_command ON alert_episodes(acknowledged_command_id) WHERE acknowledged_command_id IS NOT NULL;
CREATE INDEX alert_episodes_resolve_command ON alert_episodes(resolved_command_id) WHERE resolved_command_id IS NOT NULL;

CREATE INDEX alert_customer_aging_source ON reservations(tenant_id,created_at,request_id COLLATE "C")
 WHERE status='pending_reconciliation';
CREATE INDEX alert_procurement_aging_source ON procurement_reservations(account_id,created_at,attempt_id COLLATE "C")
 WHERE state IN ('unknown','disputed');

CREATE FUNCTION alert_require_command(op TEXT,k TEXT,target TEXT,who TEXT) RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE c alert_commands;
BEGIN
 SELECT * INTO c FROM alert_commands WHERE operation_id=op;
 IF NOT FOUND THEN RETURN false; END IF;
 RETURN c.transaction_id=txid_current() AND c.kind=k AND c.target_id IS NOT DISTINCT FROM target
  AND c.actor=who AND c.status IN ('claimed','applied');
END $$;
CREATE FUNCTION alert_guard_command() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable alert command'; END IF;
 IF TG_OP='INSERT' THEN
  NEW.created_at:=clock_timestamp()::timestamptz(6);
  IF NEW.transaction_id IS DISTINCT FROM txid_current() OR NEW.status IS DISTINCT FROM 'claimed' THEN RAISE EXCEPTION 'claim required'; END IF;
 ELSE
  IF OLD.status IS DISTINCT FROM 'claimed' OR NEW.status NOT IN ('applied','rejected')
   OR (NEW.operation_id,NEW.actor,NEW.kind,NEW.target_id,NEW.canonical_digest,NEW.transaction_id,NEW.created_at)
    IS DISTINCT FROM (OLD.operation_id,OLD.actor,OLD.kind,OLD.target_id,OLD.canonical_digest,OLD.transaction_id,OLD.created_at)
   OR NEW.transaction_id IS DISTINCT FROM txid_current() THEN RAISE EXCEPTION 'immutable alert command'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_command_guard BEFORE INSERT OR UPDATE OR DELETE ON alert_commands FOR EACH ROW EXECUTE FUNCTION alert_guard_command();
CREATE FUNCTION alert_guard_rule() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN; expected_kind TEXT; live_count BIGINT; enabled_count BIGINT;
BEGIN
 -- repository在rule写之前已取control；直接SQL也必须串行验证实例上限。
 -- 任意raw UPDATE本来可能先持目标rule锁，不能将其死锁行为当成受支持API锁序。
 PERFORM singleton FROM alert_control WHERE singleton FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing alert control'; END IF;
 SELECT count(*),count(*) FILTER (WHERE state='enabled')
  INTO live_count,enabled_count FROM alert_rules WHERE state<>'retired';
 IF TG_OP='INSERT' THEN
  IF NEW.state='retired' OR NOT alert_require_command(NEW.last_command_id,'rule_create',NULL,NEW.created_by)
    OR NEW.updated_by IS DISTINCT FROM NEW.created_by OR NEW.updated_at IS DISTINCT FROM NEW.created_at THEN RAISE EXCEPTION 'invalid rule creation'; END IF;
  IF live_count>=256 OR (NEW.state='enabled' AND enabled_count>=64) THEN RAISE EXCEPTION 'alert rule limit'; END IF;
  NEW.configuration_revision:=1;
 ELSE
  IF (NEW.id,NEW.kind,NEW.scope_type,NEW.scope_id,NEW.created_by,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.kind,OLD.scope_type,OLD.scope_id,OLD.created_by,OLD.created_at) THEN RAISE EXCEPTION 'immutable rule identity'; END IF;
  changed:=(NEW.label,NEW.state,NEW.parameters) IS DISTINCT FROM (OLD.label,OLD.state,OLD.parameters);
  IF NOT changed THEN
   NEW.configuration_revision:=OLD.configuration_revision; NEW.last_command_id:=OLD.last_command_id;
   NEW.updated_by:=OLD.updated_by; NEW.updated_at:=OLD.updated_at; NEW.retired_at:=OLD.retired_at;
   RETURN NEW;
  END IF;
  IF OLD.state='retired' OR OLD.configuration_revision=9223372036854775807 THEN RAISE EXCEPTION 'rule terminal or exhausted'; END IF;
  expected_kind:=CASE WHEN NEW.state='retired' THEN 'rule_retire' ELSE 'rule_configure' END;
  IF NOT alert_require_command(NEW.last_command_id,expected_kind,NEW.id,NEW.updated_by)
    OR NEW.last_command_id IS NOT DISTINCT FROM OLD.last_command_id OR NEW.updated_at<OLD.updated_at THEN RAISE EXCEPTION 'rule command required'; END IF;
  IF NEW.state='retired' AND (NEW.label,NEW.parameters) IS DISTINCT FROM (OLD.label,OLD.parameters) THEN RAISE EXCEPTION 'retire changes identity'; END IF;
  IF OLD.state<>'enabled' AND NEW.state='enabled' AND enabled_count>=64 THEN RAISE EXCEPTION 'alert rule limit'; END IF;
  NEW.configuration_revision:=OLD.configuration_revision+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_rule_guard BEFORE INSERT OR UPDATE ON alert_rules FOR EACH ROW EXECUTE FUNCTION alert_guard_rule();
CREATE FUNCTION alert_guard_runtime() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r alert_rules; reset BOOLEAN;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=NEW.rule_id;
 IF NOT FOUND OR NEW.configured_revision IS DISTINCT FROM r.configuration_revision THEN RAISE EXCEPTION 'runtime configuration mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.episode_generation<>0 OR NEW.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'invalid initial runtime'; END IF;
  NEW.write_revision:=1; reset:=true;
 ELSE
  IF NEW.rule_id IS DISTINCT FROM OLD.rule_id OR OLD.write_revision=9223372036854775807 THEN RAISE EXCEPTION 'runtime immutable or exhausted'; END IF;
  NEW.write_revision:=OLD.write_revision+1;
  reset:=NEW.configured_revision IS DISTINCT FROM OLD.configured_revision;
  IF reset AND (NEW.episode_generation IS DISTINCT FROM OLD.episode_generation
    OR NEW.configured_revision<>OLD.configured_revision+1) THEN RAISE EXCEPTION 'invalid configuration reset'; END IF;
 END IF;
 IF reset THEN
  IF NOT EXISTS(SELECT 1 FROM alert_commands c WHERE c.operation_id=r.last_command_id
    AND c.transaction_id=txid_current() AND c.status IN ('claimed','applied')
    AND c.kind IN ('rule_create','rule_configure','rule_retire')) THEN RAISE EXCEPTION 'reset command required'; END IF;
  IF NEW.last_attempt_at IS NOT NULL OR NEW.last_success_at IS NOT NULL OR NEW.evaluated_revision IS NOT NULL
    OR NEW.last_sample IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.current_episode_id IS NOT NULL
    OR NEW.next_evaluation_at IS DISTINCT FROM (CASE WHEN r.state='enabled' THEN r.updated_at ELSE NULL END) THEN RAISE EXCEPTION 'invalid reset fields'; END IF;
  RETURN NEW;
 END IF;
 IF r.state<>'enabled' OR NEW.last_attempt_at IS NULL OR NEW.next_evaluation_at IS NULL THEN RAISE EXCEPTION 'rule not evaluable'; END IF;
 IF OLD.last_attempt_at IS NOT NULL AND NEW.last_attempt_at<OLD.last_attempt_at AND NEW.last_error_code IS DISTINCT FROM 'clock_regression' THEN RAISE EXCEPTION 'attempt clock regression'; END IF;
 IF NEW.last_error_code IS NULL THEN
  IF NEW.last_success_at IS DISTINCT FROM NEW.last_attempt_at OR NEW.evaluated_revision IS DISTINCT FROM r.configuration_revision
   OR NOT alert_sample(NEW.last_sample,r.kind) OR NEW.last_success_at IS DISTINCT FROM (NEW.last_sample->>'as_of')::timestamptz
   OR (OLD.last_success_at IS NOT NULL AND NEW.last_success_at<OLD.last_success_at)
   OR NEW.next_evaluation_at IS DISTINCT FROM NEW.last_success_at+interval '60 seconds'
   OR NOT (NEW.episode_generation=OLD.episode_generation OR
      (OLD.episode_generation<9223372036854775807 AND NEW.episode_generation=OLD.episode_generation+1)) THEN RAISE EXCEPTION 'invalid successful evaluation'; END IF;
 ELSE
  IF (NEW.last_success_at,NEW.evaluated_revision,NEW.last_sample,NEW.current_episode_id,NEW.episode_generation)
    IS DISTINCT FROM (OLD.last_success_at,OLD.evaluated_revision,OLD.last_sample,OLD.current_episode_id,OLD.episode_generation)
    OR NEW.next_evaluation_at IS DISTINCT FROM NEW.last_attempt_at+interval '30 seconds' THEN RAISE EXCEPTION 'failed evaluation changed success'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_runtime_guard BEFORE INSERT OR UPDATE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION alert_guard_runtime();

CREATE FUNCTION alert_guard_episode() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r alert_rules; changed_ack BOOLEAN; changed_resolve BOOLEAN;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=NEW.rule_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing rule'; END IF;
 IF TG_OP='INSERT' THEN
  IF r.state<>'enabled' OR NEW.rule_revision IS DISTINCT FROM r.configuration_revision
    OR NEW.rule_snapshot IS DISTINCT FROM jsonb_build_object('kind',r.kind,'scope_type',r.scope_type,'scope_id',r.scope_id,'label',r.label,'parameters',r.parameters)
    OR NEW.acknowledged_by IS NOT NULL OR NEW.resolved_at IS NOT NULL
    OR NEW.latest_sample IS DISTINCT FROM NEW.initial_sample OR NEW.last_seen_at IS DISTINCT FROM NEW.opened_at
    OR alert_decision(NEW.rule_snapshot,NEW.initial_sample,false) IS DISTINCT FROM 'breach' THEN RAISE EXCEPTION 'invalid episode opening'; END IF;
  NEW.action_revision:=1;
 ELSE
  IF (NEW.id,NEW.rule_id,NEW.generation,NEW.rule_revision,NEW.rule_snapshot,NEW.opened_at,NEW.initial_sample)
    IS DISTINCT FROM (OLD.id,OLD.rule_id,OLD.generation,OLD.rule_revision,OLD.rule_snapshot,OLD.opened_at,OLD.initial_sample)
    OR OLD.resolved_at IS NOT NULL OR NEW.last_seen_at<OLD.last_seen_at THEN RAISE EXCEPTION 'immutable episode'; END IF;
  changed_ack:=(NEW.acknowledged_by,NEW.acknowledged_at,NEW.acknowledged_command_id)
    IS DISTINCT FROM (OLD.acknowledged_by,OLD.acknowledged_at,OLD.acknowledged_command_id);
  changed_resolve:=(NEW.resolved_at,NEW.resolve_reason,NEW.resolved_command_id)
    IS DISTINCT FROM (OLD.resolved_at,OLD.resolve_reason,OLD.resolved_command_id);
  IF changed_ack THEN
   IF OLD.acknowledged_by IS NOT NULL OR NEW.acknowledged_by IS NULL OR NEW.acknowledged_at IS NULL
    OR NOT alert_require_command(NEW.acknowledged_command_id,'acknowledge',NEW.id,NEW.acknowledged_by)
    OR changed_resolve OR (NEW.latest_sample,NEW.last_seen_at) IS DISTINCT FROM (OLD.latest_sample,OLD.last_seen_at)
    THEN RAISE EXCEPTION 'invalid acknowledgement'; END IF;
  END IF;
  IF changed_resolve THEN
   IF NEW.resolved_at IS NULL THEN RAISE EXCEPTION 'invalid resolution'; END IF;
   IF NEW.resolve_reason='recovered' THEN
    IF NEW.resolved_command_id IS NOT NULL OR NEW.resolved_at IS DISTINCT FROM NEW.last_seen_at
      OR alert_decision(NEW.rule_snapshot,NEW.latest_sample,true) IS DISTINCT FROM 'clear' THEN RAISE EXCEPTION 'invalid recovery'; END IF;
   ELSE
    IF (NEW.latest_sample,NEW.last_seen_at) IS DISTINCT FROM (OLD.latest_sample,OLD.last_seen_at)
      OR NEW.resolved_command_id IS DISTINCT FROM r.last_command_id
      OR NEW.rule_revision>=r.configuration_revision
      OR NEW.resolved_at IS DISTINCT FROM r.updated_at
      OR NEW.resolve_reason IS DISTINCT FROM (CASE WHEN r.state='retired' THEN 'rule_retired' WHEN r.state='disabled' THEN 'rule_disabled' ELSE 'rule_changed' END)
      OR NOT alert_require_command(NEW.resolved_command_id,CASE WHEN r.state='retired' THEN 'rule_retire' ELSE 'rule_configure' END,r.id,r.updated_by)
      THEN RAISE EXCEPTION 'invalid configuration close'; END IF;
   END IF;
  END IF;
  NEW.action_revision:=1+(NEW.acknowledged_at IS NOT NULL)::int+(NEW.resolved_at IS NOT NULL)::int;
 END IF;
 IF NEW.resolved_at IS NULL AND alert_decision(NEW.rule_snapshot,NEW.latest_sample,true) NOT IN ('breach','hold') THEN RAISE EXCEPTION 'clear sample cannot stay open'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_episode_guard BEFORE INSERT OR UPDATE ON alert_episodes FOR EACH ROW EXECUTE FUNCTION alert_guard_episode();

CREATE FUNCTION alert_check_episode(p_id TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE e alert_episodes; x alert_episode_events; n BIGINT; expected_n BIGINT; c alert_commands;
BEGIN
 SELECT * INTO e FROM alert_episodes WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'event has no episode'; END IF;
 expected_n:=1+(e.acknowledged_at IS NOT NULL)::int+(e.resolved_at IS NOT NULL)::int;
 SELECT count(*) INTO n FROM alert_episode_events WHERE episode_id=e.id;
 IF n IS DISTINCT FROM expected_n THEN RAISE EXCEPTION 'incomplete episode history'; END IF;
 SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=1;
 IF NOT FOUND OR (x.type,x.actor,x.observed_at,x.safe_sample,x.command_id)
   IS DISTINCT FROM ('opened'::text,'system'::text,e.opened_at,e.initial_sample,NULL::text) THEN RAISE EXCEPTION 'opened history mismatch'; END IF;
 IF e.acknowledged_at IS NOT NULL THEN
  SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=2;
  IF NOT FOUND OR (x.type,x.actor,x.observed_at,x.safe_sample,x.command_id)
    IS DISTINCT FROM ('acknowledged'::text,e.acknowledged_by,e.acknowledged_at,NULL::jsonb,e.acknowledged_command_id) THEN RAISE EXCEPTION 'ack history mismatch'; END IF;
  SELECT * INTO c FROM alert_commands WHERE operation_id=e.acknowledged_command_id;
  IF NOT FOUND OR c.status<>'applied' OR c.kind<>'acknowledge' OR c.target_id IS DISTINCT FROM e.id
    OR c.resource_id IS DISTINCT FROM e.id OR c.actor IS DISTINCT FROM e.acknowledged_by OR c.result_revision<>2 THEN RAISE EXCEPTION 'ack receipt mismatch'; END IF;
 END IF;
 IF e.resolved_at IS NOT NULL THEN
  SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=e.action_revision;
  IF NOT FOUND OR x.type IS DISTINCT FROM e.resolve_reason OR x.observed_at IS DISTINCT FROM e.resolved_at
    OR x.command_id IS DISTINCT FROM e.resolved_command_id THEN RAISE EXCEPTION 'resolve history mismatch'; END IF;
  IF e.resolve_reason='recovered' THEN
   IF x.actor IS DISTINCT FROM 'system' OR x.safe_sample IS DISTINCT FROM e.latest_sample THEN RAISE EXCEPTION 'recovery sample mismatch'; END IF;
  ELSE
   SELECT * INTO c FROM alert_commands WHERE operation_id=e.resolved_command_id;
   IF NOT FOUND OR c.status<>'applied' OR c.target_id IS DISTINCT FROM e.rule_id OR c.resource_id IS DISTINCT FROM e.rule_id
    OR c.kind IS DISTINCT FROM (CASE WHEN e.resolve_reason='rule_retired' THEN 'rule_retire' ELSE 'rule_configure' END)
    OR x.actor IS DISTINCT FROM c.actor OR x.safe_sample IS NOT NULL THEN RAISE EXCEPTION 'configuration event receipt mismatch'; END IF;
  END IF;
 END IF;
END $$;
CREATE FUNCTION alert_episode_history_constraint() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e alert_episodes; t alert_rule_runtime;
BEGIN
 IF TG_TABLE_NAME='alert_episodes' THEN
  PERFORM alert_check_episode(NEW.id);
  SELECT * INTO e FROM alert_episodes WHERE id=NEW.id;
  IF e.resolve_reason='recovered' THEN
   SELECT * INTO t FROM alert_rule_runtime WHERE rule_id=e.rule_id;
   IF NOT FOUND OR t.last_sample IS DISTINCT FROM e.latest_sample OR t.last_success_at IS DISTINCT FROM e.last_seen_at
     OR t.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'recovery runtime mismatch'; END IF;
  END IF;
 ELSE
  PERFORM alert_check_episode(NEW.episode_id);
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER alert_episode_history AFTER INSERT OR UPDATE ON alert_episodes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_episode_history_constraint();
CREATE CONSTRAINT TRIGGER alert_event_history AFTER INSERT ON alert_episode_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_episode_history_constraint();

CREATE FUNCTION alert_check_rule(p_id TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE r alert_rules; t alert_rule_runtime; e alert_episodes; max_generation BIGINT; c alert_commands;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing alert rule'; END IF;
 SELECT * INTO t FROM alert_rule_runtime WHERE rule_id=p_id;
 IF NOT FOUND OR t.configured_revision IS DISTINCT FROM r.configuration_revision THEN RAISE EXCEPTION 'missing/stale runtime'; END IF;
 SELECT * INTO c FROM alert_commands WHERE operation_id=r.last_command_id;
 IF NOT FOUND OR c.status<>'applied' OR c.actor IS DISTINCT FROM r.updated_by OR c.resource_id IS DISTINCT FROM r.id
  OR c.result_revision IS DISTINCT FROM r.configuration_revision
  OR c.kind NOT IN ('rule_create','rule_configure','rule_retire') THEN RAISE EXCEPTION 'rule receipt mismatch'; END IF;
 SELECT generation INTO max_generation FROM alert_episodes WHERE rule_id=p_id ORDER BY generation DESC LIMIT 1;
 IF t.episode_generation IS DISTINCT FROM COALESCE(max_generation,0) THEN RAISE EXCEPTION 'generation mismatch'; END IF;
 SELECT * INTO e FROM alert_episodes WHERE rule_id=p_id AND resolved_at IS NULL;
 IF FOUND THEN
  IF t.current_episode_id IS DISTINCT FROM e.id OR e.rule_revision IS DISTINCT FROM r.configuration_revision OR r.state<>'enabled'
    OR t.last_sample IS DISTINCT FROM e.latest_sample OR t.last_success_at IS DISTINCT FROM e.last_seen_at THEN RAISE EXCEPTION 'active pointer/sample mismatch'; END IF;
 ELSE
  IF t.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'orphan current pointer'; END IF;
 END IF;
 IF r.state<>'enabled' AND t.next_evaluation_at IS NOT NULL THEN RAISE EXCEPTION 'inactive due time'; END IF;
 IF t.last_success_at IS NOT NULL THEN
  IF t.evaluated_revision IS DISTINCT FROM r.configuration_revision OR NOT alert_sample(t.last_sample,r.kind)
    OR t.last_success_at IS DISTINCT FROM (t.last_sample->>'as_of')::timestamptz THEN RAISE EXCEPTION 'runtime sample mismatch'; END IF;
  IF t.current_episode_id IS NULL AND alert_decision(jsonb_build_object('kind',r.kind,'scope_type',r.scope_type,'scope_id',r.scope_id,'label',r.label,'parameters',r.parameters),t.last_sample,false)='breach'
    THEN RAISE EXCEPTION 'breach has no episode'; END IF;
 END IF;
END $$;
CREATE FUNCTION alert_rule_constraint() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='alert_rules' THEN PERFORM alert_check_rule(NEW.id);
 ELSE PERFORM alert_check_rule(NEW.rule_id); END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER alert_rule_consistent AFTER INSERT OR UPDATE ON alert_rules DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();
CREATE CONSTRAINT TRIGGER alert_runtime_consistent AFTER INSERT OR UPDATE ON alert_rule_runtime DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();
CREATE CONSTRAINT TRIGGER alert_episode_runtime_consistent AFTER INSERT OR UPDATE ON alert_episodes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();

CREATE FUNCTION alert_command_constraint() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c alert_commands; r alert_rules; e alert_episodes;
BEGIN
 SELECT * INTO c FROM alert_commands WHERE operation_id=NEW.operation_id;
 IF c.status IS DISTINCT FROM 'applied' AND c.status IS DISTINCT FROM 'rejected' THEN RAISE EXCEPTION 'uncommitted alert claim'; END IF;
 IF c.status='rejected' THEN
  IF EXISTS(SELECT 1 FROM alert_rules WHERE last_command_id=c.operation_id)
    OR EXISTS(SELECT 1 FROM alert_episodes WHERE acknowledged_command_id=c.operation_id OR resolved_command_id=c.operation_id)
    OR EXISTS(SELECT 1 FROM alert_episode_events WHERE command_id=c.operation_id) THEN RAISE EXCEPTION 'rejected command has effect'; END IF;
  RETURN NULL;
 END IF;
 IF c.kind='acknowledge' THEN
  SELECT * INTO e FROM alert_episodes WHERE id=c.resource_id;
  IF NOT FOUND OR e.id IS DISTINCT FROM c.target_id OR e.acknowledged_command_id IS DISTINCT FROM c.operation_id
    OR e.acknowledged_by IS DISTINCT FROM c.actor OR c.result_revision IS DISTINCT FROM 2::bigint THEN RAISE EXCEPTION 'ack command has no effect'; END IF;
  PERFORM alert_check_episode(e.id);
 ELSE
  SELECT * INTO r FROM alert_rules WHERE id=c.resource_id;
  IF NOT FOUND OR c.result_revision IS DISTINCT FROM r.configuration_revision
    OR (c.kind<>'rule_create' AND r.id IS DISTINCT FROM c.target_id)
    OR (c.kind='rule_create' AND (r.last_command_id IS DISTINCT FROM c.operation_id OR r.created_by IS DISTINCT FROM c.actor OR r.configuration_revision<>1))
    OR (c.kind='rule_retire' AND (r.last_command_id IS DISTINCT FROM c.operation_id OR r.state<>'retired'))
    OR (c.kind='rule_configure' AND r.state='retired') THEN RAISE EXCEPTION 'rule command mismatch'; END IF;
  -- configure可为CAS正确的no-op；若不是当前last_command，不允许本命令另有episode/event效果。
  IF c.kind='rule_configure' AND r.last_command_id IS DISTINCT FROM c.operation_id
    AND (EXISTS(SELECT 1 FROM alert_episodes WHERE resolved_command_id=c.operation_id)
      OR EXISTS(SELECT 1 FROM alert_episode_events WHERE command_id=c.operation_id)) THEN RAISE EXCEPTION 'no-op command has state effect'; END IF;
  PERFORM alert_check_rule(r.id);
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER alert_command_terminal AFTER INSERT OR UPDATE ON alert_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_command_constraint();

CREATE TRIGGER alert_control_immutable BEFORE UPDATE OR DELETE ON alert_control FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_rules_no_delete BEFORE DELETE ON alert_rules FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_runtime_no_delete BEFORE DELETE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_episodes_no_delete BEFORE DELETE ON alert_episodes FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_events_immutable BEFORE UPDATE OR DELETE ON alert_episode_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

REVOKE ALL ON alert_control,alert_commands,alert_rules,alert_rule_runtime,alert_episodes,alert_episode_events FROM PUBLIC;


-- Resolve all alert guard relations and helpers in the actual migration schema.
DO $$
DECLARE f RECORD; own_schema TEXT:=current_schema(); n INTEGER:=0;
BEGIN
 FOR f IN SELECT p.oid FROM pg_catalog.pg_proc p
  JOIN pg_catalog.pg_namespace ns ON ns.oid=p.pronamespace
  WHERE ns.nspname=own_schema AND p.proname=ANY(ARRAY[
   'alert_id','alert_keys','alert_uint','alert_signed','alert_label','alert_time',
   'alert_parameters','alert_snapshot','alert_sample','alert_decision',
   'alert_require_command','alert_guard_command','alert_guard_rule','alert_guard_runtime',
   'alert_guard_episode','alert_check_episode','alert_episode_history_constraint',
   'alert_check_rule','alert_rule_constraint','alert_command_constraint'])
 LOOP
  EXECUTE format('ALTER FUNCTION %s SET search_path = %I, pg_catalog, pg_temp',
   f.oid::regprocedure,own_schema);
  n:=n+1;
 END LOOP;
 IF n<>20 THEN RAISE EXCEPTION 'alert function set mismatch'; END IF;
END $$;
-- END 019_operations_alerts.sql
-- BEGIN 020_periodic_budgets.sql
-- 023-B core integration. Append-only migration; no default controls or legacy backfill.
-- 023-B0 single authoritative database clock; no GUC/client input.
CREATE FUNCTION pb_now() RETURNS timestamptz LANGUAGE sql VOLATILE AS $$ SELECT pg_catalog.clock_timestamp() $$;
-- 023-B0 isolated prototype. NOT a production migration, NOT frozen.
CREATE FUNCTION pb_uuid(v text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v<>'00000000-0000-0000-0000-000000000000',false)
$$;
CREATE FUNCTION pb_start(kind text, at_time timestamptz) RETURNS timestamptz LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE kind WHEN 'utc_day' THEN date_trunc('day',at_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN date_trunc('month',at_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' END
$$;
CREATE FUNCTION pb_end(kind text, start_time timestamptz) RETURNS timestamptz LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE kind WHEN 'utc_day' THEN (start_time AT TIME ZONE 'UTC'+interval '1 day') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN (start_time AT TIME ZONE 'UTC'+interval '1 month') AT TIME ZONE 'UTC' END
$$;
CREATE FUNCTION pb_current_tuple(stamp xid8, tuple_xid xid) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
DECLARE full_xid numeric;
BEGIN
 IF stamp IS DISTINCT FROM pg_current_xact_id() OR tuple_xid IS NULL THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM pg_locks WHERE locktype='transactionid' AND transactionid=tuple_xid
   AND pid=pg_backend_pid() AND granted AND mode='ExclusiveLock') THEN RETURN true; END IF;
 full_xid:=floor(stamp::text::numeric/4294967296)*4294967296+tuple_xid::text::numeric;
 IF full_xid<stamp::text::numeric THEN full_xid:=full_xid+4294967296; END IF;
 IF full_xid<=stamp::text::numeric THEN RETURN false; END IF;
 BEGIN RETURN pg_xact_status(full_xid::text::xid8) IS NOT DISTINCT FROM 'in progress';
 EXCEPTION WHEN invalid_parameter_value THEN RETURN false; END;
END $$;

-- First-configuration synchronization is a separate two-int advisory domain.
-- A collision only serializes unrelated scopes; no business root is acquired
-- here. READ COMMITTED is essential: a waiter must see the committed control
-- in subsequent VOLATILE/deferred checks instead of reusing an old RR snapshot.
CREATE FUNCTION pb_require_read_committed() RETURNS void LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'periodic source requires read committed' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION pb_scope_sync(scope_name text,target_id text,exclusive_lock boolean) RETURNS void LANGUAGE plpgsql VOLATILE AS $$
DECLARE lock_key integer;
BEGIN
 PERFORM pb_require_read_committed();
 IF scope_name NOT IN ('tenant','key','procurement') OR NOT pb_uuid(target_id) OR exclusive_lock IS NULL THEN
  RAISE EXCEPTION 'periodic invalid synchronization scope' USING ERRCODE='23514'; END IF;
 lock_key:=hashtext(current_schema()||':'||scope_name||':'||target_id);
 IF exclusive_lock THEN PERFORM pg_advisory_xact_lock(23023023,lock_key);
 ELSE PERFORM pg_advisory_xact_lock_shared(23023023,lock_key); END IF;
END $$;

CREATE TABLE periodic_budget_controls (
 id text PRIMARY KEY CHECK(pb_uuid(id)),
 scope text NOT NULL CHECK(scope IN ('tenant','key','procurement')),
 tenant_id text REFERENCES tenants(id), key_id text, account_id text REFERENCES procurement_accounts(id),
 period_kind text NOT NULL CHECK(period_kind IN ('utc_day','utc_month')),
 enabled boolean NOT NULL, limit_amount bigint NOT NULL CHECK(limit_amount>=0),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 tracking_started_at timestamptz NOT NULL DEFAULT pb_now(), effective_from timestamptz NOT NULL,
 max_observed_window_start timestamptz NOT NULL,
 held_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 event_seq bigint NOT NULL DEFAULT 0 CHECK(event_seq>=0), last_event_id text,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 FOREIGN KEY(tenant_id,key_id) REFERENCES api_keys(tenant_id,id),
 CHECK((scope='tenant' AND tenant_id IS NOT NULL AND key_id IS NULL AND account_id IS NULL)
 OR (scope='key' AND tenant_id IS NOT NULL AND key_id IS NOT NULL AND account_id IS NULL)
 OR (scope='procurement' AND tenant_id IS NULL AND key_id IS NULL AND account_id IS NOT NULL)),
 CHECK(isfinite(tracking_started_at) AND isfinite(effective_from) AND isfinite(max_observed_window_start)),
 CHECK(effective_from=pb_end(period_kind,pb_start(period_kind,tracking_started_at)))
);
CREATE UNIQUE INDEX pb_tenant_scope ON periodic_budget_controls(tenant_id) WHERE scope='tenant';
CREATE UNIQUE INDEX pb_key_scope ON periodic_budget_controls(key_id) WHERE scope='key';
CREATE UNIQUE INDEX pb_account_scope ON periodic_budget_controls(account_id) WHERE scope='procurement';
CREATE FUNCTION pb_sync_unconfigured(scope_name text,target_id text) RETURNS void LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 PERFORM pb_require_read_committed();
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_controls WHERE scope=scope_name AND
   CASE scope_name WHEN 'tenant' THEN tenant_id WHEN 'key' THEN key_id WHEN 'procurement' THEN account_id END=target_id) THEN
  PERFORM pb_scope_sync(scope_name,target_id,false);
 END IF;
END $$;
CREATE TABLE periodic_budget_windows (
 id text PRIMARY KEY CHECK(pb_uuid(id)), control_id text NOT NULL REFERENCES periodic_budget_controls(id),
 start_at timestamptz NOT NULL, end_at timestamptz NOT NULL,
 held_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0), last_event_id text,
 UNIQUE(control_id,start_at), UNIQUE(control_id,id)
);
CREATE TABLE periodic_budget_allocations (
 id text PRIMARY KEY CHECK(pb_uuid(id)), control_id text NOT NULL REFERENCES periodic_budget_controls(id),
 subject_type text NOT NULL CHECK(subject_type IN ('customer_request','supplier_attempt')),
 subject_id text NOT NULL CHECK(pb_uuid(subject_id)),
 origin text NOT NULL CHECK(origin IN ('new','legacy_import','legacy_catchup')),
 origin_source_id text NOT NULL CHECK(pb_uuid(origin_source_id)),
 window_id text, admitted_at timestamptz,
 upper_amount bigint NOT NULL CHECK(upper_amount>=0),
 baseline numeric(100,0) NOT NULL CHECK(baseline>=0),
 source_held numeric(100,0) NOT NULL DEFAULT 0 CHECK(source_held>=0),
 source_spent numeric(100,0) NOT NULL CHECK(source_spent>=0),
 held_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 state text NOT NULL DEFAULT 'held' CHECK(state IN ('held','disputed','settled','released')),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0), last_event_id text,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), created_at timestamptz NOT NULL DEFAULT pb_now(),
 UNIQUE(control_id,subject_type,subject_id), UNIQUE(control_id,id),
 FOREIGN KEY(control_id,window_id) REFERENCES periodic_budget_windows(control_id,id),
 CHECK((origin='new' AND window_id IS NOT NULL AND admitted_at IS NOT NULL AND isfinite(admitted_at))
 OR (origin<>'new' AND window_id IS NULL AND admitted_at IS NULL)),
 CHECK(subject_type<>'customer_request' OR (baseline=0 AND state<>'disputed')),
 CHECK(origin<>'legacy_catchup' OR subject_type='supplier_attempt')
);
CREATE INDEX pb_allocation_subject ON periodic_budget_allocations(subject_type,subject_id,control_id);
CREATE INDEX pb_allocation_page ON periodic_budget_allocations(control_id,created_at DESC,id DESC);
CREATE TABLE periodic_budget_events (
 id text PRIMARY KEY CHECK(pb_uuid(id)), control_id text NOT NULL,
 allocation_id text NOT NULL, source_kind text NOT NULL CHECK(source_kind IN ('reservation','settlement','procurement_event','legacy_import')),
 source_id text NOT NULL CHECK(pb_uuid(source_id)),
 seq bigint NOT NULL, allocation_before_revision bigint NOT NULL, window_before_revision bigint,
 before_held numeric(100,0) NOT NULL, before_spent numeric(100,0) NOT NULL,
 after_held numeric(100,0) NOT NULL, after_spent numeric(100,0) NOT NULL,
 after_source_spent numeric(100,0) NOT NULL,
 after_state text NOT NULL CHECK(after_state IN ('held','disputed','settled','released')),
 held_delta numeric(100,0) NOT NULL, spent_delta numeric(100,0) NOT NULL,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), created_at timestamptz NOT NULL DEFAULT pb_now(),
 UNIQUE(control_id,seq), UNIQUE(control_id,source_kind,source_id),
 FOREIGN KEY(control_id,allocation_id) REFERENCES periodic_budget_allocations(control_id,id)
);
CREATE INDEX pb_procurement_source_events ON procurement_budget_events(attempt_id,id);

ALTER TABLE request_records ADD COLUMN pb_created_xid xid8;
ALTER TABLE request_records ADD COLUMN pb_tenant_control text;
ALTER TABLE request_records ADD COLUMN pb_key_control text;
ALTER TABLE request_records ADD COLUMN pb_admitted_at timestamptz;
ALTER TABLE upstream_attempts ADD COLUMN pb_created_xid xid8;
ALTER TABLE upstream_attempts ADD COLUMN pb_dispatch_checked_at timestamptz(6)
 CHECK(pb_dispatch_checked_at IS NULL OR isfinite(pb_dispatch_checked_at));
ALTER TABLE procurement_reservations ADD COLUMN pb_created_xid xid8;
ALTER TABLE procurement_reservations ADD COLUMN pb_control text;
ALTER TABLE procurement_reservations ADD COLUMN pb_admitted_at timestamptz;
ALTER TABLE procurement_budget_events ADD COLUMN pb_created_xid xid8;

CREATE FUNCTION pb_stamp_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
  PERFORM id FROM api_keys WHERE tenant_id=NEW.tenant_id AND id=NEW.key_id FOR UPDATE;
  PERFORM pb_sync_unconfigured('tenant',NEW.tenant_id);
  PERFORM pb_sync_unconfigured('key',NEW.key_id);
  NEW.pb_created_xid:=pg_current_xact_id();
  SELECT id INTO NEW.pb_tenant_control FROM periodic_budget_controls WHERE scope='tenant' AND tenant_id=NEW.tenant_id;
  SELECT id INTO NEW.pb_key_control FROM periodic_budget_controls WHERE scope='key' AND key_id=NEW.key_id AND tenant_id=NEW.tenant_id;
  NEW.pb_admitted_at:=pb_now();
 ELSIF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=OLD.id) THEN
   RAISE EXCEPTION 'periodic source cannot be deleted' USING ERRCODE='23514'; END IF; RETURN OLD;
 ELSE
  PERFORM pb_require_read_committed();
  IF (NEW.pb_created_xid,NEW.pb_tenant_control,NEW.pb_key_control,NEW.pb_admitted_at)
   IS DISTINCT FROM (OLD.pb_created_xid,OLD.pb_tenant_control,OLD.pb_key_control,OLD.pb_admitted_at) THEN
   RAISE EXCEPTION 'periodic source creation metadata immutable' USING ERRCODE='23514'; END IF;
  IF (NEW.id,NEW.tenant_id,NEW.key_id) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.key_id)
   AND EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=OLD.id) THEN
   RAISE EXCEPTION 'periodic source identity immutable' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER pb_request_stamp BEFORE INSERT OR UPDATE OR DELETE ON request_records FOR EACH ROW EXECUTE FUNCTION pb_stamp_request();
CREATE FUNCTION pb_stamp_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.pb_created_xid:=pg_current_xact_id();
 ELSIF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=OLD.id) THEN
   RAISE EXCEPTION 'periodic source cannot be deleted' USING ERRCODE='23514'; END IF; RETURN OLD;
 ELSE
  IF NEW.pb_created_xid IS DISTINCT FROM OLD.pb_created_xid OR
   ((NEW.id,NEW.tenant_id,NEW.request_id) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.request_id)
    AND EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=OLD.id)) THEN
   RAISE EXCEPTION 'periodic source identity immutable' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER pb_attempt_stamp BEFORE INSERT OR UPDATE OR DELETE ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION pb_stamp_attempt();
CREATE FUNCTION pb_stamp_procurement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_stamp xid8; tuple_xid xid;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state IS DISTINCT FROM 'prepared' OR NEW.held_amount IS DISTINCT FROM NEW.upper_amount::numeric
    OR NEW.spent_amount IS DISTINCT FROM 0::numeric OR NEW.anomaly IS DISTINCT FROM false THEN
   RAISE EXCEPTION 'periodic new procurement requires original upper reservation' USING ERRCODE='23514'; END IF;
  SELECT pb_created_xid,xmin INTO source_stamp,tuple_xid FROM upstream_attempts WHERE id=NEW.attempt_id;
  IF NOT pb_current_tuple(source_stamp,tuple_xid) THEN RAISE EXCEPTION 'new procurement requires same-tx attempt' USING ERRCODE='23514'; END IF;
  PERFORM id FROM procurement_accounts WHERE id=NEW.account_id FOR NO KEY UPDATE;
  PERFORM pb_sync_unconfigured('procurement',NEW.account_id);
  NEW.pb_created_xid:=pg_current_xact_id();
  SELECT id INTO NEW.pb_control FROM periodic_budget_controls WHERE scope='procurement' AND account_id=NEW.account_id;
  NEW.pb_admitted_at:=pb_now();
 ELSE
  PERFORM pb_sync_unconfigured('procurement',OLD.account_id);
  IF (NEW.pb_created_xid,NEW.pb_control,NEW.pb_admitted_at) IS DISTINCT FROM (OLD.pb_created_xid,OLD.pb_control,OLD.pb_admitted_at) THEN
   RAISE EXCEPTION 'periodic procurement creation metadata immutable' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER pb_procurement_stamp BEFORE INSERT OR UPDATE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION pb_stamp_procurement();
CREATE FUNCTION pb_stamp_procurement_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.pb_created_xid:=pg_current_xact_id(); RETURN NEW; END $$;
CREATE TRIGGER pb_procurement_event_stamp BEFORE INSERT ON procurement_budget_events FOR EACH ROW EXECUTE FUNCTION pb_stamp_procurement_event();

CREATE FUNCTION pb_guard_control() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e periodic_budget_events%ROWTYPE;legacy_count integer;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.scope='tenant' THEN PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR NO KEY UPDATE;
  ELSIF NEW.scope='key' THEN
   PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
   PERFORM id FROM api_keys WHERE tenant_id=NEW.tenant_id AND id=NEW.key_id FOR UPDATE;
  ELSE PERFORM id FROM procurement_accounts WHERE id=NEW.account_id FOR NO KEY UPDATE; END IF;
  PERFORM pb_scope_sync(NEW.scope,coalesce(NEW.account_id,NEW.key_id,NEW.tenant_id),true);
  IF NEW.scope='procurement' THEN
   SELECT count(*) INTO legacy_count FROM (SELECT 1 FROM procurement_reservations WHERE account_id=NEW.account_id
    AND state IN ('prepared','sending','unknown','disputed') LIMIT 1001) candidates;
  ELSE
   SELECT count(*) INTO legacy_count FROM (SELECT 1 FROM reservations v JOIN request_records r ON r.id=v.request_id
    WHERE r.tenant_id=NEW.tenant_id AND (NEW.scope='tenant' OR r.key_id=NEW.key_id)
    AND v.status IN ('reserved','pending_reconciliation') LIMIT 1001) candidates;
  END IF;
  IF legacy_count>1000 THEN RAISE EXCEPTION 'periodic legacy import requires maintenance' USING ERRCODE='23514';END IF;
  NEW.created_xid:=pg_current_xact_id(); NEW.tracking_started_at:=pb_now();
  NEW.effective_from:=pb_end(NEW.period_kind,pb_start(NEW.period_kind,NEW.tracking_started_at));
  NEW.max_observed_window_start:=pb_start(NEW.period_kind,NEW.tracking_started_at);
  NEW.revision:=1; NEW.held_amount:=0;NEW.spent_amount:=0;NEW.event_seq:=0;NEW.last_event_id:=NULL;
 ELSE
  PERFORM pb_require_read_committed();
  IF (NEW.id,NEW.scope,NEW.tenant_id,NEW.key_id,NEW.account_id,NEW.period_kind,NEW.tracking_started_at,NEW.effective_from,NEW.created_xid)
   IS DISTINCT FROM (OLD.id,OLD.scope,OLD.tenant_id,OLD.key_id,OLD.account_id,OLD.period_kind,OLD.tracking_started_at,OLD.effective_from,OLD.created_xid) THEN
   RAISE EXCEPTION 'periodic control identity/calendar immutable' USING ERRCODE='23514'; END IF;
  IF (NEW.held_amount,NEW.spent_amount,NEW.event_seq,NEW.last_event_id)
   IS DISTINCT FROM (OLD.held_amount,OLD.spent_amount,OLD.event_seq,OLD.last_event_id) THEN
   SELECT * INTO e FROM periodic_budget_events WHERE id=NEW.last_event_id AND control_id=OLD.id;
   IF e.created_xid IS DISTINCT FROM pg_current_xact_id() OR e.seq IS DISTINCT FROM OLD.event_seq+1
    OR NEW.event_seq IS DISTINCT FROM e.seq OR NEW.held_amount IS DISTINCT FROM OLD.held_amount+e.held_delta
    OR NEW.spent_amount IS DISTINCT FROM OLD.spent_amount+e.spent_delta THEN
    RAISE EXCEPTION 'periodic control update requires new exact event' USING ERRCODE='23514'; END IF;
  END IF;
  IF NEW.max_observed_window_start IS DISTINCT FROM OLD.max_observed_window_start AND
   (NEW.max_observed_window_start<OLD.max_observed_window_start OR NEW.max_observed_window_start IS DISTINCT FROM pb_start(OLD.period_kind,NEW.max_observed_window_start)
   OR NEW.max_observed_window_start>pb_start(OLD.period_kind,pb_now())) THEN
   RAISE EXCEPTION 'periodic clock regression/invalid highwater' USING ERRCODE='23514'; END IF;
  NEW.revision:=OLD.revision+CASE WHEN (NEW.enabled,NEW.limit_amount) IS DISTINCT FROM (OLD.enabled,OLD.limit_amount) THEN 1 ELSE 0 END;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER pb_control_guard BEFORE INSERT OR UPDATE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION pb_guard_control();
CREATE TRIGGER pb_control_no_delete BEFORE DELETE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION pb_guard_window() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;e periodic_budget_events%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
  IF NEW.start_at IS DISTINCT FROM pb_start(c.period_kind,NEW.start_at) OR NEW.end_at IS DISTINCT FROM pb_end(c.period_kind,NEW.start_at)
   OR NEW.start_at<c.max_observed_window_start OR NEW.start_at>pb_start(c.period_kind,pb_now()) THEN
   RAISE EXCEPTION 'periodic invalid/reopened window' USING ERRCODE='23514'; END IF;
  NEW.held_amount:=0;NEW.spent_amount:=0;NEW.revision:=0;NEW.last_event_id:=NULL;
 ELSE
  IF (NEW.id,NEW.control_id,NEW.start_at,NEW.end_at) IS DISTINCT FROM (OLD.id,OLD.control_id,OLD.start_at,OLD.end_at) THEN
   RAISE EXCEPTION 'periodic window identity immutable' USING ERRCODE='23514'; END IF;
  SELECT * INTO e FROM periodic_budget_events WHERE id=NEW.last_event_id AND control_id=OLD.control_id;
  IF e.created_xid IS DISTINCT FROM pg_current_xact_id() OR e.window_before_revision IS DISTINCT FROM OLD.revision
   OR NOT EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE id=e.allocation_id AND window_id=OLD.id)
   OR NEW.revision IS DISTINCT FROM OLD.revision+1 OR NEW.held_amount IS DISTINCT FROM OLD.held_amount+e.held_delta
   OR NEW.spent_amount IS DISTINCT FROM OLD.spent_amount+e.spent_delta THEN
   RAISE EXCEPTION 'periodic window update requires new exact event' USING ERRCODE='23514'; END IF;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER pb_window_guard BEFORE INSERT OR UPDATE ON periodic_budget_windows FOR EACH ROW EXECUTE FUNCTION pb_guard_window();
CREATE TRIGGER pb_window_no_delete BEFORE DELETE ON periodic_budget_windows FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION pb_guard_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE; r request_records%ROWTYPE; v reservations%ROWTYPE;
 p procurement_reservations%ROWTYPE; pe procurement_budget_events%ROWTYPE; e periodic_budget_events%ROWTYPE;
 tuple_xid xid; marker text; prior_h numeric;prior_s numeric;w periodic_budget_windows%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.control_id,NEW.subject_type,NEW.subject_id,NEW.origin,NEW.origin_source_id,NEW.window_id,NEW.admitted_at,
      NEW.upper_amount,NEW.baseline,NEW.created_xid,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.control_id,OLD.subject_type,OLD.subject_id,OLD.origin,OLD.origin_source_id,OLD.window_id,OLD.admitted_at,
      OLD.upper_amount,OLD.baseline,OLD.created_xid,OLD.created_at) THEN
   RAISE EXCEPTION 'periodic allocation identity immutable' USING ERRCODE='23514'; END IF;
  SELECT * INTO e FROM periodic_budget_events WHERE id=NEW.last_event_id AND control_id=OLD.control_id AND allocation_id=OLD.id;
  IF e.created_xid IS DISTINCT FROM pg_current_xact_id() OR e.allocation_before_revision IS DISTINCT FROM OLD.revision
   OR NEW.revision IS DISTINCT FROM OLD.revision+1 OR (NEW.held_amount,NEW.spent_amount,NEW.source_held,NEW.source_spent,NEW.state)
    IS DISTINCT FROM (e.after_held,e.after_spent,e.after_held,e.after_source_spent,e.after_state) THEN
   RAISE EXCEPTION 'periodic allocation update requires exact new event' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
 IF NEW.subject_type='customer_request' THEN
  SELECT * INTO STRICT r FROM request_records WHERE id=NEW.subject_id;
  SELECT * INTO STRICT v FROM reservations WHERE request_id=r.id AND tenant_id=r.tenant_id;
  IF NOT ((c.scope='tenant' AND c.tenant_id=r.tenant_id) OR (c.scope='key' AND c.tenant_id=r.tenant_id AND c.key_id=r.key_id)) THEN
   RAISE EXCEPTION 'periodic customer scope mismatch' USING ERRCODE='23514'; END IF;
  marker:=CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control ELSE r.pb_key_control END;
  NEW.upper_amount:=v.amount; NEW.baseline:=0;
  IF NEW.origin='new' THEN
   SELECT xmin INTO tuple_xid FROM request_records WHERE id=r.id;
   IF marker IS DISTINCT FROM c.id OR NOT pb_current_tuple(r.pb_created_xid,tuple_xid)
    OR NEW.origin_source_id IS DISTINCT FROM v.id OR v.status NOT IN ('reserved','pending_reconciliation') THEN
    RAISE EXCEPTION 'periodic normal customer requires same-tx source/control' USING ERRCODE='23514'; END IF;
   NEW.admitted_at:=r.pb_admitted_at;
  ELSIF NEW.origin='legacy_import' THEN
   IF marker IS NOT NULL OR v.status NOT IN ('reserved','pending_reconciliation') OR NEW.origin_source_id IS DISTINCT FROM r.id THEN
    RAISE EXCEPTION 'periodic invalid legacy customer' USING ERRCODE='23514'; END IF;
  ELSE RAISE EXCEPTION 'periodic customer catchup forbidden' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT * INTO STRICT p FROM procurement_reservations WHERE attempt_id=NEW.subject_id;
  IF c.scope IS DISTINCT FROM 'procurement' OR c.account_id IS DISTINCT FROM p.account_id THEN
   RAISE EXCEPTION 'periodic supplier scope mismatch' USING ERRCODE='23514'; END IF;
  marker:=p.pb_control;NEW.upper_amount:=p.upper_amount;NEW.baseline:=0;
  IF NEW.origin='new' THEN
   SELECT xmin INTO tuple_xid FROM procurement_reservations WHERE attempt_id=p.attempt_id;
   IF marker IS DISTINCT FROM c.id OR NOT pb_current_tuple(p.pb_created_xid,tuple_xid)
    OR p.state IS DISTINCT FROM 'prepared' OR p.held_amount IS DISTINCT FROM p.upper_amount::numeric
    OR p.spent_amount IS DISTINCT FROM 0::numeric OR p.anomaly IS DISTINCT FROM false THEN
    RAISE EXCEPTION 'periodic normal supplier requires same-tx source/control' USING ERRCODE='23514'; END IF;
   NEW.admitted_at:=p.pb_admitted_at;
  ELSIF NEW.origin='legacy_import' THEN
   IF marker IS NOT NULL OR p.state NOT IN ('prepared','sending','unknown','disputed') OR NEW.origin_source_id IS DISTINCT FROM p.attempt_id THEN
    RAISE EXCEPTION 'periodic invalid legacy supplier' USING ERRCODE='23514'; END IF;
   NEW.baseline:=p.spent_amount;
  ELSE
   SELECT * INTO STRICT pe FROM procurement_budget_events WHERE id=NEW.origin_source_id;
   SELECT xmin INTO tuple_xid FROM procurement_budget_events WHERE id=pe.id;
   SELECT coalesce(sum(held_delta),0),coalesce(sum(spent_delta),0) INTO prior_h,prior_s
    FROM procurement_budget_events WHERE attempt_id=p.attempt_id AND account_id=p.account_id AND id<>pe.id;
   IF marker IS NOT NULL OR pe.attempt_id IS DISTINCT FROM p.attempt_id OR pe.account_id IS DISTINCT FROM p.account_id
    OR NOT pb_current_tuple(pe.pb_created_xid,tuple_xid) OR prior_h IS DISTINCT FROM 0::numeric OR prior_s<0
    OR (prior_h+pe.held_delta,prior_s+pe.spent_delta) IS DISTINCT FROM (p.held_amount,p.spent_amount) THEN
    RAISE EXCEPTION 'periodic catchup requires exact new source event and zero prior held' USING ERRCODE='23514'; END IF;
   NEW.baseline:=prior_s;
  END IF;
 END IF;
 IF NEW.origin='legacy_import' THEN
  SELECT xmin INTO tuple_xid FROM periodic_budget_controls WHERE id=c.id;
  IF NOT pb_current_tuple(c.created_xid,tuple_xid) THEN
   RAISE EXCEPTION 'periodic legacy import only in control creation tx' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.origin='new' THEN
  SELECT * INTO STRICT w FROM periodic_budget_windows WHERE control_id=c.id AND id=NEW.window_id;
  IF NEW.admitted_at IS NULL OR NOT isfinite(NEW.admitted_at) OR NEW.admitted_at<w.start_at OR NEW.admitted_at>=w.end_at
    OR w.start_at<c.max_observed_window_start OR NEW.admitted_at>pb_now() THEN
   RAISE EXCEPTION 'periodic invalid original admission window' USING ERRCODE='23514'; END IF;
 ELSE NEW.window_id:=NULL;NEW.admitted_at:=NULL; END IF;
 NEW.created_xid:=pg_current_xact_id();NEW.created_at:=pb_now();NEW.source_held:=0;NEW.source_spent:=NEW.baseline;
 NEW.held_amount:=0;NEW.spent_amount:=0;NEW.state:='held';NEW.revision:=0;NEW.last_event_id:=NULL;
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_allocation_guard BEFORE INSERT OR UPDATE ON periodic_budget_allocations FOR EACH ROW EXECUTE FUNCTION pb_guard_allocation();
CREATE TRIGGER pb_allocation_no_delete BEFORE DELETE ON periodic_budget_allocations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION pb_guard_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;a periodic_budget_allocations%ROWTYPE;v reservations%ROWTYPE;s settlements%ROWTYPE;
 p procurement_reservations%ROWTYPE;pe procurement_budget_events%ROWTYPE;w periodic_budget_windows%ROWTYPE;tuple_xid xid;
 target_h numeric; target_s numeric; target_state text;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT a FROM periodic_budget_allocations WHERE id=NEW.allocation_id AND control_id=c.id FOR UPDATE;
 -- A final charge cannot stand in for the original R admission: its held
 -- delta may be zero even when the original reservation exceeds the cap.
 IF a.revision=0 AND ((a.origin='legacy_import' AND NEW.source_kind<>'legacy_import')
  OR (a.subject_type='customer_request' AND a.origin='new' AND NEW.source_kind<>'reservation')) THEN
  RAISE EXCEPTION 'periodic original reserve event required first' USING ERRCODE='23514'; END IF;
 IF NEW.source_kind='legacy_import' THEN
  IF a.origin IS DISTINCT FROM 'legacy_import' OR a.revision<>0 OR NEW.source_id IS DISTINCT FROM a.subject_id THEN
   RAISE EXCEPTION 'periodic invalid legacy import event' USING ERRCODE='23514'; END IF;
  SELECT xmin INTO tuple_xid FROM periodic_budget_controls WHERE id=c.id;
  IF NOT pb_current_tuple(c.created_xid,tuple_xid) THEN RAISE EXCEPTION 'periodic legacy import tx required' USING ERRCODE='23514'; END IF;
  IF a.subject_type='customer_request' THEN
   SELECT * INTO STRICT v FROM reservations WHERE request_id=a.subject_id;
   target_h:=v.amount;target_s:=0;target_state:='held';
  ELSE
   SELECT * INTO STRICT p FROM procurement_reservations WHERE attempt_id=a.subject_id;
   IF p.spent_amount IS DISTINCT FROM a.baseline THEN RAISE EXCEPTION 'periodic legacy baseline changed' USING ERRCODE='23514'; END IF;
   target_h:=p.held_amount;target_s:=p.spent_amount;target_state:=CASE WHEN p.state='disputed' THEN 'disputed' ELSE 'held' END;
  END IF;
 ELSIF NEW.source_kind='reservation' THEN
  SELECT * INTO STRICT v FROM reservations WHERE id=NEW.source_id AND request_id=a.subject_id;
  IF a.subject_type IS DISTINCT FROM 'customer_request' OR a.origin IS DISTINCT FROM 'new' OR a.revision<>0
    OR a.origin_source_id IS DISTINCT FROM v.id OR v.status NOT IN ('reserved','pending_reconciliation') THEN
   RAISE EXCEPTION 'periodic invalid reservation effect' USING ERRCODE='23514'; END IF;
  target_h:=v.amount;target_s:=0;target_state:='held';
 ELSIF NEW.source_kind='settlement' THEN
  SELECT * INTO STRICT s FROM settlements WHERE id=NEW.source_id AND request_id=a.subject_id;
  SELECT * INTO STRICT v FROM reservations WHERE request_id=a.subject_id;
  IF a.subject_type IS DISTINCT FROM 'customer_request' OR a.revision<1 OR a.state<>'held' OR s.status IS DISTINCT FROM v.status
   OR (a.held_amount,a.source_held,a.spent_amount,a.source_spent) IS DISTINCT FROM (v.amount::numeric,v.amount::numeric,0::numeric,0::numeric)
   OR NOT EXISTS(SELECT 1 FROM periodic_budget_events original WHERE original.control_id=c.id AND original.allocation_id=a.id
    AND original.allocation_before_revision=0
    AND original.source_kind=CASE a.origin WHEN 'new' THEN 'reservation' ELSE 'legacy_import' END
    AND original.source_id=CASE a.origin WHEN 'new' THEN v.id ELSE a.subject_id END
    AND (original.before_held,original.before_spent,original.after_held,original.after_spent,original.after_source_spent,original.after_state)
     IS NOT DISTINCT FROM (0::numeric,0::numeric,v.amount::numeric,0::numeric,0::numeric,'held'::text)) THEN
   RAISE EXCEPTION 'periodic invalid settlement effect' USING ERRCODE='23514'; END IF;
  target_h:=0;target_s:=s.charged_amount;target_state:=s.status;
 ELSE
  SELECT * INTO STRICT pe FROM procurement_budget_events WHERE id=NEW.source_id;
  SELECT xmin INTO tuple_xid FROM procurement_budget_events WHERE id=pe.id;
  SELECT * INTO STRICT p FROM procurement_reservations WHERE attempt_id=a.subject_id;
  IF a.subject_type IS DISTINCT FROM 'supplier_attempt' OR pe.attempt_id IS DISTINCT FROM a.subject_id OR pe.account_id IS DISTINCT FROM c.account_id
    OR NOT pb_current_tuple(pe.pb_created_xid,tuple_xid)
    OR (a.source_held+pe.held_delta,a.source_spent+pe.spent_delta) IS DISTINCT FROM (p.held_amount,p.spent_amount) THEN
   RAISE EXCEPTION 'periodic supplier effect not exact same-tx source' USING ERRCODE='23514'; END IF;
  IF a.revision=0 AND a.origin IN ('new','legacy_catchup') AND a.origin_source_id IS DISTINCT FROM pe.id THEN
   RAISE EXCEPTION 'periodic initial procurement effect mismatch' USING ERRCODE='23514'; END IF;
  IF a.revision=0 AND a.origin='new' AND
    ((pe.type,pe.event_key,pe.held_delta,pe.spent_delta,p.state,p.held_amount,p.spent_amount,p.anomaly)
     IS DISTINCT FROM ('reserved'::text,'reserve:'||p.attempt_id,p.upper_amount::numeric,0::numeric,
                       'prepared'::text,p.upper_amount::numeric,0::numeric,false)) THEN
   RAISE EXCEPTION 'periodic original supplier reserve event required first' USING ERRCODE='23514'; END IF;
  target_h:=p.held_amount;target_s:=p.spent_amount;
  target_state:=CASE p.state WHEN 'settled' THEN 'settled' WHEN 'released' THEN 'released' WHEN 'disputed' THEN 'disputed' ELSE 'held' END;
 END IF;
 NEW.seq:=c.event_seq+1;NEW.allocation_before_revision:=a.revision;NEW.window_before_revision:=NULL;
 NEW.before_held:=a.held_amount;NEW.before_spent:=a.spent_amount;NEW.after_held:=target_h;
 NEW.after_source_spent:=target_s;NEW.after_spent:=greatest(target_s-a.baseline,0);NEW.after_state:=target_state;
 NEW.held_delta:=NEW.after_held-a.held_amount;NEW.spent_delta:=NEW.after_spent-a.spent_amount;
 IF a.window_id IS NOT NULL THEN
  SELECT * INTO STRICT w FROM periodic_budget_windows WHERE id=a.window_id AND control_id=c.id;
  NEW.window_before_revision:=w.revision;
  IF a.revision=0 THEN
   IF w.start_at<c.max_observed_window_start OR pb_start(c.period_kind,pb_now())<c.max_observed_window_start THEN
    RAISE EXCEPTION 'periodic clock regression' USING ERRCODE='23514'; END IF;
   IF c.enabled AND a.admitted_at>=c.effective_from AND w.spent_amount+c.held_amount+NEW.held_delta>c.limit_amount THEN
    RAISE EXCEPTION 'periodic budget exceeded' USING ERRCODE='23514',CONSTRAINT='pb_budget_'||c.scope; END IF;
  END IF;
 END IF;
 NEW.created_xid:=pg_current_xact_id();NEW.created_at:=pb_now();RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_event_guard BEFORE INSERT ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION pb_guard_event();
CREATE TRIGGER pb_event_immutable BEFORE UPDATE OR DELETE ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION pb_apply_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE window_id text;
BEGIN
 UPDATE periodic_budget_allocations SET held_amount=NEW.after_held,spent_amount=NEW.after_spent,source_held=NEW.after_held,
  source_spent=NEW.after_source_spent,state=NEW.after_state,revision=revision+1,last_event_id=NEW.id
  WHERE id=NEW.allocation_id AND control_id=NEW.control_id RETURNING periodic_budget_allocations.window_id INTO window_id;
 IF window_id IS NOT NULL THEN
  UPDATE periodic_budget_windows SET held_amount=held_amount+NEW.held_delta,spent_amount=spent_amount+NEW.spent_delta,
   revision=revision+1,last_event_id=NEW.id WHERE id=window_id AND control_id=NEW.control_id;
 END IF;
 UPDATE periodic_budget_controls SET held_amount=held_amount+NEW.held_delta,spent_amount=spent_amount+NEW.spent_delta,
  event_seq=NEW.seq,last_event_id=NEW.id WHERE id=NEW.control_id;
 RETURN NULL;
END $$;
CREATE TRIGGER pb_event_apply AFTER INSERT ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION pb_apply_event();

CREATE FUNCTION pb_check_customer(subject text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE r request_records%ROWTYPE;v reservations%ROWTYPE;c periodic_budget_controls%ROWTYPE;
 a periodic_budget_allocations%ROWTYPE;s settlements%ROWTYPE;expected_h numeric;expected_s numeric;expected_state text;marker text;
BEGIN
 SELECT * INTO r FROM request_records WHERE id=subject;IF NOT FOUND THEN RETURN;END IF;
 SELECT * INTO v FROM reservations WHERE request_id=r.id;IF NOT FOUND THEN
  IF r.pb_created_xid IS NOT NULL OR r.pb_tenant_control IS NOT NULL OR r.pb_key_control IS NOT NULL THEN RAISE EXCEPTION 'periodic source reservation missing' USING ERRCODE='23514'; END IF;RETURN;END IF;
 SELECT * INTO s FROM settlements WHERE request_id=r.id;
 IF v.status IN ('reserved','pending_reconciliation') THEN
  IF s.id IS NOT NULL THEN RAISE EXCEPTION 'periodic pending source already settled' USING ERRCODE='23514'; END IF;
  expected_h:=v.amount;expected_s:=0;expected_state:='held';
 ELSE
  IF s.id IS NULL OR s.status IS DISTINCT FROM v.status THEN RAISE EXCEPTION 'periodic terminal source settlement missing' USING ERRCODE='23514'; END IF;
  expected_h:=0;expected_s:=s.charged_amount;expected_state:=s.status;
 END IF;
 FOR c IN SELECT * FROM periodic_budget_controls WHERE (scope='tenant' AND tenant_id=r.tenant_id)
    OR (scope='key' AND tenant_id=r.tenant_id AND key_id=r.key_id) LOOP
  marker:=CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control ELSE r.pb_key_control END;
  SELECT * INTO a FROM periodic_budget_allocations WHERE control_id=c.id AND subject_type='customer_request' AND subject_id=r.id;
  IF NOT FOUND THEN
   IF marker=c.id OR v.status IN ('reserved','pending_reconciliation') THEN
    RAISE EXCEPTION 'periodic customer allocation missing' USING ERRCODE='23514'; END IF;
  ELSE
   IF a.revision<=0 OR a.last_event_id IS NULL OR (a.held_amount,a.spent_amount,a.source_held,a.source_spent,a.state,a.upper_amount)
    IS DISTINCT FROM (expected_h,expected_s,expected_h,expected_s,expected_state,v.amount) THEN
    RAISE EXCEPTION 'periodic customer projection/source mismatch' USING ERRCODE='23514'; END IF;
   IF (marker=c.id AND a.origin<>'new') OR (marker IS NULL AND a.origin='new') THEN
    RAISE EXCEPTION 'periodic customer origin mismatch' USING ERRCODE='23514'; END IF;
  END IF;
 END LOOP;
END $$;
CREATE FUNCTION pb_check_supplier(subject text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE p procurement_reservations%ROWTYPE;c periodic_budget_controls%ROWTYPE;a periodic_budget_allocations%ROWTYPE;expected_state text;
BEGIN
 SELECT * INTO p FROM procurement_reservations WHERE attempt_id=subject;IF NOT FOUND THEN RETURN; END IF;
 SELECT * INTO c FROM periodic_budget_controls WHERE scope='procurement' AND account_id=p.account_id;IF NOT FOUND THEN RETURN;END IF;
 SELECT * INTO a FROM periodic_budget_allocations WHERE control_id=c.id AND subject_type='supplier_attempt' AND subject_id=subject;
 IF NOT FOUND THEN
  IF p.pb_control=c.id OR p.state IN ('prepared','sending','unknown','disputed') OR EXISTS
   (SELECT 1 FROM procurement_budget_events WHERE attempt_id=p.attempt_id AND pb_created_xid=pg_current_xact_id()) THEN
   RAISE EXCEPTION 'periodic supplier allocation missing' USING ERRCODE='23514'; END IF;RETURN;
 END IF;
 expected_state:=CASE p.state WHEN 'settled' THEN 'settled' WHEN 'released' THEN 'released' WHEN 'disputed' THEN 'disputed' ELSE 'held' END;
 IF a.revision<=0 OR a.last_event_id IS NULL OR (a.held_amount,a.spent_amount,a.source_held,a.source_spent,a.state,a.upper_amount)
  IS DISTINCT FROM (p.held_amount,greatest(p.spent_amount-a.baseline,0),p.held_amount,p.spent_amount,expected_state,p.upper_amount::bigint) THEN
  RAISE EXCEPTION 'periodic supplier projection/source mismatch' USING ERRCODE='23514'; END IF;
 IF (p.pb_control=c.id AND a.origin<>'new') OR (p.pb_control IS NULL AND a.origin='new') THEN
  RAISE EXCEPTION 'periodic supplier origin mismatch' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION pb_check_source_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='request_records' THEN PERFORM pb_check_customer(NEW.id);
 ELSIF TG_TABLE_NAME='reservations' OR TG_TABLE_NAME='settlements' THEN PERFORM pb_check_customer(NEW.request_id);
 ELSIF TG_TABLE_NAME IN ('procurement_reservations','procurement_budget_events') THEN PERFORM pb_check_supplier(NEW.attempt_id);
 ELSIF NEW.subject_type='customer_request' THEN PERFORM pb_check_customer(NEW.subject_id);
 ELSE PERFORM pb_check_supplier(NEW.subject_id);END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER pb_request_source_check AFTER INSERT OR UPDATE ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_reservation_source_check AFTER INSERT OR UPDATE ON reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_settlement_source_check AFTER INSERT OR UPDATE ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_procurement_source_check AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_procurement_event_check AFTER INSERT ON procurement_budget_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_allocation_source_check AFTER INSERT OR UPDATE ON periodic_budget_allocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE FUNCTION pb_guard_customer_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r request_records%ROWTYPE;tuple_xid xid;source_stamp xid8;source_tenant text;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT pb_created_xid,xmin,tenant_id INTO source_stamp,tuple_xid,source_tenant FROM request_records WHERE id=NEW.request_id;
  IF source_tenant IS DISTINCT FROM NEW.tenant_id OR NOT pb_current_tuple(source_stamp,tuple_xid) THEN
   RAISE EXCEPTION 'new customer reservation requires same-tx request' USING ERRCODE='23514'; END IF;
  -- The parent INSERT already owns tenant/Key roots and scope shared locks.
  PERFORM pb_require_read_committed();RETURN NEW;
 END IF;
 SELECT * INTO STRICT r FROM request_records WHERE id=OLD.request_id;
 PERFORM pb_sync_unconfigured('tenant',r.tenant_id);
 PERFORM pb_sync_unconfigured('key',r.key_id);
 -- Scope synchronization is derived from this immutable parent/amount.
 -- Existing finish paths only change status, including pre-B sources.
 IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.request_id,NEW.amount,NEW.deadline,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.request_id,OLD.amount,OLD.deadline,OLD.created_at) THEN
  RAISE EXCEPTION 'periodic reservation identity immutable' USING ERRCODE='23514';END IF;
 IF EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=OLD.request_id) THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'periodic reservation delete forbidden' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
END $$;
CREATE TRIGGER pb_customer_reservation_identity BEFORE INSERT OR UPDATE OR DELETE ON reservations FOR EACH ROW EXECUTE FUNCTION pb_guard_customer_reservation();
CREATE FUNCTION pb_check_initial_import() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;subject text;n integer:=0;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.id;
 IF c.scope='procurement' THEN
  FOR subject IN SELECT attempt_id FROM procurement_reservations WHERE account_id=c.account_id AND state IN ('prepared','sending','unknown','disputed') ORDER BY attempt_id LIMIT 1001 LOOP
   n:=n+1;PERFORM pb_check_supplier(subject);
  END LOOP;
 ELSE
  FOR subject IN SELECT v.request_id FROM reservations v JOIN request_records r ON r.id=v.request_id
   WHERE r.tenant_id=c.tenant_id AND (c.scope='tenant' OR r.key_id=c.key_id) AND v.status IN ('reserved','pending_reconciliation') ORDER BY v.request_id LIMIT 1001 LOOP
   n:=n+1;PERFORM pb_check_customer(subject);
  END LOOP;
 END IF;
 IF n>1000 THEN RAISE EXCEPTION 'periodic legacy import requires maintenance' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER pb_initial_import_check AFTER INSERT ON periodic_budget_controls DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_initial_import();

-- Components for an authoritative source admission or the dispatch trigger.
-- Calling these with an arbitrary old at_time is NOT a dispatch permission.
CREATE FUNCTION pb_observe(control text, at_time timestamptz) RETURNS text LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;start_time timestamptz;observed_window text;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control FOR NO KEY UPDATE;
 start_time:=pb_start(c.period_kind,at_time);
 IF start_time<c.max_observed_window_start OR at_time>pb_now() THEN RAISE EXCEPTION 'periodic clock regression' USING ERRCODE='23514';END IF;
 UPDATE periodic_budget_controls SET max_observed_window_start=start_time WHERE id=c.id;
 SELECT id INTO observed_window FROM periodic_budget_windows WHERE control_id=c.id AND start_at=start_time;
 IF observed_window IS NULL THEN observed_window:=gen_random_uuid()::text;
  INSERT INTO periodic_budget_windows(id,control_id,start_at,end_at) VALUES(observed_window,c.id,start_time,pb_end(c.period_kind,start_time));END IF;
 RETURN observed_window;
END $$;
CREATE FUNCTION pb_gate(control text,at_time timestamptz,extra bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;w periodic_budget_windows%ROWTYPE;observed_window text;
BEGIN
 IF extra IS NULL OR extra<0 THEN RAISE EXCEPTION 'periodic invalid extra' USING ERRCODE='23514';END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control FOR NO KEY UPDATE;
 observed_window:=pb_observe(control,at_time);
 SELECT * INTO STRICT w FROM periodic_budget_windows WHERE id=observed_window AND control_id=c.id;
 IF c.enabled AND at_time>=c.effective_from AND w.spent_amount+c.held_amount+extra>c.limit_amount THEN
  RAISE EXCEPTION 'periodic budget exceeded' USING ERRCODE='23514',CONSTRAINT='pb_budget_'||c.scope;END IF;
END $$;

-- Only the actual prepared->sending transition creates a new dispatch fact.
-- Supported callers already own all front-layer locks; this guard reuses the
-- control layer and never locks tenant/key/model/account/business source rows.
-- Missing scopes first join the separate first-configuration shared barrier.
CREATE FUNCTION pb_guard_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r request_records%ROWTYPE;control text;controls text[]:=ARRAY[]::text[];at_time timestamptz;
 current_binding text;frozen_binding text;procurement_state text;procurement_account text;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.delivery_state IS DISTINCT FROM 'prepared' THEN
   RAISE EXCEPTION 'periodic new attempt must be prepared' USING ERRCODE='23514';END IF;
  NEW.pb_dispatch_checked_at:=NULL;RETURN NEW;
 END IF;
 IF NEW.delivery_state='sending' AND OLD.delivery_state IS DISTINCT FROM 'sending' THEN
  PERFORM pb_require_read_committed();
  IF OLD.delivery_state IS DISTINCT FROM 'prepared' THEN
   RAISE EXCEPTION 'periodic dispatch requires prepared attempt' USING ERRCODE='23514';END IF;
  SELECT * INTO STRICT r FROM request_records WHERE id=NEW.request_id AND tenant_id=NEW.tenant_id;
  IF NOT EXISTS(SELECT 1 FROM reservations WHERE request_id=r.id AND tenant_id=r.tenant_id AND status IN ('reserved','pending_reconciliation')) THEN
   RAISE EXCEPTION 'periodic dispatch customer source not held' USING ERRCODE='23514';END IF;
  SELECT id INTO current_binding FROM credential_procurement_bindings WHERE credential_id=NEW.credential_id AND channel_id=NEW.channel_id;
  SELECT binding_id,state,account_id INTO frozen_binding,procurement_state,procurement_account FROM procurement_reservations WHERE attempt_id=NEW.id;
  IF current_binding IS DISTINCT FROM frozen_binding OR (frozen_binding IS NOT NULL AND procurement_state NOT IN ('prepared','sending')) THEN
   RAISE EXCEPTION 'periodic dispatch procurement source mismatch' USING ERRCODE='23514';END IF;
  PERFORM pb_sync_unconfigured('tenant',r.tenant_id);
  PERFORM pb_sync_unconfigured('key',r.key_id);
  IF procurement_account IS NOT NULL THEN PERFORM pb_sync_unconfigured('procurement',procurement_account); END IF;
  FOR control IN SELECT c.id FROM periodic_budget_controls c
   WHERE (c.scope='tenant' AND c.tenant_id=r.tenant_id) OR (c.scope='key' AND c.tenant_id=r.tenant_id AND c.key_id=r.key_id)
   ORDER BY c.id FOR NO KEY UPDATE OF c LOOP controls:=array_append(controls,control);END LOOP;
  FOR control IN SELECT c.id FROM periodic_budget_controls c JOIN procurement_reservations p ON p.account_id=c.account_id
   WHERE c.scope='procurement' AND p.attempt_id=NEW.id FOR NO KEY UPDATE OF c LOOP controls:=array_append(controls,control);END LOOP;
  PERFORM pb_check_customer(r.id);PERFORM pb_check_supplier(NEW.id);
  -- One authoritative decision time after every control wait. Integrity
  -- guards may independently check not-in-future, but cannot choose its period.
  at_time:=pb_now();
  FOREACH control IN ARRAY controls LOOP PERFORM pb_gate(control,at_time,0);END LOOP;
  NEW.pb_dispatch_checked_at:=at_time;
 ELSE
  IF NEW.delivery_state='prepared' AND OLD.delivery_state IS DISTINCT FROM 'prepared' THEN
   RAISE EXCEPTION 'periodic attempt cannot return to prepared' USING ERRCODE='23514';END IF;
  IF NEW.pb_dispatch_checked_at IS DISTINCT FROM OLD.pb_dispatch_checked_at THEN
   RAISE EXCEPTION 'periodic dispatch metadata immutable' USING ERRCODE='23514';END IF;
 END IF;RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic dispatch source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_dispatch_guard BEFORE INSERT OR UPDATE ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION pb_guard_dispatch();

-- 023-B0 configuration/receipt prototype. Append after core.sql. NOT frozen.
CREATE FUNCTION pb_decimal(value text,allow_zero boolean) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF value IS NULL OR value !~ '^(0|[1-9][0-9]{0,18})$' THEN RETURN false;END IF;
 RETURN value::numeric<=9223372036854775807 AND (allow_zero OR value<>'0');
END $$;
CREATE FUNCTION pb_frame(parts text[]) RETURNS bytea LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result bytea:=''::bytea;part text;
BEGIN FOREACH part IN ARRAY parts LOOP
 IF part IS NULL THEN RAISE EXCEPTION 'periodic fingerprint null field' USING ERRCODE='23514'; END IF;
 result:=result||int4send(octet_length(convert_to(part,'UTF8')))||convert_to(part,'UTF8');
 END LOOP;RETURN result;END $$;
CREATE FUNCTION pb_safe_input(j jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT (jsonb_typeof(j)='object' AND j ?& ARRAY['expected_revision','period_kind','enabled','limit_amount']
 AND NOT EXISTS(SELECT 1 FROM jsonb_object_keys(j) k WHERE k<>ALL(ARRAY['expected_revision','period_kind','enabled','limit_amount']))
 AND jsonb_typeof(j->'expected_revision')='string' AND pb_decimal(j->>'expected_revision',true)
 AND jsonb_typeof(j->'limit_amount')='string' AND pb_decimal(j->>'limit_amount',true)
 AND jsonb_typeof(j->'period_kind')='string' AND j->>'period_kind' IN ('utc_day','utc_month')
 AND jsonb_typeof(j->'enabled')='boolean') IS TRUE
$$;
CREATE FUNCTION pb_view_at(control text,at_time timestamptz) RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
DECLARE c periodic_budget_controls%ROWTYPE;w periodic_budget_windows%ROWTYPE;window_view jsonb;current_state text;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control;
 IF pb_start(c.period_kind,at_time)<c.max_observed_window_start THEN RAISE EXCEPTION 'periodic clock regression' USING ERRCODE='23514';END IF;
 SELECT * INTO w FROM periodic_budget_windows WHERE control_id=c.id AND start_at=pb_start(c.period_kind,at_time);
 window_view:='null'::jsonb;
 IF w.id IS NOT NULL THEN window_view:=jsonb_build_object('id',w.id,'start_at',pricing_time(w.start_at),'end_at',pricing_time(w.end_at),
  'spent_amount',w.spent_amount::text,'held_amount',w.held_amount::text);END IF;
 IF c.held_amount<coalesce(w.held_amount,0) THEN RAISE EXCEPTION 'periodic negative carry' USING ERRCODE='23514'; END IF;
 current_state:=CASE WHEN NOT c.enabled THEN 'disabled' WHEN at_time<c.effective_from THEN 'scheduled' ELSE 'active' END;
 RETURN jsonb_build_object('scope',c.scope,'scope_id',coalesce(c.account_id,c.key_id,c.tenant_id),'revision',c.revision::text,
  'period_kind',c.period_kind,'enabled',c.enabled,'limit_amount',c.limit_amount::text,'state',current_state,
  'effective_from',pricing_time(c.effective_from),'tracking_started_at',pricing_time(c.tracking_started_at),
  'current_window',window_view,'carry_held',(c.held_amount-coalesce(w.held_amount,0))::text,'as_of',pricing_time(at_time));
END $$;
CREATE TABLE periodic_budget_operations (
 operation_id text PRIMARY KEY CHECK(pb_uuid(operation_id)),
 kind text NOT NULL CHECK(kind IN ('tenant_periodic_budget.configure','key_periodic_budget.configure','procurement_periodic_budget.configure')),
 actor text NOT NULL REFERENCES users(id),scope text NOT NULL CHECK(scope='platform' OR pb_uuid(scope)),
 target text NOT NULL CHECK(octet_length(target)<=200),resource_id text NOT NULL CHECK(pb_uuid(resource_id)),
 revision bigint NOT NULL CHECK(revision>0),safe_input jsonb NOT NULL CHECK(pb_safe_input(safe_input)),
 input_fingerprint text NOT NULL CHECK(input_fingerprint ~ '^[0-9a-f]{64}$'),
 result_snapshot jsonb NOT NULL CHECK(jsonb_typeof(result_snapshot)='object' AND octet_length(result_snapshot::text)<=8192),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz NOT NULL DEFAULT pb_now()
);
CREATE TRIGGER pb_operation_immutable BEFORE UPDATE OR DELETE ON periodic_budget_operations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
ALTER TABLE periodic_budget_controls ADD COLUMN configuration_operation_id text NOT NULL DEFAULT gen_random_uuid()::text
 REFERENCES periodic_budget_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE periodic_budget_configuration_versions (
 control_id text NOT NULL REFERENCES periodic_budget_controls(id),revision bigint NOT NULL CHECK(revision>0),
 operation_id text NOT NULL UNIQUE REFERENCES periodic_budget_operations(operation_id) DEFERRABLE INITIALLY DEFERRED,
 period_kind text NOT NULL CHECK(period_kind IN ('utc_day','utc_month')),enabled boolean NOT NULL,limit_amount bigint NOT NULL CHECK(limit_amount>=0),
 actor text NOT NULL REFERENCES users(id),created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz NOT NULL DEFAULT pb_now(),
 PRIMARY KEY(control_id,revision)
);
CREATE TRIGGER pb_version_immutable BEFORE UPDATE OR DELETE ON periodic_budget_configuration_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE UNIQUE INDEX pb_command_audit_unique ON audit_events(target) WHERE action='periodic_budget.configured';

CREATE FUNCTION pb_guard_operation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;actor_role text;member_role text;owner text;expected bigint;canonical_target text;canonical_scope text;canonical_kind text;
BEGIN
 IF NOT pb_safe_input(NEW.safe_input) THEN RAISE EXCEPTION 'periodic unsafe command input' USING ERRCODE='23514'; END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.resource_id;
 IF c.scope='key' THEN
  SELECT m.role,k.owner_id INTO member_role,owner FROM memberships m JOIN users u ON u.id=m.user_id JOIN api_keys k ON k.tenant_id=m.tenant_id
   WHERE m.tenant_id=c.tenant_id AND m.user_id=NEW.actor AND k.id=c.key_id AND m.status='active' AND u.status='active' AND k.revoked_at IS NULL;
  IF member_role IS NULL OR member_role NOT IN ('owner','member') OR (member_role='member' AND owner<>NEW.actor) THEN
   RAISE EXCEPTION 'periodic command actor forbidden' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM tenants WHERE id=c.tenant_id AND status='active') THEN RAISE EXCEPTION 'periodic tenant inactive' USING ERRCODE='23514';END IF;
  canonical_kind:='key_periodic_budget.configure';canonical_scope:=c.tenant_id;canonical_target:='/api/keys/'||c.key_id||'/periodic-budget';
 ELSE
  SELECT platform_role INTO actor_role FROM users WHERE id=NEW.actor AND status='active' FOR SHARE;
  IF actor_role IS DISTINCT FROM 'admin' THEN RAISE EXCEPTION 'periodic command actor forbidden' USING ERRCODE='23514';END IF;
  canonical_scope:='platform';
  IF c.scope='tenant' THEN canonical_kind:='tenant_periodic_budget.configure';canonical_target:='/api/platform/tenants/'||c.tenant_id||'/periodic-budget';
  ELSE canonical_kind:='procurement_periodic_budget.configure';canonical_target:='/api/platform/procurement-accounts/'||c.account_id||'/periodic-budget';END IF;
 END IF;
 IF (NEW.kind,NEW.scope,NEW.target) IS DISTINCT FROM (canonical_kind,canonical_scope,canonical_target) THEN
  RAISE EXCEPTION 'periodic command target/scope mismatch' USING ERRCODE='23514';END IF;
 expected:=(NEW.safe_input->>'expected_revision')::bigint;
 IF (c.period_kind,c.enabled,c.limit_amount) IS DISTINCT FROM
    (NEW.safe_input->>'period_kind',(NEW.safe_input->>'enabled')::boolean,(NEW.safe_input->>'limit_amount')::bigint) THEN
  RAISE EXCEPTION 'periodic command input/current configuration mismatch' USING ERRCODE='23514';END IF;
 IF c.configuration_operation_id=NEW.operation_id THEN
  IF expected<>c.revision-1 THEN RAISE EXCEPTION 'periodic command CAS mismatch' USING ERRCODE='23514';END IF;
 ELSE
  IF expected<>c.revision THEN RAISE EXCEPTION 'periodic noop CAS mismatch' USING ERRCODE='23514';END IF;
 END IF;
 NEW.revision:=c.revision;NEW.created_xid:=pg_current_xact_id();NEW.created_at:=pb_now();
 NEW.input_fingerprint:=encode(sha256(pb_frame(ARRAY[NEW.kind,NEW.actor,NEW.scope,NEW.target,
  NEW.safe_input->>'expected_revision',NEW.safe_input->>'period_kind',NEW.safe_input->>'enabled',NEW.safe_input->>'limit_amount'])),'hex');
 NEW.result_snapshot:=pb_view_at(c.id,NEW.created_at);RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic operation resource missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_operation_guard BEFORE INSERT ON periodic_budget_operations FOR EACH ROW EXECUTE FUNCTION pb_guard_operation();
CREATE FUNCTION pb_guard_configuration_pointer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.enabled,NEW.limit_amount) IS DISTINCT FROM (OLD.enabled,OLD.limit_amount) THEN
  IF NEW.configuration_operation_id IS NOT DISTINCT FROM OLD.configuration_operation_id THEN
   RAISE EXCEPTION 'periodic configuration requires new operation' USING ERRCODE='23514';END IF;
 ELSIF NEW.configuration_operation_id IS DISTINCT FROM OLD.configuration_operation_id THEN
  RAISE EXCEPTION 'periodic noop cannot replace configuration history' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER pb_configuration_pointer BEFORE UPDATE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION pb_guard_configuration_pointer();
CREATE FUNCTION pb_guard_version() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;o periodic_budget_operations%ROWTYPE;tuple_xid xid;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id;
 SELECT * INTO STRICT o FROM periodic_budget_operations WHERE operation_id=NEW.operation_id;
 SELECT xmin INTO tuple_xid FROM periodic_budget_operations WHERE operation_id=o.operation_id;
 IF NOT pb_current_tuple(o.created_xid,tuple_xid) OR c.configuration_operation_id IS DISTINCT FROM o.operation_id
  OR (NEW.revision,NEW.period_kind,NEW.enabled,NEW.limit_amount,NEW.actor)
   IS DISTINCT FROM (c.revision,c.period_kind,c.enabled,c.limit_amount,o.actor) THEN
  RAISE EXCEPTION 'periodic version requires exact current-tx operation' USING ERRCODE='23514';END IF;
 NEW.created_xid:=pg_current_xact_id();NEW.created_at:=o.created_at;RETURN NEW;
END $$;
CREATE TRIGGER pb_version_guard BEFORE INSERT ON periodic_budget_configuration_versions FOR EACH ROW EXECUTE FUNCTION pb_guard_version();
CREATE FUNCTION pb_record_operation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;changed boolean;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.resource_id;
 changed:=c.configuration_operation_id=NEW.operation_id;
 IF changed THEN
  INSERT INTO periodic_budget_configuration_versions(control_id,revision,operation_id,period_kind,enabled,limit_amount,actor)
  VALUES(c.id,c.revision,NEW.operation_id,c.period_kind,c.enabled,c.limit_amount,NEW.actor);
 END IF;
 INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff,created_at)
 VALUES(NEW.actor,CASE WHEN c.scope='key' THEN c.tenant_id ELSE NULL END,'periodic_budget.configured',NEW.operation_id,
  jsonb_build_object('control_id',c.id,'revision',c.revision::text,'scope',c.scope,'scope_id',coalesce(c.account_id,c.key_id,c.tenant_id),'changed',changed),NEW.created_at);
 RETURN NULL;
END $$;
CREATE TRIGGER pb_operation_record AFTER INSERT ON periodic_budget_operations FOR EACH ROW EXECUTE FUNCTION pb_record_operation();
CREATE FUNCTION pb_check_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;v periodic_budget_configuration_versions%ROWTYPE;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.id;
 SELECT * INTO v FROM periodic_budget_configuration_versions WHERE control_id=c.id AND revision=c.revision;
 IF (v.operation_id,v.period_kind,v.enabled,v.limit_amount) IS DISTINCT FROM (c.configuration_operation_id,c.period_kind,c.enabled,c.limit_amount) THEN
  RAISE EXCEPTION 'periodic configuration version/receipt missing' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER pb_configuration_receipt AFTER INSERT OR UPDATE ON periodic_budget_controls DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_configuration();

-- Isolated prototype renderer substitutes role identifiers, never SQL values.
-- Every B function resolves this exact schema before pg_catalog and pg_temp.
DO $$
DECLARE f record;
BEGIN
 FOR f IN SELECT p.oid,p.proname,pg_get_function_identity_arguments(p.oid) args
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND p.proname LIKE 'pb\_%' ESCAPE '\' LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
 END LOOP;
END $$;

-- END 020_periodic_budgets.sql
-- BEGIN 021_payment_sandbox.sql
-- 022-B2 application payment sandbox. Default off; no marker or external IO.
-- Root reviewed frozen B1/B2 contracts; append in the migration transaction after
-- real 001--020. No marker, LOGIN, nonce, key, application wiring or payment IO.
-- B1 source SHA256 fb367858ee12f00f188d819b3e5bb3ff6432e2dc8e301da41f233738cdeaca98
-- 017 source SHA256 5dc682fb01313da617baa3f9444b7e98acc4a02bc3d5e76b2a45390bf7fdff75

-- Static baseline, not a copy trusted from the running catalog. Preserve both
-- existing procedure identities and every incoming dependency target.
CREATE TEMP TABLE payment_upgrade_guard ON COMMIT DROP AS
 SELECT p.oid,p.proname,p.proowner,p.proargtypes,p.prorettype,p.prokind,
  (SELECT coalesce(jsonb_agg(jsonb_build_array(d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype)
    ORDER BY d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype),'[]'::jsonb)
   FROM pg_depend d WHERE d.refclassid='pg_proc'::regclass AND d.refobjid=p.oid) incoming
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=current_schema() AND ((p.proname='funding_journal_valid' AND p.proargtypes='25'::oidvector) OR (p.proname='funding_fact_guard' AND p.pronargs=0));
DO $upgrade$
DECLARE fn record;
BEGIN
 IF (SELECT count(*) FROM pg_temp.payment_upgrade_guard)<>2 THEN RAISE EXCEPTION 'payment legacy function baseline mismatch';END IF;
 FOR fn IN SELECT p.*,l.lanname FROM pg_proc p JOIN pg_temp.payment_upgrade_guard g ON g.oid=p.oid JOIN pg_language l ON l.oid=p.prolang LOOP
  IF fn.prosecdef OR fn.prokind<>'f' OR fn.proisstrict OR fn.proleakproof OR fn.proparallel<>'u' OR fn.proconfig IS NOT NULL
   OR (fn.proname='funding_journal_valid' AND (fn.prosrc IS DISTINCT FROM $legacy$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 LEFT JOIN credit_orders c ON f.source_kind='manual_credit' AND c.id=f.source_id
 LEFT JOIN vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 LEFT JOIN voucher_batches b ON b.id=v.batch_id
 LEFT JOIN voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 LEFT JOIN commerce_commands cmd ON cmd.operation_id=r.operation_id
 LEFT JOIN commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id AND j.currency='CNY' AND f.currency='CNY' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.state='approved' AND c.journal_id=j.id AND c.tenant_id=f.tenant_id AND c.amount=f.amount AND c.approver<>c.requester AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND j.actor=c.approver)
 OR (f.source_kind='voucher_grant' AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
  AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
  AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
  AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
  AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$legacy$ OR fn.lanname<>'sql' OR fn.provolatile<>'s' OR fn.prorettype<>'boolean'::regtype OR fn.proargnames IS DISTINCT FROM ARRAY['journal_ref']::text[]))
   OR (fn.proname='funding_fact_guard' AND (fn.prosrc IS DISTINCT FROM $guard$
DECLARE jid TEXT; credit_state TEXT;
BEGIN
 IF TG_TABLE_NAME='credit_orders' THEN
  SELECT state,journal_id INTO credit_state,jid FROM credit_orders WHERE id=NEW.id;
  IF credit_state='pending' THEN
   IF EXISTS(SELECT 1 FROM journals WHERE kind='credit' AND operation_id=NEW.id) OR EXISTS(SELECT 1 FROM projection_outbox WHERE kind='credited' AND operation_id=NEW.id) THEN RAISE EXCEPTION 'pending credit has funding'; END IF;
   RETURN NULL;
  END IF;
 ELSIF TG_TABLE_NAME='journals' THEN
  IF NEW.kind NOT IN ('credit','voucher_grant') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $guard$ OR fn.lanname<>'plpgsql' OR fn.provolatile<>'v' OR fn.prorettype<>'trigger'::regtype))
  THEN RAISE EXCEPTION 'payment legacy function baseline mismatch';END IF;
 END LOOP;
END $upgrade$;

-- Refuse every direction of invalid retained A funding before adding B schema.
DO $upgrade$
BEGIN
 IF EXISTS(SELECT 1 FROM credit_orders c WHERE
  (c.state='pending' AND (c.approver IS NOT NULL OR c.journal_id IS NOT NULL
   OR EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id)
   OR EXISTS(SELECT 1 FROM journals j WHERE j.kind='credit' AND j.operation_id=c.id)
   OR EXISTS(SELECT 1 FROM projection_outbox o WHERE o.kind='credited' AND o.operation_id=c.id)))
  OR (c.state='approved' AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=c.id AND f.tenant_id=c.tenant_id AND f.amount=c.amount AND f.journal_id=c.journal_id AND funding_journal_valid(f.journal_id))))
 OR EXISTS(SELECT 1 FROM voucher_redemptions r WHERE NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.id=r.funding_receipt_id AND f.source_kind='voucher_grant' AND f.source_id=r.voucher_id AND funding_journal_valid(r.journal_id)))
 OR EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind NOT IN ('manual_credit','voucher_grant') OR NOT funding_journal_valid(f.journal_id))
 OR EXISTS(SELECT 1 FROM journals j WHERE j.kind IN ('credit','voucher_grant') AND NOT funding_journal_valid(j.id))
 OR EXISTS(SELECT 1 FROM projection_outbox o WHERE o.kind='credited' AND NOT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id WHERE j.operation_id=o.operation_id AND funding_journal_valid(j.id)))
 OR EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING') AND NOT funding_journal_valid(l.journal_id))
 OR EXISTS(SELECT 1 FROM voucher_batches b WHERE b.count<>(SELECT count(*) FROM vouchers v WHERE v.batch_id=b.id) OR b.redeemed_count<>(SELECT count(*) FROM voucher_redemptions r WHERE r.batch_id=b.id))
 OR EXISTS(SELECT 1 FROM commerce_operation_receipts t WHERE t.kind='voucher.redeem' AND t.status='accepted' AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.operation_id=t.operation_id AND r.id=t.resource_id AND r.actor=t.actor AND 'tenant:'||r.tenant_id=t.scope AND r.amount=t.amount AND r.funding_receipt_id=t.funding_receipt_id AND r.journal_id=t.journal_id))
 OR EXISTS(SELECT 1 FROM commerce_operation_receipts WHERE status='accepted' AND kind IN ('voucher.batch.create','voucher.batch.approve','voucher.batch.void','voucher.revocation.create','voucher.revocation.approve') GROUP BY kind,resource_id HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.balance::numeric<>coalesce((SELECT sum(l.signed_delta::numeric) FROM journal_lines l WHERE l.account_id=a.id),0))
 OR EXISTS(SELECT 1 FROM journals j WHERE (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)<2 OR (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.journal_id=j.id)<>0)
 THEN RAISE EXCEPTION 'payment legacy funding verification failed';END IF;
END $upgrade$;

CREATE FUNCTION payment_require_rc() RETURNS void LANGUAGE plpgsql VOLATILE AS $$ BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'payment writes require read committed' USING ERRCODE='23514';END IF;
END $$;
CREATE FUNCTION payment_ref(v text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(octet_length(v) BETWEEN 1 AND 100 AND v!~'[[:cntrl:]]',false)
$$;
CREATE FUNCTION payment_time(v timestamptz) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT to_char(v AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
$$;
CREATE FUNCTION payment_frame(parts text[]) RETURNS bytea LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result bytea:=''::bytea;part text;
BEGIN FOREACH part IN ARRAY parts LOOP
 IF part IS NULL THEN result:=result||int4send(-1);ELSE result:=result||int4send(octet_length(convert_to(part,'UTF8')))||convert_to(part,'UTF8');END IF;
 END LOOP;RETURN result;
END $$;

CREATE TABLE payment_sandbox_environment(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),id text NOT NULL UNIQUE CHECK(funding_uuid(id)),
 merchant_id text NOT NULL CHECK(payment_ref(merchant_id)),payee_label text NOT NULL CHECK(payment_ref(payee_label)),
 database_oid oid NOT NULL,schema_oid oid NOT NULL,nonce_digest text NOT NULL CHECK(nonce_digest~'^[0-9a-f]{64}$'),
 app_role name NOT NULL,worker_role name NOT NULL,projector_role name NOT NULL,
 protocol_version integer NOT NULL DEFAULT 1 CHECK(protocol_version=1),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(id,merchant_id),CHECK(app_role<>worker_role AND app_role<>projector_role AND worker_role<>projector_role)
);
CREATE TRIGGER payment_environment_immutable BEFORE UPDATE OR DELETE ON payment_sandbox_environment FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE payment_policy(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),enabled boolean NOT NULL DEFAULT false,revision bigint NOT NULL DEFAULT 1 CHECK(revision>0));
INSERT INTO payment_policy DEFAULT VALUES;
CREATE FUNCTION payment_policy_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM payment_require_rc();IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment policy cannot be deleted';END IF;
 IF NEW.singleton IS DISTINCT FROM OLD.singleton THEN RAISE EXCEPTION 'payment policy identity immutable';END IF;
 NEW.revision:=OLD.revision+CASE WHEN NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END;RETURN NEW;
END $$;
CREATE TRIGGER payment_policy_guard BEFORE UPDATE OR DELETE ON payment_policy FOR EACH ROW EXECUTE FUNCTION payment_policy_guard();

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
 display_name text NOT NULL CHECK(payment_ref(display_name)),amount bigint NOT NULL CHECK(amount>0),wallet_credit_amount bigint NOT NULL,
 currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),environment_id text NOT NULL,merchant_id text NOT NULL,payee_label text NOT NULL CHECK(payment_ref(payee_label)),
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
 creator text NOT NULL REFERENCES users(id),sku_version_id text NOT NULL REFERENCES payment_sku_versions(id),display_name text NOT NULL CHECK(payment_ref(display_name)),
 amount bigint NOT NULL CHECK(amount>0),wallet_credit_amount bigint NOT NULL,currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 environment_id text NOT NULL,merchant_id text NOT NULL,payee_label text NOT NULL CHECK(payment_ref(payee_label)),merchant_order_id text NOT NULL UNIQUE,
 created_at timestamptz(6) NOT NULL,expires_at timestamptz(6) NOT NULL,
 UNIQUE(tenant_id,id),UNIQUE(environment_id,merchant_id,id),
 CHECK(wallet_credit_amount=amount AND expires_at=created_at+interval '900 seconds' AND isfinite(created_at) AND isfinite(expires_at)),
 CHECK(merchant_order_id='mps1.'||environment_id||'.'||id),FOREIGN KEY(environment_id,merchant_id) REFERENCES payment_sandbox_environment(id,merchant_id)
);
CREATE TABLE payment_cancel_intents(
 order_id text PRIMARY KEY REFERENCES payment_orders(id),operation_id text NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),
 actor text NOT NULL REFERENCES users(id),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION payment_order_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v payment_sku_versions%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT v FROM payment_sku_versions WHERE id=NEW.sku_version_id;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.id AND kind='payment.order.create' AND actor=NEW.creator AND scope='tenant:'||NEW.tenant_id) THEN RAISE EXCEPTION 'payment order command mismatch';END IF;
 NEW.display_name:=v.display_name;NEW.amount:=v.amount;NEW.wallet_credit_amount:=v.wallet_credit_amount;NEW.currency:=v.currency;
 NEW.environment_id:=v.environment_id;NEW.merchant_id:=v.merchant_id;NEW.payee_label:=v.payee_label;
 NEW.merchant_order_id:='mps1.'||v.environment_id||'.'||NEW.id;NEW.created_at:=clock_timestamp();NEW.expires_at:=NEW.created_at+interval '900 seconds';RETURN NEW;
END $$;
CREATE TRIGGER payment_order_insert BEFORE INSERT ON payment_orders FOR EACH ROW EXECUTE FUNCTION payment_order_insert();
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
 id text PRIMARY KEY CHECK(funding_uuid(id)),environment_id text NOT NULL,merchant_id text NOT NULL,event_id text NOT NULL CHECK(payment_ref(event_id)),
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
CREATE FUNCTION payment_original_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j jsonb;q jsonb;
BEGIN
 PERFORM payment_require_rc();j:=convert_from(NEW.canonical_body,'UTF8')::jsonb;NEW.received_at:=clock_timestamp();
 IF TG_TABLE_NAME='payment_inbox' THEN
  NEW.environment_id:=j->>'environment_id';NEW.merchant_id:=j->>'merchant_id';NEW.event_id:=j->>'event_id';
 ELSE
  SELECT convert_from(canonical_request,'UTF8')::jsonb INTO STRICT q FROM payment_query_attempts WHERE query_id=NEW.query_id;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'query_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM (q->>'schema_version',q->>'environment_id',q->>'merchant_id',q->>'query_id',q->>'merchant_order_id',q->>'command_id',q->>'command_kind',q->>'parameter_digest') OR j->>'command_state' NOT IN ('not_seen','in_progress','completed') THEN RAISE EXCEPTION 'payment query original binding mismatch';END IF;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_notification_stamp BEFORE INSERT ON payment_inbox FOR EACH ROW EXECUTE FUNCTION payment_original_insert();
CREATE TRIGGER payment_query_stamp BEFORE INSERT ON payment_verified_queries FOR EACH ROW EXECUTE FUNCTION payment_original_insert();
CREATE TABLE payment_verified_evidence(
 id text PRIMARY KEY CHECK(funding_uuid(id)),inbox_id text UNIQUE REFERENCES payment_inbox(id),verified_query_id text UNIQUE REFERENCES payment_verified_queries(id),
 environment_id text NOT NULL,merchant_id text NOT NULL,order_id text NOT NULL CHECK(funding_uuid(order_id)),
 kind text NOT NULL CHECK(kind IN ('capture','closed','observation','unsupported')),
 provider_transaction_id text CHECK(provider_transaction_id IS NULL OR payment_ref(provider_transaction_id)),amount bigint CHECK(amount>0),currency text CHECK(currency='CNY'),
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
CREATE FUNCTION payment_evidence_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j jsonb;q jsonb;fact jsonb;r jsonb;received timestamptz;env text;merchant text;oid text;evtype text;failed text;
BEGIN
 PERFORM payment_require_rc();NEW.provider_transaction_id:=NULL;NEW.amount:=NULL;NEW.currency:=NULL;NEW.happened_at:=NULL;NEW.provider_order_revision:=NULL;
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
  NEW.fact_digest:=encode(sha256(payment_frame(ARRAY[env,merchant,oid,'capture',NEW.provider_transaction_id,NEW.amount::text,NEW.currency,payment_time(NEW.happened_at),NEW.provider_order_revision::text])),'hex');
 ELSIF NEW.kind='closed' THEN NEW.fact_digest:=encode(sha256(payment_frame(ARRAY[env,merchant,oid,'closed',payment_time(NEW.happened_at),NEW.provider_order_revision::text])),'hex');
 ELSE NEW.fact_digest:=encode(sha256(payment_frame(ARRAY[env,merchant,oid,NEW.kind,coalesce(j->>'event_id',j->>'query_id')])),'hex');END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_evidence_insert BEFORE INSERT ON payment_verified_evidence FOR EACH ROW EXECUTE FUNCTION payment_evidence_insert();

CREATE TABLE payment_provider_revisions(
 environment_id text NOT NULL,merchant_id text NOT NULL,order_id text NOT NULL REFERENCES payment_orders(id),provider_order_revision bigint NOT NULL CHECK(provider_order_revision>0),
 kind text NOT NULL CHECK(kind IN ('capture','closed')),fact_digest text NOT NULL CHECK(fact_digest~'^[0-9a-f]{64}$'),evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 PRIMARY KEY(environment_id,merchant_id,order_id,provider_order_revision),FOREIGN KEY(environment_id,merchant_id,order_id) REFERENCES payment_orders(environment_id,merchant_id,id)
);
CREATE FUNCTION payment_revision_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e payment_verified_evidence%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=NEW.evidence_id;
 IF e.kind NOT IN ('capture','closed') THEN RAISE EXCEPTION 'payment provider revision needs authoritative fact';END IF;
 PERFORM id FROM payment_orders WHERE id=e.order_id FOR NO KEY UPDATE;
 IF e.kind='closed' AND EXISTS(SELECT 1 FROM payment_transaction_identities i WHERE i.order_id=e.order_id
   AND (i.captured_at>=e.happened_at OR i.provider_order_revision>=e.provider_order_revision)) THEN
  RAISE EXCEPTION 'payment closed conflicts with prior immutable capture identity';END IF;
 NEW.environment_id:=e.environment_id;NEW.merchant_id:=e.merchant_id;NEW.order_id:=e.order_id;NEW.provider_order_revision:=e.provider_order_revision;
 NEW.kind:=e.kind;NEW.fact_digest:=e.fact_digest;RETURN NEW;
END $$;
CREATE TRIGGER payment_revision_insert BEFORE INSERT ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION payment_revision_insert();
CREATE TABLE payment_transaction_identities(
 id text PRIMARY KEY CHECK(funding_uuid(id)),environment_id text NOT NULL,merchant_id text NOT NULL,provider_transaction_id text NOT NULL CHECK(payment_ref(provider_transaction_id)),
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
 tenant_id text NOT NULL,environment_id text NOT NULL,merchant_id text NOT NULL,provider_transaction_id text NOT NULL CHECK(payment_ref(provider_transaction_id)),
 amount bigint NOT NULL CHECK(amount>0),wallet_credit_amount bigint NOT NULL,currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 captured_at timestamptz(6) NOT NULL,provider_order_revision bigint NOT NULL CHECK(provider_order_revision>0),verified_evidence_id text NOT NULL REFERENCES payment_verified_evidence(id),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp(),processing_generation bigint NOT NULL CHECK(processing_generation>0),CHECK(wallet_credit_amount=amount),UNIQUE(tenant_id,id),
 FOREIGN KEY(order_id,identity_id) REFERENCES payment_transaction_identities(order_id,id),FOREIGN KEY(tenant_id,order_id) REFERENCES payment_orders(tenant_id,id)
);
CREATE TABLE payment_effects(
 capture_id text PRIMARY KEY REFERENCES payment_captures(id),identity_id text NOT NULL UNIQUE REFERENCES payment_transaction_identities(id),
 environment_id text NOT NULL,merchant_id text NOT NULL,provider_transaction_id text NOT NULL CHECK(payment_ref(provider_transaction_id)),
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
CREATE FUNCTION payment_review_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e payment_verified_evidence%ROWTYPE;o payment_orders%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=NEW.evidence_id;
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
CREATE TRIGGER payment_review_insert BEFORE INSERT ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION payment_review_insert();

-- Read-only proof of normalized capture versus its stored verified original.
-- Query request, supervisor, original command and frozen order are all bound.
-- Authentication remains exclusively in P; no HMAC/key material in SQL.
CREATE FUNCTION payment_capture_evidence_valid(evidence_ref text) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
DECLARE e payment_verified_evidence%ROWTYPE;o payment_orders%ROWTYPE;m payment_sandbox_environment%ROWTYPE;
 original jsonb;request jsonb;fact jsonb;received timestamptz;event_ref text;
 a payment_query_attempts%ROWTYPE;s payment_command_outbox%ROWTYPE;c payment_command_outbox%ROWTYPE;
BEGIN
 SELECT * INTO e FROM payment_verified_evidence WHERE id=evidence_ref;
 IF NOT FOUND OR e.kind<>'capture' THEN RETURN false;END IF;
 SELECT * INTO o FROM payment_orders WHERE id=e.order_id;IF NOT FOUND THEN RETURN false;END IF;
 SELECT * INTO m FROM payment_sandbox_environment WHERE singleton;IF NOT FOUND THEN RETURN false;END IF;
 IF m.database_oid<>(SELECT oid FROM pg_database WHERE datname=current_database()) OR m.schema_oid<>current_schema()::regnamespace::oid
  OR (e.environment_id,e.merchant_id) IS DISTINCT FROM (m.id,m.merchant_id)
  OR (e.environment_id,e.merchant_id,e.order_id,e.amount,e.currency) IS DISTINCT FROM (o.environment_id,o.merchant_id,o.id,o.amount,o.currency)
 THEN RETURN false;END IF;
 IF e.inbox_id IS NOT NULL AND e.verified_query_id IS NULL THEN
  SELECT convert_from(canonical_body,'UTF8')::jsonb,received_at,event_id INTO original,received,event_ref
   FROM payment_inbox WHERE id=e.inbox_id AND (environment_id,merchant_id)=(e.environment_id,e.merchant_id);
  IF NOT FOUND OR original->'schema_version' IS DISTINCT FROM '1'::jsonb OR original->>'event_type' IS DISTINCT FROM 'capture.confirmed'
   OR (original->>'environment_id',original->>'merchant_id',original->>'order_id',original->>'event_id') IS DISTINCT FROM (e.environment_id,e.merchant_id,e.order_id,event_ref)
  THEN RETURN false;END IF;
  fact:=original;
 ELSIF e.inbox_id IS NULL AND e.verified_query_id IS NOT NULL THEN
  SELECT convert_from(v.canonical_body,'UTF8')::jsonb,v.received_at,v.query_id INTO original,received,event_ref FROM payment_verified_queries v WHERE v.id=e.verified_query_id;
  IF NOT FOUND THEN RETURN false;END IF;
  SELECT * INTO a FROM payment_query_attempts WHERE query_id=event_ref;IF NOT FOUND THEN RETURN false;END IF;
  SELECT * INTO s FROM payment_command_outbox WHERE id=a.supervisor_id;IF NOT FOUND OR s.kind<>'query' THEN RETURN false;END IF;
  SELECT * INTO c FROM payment_command_outbox WHERE id=s.original_outbox_id;IF NOT FOUND OR c.kind NOT IN ('checkout','cancel') OR c.order_id<>e.order_id OR s.order_id<>e.order_id THEN RETURN false;END IF;
  request:=convert_from(a.canonical_request,'UTF8')::jsonb;
  IF request->'schema_version' IS DISTINCT FROM '1'::jsonb OR original->'schema_version' IS DISTINCT FROM '1'::jsonb
   OR (request->>'query_id',request->>'environment_id',request->>'merchant_id',request->>'merchant_order_id',request->>'command_id',request->>'command_kind',request->>'parameter_digest')
    IS DISTINCT FROM (a.query_id,o.environment_id,o.merchant_id,o.merchant_order_id,c.external_command_id,c.kind,c.parameter_digest)
   OR (original->>'query_id',original->>'environment_id',original->>'merchant_id',original->>'merchant_order_id',original->>'command_id',original->>'command_kind',original->>'parameter_digest')
    IS DISTINCT FROM (request->>'query_id',request->>'environment_id',request->>'merchant_id',request->>'merchant_order_id',request->>'command_id',request->>'command_kind',request->>'parameter_digest')
   OR a.created_generation<1 OR a.created_generation>s.generation OR a.mutation_generation<1 OR a.mutation_generation>c.generation
   OR a.request_digest<>encode(sha256(a.canonical_request),'hex') OR c.parameter_digest<>encode(sha256(c.canonical_parameters),'hex')
   OR original->>'command_state' IS DISTINCT FROM 'completed' OR original->'result'->>'status' IS DISTINCT FROM 'captured'
  THEN RETURN false;END IF;
  fact:=original->'result'->'capture';
  IF fact->>'provider_order_revision' IS DISTINCT FROM original->>'provider_order_revision' THEN RETURN false;END IF;
 ELSE RETURN false;END IF;
 RETURN coalesce((fact->>'provider_transaction_id',fact->>'amount',fact->>'currency',fact->>'occurred_at',fact->>'provider_order_revision')
   IS NOT DISTINCT FROM (e.provider_transaction_id,e.amount::text,e.currency,payment_time(e.happened_at),e.provider_order_revision::text)
  AND e.verified_at=received AND e.happened_at<=received+interval '5 seconds'
  AND e.fact_digest=encode(sha256(payment_frame(ARRAY[e.environment_id,e.merchant_id,e.order_id,'capture',e.provider_transaction_id,e.amount::text,e.currency,payment_time(e.happened_at),e.provider_order_revision::text])),'hex'),false);
EXCEPTION WHEN data_exception THEN RETURN false;
END $$;

CREATE FUNCTION payment_identity_valid(identity_ref text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_orders o ON o.id=i.order_id
 JOIN payment_verified_evidence e ON e.id=i.evidence_id JOIN payment_provider_revisions r ON (r.environment_id,r.merchant_id,r.order_id,r.provider_order_revision)=(i.environment_id,i.merchant_id,i.order_id,i.provider_order_revision)
 WHERE i.id=identity_ref AND payment_capture_evidence_valid(e.id) AND e.kind='capture' AND (i.environment_id,i.merchant_id,i.order_id,i.tenant_id,i.amount,i.currency)=(o.environment_id,o.merchant_id,o.id,o.tenant_id,o.amount,o.currency)
 AND (i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)=(e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
 AND r.kind='capture' AND r.fact_digest=e.fact_digest AND i.captured_at>=o.created_at AND i.captured_at<o.expires_at
 AND NOT EXISTS(SELECT 1 FROM payment_provider_revisions closed JOIN payment_verified_evidence ce ON ce.id=closed.evidence_id WHERE closed.order_id=o.id AND closed.kind='closed' AND (i.captured_at>=ce.happened_at OR i.provider_order_revision>=closed.provider_order_revision)))
$$;
CREATE FUNCTION payment_capture_source_valid(capture_ref text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM payment_captures c JOIN payment_orders o ON o.id=c.order_id JOIN payment_order_capture_selections s ON s.order_id=o.id
 JOIN payment_transaction_identities i ON i.id=s.identity_id JOIN payment_verified_evidence e ON e.id=c.verified_evidence_id
 WHERE c.id=capture_ref AND payment_identity_valid(i.id) AND payment_capture_evidence_valid(e.id) AND payment_capture_evidence_valid(s.first_evidence_id) AND c.identity_id=i.id
 AND (c.tenant_id,c.environment_id,c.merchant_id,c.provider_transaction_id,c.amount,c.wallet_credit_amount,c.currency,c.captured_at,c.provider_order_revision)
  =(i.tenant_id,i.environment_id,i.merchant_id,i.provider_transaction_id,i.amount,o.wallet_credit_amount,i.currency,i.captured_at,i.provider_order_revision)
 AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
  =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)
 AND EXISTS(SELECT 1 FROM payment_verified_evidence first WHERE first.id=s.first_evidence_id
  AND (first.environment_id,first.merchant_id,first.order_id,first.provider_transaction_id,first.amount,first.currency,first.happened_at,first.provider_order_revision)
   =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision))
 AND NOT EXISTS(SELECT 1 FROM payment_review_facts v WHERE v.selection_order_id=s.order_id AND v.reason='funding_amount_overflow'))
$$;

ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check;
ALTER TABLE ledger_accounts ADD CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING'));
ALTER TABLE ledger_accounts ADD CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR
 (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR (tenant_id IS NULL AND kind IN ('OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING') AND balance<=0));
ALTER TABLE funding_receipts DROP CONSTRAINT funding_receipts_source_kind_check;
ALTER TABLE funding_receipts ADD CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture'));
-- Literal 017 A predicate in a new invoker function; no rename/drop of its original.
CREATE FUNCTION payment_legacy_funding_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 LEFT JOIN credit_orders c ON f.source_kind='manual_credit' AND c.id=f.source_id
 LEFT JOIN vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 LEFT JOIN voucher_batches b ON b.id=v.batch_id
 LEFT JOIN voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 LEFT JOIN commerce_commands cmd ON cmd.operation_id=r.operation_id
 LEFT JOIN commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id AND j.currency='CNY' AND f.currency='CNY' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.state='approved' AND c.journal_id=j.id AND c.tenant_id=f.tenant_id AND c.amount=f.amount AND c.approver<>c.requester AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND j.actor=c.approver)
 OR (f.source_kind='voucher_grant' AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
  AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
  AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
  AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
  AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;
CREATE FUNCTION payment_sandbox_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='sandbox_capture'
 JOIN payment_captures c ON c.id=f.source_id JOIN payment_effects x ON x.capture_id=c.id
 WHERE j.id=journal_ref AND payment_capture_source_valid(c.id)
 AND EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_evidence_processing p ON p.evidence_id=i.evidence_id
  WHERE i.id=c.identity_id AND p.generation=i.processing_generation AND p.state IN ('processed','processing_review'))
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
CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
BEGIN
 -- Use a statement-level branch: the worker must not need SELECT on A's
 -- voucher/manual source tables just to validate its sandbox journal.
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='sandbox_capture') THEN RETURN payment_sandbox_journal_valid(journal_ref);END IF;
 RETURN payment_legacy_funding_journal_valid(journal_ref);
END
$$;

CREATE OR REPLACE FUNCTION funding_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid TEXT; credit_state TEXT;
BEGIN
 IF TG_TABLE_NAME='credit_orders' THEN
  SELECT state,journal_id INTO credit_state,jid FROM credit_orders WHERE id=NEW.id;
  IF credit_state='pending' THEN
   IF EXISTS(SELECT 1 FROM journals WHERE kind='credit' AND operation_id=NEW.id) OR EXISTS(SELECT 1 FROM projection_outbox WHERE kind='credited' AND operation_id=NEW.id) THEN RAISE EXCEPTION 'pending credit has funding'; END IF;
   RETURN NULL;
  END IF;
 ELSIF TG_TABLE_NAME='journals' THEN
  IF NEW.kind NOT IN ('credit','voucher_grant','sandbox_capture') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION payment_source_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE identity_ref text;order_ref text;jid text;
BEGIN
 IF TG_TABLE_NAME='payment_transaction_identities' THEN identity_ref:=NEW.id;order_ref:=NEW.order_id;
  IF NOT payment_identity_valid(identity_ref) OR NOT EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=NEW.evidence_id AND p.generation=NEW.processing_generation AND p.state IN ('processed','processing_review')) THEN RAISE EXCEPTION 'payment transaction identity invalid' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM payment_order_capture_selections WHERE identity_id=identity_ref) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE identity_id=identity_ref AND order_id=order_ref AND reason='duplicate_order_capture') THEN RAISE EXCEPTION 'payment identity neither selected nor reviewed' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME='payment_order_capture_selections' THEN
  order_ref:=NEW.order_id;identity_ref:=NEW.identity_id;
  IF NOT payment_identity_valid(identity_ref) OR NOT EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_verified_evidence e ON e.id=NEW.first_evidence_id WHERE i.id=identity_ref AND payment_capture_evidence_valid(e.id) AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)=(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)) THEN RAISE EXCEPTION 'payment selection identity mismatch' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM payment_captures WHERE order_id=order_ref) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE selection_order_id=order_ref AND identity_id=identity_ref AND reason='funding_amount_overflow') THEN RAISE EXCEPTION 'payment selection missing funding or review' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME IN ('payment_captures','payment_effects') THEN
  IF TG_TABLE_NAME='payment_captures' THEN order_ref:=NEW.order_id;SELECT journal_id INTO jid FROM funding_receipts WHERE source_kind='sandbox_capture' AND source_id=NEW.id;
  ELSE SELECT c.order_id,f.journal_id INTO order_ref,jid FROM payment_captures c LEFT JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id WHERE c.id=NEW.capture_id;END IF;
  IF jid IS NULL OR NOT payment_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'payment funding graph incomplete' USING ERRCODE='23514';END IF;
 END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_identity_complete AFTER INSERT ON payment_transaction_identities DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();
CREATE CONSTRAINT TRIGGER payment_selection_complete AFTER INSERT ON payment_order_capture_selections DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();
CREATE CONSTRAINT TRIGGER payment_capture_complete AFTER INSERT ON payment_captures DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();
CREATE CONSTRAINT TRIGGER payment_effect_complete AFTER INSERT ON payment_effects DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();
CREATE FUNCTION payment_extra_funding_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid text;
BEGIN
 IF TG_TABLE_NAME='journals' THEN IF NEW.kind<>'sandbox_capture' THEN RETURN NULL;END IF;jid:=NEW.id;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM ledger_accounts WHERE id=NEW.account_id AND kind='SANDBOX_FUNDING_CLEARING') THEN RETURN NULL;END IF;jid:=NEW.journal_id;
 END IF;
 IF NOT payment_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'sandbox journal or clearing missing funding source' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_sandbox_journal_complete AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_extra_funding_complete();
CREATE CONSTRAINT TRIGGER payment_sandbox_clearing_complete AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_extra_funding_complete();

-- Entry ownership is distinct from external signature authentication.
CREATE FUNCTION payment_source_writer() RETURNS trigger LANGUAGE plpgsql AS $$
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
 IF is_sandbox AND (worker IS NULL OR current_user<>worker OR session_user<>worker OR NOT EXISTS(SELECT 1 FROM payment_sandbox_environment WHERE singleton AND database_oid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND schema_oid=current_schema()::regnamespace::oid)) THEN RAISE EXCEPTION 'sandbox funding requires payment worker' USING ERRCODE='42501';END IF;
 IF current_user=worker AND NOT coalesce(is_sandbox,false) THEN RAISE EXCEPTION 'payment worker cannot write other funding' USING ERRCODE='42501';END IF;
 IF is_sandbox THEN
  PERFORM payment_require_rc();
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
CREATE TRIGGER payment_receipt_writer BEFORE INSERT ON funding_receipts FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_journal_writer BEFORE INSERT ON journals FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_credited_writer BEFORE INSERT ON projection_outbox FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_capture_writer BEFORE INSERT ON payment_captures FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_effect_writer BEFORE INSERT ON payment_effects FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_identity_writer BEFORE INSERT ON payment_transaction_identities FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_selection_writer BEFORE INSERT ON payment_order_capture_selections FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_revision_writer BEFORE INSERT ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE TRIGGER payment_review_writer BEFORE INSERT ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION payment_source_writer();
CREATE FUNCTION payment_worker_tenant_lock_only() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF current_user=(SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment worker tenant privilege is lock-only' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_worker_tenant_lock_only BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION payment_worker_tenant_lock_only();

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
CREATE FUNCTION payment_sku_root_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c payment_sku_changes%ROWTYPE;v bigint;
BEGIN
 PERFORM payment_require_rc();
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
CREATE TRIGGER payment_sku_root_guard BEFORE INSERT OR UPDATE OR DELETE ON payment_skus FOR EACH ROW EXECUTE FUNCTION payment_sku_root_guard();
CREATE FUNCTION payment_sku_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s payment_skus%ROWTYPE;c commerce_commands%ROWTYPE;env payment_sandbox_environment%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.sku_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=NEW.publish_operation_id;
 IF c.kind<>'payment.sku.publish' OR c.scope<>'platform' OR c.actor IS DISTINCT FROM NEW.created_by THEN RAISE EXCEPTION 'payment SKU publish command mismatch';END IF;
 PERFORM id FROM users WHERE id=c.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=c.actor AND status='active' AND platform_role='admin') THEN RAISE EXCEPTION 'payment SKU publisher forbidden';END IF;
 SELECT * INTO STRICT env FROM payment_sandbox_environment WHERE singleton;
 NEW.environment_id:=env.id;NEW.merchant_id:=env.merchant_id;NEW.payee_label:=env.payee_label;NEW.currency:='CNY';NEW.wallet_credit_amount:=NEW.amount;
 NEW.version:=s.last_version+1;NEW.checkout_ttl_seconds:=900;NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_sku_version_insert BEFORE INSERT ON payment_sku_versions FOR EACH ROW EXECUTE FUNCTION payment_sku_version_insert();
CREATE FUNCTION payment_sku_change_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s payment_skus%ROWTYPE;c commerce_commands%ROWTYPE;v payment_sku_versions%ROWTYPE;first_publish boolean;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.sku_id FOR NO KEY UPDATE;
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
CREATE TRIGGER payment_sku_change_insert BEFORE INSERT ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION payment_sku_change_insert();
CREATE FUNCTION payment_sku_change_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE payment_skus SET revision=NEW.after_revision,current_version_id=NEW.after_version_id,enabled=NEW.after_enabled,
  last_version=CASE WHEN NEW.kind='payment.sku.publish' THEN (SELECT version FROM payment_sku_versions WHERE id=NEW.published_version_id) ELSE last_version END,
  last_change_operation_id=NEW.operation_id WHERE id=NEW.sku_id;RETURN NULL;
END $$;
CREATE TRIGGER payment_sku_change_apply AFTER INSERT ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION payment_sku_change_apply();

CREATE OR REPLACE FUNCTION payment_order_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v payment_sku_versions%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();
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
CREATE FUNCTION payment_cancel_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE o payment_orders%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT o FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
 PERFORM id FROM tenants WHERE id=o.tenant_id FOR SHARE;PERFORM user_id FROM memberships WHERE tenant_id=o.tenant_id AND user_id=NEW.actor FOR SHARE;PERFORM id FROM users WHERE id=NEW.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=o.tenant_id AND m.user_id=NEW.actor AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'payment cancel owner forbidden';END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.operation_id AND kind='payment.order.cancel' AND actor=NEW.actor AND scope='tenant:'||o.tenant_id) OR
  EXISTS(SELECT 1 FROM payment_captures WHERE order_id=o.id) OR EXISTS(SELECT 1 FROM payment_provider_revisions WHERE order_id=o.id AND kind='closed') THEN RAISE EXCEPTION 'payment cancel state invalid';END IF;
 NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_cancel_insert BEFORE INSERT ON payment_cancel_intents FOR EACH ROW EXECUTE FUNCTION payment_cancel_insert();

CREATE FUNCTION payment_check_human(operation_ref text) RETURNS void LANGUAGE plpgsql AS $$
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
CREATE FUNCTION payment_human_complete() RETURNS trigger LANGUAGE plpgsql AS $$
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
 PERFORM payment_check_human(operation_ref);RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_human_command_complete AFTER INSERT ON commerce_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_human_terminal_complete AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_sku_complete AFTER INSERT OR UPDATE ON payment_skus DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_sku_version_complete AFTER INSERT ON payment_sku_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_sku_change_complete AFTER INSERT ON payment_sku_changes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_order_complete AFTER INSERT ON payment_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_cancel_complete AFTER INSERT ON payment_cancel_intents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();
CREATE CONSTRAINT TRIGGER payment_mutation_outbox_complete AFTER INSERT ON payment_command_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();

-- Lease machines. The online worker owns transitions; immutable evidence may
-- be retained after a stale response, but stale generations cannot ACK work.
ALTER TABLE payment_command_outbox ADD COLUMN last_submission_unknown_at timestamptz(6);
ALTER TABLE payment_command_outbox ADD CHECK(generation=attempt_count);
ALTER TABLE payment_query_attempts ADD COLUMN mutation_generation bigint NOT NULL CHECK(mutation_generation>0);
ALTER TABLE payment_query_attempts ADD UNIQUE(supervisor_id,created_generation);
ALTER TABLE payment_evidence_processing ADD CHECK(generation=attempt_count);
ALTER TABLE payment_evidence_processing ADD CHECK(last_error_code IS NULL OR last_error_code IN ('temporary','permanent','attempts_exhausted'));


CREATE FUNCTION payment_backoff(n integer) RETURNS interval LANGUAGE sql IMMUTABLE AS $$
 SELECT make_interval(secs=>CASE WHEN n<=1 THEN 1 WHEN n=2 THEN 5 WHEN n=3 THEN 30 WHEN n=4 THEN 120 ELSE 300 END)
$$;
CREATE FUNCTION payment_outbox_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE o payment_orders%ROWTYPE;b payment_command_outbox%ROWTYPE;j jsonb;worker name;
BEGIN
 PERFORM payment_require_rc();SELECT worker_role INTO STRICT worker FROM payment_sandbox_environment WHERE singleton;
 IF NEW.state<>'pending' OR NEW.generation<>0 OR NEW.attempt_count<>0 OR NEW.lease_until IS NOT NULL OR NEW.current_query_id IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.last_submission_unknown_at IS NOT NULL THEN RAISE EXCEPTION 'payment outbox initial state';END IF;
 SELECT * INTO STRICT o FROM payment_orders WHERE id=NEW.order_id;
 IF NEW.kind='query' THEN
  IF current_user<>worker THEN RAISE EXCEPTION 'payment query supervisor requires worker' USING ERRCODE='42501';END IF;
  SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=NEW.original_outbox_id AND kind IN ('checkout','cancel') AND order_id=o.id;
 ELSE
  IF current_user=worker THEN RAISE EXCEPTION 'payment mutation requires application' USING ERRCODE='42501';END IF;
  j:=convert_from(NEW.canonical_parameters,'UTF8')::jsonb;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'command_id',j->>'merchant_order_id') IS DISTINCT FROM ('1',o.environment_id,o.merchant_id,NEW.external_command_id,o.merchant_order_id) THEN RAISE EXCEPTION 'payment mutation frozen identity mismatch';END IF;
  IF NEW.kind='checkout' AND ((j->>'amount',j->>'currency',j->>'created_at',j->>'expires_at') IS DISTINCT FROM (o.amount::text,o.currency,payment_time(o.created_at),payment_time(o.expires_at)) OR (SELECT count(*) FROM jsonb_object_keys(j))<>9) THEN RAISE EXCEPTION 'payment checkout frozen tuple mismatch';END IF;
  IF NEW.kind='cancel' AND ((SELECT count(*) FROM jsonb_object_keys(j))<>5 OR NOT EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=o.id)) THEN RAISE EXCEPTION 'payment cancel command missing intent';END IF;
 END IF;
 NEW.created_at:=clock_timestamp();NEW.next_attempt_at:=NEW.created_at;RETURN NEW;
END $$;
CREATE TRIGGER payment_outbox_insert BEFORE INSERT ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION payment_outbox_insert();

CREATE FUNCTION payment_query_attempt_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s payment_command_outbox%ROWTYPE;b payment_command_outbox%ROWTYPE;o payment_orders%ROWTYPE;j jsonb;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT s FROM payment_command_outbox WHERE id=NEW.supervisor_id AND kind='query';
 SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=s.original_outbox_id;
 SELECT * INTO STRICT o FROM payment_orders WHERE id=b.order_id;j:=convert_from(NEW.canonical_request,'UTF8')::jsonb;
 IF s.state<>'leased' OR s.lease_until<=clock_timestamp() OR NEW.created_generation<>s.generation OR b.generation=0 THEN RAISE EXCEPTION 'payment query preparation requires current claim';END IF;
 IF s.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_verified_queries WHERE query_id=s.current_query_id) THEN RAISE EXCEPTION 'payment unknown query must reuse immutable child';END IF;
 IF (j->>'schema_version',j->>'query_id',j->>'environment_id',j->>'merchant_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM ('1',NEW.query_id,o.environment_id,o.merchant_id,o.merchant_order_id,b.external_command_id,b.kind,b.parameter_digest) OR (SELECT count(*) FROM jsonb_object_keys(j))<>8 THEN RAISE EXCEPTION 'payment query request binding mismatch';END IF;
 NEW.mutation_generation:=b.generation;NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_query_attempt_insert BEFORE INSERT ON payment_query_attempts FOR EACH ROW EXECUTE FUNCTION payment_query_attempt_insert();
CREATE FUNCTION payment_observation_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b payment_command_outbox%ROWTYPE;o payment_orders%ROWTYPE;j jsonb;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=NEW.outbox_id AND kind IN ('checkout','cancel');
 SELECT * INTO STRICT o FROM payment_orders WHERE id=b.order_id;j:=convert_from(NEW.canonical_reply,'UTF8')::jsonb;
 IF NEW.generation>b.generation OR (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM ('1',o.environment_id,o.merchant_id,o.merchant_order_id,b.external_command_id,b.kind,b.parameter_digest) OR j->>'command_state' NOT IN ('in_progress','completed') THEN RAISE EXCEPTION 'payment command observation binding mismatch';END IF;
 NEW.observed_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_observation_insert BEFORE INSERT ON payment_command_observations FOR EACH ROW EXECUTE FUNCTION payment_observation_insert();

CREATE FUNCTION payment_outbox_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp();budget integer;j jsonb;child payment_query_attempts%ROWTYPE;prior_unknown timestamptz;
BEGIN
 PERFORM payment_require_rc();
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
 NEW.next_attempt_at:=CASE WHEN NEW.state='retry_wait' THEN stamp+payment_backoff(OLD.attempt_count) ELSE OLD.next_attempt_at END;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_outbox_transition BEFORE UPDATE OR DELETE ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION payment_outbox_transition();
CREATE FUNCTION payment_outbox_complete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind<>'query' AND (NEW.state='submission_unknown' OR (NEW.state='completed' AND EXISTS(SELECT 1 FROM payment_command_observations o WHERE o.outbox_id=NEW.id AND o.generation=NEW.generation AND (convert_from(o.canonical_reply,'UTF8')::jsonb->>'command_state'='in_progress' OR convert_from(o.canonical_reply,'UTF8')::jsonb#>>'{result,status}'='captured')))) AND NOT EXISTS(SELECT 1 FROM payment_command_outbox WHERE original_outbox_id=NEW.id AND kind='query') THEN RAISE EXCEPTION 'payment unknown mutation needs query supervisor';END IF;
 IF NEW.kind='query' AND NEW.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_query_attempts WHERE supervisor_id=NEW.id AND query_id=NEW.current_query_id) THEN RAISE EXCEPTION 'payment query pointer incomplete';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_outbox_complete AFTER INSERT OR UPDATE ON payment_command_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_outbox_complete();

CREATE FUNCTION payment_processing_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp();e payment_verified_evidence%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();
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
 NEW.lease_until:=NULL;NEW.next_attempt_at:=CASE WHEN NEW.state='retry_wait' THEN stamp+payment_backoff(OLD.attempt_count) ELSE OLD.next_attempt_at END;RETURN NEW;
END $$;
CREATE TRIGGER payment_processing_guard BEFORE INSERT OR UPDATE OR DELETE ON payment_evidence_processing FOR EACH ROW EXECUTE FUNCTION payment_processing_guard();
CREATE FUNCTION payment_evidence_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM payment_evidence_processing WHERE evidence_id=NEW.id) THEN RAISE EXCEPTION 'payment evidence requires processing root';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_evidence_complete AFTER INSERT ON payment_verified_evidence DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_evidence_complete();
CREATE FUNCTION payment_capture_generation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p payment_evidence_processing%ROWTYPE;
BEGIN
 SELECT * INTO STRICT p FROM payment_evidence_processing WHERE evidence_id=NEW.verified_evidence_id;
 IF p.state<>'leased' OR p.lease_until<=clock_timestamp() OR NEW.processing_generation<>p.generation THEN RAISE EXCEPTION 'payment source requires current evidence claim';END IF;
 NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;
CREATE TRIGGER payment_capture_generation BEFORE INSERT ON payment_captures FOR EACH ROW EXECUTE FUNCTION payment_capture_generation();

CREATE FUNCTION payment_worker_account_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RETURN NEW;END IF;
 IF TG_OP='DELETE' OR NEW.kind NOT IN ('AVAILABLE','SANDBOX_FUNDING_CLEARING') OR (TG_OP='INSERT' AND NEW.balance<>0) THEN RAISE EXCEPTION 'payment worker account kind or initial balance forbidden' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.kind) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.kind) THEN RAISE EXCEPTION 'payment worker account identity immutable' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_worker_account_guard BEFORE INSERT OR UPDATE OR DELETE ON ledger_accounts FOR EACH ROW EXECUTE FUNCTION payment_worker_account_guard();
CREATE FUNCTION payment_worker_line_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF current_user=(SELECT worker_role FROM payment_sandbox_environment WHERE singleton) AND NOT EXISTS(SELECT 1 FROM journals WHERE id=NEW.journal_id AND kind='sandbox_capture') THEN RAISE EXCEPTION 'payment worker cannot append other ledger effects' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;
CREATE TRIGGER payment_worker_line_guard BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION payment_worker_line_guard();
CREATE FUNCTION payment_worker_balance_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE aid text;
BEGIN
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RETURN NULL;END IF;
 IF TG_TABLE_NAME='ledger_accounts' THEN aid:=NEW.id;ELSE aid:=NEW.account_id;END IF;
 IF NOT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=aid AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)) THEN RAISE EXCEPTION 'payment worker balance cache mismatch' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_worker_account_complete AFTER INSERT OR UPDATE ON ledger_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_worker_balance_complete();
CREATE CONSTRAINT TRIGGER payment_worker_line_complete AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_worker_balance_complete();

CREATE UNIQUE INDEX payment_capture_audit_once ON audit_events(action,target) WHERE action='payment.capture.credited';
CREATE UNIQUE INDEX payment_human_audit_once ON audit_events(action,target) WHERE action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel');
CREATE FUNCTION payment_audit_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER payment_audit_guard BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION payment_audit_guard();
CREATE FUNCTION payment_audit_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid text;
BEGIN
 IF NEW.action='payment.capture.credited' THEN
  SELECT journal_id INTO jid FROM funding_receipts WHERE source_kind='sandbox_capture' AND source_id=NEW.target;
  IF jid IS NULL OR NOT payment_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'payment capture audit missing complete source';END IF;
 ELSIF NEW.action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel') THEN PERFORM payment_check_human(NEW.target);END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_audit_complete AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_audit_complete();

CREATE FUNCTION payment_original_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_TABLE_NAME='payment_inbox' THEN
  IF NOT EXISTS(SELECT 1 FROM payment_verified_evidence WHERE inbox_id=NEW.id) THEN RAISE EXCEPTION 'payment notification evidence missing';END IF;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM payment_verified_evidence WHERE verified_query_id=NEW.id) THEN RAISE EXCEPTION 'payment query evidence missing';END IF;
 END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_notification_complete AFTER INSERT ON payment_inbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_original_complete();
CREATE CONSTRAINT TRIGGER payment_query_original_complete AFTER INSERT ON payment_verified_queries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_original_complete();
-- Fixed actual application schema, invoker identity, and no PUBLIC entry.
DO $upgrade$
DECLARE f record;t record;
BEGIN
 FOR f IN SELECT p.oid,p.proname,pg_get_function_identity_arguments(p.oid) args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND (p.proname LIKE 'payment\_%' ESCAPE '\' OR p.proname IN ('funding_journal_valid','funding_fact_guard')) LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SECURITY INVOKER',current_schema(),f.proname,f.args);
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %I.%I(%s) FROM PUBLIC',current_schema(),f.proname,f.args);
 END LOOP;
 FOR t IN SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'payment\_%' ESCAPE '\' LOOP
  EXECUTE format('REVOKE ALL ON TABLE %I.%I FROM PUBLIC',current_schema(),t.tablename);
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_temp.payment_upgrade_guard g LEFT JOIN pg_proc p ON p.oid=g.oid
  WHERE p.oid IS NULL OR (p.proname,p.proowner,p.proargtypes,p.prorettype,p.prokind) IS DISTINCT FROM (g.proname,g.proowner,g.proargtypes,g.prorettype,g.prokind)
   OR EXISTS(SELECT 1 FROM jsonb_array_elements(g.incoming) d WHERE NOT EXISTS(SELECT 1 FROM pg_depend x WHERE jsonb_build_array(x.classid,x.objid,x.objsubid,x.refclassid,x.refobjid,x.refobjsubid,x.deptype)=d)))
 THEN RAISE EXCEPTION 'payment original function identity or dependency changed';END IF;
 IF (SELECT enabled OR revision<>1 FROM payment_policy WHERE singleton) OR EXISTS(SELECT 1 FROM payment_sandbox_environment)
 THEN RAISE EXCEPTION 'payment default off migration invariant failed';END IF;
END $upgrade$;
-- END 021_payment_sandbox.sql
-- BEGIN 022_payment_runtime_attestation.sql
-- 022-B3 private application/worker proof. No default proof, credentials or IO.
-- The owner installs exactly one attestation offline; online roles only read it.
CREATE TABLE payment_runtime_attestations (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton)
  REFERENCES payment_sandbox_environment(singleton),
 environment_id text NOT NULL UNIQUE REFERENCES payment_sandbox_environment(id),
 database_oid oid NOT NULL CHECK (database_oid<>0),
 schema_oid oid NOT NULL CHECK (schema_oid<>0),
 schema_owner_oid oid NOT NULL CHECK (schema_owner_oid<>0),
 app_role_oid oid NOT NULL CHECK (app_role_oid<>0),
 worker_role_oid oid NOT NULL CHECK (worker_role_oid<>0),
 projector_role_oid oid NOT NULL CHECK (projector_role_oid<>0),
 app_nonce_digest text NOT NULL CHECK (app_nonce_digest ~ '^[0-9a-f]{64}$'),
 worker_nonce_digest text NOT NULL CHECK (worker_nonce_digest ~ '^[0-9a-f]{64}$'),
 proof_version integer NOT NULL CHECK (proof_version=1),
 proof_canonical bytea NOT NULL CHECK (octet_length(proof_canonical) BETWEEN 1 AND 65536),
 proof_sha256 text NOT NULL CHECK (proof_sha256 ~ '^[0-9a-f]{64}$'
  AND proof_sha256=encode(sha256(proof_canonical),'hex')),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 CHECK (app_role_oid<>worker_role_oid AND app_role_oid<>projector_role_oid
  AND worker_role_oid<>projector_role_oid AND app_role_oid<>schema_owner_oid
  AND worker_role_oid<>schema_owner_oid AND projector_role_oid<>schema_owner_oid)
);
CREATE TRIGGER payment_runtime_attestation_immutable
 BEFORE UPDATE OR DELETE ON payment_runtime_attestations FOR EACH ROW
 EXECUTE FUNCTION reject_history_mutation();
REVOKE ALL ON TABLE payment_runtime_attestations FROM PUBLIC;
-- END 022_payment_runtime_attestation.sql
-- BEGIN 023_wallet_sources.sql
-- Pre-MVP single current wallet model. Existing financial history is never
-- guessed, erased, or silently imported; use a separate fresh current database.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM journals) OR EXISTS(SELECT 1 FROM funding_receipts)
 OR EXISTS(SELECT 1 FROM reservations) OR EXISTS(SELECT 1 FROM settlements)
 OR EXISTS(SELECT 1 FROM refund_orders) OR EXISTS(SELECT 1 FROM credit_orders)
 OR EXISTS(SELECT 1 FROM ledger_accounts WHERE balance<>0)
 OR EXISTS(SELECT 1 FROM api_keys WHERE held<>0 OR spent<>0) THEN
  RAISE EXCEPTION '023 requires a fresh wallet history; preserve the old database and initialize a separate current database';
 END IF;
END $$;

ALTER TABLE journals ADD UNIQUE(tenant_id,id);
ALTER TABLE settlements ADD UNIQUE(tenant_id,id);
ALTER TABLE refund_orders ADD UNIQUE(tenant_id,id);
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check;
ALTER TABLE ledger_accounts ADD CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING'));
ALTER TABLE ledger_accounts ADD CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR
 (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR
 (tenant_id IS NULL AND kind IN ('OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING') AND balance<=0));
ALTER TABLE funding_receipts DROP CONSTRAINT funding_receipts_source_kind_check;
ALTER TABLE funding_receipts ADD CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture','wallet_opening'));

CREATE TABLE wallet_openings (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL UNIQUE REFERENCES tenants(id),
 external_ref TEXT NOT NULL UNIQUE CHECK(length(external_ref) BETWEEN 1 AND 160),
 amount BIGINT NOT NULL CHECK(amount>0), actor TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), UNIQUE(tenant_id,id)
);
CREATE TRIGGER immutable_wallet_opening BEFORE UPDATE OR DELETE ON wallet_openings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE funding_lots (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL REFERENCES tenants(id),
 funding_receipt_id TEXT NOT NULL UNIQUE,
 source_kind TEXT NOT NULL CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture','wallet_opening')),
 source_id TEXT NOT NULL, refund_class TEXT NOT NULL CHECK(refund_class IN ('nonrefundable','sandbox_original')),
 issued_amount BIGINT NOT NULL CHECK(issued_amount>0), fifo_at TIMESTAMPTZ NOT NULL,
 CHECK(id=funding_receipt_id), CHECK((source_kind='sandbox_capture')=(refund_class='sandbox_original')),
 UNIQUE(tenant_id,id), UNIQUE(source_kind,source_id),
 FOREIGN KEY(tenant_id,funding_receipt_id) REFERENCES funding_receipts(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_lot_balances (
 lot_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL,
 available_amount BIGINT NOT NULL CHECK(available_amount>=0), held_amount BIGINT NOT NULL CHECK(held_amount>=0),
 consumed_amount BIGINT NOT NULL CHECK(consumed_amount>=0), revision BIGINT NOT NULL CHECK(revision>0),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_lot_events (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, lot_id TEXT NOT NULL,
 operation_id TEXT NOT NULL, journal_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('funding','reserve','finish','refund')), revision BIGINT NOT NULL CHECK(revision>0),
 delta_available BIGINT NOT NULL, delta_held BIGINT NOT NULL, delta_consumed BIGINT NOT NULL,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(lot_id,revision), UNIQUE(operation_id,lot_id),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_reservation_lots (
 tenant_id TEXT NOT NULL, reservation_id TEXT NOT NULL, request_id TEXT NOT NULL, lot_id TEXT NOT NULL,
 allocation_index BIGINT NOT NULL CHECK(allocation_index>0), amount BIGINT NOT NULL CHECK(amount>0),
 PRIMARY KEY(reservation_id,allocation_index), UNIQUE(reservation_id,lot_id),
 UNIQUE(tenant_id,reservation_id,allocation_index,lot_id),
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_settlement_lots (
 tenant_id TEXT NOT NULL, settlement_id TEXT NOT NULL, reservation_id TEXT NOT NULL,
 allocation_index BIGINT NOT NULL, lot_id TEXT NOT NULL,
 charged_amount BIGINT NOT NULL CHECK(charged_amount>=0), released_amount BIGINT NOT NULL CHECK(released_amount>=0),
 PRIMARY KEY(settlement_id,allocation_index), UNIQUE(tenant_id,settlement_id,allocation_index,lot_id),
 FOREIGN KEY(tenant_id,settlement_id) REFERENCES settlements(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,reservation_id,allocation_index,lot_id) REFERENCES funding_reservation_lots(tenant_id,reservation_id,allocation_index,lot_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_consumption_refund_lots (
 tenant_id TEXT NOT NULL, refund_order_id TEXT NOT NULL, settlement_id TEXT NOT NULL,
 allocation_index BIGINT NOT NULL, lot_id TEXT NOT NULL, refund_index BIGINT NOT NULL CHECK(refund_index>0), amount BIGINT NOT NULL CHECK(amount>0),
 PRIMARY KEY(refund_order_id,refund_index), UNIQUE(refund_order_id,allocation_index),
 FOREIGN KEY(tenant_id,refund_order_id) REFERENCES refund_orders(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,settlement_id,allocation_index,lot_id) REFERENCES funding_settlement_lots(tenant_id,settlement_id,allocation_index,lot_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX wallet_lots_fifo ON funding_lots(tenant_id,fifo_at,id COLLATE "C");
CREATE INDEX wallet_lot_events_journal ON funding_lot_events(journal_id);
CREATE INDEX wallet_lot_refunds_settlement ON funding_consumption_refund_lots(settlement_id,allocation_index);

CREATE FUNCTION wallet_opening_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='wallet_opening'
 JOIN wallet_openings o ON o.id=f.source_id AND o.tenant_id=f.tenant_id
 WHERE j.id=journal_ref AND f.amount=o.amount AND f.currency='CNY' AND j.currency='CNY'
 AND j.tenant_id=o.tenant_id AND j.kind='wallet_opening' AND j.operation_id=o.id AND j.reference=o.id AND j.actor=o.actor
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=o.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=o.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='WALLET_OPENING_CLEARING' AND l.signed_delta=-o.amount)
 AND (SELECT count(*) FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited' AND x.tenant_id=o.tenant_id
  AND x.schema_version=1 AND jsonb_typeof(x.sanitized_payload->'amount')='string' AND x.sanitized_payload->>'amount'=o.amount::text))
$$;
CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE journal_kind TEXT;
BEGIN
 SELECT kind INTO journal_kind FROM journals WHERE id=journal_ref;
 IF journal_kind='sandbox_capture' THEN RETURN payment_sandbox_journal_valid(journal_ref);
 ELSIF journal_kind='wallet_opening' THEN RETURN wallet_opening_journal_valid(journal_ref);
 ELSE RETURN payment_legacy_funding_journal_valid(journal_ref); END IF;
END $$;

CREATE FUNCTION wallet_lot_balance_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE principal BIGINT;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'wallet balances cannot be deleted'; END IF;
 SELECT issued_amount INTO STRICT principal FROM funding_lots WHERE id=NEW.lot_id AND tenant_id=NEW.tenant_id;
 IF NEW.available_amount::numeric+NEW.held_amount::numeric+NEW.consumed_amount::numeric<>principal::numeric THEN RAISE EXCEPTION 'wallet lot principal mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.available_amount<>principal OR NEW.held_amount<>0 OR NEW.consumed_amount<>0 THEN RAISE EXCEPTION 'invalid wallet genesis'; END IF;
 ELSE
  IF NEW.lot_id<>OLD.lot_id OR NEW.tenant_id<>OLD.tenant_id OR NEW.revision::numeric<>OLD.revision::numeric+1
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=NEW.lot_id AND e.tenant_id=NEW.tenant_id AND e.revision=NEW.revision AND e.created_xid=pg_current_xact_id()
    AND e.delta_available::numeric=NEW.available_amount::numeric-OLD.available_amount::numeric
    AND e.delta_held::numeric=NEW.held_amount::numeric-OLD.held_amount::numeric
    AND e.delta_consumed::numeric=NEW.consumed_amount::numeric-OLD.consumed_amount::numeric) THEN RAISE EXCEPTION 'wallet update requires matching event'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER wallet_balance_write BEFORE INSERT OR UPDATE OR DELETE ON funding_lot_balances FOR EACH ROW EXECUTE FUNCTION wallet_lot_balance_guard();
CREATE FUNCTION wallet_lot_event_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=NEW.journal_id AND j.tenant_id=NEW.tenant_id AND j.operation_id=NEW.operation_id AND j.created_txid=txid_current()) THEN RAISE EXCEPTION 'wallet event requires current journal'; END IF;
 NEW.created_xid:=pg_current_xact_id(); RETURN NEW;
END $$;
CREATE TRIGGER wallet_event_stamp BEFORE INSERT ON funding_lot_events FOR EACH ROW EXECUTE FUNCTION wallet_lot_event_stamp();

CREATE FUNCTION wallet_lot_valid(lot_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM funding_lots l JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id
 JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
 WHERE l.id=lot_ref AND (l.source_kind,l.source_id,l.issued_amount,l.fifo_at)=(f.source_kind,f.source_id,f.amount,f.created_at)
 AND funding_journal_valid(f.journal_id)
 AND (SELECT count(*) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.revision
 AND (SELECT min(revision) FROM funding_lot_events e WHERE e.lot_id=l.id)=1
 AND (SELECT max(revision) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.revision
 AND (SELECT coalesce(sum(delta_available::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.available_amount::numeric
 AND (SELECT coalesce(sum(delta_held::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.held_amount::numeric
 AND (SELECT coalesce(sum(delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.consumed_amount::numeric
 AND EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=l.id AND e.revision=1 AND e.kind='funding' AND e.journal_id=f.journal_id
  AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0))
$$;
-- Statement dispatch preserves the payment worker's narrow funding-only reads.
CREATE FUNCTION wallet_event_valid(event_ref TEXT) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE event_kind TEXT;
BEGIN
 SELECT kind INTO event_kind FROM funding_lot_events WHERE id=event_ref;
 CASE event_kind
 WHEN 'funding' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id
  WHERE e.id=event_ref AND e.revision=1 AND f.journal_id=j.id AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0 AND funding_journal_valid(j.id));
 WHEN 'reserve' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id
  WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='reserve' AND j.reference=r.request_id AND a.request_id=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0);
 WHEN 'finish' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id
  WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.reference=s.request_id AND j.kind=s.status
  AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount);
 WHEN 'refund' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_consumption_refund_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN refund_orders r ON r.id=a.refund_order_id AND r.tenant_id=a.tenant_id
  WHERE e.id=event_ref AND r.state='approved' AND r.journal_id=j.id AND j.operation_id=r.id AND j.kind='refund' AND j.reference=r.settlement_id
  AND e.delta_available=a.amount AND e.delta_held=0 AND e.delta_consumed=-a.amount);
 ELSE RETURN false;
 END CASE;
END $$;

CREATE FUNCTION wallet_reservation_fifo_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_lot TEXT; available BIGINT; remaining NUMERIC; allocated NUMERIC; edge_count BIGINT;
BEGIN
 -- Every customer money mutation serializes on these existing account rows.
 PERFORM id FROM ledger_accounts WHERE tenant_id=NEW.tenant_id AND kind='AVAILABLE' FOR UPDATE;
 SELECT coalesce(sum(amount::numeric),0),count(*) INTO allocated,edge_count FROM funding_reservation_lots WHERE reservation_id=NEW.reservation_id;
 SELECT r.amount::numeric-allocated INTO STRICT remaining FROM reservations r
  WHERE r.id=NEW.reservation_id AND r.tenant_id=NEW.tenant_id AND r.request_id=NEW.request_id AND r.status='reserved';
 SELECT l.id,b.available_amount INTO next_lot,available FROM funding_lots l JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
  WHERE l.tenant_id=NEW.tenant_id AND b.available_amount>0 ORDER BY l.fifo_at,l.id COLLATE "C" LIMIT 1;
 IF NEW.allocation_index<>edge_count+1 OR NEW.lot_id IS DISTINCT FROM next_lot OR remaining<=0
  OR NEW.amount::numeric<>least(available::numeric,remaining) THEN RAISE EXCEPTION 'wallet reservation requires FIFO source allocation'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER wallet_reservation_fifo BEFORE INSERT ON funding_reservation_lots FOR EACH ROW EXECUTE FUNCTION wallet_reservation_fifo_guard();

CREATE FUNCTION wallet_reservation_valid(res_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM reservations r WHERE r.id=res_ref
 AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_reservation_lots a WHERE a.reservation_id=r.id)=r.amount::numeric
 AND (r.amount=0 OR (SELECT min(allocation_index)=1 AND max(allocation_index)=count(*) FROM funding_reservation_lots a WHERE a.reservation_id=r.id))
 AND NOT EXISTS(SELECT 1 FROM funding_reservation_lots a WHERE a.reservation_id=r.id AND (a.tenant_id<>r.tenant_id OR a.request_id<>r.request_id OR NOT EXISTS(
  SELECT 1 FROM funding_lot_events e WHERE e.lot_id=a.lot_id AND e.operation_id=r.id AND e.kind='reserve' AND wallet_event_valid(e.id))))
 AND EXISTS(SELECT 1 FROM journals j WHERE j.operation_id=r.id AND j.tenant_id=r.tenant_id AND j.kind='reserve' AND j.reference=r.request_id))
$$;
CREATE FUNCTION wallet_settlement_valid(set_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM settlements s JOIN reservations r ON r.request_id=s.request_id AND r.tenant_id=s.tenant_id WHERE s.id=set_ref
 AND (SELECT count(*) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=(SELECT count(*) FROM funding_reservation_lots a WHERE a.reservation_id=r.id)
 AND (SELECT coalesce(sum(charged_amount::numeric),0) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=s.charged_amount::numeric
 AND (SELECT coalesce(sum(released_amount::numeric),0) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=r.amount::numeric-s.charged_amount::numeric
 AND NOT EXISTS(SELECT 1 FROM funding_settlement_lots f JOIN funding_reservation_lots a ON a.reservation_id=f.reservation_id AND a.allocation_index=f.allocation_index
  WHERE f.settlement_id=s.id AND (f.reservation_id<>r.id OR f.tenant_id<>s.tenant_id OR f.lot_id<>a.lot_id
   OR f.charged_amount::numeric+f.released_amount::numeric<>a.amount::numeric
   OR f.charged_amount::numeric<>least(a.amount::numeric,greatest(0::numeric,s.charged_amount::numeric-(SELECT coalesce(sum(p.amount::numeric),0) FROM funding_reservation_lots p WHERE p.reservation_id=r.id AND p.allocation_index<a.allocation_index)))
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=s.id AND e.lot_id=f.lot_id AND e.kind='finish' AND wallet_event_valid(e.id))))
 AND (SELECT coalesce(sum(amount::numeric),0) FROM funding_consumption_refund_lots x WHERE x.settlement_id=s.id)=s.refunded_amount::numeric
 AND NOT EXISTS(SELECT 1 FROM funding_settlement_lots a WHERE a.settlement_id=s.id
  AND (SELECT coalesce(sum(x.amount::numeric),0) FROM funding_consumption_refund_lots x WHERE x.settlement_id=s.id AND x.allocation_index=a.allocation_index)
   <>least(a.charged_amount::numeric,greatest(0::numeric,s.refunded_amount::numeric-(SELECT coalesce(sum(p.charged_amount::numeric),0) FROM funding_settlement_lots p WHERE p.settlement_id=s.id AND p.allocation_index>a.allocation_index)))))
$$;
CREATE FUNCTION wallet_refund_valid(ref_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM refund_orders r WHERE r.id=ref_ref AND
  ((r.state<>'approved' AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)) OR
   (r.state='approved' AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)=r.amount::numeric
    AND (SELECT min(refund_index)=1 AND max(refund_index)=count(*) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)
    AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id AND (a.settlement_id<>r.settlement_id OR a.tenant_id<>r.tenant_id
      OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=r.id AND e.lot_id=a.lot_id AND e.kind='refund' AND wallet_event_valid(e.id))))
    AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a JOIN funding_consumption_refund_lots p ON p.refund_order_id=a.refund_order_id AND p.refund_index<a.refund_index
     WHERE a.refund_order_id=r.id AND p.allocation_index<=a.allocation_index)
    AND wallet_settlement_valid(r.settlement_id))))
$$;
CREATE FUNCTION wallet_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j WHERE j.id=journal_ref
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE')
   =(SELECT coalesce(sum(e.delta_available::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='HELD')
   =(SELECT coalesce(sum(e.delta_held::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REVENUE')
   =(SELECT coalesce(sum(e.delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.journal_id=j.id AND NOT wallet_event_valid(e.id)))
$$;
CREATE FUNCTION wallet_account_valid(account_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=account_ref
 AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)
 AND (a.tenant_id IS NULL OR (a.kind='AVAILABLE' AND a.balance::numeric=(SELECT coalesce(sum(b.available_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))
  OR (a.kind='HELD' AND a.balance::numeric=(SELECT coalesce(sum(b.held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))))
$$;
CREATE FUNCTION wallet_source_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ok BOOLEAN; target_lot TEXT;
BEGIN
 CASE TG_TABLE_NAME
 WHEN 'wallet_openings' THEN SELECT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='wallet_opening' AND f.source_id=NEW.id AND wallet_opening_journal_valid(f.journal_id)) INTO ok;
 WHEN 'funding_receipts' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lots' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lot_balances' THEN SELECT wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'funding_lot_events' THEN SELECT wallet_event_valid(NEW.id) AND wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'reservations' THEN SELECT wallet_reservation_valid(NEW.id) INTO ok;
 WHEN 'funding_reservation_lots' THEN SELECT wallet_reservation_valid(NEW.reservation_id) INTO ok;
 WHEN 'settlements' THEN SELECT wallet_settlement_valid(NEW.id) INTO ok;
 WHEN 'funding_settlement_lots' THEN SELECT wallet_settlement_valid(NEW.settlement_id) INTO ok;
 WHEN 'refund_orders' THEN SELECT wallet_refund_valid(NEW.id) INTO ok;
 WHEN 'funding_consumption_refund_lots' THEN SELECT wallet_refund_valid(NEW.refund_order_id) INTO ok;
 WHEN 'journals' THEN SELECT wallet_journal_valid(NEW.id) INTO ok;
 WHEN 'ledger_accounts' THEN SELECT wallet_account_valid(NEW.id) INTO ok;
 ELSE ok:=false;
 END CASE;
 IF ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'wallet source facts do not match'; END IF;
 RETURN NULL;
END $$;
DO $$ DECLARE t TEXT; BEGIN
 FOREACH t IN ARRAY ARRAY['funding_lots','funding_lot_events','funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots'] LOOP
  EXECUTE format('CREATE TRIGGER immutable_wallet_fact BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_history_mutation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['wallet_openings','funding_receipts','funding_lots','funding_lot_balances','funding_lot_events','reservations','funding_reservation_lots','settlements','funding_settlement_lots','refund_orders','funding_consumption_refund_lots','journals','ledger_accounts'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard()',t);
 END LOOP;
END $$;

-- All current wallet validators execute as the caller within the fixed schema.
DO $$ DECLARE f RECORD; BEGIN
 FOR f IN SELECT p.proname,pg_get_function_identity_arguments(p.oid) args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND (left(p.proname,7)='wallet_' OR p.proname='funding_journal_valid') LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SECURITY INVOKER',current_schema(),f.proname,f.args);
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %I.%I(%s) FROM PUBLIC',current_schema(),f.proname,f.args);
 END LOOP;
END $$;
-- END 023_wallet_sources.sql
-- BEGIN 024_unified_text_metering.sql
-- 028-U: one current text-vector metering model.
-- Empty current databases only. 001–023 files are unchanged.
-- Per-million token integers remain the admin input; stored rates are reduced
-- rationals over 1_000_000 so one final ceil matches the previous formula.

DO $$
DECLARE name text; n bigint;
BEGIN
 IF current_setting('server_version_num')::integer < 150000 THEN
  RAISE EXCEPTION 'unified text metering requires PostgreSQL 15 or later';
 END IF;
 FOREACH name IN ARRAY ARRAY[
  'request_records','request_idempotency','reservations','upstream_attempts','metering_events',
  'metering_event_conflicts','finalization_records','settlements','reconciliation_cases',
  'recovery_jobs','delivery_observations','platform_probe_attempts','provider_cost_corrections',
  'procurement_reservations','procurement_budget_events','procurement_evidence_identities','procurement_cost_proposals',
  'journals','journal_lines','funding_receipts','wallet_openings','funding_lots','funding_lot_events',
  'funding_lot_balances','funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots',
  'models','model_capability_versions','route_policy_versions','model_bindings','price_versions','cost_versions',
  'pricing_operations']
 LOOP
  EXECUTE format('SELECT count(*) FROM %I', name) INTO n;
  IF n <> 0 THEN
   RAISE EXCEPTION 'unified text metering refuses existing % history', name;
  END IF;
 END LOOP;
 IF EXISTS (SELECT 1 FROM api_keys WHERE held <> 0 OR spent <> 0) THEN
  RAISE EXCEPTION 'unified text metering refuses key held or spent history';
 END IF;
 IF EXISTS (SELECT 1 FROM procurement_accounts WHERE held_amount <> 0 OR spent_amount <> 0) THEN
  RAISE EXCEPTION 'unified text metering refuses procurement held or spent history';
 END IF;
 IF EXISTS (SELECT 1 FROM periodic_budget_allocations)
    OR EXISTS (SELECT 1 FROM periodic_budget_events) THEN
  RAISE EXCEPTION 'unified text metering refuses periodic budget allocation history';
 END IF;
 IF EXISTS (SELECT 1 FROM payment_transaction_identities)
    OR EXISTS (SELECT 1 FROM payment_captures)
    OR EXISTS (SELECT 1 FROM payment_effects) THEN
  RAISE EXCEPTION 'unified text metering refuses payment capture history';
 END IF;
 IF to_regclass('ledger_accounts') IS NOT NULL AND EXISTS (SELECT 1 FROM ledger_accounts WHERE balance <> 0) THEN
  RAISE EXCEPTION 'unified text metering refuses a non-zero ledger balance';
 END IF;
END $$;

-- 024 owns the empty current schema. Manual reconciliation keeps the exact
-- vector bytes so JSONB key reordering cannot change an idempotency identity.
ALTER TABLE reconciliation_cases DROP COLUMN proposed_usage;
ALTER TABLE reconciliation_cases
 ADD COLUMN proposed_observation_canonical BYTEA,
 ADD COLUMN proposed_observation_digest BYTEA,
 ADD CHECK ((proposed_observation_canonical IS NULL AND proposed_observation_digest IS NULL)
        OR (octet_length(proposed_observation_canonical) BETWEEN 1 AND 8192
            AND octet_length(proposed_observation_digest)=32));

CREATE FUNCTION meter_frames(parts bytea[]) RETURNS bytea LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE part bytea; out bytea := ''::bytea;
BEGIN
 FOREACH part IN ARRAY parts LOOP
  out := out || int4send(octet_length(part)) || part;
 END LOOP;
 RETURN out;
END $$;

CREATE FUNCTION meter_gcd(a bigint, b bigint) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
 WITH RECURSIVE g(x,y) AS (
  SELECT abs(a), abs(b)
  UNION ALL SELECT y, x % y FROM g WHERE y <> 0
 ) SELECT x FROM g WHERE y = 0
$$;

CREATE TABLE meter_schemas (
 id TEXT PRIMARY KEY COLLATE "C",
 name TEXT NOT NULL COLLATE "C" CHECK(name ~ '^[a-z][a-z0-9_]{0,99}$'),
 revision BIGINT NOT NULL CHECK(revision > 0),
 registry_version SMALLINT NOT NULL CHECK(registry_version = 1),
 wire_version SMALLINT NOT NULL CHECK(wire_version = 1),
 canonical BYTEA NOT NULL CHECK(octet_length(canonical) BETWEEN 1 AND 16384),
 schema_digest BYTEA NOT NULL CHECK(octet_length(schema_digest) = 32),
 created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
 created_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(name, revision),
 UNIQUE(id, schema_digest)
);
CREATE TABLE meter_schema_dimensions (
 schema_id TEXT NOT NULL COLLATE "C",
 dimension TEXT NOT NULL COLLATE "C",
 unit TEXT NOT NULL COLLATE "C",
 maximum_quantity BIGINT NOT NULL CHECK(maximum_quantity >= 0),
 retail BOOLEAN NOT NULL,
 supplier BOOLEAN NOT NULL,
 PRIMARY KEY(schema_id, dimension),
 UNIQUE(schema_id, dimension, unit),
 FOREIGN KEY(schema_id) REFERENCES meter_schemas(id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(retail OR supplier),
 CHECK(dimension IN ('text_input_token','text_output_token') AND unit = 'token')
);
CREATE TRIGGER immutable_meter_schema BEFORE UPDATE OR DELETE ON meter_schemas FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_meter_schema_dimension BEFORE UPDATE OR DELETE ON meter_schema_dimensions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION meter_schema_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.created_xid := pg_current_xact_id();
 NEW.created_at := clock_timestamp();
 RETURN NEW;
END $$;
CREATE TRIGGER meter_schema_stamp BEFORE INSERT ON meter_schemas FOR EACH ROW EXECUTE FUNCTION meter_schema_stamp();

CREATE FUNCTION meter_schema_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n int; canonical text; digest bytea; stored bytea; stored_digest bytea; sid text;
BEGIN
 IF TG_TABLE_NAME = 'meter_schemas' THEN sid := NEW.id; ELSE sid := NEW.schema_id; END IF;
 SELECT count(*), bool_and(retail), bool_and(supplier) INTO n FROM meter_schema_dimensions WHERE schema_id = sid;
 IF n <> 2 THEN RAISE EXCEPTION 'text schema requires input and output dimensions'; END IF;
 SELECT string_agg(format('{"dimension":"%s","unit":"%s","maximum_quantity":"%s"}', dimension, unit, maximum_quantity), ',' ORDER BY dimension)
  INTO canonical FROM meter_schema_dimensions WHERE schema_id = sid;
 canonical := format('{"schema_id":"%s","version":"1","dimensions":[%s],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}', sid, canonical);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.schema.v1','UTF8'), convert_to(canonical,'UTF8')]));
 SELECT s.canonical, s.schema_digest INTO stored, stored_digest FROM meter_schemas s WHERE s.id = sid;
 IF stored IS DISTINCT FROM convert_to(canonical,'UTF8') OR stored_digest IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'schema canonical digest mismatch';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_schema_complete AFTER INSERT ON meter_schemas DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_schema_complete();
CREATE CONSTRAINT TRIGGER meter_schema_dimension_complete AFTER INSERT ON meter_schema_dimensions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_schema_complete();

CREATE FUNCTION meter_text_schema(input_max bigint, output_max bigint) RETURNS text LANGUAGE plpgsql AS $$
DECLARE sid text; canonical text; digest bytea;
BEGIN
 IF input_max <= 0 OR output_max < 0 THEN RAISE EXCEPTION 'invalid text schema bounds'; END IF;
 SELECT s.id INTO sid FROM meter_schemas s
  WHERE EXISTS (SELECT 1 FROM meter_schema_dimensions d WHERE d.schema_id=s.id AND d.dimension='text_input_token' AND d.maximum_quantity=input_max AND d.unit='token' AND d.retail AND d.supplier)
    AND EXISTS (SELECT 1 FROM meter_schema_dimensions d WHERE d.schema_id=s.id AND d.dimension='text_output_token' AND d.maximum_quantity=output_max AND d.unit='token' AND d.retail AND d.supplier);
 IF sid IS NOT NULL THEN RETURN sid; END IF;
 sid := gen_random_uuid()::text;
 canonical := format('{"schema_id":"%s","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"%s"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"%s"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}', sid, input_max, output_max);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.schema.v1','UTF8'), convert_to(canonical,'UTF8')]));
 INSERT INTO meter_schemas(id,name,revision,registry_version,wire_version,canonical,schema_digest)
  VALUES(sid, 'text_' || substr(sid, 1, 8), 1, 1, 1, convert_to(canonical,'UTF8'), digest);
 INSERT INTO meter_schema_dimensions(schema_id,dimension,unit,maximum_quantity,retail,supplier) VALUES
  (sid,'text_input_token','token',input_max,true,true),
  (sid,'text_output_token','token',output_max,true,true);
 RETURN sid;
END $$;

ALTER TABLE model_capability_versions
 ADD COLUMN schema_id TEXT COLLATE "C",
 ADD COLUMN schema_digest BYTEA,
 ADD COLUMN protocol TEXT COLLATE "C",
 ADD COLUMN execution_kind TEXT COLLATE "C" DEFAULT 'synchronous',
 ADD COLUMN contract_digest BYTEA,
 ADD COLUMN profile_digest BYTEA,
 ADD COLUMN created_xid XID8 DEFAULT pg_current_xact_id();
ALTER TABLE model_capability_versions
 ALTER COLUMN schema_id SET NOT NULL,
 ALTER COLUMN schema_digest SET NOT NULL,
 ALTER COLUMN protocol SET NOT NULL,
 ALTER COLUMN execution_kind SET NOT NULL,
 ALTER COLUMN contract_digest SET NOT NULL,
 ALTER COLUMN profile_digest SET NOT NULL,
 ALTER COLUMN created_xid SET NOT NULL;
ALTER TABLE model_capability_versions
 ADD CONSTRAINT model_capability_text CHECK(protocol IN ('openai_chat','openai_responses','anthropic_messages','openai_embeddings','jina_rerank','gemini_generate_content') AND execution_kind='synchronous' AND octet_length(schema_digest)=32 AND octet_length(contract_digest)=32 AND octet_length(profile_digest)=32),
 ADD CONSTRAINT model_capability_schema_fk FOREIGN KEY(schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED,
 ADD UNIQUE(id, model_id),
 ADD UNIQUE(id, schema_id, schema_digest);

CREATE TABLE model_capability_variants (
 profile_version TEXT NOT NULL COLLATE "C",
 variant_key TEXT NOT NULL COLLATE "C" CHECK(variant_key = 'default'),
 parameters JSONB NOT NULL CHECK(jsonb_typeof(parameters)='object' AND octet_length(parameters::text) <= 16384),
 PRIMARY KEY(profile_version, variant_key),
 FOREIGN KEY(profile_version) REFERENCES model_capability_versions(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE model_capability_bound_lines (
 profile_version TEXT NOT NULL COLLATE "C",
 variant_key TEXT NOT NULL COLLATE "C",
 side TEXT NOT NULL COLLATE "C" CHECK(side IN ('retail','supplier')),
 dimension TEXT NOT NULL COLLATE "C",
 unit TEXT NOT NULL COLLATE "C",
 quantity BIGINT NOT NULL CHECK(quantity >= 0),
 PRIMARY KEY(profile_version, variant_key, side, dimension),
 FOREIGN KEY(profile_version, variant_key) REFERENCES model_capability_variants(profile_version, variant_key) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER immutable_capability_variant BEFORE UPDATE OR DELETE ON model_capability_variants FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_capability_bound BEFORE UPDATE OR DELETE ON model_capability_bound_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

-- A capability is published only when its immutable profile, schema and both
-- side masks have been born as one complete current-domain fact.  The helper
-- entry point below is convenient, but raw child/root INSERTs must be held to
-- the same contract at COMMIT as well.
CREATE FUNCTION meter_capability_complete() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE pid text; sid text; protocol text; profile_model text; profile_version text;
 input_max bigint; output_max bigint; schema_count bigint; bound_count bigint;
 matched_bounds bigint; variant_count bigint; schema_input bigint; schema_output bigint;
 profile_hash bytea;
BEGIN
 pid := coalesce(to_jsonb(NEW)->>'id',to_jsonb(NEW)->>'profile_version');
 SELECT c.schema_id,c.protocol,c.profile->>'model',c.profile->>'version',
        (c.profile->>'input_token_upper_bound')::bigint,
        (c.profile->>'output_limit')::bigint
   INTO sid,protocol,profile_model,profile_version,input_max,output_max
   FROM model_capability_versions c
  WHERE c.id=pid;
 IF NOT FOUND THEN RAISE EXCEPTION 'capability root is missing'; END IF;
 IF pid !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    OR pid='00000000-0000-0000-0000-000000000000'
    OR profile_version IS DISTINCT FROM pid THEN
  RAISE EXCEPTION 'capability profile identity is invalid';
 END IF;
 SELECT m.public_name INTO STRICT profile_model FROM models m
  WHERE m.id=(SELECT model_id FROM model_capability_versions WHERE id=pid);
 IF (SELECT profile->>'model' FROM model_capability_versions WHERE id=pid) IS DISTINCT FROM profile_model THEN
  RAISE EXCEPTION 'capability profile model is invalid';
 END IF;
 profile_hash := sha256(convert_to((SELECT profile::text FROM model_capability_versions WHERE id=pid),'UTF8'));
 IF (SELECT c.protocol FROM model_capability_versions c WHERE c.id=pid) IS DISTINCT FROM
      (SELECT c.profile->>'protocol' FROM model_capability_versions c WHERE c.id=pid)
    OR (SELECT c.contract_digest FROM model_capability_versions c WHERE c.id=pid) IS DISTINCT FROM profile_hash
    OR (SELECT c.profile_digest FROM model_capability_versions c WHERE c.id=pid) IS DISTINCT FROM profile_hash THEN
  RAISE EXCEPTION 'capability profile digest is invalid';
 END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') AND output_max<>0 THEN
  RAISE EXCEPTION 'input-only capability requires a zero output bound';
 END IF;
 SELECT count(*) INTO variant_count FROM model_capability_variants v
  WHERE v.profile_version=pid AND v.variant_key='default';
 SELECT count(*), count(*) FILTER (WHERE unit='token' AND
   ((side IN ('retail','supplier') AND dimension='text_input_token' AND quantity=input_max)
    OR (side IN ('retail','supplier') AND dimension='text_output_token' AND quantity=output_max)))
   INTO bound_count,matched_bounds
   FROM model_capability_bound_lines b WHERE b.profile_version=pid;
 IF variant_count<>1 OR bound_count<>4 OR matched_bounds<>4 THEN
  RAISE EXCEPTION 'capability bounds are incomplete';
 END IF;
 SELECT count(*),
        max(maximum_quantity) FILTER (WHERE dimension='text_input_token'),
        max(maximum_quantity) FILTER (WHERE dimension='text_output_token')
   INTO schema_count,schema_input,schema_output
   FROM meter_schema_dimensions d
  WHERE d.schema_id=sid AND d.unit='token' AND d.retail AND d.supplier
    AND d.dimension IN ('text_input_token','text_output_token');
 IF schema_count<>2 OR input_max IS NULL OR output_max IS NULL
    OR input_max<=0 OR output_max<0
    OR input_max>schema_input OR output_max>schema_output THEN
  RAISE EXCEPTION 'capability bounds exceed schema';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_capability_root_complete
 AFTER INSERT ON model_capability_versions DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_capability_complete();
CREATE CONSTRAINT TRIGGER meter_capability_variant_complete
 AFTER INSERT ON model_capability_variants DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_capability_complete();
CREATE CONSTRAINT TRIGGER meter_capability_bound_complete
 AFTER INSERT ON model_capability_bound_lines DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_capability_complete();
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_capability_complete() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_capability_complete() FROM PUBLIC;

ALTER TABLE models DROP COLUMN profile;
ALTER TABLE models ALTER COLUMN profile_version DROP DEFAULT;
ALTER TABLE models ADD CONSTRAINT models_profile_fk FOREIGN KEY(profile_version, id) REFERENCES model_capability_versions(id, model_id) DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION meter_insert_text_model(model_id text, public_name text, profile jsonb, profile_version text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE protocol text; input_max bigint; output_max bigint; sid text; digest bytea; canonical bytea; contract bytea;
BEGIN
 protocol := coalesce(profile->>'protocol','openai_chat');
 profile := jsonb_set(profile, '{protocol}', to_jsonb(protocol), true);
 input_max := (profile->>'input_token_upper_bound')::bigint;
 output_max := (profile->>'output_limit')::bigint;
 IF profile->>'version' IS DISTINCT FROM profile_version OR profile->>'model' IS DISTINCT FROM public_name OR input_max IS NULL OR output_max IS NULL THEN
  RAISE EXCEPTION 'text model profile identity mismatch';
 END IF;
 IF profile_version !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR profile_version = '00000000-0000-0000-0000-000000000000' THEN
  RAISE EXCEPTION 'profile version must be a canonical uuid';
 END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') AND output_max <> 0 THEN
  RAISE EXCEPTION 'input-only model requires a zero output bound';
 END IF;
 sid := meter_text_schema(input_max, output_max);
 SELECT schema_digest INTO digest FROM meter_schemas WHERE id = sid;
 contract := sha256(convert_to(profile::text,'UTF8'));
 canonical := convert_to(profile::text,'UTF8');
 INSERT INTO models(id,public_name,profile_version) VALUES(model_id, public_name, profile_version);
 INSERT INTO model_capability_versions(id,model_id,profile,schema_id,schema_digest,protocol,execution_kind,contract_digest,profile_digest)
  VALUES(profile_version, model_id, profile, sid, digest, protocol, 'synchronous', contract, sha256(canonical));
 INSERT INTO model_capability_variants(profile_version,variant_key,parameters) VALUES(profile_version,'default','{}'::jsonb);
 INSERT INTO model_capability_bound_lines(profile_version,variant_key,side,dimension,unit,quantity) VALUES
  (profile_version,'default','retail','text_input_token','token',input_max),
  (profile_version,'default','retail','text_output_token','token',output_max),
  (profile_version,'default','supplier','text_input_token','token',input_max),
  (profile_version,'default','supplier','text_output_token','token',output_max);
END $$;

CREATE FUNCTION meter_model_profile(model_id text) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT c.profile FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=meter_model_profile.model_id
$$;

ALTER TABLE route_policy_versions
 ADD COLUMN profile_version TEXT COLLATE "C" NOT NULL,
 ADD COLUMN created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
 ADD CONSTRAINT route_profile_model_fk FOREIGN KEY(profile_version,model_id) REFERENCES model_capability_versions(id,model_id) DEFERRABLE INITIALLY DEFERRED;
CREATE FUNCTION meter_route_profile() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE profile text;
BEGIN
 SELECT m.profile_version INTO STRICT profile FROM models m WHERE m.id=NEW.model_id FOR SHARE;
 IF NEW.profile_version IS NOT NULL AND NEW.profile_version IS DISTINCT FROM profile THEN
  RAISE EXCEPTION 'route profile does not match model';
 END IF;
 NEW.profile_version := profile;
 NEW.created_xid := pg_current_xact_id();
 NEW.created_at := clock_timestamp();
 RETURN NEW;
END $$;
CREATE TRIGGER meter_route_profile BEFORE INSERT ON route_policy_versions FOR EACH ROW EXECUTE FUNCTION meter_route_profile();

-- Route children are born with the route publication.  A later transaction
-- may publish another route, but it may not append a binding or a cost cap to
-- an already committed immutable route/binding root.
CREATE FUNCTION meter_binding_birth() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE route_stamp xid8; route_profile text;
BEGIN
 SELECT p.created_xid,p.profile_version INTO route_stamp,route_profile
   FROM route_policy_versions p WHERE p.id=NEW.route_policy_version_id;
 IF NOT FOUND OR route_stamp IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'model binding requires same-transaction route';
 END IF;
 IF NEW.variant_key IS DISTINCT FROM 'default' THEN
  RAISE EXCEPTION 'model binding variant is invalid';
 END IF;
 NEW.created_xid := pg_current_xact_id();
 RETURN NEW;
END $$;
CREATE TRIGGER meter_binding_birth BEFORE INSERT ON model_bindings FOR EACH ROW EXECUTE FUNCTION meter_binding_birth();

CREATE FUNCTION meter_binding_cap_birth() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE binding_stamp xid8; sid text;
BEGIN
 SELECT b.created_xid,c.schema_id INTO binding_stamp,sid
   FROM model_bindings b
   JOIN route_policy_versions p ON p.id=b.route_policy_version_id
   JOIN model_capability_versions c ON c.id=p.profile_version AND c.model_id=p.model_id
  WHERE b.id=NEW.binding_id;
 IF NOT FOUND OR binding_stamp IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'binding cost cap requires same-transaction binding';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM meter_schema_dimensions d
   WHERE d.schema_id=sid AND d.supplier AND d.dimension=NEW.dimension AND d.unit=NEW.unit
 ) OR meter_gcd(NEW.numerator,NEW.denominator)<>1 THEN
  RAISE EXCEPTION 'binding cost cap is not in the supplier schema';
 END IF;
 RETURN NEW;
END $$;
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_binding_birth() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.meter_binding_cap_birth() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_binding_birth(),meter_binding_cap_birth() FROM PUBLIC;

CREATE FUNCTION meter_model_route_matches() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM models m WHERE m.id=NEW.id AND m.active_revision<>0 AND NOT EXISTS (
  SELECT 1 FROM route_policy_versions p WHERE p.model_id=m.id AND p.revision=m.active_revision AND p.profile_version=m.profile_version)) THEN
  RAISE EXCEPTION 'active route does not match model profile';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_model_route_matches AFTER INSERT OR UPDATE ON models DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_model_route_matches();
REVOKE ALL ON FUNCTION meter_route_profile(), meter_model_route_matches() FROM PUBLIC;

ALTER TABLE price_versions
 ADD COLUMN schema_id TEXT COLLATE "C",
 ADD COLUMN schema_digest BYTEA,
 ADD COLUMN variant_key TEXT COLLATE "C" DEFAULT 'default',
 ADD COLUMN rates_canonical BYTEA,
 ADD COLUMN rates_digest BYTEA,
 ADD COLUMN created_xid XID8 DEFAULT pg_current_xact_id();
ALTER TABLE price_versions
 ALTER COLUMN schema_id SET NOT NULL,
 ALTER COLUMN schema_digest SET NOT NULL,
 ALTER COLUMN variant_key SET NOT NULL,
 ALTER COLUMN rates_canonical SET NOT NULL,
 ALTER COLUMN rates_digest SET NOT NULL,
 ALTER COLUMN created_xid SET NOT NULL,
 ADD CHECK(variant_key='default' AND octet_length(schema_digest)=32 AND octet_length(rates_digest)=32 AND octet_length(rates_canonical) BETWEEN 1 AND 8192);
ALTER TABLE price_versions
 DROP CONSTRAINT price_versions_domain_effective_key,
 ADD CONSTRAINT price_versions_domain_effective_key UNIQUE NULLS NOT DISTINCT(model_id, price_group_id, schema_id, variant_key, effective_at),
 ADD CONSTRAINT price_versions_schema_fk FOREIGN KEY(schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;
DROP INDEX price_versions_domain_lookup;
CREATE INDEX price_versions_domain_lookup ON price_versions(model_id, price_group_id, schema_id, variant_key, effective_at DESC, created_at DESC, id COLLATE "C" DESC);

CREATE TABLE price_rate_lines (
 price_version_id TEXT NOT NULL COLLATE "C",
 dimension TEXT NOT NULL COLLATE "C",
 unit TEXT NOT NULL COLLATE "C",
 numerator BIGINT NOT NULL CHECK(numerator >= 0),
 denominator BIGINT NOT NULL CHECK(denominator BETWEEN 1 AND 1000000),
 PRIMARY KEY(price_version_id, dimension),
 FOREIGN KEY(price_version_id) REFERENCES price_versions(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER immutable_price_rate BEFORE UPDATE OR DELETE ON price_rate_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION meter_rate_canonical(schema_id text, side text, input_num bigint, input_den bigint, output_num bigint, output_den bigint) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT convert_to(format('{"schema_id":"%s","version":"1","side":"%s","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"%s","denominator":"%s"},{"dimension":"text_output_token","unit":"token","numerator":"%s","denominator":"%s"}]}', schema_id, side, input_num, input_den, output_num, output_den), 'UTF8')
$$;

CREATE FUNCTION meter_price_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pid text; canonical bytea; digest bytea; schema_digest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; sid text;
BEGIN
 IF TG_TABLE_NAME = 'price_versions' THEN pid := NEW.id; ELSE pid := NEW.price_version_id; END IF;
 SELECT l.numerator, l.denominator INTO in_n, in_d FROM price_rate_lines l WHERE l.price_version_id=pid AND l.dimension='text_input_token' AND l.unit='token';
 SELECT l.numerator, l.denominator INTO out_n, out_d FROM price_rate_lines l WHERE l.price_version_id=pid AND l.dimension='text_output_token' AND l.unit='token';
 IF in_n IS NULL OR out_n IS NULL OR (SELECT count(*) FROM price_rate_lines WHERE price_version_id=pid) <> 2 THEN
  RAISE EXCEPTION 'retail price requires both token rates';
 END IF;
 IF meter_gcd(in_n, in_d) <> 1 OR meter_gcd(out_n, out_d) <> 1 THEN RAISE EXCEPTION 'retail rate is not reduced'; END IF;
 SELECT p.schema_id, s.schema_digest, p.rates_canonical, p.rates_digest INTO sid, schema_digest, canonical, digest
  FROM price_versions p JOIN meter_schemas s ON s.id=p.schema_id WHERE p.id=pid;
 canonical := meter_rate_canonical(sid, 'retail', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), schema_digest, convert_to('retail','UTF8'), canonical]));
 IF (SELECT rates_canonical FROM price_versions WHERE id=pid) IS DISTINCT FROM canonical
    OR (SELECT rates_digest FROM price_versions WHERE id=pid) IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'retail rate canonical mismatch';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_price_root_complete AFTER INSERT ON price_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_price_complete();
CREATE CONSTRAINT TRIGGER meter_price_line_complete AFTER INSERT ON price_rate_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_price_complete();

CREATE FUNCTION meter_reduce_million(rate bigint, OUT numerator bigint, OUT denominator bigint) LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE g bigint;
BEGIN
 IF rate < 0 THEN RAISE EXCEPTION 'negative token rate'; END IF;
 g := meter_gcd(rate, 1000000);
 numerator := rate / g;
 denominator := 1000000 / g;
END $$;

CREATE FUNCTION meter_insert_token_price(price_id text, model_id text, input_per_million bigint, output_per_million bigint, effective_at timestamptz, created_by text, price_group_id text, pricing_operation_id text, created_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea; protocol text; cid text;
BEGIN
 SELECT c.schema_id, c.schema_digest, c.protocol INTO sid, sdigest, protocol
  FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=meter_insert_token_price.model_id;
 IF sid IS NULL THEN RAISE EXCEPTION 'model has no text schema'; END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') AND output_per_million <> 0 THEN
  RAISE EXCEPTION 'input-only price requires zero output rate';
 END IF;
 SELECT r.numerator, r.denominator INTO in_n, in_d FROM meter_reduce_million(input_per_million) r;
 SELECT r.numerator, r.denominator INTO out_n, out_d FROM meter_reduce_million(output_per_million) r;
 canonical := meter_rate_canonical(sid, 'retail', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('retail','UTF8'), canonical]));
 cid := coalesce(price_id, gen_random_uuid()::text);
 INSERT INTO price_versions(id,model_id,currency,effective_at,created_by,created_at,price_group_id,pricing_operation_id,schema_id,schema_digest,variant_key,rates_canonical,rates_digest)
  VALUES(cid, model_id, 'CNY', coalesce(effective_at, clock_timestamp()), created_by, coalesce(created_at, clock_timestamp()), price_group_id, pricing_operation_id, sid, sdigest, 'default', canonical, digest);
 INSERT INTO price_rate_lines(price_version_id,dimension,unit,numerator,denominator) VALUES
  (cid,'text_input_token','token',in_n,in_d),
  (cid,'text_output_token','token',out_n,out_d);
END $$;

-- Normalize object keys only. Array ordering, closed shape and reduced integer
-- rates are independently checked before framing; JSONB::text is not canonical.
CREATE FUNCTION meter_text_rates_canonical(r jsonb, expected_side text) RETURNS bytea LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE line jsonb; i integer; nums bigint[] := '{}'; dens bigint[] := '{}'; name text;
BEGIN
 IF expected_side IS NULL OR expected_side NOT IN ('retail','supplier') OR jsonb_typeof(r) IS DISTINCT FROM 'object'
  OR NOT pricing_json_uuid(r->'schema_id') OR r->'version' IS DISTINCT FROM '"1"'::jsonb
  OR r->'side' IS DISTINCT FROM to_jsonb(expected_side) OR r->'currency' IS DISTINCT FROM '"CNY"'::jsonb
  OR jsonb_typeof(r->'rates') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid text rates'; END IF;
 IF jsonb_array_length(r->'rates')<>2 OR r IS DISTINCT FROM jsonb_build_object('schema_id',r->'schema_id','version','1','side',expected_side,'currency','CNY','rates',r->'rates') THEN RAISE EXCEPTION 'invalid text rates shape'; END IF;
 FOR i IN 0..1 LOOP
  line := r->'rates'->i;
  name := CASE i WHEN 0 THEN 'text_input_token' ELSE 'text_output_token' END;
  IF jsonb_typeof(line) IS DISTINCT FROM 'object' OR line->'dimension' IS DISTINCT FROM to_jsonb(name)
   OR line->'unit' IS DISTINCT FROM '"token"'::jsonb OR NOT pricing_json_uint(line->'numerator',false)
   OR NOT pricing_json_uint(line->'denominator',true)
   OR line IS DISTINCT FROM jsonb_build_object('dimension',name,'unit','token','numerator',line->'numerator','denominator',line->'denominator') THEN RAISE EXCEPTION 'invalid text rate line'; END IF;
  nums := array_append(nums,(line->>'numerator')::bigint);
  dens := array_append(dens,(line->>'denominator')::bigint);
  IF dens[i+1]>1000000 OR meter_gcd(nums[i+1],dens[i+1])<>1 THEN RAISE EXCEPTION 'text rate not reduced'; END IF;
 END LOOP;
 RETURN meter_rate_canonical(r->>'schema_id',expected_side,nums[1],dens[1],nums[2],dens[2]);
END $$;

CREATE OR REPLACE FUNCTION assert_pricing_safe_input(k text,j jsonb) RETURNS void LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE shape jsonb; label text;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'pricing safe input must be object'; END IF;
 CASE k
 WHEN 'price_group.create' THEN shape:=jsonb_build_object('label',j->'label');
 WHEN 'price_group.update' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','label',j->'label','state',j->'state');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR jsonb_typeof(j->'state') IS DISTINCT FROM 'string' OR (j->>'state') NOT IN ('active','retired') THEN RAISE EXCEPTION 'invalid group update input'; END IF;
 WHEN 'group_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id')
   OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$'
   OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb
   OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid group price input'; END IF;
  PERFORM meter_text_rates_canonical(j->'rates','retail');
 WHEN 'default_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid default price input'; END IF;
  PERFORM meter_text_rates_canonical(j->'rates','retail');
 WHEN 'cost_price.publish' THEN
  shape:=jsonb_build_object('channel_id',j->'channel_id','upstream_model',j->'upstream_model','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','basis',j->'basis','rates',j->'rates','effective_at',j->'effective_at','expected_channel_revision',j->'expected_channel_revision');
  IF NOT pricing_json_uuid(j->'channel_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'upstream_model') IS DISTINCT FROM 'string' OR octet_length(j->>'upstream_model') NOT BETWEEN 1 AND 200 OR (j->>'upstream_model')<>btrim(j->>'upstream_model') OR (j->>'upstream_model')~'[[:cntrl:]]' OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb OR (j->>'basis') NOT IN ('contract','invoice','unknown') OR NOT pricing_json_time(j->'effective_at') OR NOT pricing_json_uint(j->'expected_channel_revision',true) THEN RAISE EXCEPTION 'invalid cost price input'; END IF;
  IF (j->>'basis')='unknown' THEN IF j->'rates' IS DISTINCT FROM 'null'::jsonb THEN RAISE EXCEPTION 'unknown cost has rates'; END IF; ELSE IF j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid cost price input'; END IF; PERFORM meter_text_rates_canonical(j->'rates','supplier'); END IF;
 WHEN 'tenant_pricing.assign' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','price_group_id',j->'price_group_id');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR NOT (j ? 'price_group_id') OR (jsonb_typeof(j->'price_group_id') IS DISTINCT FROM 'null' AND NOT pricing_json_uuid(j->'price_group_id')) THEN RAISE EXCEPTION 'invalid tenant pricing input'; END IF;
 ELSE RAISE EXCEPTION 'unknown pricing operation kind'; END CASE;
 IF j IS DISTINCT FROM shape THEN RAISE EXCEPTION 'pricing input keys mismatch'; END IF;
 IF k IN ('price_group.create','price_group.update') THEN
  IF jsonb_typeof(j->'label') IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'pricing label must be string'; END IF;
  label:=j->>'label';
  IF octet_length(label) NOT BETWEEN 1 AND 100 OR label<>btrim(label) OR label ~ '[[:cntrl:]]' THEN RAISE EXCEPTION 'invalid pricing label'; END IF;
 END IF;
END $$;

CREATE OR REPLACE FUNCTION pricing_fingerprint(k text,a text,s text,t text,j jsonb) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(pricing_frame(ARRAY[k,a,s,t] || CASE k
  WHEN 'price_group.create' THEN ARRAY[j->>'label']
  WHEN 'price_group.update' THEN ARRAY[j->>'expected_revision',j->>'label',j->>'state']
  WHEN 'group_price.publish' THEN ARRAY[j->>'model_id',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',convert_from(meter_text_rates_canonical(j->'rates','retail'),'UTF8'),j->>'effective_at']
  WHEN 'default_price.publish' THEN ARRAY[j->>'model_id',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',convert_from(meter_text_rates_canonical(j->'rates','retail'),'UTF8'),j->>'effective_at']
  WHEN 'cost_price.publish' THEN ARRAY[j->>'channel_id',j->>'upstream_model',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',j->>'basis',CASE WHEN j->'rates'='null'::jsonb THEN NULL ELSE convert_from(meter_text_rates_canonical(j->'rates','supplier'),'UTF8') END,j->>'effective_at',j->>'expected_channel_revision']
  WHEN 'tenant_pricing.assign' THEN ARRAY[j->>'expected_revision',j->>'price_group_id'] ELSE ARRAY[]::text[] END)),'hex')
$$;

CREATE OR REPLACE FUNCTION guard_group_price_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gstate TEXT; op pricing_operations%ROWTYPE; expected_kind text;
BEGIN
 PERFORM id FROM models WHERE id=NEW.model_id FOR SHARE;
 expected_kind:=CASE WHEN NEW.price_group_id IS NULL THEN 'default_price.publish' ELSE 'group_price.publish' END;
 IF NOT EXISTS (SELECT 1 FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN model_capability_variants v ON v.profile_version=c.id AND v.variant_key=NEW.variant_key WHERE m.id=NEW.model_id AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest) THEN RAISE EXCEPTION 'price schema mismatch'; END IF;
 IF NEW.price_group_id IS NOT NULL THEN
  IF NOT EXISTS (SELECT 1 FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN model_capability_variants v ON v.profile_version=c.id AND v.variant_key=NEW.variant_key WHERE m.id=NEW.model_id AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest) THEN RAISE EXCEPTION 'group price schema mismatch'; END IF;
  SELECT state INTO STRICT gstate FROM price_groups WHERE id=NEW.price_group_id FOR SHARE;
  IF gstate<>'active' THEN RAISE EXCEPTION 'price group retired'; END IF;
 END IF;
 SELECT * INTO op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 IF NOT FOUND OR op.created_txid<>txid_current() OR op.kind<>expected_kind OR op.resource_id<>NEW.id OR op.actor<>NEW.created_by THEN RAISE EXCEPTION 'price operation mismatch'; END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION pricing_price_result(p price_versions) RETURNS JSONB LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('id',p.id,'model_id',p.model_id,'price_group_id',p.price_group_id,
  'currency',p.currency,'schema_id',p.schema_id,'schema_digest',encode(p.schema_digest,'hex'),
  'variant_key',p.variant_key,'rates',convert_from(p.rates_canonical,'UTF8')::jsonb,
  'effective_at',pricing_time(p.effective_at),'created_at',pricing_time(p.created_at))
$$;

CREATE OR REPLACE FUNCTION require_pricing_operation_result() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g price_groups%ROWTYPE; t tenants%ROWTYPE; p price_versions%ROWTYPE; c cost_versions%ROWTYPE;
 e tenant_pricing_assignments%ROWTYPE; expected JSONB; prior BIGINT; has_event BOOLEAN; current_revision bigint;
BEGIN
 IF NEW.created_txid<>txid_current() THEN RAISE EXCEPTION 'pricing operation not current transaction'; END IF;
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input);
 IF NEW.kind='price_group.create' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  expected:=jsonb_build_object('label',g.label);
  IF NEW.target<>'/api/platform/price-groups' OR g.last_operation_id<>NEW.operation_id
     OR g.revision<>1 OR g.state<>'active' OR g.created_at<>NEW.created_at
     OR NEW.revision<>1 OR NEW.result_snapshot<>pricing_group_result(g) THEN
   RAISE EXCEPTION 'price group create result mismatch';
  END IF;
 ELSIF NEW.kind='price_group.update' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'label',g.label,'state',g.state);
  IF NEW.target<>('/api/platform/price-groups/'||g.id) OR NEW.revision<>g.revision
     OR NEW.result_snapshot<>pricing_group_result(g)
     OR NOT ((g.last_operation_id=NEW.operation_id AND prior<9223372036854775807 AND g.revision=prior+1)
       OR (g.last_operation_id<>NEW.operation_id AND g.revision=prior)) THEN
   RAISE EXCEPTION 'price group update result mismatch';
  END IF;
 ELSIF NEW.kind='group_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=pricing_price_result(p)-ARRAY['id','price_group_id','created_at'];
  IF p.price_group_id IS NULL OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor
     OR p.created_at<>NEW.created_at
     OR NEW.target<>('/api/platform/price-groups/'||p.price_group_id||'/price-versions')
     OR NEW.result_snapshot<>pricing_price_result(p) THEN
   RAISE EXCEPTION 'group price result mismatch';
  END IF;
  PERFORM meter_group_price_complete(p.id);
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSIF NEW.kind='default_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=pricing_price_result(p)-ARRAY['id','price_group_id','created_at'];
  IF p.price_group_id IS NOT NULL OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor OR p.created_at<>NEW.created_at OR NEW.target<>'/api/platform/price-versions' OR NEW.result_snapshot<>pricing_price_result(p) THEN RAISE EXCEPTION 'default price result mismatch'; END IF;
  PERFORM meter_group_price_complete(p.id);
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSIF NEW.kind='cost_price.publish' THEN
  SELECT * INTO STRICT c FROM cost_versions WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_channel_revision')::bigint;
  expected:=pricing_cost_result(c,prior)-ARRAY['id','created_at','channel_revision'];
  SELECT management_revision INTO STRICT current_revision FROM channels WHERE id=c.channel_id;
  IF c.pricing_operation_id<>NEW.operation_id OR c.created_by<>NEW.actor OR c.created_at<>NEW.created_at OR NEW.target<>'/api/platform/cost-versions' OR current_revision<>prior+1 OR NEW.result_snapshot<>pricing_cost_result(c,prior) THEN RAISE EXCEPTION 'cost price result mismatch'; END IF;
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSE
  SELECT * INTO STRICT t FROM tenants WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'price_group_id',t.price_group_id);
  SELECT * INTO e FROM tenant_pricing_assignments WHERE operation_id=NEW.operation_id;
  has_event:=FOUND;
  IF NEW.target<>('/api/platform/tenants/'||t.id||'/pricing') OR NEW.revision<>t.pricing_revision
     OR NEW.result_snapshot<>pricing_assignment_result(t) THEN
   RAISE EXCEPTION 'tenant pricing result mismatch';
  END IF;
  IF t.pricing_operation_id=NEW.operation_id THEN
   IF NOT has_event OR e.created_txid<>txid_current() OR e.tenant_id<>t.id OR e.actor<>NEW.actor
      OR e.after_group_id IS DISTINCT FROM t.price_group_id OR e.before_revision<>prior
      OR e.after_revision<>t.pricing_revision OR e.created_at<>NEW.created_at THEN
    RAISE EXCEPTION 'tenant pricing event mismatch';
   END IF;
  ELSIF has_event OR t.pricing_revision<>prior THEN
   RAISE EXCEPTION 'tenant pricing no-op mismatch';
  END IF;
 END IF;
 IF NEW.safe_input IS DISTINCT FROM expected OR NEW.input_fingerprint IS DISTINCT FROM
    pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,expected) THEN
  RAISE EXCEPTION 'pricing command input mismatch';
 END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION meter_group_price_complete(pid text) RETURNS void LANGUAGE plpgsql STABLE AS $$
DECLARE p price_versions%ROWTYPE; qi bigint; qo bigint; n1 bigint; d1 bigint; n2 bigint; d2 bigint; amount numeric;
BEGIN
 SELECT * INTO STRICT p FROM price_versions WHERE id=pid;
 IF p.rates_canonical IS DISTINCT FROM meter_text_rates_canonical(convert_from(p.rates_canonical,'UTF8')::jsonb,'retail') THEN RAISE EXCEPTION 'group price rates mismatch'; END IF;
 SELECT maximum_quantity INTO STRICT qi FROM meter_schema_dimensions WHERE schema_id=p.schema_id AND dimension='text_input_token' AND unit='token' AND retail;
 SELECT maximum_quantity INTO STRICT qo FROM meter_schema_dimensions WHERE schema_id=p.schema_id AND dimension='text_output_token' AND unit='token' AND retail;
 SELECT numerator,denominator INTO STRICT n1,d1 FROM price_rate_lines WHERE price_version_id=pid AND dimension='text_input_token';
 SELECT numerator,denominator INTO STRICT n2,d2 FROM price_rate_lines WHERE price_version_id=pid AND dimension='text_output_token';
 IF qo=0 AND n2<>0 THEN RAISE EXCEPTION 'input-only group price requires zero output'; END IF;
 amount:=meter_token_amount(qi,qo,n1,d1,n2,d2);
 IF amount IS NULL OR amount>9223372036854775807 THEN RAISE EXCEPTION 'group price maximum amount overflow'; END IF;
END $$;

CREATE FUNCTION meter_group_price_audit(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$
DECLARE op pricing_operations%ROWTYPE; n bigint; want_action text;
BEGIN
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=oid AND kind IN ('group_price.publish','default_price.publish','cost_price.publish');
 want_action:=CASE WHEN op.kind='cost_price.publish' THEN 'pricing.cost.publish' ELSE 'pricing.price.publish' END;
 SELECT count(*) INTO n FROM audit_events a WHERE a.sanitized_diff->>'operation_id'=oid;
 IF n<>1 OR NOT EXISTS (SELECT 1 FROM audit_events a WHERE a.sanitized_diff->>'operation_id'=oid
  AND op.created_txid=txid_current()
  AND a.actor=op.actor AND a.tenant_scope IS NULL AND a.action=want_action AND a.target=op.target
  AND a.sanitized_diff=jsonb_build_object('operation_id',oid,'target',op.target,'after',op.result_snapshot)) THEN RAISE EXCEPTION 'group price audit mismatch'; END IF;
END $$;

CREATE FUNCTION meter_group_price_audit_birth() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 -- Only pricing audits are inspected: roles such as the payment worker write
 -- ordinary audits but deliberately cannot read pricing_operations.
 IF NEW.action IN ('pricing.price.publish','pricing.cost.publish') THEN
  SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.sanitized_diff->>'operation_id' AND kind IN ('group_price.publish','default_price.publish','cost_price.publish');
  IF op.created_txid<>txid_current() OR NEW.action<>(CASE WHEN op.kind='cost_price.publish' THEN 'pricing.cost.publish' ELSE 'pricing.price.publish' END) THEN RAISE EXCEPTION 'publish audit mismatch'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER meter_group_price_audit_birth BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION meter_group_price_audit_birth();

CREATE FUNCTION meter_group_price_audit_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.action IN ('pricing.price.publish','pricing.cost.publish') THEN
  PERFORM meter_group_price_audit(NEW.sanitized_diff->>'operation_id');
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_group_price_audit_insert AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_group_price_audit_insert();
DO $$ DECLARE fn text; BEGIN
 FOREACH fn IN ARRAY ARRAY['meter_text_rates_canonical(jsonb,text)','meter_group_price_complete(text)','meter_group_price_audit(text)','meter_group_price_audit_birth()','meter_group_price_audit_insert()','assert_pricing_safe_input(text,jsonb)','pricing_fingerprint(text,text,text,text,jsonb)','guard_group_price_insert()','pricing_price_result(price_versions)','require_pricing_operation_result()'] LOOP
  EXECUTE format('ALTER FUNCTION %I.%s SET search_path TO %I,pg_catalog,pg_temp',current_schema(),fn,current_schema());
 END LOOP;
END $$;
REVOKE ALL ON FUNCTION meter_text_rates_canonical(jsonb,text),meter_group_price_complete(text),meter_group_price_audit(text),meter_group_price_audit_birth(),meter_group_price_audit_insert() FROM PUBLIC;

ALTER TABLE price_versions DROP COLUMN input_rate, DROP COLUMN output_rate;
ALTER TABLE price_versions DROP CONSTRAINT IF EXISTS price_versions_group_operation;
ALTER TABLE price_versions ALTER COLUMN pricing_operation_id SET NOT NULL;
ALTER TABLE pricing_operations DROP CONSTRAINT IF EXISTS pricing_operations_kind_check;
ALTER TABLE pricing_operations DROP CONSTRAINT IF EXISTS pricing_operations_revision_check;
ALTER TABLE pricing_operations DROP CONSTRAINT IF EXISTS pricing_operations_check1;
ALTER TABLE pricing_operations DROP CONSTRAINT IF EXISTS pricing_operations_check2;
ALTER TABLE pricing_operations ADD CONSTRAINT pricing_operations_kind_check CHECK(kind IN ('price_group.create','price_group.update','group_price.publish','tenant_pricing.assign','default_price.publish','cost_price.publish'));
ALTER TABLE pricing_operations ADD CONSTRAINT pricing_operations_revision_check CHECK((kind IN ('group_price.publish','default_price.publish','cost_price.publish') AND revision IS NULL) OR (kind NOT IN ('group_price.publish','default_price.publish','cost_price.publish') AND revision IS NOT NULL));
CREATE OR REPLACE FUNCTION guard_pricing_operation_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input); NEW.created_txid:=txid_current(); NEW.created_at:=clock_timestamp(); NEW.input_fingerprint:=pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,NEW.safe_input);
 IF NEW.kind IN ('price_group.create','group_price.publish','default_price.publish','cost_price.publish') THEN NEW.result_snapshot:=jsonb_set(NEW.result_snapshot,'{created_at}',to_jsonb(pricing_time(NEW.created_at)),true); END IF;
 RETURN NEW;
END $$;

ALTER TABLE cost_versions
 ADD COLUMN schema_id TEXT COLLATE "C",
 ADD COLUMN schema_digest BYTEA,
 ADD COLUMN variant_key TEXT COLLATE "C" DEFAULT 'default',
 ADD COLUMN currency TEXT DEFAULT 'CNY',
 ADD COLUMN rates_canonical BYTEA,
 ADD COLUMN rates_digest BYTEA,
 ADD COLUMN pricing_operation_id TEXT,
 ADD COLUMN created_by TEXT,
 ADD COLUMN created_xid XID8 DEFAULT pg_current_xact_id();
ALTER TABLE cost_versions ALTER COLUMN pricing_operation_id SET NOT NULL, ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE cost_versions ADD CONSTRAINT cost_versions_pricing_operation_key UNIQUE(pricing_operation_id);
CREATE FUNCTION pricing_cost_result(c cost_versions, expected_revision bigint) RETURNS JSONB LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('id',($1).id,'channel_id',($1).channel_id,'upstream_model',($1).upstream_model,'schema_id',($1).schema_id,'schema_digest',encode(($1).schema_digest,'hex'),'variant_key',($1).variant_key,'currency',($1).currency,'basis',($1).basis,'rates',CASE WHEN ($1).rates_canonical IS NULL THEN 'null'::jsonb ELSE convert_from(($1).rates_canonical,'UTF8')::jsonb END,'effective_at',pricing_time(($1).effective_at),'created_at',pricing_time(($1).created_at),'expected_channel_revision',expected_revision::text,'channel_revision',(expected_revision+1)::text)
$$;
ALTER TABLE cost_versions
 ALTER COLUMN schema_id SET NOT NULL,
 ALTER COLUMN schema_digest SET NOT NULL,
 ALTER COLUMN variant_key SET NOT NULL,
 ALTER COLUMN currency SET NOT NULL;
ALTER TABLE cost_versions
 ADD CHECK(currency='CNY' AND variant_key='default'),
 ADD CONSTRAINT cost_versions_schema_fk FOREIGN KEY(schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED,
 ADD CONSTRAINT cost_versions_operation_fk FOREIGN KEY(pricing_operation_id) REFERENCES pricing_operations(operation_id) DEFERRABLE INITIALLY DEFERRED,
 ADD CONSTRAINT cost_versions_actor_fk FOREIGN KEY(created_by) REFERENCES users(id);

CREATE TABLE cost_rate_lines (
 cost_version_id TEXT NOT NULL COLLATE "C",
 dimension TEXT NOT NULL COLLATE "C",
 unit TEXT NOT NULL COLLATE "C",
 numerator BIGINT NOT NULL CHECK(numerator >= 0),
 denominator BIGINT NOT NULL CHECK(denominator BETWEEN 1 AND 1000000),
 PRIMARY KEY(cost_version_id, dimension),
 FOREIGN KEY(cost_version_id) REFERENCES cost_versions(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER immutable_cost_rate BEFORE UPDATE OR DELETE ON cost_rate_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION meter_cost_operation_birth() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 SELECT * INTO op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 IF NOT FOUND OR op.created_txid<>txid_current() OR op.kind<>'cost_price.publish' OR op.resource_id<>NEW.id OR op.actor<>NEW.created_by THEN RAISE EXCEPTION 'cost price operation mismatch'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER meter_cost_operation_birth BEFORE INSERT ON cost_versions FOR EACH ROW EXECUTE FUNCTION meter_cost_operation_birth();

CREATE FUNCTION meter_insert_token_cost(cost_id text, channel_id text, upstream_model text, schema_id text, input_per_million bigint, output_per_million bigint, basis text, effective_at timestamptz, created_by text, pricing_operation_id text, created_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea; cid text;
BEGIN
 IF basis NOT IN ('contract','invoice','unknown') THEN RAISE EXCEPTION 'invalid cost basis'; END IF;
 SELECT s.id, s.schema_digest INTO sid, sdigest FROM meter_schemas s
  WHERE s.id=meter_insert_token_cost.schema_id AND EXISTS (
   SELECT 1 FROM model_capability_versions c JOIN model_capability_variants v ON v.profile_version=c.id
   WHERE c.schema_id=s.id AND c.schema_digest=s.schema_digest AND v.variant_key='default');
 IF sid IS NULL THEN RAISE EXCEPTION 'published text schema is required before a supplier quote'; END IF;
 cid := coalesce(cost_id, gen_random_uuid()::text);
 IF basis = 'unknown' THEN
  IF input_per_million IS NOT NULL OR output_per_million IS NOT NULL THEN RAISE EXCEPTION 'unknown cost has no rates'; END IF;
  INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,created_by,pricing_operation_id,created_at)
   VALUES(cid, channel_id, upstream_model, basis, coalesce(effective_at, clock_timestamp()), sid, sdigest, 'default', 'CNY', created_by, pricing_operation_id, coalesce(created_at,clock_timestamp()));
  RETURN;
 END IF;
 IF input_per_million IS NULL OR output_per_million IS NULL THEN RAISE EXCEPTION 'known cost requires both rates'; END IF;
 SELECT r.numerator, r.denominator INTO in_n, in_d FROM meter_reduce_million(input_per_million) r;
 SELECT r.numerator, r.denominator INTO out_n, out_d FROM meter_reduce_million(output_per_million) r;
 canonical := meter_rate_canonical(sid, 'supplier', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,rates_canonical,rates_digest,created_by,pricing_operation_id,created_at)
  VALUES(cid, channel_id, upstream_model, basis, coalesce(effective_at, clock_timestamp()), sid, sdigest, 'default', 'CNY', canonical, digest, created_by, pricing_operation_id, coalesce(created_at,clock_timestamp()));
 INSERT INTO cost_rate_lines(cost_version_id,dimension,unit,numerator,denominator) VALUES
  (cid,'text_input_token','token',in_n,in_d),
  (cid,'text_output_token','token',out_n,out_d);
END $$;

CREATE OR REPLACE FUNCTION synchronize_cost_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE channels SET id=id WHERE id=NEW.channel_id;
 IF EXISTS(SELECT 1 FROM cost_versions WHERE channel_id=NEW.channel_id AND upstream_model=NEW.upstream_model AND schema_id=NEW.schema_id AND variant_key=NEW.variant_key AND effective_at=NEW.effective_at) THEN
  RAISE EXCEPTION 'cost version effective instant already exists' USING ERRCODE='23505',CONSTRAINT='cost_version_effective_instant';
 END IF;
 RETURN NEW;
END $$;
DROP INDEX cost_versions_current_quote;
CREATE INDEX cost_versions_current_quote ON cost_versions(channel_id, upstream_model, schema_id, variant_key, effective_at DESC, created_at DESC, id DESC);
ALTER TABLE cost_versions DROP COLUMN input_rate, DROP COLUMN output_rate;

ALTER TABLE model_bindings DROP COLUMN max_input_cost_rate, DROP COLUMN max_output_cost_rate;
ALTER TABLE model_bindings ADD COLUMN variant_key TEXT COLLATE "C" NOT NULL DEFAULT 'default' CHECK(variant_key='default'), ADD COLUMN created_xid XID8 NOT NULL DEFAULT pg_current_xact_id();
DO $$
DECLARE cname text;
BEGIN
 SELECT conname INTO cname FROM pg_constraint
  WHERE conrelid = 'model_bindings'::regclass AND contype = 'u'
    AND pg_get_constraintdef(oid) LIKE '%upstream_model%';
 IF cname IS NULL THEN RAISE EXCEPTION 'model binding uniqueness constraint is missing'; END IF;
 EXECUTE format('ALTER TABLE model_bindings DROP CONSTRAINT %I', cname);
END $$;
ALTER TABLE model_bindings ADD CONSTRAINT model_bindings_route_variant_key UNIQUE(route_policy_version_id, channel_id, upstream_model, variant_key);
CREATE TABLE binding_cost_rate_limits (
 binding_id TEXT NOT NULL,
 dimension TEXT NOT NULL COLLATE "C",
 unit TEXT NOT NULL COLLATE "C",
 numerator BIGINT NOT NULL CHECK(numerator >= 0),
 denominator BIGINT NOT NULL CHECK(denominator BETWEEN 1 AND 1000000),
 PRIMARY KEY(binding_id, dimension),
 FOREIGN KEY(binding_id) REFERENCES model_bindings(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER meter_binding_cap_birth BEFORE INSERT ON binding_cost_rate_limits FOR EACH ROW EXECUTE FUNCTION meter_binding_cap_birth();
CREATE TRIGGER immutable_binding_cost_cap BEFORE UPDATE OR DELETE ON binding_cost_rate_limits FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION meter_insert_token_cap(binding_id text, dimension text, per_million bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE n bigint; d bigint;
BEGIN
 IF dimension NOT IN ('text_input_token','text_output_token') THEN RAISE EXCEPTION 'unsupported cap dimension'; END IF;
 SELECT r.numerator, r.denominator INTO n, d FROM meter_reduce_million(per_million) r;
 INSERT INTO binding_cost_rate_limits(binding_id,dimension,unit,numerator,denominator) VALUES(binding_id, dimension, 'token', n, d);
END $$;

-- A route is one immutable executable set.  Birth checks above prevent
-- appending children to an already committed route, while this deferred
-- checker prevents an empty or structurally detached route from committing.
CREATE FUNCTION meter_route_complete() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE rid text; route_model text; route_profile text; binding_count bigint;
BEGIN
 IF TG_TABLE_NAME='route_policy_versions' THEN
  rid := NEW.id;
 ELSIF TG_TABLE_NAME='model_bindings' THEN
  rid := NEW.route_policy_version_id;
 ELSE
  SELECT b.route_policy_version_id INTO rid FROM model_bindings b WHERE b.id=NEW.binding_id;
 END IF;
 SELECT p.model_id,p.profile_version INTO route_model,route_profile
 FROM route_policy_versions p WHERE p.id=rid;
 IF NOT FOUND THEN RAISE EXCEPTION 'route root is missing'; END IF;
 SELECT count(*) INTO binding_count FROM model_bindings b WHERE b.route_policy_version_id=rid;
 IF binding_count=0 THEN RAISE EXCEPTION 'route requires at least one binding'; END IF;
IF EXISTS (
 SELECT 1
 FROM model_bindings b
 WHERE b.route_policy_version_id=rid
   AND (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id)>0
   AND (
    (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id)<>2
    OR (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id AND l.dimension='text_input_token')<>1
    OR (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id AND l.dimension='text_output_token')<>1
   )
) THEN
 RAISE EXCEPTION 'route binding cap must contain exactly two text dimensions';
END IF;
 IF EXISTS (
  SELECT 1
  FROM model_bindings b
  LEFT JOIN channel_versions v ON v.id=b.channel_version_id
  WHERE b.route_policy_version_id=rid
    AND (v.id IS NULL OR v.channel_id IS DISTINCT FROM b.channel_id
      OR NOT EXISTS (
       SELECT 1
       FROM channel_version_credentials vc
       JOIN channel_credentials c ON c.id=vc.credential_id AND c.channel_id=vc.channel_id
       WHERE vc.channel_version_id=v.id AND vc.channel_id=b.channel_id AND c.revoked_at IS NULL
      ))
 ) THEN
  RAISE EXCEPTION 'route binding supplier contract is incomplete';
 END IF;
 IF EXISTS (
  SELECT 1
  FROM binding_cost_rate_limits l
  JOIN model_bindings b ON b.id=l.binding_id
  LEFT JOIN meter_schema_dimensions d ON d.schema_id=(SELECT c.schema_id FROM model_capability_versions c WHERE c.id=route_profile)
   AND d.dimension=l.dimension AND d.unit=l.unit AND d.supplier
  WHERE b.route_policy_version_id=rid AND d.dimension IS NULL
 ) THEN
  RAISE EXCEPTION 'route binding cap is outside supplier schema';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_route_root_complete
 AFTER INSERT ON route_policy_versions DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_route_complete();
CREATE CONSTRAINT TRIGGER meter_route_binding_complete
 AFTER INSERT ON model_bindings DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_route_complete();
CREATE CONSTRAINT TRIGGER meter_route_cap_complete
 AFTER INSERT ON binding_cost_rate_limits DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_route_complete();
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_route_complete() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_route_complete() FROM PUBLIC;

ALTER TABLE request_records DROP CONSTRAINT request_retail_snapshot_shape;
ALTER TABLE request_records
 ADD COLUMN protocol TEXT COLLATE "C",
 ADD COLUMN execution_kind TEXT COLLATE "C" DEFAULT 'synchronous',
 ADD COLUMN result_kind TEXT COLLATE "C";
ALTER TABLE request_records DROP CONSTRAINT IF EXISTS request_records_route_revision_check;
ALTER TABLE request_records ADD CONSTRAINT request_records_route_revision_positive CHECK(route_revision>0);
ALTER TABLE request_records DROP COLUMN price_version_id, DROP COLUMN input_rate, DROP COLUMN output_rate, DROP COLUMN input_upper_bound, DROP COLUMN output_limit, DROP COLUMN pricing_group_id, DROP COLUMN tenant_pricing_revision, DROP COLUMN retail_priced_at;

CREATE OR REPLACE FUNCTION guard_request_retail_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.profile_version,NEW.protocol,NEW.execution_kind,NEW.result_kind,NEW.request_hmac,NEW.streaming,NEW.route_revision)
   IS DISTINCT FROM ROW(OLD.tenant_id,OLD.key_id,OLD.owner_id,OLD.model_id,OLD.profile_version,OLD.protocol,OLD.execution_kind,OLD.result_kind,OLD.request_hmac,OLD.streaming,OLD.route_revision) THEN
   RAISE EXCEPTION 'immutable request identity';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER meter_request_quote_identity
 BEFORE UPDATE ON request_records FOR EACH ROW EXECUTE FUNCTION guard_request_retail_snapshot();
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.guard_request_retail_snapshot() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION guard_request_retail_snapshot() FROM PUBLIC;

CREATE OR REPLACE FUNCTION guard_procurement_reservation_identity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual_credential TEXT; actual_channel TEXT; bound_credential TEXT; bound_channel TEXT;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.attempt_id,NEW.account_id,NEW.binding_id,NEW.upper_amount,NEW.created_at)
   IS DISTINCT FROM (OLD.attempt_id,OLD.account_id,OLD.binding_id,OLD.upper_amount,OLD.created_at) THEN
   RAISE EXCEPTION 'procurement reservation identity and bounds are immutable';
  END IF;
  IF NEW.state<>OLD.state AND NOT (
   (OLD.state='prepared' AND NEW.state IN ('sending','released','unknown','settled','disputed')) OR
   (OLD.state='sending' AND NEW.state IN ('unknown','settled','released','disputed')) OR
   (OLD.state='unknown' AND NEW.state IN ('settled','released','disputed')) OR
   (OLD.state='settled' AND NEW.state='disputed') OR
   (OLD.state='released' AND NEW.state IN ('disputed','settled')) OR
   (OLD.state='disputed' AND NEW.state='settled')
  ) THEN RAISE EXCEPTION 'invalid procurement reservation state transition'; END IF;
  IF (NEW.state,NEW.held_amount,NEW.spent_amount,NEW.anomaly) IS DISTINCT FROM (OLD.state,OLD.held_amount,OLD.spent_amount,OLD.anomaly) THEN NEW.revision := OLD.revision+1; ELSE NEW.revision := OLD.revision; END IF;
  RETURN NEW;
 END IF;
 NEW.revision := 1;
 SELECT credential_id,channel_id INTO actual_credential,actual_channel FROM upstream_attempts WHERE id=NEW.attempt_id;
 SELECT credential_id,channel_id INTO bound_credential,bound_channel FROM credential_procurement_bindings WHERE id=NEW.binding_id AND account_id=NEW.account_id;
 IF actual_credential IS NULL OR bound_credential IS NULL OR (actual_credential,actual_channel) IS DISTINCT FROM (bound_credential,bound_channel) THEN
  RAISE EXCEPTION 'procurement reservation attempt binding mismatch' USING ERRCODE='23503';
 END IF;
 RETURN NEW;
END $$;
ALTER TABLE procurement_reservations DROP COLUMN input_bound, DROP COLUMN output_bound;

CREATE TABLE request_meter_snapshots (
 tenant_id TEXT NOT NULL,
 request_id TEXT PRIMARY KEY,
 model_id TEXT NOT NULL,
 profile_version TEXT NOT NULL,
 schema_id TEXT NOT NULL,
 schema_digest BYTEA NOT NULL CHECK(octet_length(schema_digest)=32),
 variant_key TEXT NOT NULL DEFAULT 'default' CHECK(variant_key='default'),
 price_version_id TEXT NOT NULL REFERENCES price_versions(id),
 pricing_group_id TEXT,
 tenant_pricing_revision BIGINT NOT NULL CHECK(tenant_pricing_revision>0),
 retail_priced_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(retail_priced_at)),
 input_bound BIGINT NOT NULL CHECK(input_bound>=0),
 output_bound BIGINT NOT NULL CHECK(output_bound>=0),
 plan_canonical BYTEA,
 plan_digest BYTEA,
 retail_bounds_canonical BYTEA,
 retail_bounds_digest BYTEA,
 supplier_bounds_canonical BYTEA,
 supplier_bounds_digest BYTEA,
 retail_rates_canonical BYTEA,
 retail_rates_digest BYTEA,
 retail_meter_quote_digest BYTEA,
 retail_quote_canonical BYTEA,
 retail_quote_digest BYTEA,
 created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
 CHECK((plan_canonical IS NULL AND plan_digest IS NULL) OR (plan_canonical IS NOT NULL AND octet_length(plan_canonical)<=65536 AND octet_length(plan_digest)=32)),
 CHECK(octet_length(retail_bounds_canonical) BETWEEN 1 AND 8192 AND octet_length(retail_bounds_digest)=32),
 CHECK(octet_length(supplier_bounds_canonical) BETWEEN 1 AND 8192 AND octet_length(supplier_bounds_digest)=32),
 CHECK(octet_length(retail_rates_canonical) BETWEEN 1 AND 8192 AND octet_length(retail_rates_digest)=32 AND octet_length(retail_meter_quote_digest)=32),
 CHECK((retail_quote_canonical IS NULL AND retail_quote_digest IS NULL) OR (retail_quote_canonical IS NOT NULL AND octet_length(retail_quote_canonical)<=16384 AND octet_length(retail_quote_digest)=32)),
 created_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(tenant_id, request_id),
 FOREIGN KEY(tenant_id, request_id) REFERENCES request_records(tenant_id, id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER immutable_request_meter_snapshot BEFORE UPDATE OR DELETE ON request_meter_snapshots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION meter_request_snapshot_present() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM request_meter_snapshots WHERE request_id=NEW.id AND tenant_id=NEW.tenant_id AND model_id=NEW.model_id AND profile_version=NEW.profile_version) THEN
  RAISE EXCEPTION 'request requires one meter snapshot';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_snapshot_present AFTER INSERT ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_snapshot_present();

CREATE FUNCTION meter_plan_ready(profile_version text, request_hmac text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT profile_version ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND profile_version <> '00000000-0000-0000-0000-000000000000'
  AND request_hmac ~ '^[0-9a-f]{64}$'
$$;
CREATE FUNCTION meter_profile_canonical(profile_version text, model text, protocol text, schema_id text, schema_digest bytea, contract bytea, input_max bigint, output_max bigint) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT convert_to(format('{"version":"1","profile_version":"%s","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kinds":%s,"schema_id":"%s","schema_digest":"%s","variant_key":"default","contract_digest":"%s","input_token_upper_bound":"%s","output_limit":"%s"}',
  profile_version, model, protocol,
  CASE WHEN protocol IN ('openai_embeddings','jina_rerank') THEN '["json"]' ELSE '["json","events"]' END,
  schema_id, encode(schema_digest,'hex'), encode(contract,'hex'), input_max, output_max), 'UTF8')
$$;
CREATE FUNCTION meter_profile_digest(schema_digest bytea, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.profile.v1','UTF8'), schema_digest, canonical]))
$$;
CREATE FUNCTION meter_bounds_canonical(schema_id text, side text, input_qty bigint, output_qty bigint) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT convert_to(format('{"schema_id":"%s","version":"1","side":"%s","bounds":[{"dimension":"text_input_token","unit":"token","quantity":"%s"},{"dimension":"text_output_token","unit":"token","quantity":"%s"}]}', schema_id, side, input_qty, output_qty), 'UTF8')
$$;
CREATE FUNCTION meter_request_vectors() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rates bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL
   OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL
   OR NEW.retail_rates_canonical IS NOT NULL OR NEW.retail_rates_digest IS NOT NULL OR NEW.retail_meter_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'request meter vectors require plan';
  END IF;
  RETURN NEW;
 END IF;
 SELECT p.rates_canonical INTO rates
  FROM price_versions p WHERE p.id=NEW.price_version_id;
 IF rates IS NULL THEN RAISE EXCEPTION 'request meter vectors price mismatch'; END IF;
 NEW.retail_bounds_canonical := meter_bounds_canonical(NEW.schema_id,'retail',NEW.input_bound,NEW.output_bound);
 NEW.supplier_bounds_canonical := meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 NEW.retail_bounds_digest := meter_bounds_digest(NEW.schema_digest,'retail',NEW.retail_bounds_canonical);
 NEW.supplier_bounds_digest := meter_bounds_digest(NEW.schema_digest,'supplier',NEW.supplier_bounds_canonical);
 NEW.retail_rates_canonical := rates;
 NEW.retail_rates_digest := meter_rates_digest(NEW.schema_digest,'retail',rates);
 NEW.retail_meter_quote_digest := meter_pure_quote_digest(NEW.schema_digest,'retail',NEW.retail_bounds_canonical,rates);
 RETURN NEW;
END $$;
CREATE TRIGGER meter_request_vectors BEFORE INSERT ON request_meter_snapshots FOR EACH ROW EXECUTE FUNCTION meter_request_vectors();
CREATE FUNCTION meter_request_vectors_match() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rates bytea; rb bytea; sb bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL
   OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL
   OR NEW.retail_rates_canonical IS NOT NULL OR NEW.retail_rates_digest IS NOT NULL OR NEW.retail_meter_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'request meter vectors require plan';
  END IF;
  RETURN NULL;
 END IF;
 SELECT p.rates_canonical INTO rates FROM price_versions p WHERE p.id=NEW.price_version_id;
 rb := meter_bounds_canonical(NEW.schema_id,'retail',NEW.input_bound,NEW.output_bound);
 sb := meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 IF rates IS NULL OR NEW.retail_bounds_canonical IS DISTINCT FROM rb OR NEW.supplier_bounds_canonical IS DISTINCT FROM sb
  OR NEW.retail_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'retail',rb)
  OR NEW.supplier_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'supplier',sb)
  OR NEW.retail_rates_canonical IS DISTINCT FROM rates
  OR NEW.retail_rates_digest IS DISTINCT FROM meter_rates_digest(NEW.schema_digest,'retail',rates)
  OR NEW.retail_meter_quote_digest IS DISTINCT FROM meter_pure_quote_digest(NEW.schema_digest,'retail',rb,rates) THEN
  RAISE EXCEPTION 'request meter vectors mismatch';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_vectors_match AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_vectors_match();
CREATE FUNCTION meter_request_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text) RETURNS bytea LANGUAGE plpgsql STABLE AS $$
DECLARE model text; protocol text; contract bytea; input_max bigint; output_max bigint; profile bytea; result_kind text;
BEGIN
 SELECT c.profile->>'model', c.protocol, c.contract_digest, (c.profile->>'input_token_upper_bound')::bigint, (c.profile->>'output_limit')::bigint
  INTO model, protocol, contract, input_max, output_max
  FROM model_capability_versions c WHERE c.id=profile_version AND c.model_id=meter_request_plan_canonical.model_id AND c.schema_id=meter_request_plan_canonical.schema_id AND c.schema_digest=meter_request_plan_canonical.schema_digest;
 IF model IS NULL OR output_limit < 0 OR output_limit > output_max THEN RETURN NULL; END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') THEN
  IF output_limit <> 0 OR streaming THEN RETURN NULL; END IF;
 ELSIF output_limit = 0 THEN RETURN NULL; END IF;
 profile := meter_profile_canonical(profile_version, model, protocol, schema_id, schema_digest, contract, input_max, output_max);
 result_kind := CASE WHEN streaming THEN 'events' ELSE 'json' END;
 RETURN convert_to(format('{"version":"1","scope":"request","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kind":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","input_hmac":"%s","output_limit":"%s","candidate_profile_digests":["%s"],"selected_profile_digest":null,"parent_plan_digest":null,"retail_bounds":%s,"supplier_bounds":%s}',
  model, protocol, result_kind, schema_id, encode(schema_digest,'hex'), request_hmac, output_limit,
  encode(meter_profile_digest(schema_digest, profile),'hex'),
  convert_from(meter_bounds_canonical(schema_id,'retail',input_max,output_limit),'UTF8'),
  convert_from(meter_bounds_canonical(schema_id,'supplier',input_max,output_limit),'UTF8')), 'UTF8');
END $$;
CREATE FUNCTION meter_plan_digest(schema_digest bytea, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.plan.v1','UTF8'), schema_digest, canonical]))
$$;
CREATE FUNCTION meter_request_plan_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE hmac text; streaming boolean; canonical bytea; digest bytea;
BEGIN
 SELECT r.request_hmac, r.streaming INTO hmac, streaming FROM request_records r WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
 IF NOT meter_plan_ready(NEW.profile_version, hmac) THEN
  IF NEW.plan_canonical IS NOT NULL OR NEW.plan_digest IS NOT NULL THEN RAISE EXCEPTION 'request plan does not match profile'; END IF;
  RETURN NULL;
 END IF;
 canonical := meter_request_plan_canonical(NEW.profile_version, NEW.model_id, NEW.schema_id, NEW.schema_digest, NEW.output_bound, streaming, hmac);
 digest := meter_plan_digest(NEW.schema_digest, canonical);
 IF canonical IS NULL OR NEW.plan_canonical IS DISTINCT FROM canonical OR NEW.plan_digest IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'request plan does not match profile';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_plan_matches AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_plan_matches();

CREATE FUNCTION meter_attempt_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text) RETURNS bytea LANGUAGE plpgsql STABLE AS $$
DECLARE model text; protocol text; contract bytea; input_max bigint; output_max bigint; profile bytea; request_plan bytea; result_kind text; profile_hex text;
BEGIN
 request_plan := meter_request_plan_canonical(profile_version, model_id, schema_id, schema_digest, output_limit, streaming, request_hmac);
 IF request_plan IS NULL THEN RETURN NULL; END IF;
 SELECT c.profile->>'model', c.protocol, c.contract_digest, (c.profile->>'input_token_upper_bound')::bigint, (c.profile->>'output_limit')::bigint
  INTO model, protocol, contract, input_max, output_max
  FROM model_capability_versions c WHERE c.id=profile_version AND c.model_id=meter_attempt_plan_canonical.model_id AND c.schema_id=meter_attempt_plan_canonical.schema_id AND c.schema_digest=meter_attempt_plan_canonical.schema_digest;
 profile := meter_profile_canonical(profile_version, model, protocol, schema_id, schema_digest, contract, input_max, output_max);
 profile_hex := encode(meter_profile_digest(schema_digest, profile), 'hex');
 result_kind := CASE WHEN streaming THEN 'events' ELSE 'json' END;
 RETURN convert_to(format('{"version":"1","scope":"attempt","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kind":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","input_hmac":"%s","output_limit":"%s","candidate_profile_digests":["%s"],"selected_profile_digest":"%s","parent_plan_digest":"%s","retail_bounds":%s,"supplier_bounds":%s}',
  model, protocol, result_kind, schema_id, encode(schema_digest,'hex'), request_hmac, output_limit, profile_hex, profile_hex,
  encode(meter_plan_digest(schema_digest, request_plan), 'hex'),
  convert_from(meter_bounds_canonical(schema_id,'retail',input_max,output_limit),'UTF8'),
  convert_from(meter_bounds_canonical(schema_id,'supplier',input_max,output_limit),'UTF8')), 'UTF8');
END $$;

CREATE FUNCTION meter_bounds_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.bounds.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;
CREATE FUNCTION meter_rates_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;
CREATE FUNCTION meter_pure_quote_digest(schema_digest bytea, side text, bounds_canonical bytea, rates_canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.quote.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), bounds_canonical, rates_canonical]))
$$;
CREATE FUNCTION meter_quote_digest(schema_digest bytea, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.quote.v1','UTF8'), schema_digest, canonical]))
$$;
CREATE FUNCTION meter_retail_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, price_version_id text, input_bound bigint, output_bound bigint) RETURNS bytea LANGUAGE plpgsql STABLE AS $$
DECLARE rates bytea;
BEGIN
 IF octet_length(plan_digest)<>32 OR octet_length(schema_digest)<>32
  OR price_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  OR price_version_id='00000000-0000-0000-0000-000000000000' THEN
  RETURN NULL;
 END IF;
 SELECT p.rates_canonical INTO rates FROM price_versions p
  WHERE p.id=price_version_id AND p.schema_id=meter_retail_quote_canonical.schema_id AND p.schema_digest=meter_retail_quote_canonical.schema_digest AND p.variant_key='default';
 IF rates IS NULL THEN RETURN NULL; END IF;
 RETURN convert_to(format('{"version":"1","status":"known","side":"retail","plan_digest":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","version_id":"%s","unknown_reason":null,"bounds":%s,"rates":%s}',
  encode(plan_digest,'hex'), schema_id, encode(schema_digest,'hex'), price_version_id,
  convert_from(meter_bounds_canonical(schema_id,'retail',input_bound,output_bound),'UTF8'), convert_from(rates,'UTF8')), 'UTF8');
END $$;
CREATE FUNCTION meter_supplier_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, quote_status text, unknown_reason text, cost_version_id text, rates_canonical bytea, input_bound bigint, output_bound bigint) RETURNS bytea LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE version_json text; reason_json text; rates_json text;
BEGIN
 IF octet_length(plan_digest)<>32 OR octet_length(schema_digest)<>32 THEN RETURN NULL; END IF;
 IF quote_status='known' THEN
  IF unknown_reason IS NOT NULL OR rates_canonical IS NULL
   OR cost_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR cost_version_id='00000000-0000-0000-0000-000000000000' THEN
   RETURN NULL;
  END IF;
  version_json := format('"%s"', cost_version_id);
  reason_json := 'null';
  rates_json := convert_from(rates_canonical,'UTF8');
 ELSIF quote_status='unknown' AND unknown_reason='version_missing' AND cost_version_id IS NULL AND rates_canonical IS NULL THEN
  version_json := 'null';
  reason_json := '"version_missing"';
  rates_json := 'null';
 ELSIF quote_status='unknown' AND unknown_reason='basis_unknown' AND rates_canonical IS NULL
  AND cost_version_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND cost_version_id<>'00000000-0000-0000-0000-000000000000' THEN
  version_json := format('"%s"', cost_version_id);
  reason_json := '"basis_unknown"';
  rates_json := 'null';
 ELSE
  RETURN NULL;
 END IF;
 RETURN convert_to(format('{"version":"1","status":"%s","side":"supplier","plan_digest":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","version_id":%s,"unknown_reason":%s,"bounds":%s,"rates":%s}',
  quote_status, encode(plan_digest,'hex'), schema_id, encode(schema_digest,'hex'), version_json, reason_json,
  convert_from(meter_bounds_canonical(schema_id,'supplier',input_bound,output_bound),'UTF8'), rates_json), 'UTF8');
END $$;
CREATE FUNCTION meter_request_quote_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE canonical bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_quote_canonical IS NOT NULL OR NEW.retail_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'request quote does not match price';
  END IF;
  RETURN NULL;
 END IF;
 canonical := meter_retail_quote_canonical(NEW.schema_id, NEW.schema_digest, NEW.plan_digest, NEW.price_version_id, NEW.input_bound, NEW.output_bound);
 IF canonical IS NULL OR NEW.retail_quote_canonical IS DISTINCT FROM canonical OR NEW.retail_quote_digest IS DISTINCT FROM meter_quote_digest(NEW.schema_digest, canonical) THEN
  RAISE EXCEPTION 'request quote does not match price';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_quote_matches AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_quote_matches();

CREATE FUNCTION meter_request_price_matches() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM price_versions p
  WHERE p.id=NEW.price_version_id AND p.model_id=NEW.model_id AND p.schema_id=NEW.schema_id
    AND p.schema_digest=NEW.schema_digest AND p.variant_key=NEW.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM NEW.pricing_group_id
 ) THEN
  RAISE EXCEPTION 'request price does not match model';
 END IF;
 IF EXISTS (SELECT 1 FROM request_records r WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id
  AND NOT EXISTS (SELECT 1 FROM route_policy_versions p WHERE p.model_id=r.model_id AND p.revision=r.route_revision AND p.profile_version=NEW.profile_version)) THEN
  RAISE EXCEPTION 'request route does not match profile';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_price_matches AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_price_matches();

CREATE TABLE request_meter_bound_lines (
 tenant_id TEXT NOT NULL,
 request_id TEXT NOT NULL,
 side TEXT NOT NULL CHECK(side IN ('retail','supplier')),
 dimension TEXT NOT NULL CHECK(dimension IN ('text_input_token','text_output_token')),
 unit TEXT NOT NULL CHECK(unit='token'),
 quantity BIGINT NOT NULL CHECK(quantity>=0),
 PRIMARY KEY(request_id, side, dimension),
 UNIQUE(tenant_id, request_id, side, dimension),
 FOREIGN KEY(tenant_id, request_id) REFERENCES request_meter_snapshots(tenant_id, request_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER immutable_request_meter_bound BEFORE UPDATE OR DELETE ON request_meter_bound_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION meter_request_bounds_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE matched bigint; total bigint;
BEGIN
 SELECT count(*) INTO total FROM request_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 SELECT count(*) INTO matched FROM request_meter_bound_lines
  WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id AND unit='token'
    AND ((side IN ('retail','supplier') AND dimension='text_input_token' AND quantity=NEW.input_bound)
      OR (side IN ('retail','supplier') AND dimension='text_output_token' AND quantity=NEW.output_bound));
 IF total IS DISTINCT FROM 4 OR matched IS DISTINCT FROM 4 THEN
  RAISE EXCEPTION 'request bounds are incomplete';
 END IF;
 IF EXISTS (
  SELECT 1 FROM (VALUES ('retail'),('supplier')) AS s(side)
  WHERE (SELECT b.quantity FROM model_capability_bound_lines b WHERE b.profile_version=NEW.profile_version AND b.variant_key=NEW.variant_key AND b.side=s.side AND b.dimension='text_input_token') IS DISTINCT FROM NEW.input_bound
     OR (SELECT b.quantity FROM model_capability_bound_lines b WHERE b.profile_version=NEW.profile_version AND b.variant_key=NEW.variant_key AND b.side=s.side AND b.dimension='text_output_token') < NEW.output_bound
 ) THEN
  RAISE EXCEPTION 'request bounds are incomplete';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_bounds_complete AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_bounds_complete();

ALTER TABLE upstream_attempts DROP COLUMN cost_version_id, DROP COLUMN cost_input_rate, DROP COLUMN cost_output_rate, DROP COLUMN cost_basis;
ALTER TABLE upstream_attempts
 ADD COLUMN latest_cost_finalization_id TEXT,
 ADD COLUMN latest_cost_correction_id TEXT,
 ADD CONSTRAINT upstream_attempts_request_identity UNIQUE(tenant_id, id, request_id),
 ADD CONSTRAINT upstream_attempts_cost_pointer_exclusive CHECK(
  NOT (latest_cost_finalization_id IS NOT NULL AND latest_cost_correction_id IS NOT NULL)
 ),
 ADD CONSTRAINT upstream_attempts_cost_source_projection CHECK(
  (cost_status='unknown' AND cost_amount IS NULL AND latest_cost_finalization_id IS NULL AND latest_cost_correction_id IS NULL)
  OR
  (cost_status IN ('contract','invoice') AND cost_amount IS NOT NULL AND
   ((latest_cost_finalization_id IS NOT NULL) <> (latest_cost_correction_id IS NOT NULL)))
 );
ALTER TABLE provider_cost_corrections
 ADD CONSTRAINT provider_cost_corrections_attempt_identity UNIQUE(tenant_id, id, attempt_id);

CREATE TABLE attempt_meter_snapshots (
 tenant_id TEXT NOT NULL,
 attempt_id TEXT PRIMARY KEY,
 request_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 profile_version TEXT NOT NULL,
 schema_id TEXT NOT NULL,
 schema_digest BYTEA NOT NULL CHECK(octet_length(schema_digest)=32),
 variant_key TEXT NOT NULL DEFAULT 'default' CHECK(variant_key='default'),
 quoted_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(quoted_at)),
 cost_version_id TEXT REFERENCES cost_versions(id),
 quote_status TEXT NOT NULL CHECK(quote_status IN ('known','unknown')),
 unknown_reason TEXT CHECK(unknown_reason IN ('version_missing','basis_unknown')),
 cost_basis TEXT NOT NULL CHECK(cost_basis IN ('contract','invoice','unknown')),
 supplier_rates_canonical BYTEA,
 supplier_rates_digest BYTEA,
 retail_bounds_canonical BYTEA,
 retail_bounds_digest BYTEA,
 supplier_bounds_canonical BYTEA,
 supplier_bounds_digest BYTEA,
 supplier_meter_quote_digest BYTEA,
 input_bound BIGINT NOT NULL CHECK(input_bound>=0),
 output_bound BIGINT NOT NULL CHECK(output_bound>=0),
 plan_canonical BYTEA,
 plan_digest BYTEA,
 parent_plan_digest BYTEA,
 supplier_quote_canonical BYTEA,
 supplier_quote_digest BYTEA,
 created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
 created_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((plan_canonical IS NULL AND plan_digest IS NULL) OR (plan_canonical IS NOT NULL AND octet_length(plan_canonical)<=65536 AND octet_length(plan_digest)=32)),
 CHECK((plan_digest IS NULL AND parent_plan_digest IS NULL) OR (plan_digest IS NOT NULL AND octet_length(parent_plan_digest)=32)),
 CHECK(octet_length(retail_bounds_canonical) BETWEEN 1 AND 8192 AND octet_length(retail_bounds_digest)=32),
 CHECK(octet_length(supplier_bounds_canonical) BETWEEN 1 AND 8192 AND octet_length(supplier_bounds_digest)=32),
 CHECK((supplier_rates_canonical IS NULL AND supplier_rates_digest IS NULL AND supplier_meter_quote_digest IS NULL)
    OR (supplier_rates_canonical IS NOT NULL AND octet_length(supplier_rates_canonical) BETWEEN 1 AND 8192 AND octet_length(supplier_rates_digest)=32 AND octet_length(supplier_meter_quote_digest)=32)),
 CHECK((supplier_quote_canonical IS NULL AND supplier_quote_digest IS NULL) OR (supplier_quote_canonical IS NOT NULL AND octet_length(supplier_quote_canonical)<=16384 AND octet_length(supplier_quote_digest)=32)),
 UNIQUE(tenant_id, attempt_id),
 UNIQUE(tenant_id, attempt_id, request_id),
 FOREIGN KEY(tenant_id, attempt_id, request_id) REFERENCES upstream_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id, request_id) REFERENCES request_meter_snapshots(tenant_id, request_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED,
 CHECK(
  (quote_status='known' AND unknown_reason IS NULL AND cost_version_id IS NOT NULL AND cost_basis IN ('contract','invoice')
   AND (plan_digest IS NULL OR (supplier_rates_canonical IS NOT NULL AND supplier_rates_digest IS NOT NULL)))
  OR
  (quote_status='unknown' AND cost_basis='unknown' AND supplier_rates_canonical IS NULL AND supplier_rates_digest IS NULL AND (
    (unknown_reason='version_missing' AND cost_version_id IS NULL) OR
    (unknown_reason='basis_unknown' AND cost_version_id IS NOT NULL)
  ))
 )
);
CREATE TRIGGER immutable_attempt_meter_snapshot BEFORE UPDATE OR DELETE ON attempt_meter_snapshots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION meter_insert_attempt_snapshot(tenant_id text, attempt_id text, request_id text, cost_version_id text, basis text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; model text; profile text; inb bigint; outb bigint; request_plan_digest bytea; parent_plan_digest bytea; stored text; rates bytea; digest bytea; status text; reason text; hmac text; streaming boolean; attempt_plan bytea; attempt_digest bytea; supplier_quote bytea; supplier_quote_digest bytea; retail_bounds bytea; supplier_bounds bytea; retail_bounds_digest bytea; supplier_bounds_digest bytea; supplier_meter_quote_digest bytea;
BEGIN
 SELECT s.schema_id, s.schema_digest, s.model_id, s.profile_version, s.input_bound, s.output_bound, s.plan_digest
  INTO sid, sdigest, model, profile, inb, outb, request_plan_digest
  FROM request_meter_snapshots s
  WHERE s.tenant_id=meter_insert_attempt_snapshot.tenant_id AND s.request_id=meter_insert_attempt_snapshot.request_id;
 IF sid IS NULL THEN RAISE EXCEPTION 'request meter snapshot missing'; END IF;
 IF meter_insert_attempt_snapshot.cost_version_id IS NULL THEN
  IF basis IS DISTINCT FROM 'unknown' THEN RAISE EXCEPTION 'missing supplier quote is unknown'; END IF;
  status := 'unknown'; reason := 'version_missing';
 ELSE
  SELECT c.basis, c.rates_canonical, c.rates_digest INTO stored, rates, digest FROM cost_versions c WHERE c.id=meter_insert_attempt_snapshot.cost_version_id;
  IF stored IS NULL THEN RAISE EXCEPTION 'supplier quote version missing'; END IF;
  IF stored='unknown' THEN
   status := 'unknown'; reason := 'basis_unknown'; rates := NULL; digest := NULL; basis := 'unknown';
  ELSE
   status := 'known'; reason := NULL; basis := stored;
  END IF;
 END IF;
 SELECT r.request_hmac, r.streaming INTO hmac, streaming FROM request_records r WHERE r.tenant_id=meter_insert_attempt_snapshot.tenant_id AND r.id=meter_insert_attempt_snapshot.request_id;
 IF meter_plan_ready(profile, hmac) THEN
  attempt_plan := meter_attempt_plan_canonical(profile, model, sid, sdigest, outb, streaming, hmac);
  IF attempt_plan IS NOT NULL THEN
   attempt_digest := meter_plan_digest(sdigest, attempt_plan);
   parent_plan_digest := request_plan_digest;
  END IF;
  supplier_quote := meter_supplier_quote_canonical(sid, sdigest, attempt_digest, status, reason, meter_insert_attempt_snapshot.cost_version_id, rates, inb, outb);
  supplier_quote_digest := meter_quote_digest(sdigest, supplier_quote);
 END IF;
 IF attempt_digest IS NOT NULL THEN
  retail_bounds := meter_bounds_canonical(sid,'retail',inb,outb);
  supplier_bounds := meter_bounds_canonical(sid,'supplier',inb,outb);
  retail_bounds_digest := meter_bounds_digest(sdigest,'retail',retail_bounds);
  supplier_bounds_digest := meter_bounds_digest(sdigest,'supplier',supplier_bounds);
  IF rates IS NOT NULL THEN
   supplier_meter_quote_digest := meter_pure_quote_digest(sdigest,'supplier',supplier_bounds,rates);
  END IF;
 ELSE
  rates := NULL;
  digest := NULL;
 END IF;
 INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,parent_plan_digest,supplier_quote_canonical,supplier_quote_digest)
  VALUES(meter_insert_attempt_snapshot.tenant_id, meter_insert_attempt_snapshot.attempt_id, meter_insert_attempt_snapshot.request_id, model, profile, sid, sdigest, 'default', meter_insert_attempt_snapshot.cost_version_id, status, reason, basis, rates, digest, retail_bounds, retail_bounds_digest, supplier_bounds, supplier_bounds_digest, supplier_meter_quote_digest, inb, outb, attempt_plan, attempt_digest, parent_plan_digest, supplier_quote, supplier_quote_digest);
 INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
  SELECT meter_insert_attempt_snapshot.tenant_id, meter_insert_attempt_snapshot.attempt_id, side, dimension, 'token', CASE dimension WHEN 'text_input_token' THEN inb ELSE outb END
  FROM (VALUES ('retail'),('supplier')) AS s(side)
  CROSS JOIN (VALUES ('text_input_token'),('text_output_token')) AS d(dimension);
END $$;

CREATE FUNCTION meter_attempt_plan_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE hmac text; streaming boolean; ready boolean; canonical bytea; digest bytea; request_plan_digest bytea;
BEGIN
 SELECT r.request_hmac, r.streaming, q.plan_canonical IS NOT NULL, q.plan_digest INTO hmac, streaming, ready, request_plan_digest
  FROM request_records r JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
  WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
 IF NOT coalesce(ready, false) THEN
  IF NEW.plan_canonical IS NOT NULL OR NEW.plan_digest IS NOT NULL OR NEW.parent_plan_digest IS NOT NULL THEN RAISE EXCEPTION 'attempt plan does not match request'; END IF;
  RETURN NULL;
 END IF;
 canonical := meter_attempt_plan_canonical(NEW.profile_version, NEW.model_id, NEW.schema_id, NEW.schema_digest, NEW.output_bound, streaming, hmac);
 digest := meter_plan_digest(NEW.schema_digest, canonical);
 IF canonical IS NULL OR NEW.plan_canonical IS DISTINCT FROM canonical OR NEW.plan_digest IS DISTINCT FROM digest OR NEW.parent_plan_digest IS DISTINCT FROM request_plan_digest THEN
  RAISE EXCEPTION 'attempt plan does not match request';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_plan_matches AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_plan_matches();
CREATE FUNCTION meter_attempt_quote_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE canonical bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.supplier_quote_canonical IS NOT NULL OR NEW.supplier_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'attempt quote does not match cost';
  END IF;
  RETURN NULL;
 END IF;
 canonical := meter_supplier_quote_canonical(NEW.schema_id, NEW.schema_digest, NEW.plan_digest, NEW.quote_status, NEW.unknown_reason, NEW.cost_version_id, NEW.supplier_rates_canonical, NEW.input_bound, NEW.output_bound);
 IF canonical IS NULL OR NEW.supplier_quote_canonical IS DISTINCT FROM canonical OR NEW.supplier_quote_digest IS DISTINCT FROM meter_quote_digest(NEW.schema_digest, canonical) THEN
  RAISE EXCEPTION 'attempt quote does not match cost';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_quote_matches AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_quote_matches();
CREATE FUNCTION meter_attempt_vectors_match() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rb bytea; sb bytea; rates bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL
   OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL
   OR NEW.supplier_rates_canonical IS NOT NULL OR NEW.supplier_rates_digest IS NOT NULL OR NEW.supplier_meter_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'attempt meter vectors require plan';
  END IF;
  RETURN NULL;
 END IF;
 rb := meter_bounds_canonical(NEW.schema_id,'retail',NEW.input_bound,NEW.output_bound);
 sb := meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 SELECT c.rates_canonical INTO rates FROM cost_versions c WHERE c.id=NEW.cost_version_id AND c.basis IN ('contract','invoice');
 IF NEW.retail_bounds_canonical IS DISTINCT FROM rb OR NEW.supplier_bounds_canonical IS DISTINCT FROM sb
  OR NEW.retail_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'retail',rb)
  OR NEW.supplier_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'supplier',sb)
  OR (rates IS NULL AND (NEW.supplier_rates_canonical IS NOT NULL OR NEW.supplier_rates_digest IS NOT NULL OR NEW.supplier_meter_quote_digest IS NOT NULL))
  OR (rates IS NOT NULL AND (NEW.supplier_rates_canonical IS DISTINCT FROM rates
    OR NEW.supplier_rates_digest IS DISTINCT FROM meter_rates_digest(NEW.schema_digest,'supplier',rates)
    OR NEW.supplier_meter_quote_digest IS DISTINCT FROM meter_pure_quote_digest(NEW.schema_digest,'supplier',sb,rates))) THEN
  RAISE EXCEPTION 'attempt meter vectors mismatch';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_vectors_match AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_vectors_match();

CREATE TABLE attempt_meter_bound_lines (
 tenant_id TEXT NOT NULL,
 attempt_id TEXT NOT NULL,
 side TEXT NOT NULL CHECK(side IN ('retail','supplier')),
 dimension TEXT NOT NULL CHECK(dimension IN ('text_input_token','text_output_token')),
 unit TEXT NOT NULL CHECK(unit='token'),
 quantity BIGINT NOT NULL CHECK(quantity>=0),
 PRIMARY KEY(attempt_id, side, dimension),
 UNIQUE(tenant_id, attempt_id, side, dimension),
 FOREIGN KEY(tenant_id, attempt_id) REFERENCES attempt_meter_snapshots(tenant_id, attempt_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER immutable_attempt_meter_bound BEFORE UPDATE OR DELETE ON attempt_meter_bound_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION meter_attempt_bounds_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE matched bigint; total bigint; request_input bigint; request_output bigint;
BEGIN
 SELECT input_bound, output_bound INTO request_input, request_output FROM request_meter_snapshots WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 SELECT count(*) INTO total FROM attempt_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND attempt_id=NEW.attempt_id;
 SELECT count(*) INTO matched FROM attempt_meter_bound_lines
  WHERE tenant_id=NEW.tenant_id AND attempt_id=NEW.attempt_id AND unit='token'
    AND ((side IN ('retail','supplier') AND dimension='text_input_token' AND quantity=NEW.input_bound)
      OR (side IN ('retail','supplier') AND dimension='text_output_token' AND quantity=NEW.output_bound));
 IF total IS DISTINCT FROM 4 OR matched IS DISTINCT FROM 4 OR NEW.input_bound IS DISTINCT FROM request_input OR NEW.output_bound IS DISTINCT FROM request_output THEN
  RAISE EXCEPTION 'attempt bounds are incomplete';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_bounds_complete AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_bounds_complete();

-- Read-only HTTP projection of the original supplier bound lines, including
-- plan-unready attempts. Missing originals must fail a page, never hide a hold.
CREATE FUNCTION meter_attempt_supplier_bounds(tenant_id text,request_id text,attempt_id text) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
DECLARE sid text; input_max bigint; output_max bigint; total bigint; matched bigint; mask_count bigint; entries jsonb;
BEGIN
 SELECT s.schema_id,s.input_bound,s.output_bound INTO sid,input_max,output_max
 FROM attempt_meter_snapshots s
 JOIN upstream_attempts a ON a.tenant_id=s.tenant_id AND a.request_id=s.request_id AND a.id=s.attempt_id
 JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest
 WHERE s.tenant_id=$1 AND s.request_id=$2 AND s.attempt_id=$3 AND s.variant_key='default';
 IF NOT FOUND THEN RAISE EXCEPTION 'original supplier bounds are unavailable'; END IF;
 SELECT count(*) INTO mask_count FROM meter_schema_dimensions d WHERE d.schema_id=sid AND d.supplier;
 SELECT count(*),count(*) FILTER (WHERE b.tenant_id=$1 AND d.supplier AND b.unit=d.unit
   AND b.unit='token' AND b.dimension IN ('text_input_token','text_output_token')
   AND b.quantity BETWEEN 0 AND d.maximum_quantity
   AND b.quantity=CASE b.dimension WHEN 'text_input_token' THEN input_max WHEN 'text_output_token' THEN output_max END),
  jsonb_agg(jsonb_build_object('dimension',b.dimension,'unit',b.unit,'quantity',b.quantity::text) ORDER BY b.dimension COLLATE "C")
 INTO total,matched,entries
 FROM attempt_meter_bound_lines b
 LEFT JOIN meter_schema_dimensions d ON d.schema_id=sid AND d.dimension=b.dimension
 WHERE b.attempt_id=$3 AND b.side='supplier';
 IF mask_count<>2 OR total<>2 OR matched<>2 THEN RAISE EXCEPTION 'original supplier bounds are incomplete'; END IF;
 RETURN jsonb_build_object('schema_id',sid,'version','1','side','supplier','bounds',entries);
END $$;
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_attempt_supplier_bounds(text,text,text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_attempt_supplier_bounds(text,text,text) FROM PUBLIC;

CREATE FUNCTION meter_attempt_snapshot_present() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM attempt_meter_snapshots WHERE attempt_id=NEW.id AND tenant_id=NEW.tenant_id AND request_id=NEW.request_id) THEN
  RAISE EXCEPTION 'attempt requires one meter snapshot';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_snapshot_present AFTER INSERT ON upstream_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_snapshot_present();

CREATE FUNCTION meter_attempt_matches_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM request_meter_snapshots r
  WHERE r.tenant_id=NEW.tenant_id AND r.request_id=NEW.request_id AND r.model_id=NEW.model_id
    AND r.profile_version=NEW.profile_version AND r.schema_id=NEW.schema_id AND r.schema_digest=NEW.schema_digest
    AND r.variant_key=NEW.variant_key AND r.input_bound=NEW.input_bound AND r.output_bound=NEW.output_bound
 ) THEN
  RAISE EXCEPTION 'attempt quote does not match request';
 END IF;
 IF NEW.cost_version_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM upstream_attempts a JOIN cost_versions c ON c.id=NEW.cost_version_id
  WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id
   AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
   AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest AND c.variant_key=NEW.variant_key
   AND c.basis=NEW.cost_basis
 ) THEN
  RAISE EXCEPTION 'attempt quote domain does not match original attempt';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_matches_request AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_matches_request();

ALTER TABLE upstream_attempts ADD CONSTRAINT upstream_attempts_cost_projection CHECK (
 (cost_status='unknown' AND cost_amount IS NULL)
 OR (cost_status IN ('contract','invoice') AND cost_amount IS NOT NULL)
);

ALTER TABLE metering_events
 ADD COLUMN profile_version TEXT,
 ADD COLUMN schema_id TEXT,
 ADD COLUMN schema_digest BYTEA,
 ADD COLUMN variant_key TEXT DEFAULT 'default',
 ADD COLUMN retail_usage_state TEXT,
 ADD COLUMN supplier_usage_state TEXT,
 ADD COLUMN created_xid XID8 DEFAULT pg_current_xact_id(),
 ADD COLUMN plan_digest BYTEA,
 ADD COLUMN protocol_outcome TEXT,
 ADD COLUMN transport_end_reason TEXT,
 ADD COLUMN delivery_stage TEXT,
 ADD COLUMN content_written BOOLEAN,
 ADD COLUMN finish_reason TEXT,
 ADD COLUMN observation_canonical BYTEA,
 ADD COLUMN observation_digest BYTEA;
ALTER TABLE metering_events DROP COLUMN normalized_usage, DROP COLUMN schema_version;
ALTER TABLE metering_events
 ALTER COLUMN attempt_id SET NOT NULL,
 ADD CHECK(retail_usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CHECK(supplier_usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CHECK(variant_key IS NULL OR variant_key='default'),
 ADD CHECK(source IN ('adapter','retrieval','gateway','approved_reconciliation')),
 ADD CHECK(protocol_outcome IN ('completed','failed','incomplete','cancelled','unknown')),
 ADD CHECK(transport_end_reason IN ('done','eof','provider_error','platform_error','platform_timeout','client_cancelled','connection_error','unknown','not_sent','rejected_no_consumption')),
 ADD CHECK(delivery_stage IN ('none','committed','content','unknown')),
 ADD CHECK(content_written IS NOT NULL AND content_written=(delivery_stage='content')),
 ADD CHECK(finish_reason IS NOT NULL AND octet_length(finish_reason)<=80),
 ADD CHECK(octet_length(schema_digest)=32),
 ADD CHECK(octet_length(plan_digest)=32),
 ADD CHECK(octet_length(observation_canonical) BETWEEN 1 AND 32768),
 ADD CHECK(octet_length(observation_digest)=32),
 ADD UNIQUE(tenant_id, id);

CREATE TABLE meter_event_quantities (
 tenant_id TEXT NOT NULL,
 event_id TEXT NOT NULL,
 dimension TEXT NOT NULL CHECK(dimension IN ('text_input_token','text_output_token')),
 unit TEXT NOT NULL CHECK(unit='token'),
 status TEXT NOT NULL CHECK(status IN ('confirmed_final','provisional','missing','conflicting')),
 quantity BIGINT CHECK(quantity>=0),
 PRIMARY KEY(event_id, dimension),
 FOREIGN KEY(tenant_id, event_id) REFERENCES metering_events(tenant_id, id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((status IN ('confirmed_final','provisional') AND quantity IS NOT NULL) OR (status IN ('missing','conflicting') AND quantity IS NULL))
);
CREATE TRIGGER immutable_meter_quantity BEFORE UPDATE OR DELETE ON meter_event_quantities FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

ALTER TABLE metering_event_conflicts ADD COLUMN tenant_id TEXT, ADD COLUMN candidate_canonical BYTEA, ADD COLUMN candidate_digest BYTEA;
ALTER TABLE metering_event_conflicts DROP COLUMN normalized_usage;

CREATE FUNCTION meter_usage_state(status text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE status WHEN 'estimated' THEN 'provisional' WHEN 'confirmed_final' THEN 'confirmed_final' WHEN 'provisional' THEN 'provisional' WHEN 'missing' THEN 'missing' WHEN 'conflicting' THEN 'conflicting' ELSE NULL END
$$;

CREATE FUNCTION meter_observation_quantity(status text, quantity bigint) RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN status IN ('confirmed_final','provisional') THEN to_json(quantity::text)::text ELSE 'null' END
$$;
CREATE FUNCTION meter_observation_identity_canonical(
 tenant_id text, request_id text, attempt_id text, source text, source_event_id text,
 profile_version text, schema_id text, schema_digest bytea, plan_digest bytea,
 protocol_outcome text, transport_end_reason text, delivery_stage text,
 content_written boolean, finish_reason text, unexpected_charge_reason text,
 input_status text, input_quantity bigint, output_status text, output_quantity bigint
) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT convert_to(format(
  '{"version":"1","tenant_id":%s,"request_id":%s,"attempt_id":%s,"source":%s,"source_event_id":%s,"profile_version":%s,"schema_id":%s,"schema_digest":%s,"variant_key":"default","plan_digest":%s,"provider_reference":null,"observed_at":null,"protocol_outcome":%s,"transport_end_reason":%s,"delivery_stage":%s,"content_written":%s,"finish_reason":%s,"quantities":[{"dimension":"text_input_token","unit":"token","status":%s,"quantity":%s},{"dimension":"text_output_token","unit":"token","status":%s,"quantity":%s}],"unexpected_charge_reason":%s}',
  to_json(tenant_id)::text, to_json(request_id)::text, to_json(attempt_id)::text, to_json(source)::text, to_json(source_event_id)::text,
  to_json(profile_version)::text, to_json(schema_id)::text, to_json(encode(schema_digest,'hex'))::text, to_json(encode(plan_digest,'hex'))::text,
  to_json(protocol_outcome)::text, to_json(transport_end_reason)::text, to_json(delivery_stage)::text, content_written,
  to_json(finish_reason)::text, to_json(input_status)::text, meter_observation_quantity(input_status,input_quantity),
  to_json(output_status)::text, meter_observation_quantity(output_status,output_quantity), coalesce(to_json(unexpected_charge_reason)::text,'null')), 'UTF8')
$$;
CREATE FUNCTION meter_observation_canonical(
 tenant_id text, request_id text, attempt_id text, source text, source_event_id text,
 profile_version text, schema_id text, schema_digest bytea, plan_digest bytea,
 observed_at timestamptz, protocol_outcome text, transport_end_reason text, delivery_stage text,
 content_written boolean, finish_reason text, unexpected_charge_reason text,
 input_status text, input_quantity bigint, output_status text, output_quantity bigint
) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT convert_to(replace(convert_from(meter_observation_identity_canonical(tenant_id,request_id,attempt_id,source,source_event_id,profile_version,schema_id,schema_digest,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,unexpected_charge_reason,input_status,input_quantity,output_status,output_quantity),'UTF8'),
  '"observed_at":null', '"observed_at":' || to_json(to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))::text), 'UTF8')
$$;
CREATE FUNCTION meter_observation_digest(schema_digest bytea, identity_canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.observation.v1','UTF8'), schema_digest, identity_canonical]))
$$;

CREATE FUNCTION meter_record_event_quantities(event_id text, tenant_id text, status text, input_tokens bigint, output_tokens bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE mapped text;
BEGIN
 mapped := meter_usage_state(status);
 IF mapped IS NULL OR input_tokens < 0 OR output_tokens < 0 THEN RAISE EXCEPTION 'invalid text usage'; END IF;
 IF mapped IN ('missing','conflicting') THEN
  input_tokens := NULL; output_tokens := NULL;
 END IF;
 INSERT INTO meter_event_quantities(tenant_id,event_id,dimension,unit,status,quantity) VALUES
  (meter_record_event_quantities.tenant_id, meter_record_event_quantities.event_id, 'text_input_token', 'token', mapped, input_tokens),
  (meter_record_event_quantities.tenant_id, meter_record_event_quantities.event_id, 'text_output_token', 'token', mapped, output_tokens);
END $$;

CREATE FUNCTION meter_event_quantity(event_id text, dimension text) RETURNS bigint LANGUAGE sql STABLE AS $$
 SELECT quantity FROM meter_event_quantities WHERE event_id=meter_event_quantity.event_id AND dimension=meter_event_quantity.dimension
$$;
ALTER TABLE platform_probe_attempts DROP COLUMN normalized_usage;
ALTER TABLE platform_probe_attempts
 ADD COLUMN schema_id TEXT,
 ADD COLUMN schema_digest BYTEA,
 ADD COLUMN variant_key TEXT DEFAULT 'default',
 ADD COLUMN usage_state TEXT,
 ADD COLUMN quantities_canonical BYTEA,
 ADD COLUMN quantities_digest BYTEA,
 ADD CHECK(variant_key IS NULL OR variant_key='default'),
 ADD CHECK(usage_state IS NULL OR usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CHECK((usage_state='confirmed_final' AND quantities_canonical IS NOT NULL AND quantities_digest IS NOT NULL) OR (usage_state IS DISTINCT FROM 'confirmed_final' AND quantities_canonical IS NULL AND quantities_digest IS NULL));

CREATE FUNCTION meter_side_quantity_canonical(schema_id text, side text, input_tokens bigint, output_tokens bigint) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT convert_to(format('{"schema_id":"%s","version":"1","side":"%s","quantities":[{"dimension":"text_input_token","unit":"token","quantity":"%s"},{"dimension":"text_output_token","unit":"token","quantity":"%s"}]}', schema_id, side, input_tokens, output_tokens), 'UTF8')
$$;
CREATE FUNCTION meter_quantities_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.quantities.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;
CREATE FUNCTION meter_final_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.final.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;
CREATE FUNCTION meter_quantity_canonical(schema_id text, input_tokens bigint, output_tokens bigint) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT meter_side_quantity_canonical(schema_id, 'supplier', input_tokens, output_tokens)
$$;

CREATE FUNCTION meter_reconciliation_proposal_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE sid text; sd bytea; canonical bytea; input_quantity bigint; output_quantity bigint; input_max bigint; output_max bigint; value jsonb;
BEGIN
 IF NEW.state='open' THEN
  IF NEW.proposed_observation_canonical IS NOT NULL OR NEW.proposed_observation_digest IS NOT NULL THEN
   RAISE EXCEPTION 'open reconciliation cannot carry a proposal';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.state='closed' AND NEW.proposed_observation_canonical IS NULL AND NEW.proposed_observation_digest IS NULL THEN
  RETURN NEW;
 END IF;
 IF NEW.state NOT IN ('proposed','approved','closed') THEN
  RAISE EXCEPTION 'reconciliation state is invalid';
 END IF;
 IF NEW.proposed_observation_canonical IS NULL OR NEW.proposed_observation_digest IS NULL THEN
  RAISE EXCEPTION 'reconciliation proposal vector is incomplete';
 END IF;
 SELECT s.schema_id,s.schema_digest INTO sid,sd FROM request_meter_snapshots s
  WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
 IF sid IS NULL OR octet_length(sd)<>32 THEN RAISE EXCEPTION 'reconciliation request schema is missing'; END IF;
 SELECT max(quantity) FILTER (WHERE dimension='text_input_token'),max(quantity) FILTER (WHERE dimension='text_output_token')
  INTO input_max,output_max FROM request_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id AND side='retail';
 value := convert_from(NEW.proposed_observation_canonical,'UTF8')::jsonb;
 input_quantity := (value->'quantities'->0->>'quantity')::bigint;
 output_quantity := (value->'quantities'->1->>'quantity')::bigint;
 IF input_quantity IS NULL OR output_quantity IS NULL OR input_quantity<0 OR output_quantity<0 OR input_max IS NULL OR output_max IS NULL
    OR input_quantity>input_max OR output_quantity>output_max THEN
  RAISE EXCEPTION 'reconciliation proposal quantity exceeds the request schema';
 END IF;
 canonical := meter_side_quantity_canonical(sid,'retail',input_quantity,output_quantity);
 IF NEW.proposed_observation_canonical IS DISTINCT FROM canonical
    OR NEW.proposed_observation_digest IS DISTINCT FROM meter_quantities_digest(sd,'retail',canonical) THEN
  RAISE EXCEPTION 'reconciliation proposal vector mismatch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER meter_reconciliation_proposal_complete BEFORE INSERT OR UPDATE ON reconciliation_cases
 FOR EACH ROW EXECUTE FUNCTION meter_reconciliation_proposal_complete();
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_quantities_digest(bytea,text,bytea) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.meter_reconciliation_proposal_complete() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_quantities_digest(bytea,text,bytea), meter_reconciliation_proposal_complete() FROM PUBLIC;

CREATE FUNCTION meter_event_rows_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n integer; matched integer;
BEGIN
 SELECT count(*), count(*) FILTER (WHERE status=NEW.retail_usage_state AND status=NEW.supplier_usage_state AND unit='token')
  INTO n, matched FROM meter_event_quantities WHERE event_id=NEW.id;
 IF NEW.retail_usage_state IS NULL OR NEW.retail_usage_state IS DISTINCT FROM NEW.supplier_usage_state OR n<>2 OR matched<>2
    OR NOT EXISTS (SELECT 1 FROM meter_event_quantities WHERE event_id=NEW.id AND dimension='text_input_token')
    OR NOT EXISTS (SELECT 1 FROM meter_event_quantities WHERE event_id=NEW.id AND dimension='text_output_token') THEN
  RAISE EXCEPTION 'meter event quantities are incomplete';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_event_rows_complete AFTER INSERT ON metering_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_event_rows_complete();

CREATE FUNCTION meter_event_observation_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE input_status text; output_status text; input_quantity bigint; output_quantity bigint; expected bytea; identity bytea;
BEGIN
 SELECT qi.status, qo.status, qi.quantity, qo.quantity INTO input_status, output_status, input_quantity, output_quantity
  FROM meter_event_quantities qi JOIN meter_event_quantities qo ON qo.event_id=qi.event_id AND qo.dimension='text_output_token'
  WHERE qi.event_id=NEW.id AND qi.dimension='text_input_token';
 expected := meter_observation_canonical(NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.source,NEW.source_event_id,NEW.profile_version,NEW.schema_id,NEW.schema_digest,NEW.plan_digest,NEW.happened_at,NEW.protocol_outcome,NEW.transport_end_reason,NEW.delivery_stage,NEW.content_written,NEW.finish_reason,NULL,input_status,input_quantity,output_status,output_quantity);
 identity := meter_observation_identity_canonical(NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.source,NEW.source_event_id,NEW.profile_version,NEW.schema_id,NEW.schema_digest,NEW.plan_digest,NEW.protocol_outcome,NEW.transport_end_reason,NEW.delivery_stage,NEW.content_written,NEW.finish_reason,NULL,input_status,input_quantity,output_status,output_quantity);
 IF NEW.observation_canonical IS DISTINCT FROM expected OR NEW.observation_digest IS DISTINCT FROM meter_observation_digest(NEW.schema_digest,identity) THEN
  RAISE EXCEPTION 'inference observation canonical mismatch';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_event_observation_complete AFTER INSERT ON metering_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_event_observation_complete();

CREATE FUNCTION meter_event_attempt_bound() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.attempt_id IS NULL THEN
  RAISE EXCEPTION 'observation requires the original attempt';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM upstream_attempts a WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id
 ) THEN
  RAISE EXCEPTION 'observation attempt belongs to another request';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_event_attempt_bound AFTER INSERT ON metering_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_event_attempt_bound();

CREATE FUNCTION meter_cost_lines_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cid text; basis text; sid text; sdigest bytea; stored bytea; stored_digest bytea; n integer; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea;
BEGIN
 IF TG_TABLE_NAME='cost_versions' THEN cid := NEW.id; ELSE cid := NEW.cost_version_id; END IF;
 SELECT c.basis, c.schema_id, c.schema_digest, c.rates_canonical, c.rates_digest, (SELECT count(*) FROM cost_rate_lines l WHERE l.cost_version_id=c.id)
  INTO basis, sid, sdigest, stored, stored_digest, n FROM cost_versions c WHERE c.id=cid;
 IF NOT EXISTS (SELECT 1 FROM model_capability_versions c JOIN model_capability_variants v ON v.profile_version=c.id
  WHERE c.schema_id=sid AND c.schema_digest=sdigest AND v.variant_key='default') THEN
  RAISE EXCEPTION 'supplier quote requires published text schema';
 END IF;
 IF basis='unknown' THEN
  IF n<>0 OR stored IS NOT NULL OR stored_digest IS NOT NULL THEN RAISE EXCEPTION 'unknown supplier quote has rates'; END IF;
  RETURN NULL;
 END IF;
 IF basis NOT IN ('contract','invoice') OR n<>2 THEN RAISE EXCEPTION 'known supplier quote is incomplete'; END IF;
 SELECT l.numerator, l.denominator INTO in_n, in_d FROM cost_rate_lines l WHERE l.cost_version_id=cid AND l.dimension='text_input_token' AND l.unit='token';
 SELECT l.numerator, l.denominator INTO out_n, out_d FROM cost_rate_lines l WHERE l.cost_version_id=cid AND l.dimension='text_output_token' AND l.unit='token';
 IF out_n<>0 AND EXISTS (SELECT 1 FROM meter_schema_dimensions d WHERE d.schema_id=sid AND d.dimension='text_output_token' AND d.maximum_quantity=0) THEN
  RAISE EXCEPTION 'input-only cost requires zero output rate';
 END IF;
 canonical := meter_rate_canonical(sid, 'supplier', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 IF in_n IS NULL OR out_n IS NULL OR stored IS DISTINCT FROM canonical OR stored_digest IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'known supplier quote is incomplete';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_cost_root_complete AFTER INSERT ON cost_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_cost_lines_complete();
CREATE CONSTRAINT TRIGGER meter_cost_line_complete AFTER INSERT ON cost_rate_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_cost_lines_complete();

CREATE FUNCTION meter_finish_probe(probe_id text, protocol_status text, usage_status text, input_tokens bigint, output_tokens bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE mapped text; sid text; sdigest bytea; canonical bytea; digest bytea;
BEGIN
 mapped := meter_usage_state(usage_status);
 IF mapped IS NULL OR protocol_status NOT IN ('sending','completed','failed','unknown') THEN RAISE EXCEPTION 'invalid probe result'; END IF;
 SELECT c.schema_id, c.schema_digest INTO sid, sdigest
  FROM platform_probe_attempts p JOIN models m ON m.id=p.model_id JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id
  WHERE p.id=meter_finish_probe.probe_id;
 IF sid IS NULL THEN RAISE EXCEPTION 'probe model schema missing'; END IF;
 IF mapped='confirmed_final' THEN
  IF input_tokens < 0 OR output_tokens < 0 THEN RAISE EXCEPTION 'invalid probe quantities'; END IF;
  canonical := meter_quantity_canonical(sid, input_tokens, output_tokens);
  digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.quantities.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 ELSE
  canonical := NULL; digest := NULL;
 END IF;
 UPDATE platform_probe_attempts SET status=protocol_status, usage_state=mapped, schema_id=sid, schema_digest=sdigest, variant_key='default', quantities_canonical=canonical, quantities_digest=digest, completed_at=clock_timestamp()
  WHERE id=meter_finish_probe.probe_id;
END $$;

ALTER TABLE finalization_records
 ADD COLUMN attempt_id TEXT,
 ADD COLUMN retail_usage_state TEXT NOT NULL,
 ADD COLUMN supplier_usage_state TEXT NOT NULL,
 ADD COLUMN retail_quantities_canonical BYTEA,
 ADD COLUMN retail_quantities_digest BYTEA,
 ADD COLUMN supplier_quantities_canonical BYTEA,
 ADD COLUMN supplier_quantities_digest BYTEA,
 ADD CONSTRAINT finalization_retail_state CHECK(retail_usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CONSTRAINT finalization_supplier_state CHECK(supplier_usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CONSTRAINT finalization_retail_projection CHECK(
  (retail_usage_state='confirmed_final' AND retail_quantities_canonical IS NOT NULL AND octet_length(retail_quantities_canonical)<=8192 AND octet_length(retail_quantities_digest)=32)
  OR (retail_usage_state<>'confirmed_final' AND retail_quantities_canonical IS NULL AND retail_quantities_digest IS NULL)),
 ADD CONSTRAINT finalization_supplier_projection CHECK(
  (supplier_usage_state='confirmed_final' AND supplier_quantities_canonical IS NOT NULL AND octet_length(supplier_quantities_canonical)<=8192 AND octet_length(supplier_quantities_digest)=32)
  OR (supplier_usage_state<>'confirmed_final' AND supplier_quantities_canonical IS NULL AND supplier_quantities_digest IS NULL)),
 ADD UNIQUE(tenant_id, id),
 ADD UNIQUE(tenant_id, request_id, id),
 ADD UNIQUE(tenant_id, request_id, attempt_id, id),
 ADD UNIQUE(tenant_id, id, attempt_id);
ALTER TABLE upstream_attempts
 ADD CONSTRAINT upstream_attempts_cost_finalization_fk
 FOREIGN KEY(tenant_id, latest_cost_finalization_id, id)
 REFERENCES finalization_records(tenant_id, id, attempt_id) DEFERRABLE INITIALLY DEFERRED,
 ADD CONSTRAINT upstream_attempts_cost_correction_fk
 FOREIGN KEY(tenant_id, latest_cost_correction_id, id)
 REFERENCES provider_cost_corrections(tenant_id, id, attempt_id) DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION meter_finalization_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e metering_events%ROWTYPE; in_q bigint; out_q bigint; retail_state text; supplier_state text;
 retail_canonical bytea; retail_digest bytea; supplier_canonical bytea; supplier_digest bytea;
BEGIN
 SELECT * INTO e FROM metering_events WHERE id=NEW.event_id;
 IF NOT FOUND OR e.tenant_id IS DISTINCT FROM NEW.tenant_id OR e.request_id IS DISTINCT FROM NEW.request_id
    OR e.attempt_id IS DISTINCT FROM NEW.attempt_id THEN
  RAISE EXCEPTION 'finalization does not match event';
 END IF;
 SELECT quantity INTO in_q FROM meter_event_quantities WHERE event_id=e.id AND dimension='text_input_token';
 SELECT quantity INTO out_q FROM meter_event_quantities WHERE event_id=e.id AND dimension='text_output_token';
 retail_state := e.retail_usage_state;
 supplier_state := e.supplier_usage_state;
 IF e.source='approved_reconciliation' AND supplier_state IS DISTINCT FROM 'conflicting' THEN
  supplier_state := 'missing';
 END IF;
 IF retail_state='confirmed_final' THEN
  retail_canonical := meter_side_quantity_canonical(e.schema_id, 'retail', in_q, out_q);
  retail_digest := meter_final_digest(e.schema_digest, 'retail', retail_canonical);
 END IF;
 IF supplier_state='confirmed_final' THEN
  supplier_canonical := meter_side_quantity_canonical(e.schema_id, 'supplier', in_q, out_q);
  supplier_digest := meter_final_digest(e.schema_digest, 'supplier', supplier_canonical);
 END IF;
 IF NEW.retail_usage_state IS DISTINCT FROM retail_state OR NEW.supplier_usage_state IS DISTINCT FROM supplier_state
    OR NEW.retail_quantities_canonical IS DISTINCT FROM retail_canonical OR NEW.retail_quantities_digest IS DISTINCT FROM retail_digest
    OR NEW.supplier_quantities_canonical IS DISTINCT FROM supplier_canonical OR NEW.supplier_quantities_digest IS DISTINCT FROM supplier_digest THEN
  RAISE EXCEPTION 'finalization does not match event';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_finalization_complete AFTER INSERT ON finalization_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_finalization_complete();

CREATE FUNCTION meter_request_finalization_pointer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.latest_finalization_id IS NOT DISTINCT FROM OLD.latest_finalization_id THEN RETURN NULL; END IF;
 IF NEW.latest_finalization_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM finalization_records f WHERE f.id=NEW.latest_finalization_id AND f.tenant_id=NEW.tenant_id AND f.request_id=NEW.id
 ) THEN RAISE EXCEPTION 'request finalization does not match request'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_request_finalization_pointer AFTER UPDATE ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_finalization_pointer();

CREATE FUNCTION meter_settlement_finalization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE retail text;
BEGIN
 IF NEW.finalization_id IS NULL THEN
  IF NEW.reason='charged_usage' THEN RAISE EXCEPTION 'charged usage has no finalization'; END IF;
  RETURN NULL;
 END IF;
 SELECT retail_usage_state INTO retail FROM finalization_records WHERE id=NEW.finalization_id AND tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'settlement finalization does not match request'; END IF;
 IF NEW.reason='charged_usage' AND retail IS DISTINCT FROM 'confirmed_final' THEN
  RAISE EXCEPTION 'charged usage is not a confirmed retail final';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_settlement_finalization AFTER INSERT ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_settlement_finalization();

CREATE FUNCTION meter_token_amount(input_qty bigint, output_qty bigint, in_n bigint, in_d bigint, out_n bigint, out_d bigint) RETURNS numeric LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE
  WHEN input_qty IS NULL OR output_qty IS NULL OR in_n IS NULL OR in_d IS NULL OR out_n IS NULL OR out_d IS NULL THEN NULL
  WHEN input_qty < 0 OR output_qty < 0 OR in_n < 0 OR out_n < 0 OR in_d < 1 OR out_d < 1 THEN NULL
  ELSE div(input_qty::numeric * in_n * out_d + output_qty::numeric * out_n * in_d + in_d::numeric * out_d - 1, in_d::numeric * out_d)
 END
$$;

-- Recompute the customer reservation ceiling from the immutable request
-- snapshot.  The application may calculate the same value for admission, but
-- the database is the final authority for the stored R.
CREATE FUNCTION meter_reservation_amount() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE expected numeric; input_qty bigint; output_qty bigint;
 in_n bigint; in_d bigint; out_n bigint; out_d bigint;
BEGIN
 SELECT s.input_bound,s.output_bound,ri.numerator,ri.denominator,ro.numerator,ro.denominator
   INTO input_qty,output_qty,in_n,in_d,out_n,out_d
   FROM request_meter_snapshots s
   JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
    AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
   JOIN price_rate_lines ri ON ri.price_version_id=p.id AND ri.dimension='text_input_token' AND ri.unit='token'
   JOIN price_rate_lines ro ON ro.price_version_id=p.id AND ro.dimension='text_output_token' AND ro.unit='token'
  WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'reservation meter snapshot is missing'; END IF;
 expected := meter_token_amount(input_qty,output_qty,in_n,in_d,out_n,out_d);
 IF expected IS NULL OR expected>9223372036854775807::numeric OR NEW.amount::numeric IS DISTINCT FROM expected THEN
  RAISE EXCEPTION 'reservation amount does not match original retail vector';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_reservation_amount
 AFTER INSERT ON reservations DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_reservation_amount();

-- Settlement facts are checked against the original finalization and price
-- roots.  A released outcome is deliberately zero-valued; it must not claim
-- that a missing/unknown supplier observation was free.
CREATE OR REPLACE FUNCTION meter_settlement_amount() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE expected numeric; charge numeric; reserved numeric; input_qty bigint; output_qty bigint;
 in_n bigint; in_d bigint; out_n bigint; out_d bigint; state text;
BEGIN
 IF NEW.reason<>'charged_usage' THEN
  IF NEW.status<>'released' OR NEW.charged_amount<>0 OR NEW.raw_retail_amount<>0 OR NEW.waived_retail_amount<>0 THEN
   RAISE EXCEPTION 'released settlement must be zero-valued';
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.status<>'settled' OR NEW.finalization_id IS NULL THEN
  RAISE EXCEPTION 'charged usage settlement is incomplete';
 END IF;
 SELECT f.retail_usage_state,r.amount,qi.quantity,qo.quantity,ri.numerator,ri.denominator,ro.numerator,ro.denominator
   INTO state,reserved,input_qty,output_qty,in_n,in_d,out_n,out_d
   FROM finalization_records f
   JOIN metering_events e ON e.tenant_id=f.tenant_id AND e.id=f.event_id
    AND e.request_id=f.request_id AND e.attempt_id=f.attempt_id
   JOIN request_meter_snapshots s ON s.tenant_id=f.tenant_id AND s.request_id=f.request_id
   JOIN reservations r ON r.tenant_id=s.tenant_id AND r.request_id=s.request_id
   JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
    AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
   JOIN price_rate_lines ri ON ri.price_version_id=p.id AND ri.dimension='text_input_token' AND ri.unit='token'
   JOIN price_rate_lines ro ON ro.price_version_id=p.id AND ro.dimension='text_output_token' AND ro.unit='token'
   LEFT JOIN meter_event_quantities qi ON qi.tenant_id=e.tenant_id AND qi.event_id=e.id AND qi.dimension='text_input_token'
   LEFT JOIN meter_event_quantities qo ON qo.tenant_id=e.tenant_id AND qo.event_id=e.id AND qo.dimension='text_output_token'
  WHERE f.tenant_id=NEW.tenant_id AND f.request_id=NEW.request_id AND f.id=NEW.finalization_id;
 IF NOT FOUND OR state IS DISTINCT FROM 'confirmed_final' OR input_qty IS NULL OR output_qty IS NULL THEN
  RAISE EXCEPTION 'charged usage finalization is not a retail final';
 END IF;
 expected := meter_token_amount(input_qty,output_qty,in_n,in_d,out_n,out_d);
 IF expected IS NULL THEN RAISE EXCEPTION 'charged usage amount cannot be calculated'; END IF;
 charge := LEAST(expected,reserved::numeric);
 IF NEW.raw_retail_amount IS DISTINCT FROM expected
    OR NEW.charged_amount::numeric IS DISTINCT FROM charge
    OR NEW.waived_retail_amount IS DISTINCT FROM expected-charge THEN
  RAISE EXCEPTION 'settlement amount does not match original retail vector';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_settlement_amount
 AFTER INSERT ON settlements DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION meter_settlement_amount();
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_reservation_amount() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.meter_settlement_amount() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_reservation_amount(),meter_settlement_amount() FROM PUBLIC;

CREATE OR REPLACE FUNCTION meter_attempt_cost_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE corr_amount bigint; corr_basis text; in_q bigint; out_q bigint; in_n bigint; in_d bigint; out_n bigint; out_d bigint; basis text; version text; expected numeric; end_reason text; supplier_state text;
BEGIN
 IF NEW.cost_status='unknown' AND NEW.cost_amount IS NULL THEN RETURN NULL; END IF;
 IF TG_OP='UPDATE' AND OLD.latest_cost_correction_id IS NOT NULL AND NEW.latest_cost_finalization_id IS NOT NULL THEN
  RAISE EXCEPTION 'supplier cost correction is authoritative';
 END IF;
 IF NEW.latest_cost_correction_id IS NOT NULL THEN
  SELECT c.amount, c.basis INTO corr_amount, corr_basis FROM provider_cost_corrections c
   WHERE c.id=NEW.latest_cost_correction_id AND c.tenant_id=NEW.tenant_id AND c.attempt_id=NEW.id;
  IF NOT FOUND OR NEW.cost_amount IS DISTINCT FROM corr_amount::numeric OR NEW.cost_status IS DISTINCT FROM corr_basis THEN
   RAISE EXCEPTION 'supplier cost does not match correction';
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.latest_cost_finalization_id IS NOT NULL THEN
  SELECT qi.quantity, qo.quantity, s.cost_version_id, s.cost_basis, f.transport_end_reason, f.supplier_usage_state
   INTO in_q, out_q, version, basis, end_reason, supplier_state
   FROM finalization_records f
   LEFT JOIN meter_event_quantities qi ON qi.event_id=f.event_id AND qi.dimension='text_input_token'
   LEFT JOIN meter_event_quantities qo ON qo.event_id=f.event_id AND qo.dimension='text_output_token'
   LEFT JOIN attempt_meter_snapshots s ON s.attempt_id=f.attempt_id
   WHERE f.id=NEW.latest_cost_finalization_id AND f.tenant_id=NEW.tenant_id AND f.attempt_id=NEW.id;
  IF NOT FOUND THEN RAISE EXCEPTION 'supplier cost finalization is missing'; END IF;
  IF supplier_state='confirmed_final' THEN
   SELECT l.numerator, l.denominator INTO in_n, in_d FROM cost_rate_lines l WHERE l.cost_version_id=version AND l.dimension='text_input_token';
   SELECT l.numerator, l.denominator INTO out_n, out_d FROM cost_rate_lines l WHERE l.cost_version_id=version AND l.dimension='text_output_token';
   expected := meter_token_amount(in_q, out_q, in_n, in_d, out_n, out_d);
   IF expected IS NOT NULL AND NEW.cost_amount = expected AND NEW.cost_status = basis THEN RETURN NULL; END IF;
  ELSIF NEW.cost_amount=0 AND NEW.cost_status='contract' AND end_reason IN ('not_sent','rejected_no_consumption') THEN
   RETURN NULL;
  END IF;
  RAISE EXCEPTION 'supplier cost does not match final usage';
 END IF;
 RAISE EXCEPTION 'supplier cost has no source pointer';
END $$;
CREATE CONSTRAINT TRIGGER meter_attempt_cost_matches AFTER UPDATE ON upstream_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_cost_matches();

CREATE FUNCTION meter_procurement_attempt_closed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_held numeric; event_spent numeric; proposal_amount bigint; proposal_basis text; proposal_op text; proposal_evidence text; stored_amount numeric; stored_status text;
BEGIN
 SELECT coalesce(sum(held_delta),0), coalesce(sum(spent_delta),0) INTO event_held, event_spent
  FROM procurement_budget_events WHERE attempt_id=NEW.attempt_id;
 IF event_held IS DISTINCT FROM NEW.held_amount OR event_spent IS DISTINCT FROM NEW.spent_amount THEN
  RAISE EXCEPTION 'procurement reservation does not match its budget events';
 END IF;
 SELECT p.amount, p.basis, p.approval_operation_id, p.evidence_ref
  INTO proposal_amount, proposal_basis, proposal_op, proposal_evidence
  FROM procurement_cost_proposals p
  WHERE p.attempt_id=NEW.attempt_id AND p.state='approved'
  ORDER BY p.approved_at DESC, p.id DESC
  LIMIT 1;
 IF NOT FOUND THEN
  IF NEW.state='settled' THEN
   SELECT a.cost_amount, a.cost_status INTO stored_amount, stored_status FROM upstream_attempts a WHERE a.id=NEW.attempt_id;
   IF stored_amount IS DISTINCT FROM NEW.spent_amount OR stored_status NOT IN ('contract','invoice') THEN
    RAISE EXCEPTION 'procurement reservation does not match its budget events';
   END IF;
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.spent_amount IS DISTINCT FROM proposal_amount::numeric THEN
  RAISE EXCEPTION 'procurement reservation does not match its budget events';
 END IF;
 IF NEW.state IN ('settled','disputed') AND (
  NOT EXISTS (
   SELECT 1 FROM provider_cost_corrections c
   WHERE c.attempt_id=NEW.attempt_id AND c.amount=proposal_amount AND c.basis=proposal_basis AND c.evidence_ref=proposal_evidence
     AND c.operation_id='procurement:'||proposal_op
  ) OR NOT EXISTS (
   SELECT 1 FROM procurement_budget_events e
   WHERE e.attempt_id=NEW.attempt_id AND e.event_key='correction:'||proposal_op AND e.type='cost_corrected' AND e.evidence_ref=proposal_evidence
  )
 ) THEN
  RAISE EXCEPTION 'procurement reservation does not match its budget events';
 END IF;
 IF NEW.state='settled' THEN
  SELECT a.cost_amount, a.cost_status INTO stored_amount, stored_status FROM upstream_attempts a WHERE a.id=NEW.attempt_id;
  IF stored_amount IS DISTINCT FROM proposal_amount::numeric OR stored_status IS DISTINCT FROM proposal_basis THEN
   RAISE EXCEPTION 'procurement reservation does not match its budget events';
  END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_procurement_attempt_closed AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_procurement_attempt_closed();

-- Account/event projection equality alone cannot prove that the original U
-- covers the original supplier contract. Recompute it independently on every
-- reservation birth/state change; never replace it with a later price/actual.
CREATE FUNCTION meter_procurement_bound_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected numeric;
BEGIN
 SELECT meter_token_amount(qi.quantity,qo.quantity,ri.numerator,ri.denominator,ro.numerator,ro.denominator)
 INTO expected
 FROM upstream_attempts a
 JOIN attempt_meter_snapshots s ON s.tenant_id=a.tenant_id AND s.request_id=a.request_id AND s.attempt_id=a.id
 JOIN cost_versions c ON c.id=s.cost_version_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
  AND c.schema_id=s.schema_id AND c.schema_digest=s.schema_digest AND c.variant_key=s.variant_key AND c.basis=s.cost_basis
 JOIN attempt_meter_bound_lines qi ON qi.tenant_id=s.tenant_id AND qi.attempt_id=s.attempt_id AND qi.side='supplier' AND qi.dimension='text_input_token' AND qi.unit='token'
 JOIN attempt_meter_bound_lines qo ON qo.tenant_id=s.tenant_id AND qo.attempt_id=s.attempt_id AND qo.side='supplier' AND qo.dimension='text_output_token' AND qo.unit='token'
 JOIN cost_rate_lines ri ON ri.cost_version_id=c.id AND ri.dimension='text_input_token' AND ri.unit='token'
 JOIN cost_rate_lines ro ON ro.cost_version_id=c.id AND ro.dimension='text_output_token' AND ro.unit='token'
 WHERE a.id=NEW.attempt_id AND s.quote_status='known' AND c.basis IN ('contract','invoice')
  AND (SELECT count(*) FROM attempt_meter_bound_lines b WHERE b.attempt_id=s.attempt_id AND b.side='supplier')=2
  AND (SELECT count(*) FROM cost_rate_lines l WHERE l.cost_version_id=c.id)=2;
 IF expected IS NULL OR expected<0 OR expected>9223372036854775807 OR NEW.upper_amount IS DISTINCT FROM expected THEN
  RAISE EXCEPTION 'procurement upper amount does not match original supplier vector';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_procurement_bound_matches AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_procurement_bound_matches();
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.meter_procurement_bound_matches() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION meter_procurement_bound_matches() FROM PUBLIC;

CREATE FUNCTION meter_procurement_proposal_current() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE evidence bigint; rev bigint;
BEGIN
 IF NEW.state<>'approved' THEN RETURN NULL; END IF;
 SELECT count(*) INTO evidence FROM procurement_evidence_identities WHERE attempt_id=NEW.attempt_id;
 IF evidence IS DISTINCT FROM NEW.expected_evidence_count THEN
  RAISE EXCEPTION 'procurement proposal evidence is stale';
 END IF;
 SELECT revision INTO rev FROM procurement_reservations WHERE attempt_id=NEW.attempt_id;
 IF rev IS DISTINCT FROM NEW.expected_reservation_revision AND rev IS DISTINCT FROM NEW.expected_reservation_revision+1 THEN
  RAISE EXCEPTION 'procurement proposal revision is stale';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER meter_procurement_proposal_current AFTER INSERT OR UPDATE ON procurement_cost_proposals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_procurement_proposal_current();

REVOKE ALL ON FUNCTION meter_frames(bytea[]), meter_gcd(bigint,bigint), meter_schema_stamp(), meter_schema_complete(), meter_text_schema(bigint,bigint), meter_insert_text_model(text,text,jsonb,text), meter_model_profile(text), meter_rate_canonical(text,text,bigint,bigint,bigint,bigint), meter_price_complete(), meter_reduce_million(bigint), meter_insert_token_price(text,text,bigint,bigint,timestamptz,text,text,text,timestamptz), meter_insert_token_cost(text,text,text,text,bigint,bigint,text,timestamptz,text,text,timestamptz), meter_insert_token_cap(text,text,bigint), meter_route_complete(), meter_request_snapshot_present(), meter_plan_ready(text,text), meter_profile_canonical(text,text,text,text,bytea,bytea,bigint,bigint), meter_profile_digest(bytea,bytea), meter_bounds_canonical(text,text,bigint,bigint), meter_bounds_digest(bytea,text,bytea), meter_rates_digest(bytea,text,bytea), meter_pure_quote_digest(bytea,text,bytea,bytea), meter_request_vectors(), meter_request_vectors_match(), meter_request_plan_canonical(text,text,text,bytea,bigint,boolean,text), meter_attempt_plan_canonical(text,text,text,bytea,bigint,boolean,text), meter_plan_digest(bytea,bytea), meter_request_plan_matches(), meter_attempt_plan_matches(), meter_request_price_matches(), meter_insert_attempt_snapshot(text,text,text,text,text), meter_attempt_snapshot_present(), meter_attempt_matches_request(), meter_usage_state(text), meter_record_event_quantities(text,text,text,bigint,bigint), meter_event_quantity(text,text), meter_side_quantity_canonical(text,text,bigint,bigint), meter_quantities_digest(bytea,text,bytea), meter_final_digest(bytea,text,bytea), meter_quantity_canonical(text,bigint,bigint), meter_reconciliation_proposal_complete(), meter_event_rows_complete(), meter_event_attempt_bound(), meter_cost_lines_complete(), meter_finish_probe(text,text,text,bigint,bigint), meter_finalization_complete(), meter_request_finalization_pointer(), meter_settlement_finalization(), meter_token_amount(bigint,bigint,bigint,bigint,bigint,bigint), meter_attempt_cost_matches(), meter_procurement_attempt_closed(), meter_procurement_proposal_current(), meter_request_bounds_complete(), meter_attempt_bounds_complete(), meter_quote_digest(bytea,bytea), meter_retail_quote_canonical(text,bytea,bytea,text,bigint,bigint), meter_supplier_quote_canonical(text,bytea,bytea,text,text,text,bytea,bigint,bigint), meter_request_quote_matches(), meter_attempt_quote_matches(), meter_attempt_vectors_match() FROM PUBLIC;

-- 028-S/A/R current storage minimum. These tables are intentionally appended
-- to 024. They contain only current identities and observations. They do not
-- introduce a second wallet, generation path, or historical backfill.
CREATE FUNCTION media_uuid(v text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND v <> '00000000-0000-0000-0000-000000000000', false)
$$;

CREATE TABLE media_tasks (
 tenant_id text NOT NULL,
 id text NOT NULL CHECK(media_uuid(id)),
 request_id text NOT NULL,
 owner_id text NOT NULL,
 attempt_id text,
 reservation_id text NOT NULL,
 source_id text NOT NULL,
 settlement_id text,
 model_id text NOT NULL,
 profile_version text NOT NULL,
 schema_id text NOT NULL,
 schema_digest bytea NOT NULL CHECK(octet_length(schema_digest)=32),
 variant_key text NOT NULL DEFAULT 'default' CHECK(variant_key='default'),
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
 submit_fingerprint bytea NOT NULL CHECK(octet_length(submit_fingerprint)=32),
 input_digest bytea NOT NULL CHECK(octet_length(input_digest)=32),
 status text NOT NULL DEFAULT 'prepared' CHECK(status IN ('prepared','submitting','queued','running','cancel_requested','submission_unknown','succeeded','failed','cancelled')),
 observation_state text NOT NULL DEFAULT 'unobserved' CHECK(observation_state IN ('unobserved','fresh','blocked','expired')),
 cancel_state text NOT NULL DEFAULT 'none' CHECK(cancel_state IN ('none','requested','accepted','rejected')),
 artifact_state text NOT NULL DEFAULT 'none' CHECK(artifact_state IN ('none','available','expired')),
 cancel_resume_state text CHECK(cancel_resume_state IS NULL OR cancel_resume_state IN ('queued','running','submission_unknown')),
 provider_reference text CHECK(provider_reference IS NULL OR provider_reference !~ '://'),
 lease_id text CHECK(lease_id IS NULL OR media_uuid(lease_id)),
 lease_worker_id text CHECK(lease_worker_id IS NULL OR media_uuid(lease_worker_id)),
 lease_kind text CHECK(lease_kind IS NULL OR lease_kind IN ('submit','observe','cancel','expire')),
 lease_until timestamptz(6) CHECK(lease_until IS NULL OR isfinite(lease_until)),
 submit_deadline timestamptz(6) NOT NULL CHECK(isfinite(submit_deadline)),
 observe_deadline timestamptz(6) NOT NULL CHECK(isfinite(observe_deadline)),
 result_deadline timestamptz(6) NOT NULL CHECK(isfinite(result_deadline)),
 artifact_deadline timestamptz(6) NOT NULL CHECK(isfinite(artifact_deadline)),
 max_artifact_bytes bigint NOT NULL CHECK(max_artifact_bytes>0),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id),
 UNIQUE(tenant_id,idempotency_key),
 UNIQUE(tenant_id,provider_reference),
 UNIQUE(tenant_id,id,request_id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,source_id) REFERENCES funding_receipts(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,owner_id) REFERENCES memberships(tenant_id,user_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,attempt_id,request_id) REFERENCES upstream_attempts(tenant_id,id,request_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(model_id) REFERENCES models(id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(profile_version) REFERENCES model_capability_versions(id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(schema_id,schema_digest) REFERENCES meter_schemas(id,schema_digest) DEFERRABLE INITIALLY DEFERRED,
 CHECK(submit_deadline<=observe_deadline AND result_deadline>=observe_deadline AND artifact_deadline>=result_deadline),
 CHECK((lease_id IS NULL AND lease_worker_id IS NULL AND lease_kind IS NULL AND lease_until IS NULL) OR (lease_id IS NOT NULL AND lease_worker_id IS NOT NULL AND lease_kind IS NOT NULL AND lease_until IS NOT NULL)),
 CHECK((status='cancel_requested')=(cancel_state='requested'))
);

CREATE FUNCTION media_task_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE request_owner text; request_model text; reservation_request text; attempt_request text; profile_model text; profile_schema text; profile_digest bytea;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.request_id,NEW.owner_id,NEW.attempt_id,NEW.reservation_id,NEW.source_id,NEW.model_id,NEW.profile_version,NEW.schema_id,NEW.schema_digest,NEW.variant_key,NEW.idempotency_key,NEW.submit_fingerprint,NEW.input_digest,NEW.submit_deadline,NEW.observe_deadline,NEW.result_deadline,NEW.artifact_deadline,NEW.max_artifact_bytes,NEW.created_at)
     IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.request_id,OLD.owner_id,OLD.attempt_id,OLD.reservation_id,OLD.source_id,OLD.model_id,OLD.profile_version,OLD.schema_id,OLD.schema_digest,OLD.variant_key,OLD.idempotency_key,OLD.submit_fingerprint,OLD.input_digest,OLD.submit_deadline,OLD.observe_deadline,OLD.result_deadline,OLD.artifact_deadline,OLD.max_artifact_bytes,OLD.created_at) THEN
   RAISE EXCEPTION 'media task identity is immutable';
  END IF;
  IF OLD.provider_reference IS NOT NULL AND NEW.provider_reference IS DISTINCT FROM OLD.provider_reference THEN
   RAISE EXCEPTION 'media provider reference is immutable';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
   (OLD.status='prepared' AND NEW.status='submitting') OR
   (OLD.status='submitting' AND NEW.status IN ('queued','submission_unknown')) OR
   (OLD.status='queued' AND NEW.status IN ('running','cancel_requested','failed','succeeded')) OR
   (OLD.status='running' AND NEW.status IN ('cancel_requested','failed','succeeded')) OR
   (OLD.status='submission_unknown' AND NEW.status IN ('queued','running','failed','succeeded','cancel_requested')) OR
   (OLD.status='cancel_requested' AND NEW.status IN ('queued','running','succeeded','failed','cancelled'))
  ) THEN RAISE EXCEPTION 'invalid media task state transition'; END IF;
  IF OLD.cancel_state='none' AND NEW.cancel_state NOT IN ('none','requested') THEN RAISE EXCEPTION 'invalid media cancel state'; END IF;
  IF OLD.cancel_state='requested' AND NEW.cancel_state NOT IN ('requested','accepted','rejected') THEN RAISE EXCEPTION 'invalid media cancel state'; END IF;
  IF OLD.cancel_state IN ('accepted','rejected') AND NEW.cancel_state IS DISTINCT FROM OLD.cancel_state THEN RAISE EXCEPTION 'media cancel state is terminal'; END IF;
  IF OLD.artifact_state='none' AND NEW.artifact_state NOT IN ('none','available') THEN RAISE EXCEPTION 'invalid media artifact state'; END IF;
  IF OLD.artifact_state='available' AND NEW.artifact_state NOT IN ('available','expired') THEN RAISE EXCEPTION 'invalid media artifact state'; END IF;
  IF OLD.artifact_state='expired' AND NEW.artifact_state IS DISTINCT FROM OLD.artifact_state THEN RAISE EXCEPTION 'media artifact state is terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.status<>'prepared' OR NEW.observation_state<>'unobserved' OR NEW.cancel_state<>'none' OR NEW.artifact_state<>'none' THEN
  RAISE EXCEPTION 'media task must be born prepared';
 END IF;
 SELECT owner_id,model_id INTO request_owner,request_model FROM request_records WHERE tenant_id=NEW.tenant_id AND id=NEW.request_id;
 SELECT request_id INTO reservation_request FROM reservations WHERE tenant_id=NEW.tenant_id AND id=NEW.reservation_id;
 IF NEW.attempt_id IS NOT NULL THEN
  SELECT request_id INTO attempt_request FROM upstream_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
 END IF;
 SELECT c.model_id,c.schema_id,c.schema_digest INTO profile_model,profile_schema,profile_digest FROM model_capability_versions c WHERE c.id=NEW.profile_version;
 IF request_owner IS NULL OR request_owner IS DISTINCT FROM NEW.owner_id OR request_model IS DISTINCT FROM NEW.model_id
    OR reservation_request IS DISTINCT FROM NEW.request_id OR (NEW.attempt_id IS NOT NULL AND attempt_request IS DISTINCT FROM NEW.request_id)
    OR profile_model IS DISTINCT FROM NEW.model_id OR profile_schema IS DISTINCT FROM NEW.schema_id OR profile_digest IS DISTINCT FROM NEW.schema_digest THEN
  RAISE EXCEPTION 'media task binding mismatch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_task_identity BEFORE INSERT OR UPDATE ON media_tasks FOR EACH ROW EXECUTE FUNCTION media_task_identity_guard();
CREATE TRIGGER media_task_immutable BEFORE DELETE ON media_tasks FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_task_artifacts (
 tenant_id text NOT NULL, task_id text NOT NULL, artifact_reference text NOT NULL CHECK(length(artifact_reference) BETWEEN 1 AND 512 AND artifact_reference !~ '://'),
 media_type text NOT NULL CHECK(media_type ~ '^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$'),
 byte_length bigint NOT NULL CHECK(byte_length>0), sha256 bytea NOT NULL CHECK(octet_length(sha256)=32),
 body bytea CHECK(body IS NULL OR (octet_length(body)=byte_length AND octet_length(body)<=67108864 AND sha256(body)=sha256)),
 expires_at timestamptz(6) NOT NULL CHECK(isfinite(expires_at)), status text NOT NULL DEFAULT 'available' CHECK(status IN ('available','expired')),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,task_id,artifact_reference),
 UNIQUE(tenant_id,task_id,sha256),
 FOREIGN KEY(tenant_id,task_id) REFERENCES media_tasks(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION media_artifact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE task_status text; task_max bigint; task_artifact_state text;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.task_id,NEW.artifact_reference,NEW.media_type,NEW.byte_length,NEW.sha256,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.task_id,OLD.artifact_reference,OLD.media_type,OLD.byte_length,OLD.sha256,OLD.created_at) THEN RAISE EXCEPTION 'media artifact identity is immutable'; END IF;
  IF OLD.body IS NOT NULL AND NEW.body IS DISTINCT FROM OLD.body THEN RAISE EXCEPTION 'media artifact body is immutable'; END IF;
  IF OLD.body IS NULL AND NEW.body IS NOT NULL AND (octet_length(NEW.body) IS DISTINCT FROM NEW.byte_length OR sha256(NEW.body) IS DISTINCT FROM NEW.sha256) THEN RAISE EXCEPTION 'media artifact body digest mismatch'; END IF;
  IF OLD.status='expired' AND NEW.status IS DISTINCT FROM OLD.status THEN RAISE EXCEPTION 'media artifact is terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.body IS NOT NULL THEN RAISE EXCEPTION 'media artifact body must be written after success'; END IF;
 SELECT status,max_artifact_bytes,artifact_state INTO task_status,task_max,task_artifact_state FROM media_tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 IF task_status IS DISTINCT FROM 'succeeded' OR task_artifact_state='expired' OR NEW.byte_length>task_max THEN RAISE EXCEPTION 'media artifact is not allowed'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_artifact_guard BEFORE INSERT OR UPDATE ON media_task_artifacts FOR EACH ROW EXECUTE FUNCTION media_artifact_guard();
CREATE TRIGGER media_artifact_immutable BEFORE DELETE ON media_task_artifacts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_task_events (
 tenant_id text NOT NULL, task_id text NOT NULL, event_id text NOT NULL CHECK(media_uuid(event_id)), sequence bigint NOT NULL CHECK(sequence>0),
 source text NOT NULL CHECK(source IN ('adapter','signed_callback','original_query','approved_reconciliation')),
 kind text NOT NULL CHECK(kind IN ('submission_accepted','submission_unknown','status_queued','status_running','status_succeeded','status_failed','status_cancelled','cancel_rejected','artifact_expired')),
 observed_at timestamptz(6) NOT NULL CHECK(isfinite(observed_at)), provider_reference text CHECK(provider_reference IS NULL OR provider_reference !~ '://'),
 artifact_reference text, artifact_media_type text, artifact_bytes bigint, artifact_sha256 bytea, artifact_expires_at timestamptz(6), failure_code text,
 canonical bytea NOT NULL CHECK(octet_length(canonical) BETWEEN 1 AND 16384), digest bytea NOT NULL CHECK(octet_length(digest)=32 AND digest=sha256(canonical)),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,task_id,event_id), UNIQUE(tenant_id,task_id,sequence),
 FOREIGN KEY(tenant_id,task_id) REFERENCES media_tasks(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION media_task_event_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text; obs text; cancel_s text; artifact_s text; prior text; task_provider text;
BEGIN
 SELECT status,observation_state,cancel_state,artifact_state,cancel_resume_state,provider_reference INTO s,obs,cancel_s,artifact_s,prior,task_provider FROM media_tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id FOR UPDATE;
 IF NEW.kind IN ('submission_accepted','status_queued') AND s<>'submitting' AND NOT (NEW.kind='status_queued' AND s='submission_unknown') THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='submission_unknown' AND s<>'submitting' THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='status_running' AND s NOT IN ('queued','submission_unknown') THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind IN ('status_succeeded','status_failed') AND s NOT IN ('queued','running','submission_unknown') THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='status_cancelled' AND s<>'cancel_requested' THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='cancel_rejected' AND s<>'cancel_requested' THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='artifact_expired' AND (s<>'succeeded' OR artifact_s<>'available') THEN RAISE EXCEPTION 'media artifact expiration mismatch'; END IF;
 IF NEW.kind IN ('submission_accepted','status_queued','status_running','status_succeeded') AND NEW.provider_reference IS NULL AND task_provider IS NULL THEN RAISE EXCEPTION 'media event requires provider reference'; END IF;
 IF NEW.kind='status_succeeded' AND (NEW.artifact_reference IS NULL OR NEW.artifact_media_type IS NULL OR NEW.artifact_bytes IS NULL OR NEW.artifact_sha256 IS NULL OR NEW.artifact_expires_at IS NULL) THEN RAISE EXCEPTION 'media success requires artifact'; END IF;
 IF NEW.kind='status_failed' AND (NEW.failure_code IS NULL OR NEW.failure_code !~ '^[a-z0-9_.:-]{1,64}$') THEN RAISE EXCEPTION 'media failure requires code'; END IF;
 IF NEW.kind<>'status_succeeded' AND (NEW.artifact_reference IS NOT NULL OR NEW.artifact_media_type IS NOT NULL OR NEW.artifact_bytes IS NOT NULL OR NEW.artifact_sha256 IS NOT NULL OR NEW.artifact_expires_at IS NOT NULL) THEN RAISE EXCEPTION 'media event artifact is unexpected'; END IF;
 IF NEW.kind<>'status_failed' AND NEW.failure_code IS NOT NULL THEN RAISE EXCEPTION 'media event failure is unexpected'; END IF;
 IF NEW.kind='submission_unknown' AND NEW.provider_reference IS NOT NULL THEN RAISE EXCEPTION 'unknown submission has provider reference'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION media_task_event_apply() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_status text; next_cancel text; prior text; ref text;
BEGIN
 SELECT cancel_resume_state,provider_reference INTO prior,ref FROM media_tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 next_status:=CASE NEW.kind WHEN 'submission_accepted' THEN 'queued' WHEN 'status_queued' THEN 'queued' WHEN 'status_running' THEN 'running' WHEN 'status_succeeded' THEN 'succeeded' WHEN 'status_failed' THEN 'failed' WHEN 'status_cancelled' THEN 'cancelled' WHEN 'submission_unknown' THEN 'submission_unknown' WHEN 'cancel_rejected' THEN coalesce(prior,'queued') ELSE NULL END;
 next_cancel:=CASE NEW.kind WHEN 'status_cancelled' THEN 'accepted' WHEN 'cancel_rejected' THEN 'rejected' ELSE NULL END;
 UPDATE media_tasks SET status=coalesce(next_status,status), cancel_state=coalesce(next_cancel,cancel_state), provider_reference=coalesce(provider_reference,NEW.provider_reference), observation_state='fresh'
  WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 IF NEW.kind='status_succeeded' THEN
  INSERT INTO media_task_artifacts(tenant_id,task_id,artifact_reference,media_type,byte_length,sha256,expires_at)
  VALUES(NEW.tenant_id,NEW.task_id,NEW.artifact_reference,NEW.artifact_media_type,NEW.artifact_bytes,NEW.artifact_sha256,NEW.artifact_expires_at);
  UPDATE media_tasks SET artifact_state='available' WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 ELSIF NEW.kind='artifact_expired' THEN
  UPDATE media_task_artifacts SET status='expired' WHERE tenant_id=NEW.tenant_id AND task_id=NEW.task_id AND status='available';
  UPDATE media_tasks SET artifact_state='expired' WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER media_task_event_guard BEFORE INSERT ON media_task_events FOR EACH ROW EXECUTE FUNCTION media_task_event_guard();
CREATE TRIGGER media_task_event_apply AFTER INSERT ON media_task_events FOR EACH ROW EXECUTE FUNCTION media_task_event_apply();
CREATE TRIGGER media_task_event_immutable BEFORE UPDATE OR DELETE ON media_task_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE realtime_sessions (
 tenant_id text NOT NULL, id text NOT NULL CHECK(media_uuid(id)), actor_id text NOT NULL, api_key_id text NOT NULL, model_id text NOT NULL, profile_version text NOT NULL, route_id text NOT NULL, origin text NOT NULL CHECK(origin ~ '^https?://[^/?#:@]+(:[0-9]+)?$'),
 state text NOT NULL DEFAULT 'opening' CHECK(state IN ('opening','open','draining','closed')), provider_session_id text CHECK(provider_session_id IS NULL OR length(provider_session_id) BETWEEN 1 AND 512), expires_at timestamptz(6) NOT NULL CHECK(isfinite(expires_at)), max_turns integer NOT NULL DEFAULT 100 CHECK(max_turns BETWEEN 1 AND 100), created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,id,model_id,profile_version,route_id),
 FOREIGN KEY(tenant_id,actor_id) REFERENCES memberships(tenant_id,user_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,api_key_id) REFERENCES api_keys(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(model_id) REFERENCES models(id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(profile_version) REFERENCES model_capability_versions(id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(route_id) REFERENCES route_policy_versions(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION realtime_session_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE profile_model text; route_model text;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.actor_id,NEW.api_key_id,NEW.model_id,NEW.profile_version,NEW.route_id,NEW.origin,NEW.expires_at,NEW.max_turns,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.actor_id,OLD.api_key_id,OLD.model_id,OLD.profile_version,OLD.route_id,OLD.origin,OLD.expires_at,OLD.max_turns,OLD.created_at) THEN RAISE EXCEPTION 'realtime session identity is immutable'; END IF;
  IF OLD.provider_session_id IS NOT NULL AND NEW.provider_session_id IS DISTINCT FROM OLD.provider_session_id THEN RAISE EXCEPTION 'realtime provider session identity is immutable'; END IF;
  IF NOT ((OLD.state='opening' AND NEW.state='open') OR (OLD.state='open' AND NEW.state='draining') OR (OLD.state='draining' AND NEW.state='closed') OR NEW.state=OLD.state) THEN RAISE EXCEPTION 'invalid realtime session state transition'; END IF;
  RETURN NEW;
 END IF;
 SELECT model_id INTO profile_model FROM model_capability_versions WHERE id=NEW.profile_version;
 SELECT model_id INTO route_model FROM route_policy_versions WHERE id=NEW.route_id;
 IF profile_model IS DISTINCT FROM NEW.model_id OR route_model IS DISTINCT FROM NEW.model_id THEN RAISE EXCEPTION 'realtime session binding mismatch'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER realtime_session_guard BEFORE INSERT OR UPDATE ON realtime_sessions FOR EACH ROW EXECUTE FUNCTION realtime_session_guard();
CREATE TRIGGER realtime_session_immutable BEFORE DELETE ON realtime_sessions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE realtime_turns (
 tenant_id text NOT NULL, id text NOT NULL CHECK(media_uuid(id)), session_id text NOT NULL, ordinal bigint NOT NULL CHECK(ordinal>0),
 request_id text NOT NULL, reservation_id text NOT NULL, attempt_id text NOT NULL, state text NOT NULL DEFAULT 'reserved' CHECK(state IN ('reserved','sending','receiving','finalizing','pending','settled','released')), provider_handle text CHECK(provider_handle IS NULL OR length(provider_handle) BETWEEN 1 AND 512), dispatch_unknown boolean NOT NULL DEFAULT false, cancel_requested boolean NOT NULL DEFAULT false,
 response_reference text, created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,session_id,ordinal), UNIQUE(tenant_id,id,session_id),
 CONSTRAINT realtime_turn_session_fk FOREIGN KEY(tenant_id,session_id) REFERENCES realtime_sessions(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION realtime_turn_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE session_state text; session_tenant text; reserved_request text; attempt_request text; reserved_media boolean := false; attempt_media boolean := false;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.session_id,NEW.ordinal,NEW.request_id,NEW.reservation_id,NEW.attempt_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.session_id,OLD.ordinal,OLD.request_id,OLD.reservation_id,OLD.attempt_id,OLD.created_at) THEN RAISE EXCEPTION 'realtime turn identity is immutable'; END IF;
  IF OLD.provider_handle IS NOT NULL AND NEW.provider_handle IS DISTINCT FROM OLD.provider_handle THEN RAISE EXCEPTION 'realtime provider handle is immutable'; END IF;
  IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='reserved' AND NEW.state IN ('sending','released')) OR (OLD.state='sending' AND NEW.state IN ('receiving','pending')) OR (OLD.state='receiving' AND NEW.state IN ('finalizing','pending')) OR (OLD.state='finalizing' AND NEW.state IN ('settled','pending')) OR (OLD.state='pending' AND NEW.state IN ('settled','released'))) THEN RAISE EXCEPTION 'invalid realtime turn state transition'; END IF;
  IF OLD.response_reference IS NOT NULL AND NEW.response_reference IS DISTINCT FROM OLD.response_reference THEN RAISE EXCEPTION 'realtime response identity is immutable'; END IF;
  RETURN NEW;
 END IF;
 SELECT state,tenant_id INTO session_state,session_tenant FROM realtime_sessions WHERE tenant_id=NEW.tenant_id AND id=NEW.session_id;
 SELECT request_id INTO reserved_request FROM reservations WHERE tenant_id=NEW.tenant_id AND id=NEW.reservation_id;
 IF NOT FOUND THEN
  SELECT q.id INTO reserved_request FROM media_reservations r JOIN media_requests q ON q.tenant_id=r.tenant_id AND q.id=r.request_id
   WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.reservation_id AND q.id=NEW.request_id AND q.operation='realtime_turn';
  reserved_media := FOUND;
 END IF;
 SELECT request_id INTO attempt_request FROM upstream_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
 IF NOT FOUND THEN
  SELECT request_id INTO attempt_request FROM media_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
  attempt_media := FOUND;
 END IF;
 IF session_state IS DISTINCT FROM 'open' OR session_tenant IS NULL OR reserved_request IS DISTINCT FROM NEW.request_id OR attempt_request IS DISTINCT FROM NEW.request_id OR reserved_media IS DISTINCT FROM attempt_media THEN RAISE EXCEPTION 'realtime turn binding mismatch'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION realtime_turn_inflight_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (SELECT count(*) FROM realtime_turns WHERE tenant_id=NEW.tenant_id AND session_id=NEW.session_id AND state NOT IN ('settled','released'))>1 THEN RAISE EXCEPTION 'realtime session has multiple inflight turns'; END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER realtime_turn_guard BEFORE INSERT OR UPDATE ON realtime_turns FOR EACH ROW EXECUTE FUNCTION realtime_turn_guard();
CREATE CONSTRAINT TRIGGER realtime_turn_inflight AFTER INSERT OR UPDATE ON realtime_turns DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION realtime_turn_inflight_guard();
CREATE TRIGGER realtime_turn_immutable BEFORE DELETE ON realtime_turns FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE realtime_events (
 tenant_id text NOT NULL, session_id text NOT NULL, event_id text NOT NULL CHECK(media_uuid(event_id)), turn_id text, type text NOT NULL CHECK(type IN ('input.text','input.audio','response.create','response.cancel','heartbeat','status')),
 payload_bytes integer NOT NULL CHECK(payload_bytes>=0 AND payload_bytes<=1048576), payload_digest bytea NOT NULL CHECK(octet_length(payload_digest)=32), canonical bytea NOT NULL CHECK(octet_length(canonical) BETWEEN 1 AND 16384),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)), PRIMARY KEY(tenant_id,session_id,event_id),
 FOREIGN KEY(tenant_id,session_id) REFERENCES realtime_sessions(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,turn_id,session_id) REFERENCES realtime_turns(tenant_id,id,session_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION realtime_event_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE session_state text; turn_state text;
BEGIN
 IF NEW.payload_digest IS DISTINCT FROM sha256(NEW.canonical) THEN RAISE EXCEPTION 'realtime event digest mismatch'; END IF;
 SELECT state INTO session_state FROM realtime_sessions WHERE tenant_id=NEW.tenant_id AND id=NEW.session_id;
 IF NEW.type IN ('heartbeat','status') AND NEW.turn_id IS NOT NULL THEN RAISE EXCEPTION 'realtime control event cannot bind turn'; END IF;
 IF NEW.type IN ('input.text','input.audio','response.create','response.cancel') AND NEW.turn_id IS NULL THEN RAISE EXCEPTION 'realtime event requires turn'; END IF;
 IF NEW.turn_id IS NOT NULL THEN SELECT state INTO turn_state FROM realtime_turns WHERE tenant_id=NEW.tenant_id AND id=NEW.turn_id AND session_id=NEW.session_id; IF turn_state IS NULL OR turn_state IN ('settled','released') THEN RAISE EXCEPTION 'realtime event turn is terminal'; END IF; END IF;
 IF session_state IS DISTINCT FROM 'open' AND NEW.type NOT IN ('heartbeat','status') THEN RAISE EXCEPTION 'realtime session is not open'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER realtime_event_guard BEFORE INSERT OR UPDATE ON realtime_events FOR EACH ROW EXECUTE FUNCTION realtime_event_guard();
CREATE TRIGGER realtime_event_immutable BEFORE UPDATE OR DELETE ON realtime_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.media_uuid(text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_task_identity_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_artifact_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_task_event_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_task_event_apply() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.realtime_session_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.realtime_turn_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.realtime_turn_inflight_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.realtime_event_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON TABLE media_tasks,media_task_events,media_task_artifacts,realtime_sessions,realtime_turns,realtime_events FROM PUBLIC;
REVOKE ALL ON FUNCTION media_uuid(text),media_task_identity_guard(),media_artifact_guard(),media_task_event_guard(),media_task_event_apply(),realtime_session_guard(),realtime_turn_guard(),realtime_turn_inflight_guard(),realtime_event_guard() FROM PUBLIC;

-- Current synchronous/asynchronous media commercial facts.  These tables use
-- the same wallet journals and funding lots as text, but keep media units
-- explicit instead of pretending that bytes or characters are text tokens.
CREATE TABLE media_model_profiles (
 model_key text PRIMARY KEY COLLATE "C" CHECK(length(model_key) BETWEEN 1 AND 256 AND model_key !~ '[[:space:]]'),
 operation text NOT NULL CHECK(operation IN ('image_generation','tts','video_generation','realtime_turn')),
 profile_version text NOT NULL CHECK(media_uuid(profile_version)),
 unit text NOT NULL CHECK(unit IN ('image','character','task','realtime_byte')),
 rate_per_million bigint NOT NULL CHECK(rate_per_million>=0),
 max_quantity bigint NOT NULL CHECK(max_quantity>0),
 max_result_bytes bigint NOT NULL CHECK(max_result_bytes>0),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(operation,profile_version), UNIQUE(model_key,operation,profile_version)
);
CREATE FUNCTION media_profile_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.model_key,NEW.operation,NEW.profile_version,NEW.unit,NEW.rate_per_million,NEW.max_quantity,NEW.max_result_bytes,NEW.created_at)
    IS DISTINCT FROM ROW(OLD.model_key,OLD.operation,OLD.profile_version,OLD.unit,OLD.rate_per_million,OLD.max_quantity,OLD.max_result_bytes,OLD.created_at) THEN
  RAISE EXCEPTION 'media model profile identity is immutable';
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'media model profile is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_profile_immutable BEFORE UPDATE OR DELETE ON media_model_profiles FOR EACH ROW EXECUTE FUNCTION media_profile_immutable();

INSERT INTO media_model_profiles(model_key,operation,profile_version,unit,rate_per_million,max_quantity,max_result_bytes)
VALUES
 ('mock-image-generation','image_generation','11111111-1111-4111-8111-111111111111','image',1000000,1,16777216),
 ('mock-tts','tts','22222222-2222-4222-8222-222222222222','character',1000000,16384,16777216),
 ('mock-video-generation','video_generation','33333333-3333-4333-8333-333333333333','task',1000000,1,67108864),
 ('mock-realtime','realtime_turn','44444444-4444-4444-8444-444444444444','realtime_byte',1000000,67108864,16777216);

CREATE FUNCTION media_amount(quantity bigint, rate_per_million bigint) RETURNS numeric
LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN quantity IS NULL OR rate_per_million IS NULL OR quantity<0 OR rate_per_million<0 THEN NULL
   ELSE div(quantity::numeric*rate_per_million+999999,1000000) END
$$;

CREATE TABLE media_requests (
 tenant_id text NOT NULL REFERENCES tenants(id), id text NOT NULL CHECK(media_uuid(id)),
 key_id text NOT NULL, owner_id text NOT NULL REFERENCES users(id), model_key text NOT NULL COLLATE "C",
 operation text NOT NULL CHECK(operation IN ('image_generation','tts','video_generation','realtime_turn')),
 profile_version text NOT NULL CHECK(media_uuid(profile_version)), idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 200),
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32), quantity_bound bigint NOT NULL CHECK(quantity_bound>0),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 deadline timestamptz(6) NOT NULL CHECK(isfinite(deadline)),
 state text NOT NULL DEFAULT 'reserved' CHECK(state IN ('reserved','pending','settled','released')),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,operation,idempotency_key),
 FOREIGN KEY(tenant_id,key_id) REFERENCES api_keys(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(model_key,operation,profile_version) REFERENCES media_model_profiles(model_key,operation,profile_version) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION media_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.key_id,NEW.owner_id,NEW.model_key,NEW.operation,NEW.profile_version,NEW.idempotency_key,NEW.request_digest,NEW.quantity_bound,NEW.created_at,NEW.deadline)
     IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.key_id,OLD.owner_id,OLD.model_key,OLD.operation,OLD.profile_version,OLD.idempotency_key,OLD.request_digest,OLD.quantity_bound,OLD.created_at,OLD.deadline) THEN
   RAISE EXCEPTION 'media request identity is immutable';
  END IF;
  IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='reserved' AND NEW.state IN ('pending','settled','released')) OR (OLD.state='pending' AND NEW.state IN ('settled','released')) OR NEW.state=OLD.state) THEN
   RAISE EXCEPTION 'invalid media request state transition';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_request_guard BEFORE INSERT OR UPDATE ON media_requests FOR EACH ROW EXECUTE FUNCTION media_request_guard();
CREATE TRIGGER media_request_immutable BEFORE DELETE ON media_requests FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_reservations (
 tenant_id text NOT NULL, id text NOT NULL CHECK(media_uuid(id)), request_id text NOT NULL, amount bigint NOT NULL CHECK(amount>=0),
 rate_per_million bigint NOT NULL CHECK(rate_per_million>=0), quantity_bound bigint NOT NULL CHECK(quantity_bound>0),
 status text NOT NULL DEFAULT 'reserved' CHECK(status IN ('reserved','pending_reconciliation','settled','released')),
 deadline timestamptz(6) NOT NULL CHECK(isfinite(deadline)), journal_id text NOT NULL REFERENCES journals(id),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,request_id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES media_requests(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION media_reservation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected numeric;
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.id,NEW.request_id,NEW.amount,NEW.rate_per_million,NEW.quantity_bound,NEW.deadline,NEW.journal_id,NEW.created_at)
   IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.request_id,OLD.amount,OLD.rate_per_million,OLD.quantity_bound,OLD.deadline,OLD.journal_id,OLD.created_at) THEN RAISE EXCEPTION 'media reservation identity is immutable'; END IF;
 IF TG_OP='UPDATE' THEN
  IF NOT ((OLD.status='reserved' AND NEW.status IN ('pending_reconciliation','settled','released')) OR (OLD.status='pending_reconciliation' AND NEW.status IN ('settled','released')) OR NEW.status=OLD.status) THEN RAISE EXCEPTION 'invalid media reservation state transition'; END IF;
 ELSE
  expected:=media_amount(NEW.quantity_bound,NEW.rate_per_million);
  IF expected IS NULL OR expected>9223372036854775807::numeric OR NEW.amount::numeric IS DISTINCT FROM expected THEN RAISE EXCEPTION 'media reservation amount mismatch'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_reservation_guard BEFORE INSERT OR UPDATE ON media_reservations FOR EACH ROW EXECUTE FUNCTION media_reservation_guard();
CREATE TRIGGER media_reservation_immutable BEFORE DELETE ON media_reservations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_attempts (
 tenant_id text NOT NULL, id text NOT NULL CHECK(media_uuid(id)), request_id text NOT NULL, provider_reference text,
 delivery_state text NOT NULL DEFAULT 'prepared' CHECK(delivery_state IN ('prepared','sending','completed','failed','unknown')),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,id,request_id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES media_requests(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION media_attempt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.id,NEW.request_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.request_id,OLD.created_at) THEN RAISE EXCEPTION 'media attempt identity is immutable'; END IF;
 IF TG_OP='UPDATE' AND NOT ((OLD.delivery_state='prepared' AND NEW.delivery_state IN ('sending','failed','unknown')) OR (OLD.delivery_state='sending' AND NEW.delivery_state IN ('completed','failed','unknown')) OR NEW.delivery_state=OLD.delivery_state) THEN RAISE EXCEPTION 'invalid media attempt state transition'; END IF;
 IF NEW.delivery_state='unknown' AND NEW.provider_reference IS NOT NULL THEN RAISE EXCEPTION 'unknown media attempt cannot expose provider reference'; END IF;
 IF TG_OP='UPDATE' AND OLD.delivery_state='unknown' AND NEW.delivery_state IS DISTINCT FROM OLD.delivery_state THEN RAISE EXCEPTION 'unknown media attempt is terminal'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_attempt_guard BEFORE INSERT OR UPDATE ON media_attempts FOR EACH ROW EXECUTE FUNCTION media_attempt_guard();
CREATE TRIGGER media_attempt_immutable BEFORE DELETE ON media_attempts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_observations (
 tenant_id text NOT NULL, id text NOT NULL CHECK(media_uuid(id)), request_id text NOT NULL, attempt_id text NOT NULL,
 source text NOT NULL CHECK(source IN ('adapter','approved_reconciliation')), source_event_id text NOT NULL,
 usage_state text NOT NULL CHECK(usage_state IN ('confirmed_final','missing','unknown','conflicting')), quantity bigint,
 canonical bytea NOT NULL CHECK(octet_length(canonical) BETWEEN 1 AND 8192), digest bytea NOT NULL CHECK(octet_length(digest)=32 AND digest=sha256(canonical)),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,source,source_event_id), UNIQUE(tenant_id,request_id,attempt_id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES media_requests(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,attempt_id,request_id) REFERENCES media_attempts(tenant_id,id,request_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((usage_state='confirmed_final' AND quantity IS NOT NULL AND quantity>=0) OR (usage_state<>'confirmed_final' AND quantity IS NULL))
);
CREATE FUNCTION media_observation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.source='approved_reconciliation' AND NEW.usage_state='confirmed_final' THEN RAISE EXCEPTION 'reconciliation cannot confirm media supplier usage'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER media_observation_guard AFTER INSERT ON media_observations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION media_observation_guard();
CREATE TRIGGER media_observation_immutable BEFORE UPDATE OR DELETE ON media_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_reservation_lots (
 tenant_id text NOT NULL, reservation_id text NOT NULL, allocation_index bigint NOT NULL CHECK(allocation_index>0), lot_id text NOT NULL, amount bigint NOT NULL CHECK(amount>0),
 PRIMARY KEY(tenant_id,reservation_id,allocation_index), UNIQUE(tenant_id,reservation_id,lot_id),
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES media_reservations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER media_reservation_lot_immutable BEFORE UPDATE OR DELETE ON media_reservation_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE media_settlements (
 tenant_id text NOT NULL, id text NOT NULL CHECK(media_uuid(id)), request_id text NOT NULL, reservation_id text NOT NULL, attempt_id text NOT NULL,
 charged_amount bigint NOT NULL CHECK(charged_amount>=0), raw_amount numeric(100,0) NOT NULL CHECK(raw_amount>=0), released_amount numeric(100,0) NOT NULL CHECK(released_amount>=0),
 status text NOT NULL CHECK(status IN ('settled','released')), reason text NOT NULL, journal_id text NOT NULL REFERENCES journals(id),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,request_id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES media_requests(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES media_reservations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,attempt_id,request_id) REFERENCES media_attempts(tenant_id,id,request_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION media_settlement_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='released' AND (NEW.charged_amount<>0 OR NEW.raw_amount<>0) THEN RAISE EXCEPTION 'released media settlement must have zero charge'; END IF;
 IF NEW.status='settled' AND NEW.charged_amount<0 THEN RAISE EXCEPTION 'invalid media settlement'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER media_settlement_guard AFTER INSERT ON media_settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION media_settlement_guard();
CREATE FUNCTION media_settlement_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR (to_jsonb(NEW)-'refunded_amount') IS DISTINCT FROM (to_jsonb(OLD)-'refunded_amount') THEN RAISE EXCEPTION 'media settlement is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER media_settlement_immutable BEFORE UPDATE OR DELETE ON media_settlements FOR EACH ROW EXECUTE FUNCTION media_settlement_immutable();

CREATE TABLE media_settlement_lots (
 tenant_id text NOT NULL, settlement_id text NOT NULL, reservation_id text NOT NULL, allocation_index bigint NOT NULL, lot_id text NOT NULL,
 charged_amount bigint NOT NULL CHECK(charged_amount>=0), released_amount bigint NOT NULL CHECK(released_amount>=0),
 PRIMARY KEY(tenant_id,settlement_id,allocation_index), UNIQUE(tenant_id,settlement_id,allocation_index,lot_id),
 FOREIGN KEY(tenant_id,settlement_id) REFERENCES media_settlements(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,reservation_id,allocation_index) REFERENCES media_reservation_lots(tenant_id,reservation_id,allocation_index) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER media_settlement_lot_immutable BEFORE UPDATE OR DELETE ON media_settlement_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.media_profile_immutable() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_amount(bigint,bigint) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_request_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_reservation_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_attempt_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_observation_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_settlement_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.media_settlement_immutable() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON TABLE media_model_profiles,media_requests,media_reservations,media_attempts,media_observations,media_reservation_lots,media_settlements,media_settlement_lots FROM PUBLIC;
REVOKE ALL ON FUNCTION media_profile_immutable(),media_amount(bigint,bigint),media_request_guard(),media_reservation_guard(),media_attempt_guard(),media_observation_guard(),media_settlement_guard(),media_settlement_immutable() FROM PUBLIC;

-- Extend the existing 023 wallet validator for the current media lot edges.
-- The wallet keeps one journal and one lot-event graph. Media rows only add
-- their own reservation/settlement edge identities.
CREATE OR REPLACE FUNCTION wallet_event_valid(event_ref TEXT) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE event_kind TEXT;
BEGIN
 SELECT kind INTO event_kind FROM funding_lot_events WHERE id=event_ref;
 CASE event_kind
 WHEN 'funding' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id WHERE e.id=event_ref AND e.revision=1 AND f.journal_id=j.id AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0 AND funding_journal_valid(j.id));
 WHEN 'reserve' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='reserve' AND j.reference=r.request_id AND a.request_id=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0)
   OR EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN media_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN media_reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='media.reserve' AND j.reference=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0);
 WHEN 'finish' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.reference=s.request_id AND j.kind=s.status AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount)
   OR EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN media_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN media_settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.kind='media.settle' AND j.reference=s.request_id AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount);
 WHEN 'refund' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_consumption_refund_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN refund_orders r ON r.id=a.refund_order_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND r.state='approved' AND r.journal_id=j.id AND j.operation_id=r.id AND j.kind='refund' AND j.reference=r.settlement_id AND e.delta_available=a.amount AND e.delta_held=0 AND e.delta_consumed=-a.amount);
 ELSE RETURN false;
 END CASE;
END $$;
ALTER FUNCTION wallet_event_valid(text) SECURITY INVOKER;
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.wallet_event_valid(text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;
REVOKE ALL ON FUNCTION wallet_event_valid(text) FROM PUBLIC;

-- Inline MVP results are immutable protected artifacts. They remain attached to
-- the original request and attempt so restart recovery cannot invent a second
-- delivery or detach bytes from the charge that produced them.
CREATE TABLE media_result_artifacts (
 tenant_id text NOT NULL,
 request_id text NOT NULL,
 attempt_id text NOT NULL,
 operation text NOT NULL CHECK(operation IN ('image_generation','tts')),
 media_type text NOT NULL CHECK(length(media_type) BETWEEN 1 AND 128 AND media_type !~ '[[:space:]]'),
 body bytea NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 67108864),
 sha256 bytea NOT NULL CHECK(octet_length(sha256)=32 AND sha256=sha256(body)),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 expires_at timestamptz(6) NOT NULL CHECK(isfinite(expires_at) AND expires_at>created_at),
 PRIMARY KEY(tenant_id,request_id), UNIQUE(tenant_id,attempt_id),
 FOREIGN KEY(tenant_id,request_id) REFERENCES media_requests(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,attempt_id,request_id) REFERENCES media_attempts(tenant_id,id,request_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER media_result_artifact_immutable BEFORE UPDATE OR DELETE ON media_result_artifacts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
REVOKE ALL ON TABLE media_result_artifacts FROM PUBLIC;
-- END 024_unified_text_metering.sql

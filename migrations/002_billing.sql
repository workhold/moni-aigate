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

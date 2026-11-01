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

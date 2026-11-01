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

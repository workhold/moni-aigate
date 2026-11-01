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

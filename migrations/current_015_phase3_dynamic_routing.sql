-- 033 text dynamic routing. Append-only current schema change.
-- The logical model profile remains the sole retail/reservation contract.

CREATE TABLE physical_models (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    logical_model_id text NOT NULL REFERENCES models(id),
    upstream_model text NOT NULL,
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','retired')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    capability jsonb NOT NULL CHECK (jsonb_typeof(capability)='object' AND octet_length(capability::text)<=16384),
    expose_to_customer boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (logical_model_id,upstream_model),
    CHECK (capability ?& ARRAY['context_limit','input_upper_bound','output_limit','supports_tools','supports_parallel_tool_calls','usage_semantics','modalities','quality_tier','quality_score','max_concurrency','supported_parameters']),
    CHECK (capability->>'usage_semantics' IN ('final','cumulative') AND capability->'modalities'='["text"]'::jsonb AND capability->>'quality_tier' IN ('economy','standard','premium')),
    CHECK ((capability->>'context_limit')::bigint>0 AND (capability->>'input_upper_bound')::bigint>0 AND (capability->>'output_limit')::bigint>=0),
    CHECK ((capability->>'input_upper_bound')::bigint<=(capability->>'context_limit')::bigint AND (capability->>'output_limit')::bigint<=(capability->>'context_limit')::bigint),
    CHECK ((capability->>'max_concurrency')::integer BETWEEN 1 AND 1000),
    CHECK (jsonb_typeof(capability->'supported_parameters')='array' AND jsonb_typeof(capability->'quality_score') IN ('number','null')),
    CHECK (jsonb_typeof(capability->'supports_tools')='boolean' AND jsonb_typeof(capability->'supports_parallel_tool_calls')='boolean')
);

CREATE FUNCTION physical_model_history_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'physical model history is immutable'; END IF;
    IF OLD.state<>'active' OR NEW.state<>'retired' OR NEW.id<>OLD.id OR NEW.logical_model_id<>OLD.logical_model_id OR NEW.upstream_model<>OLD.upstream_model OR NEW.revision<>OLD.revision OR NEW.capability<>OLD.capability OR NEW.expose_to_customer<>OLD.expose_to_customer OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'physical model only permits active to retired';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER physical_model_history_guard BEFORE UPDATE OR DELETE ON physical_models FOR EACH ROW EXECUTE FUNCTION physical_model_history_guard();

-- Existing bindings acquire a physical row exactly equivalent to the logical profile.
INSERT INTO physical_models(logical_model_id,upstream_model,capability)
SELECT DISTINCT ON (p.model_id,b.upstream_model)
    p.model_id,b.upstream_model,jsonb_build_object(
        'context_limit',c.profile->'context_limit',
        'input_upper_bound',c.profile->'input_token_upper_bound',
        'output_limit',c.profile->'output_limit',
        'supports_tools',coalesce(c.profile->'supports_tools','false'::jsonb),
        'supports_parallel_tool_calls',coalesce(c.profile->'supports_parallel_tool_calls','false'::jsonb),
        'usage_semantics',coalesce(c.profile->>'usage_semantics','final'),
        'modalities',jsonb_build_array('text'),
        'quality_tier','standard',
        'quality_score',NULL,
        'max_concurrency',(SELECT max(x.max_inflight) FROM model_bindings x JOIN route_policy_versions xp ON xp.id=x.route_policy_version_id WHERE xp.model_id=p.model_id AND x.upstream_model=b.upstream_model),
        'supported_parameters',coalesce(c.profile->'allowed_parameters','[]'::jsonb)
    )
FROM model_bindings b
JOIN route_policy_versions p ON p.id=b.route_policy_version_id
JOIN model_capability_versions c ON c.id=p.profile_version AND c.model_id=p.model_id
ORDER BY p.model_id,b.upstream_model,p.revision;

ALTER TABLE model_bindings ADD COLUMN physical_model_id text REFERENCES physical_models(id);
ALTER TABLE model_bindings DISABLE TRIGGER immutable_binding;
UPDATE model_bindings b SET physical_model_id=m.id FROM physical_models m,route_policy_versions p WHERE p.id=b.route_policy_version_id AND p.model_id=m.logical_model_id AND m.upstream_model=b.upstream_model;
ALTER TABLE model_bindings ENABLE TRIGGER immutable_binding;
ALTER TABLE model_bindings ALTER COLUMN physical_model_id SET NOT NULL;

CREATE FUNCTION physical_binding_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM physical_models m JOIN route_policy_versions p ON p.id=NEW.route_policy_version_id
        WHERE m.id=NEW.physical_model_id AND m.logical_model_id=p.model_id AND m.upstream_model=NEW.upstream_model
    ) THEN RAISE EXCEPTION 'physical binding identity mismatch'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER physical_binding_guard BEFORE INSERT ON model_bindings FOR EACH ROW EXECUTE FUNCTION physical_binding_guard();

ALTER TABLE route_policy_versions ADD COLUMN scoring jsonb CHECK (scoring IS NULL OR jsonb_typeof(scoring)='object' AND octet_length(scoring::text)<=4096);
ALTER TABLE route_policy_versions ADD COLUMN quality_floor text CHECK (quality_floor IN ('economy','standard','premium'));
ALTER TABLE route_policy_versions ADD COLUMN min_margin_rate bigint CHECK (min_margin_rate BETWEEN 0 AND 10000);
ALTER TABLE route_policy_versions ADD COLUMN stickiness jsonb CHECK (stickiness IS NULL OR jsonb_typeof(stickiness)='object' AND octet_length(stickiness::text)<=4096);

ALTER TABLE upstream_attempts DROP CONSTRAINT upstream_attempts_request_id_channel_id_key;
ALTER TABLE upstream_attempts ADD CONSTRAINT upstream_attempts_request_physical_key UNIQUE(request_id,channel_id,upstream_model);
ALTER TABLE upstream_attempts ADD COLUMN completed_at timestamptz;
ALTER TABLE upstream_attempts ADD COLUMN first_content_at timestamptz;
ALTER TABLE upstream_attempts ADD CONSTRAINT upstream_attempts_completion_times CHECK ((completed_at IS NULL OR isfinite(completed_at)) AND (first_content_at IS NULL OR isfinite(first_content_at)));

CREATE TABLE route_sessions (
    tenant_id text NOT NULL,
    session_id text NOT NULL,
    physical_model_id text NOT NULL REFERENCES physical_models(id),
    channel_id text NOT NULL REFERENCES channels(id),
    established_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    breaks integer NOT NULL DEFAULT 0 CHECK (breaks>=0),
    PRIMARY KEY(tenant_id,session_id),
    CHECK (octet_length(session_id) BETWEEN 1 AND 128 AND session_id ~ '^[A-Za-z0-9_.:-]+$'),
    CHECK (isfinite(established_at) AND isfinite(expires_at) AND isfinite(last_seen_at) AND expires_at>established_at)
);
CREATE INDEX route_sessions_expiry ON route_sessions(expires_at);

CREATE TABLE route_stats_windows (
    physical_model_id text NOT NULL REFERENCES physical_models(id),
    channel_id text NOT NULL REFERENCES channels(id),
    window_started_at timestamptz NOT NULL,
    sample_count bigint NOT NULL CHECK(sample_count>=0),
    success_count bigint NOT NULL CHECK(success_count>=0),
    timeout_count bigint NOT NULL CHECK(timeout_count>=0),
    p50_first_content_us bigint,
    p95_first_content_us bigint,
    throughput_micro_s bigint,
    refreshed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(physical_model_id,channel_id,window_started_at),
    CHECK (isfinite(window_started_at) AND isfinite(refreshed_at)),
    CHECK (sample_count<50 OR throughput_micro_s IS NOT NULL)
);
CREATE INDEX route_stats_window_lookup ON route_stats_windows(physical_model_id,channel_id,window_started_at DESC);

CREATE TABLE route_decisions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    attempt_id text,
    sequence integer NOT NULL CHECK(sequence BETWEEN 1 AND 2),
    policy_version_id text NOT NULL REFERENCES route_policy_versions(id),
    selected_binding_id text REFERENCES model_bindings(id),
    selected_physical_model_id text REFERENCES physical_models(id),
    draw numeric(30,0),
    sticky text CHECK(sticky IN ('inherit','establish','break_quality','break_cost','break_health')),
    degradation text NOT NULL DEFAULT 'none' CHECK(degradation IN ('none','tier_changed')),
    algorithm_version text NOT NULL CHECK(algorithm_version IN ('legacy-v1','dynamic-v1')),
    policy_snapshot jsonb NOT NULL CHECK(jsonb_typeof(policy_snapshot)='object' AND octet_length(policy_snapshot::text)<=16384),
    request_shape jsonb NOT NULL CHECK(jsonb_typeof(request_shape)='object' AND octet_length(request_shape::text)<=32768),
    candidates jsonb NOT NULL CHECK(jsonb_typeof(candidates)='array' AND jsonb_array_length(candidates)<=1000 AND octet_length(candidates::text)<=262144),
    candidates_digest bytea NOT NULL CHECK(octet_length(candidates_digest)=32),
    session_id text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id),
    FOREIGN KEY(tenant_id,attempt_id,request_id) REFERENCES upstream_attempts(tenant_id,id,request_id),
    CHECK(attempt_id IS NOT NULL OR (selected_binding_id IS NULL) = (selected_physical_model_id IS NULL)),
    CHECK(session_id IS NULL OR octet_length(session_id) BETWEEN 1 AND 128 AND session_id ~ '^[A-Za-z0-9_.:-]+$')
);
CREATE UNIQUE INDEX route_decisions_request_sequence ON route_decisions(request_id,sequence);
CREATE UNIQUE INDEX route_decisions_attempt ON route_decisions(attempt_id) WHERE attempt_id IS NOT NULL;
CREATE INDEX route_decisions_tenant_request ON route_decisions(tenant_id,request_id,sequence);

CREATE FUNCTION route_decision_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE route_id text;
BEGIN
    SELECT p.id INTO STRICT route_id FROM request_records r JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
    IF route_id<>NEW.policy_version_id THEN RAISE EXCEPTION 'route decision policy mismatch'; END IF;
    IF NEW.attempt_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM upstream_attempts a
        JOIN request_records r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
        JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
        JOIN model_bindings b ON b.route_policy_version_id=p.id AND b.channel_id=a.channel_id AND b.channel_version_id=a.channel_version_id AND b.upstream_model=a.upstream_model
        WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id AND a.sequence=NEW.sequence AND b.id=NEW.selected_binding_id AND b.physical_model_id=NEW.selected_physical_model_id
    ) THEN RAISE EXCEPTION 'route decision attempt mismatch'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER route_decision_guard BEFORE INSERT ON route_decisions FOR EACH ROW EXECUTE FUNCTION route_decision_guard();
CREATE TRIGGER immutable_route_decision BEFORE UPDATE OR DELETE ON route_decisions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

-- Convert the former per-attempt array once. Missing old draw/stats remain NULL;
-- legacy replay returns this frozen explanation rather than inventing inputs.
WITH converted AS (
    SELECT a.tenant_id,a.request_id,a.id AS attempt_id,a.sequence,p.id AS policy_version_id,sb.id AS binding_id,sb.physical_model_id,
        coalesce((
            SELECT jsonb_agg(jsonb_build_object(
                'binding_id',br.id,'channel_id',d.channel_id,'channel_version_id',d.version_id,
                'physical_model_id',br.physical_model_id,'upstream_model',a.upstream_model,
                'priority',d.priority,'weight',d.weight,'reason',d.reason,
                'quality_tier','standard','capability',pm.capability,
                'score',jsonb_build_object('margin_rate',NULL,'stats',jsonb_build_object('sample_count',0,'success_count',0,'timeout_count',0,'p50_first_content_us',NULL,'p95_first_content_us',NULL,'throughput_micro_s',NULL)),
                'cost_snapshot',jsonb_build_object('basis','unknown','schema_canonical',NULL,'schema_digest','','rates',jsonb_build_array(),'upper_amount',NULL)
            ) ORDER BY e.ordinality)
            FROM jsonb_array_elements(a.route_decisions) WITH ORDINALITY AS e(value,ordinality)
            CROSS JOIN LATERAL jsonb_to_record(e.value) AS d(channel_id text,version_id text,priority integer,weight bigint,reason text)
            LEFT JOIN model_bindings br ON br.route_policy_version_id=p.id AND br.channel_id=d.channel_id AND br.channel_version_id=d.version_id AND br.upstream_model=a.upstream_model
            LEFT JOIN physical_models pm ON pm.id=br.physical_model_id
        ),'[]'::jsonb) AS candidates
    FROM upstream_attempts a
    JOIN request_records r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
    JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
    JOIN model_bindings sb ON sb.route_policy_version_id=p.id AND sb.channel_id=a.channel_id AND sb.channel_version_id=a.channel_version_id AND sb.upstream_model=a.upstream_model
)
INSERT INTO route_decisions(id,tenant_id,request_id,attempt_id,sequence,policy_version_id,selected_binding_id,selected_physical_model_id,draw,degradation,algorithm_version,policy_snapshot,request_shape,candidates,candidates_digest)
SELECT gen_random_uuid()::text,tenant_id,request_id,attempt_id,sequence,policy_version_id,binding_id,physical_model_id,NULL,'none','legacy-v1','{}'::jsonb,'{}'::jsonb,candidates,sha256(convert_to(candidates::text,'UTF8')) FROM converted;

DROP TRIGGER immutable_route_decisions ON upstream_attempts;
DROP FUNCTION protect_route_decisions();
ALTER TABLE upstream_attempts DROP COLUMN route_decisions;

-- Attempt supplier bounds now derive from the selected physical capability.
-- Retail bounds continue to come from the original request snapshot.
CREATE OR REPLACE FUNCTION meter_attempt_bounds_complete() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE matched bigint; total bigint; request_input bigint; request_output bigint;
BEGIN
 SELECT input_bound,output_bound INTO STRICT request_input,request_output FROM request_meter_snapshots WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 SELECT count(*) INTO total FROM attempt_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND attempt_id=NEW.attempt_id;
 SELECT count(*) INTO matched FROM attempt_meter_bound_lines
  WHERE tenant_id=NEW.tenant_id AND attempt_id=NEW.attempt_id AND unit='token'
    AND (dimension='text_input_token' AND ((side='retail' AND quantity=request_input) OR (side='supplier' AND quantity=NEW.input_bound))
      OR dimension='text_output_token' AND side IN ('retail','supplier') AND quantity=NEW.output_bound);
 IF total IS DISTINCT FROM 4 OR matched IS DISTINCT FROM 4 OR NEW.output_bound IS DISTINCT FROM request_output THEN RAISE EXCEPTION 'attempt bounds are incomplete'; END IF;
 RETURN NULL;
END;
$$;

CREATE FUNCTION meter_attempt_plan_canonical(profile_version text,model_id text,schema_id text,schema_digest bytea,supplier_input_bound bigint,retail_input_bound bigint,output_limit bigint,streaming boolean,request_hmac text) RETURNS bytea
LANGUAGE plpgsql STABLE AS $$
DECLARE model text; protocol text; contract bytea; output_max bigint; profile bytea; request_plan bytea; result_kind text; profile_hex text;
BEGIN
 request_plan:=meter_request_plan_canonical(profile_version,model_id,schema_id,schema_digest,output_limit,streaming,request_hmac);
 IF request_plan IS NULL THEN RETURN NULL; END IF;
 SELECT c.profile->>'model',c.protocol,c.contract_digest,(c.profile->>'output_limit')::bigint INTO STRICT model,protocol,contract,output_max FROM model_capability_versions c WHERE c.id=profile_version AND c.model_id=meter_attempt_plan_canonical.model_id AND c.schema_id=meter_attempt_plan_canonical.schema_id AND c.schema_digest=meter_attempt_plan_canonical.schema_digest;
 profile:=meter_profile_canonical(profile_version,model,protocol,schema_id,schema_digest,contract,retail_input_bound,output_max);
 profile_hex:=encode(meter_profile_digest(schema_digest,profile),'hex');
 result_kind:=CASE WHEN streaming THEN 'events' ELSE 'json' END;
 RETURN convert_to(format('{"version":"1","scope":"attempt","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kind":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","input_hmac":"%s","output_limit":"%s","candidate_profile_digests":["%s"],"selected_profile_digest":"%s","parent_plan_digest":"%s","retail_bounds":%s,"supplier_bounds":%s}',model,protocol,result_kind,schema_id,encode(schema_digest,'hex'),request_hmac,output_limit,profile_hex,profile_hex,encode(meter_plan_digest(schema_digest,request_plan),'hex'),convert_from(meter_bounds_canonical(schema_id,'retail',retail_input_bound,output_limit),'UTF8'),convert_from(meter_bounds_canonical(schema_id,'supplier',supplier_input_bound,output_limit),'UTF8')),'UTF8');
END;
$$;

CREATE OR REPLACE FUNCTION meter_insert_attempt_snapshot(tenant_id text,attempt_id text,request_id text,cost_version_id text,basis text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; model text; profile text; inb bigint; retail_inb bigint; outb bigint; request_plan_digest bytea; parent_plan_digest bytea; stored text; rates bytea; digest bytea; status text; reason text; hmac text; streaming boolean; attempt_plan bytea; attempt_digest bytea; supplier_quote bytea; supplier_quote_digest bytea; retail_bounds bytea; supplier_bounds bytea; retail_bounds_digest bytea; supplier_bounds_digest bytea; supplier_meter_quote_digest bytea;
BEGIN
 SELECT s.schema_id,s.schema_digest,s.model_id,s.profile_version,s.input_bound,s.output_bound,s.plan_digest INTO STRICT sid,sdigest,model,profile,retail_inb,outb,request_plan_digest FROM request_meter_snapshots s WHERE s.tenant_id=meter_insert_attempt_snapshot.tenant_id AND s.request_id=meter_insert_attempt_snapshot.request_id;
 SELECT (m.capability->>'input_upper_bound')::bigint INTO STRICT inb
 FROM upstream_attempts a
 JOIN request_records r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
 JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
 JOIN model_bindings b ON b.route_policy_version_id=p.id AND b.channel_id=a.channel_id AND b.channel_version_id=a.channel_version_id AND b.upstream_model=a.upstream_model
 JOIN physical_models m ON m.id=b.physical_model_id
 WHERE a.tenant_id=meter_insert_attempt_snapshot.tenant_id AND a.id=meter_insert_attempt_snapshot.attempt_id AND a.request_id=meter_insert_attempt_snapshot.request_id;
 IF inb<retail_inb THEN RAISE EXCEPTION 'physical input bound is below request bound'; END IF;
 IF meter_insert_attempt_snapshot.cost_version_id IS NULL THEN
  IF basis IS DISTINCT FROM 'unknown' THEN RAISE EXCEPTION 'missing supplier quote is unknown'; END IF;
  status:='unknown'; reason:='version_missing';
 ELSE
  SELECT c.basis,c.rates_canonical,c.rates_digest INTO STRICT stored,rates,digest FROM cost_versions c WHERE c.id=meter_insert_attempt_snapshot.cost_version_id;
  IF stored='unknown' THEN status:='unknown'; reason:='basis_unknown'; rates:=NULL; digest:=NULL; basis:='unknown'; ELSE status:='known'; reason:=NULL; basis:=stored; END IF;
 END IF;
 SELECT r.request_hmac,r.streaming INTO STRICT hmac,streaming FROM request_records r WHERE r.tenant_id=meter_insert_attempt_snapshot.tenant_id AND r.id=meter_insert_attempt_snapshot.request_id;
 IF meter_plan_ready(profile,hmac) THEN
  attempt_plan:=meter_attempt_plan_canonical(profile,model,sid,sdigest,inb,retail_inb,outb,streaming,hmac);
  IF attempt_plan IS NOT NULL THEN attempt_digest:=meter_plan_digest(sdigest,attempt_plan); parent_plan_digest:=request_plan_digest; END IF;
  supplier_quote:=meter_supplier_quote_canonical(sid,sdigest,attempt_digest,status,reason,meter_insert_attempt_snapshot.cost_version_id,rates,inb,outb);
  supplier_quote_digest:=meter_quote_digest(sdigest,supplier_quote);
 END IF;
 IF attempt_digest IS NOT NULL THEN
  retail_bounds:=meter_bounds_canonical(sid,'retail',retail_inb,outb); supplier_bounds:=meter_bounds_canonical(sid,'supplier',inb,outb);
  retail_bounds_digest:=meter_bounds_digest(sdigest,'retail',retail_bounds); supplier_bounds_digest:=meter_bounds_digest(sdigest,'supplier',supplier_bounds);
  IF rates IS NOT NULL THEN supplier_meter_quote_digest:=meter_pure_quote_digest(sdigest,'supplier',supplier_bounds,rates); END IF;
 ELSE rates:=NULL; digest:=NULL; END IF;
 INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,parent_plan_digest,supplier_quote_canonical,supplier_quote_digest)
 VALUES(meter_insert_attempt_snapshot.tenant_id,meter_insert_attempt_snapshot.attempt_id,meter_insert_attempt_snapshot.request_id,model,profile,sid,sdigest,'default',meter_insert_attempt_snapshot.cost_version_id,status,reason,basis,rates,digest,retail_bounds,retail_bounds_digest,supplier_bounds,supplier_bounds_digest,supplier_meter_quote_digest,inb,outb,attempt_plan,attempt_digest,parent_plan_digest,supplier_quote,supplier_quote_digest);
 INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
 SELECT meter_insert_attempt_snapshot.tenant_id,meter_insert_attempt_snapshot.attempt_id,side,dimension,'token',CASE dimension WHEN 'text_input_token' THEN CASE side WHEN 'retail' THEN retail_inb ELSE inb END ELSE outb END
 FROM (VALUES('retail'),('supplier')) AS s(side) CROSS JOIN(VALUES('text_input_token'),('text_output_token')) AS d(dimension);
END;
$$;

CREATE OR REPLACE FUNCTION meter_attempt_matches_request() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM request_meter_snapshots r WHERE r.tenant_id=NEW.tenant_id AND r.request_id=NEW.request_id AND r.model_id=NEW.model_id AND r.profile_version=NEW.profile_version AND r.schema_id=NEW.schema_id AND r.schema_digest=NEW.schema_digest AND r.variant_key=NEW.variant_key AND r.output_bound=NEW.output_bound) THEN RAISE EXCEPTION 'attempt quote does not match request'; END IF;
 IF NOT EXISTS(
  SELECT 1 FROM upstream_attempts a
  JOIN request_records r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
  JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
  JOIN model_bindings b ON b.route_policy_version_id=p.id AND b.channel_id=a.channel_id AND b.channel_version_id=a.channel_version_id AND b.upstream_model=a.upstream_model
  JOIN physical_models m ON m.id=b.physical_model_id
  WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id AND (m.capability->>'input_upper_bound')::bigint=NEW.input_bound AND (m.capability->>'output_limit')::bigint>=NEW.output_bound
 ) THEN RAISE EXCEPTION 'attempt physical bounds mismatch'; END IF;
 IF NEW.cost_version_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM upstream_attempts a JOIN cost_versions c ON c.id=NEW.cost_version_id WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest AND c.variant_key=NEW.variant_key AND c.basis=NEW.cost_basis) THEN RAISE EXCEPTION 'attempt quote domain does not match original attempt'; END IF;
 RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION meter_attempt_plan_matches() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE hmac text; streaming boolean; ready boolean; canonical bytea; digest bytea; request_plan_digest bytea; retail_input bigint;
BEGIN
 SELECT r.request_hmac,r.streaming,q.plan_canonical IS NOT NULL,q.plan_digest,q.input_bound INTO STRICT hmac,streaming,ready,request_plan_digest,retail_input FROM request_records r JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
 IF NOT coalesce(ready,false) THEN IF NEW.plan_canonical IS NOT NULL OR NEW.plan_digest IS NOT NULL OR NEW.parent_plan_digest IS NOT NULL THEN RAISE EXCEPTION 'attempt plan does not match request'; END IF; RETURN NULL; END IF;
 canonical:=meter_attempt_plan_canonical(NEW.profile_version,NEW.model_id,NEW.schema_id,NEW.schema_digest,NEW.input_bound,retail_input,NEW.output_bound,streaming,hmac);
 digest:=meter_plan_digest(NEW.schema_digest,canonical);
 IF canonical IS NULL OR NEW.plan_canonical IS DISTINCT FROM canonical OR NEW.plan_digest IS DISTINCT FROM digest OR NEW.parent_plan_digest IS DISTINCT FROM request_plan_digest THEN RAISE EXCEPTION 'attempt plan does not match request'; END IF;
 RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION meter_attempt_vectors_match() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE rb bytea; sb bytea; rates bytea; retail_input bigint;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL OR NEW.supplier_rates_canonical IS NOT NULL OR NEW.supplier_rates_digest IS NOT NULL OR NEW.supplier_meter_quote_digest IS NOT NULL THEN RAISE EXCEPTION 'attempt meter vectors require plan'; END IF;
  RETURN NULL;
 END IF;
 SELECT input_bound INTO STRICT retail_input FROM request_meter_snapshots WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 rb:=meter_bounds_canonical(NEW.schema_id,'retail',retail_input,NEW.output_bound); sb:=meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 SELECT c.rates_canonical INTO rates FROM cost_versions c WHERE c.id=NEW.cost_version_id AND c.basis IN ('contract','invoice');
 IF NEW.retail_bounds_canonical IS DISTINCT FROM rb OR NEW.supplier_bounds_canonical IS DISTINCT FROM sb OR NEW.retail_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'retail',rb) OR NEW.supplier_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'supplier',sb) OR (rates IS NULL AND(NEW.supplier_rates_canonical IS NOT NULL OR NEW.supplier_rates_digest IS NOT NULL OR NEW.supplier_meter_quote_digest IS NOT NULL)) OR (rates IS NOT NULL AND(NEW.supplier_rates_canonical IS DISTINCT FROM rates OR NEW.supplier_rates_digest IS DISTINCT FROM meter_rates_digest(NEW.schema_digest,'supplier',rates) OR NEW.supplier_meter_quote_digest IS DISTINCT FROM meter_pure_quote_digest(NEW.schema_digest,'supplier',sb,rates))) THEN RAISE EXCEPTION 'attempt meter vectors mismatch'; END IF;
 RETURN NULL;
END;
$$;

REVOKE ALL ON physical_models,route_sessions,route_stats_windows,route_decisions FROM PUBLIC;
REVOKE ALL ON FUNCTION physical_model_history_guard(),physical_binding_guard(),route_decision_guard(),meter_attempt_plan_canonical(text,text,text,bytea,bigint,bigint,bigint,boolean,text) FROM PUBLIC;
DO $$ BEGIN
    EXECUTE format('ALTER FUNCTION %I.physical_model_history_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.physical_binding_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.route_decision_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_attempt_plan_canonical(text,text,text,bytea,bigint,bigint,bigint,boolean,text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_attempt_bounds_complete() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_attempt_matches_request() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_attempt_plan_matches() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_attempt_vectors_match() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_insert_attempt_snapshot(text,text,text,text,text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;

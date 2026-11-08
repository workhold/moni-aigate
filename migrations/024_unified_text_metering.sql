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
  'procurement_reservations','procurement_budget_events','procurement_cost_proposals',
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
  IF OLD.status='expired' AND NEW.status IS DISTINCT FROM OLD.status THEN RAISE EXCEPTION 'media artifact is terminal'; END IF;
  RETURN NEW;
 END IF;
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
 state text NOT NULL DEFAULT 'opening' CHECK(state IN ('opening','open','draining','closed')), expires_at timestamptz(6) NOT NULL CHECK(isfinite(expires_at)), max_turns integer NOT NULL DEFAULT 100 CHECK(max_turns BETWEEN 1 AND 100), created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
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
 request_id text NOT NULL, reservation_id text NOT NULL, attempt_id text NOT NULL, state text NOT NULL DEFAULT 'reserved' CHECK(state IN ('reserved','sending','receiving','finalizing','pending','settled','released')),
 response_reference text, created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,session_id,ordinal), UNIQUE(tenant_id,id,session_id),
 FOREIGN KEY(tenant_id,session_id) REFERENCES realtime_sessions(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,attempt_id,request_id) REFERENCES upstream_attempts(tenant_id,id,request_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE FUNCTION realtime_turn_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE session_state text; session_tenant text; reserved_request text; attempt_request text;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.session_id,NEW.ordinal,NEW.request_id,NEW.reservation_id,NEW.attempt_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.session_id,OLD.ordinal,OLD.request_id,OLD.reservation_id,OLD.attempt_id,OLD.created_at) THEN RAISE EXCEPTION 'realtime turn identity is immutable'; END IF;
  IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='reserved' AND NEW.state IN ('sending','released')) OR (OLD.state='sending' AND NEW.state IN ('receiving','pending')) OR (OLD.state='receiving' AND NEW.state IN ('finalizing','pending')) OR (OLD.state='finalizing' AND NEW.state IN ('settled','pending')) OR (OLD.state='pending' AND NEW.state IN ('settled','released'))) THEN RAISE EXCEPTION 'invalid realtime turn state transition'; END IF;
  IF OLD.response_reference IS NOT NULL AND NEW.response_reference IS DISTINCT FROM OLD.response_reference THEN RAISE EXCEPTION 'realtime response identity is immutable'; END IF;
  RETURN NEW;
 END IF;
 SELECT state,tenant_id INTO session_state,session_tenant FROM realtime_sessions WHERE tenant_id=NEW.tenant_id AND id=NEW.session_id;
 SELECT request_id INTO reserved_request FROM reservations WHERE tenant_id=NEW.tenant_id AND id=NEW.reservation_id;
 SELECT request_id INTO attempt_request FROM upstream_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
 IF session_state IS DISTINCT FROM 'open' OR session_tenant IS NULL OR reserved_request IS DISTINCT FROM NEW.request_id OR attempt_request IS DISTINCT FROM NEW.request_id THEN RAISE EXCEPTION 'realtime turn binding mismatch'; END IF;
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

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
  'funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots',
  'models','model_capability_versions','route_policy_versions','model_bindings','price_versions','cost_versions',
  'pricing_operations']
 LOOP
  EXECUTE format('SELECT count(*) FROM %I', name) INTO n;
  IF n <> 0 THEN
   RAISE EXCEPTION 'unified text metering refuses existing % history', name;
  END IF;
 END LOOP;
 IF to_regclass('ledger_accounts') IS NOT NULL AND EXISTS (SELECT 1 FROM ledger_accounts WHERE balance <> 0) THEN
  RAISE EXCEPTION 'unified text metering refuses a non-zero ledger balance';
 END IF;
END $$;

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

ALTER TABLE models DROP COLUMN profile;
ALTER TABLE models ALTER COLUMN profile_version DROP DEFAULT;
ALTER TABLE models ADD CONSTRAINT models_profile_fk FOREIGN KEY(profile_version, id) REFERENCES model_capability_versions(id, model_id) DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION meter_insert_text_model(model_id text, public_name text, profile jsonb, profile_version text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE protocol text; input_max bigint; output_max bigint; sid text; digest bytea; canonical bytea; contract bytea;
BEGIN
 protocol := coalesce(profile->>'protocol','openai_chat');
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

CREATE FUNCTION meter_price_million(price_id text, dimension text) RETURNS bigint LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN (numerator::numeric * 1000000) % denominator = 0 THEN ((numerator::numeric * 1000000) / denominator)::bigint ELSE NULL END
 FROM price_rate_lines WHERE price_version_id=price_id AND dimension=meter_price_million.dimension
$$;

CREATE OR REPLACE FUNCTION guard_group_price_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gstate TEXT; op pricing_operations%ROWTYPE;
BEGIN
 PERFORM id FROM models WHERE id=NEW.model_id FOR SHARE;
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

CREATE OR REPLACE FUNCTION pricing_price_result(p price_versions) RETURNS JSONB LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('id',p.id,'model_id',p.model_id,'price_group_id',p.price_group_id,
  'currency',p.currency,
  'input_rate',meter_price_million(p.id,'text_input_token')::text,
  'output_rate',meter_price_million(p.id,'text_output_token')::text,
  'effective_at',pricing_time(p.effective_at),'created_at',pricing_time(p.created_at))
$$;

CREATE OR REPLACE FUNCTION require_pricing_operation_result() RETURNS trigger LANGUAGE plpgsql AS $$
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
  expected:=jsonb_build_object('model_id',p.model_id,
   'input_rate',meter_price_million(p.id,'text_input_token')::text,
   'output_rate',meter_price_million(p.id,'text_output_token')::text,
   'effective_at',pricing_time(p.effective_at));
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

ALTER TABLE price_versions DROP COLUMN input_rate, DROP COLUMN output_rate;

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

CREATE FUNCTION meter_insert_token_cost(cost_id text, channel_id text, upstream_model text, input_per_million bigint, output_per_million bigint, basis text, effective_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea; cid text;
BEGIN
 IF basis NOT IN ('contract','invoice','unknown') THEN RAISE EXCEPTION 'invalid cost basis'; END IF;
 SELECT c.schema_id, c.schema_digest INTO sid, sdigest FROM model_capability_versions c ORDER BY c.created_at DESC LIMIT 1;
 IF sid IS NULL THEN RAISE EXCEPTION 'text schema is required before a supplier quote'; END IF;
 cid := coalesce(cost_id, gen_random_uuid()::text);
 IF basis = 'unknown' THEN
  IF input_per_million IS NOT NULL OR output_per_million IS NOT NULL THEN RAISE EXCEPTION 'unknown cost has no rates'; END IF;
  INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency)
   VALUES(cid, channel_id, upstream_model, basis, coalesce(effective_at, clock_timestamp()), sid, sdigest, 'default', 'CNY');
  RETURN;
 END IF;
 IF input_per_million IS NULL OR output_per_million IS NULL THEN RAISE EXCEPTION 'known cost requires both rates'; END IF;
 SELECT r.numerator, r.denominator INTO in_n, in_d FROM meter_reduce_million(input_per_million) r;
 SELECT r.numerator, r.denominator INTO out_n, out_d FROM meter_reduce_million(output_per_million) r;
 canonical := meter_rate_canonical(sid, 'supplier', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,rates_canonical,rates_digest)
  VALUES(cid, channel_id, upstream_model, basis, coalesce(effective_at, clock_timestamp()), sid, sdigest, 'default', 'CNY', canonical, digest);
 INSERT INTO cost_rate_lines(cost_version_id,dimension,unit,numerator,denominator) VALUES
  (cid,'text_input_token','token',in_n,in_d),
  (cid,'text_output_token','token',out_n,out_d);
END $$;

CREATE FUNCTION meter_cost_million(cost_id text, dimension text) RETURNS bigint LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN (numerator::numeric * 1000000) % denominator = 0 THEN ((numerator::numeric * 1000000) / denominator)::bigint ELSE NULL END
 FROM cost_rate_lines WHERE cost_version_id=cost_id AND dimension=meter_cost_million.dimension
$$;

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
CREATE TRIGGER immutable_binding_cost_cap BEFORE UPDATE OR DELETE ON binding_cost_rate_limits FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION meter_insert_token_cap(binding_id text, dimension text, per_million bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE n bigint; d bigint;
BEGIN
 IF dimension NOT IN ('text_input_token','text_output_token') THEN RAISE EXCEPTION 'unsupported cap dimension'; END IF;
 SELECT r.numerator, r.denominator INTO n, d FROM meter_reduce_million(per_million) r;
 INSERT INTO binding_cost_rate_limits(binding_id,dimension,unit,numerator,denominator) VALUES(binding_id, dimension, 'token', n, d);
END $$;

ALTER TABLE request_records DROP CONSTRAINT request_retail_snapshot_shape;
ALTER TABLE request_records
 ADD COLUMN protocol TEXT COLLATE "C",
 ADD COLUMN execution_kind TEXT COLLATE "C" DEFAULT 'synchronous',
 ADD COLUMN result_kind TEXT COLLATE "C";
ALTER TABLE request_records DROP COLUMN price_version_id, DROP COLUMN input_rate, DROP COLUMN output_rate, DROP COLUMN input_upper_bound, DROP COLUMN output_limit, DROP COLUMN pricing_group_id, DROP COLUMN tenant_pricing_revision, DROP COLUMN retail_priced_at;

CREATE OR REPLACE FUNCTION guard_request_retail_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.profile_version,NEW.protocol,NEW.execution_kind,NEW.result_kind)
   IS DISTINCT FROM ROW(OLD.tenant_id,OLD.key_id,OLD.owner_id,OLD.model_id,OLD.profile_version,OLD.protocol,OLD.execution_kind,OLD.result_kind) THEN
   RAISE EXCEPTION 'immutable request identity';
  END IF;
 END IF;
 RETURN NEW;
END $$;

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
 retail_quote_canonical BYTEA,
 retail_quote_digest BYTEA,
 created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
 CHECK((plan_canonical IS NULL AND plan_digest IS NULL) OR (plan_canonical IS NOT NULL AND octet_length(plan_canonical)<=65536 AND octet_length(plan_digest)=32)),
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
ALTER TABLE upstream_attempts ADD CONSTRAINT upstream_attempts_request_identity UNIQUE(tenant_id, id, request_id);

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
 input_bound BIGINT NOT NULL CHECK(input_bound>=0),
 output_bound BIGINT NOT NULL CHECK(output_bound>=0),
 plan_canonical BYTEA,
 plan_digest BYTEA,
 supplier_quote_canonical BYTEA,
 supplier_quote_digest BYTEA,
 created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
 created_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((plan_canonical IS NULL AND plan_digest IS NULL) OR (plan_canonical IS NOT NULL AND octet_length(plan_canonical)<=65536 AND octet_length(plan_digest)=32)),
 CHECK((supplier_quote_canonical IS NULL AND supplier_quote_digest IS NULL) OR (supplier_quote_canonical IS NOT NULL AND octet_length(supplier_quote_canonical)<=16384 AND octet_length(supplier_quote_digest)=32)),
 UNIQUE(tenant_id, attempt_id),
 UNIQUE(tenant_id, attempt_id, request_id),
 FOREIGN KEY(tenant_id, attempt_id, request_id) REFERENCES upstream_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id, request_id) REFERENCES request_meter_snapshots(tenant_id, request_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED,
 CHECK(
  (quote_status='known' AND unknown_reason IS NULL AND cost_version_id IS NOT NULL AND cost_basis IN ('contract','invoice') AND supplier_rates_canonical IS NOT NULL AND supplier_rates_digest IS NOT NULL)
  OR
  (quote_status='unknown' AND cost_basis='unknown' AND supplier_rates_canonical IS NULL AND supplier_rates_digest IS NULL AND (
    (unknown_reason='version_missing' AND cost_version_id IS NULL) OR
    (unknown_reason='basis_unknown' AND cost_version_id IS NOT NULL)
  ))
 )
);
CREATE TRIGGER immutable_attempt_meter_snapshot BEFORE UPDATE OR DELETE ON attempt_meter_snapshots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION meter_insert_attempt_snapshot(tenant_id text, attempt_id text, request_id text, cost_version_id text, basis text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; model text; profile text; inb bigint; outb bigint; stored text; rates bytea; digest bytea; status text; reason text; hmac text; streaming boolean; attempt_plan bytea; attempt_digest bytea; supplier_quote bytea; supplier_quote_digest bytea;
BEGIN
 SELECT s.schema_id, s.schema_digest, s.model_id, s.profile_version, s.input_bound, s.output_bound
  INTO sid, sdigest, model, profile, inb, outb
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
  attempt_digest := meter_plan_digest(sdigest, attempt_plan);
  supplier_quote := meter_supplier_quote_canonical(sid, sdigest, attempt_digest, status, reason, meter_insert_attempt_snapshot.cost_version_id, rates, inb, outb);
  supplier_quote_digest := meter_quote_digest(sdigest, supplier_quote);
 END IF;
 INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,input_bound,output_bound,plan_canonical,plan_digest,supplier_quote_canonical,supplier_quote_digest)
  VALUES(meter_insert_attempt_snapshot.tenant_id, meter_insert_attempt_snapshot.attempt_id, meter_insert_attempt_snapshot.request_id, model, profile, sid, sdigest, 'default', meter_insert_attempt_snapshot.cost_version_id, status, reason, basis, rates, digest, inb, outb, attempt_plan, attempt_digest, supplier_quote, supplier_quote_digest);
 INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
  SELECT meter_insert_attempt_snapshot.tenant_id, meter_insert_attempt_snapshot.attempt_id, side, dimension, 'token', CASE dimension WHEN 'text_input_token' THEN inb ELSE outb END
  FROM (VALUES ('retail'),('supplier')) AS s(side)
  CROSS JOIN (VALUES ('text_input_token'),('text_output_token')) AS d(dimension);
END $$;

CREATE FUNCTION meter_attempt_plan_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE hmac text; streaming boolean; ready boolean; canonical bytea; digest bytea;
BEGIN
 SELECT r.request_hmac, r.streaming, q.plan_canonical IS NOT NULL INTO hmac, streaming, ready
  FROM request_records r JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
  WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
 IF NOT coalesce(ready, false) THEN
  IF NEW.plan_canonical IS NOT NULL OR NEW.plan_digest IS NOT NULL THEN RAISE EXCEPTION 'attempt plan does not match request'; END IF;
  RETURN NULL;
 END IF;
 canonical := meter_attempt_plan_canonical(NEW.profile_version, NEW.model_id, NEW.schema_id, NEW.schema_digest, NEW.output_bound, streaming, hmac);
 digest := meter_plan_digest(NEW.schema_digest, canonical);
 IF canonical IS NULL OR NEW.plan_canonical IS DISTINCT FROM canonical OR NEW.plan_digest IS DISTINCT FROM digest THEN
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
 ADD COLUMN observation_canonical BYTEA;
ALTER TABLE metering_events DROP COLUMN normalized_usage, DROP COLUMN schema_version;
ALTER TABLE metering_events
 ADD CHECK(retail_usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CHECK(supplier_usage_state IN ('confirmed_final','provisional','missing','conflicting')),
 ADD CHECK(variant_key IS NULL OR variant_key='default'),
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
CREATE FUNCTION meter_event_usage(event_id text) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN observation_canonical IS NULL THEN NULL ELSE convert_from(observation_canonical,'UTF8')::jsonb END FROM metering_events WHERE id=meter_event_usage.event_id
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
CREATE FUNCTION meter_final_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.final.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;
CREATE FUNCTION meter_quantity_canonical(schema_id text, input_tokens bigint, output_tokens bigint) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
 SELECT meter_side_quantity_canonical(schema_id, 'supplier', input_tokens, output_tokens)
$$;

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
 IF basis='unknown' THEN
  IF n<>0 OR stored IS NOT NULL OR stored_digest IS NOT NULL THEN RAISE EXCEPTION 'unknown supplier quote has rates'; END IF;
  RETURN NULL;
 END IF;
 IF basis NOT IN ('contract','invoice') OR n<>2 THEN RAISE EXCEPTION 'known supplier quote is incomplete'; END IF;
 SELECT l.numerator, l.denominator INTO in_n, in_d FROM cost_rate_lines l WHERE l.cost_version_id=cid AND l.dimension='text_input_token' AND l.unit='token';
 SELECT l.numerator, l.denominator INTO out_n, out_d FROM cost_rate_lines l WHERE l.cost_version_id=cid AND l.dimension='text_output_token' AND l.unit='token';
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
 ADD UNIQUE(tenant_id, request_id, attempt_id, id);

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

CREATE FUNCTION meter_attempt_cost_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE corr_amount bigint; corr_basis text; in_q bigint; out_q bigint; in_n bigint; in_d bigint; out_n bigint; out_d bigint; basis text; version text; expected numeric;
BEGIN
 IF NEW.cost_status='unknown' AND NEW.cost_amount IS NULL THEN RETURN NULL; END IF;
 SELECT c.amount, c.basis INTO corr_amount, corr_basis FROM provider_cost_corrections c
  WHERE c.attempt_id=NEW.id AND c.tenant_id=NEW.tenant_id
  ORDER BY (c.xmin = pg_current_xact_id()::xid) DESC, c.created_at DESC, c.id DESC
  LIMIT 1;
 IF FOUND THEN
  IF NEW.cost_amount = corr_amount::numeric AND NEW.cost_status = corr_basis THEN RETURN NULL; END IF;
  RAISE EXCEPTION 'supplier cost does not match final usage';
 END IF;
 SELECT qi.quantity, qo.quantity INTO in_q, out_q
  FROM finalization_records f
  JOIN meter_event_quantities qi ON qi.event_id=f.event_id AND qi.dimension='text_input_token'
  JOIN meter_event_quantities qo ON qo.event_id=f.event_id AND qo.dimension='text_output_token'
  WHERE f.attempt_id=NEW.id AND f.tenant_id=NEW.tenant_id AND f.supplier_usage_state='confirmed_final'
  ORDER BY f.created_at DESC, f.id DESC
  LIMIT 1;
 IF FOUND THEN
  SELECT s.cost_version_id, s.cost_basis INTO version, basis FROM attempt_meter_snapshots s WHERE s.attempt_id=NEW.id;
  SELECT l.numerator, l.denominator INTO in_n, in_d FROM cost_rate_lines l WHERE l.cost_version_id=version AND l.dimension='text_input_token';
  SELECT l.numerator, l.denominator INTO out_n, out_d FROM cost_rate_lines l WHERE l.cost_version_id=version AND l.dimension='text_output_token';
  expected := meter_token_amount(in_q, out_q, in_n, in_d, out_n, out_d);
  IF expected IS NOT NULL AND NEW.cost_amount = expected AND NEW.cost_status = basis THEN RETURN NULL; END IF;
  RAISE EXCEPTION 'supplier cost does not match final usage';
 END IF;
 IF NEW.cost_amount = 0 AND NEW.cost_status='contract' AND EXISTS (
  SELECT 1 FROM finalization_records f WHERE f.attempt_id=NEW.id AND f.tenant_id=NEW.tenant_id AND f.transport_end_reason IN ('not_sent','rejected_no_consumption')
 ) THEN RETURN NULL; END IF;
 RAISE EXCEPTION 'supplier cost does not match final usage';
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

REVOKE ALL ON FUNCTION meter_frames(bytea[]), meter_gcd(bigint,bigint), meter_schema_stamp(), meter_schema_complete(), meter_text_schema(bigint,bigint), meter_insert_text_model(text,text,jsonb,text), meter_model_profile(text), meter_rate_canonical(text,text,bigint,bigint,bigint,bigint), meter_price_complete(), meter_reduce_million(bigint), meter_insert_token_price(text,text,bigint,bigint,timestamptz,text,text,text,timestamptz), meter_price_million(text,text), meter_insert_token_cost(text,text,text,bigint,bigint,text,timestamptz), meter_cost_million(text,text), meter_insert_token_cap(text,text,bigint), meter_request_snapshot_present(), meter_plan_ready(text,text), meter_profile_canonical(text,text,text,text,bytea,bytea,bigint,bigint), meter_profile_digest(bytea,bytea), meter_bounds_canonical(text,text,bigint,bigint), meter_request_plan_canonical(text,text,text,bytea,bigint,boolean,text), meter_attempt_plan_canonical(text,text,text,bytea,bigint,boolean,text), meter_plan_digest(bytea,bytea), meter_request_plan_matches(), meter_attempt_plan_matches(), meter_request_price_matches(), meter_insert_attempt_snapshot(text,text,text,text,text), meter_attempt_snapshot_present(), meter_attempt_matches_request(), meter_usage_state(text), meter_record_event_quantities(text,text,text,bigint,bigint), meter_event_quantity(text,text), meter_event_usage(text), meter_side_quantity_canonical(text,text,bigint,bigint), meter_final_digest(bytea,text,bytea), meter_quantity_canonical(text,bigint,bigint), meter_event_rows_complete(), meter_event_attempt_bound(), meter_cost_lines_complete(), meter_finish_probe(text,text,text,bigint,bigint), meter_finalization_complete(), meter_request_finalization_pointer(), meter_settlement_finalization(), meter_token_amount(bigint,bigint,bigint,bigint,bigint,bigint), meter_attempt_cost_matches(), meter_procurement_attempt_closed(), meter_procurement_proposal_current(), meter_request_bounds_complete(), meter_attempt_bounds_complete(), meter_quote_digest(bytea,bytea), meter_retail_quote_canonical(text,bytea,bytea,text,bigint,bigint), meter_supplier_quote_canonical(text,bytea,bytea,text,text,text,bytea,bigint,bigint), meter_request_quote_matches(), meter_attempt_quote_matches() FROM PUBLIC;

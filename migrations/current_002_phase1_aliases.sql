-- Phase 1 model aliases and original client model-name capture.
-- This is a current-only append migration. Historical migration plans never
-- execute this file.

ALTER TABLE request_records
  ADD COLUMN requested_model_name TEXT COLLATE "C";

ALTER TABLE request_records
  ADD CONSTRAINT request_records_requested_model_name_check
  CHECK (requested_model_name IS NULL OR
    (octet_length(requested_model_name) BETWEEN 1 AND 100
     AND requested_model_name = btrim(requested_model_name)
     AND requested_model_name !~ '[[:space:]]'
     AND requested_model_name !~ '[[:cntrl:]]'));

CREATE FUNCTION phase1_requested_model_name_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.requested_model_name IS DISTINCT FROM OLD.requested_model_name THEN
    RAISE EXCEPTION 'requested model name is immutable';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER phase1_requested_model_name_immutable
BEFORE UPDATE ON request_records
FOR EACH ROW EXECUTE FUNCTION phase1_requested_model_name_immutable();

CREATE TABLE model_aliases (
  alias TEXT PRIMARY KEY COLLATE "C",
  model_id TEXT NOT NULL REFERENCES models(id),
  state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','retired')),
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  operation_id TEXT NOT NULL REFERENCES pricing_operations(operation_id)
    DEFERRABLE INITIALLY DEFERRED,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  retired_at TIMESTAMPTZ,
  CHECK (octet_length(alias) BETWEEN 1 AND 100
    AND alias = btrim(alias)
    AND alias !~ '[[:space:]]'
    AND alias !~ '[[:cntrl:]]'),
  CHECK ((state = 'retired') = (retired_at IS NOT NULL)),
  CHECK (isfinite(created_at)),
  CHECK (retired_at IS NULL OR isfinite(retired_at))
);

CREATE INDEX model_aliases_model_active
ON model_aliases(model_id, alias COLLATE "C")
WHERE state = 'active';

CREATE FUNCTION phase1_model_name_lock(value TEXT) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('model-name:' || value, 0));
END $$;

CREATE FUNCTION phase1_alias_name_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM phase1_model_name_lock(NEW.alias);
  IF EXISTS (SELECT 1 FROM models WHERE public_name = NEW.alias) THEN
    RAISE EXCEPTION 'model alias conflicts with public model name'
      USING ERRCODE = '23505';
  END IF;
  IF TG_OP = 'UPDATE' THEN
    IF ROW(NEW.alias,NEW.model_id,NEW.created_by,NEW.created_at)
       IS DISTINCT FROM ROW(OLD.alias,OLD.model_id,OLD.created_by,OLD.created_at)
       OR OLD.state <> 'active' OR NEW.state <> 'retired'
       OR NEW.revision <> OLD.revision + 1
       OR NEW.operation_id=OLD.operation_id
       OR NEW.retired_at IS NULL THEN
      RAISE EXCEPTION 'model alias identity or state is immutable';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER phase1_alias_name_guard
BEFORE INSERT OR UPDATE ON model_aliases
FOR EACH ROW EXECUTE FUNCTION phase1_alias_name_guard();

CREATE FUNCTION phase1_model_name_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' OR NEW.public_name IS DISTINCT FROM OLD.public_name THEN
    PERFORM phase1_model_name_lock(NEW.public_name);
    IF EXISTS (SELECT 1 FROM model_aliases WHERE alias = NEW.public_name) THEN
      RAISE EXCEPTION 'model public name conflicts with alias'
        USING ERRCODE = '23505';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER phase1_model_name_guard
BEFORE INSERT OR UPDATE OF public_name ON models
FOR EACH ROW EXECUTE FUNCTION phase1_model_name_guard();

ALTER TABLE pricing_operations
  DROP CONSTRAINT pricing_operations_kind_check,
  ADD CONSTRAINT pricing_operations_kind_check CHECK (kind IN (
    'price_group.create','price_group.update','group_price.publish',
    'tenant_pricing.assign','default_price.publish','cost_price.publish',
    'model_alias.create','model_alias.retire'));

CREATE FUNCTION pricing_model_alias_result(a model_aliases) RETURNS jsonb
LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT jsonb_build_object(
    'alias',a.alias,
    'model_id',a.model_id,
    'state',a.state,
    'revision',a.revision::text,
    'operation_id',a.operation_id,
    'created_by',a.created_by,
    'created_at',pricing_time(a.created_at),
    'retired_at',CASE WHEN a.retired_at IS NULL THEN NULL ELSE pricing_time(a.retired_at) END)
$$;

CREATE OR REPLACE FUNCTION assert_pricing_safe_input(k text, j jsonb) RETURNS void
LANGUAGE plpgsql IMMUTABLE AS $_$
DECLARE shape jsonb; label text;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'pricing safe input must be object'; END IF;
 CASE k
 WHEN 'price_group.create' THEN shape:=jsonb_build_object('label',j->'label');
 WHEN 'price_group.update' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','label',j->'label','state',j->'state');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR jsonb_typeof(j->'state') IS DISTINCT FROM 'string' OR (j->>'state') NOT IN ('active','retired') THEN RAISE EXCEPTION 'invalid group update input'; END IF;
 WHEN 'model_alias.create' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','alias',j->'alias');
  IF NOT pricing_json_uuid(j->'model_id') OR jsonb_typeof(j->'alias') IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'invalid model alias create input'; END IF;
 WHEN 'model_alias.retire' THEN
  shape:=jsonb_build_object('alias',j->'alias','expected_revision',j->'expected_revision');
  IF jsonb_typeof(j->'alias') IS DISTINCT FROM 'string' OR NOT pricing_json_uint(j->'expected_revision',true) THEN RAISE EXCEPTION 'invalid model alias retire input'; END IF;
 WHEN 'group_price.compile' THEN
  shape:=jsonb_build_object('multiplier',j->'multiplier','model_ids',j->'model_ids','effective_at',j->'effective_at','expected_base_version_ids',j->'expected_base_version_ids');
  IF jsonb_typeof(j->'multiplier') IS DISTINCT FROM 'string' OR (j->>'multiplier') !~ '^(0|[1-9][0-9]*)([.][0-9]{1,6})?$' OR (j->>'multiplier')::numeric<=0 OR (j->>'multiplier')::numeric>100 OR jsonb_typeof(j->'model_ids') IS DISTINCT FROM 'array' OR jsonb_array_length(j->'model_ids') NOT BETWEEN 1 AND 100 OR NOT pricing_json_time(j->'effective_at') OR (jsonb_typeof(j->'expected_base_version_ids') IS DISTINCT FROM 'object' AND j->'expected_base_version_ids' IS DISTINCT FROM 'null'::jsonb) THEN RAISE EXCEPTION 'invalid group price compile input'; END IF;
  IF EXISTS (SELECT 1 FROM jsonb_array_elements(j->'model_ids') x WHERE jsonb_typeof(x) IS DISTINCT FROM 'string' OR NOT pricing_json_uuid(x)) THEN RAISE EXCEPTION 'invalid group price compile model ids'; END IF;
 WHEN 'group_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid group price input'; END IF;
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
 IF k='model_alias.create' THEN
  IF octet_length(j->>'alias') NOT BETWEEN 1 AND 100 OR j->>'alias'<>btrim(j->>'alias') OR (j->>'alias')~'[[:space:]]' OR (j->>'alias')~'[[:cntrl:]]' THEN RAISE EXCEPTION 'invalid model alias'; END IF;
 END IF;
 IF k='model_alias.retire' AND octet_length(j->>'alias') NOT BETWEEN 1 AND 100 THEN RAISE EXCEPTION 'invalid model alias'; END IF;
END $_$;

CREATE FUNCTION phase1_compile_models(j jsonb) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT '['||string_agg(to_jsonb(v #>> '{}')::text,',' ORDER BY ord)||']'
 FROM jsonb_array_elements(j) WITH ORDINALITY AS x(v,ord)
$$;

CREATE FUNCTION phase1_compile_expected(j jsonb) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT CASE WHEN j='null'::jsonb THEN NULL ELSE
  '{'||coalesce((SELECT string_agg(to_jsonb(key)::text||':'||value::text,',' ORDER BY key COLLATE "C") FROM jsonb_each(j)),'')||'}' END
$$;

CREATE OR REPLACE FUNCTION pricing_fingerprint(k text, a text, s text, t text, j jsonb) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(pricing_frame(ARRAY[k,a,s,t] || CASE k
  WHEN 'price_group.create' THEN ARRAY[j->>'label']
  WHEN 'price_group.update' THEN ARRAY[j->>'expected_revision',j->>'label',j->>'state']
  WHEN 'model_alias.create' THEN ARRAY[j->>'model_id',j->>'alias']
  WHEN 'model_alias.retire' THEN ARRAY[j->>'alias',j->>'expected_revision']
  WHEN 'group_price.compile' THEN ARRAY[j->>'multiplier',phase1_compile_models(j->'model_ids'),j->>'effective_at',phase1_compile_expected(j->'expected_base_version_ids')]
  WHEN 'group_price.publish' THEN ARRAY[j->>'model_id',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',convert_from(meter_text_rates_canonical(j->'rates','retail'),'UTF8'),j->>'effective_at']
  WHEN 'default_price.publish' THEN ARRAY[j->>'model_id',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',convert_from(meter_text_rates_canonical(j->'rates','retail'),'UTF8'),j->>'effective_at']
  WHEN 'cost_price.publish' THEN ARRAY[j->>'channel_id',j->>'upstream_model',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',j->>'basis',CASE WHEN j->'rates'='null'::jsonb THEN NULL ELSE convert_from(meter_text_rates_canonical(j->'rates','supplier'),'UTF8') END,j->>'effective_at',j->>'expected_channel_revision']
  WHEN 'tenant_pricing.assign' THEN ARRAY[j->>'expected_revision',j->>'price_group_id'] ELSE ARRAY[]::text[] END)),'hex')
$$;

CREATE OR REPLACE FUNCTION guard_pricing_operation_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input);
 NEW.created_txid:=txid_current();
 NEW.created_at:=clock_timestamp();
 NEW.input_fingerprint:=pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,NEW.safe_input);
 IF NEW.kind IN ('price_group.create','group_price.publish','default_price.publish','cost_price.publish','model_alias.create') THEN
  NEW.result_snapshot:=jsonb_set(NEW.result_snapshot,'{created_at}',to_jsonb(pricing_time(NEW.created_at)),true);
 END IF;
 IF NEW.kind='model_alias.retire' THEN
  NEW.result_snapshot:=jsonb_set(NEW.result_snapshot,'{retired_at}',to_jsonb(pricing_time(NEW.created_at)),true);
 END IF;
 RETURN NEW;
END $$;

ALTER TABLE pricing_operations
  DROP CONSTRAINT pricing_operations_kind_check,
  ADD CONSTRAINT pricing_operations_kind_check CHECK (kind IN (
    'price_group.create','price_group.update','group_price.publish','group_price.compile',
    'tenant_pricing.assign','default_price.publish','cost_price.publish',
    'model_alias.create','model_alias.retire'));

ALTER TABLE pricing_operations
  DROP CONSTRAINT pricing_operations_revision_check,
  ADD CONSTRAINT pricing_operations_revision_check CHECK (
    (kind IN ('group_price.publish','group_price.compile','default_price.publish','cost_price.publish') AND revision IS NULL)
    OR (kind NOT IN ('group_price.publish','group_price.compile','default_price.publish','cost_price.publish') AND revision IS NOT NULL));

ALTER TABLE pricing_operations DROP CONSTRAINT pricing_operations_result_snapshot_check,
 ADD CONSTRAINT pricing_operations_result_snapshot_check CHECK (
 jsonb_typeof(result_snapshot)='object' AND octet_length(result_snapshot::text)<=CASE WHEN kind='group_price.compile' THEN 65536 ELSE 8192 END);

DROP INDEX price_versions_pricing_operation;
CREATE INDEX price_versions_pricing_operation
ON price_versions(pricing_operation_id)
WHERE pricing_operation_id IS NOT NULL;

CREATE OR REPLACE FUNCTION guard_group_price_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE gstate TEXT; op pricing_operations%ROWTYPE; expected_kind text;
BEGIN
 PERFORM id FROM models WHERE id=NEW.model_id FOR SHARE;
 IF NOT EXISTS (SELECT 1 FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN model_capability_variants v ON v.profile_version=c.id AND v.variant_key=NEW.variant_key WHERE m.id=NEW.model_id AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest) THEN RAISE EXCEPTION 'price schema mismatch'; END IF;
 IF NEW.price_group_id IS NOT NULL THEN
  SELECT state INTO STRICT gstate FROM price_groups WHERE id=NEW.price_group_id FOR SHARE;
  IF gstate<>'active' THEN RAISE EXCEPTION 'price group retired'; END IF;
 END IF;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 expected_kind:=CASE WHEN NEW.price_group_id IS NULL THEN 'default_price.publish' ELSE op.kind END;
 IF op.created_txid<>txid_current() OR op.actor<>NEW.created_by OR (NEW.price_group_id IS NULL AND op.kind<>expected_kind) OR (NEW.price_group_id IS NOT NULL AND op.kind NOT IN ('group_price.publish','group_price.compile')) OR (op.kind='group_price.publish' AND op.resource_id<>NEW.id) OR (op.kind='group_price.compile' AND op.resource_id<>NEW.price_group_id) THEN RAISE EXCEPTION 'price operation mismatch'; END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION meter_group_price_audit(oid text) RETURNS void
LANGUAGE plpgsql STABLE AS $$
DECLARE op pricing_operations%ROWTYPE; n bigint; want_action text;
BEGIN
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=oid AND kind IN ('group_price.publish','group_price.compile','default_price.publish','cost_price.publish');
 want_action:=CASE WHEN op.kind='cost_price.publish' THEN 'pricing.cost.publish' WHEN op.kind='group_price.compile' THEN 'pricing.price.compile' ELSE 'pricing.price.publish' END;
 SELECT count(*) INTO n FROM audit_events a WHERE a.sanitized_diff->>'operation_id'=oid;
 IF n<>1 OR NOT EXISTS (SELECT 1 FROM audit_events a WHERE a.sanitized_diff->>'operation_id'=oid AND op.created_txid=txid_current() AND a.actor=op.actor AND a.tenant_scope IS NULL AND a.action=want_action AND a.target=op.target AND a.sanitized_diff=jsonb_build_object('operation_id',oid,'target',op.target,'after',op.result_snapshot)) THEN RAISE EXCEPTION 'group price audit mismatch'; END IF;
END $$;

CREATE OR REPLACE FUNCTION meter_group_price_audit_birth() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 IF NEW.action IN ('pricing.price.publish','pricing.price.compile','pricing.cost.publish') THEN
  SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.sanitized_diff->>'operation_id' AND kind IN ('group_price.publish','group_price.compile','default_price.publish','cost_price.publish');
  IF op.created_txid<>txid_current() OR NEW.action<>(CASE WHEN op.kind='cost_price.publish' THEN 'pricing.cost.publish' WHEN op.kind='group_price.compile' THEN 'pricing.price.compile' ELSE 'pricing.price.publish' END) THEN RAISE EXCEPTION 'publish audit mismatch'; END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION meter_group_price_audit_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.action IN ('pricing.price.publish','pricing.price.compile','pricing.cost.publish') THEN PERFORM meter_group_price_audit(NEW.sanitized_diff->>'operation_id'); END IF;
 RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION require_pricing_operation_result() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE g price_groups%ROWTYPE; t tenants%ROWTYPE; p price_versions%ROWTYPE; c cost_versions%ROWTYPE;
 e tenant_pricing_assignments%ROWTYPE; a model_aliases%ROWTYPE;
 expected JSONB; prior BIGINT; has_event BOOLEAN; current_revision bigint;
BEGIN
 IF NEW.created_txid<>txid_current() THEN RAISE EXCEPTION 'pricing operation not current transaction'; END IF;
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input);
 IF NEW.kind='price_group.create' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  expected:=jsonb_build_object('label',g.label);
  IF NEW.target<>'/api/platform/price-groups' OR g.last_operation_id<>NEW.operation_id OR g.revision<>1 OR g.state<>'active' OR g.created_at<>NEW.created_at OR NEW.revision<>1 OR NEW.result_snapshot<>pricing_group_result(g) THEN RAISE EXCEPTION 'price group create result mismatch'; END IF;
 ELSIF NEW.kind='price_group.update' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'label',g.label,'state',g.state);
  IF NEW.target<>('/api/platform/price-groups/'||g.id) OR NEW.revision<>g.revision OR NEW.result_snapshot<>pricing_group_result(g) OR NOT ((g.last_operation_id=NEW.operation_id AND prior<9223372036854775807 AND g.revision=prior+1) OR (g.last_operation_id<>NEW.operation_id AND g.revision=prior)) THEN RAISE EXCEPTION 'price group update result mismatch'; END IF;
 ELSIF NEW.kind='model_alias.create' THEN
  SELECT * INTO STRICT a FROM model_aliases WHERE operation_id=NEW.operation_id;
  expected:=jsonb_build_object('model_id',a.model_id,'alias',a.alias);
  IF NEW.target<>('/api/platform/models/'||a.model_id||'/aliases') OR a.operation_id<>NEW.operation_id OR a.created_by<>NEW.actor OR a.created_at<>NEW.created_at OR a.state<>'active' OR a.revision<>1 OR NEW.revision<>1 OR NEW.result_snapshot<>pricing_model_alias_result(a) THEN RAISE EXCEPTION 'model alias create result mismatch'; END IF;
 ELSIF NEW.kind='model_alias.retire' THEN
  SELECT * INTO STRICT a FROM model_aliases WHERE operation_id=NEW.operation_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('alias',a.alias,'expected_revision',prior::text);
  IF NEW.resource_id<>a.model_id OR NEW.target<>('/api/platform/model-aliases/'||a.alias||'/retire') OR a.operation_id<>NEW.operation_id OR a.state<>'retired' OR a.revision<>prior+1 OR a.retired_at<>NEW.created_at OR NEW.revision<>a.revision OR NEW.result_snapshot<>pricing_model_alias_result(a) THEN RAISE EXCEPTION 'model alias retire result mismatch'; END IF;
 ELSIF NEW.kind='group_price.compile' THEN
  expected:=NEW.safe_input;
  IF NEW.target<>('/api/platform/price-groups/'||(NEW.resource_id)||'/price-versions/compile') OR NEW.revision IS NOT NULL OR (NEW.result_snapshot->>'group_id') IS DISTINCT FROM NEW.resource_id OR (NEW.result_snapshot->>'multiplier') IS DISTINCT FROM NEW.safe_input->>'multiplier' OR (NEW.result_snapshot->>'effective_at') IS DISTINCT FROM NEW.safe_input->>'effective_at' OR jsonb_typeof(NEW.result_snapshot->'items') IS DISTINCT FROM 'array' OR jsonb_array_length(NEW.result_snapshot->'items') NOT BETWEEN 1 AND 100 OR (SELECT count(*) FROM price_versions pv WHERE pv.pricing_operation_id=NEW.operation_id)<>jsonb_array_length(NEW.result_snapshot->'items') OR (SELECT count(DISTINCT item->>'price_version_id') FROM jsonb_array_elements(NEW.result_snapshot->'items') item)<>jsonb_array_length(NEW.result_snapshot->'items') OR EXISTS (SELECT 1 FROM jsonb_array_elements(NEW.result_snapshot->'items') item WHERE item->>'price_version_id' IS NULL OR NOT EXISTS (SELECT 1 FROM price_versions pv WHERE pv.id=item->>'price_version_id' AND pv.price_group_id=NEW.resource_id AND pv.pricing_operation_id=NEW.operation_id AND pv.created_by=NEW.actor AND pv.created_at=NEW.created_at AND pv.effective_at=(NEW.safe_input->>'effective_at')::timestamptz)) THEN RAISE EXCEPTION 'group price compile result mismatch'; END IF;
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSIF NEW.kind='group_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=pricing_price_result(p)-ARRAY['id','price_group_id','created_at'];
  IF p.price_group_id IS NULL OR (SELECT count(*) FROM price_versions WHERE pricing_operation_id=NEW.operation_id)<>1 OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor OR p.created_at<>NEW.created_at OR NEW.target<>('/api/platform/price-groups/'||p.price_group_id||'/price-versions') OR NEW.result_snapshot<>pricing_price_result(p) THEN RAISE EXCEPTION 'group price result mismatch'; END IF;
  PERFORM meter_group_price_complete(p.id); PERFORM meter_group_price_audit(NEW.operation_id);
 ELSIF NEW.kind='default_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=pricing_price_result(p)-ARRAY['id','price_group_id','created_at'];
  IF p.price_group_id IS NOT NULL OR (SELECT count(*) FROM price_versions WHERE pricing_operation_id=NEW.operation_id)<>1 OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor OR p.created_at<>NEW.created_at OR NEW.target<>'/api/platform/price-versions' OR NEW.result_snapshot<>pricing_price_result(p) THEN RAISE EXCEPTION 'default price result mismatch'; END IF;
  PERFORM meter_group_price_complete(p.id); PERFORM meter_group_price_audit(NEW.operation_id);
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
  IF NEW.target<>('/api/platform/tenants/'||t.id||'/pricing') OR NEW.revision<>t.pricing_revision OR NEW.result_snapshot<>pricing_assignment_result(t) THEN RAISE EXCEPTION 'tenant pricing result mismatch'; END IF;
  IF t.pricing_operation_id=NEW.operation_id THEN
   IF NOT has_event OR e.created_txid<>txid_current() OR e.tenant_id<>t.id OR e.actor<>NEW.actor OR e.after_group_id IS DISTINCT FROM t.price_group_id OR e.before_revision<>prior OR e.after_revision<>t.pricing_revision OR e.created_at<>NEW.created_at THEN RAISE EXCEPTION 'tenant pricing event mismatch'; END IF;
  ELSIF has_event OR t.pricing_revision<>prior THEN
   RAISE EXCEPTION 'tenant pricing no-op mismatch';
  END IF;
 END IF;
 IF NEW.safe_input IS DISTINCT FROM expected OR NEW.input_fingerprint IS DISTINCT FROM pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,expected) THEN RAISE EXCEPTION 'pricing command input mismatch'; END IF;
 RETURN NULL;
END $$;

REVOKE ALL ON TABLE model_aliases FROM PUBLIC;
REVOKE ALL ON FUNCTION phase1_compile_models(jsonb),phase1_compile_expected(jsonb) FROM PUBLIC;
REVOKE ALL ON FUNCTION phase1_requested_model_name_immutable(),phase1_model_name_lock(text),phase1_alias_name_guard(),phase1_model_name_guard(),pricing_model_alias_result(model_aliases) FROM PUBLIC;
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.phase1_compile_models(jsonb) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.phase1_compile_expected(jsonb) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.phase1_requested_model_name_immutable() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.phase1_model_name_lock(text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.phase1_alias_name_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.phase1_model_name_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.pricing_model_alias_result(%I.model_aliases) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.assert_pricing_safe_input(text,jsonb) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.pricing_fingerprint(text,text,text,text,jsonb) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.guard_pricing_operation_insert() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.require_pricing_operation_result() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.guard_group_price_insert() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.meter_group_price_audit(text) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.meter_group_price_audit_birth() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
 EXECUTE format('ALTER FUNCTION %I.meter_group_price_audit_insert() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;

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

-- 032 slices 2.4/2.5: tenant-scoped exact text response cache and the closed
-- cache-hit finalization branch. Existing migrations remain immutable.

ALTER TABLE route_policy_versions
    ADD COLUMN cache_ttl_seconds integer NOT NULL DEFAULT 0
        CHECK (cache_ttl_seconds=0 OR cache_ttl_seconds BETWEEN 60 AND 86400);

ALTER TABLE request_records
    ADD COLUMN cache_request_digest bytea
        CHECK (cache_request_digest IS NULL OR octet_length(cache_request_digest)=32);

ALTER TABLE finalization_records
    ADD COLUMN source text,
    ADD COLUMN source_event_id text,
    ADD COLUMN original_finalization_id text,
    ADD COLUMN cache_id text;

ALTER TABLE finalization_records DISABLE TRIGGER immutable_finalization;
UPDATE finalization_records f
SET source=e.source,source_event_id=e.source_event_id
FROM metering_events e
WHERE e.id=f.event_id AND e.tenant_id=f.tenant_id AND e.request_id=f.request_id;
ALTER TABLE finalization_records ENABLE TRIGGER immutable_finalization;

ALTER TABLE finalization_records
    ALTER COLUMN event_id DROP NOT NULL,
    ALTER COLUMN source SET NOT NULL,
    ALTER COLUMN source_event_id SET NOT NULL,
    ADD CONSTRAINT finalization_source_closed CHECK (
        source IN ('adapter','retrieval','gateway','approved_reconciliation','cache')),
    ADD CONSTRAINT finalization_cache_branch CHECK (
        (source='cache' AND event_id IS NULL AND attempt_id IS NULL
            AND original_finalization_id IS NOT NULL AND cache_id IS NOT NULL)
        OR (source<>'cache' AND event_id IS NOT NULL AND attempt_id IS NOT NULL
            AND original_finalization_id IS NULL AND cache_id IS NULL)),
    ADD CONSTRAINT finalization_source_identity_unique
        UNIQUE (tenant_id,source,source_event_id),
    ADD CONSTRAINT finalization_original_fk
        FOREIGN KEY (tenant_id,original_finalization_id)
        REFERENCES finalization_records(tenant_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE text_response_cache (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    tenant_id text NOT NULL REFERENCES tenants(id),
    protocol text NOT NULL,
    model_id text NOT NULL REFERENCES models(id),
    route_revision bigint NOT NULL CHECK(route_revision>0),
    request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
    response_body bytea NOT NULL CHECK(octet_length(response_body) BETWEEN 1 AND 8388608),
    retail_quantities_canonical bytea NOT NULL CHECK(octet_length(retail_quantities_canonical) BETWEEN 1 AND 8192),
    retail_quantities_digest bytea NOT NULL CHECK(octet_length(retail_quantities_digest)=32),
    finish_reason text NOT NULL CHECK(octet_length(finish_reason)<=80),
    original_request_id text NOT NULL,
    original_finalization_id text NOT NULL,
    original_source_event_id text NOT NULL,
    schema_id text NOT NULL,
    schema_digest bytea NOT NULL CHECK(octet_length(schema_digest)=32),
    profile_version text NOT NULL,
    variant_key text NOT NULL CHECK(variant_key='default'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    UNIQUE (tenant_id,protocol,model_id,route_revision,request_digest),
    FOREIGN KEY (tenant_id,original_request_id)
        REFERENCES request_records(tenant_id,id),
    FOREIGN KEY (tenant_id,original_finalization_id)
        REFERENCES finalization_records(tenant_id,id),
    CHECK(isfinite(created_at) AND isfinite(expires_at) AND expires_at>created_at)
);
CREATE INDEX text_response_cache_expiry ON text_response_cache(expires_at,id);

CREATE FUNCTION text_response_cache_complete() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF NOT pb_uuid(NEW.id) OR NEW.original_source_event_id='' THEN
  RAISE EXCEPTION 'invalid cache identity';
 END IF;
 IF jsonb_typeof(convert_from(NEW.response_body,'UTF8')::jsonb)<>'object' THEN
  RAISE EXCEPTION 'cache response is not a JSON object';
 END IF;
 IF NOT EXISTS (
  SELECT 1
  FROM request_records r
  JOIN finalization_records f ON f.tenant_id=r.tenant_id AND f.request_id=r.id
    AND f.id=r.latest_finalization_id AND f.id=NEW.original_finalization_id
  JOIN settlements st ON st.tenant_id=r.tenant_id AND st.request_id=r.id
    AND st.finalization_id=f.id AND st.status='settled'
  JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
  JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
    AND p.profile_version=r.profile_version AND p.cache_ttl_seconds>0
  WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.original_request_id
    AND r.protocol=NEW.protocol AND r.model_id=NEW.model_id
    AND r.route_revision=NEW.route_revision AND r.profile_version=NEW.profile_version
    AND r.cache_request_digest=NEW.request_digest
    AND NOT r.streaming AND r.result_kind='json'
    AND f.source<>'cache' AND f.event_id IS NOT NULL AND f.attempt_id IS NOT NULL
    AND f.retail_usage_state='confirmed_final' AND f.supplier_usage_state='confirmed_final'
    AND f.protocol_outcome='completed' AND f.transport_end_reason='done'
    AND f.finish_reason=NEW.finish_reason AND f.finish_reason<>'tool_calls'
    AND f.source_event_id=NEW.original_source_event_id
    AND f.retail_quantities_canonical=NEW.retail_quantities_canonical
    AND f.retail_quantities_digest=NEW.retail_quantities_digest
    AND q.schema_id=NEW.schema_id AND q.schema_digest=NEW.schema_digest
    AND q.variant_key=NEW.variant_key
    AND NEW.expires_at=NEW.created_at+p.cache_ttl_seconds*interval '1 second'
 ) THEN
  RAISE EXCEPTION 'cache response does not match an eligible settled original';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER text_response_cache_complete
AFTER INSERT ON text_response_cache DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION text_response_cache_complete();

CREATE FUNCTION text_response_cache_mutation() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'cache entries are immutable'; END IF;
 IF OLD.expires_at>clock_timestamp() THEN
  RAISE EXCEPTION 'unexpired cache entry cannot be deleted';
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER text_response_cache_mutation
BEFORE UPDATE OR DELETE ON text_response_cache
FOR EACH ROW EXECUTE FUNCTION text_response_cache_mutation();

CREATE FUNCTION cache_request_digest_immutable() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF NEW.cache_request_digest IS DISTINCT FROM OLD.cache_request_digest THEN
  RAISE EXCEPTION 'cache request digest is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER cache_request_digest_immutable
BEFORE UPDATE ON request_records FOR EACH ROW
EXECUTE FUNCTION cache_request_digest_immutable();

CREATE FUNCTION cache_hit_no_attempt() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM request_records r JOIN finalization_records f
   ON f.tenant_id=r.tenant_id AND f.request_id=r.id AND f.id=r.latest_finalization_id
  WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id AND f.source='cache'
 ) THEN
  RAISE EXCEPTION 'cache hit cannot acquire an upstream attempt';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER cache_hit_no_attempt
BEFORE INSERT ON upstream_attempts FOR EACH ROW
EXECUTE FUNCTION cache_hit_no_attempt();

-- The original event-backed validator remains authoritative for every ordinary
-- finalization. Cache rows use the separate closed validator below.
CREATE OR REPLACE FUNCTION meter_finalization_complete() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE e metering_events%ROWTYPE; in_q bigint; out_q bigint; retail_state text; supplier_state text;
 retail_canonical bytea; retail_digest bytea; supplier_canonical bytea; supplier_digest bytea;
BEGIN
 IF NEW.source='cache' THEN RETURN NULL; END IF;
 SELECT * INTO e FROM metering_events WHERE id=NEW.event_id;
 IF NOT FOUND OR e.tenant_id IS DISTINCT FROM NEW.tenant_id OR e.request_id IS DISTINCT FROM NEW.request_id
    OR e.attempt_id IS DISTINCT FROM NEW.attempt_id
    OR NEW.source IS DISTINCT FROM e.source OR NEW.source_event_id IS DISTINCT FROM e.source_event_id
    OR NEW.original_finalization_id IS NOT NULL OR NEW.cache_id IS NOT NULL THEN
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

CREATE FUNCTION cache_finalization_complete() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE zero bytea;
BEGIN
 IF NEW.source<>'cache' THEN RETURN NULL; END IF;
 IF NEW.source_event_id='' OR octet_length(NEW.source_event_id)>200 THEN
  RAISE EXCEPTION 'invalid cache finalization identity';
 END IF;
 SELECT meter_side_quantity_canonical(q.schema_id,'supplier',0,0) INTO zero
 FROM request_meter_snapshots q
 WHERE q.tenant_id=NEW.tenant_id AND q.request_id=NEW.request_id;
 IF zero IS NULL THEN RAISE EXCEPTION 'cache finalization schema is missing'; END IF;
 IF NEW.event_id IS NOT NULL OR NEW.attempt_id IS NOT NULL
    OR NEW.retail_usage_state<>'confirmed_final' OR NEW.supplier_usage_state<>'confirmed_final'
    OR NEW.supplier_quantities_canonical IS DISTINCT FROM zero
    OR NEW.supplier_quantities_digest IS DISTINCT FROM meter_final_digest((SELECT q.schema_digest FROM request_meter_snapshots q WHERE q.tenant_id=NEW.tenant_id AND q.request_id=NEW.request_id),'supplier',zero)
    OR NEW.protocol_outcome<>'completed' OR NEW.transport_end_reason<>'done'
    OR NEW.delivery_stage<>'none' OR NEW.content_written THEN
  RAISE EXCEPTION 'invalid cache finalization branch';
 END IF;
 IF NOT EXISTS (
  SELECT 1
  FROM request_records r
  JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
  JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
    AND p.profile_version=r.profile_version AND p.cache_ttl_seconds>0
  JOIN text_response_cache c ON c.tenant_id=r.tenant_id AND c.id=NEW.cache_id
    AND c.protocol=r.protocol AND c.model_id=r.model_id AND c.route_revision=r.route_revision
    AND c.request_digest=r.cache_request_digest AND c.profile_version=r.profile_version
    AND c.schema_id=q.schema_id AND c.schema_digest=q.schema_digest AND c.variant_key=q.variant_key
    AND c.expires_at>clock_timestamp()
    AND c.expires_at=c.created_at+p.cache_ttl_seconds*interval '1 second'
  JOIN finalization_records o ON o.tenant_id=r.tenant_id AND o.id=NEW.original_finalization_id
    AND o.id=c.original_finalization_id AND o.request_id=c.original_request_id
    AND o.source<>'cache' AND o.event_id IS NOT NULL AND o.attempt_id IS NOT NULL
    AND o.source_event_id=c.original_source_event_id
    AND o.retail_usage_state='confirmed_final' AND o.supplier_usage_state='confirmed_final'
    AND o.retail_quantities_canonical=NEW.retail_quantities_canonical
    AND o.retail_quantities_digest=NEW.retail_quantities_digest
    AND o.retail_quantities_canonical=c.retail_quantities_canonical
    AND o.retail_quantities_digest=c.retail_quantities_digest
    AND o.protocol_outcome='completed' AND o.transport_end_reason='done'
    AND o.finish_reason=NEW.finish_reason AND o.finish_reason=c.finish_reason
    AND o.finish_reason<>'tool_calls'
  JOIN request_records original_request ON original_request.tenant_id=o.tenant_id
    AND original_request.id=o.request_id AND original_request.latest_finalization_id=o.id
    AND original_request.protocol=r.protocol AND original_request.model_id=r.model_id
    AND original_request.route_revision=r.route_revision
    AND original_request.profile_version=r.profile_version
    AND original_request.cache_request_digest=c.request_digest
    AND NOT original_request.streaming AND original_request.result_kind='json'
  JOIN settlements original_settlement ON original_settlement.tenant_id=o.tenant_id
    AND original_settlement.request_id=o.request_id AND original_settlement.finalization_id=o.id
    AND original_settlement.status='settled'
  WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id
    AND r.latest_finalization_id=NEW.id AND r.policy_version=NEW.policy_version
    AND NOT r.streaming AND r.result_kind='json'
    AND NOT EXISTS(SELECT 1 FROM upstream_attempts a WHERE a.tenant_id=r.tenant_id AND a.request_id=r.id)
 ) THEN
  RAISE EXCEPTION 'cache finalization does not match its original and key';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER cache_finalization_complete
AFTER INSERT ON finalization_records DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION cache_finalization_complete();

-- Read the final retail vector directly for both ordinary and cache settlement.
-- The ordinary values remain identical to the prior event-quantity projection.
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
 SELECT f.retail_usage_state,r.amount,
   (convert_from(f.retail_quantities_canonical,'UTF8')::jsonb->'quantities'->0->>'quantity')::bigint,
   (convert_from(f.retail_quantities_canonical,'UTF8')::jsonb->'quantities'->1->>'quantity')::bigint,
   ri.numerator,ri.denominator,ro.numerator,ro.denominator
   INTO state,reserved,input_qty,output_qty,in_n,in_d,out_n,out_d
   FROM finalization_records f
   JOIN request_meter_snapshots s ON s.tenant_id=f.tenant_id AND s.request_id=f.request_id
   JOIN reservations r ON r.tenant_id=s.tenant_id AND r.request_id=s.request_id
   JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
    AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
   JOIN price_rate_lines ri ON ri.price_version_id=p.id AND ri.dimension='text_input_token' AND ri.unit='token'
   JOIN price_rate_lines ro ON ro.price_version_id=p.id AND ro.dimension='text_output_token' AND ro.unit='token'
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

REVOKE ALL ON TABLE text_response_cache FROM PUBLIC;
REVOKE ALL ON FUNCTION text_response_cache_complete(),text_response_cache_mutation(),
 cache_request_digest_immutable(),cache_hit_no_attempt(),cache_finalization_complete() FROM PUBLIC;

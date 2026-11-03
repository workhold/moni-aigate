-- Exact same-source pure account helpers for the independent A2 candidate.
-- Do not install the old command-only writer, validators or triggers.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_value text,p_nonzero boolean)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
 SELECT coalesce(p_value ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND (NOT p_nonzero OR p_value<>'00000000-0000-0000-0000-000000000000'),false)
$$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_text_v1(p_value text)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE i integer; n integer; ch integer;
BEGIN
 IF p_value IS NULL OR pg_catalog.octet_length(p_value) NOT BETWEEN 1 AND 100 THEN RETURN false; END IF;
 n:=pg_catalog.char_length(p_value);
 FOR i IN 1..n LOOP
  ch:=pg_catalog.ascii(pg_catalog.substr(p_value,i,1));
  IF ch BETWEEN 0 AND 31 OR ch BETWEEN 127 AND 159 THEN RETURN false; END IF;
  IF i IN (1,n) AND (ch BETWEEN 9 AND 13 OR ch IN (32,133,160,5760,8232,8233,8239,8287,12288) OR ch BETWEEN 8192 AND 8202) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_command_digest_v1(p_kind text,p_operation text,p_target text,p_expected bigint,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS text LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE v_payload text;
BEGIN
 IF p_kind NOT IN ('account_create','account_configure') OR p_operation IS NULL OR p_target IS NULL OR p_label IS NULL OR p_provider IS NULL OR p_state IS NULL OR p_limit IS NULL OR (p_kind='account_configure' AND p_expected IS NULL) THEN RETURN NULL; END IF;
 v_payload:='{"operation_id":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_operation);
 IF p_kind='account_configure' THEN v_payload:=v_payload||',"expected_revision":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_expected::text); END IF;
 v_payload:=v_payload||',"label":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_label)||',"provider":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_provider)||',"state":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_state)||',"limit_amount":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_limit::text)||'}';
 RETURN pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to('{"Kind":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_kind)||',"Target":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_target)||',"Payload":'||v_payload||'}','UTF8')),'hex');
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_time_v1(p_time timestamptz)
RETURNS text LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF p_time IS NULL OR NOT pg_catalog.isfinite(p_time) OR extract(year FROM p_time AT TIME ZONE 'UTC') NOT BETWEEN 1 AND 9999 THEN
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 RETURN pg_catalog.rtrim(pg_catalog.rtrim(pg_catalog.to_char(p_time AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US'),'0'),'.')||'Z';
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_snapshot_v1(p_id text,p_label text,p_provider text,p_state text,p_limit bigint,p_held numeric,p_spent numeric,p_revision bigint,p_time timestamptz)
RETURNS jsonb LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_provider) OR p_state IS NULL OR p_state NOT IN ('active','paused') OR p_limit IS NULL OR p_limit<0 OR p_revision IS NULL OR p_revision<=0 OR p_held IS NULL OR p_spent IS NULL OR p_held::text !~ '^(0|[1-9][0-9]{0,99})$' OR p_spent::text !~ '^(0|[1-9][0-9]{0,99})$' THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN pg_catalog.jsonb_build_object('id',p_id,'label',p_label,'provider',p_provider,'state',p_state,'limit_amount',p_limit::text,'held_amount',p_held::text,'spent_amount',p_spent::text,'revision',p_revision::text,'created_at',{{MONI_SCHEMA}}.pp_ac_time_v1(p_time));
END $$;

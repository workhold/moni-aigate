-- B-P4-A0 original-attempt rules, version 1. Not an installer or business root.
-- Replace {{MONI_SCHEMA}} once with a safely quoted trusted schema identifier.
-- pp_json_string_v1 is the separately frozen original six-function identity
-- dependency. This material does not recreate or alter that dependency.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_at_uuid_parse_v1(value pg_catalog.text)
RETURNS pg_catalog.bool
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
AS $pp_at_uuid_parse_v1$
DECLARE size pg_catalog.int4; core pg_catalog.text;
BEGIN
 IF value IS NULL THEN RETURN false; END IF;
 size:=pg_catalog.octet_length(value);
 IF size OPERATOR(pg_catalog.=) 32 THEN
  RETURN value COLLATE pg_catalog."C" OPERATOR(pg_catalog.~) '^[0-9A-Fa-f]{32}$';
 ELSIF size OPERATOR(pg_catalog.=) 36 THEN
  core:=value;
 ELSIF size OPERATOR(pg_catalog.=) 45 THEN
  IF pg_catalog.translate(pg_catalog.left(value,9),'URNID','urnid') COLLATE pg_catalog."C"
   OPERATOR(pg_catalog.<>) 'urn:uuid:' THEN RETURN false; END IF;
  core:=pg_catalog.substr(value,10);
 ELSIF size OPERATOR(pg_catalog.=) 38 THEN
  -- google/uuid v1.6.0 Parse discards both outside bytes without checking {}.
  -- With PG-valid UTF8, a multibyte outside character cannot be one such byte.
  IF pg_catalog.ascii(pg_catalog.left(value,1)) OPERATOR(pg_catalog.>) 127
   OR pg_catalog.ascii(pg_catalog.right(value,1)) OPERATOR(pg_catalog.>) 127 THEN
   RETURN false;
  END IF;
  core:=pg_catalog.substr(value,2,36);
 ELSE RETURN false;
 END IF;
 RETURN pg_catalog.octet_length(core) OPERATOR(pg_catalog.=) 36
  AND core COLLATE pg_catalog."C" OPERATOR(pg_catalog.~)
   '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$';
END
$pp_at_uuid_parse_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_at_route_json_v1(
 decisions pg_catalog.jsonb, selected_channel pg_catalog.text, selected_version pg_catalog.text)
RETURNS pg_catalog.bytea
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
AS $pp_at_route_json_v1$
DECLARE count pg_catalog.int4; selected pg_catalog.int4:=0;
 item pg_catalog.jsonb; channel pg_catalog.text; version pg_catalog.text;
 priority pg_catalog.text; weight pg_catalog.text; reason pg_catalog.text;
 seen pg_catalog.text[]:=ARRAY[]::pg_catalog.text[];
 result pg_catalog.text:='['; separator pg_catalog.text:='';
BEGIN
 IF decisions IS NULL OR pg_catalog.jsonb_typeof(decisions) OPERATOR(pg_catalog.<>) 'array' THEN
  RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
 END IF;
 count:=pg_catalog.jsonb_array_length(decisions);
 IF count OPERATOR(pg_catalog.>) 1000 THEN
  RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='too many route decisions';
 END IF;
 IF count OPERATOR(pg_catalog.=) 0 THEN RETURN pg_catalog.convert_to('[]','UTF8'); END IF;
 FOR item IN SELECT e.value FROM pg_catalog.jsonb_array_elements(decisions) WITH ORDINALITY AS e(value,ordinal)
  ORDER BY e.ordinal LOOP
  IF pg_catalog.jsonb_typeof(item) OPERATOR(pg_catalog.<>) 'object' THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
  END IF;
  IF NOT (item OPERATOR(pg_catalog.?&) ARRAY['channel_id','version_id','priority','weight','reason'])
   OR EXISTS(SELECT 1 FROM pg_catalog.jsonb_object_keys(item) AS k(name)
    WHERE NOT (k.name COLLATE pg_catalog."C" OPERATOR(pg_catalog.=)
     ANY(ARRAY['channel_id','version_id','priority','weight','reason']))) THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
  END IF;
  IF pg_catalog.jsonb_typeof(item OPERATOR(pg_catalog.->) 'channel_id') OPERATOR(pg_catalog.<>) 'string'
   OR pg_catalog.jsonb_typeof(item OPERATOR(pg_catalog.->) 'version_id') OPERATOR(pg_catalog.<>) 'string'
   OR pg_catalog.jsonb_typeof(item OPERATOR(pg_catalog.->) 'reason') OPERATOR(pg_catalog.<>) 'string'
   OR pg_catalog.jsonb_typeof(item OPERATOR(pg_catalog.->) 'priority') OPERATOR(pg_catalog.<>) 'number'
   OR pg_catalog.jsonb_typeof(item OPERATOR(pg_catalog.->) 'weight') OPERATOR(pg_catalog.<>) 'number' THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
  END IF;
  channel:=item OPERATOR(pg_catalog.->>) 'channel_id';
  version:=item OPERATOR(pg_catalog.->>) 'version_id';
  priority:=item OPERATOR(pg_catalog.->>) 'priority';
  weight:=item OPERATOR(pg_catalog.->>) 'weight';
  reason:=item OPERATOR(pg_catalog.->>) 'reason';
  -- Bound numeric text before any cast. JSONB already normalizes some exponent
  -- forms; stored decimal fractions still do not represent typed Go integers.
  IF pg_catalog.octet_length(priority) OPERATOR(pg_catalog.>) 20
   OR NOT (priority COLLATE pg_catalog."C" OPERATOR(pg_catalog.~) '^-?(0|[1-9][0-9]*)$')
   OR pg_catalog.octet_length(weight) OPERATOR(pg_catalog.>) 20
   OR NOT (weight COLLATE pg_catalog."C" OPERATOR(pg_catalog.~) '^[1-9][0-9]*$') THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
  END IF;
  IF priority::pg_catalog.numeric OPERATOR(pg_catalog.<) '-9223372036854775808'::pg_catalog.numeric
   OR priority::pg_catalog.numeric OPERATOR(pg_catalog.>) '9223372036854775807'::pg_catalog.numeric
   OR weight::pg_catalog.numeric OPERATOR(pg_catalog.>) '18446744073709551615'::pg_catalog.numeric THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
  END IF;
  IF NOT {{MONI_SCHEMA}}.pp_at_uuid_parse_v1(channel) OR NOT {{MONI_SCHEMA}}.pp_at_uuid_parse_v1(version)
   OR channel COLLATE pg_catalog."C" OPERATOR(pg_catalog.=) ANY(seen) THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route decision';
  END IF;
  seen:=pg_catalog.array_append(seen,channel);
  IF NOT (reason COLLATE pg_catalog."C" OPERATOR(pg_catalog.=) ANY(ARRAY[
   'selected','weighted_not_selected','lower_priority','disabled','already_tried','capacity_full',
   'quarantined','circuit_open','half_open_busy','channel_quarantined','credential_revoked',
   'binding_isolated','region_forbidden','cost_unknown','cost_limit_exceeded','higher_cost',
   'procurement_paused','procurement_budget_exceeded','procurement_bound_invalid',
   'procurement_budget_unavailable'])) THEN
   RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='invalid route reason';
  END IF;
  IF reason COLLATE pg_catalog."C" OPERATOR(pg_catalog.=) 'selected' THEN
   selected:=selected OPERATOR(pg_catalog.+) 1;
   IF channel COLLATE pg_catalog."C" IS DISTINCT FROM selected_channel COLLATE pg_catalog."C"
    OR version COLLATE pg_catalog."C" IS DISTINCT FROM selected_version COLLATE pg_catalog."C" THEN
    RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='selected route mismatch';
   END IF;
  END IF;
  result:=result OPERATOR(pg_catalog.||) separator OPERATOR(pg_catalog.||)
   '{"channel_id":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(channel)
   OPERATOR(pg_catalog.||) ',"version_id":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(version)
   OPERATOR(pg_catalog.||) ',"priority":' OPERATOR(pg_catalog.||) (priority::pg_catalog.int8)::pg_catalog.text
   OPERATOR(pg_catalog.||) ',"weight":' OPERATOR(pg_catalog.||) weight
   OPERATOR(pg_catalog.||) ',"reason":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(reason)
   OPERATOR(pg_catalog.||) '}';
  separator:=',';
 END LOOP;
 IF selected OPERATOR(pg_catalog.<>) 1 THEN
  RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='route needs one selected candidate';
 END IF;
 result:=result OPERATOR(pg_catalog.||) ']';
 IF pg_catalog.octet_length(result) OPERATOR(pg_catalog.>) 262144 THEN
  RAISE EXCEPTION USING ERRCODE='PP101',MESSAGE='route decisions too large';
 END IF;
 RETURN pg_catalog.convert_to(result,'UTF8');
END
$pp_at_route_json_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_at_expected_shape_v1(
 has_expected pg_catalog.bool, expected_version pg_catalog.bytea, expected_input pg_catalog.int8,
 expected_output pg_catalog.int8, expected_basis pg_catalog.bytea)
RETURNS pg_catalog.bool
LANGUAGE sql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
AS $pp_at_expected_shape_v1$
 SELECT CASE WHEN has_expected IS FALSE THEN
  expected_version IS NULL AND expected_input IS NULL AND expected_output IS NULL AND expected_basis IS NULL
 WHEN has_expected IS TRUE THEN expected_version IS NOT NULL AND expected_basis IS NOT NULL
 ELSE false END
$pp_at_expected_shape_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_at_quote_equal_v1(
 has_expected pg_catalog.bool, expected_version pg_catalog.bytea, expected_input pg_catalog.int8,
 expected_output pg_catalog.int8, expected_basis pg_catalog.bytea,
 actual_version pg_catalog.text, actual_input pg_catalog.int8, actual_output pg_catalog.int8,
 actual_basis pg_catalog.text)
RETURNS pg_catalog.bool
LANGUAGE sql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
AS $pp_at_quote_equal_v1$
 SELECT CASE WHEN has_expected IS TRUE AND actual_basis IS NOT NULL
  AND {{MONI_SCHEMA}}.pp_at_expected_shape_v1(has_expected,expected_version,expected_input,expected_output,expected_basis)
 THEN expected_version OPERATOR(pg_catalog.=) pg_catalog.convert_to(coalesce(actual_version,''),'UTF8')
  AND expected_basis OPERATOR(pg_catalog.=) pg_catalog.convert_to(actual_basis,'UTF8')
  AND expected_input IS NOT DISTINCT FROM actual_input
  AND expected_output IS NOT DISTINCT FROM actual_output
 ELSE false END
$pp_at_quote_equal_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_at_current_quote_v1(channel pg_catalog.text,upstream_model pg_catalog.text)
RETURNS TABLE(quote_selected_at pg_catalog.timestamptz,cost_version_id pg_catalog.text,
 input_rate pg_catalog.int8,output_rate pg_catalog.int8,basis pg_catalog.text)
LANGUAGE sql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
AS $pp_at_current_quote_v1$
 WITH instant AS MATERIALIZED (SELECT pg_catalog.clock_timestamp() AS at)
 SELECT instant.at,c.id,c.input_rate,c.output_rate,coalesce(c.basis,'unknown')
 FROM instant LEFT JOIN LATERAL (
  SELECT cv.id,cv.input_rate,cv.output_rate,cv.basis
  FROM {{MONI_SCHEMA}}.cost_versions AS cv
  WHERE cv.channel_id OPERATOR(pg_catalog.=) $1
   AND cv.upstream_model OPERATOR(pg_catalog.=) $2
   AND cv.effective_at OPERATOR(pg_catalog.<=) instant.at
  ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1
 ) AS c ON true
$pp_at_current_quote_v1$;

REVOKE EXECUTE ON FUNCTION
 {{MONI_SCHEMA}}.pp_at_uuid_parse_v1(pg_catalog.text),
 {{MONI_SCHEMA}}.pp_at_route_json_v1(pg_catalog.jsonb,pg_catalog.text,pg_catalog.text),
 {{MONI_SCHEMA}}.pp_at_expected_shape_v1(pg_catalog.bool,pg_catalog.bytea,pg_catalog.int8,pg_catalog.int8,pg_catalog.bytea),
 {{MONI_SCHEMA}}.pp_at_quote_equal_v1(pg_catalog.bool,pg_catalog.bytea,pg_catalog.int8,pg_catalog.int8,pg_catalog.bytea,pg_catalog.text,pg_catalog.int8,pg_catalog.int8,pg_catalog.text),
 {{MONI_SCHEMA}}.pp_at_current_quote_v1(pg_catalog.text,pg_catalog.text)
FROM PUBLIC;

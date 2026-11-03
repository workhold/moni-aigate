-- B-P4-D1-I pure identity ABI leaf, version 1. This material is not an installer.
-- Replace {{MONI_SCHEMA}} only with one safely quoted trusted schema identifier.
-- These functions perform byte conversion only; they do not validate business
-- inputs, read database facts, write financial state, or grant runtime authority.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_json_string_v1(value pg_catalog.text)
RETURNS pg_catalog.text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SECURITY INVOKER
AS $pp_json_string_v1$
 SELECT pg_catalog.replace(
  pg_catalog.replace(
   pg_catalog.replace(
    pg_catalog.replace(
     pg_catalog.replace(pg_catalog.to_json(value)::pg_catalog.text,
      '<', pg_catalog.chr(92) OPERATOR(pg_catalog.||) 'u003c'),
     '>', pg_catalog.chr(92) OPERATOR(pg_catalog.||) 'u003e'),
    '&', pg_catalog.chr(92) OPERATOR(pg_catalog.||) 'u0026'),
   pg_catalog.chr(8232), pg_catalog.chr(92) OPERATOR(pg_catalog.||) 'u2028'),
  pg_catalog.chr(8233), pg_catalog.chr(92) OPERATOR(pg_catalog.||) 'u2029')
$pp_json_string_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_usage_json_v1(
 status pg_catalog.text,
 input_tokens pg_catalog.int8,
 output_tokens pg_catalog.int8,
 total_tokens pg_catalog.int8,
 cached_tokens pg_catalog.int8,
 reasoning_tokens pg_catalog.int8)
RETURNS pg_catalog.text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SECURITY INVOKER
AS $pp_usage_json_v1$
 SELECT '{"status":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(status)
  OPERATOR(pg_catalog.||) ',"input_tokens":' OPERATOR(pg_catalog.||) input_tokens::pg_catalog.text
  OPERATOR(pg_catalog.||) ',"output_tokens":' OPERATOR(pg_catalog.||) output_tokens::pg_catalog.text
  OPERATOR(pg_catalog.||) ',"total_tokens":' OPERATOR(pg_catalog.||) total_tokens::pg_catalog.text
  OPERATOR(pg_catalog.||) ',"cached_tokens":' OPERATOR(pg_catalog.||) cached_tokens::pg_catalog.text
  OPERATOR(pg_catalog.||) ',"reasoning_tokens":' OPERATOR(pg_catalog.||) reasoning_tokens::pg_catalog.text OPERATOR(pg_catalog.||) '}'
$pp_usage_json_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_finalization_identity_v1(
 id pg_catalog.text,
 tenant_id pg_catalog.text,
 request_id pg_catalog.text,
 attempt_id pg_catalog.text,
 source pg_catalog.text,
 source_event_id pg_catalog.text,
 usage_status pg_catalog.text,
 input_tokens pg_catalog.int8,
 output_tokens pg_catalog.int8,
 total_tokens pg_catalog.int8,
 cached_tokens pg_catalog.int8,
 reasoning_tokens pg_catalog.int8,
 protocol_outcome pg_catalog.text,
 transport_end_reason pg_catalog.text,
 delivery_stage pg_catalog.text,
 finish_reason pg_catalog.text,
 content_written pg_catalog.bool)
RETURNS pg_catalog.bytea
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SECURITY INVOKER
AS $pp_finalization_identity_v1$
 SELECT pg_catalog.convert_to(
  '{"ID":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(id)
  OPERATOR(pg_catalog.||) ',"TenantID":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(tenant_id)
  OPERATOR(pg_catalog.||) ',"RequestID":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(request_id)
  OPERATOR(pg_catalog.||) ',"AttemptID":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(attempt_id)
  OPERATOR(pg_catalog.||) ',"Source":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(source)
  OPERATOR(pg_catalog.||) ',"SourceEventID":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(source_event_id)
  OPERATOR(pg_catalog.||) ',"Usage":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_usage_json_v1(usage_status,
   input_tokens, output_tokens, total_tokens, cached_tokens, reasoning_tokens)
  OPERATOR(pg_catalog.||) ',"ProtocolOutcome":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(protocol_outcome)
  OPERATOR(pg_catalog.||) ',"TransportEndReason":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(transport_end_reason)
  OPERATOR(pg_catalog.||) ',"DeliveryStage":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(delivery_stage)
  OPERATOR(pg_catalog.||) ',"FinishReason":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(finish_reason)
  OPERATOR(pg_catalog.||) ',"ContentWritten":' OPERATOR(pg_catalog.||) content_written::pg_catalog.text
  OPERATOR(pg_catalog.||) ',"ObservedAt":"0001-01-01T00:00:00Z"}', 'UTF8')
$pp_finalization_identity_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_evidence_identity_v1(
 status pg_catalog.text,
 input_tokens pg_catalog.int8,
 output_tokens pg_catalog.int8,
 total_tokens pg_catalog.int8,
 cached_tokens pg_catalog.int8,
 reasoning_tokens pg_catalog.int8,
 reason pg_catalog.text)
RETURNS pg_catalog.bytea
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SECURITY INVOKER
AS $pp_evidence_identity_v1$
 SELECT pg_catalog.convert_to(
  '{"Usage":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_usage_json_v1(status,
   input_tokens, output_tokens, total_tokens, cached_tokens, reasoning_tokens)
  OPERATOR(pg_catalog.||) ',"Reason":' OPERATOR(pg_catalog.||) {{MONI_SCHEMA}}.pp_json_string_v1(reason) OPERATOR(pg_catalog.||) '}', 'UTF8')
$pp_evidence_identity_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_finalization_digest_v1(
 id pg_catalog.text,
 tenant_id pg_catalog.text,
 request_id pg_catalog.text,
 attempt_id pg_catalog.text,
 source pg_catalog.text,
 source_event_id pg_catalog.text,
 usage_status pg_catalog.text,
 input_tokens pg_catalog.int8,
 output_tokens pg_catalog.int8,
 total_tokens pg_catalog.int8,
 cached_tokens pg_catalog.int8,
 reasoning_tokens pg_catalog.int8,
 protocol_outcome pg_catalog.text,
 transport_end_reason pg_catalog.text,
 delivery_stage pg_catalog.text,
 finish_reason pg_catalog.text,
 content_written pg_catalog.bool)
RETURNS pg_catalog.text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SECURITY INVOKER
AS $pp_finalization_digest_v1$
 SELECT pg_catalog.encode(pg_catalog.sha256(
  {{MONI_SCHEMA}}.pp_finalization_identity_v1(id, tenant_id, request_id, attempt_id,
   source, source_event_id, usage_status, input_tokens, output_tokens,
   total_tokens, cached_tokens, reasoning_tokens, protocol_outcome,
   transport_end_reason, delivery_stage, finish_reason, content_written)), 'hex')
$pp_finalization_digest_v1$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_evidence_digest_v1(
 status pg_catalog.text,
 input_tokens pg_catalog.int8,
 output_tokens pg_catalog.int8,
 total_tokens pg_catalog.int8,
 cached_tokens pg_catalog.int8,
 reasoning_tokens pg_catalog.int8,
 reason pg_catalog.text)
RETURNS pg_catalog.text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SECURITY INVOKER
AS $pp_evidence_digest_v1$
 SELECT pg_catalog.encode(pg_catalog.sha256(
  {{MONI_SCHEMA}}.pp_evidence_identity_v1(status, input_tokens, output_tokens,
   total_tokens, cached_tokens, reasoning_tokens, reason)), 'hex')
$pp_evidence_digest_v1$;

REVOKE EXECUTE ON FUNCTION
 {{MONI_SCHEMA}}.pp_json_string_v1(pg_catalog.text),
 {{MONI_SCHEMA}}.pp_usage_json_v1(pg_catalog.text,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8),
 {{MONI_SCHEMA}}.pp_finalization_identity_v1(pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.bool),
 {{MONI_SCHEMA}}.pp_evidence_identity_v1(pg_catalog.text,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.text),
 {{MONI_SCHEMA}}.pp_finalization_digest_v1(pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.text,pg_catalog.bool),
 {{MONI_SCHEMA}}.pp_evidence_digest_v1(pg_catalog.text,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.int8,pg_catalog.text)
FROM PUBLIC;

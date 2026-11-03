-- Reject mixed candidates before any relation/function mutation. This is an
-- installation prerequisite check, never an in-place upgrade or a DROP plan.
DO $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname={{MONI_SCHEMA_TEXT}})
  OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname={{MONI_SCHEMA_TEXT}} AND (p.proname LIKE 'pp_rb_%' OR (p.proname LIKE 'pp_ac_%' AND p.proname NOT IN ('pp_ac_uuid_v1','pp_ac_text_v1','pp_ac_time_v1','pp_ac_snapshot_v1','pp_ac_command_digest_v1'))))
 THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname={{MONI_SCHEMA_TEXT}} AND c.relname IN ('pp_operation_roots','pp_root_steps','pp_request_origins','pp_request_heads','pp_attempt_origins','pp_attempt_heads','pp_attempt_nodes','pp_account_command_sources','pp_account_origins','pp_account_heads','pp_account_journal','pp_procurement_transitions'))
 THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 -- to_regprocedure uses the explicit quoted namespace, never a caller path.
 IF pg_catalog.to_regprocedure(pg_catalog.format('%I.pp_json_string_v1(text)',{{MONI_SCHEMA_TEXT}})) IS NULL
  OR pg_catalog.to_regprocedure(pg_catalog.format('%I.pp_at_current_quote_v1(text,text)',{{MONI_SCHEMA_TEXT}})) IS NULL
  OR pg_catalog.to_regclass(pg_catalog.format('%I.periodic_budget_observation_journal',{{MONI_SCHEMA_TEXT}})) IS NULL
 THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
END $$;

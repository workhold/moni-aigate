-- Necessary execution conditions only; these helpers do not prove birth phase.
-- They neither establish authority for caller-supplied rows nor reset timeouts.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_require_writer_v1()
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN
  RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901';
 END IF;
 IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('statement_timeout')::interval<>interval '5 seconds' OR pg_catalog.current_setting('lock_timeout')::interval<>interval '3 seconds' THEN
  RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903';
 END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_require_root_v1()
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
END $$;

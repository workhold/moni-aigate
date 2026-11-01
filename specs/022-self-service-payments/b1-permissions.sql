-- Offline identifier template; render :"..." using pgx.Identifier, never string
-- interpolation of untrusted identifiers. Run after ProvisionFor's blanket grants.
-- Existing A and projection grants stay in effect; new worker starts empty.
GRANT USAGE ON SCHEMA :"schema" TO :"app_role",:"worker_role",:"projection_role";
REVOKE CREATE ON SCHEMA :"schema" FROM PUBLIC,:"app_role",:"worker_role",:"projection_role";
REVOKE ALL ON ALL TABLES IN SCHEMA :"schema" FROM :"worker_role";
REVOKE ALL ON payment_sandbox_environment,payment_policy,payment_skus,payment_sku_versions,payment_sku_changes,payment_orders,payment_cancel_intents,payment_command_outbox,payment_command_observations,payment_query_attempts,payment_inbox,payment_event_conflicts,payment_verified_queries,payment_query_conflicts,payment_verified_evidence,payment_evidence_processing,payment_provider_revisions,payment_transaction_identities,payment_order_capture_selections,payment_captures,payment_effects,payment_review_facts FROM :"app_role",:"projection_role";
GRANT SELECT ON payment_sandbox_environment,payment_policy,payment_skus,payment_sku_versions,payment_sku_changes,payment_orders,payment_cancel_intents,payment_command_outbox,payment_command_observations,payment_inbox,payment_event_conflicts,payment_verified_evidence,payment_evidence_processing,payment_provider_revisions,payment_transaction_identities,payment_order_capture_selections,payment_captures,payment_effects,payment_review_facts TO :"app_role";
GRANT INSERT ON payment_skus,payment_sku_versions,payment_sku_changes,payment_orders,payment_cancel_intents,payment_command_outbox,payment_inbox,payment_event_conflicts,payment_verified_evidence,payment_evidence_processing TO :"app_role";
GRANT UPDATE(enabled) ON payment_policy TO :"app_role";
GRANT UPDATE(revision,last_version,enabled,current_version_id,last_change_operation_id) ON payment_skus TO :"app_role";
-- Row locking needs a column UPDATE privilege, but actual root mutations reject.
GRANT UPDATE(id) ON payment_orders TO :"app_role";
GRANT SELECT ON payment_sandbox_environment,payment_sku_versions,payment_orders,payment_cancel_intents,payment_command_outbox,payment_command_observations,payment_query_attempts,payment_inbox,payment_event_conflicts,payment_verified_queries,payment_query_conflicts,payment_verified_evidence,payment_evidence_processing,payment_provider_revisions,payment_transaction_identities,payment_order_capture_selections,payment_captures,payment_effects,payment_review_facts,ledger_accounts,journals,journal_lines,funding_receipts,projection_outbox,audit_events TO :"worker_role";
GRANT SELECT(id),UPDATE(id) ON tenants TO :"worker_role";
GRANT UPDATE(id) ON payment_orders TO :"worker_role";
GRANT INSERT ON payment_command_outbox,payment_command_observations,payment_query_attempts,payment_verified_queries,payment_query_conflicts,payment_verified_evidence,payment_evidence_processing,payment_provider_revisions,payment_transaction_identities,payment_order_capture_selections,payment_captures,payment_effects,payment_review_facts,journals,journal_lines,funding_receipts,projection_outbox,audit_events TO :"worker_role";
GRANT INSERT(tenant_id,kind) ON ledger_accounts TO :"worker_role";
GRANT UPDATE(balance) ON ledger_accounts TO :"worker_role";
GRANT UPDATE(state,generation,attempt_count,current_query_id,next_attempt_at,lease_until,last_error_code,last_submission_unknown_at) ON payment_command_outbox TO :"worker_role";
GRANT UPDATE(state,generation,attempt_count,next_attempt_at,lease_until,last_error_code) ON payment_evidence_processing TO :"worker_role";
-- Trigger invocation does not require EXECUTE, nested invoker calls do. Helpers
-- are side-effect free or trigger-returning; no public funding entry is added.
DO $$
DECLARE f record;app_name name;worker_name name;
BEGIN
 SELECT app_role,worker_role INTO STRICT app_name,worker_name FROM payment_sandbox_environment WHERE singleton;
 FOR f IN SELECT p.oid,p.proname,pg_get_function_identity_arguments(p.oid) args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND (p.proname LIKE 'p1\_%' ESCAPE '\' OR p.proname='funding_journal_valid') LOOP
  EXECUTE format('REVOKE EXECUTE ON FUNCTION %I.%I(%s) FROM PUBLIC',current_schema(),f.proname,f.args);
  EXECUTE format('GRANT EXECUTE ON FUNCTION %I.%I(%s) TO %I,%I',current_schema(),f.proname,f.args,app_name,worker_name);
 END LOOP;
END $$;

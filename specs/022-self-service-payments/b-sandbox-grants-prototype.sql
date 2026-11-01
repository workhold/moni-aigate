-- DESIGN PROTOTYPE ONLY. Apply offline AFTER app's existing migrations+ProvisionFor,
-- mock/receiver private schema SQL and owner-only immutable markers/key digests.
-- Full-schema revokes are restricted to exactly these three new QA schemas.
REVOKE ALL ON SCHEMA :"app_schema",:"mock_schema",:"receiver_schema" FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA :"app_schema",:"mock_schema",:"receiver_schema" FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA :"app_schema",:"mock_schema",:"receiver_schema" FROM PUBLIC;
-- Existing application functions are invoker-only. Restore their execution after PUBLIC revoke.
-- Table access remains the exact existing ProvisionFor matrix. No new application table/grant.
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA :"app_schema" TO :"app_runtime",:"app_projector";
GRANT USAGE ON SCHEMA :"mock_schema" TO :"mock_runtime";
GRANT SELECT ON ALL TABLES IN SCHEMA :"mock_schema" TO :"mock_runtime";
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA :"mock_schema" TO :"mock_runtime";
SET search_path TO :"mock_schema",pg_catalog;
GRANT INSERT ON mock_orders,mock_commands,mock_command_processing,mock_command_results,mock_order_transitions,mock_captures,mock_queries,mock_notification_events,mock_notification_delivery,mock_delivery_attempts,mock_delivery_observations,mock_controls,mock_fault_plans TO :"mock_runtime";
GRANT UPDATE(state,revision,closed_at) ON mock_orders TO :"mock_runtime";
GRANT UPDATE(state,completed_at,released_by_control_id) ON mock_command_processing TO :"mock_runtime";
GRANT UPDATE(state,generation,attempt_count,next_attempt_at,lease_until,last_error_code,delivered_at) ON mock_notification_delivery TO :"mock_runtime";
GRANT UPDATE(remaining,consumed_at) ON mock_fault_plans TO :"mock_runtime";
GRANT USAGE ON SCHEMA :"receiver_schema" TO :"receiver_runtime";
GRANT SELECT ON ALL TABLES IN SCHEMA :"receiver_schema" TO :"receiver_runtime";
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA :"receiver_schema" TO :"receiver_runtime";
SET search_path TO :"receiver_schema",pg_catalog;
GRANT INSERT ON receiver_inbox,receiver_receipts,receiver_conflicts TO :"receiver_runtime";
RESET search_path;
-- No SECURITY DEFINER, cross-schema call, runtime membership, CREATE, TRUNCATE or DELETE grant.
-- Future schema additions must explicitly repeat this review; runtime never migrates automatically.

-- Isolated prototype renderer substitutes role identifiers, never SQL values.
-- Every B function resolves this exact schema before pg_catalog and pg_temp.
DO $$
DECLARE f record;
BEGIN
 FOR f IN SELECT p.oid,p.proname,pg_get_function_identity_arguments(p.oid) args
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND p.proname LIKE 'pb\_%' ESCAPE '\' LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
 END LOOP;
END $$;

-- psql-renderable owner entry point: set app_role and projection_role to
-- identifier values from ProvisionFor, then include this file. Prototype Go
-- renders the same template with pgx.Identifier; never use string quoting.
REVOKE ALL ON periodic_budget_controls,periodic_budget_windows,periodic_budget_allocations,
 periodic_budget_events,periodic_budget_operations,periodic_budget_configuration_versions FROM :"app_role";
GRANT SELECT,INSERT ON periodic_budget_controls,periodic_budget_windows,periodic_budget_allocations,
 periodic_budget_events,periodic_budget_operations,periodic_budget_configuration_versions TO :"app_role";
GRANT UPDATE(enabled,limit_amount,configuration_operation_id,revision,held_amount,spent_amount,event_seq,last_event_id,max_observed_window_start)
 ON periodic_budget_controls TO :"app_role";
GRANT UPDATE(held_amount,spent_amount,revision,last_event_id) ON periodic_budget_windows TO :"app_role";
GRANT UPDATE(held_amount,spent_amount,source_held,source_spent,state,revision,last_event_id)
 ON periodic_budget_allocations TO :"app_role";
REVOKE ALL ON periodic_budget_controls,periodic_budget_windows,periodic_budget_allocations,
 periodic_budget_events,periodic_budget_operations,periodic_budget_configuration_versions FROM :"projection_role";

-- Existing upstream_attempts UPDATE includes lawful finalization/cost columns.
-- pb_dispatch_guard, also invoker with the fixed path above, overwrites a new
-- permit's checked_at and rejects all other metadata changes. Keep helper
-- EXECUTE available for nested invoker calls; it is not a standalone permit.

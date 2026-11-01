-- ISOLATED PROTOTYPE ONLY, paired with c0-core-prototype.sql.
-- psql variables: :c0p_schema, :c0p_app, :c0p_worker, :c0p_projector.
-- Roles must be distinct real LOGIN, NOINHERIT, NOSUPERUSER, NOCREATEROLE,
-- NOCREATEDB, NOREPLICATION, NOBYPASSRLS; no role memberships. Owner creates
-- the random schema. Harness supplies runtime-only random passwords.
-- This file does not grant privileges on any real application object.
-- Witness sources/journals intentionally unify fixture columns; they are NOT
-- permission evidence for real A/B source tables (production needs split
-- kind validators and exact per-column grants in c0-sequence-contract.md).

REVOKE ALL ON SCHEMA :"c0p_schema" FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA :"c0p_schema" FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA :"c0p_schema" FROM PUBLIC;
GRANT USAGE ON SCHEMA :"c0p_schema" TO :"c0p_app",:"c0p_worker",:"c0p_projector";

GRANT SELECT ON c0p_epoch,c0p_tenants,c0p_sources,c0p_journals,c0p_operations,c0p_heads,c0p_openings,c0p_lots,c0p_events,c0p_balances,c0p_reservation_lots,c0p_settlement_lots,c0p_refund_lots TO :"c0p_app";
GRANT INSERT ON c0p_tenants,c0p_sources,c0p_journals,c0p_heads,c0p_lots,c0p_events,c0p_balances,c0p_reservation_lots,c0p_settlement_lots,c0p_refund_lots TO :"c0p_app";
GRANT UPDATE(tail_seq,tail_id) ON c0p_heads TO :"c0p_app";
GRANT UPDATE(a,h,s,revision,last_event_id) ON c0p_balances TO :"c0p_app";

GRANT SELECT ON c0p_epoch,c0p_tenants,c0p_sources,c0p_journals,c0p_operations,c0p_heads,c0p_lots,c0p_events,c0p_balances TO :"c0p_worker";
-- Opening rows can be queried by c0p_validate_initial, but the sandbox branch
-- short circuits before touching them. No worker opening SELECT is needed.
GRANT INSERT ON c0p_sources,c0p_journals,c0p_lots,c0p_events,c0p_balances TO :"c0p_worker";
GRANT INSERT(id,tenant_id,epoch_id,seq,previous_id,previous_seq,source_id,event_count,available_delta,held_delta,consumed_delta,issued_delta) ON c0p_operations TO :"c0p_app",:"c0p_worker";
GRANT UPDATE(tail_seq,tail_id) ON c0p_heads TO :"c0p_worker";

GRANT EXECUTE ON FUNCTION c0p_immutable(),c0p_operation_stamp(),c0p_guard_head(),c0p_guard_balance(),c0p_deferred(),c0p_check_head(text),c0p_check_operation(text),c0p_check_lot(text),c0p_validate_initial(text,boolean),c0p_validate_sandbox(text) TO :"c0p_app",:"c0p_worker";
GRANT EXECUTE ON FUNCTION c0p_guard_allocation(),c0p_validate_app_initial(text),c0p_validate_reserve(text),c0p_validate_finish(text),c0p_validate_refund(text),c0p_verify() TO :"c0p_app";

-- No owner/epoch/opening DML, DELETE, DDL, grants, function replacement,
-- existing balance UPDATE, genesis INSERT or allocation DML for worker.
-- Projector has schema USAGE only: zero table and function privileges.

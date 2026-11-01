-- C0 ORDINARY permission variant. Offline installation only; not a migration.
-- Independent reviewed variant of c0-production-permissions.sql. No worker is
-- created, named, granted, or silently filtered. psql variable: fl_app.
-- Original 001--022 provisioning MUST finish before C0 DDL and this script.
-- APP/projector remain distinct real minimal LOGIN; object owner is separate.
-- This script never runs in serve, checker, CI repair, or automatic refresh.

REVOKE ALL ON TYPE fl_source_proof FROM PUBLIC;
GRANT USAGE ON TYPE fl_source_proof TO :"fl_app";
REVOKE ALL ON TABLE funding_lot_epoch,funding_lot_runtime_attestations,funding_lot_legacy_receipts,funding_lot_legacy_sandbox_receipts,funding_lot_legacy_settlements,funding_lot_legacy_approved_refunds,funding_lot_legacy_openings,funding_wallet_legacy_sources,funding_wallet_operations,funding_wallet_heads,funding_lots,funding_lot_events,funding_lot_balances,funding_reservation_lots,funding_settlement_lots,funding_consumption_refund_lots FROM PUBLIC;

-- Preserve exactly the original eight tenant INSERT and six UPDATE columns.
REVOKE INSERT,UPDATE ON tenants FROM :"fl_app";
GRANT INSERT(id,name,status,allowed_regions,created_at,price_group_id,pricing_revision,pricing_operation_id),UPDATE(name,status,allowed_regions,price_group_id,pricing_revision,pricing_operation_id) ON tenants TO :"fl_app";

GRANT SELECT ON funding_lot_epoch,funding_lot_runtime_attestations,funding_lot_legacy_receipts,funding_lot_legacy_sandbox_receipts,funding_lot_legacy_settlements,funding_lot_legacy_approved_refunds,funding_lot_legacy_openings,funding_wallet_legacy_sources,funding_wallet_operations,funding_wallet_heads,funding_lots,funding_lot_events,funding_lot_balances,funding_reservation_lots,funding_settlement_lots,funding_consumption_refund_lots TO :"fl_app";
GRANT INSERT(tenant_id,epoch_id,tail_seq,tail_operation_id) ON funding_wallet_heads TO :"fl_app";
GRANT INSERT(id,tenant_id,origin_kind,funding_receipt_id,legacy_opening_id,legacy_refund_order_id,issued_amount,currency,refund_class,fifo_at) ON funding_lots TO :"fl_app";
GRANT INSERT(lot_id,tenant_id,available_amount,inference_held_amount,consumed_amount,revision,last_event_id) ON funding_lot_balances TO :"fl_app";
GRANT INSERT(id,tenant_id,reservation_id,lot_id,allocation_index,amount) ON funding_reservation_lots TO :"fl_app";
GRANT INSERT(id,tenant_id,settlement_id,reservation_lot_id,charged_amount,released_amount) ON funding_settlement_lots TO :"fl_app";
GRANT INSERT(id,tenant_id,refund_order_id,lot_id,settlement_lot_id,allocation_index,amount,origin) ON funding_consumption_refund_lots TO :"fl_app";
GRANT UPDATE(available_amount,inference_held_amount,consumed_amount,revision,last_event_id) ON funding_lot_balances TO :"fl_app";

GRANT INSERT(id,tenant_id,epoch_id,seq,previous_seq,previous_id,kind,source_id,journal_id,journal_operation_id,event_count,available_delta,held_delta,consumed_delta,issued_delta) ON funding_wallet_operations TO :"fl_app";
GRANT INSERT(id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,operation_id,journal_id,previous_event_id,before_revision,after_revision,before_available,before_held,before_consumed,after_available,after_held,after_consumed,available_delta,inference_held_delta,consumed_delta,issued_delta) ON funding_lot_events TO :"fl_app";
GRANT UPDATE(tail_seq,tail_operation_id) ON funding_wallet_heads TO :"fl_app";

-- The same APP39/owner-only2 function surface as the original fixed candidate.
GRANT EXECUTE ON FUNCTION fl_uuid(text),fl_require_epoch(),fl_immutable(),fl_stamp(),fl_guard_head(),fl_guard_event(),fl_guard_balance(),fl_check_wallet_head(text),fl_check_wallet_account(text),fl_check_lot(text),fl_validate_new_funding(text),fl_validate_sandbox_funding(text),fl_source_sandbox(text),fl_read_source(text,text),fl_check_wallet_operation(text),fl_check_sandbox_receipt(text),fl_check_receipt(text),fl_check_wallet_journal(text),fl_new_fact_constraint(),fl_source_constraint() TO :"fl_app";
GRANT EXECUTE ON FUNCTION fl_tenant_birth_guard(),fl_guard_allocation(),fl_validate_funding(text),fl_validate_reserve(text),fl_validate_finish(text),fl_validate_refund(text),fl_validate_legacy_refund(text),fl_source_manual(text),fl_source_voucher(text),fl_journal_lines(text,text,numeric,numeric,numeric,boolean),fl_source_reserve(text),fl_source_finish(text),fl_source_refund(text,boolean),fl_validate_genesis(text),fl_check_legacy_source(text,text),fl_check_reservation(text),fl_check_settlement(text),fl_check_refund(text),fl_new_tenant() TO :"fl_app";

-- No new projector data or EXECUTE rights. Automatic row/array type USAGE
-- retains PostgreSQL semantics; it is not table SELECT or function EXECUTE.

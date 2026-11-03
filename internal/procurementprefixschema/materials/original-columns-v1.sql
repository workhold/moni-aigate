-- Original001-022 additions only; nullable, no defaults, no backfill.

ALTER TABLE {{MONI_SCHEMA}}."request_records"
 ADD COLUMN "pp_origin_root_id" pg_catalog.text,
 ADD COLUMN "pp_origin_root_step" pg_catalog.int8,
 ADD COLUMN "pp_origin_xid" pg_catalog.xid8,
 ADD COLUMN "pp_last_decision_id" pg_catalog.text;

ALTER TABLE {{MONI_SCHEMA}}."upstream_attempts"
 ADD COLUMN "pp_origin_root_id" pg_catalog.text,
 ADD COLUMN "pp_origin_root_step" pg_catalog.int8,
 ADD COLUMN "pp_origin_xid" pg_catalog.xid8,
 ADD COLUMN "pp_quote_selected_at" pg_catalog.timestamptz,
 ADD COLUMN "pp_last_cost_mutation_id" pg_catalog.text,
 ADD COLUMN "pp_last_delivery_source_id" pg_catalog.text,
 ADD COLUMN "pp_dispatch_source_id" pg_catalog.text,
 ADD COLUMN "pp_dispatch_root_id" pg_catalog.text,
 ADD COLUMN "pp_dispatch_root_step" pg_catalog.int8,
 ADD COLUMN "pp_dispatch_xid" pg_catalog.xid8,
 ADD COLUMN "pp_dispatch_account_id" pg_catalog.text,
 ADD COLUMN "pp_dispatch_journal_id" pg_catalog.text;

ALTER TABLE {{MONI_SCHEMA}}."procurement_accounts"
 ADD COLUMN "pp_version" pg_catalog.int2,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8,
 ADD COLUMN "pp_origin_journal_id" pg_catalog.text;

ALTER TABLE {{MONI_SCHEMA}}."procurement_reservations"
 ADD COLUMN "pp_created_xid" pg_catalog.xid8,
 ADD COLUMN "pp_origin_journal_id" pg_catalog.text,
 ADD COLUMN "pp_last_journal_id" pg_catalog.text;

ALTER TABLE {{MONI_SCHEMA}}."procurement_budget_events"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_transition_id" pg_catalog.text,
 ADD COLUMN "pp_journal_id" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."procurement_commands"
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8,
 ADD COLUMN "pp_account_journal_id" pg_catalog.text;

ALTER TABLE {{MONI_SCHEMA}}."metering_events"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."metering_event_conflicts"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."finalization_records"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."procurement_evidence_identities"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."procurement_cost_proposals"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8,
 ADD COLUMN "pp_approval_root_id" pg_catalog.text,
 ADD COLUMN "pp_approval_root_step" pg_catalog.int8,
 ADD COLUMN "pp_approval_source_kind" pg_catalog.text,
 ADD COLUMN "pp_approval_source_id" pg_catalog.text,
 ADD COLUMN "pp_approval_source_role" pg_catalog.text,
 ADD COLUMN "pp_approval_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."provider_cost_corrections"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."reconciliation_cases"
 ADD COLUMN "pp_last_root_id" pg_catalog.text,
 ADD COLUMN "pp_last_root_step" pg_catalog.int8,
 ADD COLUMN "pp_last_source_kind" pg_catalog.text,
 ADD COLUMN "pp_last_source_id" pg_catalog.text,
 ADD COLUMN "pp_last_source_role" pg_catalog.text,
 ADD COLUMN "pp_last_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."settlements"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."journals"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."journal_lines"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."audit_events"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

ALTER TABLE {{MONI_SCHEMA}}."projection_outbox"
 ADD COLUMN "pp_root_id" pg_catalog.text,
 ADD COLUMN "pp_root_step" pg_catalog.int8,
 ADD COLUMN "pp_source_kind" pg_catalog.text,
 ADD COLUMN "pp_source_id" pg_catalog.text,
 ADD COLUMN "pp_source_role" pg_catalog.text,
 ADD COLUMN "pp_created_xid" pg_catalog.xid8;

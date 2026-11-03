-- A1 original request additions only; nullable, no defaults, no backfill.

ALTER TABLE {{MONI_SCHEMA}}."request_records"
 ADD COLUMN "pp_origin_root_id" pg_catalog.text,
 ADD COLUMN "pp_origin_root_step" pg_catalog.int8,
 ADD COLUMN "pp_origin_xid" pg_catalog.xid8,
 ADD COLUMN "pp_last_decision_id" pg_catalog.text;

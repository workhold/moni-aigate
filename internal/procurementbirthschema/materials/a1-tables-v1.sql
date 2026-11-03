-- A1 isolated request birth relation slice: four tables / 71 columns.
-- No writer, guard, trigger, grants, or runtime installation.

CREATE TABLE {{MONI_SCHEMA}}."pp_operation_roots" (
 "id" pg_catalog.text NOT NULL,
 "entry_kind" pg_catalog.text NOT NULL,
 "scope_kind" pg_catalog.text NOT NULL,
 "tenant_id" pg_catalog.text,
 "request_id" pg_catalog.text,
 "attempt_id" pg_catalog.text,
 "account_target_id" pg_catalog.text,
 "created_xid" pg_catalog.xid8 NOT NULL,
 "session_role_oid" pg_catalog.oid NOT NULL,
 "step_count" pg_catalog.int8 NOT NULL,
 "semantic_result" pg_catalog.text NOT NULL
);

CREATE TABLE {{MONI_SCHEMA}}."pp_root_steps" (
 "root_id" pg_catalog.text NOT NULL,
 "step_no" pg_catalog.int8 NOT NULL,
 "kind" pg_catalog.text NOT NULL,
 "source_id" pg_catalog.text NOT NULL,
 "created_xid" pg_catalog.xid8 NOT NULL,
 "d_request_birth" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'request_birth' THEN source_id ELSE NULL END) STORED,
 "d_attempt_birth" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'attempt_birth' THEN source_id ELSE NULL END) STORED,
 "d_account_command" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'account_command' THEN source_id ELSE NULL END) STORED,
 "d_attempt_delivery" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'attempt_delivery' THEN source_id ELSE NULL END) STORED,
 "d_incoming" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'incoming' THEN source_id ELSE NULL END) STORED,
 "d_meter_choice" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'meter_choice' THEN source_id ELSE NULL END) STORED,
 "d_request_decision" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'request_decision' THEN source_id ELSE NULL END) STORED,
 "d_normal_cost_decision" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'normal_cost_decision' THEN source_id ELSE NULL END) STORED,
 "d_cost_mutation" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'cost_mutation' THEN source_id ELSE NULL END) STORED,
 "d_cost_reset_decision" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'cost_reset_decision' THEN source_id ELSE NULL END) STORED,
 "d_evidence_choice" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'evidence_choice' THEN source_id ELSE NULL END) STORED,
 "d_procurement_transition" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'procurement_transition' THEN source_id ELSE NULL END) STORED,
 "d_proposal_birth" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'proposal_birth' THEN source_id ELSE NULL END) STORED,
 "d_approval_consumed" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'approval_consumed' THEN source_id ELSE NULL END) STORED,
 "d_unmanaged_correction" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'unmanaged_correction' THEN source_id ELSE NULL END) STORED,
 "d_recovery" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'recovery' THEN source_id ELSE NULL END) STORED,
 "d_cost_outbox" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'cost_outbox' THEN source_id ELSE NULL END) STORED,
 "d_reconciliation" pg_catalog.text GENERATED ALWAYS AS (CASE WHEN kind = 'reconciliation' THEN source_id ELSE NULL END) STORED
);

CREATE TABLE {{MONI_SCHEMA}}."pp_request_origins" (
 "request_id" pg_catalog.text NOT NULL,
 "root_id" pg_catalog.text NOT NULL,
 "step_no" pg_catalog.int8 NOT NULL,
 "step_kind" pg_catalog.text GENERATED ALWAYS AS ('request_birth'::pg_catalog.text) STORED NOT NULL,
 "created_xid" pg_catalog.xid8 NOT NULL,
 "tenant_id" pg_catalog.text NOT NULL,
 "key_id" pg_catalog.text NOT NULL,
 "owner_id" pg_catalog.text NOT NULL,
 "model_id" pg_catalog.text NOT NULL,
 "price_version_id" pg_catalog.text NOT NULL,
 "input_rate" pg_catalog.int8 NOT NULL,
 "output_rate" pg_catalog.int8 NOT NULL,
 "input_upper_bound" pg_catalog.int8 NOT NULL,
 "output_limit" pg_catalog.int8 NOT NULL,
 "profile_version" pg_catalog.text NOT NULL,
 "policy_version" pg_catalog.text NOT NULL,
 "request_hmac" pg_catalog.text NOT NULL,
 "route_revision" pg_catalog.int8 NOT NULL,
 "request_deadline" pg_catalog.timestamptz NOT NULL,
 "instance_id" pg_catalog.text NOT NULL,
 "streaming" pg_catalog.bool NOT NULL,
 "client_ip" pg_catalog.text,
 "pricing_group_id" pg_catalog.text,
 "tenant_pricing_revision" pg_catalog.int8 NOT NULL,
 "retail_priced_at" pg_catalog.timestamptz NOT NULL,
 "request_created_at" pg_catalog.timestamptz NOT NULL,
 "pb_created_xid" pg_catalog.xid8 NOT NULL,
 "pb_tenant_control" pg_catalog.text,
 "pb_key_control" pg_catalog.text,
 "pb_admitted_at" pg_catalog.timestamptz NOT NULL,
 "initial_latest_id" pg_catalog.text,
 "initial_status" pg_catalog.text NOT NULL,
 "initial_billing_status" pg_catalog.text NOT NULL
);

CREATE TABLE {{MONI_SCHEMA}}."pp_request_heads" (
 "request_id" pg_catalog.text NOT NULL,
 "ordinal" pg_catalog.int8 NOT NULL,
 "last_decision_id" pg_catalog.text,
 "updated_xid" pg_catalog.xid8 NOT NULL
);

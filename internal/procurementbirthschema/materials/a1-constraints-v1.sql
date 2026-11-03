-- A1 isolated request birth keys, checks and native locator foreign keys.
-- Extra payload and reverse completeness require the separate real writer guards.
-- Not the full 685-constraint candidate; no wide payload UK is installed here.
-- Render exactly one safely quoted explicit schema, never install automatically.

-- @phase keys

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_xid_uq
 UNIQUE (id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_pk
 PRIMARY KEY (root_id,step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_pk
 PRIMARY KEY (request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_pk
 PRIMARY KEY (request_id);

-- @phase checks

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_scalar_ck
 CHECK ((id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000' AND created_xid <> '0'::pg_catalog.xid8 AND session_role_oid <> 0::pg_catalog.oid AND step_count > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_template_ck
 CHECK (((entry_kind = 'request_birth' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NULL AND semantic_result = 'born' AND step_count = 1)
  OR (entry_kind = 'attempt_birth' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NOT NULL AND ((semantic_result = 'managed' AND step_count = 2) OR (semantic_result = 'unmanaged' AND step_count = 1)))
  OR (entry_kind = 'account_create' AND scope_kind = 'account_command' AND tenant_id IS NULL AND request_id IS NULL AND attempt_id IS NULL AND account_target_id IS NOT NULL AND semantic_result = 'applied' AND step_count = 1)
  OR (entry_kind = 'account_configure' AND scope_kind = 'account_command' AND tenant_id IS NULL AND request_id IS NULL AND attempt_id IS NULL AND account_target_id IS NOT NULL AND semantic_result IN ('applied','rejected') AND step_count = 1)
  OR (entry_kind = 'dispatch' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NOT NULL AND semantic_result = 'permitted' AND step_count = 1)
  OR (entry_kind = 'record_finalization' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND semantic_result IN ('normal','conflict') AND step_count >= 3)
  OR (entry_kind = 'propose_cost' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NOT NULL AND semantic_result = 'proposed' AND step_count = 1)
  OR (entry_kind = 'approve_cost' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NOT NULL AND semantic_result = 'approved' AND step_count = 4)
  OR (entry_kind = 'unmanaged_correction' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NOT NULL AND semantic_result = 'corrected' AND step_count = 3)
  OR (entry_kind = 'recover_procurement' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NOT NULL AND semantic_result IN ('released','unknown') AND step_count = 3)
  OR (entry_kind = 'reconcile' AND scope_kind = 'request' AND tenant_id IS NOT NULL AND request_id IS NOT NULL AND account_target_id IS NULL AND attempt_id IS NULL AND ((semantic_result IN ('proposed','released') AND step_count = 1) OR (semantic_result = 'approved' AND step_count IN (1,4))))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_shape_ck
 CHECK ((step_no > 0 AND kind IN ('request_birth','attempt_birth','account_command','attempt_delivery','incoming','meter_choice','request_decision','normal_cost_decision','cost_mutation','cost_reset_decision','evidence_choice','procurement_transition','proposal_birth','approval_consumed','unmanaged_correction','recovery','cost_outbox','reconciliation')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_step_ck
 CHECK ((step_no > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_initial_ck
 CHECK ((initial_latest_id IS NULL AND initial_status = 'accepted' AND initial_billing_status = 'reserved') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_snapshot_ck
 CHECK ((input_rate >= 0 AND output_rate >= 0 AND input_upper_bound >= 0 AND output_limit >= 0 AND route_revision >= 0 AND tenant_pricing_revision > 0 AND pb_created_xid = created_xid AND pg_catalog.isfinite(request_deadline) AND pg_catalog.isfinite(retail_priced_at) AND pg_catalog.isfinite(request_created_at) AND pg_catalog.isfinite(pb_admitted_at)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_shape_ck
 CHECK ((ordinal >= 0 AND ((ordinal = 0 AND last_decision_id IS NULL) OR (ordinal > 0 AND last_decision_id IS NOT NULL))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_o_request_birth_ck
 CHECK (((pp_origin_root_id IS NULL AND pp_origin_root_step IS NULL AND pp_origin_xid IS NULL AND pp_last_decision_id IS NULL) OR ((pp_origin_root_id IS NOT NULL AND pp_origin_root_step IS NOT NULL AND pp_origin_xid IS NOT NULL) AND pp_origin_root_step > 0 AND pp_origin_xid <> '0'::pg_catalog.xid8)) IS TRUE);

-- @phase foreign-keys

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_request_fk
 FOREIGN KEY (tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.request_records (tenant_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_original_fk
 FOREIGN KEY (tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.request_records (tenant_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_origin_head_fk
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_heads (request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_origin_fk
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_bx_request_step_locator_fk
 FOREIGN KEY (root_id,step_no)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_bx_step_request_origin_fk
 FOREIGN KEY (d_request_birth)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_bx_root_request_origin_fk
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_bx_original_request_step_locator_fk
 FOREIGN KEY (pp_origin_root_id,pp_origin_root_step)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

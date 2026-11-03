-- B-P4-D1-SQL-S: fixed source/step/birth/chain constraints; NOT an installer.
-- Replace {{MONI_SCHEMA}} with one safely quoted trusted schema identifier.
-- Requires all new columns/generated columns and original extension keys first.
-- Assemble ALL domain keys, then checks, then foreign-keys; no runtime grants,
-- functions, triggers, validators, writer stubs, or business entry points here.
-- Cross-row templates, exact NULL identity, head maxima, original payloads,
-- source-before-effects, actual LOGIN and DB stamps remain unimplemented gates.

-- @phase keys

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_xid_uq
 UNIQUE (id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_r_uq
 UNIQUE (id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_a_uq
 UNIQUE (id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_attempt_uq
 UNIQUE (id,created_xid,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_pk
 PRIMARY KEY (root_id,step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_source_uq
 UNIQUE (kind,source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_identity_uq
 UNIQUE (root_id,step_no,kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_pk
 PRIMARY KEY (request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_step_uq
 UNIQUE (request_id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_source_uq
 UNIQUE (request_id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_r_uq
 UNIQUE (request_id,root_id,created_xid,tenant_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_pk
 PRIMARY KEY (attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_step_uq
 UNIQUE (attempt_id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_source_uq
 UNIQUE (attempt_id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_r_uq
 UNIQUE (attempt_id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_step_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_source_uq
 UNIQUE (id,root_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_origin_birth_uq
 UNIQUE (request_id,root_id,step_no,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_origin_tenant_uq
 UNIQUE (request_id,tenant_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_origin_birth_uq
 UNIQUE (attempt_id,root_id,step_no,created_xid,quote_selected_at);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_origin_request_uq
 UNIQUE (attempt_id,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_pk
 PRIMARY KEY (request_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_last_uq
 UNIQUE (request_id,last_decision_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_pk
 PRIMARY KEY (attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_attempt_node_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_position_uq
 UNIQUE (attempt_id,ordinal);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_position_id_uq
 UNIQUE (attempt_id,ordinal,id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_step_uq
 UNIQUE (root_id,step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_attempt_uq
 UNIQUE (attempt_id,id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_origins ADD CONSTRAINT pp_s_meter_origin_pk
 PRIMARY KEY (meter_event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_origins ADD CONSTRAINT pp_s_meter_origin_pointer_uq
 UNIQUE (meter_event_id,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_source_pk
 PRIMARY KEY (conflict_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_pointer_uq
 UNIQUE (conflict_id,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_decision_uq
 UNIQUE (conflict_id,root_id,created_xid,tenant_id,request_id,incoming_source_id,meter_choice_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_origin_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid,meter_event_id,incoming_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_event_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid,meter_event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_incoming_r_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,incoming_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_incoming_a_uq
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,incoming_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_position_uq
 UNIQUE (request_id,ordinal);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_position_id_uq
 UNIQUE (request_id,ordinal,id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_previous_latest_uq
 UNIQUE (request_id,id,after_latest_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_current_latest_uq
 UNIQUE (id,tenant_id,request_id,after_latest_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_output_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,finalization_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_position_uq
 UNIQUE (attempt_id,member_ordinal);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_position_id_uq
 UNIQUE (attempt_id,member_ordinal,id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_digest_uq
 UNIQUE (attempt_id,identity_digest);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_identity_uq
 UNIQUE (attempt_id,identity_digest,identity_event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_attempt_uq
 UNIQUE (id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_pointer_uq
 UNIQUE (attempt_id,identity_digest,identity_event_id,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_member_uq
 UNIQUE (id,root_id,step_no,step_kind,created_xid,attempt_id,identity_digest,identity_event_id);

-- @phase checks

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_step_ck
 CHECK ((step_no > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_step_ck
 CHECK ((step_no > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

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

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_initial_ck
 CHECK ((initial_latest_id IS NULL AND initial_status = 'accepted' AND initial_billing_status = 'reserved') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_snapshot_ck
 CHECK ((input_rate >= 0 AND output_rate >= 0 AND input_upper_bound >= 0 AND output_limit >= 0 AND route_revision >= 0 AND tenant_pricing_revision > 0 AND pb_created_xid = created_xid AND pg_catalog.isfinite(request_deadline) AND pg_catalog.isfinite(retail_priced_at) AND pg_catalog.isfinite(request_created_at) AND pg_catalog.isfinite(pb_admitted_at)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_shape_ck
 CHECK ((ordinal >= 0 AND ((ordinal = 0 AND last_decision_id IS NULL) OR (ordinal > 0 AND last_decision_id IS NOT NULL))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_initial_ck
 CHECK ((sequence BETWEEN 1 AND 2 AND initial_delivery_state = 'prepared' AND initial_cost_status = 'unknown' AND initial_cost_amount IS NULL AND pb_created_xid = created_xid AND pg_catalog.isfinite(quote_selected_at) AND pg_catalog.isfinite(attempt_created_at) AND pg_catalog.jsonb_typeof(route_decisions) = 'array' AND pg_catalog.jsonb_array_length(route_decisions) <= 1000) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_quote_ck
 CHECK ((cost_basis IN ('unknown','contract','invoice') AND (cost_input_rate IS NULL OR cost_input_rate >= 0) AND (cost_output_rate IS NULL OR cost_output_rate >= 0) AND (cost_version_id IS NOT NULL OR (cost_input_rate IS NULL AND cost_output_rate IS NULL AND cost_basis = 'unknown'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_management_ck
 CHECK (((management_mode = 'managed' AND account_id IS NOT NULL AND binding_id IS NOT NULL AND procurement_transition_id IS NOT NULL AND reserved_event_id IS NOT NULL AND account_journal_id IS NOT NULL AND admitted_at IS NOT NULL AND pg_catalog.isfinite(admitted_at)) OR (management_mode = 'unmanaged' AND account_id IS NULL AND binding_id IS NULL AND procurement_transition_id IS NULL AND reserved_event_id IS NULL AND account_journal_id IS NULL AND admitted_at IS NULL)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_shape_ck
 CHECK ((ordinal >= 0 AND ((ordinal = 0 AND last_node_id IS NULL) OR (ordinal > 0 AND last_node_id IS NOT NULL)) AND ((member_count IS NULL AND member_tail_id IS NULL) OR (member_count IS NOT NULL AND member_count >= 0 AND ((member_count = 0 AND member_tail_id IS NULL) OR (member_count > 0 AND member_tail_id IS NOT NULL))))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_shape_ck
 CHECK ((id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000' AND ordinal > 0 AND step_no > 0 AND ((ordinal = 1 AND previous_node_id IS NULL) OR (ordinal > 1 AND previous_node_id IS NOT NULL)) AND step_kind IN ('attempt_delivery','incoming','meter_choice','request_decision','normal_cost_decision','cost_mutation','cost_reset_decision','evidence_choice','procurement_transition','proposal_birth','approval_consumed','unmanaged_correction','recovery','cost_outbox')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_usage_ck
 CHECK ((usage_status IN ('confirmed_final','provisional','estimated','missing','conflicting') AND input_tokens >= 0 AND output_tokens >= 0 AND total_tokens >= 0 AND cached_tokens >= 0 AND reasoning_tokens >= 0 AND cached_tokens <= input_tokens AND reasoning_tokens <= output_tokens AND (usage_status <> 'confirmed_final' OR input_tokens::pg_catalog.numeric + output_tokens::pg_catalog.numeric = total_tokens::pg_catalog.numeric)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_protocol_ck
 CHECK ((protocol_outcome IN ('completed','failed','incomplete','cancelled','unknown') AND transport_end_reason IN ('done','eof','provider_error','platform_error','platform_timeout','client_cancelled','connection_error','unknown','not_sent','rejected_no_consumption') AND raw_delivery_stage IN ('none','committed','content','unknown') AND pg_catalog.octet_length(source) BETWEEN 1 AND 80 AND pg_catalog.octet_length(source_event_id) BETWEEN 1 AND 200 AND pg_catalog.octet_length(finish_reason) <= 80 AND input_digest ~ '^[0-9a-f]{64}$') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_time_ck
 CHECK ((pg_catalog.isfinite(effective_observed_at) AND (observed_at_input IS NULL OR (pg_catalog.isfinite(observed_at_input) AND observed_at_input = effective_observed_at))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_shape_ck
 CHECK ((disposition IN ('created','reused') AND meter_origin_id = meter_event_id AND collision_kind IN ('none','source_payload_mismatch','other_confirmed_usage') AND ((collision_kind = 'other_confirmed_usage' AND competing_meter_id IS NOT NULL AND competing_meter_id <> meter_event_id) OR (collision_kind IN ('none','source_payload_mismatch') AND competing_meter_id IS NULL)) AND (attempt_id IS NOT NULL OR (attempt_cut_before_id IS NULL AND attempt_cut_after_meter_id IS NULL))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_shape_ck
 CHECK ((source_id = meter_choice_id AND step_no > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_origins ADD CONSTRAINT pp_s_meter_origin_shape_ck
 CHECK ((step_no > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_chain_ck
 CHECK ((ordinal > 0 AND ((ordinal = 1 AND previous_decision_id IS NULL AND before_latest_id IS NULL) OR (ordinal > 1 AND previous_decision_id IS NOT NULL)) AND before_status IN ('accepted','dispatching','succeeded','failed','cancelled') AND after_status IN ('accepted','dispatching','succeeded','failed','cancelled') AND normalized_delivery_stage IN ('none','committed','content','unknown')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_branch_ck
 CHECK (((branch = 'normal' AND finalization_id IS NOT NULL AND conflict_source_id IS NULL AND after_latest_id = finalization_id AND after_status IN ('succeeded','failed','cancelled')) OR (branch = 'conflict' AND finalization_id IS NULL AND conflict_source_id IS NOT NULL AND after_latest_id IS NOT DISTINCT FROM before_latest_id AND after_status = before_status)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_shape_ck
 CHECK ((id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000' AND member_ordinal > 0 AND step_no > 0 AND identity_digest ~ '^[0-9a-f]{64}$' AND source_id = evidence_choice_id AND ((member_ordinal = 1 AND previous_member_id IS NULL) OR (member_ordinal > 1 AND previous_member_id IS NOT NULL))) IS TRUE);

-- @phase foreign-keys

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,request_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_request_birth_fk
 FOREIGN KEY (d_request_birth,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,attempt_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_attempt_birth_fk
 FOREIGN KEY (d_attempt_birth,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_account_command_fk
 FOREIGN KEY (d_account_command,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_s_attempt_delivery_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_attempt_delivery_fk
 FOREIGN KEY (d_attempt_delivery,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_delivery_sources (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_incoming_fk
 FOREIGN KEY (d_incoming,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_meter_choice_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_meter_choice_fk
 FOREIGN KEY (d_meter_choice,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_request_decision_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_request_decision_fk
 FOREIGN KEY (d_request_decision,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_s_normal_cost_decision_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_normal_cost_decision_fk
 FOREIGN KEY (d_normal_cost_decision,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_normal_cost_decisions (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_s_cost_mutation_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_cost_mutation_fk
 FOREIGN KEY (d_cost_mutation,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_mutations (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_s_cost_reset_decision_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_cost_reset_decision_fk
 FOREIGN KEY (d_cost_reset_decision,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_reset_decisions (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices ADD CONSTRAINT pp_s_evidence_choice_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_evidence_choice_fk
 FOREIGN KEY (d_evidence_choice,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_choices (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_procurement_transition_fk
 FOREIGN KEY (d_procurement_transition,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births ADD CONSTRAINT pp_s_proposal_birth_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_proposal_birth_fk
 FOREIGN KEY (d_proposal_birth,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_proposal_births (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_s_approval_consumed_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_approval_consumed_fk
 FOREIGN KEY (d_approval_consumed,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_approval_consumptions (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_s_unmanaged_correction_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_unmanaged_correction_fk
 FOREIGN KEY (d_unmanaged_correction,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_unmanaged_correction_sources (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources ADD CONSTRAINT pp_s_recovery_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_recovery_fk
 FOREIGN KEY (d_recovery,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_recovery_sources (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_root_a_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_s_cost_outbox_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_cost_outbox_fk
 FOREIGN KEY (d_cost_outbox,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_outbox_links (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_root_r_fk
 FOREIGN KEY (root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_s_reconciliation_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_reconciliation_fk
 FOREIGN KEY (d_reconciliation,root_id,step_no,kind,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_reconciliation_sources (id,root_id,step_no,step_kind,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_s_step_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_request_fk
 FOREIGN KEY (tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.request_records (tenant_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_original_attempt_fk
 FOREIGN KEY (tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (tenant_id,request_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_request_origin_fk
 FOREIGN KEY (request_id,tenant_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id,tenant_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_s_root_attempt_origin_fk
 FOREIGN KEY (attempt_id,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_original_fk
 FOREIGN KEY (tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.request_records (tenant_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_birth_original_fk
 FOREIGN KEY (request_id,root_id,step_no,created_xid)
 REFERENCES {{MONI_SCHEMA}}.request_records (id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_s_original_request_birth_fk
 FOREIGN KEY (id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id,root_id,step_no,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins ADD CONSTRAINT pp_s_request_origin_head_fk
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_heads (request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_origin_fk
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_decision_fk
 FOREIGN KEY (request_id,ordinal,last_decision_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (request_id,ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_heads ADD CONSTRAINT pp_s_request_head_original_fk
 FOREIGN KEY (request_id,last_decision_id)
 REFERENCES {{MONI_SCHEMA}}.request_records (id,pp_last_decision_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_s_original_request_head_fk
 FOREIGN KEY (id,pp_last_decision_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_heads (request_id,last_decision_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_s_original_current_latest_fk
 FOREIGN KEY (pp_last_decision_id,tenant_id,id,latest_finalization_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (id,tenant_id,request_id,after_latest_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_original_fk
 FOREIGN KEY (tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (tenant_id,request_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_original_fk
 FOREIGN KEY (attempt_id,root_id,step_no,created_xid,quote_selected_at)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts ADD CONSTRAINT pp_s_original_attempt_birth_fk
 FOREIGN KEY (id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id,root_id,step_no,created_xid,quote_selected_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_origin_head_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_heads (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_origin_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_node_fk
 FOREIGN KEY (attempt_id,ordinal,last_node_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_nodes (attempt_id,ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_member_fk
 FOREIGN KEY (attempt_id,member_count,member_tail_id)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id,member_ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_origin_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_root_attempt_fk
 FOREIGN KEY (root_id,created_xid,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_step_fk
 FOREIGN KEY (root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_previous_fk
 FOREIGN KEY (attempt_id,previous_ordinal,previous_node_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_nodes (attempt_id,ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_s_incoming_reconcile_fk
 FOREIGN KEY (reconcile_source_id,root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_reconciliation_sources (id,root_id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_incoming_r_fk
 FOREIGN KEY (incoming_source_id,root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_incoming_a_fk
 FOREIGN KEY (incoming_source_id,root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_origin_fk
 FOREIGN KEY (meter_origin_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_origins (meter_event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_competing_meter_fk
 FOREIGN KEY (competing_meter_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_origins (meter_event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_cut_before_fk
 FOREIGN KEY (attempt_id,attempt_cut_before_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_nodes (attempt_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_choices ADD CONSTRAINT pp_s_choice_cut_after_meter_fk
 FOREIGN KEY (attempt_id,attempt_cut_after_meter_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_nodes (attempt_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_origins ADD CONSTRAINT pp_s_meter_origin_choice_fk
 FOREIGN KEY (source_id,root_id,step_no,step_kind,created_xid,meter_event_id,incoming_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,step_no,step_kind,created_xid,meter_event_id,incoming_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_origins ADD CONSTRAINT pp_s_meter_origin_incoming_fk
 FOREIGN KEY (incoming_source_id,root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_meter_origins ADD CONSTRAINT pp_s_meter_origin_original_fk
 FOREIGN KEY (meter_event_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.metering_events (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.metering_events ADD CONSTRAINT pp_s_original_meter_origin_fk
 FOREIGN KEY (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_origins (meter_event_id,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_choice_r_fk
 FOREIGN KEY (meter_choice_id,root_id,created_xid,tenant_id,request_id,incoming_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,created_xid,tenant_id,request_id,incoming_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_choice_a_fk
 FOREIGN KEY (meter_choice_id,root_id,created_xid,tenant_id,request_id,attempt_id,incoming_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,created_xid,tenant_id,request_id,attempt_id,incoming_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_s_conflict_original_fk
 FOREIGN KEY (conflict_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.metering_event_conflicts (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.metering_event_conflicts ADD CONSTRAINT pp_s_original_conflict_source_fk
 FOREIGN KEY (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_conflict_sources (conflict_id,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.metering_event_conflicts ADD CONSTRAINT pp_s_original_conflict_meter_fk
 FOREIGN KEY (pp_source_id,pp_root_id,pp_root_step,pp_source_kind,pp_created_xid,event_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,step_no,step_kind,created_xid,meter_event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_previous_fk
 FOREIGN KEY (request_id,previous_ordinal,previous_decision_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (request_id,ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_previous_latest_fk
 FOREIGN KEY (request_id,previous_decision_id,before_latest_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (request_id,id,after_latest_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_incoming_fk
 FOREIGN KEY (incoming_source_id,root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_choice_fk
 FOREIGN KEY (meter_choice_id,root_id,created_xid,tenant_id,request_id,incoming_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,created_xid,tenant_id,request_id,incoming_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_conflict_fk
 FOREIGN KEY (conflict_source_id,root_id,created_xid,tenant_id,request_id,incoming_source_id,meter_choice_id)
 REFERENCES {{MONI_SCHEMA}}.pp_conflict_sources (conflict_id,root_id,created_xid,tenant_id,request_id,incoming_source_id,meter_choice_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_before_latest_fk
 FOREIGN KEY (before_latest_id,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.finalization_records (id,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_after_latest_fk
 FOREIGN KEY (after_latest_id,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.finalization_records (id,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_s_decision_finalization_fk
 FOREIGN KEY (finalization_id,root_id,step_no,step_kind,id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.finalization_records (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.finalization_records ADD CONSTRAINT pp_s_original_finalization_source_fk
 FOREIGN KEY (pp_source_id,pp_root_id,pp_root_step,pp_source_kind,pp_created_xid,tenant_id,request_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,finalization_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_origin_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_previous_fk
 FOREIGN KEY (attempt_id,previous_member_ordinal,previous_member_id)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id,member_ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_choice_fk
 FOREIGN KEY (evidence_choice_id,root_id,step_no,step_kind,created_xid,attempt_id,identity_digest,identity_event_id)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_choices (id,root_id,step_no,step_kind,created_xid,attempt_id,identity_digest,identity_event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_meter_origin_fk
 FOREIGN KEY (identity_event_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_origins (meter_event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_members ADD CONSTRAINT pp_s_member_original_fk
 FOREIGN KEY (attempt_id,identity_digest,identity_event_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.procurement_evidence_identities (attempt_id,evidence_digest,event_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_evidence_identities ADD CONSTRAINT pp_s_original_member_fk
 FOREIGN KEY (attempt_id,evidence_digest,event_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id,identity_digest,identity_event_id,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

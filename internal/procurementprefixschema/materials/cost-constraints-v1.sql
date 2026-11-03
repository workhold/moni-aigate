-- B-P4 D1 cost / reconciliation declarative materials only.
-- See specs/023-periodic-products/b-procurement-cost-ddl-contract.md.
-- Cross-material assembly: every keys phase, every checks phase, every FK phase.
-- No writer, guard, validator, grants, business DML, or serving installation.

-- @phase keys

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_cd_origin_management
 UNIQUE (attempt_id,tenant_id,request_id,management_mode);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_cd_origin_account
 UNIQUE (attempt_id,tenant_id,request_id,account_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_cd_decision_normal_output
 UNIQUE (finalization_id,root_id,created_xid,tenant_id,request_id,incoming_source_id,meter_choice_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_decisions ADD CONSTRAINT pp_cd_decision_reconcile_output
 UNIQUE (finalization_id,root_id,created_xid,tenant_id,request_id,incoming_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_conflict_sources ADD CONSTRAINT pp_cd_conflict_attempt_identity
 UNIQUE (conflict_id,root_id,created_xid,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_attempt
 UNIQUE (id,root_id,created_xid,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_permit_identity
 UNIQUE (id,tenant_id,request_id,attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_cause_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_cd_reset_mutation_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_mutation_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_outbox_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_recorded_outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_cd_approval_mutation_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions ADD CONSTRAINT pp_cd_approval_outbox_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_mutation_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_outbox_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_id_unique
 UNIQUE (outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_cause_output
 UNIQUE (outbox_id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_original_binding
 UNIQUE (outbox_id,tenant_id,root_id,step_no,step_kind,id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_incoming_sources ADD CONSTRAINT pp_cd_incoming_reconcile_output
 UNIQUE (id,root_id,created_xid,tenant_id,request_id,reconcile_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_finish_output
 UNIQUE (id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,case_id,customer_finish_outcome_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_source_unique
 UNIQUE (reconciliation_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_step_unique
 UNIQUE (root_id,step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_companion
 UNIQUE (id,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_tenant_companion
 UNIQUE (id,root_id,step_no,step_kind,source_id,created_xid,tenant_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_case_companion
 UNIQUE (id,root_id,step_no,step_kind,source_id,created_xid,tenant_id,request_id,case_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_tenant
 UNIQUE (id,tenant_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_new_settlement
 UNIQUE (id,d_new_settlement);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_original_settlement
 UNIQUE (d_new_settlement,tenant_id,request_id,customer_journal_id,result_status,selected_reason,charged_amount,raw_amount,waived_amount,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_original_journal
 UNIQUE (customer_journal_id,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_pk
 PRIMARY KEY (finish_outcome_id,account_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_line_unique
 UNIQUE (journal_line_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_kind_unique
 UNIQUE (finish_outcome_id,account_kind);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_original_line
 UNIQUE (journal_line_id,account_id,signed_delta,root_id,step_no,step_kind,source_id,created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_pk
 PRIMARY KEY (finish_outcome_id,scope);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_event_unique
 UNIQUE (event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_finalization_unique
 UNIQUE (finalization_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_mutation_unique
 UNIQUE (cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_outbox_unique
 UNIQUE (cost_recorded_outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_cd_reset_mutation_unique
 UNIQUE (cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_cause_unique
 UNIQUE (cause_kind,cause_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_operation_unique
 UNIQUE (operation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_correction_unique
 UNIQUE (provider_correction_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_mutation_unique
 UNIQUE (cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_audit_unique
 UNIQUE (audit_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_outbox_unique
 UNIQUE (outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_audit_unique
 UNIQUE (audit_id);

-- @phase checks

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_cost_before
 CHECK ((((before_cost_status = 'unknown' AND before_cost_amount IS NULL) OR (before_cost_status IN ('contract','invoice') AND before_cost_amount IS NOT NULL AND before_cost_amount >= 0 AND before_cost_amount <> 'NaN'::pg_catalog.numeric))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_management
 CHECK (((management_mode = 'unmanaged' AND before_procurement_revision IS NULL AND before_procurement_state IS NULL AND before_procurement_held IS NULL AND before_procurement_spent IS NULL AND before_procurement_anomaly IS NULL AND member_count_before IS NULL AND account_id IS NULL AND account_ordinal_cut IS NULL AND account_tail_cut IS NULL AND member_tail_before IS NULL) OR (management_mode = 'managed' AND before_procurement_revision IS NOT NULL AND before_procurement_state IS NOT NULL AND before_procurement_held IS NOT NULL AND before_procurement_spent IS NOT NULL AND before_procurement_anomaly IS NOT NULL AND member_count_before IS NOT NULL AND account_id IS NOT NULL AND account_ordinal_cut IS NOT NULL AND account_tail_cut IS NOT NULL AND before_procurement_revision >= 1 AND before_procurement_state IN ('prepared','sending','unknown','settled','released','disputed') AND before_procurement_held IS NOT NULL AND before_procurement_held >= 0 AND before_procurement_held <> 'NaN'::pg_catalog.numeric AND before_procurement_spent IS NOT NULL AND before_procurement_spent >= 0 AND before_procurement_spent <> 'NaN'::pg_catalog.numeric AND member_count_before >= 0 AND ((member_count_before = 0 AND member_tail_before IS NULL) OR (member_count_before > 0 AND member_tail_before IS NOT NULL)) AND account_ordinal_cut >= 1)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_write_skip
 CHECK (((decision IN ('write_no_consumption','write_final_usage') AND selected_amount IS NOT NULL AND selected_basis IS NOT NULL AND cost_mutation_id IS NOT NULL AND cost_recorded_outbox_id IS NOT NULL AND selected_amount IS NOT NULL AND selected_amount >= 0 AND selected_amount <> 'NaN'::pg_catalog.numeric AND selected_basis IN ('contract','invoice')) OR (decision IN ('skip_already_known','skip_disputed','skip_no_final_usage_or_rate') AND selected_amount IS NULL AND selected_basis IS NULL AND cost_mutation_id IS NULL AND cost_recorded_outbox_id IS NULL)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_priority
 CHECK (((before_cost_status <> 'unknown' AND decision = 'skip_already_known') OR (before_cost_status = 'unknown' AND management_mode = 'managed' AND before_procurement_state = 'disputed' AND decision = 'skip_disputed') OR (before_cost_status = 'unknown' AND (management_mode = 'unmanaged' OR before_procurement_state <> 'disputed') AND decision IN ('write_no_consumption','write_final_usage','skip_no_final_usage_or_rate'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_no_consumption
 CHECK ((decision <> 'write_no_consumption' OR (selected_amount = 0 AND selected_basis = 'contract')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_cost_before
 CHECK ((((before_status = 'unknown' AND before_amount IS NULL) OR (before_status IN ('contract','invoice') AND before_amount IS NOT NULL AND before_amount >= 0 AND before_amount <> 'NaN'::pg_catalog.numeric))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_cost_after
 CHECK ((((after_status = 'unknown' AND after_amount IS NULL) OR (after_status IN ('contract','invoice') AND after_amount IS NOT NULL AND after_amount >= 0 AND after_amount <> 'NaN'::pg_catalog.numeric))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_cause
 CHECK (((cause_kind IN ('procurement_dispute_reset','incoming_conflict_reset') AND after_status = 'unknown' AND after_amount IS NULL) OR (cause_kind IN ('normal_cost_decision','approval_consumed','unmanaged_correction') AND after_status IN ('contract','invoice') AND after_amount IS NOT NULL)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_cd_reset_policy
 CHECK (((cause_kind = 'procurement_dispute' AND procurement_state_before IN ('prepared','sending','unknown','settled','released','disputed') AND policy = 'applied' AND cost_mutation_id IS NOT NULL) OR (cause_kind = 'incoming_conflict' AND ((procurement_state_before IN ('settled','released') AND policy = 'suppressed_adjudicated' AND cost_mutation_id IS NULL) OR ((procurement_state_before IS NULL OR procurement_state_before IN ('prepared','sending','unknown','disputed')) AND policy = 'applied' AND cost_mutation_id IS NOT NULL)))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_states
 CHECK ((before_delivery_state IN ('prepared','sending','completed','failed','unknown') AND after_delivery_state IN ('prepared','sending','completed','failed','unknown')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_mode
 CHECK (((mode = 'dispatch_permit' AND cause_source_id IS NULL AND before_delivery_state = 'prepared' AND after_delivery_state = 'sending' AND dispatch_xid IS NOT NULL AND dispatch_xid = created_xid AND pb_dispatch_checked_at IS NOT NULL AND pg_catalog.isfinite(pb_dispatch_checked_at) AND ((dispatch_account_id IS NULL AND dispatch_journal_id IS NULL) OR (dispatch_account_id IS NOT NULL AND dispatch_journal_id IS NOT NULL))) OR (mode = 'finalization' AND cause_source_id IS NOT NULL AND after_delivery_state IN ('completed','failed','unknown') AND dispatch_account_id IS NULL AND dispatch_journal_id IS NULL AND dispatch_xid IS NULL AND pb_dispatch_checked_at IS NULL) OR (mode = 'recovery' AND cause_source_id IS NOT NULL AND after_delivery_state IN ('failed','unknown') AND dispatch_account_id IS NULL AND dispatch_journal_id IS NULL AND dispatch_xid IS NULL AND pb_dispatch_checked_at IS NULL)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_cause
 CHECK ((cause_kind IN ('normal_cost_decision','approval_consumed','unmanaged_correction')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_before
 CHECK ((((before_cost_status = 'unknown' AND before_cost_amount IS NULL) OR (before_cost_status IN ('contract','invoice') AND before_cost_amount IS NOT NULL AND before_cost_amount >= 0 AND before_cost_amount <> 'NaN'::pg_catalog.numeric))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_input
 CHECK ((amount >= 0 AND basis IN ('contract','invoice') AND pg_catalog.octet_length(operation_id) BETWEEN 1 AND 200 AND pg_catalog.octet_length(evidence_ref) BETWEEN 1 AND 256 AND (input_actor_id IS NULL OR pg_catalog.octet_length(input_actor_id) > 0) AND effective_audit_actor = COALESCE(input_actor_id,'system')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_after
 CHECK ((after_cost_status = basis AND after_cost_amount = amount::pg_catalog.numeric AND after_cost_amount <> 'NaN'::pg_catalog.numeric) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_input
 CHECK (((action = 'propose' AND input_usage_status IN ('confirmed_final','provisional','estimated','missing','conflicting') AND input_input_tokens IS NOT NULL AND input_output_tokens IS NOT NULL AND input_total_tokens IS NOT NULL AND input_cached_tokens IS NOT NULL AND input_reasoning_tokens IS NOT NULL AND input_input_tokens >= 0 AND input_output_tokens >= 0 AND input_total_tokens >= 0 AND input_cached_tokens >= 0 AND input_reasoning_tokens >= 0 AND input_cached_tokens <= input_input_tokens AND input_reasoning_tokens <= input_output_tokens AND (input_usage_status <> 'confirmed_final' OR input_input_tokens::pg_catalog.numeric + input_output_tokens::pg_catalog.numeric = input_total_tokens::pg_catalog.numeric) AND input_usage_status = 'confirmed_final' AND input_evidence_ref IS NOT NULL AND pg_catalog.octet_length(input_evidence_ref) BETWEEN 1 AND 256 AND input_reason IS NOT NULL AND pg_catalog.octet_length(input_reason) BETWEEN 1 AND 200) OR (action = 'approve' AND input_usage_status IS NULL AND input_input_tokens IS NULL AND input_output_tokens IS NULL AND input_total_tokens IS NULL AND input_cached_tokens IS NULL AND input_reasoning_tokens IS NULL AND input_evidence_ref IS NULL AND input_reason IS NULL) OR (action = 'release' AND input_usage_status IS NULL AND input_input_tokens IS NULL AND input_output_tokens IS NULL AND input_total_tokens IS NULL AND input_cached_tokens IS NULL AND input_reasoning_tokens IS NULL AND input_evidence_ref IS NULL AND input_reason IS NOT NULL AND pg_catalog.octet_length(input_reason) BETWEEN 1 AND 200)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_case_usage
 CHECK (((case_usage_status IS NULL AND case_input_tokens IS NULL AND case_output_tokens IS NULL AND case_total_tokens IS NULL AND case_cached_tokens IS NULL AND case_reasoning_tokens IS NULL AND case_evidence_ref IS NULL AND case_proposal_reason IS NULL) OR (case_usage_status IN ('confirmed_final','provisional','estimated','missing','conflicting') AND case_input_tokens IS NOT NULL AND case_output_tokens IS NOT NULL AND case_total_tokens IS NOT NULL AND case_cached_tokens IS NOT NULL AND case_reasoning_tokens IS NOT NULL AND case_input_tokens >= 0 AND case_output_tokens >= 0 AND case_total_tokens >= 0 AND case_cached_tokens >= 0 AND case_reasoning_tokens >= 0 AND case_cached_tokens <= case_input_tokens AND case_reasoning_tokens <= case_output_tokens AND (case_usage_status <> 'confirmed_final' OR case_input_tokens::pg_catalog.numeric + case_output_tokens::pg_catalog.numeric = case_total_tokens::pg_catalog.numeric) AND case_evidence_ref IS NOT NULL AND pg_catalog.octet_length(case_evidence_ref) BETWEEN 1 AND 256 AND case_proposal_reason IS NOT NULL AND pg_catalog.octet_length(case_proposal_reason) BETWEEN 1 AND 200)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_role
 CHECK ((actor_status = 'active' AND ((action = 'propose' AND actor_role = 'finance_operator') OR (action IN ('approve','release') AND actor_role = 'finance_approver'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_times
 CHECK ((pg_catalog.isfinite(case_created_at) AND pg_catalog.isfinite(reservation_deadline) AND (approval_checked_at IS NULL OR pg_catalog.isfinite(approval_checked_at))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_statuses
 CHECK ((before_case_state IN ('open','proposed','approved','closed') AND after_case_state IN ('open','proposed','approved','closed') AND before_reservation_status IN ('reserved','pending_reconciliation','settled','released')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_action
 CHECK (((action = 'propose' AND before_case_state = 'open' AND after_case_state = 'proposed' AND before_reservation_status = 'pending_reconciliation' AND after_requester = actor AND after_approver IS NULL AND requester_role IS NULL AND requester_status IS NULL AND approval_checked_at IS NULL AND derived_incoming_source_id IS NULL AND finalization_id IS NULL AND customer_finish_outcome_id IS NULL) OR (action = 'approve' AND before_case_state = 'proposed' AND after_case_state = 'approved' AND before_requester IS NOT NULL AND before_requester <> actor AND after_requester = before_requester AND after_approver = actor AND requester_role = 'finance_operator' AND requester_status = 'active' AND case_usage_status = 'confirmed_final' AND approval_checked_at IS NOT NULL AND customer_finish_outcome_id IS NOT NULL AND ((approval_checked_at < reservation_deadline AND derived_incoming_source_id IS NOT NULL AND finalization_id IS NOT NULL) OR (approval_checked_at >= reservation_deadline AND derived_incoming_source_id IS NULL AND finalization_id IS NULL))) OR (action = 'release' AND before_reservation_status = 'pending_reconciliation' AND after_case_state = CASE WHEN before_case_state = 'approved' THEN 'approved' ELSE 'closed' END AND after_requester IS NOT DISTINCT FROM before_requester AND after_approver IS NOT DISTINCT FROM before_approver AND requester_role IS NULL AND requester_status IS NULL AND approval_checked_at IS NULL AND derived_incoming_source_id IS NULL AND finalization_id IS NULL AND customer_finish_outcome_id IS NOT NULL)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_identity
 CHECK ((step_no > 0 AND source_id = reconciliation_source_id AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_money
 CHECK ((reservation_amount >= 0 AND before_key_held >= 0 AND before_key_spent >= 0 AND after_key_held >= 0 AND after_key_spent >= 0 AND charged_amount >= 0 AND charged_amount <= reservation_amount AND raw_amount IS NOT NULL AND raw_amount >= 0 AND raw_amount <> 'NaN'::pg_catalog.numeric AND waived_amount IS NOT NULL AND waived_amount >= 0 AND waived_amount <> 'NaN'::pg_catalog.numeric AND raw_amount = charged_amount::pg_catalog.numeric + waived_amount) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_states
 CHECK ((before_case_state IN ('open','proposed','approved','closed') AND after_case_state IN ('open','proposed','approved','closed') AND before_reservation_status IN ('reserved','pending_reconciliation','settled','released') AND after_reservation_status IN ('reserved','pending_reconciliation','settled','released') AND before_request_status IN ('accepted','dispatching','succeeded','failed','cancelled') AND after_request_status IN ('accepted','dispatching','succeeded','failed','cancelled') AND before_billing_status IN ('reserved','pending_reconciliation','settled','released') AND after_billing_status IN ('reserved','pending_reconciliation','settled','released')) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_times
 CHECK ((pg_catalog.isfinite(reservation_deadline) AND (financial_checked_at IS NULL OR pg_catalog.isfinite(financial_checked_at)) AND (settlement_decision_checked_at IS NULL OR pg_catalog.isfinite(settlement_decision_checked_at))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_mode
 CHECK (((selected_mode = 'recovery') OR (selected_mode = 'release' AND result_kind = 'new_settlement' AND settlement_decision_checked_at IS NULL AND selected_reason IN ('released_reconciliation','released_deadline'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_result
 CHECK (((result_kind = 'new_settlement' AND settlement_id IS NOT NULL AND customer_journal_id IS NOT NULL AND billing_audit_id IS NOT NULL AND billing_outbox_id IS NOT NULL AND financial_checked_at IS NOT NULL AND ((selected_mode = 'recovery' AND settlement_decision_checked_at IS NOT NULL) OR (selected_mode = 'release' AND settlement_decision_checked_at IS NULL)) AND result_status IN ('settled','released')) OR (result_kind = 'existing_settlement' AND settlement_id IS NOT NULL AND customer_journal_id IS NULL AND customer_tenant_control_id IS NULL AND customer_key_control_id IS NULL AND billing_audit_id IS NULL AND billing_outbox_id IS NULL AND profile_block_model_id IS NULL AND profile_block_profile_version IS NULL AND profile_block_disposition IS NULL AND profile_block_audit_id IS NULL AND financial_checked_at IS NULL AND settlement_decision_checked_at IS NULL AND result_status IN ('settled','released')) OR (result_kind = 'pending' AND selected_mode = 'recovery' AND settlement_id IS NULL AND customer_journal_id IS NULL AND customer_tenant_control_id IS NULL AND customer_key_control_id IS NULL AND billing_audit_id IS NULL AND billing_outbox_id IS NULL AND profile_block_model_id IS NULL AND profile_block_profile_version IS NULL AND profile_block_disposition IS NULL AND profile_block_audit_id IS NULL AND financial_checked_at IS NULL AND settlement_decision_checked_at IS NOT NULL AND result_status = 'pending_reconciliation' AND selected_reason IN ('metering_conflict','delivery_unknown','usage_unconfirmed') AND raw_amount = 0 AND charged_amount = 0 AND waived_amount = 0)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_new_state
 CHECK ((result_kind <> 'new_settlement' OR (after_reservation_status = result_status AND after_billing_status = result_status AND after_case_state = CASE WHEN before_case_state = 'approved' THEN 'approved' ELSE 'closed' END AND after_case_reason = before_case_reason AND after_request_status = CASE WHEN result_status = 'released' AND before_request_status IN ('accepted','dispatching') THEN 'failed' ELSE before_request_status END AND before_key_held::pg_catalog.numeric - reservation_amount::pg_catalog.numeric = after_key_held::pg_catalog.numeric AND before_key_spent::pg_catalog.numeric + charged_amount::pg_catalog.numeric = after_key_spent::pg_catalog.numeric AND ((selected_reason = 'charged_usage' AND result_status = 'settled' AND charged_amount::pg_catalog.numeric = LEAST(raw_amount,reservation_amount::pg_catalog.numeric)) OR (selected_reason IN ('released_deadline','released_no_delivery','released_reconciliation') AND result_status = 'released' AND raw_amount = 0 AND charged_amount = 0 AND waived_amount = 0)) AND ((financial_checked_at >= reservation_deadline AND selected_reason = 'released_deadline' AND raw_amount = 0 AND charged_amount = 0 AND waived_amount = 0) OR financial_checked_at < reservation_deadline))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_existing_unchanged
 CHECK ((result_kind <> 'existing_settlement' OR (after_reservation_status = before_reservation_status AND after_billing_status = before_billing_status AND after_case_state = before_case_state AND after_case_reason = before_case_reason AND after_request_status = before_request_status AND after_key_held = before_key_held AND after_key_spent = before_key_spent)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_pending_target
 CHECK ((result_kind <> 'pending' OR (after_case_state = before_case_state AND after_request_status = before_request_status AND after_key_held = before_key_held AND after_key_spent = before_key_spent AND ((before_reservation_status IN ('settled','released') AND after_reservation_status = before_reservation_status AND after_billing_status = before_billing_status AND after_case_reason = before_case_reason) OR (before_reservation_status IN ('reserved','pending_reconciliation') AND after_reservation_status = 'pending_reconciliation' AND after_billing_status = 'pending_reconciliation' AND after_case_reason = CASE WHEN before_case_reason = 'metering_conflict' THEN 'metering_conflict' ELSE selected_reason END)))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_profile_block
 CHECK (((result_kind = 'new_settlement' AND raw_amount > reservation_amount::pg_catalog.numeric AND profile_block_model_id IS NOT NULL AND profile_block_profile_version IS NOT NULL AND profile_block_disposition IS NOT NULL AND profile_block_audit_id IS NOT NULL AND profile_block_disposition IN ('created','reused')) OR ((result_kind <> 'new_settlement' OR raw_amount <= reservation_amount::pg_catalog.numeric) AND profile_block_model_id IS NULL AND profile_block_profile_version IS NULL AND profile_block_disposition IS NULL AND profile_block_audit_id IS NULL)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_balance
 CHECK ((after_balance::pg_catalog.numeric = before_balance::pg_catalog.numeric + signed_delta::pg_catalog.numeric AND ((account_kind IN ('HELD','AVAILABLE') AND account_tenant_id IS NOT NULL AND account_tenant_id = tenant_id AND before_balance >= 0 AND after_balance >= 0) OR (account_kind = 'REVENUE' AND account_tenant_id IS NULL))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_scope
 CHECK ((scope IN ('tenant','key') AND original_source_kind = 'settlement') IS TRUE);

-- @phase foreign-keys

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_incoming_source_id
 FOREIGN KEY (incoming_source_id,root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_meter_choice_id
 FOREIGN KEY (meter_choice_id,root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id,root_id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_request_output
 FOREIGN KEY (finalization_id,root_id,created_xid,tenant_id,request_id,incoming_source_id,meter_choice_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (finalization_id,root_id,created_xid,tenant_id,request_id,incoming_source_id,meter_choice_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_origin_mode
 FOREIGN KEY (attempt_id,tenant_id,request_id,management_mode)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id,tenant_id,request_id,management_mode)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_origin_account
 FOREIGN KEY (attempt_id,tenant_id,request_id,account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id,tenant_id,request_id,account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_account_cut
 FOREIGN KEY (account_id,account_ordinal_cut,account_tail_cut)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id,ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_member_cut
 FOREIGN KEY (attempt_id,member_count_before,member_tail_before)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id,member_ordinal,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_mutation
 FOREIGN KEY (cost_mutation_id,root_id,created_xid,tenant_id,request_id,attempt_id,step_kind,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_mutations (id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_normal_cost_decisions ADD CONSTRAINT pp_cd_normal_outbox
 FOREIGN KEY (cost_recorded_outbox_id,root_id,created_xid,tenant_id,request_id,attempt_id,step_kind,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_outbox_links (outbox_id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_from_normal_cost_decision
 FOREIGN KEY (d_normal_cost_decision,root_id,created_xid,tenant_id,request_id,attempt_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_normal_cost_decisions (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_from_procurement_dispute_reset
 FOREIGN KEY (d_procurement_dispute_reset,root_id,created_xid,tenant_id,request_id,attempt_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_reset_decisions (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_from_incoming_conflict_reset
 FOREIGN KEY (d_incoming_conflict_reset,root_id,created_xid,tenant_id,request_id,attempt_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_reset_decisions (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_from_approval_consumed
 FOREIGN KEY (d_approval_consumed,root_id,created_xid,tenant_id,request_id,attempt_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_approval_consumptions (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_mutations ADD CONSTRAINT pp_cd_mutation_from_unmanaged_correction
 FOREIGN KEY (d_unmanaged_correction,root_id,created_xid,tenant_id,request_id,attempt_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_unmanaged_correction_sources (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_mutation_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_cd_reset_dispute
 FOREIGN KEY (d_procurement_dispute,root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id,root_id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_cd_reset_incoming
 FOREIGN KEY (d_incoming_conflict,root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_conflict_sources (conflict_id,root_id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_reset_decisions ADD CONSTRAINT pp_cd_reset_mutation
 FOREIGN KEY (cost_mutation_id,root_id,created_xid,tenant_id,request_id,attempt_id,mutation_cause_kind,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_mutations (id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_finalization
 FOREIGN KEY (d_finalization,root_id,created_xid,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (id,root_id,created_xid,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_recovery
 FOREIGN KEY (d_recovery,root_id,created_xid,tenant_id,request_id,attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_recovery_sources (id,root_id,created_xid,tenant_id,request_id,attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_account
 FOREIGN KEY (dispatch_account_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_accounts (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_delivery_sources ADD CONSTRAINT pp_cd_delivery_dispatch_journal
 FOREIGN KEY (dispatch_journal_id,dispatch_account_id,attempt_id,id,root_id,step_no,created_xid,pb_dispatch_checked_at)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id,account_id,dispatch_attempt_id,delivery_source_id,root_id,step_no,created_xid,pb_decision_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_from_normal_cost_decision
 FOREIGN KEY (d_normal_cost_decision,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id)
 REFERENCES {{MONI_SCHEMA}}.pp_normal_cost_decisions (id,root_id,created_xid,tenant_id,request_id,attempt_id,cost_recorded_outbox_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_from_approval_consumed
 FOREIGN KEY (d_approval_consumed,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id)
 REFERENCES {{MONI_SCHEMA}}.pp_approval_consumptions (id,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_from_unmanaged_correction
 FOREIGN KEY (d_unmanaged_correction,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id)
 REFERENCES {{MONI_SCHEMA}}.pp_unmanaged_correction_sources (id,root_id,created_xid,tenant_id,request_id,attempt_id,outbox_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_cost_outbox_links ADD CONSTRAINT pp_cd_outbox_original
 FOREIGN KEY (outbox_id,tenant_id,root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.projection_outbox (event_id,tenant_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_original
 FOREIGN KEY (provider_correction_id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref,root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.provider_cost_corrections (id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_mutation
 FOREIGN KEY (cost_mutation_id,root_id,created_xid,tenant_id,request_id,attempt_id,step_kind,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_mutations (id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_outbox
 FOREIGN KEY (outbox_id,root_id,created_xid,tenant_id,request_id,attempt_id,step_kind,id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_outbox_links (outbox_id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_unmanaged_correction_sources ADD CONSTRAINT pp_cd_unmanaged_audit
 FOREIGN KEY (audit_id,effective_audit_actor,tenant_id,provider_correction_id,request_id,root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id,actor,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_case
 FOREIGN KEY (case_id,tenant_id,request_id,case_created_at)
 REFERENCES {{MONI_SCHEMA}}.reconciliation_cases (id,tenant_id,request_id,created_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_prior_latest
 FOREIGN KEY (prior_latest_id,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.finalization_records (id,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_derived_incoming
 FOREIGN KEY (derived_incoming_source_id,root_id,created_xid,tenant_id,request_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id,root_id,created_xid,tenant_id,request_id,reconcile_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_finalization
 FOREIGN KEY (finalization_id,root_id,created_xid,tenant_id,request_id,derived_incoming_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_decisions (finalization_id,root_id,created_xid,tenant_id,request_id,incoming_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_finish
 FOREIGN KEY (customer_finish_outcome_id,root_id,step_no,step_kind,id,created_xid,tenant_id,request_id,case_id)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_outcomes (id,root_id,step_no,step_kind,source_id,created_xid,tenant_id,request_id,case_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_audit
 FOREIGN KEY (audit_id,actor,tenant_id,case_id,request_id,root_id,step_no,step_kind,id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id,actor,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_user_actor
 FOREIGN KEY (actor)
 REFERENCES {{MONI_SCHEMA}}.users (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_user_before_requester
 FOREIGN KEY (before_requester)
 REFERENCES {{MONI_SCHEMA}}.users (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_user_before_approver
 FOREIGN KEY (before_approver)
 REFERENCES {{MONI_SCHEMA}}.users (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_user_after_requester
 FOREIGN KEY (after_requester)
 REFERENCES {{MONI_SCHEMA}}.users (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_reconciliation_sources ADD CONSTRAINT pp_cd_reconcile_user_after_approver
 FOREIGN KEY (after_approver)
 REFERENCES {{MONI_SCHEMA}}.users (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_source
 FOREIGN KEY (reconciliation_source_id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,case_id,id)
 REFERENCES {{MONI_SCHEMA}}.pp_reconciliation_sources (id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,case_id,customer_finish_outcome_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_reservation
 FOREIGN KEY (customer_reservation_id,tenant_id,request_id,reservation_amount,reservation_deadline)
 REFERENCES {{MONI_SCHEMA}}.reservations (id,tenant_id,request_id,amount,deadline)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_latest
 FOREIGN KEY (latest_finalization_id,tenant_id,request_id)
 REFERENCES {{MONI_SCHEMA}}.finalization_records (id,tenant_id,request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_existing_result
 FOREIGN KEY (settlement_id,tenant_id,request_id,result_status,selected_reason,charged_amount,raw_amount,waived_amount)
 REFERENCES {{MONI_SCHEMA}}.settlements (id,tenant_id,request_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_new_original
 FOREIGN KEY (d_new_settlement,tenant_id,request_id,customer_journal_id,result_status,selected_reason,charged_amount,raw_amount,waived_amount,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.settlements (id,tenant_id,request_id,journal_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.settlements ADD CONSTRAINT pp_cd_original_finish
 FOREIGN KEY (id,tenant_id,request_id,journal_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_outcomes (d_new_settlement,tenant_id,request_id,customer_journal_id,result_status,selected_reason,charged_amount,raw_amount,waived_amount,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_journal
 FOREIGN KEY (customer_journal_id,tenant_id,settlement_id,result_status,request_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.journals (id,tenant_id,operation_id,kind,reference,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.journals ADD CONSTRAINT pp_cd_original_finish_journal
 FOREIGN KEY (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_outcomes (customer_journal_id,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.journal_lines ADD CONSTRAINT pp_cd_line_journal_identity
 FOREIGN KEY (journal_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.journals (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_billing_audit
 FOREIGN KEY (billing_audit_id,tenant_id,settlement_id,request_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_billing_outbox
 FOREIGN KEY (billing_outbox_id,tenant_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.projection_outbox (event_id,tenant_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_profile
 FOREIGN KEY (profile_block_model_id,profile_block_profile_version)
 REFERENCES {{MONI_SCHEMA}}.billing_profile_blocks (model_id,profile_version)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_profile_audit
 FOREIGN KEY (profile_block_audit_id,tenant_id,profile_block_model_id,request_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_tenant_control
 FOREIGN KEY (customer_tenant_control_id)
 REFERENCES {{MONI_SCHEMA}}.periodic_budget_controls (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_outcomes ADD CONSTRAINT pp_cd_finish_key_control
 FOREIGN KEY (customer_key_control_id)
 REFERENCES {{MONI_SCHEMA}}.periodic_budget_controls (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_finish
 FOREIGN KEY (finish_outcome_id,root_id,step_no,step_kind,source_id,created_xid,tenant_id)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_outcomes (id,root_id,step_no,step_kind,source_id,created_xid,tenant_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_account_kind
 FOREIGN KEY (account_id,account_kind)
 REFERENCES {{MONI_SCHEMA}}.ledger_accounts (id,kind)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_account_tenant
 FOREIGN KEY (account_id,account_tenant_id)
 REFERENCES {{MONI_SCHEMA}}.ledger_accounts (id,tenant_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects ADD CONSTRAINT pp_cd_ledger_line
 FOREIGN KEY (journal_line_id,account_id,signed_delta,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.journal_lines (id,account_id,signed_delta,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.journal_lines ADD CONSTRAINT pp_cd_original_ledger_effect
 FOREIGN KEY (id,account_id,signed_delta,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects (journal_line_id,account_id,signed_delta,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_finish
 FOREIGN KEY (finish_outcome_id,root_id,step_no,step_kind,source_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_outcomes (id,root_id,step_no,step_kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_new_settlement
 FOREIGN KEY (finish_outcome_id,original_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_customer_finish_outcomes (id,d_new_settlement)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_original_event
 FOREIGN KEY (event_id,control_id,allocation_id,original_source_kind,original_source_id)
 REFERENCES {{MONI_SCHEMA}}.periodic_budget_events (id,control_id,allocation_id,source_kind,source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_original_allocation
 FOREIGN KEY (control_id,allocation_id)
 REFERENCES {{MONI_SCHEMA}}.periodic_budget_allocations (control_id,id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_customer_finish_pb_effects ADD CONSTRAINT pp_cd_pb_original_scope
 FOREIGN KEY (control_id,scope)
 REFERENCES {{MONI_SCHEMA}}.periodic_budget_controls (id,scope)
 DEFERRABLE INITIALLY DEFERRED;

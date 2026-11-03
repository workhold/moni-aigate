-- B-P4 original-table constraints, isolated candidate material only.
-- Original columns/types/defaults/history remain unchanged; no backfill.
-- Replace {{MONI_SCHEMA}} with one safely quoted trusted schema identifier.
-- Target keys are the fixed union of source/account/cost FK interfaces, less
-- exact existing 001-022/B-P1 keys. Wide legacy TEXT keys require separate
-- data/length acceptance before installation; never truncate or hash to pass.
-- No functions, triggers, validators, grants, writer, or business activation.

-- @phase keys

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_events_35ae3e26_uq
 UNIQUE (id,actor,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_events_ab8c881d_uq
 UNIQUE (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_events_57534324_uq
 UNIQUE (id,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.finalization_records ADD CONSTRAINT pp_o_finalization_records_61c821a1_uq
 UNIQUE (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.finalization_records ADD CONSTRAINT pp_o_finalization_records_a2e49205_uq
 UNIQUE (id,tenant_id,request_id);

ALTER TABLE {{MONI_SCHEMA}}.journal_lines ADD CONSTRAINT pp_o_journal_lines_8438b3ee_uq
 UNIQUE (id,account_id,signed_delta,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.journals ADD CONSTRAINT pp_o_journals_ab8c881d_uq
 UNIQUE (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.journals ADD CONSTRAINT pp_o_journals_7135d54f_uq
 UNIQUE (id,tenant_id,operation_id,kind,reference,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.ledger_accounts ADD CONSTRAINT pp_o_ledger_accounts_3407ca14_uq
 UNIQUE (id,kind);

ALTER TABLE {{MONI_SCHEMA}}.ledger_accounts ADD CONSTRAINT pp_o_ledger_accounts_60a8df38_uq
 UNIQUE (id,tenant_id);

ALTER TABLE {{MONI_SCHEMA}}.metering_event_conflicts ADD CONSTRAINT pp_o_metering_event_conflicts_ab8c881d_uq
 UNIQUE (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.metering_events ADD CONSTRAINT pp_o_metering_events_ab8c881d_uq
 UNIQUE (id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.periodic_budget_controls ADD CONSTRAINT pp_o_periodic_budget_controls_b58af47f_uq
 UNIQUE (id,scope);

ALTER TABLE {{MONI_SCHEMA}}.periodic_budget_events ADD CONSTRAINT pp_o_periodic_budget_events_67e4a9e6_uq
 UNIQUE (id,control_id,allocation_id,source_kind,source_id);

ALTER TABLE {{MONI_SCHEMA}}.procurement_accounts ADD CONSTRAINT pp_o_procurement_accounts_17ec2a0f_uq
 UNIQUE (id,label,provider,state,limit_amount,revision,held_amount,spent_amount);

ALTER TABLE {{MONI_SCHEMA}}.procurement_accounts ADD CONSTRAINT pp_o_procurement_accounts_ce343791_uq
 UNIQUE (id,pp_version,pp_created_xid,pp_origin_journal_id,created_at);

ALTER TABLE {{MONI_SCHEMA}}.procurement_budget_events ADD CONSTRAINT pp_o_procurement_budget_events_dcddcdc2_uq
 UNIQUE (id,account_id,attempt_id,event_key,type,held_delta,spent_delta,evidence_ref,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.procurement_budget_events ADD CONSTRAINT pp_o_procurement_budget_events_cdae2585_uq
 UNIQUE (id,attempt_id,account_id);

ALTER TABLE {{MONI_SCHEMA}}.procurement_commands ADD CONSTRAINT pp_o_procurement_commands_c295f293_uq
 UNIQUE (operation_id,actor,kind,target_id,input_digest,status,reason,created_at,pp_source_id,pp_root_id,pp_root_step,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_procurement_cost_proposals_eb2263ee_uq
 UNIQUE (id,tenant_id,attempt_id,approver,approval_operation_id,pp_approval_root_id,pp_approval_root_step,pp_approval_source_kind,pp_approval_source_id,pp_approval_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_procurement_cost_proposals_7136ef38_uq
 UNIQUE (id,tenant_id,attempt_id,requester,expected_reservation_revision,expected_evidence_count,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.procurement_evidence_identities ADD CONSTRAINT pp_o_procurement_evidence_identities_427b999b_uq
 UNIQUE (attempt_id,evidence_digest,event_id);

ALTER TABLE {{MONI_SCHEMA}}.procurement_evidence_identities ADD CONSTRAINT pp_o_procurement_evidence_identities_0f2efa16_uq
 UNIQUE (attempt_id,evidence_digest,event_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.procurement_reservations ADD CONSTRAINT pp_o_procurement_reservations_a551d65b_uq
 UNIQUE (attempt_id,account_id,binding_id,pp_created_xid,pp_origin_journal_id,pb_admitted_at);

ALTER TABLE {{MONI_SCHEMA}}.projection_outbox ADD CONSTRAINT pp_o_projection_outbox_db89ff4f_uq
 UNIQUE (event_id,tenant_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.provider_cost_corrections ADD CONSTRAINT pp_o_provider_cost_corrections_a27cb28a_uq
 UNIQUE (id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.provider_cost_corrections ADD CONSTRAINT pp_o_provider_cost_corrections_de8841db_uq
 UNIQUE (id,tenant_id,attempt_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.reconciliation_cases ADD CONSTRAINT pp_o_reconciliation_cases_36248e61_uq
 UNIQUE (id,tenant_id,request_id,created_at);

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_o_request_records_fd9a52e0_uq
 UNIQUE (id,pp_last_decision_id);

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_o_request_records_d25b0701_uq
 UNIQUE (id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid);

ALTER TABLE {{MONI_SCHEMA}}.reservations ADD CONSTRAINT pp_o_reservations_d051283c_uq
 UNIQUE (id,tenant_id,request_id,amount,deadline);

ALTER TABLE {{MONI_SCHEMA}}.settlements ADD CONSTRAINT pp_o_settlements_891de2d6_uq
 UNIQUE (id,tenant_id,request_id,journal_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid);

ALTER TABLE {{MONI_SCHEMA}}.settlements ADD CONSTRAINT pp_o_settlements_490ef2b8_uq
 UNIQUE (id,tenant_id,request_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount);

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts ADD CONSTRAINT pp_o_upstream_attempts_b9511f91_uq
 UNIQUE (id,pp_dispatch_source_id,pp_dispatch_root_id,pp_dispatch_root_step,pp_dispatch_xid,pp_dispatch_account_id,pp_dispatch_journal_id,pb_dispatch_checked_at);

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts ADD CONSTRAINT pp_o_upstream_attempts_bae4c117_uq
 UNIQUE (id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at);

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts ADD CONSTRAINT pp_o_upstream_attempts_191efc0f_uq
 UNIQUE (tenant_id,request_id,id);

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_events_b0447a51_uq
 UNIQUE (pp_source_kind,pp_source_id,pp_source_role);

ALTER TABLE {{MONI_SCHEMA}}.projection_outbox ADD CONSTRAINT pp_o_projection_outbox_b0447a51_uq
 UNIQUE (pp_source_kind,pp_source_id,pp_source_role);

ALTER TABLE {{MONI_SCHEMA}}.journals ADD CONSTRAINT pp_o_journals_b0447a51_uq
 UNIQUE (pp_source_kind,pp_source_id,pp_source_role);

-- @phase checks

ALTER TABLE {{MONI_SCHEMA}}.request_records ADD CONSTRAINT pp_o_request_birth_ck
 CHECK (((pp_origin_root_id IS NULL AND pp_origin_root_step IS NULL AND pp_origin_xid IS NULL AND pp_last_decision_id IS NULL) OR ((pp_origin_root_id IS NOT NULL AND pp_origin_root_step IS NOT NULL AND pp_origin_xid IS NOT NULL) AND pp_origin_root_step > 0 AND pp_origin_xid <> '0'::pg_catalog.xid8)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts ADD CONSTRAINT pp_o_attempt_birth_ck
 CHECK (((pp_origin_root_id IS NULL AND pp_origin_root_step IS NULL AND pp_origin_xid IS NULL AND pp_quote_selected_at IS NULL AND pp_last_cost_mutation_id IS NULL AND pp_last_delivery_source_id IS NULL AND pp_dispatch_source_id IS NULL AND pp_dispatch_root_id IS NULL AND pp_dispatch_root_step IS NULL AND pp_dispatch_xid IS NULL AND pp_dispatch_account_id IS NULL AND pp_dispatch_journal_id IS NULL) OR ((pp_origin_root_id IS NOT NULL AND pp_origin_root_step IS NOT NULL AND pp_origin_xid IS NOT NULL AND pp_quote_selected_at IS NOT NULL) AND pp_origin_root_step > 0 AND pp_origin_xid <> '0'::pg_catalog.xid8 AND pg_catalog.isfinite(pp_quote_selected_at) AND ((pp_dispatch_source_id IS NULL AND pp_dispatch_root_id IS NULL AND pp_dispatch_root_step IS NULL AND pp_dispatch_xid IS NULL AND pp_dispatch_account_id IS NULL AND pp_dispatch_journal_id IS NULL) OR ((pp_dispatch_source_id IS NOT NULL AND pp_dispatch_root_id IS NOT NULL AND pp_dispatch_root_step IS NOT NULL AND pp_dispatch_xid IS NOT NULL) AND pp_dispatch_root_step > 0 AND pp_dispatch_xid <> '0'::pg_catalog.xid8 AND pb_dispatch_checked_at IS NOT NULL AND pg_catalog.isfinite(pb_dispatch_checked_at) AND ((pp_dispatch_account_id IS NULL AND pp_dispatch_journal_id IS NULL) OR (pp_dispatch_account_id IS NOT NULL AND pp_dispatch_journal_id IS NOT NULL)))) AND (delivery_state <> 'sending' OR (pp_dispatch_source_id IS NOT NULL AND pp_dispatch_root_id IS NOT NULL AND pp_dispatch_root_step IS NOT NULL AND pp_dispatch_xid IS NOT NULL)))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_accounts ADD CONSTRAINT pp_o_account_birth_ck
 CHECK (((pp_version IS NULL AND pp_created_xid IS NULL AND pp_origin_journal_id IS NULL) OR ((pp_version IS NOT NULL AND pp_created_xid IS NOT NULL AND pp_origin_journal_id IS NOT NULL) AND pp_version = 1 AND pp_created_xid <> '0'::pg_catalog.xid8)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_reservations ADD CONSTRAINT pp_o_procurement_birth_ck
 CHECK (((pp_created_xid IS NULL AND pp_origin_journal_id IS NULL AND pp_last_journal_id IS NULL) OR ((pp_created_xid IS NOT NULL AND pp_origin_journal_id IS NOT NULL AND pp_last_journal_id IS NOT NULL) AND pp_created_xid <> '0'::pg_catalog.xid8)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_budget_events ADD CONSTRAINT pp_o_procurement_event_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_transition_id IS NULL AND pp_journal_id IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_transition_id IS NOT NULL AND pp_journal_id IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8)) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_commands ADD CONSTRAINT pp_o_command_source_ck
 CHECK (((pp_source_id IS NULL AND pp_root_id IS NULL AND pp_root_step IS NULL AND pp_created_xid IS NULL AND pp_account_journal_id IS NULL) OR ((pp_source_id IS NOT NULL AND pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND kind IN ('account_create','account_configure') AND ((kind = 'account_configure' AND status = 'rejected' AND reason = 'not_found' AND pp_account_journal_id IS NULL) OR (NOT (kind = 'account_configure' AND status = 'rejected' AND reason = 'not_found') AND pp_account_journal_id IS NOT NULL)))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.metering_events ADD CONSTRAINT pp_o_meter_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'meter_choice' AND pp_source_role = 'meter_origin'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.metering_event_conflicts ADD CONSTRAINT pp_o_conflict_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'meter_choice' AND pp_source_role = 'conflict'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.finalization_records ADD CONSTRAINT pp_o_finalization_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'request_decision' AND pp_source_role = 'finalization'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_evidence_identities ADD CONSTRAINT pp_o_identity_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'evidence_choice' AND pp_source_role = 'evidence_identity'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_proposal_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'proposal_birth' AND pp_source_role = 'proposal_birth'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_approval_group_ck
 CHECK (((pp_approval_root_id IS NULL AND pp_approval_root_step IS NULL AND pp_approval_source_kind IS NULL AND pp_approval_source_id IS NULL AND pp_approval_source_role IS NULL AND pp_approval_created_xid IS NULL) OR ((pp_approval_root_id IS NOT NULL AND pp_approval_root_step IS NOT NULL AND pp_approval_source_kind IS NOT NULL AND pp_approval_source_id IS NOT NULL AND pp_approval_source_role IS NOT NULL AND pp_approval_created_xid IS NOT NULL) AND pp_approval_root_step > 0 AND pp_approval_created_xid <> '0'::pg_catalog.xid8 AND (pp_approval_source_kind = 'approval_consumed' AND pp_approval_source_role = 'approval'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.provider_cost_corrections ADD CONSTRAINT pp_o_correction_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind IN ('approval_consumed','unmanaged_correction') AND pp_source_role = 'correction'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.reconciliation_cases ADD CONSTRAINT pp_o_case_last_group_ck
 CHECK (((pp_last_root_id IS NULL AND pp_last_root_step IS NULL AND pp_last_source_kind IS NULL AND pp_last_source_id IS NULL AND pp_last_source_role IS NULL AND pp_last_created_xid IS NULL) OR ((pp_last_root_id IS NOT NULL AND pp_last_root_step IS NOT NULL AND pp_last_source_kind IS NOT NULL AND pp_last_source_id IS NOT NULL AND pp_last_source_role IS NOT NULL AND pp_last_created_xid IS NOT NULL) AND pp_last_root_step > 0 AND pp_last_created_xid <> '0'::pg_catalog.xid8 AND (pp_last_source_kind = 'reconciliation' AND pp_last_source_role = 'reconciliation'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.settlements ADD CONSTRAINT pp_o_settlement_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'reconciliation' AND pp_source_role = 'settlement'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.journals ADD CONSTRAINT pp_o_journal_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'reconciliation' AND pp_source_role = 'customer_journal'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.journal_lines ADD CONSTRAINT pp_o_line_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND (pp_source_kind = 'reconciliation' AND pp_source_role = 'ledger_line'))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND ((pp_source_kind = 'account_command' AND pp_source_role = 'account_command' AND action IN ('procurement.command.applied','procurement.command.rejected')) OR (pp_source_kind = 'proposal_birth' AND pp_source_role = 'proposal' AND action = 'procurement.cost_proposed') OR (pp_source_kind = 'approval_consumed' AND pp_source_role = 'approval' AND action = 'procurement.cost_approved') OR (pp_source_kind = 'unmanaged_correction' AND pp_source_role = 'unmanaged_correction' AND action = 'cost.corrected') OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'reconciliation' AND action IN ('reconciliation.proposed','reconciliation.approved','reconciliation.released')) OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'settlement' AND action IN ('billing.settled','billing.released')) OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'profile_block' AND action = 'billing.profile_blocked')))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.projection_outbox ADD CONSTRAINT pp_o_outbox_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND ((pp_source_kind = 'cost_outbox' AND pp_source_role = 'cost' AND kind IN ('cost_recorded','cost_corrected')) OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'settlement' AND kind IN ('settled','released'))))) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_proposal_approval_union_ck
 CHECK ((((pp_approval_root_id IS NULL AND pp_approval_root_step IS NULL AND pp_approval_source_kind IS NULL AND pp_approval_source_id IS NULL AND pp_approval_source_role IS NULL AND pp_approval_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND state = 'approved')) AND ((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((state = 'proposed' AND (pp_approval_root_id IS NULL AND pp_approval_root_step IS NULL AND pp_approval_source_kind IS NULL AND pp_approval_source_id IS NULL AND pp_approval_source_role IS NULL AND pp_approval_created_xid IS NULL)) OR (state = 'approved' AND (pp_approval_root_id IS NOT NULL AND pp_approval_root_step IS NOT NULL AND pp_approval_source_kind IS NOT NULL AND pp_approval_source_id IS NOT NULL AND pp_approval_source_role IS NOT NULL AND pp_approval_created_xid IS NOT NULL))))) IS TRUE);

-- @phase foreign-keys

ALTER TABLE {{MONI_SCHEMA}}.metering_events ADD CONSTRAINT pp_o_meter_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.metering_event_conflicts ADD CONSTRAINT pp_o_conflict_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.finalization_records ADD CONSTRAINT pp_o_finalization_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_evidence_identities ADD CONSTRAINT pp_o_identity_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_proposal_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals ADD CONSTRAINT pp_o_approval_step_fk
 FOREIGN KEY (pp_approval_root_id,pp_approval_root_step,pp_approval_source_kind,pp_approval_source_id,pp_approval_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.provider_cost_corrections ADD CONSTRAINT pp_o_correction_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.reconciliation_cases ADD CONSTRAINT pp_o_case_last_step_fk
 FOREIGN KEY (pp_last_root_id,pp_last_root_step,pp_last_source_kind,pp_last_source_id,pp_last_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.settlements ADD CONSTRAINT pp_o_settlement_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.journals ADD CONSTRAINT pp_o_journal_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.journal_lines ADD CONSTRAINT pp_o_line_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.projection_outbox ADD CONSTRAINT pp_o_outbox_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no,kind,source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

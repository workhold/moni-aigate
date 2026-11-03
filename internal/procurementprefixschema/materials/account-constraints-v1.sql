-- B-P4 ACCOUNT DOMAIN CONSTRAINT CANDIDATE. Not a migration or writer.
-- Apply only through the reviewed phase assembler after all fixed tables/columns.
-- Replace {{MONI_SCHEMA}} with a safely quoted SQL identifier. No PG execution claimed.

-- @phase keys

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_pk PRIMARY KEY (account_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_pk PRIMARY KEY (account_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_pk PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_create_operation_id UNIQUE (create_operation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_command_source_id UNIQUE (command_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_journal_id UNIQUE (journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_original UNIQUE (account_id, version, created_xid, journal_id, created_at);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_pointer UNIQUE (account_id, journal_id, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_current UNIQUE (account_id, label, provider, state, limit_amount, configuration_revision, held_amount, spent_amount);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_ordinal UNIQUE (account_id, ordinal);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_position UNIQUE (account_id, ordinal, id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_position_xid UNIQUE (account_id, ordinal, id, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_step UNIQUE (root_id, step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_source UNIQUE (source_kind, source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_head UNIQUE (account_id, ordinal, id, created_xid, after_label, after_provider, after_state, after_limit_amount, after_configuration_revision, after_held_amount, after_spent_amount, after_blocking_count);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_p_origin UNIQUE (account_id, attempt_id, created_xid, id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_p_current UNIQUE (id, account_id, attempt_id, after_reservation_revision, after_reservation_state, after_reservation_held_amount, after_reservation_spent_amount, after_reservation_anomaly);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_command UNIQUE (id, account_id, command_source_id, command_operation_id, root_id, step_no, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_transition UNIQUE (id, account_id, attempt_id, transition_id, procurement_event_id, root_id, step_no, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_dispatch UNIQUE (id, account_id, dispatch_attempt_id, delivery_source_id, root_id, step_no, created_xid, pb_decision_at);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_admission UNIQUE (id, account_id, attempt_id, pb_decision_at, root_id, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_original UNIQUE (operation_id, actor_id, kind, target_id, input_digest, command_status, command_reason, command_created_at, id, root_id, step_no, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_create UNIQUE (operation_id, id, target_id, root_id, step_no, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_operation_id UNIQUE (operation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_audit_id UNIQUE (audit_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_original UNIQUE (event_id, account_id, attempt_id, event_key, event_type, held_delta, spent_delta, evidence_ref, root_id, step_no, id, journal_id, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_cause UNIQUE (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id, event_id, journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_cause_event UNIQUE (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id, event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_journal UNIQUE (id, event_id, account_id, attempt_id, root_id, step_no, created_xid, journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_before UNIQUE (id, account_id, attempt_id, before_revision, before_state, before_held, before_spent, before_anomaly);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_before_attempt UNIQUE (id, attempt_id, before_revision, before_state, before_held, before_spent, before_anomaly);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_after UNIQUE (id, account_id, attempt_id, after_revision, after_state, after_held, after_spent, after_anomaly);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_quote UNIQUE (id, account_id, attempt_id, binding_id, input_bound, output_bound, upper_amount, cost_version_id, cost_input_rate, cost_output_rate, cost_basis);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_event_id UNIQUE (event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_journal_id UNIQUE (journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_original UNIQUE (proposal_id, tenant_id, attempt_id, requester, reservation_revision, member_count, root_id, step_no, step_kind, id, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_barrier UNIQUE (id, proposal_id, tenant_id, request_id, attempt_id, requester, account_id, reservation_revision, reservation_state, reservation_held, reservation_spent, reservation_anomaly, member_count);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_member_tail UNIQUE (id, attempt_id, member_count, member_tail);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_proposal_id UNIQUE (proposal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_audit_id UNIQUE (audit_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_original UNIQUE (proposal_id, tenant_id, attempt_id, approver, approval_operation_id, root_id, step_no, step_kind, id, created_xid);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_proposal_id UNIQUE (proposal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_approval_operation_id UNIQUE (approval_operation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_provider_correction_id UNIQUE (provider_correction_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_cost_mutation_id UNIQUE (cost_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_outbox_id UNIQUE (outbox_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_audit_id UNIQUE (audit_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_procurement_transition_id UNIQUE (procurement_transition_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_procurement_event_id UNIQUE (procurement_event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_account_journal_id UNIQUE (account_journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_procurement_transition_id UNIQUE (procurement_transition_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_delivery_mutation_id UNIQUE (delivery_mutation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_procurement_event_id UNIQUE (procurement_event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_account_journal_id UNIQUE (account_journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_frozen_quote UNIQUE (attempt_id, account_id, binding_id, cost_version_id, cost_input_rate, cost_output_rate, cost_basis);

ALTER TABLE {{MONI_SCHEMA}}.pp_request_origins
 ADD CONSTRAINT pp_ad_request_frozen_bounds UNIQUE (request_id, tenant_id, input_upper_bound, output_limit);

-- @phase checks

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_values CHECK (
  version=1 AND pg_catalog.isfinite(created_at)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_values CHECK (
  ordinal>0 AND configuration_revision>0 AND limit_amount>=0 AND held_amount>=0 AND spent_amount>=0 AND blocking_count>=0 AND state IN ('active','paused')
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_position_ck CHECK (
  ordinal>0 AND ((ordinal=1 AND kind='create' AND previous_id IS NULL) OR (ordinal>1 AND kind<>'create' AND previous_id IS NOT NULL)) AND pg_catalog.isfinite(created_at)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_kinds CHECK (
  kind IN ('create', 'configure', 'configure_rejected', 'reserve', 'dispatch', 'released_no_consumption', 'consumption_unknown', 'cost_confirmed', 'evidence_disputed', 'expired_without_permit', 'recovered_unknown', 'cost_corrected')
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_source_ck CHECK (
  (source_kind='command' AND step_kind='account_command' AND kind IN ('create','configure','configure_rejected') AND command_source_id IS NOT NULL AND transition_id IS NULL AND delivery_source_id IS NULL) OR
  (source_kind='budget_event' AND step_kind='procurement_transition' AND kind NOT IN ('create','configure','configure_rejected','dispatch') AND command_source_id IS NULL AND transition_id IS NOT NULL AND delivery_source_id IS NULL) OR
  (source_kind='dispatch' AND step_kind='attempt_delivery' AND kind='dispatch' AND command_source_id IS NULL AND transition_id IS NULL AND delivery_source_id IS NOT NULL AND attempt_id=source_id)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_config_before CHECK (
  (kind='create' AND (before_label IS NULL AND before_provider IS NULL AND before_state IS NULL AND before_limit_amount IS NULL AND before_configuration_revision IS NULL AND before_held_amount IS NULL AND before_spent_amount IS NULL AND before_blocking_count IS NULL)) OR (kind<>'create' AND (before_label IS NOT NULL AND before_provider IS NOT NULL AND before_state IS NOT NULL AND before_limit_amount IS NOT NULL AND before_configuration_revision IS NOT NULL AND before_held_amount IS NOT NULL AND before_spent_amount IS NOT NULL AND before_blocking_count IS NOT NULL) AND before_state IN ('active','paused') AND before_configuration_revision>0 AND before_limit_amount>=0 AND before_held_amount>=0 AND before_spent_amount>=0 AND before_blocking_count>=0)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_config_after CHECK (
  after_state IN ('active','paused') AND after_configuration_revision>0 AND after_limit_amount>=0 AND after_held_amount>=0 AND after_spent_amount>=0 AND after_blocking_count>=0 AND ((kind='create' AND after_configuration_revision=1 AND after_held_amount=0 AND after_spent_amount=0 AND after_blocking_count=0) OR (kind<>'create' AND after_configuration_revision::pg_catalog.numeric=before_configuration_revision::pg_catalog.numeric+CASE WHEN (before_label, before_provider, before_state, before_limit_amount) IS DISTINCT FROM (after_label, after_provider, after_state, after_limit_amount) THEN 1 ELSE 0 END))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_reservation_union CHECK (
  (source_kind='command' AND (attempt_id IS NULL AND binding_id IS NULL AND input_bound IS NULL AND output_bound IS NULL AND upper_amount IS NULL AND cost_version_id IS NULL AND cost_input_rate IS NULL AND cost_output_rate IS NULL AND cost_basis IS NULL AND before_reservation_revision IS NULL AND before_reservation_state IS NULL AND before_reservation_held_amount IS NULL AND before_reservation_spent_amount IS NULL AND before_reservation_anomaly IS NULL AND after_reservation_revision IS NULL AND after_reservation_state IS NULL AND after_reservation_held_amount IS NULL AND after_reservation_spent_amount IS NULL AND after_reservation_anomaly IS NULL AND gate_extra IS NULL AND gate_checked_at IS NULL AND pb_decision_at IS NULL AND pause_event_id IS NULL)) OR
  (source_kind IN ('budget_event','dispatch') AND (attempt_id IS NOT NULL AND binding_id IS NOT NULL AND input_bound IS NOT NULL AND output_bound IS NOT NULL AND upper_amount IS NOT NULL AND cost_version_id IS NOT NULL AND cost_input_rate IS NOT NULL AND cost_output_rate IS NOT NULL AND cost_basis IS NOT NULL AND after_reservation_revision IS NOT NULL AND after_reservation_state IS NOT NULL AND after_reservation_held_amount IS NOT NULL AND after_reservation_spent_amount IS NOT NULL AND after_reservation_anomaly IS NOT NULL) AND input_bound>0 AND output_bound>=0 AND upper_amount>=0 AND cost_input_rate>=0 AND cost_output_rate>=0 AND cost_basis IN ('contract','invoice') AND after_reservation_state IN ('prepared', 'sending', 'unknown', 'settled', 'released', 'disputed') AND after_reservation_revision>0 AND after_reservation_held_amount>=0 AND after_reservation_spent_amount>=0 AND ((kind='reserve' AND (before_reservation_revision IS NULL AND before_reservation_state IS NULL AND before_reservation_held_amount IS NULL AND before_reservation_spent_amount IS NULL AND before_reservation_anomaly IS NULL)) OR (kind<>'reserve' AND (before_reservation_revision IS NOT NULL AND before_reservation_state IS NOT NULL AND before_reservation_held_amount IS NOT NULL AND before_reservation_spent_amount IS NOT NULL AND before_reservation_anomaly IS NOT NULL) AND before_reservation_revision>0 AND before_reservation_state IN ('prepared', 'sending', 'unknown', 'settled', 'released', 'disputed') AND before_reservation_held_amount>=0 AND before_reservation_spent_amount>=0)))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_gate CHECK (
  (kind IN ('reserve','dispatch') AND (gate_extra IS NOT NULL AND gate_checked_at IS NOT NULL AND pb_decision_at IS NOT NULL) AND pg_catalog.isfinite(gate_checked_at) AND pg_catalog.isfinite(pb_decision_at) AND gate_extra=CASE kind WHEN 'reserve' THEN upper_amount ELSE 0 END AND before_state='active' AND before_blocking_count=0 AND before_held_amount+before_spent_amount+gate_extra<=before_limit_amount) OR (kind NOT IN ('reserve','dispatch') AND (gate_extra IS NULL AND gate_checked_at IS NULL AND pb_decision_at IS NULL))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_dispatch_ck CHECK (
  kind<>'dispatch' OR ((before_label, before_provider, before_state, before_limit_amount, before_configuration_revision, before_held_amount, before_spent_amount, before_blocking_count) IS NOT DISTINCT FROM (after_label, after_provider, after_state, after_limit_amount, after_configuration_revision, after_held_amount, after_spent_amount, after_blocking_count) AND before_reservation_state='prepared' AND after_reservation_state='sending' AND after_reservation_revision::pg_catalog.numeric=before_reservation_revision::pg_catalog.numeric+1 AND before_reservation_held_amount=upper_amount AND after_reservation_held_amount=upper_amount AND before_reservation_spent_amount=0 AND after_reservation_spent_amount=0 AND NOT before_reservation_anomaly AND NOT after_reservation_anomaly)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_pause CHECK (
  (pause_event_id IS NULL AND (source_kind<>'budget_event' OR NOT after_reservation_anomaly)) OR (pause_event_id IS NOT NULL AND source_kind='budget_event' AND after_reservation_anomaly AND pause_event_id=procurement_event_id AND after_state='paused')
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_bucket_delta CHECK (
  source_kind<>'budget_event' OR (after_blocking_count::pg_catalog.numeric=before_blocking_count::pg_catalog.numeric-CASE WHEN before_reservation_anomaly AND before_reservation_state IN ('unknown','disputed') THEN 1 ELSE 0 END+CASE WHEN after_reservation_anomaly AND after_reservation_state IN ('unknown','disputed') THEN 1 ELSE 0 END AND after_label=before_label AND after_provider=before_provider AND after_limit_amount=before_limit_amount AND after_state=CASE WHEN after_reservation_anomaly THEN 'paused' ELSE before_state END)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_values CHECK (
  actor_role='admin' AND actor_status='active' AND input_limit_amount>=0 AND input_state IN ('active','paused') AND pg_catalog.octet_length(input_label) BETWEEN 1 AND 100 AND pg_catalog.octet_length(input_provider) BETWEEN 1 AND 100 AND input_digest ~ '^[0-9a-f]{64}$' AND operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND pg_catalog.isfinite(created_at) AND pg_catalog.isfinite(command_created_at)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_branches CHECK (
  (kind='account_create' AND input_target_id='' AND input_expected_revision IS NULL AND command_status='applied' AND command_reason='' AND journal_id IS NOT NULL) OR
  (kind='account_configure' AND input_target_id=target_id AND input_expected_revision IS NOT NULL AND input_expected_revision>0 AND ((command_status='applied' AND command_reason='' AND journal_id IS NOT NULL) OR (command_status='rejected' AND command_reason='revision_conflict' AND journal_id IS NOT NULL) OR (command_status='rejected' AND command_reason='not_found' AND journal_id IS NULL)))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_scalar CHECK (
  pg_catalog.isfinite(created_at) AND input_bound>0 AND output_bound>=0 AND upper_amount>=0 AND cost_input_rate>=0 AND cost_output_rate>=0 AND cost_basis IN ('contract','invoice') AND pg_catalog.octet_length(event_key) BETWEEN 1 AND 400 AND pg_catalog.octet_length(evidence_ref)<=256 AND evidence_ref !~ '[[:cntrl:]]' AND after_revision>0 AND after_state IN ('prepared', 'sending', 'unknown', 'settled', 'released', 'disputed') AND after_held>=0 AND after_spent>=0
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_cause_ck CHECK (
  (cause_kind='attempt_birth' AND event_type='reserved') OR (cause_kind='evidence_choice' AND event_type IN ('released_no_consumption','consumption_unknown','cost_confirmed','evidence_disputed')) OR (cause_kind='approval_consumed' AND event_type='cost_corrected') OR (cause_kind='recovery' AND event_type IN ('expired_without_permit','recovered_unknown'))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_before_ck CHECK (
  (event_type='reserved' AND (before_revision IS NULL AND before_state IS NULL AND before_held IS NULL AND before_spent IS NULL AND before_anomaly IS NULL) AND after_revision=1 AND after_state='prepared' AND after_held=upper_amount AND after_spent=0 AND NOT after_anomaly) OR
  (event_type<>'reserved' AND (before_revision IS NOT NULL AND before_state IS NOT NULL AND before_held IS NOT NULL AND before_spent IS NOT NULL AND before_anomaly IS NOT NULL) AND before_revision>0 AND before_state IN ('prepared', 'sending', 'unknown', 'settled', 'released', 'disputed') AND before_held>=0 AND before_spent>=0 AND after_revision::pg_catalog.numeric=before_revision::pg_catalog.numeric+CASE WHEN (before_state, before_held, before_spent, before_anomaly) IS DISTINCT FROM (after_state, after_held, after_spent, after_anomaly) THEN 1 ELSE 0 END)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_deltas CHECK (
  after_held=coalesce(before_held,0)+held_delta AND after_spent=coalesce(before_spent,0)+spent_delta
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_targets CHECK (
  (event_type='reserved' AND event_key='reserve:'||attempt_id AND evidence_ref='') OR
  (event_type IN ('released_no_consumption','expired_without_permit') AND after_state='released' AND after_held=0 AND after_spent=before_spent AND after_spent=0 AND NOT after_anomaly AND event_key='release:'||attempt_id) OR
  (event_type IN ('consumption_unknown','recovered_unknown') AND after_state='unknown' AND after_held=before_held AND after_spent=before_spent AND NOT after_anomaly AND event_key='unknown:'||attempt_id) OR
  (event_type IN ('cost_confirmed','cost_corrected') AND after_state='settled' AND after_held=0 AND ((event_type='cost_confirmed' AND event_key='settle:'||attempt_id) OR (event_type='cost_corrected' AND event_key LIKE 'correction:%'))) OR
  (event_type='evidence_disputed' AND after_state='disputed' AND after_held=greatest(upper_amount::pg_catalog.numeric-before_spent,0) AND after_spent=before_spent AND after_anomaly AND event_key LIKE 'conflict:%')
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_original_p CHECK (
  (after_state NOT IN ('prepared','sending') OR (after_held=upper_amount AND after_spent=0 AND NOT after_anomaly)) AND (after_state NOT IN ('settled','released') OR after_held=0) AND (after_state<>'released' OR after_spent=0)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_p_values CHECK (
  reservation_revision>0 AND reservation_state IN ('prepared', 'sending', 'unknown', 'settled', 'released', 'disputed') AND reservation_held>=0 AND reservation_spent>=0 AND account_ordinal_cut>0
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_p_values CHECK (
  procurement_before_revision>0 AND procurement_before_state IN ('prepared', 'sending', 'unknown', 'settled', 'released', 'disputed') AND procurement_before_held>=0 AND procurement_before_spent>=0 AND account_ordinal_cut>0
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_p_values CHECK (
  procurement_before_revision>0 AND procurement_before_state IN ('prepared', 'sending') AND procurement_before_held>=0 AND procurement_before_spent>=0
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_actor CHECK (
  requester_role='finance_operator' AND requester_status='active' AND reservation_state NOT IN ('prepared','sending') AND member_count>=0 AND ((member_count=0 AND member_tail IS NULL) OR (member_count>0 AND member_tail IS NOT NULL))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_actor CHECK (
  requester_role='finance_operator' AND requester_status='active' AND approver_role='finance_approver' AND approver_status='active' AND requester<>approver AND consumed_revision>0 AND consumed_state IN ('unknown', 'settled', 'released', 'disputed') AND consumed_held>=0 AND consumed_spent>=0 AND account_ordinal_cut>0 AND consumed_member_count>=0 AND ((consumed_member_count=0 AND consumed_member_tail IS NULL) OR (consumed_member_count>0 AND consumed_member_tail IS NOT NULL))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_cost CHECK (
  before_cost_status IN ('unknown','contract','invoice') AND ((before_cost_status='unknown' AND before_cost_amount IS NULL) OR (before_cost_status IN ('contract','invoice') AND before_cost_amount IS NOT NULL AND before_cost_amount>=0)) AND after_state='settled' AND after_held=0 AND after_spent>=0 AND after_revision::pg_catalog.numeric=consumed_revision::pg_catalog.numeric+CASE WHEN (consumed_state,consumed_held,consumed_spent,consumed_anomaly) IS DISTINCT FROM (after_state,after_held,after_spent,after_anomaly) THEN 1 ELSE 0 END
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_identity CHECK (
  identity_attempt_id=attempt_id AND identity_digest=evidence_digest AND evidence_digest ~ '^[0-9a-f]{64}$' AND member_count_before>=0 AND member_count_after>=0 AND ((member_count_before=0 AND member_tail_before IS NULL) OR (member_count_before>0 AND member_tail_before IS NOT NULL)) AND ((member_count_after=0 AND member_tail_after IS NULL) OR (member_count_after>0 AND member_tail_after IS NOT NULL))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_members CHECK (
  (disposition='existing_member' AND decision='skip_existing_identity' AND member_count_after=member_count_before AND member_tail_after IS NOT DISTINCT FROM member_tail_before AND effective_conflict IS NULL AND conflict_cause IS NULL) OR
  (disposition='new_member' AND decision<>'skip_existing_identity' AND member_count_after::pg_catalog.numeric=member_count_before::pg_catalog.numeric+1 AND member_tail_after IS NOT NULL AND effective_conflict IS NOT NULL AND conflict_cause IS NOT NULL AND conflict_cause IN ('none','incoming_meter_conflict','released_positive_usage','settled_positive_no_consumption') AND effective_conflict=(conflict_cause<>'none'))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_decision CHECK (
  (decision IN ('skip_existing_identity','skip_terminal_normal','skip_already_disputed','skip_duplicate_dispute_event','skip_already_unknown') AND skip_reason IS NOT NULL AND skip_reason=pg_catalog.substring(decision,6) AND (procurement_transition_id IS NULL AND procurement_event_id IS NULL AND account_journal_id IS NULL)) OR (decision IN ('dispute', 'release_no_consumption', 'mark_unknown', 'confirm_cost') AND skip_reason IS NULL AND (procurement_transition_id IS NOT NULL AND procurement_event_id IS NOT NULL AND account_journal_id IS NOT NULL))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_duplicate CHECK (
  (decision='skip_duplicate_dispute_event' AND existing_dispute_event_id IS NOT NULL) OR (decision<>'skip_duplicate_dispute_event' AND existing_dispute_event_id IS NULL)
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_decision CHECK (
  pg_catalog.isfinite(checked_at) AND pg_catalog.isfinite(request_deadline) AND checked_at>=request_deadline AND delivery_before_state IN ('prepared','sending','completed','failed','unknown') AND ((decision='expired_without_permit' AND procurement_before_state='prepared' AND delivery_before_state='prepared' AND permit_source_id IS NULL) OR (decision='recovered_unknown' AND (procurement_before_state<>'prepared' OR delivery_before_state<>'prepared' OR permit_source_id IS NOT NULL)))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_money_delta CHECK (
  source_kind<>'budget_event' OR (after_held_amount-before_held_amount=after_reservation_held_amount-coalesce(before_reservation_held_amount,0) AND after_spent_amount-before_spent_amount=after_reservation_spent_amount-coalesce(before_reservation_spent_amount,0))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_command_buckets CHECK (
  source_kind<>'command' OR kind='create' OR ((before_held_amount,before_spent_amount,before_blocking_count) IS NOT DISTINCT FROM (after_held_amount,after_spent_amount,after_blocking_count) AND (kind<>'configure_rejected' OR (before_label,before_provider,before_state,before_limit_amount,before_configuration_revision) IS NOT DISTINCT FROM (after_label,after_provider,after_state,after_limit_amount,after_configuration_revision)))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_predecessor CHECK (
  (event_type='reserved' AND cause_source_id=attempt_id) OR (event_type IN ('released_no_consumption','consumption_unknown','cost_confirmed') AND before_state IN ('prepared','sending','unknown')) OR (event_type='evidence_disputed' AND before_state<>'disputed') OR (event_type='expired_without_permit' AND before_state='prepared' AND evidence_ref='') OR (event_type='recovered_unknown' AND before_state IN ('prepared','sending') AND evidence_ref='') OR (event_type='cost_corrected' AND before_state IN ('unknown','settled','released','disputed'))
 );

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_before_p CHECK (
  event_type='reserved' OR ((before_state NOT IN ('prepared','sending') OR (before_held=upper_amount AND before_spent=0 AND NOT before_anomaly)) AND (before_state NOT IN ('settled','released') OR before_held=0) AND (before_state<>'released' OR before_spent=0))
 );

-- @phase foreign-keys

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_account FOREIGN KEY (account_id, version, created_xid, journal_id, created_at)
 REFERENCES {{MONI_SCHEMA}}.procurement_accounts (id, pp_version, pp_created_xid, pp_origin_journal_id, created_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_accounts
 ADD CONSTRAINT pp_ad_account_origin FOREIGN KEY (id, pp_origin_journal_id, pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id, journal_id, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_command FOREIGN KEY (create_operation_id, command_source_id, account_id, root_id, step_no, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (operation_id, id, target_id, root_id, step_no, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_journal FOREIGN KEY (journal_id, account_id, command_source_id, create_operation_id, root_id, step_no, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id, account_id, command_source_id, command_operation_id, root_id, step_no, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_origin FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_journal FOREIGN KEY (account_id, ordinal, journal_id, updated_xid, label, provider, state, limit_amount, configuration_revision, held_amount, spent_amount, blocking_count)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id, ordinal, id, created_xid, after_label, after_provider, after_state, after_limit_amount, after_configuration_revision, after_held_amount, after_spent_amount, after_blocking_count)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_account FOREIGN KEY (account_id, label, provider, state, limit_amount, configuration_revision, held_amount, spent_amount)
 REFERENCES {{MONI_SCHEMA}}.procurement_accounts (id, label, provider, state, limit_amount, revision, held_amount, spent_amount)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_origin FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_previous FOREIGN KEY (account_id, previous_ordinal, previous_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id, ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_binding FOREIGN KEY (binding_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.credential_procurement_bindings (id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_reservations
 ADD CONSTRAINT pp_ad_p_origin_journal FOREIGN KEY (account_id, attempt_id, pp_created_xid, pp_origin_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id, attempt_id, created_xid, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_reservations
 ADD CONSTRAINT pp_ad_p_last_journal FOREIGN KEY (pp_last_journal_id, account_id, attempt_id, revision, state, held_amount, spent_amount, anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id, account_id, attempt_id, after_reservation_revision, after_reservation_state, after_reservation_held_amount, after_reservation_spent_amount, after_reservation_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_original_fk FOREIGN KEY (operation_id, actor_id, kind, target_id, input_digest, command_status, command_reason, command_created_at, id, root_id, step_no, created_xid)
 REFERENCES {{MONI_SCHEMA}}.procurement_commands (operation_id, actor, kind, target_id, input_digest, status, reason, created_at, pp_source_id, pp_root_id, pp_root_step, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_commands
 ADD CONSTRAINT pp_ad_original_command_fk FOREIGN KEY (operation_id, actor, kind, target_id, input_digest, status, reason, created_at, pp_source_id, pp_root_id, pp_root_step, pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (operation_id, actor_id, kind, target_id, input_digest, command_status, command_reason, command_created_at, id, root_id, step_no, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_journal_fk FOREIGN KEY (journal_id, target_id, id, operation_id, root_id, step_no, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id, account_id, command_source_id, command_operation_id, root_id, step_no, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_command_fk FOREIGN KEY (command_source_id, root_id, step_no, step_kind, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (id, root_id, step_no, step_kind, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_original_fk FOREIGN KEY (event_id, account_id, attempt_id, event_key, event_type, held_delta, spent_delta, evidence_ref, root_id, step_no, id, journal_id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.procurement_budget_events (id, account_id, attempt_id, event_key, type, held_delta, spent_delta, evidence_ref, pp_root_id, pp_root_step, pp_transition_id, pp_journal_id, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_budget_events
 ADD CONSTRAINT pp_ad_original_transition_fk FOREIGN KEY (id, account_id, attempt_id, event_key, type, held_delta, spent_delta, evidence_ref, pp_root_id, pp_root_step, pp_transition_id, pp_journal_id, pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (event_id, account_id, attempt_id, event_key, event_type, held_delta, spent_delta, evidence_ref, root_id, step_no, id, journal_id, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_binding FOREIGN KEY (binding_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.credential_procurement_bindings (id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_journal_fk FOREIGN KEY (journal_id, account_id, attempt_id, id, event_id, root_id, step_no, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id, account_id, attempt_id, transition_id, procurement_event_id, root_id, step_no, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_transition_fk FOREIGN KEY (transition_id, procurement_event_id, account_id, attempt_id, root_id, step_no, created_xid, id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, event_id, account_id, attempt_id, root_id, step_no, created_xid, journal_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_transition_before FOREIGN KEY (transition_id, account_id, attempt_id, before_reservation_revision, before_reservation_state, before_reservation_held_amount, before_reservation_spent_amount, before_reservation_anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, account_id, attempt_id, before_revision, before_state, before_held, before_spent, before_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_transition_after FOREIGN KEY (transition_id, account_id, attempt_id, after_reservation_revision, after_reservation_state, after_reservation_held_amount, after_reservation_spent_amount, after_reservation_anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, account_id, attempt_id, after_revision, after_state, after_held, after_spent, after_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_transition_quote FOREIGN KEY (transition_id, account_id, attempt_id, binding_id, input_bound, output_bound, upper_amount, cost_version_id, cost_input_rate, cost_output_rate, cost_basis)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, account_id, attempt_id, binding_id, input_bound, output_bound, upper_amount, cost_version_id, cost_input_rate, cost_output_rate, cost_basis)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_dispatch_source FOREIGN KEY (delivery_source_id, root_id, step_no, step_kind, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_delivery_sources (id, root_id, step_no, step_kind, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_dispatch_attempt FOREIGN KEY (delivery_source_id, root_id, created_xid, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_delivery_sources (id, root_id, created_xid, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_dispatch_original FOREIGN KEY (dispatch_attempt_id, delivery_source_id, root_id, step_no, created_xid, account_id, id, pb_decision_at)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (id, pp_dispatch_source_id, pp_dispatch_root_id, pp_dispatch_root_step, pp_dispatch_xid, pp_dispatch_account_id, pp_dispatch_journal_id, pb_dispatch_checked_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts
 ADD CONSTRAINT pp_ad_dispatch_journal FOREIGN KEY (pp_dispatch_journal_id, pp_dispatch_account_id, id, pp_dispatch_source_id, pp_dispatch_root_id, pp_dispatch_root_step, pp_dispatch_xid, pb_dispatch_checked_at)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id, account_id, dispatch_attempt_id, delivery_source_id, root_id, step_no, created_xid, pb_decision_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_binding FOREIGN KEY (binding_id, credential_id, channel_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.credential_procurement_bindings (id, credential_id, channel_id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_account FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_p FOREIGN KEY (attempt_id, account_id, binding_id, created_xid, account_journal_id, admitted_at)
 REFERENCES {{MONI_SCHEMA}}.procurement_reservations (attempt_id, account_id, binding_id, pp_created_xid, pp_origin_journal_id, pb_admitted_at)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_admission FOREIGN KEY (account_journal_id, account_id, attempt_id, admitted_at, root_id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id, account_id, attempt_id, pb_decision_at, root_id, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_transition FOREIGN KEY (procurement_transition_id, root_id, created_xid, tenant_id, request_id, attempt_id, step_kind, attempt_id, reserved_event_id, account_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id, event_id, journal_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_transition FOREIGN KEY (procurement_transition_id, root_id, created_xid, tenant_id, request_id, attempt_id, step_kind, id, procurement_event_id, account_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id, event_id, journal_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_transition FOREIGN KEY (procurement_transition_id, root_id, created_xid, tenant_id, request_id, attempt_id, step_kind, id, procurement_event_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id, event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_transition FOREIGN KEY (procurement_transition_id, root_id, created_xid, tenant_id, request_id, attempt_id, step_kind, id, procurement_event_id, account_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id, event_id, journal_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_account_cut FOREIGN KEY (account_id, account_ordinal_cut, account_tail_cut)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id, ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_account_cut FOREIGN KEY (account_id, account_ordinal_cut, account_tail_cut)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id, ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_account_cut FOREIGN KEY (account_id, account_ordinal_cut, account_tail_cut)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (account_id, ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_before_member_cut FOREIGN KEY (attempt_id, member_count_before, member_tail_before)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id, member_ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_after_member_cut FOREIGN KEY (attempt_id, member_count_after, member_tail_after)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id, member_ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_member_cut FOREIGN KEY (attempt_id, member_count, member_tail)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id, member_ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_member_cut FOREIGN KEY (attempt_id, consumed_member_count, consumed_member_tail)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id, member_ordinal, id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_member FOREIGN KEY (attempt_id, identity_digest, identity_event_id)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_members (attempt_id, identity_digest, identity_event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_identity_fk FOREIGN KEY (identity_attempt_id, identity_digest, identity_event_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_evidence_identities (attempt_id, evidence_digest, event_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_old_dispute FOREIGN KEY (existing_dispute_event_id, attempt_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_budget_events (id, attempt_id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_original_fk FOREIGN KEY (proposal_id, tenant_id, attempt_id, requester, reservation_revision, member_count, root_id, step_no, step_kind, id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.procurement_cost_proposals (id, tenant_id, attempt_id, requester, expected_reservation_revision, expected_evidence_count, pp_root_id, pp_root_step, pp_source_kind, pp_source_id, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals
 ADD CONSTRAINT pp_ad_original_proposal_fk FOREIGN KEY (id, tenant_id, attempt_id, requester, expected_reservation_revision, expected_evidence_count, pp_root_id, pp_root_step, pp_source_kind, pp_source_id, pp_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_proposal_births (proposal_id, tenant_id, attempt_id, requester, reservation_revision, member_count, root_id, step_no, step_kind, id, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_proposal FOREIGN KEY (proposal_birth_id, proposal_id, tenant_id, request_id, attempt_id, requester, account_id, consumed_revision, consumed_state, consumed_held, consumed_spent, consumed_anomaly, consumed_member_count)
 REFERENCES {{MONI_SCHEMA}}.pp_proposal_births (id, proposal_id, tenant_id, request_id, attempt_id, requester, account_id, reservation_revision, reservation_state, reservation_held, reservation_spent, reservation_anomaly, member_count)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_proposal_tail FOREIGN KEY (proposal_birth_id, attempt_id, consumed_member_count, consumed_member_tail)
 REFERENCES {{MONI_SCHEMA}}.pp_proposal_births (id, attempt_id, member_count, member_tail)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_original_fk FOREIGN KEY (proposal_id, tenant_id, attempt_id, approver, approval_operation_id, root_id, step_no, step_kind, id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.procurement_cost_proposals (id, tenant_id, attempt_id, approver, approval_operation_id, pp_approval_root_id, pp_approval_root_step, pp_approval_source_kind, pp_approval_source_id, pp_approval_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_cost_proposals
 ADD CONSTRAINT pp_ad_original_approval_fk FOREIGN KEY (id, tenant_id, attempt_id, approver, approval_operation_id, pp_approval_root_id, pp_approval_root_step, pp_approval_source_kind, pp_approval_source_id, pp_approval_created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_approval_consumptions (proposal_id, tenant_id, attempt_id, approver, approval_operation_id, root_id, step_no, step_kind, id, created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_correction FOREIGN KEY (provider_correction_id, tenant_id, attempt_id, root_id, step_no, step_kind, id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.provider_cost_corrections (id, tenant_id, attempt_id, pp_root_id, pp_root_step, pp_source_kind, pp_source_id, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_mutation FOREIGN KEY (cost_mutation_id, root_id, created_xid, tenant_id, request_id, attempt_id, step_kind, id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_mutations (id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_outbox FOREIGN KEY (outbox_id, root_id, created_xid, tenant_id, request_id, attempt_id, step_kind, id)
 REFERENCES {{MONI_SCHEMA}}.pp_cost_outbox_links (outbox_id, root_id, created_xid, tenant_id, request_id, attempt_id, cause_kind, cause_source_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_audit FOREIGN KEY (audit_id, root_id, step_no, step_kind, id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id, pp_root_id, pp_root_step, pp_source_kind, pp_source_id, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_proposal_births
 ADD CONSTRAINT pp_ad_proposal_audit FOREIGN KEY (audit_id, root_id, step_no, step_kind, id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id, pp_root_id, pp_root_step, pp_source_kind, pp_source_id, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_audit FOREIGN KEY (audit_id, root_id, step_no, step_kind, id, created_xid)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id, pp_root_id, pp_root_step, pp_source_kind, pp_source_id, pp_created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_delivery FOREIGN KEY (delivery_mutation_id, root_id, created_xid, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_delivery_sources (id, root_id, created_xid, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_permit FOREIGN KEY (permit_source_id, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_delivery_sources (id, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_transition_before FOREIGN KEY (procurement_transition_id, account_id, attempt_id, procurement_before_revision, procurement_before_state, procurement_before_held, procurement_before_spent, procurement_before_anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, account_id, attempt_id, before_revision, before_state, before_held, before_spent, before_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_recovery_sources
 ADD CONSTRAINT pp_ad_recovery_transition_before FOREIGN KEY (procurement_transition_id, attempt_id, procurement_before_revision, procurement_before_state, procurement_before_held, procurement_before_spent, procurement_before_anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, attempt_id, before_revision, before_state, before_held, before_spent, before_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_transition_before FOREIGN KEY (procurement_transition_id, account_id, attempt_id, consumed_revision, consumed_state, consumed_held, consumed_spent, consumed_anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, account_id, attempt_id, before_revision, before_state, before_held, before_spent, before_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_approval_consumptions
 ADD CONSTRAINT pp_ad_approval_transition_after FOREIGN KEY (procurement_transition_id, account_id, attempt_id, after_revision, after_state, after_held, after_spent, after_anomaly)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id, account_id, attempt_id, after_revision, after_state, after_held, after_spent, after_anomaly)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_from_attempt_birth FOREIGN KEY (d_attempt_birth, root_id, created_xid, tenant_id, request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id, root_id, created_xid, tenant_id, request_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_from_evidence_choice FOREIGN KEY (d_evidence_choice, root_id, created_xid, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_evidence_choices (id, root_id, created_xid, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_from_approval_consumed FOREIGN KEY (d_approval_consumed, root_id, created_xid, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_approval_consumptions (id, root_id, created_xid, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_from_recovery FOREIGN KEY (d_recovery, root_id, created_xid, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_recovery_sources (id, root_id, created_xid, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_frozen_quote FOREIGN KEY (attempt_id, account_id, binding_id, cost_version_id, cost_input_rate, cost_output_rate, cost_basis)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id, account_id, binding_id, cost_version_id, cost_input_rate, cost_output_rate, cost_basis)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_frozen_bounds FOREIGN KEY (request_id, tenant_id, input_bound, output_bound)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id, tenant_id, input_upper_bound, output_limit)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_incoming_source_id FOREIGN KEY (incoming_source_id, root_id, created_xid, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_incoming_sources (id, root_id, created_xid, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_evidence_choices
 ADD CONSTRAINT pp_ad_evidence_meter_choice_id FOREIGN KEY (meter_choice_id, root_id, created_xid, tenant_id, request_id, attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_meter_choices (id, root_id, created_xid, tenant_id, request_id, attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

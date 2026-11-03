-- birth-exact-link-a2-v1: 22 keys / 46 checks / 69 deferred FKs (native, existing-key replacement and exact locators).
-- Complete relationship candidate only; payload exactness also requires the
-- separate real guards, readonly validators, writer closure and catalog proof.
-- All keys precede checks and FKs. No payload-wide UK is installed.

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

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_pk
 PRIMARY KEY (attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_pk
 PRIMARY KEY (attempt_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_attempt_node_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_step_uq
 UNIQUE (root_id,step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_operation_id UNIQUE (operation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources
 ADD CONSTRAINT pp_ad_command_audit_id UNIQUE (audit_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_pk PRIMARY KEY (account_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_create_operation_id UNIQUE (create_operation_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_command_source_id UNIQUE (command_source_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins
 ADD CONSTRAINT pp_ad_origin_journal_id UNIQUE (journal_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_pk PRIMARY KEY (account_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_pk PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_step UNIQUE (root_id, step_no);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_pk
 PRIMARY KEY (id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_event_id UNIQUE (event_id);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_journal_id UNIQUE (journal_id);

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

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_step_ck
 CHECK ((step_no > 0) IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_step_ck
 CHECK ((step_no > 0 AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND id <> '00000000-0000-0000-0000-000000000000') IS TRUE);

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

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_o_audit_group_ck
 CHECK (((pp_root_id IS NULL AND pp_root_step IS NULL AND pp_source_kind IS NULL AND pp_source_id IS NULL AND pp_source_role IS NULL AND pp_created_xid IS NULL) OR ((pp_root_id IS NOT NULL AND pp_root_step IS NOT NULL AND pp_source_kind IS NOT NULL AND pp_source_id IS NOT NULL AND pp_source_role IS NOT NULL AND pp_created_xid IS NOT NULL) AND pp_root_step > 0 AND pp_created_xid <> '0'::pg_catalog.xid8 AND ((pp_source_kind = 'account_command' AND pp_source_role = 'account_command' AND action IN ('procurement.command.applied','procurement.command.rejected')) OR (pp_source_kind = 'proposal_birth' AND pp_source_role = 'proposal' AND action = 'procurement.cost_proposed') OR (pp_source_kind = 'approval_consumed' AND pp_source_role = 'approval' AND action = 'procurement.cost_approved') OR (pp_source_kind = 'unmanaged_correction' AND pp_source_role = 'unmanaged_correction' AND action = 'cost.corrected') OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'reconciliation' AND action IN ('reconciliation.proposed','reconciliation.approved','reconciliation.released')) OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'settlement' AND action IN ('billing.settled','billing.released')) OR (pp_source_kind = 'reconciliation' AND pp_source_role = 'profile_block' AND action = 'billing.profile_blocked')))) IS TRUE);

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

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_birth_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_s_attempt_birth_root_r_fk
 FOREIGN KEY (root_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_s_attempt_birth_root_a_fk
 FOREIGN KEY (root_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_s_attempt_birth_step_fk
 FOREIGN KEY (root_id,step_no)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_ba_s_step_attempt_birth_fk
 FOREIGN KEY (d_attempt_birth)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_ba_s_root_original_attempt_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_operation_roots ADD CONSTRAINT pp_ba_s_root_attempt_origin_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_s_attempt_original_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_s_attempt_birth_original_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.upstream_attempts (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.upstream_attempts ADD CONSTRAINT pp_ba_s_original_attempt_birth_fk
 FOREIGN KEY (pp_origin_root_id,pp_origin_root_step)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_s_attempt_origin_head_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_heads (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_s_attempt_head_origin_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_heads ADD CONSTRAINT pp_ba_s_attempt_head_node_fk
 FOREIGN KEY (last_node_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_nodes (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_s_node_origin_fk
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_ba_s_node_root_attempt_fk
 FOREIGN KEY (root_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_ba_s_node_step_fk
 FOREIGN KEY (root_id,step_no)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_nodes ADD CONSTRAINT pp_ba_s_node_previous_fk
 FOREIGN KEY (previous_node_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_nodes (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_s_account_command_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_ba_s_account_command_step_fk
 FOREIGN KEY (root_id,step_no)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_ba_s_step_account_command_fk
 FOREIGN KEY (d_account_command)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_s_procurement_transition_root_fk
 FOREIGN KEY (root_id,created_xid)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_s_procurement_transition_root_r_fk
 FOREIGN KEY (root_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_s_procurement_transition_root_a_fk
 FOREIGN KEY (root_id)
 REFERENCES {{MONI_SCHEMA}}.pp_operation_roots (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_s_procurement_transition_step_fk
 FOREIGN KEY (root_id,step_no)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_root_steps ADD CONSTRAINT pp_ba_s_step_procurement_transition_fk
 FOREIGN KEY (d_procurement_transition)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins ADD CONSTRAINT pp_ba_ad_origin_account
 FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_accounts (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_accounts ADD CONSTRAINT pp_ba_ad_account_origin
 FOREIGN KEY (pp_origin_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins ADD CONSTRAINT pp_ba_ad_origin_command
 FOREIGN KEY (command_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_origins ADD CONSTRAINT pp_ba_ad_origin_journal
 FOREIGN KEY (journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads
 ADD CONSTRAINT pp_ad_head_origin FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads ADD CONSTRAINT pp_ba_ad_head_journal
 FOREIGN KEY (journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_heads ADD CONSTRAINT pp_ba_ad_head_account
 FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_accounts (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_origin FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal ADD CONSTRAINT pp_ba_ad_journal_previous
 FOREIGN KEY (previous_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal
 ADD CONSTRAINT pp_ad_journal_binding FOREIGN KEY (binding_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.credential_procurement_bindings (id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_reservations ADD CONSTRAINT pp_ba_ad_p_origin_journal
 FOREIGN KEY (pp_origin_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_reservations ADD CONSTRAINT pp_ba_ad_p_last_journal
 FOREIGN KEY (pp_last_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_ba_ad_command_original_fk
 FOREIGN KEY (operation_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_commands (operation_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_commands ADD CONSTRAINT pp_ba_ad_original_command_fk
 FOREIGN KEY (pp_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_ba_ad_command_journal_fk
 FOREIGN KEY (journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_command_sources ADD CONSTRAINT pp_ba_ad_command_audit
 FOREIGN KEY (audit_id)
 REFERENCES {{MONI_SCHEMA}}.audit_events (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.audit_events ADD CONSTRAINT pp_ba_o_audit_step_fk
 FOREIGN KEY (pp_root_id,pp_root_step)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_ad_transition_original_fk
 FOREIGN KEY (event_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_budget_events (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.procurement_budget_events ADD CONSTRAINT pp_ba_ad_original_transition_fk
 FOREIGN KEY (pp_transition_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions
 ADD CONSTRAINT pp_ad_transition_binding FOREIGN KEY (binding_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.credential_procurement_bindings (id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_ad_transition_journal_fk
 FOREIGN KEY (journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal ADD CONSTRAINT pp_ba_ad_journal_command_fk
 FOREIGN KEY (command_source_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_command_sources (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal ADD CONSTRAINT pp_ba_ad_journal_transition_fk
 FOREIGN KEY (transition_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal ADD CONSTRAINT pp_ba_ad_journal_transition_before
 FOREIGN KEY (transition_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal ADD CONSTRAINT pp_ba_ad_journal_transition_after
 FOREIGN KEY (transition_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_account_journal ADD CONSTRAINT pp_ba_ad_journal_transition_quote
 FOREIGN KEY (transition_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_binding FOREIGN KEY (binding_id, credential_id, channel_id, account_id)
 REFERENCES {{MONI_SCHEMA}}.credential_procurement_bindings (id, credential_id, channel_id, account_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins
 ADD CONSTRAINT pp_ad_birth_account FOREIGN KEY (account_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_origins (account_id)
 DEFERRABLE INITIALLY DEFERRED;

-- Keep the managed-only activation of the old six-column MATCH SIMPLE edge.
-- account_id is NULL for unmanaged origins; attempt_id alone would wrongly
-- require a procurement reservation for that supported, unreserved branch.
-- The two-column referenced UNIQUE already belongs to original migration 014.
ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ad_birth_p
 FOREIGN KEY (attempt_id,account_id)
 REFERENCES {{MONI_SCHEMA}}.procurement_reservations (attempt_id,account_id)
 MATCH SIMPLE DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_ad_birth_admission
 FOREIGN KEY (account_journal_id)
 REFERENCES {{MONI_SCHEMA}}.pp_account_journal (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_attempt_origins ADD CONSTRAINT pp_ba_ad_birth_transition
 FOREIGN KEY (procurement_transition_id)
 REFERENCES {{MONI_SCHEMA}}.pp_procurement_transitions (id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_ad_transition_from_attempt_birth
 FOREIGN KEY (d_attempt_birth)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_ad_transition_frozen_quote
 FOREIGN KEY (attempt_id)
 REFERENCES {{MONI_SCHEMA}}.pp_attempt_origins (attempt_id)
 DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {{MONI_SCHEMA}}.pp_procurement_transitions ADD CONSTRAINT pp_ba_ad_transition_frozen_bounds
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)
 DEFERRABLE INITIALLY DEFERRED;

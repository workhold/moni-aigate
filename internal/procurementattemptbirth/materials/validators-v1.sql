-- birth-exact-link-a2-v1 readonly closure. Not a migration or serving profile.
-- Four leaves form a directed acyclic graph: account -> command/attempt;
-- request, command and attempt never call another leaf or a public validator.
-- No locks, writes, authority issuance, live catalog discovery or dynamic SQL.
-- Readonly identity checks do not impose mutation isolation/timeout settings.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(p_request text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 r {{MONI_SCHEMA}}.request_records%ROWTYPE; o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_request_heads%ROWTYPE; rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE;
 st {{MONI_SCHEMA}}.pp_root_steps%ROWTYPE;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records WHERE id=p_request;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=p_request;
 IF NOT FOUND THEN
  IF r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=p_request)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=p_request)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=p_request)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE request_id=p_request)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE request_id=p_request)
  THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 IF (r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at)
  IS DISTINCT FROM
  (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)
  OR (r.pp_origin_root_id,r.pp_origin_root_step,r.pp_origin_xid) IS DISTINCT FROM (o.root_id,o.step_no,o.created_xid)
  OR r.pp_last_decision_id IS NOT NULL OR r.latest_finalization_id IS NOT NULL
  OR (o.initial_status,o.initial_billing_status,o.initial_latest_id) IS DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)
  OR o.pb_created_xid IS DISTINCT FROM o.created_xid OR o.created_xid='0'::xid8 OR o.step_no<>1 OR o.step_kind<>'request_birth'
  OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.root_id,true)
  OR NOT (pg_catalog.isfinite(o.request_deadline) AND pg_catalog.isfinite(o.request_created_at) AND pg_catalog.isfinite(o.retail_priced_at) AND pg_catalog.isfinite(o.pb_admitted_at))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=p_request;
 IF NOT FOUND OR (h.ordinal,h.last_decision_id,h.updated_xid) IS DISTINCT FROM (0::bigint,NULL::text,o.created_xid)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=o.root_id;
 IF NOT FOUND OR (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
  IS DISTINCT FROM ('request_birth'::text,'request'::text,o.tenant_id,o.request_id,NULL::text,NULL::text,o.created_xid,{{MONI_APP_OID}}::oid,1::bigint,'born'::text)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id AND step_no=1;
 IF NOT FOUND OR (st.kind,st.source_id,st.created_xid,st.d_request_birth) IS DISTINCT FROM ('request_birth'::text,o.request_id,o.created_xid,o.request_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=p_request)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=p_request AND entry_kind='request_birth')<>1
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE pp_origin_root_id=o.root_id OR pp_dispatch_root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE pp_root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE pp_root_id=o.root_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_validate_attempt_leaf_v1(p_attempt text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE; o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE;
 q {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE; h {{MONI_SCHEMA}}.pp_attempt_heads%ROWTYPE;
 rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; st {{MONI_SCHEMA}}.pp_root_steps%ROWTYPE;
 n {{MONI_SCHEMA}}.pp_attempt_nodes%ROWTYPE; p {{MONI_SCHEMA}}.procurement_reservations%ROWTYPE;
 e {{MONI_SCHEMA}}.procurement_budget_events%ROWTYPE; tr {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE;
 j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; prev {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE;
 c {{MONI_SCHEMA}}.periodic_budget_controls%ROWTYPE; al {{MONI_SCHEMA}}.periodic_budget_allocations%ROWTYPE;
 pe {{MONI_SCHEMA}}.periodic_budget_events%ROWTYPE; w {{MONI_SCHEMA}}.periodic_budget_windows%ROWTYPE;
 v_upper numeric; v_managed boolean; v_event_kind text; v_event_source text; v_pb_xid xid8;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO u FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=p_attempt;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=p_attempt;
 IF NOT FOUND THEN
  IF u.pp_origin_root_id IS NULL AND u.pp_origin_root_step IS NULL AND u.pp_origin_xid IS NULL AND u.pp_quote_selected_at IS NULL
   AND u.pp_last_cost_mutation_id IS NULL AND u.pp_last_delivery_source_id IS NULL AND u.pp_dispatch_source_id IS NULL
   AND u.pp_dispatch_root_id IS NULL AND u.pp_dispatch_root_step IS NULL AND u.pp_dispatch_xid IS NULL AND u.pp_dispatch_account_id IS NULL AND u.pp_dispatch_journal_id IS NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=u.request_id)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations xp JOIN {{MONI_SCHEMA}}.procurement_accounts xa ON xa.id=xp.account_id
    WHERE xp.attempt_id=p_attempt AND (xa.pp_version IS NOT NULL OR xa.pp_created_xid IS NOT NULL OR xa.pp_origin_journal_id IS NOT NULL))
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=p_attempt)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE attempt_id=p_attempt)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE attempt_id=p_attempt)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='attempt_birth' AND source_id=p_attempt)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE attempt_id=p_attempt)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE attempt_id=p_attempt)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=p_attempt AND (pp_created_xid IS NOT NULL OR pp_origin_journal_id IS NOT NULL OR pp_last_journal_id IS NOT NULL))
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=p_attempt AND (pp_root_id IS NOT NULL OR pp_root_step IS NOT NULL OR pp_transition_id IS NOT NULL OR pp_journal_id IS NOT NULL OR pp_created_xid IS NOT NULL))
  THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 IF (u.id,u.tenant_id,u.request_id,u.sequence,u.channel_id,u.channel_version_id,u.credential_id,u.upstream_model,u.route_decisions,u.cost_version_id,u.cost_input_rate,u.cost_output_rate,u.cost_basis,u.pp_quote_selected_at,u.created_at,u.pb_created_xid)
  IS DISTINCT FROM (o.attempt_id,o.tenant_id,o.request_id,o.sequence,o.channel_id,o.channel_version_id,o.credential_id,o.upstream_model,o.route_decisions,o.cost_version_id,o.cost_input_rate,o.cost_output_rate,o.cost_basis,o.quote_selected_at,o.attempt_created_at,o.pb_created_xid)
  OR (u.pp_origin_root_id,u.pp_origin_root_step,u.pp_origin_xid) IS DISTINCT FROM (o.root_id,o.step_no,o.created_xid)
  OR (o.initial_delivery_state,o.initial_cost_status,o.initial_cost_amount) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric)
  OR (u.delivery_state,u.cost_status,u.cost_amount,u.pb_dispatch_checked_at) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric,NULL::timestamptz)
  OR u.pp_last_cost_mutation_id IS NOT NULL OR u.pp_last_delivery_source_id IS NOT NULL OR u.pp_dispatch_source_id IS NOT NULL
  OR u.pp_dispatch_root_id IS NOT NULL OR u.pp_dispatch_root_step IS NOT NULL OR u.pp_dispatch_xid IS NOT NULL OR u.pp_dispatch_account_id IS NOT NULL OR u.pp_dispatch_journal_id IS NOT NULL
  OR o.step_no<>1 OR o.step_kind<>'attempt_birth' OR o.created_xid='0'::xid8 OR o.pb_created_xid IS DISTINCT FROM o.created_xid
  OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.root_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.attempt_id,true)
  OR o.sequence NOT BETWEEN 1 AND 2 OR pg_catalog.octet_length(o.upstream_model) NOT BETWEEN 1 AND 200
  OR NOT pg_catalog.isfinite(o.quote_selected_at) OR NOT pg_catalog.isfinite(o.attempt_created_at)
  OR o.cost_basis NOT IN ('unknown','contract','invoice') OR o.cost_input_rate<0 OR o.cost_output_rate<0
  OR (o.cost_version_id IS NULL AND (o.cost_input_rate IS NOT NULL OR o.cost_output_rate IS NOT NULL OR o.cost_basis<>'unknown'))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 BEGIN
  PERFORM {{MONI_SCHEMA}}.pp_at_route_json_v1(o.route_decisions,o.channel_id,o.channel_version_id);
 EXCEPTION WHEN SQLSTATE 'PP101' THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END;
 SELECT * INTO q FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=o.request_id;
 IF NOT FOUND OR q.tenant_id IS DISTINCT FROM o.tenant_id THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF o.management_mode='managed' THEN
  IF o.account_id IS NULL OR o.binding_id IS NULL OR o.procurement_transition_id IS NULL OR o.reserved_event_id IS NULL OR o.account_journal_id IS NULL OR o.admitted_at IS NULL
   OR NOT pg_catalog.isfinite(o.admitted_at) OR o.cost_version_id IS NULL OR o.cost_input_rate IS NULL OR o.cost_output_rate IS NULL OR o.cost_basis NOT IN ('contract','invoice')
   OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.procurement_transition_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.reserved_event_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.account_journal_id,true)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_managed:=true;
 ELSIF o.management_mode='unmanaged' THEN
  IF o.account_id IS NOT NULL OR o.binding_id IS NOT NULL OR o.procurement_transition_id IS NOT NULL OR o.reserved_event_id IS NOT NULL OR o.account_journal_id IS NOT NULL OR o.admitted_at IS NOT NULL
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_managed:=false;
 ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=o.root_id;
 IF NOT FOUND OR (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
  IS DISTINCT FROM ('attempt_birth'::text,'request'::text,o.tenant_id,o.request_id,o.attempt_id,NULL::text,o.created_xid,{{MONI_APP_OID}}::oid,(CASE WHEN v_managed THEN 2 ELSE 1 END)::bigint,o.management_mode)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id AND step_no=1;
 IF NOT FOUND OR (st.kind,st.source_id,st.created_xid,st.d_attempt_birth) IS DISTINCT FROM ('attempt_birth'::text,o.attempt_id,o.created_xid,o.attempt_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=p_attempt;
 IF NOT FOUND OR h.updated_xid IS DISTINCT FROM o.created_xid
  OR (NOT v_managed AND (h.ordinal,h.last_node_id,h.member_count,h.member_tail_id) IS DISTINCT FROM (0::bigint,NULL::text,NULL::bigint,NULL::text))
  OR (v_managed AND (h.ordinal,h.member_count,h.member_tail_id) IS DISTINCT FROM (1::bigint,0::bigint,NULL::text))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE attempt_id=p_attempt)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id)<>(CASE WHEN v_managed THEN 2 ELSE 1 END)
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='attempt_birth' AND source_id=p_attempt)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.upstream_attempts WHERE pp_origin_root_id=o.root_id)<>1
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE pp_dispatch_root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=o.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE pp_root_id=o.root_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF NOT v_managed THEN
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=p_attempt)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=p_attempt OR pp_root_id=o.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE attempt_id=p_attempt OR root_id=o.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE attempt_id=p_attempt OR root_id=o.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE attempt_id=p_attempt OR root_id=o.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=p_attempt)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN;
 END IF;
 SELECT * INTO p FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=p_attempt;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_upper:=pg_catalog.ceil((q.input_upper_bound::numeric*o.cost_input_rate::numeric+q.output_limit::numeric*o.cost_output_rate::numeric)/1000000::numeric);
 IF v_upper<0 OR v_upper>9223372036854775807::numeric THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (p.account_id,p.binding_id,p.input_bound,p.output_bound,p.upper_amount,p.revision,p.state,p.held_amount,p.spent_amount,p.anomaly,p.pp_created_xid,p.pp_origin_journal_id,p.pp_last_journal_id,p.pb_created_xid,p.pb_admitted_at)
  IS DISTINCT FROM (o.account_id,o.binding_id,q.input_upper_bound,q.output_limit,v_upper,1::bigint,'prepared'::text,v_upper,0::numeric,false,o.created_xid,o.account_journal_id,o.account_journal_id,o.created_xid,o.admitted_at)
  OR NOT pg_catalog.isfinite(p.created_at) OR NOT pg_catalog.isfinite(p.updated_at)
  OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.credential_procurement_bindings b WHERE (b.id,b.credential_id,b.channel_id,b.account_id) IS NOT DISTINCT FROM (o.binding_id,o.credential_id,o.channel_id,o.account_id))
  OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins a WHERE a.account_id=o.account_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO tr FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=o.procurement_transition_id;
 IF NOT FOUND OR (tr.root_id,tr.step_no,tr.step_kind,tr.created_xid,tr.tenant_id,tr.request_id,tr.attempt_id,tr.account_id,tr.binding_id,tr.cause_kind,tr.cause_source_id,tr.event_id,tr.journal_id,tr.event_type,tr.event_key,tr.evidence_ref,tr.held_delta,tr.spent_delta)
  IS DISTINCT FROM (o.root_id,2::bigint,'procurement_transition'::text,o.created_xid,o.tenant_id,o.request_id,o.attempt_id,o.account_id,o.binding_id,'attempt_birth'::text,o.attempt_id,o.reserved_event_id,o.account_journal_id,'reserved'::text,'reserve:'||o.attempt_id,''::text,v_upper,0::numeric)
  OR (tr.before_revision,tr.before_state,tr.before_held,tr.before_spent,tr.before_anomaly) IS DISTINCT FROM (NULL::bigint,NULL::text,NULL::numeric,NULL::numeric,NULL::boolean)
  OR (tr.after_revision,tr.after_state,tr.after_held,tr.after_spent,tr.after_anomaly,tr.input_bound,tr.output_bound,tr.upper_amount,tr.cost_version_id,tr.cost_input_rate,tr.cost_output_rate,tr.cost_basis)
  IS DISTINCT FROM (p.revision,p.state,p.held_amount,p.spent_amount,p.anomaly,p.input_bound,p.output_bound,v_upper::bigint,o.cost_version_id,o.cost_input_rate,o.cost_output_rate,o.cost_basis)
  OR tr.d_attempt_birth IS DISTINCT FROM o.attempt_id OR NOT pg_catalog.isfinite(tr.created_at)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO e FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=o.reserved_event_id;
 IF NOT FOUND OR (e.account_id,e.attempt_id,e.event_key,e.type,e.held_delta,e.spent_delta,e.evidence_ref,e.pp_root_id,e.pp_root_step,e.pp_transition_id,e.pp_journal_id,e.pp_created_xid,e.pb_created_xid)
  IS DISTINCT FROM (tr.account_id,tr.attempt_id,tr.event_key,tr.event_type,tr.held_delta,tr.spent_delta,tr.evidence_ref,tr.root_id,tr.step_no,tr.id,tr.journal_id,tr.created_xid,tr.created_xid)
  OR NOT pg_catalog.isfinite(e.created_at)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=o.account_journal_id;
 IF NOT FOUND OR (j.account_id,j.root_id,j.step_no,j.step_kind,j.created_xid,j.kind,j.source_kind,j.source_id,j.attempt_id,j.binding_id,j.transition_id,j.procurement_event_id,j.command_source_id,j.delivery_source_id,j.command_operation_id,j.dispatch_attempt_id)
  IS DISTINCT FROM (tr.account_id,tr.root_id,tr.step_no,tr.step_kind,tr.created_xid,'reserve'::text,'budget_event'::text,e.id,tr.attempt_id,tr.binding_id,tr.id,e.id,NULL::text,NULL::text,NULL::text,NULL::text)
  OR (j.before_reservation_revision,j.before_reservation_state,j.before_reservation_held_amount,j.before_reservation_spent_amount,j.before_reservation_anomaly)
  IS DISTINCT FROM (tr.before_revision,tr.before_state,tr.before_held,tr.before_spent,tr.before_anomaly)
  OR (j.after_reservation_revision,j.after_reservation_state,j.after_reservation_held_amount,j.after_reservation_spent_amount,j.after_reservation_anomaly)
  IS DISTINCT FROM (tr.after_revision,tr.after_state,tr.after_held,tr.after_spent,tr.after_anomaly)
  OR (j.input_bound,j.output_bound,j.upper_amount,j.cost_version_id,j.cost_input_rate,j.cost_output_rate,j.cost_basis,j.gate_extra,j.gate_checked_at,j.pb_decision_at,j.pause_event_id)
  IS DISTINCT FROM (tr.input_bound,tr.output_bound,tr.upper_amount,tr.cost_version_id,tr.cost_input_rate,tr.cost_output_rate,tr.cost_basis,tr.upper_amount,tr.created_at,o.admitted_at,NULL::text)
  OR j.ordinal<=1 OR j.previous_id IS NULL OR j.previous_ordinal IS DISTINCT FROM j.ordinal-1 OR NOT pg_catalog.isfinite(j.created_at)
  OR (j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count)
  IS DISTINCT FROM (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount+v_upper,j.before_spent_amount,j.before_blocking_count)
  OR j.before_state IS DISTINCT FROM 'active' OR j.before_blocking_count IS DISTINCT FROM 0::bigint
  OR j.before_held_amount IS NULL OR j.before_spent_amount IS NULL OR j.before_limit_amount IS NULL
  OR j.before_held_amount+j.before_spent_amount+v_upper>j.before_limit_amount
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO prev FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=j.previous_id;
 IF NOT FOUND OR (prev.account_id,prev.ordinal,prev.after_label,prev.after_provider,prev.after_state,prev.after_limit_amount,prev.after_configuration_revision,prev.after_held_amount,prev.after_spent_amount,prev.after_blocking_count)
  IS DISTINCT FROM (j.account_id,j.previous_ordinal,j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id AND step_no=2;
 IF NOT FOUND OR (st.kind,st.source_id,st.created_xid,st.d_procurement_transition) IS DISTINCT FROM ('procurement_transition'::text,tr.id,o.created_xid,tr.id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO n FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE id=h.last_node_id;
 IF NOT FOUND OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(n.id,true)
  OR (n.attempt_id,n.ordinal,n.previous_node_id,n.root_id,n.step_no,n.step_kind,n.source_id,n.created_xid,n.previous_ordinal)
  IS DISTINCT FROM (o.attempt_id,1::bigint,NULL::text,o.root_id,2::bigint,'procurement_transition'::text,tr.id,o.created_xid,0::bigint)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE attempt_id=p_attempt OR root_id=o.root_id OR cause_kind='attempt_birth' AND cause_source_id=p_attempt)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=p_attempt OR pp_root_id=o.root_id OR pp_transition_id=tr.id OR pp_journal_id=j.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_journal WHERE attempt_id=p_attempt OR root_id=o.root_id OR transition_id=tr.id OR source_kind='budget_event' AND source_id=e.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE attempt_id=p_attempt OR root_id=o.root_id OR step_kind='procurement_transition' AND source_id=tr.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='procurement_transition' AND source_id=tr.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_reservations WHERE pp_origin_journal_id=j.id OR pp_last_journal_id=j.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE procurement_transition_id=tr.id OR reserved_event_id=e.id OR account_journal_id=j.id)<>1
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;

 -- A later Commander may import a pre-control tracked reservation. Preserve
 -- that original supported path; a birth marker can only name origin=new.
 SELECT * INTO c FROM {{MONI_SCHEMA}}.periodic_budget_controls WHERE scope='procurement' AND account_id=p.account_id;
 IF NOT FOUND THEN
  IF p.pb_control IS NOT NULL
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=p_attempt)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE source_kind='procurement_event' AND source_id=e.id)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN;
 END IF;
 SELECT * INTO al FROM {{MONI_SCHEMA}}.periodic_budget_allocations WHERE control_id=c.id AND subject_type='supplier_attempt' AND subject_id=p_attempt;
 IF NOT FOUND OR (al.upper_amount,al.baseline,al.source_held,al.source_spent,al.held_amount,al.spent_amount,al.state,al.revision)
  IS DISTINCT FROM (v_upper::bigint,0::numeric,v_upper,0::numeric,v_upper,0::numeric,'held'::text,1::bigint)
  OR al.created_xid='0'::xid8 OR NOT pg_catalog.isfinite(al.created_at)
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=p_attempt)<>1
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF p.pb_control IS NOT NULL THEN
  IF p.pb_control IS DISTINCT FROM c.id OR al.origin IS DISTINCT FROM 'new' OR al.origin_source_id IS DISTINCT FROM e.id
   OR al.admitted_at IS DISTINCT FROM o.admitted_at OR al.created_xid IS DISTINCT FROM o.created_xid OR al.window_id IS NULL
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO w FROM {{MONI_SCHEMA}}.periodic_budget_windows WHERE id=al.window_id;
  IF NOT FOUND OR (w.control_id,w.start_at,w.end_at) IS DISTINCT FROM (c.id,{{MONI_SCHEMA}}.pb_start(c.period_kind,o.admitted_at),{{MONI_SCHEMA}}.pb_end(c.period_kind,{{MONI_SCHEMA}}.pb_start(c.period_kind,o.admitted_at)))
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_event_kind:='procurement_event'; v_event_source:=e.id; v_pb_xid:=o.created_xid;
 ELSE
  IF al.origin IS DISTINCT FROM 'legacy_import' OR al.origin_source_id IS DISTINCT FROM p_attempt OR al.admitted_at IS NOT NULL OR al.window_id IS NOT NULL OR al.created_xid IS DISTINCT FROM c.created_xid
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_event_kind:='legacy_import'; v_event_source:=p_attempt; v_pb_xid:=c.created_xid;
 END IF;
 SELECT * INTO pe FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE id=al.last_event_id;
 IF NOT FOUND OR (pe.control_id,pe.allocation_id,pe.source_kind,pe.source_id,pe.allocation_before_revision,pe.before_held,pe.before_spent,pe.after_held,pe.after_spent,pe.after_source_spent,pe.after_state,pe.held_delta,pe.spent_delta,pe.created_xid)
  IS DISTINCT FROM (c.id,al.id,v_event_kind,v_event_source,0::bigint,0::numeric,0::numeric,v_upper,0::numeric,0::numeric,'held'::text,v_upper,0::numeric,v_pb_xid)
  OR pe.seq<=0 OR NOT pg_catalog.isfinite(pe.created_at)
  OR (al.window_id IS NULL AND pe.window_before_revision IS NOT NULL)
  OR (al.window_id IS NOT NULL AND (pe.window_before_revision IS NULL OR pe.window_before_revision<0))
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE allocation_id=al.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE source_kind='procurement_event' AND source_id=e.id)<>(CASE WHEN p.pb_control IS NULL THEN 0 ELSE 1 END)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(p_operation text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE c {{MONI_SCHEMA}}.procurement_commands%ROWTYPE; s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; r {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; a {{MONI_SCHEMA}}.audit_events%ROWTYPE; v_time timestamptz; v_before jsonb; v_after jsonb;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO c FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=p_operation;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF c.kind NOT IN ('account_create','account_configure') THEN
  IF c.pp_source_id IS NOT NULL OR c.pp_root_id IS NOT NULL OR c.pp_root_step IS NOT NULL OR c.pp_created_xid IS NOT NULL OR c.pp_account_journal_id IS NOT NULL OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE operation_id=p_operation) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE source_kind='command' AND source_id=p_operation) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE target=p_operation AND pp_root_id IS NOT NULL) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 END IF;
 IF c.pp_source_id IS NULL AND c.pp_root_id IS NULL AND c.pp_root_step IS NULL AND c.pp_created_xid IS NULL AND c.pp_account_journal_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE operation_id=p_operation) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE source_kind='command' AND source_id=p_operation) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE target=p_operation AND pp_root_id IS NOT NULL) THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=c.pp_source_id;
 IF NOT FOUND OR (c.operation_id,c.actor,c.kind,c.target_id,c.input_digest,c.status,c.reason,c.created_at,c.pp_root_id,c.pp_root_step,c.pp_created_xid,c.pp_account_journal_id) IS DISTINCT FROM (s.operation_id,s.actor_id,s.kind,s.target_id,s.input_digest,s.command_status,s.command_reason,s.command_created_at,s.root_id,s.step_no,s.created_xid,s.journal_id) OR s.actor_role<>'admin' OR s.actor_status<>'active' OR s.step_no<>1 OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(s.input_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(s.input_provider) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(s.id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(s.root_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(s.audit_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(s.actor_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(s.operation_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(s.target_id,s.kind='account_create') OR s.created_xid='0'::xid8 OR NOT pg_catalog.isfinite(s.created_at) OR NOT pg_catalog.isfinite(s.command_created_at) OR s.input_state NOT IN ('active','paused') OR s.input_limit_amount<0 OR s.input_digest IS DISTINCT FROM {{MONI_SCHEMA}}.pp_ac_command_digest_v1(s.kind,s.operation_id,s.input_target_id,s.input_expected_revision,s.input_label,s.input_provider,s.input_state,s.input_limit_amount) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=s.root_id;
 IF NOT FOUND OR (r.entry_kind,r.scope_kind,r.tenant_id,r.request_id,r.attempt_id,r.account_target_id,r.created_xid,r.session_role_oid,r.step_count,r.semantic_result) IS DISTINCT FROM (s.kind,'account_command'::text,NULL::text,NULL::text,NULL::text,s.target_id,s.created_xid,{{MONI_APP_OID}}::oid,1::bigint,s.command_status) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps st WHERE (st.root_id,st.step_no,st.kind,st.source_id,st.created_xid)=(s.root_id,1::bigint,'account_command'::text,s.id,s.created_xid)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=s.root_id)<>(CASE WHEN s.kind='account_create' THEN 1 ELSE 0 END) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=s.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=s.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE root_id=s.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=s.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=s.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE pp_origin_root_id=s.root_id OR pp_dispatch_root_id=s.root_id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE pp_root_id=s.root_id)
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_source_id=s.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='account_command' AND source_id=s.id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.audit_events WHERE pp_source_id=s.id)<>1
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO a FROM {{MONI_SCHEMA}}.audit_events WHERE id=s.audit_id;
 IF NOT FOUND OR (a.actor,a.action,a.target,a.tenant_scope,a.request_id,a.sanitized_diff,a.pp_root_id,a.pp_root_step,a.pp_source_kind,a.pp_source_id,a.pp_source_role,a.pp_created_xid) IS DISTINCT FROM (s.actor_id,'procurement.command.'||s.command_status,s.operation_id,NULL::text,''::text,pg_catalog.jsonb_build_object('kind',s.kind,'status',s.command_status),s.root_id,1::bigint,'account_command'::text,s.id,'account_command'::text,s.created_xid) OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.audit_events WHERE pp_root_id=s.root_id)<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_before:='null'::jsonb; v_after:='null'::jsonb;
 IF s.journal_id IS NULL THEN
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_accounts WHERE pp_origin_journal_id IN (SELECT id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=s.root_id)) OR s.kind<>'account_configure' OR s.input_target_id<>s.target_id OR s.input_expected_revision IS NULL OR s.input_expected_revision<=0 OR s.command_status<>'rejected' OR s.command_reason<>'not_found' OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE command_source_id=s.id OR root_id=s.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSE
  SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=s.journal_id;
  IF NOT FOUND OR (j.account_id,j.root_id,j.step_no,j.step_kind,j.created_xid,j.source_kind,j.source_id,j.command_source_id) IS DISTINCT FROM (s.target_id,s.root_id,1::bigint,'account_command'::text,s.created_xid,'command'::text,s.operation_id,s.id) OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_journal WHERE command_source_id=s.id OR (source_kind='command' AND source_id=s.operation_id))<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF j.source_kind<>'command' OR j.kind NOT IN ('create','configure','configure_rejected') OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(j.id,true) OR NOT pg_catalog.isfinite(j.created_at) OR j.created_xid='0'::xid8 OR j.step_no<>1 OR j.step_kind<>'account_command' OR j.attempt_id IS NOT NULL OR j.binding_id IS NOT NULL OR j.before_reservation_revision IS NOT NULL OR j.before_reservation_state IS NOT NULL OR j.before_reservation_held_amount IS NOT NULL OR j.before_reservation_spent_amount IS NOT NULL OR j.before_reservation_anomaly IS NOT NULL OR j.after_reservation_revision IS NOT NULL OR j.after_reservation_state IS NOT NULL OR j.after_reservation_held_amount IS NOT NULL OR j.after_reservation_spent_amount IS NOT NULL OR j.after_reservation_anomaly IS NOT NULL OR j.input_bound IS NOT NULL OR j.output_bound IS NOT NULL OR j.upper_amount IS NOT NULL OR j.cost_version_id IS NOT NULL OR j.cost_input_rate IS NOT NULL OR j.cost_output_rate IS NOT NULL OR j.cost_basis IS NOT NULL OR j.gate_extra IS NOT NULL OR j.gate_checked_at IS NOT NULL OR j.pb_decision_at IS NOT NULL OR j.pause_event_id IS NOT NULL OR j.transition_id IS NOT NULL OR j.delivery_source_id IS NOT NULL OR j.command_source_id IS NULL OR j.command_operation_id IS DISTINCT FROM j.source_id OR j.procurement_event_id IS NOT NULL OR j.dispatch_attempt_id IS NOT NULL OR j.previous_ordinal<>j.ordinal-1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT created_at INTO v_time FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=s.target_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF s.kind='account_create' THEN
   IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins ao WHERE (ao.account_id,ao.version,ao.create_operation_id,ao.command_source_id,ao.root_id,ao.step_no,ao.step_kind,ao.journal_id,ao.created_xid) IS NOT DISTINCT FROM (s.target_id,1::smallint,s.operation_id,s.id,s.root_id,1::bigint,'account_command'::text,s.journal_id,s.created_xid))
    OR (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (NULL::text,NULL::text,NULL::text,NULL::bigint,NULL::bigint,NULL::numeric,NULL::numeric,NULL::bigint)
    OR j.kind<>'create' OR j.ordinal<>1 OR j.previous_id IS NOT NULL OR j.after_configuration_revision<>1 OR j.after_held_amount<>0 OR j.after_spent_amount<>0 OR j.after_blocking_count<>0 OR s.command_status<>'applied' OR s.command_reason<>'' OR s.input_target_id<>'' OR s.input_expected_revision IS NOT NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   IF s.input_target_id<>s.target_id OR s.input_expected_revision IS NULL OR s.input_expected_revision<=0 OR j.kind NOT IN ('configure','configure_rejected') THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   v_before:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(j.account_id,j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_held_amount,j.before_spent_amount,j.before_configuration_revision,v_time);
   IF (j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (j.after_held_amount,j.after_spent_amount,j.after_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
  IF j.ordinal>1 AND j.after_configuration_revision::numeric IS DISTINCT FROM j.before_configuration_revision::numeric+(CASE WHEN (j.before_label,j.before_provider,j.before_state,j.before_limit_amount) IS DISTINCT FROM (j.after_label,j.after_provider,j.after_state,j.after_limit_amount) THEN 1 ELSE 0 END)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF s.command_status='applied' THEN
   IF (j.after_label,j.after_provider,j.after_state,j.after_limit_amount) IS DISTINCT FROM (s.input_label,s.input_provider,s.input_state,s.input_limit_amount) OR (s.kind='account_configure' AND (j.kind<>'configure' OR j.before_configuration_revision<>s.input_expected_revision)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   v_after:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(j.account_id,j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_held_amount,j.after_spent_amount,j.after_configuration_revision,v_time);
  ELSIF s.command_reason<>'revision_conflict' OR j.kind<>'configure_rejected' OR j.before_configuration_revision=s.input_expected_revision OR (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 -- Go's historic process time zone is accepted by instant, not byte spelling.
 IF c.before_state IS DISTINCT FROM v_before THEN
  IF pg_catalog.jsonb_typeof(c.before_state)<>'object' OR pg_catalog.jsonb_typeof(v_before)<>'object' OR c.before_state-'created_at' IS DISTINCT FROM v_before-'created_at' OR (c.before_state->>'created_at') !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})$' THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  BEGIN IF (c.before_state->>'created_at')::timestamptz IS DISTINCT FROM v_time THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF; EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END;
 END IF;
 IF c.after_state IS DISTINCT FROM v_after THEN
  IF pg_catalog.jsonb_typeof(c.after_state)<>'object' OR pg_catalog.jsonb_typeof(v_after)<>'object' OR c.after_state-'created_at' IS DISTINCT FROM v_after-'created_at' OR (c.after_state->>'created_at') !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})$' THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  BEGIN IF (c.after_state->>'created_at')::timestamptz IS DISTINCT FROM v_time THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF; EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END;
 END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(p_account text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE; o {{MONI_SCHEMA}}.pp_account_origins%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE;
 prev {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; cs {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
 tr {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE; e {{MONI_SCHEMA}}.procurement_budget_events%ROWTYPE;
 rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE;
 c {{MONI_SCHEMA}}.periodic_budget_controls%ROWTYPE; w {{MONI_SCHEMA}}.periodic_budget_windows%ROWTYPE;
 v_count bigint:=0; v_held numeric; v_spent numeric; v_bheld numeric; v_bspent numeric; v_block bigint;
 v_events bigint; v_min_seq bigint; v_max_seq bigint; v_last text; v_allocation_held numeric; v_allocation_spent numeric;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=p_account;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF a.pp_version IS NULL AND a.pp_created_xid IS NULL AND a.pp_origin_journal_id IS NULL
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=p_account)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=p_account)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE account_id=p_account)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE target_id=p_account AND journal_id IS NOT NULL)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE target_id=p_account AND (pp_source_id IS NOT NULL OR pp_root_id IS NOT NULL OR pp_root_step IS NOT NULL OR pp_created_xid IS NOT NULL OR pp_account_journal_id IS NOT NULL)
   AND NOT (kind='account_configure' AND status='rejected' AND reason='not_found' AND pp_account_journal_id IS NULL))
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots ar WHERE ar.account_target_id=p_account
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources ns WHERE (ns.root_id,ns.kind,ns.target_id,ns.command_status,ns.command_reason,ns.journal_id) IS NOT DISTINCT FROM (ar.id,'account_configure'::text,p_account,'rejected'::text,'not_found'::text,NULL::text)))
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE account_id=p_account)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE account_id=p_account)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE account_id=p_account AND (pp_created_xid IS NOT NULL OR pp_origin_journal_id IS NOT NULL OR pp_last_journal_id IS NOT NULL))
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE account_id=p_account AND (pp_root_id IS NOT NULL OR pp_root_step IS NOT NULL OR pp_transition_id IS NOT NULL OR pp_journal_id IS NOT NULL OR pp_created_xid IS NOT NULL))
 THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=p_account;
 IF NOT FOUND OR (a.pp_version,a.pp_created_xid,a.pp_origin_journal_id,a.created_at) IS DISTINCT FROM (o.version,o.created_xid,o.journal_id,o.created_at)
  OR o.version<>1 OR o.created_xid='0'::xid8 OR o.step_no<>1 OR o.step_kind<>'account_command' OR NOT pg_catalog.isfinite(o.created_at)
  OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.root_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.command_source_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(o.journal_id,true)
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_accounts WHERE pp_origin_journal_id=o.journal_id)<>1
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO cs FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=o.command_source_id;
 IF NOT FOUND OR (cs.kind,cs.operation_id,cs.target_id,cs.root_id,cs.step_no,cs.created_xid,cs.journal_id)
  IS DISTINCT FROM ('account_create'::text,o.create_operation_id,o.account_id,o.root_id,o.step_no,o.created_xid,o.journal_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=p_account;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 FOR j IN SELECT * FROM {{MONI_SCHEMA}}.pp_account_journal WHERE account_id=p_account ORDER BY ordinal,id LOOP
  IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(j.id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(j.root_id,true)
   OR j.created_xid='0'::xid8 OR NOT pg_catalog.isfinite(j.created_at) OR j.step_no<=0 OR j.ordinal<=0
   OR j.previous_ordinal IS DISTINCT FROM j.ordinal-1 OR j.after_configuration_revision<=0 OR j.after_state NOT IN ('active','paused')
   OR j.after_limit_amount<0 OR j.after_held_amount<0 OR j.after_spent_amount<0 OR j.after_blocking_count<0
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_count=9223372036854775807 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_count:=v_count+1;
  IF j.ordinal<>v_count THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_count=1 THEN
   IF (j.id,j.previous_id,j.kind,j.source_kind,j.root_id,j.step_no,j.step_kind,j.command_source_id,j.source_id,j.created_xid)
    IS DISTINCT FROM (o.journal_id,NULL::text,'create'::text,'command'::text,o.root_id,o.step_no,o.step_kind,o.command_source_id,o.create_operation_id,o.created_xid)
    OR (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count)
    IS DISTINCT FROM (NULL::text,NULL::text,NULL::text,NULL::bigint,NULL::bigint,NULL::numeric,NULL::numeric,NULL::bigint)
    OR (j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count) IS DISTINCT FROM (1::bigint,0::numeric,0::numeric,0::bigint)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   IF j.previous_id IS DISTINCT FROM prev.id
    OR (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count)
    IS DISTINCT FROM (prev.after_label,prev.after_provider,prev.after_state,prev.after_limit_amount,prev.after_configuration_revision,prev.after_held_amount,prev.after_spent_amount,prev.after_blocking_count)
    OR j.after_configuration_revision::numeric IS DISTINCT FROM j.before_configuration_revision::numeric+(CASE WHEN (j.before_label,j.before_provider,j.before_state,j.before_limit_amount) IS DISTINCT FROM (j.after_label,j.after_provider,j.after_state,j.after_limit_amount) THEN 1 ELSE 0 END)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
  IF j.source_kind='command' THEN
   IF j.kind NOT IN ('create','configure','configure_rejected') OR j.command_source_id IS NULL
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE journal_id=j.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE account_journal_id=j.id)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   PERFORM {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(j.source_id);
  ELSIF j.source_kind='budget_event' AND j.kind='reserve' THEN
   IF j.attempt_id IS NULL OR j.command_source_id IS NOT NULL OR j.delivery_source_id IS NOT NULL
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE journal_id=j.id)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   PERFORM {{MONI_SCHEMA}}.pp_ab_validate_attempt_leaf_v1(j.attempt_id);
  ELSIF j.source_kind='budget_event' AND j.kind IN ('released_no_consumption','consumption_unknown','cost_confirmed','evidence_disputed','expired_without_permit','recovered_unknown','cost_corrected') THEN
   -- Only an independent, structurally identified future domain is PP906.
   -- A damaged reserve cannot escape through a changed discriminator.
   SELECT * INTO tr FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=j.transition_id;
   IF NOT FOUND OR tr.cause_kind='attempt_birth' OR j.step_kind<>'procurement_transition' OR j.command_source_id IS NOT NULL OR j.delivery_source_id IS NOT NULL
    OR (tr.account_id,tr.attempt_id,tr.root_id,tr.step_no,tr.created_xid,tr.journal_id,tr.event_id,tr.event_type)
    IS DISTINCT FROM (j.account_id,j.attempt_id,j.root_id,j.step_no,j.created_xid,j.id,j.source_id,j.kind)
    OR (j.before_reservation_revision,j.before_reservation_state,j.before_reservation_held_amount,j.before_reservation_spent_amount,j.before_reservation_anomaly)
    IS DISTINCT FROM (tr.before_revision,tr.before_state,tr.before_held,tr.before_spent,tr.before_anomaly)
    OR (j.after_reservation_revision,j.after_reservation_state,j.after_reservation_held_amount,j.after_reservation_spent_amount,j.after_reservation_anomaly)
    IS DISTINCT FROM (tr.after_revision,tr.after_state,tr.after_held,tr.after_spent,tr.after_anomaly)
    OR (j.binding_id,j.input_bound,j.output_bound,j.upper_amount,j.cost_version_id,j.cost_input_rate,j.cost_output_rate,j.cost_basis)
    IS DISTINCT FROM (tr.binding_id,tr.input_bound,tr.output_bound,tr.upper_amount,tr.cost_version_id,tr.cost_input_rate,tr.cost_output_rate,tr.cost_basis)
    OR NOT ((tr.cause_kind='evidence_choice' AND tr.event_type IN ('released_no_consumption','consumption_unknown','cost_confirmed','evidence_disputed'))
      OR (tr.cause_kind='approval_consumed' AND tr.event_type='cost_corrected')
      OR (tr.cause_kind='recovery' AND tr.event_type IN ('expired_without_permit','recovered_unknown')))
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=j.root_id OR account_journal_id=j.id OR procurement_transition_id=tr.id OR reserved_event_id=tr.event_id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=j.root_id OR journal_id=j.id)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT * INTO e FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=j.source_id;
   IF NOT FOUND OR (e.account_id,e.attempt_id,e.type,e.event_key,e.held_delta,e.spent_delta,e.evidence_ref,e.pp_root_id,e.pp_root_step,e.pp_transition_id,e.pp_journal_id,e.pp_created_xid,e.pb_created_xid)
    IS DISTINCT FROM (tr.account_id,tr.attempt_id,tr.event_type,tr.event_key,tr.held_delta,tr.spent_delta,tr.evidence_ref,tr.root_id,tr.step_no,tr.id,tr.journal_id,tr.created_xid,tr.created_xid)
    OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps s WHERE (s.root_id,s.step_no,s.kind,s.source_id,s.created_xid) IS NOT DISTINCT FROM (j.root_id,j.step_no,j.step_kind,tr.id,j.created_xid))
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=j.root_id;
   IF NOT FOUND OR (rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid)
    IS DISTINCT FROM ('request'::text,tr.tenant_id,tr.request_id,tr.attempt_id,NULL::text,j.created_xid,{{MONI_APP_OID}}::oid)
    OR (tr.cause_kind='evidence_choice' AND rt.entry_kind<>'record_finalization')
    OR (tr.cause_kind='approval_consumed' AND rt.entry_kind<>'approve_cost')
    OR (tr.cause_kind='recovery' AND rt.entry_kind<>'recover_procurement')
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
  ELSIF j.source_kind='dispatch' AND j.kind='dispatch' THEN
   IF j.step_kind<>'attempt_delivery' OR j.command_source_id IS NOT NULL OR j.transition_id IS NOT NULL OR j.delivery_source_id IS NULL OR j.attempt_id IS DISTINCT FROM j.source_id
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=j.root_id OR account_journal_id=j.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=j.root_id OR journal_id=j.id)
    OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps s WHERE (s.root_id,s.step_no,s.kind,s.source_id,s.created_xid) IS NOT DISTINCT FROM (j.root_id,j.step_no,j.step_kind,j.delivery_source_id,j.created_xid))
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT * INTO u FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=j.attempt_id;
   IF NOT FOUND OR (u.pp_dispatch_source_id,u.pp_dispatch_root_id,u.pp_dispatch_root_step,u.pp_dispatch_xid,u.pp_dispatch_account_id,u.pp_dispatch_journal_id,u.pb_dispatch_checked_at)
    IS DISTINCT FROM (j.delivery_source_id,j.root_id,j.step_no,j.created_xid,j.account_id,j.id,j.pb_decision_at)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=j.root_id;
   IF NOT FOUND OR (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
    IS DISTINCT FROM ('dispatch'::text,'request'::text,u.tenant_id,u.request_id,u.id,NULL::text,j.created_xid,{{MONI_APP_OID}}::oid,1::bigint,'permitted'::text)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
  ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  prev:=j;
 END LOOP;
 -- Enumerate sources and root targets independently of the journal traversal,
 -- including not_found command roots whose journal is intentionally SQL NULL.
 FOR cs IN SELECT * FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE target_id=p_account LOOP
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(cs.operation_id);
 END LOOP;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.account_target_id=p_account
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources s WHERE (s.root_id,s.kind,s.target_id) IS NOT DISTINCT FROM (r.id,r.entry_kind,p_account)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands pc WHERE pc.target_id=p_account
   AND (pc.pp_source_id IS NOT NULL OR pc.pp_root_id IS NOT NULL OR pc.pp_root_step IS NOT NULL OR pc.pp_created_xid IS NOT NULL OR pc.pp_account_journal_id IS NOT NULL)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources s WHERE (s.id,s.operation_id,s.target_id,s.root_id,s.step_no,s.created_xid,s.journal_id) IS NOT DISTINCT FROM (pc.pp_source_id,pc.operation_id,p_account,pc.pp_root_id,pc.pp_root_step,pc.pp_created_xid,pc.pp_account_journal_id)))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF v_count=0 OR (h.ordinal,h.journal_id,h.updated_xid,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count)
  IS DISTINCT FROM (prev.ordinal,prev.id,prev.created_xid,prev.after_label,prev.after_provider,prev.after_state,prev.after_limit_amount,prev.after_configuration_revision,prev.after_held_amount,prev.after_spent_amount,prev.after_blocking_count)
  OR (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount) IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources s WHERE s.target_id=p_account AND s.journal_id IS NOT NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal x WHERE (x.id,x.account_id,x.command_source_id,x.source_kind,x.source_id) IS NOT DISTINCT FROM (s.journal_id,p_account,s.id,'command'::text,s.operation_id)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands pc WHERE pc.target_id=p_account AND pc.pp_account_journal_id IS NOT NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources s WHERE (s.id,s.operation_id,s.target_id,s.journal_id) IS NOT DISTINCT FROM (pc.pp_source_id,pc.operation_id,p_account,pc.pp_account_journal_id)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations p WHERE p.account_id=p_account
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins b JOIN {{MONI_SCHEMA}}.pp_account_journal x ON x.id=b.account_journal_id
    WHERE (b.attempt_id,b.management_mode,b.account_id,b.binding_id,b.created_xid,b.account_journal_id,b.account_journal_id,x.account_id,x.attempt_id,x.kind)
    IS NOT DISTINCT FROM (p.attempt_id,'managed'::text,p.account_id,p.binding_id,p.pp_created_xid,p.pp_origin_journal_id,p.pp_last_journal_id,p.account_id,p.attempt_id,'reserve'::text)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins b WHERE b.account_id=p_account
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations p WHERE (p.attempt_id,p.account_id,p.pp_origin_journal_id) IS NOT DISTINCT FROM (b.attempt_id,p_account,b.account_journal_id)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events pe WHERE pe.account_id=p_account
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions t JOIN {{MONI_SCHEMA}}.pp_account_journal x ON x.id=t.journal_id
    WHERE (t.id,t.event_id,t.account_id,t.attempt_id,t.event_type,x.account_id,x.attempt_id,x.source_kind,x.source_id)
    IS NOT DISTINCT FROM (pe.pp_transition_id,pe.id,p_account,pe.attempt_id,'reserved'::text,p_account,pe.attempt_id,'budget_event'::text,pe.id)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions t WHERE t.account_id=p_account
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins b WHERE (b.attempt_id,b.account_id,b.procurement_transition_id,b.reserved_event_id,b.account_journal_id) IS NOT DISTINCT FROM (t.attempt_id,p_account,t.id,t.event_id,t.journal_id)))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT coalesce(pg_catalog.sum(held_amount),0),coalesce(pg_catalog.sum(spent_amount),0),pg_catalog.count(*) FILTER(WHERE anomaly AND state IN ('unknown','disputed'))
 INTO v_held,v_spent,v_block FROM {{MONI_SCHEMA}}.procurement_reservations WHERE account_id=p_account;
 SELECT coalesce(pg_catalog.sum(held_delta),0),coalesce(pg_catalog.sum(spent_delta),0) INTO v_bheld,v_bspent FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE account_id=p_account;
 IF (a.held_amount,a.spent_amount,h.blocking_count) IS DISTINCT FROM (v_held,v_spent,v_block)
  OR (a.held_amount,a.spent_amount) IS DISTINCT FROM (v_bheld,v_bspent)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;

 FOR c IN SELECT * FROM {{MONI_SCHEMA}}.periodic_budget_controls WHERE scope='procurement' AND account_id=p_account LOOP
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations al WHERE al.control_id=c.id
    AND (al.subject_type<>'supplier_attempt' OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations p WHERE p.attempt_id=al.subject_id AND p.account_id=p_account)))
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT pg_catalog.count(*),pg_catalog.min(seq),pg_catalog.max(seq),coalesce(pg_catalog.sum(held_delta),0),coalesce(pg_catalog.sum(spent_delta),0)
  INTO v_events,v_min_seq,v_max_seq,v_bheld,v_bspent FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE control_id=c.id;
  SELECT id INTO v_last FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE control_id=c.id ORDER BY seq DESC LIMIT 1;
  SELECT coalesce(pg_catalog.sum(held_amount),0),coalesce(pg_catalog.sum(spent_amount),0) INTO v_allocation_held,v_allocation_spent FROM {{MONI_SCHEMA}}.periodic_budget_allocations WHERE control_id=c.id;
  IF c.event_seq IS DISTINCT FROM v_events OR (v_events>0 AND (v_min_seq<>1 OR v_max_seq<>v_events))
   OR c.last_event_id IS DISTINCT FROM v_last OR (c.held_amount,c.spent_amount) IS DISTINCT FROM (v_bheld,v_bspent)
   OR (c.held_amount,c.spent_amount) IS DISTINCT FROM (v_allocation_held,v_allocation_spent)
   OR c.effective_from IS DISTINCT FROM {{MONI_SCHEMA}}.pb_end(c.period_kind,{{MONI_SCHEMA}}.pb_start(c.period_kind,c.tracking_started_at))
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_events pe WHERE pe.control_id=c.id
    AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations al WHERE al.id=pe.allocation_id AND al.control_id=c.id AND al.last_event_id=pe.id))
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  FOR w IN SELECT * FROM {{MONI_SCHEMA}}.periodic_budget_windows WHERE control_id=c.id LOOP
   SELECT pg_catalog.count(*),coalesce(pg_catalog.sum(pe.held_delta),0),coalesce(pg_catalog.sum(pe.spent_delta),0)
   INTO v_events,v_bheld,v_bspent FROM {{MONI_SCHEMA}}.periodic_budget_events pe JOIN {{MONI_SCHEMA}}.periodic_budget_allocations al ON al.id=pe.allocation_id AND al.control_id=pe.control_id WHERE pe.control_id=c.id AND al.window_id=w.id;
   SELECT pe.id INTO v_last FROM {{MONI_SCHEMA}}.periodic_budget_events pe JOIN {{MONI_SCHEMA}}.periodic_budget_allocations al ON al.id=pe.allocation_id AND al.control_id=pe.control_id WHERE pe.control_id=c.id AND al.window_id=w.id ORDER BY pe.seq DESC LIMIT 1;
   IF (w.revision,w.held_amount,w.spent_amount,w.last_event_id) IS DISTINCT FROM (v_events,v_bheld,v_bspent,v_last)
    OR w.start_at IS DISTINCT FROM {{MONI_SCHEMA}}.pb_start(c.period_kind,w.start_at) OR w.end_at IS DISTINCT FROM {{MONI_SCHEMA}}.pb_end(c.period_kind,w.start_at)
    OR w.start_at>c.max_observed_window_start
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_events pe JOIN {{MONI_SCHEMA}}.periodic_budget_allocations al ON al.id=pe.allocation_id AND al.control_id=pe.control_id
      WHERE pe.control_id=c.id AND al.window_id=w.id AND pe.window_before_revision IS DISTINCT FROM (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.periodic_budget_events prior JOIN {{MONI_SCHEMA}}.periodic_budget_allocations pa ON pa.id=prior.allocation_id AND pa.control_id=prior.control_id WHERE prior.control_id=c.id AND pa.window_id=w.id AND prior.seq<pe.seq))
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END LOOP;
 END LOOP;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_validate_request(p_request text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE; u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE;
 b {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE; rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; v_account text;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(p_request);
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=p_request;
 FOR u IN SELECT * FROM {{MONI_SCHEMA}}.upstream_attempts WHERE request_id=p_request ORDER BY sequence,id LOOP
  SELECT * INTO b FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=u.id;
  IF NOT FOUND OR (b.tenant_id,b.request_id) IS DISTINCT FROM (o.tenant_id,p_request) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_attempt_leaf_v1(u.id);
 END LOOP;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins xb WHERE xb.request_id=p_request
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts xu WHERE (xu.id,xu.tenant_id,xu.request_id) IS NOT DISTINCT FROM (xb.attempt_id,o.tenant_id,p_request)))
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions t WHERE t.request_id=p_request AND t.cause_kind='attempt_birth'
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins xb WHERE (xb.attempt_id,xb.tenant_id,xb.request_id,xb.procurement_transition_id) IS NOT DISTINCT FROM (t.attempt_id,o.tenant_id,p_request,t.id)))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 FOR rt IN SELECT * FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=p_request LOOP
  IF rt.entry_kind='request_birth' THEN
   IF rt.id IS DISTINCT FROM o.root_id THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF rt.entry_kind='attempt_birth' THEN
   IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins xb WHERE (xb.attempt_id,xb.root_id,xb.tenant_id,xb.request_id) IS NOT DISTINCT FROM (rt.attempt_id,rt.id,o.tenant_id,p_request))
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF rt.entry_kind IN ('dispatch','record_finalization','propose_cost','approve_cost','unmanaged_correction','recover_procurement','reconcile') THEN
   IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(rt.id,true) OR rt.scope_kind<>'request' OR rt.tenant_id IS DISTINCT FROM o.tenant_id OR rt.account_target_id IS NOT NULL
    OR rt.created_xid='0'::xid8 OR rt.session_role_oid<>{{MONI_APP_OID}}::oid OR rt.step_count<=0
    OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=rt.id)<>rt.step_count
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps s WHERE s.root_id=rt.id AND (s.created_xid IS DISTINCT FROM rt.created_xid OR s.step_no<=0 OR s.step_no>rt.step_count OR s.kind IN ('request_birth','attempt_birth','account_command')))
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=rt.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=rt.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=rt.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=rt.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=rt.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE pp_origin_root_id=rt.id)
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=rt.id AND cause_kind='attempt_birth')
    OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=rt.id AND (source_kind='command' OR kind='reserve'))
    OR NOT ((rt.entry_kind='dispatch' AND rt.attempt_id IS NOT NULL AND rt.step_count=1 AND rt.semantic_result='permitted')
     OR (rt.entry_kind='record_finalization' AND rt.step_count>=3 AND rt.semantic_result IN ('normal','conflict'))
     OR (rt.entry_kind='propose_cost' AND rt.attempt_id IS NOT NULL AND rt.step_count=1 AND rt.semantic_result='proposed')
     OR (rt.entry_kind='approve_cost' AND rt.attempt_id IS NOT NULL AND rt.step_count=4 AND rt.semantic_result='approved')
     OR (rt.entry_kind='unmanaged_correction' AND rt.attempt_id IS NOT NULL AND rt.step_count=3 AND rt.semantic_result='corrected')
     OR (rt.entry_kind='recover_procurement' AND rt.attempt_id IS NOT NULL AND rt.step_count=3 AND rt.semantic_result IN ('released','unknown'))
     OR (rt.entry_kind='reconcile' AND rt.attempt_id IS NULL AND ((rt.semantic_result IN ('proposed','released') AND rt.step_count=1) OR (rt.semantic_result='approved' AND rt.step_count IN (1,4)))))
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
  ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END LOOP;
 FOR v_account IN SELECT DISTINCT account_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE request_id=p_request AND management_mode='managed' LOOP
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(v_account);
 END LOOP;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_validate_attempt(p_attempt text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE; o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO u FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=p_attempt;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- Classify the original attempt first, so a forged tracked child of a legacy
 -- request cannot be downgraded by the parent's untracked-domain result.
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_attempt_leaf_v1(p_attempt);
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(u.request_id);
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=p_attempt;
 IF o.management_mode='managed' THEN PERFORM {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(o.account_id); END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_validate_account(p_account text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(p_account);
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_validate_root(p_root text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
 o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=p_root;
 IF NOT FOUND OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(rt.id,true) OR rt.created_xid='0'::xid8 OR rt.session_role_oid<>{{MONI_APP_OID}}::oid OR rt.step_count<=0
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=p_root)<>rt.step_count
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=p_root AND (created_xid IS DISTINCT FROM rt.created_xid OR step_no<=0 OR step_no>rt.step_count))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF rt.entry_kind='request_birth' THEN
  IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=rt.request_id AND root_id=p_root)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(rt.request_id);
 ELSIF rt.entry_kind='attempt_birth' THEN
  IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=p_root)<>1
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=p_root;
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_attempt_leaf_v1(o.attempt_id);
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(o.request_id);
  IF o.management_mode='managed' THEN PERFORM {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(o.account_id); END IF;
 ELSIF rt.entry_kind IN ('account_create','account_configure') THEN
  IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=p_root)<>1
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=p_root;
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(s.operation_id);
  IF s.journal_id IS NOT NULL THEN PERFORM {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(s.target_id); END IF;
 ELSIF rt.entry_kind IN ('dispatch','record_finalization','propose_cost','approve_cost','unmanaged_correction','recover_procurement','reconcile') THEN
  IF rt.scope_kind<>'request' OR rt.tenant_id IS NULL OR rt.request_id IS NULL OR rt.account_target_id IS NOT NULL
   OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.request_records WHERE (id,tenant_id) IS NOT DISTINCT FROM (rt.request_id,rt.tenant_id))
   OR (rt.attempt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE (id,tenant_id,request_id) IS NOT DISTINCT FROM (rt.attempt_id,rt.tenant_id,rt.request_id)))
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=p_root)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=p_root)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=p_root)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=p_root)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=p_root)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE pp_origin_root_id=p_root)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=p_root AND cause_kind='attempt_birth')
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=p_root AND (source_kind='command' OR kind='reserve'))
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=p_root AND kind IN ('request_birth','attempt_birth','account_command'))
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=p_root)
   OR NOT ((rt.entry_kind='dispatch' AND rt.attempt_id IS NOT NULL AND rt.step_count=1 AND rt.semantic_result='permitted')
    OR (rt.entry_kind='record_finalization' AND rt.step_count>=3 AND rt.semantic_result IN ('normal','conflict'))
    OR (rt.entry_kind='propose_cost' AND rt.attempt_id IS NOT NULL AND rt.step_count=1 AND rt.semantic_result='proposed')
    OR (rt.entry_kind='approve_cost' AND rt.attempt_id IS NOT NULL AND rt.step_count=4 AND rt.semantic_result='approved')
    OR (rt.entry_kind='unmanaged_correction' AND rt.attempt_id IS NOT NULL AND rt.step_count=3 AND rt.semantic_result='corrected')
    OR (rt.entry_kind='recover_procurement' AND rt.attempt_id IS NOT NULL AND rt.step_count=3 AND rt.semantic_result IN ('released','unknown'))
    OR (rt.entry_kind='reconcile' AND rt.attempt_id IS NULL AND ((rt.semantic_result IN ('proposed','released') AND rt.step_count=1) OR (rt.semantic_result='approved' AND rt.step_count IN (1,4)))))
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_validate_original(p_tag text,p_key1 text,p_key2 text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 p {{MONI_SCHEMA}}.procurement_reservations%ROWTYPE; e {{MONI_SCHEMA}}.procurement_budget_events%ROWTYPE;
 c {{MONI_SCHEMA}}.procurement_commands%ROWTYPE; a {{MONI_SCHEMA}}.audit_events%ROWTYPE;
 s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF p_tag IS NULL OR p_key1 IS NULL THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF p_tag IN ('request','attempt','account','procurement','procurement_event','procurement_command','audit') THEN
  IF p_key2 IS NOT NULL THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 ELSIF p_tag IN ('meter','meter_conflict','finalization','evidence_identity','proposal','correction','reconciliation_case','settlement','customer_journal','journal_line','outbox','profile_block') THEN
  IF (p_tag IN ('evidence_identity','journal_line','profile_block') AND p_key2 IS NULL)
   OR (p_tag NOT IN ('evidence_identity','journal_line','profile_block') AND p_key2 IS NOT NULL)
  THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 ELSE RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 CASE p_tag
 WHEN 'request' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_request(p_key1);
 WHEN 'attempt' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_attempt(p_key1);
 WHEN 'account' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_account(p_key1);
 WHEN 'procurement' THEN
  SELECT * INTO p FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=p_key1;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF p.pp_created_xid IS NULL AND p.pp_origin_journal_id IS NULL AND p.pp_last_journal_id IS NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=p_key1)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=p_key1 AND (pp_origin_root_id IS NOT NULL OR pp_origin_root_step IS NOT NULL OR pp_origin_xid IS NOT NULL OR pp_quote_selected_at IS NOT NULL))
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE attempt_id=p_key1)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE attempt_id=p_key1)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=p.account_id AND (pp_version IS NOT NULL OR pp_created_xid IS NOT NULL OR pp_origin_journal_id IS NOT NULL))
  THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
  IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=p_key1 AND management_mode='managed' AND account_id=p.account_id)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_validate_attempt(p_key1);
 WHEN 'procurement_event' THEN
  SELECT * INTO e FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=p_key1;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF e.pp_root_id IS NULL AND e.pp_root_step IS NULL AND e.pp_transition_id IS NULL AND e.pp_journal_id IS NULL AND e.pp_created_xid IS NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE event_id=p_key1)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE source_kind='budget_event' AND source_id=p_key1)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE reserved_event_id=p_key1 OR attempt_id=e.attempt_id)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=e.attempt_id AND (pp_origin_root_id IS NOT NULL OR pp_origin_root_step IS NOT NULL OR pp_origin_xid IS NOT NULL OR pp_quote_selected_at IS NOT NULL))
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=e.account_id AND (pp_version IS NOT NULL OR pp_created_xid IS NOT NULL OR pp_origin_journal_id IS NOT NULL))
  THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
  IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions t WHERE (t.id,t.event_id,t.account_id,t.attempt_id,t.event_type,t.event_key,t.held_delta,t.spent_delta,t.evidence_ref,t.root_id,t.step_no,t.journal_id,t.created_xid)
   IS NOT DISTINCT FROM (e.pp_transition_id,e.id,e.account_id,e.attempt_id,e.type,e.event_key,e.held_delta,e.spent_delta,e.evidence_ref,e.pp_root_id,e.pp_root_step,e.pp_journal_id,e.pp_created_xid))
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF e.type='reserved' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_attempt(e.attempt_id);
  ELSE PERFORM {{MONI_SCHEMA}}.pp_validate_account(e.account_id); END IF;
 WHEN 'procurement_command' THEN
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(p_key1);
  SELECT * INTO c FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=p_key1;
  PERFORM {{MONI_SCHEMA}}.pp_validate_root(c.pp_root_id);
 WHEN 'audit' THEN
  SELECT * INTO a FROM {{MONI_SCHEMA}}.audit_events WHERE id=p_key1;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF a.pp_root_id IS NULL AND a.pp_root_step IS NULL AND a.pp_source_kind IS NULL AND a.pp_source_id IS NULL AND a.pp_source_role IS NULL AND a.pp_created_xid IS NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE audit_id=p_key1)
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=a.target AND (pp_source_id IS NOT NULL OR pp_root_id IS NOT NULL OR pp_root_step IS NOT NULL OR pp_created_xid IS NOT NULL OR pp_account_journal_id IS NOT NULL))
  THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
  IF a.pp_source_kind='account_command' AND a.pp_source_role='account_command' THEN
   SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=a.pp_source_id;
   IF NOT FOUND OR s.audit_id IS DISTINCT FROM a.id THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   PERFORM {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(s.operation_id);
   PERFORM {{MONI_SCHEMA}}.pp_validate_root(s.root_id);
  ELSIF a.pp_root_id IS NOT NULL AND a.pp_root_step IS NOT NULL AND a.pp_source_kind IS NOT NULL AND a.pp_source_id IS NOT NULL AND a.pp_source_role IS NOT NULL AND a.pp_created_xid IS NOT NULL
   AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE audit_id=p_key1 OR id=a.pp_source_id OR root_id=a.pp_root_id)
   AND EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps st WHERE (st.root_id,st.step_no,st.kind,st.source_id,st.created_xid) IS NOT DISTINCT FROM (a.pp_root_id,a.pp_root_step,a.pp_source_kind,a.pp_source_id,a.pp_created_xid))
   AND ((a.pp_source_kind='proposal_birth' AND a.pp_source_role='proposal' AND a.action='procurement.cost_proposed')
    OR (a.pp_source_kind='approval_consumed' AND a.pp_source_role='approval' AND a.action='procurement.cost_approved')
    OR (a.pp_source_kind='unmanaged_correction' AND a.pp_source_role='unmanaged_correction' AND a.action='cost.corrected')
    OR (a.pp_source_kind='reconciliation' AND a.pp_source_role='reconciliation' AND a.action IN ('reconciliation.proposed','reconciliation.approved','reconciliation.released'))
    OR (a.pp_source_kind='reconciliation' AND a.pp_source_role='settlement' AND a.action IN ('billing.settled','billing.released'))
    OR (a.pp_source_kind='reconciliation' AND a.pp_source_role='profile_block' AND a.action='billing.profile_blocked'))
  THEN
   -- The fixed root interface rejects all damaged A2 claims before PP906.
   PERFORM {{MONI_SCHEMA}}.pp_validate_root(a.pp_root_id);
   RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
  ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END CASE;
END $$;

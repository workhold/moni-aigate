-- Independent birth-exact-link-a2-v1. Never installed by ordinary serve.
-- All helpers are INVOKER; only the complete public root is DEFINER.
CREATE FUNCTION {{MONI_SCHEMA}}.pp_apply_supplier_pb(p_root text,p_attempt text,p_event text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 a {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE;
 p {{MONI_SCHEMA}}.procurement_reservations%ROWTYPE;
 o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE;
 e {{MONI_SCHEMA}}.procurement_budget_events%ROWTYPE;
 allocation {{MONI_SCHEMA}}.periodic_budget_allocations%ROWTYPE;
 v_control text; v_window text; v_allocation text;
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 SELECT * INTO a FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=p_attempt;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO p FROM {{MONI_SCHEMA}}.procurement_reservations x WHERE x.attempt_id=p_attempt;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins x WHERE x.attempt_id=p_attempt;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO e FROM {{MONI_SCHEMA}}.procurement_budget_events x WHERE x.id=p_event;
 IF NOT FOUND OR o.management_mode<>'managed' OR o.root_id IS DISTINCT FROM p_root
  OR (e.id,e.attempt_id,e.account_id,e.type,e.event_key,e.held_delta,e.spent_delta,e.evidence_ref,e.pp_root_id,e.pp_root_step,e.pp_transition_id,e.pp_journal_id,e.pp_created_xid)
   IS DISTINCT FROM (o.reserved_event_id,o.attempt_id,o.account_id,'reserved'::text,'reserve:'||o.attempt_id,p.upper_amount,0::numeric,''::text,o.root_id,2::bigint,o.procurement_transition_id,o.account_journal_id,o.created_xid)
  OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.procurement_budget_events x WHERE x.id=p_event)
  OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.procurement_reservations x WHERE x.attempt_id=p_attempt)
  OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=p_attempt)
  OR o.created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id()
  OR (p.account_id,p.binding_id,p.state,p.held_amount,p.spent_amount,p.anomaly,p.pb_admitted_at)
   IS DISTINCT FROM (o.account_id,o.binding_id,'prepared'::text,p.upper_amount,0::numeric,false,o.admitted_at)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots x WHERE x.id=p_root)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT c.id INTO v_control FROM {{MONI_SCHEMA}}.periodic_budget_controls c
  WHERE c.scope='procurement' AND c.account_id=o.account_id;
 IF p.pb_control IS DISTINCT FROM v_control THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF v_control IS NULL THEN
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations x WHERE x.subject_type='supplier_attempt' AND x.subject_id=p_attempt)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_events x WHERE x.source_kind='procurement_event' AND x.source_id=p_event)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN;
 END IF;
 -- This fixed birth helper cannot consume a legacy/import allocation or an
 -- already applied event. A committed replay never enters this helper.
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations x WHERE x.control_id=v_control AND x.subject_type='supplier_attempt' AND x.subject_id=p_attempt)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_events x WHERE x.control_id=v_control AND x.source_kind='procurement_event' AND x.source_id=p_event)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT {{MONI_SCHEMA}}.pb_observe(v_control,p.pb_admitted_at) INTO v_window;
 IF NOT {{MONI_SCHEMA}}.pb_uuid(v_window) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_allocation:=pg_catalog.gen_random_uuid()::text;
 -- Original 020 BEFORE derives upper/baseline/source_spent, including U=0.
 INSERT INTO {{MONI_SCHEMA}}.periodic_budget_allocations(id,control_id,subject_type,subject_id,origin,origin_source_id,window_id)
  VALUES(v_allocation,v_control,'supplier_attempt',p_attempt,'new',p_event,v_window);
 SELECT * INTO allocation FROM {{MONI_SCHEMA}}.periodic_budget_allocations x WHERE x.id=v_allocation;
 IF NOT FOUND OR (allocation.control_id,allocation.subject_type,allocation.subject_id,allocation.origin,allocation.origin_source_id,allocation.window_id,allocation.admitted_at,allocation.upper_amount,allocation.baseline,allocation.source_held,allocation.source_spent,allocation.held_amount,allocation.spent_amount,allocation.revision,allocation.last_event_id,allocation.state)
  IS DISTINCT FROM (v_control,'supplier_attempt'::text,p_attempt,'new'::text,p_event,v_window,p.pb_admitted_at,p.upper_amount,0::numeric,0::numeric,0::numeric,0::numeric,0::numeric,0::bigint,NULL::text,'held'::text)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 INSERT INTO {{MONI_SCHEMA}}.periodic_budget_events(id,control_id,allocation_id,source_kind,source_id)
  VALUES(pg_catalog.gen_random_uuid()::text,v_control,v_allocation,'procurement_event',p_event);
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_account_effect(p_root text,p_attempt text,p_source_kind text,p_source text)
RETURNS TABLE(event_id text,journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE;
 t {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE;
 p {{MONI_SCHEMA}}.procurement_reservations%ROWTYPE;
 a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE;
 v_block bigint; v_affected bigint;
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF p_root IS NULL OR p_attempt IS NULL OR p_source_kind IS NULL OR p_source IS NULL THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF p_source_kind NOT IN ('attempt_birth','evidence_choice','approval_consumed','recovery') OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_root,true)
  OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=p_attempt)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF p_source_kind<>'attempt_birth' THEN
  IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_source,true) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 END IF;
 IF p_attempt IS DISTINCT FROM p_source THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM x.attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins x WHERE x.attempt_id=p_source FOR SHARE;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins x WHERE x.attempt_id=p_source;
 IF NOT FOUND OR o.management_mode<>'managed' OR o.root_id IS DISTINCT FROM p_root OR o.created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id()
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots x WHERE x.id=p_root)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM x.id FROM {{MONI_SCHEMA}}.pp_procurement_transitions x WHERE x.id=o.procurement_transition_id FOR SHARE;
 SELECT * INTO t FROM {{MONI_SCHEMA}}.pp_procurement_transitions x WHERE x.id=o.procurement_transition_id;
 IF NOT FOUND OR (t.root_id,t.step_no,t.created_xid,t.tenant_id,t.request_id,t.attempt_id,t.account_id,t.binding_id,t.cause_kind,t.cause_source_id,t.event_id,t.journal_id,t.event_type,t.event_key,t.evidence_ref,t.held_delta,t.spent_delta,t.after_revision,t.after_state,t.after_held,t.after_spent,t.after_anomaly)
  IS DISTINCT FROM (p_root,2::bigint,o.created_xid,o.tenant_id,o.request_id,o.attempt_id,o.account_id,o.binding_id,'attempt_birth'::text,o.attempt_id,o.reserved_event_id,o.account_journal_id,'reserved'::text,'reserve:'||o.attempt_id,''::text,t.upper_amount,0::numeric,1::bigint,'prepared'::text,t.upper_amount,0::numeric,false)
  OR NOT (t.before_revision IS NULL AND t.before_state IS NULL AND t.before_held IS NULL AND t.before_spent IS NULL AND t.before_anomaly IS NULL)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM x.attempt_id FROM {{MONI_SCHEMA}}.procurement_reservations x WHERE x.attempt_id=p_source FOR SHARE;
 SELECT * INTO p FROM {{MONI_SCHEMA}}.procurement_reservations x WHERE x.attempt_id=p_source;
 IF NOT FOUND OR (p.account_id,p.binding_id,p.revision,p.state,p.held_amount,p.spent_amount,p.anomaly,p.input_bound,p.output_bound,p.upper_amount,p.pb_admitted_at,p.pp_created_xid,p.pp_origin_journal_id,p.pp_last_journal_id)
  IS DISTINCT FROM (o.account_id,o.binding_id,1::bigint,'prepared'::text,t.upper_amount,0::numeric,false,t.input_bound,t.output_bound,t.upper_amount,o.admitted_at,o.created_xid,o.account_journal_id,o.account_journal_id)
  OR (t.cost_version_id,t.cost_input_rate,t.cost_output_rate,t.cost_basis) IS DISTINCT FROM (o.cost_version_id,o.cost_input_rate,o.cost_output_rate,o.cost_basis)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events x WHERE x.attempt_id=p_source)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal x WHERE x.id=o.account_journal_id OR (x.attempt_id=p_source AND x.kind='reserve'))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- N and head U were acquired before p. No terminal validator is called
 -- during the explicit p/PE/account/PB temporary states.
 SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts x WHERE x.id=o.account_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads x WHERE x.account_id=o.account_id;
 IF NOT FOUND OR (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount)
  IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount)
  OR a.state<>'active' OR h.blocking_count<>0 OR a.held_amount+a.spent_amount+t.upper_amount>a.limit_amount
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO v_block FROM {{MONI_SCHEMA}}.procurement_reservations x
  WHERE x.account_id=o.account_id AND x.anomaly AND x.state IN ('unknown','disputed');
 IF v_block<>0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF h.ordinal=9223372036854775807 THEN RAISE EXCEPTION 'procurement_history_exhausted' USING ERRCODE='PP905'; END IF;
 INSERT INTO {{MONI_SCHEMA}}.procurement_budget_events(id,event_key,type,account_id,attempt_id,held_delta,spent_delta,evidence_ref,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid)
  VALUES(o.reserved_event_id,t.event_key,'reserved',o.account_id,o.attempt_id,t.upper_amount,0,'',p_root,2,t.id,o.account_journal_id,o.created_xid);
 UPDATE {{MONI_SCHEMA}}.procurement_accounts x SET held_amount=x.held_amount+t.upper_amount
  WHERE x.id=o.account_id RETURNING * INTO a;
 IF NOT FOUND OR (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount)
  IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount+t.upper_amount,h.spent_amount)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_apply_supplier_pb(p_root,p_source,o.reserved_event_id);
 INSERT INTO {{MONI_SCHEMA}}.pp_account_journal(id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,
  before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,
  after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,
  attempt_id,binding_id,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,
  input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,transition_id)
 VALUES(o.account_journal_id,o.account_id,h.ordinal+1,h.journal_id,p_root,2,'procurement_transition',o.created_xid,pg_catalog.clock_timestamp(),'reserve','budget_event',o.reserved_event_id,
  h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count,
  a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,v_block,
  o.attempt_id,o.binding_id,1,'prepared',t.upper_amount,0,false,
  t.input_bound,t.output_bound,t.upper_amount,o.cost_version_id,o.cost_input_rate,o.cost_output_rate,o.cost_basis,t.upper_amount,t.created_at,o.admitted_at,t.id);
 UPDATE {{MONI_SCHEMA}}.pp_account_heads x
  SET ordinal=h.ordinal+1,journal_id=o.account_journal_id,updated_xid=o.created_xid,
   label=a.label,provider=a.provider,state=a.state,limit_amount=a.limit_amount,configuration_revision=a.revision,
   held_amount=a.held_amount,spent_amount=a.spent_amount,blocking_count=v_block
  WHERE x.account_id=o.account_id AND x.ordinal=h.ordinal AND x.journal_id=h.journal_id;
 GET DIAGNOSTICS v_affected=ROW_COUNT;
 IF v_affected<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN QUERY SELECT o.reserved_event_id,o.account_journal_id;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_complete_attempt_birth(p_tenant text,p_request text,p_attempt text)
RETURNS TABLE(root_id text,management_mode text,account_id text,reserved_event_id text,account_journal_id text,admitted_at timestamptz)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 a {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE;
 r {{MONI_SCHEMA}}.request_records%ROWTYPE;
 v {{MONI_SCHEMA}}.reservations%ROWTYPE;
 binding {{MONI_SCHEMA}}.credential_procurement_bindings%ROWTYPE;
 account {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE;
 p {{MONI_SCHEMA}}.procurement_reservations%ROWTYPE;
 o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE;
 q record; dispatch_quote record; policy record; effect record;
 v_root text; v_node text; v_control text; v_window text; v_expires timestamptz; v_eligible boolean;
 v_input bigint; v_output bigint; v_upper numeric; v_active boolean; v_available boolean; v_managed boolean;
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 PERFORM x.id FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=p_attempt FOR SHARE;
 SELECT * INTO a FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=p_attempt;
 IF NOT FOUND OR (a.tenant_id,a.request_id) IS DISTINCT FROM (p_tenant,p_request)
  OR a.pp_origin_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR a.pp_origin_root_step IS DISTINCT FROM 1::bigint
  OR a.pp_origin_root_id IS NULL OR a.pp_quote_selected_at IS NULL
  OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=p_attempt)
  OR (a.delivery_state,a.cost_status,a.cost_amount) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric)
  OR a.pb_dispatch_checked_at IS NOT NULL
  OR NOT (a.pp_last_cost_mutation_id IS NULL AND a.pp_last_delivery_source_id IS NULL AND a.pp_dispatch_source_id IS NULL
   AND a.pp_dispatch_root_id IS NULL AND a.pp_dispatch_root_step IS NULL AND a.pp_dispatch_xid IS NULL AND a.pp_dispatch_account_id IS NULL AND a.pp_dispatch_journal_id IS NULL)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins x WHERE x.attempt_id=p_attempt)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads x WHERE x.attempt_id=p_attempt)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations x WHERE x.attempt_id=p_attempt)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events x WHERE x.attempt_id=p_attempt)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots x WHERE x.id=a.pp_origin_root_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_root:=a.pp_origin_root_id;
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(p_request);
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records x WHERE x.id=p_request AND x.tenant_id=p_tenant;
 SELECT * INTO v FROM {{MONI_SCHEMA}}.reservations x WHERE x.request_id=p_request AND x.tenant_id=p_tenant;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- The earlier public root owns channel S. Binding synchronization cannot
 -- pass that barrier; this new statement establishes the actual classification.
 SELECT * INTO binding FROM {{MONI_SCHEMA}}.credential_procurement_bindings x
  WHERE x.credential_id=a.credential_id AND x.channel_id=a.channel_id;
 v_managed:=FOUND;
 IF NOT v_managed THEN
  INSERT INTO {{MONI_SCHEMA}}.pp_attempt_origins(attempt_id,root_id,step_no,created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at)
  VALUES(a.id,v_root,1,a.pp_origin_xid,a.tenant_id,a.request_id,a.sequence,a.channel_id,a.channel_version_id,a.credential_id,a.upstream_model,a.route_decisions,a.cost_version_id,a.cost_input_rate,a.cost_output_rate,a.cost_basis,a.pp_quote_selected_at,a.created_at,a.pb_created_xid,'prepared','unknown',NULL,'unmanaged',NULL,NULL,NULL,NULL,NULL,NULL);
  INSERT INTO {{MONI_SCHEMA}}.pp_attempt_heads(attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid)
   VALUES(a.id,0,NULL,NULL,NULL,a.pp_origin_xid);
  INSERT INTO {{MONI_SCHEMA}}.pp_root_steps(root_id,step_no,kind,source_id,created_xid) VALUES(v_root,1,'attempt_birth',a.id,a.pp_origin_xid);
 ELSE
  PERFORM x.id FROM {{MONI_SCHEMA}}.procurement_accounts x WHERE x.id=binding.account_id FOR NO KEY UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM {{MONI_SCHEMA}}.pb_sync_unconfigured('procurement',binding.account_id);
  SELECT x.id INTO v_control FROM {{MONI_SCHEMA}}.periodic_budget_controls x
   WHERE x.scope='procurement' AND x.account_id=binding.account_id FOR NO KEY UPDATE;
  PERFORM x.account_id FROM {{MONI_SCHEMA}}.pp_account_heads x WHERE x.account_id=binding.account_id FOR UPDATE;
  -- No p exists yet, so this complete committed account prefix is valid.
  PERFORM {{MONI_SCHEMA}}.pp_validate_account(binding.account_id);
  SELECT * INTO account FROM {{MONI_SCHEMA}}.procurement_accounts x WHERE x.id=binding.account_id;
  SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads x WHERE x.account_id=binding.account_id;
  SELECT * INTO q FROM {{MONI_SCHEMA}}.pp_at_current_quote_v1(a.channel_id,a.upstream_model);
  IF q.cost_version_id IS NULL THEN RAISE EXCEPTION 'controlled procurement requires a complete quote' USING ERRCODE='PP413'; END IF;
  IF NOT {{MONI_SCHEMA}}.pp_at_quote_equal_v1(true,pg_catalog.convert_to(coalesce(a.cost_version_id,''),'UTF8'),a.cost_input_rate,a.cost_output_rate,pg_catalog.convert_to(a.cost_basis,'UTF8'),q.cost_version_id,q.input_rate,q.output_rate,q.basis)
   THEN RAISE EXCEPTION 'configured procurement quote changed before reservation' USING ERRCODE='PP412'; END IF;
  IF q.input_rate IS NULL OR q.output_rate IS NULL OR q.basis NOT IN ('contract','invoice') THEN RAISE EXCEPTION 'controlled procurement requires both known cost dimensions' USING ERRCODE='PP414'; END IF;
  SELECT x.input_upper_bound,x.output_limit,pg_catalog.clock_timestamp()<x.request_deadline INTO v_input,v_output,v_eligible
   FROM {{MONI_SCHEMA}}.request_records x WHERE x.id=p_request;
  IF NOT v_eligible THEN RAISE EXCEPTION 'request deadline expired while waiting for procurement' USING ERRCODE='PP409'; END IF;
  v_upper:=pg_catalog.ceil((v_input::numeric*q.input_rate::numeric+v_output::numeric*q.output_rate::numeric)/1000000);
  IF v_upper IS NULL OR v_upper<0 OR v_upper>9223372036854775807 THEN RAISE EXCEPTION 'procurement hard upper bound cannot be represented' USING ERRCODE='PP113'; END IF;
  SELECT x.state='active' AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations pr WHERE pr.account_id=x.id AND pr.anomaly AND pr.state IN ('unknown','disputed')),
   x.spent_amount+x.held_amount+v_upper<=x.limit_amount INTO v_active,v_available
   FROM {{MONI_SCHEMA}}.procurement_accounts x WHERE x.id=binding.account_id;
  IF NOT v_active THEN RAISE EXCEPTION 'supplier procurement account is paused' USING ERRCODE='PP114'; END IF;
  IF NOT v_available THEN RAISE EXCEPTION 'shared supplier procurement budget is exhausted' USING ERRCODE='PP115'; END IF;
  -- Original 020 stamp then identity then the final A2 BEFORE hook creates
  -- the complete origin/transition and derives NEW's journal pointers.
  INSERT INTO {{MONI_SCHEMA}}.procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,spent_amount,state)
   VALUES(a.id,binding.account_id,binding.id,v_input,v_output,v_upper,v_upper,0,'prepared') RETURNING * INTO p;
  SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins x WHERE x.attempt_id=a.id;
  IF NOT FOUND OR (o.root_id,o.management_mode,o.account_id,o.binding_id,o.admitted_at,o.created_xid)
   IS DISTINCT FROM (v_root,'managed'::text,binding.account_id,binding.id,p.pb_admitted_at,a.pp_origin_xid)
   OR (p.pb_control,p.pp_created_xid,p.pp_origin_journal_id,p.pp_last_journal_id)
    IS DISTINCT FROM (v_control,o.created_xid,o.account_journal_id,o.account_journal_id)
   THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  -- Same acceptance set as the original Go supplier read/newProof, including
  -- the no-control case. Ordinary TEXT parents are not a PB UUID certificate.
  IF NOT ({{MONI_SCHEMA}}.pb_uuid(a.id) AND {{MONI_SCHEMA}}.pb_uuid(p.account_id)
   AND {{MONI_SCHEMA}}.pb_uuid(a.request_id) AND {{MONI_SCHEMA}}.pb_uuid(a.tenant_id)
   AND {{MONI_SCHEMA}}.pb_uuid(a.credential_id) AND {{MONI_SCHEMA}}.pb_uuid(a.channel_id) AND {{MONI_SCHEMA}}.pb_uuid(p.binding_id))
   OR (p.state,p.held_amount,p.spent_amount,p.anomaly,p.upper_amount) IS DISTINCT FROM ('prepared'::text,v_upper,0::numeric,false,v_upper)
   OR NOT pg_catalog.isfinite(p.pb_admitted_at)
   OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.procurement_reservations x WHERE x.attempt_id=a.id)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_control IS NOT NULL THEN
   PERFORM {{MONI_SCHEMA}}.pb_gate(v_control,p.pb_admitted_at,v_upper::bigint);
   SELECT x.id INTO v_window FROM {{MONI_SCHEMA}}.periodic_budget_windows x WHERE x.control_id=v_control
    AND x.start_at={{MONI_SCHEMA}}.pb_start((SELECT c.period_kind FROM {{MONI_SCHEMA}}.periodic_budget_controls c WHERE c.id=v_control),p.pb_admitted_at);
   IF NOT FOUND OR NOT {{MONI_SCHEMA}}.pb_uuid(v_window) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
  SELECT x.expires_at INTO v_expires FROM {{MONI_SCHEMA}}.api_keys x WHERE x.tenant_id=p_tenant AND x.id=r.key_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_expires IS NOT NULL AND p.pb_admitted_at>=v_expires THEN RAISE EXCEPTION 'key is not active' USING ERRCODE='PP103'; END IF;
  IF p.pb_admitted_at>=v.deadline OR p.pb_admitted_at>=r.request_deadline THEN RAISE EXCEPTION 'request is no longer eligible for dispatch' USING ERRCODE='PP108'; END IF;
  SELECT coalesce(pol.cost_mode,'manual') AS mode,coalesce(pol.unknown_cost_policy,'allow') AS unknown_policy,b.max_input_cost_rate AS max_input,b.max_output_cost_rate AS max_output
   INTO policy FROM {{MONI_SCHEMA}}.upstream_attempts x JOIN {{MONI_SCHEMA}}.request_records rr ON rr.id=x.request_id
   LEFT JOIN {{MONI_SCHEMA}}.route_policy_versions pol ON pol.model_id=rr.model_id AND pol.revision=rr.route_revision
   LEFT JOIN {{MONI_SCHEMA}}.model_bindings b ON b.route_policy_version_id=pol.id AND b.channel_id=x.channel_id AND b.channel_version_id=x.channel_version_id AND b.upstream_model=x.upstream_model
   WHERE x.tenant_id=p_tenant AND x.id=a.id;
  SELECT cv.id,cv.input_rate,cv.output_rate,cv.basis INTO dispatch_quote FROM {{MONI_SCHEMA}}.cost_versions cv
   WHERE cv.channel_id=a.channel_id AND cv.upstream_model=a.upstream_model AND cv.effective_at<=p.pb_admitted_at
   ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1;
  IF dispatch_quote.input_rate IS NULL OR dispatch_quote.output_rate IS NULL OR coalesce(dispatch_quote.basis,'unknown') NOT IN ('contract','invoice')
   THEN RAISE EXCEPTION 'controlled procurement requires a complete quote' USING ERRCODE='PP413'; END IF;
  IF NOT {{MONI_SCHEMA}}.pp_at_quote_equal_v1(true,pg_catalog.convert_to(coalesce(a.cost_version_id,''),'UTF8'),a.cost_input_rate,a.cost_output_rate,pg_catalog.convert_to(a.cost_basis,'UTF8'),dispatch_quote.id,dispatch_quote.input_rate,dispatch_quote.output_rate,coalesce(dispatch_quote.basis,'unknown'))
   THEN RAISE EXCEPTION 'configured procurement quote changed before dispatch' USING ERRCODE='PP111'; END IF;
  IF dispatch_quote.id IS NULL OR dispatch_quote.id='' OR dispatch_quote.input_rate<0 OR dispatch_quote.output_rate<0 THEN
   IF policy.mode='cost_first' OR policy.unknown_policy='deny' OR policy.max_input IS NOT NULL OR policy.max_output IS NOT NULL
    THEN RAISE EXCEPTION 'route requires a known procurement quote' USING ERRCODE='PP415'; END IF;
  ELSIF (policy.max_input IS NOT NULL AND dispatch_quote.input_rate>policy.max_input)
   OR (policy.max_output IS NOT NULL AND dispatch_quote.output_rate>policy.max_output)
   THEN RAISE EXCEPTION 'configured procurement rate exceeds route limit' USING ERRCODE='PP112';
  END IF;
  SELECT * INTO effect FROM {{MONI_SCHEMA}}.pp_account_effect(v_root,a.id,'attempt_birth',a.id);
  IF (effect.event_id,effect.journal_id) IS DISTINCT FROM (o.reserved_event_id,o.account_journal_id) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
  INSERT INTO {{MONI_SCHEMA}}.pp_root_steps(root_id,step_no,kind,source_id,created_xid) VALUES(v_root,1,'attempt_birth',a.id,a.pp_origin_xid);
  INSERT INTO {{MONI_SCHEMA}}.pp_root_steps(root_id,step_no,kind,source_id,created_xid) VALUES(v_root,2,'procurement_transition',o.procurement_transition_id,a.pp_origin_xid);
  v_node:=pg_catalog.gen_random_uuid()::text;
  INSERT INTO {{MONI_SCHEMA}}.pp_attempt_nodes(id,attempt_id,ordinal,previous_node_id,root_id,step_no,step_kind,source_id,created_xid)
   VALUES(v_node,a.id,1,NULL,v_root,2,'procurement_transition',o.procurement_transition_id,a.pp_origin_xid);
  UPDATE {{MONI_SCHEMA}}.pp_attempt_heads x SET ordinal=1,last_node_id=v_node,member_count=0,member_tail_id=NULL,updated_xid=a.pp_origin_xid WHERE x.attempt_id=a.id;
 END IF;
 -- The only seal of this attempt is owned by this private closure.
 INSERT INTO {{MONI_SCHEMA}}.pp_operation_roots(id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result)
  VALUES(v_root,'attempt_birth','request',a.tenant_id,a.request_id,a.id,NULL,a.pp_origin_xid,{{MONI_APP_OID}}::oid,CASE WHEN v_managed THEN 2 ELSE 1 END,CASE WHEN v_managed THEN 'managed' ELSE 'unmanaged' END);
 PERFORM {{MONI_SCHEMA}}.pp_validate_root(v_root);
 RETURN QUERY SELECT v_root,CASE WHEN v_managed THEN 'managed'::text ELSE 'unmanaged'::text END,
  CASE WHEN v_managed THEN o.account_id ELSE NULL::text END,CASE WHEN v_managed THEN o.reserved_event_id ELSE NULL::text END,
  CASE WHEN v_managed THEN o.account_journal_id ELSE NULL::text END,CASE WHEN v_managed THEN o.admitted_at ELSE NULL::timestamptz END;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_record_attempt(p_tenant text,p_request text,p_sequence integer,p_channel text,p_channel_version text,p_credential text,p_upstream_model text,
 p_route_decisions jsonb,p_has_expected boolean,p_expected_version bytea,p_expected_input bigint,p_expected_output bigint,p_expected_basis bytea)
RETURNS TABLE(outcome text,root_id text,source_id text,attempt_id text,request_id text,sequence integer,delivery_state text,cost_status text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY DEFINER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 r {{MONI_SCHEMA}}.request_records%ROWTYPE; v {{MONI_SCHEMA}}.reservations%ROWTYPE;
 a {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE;
 key_policy record; q record; policy record; completion record;
 v_enabled boolean; v_quarantined boolean; v_isolated boolean; v_published boolean; v_member boolean; v_valid boolean; v_safe boolean;
 v_procurement boolean; v_mode_managed boolean; v_created boolean; v_route bytea; v_root text; v_attempt text; v_request_status text; v_latest text;
 v_cidr text; v_cidrs text[]; v_total bigint:=0; v_allowed boolean; v_address inet; v_wire bytea;
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_root_v1();
 IF p_sequence IS NULL OR p_sequence NOT BETWEEN 1 AND 2 OR p_upstream_model IS NULL OR pg_catalog.octet_length(p_upstream_model) NOT BETWEEN 1 AND 200
  THEN RAISE EXCEPTION 'invalid attempt input' USING ERRCODE='PP101'; END IF;
 v_route:={{MONI_SCHEMA}}.pp_at_route_json_v1(p_route_decisions,p_channel,p_channel_version);
 IF p_tenant IS NULL OR p_request IS NULL OR p_channel IS NULL OR p_channel_version IS NULL OR p_credential IS NULL
  OR NOT {{MONI_SCHEMA}}.pp_at_expected_shape_v1(p_has_expected,p_expected_version,p_expected_input,p_expected_output,p_expected_basis)
  THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records x WHERE x.tenant_id=p_tenant AND x.id=p_request;
 IF NOT FOUND THEN RAISE EXCEPTION 'request lookup returned no rows' USING ERRCODE='PP190'; END IF;
 PERFORM x.id FROM {{MONI_SCHEMA}}.tenants x WHERE x.id=p_tenant FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'tenant lookup returned no rows' USING ERRCODE='PP190'; END IF;
 PERFORM x.user_id FROM {{MONI_SCHEMA}}.memberships x WHERE x.tenant_id=p_tenant AND x.user_id=r.owner_id FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'membership lookup returned no rows' USING ERRCODE='PP190'; END IF;
 PERFORM x.id FROM {{MONI_SCHEMA}}.api_keys x WHERE x.tenant_id=p_tenant AND x.id=r.key_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'key lookup returned no rows' USING ERRCODE='PP190'; END IF;
 PERFORM x.id FROM {{MONI_SCHEMA}}.models x WHERE x.id=r.model_id FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'model lookup returned no rows' USING ERRCODE='PP190'; END IF;
 IF p_channel<>'' THEN
  PERFORM x.id FROM {{MONI_SCHEMA}}.channels x WHERE x.id=p_channel FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'channel lookup returned no rows' USING ERRCODE='PP190'; END IF;
  SELECT x.enabled,x.quarantined INTO v_enabled,v_quarantined FROM {{MONI_SCHEMA}}.channels x WHERE x.id=p_channel;
  IF NOT FOUND THEN RAISE EXCEPTION 'channel lookup returned no rows' USING ERRCODE='PP190'; END IF;
  IF NOT v_enabled OR v_quarantined THEN RAISE EXCEPTION 'channel is not available' USING ERRCODE='PP117'; END IF;
 END IF;
 SELECT * INTO v FROM {{MONI_SCHEMA}}.reservations x WHERE x.tenant_id=p_tenant AND x.request_id=p_request FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'reservation lookup returned no rows' USING ERRCODE='PP190'; END IF;
 -- Each post-wait query below is an independent VOLATILE RC statement.
 SELECT x.status,x.latest_finalization_id INTO v_request_status,v_latest FROM {{MONI_SCHEMA}}.request_records x WHERE x.tenant_id=p_tenant AND x.id=p_request;
 IF NOT FOUND THEN RAISE EXCEPTION 'request lookup returned no rows' USING ERRCODE='PP190'; END IF;
 SELECT k.allowed_client_cidrs,k.scopes,k.allowed_regions,k.revoked_at IS NOT NULL AS revoked,k.expires_at,cv.region,pg_catalog.clock_timestamp() AS checked_at
  INTO key_policy FROM {{MONI_SCHEMA}}.api_keys k LEFT JOIN {{MONI_SCHEMA}}.channel_versions cv ON cv.id=p_channel_version
  WHERE k.tenant_id=p_tenant AND k.id=r.key_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'key lookup returned no rows' USING ERRCODE='PP190'; END IF;
 v_cidrs:=key_policy.allowed_client_cidrs;v_allowed:=pg_catalog.cardinality(v_cidrs)=0;
 IF NOT v_allowed THEN
  IF pg_catalog.cardinality(v_cidrs)>32 OR r.client_ip IS NULL THEN RAISE EXCEPTION 'request source is not authorized' USING ERRCODE='PP102'; END IF;
  v_address:=pg_catalog.host(r.client_ip)::inet;v_wire:=pg_catalog.inet_send(v_address);
  IF pg_catalog.family(v_address)=6 AND pg_catalog.substring(v_wire,5,12)=pg_catalog.decode('00000000000000000000ffff','hex') THEN
   v_address:=(pg_catalog.get_byte(v_wire,16)::text||'.'||pg_catalog.get_byte(v_wire,17)::text||'.'||pg_catalog.get_byte(v_wire,18)::text||'.'||pg_catalog.get_byte(v_wire,19)::text)::inet;
  END IF;
  FOREACH v_cidr IN ARRAY v_cidrs LOOP
   v_total:=v_total+pg_catalog.octet_length(v_cidr);
   IF v_total>4096 OR {{MONI_SCHEMA}}.canonical_key_client_cidr(v_cidr) IS DISTINCT FROM v_cidr OR v_cidr IS NULL
    THEN RAISE EXCEPTION 'request source is not authorized' USING ERRCODE='PP102'; END IF;
   v_allowed:=v_allowed OR v_address <<= v_cidr::cidr;
  END LOOP;
 END IF;
 IF NOT v_allowed THEN RAISE EXCEPTION 'request source is not authorized' USING ERRCODE='PP102'; END IF;
 IF key_policy.revoked OR (key_policy.expires_at IS NOT NULL AND key_policy.checked_at>=key_policy.expires_at)
  THEN RAISE EXCEPTION 'key is not active' USING ERRCODE='PP103'; END IF;
 IF NOT (r.model_id=ANY(key_policy.scopes) OR '*'=ANY(key_policy.scopes)) THEN RAISE EXCEPTION 'key model scope is not authorized' USING ERRCODE='PP406'; END IF;
 SELECT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.route_binding_isolations x WHERE x.channel_version_id=p_channel_version AND x.upstream_model=p_upstream_model) INTO v_isolated;
 IF v_isolated THEN RAISE EXCEPTION 'published model binding is quarantined' USING ERRCODE='PP105'; END IF;
 IF v.status<>'reserved' THEN RAISE EXCEPTION 'request is not eligible for a new attempt' USING ERRCODE='PP106'; END IF;
 IF r.route_revision>0 THEN
  SELECT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.route_policy_versions rp JOIN {{MONI_SCHEMA}}.model_bindings b ON b.route_policy_version_id=rp.id
   WHERE rp.model_id=r.model_id AND rp.revision=r.route_revision AND b.channel_id=p_channel AND b.channel_version_id=p_channel_version AND b.upstream_model=p_upstream_model) INTO v_published;
  IF NOT v_published THEN RAISE EXCEPTION 'attempt is not part of the request route snapshot' USING ERRCODE='PP107'; END IF;
 END IF;
 SELECT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.channel_version_credentials x WHERE x.channel_version_id=p_channel_version AND x.channel_id=p_channel AND x.credential_id=p_credential) INTO v_member;
 IF NOT v_member OR key_policy.region IS NULL THEN RAISE EXCEPTION 'credential is not a member of the frozen channel version' USING ERRCODE='PP101'; END IF;
 IF NOT (key_policy.region=ANY(key_policy.allowed_regions) OR '*'=ANY(key_policy.allowed_regions)) THEN RAISE EXCEPTION 'key channel region is not authorized' USING ERRCODE='PP407'; END IF;
 SELECT pg_catalog.clock_timestamp()<r.request_deadline INTO v_valid;
 IF NOT v_valid THEN RAISE EXCEPTION 'request deadline expired' USING ERRCODE='PP408'; END IF;
 IF p_sequence=2 THEN
  IF v_latest IS NULL THEN RAISE EXCEPTION 'prior attempt has no terminal evidence' USING ERRCODE='PP410'; END IF;
  SELECT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.finalization_records f JOIN {{MONI_SCHEMA}}.metering_events e ON e.id=f.event_id JOIN {{MONI_SCHEMA}}.upstream_attempts x ON x.id=e.attempt_id
   WHERE x.request_id=p_request AND x.sequence=1 AND f.id=v_latest AND f.transport_end_reason IN ('not_sent','rejected_no_consumption') AND f.delivery_stage='none' AND NOT f.content_written) INTO v_safe;
  IF NOT v_safe THEN RAISE EXCEPTION 'prior attempt has no trustworthy non-consumption evidence' USING ERRCODE='PP109'; END IF;
 END IF;
 SELECT * INTO q FROM {{MONI_SCHEMA}}.pp_at_current_quote_v1(p_channel,p_upstream_model);
 SELECT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.credential_procurement_bindings x WHERE x.credential_id=p_credential) INTO v_procurement;
 IF v_procurement THEN
  IF q.input_rate IS NULL OR q.output_rate IS NULL OR q.basis NOT IN ('contract','invoice') THEN RAISE EXCEPTION 'controlled procurement requires a complete known quote' USING ERRCODE='PP110'; END IF;
  IF NOT {{MONI_SCHEMA}}.pp_at_quote_equal_v1(p_has_expected,p_expected_version,p_expected_input,p_expected_output,p_expected_basis,q.cost_version_id,q.input_rate,q.output_rate,q.basis)
   THEN RAISE EXCEPTION 'procurement quote changed before attempt' USING ERRCODE='PP411'; END IF;
 END IF;
 IF r.route_revision<>0 THEN
  SELECT rp.cost_mode AS mode,rp.unknown_cost_policy AS unknown_policy,b.max_input_cost_rate AS max_input,b.max_output_cost_rate AS max_output INTO policy
   FROM {{MONI_SCHEMA}}.route_policy_versions rp JOIN {{MONI_SCHEMA}}.model_bindings b ON b.route_policy_version_id=rp.id
   WHERE rp.model_id=r.model_id AND rp.revision=r.route_revision AND b.channel_id=p_channel AND b.channel_version_id=p_channel_version AND b.upstream_model=p_upstream_model;
  IF NOT FOUND THEN RAISE EXCEPTION 'route policy lookup returned no rows' USING ERRCODE='PP190'; END IF;
  v_mode_managed:=policy.mode='cost_first' OR policy.unknown_policy='deny' OR policy.max_input IS NOT NULL OR policy.max_output IS NOT NULL;
  IF v_mode_managed OR v_procurement THEN
   IF NOT {{MONI_SCHEMA}}.pp_at_quote_equal_v1(p_has_expected,p_expected_version,p_expected_input,p_expected_output,p_expected_basis,q.cost_version_id,q.input_rate,q.output_rate,q.basis)
    THEN RAISE EXCEPTION 'configured procurement quote changed before dispatch' USING ERRCODE='PP111'; END IF;
   IF q.cost_version_id IS NULL OR q.cost_version_id='' OR q.input_rate IS NULL OR q.output_rate IS NULL OR q.input_rate<0 OR q.output_rate<0 OR q.basis NOT IN ('contract','invoice') THEN
    IF v_mode_managed THEN RAISE EXCEPTION 'route requires a known procurement quote' USING ERRCODE='PP415'; END IF;
   ELSIF (policy.max_input IS NOT NULL AND q.input_rate>policy.max_input) OR (policy.max_output IS NOT NULL AND q.output_rate>policy.max_output)
    THEN RAISE EXCEPTION 'configured procurement rate exceeds route limit' USING ERRCODE='PP112';
   END IF;
  END IF;
 END IF;
 -- No early idempotency lookup is permitted: every original check above ran.
 v_attempt:=pg_catalog.gen_random_uuid()::text;v_root:=pg_catalog.gen_random_uuid()::text;
 INSERT INTO {{MONI_SCHEMA}}.upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,route_decisions,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at)
  VALUES(v_attempt,p_tenant,p_request,p_sequence,p_channel,p_channel_version,p_credential,p_upstream_model,q.cost_version_id,q.input_rate,q.output_rate,q.basis,pg_catalog.convert_from(v_route,'UTF8')::jsonb,v_root,1,pg_catalog.pg_current_xact_id(),q.quote_selected_at)
  ON CONFLICT ON CONSTRAINT upstream_attempts_request_id_sequence_key DO NOTHING RETURNING * INTO a;
 v_created:=FOUND;
 IF NOT v_created THEN
  SELECT * INTO a FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.tenant_id=p_tenant AND x.request_id=p_request AND x.sequence=p_sequence;
  IF NOT FOUND THEN RAISE EXCEPTION 'attempt lookup returned no rows' USING ERRCODE='PP190'; END IF;
  IF (a.channel_id,a.channel_version_id,a.credential_id,a.upstream_model) IS DISTINCT FROM (p_channel,p_channel_version,p_credential,p_upstream_model)
   THEN RAISE EXCEPTION 'attempt sequence already used' USING ERRCODE='PP116'; END IF;
  RETURN QUERY SELECT 'replay'::text,NULL::text,NULL::text,a.id,a.request_id,a.sequence,a.delivery_state,a.cost_status;
  RETURN;
 END IF;
 SELECT * INTO completion FROM {{MONI_SCHEMA}}.pp_complete_attempt_birth(p_tenant,p_request,a.id);
 IF completion.root_id IS DISTINCT FROM v_root OR completion.management_mode NOT IN ('managed','unmanaged')
  THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 RETURN QUERY SELECT 'applied'::text,v_root,a.id,a.id,a.request_id,a.sequence,a.delivery_state,a.cost_status;
END $$;

-- A2 invoker guards. Exact temporary birth phases; no generic xid exemption.
-- Generated columns are inspected only on physical rows, never BEFORE NEW.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_request_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE; h {{MONI_SCHEMA}}.pp_request_heads%ROWTYPE; v_status text;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME<>'request_records' OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},'request_records')) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP='INSERT' THEN
  IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('lock_timeout')<>'3s' OR pg_catalog.current_setting('statement_timeout')<>'5s' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
  IF NOT ((NEW.status,NEW.billing_status,NEW.latest_finalization_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)) OR NOT (NEW.pp_origin_root_id IS NULL AND NEW.pp_origin_root_step IS NULL AND NEW.pp_origin_xid IS NULL AND NEW.pp_last_decision_id IS NULL) OR NOT (NEW.pb_created_xid IS NULL AND NEW.pb_tenant_control IS NULL AND NEW.pb_key_control IS NULL AND NEW.pb_admitted_at IS NULL)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=NEW.id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=NEW.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP NOT IN ('UPDATE','DELETE') THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 -- Legacy stays legacy. A partial marker, source, or head never falls back.
 IF OLD.pp_origin_root_id IS NULL AND OLD.pp_origin_root_step IS NULL AND OLD.pp_origin_xid IS NULL AND OLD.pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=OLD.id)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=OLD.id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=OLD.id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=OLD.id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE request_id=OLD.id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE request_id=OLD.id) THEN
  IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  IF NOT (NEW.pp_origin_root_id IS NULL AND NEW.pp_origin_root_step IS NULL AND NEW.pp_origin_xid IS NULL AND NEW.pp_last_decision_id IS NULL) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('lock_timeout')<>'3s' OR pg_catalog.current_setting('statement_timeout')<>'5s' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.price_version_id,NEW.input_rate,NEW.output_rate,NEW.input_upper_bound,NEW.output_limit,NEW.profile_version,NEW.policy_version,NEW.request_hmac,NEW.route_revision,NEW.request_deadline,NEW.instance_id,NEW.streaming,NEW.client_ip,NEW.pricing_group_id,NEW.tenant_pricing_revision,NEW.retail_priced_at,NEW.created_at,NEW.pb_created_xid,NEW.pb_tenant_control,NEW.pb_key_control,NEW.pb_admitted_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.key_id,OLD.owner_id,OLD.model_id,OLD.price_version_id,OLD.input_rate,OLD.output_rate,OLD.input_upper_bound,OLD.output_limit,OLD.profile_version,OLD.policy_version,OLD.request_hmac,OLD.route_revision,OLD.request_deadline,OLD.instance_id,OLD.streaming,OLD.client_ip,OLD.pricing_group_id,OLD.tenant_pricing_revision,OLD.retail_priced_at,OLD.created_at,OLD.pb_created_xid,OLD.pb_tenant_control,OLD.pb_key_control,OLD.pb_admitted_at)
  OR (NEW.latest_finalization_id,NEW.pp_last_decision_id) IS DISTINCT FROM (OLD.latest_finalization_id,OLD.pp_last_decision_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN
  -- Parent SHARE and a separate post-wait statement, never one stale CTE.
  PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=OLD.id FOR SHARE;
  SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=OLD.id;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=OLD.id FOR SHARE;
  SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=OLD.id;
  IF NOT FOUND OR NOT (OLD.pp_origin_root_id IS NULL AND OLD.pp_origin_root_step IS NULL AND OLD.pp_origin_xid IS NULL AND OLD.pp_last_decision_id IS NULL) OR NOT ((OLD.status,OLD.billing_status,OLD.latest_finalization_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)) OR NOT ((OLD.id,OLD.tenant_id,OLD.key_id,OLD.owner_id,OLD.model_id,OLD.price_version_id,OLD.input_rate,OLD.output_rate,OLD.input_upper_bound,OLD.output_limit,OLD.profile_version,OLD.policy_version,OLD.request_hmac,OLD.route_revision,OLD.request_deadline,OLD.instance_id,OLD.streaming,OLD.client_ip::pg_catalog.text,OLD.pricing_group_id,OLD.tenant_pricing_revision,OLD.retail_priced_at,OLD.created_at,OLD.pb_created_xid,OLD.pb_tenant_control,OLD.pb_key_control,OLD.pb_admitted_at) IS NOT DISTINCT FROM (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)) OR o.created_xid<>pg_catalog.pg_current_xact_id()
   OR NOT ((NEW.pp_origin_root_id,NEW.pp_origin_root_step,NEW.pp_origin_xid) IS NOT DISTINCT FROM (o.root_id,o.step_no,o.created_xid)) OR (NEW.status,NEW.billing_status) IS DISTINCT FROM (OLD.status,OLD.billing_status)
   OR (h.ordinal,h.last_decision_id,h.updated_xid) IS DISTINCT FROM (0::bigint,NULL::text,o.created_xid)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=o.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.pp_origin_root_id,NEW.pp_origin_root_step,NEW.pp_origin_xid) IS DISTINCT FROM (OLD.pp_origin_root_id,OLD.pp_origin_root_step,OLD.pp_origin_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(OLD.id);
 SELECT status INTO v_status FROM {{MONI_SCHEMA}}.reservations WHERE request_id=OLD.id AND tenant_id=OLD.tenant_id;
 IF NOT FOUND OR NEW.billing_status IS DISTINCT FROM v_status THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF v_status='pending_reconciliation' THEN
  -- markPending creates the case after this UPDATE; final trigger checks it.
  IF NEW.status IS DISTINCT FROM OLD.status THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF v_status IN ('settled','released') THEN
  IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.settlements WHERE request_id=OLD.id AND tenant_id=OLD.tenant_id AND status=v_status)
   OR NEW.status IS DISTINCT FROM (CASE WHEN v_status='released' AND OLD.status IN ('accepted','dispatching') THEN 'failed' ELSE OLD.status END) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF v_status='reserved' THEN
  IF (NEW.status,NEW.billing_status) IS DISTINCT FROM (OLD.status,OLD.billing_status) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_shared_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 v_root text; v_count bigint; v_requests bigint; v_commands bigint; v_attempts bigint;
 o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE; ao {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE; cs {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; tr {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('pp_operation_roots','pp_root_steps') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF TG_TABLE_NAME='pp_operation_roots' THEN v_root:=NEW.id; ELSE v_root:=NEW.root_id; END IF;
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(v_root,true) OR NEW.created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=v_root) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO v_requests FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root;
 SELECT pg_catalog.count(*) INTO v_commands FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root;
 SELECT pg_catalog.count(*) INTO v_attempts FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root;
 IF v_requests+v_commands+v_attempts<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF TG_TABLE_NAME='pp_root_steps' THEN
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root AND (step_no=NEW.step_no OR (kind=NEW.kind AND source_id=NEW.source_id))) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_requests=1 THEN
 PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root FOR SHARE;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.step_no,NEW.kind,NEW.source_id,NEW.created_xid) IS DISTINCT FROM (1::bigint,'request_birth'::text,o.request_id,o.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF v_commands=1 THEN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root FOR SHARE;
 SELECT * INTO cs FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.step_no,NEW.kind,NEW.source_id,NEW.created_xid) IS DISTINCT FROM (1::bigint,'account_command'::text,cs.id,cs.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   IF NEW.step_no=1 THEN
 IF (NEW.kind,NEW.source_id,NEW.created_xid) IS DISTINCT FROM ('attempt_birth'::text,ao.attempt_id,ao.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   ELSE
 PERFORM id FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=ao.procurement_transition_id FOR SHARE;
 SELECT * INTO tr FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=ao.procurement_transition_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF ao.management_mode IS DISTINCT FROM 'managed' OR (NEW.step_no,NEW.kind,NEW.source_id,NEW.created_xid) IS DISTINCT FROM (2::bigint,'procurement_transition'::text,tr.id,ao.created_xid) OR (tr.root_id,tr.step_no,tr.attempt_id) IS DISTINCT FROM (ao.root_id,2::bigint,ao.attempt_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF v_requests=1 THEN
 PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root FOR SHARE;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF NEW.request_id IS DISTINCT FROM o.request_id OR NEW.tenant_id IS DISTINCT FROM o.tenant_id THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 DECLARE

 r {{MONI_SCHEMA}}.request_records%ROWTYPE; o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_request_heads%ROWTYPE; rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE;
 st {{MONI_SCHEMA}}.pp_root_steps%ROWTYPE;
 p_request text:=NEW.request_id;
 BEGIN
 PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.request_records WHERE id=NEW.request_id FOR SHARE;
 PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=NEW.request_id FOR SHARE;
 PERFORM root_id FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root FOR SHARE;
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
 rt:=NEW;
 IF (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
  IS DISTINCT FROM ('request_birth'::text,'request'::text,o.tenant_id,o.request_id,NULL::text,NULL::text,o.created_xid,{{MONI_APP_OID}}::oid,1::bigint,'born'::text)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id AND step_no=1;
 IF NOT FOUND OR (st.kind,st.source_id,st.created_xid,st.d_request_birth) IS DISTINCT FROM ('request_birth'::text,o.request_id,o.created_xid,o.request_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=o.root_id)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=p_request)<>1
  OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=p_request AND entry_kind='request_birth')<>0
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
 IF (r.status,r.billing_status,r.latest_finalization_id) IS DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;

 END;
 ELSIF v_commands=1 THEN
 DECLARE
 c {{MONI_SCHEMA}}.procurement_commands%ROWTYPE; s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; r {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; a {{MONI_SCHEMA}}.audit_events%ROWTYPE; v_time timestamptz; v_before jsonb; v_after jsonb;
 p_operation text:=(SELECT operation_id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root);
 BEGIN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root FOR SHARE;
 SELECT operation_id INTO p_operation FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root;
 PERFORM account_id FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_target_id FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=v_root FOR SHARE;
 PERFORM operation_id FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=v_root FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.audit_events WHERE pp_root_id=v_root FOR SHARE;
 PERFORM root_id FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root FOR SHARE;
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
 r:=NEW;
 IF (r.entry_kind,r.scope_kind,r.tenant_id,r.request_id,r.attempt_id,r.account_target_id,r.created_xid,r.session_role_oid,r.step_count,r.semantic_result) IS DISTINCT FROM (s.kind,'account_command'::text,NULL::text,NULL::text,NULL::text,s.target_id,s.created_xid,{{MONI_APP_OID}}::oid,1::bigint,s.command_status) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps st WHERE (st.root_id,st.step_no,st.kind,st.source_id,st.created_xid)=(s.root_id,1::bigint,'account_command'::text,s.id,s.created_xid)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
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
   IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins ab_account_origin WHERE (ab_account_origin.account_id,ab_account_origin.version,ab_account_origin.create_operation_id,ab_account_origin.command_source_id,ab_account_origin.root_id,ab_account_origin.step_no,ab_account_origin.step_kind,ab_account_origin.journal_id,ab_account_origin.created_xid) IS NOT DISTINCT FROM (s.target_id,1::smallint,s.operation_id,s.id,s.root_id,1::bigint,'account_command'::text,s.journal_id,s.created_xid))
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
 IF s.kind='account_create' AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins oo JOIN {{MONI_SCHEMA}}.procurement_accounts aa ON aa.id=oo.account_id WHERE (oo.account_id,oo.version,oo.create_operation_id,oo.command_source_id,oo.root_id,oo.step_no,oo.step_kind,oo.journal_id,oo.created_xid,oo.created_at) IS NOT DISTINCT FROM (s.target_id,1::smallint,s.operation_id,s.id,s.root_id,1::bigint,'account_command'::text,s.journal_id,s.created_xid,aa.created_at) AND (aa.pp_version,aa.pp_created_xid,aa.pp_origin_journal_id) IS NOT DISTINCT FROM (oo.version,oo.created_xid,oo.journal_id)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF s.journal_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_heads h JOIN {{MONI_SCHEMA}}.procurement_accounts pa ON pa.id=h.account_id WHERE (h.account_id,h.ordinal,h.journal_id,h.updated_xid,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count) IS NOT DISTINCT FROM (j.account_id,j.ordinal,j.id,j.created_xid,j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count) AND (pa.id,pa.label,pa.provider,pa.state,pa.limit_amount,pa.revision,pa.held_amount,pa.spent_amount) IS NOT DISTINCT FROM (h.account_id,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;

 END;
 ELSE
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF NEW.attempt_id IS DISTINCT FROM ao.attempt_id OR NEW.request_id IS DISTINCT FROM ao.request_id OR NEW.tenant_id IS DISTINCT FROM ao.tenant_id THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
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
 p_attempt text:=NEW.attempt_id;
 BEGIN
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=v_root FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=v_root FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE root_id=v_root FOR SHARE;
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=NEW.attempt_id FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE pp_root_id=v_root FOR SHARE;
 PERFORM root_id FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root FOR SHARE;
 PERFORM account_id FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id IN (SELECT account_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=v_root) FOR SHARE;
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
 rt:=NEW;
 IF (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
  IS DISTINCT FROM ('attempt_birth'::text,'request'::text,o.tenant_id,o.request_id,o.attempt_id,NULL::text,o.created_xid,{{MONI_APP_OID}}::oid,(CASE WHEN v_managed THEN 2 ELSE 1 END)::bigint,CASE WHEN v_managed THEN 'managed'::text ELSE 'unmanaged'::text END)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id AND step_no=1;
 IF NOT FOUND OR (st.kind,st.source_id,st.created_xid,st.d_attempt_birth) IS DISTINCT FROM ('attempt_birth'::text,o.attempt_id,o.created_xid,o.attempt_id)
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=p_attempt;
 IF NOT FOUND OR h.updated_xid IS DISTINCT FROM o.created_xid
  OR (NOT v_managed AND (h.ordinal,h.last_node_id,h.member_count,h.member_tail_id) IS DISTINCT FROM (0::bigint,NULL::text,NULL::bigint,NULL::text))
  OR (v_managed AND (h.ordinal,h.member_count,h.member_tail_id) IS DISTINCT FROM (1::bigint,0::bigint,NULL::text))
 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE attempt_id=p_attempt)<>0
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
  RETURN NEW;
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

 IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_heads hh JOIN {{MONI_SCHEMA}}.procurement_accounts aa ON aa.id=hh.account_id WHERE (hh.account_id,hh.ordinal,hh.journal_id,hh.updated_xid,hh.label,hh.provider,hh.state,hh.limit_amount,hh.configuration_revision,hh.held_amount,hh.spent_amount,hh.blocking_count) IS NOT DISTINCT FROM (j.account_id,j.ordinal,j.id,j.created_xid,j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count) AND (aa.id,aa.label,aa.provider,aa.state,aa.limit_amount,aa.revision,aa.held_amount,aa.spent_amount) IS NOT DISTINCT FROM (hh.account_id,hh.label,hh.provider,hh.state,hh.limit_amount,hh.configuration_revision,hh.held_amount,hh.spent_amount)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- A later Commander may import a pre-control tracked reservation. Preserve
 -- that original supported path; a birth marker can only name origin=new.
 SELECT * INTO c FROM {{MONI_SCHEMA}}.periodic_budget_controls WHERE scope='procurement' AND account_id=p.account_id;
 IF NOT FOUND THEN
  IF p.pb_control IS NOT NULL
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=p_attempt)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.periodic_budget_events WHERE source_kind='procurement_event' AND source_id=e.id)
  THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RETURN NEW;
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

 END;
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_source_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 r {{MONI_SCHEMA}}.request_records%ROWTYPE; o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE;
 a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE; h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE;
 prev {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; cs {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
 u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE; ao {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE; ah {{MONI_SCHEMA}}.pp_attempt_heads%ROWTYPE;
 n {{MONI_SCHEMA}}.pp_attempt_nodes%ROWTYPE; tr {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE;
 p {{MONI_SCHEMA}}.procurement_reservations%ROWTYPE; e {{MONI_SCHEMA}}.procurement_budget_events%ROWTYPE;
 v_root text; v_xid xid8:=pg_catalog.pg_current_xact_id(); v_count bigint; v_block bigint; v_role text; v_status text; v_upper numeric;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('pp_request_origins','pp_request_heads','pp_attempt_origins','pp_attempt_heads','pp_attempt_nodes','pp_account_command_sources','pp_account_origins','pp_account_heads','pp_account_journal','pp_procurement_transitions') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT','UPDATE') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF TG_OP='UPDATE' AND TG_TABLE_NAME NOT IN ('pp_attempt_heads','pp_account_heads') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_TABLE_NAME IN ('pp_request_origins','pp_request_heads') THEN
 PERFORM id FROM {{MONI_SCHEMA}}.request_records WHERE id=NEW.request_id FOR SHARE;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records WHERE id=NEW.request_id;
 IF NOT FOUND OR NOT (r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL) OR NOT ((r.status,r.billing_status,r.latest_finalization_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)) OR r.pb_created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF TG_TABLE_NAME='pp_request_origins' THEN
  IF NOT ((r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::pg_catalog.text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at) IS NOT DISTINCT FROM (NEW.request_id,NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.price_version_id,NEW.input_rate,NEW.output_rate,NEW.input_upper_bound,NEW.output_limit,NEW.profile_version,NEW.policy_version,NEW.request_hmac,NEW.route_revision,NEW.request_deadline,NEW.instance_id,NEW.streaming,NEW.client_ip,NEW.pricing_group_id,NEW.tenant_pricing_revision,NEW.retail_priced_at,NEW.request_created_at,NEW.pb_created_xid,NEW.pb_tenant_control,NEW.pb_key_control,NEW.pb_admitted_at)) OR NOT ((NEW.initial_status,NEW.initial_billing_status,NEW.initial_latest_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)) OR NEW.step_no<>1 OR NEW.created_xid<>pg_catalog.pg_current_xact_id()
   OR NOT (NEW.root_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND NEW.root_id<>'00000000-0000-0000-0000-000000000000') OR NOT (pg_catalog.isfinite(NEW.request_deadline) AND pg_catalog.isfinite(NEW.request_created_at) AND pg_catalog.isfinite(NEW.retail_priced_at) AND pg_catalog.isfinite(NEW.pb_admitted_at))
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=NEW.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=NEW.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=NEW.root_id OR request_id=NEW.request_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=NEW.request_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME='pp_request_heads' THEN
  PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=NEW.request_id FOR SHARE;
  SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=NEW.request_id;
  IF NOT FOUND OR NOT ((r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::pg_catalog.text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at) IS NOT DISTINCT FROM (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)) OR (NEW.ordinal,NEW.last_decision_id,NEW.updated_xid) IS DISTINCT FROM (0::bigint,NULL::text,o.created_xid)
   OR o.created_xid<>pg_catalog.pg_current_xact_id() OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=o.root_id)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 RETURN NEW;
 END IF;
 IF TG_TABLE_NAME='pp_account_heads' THEN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id FOR SHARE;
 SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM account_id FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_id FOR SHARE;
 IF (NEW.account_id,NEW.ordinal,NEW.updated_xid,NEW.label,NEW.provider,NEW.state,NEW.limit_amount,NEW.configuration_revision,NEW.held_amount,NEW.spent_amount,NEW.blocking_count) IS DISTINCT FROM (j.account_id,j.ordinal,j.created_xid,j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count) OR j.created_xid IS DISTINCT FROM v_xid THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF TG_OP='INSERT' THEN
   IF j.kind<>'create' OR j.ordinal<>1 OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_id AND journal_id=j.id AND root_id=j.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   IF NEW.account_id IS DISTINCT FROM OLD.account_id OR OLD.ordinal=9223372036854775807 OR NEW.ordinal::numeric IS DISTINCT FROM OLD.ordinal::numeric+1 OR (j.previous_id,j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (OLD.journal_id,OLD.label,OLD.provider,OLD.state,OLD.limit_amount,OLD.configuration_revision,OLD.held_amount,OLD.spent_amount,OLD.blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
  v_root:=j.root_id;
 ELSIF TG_TABLE_NAME='pp_attempt_heads' THEN
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF NEW.updated_xid IS DISTINCT FROM ao.created_xid OR ao.created_xid IS DISTINCT FROM v_xid THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF TG_OP='INSERT' THEN
   IF (NEW.ordinal,NEW.last_node_id,NEW.member_count,NEW.member_tail_id) IS DISTINCT FROM (0::bigint,NULL::text,NULL::bigint,NULL::text) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=NEW.attempt_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
 PERFORM id FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE id=NEW.last_node_id FOR SHARE;
 SELECT * INTO n FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE id=NEW.last_node_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF ao.management_mode<>'managed' OR (OLD.attempt_id,OLD.ordinal,OLD.last_node_id,OLD.member_count,OLD.member_tail_id,OLD.updated_xid) IS DISTINCT FROM (ao.attempt_id,0::bigint,NULL::text,NULL::bigint,NULL::text,ao.created_xid) OR (NEW.attempt_id,NEW.ordinal,NEW.member_count,NEW.member_tail_id) IS DISTINCT FROM (ao.attempt_id,1::bigint,0::bigint,NULL::text) OR (n.attempt_id,n.ordinal,n.previous_node_id,n.root_id,n.step_no,n.step_kind,n.source_id,n.created_xid) IS DISTINCT FROM (ao.attempt_id,1::bigint,NULL::text,ao.root_id,2::bigint,'procurement_transition'::text,ao.procurement_transition_id,ao.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
  v_root:=ao.root_id;
 ELSE
  v_root:=NEW.root_id;
 IF NEW.created_xid IS DISTINCT FROM v_xid THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(v_root,true) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=v_root) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF TG_TABLE_NAME='pp_account_command_sources' THEN
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.audit_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.operation_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.actor_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.target_id,NEW.kind='account_create') OR NEW.step_no<>1 OR NEW.kind NOT IN ('account_create','account_configure') OR NEW.actor_role<>'admin' OR NEW.actor_status<>'active' OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(NEW.input_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(NEW.input_provider) OR NEW.input_state NOT IN ('active','paused') OR NEW.input_limit_amount<0 OR NEW.input_digest IS DISTINCT FROM {{MONI_SCHEMA}}.pp_ac_command_digest_v1(NEW.kind,NEW.operation_id,NEW.input_target_id,NEW.input_expected_revision,NEW.input_label,NEW.input_provider,NEW.input_state,NEW.input_limit_amount) OR NOT pg_catalog.isfinite(NEW.created_at) OR NOT pg_catalog.isfinite(NEW.command_created_at) OR (NEW.journal_id IS NOT NULL AND NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.journal_id,true)) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=NEW.operation_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE id=NEW.audit_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id OR root_id=v_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root OR operation_id=NEW.operation_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM id FROM {{MONI_SCHEMA}}.users WHERE id=NEW.actor_id FOR SHARE;
  SELECT platform_role,status INTO v_role,v_status FROM {{MONI_SCHEMA}}.users WHERE id=NEW.actor_id;
 IF NOT FOUND OR (v_role,v_status) IS DISTINCT FROM ('admin'::text,'active'::text) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.target_id;
  IF NEW.kind='account_create' THEN
   IF FOUND OR NEW.input_target_id<>'' OR NEW.input_expected_revision IS NOT NULL OR NEW.command_status<>'applied' OR NEW.command_reason<>'' OR NEW.journal_id IS NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   IF NEW.input_target_id<>NEW.target_id OR NEW.input_expected_revision IS NULL OR NEW.input_expected_revision<=0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   IF NOT FOUND THEN
    IF NEW.command_status<>'rejected' OR NEW.command_reason<>'not_found' OR NEW.journal_id IS NOT NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   ELSE
    PERFORM {{MONI_SCHEMA}}.pp_ab_validate_account_leaf_v1(NEW.target_id);
    IF NEW.journal_id IS NULL OR (a.revision=NEW.input_expected_revision AND (NEW.command_status<>'applied' OR NEW.command_reason<>'')) OR (a.revision<>NEW.input_expected_revision AND (NEW.command_status<>'rejected' OR NEW.command_reason<>'revision_conflict')) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   END IF;
  END IF;
 ELSIF TG_TABLE_NAME='pp_account_origins' THEN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.command_source_id FOR SHARE;
 SELECT * INTO cs FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.command_source_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id FOR SHARE;
 SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.account_id;
 IF NOT FOUND OR (NEW.version,NEW.create_operation_id,NEW.root_id,NEW.step_no,NEW.created_xid,NEW.created_at) IS DISTINCT FROM (1::smallint,cs.operation_id,cs.root_id,1::bigint,cs.created_xid,a.created_at) OR cs.kind<>'account_create' OR (cs.target_id,cs.journal_id) IS DISTINCT FROM (NEW.account_id,j.id) OR (j.account_id,j.ordinal,j.kind,j.root_id,j.command_source_id,j.created_xid) IS DISTINCT FROM (NEW.account_id,1::bigint,'create'::text,NEW.root_id,cs.id,NEW.created_xid) OR (a.pp_version,a.pp_created_xid,a.pp_origin_journal_id) IS DISTINCT FROM (NEW.version,NEW.created_xid,NEW.journal_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME='pp_attempt_origins' THEN
 PERFORM id FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id FOR SHARE;
 SELECT * INTO u FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (u.id,u.tenant_id,u.request_id,u.sequence,u.channel_id,u.channel_version_id,u.credential_id,u.upstream_model,u.route_decisions,u.cost_version_id,u.cost_input_rate,u.cost_output_rate,u.cost_basis,u.pp_quote_selected_at,u.created_at,u.pb_created_xid,u.pp_origin_root_id,u.pp_origin_root_step,u.pp_origin_xid) IS DISTINCT FROM (NEW.attempt_id,NEW.tenant_id,NEW.request_id,NEW.sequence,NEW.channel_id,NEW.channel_version_id,NEW.credential_id,NEW.upstream_model,NEW.route_decisions,NEW.cost_version_id,NEW.cost_input_rate,NEW.cost_output_rate,NEW.cost_basis,NEW.quote_selected_at,NEW.attempt_created_at,NEW.pb_created_xid,NEW.root_id,NEW.step_no,NEW.created_xid) OR (NEW.initial_delivery_state,NEW.initial_cost_status,NEW.initial_cost_amount) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric) OR (u.delivery_state,u.cost_status,u.cost_amount,u.pb_dispatch_checked_at) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric,NULL::timestamptz) OR NEW.step_no<>1 OR NEW.pb_created_xid IS DISTINCT FROM v_xid THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.attempt_id,true) OR NOT pg_catalog.isfinite(NEW.quote_selected_at) OR NOT pg_catalog.isfinite(NEW.attempt_created_at) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id OR root_id=v_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=NEW.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=NEW.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=NEW.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE attempt_id=NEW.attempt_id OR root_id=v_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE attempt_id=NEW.attempt_id OR root_id=v_root) OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=NEW.attempt_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(NEW.request_id);
  IF NEW.management_mode='unmanaged' THEN
   IF NOT (NEW.account_id IS NULL AND NEW.binding_id IS NULL AND NEW.procurement_transition_id IS NULL AND NEW.reserved_event_id IS NULL AND NEW.account_journal_id IS NULL AND NEW.admitted_at IS NULL) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.credential_procurement_bindings WHERE credential_id=NEW.credential_id AND channel_id=NEW.channel_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF NEW.management_mode='managed' THEN
   IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.procurement_transition_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.reserved_event_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.account_journal_id,true) OR NOT pg_catalog.isfinite(NEW.admitted_at) OR NEW.cost_version_id IS NULL OR NEW.cost_input_rate IS NULL OR NEW.cost_output_rate IS NULL OR NEW.cost_basis NOT IN ('contract','invoice') OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.credential_procurement_bindings WHERE (id,credential_id,channel_id,account_id) IS NOT DISTINCT FROM (NEW.binding_id,NEW.credential_id,NEW.channel_id,NEW.account_id)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
  END IF;
 ELSIF TG_TABLE_NAME='pp_procurement_transitions' THEN
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=ao.request_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_upper:=pg_catalog.ceil((o.input_upper_bound::numeric*ao.cost_input_rate::numeric+o.output_limit::numeric*ao.cost_output_rate::numeric)/1000000);
 IF ao.management_mode<>'managed' OR (NEW.id,NEW.root_id,NEW.step_no,NEW.created_xid,NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.account_id,NEW.binding_id,NEW.cause_kind,NEW.cause_source_id,NEW.event_id,NEW.journal_id,NEW.event_type,NEW.event_key,NEW.evidence_ref,NEW.held_delta,NEW.spent_delta) IS DISTINCT FROM (ao.procurement_transition_id,ao.root_id,2::bigint,ao.created_xid,ao.tenant_id,ao.request_id,ao.attempt_id,ao.account_id,ao.binding_id,'attempt_birth'::text,ao.attempt_id,ao.reserved_event_id,ao.account_journal_id,'reserved'::text,'reserve:'||ao.attempt_id,''::text,v_upper,0::numeric) OR (NEW.before_revision,NEW.before_state,NEW.before_held,NEW.before_spent,NEW.before_anomaly) IS DISTINCT FROM (NULL::bigint,NULL::text,NULL::numeric,NULL::numeric,NULL::boolean) OR (NEW.after_revision,NEW.after_state,NEW.after_held,NEW.after_spent,NEW.after_anomaly,NEW.input_bound,NEW.output_bound,NEW.upper_amount,NEW.cost_version_id,NEW.cost_input_rate,NEW.cost_output_rate,NEW.cost_basis) IS DISTINCT FROM (1::bigint,'prepared'::text,v_upper,0::numeric,false,o.input_upper_bound,o.output_limit,v_upper::bigint,ao.cost_version_id,ao.cost_input_rate,ao.cost_output_rate,ao.cost_basis) OR NOT pg_catalog.isfinite(NEW.created_at) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=ao.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=ao.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=ao.account_journal_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=v_root OR attempt_id=ao.attempt_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME='pp_attempt_nodes' THEN
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=NEW.source_id FOR SHARE;
 SELECT * INTO tr FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=NEW.source_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM root_id FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=NEW.root_id AND step_no=NEW.step_no FOR SHARE;
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.id,true) OR ao.management_mode<>'managed' OR (NEW.root_id,NEW.step_no,NEW.step_kind,NEW.source_id,NEW.created_xid,NEW.ordinal,NEW.previous_node_id) IS DISTINCT FROM (ao.root_id,2::bigint,'procurement_transition'::text,ao.procurement_transition_id,ao.created_xid,1::bigint,NULL::text) OR (tr.root_id,tr.attempt_id,tr.created_xid) IS DISTINCT FROM (ao.root_id,ao.attempt_id,ao.created_xid) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE (root_id,step_no,kind,source_id,created_xid) IS NOT DISTINCT FROM (NEW.root_id,NEW.step_no,NEW.step_kind,NEW.source_id,NEW.created_xid)) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE (attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid) IS NOT DISTINCT FROM (ao.attempt_id,0::bigint,NULL::text,NULL::bigint,NULL::text,ao.created_xid)) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE attempt_id=NEW.attempt_id OR root_id=v_root) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME='pp_account_journal' THEN
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.id,true) OR NEW.ordinal<1 OR NOT pg_catalog.isfinite(NEW.created_at) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=v_root OR (account_id=NEW.account_id AND ordinal=NEW.ordinal) OR (source_kind=NEW.source_kind AND source_id=NEW.source_id)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.account_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.after_label,NEW.after_provider,NEW.after_state,NEW.after_limit_amount,NEW.after_configuration_revision,NEW.after_held_amount,NEW.after_spent_amount) IS DISTINCT FROM (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF NEW.ordinal=1 THEN
   IF NEW.previous_id IS NOT NULL OR (NEW.before_label,NEW.before_provider,NEW.before_state,NEW.before_limit_amount,NEW.before_configuration_revision,NEW.before_held_amount,NEW.before_spent_amount,NEW.before_blocking_count) IS DISTINCT FROM (NULL::text,NULL::text,NULL::text,NULL::bigint,NULL::bigint,NULL::numeric,NULL::numeric,NULL::bigint) OR NEW.kind<>'create' OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=NEW.account_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
 PERFORM account_id FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_id FOR SHARE;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.previous_id FOR SHARE;
 SELECT * INTO prev FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.previous_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=NEW.account_id;
 IF NOT FOUND OR (h.account_id,h.ordinal,h.journal_id,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count) IS DISTINCT FROM (NEW.account_id,NEW.ordinal-1,NEW.previous_id,NEW.before_label,NEW.before_provider,NEW.before_state,NEW.before_limit_amount,NEW.before_configuration_revision,NEW.before_held_amount,NEW.before_spent_amount,NEW.before_blocking_count) OR (prev.account_id,prev.ordinal,prev.after_label,prev.after_provider,prev.after_state,prev.after_limit_amount,prev.after_configuration_revision,prev.after_held_amount,prev.after_spent_amount,prev.after_blocking_count) IS DISTINCT FROM (h.account_id,h.ordinal,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
  SELECT pg_catalog.count(*) INTO v_block FROM {{MONI_SCHEMA}}.procurement_reservations WHERE account_id=NEW.account_id AND anomaly AND state IN ('unknown','disputed');
 IF NEW.after_blocking_count IS DISTINCT FROM v_block THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF NEW.source_kind='command' THEN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.command_source_id FOR SHARE;
 SELECT * INTO cs FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.command_source_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.id,NEW.root_id,NEW.step_no,NEW.step_kind,NEW.created_xid,NEW.account_id,NEW.source_id) IS DISTINCT FROM (cs.journal_id,cs.root_id,1::bigint,'account_command'::text,cs.created_xid,cs.target_id,cs.operation_id) OR NEW.kind IS DISTINCT FROM (CASE WHEN cs.kind='account_create' THEN 'create' WHEN cs.command_status='rejected' THEN 'configure_rejected' ELSE 'configure' END) OR NOT (NEW.attempt_id IS NULL AND NEW.binding_id IS NULL AND NEW.before_reservation_revision IS NULL AND NEW.before_reservation_state IS NULL AND NEW.before_reservation_held_amount IS NULL AND NEW.before_reservation_spent_amount IS NULL AND NEW.before_reservation_anomaly IS NULL AND NEW.after_reservation_revision IS NULL AND NEW.after_reservation_state IS NULL AND NEW.after_reservation_held_amount IS NULL AND NEW.after_reservation_spent_amount IS NULL AND NEW.after_reservation_anomaly IS NULL AND NEW.input_bound IS NULL AND NEW.output_bound IS NULL AND NEW.upper_amount IS NULL AND NEW.cost_version_id IS NULL AND NEW.cost_input_rate IS NULL AND NEW.cost_output_rate IS NULL AND NEW.cost_basis IS NULL AND NEW.gate_extra IS NULL AND NEW.gate_checked_at IS NULL AND NEW.pb_decision_at IS NULL AND NEW.pause_event_id IS NULL AND NEW.transition_id IS NULL AND NEW.delivery_source_id IS NULL) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   IF cs.kind='account_create' THEN
    IF NEW.ordinal<>1 OR (NEW.after_configuration_revision,NEW.after_held_amount,NEW.after_spent_amount,NEW.after_blocking_count) IS DISTINCT FROM (1::bigint,0::numeric,0::numeric,0::bigint) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   ELSIF (NEW.after_held_amount,NEW.after_spent_amount,NEW.after_blocking_count) IS DISTINCT FROM (NEW.before_held_amount,NEW.before_spent_amount,NEW.before_blocking_count) OR NEW.after_configuration_revision::numeric IS DISTINCT FROM NEW.before_configuration_revision::numeric+(CASE WHEN (NEW.after_label,NEW.after_provider,NEW.after_state,NEW.after_limit_amount) IS DISTINCT FROM (NEW.before_label,NEW.before_provider,NEW.before_state,NEW.before_limit_amount) THEN 1 ELSE 0 END) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   IF cs.command_status='applied' THEN
    IF (NEW.after_label,NEW.after_provider,NEW.after_state,NEW.after_limit_amount) IS DISTINCT FROM (cs.input_label,cs.input_provider,cs.input_state,cs.input_limit_amount) OR (cs.kind='account_configure' AND NEW.before_configuration_revision IS DISTINCT FROM cs.input_expected_revision) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   ELSIF cs.command_reason<>'revision_conflict' OR NEW.before_configuration_revision=cs.input_expected_revision OR (NEW.after_label,NEW.after_provider,NEW.after_state,NEW.after_limit_amount,NEW.after_configuration_revision) IS DISTINCT FROM (NEW.before_label,NEW.before_provider,NEW.before_state,NEW.before_limit_amount,NEW.before_configuration_revision) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF NEW.source_kind='budget_event' THEN
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=NEW.transition_id FOR SHARE;
 SELECT * INTO tr FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=NEW.transition_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=NEW.attempt_id FOR SHARE;
 SELECT * INTO p FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=NEW.source_id FOR SHARE;
 SELECT * INTO e FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=NEW.source_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.id,NEW.root_id,NEW.step_no,NEW.step_kind,NEW.created_xid,NEW.kind,NEW.account_id,NEW.binding_id,NEW.source_id,NEW.transition_id,NEW.command_source_id,NEW.delivery_source_id) IS DISTINCT FROM (ao.account_journal_id,ao.root_id,2::bigint,'procurement_transition'::text,ao.created_xid,'reserve'::text,ao.account_id,ao.binding_id,ao.reserved_event_id,ao.procurement_transition_id,NULL::text,NULL::text) OR (tr.id,tr.attempt_id,tr.event_id,tr.journal_id,tr.root_id) IS DISTINCT FROM (ao.procurement_transition_id,ao.attempt_id,ao.reserved_event_id,ao.account_journal_id,ao.root_id) OR (e.id,e.account_id,e.attempt_id,e.type,e.event_key,e.held_delta,e.spent_delta,e.evidence_ref,e.pp_root_id,e.pp_root_step,e.pp_transition_id,e.pp_journal_id,e.pp_created_xid) IS DISTINCT FROM (tr.event_id,tr.account_id,tr.attempt_id,tr.event_type,tr.event_key,tr.held_delta,tr.spent_delta,tr.evidence_ref,tr.root_id,tr.step_no,tr.id,tr.journal_id,tr.created_xid) OR (p.account_id,p.binding_id,p.revision,p.state,p.held_amount,p.spent_amount,p.anomaly,p.pp_created_xid,p.pp_origin_journal_id,p.pp_last_journal_id,p.pb_admitted_at) IS DISTINCT FROM (ao.account_id,ao.binding_id,1::bigint,'prepared'::text,tr.upper_amount::numeric,0::numeric,false,ao.created_xid,ao.account_journal_id,ao.account_journal_id,ao.admitted_at) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.before_reservation_revision,NEW.before_reservation_state,NEW.before_reservation_held_amount,NEW.before_reservation_spent_amount,NEW.before_reservation_anomaly,NEW.after_reservation_revision,NEW.after_reservation_state,NEW.after_reservation_held_amount,NEW.after_reservation_spent_amount,NEW.after_reservation_anomaly,NEW.input_bound,NEW.output_bound,NEW.upper_amount,NEW.cost_version_id,NEW.cost_input_rate,NEW.cost_output_rate,NEW.cost_basis,NEW.gate_extra,NEW.gate_checked_at,NEW.pb_decision_at,NEW.pause_event_id) IS DISTINCT FROM (tr.before_revision,tr.before_state,tr.before_held,tr.before_spent,tr.before_anomaly,tr.after_revision,tr.after_state,tr.after_held,tr.after_spent,tr.after_anomaly,tr.input_bound,tr.output_bound,tr.upper_amount,tr.cost_version_id,tr.cost_input_rate,tr.cost_output_rate,tr.cost_basis,tr.upper_amount,tr.created_at,ao.admitted_at,NULL::text) OR NEW.ordinal<=1 OR NEW.before_state<>'active' OR NEW.before_blocking_count<>0 OR NEW.before_held_amount+NEW.before_spent_amount+NEW.upper_amount>NEW.before_limit_amount OR (NEW.after_label,NEW.after_provider,NEW.after_state,NEW.after_limit_amount,NEW.after_configuration_revision,NEW.after_held_amount,NEW.after_spent_amount,NEW.after_blocking_count) IS DISTINCT FROM (NEW.before_label,NEW.before_provider,NEW.before_state,NEW.before_limit_amount,NEW.before_configuration_revision,NEW.before_held_amount+NEW.upper_amount,NEW.before_spent_amount,NEW.before_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
  END IF;
 ELSIF TG_TABLE_NAME NOT IN ('pp_account_heads','pp_attempt_heads') THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_attempt_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 v_unused boolean;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('upstream_attempts') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.pp_origin_root_id,true) OR NEW.pp_origin_root_step IS DISTINCT FROM 1::bigint OR NEW.pp_origin_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR NEW.pp_quote_selected_at IS NULL OR NOT pg_catalog.isfinite(NEW.pp_quote_selected_at) OR NOT pg_catalog.isfinite(NEW.created_at) OR (NEW.delivery_state,NEW.cost_status,NEW.cost_amount) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric) OR NOT (NEW.pb_created_xid IS NULL AND NEW.pb_dispatch_checked_at IS NULL AND NEW.pp_last_cost_mutation_id IS NULL AND NEW.pp_last_delivery_source_id IS NULL AND NEW.pp_dispatch_source_id IS NULL AND NEW.pp_dispatch_root_id IS NULL AND NEW.pp_dispatch_root_step IS NULL AND NEW.pp_dispatch_xid IS NULL AND NEW.pp_dispatch_account_id IS NULL AND NEW.pp_dispatch_journal_id IS NULL) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=NEW.pp_origin_root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=NEW.pp_origin_root_id OR attempt_id=NEW.id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=NEW.id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE attempt_id=NEW.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(NEW.request_id);
 IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=NEW.request_id AND tenant_id=NEW.tenant_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_at_route_json_v1(NEW.route_decisions,NEW.channel_id,NEW.channel_version_id);
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_attempt_result_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 v_unused boolean;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('upstream_attempts') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF NEW.pb_created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR (NEW.delivery_state,NEW.cost_status,NEW.cost_amount,NEW.pb_dispatch_checked_at) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric,NULL::timestamptz) OR NEW.pp_origin_xid IS DISTINCT FROM NEW.pb_created_xid OR NEW.pp_origin_root_step IS DISTINCT FROM 1::bigint OR NEW.pp_origin_root_id IS NULL OR NEW.pp_quote_selected_at IS NULL OR NOT (NEW.pp_last_cost_mutation_id IS NULL AND NEW.pp_last_delivery_source_id IS NULL AND NEW.pp_dispatch_source_id IS NULL AND NEW.pp_dispatch_root_id IS NULL AND NEW.pp_dispatch_root_step IS NULL AND NEW.pp_dispatch_xid IS NULL AND NEW.pp_dispatch_account_id IS NULL AND NEW.pp_dispatch_journal_id IS NULL) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_account_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE; t {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE; h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE; e {{MONI_SCHEMA}}.procurement_budget_events%ROWTYPE; v_commands bigint; v_reserves bigint;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('procurement_accounts') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT','UPDATE') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 SELECT pg_catalog.count(*) INTO v_commands FROM {{MONI_SCHEMA}}.pp_account_command_sources candidate_command WHERE candidate_command.target_id=NEW.id AND candidate_command.command_status='applied' AND candidate_command.created_xid=pg_catalog.pg_current_xact_id() AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal j WHERE j.id=candidate_command.journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=candidate_command.root_id);
 SELECT pg_catalog.count(*) INTO v_reserves FROM {{MONI_SCHEMA}}.pp_attempt_origins candidate_birth JOIN {{MONI_SCHEMA}}.pp_procurement_transitions candidate_transition ON candidate_transition.id=candidate_birth.procurement_transition_id JOIN {{MONI_SCHEMA}}.procurement_budget_events candidate_event ON candidate_event.id=candidate_birth.reserved_event_id WHERE candidate_birth.account_id=NEW.id AND candidate_birth.management_mode='managed' AND candidate_birth.created_xid=pg_catalog.pg_current_xact_id() AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal j WHERE j.id=candidate_birth.account_journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=candidate_birth.root_id);
 IF v_commands+v_reserves<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF v_commands=1 THEN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE target_id=NEW.id AND command_status='applied' AND created_xid=pg_catalog.pg_current_xact_id() AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal j WHERE j.id=pp_account_command_sources.journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=pp_account_command_sources.root_id) FOR SHARE;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE target_id=NEW.id AND command_status='applied' AND created_xid=pg_catalog.pg_current_xact_id() AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal j WHERE j.id=pp_account_command_sources.journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=pp_account_command_sources.root_id);
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (NEW.label,NEW.provider,NEW.state,NEW.limit_amount) IS DISTINCT FROM (s.input_label,s.input_provider,s.input_state,s.input_limit_amount) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF TG_OP='INSERT' THEN
   IF s.kind<>'account_create' OR (NEW.pp_version,NEW.pp_created_xid,NEW.pp_origin_journal_id,NEW.revision,NEW.held_amount,NEW.spent_amount) IS DISTINCT FROM (1::smallint,s.created_xid,s.journal_id,1::bigint,0::numeric,0::numeric) OR NOT pg_catalog.isfinite(NEW.created_at) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   IF s.kind<>'account_configure' OR OLD.revision IS DISTINCT FROM s.input_expected_revision OR (NEW.id,NEW.created_at,NEW.pp_version,NEW.pp_created_xid,NEW.pp_origin_journal_id,NEW.held_amount,NEW.spent_amount,NEW.revision) IS DISTINCT FROM (OLD.id,OLD.created_at,OLD.pp_version,OLD.pp_created_xid,OLD.pp_origin_journal_id,OLD.held_amount,OLD.spent_amount,OLD.revision) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
 ELSE
  IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE account_id=NEW.id AND management_mode='managed' AND created_xid=pg_catalog.pg_current_xact_id() AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal j WHERE j.id=pp_attempt_origins.account_journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=pp_attempt_origins.root_id) FOR SHARE;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE account_id=NEW.id AND management_mode='managed' AND created_xid=pg_catalog.pg_current_xact_id() AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal j WHERE j.id=pp_attempt_origins.account_journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=pp_attempt_origins.root_id);
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=o.procurement_transition_id FOR SHARE;
 SELECT * INTO t FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=o.procurement_transition_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=o.reserved_event_id FOR SHARE;
 SELECT * INTO e FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE id=o.reserved_event_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=NEW.id;
 IF NOT FOUND OR (OLD.label,OLD.provider,OLD.state,OLD.limit_amount,OLD.revision,OLD.held_amount,OLD.spent_amount) IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount) OR (e.account_id,e.attempt_id,e.type,e.event_key,e.held_delta,e.spent_delta,e.evidence_ref,e.pp_root_id,e.pp_root_step,e.pp_transition_id,e.pp_journal_id,e.pp_created_xid) IS DISTINCT FROM (o.account_id,o.attempt_id,'reserved'::text,'reserve:'||o.attempt_id,t.upper_amount::numeric,0::numeric,''::text,o.root_id,2::bigint,t.id,o.account_journal_id,o.created_xid) OR (NEW.id,NEW.label,NEW.provider,NEW.state,NEW.limit_amount,NEW.revision,NEW.held_amount,NEW.spent_amount,NEW.created_at,NEW.pp_version,NEW.pp_created_xid,NEW.pp_origin_journal_id) IS DISTINCT FROM (OLD.id,OLD.label,OLD.provider,OLD.state,OLD.limit_amount,OLD.revision,OLD.held_amount+t.upper_amount,OLD.spent_amount,OLD.created_at,OLD.pp_version,OLD.pp_created_xid,OLD.pp_origin_journal_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_account_result_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 v_unused boolean;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('procurement_accounts') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT','UPDATE') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF TG_OP='INSERT' THEN
  IF (NEW.revision,NEW.held_amount,NEW.spent_amount) IS DISTINCT FROM (1::bigint,0::numeric,0::numeric) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSE
  IF NEW.revision::numeric IS DISTINCT FROM OLD.revision::numeric+(CASE WHEN (NEW.label,NEW.provider,NEW.state,NEW.limit_amount) IS DISTINCT FROM (OLD.label,OLD.provider,OLD.state,OLD.limit_amount) THEN 1 ELSE 0 END) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_procurement_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('procurement_reservations') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 IF NOT (NEW.pp_created_xid IS NULL AND NEW.pp_origin_journal_id IS NULL AND NEW.pp_last_journal_id IS NULL AND NEW.pb_created_xid IS NULL AND NEW.pb_control IS NULL AND NEW.pb_admitted_at IS NULL) OR (NEW.state,NEW.held_amount,NEW.spent_amount,NEW.anomaly,NEW.revision) IS DISTINCT FROM ('prepared'::text,NEW.upper_amount,0::numeric,false,1::bigint) OR NOT pg_catalog.isfinite(NEW.created_at) OR NOT pg_catalog.isfinite(NEW.updated_at) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=NEW.attempt_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=NEW.attempt_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id FOR SHARE;
 SELECT * INTO u FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF u.pp_origin_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR u.pp_origin_root_step IS DISTINCT FROM 1::bigint OR u.pp_origin_root_id IS NULL OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=u.pp_origin_root_id) OR NOT (SELECT {{MONI_SCHEMA}}.pb_current_tuple(x.pb_created_xid,x.xmin) FROM {{MONI_SCHEMA}}.upstream_attempts x WHERE x.id=NEW.attempt_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_attempt_birth_p_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 u {{MONI_SCHEMA}}.upstream_attempts%ROWTYPE; q {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE; a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE; h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE;
 ao {{MONI_SCHEMA}}.pp_account_origins%ROWTYPE; v_root text; v_transition text; v_event text; v_journal text; v_gate timestamptz; v_upper numeric; v_control text;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('procurement_reservations') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 PERFORM id FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id FOR SHARE;
 SELECT * INTO u FROM {{MONI_SCHEMA}}.upstream_attempts WHERE id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_validate_request_leaf_v1(u.request_id);
 SELECT * INTO q FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=u.request_id;
 IF NOT FOUND OR q.tenant_id IS DISTINCT FROM u.tenant_id THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM account_id FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_id FOR SHARE;
 SELECT * INTO ao FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.account_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=NEW.account_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT id INTO v_control FROM {{MONI_SCHEMA}}.periodic_budget_controls WHERE scope='procurement' AND account_id=NEW.account_id;
 v_upper:=pg_catalog.ceil((q.input_upper_bound::numeric*u.cost_input_rate::numeric+q.output_limit::numeric*u.cost_output_rate::numeric)/1000000);
 IF TG_NAME<>'zz_pp_attempt_birth_p' OR u.pp_origin_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR u.pp_origin_root_step IS DISTINCT FROM 1::bigint OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(u.pp_origin_root_id,true) OR (u.delivery_state,u.cost_status,u.cost_amount,u.pb_dispatch_checked_at) IS DISTINCT FROM ('prepared'::text,'unknown'::text,NULL::numeric,NULL::timestamptz) OR u.cost_version_id IS NULL OR u.cost_input_rate IS NULL OR u.cost_output_rate IS NULL OR u.cost_basis NOT IN ('contract','invoice') OR v_upper IS NULL OR v_upper<0 OR v_upper>9223372036854775807 OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.credential_procurement_bindings WHERE (id,credential_id,channel_id,account_id) IS NOT DISTINCT FROM (NEW.binding_id,u.credential_id,u.channel_id,NEW.account_id)) OR (NEW.input_bound,NEW.output_bound,NEW.upper_amount,NEW.state,NEW.held_amount,NEW.spent_amount,NEW.anomaly,NEW.revision,NEW.pb_created_xid,NEW.pb_control) IS DISTINCT FROM (q.input_upper_bound,q.output_limit,v_upper,'prepared'::text,v_upper,0::numeric,false,1::bigint,u.pp_origin_xid,v_control) OR NOT pg_catalog.isfinite(NEW.pb_admitted_at) OR NOT (NEW.pp_created_xid IS NULL AND NEW.pp_origin_journal_id IS NULL AND NEW.pp_last_journal_id IS NULL) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=u.pp_origin_root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=u.id OR root_id=u.pp_origin_root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_heads WHERE attempt_id=u.id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=u.id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE attempt_id=u.id OR root_id=u.pp_origin_root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE attempt_id=u.id OR root_id=u.pp_origin_root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount) IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount) OR a.state<>'active' OR h.blocking_count<>0 OR a.held_amount+a.spent_amount+v_upper>a.limit_amount OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE account_id=NEW.account_id AND anomaly AND state IN ('unknown','disputed')) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_gate:=pg_catalog.clock_timestamp(); v_root:=u.pp_origin_root_id;
 v_transition:=pg_catalog.gen_random_uuid()::text; v_event:=pg_catalog.gen_random_uuid()::text; v_journal:=pg_catalog.gen_random_uuid()::text;
 INSERT INTO {{MONI_SCHEMA}}.pp_attempt_origins(attempt_id,root_id,step_no,created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at)
 VALUES(u.id,v_root,1,u.pp_origin_xid,u.tenant_id,u.request_id,u.sequence,u.channel_id,u.channel_version_id,u.credential_id,u.upstream_model,u.route_decisions,u.cost_version_id,u.cost_input_rate,u.cost_output_rate,u.cost_basis,u.pp_quote_selected_at,u.created_at,u.pb_created_xid,'prepared','unknown',NULL,'managed',NEW.account_id,NEW.binding_id,v_transition,v_event,v_journal,NEW.pb_admitted_at);
 INSERT INTO {{MONI_SCHEMA}}.pp_attempt_heads(attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid) VALUES(u.id,0,NULL,NULL,NULL,u.pp_origin_xid);
 INSERT INTO {{MONI_SCHEMA}}.pp_procurement_transitions(id,root_id,step_no,created_xid,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,cause_kind,cause_source_id,event_id,journal_id,event_type,event_key,evidence_ref,held_delta,spent_delta,after_revision,after_state,after_held,after_spent,after_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis)
 VALUES(v_transition,v_root,2,u.pp_origin_xid,v_gate,u.tenant_id,u.request_id,u.id,NEW.account_id,NEW.binding_id,'attempt_birth',u.id,v_event,v_journal,'reserved','reserve:'||u.id,'',v_upper,0,1,'prepared',v_upper,0,false,q.input_upper_bound,q.output_limit,v_upper::bigint,u.cost_version_id,u.cost_input_rate,u.cost_output_rate,u.cost_basis);
 NEW.pp_created_xid:=u.pp_origin_xid; NEW.pp_origin_journal_id:=v_journal; NEW.pp_last_journal_id:=v_journal;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_event_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 o {{MONI_SCHEMA}}.pp_attempt_origins%ROWTYPE; t {{MONI_SCHEMA}}.pp_procurement_transitions%ROWTYPE;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('procurement_budget_events') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP NOT IN ('INSERT') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 PERFORM attempt_id FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id FOR SHARE;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE attempt_id=NEW.attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM id FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=NEW.pp_transition_id FOR SHARE;
 SELECT * INTO t FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE id=NEW.pp_transition_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF o.management_mode<>'managed' OR o.created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR (NEW.id,NEW.event_key,NEW.type,NEW.account_id,NEW.attempt_id,NEW.held_delta,NEW.spent_delta,NEW.evidence_ref,NEW.pp_root_id,NEW.pp_root_step,NEW.pp_transition_id,NEW.pp_journal_id,NEW.pp_created_xid) IS DISTINCT FROM (o.reserved_event_id,t.event_key,'reserved'::text,o.account_id,o.attempt_id,t.upper_amount::numeric,0::numeric,''::text,o.root_id,2::bigint,t.id,o.account_journal_id,o.created_xid) OR (t.id,t.root_id,t.attempt_id,t.event_id,t.journal_id) IS DISTINCT FROM (o.procurement_transition_id,o.root_id,o.attempt_id,o.reserved_event_id,o.account_journal_id) OR NEW.pb_created_xid IS NOT NULL OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=o.root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE attempt_id=o.attempt_id OR pp_root_id=o.root_id OR pp_transition_id=t.id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=o.account_journal_id) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE (attempt_id,account_id,binding_id,revision,state,held_amount,spent_amount,anomaly,pp_created_xid,pp_origin_journal_id,pp_last_journal_id,pb_admitted_at) IS NOT DISTINCT FROM (o.attempt_id,o.account_id,o.binding_id,1::bigint,'prepared'::text,t.upper_amount::numeric,0::numeric,false,o.created_xid,o.account_journal_id,o.account_journal_id,o.admitted_at)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_command_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; a {{MONI_SCHEMA}}.pp_account_origins%ROWTYPE; v_before jsonb:='null'::jsonb; v_after jsonb:='null'::jsonb;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('procurement_commands') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF NEW.kind IN ('credential_bind','balance_observe') THEN
  IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid OR NOT (NEW.pp_source_id IS NULL AND NEW.pp_root_id IS NULL AND NEW.pp_root_step IS NULL AND NEW.pp_created_xid IS NULL AND NEW.pp_account_journal_id IS NULL) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE operation_id=NEW.operation_id) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  RETURN NEW;
 END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.pp_source_id FOR SHARE;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.pp_source_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF s.created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR s.kind NOT IN ('account_create','account_configure') OR (NEW.operation_id,NEW.actor,NEW.kind,NEW.target_id,NEW.input_digest,NEW.status,NEW.reason,NEW.pp_root_id,NEW.pp_root_step,NEW.pp_created_xid,NEW.pp_account_journal_id) IS DISTINCT FROM (s.operation_id,s.actor_id,s.kind,s.target_id,s.input_digest,s.command_status,s.command_reason,s.root_id,1::bigint,s.created_xid,s.journal_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=s.root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=s.operation_id OR pp_source_id=s.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF s.journal_id IS NOT NULL THEN
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=s.journal_id FOR SHARE;
 SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=s.journal_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM account_id FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=s.target_id FOR SHARE;
 SELECT * INTO a FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=s.target_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF (j.account_id,j.root_id,j.step_no,j.step_kind,j.created_xid,j.source_kind,j.source_id,j.command_source_id) IS DISTINCT FROM (s.target_id,s.root_id,1::bigint,'account_command'::text,s.created_xid,'command'::text,s.operation_id,s.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF s.kind='account_configure' THEN v_before:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(j.account_id,j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_held_amount,j.before_spent_amount,j.before_configuration_revision,a.created_at); END IF;
  IF s.command_status='applied' THEN v_after:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(j.account_id,j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_held_amount,j.after_spent_amount,j.after_configuration_revision,a.created_at); END IF;
 ELSE
  IF s.kind<>'account_configure' OR s.command_status<>'rejected' OR s.command_reason<>'not_found' OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=s.target_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=s.root_id OR command_source_id=s.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 IF (NEW.before_state,NEW.after_state) IS DISTINCT FROM (v_before,v_after) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 NEW.created_at:=s.command_created_at;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_audit_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('audit_events') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP='TRUNCATE' OR (TG_OP<>'INSERT' AND current_user::regrole::oid={{MONI_WRITER_OID}}::oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP IN ('UPDATE','DELETE') THEN
  IF NOT (OLD.pp_root_id IS NULL AND OLD.pp_root_step IS NULL AND OLD.pp_source_kind IS NULL AND OLD.pp_source_id IS NULL AND OLD.pp_source_role IS NULL AND OLD.pp_created_xid IS NULL) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  IF NOT (NEW.pp_root_id IS NULL AND NEW.pp_root_step IS NULL AND NEW.pp_source_kind IS NULL AND NEW.pp_source_id IS NULL AND NEW.pp_source_role IS NULL AND NEW.pp_created_xid IS NULL) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  IF NEW.action IN ('procurement.command.applied','procurement.command.rejected') AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=NEW.target AND kind IN ('credential_bind','balance_observe') AND pp_source_id IS NULL) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF NEW.pp_root_id IS NULL AND NEW.pp_root_step IS NULL AND NEW.pp_source_kind IS NULL AND NEW.pp_source_id IS NULL AND NEW.pp_source_role IS NULL AND NEW.pp_created_xid IS NULL THEN
  IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  IF NEW.action IN ('procurement.command.applied','procurement.command.rejected') AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=NEW.target AND kind IN ('credential_bind','balance_observe') AND pp_source_id IS NULL) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  RETURN NEW;
 END IF;
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_writer_v1();
 PERFORM id FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.pp_source_id FOR SHARE;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.pp_source_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF s.created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id() OR (NEW.id,NEW.actor,NEW.action,NEW.target,NEW.tenant_scope,NEW.request_id,NEW.sanitized_diff,NEW.pp_root_id,NEW.pp_root_step,NEW.pp_source_kind,NEW.pp_source_id,NEW.pp_source_role,NEW.pp_created_xid) IS DISTINCT FROM (s.audit_id,s.actor_id,'procurement.command.'||s.command_status,s.operation_id,NULL::text,''::text,pg_catalog.jsonb_build_object('kind',s.kind,'status',s.command_status),s.root_id,1::bigint,'account_command'::text,s.id,'account_command'::text,s.created_xid) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=s.root_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE id=s.audit_id OR pp_root_id=s.root_id) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE (operation_id,pp_source_id,pp_root_id,pp_root_step,pp_created_xid) IS NOT DISTINCT FROM (s.operation_id,s.id,s.root_id,1::bigint,s.created_xid)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_parent_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 v_unused boolean;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('users','tenants','memberships','models','channels','api_keys','reservations') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_deferred_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 r {{MONI_SCHEMA}}.request_records%ROWTYPE; current_request {{MONI_SCHEMA}}.request_records%ROWTYPE; v_request text;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('pp_operation_roots','pp_root_steps','pp_request_origins','pp_request_heads','pp_attempt_origins','pp_attempt_heads','pp_attempt_nodes','pp_account_command_sources','pp_account_origins','pp_account_heads','pp_account_journal','pp_procurement_transitions','request_records','upstream_attempts','procurement_accounts','procurement_reservations','procurement_budget_events','procurement_commands','audit_events') OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_OP NOT IN ('INSERT','UPDATE','DELETE') THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_TABLE_NAME='request_records' THEN
  IF TG_OP='DELETE' THEN r:=OLD; ELSE r:=NEW; END IF;
  v_request:=r.id;
  SELECT * INTO current_request FROM {{MONI_SCHEMA}}.request_records WHERE id=v_request;
  IF NOT FOUND THEN
   IF r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE request_id=v_request) THEN RETURN NULL; END IF;
   RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
  END IF;
  IF current_request.pp_origin_root_id IS NULL AND current_request.pp_origin_root_step IS NULL AND current_request.pp_origin_xid IS NULL AND current_request.pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE request_id=v_request) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE request_id=v_request) THEN RETURN NULL; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_validate_request(v_request);
  IF current_request.billing_status='pending_reconciliation' AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.reconciliation_cases WHERE request_id=v_request AND tenant_id=current_request.tenant_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME IN ('pp_request_origins','pp_request_heads') THEN
  PERFORM {{MONI_SCHEMA}}.pp_validate_request(NEW.request_id);
 ELSIF TG_TABLE_NAME='upstream_attempts' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_attempt(NEW.id);
 ELSIF TG_TABLE_NAME IN ('pp_attempt_origins','pp_attempt_heads','pp_attempt_nodes') THEN PERFORM {{MONI_SCHEMA}}.pp_validate_attempt(NEW.attempt_id);
 ELSIF TG_TABLE_NAME='procurement_accounts' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_account(NEW.id);
 ELSIF TG_TABLE_NAME IN ('pp_account_origins','pp_account_heads','pp_account_journal') THEN PERFORM {{MONI_SCHEMA}}.pp_validate_account(NEW.account_id);
 ELSIF TG_TABLE_NAME='pp_account_command_sources' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_root(NEW.root_id);
 ELSIF TG_TABLE_NAME='pp_procurement_transitions' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_root(NEW.root_id);
 ELSIF TG_TABLE_NAME='procurement_reservations' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_original('procurement',NEW.attempt_id,NULL);
 ELSIF TG_TABLE_NAME='procurement_budget_events' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_original('procurement_event',NEW.id,NULL);
 ELSIF TG_TABLE_NAME='procurement_commands' THEN
  IF NEW.kind IN ('account_create','account_configure') OR NEW.pp_source_id IS NOT NULL OR NEW.pp_root_id IS NOT NULL OR NEW.pp_root_step IS NOT NULL OR NEW.pp_created_xid IS NOT NULL OR NEW.pp_account_journal_id IS NOT NULL THEN PERFORM {{MONI_SCHEMA}}.pp_validate_original('procurement_command',NEW.operation_id,NULL); END IF;
 ELSIF TG_TABLE_NAME='audit_events' THEN
  IF TG_OP='DELETE' THEN
   IF OLD.pp_root_id IS NOT NULL OR OLD.pp_root_step IS NOT NULL OR OLD.pp_source_kind IS NOT NULL OR OLD.pp_source_id IS NOT NULL OR OLD.pp_source_role IS NOT NULL OR OLD.pp_created_xid IS NOT NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF NEW.pp_root_id IS NOT NULL OR NEW.pp_root_step IS NOT NULL OR NEW.pp_source_kind IS NOT NULL OR NEW.pp_source_id IS NOT NULL OR NEW.pp_source_role IS NOT NULL OR NEW.pp_created_xid IS NOT NULL THEN PERFORM {{MONI_SCHEMA}}.pp_validate_original('audit',NEW.id,NULL); END IF;
 ELSIF TG_TABLE_NAME='pp_operation_roots' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_root(NEW.id);
 ELSIF TG_TABLE_NAME='pp_root_steps' THEN PERFORM {{MONI_SCHEMA}}.pp_validate_root(NEW.root_id);
 ELSE RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903';
 END IF;
 RETURN NULL;
END $$;

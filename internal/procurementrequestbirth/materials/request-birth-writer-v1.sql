-- Isolated birth-exact-link-a1-v1 writer. Not a serving installer or full Reserve root.
-- Requires only procurementbirthschema.A1SQL plus original migrations 001-022.
CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_validate_request_v1(p_request text) RETURNS void
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 r {{MONI_SCHEMA}}.request_records%ROWTYPE; o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_request_heads%ROWTYPE; rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE;
 st {{MONI_SCHEMA}}.pp_root_steps%ROWTYPE; n bigint;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname={{MONI_SCHEMA_TEXT}} AND c.relkind IN ('r','p','v','m','f') AND pg_catalog.left(c.relname,3)='pp_'
   AND c.relname NOT IN ('pp_operation_roots','pp_root_steps','pp_request_origins','pp_request_heads')) THEN
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 END IF;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records WHERE id=p_request;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=p_request;
 IF NOT FOUND THEN
  IF r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=p_request) THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 IF NOT ((r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::pg_catalog.text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at) IS NOT DISTINCT FROM (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)) OR NOT ((r.pp_origin_root_id,r.pp_origin_root_step,r.pp_origin_xid) IS NOT DISTINCT FROM (o.root_id,o.step_no,o.created_xid)) OR r.pp_last_decision_id IS NOT NULL OR r.latest_finalization_id IS NOT NULL
  OR NOT ((o.initial_status,o.initial_billing_status,o.initial_latest_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)) OR o.pb_created_xid IS DISTINCT FROM o.created_xid OR o.step_no<>1 OR o.step_kind<>'request_birth' OR NOT (o.root_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND o.root_id<>'00000000-0000-0000-0000-000000000000')
  OR NOT (pg_catalog.isfinite(o.request_deadline) AND pg_catalog.isfinite(o.request_created_at) AND pg_catalog.isfinite(o.retail_priced_at) AND pg_catalog.isfinite(o.pb_admitted_at)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=p_request;
 IF NOT FOUND OR (h.ordinal,h.last_decision_id,h.updated_xid) IS DISTINCT FROM (0::bigint,NULL::text,o.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=o.root_id;
 IF NOT FOUND OR NOT ((rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result) IS NOT DISTINCT FROM ('request_birth'::text,'request'::text,o.tenant_id,o.request_id,NULL::text,NULL::text,o.created_xid,{{MONI_APP_OID}}::oid,1::bigint,'born'::text)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id AND step_no=1;
 IF NOT FOUND OR (st.kind,st.source_id,st.created_xid,st.d_request_birth) IS DISTINCT FROM ('request_birth'::text,o.request_id,o.created_xid,o.request_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=o.root_id;
 IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=o.root_id;
 IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=o.root_id;
 IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=p_request;
 IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=p_request;
 IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_validate_root_v1(p_root text) RETURNS void
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE rt {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; n bigint;
BEGIN
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid NOT IN ({{MONI_APP_OID}}::pg_catalog.oid,{{MONI_WRITER_OID}}::pg_catalog.oid) THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO rt FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=p_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- A claimed request graph cannot be downgraded by changing its kind.
 IF rt.entry_kind<>'request_birth' THEN
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=p_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=p_root AND kind='request_birth') THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 END IF;
 PERFORM {{MONI_SCHEMA}}.pp_rb_validate_request_v1(rt.request_id);
 IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=rt.request_id AND root_id=p_root) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_request_write_v1() RETURNS trigger
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
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=OLD.id) THEN
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
 PERFORM {{MONI_SCHEMA}}.pp_rb_validate_request_v1(OLD.id);
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

CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_source_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE r {{MONI_SCHEMA}}.request_records%ROWTYPE; o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('pp_request_origins','pp_request_heads')
  OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP<>'INSERT' OR session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_WRITER_OID}}::pg_catalog.oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('lock_timeout')<>'3s' OR pg_catalog.current_setting('statement_timeout')<>'5s' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
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
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_shared_write_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE r {{MONI_SCHEMA}}.request_records%ROWTYPE; o {{MONI_SCHEMA}}.pp_request_origins%ROWTYPE; h {{MONI_SCHEMA}}.pp_request_heads%ROWTYPE; st {{MONI_SCHEMA}}.pp_root_steps%ROWTYPE; v_root text; n bigint;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME NOT IN ('pp_operation_roots','pp_root_steps')
  OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_OP<>'INSERT' OR session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_WRITER_OID}}::pg_catalog.oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('lock_timeout')<>'3s' OR pg_catalog.current_setting('statement_timeout')<>'5s' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_TABLE_NAME='pp_operation_roots' THEN v_root:=NEW.id; ELSE v_root:=NEW.root_id; END IF;
 IF NOT (v_root ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v_root<>'00000000-0000-0000-0000-000000000000') OR NEW.created_xid<>pg_catalog.pg_current_xact_id()
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=v_root) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- Exactly one actual source, then lock it and re-read after every wait.
 SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root;
 IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root FOR SHARE;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=v_root;
 PERFORM id FROM {{MONI_SCHEMA}}.request_records WHERE id=o.request_id FOR SHARE;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records WHERE id=o.request_id;
 IF NOT FOUND OR NOT ((r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::pg_catalog.text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at) IS NOT DISTINCT FROM (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)) OR NOT ((r.pp_origin_root_id,r.pp_origin_root_step,r.pp_origin_xid) IS NOT DISTINCT FROM (o.root_id,o.step_no,o.created_xid)) OR NOT ((r.status,r.billing_status,r.latest_finalization_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)) OR r.pp_last_decision_id IS NOT NULL OR o.created_xid<>pg_catalog.pg_current_xact_id() THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 PERFORM request_id FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=o.request_id FOR SHARE;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=o.request_id;
 IF NOT FOUND OR (h.ordinal,h.last_decision_id,h.updated_xid) IS DISTINCT FROM (0::bigint,NULL::text,o.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF TG_TABLE_NAME='pp_root_steps' THEN
  IF (NEW.step_no,NEW.kind,NEW.source_id,NEW.created_xid) IS DISTINCT FROM (1::bigint,'request_birth'::text,o.request_id,o.created_xid)
   OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root OR (kind='request_birth' AND source_id=o.request_id)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSE
  PERFORM root_id FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root FOR SHARE;
  SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root;
  IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO st FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root AND step_no=1;
  IF NOT FOUND OR (st.kind,st.source_id,st.created_xid) IS DISTINCT FROM ('request_birth'::text,o.request_id,o.created_xid)
   OR NOT ((NEW.entry_kind,NEW.scope_kind,NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.account_target_id,NEW.created_xid,NEW.session_role_oid,NEW.step_count,NEW.semantic_result) IS NOT DISTINCT FROM ('request_birth'::text,'request'::text,o.tenant_id,o.request_id,NULL::text,NULL::text,o.created_xid,{{MONI_APP_OID}}::oid,1::bigint,'born'::text)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT pg_catalog.count(*) INTO n FROM {{MONI_SCHEMA}}.request_records WHERE pp_origin_root_id=v_root;
  IF n<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=o.request_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_capture_request_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY DEFINER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE r {{MONI_SCHEMA}}.request_records%ROWTYPE; v_root text; v_xid xid8;
BEGIN
 IF TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}} OR TG_TABLE_NAME<>'request_records' OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},'request_records')) OR TG_WHEN<>'AFTER' OR TG_OP<>'INSERT' OR TG_LEVEL<>'ROW' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF session_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_APP_OID}}::pg_catalog.oid OR current_user::pg_catalog.regrole::pg_catalog.oid<>{{MONI_WRITER_OID}}::pg_catalog.oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('lock_timeout')<>'3s' OR pg_catalog.current_setting('statement_timeout')<>'5s' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records WHERE id=NEW.id;
 IF NOT FOUND OR (r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at) IS DISTINCT FROM (NEW.id,NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.price_version_id,NEW.input_rate,NEW.output_rate,NEW.input_upper_bound,NEW.output_limit,NEW.profile_version,NEW.policy_version,NEW.request_hmac,NEW.route_revision,NEW.request_deadline,NEW.instance_id,NEW.streaming,NEW.client_ip,NEW.pricing_group_id,NEW.tenant_pricing_revision,NEW.retail_priced_at,NEW.created_at,NEW.pb_created_xid,NEW.pb_tenant_control,NEW.pb_key_control,NEW.pb_admitted_at) OR NOT (r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL) OR NOT ((r.status,r.billing_status,r.latest_finalization_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text))
  OR r.pb_created_xid IS DISTINCT FROM pg_catalog.pg_current_xact_id()
  OR NOT pg_catalog.isfinite(r.created_at) OR NOT pg_catalog.isfinite(r.retail_priced_at) OR NOT pg_catalog.isfinite(r.request_deadline) OR NOT pg_catalog.isfinite(r.pb_admitted_at)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=r.id)
  OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=r.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_root:=pg_catalog.gen_random_uuid()::pg_catalog.text; v_xid:=pg_catalog.pg_current_xact_id();
 INSERT INTO {{MONI_SCHEMA}}.pp_request_origins(request_id,root_id,step_no,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status) VALUES(r.id,v_root,1,v_xid,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::pg_catalog.text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at,r.latest_finalization_id,r.status,r.billing_status);
 INSERT INTO {{MONI_SCHEMA}}.pp_request_heads(request_id,ordinal,last_decision_id,updated_xid) VALUES(r.id,0,NULL,v_xid);
 UPDATE {{MONI_SCHEMA}}.request_records SET pp_origin_root_id=v_root,pp_origin_root_step=1,pp_origin_xid=v_xid WHERE id=r.id;
 INSERT INTO {{MONI_SCHEMA}}.pp_root_steps(root_id,step_no,kind,source_id,created_xid) VALUES(v_root,1,'request_birth',r.id,v_xid);
 INSERT INTO {{MONI_SCHEMA}}.pp_operation_roots(id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result)
 VALUES(v_root,'request_birth','request',r.tenant_id,r.id,NULL,NULL,v_xid,{{MONI_APP_OID}}::oid,1,'born');
 PERFORM {{MONI_SCHEMA}}.pp_rb_validate_request_v1(r.id);
 PERFORM {{MONI_SCHEMA}}.pp_rb_validate_root_v1(v_root);
 RETURN NULL;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_rb_deferred_v1() RETURNS trigger
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE v_request text; r {{MONI_SCHEMA}}.request_records%ROWTYPE;
BEGIN
 IF TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_OP NOT IN ('INSERT','UPDATE') OR TG_TABLE_SCHEMA IS DISTINCT FROM {{MONI_SCHEMA_TEXT}}
  OR TG_RELID<>pg_catalog.to_regclass(pg_catalog.format('%I.%I',{{MONI_SCHEMA_TEXT}},TG_TABLE_NAME)) THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
 IF TG_TABLE_NAME='pp_operation_roots' THEN PERFORM {{MONI_SCHEMA}}.pp_rb_validate_root_v1(NEW.id); RETURN NULL;
 ELSIF TG_TABLE_NAME='pp_root_steps' THEN PERFORM {{MONI_SCHEMA}}.pp_rb_validate_root_v1(NEW.root_id); RETURN NULL;
 ELSIF TG_TABLE_NAME IN ('pp_request_origins','pp_request_heads') THEN v_request:=NEW.request_id;
 ELSIF TG_TABLE_NAME='request_records' THEN v_request:=NEW.id;
 ELSE RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903';
 END IF;
 SELECT * INTO r FROM {{MONI_SCHEMA}}.request_records WHERE id=v_request;
 IF NOT FOUND THEN
  -- A queued UPDATE of a genuinely untracked legacy row may be followed by
  -- its originally legal DELETE. Never swallow missing tracked/claimed rows.
  IF TG_TABLE_NAME='request_records' THEN
   IF NEW.pp_origin_root_id IS NULL AND NEW.pp_origin_root_step IS NULL AND NEW.pp_origin_xid IS NULL AND NEW.pp_last_decision_id IS NULL
    AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=v_request)
    AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=v_request)
    AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE request_id=v_request)
    AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE kind='request_birth' AND source_id=v_request) THEN RETURN NULL; END IF;
  END IF;
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 IF r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE request_id=v_request)
  AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_heads WHERE request_id=v_request) THEN RETURN NULL; END IF;
 PERFORM {{MONI_SCHEMA}}.pp_rb_validate_request_v1(v_request);
 IF r.billing_status='pending_reconciliation' AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.reconciliation_cases WHERE request_id=r.id AND tenant_id=r.tenant_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER aa_pp_rb_request_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.request_records FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_request_write_v1();
CREATE TRIGGER aa_pp_rb_request_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.request_records FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_request_write_v1();
CREATE CONSTRAINT TRIGGER zz_pp_rb_request_complete AFTER INSERT OR UPDATE ON {{MONI_SCHEMA}}.request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_deferred_v1();
CREATE TRIGGER aa_pp_rb_origin_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_request_origins FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_source_write_v1();
CREATE TRIGGER aa_pp_rb_origin_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_request_origins FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_source_write_v1();
CREATE CONSTRAINT TRIGGER zz_pp_rb_origin_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_request_origins DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_deferred_v1();
CREATE TRIGGER aa_pp_rb_head_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_request_heads FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_source_write_v1();
CREATE TRIGGER aa_pp_rb_head_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_request_heads FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_source_write_v1();
CREATE CONSTRAINT TRIGGER zz_pp_rb_head_complete AFTER INSERT OR UPDATE ON {{MONI_SCHEMA}}.pp_request_heads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_deferred_v1();
CREATE TRIGGER aa_pp_rb_root_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_operation_roots FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_shared_write_v1();
CREATE TRIGGER aa_pp_rb_root_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_operation_roots FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_shared_write_v1();
CREATE CONSTRAINT TRIGGER zz_pp_rb_root_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_operation_roots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_deferred_v1();
CREATE TRIGGER aa_pp_rb_step_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_root_steps FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_shared_write_v1();
CREATE TRIGGER aa_pp_rb_step_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_root_steps FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_shared_write_v1();
CREATE CONSTRAINT TRIGGER zz_pp_rb_step_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_root_steps DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_deferred_v1();
CREATE TRIGGER zz_pp_rb_request_birth AFTER INSERT ON {{MONI_SCHEMA}}.request_records FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_rb_capture_request_v1();

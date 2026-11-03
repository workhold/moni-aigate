-- Same frozen A1 request capture, with the expanded A2 read-only validators.
CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_capture_request_v1() RETURNS trigger
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
 PERFORM {{MONI_SCHEMA}}.pp_validate_request(r.id);
 PERFORM {{MONI_SCHEMA}}.pp_validate_root(v_root);
 RETURN NULL;
END $$;

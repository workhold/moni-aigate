-- Account commands for the independent command+request+attempt A2 candidate.
-- All business effects precede the single final root seal. No retry is hidden.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ab_account_mutate_v1(p_kind text,p_actor text,p_operation text,p_target text,p_expected bigint,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS TABLE(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 c {{MONI_SCHEMA}}.procurement_commands%ROWTYPE;
 a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE;
 b {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE;
 v_digest text; v_role text; v_user_status text; v_existing boolean; v_present boolean;
 v_root text; v_source text; v_journal text; v_audit text; v_target text;
 v_status text:='applied'; v_reason text:=''; v_before jsonb:='null'::jsonb; v_after jsonb:='null'::jsonb;
 v_time timestamptz; v_xid xid8; v_journal_kind text; v_block bigint; v_rows bigint;
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ab_require_root_v1();
 IF p_kind IS NULL OR p_kind NOT IN ('account_create','account_configure') OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_actor,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_operation,false) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_provider) OR p_state IS NULL OR p_state NOT IN ('active','paused') OR p_limit IS NULL OR p_limit<0 OR p_target IS NULL OR (p_kind='account_create' AND (p_target<>'' OR p_expected IS NOT NULL)) OR (p_kind='account_configure' AND (NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_target,false) OR p_expected IS NULL OR p_expected<=0)) THEN
  RAISE EXCEPTION 'invalid_procurement_configuration' USING ERRCODE='PP001';
 END IF;
 v_digest:={{MONI_SCHEMA}}.pp_ac_command_digest_v1(p_kind,p_operation,p_target,p_expected,p_label,p_provider,p_state,p_limit);
 PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('procurement:'||p_operation,19019));
 SELECT * INTO c FROM {{MONI_SCHEMA}}.procurement_commands pc WHERE pc.operation_id=p_operation;
 v_existing:=FOUND;
 -- Preserve the original conflict-before-authorization error priority.
 IF v_existing AND (c.actor IS DISTINCT FROM p_actor OR c.input_digest IS DISTINCT FROM v_digest) THEN
  RAISE EXCEPTION 'procurement_operation_conflict' USING ERRCODE='PP002';
 END IF;
 PERFORM u.id FROM {{MONI_SCHEMA}}.users u WHERE u.id=p_actor FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_forbidden' USING ERRCODE='PP003'; END IF;
 SELECT u.platform_role,u.status INTO v_role,v_user_status FROM {{MONI_SCHEMA}}.users u WHERE u.id=p_actor;
 IF NOT FOUND OR v_role<>'admin' OR v_user_status<>'active' THEN
  RAISE EXCEPTION 'procurement_forbidden' USING ERRCODE='PP003';
 END IF;
 IF v_existing THEN
  PERFORM {{MONI_SCHEMA}}.pp_ab_validate_command_leaf_v1(p_operation);
  PERFORM {{MONI_SCHEMA}}.pp_validate_root(c.pp_root_id);
  RETURN QUERY SELECT 'replay'::text,c.operation_id,c.actor,c.kind,c.target_id,c.status,c.reason,c.before_state,c.after_state,c.created_at,c.pp_root_id,c.pp_account_journal_id;
  RETURN;
 END IF;
 v_root:=pg_catalog.gen_random_uuid()::text;
 v_source:=pg_catalog.gen_random_uuid()::text;
 v_journal:=pg_catalog.gen_random_uuid()::text;
 v_audit:=pg_catalog.gen_random_uuid()::text;
 v_xid:=pg_catalog.pg_current_xact_id();
 IF p_kind='account_create' THEN
  v_target:=pg_catalog.gen_random_uuid()::text;
  v_journal_kind:='create';
  v_present:=false;
  v_block:=0;
 ELSE
  v_target:=p_target;
  PERFORM pa.id FROM {{MONI_SCHEMA}}.procurement_accounts pa WHERE pa.id=p_target FOR NO KEY UPDATE;
  v_present:=FOUND;
  IF v_present THEN
   PERFORM ph.account_id FROM {{MONI_SCHEMA}}.pp_account_heads ph WHERE ph.account_id=p_target FOR UPDATE;
   -- The new statement follows every account/head wait and accepts the full
   -- certified command+reserve prefix, never a command-only approximation.
   PERFORM {{MONI_SCHEMA}}.pp_validate_account(p_target);
   SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts pa WHERE pa.id=p_target;
   IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads ph WHERE ph.account_id=p_target;
   IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   SELECT pg_catalog.count(*) INTO v_block FROM {{MONI_SCHEMA}}.procurement_reservations pr WHERE pr.account_id=p_target AND pr.anomaly AND pr.state IN ('unknown','disputed');
   IF (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,v_block) IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count) THEN
    RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
   END IF;
   b:=a;
   IF h.ordinal=9223372036854775807 THEN RAISE EXCEPTION 'procurement_history_exhausted' USING ERRCODE='PP905'; END IF;
   v_before:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(a.id,a.label,a.provider,a.state,a.limit_amount,a.held_amount,a.spent_amount,a.revision,a.created_at);
   IF a.revision<>p_expected THEN
    v_status:='rejected';
    v_reason:='revision_conflict';
    v_journal_kind:='configure_rejected';
   ELSE
    v_journal_kind:='configure';
    IF a.revision=9223372036854775807 AND (a.label,a.provider,a.state,a.limit_amount) IS DISTINCT FROM (p_label,p_provider,p_state,p_limit) THEN
     RAISE EXCEPTION 'procurement_history_exhausted' USING ERRCODE='PP905';
    END IF;
   END IF;
  ELSE
   v_status:='rejected';
   v_reason:='not_found';
   v_journal:=NULL;
  END IF;
 END IF;
 -- This independent database time belongs to the original command. The
 -- command BEFORE guard derives created_at from this complete typed source.
 v_time:=pg_catalog.clock_timestamp();
 INSERT INTO {{MONI_SCHEMA}}.pp_account_command_sources(id,root_id,step_no,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id)
 VALUES(v_source,v_root,1,v_xid,pg_catalog.clock_timestamp(),p_operation,p_actor,v_role,v_user_status,p_kind,p_target,v_target,p_label,p_provider,p_state,p_limit,v_digest,v_status,v_reason,v_time,v_audit,p_expected,v_journal);
 IF p_kind='account_create' THEN
  INSERT INTO {{MONI_SCHEMA}}.procurement_accounts(id,label,provider,state,limit_amount,pp_version,pp_created_xid,pp_origin_journal_id)
  VALUES(v_target,p_label,p_provider,p_state,p_limit,1,v_xid,v_journal) RETURNING * INTO a;
  IF NOT FOUND OR (a.id,a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,a.pp_version,a.pp_created_xid,a.pp_origin_journal_id) IS DISTINCT FROM (v_target,p_label,p_provider,p_state,p_limit,1::bigint,0::numeric,0::numeric,1::smallint,v_xid,v_journal) OR NOT pg_catalog.isfinite(a.created_at) THEN
   RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
  END IF;
 ELSIF v_status='applied' THEN
  UPDATE {{MONI_SCHEMA}}.procurement_accounts pa SET label=p_label,provider=p_provider,state=p_state,limit_amount=p_limit WHERE pa.id=v_target RETURNING * INTO a;
  IF NOT FOUND OR (a.id,a.label,a.provider,a.state,a.limit_amount,a.held_amount,a.spent_amount,a.created_at,a.pp_version,a.pp_created_xid,a.pp_origin_journal_id) IS DISTINCT FROM (b.id,p_label,p_provider,p_state,p_limit,b.held_amount,b.spent_amount,b.created_at,b.pp_version,b.pp_created_xid,b.pp_origin_journal_id) OR a.revision::numeric IS DISTINCT FROM b.revision::numeric+(CASE WHEN (b.label,b.provider,b.state,b.limit_amount) IS DISTINCT FROM (p_label,p_provider,p_state,p_limit) THEN 1 ELSE 0 END) THEN
   RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
  END IF;
 END IF;
 IF v_status='applied' THEN
  v_after:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(a.id,a.label,a.provider,a.state,a.limit_amount,a.held_amount,a.spent_amount,a.revision,a.created_at);
 END IF;
 IF v_journal IS NOT NULL THEN
  INSERT INTO {{MONI_SCHEMA}}.pp_account_journal(id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,command_source_id)
  VALUES(v_journal,v_target,coalesce(h.ordinal,0)+1,h.journal_id,v_root,1,'account_command',v_xid,pg_catalog.clock_timestamp(),v_journal_kind,'command',p_operation,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count,a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,v_block,v_source);
  IF p_kind='account_create' THEN
   INSERT INTO {{MONI_SCHEMA}}.pp_account_origins(account_id,version,create_operation_id,command_source_id,root_id,step_no,journal_id,created_xid,created_at)
   VALUES(v_target,1,p_operation,v_source,v_root,1,v_journal,v_xid,a.created_at);
   INSERT INTO {{MONI_SCHEMA}}.pp_account_heads(account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count)
   VALUES(v_target,1,v_journal,v_xid,a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,v_block);
  ELSE
   UPDATE {{MONI_SCHEMA}}.pp_account_heads ph SET ordinal=h.ordinal+1,journal_id=v_journal,updated_xid=v_xid,label=a.label,provider=a.provider,state=a.state,limit_amount=a.limit_amount,configuration_revision=a.revision,held_amount=a.held_amount,spent_amount=a.spent_amount,blocking_count=v_block
   WHERE ph.account_id=v_target AND ph.ordinal=h.ordinal AND ph.journal_id=h.journal_id;
   GET DIAGNOSTICS v_rows=ROW_COUNT;
   IF v_rows<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
 END IF;
 INSERT INTO {{MONI_SCHEMA}}.procurement_commands(operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id)
 VALUES(p_operation,p_actor,p_kind,v_target,v_digest,v_status,v_reason,v_before,v_after,v_source,v_root,1,v_xid,v_journal) RETURNING * INTO c;
 IF NOT FOUND OR (c.operation_id,c.actor,c.kind,c.target_id,c.input_digest,c.status,c.reason,c.before_state,c.after_state,c.created_at,c.pp_source_id,c.pp_root_id,c.pp_root_step,c.pp_created_xid,c.pp_account_journal_id) IS DISTINCT FROM (p_operation,p_actor,p_kind,v_target,v_digest,v_status,v_reason,v_before,v_after,v_time,v_source,v_root,1::bigint,v_xid,v_journal) THEN
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 INSERT INTO {{MONI_SCHEMA}}.audit_events(id,actor,action,target,sanitized_diff,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid)
 VALUES(v_audit,p_actor,'procurement.command.'||v_status,p_operation,pg_catalog.jsonb_build_object('kind',p_kind,'status',v_status),v_root,1,'account_command',v_source,'account_command',v_xid);
 INSERT INTO {{MONI_SCHEMA}}.pp_root_steps(root_id,step_no,kind,source_id,created_xid)
 VALUES(v_root,1,'account_command',v_source,v_xid);
 -- The only seal is the final mutation. Nothing after this INSERT writes.
 INSERT INTO {{MONI_SCHEMA}}.pp_operation_roots(id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result)
 VALUES(v_root,p_kind,'account_command',NULL,NULL,NULL,v_target,v_xid,{{MONI_APP_OID}}::oid,1,v_status);
 PERFORM {{MONI_SCHEMA}}.pp_validate_root(v_root);
 RETURN QUERY SELECT 'applied'::text,c.operation_id,c.actor,c.kind,c.target_id,c.status,c.reason,c.before_state,c.after_state,c.created_at,c.pp_root_id,c.pp_account_journal_id;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_create_account(p_actor text,p_operation text,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS TABLE(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY DEFINER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE v_rows bigint;
BEGIN
 RETURN QUERY SELECT * FROM {{MONI_SCHEMA}}.pp_ab_account_mutate_v1('account_create',p_actor,p_operation,'',NULL,p_label,p_provider,p_state,p_limit);
 GET DIAGNOSTICS v_rows=ROW_COUNT;
 IF v_rows<>1 THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_configure_account(p_actor text,p_operation text,p_account text,p_expected bigint,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS TABLE(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY DEFINER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE v_rows bigint;
BEGIN
 RETURN QUERY SELECT * FROM {{MONI_SCHEMA}}.pp_ab_account_mutate_v1('account_configure',p_actor,p_operation,p_account,p_expected,p_label,p_provider,p_state,p_limit);
 GET DIAGNOSTICS v_rows=ROW_COUNT;
 IF v_rows<>1 THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903'; END IF;
END $$;

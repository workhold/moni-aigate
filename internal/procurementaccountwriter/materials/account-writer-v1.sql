-- Isolated account-command writer, not a serving migration or full PP writer.
-- Only trusted schema identifier and decimal actual APP/W role OIDs are rendered.
-- Owners, REVOKE/GRANT and role provisioning belong to a separate owner script.

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_value text,p_nonzero boolean)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
 SELECT coalesce(p_value ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND (NOT p_nonzero OR p_value<>'00000000-0000-0000-0000-000000000000'),false)
$$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_text_v1(p_value text)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE i integer; n integer; ch integer;
BEGIN
 IF p_value IS NULL OR pg_catalog.octet_length(p_value) NOT BETWEEN 1 AND 100 THEN RETURN false; END IF;
 n:=pg_catalog.char_length(p_value);
 FOR i IN 1..n LOOP
  ch:=pg_catalog.ascii(pg_catalog.substr(p_value,i,1));
  IF ch BETWEEN 0 AND 31 OR ch BETWEEN 127 AND 159 THEN RETURN false; END IF;
  IF i IN (1,n) AND (ch BETWEEN 9 AND 13 OR ch IN (32,133,160,5760,8232,8233,8239,8287,12288) OR ch BETWEEN 8192 AND 8202) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE; a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE; v_root text; v_xid xid8:=pg_catalog.pg_current_xact_id(); v_count bigint; v_json jsonb;
BEGIN
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP IN ('DELETE','TRUNCATE') OR (TG_OP='UPDATE' AND TG_TABLE_NAME<>'pp_account_heads') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 v_json:=pg_catalog.to_jsonb(NEW);
 IF TG_TABLE_NAME='pp_account_heads' THEN
  SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id;
  IF NOT FOUND OR j.created_xid<>v_xid OR (NEW.account_id,NEW.ordinal,NEW.updated_xid,NEW.label,NEW.provider,NEW.state,NEW.limit_amount,NEW.configuration_revision,NEW.held_amount,NEW.spent_amount,NEW.blocking_count) IS DISTINCT FROM (j.account_id,j.ordinal,j.created_xid,j.after_label,j.after_provider,j.after_state,j.after_limit_amount,j.after_configuration_revision,j.after_held_amount,j.after_spent_amount,j.after_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF TG_OP='INSERT' THEN
   IF j.kind<>'create' OR j.ordinal<>1 OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=NEW.account_id AND journal_id=j.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF OLD.account_id<>NEW.account_id OR OLD.ordinal=9223372036854775807 OR NEW.ordinal<>OLD.ordinal+1 OR j.previous_id IS DISTINCT FROM OLD.journal_id OR (OLD.label,OLD.provider,OLD.state,OLD.limit_amount,OLD.configuration_revision,OLD.held_amount,OLD.spent_amount,OLD.blocking_count) IS DISTINCT FROM (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_root:=j.root_id;
 ELSE
  IF TG_TABLE_NAME='pp_operation_roots' THEN v_root:=NEW.id; ELSE v_root:=v_json->>'root_id'; END IF;
  IF (v_json->>'created_xid')::xid8 IS DISTINCT FROM v_xid THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=v_root) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF TG_TABLE_NAME='pp_account_command_sources' THEN
  IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.root_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.audit_id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.operation_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.actor_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.target_id,NEW.kind='account_create') OR NEW.step_no<>1 OR NEW.kind NOT IN ('account_create','account_configure') OR NOT pg_catalog.isfinite(NEW.created_at) OR NOT pg_catalog.isfinite(NEW.command_created_at) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(NEW.input_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(NEW.input_provider) OR NEW.input_state NOT IN ('active','paused') OR NEW.input_limit_amount<0 OR NEW.actor_role<>'admin' OR NEW.actor_status<>'active' OR NEW.input_digest IS DISTINCT FROM {{MONI_SCHEMA}}.pp_ac_command_digest_v1(NEW.kind,NEW.operation_id,NEW.input_target_id,NEW.input_expected_revision,NEW.input_label,NEW.input_provider,NEW.input_state,NEW.input_limit_amount) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=NEW.operation_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE id=NEW.audit_id) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.users WHERE id=NEW.actor_id AND platform_role='admin' AND status='active') THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF NEW.journal_id IS NOT NULL AND NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.journal_id,true) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.target_id;
  IF NEW.kind='account_create' THEN
   IF FOUND OR NEW.input_target_id<>'' OR NEW.input_expected_revision IS NOT NULL OR NEW.command_status<>'applied' OR NEW.command_reason<>'' OR NEW.journal_id IS NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF NEW.input_target_id<>NEW.target_id OR NEW.input_expected_revision IS NULL OR NEW.input_expected_revision<=0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
  ELSIF NOT FOUND THEN
   IF NEW.command_status<>'rejected' OR NEW.command_reason<>'not_found' OR NEW.journal_id IS NOT NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   PERFORM {{MONI_SCHEMA}}.pp_ac_validate_account_v1(NEW.target_id);
   IF NEW.journal_id IS NULL OR (a.revision=NEW.input_expected_revision AND (NEW.command_status<>'applied' OR NEW.command_reason<>'')) OR (a.revision<>NEW.input_expected_revision AND (NEW.command_status<>'rejected' OR NEW.command_reason<>'revision_conflict')) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
 ELSIF TG_TABLE_NAME IN ('pp_root_steps','pp_operation_roots') THEN
  SELECT pg_catalog.count(*) INTO v_count FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root;
  IF v_count<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=v_root;
  IF TG_TABLE_NAME='pp_root_steps' THEN
   IF (NEW.step_no,NEW.kind,NEW.source_id,NEW.created_xid) IS DISTINCT FROM (1::bigint,'account_command'::text,s.id,s.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF (NEW.entry_kind,NEW.scope_kind,NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.account_target_id,NEW.created_xid,NEW.session_role_oid,NEW.step_count,NEW.semantic_result) IS DISTINCT FROM (s.kind,'account_command'::text,NULL::text,NULL::text,NULL::text,s.target_id,s.created_xid,{{MONI_APP_OID}}::oid,1::bigint,s.command_status) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=s.operation_id AND pp_source_id=s.id) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE id=s.audit_id AND pp_source_id=s.id) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=v_root AND step_no=1 AND source_id=s.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME='pp_account_origins' THEN
  SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.command_source_id;
  IF NOT FOUND OR s.kind<>'account_create' OR (NEW.version,NEW.account_id,NEW.create_operation_id,NEW.root_id,NEW.step_no,NEW.journal_id,NEW.created_xid) IS DISTINCT FROM (1::smallint,s.target_id,s.operation_id,s.root_id,1::bigint,s.journal_id,s.created_xid) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.account_id;
  IF NOT FOUND OR NEW.created_at IS DISTINCT FROM a.created_at OR a.pp_origin_journal_id IS DISTINCT FROM NEW.journal_id OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=NEW.journal_id AND kind='create' AND command_source_id=s.id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME='pp_account_journal' THEN
  SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.command_source_id;
  IF NOT FOUND OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(NEW.id,true) OR NEW.source_kind<>'command' OR (NEW.id,NEW.root_id,NEW.step_no,NEW.step_kind,NEW.created_xid,NEW.account_id,NEW.source_id) IS DISTINCT FROM (s.journal_id,s.root_id,1::bigint,'account_command'::text,s.created_xid,s.target_id,s.operation_id) OR NOT pg_catalog.isfinite(NEW.created_at) OR (s.kind='account_create' AND NEW.kind<>'create') OR (s.kind='account_configure' AND NEW.kind<>(CASE s.command_status WHEN 'applied' THEN 'configure' ELSE 'configure_rejected' END)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=NEW.account_id;
  IF NOT FOUND OR (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount) IS DISTINCT FROM (NEW.after_label,NEW.after_provider,NEW.after_state,NEW.after_limit_amount,NEW.after_configuration_revision,NEW.after_held_amount,NEW.after_spent_amount) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=NEW.account_id;
  IF NEW.kind='create' THEN
   IF FOUND OR NEW.ordinal<>1 OR NEW.previous_id IS NOT NULL OR NEW.after_configuration_revision<>1 OR NEW.after_held_amount<>0 OR NEW.after_spent_amount<>0 OR NEW.after_blocking_count<>0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF NOT FOUND OR h.ordinal=9223372036854775807 OR NEW.ordinal<>h.ordinal+1 OR NEW.previous_id IS DISTINCT FROM h.journal_id OR (NEW.before_label,NEW.before_provider,NEW.before_state,NEW.before_limit_amount,NEW.before_configuration_revision,NEW.before_held_amount,NEW.before_spent_amount,NEW.before_blocking_count) IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF TG_TABLE_NAME<>'pp_account_heads' THEN RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_account_write_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; v_count bigint;
BEGIN
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP NOT IN ('INSERT','UPDATE') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT pg_catalog.count(*) INTO v_count FROM {{MONI_SCHEMA}}.pp_account_command_sources x WHERE x.target_id=NEW.id AND x.created_xid=pg_catalog.pg_current_xact_id() AND x.command_status='applied' AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal y WHERE y.id=x.journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots y WHERE y.id=x.root_id);
 IF v_count<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources x WHERE x.target_id=NEW.id AND x.created_xid=pg_catalog.pg_current_xact_id() AND x.command_status='applied' AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal y WHERE y.id=x.journal_id) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots y WHERE y.id=x.root_id);
 IF (NEW.label,NEW.provider,NEW.state,NEW.limit_amount) IS DISTINCT FROM (s.input_label,s.input_provider,s.input_state,s.input_limit_amount) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF TG_OP='INSERT' THEN
  IF s.kind<>'account_create' OR (NEW.pp_version,NEW.pp_created_xid,NEW.pp_origin_journal_id,NEW.revision,NEW.held_amount,NEW.spent_amount) IS DISTINCT FROM (1::smallint,s.created_xid,s.journal_id,1::bigint,0::numeric,0::numeric) OR NOT pg_catalog.isfinite(NEW.created_at) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF s.kind<>'account_configure' OR OLD.revision<>s.input_expected_revision OR (NEW.id,NEW.created_at,NEW.pp_version,NEW.pp_created_xid,NEW.pp_origin_journal_id,NEW.held_amount,NEW.spent_amount,NEW.revision) IS DISTINCT FROM (OLD.id,OLD.created_at,OLD.pp_version,OLD.pp_created_xid,OLD.pp_origin_journal_id,OLD.held_amount,OLD.spent_amount,OLD.revision) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_account_result_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.held_amount<>0 OR NEW.spent_amount<>0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSIF NEW.revision::numeric<>OLD.revision::numeric+(CASE WHEN (NEW.label,NEW.provider,NEW.state,NEW.limit_amount) IS DISTINCT FROM (OLD.label,OLD.provider,OLD.state,OLD.limit_amount) THEN 1 ELSE 0 END) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_command_write_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF NEW.kind IN ('credential_bind','balance_observe') THEN
  IF NEW.pp_source_id IS NOT NULL OR NEW.pp_root_id IS NOT NULL OR NEW.pp_root_step IS NOT NULL OR NEW.pp_created_xid IS NOT NULL OR NEW.pp_account_journal_id IS NOT NULL OR current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  RETURN NEW;
 END IF;
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.pp_source_id;
 IF NOT FOUND OR (NEW.operation_id,NEW.actor,NEW.kind,NEW.target_id,NEW.input_digest,NEW.status,NEW.reason,NEW.pp_root_id,NEW.pp_root_step,NEW.pp_created_xid,NEW.pp_account_journal_id) IS DISTINCT FROM (s.operation_id,s.actor_id,s.kind,s.target_id,s.input_digest,s.command_status,s.command_reason,s.root_id,1::bigint,s.created_xid,s.journal_id) OR s.created_xid<>pg_catalog.pg_current_xact_id() OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=s.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 NEW.created_at:=s.command_created_at;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_audit_write_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; v_kind text;
BEGIN
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP<>'INSERT' THEN
  IF OLD.pp_root_id IS NOT NULL OR current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  RETURN OLD;
 END IF;
 IF NEW.pp_root_id IS NULL AND NEW.pp_root_step IS NULL AND NEW.pp_source_kind IS NULL AND NEW.pp_source_id IS NULL AND NEW.pp_source_role IS NULL AND NEW.pp_created_xid IS NULL THEN
  IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  IF NEW.action IN ('procurement.command.applied','procurement.command.rejected') THEN
   SELECT kind INTO v_kind FROM {{MONI_SCHEMA}}.procurement_commands WHERE operation_id=NEW.target;
   IF v_kind IS NULL OR v_kind NOT IN ('credential_bind','balance_observe') THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE id=NEW.pp_source_id;
 IF NOT FOUND OR (NEW.id,NEW.actor,NEW.action,NEW.target,NEW.tenant_scope,NEW.request_id,NEW.sanitized_diff,NEW.pp_root_id,NEW.pp_root_step,NEW.pp_source_kind,NEW.pp_source_role,NEW.pp_created_xid) IS DISTINCT FROM (s.audit_id,s.actor_id,'procurement.command.'||s.command_status,s.operation_id,NULL::text,''::text,pg_catalog.jsonb_build_object('kind',s.kind,'status',s.command_status),s.root_id,1::bigint,'account_command'::text,'account_command'::text,s.created_xid) OR s.created_xid<>pg_catalog.pg_current_xact_id() OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=s.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_parent_write_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF current_user::regrole::oid={{MONI_WRITER_OID}}::oid THEN RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1()
RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE v_json jsonb:=pg_catalog.to_jsonb(NEW); v_root text;
BEGIN
 IF TG_TABLE_NAME='procurement_accounts' THEN
  PERFORM {{MONI_SCHEMA}}.pp_ac_validate_account_v1(NEW.id);
 ELSIF TG_TABLE_NAME='pp_account_heads' THEN
  PERFORM {{MONI_SCHEMA}}.pp_ac_validate_account_v1(NEW.account_id);
 ELSIF TG_TABLE_NAME='procurement_commands' THEN
  IF NEW.kind IN ('account_create','account_configure') THEN PERFORM {{MONI_SCHEMA}}.pp_ac_validate_root_v1(NEW.pp_root_id); END IF;
 ELSIF TG_TABLE_NAME='audit_events' THEN
  IF NEW.pp_root_id IS NOT NULL THEN PERFORM {{MONI_SCHEMA}}.pp_ac_validate_root_v1(NEW.pp_root_id); END IF;
 ELSE
  IF TG_TABLE_NAME='pp_operation_roots' THEN v_root:=NEW.id; ELSE v_root:=v_json->>'root_id'; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_ac_validate_root_v1(v_root);
 END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_command_digest_v1(p_kind text,p_operation text,p_target text,p_expected bigint,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS text LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE v_payload text;
BEGIN
 IF p_kind NOT IN ('account_create','account_configure') OR p_operation IS NULL OR p_target IS NULL OR p_label IS NULL OR p_provider IS NULL OR p_state IS NULL OR p_limit IS NULL OR (p_kind='account_configure' AND p_expected IS NULL) THEN RETURN NULL; END IF;
 v_payload:='{"operation_id":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_operation);
 IF p_kind='account_configure' THEN v_payload:=v_payload||',"expected_revision":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_expected::text); END IF;
 v_payload:=v_payload||',"label":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_label)||',"provider":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_provider)||',"state":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_state)||',"limit_amount":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_limit::text)||'}';
 RETURN pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to('{"Kind":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_kind)||',"Target":'||{{MONI_SCHEMA}}.pp_json_string_v1(p_target)||',"Payload":'||v_payload||'}','UTF8')),'hex');
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_time_v1(p_time timestamptz)
RETURNS text LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF p_time IS NULL OR NOT pg_catalog.isfinite(p_time) OR extract(year FROM p_time AT TIME ZONE 'UTC') NOT BETWEEN 1 AND 9999 THEN
  RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902';
 END IF;
 RETURN pg_catalog.rtrim(pg_catalog.rtrim(pg_catalog.to_char(p_time AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US'),'0'),'.')||'Z';
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_snapshot_v1(p_id text,p_label text,p_provider text,p_state text,p_limit bigint,p_held numeric,p_spent numeric,p_revision bigint,p_time timestamptz)
RETURNS jsonb LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_id,false) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_provider) OR p_state IS NULL OR p_state NOT IN ('active','paused') OR p_limit IS NULL OR p_limit<0 OR p_revision IS NULL OR p_revision<=0 OR p_held IS NULL OR p_spent IS NULL OR p_held::text !~ '^(0|[1-9][0-9]{0,99})$' OR p_spent::text !~ '^(0|[1-9][0-9]{0,99})$' THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 RETURN pg_catalog.jsonb_build_object('id',p_id,'label',p_label,'provider',p_provider,'state',p_state,'limit_amount',p_limit::text,'held_amount',p_held::text,'spent_amount',p_spent::text,'revision',p_revision::text,'created_at',{{MONI_SCHEMA}}.pp_ac_time_v1(p_time));
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_require_writer_v1()
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 IF current_user::regrole::oid<>{{MONI_WRITER_OID}}::oid OR session_user::regrole::oid<>{{MONI_APP_OID}}::oid THEN
  RAISE EXCEPTION 'procurement_execution_forbidden' USING ERRCODE='PP901';
 END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_require_root_v1()
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ac_require_writer_v1();
 IF pg_catalog.current_setting('transaction_isolation')<>'read committed' OR pg_catalog.current_setting('statement_timeout')::interval<>interval '5 seconds' OR pg_catalog.current_setting('lock_timeout')::interval<>interval '3 seconds' THEN
  RAISE EXCEPTION 'procurement_execution_protocol' USING ERRCODE='PP903';
 END IF;
END $$;

-- Readonly validators deliberately use no row locks or mutation helpers.
CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_validate_command_v1(p_operation text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE c {{MONI_SCHEMA}}.procurement_commands%ROWTYPE; s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE; r {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; a {{MONI_SCHEMA}}.audit_events%ROWTYPE; v_time timestamptz; v_before jsonb; v_after jsonb;
BEGIN
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
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=s.root_id)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=s.root_id)<>(CASE WHEN s.kind='account_create' THEN 1 ELSE 0 END) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.projection_outbox WHERE pp_root_id=s.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF EXISTS(
  SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_delivery_sources WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_incoming_sources WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_meter_choices WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_meter_origins WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_conflict_sources WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_decisions WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_normal_cost_decisions WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_cost_mutations WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_cost_reset_decisions WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_evidence_choices WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_evidence_members WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_proposal_births WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_approval_consumptions WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_unmanaged_correction_sources WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_recovery_sources WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_cost_outbox_links WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_reconciliation_sources WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_customer_finish_outcomes WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects WHERE root_id=s.root_id UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_customer_finish_pb_effects WHERE root_id=s.root_id
 ) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO a FROM {{MONI_SCHEMA}}.audit_events WHERE id=s.audit_id;
 IF NOT FOUND OR (a.actor,a.action,a.target,a.tenant_scope,a.request_id,a.sanitized_diff,a.pp_root_id,a.pp_root_step,a.pp_source_kind,a.pp_source_id,a.pp_source_role,a.pp_created_xid) IS DISTINCT FROM (s.actor_id,'procurement.command.'||s.command_status,s.operation_id,NULL::text,''::text,pg_catalog.jsonb_build_object('kind',s.kind,'status',s.command_status),s.root_id,1::bigint,'account_command'::text,s.id,'account_command'::text,s.created_xid) OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.audit_events WHERE pp_root_id=s.root_id)<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 v_before:='null'::jsonb; v_after:='null'::jsonb;
 IF s.journal_id IS NULL THEN
  IF s.kind<>'account_configure' OR s.input_target_id<>s.target_id OR s.input_expected_revision IS NULL OR s.input_expected_revision<=0 OR s.command_status<>'rejected' OR s.command_reason<>'not_found' OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE command_source_id=s.id OR root_id=s.root_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 ELSE
  SELECT * INTO j FROM {{MONI_SCHEMA}}.pp_account_journal WHERE id=s.journal_id;
  IF NOT FOUND OR (j.account_id,j.root_id,j.step_no,j.step_kind,j.created_xid,j.source_kind,j.source_id,j.command_source_id) IS DISTINCT FROM (s.target_id,s.root_id,1::bigint,'account_command'::text,s.created_xid,'command'::text,s.operation_id,s.id) OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=s.root_id)<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  SELECT created_at INTO v_time FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=s.target_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF s.kind='account_create' THEN
   IF j.kind<>'create' OR j.ordinal<>1 OR s.command_status<>'applied' OR s.command_reason<>'' OR s.input_target_id<>'' OR s.input_expected_revision IS NOT NULL THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSE
   IF s.input_target_id<>s.target_id OR s.input_expected_revision IS NULL OR s.input_expected_revision<=0 OR j.kind NOT IN ('configure','configure_rejected') THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   v_before:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(j.account_id,j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_held_amount,j.before_spent_amount,j.before_configuration_revision,v_time);
   IF (j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (j.after_held_amount,j.after_spent_amount,j.after_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  END IF;
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

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_validate_account_v1(p_account text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE; o {{MONI_SCHEMA}}.pp_account_origins%ROWTYPE; h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE; j {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; prev {{MONI_SCHEMA}}.pp_account_journal%ROWTYPE; v_count bigint:=0; v_held numeric; v_spent numeric; v_bheld numeric; v_bspent numeric; v_block bigint;
BEGIN
 SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts WHERE id=p_account;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF a.pp_version IS NULL AND a.pp_created_xid IS NULL AND a.pp_origin_journal_id IS NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=p_account) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=p_account) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE account_id=p_account) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE target_id=p_account AND journal_id IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE target_id=p_account AND pp_source_id IS NOT NULL) THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
 SELECT * INTO o FROM {{MONI_SCHEMA}}.pp_account_origins WHERE account_id=p_account;
 IF NOT FOUND OR (a.pp_version,a.pp_created_xid,a.pp_origin_journal_id,a.created_at) IS DISTINCT FROM (o.version,o.created_xid,o.journal_id,o.created_at) OR o.version<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads WHERE account_id=p_account;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 FOR j IN SELECT * FROM {{MONI_SCHEMA}}.pp_account_journal WHERE account_id=p_account ORDER BY ordinal LOOP
  IF j.source_kind IN ('budget_event','dispatch') THEN
   IF NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(j.id,true) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(j.root_id,true) OR j.created_xid='0'::xid8 OR NOT pg_catalog.isfinite(j.created_at) OR j.step_no<=0 OR j.ordinal<=0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   -- An unsupported domain must not hide a definitely missing or misbound source.
   IF NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_operation_roots r WHERE r.id=j.root_id AND r.scope_kind='request' AND r.created_xid=j.created_xid AND r.session_role_oid={{MONI_APP_OID}}::oid) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps st WHERE (st.root_id,st.step_no,st.kind,st.source_id,st.created_xid)=(j.root_id,j.step_no,j.step_kind,coalesce(j.transition_id,j.delivery_source_id),j.created_xid)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   IF j.source_kind='budget_event' THEN
    IF j.kind NOT IN ('reserve','released_no_consumption','consumption_unknown','cost_confirmed','evidence_disputed','expired_without_permit','recovered_unknown','cost_corrected') OR j.step_kind<>'procurement_transition' OR j.command_source_id IS NOT NULL OR j.delivery_source_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions t WHERE (t.id,t.event_id,t.account_id,t.attempt_id,t.root_id,t.step_no,t.step_kind,t.created_xid,t.journal_id)=(j.transition_id,j.source_id,j.account_id,j.attempt_id,j.root_id,j.step_no,j.step_kind,j.created_xid,j.id)) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events e WHERE (e.id,e.account_id,e.attempt_id,e.pp_transition_id,e.pp_journal_id,e.pp_root_id,e.pp_root_step,e.pp_created_xid)=(j.source_id,j.account_id,j.attempt_id,j.transition_id,j.id,j.root_id,j.step_no,j.created_xid)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   ELSE
    IF j.kind<>'dispatch' OR j.step_kind<>'attempt_delivery' OR j.command_source_id IS NOT NULL OR j.transition_id IS NOT NULL OR j.attempt_id IS DISTINCT FROM j.source_id OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_delivery_sources d WHERE (d.id,d.root_id,d.step_no,d.step_kind,d.created_xid,d.attempt_id,d.mode,d.dispatch_account_id,d.dispatch_journal_id,d.dispatch_xid,d.pb_dispatch_checked_at)=(j.delivery_source_id,j.root_id,j.step_no,j.step_kind,j.created_xid,j.attempt_id,'dispatch_permit'::text,j.account_id,j.id,j.created_xid,j.pb_decision_at)) OR NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.upstream_attempts u WHERE (u.id,u.pp_dispatch_source_id,u.pp_dispatch_root_id,u.pp_dispatch_root_step,u.pp_dispatch_xid,u.pp_dispatch_account_id,u.pp_dispatch_journal_id,u.pb_dispatch_checked_at)=(j.attempt_id,j.delivery_source_id,j.root_id,j.step_no,j.created_xid,j.account_id,j.id,j.pb_decision_at)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
   END IF;
   RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
  END IF;
  IF j.source_kind<>'command' OR j.kind NOT IN ('create','configure','configure_rejected') OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(j.id,true) OR NOT pg_catalog.isfinite(j.created_at) OR j.created_xid='0'::xid8 OR j.step_no<>1 OR j.step_kind<>'account_command' OR j.attempt_id IS NOT NULL OR j.binding_id IS NOT NULL OR j.before_reservation_revision IS NOT NULL OR j.before_reservation_state IS NOT NULL OR j.before_reservation_held_amount IS NOT NULL OR j.before_reservation_spent_amount IS NOT NULL OR j.before_reservation_anomaly IS NOT NULL OR j.after_reservation_revision IS NOT NULL OR j.after_reservation_state IS NOT NULL OR j.after_reservation_held_amount IS NOT NULL OR j.after_reservation_spent_amount IS NOT NULL OR j.after_reservation_anomaly IS NOT NULL OR j.input_bound IS NOT NULL OR j.output_bound IS NOT NULL OR j.upper_amount IS NOT NULL OR j.cost_version_id IS NOT NULL OR j.cost_input_rate IS NOT NULL OR j.cost_output_rate IS NOT NULL OR j.cost_basis IS NOT NULL OR j.gate_extra IS NOT NULL OR j.gate_checked_at IS NOT NULL OR j.pb_decision_at IS NOT NULL OR j.pause_event_id IS NOT NULL OR j.transition_id IS NOT NULL OR j.delivery_source_id IS NOT NULL OR j.command_source_id IS NULL OR j.command_operation_id IS DISTINCT FROM j.source_id OR j.procurement_event_id IS NOT NULL OR j.dispatch_attempt_id IS NOT NULL OR j.previous_ordinal<>j.ordinal-1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_count=9223372036854775807 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  v_count:=v_count+1;
  IF j.ordinal<>v_count THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF v_count=1 THEN
   IF (j.id,j.previous_id,j.kind,j.root_id,j.step_no,j.command_source_id,j.source_id,j.created_xid) IS DISTINCT FROM (o.journal_id,NULL::text,'create'::text,o.root_id,o.step_no,o.command_source_id,o.create_operation_id,o.created_xid) OR j.before_label IS NOT NULL OR j.before_provider IS NOT NULL OR j.before_state IS NOT NULL OR j.before_limit_amount IS NOT NULL OR j.before_configuration_revision IS NOT NULL OR j.before_held_amount IS NOT NULL OR j.before_spent_amount IS NOT NULL OR j.before_blocking_count IS NOT NULL OR j.after_configuration_revision<>1 OR j.after_held_amount<>0 OR j.after_spent_amount<>0 OR j.after_blocking_count<>0 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  ELSIF j.previous_id IS DISTINCT FROM prev.id OR (j.before_label,j.before_provider,j.before_state,j.before_limit_amount,j.before_configuration_revision,j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (prev.after_label,prev.after_provider,prev.after_state,prev.after_limit_amount,prev.after_configuration_revision,prev.after_held_amount,prev.after_spent_amount,prev.after_blocking_count) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  IF j.ordinal>1 AND (j.after_configuration_revision::numeric<>j.before_configuration_revision::numeric+(CASE WHEN (j.before_label,j.before_provider,j.before_state,j.before_limit_amount) IS DISTINCT FROM (j.after_label,j.after_provider,j.after_state,j.after_limit_amount) THEN 1 ELSE 0 END) OR (j.before_held_amount,j.before_spent_amount,j.before_blocking_count) IS DISTINCT FROM (j.after_held_amount,j.after_spent_amount,j.after_blocking_count)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  PERFORM {{MONI_SCHEMA}}.pp_ac_validate_command_v1(j.source_id);
  prev:=j;
 END LOOP;
 IF v_count=0 OR (h.ordinal,h.journal_id,h.updated_xid,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count) IS DISTINCT FROM (prev.ordinal,prev.id,prev.created_xid,prev.after_label,prev.after_provider,prev.after_state,prev.after_limit_amount,prev.after_configuration_revision,prev.after_held_amount,prev.after_spent_amount,prev.after_blocking_count) OR (a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount) IS DISTINCT FROM (h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT coalesce(pg_catalog.sum(held_amount),0),coalesce(pg_catalog.sum(spent_amount),0),pg_catalog.count(*) FILTER(WHERE anomaly AND state IN ('unknown','disputed')) INTO v_held,v_spent,v_block FROM {{MONI_SCHEMA}}.procurement_reservations WHERE account_id=p_account;
 SELECT coalesce(pg_catalog.sum(held_delta),0),coalesce(pg_catalog.sum(spent_delta),0) INTO v_bheld,v_bspent FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE account_id=p_account;
 IF (a.held_amount,a.spent_amount,h.blocking_count) IS DISTINCT FROM (v_held,v_spent,v_block) OR (a.held_amount,a.spent_amount) IS DISTINCT FROM (v_bheld,v_bspent) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 -- Account-command-only material cannot certify hidden reservation/event history.
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_reservations WHERE account_id=p_account) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_budget_events WHERE account_id=p_account) THEN RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906'; END IF;
 IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources s WHERE s.target_id=p_account AND s.journal_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal x WHERE x.id=s.journal_id AND x.command_source_id=s.id)) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_validate_root_v1(p_root text)
RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE r {{MONI_SCHEMA}}.pp_operation_roots%ROWTYPE; s {{MONI_SCHEMA}}.pp_account_command_sources%ROWTYPE;
BEGIN
 SELECT * INTO r FROM {{MONI_SCHEMA}}.pp_operation_roots WHERE id=p_root;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF r.entry_kind NOT IN ('account_create','account_configure') OR r.scope_kind<>'account_command' THEN
  IF EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=p_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=p_root AND source_kind='command') OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=p_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=p_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.audit_events WHERE pp_root_id=p_root AND (pp_source_kind='account_command' OR pp_source_role='account_command')) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=p_root AND kind='account_command') THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
  RAISE EXCEPTION 'procurement_prefix_untracked_domain' USING ERRCODE='PP906';
 END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=p_root)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_root_steps WHERE root_id=p_root)<>1 OR (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.procurement_commands WHERE pp_root_id=p_root)<>1 THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 SELECT * INTO s FROM {{MONI_SCHEMA}}.pp_account_command_sources WHERE root_id=p_root;
 PERFORM {{MONI_SCHEMA}}.pp_ac_validate_command_v1(s.operation_id);
 IF s.journal_id IS NOT NULL THEN PERFORM {{MONI_SCHEMA}}.pp_ac_validate_account_v1(s.target_id); END IF;
 IF (SELECT pg_catalog.count(*) FROM {{MONI_SCHEMA}}.pp_account_origins WHERE root_id=p_root)<>(CASE WHEN s.kind='account_create' THEN 1 ELSE 0 END) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.projection_outbox WHERE pp_root_id=p_root) OR EXISTS(SELECT 1 FROM {{MONI_SCHEMA}}.pp_account_journal WHERE root_id=p_root AND id IS DISTINCT FROM s.journal_id) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
 IF EXISTS(
  SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_origins WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_origins WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_nodes WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_attempt_delivery_sources WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_incoming_sources WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_meter_choices WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_meter_origins WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_conflict_sources WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_request_decisions WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_normal_cost_decisions WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_cost_mutations WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_cost_reset_decisions WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_evidence_choices WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_evidence_members WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_procurement_transitions WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_proposal_births WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_approval_consumptions WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_unmanaged_correction_sources WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_recovery_sources WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_cost_outbox_links WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_reconciliation_sources WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_customer_finish_outcomes WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_customer_finish_ledger_effects WHERE root_id=p_root UNION ALL SELECT 1 FROM {{MONI_SCHEMA}}.pp_customer_finish_pb_effects WHERE root_id=p_root
 ) THEN RAISE EXCEPTION 'procurement_history_invariant' USING ERRCODE='PP902'; END IF;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_ac_mutate_v1(p_kind text,p_actor text,p_operation text,p_target text,p_expected bigint,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS TABLE(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
DECLARE
 c {{MONI_SCHEMA}}.procurement_commands%ROWTYPE;
 a {{MONI_SCHEMA}}.procurement_accounts%ROWTYPE;
 h {{MONI_SCHEMA}}.pp_account_heads%ROWTYPE;
 v_digest text; v_role text; v_user_status text; v_existing boolean; v_present boolean;
 v_root text; v_source text; v_journal text; v_audit text; v_target text;
 v_status text:='applied'; v_reason text:=''; v_before jsonb:='null'::jsonb; v_after jsonb:='null'::jsonb;
 v_time timestamptz; v_xid xid8; v_journal_kind text; v_block bigint;
BEGIN
 PERFORM {{MONI_SCHEMA}}.pp_ac_require_root_v1();
 IF p_kind IS NULL OR p_kind NOT IN ('account_create','account_configure') OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_actor,false) OR NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_operation,false) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_label) OR NOT {{MONI_SCHEMA}}.pp_ac_text_v1(p_provider) OR p_state IS NULL OR p_state NOT IN ('active','paused') OR p_limit IS NULL OR p_limit<0 OR p_target IS NULL OR (p_kind='account_create' AND (p_target<>'' OR p_expected IS NOT NULL)) OR (p_kind='account_configure' AND (NOT {{MONI_SCHEMA}}.pp_ac_uuid_v1(p_target,false) OR p_expected IS NULL OR p_expected<=0)) THEN RAISE EXCEPTION 'invalid_procurement_configuration' USING ERRCODE='PP001'; END IF;
 v_digest:={{MONI_SCHEMA}}.pp_ac_command_digest_v1(p_kind,p_operation,p_target,p_expected,p_label,p_provider,p_state,p_limit);
 PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('procurement:'||p_operation,19019));
 SELECT * INTO c FROM {{MONI_SCHEMA}}.procurement_commands pc WHERE pc.operation_id=p_operation;
 v_existing:=FOUND;
 IF v_existing AND (c.actor<>p_actor OR c.input_digest<>v_digest) THEN RAISE EXCEPTION 'procurement_operation_conflict' USING ERRCODE='PP002'; END IF;
 SELECT u.platform_role,u.status INTO v_role,v_user_status FROM {{MONI_SCHEMA}}.users u WHERE u.id=p_actor FOR SHARE;
 IF NOT FOUND OR v_role<>'admin' OR v_user_status<>'active' THEN RAISE EXCEPTION 'procurement_forbidden' USING ERRCODE='PP003'; END IF;
 IF v_existing THEN
  PERFORM {{MONI_SCHEMA}}.pp_ac_validate_command_v1(p_operation);
  PERFORM {{MONI_SCHEMA}}.pp_ac_validate_root_v1(c.pp_root_id);
  RETURN QUERY SELECT 'replay'::text,c.operation_id,c.actor,c.kind,c.target_id,c.status,c.reason,c.before_state,c.after_state,c.created_at,c.pp_root_id,c.pp_account_journal_id;
  RETURN;
 END IF;
 v_root:=pg_catalog.gen_random_uuid()::text; v_source:=pg_catalog.gen_random_uuid()::text; v_journal:=pg_catalog.gen_random_uuid()::text; v_audit:=pg_catalog.gen_random_uuid()::text; v_xid:=pg_catalog.pg_current_xact_id();
 IF p_kind='account_create' THEN
  v_target:=pg_catalog.gen_random_uuid()::text; v_journal_kind:='create'; v_present:=false; v_block:=0;
 ELSE
  v_target:=p_target;
  PERFORM pa.id FROM {{MONI_SCHEMA}}.procurement_accounts pa WHERE pa.id=p_target FOR NO KEY UPDATE;
  v_present:=FOUND;
  IF v_present THEN
   PERFORM ph.account_id FROM {{MONI_SCHEMA}}.pp_account_heads ph WHERE ph.account_id=p_target FOR UPDATE;
   -- A new statement follows all waits, then validates the actual committed prefix.
   PERFORM {{MONI_SCHEMA}}.pp_ac_validate_account_v1(p_target);
   SELECT * INTO a FROM {{MONI_SCHEMA}}.procurement_accounts pa WHERE pa.id=p_target;
   SELECT * INTO h FROM {{MONI_SCHEMA}}.pp_account_heads ph WHERE ph.account_id=p_target;
   SELECT pg_catalog.count(*) INTO v_block FROM {{MONI_SCHEMA}}.procurement_reservations pr WHERE pr.account_id=p_target AND pr.anomaly AND pr.state IN ('unknown','disputed');
   IF h.ordinal=9223372036854775807 THEN RAISE EXCEPTION 'procurement_history_exhausted' USING ERRCODE='PP905'; END IF;
   v_before:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(a.id,a.label,a.provider,a.state,a.limit_amount,a.held_amount,a.spent_amount,a.revision,a.created_at);
   IF a.revision<>p_expected THEN v_status:='rejected'; v_reason:='revision_conflict'; v_journal_kind:='configure_rejected';
   ELSE
    v_journal_kind:='configure';
    IF a.revision=9223372036854775807 AND (a.label,a.provider,a.state,a.limit_amount) IS DISTINCT FROM (p_label,p_provider,p_state,p_limit) THEN RAISE EXCEPTION 'procurement_history_exhausted' USING ERRCODE='PP905'; END IF;
   END IF;
  ELSE v_status:='rejected'; v_reason:='not_found'; v_journal:=NULL;
  END IF;
 END IF;
 v_time:=pg_catalog.clock_timestamp();
 INSERT INTO {{MONI_SCHEMA}}.pp_account_command_sources(id,root_id,step_no,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id)
 VALUES(v_source,v_root,1,v_xid,pg_catalog.clock_timestamp(),p_operation,p_actor,v_role,v_user_status,p_kind,p_target,v_target,p_label,p_provider,p_state,p_limit,v_digest,v_status,v_reason,v_time,v_audit,p_expected,v_journal);
 IF p_kind='account_create' THEN
  INSERT INTO {{MONI_SCHEMA}}.procurement_accounts(id,label,provider,state,limit_amount,pp_version,pp_created_xid,pp_origin_journal_id)
  VALUES(v_target,p_label,p_provider,p_state,p_limit,1,v_xid,v_journal) RETURNING * INTO a;
 ELSIF v_status='applied' THEN
  UPDATE {{MONI_SCHEMA}}.procurement_accounts pa SET label=p_label,provider=p_provider,state=p_state,limit_amount=p_limit WHERE pa.id=v_target RETURNING * INTO a;
 END IF;
 IF v_status='applied' THEN v_after:={{MONI_SCHEMA}}.pp_ac_snapshot_v1(a.id,a.label,a.provider,a.state,a.limit_amount,a.held_amount,a.spent_amount,a.revision,a.created_at); END IF;
 IF v_journal IS NOT NULL THEN
  INSERT INTO {{MONI_SCHEMA}}.pp_account_journal(id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,command_source_id)
  VALUES(v_journal,v_target,coalesce(h.ordinal,0)+1,h.journal_id,v_root,1,'account_command',v_xid,pg_catalog.clock_timestamp(),v_journal_kind,'command',p_operation,h.label,h.provider,h.state,h.limit_amount,h.configuration_revision,h.held_amount,h.spent_amount,h.blocking_count,a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,v_block,v_source);
  IF p_kind='account_create' THEN
   INSERT INTO {{MONI_SCHEMA}}.pp_account_origins(account_id,version,create_operation_id,command_source_id,root_id,step_no,journal_id,created_xid,created_at) VALUES(v_target,1,p_operation,v_source,v_root,1,v_journal,v_xid,a.created_at);
   INSERT INTO {{MONI_SCHEMA}}.pp_account_heads(account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count) VALUES(v_target,1,v_journal,v_xid,a.label,a.provider,a.state,a.limit_amount,a.revision,a.held_amount,a.spent_amount,v_block);
  ELSE
   UPDATE {{MONI_SCHEMA}}.pp_account_heads ph SET ordinal=h.ordinal+1,journal_id=v_journal,updated_xid=v_xid,label=a.label,provider=a.provider,state=a.state,limit_amount=a.limit_amount,configuration_revision=a.revision,held_amount=a.held_amount,spent_amount=a.spent_amount,blocking_count=v_block WHERE ph.account_id=v_target;
  END IF;
 END IF;
 INSERT INTO {{MONI_SCHEMA}}.procurement_commands(operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id)
 VALUES(p_operation,p_actor,p_kind,v_target,v_digest,v_status,v_reason,v_before,v_after,v_source,v_root,1,v_xid,v_journal);
 INSERT INTO {{MONI_SCHEMA}}.audit_events(id,actor,action,target,sanitized_diff,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid)
 VALUES(v_audit,p_actor,'procurement.command.'||v_status,p_operation,pg_catalog.jsonb_build_object('kind',p_kind,'status',v_status),v_root,1,'account_command',v_source,'account_command',v_xid);
 INSERT INTO {{MONI_SCHEMA}}.pp_root_steps(root_id,step_no,kind,source_id,created_xid) VALUES(v_root,1,'account_command',v_source,v_xid);
 INSERT INTO {{MONI_SCHEMA}}.pp_operation_roots(id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result) VALUES(v_root,p_kind,'account_command',NULL,NULL,NULL,v_target,v_xid,{{MONI_APP_OID}}::oid,1,v_status);
 PERFORM {{MONI_SCHEMA}}.pp_ac_validate_root_v1(v_root);
 RETURN QUERY SELECT 'applied'::text,p_operation,p_actor,p_kind,v_target,v_status,v_reason,v_before,v_after,v_time,v_root,v_journal;
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_create_account(p_actor text,p_operation text,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS TABLE(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY DEFINER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 RETURN QUERY SELECT * FROM {{MONI_SCHEMA}}.pp_ac_mutate_v1('account_create',p_actor,p_operation,'',NULL,p_label,p_provider,p_state,p_limit);
END $$;

CREATE FUNCTION {{MONI_SCHEMA}}.pp_configure_account(p_actor text,p_operation text,p_account text,p_expected bigint,p_label text,p_provider text,p_state text,p_limit bigint)
RETURNS TABLE(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)
LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY DEFINER
SET search_path={{MONI_SCHEMA}},pg_catalog,pg_temp AS $$
BEGIN
 RETURN QUERY SELECT * FROM {{MONI_SCHEMA}}.pp_ac_mutate_v1('account_configure',p_actor,p_operation,p_account,p_expected,p_label,p_provider,p_state,p_limit);
END $$;

CREATE TRIGGER aa_pp_ac_root_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_operation_roots FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_step_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_root_steps FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_source_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_account_command_sources FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_origin_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_account_origins FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_journal_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_account_journal FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_head_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.pp_account_heads FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_root_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_operation_roots FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_step_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_root_steps FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_source_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_account_command_sources FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_origin_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_account_origins FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_journal_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_account_journal FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_head_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.pp_account_heads FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_guard_v1();
CREATE TRIGGER aa_pp_ac_account_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.procurement_accounts FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_account_write_v1();
CREATE TRIGGER aa_pp_ac_account_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.procurement_accounts FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_account_write_v1();
CREATE TRIGGER zz_pp_ac_account_result BEFORE INSERT OR UPDATE ON {{MONI_SCHEMA}}.procurement_accounts FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_account_result_v1();
CREATE TRIGGER aa_pp_ac_command_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.procurement_commands FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_command_write_v1();
CREATE TRIGGER aa_pp_ac_command_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.procurement_commands FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_command_write_v1();
CREATE TRIGGER aa_pp_ac_audit_write BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.audit_events FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_audit_write_v1();
CREATE TRIGGER aa_pp_ac_audit_truncate BEFORE TRUNCATE ON {{MONI_SCHEMA}}.audit_events FOR EACH STATEMENT EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_audit_write_v1();
CREATE TRIGGER aa_pp_ac_parent_lock_only BEFORE INSERT OR UPDATE OR DELETE ON {{MONI_SCHEMA}}.users FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_parent_write_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_root_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_operation_roots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_step_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_root_steps DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_source_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_account_command_sources DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_origin_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_account_origins DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_journal_complete AFTER INSERT ON {{MONI_SCHEMA}}.pp_account_journal DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_head_complete AFTER INSERT OR UPDATE ON {{MONI_SCHEMA}}.pp_account_heads DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_account_complete AFTER INSERT OR UPDATE ON {{MONI_SCHEMA}}.procurement_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_command_complete AFTER INSERT ON {{MONI_SCHEMA}}.procurement_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();
CREATE CONSTRAINT TRIGGER zz_pp_ac_audit_complete AFTER INSERT ON {{MONI_SCHEMA}}.audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{MONI_SCHEMA}}.pp_ac_deferred_v1();

-- Current-only MVP baseline. Historical migrations are not executed here.
DO $$
DECLARE owner_oid oid; database_owner_oid oid; schema_owner oid;
BEGIN
 IF current_setting('server_version_num')::integer < 150000 THEN
  RAISE EXCEPTION 'current MVP requires PostgreSQL 15 or later';
 END IF;
 SELECT oid INTO owner_oid FROM pg_catalog.pg_roles WHERE rolname=current_user;
 SELECT datdba INTO database_owner_oid FROM pg_catalog.pg_database WHERE datname=current_database();
 SELECT nspowner INTO schema_owner FROM pg_catalog.pg_namespace WHERE nspname=current_schema();
 IF owner_oid IS NULL OR schema_owner IS NULL OR NOT (schema_owner=owner_oid OR (schema_owner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname='pg_database_owner') AND database_owner_oid=owner_oid)) THEN
  RAISE EXCEPTION 'current MVP refuses changed schema owner';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relkind IN ('r','p','v','m','f','S') AND c.relname <> 'schema_migrations') THEN
  RAISE EXCEPTION 'current MVP refuses a non-empty schema before DDL';
 END IF;
END $$;
--
-- PostgreSQL database dump
--

-- pg_dump restrict removed PctMPQiEepacfssg8xOKIFX7xpdROu1EKojTxD2w5Qj5OVOHr5pGmeczV5ZmHdH

-- Dumped from database version 16.15 (Homebrew)
-- Dumped by pg_dump version 16.15 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', current_schema()::text || ', pg_catalog, pg_temp', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: dump_current_3b04ba63ced5; Type: SCHEMA; Schema: -; Owner: -
--

-- removed dump schema
--
-- Name: advance_channel_management_revision(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION advance_channel_management_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.management_revision := 1;
 ELSE NEW.management_revision := OLD.management_revision+1;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: advance_key_access_policy_revision(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION advance_key_access_policy_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.access_policy_revision:=1;
 ELSIF NEW.allowed_client_cidrs IS DISTINCT FROM OLD.allowed_client_cidrs THEN NEW.access_policy_revision:=OLD.access_policy_revision+1;
 ELSE NEW.access_policy_revision:=OLD.access_policy_revision;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: advance_key_configuration_revision(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION advance_key_configuration_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.configuration_revision:=1;
 ELSIF ROW(NEW.name,NEW.scopes,NEW.allowed_regions,NEW.lifetime_limit,NEW.expires_at) IS DISTINCT FROM ROW(OLD.name,OLD.scopes,OLD.allowed_regions,OLD.lifetime_limit,OLD.expires_at) THEN NEW.configuration_revision:=OLD.configuration_revision+1;
 ELSE NEW.configuration_revision:=OLD.configuration_revision;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: advance_key_runtime_limit_revision(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION advance_key_runtime_limit_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.limit_revision:=1;
 ELSIF ROW(NEW.rpm_limit,NEW.burst_limit,NEW.concurrency_limit) IS DISTINCT FROM ROW(OLD.rpm_limit,OLD.burst_limit,OLD.concurrency_limit) THEN NEW.limit_revision:=OLD.limit_revision+1;
 ELSE NEW.limit_revision:=OLD.limit_revision;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: alert_check_episode(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_check_episode(p_id text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE e alert_episodes; x alert_episode_events; n BIGINT; expected_n BIGINT; c alert_commands;
BEGIN
 SELECT * INTO e FROM alert_episodes WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'event has no episode'; END IF;
 expected_n:=1+(e.acknowledged_at IS NOT NULL)::int+(e.resolved_at IS NOT NULL)::int;
 SELECT count(*) INTO n FROM alert_episode_events WHERE episode_id=e.id;
 IF n IS DISTINCT FROM expected_n THEN RAISE EXCEPTION 'incomplete episode history'; END IF;
 SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=1;
 IF NOT FOUND OR (x.type,x.actor,x.observed_at,x.safe_sample,x.command_id)
   IS DISTINCT FROM ('opened'::text,'system'::text,e.opened_at,e.initial_sample,NULL::text) THEN RAISE EXCEPTION 'opened history mismatch'; END IF;
 IF e.acknowledged_at IS NOT NULL THEN
  SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=2;
  IF NOT FOUND OR (x.type,x.actor,x.observed_at,x.safe_sample,x.command_id)
    IS DISTINCT FROM ('acknowledged'::text,e.acknowledged_by,e.acknowledged_at,NULL::jsonb,e.acknowledged_command_id) THEN RAISE EXCEPTION 'ack history mismatch'; END IF;
  SELECT * INTO c FROM alert_commands WHERE operation_id=e.acknowledged_command_id;
  IF NOT FOUND OR c.status<>'applied' OR c.kind<>'acknowledge' OR c.target_id IS DISTINCT FROM e.id
    OR c.resource_id IS DISTINCT FROM e.id OR c.actor IS DISTINCT FROM e.acknowledged_by OR c.result_revision<>2 THEN RAISE EXCEPTION 'ack receipt mismatch'; END IF;
 END IF;
 IF e.resolved_at IS NOT NULL THEN
  SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=e.action_revision;
  IF NOT FOUND OR x.type IS DISTINCT FROM e.resolve_reason OR x.observed_at IS DISTINCT FROM e.resolved_at
    OR x.command_id IS DISTINCT FROM e.resolved_command_id THEN RAISE EXCEPTION 'resolve history mismatch'; END IF;
  IF e.resolve_reason='recovered' THEN
   IF x.actor IS DISTINCT FROM 'system' OR x.safe_sample IS DISTINCT FROM e.latest_sample THEN RAISE EXCEPTION 'recovery sample mismatch'; END IF;
  ELSE
   SELECT * INTO c FROM alert_commands WHERE operation_id=e.resolved_command_id;
   IF NOT FOUND OR c.status<>'applied' OR c.target_id IS DISTINCT FROM e.rule_id OR c.resource_id IS DISTINCT FROM e.rule_id
    OR c.kind IS DISTINCT FROM (CASE WHEN e.resolve_reason='rule_retired' THEN 'rule_retire' ELSE 'rule_configure' END)
    OR x.actor IS DISTINCT FROM c.actor OR x.safe_sample IS NOT NULL THEN RAISE EXCEPTION 'configuration event receipt mismatch'; END IF;
  END IF;
 END IF;
END $$;


--
-- Name: alert_check_rule(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_check_rule(p_id text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE r alert_rules; t alert_rule_runtime; e alert_episodes; max_generation BIGINT; c alert_commands;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing alert rule'; END IF;
 SELECT * INTO t FROM alert_rule_runtime WHERE rule_id=p_id;
 IF NOT FOUND OR t.configured_revision IS DISTINCT FROM r.configuration_revision THEN RAISE EXCEPTION 'missing/stale runtime'; END IF;
 SELECT * INTO c FROM alert_commands WHERE operation_id=r.last_command_id;
 IF NOT FOUND OR c.status<>'applied' OR c.actor IS DISTINCT FROM r.updated_by OR c.resource_id IS DISTINCT FROM r.id
  OR c.result_revision IS DISTINCT FROM r.configuration_revision
  OR c.kind NOT IN ('rule_create','rule_configure','rule_retire') THEN RAISE EXCEPTION 'rule receipt mismatch'; END IF;
 SELECT generation INTO max_generation FROM alert_episodes WHERE rule_id=p_id ORDER BY generation DESC LIMIT 1;
 IF t.episode_generation IS DISTINCT FROM COALESCE(max_generation,0) THEN RAISE EXCEPTION 'generation mismatch'; END IF;
 SELECT * INTO e FROM alert_episodes WHERE rule_id=p_id AND resolved_at IS NULL;
 IF FOUND THEN
  IF t.current_episode_id IS DISTINCT FROM e.id OR e.rule_revision IS DISTINCT FROM r.configuration_revision OR r.state<>'enabled'
    OR t.last_sample IS DISTINCT FROM e.latest_sample OR t.last_success_at IS DISTINCT FROM e.last_seen_at THEN RAISE EXCEPTION 'active pointer/sample mismatch'; END IF;
 ELSE
  IF t.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'orphan current pointer'; END IF;
 END IF;
 IF r.state<>'enabled' AND t.next_evaluation_at IS NOT NULL THEN RAISE EXCEPTION 'inactive due time'; END IF;
 IF t.last_success_at IS NOT NULL THEN
  IF t.evaluated_revision IS DISTINCT FROM r.configuration_revision OR NOT alert_sample(t.last_sample,r.kind)
    OR t.last_success_at IS DISTINCT FROM (t.last_sample->>'as_of')::timestamptz THEN RAISE EXCEPTION 'runtime sample mismatch'; END IF;
  IF t.current_episode_id IS NULL AND alert_decision(jsonb_build_object('kind',r.kind,'scope_type',r.scope_type,'scope_id',r.scope_id,'label',r.label,'parameters',r.parameters),t.last_sample,false)='breach'
    THEN RAISE EXCEPTION 'breach has no episode'; END IF;
 END IF;
END $$;


--
-- Name: alert_command_constraint(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_command_constraint() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c alert_commands; r alert_rules; e alert_episodes;
BEGIN
 SELECT * INTO c FROM alert_commands WHERE operation_id=NEW.operation_id;
 IF c.status IS DISTINCT FROM 'applied' AND c.status IS DISTINCT FROM 'rejected' THEN RAISE EXCEPTION 'uncommitted alert claim'; END IF;
 IF c.status='rejected' THEN
  IF EXISTS(SELECT 1 FROM alert_rules WHERE last_command_id=c.operation_id)
    OR EXISTS(SELECT 1 FROM alert_episodes WHERE acknowledged_command_id=c.operation_id OR resolved_command_id=c.operation_id)
    OR EXISTS(SELECT 1 FROM alert_episode_events WHERE command_id=c.operation_id) THEN RAISE EXCEPTION 'rejected command has effect'; END IF;
  RETURN NULL;
 END IF;
 IF c.kind='acknowledge' THEN
  SELECT * INTO e FROM alert_episodes WHERE id=c.resource_id;
  IF NOT FOUND OR e.id IS DISTINCT FROM c.target_id OR e.acknowledged_command_id IS DISTINCT FROM c.operation_id
    OR e.acknowledged_by IS DISTINCT FROM c.actor OR c.result_revision IS DISTINCT FROM 2::bigint THEN RAISE EXCEPTION 'ack command has no effect'; END IF;
  PERFORM alert_check_episode(e.id);
 ELSE
  SELECT * INTO r FROM alert_rules WHERE id=c.resource_id;
  IF NOT FOUND OR c.result_revision IS DISTINCT FROM r.configuration_revision
    OR (c.kind<>'rule_create' AND r.id IS DISTINCT FROM c.target_id)
    OR (c.kind='rule_create' AND (r.last_command_id IS DISTINCT FROM c.operation_id OR r.created_by IS DISTINCT FROM c.actor OR r.configuration_revision<>1))
    OR (c.kind='rule_retire' AND (r.last_command_id IS DISTINCT FROM c.operation_id OR r.state<>'retired'))
    OR (c.kind='rule_configure' AND r.state='retired') THEN RAISE EXCEPTION 'rule command mismatch'; END IF;
  -- configure可为CAS正确的no-op；若不是当前last_command，不允许本命令另有episode/event效果。
  IF c.kind='rule_configure' AND r.last_command_id IS DISTINCT FROM c.operation_id
    AND (EXISTS(SELECT 1 FROM alert_episodes WHERE resolved_command_id=c.operation_id)
      OR EXISTS(SELECT 1 FROM alert_episode_events WHERE command_id=c.operation_id)) THEN RAISE EXCEPTION 'no-op command has state effect'; END IF;
  PERFORM alert_check_rule(r.id);
 END IF;
 RETURN NULL;
END $$;


--
-- Name: alert_decision(jsonb, jsonb, boolean); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_decision(snapshot jsonb, sample jsonb, has_episode boolean) RETURNS text
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE k TEXT:=snapshot->>'kind'; p JSONB:=snapshot->'parameters'; n NUMERIC;
BEGIN
 IF NOT alert_snapshot(snapshot) OR NOT alert_sample(sample,k) OR has_episode IS NULL THEN RETURN 'invalid'; END IF;
 IF k='channel_quarantined' THEN RETURN CASE WHEN (sample->>'quarantined')::boolean THEN 'breach' ELSE 'clear' END; END IF;
 IF k='procurement_budget_low' THEN
  n:=(sample->>'available_amount')::numeric;
  IF has_episode THEN RETURN CASE WHEN n>(p->>'recovery_amount')::numeric THEN 'clear' ELSE 'hold' END; END IF;
  RETURN CASE WHEN n<=(p->>'threshold_amount')::numeric THEN 'breach' ELSE 'hold' END;
 END IF;
 IF (sample->>'minimum_age_seconds') IS DISTINCT FROM (p->>'minimum_age_seconds') THEN RETURN 'invalid'; END IF;
 n:=(sample->>'observed_count')::numeric;
 IF has_episode THEN RETURN CASE WHEN n<=(p->>'recovery_count')::numeric THEN 'clear' ELSE 'hold' END; END IF;
 RETURN CASE WHEN n>=(p->>'trigger_count')::numeric THEN 'breach' ELSE 'hold' END;
END $$;


--
-- Name: alert_episode_history_constraint(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_episode_history_constraint() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE e alert_episodes; t alert_rule_runtime;
BEGIN
 IF TG_TABLE_NAME='alert_episodes' THEN
  PERFORM alert_check_episode(NEW.id);
  SELECT * INTO e FROM alert_episodes WHERE id=NEW.id;
  IF e.resolve_reason='recovered' THEN
   SELECT * INTO t FROM alert_rule_runtime WHERE rule_id=e.rule_id;
   IF NOT FOUND OR t.last_sample IS DISTINCT FROM e.latest_sample OR t.last_success_at IS DISTINCT FROM e.last_seen_at
     OR t.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'recovery runtime mismatch'; END IF;
  END IF;
 ELSE
  PERFORM alert_check_episode(NEW.episode_id);
 END IF;
 RETURN NULL;
END $$;


--
-- Name: alert_guard_command(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_guard_command() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable alert command'; END IF;
 IF TG_OP='INSERT' THEN
  NEW.created_at:=clock_timestamp()::timestamptz(6);
  IF NEW.transaction_id IS DISTINCT FROM txid_current() OR NEW.status IS DISTINCT FROM 'claimed' THEN RAISE EXCEPTION 'claim required'; END IF;
 ELSE
  IF OLD.status IS DISTINCT FROM 'claimed' OR NEW.status NOT IN ('applied','rejected')
   OR (NEW.operation_id,NEW.actor,NEW.kind,NEW.target_id,NEW.canonical_digest,NEW.transaction_id,NEW.created_at)
    IS DISTINCT FROM (OLD.operation_id,OLD.actor,OLD.kind,OLD.target_id,OLD.canonical_digest,OLD.transaction_id,OLD.created_at)
   OR NEW.transaction_id IS DISTINCT FROM txid_current() THEN RAISE EXCEPTION 'immutable alert command'; END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: alert_guard_episode(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_guard_episode() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE r alert_rules; changed_ack BOOLEAN; changed_resolve BOOLEAN;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=NEW.rule_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing rule'; END IF;
 IF TG_OP='INSERT' THEN
  IF r.state<>'enabled' OR NEW.rule_revision IS DISTINCT FROM r.configuration_revision
    OR NEW.rule_snapshot IS DISTINCT FROM jsonb_build_object('kind',r.kind,'scope_type',r.scope_type,'scope_id',r.scope_id,'label',r.label,'parameters',r.parameters)
    OR NEW.acknowledged_by IS NOT NULL OR NEW.resolved_at IS NOT NULL
    OR NEW.latest_sample IS DISTINCT FROM NEW.initial_sample OR NEW.last_seen_at IS DISTINCT FROM NEW.opened_at
    OR alert_decision(NEW.rule_snapshot,NEW.initial_sample,false) IS DISTINCT FROM 'breach' THEN RAISE EXCEPTION 'invalid episode opening'; END IF;
  NEW.action_revision:=1;
 ELSE
  IF (NEW.id,NEW.rule_id,NEW.generation,NEW.rule_revision,NEW.rule_snapshot,NEW.opened_at,NEW.initial_sample)
    IS DISTINCT FROM (OLD.id,OLD.rule_id,OLD.generation,OLD.rule_revision,OLD.rule_snapshot,OLD.opened_at,OLD.initial_sample)
    OR OLD.resolved_at IS NOT NULL OR NEW.last_seen_at<OLD.last_seen_at THEN RAISE EXCEPTION 'immutable episode'; END IF;
  changed_ack:=(NEW.acknowledged_by,NEW.acknowledged_at,NEW.acknowledged_command_id)
    IS DISTINCT FROM (OLD.acknowledged_by,OLD.acknowledged_at,OLD.acknowledged_command_id);
  changed_resolve:=(NEW.resolved_at,NEW.resolve_reason,NEW.resolved_command_id)
    IS DISTINCT FROM (OLD.resolved_at,OLD.resolve_reason,OLD.resolved_command_id);
  IF changed_ack THEN
   IF OLD.acknowledged_by IS NOT NULL OR NEW.acknowledged_by IS NULL OR NEW.acknowledged_at IS NULL
    OR NOT alert_require_command(NEW.acknowledged_command_id,'acknowledge',NEW.id,NEW.acknowledged_by)
    OR changed_resolve OR (NEW.latest_sample,NEW.last_seen_at) IS DISTINCT FROM (OLD.latest_sample,OLD.last_seen_at)
    THEN RAISE EXCEPTION 'invalid acknowledgement'; END IF;
  END IF;
  IF changed_resolve THEN
   IF NEW.resolved_at IS NULL THEN RAISE EXCEPTION 'invalid resolution'; END IF;
   IF NEW.resolve_reason='recovered' THEN
    IF NEW.resolved_command_id IS NOT NULL OR NEW.resolved_at IS DISTINCT FROM NEW.last_seen_at
      OR alert_decision(NEW.rule_snapshot,NEW.latest_sample,true) IS DISTINCT FROM 'clear' THEN RAISE EXCEPTION 'invalid recovery'; END IF;
   ELSE
    IF (NEW.latest_sample,NEW.last_seen_at) IS DISTINCT FROM (OLD.latest_sample,OLD.last_seen_at)
      OR NEW.resolved_command_id IS DISTINCT FROM r.last_command_id
      OR NEW.rule_revision>=r.configuration_revision
      OR NEW.resolved_at IS DISTINCT FROM r.updated_at
      OR NEW.resolve_reason IS DISTINCT FROM (CASE WHEN r.state='retired' THEN 'rule_retired' WHEN r.state='disabled' THEN 'rule_disabled' ELSE 'rule_changed' END)
      OR NOT alert_require_command(NEW.resolved_command_id,CASE WHEN r.state='retired' THEN 'rule_retire' ELSE 'rule_configure' END,r.id,r.updated_by)
      THEN RAISE EXCEPTION 'invalid configuration close'; END IF;
   END IF;
  END IF;
  NEW.action_revision:=1+(NEW.acknowledged_at IS NOT NULL)::int+(NEW.resolved_at IS NOT NULL)::int;
 END IF;
 IF NEW.resolved_at IS NULL AND alert_decision(NEW.rule_snapshot,NEW.latest_sample,true) NOT IN ('breach','hold') THEN RAISE EXCEPTION 'clear sample cannot stay open'; END IF;
 RETURN NEW;
END $$;


--
-- Name: alert_guard_rule(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_guard_rule() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE changed BOOLEAN; expected_kind TEXT; live_count BIGINT; enabled_count BIGINT;
BEGIN
 -- repository在rule写之前已取control；直接SQL也必须串行验证实例上限。
 -- 任意raw UPDATE本来可能先持目标rule锁，不能将其死锁行为当成受支持API锁序。
 PERFORM singleton FROM alert_control WHERE singleton FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing alert control'; END IF;
 SELECT count(*),count(*) FILTER (WHERE state='enabled')
  INTO live_count,enabled_count FROM alert_rules WHERE state<>'retired';
 IF TG_OP='INSERT' THEN
  IF NEW.state='retired' OR NOT alert_require_command(NEW.last_command_id,'rule_create',NULL,NEW.created_by)
    OR NEW.updated_by IS DISTINCT FROM NEW.created_by OR NEW.updated_at IS DISTINCT FROM NEW.created_at THEN RAISE EXCEPTION 'invalid rule creation'; END IF;
  IF live_count>=256 OR (NEW.state='enabled' AND enabled_count>=64) THEN RAISE EXCEPTION 'alert rule limit'; END IF;
  NEW.configuration_revision:=1;
 ELSE
  IF (NEW.id,NEW.kind,NEW.scope_type,NEW.scope_id,NEW.created_by,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.kind,OLD.scope_type,OLD.scope_id,OLD.created_by,OLD.created_at) THEN RAISE EXCEPTION 'immutable rule identity'; END IF;
  changed:=(NEW.label,NEW.state,NEW.parameters) IS DISTINCT FROM (OLD.label,OLD.state,OLD.parameters);
  IF NOT changed THEN
   NEW.configuration_revision:=OLD.configuration_revision; NEW.last_command_id:=OLD.last_command_id;
   NEW.updated_by:=OLD.updated_by; NEW.updated_at:=OLD.updated_at; NEW.retired_at:=OLD.retired_at;
   RETURN NEW;
  END IF;
  IF OLD.state='retired' OR OLD.configuration_revision=9223372036854775807 THEN RAISE EXCEPTION 'rule terminal or exhausted'; END IF;
  expected_kind:=CASE WHEN NEW.state='retired' THEN 'rule_retire' ELSE 'rule_configure' END;
  IF NOT alert_require_command(NEW.last_command_id,expected_kind,NEW.id,NEW.updated_by)
    OR NEW.last_command_id IS NOT DISTINCT FROM OLD.last_command_id OR NEW.updated_at<OLD.updated_at THEN RAISE EXCEPTION 'rule command required'; END IF;
  IF NEW.state='retired' AND (NEW.label,NEW.parameters) IS DISTINCT FROM (OLD.label,OLD.parameters) THEN RAISE EXCEPTION 'retire changes identity'; END IF;
  IF OLD.state<>'enabled' AND NEW.state='enabled' AND enabled_count>=64 THEN RAISE EXCEPTION 'alert rule limit'; END IF;
  NEW.configuration_revision:=OLD.configuration_revision+1;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: alert_guard_runtime(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_guard_runtime() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE r alert_rules; reset BOOLEAN;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=NEW.rule_id;
 IF NOT FOUND OR NEW.configured_revision IS DISTINCT FROM r.configuration_revision THEN RAISE EXCEPTION 'runtime configuration mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.episode_generation<>0 OR NEW.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'invalid initial runtime'; END IF;
  NEW.write_revision:=1; reset:=true;
 ELSE
  IF NEW.rule_id IS DISTINCT FROM OLD.rule_id OR OLD.write_revision=9223372036854775807 THEN RAISE EXCEPTION 'runtime immutable or exhausted'; END IF;
  NEW.write_revision:=OLD.write_revision+1;
  reset:=NEW.configured_revision IS DISTINCT FROM OLD.configured_revision;
  IF reset AND (NEW.episode_generation IS DISTINCT FROM OLD.episode_generation
    OR NEW.configured_revision<>OLD.configured_revision+1) THEN RAISE EXCEPTION 'invalid configuration reset'; END IF;
 END IF;
 IF reset THEN
  IF NOT EXISTS(SELECT 1 FROM alert_commands c WHERE c.operation_id=r.last_command_id
    AND c.transaction_id=txid_current() AND c.status IN ('claimed','applied')
    AND c.kind IN ('rule_create','rule_configure','rule_retire')) THEN RAISE EXCEPTION 'reset command required'; END IF;
  IF NEW.last_attempt_at IS NOT NULL OR NEW.last_success_at IS NOT NULL OR NEW.evaluated_revision IS NOT NULL
    OR NEW.last_sample IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.current_episode_id IS NOT NULL
    OR NEW.next_evaluation_at IS DISTINCT FROM (CASE WHEN r.state='enabled' THEN r.updated_at ELSE NULL END) THEN RAISE EXCEPTION 'invalid reset fields'; END IF;
  RETURN NEW;
 END IF;
 IF r.state<>'enabled' OR NEW.last_attempt_at IS NULL OR NEW.next_evaluation_at IS NULL THEN RAISE EXCEPTION 'rule not evaluable'; END IF;
 IF OLD.last_attempt_at IS NOT NULL AND NEW.last_attempt_at<OLD.last_attempt_at AND NEW.last_error_code IS DISTINCT FROM 'clock_regression' THEN RAISE EXCEPTION 'attempt clock regression'; END IF;
 IF NEW.last_error_code IS NULL THEN
  IF NEW.last_success_at IS DISTINCT FROM NEW.last_attempt_at OR NEW.evaluated_revision IS DISTINCT FROM r.configuration_revision
   OR NOT alert_sample(NEW.last_sample,r.kind) OR NEW.last_success_at IS DISTINCT FROM (NEW.last_sample->>'as_of')::timestamptz
   OR (OLD.last_success_at IS NOT NULL AND NEW.last_success_at<OLD.last_success_at)
   OR NEW.next_evaluation_at IS DISTINCT FROM NEW.last_success_at+interval '60 seconds'
   OR NOT (NEW.episode_generation=OLD.episode_generation OR
      (OLD.episode_generation<9223372036854775807 AND NEW.episode_generation=OLD.episode_generation+1)) THEN RAISE EXCEPTION 'invalid successful evaluation'; END IF;
 ELSE
  IF (NEW.last_success_at,NEW.evaluated_revision,NEW.last_sample,NEW.current_episode_id,NEW.episode_generation)
    IS DISTINCT FROM (OLD.last_success_at,OLD.evaluated_revision,OLD.last_sample,OLD.current_episode_id,OLD.episode_generation)
    OR NEW.next_evaluation_at IS DISTINCT FROM NEW.last_attempt_at+interval '30 seconds' THEN RAISE EXCEPTION 'failed evaluation changed success'; END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: alert_id(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_id(v text) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $_$
 SELECT COALESCE(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND v <> '00000000-0000-0000-0000-000000000000',false)
$_$;


--
-- Name: alert_keys(jsonb, text[]); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_keys(v jsonb, names text[]) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
BEGIN
 IF jsonb_typeof(v) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 RETURN (v ?& names) IS TRUE AND NOT EXISTS
   (SELECT 1 FROM jsonb_object_keys(v) k WHERE NOT (k=ANY(names)));
END $$;


--
-- Name: alert_label(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_label(v text) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE n INTEGER; i INTEGER; cp INTEGER;
BEGIN
 IF v IS NULL THEN RETURN false; END IF;
 n:=char_length(v);
 IF n<1 OR n>80 OR octet_length(v)>160 THEN RETURN false; END IF;
 FOR i IN 1..n LOOP
  cp:=ascii(substr(v,i,1)); -- 受支持数据库编码必须UTF8，ascii返回Unicode code point
  IF cp BETWEEN 0 AND 31 OR cp BETWEEN 127 AND 159 THEN RETURN false; END IF;
  IF (i=1 OR i=n) AND (cp IN (32,133,160,5760,8232,8233,8239,8287,12288)
    OR cp BETWEEN 8192 AND 8202) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;


--
-- Name: alert_parameters(text, jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_parameters(k text, p jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
BEGIN
 IF p IS NULL OR octet_length(p::text)>512 THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN RETURN p='{}'::jsonb; END IF;
 IF k='procurement_budget_low' THEN
  IF NOT alert_keys(p,ARRAY['threshold_amount','recovery_amount']) THEN RETURN false; END IF;
  IF jsonb_typeof(p->'threshold_amount') IS DISTINCT FROM 'string'
    OR jsonb_typeof(p->'recovery_amount') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'threshold_amount',9223372036854775807)
    OR NOT alert_uint(p->>'recovery_amount',9223372036854775807) THEN RETURN false; END IF;
  RETURN (p->>'recovery_amount')::numeric >= (p->>'threshold_amount')::numeric;
 END IF;
 IF k IN ('customer_reconciliation_aged','procurement_unresolved_aged') THEN
  IF NOT alert_keys(p,ARRAY['minimum_age_seconds','trigger_count','recovery_count']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(p) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'minimum_age_seconds',604800) OR NOT alert_uint(p->>'trigger_count',1000)
    OR NOT alert_uint(p->>'recovery_count',999) THEN RETURN false; END IF;
  RETURN (p->>'minimum_age_seconds')::numeric>=600 AND (p->>'trigger_count')::numeric>=1
    AND (p->>'recovery_count')::numeric<(p->>'trigger_count')::numeric;
 END IF;
 RETURN false;
END $$;


--
-- Name: alert_require_command(text, text, text, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_require_command(op text, k text, target text, who text) RETURNS boolean
    LANGUAGE plpgsql
    
    AS $$
DECLARE c alert_commands;
BEGIN
 SELECT * INTO c FROM alert_commands WHERE operation_id=op;
 IF NOT FOUND THEN RETURN false; END IF;
 RETURN c.transaction_id=txid_current() AND c.kind=k AND c.target_id IS NOT DISTINCT FROM target
  AND c.actor=who AND c.status IN ('claimed','applied');
END $$;


--
-- Name: alert_rule_constraint(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_rule_constraint() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_TABLE_NAME='alert_rules' THEN PERFORM alert_check_rule(NEW.id);
 ELSE PERFORM alert_check_rule(NEW.rule_id); END IF;
 RETURN NULL;
END $$;


--
-- Name: alert_sample(jsonb, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_sample(v jsonb, k text) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE n NUMERIC; age_seconds NUMERIC;
BEGIN
 IF v IS NULL OR octet_length(v::text)>2048 OR jsonb_typeof(v) IS DISTINCT FROM 'object'
   OR jsonb_typeof(v->'kind') IS DISTINCT FROM 'string' OR (v->>'kind') IS DISTINCT FROM k
   OR jsonb_typeof(v->'as_of') IS DISTINCT FROM 'string' OR NOT alert_time(v->>'as_of') THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN
  RETURN alert_keys(v,ARRAY['kind','as_of','enabled','quarantined'])
   AND jsonb_typeof(v->'enabled')='boolean' AND jsonb_typeof(v->'quarantined')='boolean';
 ELSIF k='procurement_budget_low' THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','account_state','limit_amount','held_amount','spent_amount','available_amount','currency']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF (v->>'account_state') NOT IN ('active','paused') OR (v->>'currency') IS DISTINCT FROM 'CNY'
    OR NOT alert_uint(v->>'limit_amount',9223372036854775807)
    OR NOT alert_uint(v->>'held_amount',power(10::numeric,100)-1)
    OR NOT alert_uint(v->>'spent_amount',power(10::numeric,100)-1)
    OR NOT alert_signed(v->>'available_amount') THEN RETURN false; END IF;
  RETURN (v->>'available_amount')::numeric=(v->>'limit_amount')::numeric-(v->>'held_amount')::numeric-(v->>'spent_amount')::numeric;
 ELSIF k IN ('customer_reconciliation_aged','procurement_unresolved_aged') THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','minimum_age_seconds','observed_count','count_is_lower_bound','oldest_created_at','oldest_reference_id']) THEN RETURN false; END IF;
  IF jsonb_typeof(v->'minimum_age_seconds') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'observed_count') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'count_is_lower_bound') IS DISTINCT FROM 'boolean'
    OR NOT alert_uint(v->>'minimum_age_seconds',604800) OR NOT alert_uint(v->>'observed_count',1001) THEN RETURN false; END IF;
  n:=(v->>'observed_count')::numeric; age_seconds:=(v->>'minimum_age_seconds')::numeric;
  IF age_seconds<600 OR (v->>'count_is_lower_bound')::boolean IS DISTINCT FROM (n=1001) THEN RETURN false; END IF;
  IF n=0 THEN RETURN v->'oldest_created_at'='null'::jsonb AND v->'oldest_reference_id'='null'::jsonb; END IF;
  IF jsonb_typeof(v->'oldest_created_at') IS DISTINCT FROM 'string' OR jsonb_typeof(v->'oldest_reference_id') IS DISTINCT FROM 'string'
    OR NOT alert_time(v->>'oldest_created_at') OR NOT alert_id(v->>'oldest_reference_id') THEN RETURN false; END IF;
  RETURN (v->>'oldest_created_at')::timestamptz <= (v->>'as_of')::timestamptz-age_seconds*interval '1 second';
 END IF;
 RETURN false;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;


--
-- Name: alert_signed(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_signed(v text) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $_$
 SELECT COALESCE(length(v)<=102 AND v ~ '^(0|[1-9][0-9]{0,100}|-[1-9][0-9]{0,100})$',false)
$_$;


--
-- Name: alert_snapshot(jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_snapshot(v jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE k TEXT; s TEXT;
BEGIN
 IF v IS NULL OR octet_length(v::text)>1024 OR NOT alert_keys(v,ARRAY['kind','scope_type','scope_id','label','parameters']) THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE x.key<>'parameters' AND jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
 k:=v->>'kind'; s:=v->>'scope_type';
 RETURN alert_id(v->>'scope_id') AND alert_label(v->>'label') AND alert_parameters(k,v->'parameters')
  AND ((k='channel_quarantined' AND s='channel')
    OR (k IN ('procurement_budget_low','procurement_unresolved_aged') AND s='procurement_account')
    OR (k='customer_reconciliation_aged' AND s='tenant'));
END $$;


--
-- Name: alert_time(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_time(v text) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $_$
BEGIN
 IF v IS NULL OR v !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.][0-9]{6}Z$' THEN RETURN false; END IF;
 RETURN to_char(v::timestamptz AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')=v;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $_$;


--
-- Name: alert_uint(text, numeric); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION alert_uint(v text, max_value numeric) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $_$
BEGIN
 IF v IS NULL OR length(v)>101 OR v !~ '^(0|[1-9][0-9]*)$' THEN RETURN false; END IF;
 RETURN v::numeric<=max_value;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $_$;


--
-- Name: assert_pricing_safe_input(text, jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION assert_pricing_safe_input(k text, j jsonb) RETURNS void
    LANGUAGE plpgsql IMMUTABLE
    
    AS $_$
DECLARE shape jsonb; label text;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'pricing safe input must be object'; END IF;
 CASE k
 WHEN 'price_group.create' THEN shape:=jsonb_build_object('label',j->'label');
 WHEN 'price_group.update' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','label',j->'label','state',j->'state');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR jsonb_typeof(j->'state') IS DISTINCT FROM 'string' OR (j->>'state') NOT IN ('active','retired') THEN RAISE EXCEPTION 'invalid group update input'; END IF;
 WHEN 'group_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id')
   OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$'
   OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb
   OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid group price input'; END IF;
  PERFORM meter_text_rates_canonical(j->'rates','retail');
 WHEN 'default_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid default price input'; END IF;
  PERFORM meter_text_rates_canonical(j->'rates','retail');
 WHEN 'cost_price.publish' THEN
  shape:=jsonb_build_object('channel_id',j->'channel_id','upstream_model',j->'upstream_model','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','basis',j->'basis','rates',j->'rates','effective_at',j->'effective_at','expected_channel_revision',j->'expected_channel_revision');
  IF NOT pricing_json_uuid(j->'channel_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'upstream_model') IS DISTINCT FROM 'string' OR octet_length(j->>'upstream_model') NOT BETWEEN 1 AND 200 OR (j->>'upstream_model')<>btrim(j->>'upstream_model') OR (j->>'upstream_model')~'[[:cntrl:]]' OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"CNY"'::jsonb OR (j->>'basis') NOT IN ('contract','invoice','unknown') OR NOT pricing_json_time(j->'effective_at') OR NOT pricing_json_uint(j->'expected_channel_revision',true) THEN RAISE EXCEPTION 'invalid cost price input'; END IF;
  IF (j->>'basis')='unknown' THEN IF j->'rates' IS DISTINCT FROM 'null'::jsonb THEN RAISE EXCEPTION 'unknown cost has rates'; END IF; ELSE IF j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid cost price input'; END IF; PERFORM meter_text_rates_canonical(j->'rates','supplier'); END IF;
 WHEN 'tenant_pricing.assign' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','price_group_id',j->'price_group_id');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR NOT (j ? 'price_group_id') OR (jsonb_typeof(j->'price_group_id') IS DISTINCT FROM 'null' AND NOT pricing_json_uuid(j->'price_group_id')) THEN RAISE EXCEPTION 'invalid tenant pricing input'; END IF;
 ELSE RAISE EXCEPTION 'unknown pricing operation kind'; END CASE;
 IF j IS DISTINCT FROM shape THEN RAISE EXCEPTION 'pricing input keys mismatch'; END IF;
 IF k IN ('price_group.create','price_group.update') THEN
  IF jsonb_typeof(j->'label') IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'pricing label must be string'; END IF;
  label:=j->>'label';
  IF octet_length(label) NOT BETWEEN 1 AND 100 OR label<>btrim(label) OR label ~ '[[:cntrl:]]' THEN RAISE EXCEPTION 'invalid pricing label'; END IF;
 END IF;
END $_$;


--
-- Name: assert_procurement_budget_projection(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION assert_procurement_budget_projection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE target TEXT; actual_held NUMERIC; actual_spent NUMERIC; event_held NUMERIC; event_spent NUMERIC; reserved_held NUMERIC; reserved_spent NUMERIC;
BEGIN
 IF TG_TABLE_NAME='procurement_accounts' THEN target := NEW.id; ELSE target := NEW.account_id; END IF;
 SELECT held_amount,spent_amount INTO actual_held,actual_spent FROM procurement_accounts WHERE id=target;
 SELECT coalesce(sum(held_delta),0),coalesce(sum(spent_delta),0) INTO event_held,event_spent FROM procurement_budget_events WHERE account_id=target;
 SELECT coalesce(sum(held_amount),0),coalesce(sum(spent_amount),0) INTO reserved_held,reserved_spent FROM procurement_reservations WHERE account_id=target;
 IF actual_held IS DISTINCT FROM event_held OR actual_spent IS DISTINCT FROM event_spent OR actual_held IS DISTINCT FROM reserved_held OR actual_spent IS DISTINCT FROM reserved_spent THEN
  RAISE EXCEPTION 'procurement budget projection does not match immutable events and reservations' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: canonical_key_client_cidr(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION canonical_key_client_cidr(value text) RETURNS text
    LANGUAGE plpgsql IMMUTABLE
    AS $$
DECLARE address CIDR; wire BYTEA; parts TEXT[] := '{}'; part INTEGER;
 current_start INTEGER := 0; current_length INTEGER := 0;
 best_start INTEGER := 0; best_length INTEGER := 0; position INTEGER; canonical TEXT;
BEGIN
 IF value IS NULL OR octet_length(value)>128 THEN RETURN NULL; END IF;
 address := value::CIDR;
 IF masklen(address)=0 THEN RETURN NULL; END IF;
 IF family(address)=4 THEN RETURN address::TEXT; END IF;
 wire := inet_send(address::INET);
 IF substring(wire FROM 5 FOR 12)=decode('00000000000000000000ffff','hex') THEN RETURN NULL; END IF;
 FOR position IN 0..7 LOOP
  part := get_byte(wire,4+position*2)*256+get_byte(wire,5+position*2);
  parts := array_append(parts,to_hex(part));
  IF part=0 THEN
   IF current_length=0 THEN current_start:=position+1; END IF;
   current_length:=current_length+1;
   IF current_length>best_length THEN best_start:=current_start; best_length:=current_length; END IF;
  ELSE current_length:=0;
  END IF;
 END LOOP;
 IF best_length>=2 THEN
  canonical := COALESCE(array_to_string(parts[1:best_start-1],':'),'') || '::' || COALESCE(array_to_string(parts[best_start+best_length:8],':'),'');
 ELSE canonical:=array_to_string(parts,':');
 END IF;
 RETURN canonical || '/' || masklen(address)::TEXT;
EXCEPTION WHEN OTHERS THEN RETURN NULL;
END $$;


--
-- Name: commerce_command_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION commerce_command_complete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts r USING(operation_id) WHERE c.operation_id=NEW.operation_id AND (c.kind,c.scope,c.actor)=(r.kind,r.scope,r.actor)) THEN RAISE EXCEPTION 'commerce command incomplete'; END IF;
 IF TG_TABLE_NAME='commerce_operation_receipts' THEN
  IF NEW.status='accepted' AND NEW.kind='voucher.redeem' AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.operation_id=NEW.operation_id AND r.id=NEW.resource_id AND r.actor=NEW.actor AND 'tenant:'||r.tenant_id=NEW.scope AND r.amount=NEW.amount AND r.funding_receipt_id=NEW.funding_receipt_id AND r.journal_id=NEW.journal_id) THEN RAISE EXCEPTION 'commerce redemption incomplete';END IF;
  IF NEW.status='accepted' AND (
   (NEW.kind='voucher.batch.create' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.maker=NEW.actor)) OR
   (NEW.kind='voucher.batch.approve' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.state IN ('active','revoked') AND b.approver=NEW.actor)) OR
   (NEW.kind='voucher.batch.void' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.state='voided' AND b.terminal_actor=NEW.actor AND b.maker=NEW.actor)) OR
   (NEW.kind='voucher.revocation.create' AND NOT EXISTS(SELECT 1 FROM voucher_revocations p WHERE p.id=NEW.resource_id AND p.operation_id=NEW.operation_id AND p.maker=NEW.actor)) OR
   (NEW.kind='voucher.revocation.approve' AND NOT EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.proposal_id=NEW.resource_id AND d.operation_id=NEW.operation_id AND d.approver=NEW.actor AND d.status='accepted'))
  ) THEN RAISE EXCEPTION 'commerce resource incomplete';END IF;
  IF NEW.status='rejected' AND NEW.kind='voucher.revocation.approve' AND NEW.reason='voucher_evidence_stale' AND NOT EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.operation_id=NEW.operation_id AND d.approver=NEW.actor AND d.status='rejected' AND d.reason=NEW.reason) THEN RAISE EXCEPTION 'commerce stale decision incomplete';END IF;
 END IF;
 RETURN NULL;
END $$;


--
-- Name: funding_credit_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION funding_credit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL THEN RAISE EXCEPTION 'credit initial state'; END IF;
 ELSE
  IF (NEW.id,NEW.tenant_id,NEW.business_ref,NEW.amount,NEW.requester,NEW.proof_ref,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.business_ref,OLD.amount,OLD.requester,OLD.proof_ref,OLD.created_at) OR OLD.state<>'pending' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 END IF;
 IF (NEW.state='pending' AND (NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL)) OR (NEW.state='approved' AND (NEW.approver IS NULL OR NEW.approver=NEW.requester OR NEW.journal_id IS NULL)) THEN RAISE EXCEPTION 'credit approval incomplete'; END IF;
 RETURN NEW;
END $$;


--
-- Name: funding_fact_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION funding_fact_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE jid TEXT; credit_state TEXT;
BEGIN
 IF TG_TABLE_NAME='credit_orders' THEN
  SELECT state,journal_id INTO credit_state,jid FROM credit_orders WHERE id=NEW.id;
  IF credit_state='pending' THEN
   IF EXISTS(SELECT 1 FROM journals WHERE kind='credit' AND operation_id=NEW.id) OR EXISTS(SELECT 1 FROM projection_outbox WHERE kind='credited' AND operation_id=NEW.id) THEN RAISE EXCEPTION 'pending credit has funding'; END IF;
   RETURN NULL;
  END IF;
 ELSIF TG_TABLE_NAME='journals' THEN
  IF NEW.kind NOT IN ('credit','voucher_grant','sandbox_capture') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $$;


--
-- Name: funding_journal_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION funding_journal_valid(journal_ref text) RETURNS boolean
    LANGUAGE plpgsql STABLE
    
    AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='sandbox_capture') THEN RETURN payment_sandbox_journal_valid(journal_ref); END IF;
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='wallet_opening') THEN RETURN wallet_opening_journal_valid(journal_ref); END IF;
 RETURN payment_funding_journal_valid(journal_ref);
END
$$;


--
-- Name: funding_uuid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION funding_uuid(value text) RETURNS boolean
    LANGUAGE sql IMMUTABLE STRICT
    AS $_$
 SELECT value ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND value<>'00000000-0000-0000-0000-000000000000'
$_$;


--
-- Name: guard_financial_idempotency(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_financial_idempotency() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.created_txid<>txid_current() THEN RAISE EXCEPTION 'immutable financial idempotency'; END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_group_price_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_group_price_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE gstate TEXT; op pricing_operations%ROWTYPE; expected_kind text;
BEGIN
 PERFORM id FROM models WHERE id=NEW.model_id FOR SHARE;
 expected_kind:=CASE WHEN NEW.price_group_id IS NULL THEN 'default_price.publish' ELSE 'group_price.publish' END;
 IF NOT EXISTS (SELECT 1 FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN model_capability_variants v ON v.profile_version=c.id AND v.variant_key=NEW.variant_key WHERE m.id=NEW.model_id AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest) THEN RAISE EXCEPTION 'price schema mismatch'; END IF;
 IF NEW.price_group_id IS NOT NULL THEN
  IF NOT EXISTS (SELECT 1 FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN model_capability_variants v ON v.profile_version=c.id AND v.variant_key=NEW.variant_key WHERE m.id=NEW.model_id AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest) THEN RAISE EXCEPTION 'group price schema mismatch'; END IF;
  SELECT state INTO STRICT gstate FROM price_groups WHERE id=NEW.price_group_id FOR SHARE;
  IF gstate<>'active' THEN RAISE EXCEPTION 'price group retired'; END IF;
 END IF;
 SELECT * INTO op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 IF NOT FOUND OR op.created_txid<>txid_current() OR op.kind<>expected_kind OR op.resource_id<>NEW.id OR op.actor<>NEW.created_by THEN RAISE EXCEPTION 'price operation mismatch'; END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_journal_line_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_journal_line_insert() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM journals WHERE id=NEW.journal_id AND created_txid=txid_current()) THEN
  RAISE EXCEPTION 'cannot append to committed journal';
 END IF;
 IF EXISTS(SELECT 1 FROM ledger_accounts a JOIN journals j ON j.id=NEW.journal_id WHERE a.id=NEW.account_id AND a.tenant_id IS NOT NULL AND a.tenant_id<>j.tenant_id) THEN
  RAISE EXCEPTION 'cross-tenant journal account';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_model_public_revision(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_model_public_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  NEW.public_revision := 1;
 ELSIF NEW.public_visible IS DISTINCT FROM OLD.public_visible THEN
  NEW.public_revision := OLD.public_revision+1;
 ELSE
  NEW.public_revision := OLD.public_revision;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_price_group_change(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_price_group_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'active' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'invalid initial price group';
  END IF;
 ELSE
  IF ROW(NEW.id,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.created_at) THEN
   RAISE EXCEPTION 'immutable price group identity';
  END IF;
  IF OLD.state='retired' AND NEW.state<>'retired' THEN
   RAISE EXCEPTION 'price group cannot be restored';
  END IF;
  IF ROW(NEW.label,NEW.state) IS DISTINCT FROM ROW(OLD.label,OLD.state) THEN
   IF OLD.revision=9223372036854775807 OR NEW.revision<>OLD.revision+1
      OR NEW.last_operation_id=OLD.last_operation_id THEN
    RAISE EXCEPTION 'invalid price group revision';
   END IF;
  ELSIF ROW(NEW.revision,NEW.last_operation_id)
        IS DISTINCT FROM ROW(OLD.revision,OLD.last_operation_id) THEN
   RAISE EXCEPTION 'price group no-op changed revision';
  END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_pricing_operation_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_pricing_operation_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input); NEW.created_txid:=txid_current(); NEW.created_at:=clock_timestamp(); NEW.input_fingerprint:=pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,NEW.safe_input);
 IF NEW.kind IN ('price_group.create','group_price.publish','default_price.publish','cost_price.publish') THEN NEW.result_snapshot:=jsonb_set(NEW.result_snapshot,'{created_at}',to_jsonb(pricing_time(NEW.created_at)),true); END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_procurement_cost_proposal(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_procurement_cost_proposal() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF OLD.state<>'proposed' OR NEW.state<>'approved' OR
  (NEW.id,NEW.tenant_id,NEW.attempt_id,NEW.amount,NEW.basis,NEW.evidence_ref,NEW.reason,NEW.requester,NEW.expected_reservation_revision,NEW.expected_evidence_count,NEW.created_at)
  IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.attempt_id,OLD.amount,OLD.basis,OLD.evidence_ref,OLD.reason,OLD.requester,OLD.expected_reservation_revision,OLD.expected_evidence_count,OLD.created_at) THEN
  RAISE EXCEPTION 'procurement cost proposal only permits one immutable approval';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_procurement_reservation_identity(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_procurement_reservation_identity() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE actual_credential TEXT; actual_channel TEXT; bound_credential TEXT; bound_channel TEXT;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.attempt_id,NEW.account_id,NEW.binding_id,NEW.upper_amount,NEW.created_at)
   IS DISTINCT FROM (OLD.attempt_id,OLD.account_id,OLD.binding_id,OLD.upper_amount,OLD.created_at) THEN
   RAISE EXCEPTION 'procurement reservation identity and bounds are immutable';
  END IF;
  IF NEW.state<>OLD.state AND NOT (
   (OLD.state='prepared' AND NEW.state IN ('sending','released','unknown','settled','disputed')) OR
   (OLD.state='sending' AND NEW.state IN ('unknown','settled','released','disputed')) OR
   (OLD.state='unknown' AND NEW.state IN ('settled','released','disputed')) OR
   (OLD.state='settled' AND NEW.state='disputed') OR
   (OLD.state='released' AND NEW.state IN ('disputed','settled')) OR
   (OLD.state='disputed' AND NEW.state='settled')
  ) THEN RAISE EXCEPTION 'invalid procurement reservation state transition'; END IF;
  IF (NEW.state,NEW.held_amount,NEW.spent_amount,NEW.anomaly) IS DISTINCT FROM (OLD.state,OLD.held_amount,OLD.spent_amount,OLD.anomaly) THEN NEW.revision := OLD.revision+1; ELSE NEW.revision := OLD.revision; END IF;
  RETURN NEW;
 END IF;
 NEW.revision := 1;
 SELECT credential_id,channel_id INTO actual_credential,actual_channel FROM upstream_attempts WHERE id=NEW.attempt_id;
 SELECT credential_id,channel_id INTO bound_credential,bound_channel FROM credential_procurement_bindings WHERE id=NEW.binding_id AND account_id=NEW.account_id;
 IF actual_credential IS NULL OR bound_credential IS NULL OR (actual_credential,actual_channel) IS DISTINCT FROM (bound_credential,bound_channel) THEN
  RAISE EXCEPTION 'procurement reservation attempt binding mismatch' USING ERRCODE='23503';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_recovery_gate(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_recovery_gate() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'recovery singleton cannot be deleted'; END IF;
 IF NEW.epoch<>OLD.epoch THEN
  IF NEW.epoch<>OLD.epoch+1 OR NOT NEW.required OR NEW.proposal_id IS NOT NULL OR NEW.approver IS NOT NULL THEN
   RAISE EXCEPTION 'invalid recovery epoch transition';
  END IF;
 ELSE
  IF NOT OLD.required AND NEW.required THEN RAISE EXCEPTION 'recovery latch requires a new epoch'; END IF;
  IF OLD.proposal_id IS NOT NULL AND
   (NEW.proposal_id,NEW.requester,NEW.evidence_ref,NEW.reason,NEW.proposed_at) IS DISTINCT FROM
   (OLD.proposal_id,OLD.requester,OLD.evidence_ref,OLD.reason,OLD.proposed_at) THEN
   RAISE EXCEPTION 'immutable recovery proposal';
  END IF;
  IF OLD.required AND NOT NEW.required AND (NEW.proposal_id IS NULL OR NEW.approver IS NULL OR NEW.approved_at IS NULL) THEN
   RAISE EXCEPTION 'recovery checker approval required';
  END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_request_retail_snapshot(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_request_retail_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.key_id,NEW.owner_id,NEW.model_id,NEW.profile_version,NEW.protocol,NEW.execution_kind,NEW.result_kind,NEW.request_hmac,NEW.streaming,NEW.route_revision)
   IS DISTINCT FROM ROW(OLD.tenant_id,OLD.key_id,OLD.owner_id,OLD.model_id,OLD.profile_version,OLD.protocol,OLD.execution_kind,OLD.result_kind,OLD.request_hmac,OLD.streaming,OLD.route_revision) THEN
   RAISE EXCEPTION 'immutable request identity';
  END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_settlement_history(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_settlement_history() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable settlement'; END IF;
 IF (to_jsonb(NEW)-'refunded_amount') IS DISTINCT FROM (to_jsonb(OLD)-'refunded_amount') OR NEW.refunded_amount<OLD.refunded_amount THEN
  RAISE EXCEPTION 'immutable settlement facts';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: guard_tenant_pricing_change(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION guard_tenant_pricing_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE gstate TEXT; op pricing_operations%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.price_group_id IS NOT NULL OR NEW.pricing_revision<>1
     OR NEW.pricing_operation_id IS NOT NULL THEN
   RAISE EXCEPTION 'new tenant must start with default pricing';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.price_group_id IS NOT DISTINCT FROM OLD.price_group_id THEN
  IF ROW(NEW.pricing_revision,NEW.pricing_operation_id)
    IS DISTINCT FROM ROW(OLD.pricing_revision,OLD.pricing_operation_id) THEN
   RAISE EXCEPTION 'tenant pricing no-op changed revision';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.pricing_revision=9223372036854775807
    OR NEW.pricing_revision<>OLD.pricing_revision+1
    OR NEW.pricing_operation_id IS NULL
    OR NEW.pricing_operation_id IS NOT DISTINCT FROM OLD.pricing_operation_id THEN
  RAISE EXCEPTION 'invalid tenant pricing revision';
 END IF;
 -- 在线 service 已按 tenant -> actor -> group 取得同一行锁。
 IF NEW.price_group_id IS NOT NULL THEN
  SELECT state INTO STRICT gstate FROM price_groups WHERE id=NEW.price_group_id FOR SHARE;
  IF gstate<>'active' THEN RAISE EXCEPTION 'price group retired'; END IF;
 END IF;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 IF op.created_txid<>txid_current() OR op.kind<>'tenant_pricing.assign'
    OR op.resource_id<>NEW.id OR op.revision<>NEW.pricing_revision THEN
  RAISE EXCEPTION 'tenant pricing operation mismatch';
 END IF;
 INSERT INTO tenant_pricing_assignments(operation_id,tenant_id,before_group_id,after_group_id,
  before_revision,after_revision,actor,created_txid,created_at)
 VALUES(op.operation_id,NEW.id,OLD.price_group_id,NEW.price_group_id,
  OLD.pricing_revision,NEW.pricing_revision,op.actor,txid_current(),op.created_at);
 RETURN NEW;
END $$;


--
-- Name: identity_challenge_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION identity_challenge_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.consumed_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR (NEW.purpose='registration' AND NEW.registration_email_ciphertext IS NULL) THEN RAISE EXCEPTION 'identity challenge initial state'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.created_at>clock_timestamp()-interval '7 days' OR (OLD.consumed_at IS NULL AND OLD.revoked_at IS NULL AND OLD.expires_at>clock_timestamp()) THEN RAISE EXCEPTION 'identity challenge retention'; END IF;
  RETURN OLD;
 END IF;
 IF current_user=(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND OLD.registration_email_ciphertext IS NOT NULL AND NEW.registration_email_ciphertext IS NOT NULL AND
  (to_jsonb(NEW)-'registration_email_ciphertext')=(to_jsonb(OLD)-'registration_email_ciphertext') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.subject_id,NEW.purpose,NEW.token_digest,NEW.user_id,NEW.password_revision,NEW.created_at,NEW.expires_at) IS DISTINCT FROM
    (OLD.id,OLD.subject_id,OLD.purpose,OLD.token_digest,OLD.user_id,OLD.password_revision,OLD.created_at,OLD.expires_at) THEN RAISE EXCEPTION 'identity challenge immutable'; END IF;
 IF (OLD.consumed_at IS NOT NULL AND NEW.consumed_at IS DISTINCT FROM OLD.consumed_at) OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN RAISE EXCEPTION 'identity challenge terminal'; END IF;
 IF NEW.registration_email_ciphertext IS DISTINCT FROM OLD.registration_email_ciphertext AND
   (NEW.registration_email_ciphertext IS NOT NULL OR (NEW.consumed_at IS NULL AND NEW.revoked_at IS NULL AND NEW.expires_at>clock_timestamp())) THEN RAISE EXCEPTION 'identity challenge secret immutable'; END IF;
 RETURN NEW;
END $$;


--
-- Name: identity_initialization_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION identity_initialization_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'identity initialization immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initialized_by AND platform_role='admin') THEN RAISE EXCEPTION 'identity initialization requires admin'; END IF;
 IF current_user<>(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND
   (NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initialized_by AND status='active') OR (SELECT count(*) FROM users)<>1) THEN
   RAISE EXCEPTION 'identity initialization requires bootstrap';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: identity_outbox_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION identity_outbox_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.attempt_count<>0 OR NEW.claim_generation<>0 OR NEW.lease_until IS NOT NULL OR NEW.sent_at IS NOT NULL OR NEW.payload_ciphertext IS NULL OR NEW.last_error_code<>'' THEN RAISE EXCEPTION 'identity outbox initial state'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.state IN ('pending','sending') OR OLD.created_at>clock_timestamp()-interval '7 days' THEN RAISE EXCEPTION 'identity outbox retention'; END IF;
  RETURN OLD;
 END IF;
 IF current_user=(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND OLD.payload_ciphertext IS NOT NULL AND NEW.payload_ciphertext IS NOT NULL AND
  (to_jsonb(NEW)-'payload_ciphertext')=(to_jsonb(OLD)-'payload_ciphertext') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.challenge_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.challenge_id,OLD.created_at) THEN RAISE EXCEPTION 'identity outbox immutable'; END IF;
 IF OLD.state IN ('sent','dead','cancelled') THEN
  IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'identity outbox terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.state='sending' AND (OLD.state='pending' OR (OLD.state='sending' AND OLD.lease_until<=clock_timestamp())) THEN
  IF NEW.attempt_count<>OLD.attempt_count+1 OR NEW.claim_generation<>OLD.claim_generation+1 OR OLD.attempt_count>=5 OR NEW.lease_until<=clock_timestamp() OR NEW.lease_until>clock_timestamp()+interval '60 seconds' OR NEW.payload_ciphertext IS DISTINCT FROM OLD.payload_ciphertext THEN RAISE EXCEPTION 'identity outbox claim'; END IF;
 ELSIF OLD.state='sending' AND NEW.state IN ('pending','sent','dead') OR NEW.state='cancelled' THEN
  IF (NEW.attempt_count,NEW.claim_generation) IS DISTINCT FROM (OLD.attempt_count,OLD.claim_generation) THEN RAISE EXCEPTION 'identity outbox counter'; END IF;
  IF NEW.state='pending' AND (NEW.payload_ciphertext IS DISTINCT FROM OLD.payload_ciphertext OR NEW.next_attempt_at<clock_timestamp()) THEN RAISE EXCEPTION 'identity outbox retry'; END IF;
  IF NEW.state='sent' AND NEW.last_error_code<>'' THEN RAISE EXCEPTION 'identity outbox sent error'; END IF;
 ELSE RAISE EXCEPTION 'identity outbox transition'; END IF;
 RETURN NEW;
END $$;


--
-- Name: identity_password_revision_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION identity_password_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.password_revision:=1;
 ELSIF NEW.password_hash IS DISTINCT FROM OLD.password_hash THEN NEW.password_revision:=OLD.password_revision+1;
 ELSE NEW.password_revision:=OLD.password_revision; END IF;
 RETURN NEW;
END $$;


--
-- Name: identity_subject_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION identity_subject_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE db_now TIMESTAMPTZ:=clock_timestamp();
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.window_count NOT IN (0,1) OR NEW.window_started_at>db_now OR NEW.last_seen_at>db_now THEN RAISE EXCEPTION 'identity subject initial state'; END IF;
  NEW.window_started_at:=db_now;
  IF NEW.window_count=1 THEN NEW.last_seen_at:=db_now; NEW.next_issue_at:=db_now+interval '60 seconds'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.last_seen_at>clock_timestamp()-interval '24 hours' OR EXISTS(SELECT 1 FROM identity_challenges WHERE subject_id=OLD.id) THEN RAISE EXCEPTION 'identity subject retention'; END IF;
  RETURN OLD;
 END IF;
 IF (NEW.id,NEW.purpose,NEW.email_digest) IS DISTINCT FROM (OLD.id,OLD.purpose,OLD.email_digest) OR NEW.last_seen_at<OLD.last_seen_at OR NEW.last_seen_at>db_now OR NEW.next_issue_at<OLD.next_issue_at THEN RAISE EXCEPTION 'identity subject immutable'; END IF;
 IF NEW.window_started_at=OLD.window_started_at THEN
  IF NEW.window_count<OLD.window_count OR NEW.window_count>OLD.window_count+1 THEN RAISE EXCEPTION 'identity subject counter'; END IF;
 ELSIF NEW.window_started_at<OLD.window_started_at+interval '1 hour' OR NEW.window_started_at>db_now OR db_now<OLD.window_started_at+interval '1 hour' OR NEW.window_count<>1 THEN RAISE EXCEPTION 'identity subject window'; END IF;
 IF NEW.window_count>OLD.window_count OR NEW.window_started_at<>OLD.window_started_at THEN
  IF OLD.next_issue_at>db_now THEN RAISE EXCEPTION 'identity subject cooldown'; END IF;
  IF NEW.window_started_at<>OLD.window_started_at THEN NEW.window_started_at:=db_now; END IF;
  NEW.last_seen_at:=db_now; NEW.next_issue_at:=db_now+interval '60 seconds';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: media_amount(bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_amount(quantity bigint, rate_per_million bigint) RETURNS numeric
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT CASE WHEN quantity IS NULL OR rate_per_million IS NULL OR quantity<0 OR rate_per_million<0 THEN NULL
   ELSE div(quantity::numeric*rate_per_million+999999,1000000) END
$$;


--
-- Name: media_artifact_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_artifact_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE task_status text; task_max bigint; task_artifact_state text;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.task_id,NEW.artifact_reference,NEW.media_type,NEW.byte_length,NEW.sha256,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.task_id,OLD.artifact_reference,OLD.media_type,OLD.byte_length,OLD.sha256,OLD.created_at) THEN RAISE EXCEPTION 'media artifact identity is immutable'; END IF;
  IF OLD.body IS NOT NULL AND NEW.body IS DISTINCT FROM OLD.body THEN RAISE EXCEPTION 'media artifact body is immutable'; END IF;
  IF OLD.body IS NULL AND NEW.body IS NOT NULL AND (octet_length(NEW.body) IS DISTINCT FROM NEW.byte_length OR sha256(NEW.body) IS DISTINCT FROM NEW.sha256) THEN RAISE EXCEPTION 'media artifact body digest mismatch'; END IF;
  IF OLD.status='expired' AND NEW.status IS DISTINCT FROM OLD.status THEN RAISE EXCEPTION 'media artifact is terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.body IS NOT NULL THEN RAISE EXCEPTION 'media artifact body must be written after success'; END IF;
 SELECT status,max_artifact_bytes,artifact_state INTO task_status,task_max,task_artifact_state FROM media_tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 IF task_status IS DISTINCT FROM 'succeeded' OR task_artifact_state='expired' OR NEW.byte_length>task_max THEN RAISE EXCEPTION 'media artifact is not allowed'; END IF;
 RETURN NEW;
END $$;


--
-- Name: media_attempt_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_attempt_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.id,NEW.request_id,NEW.supplier_rate_per_million,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.request_id,OLD.supplier_rate_per_million,OLD.created_at) THEN RAISE EXCEPTION 'media attempt identity is immutable'; END IF;
 IF TG_OP='UPDATE' AND NOT ((OLD.delivery_state='prepared' AND NEW.delivery_state IN ('sending','failed','unknown')) OR (OLD.delivery_state='sending' AND NEW.delivery_state IN ('completed','failed','unknown')) OR NEW.delivery_state=OLD.delivery_state) THEN RAISE EXCEPTION 'invalid media attempt state transition'; END IF;
 IF NEW.delivery_state='unknown' AND NEW.provider_reference IS NOT NULL THEN RAISE EXCEPTION 'unknown media attempt cannot expose provider reference'; END IF;
 IF TG_OP='UPDATE' AND OLD.delivery_state='unknown' AND NEW.delivery_state IS DISTINCT FROM OLD.delivery_state THEN RAISE EXCEPTION 'unknown media attempt is terminal'; END IF;
 RETURN NEW;
END $$;


--
-- Name: media_observation_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_observation_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NEW.source='approved_reconciliation' AND NEW.usage_state='confirmed_final' THEN RAISE EXCEPTION 'reconciliation cannot confirm media supplier usage'; END IF;
 RETURN NULL;
END $$;


--
-- Name: media_profile_immutable(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_profile_immutable() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.model_key,NEW.operation,NEW.profile_version,NEW.unit,NEW.rate_per_million,NEW.supplier_rate_per_million,NEW.max_quantity,NEW.max_result_bytes,NEW.created_at)
    IS DISTINCT FROM ROW(OLD.model_key,OLD.operation,OLD.profile_version,OLD.unit,OLD.rate_per_million,OLD.supplier_rate_per_million,OLD.max_quantity,OLD.max_result_bytes,OLD.created_at) THEN
  RAISE EXCEPTION 'media model profile identity is immutable';
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'media model profile is immutable'; END IF;
 RETURN NEW;
END $$;


--
-- Name: media_request_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_request_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.key_id,NEW.owner_id,NEW.model_key,NEW.operation,NEW.profile_version,NEW.idempotency_key,NEW.request_digest,NEW.quantity_bound,NEW.created_at,NEW.deadline)
     IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.key_id,OLD.owner_id,OLD.model_key,OLD.operation,OLD.profile_version,OLD.idempotency_key,OLD.request_digest,OLD.quantity_bound,OLD.created_at,OLD.deadline) THEN
   RAISE EXCEPTION 'media request identity is immutable';
  END IF;
  IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='reserved' AND NEW.state IN ('pending','settled','released')) OR (OLD.state='pending' AND NEW.state IN ('settled','released')) OR NEW.state=OLD.state) THEN
   RAISE EXCEPTION 'invalid media request state transition';
  END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: media_reservation_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_reservation_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE expected numeric;
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.id,NEW.request_id,NEW.amount,NEW.rate_per_million,NEW.quantity_bound,NEW.deadline,NEW.journal_id,NEW.created_at)
   IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.request_id,OLD.amount,OLD.rate_per_million,OLD.quantity_bound,OLD.deadline,OLD.journal_id,OLD.created_at) THEN RAISE EXCEPTION 'media reservation identity is immutable'; END IF;
 IF TG_OP='UPDATE' THEN
  IF NOT ((OLD.status='reserved' AND NEW.status IN ('pending_reconciliation','settled','released')) OR (OLD.status='pending_reconciliation' AND NEW.status IN ('settled','released')) OR NEW.status=OLD.status) THEN RAISE EXCEPTION 'invalid media reservation state transition'; END IF;
 ELSE
  expected:=media_amount(NEW.quantity_bound,NEW.rate_per_million);
  IF expected IS NULL OR expected>9223372036854775807::numeric OR NEW.amount::numeric IS DISTINCT FROM expected THEN RAISE EXCEPTION 'media reservation amount mismatch'; END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: media_settlement_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_settlement_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE expected numeric; quantity bigint; supplier_rate bigint; usage_state text;
BEGIN
 IF NEW.status='released' AND (NEW.charged_amount<>0 OR NEW.raw_amount<>0) THEN RAISE EXCEPTION 'released media settlement must have zero charge'; END IF;
 IF NEW.status='settled' AND NEW.charged_amount<0 THEN RAISE EXCEPTION 'invalid media settlement'; END IF;
 SELECT o.quantity,o.usage_state,a.supplier_rate_per_million INTO quantity,usage_state,supplier_rate
 FROM media_observations o JOIN media_attempts a ON a.tenant_id=o.tenant_id AND a.id=o.attempt_id AND a.request_id=o.request_id
 WHERE o.tenant_id=NEW.tenant_id AND o.request_id=NEW.request_id AND o.attempt_id=NEW.attempt_id;
 IF NEW.supplier_cost_status='known' THEN
  IF usage_state IS DISTINCT FROM 'confirmed_final' OR quantity IS NULL THEN RAISE EXCEPTION 'known media supplier cost requires confirmed usage'; END IF;
  expected:=media_amount(quantity,supplier_rate);
  IF expected IS NULL OR expected>9223372036854775807::numeric OR NEW.supplier_cost_amount::numeric IS DISTINCT FROM expected THEN RAISE EXCEPTION 'media supplier cost mismatch'; END IF;
 ELSIF NEW.supplier_cost_amount IS NOT NULL THEN
  RAISE EXCEPTION 'unknown media supplier cost cannot carry amount';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: media_settlement_immutable(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_settlement_immutable() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='DELETE' OR (to_jsonb(NEW)-'refunded_amount') IS DISTINCT FROM (to_jsonb(OLD)-'refunded_amount') THEN RAISE EXCEPTION 'media settlement is immutable'; END IF;
 RETURN NEW;
END $$;


--
-- Name: media_task_event_apply(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_task_event_apply() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE next_status text; next_cancel text; prior text; ref text;
BEGIN
 SELECT cancel_resume_state,provider_reference INTO prior,ref FROM media_tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 next_status:=CASE NEW.kind WHEN 'submission_accepted' THEN 'queued' WHEN 'status_queued' THEN 'queued' WHEN 'status_running' THEN 'running' WHEN 'status_succeeded' THEN 'succeeded' WHEN 'status_failed' THEN 'failed' WHEN 'status_cancelled' THEN 'cancelled' WHEN 'submission_unknown' THEN 'submission_unknown' WHEN 'cancel_rejected' THEN coalesce(prior,'queued') ELSE NULL END;
 next_cancel:=CASE NEW.kind WHEN 'status_cancelled' THEN 'accepted' WHEN 'cancel_rejected' THEN 'rejected' ELSE NULL END;
 UPDATE media_tasks SET status=coalesce(next_status,status), cancel_state=coalesce(next_cancel,cancel_state), provider_reference=coalesce(provider_reference,NEW.provider_reference), observation_state='fresh'
  WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 IF NEW.kind='status_succeeded' THEN
  INSERT INTO media_task_artifacts(tenant_id,task_id,artifact_reference,media_type,byte_length,sha256,expires_at)
  VALUES(NEW.tenant_id,NEW.task_id,NEW.artifact_reference,NEW.artifact_media_type,NEW.artifact_bytes,NEW.artifact_sha256,NEW.artifact_expires_at);
  UPDATE media_tasks SET artifact_state='available' WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 ELSIF NEW.kind='artifact_expired' THEN
  UPDATE media_task_artifacts SET status='expired' WHERE tenant_id=NEW.tenant_id AND task_id=NEW.task_id AND status='available';
  UPDATE media_tasks SET artifact_state='expired' WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
 END IF;
 RETURN NULL;
END $$;


--
-- Name: media_task_event_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_task_event_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $_$
DECLARE s text; obs text; cancel_s text; artifact_s text; prior text; task_provider text;
BEGIN
 SELECT status,observation_state,cancel_state,artifact_state,cancel_resume_state,provider_reference INTO s,obs,cancel_s,artifact_s,prior,task_provider FROM media_tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id FOR UPDATE;
 IF NEW.kind IN ('submission_accepted','status_queued') AND s<>'submitting' AND NOT (NEW.kind='status_queued' AND s='submission_unknown') THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='submission_unknown' AND s<>'submitting' THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='status_running' AND s NOT IN ('queued','submission_unknown') THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind IN ('status_succeeded','status_failed') AND s NOT IN ('queued','running','submission_unknown') THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='status_cancelled' AND s<>'cancel_requested' THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='cancel_rejected' AND s<>'cancel_requested' THEN RAISE EXCEPTION 'media event state mismatch'; END IF;
 IF NEW.kind='artifact_expired' AND (s<>'succeeded' OR artifact_s<>'available') THEN RAISE EXCEPTION 'media artifact expiration mismatch'; END IF;
 IF NEW.kind IN ('submission_accepted','status_queued','status_running','status_succeeded') AND NEW.provider_reference IS NULL AND task_provider IS NULL THEN RAISE EXCEPTION 'media event requires provider reference'; END IF;
 IF NEW.kind='status_succeeded' AND (NEW.artifact_reference IS NULL OR NEW.artifact_media_type IS NULL OR NEW.artifact_bytes IS NULL OR NEW.artifact_sha256 IS NULL OR NEW.artifact_expires_at IS NULL) THEN RAISE EXCEPTION 'media success requires artifact'; END IF;
 IF NEW.kind='status_failed' AND (NEW.failure_code IS NULL OR NEW.failure_code !~ '^[a-z0-9_.:-]{1,64}$') THEN RAISE EXCEPTION 'media failure requires code'; END IF;
 IF NEW.kind<>'status_succeeded' AND (NEW.artifact_reference IS NOT NULL OR NEW.artifact_media_type IS NOT NULL OR NEW.artifact_bytes IS NOT NULL OR NEW.artifact_sha256 IS NOT NULL OR NEW.artifact_expires_at IS NOT NULL) THEN RAISE EXCEPTION 'media event artifact is unexpected'; END IF;
 IF NEW.kind<>'status_failed' AND NEW.failure_code IS NOT NULL THEN RAISE EXCEPTION 'media event failure is unexpected'; END IF;
 IF NEW.kind='submission_unknown' AND NEW.provider_reference IS NOT NULL THEN RAISE EXCEPTION 'unknown submission has provider reference'; END IF;
 RETURN NEW;
END $_$;


--
-- Name: media_task_identity_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_task_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE request_owner text; request_model text; reservation_request text; attempt_request text; profile_model text; profile_schema text; profile_digest bytea;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.request_id,NEW.owner_id,NEW.attempt_id,NEW.reservation_id,NEW.source_id,NEW.model_id,NEW.profile_version,NEW.schema_id,NEW.schema_digest,NEW.variant_key,NEW.idempotency_key,NEW.submit_fingerprint,NEW.input_digest,NEW.submit_deadline,NEW.observe_deadline,NEW.result_deadline,NEW.artifact_deadline,NEW.max_artifact_bytes,NEW.created_at)
     IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.request_id,OLD.owner_id,OLD.attempt_id,OLD.reservation_id,OLD.source_id,OLD.model_id,OLD.profile_version,OLD.schema_id,OLD.schema_digest,OLD.variant_key,OLD.idempotency_key,OLD.submit_fingerprint,OLD.input_digest,OLD.submit_deadline,OLD.observe_deadline,OLD.result_deadline,OLD.artifact_deadline,OLD.max_artifact_bytes,OLD.created_at) THEN
   RAISE EXCEPTION 'media task identity is immutable';
  END IF;
  IF OLD.provider_reference IS NOT NULL AND NEW.provider_reference IS DISTINCT FROM OLD.provider_reference THEN
   RAISE EXCEPTION 'media provider reference is immutable';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
   (OLD.status='prepared' AND NEW.status='submitting') OR
   (OLD.status='submitting' AND NEW.status IN ('queued','submission_unknown')) OR
   (OLD.status='queued' AND NEW.status IN ('running','cancel_requested','failed','succeeded')) OR
   (OLD.status='running' AND NEW.status IN ('cancel_requested','failed','succeeded')) OR
   (OLD.status='submission_unknown' AND NEW.status IN ('queued','running','failed','succeeded','cancel_requested')) OR
   (OLD.status='cancel_requested' AND NEW.status IN ('queued','running','succeeded','failed','cancelled'))
  ) THEN RAISE EXCEPTION 'invalid media task state transition'; END IF;
  IF OLD.cancel_state='none' AND NEW.cancel_state NOT IN ('none','requested') THEN RAISE EXCEPTION 'invalid media cancel state'; END IF;
  IF OLD.cancel_state='requested' AND NEW.cancel_state NOT IN ('requested','accepted','rejected') THEN RAISE EXCEPTION 'invalid media cancel state'; END IF;
  IF OLD.cancel_state IN ('accepted','rejected') AND NEW.cancel_state IS DISTINCT FROM OLD.cancel_state THEN RAISE EXCEPTION 'media cancel state is terminal'; END IF;
  IF OLD.artifact_state='none' AND NEW.artifact_state NOT IN ('none','available') THEN RAISE EXCEPTION 'invalid media artifact state'; END IF;
  IF OLD.artifact_state='available' AND NEW.artifact_state NOT IN ('available','expired') THEN RAISE EXCEPTION 'invalid media artifact state'; END IF;
  IF OLD.artifact_state='expired' AND NEW.artifact_state IS DISTINCT FROM OLD.artifact_state THEN RAISE EXCEPTION 'media artifact state is terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.status<>'prepared' OR NEW.observation_state<>'unobserved' OR NEW.cancel_state<>'none' OR NEW.artifact_state<>'none' THEN
  RAISE EXCEPTION 'media task must be born prepared';
 END IF;
 SELECT owner_id,model_id INTO request_owner,request_model FROM request_records WHERE tenant_id=NEW.tenant_id AND id=NEW.request_id;
 SELECT request_id INTO reservation_request FROM reservations WHERE tenant_id=NEW.tenant_id AND id=NEW.reservation_id;
 IF NEW.attempt_id IS NOT NULL THEN
  SELECT request_id INTO attempt_request FROM upstream_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
 END IF;
 SELECT c.model_id,c.schema_id,c.schema_digest INTO profile_model,profile_schema,profile_digest FROM model_capability_versions c WHERE c.id=NEW.profile_version;
 IF request_owner IS NULL OR request_owner IS DISTINCT FROM NEW.owner_id OR request_model IS DISTINCT FROM NEW.model_id
    OR reservation_request IS DISTINCT FROM NEW.request_id OR (NEW.attempt_id IS NOT NULL AND attempt_request IS DISTINCT FROM NEW.request_id)
    OR profile_model IS DISTINCT FROM NEW.model_id OR profile_schema IS DISTINCT FROM NEW.schema_id OR profile_digest IS DISTINCT FROM NEW.schema_digest THEN
  RAISE EXCEPTION 'media task binding mismatch';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: media_uuid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION media_uuid(v text) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $_$
 SELECT coalesce(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND v <> '00000000-0000-0000-0000-000000000000', false)
$_$;


--
-- Name: meter_attempt_bounds_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_bounds_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE matched bigint; total bigint; request_input bigint; request_output bigint;
BEGIN
 SELECT input_bound, output_bound INTO request_input, request_output FROM request_meter_snapshots WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 SELECT count(*) INTO total FROM attempt_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND attempt_id=NEW.attempt_id;
 SELECT count(*) INTO matched FROM attempt_meter_bound_lines
  WHERE tenant_id=NEW.tenant_id AND attempt_id=NEW.attempt_id AND unit='token'
    AND ((side IN ('retail','supplier') AND dimension='text_input_token' AND quantity=NEW.input_bound)
      OR (side IN ('retail','supplier') AND dimension='text_output_token' AND quantity=NEW.output_bound));
 IF total IS DISTINCT FROM 4 OR matched IS DISTINCT FROM 4 OR NEW.input_bound IS DISTINCT FROM request_input OR NEW.output_bound IS DISTINCT FROM request_output THEN
  RAISE EXCEPTION 'attempt bounds are incomplete';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_attempt_cost_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_cost_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE corr_amount bigint; corr_basis text; in_q bigint; out_q bigint; in_n bigint; in_d bigint; out_n bigint; out_d bigint; basis text; version text; expected numeric; end_reason text; supplier_state text;
BEGIN
 IF NEW.cost_status='unknown' AND NEW.cost_amount IS NULL THEN RETURN NULL; END IF;
 IF TG_OP='UPDATE' AND OLD.latest_cost_correction_id IS NOT NULL AND NEW.latest_cost_finalization_id IS NOT NULL THEN
  RAISE EXCEPTION 'supplier cost correction is authoritative';
 END IF;
 IF NEW.latest_cost_correction_id IS NOT NULL THEN
  SELECT c.amount, c.basis INTO corr_amount, corr_basis FROM provider_cost_corrections c
   WHERE c.id=NEW.latest_cost_correction_id AND c.tenant_id=NEW.tenant_id AND c.attempt_id=NEW.id;
  IF NOT FOUND OR NEW.cost_amount IS DISTINCT FROM corr_amount::numeric OR NEW.cost_status IS DISTINCT FROM corr_basis THEN
   RAISE EXCEPTION 'supplier cost does not match correction';
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.latest_cost_finalization_id IS NOT NULL THEN
  SELECT qi.quantity, qo.quantity, s.cost_version_id, s.cost_basis, f.transport_end_reason, f.supplier_usage_state
   INTO in_q, out_q, version, basis, end_reason, supplier_state
   FROM finalization_records f
   LEFT JOIN meter_event_quantities qi ON qi.event_id=f.event_id AND qi.dimension='text_input_token'
   LEFT JOIN meter_event_quantities qo ON qo.event_id=f.event_id AND qo.dimension='text_output_token'
   LEFT JOIN attempt_meter_snapshots s ON s.attempt_id=f.attempt_id
   WHERE f.id=NEW.latest_cost_finalization_id AND f.tenant_id=NEW.tenant_id AND f.attempt_id=NEW.id;
  IF NOT FOUND THEN RAISE EXCEPTION 'supplier cost finalization is missing'; END IF;
  IF supplier_state='confirmed_final' THEN
   SELECT l.numerator, l.denominator INTO in_n, in_d FROM cost_rate_lines l WHERE l.cost_version_id=version AND l.dimension='text_input_token';
   SELECT l.numerator, l.denominator INTO out_n, out_d FROM cost_rate_lines l WHERE l.cost_version_id=version AND l.dimension='text_output_token';
   expected := meter_token_amount(in_q, out_q, in_n, in_d, out_n, out_d);
   IF expected IS NOT NULL AND NEW.cost_amount = expected AND NEW.cost_status = basis THEN RETURN NULL; END IF;
  ELSIF NEW.cost_amount=0 AND NEW.cost_status='contract' AND end_reason IN ('not_sent','rejected_no_consumption') THEN
   RETURN NULL;
  END IF;
  RAISE EXCEPTION 'supplier cost does not match final usage';
 END IF;
 RAISE EXCEPTION 'supplier cost has no source pointer';
END $$;


--
-- Name: meter_attempt_matches_request(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_matches_request() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM request_meter_snapshots r
  WHERE r.tenant_id=NEW.tenant_id AND r.request_id=NEW.request_id AND r.model_id=NEW.model_id
    AND r.profile_version=NEW.profile_version AND r.schema_id=NEW.schema_id AND r.schema_digest=NEW.schema_digest
    AND r.variant_key=NEW.variant_key AND r.input_bound=NEW.input_bound AND r.output_bound=NEW.output_bound
 ) THEN
  RAISE EXCEPTION 'attempt quote does not match request';
 END IF;
 IF NEW.cost_version_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM upstream_attempts a JOIN cost_versions c ON c.id=NEW.cost_version_id
  WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id
   AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
   AND c.schema_id=NEW.schema_id AND c.schema_digest=NEW.schema_digest AND c.variant_key=NEW.variant_key
   AND c.basis=NEW.cost_basis
 ) THEN
  RAISE EXCEPTION 'attempt quote domain does not match original attempt';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_attempt_plan_canonical(text, text, text, bytea, bigint, boolean, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text) RETURNS bytea
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE model text; protocol text; contract bytea; input_max bigint; output_max bigint; profile bytea; request_plan bytea; result_kind text; profile_hex text;
BEGIN
 request_plan := meter_request_plan_canonical(profile_version, model_id, schema_id, schema_digest, output_limit, streaming, request_hmac);
 IF request_plan IS NULL THEN RETURN NULL; END IF;
 SELECT c.profile->>'model', c.protocol, c.contract_digest, (c.profile->>'input_token_upper_bound')::bigint, (c.profile->>'output_limit')::bigint
  INTO model, protocol, contract, input_max, output_max
  FROM model_capability_versions c WHERE c.id=profile_version AND c.model_id=meter_attempt_plan_canonical.model_id AND c.schema_id=meter_attempt_plan_canonical.schema_id AND c.schema_digest=meter_attempt_plan_canonical.schema_digest;
 profile := meter_profile_canonical(profile_version, model, protocol, schema_id, schema_digest, contract, input_max, output_max);
 profile_hex := encode(meter_profile_digest(schema_digest, profile), 'hex');
 result_kind := CASE WHEN streaming THEN 'events' ELSE 'json' END;
 RETURN convert_to(format('{"version":"1","scope":"attempt","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kind":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","input_hmac":"%s","output_limit":"%s","candidate_profile_digests":["%s"],"selected_profile_digest":"%s","parent_plan_digest":"%s","retail_bounds":%s,"supplier_bounds":%s}',
  model, protocol, result_kind, schema_id, encode(schema_digest,'hex'), request_hmac, output_limit, profile_hex, profile_hex,
  encode(meter_plan_digest(schema_digest, request_plan), 'hex'),
  convert_from(meter_bounds_canonical(schema_id,'retail',input_max,output_limit),'UTF8'),
  convert_from(meter_bounds_canonical(schema_id,'supplier',input_max,output_limit),'UTF8')), 'UTF8');
END $$;


--
-- Name: meter_attempt_plan_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_plan_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE hmac text; streaming boolean; ready boolean; canonical bytea; digest bytea; request_plan_digest bytea;
BEGIN
 SELECT r.request_hmac, r.streaming, q.plan_canonical IS NOT NULL, q.plan_digest INTO hmac, streaming, ready, request_plan_digest
  FROM request_records r JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
  WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
 IF NOT coalesce(ready, false) THEN
  IF NEW.plan_canonical IS NOT NULL OR NEW.plan_digest IS NOT NULL OR NEW.parent_plan_digest IS NOT NULL THEN RAISE EXCEPTION 'attempt plan does not match request'; END IF;
  RETURN NULL;
 END IF;
 canonical := meter_attempt_plan_canonical(NEW.profile_version, NEW.model_id, NEW.schema_id, NEW.schema_digest, NEW.output_bound, streaming, hmac);
 digest := meter_plan_digest(NEW.schema_digest, canonical);
 IF canonical IS NULL OR NEW.plan_canonical IS DISTINCT FROM canonical OR NEW.plan_digest IS DISTINCT FROM digest OR NEW.parent_plan_digest IS DISTINCT FROM request_plan_digest THEN
  RAISE EXCEPTION 'attempt plan does not match request';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_attempt_quote_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_quote_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE canonical bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.supplier_quote_canonical IS NOT NULL OR NEW.supplier_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'attempt quote does not match cost';
  END IF;
  RETURN NULL;
 END IF;
 canonical := meter_supplier_quote_canonical(NEW.schema_id, NEW.schema_digest, NEW.plan_digest, NEW.quote_status, NEW.unknown_reason, NEW.cost_version_id, NEW.supplier_rates_canonical, NEW.input_bound, NEW.output_bound);
 IF canonical IS NULL OR NEW.supplier_quote_canonical IS DISTINCT FROM canonical OR NEW.supplier_quote_digest IS DISTINCT FROM meter_quote_digest(NEW.schema_digest, canonical) THEN
  RAISE EXCEPTION 'attempt quote does not match cost';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_attempt_snapshot_present(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_snapshot_present() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM attempt_meter_snapshots WHERE attempt_id=NEW.id AND tenant_id=NEW.tenant_id AND request_id=NEW.request_id) THEN
  RAISE EXCEPTION 'attempt requires one meter snapshot';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_attempt_supplier_bounds(text, text, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_supplier_bounds(tenant_id text, request_id text, attempt_id text) RETURNS jsonb
    LANGUAGE plpgsql STABLE
    
    AS $_$
DECLARE sid text; input_max bigint; output_max bigint; total bigint; matched bigint; mask_count bigint; entries jsonb;
BEGIN
 SELECT s.schema_id,s.input_bound,s.output_bound INTO sid,input_max,output_max
 FROM attempt_meter_snapshots s
 JOIN upstream_attempts a ON a.tenant_id=s.tenant_id AND a.request_id=s.request_id AND a.id=s.attempt_id
 JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest
 WHERE s.tenant_id=$1 AND s.request_id=$2 AND s.attempt_id=$3 AND s.variant_key='default';
 IF NOT FOUND THEN RAISE EXCEPTION 'original supplier bounds are unavailable'; END IF;
 SELECT count(*) INTO mask_count FROM meter_schema_dimensions d WHERE d.schema_id=sid AND d.supplier;
 SELECT count(*),count(*) FILTER (WHERE b.tenant_id=$1 AND d.supplier AND b.unit=d.unit
   AND b.unit='token' AND b.dimension IN ('text_input_token','text_output_token')
   AND b.quantity BETWEEN 0 AND d.maximum_quantity
   AND b.quantity=CASE b.dimension WHEN 'text_input_token' THEN input_max WHEN 'text_output_token' THEN output_max END),
  jsonb_agg(jsonb_build_object('dimension',b.dimension,'unit',b.unit,'quantity',b.quantity::text) ORDER BY b.dimension COLLATE "C")
 INTO total,matched,entries
 FROM attempt_meter_bound_lines b
 LEFT JOIN meter_schema_dimensions d ON d.schema_id=sid AND d.dimension=b.dimension
 WHERE b.attempt_id=$3 AND b.side='supplier';
 IF mask_count<>2 OR total<>2 OR matched<>2 THEN RAISE EXCEPTION 'original supplier bounds are incomplete'; END IF;
 RETURN jsonb_build_object('schema_id',sid,'version','1','side','supplier','bounds',entries);
END $_$;


--
-- Name: meter_attempt_vectors_match(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_attempt_vectors_match() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE rb bytea; sb bytea; rates bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL
   OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL
   OR NEW.supplier_rates_canonical IS NOT NULL OR NEW.supplier_rates_digest IS NOT NULL OR NEW.supplier_meter_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'attempt meter vectors require plan';
  END IF;
  RETURN NULL;
 END IF;
 rb := meter_bounds_canonical(NEW.schema_id,'retail',NEW.input_bound,NEW.output_bound);
 sb := meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 SELECT c.rates_canonical INTO rates FROM cost_versions c WHERE c.id=NEW.cost_version_id AND c.basis IN ('contract','invoice');
 IF NEW.retail_bounds_canonical IS DISTINCT FROM rb OR NEW.supplier_bounds_canonical IS DISTINCT FROM sb
  OR NEW.retail_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'retail',rb)
  OR NEW.supplier_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'supplier',sb)
  OR (rates IS NULL AND (NEW.supplier_rates_canonical IS NOT NULL OR NEW.supplier_rates_digest IS NOT NULL OR NEW.supplier_meter_quote_digest IS NOT NULL))
  OR (rates IS NOT NULL AND (NEW.supplier_rates_canonical IS DISTINCT FROM rates
    OR NEW.supplier_rates_digest IS DISTINCT FROM meter_rates_digest(NEW.schema_digest,'supplier',rates)
    OR NEW.supplier_meter_quote_digest IS DISTINCT FROM meter_pure_quote_digest(NEW.schema_digest,'supplier',sb,rates))) THEN
  RAISE EXCEPTION 'attempt meter vectors mismatch';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_binding_birth(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_binding_birth() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE route_stamp xid8; route_profile text;
BEGIN
 SELECT p.created_xid,p.profile_version INTO route_stamp,route_profile
   FROM route_policy_versions p WHERE p.id=NEW.route_policy_version_id;
 IF NOT FOUND OR route_stamp IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'model binding requires same-transaction route';
 END IF;
 IF NEW.variant_key IS DISTINCT FROM 'default' THEN
  RAISE EXCEPTION 'model binding variant is invalid';
 END IF;
 NEW.created_xid := pg_current_xact_id();
 RETURN NEW;
END $$;


--
-- Name: meter_binding_cap_birth(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_binding_cap_birth() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE binding_stamp xid8; sid text;
BEGIN
 SELECT b.created_xid,c.schema_id INTO binding_stamp,sid
   FROM model_bindings b
   JOIN route_policy_versions p ON p.id=b.route_policy_version_id
   JOIN model_capability_versions c ON c.id=p.profile_version AND c.model_id=p.model_id
  WHERE b.id=NEW.binding_id;
 IF NOT FOUND OR binding_stamp IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'binding cost cap requires same-transaction binding';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM meter_schema_dimensions d
   WHERE d.schema_id=sid AND d.supplier AND d.dimension=NEW.dimension AND d.unit=NEW.unit
 ) OR meter_gcd(NEW.numerator,NEW.denominator)<>1 THEN
  RAISE EXCEPTION 'binding cost cap is not in the supplier schema';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: meter_bounds_canonical(text, text, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_bounds_canonical(schema_id text, side text, input_qty bigint, output_qty bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT convert_to(format('{"schema_id":"%s","version":"1","side":"%s","bounds":[{"dimension":"text_input_token","unit":"token","quantity":"%s"},{"dimension":"text_output_token","unit":"token","quantity":"%s"}]}', schema_id, side, input_qty, output_qty), 'UTF8')
$$;


--
-- Name: meter_bounds_digest(bytea, text, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_bounds_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.bounds.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;


--
-- Name: meter_capability_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_capability_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $_$
DECLARE pid text; sid text; protocol text; profile_model text; profile_version text;
 input_max bigint; output_max bigint; schema_count bigint; bound_count bigint;
 matched_bounds bigint; variant_count bigint; schema_input bigint; schema_output bigint;
 profile_hash bytea;
BEGIN
 pid := coalesce(to_jsonb(NEW)->>'id',to_jsonb(NEW)->>'profile_version');
 SELECT c.schema_id,c.protocol,c.profile->>'model',c.profile->>'version',
        (c.profile->>'input_token_upper_bound')::bigint,
        (c.profile->>'output_limit')::bigint
   INTO sid,protocol,profile_model,profile_version,input_max,output_max
   FROM model_capability_versions c
  WHERE c.id=pid;
 IF NOT FOUND THEN RAISE EXCEPTION 'capability root is missing'; END IF;
 IF pid !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    OR pid='00000000-0000-0000-0000-000000000000'
    OR profile_version IS DISTINCT FROM pid THEN
  RAISE EXCEPTION 'capability profile identity is invalid';
 END IF;
 SELECT m.public_name INTO STRICT profile_model FROM models m
  WHERE m.id=(SELECT model_id FROM model_capability_versions WHERE id=pid);
 IF (SELECT profile->>'model' FROM model_capability_versions WHERE id=pid) IS DISTINCT FROM profile_model THEN
  RAISE EXCEPTION 'capability profile model is invalid';
 END IF;
 profile_hash := sha256(convert_to((SELECT profile::text FROM model_capability_versions WHERE id=pid),'UTF8'));
 IF (SELECT c.protocol FROM model_capability_versions c WHERE c.id=pid) IS DISTINCT FROM
      (SELECT c.profile->>'protocol' FROM model_capability_versions c WHERE c.id=pid)
    OR (SELECT c.contract_digest FROM model_capability_versions c WHERE c.id=pid) IS DISTINCT FROM profile_hash
    OR (SELECT c.profile_digest FROM model_capability_versions c WHERE c.id=pid) IS DISTINCT FROM profile_hash THEN
  RAISE EXCEPTION 'capability profile digest is invalid';
 END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') AND output_max<>0 THEN
  RAISE EXCEPTION 'input-only capability requires a zero output bound';
 END IF;
 SELECT count(*) INTO variant_count FROM model_capability_variants v
  WHERE v.profile_version=pid AND v.variant_key='default';
 SELECT count(*), count(*) FILTER (WHERE unit='token' AND
   ((side IN ('retail','supplier') AND dimension='text_input_token' AND quantity=input_max)
    OR (side IN ('retail','supplier') AND dimension='text_output_token' AND quantity=output_max)))
   INTO bound_count,matched_bounds
   FROM model_capability_bound_lines b WHERE b.profile_version=pid;
 IF variant_count<>1 OR bound_count<>4 OR matched_bounds<>4 THEN
  RAISE EXCEPTION 'capability bounds are incomplete';
 END IF;
 SELECT count(*),
        max(maximum_quantity) FILTER (WHERE dimension='text_input_token'),
        max(maximum_quantity) FILTER (WHERE dimension='text_output_token')
   INTO schema_count,schema_input,schema_output
   FROM meter_schema_dimensions d
  WHERE d.schema_id=sid AND d.unit='token' AND d.retail AND d.supplier
    AND d.dimension IN ('text_input_token','text_output_token');
 IF schema_count<>2 OR input_max IS NULL OR output_max IS NULL
    OR input_max<=0 OR output_max<0
    OR input_max>schema_input OR output_max>schema_output THEN
  RAISE EXCEPTION 'capability bounds exceed schema';
 END IF;
 RETURN NULL;
END $_$;


--
-- Name: meter_cost_lines_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_cost_lines_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE cid text; basis text; sid text; sdigest bytea; stored bytea; stored_digest bytea; n integer; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea;
BEGIN
 IF TG_TABLE_NAME='cost_versions' THEN cid := NEW.id; ELSE cid := NEW.cost_version_id; END IF;
 SELECT c.basis, c.schema_id, c.schema_digest, c.rates_canonical, c.rates_digest, (SELECT count(*) FROM cost_rate_lines l WHERE l.cost_version_id=c.id)
  INTO basis, sid, sdigest, stored, stored_digest, n FROM cost_versions c WHERE c.id=cid;
 IF NOT EXISTS (SELECT 1 FROM model_capability_versions c JOIN model_capability_variants v ON v.profile_version=c.id
  WHERE c.schema_id=sid AND c.schema_digest=sdigest AND v.variant_key='default') THEN
  RAISE EXCEPTION 'supplier quote requires published text schema';
 END IF;
 IF basis='unknown' THEN
  IF n<>0 OR stored IS NOT NULL OR stored_digest IS NOT NULL THEN RAISE EXCEPTION 'unknown supplier quote has rates'; END IF;
  RETURN NULL;
 END IF;
 IF basis NOT IN ('contract','invoice') OR n<>2 THEN RAISE EXCEPTION 'known supplier quote is incomplete'; END IF;
 SELECT l.numerator, l.denominator INTO in_n, in_d FROM cost_rate_lines l WHERE l.cost_version_id=cid AND l.dimension='text_input_token' AND l.unit='token';
 SELECT l.numerator, l.denominator INTO out_n, out_d FROM cost_rate_lines l WHERE l.cost_version_id=cid AND l.dimension='text_output_token' AND l.unit='token';
 IF out_n<>0 AND EXISTS (SELECT 1 FROM meter_schema_dimensions d WHERE d.schema_id=sid AND d.dimension='text_output_token' AND d.maximum_quantity=0) THEN
  RAISE EXCEPTION 'input-only cost requires zero output rate';
 END IF;
 canonical := meter_rate_canonical(sid, 'supplier', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 IF in_n IS NULL OR out_n IS NULL OR stored IS DISTINCT FROM canonical OR stored_digest IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'known supplier quote is incomplete';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_cost_operation_birth(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_cost_operation_birth() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 SELECT * INTO op FROM pricing_operations WHERE operation_id=NEW.pricing_operation_id;
 IF NOT FOUND OR op.created_txid<>txid_current() OR op.kind<>'cost_price.publish' OR op.resource_id<>NEW.id OR op.actor<>NEW.created_by THEN RAISE EXCEPTION 'cost price operation mismatch'; END IF;
 RETURN NEW;
END $$;


--
-- Name: meter_event_attempt_bound(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_event_attempt_bound() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NEW.attempt_id IS NULL THEN
  RAISE EXCEPTION 'observation requires the original attempt';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM upstream_attempts a WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id AND a.request_id=NEW.request_id
 ) THEN
  RAISE EXCEPTION 'observation attempt belongs to another request';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_event_observation_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_event_observation_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE input_status text; output_status text; input_quantity bigint; output_quantity bigint; expected bytea; identity bytea;
BEGIN
 SELECT qi.status, qo.status, qi.quantity, qo.quantity INTO input_status, output_status, input_quantity, output_quantity
  FROM meter_event_quantities qi JOIN meter_event_quantities qo ON qo.event_id=qi.event_id AND qo.dimension='text_output_token'
  WHERE qi.event_id=NEW.id AND qi.dimension='text_input_token';
 expected := meter_observation_canonical(NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.source,NEW.source_event_id,NEW.profile_version,NEW.schema_id,NEW.schema_digest,NEW.plan_digest,NEW.happened_at,NEW.protocol_outcome,NEW.transport_end_reason,NEW.delivery_stage,NEW.content_written,NEW.finish_reason,NULL,input_status,input_quantity,output_status,output_quantity);
 identity := meter_observation_identity_canonical(NEW.tenant_id,NEW.request_id,NEW.attempt_id,NEW.source,NEW.source_event_id,NEW.profile_version,NEW.schema_id,NEW.schema_digest,NEW.plan_digest,NEW.protocol_outcome,NEW.transport_end_reason,NEW.delivery_stage,NEW.content_written,NEW.finish_reason,NULL,input_status,input_quantity,output_status,output_quantity);
 IF NEW.observation_canonical IS DISTINCT FROM expected OR NEW.observation_digest IS DISTINCT FROM meter_observation_digest(NEW.schema_digest,identity) THEN
  RAISE EXCEPTION 'inference observation canonical mismatch';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_event_quantity(text, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_event_quantity(event_id text, dimension text) RETURNS bigint
    LANGUAGE sql STABLE
    
    AS $$
 SELECT quantity FROM meter_event_quantities WHERE event_id=meter_event_quantity.event_id AND dimension=meter_event_quantity.dimension
$$;


--
-- Name: meter_event_rows_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_event_rows_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE n integer; matched integer;
BEGIN
 SELECT count(*), count(*) FILTER (WHERE status=NEW.retail_usage_state AND status=NEW.supplier_usage_state AND unit='token')
  INTO n, matched FROM meter_event_quantities WHERE event_id=NEW.id;
 IF NEW.retail_usage_state IS NULL OR NEW.retail_usage_state IS DISTINCT FROM NEW.supplier_usage_state OR n<>2 OR matched<>2
    OR NOT EXISTS (SELECT 1 FROM meter_event_quantities WHERE event_id=NEW.id AND dimension='text_input_token')
    OR NOT EXISTS (SELECT 1 FROM meter_event_quantities WHERE event_id=NEW.id AND dimension='text_output_token') THEN
  RAISE EXCEPTION 'meter event quantities are incomplete';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_final_digest(bytea, text, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_final_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.final.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;


--
-- Name: meter_finalization_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_finalization_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE e metering_events%ROWTYPE; in_q bigint; out_q bigint; retail_state text; supplier_state text;
 retail_canonical bytea; retail_digest bytea; supplier_canonical bytea; supplier_digest bytea;
BEGIN
 SELECT * INTO e FROM metering_events WHERE id=NEW.event_id;
 IF NOT FOUND OR e.tenant_id IS DISTINCT FROM NEW.tenant_id OR e.request_id IS DISTINCT FROM NEW.request_id
    OR e.attempt_id IS DISTINCT FROM NEW.attempt_id THEN
  RAISE EXCEPTION 'finalization does not match event';
 END IF;
 SELECT quantity INTO in_q FROM meter_event_quantities WHERE event_id=e.id AND dimension='text_input_token';
 SELECT quantity INTO out_q FROM meter_event_quantities WHERE event_id=e.id AND dimension='text_output_token';
 retail_state := e.retail_usage_state;
 supplier_state := e.supplier_usage_state;
 IF e.source='approved_reconciliation' AND supplier_state IS DISTINCT FROM 'conflicting' THEN
  supplier_state := 'missing';
 END IF;
 IF retail_state='confirmed_final' THEN
  retail_canonical := meter_side_quantity_canonical(e.schema_id, 'retail', in_q, out_q);
  retail_digest := meter_final_digest(e.schema_digest, 'retail', retail_canonical);
 END IF;
 IF supplier_state='confirmed_final' THEN
  supplier_canonical := meter_side_quantity_canonical(e.schema_id, 'supplier', in_q, out_q);
  supplier_digest := meter_final_digest(e.schema_digest, 'supplier', supplier_canonical);
 END IF;
 IF NEW.retail_usage_state IS DISTINCT FROM retail_state OR NEW.supplier_usage_state IS DISTINCT FROM supplier_state
    OR NEW.retail_quantities_canonical IS DISTINCT FROM retail_canonical OR NEW.retail_quantities_digest IS DISTINCT FROM retail_digest
    OR NEW.supplier_quantities_canonical IS DISTINCT FROM supplier_canonical OR NEW.supplier_quantities_digest IS DISTINCT FROM supplier_digest THEN
  RAISE EXCEPTION 'finalization does not match event';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_finish_probe(text, text, text, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_finish_probe(probe_id text, protocol_status text, usage_status text, input_tokens bigint, output_tokens bigint) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE mapped text; sid text; sdigest bytea; canonical bytea; digest bytea;
BEGIN
 mapped := meter_usage_state(usage_status);
 IF mapped IS NULL OR protocol_status NOT IN ('sending','completed','failed','unknown') THEN RAISE EXCEPTION 'invalid probe result'; END IF;
 SELECT c.schema_id, c.schema_digest INTO sid, sdigest
  FROM platform_probe_attempts p JOIN models m ON m.id=p.model_id JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id
  WHERE p.id=meter_finish_probe.probe_id;
 IF sid IS NULL THEN RAISE EXCEPTION 'probe model schema missing'; END IF;
 IF mapped='confirmed_final' THEN
  IF input_tokens < 0 OR output_tokens < 0 THEN RAISE EXCEPTION 'invalid probe quantities'; END IF;
  canonical := meter_quantity_canonical(sid, input_tokens, output_tokens);
  digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.quantities.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 ELSE
  canonical := NULL; digest := NULL;
 END IF;
 UPDATE platform_probe_attempts SET status=protocol_status, usage_state=mapped, schema_id=sid, schema_digest=sdigest, variant_key='default', quantities_canonical=canonical, quantities_digest=digest, completed_at=clock_timestamp()
  WHERE id=meter_finish_probe.probe_id;
END $$;


--
-- Name: meter_frames(bytea[]); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_frames(parts bytea[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE part bytea; out bytea := ''::bytea;
BEGIN
 FOREACH part IN ARRAY parts LOOP
  out := out || int4send(octet_length(part)) || part;
 END LOOP;
 RETURN out;
END $$;


--
-- Name: meter_gcd(bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_gcd(a bigint, b bigint) RETURNS bigint
    LANGUAGE sql IMMUTABLE
    
    AS $$
 WITH RECURSIVE g(x,y) AS (
  SELECT abs(a), abs(b)
  UNION ALL SELECT y, x % y FROM g WHERE y <> 0
 ) SELECT x FROM g WHERE y = 0
$$;


--
-- Name: meter_group_price_audit(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_group_price_audit(oid text) RETURNS void
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE op pricing_operations%ROWTYPE; n bigint; want_action text;
BEGIN
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=oid AND kind IN ('group_price.publish','default_price.publish','cost_price.publish');
 want_action:=CASE WHEN op.kind='cost_price.publish' THEN 'pricing.cost.publish' ELSE 'pricing.price.publish' END;
 SELECT count(*) INTO n FROM audit_events a WHERE a.sanitized_diff->>'operation_id'=oid;
 IF n<>1 OR NOT EXISTS (SELECT 1 FROM audit_events a WHERE a.sanitized_diff->>'operation_id'=oid
  AND op.created_txid=txid_current()
  AND a.actor=op.actor AND a.tenant_scope IS NULL AND a.action=want_action AND a.target=op.target
  AND a.sanitized_diff=jsonb_build_object('operation_id',oid,'target',op.target,'after',op.result_snapshot)) THEN RAISE EXCEPTION 'group price audit mismatch'; END IF;
END $$;


--
-- Name: meter_group_price_audit_birth(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_group_price_audit_birth() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 -- Only pricing audits are inspected: roles such as the payment worker write
 -- ordinary audits but deliberately cannot read pricing_operations.
 IF NEW.action IN ('pricing.price.publish','pricing.cost.publish') THEN
  SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.sanitized_diff->>'operation_id' AND kind IN ('group_price.publish','default_price.publish','cost_price.publish');
  IF op.created_txid<>txid_current() OR NEW.action<>(CASE WHEN op.kind='cost_price.publish' THEN 'pricing.cost.publish' ELSE 'pricing.price.publish' END) THEN RAISE EXCEPTION 'publish audit mismatch'; END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: meter_group_price_audit_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_group_price_audit_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NEW.action IN ('pricing.price.publish','pricing.cost.publish') THEN
  PERFORM meter_group_price_audit(NEW.sanitized_diff->>'operation_id');
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_group_price_complete(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_group_price_complete(pid text) RETURNS void
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE p price_versions%ROWTYPE; qi bigint; qo bigint; n1 bigint; d1 bigint; n2 bigint; d2 bigint; amount numeric;
BEGIN
 SELECT * INTO STRICT p FROM price_versions WHERE id=pid;
 IF p.rates_canonical IS DISTINCT FROM meter_text_rates_canonical(convert_from(p.rates_canonical,'UTF8')::jsonb,'retail') THEN RAISE EXCEPTION 'group price rates mismatch'; END IF;
 SELECT maximum_quantity INTO STRICT qi FROM meter_schema_dimensions WHERE schema_id=p.schema_id AND dimension='text_input_token' AND unit='token' AND retail;
 SELECT maximum_quantity INTO STRICT qo FROM meter_schema_dimensions WHERE schema_id=p.schema_id AND dimension='text_output_token' AND unit='token' AND retail;
 SELECT numerator,denominator INTO STRICT n1,d1 FROM price_rate_lines WHERE price_version_id=pid AND dimension='text_input_token';
 SELECT numerator,denominator INTO STRICT n2,d2 FROM price_rate_lines WHERE price_version_id=pid AND dimension='text_output_token';
 IF qo=0 AND n2<>0 THEN RAISE EXCEPTION 'input-only group price requires zero output'; END IF;
 amount:=meter_token_amount(qi,qo,n1,d1,n2,d2);
 IF amount IS NULL OR amount>9223372036854775807 THEN RAISE EXCEPTION 'group price maximum amount overflow'; END IF;
END $$;


--
-- Name: meter_insert_attempt_snapshot(text, text, text, text, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_insert_attempt_snapshot(tenant_id text, attempt_id text, request_id text, cost_version_id text, basis text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE sid text; sdigest bytea; model text; profile text; inb bigint; outb bigint; request_plan_digest bytea; parent_plan_digest bytea; stored text; rates bytea; digest bytea; status text; reason text; hmac text; streaming boolean; attempt_plan bytea; attempt_digest bytea; supplier_quote bytea; supplier_quote_digest bytea; retail_bounds bytea; supplier_bounds bytea; retail_bounds_digest bytea; supplier_bounds_digest bytea; supplier_meter_quote_digest bytea;
BEGIN
 SELECT s.schema_id, s.schema_digest, s.model_id, s.profile_version, s.input_bound, s.output_bound, s.plan_digest
  INTO sid, sdigest, model, profile, inb, outb, request_plan_digest
  FROM request_meter_snapshots s
  WHERE s.tenant_id=meter_insert_attempt_snapshot.tenant_id AND s.request_id=meter_insert_attempt_snapshot.request_id;
 IF sid IS NULL THEN RAISE EXCEPTION 'request meter snapshot missing'; END IF;
 IF meter_insert_attempt_snapshot.cost_version_id IS NULL THEN
  IF basis IS DISTINCT FROM 'unknown' THEN RAISE EXCEPTION 'missing supplier quote is unknown'; END IF;
  status := 'unknown'; reason := 'version_missing';
 ELSE
  SELECT c.basis, c.rates_canonical, c.rates_digest INTO stored, rates, digest FROM cost_versions c WHERE c.id=meter_insert_attempt_snapshot.cost_version_id;
  IF stored IS NULL THEN RAISE EXCEPTION 'supplier quote version missing'; END IF;
  IF stored='unknown' THEN
   status := 'unknown'; reason := 'basis_unknown'; rates := NULL; digest := NULL; basis := 'unknown';
  ELSE
   status := 'known'; reason := NULL; basis := stored;
  END IF;
 END IF;
 SELECT r.request_hmac, r.streaming INTO hmac, streaming FROM request_records r WHERE r.tenant_id=meter_insert_attempt_snapshot.tenant_id AND r.id=meter_insert_attempt_snapshot.request_id;
 IF meter_plan_ready(profile, hmac) THEN
  attempt_plan := meter_attempt_plan_canonical(profile, model, sid, sdigest, outb, streaming, hmac);
  IF attempt_plan IS NOT NULL THEN
   attempt_digest := meter_plan_digest(sdigest, attempt_plan);
   parent_plan_digest := request_plan_digest;
  END IF;
  supplier_quote := meter_supplier_quote_canonical(sid, sdigest, attempt_digest, status, reason, meter_insert_attempt_snapshot.cost_version_id, rates, inb, outb);
  supplier_quote_digest := meter_quote_digest(sdigest, supplier_quote);
 END IF;
 IF attempt_digest IS NOT NULL THEN
  retail_bounds := meter_bounds_canonical(sid,'retail',inb,outb);
  supplier_bounds := meter_bounds_canonical(sid,'supplier',inb,outb);
  retail_bounds_digest := meter_bounds_digest(sdigest,'retail',retail_bounds);
  supplier_bounds_digest := meter_bounds_digest(sdigest,'supplier',supplier_bounds);
  IF rates IS NOT NULL THEN
   supplier_meter_quote_digest := meter_pure_quote_digest(sdigest,'supplier',supplier_bounds,rates);
  END IF;
 ELSE
  rates := NULL;
  digest := NULL;
 END IF;
 INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,parent_plan_digest,supplier_quote_canonical,supplier_quote_digest)
  VALUES(meter_insert_attempt_snapshot.tenant_id, meter_insert_attempt_snapshot.attempt_id, meter_insert_attempt_snapshot.request_id, model, profile, sid, sdigest, 'default', meter_insert_attempt_snapshot.cost_version_id, status, reason, basis, rates, digest, retail_bounds, retail_bounds_digest, supplier_bounds, supplier_bounds_digest, supplier_meter_quote_digest, inb, outb, attempt_plan, attempt_digest, parent_plan_digest, supplier_quote, supplier_quote_digest);
 INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
  SELECT meter_insert_attempt_snapshot.tenant_id, meter_insert_attempt_snapshot.attempt_id, side, dimension, 'token', CASE dimension WHEN 'text_input_token' THEN inb ELSE outb END
  FROM (VALUES ('retail'),('supplier')) AS s(side)
  CROSS JOIN (VALUES ('text_input_token'),('text_output_token')) AS d(dimension);
END $$;


--
-- Name: meter_insert_text_model(text, text, jsonb, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_insert_text_model(model_id text, public_name text, profile jsonb, profile_version text) RETURNS void
    LANGUAGE plpgsql
    
    AS $_$
DECLARE protocol text; input_max bigint; output_max bigint; sid text; digest bytea; canonical bytea; contract bytea;
BEGIN
 protocol := coalesce(profile->>'protocol','openai_chat');
 profile := jsonb_set(profile, '{protocol}', to_jsonb(protocol), true);
 input_max := (profile->>'input_token_upper_bound')::bigint;
 output_max := (profile->>'output_limit')::bigint;
 IF profile->>'version' IS DISTINCT FROM profile_version OR profile->>'model' IS DISTINCT FROM public_name OR input_max IS NULL OR output_max IS NULL THEN
  RAISE EXCEPTION 'text model profile identity mismatch';
 END IF;
 IF profile_version !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR profile_version = '00000000-0000-0000-0000-000000000000' THEN
  RAISE EXCEPTION 'profile version must be a canonical uuid';
 END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') AND output_max <> 0 THEN
  RAISE EXCEPTION 'input-only model requires a zero output bound';
 END IF;
 sid := meter_text_schema(input_max, output_max);
 SELECT schema_digest INTO digest FROM meter_schemas WHERE id = sid;
 contract := sha256(convert_to(profile::text,'UTF8'));
 canonical := convert_to(profile::text,'UTF8');
 INSERT INTO models(id,public_name,profile_version) VALUES(model_id, public_name, profile_version);
 INSERT INTO model_capability_versions(id,model_id,profile,schema_id,schema_digest,protocol,execution_kind,contract_digest,profile_digest)
  VALUES(profile_version, model_id, profile, sid, digest, protocol, 'synchronous', contract, sha256(canonical));
 INSERT INTO model_capability_variants(profile_version,variant_key,parameters) VALUES(profile_version,'default','{}'::jsonb);
 INSERT INTO model_capability_bound_lines(profile_version,variant_key,side,dimension,unit,quantity) VALUES
  (profile_version,'default','retail','text_input_token','token',input_max),
  (profile_version,'default','retail','text_output_token','token',output_max),
  (profile_version,'default','supplier','text_input_token','token',input_max),
  (profile_version,'default','supplier','text_output_token','token',output_max);
END $_$;


--
-- Name: meter_insert_token_cap(text, text, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_insert_token_cap(binding_id text, dimension text, per_million bigint) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE n bigint; d bigint;
BEGIN
 IF dimension NOT IN ('text_input_token','text_output_token') THEN RAISE EXCEPTION 'unsupported cap dimension'; END IF;
 SELECT r.numerator, r.denominator INTO n, d FROM meter_reduce_million(per_million) r;
 INSERT INTO binding_cost_rate_limits(binding_id,dimension,unit,numerator,denominator) VALUES(binding_id, dimension, 'token', n, d);
END $$;


--
-- Name: meter_insert_token_cost(text, text, text, text, bigint, bigint, text, timestamp with time zone, text, text, timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_insert_token_cost(cost_id text, channel_id text, upstream_model text, schema_id text, input_per_million bigint, output_per_million bigint, basis text, effective_at timestamp with time zone, created_by text, pricing_operation_id text, created_at timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE sid text; sdigest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea; cid text;
BEGIN
 IF basis NOT IN ('contract','invoice','unknown') THEN RAISE EXCEPTION 'invalid cost basis'; END IF;
 SELECT s.id, s.schema_digest INTO sid, sdigest FROM meter_schemas s
  WHERE s.id=meter_insert_token_cost.schema_id AND EXISTS (
   SELECT 1 FROM model_capability_versions c JOIN model_capability_variants v ON v.profile_version=c.id
   WHERE c.schema_id=s.id AND c.schema_digest=s.schema_digest AND v.variant_key='default');
 IF sid IS NULL THEN RAISE EXCEPTION 'published text schema is required before a supplier quote'; END IF;
 cid := coalesce(cost_id, gen_random_uuid()::text);
 IF basis = 'unknown' THEN
  IF input_per_million IS NOT NULL OR output_per_million IS NOT NULL THEN RAISE EXCEPTION 'unknown cost has no rates'; END IF;
  INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,created_by,pricing_operation_id,created_at)
   VALUES(cid, channel_id, upstream_model, basis, coalesce(effective_at, clock_timestamp()), sid, sdigest, 'default', 'CNY', created_by, pricing_operation_id, coalesce(created_at,clock_timestamp()));
  RETURN;
 END IF;
 IF input_per_million IS NULL OR output_per_million IS NULL THEN RAISE EXCEPTION 'known cost requires both rates'; END IF;
 SELECT r.numerator, r.denominator INTO in_n, in_d FROM meter_reduce_million(input_per_million) r;
 SELECT r.numerator, r.denominator INTO out_n, out_d FROM meter_reduce_million(output_per_million) r;
 canonical := meter_rate_canonical(sid, 'supplier', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('supplier','UTF8'), canonical]));
 INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,rates_canonical,rates_digest,created_by,pricing_operation_id,created_at)
  VALUES(cid, channel_id, upstream_model, basis, coalesce(effective_at, clock_timestamp()), sid, sdigest, 'default', 'CNY', canonical, digest, created_by, pricing_operation_id, coalesce(created_at,clock_timestamp()));
 INSERT INTO cost_rate_lines(cost_version_id,dimension,unit,numerator,denominator) VALUES
  (cid,'text_input_token','token',in_n,in_d),
  (cid,'text_output_token','token',out_n,out_d);
END $$;


--
-- Name: meter_insert_token_price(text, text, bigint, bigint, timestamp with time zone, text, text, text, timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_insert_token_price(price_id text, model_id text, input_per_million bigint, output_per_million bigint, effective_at timestamp with time zone, created_by text, price_group_id text, pricing_operation_id text, created_at timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE sid text; sdigest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea; protocol text; cid text;
BEGIN
 SELECT c.schema_id, c.schema_digest, c.protocol INTO sid, sdigest, protocol
  FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=meter_insert_token_price.model_id;
 IF sid IS NULL THEN RAISE EXCEPTION 'model has no text schema'; END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') AND output_per_million <> 0 THEN
  RAISE EXCEPTION 'input-only price requires zero output rate';
 END IF;
 SELECT r.numerator, r.denominator INTO in_n, in_d FROM meter_reduce_million(input_per_million) r;
 SELECT r.numerator, r.denominator INTO out_n, out_d FROM meter_reduce_million(output_per_million) r;
 canonical := meter_rate_canonical(sid, 'retail', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), sdigest, convert_to('retail','UTF8'), canonical]));
 cid := coalesce(price_id, gen_random_uuid()::text);
 INSERT INTO price_versions(id,model_id,currency,effective_at,created_by,created_at,price_group_id,pricing_operation_id,schema_id,schema_digest,variant_key,rates_canonical,rates_digest)
  VALUES(cid, model_id, 'CNY', coalesce(effective_at, clock_timestamp()), created_by, coalesce(created_at, clock_timestamp()), price_group_id, pricing_operation_id, sid, sdigest, 'default', canonical, digest);
 INSERT INTO price_rate_lines(price_version_id,dimension,unit,numerator,denominator) VALUES
  (cid,'text_input_token','token',in_n,in_d),
  (cid,'text_output_token','token',out_n,out_d);
END $$;


--
-- Name: meter_model_profile(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_model_profile(model_id text) RETURNS jsonb
    LANGUAGE sql STABLE
    
    AS $$
 SELECT c.profile FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=meter_model_profile.model_id
$$;


--
-- Name: meter_model_route_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_model_route_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM models m WHERE m.id=NEW.id AND m.active_revision<>0 AND NOT EXISTS (
  SELECT 1 FROM route_policy_versions p WHERE p.model_id=m.id AND p.revision=m.active_revision AND p.profile_version=m.profile_version)) THEN
  RAISE EXCEPTION 'active route does not match model profile';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_observation_canonical(text, text, text, text, text, text, text, bytea, bytea, timestamp with time zone, text, text, text, boolean, text, text, text, bigint, text, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_observation_canonical(tenant_id text, request_id text, attempt_id text, source text, source_event_id text, profile_version text, schema_id text, schema_digest bytea, plan_digest bytea, observed_at timestamp with time zone, protocol_outcome text, transport_end_reason text, delivery_stage text, content_written boolean, finish_reason text, unexpected_charge_reason text, input_status text, input_quantity bigint, output_status text, output_quantity bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT convert_to(replace(convert_from(meter_observation_identity_canonical(tenant_id,request_id,attempt_id,source,source_event_id,profile_version,schema_id,schema_digest,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,unexpected_charge_reason,input_status,input_quantity,output_status,output_quantity),'UTF8'),
  '"observed_at":null', '"observed_at":' || to_json(to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))::text), 'UTF8')
$$;


--
-- Name: meter_observation_digest(bytea, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_observation_digest(schema_digest bytea, identity_canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.observation.v1','UTF8'), schema_digest, identity_canonical]))
$$;


--
-- Name: meter_observation_identity_canonical(text, text, text, text, text, text, text, bytea, bytea, text, text, text, boolean, text, text, text, bigint, text, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_observation_identity_canonical(tenant_id text, request_id text, attempt_id text, source text, source_event_id text, profile_version text, schema_id text, schema_digest bytea, plan_digest bytea, protocol_outcome text, transport_end_reason text, delivery_stage text, content_written boolean, finish_reason text, unexpected_charge_reason text, input_status text, input_quantity bigint, output_status text, output_quantity bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT convert_to(format(
  '{"version":"1","tenant_id":%s,"request_id":%s,"attempt_id":%s,"source":%s,"source_event_id":%s,"profile_version":%s,"schema_id":%s,"schema_digest":%s,"variant_key":"default","plan_digest":%s,"provider_reference":null,"observed_at":null,"protocol_outcome":%s,"transport_end_reason":%s,"delivery_stage":%s,"content_written":%s,"finish_reason":%s,"quantities":[{"dimension":"text_input_token","unit":"token","status":%s,"quantity":%s},{"dimension":"text_output_token","unit":"token","status":%s,"quantity":%s}],"unexpected_charge_reason":%s}',
  to_json(tenant_id)::text, to_json(request_id)::text, to_json(attempt_id)::text, to_json(source)::text, to_json(source_event_id)::text,
  to_json(profile_version)::text, to_json(schema_id)::text, to_json(encode(schema_digest,'hex'))::text, to_json(encode(plan_digest,'hex'))::text,
  to_json(protocol_outcome)::text, to_json(transport_end_reason)::text, to_json(delivery_stage)::text, content_written,
  to_json(finish_reason)::text, to_json(input_status)::text, meter_observation_quantity(input_status,input_quantity),
  to_json(output_status)::text, meter_observation_quantity(output_status,output_quantity), coalesce(to_json(unexpected_charge_reason)::text,'null')), 'UTF8')
$$;


--
-- Name: meter_observation_quantity(text, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_observation_quantity(status text, quantity bigint) RETURNS text
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT CASE WHEN status IN ('confirmed_final','provisional') THEN to_json(quantity::text)::text ELSE 'null' END
$$;


--
-- Name: meter_plan_digest(bytea, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_plan_digest(schema_digest bytea, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.plan.v1','UTF8'), schema_digest, canonical]))
$$;


--
-- Name: meter_plan_ready(text, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_plan_ready(profile_version text, request_hmac text) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $_$
 SELECT profile_version ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND profile_version <> '00000000-0000-0000-0000-000000000000'
  AND request_hmac ~ '^[0-9a-f]{64}$'
$_$;


--
-- Name: meter_price_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_price_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE pid text; canonical bytea; digest bytea; schema_digest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; sid text;
BEGIN
 IF TG_TABLE_NAME = 'price_versions' THEN pid := NEW.id; ELSE pid := NEW.price_version_id; END IF;
 SELECT l.numerator, l.denominator INTO in_n, in_d FROM price_rate_lines l WHERE l.price_version_id=pid AND l.dimension='text_input_token' AND l.unit='token';
 SELECT l.numerator, l.denominator INTO out_n, out_d FROM price_rate_lines l WHERE l.price_version_id=pid AND l.dimension='text_output_token' AND l.unit='token';
 IF in_n IS NULL OR out_n IS NULL OR (SELECT count(*) FROM price_rate_lines WHERE price_version_id=pid) <> 2 THEN
  RAISE EXCEPTION 'retail price requires both token rates';
 END IF;
 IF meter_gcd(in_n, in_d) <> 1 OR meter_gcd(out_n, out_d) <> 1 THEN RAISE EXCEPTION 'retail rate is not reduced'; END IF;
 SELECT p.schema_id, s.schema_digest, p.rates_canonical, p.rates_digest INTO sid, schema_digest, canonical, digest
  FROM price_versions p JOIN meter_schemas s ON s.id=p.schema_id WHERE p.id=pid;
 canonical := meter_rate_canonical(sid, 'retail', in_n, in_d, out_n, out_d);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), schema_digest, convert_to('retail','UTF8'), canonical]));
 IF (SELECT rates_canonical FROM price_versions WHERE id=pid) IS DISTINCT FROM canonical
    OR (SELECT rates_digest FROM price_versions WHERE id=pid) IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'retail rate canonical mismatch';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_procurement_attempt_closed(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_procurement_attempt_closed() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE event_held numeric; event_spent numeric; proposal_amount bigint; proposal_basis text; proposal_op text; proposal_evidence text; stored_amount numeric; stored_status text;
BEGIN
 SELECT coalesce(sum(held_delta),0), coalesce(sum(spent_delta),0) INTO event_held, event_spent
  FROM procurement_budget_events WHERE attempt_id=NEW.attempt_id;
 IF event_held IS DISTINCT FROM NEW.held_amount OR event_spent IS DISTINCT FROM NEW.spent_amount THEN
  RAISE EXCEPTION 'procurement reservation does not match its budget events';
 END IF;
 SELECT p.amount, p.basis, p.approval_operation_id, p.evidence_ref
  INTO proposal_amount, proposal_basis, proposal_op, proposal_evidence
  FROM procurement_cost_proposals p
  WHERE p.attempt_id=NEW.attempt_id AND p.state='approved'
  ORDER BY p.approved_at DESC, p.id DESC
  LIMIT 1;
 IF NOT FOUND THEN
  IF NEW.state='settled' THEN
   SELECT a.cost_amount, a.cost_status INTO stored_amount, stored_status FROM upstream_attempts a WHERE a.id=NEW.attempt_id;
   IF stored_amount IS DISTINCT FROM NEW.spent_amount OR stored_status NOT IN ('contract','invoice') THEN
    RAISE EXCEPTION 'procurement reservation does not match its budget events';
   END IF;
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.spent_amount IS DISTINCT FROM proposal_amount::numeric THEN
  RAISE EXCEPTION 'procurement reservation does not match its budget events';
 END IF;
 IF NEW.state IN ('settled','disputed') AND (
  NOT EXISTS (
   SELECT 1 FROM provider_cost_corrections c
   WHERE c.attempt_id=NEW.attempt_id AND c.amount=proposal_amount AND c.basis=proposal_basis AND c.evidence_ref=proposal_evidence
     AND c.operation_id='procurement:'||proposal_op
  ) OR NOT EXISTS (
   SELECT 1 FROM procurement_budget_events e
   WHERE e.attempt_id=NEW.attempt_id AND e.event_key='correction:'||proposal_op AND e.type='cost_corrected' AND e.evidence_ref=proposal_evidence
  )
 ) THEN
  RAISE EXCEPTION 'procurement reservation does not match its budget events';
 END IF;
 IF NEW.state='settled' THEN
  SELECT a.cost_amount, a.cost_status INTO stored_amount, stored_status FROM upstream_attempts a WHERE a.id=NEW.attempt_id;
  IF stored_amount IS DISTINCT FROM proposal_amount::numeric OR stored_status IS DISTINCT FROM proposal_basis THEN
   RAISE EXCEPTION 'procurement reservation does not match its budget events';
  END IF;
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_procurement_bound_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_procurement_bound_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE expected numeric;
BEGIN
 SELECT meter_token_amount(qi.quantity,qo.quantity,ri.numerator,ri.denominator,ro.numerator,ro.denominator)
 INTO expected
 FROM upstream_attempts a
 JOIN attempt_meter_snapshots s ON s.tenant_id=a.tenant_id AND s.request_id=a.request_id AND s.attempt_id=a.id
 JOIN cost_versions c ON c.id=s.cost_version_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
  AND c.schema_id=s.schema_id AND c.schema_digest=s.schema_digest AND c.variant_key=s.variant_key AND c.basis=s.cost_basis
 JOIN attempt_meter_bound_lines qi ON qi.tenant_id=s.tenant_id AND qi.attempt_id=s.attempt_id AND qi.side='supplier' AND qi.dimension='text_input_token' AND qi.unit='token'
 JOIN attempt_meter_bound_lines qo ON qo.tenant_id=s.tenant_id AND qo.attempt_id=s.attempt_id AND qo.side='supplier' AND qo.dimension='text_output_token' AND qo.unit='token'
 JOIN cost_rate_lines ri ON ri.cost_version_id=c.id AND ri.dimension='text_input_token' AND ri.unit='token'
 JOIN cost_rate_lines ro ON ro.cost_version_id=c.id AND ro.dimension='text_output_token' AND ro.unit='token'
 WHERE a.id=NEW.attempt_id AND s.quote_status='known' AND c.basis IN ('contract','invoice')
  AND (SELECT count(*) FROM attempt_meter_bound_lines b WHERE b.attempt_id=s.attempt_id AND b.side='supplier')=2
  AND (SELECT count(*) FROM cost_rate_lines l WHERE l.cost_version_id=c.id)=2;
 IF expected IS NULL OR expected<0 OR expected>9223372036854775807 OR NEW.upper_amount IS DISTINCT FROM expected THEN
  RAISE EXCEPTION 'procurement upper amount does not match original supplier vector';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_procurement_proposal_current(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_procurement_proposal_current() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE evidence bigint; rev bigint;
BEGIN
 IF NEW.state<>'approved' THEN RETURN NULL; END IF;
 SELECT count(*) INTO evidence FROM procurement_evidence_identities WHERE attempt_id=NEW.attempt_id;
 IF evidence IS DISTINCT FROM NEW.expected_evidence_count THEN
  RAISE EXCEPTION 'procurement proposal evidence is stale';
 END IF;
 SELECT revision INTO rev FROM procurement_reservations WHERE attempt_id=NEW.attempt_id;
 IF rev IS DISTINCT FROM NEW.expected_reservation_revision AND rev IS DISTINCT FROM NEW.expected_reservation_revision+1 THEN
  RAISE EXCEPTION 'procurement proposal revision is stale';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_profile_canonical(text, text, text, text, bytea, bytea, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_profile_canonical(profile_version text, model text, protocol text, schema_id text, schema_digest bytea, contract bytea, input_max bigint, output_max bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT convert_to(format('{"version":"1","profile_version":"%s","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kinds":%s,"schema_id":"%s","schema_digest":"%s","variant_key":"default","contract_digest":"%s","input_token_upper_bound":"%s","output_limit":"%s"}',
  profile_version, model, protocol,
  CASE WHEN protocol IN ('openai_embeddings','jina_rerank') THEN '["json"]' ELSE '["json","events"]' END,
  schema_id, encode(schema_digest,'hex'), encode(contract,'hex'), input_max, output_max), 'UTF8')
$$;


--
-- Name: meter_profile_digest(bytea, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_profile_digest(schema_digest bytea, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.profile.v1','UTF8'), schema_digest, canonical]))
$$;


--
-- Name: meter_pure_quote_digest(bytea, text, bytea, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_pure_quote_digest(schema_digest bytea, side text, bounds_canonical bytea, rates_canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.quote.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), bounds_canonical, rates_canonical]))
$$;


--
-- Name: meter_quantities_digest(bytea, text, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_quantities_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.quantities.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;


--
-- Name: meter_quantity_canonical(text, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_quantity_canonical(schema_id text, input_tokens bigint, output_tokens bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT meter_side_quantity_canonical(schema_id, 'supplier', input_tokens, output_tokens)
$$;


--
-- Name: meter_quote_digest(bytea, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_quote_digest(schema_digest bytea, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.inference.quote.v1','UTF8'), schema_digest, canonical]))
$$;


--
-- Name: meter_rate_canonical(text, text, bigint, bigint, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_rate_canonical(schema_id text, side text, input_num bigint, input_den bigint, output_num bigint, output_den bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT convert_to(format('{"schema_id":"%s","version":"1","side":"%s","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"%s","denominator":"%s"},{"dimension":"text_output_token","unit":"token","numerator":"%s","denominator":"%s"}]}', schema_id, side, input_num, input_den, output_num, output_den), 'UTF8')
$$;


--
-- Name: meter_rates_digest(bytea, text, bytea); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_rates_digest(schema_digest bytea, side text, canonical bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'), schema_digest, convert_to(side,'UTF8'), canonical]))
$$;


--
-- Name: meter_reconciliation_proposal_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_reconciliation_proposal_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE sid text; sd bytea; canonical bytea; input_quantity bigint; output_quantity bigint; input_max bigint; output_max bigint; value jsonb;
BEGIN
 IF NEW.state='open' THEN
  IF NEW.proposed_observation_canonical IS NOT NULL OR NEW.proposed_observation_digest IS NOT NULL THEN
   RAISE EXCEPTION 'open reconciliation cannot carry a proposal';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.state='closed' AND NEW.proposed_observation_canonical IS NULL AND NEW.proposed_observation_digest IS NULL THEN
  RETURN NEW;
 END IF;
 IF NEW.state NOT IN ('proposed','approved','closed') THEN
  RAISE EXCEPTION 'reconciliation state is invalid';
 END IF;
 IF NEW.proposed_observation_canonical IS NULL OR NEW.proposed_observation_digest IS NULL THEN
  RAISE EXCEPTION 'reconciliation proposal vector is incomplete';
 END IF;
 SELECT s.schema_id,s.schema_digest INTO sid,sd FROM request_meter_snapshots s
  WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
 IF sid IS NULL OR octet_length(sd)<>32 THEN RAISE EXCEPTION 'reconciliation request schema is missing'; END IF;
 SELECT max(quantity) FILTER (WHERE dimension='text_input_token'),max(quantity) FILTER (WHERE dimension='text_output_token')
  INTO input_max,output_max FROM request_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id AND side='retail';
 value := convert_from(NEW.proposed_observation_canonical,'UTF8')::jsonb;
 input_quantity := (value->'quantities'->0->>'quantity')::bigint;
 output_quantity := (value->'quantities'->1->>'quantity')::bigint;
 IF input_quantity IS NULL OR output_quantity IS NULL OR input_quantity<0 OR output_quantity<0 OR input_max IS NULL OR output_max IS NULL
    OR input_quantity>input_max OR output_quantity>output_max THEN
  RAISE EXCEPTION 'reconciliation proposal quantity exceeds the request schema';
 END IF;
 canonical := meter_side_quantity_canonical(sid,'retail',input_quantity,output_quantity);
 IF NEW.proposed_observation_canonical IS DISTINCT FROM canonical
    OR NEW.proposed_observation_digest IS DISTINCT FROM meter_quantities_digest(sd,'retail',canonical) THEN
  RAISE EXCEPTION 'reconciliation proposal vector mismatch';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: meter_record_event_quantities(text, text, text, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_record_event_quantities(event_id text, tenant_id text, status text, input_tokens bigint, output_tokens bigint) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE mapped text;
BEGIN
 mapped := meter_usage_state(status);
 IF mapped IS NULL OR input_tokens < 0 OR output_tokens < 0 THEN RAISE EXCEPTION 'invalid text usage'; END IF;
 IF mapped IN ('missing','conflicting') THEN
  input_tokens := NULL; output_tokens := NULL;
 END IF;
 INSERT INTO meter_event_quantities(tenant_id,event_id,dimension,unit,status,quantity) VALUES
  (meter_record_event_quantities.tenant_id, meter_record_event_quantities.event_id, 'text_input_token', 'token', mapped, input_tokens),
  (meter_record_event_quantities.tenant_id, meter_record_event_quantities.event_id, 'text_output_token', 'token', mapped, output_tokens);
END $$;


--
-- Name: meter_reduce_million(bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_reduce_million(rate bigint, OUT numerator bigint, OUT denominator bigint) RETURNS record
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE g bigint;
BEGIN
 IF rate < 0 THEN RAISE EXCEPTION 'negative token rate'; END IF;
 g := meter_gcd(rate, 1000000);
 numerator := rate / g;
 denominator := 1000000 / g;
END $$;


--
-- Name: meter_request_bounds_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_bounds_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE matched bigint; total bigint;
BEGIN
 SELECT count(*) INTO total FROM request_meter_bound_lines WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 SELECT count(*) INTO matched FROM request_meter_bound_lines
  WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id AND unit='token'
    AND ((side IN ('retail','supplier') AND dimension='text_input_token' AND quantity=NEW.input_bound)
      OR (side IN ('retail','supplier') AND dimension='text_output_token' AND quantity=NEW.output_bound));
 IF total IS DISTINCT FROM 4 OR matched IS DISTINCT FROM 4 THEN
  RAISE EXCEPTION 'request bounds are incomplete';
 END IF;
 IF EXISTS (
  SELECT 1 FROM (VALUES ('retail'),('supplier')) AS s(side)
  WHERE (SELECT b.quantity FROM model_capability_bound_lines b WHERE b.profile_version=NEW.profile_version AND b.variant_key=NEW.variant_key AND b.side=s.side AND b.dimension='text_input_token') IS DISTINCT FROM NEW.input_bound
     OR (SELECT b.quantity FROM model_capability_bound_lines b WHERE b.profile_version=NEW.profile_version AND b.variant_key=NEW.variant_key AND b.side=s.side AND b.dimension='text_output_token') < NEW.output_bound
 ) THEN
  RAISE EXCEPTION 'request bounds are incomplete';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_request_finalization_pointer(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_finalization_pointer() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NEW.latest_finalization_id IS NOT DISTINCT FROM OLD.latest_finalization_id THEN RETURN NULL; END IF;
 IF NEW.latest_finalization_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM finalization_records f WHERE f.id=NEW.latest_finalization_id AND f.tenant_id=NEW.tenant_id AND f.request_id=NEW.id
 ) THEN RAISE EXCEPTION 'request finalization does not match request'; END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_request_plan_canonical(text, text, text, bytea, bigint, boolean, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text) RETURNS bytea
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE model text; protocol text; contract bytea; input_max bigint; output_max bigint; profile bytea; result_kind text;
BEGIN
 SELECT c.profile->>'model', c.protocol, c.contract_digest, (c.profile->>'input_token_upper_bound')::bigint, (c.profile->>'output_limit')::bigint
  INTO model, protocol, contract, input_max, output_max
  FROM model_capability_versions c WHERE c.id=profile_version AND c.model_id=meter_request_plan_canonical.model_id AND c.schema_id=meter_request_plan_canonical.schema_id AND c.schema_digest=meter_request_plan_canonical.schema_digest;
 IF model IS NULL OR output_limit < 0 OR output_limit > output_max THEN RETURN NULL; END IF;
 IF protocol IN ('openai_embeddings','jina_rerank') THEN
  IF output_limit <> 0 OR streaming THEN RETURN NULL; END IF;
 ELSIF output_limit = 0 THEN RETURN NULL; END IF;
 profile := meter_profile_canonical(profile_version, model, protocol, schema_id, schema_digest, contract, input_max, output_max);
 result_kind := CASE WHEN streaming THEN 'events' ELSE 'json' END;
 RETURN convert_to(format('{"version":"1","scope":"request","model":"%s","protocol":"%s","execution_kind":"synchronous","result_kind":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","input_hmac":"%s","output_limit":"%s","candidate_profile_digests":["%s"],"selected_profile_digest":null,"parent_plan_digest":null,"retail_bounds":%s,"supplier_bounds":%s}',
  model, protocol, result_kind, schema_id, encode(schema_digest,'hex'), request_hmac, output_limit,
  encode(meter_profile_digest(schema_digest, profile),'hex'),
  convert_from(meter_bounds_canonical(schema_id,'retail',input_max,output_limit),'UTF8'),
  convert_from(meter_bounds_canonical(schema_id,'supplier',input_max,output_limit),'UTF8')), 'UTF8');
END $$;


--
-- Name: meter_request_plan_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_plan_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE hmac text; streaming boolean; canonical bytea; digest bytea;
BEGIN
 SELECT r.request_hmac, r.streaming INTO hmac, streaming FROM request_records r WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id;
 IF NOT meter_plan_ready(NEW.profile_version, hmac) THEN
  IF NEW.plan_canonical IS NOT NULL OR NEW.plan_digest IS NOT NULL THEN RAISE EXCEPTION 'request plan does not match profile'; END IF;
  RETURN NULL;
 END IF;
 canonical := meter_request_plan_canonical(NEW.profile_version, NEW.model_id, NEW.schema_id, NEW.schema_digest, NEW.output_bound, streaming, hmac);
 digest := meter_plan_digest(NEW.schema_digest, canonical);
 IF canonical IS NULL OR NEW.plan_canonical IS DISTINCT FROM canonical OR NEW.plan_digest IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'request plan does not match profile';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_request_price_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_price_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM price_versions p
  WHERE p.id=NEW.price_version_id AND p.model_id=NEW.model_id AND p.schema_id=NEW.schema_id
    AND p.schema_digest=NEW.schema_digest AND p.variant_key=NEW.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM NEW.pricing_group_id
 ) THEN
  RAISE EXCEPTION 'request price does not match model';
 END IF;
 IF EXISTS (SELECT 1 FROM request_records r WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id
  AND NOT EXISTS (SELECT 1 FROM route_policy_versions p WHERE p.model_id=r.model_id AND p.revision=r.route_revision AND p.profile_version=NEW.profile_version)) THEN
  RAISE EXCEPTION 'request route does not match profile';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_request_quote_matches(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_quote_matches() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE canonical bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_quote_canonical IS NOT NULL OR NEW.retail_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'request quote does not match price';
  END IF;
  RETURN NULL;
 END IF;
 canonical := meter_retail_quote_canonical(NEW.schema_id, NEW.schema_digest, NEW.plan_digest, NEW.price_version_id, NEW.input_bound, NEW.output_bound);
 IF canonical IS NULL OR NEW.retail_quote_canonical IS DISTINCT FROM canonical OR NEW.retail_quote_digest IS DISTINCT FROM meter_quote_digest(NEW.schema_digest, canonical) THEN
  RAISE EXCEPTION 'request quote does not match price';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_request_snapshot_present(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_snapshot_present() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM request_meter_snapshots WHERE request_id=NEW.id AND tenant_id=NEW.tenant_id AND model_id=NEW.model_id AND profile_version=NEW.profile_version) THEN
  RAISE EXCEPTION 'request requires one meter snapshot';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_request_vectors(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_vectors() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE rates bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL
   OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL
   OR NEW.retail_rates_canonical IS NOT NULL OR NEW.retail_rates_digest IS NOT NULL OR NEW.retail_meter_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'request meter vectors require plan';
  END IF;
  RETURN NEW;
 END IF;
 SELECT p.rates_canonical INTO rates
  FROM price_versions p WHERE p.id=NEW.price_version_id;
 IF rates IS NULL THEN RAISE EXCEPTION 'request meter vectors price mismatch'; END IF;
 NEW.retail_bounds_canonical := meter_bounds_canonical(NEW.schema_id,'retail',NEW.input_bound,NEW.output_bound);
 NEW.supplier_bounds_canonical := meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 NEW.retail_bounds_digest := meter_bounds_digest(NEW.schema_digest,'retail',NEW.retail_bounds_canonical);
 NEW.supplier_bounds_digest := meter_bounds_digest(NEW.schema_digest,'supplier',NEW.supplier_bounds_canonical);
 NEW.retail_rates_canonical := rates;
 NEW.retail_rates_digest := meter_rates_digest(NEW.schema_digest,'retail',rates);
 NEW.retail_meter_quote_digest := meter_pure_quote_digest(NEW.schema_digest,'retail',NEW.retail_bounds_canonical,rates);
 RETURN NEW;
END $$;


--
-- Name: meter_request_vectors_match(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_request_vectors_match() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE rates bytea; rb bytea; sb bytea;
BEGIN
 IF NEW.plan_digest IS NULL THEN
  IF NEW.retail_bounds_canonical IS NOT NULL OR NEW.retail_bounds_digest IS NOT NULL
   OR NEW.supplier_bounds_canonical IS NOT NULL OR NEW.supplier_bounds_digest IS NOT NULL
   OR NEW.retail_rates_canonical IS NOT NULL OR NEW.retail_rates_digest IS NOT NULL OR NEW.retail_meter_quote_digest IS NOT NULL THEN
   RAISE EXCEPTION 'request meter vectors require plan';
  END IF;
  RETURN NULL;
 END IF;
 SELECT p.rates_canonical INTO rates FROM price_versions p WHERE p.id=NEW.price_version_id;
 rb := meter_bounds_canonical(NEW.schema_id,'retail',NEW.input_bound,NEW.output_bound);
 sb := meter_bounds_canonical(NEW.schema_id,'supplier',NEW.input_bound,NEW.output_bound);
 IF rates IS NULL OR NEW.retail_bounds_canonical IS DISTINCT FROM rb OR NEW.supplier_bounds_canonical IS DISTINCT FROM sb
  OR NEW.retail_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'retail',rb)
  OR NEW.supplier_bounds_digest IS DISTINCT FROM meter_bounds_digest(NEW.schema_digest,'supplier',sb)
  OR NEW.retail_rates_canonical IS DISTINCT FROM rates
  OR NEW.retail_rates_digest IS DISTINCT FROM meter_rates_digest(NEW.schema_digest,'retail',rates)
  OR NEW.retail_meter_quote_digest IS DISTINCT FROM meter_pure_quote_digest(NEW.schema_digest,'retail',rb,rates) THEN
  RAISE EXCEPTION 'request meter vectors mismatch';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_reservation_amount(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_reservation_amount() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE expected numeric; input_qty bigint; output_qty bigint;
 in_n bigint; in_d bigint; out_n bigint; out_d bigint;
BEGIN
 SELECT s.input_bound,s.output_bound,ri.numerator,ri.denominator,ro.numerator,ro.denominator
   INTO input_qty,output_qty,in_n,in_d,out_n,out_d
   FROM request_meter_snapshots s
   JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
    AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
   JOIN price_rate_lines ri ON ri.price_version_id=p.id AND ri.dimension='text_input_token' AND ri.unit='token'
   JOIN price_rate_lines ro ON ro.price_version_id=p.id AND ro.dimension='text_output_token' AND ro.unit='token'
  WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'reservation meter snapshot is missing'; END IF;
 expected := meter_token_amount(input_qty,output_qty,in_n,in_d,out_n,out_d);
 IF expected IS NULL OR expected>9223372036854775807::numeric OR NEW.amount::numeric IS DISTINCT FROM expected THEN
  RAISE EXCEPTION 'reservation amount does not match original retail vector';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_retail_quote_canonical(text, bytea, bytea, text, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_retail_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, price_version_id text, input_bound bigint, output_bound bigint) RETURNS bytea
    LANGUAGE plpgsql STABLE
    
    AS $_$
DECLARE rates bytea;
BEGIN
 IF octet_length(plan_digest)<>32 OR octet_length(schema_digest)<>32
  OR price_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  OR price_version_id='00000000-0000-0000-0000-000000000000' THEN
  RETURN NULL;
 END IF;
 SELECT p.rates_canonical INTO rates FROM price_versions p
  WHERE p.id=price_version_id AND p.schema_id=meter_retail_quote_canonical.schema_id AND p.schema_digest=meter_retail_quote_canonical.schema_digest AND p.variant_key='default';
 IF rates IS NULL THEN RETURN NULL; END IF;
 RETURN convert_to(format('{"version":"1","status":"known","side":"retail","plan_digest":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","version_id":"%s","unknown_reason":null,"bounds":%s,"rates":%s}',
  encode(plan_digest,'hex'), schema_id, encode(schema_digest,'hex'), price_version_id,
  convert_from(meter_bounds_canonical(schema_id,'retail',input_bound,output_bound),'UTF8'), convert_from(rates,'UTF8')), 'UTF8');
END $_$;


--
-- Name: meter_route_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_route_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE rid text; route_model text; route_profile text; binding_count bigint;
BEGIN
 IF TG_TABLE_NAME='route_policy_versions' THEN
  rid := NEW.id;
 ELSIF TG_TABLE_NAME='model_bindings' THEN
  rid := NEW.route_policy_version_id;
 ELSE
  SELECT b.route_policy_version_id INTO rid FROM model_bindings b WHERE b.id=NEW.binding_id;
 END IF;
 SELECT p.model_id,p.profile_version INTO route_model,route_profile
 FROM route_policy_versions p WHERE p.id=rid;
 IF NOT FOUND THEN RAISE EXCEPTION 'route root is missing'; END IF;
 SELECT count(*) INTO binding_count FROM model_bindings b WHERE b.route_policy_version_id=rid;
 IF binding_count=0 THEN RAISE EXCEPTION 'route requires at least one binding'; END IF;
IF EXISTS (
 SELECT 1
 FROM model_bindings b
 WHERE b.route_policy_version_id=rid
   AND (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id)>0
   AND (
    (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id)<>2
    OR (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id AND l.dimension='text_input_token')<>1
    OR (SELECT count(*) FROM binding_cost_rate_limits l WHERE l.binding_id=b.id AND l.dimension='text_output_token')<>1
   )
) THEN
 RAISE EXCEPTION 'route binding cap must contain exactly two text dimensions';
END IF;
 IF EXISTS (
  SELECT 1
  FROM model_bindings b
  LEFT JOIN channel_versions v ON v.id=b.channel_version_id
  WHERE b.route_policy_version_id=rid
    AND (v.id IS NULL OR v.channel_id IS DISTINCT FROM b.channel_id
      OR NOT EXISTS (
       SELECT 1
       FROM channel_version_credentials vc
       JOIN channel_credentials c ON c.id=vc.credential_id AND c.channel_id=vc.channel_id
       WHERE vc.channel_version_id=v.id AND vc.channel_id=b.channel_id AND c.revoked_at IS NULL
      ))
 ) THEN
  RAISE EXCEPTION 'route binding supplier contract is incomplete';
 END IF;
 IF EXISTS (
  SELECT 1
  FROM binding_cost_rate_limits l
  JOIN model_bindings b ON b.id=l.binding_id
  LEFT JOIN meter_schema_dimensions d ON d.schema_id=(SELECT c.schema_id FROM model_capability_versions c WHERE c.id=route_profile)
   AND d.dimension=l.dimension AND d.unit=l.unit AND d.supplier
  WHERE b.route_policy_version_id=rid AND d.dimension IS NULL
 ) THEN
  RAISE EXCEPTION 'route binding cap is outside supplier schema';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_route_profile(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_route_profile() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE profile text;
BEGIN
 SELECT m.profile_version INTO STRICT profile FROM models m WHERE m.id=NEW.model_id FOR SHARE;
 IF NEW.profile_version IS NOT NULL AND NEW.profile_version IS DISTINCT FROM profile THEN
  RAISE EXCEPTION 'route profile does not match model';
 END IF;
 NEW.profile_version := profile;
 NEW.created_xid := pg_current_xact_id();
 NEW.created_at := clock_timestamp();
 RETURN NEW;
END $$;


--
-- Name: meter_schema_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_schema_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE n int; canonical text; digest bytea; stored bytea; stored_digest bytea; sid text;
BEGIN
 IF TG_TABLE_NAME = 'meter_schemas' THEN sid := NEW.id; ELSE sid := NEW.schema_id; END IF;
 SELECT count(*), bool_and(retail), bool_and(supplier) INTO n FROM meter_schema_dimensions WHERE schema_id = sid;
 IF n <> 2 THEN RAISE EXCEPTION 'text schema requires input and output dimensions'; END IF;
 SELECT string_agg(format('{"dimension":"%s","unit":"%s","maximum_quantity":"%s"}', dimension, unit, maximum_quantity), ',' ORDER BY dimension)
  INTO canonical FROM meter_schema_dimensions WHERE schema_id = sid;
 canonical := format('{"schema_id":"%s","version":"1","dimensions":[%s],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}', sid, canonical);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.schema.v1','UTF8'), convert_to(canonical,'UTF8')]));
 SELECT s.canonical, s.schema_digest INTO stored, stored_digest FROM meter_schemas s WHERE s.id = sid;
 IF stored IS DISTINCT FROM convert_to(canonical,'UTF8') OR stored_digest IS DISTINCT FROM digest THEN
  RAISE EXCEPTION 'schema canonical digest mismatch';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_schema_stamp(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_schema_stamp() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 NEW.created_xid := pg_current_xact_id();
 NEW.created_at := clock_timestamp();
 RETURN NEW;
END $$;


--
-- Name: meter_settlement_amount(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_settlement_amount() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE expected numeric; charge numeric; reserved numeric; input_qty bigint; output_qty bigint;
 in_n bigint; in_d bigint; out_n bigint; out_d bigint; state text;
BEGIN
 IF NEW.reason<>'charged_usage' THEN
  IF NEW.status<>'released' OR NEW.charged_amount<>0 OR NEW.raw_retail_amount<>0 OR NEW.waived_retail_amount<>0 THEN
   RAISE EXCEPTION 'released settlement must be zero-valued';
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.status<>'settled' OR NEW.finalization_id IS NULL THEN
  RAISE EXCEPTION 'charged usage settlement is incomplete';
 END IF;
 SELECT f.retail_usage_state,r.amount,qi.quantity,qo.quantity,ri.numerator,ri.denominator,ro.numerator,ro.denominator
   INTO state,reserved,input_qty,output_qty,in_n,in_d,out_n,out_d
   FROM finalization_records f
   JOIN metering_events e ON e.tenant_id=f.tenant_id AND e.id=f.event_id
    AND e.request_id=f.request_id AND e.attempt_id=f.attempt_id
   JOIN request_meter_snapshots s ON s.tenant_id=f.tenant_id AND s.request_id=f.request_id
   JOIN reservations r ON r.tenant_id=s.tenant_id AND r.request_id=s.request_id
   JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
    AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
   JOIN price_rate_lines ri ON ri.price_version_id=p.id AND ri.dimension='text_input_token' AND ri.unit='token'
   JOIN price_rate_lines ro ON ro.price_version_id=p.id AND ro.dimension='text_output_token' AND ro.unit='token'
   LEFT JOIN meter_event_quantities qi ON qi.tenant_id=e.tenant_id AND qi.event_id=e.id AND qi.dimension='text_input_token'
   LEFT JOIN meter_event_quantities qo ON qo.tenant_id=e.tenant_id AND qo.event_id=e.id AND qo.dimension='text_output_token'
  WHERE f.tenant_id=NEW.tenant_id AND f.request_id=NEW.request_id AND f.id=NEW.finalization_id;
 IF NOT FOUND OR state IS DISTINCT FROM 'confirmed_final' OR input_qty IS NULL OR output_qty IS NULL THEN
  RAISE EXCEPTION 'charged usage finalization is not a retail final';
 END IF;
 expected := meter_token_amount(input_qty,output_qty,in_n,in_d,out_n,out_d);
 IF expected IS NULL THEN RAISE EXCEPTION 'charged usage amount cannot be calculated'; END IF;
 charge := LEAST(expected,reserved::numeric);
 IF NEW.raw_retail_amount IS DISTINCT FROM expected
    OR NEW.charged_amount::numeric IS DISTINCT FROM charge
    OR NEW.waived_retail_amount IS DISTINCT FROM expected-charge THEN
  RAISE EXCEPTION 'settlement amount does not match original retail vector';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_settlement_finalization(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_settlement_finalization() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE retail text;
BEGIN
 IF NEW.finalization_id IS NULL THEN
  IF NEW.reason='charged_usage' THEN RAISE EXCEPTION 'charged usage has no finalization'; END IF;
  RETURN NULL;
 END IF;
 SELECT retail_usage_state INTO retail FROM finalization_records WHERE id=NEW.finalization_id AND tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'settlement finalization does not match request'; END IF;
 IF NEW.reason='charged_usage' AND retail IS DISTINCT FROM 'confirmed_final' THEN
  RAISE EXCEPTION 'charged usage is not a confirmed retail final';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: meter_side_quantity_canonical(text, text, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_side_quantity_canonical(schema_id text, side text, input_tokens bigint, output_tokens bigint) RETURNS bytea
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT convert_to(format('{"schema_id":"%s","version":"1","side":"%s","quantities":[{"dimension":"text_input_token","unit":"token","quantity":"%s"},{"dimension":"text_output_token","unit":"token","quantity":"%s"}]}', schema_id, side, input_tokens, output_tokens), 'UTF8')
$$;


--
-- Name: meter_supplier_quote_canonical(text, bytea, bytea, text, text, text, bytea, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_supplier_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, quote_status text, unknown_reason text, cost_version_id text, rates_canonical bytea, input_bound bigint, output_bound bigint) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE
    
    AS $_$
DECLARE version_json text; reason_json text; rates_json text;
BEGIN
 IF octet_length(plan_digest)<>32 OR octet_length(schema_digest)<>32 THEN RETURN NULL; END IF;
 IF quote_status='known' THEN
  IF unknown_reason IS NOT NULL OR rates_canonical IS NULL
   OR cost_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR cost_version_id='00000000-0000-0000-0000-000000000000' THEN
   RETURN NULL;
  END IF;
  version_json := format('"%s"', cost_version_id);
  reason_json := 'null';
  rates_json := convert_from(rates_canonical,'UTF8');
 ELSIF quote_status='unknown' AND unknown_reason='version_missing' AND cost_version_id IS NULL AND rates_canonical IS NULL THEN
  version_json := 'null';
  reason_json := '"version_missing"';
  rates_json := 'null';
 ELSIF quote_status='unknown' AND unknown_reason='basis_unknown' AND rates_canonical IS NULL
  AND cost_version_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND cost_version_id<>'00000000-0000-0000-0000-000000000000' THEN
  version_json := format('"%s"', cost_version_id);
  reason_json := '"basis_unknown"';
  rates_json := 'null';
 ELSE
  RETURN NULL;
 END IF;
 RETURN convert_to(format('{"version":"1","status":"%s","side":"supplier","plan_digest":"%s","schema_id":"%s","schema_digest":"%s","variant_key":"default","version_id":%s,"unknown_reason":%s,"bounds":%s,"rates":%s}',
  quote_status, encode(plan_digest,'hex'), schema_id, encode(schema_digest,'hex'), version_json, reason_json,
  convert_from(meter_bounds_canonical(schema_id,'supplier',input_bound,output_bound),'UTF8'), rates_json), 'UTF8');
END $_$;


--
-- Name: meter_text_rates_canonical(jsonb, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_text_rates_canonical(r jsonb, expected_side text) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE line jsonb; i integer; nums bigint[] := '{}'; dens bigint[] := '{}'; name text;
BEGIN
 IF expected_side IS NULL OR expected_side NOT IN ('retail','supplier') OR jsonb_typeof(r) IS DISTINCT FROM 'object'
  OR NOT pricing_json_uuid(r->'schema_id') OR r->'version' IS DISTINCT FROM '"1"'::jsonb
  OR r->'side' IS DISTINCT FROM to_jsonb(expected_side) OR r->'currency' IS DISTINCT FROM '"CNY"'::jsonb
  OR jsonb_typeof(r->'rates') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid text rates'; END IF;
 IF jsonb_array_length(r->'rates')<>2 OR r IS DISTINCT FROM jsonb_build_object('schema_id',r->'schema_id','version','1','side',expected_side,'currency','CNY','rates',r->'rates') THEN RAISE EXCEPTION 'invalid text rates shape'; END IF;
 FOR i IN 0..1 LOOP
  line := r->'rates'->i;
  name := CASE i WHEN 0 THEN 'text_input_token' ELSE 'text_output_token' END;
  IF jsonb_typeof(line) IS DISTINCT FROM 'object' OR line->'dimension' IS DISTINCT FROM to_jsonb(name)
   OR line->'unit' IS DISTINCT FROM '"token"'::jsonb OR NOT pricing_json_uint(line->'numerator',false)
   OR NOT pricing_json_uint(line->'denominator',true)
   OR line IS DISTINCT FROM jsonb_build_object('dimension',name,'unit','token','numerator',line->'numerator','denominator',line->'denominator') THEN RAISE EXCEPTION 'invalid text rate line'; END IF;
  nums := array_append(nums,(line->>'numerator')::bigint);
  dens := array_append(dens,(line->>'denominator')::bigint);
  IF dens[i+1]>1000000 OR meter_gcd(nums[i+1],dens[i+1])<>1 THEN RAISE EXCEPTION 'text rate not reduced'; END IF;
 END LOOP;
 RETURN meter_rate_canonical(r->>'schema_id',expected_side,nums[1],dens[1],nums[2],dens[2]);
END $$;


--
-- Name: meter_text_schema(bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_text_schema(input_max bigint, output_max bigint) RETURNS text
    LANGUAGE plpgsql
    
    AS $$
DECLARE sid text; canonical text; digest bytea;
BEGIN
 IF input_max <= 0 OR output_max < 0 THEN RAISE EXCEPTION 'invalid text schema bounds'; END IF;
 SELECT s.id INTO sid FROM meter_schemas s
  WHERE EXISTS (SELECT 1 FROM meter_schema_dimensions d WHERE d.schema_id=s.id AND d.dimension='text_input_token' AND d.maximum_quantity=input_max AND d.unit='token' AND d.retail AND d.supplier)
    AND EXISTS (SELECT 1 FROM meter_schema_dimensions d WHERE d.schema_id=s.id AND d.dimension='text_output_token' AND d.maximum_quantity=output_max AND d.unit='token' AND d.retail AND d.supplier);
 IF sid IS NOT NULL THEN RETURN sid; END IF;
 sid := gen_random_uuid()::text;
 canonical := format('{"schema_id":"%s","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"%s"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"%s"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}', sid, input_max, output_max);
 digest := sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.schema.v1','UTF8'), convert_to(canonical,'UTF8')]));
 INSERT INTO meter_schemas(id,name,revision,registry_version,wire_version,canonical,schema_digest)
  VALUES(sid, 'text_' || substr(sid, 1, 8), 1, 1, 1, convert_to(canonical,'UTF8'), digest);
 INSERT INTO meter_schema_dimensions(schema_id,dimension,unit,maximum_quantity,retail,supplier) VALUES
  (sid,'text_input_token','token',input_max,true,true),
  (sid,'text_output_token','token',output_max,true,true);
 RETURN sid;
END $$;


--
-- Name: meter_token_amount(bigint, bigint, bigint, bigint, bigint, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_token_amount(input_qty bigint, output_qty bigint, in_n bigint, in_d bigint, out_n bigint, out_d bigint) RETURNS numeric
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT CASE
  WHEN input_qty IS NULL OR output_qty IS NULL OR in_n IS NULL OR in_d IS NULL OR out_n IS NULL OR out_d IS NULL THEN NULL
  WHEN input_qty < 0 OR output_qty < 0 OR in_n < 0 OR out_n < 0 OR in_d < 1 OR out_d < 1 THEN NULL
  ELSE div(input_qty::numeric * in_n * out_d + output_qty::numeric * out_n * in_d + in_d::numeric * out_d - 1, in_d::numeric * out_d)
 END
$$;


--
-- Name: meter_usage_state(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION meter_usage_state(status text) RETURNS text
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT CASE status WHEN 'estimated' THEN 'provisional' WHEN 'confirmed_final' THEN 'confirmed_final' WHEN 'provisional' THEN 'provisional' WHEN 'missing' THEN 'missing' WHEN 'conflicting' THEN 'conflicting' ELSE NULL END
$$;


--
-- Name: payment_audit_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_audit_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE jid text;
BEGIN
 IF NEW.action='payment.capture.credited' THEN
  SELECT journal_id INTO jid FROM funding_receipts WHERE source_kind='sandbox_capture' AND source_id=NEW.target;
  IF jid IS NULL OR NOT payment_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'payment capture audit missing complete source';END IF;
 ELSIF NEW.action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel') THEN PERFORM payment_check_human(NEW.target);END IF;RETURN NULL;
END $$;


--
-- Name: payment_audit_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_audit_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE worker name;c payment_captures%ROWTYPE;op commerce_commands%ROWTYPE;
BEGIN
 SELECT worker_role INTO worker FROM payment_sandbox_environment WHERE singleton;
 IF NEW.action='payment.capture.credited' THEN
  IF current_user IS DISTINCT FROM worker THEN RAISE EXCEPTION 'payment capture audit requires worker' USING ERRCODE='42501';END IF;
  SELECT * INTO STRICT c FROM payment_captures WHERE id=NEW.target;
  IF (NEW.actor,NEW.tenant_scope,NEW.request_id,NEW.sanitized_diff) IS DISTINCT FROM ('payment-sandbox-worker',c.tenant_id,'',jsonb_build_object('amount',c.wallet_credit_amount::text)) THEN RAISE EXCEPTION 'payment capture audit mismatch';END IF;
 ELSIF current_user=worker THEN RAISE EXCEPTION 'payment worker audit action forbidden' USING ERRCODE='42501';
 ELSIF NEW.action IN ('payment.sku.publish','payment.sku.activate','payment.order.create','payment.order.cancel') THEN
  SELECT * INTO STRICT op FROM commerce_commands WHERE operation_id=NEW.target AND kind=NEW.action;
  IF NEW.actor IS DISTINCT FROM op.actor OR NEW.tenant_scope IS DISTINCT FROM (CASE WHEN op.scope='platform' THEN NULL ELSE substr(op.scope,8) END) OR NEW.sanitized_diff<>'{}'::jsonb THEN RAISE EXCEPTION 'payment human audit mismatch';END IF;
 END IF;
 IF NEW.action LIKE 'payment.%' THEN NEW.created_at:=clock_timestamp();END IF;RETURN NEW;
END $$;


--
-- Name: payment_backoff(integer); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_backoff(n integer) RETURNS interval
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT make_interval(secs=>CASE WHEN n<=1 THEN 1 WHEN n=2 THEN 5 WHEN n=3 THEN 30 WHEN n=4 THEN 120 ELSE 300 END)
$$;


--
-- Name: payment_cancel_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_cancel_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE o payment_orders%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT o FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
 PERFORM id FROM tenants WHERE id=o.tenant_id FOR SHARE;PERFORM user_id FROM memberships WHERE tenant_id=o.tenant_id AND user_id=NEW.actor FOR SHARE;PERFORM id FROM users WHERE id=NEW.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=o.tenant_id AND m.user_id=NEW.actor AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'payment cancel owner forbidden';END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.operation_id AND kind='payment.order.cancel' AND actor=NEW.actor AND scope='tenant:'||o.tenant_id) OR
  EXISTS(SELECT 1 FROM payment_captures WHERE order_id=o.id) OR EXISTS(SELECT 1 FROM payment_provider_revisions WHERE order_id=o.id AND kind='closed') THEN RAISE EXCEPTION 'payment cancel state invalid';END IF;
 NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;


--
-- Name: payment_capture_evidence_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_capture_evidence_valid(evidence_ref text) RETURNS boolean
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE e payment_verified_evidence%ROWTYPE;o payment_orders%ROWTYPE;m payment_sandbox_environment%ROWTYPE;
 original jsonb;request jsonb;fact jsonb;received timestamptz;event_ref text;
 a payment_query_attempts%ROWTYPE;s payment_command_outbox%ROWTYPE;c payment_command_outbox%ROWTYPE;
BEGIN
 SELECT * INTO e FROM payment_verified_evidence WHERE id=evidence_ref;
 IF NOT FOUND OR e.kind<>'capture' THEN RETURN false;END IF;
 SELECT * INTO o FROM payment_orders WHERE id=e.order_id;IF NOT FOUND THEN RETURN false;END IF;
 SELECT * INTO m FROM payment_sandbox_environment WHERE singleton;IF NOT FOUND THEN RETURN false;END IF;
 IF m.database_oid<>(SELECT oid FROM pg_database WHERE datname=current_database()) OR m.schema_oid<>current_schema()::regnamespace::oid
  OR (e.environment_id,e.merchant_id) IS DISTINCT FROM (m.id,m.merchant_id)
  OR (e.environment_id,e.merchant_id,e.order_id,e.amount,e.currency) IS DISTINCT FROM (o.environment_id,o.merchant_id,o.id,o.amount,o.currency)
 THEN RETURN false;END IF;
 IF e.inbox_id IS NOT NULL AND e.verified_query_id IS NULL THEN
  SELECT convert_from(canonical_body,'UTF8')::jsonb,received_at,event_id INTO original,received,event_ref
   FROM payment_inbox WHERE id=e.inbox_id AND (environment_id,merchant_id)=(e.environment_id,e.merchant_id);
  IF NOT FOUND OR original->'schema_version' IS DISTINCT FROM '1'::jsonb OR original->>'event_type' IS DISTINCT FROM 'capture.confirmed'
   OR (original->>'environment_id',original->>'merchant_id',original->>'order_id',original->>'event_id') IS DISTINCT FROM (e.environment_id,e.merchant_id,e.order_id,event_ref)
  THEN RETURN false;END IF;
  fact:=original;
 ELSIF e.inbox_id IS NULL AND e.verified_query_id IS NOT NULL THEN
  SELECT convert_from(v.canonical_body,'UTF8')::jsonb,v.received_at,v.query_id INTO original,received,event_ref FROM payment_verified_queries v WHERE v.id=e.verified_query_id;
  IF NOT FOUND THEN RETURN false;END IF;
  SELECT * INTO a FROM payment_query_attempts WHERE query_id=event_ref;IF NOT FOUND THEN RETURN false;END IF;
  SELECT * INTO s FROM payment_command_outbox WHERE id=a.supervisor_id;IF NOT FOUND OR s.kind<>'query' THEN RETURN false;END IF;
  SELECT * INTO c FROM payment_command_outbox WHERE id=s.original_outbox_id;IF NOT FOUND OR c.kind NOT IN ('checkout','cancel') OR c.order_id<>e.order_id OR s.order_id<>e.order_id THEN RETURN false;END IF;
  request:=convert_from(a.canonical_request,'UTF8')::jsonb;
  IF request->'schema_version' IS DISTINCT FROM '1'::jsonb OR original->'schema_version' IS DISTINCT FROM '1'::jsonb
   OR (request->>'query_id',request->>'environment_id',request->>'merchant_id',request->>'merchant_order_id',request->>'command_id',request->>'command_kind',request->>'parameter_digest')
    IS DISTINCT FROM (a.query_id,o.environment_id,o.merchant_id,o.merchant_order_id,c.external_command_id,c.kind,c.parameter_digest)
   OR (original->>'query_id',original->>'environment_id',original->>'merchant_id',original->>'merchant_order_id',original->>'command_id',original->>'command_kind',original->>'parameter_digest')
    IS DISTINCT FROM (request->>'query_id',request->>'environment_id',request->>'merchant_id',request->>'merchant_order_id',request->>'command_id',request->>'command_kind',request->>'parameter_digest')
   OR a.created_generation<1 OR a.created_generation>s.generation OR a.mutation_generation<1 OR a.mutation_generation>c.generation
   OR a.request_digest<>encode(sha256(a.canonical_request),'hex') OR c.parameter_digest<>encode(sha256(c.canonical_parameters),'hex')
   OR original->>'command_state' IS DISTINCT FROM 'completed' OR original->'result'->>'status' IS DISTINCT FROM 'captured'
  THEN RETURN false;END IF;
  fact:=original->'result'->'capture';
  IF fact->>'provider_order_revision' IS DISTINCT FROM original->>'provider_order_revision' THEN RETURN false;END IF;
 ELSE RETURN false;END IF;
 RETURN coalesce((fact->>'provider_transaction_id',fact->>'amount',fact->>'currency',fact->>'occurred_at',fact->>'provider_order_revision')
   IS NOT DISTINCT FROM (e.provider_transaction_id,e.amount::text,e.currency,payment_time(e.happened_at),e.provider_order_revision::text)
  AND e.verified_at=received AND e.happened_at<=received+interval '5 seconds'
  AND e.fact_digest=encode(sha256(payment_frame(ARRAY[e.environment_id,e.merchant_id,e.order_id,'capture',e.provider_transaction_id,e.amount::text,e.currency,payment_time(e.happened_at),e.provider_order_revision::text])),'hex'),false);
EXCEPTION WHEN data_exception THEN RETURN false;
END $$;


--
-- Name: payment_capture_generation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_capture_generation() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE p payment_evidence_processing%ROWTYPE;
BEGIN
 SELECT * INTO STRICT p FROM payment_evidence_processing WHERE evidence_id=NEW.verified_evidence_id;
 IF p.state<>'leased' OR p.lease_until<=clock_timestamp() OR NEW.processing_generation<>p.generation THEN RAISE EXCEPTION 'payment source requires current evidence claim';END IF;
 NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;


--
-- Name: payment_capture_source_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_capture_source_valid(capture_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM payment_captures c JOIN payment_orders o ON o.id=c.order_id JOIN payment_order_capture_selections s ON s.order_id=o.id
 JOIN payment_transaction_identities i ON i.id=s.identity_id JOIN payment_verified_evidence e ON e.id=c.verified_evidence_id
 WHERE c.id=capture_ref AND payment_identity_valid(i.id) AND payment_capture_evidence_valid(e.id) AND payment_capture_evidence_valid(s.first_evidence_id) AND c.identity_id=i.id
 AND (c.tenant_id,c.environment_id,c.merchant_id,c.provider_transaction_id,c.amount,c.wallet_credit_amount,c.currency,c.captured_at,c.provider_order_revision)
  =(i.tenant_id,i.environment_id,i.merchant_id,i.provider_transaction_id,i.amount,o.wallet_credit_amount,i.currency,i.captured_at,i.provider_order_revision)
 AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
  =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)
 AND EXISTS(SELECT 1 FROM payment_verified_evidence first WHERE first.id=s.first_evidence_id
  AND (first.environment_id,first.merchant_id,first.order_id,first.provider_transaction_id,first.amount,first.currency,first.happened_at,first.provider_order_revision)
   =(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision))
 AND NOT EXISTS(SELECT 1 FROM payment_review_facts v WHERE v.selection_order_id=s.order_id AND v.reason='funding_amount_overflow'))
$$;


--
-- Name: payment_check_human(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_check_human(operation_ref text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE c commerce_commands%ROWTYPE;r commerce_operation_receipts%ROWTYPE;valid boolean:=false;
BEGIN
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=operation_ref;
 IF c.kind NOT LIKE 'payment.%' THEN RETURN;END IF;
 SELECT * INTO STRICT r FROM commerce_operation_receipts WHERE operation_id=c.operation_id;
 IF r.status='rejected' THEN
  IF (c.kind IN ('payment.sku.publish','payment.sku.activate') AND r.reason NOT IN ('payment_revision_conflict','payment_state_conflict')) OR
   (c.kind='payment.order.create' AND r.reason NOT IN ('payments_disabled','payment_sku_unavailable')) OR
   (c.kind='payment.order.cancel' AND r.reason<>'payment_state_conflict') THEN RAISE EXCEPTION 'payment terminal reason invalid';END IF;
  IF EXISTS(SELECT 1 FROM payment_sku_changes WHERE operation_id=c.operation_id) OR EXISTS(SELECT 1 FROM payment_sku_versions WHERE publish_operation_id=c.operation_id) OR
   EXISTS(SELECT 1 FROM payment_orders WHERE id=c.operation_id) OR EXISTS(SELECT 1 FROM payment_cancel_intents WHERE operation_id=c.operation_id) OR EXISTS(SELECT 1 FROM audit_events WHERE action=c.kind AND target=c.operation_id) THEN RAISE EXCEPTION 'rejected payment command has effect';END IF;RETURN;
 END IF;
 IF c.kind='payment.sku.publish' THEN
  SELECT EXISTS(SELECT 1 FROM payment_sku_versions v JOIN payment_sku_changes x ON x.published_version_id=v.id WHERE v.id=r.resource_id AND v.publish_operation_id=c.operation_id AND v.created_by=c.actor AND x.operation_id=c.operation_id AND x.sku_id=v.sku_id AND x.kind=c.kind) INTO valid;
 ELSIF c.kind='payment.sku.activate' THEN
  SELECT EXISTS(SELECT 1 FROM payment_sku_changes x WHERE x.operation_id=c.operation_id AND x.sku_id=r.resource_id AND x.kind=c.kind) INTO valid;
 ELSIF c.kind='payment.order.create' THEN
  SELECT EXISTS(SELECT 1 FROM payment_orders o JOIN payment_command_outbox b ON b.order_id=o.id AND b.kind='checkout' WHERE o.id=c.operation_id AND o.id=r.resource_id AND o.creator=c.actor AND c.scope='tenant:'||o.tenant_id) INTO valid;
 ELSE
  SELECT EXISTS(SELECT 1 FROM payment_cancel_intents x JOIN payment_orders o ON o.id=x.order_id JOIN payment_command_outbox b ON b.order_id=o.id AND b.kind='cancel' WHERE x.operation_id=c.operation_id AND x.order_id=r.resource_id AND x.actor=c.actor AND c.scope='tenant:'||o.tenant_id) INTO valid;
 END IF;
 IF NOT valid OR NOT EXISTS(SELECT 1 FROM audit_events WHERE action=c.kind AND target=c.operation_id AND actor=c.actor AND tenant_scope IS NOT DISTINCT FROM (CASE WHEN c.scope='platform' THEN NULL ELSE substr(c.scope,8) END) AND sanitized_diff='{}'::jsonb) THEN RAISE EXCEPTION 'payment command resource incomplete' USING ERRCODE='23514';END IF;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'payment command terminal missing' USING ERRCODE='23514';
END $$;


--
-- Name: payment_evidence_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_evidence_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM payment_evidence_processing WHERE evidence_id=NEW.id) THEN RAISE EXCEPTION 'payment evidence requires processing root';END IF;RETURN NULL;
END $$;


--
-- Name: payment_evidence_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_evidence_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE j jsonb;q jsonb;fact jsonb;r jsonb;received timestamptz;env text;merchant text;oid text;evtype text;failed text;
BEGIN
 PERFORM payment_require_rc();NEW.provider_transaction_id:=NULL;NEW.amount:=NULL;NEW.currency:=NULL;NEW.happened_at:=NULL;NEW.provider_order_revision:=NULL;
 IF NEW.inbox_id IS NOT NULL AND NEW.verified_query_id IS NULL THEN
  SELECT convert_from(canonical_body,'UTF8')::jsonb,received_at,environment_id,merchant_id INTO STRICT j,received,env,merchant FROM payment_inbox WHERE id=NEW.inbox_id;
  IF j->>'environment_id' IS DISTINCT FROM env OR j->>'merchant_id' IS DISTINCT FROM merchant OR (j->>'schema_version')::integer<>1 THEN RAISE EXCEPTION 'payment notification original mismatch';END IF;
  oid:=j->>'order_id';evtype:=j->>'event_type';failed:=j->>'failure_code';
  NEW.provider_transaction_id:=j->>'provider_transaction_id';NEW.amount:=(j->>'amount')::bigint;NEW.currency:=j->>'currency';
  NEW.happened_at:=(j->>'occurred_at')::timestamptz;NEW.provider_order_revision:=(j->>'provider_order_revision')::bigint;
  NEW.kind:=CASE WHEN evtype='capture.confirmed' THEN 'capture' WHEN evtype='checkout.failed' AND failed='order_closed' THEN 'closed' WHEN evtype='checkout.failed' THEN 'observation' ELSE 'unsupported' END;
 ELSIF NEW.inbox_id IS NULL AND NEW.verified_query_id IS NOT NULL THEN
  SELECT convert_from(v.canonical_body,'UTF8')::jsonb,convert_from(a.canonical_request,'UTF8')::jsonb,v.received_at INTO STRICT j,q,received
   FROM payment_verified_queries v JOIN payment_query_attempts a ON a.query_id=v.query_id WHERE v.id=NEW.verified_query_id;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'query_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest')
   IS DISTINCT FROM (q->>'schema_version',q->>'environment_id',q->>'merchant_id',q->>'query_id',q->>'merchant_order_id',q->>'command_id',q->>'command_kind',q->>'parameter_digest') THEN RAISE EXCEPTION 'payment query original mismatch';END IF;
  env:=j->>'environment_id';merchant:=j->>'merchant_id';oid:=split_part(j->>'merchant_order_id','.',3);r:=j->'result';
  NEW.kind:='observation';
  IF j->>'command_state'='completed' AND r->>'status'='captured' THEN
   fact:=r->'capture';NEW.kind:='capture';NEW.provider_transaction_id:=fact->>'provider_transaction_id';NEW.amount:=(fact->>'amount')::bigint;NEW.currency:=fact->>'currency';
   NEW.happened_at:=(fact->>'occurred_at')::timestamptz;NEW.provider_order_revision:=(fact->>'provider_order_revision')::bigint;
   IF fact->>'provider_order_revision' IS DISTINCT FROM j->>'provider_order_revision' THEN RAISE EXCEPTION 'payment query revision mismatch';END IF;
  ELSIF j->>'command_state'='completed' AND r->>'status'='closed' THEN
   NEW.kind:='closed';NEW.happened_at:=(r->>'closed_at')::timestamptz;NEW.provider_order_revision:=(j->>'provider_order_revision')::bigint;
  END IF;
 ELSE RAISE EXCEPTION 'payment evidence must have one original';END IF;
 NEW.environment_id:=env;NEW.merchant_id:=merchant;NEW.order_id:=oid;NEW.verified_at:=received;
 IF NEW.happened_at>received+interval '5 seconds' THEN RAISE EXCEPTION 'payment verified fact from future';END IF;
 IF NEW.kind='capture' THEN
  NEW.fact_digest:=encode(sha256(payment_frame(ARRAY[env,merchant,oid,'capture',NEW.provider_transaction_id,NEW.amount::text,NEW.currency,payment_time(NEW.happened_at),NEW.provider_order_revision::text])),'hex');
 ELSIF NEW.kind='closed' THEN NEW.fact_digest:=encode(sha256(payment_frame(ARRAY[env,merchant,oid,'closed',payment_time(NEW.happened_at),NEW.provider_order_revision::text])),'hex');
 ELSE NEW.fact_digest:=encode(sha256(payment_frame(ARRAY[env,merchant,oid,NEW.kind,coalesce(j->>'event_id',j->>'query_id')])),'hex');END IF;
 RETURN NEW;
END $$;


--
-- Name: payment_extra_funding_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_extra_funding_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE jid text;
BEGIN
 IF TG_TABLE_NAME='journals' THEN IF NEW.kind<>'sandbox_capture' THEN RETURN NULL;END IF;jid:=NEW.id;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM ledger_accounts WHERE id=NEW.account_id AND kind='SANDBOX_FUNDING_CLEARING') THEN RETURN NULL;END IF;jid:=NEW.journal_id;
 END IF;
 IF NOT payment_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'sandbox journal or clearing missing funding source' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;


--
-- Name: payment_frame(text[]); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_frame(parts text[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE result bytea:=''::bytea;part text;
BEGIN FOREACH part IN ARRAY parts LOOP
 IF part IS NULL THEN result:=result||int4send(-1);ELSE result:=result||int4send(octet_length(convert_to(part,'UTF8')))||convert_to(part,'UTF8');END IF;
 END LOOP;RETURN result;
END $$;


--
-- Name: payment_funding_journal_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_funding_journal_valid(journal_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 LEFT JOIN credit_orders c ON f.source_kind='manual_credit' AND c.id=f.source_id
 LEFT JOIN vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 LEFT JOIN voucher_batches b ON b.id=v.batch_id
 LEFT JOIN voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 LEFT JOIN commerce_commands cmd ON cmd.operation_id=r.operation_id
 LEFT JOIN commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id AND j.currency='CNY' AND f.currency='CNY' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.state='approved' AND c.journal_id=j.id AND c.tenant_id=f.tenant_id AND c.amount=f.amount AND c.approver<>c.requester AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND j.actor=c.approver)
 OR (f.source_kind='voucher_grant' AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
  AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
  AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
  AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
  AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;


--
-- Name: payment_human_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_human_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE operation_ref text;s payment_skus%ROWTYPE;c payment_sku_changes%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='payment_skus' THEN
  SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.id;
  SELECT * INTO c FROM payment_sku_changes WHERE operation_id=s.last_change_operation_id;
  IF c.operation_id IS NULL OR s.last_version<1 OR (s.revision,s.current_version_id,s.enabled) IS DISTINCT FROM (c.after_revision,c.after_version_id,c.after_enabled)
   OR NOT EXISTS(SELECT 1 FROM payment_sku_versions WHERE sku_id=s.id AND version=s.last_version) THEN RAISE EXCEPTION 'payment SKU root incomplete';END IF;
  operation_ref:=s.last_change_operation_id;
 ELSIF TG_TABLE_NAME='payment_sku_versions' THEN operation_ref:=NEW.publish_operation_id;
 ELSIF TG_TABLE_NAME='payment_orders' THEN operation_ref:=NEW.id;
 ELSIF TG_TABLE_NAME='payment_command_outbox' THEN
  IF NEW.kind='query' THEN RETURN NULL;END IF;
  IF NEW.kind='checkout' THEN operation_ref:=NEW.order_id;ELSE SELECT operation_id INTO operation_ref FROM payment_cancel_intents WHERE order_id=NEW.order_id;END IF;
 ELSE operation_ref:=NEW.operation_id;END IF;
 PERFORM payment_check_human(operation_ref);RETURN NULL;
END $$;


--
-- Name: payment_identity_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_identity_valid(identity_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_orders o ON o.id=i.order_id
 JOIN payment_verified_evidence e ON e.id=i.evidence_id JOIN payment_provider_revisions r ON (r.environment_id,r.merchant_id,r.order_id,r.provider_order_revision)=(i.environment_id,i.merchant_id,i.order_id,i.provider_order_revision)
 WHERE i.id=identity_ref AND payment_capture_evidence_valid(e.id) AND e.kind='capture' AND (i.environment_id,i.merchant_id,i.order_id,i.tenant_id,i.amount,i.currency)=(o.environment_id,o.merchant_id,o.id,o.tenant_id,o.amount,o.currency)
 AND (i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)=(e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)
 AND r.kind='capture' AND r.fact_digest=e.fact_digest AND i.captured_at>=o.created_at AND i.captured_at<o.expires_at
 AND NOT EXISTS(SELECT 1 FROM payment_provider_revisions closed JOIN payment_verified_evidence ce ON ce.id=closed.evidence_id WHERE closed.order_id=o.id AND closed.kind='closed' AND (i.captured_at>=ce.happened_at OR i.provider_order_revision>=closed.provider_order_revision)))
$$;


--
-- Name: payment_observation_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_observation_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE b payment_command_outbox%ROWTYPE;o payment_orders%ROWTYPE;j jsonb;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=NEW.outbox_id AND kind IN ('checkout','cancel');
 SELECT * INTO STRICT o FROM payment_orders WHERE id=b.order_id;j:=convert_from(NEW.canonical_reply,'UTF8')::jsonb;
 IF NEW.generation>b.generation OR (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM ('1',o.environment_id,o.merchant_id,o.merchant_order_id,b.external_command_id,b.kind,b.parameter_digest) OR j->>'command_state' NOT IN ('in_progress','completed') THEN RAISE EXCEPTION 'payment command observation binding mismatch';END IF;
 NEW.observed_at:=clock_timestamp();RETURN NEW;
END $$;


--
-- Name: payment_order_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_order_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE v payment_sku_versions%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();
 PERFORM s.id FROM payment_skus s JOIN payment_sku_versions pv ON pv.sku_id=s.id WHERE pv.id=NEW.sku_version_id FOR SHARE OF s;
 PERFORM singleton FROM payment_policy WHERE singleton FOR SHARE;
 PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
 PERFORM user_id FROM memberships WHERE tenant_id=NEW.tenant_id AND user_id=NEW.creator FOR SHARE;
 PERFORM id FROM users WHERE id=NEW.creator FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=NEW.tenant_id AND m.user_id=NEW.creator AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'payment order owner forbidden';END IF;
 SELECT * INTO STRICT v FROM payment_sku_versions WHERE id=NEW.sku_version_id;
 IF NOT EXISTS(SELECT 1 FROM payment_skus WHERE id=v.sku_id AND enabled AND current_version_id=v.id) OR NOT EXISTS(SELECT 1 FROM payment_policy WHERE singleton AND enabled) THEN RAISE EXCEPTION 'payment order sale unavailable';END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands WHERE operation_id=NEW.id AND kind='payment.order.create' AND actor=NEW.creator AND scope='tenant:'||NEW.tenant_id) THEN RAISE EXCEPTION 'payment order command mismatch';END IF;
 NEW.display_name:=v.display_name;NEW.amount:=v.amount;NEW.wallet_credit_amount:=v.wallet_credit_amount;NEW.currency:=v.currency;
 NEW.environment_id:=v.environment_id;NEW.merchant_id:=v.merchant_id;NEW.payee_label:=v.payee_label;
 NEW.merchant_order_id:='mps1.'||v.environment_id||'.'||NEW.id;NEW.created_at:=clock_timestamp();NEW.expires_at:=NEW.created_at+interval '900 seconds';RETURN NEW;
END $$;


--
-- Name: payment_original_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_original_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 IF TG_TABLE_NAME='payment_inbox' THEN
  IF NOT EXISTS(SELECT 1 FROM payment_verified_evidence WHERE inbox_id=NEW.id) THEN RAISE EXCEPTION 'payment notification evidence missing';END IF;
 ELSE
  IF NOT EXISTS(SELECT 1 FROM payment_verified_evidence WHERE verified_query_id=NEW.id) THEN RAISE EXCEPTION 'payment query evidence missing';END IF;
 END IF;RETURN NULL;
END $$;


--
-- Name: payment_original_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_original_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE j jsonb;q jsonb;
BEGIN
 PERFORM payment_require_rc();j:=convert_from(NEW.canonical_body,'UTF8')::jsonb;NEW.received_at:=clock_timestamp();
 IF TG_TABLE_NAME='payment_inbox' THEN
  NEW.environment_id:=j->>'environment_id';NEW.merchant_id:=j->>'merchant_id';NEW.event_id:=j->>'event_id';
 ELSE
  SELECT convert_from(canonical_request,'UTF8')::jsonb INTO STRICT q FROM payment_query_attempts WHERE query_id=NEW.query_id;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'query_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM (q->>'schema_version',q->>'environment_id',q->>'merchant_id',q->>'query_id',q->>'merchant_order_id',q->>'command_id',q->>'command_kind',q->>'parameter_digest') OR j->>'command_state' NOT IN ('not_seen','in_progress','completed') THEN RAISE EXCEPTION 'payment query original binding mismatch';END IF;
 END IF;RETURN NEW;
END $$;


--
-- Name: payment_outbox_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_outbox_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NEW.kind<>'query' AND (NEW.state='submission_unknown' OR (NEW.state='completed' AND EXISTS(SELECT 1 FROM payment_command_observations o WHERE o.outbox_id=NEW.id AND o.generation=NEW.generation AND (convert_from(o.canonical_reply,'UTF8')::jsonb->>'command_state'='in_progress' OR convert_from(o.canonical_reply,'UTF8')::jsonb#>>'{result,status}'='captured')))) AND NOT EXISTS(SELECT 1 FROM payment_command_outbox WHERE original_outbox_id=NEW.id AND kind='query') THEN RAISE EXCEPTION 'payment unknown mutation needs query supervisor';END IF;
 IF NEW.kind='query' AND NEW.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_query_attempts WHERE supervisor_id=NEW.id AND query_id=NEW.current_query_id) THEN RAISE EXCEPTION 'payment query pointer incomplete';END IF;RETURN NULL;
END $$;


--
-- Name: payment_outbox_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_outbox_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE o payment_orders%ROWTYPE;b payment_command_outbox%ROWTYPE;j jsonb;worker name;
BEGIN
 PERFORM payment_require_rc();SELECT worker_role INTO STRICT worker FROM payment_sandbox_environment WHERE singleton;
 IF NEW.state<>'pending' OR NEW.generation<>0 OR NEW.attempt_count<>0 OR NEW.lease_until IS NOT NULL OR NEW.current_query_id IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.last_submission_unknown_at IS NOT NULL THEN RAISE EXCEPTION 'payment outbox initial state';END IF;
 SELECT * INTO STRICT o FROM payment_orders WHERE id=NEW.order_id;
 IF NEW.kind='query' THEN
  IF current_user<>worker THEN RAISE EXCEPTION 'payment query supervisor requires worker' USING ERRCODE='42501';END IF;
  SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=NEW.original_outbox_id AND kind IN ('checkout','cancel') AND order_id=o.id;
 ELSE
  IF current_user=worker THEN RAISE EXCEPTION 'payment mutation requires application' USING ERRCODE='42501';END IF;
  j:=convert_from(NEW.canonical_parameters,'UTF8')::jsonb;
  IF (j->>'schema_version',j->>'environment_id',j->>'merchant_id',j->>'command_id',j->>'merchant_order_id') IS DISTINCT FROM ('1',o.environment_id,o.merchant_id,NEW.external_command_id,o.merchant_order_id) THEN RAISE EXCEPTION 'payment mutation frozen identity mismatch';END IF;
  IF NEW.kind='checkout' AND ((j->>'amount',j->>'currency',j->>'created_at',j->>'expires_at') IS DISTINCT FROM (o.amount::text,o.currency,payment_time(o.created_at),payment_time(o.expires_at)) OR (SELECT count(*) FROM jsonb_object_keys(j))<>9) THEN RAISE EXCEPTION 'payment checkout frozen tuple mismatch';END IF;
  IF NEW.kind='cancel' AND ((SELECT count(*) FROM jsonb_object_keys(j))<>5 OR NOT EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=o.id)) THEN RAISE EXCEPTION 'payment cancel command missing intent';END IF;
 END IF;
 NEW.created_at:=clock_timestamp();NEW.next_attempt_at:=NEW.created_at;RETURN NEW;
END $$;


--
-- Name: payment_outbox_transition(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_outbox_transition() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE stamp timestamptz:=clock_timestamp();budget integer;j jsonb;child payment_query_attempts%ROWTYPE;prior_unknown timestamptz;
BEGIN
 PERFORM payment_require_rc();
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment lease requires worker' USING ERRCODE='42501';END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment outbox cannot be deleted';END IF;
 IF (NEW.id,NEW.order_id,NEW.kind,NEW.external_command_id,NEW.original_outbox_id,NEW.canonical_parameters,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.order_id,OLD.kind,OLD.external_command_id,OLD.original_outbox_id,OLD.canonical_parameters,OLD.created_at) THEN RAISE EXCEPTION 'payment outbox identity immutable';END IF;
 budget:=CASE WHEN OLD.kind='query' THEN 20 ELSE 10 END;
 IF OLD.state IN ('completed','review_required') THEN RAISE EXCEPTION 'payment outbox terminal immutable';END IF;
 IF NEW.state='leased' AND OLD.state IN ('pending','retry_wait') THEN
  IF OLD.next_attempt_at>stamp OR OLD.attempt_count>=budget OR NEW.generation<>OLD.generation+1 OR NEW.attempt_count<>OLD.attempt_count+1 OR NEW.current_query_id IS DISTINCT FROM OLD.current_query_id THEN RAISE EXCEPTION 'payment outbox claim invalid';END IF;
  NEW.lease_until:=stamp+interval '60 seconds';NEW.last_error_code:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;NEW.last_submission_unknown_at:=OLD.last_submission_unknown_at;RETURN NEW;
 END IF;
 IF NEW.generation<>OLD.generation OR NEW.attempt_count<>OLD.attempt_count THEN RAISE EXCEPTION 'payment outbox budget immutable outside claim';END IF;
 IF OLD.kind='query' AND OLD.state='leased' AND NEW.state='leased' THEN
  IF OLD.lease_until<=stamp OR NEW.current_query_id IS NOT DISTINCT FROM OLD.current_query_id THEN RAISE EXCEPTION 'payment query pointer preparation invalid';END IF;
  SELECT * INTO STRICT child FROM payment_query_attempts WHERE query_id=NEW.current_query_id AND supervisor_id=OLD.id AND created_generation=OLD.generation;
  IF OLD.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_verified_queries WHERE query_id=OLD.current_query_id) THEN RAISE EXCEPTION 'payment unknown query pointer cannot be replaced';END IF;
  NEW.lease_until:=OLD.lease_until;NEW.next_attempt_at:=OLD.next_attempt_at;NEW.last_error_code:=OLD.last_error_code;NEW.last_submission_unknown_at:=OLD.last_submission_unknown_at;RETURN NEW;
 END IF;
 IF NEW.current_query_id IS DISTINCT FROM OLD.current_query_id THEN RAISE EXCEPTION 'payment query pointer immutable outside preparation';END IF;
 NEW.lease_until:=NULL;NEW.last_submission_unknown_at:=OLD.last_submission_unknown_at;
 IF OLD.state='leased' THEN
  IF OLD.kind='query' THEN
   IF OLD.lease_until>stamp THEN
    SELECT convert_from(canonical_body,'UTF8')::jsonb INTO j FROM payment_verified_queries WHERE query_id=OLD.current_query_id;
    IF NEW.state='completed' THEN
     IF j IS NULL OR j->>'command_state'<>'completed' THEN RAISE EXCEPTION 'payment completed query requires frozen completed snapshot';END IF;NEW.last_error_code:=NULL;
    ELSIF NEW.state='retry_wait' THEN
     IF OLD.attempt_count>=20 OR (j IS NOT NULL AND j->>'command_state'='completed') THEN RAISE EXCEPTION 'payment query retry invalid';END IF;
     IF NEW.last_error_code IS NULL AND j IS NULL THEN RAISE EXCEPTION 'payment unknown query requires fixed error';END IF;
    ELSIF NEW.state='review_required' THEN
     IF j IS NOT NULL AND j->>'command_state'='completed' THEN RAISE EXCEPTION 'payment completed query cannot become exhausted review';END IF;
     IF OLD.attempt_count<20 AND NEW.last_error_code IS DISTINCT FROM 'permanent' THEN RAISE EXCEPTION 'payment premature query review';END IF;
     IF OLD.attempt_count=20 THEN NEW.last_error_code:='attempts_exhausted';END IF;
    ELSE RAISE EXCEPTION 'payment query lease transition invalid';END IF;
   ELSE
    IF NEW.state IS DISTINCT FROM (CASE WHEN OLD.attempt_count=20 THEN 'review_required' ELSE 'retry_wait' END) THEN RAISE EXCEPTION 'payment expired query transition invalid';END IF;
    NEW.last_error_code:=CASE WHEN OLD.attempt_count=20 THEN 'attempts_exhausted' ELSE 'timeout' END;
   END IF;
  ELSE
   IF OLD.lease_until>stamp AND NEW.state='completed' THEN
    IF NOT EXISTS(SELECT 1 FROM payment_command_observations WHERE outbox_id=OLD.id AND generation=OLD.generation) THEN RAISE EXCEPTION 'payment mutation ACK requires current observation';END IF;NEW.last_error_code:=NULL;
   ELSIF NEW.state='submission_unknown' THEN
    NEW.last_submission_unknown_at:=stamp;NEW.last_error_code:=CASE WHEN OLD.attempt_count=10 THEN 'attempts_exhausted' ELSE coalesce(NEW.last_error_code,'timeout') END;
   ELSIF OLD.lease_until>stamp AND NEW.state='review_required' AND NEW.last_error_code='permanent' THEN NULL;
   ELSE RAISE EXCEPTION 'payment mutation lease transition invalid';END IF;
  END IF;
 ELSIF OLD.kind<>'query' AND OLD.state='submission_unknown' AND NEW.state IN ('retry_wait','completed') THEN
  SELECT convert_from(v.canonical_body,'UTF8')::jsonb INTO j FROM payment_command_outbox s JOIN payment_query_attempts a ON a.supervisor_id=s.id JOIN payment_verified_queries v ON v.query_id=a.query_id WHERE s.original_outbox_id=OLD.id AND (NEW.state='completed' OR (s.state IN ('leased','retry_wait') AND s.current_query_id=a.query_id)) AND a.mutation_generation=OLD.generation AND a.created_at>=OLD.last_submission_unknown_at AND convert_from(v.canonical_body,'UTF8')::jsonb->>'command_state'=CASE WHEN NEW.state='completed' THEN 'completed' ELSE 'not_seen' END ORDER BY a.created_at DESC LIMIT 1;
  IF j IS NULL OR (NEW.state='retry_wait' AND (OLD.attempt_count>=10 OR (OLD.kind='checkout' AND EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=OLD.order_id)))) THEN RAISE EXCEPTION 'payment mutation recovery requires current signed query';END IF;
  NEW.last_error_code:=NULL;
 ELSE RAISE EXCEPTION 'payment outbox transition invalid';END IF;
 NEW.next_attempt_at:=CASE WHEN NEW.state='retry_wait' THEN stamp+payment_backoff(OLD.attempt_count) ELSE OLD.next_attempt_at END;
 RETURN NEW;
END $$;


--
-- Name: payment_policy_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_policy_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 PERFORM payment_require_rc();IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment policy cannot be deleted';END IF;
 IF NEW.singleton IS DISTINCT FROM OLD.singleton THEN RAISE EXCEPTION 'payment policy identity immutable';END IF;
 NEW.revision:=OLD.revision+CASE WHEN NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END;RETURN NEW;
END $$;


--
-- Name: payment_processing_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_processing_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE stamp timestamptz:=clock_timestamp();e payment_verified_evidence%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment processing cannot be deleted';END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.generation<>0 OR NEW.attempt_count<>0 OR NEW.lease_until IS NOT NULL OR NEW.last_error_code IS NOT NULL THEN RAISE EXCEPTION 'payment processing initial state';END IF;
  NEW.next_attempt_at:=stamp;RETURN NEW;
 END IF;
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment processing requires worker' USING ERRCODE='42501';END IF;
 IF NEW.evidence_id IS DISTINCT FROM OLD.evidence_id OR OLD.state IN ('processed','processing_review') THEN RAISE EXCEPTION 'payment processing terminal/identity immutable';END IF;
 IF OLD.state IN ('pending','retry_wait') AND NEW.state='leased' THEN
  IF OLD.next_attempt_at>stamp OR OLD.attempt_count>=20 OR NEW.generation<>OLD.generation+1 OR NEW.attempt_count<>OLD.attempt_count+1 THEN RAISE EXCEPTION 'payment processing claim invalid';END IF;
  NEW.lease_until:=stamp+interval '30 seconds';NEW.last_error_code:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;RETURN NEW;
 END IF;
 IF OLD.state<>'leased' OR NEW.generation<>OLD.generation OR NEW.attempt_count<>OLD.attempt_count THEN RAISE EXCEPTION 'payment processing transition invalid';END IF;
 IF OLD.lease_until<=stamp THEN
  IF NEW.state IS DISTINCT FROM (CASE WHEN OLD.attempt_count=20 THEN 'processing_review' ELSE 'retry_wait' END) THEN RAISE EXCEPTION 'payment expired processing transition invalid';END IF;
  NEW.last_error_code:=CASE WHEN OLD.attempt_count=20 THEN 'attempts_exhausted' ELSE 'temporary' END;
 ELSIF NEW.state='processed' THEN
  SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=OLD.evidence_id;
  IF e.kind='closed' AND NOT EXISTS(SELECT 1 FROM payment_provider_revisions r WHERE r.order_id=e.order_id AND r.provider_order_revision=e.provider_order_revision AND r.kind='closed' AND r.fact_digest=e.fact_digest) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE evidence_id=e.id) THEN RAISE EXCEPTION 'payment closed processing requires revision or review';END IF;
  IF e.kind IN ('capture','unsupported') AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE evidence_id=e.id) AND NOT EXISTS(SELECT 1 FROM payment_captures c JOIN payment_transaction_identities i ON i.id=c.identity_id WHERE c.order_id=e.order_id AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)=(i.environment_id,i.merchant_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)) THEN RAISE EXCEPTION 'payment processing has no matching capture or review';END IF;
  NEW.last_error_code:=NULL;
 ELSIF NEW.state='processing_review' THEN
  IF NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE evidence_id=OLD.evidence_id) AND NOT (OLD.attempt_count=20 AND NEW.last_error_code='attempts_exhausted') THEN RAISE EXCEPTION 'payment processing review missing fact';END IF;
 ELSIF NEW.state='retry_wait' THEN
  IF OLD.attempt_count>=20 OR NEW.last_error_code IS DISTINCT FROM 'temporary' THEN RAISE EXCEPTION 'payment processing retry invalid';END IF;
 ELSE RAISE EXCEPTION 'payment processing ACK invalid';END IF;
 NEW.lease_until:=NULL;NEW.next_attempt_at:=CASE WHEN NEW.state='retry_wait' THEN stamp+payment_backoff(OLD.attempt_count) ELSE OLD.next_attempt_at END;RETURN NEW;
END $$;


--
-- Name: payment_query_attempt_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_query_attempt_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE s payment_command_outbox%ROWTYPE;b payment_command_outbox%ROWTYPE;o payment_orders%ROWTYPE;j jsonb;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT s FROM payment_command_outbox WHERE id=NEW.supervisor_id AND kind='query';
 SELECT * INTO STRICT b FROM payment_command_outbox WHERE id=s.original_outbox_id;
 SELECT * INTO STRICT o FROM payment_orders WHERE id=b.order_id;j:=convert_from(NEW.canonical_request,'UTF8')::jsonb;
 IF s.state<>'leased' OR s.lease_until<=clock_timestamp() OR NEW.created_generation<>s.generation OR b.generation=0 THEN RAISE EXCEPTION 'payment query preparation requires current claim';END IF;
 IF s.current_query_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_verified_queries WHERE query_id=s.current_query_id) THEN RAISE EXCEPTION 'payment unknown query must reuse immutable child';END IF;
 IF (j->>'schema_version',j->>'query_id',j->>'environment_id',j->>'merchant_id',j->>'merchant_order_id',j->>'command_id',j->>'command_kind',j->>'parameter_digest') IS DISTINCT FROM ('1',NEW.query_id,o.environment_id,o.merchant_id,o.merchant_order_id,b.external_command_id,b.kind,b.parameter_digest) OR (SELECT count(*) FROM jsonb_object_keys(j))<>8 THEN RAISE EXCEPTION 'payment query request binding mismatch';END IF;
 NEW.mutation_generation:=b.generation;NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;


--
-- Name: payment_ref(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_ref(v text) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT coalesce(octet_length(v) BETWEEN 1 AND 100 AND v!~'[[:cntrl:]]',false)
$$;


--
-- Name: payment_require_rc(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_require_rc() RETURNS void
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'payment writes require read committed' USING ERRCODE='23514';END IF;
END $$;


--
-- Name: payment_review_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_review_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE e payment_verified_evidence%ROWTYPE;o payment_orders%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=NEW.evidence_id;
 IF NEW.order_id IS DISTINCT FROM e.order_id THEN RAISE EXCEPTION 'payment review order/evidence mismatch';END IF;
 SELECT * INTO o FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
 IF (NEW.reason='unknown_order' AND o.id IS NOT NULL) OR (NEW.reason='amount_mismatch' AND (o.id IS NULL OR e.amount IS NOT DISTINCT FROM o.amount)) OR (NEW.reason='currency_mismatch' AND (o.id IS NULL OR e.currency IS NOT DISTINCT FROM o.currency)) OR (NEW.reason='capture_outside_window' AND (o.id IS NULL OR (e.happened_at>=o.created_at AND e.happened_at<o.expires_at))) OR (NEW.reason='unsupported_effect_review' AND e.kind<>'unsupported') THEN RAISE EXCEPTION 'payment review reason does not match evidence';END IF;
 IF NEW.identity_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_transaction_identities WHERE id=NEW.identity_id AND order_id=NEW.order_id) THEN RAISE EXCEPTION 'payment review identity mismatch';END IF;
 NEW.created_at:=clock_timestamp();
 IF NEW.reason='funding_amount_overflow' AND (NEW.selection_order_id IS DISTINCT FROM NEW.order_id OR
  NOT EXISTS(SELECT 1 FROM payment_order_capture_selections s WHERE s.order_id=NEW.order_id AND s.identity_id=NEW.identity_id)
  OR EXISTS(SELECT 1 FROM payment_captures WHERE order_id=NEW.order_id)) THEN
  RAISE EXCEPTION 'payment overflow review requires unfunded selected identity';END IF;
 IF NEW.reason='duplicate_order_capture' AND NOT EXISTS(SELECT 1 FROM payment_order_capture_selections s
  JOIN payment_transaction_identities i ON i.id=NEW.identity_id WHERE s.order_id=NEW.order_id AND i.order_id=s.order_id AND s.identity_id<>i.id) THEN
  RAISE EXCEPTION 'payment duplicate capture review requires other selected identity';END IF;
 RETURN NEW;
END $$;


--
-- Name: payment_revision_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_revision_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE e payment_verified_evidence%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT e FROM payment_verified_evidence WHERE id=NEW.evidence_id;
 IF e.kind NOT IN ('capture','closed') THEN RAISE EXCEPTION 'payment provider revision needs authoritative fact';END IF;
 PERFORM id FROM payment_orders WHERE id=e.order_id FOR NO KEY UPDATE;
 IF e.kind='closed' AND EXISTS(SELECT 1 FROM payment_transaction_identities i WHERE i.order_id=e.order_id
   AND (i.captured_at>=e.happened_at OR i.provider_order_revision>=e.provider_order_revision)) THEN
  RAISE EXCEPTION 'payment closed conflicts with prior immutable capture identity';END IF;
 NEW.environment_id:=e.environment_id;NEW.merchant_id:=e.merchant_id;NEW.order_id:=e.order_id;NEW.provider_order_revision:=e.provider_order_revision;
 NEW.kind:=e.kind;NEW.fact_digest:=e.fact_digest;RETURN NEW;
END $$;


--
-- Name: payment_sandbox_journal_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_sandbox_journal_valid(journal_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='sandbox_capture'
 JOIN payment_captures c ON c.id=f.source_id JOIN payment_effects x ON x.capture_id=c.id
 WHERE j.id=journal_ref AND payment_capture_source_valid(c.id)
 AND EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_evidence_processing p ON p.evidence_id=i.evidence_id
  WHERE i.id=c.identity_id AND p.generation=i.processing_generation AND p.state IN ('processed','processing_review'))
 AND EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=c.verified_evidence_id AND p.state='processed' AND p.generation=c.processing_generation)
 AND EXISTS(SELECT 1 FROM audit_events a WHERE a.action='payment.capture.credited' AND a.target=c.id AND a.actor='payment-sandbox-worker' AND a.tenant_scope=c.tenant_id AND a.sanitized_diff=jsonb_build_object('amount',c.wallet_credit_amount::text) AND a.request_id='')
 AND (j.kind,j.operation_id,j.reference,j.actor,j.currency)=('sandbox_capture',c.id,c.order_id,'payment-sandbox-worker','CNY')
 AND (f.tenant_id,f.amount,f.currency,j.tenant_id)=(c.tenant_id,c.wallet_credit_amount,'CNY',c.tenant_id)
 AND (x.identity_id,x.environment_id,x.merchant_id,x.provider_transaction_id,x.effect_kind,x.effect_id)=(c.identity_id,c.environment_id,c.merchant_id,c.provider_transaction_id,'capture','capture')
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=c.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=c.wallet_credit_amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='SANDBOX_FUNDING_CLEARING' AND l.signed_delta=-c.wallet_credit_amount)
 AND (SELECT count(*) FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited' AND b.tenant_id=c.tenant_id AND b.schema_version=1 AND b.sanitized_payload=jsonb_build_object('amount',c.wallet_credit_amount::text)))
$$;


--
-- Name: payment_sku_change_apply(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_sku_change_apply() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 UPDATE payment_skus SET revision=NEW.after_revision,current_version_id=NEW.after_version_id,enabled=NEW.after_enabled,
  last_version=CASE WHEN NEW.kind='payment.sku.publish' THEN (SELECT version FROM payment_sku_versions WHERE id=NEW.published_version_id) ELSE last_version END,
  last_change_operation_id=NEW.operation_id WHERE id=NEW.sku_id;RETURN NULL;
END $$;


--
-- Name: payment_sku_change_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_sku_change_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE s payment_skus%ROWTYPE;c commerce_commands%ROWTYPE;v payment_sku_versions%ROWTYPE;first_publish boolean;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.sku_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=NEW.operation_id;
 IF c.kind IS DISTINCT FROM NEW.kind OR c.scope<>'platform' THEN RAISE EXCEPTION 'payment SKU change command mismatch';END IF;
 PERFORM id FROM users WHERE id=c.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=c.actor AND status='active' AND platform_role='admin') THEN RAISE EXCEPTION 'payment SKU actor forbidden';END IF;
 first_publish:=s.last_version=0 AND s.last_change_operation_id IS NULL;
 IF NEW.before_revision IS DISTINCT FROM (CASE WHEN first_publish THEN 0 ELSE s.revision END) THEN RAISE EXCEPTION 'payment SKU revision conflict';END IF;
 NEW.before_operation_id:=s.last_change_operation_id;NEW.before_version_id:=s.current_version_id;NEW.before_enabled:=s.enabled;
 IF NEW.kind='payment.sku.publish' THEN
  SELECT * INTO STRICT v FROM payment_sku_versions WHERE id=NEW.published_version_id AND sku_id=s.id AND publish_operation_id=c.operation_id;
  IF v.version<>s.last_version+1 OR (first_publish AND s.created_by<>c.actor) THEN RAISE EXCEPTION 'payment published version mismatch';END IF;
  NEW.after_version_id:=s.current_version_id;NEW.after_enabled:=s.enabled;NEW.after_revision:=CASE WHEN first_publish THEN 1 ELSE s.revision+1 END;
 ELSE
  IF first_publish OR NEW.published_version_id IS NOT NULL OR (NEW.after_enabled AND NEW.after_version_id IS NULL) OR
   (NEW.after_version_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM payment_sku_versions WHERE id=NEW.after_version_id AND sku_id=s.id)) THEN RAISE EXCEPTION 'payment activation state invalid';END IF;
  NEW.after_revision:=s.revision+CASE WHEN (NEW.after_version_id,NEW.after_enabled) IS DISTINCT FROM (s.current_version_id,s.enabled) THEN 1 ELSE 0 END;
 END IF;
 NEW.created_at:=clock_timestamp();NEW.created_xid:=pg_current_xact_id();RETURN NEW;
END $$;


--
-- Name: payment_sku_root_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_sku_root_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c payment_sku_changes%ROWTYPE;v bigint;
BEGIN
 PERFORM payment_require_rc();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment SKU cannot be deleted';END IF;
 IF TG_OP='INSERT' THEN
  NEW.revision:=1;NEW.last_version:=0;NEW.enabled:=false;NEW.current_version_id:=NULL;NEW.last_change_operation_id:=NULL;NEW.created_at:=clock_timestamp();RETURN NEW;
 END IF;
 IF (NEW.id,NEW.created_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.created_by,OLD.created_at) THEN RAISE EXCEPTION 'payment SKU identity immutable';END IF;
 SELECT * INTO STRICT c FROM payment_sku_changes WHERE operation_id=NEW.last_change_operation_id AND sku_id=OLD.id;
 SELECT version INTO v FROM payment_sku_versions WHERE id=c.published_version_id;
 IF c.created_xid IS DISTINCT FROM pg_current_xact_id() OR c.before_operation_id IS DISTINCT FROM OLD.last_change_operation_id
  OR (c.before_version_id,c.before_enabled) IS DISTINCT FROM (OLD.current_version_id,OLD.enabled)
  OR c.before_revision IS DISTINCT FROM (CASE WHEN OLD.last_version=0 AND OLD.last_change_operation_id IS NULL THEN 0 ELSE OLD.revision END)
  OR (NEW.revision,NEW.current_version_id,NEW.enabled,NEW.last_version) IS DISTINCT FROM
   (c.after_revision,c.after_version_id,c.after_enabled,CASE WHEN c.kind='payment.sku.publish' THEN v ELSE OLD.last_version END)
 THEN RAISE EXCEPTION 'payment SKU projection requires exact new change';END IF;RETURN NEW;
END $$;


--
-- Name: payment_sku_version_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_sku_version_insert() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE s payment_skus%ROWTYPE;c commerce_commands%ROWTYPE;env payment_sandbox_environment%ROWTYPE;
BEGIN
 PERFORM payment_require_rc();SELECT * INTO STRICT s FROM payment_skus WHERE id=NEW.sku_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT c FROM commerce_commands WHERE operation_id=NEW.publish_operation_id;
 IF c.kind<>'payment.sku.publish' OR c.scope<>'platform' OR c.actor IS DISTINCT FROM NEW.created_by THEN RAISE EXCEPTION 'payment SKU publish command mismatch';END IF;
 PERFORM id FROM users WHERE id=c.actor FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=c.actor AND status='active' AND platform_role='admin') THEN RAISE EXCEPTION 'payment SKU publisher forbidden';END IF;
 SELECT * INTO STRICT env FROM payment_sandbox_environment WHERE singleton;
 NEW.environment_id:=env.id;NEW.merchant_id:=env.merchant_id;NEW.payee_label:=env.payee_label;NEW.currency:='CNY';NEW.wallet_credit_amount:=NEW.amount;
 NEW.version:=s.last_version+1;NEW.checkout_ttl_seconds:=900;NEW.created_at:=clock_timestamp();RETURN NEW;
END $$;


--
-- Name: payment_source_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_source_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE identity_ref text;order_ref text;jid text;
BEGIN
 IF TG_TABLE_NAME='payment_transaction_identities' THEN identity_ref:=NEW.id;order_ref:=NEW.order_id;
  IF NOT payment_identity_valid(identity_ref) OR NOT EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=NEW.evidence_id AND p.generation=NEW.processing_generation AND p.state IN ('processed','processing_review')) THEN RAISE EXCEPTION 'payment transaction identity invalid' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM payment_order_capture_selections WHERE identity_id=identity_ref) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE identity_id=identity_ref AND order_id=order_ref AND reason='duplicate_order_capture') THEN RAISE EXCEPTION 'payment identity neither selected nor reviewed' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME='payment_order_capture_selections' THEN
  order_ref:=NEW.order_id;identity_ref:=NEW.identity_id;
  IF NOT payment_identity_valid(identity_ref) OR NOT EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_verified_evidence e ON e.id=NEW.first_evidence_id WHERE i.id=identity_ref AND payment_capture_evidence_valid(e.id) AND e.kind='capture' AND (e.environment_id,e.merchant_id,e.order_id,e.provider_transaction_id,e.amount,e.currency,e.happened_at,e.provider_order_revision)=(i.environment_id,i.merchant_id,i.order_id,i.provider_transaction_id,i.amount,i.currency,i.captured_at,i.provider_order_revision)) THEN RAISE EXCEPTION 'payment selection identity mismatch' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM payment_captures WHERE order_id=order_ref) AND NOT EXISTS(SELECT 1 FROM payment_review_facts WHERE selection_order_id=order_ref AND identity_id=identity_ref AND reason='funding_amount_overflow') THEN RAISE EXCEPTION 'payment selection missing funding or review' USING ERRCODE='23514';END IF;
 ELSIF TG_TABLE_NAME IN ('payment_captures','payment_effects') THEN
  IF TG_TABLE_NAME='payment_captures' THEN order_ref:=NEW.order_id;SELECT journal_id INTO jid FROM funding_receipts WHERE source_kind='sandbox_capture' AND source_id=NEW.id;
  ELSE SELECT c.order_id,f.journal_id INTO order_ref,jid FROM payment_captures c LEFT JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id WHERE c.id=NEW.capture_id;END IF;
  IF jid IS NULL OR NOT payment_sandbox_journal_valid(jid) THEN RAISE EXCEPTION 'payment funding graph incomplete' USING ERRCODE='23514';END IF;
 END IF;RETURN NULL;
END $$;


--
-- Name: payment_source_writer(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_source_writer() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE worker name;is_sandbox boolean;parent_kind text;processing payment_evidence_processing%ROWTYPE;
BEGIN
 SELECT worker_role INTO worker FROM payment_sandbox_environment WHERE singleton;
 IF TG_TABLE_NAME='funding_receipts' THEN is_sandbox:=NEW.source_kind='sandbox_capture';
 ELSIF TG_TABLE_NAME='journals' THEN is_sandbox:=NEW.kind='sandbox_capture';
 ELSIF TG_TABLE_NAME='projection_outbox' THEN
  IF NEW.kind<>'credited' THEN
   IF current_user=worker THEN RAISE EXCEPTION 'payment worker cannot emit other projection effects' USING ERRCODE='42501';END IF;RETURN NEW;
  END IF;SELECT kind INTO parent_kind FROM journals WHERE operation_id=NEW.operation_id;is_sandbox:=parent_kind='sandbox_capture';
 ELSE is_sandbox:=true;END IF;
 IF is_sandbox AND (worker IS NULL OR current_user<>worker OR session_user<>worker OR NOT EXISTS(SELECT 1 FROM payment_sandbox_environment WHERE singleton AND database_oid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND schema_oid=current_schema()::regnamespace::oid)) THEN RAISE EXCEPTION 'sandbox funding requires payment worker' USING ERRCODE='42501';END IF;
 IF current_user=worker AND NOT coalesce(is_sandbox,false) THEN RAISE EXCEPTION 'payment worker cannot write other funding' USING ERRCODE='42501';END IF;
 IF is_sandbox THEN
  PERFORM payment_require_rc();
  IF TG_TABLE_NAME IN ('payment_transaction_identities','payment_order_capture_selections','payment_captures') THEN
   PERFORM id FROM payment_orders WHERE id=NEW.order_id FOR NO KEY UPDATE;
   IF TG_TABLE_NAME='payment_transaction_identities' THEN
    SELECT * INTO STRICT processing FROM payment_evidence_processing WHERE evidence_id=NEW.evidence_id;
    IF processing.state<>'leased' OR processing.lease_until<=clock_timestamp() OR NEW.processing_generation IS DISTINCT FROM processing.generation THEN RAISE EXCEPTION 'payment identity requires exact caller processing claim';END IF;
    NEW.processing_generation:=processing.generation;NEW.created_at:=clock_timestamp();
   END IF;
  END IF;
 END IF;RETURN NEW;
END $$;


--
-- Name: payment_time(timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_time(v timestamp with time zone) RETURNS text
    LANGUAGE sql STABLE
    
    AS $$
 SELECT to_char(v AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
$$;


--
-- Name: payment_worker_account_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_worker_account_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RETURN NEW;END IF;
 IF TG_OP='DELETE' OR NEW.kind NOT IN ('AVAILABLE','SANDBOX_FUNDING_CLEARING') OR (TG_OP='INSERT' AND NEW.balance<>0) THEN RAISE EXCEPTION 'payment worker account kind or initial balance forbidden' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.kind) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.kind) THEN RAISE EXCEPTION 'payment worker account identity immutable' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;


--
-- Name: payment_worker_balance_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_worker_balance_complete() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE aid text;
BEGIN
 IF current_user IS DISTINCT FROM (SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RETURN NULL;END IF;
 IF TG_TABLE_NAME='ledger_accounts' THEN aid:=NEW.id;ELSE aid:=NEW.account_id;END IF;
 IF NOT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=aid AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)) THEN RAISE EXCEPTION 'payment worker balance cache mismatch' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;


--
-- Name: payment_worker_line_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_worker_line_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 IF current_user=(SELECT worker_role FROM payment_sandbox_environment WHERE singleton) AND NOT EXISTS(SELECT 1 FROM journals WHERE id=NEW.journal_id AND kind='sandbox_capture') THEN RAISE EXCEPTION 'payment worker cannot append other ledger effects' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;


--
-- Name: payment_worker_tenant_lock_only(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION payment_worker_tenant_lock_only() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$ BEGIN
 IF current_user=(SELECT worker_role FROM payment_sandbox_environment WHERE singleton) THEN RAISE EXCEPTION 'payment worker tenant privilege is lock-only' USING ERRCODE='42501';END IF;RETURN NEW;
END $$;


--
-- Name: pb_apply_event(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_apply_event() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE window_id text;
BEGIN
 UPDATE periodic_budget_allocations SET held_amount=NEW.after_held,spent_amount=NEW.after_spent,source_held=NEW.after_held,
  source_spent=NEW.after_source_spent,state=NEW.after_state,revision=revision+1,last_event_id=NEW.id
  WHERE id=NEW.allocation_id AND control_id=NEW.control_id RETURNING periodic_budget_allocations.window_id INTO window_id;
 IF window_id IS NOT NULL THEN
  UPDATE periodic_budget_windows SET held_amount=held_amount+NEW.held_delta,spent_amount=spent_amount+NEW.spent_delta,
   revision=revision+1,last_event_id=NEW.id WHERE id=window_id AND control_id=NEW.control_id;
 END IF;
 UPDATE periodic_budget_controls SET held_amount=held_amount+NEW.held_delta,spent_amount=spent_amount+NEW.spent_delta,
  event_seq=NEW.seq,last_event_id=NEW.id WHERE id=NEW.control_id;
 RETURN NULL;
END $$;


--
-- Name: pb_check_configuration(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_check_configuration() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;v periodic_budget_configuration_versions%ROWTYPE;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.id;
 SELECT * INTO v FROM periodic_budget_configuration_versions WHERE control_id=c.id AND revision=c.revision;
 IF (v.operation_id,v.period_kind,v.enabled,v.limit_amount) IS DISTINCT FROM (c.configuration_operation_id,c.period_kind,c.enabled,c.limit_amount) THEN
  RAISE EXCEPTION 'periodic configuration version/receipt missing' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;


--
-- Name: pb_check_customer(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_check_customer(subject text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE r request_records%ROWTYPE;v reservations%ROWTYPE;c periodic_budget_controls%ROWTYPE;
 a periodic_budget_allocations%ROWTYPE;s settlements%ROWTYPE;expected_h numeric;expected_s numeric;expected_state text;marker text;
BEGIN
 SELECT * INTO r FROM request_records WHERE id=subject;IF NOT FOUND THEN RETURN;END IF;
 SELECT * INTO v FROM reservations WHERE request_id=r.id;IF NOT FOUND THEN
  IF r.pb_created_xid IS NOT NULL OR r.pb_tenant_control IS NOT NULL OR r.pb_key_control IS NOT NULL THEN RAISE EXCEPTION 'periodic source reservation missing' USING ERRCODE='23514'; END IF;RETURN;END IF;
 SELECT * INTO s FROM settlements WHERE request_id=r.id;
 IF v.status IN ('reserved','pending_reconciliation') THEN
  IF s.id IS NOT NULL THEN RAISE EXCEPTION 'periodic pending source already settled' USING ERRCODE='23514'; END IF;
  expected_h:=v.amount;expected_s:=0;expected_state:='held';
 ELSE
  IF s.id IS NULL OR s.status IS DISTINCT FROM v.status THEN RAISE EXCEPTION 'periodic terminal source settlement missing' USING ERRCODE='23514'; END IF;
  expected_h:=0;expected_s:=s.charged_amount;expected_state:=s.status;
 END IF;
 FOR c IN SELECT * FROM periodic_budget_controls WHERE (scope='tenant' AND tenant_id=r.tenant_id)
    OR (scope='key' AND tenant_id=r.tenant_id AND key_id=r.key_id) LOOP
  marker:=CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control ELSE r.pb_key_control END;
  SELECT * INTO a FROM periodic_budget_allocations WHERE control_id=c.id AND subject_type='customer_request' AND subject_id=r.id;
  IF NOT FOUND THEN
   IF marker=c.id OR v.status IN ('reserved','pending_reconciliation') THEN
    RAISE EXCEPTION 'periodic customer allocation missing' USING ERRCODE='23514'; END IF;
  ELSE
   IF a.revision<=0 OR a.last_event_id IS NULL OR (a.held_amount,a.spent_amount,a.source_held,a.source_spent,a.state,a.upper_amount)
    IS DISTINCT FROM (expected_h,expected_s,expected_h,expected_s,expected_state,v.amount) THEN
    RAISE EXCEPTION 'periodic customer projection/source mismatch' USING ERRCODE='23514'; END IF;
   IF (marker=c.id AND a.origin<>'new') OR (marker IS NULL AND a.origin='new') THEN
    RAISE EXCEPTION 'periodic customer origin mismatch' USING ERRCODE='23514'; END IF;
  END IF;
 END LOOP;
END $$;


--
-- Name: pb_check_initial_import(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_check_initial_import() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;subject text;n integer:=0;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.id;
 IF c.scope='procurement' THEN
  FOR subject IN SELECT attempt_id FROM procurement_reservations WHERE account_id=c.account_id AND state IN ('prepared','sending','unknown','disputed') ORDER BY attempt_id LIMIT 1001 LOOP
   n:=n+1;PERFORM pb_check_supplier(subject);
  END LOOP;
 ELSE
  FOR subject IN SELECT v.request_id FROM reservations v JOIN request_records r ON r.id=v.request_id
   WHERE r.tenant_id=c.tenant_id AND (c.scope='tenant' OR r.key_id=c.key_id) AND v.status IN ('reserved','pending_reconciliation') ORDER BY v.request_id LIMIT 1001 LOOP
   n:=n+1;PERFORM pb_check_customer(subject);
  END LOOP;
 END IF;
 IF n>1000 THEN RAISE EXCEPTION 'periodic legacy import requires maintenance' USING ERRCODE='23514';END IF;RETURN NULL;
END $$;


--
-- Name: pb_check_source_trigger(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_check_source_trigger() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_TABLE_NAME='request_records' THEN PERFORM pb_check_customer(NEW.id);
 ELSIF TG_TABLE_NAME='reservations' OR TG_TABLE_NAME='settlements' THEN PERFORM pb_check_customer(NEW.request_id);
 ELSIF TG_TABLE_NAME IN ('procurement_reservations','procurement_budget_events') THEN PERFORM pb_check_supplier(NEW.attempt_id);
 ELSIF NEW.subject_type='customer_request' THEN PERFORM pb_check_customer(NEW.subject_id);
 ELSE PERFORM pb_check_supplier(NEW.subject_id);END IF;RETURN NULL;
END $$;


--
-- Name: pb_check_supplier(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_check_supplier(subject text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE p procurement_reservations%ROWTYPE;c periodic_budget_controls%ROWTYPE;a periodic_budget_allocations%ROWTYPE;expected_state text;
BEGIN
 SELECT * INTO p FROM procurement_reservations WHERE attempt_id=subject;IF NOT FOUND THEN RETURN; END IF;
 SELECT * INTO c FROM periodic_budget_controls WHERE scope='procurement' AND account_id=p.account_id;IF NOT FOUND THEN RETURN;END IF;
 SELECT * INTO a FROM periodic_budget_allocations WHERE control_id=c.id AND subject_type='supplier_attempt' AND subject_id=subject;
 IF NOT FOUND THEN
  IF p.pb_control=c.id OR p.state IN ('prepared','sending','unknown','disputed') OR EXISTS
   (SELECT 1 FROM procurement_budget_events WHERE attempt_id=p.attempt_id AND pb_created_xid=pg_current_xact_id()) THEN
   RAISE EXCEPTION 'periodic supplier allocation missing' USING ERRCODE='23514'; END IF;RETURN;
 END IF;
 expected_state:=CASE p.state WHEN 'settled' THEN 'settled' WHEN 'released' THEN 'released' WHEN 'disputed' THEN 'disputed' ELSE 'held' END;
 IF a.revision<=0 OR a.last_event_id IS NULL OR (a.held_amount,a.spent_amount,a.source_held,a.source_spent,a.state,a.upper_amount)
  IS DISTINCT FROM (p.held_amount,greatest(p.spent_amount-a.baseline,0),p.held_amount,p.spent_amount,expected_state,p.upper_amount::bigint) THEN
  RAISE EXCEPTION 'periodic supplier projection/source mismatch' USING ERRCODE='23514'; END IF;
 IF (p.pb_control=c.id AND a.origin<>'new') OR (p.pb_control IS NULL AND a.origin='new') THEN
  RAISE EXCEPTION 'periodic supplier origin mismatch' USING ERRCODE='23514'; END IF;
END $$;


--
-- Name: pb_current_tuple(xid8, xid); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_current_tuple(stamp xid8, tuple_xid xid) RETURNS boolean
    LANGUAGE plpgsql
    
    AS $$
DECLARE full_xid numeric;
BEGIN
 IF stamp IS DISTINCT FROM pg_current_xact_id() OR tuple_xid IS NULL THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM pg_locks WHERE locktype='transactionid' AND transactionid=tuple_xid
   AND pid=pg_backend_pid() AND granted AND mode='ExclusiveLock') THEN RETURN true; END IF;
 full_xid:=floor(stamp::text::numeric/4294967296)*4294967296+tuple_xid::text::numeric;
 IF full_xid<stamp::text::numeric THEN full_xid:=full_xid+4294967296; END IF;
 IF full_xid<=stamp::text::numeric THEN RETURN false; END IF;
 BEGIN RETURN pg_xact_status(full_xid::text::xid8) IS NOT DISTINCT FROM 'in progress';
 EXCEPTION WHEN invalid_parameter_value THEN RETURN false; END;
END $$;


--
-- Name: pb_decimal(text, boolean); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_decimal(value text, allow_zero boolean) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    
    AS $_$
BEGIN
 IF value IS NULL OR value !~ '^(0|[1-9][0-9]{0,18})$' THEN RETURN false;END IF;
 RETURN value::numeric<=9223372036854775807 AND (allow_zero OR value<>'0');
END $_$;


--
-- Name: pb_end(text, timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_end(kind text, start_time timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT CASE kind WHEN 'utc_day' THEN (start_time AT TIME ZONE 'UTC'+interval '1 day') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN (start_time AT TIME ZONE 'UTC'+interval '1 month') AT TIME ZONE 'UTC' END
$$;


--
-- Name: pb_frame(text[]); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_frame(parts text[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE
    
    AS $$
DECLARE result bytea:=''::bytea;part text;
BEGIN FOREACH part IN ARRAY parts LOOP
 IF part IS NULL THEN RAISE EXCEPTION 'periodic fingerprint null field' USING ERRCODE='23514'; END IF;
 result:=result||int4send(octet_length(convert_to(part,'UTF8')))||convert_to(part,'UTF8');
 END LOOP;RETURN result;END $$;


--
-- Name: pb_gate(text, timestamp with time zone, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_gate(control text, at_time timestamp with time zone, extra bigint) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;w periodic_budget_windows%ROWTYPE;observed_window text;
BEGIN
 IF extra IS NULL OR extra<0 THEN RAISE EXCEPTION 'periodic invalid extra' USING ERRCODE='23514';END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control FOR NO KEY UPDATE;
 observed_window:=pb_observe(control,at_time);
 SELECT * INTO STRICT w FROM periodic_budget_windows WHERE id=observed_window AND control_id=c.id;
 IF c.enabled AND at_time>=c.effective_from AND w.spent_amount+c.held_amount+extra>c.limit_amount THEN
  RAISE EXCEPTION 'periodic budget exceeded' USING ERRCODE='23514',CONSTRAINT='pb_budget_'||c.scope;END IF;
END $$;


--
-- Name: pb_guard_allocation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_allocation() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE; r request_records%ROWTYPE; v reservations%ROWTYPE;
 p procurement_reservations%ROWTYPE; pe procurement_budget_events%ROWTYPE; e periodic_budget_events%ROWTYPE;
 tuple_xid xid; marker text; prior_h numeric;prior_s numeric;w periodic_budget_windows%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.control_id,NEW.subject_type,NEW.subject_id,NEW.origin,NEW.origin_source_id,NEW.window_id,NEW.admitted_at,
      NEW.upper_amount,NEW.baseline,NEW.created_xid,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.control_id,OLD.subject_type,OLD.subject_id,OLD.origin,OLD.origin_source_id,OLD.window_id,OLD.admitted_at,
      OLD.upper_amount,OLD.baseline,OLD.created_xid,OLD.created_at) THEN
   RAISE EXCEPTION 'periodic allocation identity immutable' USING ERRCODE='23514'; END IF;
  SELECT * INTO e FROM periodic_budget_events WHERE id=NEW.last_event_id AND control_id=OLD.control_id AND allocation_id=OLD.id;
  IF e.created_xid IS DISTINCT FROM pg_current_xact_id() OR e.allocation_before_revision IS DISTINCT FROM OLD.revision
   OR NEW.revision IS DISTINCT FROM OLD.revision+1 OR (NEW.held_amount,NEW.spent_amount,NEW.source_held,NEW.source_spent,NEW.state)
    IS DISTINCT FROM (e.after_held,e.after_spent,e.after_held,e.after_source_spent,e.after_state) THEN
   RAISE EXCEPTION 'periodic allocation update requires exact new event' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
 IF NEW.subject_type='customer_request' THEN
  SELECT * INTO STRICT r FROM request_records WHERE id=NEW.subject_id;
  SELECT * INTO STRICT v FROM reservations WHERE request_id=r.id AND tenant_id=r.tenant_id;
  IF NOT ((c.scope='tenant' AND c.tenant_id=r.tenant_id) OR (c.scope='key' AND c.tenant_id=r.tenant_id AND c.key_id=r.key_id)) THEN
   RAISE EXCEPTION 'periodic customer scope mismatch' USING ERRCODE='23514'; END IF;
  marker:=CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control ELSE r.pb_key_control END;
  NEW.upper_amount:=v.amount; NEW.baseline:=0;
  IF NEW.origin='new' THEN
   SELECT xmin INTO tuple_xid FROM request_records WHERE id=r.id;
   IF marker IS DISTINCT FROM c.id OR NOT pb_current_tuple(r.pb_created_xid,tuple_xid)
    OR NEW.origin_source_id IS DISTINCT FROM v.id OR v.status NOT IN ('reserved','pending_reconciliation') THEN
    RAISE EXCEPTION 'periodic normal customer requires same-tx source/control' USING ERRCODE='23514'; END IF;
   NEW.admitted_at:=r.pb_admitted_at;
  ELSIF NEW.origin='legacy_import' THEN
   IF marker IS NOT NULL OR v.status NOT IN ('reserved','pending_reconciliation') OR NEW.origin_source_id IS DISTINCT FROM r.id THEN
    RAISE EXCEPTION 'periodic invalid legacy customer' USING ERRCODE='23514'; END IF;
  ELSE RAISE EXCEPTION 'periodic customer catchup forbidden' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT * INTO STRICT p FROM procurement_reservations WHERE attempt_id=NEW.subject_id;
  IF c.scope IS DISTINCT FROM 'procurement' OR c.account_id IS DISTINCT FROM p.account_id THEN
   RAISE EXCEPTION 'periodic supplier scope mismatch' USING ERRCODE='23514'; END IF;
  marker:=p.pb_control;NEW.upper_amount:=p.upper_amount;NEW.baseline:=0;
  IF NEW.origin='new' THEN
   SELECT xmin INTO tuple_xid FROM procurement_reservations WHERE attempt_id=p.attempt_id;
   IF marker IS DISTINCT FROM c.id OR NOT pb_current_tuple(p.pb_created_xid,tuple_xid)
    OR p.state IS DISTINCT FROM 'prepared' OR p.held_amount IS DISTINCT FROM p.upper_amount::numeric
    OR p.spent_amount IS DISTINCT FROM 0::numeric OR p.anomaly IS DISTINCT FROM false THEN
    RAISE EXCEPTION 'periodic normal supplier requires same-tx source/control' USING ERRCODE='23514'; END IF;
   NEW.admitted_at:=p.pb_admitted_at;
  ELSIF NEW.origin='legacy_import' THEN
   IF marker IS NOT NULL OR p.state NOT IN ('prepared','sending','unknown','disputed') OR NEW.origin_source_id IS DISTINCT FROM p.attempt_id THEN
    RAISE EXCEPTION 'periodic invalid legacy supplier' USING ERRCODE='23514'; END IF;
   NEW.baseline:=p.spent_amount;
  ELSE
   SELECT * INTO STRICT pe FROM procurement_budget_events WHERE id=NEW.origin_source_id;
   SELECT xmin INTO tuple_xid FROM procurement_budget_events WHERE id=pe.id;
   SELECT coalesce(sum(held_delta),0),coalesce(sum(spent_delta),0) INTO prior_h,prior_s
    FROM procurement_budget_events WHERE attempt_id=p.attempt_id AND account_id=p.account_id AND id<>pe.id;
   IF marker IS NOT NULL OR pe.attempt_id IS DISTINCT FROM p.attempt_id OR pe.account_id IS DISTINCT FROM p.account_id
    OR NOT pb_current_tuple(pe.pb_created_xid,tuple_xid) OR prior_h IS DISTINCT FROM 0::numeric OR prior_s<0
    OR (prior_h+pe.held_delta,prior_s+pe.spent_delta) IS DISTINCT FROM (p.held_amount,p.spent_amount) THEN
    RAISE EXCEPTION 'periodic catchup requires exact new source event and zero prior held' USING ERRCODE='23514'; END IF;
   NEW.baseline:=prior_s;
  END IF;
 END IF;
 IF NEW.origin='legacy_import' THEN
  SELECT xmin INTO tuple_xid FROM periodic_budget_controls WHERE id=c.id;
  IF NOT pb_current_tuple(c.created_xid,tuple_xid) THEN
   RAISE EXCEPTION 'periodic legacy import only in control creation tx' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.origin='new' THEN
  SELECT * INTO STRICT w FROM periodic_budget_windows WHERE control_id=c.id AND id=NEW.window_id;
  IF NEW.admitted_at IS NULL OR NOT isfinite(NEW.admitted_at) OR NEW.admitted_at<w.start_at OR NEW.admitted_at>=w.end_at
    OR w.start_at<c.max_observed_window_start OR NEW.admitted_at>pb_now() THEN
   RAISE EXCEPTION 'periodic invalid original admission window' USING ERRCODE='23514'; END IF;
 ELSE NEW.window_id:=NULL;NEW.admitted_at:=NULL; END IF;
 NEW.created_xid:=pg_current_xact_id();NEW.created_at:=pb_now();NEW.source_held:=0;NEW.source_spent:=NEW.baseline;
 NEW.held_amount:=0;NEW.spent_amount:=0;NEW.state:='held';NEW.revision:=0;NEW.last_event_id:=NULL;
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic source missing' USING ERRCODE='23514';
END $$;


--
-- Name: pb_guard_configuration_pointer(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_configuration_pointer() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF (NEW.enabled,NEW.limit_amount) IS DISTINCT FROM (OLD.enabled,OLD.limit_amount) THEN
  IF NEW.configuration_operation_id IS NOT DISTINCT FROM OLD.configuration_operation_id THEN
   RAISE EXCEPTION 'periodic configuration requires new operation' USING ERRCODE='23514';END IF;
 ELSIF NEW.configuration_operation_id IS DISTINCT FROM OLD.configuration_operation_id THEN
  RAISE EXCEPTION 'periodic noop cannot replace configuration history' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;


--
-- Name: pb_guard_control(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_control() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE e periodic_budget_events%ROWTYPE;legacy_count integer;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.scope='tenant' THEN PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR NO KEY UPDATE;
  ELSIF NEW.scope='key' THEN
   PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
   PERFORM id FROM api_keys WHERE tenant_id=NEW.tenant_id AND id=NEW.key_id FOR UPDATE;
  ELSE PERFORM id FROM procurement_accounts WHERE id=NEW.account_id FOR NO KEY UPDATE; END IF;
  PERFORM pb_scope_sync(NEW.scope,coalesce(NEW.account_id,NEW.key_id,NEW.tenant_id),true);
  IF NEW.scope='procurement' THEN
   SELECT count(*) INTO legacy_count FROM (SELECT 1 FROM procurement_reservations WHERE account_id=NEW.account_id
    AND state IN ('prepared','sending','unknown','disputed') LIMIT 1001) candidates;
  ELSE
   SELECT count(*) INTO legacy_count FROM (SELECT 1 FROM reservations v JOIN request_records r ON r.id=v.request_id
    WHERE r.tenant_id=NEW.tenant_id AND (NEW.scope='tenant' OR r.key_id=NEW.key_id)
    AND v.status IN ('reserved','pending_reconciliation') LIMIT 1001) candidates;
  END IF;
  IF legacy_count>1000 THEN RAISE EXCEPTION 'periodic legacy import requires maintenance' USING ERRCODE='23514';END IF;
  NEW.created_xid:=pg_current_xact_id(); NEW.tracking_started_at:=pb_now();
  NEW.effective_from:=pb_end(NEW.period_kind,pb_start(NEW.period_kind,NEW.tracking_started_at));
  NEW.max_observed_window_start:=pb_start(NEW.period_kind,NEW.tracking_started_at);
  NEW.revision:=1; NEW.held_amount:=0;NEW.spent_amount:=0;NEW.event_seq:=0;NEW.last_event_id:=NULL;
 ELSE
  PERFORM pb_require_read_committed();
  IF (NEW.id,NEW.scope,NEW.tenant_id,NEW.key_id,NEW.account_id,NEW.period_kind,NEW.tracking_started_at,NEW.effective_from,NEW.created_xid)
   IS DISTINCT FROM (OLD.id,OLD.scope,OLD.tenant_id,OLD.key_id,OLD.account_id,OLD.period_kind,OLD.tracking_started_at,OLD.effective_from,OLD.created_xid) THEN
   RAISE EXCEPTION 'periodic control identity/calendar immutable' USING ERRCODE='23514'; END IF;
  IF (NEW.held_amount,NEW.spent_amount,NEW.event_seq,NEW.last_event_id)
   IS DISTINCT FROM (OLD.held_amount,OLD.spent_amount,OLD.event_seq,OLD.last_event_id) THEN
   SELECT * INTO e FROM periodic_budget_events WHERE id=NEW.last_event_id AND control_id=OLD.id;
   IF e.created_xid IS DISTINCT FROM pg_current_xact_id() OR e.seq IS DISTINCT FROM OLD.event_seq+1
    OR NEW.event_seq IS DISTINCT FROM e.seq OR NEW.held_amount IS DISTINCT FROM OLD.held_amount+e.held_delta
    OR NEW.spent_amount IS DISTINCT FROM OLD.spent_amount+e.spent_delta THEN
    RAISE EXCEPTION 'periodic control update requires new exact event' USING ERRCODE='23514'; END IF;
  END IF;
  IF NEW.max_observed_window_start IS DISTINCT FROM OLD.max_observed_window_start AND
   (NEW.max_observed_window_start<OLD.max_observed_window_start OR NEW.max_observed_window_start IS DISTINCT FROM pb_start(OLD.period_kind,NEW.max_observed_window_start)
   OR NEW.max_observed_window_start>pb_start(OLD.period_kind,pb_now())) THEN
   RAISE EXCEPTION 'periodic clock regression/invalid highwater' USING ERRCODE='23514'; END IF;
  NEW.revision:=OLD.revision+CASE WHEN (NEW.enabled,NEW.limit_amount) IS DISTINCT FROM (OLD.enabled,OLD.limit_amount) THEN 1 ELSE 0 END;
 END IF;RETURN NEW;
END $$;


--
-- Name: pb_guard_customer_reservation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_customer_reservation() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE r request_records%ROWTYPE;tuple_xid xid;source_stamp xid8;source_tenant text;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT pb_created_xid,xmin,tenant_id INTO source_stamp,tuple_xid,source_tenant FROM request_records WHERE id=NEW.request_id;
  IF source_tenant IS DISTINCT FROM NEW.tenant_id OR NOT pb_current_tuple(source_stamp,tuple_xid) THEN
   RAISE EXCEPTION 'new customer reservation requires same-tx request' USING ERRCODE='23514'; END IF;
  -- The parent INSERT already owns tenant/Key roots and scope shared locks.
  PERFORM pb_require_read_committed();RETURN NEW;
 END IF;
 SELECT * INTO STRICT r FROM request_records WHERE id=OLD.request_id;
 PERFORM pb_sync_unconfigured('tenant',r.tenant_id);
 PERFORM pb_sync_unconfigured('key',r.key_id);
 -- Scope synchronization is derived from this immutable parent/amount.
 -- Existing finish paths only change status, including pre-B sources.
 IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.request_id,NEW.amount,NEW.deadline,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.request_id,OLD.amount,OLD.deadline,OLD.created_at) THEN
  RAISE EXCEPTION 'periodic reservation identity immutable' USING ERRCODE='23514';END IF;
 IF EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=OLD.request_id) THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'periodic reservation delete forbidden' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
END $$;


--
-- Name: pb_guard_dispatch(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_dispatch() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE r request_records%ROWTYPE;control text;controls text[]:=ARRAY[]::text[];at_time timestamptz;
 current_binding text;frozen_binding text;procurement_state text;procurement_account text;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.delivery_state IS DISTINCT FROM 'prepared' THEN
   RAISE EXCEPTION 'periodic new attempt must be prepared' USING ERRCODE='23514';END IF;
  NEW.pb_dispatch_checked_at:=NULL;RETURN NEW;
 END IF;
 IF NEW.delivery_state='sending' AND OLD.delivery_state IS DISTINCT FROM 'sending' THEN
  PERFORM pb_require_read_committed();
  IF OLD.delivery_state IS DISTINCT FROM 'prepared' THEN
   RAISE EXCEPTION 'periodic dispatch requires prepared attempt' USING ERRCODE='23514';END IF;
  SELECT * INTO STRICT r FROM request_records WHERE id=NEW.request_id AND tenant_id=NEW.tenant_id;
  IF NOT EXISTS(SELECT 1 FROM reservations WHERE request_id=r.id AND tenant_id=r.tenant_id AND status IN ('reserved','pending_reconciliation')) THEN
   RAISE EXCEPTION 'periodic dispatch customer source not held' USING ERRCODE='23514';END IF;
  SELECT id INTO current_binding FROM credential_procurement_bindings WHERE credential_id=NEW.credential_id AND channel_id=NEW.channel_id;
  SELECT binding_id,state,account_id INTO frozen_binding,procurement_state,procurement_account FROM procurement_reservations WHERE attempt_id=NEW.id;
  IF current_binding IS DISTINCT FROM frozen_binding OR (frozen_binding IS NOT NULL AND procurement_state NOT IN ('prepared','sending')) THEN
   RAISE EXCEPTION 'periodic dispatch procurement source mismatch' USING ERRCODE='23514';END IF;
  PERFORM pb_sync_unconfigured('tenant',r.tenant_id);
  PERFORM pb_sync_unconfigured('key',r.key_id);
  IF procurement_account IS NOT NULL THEN PERFORM pb_sync_unconfigured('procurement',procurement_account); END IF;
  FOR control IN SELECT c.id FROM periodic_budget_controls c
   WHERE (c.scope='tenant' AND c.tenant_id=r.tenant_id) OR (c.scope='key' AND c.tenant_id=r.tenant_id AND c.key_id=r.key_id)
   ORDER BY c.id FOR NO KEY UPDATE OF c LOOP controls:=array_append(controls,control);END LOOP;
  FOR control IN SELECT c.id FROM periodic_budget_controls c JOIN procurement_reservations p ON p.account_id=c.account_id
   WHERE c.scope='procurement' AND p.attempt_id=NEW.id FOR NO KEY UPDATE OF c LOOP controls:=array_append(controls,control);END LOOP;
  PERFORM pb_check_customer(r.id);PERFORM pb_check_supplier(NEW.id);
  -- One authoritative decision time after every control wait. Integrity
  -- guards may independently check not-in-future, but cannot choose its period.
  at_time:=pb_now();
  FOREACH control IN ARRAY controls LOOP PERFORM pb_gate(control,at_time,0);END LOOP;
  NEW.pb_dispatch_checked_at:=at_time;
 ELSE
  IF NEW.delivery_state='prepared' AND OLD.delivery_state IS DISTINCT FROM 'prepared' THEN
   RAISE EXCEPTION 'periodic attempt cannot return to prepared' USING ERRCODE='23514';END IF;
  IF NEW.pb_dispatch_checked_at IS DISTINCT FROM OLD.pb_dispatch_checked_at THEN
   RAISE EXCEPTION 'periodic dispatch metadata immutable' USING ERRCODE='23514';END IF;
 END IF;RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic dispatch source missing' USING ERRCODE='23514';
END $$;


--
-- Name: pb_guard_event(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_event() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;a periodic_budget_allocations%ROWTYPE;v reservations%ROWTYPE;s settlements%ROWTYPE;
 p procurement_reservations%ROWTYPE;pe procurement_budget_events%ROWTYPE;w periodic_budget_windows%ROWTYPE;tuple_xid xid;
 target_h numeric; target_s numeric; target_state text;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
 SELECT * INTO STRICT a FROM periodic_budget_allocations WHERE id=NEW.allocation_id AND control_id=c.id FOR UPDATE;
 -- A final charge cannot stand in for the original R admission: its held
 -- delta may be zero even when the original reservation exceeds the cap.
 IF a.revision=0 AND ((a.origin='legacy_import' AND NEW.source_kind<>'legacy_import')
  OR (a.subject_type='customer_request' AND a.origin='new' AND NEW.source_kind<>'reservation')) THEN
  RAISE EXCEPTION 'periodic original reserve event required first' USING ERRCODE='23514'; END IF;
 IF NEW.source_kind='legacy_import' THEN
  IF a.origin IS DISTINCT FROM 'legacy_import' OR a.revision<>0 OR NEW.source_id IS DISTINCT FROM a.subject_id THEN
   RAISE EXCEPTION 'periodic invalid legacy import event' USING ERRCODE='23514'; END IF;
  SELECT xmin INTO tuple_xid FROM periodic_budget_controls WHERE id=c.id;
  IF NOT pb_current_tuple(c.created_xid,tuple_xid) THEN RAISE EXCEPTION 'periodic legacy import tx required' USING ERRCODE='23514'; END IF;
  IF a.subject_type='customer_request' THEN
   SELECT * INTO STRICT v FROM reservations WHERE request_id=a.subject_id;
   target_h:=v.amount;target_s:=0;target_state:='held';
  ELSE
   SELECT * INTO STRICT p FROM procurement_reservations WHERE attempt_id=a.subject_id;
   IF p.spent_amount IS DISTINCT FROM a.baseline THEN RAISE EXCEPTION 'periodic legacy baseline changed' USING ERRCODE='23514'; END IF;
   target_h:=p.held_amount;target_s:=p.spent_amount;target_state:=CASE WHEN p.state='disputed' THEN 'disputed' ELSE 'held' END;
  END IF;
 ELSIF NEW.source_kind='reservation' THEN
  SELECT * INTO STRICT v FROM reservations WHERE id=NEW.source_id AND request_id=a.subject_id;
  IF a.subject_type IS DISTINCT FROM 'customer_request' OR a.origin IS DISTINCT FROM 'new' OR a.revision<>0
    OR a.origin_source_id IS DISTINCT FROM v.id OR v.status NOT IN ('reserved','pending_reconciliation') THEN
   RAISE EXCEPTION 'periodic invalid reservation effect' USING ERRCODE='23514'; END IF;
  target_h:=v.amount;target_s:=0;target_state:='held';
 ELSIF NEW.source_kind='settlement' THEN
  SELECT * INTO STRICT s FROM settlements WHERE id=NEW.source_id AND request_id=a.subject_id;
  SELECT * INTO STRICT v FROM reservations WHERE request_id=a.subject_id;
  IF a.subject_type IS DISTINCT FROM 'customer_request' OR a.revision<1 OR a.state<>'held' OR s.status IS DISTINCT FROM v.status
   OR (a.held_amount,a.source_held,a.spent_amount,a.source_spent) IS DISTINCT FROM (v.amount::numeric,v.amount::numeric,0::numeric,0::numeric)
   OR NOT EXISTS(SELECT 1 FROM periodic_budget_events original WHERE original.control_id=c.id AND original.allocation_id=a.id
    AND original.allocation_before_revision=0
    AND original.source_kind=CASE a.origin WHEN 'new' THEN 'reservation' ELSE 'legacy_import' END
    AND original.source_id=CASE a.origin WHEN 'new' THEN v.id ELSE a.subject_id END
    AND (original.before_held,original.before_spent,original.after_held,original.after_spent,original.after_source_spent,original.after_state)
     IS NOT DISTINCT FROM (0::numeric,0::numeric,v.amount::numeric,0::numeric,0::numeric,'held'::text)) THEN
   RAISE EXCEPTION 'periodic invalid settlement effect' USING ERRCODE='23514'; END IF;
  target_h:=0;target_s:=s.charged_amount;target_state:=s.status;
 ELSE
  SELECT * INTO STRICT pe FROM procurement_budget_events WHERE id=NEW.source_id;
  SELECT xmin INTO tuple_xid FROM procurement_budget_events WHERE id=pe.id;
  SELECT * INTO STRICT p FROM procurement_reservations WHERE attempt_id=a.subject_id;
  IF a.subject_type IS DISTINCT FROM 'supplier_attempt' OR pe.attempt_id IS DISTINCT FROM a.subject_id OR pe.account_id IS DISTINCT FROM c.account_id
    OR NOT pb_current_tuple(pe.pb_created_xid,tuple_xid)
    OR (a.source_held+pe.held_delta,a.source_spent+pe.spent_delta) IS DISTINCT FROM (p.held_amount,p.spent_amount) THEN
   RAISE EXCEPTION 'periodic supplier effect not exact same-tx source' USING ERRCODE='23514'; END IF;
  IF a.revision=0 AND a.origin IN ('new','legacy_catchup') AND a.origin_source_id IS DISTINCT FROM pe.id THEN
   RAISE EXCEPTION 'periodic initial procurement effect mismatch' USING ERRCODE='23514'; END IF;
  IF a.revision=0 AND a.origin='new' AND
    ((pe.type,pe.event_key,pe.held_delta,pe.spent_delta,p.state,p.held_amount,p.spent_amount,p.anomaly)
     IS DISTINCT FROM ('reserved'::text,'reserve:'||p.attempt_id,p.upper_amount::numeric,0::numeric,
                       'prepared'::text,p.upper_amount::numeric,0::numeric,false)) THEN
   RAISE EXCEPTION 'periodic original supplier reserve event required first' USING ERRCODE='23514'; END IF;
  target_h:=p.held_amount;target_s:=p.spent_amount;
  target_state:=CASE p.state WHEN 'settled' THEN 'settled' WHEN 'released' THEN 'released' WHEN 'disputed' THEN 'disputed' ELSE 'held' END;
 END IF;
 NEW.seq:=c.event_seq+1;NEW.allocation_before_revision:=a.revision;NEW.window_before_revision:=NULL;
 NEW.before_held:=a.held_amount;NEW.before_spent:=a.spent_amount;NEW.after_held:=target_h;
 NEW.after_source_spent:=target_s;NEW.after_spent:=greatest(target_s-a.baseline,0);NEW.after_state:=target_state;
 NEW.held_delta:=NEW.after_held-a.held_amount;NEW.spent_delta:=NEW.after_spent-a.spent_amount;
 IF a.window_id IS NOT NULL THEN
  SELECT * INTO STRICT w FROM periodic_budget_windows WHERE id=a.window_id AND control_id=c.id;
  NEW.window_before_revision:=w.revision;
  IF a.revision=0 THEN
   IF w.start_at<c.max_observed_window_start OR pb_start(c.period_kind,pb_now())<c.max_observed_window_start THEN
    RAISE EXCEPTION 'periodic clock regression' USING ERRCODE='23514'; END IF;
   IF c.enabled AND a.admitted_at>=c.effective_from AND w.spent_amount+c.held_amount+NEW.held_delta>c.limit_amount THEN
    RAISE EXCEPTION 'periodic budget exceeded' USING ERRCODE='23514',CONSTRAINT='pb_budget_'||c.scope; END IF;
  END IF;
 END IF;
 NEW.created_xid:=pg_current_xact_id();NEW.created_at:=pb_now();RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic source missing' USING ERRCODE='23514';
END $$;


--
-- Name: pb_guard_operation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_operation() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;actor_role text;member_role text;owner text;expected bigint;canonical_target text;canonical_scope text;canonical_kind text;
BEGIN
 IF NOT pb_safe_input(NEW.safe_input) THEN RAISE EXCEPTION 'periodic unsafe command input' USING ERRCODE='23514'; END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.resource_id;
 IF c.scope='key' THEN
  SELECT m.role,k.owner_id INTO member_role,owner FROM memberships m JOIN users u ON u.id=m.user_id JOIN api_keys k ON k.tenant_id=m.tenant_id
   WHERE m.tenant_id=c.tenant_id AND m.user_id=NEW.actor AND k.id=c.key_id AND m.status='active' AND u.status='active' AND k.revoked_at IS NULL;
  IF member_role IS NULL OR member_role NOT IN ('owner','member') OR (member_role='member' AND owner<>NEW.actor) THEN
   RAISE EXCEPTION 'periodic command actor forbidden' USING ERRCODE='23514';END IF;
  IF NOT EXISTS(SELECT 1 FROM tenants WHERE id=c.tenant_id AND status='active') THEN RAISE EXCEPTION 'periodic tenant inactive' USING ERRCODE='23514';END IF;
  canonical_kind:='key_periodic_budget.configure';canonical_scope:=c.tenant_id;canonical_target:='/api/keys/'||c.key_id||'/periodic-budget';
 ELSE
  SELECT platform_role INTO actor_role FROM users WHERE id=NEW.actor AND status='active' FOR SHARE;
  IF actor_role IS DISTINCT FROM 'admin' THEN RAISE EXCEPTION 'periodic command actor forbidden' USING ERRCODE='23514';END IF;
  canonical_scope:='platform';
  IF c.scope='tenant' THEN canonical_kind:='tenant_periodic_budget.configure';canonical_target:='/api/platform/tenants/'||c.tenant_id||'/periodic-budget';
  ELSE canonical_kind:='procurement_periodic_budget.configure';canonical_target:='/api/platform/procurement-accounts/'||c.account_id||'/periodic-budget';END IF;
 END IF;
 IF (NEW.kind,NEW.scope,NEW.target) IS DISTINCT FROM (canonical_kind,canonical_scope,canonical_target) THEN
  RAISE EXCEPTION 'periodic command target/scope mismatch' USING ERRCODE='23514';END IF;
 expected:=(NEW.safe_input->>'expected_revision')::bigint;
 IF (c.period_kind,c.enabled,c.limit_amount) IS DISTINCT FROM
    (NEW.safe_input->>'period_kind',(NEW.safe_input->>'enabled')::boolean,(NEW.safe_input->>'limit_amount')::bigint) THEN
  RAISE EXCEPTION 'periodic command input/current configuration mismatch' USING ERRCODE='23514';END IF;
 IF c.configuration_operation_id=NEW.operation_id THEN
  IF expected<>c.revision-1 THEN RAISE EXCEPTION 'periodic command CAS mismatch' USING ERRCODE='23514';END IF;
 ELSE
  IF expected<>c.revision THEN RAISE EXCEPTION 'periodic noop CAS mismatch' USING ERRCODE='23514';END IF;
 END IF;
 NEW.revision:=c.revision;NEW.created_xid:=pg_current_xact_id();NEW.created_at:=pb_now();
 NEW.input_fingerprint:=encode(sha256(pb_frame(ARRAY[NEW.kind,NEW.actor,NEW.scope,NEW.target,
  NEW.safe_input->>'expected_revision',NEW.safe_input->>'period_kind',NEW.safe_input->>'enabled',NEW.safe_input->>'limit_amount'])),'hex');
 NEW.result_snapshot:=pb_view_at(c.id,NEW.created_at);RETURN NEW;
EXCEPTION WHEN no_data_found THEN RAISE EXCEPTION 'periodic operation resource missing' USING ERRCODE='23514';
END $$;


--
-- Name: pb_guard_version(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_version() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;o periodic_budget_operations%ROWTYPE;tuple_xid xid;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id;
 SELECT * INTO STRICT o FROM periodic_budget_operations WHERE operation_id=NEW.operation_id;
 SELECT xmin INTO tuple_xid FROM periodic_budget_operations WHERE operation_id=o.operation_id;
 IF NOT pb_current_tuple(o.created_xid,tuple_xid) OR c.configuration_operation_id IS DISTINCT FROM o.operation_id
  OR (NEW.revision,NEW.period_kind,NEW.enabled,NEW.limit_amount,NEW.actor)
   IS DISTINCT FROM (c.revision,c.period_kind,c.enabled,c.limit_amount,o.actor) THEN
  RAISE EXCEPTION 'periodic version requires exact current-tx operation' USING ERRCODE='23514';END IF;
 NEW.created_xid:=pg_current_xact_id();NEW.created_at:=o.created_at;RETURN NEW;
END $$;


--
-- Name: pb_guard_window(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_guard_window() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;e periodic_budget_events%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
  IF NEW.start_at IS DISTINCT FROM pb_start(c.period_kind,NEW.start_at) OR NEW.end_at IS DISTINCT FROM pb_end(c.period_kind,NEW.start_at)
   OR NEW.start_at<c.max_observed_window_start OR NEW.start_at>pb_start(c.period_kind,pb_now()) THEN
   RAISE EXCEPTION 'periodic invalid/reopened window' USING ERRCODE='23514'; END IF;
  NEW.held_amount:=0;NEW.spent_amount:=0;NEW.revision:=0;NEW.last_event_id:=NULL;
 ELSE
  IF (NEW.id,NEW.control_id,NEW.start_at,NEW.end_at) IS DISTINCT FROM (OLD.id,OLD.control_id,OLD.start_at,OLD.end_at) THEN
   RAISE EXCEPTION 'periodic window identity immutable' USING ERRCODE='23514'; END IF;
  SELECT * INTO e FROM periodic_budget_events WHERE id=NEW.last_event_id AND control_id=OLD.control_id;
  IF e.created_xid IS DISTINCT FROM pg_current_xact_id() OR e.window_before_revision IS DISTINCT FROM OLD.revision
   OR NOT EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE id=e.allocation_id AND window_id=OLD.id)
   OR NEW.revision IS DISTINCT FROM OLD.revision+1 OR NEW.held_amount IS DISTINCT FROM OLD.held_amount+e.held_delta
   OR NEW.spent_amount IS DISTINCT FROM OLD.spent_amount+e.spent_delta THEN
   RAISE EXCEPTION 'periodic window update requires new exact event' USING ERRCODE='23514'; END IF;
 END IF;RETURN NEW;
END $$;


--
-- Name: pb_now(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_now() RETURNS timestamp with time zone
    LANGUAGE sql
    
    AS $$ SELECT pg_catalog.clock_timestamp() $$;


--
-- Name: pb_observe(text, timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_observe(control text, at_time timestamp with time zone) RETURNS text
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;start_time timestamptz;observed_window text;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control FOR NO KEY UPDATE;
 start_time:=pb_start(c.period_kind,at_time);
 IF start_time<c.max_observed_window_start OR at_time>pb_now() THEN RAISE EXCEPTION 'periodic clock regression' USING ERRCODE='23514';END IF;
 UPDATE periodic_budget_controls SET max_observed_window_start=start_time WHERE id=c.id;
 SELECT id INTO observed_window FROM periodic_budget_windows WHERE control_id=c.id AND start_at=start_time;
 IF observed_window IS NULL THEN observed_window:=gen_random_uuid()::text;
  INSERT INTO periodic_budget_windows(id,control_id,start_at,end_at) VALUES(observed_window,c.id,start_time,pb_end(c.period_kind,start_time));END IF;
 RETURN observed_window;
END $$;


--
-- Name: pb_record_operation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_record_operation() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;changed boolean;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.resource_id;
 changed:=c.configuration_operation_id=NEW.operation_id;
 IF changed THEN
  INSERT INTO periodic_budget_configuration_versions(control_id,revision,operation_id,period_kind,enabled,limit_amount,actor)
  VALUES(c.id,c.revision,NEW.operation_id,c.period_kind,c.enabled,c.limit_amount,NEW.actor);
 END IF;
 INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff,created_at)
 VALUES(NEW.actor,CASE WHEN c.scope='key' THEN c.tenant_id ELSE NULL END,'periodic_budget.configured',NEW.operation_id,
  jsonb_build_object('control_id',c.id,'revision',c.revision::text,'scope',c.scope,'scope_id',coalesce(c.account_id,c.key_id,c.tenant_id),'changed',changed),NEW.created_at);
 RETURN NULL;
END $$;


--
-- Name: pb_require_read_committed(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_require_read_committed() RETURNS void
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'periodic source requires read committed' USING ERRCODE='23514'; END IF;
END $$;


--
-- Name: pb_safe_input(jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_safe_input(j jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT (jsonb_typeof(j)='object' AND j ?& ARRAY['expected_revision','period_kind','enabled','limit_amount']
 AND NOT EXISTS(SELECT 1 FROM jsonb_object_keys(j) k WHERE k<>ALL(ARRAY['expected_revision','period_kind','enabled','limit_amount']))
 AND jsonb_typeof(j->'expected_revision')='string' AND pb_decimal(j->>'expected_revision',true)
 AND jsonb_typeof(j->'limit_amount')='string' AND pb_decimal(j->>'limit_amount',true)
 AND jsonb_typeof(j->'period_kind')='string' AND j->>'period_kind' IN ('utc_day','utc_month')
 AND jsonb_typeof(j->'enabled')='boolean') IS TRUE
$$;


--
-- Name: pb_scope_sync(text, text, boolean); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_scope_sync(scope_name text, target_id text, exclusive_lock boolean) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
DECLARE lock_key integer;
BEGIN
 PERFORM pb_require_read_committed();
 IF scope_name NOT IN ('tenant','key','procurement') OR NOT pb_uuid(target_id) OR exclusive_lock IS NULL THEN
  RAISE EXCEPTION 'periodic invalid synchronization scope' USING ERRCODE='23514'; END IF;
 lock_key:=hashtext(current_schema()||':'||scope_name||':'||target_id);
 IF exclusive_lock THEN PERFORM pg_advisory_xact_lock(23023023,lock_key);
 ELSE PERFORM pg_advisory_xact_lock_shared(23023023,lock_key); END IF;
END $$;


--
-- Name: pb_stamp_attempt(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_stamp_attempt() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.pb_created_xid:=pg_current_xact_id();
 ELSIF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=OLD.id) THEN
   RAISE EXCEPTION 'periodic source cannot be deleted' USING ERRCODE='23514'; END IF; RETURN OLD;
 ELSE
  IF NEW.pb_created_xid IS DISTINCT FROM OLD.pb_created_xid OR
   ((NEW.id,NEW.tenant_id,NEW.request_id) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.request_id)
    AND EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=OLD.id)) THEN
   RAISE EXCEPTION 'periodic source identity immutable' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;


--
-- Name: pb_stamp_procurement(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_stamp_procurement() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE source_stamp xid8; tuple_xid xid;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state IS DISTINCT FROM 'prepared' OR NEW.held_amount IS DISTINCT FROM NEW.upper_amount::numeric
    OR NEW.spent_amount IS DISTINCT FROM 0::numeric OR NEW.anomaly IS DISTINCT FROM false THEN
   RAISE EXCEPTION 'periodic new procurement requires original upper reservation' USING ERRCODE='23514'; END IF;
  SELECT pb_created_xid,xmin INTO source_stamp,tuple_xid FROM upstream_attempts WHERE id=NEW.attempt_id;
  IF NOT pb_current_tuple(source_stamp,tuple_xid) THEN RAISE EXCEPTION 'new procurement requires same-tx attempt' USING ERRCODE='23514'; END IF;
  PERFORM id FROM procurement_accounts WHERE id=NEW.account_id FOR NO KEY UPDATE;
  PERFORM pb_sync_unconfigured('procurement',NEW.account_id);
  NEW.pb_created_xid:=pg_current_xact_id();
  SELECT id INTO NEW.pb_control FROM periodic_budget_controls WHERE scope='procurement' AND account_id=NEW.account_id;
  NEW.pb_admitted_at:=pb_now();
 ELSE
  PERFORM pb_sync_unconfigured('procurement',OLD.account_id);
  IF (NEW.pb_created_xid,NEW.pb_control,NEW.pb_admitted_at) IS DISTINCT FROM (OLD.pb_created_xid,OLD.pb_control,OLD.pb_admitted_at) THEN
   RAISE EXCEPTION 'periodic procurement creation metadata immutable' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;


--
-- Name: pb_stamp_procurement_event(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_stamp_procurement_event() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN NEW.pb_created_xid:=pg_current_xact_id(); RETURN NEW; END $$;


--
-- Name: pb_stamp_request(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_stamp_request() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM id FROM tenants WHERE id=NEW.tenant_id FOR SHARE;
  PERFORM id FROM api_keys WHERE tenant_id=NEW.tenant_id AND id=NEW.key_id FOR UPDATE;
  PERFORM pb_sync_unconfigured('tenant',NEW.tenant_id);
  PERFORM pb_sync_unconfigured('key',NEW.key_id);
  NEW.pb_created_xid:=pg_current_xact_id();
  SELECT id INTO NEW.pb_tenant_control FROM periodic_budget_controls WHERE scope='tenant' AND tenant_id=NEW.tenant_id;
  SELECT id INTO NEW.pb_key_control FROM periodic_budget_controls WHERE scope='key' AND key_id=NEW.key_id AND tenant_id=NEW.tenant_id;
  NEW.pb_admitted_at:=pb_now();
 ELSIF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=OLD.id) THEN
   RAISE EXCEPTION 'periodic source cannot be deleted' USING ERRCODE='23514'; END IF; RETURN OLD;
 ELSE
  PERFORM pb_require_read_committed();
  IF (NEW.pb_created_xid,NEW.pb_tenant_control,NEW.pb_key_control,NEW.pb_admitted_at)
   IS DISTINCT FROM (OLD.pb_created_xid,OLD.pb_tenant_control,OLD.pb_key_control,OLD.pb_admitted_at) THEN
   RAISE EXCEPTION 'periodic source creation metadata immutable' USING ERRCODE='23514'; END IF;
  IF (NEW.id,NEW.tenant_id,NEW.key_id) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.key_id)
   AND EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=OLD.id) THEN
   RAISE EXCEPTION 'periodic source identity immutable' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;


--
-- Name: pb_start(text, timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_start(kind text, at_time timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE sql IMMUTABLE
    
    AS $$
 SELECT CASE kind WHEN 'utc_day' THEN date_trunc('day',at_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN date_trunc('month',at_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' END
$$;


--
-- Name: pb_sync_unconfigured(text, text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_sync_unconfigured(scope_name text, target_id text) RETURNS void
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 PERFORM pb_require_read_committed();
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_controls WHERE scope=scope_name AND
   CASE scope_name WHEN 'tenant' THEN tenant_id WHEN 'key' THEN key_id WHEN 'procurement' THEN account_id END=target_id) THEN
  PERFORM pb_scope_sync(scope_name,target_id,false);
 END IF;
END $$;


--
-- Name: pb_uuid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_uuid(v text) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    
    AS $_$
 SELECT coalesce(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v<>'00000000-0000-0000-0000-000000000000',false)
$_$;


--
-- Name: pb_view_at(text, timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pb_view_at(control text, at_time timestamp with time zone) RETURNS jsonb
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE c periodic_budget_controls%ROWTYPE;w periodic_budget_windows%ROWTYPE;window_view jsonb;current_state text;
BEGIN
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control;
 IF pb_start(c.period_kind,at_time)<c.max_observed_window_start THEN RAISE EXCEPTION 'periodic clock regression' USING ERRCODE='23514';END IF;
 SELECT * INTO w FROM periodic_budget_windows WHERE control_id=c.id AND start_at=pb_start(c.period_kind,at_time);
 window_view:='null'::jsonb;
 IF w.id IS NOT NULL THEN window_view:=jsonb_build_object('id',w.id,'start_at',pricing_time(w.start_at),'end_at',pricing_time(w.end_at),
  'spent_amount',w.spent_amount::text,'held_amount',w.held_amount::text);END IF;
 IF c.held_amount<coalesce(w.held_amount,0) THEN RAISE EXCEPTION 'periodic negative carry' USING ERRCODE='23514'; END IF;
 current_state:=CASE WHEN NOT c.enabled THEN 'disabled' WHEN at_time<c.effective_from THEN 'scheduled' ELSE 'active' END;
 RETURN jsonb_build_object('scope',c.scope,'scope_id',coalesce(c.account_id,c.key_id,c.tenant_id),'revision',c.revision::text,
  'period_kind',c.period_kind,'enabled',c.enabled,'limit_amount',c.limit_amount::text,'state',current_state,
  'effective_from',pricing_time(c.effective_from),'tracking_started_at',pricing_time(c.tracking_started_at),
  'current_window',window_view,'carry_held',(c.held_amount-coalesce(w.held_amount,0))::text,'as_of',pricing_time(at_time));
END $$;


--
-- Name: preserve_request_client_ip(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION preserve_request_client_ip() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.client_ip IS DISTINCT FROM OLD.client_ip THEN RAISE EXCEPTION 'request source IP is immutable'; END IF;
 RETURN NEW;
END $$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: tenants; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE tenants (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    name text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    allowed_regions text[] DEFAULT ARRAY['global'::text] NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    price_group_id text,
    pricing_revision bigint DEFAULT 1 NOT NULL,
    pricing_operation_id text,
    CONSTRAINT tenants_name_check CHECK (((length(name) >= 1) AND (length(name) <= 200))),
    CONSTRAINT tenants_pricing_revision_check CHECK ((pricing_revision > 0)),
    CONSTRAINT tenants_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text])))
);


--
-- Name: pricing_assignment_result(tenants); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_assignment_result(t tenants) RETURNS jsonb
    LANGUAGE sql IMMUTABLE STRICT
    AS $$
 SELECT jsonb_build_object('tenant_id',t.id,'price_group_id',t.price_group_id,
  'pricing_revision',t.pricing_revision::text)
$$;


--
-- Name: cost_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE cost_versions (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    channel_id text NOT NULL,
    upstream_model text NOT NULL,
    basis text DEFAULT 'unknown'::text NOT NULL,
    effective_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    schema_id text NOT NULL COLLATE pg_catalog."C",
    schema_digest bytea NOT NULL,
    variant_key text DEFAULT 'default'::text NOT NULL COLLATE pg_catalog."C",
    currency text DEFAULT 'CNY'::text NOT NULL,
    rates_canonical bytea,
    rates_digest bytea,
    pricing_operation_id text NOT NULL,
    created_by text NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id(),
    CONSTRAINT cost_versions_basis_check CHECK ((basis = ANY (ARRAY['contract'::text, 'invoice'::text, 'unknown'::text]))),
    CONSTRAINT cost_versions_check CHECK (((currency = 'CNY'::text) AND (variant_key = 'default'::text)))
);


--
-- Name: pricing_cost_result(cost_versions, bigint); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_cost_result(c cost_versions, expected_revision bigint) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $_$
 SELECT jsonb_build_object('id',($1).id,'channel_id',($1).channel_id,'upstream_model',($1).upstream_model,'schema_id',($1).schema_id,'schema_digest',encode(($1).schema_digest,'hex'),'variant_key',($1).variant_key,'currency',($1).currency,'basis',($1).basis,'rates',CASE WHEN ($1).rates_canonical IS NULL THEN 'null'::jsonb ELSE convert_from(($1).rates_canonical,'UTF8')::jsonb END,'effective_at',pricing_time(($1).effective_at),'created_at',pricing_time(($1).created_at),'expected_channel_revision',expected_revision::text,'channel_revision',(expected_revision+1)::text)
$_$;


--
-- Name: pricing_fingerprint(text, text, text, text, jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_fingerprint(k text, a text, s text, t text, j jsonb) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT
    
    AS $$
 SELECT encode(sha256(pricing_frame(ARRAY[k,a,s,t] || CASE k
  WHEN 'price_group.create' THEN ARRAY[j->>'label']
  WHEN 'price_group.update' THEN ARRAY[j->>'expected_revision',j->>'label',j->>'state']
  WHEN 'group_price.publish' THEN ARRAY[j->>'model_id',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',convert_from(meter_text_rates_canonical(j->'rates','retail'),'UTF8'),j->>'effective_at']
  WHEN 'default_price.publish' THEN ARRAY[j->>'model_id',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',convert_from(meter_text_rates_canonical(j->'rates','retail'),'UTF8'),j->>'effective_at']
  WHEN 'cost_price.publish' THEN ARRAY[j->>'channel_id',j->>'upstream_model',j->>'schema_id',j->>'schema_digest',j->>'variant_key',j->>'currency',j->>'basis',CASE WHEN j->'rates'='null'::jsonb THEN NULL ELSE convert_from(meter_text_rates_canonical(j->'rates','supplier'),'UTF8') END,j->>'effective_at',j->>'expected_channel_revision']
  WHEN 'tenant_pricing.assign' THEN ARRAY[j->>'expected_revision',j->>'price_group_id'] ELSE ARRAY[]::text[] END)),'hex')
$$;


--
-- Name: pricing_frame(text[]); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_frame(parts text[]) RETURNS bytea
    LANGUAGE plpgsql IMMUTABLE STRICT
    AS $$
DECLARE v TEXT; b BYTEA; raw BYTEA;
BEGIN
 b:=convert_to('moni-aigate/pricing-command/v1','UTF8')||decode('00','hex')
    ||int4send(cardinality(parts));
 FOREACH v IN ARRAY parts LOOP
  IF v IS NULL THEN b:=b||int4send(-1);
  ELSE raw:=convert_to(v,'UTF8'); b:=b||int4send(octet_length(raw))||raw;
  END IF;
 END LOOP;
 RETURN b;
END $$;


--
-- Name: price_groups; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE price_groups (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    label text NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    last_operation_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT price_groups_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT price_groups_label_check CHECK ((((octet_length(label) >= 1) AND (octet_length(label) <= 100)) AND (label = btrim(label)) AND (label !~ '[[:cntrl:]]'::text))),
    CONSTRAINT price_groups_revision_check CHECK ((revision > 0)),
    CONSTRAINT price_groups_state_check CHECK ((state = ANY (ARRAY['active'::text, 'retired'::text])))
);


--
-- Name: pricing_group_result(price_groups); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_group_result(g price_groups) RETURNS jsonb
    LANGUAGE sql IMMUTABLE STRICT
    AS $$
 SELECT jsonb_build_object('id',g.id,'label',g.label,'state',g.state,
  'revision',g.revision::text,'created_at',pricing_time(g.created_at))
$$;


--
-- Name: pricing_json_time(jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_json_time(j jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
DECLARE v TEXT; t TIMESTAMPTZ;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'string' THEN RETURN false; END IF;
 v:=j#>>'{}';
 IF v !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.][0-9]{6}Z$' THEN
  RETURN false;
 END IF;
 BEGIN t:=v::timestamptz;
 EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow THEN RETURN false;
 END;
 RETURN isfinite(t) AND pricing_time(t)=v;
END $_$;


--
-- Name: pricing_json_uint(jsonb, boolean); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_json_uint(j jsonb, positive boolean) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
DECLARE v TEXT;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'string' THEN RETURN false; END IF;
 v:=j#>>'{}';
 IF positive THEN
  IF v !~ '^[1-9][0-9]*$' THEN RETURN false; END IF;
 ELSE
  IF v !~ '^(0|[1-9][0-9]*)$' THEN RETURN false; END IF;
 END IF;
 IF octet_length(v)>19 THEN RETURN false; END IF;
 RETURN v::numeric<=9223372036854775807::numeric;
END $_$;


--
-- Name: pricing_json_uuid(jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_json_uuid(j jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    AS $_$
 SELECT coalesce(jsonb_typeof(j)='string'
  AND (j#>>'{}') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND (j#>>'{}')<>'00000000-0000-0000-0000-000000000000',false)
$_$;


--
-- Name: price_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE price_versions (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    model_id text NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    effective_at timestamp with time zone NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    price_group_id text,
    pricing_operation_id text NOT NULL,
    schema_id text NOT NULL COLLATE pg_catalog."C",
    schema_digest bytea NOT NULL,
    variant_key text DEFAULT 'default'::text NOT NULL COLLATE pg_catalog."C",
    rates_canonical bytea NOT NULL,
    rates_digest bytea NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    CONSTRAINT price_versions_check CHECK (((variant_key = 'default'::text) AND (octet_length(schema_digest) = 32) AND (octet_length(rates_digest) = 32) AND ((octet_length(rates_canonical) >= 1) AND (octet_length(rates_canonical) <= 8192)))),
    CONSTRAINT price_versions_currency_check CHECK ((currency = 'CNY'::text))
);


--
-- Name: pricing_price_result(price_versions); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_price_result(p price_versions) RETURNS jsonb
    LANGUAGE sql STABLE
    
    AS $$
 SELECT jsonb_build_object('id',p.id,'model_id',p.model_id,'price_group_id',p.price_group_id,
  'currency',p.currency,'schema_id',p.schema_id,'schema_digest',encode(p.schema_digest,'hex'),
  'variant_key',p.variant_key,'rates',convert_from(p.rates_canonical,'UTF8')::jsonb,
  'effective_at',pricing_time(p.effective_at),'created_at',pricing_time(p.created_at))
$$;


--
-- Name: pricing_time(timestamp with time zone); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION pricing_time(t timestamp with time zone) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT
    AS $$
 SELECT to_char(t AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
$$;


--
-- Name: procurement_account_configuration_revision(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION procurement_account_configuration_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.revision := 1;
 ELSE
  IF NEW.id<>OLD.id OR NEW.created_at<>OLD.created_at THEN RAISE EXCEPTION 'procurement account identity is immutable'; END IF;
  IF (NEW.label,NEW.provider,NEW.state,NEW.limit_amount) IS DISTINCT FROM (OLD.label,OLD.provider,OLD.state,OLD.limit_amount) THEN NEW.revision := OLD.revision+1;
  ELSE NEW.revision := OLD.revision; END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: protect_route_decisions(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION protect_route_decisions() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.route_decisions IS DISTINCT FROM OLD.route_decisions THEN
  RAISE EXCEPTION 'route decision snapshots are immutable';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: realtime_event_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION realtime_event_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE session_state text; turn_state text;
BEGIN
 IF NEW.payload_digest IS DISTINCT FROM sha256(NEW.canonical) THEN RAISE EXCEPTION 'realtime event digest mismatch'; END IF;
 SELECT state INTO session_state FROM realtime_sessions WHERE tenant_id=NEW.tenant_id AND id=NEW.session_id;
 IF NEW.type IN ('heartbeat','status') AND NEW.turn_id IS NOT NULL THEN RAISE EXCEPTION 'realtime control event cannot bind turn'; END IF;
 IF NEW.type IN ('input.text','input.audio','response.create','response.cancel') AND NEW.turn_id IS NULL THEN RAISE EXCEPTION 'realtime event requires turn'; END IF;
 IF NEW.turn_id IS NOT NULL THEN SELECT state INTO turn_state FROM realtime_turns WHERE tenant_id=NEW.tenant_id AND id=NEW.turn_id AND session_id=NEW.session_id; IF turn_state IS NULL OR turn_state IN ('settled','released') THEN RAISE EXCEPTION 'realtime event turn is terminal'; END IF; END IF;
 IF session_state IS DISTINCT FROM 'open' AND NEW.type NOT IN ('heartbeat','status') THEN RAISE EXCEPTION 'realtime session is not open'; END IF;
 RETURN NEW;
END $$;


--
-- Name: realtime_session_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION realtime_session_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE profile_model text; route_model text;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.actor_id,NEW.api_key_id,NEW.model_id,NEW.profile_version,NEW.route_id,NEW.origin,NEW.expires_at,NEW.max_turns,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.actor_id,OLD.api_key_id,OLD.model_id,OLD.profile_version,OLD.route_id,OLD.origin,OLD.expires_at,OLD.max_turns,OLD.created_at) THEN RAISE EXCEPTION 'realtime session identity is immutable'; END IF;
  IF OLD.provider_session_id IS NOT NULL AND NEW.provider_session_id IS DISTINCT FROM OLD.provider_session_id THEN RAISE EXCEPTION 'realtime provider session identity is immutable'; END IF;
  IF NOT ((OLD.state='opening' AND NEW.state='open') OR (OLD.state='open' AND NEW.state='draining') OR (OLD.state='draining' AND NEW.state='closed') OR NEW.state=OLD.state) THEN RAISE EXCEPTION 'invalid realtime session state transition'; END IF;
  RETURN NEW;
 END IF;
 SELECT model_id INTO profile_model FROM model_capability_versions WHERE id=NEW.profile_version;
 SELECT model_id INTO route_model FROM route_policy_versions WHERE id=NEW.route_id;
 IF profile_model IS DISTINCT FROM NEW.model_id OR route_model IS DISTINCT FROM NEW.model_id THEN RAISE EXCEPTION 'realtime session binding mismatch'; END IF;
 RETURN NEW;
END $$;


--
-- Name: realtime_turn_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION realtime_turn_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE session_state text; session_tenant text; reserved_request text; attempt_request text; reserved_media boolean := false; attempt_media boolean := false;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.tenant_id,NEW.id,NEW.session_id,NEW.ordinal,NEW.request_id,NEW.reservation_id,NEW.attempt_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.session_id,OLD.ordinal,OLD.request_id,OLD.reservation_id,OLD.attempt_id,OLD.created_at) THEN RAISE EXCEPTION 'realtime turn identity is immutable'; END IF;
  IF OLD.provider_handle IS NOT NULL AND NEW.provider_handle IS DISTINCT FROM OLD.provider_handle THEN RAISE EXCEPTION 'realtime provider handle is immutable'; END IF;
  IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='reserved' AND NEW.state IN ('sending','released')) OR (OLD.state='sending' AND NEW.state IN ('receiving','pending')) OR (OLD.state='receiving' AND NEW.state IN ('finalizing','pending')) OR (OLD.state='finalizing' AND NEW.state IN ('settled','pending')) OR (OLD.state='pending' AND NEW.state IN ('settled','released'))) THEN RAISE EXCEPTION 'invalid realtime turn state transition'; END IF;
  IF OLD.response_reference IS NOT NULL AND NEW.response_reference IS DISTINCT FROM OLD.response_reference THEN RAISE EXCEPTION 'realtime response identity is immutable'; END IF;
  RETURN NEW;
 END IF;
 SELECT state,tenant_id INTO session_state,session_tenant FROM realtime_sessions WHERE tenant_id=NEW.tenant_id AND id=NEW.session_id;
 SELECT request_id INTO reserved_request FROM reservations WHERE tenant_id=NEW.tenant_id AND id=NEW.reservation_id;
 IF NOT FOUND THEN
  SELECT q.id INTO reserved_request FROM media_reservations r JOIN media_requests q ON q.tenant_id=r.tenant_id AND q.id=r.request_id
   WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.reservation_id AND q.id=NEW.request_id AND q.operation='realtime_turn';
  reserved_media := FOUND;
 END IF;
 SELECT request_id INTO attempt_request FROM upstream_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
 IF NOT FOUND THEN
  SELECT request_id INTO attempt_request FROM media_attempts WHERE tenant_id=NEW.tenant_id AND id=NEW.attempt_id;
  attempt_media := FOUND;
 END IF;
 IF session_state IS DISTINCT FROM 'open' OR session_tenant IS NULL OR reserved_request IS DISTINCT FROM NEW.request_id OR attempt_request IS DISTINCT FROM NEW.request_id OR reserved_media IS DISTINCT FROM attempt_media THEN RAISE EXCEPTION 'realtime turn binding mismatch'; END IF;
 RETURN NEW;
END $$;


--
-- Name: realtime_turn_inflight_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION realtime_turn_inflight_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF (SELECT count(*) FROM realtime_turns WHERE tenant_id=NEW.tenant_id AND session_id=NEW.session_id AND state NOT IN ('settled','released'))>1 THEN RAISE EXCEPTION 'realtime session has multiple inflight turns'; END IF;
 RETURN NULL;
END $$;


--
-- Name: reject_history_mutation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION reject_history_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN RAISE EXCEPTION 'immutable history'; END $$;


--
-- Name: require_balanced_journal(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION require_balanced_journal() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE line_count BIGINT; total NUMERIC;
BEGIN
 SELECT count(*),coalesce(sum(signed_delta::numeric),0) INTO line_count,total FROM journal_lines WHERE journal_id=NEW.id;
 IF line_count<2 OR total<>0 THEN RAISE EXCEPTION 'journal must have balanced entries'; END IF;
 RETURN NULL;
END $$;


--
-- Name: require_credential_pool_creation_transaction(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION require_credential_pool_creation_transaction() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE creation_xid xid8; creation_tuple_xid xid; tuple_xid NUMERIC;
BEGIN
 SELECT credential_pool_creation_xid,xmin INTO creation_xid,creation_tuple_xid FROM channel_versions WHERE id=NEW.channel_version_id;
 IF creation_xid IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'credential pool membership must be created with its channel version';
 END IF;
 -- A matching persisted stamp alone is insufficient after a cluster restore.
 IF NOT EXISTS (
  SELECT 1 FROM pg_locks WHERE locktype='transactionid'
   AND transactionid=creation_tuple_xid AND pid=pg_backend_pid()
   AND granted AND mode='ExclusiveLock'
 ) THEN
  -- RELEASE SAVEPOINT releases the child's xid lock. A visible uncommitted
  -- child tuple still belongs to this transaction under PostgreSQL MVCC.
  -- A child is assigned after its parent; reconstruct its full xid from the
  -- parent epoch, including a child assigned immediately after xid wrap.
  tuple_xid := floor(creation_xid::text::NUMERIC/4294967296)*4294967296 + creation_tuple_xid::text::NUMERIC;
  IF tuple_xid<creation_xid::text::NUMERIC THEN tuple_xid := tuple_xid+4294967296; END IF;
  IF tuple_xid<=creation_xid::text::NUMERIC THEN
   RAISE EXCEPTION 'credential pool membership must be created with its channel version';
  END IF;
  BEGIN
   IF pg_xact_status(tuple_xid::text::xid8) IS DISTINCT FROM 'in progress' THEN
    RAISE EXCEPTION 'credential pool membership must be created with its channel version';
   END IF;
  EXCEPTION WHEN invalid_parameter_value THEN
   RAISE EXCEPTION 'credential pool membership must be created with its channel version';
  END;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: require_price_group_operation(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION require_price_group_operation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE op pricing_operations%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.label,NEW.state) IS NOT DISTINCT FROM ROW(OLD.label,OLD.state) THEN
  RETURN NULL;
 END IF;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.last_operation_id;
 IF op.created_txid<>txid_current() OR op.resource_id<>NEW.id OR op.revision<>NEW.revision
    OR op.kind<>(CASE WHEN TG_OP='INSERT' THEN 'price_group.create' ELSE 'price_group.update' END)
    OR op.result_snapshot<>pricing_group_result(NEW) THEN
  RAISE EXCEPTION 'price group operation required';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: require_pricing_operation_result(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION require_pricing_operation_result() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE g price_groups%ROWTYPE; t tenants%ROWTYPE; p price_versions%ROWTYPE; c cost_versions%ROWTYPE;
 e tenant_pricing_assignments%ROWTYPE; expected JSONB; prior BIGINT; has_event BOOLEAN; current_revision bigint;
BEGIN
 IF NEW.created_txid<>txid_current() THEN RAISE EXCEPTION 'pricing operation not current transaction'; END IF;
 PERFORM assert_pricing_safe_input(NEW.kind,NEW.safe_input);
 IF NEW.kind='price_group.create' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  expected:=jsonb_build_object('label',g.label);
  IF NEW.target<>'/api/platform/price-groups' OR g.last_operation_id<>NEW.operation_id
     OR g.revision<>1 OR g.state<>'active' OR g.created_at<>NEW.created_at
     OR NEW.revision<>1 OR NEW.result_snapshot<>pricing_group_result(g) THEN
   RAISE EXCEPTION 'price group create result mismatch';
  END IF;
 ELSIF NEW.kind='price_group.update' THEN
  SELECT * INTO STRICT g FROM price_groups WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'label',g.label,'state',g.state);
  IF NEW.target<>('/api/platform/price-groups/'||g.id) OR NEW.revision<>g.revision
     OR NEW.result_snapshot<>pricing_group_result(g)
     OR NOT ((g.last_operation_id=NEW.operation_id AND prior<9223372036854775807 AND g.revision=prior+1)
       OR (g.last_operation_id<>NEW.operation_id AND g.revision=prior)) THEN
   RAISE EXCEPTION 'price group update result mismatch';
  END IF;
 ELSIF NEW.kind='group_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=pricing_price_result(p)-ARRAY['id','price_group_id','created_at'];
  IF p.price_group_id IS NULL OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor
     OR p.created_at<>NEW.created_at
     OR NEW.target<>('/api/platform/price-groups/'||p.price_group_id||'/price-versions')
     OR NEW.result_snapshot<>pricing_price_result(p) THEN
   RAISE EXCEPTION 'group price result mismatch';
  END IF;
  PERFORM meter_group_price_complete(p.id);
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSIF NEW.kind='default_price.publish' THEN
  SELECT * INTO STRICT p FROM price_versions WHERE id=NEW.resource_id;
  expected:=pricing_price_result(p)-ARRAY['id','price_group_id','created_at'];
  IF p.price_group_id IS NOT NULL OR p.pricing_operation_id<>NEW.operation_id OR p.created_by<>NEW.actor OR p.created_at<>NEW.created_at OR NEW.target<>'/api/platform/price-versions' OR NEW.result_snapshot<>pricing_price_result(p) THEN RAISE EXCEPTION 'default price result mismatch'; END IF;
  PERFORM meter_group_price_complete(p.id);
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSIF NEW.kind='cost_price.publish' THEN
  SELECT * INTO STRICT c FROM cost_versions WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_channel_revision')::bigint;
  expected:=pricing_cost_result(c,prior)-ARRAY['id','created_at','channel_revision'];
  SELECT management_revision INTO STRICT current_revision FROM channels WHERE id=c.channel_id;
  IF c.pricing_operation_id<>NEW.operation_id OR c.created_by<>NEW.actor OR c.created_at<>NEW.created_at OR NEW.target<>'/api/platform/cost-versions' OR current_revision<>prior+1 OR NEW.result_snapshot<>pricing_cost_result(c,prior) THEN RAISE EXCEPTION 'cost price result mismatch'; END IF;
  PERFORM meter_group_price_audit(NEW.operation_id);
 ELSE
  SELECT * INTO STRICT t FROM tenants WHERE id=NEW.resource_id;
  prior:=(NEW.safe_input->>'expected_revision')::bigint;
  expected:=jsonb_build_object('expected_revision',prior::text,'price_group_id',t.price_group_id);
  SELECT * INTO e FROM tenant_pricing_assignments WHERE operation_id=NEW.operation_id;
  has_event:=FOUND;
  IF NEW.target<>('/api/platform/tenants/'||t.id||'/pricing') OR NEW.revision<>t.pricing_revision
     OR NEW.result_snapshot<>pricing_assignment_result(t) THEN
   RAISE EXCEPTION 'tenant pricing result mismatch';
  END IF;
  IF t.pricing_operation_id=NEW.operation_id THEN
   IF NOT has_event OR e.created_txid<>txid_current() OR e.tenant_id<>t.id OR e.actor<>NEW.actor
      OR e.after_group_id IS DISTINCT FROM t.price_group_id OR e.before_revision<>prior
      OR e.after_revision<>t.pricing_revision OR e.created_at<>NEW.created_at THEN
    RAISE EXCEPTION 'tenant pricing event mismatch';
   END IF;
  ELSIF has_event OR t.pricing_revision<>prior THEN
   RAISE EXCEPTION 'tenant pricing no-op mismatch';
  END IF;
 END IF;
 IF NEW.safe_input IS DISTINCT FROM expected OR NEW.input_fingerprint IS DISTINCT FROM
    pricing_fingerprint(NEW.kind,NEW.actor,NEW.scope,NEW.target,expected) THEN
  RAISE EXCEPTION 'pricing command input mismatch';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: require_tenant_pricing_assignment(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION require_tenant_pricing_assignment() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE t tenants%ROWTYPE; op pricing_operations%ROWTYPE;
BEGIN
 SELECT * INTO STRICT t FROM tenants WHERE id=NEW.tenant_id;
 SELECT * INTO STRICT op FROM pricing_operations WHERE operation_id=NEW.operation_id;
 IF NEW.created_txid<>txid_current() OR op.created_txid<>txid_current()
    OR op.kind<>'tenant_pricing.assign' OR op.actor<>NEW.actor OR op.created_at<>NEW.created_at
    OR op.resource_id<>NEW.tenant_id OR op.revision<>NEW.after_revision
    OR t.pricing_operation_id<>NEW.operation_id OR t.pricing_revision<>NEW.after_revision
    OR t.price_group_id IS DISTINCT FROM NEW.after_group_id THEN
  RAISE EXCEPTION 'assignment event without same transaction change';
 END IF;
 RETURN NULL;
END $$;


--
-- Name: set_credential_pool_creation_xid(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION set_credential_pool_creation_xid() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 NEW.credential_pool_creation_xid := pg_current_xact_id();
 RETURN NEW;
END $$;


--
-- Name: synchronize_cost_version_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION synchronize_cost_version_insert() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 UPDATE channels SET id=id WHERE id=NEW.channel_id;
 IF EXISTS(SELECT 1 FROM cost_versions WHERE channel_id=NEW.channel_id AND upstream_model=NEW.upstream_model AND schema_id=NEW.schema_id AND variant_key=NEW.variant_key AND effective_at=NEW.effective_at) THEN
  RAISE EXCEPTION 'cost version effective instant already exists' USING ERRCODE='23505',CONSTRAINT='cost_version_effective_instant';
 END IF;
 RETURN NEW;
END $$;


--
-- Name: synchronize_procurement_binding_insert(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION synchronize_procurement_binding_insert() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE credential_channel TEXT;
BEGIN
 -- This is also the barrier for raw SQL and for an initially absent binding.
 -- Never start from the account and return to an earlier channel lock.
 UPDATE channels SET id=id WHERE id=NEW.channel_id;
 SELECT channel_id INTO credential_channel FROM channel_credentials WHERE id=NEW.credential_id FOR SHARE;
 IF credential_channel IS DISTINCT FROM NEW.channel_id THEN RAISE EXCEPTION 'procurement binding credential identity mismatch' USING ERRCODE='23503'; END IF;
 PERFORM id FROM procurement_accounts WHERE id=NEW.account_id FOR NO KEY UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'procurement account not found' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;


--
-- Name: valid_channel_batch_items(jsonb, text, text, integer, integer); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION valid_channel_batch_items(items jsonb, operation_status text, operation_action text, targets integer, changes integer) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
DECLARE item JSONB; before_state JSONB; after_state JSONB; item_status TEXT; reason TEXT;
 previous_channel TEXT := ''; channel TEXT; changed_items INTEGER := 0; blocked_items INTEGER := 0;
 metric TEXT; desired BOOLEAN := operation_action='enable';
BEGIN
 IF jsonb_typeof(items) IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 IF targets NOT BETWEEN 1 AND 100 OR jsonb_array_length(items)<>targets OR octet_length(items::text)>262144 THEN RETURN false; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(items) LOOP
  IF jsonb_typeof(item) IS DISTINCT FROM 'object'
   OR NOT item ?& ARRAY['channel_id','expected_version','expected_revision','before','live_credentials','published_bindings','older_version_bindings','status','reason']
   OR EXISTS(SELECT 1 FROM jsonb_object_keys(item) k WHERE k NOT IN ('channel_id','expected_version','expected_revision','before','after','live_credentials','published_bindings','older_version_bindings','status','reason')) THEN RETURN false; END IF;
  channel := item->>'channel_id';
  IF jsonb_typeof(item->'channel_id') IS DISTINCT FROM 'string' OR channel !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR channel<=previous_channel OR jsonb_typeof(item->'expected_version') IS DISTINCT FROM 'string'
   OR (item->>'expected_version') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR jsonb_typeof(item->'expected_revision') IS DISTINCT FROM 'string' OR NOT valid_channel_batch_revision(item->>'expected_revision') THEN RETURN false; END IF;
  previous_channel := channel;
  FOREACH metric IN ARRAY ARRAY['live_credentials','published_bindings','older_version_bindings'] LOOP
   IF jsonb_typeof(item->metric) IS DISTINCT FROM 'number' OR (item->>metric) !~ '^(0|[1-9][0-9]{0,9})$' THEN RETURN false; END IF;
   IF (item->>metric)::NUMERIC>2147483647 THEN RETURN false; END IF;
  END LOOP;
  IF (item->>'live_credentials')::INTEGER>32 OR (item->>'older_version_bindings')::INTEGER>(item->>'published_bindings')::INTEGER THEN RETURN false; END IF;
  IF jsonb_typeof(item->'status') IS DISTINCT FROM 'string' OR jsonb_typeof(item->'reason') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  before_state := item->'before'; after_state := item->'after'; item_status := item->>'status'; reason := item->>'reason';
  IF item_status='blocked' THEN
   blocked_items := blocked_items+1;
   IF operation_status<>'rejected' OR item ? 'after' OR reason NOT IN ('not_found','stale_version','stale_revision','channel_quarantined','no_live_credential') THEN RETURN false; END IF;
   IF reason='not_found' THEN
    IF before_state<>'null'::jsonb OR (item->>'live_credentials')::INTEGER<>0 OR (item->>'published_bindings')::INTEGER<>0 THEN RETURN false; END IF;
   ELSE
    IF NOT valid_channel_batch_state(before_state) THEN RETURN false; END IF;
    IF reason='stale_version' THEN
     IF before_state->>'current_version'=item->>'expected_version' THEN RETURN false; END IF;
    ELSE
     IF before_state->>'current_version'<>item->>'expected_version' THEN RETURN false; END IF;
     IF reason='stale_revision' THEN
      IF before_state->>'management_revision'=item->>'expected_revision' THEN RETURN false; END IF;
     ELSE
      IF before_state->>'management_revision'<>item->>'expected_revision' OR NOT desired THEN RETURN false; END IF;
      IF reason='channel_quarantined' AND NOT (before_state->>'quarantined')::BOOLEAN THEN RETURN false; END IF;
      IF reason='no_live_credential' AND ((before_state->>'quarantined')::BOOLEAN OR (item->>'live_credentials')::INTEGER<>0) THEN RETURN false; END IF;
     END IF;
    END IF;
   END IF;
  ELSE
   IF NOT valid_channel_batch_state(before_state) OR reason<>''
    OR before_state->>'current_version'<>item->>'expected_version'
    OR before_state->>'management_revision'<>item->>'expected_revision' THEN RETURN false; END IF;
   IF desired AND ((before_state->>'quarantined')::BOOLEAN OR (item->>'live_credentials')::INTEGER=0) THEN RETURN false; END IF;
   IF item_status='changed' THEN
    changed_items := changed_items+1;
    IF operation_status<>'applied' OR NOT valid_channel_batch_state(after_state)
     OR (before_state->>'enabled')::BOOLEAN=desired OR (after_state->>'enabled')::BOOLEAN<>desired
     OR before_state->>'current_version'<>after_state->>'current_version'
     OR before_state->>'quarantined'<>after_state->>'quarantined'
     OR (after_state->>'management_revision')::NUMERIC<>(before_state->>'management_revision')::NUMERIC+1 THEN RETURN false; END IF;
   ELSIF item_status='unchanged' THEN
    IF (before_state->>'enabled')::BOOLEAN<>desired THEN RETURN false; END IF;
    IF operation_status='applied' THEN
     IF NOT valid_channel_batch_state(after_state) OR after_state<>before_state THEN RETURN false; END IF;
    ELSIF item ? 'after' THEN RETURN false;
    END IF;
   ELSIF item_status='not_applied' THEN
    IF operation_status<>'rejected' OR item ? 'after' OR (before_state->>'enabled')::BOOLEAN=desired THEN RETURN false; END IF;
   ELSE RETURN false;
   END IF;
  END IF;
 END LOOP;
 RETURN changed_items=changes AND ((operation_status='applied' AND blocked_items=0) OR (operation_status='rejected' AND changes=0 AND blocked_items>0));
END $_$;


--
-- Name: valid_channel_batch_revision(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION valid_channel_batch_revision(value text) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
BEGIN
 IF value IS NULL OR value !~ '^[1-9][0-9]{0,18}$' THEN RETURN false; END IF;
 RETURN value::NUMERIC<=9223372036854775807;
END $_$;


--
-- Name: valid_channel_batch_state(jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION valid_channel_batch_state(value jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
BEGIN
 IF jsonb_typeof(value) IS DISTINCT FROM 'object'
  OR NOT value ?& ARRAY['current_version','management_revision','enabled','quarantined']
  OR EXISTS(SELECT 1 FROM jsonb_object_keys(value) k WHERE k NOT IN ('current_version','management_revision','enabled','quarantined')) THEN RETURN false; END IF;
 RETURN jsonb_typeof(value->'current_version')='string'
  AND (value->>'current_version') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND jsonb_typeof(value->'management_revision')='string'
  AND valid_channel_batch_revision(value->>'management_revision')
  AND jsonb_typeof(value->'enabled')='boolean'
  AND jsonb_typeof(value->'quarantined')='boolean';
END $_$;


--
-- Name: valid_key_client_cidrs(text[]); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION valid_key_client_cidrs(items text[]) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $$
DECLARE value TEXT; canonical TEXT; previous TEXT := NULL; total INTEGER := 0;
BEGIN
 IF items IS NULL OR cardinality(items)>32 THEN RETURN false; END IF;
 IF cardinality(items)=0 THEN RETURN true; END IF;
 IF array_ndims(items)<>1 OR array_lower(items,1)<>1 THEN RETURN false; END IF;
 FOREACH value IN ARRAY items LOOP
  IF value IS NULL THEN RETURN false; END IF;
  total:=total+octet_length(value);
  IF total>4096 THEN RETURN false; END IF;
  canonical:=canonical_key_client_cidr(value);
  IF canonical IS NULL OR value<>canonical OR (previous IS NOT NULL AND (value COLLATE "C")<=(previous COLLATE "C")) THEN RETURN false; END IF;
  previous:=value;
 END LOOP;
 RETURN true;
END $$;


--
-- Name: valid_procurement_command_snapshot(text, jsonb); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION valid_procurement_command_snapshot(operation_kind text, snapshot jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
DECLARE allowed TEXT[]; entry RECORD;
BEGIN
 IF snapshot='null'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(snapshot) IS DISTINCT FROM 'object' OR octet_length(snapshot::text)>16384 THEN RETURN false; END IF;
 IF operation_kind IN ('account_create','account_configure') THEN allowed := ARRAY['id','label','provider','state','limit_amount','held_amount','spent_amount','revision','created_at'];
 ELSIF operation_kind='credential_bind' THEN allowed := ARRAY['id','credential_id','channel_id','account_id','operation_id','bound_at','channel_revision'];
 ELSIF operation_kind='balance_observe' THEN allowed := ARRAY['id','account_id','operation_id','actor','status','amount','currency','source','semantic','observed_at','provider_as_of','evidence_ref','created_at'];
 ELSE RETURN false; END IF;
 IF NOT snapshot ?& allowed OR EXISTS(SELECT 1 FROM jsonb_object_keys(snapshot) k WHERE NOT k=ANY(allowed)) THEN RETURN false; END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(snapshot) LOOP
  IF operation_kind='balance_observe' AND entry.key IN ('amount','provider_as_of') AND entry.value='null'::jsonb THEN CONTINUE; END IF;
  IF jsonb_typeof(entry.value) IS DISTINCT FROM 'string' OR octet_length(entry.value#>>'{}')>1000 OR (entry.value#>>'{}') ~ '[[:cntrl:]]' THEN RETURN false; END IF;
 END LOOP;
 IF operation_kind IN ('account_create','account_configure') THEN
  RETURN snapshot->>'state' IN ('active','paused') AND valid_channel_batch_revision(snapshot->>'revision')
   AND snapshot->>'limit_amount' ~ '^(0|[1-9][0-9]{0,18})$' AND (snapshot->>'limit_amount')::numeric<=9223372036854775807
   AND snapshot->>'held_amount' ~ '^(0|[1-9][0-9]{0,99})$' AND snapshot->>'spent_amount' ~ '^(0|[1-9][0-9]{0,99})$';
 ELSIF operation_kind='credential_bind' THEN RETURN valid_channel_batch_revision(snapshot->>'channel_revision');
 ELSE RETURN snapshot->>'currency'='CNY' AND snapshot->>'status' IN ('known','unknown','unsupported','error')
  AND ((snapshot->>'status'='known' AND jsonb_typeof(snapshot->'amount')='string' AND snapshot->>'amount' ~ '^(0|[1-9][0-9]{0,18})$' AND (snapshot->>'amount')::numeric<=9223372036854775807)
   OR (snapshot->>'status'<>'known' AND snapshot->'amount'='null'::jsonb));
 END IF;
END $_$;


--
-- Name: voucher_batch_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION voucher_batch_complete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE batch_ref TEXT; b voucher_batches%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='voucher_batches' THEN batch_ref:=NEW.id;ELSE batch_ref:=NEW.batch_id;END IF;
 SELECT * INTO b FROM voucher_batches WHERE id=batch_ref;
 IF b.id IS NULL OR b.count<>(SELECT count(*) FROM vouchers WHERE batch_id=batch_ref) OR b.redeemed_count<>(SELECT count(*) FROM voucher_redemptions WHERE batch_id=batch_ref) THEN RAISE EXCEPTION 'voucher batch facts incomplete'; END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.create' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.maker) THEN RAISE EXCEPTION 'voucher batch creation incomplete';END IF;
 IF b.state IN ('active','revoked') AND NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.approve' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.approver) THEN RAISE EXCEPTION 'voucher batch approval incomplete';END IF;
 IF b.state='voided' AND NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.void' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.maker) THEN RAISE EXCEPTION 'voucher batch void incomplete';END IF;
 IF b.state='revoked' AND NOT EXISTS(SELECT 1 FROM voucher_revocations p JOIN voucher_revocation_decisions d ON d.proposal_id=p.id
  JOIN commerce_commands cmd ON cmd.operation_id=d.operation_id JOIN commerce_operation_receipts terminal ON terminal.operation_id=cmd.operation_id
  WHERE p.batch_id=b.id AND d.batch_id=b.id AND d.status='accepted' AND d.reason='accepted' AND d.approver<>p.maker
  AND b.terminal_actor=d.approver AND b.revision=p.expected_revision+1 AND b.redeemed_count=p.expected_redeemed_count
  AND cmd.kind='voucher.revocation.approve' AND cmd.scope='platform' AND cmd.actor=d.approver
  AND terminal.status='accepted' AND terminal.resource_id=p.id) THEN RAISE EXCEPTION 'voucher revocation decision incomplete';END IF;
 RETURN NULL;
END $$;


--
-- Name: voucher_batch_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION voucher_batch_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable voucher batch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'proposed' OR NEW.redeemed_count<>0 THEN RAISE EXCEPTION 'voucher batch initial state'; END IF;NEW.revision:=1;RETURN NEW;
 END IF;
 IF (NEW.id,NEW.face_amount,NEW.count,NEW.expires_at,NEW.purpose_ref,NEW.reason,NEW.maker,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.face_amount,OLD.count,OLD.expires_at,OLD.purpose_ref,OLD.reason,OLD.maker,OLD.created_at) THEN RAISE EXCEPTION 'immutable voucher batch'; END IF;
 IF NEW.state=OLD.state THEN
  IF (NEW.approver,NEW.activated_at,NEW.terminal_actor,NEW.terminal_at) IS DISTINCT FROM (OLD.approver,OLD.activated_at,OLD.terminal_actor,OLD.terminal_at) THEN RAISE EXCEPTION 'immutable voucher approval'; END IF;
  IF NEW.redeemed_count<>OLD.redeemed_count AND (OLD.state<>'active' OR NEW.redeemed_count<>OLD.redeemed_count+1 OR NEW.redeemed_count<>(SELECT count(*) FROM voucher_redemptions WHERE batch_id=NEW.id)) THEN RAISE EXCEPTION 'voucher batch counter'; END IF;
  NEW.revision:=OLD.revision;
 ELSE
  IF NOT ((OLD.state='proposed' AND NEW.state IN ('active','voided')) OR (OLD.state='active' AND NEW.state='revoked')) OR NEW.redeemed_count<>OLD.redeemed_count THEN RAISE EXCEPTION 'voucher batch transition'; END IF;
  IF OLD.state='active' AND (NEW.approver,NEW.activated_at) IS DISTINCT FROM (OLD.approver,OLD.activated_at) THEN RAISE EXCEPTION 'immutable voucher approval'; END IF;
  NEW.revision:=OLD.revision+1;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: voucher_policy_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION voucher_policy_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable voucher policy'; END IF;
 IF TG_OP='INSERT' THEN NEW.revision:=1; ELSE NEW.revision:=OLD.revision+CASE WHEN NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END; END IF;
 RETURN NEW;
END $$;


--
-- Name: voucher_proposal_snapshot(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION voucher_proposal_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.batch_id AND b.state='active' AND b.revision=NEW.expected_revision AND b.redeemed_count=NEW.expected_redeemed_count) THEN RAISE EXCEPTION 'voucher proposal snapshot invalid';END IF;
 RETURN NEW;
END $$;


--
-- Name: voucher_redemption_count(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION voucher_redemption_count() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ BEGIN
 UPDATE voucher_batches SET redeemed_count=redeemed_count+1 WHERE id=NEW.batch_id;RETURN NEW;
END $$;


--
-- Name: voucher_revocation_complete(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION voucher_revocation_complete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE p voucher_revocations%ROWTYPE; d voucher_revocation_decisions%ROWTYPE; b voucher_batches%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='voucher_revocations' THEN
  p:=NEW;
 ELSE
  d:=NEW;SELECT * INTO p FROM voucher_revocations WHERE id=d.proposal_id;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts terminal USING(operation_id)
  WHERE c.operation_id=p.operation_id AND c.kind='voucher.revocation.create' AND c.scope='platform' AND c.actor=p.maker
   AND terminal.status='accepted' AND terminal.resource_id=p.id) THEN RAISE EXCEPTION 'voucher revocation proposal incomplete';END IF;
 IF TG_TABLE_NAME='voucher_revocation_decisions' THEN
  SELECT * INTO b FROM voucher_batches WHERE id=p.batch_id;
  IF d.approver=p.maker OR d.batch_id<>p.batch_id OR NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts terminal USING(operation_id)
   WHERE c.operation_id=d.operation_id AND c.kind='voucher.revocation.approve' AND c.scope='platform' AND c.actor=d.approver
    AND terminal.status=d.status AND terminal.reason=d.reason AND (d.status='rejected' OR terminal.resource_id=p.id)) THEN RAISE EXCEPTION 'voucher revocation terminal incomplete';END IF;
  IF d.status='accepted' THEN
   IF b.state<>'revoked' OR b.terminal_actor<>d.approver OR b.revision<>p.expected_revision+1 OR b.redeemed_count<>p.expected_redeemed_count THEN RAISE EXCEPTION 'voucher revocation state incomplete';END IF;
  ELSIF b.state='active' AND b.revision=p.expected_revision AND b.redeemed_count=p.expected_redeemed_count THEN RAISE EXCEPTION 'voucher revocation evidence not stale';END IF;
 END IF;
 RETURN NULL;
END $$;


--
-- Name: wallet_account_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_account_valid(account_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=account_ref
 AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)
 AND (a.tenant_id IS NULL OR (a.kind='AVAILABLE' AND a.balance::numeric=(SELECT coalesce(sum(b.available_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))
  OR (a.kind='HELD' AND a.balance::numeric=(SELECT coalesce(sum(b.held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))))
$$;


--
-- Name: wallet_event_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_event_valid(event_ref text) RETURNS boolean
    LANGUAGE plpgsql STABLE
    
    AS $$
DECLARE event_kind TEXT;
BEGIN
 SELECT kind INTO event_kind FROM funding_lot_events WHERE id=event_ref;
 CASE event_kind
 WHEN 'funding' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id WHERE e.id=event_ref AND e.revision=1 AND f.journal_id=j.id AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0 AND funding_journal_valid(j.id));
 WHEN 'reserve' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='reserve' AND j.reference=r.request_id AND a.request_id=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0)
   OR EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN media_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN media_reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='media.reserve' AND j.reference=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0);
 WHEN 'finish' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.reference=s.request_id AND j.kind=s.status AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount)
   OR EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN media_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN media_settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.kind='media.settle' AND j.reference=s.request_id AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount);
 WHEN 'refund' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_consumption_refund_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN refund_orders r ON r.id=a.refund_order_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND r.state='approved' AND r.journal_id=j.id AND j.operation_id=r.id AND j.kind='refund' AND j.reference=r.settlement_id AND e.delta_available=a.amount AND e.delta_held=0 AND e.delta_consumed=-a.amount);
 ELSE RETURN false;
 END CASE;
END $$;


--
-- Name: wallet_journal_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_journal_valid(journal_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM journals j WHERE j.id=journal_ref
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE')
   =(SELECT coalesce(sum(e.delta_available::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='HELD')
   =(SELECT coalesce(sum(e.delta_held::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REVENUE')
   =(SELECT coalesce(sum(e.delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.journal_id=j.id AND NOT wallet_event_valid(e.id)))
$$;


--
-- Name: wallet_lot_balance_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_lot_balance_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE principal BIGINT;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'wallet balances cannot be deleted'; END IF;
 SELECT issued_amount INTO STRICT principal FROM funding_lots WHERE id=NEW.lot_id AND tenant_id=NEW.tenant_id;
 IF NEW.available_amount::numeric+NEW.held_amount::numeric+NEW.consumed_amount::numeric<>principal::numeric THEN RAISE EXCEPTION 'wallet lot principal mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.available_amount<>principal OR NEW.held_amount<>0 OR NEW.consumed_amount<>0 THEN RAISE EXCEPTION 'invalid wallet genesis'; END IF;
 ELSE
  IF NEW.lot_id<>OLD.lot_id OR NEW.tenant_id<>OLD.tenant_id OR NEW.revision::numeric<>OLD.revision::numeric+1
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=NEW.lot_id AND e.tenant_id=NEW.tenant_id AND e.revision=NEW.revision AND e.created_xid=pg_current_xact_id()
    AND e.delta_available::numeric=NEW.available_amount::numeric-OLD.available_amount::numeric
    AND e.delta_held::numeric=NEW.held_amount::numeric-OLD.held_amount::numeric
    AND e.delta_consumed::numeric=NEW.consumed_amount::numeric-OLD.consumed_amount::numeric) THEN RAISE EXCEPTION 'wallet update requires matching event'; END IF;
 END IF;
 RETURN NEW;
END $$;


--
-- Name: wallet_lot_event_stamp(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_lot_event_stamp() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=NEW.journal_id AND j.tenant_id=NEW.tenant_id AND j.operation_id=NEW.operation_id AND j.created_txid=txid_current()) THEN RAISE EXCEPTION 'wallet event requires current journal'; END IF;
 NEW.created_xid:=pg_current_xact_id(); RETURN NEW;
END $$;


--
-- Name: wallet_lot_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_lot_valid(lot_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM funding_lots l JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id
 JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
 WHERE l.id=lot_ref AND (l.source_kind,l.source_id,l.issued_amount,l.fifo_at)=(f.source_kind,f.source_id,f.amount,f.created_at)
 AND funding_journal_valid(f.journal_id)
 AND (SELECT count(*) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.revision
 AND (SELECT min(revision) FROM funding_lot_events e WHERE e.lot_id=l.id)=1
 AND (SELECT max(revision) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.revision
 AND (SELECT coalesce(sum(delta_available::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.available_amount::numeric
 AND (SELECT coalesce(sum(delta_held::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.held_amount::numeric
 AND (SELECT coalesce(sum(delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.consumed_amount::numeric
 AND EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=l.id AND e.revision=1 AND e.kind='funding' AND e.journal_id=f.journal_id
  AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0))
$$;


--
-- Name: wallet_opening_journal_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_opening_journal_valid(journal_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='wallet_opening'
 JOIN wallet_openings o ON o.id=f.source_id AND o.tenant_id=f.tenant_id
 WHERE j.id=journal_ref AND f.amount=o.amount AND f.currency='CNY' AND j.currency='CNY'
 AND j.tenant_id=o.tenant_id AND j.kind='wallet_opening' AND j.operation_id=o.id AND j.reference=o.id AND j.actor=o.actor
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=o.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=o.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='WALLET_OPENING_CLEARING' AND l.signed_delta=-o.amount)
 AND (SELECT count(*) FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited' AND x.tenant_id=o.tenant_id
  AND x.schema_version=1 AND jsonb_typeof(x.sanitized_payload->'amount')='string' AND x.sanitized_payload->>'amount'=o.amount::text))
$$;


--
-- Name: wallet_refund_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_refund_valid(ref_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM refund_orders r WHERE r.id=ref_ref AND
  ((r.state<>'approved' AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)) OR
   (r.state='approved' AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)=r.amount::numeric
    AND (SELECT min(refund_index)=1 AND max(refund_index)=count(*) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)
    AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id AND (a.settlement_id<>r.settlement_id OR a.tenant_id<>r.tenant_id
      OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=r.id AND e.lot_id=a.lot_id AND e.kind='refund' AND wallet_event_valid(e.id))))
    AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a JOIN funding_consumption_refund_lots p ON p.refund_order_id=a.refund_order_id AND p.refund_index<a.refund_index
     WHERE a.refund_order_id=r.id AND p.allocation_index<=a.allocation_index)
    AND wallet_settlement_valid(r.settlement_id))))
$$;


--
-- Name: wallet_reservation_fifo_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_reservation_fifo_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE next_lot TEXT; available BIGINT; remaining NUMERIC; allocated NUMERIC; edge_count BIGINT;
BEGIN
 -- Every customer money mutation serializes on these existing account rows.
 PERFORM id FROM ledger_accounts WHERE tenant_id=NEW.tenant_id AND kind='AVAILABLE' FOR UPDATE;
 SELECT coalesce(sum(amount::numeric),0),count(*) INTO allocated,edge_count FROM funding_reservation_lots WHERE reservation_id=NEW.reservation_id;
 SELECT r.amount::numeric-allocated INTO STRICT remaining FROM reservations r
  WHERE r.id=NEW.reservation_id AND r.tenant_id=NEW.tenant_id AND r.request_id=NEW.request_id AND r.status='reserved';
 SELECT l.id,b.available_amount INTO next_lot,available FROM funding_lots l JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
  WHERE l.tenant_id=NEW.tenant_id AND b.available_amount>0 ORDER BY l.fifo_at,l.id COLLATE "C" LIMIT 1;
 IF NEW.allocation_index<>edge_count+1 OR NEW.lot_id IS DISTINCT FROM next_lot OR remaining<=0
  OR NEW.amount::numeric<>least(available::numeric,remaining) THEN RAISE EXCEPTION 'wallet reservation requires FIFO source allocation'; END IF;
 RETURN NEW;
END $$;


--
-- Name: wallet_reservation_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_reservation_valid(res_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM reservations r WHERE r.id=res_ref
 AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_reservation_lots a WHERE a.reservation_id=r.id)=r.amount::numeric
 AND (r.amount=0 OR (SELECT min(allocation_index)=1 AND max(allocation_index)=count(*) FROM funding_reservation_lots a WHERE a.reservation_id=r.id))
 AND NOT EXISTS(SELECT 1 FROM funding_reservation_lots a WHERE a.reservation_id=r.id AND (a.tenant_id<>r.tenant_id OR a.request_id<>r.request_id OR NOT EXISTS(
  SELECT 1 FROM funding_lot_events e WHERE e.lot_id=a.lot_id AND e.operation_id=r.id AND e.kind='reserve' AND wallet_event_valid(e.id))))
 AND EXISTS(SELECT 1 FROM journals j WHERE j.operation_id=r.id AND j.tenant_id=r.tenant_id AND j.kind='reserve' AND j.reference=r.request_id))
$$;


--
-- Name: wallet_settlement_valid(text); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_settlement_valid(set_ref text) RETURNS boolean
    LANGUAGE sql STABLE
    
    AS $$
 SELECT EXISTS(SELECT 1 FROM settlements s JOIN reservations r ON r.request_id=s.request_id AND r.tenant_id=s.tenant_id WHERE s.id=set_ref
 AND (SELECT count(*) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=(SELECT count(*) FROM funding_reservation_lots a WHERE a.reservation_id=r.id)
 AND (SELECT coalesce(sum(charged_amount::numeric),0) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=s.charged_amount::numeric
 AND (SELECT coalesce(sum(released_amount::numeric),0) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=r.amount::numeric-s.charged_amount::numeric
 AND NOT EXISTS(SELECT 1 FROM funding_settlement_lots f JOIN funding_reservation_lots a ON a.reservation_id=f.reservation_id AND a.allocation_index=f.allocation_index
  WHERE f.settlement_id=s.id AND (f.reservation_id<>r.id OR f.tenant_id<>s.tenant_id OR f.lot_id<>a.lot_id
   OR f.charged_amount::numeric+f.released_amount::numeric<>a.amount::numeric
   OR f.charged_amount::numeric<>least(a.amount::numeric,greatest(0::numeric,s.charged_amount::numeric-(SELECT coalesce(sum(p.amount::numeric),0) FROM funding_reservation_lots p WHERE p.reservation_id=r.id AND p.allocation_index<a.allocation_index)))
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=s.id AND e.lot_id=f.lot_id AND e.kind='finish' AND wallet_event_valid(e.id))))
 AND (SELECT coalesce(sum(amount::numeric),0) FROM funding_consumption_refund_lots x WHERE x.settlement_id=s.id)=s.refunded_amount::numeric
 AND NOT EXISTS(SELECT 1 FROM funding_settlement_lots a WHERE a.settlement_id=s.id
  AND (SELECT coalesce(sum(x.amount::numeric),0) FROM funding_consumption_refund_lots x WHERE x.settlement_id=s.id AND x.allocation_index=a.allocation_index)
   <>least(a.charged_amount::numeric,greatest(0::numeric,s.refunded_amount::numeric-(SELECT coalesce(sum(p.charged_amount::numeric),0) FROM funding_settlement_lots p WHERE p.settlement_id=s.id AND p.allocation_index>a.allocation_index)))))
$$;


--
-- Name: wallet_source_fact_guard(); Type: FUNCTION; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE FUNCTION wallet_source_fact_guard() RETURNS trigger
    LANGUAGE plpgsql
    
    AS $$
DECLARE ok BOOLEAN; target_lot TEXT;
BEGIN
 CASE TG_TABLE_NAME
 WHEN 'wallet_openings' THEN SELECT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='wallet_opening' AND f.source_id=NEW.id AND wallet_opening_journal_valid(f.journal_id)) INTO ok;
 WHEN 'funding_receipts' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lots' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lot_balances' THEN SELECT wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'funding_lot_events' THEN SELECT wallet_event_valid(NEW.id) AND wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'reservations' THEN SELECT wallet_reservation_valid(NEW.id) INTO ok;
 WHEN 'funding_reservation_lots' THEN SELECT wallet_reservation_valid(NEW.reservation_id) INTO ok;
 WHEN 'settlements' THEN SELECT wallet_settlement_valid(NEW.id) INTO ok;
 WHEN 'funding_settlement_lots' THEN SELECT wallet_settlement_valid(NEW.settlement_id) INTO ok;
 WHEN 'refund_orders' THEN SELECT wallet_refund_valid(NEW.id) INTO ok;
 WHEN 'funding_consumption_refund_lots' THEN SELECT wallet_refund_valid(NEW.refund_order_id) INTO ok;
 WHEN 'journals' THEN SELECT wallet_journal_valid(NEW.id) INTO ok;
 WHEN 'ledger_accounts' THEN SELECT wallet_account_valid(NEW.id) INTO ok;
 ELSE ok:=false;
 END CASE;
 IF ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'wallet source facts do not match'; END IF;
 RETURN NULL;
END $$;


--
-- Name: active_report_projection; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE active_report_projection (
    singleton boolean DEFAULT true NOT NULL,
    consumer text NOT NULL,
    switched_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT active_report_projection_singleton_check CHECK (singleton)
);


--
-- Name: alert_commands; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE alert_commands (
    operation_id text NOT NULL,
    actor text NOT NULL,
    kind text NOT NULL,
    target_id text,
    canonical_digest text NOT NULL,
    transaction_id bigint DEFAULT txid_current() NOT NULL,
    status text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    resource_id text,
    result_revision bigint,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT alert_commands_actor_check CHECK ((alert_id(actor) IS TRUE)),
    CONSTRAINT alert_commands_canonical_digest_check CHECK ((canonical_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT alert_commands_check CHECK ((((kind = 'rule_create'::text) AND (target_id IS NULL)) OR ((kind <> 'rule_create'::text) AND (target_id IS NOT NULL)))),
    CONSTRAINT alert_commands_check1 CHECK ((((status = 'claimed'::text) AND (reason = ''::text) AND (resource_id IS NULL) AND (result_revision IS NULL)) OR ((status = 'applied'::text) AND (reason = ''::text) AND (resource_id IS NOT NULL) AND (result_revision IS NOT NULL)) OR ((status = 'rejected'::text) AND (reason = ANY (ARRAY['alert_revision_stale'::text, 'alert_rule_limit'::text, 'alert_rule_exists'::text, 'alert_rule_retired'::text, 'alert_source_missing'::text, 'alert_not_open'::text, 'alert_already_acknowledged'::text, 'alert_not_found'::text])) AND (result_revision IS NULL) AND (((kind = 'rule_create'::text) AND (resource_id IS NULL)) OR ((kind <> 'rule_create'::text) AND (NOT (resource_id IS DISTINCT FROM target_id))))))),
    CONSTRAINT alert_commands_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT alert_commands_kind_check CHECK ((kind = ANY (ARRAY['rule_create'::text, 'rule_configure'::text, 'rule_retire'::text, 'acknowledge'::text]))),
    CONSTRAINT alert_commands_operation_id_check CHECK ((alert_id(operation_id) IS TRUE)),
    CONSTRAINT alert_commands_resource_id_check CHECK (((resource_id IS NULL) OR (alert_id(resource_id) IS TRUE))),
    CONSTRAINT alert_commands_result_revision_check CHECK (((result_revision IS NULL) OR (result_revision > 0))),
    CONSTRAINT alert_commands_status_check CHECK ((status = ANY (ARRAY['claimed'::text, 'applied'::text, 'rejected'::text]))),
    CONSTRAINT alert_commands_target_id_check CHECK (((target_id IS NULL) OR (alert_id(target_id) IS TRUE)))
);


--
-- Name: alert_control; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE alert_control (
    singleton boolean NOT NULL,
    CONSTRAINT alert_control_singleton_check CHECK ((singleton IS TRUE))
);


--
-- Name: alert_episode_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE alert_episode_events (
    id text NOT NULL,
    episode_id text NOT NULL,
    action_revision bigint NOT NULL,
    type text NOT NULL,
    actor text NOT NULL,
    observed_at timestamp(6) with time zone NOT NULL,
    safe_sample jsonb,
    created_at timestamp(6) with time zone NOT NULL,
    command_id text,
    CONSTRAINT alert_episode_events_action_revision_check CHECK (((action_revision >= 1) AND (action_revision <= 3))),
    CONSTRAINT alert_episode_events_actor_check CHECK (((actor = 'system'::text) OR (alert_id(actor) IS TRUE))),
    CONSTRAINT alert_episode_events_check CHECK ((created_at = observed_at)),
    CONSTRAINT alert_episode_events_check1 CHECK ((((type = ANY (ARRAY['opened'::text, 'recovered'::text])) AND (actor = 'system'::text) AND (command_id IS NULL) AND (safe_sample IS NOT NULL)) OR ((type <> ALL (ARRAY['opened'::text, 'recovered'::text])) AND (actor <> 'system'::text) AND (command_id IS NOT NULL) AND (safe_sample IS NULL)))),
    CONSTRAINT alert_episode_events_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT alert_episode_events_id_check CHECK ((alert_id(id) IS TRUE)),
    CONSTRAINT alert_episode_events_observed_at_check CHECK (isfinite(observed_at)),
    CONSTRAINT alert_episode_events_type_check CHECK ((type = ANY (ARRAY['opened'::text, 'acknowledged'::text, 'recovered'::text, 'rule_changed'::text, 'rule_disabled'::text, 'rule_retired'::text])))
);


--
-- Name: alert_episodes; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE alert_episodes (
    id text NOT NULL,
    rule_id text NOT NULL,
    generation bigint NOT NULL,
    rule_revision bigint NOT NULL,
    rule_snapshot jsonb NOT NULL,
    opened_at timestamp(6) with time zone NOT NULL,
    initial_sample jsonb NOT NULL,
    last_seen_at timestamp(6) with time zone NOT NULL,
    latest_sample jsonb NOT NULL,
    action_revision bigint NOT NULL,
    acknowledged_by text,
    acknowledged_at timestamp(6) with time zone,
    acknowledged_command_id text,
    resolved_at timestamp(6) with time zone,
    resolve_reason text,
    resolved_command_id text,
    CONSTRAINT alert_episodes_acknowledged_at_check CHECK (((acknowledged_at IS NULL) OR isfinite(acknowledged_at))),
    CONSTRAINT alert_episodes_acknowledged_by_check CHECK (((acknowledged_by IS NULL) OR (alert_id(acknowledged_by) IS TRUE))),
    CONSTRAINT alert_episodes_action_revision_check CHECK (((action_revision >= 1) AND (action_revision <= 3))),
    CONSTRAINT alert_episodes_check CHECK ((((acknowledged_by IS NULL) AND (acknowledged_at IS NULL) AND (acknowledged_command_id IS NULL)) OR ((acknowledged_by IS NOT NULL) AND (acknowledged_at IS NOT NULL) AND (acknowledged_command_id IS NOT NULL) AND (acknowledged_at >= opened_at)))),
    CONSTRAINT alert_episodes_check1 CHECK ((((resolved_at IS NULL) AND (resolve_reason IS NULL) AND (resolved_command_id IS NULL)) OR ((resolved_at IS NOT NULL) AND (resolved_at >= last_seen_at) AND (resolve_reason = ANY (ARRAY['recovered'::text, 'rule_changed'::text, 'rule_disabled'::text, 'rule_retired'::text])) AND (((resolve_reason = 'recovered'::text) AND (resolved_command_id IS NULL)) OR ((resolve_reason <> 'recovered'::text) AND (resolved_command_id IS NOT NULL)))))),
    CONSTRAINT alert_episodes_check2 CHECK (((resolved_at IS NULL) OR (acknowledged_at IS NULL) OR (resolved_at >= acknowledged_at))),
    CONSTRAINT alert_episodes_check3 CHECK ((last_seen_at >= opened_at)),
    CONSTRAINT alert_episodes_check4 CHECK ((alert_sample(initial_sample, (rule_snapshot ->> 'kind'::text)) IS TRUE)),
    CONSTRAINT alert_episodes_check5 CHECK ((alert_sample(latest_sample, (rule_snapshot ->> 'kind'::text)) IS TRUE)),
    CONSTRAINT alert_episodes_check6 CHECK ((opened_at = ((initial_sample ->> 'as_of'::text))::timestamp with time zone)),
    CONSTRAINT alert_episodes_check7 CHECK ((last_seen_at = ((latest_sample ->> 'as_of'::text))::timestamp with time zone)),
    CONSTRAINT alert_episodes_check8 CHECK ((action_revision = ((1 + ((acknowledged_at IS NOT NULL))::integer) + ((resolved_at IS NOT NULL))::integer))),
    CONSTRAINT alert_episodes_generation_check CHECK ((generation > 0)),
    CONSTRAINT alert_episodes_id_check CHECK ((alert_id(id) IS TRUE)),
    CONSTRAINT alert_episodes_last_seen_at_check CHECK (isfinite(last_seen_at)),
    CONSTRAINT alert_episodes_opened_at_check CHECK (isfinite(opened_at)),
    CONSTRAINT alert_episodes_resolved_at_check CHECK (((resolved_at IS NULL) OR isfinite(resolved_at))),
    CONSTRAINT alert_episodes_rule_revision_check CHECK ((rule_revision > 0)),
    CONSTRAINT alert_episodes_rule_snapshot_check CHECK ((alert_snapshot(rule_snapshot) IS TRUE))
);


--
-- Name: alert_rule_runtime; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE alert_rule_runtime (
    rule_id text NOT NULL,
    configured_revision bigint NOT NULL,
    write_revision bigint DEFAULT 1 NOT NULL,
    episode_generation bigint DEFAULT 0 NOT NULL,
    next_evaluation_at timestamp(6) with time zone,
    last_attempt_at timestamp(6) with time zone,
    last_success_at timestamp(6) with time zone,
    evaluated_revision bigint,
    last_sample jsonb,
    last_error_code text,
    current_episode_id text,
    CONSTRAINT alert_rule_runtime_check CHECK ((((last_success_at IS NULL) AND (evaluated_revision IS NULL) AND (last_sample IS NULL)) OR ((last_success_at IS NOT NULL) AND (evaluated_revision IS NOT NULL) AND (evaluated_revision > 0) AND (last_sample IS NOT NULL)))),
    CONSTRAINT alert_rule_runtime_configured_revision_check CHECK ((configured_revision > 0)),
    CONSTRAINT alert_rule_runtime_episode_generation_check CHECK ((episode_generation >= 0)),
    CONSTRAINT alert_rule_runtime_last_attempt_at_check CHECK (((last_attempt_at IS NULL) OR isfinite(last_attempt_at))),
    CONSTRAINT alert_rule_runtime_last_error_code_check CHECK ((last_error_code = ANY (ARRAY['source_missing'::text, 'source_query_timeout'::text, 'source_unavailable'::text, 'clock_regression'::text, 'counter_exhausted'::text, 'evaluation_failed'::text]))),
    CONSTRAINT alert_rule_runtime_last_success_at_check CHECK (((last_success_at IS NULL) OR isfinite(last_success_at))),
    CONSTRAINT alert_rule_runtime_next_evaluation_at_check CHECK (((next_evaluation_at IS NULL) OR isfinite(next_evaluation_at))),
    CONSTRAINT alert_rule_runtime_write_revision_check CHECK ((write_revision > 0))
);


--
-- Name: alert_rules; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE alert_rules (
    id text NOT NULL,
    kind text NOT NULL,
    scope_type text NOT NULL,
    scope_id text NOT NULL,
    label text NOT NULL,
    state text NOT NULL,
    parameters jsonb NOT NULL,
    configuration_revision bigint NOT NULL,
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamp(6) with time zone NOT NULL,
    updated_at timestamp(6) with time zone NOT NULL,
    retired_at timestamp(6) with time zone,
    last_command_id text NOT NULL,
    CONSTRAINT alert_rules_check CHECK ((alert_snapshot(jsonb_build_object('kind', kind, 'scope_type', scope_type, 'scope_id', scope_id, 'label', label, 'parameters', parameters)) IS TRUE)),
    CONSTRAINT alert_rules_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT alert_rules_check2 CHECK ((((state = 'retired'::text) AND (retired_at IS NOT NULL) AND (retired_at = updated_at)) OR ((state <> 'retired'::text) AND (retired_at IS NULL)))),
    CONSTRAINT alert_rules_configuration_revision_check CHECK ((configuration_revision > 0)),
    CONSTRAINT alert_rules_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT alert_rules_created_by_check CHECK ((alert_id(created_by) IS TRUE)),
    CONSTRAINT alert_rules_id_check CHECK ((alert_id(id) IS TRUE)),
    CONSTRAINT alert_rules_label_check CHECK ((alert_label(label) IS TRUE)),
    CONSTRAINT alert_rules_retired_at_check CHECK (((retired_at IS NULL) OR isfinite(retired_at))),
    CONSTRAINT alert_rules_state_check CHECK ((state = ANY (ARRAY['enabled'::text, 'disabled'::text, 'retired'::text]))),
    CONSTRAINT alert_rules_updated_at_check CHECK (isfinite(updated_at)),
    CONSTRAINT alert_rules_updated_by_check CHECK ((alert_id(updated_by) IS TRUE))
);


--
-- Name: api_keys; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE api_keys (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    owner_id text NOT NULL,
    name text NOT NULL,
    prefix text NOT NULL,
    digest text NOT NULL,
    scopes text[] NOT NULL,
    allowed_regions text[] DEFAULT ARRAY['global'::text] NOT NULL,
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    lifetime_limit bigint,
    spent bigint DEFAULT 0 NOT NULL,
    held bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    allowed_client_cidrs text[] DEFAULT '{}'::text[] NOT NULL,
    access_policy_revision bigint DEFAULT 1 NOT NULL,
    rpm_limit integer DEFAULT 60 NOT NULL,
    burst_limit integer DEFAULT 20 NOT NULL,
    concurrency_limit integer DEFAULT 16 NOT NULL,
    limit_revision bigint DEFAULT 1 NOT NULL,
    configuration_revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT api_keys_access_policy_revision_check CHECK ((access_policy_revision > 0)),
    CONSTRAINT api_keys_allowed_client_cidrs_check CHECK (valid_key_client_cidrs(allowed_client_cidrs)),
    CONSTRAINT api_keys_burst_limit_check CHECK (((burst_limit >= 1) AND (burst_limit <= 200))),
    CONSTRAINT api_keys_check CHECK (((lifetime_limit IS NULL) OR (((spent)::numeric + (held)::numeric) <= (lifetime_limit)::numeric))),
    CONSTRAINT api_keys_concurrency_limit_check CHECK (((concurrency_limit >= 1) AND (concurrency_limit <= 16))),
    CONSTRAINT api_keys_configuration_revision_check CHECK ((configuration_revision > 0)),
    CONSTRAINT api_keys_held_check CHECK ((held >= 0)),
    CONSTRAINT api_keys_lifetime_limit_check CHECK ((lifetime_limit >= 0)),
    CONSTRAINT api_keys_limit_revision_check CHECK ((limit_revision > 0)),
    CONSTRAINT api_keys_rpm_limit_check CHECK (((rpm_limit >= 1) AND (rpm_limit <= 6000))),
    CONSTRAINT api_keys_spent_check CHECK ((spent >= 0))
);


--
-- Name: attempt_meter_bound_lines; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE attempt_meter_bound_lines (
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    side text NOT NULL,
    dimension text NOT NULL,
    unit text NOT NULL,
    quantity bigint NOT NULL,
    CONSTRAINT attempt_meter_bound_lines_dimension_check CHECK ((dimension = ANY (ARRAY['text_input_token'::text, 'text_output_token'::text]))),
    CONSTRAINT attempt_meter_bound_lines_quantity_check CHECK ((quantity >= 0)),
    CONSTRAINT attempt_meter_bound_lines_side_check CHECK ((side = ANY (ARRAY['retail'::text, 'supplier'::text]))),
    CONSTRAINT attempt_meter_bound_lines_unit_check CHECK ((unit = 'token'::text))
);


--
-- Name: attempt_meter_snapshots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE attempt_meter_snapshots (
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    request_id text NOT NULL,
    model_id text NOT NULL,
    profile_version text NOT NULL,
    schema_id text NOT NULL,
    schema_digest bytea NOT NULL,
    variant_key text DEFAULT 'default'::text NOT NULL,
    quoted_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    cost_version_id text,
    quote_status text NOT NULL,
    unknown_reason text,
    cost_basis text NOT NULL,
    supplier_rates_canonical bytea,
    supplier_rates_digest bytea,
    retail_bounds_canonical bytea,
    retail_bounds_digest bytea,
    supplier_bounds_canonical bytea,
    supplier_bounds_digest bytea,
    supplier_meter_quote_digest bytea,
    input_bound bigint NOT NULL,
    output_bound bigint NOT NULL,
    plan_canonical bytea,
    plan_digest bytea,
    parent_plan_digest bytea,
    supplier_quote_canonical bytea,
    supplier_quote_digest bytea,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT attempt_meter_snapshots_check CHECK ((((plan_canonical IS NULL) AND (plan_digest IS NULL)) OR ((plan_canonical IS NOT NULL) AND (octet_length(plan_canonical) <= 65536) AND (octet_length(plan_digest) = 32)))),
    CONSTRAINT attempt_meter_snapshots_check1 CHECK ((((plan_digest IS NULL) AND (parent_plan_digest IS NULL)) OR ((plan_digest IS NOT NULL) AND (octet_length(parent_plan_digest) = 32)))),
    CONSTRAINT attempt_meter_snapshots_check2 CHECK ((((octet_length(retail_bounds_canonical) >= 1) AND (octet_length(retail_bounds_canonical) <= 8192)) AND (octet_length(retail_bounds_digest) = 32))),
    CONSTRAINT attempt_meter_snapshots_check3 CHECK ((((octet_length(supplier_bounds_canonical) >= 1) AND (octet_length(supplier_bounds_canonical) <= 8192)) AND (octet_length(supplier_bounds_digest) = 32))),
    CONSTRAINT attempt_meter_snapshots_check4 CHECK ((((supplier_rates_canonical IS NULL) AND (supplier_rates_digest IS NULL) AND (supplier_meter_quote_digest IS NULL)) OR ((supplier_rates_canonical IS NOT NULL) AND ((octet_length(supplier_rates_canonical) >= 1) AND (octet_length(supplier_rates_canonical) <= 8192)) AND (octet_length(supplier_rates_digest) = 32) AND (octet_length(supplier_meter_quote_digest) = 32)))),
    CONSTRAINT attempt_meter_snapshots_check5 CHECK ((((supplier_quote_canonical IS NULL) AND (supplier_quote_digest IS NULL)) OR ((supplier_quote_canonical IS NOT NULL) AND (octet_length(supplier_quote_canonical) <= 16384) AND (octet_length(supplier_quote_digest) = 32)))),
    CONSTRAINT attempt_meter_snapshots_check6 CHECK ((((quote_status = 'known'::text) AND (unknown_reason IS NULL) AND (cost_version_id IS NOT NULL) AND (cost_basis = ANY (ARRAY['contract'::text, 'invoice'::text])) AND ((plan_digest IS NULL) OR ((supplier_rates_canonical IS NOT NULL) AND (supplier_rates_digest IS NOT NULL)))) OR ((quote_status = 'unknown'::text) AND (cost_basis = 'unknown'::text) AND (supplier_rates_canonical IS NULL) AND (supplier_rates_digest IS NULL) AND (((unknown_reason = 'version_missing'::text) AND (cost_version_id IS NULL)) OR ((unknown_reason = 'basis_unknown'::text) AND (cost_version_id IS NOT NULL)))))),
    CONSTRAINT attempt_meter_snapshots_cost_basis_check CHECK ((cost_basis = ANY (ARRAY['contract'::text, 'invoice'::text, 'unknown'::text]))),
    CONSTRAINT attempt_meter_snapshots_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT attempt_meter_snapshots_input_bound_check CHECK ((input_bound >= 0)),
    CONSTRAINT attempt_meter_snapshots_output_bound_check CHECK ((output_bound >= 0)),
    CONSTRAINT attempt_meter_snapshots_quote_status_check CHECK ((quote_status = ANY (ARRAY['known'::text, 'unknown'::text]))),
    CONSTRAINT attempt_meter_snapshots_quoted_at_check CHECK (isfinite(quoted_at)),
    CONSTRAINT attempt_meter_snapshots_schema_digest_check CHECK ((octet_length(schema_digest) = 32)),
    CONSTRAINT attempt_meter_snapshots_unknown_reason_check CHECK ((unknown_reason = ANY (ARRAY['version_missing'::text, 'basis_unknown'::text]))),
    CONSTRAINT attempt_meter_snapshots_variant_key_check CHECK ((variant_key = 'default'::text))
);


--
-- Name: audit_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE audit_events (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    actor text NOT NULL,
    tenant_scope text,
    action text NOT NULL,
    target text NOT NULL,
    sanitized_diff jsonb DEFAULT '{}'::jsonb NOT NULL,
    request_id text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: billing_profile_blocks; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE billing_profile_blocks (
    model_id text NOT NULL,
    profile_version text NOT NULL,
    reason text NOT NULL,
    request_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: binding_cost_rate_limits; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE binding_cost_rate_limits (
    binding_id text NOT NULL,
    dimension text NOT NULL COLLATE pg_catalog."C",
    unit text NOT NULL COLLATE pg_catalog."C",
    numerator bigint NOT NULL,
    denominator bigint NOT NULL,
    CONSTRAINT binding_cost_rate_limits_denominator_check CHECK (((denominator >= 1) AND (denominator <= 1000000))),
    CONSTRAINT binding_cost_rate_limits_numerator_check CHECK ((numerator >= 0))
);


--
-- Name: channel_batch_operations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channel_batch_operations (
    operation_id text NOT NULL,
    actor text NOT NULL,
    action text NOT NULL,
    input_digest text NOT NULL,
    status text NOT NULL,
    target_count integer NOT NULL,
    changed_count integer NOT NULL,
    items jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT channel_batch_operations_action_check CHECK ((action = ANY (ARRAY['enable'::text, 'disable'::text]))),
    CONSTRAINT channel_batch_operations_check CHECK (((changed_count >= 0) AND (changed_count <= target_count))),
    CONSTRAINT channel_batch_operations_check1 CHECK (valid_channel_batch_items(items, status, action, target_count, changed_count)),
    CONSTRAINT channel_batch_operations_input_digest_check CHECK ((input_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT channel_batch_operations_operation_id_check CHECK ((operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text)),
    CONSTRAINT channel_batch_operations_status_check CHECK ((status = ANY (ARRAY['applied'::text, 'rejected'::text]))),
    CONSTRAINT channel_batch_operations_target_count_check CHECK (((target_count >= 1) AND (target_count <= 100)))
);


--
-- Name: channel_credentials; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channel_credentials (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    channel_id text NOT NULL,
    secret_ciphertext text NOT NULL,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: channel_model_discoveries; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channel_model_discoveries (
    id text NOT NULL,
    actor text NOT NULL,
    channel_id text NOT NULL,
    channel_version_id text NOT NULL,
    credential_id text NOT NULL,
    format text NOT NULL,
    bindings jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT channel_model_discoveries_bindings_check CHECK (((jsonb_typeof(bindings) = 'array'::text) AND (jsonb_array_length(bindings) <= 1000) AND (octet_length((bindings)::text) <= 262144))),
    CONSTRAINT channel_model_discoveries_format_check CHECK ((format = ANY (ARRAY['openai'::text, 'anthropic'::text, 'gemini'::text])))
);


--
-- Name: channel_model_discovery_results; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channel_model_discovery_results (
    discovery_id text NOT NULL,
    status text NOT NULL,
    model_ids jsonb NOT NULL,
    pages integer NOT NULL,
    error_code text DEFAULT ''::text NOT NULL,
    completed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT channel_model_discovery_results_check CHECK ((((status = 'completed'::text) AND (error_code = ''::text) AND ((pages >= 1) AND (pages <= 10))) OR ((status = 'failed'::text) AND (error_code <> ''::text) AND (pages = 0) AND (model_ids = '[]'::jsonb)))),
    CONSTRAINT channel_model_discovery_results_error_code_check CHECK (((error_code = ''::text) OR (error_code ~ '^[a-z][a-z0-9_]{0,79}$'::text))),
    CONSTRAINT channel_model_discovery_results_model_ids_check CHECK (((jsonb_typeof(model_ids) = 'array'::text) AND (jsonb_array_length(model_ids) <= 1000) AND (octet_length((model_ids)::text) <= 262144))),
    CONSTRAINT channel_model_discovery_results_pages_check CHECK (((pages >= 0) AND (pages <= 10))),
    CONSTRAINT channel_model_discovery_results_status_check CHECK ((status = ANY (ARRAY['completed'::text, 'failed'::text])))
);


--
-- Name: channel_version_credentials; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channel_version_credentials (
    channel_version_id text NOT NULL,
    channel_id text NOT NULL,
    credential_id text NOT NULL,
    "position" integer NOT NULL,
    label text NOT NULL,
    weight integer NOT NULL,
    max_inflight integer NOT NULL,
    CONSTRAINT channel_version_credentials_label_check CHECK ((((octet_length(label) >= 1) AND (octet_length(label) <= 64)) AND (label !~ '[[:cntrl:]]'::text) AND (label ~ '[^[:space:]]'::text))),
    CONSTRAINT channel_version_credentials_max_inflight_check CHECK (((max_inflight >= 1) AND (max_inflight <= 1000))),
    CONSTRAINT channel_version_credentials_position_check CHECK ((("position" >= 1) AND ("position" <= 32))),
    CONSTRAINT channel_version_credentials_weight_check CHECK (((weight >= 1) AND (weight <= 1000000)))
);


--
-- Name: channel_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channel_versions (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    channel_id text NOT NULL,
    base_url text NOT NULL,
    provider text DEFAULT 'openai'::text NOT NULL,
    region text DEFAULT 'global'::text NOT NULL,
    allowed_cidrs text[] DEFAULT '{}'::text[] NOT NULL,
    credential_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    credential_pool_creation_xid xid8 DEFAULT pg_current_xact_id() NOT NULL
);


--
-- Name: channels; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE channels (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    quarantined boolean DEFAULT false NOT NULL,
    current_version text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    management_revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT channels_management_revision_check CHECK ((management_revision > 0))
);


--
-- Name: commerce_commands; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE commerce_commands (
    operation_id text NOT NULL,
    actor text NOT NULL,
    scope text NOT NULL,
    kind text NOT NULL,
    request_digest text NOT NULL,
    fingerprint_key_version text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT commerce_commands_check CHECK (((kind = 'voucher.redeem'::text) = (fingerprint_key_version IS NOT NULL))),
    CONSTRAINT commerce_commands_check1 CHECK (((kind = ANY (ARRAY['voucher.redeem'::text, 'payment.order.create'::text, 'payment.order.cancel'::text])) = (scope <> 'platform'::text))),
    CONSTRAINT commerce_commands_fingerprint_key_version_check CHECK ((fingerprint_key_version ~ '^[A-Za-z0-9_-]{1,32}$'::text)),
    CONSTRAINT commerce_commands_kind_check CHECK ((kind = ANY (ARRAY['voucher.batch.create'::text, 'voucher.batch.approve'::text, 'voucher.batch.void'::text, 'voucher.revocation.create'::text, 'voucher.revocation.approve'::text, 'voucher.redeem'::text, 'payment.sku.publish'::text, 'payment.sku.activate'::text, 'payment.order.create'::text, 'payment.order.cancel'::text]))),
    CONSTRAINT commerce_commands_operation_id_check CHECK (funding_uuid(operation_id)),
    CONSTRAINT commerce_commands_request_digest_check CHECK ((request_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT commerce_commands_scope_check CHECK (((scope = 'platform'::text) OR (("left"(scope, 7) = 'tenant:'::text) AND funding_uuid(substr(scope, 8)))))
);


--
-- Name: commerce_operation_receipts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE commerce_operation_receipts (
    operation_id text NOT NULL,
    kind text NOT NULL,
    scope text NOT NULL,
    actor text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    resource_id text,
    amount bigint,
    funding_receipt_id text,
    journal_id text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT commerce_operation_receipts_amount_check CHECK ((amount > 0)),
    CONSTRAINT commerce_operation_receipts_check CHECK (((status = 'accepted'::text) = (reason = 'accepted'::text))),
    CONSTRAINT commerce_operation_receipts_check1 CHECK ((((status = 'accepted'::text) AND (resource_id IS NOT NULL) AND (((kind = 'voucher.redeem'::text) AND (amount IS NOT NULL) AND (funding_receipt_id IS NOT NULL) AND (journal_id IS NOT NULL)) OR ((kind <> 'voucher.redeem'::text) AND (amount IS NULL) AND (funding_receipt_id IS NULL) AND (journal_id IS NULL)))) OR ((status = 'rejected'::text) AND (resource_id IS NULL) AND (amount IS NULL) AND (funding_receipt_id IS NULL) AND (journal_id IS NULL)))),
    CONSTRAINT commerce_operation_receipts_reason_check CHECK ((reason = ANY (ARRAY['accepted'::text, 'voucher_unavailable'::text, 'vouchers_disabled'::text, 'voucher_revision_conflict'::text, 'voucher_evidence_stale'::text, 'voucher_state_conflict'::text, 'funding_amount_overflow'::text, 'payment_revision_conflict'::text, 'payment_state_conflict'::text, 'payments_disabled'::text, 'payment_sku_unavailable'::text]))),
    CONSTRAINT commerce_operation_receipts_resource_id_check CHECK (funding_uuid(resource_id)),
    CONSTRAINT commerce_operation_receipts_status_check CHECK ((status = ANY (ARRAY['accepted'::text, 'rejected'::text])))
);


--
-- Name: cost_rate_lines; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE cost_rate_lines (
    cost_version_id text NOT NULL COLLATE pg_catalog."C",
    dimension text NOT NULL COLLATE pg_catalog."C",
    unit text NOT NULL COLLATE pg_catalog."C",
    numerator bigint NOT NULL,
    denominator bigint NOT NULL,
    CONSTRAINT cost_rate_lines_denominator_check CHECK (((denominator >= 1) AND (denominator <= 1000000))),
    CONSTRAINT cost_rate_lines_numerator_check CHECK ((numerator >= 0))
);


--
-- Name: credential_procurement_bindings; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE credential_procurement_bindings (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    credential_id text NOT NULL,
    channel_id text NOT NULL,
    account_id text NOT NULL,
    operation_id text NOT NULL,
    bound_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT credential_procurement_bindings_operation_id_check CHECK ((operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text))
);


--
-- Name: credit_orders; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE credit_orders (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    business_ref text NOT NULL,
    amount bigint NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    requester text NOT NULL,
    approver text,
    proof_ref text NOT NULL,
    journal_id text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT credit_orders_amount_check CHECK ((amount > 0)),
    CONSTRAINT credit_orders_check CHECK (((approver IS NULL) OR (approver <> requester))),
    CONSTRAINT credit_orders_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'approved'::text])))
);


--
-- Name: delivery_observations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE delivery_observations (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    stage text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT delivery_observations_stage_check CHECK ((stage = ANY (ARRAY['complete_local_write'::text, 'write_failed'::text, 'cancelled'::text])))
);


--
-- Name: finalization_records; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE finalization_records (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    event_id text NOT NULL,
    input_digest text NOT NULL,
    protocol_outcome text NOT NULL,
    transport_end_reason text NOT NULL,
    delivery_stage text NOT NULL,
    content_written boolean NOT NULL,
    finish_reason text DEFAULT ''::text NOT NULL,
    policy_version text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    attempt_id text,
    retail_usage_state text NOT NULL,
    supplier_usage_state text NOT NULL,
    retail_quantities_canonical bytea,
    retail_quantities_digest bytea,
    supplier_quantities_canonical bytea,
    supplier_quantities_digest bytea,
    CONSTRAINT finalization_retail_projection CHECK ((((retail_usage_state = 'confirmed_final'::text) AND (retail_quantities_canonical IS NOT NULL) AND (octet_length(retail_quantities_canonical) <= 8192) AND (octet_length(retail_quantities_digest) = 32)) OR ((retail_usage_state <> 'confirmed_final'::text) AND (retail_quantities_canonical IS NULL) AND (retail_quantities_digest IS NULL)))),
    CONSTRAINT finalization_retail_state CHECK ((retail_usage_state = ANY (ARRAY['confirmed_final'::text, 'provisional'::text, 'missing'::text, 'conflicting'::text]))),
    CONSTRAINT finalization_supplier_projection CHECK ((((supplier_usage_state = 'confirmed_final'::text) AND (supplier_quantities_canonical IS NOT NULL) AND (octet_length(supplier_quantities_canonical) <= 8192) AND (octet_length(supplier_quantities_digest) = 32)) OR ((supplier_usage_state <> 'confirmed_final'::text) AND (supplier_quantities_canonical IS NULL) AND (supplier_quantities_digest IS NULL)))),
    CONSTRAINT finalization_supplier_state CHECK ((supplier_usage_state = ANY (ARRAY['confirmed_final'::text, 'provisional'::text, 'missing'::text, 'conflicting'::text])))
);


--
-- Name: financial_idempotency; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE financial_idempotency (
    tenant_id text NOT NULL,
    operation text NOT NULL,
    key text NOT NULL,
    request_hmac text NOT NULL,
    resource_id text NOT NULL,
    created_txid bigint DEFAULT txid_current() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT financial_idempotency_operation_check CHECK ((operation = ANY (ARRAY['credit'::text, 'refund'::text])))
);


--
-- Name: funding_consumption_refund_lots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_consumption_refund_lots (
    tenant_id text NOT NULL,
    refund_order_id text NOT NULL,
    settlement_id text NOT NULL,
    allocation_index bigint NOT NULL,
    lot_id text NOT NULL,
    refund_index bigint NOT NULL,
    amount bigint NOT NULL,
    CONSTRAINT funding_consumption_refund_lots_amount_check CHECK ((amount > 0)),
    CONSTRAINT funding_consumption_refund_lots_refund_index_check CHECK ((refund_index > 0))
);


--
-- Name: funding_lot_balances; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_lot_balances (
    lot_id text NOT NULL,
    tenant_id text NOT NULL,
    available_amount bigint NOT NULL,
    held_amount bigint NOT NULL,
    consumed_amount bigint NOT NULL,
    revision bigint NOT NULL,
    CONSTRAINT funding_lot_balances_available_amount_check CHECK ((available_amount >= 0)),
    CONSTRAINT funding_lot_balances_consumed_amount_check CHECK ((consumed_amount >= 0)),
    CONSTRAINT funding_lot_balances_held_amount_check CHECK ((held_amount >= 0)),
    CONSTRAINT funding_lot_balances_revision_check CHECK ((revision > 0))
);


--
-- Name: funding_lot_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_lot_events (
    id text NOT NULL,
    tenant_id text NOT NULL,
    lot_id text NOT NULL,
    operation_id text NOT NULL,
    journal_id text NOT NULL,
    kind text NOT NULL,
    revision bigint NOT NULL,
    delta_available bigint NOT NULL,
    delta_held bigint NOT NULL,
    delta_consumed bigint NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT funding_lot_events_kind_check CHECK ((kind = ANY (ARRAY['funding'::text, 'reserve'::text, 'finish'::text, 'refund'::text]))),
    CONSTRAINT funding_lot_events_revision_check CHECK ((revision > 0))
);


--
-- Name: funding_lots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_lots (
    id text NOT NULL,
    tenant_id text NOT NULL,
    funding_receipt_id text NOT NULL,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    refund_class text NOT NULL,
    issued_amount bigint NOT NULL,
    fifo_at timestamp with time zone NOT NULL,
    CONSTRAINT funding_lots_check CHECK ((id = funding_receipt_id)),
    CONSTRAINT funding_lots_check1 CHECK (((source_kind = 'sandbox_capture'::text) = (refund_class = 'sandbox_original'::text))),
    CONSTRAINT funding_lots_issued_amount_check CHECK ((issued_amount > 0)),
    CONSTRAINT funding_lots_refund_class_check CHECK ((refund_class = ANY (ARRAY['nonrefundable'::text, 'sandbox_original'::text]))),
    CONSTRAINT funding_lots_source_kind_check CHECK ((source_kind = ANY (ARRAY['manual_credit'::text, 'voucher_grant'::text, 'sandbox_capture'::text, 'wallet_opening'::text])))
);


--
-- Name: funding_receipts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_receipts (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    amount bigint NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    journal_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT funding_receipts_amount_check CHECK ((amount > 0)),
    CONSTRAINT funding_receipts_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT funding_receipts_id_check CHECK (funding_uuid(id)),
    CONSTRAINT funding_receipts_source_kind_check CHECK ((source_kind = ANY (ARRAY['manual_credit'::text, 'voucher_grant'::text, 'sandbox_capture'::text, 'wallet_opening'::text])))
);


--
-- Name: funding_reservation_lots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_reservation_lots (
    tenant_id text NOT NULL,
    reservation_id text NOT NULL,
    request_id text NOT NULL,
    lot_id text NOT NULL,
    allocation_index bigint NOT NULL,
    amount bigint NOT NULL,
    CONSTRAINT funding_reservation_lots_allocation_index_check CHECK ((allocation_index > 0)),
    CONSTRAINT funding_reservation_lots_amount_check CHECK ((amount > 0))
);


--
-- Name: funding_settlement_lots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE funding_settlement_lots (
    tenant_id text NOT NULL,
    settlement_id text NOT NULL,
    reservation_id text NOT NULL,
    allocation_index bigint NOT NULL,
    lot_id text NOT NULL,
    charged_amount bigint NOT NULL,
    released_amount bigint NOT NULL,
    CONSTRAINT funding_settlement_lots_charged_amount_check CHECK ((charged_amount >= 0)),
    CONSTRAINT funding_settlement_lots_released_amount_check CHECK ((released_amount >= 0))
);


--
-- Name: identity_challenges; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE identity_challenges (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    subject_id uuid NOT NULL,
    purpose text NOT NULL,
    token_digest text NOT NULL,
    user_id text,
    password_revision bigint,
    registration_email_ciphertext text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    revoked_at timestamp with time zone,
    CONSTRAINT identity_challenges_check CHECK ((expires_at > created_at)),
    CONSTRAINT identity_challenges_check1 CHECK (((consumed_at IS NULL) OR (revoked_at IS NULL))),
    CONSTRAINT identity_challenges_check2 CHECK ((((purpose = 'registration'::text) AND (user_id IS NULL) AND (password_revision IS NULL)) OR ((purpose = 'password_reset'::text) AND (user_id IS NOT NULL) AND (password_revision IS NOT NULL) AND (registration_email_ciphertext IS NULL)))),
    CONSTRAINT identity_challenges_id_check CHECK ((id <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT identity_challenges_password_revision_check CHECK ((password_revision > 0)),
    CONSTRAINT identity_challenges_purpose_check CHECK ((purpose = ANY (ARRAY['registration'::text, 'password_reset'::text]))),
    CONSTRAINT identity_challenges_token_digest_check CHECK ((token_digest ~ '^[0-9a-f]{64}$'::text))
);


--
-- Name: identity_initialization; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE identity_initialization (
    singleton boolean DEFAULT true NOT NULL,
    initialized_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    initialized_by text NOT NULL,
    CONSTRAINT identity_initialization_singleton_check CHECK (singleton)
);


--
-- Name: identity_mail_outbox; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE identity_mail_outbox (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    challenge_id uuid NOT NULL,
    payload_ciphertext text,
    state text DEFAULT 'pending'::text NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    claim_generation bigint DEFAULT 0 NOT NULL,
    lease_until timestamp with time zone,
    sent_at timestamp with time zone,
    last_error_code text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT identity_mail_outbox_attempt_count_check CHECK (((attempt_count >= 0) AND (attempt_count <= 5))),
    CONSTRAINT identity_mail_outbox_check CHECK ((claim_generation = attempt_count)),
    CONSTRAINT identity_mail_outbox_check1 CHECK (((state = ANY (ARRAY['pending'::text, 'sending'::text])) = (payload_ciphertext IS NOT NULL))),
    CONSTRAINT identity_mail_outbox_check2 CHECK (((state = 'sending'::text) = (lease_until IS NOT NULL))),
    CONSTRAINT identity_mail_outbox_check3 CHECK (((state = 'sent'::text) = (sent_at IS NOT NULL))),
    CONSTRAINT identity_mail_outbox_check4 CHECK (((state <> 'pending'::text) OR (attempt_count < 5))),
    CONSTRAINT identity_mail_outbox_check5 CHECK (((state <> ALL (ARRAY['sending'::text, 'sent'::text])) OR (attempt_count > 0))),
    CONSTRAINT identity_mail_outbox_id_check CHECK ((id <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT identity_mail_outbox_last_error_code_check CHECK ((last_error_code = ANY (ARRAY[''::text, 'mail_configuration'::text, 'mail_payload_invalid'::text, 'mail_rejected'::text, 'mail_temporary'::text, 'mail_network'::text, 'mail_tls'::text, 'mail_tls_required'::text, 'mail_decrypt'::text, 'mail_attempts_exhausted'::text, 'mail_proof_inactive'::text]))),
    CONSTRAINT identity_mail_outbox_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'sending'::text, 'sent'::text, 'dead'::text, 'cancelled'::text])))
);


--
-- Name: identity_mail_subjects; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE identity_mail_subjects (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    purpose text NOT NULL,
    email_digest text NOT NULL,
    window_started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    window_count integer DEFAULT 0 NOT NULL,
    next_issue_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT identity_mail_subjects_email_digest_check CHECK ((email_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT identity_mail_subjects_id_check CHECK ((id <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT identity_mail_subjects_purpose_check CHECK ((purpose = ANY (ARRAY['registration'::text, 'password_reset'::text]))),
    CONSTRAINT identity_mail_subjects_window_count_check CHECK (((window_count >= 0) AND (window_count <= 5)))
);


--
-- Name: journal_lines; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE journal_lines (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    journal_id text NOT NULL,
    account_id text NOT NULL,
    signed_delta bigint NOT NULL
);


--
-- Name: journals; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE journals (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    kind text NOT NULL,
    reference text NOT NULL,
    actor text NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    created_txid bigint DEFAULT txid_current() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT journals_currency_check CHECK ((currency = 'CNY'::text))
);


--
-- Name: ledger_accounts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE ledger_accounts (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text,
    kind text NOT NULL,
    balance bigint DEFAULT 0 NOT NULL,
    CONSTRAINT ledger_accounts_check CHECK ((((tenant_id IS NOT NULL) AND (kind = ANY (ARRAY['AVAILABLE'::text, 'HELD'::text])) AND (balance >= 0)) OR ((tenant_id IS NULL) AND (kind = ANY (ARRAY['FUNDING_CLEARING'::text, 'REVENUE'::text]))) OR ((tenant_id IS NULL) AND (kind = ANY (ARRAY['OPERATING_GRANT_CLEARING'::text, 'SANDBOX_FUNDING_CLEARING'::text, 'WALLET_OPENING_CLEARING'::text])) AND (balance <= 0)))),
    CONSTRAINT ledger_accounts_kind_check CHECK ((kind = ANY (ARRAY['AVAILABLE'::text, 'HELD'::text, 'FUNDING_CLEARING'::text, 'REVENUE'::text, 'OPERATING_GRANT_CLEARING'::text, 'SANDBOX_FUNDING_CLEARING'::text, 'WALLET_OPENING_CLEARING'::text])))
);


--
-- Name: media_attempts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_attempts (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    provider_reference text,
    supplier_rate_per_million bigint NOT NULL,
    delivery_state text DEFAULT 'prepared'::text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_attempts_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_attempts_delivery_state_check CHECK ((delivery_state = ANY (ARRAY['prepared'::text, 'sending'::text, 'completed'::text, 'failed'::text, 'unknown'::text]))),
    CONSTRAINT media_attempts_id_check CHECK (media_uuid(id)),
    CONSTRAINT media_attempts_supplier_rate_per_million_check CHECK ((supplier_rate_per_million >= 0))
);


--
-- Name: media_model_profiles; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_model_profiles (
    model_key text NOT NULL COLLATE pg_catalog."C",
    operation text NOT NULL,
    profile_version text NOT NULL,
    unit text NOT NULL,
    rate_per_million bigint NOT NULL,
    supplier_rate_per_million bigint NOT NULL,
    max_quantity bigint NOT NULL,
    max_result_bytes bigint NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_model_profiles_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_model_profiles_max_quantity_check CHECK ((max_quantity > 0)),
    CONSTRAINT media_model_profiles_max_result_bytes_check CHECK ((max_result_bytes > 0)),
    CONSTRAINT media_model_profiles_model_key_check CHECK ((((length(model_key) >= 1) AND (length(model_key) <= 256)) AND (model_key !~ '[[:space:]]'::text))),
    CONSTRAINT media_model_profiles_operation_check CHECK ((operation = ANY (ARRAY['image_generation'::text, 'tts'::text, 'video_generation'::text, 'realtime_turn'::text]))),
    CONSTRAINT media_model_profiles_profile_version_check CHECK (media_uuid(profile_version)),
    CONSTRAINT media_model_profiles_rate_per_million_check CHECK ((rate_per_million >= 0)),
    CONSTRAINT media_model_profiles_supplier_rate_per_million_check CHECK ((supplier_rate_per_million >= 0)),
    CONSTRAINT media_model_profiles_unit_check CHECK ((unit = ANY (ARRAY['image'::text, 'character'::text, 'task'::text, 'realtime_byte'::text])))
);


--
-- Name: media_observations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_observations (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    attempt_id text NOT NULL,
    source text NOT NULL,
    source_event_id text NOT NULL,
    usage_state text NOT NULL,
    quantity bigint,
    canonical bytea NOT NULL,
    digest bytea NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_observations_canonical_check CHECK (((octet_length(canonical) >= 1) AND (octet_length(canonical) <= 8192))),
    CONSTRAINT media_observations_check CHECK (((octet_length(digest) = 32) AND (digest = sha256(canonical)))),
    CONSTRAINT media_observations_check1 CHECK ((((usage_state = 'confirmed_final'::text) AND (quantity IS NOT NULL) AND (quantity >= 0)) OR ((usage_state <> 'confirmed_final'::text) AND (quantity IS NULL)))),
    CONSTRAINT media_observations_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_observations_id_check CHECK (media_uuid(id)),
    CONSTRAINT media_observations_source_check CHECK ((source = ANY (ARRAY['adapter'::text, 'approved_reconciliation'::text]))),
    CONSTRAINT media_observations_usage_state_check CHECK ((usage_state = ANY (ARRAY['confirmed_final'::text, 'missing'::text, 'unknown'::text, 'conflicting'::text])))
);


--
-- Name: media_requests; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_requests (
    tenant_id text NOT NULL,
    id text NOT NULL,
    key_id text NOT NULL,
    owner_id text NOT NULL,
    model_key text NOT NULL COLLATE pg_catalog."C",
    operation text NOT NULL,
    profile_version text NOT NULL,
    idempotency_key text NOT NULL,
    request_digest bytea NOT NULL,
    quantity_bound bigint NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    deadline timestamp(6) with time zone NOT NULL,
    state text DEFAULT 'reserved'::text NOT NULL,
    CONSTRAINT media_requests_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_requests_deadline_check CHECK (isfinite(deadline)),
    CONSTRAINT media_requests_id_check CHECK (media_uuid(id)),
    CONSTRAINT media_requests_idempotency_key_check CHECK (((length(idempotency_key) >= 1) AND (length(idempotency_key) <= 200))),
    CONSTRAINT media_requests_operation_check CHECK ((operation = ANY (ARRAY['image_generation'::text, 'tts'::text, 'video_generation'::text, 'realtime_turn'::text]))),
    CONSTRAINT media_requests_profile_version_check CHECK (media_uuid(profile_version)),
    CONSTRAINT media_requests_quantity_bound_check CHECK ((quantity_bound > 0)),
    CONSTRAINT media_requests_request_digest_check CHECK ((octet_length(request_digest) = 32)),
    CONSTRAINT media_requests_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'pending'::text, 'settled'::text, 'released'::text])))
);


--
-- Name: media_reservation_lots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_reservation_lots (
    tenant_id text NOT NULL,
    reservation_id text NOT NULL,
    allocation_index bigint NOT NULL,
    lot_id text NOT NULL,
    amount bigint NOT NULL,
    CONSTRAINT media_reservation_lots_allocation_index_check CHECK ((allocation_index > 0)),
    CONSTRAINT media_reservation_lots_amount_check CHECK ((amount > 0))
);


--
-- Name: media_reservations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_reservations (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    amount bigint NOT NULL,
    rate_per_million bigint NOT NULL,
    quantity_bound bigint NOT NULL,
    status text DEFAULT 'reserved'::text NOT NULL,
    deadline timestamp(6) with time zone NOT NULL,
    journal_id text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_reservations_amount_check CHECK ((amount >= 0)),
    CONSTRAINT media_reservations_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_reservations_deadline_check CHECK (isfinite(deadline)),
    CONSTRAINT media_reservations_id_check CHECK (media_uuid(id)),
    CONSTRAINT media_reservations_quantity_bound_check CHECK ((quantity_bound > 0)),
    CONSTRAINT media_reservations_rate_per_million_check CHECK ((rate_per_million >= 0)),
    CONSTRAINT media_reservations_status_check CHECK ((status = ANY (ARRAY['reserved'::text, 'pending_reconciliation'::text, 'settled'::text, 'released'::text])))
);


--
-- Name: media_result_artifacts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_result_artifacts (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    attempt_id text NOT NULL,
    operation text NOT NULL,
    media_type text NOT NULL,
    body bytea NOT NULL,
    sha256 bytea NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    expires_at timestamp(6) with time zone NOT NULL,
    CONSTRAINT media_result_artifacts_body_check CHECK (((octet_length(body) >= 1) AND (octet_length(body) <= 67108864))),
    CONSTRAINT media_result_artifacts_check CHECK (((octet_length(sha256) = 32) AND (sha256 = sha256(body)))),
    CONSTRAINT media_result_artifacts_check1 CHECK ((isfinite(expires_at) AND (expires_at > created_at))),
    CONSTRAINT media_result_artifacts_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_result_artifacts_media_type_check CHECK ((((length(media_type) >= 1) AND (length(media_type) <= 128)) AND (media_type !~ '[[:space:]]'::text))),
    CONSTRAINT media_result_artifacts_operation_check CHECK ((operation = ANY (ARRAY['image_generation'::text, 'tts'::text])))
);


--
-- Name: media_settlement_lots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_settlement_lots (
    tenant_id text NOT NULL,
    settlement_id text NOT NULL,
    reservation_id text NOT NULL,
    allocation_index bigint NOT NULL,
    lot_id text NOT NULL,
    charged_amount bigint NOT NULL,
    released_amount bigint NOT NULL,
    CONSTRAINT media_settlement_lots_charged_amount_check CHECK ((charged_amount >= 0)),
    CONSTRAINT media_settlement_lots_released_amount_check CHECK ((released_amount >= 0))
);


--
-- Name: media_settlements; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_settlements (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    reservation_id text NOT NULL,
    attempt_id text NOT NULL,
    charged_amount bigint NOT NULL,
    raw_amount numeric(100,0) NOT NULL,
    released_amount numeric(100,0) NOT NULL,
    supplier_cost_status text NOT NULL,
    supplier_cost_amount bigint,
    status text NOT NULL,
    reason text NOT NULL,
    journal_id text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_settlements_charged_amount_check CHECK ((charged_amount >= 0)),
    CONSTRAINT media_settlements_check CHECK ((((supplier_cost_status = 'known'::text) AND (supplier_cost_amount IS NOT NULL)) OR ((supplier_cost_status = 'unknown'::text) AND (supplier_cost_amount IS NULL)))),
    CONSTRAINT media_settlements_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_settlements_id_check CHECK (media_uuid(id)),
    CONSTRAINT media_settlements_raw_amount_check CHECK ((raw_amount >= (0)::numeric)),
    CONSTRAINT media_settlements_released_amount_check CHECK ((released_amount >= (0)::numeric)),
    CONSTRAINT media_settlements_status_check CHECK ((status = ANY (ARRAY['settled'::text, 'released'::text]))),
    CONSTRAINT media_settlements_supplier_cost_amount_check CHECK ((supplier_cost_amount >= 0)),
    CONSTRAINT media_settlements_supplier_cost_status_check CHECK ((supplier_cost_status = ANY (ARRAY['known'::text, 'unknown'::text])))
);


--
-- Name: media_task_artifacts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_task_artifacts (
    tenant_id text NOT NULL,
    task_id text NOT NULL,
    artifact_reference text NOT NULL,
    media_type text NOT NULL,
    byte_length bigint NOT NULL,
    sha256 bytea NOT NULL,
    body bytea,
    expires_at timestamp(6) with time zone NOT NULL,
    status text DEFAULT 'available'::text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_task_artifacts_artifact_reference_check CHECK ((((length(artifact_reference) >= 1) AND (length(artifact_reference) <= 512)) AND (artifact_reference !~ '://'::text))),
    CONSTRAINT media_task_artifacts_byte_length_check CHECK ((byte_length > 0)),
    CONSTRAINT media_task_artifacts_check CHECK (((body IS NULL) OR ((octet_length(body) = byte_length) AND (octet_length(body) <= 67108864) AND (sha256(body) = sha256)))),
    CONSTRAINT media_task_artifacts_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_task_artifacts_expires_at_check CHECK (isfinite(expires_at)),
    CONSTRAINT media_task_artifacts_media_type_check CHECK ((media_type ~ '^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$'::text)),
    CONSTRAINT media_task_artifacts_sha256_check CHECK ((octet_length(sha256) = 32)),
    CONSTRAINT media_task_artifacts_status_check CHECK ((status = ANY (ARRAY['available'::text, 'expired'::text])))
);


--
-- Name: media_task_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_task_events (
    tenant_id text NOT NULL,
    task_id text NOT NULL,
    event_id text NOT NULL,
    sequence bigint NOT NULL,
    source text NOT NULL,
    kind text NOT NULL,
    observed_at timestamp(6) with time zone NOT NULL,
    provider_reference text,
    artifact_reference text,
    artifact_media_type text,
    artifact_bytes bigint,
    artifact_sha256 bytea,
    artifact_expires_at timestamp(6) with time zone,
    failure_code text,
    canonical bytea NOT NULL,
    digest bytea NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_task_events_canonical_check CHECK (((octet_length(canonical) >= 1) AND (octet_length(canonical) <= 16384))),
    CONSTRAINT media_task_events_check CHECK (((octet_length(digest) = 32) AND (digest = sha256(canonical)))),
    CONSTRAINT media_task_events_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_task_events_event_id_check CHECK (media_uuid(event_id)),
    CONSTRAINT media_task_events_kind_check CHECK ((kind = ANY (ARRAY['submission_accepted'::text, 'submission_unknown'::text, 'status_queued'::text, 'status_running'::text, 'status_succeeded'::text, 'status_failed'::text, 'status_cancelled'::text, 'cancel_rejected'::text, 'artifact_expired'::text]))),
    CONSTRAINT media_task_events_observed_at_check CHECK (isfinite(observed_at)),
    CONSTRAINT media_task_events_provider_reference_check CHECK (((provider_reference IS NULL) OR (provider_reference !~ '://'::text))),
    CONSTRAINT media_task_events_sequence_check CHECK ((sequence > 0)),
    CONSTRAINT media_task_events_source_check CHECK ((source = ANY (ARRAY['adapter'::text, 'signed_callback'::text, 'original_query'::text, 'approved_reconciliation'::text])))
);


--
-- Name: media_tasks; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE media_tasks (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    owner_id text NOT NULL,
    attempt_id text,
    reservation_id text NOT NULL,
    source_id text NOT NULL,
    settlement_id text,
    model_id text NOT NULL,
    profile_version text NOT NULL,
    schema_id text NOT NULL,
    schema_digest bytea NOT NULL,
    variant_key text DEFAULT 'default'::text NOT NULL,
    idempotency_key text NOT NULL,
    submit_fingerprint bytea NOT NULL,
    input_digest bytea NOT NULL,
    status text DEFAULT 'prepared'::text NOT NULL,
    observation_state text DEFAULT 'unobserved'::text NOT NULL,
    cancel_state text DEFAULT 'none'::text NOT NULL,
    artifact_state text DEFAULT 'none'::text NOT NULL,
    cancel_resume_state text,
    provider_reference text,
    lease_id text,
    lease_worker_id text,
    lease_kind text,
    lease_until timestamp(6) with time zone,
    submit_deadline timestamp(6) with time zone NOT NULL,
    observe_deadline timestamp(6) with time zone NOT NULL,
    result_deadline timestamp(6) with time zone NOT NULL,
    artifact_deadline timestamp(6) with time zone NOT NULL,
    max_artifact_bytes bigint NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT media_tasks_artifact_deadline_check CHECK (isfinite(artifact_deadline)),
    CONSTRAINT media_tasks_artifact_state_check CHECK ((artifact_state = ANY (ARRAY['none'::text, 'available'::text, 'expired'::text]))),
    CONSTRAINT media_tasks_cancel_resume_state_check CHECK (((cancel_resume_state IS NULL) OR (cancel_resume_state = ANY (ARRAY['queued'::text, 'running'::text, 'submission_unknown'::text])))),
    CONSTRAINT media_tasks_cancel_state_check CHECK ((cancel_state = ANY (ARRAY['none'::text, 'requested'::text, 'accepted'::text, 'rejected'::text]))),
    CONSTRAINT media_tasks_check CHECK (((submit_deadline <= observe_deadline) AND (result_deadline >= observe_deadline) AND (artifact_deadline >= result_deadline))),
    CONSTRAINT media_tasks_check1 CHECK ((((lease_id IS NULL) AND (lease_worker_id IS NULL) AND (lease_kind IS NULL) AND (lease_until IS NULL)) OR ((lease_id IS NOT NULL) AND (lease_worker_id IS NOT NULL) AND (lease_kind IS NOT NULL) AND (lease_until IS NOT NULL)))),
    CONSTRAINT media_tasks_check2 CHECK (((status = 'cancel_requested'::text) = (cancel_state = 'requested'::text))),
    CONSTRAINT media_tasks_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT media_tasks_id_check CHECK (media_uuid(id)),
    CONSTRAINT media_tasks_idempotency_key_check CHECK ((idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'::text)),
    CONSTRAINT media_tasks_input_digest_check CHECK ((octet_length(input_digest) = 32)),
    CONSTRAINT media_tasks_lease_id_check CHECK (((lease_id IS NULL) OR media_uuid(lease_id))),
    CONSTRAINT media_tasks_lease_kind_check CHECK (((lease_kind IS NULL) OR (lease_kind = ANY (ARRAY['submit'::text, 'observe'::text, 'cancel'::text, 'expire'::text])))),
    CONSTRAINT media_tasks_lease_until_check CHECK (((lease_until IS NULL) OR isfinite(lease_until))),
    CONSTRAINT media_tasks_lease_worker_id_check CHECK (((lease_worker_id IS NULL) OR media_uuid(lease_worker_id))),
    CONSTRAINT media_tasks_max_artifact_bytes_check CHECK ((max_artifact_bytes > 0)),
    CONSTRAINT media_tasks_observation_state_check CHECK ((observation_state = ANY (ARRAY['unobserved'::text, 'fresh'::text, 'blocked'::text, 'expired'::text]))),
    CONSTRAINT media_tasks_observe_deadline_check CHECK (isfinite(observe_deadline)),
    CONSTRAINT media_tasks_provider_reference_check CHECK (((provider_reference IS NULL) OR (provider_reference !~ '://'::text))),
    CONSTRAINT media_tasks_result_deadline_check CHECK (isfinite(result_deadline)),
    CONSTRAINT media_tasks_schema_digest_check CHECK ((octet_length(schema_digest) = 32)),
    CONSTRAINT media_tasks_status_check CHECK ((status = ANY (ARRAY['prepared'::text, 'submitting'::text, 'queued'::text, 'running'::text, 'cancel_requested'::text, 'submission_unknown'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text]))),
    CONSTRAINT media_tasks_submit_deadline_check CHECK (isfinite(submit_deadline)),
    CONSTRAINT media_tasks_submit_fingerprint_check CHECK ((octet_length(submit_fingerprint) = 32)),
    CONSTRAINT media_tasks_variant_key_check CHECK ((variant_key = 'default'::text))
);


--
-- Name: member_model_grants; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE member_model_grants (
    tenant_id text NOT NULL,
    user_id text NOT NULL,
    model_id text NOT NULL
);


--
-- Name: memberships; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE memberships (
    tenant_id text NOT NULL,
    user_id text NOT NULL,
    role text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    allow_all_models boolean DEFAULT false NOT NULL,
    CONSTRAINT memberships_role_check CHECK ((role = ANY (ARRAY['owner'::text, 'member'::text, 'billing_viewer'::text]))),
    CONSTRAINT memberships_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text])))
);


--
-- Name: meter_event_quantities; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE meter_event_quantities (
    tenant_id text NOT NULL,
    event_id text NOT NULL,
    dimension text NOT NULL,
    unit text NOT NULL,
    status text NOT NULL,
    quantity bigint,
    CONSTRAINT meter_event_quantities_check CHECK ((((status = ANY (ARRAY['confirmed_final'::text, 'provisional'::text])) AND (quantity IS NOT NULL)) OR ((status = ANY (ARRAY['missing'::text, 'conflicting'::text])) AND (quantity IS NULL)))),
    CONSTRAINT meter_event_quantities_dimension_check CHECK ((dimension = ANY (ARRAY['text_input_token'::text, 'text_output_token'::text]))),
    CONSTRAINT meter_event_quantities_quantity_check CHECK ((quantity >= 0)),
    CONSTRAINT meter_event_quantities_status_check CHECK ((status = ANY (ARRAY['confirmed_final'::text, 'provisional'::text, 'missing'::text, 'conflicting'::text]))),
    CONSTRAINT meter_event_quantities_unit_check CHECK ((unit = 'token'::text))
);


--
-- Name: meter_schema_dimensions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE meter_schema_dimensions (
    schema_id text NOT NULL COLLATE pg_catalog."C",
    dimension text NOT NULL COLLATE pg_catalog."C",
    unit text NOT NULL COLLATE pg_catalog."C",
    maximum_quantity bigint NOT NULL,
    retail boolean NOT NULL,
    supplier boolean NOT NULL,
    CONSTRAINT meter_schema_dimensions_check CHECK ((retail OR supplier)),
    CONSTRAINT meter_schema_dimensions_check1 CHECK (((dimension = ANY (ARRAY['text_input_token'::text, 'text_output_token'::text])) AND (unit = 'token'::text))),
    CONSTRAINT meter_schema_dimensions_maximum_quantity_check CHECK ((maximum_quantity >= 0))
);


--
-- Name: meter_schemas; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE meter_schemas (
    id text NOT NULL COLLATE pg_catalog."C",
    name text NOT NULL COLLATE pg_catalog."C",
    revision bigint NOT NULL,
    registry_version smallint NOT NULL,
    wire_version smallint NOT NULL,
    canonical bytea NOT NULL,
    schema_digest bytea NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT meter_schemas_canonical_check CHECK (((octet_length(canonical) >= 1) AND (octet_length(canonical) <= 16384))),
    CONSTRAINT meter_schemas_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT meter_schemas_name_check CHECK ((name ~ '^[a-z][a-z0-9_]{0,99}$'::text)),
    CONSTRAINT meter_schemas_registry_version_check CHECK ((registry_version = 1)),
    CONSTRAINT meter_schemas_revision_check CHECK ((revision > 0)),
    CONSTRAINT meter_schemas_schema_digest_check CHECK ((octet_length(schema_digest) = 32)),
    CONSTRAINT meter_schemas_wire_version_check CHECK ((wire_version = 1))
);


--
-- Name: metering_event_conflicts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE metering_event_conflicts (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    event_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    tenant_id text,
    candidate_canonical bytea,
    candidate_digest bytea
);


--
-- Name: metering_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE metering_events (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    attempt_id text NOT NULL,
    source text NOT NULL,
    source_event_id text NOT NULL,
    happened_at timestamp with time zone NOT NULL,
    received_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    profile_version text,
    schema_id text,
    schema_digest bytea,
    variant_key text DEFAULT 'default'::text,
    retail_usage_state text,
    supplier_usage_state text,
    created_xid xid8 DEFAULT pg_current_xact_id(),
    plan_digest bytea,
    protocol_outcome text,
    transport_end_reason text,
    delivery_stage text,
    content_written boolean,
    finish_reason text,
    observation_canonical bytea,
    observation_digest bytea,
    CONSTRAINT metering_events_check CHECK (((content_written IS NOT NULL) AND (content_written = (delivery_stage = 'content'::text)))),
    CONSTRAINT metering_events_delivery_stage_check CHECK ((delivery_stage = ANY (ARRAY['none'::text, 'committed'::text, 'content'::text, 'unknown'::text]))),
    CONSTRAINT metering_events_finish_reason_check CHECK (((finish_reason IS NOT NULL) AND (octet_length(finish_reason) <= 80))),
    CONSTRAINT metering_events_observation_canonical_check CHECK (((octet_length(observation_canonical) >= 1) AND (octet_length(observation_canonical) <= 32768))),
    CONSTRAINT metering_events_observation_digest_check CHECK ((octet_length(observation_digest) = 32)),
    CONSTRAINT metering_events_plan_digest_check CHECK ((octet_length(plan_digest) = 32)),
    CONSTRAINT metering_events_protocol_outcome_check CHECK ((protocol_outcome = ANY (ARRAY['completed'::text, 'failed'::text, 'incomplete'::text, 'cancelled'::text, 'unknown'::text]))),
    CONSTRAINT metering_events_retail_usage_state_check CHECK ((retail_usage_state = ANY (ARRAY['confirmed_final'::text, 'provisional'::text, 'missing'::text, 'conflicting'::text]))),
    CONSTRAINT metering_events_schema_digest_check CHECK ((octet_length(schema_digest) = 32)),
    CONSTRAINT metering_events_source_check CHECK ((source = ANY (ARRAY['adapter'::text, 'retrieval'::text, 'gateway'::text, 'approved_reconciliation'::text]))),
    CONSTRAINT metering_events_supplier_usage_state_check CHECK ((supplier_usage_state = ANY (ARRAY['confirmed_final'::text, 'provisional'::text, 'missing'::text, 'conflicting'::text]))),
    CONSTRAINT metering_events_transport_end_reason_check CHECK ((transport_end_reason = ANY (ARRAY['done'::text, 'eof'::text, 'provider_error'::text, 'platform_error'::text, 'platform_timeout'::text, 'client_cancelled'::text, 'connection_error'::text, 'unknown'::text, 'not_sent'::text, 'rejected_no_consumption'::text]))),
    CONSTRAINT metering_events_variant_key_check CHECK (((variant_key IS NULL) OR (variant_key = 'default'::text)))
);


--
-- Name: mfa_recovery_codes; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE mfa_recovery_codes (
    user_id text NOT NULL,
    digest text NOT NULL,
    used_at timestamp with time zone
);


--
-- Name: model_bindings; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE model_bindings (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    route_policy_version_id text NOT NULL,
    channel_id text NOT NULL,
    channel_version_id text NOT NULL,
    upstream_model text NOT NULL,
    priority integer DEFAULT 0 NOT NULL,
    weight integer NOT NULL,
    max_inflight integer NOT NULL,
    variant_key text DEFAULT 'default'::text NOT NULL COLLATE pg_catalog."C",
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    CONSTRAINT model_bindings_max_inflight_check CHECK (((max_inflight > 0) AND (max_inflight <= 1000))),
    CONSTRAINT model_bindings_variant_key_check CHECK ((variant_key = 'default'::text)),
    CONSTRAINT model_bindings_weight_check CHECK (((weight > 0) AND (weight <= 1000000)))
);


--
-- Name: model_capability_bound_lines; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE model_capability_bound_lines (
    profile_version text NOT NULL COLLATE pg_catalog."C",
    variant_key text NOT NULL COLLATE pg_catalog."C",
    side text NOT NULL COLLATE pg_catalog."C",
    dimension text NOT NULL COLLATE pg_catalog."C",
    unit text NOT NULL COLLATE pg_catalog."C",
    quantity bigint NOT NULL,
    CONSTRAINT model_capability_bound_lines_quantity_check CHECK ((quantity >= 0)),
    CONSTRAINT model_capability_bound_lines_side_check CHECK ((side = ANY (ARRAY['retail'::text, 'supplier'::text])))
);


--
-- Name: model_capability_variants; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE model_capability_variants (
    profile_version text NOT NULL COLLATE pg_catalog."C",
    variant_key text NOT NULL COLLATE pg_catalog."C",
    parameters jsonb NOT NULL,
    CONSTRAINT model_capability_variants_parameters_check CHECK (((jsonb_typeof(parameters) = 'object'::text) AND (octet_length((parameters)::text) <= 16384))),
    CONSTRAINT model_capability_variants_variant_key_check CHECK ((variant_key = 'default'::text))
);


--
-- Name: model_capability_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE model_capability_versions (
    id text NOT NULL,
    model_id text NOT NULL,
    profile jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    schema_id text NOT NULL COLLATE pg_catalog."C",
    schema_digest bytea NOT NULL,
    protocol text NOT NULL COLLATE pg_catalog."C",
    execution_kind text DEFAULT 'synchronous'::text NOT NULL COLLATE pg_catalog."C",
    contract_digest bytea NOT NULL,
    profile_digest bytea NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    CONSTRAINT model_capability_text CHECK (((protocol = ANY (ARRAY['openai_chat'::text, 'openai_responses'::text, 'anthropic_messages'::text, 'openai_embeddings'::text, 'jina_rerank'::text, 'gemini_generate_content'::text])) AND (execution_kind = 'synchronous'::text) AND (octet_length(schema_digest) = 32) AND (octet_length(contract_digest) = 32) AND (octet_length(profile_digest) = 32)))
);


--
-- Name: models; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE models (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    public_name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    profile_version text NOT NULL,
    active_revision bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    public_visible boolean DEFAULT false NOT NULL,
    public_revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT models_public_revision_check CHECK ((public_revision > 0))
);


--
-- Name: payment_cancel_intents; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_cancel_intents (
    order_id text NOT NULL,
    operation_id text NOT NULL,
    actor text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: payment_captures; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_captures (
    id text NOT NULL,
    order_id text NOT NULL,
    identity_id text NOT NULL,
    tenant_id text NOT NULL,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    provider_transaction_id text NOT NULL,
    amount bigint NOT NULL,
    wallet_credit_amount bigint NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    captured_at timestamp(6) with time zone NOT NULL,
    provider_order_revision bigint NOT NULL,
    verified_evidence_id text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    processing_generation bigint NOT NULL,
    CONSTRAINT payment_captures_amount_check CHECK ((amount > 0)),
    CONSTRAINT payment_captures_check CHECK ((wallet_credit_amount = amount)),
    CONSTRAINT payment_captures_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT payment_captures_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_captures_processing_generation_check CHECK ((processing_generation > 0)),
    CONSTRAINT payment_captures_provider_order_revision_check CHECK ((provider_order_revision > 0)),
    CONSTRAINT payment_captures_provider_transaction_id_check CHECK (payment_ref(provider_transaction_id))
);


--
-- Name: payment_command_observations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_command_observations (
    id text NOT NULL,
    outbox_id text NOT NULL,
    generation bigint NOT NULL,
    canonical_reply bytea NOT NULL,
    reply_digest text GENERATED ALWAYS AS (encode(sha256(canonical_reply), 'hex'::text)) STORED,
    observed_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_command_observations_canonical_reply_check CHECK (((octet_length(canonical_reply) >= 1) AND (octet_length(canonical_reply) <= 65536))),
    CONSTRAINT payment_command_observations_generation_check CHECK ((generation > 0)),
    CONSTRAINT payment_command_observations_id_check CHECK (funding_uuid(id))
);


--
-- Name: payment_command_outbox; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_command_outbox (
    id text NOT NULL,
    order_id text NOT NULL,
    kind text NOT NULL,
    external_command_id text,
    original_outbox_id text,
    canonical_parameters bytea,
    parameter_digest text GENERATED ALWAYS AS (encode(sha256(canonical_parameters), 'hex'::text)) STORED,
    state text DEFAULT 'pending'::text NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    current_query_id text,
    next_attempt_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    lease_until timestamp(6) with time zone,
    last_error_code text,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    last_submission_unknown_at timestamp(6) with time zone,
    CONSTRAINT payment_command_outbox_attempt_count_check CHECK ((attempt_count >= 0)),
    CONSTRAINT payment_command_outbox_check CHECK ((((kind = ANY (ARRAY['checkout'::text, 'cancel'::text])) AND (external_command_id IS NOT NULL) AND (canonical_parameters IS NOT NULL) AND ((octet_length(canonical_parameters) >= 1) AND (octet_length(canonical_parameters) <= 8192)) AND (original_outbox_id IS NULL) AND (attempt_count <= 10) AND (current_query_id IS NULL)) OR ((kind = 'query'::text) AND (external_command_id IS NULL) AND (canonical_parameters IS NULL) AND (original_outbox_id IS NOT NULL) AND (attempt_count <= 20)))),
    CONSTRAINT payment_command_outbox_check1 CHECK (((state = 'leased'::text) = (lease_until IS NOT NULL))),
    CONSTRAINT payment_command_outbox_check2 CHECK ((generation = attempt_count)),
    CONSTRAINT payment_command_outbox_external_command_id_check CHECK (funding_uuid(external_command_id)),
    CONSTRAINT payment_command_outbox_generation_check CHECK ((generation >= 0)),
    CONSTRAINT payment_command_outbox_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_command_outbox_kind_check CHECK ((kind = ANY (ARRAY['checkout'::text, 'cancel'::text, 'query'::text]))),
    CONSTRAINT payment_command_outbox_last_error_code_check CHECK (((last_error_code IS NULL) OR (last_error_code = ANY (ARRAY['network'::text, 'timeout'::text, 'auth'::text, 'protocol'::text, 'environment'::text, 'temporary'::text, 'permanent'::text, 'attempts_exhausted'::text])))),
    CONSTRAINT payment_command_outbox_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'retry_wait'::text, 'leased'::text, 'completed'::text, 'submission_unknown'::text, 'review_required'::text])))
);


--
-- Name: payment_effects; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_effects (
    capture_id text NOT NULL,
    identity_id text NOT NULL,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    provider_transaction_id text NOT NULL,
    effect_kind text DEFAULT 'capture'::text NOT NULL,
    effect_id text DEFAULT 'capture'::text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_effects_effect_id_check CHECK ((effect_id = 'capture'::text)),
    CONSTRAINT payment_effects_effect_kind_check CHECK ((effect_kind = 'capture'::text)),
    CONSTRAINT payment_effects_provider_transaction_id_check CHECK (payment_ref(provider_transaction_id))
);


--
-- Name: payment_event_conflicts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_event_conflicts (
    id text NOT NULL,
    inbox_id text NOT NULL,
    conflicting_digest text NOT NULL,
    received_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_event_conflicts_conflicting_digest_check CHECK ((conflicting_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_event_conflicts_id_check CHECK (funding_uuid(id))
);


--
-- Name: payment_evidence_processing; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_evidence_processing (
    evidence_id text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    lease_until timestamp(6) with time zone,
    last_error_code text,
    CONSTRAINT payment_evidence_processing_attempt_count_check CHECK (((attempt_count >= 0) AND (attempt_count <= 20))),
    CONSTRAINT payment_evidence_processing_check CHECK (((state = 'leased'::text) = (lease_until IS NOT NULL))),
    CONSTRAINT payment_evidence_processing_check1 CHECK ((generation = attempt_count)),
    CONSTRAINT payment_evidence_processing_generation_check CHECK ((generation >= 0)),
    CONSTRAINT payment_evidence_processing_last_error_code_check CHECK (((last_error_code IS NULL) OR (last_error_code = ANY (ARRAY['temporary'::text, 'permanent'::text, 'attempts_exhausted'::text])))),
    CONSTRAINT payment_evidence_processing_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'retry_wait'::text, 'leased'::text, 'processed'::text, 'processing_review'::text])))
);


--
-- Name: payment_inbox; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_inbox (
    id text NOT NULL,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    event_id text NOT NULL,
    canonical_body bytea NOT NULL,
    business_digest text GENERATED ALWAYS AS (encode(sha256(canonical_body), 'hex'::text)) STORED,
    verified_key_id text NOT NULL,
    received_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_inbox_canonical_body_check CHECK (((octet_length(canonical_body) >= 1) AND (octet_length(canonical_body) <= 65536))),
    CONSTRAINT payment_inbox_event_id_check CHECK (payment_ref(event_id)),
    CONSTRAINT payment_inbox_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_inbox_verified_key_id_check CHECK ((verified_key_id ~ '^[A-Za-z0-9_-]{1,32}$'::text))
);


--
-- Name: payment_order_capture_selections; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_order_capture_selections (
    order_id text NOT NULL,
    identity_id text NOT NULL,
    first_evidence_id text NOT NULL,
    selected_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: payment_orders; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_orders (
    id text NOT NULL,
    tenant_id text NOT NULL,
    creator text NOT NULL,
    sku_version_id text NOT NULL,
    display_name text NOT NULL,
    amount bigint NOT NULL,
    wallet_credit_amount bigint NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    payee_label text NOT NULL,
    merchant_order_id text NOT NULL,
    created_at timestamp(6) with time zone NOT NULL,
    expires_at timestamp(6) with time zone NOT NULL,
    CONSTRAINT payment_orders_amount_check CHECK ((amount > 0)),
    CONSTRAINT payment_orders_check CHECK (((wallet_credit_amount = amount) AND (expires_at = (created_at + '00:15:00'::interval)) AND isfinite(created_at) AND isfinite(expires_at))),
    CONSTRAINT payment_orders_check1 CHECK ((merchant_order_id = ((('mps1.'::text || environment_id) || '.'::text) || id))),
    CONSTRAINT payment_orders_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT payment_orders_display_name_check CHECK (payment_ref(display_name)),
    CONSTRAINT payment_orders_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_orders_payee_label_check CHECK (payment_ref(payee_label))
);


--
-- Name: payment_policy; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_policy (
    singleton boolean DEFAULT true NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT payment_policy_revision_check CHECK ((revision > 0)),
    CONSTRAINT payment_policy_singleton_check CHECK (singleton)
);


--
-- Name: payment_provider_revisions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_provider_revisions (
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    order_id text NOT NULL,
    provider_order_revision bigint NOT NULL,
    kind text NOT NULL,
    fact_digest text NOT NULL,
    evidence_id text NOT NULL,
    CONSTRAINT payment_provider_revisions_fact_digest_check CHECK ((fact_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_provider_revisions_kind_check CHECK ((kind = ANY (ARRAY['capture'::text, 'closed'::text]))),
    CONSTRAINT payment_provider_revisions_provider_order_revision_check CHECK ((provider_order_revision > 0))
);


--
-- Name: payment_query_attempts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_query_attempts (
    query_id text NOT NULL,
    supervisor_id text NOT NULL,
    created_generation bigint NOT NULL,
    canonical_request bytea NOT NULL,
    request_digest text GENERATED ALWAYS AS (encode(sha256(canonical_request), 'hex'::text)) STORED,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    mutation_generation bigint NOT NULL,
    CONSTRAINT payment_query_attempts_canonical_request_check CHECK (((octet_length(canonical_request) >= 1) AND (octet_length(canonical_request) <= 8192))),
    CONSTRAINT payment_query_attempts_created_generation_check CHECK ((created_generation > 0)),
    CONSTRAINT payment_query_attempts_mutation_generation_check CHECK ((mutation_generation > 0)),
    CONSTRAINT payment_query_attempts_query_id_check CHECK (funding_uuid(query_id))
);


--
-- Name: payment_query_conflicts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_query_conflicts (
    id text NOT NULL,
    verified_query_id text NOT NULL,
    conflicting_digest text NOT NULL,
    received_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_query_conflicts_conflicting_digest_check CHECK ((conflicting_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_query_conflicts_id_check CHECK (funding_uuid(id))
);


--
-- Name: payment_review_facts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_review_facts (
    id text NOT NULL,
    order_id text NOT NULL,
    evidence_id text NOT NULL,
    identity_id text,
    selection_order_id text,
    reason text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_review_facts_check CHECK (((selection_order_id IS NULL) OR (selection_order_id = order_id))),
    CONSTRAINT payment_review_facts_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_review_facts_order_id_check CHECK (funding_uuid(order_id)),
    CONSTRAINT payment_review_facts_reason_check CHECK ((reason = ANY (ARRAY['unknown_order'::text, 'capture_outside_window'::text, 'provider_time_invalid'::text, 'provider_revision_conflict'::text, 'amount_mismatch'::text, 'currency_mismatch'::text, 'transaction_conflict'::text, 'duplicate_order_capture'::text, 'funding_amount_overflow'::text, 'unsupported_effect_review'::text, 'processing_attempts_exhausted'::text, 'command_attempts_exhausted'::text, 'query_attempts_exhausted'::text, 'provider_protocol_invalid'::text])))
);


--
-- Name: payment_runtime_attestations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_runtime_attestations (
    singleton boolean DEFAULT true NOT NULL,
    environment_id text NOT NULL,
    database_oid oid NOT NULL,
    schema_oid oid NOT NULL,
    schema_owner_oid oid NOT NULL,
    app_role_oid oid NOT NULL,
    worker_role_oid oid NOT NULL,
    projector_role_oid oid NOT NULL,
    app_nonce_digest text NOT NULL,
    worker_nonce_digest text NOT NULL,
    proof_version integer NOT NULL,
    proof_canonical bytea NOT NULL,
    proof_sha256 text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_runtime_attestations_app_nonce_digest_check CHECK ((app_nonce_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_runtime_attestations_app_role_oid_check CHECK ((app_role_oid <> (0)::oid)),
    CONSTRAINT payment_runtime_attestations_check CHECK (((proof_sha256 ~ '^[0-9a-f]{64}$'::text) AND (proof_sha256 = encode(sha256(proof_canonical), 'hex'::text)))),
    CONSTRAINT payment_runtime_attestations_check1 CHECK (((app_role_oid <> worker_role_oid) AND (app_role_oid <> projector_role_oid) AND (worker_role_oid <> projector_role_oid) AND (app_role_oid <> schema_owner_oid) AND (worker_role_oid <> schema_owner_oid) AND (projector_role_oid <> schema_owner_oid))),
    CONSTRAINT payment_runtime_attestations_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT payment_runtime_attestations_database_oid_check CHECK ((database_oid <> (0)::oid)),
    CONSTRAINT payment_runtime_attestations_projector_role_oid_check CHECK ((projector_role_oid <> (0)::oid)),
    CONSTRAINT payment_runtime_attestations_proof_canonical_check CHECK (((octet_length(proof_canonical) >= 1) AND (octet_length(proof_canonical) <= 65536))),
    CONSTRAINT payment_runtime_attestations_proof_version_check CHECK ((proof_version = 1)),
    CONSTRAINT payment_runtime_attestations_schema_oid_check CHECK ((schema_oid <> (0)::oid)),
    CONSTRAINT payment_runtime_attestations_schema_owner_oid_check CHECK ((schema_owner_oid <> (0)::oid)),
    CONSTRAINT payment_runtime_attestations_singleton_check CHECK (singleton),
    CONSTRAINT payment_runtime_attestations_worker_nonce_digest_check CHECK ((worker_nonce_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_runtime_attestations_worker_role_oid_check CHECK ((worker_role_oid <> (0)::oid))
);


--
-- Name: payment_sandbox_environment; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_sandbox_environment (
    singleton boolean DEFAULT true NOT NULL,
    id text NOT NULL,
    merchant_id text NOT NULL,
    payee_label text NOT NULL,
    database_oid oid NOT NULL,
    schema_oid oid NOT NULL,
    nonce_digest text NOT NULL,
    app_role name NOT NULL,
    worker_role name NOT NULL,
    projector_role name NOT NULL,
    protocol_version integer DEFAULT 1 NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_sandbox_environment_check CHECK (((app_role <> worker_role) AND (app_role <> projector_role) AND (worker_role <> projector_role))),
    CONSTRAINT payment_sandbox_environment_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_sandbox_environment_merchant_id_check CHECK (payment_ref(merchant_id)),
    CONSTRAINT payment_sandbox_environment_nonce_digest_check CHECK ((nonce_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_sandbox_environment_payee_label_check CHECK (payment_ref(payee_label)),
    CONSTRAINT payment_sandbox_environment_protocol_version_check CHECK ((protocol_version = 1)),
    CONSTRAINT payment_sandbox_environment_singleton_check CHECK (singleton)
);


--
-- Name: payment_sku_changes; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_sku_changes (
    operation_id text NOT NULL,
    sku_id text NOT NULL,
    kind text NOT NULL,
    before_revision bigint NOT NULL,
    after_revision bigint NOT NULL,
    before_version_id text,
    after_version_id text,
    published_version_id text,
    before_enabled boolean NOT NULL,
    after_enabled boolean NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    before_operation_id text,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    CONSTRAINT payment_sku_changes_after_revision_check CHECK ((after_revision > 0)),
    CONSTRAINT payment_sku_changes_before_revision_check CHECK ((before_revision >= 0)),
    CONSTRAINT payment_sku_changes_check CHECK ((((kind = 'payment.sku.publish'::text) AND (published_version_id IS NOT NULL) AND (after_revision = (before_revision + 1))) OR ((kind = 'payment.sku.activate'::text) AND (published_version_id IS NULL) AND (after_revision = (before_revision +
CASE
    WHEN ((before_version_id IS DISTINCT FROM after_version_id) OR (before_enabled IS DISTINCT FROM after_enabled)) THEN 1
    ELSE 0
END))))),
    CONSTRAINT payment_sku_changes_kind_check CHECK ((kind = ANY (ARRAY['payment.sku.publish'::text, 'payment.sku.activate'::text])))
);


--
-- Name: payment_sku_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_sku_versions (
    id text NOT NULL,
    sku_id text NOT NULL,
    version bigint NOT NULL,
    display_name text NOT NULL,
    amount bigint NOT NULL,
    wallet_credit_amount bigint NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    payee_label text NOT NULL,
    checkout_ttl_seconds integer DEFAULT 900 NOT NULL,
    publish_operation_id text NOT NULL,
    created_by text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_sku_versions_amount_check CHECK ((amount > 0)),
    CONSTRAINT payment_sku_versions_check CHECK ((wallet_credit_amount = amount)),
    CONSTRAINT payment_sku_versions_checkout_ttl_seconds_check CHECK ((checkout_ttl_seconds = 900)),
    CONSTRAINT payment_sku_versions_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT payment_sku_versions_display_name_check CHECK (payment_ref(display_name)),
    CONSTRAINT payment_sku_versions_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_sku_versions_payee_label_check CHECK (payment_ref(payee_label)),
    CONSTRAINT payment_sku_versions_version_check CHECK ((version > 0))
);


--
-- Name: payment_skus; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_skus (
    id text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    last_version bigint DEFAULT 0 NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    current_version_id text,
    created_by text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    last_change_operation_id text,
    CONSTRAINT payment_skus_check CHECK (((NOT enabled) OR (current_version_id IS NOT NULL))),
    CONSTRAINT payment_skus_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_skus_last_version_check CHECK ((last_version >= 0)),
    CONSTRAINT payment_skus_revision_check CHECK ((revision > 0))
);


--
-- Name: payment_transaction_identities; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_transaction_identities (
    id text NOT NULL,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    provider_transaction_id text NOT NULL,
    order_id text NOT NULL,
    tenant_id text NOT NULL,
    amount bigint NOT NULL,
    currency text DEFAULT 'CNY'::text NOT NULL,
    captured_at timestamp(6) with time zone NOT NULL,
    provider_order_revision bigint NOT NULL,
    evidence_id text NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    processing_generation bigint NOT NULL,
    CONSTRAINT payment_transaction_identities_amount_check CHECK ((amount > 0)),
    CONSTRAINT payment_transaction_identities_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT payment_transaction_identities_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_transaction_identities_processing_generation_check CHECK ((processing_generation > 0)),
    CONSTRAINT payment_transaction_identities_provider_order_revision_check CHECK ((provider_order_revision > 0)),
    CONSTRAINT payment_transaction_identities_provider_transaction_id_check CHECK (payment_ref(provider_transaction_id))
);


--
-- Name: payment_verified_evidence; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_verified_evidence (
    id text NOT NULL,
    inbox_id text,
    verified_query_id text,
    environment_id text NOT NULL,
    merchant_id text NOT NULL,
    order_id text NOT NULL,
    kind text NOT NULL,
    provider_transaction_id text,
    amount bigint,
    currency text,
    happened_at timestamp(6) with time zone,
    provider_order_revision bigint,
    fact_digest text NOT NULL,
    verified_at timestamp(6) with time zone NOT NULL,
    CONSTRAINT payment_verified_evidence_amount_check CHECK ((amount > 0)),
    CONSTRAINT payment_verified_evidence_check CHECK (((inbox_id IS NOT NULL) <> (verified_query_id IS NOT NULL))),
    CONSTRAINT payment_verified_evidence_check1 CHECK (((kind <> 'capture'::text) OR ((provider_transaction_id IS NOT NULL) AND (amount IS NOT NULL) AND (currency IS NOT NULL) AND (happened_at IS NOT NULL) AND (provider_order_revision IS NOT NULL)))),
    CONSTRAINT payment_verified_evidence_check2 CHECK (((kind <> 'closed'::text) OR ((happened_at IS NOT NULL) AND (provider_order_revision IS NOT NULL)))),
    CONSTRAINT payment_verified_evidence_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT payment_verified_evidence_fact_digest_check CHECK ((fact_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT payment_verified_evidence_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_verified_evidence_kind_check CHECK ((kind = ANY (ARRAY['capture'::text, 'closed'::text, 'observation'::text, 'unsupported'::text]))),
    CONSTRAINT payment_verified_evidence_order_id_check CHECK (funding_uuid(order_id)),
    CONSTRAINT payment_verified_evidence_provider_order_revision_check CHECK ((provider_order_revision > 0)),
    CONSTRAINT payment_verified_evidence_provider_transaction_id_check CHECK (((provider_transaction_id IS NULL) OR payment_ref(provider_transaction_id)))
);


--
-- Name: payment_verified_queries; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE payment_verified_queries (
    id text NOT NULL,
    query_id text NOT NULL,
    canonical_body bytea NOT NULL,
    snapshot_digest text GENERATED ALWAYS AS (encode(sha256(canonical_body), 'hex'::text)) STORED,
    verified_key_id text NOT NULL,
    received_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_verified_queries_canonical_body_check CHECK (((octet_length(canonical_body) >= 1) AND (octet_length(canonical_body) <= 65536))),
    CONSTRAINT payment_verified_queries_id_check CHECK (funding_uuid(id)),
    CONSTRAINT payment_verified_queries_verified_key_id_check CHECK ((verified_key_id ~ '^[A-Za-z0-9_-]{1,32}$'::text))
);


--
-- Name: periodic_budget_allocations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE periodic_budget_allocations (
    id text NOT NULL,
    control_id text NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    origin text NOT NULL,
    origin_source_id text NOT NULL,
    window_id text,
    admitted_at timestamp with time zone,
    upper_amount bigint NOT NULL,
    baseline numeric(100,0) NOT NULL,
    source_held numeric(100,0) DEFAULT 0 NOT NULL,
    source_spent numeric(100,0) NOT NULL,
    held_amount numeric(100,0) DEFAULT 0 NOT NULL,
    spent_amount numeric(100,0) DEFAULT 0 NOT NULL,
    state text DEFAULT 'held'::text NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    last_event_id text,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp with time zone DEFAULT pb_now() NOT NULL,
    CONSTRAINT periodic_budget_allocations_baseline_check CHECK ((baseline >= (0)::numeric)),
    CONSTRAINT periodic_budget_allocations_check CHECK ((((origin = 'new'::text) AND (window_id IS NOT NULL) AND (admitted_at IS NOT NULL) AND isfinite(admitted_at)) OR ((origin <> 'new'::text) AND (window_id IS NULL) AND (admitted_at IS NULL)))),
    CONSTRAINT periodic_budget_allocations_check1 CHECK (((subject_type <> 'customer_request'::text) OR ((baseline = (0)::numeric) AND (state <> 'disputed'::text)))),
    CONSTRAINT periodic_budget_allocations_check2 CHECK (((origin <> 'legacy_catchup'::text) OR (subject_type = 'supplier_attempt'::text))),
    CONSTRAINT periodic_budget_allocations_held_amount_check CHECK ((held_amount >= (0)::numeric)),
    CONSTRAINT periodic_budget_allocations_id_check CHECK (pb_uuid(id)),
    CONSTRAINT periodic_budget_allocations_origin_check CHECK ((origin = ANY (ARRAY['new'::text, 'legacy_import'::text, 'legacy_catchup'::text]))),
    CONSTRAINT periodic_budget_allocations_origin_source_id_check CHECK (pb_uuid(origin_source_id)),
    CONSTRAINT periodic_budget_allocations_revision_check CHECK ((revision >= 0)),
    CONSTRAINT periodic_budget_allocations_source_held_check CHECK ((source_held >= (0)::numeric)),
    CONSTRAINT periodic_budget_allocations_source_spent_check CHECK ((source_spent >= (0)::numeric)),
    CONSTRAINT periodic_budget_allocations_spent_amount_check CHECK ((spent_amount >= (0)::numeric)),
    CONSTRAINT periodic_budget_allocations_state_check CHECK ((state = ANY (ARRAY['held'::text, 'disputed'::text, 'settled'::text, 'released'::text]))),
    CONSTRAINT periodic_budget_allocations_subject_id_check CHECK (pb_uuid(subject_id)),
    CONSTRAINT periodic_budget_allocations_subject_type_check CHECK ((subject_type = ANY (ARRAY['customer_request'::text, 'supplier_attempt'::text]))),
    CONSTRAINT periodic_budget_allocations_upper_amount_check CHECK ((upper_amount >= 0))
);


--
-- Name: periodic_budget_configuration_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE periodic_budget_configuration_versions (
    control_id text NOT NULL,
    revision bigint NOT NULL,
    operation_id text NOT NULL,
    period_kind text NOT NULL,
    enabled boolean NOT NULL,
    limit_amount bigint NOT NULL,
    actor text NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp with time zone DEFAULT pb_now() NOT NULL,
    CONSTRAINT periodic_budget_configuration_versions_limit_amount_check CHECK ((limit_amount >= 0)),
    CONSTRAINT periodic_budget_configuration_versions_period_kind_check CHECK ((period_kind = ANY (ARRAY['utc_day'::text, 'utc_month'::text]))),
    CONSTRAINT periodic_budget_configuration_versions_revision_check CHECK ((revision > 0))
);


--
-- Name: periodic_budget_controls; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE periodic_budget_controls (
    id text NOT NULL,
    scope text NOT NULL,
    tenant_id text,
    key_id text,
    account_id text,
    period_kind text NOT NULL,
    enabled boolean NOT NULL,
    limit_amount bigint NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    tracking_started_at timestamp with time zone DEFAULT pb_now() NOT NULL,
    effective_from timestamp with time zone NOT NULL,
    max_observed_window_start timestamp with time zone NOT NULL,
    held_amount numeric(100,0) DEFAULT 0 NOT NULL,
    spent_amount numeric(100,0) DEFAULT 0 NOT NULL,
    event_seq bigint DEFAULT 0 NOT NULL,
    last_event_id text,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    configuration_operation_id text DEFAULT (gen_random_uuid())::text NOT NULL,
    CONSTRAINT periodic_budget_controls_check CHECK ((((scope = 'tenant'::text) AND (tenant_id IS NOT NULL) AND (key_id IS NULL) AND (account_id IS NULL)) OR ((scope = 'key'::text) AND (tenant_id IS NOT NULL) AND (key_id IS NOT NULL) AND (account_id IS NULL)) OR ((scope = 'procurement'::text) AND (tenant_id IS NULL) AND (key_id IS NULL) AND (account_id IS NOT NULL)))),
    CONSTRAINT periodic_budget_controls_check1 CHECK ((isfinite(tracking_started_at) AND isfinite(effective_from) AND isfinite(max_observed_window_start))),
    CONSTRAINT periodic_budget_controls_check2 CHECK ((effective_from = pb_end(period_kind, pb_start(period_kind, tracking_started_at)))),
    CONSTRAINT periodic_budget_controls_event_seq_check CHECK ((event_seq >= 0)),
    CONSTRAINT periodic_budget_controls_held_amount_check CHECK ((held_amount >= (0)::numeric)),
    CONSTRAINT periodic_budget_controls_id_check CHECK (pb_uuid(id)),
    CONSTRAINT periodic_budget_controls_limit_amount_check CHECK ((limit_amount >= 0)),
    CONSTRAINT periodic_budget_controls_period_kind_check CHECK ((period_kind = ANY (ARRAY['utc_day'::text, 'utc_month'::text]))),
    CONSTRAINT periodic_budget_controls_revision_check CHECK ((revision > 0)),
    CONSTRAINT periodic_budget_controls_scope_check CHECK ((scope = ANY (ARRAY['tenant'::text, 'key'::text, 'procurement'::text]))),
    CONSTRAINT periodic_budget_controls_spent_amount_check CHECK ((spent_amount >= (0)::numeric))
);


--
-- Name: periodic_budget_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE periodic_budget_events (
    id text NOT NULL,
    control_id text NOT NULL,
    allocation_id text NOT NULL,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    seq bigint NOT NULL,
    allocation_before_revision bigint NOT NULL,
    window_before_revision bigint,
    before_held numeric(100,0) NOT NULL,
    before_spent numeric(100,0) NOT NULL,
    after_held numeric(100,0) NOT NULL,
    after_spent numeric(100,0) NOT NULL,
    after_source_spent numeric(100,0) NOT NULL,
    after_state text NOT NULL,
    held_delta numeric(100,0) NOT NULL,
    spent_delta numeric(100,0) NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp with time zone DEFAULT pb_now() NOT NULL,
    CONSTRAINT periodic_budget_events_after_state_check CHECK ((after_state = ANY (ARRAY['held'::text, 'disputed'::text, 'settled'::text, 'released'::text]))),
    CONSTRAINT periodic_budget_events_id_check CHECK (pb_uuid(id)),
    CONSTRAINT periodic_budget_events_source_id_check CHECK (pb_uuid(source_id)),
    CONSTRAINT periodic_budget_events_source_kind_check CHECK ((source_kind = ANY (ARRAY['reservation'::text, 'settlement'::text, 'procurement_event'::text, 'legacy_import'::text])))
);


--
-- Name: periodic_budget_operations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE periodic_budget_operations (
    operation_id text NOT NULL,
    kind text NOT NULL,
    actor text NOT NULL,
    scope text NOT NULL,
    target text NOT NULL,
    resource_id text NOT NULL,
    revision bigint NOT NULL,
    safe_input jsonb NOT NULL,
    input_fingerprint text NOT NULL,
    result_snapshot jsonb NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp with time zone DEFAULT pb_now() NOT NULL,
    CONSTRAINT periodic_budget_operations_input_fingerprint_check CHECK ((input_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT periodic_budget_operations_kind_check CHECK ((kind = ANY (ARRAY['tenant_periodic_budget.configure'::text, 'key_periodic_budget.configure'::text, 'procurement_periodic_budget.configure'::text]))),
    CONSTRAINT periodic_budget_operations_operation_id_check CHECK (pb_uuid(operation_id)),
    CONSTRAINT periodic_budget_operations_resource_id_check CHECK (pb_uuid(resource_id)),
    CONSTRAINT periodic_budget_operations_result_snapshot_check CHECK (((jsonb_typeof(result_snapshot) = 'object'::text) AND (octet_length((result_snapshot)::text) <= 8192))),
    CONSTRAINT periodic_budget_operations_revision_check CHECK ((revision > 0)),
    CONSTRAINT periodic_budget_operations_safe_input_check CHECK (pb_safe_input(safe_input)),
    CONSTRAINT periodic_budget_operations_scope_check CHECK (((scope = 'platform'::text) OR pb_uuid(scope))),
    CONSTRAINT periodic_budget_operations_target_check CHECK ((octet_length(target) <= 200))
);


--
-- Name: periodic_budget_windows; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE periodic_budget_windows (
    id text NOT NULL,
    control_id text NOT NULL,
    start_at timestamp with time zone NOT NULL,
    end_at timestamp with time zone NOT NULL,
    held_amount numeric(100,0) DEFAULT 0 NOT NULL,
    spent_amount numeric(100,0) DEFAULT 0 NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    last_event_id text,
    CONSTRAINT periodic_budget_windows_held_amount_check CHECK ((held_amount >= (0)::numeric)),
    CONSTRAINT periodic_budget_windows_id_check CHECK (pb_uuid(id)),
    CONSTRAINT periodic_budget_windows_revision_check CHECK ((revision >= 0)),
    CONSTRAINT periodic_budget_windows_spent_amount_check CHECK ((spent_amount >= (0)::numeric))
);


--
-- Name: platform_probe_attempts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE platform_probe_attempts (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    actor text NOT NULL,
    channel_id text NOT NULL,
    credential_id text NOT NULL,
    channel_version_id text NOT NULL,
    model_id text NOT NULL,
    profile_version text NOT NULL,
    upstream_model text NOT NULL,
    status text DEFAULT 'sending'::text NOT NULL,
    cost_basis text DEFAULT 'unknown'::text NOT NULL,
    cost_amount numeric(100,0),
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    completed_at timestamp with time zone,
    schema_id text,
    schema_digest bytea,
    variant_key text DEFAULT 'default'::text,
    usage_state text,
    quantities_canonical bytea,
    quantities_digest bytea,
    CONSTRAINT platform_probe_attempts_check CHECK ((((usage_state = 'confirmed_final'::text) AND (quantities_canonical IS NOT NULL) AND (quantities_digest IS NOT NULL)) OR ((usage_state IS DISTINCT FROM 'confirmed_final'::text) AND (quantities_canonical IS NULL) AND (quantities_digest IS NULL)))),
    CONSTRAINT platform_probe_attempts_status_check CHECK ((status = ANY (ARRAY['sending'::text, 'completed'::text, 'failed'::text, 'unknown'::text]))),
    CONSTRAINT platform_probe_attempts_usage_state_check CHECK (((usage_state IS NULL) OR (usage_state = ANY (ARRAY['confirmed_final'::text, 'provisional'::text, 'missing'::text, 'conflicting'::text])))),
    CONSTRAINT platform_probe_attempts_variant_key_check CHECK (((variant_key IS NULL) OR (variant_key = 'default'::text)))
);


--
-- Name: price_rate_lines; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE price_rate_lines (
    price_version_id text NOT NULL COLLATE pg_catalog."C",
    dimension text NOT NULL COLLATE pg_catalog."C",
    unit text NOT NULL COLLATE pg_catalog."C",
    numerator bigint NOT NULL,
    denominator bigint NOT NULL,
    CONSTRAINT price_rate_lines_denominator_check CHECK (((denominator >= 1) AND (denominator <= 1000000))),
    CONSTRAINT price_rate_lines_numerator_check CHECK ((numerator >= 0))
);


--
-- Name: pricing_operations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE pricing_operations (
    operation_id text NOT NULL,
    kind text NOT NULL,
    actor text NOT NULL,
    scope text NOT NULL,
    scope_tenant_id text,
    target text NOT NULL,
    resource_id text NOT NULL,
    revision bigint,
    safe_input jsonb NOT NULL,
    input_fingerprint text NOT NULL,
    result_schema smallint DEFAULT 1 NOT NULL,
    result_snapshot jsonb NOT NULL,
    created_txid bigint DEFAULT txid_current() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT pricing_operations_check CHECK ((((kind = 'tenant_pricing.assign'::text) AND (scope_tenant_id IS NOT NULL) AND (scope = scope_tenant_id) AND (resource_id = scope_tenant_id)) OR ((kind <> 'tenant_pricing.assign'::text) AND (scope = 'platform'::text) AND (scope_tenant_id IS NULL)))),
    CONSTRAINT pricing_operations_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT pricing_operations_input_fingerprint_check CHECK ((input_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT pricing_operations_kind_check CHECK ((kind = ANY (ARRAY['price_group.create'::text, 'price_group.update'::text, 'group_price.publish'::text, 'tenant_pricing.assign'::text, 'default_price.publish'::text, 'cost_price.publish'::text]))),
    CONSTRAINT pricing_operations_result_schema_check CHECK ((result_schema = 1)),
    CONSTRAINT pricing_operations_result_snapshot_check CHECK (((jsonb_typeof(result_snapshot) = 'object'::text) AND (octet_length((result_snapshot)::text) <= 8192))),
    CONSTRAINT pricing_operations_revision_check CHECK ((((kind = ANY (ARRAY['group_price.publish'::text, 'default_price.publish'::text, 'cost_price.publish'::text])) AND (revision IS NULL)) OR ((kind <> ALL (ARRAY['group_price.publish'::text, 'default_price.publish'::text, 'cost_price.publish'::text])) AND (revision IS NOT NULL)))),
    CONSTRAINT pricing_operations_safe_input_check CHECK ((jsonb_typeof(safe_input) = 'object'::text)),
    CONSTRAINT pricing_operations_target_check CHECK (((octet_length(target) >= 1) AND (octet_length(target) <= 200)))
);


--
-- Name: procurement_accounts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_accounts (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    label text NOT NULL,
    provider text NOT NULL,
    state text NOT NULL,
    limit_amount bigint NOT NULL,
    held_amount numeric(100,0) DEFAULT 0 NOT NULL,
    spent_amount numeric(100,0) DEFAULT 0 NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT procurement_accounts_held_amount_check CHECK ((held_amount >= (0)::numeric)),
    CONSTRAINT procurement_accounts_label_check CHECK ((((octet_length(label) >= 1) AND (octet_length(label) <= 100)) AND (label !~ '[[:cntrl:]]'::text) AND (label ~ '[^[:space:]]'::text))),
    CONSTRAINT procurement_accounts_limit_amount_check CHECK ((limit_amount >= 0)),
    CONSTRAINT procurement_accounts_provider_check CHECK ((((octet_length(provider) >= 1) AND (octet_length(provider) <= 100)) AND (provider !~ '[[:cntrl:]]'::text) AND (provider ~ '[^[:space:]]'::text))),
    CONSTRAINT procurement_accounts_revision_check CHECK ((revision > 0)),
    CONSTRAINT procurement_accounts_spent_amount_check CHECK ((spent_amount >= (0)::numeric)),
    CONSTRAINT procurement_accounts_state_check CHECK ((state = ANY (ARRAY['active'::text, 'paused'::text])))
);


--
-- Name: procurement_balance_observations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_balance_observations (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    account_id text NOT NULL,
    operation_id text NOT NULL,
    actor text NOT NULL,
    status text NOT NULL,
    amount bigint,
    currency text NOT NULL,
    source text NOT NULL,
    semantic text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    provider_as_of timestamp with time zone,
    evidence_ref text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT procurement_balance_observations_amount_check CHECK ((amount >= 0)),
    CONSTRAINT procurement_balance_observations_check CHECK ((((status = 'known'::text) AND (amount IS NOT NULL)) OR ((status <> 'known'::text) AND (amount IS NULL)))),
    CONSTRAINT procurement_balance_observations_currency_check CHECK ((currency = 'CNY'::text)),
    CONSTRAINT procurement_balance_observations_evidence_ref_check CHECK ((((octet_length(evidence_ref) >= 1) AND (octet_length(evidence_ref) <= 256)) AND (evidence_ref !~ '[[:cntrl:]]'::text))),
    CONSTRAINT procurement_balance_observations_semantic_check CHECK ((((octet_length(semantic) >= 1) AND (octet_length(semantic) <= 100)) AND (semantic !~ '[[:cntrl:]]'::text))),
    CONSTRAINT procurement_balance_observations_source_check CHECK ((((octet_length(source) >= 1) AND (octet_length(source) <= 100)) AND (source !~ '[[:cntrl:]]'::text))),
    CONSTRAINT procurement_balance_observations_status_check CHECK ((status = ANY (ARRAY['known'::text, 'unknown'::text, 'unsupported'::text, 'error'::text])))
);


--
-- Name: procurement_budget_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_budget_events (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    event_key text NOT NULL,
    account_id text NOT NULL,
    attempt_id text NOT NULL,
    held_delta numeric(100,0) NOT NULL,
    spent_delta numeric(100,0) NOT NULL,
    type text NOT NULL,
    evidence_ref text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    pb_created_xid xid8,
    CONSTRAINT procurement_budget_events_event_key_check CHECK (((octet_length(event_key) >= 1) AND (octet_length(event_key) <= 400))),
    CONSTRAINT procurement_budget_events_evidence_ref_check CHECK (((octet_length(evidence_ref) <= 256) AND (evidence_ref !~ '[[:cntrl:]]'::text))),
    CONSTRAINT procurement_budget_events_type_check CHECK ((((octet_length(type) >= 1) AND (octet_length(type) <= 80)) AND (type !~ '[[:cntrl:]]'::text)))
);


--
-- Name: procurement_commands; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_commands (
    operation_id text NOT NULL,
    actor text NOT NULL,
    kind text NOT NULL,
    target_id text NOT NULL,
    input_digest text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT procurement_commands_check CHECK ((valid_procurement_command_snapshot(kind, before_state) AND valid_procurement_command_snapshot(kind, after_state))),
    CONSTRAINT procurement_commands_check1 CHECK ((((status = 'applied'::text) AND (reason = ''::text) AND (jsonb_typeof(after_state) = 'object'::text)) OR ((status = 'rejected'::text) AND (reason <> ''::text) AND (after_state = 'null'::jsonb)))),
    CONSTRAINT procurement_commands_input_digest_check CHECK ((input_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT procurement_commands_kind_check CHECK ((kind = ANY (ARRAY['account_create'::text, 'account_configure'::text, 'credential_bind'::text, 'balance_observe'::text]))),
    CONSTRAINT procurement_commands_operation_id_check CHECK ((operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text)),
    CONSTRAINT procurement_commands_reason_check CHECK ((reason = ANY (ARRAY[''::text, 'not_found'::text, 'revision_conflict'::text, 'already_bound'::text, 'credential_revoked'::text]))),
    CONSTRAINT procurement_commands_status_check CHECK ((status = ANY (ARRAY['applied'::text, 'rejected'::text])))
);


--
-- Name: procurement_cost_proposals; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_cost_proposals (
    id text NOT NULL,
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    amount bigint NOT NULL,
    basis text NOT NULL,
    evidence_ref text NOT NULL,
    reason text NOT NULL,
    requester text NOT NULL,
    expected_reservation_revision bigint NOT NULL,
    expected_evidence_count bigint NOT NULL,
    state text DEFAULT 'proposed'::text NOT NULL,
    approver text,
    approval_operation_id text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    approved_at timestamp with time zone,
    CONSTRAINT procurement_cost_proposals_amount_check CHECK ((amount >= 0)),
    CONSTRAINT procurement_cost_proposals_approval_operation_id_check CHECK (((approval_operation_id IS NULL) OR (approval_operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text))),
    CONSTRAINT procurement_cost_proposals_basis_check CHECK ((basis = ANY (ARRAY['contract'::text, 'invoice'::text]))),
    CONSTRAINT procurement_cost_proposals_check CHECK ((((state = 'proposed'::text) AND (approver IS NULL) AND (approval_operation_id IS NULL) AND (approved_at IS NULL)) OR ((state = 'approved'::text) AND (approver IS NOT NULL) AND (approver <> requester) AND (approval_operation_id IS NOT NULL) AND (approval_operation_id <> id) AND (approved_at IS NOT NULL)))),
    CONSTRAINT procurement_cost_proposals_evidence_ref_check CHECK ((((octet_length(evidence_ref) >= 1) AND (octet_length(evidence_ref) <= 256)) AND (evidence_ref !~ '[[:cntrl:]]'::text))),
    CONSTRAINT procurement_cost_proposals_expected_evidence_count_check CHECK ((expected_evidence_count >= 0)),
    CONSTRAINT procurement_cost_proposals_expected_reservation_revision_check CHECK ((expected_reservation_revision > 0)),
    CONSTRAINT procurement_cost_proposals_id_check CHECK ((id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text)),
    CONSTRAINT procurement_cost_proposals_reason_check CHECK ((((octet_length(reason) >= 1) AND (octet_length(reason) <= 400)) AND (reason !~ '[[:cntrl:]]'::text))),
    CONSTRAINT procurement_cost_proposals_state_check CHECK ((state = ANY (ARRAY['proposed'::text, 'approved'::text])))
);


--
-- Name: procurement_evidence_identities; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_evidence_identities (
    attempt_id text NOT NULL,
    evidence_digest text NOT NULL,
    event_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT procurement_evidence_identities_event_id_check CHECK ((event_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text)),
    CONSTRAINT procurement_evidence_identities_evidence_digest_check CHECK ((evidence_digest ~ '^[0-9a-f]{64}$'::text))
);


--
-- Name: procurement_reservations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE procurement_reservations (
    attempt_id text NOT NULL,
    account_id text NOT NULL,
    binding_id text NOT NULL,
    upper_amount numeric(100,0) NOT NULL,
    held_amount numeric(100,0) NOT NULL,
    spent_amount numeric(100,0) DEFAULT 0 NOT NULL,
    state text NOT NULL,
    anomaly boolean DEFAULT false NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    pb_created_xid xid8,
    pb_control text,
    pb_admitted_at timestamp with time zone,
    CONSTRAINT procurement_reservations_check CHECK (((state <> ALL (ARRAY['prepared'::text, 'sending'::text])) OR ((held_amount = upper_amount) AND (spent_amount = (0)::numeric) AND (NOT anomaly)))),
    CONSTRAINT procurement_reservations_check1 CHECK (((state <> ALL (ARRAY['settled'::text, 'released'::text])) OR (held_amount = (0)::numeric))),
    CONSTRAINT procurement_reservations_check2 CHECK (((state <> 'released'::text) OR (spent_amount = (0)::numeric))),
    CONSTRAINT procurement_reservations_held_amount_check CHECK ((held_amount >= (0)::numeric)),
    CONSTRAINT procurement_reservations_revision_check CHECK ((revision > 0)),
    CONSTRAINT procurement_reservations_spent_amount_check CHECK ((spent_amount >= (0)::numeric)),
    CONSTRAINT procurement_reservations_state_check CHECK ((state = ANY (ARRAY['prepared'::text, 'sending'::text, 'unknown'::text, 'settled'::text, 'released'::text, 'disputed'::text]))),
    CONSTRAINT procurement_reservations_upper_amount_check CHECK (((upper_amount >= (0)::numeric) AND (upper_amount <= ('9223372036854775807'::bigint)::numeric)))
);


--
-- Name: projection_outbox; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE projection_outbox (
    event_id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    kind text NOT NULL,
    schema_version integer DEFAULT 1 NOT NULL,
    sanitized_payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: projection_receipts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE projection_receipts (
    consumer text NOT NULL,
    event_id text NOT NULL,
    processed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: provider_cost_corrections; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE provider_cost_corrections (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    operation_id text NOT NULL,
    amount bigint NOT NULL,
    basis text NOT NULL,
    evidence_ref text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT provider_cost_corrections_amount_check CHECK ((amount >= 0)),
    CONSTRAINT provider_cost_corrections_basis_check CHECK ((basis = ANY (ARRAY['contract'::text, 'invoice'::text])))
);


--
-- Name: realtime_events; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE realtime_events (
    tenant_id text NOT NULL,
    session_id text NOT NULL,
    event_id text NOT NULL,
    turn_id text,
    type text NOT NULL,
    payload_bytes integer NOT NULL,
    payload_digest bytea NOT NULL,
    canonical bytea NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT realtime_events_canonical_check CHECK (((octet_length(canonical) >= 1) AND (octet_length(canonical) <= 16384))),
    CONSTRAINT realtime_events_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT realtime_events_event_id_check CHECK (media_uuid(event_id)),
    CONSTRAINT realtime_events_payload_bytes_check CHECK (((payload_bytes >= 0) AND (payload_bytes <= 1048576))),
    CONSTRAINT realtime_events_payload_digest_check CHECK ((octet_length(payload_digest) = 32)),
    CONSTRAINT realtime_events_type_check CHECK ((type = ANY (ARRAY['input.text'::text, 'input.audio'::text, 'response.create'::text, 'response.cancel'::text, 'heartbeat'::text, 'status'::text])))
);


--
-- Name: realtime_sessions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE realtime_sessions (
    tenant_id text NOT NULL,
    id text NOT NULL,
    actor_id text NOT NULL,
    api_key_id text NOT NULL,
    model_id text NOT NULL,
    profile_version text NOT NULL,
    route_id text NOT NULL,
    origin text NOT NULL,
    state text DEFAULT 'opening'::text NOT NULL,
    provider_session_id text,
    expires_at timestamp(6) with time zone NOT NULL,
    max_turns integer DEFAULT 100 NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT realtime_sessions_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT realtime_sessions_expires_at_check CHECK (isfinite(expires_at)),
    CONSTRAINT realtime_sessions_id_check CHECK (media_uuid(id)),
    CONSTRAINT realtime_sessions_max_turns_check CHECK (((max_turns >= 1) AND (max_turns <= 100))),
    CONSTRAINT realtime_sessions_origin_check CHECK ((origin ~ '^https?://[^/?#:@]+(:[0-9]+)?$'::text)),
    CONSTRAINT realtime_sessions_provider_session_id_check CHECK (((provider_session_id IS NULL) OR ((length(provider_session_id) >= 1) AND (length(provider_session_id) <= 512)))),
    CONSTRAINT realtime_sessions_state_check CHECK ((state = ANY (ARRAY['opening'::text, 'open'::text, 'draining'::text, 'closed'::text])))
);


--
-- Name: realtime_turns; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE realtime_turns (
    tenant_id text NOT NULL,
    id text NOT NULL,
    session_id text NOT NULL,
    ordinal bigint NOT NULL,
    request_id text NOT NULL,
    reservation_id text NOT NULL,
    attempt_id text NOT NULL,
    state text DEFAULT 'reserved'::text NOT NULL,
    provider_handle text,
    dispatch_unknown boolean DEFAULT false NOT NULL,
    cancel_requested boolean DEFAULT false NOT NULL,
    response_reference text,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT realtime_turns_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT realtime_turns_id_check CHECK (media_uuid(id)),
    CONSTRAINT realtime_turns_ordinal_check CHECK ((ordinal > 0)),
    CONSTRAINT realtime_turns_provider_handle_check CHECK (((provider_handle IS NULL) OR ((length(provider_handle) >= 1) AND (length(provider_handle) <= 512)))),
    CONSTRAINT realtime_turns_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'sending'::text, 'receiving'::text, 'finalizing'::text, 'pending'::text, 'settled'::text, 'released'::text])))
);


--
-- Name: reconciliation_cases; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE reconciliation_cases (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    reason text NOT NULL,
    state text DEFAULT 'open'::text NOT NULL,
    evidence_ref text,
    proposal_reason text,
    requester text,
    approver text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    proposed_observation_canonical bytea,
    proposed_observation_digest bytea,
    CONSTRAINT reconciliation_cases_check CHECK (((approver IS NULL) OR (approver <> requester))),
    CONSTRAINT reconciliation_cases_check1 CHECK ((((proposed_observation_canonical IS NULL) AND (proposed_observation_digest IS NULL)) OR (((octet_length(proposed_observation_canonical) >= 1) AND (octet_length(proposed_observation_canonical) <= 8192)) AND (octet_length(proposed_observation_digest) = 32)))),
    CONSTRAINT reconciliation_cases_state_check CHECK ((state = ANY (ARRAY['open'::text, 'proposed'::text, 'approved'::text, 'closed'::text])))
);


--
-- Name: recovery_gate; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE recovery_gate (
    singleton boolean DEFAULT true NOT NULL,
    required boolean DEFAULT false NOT NULL,
    epoch bigint DEFAULT 0 NOT NULL,
    proposal_id text,
    requester text,
    approver text,
    evidence_ref text,
    reason text,
    proposed_at timestamp with time zone,
    approved_at timestamp with time zone,
    latched_at timestamp with time zone,
    CONSTRAINT recovery_gate_check CHECK (((proposal_id IS NULL) = (requester IS NULL))),
    CONSTRAINT recovery_gate_check1 CHECK (((approver IS NULL) OR ((requester IS NOT NULL) AND (approver <> requester)))),
    CONSTRAINT recovery_gate_check2 CHECK (((approved_at IS NULL) OR (approver IS NOT NULL))),
    CONSTRAINT recovery_gate_epoch_check CHECK ((epoch >= 0)),
    CONSTRAINT recovery_gate_singleton_check CHECK (singleton)
);


--
-- Name: recovery_jobs; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE recovery_jobs (
    request_id text NOT NULL,
    tenant_id text NOT NULL,
    lease_until timestamp with time zone,
    owner text,
    next_run_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    attempts integer DEFAULT 0 NOT NULL
);


--
-- Name: refund_orders; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE refund_orders (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    settlement_id text NOT NULL,
    business_ref text NOT NULL,
    amount bigint NOT NULL,
    reason text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    requester text NOT NULL,
    approver text,
    journal_id text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT refund_orders_amount_check CHECK ((amount > 0)),
    CONSTRAINT refund_orders_check CHECK (((approver IS NULL) OR (approver <> requester))),
    CONSTRAINT refund_orders_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'approved'::text])))
);


--
-- Name: report_totals; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE report_totals (
    consumer text NOT NULL,
    tenant_id text NOT NULL,
    credited numeric(100,0) DEFAULT 0 NOT NULL,
    charged numeric(100,0) DEFAULT 0 NOT NULL,
    refunded numeric(100,0) DEFAULT 0 NOT NULL,
    released numeric(100,0) DEFAULT 0 NOT NULL,
    requests bigint DEFAULT 0 NOT NULL
);


--
-- Name: request_idempotency; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE request_idempotency (
    tenant_id text NOT NULL,
    operation text DEFAULT 'inference'::text NOT NULL,
    key text NOT NULL,
    request_hmac text NOT NULL,
    request_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: request_meter_bound_lines; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE request_meter_bound_lines (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    side text NOT NULL,
    dimension text NOT NULL,
    unit text NOT NULL,
    quantity bigint NOT NULL,
    CONSTRAINT request_meter_bound_lines_dimension_check CHECK ((dimension = ANY (ARRAY['text_input_token'::text, 'text_output_token'::text]))),
    CONSTRAINT request_meter_bound_lines_quantity_check CHECK ((quantity >= 0)),
    CONSTRAINT request_meter_bound_lines_side_check CHECK ((side = ANY (ARRAY['retail'::text, 'supplier'::text]))),
    CONSTRAINT request_meter_bound_lines_unit_check CHECK ((unit = 'token'::text))
);


--
-- Name: request_meter_snapshots; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE request_meter_snapshots (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    model_id text NOT NULL,
    profile_version text NOT NULL,
    schema_id text NOT NULL,
    schema_digest bytea NOT NULL,
    variant_key text DEFAULT 'default'::text NOT NULL,
    price_version_id text NOT NULL,
    pricing_group_id text,
    tenant_pricing_revision bigint NOT NULL,
    retail_priced_at timestamp(6) with time zone NOT NULL,
    input_bound bigint NOT NULL,
    output_bound bigint NOT NULL,
    plan_canonical bytea,
    plan_digest bytea,
    retail_bounds_canonical bytea,
    retail_bounds_digest bytea,
    supplier_bounds_canonical bytea,
    supplier_bounds_digest bytea,
    retail_rates_canonical bytea,
    retail_rates_digest bytea,
    retail_meter_quote_digest bytea,
    retail_quote_canonical bytea,
    retail_quote_digest bytea,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    created_at timestamp(6) with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT request_meter_snapshots_check CHECK ((((plan_canonical IS NULL) AND (plan_digest IS NULL)) OR ((plan_canonical IS NOT NULL) AND (octet_length(plan_canonical) <= 65536) AND (octet_length(plan_digest) = 32)))),
    CONSTRAINT request_meter_snapshots_check1 CHECK ((((octet_length(retail_bounds_canonical) >= 1) AND (octet_length(retail_bounds_canonical) <= 8192)) AND (octet_length(retail_bounds_digest) = 32))),
    CONSTRAINT request_meter_snapshots_check2 CHECK ((((octet_length(supplier_bounds_canonical) >= 1) AND (octet_length(supplier_bounds_canonical) <= 8192)) AND (octet_length(supplier_bounds_digest) = 32))),
    CONSTRAINT request_meter_snapshots_check3 CHECK ((((octet_length(retail_rates_canonical) >= 1) AND (octet_length(retail_rates_canonical) <= 8192)) AND (octet_length(retail_rates_digest) = 32) AND (octet_length(retail_meter_quote_digest) = 32))),
    CONSTRAINT request_meter_snapshots_check4 CHECK ((((retail_quote_canonical IS NULL) AND (retail_quote_digest IS NULL)) OR ((retail_quote_canonical IS NOT NULL) AND (octet_length(retail_quote_canonical) <= 16384) AND (octet_length(retail_quote_digest) = 32)))),
    CONSTRAINT request_meter_snapshots_created_at_check CHECK (isfinite(created_at)),
    CONSTRAINT request_meter_snapshots_input_bound_check CHECK ((input_bound >= 0)),
    CONSTRAINT request_meter_snapshots_output_bound_check CHECK ((output_bound >= 0)),
    CONSTRAINT request_meter_snapshots_retail_priced_at_check CHECK (isfinite(retail_priced_at)),
    CONSTRAINT request_meter_snapshots_schema_digest_check CHECK ((octet_length(schema_digest) = 32)),
    CONSTRAINT request_meter_snapshots_tenant_pricing_revision_check CHECK ((tenant_pricing_revision > 0)),
    CONSTRAINT request_meter_snapshots_variant_key_check CHECK ((variant_key = 'default'::text))
);


--
-- Name: request_records; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE request_records (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    key_id text NOT NULL,
    owner_id text NOT NULL,
    model_id text NOT NULL,
    status text DEFAULT 'accepted'::text NOT NULL,
    billing_status text DEFAULT 'reserved'::text NOT NULL,
    profile_version text NOT NULL,
    policy_version text NOT NULL,
    request_hmac text NOT NULL,
    route_revision bigint DEFAULT 0 NOT NULL,
    request_deadline timestamp with time zone NOT NULL,
    instance_id text NOT NULL,
    streaming boolean NOT NULL,
    latest_finalization_id text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    client_ip inet,
    pb_created_xid xid8,
    pb_tenant_control text,
    pb_key_control text,
    pb_admitted_at timestamp with time zone,
    protocol text COLLATE pg_catalog."C",
    execution_kind text DEFAULT 'synchronous'::text COLLATE pg_catalog."C",
    result_kind text COLLATE pg_catalog."C",
    CONSTRAINT request_records_route_revision_positive CHECK ((route_revision > 0))
);


--
-- Name: reservations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE reservations (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    amount bigint NOT NULL,
    status text DEFAULT 'reserved'::text NOT NULL,
    deadline timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT reservations_amount_check CHECK ((amount >= 0)),
    CONSTRAINT reservations_status_check CHECK ((status = ANY (ARRAY['reserved'::text, 'pending_reconciliation'::text, 'settled'::text, 'released'::text])))
);


--
-- Name: route_binding_isolations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE route_binding_isolations (
    channel_version_id text NOT NULL,
    upstream_model text NOT NULL,
    reason text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT route_binding_isolations_reason_check CHECK ((reason = 'model_configuration_error'::text)),
    CONSTRAINT route_binding_isolations_upstream_model_check CHECK (((length(upstream_model) >= 1) AND (length(upstream_model) <= 200)))
);


--
-- Name: route_policy_versions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE route_policy_versions (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    model_id text NOT NULL,
    revision bigint NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    cost_mode text DEFAULT 'manual'::text NOT NULL,
    unknown_cost_policy text DEFAULT 'allow'::text NOT NULL,
    profile_version text NOT NULL COLLATE pg_catalog."C",
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    CONSTRAINT cost_first_requires_known_cost CHECK (((cost_mode <> 'cost_first'::text) OR (unknown_cost_policy = 'deny'::text))),
    CONSTRAINT route_policy_versions_cost_mode_check CHECK ((cost_mode = ANY (ARRAY['manual'::text, 'cost_first'::text]))),
    CONSTRAINT route_policy_versions_unknown_cost_policy_check CHECK ((unknown_cost_policy = ANY (ARRAY['allow'::text, 'deny'::text])))
);


--
-- Name: session_families; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE session_families (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    user_id text NOT NULL,
    revoked_at timestamp with time zone
);


--
-- Name: sessions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE sessions (
    digest text NOT NULL,
    family_id text NOT NULL,
    user_id text NOT NULL,
    tenant_id text,
    csrf_token text NOT NULL,
    mfa_verified boolean DEFAULT false NOT NULL,
    reauthenticated_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);


--
-- Name: settlements; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE settlements (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    finalization_id text,
    charged_amount bigint NOT NULL,
    raw_retail_amount numeric(100,0) NOT NULL,
    waived_retail_amount numeric(100,0) NOT NULL,
    refunded_amount bigint DEFAULT 0 NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    journal_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT settlements_charged_amount_check CHECK ((charged_amount >= 0)),
    CONSTRAINT settlements_check CHECK (((refunded_amount >= 0) AND (refunded_amount <= charged_amount))),
    CONSTRAINT settlements_raw_retail_amount_check CHECK ((raw_retail_amount >= (0)::numeric)),
    CONSTRAINT settlements_status_check CHECK ((status = ANY (ARRAY['settled'::text, 'released'::text]))),
    CONSTRAINT settlements_waived_retail_amount_check CHECK ((waived_retail_amount >= (0)::numeric))
);


--
-- Name: tenant_model_grants; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE tenant_model_grants (
    tenant_id text NOT NULL,
    model_id text NOT NULL
);


--
-- Name: tenant_pricing_assignments; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE tenant_pricing_assignments (
    operation_id text NOT NULL,
    tenant_id text NOT NULL,
    before_group_id text,
    after_group_id text,
    before_revision bigint NOT NULL,
    after_revision bigint NOT NULL,
    actor text NOT NULL,
    created_txid bigint DEFAULT txid_current() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT tenant_pricing_assignments_before_revision_check CHECK ((before_revision > 0)),
    CONSTRAINT tenant_pricing_assignments_check CHECK ((after_revision = (before_revision + 1))),
    CONSTRAINT tenant_pricing_assignments_check1 CHECK ((before_group_id IS DISTINCT FROM after_group_id)),
    CONSTRAINT tenant_pricing_assignments_created_at_check CHECK (isfinite(created_at))
);


--
-- Name: upstream_attempts; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE upstream_attempts (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    sequence integer NOT NULL,
    channel_id text NOT NULL,
    channel_version_id text NOT NULL,
    credential_id text NOT NULL,
    upstream_model text NOT NULL,
    delivery_state text DEFAULT 'prepared'::text NOT NULL,
    cost_status text DEFAULT 'unknown'::text NOT NULL,
    cost_amount numeric(100,0),
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    route_decisions jsonb DEFAULT '[]'::jsonb NOT NULL,
    pb_created_xid xid8,
    pb_dispatch_checked_at timestamp(6) with time zone,
    latest_cost_finalization_id text,
    latest_cost_correction_id text,
    CONSTRAINT upstream_attempts_cost_pointer_exclusive CHECK ((NOT ((latest_cost_finalization_id IS NOT NULL) AND (latest_cost_correction_id IS NOT NULL)))),
    CONSTRAINT upstream_attempts_cost_projection CHECK ((((cost_status = 'unknown'::text) AND (cost_amount IS NULL)) OR ((cost_status = ANY (ARRAY['contract'::text, 'invoice'::text])) AND (cost_amount IS NOT NULL)))),
    CONSTRAINT upstream_attempts_cost_source_projection CHECK ((((cost_status = 'unknown'::text) AND (cost_amount IS NULL) AND (latest_cost_finalization_id IS NULL) AND (latest_cost_correction_id IS NULL)) OR ((cost_status = ANY (ARRAY['contract'::text, 'invoice'::text])) AND (cost_amount IS NOT NULL) AND ((latest_cost_finalization_id IS NOT NULL) <> (latest_cost_correction_id IS NOT NULL))))),
    CONSTRAINT upstream_attempts_delivery_state_check CHECK ((delivery_state = ANY (ARRAY['prepared'::text, 'sending'::text, 'completed'::text, 'failed'::text, 'unknown'::text]))),
    CONSTRAINT upstream_attempts_pb_dispatch_checked_at_check CHECK (((pb_dispatch_checked_at IS NULL) OR isfinite(pb_dispatch_checked_at))),
    CONSTRAINT upstream_attempts_route_decisions_check CHECK (((jsonb_typeof(route_decisions) = 'array'::text) AND (jsonb_array_length(route_decisions) <= 1000) AND (octet_length((route_decisions)::text) <= 262144))),
    CONSTRAINT upstream_attempts_sequence_check CHECK (((sequence >= 1) AND (sequence <= 2)))
);


--
-- Name: users; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE users (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    email text NOT NULL,
    password_hash text NOT NULL,
    platform_role text DEFAULT 'none'::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    mfa_secret text DEFAULT ''::text NOT NULL,
    mfa_pending_secret text DEFAULT ''::text NOT NULL,
    mfa_enabled boolean DEFAULT false NOT NULL,
    mfa_last_step bigint DEFAULT '-1'::integer NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    email_verified_at timestamp with time zone,
    password_revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT users_email_check CHECK ((email = lower(email))),
    CONSTRAINT users_password_revision_check CHECK ((password_revision > 0)),
    CONSTRAINT users_platform_role_check CHECK ((platform_role = ANY (ARRAY['none'::text, 'admin'::text, 'finance_operator'::text, 'finance_approver'::text]))),
    CONSTRAINT users_status_check CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text])))
);


--
-- Name: voucher_batches; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE voucher_batches (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    state text DEFAULT 'proposed'::text NOT NULL,
    face_amount bigint NOT NULL,
    count integer NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    purpose_ref text NOT NULL,
    reason text NOT NULL,
    maker text NOT NULL,
    approver text,
    activated_at timestamp with time zone,
    terminal_actor text,
    terminal_at timestamp with time zone,
    revision bigint DEFAULT 1 NOT NULL,
    redeemed_count bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT voucher_batches_check CHECK (((redeemed_count >= 0) AND (redeemed_count <= count))),
    CONSTRAINT voucher_batches_check1 CHECK ((((face_amount)::numeric * (count)::numeric) <= ('9223372036854775807'::bigint)::numeric)),
    CONSTRAINT voucher_batches_check2 CHECK (((expires_at >= (created_at + '01:00:00'::interval)) AND (expires_at <= (created_at + '365 days'::interval)))),
    CONSTRAINT voucher_batches_check3 CHECK ((((state = 'proposed'::text) AND (approver IS NULL) AND (activated_at IS NULL) AND (terminal_actor IS NULL) AND (terminal_at IS NULL)) OR ((state = 'active'::text) AND (approver IS NOT NULL) AND (approver <> maker) AND (activated_at IS NOT NULL) AND (terminal_actor IS NULL) AND (terminal_at IS NULL)) OR ((state = 'voided'::text) AND (approver IS NULL) AND (activated_at IS NULL) AND (terminal_actor IS NOT NULL) AND (terminal_actor = maker) AND (terminal_at IS NOT NULL)) OR ((state = 'revoked'::text) AND (approver IS NOT NULL) AND (approver <> maker) AND (activated_at IS NOT NULL) AND (terminal_actor IS NOT NULL) AND (terminal_at IS NOT NULL)))),
    CONSTRAINT voucher_batches_count_check CHECK (((count >= 1) AND (count <= 100))),
    CONSTRAINT voucher_batches_face_amount_check CHECK ((face_amount > 0)),
    CONSTRAINT voucher_batches_id_check CHECK (funding_uuid(id)),
    CONSTRAINT voucher_batches_purpose_ref_check CHECK (((octet_length(purpose_ref) >= 1) AND (octet_length(purpose_ref) <= 256))),
    CONSTRAINT voucher_batches_reason_check CHECK (((octet_length(reason) >= 1) AND (octet_length(reason) <= 200))),
    CONSTRAINT voucher_batches_revision_check CHECK ((revision > 0)),
    CONSTRAINT voucher_batches_state_check CHECK ((state = ANY (ARRAY['proposed'::text, 'active'::text, 'voided'::text, 'revoked'::text])))
);


--
-- Name: voucher_policy; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE voucher_policy (
    singleton boolean DEFAULT true NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT voucher_policy_revision_check CHECK ((revision > 0)),
    CONSTRAINT voucher_policy_singleton_check CHECK (singleton)
);


--
-- Name: voucher_redemptions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE voucher_redemptions (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    tenant_id text NOT NULL,
    voucher_id text NOT NULL,
    batch_id text NOT NULL,
    operation_id text NOT NULL,
    actor text NOT NULL,
    amount bigint NOT NULL,
    journal_id text NOT NULL,
    funding_receipt_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT voucher_redemptions_amount_check CHECK ((amount > 0)),
    CONSTRAINT voucher_redemptions_id_check CHECK (funding_uuid(id))
);


--
-- Name: voucher_revocation_decisions; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE voucher_revocation_decisions (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    proposal_id text NOT NULL,
    batch_id text NOT NULL,
    operation_id text NOT NULL,
    approver text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    decided_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT voucher_revocation_decisions_check CHECK ((((status = 'accepted'::text) AND (reason = 'accepted'::text)) OR ((status = 'rejected'::text) AND (reason = 'voucher_evidence_stale'::text)))),
    CONSTRAINT voucher_revocation_decisions_id_check CHECK (funding_uuid(id)),
    CONSTRAINT voucher_revocation_decisions_status_check CHECK ((status = ANY (ARRAY['accepted'::text, 'rejected'::text])))
);


--
-- Name: voucher_revocations; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE voucher_revocations (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    batch_id text NOT NULL,
    operation_id text NOT NULL,
    maker text NOT NULL,
    expected_revision bigint NOT NULL,
    expected_redeemed_count bigint NOT NULL,
    reason text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT voucher_revocations_expected_redeemed_count_check CHECK ((expected_redeemed_count >= 0)),
    CONSTRAINT voucher_revocations_expected_revision_check CHECK ((expected_revision > 0)),
    CONSTRAINT voucher_revocations_id_check CHECK (funding_uuid(id)),
    CONSTRAINT voucher_revocations_reason_check CHECK (((octet_length(reason) >= 1) AND (octet_length(reason) <= 200)))
);


--
-- Name: vouchers; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE vouchers (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    batch_id text NOT NULL,
    code_digest text NOT NULL,
    digest_key_version text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT vouchers_code_digest_check CHECK ((code_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT vouchers_digest_key_version_check CHECK ((digest_key_version ~ '^[A-Za-z0-9_-]{1,32}$'::text)),
    CONSTRAINT vouchers_id_check CHECK (funding_uuid(id))
);


--
-- Name: wallet_openings; Type: TABLE; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TABLE wallet_openings (
    id text NOT NULL,
    tenant_id text NOT NULL,
    external_ref text NOT NULL,
    amount bigint NOT NULL,
    actor text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    created_xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    CONSTRAINT wallet_openings_amount_check CHECK ((amount > 0)),
    CONSTRAINT wallet_openings_external_ref_check CHECK (((length(external_ref) >= 1) AND (length(external_ref) <= 160)))
);


--
-- Name: FUNCTION funding_fact_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION funding_fact_guard() FROM PUBLIC;


--
-- Name: FUNCTION funding_journal_valid(journal_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION funding_journal_valid(journal_ref text) FROM PUBLIC;


--
-- Name: FUNCTION guard_group_price_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION guard_group_price_insert() FROM PUBLIC;


--
-- Name: FUNCTION guard_pricing_operation_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION guard_pricing_operation_insert() FROM PUBLIC;


--
-- Name: FUNCTION guard_procurement_reservation_identity(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION guard_procurement_reservation_identity() FROM PUBLIC;


--
-- Name: FUNCTION guard_request_retail_snapshot(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION guard_request_retail_snapshot() FROM PUBLIC;


--
-- Name: FUNCTION media_amount(quantity bigint, rate_per_million bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_amount(quantity bigint, rate_per_million bigint) FROM PUBLIC;


--
-- Name: FUNCTION media_artifact_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_artifact_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_attempt_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_attempt_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_observation_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_observation_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_profile_immutable(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_profile_immutable() FROM PUBLIC;


--
-- Name: FUNCTION media_request_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_request_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_reservation_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_reservation_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_settlement_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_settlement_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_settlement_immutable(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_settlement_immutable() FROM PUBLIC;


--
-- Name: FUNCTION media_task_event_apply(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_task_event_apply() FROM PUBLIC;


--
-- Name: FUNCTION media_task_event_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_task_event_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_task_identity_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_task_identity_guard() FROM PUBLIC;


--
-- Name: FUNCTION media_uuid(v text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION media_uuid(v text) FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_bounds_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_bounds_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_cost_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_cost_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_matches_request(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_matches_request() FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text) FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_plan_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_plan_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_quote_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_quote_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_snapshot_present(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_snapshot_present() FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_supplier_bounds(tenant_id text, request_id text, attempt_id text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_supplier_bounds(tenant_id text, request_id text, attempt_id text) FROM PUBLIC;


--
-- Name: FUNCTION meter_attempt_vectors_match(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_attempt_vectors_match() FROM PUBLIC;


--
-- Name: FUNCTION meter_binding_birth(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_binding_birth() FROM PUBLIC;


--
-- Name: FUNCTION meter_binding_cap_birth(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_binding_cap_birth() FROM PUBLIC;


--
-- Name: FUNCTION meter_bounds_canonical(schema_id text, side text, input_qty bigint, output_qty bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_bounds_canonical(schema_id text, side text, input_qty bigint, output_qty bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_bounds_digest(schema_digest bytea, side text, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_bounds_digest(schema_digest bytea, side text, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_capability_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_capability_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_cost_lines_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_cost_lines_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_cost_operation_birth(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_cost_operation_birth() FROM PUBLIC;


--
-- Name: FUNCTION meter_event_attempt_bound(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_event_attempt_bound() FROM PUBLIC;


--
-- Name: FUNCTION meter_event_observation_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_event_observation_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_event_quantity(event_id text, dimension text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_event_quantity(event_id text, dimension text) FROM PUBLIC;


--
-- Name: FUNCTION meter_event_rows_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_event_rows_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_final_digest(schema_digest bytea, side text, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_final_digest(schema_digest bytea, side text, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_finalization_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_finalization_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_finish_probe(probe_id text, protocol_status text, usage_status text, input_tokens bigint, output_tokens bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_finish_probe(probe_id text, protocol_status text, usage_status text, input_tokens bigint, output_tokens bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_frames(parts bytea[]); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_frames(parts bytea[]) FROM PUBLIC;


--
-- Name: FUNCTION meter_gcd(a bigint, b bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_gcd(a bigint, b bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_group_price_audit(oid text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_group_price_audit(oid text) FROM PUBLIC;


--
-- Name: FUNCTION meter_group_price_audit_birth(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_group_price_audit_birth() FROM PUBLIC;


--
-- Name: FUNCTION meter_group_price_audit_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_group_price_audit_insert() FROM PUBLIC;


--
-- Name: FUNCTION meter_group_price_complete(pid text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_group_price_complete(pid text) FROM PUBLIC;


--
-- Name: FUNCTION meter_insert_attempt_snapshot(tenant_id text, attempt_id text, request_id text, cost_version_id text, basis text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_insert_attempt_snapshot(tenant_id text, attempt_id text, request_id text, cost_version_id text, basis text) FROM PUBLIC;


--
-- Name: FUNCTION meter_insert_text_model(model_id text, public_name text, profile jsonb, profile_version text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_insert_text_model(model_id text, public_name text, profile jsonb, profile_version text) FROM PUBLIC;


--
-- Name: FUNCTION meter_insert_token_cap(binding_id text, dimension text, per_million bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_insert_token_cap(binding_id text, dimension text, per_million bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_insert_token_cost(cost_id text, channel_id text, upstream_model text, schema_id text, input_per_million bigint, output_per_million bigint, basis text, effective_at timestamp with time zone, created_by text, pricing_operation_id text, created_at timestamp with time zone); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_insert_token_cost(cost_id text, channel_id text, upstream_model text, schema_id text, input_per_million bigint, output_per_million bigint, basis text, effective_at timestamp with time zone, created_by text, pricing_operation_id text, created_at timestamp with time zone) FROM PUBLIC;


--
-- Name: FUNCTION meter_insert_token_price(price_id text, model_id text, input_per_million bigint, output_per_million bigint, effective_at timestamp with time zone, created_by text, price_group_id text, pricing_operation_id text, created_at timestamp with time zone); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_insert_token_price(price_id text, model_id text, input_per_million bigint, output_per_million bigint, effective_at timestamp with time zone, created_by text, price_group_id text, pricing_operation_id text, created_at timestamp with time zone) FROM PUBLIC;


--
-- Name: FUNCTION meter_model_profile(model_id text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_model_profile(model_id text) FROM PUBLIC;


--
-- Name: FUNCTION meter_model_route_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_model_route_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_observation_canonical(tenant_id text, request_id text, attempt_id text, source text, source_event_id text, profile_version text, schema_id text, schema_digest bytea, plan_digest bytea, observed_at timestamp with time zone, protocol_outcome text, transport_end_reason text, delivery_stage text, content_written boolean, finish_reason text, unexpected_charge_reason text, input_status text, input_quantity bigint, output_status text, output_quantity bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_observation_canonical(tenant_id text, request_id text, attempt_id text, source text, source_event_id text, profile_version text, schema_id text, schema_digest bytea, plan_digest bytea, observed_at timestamp with time zone, protocol_outcome text, transport_end_reason text, delivery_stage text, content_written boolean, finish_reason text, unexpected_charge_reason text, input_status text, input_quantity bigint, output_status text, output_quantity bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_observation_digest(schema_digest bytea, identity_canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_observation_digest(schema_digest bytea, identity_canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_observation_identity_canonical(tenant_id text, request_id text, attempt_id text, source text, source_event_id text, profile_version text, schema_id text, schema_digest bytea, plan_digest bytea, protocol_outcome text, transport_end_reason text, delivery_stage text, content_written boolean, finish_reason text, unexpected_charge_reason text, input_status text, input_quantity bigint, output_status text, output_quantity bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_observation_identity_canonical(tenant_id text, request_id text, attempt_id text, source text, source_event_id text, profile_version text, schema_id text, schema_digest bytea, plan_digest bytea, protocol_outcome text, transport_end_reason text, delivery_stage text, content_written boolean, finish_reason text, unexpected_charge_reason text, input_status text, input_quantity bigint, output_status text, output_quantity bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_observation_quantity(status text, quantity bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_observation_quantity(status text, quantity bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_plan_digest(schema_digest bytea, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_plan_digest(schema_digest bytea, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_plan_ready(profile_version text, request_hmac text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_plan_ready(profile_version text, request_hmac text) FROM PUBLIC;


--
-- Name: FUNCTION meter_price_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_price_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_procurement_attempt_closed(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_procurement_attempt_closed() FROM PUBLIC;


--
-- Name: FUNCTION meter_procurement_bound_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_procurement_bound_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_procurement_proposal_current(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_procurement_proposal_current() FROM PUBLIC;


--
-- Name: FUNCTION meter_profile_canonical(profile_version text, model text, protocol text, schema_id text, schema_digest bytea, contract bytea, input_max bigint, output_max bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_profile_canonical(profile_version text, model text, protocol text, schema_id text, schema_digest bytea, contract bytea, input_max bigint, output_max bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_profile_digest(schema_digest bytea, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_profile_digest(schema_digest bytea, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_pure_quote_digest(schema_digest bytea, side text, bounds_canonical bytea, rates_canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_pure_quote_digest(schema_digest bytea, side text, bounds_canonical bytea, rates_canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_quantities_digest(schema_digest bytea, side text, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_quantities_digest(schema_digest bytea, side text, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_quantity_canonical(schema_id text, input_tokens bigint, output_tokens bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_quantity_canonical(schema_id text, input_tokens bigint, output_tokens bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_quote_digest(schema_digest bytea, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_quote_digest(schema_digest bytea, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_rate_canonical(schema_id text, side text, input_num bigint, input_den bigint, output_num bigint, output_den bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_rate_canonical(schema_id text, side text, input_num bigint, input_den bigint, output_num bigint, output_den bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_rates_digest(schema_digest bytea, side text, canonical bytea); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_rates_digest(schema_digest bytea, side text, canonical bytea) FROM PUBLIC;


--
-- Name: FUNCTION meter_reconciliation_proposal_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_reconciliation_proposal_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_record_event_quantities(event_id text, tenant_id text, status text, input_tokens bigint, output_tokens bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_record_event_quantities(event_id text, tenant_id text, status text, input_tokens bigint, output_tokens bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_reduce_million(rate bigint, OUT numerator bigint, OUT denominator bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_reduce_million(rate bigint, OUT numerator bigint, OUT denominator bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_request_bounds_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_bounds_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_finalization_pointer(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_finalization_pointer() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_plan_canonical(profile_version text, model_id text, schema_id text, schema_digest bytea, output_limit bigint, streaming boolean, request_hmac text) FROM PUBLIC;


--
-- Name: FUNCTION meter_request_plan_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_plan_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_price_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_price_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_quote_matches(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_quote_matches() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_snapshot_present(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_snapshot_present() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_vectors(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_vectors() FROM PUBLIC;


--
-- Name: FUNCTION meter_request_vectors_match(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_request_vectors_match() FROM PUBLIC;


--
-- Name: FUNCTION meter_reservation_amount(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_reservation_amount() FROM PUBLIC;


--
-- Name: FUNCTION meter_retail_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, price_version_id text, input_bound bigint, output_bound bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_retail_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, price_version_id text, input_bound bigint, output_bound bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_route_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_route_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_route_profile(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_route_profile() FROM PUBLIC;


--
-- Name: FUNCTION meter_schema_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_schema_complete() FROM PUBLIC;


--
-- Name: FUNCTION meter_schema_stamp(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_schema_stamp() FROM PUBLIC;


--
-- Name: FUNCTION meter_settlement_amount(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_settlement_amount() FROM PUBLIC;


--
-- Name: FUNCTION meter_settlement_finalization(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_settlement_finalization() FROM PUBLIC;


--
-- Name: FUNCTION meter_side_quantity_canonical(schema_id text, side text, input_tokens bigint, output_tokens bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_side_quantity_canonical(schema_id text, side text, input_tokens bigint, output_tokens bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_supplier_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, quote_status text, unknown_reason text, cost_version_id text, rates_canonical bytea, input_bound bigint, output_bound bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_supplier_quote_canonical(schema_id text, schema_digest bytea, plan_digest bytea, quote_status text, unknown_reason text, cost_version_id text, rates_canonical bytea, input_bound bigint, output_bound bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_text_rates_canonical(r jsonb, expected_side text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_text_rates_canonical(r jsonb, expected_side text) FROM PUBLIC;


--
-- Name: FUNCTION meter_text_schema(input_max bigint, output_max bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_text_schema(input_max bigint, output_max bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_token_amount(input_qty bigint, output_qty bigint, in_n bigint, in_d bigint, out_n bigint, out_d bigint); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_token_amount(input_qty bigint, output_qty bigint, in_n bigint, in_d bigint, out_n bigint, out_d bigint) FROM PUBLIC;


--
-- Name: FUNCTION meter_usage_state(status text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION meter_usage_state(status text) FROM PUBLIC;


--
-- Name: FUNCTION payment_audit_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_audit_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_audit_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_audit_guard() FROM PUBLIC;


--
-- Name: FUNCTION payment_backoff(n integer); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_backoff(n integer) FROM PUBLIC;


--
-- Name: FUNCTION payment_cancel_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_cancel_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_capture_evidence_valid(evidence_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_capture_evidence_valid(evidence_ref text) FROM PUBLIC;


--
-- Name: FUNCTION payment_capture_generation(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_capture_generation() FROM PUBLIC;


--
-- Name: FUNCTION payment_capture_source_valid(capture_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_capture_source_valid(capture_ref text) FROM PUBLIC;


--
-- Name: FUNCTION payment_check_human(operation_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_check_human(operation_ref text) FROM PUBLIC;


--
-- Name: FUNCTION payment_evidence_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_evidence_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_evidence_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_evidence_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_extra_funding_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_extra_funding_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_frame(parts text[]); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_frame(parts text[]) FROM PUBLIC;


--
-- Name: FUNCTION payment_funding_journal_valid(journal_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_funding_journal_valid(journal_ref text) FROM PUBLIC;


--
-- Name: FUNCTION payment_human_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_human_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_identity_valid(identity_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_identity_valid(identity_ref text) FROM PUBLIC;


--
-- Name: FUNCTION payment_observation_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_observation_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_order_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_order_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_original_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_original_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_original_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_original_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_outbox_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_outbox_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_outbox_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_outbox_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_outbox_transition(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_outbox_transition() FROM PUBLIC;


--
-- Name: FUNCTION payment_policy_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_policy_guard() FROM PUBLIC;


--
-- Name: FUNCTION payment_processing_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_processing_guard() FROM PUBLIC;


--
-- Name: FUNCTION payment_query_attempt_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_query_attempt_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_ref(v text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_ref(v text) FROM PUBLIC;


--
-- Name: FUNCTION payment_require_rc(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_require_rc() FROM PUBLIC;


--
-- Name: FUNCTION payment_review_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_review_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_revision_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_revision_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_sandbox_journal_valid(journal_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_sandbox_journal_valid(journal_ref text) FROM PUBLIC;


--
-- Name: FUNCTION payment_sku_change_apply(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_sku_change_apply() FROM PUBLIC;


--
-- Name: FUNCTION payment_sku_change_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_sku_change_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_sku_root_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_sku_root_guard() FROM PUBLIC;


--
-- Name: FUNCTION payment_sku_version_insert(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_sku_version_insert() FROM PUBLIC;


--
-- Name: FUNCTION payment_source_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_source_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_source_writer(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_source_writer() FROM PUBLIC;


--
-- Name: FUNCTION payment_time(v timestamp with time zone); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_time(v timestamp with time zone) FROM PUBLIC;


--
-- Name: FUNCTION payment_worker_account_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_worker_account_guard() FROM PUBLIC;


--
-- Name: FUNCTION payment_worker_balance_complete(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_worker_balance_complete() FROM PUBLIC;


--
-- Name: FUNCTION payment_worker_line_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_worker_line_guard() FROM PUBLIC;


--
-- Name: FUNCTION payment_worker_tenant_lock_only(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION payment_worker_tenant_lock_only() FROM PUBLIC;


--
-- Name: FUNCTION realtime_event_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION realtime_event_guard() FROM PUBLIC;


--
-- Name: FUNCTION realtime_session_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION realtime_session_guard() FROM PUBLIC;


--
-- Name: FUNCTION realtime_turn_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION realtime_turn_guard() FROM PUBLIC;


--
-- Name: FUNCTION realtime_turn_inflight_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION realtime_turn_inflight_guard() FROM PUBLIC;


--
-- Name: FUNCTION wallet_account_valid(account_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_account_valid(account_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_event_valid(event_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_event_valid(event_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_journal_valid(journal_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_journal_valid(journal_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_lot_balance_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_lot_balance_guard() FROM PUBLIC;


--
-- Name: FUNCTION wallet_lot_event_stamp(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_lot_event_stamp() FROM PUBLIC;


--
-- Name: FUNCTION wallet_lot_valid(lot_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_lot_valid(lot_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_opening_journal_valid(journal_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_opening_journal_valid(journal_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_refund_valid(ref_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_refund_valid(ref_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_reservation_fifo_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_reservation_fifo_guard() FROM PUBLIC;


--
-- Name: FUNCTION wallet_reservation_valid(res_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_reservation_valid(res_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_settlement_valid(set_ref text); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_settlement_valid(set_ref text) FROM PUBLIC;


--
-- Name: FUNCTION wallet_source_fact_guard(); Type: ACL; Schema: dump_current_3b04ba63ced5; Owner: -
--

REVOKE ALL ON FUNCTION wallet_source_fact_guard() FROM PUBLIC;


--
-- PostgreSQL database dump complete
--

-- pg_dump unrestrict removed


--
-- PostgreSQL database dump
--

-- pg_dump restrict removed J6jHH5ZcFYfoEHpBmXvni6Y6Y0vtHkJHDOVTh9IIfaJLDSTaxZ9YEgby2dsGiKg

-- Dumped from database version 16.15 (Homebrew)
-- Dumped by pg_dump version 16.15 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', current_schema()::text || ', pg_catalog, pg_temp', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Data for Name: active_report_projection; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--

INSERT INTO active_report_projection (singleton, consumer, switched_at) VALUES (true, 'reports-v1', '2026-11-10 13:23:16.099172+08');


--
-- Data for Name: alert_commands; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: alert_control; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--

INSERT INTO alert_control (singleton) VALUES (true);


--
-- Data for Name: alert_episode_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: alert_episodes; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: alert_rule_runtime; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: alert_rules; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: api_keys; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: attempt_meter_bound_lines; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: attempt_meter_snapshots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: audit_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: billing_profile_blocks; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: binding_cost_rate_limits; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channel_batch_operations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channel_credentials; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channel_model_discoveries; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channel_model_discovery_results; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channel_version_credentials; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channel_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: channels; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: commerce_commands; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: commerce_operation_receipts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: cost_rate_lines; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: cost_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: credential_procurement_bindings; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: credit_orders; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: delivery_observations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: finalization_records; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: financial_idempotency; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_consumption_refund_lots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_lot_balances; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_lot_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_lots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_receipts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_reservation_lots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: funding_settlement_lots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: identity_challenges; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: identity_initialization; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: identity_mail_outbox; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: identity_mail_subjects; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: journal_lines; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: journals; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: ledger_accounts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_attempts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_model_profiles; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--

INSERT INTO media_model_profiles (model_key, operation, profile_version, unit, rate_per_million, supplier_rate_per_million, max_quantity, max_result_bytes, enabled, created_at) VALUES ('mock-image-generation', 'image_generation', '11111111-1111-4111-8111-111111111111', 'image', 1000000, 500000, 1, 16777216, true, '2026-11-10 13:23:17.485402+08');
INSERT INTO media_model_profiles (model_key, operation, profile_version, unit, rate_per_million, supplier_rate_per_million, max_quantity, max_result_bytes, enabled, created_at) VALUES ('mock-tts', 'tts', '22222222-2222-4222-8222-222222222222', 'character', 1000000, 500000, 16384, 16777216, true, '2026-11-10 13:23:17.485558+08');
INSERT INTO media_model_profiles (model_key, operation, profile_version, unit, rate_per_million, supplier_rate_per_million, max_quantity, max_result_bytes, enabled, created_at) VALUES ('mock-video-generation', 'video_generation', '33333333-3333-4333-8333-333333333333', 'task', 1000000, 500000, 1, 67108864, true, '2026-11-10 13:23:17.485566+08');
INSERT INTO media_model_profiles (model_key, operation, profile_version, unit, rate_per_million, supplier_rate_per_million, max_quantity, max_result_bytes, enabled, created_at) VALUES ('mock-realtime', 'realtime_turn', '44444444-4444-4444-8444-444444444444', 'realtime_byte', 1000000, 500000, 67108864, 16777216, true, '2026-11-10 13:23:17.485571+08');


--
-- Data for Name: media_observations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_requests; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_reservation_lots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_reservations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_result_artifacts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_settlement_lots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_settlements; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_task_artifacts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_task_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: media_tasks; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: member_model_grants; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: memberships; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: meter_event_quantities; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: meter_schema_dimensions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: meter_schemas; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: metering_event_conflicts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: metering_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: mfa_recovery_codes; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: model_bindings; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: model_capability_bound_lines; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: model_capability_variants; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: model_capability_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: models; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_cancel_intents; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_captures; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_command_observations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_command_outbox; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_effects; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_event_conflicts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_evidence_processing; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_inbox; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_order_capture_selections; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_orders; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_policy; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--

INSERT INTO payment_policy (singleton, enabled, revision) VALUES (true, false, 1);


--
-- Data for Name: payment_provider_revisions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_query_attempts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_query_conflicts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_review_facts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_runtime_attestations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_sandbox_environment; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_sku_changes; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_sku_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_skus; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_transaction_identities; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_verified_evidence; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: payment_verified_queries; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: periodic_budget_allocations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: periodic_budget_configuration_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: periodic_budget_controls; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: periodic_budget_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: periodic_budget_operations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: periodic_budget_windows; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: platform_probe_attempts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: price_groups; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: price_rate_lines; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: price_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: pricing_operations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_accounts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_balance_observations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_budget_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_commands; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_cost_proposals; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_evidence_identities; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: procurement_reservations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: projection_outbox; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: projection_receipts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: provider_cost_corrections; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: realtime_events; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: realtime_sessions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: realtime_turns; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: reconciliation_cases; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: recovery_gate; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--

INSERT INTO recovery_gate (singleton, required, epoch, proposal_id, requester, approver, evidence_ref, reason, proposed_at, approved_at, latched_at) VALUES (true, false, 0, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL);


--
-- Data for Name: recovery_jobs; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: refund_orders; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: report_totals; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: request_idempotency; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: request_meter_bound_lines; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: request_meter_snapshots; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: request_records; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: reservations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: route_binding_isolations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: route_policy_versions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: session_families; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: sessions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: settlements; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: tenant_model_grants; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: tenant_pricing_assignments; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: tenants; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: upstream_attempts; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: users; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: voucher_batches; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: voucher_policy; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--

INSERT INTO voucher_policy (singleton, enabled, revision) VALUES (true, false, 1);


--
-- Data for Name: voucher_redemptions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: voucher_revocation_decisions; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: voucher_revocations; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: vouchers; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- Data for Name: wallet_openings; Type: TABLE DATA; Schema: dump_current_3b04ba63ced5; Owner: -
--



--
-- PostgreSQL database dump complete
--

-- pg_dump unrestrict removed


--
-- PostgreSQL database dump
--

-- pg_dump restrict removed 6Pc4AbJD6H9impbvtRk2yjbvJhgkO11POq4Hy4EaeExCBkXD061rE0eVjWl6hVm

-- Dumped from database version 16.15 (Homebrew)
-- Dumped by pg_dump version 16.15 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', current_schema()::text || ', pg_catalog, pg_temp', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

--
-- Name: active_report_projection active_report_projection_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY active_report_projection
    ADD CONSTRAINT active_report_projection_pkey PRIMARY KEY (singleton);


--
-- Name: alert_commands alert_commands_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_commands
    ADD CONSTRAINT alert_commands_pkey PRIMARY KEY (operation_id);


--
-- Name: alert_commands alert_commands_transaction_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_commands
    ADD CONSTRAINT alert_commands_transaction_id_key UNIQUE (transaction_id);


--
-- Name: alert_control alert_control_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_control
    ADD CONSTRAINT alert_control_pkey PRIMARY KEY (singleton);


--
-- Name: alert_episode_events alert_episode_events_episode_id_action_revision_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episode_events
    ADD CONSTRAINT alert_episode_events_episode_id_action_revision_key UNIQUE (episode_id, action_revision);


--
-- Name: alert_episode_events alert_episode_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episode_events
    ADD CONSTRAINT alert_episode_events_pkey PRIMARY KEY (id);


--
-- Name: alert_episodes alert_episodes_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episodes
    ADD CONSTRAINT alert_episodes_pkey PRIMARY KEY (id);


--
-- Name: alert_episodes alert_episodes_rule_id_generation_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episodes
    ADD CONSTRAINT alert_episodes_rule_id_generation_key UNIQUE (rule_id, generation);


--
-- Name: alert_episodes alert_episodes_rule_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episodes
    ADD CONSTRAINT alert_episodes_rule_id_id_key UNIQUE (rule_id, id);


--
-- Name: alert_rule_runtime alert_rule_runtime_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_rule_runtime
    ADD CONSTRAINT alert_rule_runtime_pkey PRIMARY KEY (rule_id);


--
-- Name: alert_rules alert_rules_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT alert_rules_pkey PRIMARY KEY (id);


--
-- Name: api_keys api_keys_digest_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_digest_key UNIQUE (digest);


--
-- Name: api_keys api_keys_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (id);


--
-- Name: api_keys api_keys_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: attempt_meter_bound_lines attempt_meter_bound_lines_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_bound_lines
    ADD CONSTRAINT attempt_meter_bound_lines_pkey PRIMARY KEY (attempt_id, side, dimension);


--
-- Name: attempt_meter_bound_lines attempt_meter_bound_lines_tenant_id_attempt_id_side_dimensi_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_bound_lines
    ADD CONSTRAINT attempt_meter_bound_lines_tenant_id_attempt_id_side_dimensi_key UNIQUE (tenant_id, attempt_id, side, dimension);


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_pkey PRIMARY KEY (attempt_id);


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_tenant_id_attempt_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_tenant_id_attempt_id_key UNIQUE (tenant_id, attempt_id);


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_tenant_id_attempt_id_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_tenant_id_attempt_id_request_id_key UNIQUE (tenant_id, attempt_id, request_id);


--
-- Name: audit_events audit_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY audit_events
    ADD CONSTRAINT audit_events_pkey PRIMARY KEY (id);


--
-- Name: billing_profile_blocks billing_profile_blocks_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY billing_profile_blocks
    ADD CONSTRAINT billing_profile_blocks_pkey PRIMARY KEY (model_id, profile_version);


--
-- Name: binding_cost_rate_limits binding_cost_rate_limits_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY binding_cost_rate_limits
    ADD CONSTRAINT binding_cost_rate_limits_pkey PRIMARY KEY (binding_id, dimension);


--
-- Name: channel_batch_operations channel_batch_operations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_batch_operations
    ADD CONSTRAINT channel_batch_operations_pkey PRIMARY KEY (operation_id);


--
-- Name: channel_credentials channel_credentials_discovery_identity; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_credentials
    ADD CONSTRAINT channel_credentials_discovery_identity UNIQUE (id, channel_id);


--
-- Name: channel_credentials channel_credentials_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_credentials
    ADD CONSTRAINT channel_credentials_pkey PRIMARY KEY (id);


--
-- Name: channel_model_discoveries channel_model_discoveries_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_pkey PRIMARY KEY (id);


--
-- Name: channel_model_discovery_results channel_model_discovery_results_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discovery_results
    ADD CONSTRAINT channel_model_discovery_results_pkey PRIMARY KEY (discovery_id);


--
-- Name: channel_version_credentials channel_version_credentials_channel_version_id_channel_id_c_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_version_credentials
    ADD CONSTRAINT channel_version_credentials_channel_version_id_channel_id_c_key UNIQUE (channel_version_id, channel_id, credential_id);


--
-- Name: channel_version_credentials channel_version_credentials_channel_version_id_position_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_version_credentials
    ADD CONSTRAINT channel_version_credentials_channel_version_id_position_key UNIQUE (channel_version_id, "position");


--
-- Name: channel_version_credentials channel_version_credentials_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_version_credentials
    ADD CONSTRAINT channel_version_credentials_pkey PRIMARY KEY (channel_version_id, credential_id);


--
-- Name: channel_versions channel_versions_channel_identity; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_versions
    ADD CONSTRAINT channel_versions_channel_identity UNIQUE (id, channel_id);


--
-- Name: channel_versions channel_versions_discovery_identity; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_versions
    ADD CONSTRAINT channel_versions_discovery_identity UNIQUE (id, channel_id, credential_id);


--
-- Name: channel_versions channel_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_versions
    ADD CONSTRAINT channel_versions_pkey PRIMARY KEY (id);


--
-- Name: channels channels_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channels
    ADD CONSTRAINT channels_pkey PRIMARY KEY (id);


--
-- Name: commerce_commands commerce_commands_operation_id_kind_scope_actor_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_commands
    ADD CONSTRAINT commerce_commands_operation_id_kind_scope_actor_key UNIQUE (operation_id, kind, scope, actor);


--
-- Name: commerce_commands commerce_commands_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_commands
    ADD CONSTRAINT commerce_commands_pkey PRIMARY KEY (operation_id);


--
-- Name: commerce_operation_receipts commerce_operation_receipts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_operation_receipts
    ADD CONSTRAINT commerce_operation_receipts_pkey PRIMARY KEY (operation_id);


--
-- Name: cost_rate_lines cost_rate_lines_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_rate_lines
    ADD CONSTRAINT cost_rate_lines_pkey PRIMARY KEY (cost_version_id, dimension);


--
-- Name: cost_versions cost_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_versions
    ADD CONSTRAINT cost_versions_pkey PRIMARY KEY (id);


--
-- Name: cost_versions cost_versions_pricing_operation_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_versions
    ADD CONSTRAINT cost_versions_pricing_operation_key UNIQUE (pricing_operation_id);


--
-- Name: credential_procurement_bindings credential_procurement_bindin_id_credential_id_channel_id_a_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindin_id_credential_id_channel_id_a_key UNIQUE (id, credential_id, channel_id, account_id);


--
-- Name: credential_procurement_bindings credential_procurement_bindings_credential_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindings_credential_id_key UNIQUE (credential_id);


--
-- Name: credential_procurement_bindings credential_procurement_bindings_id_account_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindings_id_account_id_key UNIQUE (id, account_id);


--
-- Name: credential_procurement_bindings credential_procurement_bindings_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindings_operation_id_key UNIQUE (operation_id);


--
-- Name: credential_procurement_bindings credential_procurement_bindings_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindings_pkey PRIMARY KEY (id);


--
-- Name: credit_orders credit_orders_business_ref_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_business_ref_key UNIQUE (business_ref);


--
-- Name: credit_orders credit_orders_journal_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_journal_id_key UNIQUE (journal_id);


--
-- Name: credit_orders credit_orders_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_pkey PRIMARY KEY (id);


--
-- Name: delivery_observations delivery_observations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY delivery_observations
    ADD CONSTRAINT delivery_observations_pkey PRIMARY KEY (id);


--
-- Name: finalization_records finalization_records_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_pkey PRIMARY KEY (id);


--
-- Name: finalization_records finalization_records_tenant_id_id_attempt_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_tenant_id_id_attempt_id_key UNIQUE (tenant_id, id, attempt_id);


--
-- Name: finalization_records finalization_records_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: finalization_records finalization_records_tenant_id_request_id_attempt_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_tenant_id_request_id_attempt_id_id_key UNIQUE (tenant_id, request_id, attempt_id, id);


--
-- Name: finalization_records finalization_records_tenant_id_request_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_tenant_id_request_id_id_key UNIQUE (tenant_id, request_id, id);


--
-- Name: financial_idempotency financial_idempotency_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY financial_idempotency
    ADD CONSTRAINT financial_idempotency_pkey PRIMARY KEY (tenant_id, operation, key);


--
-- Name: funding_consumption_refund_lots funding_consumption_refund_lo_refund_order_id_allocation_in_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_consumption_refund_lots
    ADD CONSTRAINT funding_consumption_refund_lo_refund_order_id_allocation_in_key UNIQUE (refund_order_id, allocation_index);


--
-- Name: funding_consumption_refund_lots funding_consumption_refund_lots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_consumption_refund_lots
    ADD CONSTRAINT funding_consumption_refund_lots_pkey PRIMARY KEY (refund_order_id, refund_index);


--
-- Name: funding_lot_balances funding_lot_balances_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_balances
    ADD CONSTRAINT funding_lot_balances_pkey PRIMARY KEY (lot_id);


--
-- Name: funding_lot_events funding_lot_events_lot_id_revision_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_events
    ADD CONSTRAINT funding_lot_events_lot_id_revision_key UNIQUE (lot_id, revision);


--
-- Name: funding_lot_events funding_lot_events_operation_id_lot_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_events
    ADD CONSTRAINT funding_lot_events_operation_id_lot_id_key UNIQUE (operation_id, lot_id);


--
-- Name: funding_lot_events funding_lot_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_events
    ADD CONSTRAINT funding_lot_events_pkey PRIMARY KEY (id);


--
-- Name: funding_lots funding_lots_funding_receipt_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lots
    ADD CONSTRAINT funding_lots_funding_receipt_id_key UNIQUE (funding_receipt_id);


--
-- Name: funding_lots funding_lots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lots
    ADD CONSTRAINT funding_lots_pkey PRIMARY KEY (id);


--
-- Name: funding_lots funding_lots_source_kind_source_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lots
    ADD CONSTRAINT funding_lots_source_kind_source_id_key UNIQUE (source_kind, source_id);


--
-- Name: funding_lots funding_lots_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lots
    ADD CONSTRAINT funding_lots_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: funding_receipts funding_receipts_journal_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_receipts
    ADD CONSTRAINT funding_receipts_journal_id_key UNIQUE (journal_id);


--
-- Name: funding_receipts funding_receipts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_receipts
    ADD CONSTRAINT funding_receipts_pkey PRIMARY KEY (id);


--
-- Name: funding_receipts funding_receipts_source_kind_source_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_receipts
    ADD CONSTRAINT funding_receipts_source_kind_source_id_key UNIQUE (source_kind, source_id);


--
-- Name: funding_receipts funding_receipts_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_receipts
    ADD CONSTRAINT funding_receipts_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: funding_reservation_lots funding_reservation_lots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_reservation_lots
    ADD CONSTRAINT funding_reservation_lots_pkey PRIMARY KEY (reservation_id, allocation_index);


--
-- Name: funding_reservation_lots funding_reservation_lots_reservation_id_lot_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_reservation_lots
    ADD CONSTRAINT funding_reservation_lots_reservation_id_lot_id_key UNIQUE (reservation_id, lot_id);


--
-- Name: funding_reservation_lots funding_reservation_lots_tenant_id_reservation_id_allocatio_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_reservation_lots
    ADD CONSTRAINT funding_reservation_lots_tenant_id_reservation_id_allocatio_key UNIQUE (tenant_id, reservation_id, allocation_index, lot_id);


--
-- Name: funding_settlement_lots funding_settlement_lots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_settlement_lots
    ADD CONSTRAINT funding_settlement_lots_pkey PRIMARY KEY (settlement_id, allocation_index);


--
-- Name: funding_settlement_lots funding_settlement_lots_tenant_id_settlement_id_allocation__key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_settlement_lots
    ADD CONSTRAINT funding_settlement_lots_tenant_id_settlement_id_allocation__key UNIQUE (tenant_id, settlement_id, allocation_index, lot_id);


--
-- Name: identity_challenges identity_challenges_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_challenges
    ADD CONSTRAINT identity_challenges_pkey PRIMARY KEY (id);


--
-- Name: identity_challenges identity_challenges_token_digest_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_challenges
    ADD CONSTRAINT identity_challenges_token_digest_key UNIQUE (token_digest);


--
-- Name: identity_initialization identity_initialization_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_initialization
    ADD CONSTRAINT identity_initialization_pkey PRIMARY KEY (singleton);


--
-- Name: identity_mail_outbox identity_mail_outbox_challenge_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_mail_outbox
    ADD CONSTRAINT identity_mail_outbox_challenge_id_key UNIQUE (challenge_id);


--
-- Name: identity_mail_outbox identity_mail_outbox_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_mail_outbox
    ADD CONSTRAINT identity_mail_outbox_pkey PRIMARY KEY (id);


--
-- Name: identity_mail_subjects identity_mail_subjects_id_purpose_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_mail_subjects
    ADD CONSTRAINT identity_mail_subjects_id_purpose_key UNIQUE (id, purpose);


--
-- Name: identity_mail_subjects identity_mail_subjects_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_mail_subjects
    ADD CONSTRAINT identity_mail_subjects_pkey PRIMARY KEY (id);


--
-- Name: identity_mail_subjects identity_mail_subjects_purpose_email_digest_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_mail_subjects
    ADD CONSTRAINT identity_mail_subjects_purpose_email_digest_key UNIQUE (purpose, email_digest);


--
-- Name: journal_lines journal_lines_journal_id_account_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journal_lines
    ADD CONSTRAINT journal_lines_journal_id_account_id_key UNIQUE (journal_id, account_id);


--
-- Name: journal_lines journal_lines_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journal_lines
    ADD CONSTRAINT journal_lines_pkey PRIMARY KEY (id);


--
-- Name: journals journals_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journals
    ADD CONSTRAINT journals_operation_id_key UNIQUE (operation_id);


--
-- Name: journals journals_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journals
    ADD CONSTRAINT journals_pkey PRIMARY KEY (id);


--
-- Name: journals journals_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journals
    ADD CONSTRAINT journals_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: journals journals_tenant_id_id_key1; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journals
    ADD CONSTRAINT journals_tenant_id_id_key1 UNIQUE (tenant_id, id);


--
-- Name: ledger_accounts ledger_accounts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY ledger_accounts
    ADD CONSTRAINT ledger_accounts_pkey PRIMARY KEY (id);


--
-- Name: media_attempts media_attempts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_attempts
    ADD CONSTRAINT media_attempts_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: media_attempts media_attempts_tenant_id_id_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_attempts
    ADD CONSTRAINT media_attempts_tenant_id_id_request_id_key UNIQUE (tenant_id, id, request_id);


--
-- Name: media_model_profiles media_model_profiles_model_key_operation_profile_version_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_model_profiles
    ADD CONSTRAINT media_model_profiles_model_key_operation_profile_version_key UNIQUE (model_key, operation, profile_version);


--
-- Name: media_model_profiles media_model_profiles_operation_profile_version_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_model_profiles
    ADD CONSTRAINT media_model_profiles_operation_profile_version_key UNIQUE (operation, profile_version);


--
-- Name: media_model_profiles media_model_profiles_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_model_profiles
    ADD CONSTRAINT media_model_profiles_pkey PRIMARY KEY (model_key);


--
-- Name: media_observations media_observations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_observations
    ADD CONSTRAINT media_observations_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: media_observations media_observations_tenant_id_request_id_attempt_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_observations
    ADD CONSTRAINT media_observations_tenant_id_request_id_attempt_id_key UNIQUE (tenant_id, request_id, attempt_id);


--
-- Name: media_observations media_observations_tenant_id_source_source_event_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_observations
    ADD CONSTRAINT media_observations_tenant_id_source_source_event_id_key UNIQUE (tenant_id, source, source_event_id);


--
-- Name: media_requests media_requests_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_requests
    ADD CONSTRAINT media_requests_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: media_requests media_requests_tenant_id_operation_idempotency_key_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_requests
    ADD CONSTRAINT media_requests_tenant_id_operation_idempotency_key_key UNIQUE (tenant_id, operation, idempotency_key);


--
-- Name: media_reservation_lots media_reservation_lots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservation_lots
    ADD CONSTRAINT media_reservation_lots_pkey PRIMARY KEY (tenant_id, reservation_id, allocation_index);


--
-- Name: media_reservation_lots media_reservation_lots_tenant_id_reservation_id_lot_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservation_lots
    ADD CONSTRAINT media_reservation_lots_tenant_id_reservation_id_lot_id_key UNIQUE (tenant_id, reservation_id, lot_id);


--
-- Name: media_reservations media_reservations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservations
    ADD CONSTRAINT media_reservations_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: media_reservations media_reservations_tenant_id_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservations
    ADD CONSTRAINT media_reservations_tenant_id_request_id_key UNIQUE (tenant_id, request_id);


--
-- Name: media_result_artifacts media_result_artifacts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_result_artifacts
    ADD CONSTRAINT media_result_artifacts_pkey PRIMARY KEY (tenant_id, request_id);


--
-- Name: media_result_artifacts media_result_artifacts_tenant_id_attempt_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_result_artifacts
    ADD CONSTRAINT media_result_artifacts_tenant_id_attempt_id_key UNIQUE (tenant_id, attempt_id);


--
-- Name: media_settlement_lots media_settlement_lots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlement_lots
    ADD CONSTRAINT media_settlement_lots_pkey PRIMARY KEY (tenant_id, settlement_id, allocation_index);


--
-- Name: media_settlement_lots media_settlement_lots_tenant_id_settlement_id_allocation_in_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlement_lots
    ADD CONSTRAINT media_settlement_lots_tenant_id_settlement_id_allocation_in_key UNIQUE (tenant_id, settlement_id, allocation_index, lot_id);


--
-- Name: media_settlements media_settlements_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlements
    ADD CONSTRAINT media_settlements_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: media_settlements media_settlements_tenant_id_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlements
    ADD CONSTRAINT media_settlements_tenant_id_request_id_key UNIQUE (tenant_id, request_id);


--
-- Name: media_task_artifacts media_task_artifacts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_task_artifacts
    ADD CONSTRAINT media_task_artifacts_pkey PRIMARY KEY (tenant_id, task_id, artifact_reference);


--
-- Name: media_task_artifacts media_task_artifacts_tenant_id_task_id_sha256_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_task_artifacts
    ADD CONSTRAINT media_task_artifacts_tenant_id_task_id_sha256_key UNIQUE (tenant_id, task_id, sha256);


--
-- Name: media_task_events media_task_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_task_events
    ADD CONSTRAINT media_task_events_pkey PRIMARY KEY (tenant_id, task_id, event_id);


--
-- Name: media_task_events media_task_events_tenant_id_task_id_sequence_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_task_events
    ADD CONSTRAINT media_task_events_tenant_id_task_id_sequence_key UNIQUE (tenant_id, task_id, sequence);


--
-- Name: media_tasks media_tasks_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: media_tasks media_tasks_tenant_id_id_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_id_request_id_key UNIQUE (tenant_id, id, request_id);


--
-- Name: media_tasks media_tasks_tenant_id_idempotency_key_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_idempotency_key_key UNIQUE (tenant_id, idempotency_key);


--
-- Name: media_tasks media_tasks_tenant_id_provider_reference_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_provider_reference_key UNIQUE (tenant_id, provider_reference);


--
-- Name: member_model_grants member_model_grants_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY member_model_grants
    ADD CONSTRAINT member_model_grants_pkey PRIMARY KEY (tenant_id, user_id, model_id);


--
-- Name: memberships memberships_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY memberships
    ADD CONSTRAINT memberships_pkey PRIMARY KEY (tenant_id, user_id);


--
-- Name: meter_event_quantities meter_event_quantities_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_event_quantities
    ADD CONSTRAINT meter_event_quantities_pkey PRIMARY KEY (event_id, dimension);


--
-- Name: meter_schema_dimensions meter_schema_dimensions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_schema_dimensions
    ADD CONSTRAINT meter_schema_dimensions_pkey PRIMARY KEY (schema_id, dimension);


--
-- Name: meter_schema_dimensions meter_schema_dimensions_schema_id_dimension_unit_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_schema_dimensions
    ADD CONSTRAINT meter_schema_dimensions_schema_id_dimension_unit_key UNIQUE (schema_id, dimension, unit);


--
-- Name: meter_schemas meter_schemas_id_schema_digest_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_schemas
    ADD CONSTRAINT meter_schemas_id_schema_digest_key UNIQUE (id, schema_digest);


--
-- Name: meter_schemas meter_schemas_name_revision_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_schemas
    ADD CONSTRAINT meter_schemas_name_revision_key UNIQUE (name, revision);


--
-- Name: meter_schemas meter_schemas_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_schemas
    ADD CONSTRAINT meter_schemas_pkey PRIMARY KEY (id);


--
-- Name: metering_event_conflicts metering_event_conflicts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_event_conflicts
    ADD CONSTRAINT metering_event_conflicts_pkey PRIMARY KEY (id);


--
-- Name: metering_events metering_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_events
    ADD CONSTRAINT metering_events_pkey PRIMARY KEY (id);


--
-- Name: metering_events metering_events_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_events
    ADD CONSTRAINT metering_events_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: metering_events metering_events_tenant_id_source_source_event_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_events
    ADD CONSTRAINT metering_events_tenant_id_source_source_event_id_key UNIQUE (tenant_id, source, source_event_id);


--
-- Name: mfa_recovery_codes mfa_recovery_codes_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY mfa_recovery_codes
    ADD CONSTRAINT mfa_recovery_codes_pkey PRIMARY KEY (user_id, digest);


--
-- Name: model_bindings model_bindings_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_bindings
    ADD CONSTRAINT model_bindings_pkey PRIMARY KEY (id);


--
-- Name: model_bindings model_bindings_route_variant_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_bindings
    ADD CONSTRAINT model_bindings_route_variant_key UNIQUE (route_policy_version_id, channel_id, upstream_model, variant_key);


--
-- Name: model_capability_bound_lines model_capability_bound_lines_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_bound_lines
    ADD CONSTRAINT model_capability_bound_lines_pkey PRIMARY KEY (profile_version, variant_key, side, dimension);


--
-- Name: model_capability_variants model_capability_variants_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_variants
    ADD CONSTRAINT model_capability_variants_pkey PRIMARY KEY (profile_version, variant_key);


--
-- Name: model_capability_versions model_capability_versions_id_model_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_versions
    ADD CONSTRAINT model_capability_versions_id_model_id_key UNIQUE (id, model_id);


--
-- Name: model_capability_versions model_capability_versions_id_schema_id_schema_digest_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_versions
    ADD CONSTRAINT model_capability_versions_id_schema_id_schema_digest_key UNIQUE (id, schema_id, schema_digest);


--
-- Name: model_capability_versions model_capability_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_versions
    ADD CONSTRAINT model_capability_versions_pkey PRIMARY KEY (id);


--
-- Name: models models_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY models
    ADD CONSTRAINT models_pkey PRIMARY KEY (id);


--
-- Name: models models_public_name_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY models
    ADD CONSTRAINT models_public_name_key UNIQUE (public_name);


--
-- Name: payment_cancel_intents payment_cancel_intents_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_cancel_intents
    ADD CONSTRAINT payment_cancel_intents_operation_id_key UNIQUE (operation_id);


--
-- Name: payment_cancel_intents payment_cancel_intents_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_cancel_intents
    ADD CONSTRAINT payment_cancel_intents_pkey PRIMARY KEY (order_id);


--
-- Name: payment_captures payment_captures_identity_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_identity_id_key UNIQUE (identity_id);


--
-- Name: payment_captures payment_captures_order_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_order_id_key UNIQUE (order_id);


--
-- Name: payment_captures payment_captures_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_pkey PRIMARY KEY (id);


--
-- Name: payment_captures payment_captures_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: payment_command_observations payment_command_observations_outbox_id_generation_reply_dig_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_observations
    ADD CONSTRAINT payment_command_observations_outbox_id_generation_reply_dig_key UNIQUE (outbox_id, generation, reply_digest);


--
-- Name: payment_command_observations payment_command_observations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_observations
    ADD CONSTRAINT payment_command_observations_pkey PRIMARY KEY (id);


--
-- Name: payment_command_outbox payment_command_outbox_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_outbox
    ADD CONSTRAINT payment_command_outbox_pkey PRIMARY KEY (id);


--
-- Name: payment_effects payment_effects_environment_id_merchant_id_provider_transac_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_effects
    ADD CONSTRAINT payment_effects_environment_id_merchant_id_provider_transac_key UNIQUE (environment_id, merchant_id, provider_transaction_id, effect_kind, effect_id);


--
-- Name: payment_effects payment_effects_identity_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_effects
    ADD CONSTRAINT payment_effects_identity_id_key UNIQUE (identity_id);


--
-- Name: payment_effects payment_effects_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_effects
    ADD CONSTRAINT payment_effects_pkey PRIMARY KEY (capture_id);


--
-- Name: payment_event_conflicts payment_event_conflicts_inbox_id_conflicting_digest_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_event_conflicts
    ADD CONSTRAINT payment_event_conflicts_inbox_id_conflicting_digest_key UNIQUE (inbox_id, conflicting_digest);


--
-- Name: payment_event_conflicts payment_event_conflicts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_event_conflicts
    ADD CONSTRAINT payment_event_conflicts_pkey PRIMARY KEY (id);


--
-- Name: payment_evidence_processing payment_evidence_processing_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_evidence_processing
    ADD CONSTRAINT payment_evidence_processing_pkey PRIMARY KEY (evidence_id);


--
-- Name: payment_inbox payment_inbox_environment_id_merchant_id_event_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_inbox
    ADD CONSTRAINT payment_inbox_environment_id_merchant_id_event_id_key UNIQUE (environment_id, merchant_id, event_id);


--
-- Name: payment_inbox payment_inbox_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_inbox
    ADD CONSTRAINT payment_inbox_pkey PRIMARY KEY (id);


--
-- Name: payment_order_capture_selections payment_order_capture_selections_identity_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_order_capture_selections
    ADD CONSTRAINT payment_order_capture_selections_identity_id_key UNIQUE (identity_id);


--
-- Name: payment_order_capture_selections payment_order_capture_selections_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_order_capture_selections
    ADD CONSTRAINT payment_order_capture_selections_pkey PRIMARY KEY (order_id);


--
-- Name: payment_orders payment_orders_environment_id_merchant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_environment_id_merchant_id_id_key UNIQUE (environment_id, merchant_id, id);


--
-- Name: payment_orders payment_orders_merchant_order_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_merchant_order_id_key UNIQUE (merchant_order_id);


--
-- Name: payment_orders payment_orders_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_pkey PRIMARY KEY (id);


--
-- Name: payment_orders payment_orders_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: payment_policy payment_policy_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_policy
    ADD CONSTRAINT payment_policy_pkey PRIMARY KEY (singleton);


--
-- Name: payment_provider_revisions payment_provider_revisions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_provider_revisions
    ADD CONSTRAINT payment_provider_revisions_pkey PRIMARY KEY (environment_id, merchant_id, order_id, provider_order_revision);


--
-- Name: payment_query_attempts payment_query_attempts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_attempts
    ADD CONSTRAINT payment_query_attempts_pkey PRIMARY KEY (query_id);


--
-- Name: payment_query_attempts payment_query_attempts_supervisor_id_created_generation_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_attempts
    ADD CONSTRAINT payment_query_attempts_supervisor_id_created_generation_key UNIQUE (supervisor_id, created_generation);


--
-- Name: payment_query_attempts payment_query_attempts_supervisor_id_query_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_attempts
    ADD CONSTRAINT payment_query_attempts_supervisor_id_query_id_key UNIQUE (supervisor_id, query_id);


--
-- Name: payment_query_conflicts payment_query_conflicts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_conflicts
    ADD CONSTRAINT payment_query_conflicts_pkey PRIMARY KEY (id);


--
-- Name: payment_query_conflicts payment_query_conflicts_verified_query_id_conflicting_diges_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_conflicts
    ADD CONSTRAINT payment_query_conflicts_verified_query_id_conflicting_diges_key UNIQUE (verified_query_id, conflicting_digest);


--
-- Name: payment_review_facts payment_review_facts_evidence_id_reason_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_review_facts
    ADD CONSTRAINT payment_review_facts_evidence_id_reason_key UNIQUE (evidence_id, reason);


--
-- Name: payment_review_facts payment_review_facts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_review_facts
    ADD CONSTRAINT payment_review_facts_pkey PRIMARY KEY (id);


--
-- Name: payment_runtime_attestations payment_runtime_attestations_environment_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_runtime_attestations
    ADD CONSTRAINT payment_runtime_attestations_environment_id_key UNIQUE (environment_id);


--
-- Name: payment_runtime_attestations payment_runtime_attestations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_runtime_attestations
    ADD CONSTRAINT payment_runtime_attestations_pkey PRIMARY KEY (singleton);


--
-- Name: payment_sandbox_environment payment_sandbox_environment_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sandbox_environment
    ADD CONSTRAINT payment_sandbox_environment_id_key UNIQUE (id);


--
-- Name: payment_sandbox_environment payment_sandbox_environment_id_merchant_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sandbox_environment
    ADD CONSTRAINT payment_sandbox_environment_id_merchant_id_key UNIQUE (id, merchant_id);


--
-- Name: payment_sandbox_environment payment_sandbox_environment_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sandbox_environment
    ADD CONSTRAINT payment_sandbox_environment_pkey PRIMARY KEY (singleton);


--
-- Name: payment_sku_changes payment_sku_changes_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_pkey PRIMARY KEY (operation_id);


--
-- Name: payment_sku_versions payment_sku_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_pkey PRIMARY KEY (id);


--
-- Name: payment_sku_versions payment_sku_versions_publish_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_publish_operation_id_key UNIQUE (publish_operation_id);


--
-- Name: payment_sku_versions payment_sku_versions_sku_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_sku_id_id_key UNIQUE (sku_id, id);


--
-- Name: payment_sku_versions payment_sku_versions_sku_id_version_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_sku_id_version_key UNIQUE (sku_id, version);


--
-- Name: payment_skus payment_skus_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_skus
    ADD CONSTRAINT payment_skus_pkey PRIMARY KEY (id);


--
-- Name: payment_transaction_identities payment_transaction_identitie_environment_id_merchant_id_pr_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_transaction_identities
    ADD CONSTRAINT payment_transaction_identitie_environment_id_merchant_id_pr_key UNIQUE (environment_id, merchant_id, provider_transaction_id);


--
-- Name: payment_transaction_identities payment_transaction_identities_order_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_transaction_identities
    ADD CONSTRAINT payment_transaction_identities_order_id_id_key UNIQUE (order_id, id);


--
-- Name: payment_transaction_identities payment_transaction_identities_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_transaction_identities
    ADD CONSTRAINT payment_transaction_identities_pkey PRIMARY KEY (id);


--
-- Name: payment_verified_evidence payment_verified_evidence_inbox_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_evidence
    ADD CONSTRAINT payment_verified_evidence_inbox_id_key UNIQUE (inbox_id);


--
-- Name: payment_verified_evidence payment_verified_evidence_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_evidence
    ADD CONSTRAINT payment_verified_evidence_pkey PRIMARY KEY (id);


--
-- Name: payment_verified_evidence payment_verified_evidence_verified_query_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_evidence
    ADD CONSTRAINT payment_verified_evidence_verified_query_id_key UNIQUE (verified_query_id);


--
-- Name: payment_verified_queries payment_verified_queries_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_queries
    ADD CONSTRAINT payment_verified_queries_pkey PRIMARY KEY (id);


--
-- Name: payment_verified_queries payment_verified_queries_query_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_queries
    ADD CONSTRAINT payment_verified_queries_query_id_key UNIQUE (query_id);


--
-- Name: periodic_budget_allocations periodic_budget_allocations_control_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_allocations
    ADD CONSTRAINT periodic_budget_allocations_control_id_id_key UNIQUE (control_id, id);


--
-- Name: periodic_budget_allocations periodic_budget_allocations_control_id_subject_type_subject_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_allocations
    ADD CONSTRAINT periodic_budget_allocations_control_id_subject_type_subject_key UNIQUE (control_id, subject_type, subject_id);


--
-- Name: periodic_budget_allocations periodic_budget_allocations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_allocations
    ADD CONSTRAINT periodic_budget_allocations_pkey PRIMARY KEY (id);


--
-- Name: periodic_budget_configuration_versions periodic_budget_configuration_versions_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_configuration_versions
    ADD CONSTRAINT periodic_budget_configuration_versions_operation_id_key UNIQUE (operation_id);


--
-- Name: periodic_budget_configuration_versions periodic_budget_configuration_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_configuration_versions
    ADD CONSTRAINT periodic_budget_configuration_versions_pkey PRIMARY KEY (control_id, revision);


--
-- Name: periodic_budget_controls periodic_budget_controls_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_controls
    ADD CONSTRAINT periodic_budget_controls_pkey PRIMARY KEY (id);


--
-- Name: periodic_budget_events periodic_budget_events_control_id_seq_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_events
    ADD CONSTRAINT periodic_budget_events_control_id_seq_key UNIQUE (control_id, seq);


--
-- Name: periodic_budget_events periodic_budget_events_control_id_source_kind_source_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_events
    ADD CONSTRAINT periodic_budget_events_control_id_source_kind_source_id_key UNIQUE (control_id, source_kind, source_id);


--
-- Name: periodic_budget_events periodic_budget_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_events
    ADD CONSTRAINT periodic_budget_events_pkey PRIMARY KEY (id);


--
-- Name: periodic_budget_operations periodic_budget_operations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_operations
    ADD CONSTRAINT periodic_budget_operations_pkey PRIMARY KEY (operation_id);


--
-- Name: periodic_budget_windows periodic_budget_windows_control_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_windows
    ADD CONSTRAINT periodic_budget_windows_control_id_id_key UNIQUE (control_id, id);


--
-- Name: periodic_budget_windows periodic_budget_windows_control_id_start_at_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_windows
    ADD CONSTRAINT periodic_budget_windows_control_id_start_at_key UNIQUE (control_id, start_at);


--
-- Name: periodic_budget_windows periodic_budget_windows_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_windows
    ADD CONSTRAINT periodic_budget_windows_pkey PRIMARY KEY (id);


--
-- Name: platform_probe_attempts platform_probe_attempts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_pkey PRIMARY KEY (id);


--
-- Name: price_groups price_groups_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_groups
    ADD CONSTRAINT price_groups_pkey PRIMARY KEY (id);


--
-- Name: price_rate_lines price_rate_lines_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_rate_lines
    ADD CONSTRAINT price_rate_lines_pkey PRIMARY KEY (price_version_id, dimension);


--
-- Name: price_versions price_versions_domain_effective_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_domain_effective_key UNIQUE NULLS NOT DISTINCT (model_id, price_group_id, schema_id, variant_key, effective_at);


--
-- Name: price_versions price_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_pkey PRIMARY KEY (id);


--
-- Name: pricing_operations pricing_operations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY pricing_operations
    ADD CONSTRAINT pricing_operations_pkey PRIMARY KEY (operation_id);


--
-- Name: procurement_accounts procurement_accounts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_accounts
    ADD CONSTRAINT procurement_accounts_pkey PRIMARY KEY (id);


--
-- Name: procurement_balance_observations procurement_balance_observations_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_balance_observations
    ADD CONSTRAINT procurement_balance_observations_operation_id_key UNIQUE (operation_id);


--
-- Name: procurement_balance_observations procurement_balance_observations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_balance_observations
    ADD CONSTRAINT procurement_balance_observations_pkey PRIMARY KEY (id);


--
-- Name: procurement_budget_events procurement_budget_events_event_key_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_budget_events
    ADD CONSTRAINT procurement_budget_events_event_key_key UNIQUE (event_key);


--
-- Name: procurement_budget_events procurement_budget_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_budget_events
    ADD CONSTRAINT procurement_budget_events_pkey PRIMARY KEY (id);


--
-- Name: procurement_commands procurement_commands_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_commands
    ADD CONSTRAINT procurement_commands_pkey PRIMARY KEY (operation_id);


--
-- Name: procurement_cost_proposals procurement_cost_proposals_approval_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_cost_proposals
    ADD CONSTRAINT procurement_cost_proposals_approval_operation_id_key UNIQUE (approval_operation_id);


--
-- Name: procurement_cost_proposals procurement_cost_proposals_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_cost_proposals
    ADD CONSTRAINT procurement_cost_proposals_pkey PRIMARY KEY (id);


--
-- Name: procurement_evidence_identities procurement_evidence_identities_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_evidence_identities
    ADD CONSTRAINT procurement_evidence_identities_pkey PRIMARY KEY (attempt_id, evidence_digest);


--
-- Name: procurement_reservations procurement_reservations_attempt_id_account_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_reservations
    ADD CONSTRAINT procurement_reservations_attempt_id_account_id_key UNIQUE (attempt_id, account_id);


--
-- Name: procurement_reservations procurement_reservations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_reservations
    ADD CONSTRAINT procurement_reservations_pkey PRIMARY KEY (attempt_id);


--
-- Name: projection_outbox projection_outbox_operation_id_kind_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY projection_outbox
    ADD CONSTRAINT projection_outbox_operation_id_kind_key UNIQUE (operation_id, kind);


--
-- Name: projection_outbox projection_outbox_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY projection_outbox
    ADD CONSTRAINT projection_outbox_pkey PRIMARY KEY (event_id);


--
-- Name: projection_receipts projection_receipts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY projection_receipts
    ADD CONSTRAINT projection_receipts_pkey PRIMARY KEY (consumer, event_id);


--
-- Name: provider_cost_corrections provider_cost_corrections_attempt_identity; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY provider_cost_corrections
    ADD CONSTRAINT provider_cost_corrections_attempt_identity UNIQUE (tenant_id, id, attempt_id);


--
-- Name: provider_cost_corrections provider_cost_corrections_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY provider_cost_corrections
    ADD CONSTRAINT provider_cost_corrections_operation_id_key UNIQUE (operation_id);


--
-- Name: provider_cost_corrections provider_cost_corrections_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY provider_cost_corrections
    ADD CONSTRAINT provider_cost_corrections_pkey PRIMARY KEY (id);


--
-- Name: realtime_events realtime_events_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_events
    ADD CONSTRAINT realtime_events_pkey PRIMARY KEY (tenant_id, session_id, event_id);


--
-- Name: realtime_sessions realtime_sessions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: realtime_sessions realtime_sessions_tenant_id_id_model_id_profile_version_rou_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_tenant_id_id_model_id_profile_version_rou_key UNIQUE (tenant_id, id, model_id, profile_version, route_id);


--
-- Name: realtime_turns realtime_turns_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_turns
    ADD CONSTRAINT realtime_turns_pkey PRIMARY KEY (tenant_id, id);


--
-- Name: realtime_turns realtime_turns_tenant_id_id_session_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_turns
    ADD CONSTRAINT realtime_turns_tenant_id_id_session_id_key UNIQUE (tenant_id, id, session_id);


--
-- Name: realtime_turns realtime_turns_tenant_id_session_id_ordinal_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_turns
    ADD CONSTRAINT realtime_turns_tenant_id_session_id_ordinal_key UNIQUE (tenant_id, session_id, ordinal);


--
-- Name: reconciliation_cases reconciliation_cases_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reconciliation_cases
    ADD CONSTRAINT reconciliation_cases_pkey PRIMARY KEY (id);


--
-- Name: reconciliation_cases reconciliation_cases_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reconciliation_cases
    ADD CONSTRAINT reconciliation_cases_request_id_key UNIQUE (request_id);


--
-- Name: recovery_gate recovery_gate_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY recovery_gate
    ADD CONSTRAINT recovery_gate_pkey PRIMARY KEY (singleton);


--
-- Name: recovery_jobs recovery_jobs_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY recovery_jobs
    ADD CONSTRAINT recovery_jobs_pkey PRIMARY KEY (request_id);


--
-- Name: refund_orders refund_orders_business_ref_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_business_ref_key UNIQUE (business_ref);


--
-- Name: refund_orders refund_orders_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_pkey PRIMARY KEY (id);


--
-- Name: refund_orders refund_orders_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: report_totals report_totals_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY report_totals
    ADD CONSTRAINT report_totals_pkey PRIMARY KEY (consumer, tenant_id);


--
-- Name: request_idempotency request_idempotency_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_idempotency
    ADD CONSTRAINT request_idempotency_pkey PRIMARY KEY (tenant_id, operation, key);


--
-- Name: request_meter_bound_lines request_meter_bound_lines_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_bound_lines
    ADD CONSTRAINT request_meter_bound_lines_pkey PRIMARY KEY (request_id, side, dimension);


--
-- Name: request_meter_bound_lines request_meter_bound_lines_tenant_id_request_id_side_dimensi_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_bound_lines
    ADD CONSTRAINT request_meter_bound_lines_tenant_id_request_id_side_dimensi_key UNIQUE (tenant_id, request_id, side, dimension);


--
-- Name: request_meter_snapshots request_meter_snapshots_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_snapshots
    ADD CONSTRAINT request_meter_snapshots_pkey PRIMARY KEY (request_id);


--
-- Name: request_meter_snapshots request_meter_snapshots_tenant_id_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_snapshots
    ADD CONSTRAINT request_meter_snapshots_tenant_id_request_id_key UNIQUE (tenant_id, request_id);


--
-- Name: request_records request_records_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_records_pkey PRIMARY KEY (id);


--
-- Name: request_records request_records_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_records_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: reservations reservations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reservations
    ADD CONSTRAINT reservations_pkey PRIMARY KEY (id);


--
-- Name: reservations reservations_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reservations
    ADD CONSTRAINT reservations_request_id_key UNIQUE (request_id);


--
-- Name: reservations reservations_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reservations
    ADD CONSTRAINT reservations_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: route_binding_isolations route_binding_isolations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY route_binding_isolations
    ADD CONSTRAINT route_binding_isolations_pkey PRIMARY KEY (channel_version_id, upstream_model);


--
-- Name: route_policy_versions route_policy_versions_model_id_revision_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY route_policy_versions
    ADD CONSTRAINT route_policy_versions_model_id_revision_key UNIQUE (model_id, revision);


--
-- Name: route_policy_versions route_policy_versions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY route_policy_versions
    ADD CONSTRAINT route_policy_versions_pkey PRIMARY KEY (id);


--
-- Name: session_families session_families_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY session_families
    ADD CONSTRAINT session_families_pkey PRIMARY KEY (id);


--
-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (digest);


--
-- Name: settlements settlements_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_pkey PRIMARY KEY (id);


--
-- Name: settlements settlements_request_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_request_id_key UNIQUE (request_id);


--
-- Name: settlements settlements_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: settlements settlements_tenant_id_id_key1; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_tenant_id_id_key1 UNIQUE (tenant_id, id);


--
-- Name: tenant_model_grants tenant_model_grants_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_model_grants
    ADD CONSTRAINT tenant_model_grants_pkey PRIMARY KEY (tenant_id, model_id);


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_pkey PRIMARY KEY (operation_id);


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_tenant_id_after_revision_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_tenant_id_after_revision_key UNIQUE (tenant_id, after_revision);


--
-- Name: tenants tenants_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);


--
-- Name: upstream_attempts upstream_attempts_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_pkey PRIMARY KEY (id);


--
-- Name: upstream_attempts upstream_attempts_request_id_channel_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_request_id_channel_id_key UNIQUE (request_id, channel_id);


--
-- Name: upstream_attempts upstream_attempts_request_id_sequence_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_request_id_sequence_key UNIQUE (request_id, sequence);


--
-- Name: upstream_attempts upstream_attempts_request_identity; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_request_identity UNIQUE (tenant_id, id, request_id);


--
-- Name: upstream_attempts upstream_attempts_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: voucher_batches voucher_batches_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_batches
    ADD CONSTRAINT voucher_batches_pkey PRIMARY KEY (id);


--
-- Name: voucher_policy voucher_policy_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_policy
    ADD CONSTRAINT voucher_policy_pkey PRIMARY KEY (singleton);


--
-- Name: voucher_redemptions voucher_redemptions_funding_receipt_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_funding_receipt_id_key UNIQUE (funding_receipt_id);


--
-- Name: voucher_redemptions voucher_redemptions_journal_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_journal_id_key UNIQUE (journal_id);


--
-- Name: voucher_redemptions voucher_redemptions_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_operation_id_key UNIQUE (operation_id);


--
-- Name: voucher_redemptions voucher_redemptions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_pkey PRIMARY KEY (id);


--
-- Name: voucher_redemptions voucher_redemptions_voucher_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_voucher_id_key UNIQUE (voucher_id);


--
-- Name: voucher_revocation_decisions voucher_revocation_decisions_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocation_decisions
    ADD CONSTRAINT voucher_revocation_decisions_operation_id_key UNIQUE (operation_id);


--
-- Name: voucher_revocation_decisions voucher_revocation_decisions_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocation_decisions
    ADD CONSTRAINT voucher_revocation_decisions_pkey PRIMARY KEY (id);


--
-- Name: voucher_revocation_decisions voucher_revocation_decisions_proposal_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocation_decisions
    ADD CONSTRAINT voucher_revocation_decisions_proposal_id_key UNIQUE (proposal_id);


--
-- Name: voucher_revocations voucher_revocations_id_batch_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocations
    ADD CONSTRAINT voucher_revocations_id_batch_id_key UNIQUE (id, batch_id);


--
-- Name: voucher_revocations voucher_revocations_operation_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocations
    ADD CONSTRAINT voucher_revocations_operation_id_key UNIQUE (operation_id);


--
-- Name: voucher_revocations voucher_revocations_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocations
    ADD CONSTRAINT voucher_revocations_pkey PRIMARY KEY (id);


--
-- Name: vouchers vouchers_id_batch_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY vouchers
    ADD CONSTRAINT vouchers_id_batch_id_key UNIQUE (id, batch_id);


--
-- Name: vouchers vouchers_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY vouchers
    ADD CONSTRAINT vouchers_pkey PRIMARY KEY (id);


--
-- Name: wallet_openings wallet_openings_external_ref_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY wallet_openings
    ADD CONSTRAINT wallet_openings_external_ref_key UNIQUE (external_ref);


--
-- Name: wallet_openings wallet_openings_pkey; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY wallet_openings
    ADD CONSTRAINT wallet_openings_pkey PRIMARY KEY (id);


--
-- Name: wallet_openings wallet_openings_tenant_id_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY wallet_openings
    ADD CONSTRAINT wallet_openings_tenant_id_id_key UNIQUE (tenant_id, id);


--
-- Name: wallet_openings wallet_openings_tenant_id_key; Type: CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY wallet_openings
    ADD CONSTRAINT wallet_openings_tenant_id_key UNIQUE (tenant_id);


--
-- Name: alert_commands_actor; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_commands_actor ON alert_commands USING btree (actor, created_at DESC, operation_id COLLATE "C" DESC);


--
-- Name: alert_customer_aging_source; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_customer_aging_source ON reservations USING btree (tenant_id, created_at, request_id COLLATE "C") WHERE (status = 'pending_reconciliation'::text);


--
-- Name: alert_episodes_ack_command; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_episodes_ack_command ON alert_episodes USING btree (acknowledged_command_id) WHERE (acknowledged_command_id IS NOT NULL);


--
-- Name: alert_episodes_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_episodes_page ON alert_episodes USING btree (opened_at DESC, id COLLATE "C" DESC);


--
-- Name: alert_episodes_resolve_command; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_episodes_resolve_command ON alert_episodes USING btree (resolved_command_id) WHERE (resolved_command_id IS NOT NULL);


--
-- Name: alert_episodes_rule_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_episodes_rule_page ON alert_episodes USING btree (rule_id, opened_at DESC, id COLLATE "C" DESC);


--
-- Name: alert_episodes_unresolved; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX alert_episodes_unresolved ON alert_episodes USING btree (rule_id) WHERE (resolved_at IS NULL);


--
-- Name: alert_events_command; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_events_command ON alert_episode_events USING btree (command_id) WHERE (command_id IS NOT NULL);


--
-- Name: alert_events_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_events_page ON alert_episode_events USING btree (episode_id, created_at DESC, id COLLATE "C" DESC);


--
-- Name: alert_procurement_aging_source; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_procurement_aging_source ON procurement_reservations USING btree (account_id, created_at, attempt_id COLLATE "C") WHERE (state = ANY (ARRAY['unknown'::text, 'disputed'::text]));


--
-- Name: alert_rules_last_command; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_rules_last_command ON alert_rules USING btree (last_command_id);


--
-- Name: alert_rules_live_scope; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX alert_rules_live_scope ON alert_rules USING btree (kind, scope_id) WHERE (state <> 'retired'::text);


--
-- Name: alert_rules_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_rules_page ON alert_rules USING btree (created_at DESC, id COLLATE "C" DESC);


--
-- Name: alert_runtime_due; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX alert_runtime_due ON alert_rule_runtime USING btree (next_evaluation_at, rule_id COLLATE "C") WHERE (next_evaluation_at IS NOT NULL);


--
-- Name: api_keys_tenant; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX api_keys_tenant ON api_keys USING btree (tenant_id, created_at DESC);


--
-- Name: audit_events_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX audit_events_time ON audit_events USING btree (created_at DESC, id);


--
-- Name: channel_batch_operation_unique_audit; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX channel_batch_operation_unique_audit ON audit_events USING btree (target) WHERE (action = ANY (ARRAY['channel.batch.apply'::text, 'channel.batch.reject'::text]));


--
-- Name: channel_batch_operations_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX channel_batch_operations_time ON channel_batch_operations USING btree (created_at DESC, operation_id DESC);


--
-- Name: channel_model_discoveries_channel_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX channel_model_discoveries_channel_time ON channel_model_discoveries USING btree (channel_id, created_at DESC, id DESC);


--
-- Name: channel_version_credentials_credential; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX channel_version_credentials_credential ON channel_version_credentials USING btree (credential_id, channel_version_id);


--
-- Name: commerce_accepted_resource; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX commerce_accepted_resource ON commerce_operation_receipts USING btree (kind, resource_id) WHERE ((status = 'accepted'::text) AND (kind = ANY (ARRAY['voucher.batch.create'::text, 'voucher.batch.approve'::text, 'voucher.batch.void'::text, 'voucher.revocation.create'::text, 'voucher.revocation.approve'::text])));


--
-- Name: cost_versions_current_quote; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX cost_versions_current_quote ON cost_versions USING btree (channel_id, upstream_model, schema_id, variant_key, effective_at DESC, created_at DESC, id DESC);


--
-- Name: delivery_observations_request; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX delivery_observations_request ON delivery_observations USING btree (request_id, created_at DESC);


--
-- Name: funding_receipts_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX funding_receipts_page ON funding_receipts USING btree (tenant_id, created_at DESC, id COLLATE "C" DESC);


--
-- Name: identity_challenges_active; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX identity_challenges_active ON identity_challenges USING btree (subject_id) WHERE ((consumed_at IS NULL) AND (revoked_at IS NULL));


--
-- Name: identity_challenges_expiry; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX identity_challenges_expiry ON identity_challenges USING btree (expires_at, id);


--
-- Name: identity_mail_outbox_pending; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX identity_mail_outbox_pending ON identity_mail_outbox USING btree (next_attempt_at, id) WHERE (state = 'pending'::text);


--
-- Name: identity_mail_outbox_sending; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX identity_mail_outbox_sending ON identity_mail_outbox USING btree (lease_until, id) WHERE (state = 'sending'::text);


--
-- Name: journals_tenant_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX journals_tenant_time ON journals USING btree (tenant_id, created_at DESC, id);


--
-- Name: ledger_customer_account; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX ledger_customer_account ON ledger_accounts USING btree (tenant_id, kind) WHERE (tenant_id IS NOT NULL);


--
-- Name: ledger_platform_account; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX ledger_platform_account ON ledger_accounts USING btree (kind) WHERE (tenant_id IS NULL);


--
-- Name: models_public_catalog; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX models_public_catalog ON models USING btree (public_name COLLATE "C", id COLLATE "C") WHERE (public_visible AND enabled);


--
-- Name: payment_capture_audit_once; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX payment_capture_audit_once ON audit_events USING btree (action, target) WHERE (action = 'payment.capture.credited'::text);


--
-- Name: payment_capture_tenant; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_capture_tenant ON payment_captures USING btree (tenant_id, created_at DESC, id COLLATE "C" DESC);


--
-- Name: payment_evidence_claim; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_evidence_claim ON payment_evidence_processing USING btree (next_attempt_at, evidence_id) WHERE (state = ANY (ARRAY['pending'::text, 'retry_wait'::text]));


--
-- Name: payment_evidence_order; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_evidence_order ON payment_verified_evidence USING btree (order_id, verified_at DESC, id COLLATE "C" DESC);


--
-- Name: payment_external_command; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX payment_external_command ON payment_command_outbox USING btree (external_command_id) WHERE (external_command_id IS NOT NULL);


--
-- Name: payment_human_audit_once; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX payment_human_audit_once ON audit_events USING btree (action, target) WHERE (action = ANY (ARRAY['payment.sku.publish'::text, 'payment.sku.activate'::text, 'payment.order.create'::text, 'payment.order.cancel'::text]));


--
-- Name: payment_one_mutation; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX payment_one_mutation ON payment_command_outbox USING btree (order_id, kind) WHERE (kind = ANY (ARRAY['checkout'::text, 'cancel'::text]));


--
-- Name: payment_one_query_supervisor; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX payment_one_query_supervisor ON payment_command_outbox USING btree (original_outbox_id) WHERE (kind = 'query'::text);


--
-- Name: payment_order_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_order_page ON payment_orders USING btree (tenant_id, created_at DESC, id COLLATE "C" DESC);


--
-- Name: payment_order_platform_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_order_platform_page ON payment_orders USING btree (created_at DESC, id COLLATE "C" DESC);


--
-- Name: payment_outbox_claim; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_outbox_claim ON payment_command_outbox USING btree (next_attempt_at, id) WHERE (state = ANY (ARRAY['pending'::text, 'retry_wait'::text, 'submission_unknown'::text]));


--
-- Name: payment_review_order; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_review_order ON payment_review_facts USING btree (order_id, created_at, id COLLATE "C");


--
-- Name: payment_sku_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX payment_sku_page ON payment_skus USING btree (created_at DESC, id COLLATE "C" DESC);


--
-- Name: pb_account_scope; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX pb_account_scope ON periodic_budget_controls USING btree (account_id) WHERE (scope = 'procurement'::text);


--
-- Name: pb_allocation_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX pb_allocation_page ON periodic_budget_allocations USING btree (control_id, created_at DESC, id DESC);


--
-- Name: pb_allocation_subject; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX pb_allocation_subject ON periodic_budget_allocations USING btree (subject_type, subject_id, control_id);


--
-- Name: pb_command_audit_unique; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX pb_command_audit_unique ON audit_events USING btree (target) WHERE (action = 'periodic_budget.configured'::text);


--
-- Name: pb_key_scope; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX pb_key_scope ON periodic_budget_controls USING btree (key_id) WHERE (scope = 'key'::text);


--
-- Name: pb_procurement_source_events; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX pb_procurement_source_events ON procurement_budget_events USING btree (attempt_id, id);


--
-- Name: pb_tenant_scope; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX pb_tenant_scope ON periodic_budget_controls USING btree (tenant_id) WHERE (scope = 'tenant'::text);


--
-- Name: price_groups_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX price_groups_time ON price_groups USING btree (created_at DESC, id DESC);


--
-- Name: price_versions_domain_lookup; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX price_versions_domain_lookup ON price_versions USING btree (model_id, price_group_id, schema_id, variant_key, effective_at DESC, created_at DESC, id COLLATE "C" DESC);


--
-- Name: price_versions_pricing_operation; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX price_versions_pricing_operation ON price_versions USING btree (pricing_operation_id) WHERE (pricing_operation_id IS NOT NULL);


--
-- Name: procurement_budget_events_account; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX procurement_budget_events_account ON procurement_budget_events USING btree (account_id, created_at DESC, id DESC);


--
-- Name: procurement_command_unique_audit; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE UNIQUE INDEX procurement_command_unique_audit ON audit_events USING btree (target) WHERE (action = ANY (ARRAY['procurement.command.applied'::text, 'procurement.command.rejected'::text]));


--
-- Name: procurement_commands_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX procurement_commands_time ON procurement_commands USING btree (created_at DESC, operation_id DESC);


--
-- Name: procurement_observations_account; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX procurement_observations_account ON procurement_balance_observations USING btree (account_id, created_at DESC, id DESC);


--
-- Name: procurement_reservations_account; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX procurement_reservations_account ON procurement_reservations USING btree (account_id, created_at DESC, attempt_id DESC);


--
-- Name: procurement_reservations_recovery; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX procurement_reservations_recovery ON procurement_reservations USING btree (state, updated_at) WHERE (state = ANY (ARRAY['prepared'::text, 'sending'::text]));


--
-- Name: requests_tenant_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX requests_tenant_time ON request_records USING btree (tenant_id, created_at DESC, id);


--
-- Name: reservations_unfinished; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX reservations_unfinished ON reservations USING btree (status, deadline);


--
-- Name: sessions_expiry; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX sessions_expiry ON sessions USING btree (expires_at);


--
-- Name: tenant_pricing_assignments_time; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX tenant_pricing_assignments_time ON tenant_pricing_assignments USING btree (tenant_id, created_at DESC, operation_id DESC);


--
-- Name: voucher_batches_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX voucher_batches_page ON voucher_batches USING btree (created_at DESC, id COLLATE "C" DESC);


--
-- Name: voucher_redemptions_batch; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX voucher_redemptions_batch ON voucher_redemptions USING btree (batch_id, id);


--
-- Name: voucher_redemptions_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX voucher_redemptions_page ON voucher_redemptions USING btree (tenant_id, created_at DESC, id COLLATE "C" DESC);


--
-- Name: voucher_revocations_page; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX voucher_revocations_page ON voucher_revocations USING btree (batch_id, created_at DESC, id COLLATE "C" DESC);


--
-- Name: vouchers_batch; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX vouchers_batch ON vouchers USING btree (batch_id, id);


--
-- Name: wallet_lot_events_journal; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX wallet_lot_events_journal ON funding_lot_events USING btree (journal_id);


--
-- Name: wallet_lot_refunds_settlement; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX wallet_lot_refunds_settlement ON funding_consumption_refund_lots USING btree (settlement_id, allocation_index);


--
-- Name: wallet_lots_fifo; Type: INDEX; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE INDEX wallet_lots_fifo ON funding_lots USING btree (tenant_id, fifo_at, id COLLATE "C");


--
-- Name: alert_commands alert_command_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_command_guard BEFORE INSERT OR DELETE OR UPDATE ON alert_commands FOR EACH ROW EXECUTE FUNCTION alert_guard_command();


--
-- Name: alert_commands alert_command_terminal; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER alert_command_terminal AFTER INSERT OR UPDATE ON alert_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_command_constraint();


--
-- Name: alert_control alert_control_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_control_immutable BEFORE DELETE OR UPDATE ON alert_control FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: alert_episodes alert_episode_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_episode_guard BEFORE INSERT OR UPDATE ON alert_episodes FOR EACH ROW EXECUTE FUNCTION alert_guard_episode();


--
-- Name: alert_episodes alert_episode_history; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER alert_episode_history AFTER INSERT OR UPDATE ON alert_episodes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_episode_history_constraint();


--
-- Name: alert_episodes alert_episode_runtime_consistent; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER alert_episode_runtime_consistent AFTER INSERT OR UPDATE ON alert_episodes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();


--
-- Name: alert_episodes alert_episodes_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_episodes_no_delete BEFORE DELETE ON alert_episodes FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: alert_episode_events alert_event_history; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER alert_event_history AFTER INSERT ON alert_episode_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_episode_history_constraint();


--
-- Name: alert_episode_events alert_events_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_events_immutable BEFORE DELETE OR UPDATE ON alert_episode_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: alert_rules alert_rule_consistent; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER alert_rule_consistent AFTER INSERT OR UPDATE ON alert_rules DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();


--
-- Name: alert_rules alert_rule_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_rule_guard BEFORE INSERT OR UPDATE ON alert_rules FOR EACH ROW EXECUTE FUNCTION alert_guard_rule();


--
-- Name: alert_rules alert_rules_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_rules_no_delete BEFORE DELETE ON alert_rules FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: alert_rule_runtime alert_runtime_consistent; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER alert_runtime_consistent AFTER INSERT OR UPDATE ON alert_rule_runtime DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();


--
-- Name: alert_rule_runtime alert_runtime_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_runtime_guard BEFORE INSERT OR UPDATE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION alert_guard_runtime();


--
-- Name: alert_rule_runtime alert_runtime_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER alert_runtime_no_delete BEFORE DELETE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: journals balanced_journal; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER balanced_journal AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_balanced_journal();


--
-- Name: channels channel_management_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER channel_management_revision BEFORE INSERT OR UPDATE ON channels FOR EACH ROW EXECUTE FUNCTION advance_channel_management_revision();


--
-- Name: channel_version_credentials channel_version_credential_creation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER channel_version_credential_creation BEFORE INSERT ON channel_version_credentials FOR EACH ROW EXECUTE FUNCTION require_credential_pool_creation_transaction();


--
-- Name: channel_versions channel_version_pool_creation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER channel_version_pool_creation BEFORE INSERT ON channel_versions FOR EACH ROW EXECUTE FUNCTION set_credential_pool_creation_xid();


--
-- Name: commerce_commands commerce_command_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER commerce_command_complete AFTER INSERT ON commerce_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION commerce_command_complete();


--
-- Name: commerce_operation_receipts commerce_receipt_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER commerce_receipt_complete AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION commerce_command_complete();


--
-- Name: credit_orders funding_credit_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER funding_credit_fact AFTER INSERT OR UPDATE ON credit_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();


--
-- Name: credit_orders funding_credit_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER funding_credit_guard BEFORE INSERT OR DELETE OR UPDATE ON credit_orders FOR EACH ROW EXECUTE FUNCTION funding_credit_guard();


--
-- Name: journals funding_journal_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER funding_journal_fact AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();


--
-- Name: projection_outbox funding_outbox_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER funding_outbox_fact AFTER INSERT ON projection_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();


--
-- Name: funding_receipts funding_receipt_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER funding_receipt_fact AFTER INSERT ON funding_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();


--
-- Name: voucher_redemptions funding_redemption_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER funding_redemption_fact AFTER INSERT ON voucher_redemptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();


--
-- Name: price_versions group_price_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER group_price_insert BEFORE INSERT ON price_versions FOR EACH ROW EXECUTE FUNCTION guard_group_price_insert();


--
-- Name: identity_challenges identity_challenge_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER identity_challenge_guard BEFORE INSERT OR DELETE OR UPDATE ON identity_challenges FOR EACH ROW EXECUTE FUNCTION identity_challenge_guard();


--
-- Name: identity_initialization identity_initialization_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER identity_initialization_guard BEFORE INSERT OR DELETE OR UPDATE ON identity_initialization FOR EACH ROW EXECUTE FUNCTION identity_initialization_guard();


--
-- Name: identity_mail_outbox identity_outbox_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER identity_outbox_guard BEFORE INSERT OR DELETE OR UPDATE ON identity_mail_outbox FOR EACH ROW EXECUTE FUNCTION identity_outbox_guard();


--
-- Name: users identity_password_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER identity_password_revision BEFORE INSERT OR UPDATE ON users FOR EACH ROW EXECUTE FUNCTION identity_password_revision_guard();


--
-- Name: identity_mail_subjects identity_subject_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER identity_subject_guard BEFORE INSERT OR DELETE OR UPDATE ON identity_mail_subjects FOR EACH ROW EXECUTE FUNCTION identity_subject_guard();


--
-- Name: attempt_meter_bound_lines immutable_attempt_meter_bound; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_attempt_meter_bound BEFORE DELETE OR UPDATE ON attempt_meter_bound_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: attempt_meter_snapshots immutable_attempt_meter_snapshot; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_attempt_meter_snapshot BEFORE DELETE OR UPDATE ON attempt_meter_snapshots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: audit_events immutable_audit; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_audit BEFORE DELETE OR UPDATE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: model_bindings immutable_binding; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_binding BEFORE DELETE OR UPDATE ON model_bindings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: binding_cost_rate_limits immutable_binding_cost_cap; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_binding_cost_cap BEFORE DELETE OR UPDATE ON binding_cost_rate_limits FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: model_capability_versions immutable_capability; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_capability BEFORE DELETE OR UPDATE ON model_capability_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: model_capability_bound_lines immutable_capability_bound; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_capability_bound BEFORE DELETE OR UPDATE ON model_capability_bound_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: model_capability_variants immutable_capability_variant; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_capability_variant BEFORE DELETE OR UPDATE ON model_capability_variants FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: channel_batch_operations immutable_channel_batch_operation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_channel_batch_operation BEFORE DELETE OR UPDATE ON channel_batch_operations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: channel_versions immutable_channel_version; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_channel_version BEFORE DELETE OR UPDATE ON channel_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: channel_version_credentials immutable_channel_version_credential; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_channel_version_credential BEFORE DELETE OR UPDATE ON channel_version_credentials FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: commerce_commands immutable_commerce_command; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_commerce_command BEFORE DELETE OR UPDATE ON commerce_commands FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: commerce_operation_receipts immutable_commerce_receipt; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_commerce_receipt BEFORE DELETE OR UPDATE ON commerce_operation_receipts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: provider_cost_corrections immutable_cost_correction; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_cost_correction BEFORE DELETE OR UPDATE ON provider_cost_corrections FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: cost_rate_lines immutable_cost_rate; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_cost_rate BEFORE DELETE OR UPDATE ON cost_rate_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: cost_versions immutable_cost_version; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_cost_version BEFORE DELETE OR UPDATE ON cost_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: delivery_observations immutable_delivery_observation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_delivery_observation BEFORE DELETE OR UPDATE ON delivery_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: finalization_records immutable_finalization; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_finalization BEFORE DELETE OR UPDATE ON finalization_records FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: financial_idempotency immutable_financial_idempotency; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_financial_idempotency BEFORE DELETE OR UPDATE ON financial_idempotency FOR EACH ROW EXECUTE FUNCTION guard_financial_idempotency();


--
-- Name: funding_receipts immutable_funding_receipt; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_funding_receipt BEFORE DELETE OR UPDATE ON funding_receipts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: journals immutable_journal; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_journal BEFORE DELETE OR UPDATE ON journals FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: journal_lines immutable_journal_line; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_journal_line BEFORE DELETE OR UPDATE ON journal_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: meter_event_quantities immutable_meter_quantity; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_meter_quantity BEFORE DELETE OR UPDATE ON meter_event_quantities FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: meter_schemas immutable_meter_schema; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_meter_schema BEFORE DELETE OR UPDATE ON meter_schemas FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: meter_schema_dimensions immutable_meter_schema_dimension; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_meter_schema_dimension BEFORE DELETE OR UPDATE ON meter_schema_dimensions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: metering_events immutable_metering; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_metering BEFORE DELETE OR UPDATE ON metering_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: metering_event_conflicts immutable_metering_conflict; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_metering_conflict BEFORE DELETE OR UPDATE ON metering_event_conflicts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: channel_model_discoveries immutable_model_discovery; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_model_discovery BEFORE DELETE OR UPDATE ON channel_model_discoveries FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: channel_model_discovery_results immutable_model_discovery_result; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_model_discovery_result BEFORE DELETE OR UPDATE ON channel_model_discovery_results FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: projection_outbox immutable_outbox; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_outbox BEFORE DELETE OR UPDATE ON projection_outbox FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: price_versions immutable_price; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_price BEFORE DELETE OR UPDATE ON price_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: price_rate_lines immutable_price_rate; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_price_rate BEFORE DELETE OR UPDATE ON price_rate_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: pricing_operations immutable_pricing_operation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_pricing_operation BEFORE DELETE OR UPDATE ON pricing_operations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: credential_procurement_bindings immutable_procurement_binding; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_procurement_binding BEFORE DELETE OR UPDATE ON credential_procurement_bindings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: procurement_budget_events immutable_procurement_budget_event; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_procurement_budget_event BEFORE DELETE OR UPDATE ON procurement_budget_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: procurement_commands immutable_procurement_command; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_procurement_command BEFORE DELETE OR UPDATE ON procurement_commands FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: procurement_evidence_identities immutable_procurement_evidence_identity; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_procurement_evidence_identity BEFORE DELETE OR UPDATE ON procurement_evidence_identities FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: procurement_balance_observations immutable_procurement_observation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_procurement_observation BEFORE DELETE OR UPDATE ON procurement_balance_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: request_records immutable_request_client_ip; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_request_client_ip BEFORE UPDATE OF client_ip ON request_records FOR EACH ROW EXECUTE FUNCTION preserve_request_client_ip();


--
-- Name: request_idempotency immutable_request_idempotency; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_request_idempotency BEFORE DELETE OR UPDATE ON request_idempotency FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: request_meter_bound_lines immutable_request_meter_bound; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_request_meter_bound BEFORE DELETE OR UPDATE ON request_meter_bound_lines FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: request_meter_snapshots immutable_request_meter_snapshot; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_request_meter_snapshot BEFORE DELETE OR UPDATE ON request_meter_snapshots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: route_binding_isolations immutable_route_binding_isolation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_route_binding_isolation BEFORE DELETE OR UPDATE ON route_binding_isolations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: upstream_attempts immutable_route_decisions; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_route_decisions BEFORE UPDATE OF route_decisions ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION protect_route_decisions();


--
-- Name: route_policy_versions immutable_route_version; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_route_version BEFORE DELETE OR UPDATE ON route_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: settlements immutable_settlement; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_settlement BEFORE DELETE OR UPDATE ON settlements FOR EACH ROW EXECUTE FUNCTION guard_settlement_history();


--
-- Name: tenant_pricing_assignments immutable_tenant_pricing_assignment; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_tenant_pricing_assignment BEFORE DELETE OR UPDATE ON tenant_pricing_assignments FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: vouchers immutable_voucher; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_voucher BEFORE DELETE OR UPDATE ON vouchers FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: voucher_revocation_decisions immutable_voucher_decision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_voucher_decision BEFORE DELETE OR UPDATE ON voucher_revocation_decisions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: voucher_redemptions immutable_voucher_redemption; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_voucher_redemption BEFORE DELETE OR UPDATE ON voucher_redemptions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: voucher_revocations immutable_voucher_revocation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_voucher_revocation BEFORE DELETE OR UPDATE ON voucher_revocations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: funding_consumption_refund_lots immutable_wallet_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_wallet_fact BEFORE DELETE OR UPDATE ON funding_consumption_refund_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: funding_lot_events immutable_wallet_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_wallet_fact BEFORE DELETE OR UPDATE ON funding_lot_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: funding_lots immutable_wallet_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_wallet_fact BEFORE DELETE OR UPDATE ON funding_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: funding_reservation_lots immutable_wallet_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_wallet_fact BEFORE DELETE OR UPDATE ON funding_reservation_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: funding_settlement_lots immutable_wallet_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_wallet_fact BEFORE DELETE OR UPDATE ON funding_settlement_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: wallet_openings immutable_wallet_opening; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER immutable_wallet_opening BEFORE DELETE OR UPDATE ON wallet_openings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: journal_lines journal_line_current_transaction; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER journal_line_current_transaction BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION guard_journal_line_insert();


--
-- Name: api_keys key_access_policy_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER key_access_policy_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_access_policy_revision();


--
-- Name: api_keys key_configuration_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER key_configuration_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_configuration_revision();


--
-- Name: api_keys key_runtime_limits_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER key_runtime_limits_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_runtime_limit_revision();


--
-- Name: media_task_artifacts media_artifact_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_artifact_guard BEFORE INSERT OR UPDATE ON media_task_artifacts FOR EACH ROW EXECUTE FUNCTION media_artifact_guard();


--
-- Name: media_task_artifacts media_artifact_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_artifact_immutable BEFORE DELETE ON media_task_artifacts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_attempts media_attempt_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_attempt_guard BEFORE INSERT OR UPDATE ON media_attempts FOR EACH ROW EXECUTE FUNCTION media_attempt_guard();


--
-- Name: media_attempts media_attempt_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_attempt_immutable BEFORE DELETE ON media_attempts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_observations media_observation_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER media_observation_guard AFTER INSERT ON media_observations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION media_observation_guard();


--
-- Name: media_observations media_observation_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_observation_immutable BEFORE DELETE OR UPDATE ON media_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_model_profiles media_profile_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_profile_immutable BEFORE DELETE OR UPDATE ON media_model_profiles FOR EACH ROW EXECUTE FUNCTION media_profile_immutable();


--
-- Name: media_requests media_request_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_request_guard BEFORE INSERT OR UPDATE ON media_requests FOR EACH ROW EXECUTE FUNCTION media_request_guard();


--
-- Name: media_requests media_request_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_request_immutable BEFORE DELETE ON media_requests FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_reservations media_reservation_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_reservation_guard BEFORE INSERT OR UPDATE ON media_reservations FOR EACH ROW EXECUTE FUNCTION media_reservation_guard();


--
-- Name: media_reservations media_reservation_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_reservation_immutable BEFORE DELETE ON media_reservations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_reservation_lots media_reservation_lot_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_reservation_lot_immutable BEFORE DELETE OR UPDATE ON media_reservation_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_result_artifacts media_result_artifact_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_result_artifact_immutable BEFORE DELETE OR UPDATE ON media_result_artifacts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_settlements media_settlement_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER media_settlement_guard AFTER INSERT ON media_settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION media_settlement_guard();


--
-- Name: media_settlements media_settlement_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_settlement_immutable BEFORE DELETE OR UPDATE ON media_settlements FOR EACH ROW EXECUTE FUNCTION media_settlement_immutable();


--
-- Name: media_settlement_lots media_settlement_lot_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_settlement_lot_immutable BEFORE DELETE OR UPDATE ON media_settlement_lots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_task_events media_task_event_apply; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_task_event_apply AFTER INSERT ON media_task_events FOR EACH ROW EXECUTE FUNCTION media_task_event_apply();


--
-- Name: media_task_events media_task_event_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_task_event_guard BEFORE INSERT ON media_task_events FOR EACH ROW EXECUTE FUNCTION media_task_event_guard();


--
-- Name: media_task_events media_task_event_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_task_event_immutable BEFORE DELETE OR UPDATE ON media_task_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: media_tasks media_task_identity; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_task_identity BEFORE INSERT OR UPDATE ON media_tasks FOR EACH ROW EXECUTE FUNCTION media_task_identity_guard();


--
-- Name: media_tasks media_task_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER media_task_immutable BEFORE DELETE ON media_tasks FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: attempt_meter_snapshots meter_attempt_bounds_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_bounds_complete AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_bounds_complete();


--
-- Name: upstream_attempts meter_attempt_cost_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_cost_matches AFTER UPDATE ON upstream_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_cost_matches();


--
-- Name: attempt_meter_snapshots meter_attempt_matches_request; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_matches_request AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_matches_request();


--
-- Name: attempt_meter_snapshots meter_attempt_plan_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_plan_matches AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_plan_matches();


--
-- Name: attempt_meter_snapshots meter_attempt_quote_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_quote_matches AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_quote_matches();


--
-- Name: upstream_attempts meter_attempt_snapshot_present; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_snapshot_present AFTER INSERT ON upstream_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_snapshot_present();


--
-- Name: attempt_meter_snapshots meter_attempt_vectors_match; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_attempt_vectors_match AFTER INSERT ON attempt_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_attempt_vectors_match();


--
-- Name: model_bindings meter_binding_birth; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_binding_birth BEFORE INSERT ON model_bindings FOR EACH ROW EXECUTE FUNCTION meter_binding_birth();


--
-- Name: binding_cost_rate_limits meter_binding_cap_birth; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_binding_cap_birth BEFORE INSERT ON binding_cost_rate_limits FOR EACH ROW EXECUTE FUNCTION meter_binding_cap_birth();


--
-- Name: model_capability_bound_lines meter_capability_bound_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_capability_bound_complete AFTER INSERT ON model_capability_bound_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_capability_complete();


--
-- Name: model_capability_versions meter_capability_root_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_capability_root_complete AFTER INSERT ON model_capability_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_capability_complete();


--
-- Name: model_capability_variants meter_capability_variant_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_capability_variant_complete AFTER INSERT ON model_capability_variants DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_capability_complete();


--
-- Name: cost_rate_lines meter_cost_line_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_cost_line_complete AFTER INSERT ON cost_rate_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_cost_lines_complete();


--
-- Name: cost_versions meter_cost_operation_birth; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_cost_operation_birth BEFORE INSERT ON cost_versions FOR EACH ROW EXECUTE FUNCTION meter_cost_operation_birth();


--
-- Name: cost_versions meter_cost_root_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_cost_root_complete AFTER INSERT ON cost_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_cost_lines_complete();


--
-- Name: metering_events meter_event_attempt_bound; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_event_attempt_bound AFTER INSERT ON metering_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_event_attempt_bound();


--
-- Name: metering_events meter_event_observation_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_event_observation_complete AFTER INSERT ON metering_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_event_observation_complete();


--
-- Name: metering_events meter_event_rows_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_event_rows_complete AFTER INSERT ON metering_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_event_rows_complete();


--
-- Name: finalization_records meter_finalization_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_finalization_complete AFTER INSERT ON finalization_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_finalization_complete();


--
-- Name: audit_events meter_group_price_audit_birth; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_group_price_audit_birth BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION meter_group_price_audit_birth();


--
-- Name: audit_events meter_group_price_audit_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_group_price_audit_insert AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_group_price_audit_insert();


--
-- Name: models meter_model_route_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_model_route_matches AFTER INSERT OR UPDATE ON models DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_model_route_matches();


--
-- Name: price_rate_lines meter_price_line_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_price_line_complete AFTER INSERT ON price_rate_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_price_complete();


--
-- Name: price_versions meter_price_root_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_price_root_complete AFTER INSERT ON price_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_price_complete();


--
-- Name: procurement_reservations meter_procurement_attempt_closed; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_procurement_attempt_closed AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_procurement_attempt_closed();


--
-- Name: procurement_reservations meter_procurement_bound_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_procurement_bound_matches AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_procurement_bound_matches();


--
-- Name: procurement_cost_proposals meter_procurement_proposal_current; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_procurement_proposal_current AFTER INSERT OR UPDATE ON procurement_cost_proposals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_procurement_proposal_current();


--
-- Name: reconciliation_cases meter_reconciliation_proposal_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_reconciliation_proposal_complete BEFORE INSERT OR UPDATE ON reconciliation_cases FOR EACH ROW EXECUTE FUNCTION meter_reconciliation_proposal_complete();


--
-- Name: request_meter_snapshots meter_request_bounds_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_bounds_complete AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_bounds_complete();


--
-- Name: request_records meter_request_finalization_pointer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_finalization_pointer AFTER UPDATE ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_finalization_pointer();


--
-- Name: request_meter_snapshots meter_request_plan_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_plan_matches AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_plan_matches();


--
-- Name: request_meter_snapshots meter_request_price_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_price_matches AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_price_matches();


--
-- Name: request_records meter_request_quote_identity; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_request_quote_identity BEFORE UPDATE ON request_records FOR EACH ROW EXECUTE FUNCTION guard_request_retail_snapshot();


--
-- Name: request_meter_snapshots meter_request_quote_matches; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_quote_matches AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_quote_matches();


--
-- Name: request_records meter_request_snapshot_present; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_snapshot_present AFTER INSERT ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_snapshot_present();


--
-- Name: request_meter_snapshots meter_request_vectors; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_request_vectors BEFORE INSERT ON request_meter_snapshots FOR EACH ROW EXECUTE FUNCTION meter_request_vectors();


--
-- Name: request_meter_snapshots meter_request_vectors_match; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_request_vectors_match AFTER INSERT ON request_meter_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_request_vectors_match();


--
-- Name: reservations meter_reservation_amount; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_reservation_amount AFTER INSERT ON reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_reservation_amount();


--
-- Name: model_bindings meter_route_binding_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_route_binding_complete AFTER INSERT ON model_bindings DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_route_complete();


--
-- Name: binding_cost_rate_limits meter_route_cap_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_route_cap_complete AFTER INSERT ON binding_cost_rate_limits DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_route_complete();


--
-- Name: route_policy_versions meter_route_profile; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_route_profile BEFORE INSERT ON route_policy_versions FOR EACH ROW EXECUTE FUNCTION meter_route_profile();


--
-- Name: route_policy_versions meter_route_root_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_route_root_complete AFTER INSERT ON route_policy_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_route_complete();


--
-- Name: meter_schemas meter_schema_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_schema_complete AFTER INSERT ON meter_schemas DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_schema_complete();


--
-- Name: meter_schema_dimensions meter_schema_dimension_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_schema_dimension_complete AFTER INSERT ON meter_schema_dimensions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_schema_complete();


--
-- Name: meter_schemas meter_schema_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER meter_schema_stamp BEFORE INSERT ON meter_schemas FOR EACH ROW EXECUTE FUNCTION meter_schema_stamp();


--
-- Name: settlements meter_settlement_amount; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_settlement_amount AFTER INSERT ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_settlement_amount();


--
-- Name: settlements meter_settlement_finalization; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER meter_settlement_finalization AFTER INSERT ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION meter_settlement_finalization();


--
-- Name: models model_public_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER model_public_revision BEFORE INSERT OR UPDATE ON models FOR EACH ROW EXECUTE FUNCTION guard_model_public_revision();


--
-- Name: price_groups no_delete_price_group; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER no_delete_price_group BEFORE DELETE ON price_groups FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: audit_events payment_audit_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_audit_complete AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_audit_complete();


--
-- Name: audit_events payment_audit_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_audit_guard BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION payment_audit_guard();


--
-- Name: payment_cancel_intents payment_cancel_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_cancel_complete AFTER INSERT ON payment_cancel_intents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_cancel_intents payment_cancel_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_cancel_insert BEFORE INSERT ON payment_cancel_intents FOR EACH ROW EXECUTE FUNCTION payment_cancel_insert();


--
-- Name: payment_cancel_intents payment_cancel_intents_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_cancel_intents_immutable BEFORE DELETE OR UPDATE ON payment_cancel_intents FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_captures payment_capture_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_capture_complete AFTER INSERT ON payment_captures DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();


--
-- Name: payment_captures payment_capture_generation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_capture_generation BEFORE INSERT ON payment_captures FOR EACH ROW EXECUTE FUNCTION payment_capture_generation();


--
-- Name: payment_captures payment_capture_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_capture_writer BEFORE INSERT ON payment_captures FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_captures payment_captures_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_captures_immutable BEFORE DELETE OR UPDATE ON payment_captures FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_command_observations payment_command_observations_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_command_observations_immutable BEFORE DELETE OR UPDATE ON payment_command_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: projection_outbox payment_credited_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_credited_writer BEFORE INSERT ON projection_outbox FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_effects payment_effect_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_effect_complete AFTER INSERT ON payment_effects DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();


--
-- Name: payment_effects payment_effect_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_effect_writer BEFORE INSERT ON payment_effects FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_effects payment_effects_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_effects_immutable BEFORE DELETE OR UPDATE ON payment_effects FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_sandbox_environment payment_environment_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_environment_immutable BEFORE DELETE OR UPDATE ON payment_sandbox_environment FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_event_conflicts payment_event_conflicts_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_event_conflicts_immutable BEFORE DELETE OR UPDATE ON payment_event_conflicts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_verified_evidence payment_evidence_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_evidence_complete AFTER INSERT ON payment_verified_evidence DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_evidence_complete();


--
-- Name: payment_verified_evidence payment_evidence_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_evidence_insert BEFORE INSERT ON payment_verified_evidence FOR EACH ROW EXECUTE FUNCTION payment_evidence_insert();


--
-- Name: commerce_commands payment_human_command_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_human_command_complete AFTER INSERT ON commerce_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: commerce_operation_receipts payment_human_terminal_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_human_terminal_complete AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_transaction_identities payment_identity_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_identity_complete AFTER INSERT ON payment_transaction_identities DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();


--
-- Name: payment_transaction_identities payment_identity_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_identity_writer BEFORE INSERT ON payment_transaction_identities FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_inbox payment_inbox_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_inbox_immutable BEFORE DELETE OR UPDATE ON payment_inbox FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: journals payment_journal_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_journal_writer BEFORE INSERT ON journals FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_command_outbox payment_mutation_outbox_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_mutation_outbox_complete AFTER INSERT ON payment_command_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_inbox payment_notification_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_notification_complete AFTER INSERT ON payment_inbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_original_complete();


--
-- Name: payment_inbox payment_notification_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_notification_stamp BEFORE INSERT ON payment_inbox FOR EACH ROW EXECUTE FUNCTION payment_original_insert();


--
-- Name: payment_command_observations payment_observation_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_observation_insert BEFORE INSERT ON payment_command_observations FOR EACH ROW EXECUTE FUNCTION payment_observation_insert();


--
-- Name: payment_order_capture_selections payment_order_capture_selections_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_order_capture_selections_immutable BEFORE DELETE OR UPDATE ON payment_order_capture_selections FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_orders payment_order_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_order_complete AFTER INSERT ON payment_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_orders payment_order_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_order_immutable BEFORE DELETE OR UPDATE ON payment_orders FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_orders payment_order_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_order_insert BEFORE INSERT ON payment_orders FOR EACH ROW EXECUTE FUNCTION payment_order_insert();


--
-- Name: payment_command_outbox payment_outbox_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_outbox_complete AFTER INSERT OR UPDATE ON payment_command_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_outbox_complete();


--
-- Name: payment_command_outbox payment_outbox_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_outbox_insert BEFORE INSERT ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION payment_outbox_insert();


--
-- Name: payment_command_outbox payment_outbox_transition; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_outbox_transition BEFORE DELETE OR UPDATE ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION payment_outbox_transition();


--
-- Name: payment_policy payment_policy_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_policy_guard BEFORE DELETE OR UPDATE ON payment_policy FOR EACH ROW EXECUTE FUNCTION payment_policy_guard();


--
-- Name: payment_evidence_processing payment_processing_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_processing_guard BEFORE INSERT OR DELETE OR UPDATE ON payment_evidence_processing FOR EACH ROW EXECUTE FUNCTION payment_processing_guard();


--
-- Name: payment_provider_revisions payment_provider_revisions_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_provider_revisions_immutable BEFORE DELETE OR UPDATE ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_query_attempts payment_query_attempt_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_query_attempt_insert BEFORE INSERT ON payment_query_attempts FOR EACH ROW EXECUTE FUNCTION payment_query_attempt_insert();


--
-- Name: payment_query_attempts payment_query_attempts_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_query_attempts_immutable BEFORE DELETE OR UPDATE ON payment_query_attempts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_query_conflicts payment_query_conflicts_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_query_conflicts_immutable BEFORE DELETE OR UPDATE ON payment_query_conflicts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_verified_queries payment_query_original_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_query_original_complete AFTER INSERT ON payment_verified_queries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_original_complete();


--
-- Name: payment_verified_queries payment_query_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_query_stamp BEFORE INSERT ON payment_verified_queries FOR EACH ROW EXECUTE FUNCTION payment_original_insert();


--
-- Name: funding_receipts payment_receipt_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_receipt_writer BEFORE INSERT ON funding_receipts FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_review_facts payment_review_facts_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_review_facts_immutable BEFORE DELETE OR UPDATE ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_review_facts payment_review_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_review_insert BEFORE INSERT ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION payment_review_insert();


--
-- Name: payment_review_facts payment_review_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_review_writer BEFORE INSERT ON payment_review_facts FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_provider_revisions payment_revision_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_revision_insert BEFORE INSERT ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION payment_revision_insert();


--
-- Name: payment_provider_revisions payment_revision_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_revision_writer BEFORE INSERT ON payment_provider_revisions FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_runtime_attestations payment_runtime_attestation_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_runtime_attestation_immutable BEFORE DELETE OR UPDATE ON payment_runtime_attestations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: journal_lines payment_sandbox_clearing_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_sandbox_clearing_complete AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_extra_funding_complete();


--
-- Name: journals payment_sandbox_journal_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_sandbox_journal_complete AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_extra_funding_complete();


--
-- Name: payment_order_capture_selections payment_selection_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_selection_complete AFTER INSERT ON payment_order_capture_selections DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_source_complete();


--
-- Name: payment_order_capture_selections payment_selection_writer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_selection_writer BEFORE INSERT ON payment_order_capture_selections FOR EACH ROW EXECUTE FUNCTION payment_source_writer();


--
-- Name: payment_sku_changes payment_sku_change_apply; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_sku_change_apply AFTER INSERT ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION payment_sku_change_apply();


--
-- Name: payment_sku_changes payment_sku_change_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_sku_change_complete AFTER INSERT ON payment_sku_changes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_sku_changes payment_sku_change_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_sku_change_insert BEFORE INSERT ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION payment_sku_change_insert();


--
-- Name: payment_sku_changes payment_sku_changes_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_sku_changes_immutable BEFORE DELETE OR UPDATE ON payment_sku_changes FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_skus payment_sku_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_sku_complete AFTER INSERT OR UPDATE ON payment_skus DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_skus payment_sku_root_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_sku_root_guard BEFORE INSERT OR DELETE OR UPDATE ON payment_skus FOR EACH ROW EXECUTE FUNCTION payment_sku_root_guard();


--
-- Name: payment_sku_versions payment_sku_version_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_sku_version_complete AFTER INSERT ON payment_sku_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_human_complete();


--
-- Name: payment_sku_versions payment_sku_version_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_sku_version_insert BEFORE INSERT ON payment_sku_versions FOR EACH ROW EXECUTE FUNCTION payment_sku_version_insert();


--
-- Name: payment_sku_versions payment_sku_versions_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_sku_versions_immutable BEFORE DELETE OR UPDATE ON payment_sku_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_transaction_identities payment_transaction_identities_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_transaction_identities_immutable BEFORE DELETE OR UPDATE ON payment_transaction_identities FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_verified_evidence payment_verified_evidence_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_verified_evidence_immutable BEFORE DELETE OR UPDATE ON payment_verified_evidence FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: payment_verified_queries payment_verified_queries_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_verified_queries_immutable BEFORE DELETE OR UPDATE ON payment_verified_queries FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: ledger_accounts payment_worker_account_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_worker_account_complete AFTER INSERT OR UPDATE ON ledger_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_worker_balance_complete();


--
-- Name: ledger_accounts payment_worker_account_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_worker_account_guard BEFORE INSERT OR DELETE OR UPDATE ON ledger_accounts FOR EACH ROW EXECUTE FUNCTION payment_worker_account_guard();


--
-- Name: journal_lines payment_worker_line_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER payment_worker_line_complete AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION payment_worker_balance_complete();


--
-- Name: journal_lines payment_worker_line_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_worker_line_guard BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION payment_worker_line_guard();


--
-- Name: tenants payment_worker_tenant_lock_only; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER payment_worker_tenant_lock_only BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION payment_worker_tenant_lock_only();


--
-- Name: periodic_budget_allocations pb_allocation_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_allocation_guard BEFORE INSERT OR UPDATE ON periodic_budget_allocations FOR EACH ROW EXECUTE FUNCTION pb_guard_allocation();


--
-- Name: periodic_budget_allocations pb_allocation_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_allocation_no_delete BEFORE DELETE ON periodic_budget_allocations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: periodic_budget_allocations pb_allocation_source_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_allocation_source_check AFTER INSERT OR UPDATE ON periodic_budget_allocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();


--
-- Name: upstream_attempts pb_attempt_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_attempt_stamp BEFORE INSERT OR DELETE OR UPDATE ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION pb_stamp_attempt();


--
-- Name: periodic_budget_controls pb_configuration_pointer; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_configuration_pointer BEFORE UPDATE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION pb_guard_configuration_pointer();


--
-- Name: periodic_budget_controls pb_configuration_receipt; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_configuration_receipt AFTER INSERT OR UPDATE ON periodic_budget_controls DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_configuration();


--
-- Name: periodic_budget_controls pb_control_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_control_guard BEFORE INSERT OR UPDATE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION pb_guard_control();


--
-- Name: periodic_budget_controls pb_control_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_control_no_delete BEFORE DELETE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: reservations pb_customer_reservation_identity; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_customer_reservation_identity BEFORE INSERT OR DELETE OR UPDATE ON reservations FOR EACH ROW EXECUTE FUNCTION pb_guard_customer_reservation();


--
-- Name: upstream_attempts pb_dispatch_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_dispatch_guard BEFORE INSERT OR UPDATE ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION pb_guard_dispatch();


--
-- Name: periodic_budget_events pb_event_apply; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_event_apply AFTER INSERT ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION pb_apply_event();


--
-- Name: periodic_budget_events pb_event_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_event_guard BEFORE INSERT ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION pb_guard_event();


--
-- Name: periodic_budget_events pb_event_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_event_immutable BEFORE DELETE OR UPDATE ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: periodic_budget_controls pb_initial_import_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_initial_import_check AFTER INSERT ON periodic_budget_controls DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_initial_import();


--
-- Name: periodic_budget_operations pb_operation_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_operation_guard BEFORE INSERT ON periodic_budget_operations FOR EACH ROW EXECUTE FUNCTION pb_guard_operation();


--
-- Name: periodic_budget_operations pb_operation_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_operation_immutable BEFORE DELETE OR UPDATE ON periodic_budget_operations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: periodic_budget_operations pb_operation_record; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_operation_record AFTER INSERT ON periodic_budget_operations FOR EACH ROW EXECUTE FUNCTION pb_record_operation();


--
-- Name: procurement_budget_events pb_procurement_event_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_procurement_event_check AFTER INSERT ON procurement_budget_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();


--
-- Name: procurement_budget_events pb_procurement_event_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_procurement_event_stamp BEFORE INSERT ON procurement_budget_events FOR EACH ROW EXECUTE FUNCTION pb_stamp_procurement_event();


--
-- Name: procurement_reservations pb_procurement_source_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_procurement_source_check AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();


--
-- Name: procurement_reservations pb_procurement_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_procurement_stamp BEFORE INSERT OR UPDATE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION pb_stamp_procurement();


--
-- Name: request_records pb_request_source_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_request_source_check AFTER INSERT OR UPDATE ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();


--
-- Name: request_records pb_request_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_request_stamp BEFORE INSERT OR DELETE OR UPDATE ON request_records FOR EACH ROW EXECUTE FUNCTION pb_stamp_request();


--
-- Name: reservations pb_reservation_source_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_reservation_source_check AFTER INSERT OR UPDATE ON reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();


--
-- Name: settlements pb_settlement_source_check; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pb_settlement_source_check AFTER INSERT OR UPDATE ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();


--
-- Name: periodic_budget_configuration_versions pb_version_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_version_guard BEFORE INSERT ON periodic_budget_configuration_versions FOR EACH ROW EXECUTE FUNCTION pb_guard_version();


--
-- Name: periodic_budget_configuration_versions pb_version_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_version_immutable BEFORE DELETE OR UPDATE ON periodic_budget_configuration_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: periodic_budget_windows pb_window_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_window_guard BEFORE INSERT OR UPDATE ON periodic_budget_windows FOR EACH ROW EXECUTE FUNCTION pb_guard_window();


--
-- Name: periodic_budget_windows pb_window_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pb_window_no_delete BEFORE DELETE ON periodic_budget_windows FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: price_groups price_group_change; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER price_group_change BEFORE INSERT OR UPDATE ON price_groups FOR EACH ROW EXECUTE FUNCTION guard_price_group_change();


--
-- Name: price_groups price_group_operation; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER price_group_operation AFTER INSERT OR UPDATE ON price_groups DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_price_group_operation();


--
-- Name: pricing_operations pricing_operation_insert; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER pricing_operation_insert BEFORE INSERT ON pricing_operations FOR EACH ROW EXECUTE FUNCTION guard_pricing_operation_insert();


--
-- Name: pricing_operations pricing_operation_result; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER pricing_operation_result AFTER INSERT ON pricing_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_pricing_operation_result();


--
-- Name: procurement_accounts procurement_account_budget_balance; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER procurement_account_budget_balance AFTER INSERT OR UPDATE ON procurement_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_procurement_budget_projection();


--
-- Name: procurement_accounts procurement_account_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_account_no_delete BEFORE DELETE ON procurement_accounts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: procurement_accounts procurement_account_revision; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_account_revision BEFORE INSERT OR UPDATE ON procurement_accounts FOR EACH ROW EXECUTE FUNCTION procurement_account_configuration_revision();


--
-- Name: credential_procurement_bindings procurement_binding_synchronize; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_binding_synchronize BEFORE INSERT ON credential_procurement_bindings FOR EACH ROW EXECUTE FUNCTION synchronize_procurement_binding_insert();


--
-- Name: procurement_cost_proposals procurement_cost_proposal_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_cost_proposal_no_delete BEFORE DELETE ON procurement_cost_proposals FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: procurement_cost_proposals procurement_cost_proposal_transition; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_cost_proposal_transition BEFORE UPDATE ON procurement_cost_proposals FOR EACH ROW EXECUTE FUNCTION guard_procurement_cost_proposal();


--
-- Name: procurement_budget_events procurement_event_budget_balance; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER procurement_event_budget_balance AFTER INSERT ON procurement_budget_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_procurement_budget_projection();


--
-- Name: procurement_reservations procurement_reservation_budget_balance; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER procurement_reservation_budget_balance AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION assert_procurement_budget_projection();


--
-- Name: procurement_reservations procurement_reservation_identity; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_reservation_identity BEFORE INSERT OR UPDATE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION guard_procurement_reservation_identity();


--
-- Name: procurement_reservations procurement_reservation_no_delete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER procurement_reservation_no_delete BEFORE DELETE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: realtime_events realtime_event_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER realtime_event_guard BEFORE INSERT OR UPDATE ON realtime_events FOR EACH ROW EXECUTE FUNCTION realtime_event_guard();


--
-- Name: realtime_events realtime_event_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER realtime_event_immutable BEFORE DELETE OR UPDATE ON realtime_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: realtime_sessions realtime_session_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER realtime_session_guard BEFORE INSERT OR UPDATE ON realtime_sessions FOR EACH ROW EXECUTE FUNCTION realtime_session_guard();


--
-- Name: realtime_sessions realtime_session_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER realtime_session_immutable BEFORE DELETE ON realtime_sessions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: realtime_turns realtime_turn_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER realtime_turn_guard BEFORE INSERT OR UPDATE ON realtime_turns FOR EACH ROW EXECUTE FUNCTION realtime_turn_guard();


--
-- Name: realtime_turns realtime_turn_immutable; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER realtime_turn_immutable BEFORE DELETE ON realtime_turns FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();


--
-- Name: realtime_turns realtime_turn_inflight; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER realtime_turn_inflight AFTER INSERT OR UPDATE ON realtime_turns DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION realtime_turn_inflight_guard();


--
-- Name: recovery_gate recovery_gate_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER recovery_gate_guard BEFORE DELETE OR UPDATE ON recovery_gate FOR EACH ROW EXECUTE FUNCTION guard_recovery_gate();


--
-- Name: request_records request_retail_snapshot; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER request_retail_snapshot BEFORE INSERT OR UPDATE ON request_records FOR EACH ROW EXECUTE FUNCTION guard_request_retail_snapshot();


--
-- Name: cost_versions synchronize_cost_version; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER synchronize_cost_version BEFORE INSERT ON cost_versions FOR EACH ROW EXECUTE FUNCTION synchronize_cost_version_insert();


--
-- Name: tenant_pricing_assignments tenant_pricing_assignment_link; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER tenant_pricing_assignment_link AFTER INSERT ON tenant_pricing_assignments DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_tenant_pricing_assignment();


--
-- Name: tenants tenant_pricing_change; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER tenant_pricing_change BEFORE INSERT OR UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION guard_tenant_pricing_change();


--
-- Name: voucher_batches voucher_batch_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER voucher_batch_complete AFTER INSERT OR UPDATE ON voucher_batches DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();


--
-- Name: voucher_batches voucher_batch_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER voucher_batch_guard BEFORE INSERT OR DELETE OR UPDATE ON voucher_batches FOR EACH ROW EXECUTE FUNCTION voucher_batch_guard();


--
-- Name: voucher_redemptions voucher_batch_redemptions_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER voucher_batch_redemptions_complete AFTER INSERT ON voucher_redemptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();


--
-- Name: vouchers voucher_batch_vouchers_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER voucher_batch_vouchers_complete AFTER INSERT ON vouchers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();


--
-- Name: voucher_revocation_decisions voucher_decision_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER voucher_decision_complete AFTER INSERT ON voucher_revocation_decisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_revocation_complete();


--
-- Name: voucher_policy voucher_policy_guard; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER voucher_policy_guard BEFORE INSERT OR DELETE OR UPDATE ON voucher_policy FOR EACH ROW EXECUTE FUNCTION voucher_policy_guard();


--
-- Name: voucher_revocations voucher_proposal_snapshot; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER voucher_proposal_snapshot BEFORE INSERT ON voucher_revocations FOR EACH ROW EXECUTE FUNCTION voucher_proposal_snapshot();


--
-- Name: voucher_redemptions voucher_redemption_count; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER voucher_redemption_count AFTER INSERT ON voucher_redemptions FOR EACH ROW EXECUTE FUNCTION voucher_redemption_count();


--
-- Name: voucher_revocations voucher_revocation_complete; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER voucher_revocation_complete AFTER INSERT ON voucher_revocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_revocation_complete();


--
-- Name: funding_lot_balances wallet_balance_write; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER wallet_balance_write BEFORE INSERT OR DELETE OR UPDATE ON funding_lot_balances FOR EACH ROW EXECUTE FUNCTION wallet_lot_balance_guard();


--
-- Name: funding_lot_events wallet_event_stamp; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER wallet_event_stamp BEFORE INSERT ON funding_lot_events FOR EACH ROW EXECUTE FUNCTION wallet_lot_event_stamp();


--
-- Name: funding_reservation_lots wallet_reservation_fifo; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE TRIGGER wallet_reservation_fifo BEFORE INSERT ON funding_reservation_lots FOR EACH ROW EXECUTE FUNCTION wallet_reservation_fifo_guard();


--
-- Name: funding_consumption_refund_lots wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_consumption_refund_lots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: funding_lot_balances wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_lot_balances DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: funding_lot_events wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_lot_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: funding_lots wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_lots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: funding_receipts wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: funding_reservation_lots wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_reservation_lots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: funding_settlement_lots wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON funding_settlement_lots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: journals wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: ledger_accounts wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON ledger_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: refund_orders wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON refund_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: reservations wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: settlements wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: wallet_openings wallet_source_fact; Type: TRIGGER; Schema: dump_current_3b04ba63ced5; Owner: -
--

CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON wallet_openings DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();


--
-- Name: alert_episode_events alert_episode_events_command_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episode_events
    ADD CONSTRAINT alert_episode_events_command_id_fkey FOREIGN KEY (command_id) REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: alert_episode_events alert_episode_events_episode_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episode_events
    ADD CONSTRAINT alert_episode_events_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES alert_episodes(id);


--
-- Name: alert_episodes alert_episodes_acknowledged_command_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episodes
    ADD CONSTRAINT alert_episodes_acknowledged_command_id_fkey FOREIGN KEY (acknowledged_command_id) REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: alert_episodes alert_episodes_resolved_command_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episodes
    ADD CONSTRAINT alert_episodes_resolved_command_id_fkey FOREIGN KEY (resolved_command_id) REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: alert_episodes alert_episodes_rule_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_episodes
    ADD CONSTRAINT alert_episodes_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES alert_rules(id);


--
-- Name: alert_rule_runtime alert_rule_runtime_rule_id_current_episode_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_rule_runtime
    ADD CONSTRAINT alert_rule_runtime_rule_id_current_episode_id_fkey FOREIGN KEY (rule_id, current_episode_id) REFERENCES alert_episodes(rule_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: alert_rule_runtime alert_rule_runtime_rule_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_rule_runtime
    ADD CONSTRAINT alert_rule_runtime_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES alert_rules(id);


--
-- Name: alert_rules alert_rules_last_command_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY alert_rules
    ADD CONSTRAINT alert_rules_last_command_id_fkey FOREIGN KEY (last_command_id) REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: api_keys api_keys_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: api_keys api_keys_tenant_id_owner_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_tenant_id_owner_id_fkey FOREIGN KEY (tenant_id, owner_id) REFERENCES memberships(tenant_id, user_id);


--
-- Name: attempt_meter_bound_lines attempt_meter_bound_lines_tenant_id_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_bound_lines
    ADD CONSTRAINT attempt_meter_bound_lines_tenant_id_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempt_meter_snapshots(tenant_id, attempt_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_cost_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_cost_version_id_fkey FOREIGN KEY (cost_version_id) REFERENCES cost_versions(id);


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_schema_id_schema_digest_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_schema_id_schema_digest_fkey FOREIGN KEY (schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_tenant_id_attempt_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_tenant_id_attempt_id_request_id_fkey FOREIGN KEY (tenant_id, attempt_id, request_id) REFERENCES upstream_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: attempt_meter_snapshots attempt_meter_snapshots_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY attempt_meter_snapshots
    ADD CONSTRAINT attempt_meter_snapshots_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_meter_snapshots(tenant_id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: billing_profile_blocks billing_profile_blocks_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY billing_profile_blocks
    ADD CONSTRAINT billing_profile_blocks_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: billing_profile_blocks billing_profile_blocks_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY billing_profile_blocks
    ADD CONSTRAINT billing_profile_blocks_request_id_fkey FOREIGN KEY (request_id) REFERENCES request_records(id);


--
-- Name: binding_cost_rate_limits binding_cost_rate_limits_binding_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY binding_cost_rate_limits
    ADD CONSTRAINT binding_cost_rate_limits_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES model_bindings(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: channel_batch_operations channel_batch_operations_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_batch_operations
    ADD CONSTRAINT channel_batch_operations_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: channel_credentials channel_credentials_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_credentials
    ADD CONSTRAINT channel_credentials_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: channel_model_discoveries channel_model_discoveries_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: channel_model_discoveries channel_model_discoveries_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: channel_model_discoveries channel_model_discoveries_channel_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_channel_version_id_fkey FOREIGN KEY (channel_version_id) REFERENCES channel_versions(id);


--
-- Name: channel_model_discoveries channel_model_discoveries_credential_id_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_credential_id_channel_id_fkey FOREIGN KEY (credential_id, channel_id) REFERENCES channel_credentials(id, channel_id);


--
-- Name: channel_model_discoveries channel_model_discoveries_credential_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES channel_credentials(id);


--
-- Name: channel_model_discoveries channel_model_discoveries_pool_identity; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discoveries
    ADD CONSTRAINT channel_model_discoveries_pool_identity FOREIGN KEY (channel_version_id, channel_id, credential_id) REFERENCES channel_version_credentials(channel_version_id, channel_id, credential_id);


--
-- Name: channel_model_discovery_results channel_model_discovery_results_discovery_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_model_discovery_results
    ADD CONSTRAINT channel_model_discovery_results_discovery_id_fkey FOREIGN KEY (discovery_id) REFERENCES channel_model_discoveries(id);


--
-- Name: channel_version_credentials channel_version_credentials_channel_version_id_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_version_credentials
    ADD CONSTRAINT channel_version_credentials_channel_version_id_channel_id_fkey FOREIGN KEY (channel_version_id, channel_id) REFERENCES channel_versions(id, channel_id);


--
-- Name: channel_version_credentials channel_version_credentials_credential_id_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_version_credentials
    ADD CONSTRAINT channel_version_credentials_credential_id_channel_id_fkey FOREIGN KEY (credential_id, channel_id) REFERENCES channel_credentials(id, channel_id);


--
-- Name: channel_versions channel_versions_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_versions
    ADD CONSTRAINT channel_versions_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: channel_versions channel_versions_credential_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_versions
    ADD CONSTRAINT channel_versions_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES channel_credentials(id);


--
-- Name: channel_versions channel_versions_primary_membership; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channel_versions
    ADD CONSTRAINT channel_versions_primary_membership FOREIGN KEY (id, channel_id, credential_id) REFERENCES channel_version_credentials(channel_version_id, channel_id, credential_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: channels channels_current_version; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY channels
    ADD CONSTRAINT channels_current_version FOREIGN KEY (current_version) REFERENCES channel_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: commerce_commands commerce_commands_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_commands
    ADD CONSTRAINT commerce_commands_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: commerce_operation_receipts commerce_operation_receipts_funding_receipt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_operation_receipts
    ADD CONSTRAINT commerce_operation_receipts_funding_receipt_id_fkey FOREIGN KEY (funding_receipt_id) REFERENCES funding_receipts(id);


--
-- Name: commerce_operation_receipts commerce_operation_receipts_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_operation_receipts
    ADD CONSTRAINT commerce_operation_receipts_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: commerce_operation_receipts commerce_operation_receipts_operation_id_kind_scope_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY commerce_operation_receipts
    ADD CONSTRAINT commerce_operation_receipts_operation_id_kind_scope_actor_fkey FOREIGN KEY (operation_id, kind, scope, actor) REFERENCES commerce_commands(operation_id, kind, scope, actor);


--
-- Name: cost_rate_lines cost_rate_lines_cost_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_rate_lines
    ADD CONSTRAINT cost_rate_lines_cost_version_id_fkey FOREIGN KEY (cost_version_id) REFERENCES cost_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: cost_versions cost_versions_actor_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_versions
    ADD CONSTRAINT cost_versions_actor_fk FOREIGN KEY (created_by) REFERENCES users(id);


--
-- Name: cost_versions cost_versions_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_versions
    ADD CONSTRAINT cost_versions_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: cost_versions cost_versions_operation_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_versions
    ADD CONSTRAINT cost_versions_operation_fk FOREIGN KEY (pricing_operation_id) REFERENCES pricing_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: cost_versions cost_versions_schema_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY cost_versions
    ADD CONSTRAINT cost_versions_schema_fk FOREIGN KEY (schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: credential_procurement_bindings credential_procurement_bindings_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindings_account_id_fkey FOREIGN KEY (account_id) REFERENCES procurement_accounts(id);


--
-- Name: credential_procurement_bindings credential_procurement_bindings_credential_id_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credential_procurement_bindings
    ADD CONSTRAINT credential_procurement_bindings_credential_id_channel_id_fkey FOREIGN KEY (credential_id, channel_id) REFERENCES channel_credentials(id, channel_id);


--
-- Name: credit_orders credit_orders_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: credit_orders credit_orders_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: credit_orders credit_orders_requester_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_requester_fkey FOREIGN KEY (requester) REFERENCES users(id);


--
-- Name: credit_orders credit_orders_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: credit_orders credit_orders_tenant_id_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY credit_orders
    ADD CONSTRAINT credit_orders_tenant_id_journal_id_fkey FOREIGN KEY (tenant_id, journal_id) REFERENCES journals(tenant_id, id);


--
-- Name: delivery_observations delivery_observations_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY delivery_observations
    ADD CONSTRAINT delivery_observations_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: finalization_records finalization_records_event_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_event_id_fkey FOREIGN KEY (event_id) REFERENCES metering_events(id);


--
-- Name: finalization_records finalization_records_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY finalization_records
    ADD CONSTRAINT finalization_records_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: financial_idempotency financial_idempotency_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY financial_idempotency
    ADD CONSTRAINT financial_idempotency_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: funding_consumption_refund_lots funding_consumption_refund_lo_tenant_id_settlement_id_allo_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_consumption_refund_lots
    ADD CONSTRAINT funding_consumption_refund_lo_tenant_id_settlement_id_allo_fkey FOREIGN KEY (tenant_id, settlement_id, allocation_index, lot_id) REFERENCES funding_settlement_lots(tenant_id, settlement_id, allocation_index, lot_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_consumption_refund_lots funding_consumption_refund_lots_tenant_id_refund_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_consumption_refund_lots
    ADD CONSTRAINT funding_consumption_refund_lots_tenant_id_refund_order_id_fkey FOREIGN KEY (tenant_id, refund_order_id) REFERENCES refund_orders(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_lot_balances funding_lot_balances_tenant_id_lot_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_balances
    ADD CONSTRAINT funding_lot_balances_tenant_id_lot_id_fkey FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_lot_events funding_lot_events_tenant_id_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_events
    ADD CONSTRAINT funding_lot_events_tenant_id_journal_id_fkey FOREIGN KEY (tenant_id, journal_id) REFERENCES journals(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_lot_events funding_lot_events_tenant_id_lot_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lot_events
    ADD CONSTRAINT funding_lot_events_tenant_id_lot_id_fkey FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_lots funding_lots_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lots
    ADD CONSTRAINT funding_lots_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: funding_lots funding_lots_tenant_id_funding_receipt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_lots
    ADD CONSTRAINT funding_lots_tenant_id_funding_receipt_id_fkey FOREIGN KEY (tenant_id, funding_receipt_id) REFERENCES funding_receipts(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_receipts funding_receipts_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_receipts
    ADD CONSTRAINT funding_receipts_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: funding_receipts funding_receipts_tenant_id_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_receipts
    ADD CONSTRAINT funding_receipts_tenant_id_journal_id_fkey FOREIGN KEY (tenant_id, journal_id) REFERENCES journals(tenant_id, id);


--
-- Name: funding_reservation_lots funding_reservation_lots_tenant_id_lot_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_reservation_lots
    ADD CONSTRAINT funding_reservation_lots_tenant_id_lot_id_fkey FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_reservation_lots funding_reservation_lots_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_reservation_lots
    ADD CONSTRAINT funding_reservation_lots_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_reservation_lots funding_reservation_lots_tenant_id_reservation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_reservation_lots
    ADD CONSTRAINT funding_reservation_lots_tenant_id_reservation_id_fkey FOREIGN KEY (tenant_id, reservation_id) REFERENCES reservations(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_settlement_lots funding_settlement_lots_tenant_id_reservation_id_allocatio_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_settlement_lots
    ADD CONSTRAINT funding_settlement_lots_tenant_id_reservation_id_allocatio_fkey FOREIGN KEY (tenant_id, reservation_id, allocation_index, lot_id) REFERENCES funding_reservation_lots(tenant_id, reservation_id, allocation_index, lot_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: funding_settlement_lots funding_settlement_lots_tenant_id_settlement_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY funding_settlement_lots
    ADD CONSTRAINT funding_settlement_lots_tenant_id_settlement_id_fkey FOREIGN KEY (tenant_id, settlement_id) REFERENCES settlements(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: identity_challenges identity_challenges_subject_id_purpose_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_challenges
    ADD CONSTRAINT identity_challenges_subject_id_purpose_fkey FOREIGN KEY (subject_id, purpose) REFERENCES identity_mail_subjects(id, purpose);


--
-- Name: identity_challenges identity_challenges_user_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_challenges
    ADD CONSTRAINT identity_challenges_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);


--
-- Name: identity_initialization identity_initialization_initialized_by_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_initialization
    ADD CONSTRAINT identity_initialization_initialized_by_fkey FOREIGN KEY (initialized_by) REFERENCES users(id);


--
-- Name: identity_mail_outbox identity_mail_outbox_challenge_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY identity_mail_outbox
    ADD CONSTRAINT identity_mail_outbox_challenge_id_fkey FOREIGN KEY (challenge_id) REFERENCES identity_challenges(id);


--
-- Name: journal_lines journal_lines_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journal_lines
    ADD CONSTRAINT journal_lines_account_id_fkey FOREIGN KEY (account_id) REFERENCES ledger_accounts(id);


--
-- Name: journal_lines journal_lines_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journal_lines
    ADD CONSTRAINT journal_lines_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: journals journals_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY journals
    ADD CONSTRAINT journals_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: ledger_accounts ledger_accounts_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY ledger_accounts
    ADD CONSTRAINT ledger_accounts_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: media_attempts media_attempts_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_attempts
    ADD CONSTRAINT media_attempts_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES media_requests(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_observations media_observations_tenant_id_attempt_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_observations
    ADD CONSTRAINT media_observations_tenant_id_attempt_id_request_id_fkey FOREIGN KEY (tenant_id, attempt_id, request_id) REFERENCES media_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_observations media_observations_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_observations
    ADD CONSTRAINT media_observations_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES media_requests(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_requests media_requests_model_key_operation_profile_version_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_requests
    ADD CONSTRAINT media_requests_model_key_operation_profile_version_fkey FOREIGN KEY (model_key, operation, profile_version) REFERENCES media_model_profiles(model_key, operation, profile_version) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_requests media_requests_owner_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_requests
    ADD CONSTRAINT media_requests_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users(id);


--
-- Name: media_requests media_requests_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_requests
    ADD CONSTRAINT media_requests_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: media_requests media_requests_tenant_id_key_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_requests
    ADD CONSTRAINT media_requests_tenant_id_key_id_fkey FOREIGN KEY (tenant_id, key_id) REFERENCES api_keys(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_reservation_lots media_reservation_lots_tenant_id_lot_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservation_lots
    ADD CONSTRAINT media_reservation_lots_tenant_id_lot_id_fkey FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_reservation_lots media_reservation_lots_tenant_id_reservation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservation_lots
    ADD CONSTRAINT media_reservation_lots_tenant_id_reservation_id_fkey FOREIGN KEY (tenant_id, reservation_id) REFERENCES media_reservations(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_reservations media_reservations_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservations
    ADD CONSTRAINT media_reservations_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: media_reservations media_reservations_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_reservations
    ADD CONSTRAINT media_reservations_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES media_requests(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_result_artifacts media_result_artifacts_tenant_id_attempt_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_result_artifacts
    ADD CONSTRAINT media_result_artifacts_tenant_id_attempt_id_request_id_fkey FOREIGN KEY (tenant_id, attempt_id, request_id) REFERENCES media_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_result_artifacts media_result_artifacts_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_result_artifacts
    ADD CONSTRAINT media_result_artifacts_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES media_requests(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_settlement_lots media_settlement_lots_tenant_id_reservation_id_allocation__fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlement_lots
    ADD CONSTRAINT media_settlement_lots_tenant_id_reservation_id_allocation__fkey FOREIGN KEY (tenant_id, reservation_id, allocation_index) REFERENCES media_reservation_lots(tenant_id, reservation_id, allocation_index) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_settlement_lots media_settlement_lots_tenant_id_settlement_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlement_lots
    ADD CONSTRAINT media_settlement_lots_tenant_id_settlement_id_fkey FOREIGN KEY (tenant_id, settlement_id) REFERENCES media_settlements(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_settlements media_settlements_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlements
    ADD CONSTRAINT media_settlements_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: media_settlements media_settlements_tenant_id_attempt_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlements
    ADD CONSTRAINT media_settlements_tenant_id_attempt_id_request_id_fkey FOREIGN KEY (tenant_id, attempt_id, request_id) REFERENCES media_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_settlements media_settlements_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlements
    ADD CONSTRAINT media_settlements_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES media_requests(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_settlements media_settlements_tenant_id_reservation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_settlements
    ADD CONSTRAINT media_settlements_tenant_id_reservation_id_fkey FOREIGN KEY (tenant_id, reservation_id) REFERENCES media_reservations(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_task_artifacts media_task_artifacts_tenant_id_task_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_task_artifacts
    ADD CONSTRAINT media_task_artifacts_tenant_id_task_id_fkey FOREIGN KEY (tenant_id, task_id) REFERENCES media_tasks(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_task_events media_task_events_tenant_id_task_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_task_events
    ADD CONSTRAINT media_task_events_tenant_id_task_id_fkey FOREIGN KEY (tenant_id, task_id) REFERENCES media_tasks(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_profile_version_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_profile_version_fkey FOREIGN KEY (profile_version) REFERENCES model_capability_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_schema_id_schema_digest_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_schema_id_schema_digest_fkey FOREIGN KEY (schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_tenant_id_attempt_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_attempt_id_request_id_fkey FOREIGN KEY (tenant_id, attempt_id, request_id) REFERENCES upstream_attempts(tenant_id, id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_tenant_id_owner_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_owner_id_fkey FOREIGN KEY (tenant_id, owner_id) REFERENCES memberships(tenant_id, user_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_tenant_id_reservation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_reservation_id_fkey FOREIGN KEY (tenant_id, reservation_id) REFERENCES reservations(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: media_tasks media_tasks_tenant_id_source_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY media_tasks
    ADD CONSTRAINT media_tasks_tenant_id_source_id_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES funding_receipts(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: member_model_grants member_model_grants_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY member_model_grants
    ADD CONSTRAINT member_model_grants_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: member_model_grants member_model_grants_tenant_id_user_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY member_model_grants
    ADD CONSTRAINT member_model_grants_tenant_id_user_id_fkey FOREIGN KEY (tenant_id, user_id) REFERENCES memberships(tenant_id, user_id);


--
-- Name: memberships memberships_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY memberships
    ADD CONSTRAINT memberships_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: memberships memberships_user_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY memberships
    ADD CONSTRAINT memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);


--
-- Name: meter_event_quantities meter_event_quantities_tenant_id_event_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_event_quantities
    ADD CONSTRAINT meter_event_quantities_tenant_id_event_id_fkey FOREIGN KEY (tenant_id, event_id) REFERENCES metering_events(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: meter_schema_dimensions meter_schema_dimensions_schema_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY meter_schema_dimensions
    ADD CONSTRAINT meter_schema_dimensions_schema_id_fkey FOREIGN KEY (schema_id) REFERENCES meter_schemas(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: metering_event_conflicts metering_event_conflicts_event_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_event_conflicts
    ADD CONSTRAINT metering_event_conflicts_event_id_fkey FOREIGN KEY (event_id) REFERENCES metering_events(id);


--
-- Name: metering_events metering_events_tenant_id_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_events
    ADD CONSTRAINT metering_events_tenant_id_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES upstream_attempts(tenant_id, id);


--
-- Name: metering_events metering_events_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY metering_events
    ADD CONSTRAINT metering_events_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: mfa_recovery_codes mfa_recovery_codes_user_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY mfa_recovery_codes
    ADD CONSTRAINT mfa_recovery_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);


--
-- Name: model_bindings model_bindings_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_bindings
    ADD CONSTRAINT model_bindings_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: model_bindings model_bindings_channel_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_bindings
    ADD CONSTRAINT model_bindings_channel_version_id_fkey FOREIGN KEY (channel_version_id) REFERENCES channel_versions(id);


--
-- Name: model_bindings model_bindings_route_policy_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_bindings
    ADD CONSTRAINT model_bindings_route_policy_version_id_fkey FOREIGN KEY (route_policy_version_id) REFERENCES route_policy_versions(id);


--
-- Name: model_capability_bound_lines model_capability_bound_lines_profile_version_variant_key_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_bound_lines
    ADD CONSTRAINT model_capability_bound_lines_profile_version_variant_key_fkey FOREIGN KEY (profile_version, variant_key) REFERENCES model_capability_variants(profile_version, variant_key) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: model_capability_versions model_capability_schema_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_versions
    ADD CONSTRAINT model_capability_schema_fk FOREIGN KEY (schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: model_capability_variants model_capability_variants_profile_version_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_variants
    ADD CONSTRAINT model_capability_variants_profile_version_fkey FOREIGN KEY (profile_version) REFERENCES model_capability_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: model_capability_versions model_capability_versions_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY model_capability_versions
    ADD CONSTRAINT model_capability_versions_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: models models_profile_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY models
    ADD CONSTRAINT models_profile_fk FOREIGN KEY (profile_version, id) REFERENCES model_capability_versions(id, model_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: payment_cancel_intents payment_cancel_intents_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_cancel_intents
    ADD CONSTRAINT payment_cancel_intents_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: payment_cancel_intents payment_cancel_intents_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_cancel_intents
    ADD CONSTRAINT payment_cancel_intents_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES commerce_commands(operation_id);


--
-- Name: payment_cancel_intents payment_cancel_intents_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_cancel_intents
    ADD CONSTRAINT payment_cancel_intents_order_id_fkey FOREIGN KEY (order_id) REFERENCES payment_orders(id);


--
-- Name: payment_captures payment_captures_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_order_id_fkey FOREIGN KEY (order_id) REFERENCES payment_order_capture_selections(order_id);


--
-- Name: payment_captures payment_captures_order_id_identity_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_order_id_identity_id_fkey FOREIGN KEY (order_id, identity_id) REFERENCES payment_transaction_identities(order_id, id);


--
-- Name: payment_captures payment_captures_tenant_id_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_tenant_id_order_id_fkey FOREIGN KEY (tenant_id, order_id) REFERENCES payment_orders(tenant_id, id);


--
-- Name: payment_captures payment_captures_verified_evidence_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_captures
    ADD CONSTRAINT payment_captures_verified_evidence_id_fkey FOREIGN KEY (verified_evidence_id) REFERENCES payment_verified_evidence(id);


--
-- Name: payment_command_observations payment_command_observations_outbox_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_observations
    ADD CONSTRAINT payment_command_observations_outbox_id_fkey FOREIGN KEY (outbox_id) REFERENCES payment_command_outbox(id);


--
-- Name: payment_command_outbox payment_command_outbox_id_current_query_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_outbox
    ADD CONSTRAINT payment_command_outbox_id_current_query_id_fkey FOREIGN KEY (id, current_query_id) REFERENCES payment_query_attempts(supervisor_id, query_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: payment_command_outbox payment_command_outbox_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_outbox
    ADD CONSTRAINT payment_command_outbox_order_id_fkey FOREIGN KEY (order_id) REFERENCES payment_orders(id);


--
-- Name: payment_command_outbox payment_command_outbox_original_outbox_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_command_outbox
    ADD CONSTRAINT payment_command_outbox_original_outbox_id_fkey FOREIGN KEY (original_outbox_id) REFERENCES payment_command_outbox(id);


--
-- Name: payment_effects payment_effects_capture_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_effects
    ADD CONSTRAINT payment_effects_capture_id_fkey FOREIGN KEY (capture_id) REFERENCES payment_captures(id);


--
-- Name: payment_effects payment_effects_identity_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_effects
    ADD CONSTRAINT payment_effects_identity_id_fkey FOREIGN KEY (identity_id) REFERENCES payment_transaction_identities(id);


--
-- Name: payment_event_conflicts payment_event_conflicts_inbox_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_event_conflicts
    ADD CONSTRAINT payment_event_conflicts_inbox_id_fkey FOREIGN KEY (inbox_id) REFERENCES payment_inbox(id);


--
-- Name: payment_evidence_processing payment_evidence_processing_evidence_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_evidence_processing
    ADD CONSTRAINT payment_evidence_processing_evidence_id_fkey FOREIGN KEY (evidence_id) REFERENCES payment_verified_evidence(id);


--
-- Name: payment_inbox payment_inbox_environment_id_merchant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_inbox
    ADD CONSTRAINT payment_inbox_environment_id_merchant_id_fkey FOREIGN KEY (environment_id, merchant_id) REFERENCES payment_sandbox_environment(id, merchant_id);


--
-- Name: payment_order_capture_selections payment_order_capture_selections_first_evidence_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_order_capture_selections
    ADD CONSTRAINT payment_order_capture_selections_first_evidence_id_fkey FOREIGN KEY (first_evidence_id) REFERENCES payment_verified_evidence(id);


--
-- Name: payment_order_capture_selections payment_order_capture_selections_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_order_capture_selections
    ADD CONSTRAINT payment_order_capture_selections_order_id_fkey FOREIGN KEY (order_id) REFERENCES payment_orders(id);


--
-- Name: payment_order_capture_selections payment_order_capture_selections_order_id_identity_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_order_capture_selections
    ADD CONSTRAINT payment_order_capture_selections_order_id_identity_id_fkey FOREIGN KEY (order_id, identity_id) REFERENCES payment_transaction_identities(order_id, id);


--
-- Name: payment_orders payment_orders_creator_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_creator_fkey FOREIGN KEY (creator) REFERENCES users(id);


--
-- Name: payment_orders payment_orders_environment_id_merchant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_environment_id_merchant_id_fkey FOREIGN KEY (environment_id, merchant_id) REFERENCES payment_sandbox_environment(id, merchant_id);


--
-- Name: payment_orders payment_orders_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_id_fkey FOREIGN KEY (id) REFERENCES commerce_commands(operation_id);


--
-- Name: payment_orders payment_orders_sku_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_sku_version_id_fkey FOREIGN KEY (sku_version_id) REFERENCES payment_sku_versions(id);


--
-- Name: payment_orders payment_orders_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_orders
    ADD CONSTRAINT payment_orders_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: payment_provider_revisions payment_provider_revisions_environment_id_merchant_id_orde_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_provider_revisions
    ADD CONSTRAINT payment_provider_revisions_environment_id_merchant_id_orde_fkey FOREIGN KEY (environment_id, merchant_id, order_id) REFERENCES payment_orders(environment_id, merchant_id, id);


--
-- Name: payment_provider_revisions payment_provider_revisions_evidence_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_provider_revisions
    ADD CONSTRAINT payment_provider_revisions_evidence_id_fkey FOREIGN KEY (evidence_id) REFERENCES payment_verified_evidence(id);


--
-- Name: payment_provider_revisions payment_provider_revisions_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_provider_revisions
    ADD CONSTRAINT payment_provider_revisions_order_id_fkey FOREIGN KEY (order_id) REFERENCES payment_orders(id);


--
-- Name: payment_query_attempts payment_query_attempts_supervisor_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_attempts
    ADD CONSTRAINT payment_query_attempts_supervisor_id_fkey FOREIGN KEY (supervisor_id) REFERENCES payment_command_outbox(id);


--
-- Name: payment_query_conflicts payment_query_conflicts_verified_query_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_query_conflicts
    ADD CONSTRAINT payment_query_conflicts_verified_query_id_fkey FOREIGN KEY (verified_query_id) REFERENCES payment_verified_queries(id);


--
-- Name: payment_review_facts payment_review_facts_evidence_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_review_facts
    ADD CONSTRAINT payment_review_facts_evidence_id_fkey FOREIGN KEY (evidence_id) REFERENCES payment_verified_evidence(id);


--
-- Name: payment_review_facts payment_review_facts_identity_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_review_facts
    ADD CONSTRAINT payment_review_facts_identity_id_fkey FOREIGN KEY (identity_id) REFERENCES payment_transaction_identities(id);


--
-- Name: payment_review_facts payment_review_facts_selection_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_review_facts
    ADD CONSTRAINT payment_review_facts_selection_order_id_fkey FOREIGN KEY (selection_order_id) REFERENCES payment_order_capture_selections(order_id);


--
-- Name: payment_runtime_attestations payment_runtime_attestations_environment_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_runtime_attestations
    ADD CONSTRAINT payment_runtime_attestations_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES payment_sandbox_environment(id);


--
-- Name: payment_runtime_attestations payment_runtime_attestations_singleton_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_runtime_attestations
    ADD CONSTRAINT payment_runtime_attestations_singleton_fkey FOREIGN KEY (singleton) REFERENCES payment_sandbox_environment(singleton);


--
-- Name: payment_sku_changes payment_sku_changes_before_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_before_operation_id_fkey FOREIGN KEY (before_operation_id) REFERENCES payment_sku_changes(operation_id);


--
-- Name: payment_sku_changes payment_sku_changes_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES commerce_commands(operation_id);


--
-- Name: payment_sku_changes payment_sku_changes_sku_id_after_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_sku_id_after_version_id_fkey FOREIGN KEY (sku_id, after_version_id) REFERENCES payment_sku_versions(sku_id, id);


--
-- Name: payment_sku_changes payment_sku_changes_sku_id_before_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_sku_id_before_version_id_fkey FOREIGN KEY (sku_id, before_version_id) REFERENCES payment_sku_versions(sku_id, id);


--
-- Name: payment_sku_changes payment_sku_changes_sku_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_sku_id_fkey FOREIGN KEY (sku_id) REFERENCES payment_skus(id);


--
-- Name: payment_sku_changes payment_sku_changes_sku_id_published_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_changes
    ADD CONSTRAINT payment_sku_changes_sku_id_published_version_id_fkey FOREIGN KEY (sku_id, published_version_id) REFERENCES payment_sku_versions(sku_id, id);


--
-- Name: payment_sku_versions payment_sku_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id);


--
-- Name: payment_sku_versions payment_sku_versions_environment_id_merchant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_environment_id_merchant_id_fkey FOREIGN KEY (environment_id, merchant_id) REFERENCES payment_sandbox_environment(id, merchant_id);


--
-- Name: payment_sku_versions payment_sku_versions_publish_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_publish_operation_id_fkey FOREIGN KEY (publish_operation_id) REFERENCES commerce_commands(operation_id);


--
-- Name: payment_sku_versions payment_sku_versions_sku_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_sku_versions
    ADD CONSTRAINT payment_sku_versions_sku_id_fkey FOREIGN KEY (sku_id) REFERENCES payment_skus(id);


--
-- Name: payment_skus payment_skus_created_by_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_skus
    ADD CONSTRAINT payment_skus_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id);


--
-- Name: payment_skus payment_skus_id_current_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_skus
    ADD CONSTRAINT payment_skus_id_current_version_id_fkey FOREIGN KEY (id, current_version_id) REFERENCES payment_sku_versions(sku_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: payment_skus payment_skus_last_change_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_skus
    ADD CONSTRAINT payment_skus_last_change_operation_id_fkey FOREIGN KEY (last_change_operation_id) REFERENCES payment_sku_changes(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: payment_transaction_identities payment_transaction_identitie_environment_id_merchant_id_o_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_transaction_identities
    ADD CONSTRAINT payment_transaction_identitie_environment_id_merchant_id_o_fkey FOREIGN KEY (environment_id, merchant_id, order_id) REFERENCES payment_orders(environment_id, merchant_id, id);


--
-- Name: payment_transaction_identities payment_transaction_identities_evidence_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_transaction_identities
    ADD CONSTRAINT payment_transaction_identities_evidence_id_fkey FOREIGN KEY (evidence_id) REFERENCES payment_verified_evidence(id);


--
-- Name: payment_transaction_identities payment_transaction_identities_tenant_id_order_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_transaction_identities
    ADD CONSTRAINT payment_transaction_identities_tenant_id_order_id_fkey FOREIGN KEY (tenant_id, order_id) REFERENCES payment_orders(tenant_id, id);


--
-- Name: payment_verified_evidence payment_verified_evidence_environment_id_merchant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_evidence
    ADD CONSTRAINT payment_verified_evidence_environment_id_merchant_id_fkey FOREIGN KEY (environment_id, merchant_id) REFERENCES payment_sandbox_environment(id, merchant_id);


--
-- Name: payment_verified_evidence payment_verified_evidence_inbox_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_evidence
    ADD CONSTRAINT payment_verified_evidence_inbox_id_fkey FOREIGN KEY (inbox_id) REFERENCES payment_inbox(id);


--
-- Name: payment_verified_evidence payment_verified_evidence_verified_query_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_evidence
    ADD CONSTRAINT payment_verified_evidence_verified_query_id_fkey FOREIGN KEY (verified_query_id) REFERENCES payment_verified_queries(id);


--
-- Name: payment_verified_queries payment_verified_queries_query_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY payment_verified_queries
    ADD CONSTRAINT payment_verified_queries_query_id_fkey FOREIGN KEY (query_id) REFERENCES payment_query_attempts(query_id);


--
-- Name: periodic_budget_allocations periodic_budget_allocations_control_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_allocations
    ADD CONSTRAINT periodic_budget_allocations_control_id_fkey FOREIGN KEY (control_id) REFERENCES periodic_budget_controls(id);


--
-- Name: periodic_budget_allocations periodic_budget_allocations_control_id_window_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_allocations
    ADD CONSTRAINT periodic_budget_allocations_control_id_window_id_fkey FOREIGN KEY (control_id, window_id) REFERENCES periodic_budget_windows(control_id, id);


--
-- Name: periodic_budget_configuration_versions periodic_budget_configuration_versions_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_configuration_versions
    ADD CONSTRAINT periodic_budget_configuration_versions_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: periodic_budget_configuration_versions periodic_budget_configuration_versions_control_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_configuration_versions
    ADD CONSTRAINT periodic_budget_configuration_versions_control_id_fkey FOREIGN KEY (control_id) REFERENCES periodic_budget_controls(id);


--
-- Name: periodic_budget_configuration_versions periodic_budget_configuration_versions_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_configuration_versions
    ADD CONSTRAINT periodic_budget_configuration_versions_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES periodic_budget_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: periodic_budget_controls periodic_budget_controls_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_controls
    ADD CONSTRAINT periodic_budget_controls_account_id_fkey FOREIGN KEY (account_id) REFERENCES procurement_accounts(id);


--
-- Name: periodic_budget_controls periodic_budget_controls_configuration_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_controls
    ADD CONSTRAINT periodic_budget_controls_configuration_operation_id_fkey FOREIGN KEY (configuration_operation_id) REFERENCES periodic_budget_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: periodic_budget_controls periodic_budget_controls_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_controls
    ADD CONSTRAINT periodic_budget_controls_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: periodic_budget_controls periodic_budget_controls_tenant_id_key_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_controls
    ADD CONSTRAINT periodic_budget_controls_tenant_id_key_id_fkey FOREIGN KEY (tenant_id, key_id) REFERENCES api_keys(tenant_id, id);


--
-- Name: periodic_budget_events periodic_budget_events_control_id_allocation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_events
    ADD CONSTRAINT periodic_budget_events_control_id_allocation_id_fkey FOREIGN KEY (control_id, allocation_id) REFERENCES periodic_budget_allocations(control_id, id);


--
-- Name: periodic_budget_operations periodic_budget_operations_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_operations
    ADD CONSTRAINT periodic_budget_operations_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: periodic_budget_windows periodic_budget_windows_control_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY periodic_budget_windows
    ADD CONSTRAINT periodic_budget_windows_control_id_fkey FOREIGN KEY (control_id) REFERENCES periodic_budget_controls(id);


--
-- Name: platform_probe_attempts platform_probe_attempts_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: platform_probe_attempts platform_probe_attempts_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: platform_probe_attempts platform_probe_attempts_channel_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_channel_version_id_fkey FOREIGN KEY (channel_version_id) REFERENCES channel_versions(id);


--
-- Name: platform_probe_attempts platform_probe_attempts_credential_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES channel_credentials(id);


--
-- Name: platform_probe_attempts platform_probe_attempts_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: platform_probe_attempts platform_probe_attempts_pool_identity; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY platform_probe_attempts
    ADD CONSTRAINT platform_probe_attempts_pool_identity FOREIGN KEY (channel_version_id, channel_id, credential_id) REFERENCES channel_version_credentials(channel_version_id, channel_id, credential_id);


--
-- Name: price_groups price_groups_last_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_groups
    ADD CONSTRAINT price_groups_last_operation_id_fkey FOREIGN KEY (last_operation_id) REFERENCES pricing_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: price_rate_lines price_rate_lines_price_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_rate_lines
    ADD CONSTRAINT price_rate_lines_price_version_id_fkey FOREIGN KEY (price_version_id) REFERENCES price_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: price_versions price_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id);


--
-- Name: price_versions price_versions_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: price_versions price_versions_price_group_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_price_group_id_fkey FOREIGN KEY (price_group_id) REFERENCES price_groups(id);


--
-- Name: price_versions price_versions_pricing_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_pricing_operation_id_fkey FOREIGN KEY (pricing_operation_id) REFERENCES pricing_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: price_versions price_versions_schema_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY price_versions
    ADD CONSTRAINT price_versions_schema_fk FOREIGN KEY (schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: pricing_operations pricing_operations_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY pricing_operations
    ADD CONSTRAINT pricing_operations_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: pricing_operations pricing_operations_scope_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY pricing_operations
    ADD CONSTRAINT pricing_operations_scope_tenant_id_fkey FOREIGN KEY (scope_tenant_id) REFERENCES tenants(id);


--
-- Name: procurement_balance_observations procurement_balance_observations_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_balance_observations
    ADD CONSTRAINT procurement_balance_observations_account_id_fkey FOREIGN KEY (account_id) REFERENCES procurement_accounts(id);


--
-- Name: procurement_balance_observations procurement_balance_observations_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_balance_observations
    ADD CONSTRAINT procurement_balance_observations_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: procurement_budget_events procurement_budget_events_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_budget_events
    ADD CONSTRAINT procurement_budget_events_account_id_fkey FOREIGN KEY (account_id) REFERENCES procurement_accounts(id);


--
-- Name: procurement_budget_events procurement_budget_events_attempt_id_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_budget_events
    ADD CONSTRAINT procurement_budget_events_attempt_id_account_id_fkey FOREIGN KEY (attempt_id, account_id) REFERENCES procurement_reservations(attempt_id, account_id);


--
-- Name: procurement_commands procurement_commands_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_commands
    ADD CONSTRAINT procurement_commands_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: procurement_cost_proposals procurement_cost_proposals_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_cost_proposals
    ADD CONSTRAINT procurement_cost_proposals_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: procurement_cost_proposals procurement_cost_proposals_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_cost_proposals
    ADD CONSTRAINT procurement_cost_proposals_attempt_id_fkey FOREIGN KEY (attempt_id) REFERENCES procurement_reservations(attempt_id);


--
-- Name: procurement_cost_proposals procurement_cost_proposals_requester_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_cost_proposals
    ADD CONSTRAINT procurement_cost_proposals_requester_fkey FOREIGN KEY (requester) REFERENCES users(id);


--
-- Name: procurement_cost_proposals procurement_cost_proposals_tenant_id_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_cost_proposals
    ADD CONSTRAINT procurement_cost_proposals_tenant_id_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES upstream_attempts(tenant_id, id);


--
-- Name: procurement_evidence_identities procurement_evidence_identities_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_evidence_identities
    ADD CONSTRAINT procurement_evidence_identities_attempt_id_fkey FOREIGN KEY (attempt_id) REFERENCES procurement_reservations(attempt_id);


--
-- Name: procurement_reservations procurement_reservations_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_reservations
    ADD CONSTRAINT procurement_reservations_account_id_fkey FOREIGN KEY (account_id) REFERENCES procurement_accounts(id);


--
-- Name: procurement_reservations procurement_reservations_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_reservations
    ADD CONSTRAINT procurement_reservations_attempt_id_fkey FOREIGN KEY (attempt_id) REFERENCES upstream_attempts(id);


--
-- Name: procurement_reservations procurement_reservations_binding_id_account_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY procurement_reservations
    ADD CONSTRAINT procurement_reservations_binding_id_account_id_fkey FOREIGN KEY (binding_id, account_id) REFERENCES credential_procurement_bindings(id, account_id);


--
-- Name: projection_outbox projection_outbox_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY projection_outbox
    ADD CONSTRAINT projection_outbox_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: projection_receipts projection_receipts_event_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY projection_receipts
    ADD CONSTRAINT projection_receipts_event_id_fkey FOREIGN KEY (event_id) REFERENCES projection_outbox(event_id);


--
-- Name: provider_cost_corrections provider_cost_corrections_tenant_id_attempt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY provider_cost_corrections
    ADD CONSTRAINT provider_cost_corrections_tenant_id_attempt_id_fkey FOREIGN KEY (tenant_id, attempt_id) REFERENCES upstream_attempts(tenant_id, id);


--
-- Name: realtime_events realtime_events_tenant_id_session_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_events
    ADD CONSTRAINT realtime_events_tenant_id_session_id_fkey FOREIGN KEY (tenant_id, session_id) REFERENCES realtime_sessions(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_events realtime_events_tenant_id_turn_id_session_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_events
    ADD CONSTRAINT realtime_events_tenant_id_turn_id_session_id_fkey FOREIGN KEY (tenant_id, turn_id, session_id) REFERENCES realtime_turns(tenant_id, id, session_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_sessions realtime_sessions_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_sessions realtime_sessions_profile_version_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_profile_version_fkey FOREIGN KEY (profile_version) REFERENCES model_capability_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_sessions realtime_sessions_route_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_route_id_fkey FOREIGN KEY (route_id) REFERENCES route_policy_versions(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_sessions realtime_sessions_tenant_id_actor_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_tenant_id_actor_id_fkey FOREIGN KEY (tenant_id, actor_id) REFERENCES memberships(tenant_id, user_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_sessions realtime_sessions_tenant_id_api_key_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_sessions
    ADD CONSTRAINT realtime_sessions_tenant_id_api_key_id_fkey FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: realtime_turns realtime_turn_session_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY realtime_turns
    ADD CONSTRAINT realtime_turn_session_fk FOREIGN KEY (tenant_id, session_id) REFERENCES realtime_sessions(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: reconciliation_cases reconciliation_cases_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reconciliation_cases
    ADD CONSTRAINT reconciliation_cases_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: reconciliation_cases reconciliation_cases_requester_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reconciliation_cases
    ADD CONSTRAINT reconciliation_cases_requester_fkey FOREIGN KEY (requester) REFERENCES users(id);


--
-- Name: reconciliation_cases reconciliation_cases_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reconciliation_cases
    ADD CONSTRAINT reconciliation_cases_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: recovery_gate recovery_gate_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY recovery_gate
    ADD CONSTRAINT recovery_gate_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: recovery_gate recovery_gate_requester_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY recovery_gate
    ADD CONSTRAINT recovery_gate_requester_fkey FOREIGN KEY (requester) REFERENCES users(id);


--
-- Name: recovery_jobs recovery_jobs_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY recovery_jobs
    ADD CONSTRAINT recovery_jobs_request_id_fkey FOREIGN KEY (request_id) REFERENCES request_records(id);


--
-- Name: recovery_jobs recovery_jobs_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY recovery_jobs
    ADD CONSTRAINT recovery_jobs_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: refund_orders refund_orders_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: refund_orders refund_orders_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: refund_orders refund_orders_requester_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_requester_fkey FOREIGN KEY (requester) REFERENCES users(id);


--
-- Name: refund_orders refund_orders_tenant_id_settlement_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY refund_orders
    ADD CONSTRAINT refund_orders_tenant_id_settlement_id_fkey FOREIGN KEY (tenant_id, settlement_id) REFERENCES settlements(tenant_id, id);


--
-- Name: report_totals report_totals_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY report_totals
    ADD CONSTRAINT report_totals_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: request_records request_finalization_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_finalization_fk FOREIGN KEY (latest_finalization_id) REFERENCES finalization_records(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: request_idempotency request_idempotency_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_idempotency
    ADD CONSTRAINT request_idempotency_request_id_fkey FOREIGN KEY (request_id) REFERENCES request_records(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: request_idempotency request_idempotency_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_idempotency
    ADD CONSTRAINT request_idempotency_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: request_meter_bound_lines request_meter_bound_lines_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_bound_lines
    ADD CONSTRAINT request_meter_bound_lines_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_meter_snapshots(tenant_id, request_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: request_meter_snapshots request_meter_snapshots_price_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_snapshots
    ADD CONSTRAINT request_meter_snapshots_price_version_id_fkey FOREIGN KEY (price_version_id) REFERENCES price_versions(id);


--
-- Name: request_meter_snapshots request_meter_snapshots_schema_id_schema_digest_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_snapshots
    ADD CONSTRAINT request_meter_snapshots_schema_id_schema_digest_fkey FOREIGN KEY (schema_id, schema_digest) REFERENCES meter_schemas(id, schema_digest) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: request_meter_snapshots request_meter_snapshots_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_meter_snapshots
    ADD CONSTRAINT request_meter_snapshots_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: request_records request_records_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_records_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: request_records request_records_owner_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_records_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users(id);


--
-- Name: request_records request_records_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_records_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: request_records request_records_tenant_id_key_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY request_records
    ADD CONSTRAINT request_records_tenant_id_key_id_fkey FOREIGN KEY (tenant_id, key_id) REFERENCES api_keys(tenant_id, id);


--
-- Name: reservations reservations_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY reservations
    ADD CONSTRAINT reservations_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: route_binding_isolations route_binding_isolations_channel_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY route_binding_isolations
    ADD CONSTRAINT route_binding_isolations_channel_version_id_fkey FOREIGN KEY (channel_version_id) REFERENCES channel_versions(id);


--
-- Name: route_policy_versions route_policy_versions_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY route_policy_versions
    ADD CONSTRAINT route_policy_versions_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: route_policy_versions route_profile_model_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY route_policy_versions
    ADD CONSTRAINT route_profile_model_fk FOREIGN KEY (profile_version, model_id) REFERENCES model_capability_versions(id, model_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: session_families session_families_user_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY session_families
    ADD CONSTRAINT session_families_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);


--
-- Name: sessions sessions_family_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_family_id_fkey FOREIGN KEY (family_id) REFERENCES session_families(id);


--
-- Name: sessions sessions_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: sessions sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);


--
-- Name: settlements settlements_finalization_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_finalization_id_fkey FOREIGN KEY (finalization_id) REFERENCES finalization_records(id);


--
-- Name: settlements settlements_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_journal_id_fkey FOREIGN KEY (journal_id) REFERENCES journals(id);


--
-- Name: settlements settlements_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY settlements
    ADD CONSTRAINT settlements_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: tenant_model_grants tenant_model_grants_model_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_model_grants
    ADD CONSTRAINT tenant_model_grants_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);


--
-- Name: tenant_model_grants tenant_model_grants_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_model_grants
    ADD CONSTRAINT tenant_model_grants_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_after_group_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_after_group_id_fkey FOREIGN KEY (after_group_id) REFERENCES price_groups(id);


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_before_group_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_before_group_id_fkey FOREIGN KEY (before_group_id) REFERENCES price_groups(id);


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES pricing_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: tenant_pricing_assignments tenant_pricing_assignments_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenant_pricing_assignments
    ADD CONSTRAINT tenant_pricing_assignments_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: tenants tenants_price_group_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenants
    ADD CONSTRAINT tenants_price_group_id_fkey FOREIGN KEY (price_group_id) REFERENCES price_groups(id);


--
-- Name: tenants tenants_pricing_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY tenants
    ADD CONSTRAINT tenants_pricing_operation_id_fkey FOREIGN KEY (pricing_operation_id) REFERENCES pricing_operations(operation_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: upstream_attempts upstream_attempts_channel_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES channels(id);


--
-- Name: upstream_attempts upstream_attempts_channel_version_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_channel_version_id_fkey FOREIGN KEY (channel_version_id) REFERENCES channel_versions(id);


--
-- Name: upstream_attempts upstream_attempts_cost_correction_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_cost_correction_fk FOREIGN KEY (tenant_id, latest_cost_correction_id, id) REFERENCES provider_cost_corrections(tenant_id, id, attempt_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: upstream_attempts upstream_attempts_cost_finalization_fk; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_cost_finalization_fk FOREIGN KEY (tenant_id, latest_cost_finalization_id, id) REFERENCES finalization_records(tenant_id, id, attempt_id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: upstream_attempts upstream_attempts_credential_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES channel_credentials(id);


--
-- Name: upstream_attempts upstream_attempts_pool_identity; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_pool_identity FOREIGN KEY (channel_version_id, channel_id, credential_id) REFERENCES channel_version_credentials(channel_version_id, channel_id, credential_id);


--
-- Name: upstream_attempts upstream_attempts_tenant_id_request_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY upstream_attempts
    ADD CONSTRAINT upstream_attempts_tenant_id_request_id_fkey FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id);


--
-- Name: voucher_batches voucher_batches_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_batches
    ADD CONSTRAINT voucher_batches_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: voucher_batches voucher_batches_maker_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_batches
    ADD CONSTRAINT voucher_batches_maker_fkey FOREIGN KEY (maker) REFERENCES users(id);


--
-- Name: voucher_batches voucher_batches_terminal_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_batches
    ADD CONSTRAINT voucher_batches_terminal_actor_fkey FOREIGN KEY (terminal_actor) REFERENCES users(id);


--
-- Name: voucher_redemptions voucher_redemptions_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: voucher_redemptions voucher_redemptions_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES commerce_commands(operation_id);


--
-- Name: voucher_redemptions voucher_redemptions_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- Name: voucher_redemptions voucher_redemptions_tenant_id_funding_receipt_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_tenant_id_funding_receipt_id_fkey FOREIGN KEY (tenant_id, funding_receipt_id) REFERENCES funding_receipts(tenant_id, id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: voucher_redemptions voucher_redemptions_tenant_id_journal_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_tenant_id_journal_id_fkey FOREIGN KEY (tenant_id, journal_id) REFERENCES journals(tenant_id, id);


--
-- Name: voucher_redemptions voucher_redemptions_voucher_id_batch_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_redemptions
    ADD CONSTRAINT voucher_redemptions_voucher_id_batch_id_fkey FOREIGN KEY (voucher_id, batch_id) REFERENCES vouchers(id, batch_id);


--
-- Name: voucher_revocation_decisions voucher_revocation_decisions_approver_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocation_decisions
    ADD CONSTRAINT voucher_revocation_decisions_approver_fkey FOREIGN KEY (approver) REFERENCES users(id);


--
-- Name: voucher_revocation_decisions voucher_revocation_decisions_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocation_decisions
    ADD CONSTRAINT voucher_revocation_decisions_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES commerce_commands(operation_id);


--
-- Name: voucher_revocation_decisions voucher_revocation_decisions_proposal_id_batch_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocation_decisions
    ADD CONSTRAINT voucher_revocation_decisions_proposal_id_batch_id_fkey FOREIGN KEY (proposal_id, batch_id) REFERENCES voucher_revocations(id, batch_id);


--
-- Name: voucher_revocations voucher_revocations_batch_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocations
    ADD CONSTRAINT voucher_revocations_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES voucher_batches(id);


--
-- Name: voucher_revocations voucher_revocations_maker_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocations
    ADD CONSTRAINT voucher_revocations_maker_fkey FOREIGN KEY (maker) REFERENCES users(id);


--
-- Name: voucher_revocations voucher_revocations_operation_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY voucher_revocations
    ADD CONSTRAINT voucher_revocations_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES commerce_commands(operation_id);


--
-- Name: vouchers vouchers_batch_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY vouchers
    ADD CONSTRAINT vouchers_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES voucher_batches(id);


--
-- Name: wallet_openings wallet_openings_actor_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY wallet_openings
    ADD CONSTRAINT wallet_openings_actor_fkey FOREIGN KEY (actor) REFERENCES users(id);


--
-- Name: wallet_openings wallet_openings_tenant_id_fkey; Type: FK CONSTRAINT; Schema: dump_current_3b04ba63ced5; Owner: -
--

ALTER TABLE ONLY wallet_openings
    ADD CONSTRAINT wallet_openings_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);


--
-- PostgreSQL database dump complete
--

-- pg_dump unrestrict removed

-- Restore the caller-bound invoker policy after the current-only dump.
DO $$ DECLARE f record;
BEGIN
 FOR f IN SELECT p.proname,pg_get_function_identity_arguments(p.oid) args
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=current_schema() LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SECURITY INVOKER',current_schema(),f.proname,f.args);
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
 END LOOP;
END $$;

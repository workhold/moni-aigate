-- 023-B0 single authoritative database clock; no GUC/client input.
CREATE FUNCTION pb_now() RETURNS timestamptz LANGUAGE sql VOLATILE AS $$ SELECT pg_catalog.clock_timestamp() $$;
-- 023-B0 isolated prototype. NOT a production migration, NOT frozen.
CREATE FUNCTION pb_uuid(v text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v<>'00000000-0000-0000-0000-000000000000',false)
$$;
CREATE FUNCTION pb_start(kind text, at_time timestamptz) RETURNS timestamptz LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE kind WHEN 'utc_day' THEN date_trunc('day',at_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN date_trunc('month',at_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' END
$$;
CREATE FUNCTION pb_end(kind text, start_time timestamptz) RETURNS timestamptz LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE kind WHEN 'utc_day' THEN (start_time AT TIME ZONE 'UTC'+interval '1 day') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN (start_time AT TIME ZONE 'UTC'+interval '1 month') AT TIME ZONE 'UTC' END
$$;
CREATE FUNCTION pb_current_tuple(stamp xid8, tuple_xid xid) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
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

-- First-configuration synchronization is a separate two-int advisory domain.
-- A collision only serializes unrelated scopes; no business root is acquired
-- here. READ COMMITTED is essential: a waiter must see the committed control
-- in subsequent VOLATILE/deferred checks instead of reusing an old RR snapshot.
CREATE FUNCTION pb_require_read_committed() RETURNS void LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'periodic source requires read committed' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION pb_scope_sync(scope_name text,target_id text,exclusive_lock boolean) RETURNS void LANGUAGE plpgsql VOLATILE AS $$
DECLARE lock_key integer;
BEGIN
 PERFORM pb_require_read_committed();
 IF scope_name NOT IN ('tenant','key','procurement') OR NOT pb_uuid(target_id) OR exclusive_lock IS NULL THEN
  RAISE EXCEPTION 'periodic invalid synchronization scope' USING ERRCODE='23514'; END IF;
 lock_key:=hashtext(current_schema()||':'||scope_name||':'||target_id);
 IF exclusive_lock THEN PERFORM pg_advisory_xact_lock(23023023,lock_key);
 ELSE PERFORM pg_advisory_xact_lock_shared(23023023,lock_key); END IF;
END $$;

CREATE TABLE periodic_budget_controls (
 id text PRIMARY KEY CHECK(pb_uuid(id)),
 scope text NOT NULL CHECK(scope IN ('tenant','key','procurement')),
 tenant_id text REFERENCES tenants(id), key_id text, account_id text REFERENCES procurement_accounts(id),
 period_kind text NOT NULL CHECK(period_kind IN ('utc_day','utc_month')),
 enabled boolean NOT NULL, limit_amount bigint NOT NULL CHECK(limit_amount>=0),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 tracking_started_at timestamptz NOT NULL DEFAULT pb_now(), effective_from timestamptz NOT NULL,
 max_observed_window_start timestamptz NOT NULL,
 held_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 event_seq bigint NOT NULL DEFAULT 0 CHECK(event_seq>=0), last_event_id text,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 FOREIGN KEY(tenant_id,key_id) REFERENCES api_keys(tenant_id,id),
 CHECK((scope='tenant' AND tenant_id IS NOT NULL AND key_id IS NULL AND account_id IS NULL)
 OR (scope='key' AND tenant_id IS NOT NULL AND key_id IS NOT NULL AND account_id IS NULL)
 OR (scope='procurement' AND tenant_id IS NULL AND key_id IS NULL AND account_id IS NOT NULL)),
 CHECK(isfinite(tracking_started_at) AND isfinite(effective_from) AND isfinite(max_observed_window_start)),
 CHECK(effective_from=pb_end(period_kind,pb_start(period_kind,tracking_started_at)))
);
CREATE UNIQUE INDEX pb_tenant_scope ON periodic_budget_controls(tenant_id) WHERE scope='tenant';
CREATE UNIQUE INDEX pb_key_scope ON periodic_budget_controls(key_id) WHERE scope='key';
CREATE UNIQUE INDEX pb_account_scope ON periodic_budget_controls(account_id) WHERE scope='procurement';
CREATE FUNCTION pb_sync_unconfigured(scope_name text,target_id text) RETURNS void LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 PERFORM pb_require_read_committed();
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_controls WHERE scope=scope_name AND
   CASE scope_name WHEN 'tenant' THEN tenant_id WHEN 'key' THEN key_id WHEN 'procurement' THEN account_id END=target_id) THEN
  PERFORM pb_scope_sync(scope_name,target_id,false);
 END IF;
END $$;
CREATE TABLE periodic_budget_windows (
 id text PRIMARY KEY CHECK(pb_uuid(id)), control_id text NOT NULL REFERENCES periodic_budget_controls(id),
 start_at timestamptz NOT NULL, end_at timestamptz NOT NULL,
 held_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0), last_event_id text,
 UNIQUE(control_id,start_at), UNIQUE(control_id,id)
);
CREATE TABLE periodic_budget_allocations (
 id text PRIMARY KEY CHECK(pb_uuid(id)), control_id text NOT NULL REFERENCES periodic_budget_controls(id),
 subject_type text NOT NULL CHECK(subject_type IN ('customer_request','supplier_attempt')),
 subject_id text NOT NULL CHECK(pb_uuid(subject_id)),
 origin text NOT NULL CHECK(origin IN ('new','legacy_import','legacy_catchup')),
 origin_source_id text NOT NULL CHECK(pb_uuid(origin_source_id)),
 window_id text, admitted_at timestamptz,
 upper_amount bigint NOT NULL CHECK(upper_amount>=0),
 baseline numeric(100,0) NOT NULL CHECK(baseline>=0),
 source_held numeric(100,0) NOT NULL DEFAULT 0 CHECK(source_held>=0),
 source_spent numeric(100,0) NOT NULL CHECK(source_spent>=0),
 held_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(held_amount>=0),
 spent_amount numeric(100,0) NOT NULL DEFAULT 0 CHECK(spent_amount>=0),
 state text NOT NULL DEFAULT 'held' CHECK(state IN ('held','disputed','settled','released')),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0), last_event_id text,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), created_at timestamptz NOT NULL DEFAULT pb_now(),
 UNIQUE(control_id,subject_type,subject_id), UNIQUE(control_id,id),
 FOREIGN KEY(control_id,window_id) REFERENCES periodic_budget_windows(control_id,id),
 CHECK((origin='new' AND window_id IS NOT NULL AND admitted_at IS NOT NULL AND isfinite(admitted_at))
 OR (origin<>'new' AND window_id IS NULL AND admitted_at IS NULL)),
 CHECK(subject_type<>'customer_request' OR (baseline=0 AND state<>'disputed')),
 CHECK(origin<>'legacy_catchup' OR subject_type='supplier_attempt')
);
CREATE INDEX pb_allocation_subject ON periodic_budget_allocations(subject_type,subject_id,control_id);
CREATE INDEX pb_allocation_page ON periodic_budget_allocations(control_id,created_at DESC,id DESC);
CREATE TABLE periodic_budget_events (
 id text PRIMARY KEY CHECK(pb_uuid(id)), control_id text NOT NULL,
 allocation_id text NOT NULL, source_kind text NOT NULL CHECK(source_kind IN ('reservation','settlement','procurement_event','legacy_import')),
 source_id text NOT NULL CHECK(pb_uuid(source_id)),
 seq bigint NOT NULL, allocation_before_revision bigint NOT NULL, window_before_revision bigint,
 before_held numeric(100,0) NOT NULL, before_spent numeric(100,0) NOT NULL,
 after_held numeric(100,0) NOT NULL, after_spent numeric(100,0) NOT NULL,
 after_source_spent numeric(100,0) NOT NULL,
 after_state text NOT NULL CHECK(after_state IN ('held','disputed','settled','released')),
 held_delta numeric(100,0) NOT NULL, spent_delta numeric(100,0) NOT NULL,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), created_at timestamptz NOT NULL DEFAULT pb_now(),
 UNIQUE(control_id,seq), UNIQUE(control_id,source_kind,source_id),
 FOREIGN KEY(control_id,allocation_id) REFERENCES periodic_budget_allocations(control_id,id)
);
CREATE INDEX pb_procurement_source_events ON procurement_budget_events(attempt_id,id);

ALTER TABLE request_records ADD COLUMN pb_created_xid xid8;
ALTER TABLE request_records ADD COLUMN pb_tenant_control text;
ALTER TABLE request_records ADD COLUMN pb_key_control text;
ALTER TABLE request_records ADD COLUMN pb_admitted_at timestamptz;
ALTER TABLE upstream_attempts ADD COLUMN pb_created_xid xid8;
ALTER TABLE upstream_attempts ADD COLUMN pb_dispatch_checked_at timestamptz(6)
 CHECK(pb_dispatch_checked_at IS NULL OR isfinite(pb_dispatch_checked_at));
ALTER TABLE procurement_reservations ADD COLUMN pb_created_xid xid8;
ALTER TABLE procurement_reservations ADD COLUMN pb_control text;
ALTER TABLE procurement_reservations ADD COLUMN pb_admitted_at timestamptz;
ALTER TABLE procurement_budget_events ADD COLUMN pb_created_xid xid8;

CREATE FUNCTION pb_stamp_request() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_request_stamp BEFORE INSERT OR UPDATE OR DELETE ON request_records FOR EACH ROW EXECUTE FUNCTION pb_stamp_request();
CREATE FUNCTION pb_stamp_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_attempt_stamp BEFORE INSERT OR UPDATE OR DELETE ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION pb_stamp_attempt();
CREATE FUNCTION pb_stamp_procurement() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_procurement_stamp BEFORE INSERT OR UPDATE ON procurement_reservations FOR EACH ROW EXECUTE FUNCTION pb_stamp_procurement();
CREATE FUNCTION pb_stamp_procurement_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.pb_created_xid:=pg_current_xact_id(); RETURN NEW; END $$;
CREATE TRIGGER pb_procurement_event_stamp BEFORE INSERT ON procurement_budget_events FOR EACH ROW EXECUTE FUNCTION pb_stamp_procurement_event();

CREATE FUNCTION pb_guard_control() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_control_guard BEFORE INSERT OR UPDATE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION pb_guard_control();
CREATE TRIGGER pb_control_no_delete BEFORE DELETE ON periodic_budget_controls FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION pb_guard_window() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_window_guard BEFORE INSERT OR UPDATE ON periodic_budget_windows FOR EACH ROW EXECUTE FUNCTION pb_guard_window();
CREATE TRIGGER pb_window_no_delete BEFORE DELETE ON periodic_budget_windows FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION pb_guard_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_allocation_guard BEFORE INSERT OR UPDATE ON periodic_budget_allocations FOR EACH ROW EXECUTE FUNCTION pb_guard_allocation();
CREATE TRIGGER pb_allocation_no_delete BEFORE DELETE ON periodic_budget_allocations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION pb_guard_event() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_event_guard BEFORE INSERT ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION pb_guard_event();
CREATE TRIGGER pb_event_immutable BEFORE UPDATE OR DELETE ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE FUNCTION pb_apply_event() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_event_apply AFTER INSERT ON periodic_budget_events FOR EACH ROW EXECUTE FUNCTION pb_apply_event();

CREATE FUNCTION pb_check_customer(subject text) RETURNS void LANGUAGE plpgsql AS $$
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
CREATE FUNCTION pb_check_supplier(subject text) RETURNS void LANGUAGE plpgsql AS $$
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
CREATE FUNCTION pb_check_source_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='request_records' THEN PERFORM pb_check_customer(NEW.id);
 ELSIF TG_TABLE_NAME='reservations' OR TG_TABLE_NAME='settlements' THEN PERFORM pb_check_customer(NEW.request_id);
 ELSIF TG_TABLE_NAME IN ('procurement_reservations','procurement_budget_events') THEN PERFORM pb_check_supplier(NEW.attempt_id);
 ELSIF NEW.subject_type='customer_request' THEN PERFORM pb_check_customer(NEW.subject_id);
 ELSE PERFORM pb_check_supplier(NEW.subject_id);END IF;RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER pb_request_source_check AFTER INSERT OR UPDATE ON request_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_reservation_source_check AFTER INSERT OR UPDATE ON reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_settlement_source_check AFTER INSERT OR UPDATE ON settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_procurement_source_check AFTER INSERT OR UPDATE ON procurement_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_procurement_event_check AFTER INSERT ON procurement_budget_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE CONSTRAINT TRIGGER pb_allocation_source_check AFTER INSERT OR UPDATE ON periodic_budget_allocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_source_trigger();
CREATE FUNCTION pb_guard_customer_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_customer_reservation_identity BEFORE INSERT OR UPDATE OR DELETE ON reservations FOR EACH ROW EXECUTE FUNCTION pb_guard_customer_reservation();
CREATE FUNCTION pb_check_initial_import() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE CONSTRAINT TRIGGER pb_initial_import_check AFTER INSERT ON periodic_budget_controls DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_check_initial_import();

-- Components for an authoritative source admission or the dispatch trigger.
-- Calling these with an arbitrary old at_time is NOT a dispatch permission.
CREATE FUNCTION pb_observe(control text, at_time timestamptz) RETURNS text LANGUAGE plpgsql AS $$
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
CREATE FUNCTION pb_gate(control text,at_time timestamptz,extra bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE c periodic_budget_controls%ROWTYPE;w periodic_budget_windows%ROWTYPE;observed_window text;
BEGIN
 IF extra IS NULL OR extra<0 THEN RAISE EXCEPTION 'periodic invalid extra' USING ERRCODE='23514';END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control FOR NO KEY UPDATE;
 observed_window:=pb_observe(control,at_time);
 SELECT * INTO STRICT w FROM periodic_budget_windows WHERE id=observed_window AND control_id=c.id;
 IF c.enabled AND at_time>=c.effective_from AND w.spent_amount+c.held_amount+extra>c.limit_amount THEN
  RAISE EXCEPTION 'periodic budget exceeded' USING ERRCODE='23514',CONSTRAINT='pb_budget_'||c.scope;END IF;
END $$;

-- Only the actual prepared->sending transition creates a new dispatch fact.
-- Supported callers already own all front-layer locks; this guard reuses the
-- control layer and never locks tenant/key/model/account/business source rows.
-- Missing scopes first join the separate first-configuration shared barrier.
CREATE FUNCTION pb_guard_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER pb_dispatch_guard BEFORE INSERT OR UPDATE ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION pb_guard_dispatch();

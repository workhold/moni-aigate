-- B-P1 SQL CANDIDATE, not a migration or an automatic startup installer.
-- Install only after the exact original 001--022 in an isolated schema.
-- All functions are invoker functions. The installer must own the schema.
-- The separate permissions file grants only the additional APP surface.

ALTER TABLE periodic_budget_operations ADD COLUMN observation_id text;
ALTER TABLE periodic_budget_windows ADD COLUMN observation_id text;
ALTER TABLE periodic_budget_windows ADD COLUMN observation_created_xid xid8;
ALTER TABLE periodic_budget_operations ADD CONSTRAINT pb_operation_observation_uuid
 CHECK (observation_id IS NULL OR pb_uuid(observation_id));
ALTER TABLE periodic_budget_windows ADD CONSTRAINT pb_window_observation_shape
 CHECK ((observation_id IS NULL AND observation_created_xid IS NULL)
 OR (pb_uuid(observation_id) AND observation_created_xid IS NOT NULL));

CREATE TABLE periodic_budget_observation_origins (
 control_id text PRIMARY KEY CHECK (pb_uuid(control_id)),
 version integer NOT NULL DEFAULT 1 CHECK (version=1),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 tracking_started_at timestamptz NOT NULL CHECK (isfinite(tracking_started_at)),
 CONSTRAINT pb_observation_origin_control FOREIGN KEY(control_id)
  REFERENCES periodic_budget_controls(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE periodic_budget_observation_heads (
 control_id text PRIMARY KEY CHECK (pb_uuid(control_id)),
 ordinal bigint NOT NULL DEFAULT 0 CHECK (ordinal>=0),
 last_observation_id text,
 updated_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 CONSTRAINT pb_observation_head_origin FOREIGN KEY(control_id)
  REFERENCES periodic_budget_observation_origins(control_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK ((ordinal=0 AND last_observation_id IS NULL)
 OR (ordinal>0 AND pb_uuid(last_observation_id)))
);
ALTER TABLE periodic_budget_observation_origins
 ADD CONSTRAINT pb_observation_origin_head FOREIGN KEY(control_id)
 REFERENCES periodic_budget_observation_heads(control_id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE periodic_budget_observation_journal (
 id text PRIMARY KEY CHECK (pb_uuid(id)),
 control_id text NOT NULL CHECK (pb_uuid(control_id)),
 ordinal bigint NOT NULL CHECK (ordinal>0),
 previous_id text CHECK (previous_id IS NULL OR pb_uuid(previous_id)),
 kind text NOT NULL CHECK (kind IN ('window_birth','command')),
 source_id text NOT NULL CHECK (pb_uuid(source_id)),
 event_seq bigint NOT NULL CHECK (event_seq>=0),
 last_event_id text CHECK (last_event_id IS NULL OR pb_uuid(last_event_id)),
 configuration_revision bigint NOT NULL CHECK (configuration_revision>0),
 configuration_operation_id text NOT NULL CHECK (pb_uuid(configuration_operation_id)),
 window_id text CHECK (window_id IS NULL OR pb_uuid(window_id)),
 window_revision bigint CHECK (window_revision>=0),
 window_last_event_id text CHECK (window_last_event_id IS NULL OR pb_uuid(window_last_event_id)),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 observed_at timestamptz,
 -- These are derived FK discriminators, never an independent source claim.
 command_source_id text GENERATED ALWAYS AS
  (CASE WHEN kind='command' THEN source_id ELSE NULL END) STORED,
 window_source_id text GENERATED ALWAYS AS
  (CASE WHEN kind='window_birth' THEN source_id ELSE NULL END) STORED,
 UNIQUE(control_id,ordinal),
 UNIQUE(kind,source_id),
 UNIQUE(id,control_id,ordinal,created_xid),
 UNIQUE(id,control_id,command_source_id,created_xid,observed_at),
 UNIQUE(id,control_id,window_source_id,created_xid),
 CONSTRAINT pb_observation_journal_origin FOREIGN KEY(control_id)
  REFERENCES periodic_budget_observation_origins(control_id) DEFERRABLE INITIALLY DEFERRED,
 CONSTRAINT pb_observation_previous FOREIGN KEY(previous_id)
  REFERENCES periodic_budget_observation_journal(id) DEFERRABLE INITIALLY DEFERRED,
 CONSTRAINT pb_observation_window FOREIGN KEY(control_id,window_id)
  REFERENCES periodic_budget_windows(control_id,id) DEFERRABLE INITIALLY DEFERRED,
 CHECK ((event_seq=0 AND last_event_id IS NULL)
 OR (event_seq>0 AND last_event_id IS NOT NULL)),
 CHECK ((window_id IS NULL AND window_revision IS NULL AND window_last_event_id IS NULL)
 OR (window_id IS NOT NULL AND window_revision IS NOT NULL
  AND ((window_revision=0 AND window_last_event_id IS NULL)
    OR (window_revision>0 AND window_last_event_id IS NOT NULL)))),
 CHECK (((kind='window_birth' AND window_id=source_id AND window_revision=0
   AND window_last_event_id IS NULL AND observed_at IS NULL)
 OR (kind='command' AND observed_at IS NOT NULL AND isfinite(observed_at))) IS TRUE)
);

-- Exact kind/source/control/id/top-xid relationships work in both directions.
-- All three source-before INSERTs use deferred FKs because the source tuple
-- does not yet exist. A caller cannot pre-create a journal and have it adopted.
ALTER TABLE periodic_budget_operations ADD CONSTRAINT pb_operation_observation_identity
 UNIQUE(operation_id,resource_id,observation_id,created_xid,created_at);
ALTER TABLE periodic_budget_windows ADD CONSTRAINT pb_window_observation_identity
 UNIQUE(id,control_id,observation_id,observation_created_xid);
ALTER TABLE periodic_budget_configuration_versions ADD CONSTRAINT pb_version_observation_identity
 UNIQUE(control_id,revision,operation_id);
ALTER TABLE periodic_budget_events ADD CONSTRAINT pb_event_observation_identity
 UNIQUE(control_id,seq,id);
ALTER TABLE periodic_budget_observation_journal
 ADD CONSTRAINT pb_observation_command_source
 FOREIGN KEY(command_source_id,control_id,id,created_xid,observed_at)
 REFERENCES periodic_budget_operations(operation_id,resource_id,observation_id,created_xid,created_at)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE periodic_budget_observation_journal
 ADD CONSTRAINT pb_observation_birth_source
 FOREIGN KEY(window_source_id,control_id,id,created_xid)
 REFERENCES periodic_budget_windows(id,control_id,observation_id,observation_created_xid)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE periodic_budget_operations
 ADD CONSTRAINT pb_operation_observation_source
 FOREIGN KEY(observation_id,resource_id,operation_id,created_xid,created_at)
 REFERENCES periodic_budget_observation_journal(id,control_id,command_source_id,created_xid,observed_at)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE periodic_budget_windows
 ADD CONSTRAINT pb_window_observation_source
 FOREIGN KEY(observation_id,control_id,id,observation_created_xid)
 REFERENCES periodic_budget_observation_journal(id,control_id,window_source_id,created_xid)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE periodic_budget_observation_journal
 ADD CONSTRAINT pb_observation_configuration
 FOREIGN KEY(control_id,configuration_revision,configuration_operation_id)
 REFERENCES periodic_budget_configuration_versions(control_id,revision,operation_id)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE periodic_budget_observation_journal
 ADD CONSTRAINT pb_observation_event_cut
 FOREIGN KEY(control_id,event_seq,last_event_id)
 REFERENCES periodic_budget_events(control_id,seq,id)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE periodic_budget_observation_heads
 ADD CONSTRAINT pb_observation_head_tail
 FOREIGN KEY(last_observation_id,control_id,ordinal,updated_xid)
 REFERENCES periodic_budget_observation_journal(id,control_id,ordinal,created_xid)
 DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION pb_prefix_immutable() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
BEGIN
 RAISE EXCEPTION 'periodic observation history immutable' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_prefix_origin_immutable
 BEFORE UPDATE OR DELETE OR TRUNCATE ON periodic_budget_observation_origins
 FOR EACH STATEMENT EXECUTE FUNCTION pb_prefix_immutable();
CREATE TRIGGER pb_prefix_journal_immutable
 BEFORE UPDATE OR DELETE OR TRUNCATE ON periodic_budget_observation_journal
 FOR EACH STATEMENT EXECUTE FUNCTION pb_prefix_immutable();
CREATE TRIGGER pb_prefix_head_no_delete
 BEFORE DELETE OR TRUNCATE ON periodic_budget_observation_heads
 FOR EACH STATEMENT EXECUTE FUNCTION pb_prefix_immutable();
CREATE TRIGGER pb_prefix_window_no_truncate
 BEFORE TRUNCATE ON periodic_budget_windows
 FOR EACH STATEMENT EXECUTE FUNCTION pb_prefix_immutable();
CREATE TRIGGER pb_prefix_operation_no_truncate
 BEFORE TRUNCATE ON periodic_budget_operations
 FOR EACH STATEMENT EXECUTE FUNCTION pb_prefix_immutable();
CREATE TRIGGER pb_prefix_control_no_truncate
 BEFORE TRUNCATE ON periodic_budget_controls
 FOR EACH STATEMENT EXECUTE FUNCTION pb_prefix_immutable();

CREATE FUNCTION pb_prefix_origin_guard() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
BEGIN
 PERFORM pb_require_read_committed();
 NEW.version:=1;
 NEW.created_xid:=pg_current_xact_id();
 RETURN NEW;
END $$;
CREATE TRIGGER pb_prefix_origin_guard BEFORE INSERT ON periodic_budget_observation_origins
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_origin_guard();

CREATE FUNCTION pb_prefix_head_guard() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE o periodic_budget_observation_origins%ROWTYPE;
 j periodic_budget_observation_journal%ROWTYPE; stamp xid8; tuple_xid xid;
BEGIN
 PERFORM pb_require_read_committed();
 IF TG_OP='INSERT' THEN
  SELECT * INTO STRICT o FROM periodic_budget_observation_origins WHERE control_id=NEW.control_id;
  SELECT created_xid,xmin INTO stamp,tuple_xid
   FROM periodic_budget_observation_origins WHERE control_id=NEW.control_id;
  IF NOT pb_current_tuple(stamp,tuple_xid) THEN
   RAISE EXCEPTION 'periodic observation head requires new origin' USING ERRCODE='23514'; END IF;
  NEW.ordinal:=0; NEW.last_observation_id:=NULL;
  NEW.updated_xid:=pg_current_xact_id();
  RETURN NEW;
 END IF;
 -- The executor already owns the head tuple. Do not acquire an upstream
 -- control/root lock here: normal capture owns control before head.
 IF NEW.control_id IS DISTINCT FROM OLD.control_id OR OLD.ordinal=9223372036854775807 THEN
  RAISE EXCEPTION 'periodic observation head identity or exhaustion' USING ERRCODE='23514'; END IF;
 SELECT * INTO STRICT j FROM periodic_budget_observation_journal WHERE id=NEW.last_observation_id;
 SELECT created_xid,xmin INTO stamp,tuple_xid
  FROM periodic_budget_observation_journal WHERE id=NEW.last_observation_id;
 IF NEW.ordinal IS DISTINCT FROM OLD.ordinal+1 OR j.control_id IS DISTINCT FROM OLD.control_id
  OR j.ordinal IS DISTINCT FROM NEW.ordinal OR j.previous_id IS DISTINCT FROM OLD.last_observation_id
  OR NOT pb_current_tuple(stamp,tuple_xid) THEN
  RAISE EXCEPTION 'periodic observation head requires exact new journal' USING ERRCODE='23514'; END IF;
 NEW.updated_xid:=pg_current_xact_id();
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation head source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_prefix_head_guard BEFORE INSERT OR UPDATE ON periodic_budget_observation_heads
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_head_guard();

CREATE FUNCTION pb_prefix_journal_guard() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE c periodic_budget_controls%ROWTYPE; h periodic_budget_observation_heads%ROWTYPE;
 w periodic_budget_windows%ROWTYPE;
BEGIN
 PERFORM pb_require_read_committed();
 SELECT * INTO STRICT h FROM periodic_budget_observation_heads WHERE control_id=NEW.control_id FOR UPDATE;
 -- A plain fresh SPI read is sufficient here. The source capture already owns
 -- control. A manual journal is never adopted by a later source INSERT.
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id;
 IF h.ordinal=9223372036854775807 THEN
  RAISE EXCEPTION 'periodic observation ordinal exhausted' USING ERRCODE='23514'; END IF;
 IF NEW.ordinal IS DISTINCT FROM h.ordinal+1 OR NEW.previous_id IS DISTINCT FROM h.last_observation_id
  OR (NEW.event_seq,NEW.last_event_id,NEW.configuration_revision,NEW.configuration_operation_id)
   IS DISTINCT FROM (c.event_seq,c.last_event_id,c.revision,c.configuration_operation_id) THEN
  RAISE EXCEPTION 'periodic observation cut or predecessor mismatch' USING ERRCODE='23514'; END IF;
 IF NEW.kind='window_birth' THEN
  IF (NEW.window_id,NEW.window_revision,NEW.window_last_event_id)
    IS DISTINCT FROM (NEW.source_id,0::bigint,NULL::text) OR NEW.observed_at IS NOT NULL THEN
   RAISE EXCEPTION 'periodic observation invalid window birth' USING ERRCODE='23514'; END IF;
 ELSIF NEW.kind='command' THEN
  IF NEW.observed_at IS NULL OR NOT isfinite(NEW.observed_at) THEN
   RAISE EXCEPTION 'periodic observation invalid command time' USING ERRCODE='23514'; END IF;
  SELECT * INTO w FROM periodic_budget_windows
   WHERE control_id=c.id AND start_at=pb_start(c.period_kind,NEW.observed_at);
  IF (NEW.window_id,NEW.window_revision,NEW.window_last_event_id)
    IS DISTINCT FROM (w.id,w.revision,w.last_event_id) THEN
   RAISE EXCEPTION 'periodic observation window mismatch' USING ERRCODE='23514'; END IF;
 ELSE RAISE EXCEPTION 'periodic observation invalid kind' USING ERRCODE='23514';
 END IF;
 NEW.created_xid:=pg_current_xact_id();
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_prefix_journal_guard BEFORE INSERT ON periodic_budget_observation_journal
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_journal_guard();

CREATE FUNCTION pb_prefix_append(control text,observation_kind text,source text,
 cut_seq bigint,cut_tail text,configuration_revision bigint,configuration_operation text,
 observed_window text,observed_revision bigint,observed_tail text,observation_time timestamptz)
 RETURNS text LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE h periodic_budget_observation_heads%ROWTYPE; observation text; changed text;
BEGIN
 PERFORM pb_require_read_committed();
 SELECT * INTO STRICT h FROM periodic_budget_observation_heads WHERE control_id=control FOR UPDATE;
 IF h.ordinal=9223372036854775807 THEN
  RAISE EXCEPTION 'periodic observation ordinal exhausted' USING ERRCODE='23514'; END IF;
 observation:=gen_random_uuid()::text;
 -- No ON CONFLICT/adoption, and no explicit generated/stamp INSERT columns.
 INSERT INTO periodic_budget_observation_journal
  (id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,
   configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at)
 VALUES(observation,control,h.ordinal+1,h.last_observation_id,observation_kind,source,
  cut_seq,cut_tail,configuration_revision,configuration_operation,
  observed_window,observed_revision,observed_tail,observation_time);
 UPDATE periodic_budget_observation_heads SET ordinal=h.ordinal+1,last_observation_id=observation
  WHERE control_id=control AND ordinal=h.ordinal AND last_observation_id IS NOT DISTINCT FROM h.last_observation_id
  RETURNING control_id INTO changed;
 IF changed IS DISTINCT FROM control THEN
  RAISE EXCEPTION 'periodic observation head advance missing' USING ERRCODE='23514'; END IF;
 RETURN observation;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation origin unavailable' USING ERRCODE='23514';
END $$;

CREATE FUNCTION pb_prefix_control_origin() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
BEGIN
 -- The original pb_control_guard has already overwritten stamp, tracking and
 -- zero financial state. Both INSERTs are unconditional and independently
 -- constrained; APP-created origins/heads are never reused.
 PERFORM pb_require_read_committed();
 IF (NEW.revision,NEW.held_amount,NEW.spent_amount,NEW.event_seq,NEW.last_event_id)
   IS DISTINCT FROM (1::bigint,0::numeric,0::numeric,0::bigint,NULL::text)
  OR NEW.created_xid IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'periodic observation requires original control birth' USING ERRCODE='23514'; END IF;
 INSERT INTO periodic_budget_observation_origins(control_id,version,tracking_started_at)
  VALUES(NEW.id,1,NEW.tracking_started_at);
 INSERT INTO periodic_budget_observation_heads(control_id,ordinal,last_observation_id)
  VALUES(NEW.id,0,NULL);
 RETURN NEW;
END $$;
CREATE TRIGGER pb_control_zz_prefix_origin BEFORE INSERT ON periodic_budget_controls
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_control_origin();

CREATE FUNCTION pb_prefix_operation_lock() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE c periodic_budget_controls%ROWTYPE;
BEGIN
 -- Identity/scope columns are immutable. This unlocked read only selects the
 -- root-lock route; no mutable authority/configuration is adopted from it.
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.resource_id;
 IF c.scope='tenant' THEN
  PERFORM id FROM tenants WHERE id=c.tenant_id FOR NO KEY UPDATE;
  PERFORM id FROM users WHERE id=NEW.actor FOR SHARE;
 ELSIF c.scope='key' THEN
  PERFORM id FROM tenants WHERE id=c.tenant_id FOR SHARE;
  PERFORM user_id FROM memberships WHERE tenant_id=c.tenant_id AND user_id=NEW.actor FOR SHARE;
  PERFORM id FROM users WHERE id=NEW.actor FOR SHARE;
  PERFORM id FROM api_keys WHERE tenant_id=c.tenant_id AND id=c.key_id FOR UPDATE;
 ELSIF c.scope='procurement' THEN
  PERFORM id FROM users WHERE id=NEW.actor FOR SHARE;
  PERFORM id FROM procurement_accounts WHERE id=c.account_id FOR NO KEY UPDATE;
 ELSE RAISE EXCEPTION 'periodic observation invalid control scope' USING ERRCODE='23514';
 END IF;
 PERFORM id FROM periodic_budget_controls WHERE id=c.id FOR NO KEY UPDATE;
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic operation resource missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_operation_00_prefix_lock BEFORE INSERT ON periodic_budget_operations
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_operation_lock();

CREATE FUNCTION pb_prefix_operation_capture() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE c periodic_budget_controls%ROWTYPE; w periodic_budget_windows%ROWTYPE;
 window_view jsonb; current_state text;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_observation_origins WHERE control_id=NEW.resource_id) THEN
  NEW.observation_id:=NULL; RETURN NEW;
 END IF;
 PERFORM pb_require_read_committed();
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.resource_id FOR NO KEY UPDATE;
 SELECT * INTO w FROM periodic_budget_windows
  WHERE control_id=c.id AND start_at=pb_start(c.period_kind,NEW.created_at);
 IF NEW.revision IS DISTINCT FROM c.revision OR NEW.created_xid IS DISTINCT FROM pg_current_xact_id()
  OR NEW.created_at IS NULL OR NOT isfinite(NEW.created_at)
  OR pb_start(c.period_kind,NEW.created_at)<c.max_observed_window_start
  OR c.held_amount<coalesce(w.held_amount,0) THEN
  RAISE EXCEPTION 'periodic observation invalid command snapshot' USING ERRCODE='23514'; END IF;
 window_view:='null'::jsonb;
 IF w.id IS NOT NULL THEN
  window_view:=jsonb_build_object('id',w.id,'start_at',pricing_time(w.start_at),'end_at',pricing_time(w.end_at),
   'spent_amount',w.spent_amount::text,'held_amount',w.held_amount::text);
 END IF;
 current_state:=CASE WHEN NOT c.enabled THEN 'disabled'
  WHEN NEW.created_at<c.effective_from THEN 'scheduled' ELSE 'active' END;
 -- The result and the cut use these exact same owned c/w values. In
 -- particular this does not bind a new cut to the old STABLE view's result.
 NEW.result_snapshot:=jsonb_build_object('scope',c.scope,'scope_id',coalesce(c.account_id,c.key_id,c.tenant_id),
  'revision',c.revision::text,'period_kind',c.period_kind,'enabled',c.enabled,'limit_amount',c.limit_amount::text,
  'state',current_state,'effective_from',pricing_time(c.effective_from),
  'tracking_started_at',pricing_time(c.tracking_started_at),'current_window',window_view,
  'carry_held',(c.held_amount-coalesce(w.held_amount,0))::text,'as_of',pricing_time(NEW.created_at));
 NEW.observation_id:=pb_prefix_append(c.id,'command',NEW.operation_id,c.event_seq,c.last_event_id,
  c.revision,c.configuration_operation_id,w.id,w.revision,w.last_event_id,NEW.created_at);
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation command source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_operation_zz_prefix_capture BEFORE INSERT ON periodic_budget_operations
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_operation_capture();

CREATE FUNCTION pb_prefix_window_capture() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE c periodic_budget_controls%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.observation_id,NEW.observation_created_xid)
   IS DISTINCT FROM (OLD.observation_id,OLD.observation_created_xid) THEN
   RAISE EXCEPTION 'periodic window observation immutable' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_observation_origins WHERE control_id=NEW.control_id) THEN
  NEW.observation_id:=NULL; NEW.observation_created_xid:=NULL; RETURN NEW;
 END IF;
 PERFORM pb_require_read_committed();
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=NEW.control_id FOR NO KEY UPDATE;
 IF (NEW.revision,NEW.last_event_id,NEW.held_amount,NEW.spent_amount)
   IS DISTINCT FROM (0::bigint,NULL::text,0::numeric,0::numeric) THEN
  RAISE EXCEPTION 'periodic observation invalid window birth state' USING ERRCODE='23514'; END IF;
 NEW.observation_created_xid:=pg_current_xact_id();
 NEW.observation_id:=pb_prefix_append(c.id,'window_birth',NEW.id,c.event_seq,c.last_event_id,
  c.revision,c.configuration_operation_id,NEW.id,0,NULL,NULL);
 RETURN NEW;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation window source missing' USING ERRCODE='23514';
END $$;
CREATE TRIGGER pb_window_zz_prefix_capture BEFORE INSERT OR UPDATE ON periodic_budget_windows
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_window_capture();

CREATE FUNCTION pb_prefix_check_origin() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE control text; c periodic_budget_controls%ROWTYPE;
 o periodic_budget_observation_origins%ROWTYPE; h periodic_budget_observation_heads%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='periodic_budget_controls' THEN control:=NEW.id;
 ELSE control:=NEW.control_id; END IF;
 SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=control;
 SELECT * INTO STRICT o FROM periodic_budget_observation_origins WHERE control_id=control;
 SELECT * INTO STRICT h FROM periodic_budget_observation_heads WHERE control_id=control;
 IF o.version<>1 OR (o.created_xid,o.tracking_started_at)
   IS DISTINCT FROM (c.created_xid,c.tracking_started_at)
  OR (h.ordinal=0 AND h.updated_xid IS DISTINCT FROM o.created_xid) THEN
  RAISE EXCEPTION 'periodic observation origin identity mismatch' USING ERRCODE='23514'; END IF;
 RETURN NULL;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation origin relation missing' USING ERRCODE='23514';
END $$;
CREATE CONSTRAINT TRIGGER pb_prefix_origin_control_check AFTER INSERT ON periodic_budget_controls
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_check_origin();
CREATE CONSTRAINT TRIGGER pb_prefix_origin_check AFTER INSERT ON periodic_budget_observation_origins
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_check_origin();
CREATE CONSTRAINT TRIGGER pb_prefix_head_origin_check AFTER INSERT OR UPDATE ON periodic_budget_observation_heads
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_check_origin();

CREATE FUNCTION pb_prefix_check_journal() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE j periodic_budget_observation_journal%ROWTYPE; previous periodic_budget_observation_journal%ROWTYPE;
 h periodic_budget_observation_heads%ROWTYPE; b periodic_budget_observation_journal%ROWTYPE;
 c periodic_budget_controls%ROWTYPE; w periodic_budget_windows%ROWTYPE;
BEGIN
 SELECT * INTO STRICT j FROM periodic_budget_observation_journal WHERE id=NEW.id;
 SELECT * INTO STRICT h FROM periodic_budget_observation_heads WHERE control_id=j.control_id;
 IF h.ordinal<j.ordinal THEN
  RAISE EXCEPTION 'periodic observation journal not reached by head' USING ERRCODE='23514'; END IF;
 IF j.ordinal=1 THEN
  IF j.previous_id IS NOT NULL THEN
   RAISE EXCEPTION 'periodic observation first predecessor invalid' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT * INTO STRICT previous FROM periodic_budget_observation_journal WHERE id=j.previous_id;
  IF previous.control_id IS DISTINCT FROM j.control_id OR previous.ordinal IS DISTINCT FROM j.ordinal-1
   OR previous.event_seq>j.event_seq
   OR (previous.event_seq=j.event_seq AND previous.last_event_id IS DISTINCT FROM j.last_event_id) THEN
   RAISE EXCEPTION 'periodic observation predecessor or cut invalid' USING ERRCODE='23514'; END IF;
 END IF;
 IF h.ordinal=j.ordinal AND (h.last_observation_id,h.updated_xid) IS DISTINCT FROM (j.id,j.created_xid) THEN
  RAISE EXCEPTION 'periodic observation final head mismatch' USING ERRCODE='23514'; END IF;
 IF j.kind='window_birth' THEN
  IF EXISTS(SELECT 1 FROM periodic_budget_events e JOIN periodic_budget_allocations a
    ON a.control_id=e.control_id AND a.id=e.allocation_id
    WHERE a.control_id=j.control_id AND a.window_id=j.source_id AND e.seq<=j.event_seq) THEN
   RAISE EXCEPTION 'periodic observation birth contains its window effect' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT * INTO STRICT c FROM periodic_budget_controls WHERE id=j.control_id;
  SELECT * INTO w FROM periodic_budget_windows
   WHERE control_id=c.id AND start_at=pb_start(c.period_kind,j.observed_at);
  IF w.id IS NOT NULL THEN
   SELECT * INTO STRICT b FROM periodic_budget_observation_journal WHERE id=w.observation_id;
   IF b.kind IS DISTINCT FROM 'window_birth' OR b.control_id IS DISTINCT FROM c.id
    OR b.source_id IS DISTINCT FROM w.id OR b.ordinal=j.ordinal THEN
    RAISE EXCEPTION 'periodic observation command window birth missing' USING ERRCODE='23514'; END IF;
   IF (b.ordinal<j.ordinal AND j.window_id IS DISTINCT FROM w.id)
    OR (b.ordinal>j.ordinal AND j.window_id IS NOT NULL) THEN
    RAISE EXCEPTION 'periodic observation command window existence mismatch' USING ERRCODE='23514'; END IF;
  ELSIF j.window_id IS NOT NULL THEN
   RAISE EXCEPTION 'periodic observation command window missing' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NULL;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation journal relation missing' USING ERRCODE='23514';
END $$;
CREATE CONSTRAINT TRIGGER pb_prefix_journal_check AFTER INSERT ON periodic_budget_observation_journal
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_check_journal();

CREATE FUNCTION pb_prefix_check_source() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
DECLARE control text; observation text; stamp xid8; covered boolean;
BEGIN
 IF TG_TABLE_NAME='periodic_budget_operations' THEN
  SELECT resource_id,observation_id INTO STRICT control,observation
   FROM periodic_budget_operations WHERE operation_id=NEW.operation_id;
 ELSE
  SELECT control_id,observation_id,observation_created_xid INTO STRICT control,observation,stamp
   FROM periodic_budget_windows WHERE id=NEW.id;
 END IF;
 covered:=EXISTS(SELECT 1 FROM periodic_budget_observation_origins WHERE control_id=control);
 IF covered IS DISTINCT FROM (observation IS NOT NULL)
  OR (TG_TABLE_NAME='periodic_budget_windows' AND covered IS DISTINCT FROM (stamp IS NOT NULL)) THEN
  RAISE EXCEPTION 'periodic observation source coverage mismatch' USING ERRCODE='23514'; END IF;
 RETURN NULL;
EXCEPTION WHEN no_data_found THEN
 RAISE EXCEPTION 'periodic observation source relation missing' USING ERRCODE='23514';
END $$;
CREATE CONSTRAINT TRIGGER pb_prefix_operation_source_check AFTER INSERT ON periodic_budget_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_check_source();
CREATE CONSTRAINT TRIGGER pb_prefix_window_source_check AFTER INSERT ON periodic_budget_windows
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_check_source();

-- Freeze every new function's lookup path to this installation schema, and
-- remove implicit PUBLIC execution. The installer never trusts caller GUCs.
DO $$
DECLARE f record;
BEGIN
 FOR f IN SELECT p.oid,p.proname,pg_get_function_identity_arguments(p.oid) AS args
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND left(p.proname,10)='pb_prefix_' LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',
   current_schema(),f.proname,f.args,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %I.%I(%s) FROM PUBLIC',current_schema(),f.proname,f.args);
 END LOOP;
END $$;
REVOKE ALL ON periodic_budget_observation_origins,periodic_budget_observation_heads,
 periodic_budget_observation_journal FROM PUBLIC;
REVOKE ALL ON TYPE periodic_budget_observation_origins,periodic_budget_observation_heads,
 periodic_budget_observation_journal FROM PUBLIC;

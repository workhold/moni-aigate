-- All channel writers participate in the same monotonic management revision.
ALTER TABLE channels ADD COLUMN management_revision BIGINT NOT NULL DEFAULT 1 CHECK(management_revision>0);
CREATE FUNCTION advance_channel_management_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.management_revision := 1;
 ELSE NEW.management_revision := OLD.management_revision+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER channel_management_revision BEFORE INSERT OR UPDATE ON channels FOR EACH ROW EXECUTE FUNCTION advance_channel_management_revision();

CREATE FUNCTION valid_channel_batch_revision(value TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF value IS NULL OR value !~ '^[1-9][0-9]{0,18}$' THEN RETURN false; END IF;
 RETURN value::NUMERIC<=9223372036854775807;
END $$;
CREATE FUNCTION valid_channel_batch_state(value JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
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
END $$;

-- A receipt stores only the fixed metadata schema, never provider payloads.
CREATE FUNCTION valid_channel_batch_items(items JSONB,operation_status TEXT,operation_action TEXT,targets INTEGER,changes INTEGER) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
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
END $$;

CREATE TABLE channel_batch_operations (
 operation_id TEXT PRIMARY KEY CHECK(operation_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 actor TEXT NOT NULL REFERENCES users(id),
 action TEXT NOT NULL CHECK(action IN ('enable','disable')),
 input_digest TEXT NOT NULL CHECK(input_digest ~ '^[0-9a-f]{64}$'),
 status TEXT NOT NULL CHECK(status IN ('applied','rejected')),
 target_count INTEGER NOT NULL CHECK(target_count BETWEEN 1 AND 100),
 changed_count INTEGER NOT NULL CHECK(changed_count BETWEEN 0 AND target_count),
 items JSONB NOT NULL CHECK(valid_channel_batch_items(items,status,action,target_count,changed_count)),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX channel_batch_operations_time ON channel_batch_operations(created_at DESC,operation_id DESC);
CREATE TRIGGER immutable_channel_batch_operation BEFORE UPDATE OR DELETE ON channel_batch_operations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE UNIQUE INDEX channel_batch_operation_unique_audit ON audit_events(target) WHERE action IN ('channel.batch.apply','channel.batch.reject');

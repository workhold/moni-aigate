-- Freeze each version's complete credential pool. Credentials remain mutable
-- only for irreversible revocation and offline ciphertext rewrapping.
ALTER TABLE channel_versions ADD CONSTRAINT channel_versions_channel_identity UNIQUE(id,channel_id);
ALTER TABLE channel_versions ADD COLUMN credential_pool_creation_xid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE FUNCTION set_credential_pool_creation_xid() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.credential_pool_creation_xid := pg_current_xact_id();
 RETURN NEW;
END $$;
CREATE TRIGGER channel_version_pool_creation BEFORE INSERT ON channel_versions FOR EACH ROW EXECUTE FUNCTION set_credential_pool_creation_xid();
CREATE TABLE channel_version_credentials (
 channel_version_id TEXT NOT NULL,
 channel_id TEXT NOT NULL,
 credential_id TEXT NOT NULL,
 position INTEGER NOT NULL CHECK(position BETWEEN 1 AND 32),
 label TEXT NOT NULL CHECK(octet_length(label) BETWEEN 1 AND 64 AND label !~ '[[:cntrl:]]' AND label ~ '[^[:space:]]'),
 weight INTEGER NOT NULL CHECK(weight BETWEEN 1 AND 1000000),
 max_inflight INTEGER NOT NULL CHECK(max_inflight BETWEEN 1 AND 1000),
 PRIMARY KEY(channel_version_id,credential_id),
 UNIQUE(channel_version_id,position),
 UNIQUE(channel_version_id,channel_id,credential_id),
 FOREIGN KEY(channel_version_id,channel_id) REFERENCES channel_versions(id,channel_id),
 FOREIGN KEY(credential_id,channel_id) REFERENCES channel_credentials(id,channel_id)
);
CREATE INDEX channel_version_credentials_credential ON channel_version_credentials(credential_id,channel_version_id);
CREATE FUNCTION require_credential_pool_creation_transaction() RETURNS trigger LANGUAGE plpgsql AS $$
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
INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight)
 SELECT id,channel_id,credential_id,1,'primary',1,1000 FROM channel_versions;
CREATE TRIGGER channel_version_credential_creation BEFORE INSERT ON channel_version_credentials FOR EACH ROW EXECUTE FUNCTION require_credential_pool_creation_transaction();
ALTER TABLE channel_versions ADD CONSTRAINT channel_versions_primary_membership
 FOREIGN KEY(id,channel_id,credential_id) REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id)
 DEFERRABLE INITIALLY DEFERRED;
CREATE TRIGGER immutable_channel_version_credential BEFORE UPDATE OR DELETE ON channel_version_credentials FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

-- A directory read may use a live secondary after the primary is revoked.
-- Preserve all old records, replacing only the former primary-only identity FK.
DO $$
DECLARE old_constraint TEXT;
BEGIN
 SELECT conname INTO STRICT old_constraint FROM pg_constraint
 WHERE conrelid='channel_model_discoveries'::regclass
   AND confrelid='channel_versions'::regclass AND contype='f'
   AND array_length(conkey,1)=3;
 EXECUTE format('ALTER TABLE channel_model_discoveries DROP CONSTRAINT %I',old_constraint);
END $$;
ALTER TABLE channel_model_discoveries ADD CONSTRAINT channel_model_discoveries_pool_identity
 FOREIGN KEY(channel_version_id,channel_id,credential_id)
 REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id);
ALTER TABLE upstream_attempts ADD CONSTRAINT upstream_attempts_pool_identity
 FOREIGN KEY(channel_version_id,channel_id,credential_id)
 REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id);
ALTER TABLE platform_probe_attempts ADD CONSTRAINT platform_probe_attempts_pool_identity
 FOREIGN KEY(channel_version_id,channel_id,credential_id)
 REFERENCES channel_version_credentials(channel_version_id,channel_id,credential_id);

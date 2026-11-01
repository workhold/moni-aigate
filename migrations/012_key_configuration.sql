ALTER TABLE api_keys
 ADD COLUMN configuration_revision BIGINT NOT NULL DEFAULT 1 CHECK(configuration_revision>0);

-- This edit lane is independent of source/runtime policy and financial usage.
-- Existing restrictions remain byte-for-byte intact until explicitly edited.
CREATE FUNCTION advance_key_configuration_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.configuration_revision:=1;
 ELSIF ROW(NEW.name,NEW.scopes,NEW.allowed_regions,NEW.lifetime_limit,NEW.expires_at) IS DISTINCT FROM ROW(OLD.name,OLD.scopes,OLD.allowed_regions,OLD.lifetime_limit,OLD.expires_at) THEN NEW.configuration_revision:=OLD.configuration_revision+1;
 ELSE NEW.configuration_revision:=OLD.configuration_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_configuration_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_configuration_revision();

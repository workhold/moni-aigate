ALTER TABLE models
 ADD COLUMN public_visible BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN public_revision BIGINT NOT NULL DEFAULT 1 CHECK(public_revision>0);

CREATE FUNCTION guard_model_public_revision() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER model_public_revision BEFORE INSERT OR UPDATE ON models
 FOR EACH ROW EXECUTE FUNCTION guard_model_public_revision();

CREATE INDEX models_public_catalog ON models(public_name COLLATE "C",id COLLATE "C")
 WHERE public_visible AND enabled;

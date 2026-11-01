ALTER TABLE api_keys
 ADD COLUMN rpm_limit INTEGER NOT NULL DEFAULT 60 CHECK(rpm_limit BETWEEN 1 AND 6000),
 ADD COLUMN burst_limit INTEGER NOT NULL DEFAULT 20 CHECK(burst_limit BETWEEN 1 AND 200),
 ADD COLUMN concurrency_limit INTEGER NOT NULL DEFAULT 16 CHECK(concurrency_limit BETWEEN 1 AND 16),
 ADD COLUMN limit_revision BIGINT NOT NULL DEFAULT 1 CHECK(limit_revision>0);

-- Runtime admission and source restrictions have independent edit revisions.
-- Counters, unrelated key attributes and caller supplied revisions cannot reset
-- or advance this revision; every actual runtime tuple change advances once.
CREATE FUNCTION advance_key_runtime_limit_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.limit_revision:=1;
 ELSIF ROW(NEW.rpm_limit,NEW.burst_limit,NEW.concurrency_limit) IS DISTINCT FROM ROW(OLD.rpm_limit,OLD.burst_limit,OLD.concurrency_limit) THEN NEW.limit_revision:=OLD.limit_revision+1;
 ELSE NEW.limit_revision:=OLD.limit_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_runtime_limits_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_runtime_limit_revision();

-- Additive diagnostic metadata: old attempts retain an empty explanation.
ALTER TABLE upstream_attempts ADD COLUMN route_decisions JSONB NOT NULL DEFAULT '[]'::jsonb
 CHECK(jsonb_typeof(route_decisions)='array' AND jsonb_array_length(route_decisions)<=1000 AND octet_length(route_decisions::text)<=262144);
CREATE FUNCTION protect_route_decisions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.route_decisions IS DISTINCT FROM OLD.route_decisions THEN
  RAISE EXCEPTION 'route decision snapshots are immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_route_decisions BEFORE UPDATE OF route_decisions ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION protect_route_decisions();

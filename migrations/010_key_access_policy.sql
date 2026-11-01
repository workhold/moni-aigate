-- PostgreSQL prints IPv4-compatible IPv6 using dotted notation. Reconstruct
-- RFC 5952 hex form so database constraints agree with Go net/netip for all IPv6.
CREATE FUNCTION canonical_key_client_cidr(value TEXT) RETURNS TEXT LANGUAGE plpgsql IMMUTABLE AS $$
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

CREATE FUNCTION valid_key_client_cidrs(items TEXT[]) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
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

ALTER TABLE api_keys
 ADD COLUMN allowed_client_cidrs TEXT[] NOT NULL DEFAULT '{}' CHECK(valid_key_client_cidrs(allowed_client_cidrs)),
 ADD COLUMN access_policy_revision BIGINT NOT NULL DEFAULT 1 CHECK(access_policy_revision>0);

-- Financial counters and unrelated attributes never invalidate policy editors.
-- Every writer gets the same monotonic revision; caller supplied values are ignored.
CREATE FUNCTION advance_key_access_policy_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.access_policy_revision:=1;
 ELSIF NEW.allowed_client_cidrs IS DISTINCT FROM OLD.allowed_client_cidrs THEN NEW.access_policy_revision:=OLD.access_policy_revision+1;
 ELSE NEW.access_policy_revision:=OLD.access_policy_revision;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_access_policy_revision BEFORE INSERT OR UPDATE ON api_keys FOR EACH ROW EXECUTE FUNCTION advance_key_access_policy_revision();

ALTER TABLE request_records ADD COLUMN client_ip INET;
CREATE FUNCTION preserve_request_client_ip() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.client_ip IS DISTINCT FROM OLD.client_ip THEN RAISE EXCEPTION 'request source IP is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_request_client_ip BEFORE UPDATE OF client_ip ON request_records FOR EACH ROW EXECUTE FUNCTION preserve_request_client_ip();

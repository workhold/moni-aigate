-- DESIGN PROTOTYPE ONLY. Standalone receiver schema; no mock/application helper calls.
-- Apply as offline receiver owner, search_path=<fresh_receiver>,pg_catalog,pg_temp.
CREATE FUNCTION receiver_uuid(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$ SELECT v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v<>'00000000-0000-0000-0000-000000000000' $$;
CREATE FUNCTION receiver_ref(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$ SELECT octet_length(v) BETWEEN 1 AND 100 AND NOT EXISTS(SELECT 1 FROM regexp_split_to_table(v,'') c WHERE ascii(c)<128 AND c !~ '^[A-Za-z0-9:_./-]$') AND position('://' in v)=0 AND lower(v) !~ '^(sk-|mg_|bearer)' $$;
CREATE FUNCTION receiver_json(v bytea) RETURNS jsonb LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$ SELECT convert_from(v,'UTF8')::jsonb $$;
CREATE FUNCTION receiver_hash(v bytea) RETURNS text LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$ SELECT encode(sha256(v),'hex') $$;
CREATE FUNCTION receiver_fields(j jsonb,required text[]) RETURNS boolean LANGUAGE sql IMMUTABLE SET search_path FROM CURRENT AS $$ SELECT CASE WHEN jsonb_typeof(j) IS DISTINCT FROM 'object' THEN false ELSE ARRAY(SELECT jsonb_object_keys(j) ORDER BY 1)=ARRAY(SELECT unnest(required) ORDER BY 1) END $$;
CREATE FUNCTION receiver_strings(j jsonb,names text[]) RETURNS boolean LANGUAGE sql IMMUTABLE SET search_path FROM CURRENT AS $$ SELECT j IS NOT NULL AND NOT EXISTS(SELECT 1 FROM unnest(names) n WHERE jsonb_typeof(j->n) IS DISTINCT FROM 'string') $$;
CREATE FUNCTION receiver_immutable() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN RAISE EXCEPTION 'immutable receiver history';END $$;
CREATE FUNCTION receiver_owner_insert() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN
 IF current_user<>(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) THEN RAISE EXCEPTION 'receiver owner required';END IF;
 IF TG_TABLE_NAME='receiver_signing_keys' AND (SELECT count(*) FROM receiver_signing_keys)>=128 THEN RAISE EXCEPTION 'receiver signing key limit';END IF;RETURN NEW;
END $$;
CREATE TABLE receiver_schema_migrations(version integer PRIMARY KEY CHECK(version=1),sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),applied_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE receiver_environment(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),environment_id text NOT NULL UNIQUE CHECK(receiver_uuid(environment_id)),merchant_id text NOT NULL CHECK(receiver_ref(merchant_id)),
 database_oid oid NOT NULL,receiver_schema_name text NOT NULL,receiver_schema_oid oid NOT NULL,receiver_owner text NOT NULL,receiver_runtime text NOT NULL,
 state_directory_id text NOT NULL CHECK(receiver_uuid(state_directory_id)),nonce_digest text NOT NULL CHECK(nonce_digest ~ '^[0-9a-f]{64}$'),callback_url text NOT NULL,protocol_version integer NOT NULL CHECK(protocol_version=1),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(environment_id,merchant_id)
);
CREATE TABLE receiver_signing_keys(
 key_id text PRIMARY KEY CHECK(key_id ~ '^[A-Za-z0-9_-]{1,32}$'),key_digest text NOT NULL UNIQUE CHECK(key_digest ~ '^[0-9a-f]{64}$'),not_before timestamptz NOT NULL,not_after timestamptz NOT NULL,
 CHECK(not_before<=not_after AND not_before>='0001-01-01T00:00:00Z' AND not_after<'10000-01-01T00:00:00Z' AND not_before<>'0001-01-01T00:00:00Z' AND not_after<>'0001-01-01T00:00:00Z')
);
CREATE TABLE receiver_inbox(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(receiver_uuid(id)),environment_id text NOT NULL,merchant_id text NOT NULL,event_id text NOT NULL CHECK(receiver_ref(event_id)),
 business_digest text NOT NULL CHECK(business_digest ~ '^[0-9a-f]{64}$'),canonical_body bytea NOT NULL CHECK(octet_length(canonical_body) BETWEEN 1 AND 65536),
 first_delivery_id text NOT NULL CHECK(receiver_uuid(first_delivery_id)),delivery_time bigint NOT NULL CHECK(delivery_time BETWEEN 0 AND 253402300799),signing_key_id text NOT NULL REFERENCES receiver_signing_keys(key_id),
 verified_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(environment_id,merchant_id,event_id),FOREIGN KEY(environment_id,merchant_id) REFERENCES receiver_environment(environment_id,merchant_id),CHECK(business_digest=receiver_hash(canonical_body))
);
CREATE TABLE receiver_receipts(
 inbox_id text PRIMARY KEY REFERENCES receiver_inbox(id),receipt_id text NOT NULL UNIQUE DEFAULT gen_random_uuid()::text CHECK(receiver_uuid(receipt_id)),received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 receipt_data bytea NOT NULL CHECK(octet_length(receipt_data) BETWEEN 1 AND 8192),receipt_digest text NOT NULL CHECK(receipt_digest ~ '^[0-9a-f]{64}$'),CHECK(receipt_digest=receiver_hash(receipt_data))
);
CREATE TABLE receiver_conflicts(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(receiver_uuid(id)),inbox_id text NOT NULL REFERENCES receiver_inbox(id),observed_digest text NOT NULL CHECK(observed_digest ~ '^[0-9a-f]{64}$'),
 delivery_id text NOT NULL CHECK(receiver_uuid(delivery_id)),delivery_time bigint NOT NULL CHECK(delivery_time BETWEEN 0 AND 253402300799),signing_key_id text NOT NULL REFERENCES receiver_signing_keys(key_id),observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(inbox_id,delivery_id,observed_digest)
);
CREATE FUNCTION receiver_input_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE j jsonb;e receiver_environment%ROWTYPE;i receiver_inbox%ROWTYPE;k receiver_signing_keys%ROWTYPE;observed timestamptz;
BEGIN
 SELECT * INTO e FROM receiver_environment WHERE singleton;IF e.environment_id IS NULL THEN RAISE EXCEPTION 'receiver environment missing';END IF;
 IF TG_TABLE_NAME='receiver_inbox' THEN
  j:=receiver_json(NEW.canonical_body);
  IF NOT receiver_strings(j,ARRAY['environment_id','merchant_id','event_id','event_type','order_id','amount','currency','occurred_at','provider_order_revision']) OR j->'schema_version' IS DISTINCT FROM '1'::jsonb OR j->>'environment_id'<>NEW.environment_id OR j->>'merchant_id'<>NEW.merchant_id OR j->>'event_id'<>NEW.event_id OR NOT receiver_uuid(j->>'order_id') THEN RAISE EXCEPTION 'receiver event binding';END IF;
  NEW.verified_at:=transaction_timestamp();SELECT * INTO k FROM receiver_signing_keys WHERE key_id=NEW.signing_key_id;observed:=NEW.verified_at;
 ELSIF TG_TABLE_NAME='receiver_receipts' THEN
  SELECT * INTO i FROM receiver_inbox WHERE id=NEW.inbox_id;j:=receiver_json(NEW.receipt_data);
  IF i.id IS NULL OR NOT receiver_fields(j,ARRAY['receipt_id','event_id','status','environment','received_at']) OR NOT receiver_strings(j,ARRAY['receipt_id','event_id','status','environment','received_at']) OR j->>'receipt_id'<>NEW.receipt_id OR j->>'event_id'<>i.event_id OR j->>'status'<>'received' OR j->>'environment'<>'sandbox' OR j->>'received_at'<>to_char(NEW.received_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') THEN RAISE EXCEPTION 'receiver receipt binding';END IF;
  IF NEW.received_at<i.verified_at OR NEW.received_at>clock_timestamp()+interval '5 seconds' THEN RAISE EXCEPTION 'receiver receipt time';END IF;RETURN NEW;
 ELSIF TG_TABLE_NAME='receiver_conflicts' THEN
  SELECT * INTO i FROM receiver_inbox WHERE id=NEW.inbox_id;
  IF i.id IS NULL OR NEW.observed_digest=i.business_digest THEN RAISE EXCEPTION 'receiver conflict binding';END IF;
  NEW.observed_at:=transaction_timestamp();SELECT * INTO k FROM receiver_signing_keys WHERE key_id=NEW.signing_key_id;observed:=NEW.observed_at;
 END IF;
 IF k.key_id IS NULL OR to_timestamp(NEW.delivery_time)<k.not_before OR to_timestamp(NEW.delivery_time)>k.not_after OR abs(extract(epoch FROM observed)-NEW.delivery_time)>300 THEN RAISE EXCEPTION 'receiver signing window';END IF;RETURN NEW;
END $$;
CREATE FUNCTION receiver_complete_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE ref text;
BEGIN
 IF TG_TABLE_NAME='receiver_inbox' THEN ref:=NEW.id;ELSE ref:=NEW.inbox_id;END IF;
 IF NOT EXISTS(SELECT 1 FROM receiver_inbox i JOIN receiver_receipts r ON r.inbox_id=i.id WHERE i.id=ref) THEN RAISE EXCEPTION 'receiver inbox receipt incomplete';END IF;RETURN NULL;
END $$;
DO $$ DECLARE t text;BEGIN
 FOREACH t IN ARRAY ARRAY['receiver_schema_migrations','receiver_environment','receiver_signing_keys','receiver_inbox','receiver_receipts','receiver_conflicts'] LOOP EXECUTE format('CREATE TRIGGER immutable_history BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION receiver_immutable()',t);END LOOP;
 FOREACH t IN ARRAY ARRAY['receiver_schema_migrations','receiver_environment','receiver_signing_keys'] LOOP EXECUTE format('CREATE TRIGGER owner_insert BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION receiver_owner_insert()',t);END LOOP;
 FOREACH t IN ARRAY ARRAY['receiver_inbox','receiver_receipts','receiver_conflicts'] LOOP EXECUTE format('CREATE TRIGGER receiver_input BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION receiver_input_guard()',t);EXECUTE format('CREATE CONSTRAINT TRIGGER receiver_complete AFTER INSERT ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION receiver_complete_guard()',t);END LOOP;
END $$;

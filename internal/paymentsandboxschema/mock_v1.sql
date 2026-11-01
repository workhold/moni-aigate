-- DESIGN PROTOTYPE ONLY. Not an application migration or runtime initializer.
-- Apply as the pinned offline mock owner with search_path=<fresh_mock>,pg_catalog,pg_temp.
-- The caller creates/attests the random schema and never applies this to public.

CREATE FUNCTION mock_uuid(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
 SELECT v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v<>'00000000-0000-0000-0000-000000000000'
$$;
CREATE FUNCTION mock_ref(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
 SELECT octet_length(v) BETWEEN 1 AND 100 AND NOT EXISTS(SELECT 1 FROM regexp_split_to_table(v,'') c WHERE ascii(c)<128 AND c !~ '^[A-Za-z0-9:_./-]$')
 AND position('://' in v)=0 AND lower(v) !~ '^(sk-|mg_|bearer)'
$$;
CREATE FUNCTION mock_merchant_order(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
 SELECT octet_length(v)=78 AND left(v,5)='mps1.' AND substr(v,42,1)='.' AND mock_uuid(substr(v,6,36)) AND mock_uuid(substr(v,43,36))
$$;
CREATE FUNCTION mock_json(v bytea) RETURNS jsonb LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$ SELECT convert_from(v,'UTF8')::jsonb $$;
CREATE FUNCTION mock_hash(v bytea) RETURNS text LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$ SELECT encode(sha256(v),'hex') $$;
CREATE FUNCTION mock_fields(j jsonb, required text[]) RETURNS boolean LANGUAGE sql IMMUTABLE SET search_path FROM CURRENT AS $$
 SELECT CASE WHEN jsonb_typeof(j) IS DISTINCT FROM 'object' THEN false ELSE ARRAY(SELECT jsonb_object_keys(j) ORDER BY 1)=ARRAY(SELECT unnest(required) ORDER BY 1) END
$$;
CREATE FUNCTION mock_strings(j jsonb, names text[]) RETURNS boolean LANGUAGE sql IMMUTABLE SET search_path FROM CURRENT AS $$
 SELECT j IS NOT NULL AND NOT EXISTS(SELECT 1 FROM unnest(names) n WHERE jsonb_typeof(j->n) IS DISTINCT FROM 'string')
$$;
CREATE FUNCTION mock_positive(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
 SELECT v ~ '^[1-9][0-9]{0,18}$' AND (length(v)<19 OR v<='9223372036854775807' COLLATE "C")
$$;
CREATE FUNCTION mock_time(v text) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
DECLARE t timestamptz;
BEGIN
 IF v !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$' OR left(v,4)='0000' THEN RETURN false;END IF;
 BEGIN t:=v::timestamptz; EXCEPTION WHEN OTHERS THEN RETURN false;END;
 RETURN to_char(t AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')=v;
END $$;
CREATE FUNCTION mock_immutable() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN RAISE EXCEPTION 'immutable mock history'; END $$;
CREATE FUNCTION mock_owner_insert() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN
 IF current_user<>(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) THEN RAISE EXCEPTION 'mock owner required';END IF;IF TG_TABLE_NAME='mock_signing_keys' AND (SELECT count(*) FROM mock_signing_keys)>=128 THEN RAISE EXCEPTION 'mock signing key limit';END IF;RETURN NEW;
END $$;

CREATE TABLE mock_schema_migrations(version integer PRIMARY KEY CHECK(version=1),sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),applied_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE mock_environment(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),environment_id text NOT NULL UNIQUE CHECK(mock_uuid(environment_id)),state_directory_id text NOT NULL CHECK(mock_uuid(state_directory_id)),
 database_oid oid NOT NULL,app_schema_name text NOT NULL,app_schema_oid oid NOT NULL,app_owner text NOT NULL,app_runtime text NOT NULL,app_projector text NOT NULL,
 mock_schema_name text NOT NULL,mock_schema_oid oid NOT NULL,mock_owner text NOT NULL,mock_runtime text NOT NULL,
 receiver_schema_name text NOT NULL,receiver_schema_oid oid NOT NULL,receiver_owner text NOT NULL,receiver_runtime text NOT NULL,
 merchant_id text NOT NULL CHECK(mock_ref(merchant_id)),payee_label text NOT NULL CHECK(octet_length(payee_label) BETWEEN 1 AND 100 AND payee_label !~ '[[:cntrl:]]' AND position('://' in payee_label)=0),
 nonce_digest text NOT NULL CHECK(nonce_digest ~ '^[0-9a-f]{64}$'),api_token_digest text NOT NULL CHECK(api_token_digest ~ '^[0-9a-f]{64}$'),control_token_digest text NOT NULL CHECK(control_token_digest ~ '^[0-9a-f]{64}$'),
 api_origin text NOT NULL,control_origin text NOT NULL,callback_url text NOT NULL,protocol_version integer NOT NULL CHECK(protocol_version=1),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(environment_id,merchant_id),CHECK(app_schema_oid<>mock_schema_oid AND app_schema_oid<>receiver_schema_oid AND mock_schema_oid<>receiver_schema_oid),CHECK(api_token_digest<>control_token_digest)
);
CREATE TABLE mock_signing_keys(
 key_id text PRIMARY KEY CHECK(key_id ~ '^[A-Za-z0-9_-]{1,32}$'),key_digest text NOT NULL UNIQUE CHECK(key_digest ~ '^[0-9a-f]{64}$'),not_before timestamptz NOT NULL,not_after timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(not_before<=not_after),CHECK(not_before>='0001-01-01T00:00:00Z' AND not_after<'10000-01-01T00:00:00Z'),CHECK(not_before<>'0001-01-01T00:00:00Z' AND not_after<>'0001-01-01T00:00:00Z')
);
CREATE TABLE mock_controls(
 operation_id text PRIMARY KEY CHECK(mock_uuid(operation_id)),action text NOT NULL CHECK(action IN ('capture_order','emit_test_event','release_command','arm_fault')),
 scenario text NOT NULL,target_reference text NOT NULL,parameter_digest text NOT NULL CHECK(parameter_digest ~ '^[0-9a-f]{64}$'),canonical_parameters bytea NOT NULL CHECK(octet_length(canonical_parameters) BETWEEN 1 AND 8192),
 result_body bytea NOT NULL CHECK(octet_length(result_body) BETWEEN 1 AND 65536),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),CHECK(parameter_digest=mock_hash(canonical_parameters)),
 CHECK((action='capture_order' AND scenario='normal') OR (action='release_command' AND scenario='normal') OR
 (action='emit_test_event' AND scenario IN ('capture_at_created','capture_before_created_1us','capture_before_expiry_1us','capture_at_expiry','capture_future_6s','amount_plus_one','duplicate_capture_other_transaction','same_revision_conflict','refund_unsupported','dispute_unsupported')) OR
 (action='arm_fault' AND scenario IN ('before_command_commit_disconnect_once','after_command_commit_drop_reply_once','after_query_snapshot_commit_drop_reply_once','hold_checkout_before_effect','hold_cancel_before_effect','before_callback_send_once')))
);
CREATE TABLE mock_orders(
 merchant_order_id text PRIMARY KEY CHECK(mock_merchant_order(merchant_order_id)),order_id text NOT NULL UNIQUE CHECK(mock_uuid(order_id)),environment_id text NOT NULL,merchant_id text NOT NULL,
 amount bigint CHECK(amount>0),currency text NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),created_at timestamptz,expires_at timestamptz,state text NOT NULL CHECK(state IN ('ready','captured','closed')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision IN (1,2)),checkout_ref text,closed_at timestamptz,created_locally_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(environment_id,merchant_id) REFERENCES mock_environment(environment_id,merchant_id),CHECK(merchant_order_id='mps1.'||environment_id||'.'||order_id),
 CHECK(((amount IS NULL AND created_at IS NULL AND expires_at IS NULL AND checkout_ref IS NULL AND state='closed' AND revision=1) OR
 (amount IS NOT NULL AND created_at IS NOT NULL AND expires_at IS NOT NULL AND expires_at=created_at+interval '900 seconds' AND checkout_ref IS NOT NULL AND checkout_ref ~ '^checkout:[0-9a-f-]{36}$' AND mock_uuid(substr(checkout_ref,10)))) IS TRUE),
 CHECK(created_at IS NULL OR (created_at>='0001-01-01T00:00:00Z' AND expires_at<'10000-01-01T00:00:00Z')),
 CHECK((state='closed')=(closed_at IS NOT NULL))
);
CREATE TABLE mock_commands(
 command_id text PRIMARY KEY CHECK(mock_uuid(command_id)),merchant_order_id text NOT NULL CHECK(mock_merchant_order(merchant_order_id)),kind text NOT NULL CHECK(kind IN ('checkout','cancel')),
 parameter_digest text NOT NULL CHECK(parameter_digest ~ '^[0-9a-f]{64}$'),canonical_parameters bytea NOT NULL CHECK(octet_length(canonical_parameters) BETWEEN 1 AND 8192),
 initial_reply bytea NOT NULL CHECK(octet_length(initial_reply) BETWEEN 1 AND 65536),initial_reply_digest text NOT NULL CHECK(initial_reply_digest ~ '^[0-9a-f]{64}$'),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(parameter_digest=mock_hash(canonical_parameters)),CHECK(initial_reply_digest=mock_hash(initial_reply)),UNIQUE(command_id,merchant_order_id,kind,parameter_digest)
);
CREATE TABLE mock_fault_plans(
 control_operation_id text PRIMARY KEY REFERENCES mock_controls(operation_id),boundary text NOT NULL CHECK(boundary IN ('before_command_commit_disconnect_once','after_command_commit_drop_reply_once','after_query_snapshot_commit_drop_reply_once','hold_checkout_before_effect','hold_cancel_before_effect','before_callback_send_once')),
 target_kind text NOT NULL CHECK(target_kind IN ('command','query','callback')),target_id text NOT NULL,remaining integer NOT NULL DEFAULT 1 CHECK(remaining IN (0,1)),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),consumed_at timestamptz,
 CHECK((remaining=0)=(consumed_at IS NOT NULL)),CHECK((boundary='after_query_snapshot_commit_drop_reply_once' AND target_kind='query' AND mock_uuid(target_id)) OR
 (boundary='before_callback_send_once' AND target_kind='callback' AND mock_ref(target_id)) OR
 (boundary NOT IN ('after_query_snapshot_commit_drop_reply_once','before_callback_send_once') AND target_kind='command' AND mock_uuid(target_id)))
);
CREATE UNIQUE INDEX mock_fault_one_active_target ON mock_fault_plans(target_kind,target_id) WHERE remaining=1;
CREATE TABLE mock_command_processing(
 command_id text PRIMARY KEY REFERENCES mock_commands(command_id),state text NOT NULL CHECK(state IN ('held','completed')),held_by_control_id text REFERENCES mock_fault_plans(control_operation_id),
 released_by_control_id text REFERENCES mock_controls(operation_id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),completed_at timestamptz,
 CHECK((state='completed')=(completed_at IS NOT NULL)),CHECK(released_by_control_id IS NULL OR (state='completed' AND held_by_control_id IS NOT NULL))
);
CREATE TABLE mock_command_results(
 command_id text PRIMARY KEY REFERENCES mock_commands(command_id),result_body bytea NOT NULL CHECK(octet_length(result_body) BETWEEN 1 AND 65536),result_digest text NOT NULL CHECK(result_digest ~ '^[0-9a-f]{64}$'),
 provider_order_revision bigint NOT NULL CHECK(provider_order_revision>0),completed_at timestamptz NOT NULL,CHECK(result_digest=mock_hash(result_body))
);
CREATE FUNCTION mock_transition_digest(order_ref text,rev bigint,kind text,command text,control text,occurred timestamptz) RETURNS text LANGUAGE sql IMMUTABLE SET search_path FROM CURRENT AS $$
 SELECT mock_hash(convert_to('mock-transition-v1'||E'\n'||order_ref||E'\n'||rev::text||E'\n'||kind||E'\n'||coalesce(command,'-')||E'\n'||coalesce(control,'-')||E'\n'||to_char(occurred AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'UTF8'))
$$;
CREATE TABLE mock_order_transitions(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(mock_uuid(id)),merchant_order_id text NOT NULL REFERENCES mock_orders(merchant_order_id),revision bigint NOT NULL CHECK(revision IN (1,2)),kind text NOT NULL CHECK(kind IN ('ready','captured','closed')),
 command_id text REFERENCES mock_commands(command_id),control_operation_id text REFERENCES mock_controls(operation_id),occurred_at timestamptz NOT NULL,fact_digest text NOT NULL CHECK(fact_digest ~ '^[0-9a-f]{64}$'),
 UNIQUE(merchant_order_id,revision),CHECK((command_id IS NULL)<>(control_operation_id IS NULL)),CHECK(fact_digest=mock_transition_digest(merchant_order_id,revision,kind,command_id,control_operation_id,occurred_at))
);
CREATE TABLE mock_captures(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(mock_uuid(id)),merchant_order_id text NOT NULL UNIQUE REFERENCES mock_orders(merchant_order_id),provider_transaction_id text NOT NULL UNIQUE CHECK(mock_ref(provider_transaction_id)),amount bigint NOT NULL CHECK(amount>0),
 currency text NOT NULL CHECK(currency='CNY'),occurred_at timestamptz NOT NULL,provider_order_revision bigint NOT NULL CHECK(provider_order_revision=2),control_operation_id text NOT NULL UNIQUE REFERENCES mock_controls(operation_id),created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE mock_queries(
 query_id text PRIMARY KEY CHECK(mock_uuid(query_id)),command_id text NOT NULL CHECK(mock_uuid(command_id)),merchant_order_id text NOT NULL CHECK(mock_merchant_order(merchant_order_id)),command_kind text NOT NULL CHECK(command_kind IN ('checkout','cancel')),
 expected_parameter_digest text NOT NULL CHECK(expected_parameter_digest ~ '^[0-9a-f]{64}$'),request_digest text NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),canonical_parameters bytea NOT NULL CHECK(octet_length(canonical_parameters) BETWEEN 1 AND 8192),
 snapshot_body bytea NOT NULL CHECK(octet_length(snapshot_body) BETWEEN 1 AND 65536),snapshot_digest text NOT NULL CHECK(snapshot_digest ~ '^[0-9a-f]{64}$'),observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(request_digest=mock_hash(canonical_parameters)),CHECK(snapshot_digest=mock_hash(snapshot_body))
);
CREATE TABLE mock_notification_events(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(mock_uuid(id)),event_id text NOT NULL UNIQUE CHECK(mock_ref(event_id)),merchant_order_id text NOT NULL CHECK(mock_merchant_order(merchant_order_id)),
 event_type text NOT NULL CHECK(event_type IN ('capture.confirmed','checkout.failed','refund.confirmed','refund.failed','dispute.opened','chargeback.debited','chargeback.reversed')),
 canonical_body bytea NOT NULL CHECK(octet_length(canonical_body) BETWEEN 1 AND 65536),business_digest text NOT NULL CHECK(business_digest ~ '^[0-9a-f]{64}$'),control_operation_id text NOT NULL REFERENCES mock_controls(operation_id),
 canonical_capture_id text UNIQUE REFERENCES mock_captures(id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),CHECK(business_digest=mock_hash(canonical_body))
);
CREATE TABLE mock_notification_delivery(
 event_id text PRIMARY KEY REFERENCES mock_notification_events(id),state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','retry_wait','leased','delivered','review_required')),
 generation bigint NOT NULL DEFAULT 0 CHECK(generation BETWEEN 0 AND 20),attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 20),next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),lease_until timestamptz,last_error_code text,
 delivered_at timestamptz,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),CHECK(generation=attempt_count),CHECK((state='leased')=(lease_until IS NOT NULL)),CHECK((state='delivered')=(delivered_at IS NOT NULL)),
 CHECK(last_error_code IS NULL OR last_error_code IN ('network','timeout','auth','protocol','environment','temporary','permanent','attempts_exhausted')),
 CHECK(state<>'review_required' OR (attempt_count=20 AND last_error_code='attempts_exhausted'))
);
CREATE TABLE mock_delivery_attempts(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(mock_uuid(id)),event_id text NOT NULL REFERENCES mock_notification_events(id),generation bigint NOT NULL CHECK(generation BETWEEN 1 AND 20),attempt_no integer NOT NULL CHECK(attempt_no BETWEEN 1 AND 20),
 delivery_id text NOT NULL UNIQUE CHECK(mock_uuid(delivery_id)),delivery_time bigint NOT NULL CHECK(delivery_time BETWEEN 0 AND 253402300799),signing_key_id text NOT NULL REFERENCES mock_signing_keys(key_id),
 body_digest text NOT NULL CHECK(body_digest ~ '^[0-9a-f]{64}$'),claimed_at timestamptz NOT NULL,lease_until timestamptz NOT NULL,UNIQUE(event_id,generation),UNIQUE(event_id,attempt_no),CHECK(generation=attempt_no),CHECK(lease_until=claimed_at+interval '60 seconds')
);
-- Immutable IO result evidence. A process crash may leave a claim without an observation.
CREATE TABLE mock_delivery_observations(
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(mock_uuid(id)),event_id text NOT NULL,generation bigint NOT NULL,
 outcome text NOT NULL CHECK(outcome IN ('ack','failed')),http_status integer CHECK(http_status BETWEEN 100 AND 599),error_code text,
 receipt_data bytea CHECK(octet_length(receipt_data) BETWEEN 1 AND 8192),observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(event_id,generation),FOREIGN KEY(event_id,generation) REFERENCES mock_delivery_attempts(event_id,generation),
 CHECK(((outcome='ack' AND http_status IN (200,202) AND error_code IS NULL AND receipt_data IS NOT NULL) OR
 (outcome='failed' AND error_code IS NOT NULL AND error_code IN ('network','timeout','auth','protocol','environment','temporary','permanent') AND receipt_data IS NULL)) IS TRUE)
);
CREATE INDEX mock_delivery_due ON mock_notification_delivery(next_attempt_at,event_id) WHERE state IN ('pending','retry_wait');
CREATE INDEX mock_delivery_expired ON mock_notification_delivery(lease_until,event_id) WHERE state='leased';

CREATE FUNCTION mock_result_valid(j jsonb,kind text,revision text) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
DECLARE c jsonb;
BEGIN
 IF NOT mock_fields(j,ARRAY['status','checkout_ref','failure_code','capture','closed_at']) OR NOT mock_strings(j,ARRAY['status']) OR NOT coalesce(mock_positive(revision),false) THEN RETURN false;END IF;
 IF kind='cancel' AND j->>'status' NOT IN ('closed','captured') THEN RETURN false;END IF;
 CASE j->>'status'
 WHEN 'ready' THEN RETURN mock_strings(j,ARRAY['checkout_ref']) AND j->>'checkout_ref' ~ '^checkout:[0-9a-f-]{36}$' AND mock_uuid(substr(j->>'checkout_ref',10)) AND j->'failure_code'='null'::jsonb AND j->'capture'='null'::jsonb AND j->'closed_at'='null'::jsonb;
 WHEN 'rejected' THEN RETURN mock_strings(j,ARRAY['failure_code']) AND j->'checkout_ref'='null'::jsonb AND j->>'failure_code' IN ('checkout_rejected','order_closed','order_expired') AND j->'capture'='null'::jsonb AND j->'closed_at'='null'::jsonb;
 WHEN 'closed' THEN RETURN j->'checkout_ref'='null'::jsonb AND j->'failure_code'='null'::jsonb AND j->'capture'='null'::jsonb AND coalesce(mock_time(j->>'closed_at'),false);
 WHEN 'captured' THEN
  c:=j->'capture';RETURN j->'checkout_ref'='null'::jsonb AND j->'failure_code'='null'::jsonb AND j->'closed_at'='null'::jsonb
   AND mock_fields(c,ARRAY['provider_transaction_id','amount','currency','occurred_at','provider_order_revision']) AND mock_strings(c,ARRAY['provider_transaction_id','amount','currency','occurred_at','provider_order_revision']) AND mock_ref(c->>'provider_transaction_id') AND mock_positive(c->>'amount') AND c->>'currency'='CNY' AND mock_time(c->>'occurred_at') AND c->>'provider_order_revision'=revision;
 ELSE RETURN false;END CASE;
END $$;
CREATE FUNCTION mock_reply_valid(j jsonb,is_query boolean) RETURNS boolean LANGUAGE plpgsql STABLE SET search_path FROM CURRENT AS $$
DECLARE expected text[]; e mock_environment%ROWTYPE;
BEGIN
 expected:=ARRAY['schema_version','environment_id','merchant_id','merchant_order_id','command_id','command_kind','parameter_digest','command_state','provider_order_revision','result'];
 IF is_query THEN expected:=expected||ARRAY['query_id'];END IF;
 SELECT * INTO e FROM mock_environment WHERE singleton;
 IF e.environment_id IS NULL OR NOT mock_fields(j,expected) OR NOT mock_strings(j,ARRAY['environment_id','merchant_id','merchant_order_id','command_id','command_kind','parameter_digest','command_state']) OR (is_query AND NOT mock_strings(j,ARRAY['query_id'])) OR j->'schema_version'<>'1'::jsonb OR j->>'environment_id'<>e.environment_id OR j->>'merchant_id'<>e.merchant_id
  OR NOT mock_merchant_order(j->>'merchant_order_id') OR substr(j->>'merchant_order_id',6,36)<>e.environment_id OR NOT mock_uuid(j->>'command_id') OR j->>'command_kind' NOT IN ('checkout','cancel') OR j->>'parameter_digest' !~ '^[0-9a-f]{64}$'
  OR (is_query AND NOT mock_uuid(j->>'query_id')) THEN RETURN false;END IF;
 IF j->>'command_state'='in_progress' OR (is_query AND j->>'command_state'='not_seen') THEN RETURN j->'result'='null'::jsonb AND j->'provider_order_revision'='null'::jsonb;END IF;
 RETURN j->>'command_state'='completed' AND mock_strings(j,ARRAY['provider_order_revision']) AND coalesce(mock_result_valid(j->'result',j->>'command_kind',j->>'provider_order_revision'),false);
END $$;
CREATE FUNCTION mock_command_matches(cid text,body bytea,final boolean) RETURNS boolean LANGUAGE sql STABLE SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM mock_commands c CROSS JOIN LATERAL (SELECT mock_json(body) j) x
 WHERE c.command_id=cid AND mock_reply_valid(x.j,false) AND x.j->>'command_id'=c.command_id AND x.j->>'merchant_order_id'=c.merchant_order_id AND x.j->>'command_kind'=c.kind AND x.j->>'parameter_digest'=c.parameter_digest
 AND (NOT final OR x.j->>'command_state'='completed'))
$$;
CREATE FUNCTION mock_result_effect(cid text,body bytea,revision bigint) RETURNS boolean LANGUAGE sql STABLE SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM mock_commands c JOIN mock_orders o USING(merchant_order_id)
 CROSS JOIN LATERAL (SELECT mock_json(body)->'result' r,mock_json(c.canonical_parameters) p) x
 WHERE c.command_id=cid AND CASE x.r->>'status'
 WHEN 'ready' THEN c.kind='checkout' AND revision=1 AND o.amount=(x.p->>'amount')::bigint AND o.currency=x.p->>'currency' AND o.created_at=(x.p->>'created_at')::timestamptz AND o.expires_at=(x.p->>'expires_at')::timestamptz AND o.checkout_ref=x.r->>'checkout_ref'
  AND EXISTS(SELECT 1 FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id AND t.revision=1 AND t.kind='ready' AND t.command_id=c.command_id)
 WHEN 'closed' THEN c.kind='cancel' AND EXISTS(SELECT 1 FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id AND t.revision=revision AND t.kind='closed' AND t.occurred_at=(x.r->>'closed_at')::timestamptz)
 WHEN 'captured' THEN EXISTS(SELECT 1 FROM mock_captures a WHERE a.merchant_order_id=o.merchant_order_id AND a.provider_order_revision=revision AND a.provider_transaction_id=x.r->'capture'->>'provider_transaction_id' AND a.amount=(x.r->'capture'->>'amount')::bigint AND a.currency=x.r->'capture'->>'currency' AND a.occurred_at=(x.r->'capture'->>'occurred_at')::timestamptz)
 WHEN 'rejected' THEN c.kind='checkout' AND EXISTS(SELECT 1 FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id AND t.revision=revision AND
  ((x.r->>'failure_code'='order_closed' AND t.kind='closed') OR (x.r->>'failure_code'='checkout_rejected' AND t.kind IN ('ready','captured')) OR
   (x.r->>'failure_code'='order_expired' AND t.kind='closed' AND t.command_id=c.command_id AND o.amount=(x.p->>'amount')::bigint AND o.created_at=(x.p->>'created_at')::timestamptz AND o.expires_at=(x.p->>'expires_at')::timestamptz)))
 ELSE false END)
$$;
CREATE FUNCTION mock_command_complete(cid text) RETURNS boolean LANGUAGE sql STABLE SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM mock_commands c JOIN mock_command_processing p USING(command_id) LEFT JOIN mock_command_results r USING(command_id)
 WHERE c.command_id=cid AND mock_command_matches(c.command_id,c.initial_reply,false)
 AND ((p.state='held' AND r.command_id IS NULL AND p.held_by_control_id IS NOT NULL AND mock_json(c.initial_reply)->>'command_state'='in_progress') OR
 (p.state='completed' AND r.command_id IS NOT NULL AND mock_command_matches(c.command_id,r.result_body,true) AND mock_json(r.result_body)->>'provider_order_revision'=r.provider_order_revision::text AND r.completed_at=p.completed_at AND mock_result_effect(cid,r.result_body,r.provider_order_revision)
 AND ((mock_json(c.initial_reply)->>'command_state'='completed' AND c.initial_reply=r.result_body AND p.held_by_control_id IS NULL AND p.released_by_control_id IS NULL) OR
 (mock_json(c.initial_reply)->>'command_state'='in_progress' AND p.held_by_control_id IS NOT NULL AND p.released_by_control_id IS NOT NULL)))))
$$;
CREATE FUNCTION mock_order_complete(oid text) RETURNS boolean LANGUAGE sql STABLE SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM mock_orders o WHERE o.merchant_order_id=oid AND
 (SELECT count(*) FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id)=o.revision AND
 EXISTS(SELECT 1 FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id AND t.revision=1 AND t.kind IN ('ready','closed')) AND
 EXISTS(SELECT 1 FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id AND t.revision=o.revision AND t.kind=o.state AND (o.state<>'closed' OR t.occurred_at=o.closed_at)) AND
 NOT EXISTS(SELECT 1 FROM mock_order_transitions t WHERE t.merchant_order_id=o.merchant_order_id AND (t.revision>o.revision OR
  (t.kind='ready' AND (t.revision<>1 OR NOT EXISTS(SELECT 1 FROM mock_commands c JOIN mock_command_results r USING(command_id) WHERE c.command_id=t.command_id AND c.kind='checkout' AND c.merchant_order_id=o.merchant_order_id AND mock_json(r.result_body)->'result'->>'status'='ready' AND r.provider_order_revision=t.revision))) OR
  (t.kind='closed' AND NOT EXISTS(SELECT 1 FROM mock_commands c JOIN mock_command_results r USING(command_id) WHERE c.command_id=t.command_id AND c.merchant_order_id=o.merchant_order_id AND r.provider_order_revision=t.revision AND
   ((c.kind='cancel' AND mock_json(r.result_body)->'result'->>'status'='closed') OR (c.kind='checkout' AND mock_json(r.result_body)->'result'->>'failure_code'='order_expired')))) OR
  (t.kind='captured' AND NOT EXISTS(SELECT 1 FROM mock_captures c WHERE c.merchant_order_id=o.merchant_order_id AND c.provider_order_revision=t.revision AND c.control_operation_id=t.control_operation_id AND c.occurred_at=t.occurred_at)))) AND
 ((o.state='captured' AND EXISTS(SELECT 1 FROM mock_captures c WHERE c.merchant_order_id=o.merchant_order_id AND c.amount=o.amount AND c.currency=o.currency AND c.provider_order_revision=o.revision AND c.occurred_at>=o.created_at AND c.occurred_at<o.expires_at)) OR
 (o.state<>'captured' AND NOT EXISTS(SELECT 1 FROM mock_captures c WHERE c.merchant_order_id=o.merchant_order_id))))
$$;
CREATE FUNCTION mock_capture_complete(cid text) RETURNS boolean LANGUAGE sql STABLE SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM mock_captures c JOIN mock_controls q ON q.operation_id=c.control_operation_id JOIN mock_notification_events n ON n.canonical_capture_id=c.id
 WHERE c.id=cid AND q.action='capture_order' AND q.scenario='normal' AND q.target_reference=c.merchant_order_id AND mock_json(q.result_body)->>'status'='accepted' AND mock_json(q.result_body)->>'resource_id'=c.id
 AND n.control_operation_id=q.operation_id AND n.merchant_order_id=c.merchant_order_id AND n.event_type='capture.confirmed'
 AND mock_json(n.canonical_body)->>'provider_transaction_id'=c.provider_transaction_id AND mock_json(n.canonical_body)->>'amount'=c.amount::text AND mock_json(n.canonical_body)->>'currency'=c.currency
 AND (mock_json(n.canonical_body)->>'occurred_at')::timestamptz=c.occurred_at AND mock_json(n.canonical_body)->>'provider_order_revision'=c.provider_order_revision::text AND mock_order_complete(c.merchant_order_id))
$$;
CREATE FUNCTION mock_delivery_complete(eid text) RETURNS boolean LANGUAGE sql STABLE SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM mock_notification_delivery d WHERE d.event_id=eid AND
 (SELECT count(*) FROM mock_delivery_attempts a WHERE a.event_id=eid)=d.attempt_count AND
 (d.attempt_count=0 OR EXISTS(SELECT 1 FROM mock_delivery_attempts a WHERE a.event_id=eid AND a.generation=d.generation AND a.attempt_no=d.attempt_count AND (d.state<>'leased' OR a.lease_until=d.lease_until))) AND
 (d.state<>'delivered' OR EXISTS(SELECT 1 FROM mock_delivery_observations x WHERE x.event_id=eid AND x.generation=d.generation AND x.outcome='ack')) AND
 (d.state<>'retry_wait' OR EXISTS(SELECT 1 FROM mock_delivery_observations x WHERE x.event_id=eid AND x.generation=d.generation AND x.outcome='failed' AND x.error_code=d.last_error_code)) AND
 (d.state<>'review_required' OR EXISTS(SELECT 1 FROM mock_delivery_attempts a WHERE a.event_id=eid AND a.generation=d.generation AND
  (a.lease_until<=clock_timestamp() OR EXISTS(SELECT 1 FROM mock_delivery_observations x WHERE x.event_id=eid AND x.generation=d.generation AND x.outcome='failed')))))
$$;
CREATE FUNCTION mock_control_complete(cid text) RETURNS boolean LANGUAGE plpgsql STABLE SET search_path FROM CURRENT AS $$
DECLARE c mock_controls%ROWTYPE;j jsonb; resource text;
BEGIN
 SELECT * INTO c FROM mock_controls WHERE operation_id=cid;IF c.operation_id IS NULL THEN RETURN false;END IF;j:=mock_json(c.result_body);resource:=j->>'resource_id';
 IF NOT mock_fields(j,ARRAY['operation_id','status','reason','resource_id']) OR NOT mock_strings(j,ARRAY['operation_id','status','reason']) OR j->>'operation_id'<>c.operation_id THEN RETURN false;END IF;
 IF j->>'status'='rejected' THEN RETURN j->>'reason' IN ('state_conflict','target_not_found') AND j->'resource_id'='null'::jsonb AND
  NOT EXISTS(SELECT 1 FROM mock_captures WHERE control_operation_id=cid) AND NOT EXISTS(SELECT 1 FROM mock_notification_events WHERE control_operation_id=cid) AND NOT EXISTS(SELECT 1 FROM mock_fault_plans WHERE control_operation_id=cid) AND NOT EXISTS(SELECT 1 FROM mock_command_processing WHERE released_by_control_id=cid);END IF;
 IF j->>'status'<>'accepted' OR j->>'reason'<>'accepted' OR NOT coalesce(mock_uuid(resource),false) THEN RETURN false;END IF;
 CASE c.action
 WHEN 'capture_order' THEN RETURN EXISTS(SELECT 1 FROM mock_captures x WHERE x.id=resource AND x.merchant_order_id=c.target_reference);
 WHEN 'emit_test_event' THEN RETURN EXISTS(SELECT 1 FROM mock_notification_events x WHERE x.id=resource AND x.control_operation_id=cid AND x.merchant_order_id=c.target_reference AND x.canonical_capture_id IS NULL);
 WHEN 'release_command' THEN RETURN resource=c.target_reference AND EXISTS(SELECT 1 FROM mock_command_processing x WHERE x.command_id=resource AND x.state='completed' AND x.released_by_control_id=cid);
 WHEN 'arm_fault' THEN RETURN resource=cid AND EXISTS(SELECT 1 FROM mock_fault_plans x WHERE x.control_operation_id=cid AND x.boundary=c.scenario AND x.target_id=c.target_reference);
 ELSE RETURN false;END CASE;
END $$;

CREATE FUNCTION mock_order_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable mock order';END IF;
 IF TG_OP='INSERT' THEN IF NEW.state NOT IN ('ready','closed') THEN RAISE EXCEPTION 'mock order initial state';END IF;NEW.revision:=1;RETURN NEW;END IF;
 IF (NEW.merchant_order_id,NEW.order_id,NEW.environment_id,NEW.merchant_id,NEW.amount,NEW.currency,NEW.created_at,NEW.expires_at,NEW.checkout_ref,NEW.created_locally_at) IS DISTINCT FROM (OLD.merchant_order_id,OLD.order_id,OLD.environment_id,OLD.merchant_id,OLD.amount,OLD.currency,OLD.created_at,OLD.expires_at,OLD.checkout_ref,OLD.created_locally_at) THEN RAISE EXCEPTION 'immutable mock order terms';END IF;
 IF NEW.state=OLD.state THEN IF NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN RAISE EXCEPTION 'immutable mock close';END IF;NEW.revision:=OLD.revision;RETURN NEW;END IF;
 IF OLD.state<>'ready' OR NEW.state NOT IN ('closed','captured') THEN RAISE EXCEPTION 'mock order transition';END IF;NEW.revision:=OLD.revision+1;RETURN NEW;
END $$;
CREATE TRIGGER mock_order_guard BEFORE INSERT OR UPDATE OR DELETE ON mock_orders FOR EACH ROW EXECUTE FUNCTION mock_order_guard();
CREATE FUNCTION mock_processing_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable mock processing';END IF;
 IF TG_OP='INSERT' THEN RETURN NEW;END IF;
 IF OLD.state<>'held' OR NEW.state<>'completed' OR (NEW.command_id,NEW.held_by_control_id,NEW.created_at) IS DISTINCT FROM (OLD.command_id,OLD.held_by_control_id,OLD.created_at) OR NEW.released_by_control_id IS NULL THEN RAISE EXCEPTION 'mock processing transition';END IF;RETURN NEW;
END $$;
CREATE TRIGGER mock_processing_guard BEFORE INSERT OR UPDATE OR DELETE ON mock_command_processing FOR EACH ROW EXECUTE FUNCTION mock_processing_guard();
CREATE FUNCTION mock_fault_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable mock fault';END IF;
 IF TG_OP='INSERT' THEN IF NEW.remaining<>1 OR NEW.consumed_at IS NOT NULL THEN RAISE EXCEPTION 'mock fault initial state';END IF;RETURN NEW;END IF;
 IF (NEW.control_operation_id,NEW.boundary,NEW.target_kind,NEW.target_id,NEW.created_at) IS DISTINCT FROM (OLD.control_operation_id,OLD.boundary,OLD.target_kind,OLD.target_id,OLD.created_at) OR OLD.remaining<>1 OR NEW.remaining<>0 OR NEW.consumed_at IS NULL THEN RAISE EXCEPTION 'mock fault transition';END IF;RETURN NEW;
END $$;
CREATE TRIGGER mock_fault_guard BEFORE INSERT OR UPDATE OR DELETE ON mock_fault_plans FOR EACH ROW EXECUTE FUNCTION mock_fault_guard();
CREATE FUNCTION mock_delivery_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE at_time timestamptz:=clock_timestamp();delay_seconds integer;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable mock delivery';END IF;
 IF TG_OP='INSERT' THEN IF NEW.state<>'pending' OR NEW.generation<>0 OR NEW.attempt_count<>0 OR NEW.lease_until IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.delivered_at IS NOT NULL THEN RAISE EXCEPTION 'mock delivery initial state';END IF;RETURN NEW;END IF;
 IF (NEW.event_id,NEW.created_at) IS DISTINCT FROM (OLD.event_id,OLD.created_at) OR OLD.state IN ('delivered','review_required') THEN RAISE EXCEPTION 'mock delivery immutable terminal';END IF;
 IF NEW.state='leased' AND NEW.generation=OLD.generation+1 AND NEW.attempt_count=OLD.attempt_count+1 AND OLD.attempt_count<20 AND
  ((OLD.state IN ('pending','retry_wait') AND OLD.next_attempt_at<=at_time) OR (OLD.state='leased' AND OLD.lease_until<=at_time)) THEN
  NEW.lease_until:=at_time+interval '60 seconds';NEW.last_error_code:=NULL;NEW.delivered_at:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;RETURN NEW;END IF;
 IF OLD.state<>'leased' OR NEW.generation<>OLD.generation OR NEW.attempt_count<>OLD.attempt_count THEN RAISE EXCEPTION 'mock delivery stale transition';END IF;
 NEW.lease_until:=NULL;
 IF NEW.state='delivered' AND at_time<OLD.lease_until THEN NEW.delivered_at:=at_time;NEW.last_error_code:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;RETURN NEW;END IF;
 IF NEW.state='retry_wait' AND at_time<OLD.lease_until AND OLD.attempt_count<20 AND NEW.last_error_code IN ('network','timeout','auth','protocol','environment','temporary','permanent') THEN
  delay_seconds:=CASE OLD.attempt_count WHEN 1 THEN 1 WHEN 2 THEN 5 WHEN 3 THEN 30 WHEN 4 THEN 120 ELSE 300 END;
  NEW.next_attempt_at:=at_time+make_interval(secs=>delay_seconds);NEW.delivered_at:=NULL;RETURN NEW;END IF;
 IF NEW.state='review_required' AND OLD.attempt_count=20 AND NEW.last_error_code='attempts_exhausted' AND
  (OLD.lease_until<=at_time OR EXISTS(SELECT 1 FROM mock_delivery_observations x WHERE x.event_id=OLD.event_id AND x.generation=OLD.generation AND x.outcome='failed')) THEN
  NEW.delivered_at:=NULL;NEW.next_attempt_at:=OLD.next_attempt_at;RETURN NEW;END IF;
 RAISE EXCEPTION 'mock delivery transition';
END $$;
CREATE TRIGGER mock_delivery_guard BEFORE INSERT OR UPDATE OR DELETE ON mock_notification_delivery FOR EACH ROW EXECUTE FUNCTION mock_delivery_guard();

CREATE FUNCTION mock_input_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE j jsonb;e mock_environment%ROWTYPE;required text[];c mock_commands%ROWTYPE;processing mock_command_processing%ROWTYPE;r mock_command_results%ROWTYPE;o mock_orders%ROWTYPE;cap mock_captures%ROWTYPE;d mock_notification_delivery%ROWTYPE;k mock_signing_keys%ROWTYPE;n mock_notification_events%ROWTYPE;
BEGIN
 SELECT * INTO e FROM mock_environment WHERE singleton;IF e.environment_id IS NULL THEN RAISE EXCEPTION 'mock environment missing';END IF;
 IF TG_TABLE_NAME='mock_commands' THEN
  j:=mock_json(NEW.canonical_parameters);required:=ARRAY['schema_version','environment_id','merchant_id','command_id','merchant_order_id'];IF NEW.kind='checkout' THEN required:=required||ARRAY['amount','currency','created_at','expires_at'];END IF;
  IF NOT mock_fields(j,required) OR NOT mock_strings(j,array_remove(required,'schema_version')) OR j->'schema_version'<>'1'::jsonb OR j->>'environment_id'<>e.environment_id OR j->>'merchant_id'<>e.merchant_id OR j->>'command_id'<>NEW.command_id OR j->>'merchant_order_id'<>NEW.merchant_order_id OR substr(NEW.merchant_order_id,6,36)<>e.environment_id THEN RAISE EXCEPTION 'mock command binding';END IF;
  IF NEW.kind='checkout' AND (NOT coalesce(mock_positive(j->>'amount'),false) OR j->>'currency'<>'CNY' OR NOT coalesce(mock_time(j->>'created_at'),false) OR NOT coalesce(mock_time(j->>'expires_at'),false) OR (j->>'expires_at')::timestamptz<>(j->>'created_at')::timestamptz+interval '900 seconds') THEN RAISE EXCEPTION 'mock checkout terms';END IF;
 ELSIF TG_TABLE_NAME='mock_queries' THEN
  j:=mock_json(NEW.canonical_parameters);IF NOT mock_fields(j,ARRAY['schema_version','environment_id','merchant_id','query_id','command_id','merchant_order_id','command_kind','parameter_digest']) OR NOT mock_strings(j,ARRAY['environment_id','merchant_id','query_id','command_id','merchant_order_id','command_kind','parameter_digest']) OR j->'schema_version'<>'1'::jsonb OR j->>'environment_id'<>e.environment_id OR j->>'merchant_id'<>e.merchant_id OR j->>'query_id'<>NEW.query_id OR j->>'command_id'<>NEW.command_id OR j->>'merchant_order_id'<>NEW.merchant_order_id OR j->>'command_kind'<>NEW.command_kind OR j->>'parameter_digest'<>NEW.expected_parameter_digest THEN RAISE EXCEPTION 'mock query binding';END IF;
  j:=mock_json(NEW.snapshot_body);IF NOT coalesce(mock_reply_valid(j,true),false) OR j->>'query_id'<>NEW.query_id OR j->>'command_id'<>NEW.command_id OR j->>'merchant_order_id'<>NEW.merchant_order_id OR j->>'command_kind'<>NEW.command_kind OR j->>'parameter_digest'<>NEW.expected_parameter_digest THEN RAISE EXCEPTION 'mock query snapshot binding';END IF;
  SELECT * INTO c FROM mock_commands WHERE command_id=NEW.command_id;
  IF c.command_id IS NULL THEN IF j->>'command_state'<>'not_seen' THEN RAISE EXCEPTION 'mock unseen query state';END IF;RETURN NEW;END IF;
  IF (c.merchant_order_id,c.kind,c.parameter_digest) IS DISTINCT FROM (NEW.merchant_order_id,NEW.command_kind,NEW.expected_parameter_digest) THEN RAISE EXCEPTION 'mock query parameter conflict';END IF;
  SELECT * INTO processing FROM mock_command_processing WHERE command_id=c.command_id;
  IF processing.state='held' THEN IF j->>'command_state'<>'in_progress' THEN RAISE EXCEPTION 'mock held query state';END IF;RETURN NEW;END IF;
  SELECT * INTO r FROM mock_command_results WHERE command_id=c.command_id;SELECT * INTO o FROM mock_orders WHERE merchant_order_id=c.merchant_order_id;
  IF r.command_id IS NULL OR j->>'command_state'<>'completed' THEN RAISE EXCEPTION 'mock completed query state';END IF;
  IF o.state='captured' THEN SELECT * INTO cap FROM mock_captures WHERE merchant_order_id=o.merchant_order_id;
   IF j->'result'->>'status'<>'captured' OR j->>'provider_order_revision'<>o.revision::text OR j->'result'->'capture'->>'provider_transaction_id'<>cap.provider_transaction_id OR j->'result'->'capture'->>'amount'<>cap.amount::text OR (j->'result'->'capture'->>'occurred_at')::timestamptz<>cap.occurred_at THEN RAISE EXCEPTION 'mock query capture snapshot';END IF;
  ELSIF o.state='closed' THEN IF j->'result'->>'status'<>'closed' OR j->>'provider_order_revision'<>o.revision::text OR (j->'result'->>'closed_at')::timestamptz<>o.closed_at THEN RAISE EXCEPTION 'mock query closed snapshot';END IF;
  ELSE IF j->'result'<>mock_json(r.result_body)->'result' OR j->>'provider_order_revision'<>r.provider_order_revision::text THEN RAISE EXCEPTION 'mock query final snapshot';END IF;END IF;
 ELSIF TG_TABLE_NAME='mock_notification_events' THEN
  j:=mock_json(NEW.canonical_body);required:=ARRAY['schema_version','event_id','event_type','environment_id','merchant_id','order_id','provider_transaction_id','amount','currency','occurred_at','provider_order_revision'];
  CASE NEW.event_type WHEN 'checkout.failed' THEN required:=required||ARRAY['failure_code'];WHEN 'refund.confirmed','refund.failed' THEN required:=required||ARRAY['refund_id'];WHEN 'dispute.opened','chargeback.debited','chargeback.reversed' THEN required:=required||ARRAY['case_id'];ELSE NULL;END CASE;
  IF NOT mock_fields(j,required) OR NOT mock_strings(j,array_remove(array_remove(required,'schema_version'),'provider_transaction_id')) OR j->'schema_version'<>'1'::jsonb OR j->>'environment_id'<>e.environment_id OR j->>'merchant_id'<>e.merchant_id OR j->>'event_id'<>NEW.event_id OR j->>'event_type'<>NEW.event_type OR NEW.merchant_order_id<>'mps1.'||e.environment_id||'.'||(j->>'order_id') OR NOT coalesce(mock_positive(j->>'amount'),false) OR j->>'currency'<>'CNY' OR NOT coalesce(mock_time(j->>'occurred_at'),false) OR NOT coalesce(mock_positive(j->>'provider_order_revision'),false) THEN RAISE EXCEPTION 'mock notification binding';END IF;
  IF (NEW.event_type='checkout.failed' AND (j->'provider_transaction_id'<>'null'::jsonb OR j->>'failure_code' NOT IN ('checkout_rejected','order_closed','order_expired'))) OR (NEW.event_type<>'checkout.failed' AND (NOT mock_strings(j,ARRAY['provider_transaction_id']) OR NOT coalesce(mock_ref(j->>'provider_transaction_id'),false))) OR (NEW.event_type IN ('refund.confirmed','refund.failed') AND NOT coalesce(mock_uuid(j->>'refund_id'),false)) OR (NEW.event_type IN ('dispute.opened','chargeback.debited','chargeback.reversed') AND NOT coalesce(mock_ref(j->>'case_id'),false)) THEN RAISE EXCEPTION 'mock notification union';END IF;
 ELSIF TG_TABLE_NAME='mock_delivery_attempts' THEN
  SELECT * INTO d FROM mock_notification_delivery WHERE event_id=NEW.event_id;SELECT * INTO n FROM mock_notification_events WHERE id=NEW.event_id;SELECT * INTO k FROM mock_signing_keys WHERE key_id=NEW.signing_key_id;
  IF d.event_id IS NULL OR n.id IS NULL OR k.key_id IS NULL OR d.state<>'leased' OR (d.generation,d.attempt_count,d.lease_until) IS DISTINCT FROM (NEW.generation,NEW.attempt_no,NEW.lease_until) OR NEW.body_digest<>n.business_digest OR NEW.delivery_time<>floor(extract(epoch FROM NEW.claimed_at))::bigint OR to_timestamp(NEW.delivery_time)<k.not_before OR to_timestamp(NEW.delivery_time)>k.not_after THEN RAISE EXCEPTION 'mock delivery attempt binding';END IF;
 ELSIF TG_TABLE_NAME='mock_delivery_observations' THEN
  SELECT * INTO n FROM mock_notification_events WHERE id=NEW.event_id;
  IF n.id IS NULL THEN RAISE EXCEPTION 'mock observation event missing';END IF;
  NEW.observed_at:=clock_timestamp();
  IF NEW.outcome='ack' THEN
   j:=mock_json(NEW.receipt_data);
   IF NOT mock_fields(j,ARRAY['receipt_id','event_id','status','environment','received_at']) OR NOT mock_strings(j,ARRAY['receipt_id','event_id','status','environment','received_at']) OR NOT coalesce(mock_uuid(j->>'receipt_id'),false) OR j->>'event_id'<>n.event_id OR j->>'status'<>'received' OR j->>'environment'<>'sandbox' OR NOT coalesce(mock_time(j->>'received_at'),false) OR (j->>'received_at')::timestamptz>NEW.observed_at+interval '5 seconds' THEN RAISE EXCEPTION 'mock ACK receipt binding';END IF;
  END IF;
 ELSIF TG_TABLE_NAME='mock_controls' THEN
  j:=mock_json(NEW.canonical_parameters);IF NOT mock_fields(j,ARRAY['operation_id','action','scenario','merchant_order_id','command_id','query_id','event_id']) OR NOT mock_strings(j,ARRAY['operation_id','action','scenario']) OR j->>'operation_id'<>NEW.operation_id OR j->>'action'<>NEW.action OR j->>'scenario'<>NEW.scenario THEN RAISE EXCEPTION 'mock control binding';END IF;
  IF NEW.action IN ('capture_order','emit_test_event') THEN IF NOT mock_strings(j,ARRAY['merchant_order_id']) OR j->>'merchant_order_id'<>NEW.target_reference OR NOT mock_merchant_order(NEW.target_reference) OR j->'command_id'<>'null'::jsonb OR j->'query_id'<>'null'::jsonb OR j->'event_id'<>'null'::jsonb THEN RAISE EXCEPTION 'mock control target';END IF;
  ELSIF NEW.action='release_command' THEN IF NOT mock_strings(j,ARRAY['command_id']) OR j->>'command_id'<>NEW.target_reference OR NOT mock_uuid(NEW.target_reference) OR j->'merchant_order_id'<>'null'::jsonb OR j->'query_id'<>'null'::jsonb OR j->'event_id'<>'null'::jsonb THEN RAISE EXCEPTION 'mock release target';END IF;
  ELSE IF j->'merchant_order_id'<>'null'::jsonb OR NOT coalesce(((j->>'command_id'=NEW.target_reference AND mock_uuid(NEW.target_reference) AND j->'query_id'='null'::jsonb AND j->'event_id'='null'::jsonb) OR (j->>'query_id'=NEW.target_reference AND mock_uuid(NEW.target_reference) AND j->'command_id'='null'::jsonb AND j->'event_id'='null'::jsonb) OR (j->>'event_id'=NEW.target_reference AND mock_ref(NEW.target_reference) AND j->'command_id'='null'::jsonb AND j->'query_id'='null'::jsonb)),false) THEN RAISE EXCEPTION 'mock fault target';END IF;END IF;
  IF NEW.action='arm_fault' AND ((NEW.scenario='after_query_snapshot_commit_drop_reply_once' AND NOT mock_strings(j,ARRAY['query_id'])) OR (NEW.scenario='before_callback_send_once' AND NOT mock_strings(j,ARRAY['event_id'])) OR (NEW.scenario NOT IN ('after_query_snapshot_commit_drop_reply_once','before_callback_send_once') AND NOT mock_strings(j,ARRAY['command_id']))) THEN RAISE EXCEPTION 'mock fault scenario target';END IF;
 END IF;RETURN NEW;
END $$;

CREATE FUNCTION mock_graph_guard() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE oid text;cid text;eid text;capid text;controlid text;p mock_command_processing%ROWTYPE;f mock_fault_plans%ROWTYPE;q mock_controls%ROWTYPE;
BEGIN
 CASE TG_TABLE_NAME
 WHEN 'mock_orders' THEN oid:=NEW.merchant_order_id;
 WHEN 'mock_order_transitions' THEN oid:=NEW.merchant_order_id;cid:=NEW.command_id;controlid:=NEW.control_operation_id;
 WHEN 'mock_captures' THEN oid:=NEW.merchant_order_id;capid:=NEW.id;controlid:=NEW.control_operation_id;
 WHEN 'mock_commands','mock_command_results' THEN cid:=NEW.command_id;
 WHEN 'mock_command_processing' THEN
  cid:=NEW.command_id;SELECT * INTO p FROM mock_command_processing WHERE command_id=cid;
  IF p.held_by_control_id IS NOT NULL THEN SELECT * INTO f FROM mock_fault_plans WHERE control_operation_id=p.held_by_control_id;
   IF f.target_kind<>'command' OR f.target_id<>cid OR f.boundary<>(CASE (SELECT kind FROM mock_commands WHERE command_id=cid) WHEN 'checkout' THEN 'hold_checkout_before_effect' ELSE 'hold_cancel_before_effect' END) OR f.remaining<>0 THEN RAISE EXCEPTION 'mock held command evidence';END IF;END IF;
  IF p.released_by_control_id IS NOT NULL THEN SELECT * INTO q FROM mock_controls WHERE operation_id=p.released_by_control_id;IF q.action<>'release_command' OR q.target_reference<>cid OR NOT mock_control_complete(q.operation_id) THEN RAISE EXCEPTION 'mock release evidence';END IF;END IF;
 WHEN 'mock_notification_events' THEN
  eid:=NEW.id;controlid:=NEW.control_operation_id;capid:=NEW.canonical_capture_id;SELECT * INTO q FROM mock_controls WHERE operation_id=controlid;
  IF (capid IS NULL AND q.action<>'emit_test_event') OR (capid IS NOT NULL AND q.action<>'capture_order') THEN RAISE EXCEPTION 'mock event control evidence';END IF;
 WHEN 'mock_notification_delivery','mock_delivery_attempts','mock_delivery_observations' THEN eid:=NEW.event_id;
 WHEN 'mock_controls' THEN controlid:=NEW.operation_id;
 WHEN 'mock_fault_plans' THEN controlid:=NEW.control_operation_id;
 END CASE;
 IF oid IS NOT NULL AND NOT coalesce(mock_order_complete(oid),false) THEN RAISE EXCEPTION 'mock order facts incomplete';END IF;
 IF cid IS NOT NULL AND NOT coalesce(mock_command_complete(cid),false) THEN RAISE EXCEPTION 'mock command facts incomplete';END IF;
 IF capid IS NOT NULL AND NOT coalesce(mock_capture_complete(capid),false) THEN RAISE EXCEPTION 'mock capture facts incomplete';END IF;
 IF eid IS NOT NULL AND NOT coalesce(mock_delivery_complete(eid),false) THEN RAISE EXCEPTION 'mock notification facts incomplete';END IF;
 IF controlid IS NOT NULL AND NOT coalesce(mock_control_complete(controlid),false) THEN RAISE EXCEPTION 'mock control facts incomplete';END IF;RETURN NULL;
END $$;

DO $$ DECLARE t text;BEGIN
 FOREACH t IN ARRAY ARRAY['mock_schema_migrations','mock_environment','mock_signing_keys','mock_commands','mock_command_results','mock_order_transitions','mock_captures','mock_queries','mock_notification_events','mock_delivery_attempts','mock_delivery_observations','mock_controls'] LOOP
  EXECUTE format('CREATE TRIGGER immutable_history BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION mock_immutable()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['mock_schema_migrations','mock_environment','mock_signing_keys'] LOOP EXECUTE format('CREATE TRIGGER owner_insert BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION mock_owner_insert()',t);END LOOP;
 FOREACH t IN ARRAY ARRAY['mock_commands','mock_queries','mock_notification_events','mock_delivery_attempts','mock_delivery_observations','mock_controls'] LOOP EXECUTE format('CREATE TRIGGER mock_input BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION mock_input_guard()',t);END LOOP;
 FOREACH t IN ARRAY ARRAY['mock_orders','mock_commands','mock_command_processing','mock_command_results','mock_order_transitions','mock_captures','mock_notification_events','mock_notification_delivery','mock_delivery_attempts','mock_delivery_observations','mock_controls','mock_fault_plans'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER mock_complete AFTER INSERT OR UPDATE ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mock_graph_guard()',t);
 END LOOP;
END $$;

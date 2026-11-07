-- C1-M mock refund extension. Apply only after the compatible mock schema
-- version and catalog have been selected by the sandbox owner.

CREATE FUNCTION mock_refund_digest(domain text, v bytea) RETURNS text
LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
  SELECT encode(sha256(int8send(octet_length(convert_to(domain, 'UTF8')))
    || convert_to(domain, 'UTF8') || int8send(octet_length(v)) || v), 'hex')
$$;
CREATE FUNCTION mock_refund_parameter_digest(v bytea) RETURNS text
LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
  SELECT mock_refund_digest('moni-aigate/payment-refund-command/v2', v)
$$;
CREATE FUNCTION mock_refund_fact_digest(v bytea) RETURNS text
LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS $$
  SELECT mock_refund_digest('moni-aigate/payment-refund-fact/v2', v)
$$;

CREATE TABLE mock_refund_captures (
  environment_id text NOT NULL CHECK (mock_uuid(environment_id)),
  merchant_id text NOT NULL CHECK (mock_ref(merchant_id)),
  merchant_order_id text NOT NULL CHECK (mock_merchant_order(merchant_order_id)),
  provider_transaction_id text NOT NULL CHECK (mock_ref(provider_transaction_id)),
  capture_digest text NOT NULL CHECK (capture_digest ~ '^[0-9a-f]{64}$'),
  original_amount bigint NOT NULL CHECK (original_amount > 0),
  confirmed_amount bigint NOT NULL DEFAULT 0 CHECK (confirmed_amount >= 0),
  final_no_effect_amount bigint NOT NULL DEFAULT 0 CHECK (final_no_effect_amount >= 0),
  refund_revision bigint NOT NULL DEFAULT 0 CHECK (refund_revision >= 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (environment_id, merchant_id, merchant_order_id),
  UNIQUE (environment_id, merchant_id, provider_transaction_id),
  CHECK (confirmed_amount + final_no_effect_amount <= original_amount)
);

CREATE TABLE mock_refund_commands (
  environment_id text NOT NULL CHECK (mock_uuid(environment_id)),
  merchant_id text NOT NULL CHECK (mock_ref(merchant_id)),
  command_id text NOT NULL CHECK (mock_uuid(command_id)),
  local_refund_id text NOT NULL CHECK (mock_uuid(local_refund_id)),
  merchant_order_id text NOT NULL CHECK (mock_merchant_order(merchant_order_id)),
  provider_transaction_id text NOT NULL CHECK (mock_ref(provider_transaction_id)),
  parameter_digest text NOT NULL CHECK (parameter_digest ~ '^[0-9a-f]{64}$'),
  canonical_parameters bytea NOT NULL CHECK (octet_length(canonical_parameters) BETWEEN 1 AND 8192),
  amount bigint NOT NULL CHECK (amount > 0),
  mutation_generation bigint NOT NULL CHECK (mutation_generation > 0),
  state text NOT NULL CHECK (state IN ('in_progress', 'completed')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  PRIMARY KEY (environment_id, merchant_id, command_id),
  UNIQUE (environment_id, merchant_id, local_refund_id),
  FOREIGN KEY (environment_id, merchant_id, merchant_order_id)
    REFERENCES mock_refund_captures(environment_id, merchant_id, merchant_order_id),
  CHECK (state = 'completed' OR completed_at IS NULL),
  CHECK (parameter_digest = mock_refund_parameter_digest(canonical_parameters))
);

CREATE TABLE mock_refund_facts (
  environment_id text NOT NULL CHECK (mock_uuid(environment_id)),
  merchant_id text NOT NULL CHECK (mock_ref(merchant_id)),
  fact_id text NOT NULL CHECK (mock_uuid(fact_id)),
  command_id text NOT NULL CHECK (mock_uuid(command_id)),
  local_refund_id text NOT NULL CHECK (mock_uuid(local_refund_id)),
  provider_transaction_id text NOT NULL CHECK (mock_ref(provider_transaction_id)),
  fact_digest text NOT NULL CHECK (fact_digest ~ '^[0-9a-f]{64}$'),
  outcome text NOT NULL CHECK (outcome IN ('confirmed', 'final_no_effect')),
  provider_refund_reference text,
  failure_code text,
  amount bigint NOT NULL CHECK (amount > 0),
  capture_refund_revision bigint NOT NULL CHECK (capture_refund_revision > 0),
  canonical_fact bytea NOT NULL CHECK (octet_length(canonical_fact) BETWEEN 1 AND 65536),
  completed_at timestamptz NOT NULL,
  PRIMARY KEY (environment_id, merchant_id, fact_id),
  UNIQUE (environment_id, merchant_id, command_id),
  UNIQUE (environment_id, merchant_id, local_refund_id),
  UNIQUE (environment_id, merchant_id, provider_refund_reference),
  FOREIGN KEY (environment_id, merchant_id, command_id)
    REFERENCES mock_refund_commands(environment_id, merchant_id, command_id),
  CHECK (fact_digest = mock_refund_fact_digest(canonical_fact)),
  CHECK ((outcome = 'confirmed' AND provider_refund_reference IS NOT NULL AND failure_code IS NULL)
      OR (outcome = 'final_no_effect' AND provider_refund_reference IS NULL
          AND failure_code IN ('refund_limit_exceeded', 'refund_policy_rejected', 'capture_not_refundable')))
);

CREATE TABLE mock_refund_queries (
  environment_id text NOT NULL CHECK (mock_uuid(environment_id)),
  merchant_id text NOT NULL CHECK (mock_ref(merchant_id)),
  query_id text NOT NULL CHECK (mock_uuid(query_id)),
  command_id text NOT NULL CHECK (mock_uuid(command_id)),
  mutation_generation bigint NOT NULL CHECK (mutation_generation > 0),
  request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
  canonical_request bytea NOT NULL CHECK (octet_length(canonical_request) BETWEEN 1 AND 8192),
  snapshot_state text NOT NULL CHECK (snapshot_state IN ('not_seen', 'in_progress', 'completed')),
  snapshot_body bytea NOT NULL CHECK (octet_length(snapshot_body) BETWEEN 1 AND 65536),
  snapshot_digest text NOT NULL CHECK (snapshot_digest ~ '^[0-9a-f]{64}$'),
  observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (environment_id, merchant_id, query_id),
  CHECK (request_digest = mock_hash(canonical_request)),
  CHECK (snapshot_digest = mock_hash(snapshot_body))
);

CREATE TABLE mock_refund_conflicts (
  environment_id text NOT NULL CHECK (mock_uuid(environment_id)),
  merchant_id text NOT NULL CHECK (mock_ref(merchant_id)),
  conflict_id text NOT NULL CHECK (mock_uuid(conflict_id)),
  command_id text CHECK (command_id IS NULL OR mock_uuid(command_id)),
  local_refund_id text CHECK (local_refund_id IS NULL OR mock_uuid(local_refund_id)),
  reason text NOT NULL CHECK (reason IN ('identity_conflict', 'fact_conflict', 'capture_limit_conflict')),
  observed_digest text NOT NULL CHECK (observed_digest ~ '^[0-9a-f]{64}$'),
  observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (environment_id, merchant_id, conflict_id)
);

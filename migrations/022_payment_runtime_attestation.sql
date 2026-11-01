-- 022-B3 private application/worker proof. No default proof, credentials or IO.
-- The owner installs exactly one attestation offline; online roles only read it.
CREATE TABLE payment_runtime_attestations (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton)
  REFERENCES payment_sandbox_environment(singleton),
 environment_id text NOT NULL UNIQUE REFERENCES payment_sandbox_environment(id),
 database_oid oid NOT NULL CHECK (database_oid<>0),
 schema_oid oid NOT NULL CHECK (schema_oid<>0),
 schema_owner_oid oid NOT NULL CHECK (schema_owner_oid<>0),
 app_role_oid oid NOT NULL CHECK (app_role_oid<>0),
 worker_role_oid oid NOT NULL CHECK (worker_role_oid<>0),
 projector_role_oid oid NOT NULL CHECK (projector_role_oid<>0),
 app_nonce_digest text NOT NULL CHECK (app_nonce_digest ~ '^[0-9a-f]{64}$'),
 worker_nonce_digest text NOT NULL CHECK (worker_nonce_digest ~ '^[0-9a-f]{64}$'),
 proof_version integer NOT NULL CHECK (proof_version=1),
 proof_canonical bytea NOT NULL CHECK (octet_length(proof_canonical) BETWEEN 1 AND 65536),
 proof_sha256 text NOT NULL CHECK (proof_sha256 ~ '^[0-9a-f]{64}$'
  AND proof_sha256=encode(sha256(proof_canonical),'hex')),
 created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 CHECK (app_role_oid<>worker_role_oid AND app_role_oid<>projector_role_oid
  AND worker_role_oid<>projector_role_oid AND app_role_oid<>schema_owner_oid
  AND worker_role_oid<>schema_owner_oid AND projector_role_oid<>schema_owner_oid)
);
CREATE TRIGGER payment_runtime_attestation_immutable
 BEFORE UPDATE OR DELETE ON payment_runtime_attestations FOR EACH ROW
 EXECUTE FUNCTION reject_history_mutation();
REVOKE ALL ON TABLE payment_runtime_attestations FROM PUBLIC;

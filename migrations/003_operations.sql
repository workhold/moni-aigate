CREATE TABLE platform_probe_attempts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, actor TEXT NOT NULL REFERENCES users(id),
 channel_id TEXT NOT NULL REFERENCES channels(id), credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id), model_id TEXT NOT NULL REFERENCES models(id),
 profile_version TEXT NOT NULL, upstream_model TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'sending' CHECK(status IN ('sending','completed','failed','unknown')),
 normalized_usage JSONB, cost_basis TEXT NOT NULL DEFAULT 'unknown', cost_amount NUMERIC(100,0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), completed_at TIMESTAMPTZ
);
CREATE TABLE delivery_observations (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL,
 request_id TEXT NOT NULL, stage TEXT NOT NULL CHECK(stage IN ('complete_local_write','write_failed','cancelled')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id)
);
CREATE INDEX delivery_observations_request ON delivery_observations(request_id,created_at DESC);
CREATE TRIGGER immutable_delivery_observation BEFORE UPDATE OR DELETE ON delivery_observations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

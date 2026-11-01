-- Directory discovery records are platform metadata, never customer metering.
ALTER TABLE channel_versions ADD CONSTRAINT channel_versions_discovery_identity UNIQUE(id,channel_id,credential_id);
ALTER TABLE channel_credentials ADD CONSTRAINT channel_credentials_discovery_identity UNIQUE(id,channel_id);
CREATE TABLE channel_model_discoveries (
 id TEXT PRIMARY KEY,
 actor TEXT NOT NULL REFERENCES users(id),
 channel_id TEXT NOT NULL REFERENCES channels(id),
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id),
 credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 format TEXT NOT NULL CHECK(format IN ('openai','anthropic','gemini')),
 bindings JSONB NOT NULL CHECK(jsonb_typeof(bindings)='array' AND jsonb_array_length(bindings)<=1000 AND octet_length(bindings::text)<=262144),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(channel_version_id,channel_id,credential_id) REFERENCES channel_versions(id,channel_id,credential_id),
 FOREIGN KEY(credential_id,channel_id) REFERENCES channel_credentials(id,channel_id)
);
CREATE INDEX channel_model_discoveries_channel_time ON channel_model_discoveries(channel_id,created_at DESC,id DESC);
CREATE TABLE channel_model_discovery_results (
 discovery_id TEXT PRIMARY KEY REFERENCES channel_model_discoveries(id),
 status TEXT NOT NULL CHECK(status IN ('completed','failed')),
 model_ids JSONB NOT NULL CHECK(jsonb_typeof(model_ids)='array' AND jsonb_array_length(model_ids)<=1000 AND octet_length(model_ids::text)<=262144),
 pages INTEGER NOT NULL CHECK(pages BETWEEN 0 AND 10),
 error_code TEXT NOT NULL DEFAULT '' CHECK(error_code='' OR error_code ~ '^[a-z][a-z0-9_]{0,79}$'),
 completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK((status='completed' AND error_code='' AND pages BETWEEN 1 AND 10) OR (status='failed' AND error_code<>'' AND pages=0 AND model_ids='[]'::jsonb))
);
CREATE TRIGGER immutable_model_discovery BEFORE UPDATE OR DELETE ON channel_model_discoveries FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_model_discovery_result BEFORE UPDATE OR DELETE ON channel_model_discovery_results FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

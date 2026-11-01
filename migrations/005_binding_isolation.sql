CREATE TABLE route_binding_isolations (
 channel_version_id TEXT NOT NULL REFERENCES channel_versions(id),
 upstream_model TEXT NOT NULL CHECK(length(upstream_model) BETWEEN 1 AND 200),
 reason TEXT NOT NULL CHECK(reason='model_configuration_error'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(channel_version_id,upstream_model)
);
CREATE TRIGGER immutable_route_binding_isolation BEFORE UPDATE OR DELETE ON route_binding_isolations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE tenants (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended')),
 allowed_regions TEXT[] NOT NULL DEFAULT ARRAY['global'],
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE users (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
 email TEXT NOT NULL UNIQUE CHECK(email=lower(email)),
 password_hash TEXT NOT NULL,
 platform_role TEXT NOT NULL DEFAULT 'none' CHECK(platform_role IN ('none','admin','finance_operator','finance_approver')),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended')),
 mfa_secret TEXT NOT NULL DEFAULT '', mfa_pending_secret TEXT NOT NULL DEFAULT '', mfa_enabled BOOLEAN NOT NULL DEFAULT false, mfa_last_step BIGINT NOT NULL DEFAULT -1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE memberships (
 tenant_id TEXT NOT NULL REFERENCES tenants(id), user_id TEXT NOT NULL REFERENCES users(id),
 role TEXT NOT NULL CHECK(role IN ('owner','member','billing_viewer')),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','suspended')),
 allow_all_models BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,user_id)
);
CREATE TABLE session_families (id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,user_id TEXT NOT NULL REFERENCES users(id),revoked_at TIMESTAMPTZ);
CREATE TABLE sessions (
 digest TEXT PRIMARY KEY, family_id TEXT NOT NULL REFERENCES session_families(id), user_id TEXT NOT NULL REFERENCES users(id), tenant_id TEXT REFERENCES tenants(id),
 csrf_token TEXT NOT NULL, mfa_verified BOOLEAN NOT NULL DEFAULT false,
 reauthenticated_at TIMESTAMPTZ, expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE mfa_recovery_codes (user_id TEXT NOT NULL REFERENCES users(id), digest TEXT NOT NULL, used_at TIMESTAMPTZ, PRIMARY KEY(user_id,digest));
CREATE TABLE models (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, public_name TEXT NOT NULL UNIQUE,
 enabled BOOLEAN NOT NULL DEFAULT true, profile JSONB NOT NULL,
 profile_version TEXT NOT NULL DEFAULT gen_random_uuid()::text,
 active_revision BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE model_capability_versions (id TEXT PRIMARY KEY, model_id TEXT NOT NULL REFERENCES models(id), profile JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp());
CREATE TABLE tenant_model_grants (tenant_id TEXT NOT NULL REFERENCES tenants(id), model_id TEXT NOT NULL REFERENCES models(id), PRIMARY KEY(tenant_id,model_id));
CREATE TABLE member_model_grants (tenant_id TEXT NOT NULL, user_id TEXT NOT NULL, model_id TEXT NOT NULL REFERENCES models(id), FOREIGN KEY(tenant_id,user_id) REFERENCES memberships(tenant_id,user_id), PRIMARY KEY(tenant_id,user_id,model_id));
CREATE TABLE api_keys (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id TEXT NOT NULL REFERENCES tenants(id), owner_id TEXT NOT NULL,
 name TEXT NOT NULL, prefix TEXT NOT NULL, digest TEXT NOT NULL UNIQUE,
 scopes TEXT[] NOT NULL, allowed_regions TEXT[] NOT NULL DEFAULT ARRAY['global'],
 expires_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ, lifetime_limit BIGINT CHECK(lifetime_limit>=0),
 spent BIGINT NOT NULL DEFAULT 0 CHECK(spent>=0), held BIGINT NOT NULL DEFAULT 0 CHECK(held>=0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,owner_id) REFERENCES memberships(tenant_id,user_id),
 CHECK(lifetime_limit IS NULL OR (spent::numeric+held::numeric)<=lifetime_limit)
);
CREATE INDEX api_keys_tenant ON api_keys(tenant_id,created_at DESC);
CREATE TABLE channels (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, name TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true, quarantined BOOLEAN NOT NULL DEFAULT false,
 current_version TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE channel_credentials (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, channel_id TEXT NOT NULL REFERENCES channels(id),
 secret_ciphertext TEXT NOT NULL, revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE channel_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, channel_id TEXT NOT NULL REFERENCES channels(id),
 base_url TEXT NOT NULL, provider TEXT NOT NULL DEFAULT 'openai', region TEXT NOT NULL DEFAULT 'global',
 allowed_cidrs TEXT[] NOT NULL DEFAULT '{}', credential_id TEXT NOT NULL REFERENCES channel_credentials(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE channels ADD CONSTRAINT channels_current_version FOREIGN KEY(current_version) REFERENCES channel_versions(id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE route_policy_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, model_id TEXT NOT NULL REFERENCES models(id), revision BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(model_id,revision)
);
CREATE TABLE model_bindings (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, route_policy_version_id TEXT NOT NULL REFERENCES route_policy_versions(id),
 channel_id TEXT NOT NULL REFERENCES channels(id), channel_version_id TEXT NOT NULL REFERENCES channel_versions(id),
 upstream_model TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 0,
 weight INTEGER NOT NULL CHECK(weight>0 AND weight<=1000000), max_inflight INTEGER NOT NULL CHECK(max_inflight>0 AND max_inflight<=1000),
 UNIQUE(route_policy_version_id,channel_id,upstream_model)
);
CREATE TABLE price_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, model_id TEXT NOT NULL REFERENCES models(id),
 currency TEXT NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 input_rate BIGINT NOT NULL CHECK(input_rate>=0), output_rate BIGINT NOT NULL CHECK(output_rate>=0),
 effective_at TIMESTAMPTZ NOT NULL, created_by TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), UNIQUE(model_id,effective_at)
);
CREATE TABLE cost_versions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, channel_id TEXT NOT NULL REFERENCES channels(id), upstream_model TEXT NOT NULL,
 input_rate BIGINT CHECK(input_rate>=0), output_rate BIGINT CHECK(output_rate>=0),
 basis TEXT NOT NULL DEFAULT 'unknown' CHECK(basis IN ('contract','invoice','unknown')),
 effective_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE audit_events (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text, actor TEXT NOT NULL,
 tenant_scope TEXT, action TEXT NOT NULL, target TEXT NOT NULL,
 sanitized_diff JSONB NOT NULL DEFAULT '{}', request_id TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX audit_events_time ON audit_events(created_at DESC,id);
CREATE FUNCTION reject_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable history'; END $$;
CREATE TRIGGER immutable_price BEFORE UPDATE OR DELETE ON price_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_capability BEFORE UPDATE OR DELETE ON model_capability_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_channel_version BEFORE UPDATE OR DELETE ON channel_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_route_version BEFORE UPDATE OR DELETE ON route_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_binding BEFORE UPDATE OR DELETE ON model_bindings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_audit BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TRIGGER immutable_cost_version BEFORE UPDATE OR DELETE ON cost_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

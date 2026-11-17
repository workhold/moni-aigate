-- 038 PR-2 channel/profile release evidence and explicit release intent.
-- Existing routes remain test-only; no qualification evidence is backfilled.

CREATE TABLE channel_profile_release_evidence (
    id text DEFAULT (gen_random_uuid())::text NOT NULL,
    channel_id text NOT NULL,
    channel_version_id text NOT NULL,
    model_id text NOT NULL,
    profile_version text NOT NULL COLLATE pg_catalog."C",
    upstream_model text NOT NULL,
    variant_key text NOT NULL DEFAULT 'default' COLLATE pg_catalog."C",
    supplier text NOT NULL,
    protocol text NOT NULL COLLATE pg_catalog."C",
    qualification text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    evidence_commit text NOT NULL,
    evidence_ref text NOT NULL,
    evidence_sha256 text NOT NULL,
    source_mode text NOT NULL,
    source_repository text NOT NULL,
    source_commit text NOT NULL,
    license_ref text NOT NULL,
    notice_ref text NOT NULL,
    modification_ref text NOT NULL,
    boundary_ref text NOT NULL,
    usage_ref text NOT NULL,
    retry_ref text NOT NULL,
    cost_ref text NOT NULL,
    cost_status text NOT NULL,
    open_issue_codes text[] DEFAULT '{}'::text[] NOT NULL,
    actor text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT channel_profile_release_evidence_pkey PRIMARY KEY (id),
    CONSTRAINT release_evidence_id_version UNIQUE (id,channel_version_id),
    CONSTRAINT release_evidence_channel_version FOREIGN KEY (channel_version_id,channel_id) REFERENCES channel_versions(id,channel_id),
    CONSTRAINT release_evidence_profile FOREIGN KEY (profile_version,model_id) REFERENCES model_capability_versions(id,model_id),
    CONSTRAINT release_evidence_variant CHECK (variant_key='default'),
    CONSTRAINT release_evidence_protocol CHECK (protocol IN ('openai_chat','openai_responses','anthropic_messages','openai_embeddings','jina_rerank','gemini_generate_content')),
    CONSTRAINT release_evidence_qualification CHECK (qualification IN ('mock_verified','external_flow_verified','supplier_profile_qualified')),
    CONSTRAINT release_evidence_source_mode CHECK (source_mode IN ('reference_only','vendored')),
    CONSTRAINT release_evidence_cost_status CHECK (cost_status IN ('known','unknown')),
    CONSTRAINT release_evidence_commit CHECK (evidence_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT release_evidence_source_commit CHECK (source_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT release_evidence_digest CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT release_evidence_ref CHECK (evidence_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$'),
    CONSTRAINT release_evidence_repository CHECK (source_repository ~ '^https://[A-Za-z0-9.-]+/[A-Za-z0-9._/-]+$' AND octet_length(source_repository)<=240),
    CONSTRAINT release_evidence_refs_safe CHECK (license_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$' AND notice_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$' AND modification_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$' AND boundary_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$' AND usage_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$' AND retry_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$' AND cost_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$'),
    CONSTRAINT release_evidence_scope_bounds CHECK (octet_length(supplier) BETWEEN 1 AND 120 AND octet_length(upstream_model) BETWEEN 1 AND 200 AND octet_length(actor) BETWEEN 1 AND 200 AND supplier !~ '[[:cntrl:]]' AND upstream_model !~ '[[:cntrl:]]' AND actor !~ '[[:cntrl:]]'),
    CONSTRAINT release_evidence_issue_bound CHECK (cardinality(open_issue_codes)<=32)
);

CREATE TABLE channel_profile_release_evidence_credentials (
    evidence_id text NOT NULL,
    channel_version_id text NOT NULL,
    credential_id text NOT NULL,
    CONSTRAINT release_evidence_credentials_pkey PRIMARY KEY (evidence_id,credential_id),
    CONSTRAINT release_evidence_credentials_parent FOREIGN KEY (evidence_id,channel_version_id) REFERENCES channel_profile_release_evidence(id,channel_version_id),
    CONSTRAINT release_evidence_credentials_pool FOREIGN KEY (channel_version_id,credential_id) REFERENCES channel_version_credentials(channel_version_id,credential_id)
);

CREATE TABLE channel_profile_release_withdrawals (
    evidence_id text NOT NULL,
    reason_code text NOT NULL,
    actor text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT channel_profile_release_withdrawals_pkey PRIMARY KEY (evidence_id),
    CONSTRAINT release_withdrawal_evidence FOREIGN KEY (evidence_id) REFERENCES channel_profile_release_evidence(id),
    CONSTRAINT release_withdrawal_reason CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT release_withdrawal_actor CHECK (octet_length(actor) BETWEEN 1 AND 200 AND actor !~ '[[:cntrl:]]')
);

CREATE INDEX release_evidence_history ON channel_profile_release_evidence(created_at DESC,id);
CREATE INDEX release_evidence_channel_history ON channel_profile_release_evidence(channel_id,created_at DESC,id);
CREATE INDEX release_evidence_model_history ON channel_profile_release_evidence(model_id,created_at DESC,id);

CREATE TRIGGER immutable_channel_profile_release_evidence BEFORE UPDATE OR DELETE ON channel_profile_release_evidence FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_channel_profile_release_evidence_credentials BEFORE UPDATE OR DELETE ON channel_profile_release_evidence_credentials FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_channel_profile_release_withdrawals BEFORE UPDATE OR DELETE ON channel_profile_release_withdrawals FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

ALTER TABLE route_policy_versions ADD COLUMN release_intent text NOT NULL DEFAULT 'test_only' CHECK (release_intent IN ('test_only','commercial'));
ALTER TABLE model_bindings ADD COLUMN release_evidence_id text REFERENCES channel_profile_release_evidence(id);

CREATE FUNCTION release_binding_intent_guard() RETURNS trigger
LANGUAGE plpgsql AS $func$
DECLARE intent text;
BEGIN
    SELECT p.release_intent INTO STRICT intent FROM route_policy_versions p WHERE p.id=NEW.route_policy_version_id;
    IF (intent='commercial') <> (NEW.release_evidence_id IS NOT NULL) THEN
        RAISE EXCEPTION 'release intent and binding evidence disagree' USING ERRCODE='check_violation';
    END IF;
    RETURN NEW;
END;
$func$;
REVOKE ALL ON FUNCTION release_binding_intent_guard() FROM PUBLIC;
CREATE TRIGGER release_binding_intent_guard BEFORE INSERT ON model_bindings FOR EACH ROW EXECUTE FUNCTION release_binding_intent_guard();

CREATE FUNCTION release_commercial_active_guard() RETURNS trigger
LANGUAGE plpgsql AS $func$
BEGIN
    IF EXISTS (
        SELECT 1 FROM route_policy_versions p
        WHERE p.model_id=NEW.id AND p.revision=NEW.active_revision AND p.release_intent='commercial'
    ) THEN
        IF NOT EXISTS (
            SELECT 1 FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id
            WHERE p.model_id=NEW.id AND p.revision=NEW.active_revision
        ) OR EXISTS (
            SELECT 1
            FROM route_policy_versions p
            JOIN model_bindings b ON b.route_policy_version_id=p.id
            LEFT JOIN channel_profile_release_evidence e ON e.id=b.release_evidence_id
            LEFT JOIN channel_versions v ON v.id=b.channel_version_id AND v.channel_id=b.channel_id
            LEFT JOIN model_capability_versions m ON m.id=p.profile_version AND m.model_id=p.model_id
            WHERE p.model_id=NEW.id AND p.revision=NEW.active_revision AND (
                b.release_evidence_id IS NULL OR e.id IS NULL OR v.id IS NULL OR m.id IS NULL OR
                v.channel_kind<>'direct' OR e.channel_id<>b.channel_id OR e.channel_version_id<>b.channel_version_id OR
                e.model_id<>p.model_id OR e.profile_version<>p.profile_version OR
                e.upstream_model<>b.upstream_model OR e.variant_key<>b.variant_key OR
                e.protocol<>m.protocol OR e.qualification<>'supplier_profile_qualified' OR
                e.cost_status<>'known' OR cardinality(e.open_issue_codes)<>0 OR
                EXISTS (SELECT 1 FROM channel_profile_release_withdrawals w WHERE w.evidence_id=e.id) OR
                NOT EXISTS (
                    SELECT 1 FROM channel_version_credentials cp
                    JOIN channel_credentials k ON k.id=cp.credential_id AND k.channel_id=cp.channel_id
                    WHERE cp.channel_version_id=b.channel_version_id AND k.revoked_at IS NULL
                ) OR EXISTS (
                    SELECT 1 FROM channel_version_credentials cp
                    JOIN channel_credentials k ON k.id=cp.credential_id AND k.channel_id=cp.channel_id
                    WHERE cp.channel_version_id=b.channel_version_id AND k.revoked_at IS NULL AND NOT EXISTS (
                        SELECT 1 FROM channel_profile_release_evidence_credentials c
                        WHERE c.evidence_id=e.id AND c.channel_version_id=b.channel_version_id AND c.credential_id=cp.credential_id
                    )
                )
            )
        ) THEN
            RAISE EXCEPTION 'commercial route has unqualified release evidence' USING ERRCODE='check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$func$;
REVOKE ALL ON FUNCTION release_commercial_active_guard() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER release_commercial_active_guard
AFTER UPDATE OF active_revision ON models DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION release_commercial_active_guard();
CREATE CONSTRAINT TRIGGER release_commercial_active_guard_insert
AFTER INSERT ON models DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION release_commercial_active_guard();

-- The online APP receives SELECT/INSERT through operations.ProvisionFor.
-- No migration role, application role or fixture is granted qualification by
-- this DDL, and existing route revisions remain test_only.
DO $$ BEGIN
    EXECUTE format('ALTER FUNCTION %I.release_binding_intent_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.release_commercial_active_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;

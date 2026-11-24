-- 041-P0 immutable offers and economic admission. Append-only current change.
-- No commercial qualification, external relay, wallet, or retry relaxation.

ALTER TABLE cost_versions DROP CONSTRAINT cost_versions_check;
ALTER TABLE cost_versions ADD CONSTRAINT cost_versions_check CHECK (currency IN ('CNY','USD') AND variant_key='default');

CREATE TABLE fx_policy_versions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    valuation_currency text NOT NULL CHECK(valuation_currency IN ('CNY','USD')),
    source text NOT NULL CHECK(octet_length(source) BETWEEN 1 AND 200),
    quoted_at timestamptz NOT NULL CHECK(isfinite(quoted_at)),
    valid_until timestamptz NOT NULL CHECK(isfinite(valid_until) AND valid_until>quoted_at),
    created_by text NOT NULL REFERENCES users(id),
    created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TABLE fx_policy_rates (
    policy_version_id text NOT NULL REFERENCES fx_policy_versions(id),
    source_currency text NOT NULL CHECK(source_currency IN ('CNY','USD')),
    target_currency text NOT NULL CHECK(target_currency IN ('CNY','USD')),
    rate_num bigint NOT NULL CHECK(rate_num>0),
    rate_den bigint NOT NULL CHECK(rate_den>0),
    fee_buffer_ppm integer NOT NULL CHECK(fee_buffer_ppm BETWEEN 0 AND 100000),
    PRIMARY KEY(policy_version_id,source_currency,target_currency),
    CHECK(source_currency<>target_currency)
);
CREATE FUNCTION fx_policy_rate_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS(SELECT 1 FROM fx_policy_versions WHERE id=NEW.policy_version_id AND created_xid=pg_current_xact_id()) THEN RAISE EXCEPTION 'FX policy rates must be sealed in the version transaction'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER fx_policy_rate_guard BEFORE INSERT ON fx_policy_rates FOR EACH ROW EXECUTE FUNCTION fx_policy_rate_guard();
CREATE TRIGGER immutable_fx_policy BEFORE UPDATE OR DELETE ON fx_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_fx_rate BEFORE UPDATE OR DELETE ON fx_policy_rates FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE task_policy_versions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    logical_model_id text NOT NULL REFERENCES models(id),
    taxonomy_version text NOT NULL CHECK(octet_length(taxonomy_version) BETWEEN 1 AND 80),
    default_task_class text NOT NULL CHECK(default_task_class IN ('coding_non_reasoning','coding_reasoning','reasoning','tool_use','extraction','summarization','translation','general_chat','unknown')),
    allowed_hints text[] NOT NULL DEFAULT '{}',
    class_candidates jsonb NOT NULL CHECK(jsonb_typeof(class_candidates)='object' AND octet_length(class_candidates::text)<=32768),
    unknown_action text NOT NULL CHECK(unknown_action IN ('deny','use_unknown')),
    classifier_version text NOT NULL DEFAULT 'none' CHECK(classifier_version='none'),
    shape_digest_algorithm text NOT NULL DEFAULT 'none' CHECK(shape_digest_algorithm='none'),
    created_by text NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
    CHECK(default_task_class<>'unknown')
);
CREATE TRIGGER immutable_task_policy BEFORE UPDATE OR DELETE ON task_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE quality_policy_versions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    logical_model_id text NOT NULL REFERENCES models(id),
    suite_version text NOT NULL CHECK(octet_length(suite_version) BETWEEN 1 AND 120),
    metric text NOT NULL CHECK(octet_length(metric) BETWEEN 1 AND 80),
    direction text NOT NULL CHECK(direction IN ('higher_is_better','lower_is_better')),
    threshold_micro integer NOT NULL CHECK(threshold_micro BETWEEN 0 AND 1000000),
    min_sample_count integer NOT NULL CHECK(min_sample_count BETWEEN 1 AND 1000000),
    min_confidence_lower_bound_micro integer NOT NULL CHECK(min_confidence_lower_bound_micro BETWEEN 0 AND 1000000),
    max_age_seconds bigint NOT NULL CHECK(max_age_seconds BETWEEN 60 AND 31536000),
    created_by text NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TRIGGER immutable_quality_policy BEFORE UPDATE OR DELETE ON quality_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE quality_evidence (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    physical_model_id text NOT NULL REFERENCES physical_models(id),
    channel_id text NOT NULL,
    channel_version_id text NOT NULL,
    execution_profile_version text NOT NULL REFERENCES model_capability_versions(id),
    deployment_revision text NOT NULL CHECK(octet_length(deployment_revision) BETWEEN 1 AND 120),
    model_revision text NOT NULL CHECK(octet_length(model_revision) BETWEEN 1 AND 120),
    inner_pool_version text,
    inner_pool_digest text,
    task_class text NOT NULL CHECK(task_class IN ('coding_non_reasoning','coding_reasoning','reasoning','tool_use','extraction','summarization','translation','general_chat','unknown')),
    suite_version text NOT NULL,
    rubric_version text NOT NULL,
    judge_version text NOT NULL,
    metric text NOT NULL,
    protocol text NOT NULL,
    tools_scope text NOT NULL CHECK(tools_scope IN ('none','required','any')),
    language_scope text NOT NULL CHECK(language_scope='any'),
    reasoning_scope text NOT NULL CHECK(reasoning_scope IN ('none','required','any')),
    score_micro integer NOT NULL CHECK(score_micro BETWEEN 0 AND 1000000),
    uncertainty_micro integer NOT NULL CHECK(uncertainty_micro BETWEEN 0 AND 1000000),
    confidence_lower_bound_micro integer NOT NULL CHECK(confidence_lower_bound_micro BETWEEN 0 AND 1000000),
    sample_count integer NOT NULL CHECK(sample_count BETWEEN 1 AND 1000000),
    observed_at timestamptz NOT NULL CHECK(isfinite(observed_at)),
    valid_until timestamptz NOT NULL CHECK(isfinite(valid_until) AND valid_until>observed_at),
    evidence_ref text NOT NULL CHECK(octet_length(evidence_ref) BETWEEN 1 AND 256),
    approved_by text NOT NULL REFERENCES users(id),
    approved_at timestamptz NOT NULL CHECK(isfinite(approved_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
    FOREIGN KEY(channel_id,channel_version_id) REFERENCES channel_versions(channel_id,id),
    CHECK((inner_pool_version IS NULL)=(inner_pool_digest IS NULL))
);
CREATE TRIGGER immutable_quality_evidence BEFORE UPDATE OR DELETE ON quality_evidence FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TABLE quality_evidence_withdrawals (
    evidence_id text PRIMARY KEY REFERENCES quality_evidence(id),
    withdrawn_by text NOT NULL REFERENCES users(id),
    reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),
    withdrawn_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(withdrawn_at))
);
CREATE FUNCTION quality_evidence_withdrawal_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE evidence text;
BEGIN
    SELECT id INTO STRICT evidence FROM quality_evidence WHERE id=NEW.evidence_id FOR UPDATE;
    RETURN NEW;
END;
$$;
CREATE TRIGGER quality_evidence_withdrawal_guard BEFORE INSERT ON quality_evidence_withdrawals FOR EACH ROW EXECUTE FUNCTION quality_evidence_withdrawal_guard();
CREATE TRIGGER immutable_quality_withdrawal BEFORE UPDATE OR DELETE ON quality_evidence_withdrawals FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE execution_policy_versions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    max_outer_attempts integer NOT NULL DEFAULT 1 CHECK(max_outer_attempts=1),
    inner_max_attempts integer NOT NULL DEFAULT 1 CHECK(inner_max_attempts=1),
    max_relay_depth integer NOT NULL DEFAULT 0 CHECK(max_relay_depth=0),
    settlement_mode text NOT NULL DEFAULT 'fixed_full_reservation' CHECK(settlement_mode='fixed_full_reservation'),
    cache_mode text NOT NULL DEFAULT 'disabled' CHECK(cache_mode='disabled'),
    cumulative_cost_policy text NOT NULL DEFAULT 'request_total_v1' CHECK(cumulative_cost_policy='request_total_v1'),
    unknown_cost_policy text NOT NULL DEFAULT 'deny' CHECK(unknown_cost_policy='deny'),
    created_by text NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TRIGGER immutable_execution_policy BEFORE UPDATE OR DELETE ON execution_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
ALTER TABLE route_policy_versions ADD COLUMN execution_policy_version_id text REFERENCES execution_policy_versions(id);
ALTER TABLE model_bindings ADD COLUMN quality_evidence_id text REFERENCES quality_evidence(id);

CREATE TABLE economics_policy_versions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    valuation_currency text NOT NULL CHECK(valuation_currency IN ('CNY','USD')),
    fx_policy_version_id text NOT NULL REFERENCES fx_policy_versions(id),
    min_margin_rate bigint NOT NULL CHECK(min_margin_rate BETWEEN 0 AND 10000),
    max_cumulative_cost_micro jsonb CHECK(max_cumulative_cost_micro IS NULL OR jsonb_typeof(max_cumulative_cost_micro)='object' AND octet_length(max_cumulative_cost_micro::text)<=1024),
    created_by text NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TRIGGER immutable_economics_policy BEFORE UPDATE OR DELETE ON economics_policy_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE service_offer_documents (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    kind text NOT NULL CHECK(kind IN ('disclosure','transparency')),
    body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=32768),
    created_by text NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TRIGGER immutable_offer_document BEFORE UPDATE OR DELETE ON service_offer_documents FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE service_offer_versions (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    logical_model_id text NOT NULL REFERENCES models(id),
    kind text NOT NULL CHECK(kind IN ('exact_model','same_model_multi_provider','branded_dynamic','task_optimized','generic_auto')),
    profile_version_id text NOT NULL REFERENCES model_capability_versions(id),
    price_version_id text NOT NULL REFERENCES price_versions(id),
    route_policy_version_id text NOT NULL UNIQUE REFERENCES route_policy_versions(id),
    disclosure_version_id text NOT NULL REFERENCES service_offer_documents(id),
    task_policy_version_id text NOT NULL REFERENCES task_policy_versions(id),
    quality_policy_version_id text NOT NULL REFERENCES quality_policy_versions(id),
    economics_version_id text NOT NULL REFERENCES economics_policy_versions(id),
    transparency_policy_version_id text NOT NULL REFERENCES service_offer_documents(id),
    execution_policy_version_id text NOT NULL REFERENCES execution_policy_versions(id),
    allowed_candidate_digest bytea NOT NULL CHECK(octet_length(allowed_candidate_digest)=32),
    savings_reference_version_id text REFERENCES service_offer_versions(id),
    approval_evidence_id text NOT NULL CHECK(octet_length(approval_evidence_id) BETWEEN 1 AND 256),
    created_by text NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TRIGGER immutable_service_offer BEFORE UPDATE OR DELETE ON service_offer_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE service_offer_states (
    offer_version_id text PRIMARY KEY REFERENCES service_offer_versions(id),
    state text NOT NULL CHECK(state IN ('draft','test_candidate','test_active','commercial_pending','commercial_active','rejected','withdrawn','retired')),
    revision bigint NOT NULL CHECK(revision>0),
    updated_by text NOT NULL REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(updated_at))
);
CREATE TABLE service_offer_lifecycle_events (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    offer_version_id text NOT NULL REFERENCES service_offer_versions(id),
    from_state text,
    to_state text NOT NULL,
    revision bigint NOT NULL CHECK(revision>0),
    actor text NOT NULL REFERENCES users(id),
    reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE FUNCTION service_offer_state_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        IF NEW.state NOT IN ('draft','test_active') OR NEW.revision<>1 THEN RAISE EXCEPTION 'invalid initial offer state'; END IF;
        INSERT INTO service_offer_lifecycle_events(offer_version_id,from_state,to_state,revision,actor,reason) VALUES(NEW.offer_version_id,NULL,NEW.state,NEW.revision,NEW.updated_by,'publish');
        RETURN NEW;
    END IF;
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'offer lifecycle history is immutable'; END IF;
    IF NEW.offer_version_id<>OLD.offer_version_id OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at THEN RAISE EXCEPTION 'invalid offer lifecycle revision'; END IF;
    IF NOT ((OLD.state='draft' AND NEW.state IN ('test_candidate','rejected')) OR
        (OLD.state='test_candidate' AND NEW.state IN ('test_active','rejected')) OR
        (OLD.state='test_active' AND NEW.state IN ('commercial_pending','withdrawn')) OR
        (OLD.state='commercial_pending' AND NEW.state IN ('commercial_active','rejected')) OR
        (OLD.state='commercial_active' AND NEW.state IN ('retired','withdrawn'))) THEN RAISE EXCEPTION 'invalid offer lifecycle transition'; END IF;
    INSERT INTO service_offer_lifecycle_events(offer_version_id,from_state,to_state,revision,actor,reason) VALUES(NEW.offer_version_id,OLD.state,NEW.state,NEW.revision,NEW.updated_by,'transition');
    RETURN NEW;
END;
$$;
CREATE TRIGGER service_offer_state_guard BEFORE INSERT OR UPDATE OR DELETE ON service_offer_states FOR EACH ROW EXECUTE FUNCTION service_offer_state_guard();
CREATE TRIGGER immutable_offer_lifecycle_event BEFORE UPDATE OR DELETE ON service_offer_lifecycle_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE request_economics_snapshots (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    reservation_id text NOT NULL UNIQUE REFERENCES reservations(id),
    offer_version_id text NOT NULL REFERENCES service_offer_versions(id),
    economics_version_id text NOT NULL REFERENCES economics_policy_versions(id),
    execution_policy_version_id text NOT NULL REFERENCES execution_policy_versions(id),
    task_policy_version_id text NOT NULL REFERENCES task_policy_versions(id),
    quality_policy_version_id text NOT NULL REFERENCES quality_policy_versions(id),
    task_class text NOT NULL CHECK(task_class IN ('coding_non_reasoning','coding_reasoning','reasoning','tool_use','extraction','summarization','translation','general_chat','unknown')),
    task_source text NOT NULL CHECK(task_source IN ('sku','hint')),
    settlement_mode text NOT NULL CHECK(settlement_mode='fixed_full_reservation'),
    max_outer_attempts integer NOT NULL CHECK(max_outer_attempts=1),
    reservation_coin_amount bigint NOT NULL CHECK(reservation_coin_amount>=0),
    payment_basis_micro jsonb NOT NULL CHECK(jsonb_typeof(payment_basis_micro)='object'),
    payment_basis_complete boolean NOT NULL,
    fx_policy_version_id text NOT NULL REFERENCES fx_policy_versions(id),
    valuation_currency text NOT NULL CHECK(valuation_currency IN ('CNY','USD')),
    fx_snapshot jsonb NOT NULL CHECK(jsonb_typeof(fx_snapshot)='object'),
    max_cumulative_cost_micro jsonb,
    protocol text NOT NULL,
    tools_scope text NOT NULL CHECK(tools_scope IN ('none','required','any')),
    language_scope text NOT NULL CHECK(language_scope='any'),
    reasoning_scope text NOT NULL CHECK(reasoning_scope IN ('none','required','any')),
    snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
    snapshot_canonical bytea NOT NULL CHECK(octet_length(snapshot_canonical)<=65536),
    snapshot_digest bytea NOT NULL CHECK(octet_length(snapshot_digest)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
    PRIMARY KEY(tenant_id,request_id),
    FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id)
);
CREATE FUNCTION request_economics_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE expected record; normalized_rates jsonb;
BEGIN
    SELECT r.model_id,r.route_revision,r.profile_version,r.protocol,res.amount,o.id AS offer_id,
       o.execution_policy_version_id,o.economics_version_id,o.task_policy_version_id,o.quality_policy_version_id,
       p.execution_policy_version_id AS route_execution_policy_version_id,o.price_version_id AS offer_price_version_id,ms.price_version_id AS request_price_version_id,
       ep.min_margin_rate AS policy_min_margin,ep.max_cumulative_cost_micro AS policy_max_cost,ep.valuation_currency AS policy_valuation,ep.fx_policy_version_id AS policy_fx,
       f.id AS fx_id,f.valuation_currency AS fx_valuation,f.source AS fx_source,f.quoted_at AS fx_quoted_at,f.valid_until AS fx_valid_until,f.created_by AS fx_created_by,f.created_at AS fx_created_at,
       x.created_by AS execution_created_by,x.created_at AS execution_created_at,
       coalesce(tp.class_candidates->NEW.task_class,'[]'::jsonb) AS expected_task_candidates
    INTO STRICT expected
    FROM request_records r JOIN reservations res ON res.tenant_id=r.tenant_id AND res.request_id=r.id AND res.id=NEW.reservation_id
    JOIN request_meter_snapshots ms ON ms.tenant_id=r.tenant_id AND ms.request_id=r.id
    JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
    JOIN service_offer_versions o ON o.id=NEW.offer_version_id AND o.route_policy_version_id=p.id
    JOIN economics_policy_versions ep ON ep.id=o.economics_version_id
    JOIN fx_policy_versions f ON f.id=ep.fx_policy_version_id
    JOIN task_policy_versions tp ON tp.id=o.task_policy_version_id
    JOIN execution_policy_versions x ON x.id=o.execution_policy_version_id
    JOIN service_offer_states os ON os.offer_version_id=o.id AND os.state='test_active'
    WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.request_id
    FOR SHARE OF os;
    normalized_rates:=CASE WHEN jsonb_typeof(NEW.fx_snapshot->'rates')='array' THEN NEW.fx_snapshot->'rates' WHEN jsonb_typeof(NEW.fx_snapshot->'rates')='null' THEN '[]'::jsonb ELSE NULL END;
    IF NEW.reservation_coin_amount<>expected.amount OR NEW.max_outer_attempts<>1 OR NEW.settlement_mode<>'fixed_full_reservation' OR
       NEW.execution_policy_version_id<>expected.execution_policy_version_id OR NEW.economics_version_id<>expected.economics_version_id OR
       NEW.task_policy_version_id<>expected.task_policy_version_id OR NEW.quality_policy_version_id<>expected.quality_policy_version_id OR
       NEW.protocol<>expected.protocol OR NOT NEW.payment_basis_complete OR NEW.execution_policy_version_id<>expected.route_execution_policy_version_id OR
       expected.offer_price_version_id IS DISTINCT FROM expected.request_price_version_id OR NEW.snapshot_digest<>sha256(NEW.snapshot_canonical) OR
       NEW.snapshot<>convert_from(NEW.snapshot_canonical,'UTF8')::jsonb OR
       NEW.snapshot->>'tenant_id'<>NEW.tenant_id OR NEW.snapshot->>'request_id'<>NEW.request_id OR NEW.snapshot->>'reservation_id'<>NEW.reservation_id OR
       NEW.snapshot->>'offer_version_id'<>NEW.offer_version_id OR NEW.snapshot->>'economics_version_id'<>NEW.economics_version_id OR
       NEW.snapshot->>'execution_policy_version_id'<>NEW.execution_policy_version_id OR NEW.snapshot->>'task_policy_version_id'<>NEW.task_policy_version_id OR
       NEW.snapshot->>'quality_policy_version_id'<>NEW.quality_policy_version_id OR NEW.snapshot->>'task_class'<>NEW.task_class OR NEW.snapshot->>'task_source'<>NEW.task_source OR
       NEW.snapshot->>'settlement_mode'<>NEW.settlement_mode OR (NEW.snapshot->>'max_outer_attempts')::integer<>NEW.max_outer_attempts OR
       (NEW.snapshot->>'reservation_coin_amount')::bigint<>NEW.reservation_coin_amount OR NEW.snapshot->'payment_basis_micro'<>NEW.payment_basis_micro OR
       (NEW.snapshot->>'payment_basis_complete')::boolean<>NEW.payment_basis_complete OR NEW.snapshot->>'fx_policy_version_id'<>NEW.fx_policy_version_id OR
       NEW.snapshot->>'valuation_currency'<>NEW.valuation_currency OR NEW.snapshot->'fx_snapshot'<>NEW.fx_snapshot OR
       (NEW.snapshot->>'min_margin_rate')::bigint<>expected.policy_min_margin OR NEW.valuation_currency<>expected.policy_valuation OR
       NEW.fx_policy_version_id<>expected.policy_fx OR NEW.max_cumulative_cost_micro IS DISTINCT FROM expected.policy_max_cost OR
       NOT ((NEW.max_cumulative_cost_micro IS NULL AND NEW.snapshot->>'max_cumulative_cost_micro' IS NULL) OR NEW.max_cumulative_cost_micro=NEW.snapshot->'max_cumulative_cost_micro') OR
       NEW.fx_snapshot->>'id'<>expected.fx_id OR NEW.fx_snapshot->>'valuation_currency'<>expected.fx_valuation OR NEW.fx_snapshot->>'source'<>expected.fx_source OR
       (NEW.fx_snapshot->>'quoted_at')::timestamptz<>expected.fx_quoted_at OR (NEW.fx_snapshot->>'valid_until')::timestamptz<>expected.fx_valid_until OR
       NEW.fx_snapshot->>'created_by'<>expected.fx_created_by OR (NEW.fx_snapshot->>'created_at')::timestamptz<>expected.fx_created_at OR normalized_rates IS NULL OR normalized_rates IS DISTINCT FROM
         (SELECT coalesce(jsonb_agg(jsonb_build_object('source_currency',source_currency,'target_currency',target_currency,'rate_num',rate_num,'rate_den',rate_den,'fee_buffer_ppm',fee_buffer_ppm) ORDER BY source_currency,target_currency),'[]'::jsonb) FROM fx_policy_rates WHERE policy_version_id=expected.fx_id) OR
       NEW.snapshot->'execution_policy'->>'id'<>NEW.execution_policy_version_id OR
       (NEW.snapshot->'execution_policy'->>'max_outer_attempts')::integer<>1 OR (NEW.snapshot->'execution_policy'->>'inner_max_attempts')::integer<>1 OR
       (NEW.snapshot->'execution_policy'->>'max_relay_depth')::integer<>0 OR NEW.snapshot->'execution_policy'->>'settlement_mode'<>'fixed_full_reservation' OR
       NEW.snapshot->'execution_policy'->>'cache_mode'<>'disabled' OR NEW.snapshot->'execution_policy'->>'cumulative_cost_policy'<>'request_total_v1' OR
       NEW.snapshot->'execution_policy'->>'unknown_cost_policy'<>'deny' OR
       NEW.snapshot->'execution_policy'->>'created_by'<>expected.execution_created_by OR (NEW.snapshot->'execution_policy'->>'created_at')::timestamptz<>expected.execution_created_at OR
       NEW.snapshot->'task_candidates' IS DISTINCT FROM expected.expected_task_candidates OR
       (SELECT count(*) FROM jsonb_object_keys(NEW.snapshot->'execution_policy'))<>10 OR
       EXISTS(SELECT 1 FROM jsonb_object_keys(NEW.snapshot->'execution_policy') key WHERE key NOT IN ('id','max_outer_attempts','inner_max_attempts','max_relay_depth','settlement_mode','cache_mode','cumulative_cost_policy','unknown_cost_policy','created_by','created_at')) OR
       (SELECT count(*) FROM jsonb_object_keys(NEW.fx_snapshot))<>8 OR
       EXISTS(SELECT 1 FROM jsonb_object_keys(NEW.fx_snapshot) key WHERE key<>'rates' AND jsonb_typeof(NEW.fx_snapshot->key)='null') OR
       EXISTS(SELECT 1 FROM jsonb_object_keys(NEW.snapshot->'execution_policy') key WHERE jsonb_typeof(NEW.snapshot->'execution_policy'->key)='null') OR
       EXISTS(SELECT 1 FROM jsonb_object_keys(NEW.fx_snapshot) key WHERE key NOT IN ('id','valuation_currency','source','quoted_at','valid_until','created_by','created_at','rates')) OR
       jsonb_typeof(NEW.snapshot->'task_candidates')<>'array' OR
       (NEW.snapshot->>'created_at')::timestamptz<>NEW.created_at OR (SELECT count(*) FROM jsonb_object_keys(NEW.snapshot))<>23 OR
       EXISTS(SELECT 1 FROM jsonb_object_keys(NEW.snapshot) key WHERE key<>'max_cumulative_cost_micro' AND jsonb_typeof(NEW.snapshot->key)='null') OR EXISTS(
         SELECT 1 FROM jsonb_object_keys(NEW.snapshot) key WHERE key NOT IN
         ('tenant_id','request_id','reservation_id','offer_version_id','economics_version_id','execution_policy_version_id','task_policy_version_id','quality_policy_version_id','task_class','task_source','task_candidates','settlement_mode','max_outer_attempts','reservation_coin_amount','payment_basis_micro','payment_basis_complete','fx_policy_version_id','valuation_currency','fx_snapshot','min_margin_rate','max_cumulative_cost_micro','execution_policy','created_at')
       ) THEN RAISE EXCEPTION 'request economics snapshot mismatch'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER request_economics_guard BEFORE INSERT ON request_economics_snapshots FOR EACH ROW EXECUTE FUNCTION request_economics_guard();
CREATE TRIGGER immutable_request_economics BEFORE UPDATE OR DELETE ON request_economics_snapshots FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE attempt_cost_components (
    id text DEFAULT (gen_random_uuid())::text PRIMARY KEY,
    tenant_id text NOT NULL,
    attempt_id text NOT NULL REFERENCES upstream_attempts(id),
    component_id text NOT NULL CHECK(octet_length(component_id) BETWEEN 1 AND 200),
    layer text NOT NULL CHECK(layer='inner'),
    source text NOT NULL CHECK(octet_length(source) BETWEEN 1 AND 80),
    source_event_id text NOT NULL CHECK(octet_length(source_event_id) BETWEEN 1 AND 200),
    revision bigint NOT NULL CHECK(revision>0),
    currency text NOT NULL CHECK(currency IN ('CNY','USD')),
    basis text NOT NULL CHECK(basis IN ('contract','invoice','unknown')),
    amount_micro numeric(100,0),
    as_of timestamptz NOT NULL CHECK(isfinite(as_of)),
    evidence_ref text NOT NULL CHECK(octet_length(evidence_ref) BETWEEN 1 AND 256),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
    UNIQUE(attempt_id,component_id,revision),
    UNIQUE(tenant_id,source,source_event_id,revision),
    CHECK((basis='unknown' AND amount_micro IS NULL) OR (basis IN ('contract','invoice') AND amount_micro>=0))
);
CREATE FUNCTION attempt_cost_component_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS(SELECT 1 FROM upstream_attempts a WHERE a.tenant_id=NEW.tenant_id AND a.id=NEW.attempt_id) THEN RAISE EXCEPTION 'cost component tenant mismatch'; END IF;
    IF NEW.revision>1 AND NOT EXISTS(SELECT 1 FROM attempt_cost_components c WHERE c.attempt_id=NEW.attempt_id AND c.component_id=NEW.component_id AND c.revision=NEW.revision-1 AND c.currency=NEW.currency) THEN RAISE EXCEPTION 'cost component revision gap'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER attempt_cost_component_guard BEFORE INSERT ON attempt_cost_components FOR EACH ROW EXECUTE FUNCTION attempt_cost_component_guard();
CREATE TRIGGER immutable_attempt_cost_component BEFORE UPDATE OR DELETE ON attempt_cost_components FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE FUNCTION profit_attempt_budget_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE allowed integer;
BEGIN
    SELECT s.max_outer_attempts INTO allowed FROM request_economics_snapshots s WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
    IF allowed IS NOT NULL AND NEW.sequence>allowed THEN RAISE EXCEPTION 'execution policy outer attempt budget exceeded'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER profit_attempt_budget_guard BEFORE INSERT ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION profit_attempt_budget_guard();

CREATE FUNCTION profit_fixed_full_settlement_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE snapshot record;
BEGIN
    SELECT s.reservation_coin_amount,s.settlement_mode INTO snapshot FROM request_economics_snapshots s WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
    IF NOT FOUND THEN RETURN NEW; END IF;
    IF NEW.status='settled' AND (NEW.reason<>'charged_fixed_full_reservation' OR NEW.charged_amount<>snapshot.reservation_coin_amount OR NEW.raw_retail_amount<>snapshot.reservation_coin_amount OR NEW.waived_retail_amount<>0) THEN RAISE EXCEPTION 'fixed full reservation settlement mismatch'; END IF;
    IF NEW.status='released' AND (NEW.charged_amount<>0 OR NEW.raw_retail_amount<>0 OR NEW.waived_retail_amount<>0) THEN RAISE EXCEPTION 'released fixed full reservation has a charge'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER profit_fixed_full_settlement_guard BEFORE INSERT ON settlements FOR EACH ROW EXECUTE FUNCTION profit_fixed_full_settlement_guard();

CREATE FUNCTION profit_offer_dispatch_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE snap record; offer_state text; evidence record; policy record; now timestamptz; conservative integer;
BEGIN
    IF NOT (OLD.delivery_state='prepared' AND NEW.delivery_state='sending') THEN RETURN NEW; END IF;
    SELECT s.*,r.profile_version,r.protocol AS request_protocol INTO snap FROM request_economics_snapshots s JOIN request_records r ON r.tenant_id=s.tenant_id AND r.id=s.request_id WHERE s.tenant_id=NEW.tenant_id AND s.request_id=NEW.request_id;
    IF NOT FOUND THEN RETURN NEW; END IF;
    SELECT state INTO STRICT offer_state FROM service_offer_states WHERE offer_version_id=snap.offer_version_id FOR SHARE;
    IF offer_state<>'test_active' OR snap.max_outer_attempts<NEW.sequence THEN RAISE EXCEPTION 'service offer is not active for dispatch'; END IF;
    SELECT p.* INTO STRICT policy FROM quality_policy_versions p JOIN service_offer_versions o ON o.quality_policy_version_id=p.id WHERE o.id=snap.offer_version_id;
    SELECT e.* INTO STRICT evidence FROM quality_evidence e JOIN model_bindings b ON b.quality_evidence_id=e.id JOIN upstream_attempts a ON a.channel_id=b.channel_id AND a.channel_version_id=b.channel_version_id AND a.upstream_model=b.upstream_model
    WHERE a.id=NEW.id AND b.route_policy_version_id=(SELECT route_policy_version_id FROM service_offer_versions WHERE id=snap.offer_version_id) FOR SHARE OF e;
    now:=clock_timestamp();
    IF evidence.task_class<>snap.task_class OR evidence.execution_profile_version<>snap.profile_version OR evidence.protocol<>snap.request_protocol OR
       evidence.tools_scope NOT IN ('any',snap.tools_scope) OR evidence.language_scope<>snap.language_scope OR evidence.reasoning_scope NOT IN ('any',snap.reasoning_scope) OR
       evidence.suite_version<>policy.suite_version OR evidence.metric<>policy.metric OR evidence.sample_count<policy.min_sample_count OR
       evidence.observed_at>now OR evidence.valid_until<=now OR evidence.observed_at+make_interval(secs=>policy.max_age_seconds)<=now OR
       EXISTS(SELECT 1 FROM quality_evidence_withdrawals w WHERE w.evidence_id=evidence.id) THEN RAISE EXCEPTION 'quality evidence is not eligible'; END IF;
    conservative:=CASE WHEN policy.direction='higher_is_better' THEN evidence.confidence_lower_bound_micro ELSE evidence.score_micro+evidence.uncertainty_micro END;
    IF conservative<policy.min_confidence_lower_bound_micro OR
       (policy.direction='higher_is_better' AND conservative<policy.threshold_micro) OR
       (policy.direction='lower_is_better' AND conservative>policy.threshold_micro) THEN RAISE EXCEPTION 'quality evidence is below policy'; END IF;
    IF NOT EXISTS(SELECT 1 FROM channel_versions v WHERE v.id=NEW.channel_version_id AND v.channel_id=NEW.channel_id AND v.channel_kind='direct') THEN RAISE EXCEPTION 'P0 offers require direct execution'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER profit_offer_dispatch_guard BEFORE UPDATE OF delivery_state ON upstream_attempts FOR EACH ROW EXECUTE FUNCTION profit_offer_dispatch_guard();

CREATE OR REPLACE FUNCTION assert_pricing_safe_input(k text,j jsonb) RETURNS void
LANGUAGE plpgsql IMMUTABLE AS $_$
DECLARE shape jsonb; label text;
BEGIN
 IF jsonb_typeof(j) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'pricing safe input must be object'; END IF;
 CASE k
 WHEN 'price_group.create' THEN shape:=jsonb_build_object('label',j->'label');
 WHEN 'price_group.update' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','label',j->'label','state',j->'state');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR jsonb_typeof(j->'state') IS DISTINCT FROM 'string' OR (j->>'state') NOT IN ('active','retired') THEN RAISE EXCEPTION 'invalid group update input'; END IF;
 WHEN 'model_alias.create' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','alias',j->'alias');
  IF NOT pricing_json_uuid(j->'model_id') OR jsonb_typeof(j->'alias') IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'invalid model alias create input'; END IF;
 WHEN 'model_alias.retire' THEN
  shape:=jsonb_build_object('alias',j->'alias','expected_revision',j->'expected_revision');
  IF jsonb_typeof(j->'alias') IS DISTINCT FROM 'string' OR NOT pricing_json_uint(j->'expected_revision',true) THEN RAISE EXCEPTION 'invalid model alias retire input'; END IF;
 WHEN 'group_price.compile' THEN
  shape:=jsonb_build_object('multiplier',j->'multiplier','model_ids',j->'model_ids','effective_at',j->'effective_at','expected_base_version_ids',j->'expected_base_version_ids');
  IF jsonb_typeof(j->'multiplier') IS DISTINCT FROM 'string' OR (j->>'multiplier') !~ '^(0|[1-9][0-9]*)([.][0-9]{1,6})?$' OR (j->>'multiplier')::numeric<=0 OR (j->>'multiplier')::numeric>100 OR jsonb_typeof(j->'model_ids') IS DISTINCT FROM 'array' OR jsonb_array_length(j->'model_ids') NOT BETWEEN 1 AND 100 OR NOT pricing_json_time(j->'effective_at') OR (jsonb_typeof(j->'expected_base_version_ids') IS DISTINCT FROM 'object' AND j->'expected_base_version_ids' IS DISTINCT FROM 'null'::jsonb) THEN RAISE EXCEPTION 'invalid group price compile input'; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(j->'model_ids') x WHERE jsonb_typeof(x) IS DISTINCT FROM 'string' OR NOT pricing_json_uuid(x)) THEN RAISE EXCEPTION 'invalid group price compile model ids'; END IF;
 WHEN 'group_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"COIN"'::jsonb OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid group price input'; END IF;
  PERFORM meter_text_rates_canonical(j->'rates','retail');
 WHEN 'default_price.publish' THEN
  shape:=jsonb_build_object('model_id',j->'model_id','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','rates',j->'rates','effective_at',j->'effective_at');
  IF NOT pricing_json_uuid(j->'model_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR j->'currency' IS DISTINCT FROM '"COIN"'::jsonb OR NOT pricing_json_time(j->'effective_at') OR j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid default price input'; END IF;
  PERFORM meter_text_rates_canonical(j->'rates','retail');
 WHEN 'cost_price.publish' THEN
  shape:=jsonb_build_object('channel_id',j->'channel_id','upstream_model',j->'upstream_model','schema_id',j->'schema_id','schema_digest',j->'schema_digest','variant_key',j->'variant_key','currency',j->'currency','basis',j->'basis','rates',j->'rates','effective_at',j->'effective_at','expected_channel_revision',j->'expected_channel_revision');
  IF NOT pricing_json_uuid(j->'channel_id') OR NOT pricing_json_uuid(j->'schema_id') OR jsonb_typeof(j->'upstream_model') IS DISTINCT FROM 'string' OR octet_length(j->>'upstream_model') NOT BETWEEN 1 AND 200 OR (j->>'upstream_model')<>btrim(j->>'upstream_model') OR (j->>'upstream_model')~'[[:cntrl:]]' OR jsonb_typeof(j->'schema_digest') IS DISTINCT FROM 'string' OR (j->>'schema_digest') !~ '^[0-9a-f]{64}$' OR j->'variant_key' IS DISTINCT FROM '"default"'::jsonb OR (j->'currency') NOT IN ('"CNY"'::jsonb,'"USD"'::jsonb) OR (j->>'basis') NOT IN ('contract','invoice','unknown') OR NOT pricing_json_time(j->'effective_at') OR NOT pricing_json_uint(j->'expected_channel_revision',true) THEN RAISE EXCEPTION 'invalid cost price input'; END IF;
  IF (j->>'basis')='unknown' THEN IF j->'rates' IS DISTINCT FROM 'null'::jsonb THEN RAISE EXCEPTION 'unknown cost has rates'; END IF; ELSE IF j->'rates'->'schema_id' IS DISTINCT FROM j->'schema_id' THEN RAISE EXCEPTION 'invalid cost price input'; END IF; PERFORM meter_text_rates_canonical(j->'rates','supplier'); END IF;
 WHEN 'tenant_pricing.assign' THEN
  shape:=jsonb_build_object('expected_revision',j->'expected_revision','price_group_id',j->'price_group_id');
  IF NOT pricing_json_uint(j->'expected_revision',true) OR NOT (j ? 'price_group_id') OR (jsonb_typeof(j->'price_group_id') IS DISTINCT FROM 'null' AND NOT pricing_json_uuid(j->'price_group_id')) THEN RAISE EXCEPTION 'invalid tenant pricing input'; END IF;
 ELSE RAISE EXCEPTION 'unknown pricing operation kind'; END CASE;
 IF j IS DISTINCT FROM shape THEN RAISE EXCEPTION 'pricing input keys mismatch'; END IF;
 IF k IN ('price_group.create','price_group.update') THEN
  IF jsonb_typeof(j->'label') IS DISTINCT FROM 'string' THEN RAISE EXCEPTION 'pricing label must be string'; END IF;
  label:=j->>'label';
  IF octet_length(label) NOT BETWEEN 1 AND 100 OR label<>btrim(label) OR label ~ '[[:cntrl:]]' THEN RAISE EXCEPTION 'invalid pricing label'; END IF;
 END IF;
 IF k='model_alias.create' THEN
  IF octet_length(j->>'alias') NOT BETWEEN 1 AND 100 OR j->>'alias'<>btrim(j->>'alias') OR (j->>'alias')~'[[:space:]]' OR (j->>'alias')~'[[:cntrl:]]' THEN RAISE EXCEPTION 'invalid model alias'; END IF;
 END IF;
 IF k='model_alias.retire' AND octet_length(j->>'alias') NOT BETWEEN 1 AND 100 THEN RAISE EXCEPTION 'invalid model alias'; END IF;
END $_$;

CREATE OR REPLACE FUNCTION meter_insert_token_cost(cost_id text, channel_id text, upstream_model text, schema_id text, input_per_million bigint, output_per_million bigint, basis text, effective_at timestamptz, created_by text, pricing_operation_id text, created_at timestamptz) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE sid text; sdigest bytea; in_n bigint; in_d bigint; out_n bigint; out_d bigint; canonical bytea; digest bytea; cid text; cost_currency text;
BEGIN
 IF basis NOT IN ('contract','invoice','unknown') THEN RAISE EXCEPTION 'invalid cost basis'; END IF;
 SELECT s.id,s.schema_digest INTO sid,sdigest FROM meter_schemas s
 WHERE s.id=meter_insert_token_cost.schema_id AND EXISTS(SELECT 1 FROM model_capability_versions c JOIN model_capability_variants v ON v.profile_version=c.id WHERE c.schema_id=s.id AND c.schema_digest=s.schema_digest AND v.variant_key='default');
 IF sid IS NULL THEN RAISE EXCEPTION 'published text schema is required before a supplier quote'; END IF;
 SELECT op.safe_input->>'currency' INTO STRICT cost_currency FROM pricing_operations op WHERE op.operation_id=meter_insert_token_cost.pricing_operation_id AND op.kind='cost_price.publish';
 IF cost_currency NOT IN ('CNY','USD') THEN RAISE EXCEPTION 'invalid supplier cost currency'; END IF;
 cid:=coalesce(cost_id,gen_random_uuid()::text);
 IF basis='unknown' THEN
  IF input_per_million IS NOT NULL OR output_per_million IS NOT NULL THEN RAISE EXCEPTION 'unknown cost has no rates'; END IF;
  INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,created_by,pricing_operation_id,created_at)
  VALUES(cid,channel_id,upstream_model,basis,coalesce(effective_at,clock_timestamp()),sid,sdigest,'default',cost_currency,created_by,pricing_operation_id,coalesce(created_at,clock_timestamp()));
  RETURN;
 END IF;
 IF input_per_million IS NULL OR output_per_million IS NULL THEN RAISE EXCEPTION 'known cost requires both rates'; END IF;
 SELECT r.numerator,r.denominator INTO in_n,in_d FROM meter_reduce_million(input_per_million) r;
 SELECT r.numerator,r.denominator INTO out_n,out_d FROM meter_reduce_million(output_per_million) r;
 canonical:=meter_rate_canonical(sid,'supplier',in_n,in_d,out_n,out_d);
 digest:=sha256(meter_frames(ARRAY[convert_to('moni-aigate.metering.rates.v1','UTF8'),sdigest,convert_to('supplier','UTF8'),canonical]));
 INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,currency,rates_canonical,rates_digest,created_by,pricing_operation_id,created_at)
 VALUES(cid,channel_id,upstream_model,basis,coalesce(effective_at,clock_timestamp()),sid,sdigest,'default',cost_currency,canonical,digest,created_by,pricing_operation_id,coalesce(created_at,clock_timestamp()));
 INSERT INTO cost_rate_lines(cost_version_id,dimension,unit,numerator,denominator) VALUES(cid,'text_input_token','token',in_n,in_d),(cid,'text_output_token','token',out_n,out_d);
END $$;

CREATE OR REPLACE FUNCTION meter_settlement_amount() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE expected numeric; charge numeric; reserved numeric; input_qty bigint; output_qty bigint;
 in_n bigint; in_d bigint; out_n bigint; out_d bigint; state text;
BEGIN
 IF NEW.reason='charged_fixed_full_reservation' THEN
  SELECT f.retail_usage_state,r.amount INTO state,reserved
  FROM finalization_records f JOIN reservations r ON r.tenant_id=f.tenant_id AND r.request_id=f.request_id
  JOIN request_economics_snapshots es ON es.tenant_id=f.tenant_id AND es.request_id=f.request_id AND es.reservation_id=r.id AND es.settlement_mode='fixed_full_reservation'
  WHERE f.tenant_id=NEW.tenant_id AND f.request_id=NEW.request_id AND f.id=NEW.finalization_id;
  IF NOT FOUND OR state IS DISTINCT FROM 'confirmed_final' OR NEW.status<>'settled' OR NEW.charged_amount<>reserved OR NEW.raw_retail_amount<>reserved OR NEW.waived_retail_amount<>0 THEN
   RAISE EXCEPTION 'fixed full settlement does not match its final evidence and reservation';
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.reason<>'charged_usage' THEN
  IF NEW.status<>'released' OR NEW.charged_amount<>0 OR NEW.raw_retail_amount<>0 OR NEW.waived_retail_amount<>0 THEN
   RAISE EXCEPTION 'released settlement must be zero-valued';
  END IF;
  RETURN NULL;
 END IF;
 IF NEW.status<>'settled' OR NEW.finalization_id IS NULL THEN
  RAISE EXCEPTION 'charged usage settlement is incomplete';
 END IF;
 SELECT f.retail_usage_state,r.amount,
   (convert_from(f.retail_quantities_canonical,'UTF8')::jsonb->'quantities'->0->>'quantity')::bigint,
   (convert_from(f.retail_quantities_canonical,'UTF8')::jsonb->'quantities'->1->>'quantity')::bigint,
   ri.numerator,ri.denominator,ro.numerator,ro.denominator
   INTO state,reserved,input_qty,output_qty,in_n,in_d,out_n,out_d
   FROM finalization_records f
   JOIN request_meter_snapshots s ON s.tenant_id=f.tenant_id AND s.request_id=f.request_id
   JOIN reservations r ON r.tenant_id=s.tenant_id AND r.request_id=s.request_id
   JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
    AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
    AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
   JOIN price_rate_lines ri ON ri.price_version_id=p.id AND ri.dimension='text_input_token' AND ri.unit='token'
   JOIN price_rate_lines ro ON ro.price_version_id=p.id AND ro.dimension='text_output_token' AND ro.unit='token'
  WHERE f.tenant_id=NEW.tenant_id AND f.request_id=NEW.request_id AND f.id=NEW.finalization_id;
 IF NOT FOUND OR state IS DISTINCT FROM 'confirmed_final' OR input_qty IS NULL OR output_qty IS NULL THEN
  RAISE EXCEPTION 'charged usage finalization is not a retail final';
 END IF;
 expected := meter_token_amount(input_qty,output_qty,in_n,in_d,out_n,out_d);
 IF expected IS NULL THEN RAISE EXCEPTION 'charged usage amount cannot be calculated'; END IF;
 charge := LEAST(expected,reserved::numeric);
 IF NEW.raw_retail_amount IS DISTINCT FROM expected
    OR NEW.charged_amount::numeric IS DISTINCT FROM charge
    OR NEW.waived_retail_amount IS DISTINCT FROM expected-charge THEN
  RAISE EXCEPTION 'settlement amount does not match original retail vector';
 END IF;
 RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION meter_settlement_finalization() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE retail text;
BEGIN
 IF NEW.finalization_id IS NULL THEN
  IF NEW.reason IN ('charged_usage','charged_fixed_full_reservation') THEN RAISE EXCEPTION 'charged usage has no finalization'; END IF;
  RETURN NULL;
 END IF;
 SELECT retail_usage_state INTO retail FROM finalization_records WHERE id=NEW.finalization_id AND tenant_id=NEW.tenant_id AND request_id=NEW.request_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'settlement finalization does not match request'; END IF;
 IF NEW.reason IN ('charged_usage','charged_fixed_full_reservation') AND retail IS DISTINCT FROM 'confirmed_final' THEN
  RAISE EXCEPTION 'charged usage is not a confirmed retail final';
 END IF;
 RETURN NULL;
END $$;

REVOKE ALL ON fx_policy_versions,fx_policy_rates,task_policy_versions,quality_policy_versions,quality_evidence,quality_evidence_withdrawals,execution_policy_versions,economics_policy_versions,service_offer_documents,service_offer_versions,service_offer_states,service_offer_lifecycle_events,request_economics_snapshots,attempt_cost_components FROM PUBLIC;
REVOKE ALL ON FUNCTION quality_evidence_withdrawal_guard(),service_offer_state_guard(),request_economics_guard(),attempt_cost_component_guard(),profit_attempt_budget_guard(),profit_fixed_full_settlement_guard(),profit_offer_dispatch_guard(),meter_settlement_amount(),meter_settlement_finalization(),fx_policy_rate_guard(),meter_insert_token_cost(text,text,text,text,bigint,bigint,text,timestamptz,text,text,timestamptz) FROM PUBLIC;
DO $$ BEGIN
    EXECUTE format('ALTER FUNCTION %I.fx_policy_rate_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.assert_pricing_safe_input(text,jsonb) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.quality_evidence_withdrawal_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.service_offer_state_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.request_economics_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.attempt_cost_component_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.profit_attempt_budget_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.profit_fixed_full_settlement_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.profit_offer_dispatch_guard() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_settlement_amount() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_settlement_finalization() SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
    EXECUTE format('ALTER FUNCTION %I.meter_insert_token_cost(text,text,text,text,bigint,bigint,text,timestamptz,text,text,timestamptz) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),current_schema());
END $$;

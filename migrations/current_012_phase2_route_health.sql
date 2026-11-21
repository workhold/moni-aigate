-- 032 slice 2.2: immutable per-route circuit-breaker parameters.
-- Existing revisions retain the historical 3 failures / 30 seconds / one probe.
ALTER TABLE route_policy_versions
    ADD COLUMN failure_threshold integer NOT NULL DEFAULT 3
        CHECK (failure_threshold BETWEEN 2 AND 20),
    ADD COLUMN open_ms integer NOT NULL DEFAULT 30000
        CHECK (open_ms BETWEEN 1000 AND 600000),
    ADD COLUMN half_open_tokens integer NOT NULL DEFAULT 1
        CHECK (half_open_tokens BETWEEN 1 AND 4);

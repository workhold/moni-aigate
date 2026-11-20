-- 032 review hardening: pin every new/replaced validator to the actual deployment
-- schema, then pg_catalog, with temporary objects last. This is append-only; the
-- installed current_013 migration remains unchanged.
DO $$
DECLARE
    signature text;
BEGIN
    FOREACH signature IN ARRAY ARRAY[
        'text_response_cache_complete()',
        'text_response_cache_mutation()',
        'cache_request_digest_immutable()',
        'cache_hit_no_attempt()',
        'cache_finalization_complete()',
        'meter_finalization_complete()',
        'meter_settlement_amount()'
    ] LOOP
        EXECUTE format('ALTER FUNCTION %I.%s SET search_path TO %I,pg_catalog,pg_temp', current_schema(), signature, current_schema());
    END LOOP;
END
$$;

-- Ordinary observations can have successive finalization/delivery projections.
-- Only the closed cache source needs source-event uniqueness on finalization.
ALTER TABLE finalization_records
    DROP CONSTRAINT finalization_source_identity_unique;
CREATE UNIQUE INDEX finalization_cache_source_identity_unique
    ON finalization_records(tenant_id,source,source_event_id)
    WHERE source='cache';

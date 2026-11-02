-- C0-R1-A offline candidate; not an applied migration or runtime provisioner.
-- Render only these two identifiers with pgx.Identifier in controlled maintenance.
GRANT SELECT (id, verified_query_id, conflicting_digest, received_at)
ON {{schema}}.payment_query_conflicts TO {{app}};

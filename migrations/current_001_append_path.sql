-- Current-only append path marker. Historical 001-023 plans never execute this.
-- The table is infrastructure metadata and is deliberately outside wallet,
-- billing, settlement, provider, and media business data.
CREATE TABLE current_schema_extensions (
    extension_name TEXT PRIMARY KEY,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT current_schema_extensions_name CHECK (extension_name ~ '^[a-z][a-z0-9_-]{0,63}$')
);

INSERT INTO current_schema_extensions(extension_name)
VALUES ('append-migrations-v1');

#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=app_pass="$APP_DB_PASSWORD" --set=projection_pass="$PROJECTION_DB_PASSWORD" <<'SQL'
-- The current empty-database baseline requires the migration identity to own
-- the installation schema. PostgreSQL 16 initializes public to the implicit
-- pg_database_owner role, so make the Compose migration owner explicit before
-- the application creates schema_migrations and current MVP relations.
ALTER SCHEMA public OWNER TO moni_owner;

SELECT format('CREATE ROLE moni_app LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'app_pass') \gexec
SELECT format('CREATE ROLE moni_projection LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'projection_pass') \gexec
SQL

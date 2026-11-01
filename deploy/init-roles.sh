#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=app_pass="$APP_DB_PASSWORD" --set=projection_pass="$PROJECTION_DB_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE moni_app LOGIN PASSWORD %L', :'app_pass') \gexec
SELECT format('CREATE ROLE moni_projection LOGIN PASSWORD %L', :'projection_pass') \gexec
SQL

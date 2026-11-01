#!/usr/bin/env bash
# Sourced by offline backup/restore commands. Connection parameters come from
# PGHOST/PGPORT/PGUSER/PGDATABASE or libpq service/passfile settings, never argv.
set -euo pipefail
: "${PGDATABASE:?Set PGDATABASE explicitly to the intended database}"
if [ "${MAINTENANCE_MODE:-}" != true ]; then
  echo 'MAINTENANCE_MODE=true is required; stop and drain the application first.' >&2
  exit 1
fi
PSQL_BIN="${PG_BINDIR:+${PG_BINDIR}/}psql"
LOCK_WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/moni-maint.XXXXXXXX")
chmod 700 "$LOCK_WORKDIR"
LOCK_TOKEN="moni-maint-$(basename "$LOCK_WORKDIR")"
LOCK_BACKEND_PID=''
LOCK_CLIENT_PID=''
cleanup_maintenance_lock() {
  "$PSQL_BIN" -XqAt -v ON_ERROR_STOP=1 -v target_name="$LOCK_TOKEN" > /dev/null 2>&1 <<'SQL' || true
SELECT pg_terminate_backend(pid) FROM pg_stat_activity
WHERE application_name=:'target_name' AND datname=current_database() AND usename=current_user AND pid<>pg_backend_pid();
SQL

  if [ -n "$LOCK_CLIENT_PID" ]; then
    kill "$LOCK_CLIENT_PID" 2>/dev/null || true
    wait "$LOCK_CLIENT_PID" 2>/dev/null || true
  fi
  if [ -n "$LOCK_WORKDIR" ] && [ -d "$LOCK_WORKDIR" ]; then rm -rf "$LOCK_WORKDIR"; fi
}
trap cleanup_maintenance_lock EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
PGAPPNAME="$LOCK_TOKEN" "$PSQL_BIN" -XqAt -v ON_ERROR_STOP=1 -v expected_database="$PGDATABASE" -v lock_name="$LOCK_TOKEN" > "$LOCK_WORKDIR/status" 2> "$LOCK_WORKDIR/error" <<'SQL' &
SELECT current_database()=:'expected_database' AS target_matches \gset
\if :target_matches
\else
\quit 1
\endif
SET application_name TO :'lock_name';
SELECT pg_backend_pid();
DO $$BEGIN IF NOT pg_try_advisory_lock(782193402) THEN RAISE EXCEPTION 'service lock busy'; END IF; END$$;
SELECT 'LOCKED';
SELECT pg_sleep(86400);
SQL
LOCK_CLIENT_PID=$!
lock_acquired=false
for attempt in $(seq 1 100); do
  LOCK_BACKEND_PID=$(sed -n '1p' "$LOCK_WORKDIR/status")
  if [ "$(sed -n '2p' "$LOCK_WORKDIR/status")" = LOCKED ]; then lock_acquired=true; break; fi
  if ! kill -0 "$LOCK_CLIENT_PID" 2>/dev/null; then break; fi
  sleep 0.1
done
case "$LOCK_BACKEND_PID" in ''|*[!0-9]*) LOCK_BACKEND_PID='';; esac
if [ "$lock_acquired" != true ]; then
  echo 'Cannot acquire the database service lock; check connectivity and stop the active application.' >&2
  exit 1
fi

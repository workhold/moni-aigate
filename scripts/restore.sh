#!/usr/bin/env bash
set -euo pipefail
umask 077
if [ "$#" -ne 2 ]; then echo 'Usage: scripts/restore.sh BACKUP_DIRECTORY NEW_RECOVERED_SECRET_FILE' >&2; exit 1; fi
backup_dir=$1
recovered_secret=$2
: "${PGDATABASE:?Set PGDATABASE to the new empty recovery database}"
if [ "${CONFIRM_RESTORE_DATABASE:-}" != "$PGDATABASE" ]; then echo 'CONFIRM_RESTORE_DATABASE must equal the explicit PGDATABASE target.' >&2; exit 1; fi
if [ -e "$backup_dir/INCOMPLETE" ] || [ ! -f "$backup_dir/SHA256SUMS" ]; then echo 'Backup is incomplete.' >&2; exit 1; fi
if [ -e "$recovered_secret" ] || [ -e "$recovered_secret.recovery.env" ]; then echo 'Recovered material files already exist; refusing overwrite.' >&2; exit 1; fi
(
  cd "$backup_dir"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS >/dev/null
  else shasum -a 256 -c SHA256SUMS >/dev/null; fi
)
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source "$script_dir/lib/maintenance-lock.sh"
relations=$("$PSQL_BIN" -XqAt -v ON_ERROR_STOP=1 <<'SQL'
SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE c.relkind IN ('r','p','v','m','S','f') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%';
SQL
)
if [ "$relations" != 0 ]; then echo 'Recovery target must be a new empty database; refusing destructive restore.' >&2; exit 1; fi
# Build SQL completely before opening its transaction. If extraction fails,
# no SQL reaches the target. Restore and the persistent recovery latch commit together.
"${PG_BINDIR:+${PG_BINDIR}/}pg_restore" --exit-on-error --no-owner --no-acl --file="$LOCK_WORKDIR/restore.sql" "$backup_dir/database.dump"
cat >> "$LOCK_WORKDIR/restore.sql" <<'SQL'
SET search_path TO public;
WITH latched AS (
 UPDATE recovery_gate SET required=true,epoch=epoch+1,proposal_id=NULL,requester=NULL,approver=NULL,
 evidence_ref=NULL,reason=NULL,proposed_at=NULL,approved_at=NULL,latched_at=clock_timestamp()
 WHERE singleton RETURNING epoch
)
INSERT INTO audit_events(actor,action,target,sanitized_diff)
SELECT 'offline-restore','recovery.latched',epoch::text,jsonb_build_object('epoch',epoch,'source','verified-backup') FROM latched;
DO $$BEGIN IF NOT EXISTS(SELECT 1 FROM recovery_gate WHERE singleton AND required) THEN RAISE EXCEPTION 'persistent recovery latch missing'; END IF; END$$;
SQL
"$PSQL_BIN" -Xq -v ON_ERROR_STOP=1 --single-transaction --file="$LOCK_WORKDIR/restore.sql" > /dev/null
# Create new files without replacement. Do not execute backup material as shell code.
(set -o noclobber; cat "$backup_dir/key-material.env" > "$recovered_secret")
(set -o noclobber; printf 'RECOVERY_MODE=true\n' > "$recovered_secret.recovery.env")
chmod 600 "$recovered_secret" "$recovered_secret.recovery.env"
echo 'Database restored. Re-provision database roles, load the protected material and recovery.env override, and start only with RECOVERY_MODE=true.'

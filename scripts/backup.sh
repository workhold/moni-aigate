#!/usr/bin/env bash
set -euo pipefail
umask 077
if [ "$#" -ne 1 ]; then echo 'Usage: scripts/backup.sh NEW_BACKUP_DIRECTORY' >&2; exit 1; fi
: "${SECRET_MATERIAL_FILE:?Set SECRET_MATERIAL_FILE to the protected keyring and authentication-secret file}"
if [ ! -f "$SECRET_MATERIAL_FILE" ]; then echo 'Secret material file does not exist.' >&2; exit 1; fi
if ! grep -q '^AUTH_SECRET=' "$SECRET_MATERIAL_FILE" || ! grep -Eq '^MASTER_(KEY|KEYS_JSON)=' "$SECRET_MATERIAL_FILE"; then
  echo 'Protected material must include AUTH_SECRET and MASTER_KEY or MASTER_KEYS_JSON.' >&2
  exit 1
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source "$script_dir/lib/maintenance-lock.sh"
backup_dir=$1
mkdir -m 700 "$backup_dir"
touch "$backup_dir/INCOMPLETE"
backup_started=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
cp "$SECRET_MATERIAL_FILE" "$backup_dir/key-material.env"
chmod 600 "$backup_dir/key-material.env"
"${PG_BINDIR:+${PG_BINDIR}/}pg_dump" --format=custom --no-owner --no-acl --file="$backup_dir/database.dump"
server_version=$("$PSQL_BIN" -XqAt -v ON_ERROR_STOP=1 -c 'SHOW server_version')
{
  echo 'format=moni-aigate-offline-backup-v1'
  echo "started_at=$backup_started"
  echo "finished_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "postgresql=$server_version"
  echo 'recovery_mode_required=true'
  echo 'contains_plaintext_key_material=true'
} > "$backup_dir/manifest.txt"
(
  cd "$backup_dir"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum database.dump key-material.env manifest.txt > SHA256SUMS
  else shasum -a 256 database.dump key-material.env manifest.txt > SHA256SUMS; fi
)
rm "$backup_dir/INCOMPLETE"
echo 'Backup completed. Keep this protected bundle on encrypted storage and retain the referenced master keys.'

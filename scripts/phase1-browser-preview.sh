#!/usr/bin/env bash
set -Eeuo pipefail

# Isolated phase-one browser/deployment preview. The app and worker use a random
# schema on the dedicated 55439 PostgreSQL instance, never the Compose public
# database. Secrets stay in the private host cache and are never printed.
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
private=/Users/vchive/.cache/moni-phase1-browser
project=moni-phase1-browser
network=${project}-net
app=${project}-app
worker=${project}-worker
port=18081
export PATH="/opt/homebrew/bin:$PATH"
export DOCKER_CONTEXT=colima
mkdir -p "$private"
chmod 700 "$private"
umask 077
schema="phase1qa_$(date +%s)_$$"
app_pw=$(openssl rand -hex 24)
proj_pw=$(openssl rand -hex 24)
master_key=$(openssl rand -base64 32 | tr -d '\n')
auth_secret=$(openssl rand -base64 32 | tr -d '\n')
voucher_key=$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')
admin_pw="phase1-browser-$(openssl rand -hex 18)"
printf '%s\n' "$schema" >"$private/schema"
printf '%s\n' "$admin_pw" >"$private/admin-password"
chmod 600 "$private/schema" "$private/admin-password"
commit=$(git -C "$root" rev-parse HEAD)
image="moni-aigate:phase1-browser-${commit:0:12}"
compose_env="$private/compose.env"
cat >"$compose_env" <<EOF
POSTGRES_PASSWORD=$(openssl rand -hex 24)
APP_DB_PASSWORD=$(openssl rand -hex 24)
PROJECTION_DB_PASSWORD=$(openssl rand -hex 24)
MASTER_KEY_ID=primary
MASTER_KEY=$master_key
AUTH_SECRET=$auth_secret
PUBLIC_ORIGIN=http://127.0.0.1:${port}
COOKIE_SECURE=false
LISTEN_ADDR=0.0.0.0:8080
PORT=${port}
MONI_IMAGE=$image
RECOVERY_MODE=false
MFA_ENFORCE_PLATFORM=false
TRUSTED_PROXY_CIDRS=
AUTH_REGISTRATION_ENABLED=false
AUTH_PASSWORD_RESET_ENABLED=true
MAIL_TRANSPORT=disabled
VOUCHERS_ENABLED=true
VOUCHER_HMAC_ACTIVE_KEY=v1
VOUCHER_HMAC_KEYS={"v1":"$voucher_key"}
OPERATIONS_ALERTS_ENABLED=true
PAYMENTS_MODE=off
MEDIA_PROVIDER_PROFILE=business-qa
MEDIA_DETERMINISTIC_MOCK=true
GOPROXY=https://goproxy.cn,direct
EOF
runtime_env="$private/runtime.env"
cat >"$runtime_env" <<EOF
DATABASE_URL=postgres://moni_app:${app_pw}@host.docker.internal:55439/moni_test?sslmode=disable&search_path=${schema}
PROJECTION_DATABASE_URL=postgres://moni_projection:${proj_pw}@host.docker.internal:55439/moni_test?sslmode=disable&search_path=${schema}
MASTER_KEY=${master_key}
MASTER_KEY_ID=primary
MASTER_KEYS_JSON=
AUTH_SECRET=${auth_secret}
PUBLIC_ORIGIN=http://127.0.0.1:${port}
COOKIE_SECURE=false
MFA_ENFORCE_PLATFORM=false
TRUSTED_PROXY_CIDRS=
LISTEN_ADDR=0.0.0.0:8080
RECOVERY_MODE=false
MEDIA_PROVIDER_PROFILE=business-qa
MEDIA_DETERMINISTIC_MOCK=true
PROVIDER_CONNECT_TIMEOUT=5s
PROVIDER_HEADER_TIMEOUT=60s
PROVIDER_FIRST_EVENT_TIMEOUT=60s
PROVIDER_IDLE_TIMEOUT=60s
PROVIDER_PROGRESS_TIMEOUT=60s
AUTH_REGISTRATION_ENABLED=false
AUTH_PASSWORD_RESET_ENABLED=true
MAIL_TRANSPORT=disabled
VOUCHERS_ENABLED=true
VOUCHER_HMAC_ACTIVE_KEY=v1
VOUCHER_HMAC_KEYS={"v1":"$voucher_key"}
OPERATIONS_ALERTS_ENABLED=true
PAYMENTS_MODE=off
EOF
chmod 600 "$compose_env" "$runtime_env"
cleanup() {
  docker rm -f "$app" "$worker" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  if [[ "$schema" =~ ^phase1qa_[0-9]+_[0-9]+$ ]]; then
    psql 'postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' -v ON_ERROR_STOP=1 -c "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
docker rm -f "$app" "$worker" >/dev/null 2>&1 || true
docker network rm "$network" >/dev/null 2>&1 || true
psql 'postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' -v ON_ERROR_STOP=1 <<SQL
CREATE SCHEMA $schema;
ALTER ROLE moni_app LOGIN PASSWORD '$app_pw';
ALTER ROLE moni_projection LOGIN PASSWORD '$proj_pw';
SQL
export DATABASE_URL="postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable&search_path=$schema"
export MIGRATION_DATABASE_URL="$DATABASE_URL"
export MASTER_KEY="$master_key" MASTER_KEY_ID=primary MASTER_KEYS_JSON= AUTH_SECRET="$auth_secret"
export GOPROXY=https://goproxy.cn,direct
(
  cd "$root"
  go run ./cmd/moni-aigate migrate
  go run ./scripts/phase1-provision "$schema"
  printf '%s\n' "$admin_pw" | go run ./cmd/moni-aigate bootstrap --email admin@example.com --password-stdin
)
compose=(docker-compose --project-directory "$root" --project-name "$project" --env-file "$compose_env" --profile business-qa)
"${compose[@]}" config --quiet
"${compose[@]}" build
docker network create "$network" >/dev/null
docker run -d --name "$app" --network "$network" --network-alias app --add-host=host.docker.internal:host-gateway --env-file "$runtime_env" -p 127.0.0.1:${port}:8080 --restart unless-stopped --init "$image" serve >/dev/null
docker run -d --name "$worker" --network "$network" --network-alias worker --add-host=host.docker.internal:host-gateway --env-file "$runtime_env" --restart unless-stopped --init "$image" worker >/dev/null
printf 'started isolated phase-one app/worker on %s with dedicated schema %s\n' "$port" "$schema"
docker logs -f --tail=20 "$app"

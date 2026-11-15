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
export DOCKER_CONTEXT="${DOCKER_CONTEXT:-colima}"
mkdir -p "$private"
chmod 700 "$private"
umask 077
commit=$(git -C "$root" rev-parse HEAD)
source_tree=$(git -C "$root" rev-parse 'HEAD^{tree}')
if [[ -n "$(git -C "$root" status --porcelain)" ]]; then
  printf 'phase-one preview requires a clean source commit\n' >&2
  exit 2
fi
build_context=$(mktemp -d "$private/build-context.XXXXXX")
trap 'rm -rf "$build_context"' EXIT
git -C "$root" archive --format=tar "$commit" | tar -xf - -C "$build_context"
image="moni-aigate:phase1-browser-${commit:0:12}"
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
PHASE1_SOURCE_COMMIT=$commit
PHASE1_SOURCE_TREE=$source_tree
PHASE1_SOURCE_CLEAN=true
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
  rm -rf "$build_context"
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
  cd "$build_context"
  go run ./cmd/moni-aigate migrate
  go run ./scripts/phase1-provision "$schema"
  printf '%s\n' "$admin_pw" | go run ./cmd/moni-aigate bootstrap --email admin@example.com --password-stdin
)
compose=(docker-compose --project-directory "$build_context" --project-name "$project" --env-file "$compose_env" --profile business-qa)
"${compose[@]}" config --quiet
"${compose[@]}" build
docker network create "$network" >/dev/null
docker run -d --name "$app" --network "$network" --network-alias app --add-host=host.docker.internal:host-gateway --env-file "$runtime_env" -p 127.0.0.1:${port}:8080 --restart unless-stopped --init "$image" serve >/dev/null
docker run -d --name "$worker" --network "$network" --network-alias worker --add-host=host.docker.internal:host-gateway --env-file "$runtime_env" --restart unless-stopped --init "$image" worker >/dev/null
printf 'started isolated phase-one app/worker on %s with dedicated schema %s\n' "$port" "$schema"
docker logs --tail=20 "$app"
# The final runner deliberately stops and restarts these same containers. Keep
# this launcher alive while they exist so its EXIT trap cannot remove the schema.
# Only an explicit NotFound proves deletion; daemon/context failures must retain
# the isolated resources until the operator deliberately exits this launcher.
while true; do
  if inspection=$(docker inspect --format '{{.Id}}' "$app" 2>&1); then
    sleep 5
    continue
  fi
  case "$inspection" in
    *"No such object"*|*"No such container"*) break ;;
    *)
      printf 'Docker inspect unavailable; retaining isolated preview resources\n' >&2
      sleep 30
      ;;
  esac
done

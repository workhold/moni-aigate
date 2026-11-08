#!/usr/bin/env bash
set -Eeuo pipefail

# Disposable, non-browser deployment acceptance for the local Compose topology.
# The generated environment and database volume are removed when the script exits.

repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/moni-aigate-deployment-qa.XXXXXX")
env_file="$work_dir/.env"
password_file="$work_dir/admin-password"
project_name="moni-aigate-qa-${RANDOM}-${PPID}"
port="${DEPLOY_QA_PORT:-18080}"

if docker compose version >/dev/null 2>&1; then

  compose=(docker compose)
elif docker-compose version >/dev/null 2>&1; then
  compose=(docker-compose)
else
  echo "Docker Compose v2 or docker-compose is required" >&2
  exit 1
fi

rand_hex() {
  openssl rand -hex "$1"
}

rand_b64() {
  openssl rand -base64 "$1" | tr -d '\n'
}

run_compose() {
  "${compose[@]}" \
    --project-directory "$repo_dir" \
    --project-name "$project_name" \
    --env-file "$env_file" \
    "$@"
}

cleanup() {
  run_compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}

on_exit() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "deployment QA failed. Recent service state follows:" >&2
    run_compose ps >&2 || true
    run_compose logs --tail=80 postgres migrate provision app worker >&2 || true
  fi
  cleanup
  exit "$status"
}
trap on_exit EXIT

umask 077
cat >"$env_file" <<EOF
POSTGRES_PASSWORD=$(rand_hex 24)
APP_DB_PASSWORD=$(rand_hex 24)
PROJECTION_DB_PASSWORD=$(rand_hex 24)
MASTER_KEY_ID=primary
MASTER_KEY=$(rand_b64 32)
AUTH_SECRET=$(rand_b64 32)
PUBLIC_ORIGIN=http://127.0.0.1:${port}
COOKIE_SECURE=false
LISTEN_ADDR=0.0.0.0:8080
PORT=${port}
MONI_IMAGE=moni-aigate:qa-${project_name}
RECOVERY_MODE=false
MFA_ENFORCE_PLATFORM=false
TRUSTED_PROXY_CIDRS=
AUTH_REGISTRATION_ENABLED=false
AUTH_PASSWORD_RESET_ENABLED=true
MAIL_TRANSPORT=disabled
VOUCHERS_ENABLED=false
OPERATIONS_ALERTS_ENABLED=false
PAYMENTS_MODE=off
EOF
printf 'deployment-qa-admin-password-%s\n' "$(rand_hex 12)" >"$password_file"

echo "checking Compose rendering"
run_compose config --quiet
echo "building image"
run_compose build

echo "starting PostgreSQL"
run_compose up -d postgres

echo "running migrations twice"
run_compose run --rm migrate
run_compose run --rm migrate

echo "provisioning roles twice"
run_compose run --rm provision
run_compose run --rm provision

echo "bootstrapping first administrator"
run_compose run --rm -T app bootstrap --email admin@example.com --password-stdin <"$password_file"

echo "rejecting a repeated bootstrap"
if run_compose run --rm -T app bootstrap --email second@example.com --password-stdin <"$password_file"; then
  echo "bootstrap unexpectedly accepted a second administrator" >&2
  exit 1
fi

echo "starting app and worker"
run_compose up -d app worker

wait_for_ready() {
  local body
  for _ in $(seq 1 60); do
    if body=$(curl --silent --show-error --fail --max-time 3 "http://127.0.0.1:${port}/health/ready"); then
      python3 -c 'import json, sys; value=json.load(sys.stdin); assert value["status"] == "ready"; assert value["recovery_mode"] is False' <<<"$body"
      return 0
    fi
    sleep 2
  done
  echo "readiness did not become healthy" >&2
  return 1
}

check_processes() {
  local service id state
  for service in app worker; do
    id=$(run_compose ps -q "$service")
    [ -n "$id" ]
    state=$(docker inspect --format '{{.State.Status}}' "$id")
    [ "$state" = running ]
  done
}

echo "checking live and ready endpoints"
curl --silent --show-error --fail --max-time 3 "http://127.0.0.1:${port}/health/live" >/dev/null
wait_for_ready
check_processes

echo "restarting app and worker"
run_compose restart app worker
wait_for_ready
check_processes

echo "restarting PostgreSQL and waiting for app recovery"
run_compose restart postgres
wait_for_ready
check_processes

echo "deployment QA passed"

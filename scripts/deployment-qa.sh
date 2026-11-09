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
business="${DEPLOY_QA_BUSINESS:-false}"

case "$business" in
  true|false) ;;
  *) echo "DEPLOY_QA_BUSINESS must be true or false" >&2; exit 1 ;;
esac

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
  local -a profile_args=()
  if [ "$business" = true ]; then
    profile_args+=(--profile business-qa)
  fi
  "${compose[@]}" \
    --project-directory "$repo_dir" \
    --project-name "$project_name" \
    --env-file "$env_file" \
    "${profile_args[@]}" \
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
if [ "$business" = true ]; then
  run_compose up -d app worker qa-provider
else
  run_compose up -d app worker
fi

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

wait_for_live() {
  for _ in $(seq 1 60); do
    if curl --silent --show-error --fail --max-time 3 "http://127.0.0.1:${port}/health/live" >/dev/null; then
      return 0
    fi
    sleep 2
  done
  echo "liveness did not become healthy" >&2
  return 1
}

check_processes() {
  local service id state
  local services=(app worker)
  if [ "$business" = true ]; then
    services+=(qa-provider)
  fi
  for service in "${services[@]}"; do
    id=$(run_compose ps -q "$service")
    [ -n "$id" ]
    state=$(docker inspect --format '{{.State.Status}}' "$id")
    [ "$state" = running ]
  done
}

echo "checking live and ready endpoints"
wait_for_live
wait_for_ready
check_processes

if [ "$business" = true ]; then
  echo "provisioning disposable business fixture through HTTP"
  BUSINESS_QA_BASE_URL="http://127.0.0.1:${port}" \
  BUSINESS_QA_ADMIN_PASSWORD="$(cat "$password_file")" \
  BUSINESS_QA_OUTPUT_DIR="$work_dir" \
    "$repo_dir/scripts/deployment-business-provision.sh" >"$work_dir/business.env"
  # The provisioning output contains only the disposable model, tenant and
  # customer API key. It is consumed in-process and removed by cleanup.
  set -a
  . "$work_dir/business.env"
  set +a
  BUSINESS_QA_BASE_URL="http://127.0.0.1:${port}" \
  BUSINESS_QA_ADMIN_PASSWORD="$(cat "$password_file")" \
  BUSINESS_QA_MODEL="$BUSINESS_QA_MODEL" \
  BUSINESS_QA_API_KEY="$BUSINESS_QA_API_KEY" \
  BUSINESS_QA_IMAGE_REQUEST_FILE="$BUSINESS_QA_IMAGE_REQUEST_FILE" \
  BUSINESS_QA_TTS_REQUEST_FILE="$BUSINESS_QA_TTS_REQUEST_FILE" \
  BUSINESS_QA_TASK_REQUEST_ID="$BUSINESS_QA_TASK_REQUEST_ID" \
  BUSINESS_QA_TASK_RESERVATION_ID="$BUSINESS_QA_TASK_RESERVATION_ID" \
  BUSINESS_QA_TASK_ATTEMPT_ID="$BUSINESS_QA_TASK_ATTEMPT_ID" \
  BUSINESS_QA_TASK_SOURCE_ID="$BUSINESS_QA_TASK_SOURCE_ID" \
  BUSINESS_QA_TASK_MODEL_ID="$BUSINESS_QA_TASK_MODEL_ID" \
  BUSINESS_QA_TASK_PROFILE_VERSION="$BUSINESS_QA_TASK_PROFILE_VERSION" \
  BUSINESS_QA_TASK_SCHEMA_ID="$BUSINESS_QA_TASK_SCHEMA_ID" \
  BUSINESS_QA_TASK_SCHEMA_DIGEST="$BUSINESS_QA_TASK_SCHEMA_DIGEST" \
  BUSINESS_QA_REALTIME_URL="$BUSINESS_QA_REALTIME_URL" \
  BUSINESS_QA_REALTIME_ORIGIN="$BUSINESS_QA_REALTIME_ORIGIN" \
  BUSINESS_QA_FAIL_ON_SKIP=true \
    "$repo_dir/scripts/deployment-business-qa.sh"
fi

echo "restarting app and worker"
run_compose restart app worker
wait_for_ready
check_processes

echo "restarting PostgreSQL and waiting for app recovery"
run_compose restart postgres
wait_for_ready
check_processes

echo "deployment QA passed"

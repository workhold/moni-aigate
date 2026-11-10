#!/usr/bin/env bash
set -Eeuo pipefail

# Current-MVP HTTP/WS smoke for an already deployed disposable environment.
# This entry point deliberately does not bootstrap identities, provision
# catalog/billing facts, or connect to a database. It only forwards existing
# current-architecture references to deployment-business-qa.sh.

readonly EXIT_FAIL=1
readonly EXIT_USAGE=2
readonly REQUIRED_TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd)
runner="$repo_dir/scripts/deployment-business-qa.sh"

usage() {
  cat <<'EOF'
Usage:
  TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  BROWSER_QA_BASE_URL=http://127.0.0.1:18080 \
  BROWSER_QA_API_KEY=mg_... \
  BROWSER_QA_MODEL=model-name \
  BROWSER_QA_REALTIME_URL=ws://127.0.0.1:18080/v1/realtime \
  scripts/browser-qa/current-mvp-smoke.sh

Required inputs are an existing customer API key, an authorized current model,
and an existing Realtime endpoint. The endpoint is checked by the current
deployment-business-qa runner; the sidecar does not create tenants, users,
keys, routes, prices, wallet credit, tasks, or provider facts.

Optional inputs are BROWSER_QA_REALTIME_ORIGIN,
BROWSER_QA_REALTIME_MODEL, BROWSER_QA_REALTIME_INSECURE_TLS=true,
BROWSER_QA_IMAGE_REQUEST_FILE, BROWSER_QA_TTS_REQUEST_FILE, the
BROWSER_QA_TASK_* references, and BROWSER_QA_TIMEOUT. Optional media/task
checks are passed through and remain explicit SKIP checks when omitted.

The only accepted TEST_DATABASE_URL is the local 55439 moni_test URL shown
above. No 5432 database or historical browser fixture is accepted.
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
  exit 0
fi
if [ "$#" -ne 0 ]; then
  usage >&2
  exit "$EXIT_USAGE"
fi

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit "$EXIT_USAGE"; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit "$EXIT_USAGE"; }
[ -x "$runner" ] || { echo "current deployment-business-qa runner is missing" >&2; exit "$EXIT_USAGE"; }

if [ "${TEST_DATABASE_URL:-}" != "$REQUIRED_TEST_DATABASE_URL" ]; then
  echo "TEST_DATABASE_URL must be exactly the local 55439 moni_test URL" >&2
  exit "$EXIT_USAGE"
fi

base_url=${BROWSER_QA_BASE_URL:-http://127.0.0.1:18080}
if ! python3 - "$base_url" <<'PY'
import sys
from urllib.parse import urlsplit

value = urlsplit(sys.argv[1])
if value.scheme not in ("http", "https") or not value.netloc or value.query or value.fragment:
    raise SystemExit("BROWSER_QA_BASE_URL must be an absolute HTTP(S) URL without query or fragment")
if value.port == 5432:
    raise SystemExit("BROWSER_QA_BASE_URL cannot use the PostgreSQL port 5432")
PY
then
  echo "invalid BROWSER_QA_BASE_URL" >&2
  exit "$EXIT_USAGE"
fi

api_key=${BROWSER_QA_API_KEY:-}
model=${BROWSER_QA_MODEL:-}
realtime_url=${BROWSER_QA_REALTIME_URL:-}
[ -n "$api_key" ] || { echo "BROWSER_QA_API_KEY is required" >&2; exit "$EXIT_USAGE"; }
[ -n "$model" ] || { echo "BROWSER_QA_MODEL is required" >&2; exit "$EXIT_USAGE"; }
[ -n "$realtime_url" ] || { echo "BROWSER_QA_REALTIME_URL is required" >&2; exit "$EXIT_USAGE"; }

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/moni-aigate-browser-qa.XXXXXX")
output="$work_dir/output.log"
cleanup() {
  local status=$?
  if [ "$status" -ne 0 ] && [ -s "$output" ]; then
    cat "$output" >&2
  fi
  rm -rf "$work_dir"
  exit "$status"
}
trap cleanup EXIT

# Start from a clean BUSINESS_QA namespace. In particular, inherited admin or
# provisioning values must not silently expand this read-only scoped sidecar.
unset BUSINESS_QA_ADMIN_EMAIL BUSINESS_QA_ADMIN_PASSWORD BUSINESS_QA_ADMIN_PASSWORD_FILE \
  BUSINESS_QA_BASE_URL BUSINESS_QA_API_KEY BUSINESS_QA_MODEL BUSINESS_QA_REALTIME_URL \
  BUSINESS_QA_REALTIME_MODEL BUSINESS_QA_REALTIME_ORIGIN BUSINESS_QA_REALTIME_INSECURE_TLS \
  BUSINESS_QA_IMAGE_REQUEST_FILE BUSINESS_QA_TTS_REQUEST_FILE BUSINESS_QA_FAIL_ON_SKIP \
  BUSINESS_QA_TASK_REQUEST_ID BUSINESS_QA_TASK_RESERVATION_ID BUSINESS_QA_TASK_ATTEMPT_ID \
  BUSINESS_QA_TASK_SOURCE_ID BUSINESS_QA_TASK_MODEL_ID BUSINESS_QA_TASK_PROFILE_VERSION \
  BUSINESS_QA_TASK_SCHEMA_ID BUSINESS_QA_TASK_SCHEMA_DIGEST BUSINESS_QA_TASK_MAX_ARTIFACT_BYTES \
  BUSINESS_QA_TIMEOUT BUSINESS_QA_READY_ATTEMPTS BUSINESS_QA_TASK_POLL_SECONDS \
  BUSINESS_QA_TASK_POLL_ATTEMPTS

set +e
BUSINESS_QA_BASE_URL="$base_url" \
BUSINESS_QA_API_KEY="$api_key" \
BUSINESS_QA_MODEL="$model" \
BUSINESS_QA_REALTIME_URL="$realtime_url" \
BUSINESS_QA_REALTIME_MODEL="${BROWSER_QA_REALTIME_MODEL:-$model}" \
BUSINESS_QA_REALTIME_ORIGIN="${BROWSER_QA_REALTIME_ORIGIN:-}" \
BUSINESS_QA_REALTIME_INSECURE_TLS="${BROWSER_QA_REALTIME_INSECURE_TLS:-false}" \
BUSINESS_QA_IMAGE_REQUEST_FILE="${BROWSER_QA_IMAGE_REQUEST_FILE:-}" \
BUSINESS_QA_TTS_REQUEST_FILE="${BROWSER_QA_TTS_REQUEST_FILE:-}" \
BUSINESS_QA_TASK_REQUEST_ID="${BROWSER_QA_TASK_REQUEST_ID:-}" \
BUSINESS_QA_TASK_RESERVATION_ID="${BROWSER_QA_TASK_RESERVATION_ID:-}" \
BUSINESS_QA_TASK_ATTEMPT_ID="${BROWSER_QA_TASK_ATTEMPT_ID:-}" \
BUSINESS_QA_TASK_SOURCE_ID="${BROWSER_QA_TASK_SOURCE_ID:-}" \
BUSINESS_QA_TASK_MODEL_ID="${BROWSER_QA_TASK_MODEL_ID:-}" \
BUSINESS_QA_TASK_PROFILE_VERSION="${BROWSER_QA_TASK_PROFILE_VERSION:-}" \
BUSINESS_QA_TASK_SCHEMA_ID="${BROWSER_QA_TASK_SCHEMA_ID:-}" \
BUSINESS_QA_TASK_SCHEMA_DIGEST="${BROWSER_QA_TASK_SCHEMA_DIGEST:-}" \
BUSINESS_QA_TASK_MAX_ARTIFACT_BYTES="${BROWSER_QA_TASK_MAX_ARTIFACT_BYTES:-1048576}" \
BUSINESS_QA_TIMEOUT="${BROWSER_QA_TIMEOUT:-15}" \
BUSINESS_QA_READY_ATTEMPTS="${BROWSER_QA_READY_ATTEMPTS:-30}" \
BUSINESS_QA_TASK_POLL_SECONDS="${BROWSER_QA_TASK_POLL_SECONDS:-2}" \
BUSINESS_QA_TASK_POLL_ATTEMPTS="${BROWSER_QA_TASK_POLL_ATTEMPTS:-30}" \
BUSINESS_QA_FAIL_ON_SKIP=false \
  "$runner" >"$output" 2>&1
runner_status=$?
set -e

cat "$output"
grep -q '^PASS text$' "$output" || { echo "required HTTP text smoke did not pass" >&2; exit "$EXIT_FAIL"; }
grep -q '^PASS Realtime WebSocket$' "$output" || { echo "required Realtime WebSocket smoke did not pass" >&2; exit "$EXIT_FAIL"; }

case "$runner_status" in
  0|3) echo "PASS current MVP HTTP/WS smoke (optional media/task checks may be SKIP)" ;;
  *) exit "$EXIT_FAIL" ;;
esac

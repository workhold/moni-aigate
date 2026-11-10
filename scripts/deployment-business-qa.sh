#!/usr/bin/env bash
set -Eeuo pipefail

# Smoke an already deployed current-architecture environment. This script
# consumes existing commercial references and never creates fixtures, wallet
# credit, routes, API keys, task references, or providers.

readonly EXIT_FAIL=1
readonly EXIT_USAGE=2
readonly EXIT_SKIP=3
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
skip_count=0

usage() {
  cat <<'EOF'
Usage:
  BUSINESS_QA_BASE_URL=http://127.0.0.1:18080 \
  BUSINESS_QA_API_KEY=mg_... BUSINESS_QA_MODEL=model-name \
  scripts/deployment-business-qa.sh

Core inputs:
  BUSINESS_QA_BASE_URL       Default http://127.0.0.1:${DEPLOY_QA_PORT:-18080}
  BUSINESS_QA_API_KEY        Existing customer key. Missing means SKIP.
  BUSINESS_QA_MODEL          Public text model, or first /v1/models item.

Optional administrator check:
  BUSINESS_QA_ADMIN_EMAIL    Default admin@example.com
  BUSINESS_QA_ADMIN_PASSWORD
  BUSINESS_QA_ADMIN_PASSWORD_FILE

Optional image/TTS checks use complete, already-approved request bodies:
  BUSINESS_QA_IMAGE_REQUEST_FILE  JSON for POST /v1/images/generations
  BUSINESS_QA_TTS_REQUEST_FILE    JSON for POST /v1/audio/speech

Optional task check requires every existing reference:
  BUSINESS_QA_TASK_REQUEST_ID BUSINESS_QA_TASK_RESERVATION_ID
  BUSINESS_QA_TASK_ATTEMPT_ID BUSINESS_QA_TASK_SOURCE_ID
  BUSINESS_QA_TASK_MODEL_ID BUSINESS_QA_TASK_PROFILE_VERSION
  BUSINESS_QA_TASK_SCHEMA_ID BUSINESS_QA_TASK_SCHEMA_DIGEST
  BUSINESS_QA_TASK_MAX_ARTIFACT_BYTES (default 1048576)

Optional Realtime check:
  BUSINESS_QA_REALTIME_URL       ws:// or wss:// URL
  BUSINESS_QA_REALTIME_MODEL     Authorized model if URL has no model query
  BUSINESS_QA_REALTIME_ORIGIN    Default origin derived from BASE_URL
  BUSINESS_QA_REALTIME_INSECURE_TLS=true for an explicit disposable TLS test

Tuning:
  BUSINESS_QA_TIMEOUT (default 15)
  BUSINESS_QA_READY_ATTEMPTS (default 30)
  BUSINESS_QA_TASK_POLL_SECONDS (default 2)
  BUSINESS_QA_TASK_POLL_ATTEMPTS (default 30)
  BUSINESS_QA_FAIL_ON_SKIP=true turns SKIP into exit 1

Exit codes: 0 all enabled checks passed, 1 enabled check failed, 2 invalid
local configuration, 3 checks finished with explicit SKIP(s). No skipped
capability is reported as passed. A configured provider-unavailable response
is SKIP, while other configured errors fail.
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then usage; exit 0; fi
if [ "${1:-}" = "--version" ]; then echo "deployment-business-qa 2"; exit 0; fi
if [ "$#" -ne 0 ]; then usage >&2; exit "$EXIT_USAGE"; fi
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit "$EXIT_USAGE"; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit "$EXIT_USAGE"; }

base_url=${BUSINESS_QA_BASE_URL:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}
base_url=${base_url%/}
admin_email=${BUSINESS_QA_ADMIN_EMAIL:-admin@example.com}
timeout=${BUSINESS_QA_TIMEOUT:-15}
ready_attempts=${BUSINESS_QA_READY_ATTEMPTS:-30}
task_poll_seconds=${BUSINESS_QA_TASK_POLL_SECONDS:-2}
task_poll_attempts=${BUSINESS_QA_TASK_POLL_ATTEMPTS:-30}
fail_on_skip=${BUSINESS_QA_FAIL_ON_SKIP:-false}

positive_integer() { [[ "$1" =~ ^[1-9][0-9]*$ ]]; }
if ! positive_integer "$timeout" || ! positive_integer "$ready_attempts" || \
  ! positive_integer "$task_poll_seconds" || ! positive_integer "$task_poll_attempts"; then
  echo "timeout and attempt settings must be positive integers" >&2
  exit "$EXIT_USAGE"
fi
case "$fail_on_skip" in true|false) ;; *) echo "BUSINESS_QA_FAIL_ON_SKIP must be true or false" >&2; exit "$EXIT_USAGE" ;; esac

if ! origin=$(python3 - "$base_url" <<'PY'
import sys
from urllib.parse import urlsplit
p = urlsplit(sys.argv[1])
if p.scheme not in ("http", "https") or not p.netloc or p.query or p.fragment:
    raise SystemExit("BASE_URL must be an absolute HTTP(S) URL without query or fragment")
print(f"{p.scheme}://{p.netloc}")
PY
); then
  echo "invalid BUSINESS_QA_BASE_URL" >&2
  exit "$EXIT_USAGE"
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/moni-aigate-business-qa.XXXXXX")
cookie_jar="$work_dir/cookies.txt"
admin_password=${BUSINESS_QA_ADMIN_PASSWORD:-}
if [ -z "$admin_password" ] && [ -n "${BUSINESS_QA_ADMIN_PASSWORD_FILE:-}" ]; then
  [ -r "$BUSINESS_QA_ADMIN_PASSWORD_FILE" ] || { echo "administrator password file is not readable" >&2; exit "$EXIT_USAGE"; }
  admin_password=$(cat "$BUSINESS_QA_ADMIN_PASSWORD_FILE")
fi

last_name="" last_status="" last_body="" last_headers=""
cleanup() {
  local status=$?
  if [ "$status" -eq 0 ] && [ "$skip_count" -gt 0 ]; then
    [ "$fail_on_skip" = true ] && status=$EXIT_FAIL || status=$EXIT_SKIP
  fi
  rm -f "$cookie_jar" "$work_dir/login.json" 2>/dev/null || true
  if [ "$status" -ne 0 ]; then
    echo "business QA exit=$status; diagnostics retained at $work_dir" >&2
    if [ -n "$last_name" ]; then
      echo "last request: $last_name HTTP $last_status" >&2
      [ -s "$last_body" ] && python3 - "$last_body" <<'PY' >&2 || true
import pathlib, sys
print(pathlib.Path(sys.argv[1]).read_bytes()[:4096].decode("utf-8", errors="replace"))
PY
    fi
  else
    rm -rf "$work_dir"
  fi
  exit "$status"
}
trap cleanup EXIT

fail() { echo "FAIL $*" >&2; exit "$EXIT_FAIL"; }
skip_check() { skip_count=$((skip_count + 1)); echo "SKIP $1: $2"; }

request() {
  local name=$1 method=$2 path=$3 body_file=""
  if [ "$#" -ge 4 ]; then body_file=$4; shift 4; else shift 3; fi
  local body="$work_dir/${name}.body" headers="$work_dir/${name}.headers"
  local -a args=(--silent --show-error --max-time "$timeout" -D "$headers" -o "$body" -w '%{http_code}' -X "$method" -b "$cookie_jar" -c "$cookie_jar" -H 'Accept: application/json')
  [ -z "$body_file" ] || args+=(-H 'Content-Type: application/json' --data-binary "@$body_file")
  while [ "$#" -gt 0 ]; do args+=(-H "$1"); shift; done
  if ! last_status=$(curl "${args[@]}" "$base_url$path"); then last_status=000; fi
  last_name=$name; last_body=$body; last_headers=$headers
}
expect_status() {
  [ "$last_status" = "$1" ] || fail "$2 returned HTTP $last_status, expected $1. See $last_body"
}
json_value() {
  python3 - "$1" "$2" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
for part in sys.argv[2].split("."):
    if not isinstance(value, dict) or part not in value: raise SystemExit(1)
    value = value[part]
if isinstance(value, bool): print("true" if value else "false")
elif value is None: print("null")
else: print(value)
PY
}
json_value_or_empty() { json_value "$1" "$2" 2>/dev/null || true; }
json_error_code() {
  python3 - "$1" <<'PY'
import json, sys
try: value = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception: raise SystemExit(0)
error = value.get("error") if isinstance(value, dict) else None
if isinstance(error, dict):
    for key in ("code", "type"):
        if isinstance(error.get(key), str): print(error[key]); raise SystemExit(0)
PY
}
provider_unavailable() {
  local code; code=$(json_error_code "$last_body")
  case "$last_status:$code" in
    502:provider_error|502:media_provider_error|502:provider_unavailable|502:unavailable|\
    503:provider_error|503:media_provider_error|503:provider_unavailable|503:unavailable|\
    504:provider_error|504:media_provider_error|504:provider_unavailable|504:unavailable) return 0 ;;
  esac
  return 1
}
random_key() { python3 - <<'PY'
import uuid
print("deployment-business-qa-" + uuid.uuid4().hex)
PY
}
json_model_from_list() {
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
items = value.get("data")
if not isinstance(items, list): raise SystemExit("/v1/models did not return data")
requested = sys.argv[2]
if requested:
    if any(isinstance(x, dict) and x.get("id") == requested for x in items): print(requested); raise SystemExit(0)
    raise SystemExit("requested model is absent from /v1/models")
for item in items:
    if isinstance(item, dict) and isinstance(item.get("id"), str) and item["id"]: print(item["id"]); raise SystemExit(0)
raise SystemExit("/v1/models returned no authorized model")
PY
}
valid_json_file() {
  python3 - "$1" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
if not path.is_file() or not path.stat().st_size: raise SystemExit(1)
json.load(path.open(encoding="utf-8"))
PY
}
validate_chat() {
  python3 - "$1" <<'PY'
import json, sys
v = json.load(open(sys.argv[1], encoding="utf-8"))
choices = v.get("choices")
if not isinstance(choices, list) or not choices or not isinstance(v.get("model"), str) or not v["model"]: raise SystemExit(1)
message = choices[0].get("message") if isinstance(choices[0], dict) else None
if not isinstance(message, dict) or not isinstance(message.get("content"), str) or not message["content"]: raise SystemExit(1)
PY
}
validate_image() {
  python3 - "$1" <<'PY'
import base64, json, struct, sys
v = json.load(open(sys.argv[1], encoding="utf-8"))
d = v.get("data")
if v.get("status") != "completed" or not isinstance(v.get("id"), str) or not v["id"] or not isinstance(d, list) or not d or not isinstance(d[0], dict): raise SystemExit(1)
item = d[0]
if item.get("mime_type") != "image/png" or item.get("width") != 256 or item.get("height") != 256: raise SystemExit(1)
try: raw = base64.b64decode(item["b64_json"], validate=True)
except (KeyError, ValueError): raise SystemExit(1)
if len(raw) < 24 or raw[:8] != b"\x89PNG\r\n\x1a\n" or raw[12:16] != b"IHDR": raise SystemExit(1)
width, height = struct.unpack(">II", raw[16:24])
if (width, height) != (256, 256): raise SystemExit(1)
PY
}
validate_wav() {
  python3 - "$1" <<'PY'
import pathlib, struct, sys
raw = pathlib.Path(sys.argv[1]).read_bytes()
if len(raw) < 44 or raw[:4] != b"RIFF" or raw[8:12] != b"WAVE" or raw[12:16] != b"fmt " or raw[36:40] != b"data": raise SystemExit(1)
if struct.unpack_from("<I", raw, 40)[0] != len(raw) - 44: raise SystemExit(1)
PY
}
expect_error_code() {
  local code
  code=$(json_error_code "$last_body")
  [ "$last_status" = "$1" ] && [ "$code" = "$2" ] || fail "$3 returned HTTP $last_status code '$code', expected HTTP $1 code '$2'"
}
expect_header() {
  grep -Eiq "^${1}: *${2}[[:space:]]*$" "$last_headers" || fail "$3 response did not include ${1}: ${2}"
}

echo "checking live and ready at $base_url"
ready=0
for _ in $(seq 1 "$ready_attempts"); do
  request health-live GET /health/live
  if [ "$last_status" = 200 ]; then
    request health-ready GET /health/ready
    if [ "$last_status" = 200 ] && python3 - "$last_body" <<'PY'
import json, sys
v = json.load(open(sys.argv[1], encoding="utf-8"))
raise SystemExit(0 if v.get("status") == "ready" and v.get("recovery_mode") is False else 1)
PY
    then ready=1; break; fi
  fi
  sleep 2
done
[ "$ready" -eq 1 ] || fail "health endpoints did not reach ready state"
echo "PASS health"

if [ -n "$admin_password" ]; then
  login_body="$work_dir/login.json"
  python3 - "$login_body" "$admin_email" "$admin_password" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({"email": sys.argv[2], "password": sys.argv[3]}, separators=(",", ":")))
PY
  request login POST /api/auth/login "$login_body" "Origin: $origin"
  expect_status 200 "administrator login"
  [ "$(json_value_or_empty "$last_body" data.mfa_required)" != true ] || fail "administrator login requires MFA"
  [ "$(json_value_or_empty "$last_body" data.enrollment_required)" != true ] || fail "administrator login requires MFA enrollment"
  request me GET /api/me "" "Origin: $origin"; expect_status 200 "session lookup"
  csrf=$(json_value_or_empty "$last_body" data.csrf_token)
  [ -n "$csrf" ] && [ "$csrf" != null ] || fail "session did not return CSRF token"
  request platform-models GET /api/platform/models "" "Origin: $origin"; expect_status 200 "platform model directory"
  reauth_body="$work_dir/reauth.json"
  python3 - "$reauth_body" "$admin_password" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({"password": sys.argv[2]}, separators=(",", ":")))
PY
  request csrf-missing POST /api/auth/reauth "$reauth_body" "Origin: $origin"
  expect_error_code 403 csrf_invalid "missing CSRF token"
  request wrong-origin POST /api/auth/reauth "$reauth_body" "Origin: http://wrong-origin.invalid" "X-CSRF-Token: $csrf"
  expect_error_code 403 csrf_invalid "wrong Origin with CSRF token"
  echo "PASS administrator authentication"
else
  skip_check "administrator authentication" "no administrator password supplied"
fi

api_key=${BUSINESS_QA_API_KEY:-}
model=${BUSINESS_QA_MODEL:-}
if [ -z "$api_key" ]; then
  skip_check text "no BUSINESS_QA_API_KEY, so no customer or provider call was attempted"
else
  request api-models GET /v1/models "" "Authorization: Bearer $api_key"; expect_status 200 "API-key model directory"
  api_models_body=$last_body
  request invalid-api-key GET /v1/models "" "Authorization: Bearer ${api_key}invalid"
  expect_error_code 401 invalid_credentials "invalid customer API key"
  if ! model=$(json_model_from_list "$api_models_body" "$model"); then fail "unable to choose an authorized model"; fi
  chat_body="$work_dir/chat.json"
  python3 - "$chat_body" "$model" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({"model": sys.argv[2], "messages": [{"role": "user", "content": "smoke"}], "max_tokens": 1, "stream": False}, separators=(",", ":")))
PY
  request text-chat POST /v1/chat/completions "$chat_body" "Authorization: Bearer $api_key" "Idempotency-Key: $(random_key)"
  if provider_unavailable; then skip_check text "configured API returned provider-unavailable HTTP $last_status"
  else expect_status 200 "text chat completion"; validate_chat "$last_body" || fail "text response shape is invalid"; echo "PASS text"; fi
fi

run_media_check() {
  local label=$1 env_name=$2 endpoint=$3 validator=$4
  local body_file=${!env_name:-}
  if [ -z "$body_file" ]; then skip_check "$label" "no $env_name, so no media provider call was attempted"; return; fi
  [ -n "$api_key" ] || { skip_check "$label" "BUSINESS_QA_API_KEY is required"; return; }
  [ -r "$body_file" ] || fail "$env_name is not readable: $body_file"
  valid_json_file "$body_file" || fail "$env_name is not valid JSON"
  request "$label" POST "$endpoint" "$body_file" "Authorization: Bearer $api_key" "Idempotency-Key: $(random_key)"
  if provider_unavailable; then skip_check "$label" "configured API returned provider-unavailable HTTP $last_status"; return; fi
  expect_status 200 "$label"; "$validator" "$last_body" || fail "$label response validation failed"; echo "PASS $label"
}
run_media_check image BUSINESS_QA_IMAGE_REQUEST_FILE /v1/images/generations validate_image
run_media_check tts BUSINESS_QA_TTS_REQUEST_FILE /v1/audio/speech validate_wav

task_names=(BUSINESS_QA_TASK_REQUEST_ID BUSINESS_QA_TASK_RESERVATION_ID BUSINESS_QA_TASK_ATTEMPT_ID BUSINESS_QA_TASK_SOURCE_ID BUSINESS_QA_TASK_MODEL_ID BUSINESS_QA_TASK_PROFILE_VERSION BUSINESS_QA_TASK_SCHEMA_ID BUSINESS_QA_TASK_SCHEMA_DIGEST)
task_missing=()
for name in "${task_names[@]}"; do [ -n "${!name:-}" ] || task_missing+=("$name"); done
run_task_check() {
  [ -n "$api_key" ] || { skip_check "async task" "BUSINESS_QA_API_KEY is required"; return; }
  if [ "${#task_missing[@]}" -gt 0 ]; then skip_check "async task" "missing commercial references: ${task_missing[*]}"; return; fi
  local body="$work_dir/task.json" idem="$(random_key)" max_artifact=${BUSINESS_QA_TASK_MAX_ARTIFACT_BYTES:-1048576}
  positive_integer "$max_artifact" || fail "BUSINESS_QA_TASK_MAX_ARTIFACT_BYTES must be positive"
  python3 - "$body" "$max_artifact" <<'PY'
import json, os, pathlib, sys
v = {"request_id": os.environ["BUSINESS_QA_TASK_REQUEST_ID"], "reservation_id": os.environ["BUSINESS_QA_TASK_RESERVATION_ID"], "attempt_id": os.environ["BUSINESS_QA_TASK_ATTEMPT_ID"], "source_id": os.environ["BUSINESS_QA_TASK_SOURCE_ID"], "model_id": os.environ["BUSINESS_QA_TASK_MODEL_ID"], "profile_version": os.environ["BUSINESS_QA_TASK_PROFILE_VERSION"], "schema_id": os.environ["BUSINESS_QA_TASK_SCHEMA_ID"], "schema_digest": os.environ["BUSINESS_QA_TASK_SCHEMA_DIGEST"], "max_artifact_bytes": int(sys.argv[2])}
pathlib.Path(sys.argv[1]).write_text(json.dumps(v, separators=(",", ":")))
PY
  request task-create POST /v1/tasks "$body" "Authorization: Bearer $api_key" "Idempotency-Key: $idem"; expect_status 202 "task creation"
  local task_id; task_id=$(json_value "$last_body" id) || fail "task creation omitted id"
  request task-replay POST /v1/tasks "$body" "Authorization: Bearer $api_key" "Idempotency-Key: $idem"; expect_status 202 "task replay"
  [ "$(json_value "$last_body" id)" = "$task_id" ] || fail "task replay returned a different id"
  request task-submit POST "/v1/tasks/$task_id/submit" "" "Authorization: Bearer $api_key"; expect_status 202 "task submit"
  local state=""
  for _ in $(seq 1 "$task_poll_attempts"); do
    request task-status GET "/v1/tasks/$task_id" "" "Authorization: Bearer $api_key"; expect_status 200 "task status"
    state=$(json_value_or_empty "$last_body" status)
    case "$state" in
      succeeded) break ;;
      unknown) fail "task entered unknown state; generation will not be resubmitted" ;;
      failed|cancelled|expired) fail "task reached terminal status $state" ;;
      created|running|queued|submitting|prepared|pending) sleep "$task_poll_seconds" ;;
      *) fail "task returned unrecognized status '$state'" ;;
    esac
  done
  [ "$state" = succeeded ] || fail "task did not succeed before polling deadline"
  request task-grant POST "/v1/tasks/$task_id/result-grant" "" "Authorization: Bearer $api_key"; expect_status 200 "task result grant"
  local grant; grant=$(json_value "$last_body" grant_id) || fail "task result grant omitted grant_id"
  request task-result GET "/v1/tasks/$task_id/result?grant_id=$grant" "" "Authorization: Bearer $api_key"; expect_status 200 "task result download"
  expect_header "Content-Type" "application/octet-stream" "task result download"
  [ "$(wc -c <"$last_body" | tr -d ' ')" -gt 0 ] || fail "task result download was empty"
  echo "PASS async task id=$task_id"
}
run_task_check

realtime_url=${BUSINESS_QA_REALTIME_URL:-}
realtime_model=${BUSINESS_QA_REALTIME_MODEL:-${model:-}}
if [ -z "$realtime_url" ]; then
  skip_check "Realtime WebSocket" "no BUSINESS_QA_REALTIME_URL"
elif [ -z "$api_key" ]; then
  skip_check "Realtime WebSocket" "BUSINESS_QA_API_KEY is required"
elif [ -z "$realtime_model" ]; then
  skip_check "Realtime WebSocket" "BUSINESS_QA_REALTIME_MODEL or BUSINESS_QA_MODEL is required"
else
  if ! realtime_url=$(python3 - "$realtime_url" "$realtime_model" <<'PY'
import sys
from urllib.parse import parse_qs, urlencode, urlsplit, urlunsplit
p = urlsplit(sys.argv[1])
if p.scheme not in ("ws", "wss") or not p.netloc or p.fragment: raise SystemExit(1)
q = parse_qs(p.query, keep_blank_values=True)
if not q.get("model", [""])[0]: q["model"] = [sys.argv[2]]
print(urlunsplit((p.scheme, p.netloc, p.path or "/", urlencode(q, doseq=True), "")))
PY
  ); then fail "invalid BUSINESS_QA_REALTIME_URL"; fi
  realtime_origin=${BUSINESS_QA_REALTIME_ORIGIN:-$origin}
  ws_args=(--url "$realtime_url" --api-key "$api_key" --origin "$realtime_origin" --timeout "$timeout")
  [ "${BUSINESS_QA_REALTIME_INSECURE_TLS:-false}" = true ] && ws_args+=(--insecure-tls)
  echo "checking configured Realtime WebSocket"
  if python3 "$script_dir/deployment-business-qa-ws.py" "${ws_args[@]}"; then
    echo "PASS Realtime WebSocket"
  else
    ws_status=$?
    [ "$ws_status" -eq "$EXIT_SKIP" ] && skip_check "Realtime WebSocket" "endpoint reported provider or transport unavailable" || fail "Realtime WebSocket check failed with exit code $ws_status"
  fi
fi

if [ "$skip_count" -gt 0 ]; then
  echo "business QA completed with $skip_count SKIP(s); no skipped capability was reported as passed"
else
  echo "business QA passed all enabled checks"
fi

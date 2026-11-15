#!/usr/bin/env bash
set -Eeuo pipefail

# Provision a disposable current-architecture tenant for the Compose
# business-qa profile. All writes go through the management HTTP API. The
# script prints a customer API key only at the end so it can be piped into a
# protected local file by the caller.

base_url=${BUSINESS_QA_BASE_URL:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}
base_url=${base_url%/}
admin_email=${BUSINESS_QA_ADMIN_EMAIL:-admin@example.com}
admin_password=${BUSINESS_QA_ADMIN_PASSWORD:?set BUSINESS_QA_ADMIN_PASSWORD}
model_name=${BUSINESS_QA_MODEL_NAME:-qa-compose-chat}
provider_url=${BUSINESS_QA_PROVIDER_URL:-http://qa-provider:8090/c/a/v1}
provider_cidr=${BUSINESS_QA_PROVIDER_CIDR:-172.16.0.0/12}
owner_email=${BUSINESS_QA_OWNER_EMAIL:-qa-owner@example.com}
owner_password=${BUSINESS_QA_OWNER_PASSWORD:-qa-owner-password-2026}
operator_email=${BUSINESS_QA_OPERATOR_EMAIL:-qa-operator@example.com}
operator_password=${BUSINESS_QA_OPERATOR_PASSWORD:-qa-operator-password-2026}
approver_email=${BUSINESS_QA_APPROVER_EMAIL:-qa-approver@example.com}
approver_password=${BUSINESS_QA_APPROVER_PASSWORD:-qa-approver-password-2026}

command -v curl >/dev/null 2>&1 || { echo 'curl is required' >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo 'python3 is required' >&2; exit 2; }

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/moni-aigate-business-provision.XXXXXX")
output_dir=${BUSINESS_QA_OUTPUT_DIR:-$work_dir}
mkdir -p "$output_dir"
cookie="$work_dir/cookies"
trap 'rm -rf "$work_dir"' EXIT

json_value() {
  python3 - "$1" "$2" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
for part in sys.argv[2].split('.'):
    value = value[part]
print(value if not isinstance(value, bool) else str(value).lower())
PY
}

request() {
  local name=$1 method=$2 path=$3 body=${4:-}
  shift 4 || true
  local body_file="$work_dir/$name.body" headers="$work_dir/$name.headers"
  local -a args=(--silent --show-error --max-time 20 -D "$headers" -o "$body_file" -w '%{http_code}' -X "$method" -b "$cookie" -c "$cookie" -H 'Accept: application/json')
  if [ -n "$body" ]; then
    printf '%s' "$body" >"$work_dir/$name.request"
    args+=(-H 'Content-Type: application/json' --data-binary "@$work_dir/$name.request")
  fi
  while [ "$#" -gt 0 ]; do args+=(-H "$1"); shift; done
  local status
  status=$(curl "${args[@]}" "$base_url$path")
  if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
    echo "$name failed HTTP $status" >&2
    sed -n '1,120p' "$body_file" >&2
    exit 1
  fi
  printf '%s' "$body_file"
}

login() {
  local email=$1 password=$2
  request login POST /api/auth/login "$(python3 - "$email" "$password" <<'PY'
import json, sys
print(json.dumps({"email": sys.argv[1], "password": sys.argv[2]}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" >/dev/null
  local me
  me=$(request me GET /api/me '' "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}")
  csrf=$(json_value "$me" data.csrf_token)
  [ -n "$csrf" ] && [ "$csrf" != null ] || { echo 'login did not return CSRF' >&2; exit 1; }
  request reauth POST /api/auth/reauth "$(python3 - "$password" <<'PY'
import json, sys
print(json.dumps({"password": sys.argv[1]}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf" >/dev/null
}

login "$admin_email" "$admin_password"

model_body=$(python3 - "$model_name" <<'PY'
import json, sys
print(json.dumps({"public_name": sys.argv[1], "protocol": "openai_chat", "context_limit": 120, "output_limit": 20, "default_output_limit": 2, "input_token_upper_bound": 100, "supports_tools": False}, separators=(",", ":")))
PY
)
model_response=$(request model POST /api/platform/models "$model_body" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
model_id=$(json_value "$model_response" data.id)

channel_body=$(python3 - "$provider_url" "$provider_cidr" <<'PY'
import json, sys
print(json.dumps({"name": "mock-qa-a", "base_url": sys.argv[1], "provider": "openai", "data_region": "global", "secret": "mock-a-secret-2026", "allowed_cidrs": [sys.argv[2]]}, separators=(",", ":")))
PY
)
channel_response=$(request channel POST /api/platform/channels "$channel_body" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
channel_id=$(json_value "$channel_response" data.id)

models=$(request models GET /api/platform/models '' "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}")
schema_id=$(python3 - "$models" "$model_id" <<'PY'
import json, sys
v=json.load(open(sys.argv[1], encoding='utf-8'))
for row in v.get('data', []):
    if row.get('id') == sys.argv[2]:
        print(row['schema_id'])
        break
else: raise SystemExit('model schema not found')
PY
)
schema_digest=$(python3 - "$models" "$model_id" <<'PY'
import json, sys
v=json.load(open(sys.argv[1], encoding='utf-8'))
for row in v.get('data', []):
    if row.get('id') == sys.argv[2]:
        print(row['schema_digest'])
        break
else: raise SystemExit('model schema digest not found')
PY
)

route_body=$(python3 - "$channel_id" <<'PY'
import json, sys
print(json.dumps({"expected_revision": 0, "bindings": [{"channel_id": sys.argv[1], "upstream_model": "qa-chat", "priority": 0, "weight": 1, "max_inflight": 10, "max_input_cost_rate": None, "max_output_cost_rate": None}], "cost_policy": {"mode": "manual", "unknown_cost_policy": "allow"}}, separators=(",", ":")))
PY
)
request route PUT "/api/platform/models/$model_id/bindings" "$route_body" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf" >/dev/null

now=$(python3 - <<'PY'
from datetime import datetime, timezone
print(datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace('+00:00','Z'))
PY
)
price_body=$(python3 - "$model_id" "$schema_id" "$schema_digest" "$now" <<'PY'
import json, sys, uuid
print(json.dumps({"operation_id": str(uuid.uuid4()), "model_id": sys.argv[1], "schema_id": sys.argv[2], "schema_digest": sys.argv[3], "variant_key": "default", "input_rate": "1000000", "output_rate": "1000000", "effective_at": sys.argv[4]}, separators=(",", ":")))
PY
)
request price POST /api/platform/price-versions "$price_body" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf" >/dev/null

operator=$(request user POST /api/platform/users "$(python3 - "$operator_email" "$operator_password" <<'PY'
import json, sys
print(json.dumps({"email": sys.argv[1], "password": sys.argv[2], "platform_role": "finance_operator"}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
approver=$(request approver POST /api/platform/users "$(python3 - "$approver_email" "$approver_password" <<'PY'
import json, sys
print(json.dumps({"email": sys.argv[1], "password": sys.argv[2], "platform_role": "finance_approver"}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")

tenant=$(request tenant POST /api/platform/tenants "$(python3 - "$model_id" "$owner_email" "$owner_password" <<'PY'
import json, sys
print(json.dumps({"name": "qa-compose-tenant", "owner_email": sys.argv[2], "owner_password": sys.argv[3], "model_ids": [sys.argv[1]]}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
tenant_id=$(json_value "$tenant" data.id)

login "$operator_email" "$operator_password"
credit=$(request credit POST /api/platform/credits "$(python3 - "$tenant_id" <<'PY'
import json, sys
print(json.dumps({"tenant_id": sys.argv[1], "amount": "1000000", "business_ref": "qa-compose-credit", "proof_ref": "qa-compose-provision"}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf" "Idempotency-Key: qa-compose-credit")
credit_id=$(json_value "$credit" data.id)

login "$approver_email" "$approver_password"
request approve POST "/api/platform/credits/$credit_id/approve" '{}' "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf" "Idempotency-Key: qa-compose-approve" >/dev/null

login "$owner_email" "$owner_password"
key=$(request key POST /api/keys "$(python3 - "$model_id" <<'PY'
import json, sys
print(json.dumps({"name": "qa-compose-key", "scopes": [sys.argv[1], "mock-tts"]}, separators=(",", ":")))
PY
)" "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
api_key=$(json_value "$key" data.key)

text_body=$(python3 - "$model_name" <<'PY'
import json, sys
print(json.dumps({"model": sys.argv[1], "messages": [{"role": "user", "content": "compose business smoke"}], "max_tokens": 1, "stream": False}, separators=(",", ":")))
PY
)
request text POST /v1/chat/completions "$text_body" "Authorization: Bearer $api_key" "Idempotency-Key: deployment-business-provision-text" >/dev/null
request_id=$(awk 'tolower($1)=="x-request-id:" {print $2}' "$work_dir/text.headers" | tr -d '\r' | tail -n 1)
[ -n "$request_id" ] || { echo 'text provisioning call did not return X-Request-ID' >&2; exit 1; }
detail=$(request detail GET "/api/requests/$request_id" '' "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
funding=$(request funding GET "/api/requests/$request_id/funding" '' "Origin: ${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}" "X-CSRF-Token: $csrf")
task_refs=$(python3 - "$detail" "$funding" <<'PY'
import json, sys
detail=json.load(open(sys.argv[1], encoding='utf-8'))['data']
funding=json.load(open(sys.argv[2], encoding='utf-8'))['data']
if not funding:
    raise SystemExit('text provisioning call returned no funding source')
required=('id','reservation_id','attempt_id','model_id','profile_version','schema_id','schema_digest')
if any(not detail.get(k) for k in required):
    raise SystemExit('text provisioning detail is missing task references')
if not funding[0].get('lot_id'):
    raise SystemExit('text provisioning funding is missing source identity')
values={
    'BUSINESS_QA_TASK_REQUEST_ID': detail['id'],
    'BUSINESS_QA_TASK_RESERVATION_ID': detail['reservation_id'],
    'BUSINESS_QA_TASK_ATTEMPT_ID': detail['attempt_id'],
    'BUSINESS_QA_TASK_SOURCE_ID': funding[0]['lot_id'],
    'BUSINESS_QA_TASK_MODEL_ID': detail['model_id'],
    'BUSINESS_QA_TASK_PROFILE_VERSION': detail['profile_version'],
    'BUSINESS_QA_TASK_SCHEMA_ID': detail['schema_id'],
    'BUSINESS_QA_TASK_SCHEMA_DIGEST': detail['schema_digest'],
}
for key, value in values.items():
    print(f'{key}={value}')
PY
)
printf '%s\n' "$task_refs" >"$output_dir/task.env"

cat >"$output_dir/image.json" <<'EOF'
{"version":"1","profile_version":"11111111-1111-4111-8111-111111111111","variant_key":"default","prompt":"compose business smoke","size":"256x256","format":"png","n":"1"}
EOF
cat >"$output_dir/tts.json" <<'EOF'
{"model":"mock-tts","input":"compose business smoke","voice":"alloy","response_format":"wav"}
EOF

cat >"$output_dir/business.env" <<EOF
BUSINESS_QA_MODEL=$model_name
BUSINESS_QA_API_KEY=$api_key
BUSINESS_QA_TENANT_ID=$tenant_id
BUSINESS_QA_IMAGE_REQUEST_FILE=$output_dir/image.json
BUSINESS_QA_TTS_REQUEST_FILE=$output_dir/tts.json
BUSINESS_QA_REALTIME_URL=ws://127.0.0.1:${DEPLOY_QA_PORT:-18080}/v1/realtime?model=$model_name
BUSINESS_QA_REALTIME_ORIGIN=${BUSINESS_QA_ORIGIN:-http://127.0.0.1:${DEPLOY_QA_PORT:-18080}}
EOF
cat "$output_dir/task.env" >>"$output_dir/business.env"
cat "$output_dir/business.env"

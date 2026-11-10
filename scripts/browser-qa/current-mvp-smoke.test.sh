#!/usr/bin/env bash
set -Eeuo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
runner="$root/scripts/browser-qa/current-mvp-smoke.sh"

bash -n "$runner"
help=$("$runner" --help)
printf '%s\n' "$help" | grep -q "TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'"
printf '%s\n' "$help" | grep -q 'BROWSER_QA_REALTIME_URL'
printf '%s\n' "$help" | grep -q 'does not create tenants'

set +e
missing_output=$(env -u TEST_DATABASE_URL "$runner" 2>&1)
missing_status=$?
wrong_output=$(TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:5432/moni_test?sslmode=disable' "$runner" 2>&1)
wrong_status=$?
set -e
[ "$missing_status" -eq 2 ] || { printf '%s\n' "$missing_output" >&2; exit 1; }
[ "$wrong_status" -eq 2 ] || { printf '%s\n' "$wrong_output" >&2; exit 1; }
printf '%s\n' "$missing_output" | grep -q 'TEST_DATABASE_URL must be exactly'
printf '%s\n' "$wrong_output" | grep -q 'TEST_DATABASE_URL must be exactly'

if grep -nE 'deployment-business-provision|BROWSER_QA_(IDENTITY|VOUCHER|ALERT)|ApplyThrough|current_mvp\.sql' "$runner"; then
  echo 'current MVP browser sidecar contains a forbidden legacy/provisioning path' >&2
  exit 1
fi

tmpdir=$(mktemp -d /tmp/moni-aigate-browser-qa-test.XXXXXX)
server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

cat >"$tmpdir/server.py" <<'PY'
import base64
import hashlib
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import sys

KEY = "smoke-key"

def exact(sock, size):
    value = b""
    while len(value) < size:
        chunk = sock.recv(size - len(value))
        if not chunk:
            raise OSError("socket closed")
        value += chunk
    return value

def frame(value):
    raw = json.dumps(value, separators=(",", ":")).encode()
    return bytes((0x81, len(raw))) + raw

def receive(sock):
    first, second = exact(sock, 2)
    length = second & 127
    if length == 126:
        length = int.from_bytes(exact(sock, 2), "big")
    mask = exact(sock, 4)
    payload = bytearray(exact(sock, length))
    for index in range(length):
        payload[index] ^= mask[index % 4]
    return json.loads(payload.decode())

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        return

    def authorized(self):
        return self.headers.get("Authorization") == f"Bearer {KEY}"

    def json(self, status, value):
        raw = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path == "/health/live":
            self.json(200, {"status": "live"})
            return
        if self.path == "/health/ready":
            self.json(200, {"status": "ready", "recovery_mode": False})
            return
        if self.path == "/v1/models":
            if not self.authorized():
                self.json(401, {"error": {"code": "invalid_credentials"}})
                return
            self.json(200, {"data": [{"id": "smoke-model"}]})
            return
        if self.path.startswith("/v1/realtime"):
            if not self.authorized():
                self.json(401, {"error": {"code": "invalid_credentials"}})
                return
            key = self.headers.get("Sec-WebSocket-Key", "")
            accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
            self.send_response(101, "Switching Protocols")
            self.send_header("Upgrade", "websocket")
            self.send_header("Connection", "Upgrade")
            self.send_header("Sec-WebSocket-Accept", accept)
            self.end_headers()
            self.connection.sendall(frame({"type": "session.created", "data": {"session_id": "s", "turn_id": "t"}}))
            for _ in range(2):
                event = receive(self.connection)
                self.connection.sendall(frame({"type": "event.accepted", "data": {"event_type": event["type"]}}))
                if event["type"] == "response.create":
                    for event_type in ("response.created", "response.delta", "response.done"):
                        self.connection.sendall(frame({"type": event_type, "data": {}}))
            return
        self.json(404, {"error": {"code": "not_found"}})

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self.json(404, {"error": {"code": "not_found"}})
            return
        if not self.authorized():
            self.json(401, {"error": {"code": "invalid_credentials"}})
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length))
        self.json(200, {"model": body["model"], "choices": [{"message": {"content": "QA OK"}}]})

server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
Path(sys.argv[1]).write_text(str(server.server_port))
server.serve_forever()
PY
python3 "$tmpdir/server.py" "$tmpdir/port" &
server_pid=$!
for _ in $(seq 1 40); do [ -s "$tmpdir/port" ] && break; sleep 0.05; done
[ -s "$tmpdir/port" ]
port=$(cat "$tmpdir/port")
output=$(TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  BROWSER_QA_BASE_URL="http://127.0.0.1:$port" \
  BROWSER_QA_API_KEY='smoke-key' \
  BROWSER_QA_MODEL='smoke-model' \
  BROWSER_QA_REALTIME_URL="ws://127.0.0.1:$port/v1/realtime" \
  BROWSER_QA_TIMEOUT=2 BROWSER_QA_READY_ATTEMPTS=1 \
  "$runner")
printf '%s\n' "$output" | grep -q '^PASS text$'
printf '%s\n' "$output" | grep -q '^PASS Realtime WebSocket$'
printf '%s\n' "$output" | grep -q '^PASS current MVP HTTP/WS smoke '
kill "$server_pid" 2>/dev/null || true
wait "$server_pid" 2>/dev/null || true
server_pid=""

echo 'current MVP browser smoke shell assertions passed'

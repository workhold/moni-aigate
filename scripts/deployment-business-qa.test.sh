#!/usr/bin/env bash
set -Eeuo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
runner="$root/scripts/deployment-business-qa.sh"
ws_client="$root/scripts/deployment-business-qa-ws.py"
provision="$root/scripts/deployment-business-provision.sh"

bash -n "$runner"
bash -n "$provision"
python3 -m py_compile "$ws_client"
if [ -e "$root/scripts/deployment-qa-provider.py" ]; then
  echo "legacy deployment-qa-provider.py must be removed" >&2
  exit 1
fi
go test ./internal/testsupport/mockupstream ./scripts/mock-upstream
help=$("$runner" --help)
printf '%s\n' "$help" | grep -q 'BUSINESS_QA_TASK_REQUEST_ID'
printf '%s\n' "$help" | grep -q 'BUSINESS_QA_REALTIME_URL'
if grep -nE '5432|CHECK_MEDIA|newDefaultMediaAdapter|mock-image-generation|mock-tts' "$runner"; then
  echo "runner contains a forbidden deployment shortcut" >&2
  exit 1
fi

tmpdir=$(mktemp -d /tmp/moni-aigate-business-qa-test.XXXXXX)
server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || true; fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

cat >"$tmpdir/health.py" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import sys

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health/live":
            body = b'{"status":"live"}'
        elif self.path == "/health/ready":
            body = b'{"status":"ready","recovery_mode":false}'
        else:
            body = b'{"error":{"code":"not_found"}}'
        self.send_response(200 if self.path.startswith("/health/") else 404)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_):
        pass

server = HTTPServer(("127.0.0.1", 0), Handler)
Path(sys.argv[1]).write_text(str(server.server_port))
server.serve_forever()
PY
python3 "$tmpdir/health.py" "$tmpdir/port" &
server_pid=$!
for _ in $(seq 1 20); do [ -s "$tmpdir/port" ] && break; sleep 0.05; done
[ -s "$tmpdir/port" ]
port=$(cat "$tmpdir/port")

set +e
output=$(BUSINESS_QA_BASE_URL="http://127.0.0.1:$port" \
  BUSINESS_QA_READY_ATTEMPTS=1 BUSINESS_QA_TIMEOUT=2 "$runner" 2>&1)
status=$?
set -e
[ "$status" -eq 3 ] || { printf '%s\n' "$output" >&2; echo "expected SKIP exit 3, got $status" >&2; exit 1; }
printf '%s\n' "$output" | grep -q 'PASS health'
printf '%s\n' "$output" | grep -q 'SKIP text:'
printf '%s\n' "$output" | grep -q 'SKIP async task:'
printf '%s\n' "$output" | grep -q 'SKIP Realtime WebSocket:'
if printf '%s\n' "$output" | grep -q 'business QA passed all enabled checks'; then
  echo "runner falsely reported a complete pass" >&2
  exit 1
fi

set +e
BUSINESS_QA_BASE_URL="http://127.0.0.1:$port" BUSINESS_QA_READY_ATTEMPTS=1 \
  BUSINESS_QA_FAIL_ON_SKIP=true "$runner" >/dev/null 2>&1
strict_status=$?
set -e
[ "$strict_status" -eq 1 ] || { echo "FAIL_ON_SKIP expected 1, got $strict_status" >&2; exit 1; }

cat >"$tmpdir/ws.py" <<'PY'
import base64
import hashlib
import json
from pathlib import Path
import socket
import sys

def exact(sock, size):
    value = b""
    while len(value) < size:
        value += sock.recv(size - len(value))
    return value

def frame(value):
    value = value.encode()
    return bytes([0x81, len(value)]) + value

def receive(sock):
    first, second = exact(sock, 2)
    size = second & 127
    if size == 126:
        size = int.from_bytes(exact(sock, 2), "big")
    mask = exact(sock, 4)
    payload = bytearray(exact(sock, size))
    for index in range(size):
        payload[index] ^= mask[index % 4]
    return json.loads(payload.decode())

server = socket.socket()
server.bind(("127.0.0.1", 0))
server.listen(1)
Path(sys.argv[1]).write_text(str(server.getsockname()[1]))
sock, _ = server.accept()
handshake = b""
while b"\r\n\r\n" not in handshake:
    handshake += sock.recv(4096)
headers = handshake.decode("iso-8859-1").split("\r\n")
key = next(line.split(":", 1)[1].strip() for line in headers if line.lower().startswith("sec-websocket-key:"))
accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
sock.sendall(("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n").encode())
sock.sendall(frame(json.dumps({"type":"session.created","data":{"session_id":"s","turn_id":"t"}})))
for _ in range(2):
    event = receive(sock)
    sock.sendall(frame(json.dumps({"type":"event.accepted","data":{"event_type":event["type"]}})))
    if event["type"] == "response.create":
        for event_type in ("response.created", "response.delta", "response.done"):
            sock.sendall(frame(json.dumps({"type":event_type,"data":{}})))
sock.close()
server.close()
PY
python3 "$tmpdir/ws.py" "$tmpdir/ws-port" &
server_pid=$!
for _ in $(seq 1 20); do [ -s "$tmpdir/ws-port" ] && break; sleep 0.05; done
ws_port=$(cat "$tmpdir/ws-port")
ws_output=$(python3 "$ws_client" --url "ws://127.0.0.1:$ws_port/v1/realtime?model=test" --api-key test-key --origin http://127.0.0.1 --timeout 2)
printf '%s\n' "$ws_output" | grep -q 'Realtime WebSocket passed session=s turn=t'
wait "$server_pid"
server_pid=""
echo "deployment-business-qa shell and WebSocket self-test passed"

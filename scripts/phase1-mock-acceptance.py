#!/usr/bin/env python3
"""Deployment acceptance harness for phase-1 deterministic text mock.

This command consumes an already-running app and worker. It never invokes
Compose, starts, stops, or restarts either service, and only reads the
dedicated PostgreSQL acceptance database. The one temporary container it may
start is the mock upstream itself, attached to the app container's existing
network and removed on exit.
"""

from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
import pathlib
import re
import secrets
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone


PASS = 0
FAIL = 1
SKIP = 3
USAGE = 2
EXPECTED_DB = "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable"
PROTOCOLS = (
    ("chat", "openai_chat", "/v1/chat/completions", False),
    ("responses", "openai_responses", "/v1/responses", False),
    ("messages", "anthropic_messages", "/v1/messages", False),
    ("embeddings", "openai_embeddings", "/v1/embeddings", True),
    ("rerank", "jina_rerank", "/v1/rerank", True),
    ("gemini", "gemini_generate_content", "/v1beta/models/{model}:generateContent", False),
)


class HarnessSkip(Exception):
    pass


class HarnessFailure(Exception):
    pass


def require_program(name: str) -> None:
    if not shutil_which(name):
        raise HarnessSkip(f"missing required executable: {name}")


def shutil_which(name: str) -> str | None:
    for directory in os.environ.get("PATH", "").split(os.pathsep):
        candidate = pathlib.Path(directory) / name
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return str(candidate)
    return None


def run(command: list[str], *, check: bool = True, timeout: int = 20, input_text: str | None = None) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, text=True, capture_output=True, timeout=timeout, input=input_text)
    if check and result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise HarnessFailure(f"command failed ({' '.join(command)}): {detail[:500]}")
    return result


def json_body(value: object) -> bytes:
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False).encode()


def now_wire() -> str:
    return datetime.now(timezone.utc).replace(microsecond=123000).isoformat().replace("+00:00", "Z")


def unique(prefix: str) -> str:
    return f"phase1-{prefix}-{uuid.uuid4().hex[:12]}"


class API:
    def __init__(self, base_url: str, admin_email: str, admin_password: str):
        self.base_url = base_url.rstrip("/")
        self.admin_email = admin_email
        self.admin_password = admin_password
        self.csrf = ""
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))

    def call(self, method: str, path: str, value: object | None = None, *, csrf: bool = False, headers: dict[str, str] | None = None) -> tuple[int, dict, bytes, dict[str, str]]:
        request_headers = {"Accept": "application/json", "Origin": self.base_url}
        if value is not None:
            request_headers["Content-Type"] = "application/json"
        if csrf:
            request_headers["X-CSRF-Token"] = self.csrf
        request_headers.update(headers or {})
        request = urllib.request.Request(self.base_url + path, data=None if value is None else json_body(value), headers=request_headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                raw = response.read()
                status = response.status
                response_headers = {k.lower(): v for k, v in response.headers.items()}
        except urllib.error.HTTPError as error:
            raw = error.read()
            status = error.code
            response_headers = {k.lower(): v for k, v in error.headers.items()}
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = {}
        if not isinstance(parsed, dict):
            parsed = {"raw": parsed}
        return status, parsed, raw, response_headers

    def expect(self, method: str, path: str, value: object | None = None, *, csrf: bool = False, headers: dict[str, str] | None = None) -> object:
        status, parsed, raw, _ = self.call(method, path, value, csrf=csrf, headers=headers)
        if status < 200 or status >= 300:
            raise HarnessFailure(f"HTTP {status} {method} {path}: {raw[:500].decode(errors='replace')}")
        return parsed.get("data", parsed)

    def login(self, email: str, password: str) -> None:
        self.expect("POST", "/api/auth/login", {"email": email, "password": password})
        me = self.expect("GET", "/api/me")
        self.csrf = str(me.get("csrf_token", ""))
        if not self.csrf:
            raise HarnessFailure(f"{email} login did not return CSRF token")
        self.expect("POST", "/api/auth/reauth", {"password": password}, csrf=True)


@dataclass
class MockRuntime:
    container: str
    network: str
    config_path: pathlib.Path


class Harness:
    def __init__(self, args: argparse.Namespace):
        self.args = args
        self.base_url = os.environ.get("PHASE1_BASE_URL", "http://127.0.0.1:18080").rstrip("/")
        self.test_db = os.environ.get("TEST_DATABASE_URL", "")
        self.app_container = os.environ.get("PHASE1_APP_CONTAINER", "")
        self.worker_container = os.environ.get("PHASE1_WORKER_CONTAINER", "")
        self.mock_image = os.environ.get("PHASE1_MOCK_IMAGE", "moni-aigate:local")
        self.admin_email = os.environ.get("PHASE1_ADMIN_EMAIL", "")
        self.admin_password = os.environ.get("PHASE1_ADMIN_PASSWORD", "")
        self.mock: MockRuntime | None = None
        self.api: API | None = None
        self.run_id = unique("acceptance")
        self.requests: list[dict] = []

    def preflight(self) -> None:
        for program in ("curl", "psql", "docker"):
            require_program(program)
        if self.test_db != EXPECTED_DB:
            raise HarnessSkip(f"TEST_DATABASE_URL must be exactly {EXPECTED_DB}; current value is not the dedicated 55439 database")
        if not self.app_container or not self.worker_container:
            raise HarnessSkip("PHASE1_APP_CONTAINER and PHASE1_WORKER_CONTAINER are required; refusing to infer or restart running services")
        if not self.admin_email or not self.admin_password:
            raise HarnessSkip("PHASE1_ADMIN_EMAIL and PHASE1_ADMIN_PASSWORD are required")
        self.assert_container_running(self.app_container)
        self.assert_container_running(self.worker_container)
        self.assert_container_database(self.app_container)
        self.assert_container_database(self.worker_container)
        db = run(["psql", self.test_db, "-At", "-c", "SELECT current_database(), inet_server_port(), 1"], check=False, timeout=10)
        if db.returncode != 0:
            raise HarnessSkip("cannot connect to the dedicated 55439 database")
        if db.stdout.strip() != "moni_test|55439|1":
            raise HarnessSkip(f"dedicated database probe did not identify moni_test/55439: {db.stdout.strip()}")
        health = run(["curl", "--silent", "--show-error", "--fail", "--max-time", "5", f"{self.base_url}/health/ready"], check=False, timeout=10)
        if health.returncode != 0:
            raise HarnessSkip("app ready endpoint is unavailable")
        try:
            ready = json.loads(health.stdout)
        except json.JSONDecodeError as error:
            raise HarnessSkip(f"ready endpoint did not return JSON: {error}")
        if ready.get("status") != "ready" or ready.get("recovery_mode") is not False:
            raise HarnessSkip(f"app is not ready in non-recovery mode: {ready}")
        self.api = API(self.base_url, self.admin_email, self.admin_password)

    @staticmethod
    def assert_container_running(container: str) -> None:
        state = run(["docker", "inspect", "--format", "{{.State.Status}}", container], timeout=10).stdout.strip()
        if state != "running":
            raise HarnessSkip(f"container {container} is not running: {state}")

    @staticmethod
    def container_env(container: str) -> dict[str, str]:
        raw = run(["docker", "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", container], timeout=10).stdout
        result: dict[str, str] = {}
        for line in raw.splitlines():
            if "=" in line:
                key, value = line.split("=", 1)
                result[key] = value
        return result

    def assert_container_database(self, container: str) -> None:
        value = self.container_env(container).get("DATABASE_URL", "")
        parsed = urllib.parse.urlsplit(value)
        # host.docker.internal is the Docker-standard alias for the same
        # dedicated host database; the identity (moni_test on 55439) is what
        # the acceptance pins, not the literal loopback spelling.
        allowed_hosts = ("127.0.0.1", "host.docker.internal")
        if parsed.scheme != "postgres" or parsed.hostname not in allowed_hosts or parsed.port != 55439 or parsed.path != "/moni_test":
            # Do not print a password-bearing URL.
            raise HarnessSkip(f"container {container} DATABASE_URL is not the dedicated 55439/moni_test database")

    def start_mock(self) -> None:
        if not self.app_container:
            raise HarnessSkip("app container is required to attach the temporary mock")
        networks = json.loads(run(["docker", "inspect", "--format", "{{json .NetworkSettings.Networks}}", self.app_container], timeout=10).stdout)
        if not networks:
            raise HarnessSkip("app container has no Docker network")
        network = sorted(networks)[0]
        models = [name for name, _, _, _ in PROTOCOLS]
        config = {
            "candidates": {
                "a": {"secret": "phase1-a-secret", "models": models, "ttfb_ms": 1, "event_interval_ms": 1, "output_tokens": 8, "default_fault": "reject_429"},
                "b": {"secret": "phase1-b-secret", "models": models, "ttfb_ms": 1, "event_interval_ms": 1, "output_tokens": 8, "default_fault": ""},
            }
        }
        # Docker Desktop on macOS only bind-mounts shared host paths. The
        # system tempfile directory (/var/folders or /tmp) is not reliably
        # shared, and Docker then creates a directory at the container target
        # instead of mounting the JSON file. Keep the disposable config under
        # the repository by default; callers can provide another shared root.
        repo_dir = pathlib.Path(__file__).resolve().parent.parent
        config_root = pathlib.Path(os.environ.get("PHASE1_MOCK_CONFIG_DIR", str(repo_dir)))
        temp_dir = pathlib.Path(tempfile.mkdtemp(prefix=".phase1-mock-", dir=config_root))
        config_path = temp_dir / "config.json"
        config_path.write_text(json.dumps(config, separators=(",", ":")), encoding="utf-8")
        container = unique("mock").replace("phase1-", "phase1mock-")
        run(["docker", "run", "-d", "--name", container, "--network", network, "--network-alias", "phase1-mock-upstream", "--entrypoint", "/usr/local/bin/mock-upstream", "-e", "MOCK_UPSTREAM_ENABLE=1", "-v", f"{config_path}:/etc/moni/mock-upstream.json:ro", self.mock_image, "--config", "/etc/moni/mock-upstream.json", "--addr", ":8090", "--allow-directives"], timeout=30)
        self.mock = MockRuntime(container, network, config_path)
        for _ in range(30):
            probe = run(["docker", "exec", container, "wget", "-q", "-O", "-", "http://127.0.0.1:8090/health"], check=False, timeout=5)
            if probe.returncode == 0:
                return
            time.sleep(0.2)
        logs = run(["docker", "logs", container], check=False, timeout=10).stdout[-1000:]
        raise HarnessFailure(f"temporary mock did not become healthy: {logs}")

    def stop_mock(self) -> None:
        if self.mock:
            run(["docker", "rm", "-f", self.mock.container], check=False, timeout=15)
            try:
                self.mock.config_path.unlink()
            except FileNotFoundError:
                pass
            try:
                self.mock.config_path.parent.rmdir()
            except OSError:
                pass
            self.mock = None

    def receipts(self, since: int = 0) -> list[dict]:
        if not self.mock:
            raise HarnessFailure("mock runtime is not active")
        raw = run(["docker", "exec", self.mock.container, "wget", "-q", "-O", "-", f"http://127.0.0.1:8090/__mock/receipts?since={since}"], timeout=10).stdout
        value = json.loads(raw)
        if not isinstance(value, list):
            raise HarnessFailure(f"mock receipts response is not a list: {value}")
        return value

    def provision(self) -> dict[str, dict]:
        assert self.api and self.mock
        self.api.login(self.admin_email, self.admin_password)
        model_ids: dict[str, str] = {}
        for short, protocol, _, input_only in PROTOCOLS:
            model = f"{self.run_id}-{short}"
            profile = {"protocol": protocol, "version": unique("profile"), "model": model, "input_token_upper_bound": 100, "context_limit": 120, "no_consumption_statuses": [429]}
            if input_only:
                profile.update({"output_limit": 0, "default_output_limit": 0, "output_limit_field": ""})
            else:
                profile.update({"output_limit": 20, "default_output_limit": 8, "output_limit_field": "maxOutputTokens" if short == "gemini" else ("max_output_tokens" if short == "responses" else "max_tokens"), "usage_semantics": "cumulative" if short == "gemini" else "final"})
            model_data = self.api.expect("POST", "/api/platform/models", {"public_name": model, "protocol": protocol, "context_limit": 120, "output_limit": profile["output_limit"], "default_output_limit": profile["default_output_limit"], "input_token_upper_bound": 100, "supports_tools": False, "profile": profile}, csrf=True)
            model_ids[short] = str(model_data["id"])
        directory = self.api.expect("GET", "/api/platform/models")
        schemas = {str(row["id"]): (str(row["schema_id"]), str(row["schema_digest"])) for row in directory}
        channels: dict[tuple[str, str], str] = {}
        for short, protocol, _, _ in PROTOCOLS:
            prefix = "v1beta" if short == "gemini" else "v1"
            for candidate, secret in (("a", "phase1-a-secret"), ("b", "phase1-b-secret")):
                channel = self.api.expect("POST", "/api/platform/channels", {"name": f"mock-{self.run_id}-{short}-{candidate}", "base_url": f"http://phase1-mock-upstream:8090/c/{candidate}/{prefix}", "provider": "openai", "data_region": "global", "secret": secret, "allowed_cidrs": ["172.16.0.0/12"]}, csrf=True)
                channels[(short, candidate)] = str(channel["id"])
        channel_list = self.api.expect("GET", "/api/platform/channels")
        revisions = {str(row["id"]): int(row.get("management_revision", 0)) for row in channel_list}
        for short, protocol, _, input_only in PROTOCOLS:
            model_id = model_ids[short]
            bindings = [{"channel_id": channels[(short, "a")], "upstream_model": short, "priority": 1, "weight": 1, "max_inflight": 10}, {"channel_id": channels[(short, "b")], "upstream_model": short, "priority": 0, "weight": 1, "max_inflight": 10}]
            self.api.expect("PUT", f"/api/platform/models/{model_id}/bindings", {"expected_revision": 0, "bindings": bindings, "cost_policy": {"mode": "manual", "unknown_cost_policy": "allow"}}, csrf=True)
            schema_id, schema_digest = schemas[model_id]
            self.api.expect("POST", "/api/platform/price-versions", {"operation_id": str(uuid.uuid4()), "model_id": model_id, "schema_id": schema_id, "schema_digest": schema_digest, "variant_key": "default", "input_rate": "1000000", "output_rate": "0" if input_only else "1000000", "effective_at": now_wire()}, csrf=True)
            # Pricing writes have an intentional per-actor/global rate limit.
            # Keep the harness within that contract instead of retrying a
            # rejected operation or weakening the server's admission gate.
            time.sleep(0.6)
            for candidate in ("a", "b"):
                self.api.expect("POST", "/api/platform/cost-versions", {"operation_id": str(uuid.uuid4()), "channel_id": channels[(short, candidate)], "upstream_model": short, "schema_id": schema_id, "schema_digest": schema_digest, "variant_key": "default", "basis": "contract", "input_rate": "1000000", "output_rate": "0" if input_only else "1000000", "effective_at": now_wire(), "expected_channel_revision": str(revisions[channels[(short, candidate)]])}, csrf=True)
                time.sleep(0.6)
        owner_email = f"{self.run_id}-owner@example.invalid"
        owner_password = secrets.token_urlsafe(18)
        tenant = self.api.expect("POST", "/api/platform/tenants", {"name": self.run_id, "owner_email": owner_email, "owner_password": owner_password, "model_ids": list(model_ids.values())}, csrf=True)
        tenant_id = str(tenant["id"])
        operator_email, operator_password = f"{self.run_id}-operator@example.invalid", secrets.token_urlsafe(18)
        approver_email, approver_password = f"{self.run_id}-approver@example.invalid", secrets.token_urlsafe(18)
        self.api.expect("POST", "/api/platform/users", {"email": operator_email, "password": operator_password, "platform_role": "finance_operator"}, csrf=True)
        self.api.expect("POST", "/api/platform/users", {"email": approver_email, "password": approver_password, "platform_role": "finance_approver"}, csrf=True)
        self.api.login(operator_email, operator_password)
        credit = self.api.expect("POST", "/api/platform/credits", {"tenant_id": tenant_id, "amount": "100000000", "business_ref": self.run_id, "proof_ref": "phase1-mock-acceptance"}, csrf=True, headers={"Idempotency-Key": self.run_id})
        credit_id = str(credit["id"])
        self.api.login(approver_email, approver_password)
        self.api.expect("POST", f"/api/platform/credits/{credit_id}/approve", {}, csrf=True, headers={"Idempotency-Key": str(uuid.uuid4())})
        self.api.login(owner_email, owner_password)
        key = self.api.expect("POST", "/api/keys", {"name": self.run_id, "scopes": list(model_ids.values())}, csrf=True)
        return {short: {"model_id": model_ids[short], "model": f"{self.run_id}-{short}", "key": str(key["key"]), "tenant_id": tenant_id} for short, _, _, _ in PROTOCOLS}

    def request(self, short: str, model: str, token: str, stream: bool, unknown: bool = False, fault: str = "") -> tuple[str, int, bytes]:
        marker = f"[[mock:nonce={self.run_id}-{short}-{'stream' if stream else 'json'}{'-unknown' if unknown else ''}{'-missing-usage' if fault == 'missing_usage' else ''}]]"
        text = f"phase1 acceptance {marker}"
        if short == "chat":
            path, body = "/v1/chat/completions", {"model": model, "messages": [{"role": "user", "content": text}], "max_tokens": 2, "stream": stream}
        elif short == "responses":
            path, body = "/v1/responses", {"model": model, "input": text, "max_output_tokens": 2, "store": False, "stream": stream}
        elif short == "messages":
            path, body = "/v1/messages", {"model": model, "messages": [{"role": "user", "content": text}], "max_tokens": 2, "stream": stream}
        elif short == "embeddings":
            path, body = "/v1/embeddings", {"model": model, "input": [text]}
        elif short == "rerank":
            path, body = "/v1/rerank", {"model": model, "query": text, "documents": ["one", "two"]}
        else:
            path, body = f"/v1beta/models/{model}:{'streamGenerateContent' if stream else 'generateContent'}", {"contents": [{"parts": [{"text": text}]}], "generationConfig": {"maxOutputTokens": 2}}
            if stream:
                path += "?alt=sse"
        if unknown or fault:
            body_text = text + f" [[mock:fault={fault or 'drop_after_headers'}]]"
            if short in ("chat", "messages"):
                body["messages"][0]["content"] = body_text
            elif short == "responses":
                body["input"] = body_text
            elif short == "embeddings":
                body["input"] = [body_text]
            elif short == "rerank":
                body["query"] = body_text
            else:
                body["contents"][0]["parts"][0]["text"] = body_text
        request = urllib.request.Request(self.base_url + path, data=json_body(body), headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json", "Accept": "text/event-stream" if stream else "application/json", "Idempotency-Key": unique("request")}, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=35) as response:
                raw = response.read()
                status = response.status
                headers = {k.lower(): v for k, v in response.headers.items()}
        except urllib.error.HTTPError as error:
            raw = error.read()
            status = error.code
            headers = {k.lower(): v for k, v in error.headers.items()}
        request_id = headers.get("x-request-id", "")
        if not request_id:
            raise HarnessFailure(f"{short} {'stream' if stream else 'json'} did not return X-Request-ID; HTTP {status}")
        if not unknown and status != 200:
            raise HarnessFailure(f"{short} {'stream' if stream else 'json'} returned HTTP {status}: {raw[:500].decode(errors='replace')}")
        if not unknown and not raw:
            raise HarnessFailure(f"{short} {'stream' if stream else 'json'} returned an empty body")
        self.requests.append({"request_id": request_id, "protocol": short, "stream": stream, "status": status, "unknown": unknown})
        return request_id, status, raw

    def query(self, sql: str, *args: str) -> list[list[str]]:
        # psql only expands :'name' variables when reading commands from stdin;
        # passing the same SQL through -c leaves the placeholder literal and
        # fails before reaching the dedicated database. An optional schema is
        # used only by isolated QA deployments; the default remains the public
        # current-MVP schema.
        schema = os.environ.get("PHASE1_DATABASE_SCHEMA", "")
        if schema:
            if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", schema) is None:
                raise HarnessFailure("PHASE1_DATABASE_SCHEMA is not a safe SQL identifier")
            sql = f'SET search_path TO "{schema}";\n' + sql
        command = ["psql", self.test_db, "-q", "-At", "-F", "\t", "-v", "ON_ERROR_STOP=1"]
        for index, value in enumerate(args, 1):
            command.extend(["-v", f"p{index}={value}"])
        result = run(command, timeout=20, input_text=sql + "\n")
        return [line.split("\t") for line in result.stdout.splitlines() if line]

    def assert_request(self, request_id: str, *, unknown: bool, expected_attempts: int, expected_status: str = "", expected_billing: str = "") -> None:
        rows = self.query("SELECT r.status,r.billing_status,(SELECT count(*) FROM upstream_attempts a WHERE a.request_id=r.id),(SELECT count(*) FROM settlements s WHERE s.request_id=r.id) FROM request_records r WHERE r.id=:'p1'", request_id)
        if len(rows) != 1:
            raise HarnessFailure(f"request {request_id} is absent from 55439 database")
        status, billing, attempts, settlements = rows[0]
        if int(attempts) != expected_attempts:
            raise HarnessFailure(f"request {request_id} attempt count={attempts}, expected {expected_attempts}")
        if expected_status and status != expected_status:
            raise HarnessFailure(f"request {request_id} protocol status={status}, expected {expected_status}")
        if expected_billing and billing != expected_billing:
            raise HarnessFailure(f"request {request_id} billing status={billing}, expected {expected_billing}")
        if unknown:
            # Protocol success and charge success are separate. Missing usage
            # may complete the protocol while its reservation awaits manual
            # reconciliation; neither that nor a dropped response may settle.
            if int(settlements) > 1 or billing == "settled":
                raise HarnessFailure(f"unknown-consumption request was incorrectly settled: status={status} billing={billing} settlements={settlements}")
        elif int(settlements) != 1 or billing != "settled":
            raise HarnessFailure(f"request {request_id} settlement invariant failed: status={status} billing={billing} settlements={settlements}")
        costs = self.query("SELECT sequence,cost_status,(cost_amount IS NOT NULL),delivery_state FROM upstream_attempts WHERE request_id=:'p1' ORDER BY sequence", request_id)
        if len(costs) != expected_attempts:
            raise HarnessFailure(f"request {request_id} missing attempt cost rows: {costs}")
        for sequence, cost_status, has_amount, delivery in costs:
            if cost_status == "unknown":
                if has_amount == "t":
                    raise HarnessFailure(f"attempt {sequence} has unknown cost with an amount")
            elif cost_status not in ("contract", "invoice") or has_amount != "t":
                raise HarnessFailure(f"attempt {sequence} cost projection is not explicit: {cost_status}/{has_amount}/{delivery}")

    def run_acceptance(self) -> None:
        models = self.provision()
        for short, _, _, input_only in PROTOCOLS:
            for stream in ((False,) if input_only else (False, True)):
                request_id, _, _ = self.request(short, models[short]["model"], models[short]["key"], stream)
                self.assert_request(request_id, unknown=False, expected_attempts=2)
                nonce = f"{self.run_id}-{short}-{'stream' if stream else 'json'}"
                matches = [item for item in self.receipts() if item.get("nonce") == nonce]
                if len(matches) != 2 or [item.get("candidate") for item in matches] != ["a", "b"]:
                    raise HarnessFailure(f"{short} failover receipts do not show exactly a then b: {matches}")
        request_id, status, _ = self.request("chat", models["chat"]["model"], models["chat"]["key"], False, unknown=True)
        self.assert_request(request_id, unknown=True, expected_attempts=1, expected_status="failed")
        matches = [item for item in self.receipts() if item.get("nonce") == f"{self.run_id}-chat-json-unknown"]
        if len(matches) != 1 or matches[0].get("candidate") != "a":
            raise HarnessFailure(f"unknown-consumption receipt count/candidate failed: {matches}")
        request_id, missing_status, _ = self.request("chat", models["chat"]["model"], models["chat"]["key"], False, unknown=True, fault="missing_usage")
        self.assert_request(request_id, unknown=True, expected_attempts=1, expected_status="succeeded", expected_billing="pending_reconciliation")
        matches = [item for item in self.receipts() if item.get("nonce") == f"{self.run_id}-chat-json-unknown-missing-usage"]
        if len(matches) != 1 or matches[0].get("candidate") not in ("a", "b"):
            raise HarnessFailure(f"missing-usage request was replayed or has no receipt: {matches}")
        print(json.dumps({"status": "passed", "run_id": self.run_id, "requests": len(self.requests), "unknown_http_status": status, "missing_usage_http_status": missing_status, "database": "127.0.0.1:55439/moni_test", "mock": True}, separators=(",", ":")))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--preflight", action="store_true", help="only verify the running deployment and database boundary")
    args = parser.parse_args()
    harness = Harness(args)
    try:
        harness.preflight()
        if args.preflight:
            print(json.dumps({"status": "preflight_passed", "database": "127.0.0.1:55439/moni_test", "app": harness.base_url}, separators=(",", ":")))
            return PASS
        harness.start_mock()
        harness.run_acceptance()
        return PASS
    except HarnessSkip as error:
        print(f"SKIP: {error}", file=sys.stderr)
        return SKIP
    except (HarnessFailure, subprocess.SubprocessError, OSError, ValueError, json.JSONDecodeError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return FAIL
    finally:
        harness.stop_mock()


if __name__ == "__main__":
    raise SystemExit(main())

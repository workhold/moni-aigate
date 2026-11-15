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

# These bindings are executable evidence, not a second acceptance implementation.
# The deployment harness below covers the deployed HTTP/database path; these
# focused tests cover the named routing/provider boundaries without Compose.
PURE_GO_TEST_BINDINGS = {
    "weights_3_to_1": ("./internal/testsupport/mockupstream", "TestPhase1ThreeToOneWeightDistribution"),
    "capacity_full_demotion": ("./internal/routing", "TestAcquisitionDecisionsCoverEveryCandidate"),
    "circuit_health": ("./internal/routing", "TestHalfOpenNeutralDoesNotReopenOrProveSuccess"),
    "cost_first": ("./internal/routing", "TestCostFirstUsesBothDimensionsWithoutInt64OverflowOrRounding"),
    "unknown_cost_denial": ("./internal/routing", "TestCostUnknownAndUnitCapsAreHardFilters"),
    "procurement_price_cap": ("./internal/routing", "TestCostUnknownAndUnitCapsAreHardFilters"),
    "connection_refused_max_two_attempts": ("./internal/testsupport/mockupstream", "TestPhase1ConnectionRefusedUsesAtMostTwoAttempts"),
    "trusted_no_consumption_a_to_b": ("./internal/testsupport/mockupstream", "TestPhase1TrustedRejectionAThenBHasTwoReceipts"),
    "drop_after_events": ("./internal/testsupport/mockupstream", "TestPhase1StreamFaultsHaveOneReceiptAndNoReplay"),
    "stall_first_event": ("./internal/testsupport/mockupstream", "TestPhase1StreamFaultsHaveOneReceiptAndNoReplay"),
    "error_after_usage": ("./internal/testsupport/mockupstream", "TestPhase1StreamFaultsHaveOneReceiptAndNoReplay"),
    "conflicting_usage": ("./internal/testsupport/mockupstream", "TestPhase1StreamFaultsHaveOneReceiptAndNoReplay"),
    "tool_call_deltas": ("./internal/provider", "TestToolOnlyOutputCountsAsContent"),
    "native_tool_call_deltas": ("./internal/provider", "TestNativeToolsAndJSONEvidence"),
    "cancellation": ("./internal/provider", "TestCancelClosesRealUpstreamBody"),
}
POSTGRES_GO_TEST_BINDINGS = {
    "cost_first": ("./internal/server", "TestCostRoutingSelectsLowerCostAndKeepsRetailPrice"),
    "unknown_cost_and_price_cap": ("./internal/server", "TestCostRoutingUnknownAndRateCapsApplyToAllSixProtocols"),
    "trusted_a_to_b_unique_settlement": ("./internal/server", "TestGatewayPostgresSafeFailoverHasOneChargeAndTwoPosts"),
    "missing_usage_pending": ("./internal/server", "TestGatewayPostgresMissingUsageNeverSendsSuccessDone"),
    "cancellation_pending_no_replay": ("./internal/server", "TestGatewayPostgresClientCancellationKeepsUnknownUsagePending"),
    "alias_authorization_and_original_name": ("./internal/server", "TestModelAliasHTTPGatewayAuthorizationAndOriginalName"),
    "group_multiplier_compilation": ("./internal/catalog", "TestPhase1GroupMultiplierCompilesImmutablePrices"),
    "paid_credit_http_contract": ("./internal/server", "TestHTTPFundingManualCreditPreservesWalletAndApprovalContract"),
    "tenant_group_assignment_http_contract": ("./internal/server", "TestPricingHTTPBusinessAndCurrentDomainIsolation"),
}
REQUIRED_PURE_CASES = frozenset({
    "weights_3_to_1", "capacity_full_demotion", "circuit_health", "cost_first",
    "unknown_cost_denial", "procurement_price_cap", "connection_refused_max_two_attempts",
    "trusted_no_consumption_a_to_b", "drop_after_events", "stall_first_event",
    "error_after_usage", "conflicting_usage", "tool_call_deltas", "cancellation",
})


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


def expected_charge(input_tokens: int, output_tokens: int, input_rate: int, output_rate: int) -> int:
    """Apply an already-compiled absolute per-million price with one final ceil."""
    values = (input_tokens, output_tokens, input_rate, output_rate)
    if any(isinstance(value, bool) or not isinstance(value, int) or value < 0 for value in values):
        raise HarnessFailure(f"invalid quantities or absolute rates for charge: {values}")
    return (input_tokens * input_rate + output_tokens * output_rate + 999_999) // 1_000_000


def paid_credit_body(tenant_id: str, business_ref: str, amount: str) -> dict:
    """Current 034 paid-credit DTO; COIN and CNY test amounts intentionally match."""
    return {
        "tenant_id": tenant_id,
        "amount": amount,
        "basis_kind": "paid",
        "fiat_currency": "CNY",
        "fiat_amount": amount,
        "business_ref": business_ref,
        "proof_ref": "phase1-mock-acceptance",
    }


def keyed_values(value: object, key: str) -> list[object]:
    found: list[object] = []
    if isinstance(value, dict):
        for child_key, child in value.items():
            if child_key == key:
                found.append(child)
            found.extend(keyed_values(child, key))
    elif isinstance(value, list):
        for child in value:
            found.extend(keyed_values(child, key))
    return found


def assert_model_echo(short: str, raw: bytes, requested_name: str, stream: bool) -> None:
    key = "modelVersion" if short == "gemini" else "model"
    if not stream:
        try:
            value = json.loads(raw)
        except json.JSONDecodeError as error:
            raise HarnessFailure(f"{short} JSON response is invalid: {error}") from error
        if not isinstance(value, dict) or value.get(key) != requested_name:
            raise HarnessFailure(f"{short} response did not echo requested model {requested_name}: {raw[:500]!r}")
        return
    names: list[object] = []
    for line in raw.decode("utf-8").splitlines():
        if not line.startswith("data:"):
            continue
        payload = line[5:].strip()
        if not payload or payload == "[DONE]":
            continue
        try:
            value = json.loads(payload)
        except json.JSONDecodeError as error:
            raise HarnessFailure(f"{short} SSE data is invalid JSON: {payload[:200]!r}") from error
        names.extend(keyed_values(value, key))
    if not names or any(name != requested_name for name in names):
        raise HarnessFailure(f"{short} SSE did not consistently echo requested model {requested_name}: {names}")


def check_receipt_sequence(receipts: list[dict], nonce: str, *, expected: list[str] | None = None, count: int | None = None) -> list[dict]:
    if (expected is None) == (count is None):
        raise HarnessFailure("receipt assertion requires exactly one of expected sequence or count")
    matches = [item for item in receipts if item.get("nonce") == nonce]
    candidates = [item.get("candidate") for item in matches]
    if expected is not None and candidates != expected:
        raise HarnessFailure(f"receipt sequence for {nonce}={candidates}, expected {expected}: {matches}")
    if count is not None and (len(matches) != count or any(candidate not in ("a", "b") for candidate in candidates)):
        raise HarnessFailure(f"receipt count/candidates for {nonce}={candidates}, expected count {count}: {matches}")
    return matches


def run_go_bindings(bindings: dict[str, tuple[str, str]], *, postgres: bool) -> None:
    if not shutil_which("go"):
        raise HarnessFailure("missing required executable: go")
    grouped: dict[str, set[str]] = {}
    for package, test_name in bindings.values():
        if package == "./..." or not test_name.startswith("Test"):
            raise HarnessFailure(f"invalid focused Go binding: {package} {test_name}")
        grouped.setdefault(package, set()).add(test_name)
    for package in sorted(grouped):
        expression = "^(" + "|".join(sorted(grouped[package])) + ")$"
        command = ["go", "test", "-race", "-count=1"]
        if postgres:
            command.extend(["-p", "1"])
        command.extend([package, "-run", expression])
        result = subprocess.run(command, timeout=600 if postgres else 180)
        if result.returncode != 0:
            raise HarnessFailure(f"focused Go bindings failed for {package}")
    print(json.dumps({"status": "passed", "go_bindings": len(bindings), "postgres": postgres}, separators=(",", ":")))


def write_ui_accounts_file(raw_path: str, payload: dict) -> None:
    """Write an explicit private UI handoff without API keys or session material."""
    path = pathlib.Path(raw_path).expanduser()
    if not path.is_absolute():
        raise HarnessFailure("PHASE1_UI_ACCOUNTS_FILE must be an absolute path outside the repository")
    repo = pathlib.Path(__file__).resolve().parent.parent
    try:
        parent = path.parent.resolve(strict=True)
    except OSError as error:
        raise HarnessFailure("PHASE1_UI_ACCOUNTS_FILE parent must already exist") from error
    if parent == repo or repo in parent.parents:
        raise HarnessFailure("PHASE1_UI_ACCOUNTS_FILE must be outside the repository")
    path = parent / path.name
    forbidden = {"key", "api_key", "cookie", "session_cookie", "csrf", "csrf_token", "authorization"}

    def check_fields(value: object) -> None:
        if isinstance(value, dict):
            for key, child in value.items():
                if str(key).lower() in forbidden:
                    raise HarnessFailure(f"UI accounts export refuses session/API-key field {key}")
                check_fields(child)
        elif isinstance(value, list):
            for child in value:
                check_fields(child)

    check_fields(payload)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        descriptor = os.open(path, flags, 0o600)
    except FileExistsError as error:
        raise HarnessFailure("PHASE1_UI_ACCOUNTS_FILE must name a new non-symlink file") from error
    except OSError as error:
        raise HarnessFailure("PHASE1_UI_ACCOUNTS_FILE cannot be opened securely") from error
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            os.fchmod(output.fileno(), 0o600)
            json.dump(payload, output, ensure_ascii=False, indent=2, sort_keys=True)
            output.write("\n")
    except Exception:
        try:
            path.unlink()
        except OSError:
            pass
        raise


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
        self.group_prices: dict[str, dict[str, int | str]] = {}

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

    def assert_receipts(self, nonce: str, *, expected: list[str] | None = None, count: int | None = None) -> None:
        check_receipt_sequence(self.receipts(), nonce, expected=expected, count=count)

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
        default_price_ids: dict[str, str] = {}
        for short, protocol, _, input_only in PROTOCOLS:
            model_id = model_ids[short]
            bindings = [{"channel_id": channels[(short, "a")], "upstream_model": short, "priority": 1, "weight": 1, "max_inflight": 10}, {"channel_id": channels[(short, "b")], "upstream_model": short, "priority": 0, "weight": 1, "max_inflight": 10}]
            self.api.expect("PUT", f"/api/platform/models/{model_id}/bindings", {"expected_revision": 0, "bindings": bindings, "cost_policy": {"mode": "manual", "unknown_cost_policy": "allow"}}, csrf=True)
            schema_id, schema_digest = schemas[model_id]
            price_receipt = self.api.expect("POST", "/api/platform/price-versions", {"operation_id": str(uuid.uuid4()), "model_id": model_id, "schema_id": schema_id, "schema_digest": schema_digest, "variant_key": "default", "input_rate": "1000000", "output_rate": "0" if input_only else "1000000", "effective_at": now_wire()}, csrf=True)
            default_price_ids[model_id] = str(price_receipt["result"]["id"])
            # Pricing writes have an intentional per-actor/global rate limit.
            # Keep the harness within that contract instead of retrying a
            # rejected operation or weakening the server's admission gate.
            time.sleep(0.6)
            for candidate in ("a", "b"):
                self.api.expect("POST", "/api/platform/cost-versions", {"operation_id": str(uuid.uuid4()), "channel_id": channels[(short, candidate)], "upstream_model": short, "schema_id": schema_id, "schema_digest": schema_digest, "variant_key": "default", "basis": "contract", "input_rate": "1000000", "output_rate": "0" if input_only else "1000000", "effective_at": now_wire(), "expected_channel_revision": str(revisions[channels[(short, candidate)]])}, csrf=True)
                time.sleep(0.6)
        group = self.api.expect("POST", "/api/platform/price-groups", {"operation_id": str(uuid.uuid4()), "label": self.run_id}, csrf=True)
        group_id = str(group["operation"]["resource_id"])
        time.sleep(0.6)
        compile_body = {
            "multiplier": "0.5",
            "model_ids": sorted(model_ids.values()),
            "effective_at": now_wire(),
            "expected_base_version_ids": default_price_ids,
        }
        preview = self.api.expect("POST", f"/api/platform/price-groups/{group_id}/price-versions/compile-preview", dict(compile_body, operation_id=str(uuid.uuid4())), csrf=True)
        compiled = self.api.expect("POST", f"/api/platform/price-groups/{group_id}/price-versions/compile", dict(compile_body, operation_id=str(uuid.uuid4())), csrf=True)
        time.sleep(0.6)
        preview_items = {str(item["model_id"]): item for item in preview["items"]}
        compiled_items = {str(item["model_id"]): item for item in compiled["result"]["items"]}
        if set(preview_items) != set(model_ids.values()) or set(compiled_items) != set(model_ids.values()):
            raise HarnessFailure("group multiplier preview/publication did not cover all six protocols")
        for short, _, _, input_only in PROTOCOLS:
            model_id = model_ids[short]
            before, after = preview_items[model_id], compiled_items[model_id]
            expected_output = 0 if input_only else 500000
            for item in (before, after):
                if item["base_price_version_id"] != default_price_ids[model_id] or int(item["input_rate"]) != 500000 or int(item["output_rate"]) != expected_output:
                    raise HarnessFailure(f"compiled absolute price mismatch for {short}: {item}")
            if before["price_version_id"] is not None or before["input_rate"] != after["input_rate"] or before["output_rate"] != after["output_rate"] or not after["price_version_id"]:
                raise HarnessFailure(f"preview/publication mismatch for {short}: {before} vs {after}")
            self.group_prices[model_id] = {"price_version_id": str(after["price_version_id"]), "input_rate": 500000, "output_rate": expected_output}
        aliases: dict[str, str] = {}
        for short, _, _, _ in PROTOCOLS:
            alias = f"{self.run_id}-alias-{short}"
            self.api.expect("POST", f"/api/platform/models/{model_ids[short]}/aliases", {"operation_id": str(uuid.uuid4()), "alias": alias}, csrf=True)
            aliases[short] = alias
        owner_email = f"{self.run_id}-owner@example.invalid"
        owner_password = secrets.token_urlsafe(18)
        tenant = self.api.expect("POST", "/api/platform/tenants", {"name": self.run_id, "owner_email": owner_email, "owner_password": owner_password, "model_ids": list(model_ids.values())}, csrf=True)
        tenant_id = str(tenant["id"])
        self.api.expect("PUT", f"/api/platform/tenants/{tenant_id}/pricing", {"operation_id": str(uuid.uuid4()), "expected_revision": "1", "price_group_id": group_id}, csrf=True)
        operator_email, operator_password = f"{self.run_id}-operator@example.invalid", secrets.token_urlsafe(18)
        approver_email, approver_password = f"{self.run_id}-approver@example.invalid", secrets.token_urlsafe(18)
        self.api.expect("POST", "/api/platform/users", {"email": operator_email, "password": operator_password, "platform_role": "finance_operator"}, csrf=True)
        self.api.expect("POST", "/api/platform/users", {"email": approver_email, "password": approver_password, "platform_role": "finance_approver"}, csrf=True)
        self.api.login(operator_email, operator_password)
        credit = self.api.expect("POST", "/api/platform/credits", paid_credit_body(tenant_id, self.run_id, "100000000"), csrf=True, headers={"Idempotency-Key": self.run_id})
        credit_id = str(credit["id"])
        self.api.login(approver_email, approver_password)
        self.api.expect("POST", f"/api/platform/credits/{credit_id}/approve", {}, csrf=True, headers={"Idempotency-Key": str(uuid.uuid4())})
        self.api.login(owner_email, owner_password)
        key = self.api.expect("POST", "/api/keys", {"name": self.run_id, "scopes": list(model_ids.values())}, csrf=True)
        models = {short: {"model_id": model_ids[short], "model": f"{self.run_id}-{short}", "alias": aliases[short], "key": str(key["key"]), "tenant_id": tenant_id} for short, _, _, _ in PROTOCOLS}
        self.persist_ui_accounts(models, tenant_id, group_id, owner_email, owner_password, operator_email, operator_password, approver_email, approver_password)
        return models

    def persist_ui_accounts(self, models: dict[str, dict], tenant_id: str, group_id: str, owner_email: str, owner_password: str, operator_email: str, operator_password: str, approver_email: str, approver_password: str) -> None:
        path = os.environ.get("PHASE1_UI_ACCOUNTS_FILE", "")
        if not path:
            return
        protocols = {short: protocol for short, protocol, _, _ in PROTOCOLS}
        payload = {
            "run_id": self.run_id,
            "base_url": self.base_url,
            "tenant": {"id": tenant_id, "name": self.run_id, "price_group_id": group_id},
            "accounts": {
                "owner": {"email": owner_email, "password": owner_password},
                "operator": {"email": operator_email, "password": operator_password},
                "approver": {"email": approver_email, "password": approver_password},
            },
            "models": [
                {"model_id": values["model_id"], "public_name": values["model"], "alias": values["alias"], "protocol": protocols[short]}
                for short, values in sorted(models.items())
            ],
        }
        write_ui_accounts_file(path, payload)

    def request_nonce(self, short: str, stream: bool, unknown: bool, fault: str, case_name: str) -> str:
        nonce = f"{self.run_id}-{short}-{'stream' if stream else 'json'}{'-unknown' if unknown else ''}{'-missing-usage' if fault == 'missing_usage' else ''}"
        if case_name:
            if re.fullmatch(r"[a-z0-9-]+", case_name) is None:
                raise HarnessFailure(f"invalid mock case name: {case_name}")
            nonce += "-" + case_name
        return nonce

    def request(self, short: str, model: str, token: str, stream: bool, unknown: bool = False, fault: str = "", case_name: str = "", timeout_seconds: int = 35) -> tuple[str, int, bytes]:
        nonce = self.request_nonce(short, stream, unknown, fault, case_name)
        marker = f"[[mock:nonce={nonce}]]"
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
            with urllib.request.urlopen(request, timeout=timeout_seconds) as response:
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
        self.requests.append({"request_id": request_id, "protocol": short, "stream": stream, "status": status, "unknown": unknown, "nonce": nonce})
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

    def assert_request(self, request_id: str, *, unknown: bool, expected_attempts: int, expected_status: str = "", expected_billing: str = "", expected_requested_name: str = "", expected_protocol: str = "", expected_usage: str = "") -> None:
        rows = self.query("SELECT r.status,r.billing_status,r.model_id,COALESCE(r.requested_model_name,''),COALESCE(f.protocol_outcome,''),COALESCE(f.retail_usage_state,''),(SELECT count(*) FROM upstream_attempts a WHERE a.request_id=r.id),(SELECT count(*) FROM settlements s WHERE s.request_id=r.id),COALESCE((SELECT sum(s.charged_amount) FROM settlements s WHERE s.request_id=r.id),0) FROM request_records r LEFT JOIN finalization_records f ON f.id=r.latest_finalization_id WHERE r.id=:'p1'", request_id)
        if len(rows) != 1:
            raise HarnessFailure(f"request {request_id} is absent from 55439 database")
        status, billing, model_id, requested_name, protocol, usage, attempts, settlements, charged_total = rows[0]
        if int(attempts) != expected_attempts:
            raise HarnessFailure(f"request {request_id} attempt count={attempts}, expected {expected_attempts}")
        if expected_status and status != expected_status:
            raise HarnessFailure(f"request {request_id} status={status}, expected {expected_status}")
        if expected_billing and billing != expected_billing:
            raise HarnessFailure(f"request {request_id} billing status={billing}, expected {expected_billing}")
        if expected_requested_name and requested_name != expected_requested_name:
            raise HarnessFailure(f"request {request_id} requested model={requested_name}, expected {expected_requested_name}")
        if expected_protocol and protocol != expected_protocol:
            raise HarnessFailure(f"request {request_id} protocol outcome={protocol}, expected {expected_protocol}")
        if expected_usage and usage != expected_usage:
            raise HarnessFailure(f"request {request_id} retail usage={usage}, expected {expected_usage}")
        if unknown:
            # A released failure has one zero-charge terminal settlement row;
            # pending/unfinished requests have none. Neither may be a successful charge.
            if billing == "settled" or int(settlements) > 1 or int(charged_total) != 0:
                raise HarnessFailure(f"unknown-consumption request was incorrectly charged: status={status} billing={billing} settlements={settlements} charged={charged_total}")
            if (billing == "released" and int(settlements) != 1) or (billing != "released" and int(settlements) != 0):
                raise HarnessFailure(f"unknown-consumption terminal count mismatch: billing={billing} settlements={settlements}")
        elif int(settlements) != 1 or billing != "settled":
            raise HarnessFailure(f"request {request_id} settlement invariant failed: status={status} billing={billing} settlements={settlements}")
        else:
            price = self.group_prices.get(model_id)
            if not price:
                raise HarnessFailure(f"request {request_id} has no compiled group-price expectation for {model_id}")
            pricing = self.query("SELECT q.price_version_id,s.charged_amount,(SELECT quantity FROM meter_event_quantities WHERE event_id=f.event_id AND dimension='text_input_token'),(SELECT quantity FROM meter_event_quantities WHERE event_id=f.event_id AND dimension='text_output_token') FROM request_meter_snapshots q JOIN settlements s ON s.request_id=q.request_id JOIN finalization_records f ON f.id=s.finalization_id WHERE q.request_id=:'p1'", request_id)
            if len(pricing) != 1:
                raise HarnessFailure(f"request {request_id} missing immutable price/final quantity evidence: {pricing}")
            price_version_id, charged, input_tokens, output_tokens = pricing[0]
            if price_version_id != price["price_version_id"]:
                raise HarnessFailure(f"request {request_id} used price {price_version_id}, expected compiled group price {price['price_version_id']}")
            if not input_tokens or not output_tokens:
                raise HarnessFailure(f"request {request_id} has incomplete final quantities: {pricing}")
            wanted = expected_charge(int(input_tokens), int(output_tokens), int(price["input_rate"]), int(price["output_rate"]))
            if int(charged) != wanted:
                raise HarnessFailure(f"request {request_id} charged {charged}, expected {wanted} from compiled absolute price and final quantities {pricing}")
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
                alias = models[short]["alias"]
                request_id, _, raw = self.request(short, alias, models[short]["key"], stream)
                assert_model_echo(short, raw, alias, stream)
                self.assert_request(request_id, unknown=False, expected_attempts=2, expected_requested_name=alias, expected_protocol="completed", expected_usage="confirmed_final")
                nonce = self.request_nonce(short, stream, False, "", "")
                self.assert_receipts(nonce, expected=["a", "b"])
        # A public-name control proves alias resolution retains the same logical
        # model, compiled group-price snapshot, route, and unique settlement.
        public_name = models["chat"]["model"]
        request_id, _, raw = self.request("chat", public_name, models["chat"]["key"], False, case_name="public-model")
        assert_model_echo("chat", raw, public_name, False)
        self.assert_request(request_id, unknown=False, expected_attempts=2, expected_requested_name=public_name, expected_protocol="completed", expected_usage="confirmed_final")
        self.assert_receipts(self.request_nonce("chat", False, False, "", "public-model"), expected=["a", "b"])

        fault_statuses: dict[str, int] = {}
        request_id, status, _ = self.request("chat", models["chat"]["alias"], models["chat"]["key"], False, unknown=True)
        fault_statuses["drop_after_headers"] = status
        self.assert_request(request_id, unknown=True, expected_attempts=1, expected_status="failed", expected_billing="released", expected_requested_name=models["chat"]["alias"], expected_protocol="failed", expected_usage="missing")
        self.assert_receipts(self.request_nonce("chat", False, True, "", ""), expected=["a"])

        request_id, missing_status, _ = self.request("chat", models["chat"]["alias"], models["chat"]["key"], False, unknown=True, fault="missing_usage")
        fault_statuses["missing_usage"] = missing_status
        self.assert_request(request_id, unknown=True, expected_attempts=1, expected_status="succeeded", expected_billing="pending_reconciliation", expected_requested_name=models["chat"]["alias"], expected_protocol="completed", expected_usage="missing")
        self.assert_receipts(self.request_nonce("chat", False, True, "missing_usage", ""), count=1)

        for fault, case_name, expected_protocol, expected_usage, expected_billing in (
            ("drop_after_events:2", "drop-events", "failed", "missing", "pending_reconciliation"),
            ("error_after_usage", "error-usage", "failed", "provisional", "pending_reconciliation"),
            ("conflicting_usage", "conflicting-usage", "completed", "conflicting", "pending_reconciliation"),
        ):
            request_id, status, _ = self.request("chat", models["chat"]["alias"], models["chat"]["key"], True, unknown=True, fault=fault, case_name=case_name)
            fault_statuses[fault] = status
            if status != 200:
                raise HarnessFailure(f"{fault} stream should commit partial output before the terminal error; HTTP {status}")
            self.assert_request(request_id, unknown=True, expected_attempts=1, expected_status="succeeded" if expected_protocol == "completed" else "failed", expected_billing=expected_billing, expected_requested_name=models["chat"]["alias"], expected_protocol=expected_protocol, expected_usage=expected_usage)
            self.assert_receipts(self.request_nonce("chat", True, True, fault, case_name), count=1)

        # The mock never emits a first event. Allow the gateway's own 60-second
        # first-event timeout to classify it rather than timing out the client.
        request_id, status, _ = self.request("chat", models["chat"]["alias"], models["chat"]["key"], True, unknown=True, fault="stall_first_event", case_name="stall-first", timeout_seconds=90)
        fault_statuses["stall_first_event"] = status
        if status < 400:
            raise HarnessFailure(f"stall_first_event unexpectedly succeeded: HTTP {status}")
        self.assert_request(request_id, unknown=True, expected_attempts=1, expected_status="failed", expected_billing="released", expected_requested_name=models["chat"]["alias"], expected_protocol="failed", expected_usage="missing")
        self.assert_receipts(self.request_nonce("chat", True, True, "stall_first_event", "stall-first"), count=1)
        print(json.dumps({"status": "passed", "run_id": self.run_id, "requests": len(self.requests), "alias_protocols": 6, "compiled_group_price": True, "fault_http_status": fault_statuses, "database": "127.0.0.1:55439/moni_test", "mock": True}, separators=(",", ":")))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--preflight", action="store_true", help="only verify the running deployment and database boundary")
    parser.add_argument("--run-bound-go-tests", action="store_true", help="run the focused non-PostgreSQL Go bindings with race detection")
    parser.add_argument("--run-postgres-go-tests", action="store_true", help="run only the named PostgreSQL bindings against the dedicated 55439 database")
    parser.add_argument("--list-go-test-bindings", action="store_true", help="print the machine-readable focused Go evidence map")
    args = parser.parse_args()
    harness = Harness(args)
    try:
        if args.list_go_test_bindings:
            print(json.dumps({"pure": PURE_GO_TEST_BINDINGS, "postgres": POSTGRES_GO_TEST_BINDINGS}, indent=2, sort_keys=True))
            return PASS
        if args.run_bound_go_tests:
            run_go_bindings(PURE_GO_TEST_BINDINGS, postgres=False)
            return PASS
        if args.run_postgres_go_tests:
            if harness.test_db != EXPECTED_DB:
                raise HarnessFailure(f"TEST_DATABASE_URL must be exactly {EXPECTED_DB} for explicit PostgreSQL bindings")
            run_go_bindings(POSTGRES_GO_TEST_BINDINGS, postgres=True)
            return PASS
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

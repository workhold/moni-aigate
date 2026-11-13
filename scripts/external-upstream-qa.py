#!/usr/bin/env python3
"""Exercise real New API/Sub2API channels through aigate's public API."""
from __future__ import annotations

import http.cookiejar
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone

BASE = os.environ["AIGATE_BASE_URL"].rstrip("/")
ADMIN_EMAIL = os.environ.get("AIGATE_ADMIN_EMAIL", "admin@example.com")
ADMIN_PASSWORD = os.environ["AIGATE_ADMIN_PASSWORD"]
MODEL_ID = os.environ["AIGATE_MODEL_ID"]
SCHEMA_ID = os.environ["AIGATE_SCHEMA_ID"]
SCHEMA_DIGEST = os.environ["AIGATE_SCHEMA_DIGEST"]
CUSTOMER_KEY = os.environ["AIGATE_CUSTOMER_KEY"]
NEW_API_TOKEN = os.environ["NEW_API_TOKEN"]
SUB2API_TOKEN = os.environ["SUB2API_TOKEN"]
RUN_ID = os.environ.get("AIGATE_RUN_ID", uuid.uuid4().hex[:12])
QA_CONTAINER = os.environ.get("QA_CONTAINER", "local-mvp-external-qa-provider-1")
DB_CONTAINER = os.environ.get("DB_CONTAINER", "local-mvp-external-postgres-1")


class API:
    def __init__(self):
        self.cookies = http.cookiejar.CookieJar()
        self.client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))
        self.csrf = ""

    def call(self, method, path, value=None, csrf=False, customer=False):
        headers = {"Accept": "application/json"}
        data = None
        if value is not None:
            data = json.dumps(value, separators=(",", ":")).encode()
            headers["Content-Type"] = "application/json"
        if customer:
            headers["Authorization"] = "Bearer " + CUSTOMER_KEY
        else:
            headers["Origin"] = BASE
            if csrf:
                headers["X-CSRF-Token"] = self.csrf
        request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
        try:
            with self.client.open(request, timeout=30) as response:
                raw, status, response_headers = response.read(), response.status, response.headers
        except urllib.error.HTTPError as error:
            raw, status, response_headers = error.read(), error.code, error.headers
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = {"raw": raw.decode(errors="replace")}
        return status, parsed, raw, response_headers

    def expect(self, method, path, value=None, csrf=False):
        status, parsed, raw, _ = self.call(method, path, value, csrf)
        if status < 200 or status >= 300:
            raise RuntimeError(f"HTTP {status} {method} {path}: {raw[:800]!r}")
        return parsed.get("data", parsed)

    def login(self):
        self.expect("POST", "/api/auth/login", {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
        me = self.expect("GET", "/api/me")
        self.csrf = me["csrf_token"]
        self.expect("POST", "/api/auth/reauth", {"password": ADMIN_PASSWORD}, csrf=True)


def now_wire():
    return datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def receipts():
    raw = subprocess.check_output(["docker", "exec", QA_CONTAINER, "wget", "-q", "-O", "-", "http://127.0.0.1:8090/__mock/receipts?since=0"], text=True)
    return json.loads(raw)


def request_facts(request_id):
    password = ""
    for line in open("/tmp/local-mvp-external.env", encoding="utf-8"):
        if line.startswith("POSTGRES_PASSWORD="):
            password = line.split("=", 1)[1].strip()
    sql = f"SELECT (SELECT count(*) FROM upstream_attempts WHERE request_id='{request_id}'),(SELECT count(*) FROM settlements WHERE request_id='{request_id}'),(SELECT billing_status FROM request_records WHERE id='{request_id}')"
    env = dict(os.environ, PGPASSWORD=password)
    raw = subprocess.check_output(["docker", "exec", "-e", f"PGPASSWORD={password}", DB_CONTAINER, "psql", "-U", "moni_owner", "-d", "moni", "-At", "-c", sql], text=True, env=env)
    return raw.strip().split("|")


def main():
    api = API()
    api.login()
    channels = api.expect("GET", "/api/platform/channels")
    direct = next(row["id"] for row in channels if row["name"] == os.environ.get("DIRECT_CHANNEL_NAME", "mock-qa-a"))
    created = {}
    for kind, url, secret in (("new_api", "http://new-api:3000/v1", NEW_API_TOKEN), ("sub2api", "http://sub2api:8080/v1", SUB2API_TOKEN)):
        created[kind] = api.expect("POST", "/api/platform/channels", {"name": f"real-{kind}-{RUN_ID}", "base_url": url, "secret": secret, "data_region": "global", "provider": "openai", "channel_kind": kind, "allowed_cidrs": ["172.16.0.0/12"]}, csrf=True)["id"]
    channels = api.expect("GET", "/api/platform/channels")
    revisions = {row["id"]: str(row["management_revision"]) for row in channels}
    for kind in ("new_api", "sub2api"):
        api.expect("POST", "/api/platform/cost-versions", {"operation_id": str(uuid.uuid4()), "channel_id": created[kind], "upstream_model": "qa-compose-chat", "schema_id": SCHEMA_ID, "schema_digest": SCHEMA_DIGEST, "variant_key": "default", "basis": "contract", "input_rate": "1000000", "output_rate": "1000000", "effective_at": now_wire(), "expected_channel_revision": revisions[created[kind]]}, csrf=True)
    bindings = {
        "direct": {"channel_id": direct, "upstream_model": "qa-compose-chat", "priority": 1, "weight": 1, "max_inflight": 10},
        "new_api": {"channel_id": created["new_api"], "upstream_model": "qa-compose-chat", "priority": 1, "weight": 1, "max_inflight": 10},
        "sub2api": {"channel_id": created["sub2api"], "upstream_model": "qa-compose-chat", "priority": 1, "weight": 1, "max_inflight": 10},
    }
    revision = 1
    revision_by_kind = {}
    for selected in ("new_api", "direct", "sub2api"):
        current = [dict(bindings[selected], priority=3), dict(bindings[{"new_api": "direct", "direct": "sub2api", "sub2api": "new_api"}[selected]], priority=2)]
        api.expect("PUT", f"/api/platform/models/{MODEL_ID}/bindings", {"expected_revision": revision, "bindings": current, "cost_policy": {"mode": "manual", "unknown_cost_policy": "allow"}}, csrf=True)
        revision += 1
        revision_by_kind[selected] = revision
        for stream in (False, True):
            nonce = f"{RUN_ID}-{selected}-{'sse' if stream else 'json'}"
            body = {"model": "qa-compose-chat", "messages": [{"role": "user", "content": f"real upstream [[mock:nonce={nonce}]]"}], "max_tokens": 4, "stream": stream}
            status, parsed, raw, headers = api.call("POST", "/v1/chat/completions", body, customer=True)
            if status != 200:
                raise RuntimeError(f"{selected} stream={stream} returned {status}: {raw[:800]!r}")
            request_id = headers.get("x-request-id", "")
            attempts, settlements, billing = request_facts(request_id)
            if attempts != "1" or settlements != "1" or billing != "settled":
                raise RuntimeError(f"{selected} facts {attempts}/{settlements}/{billing}")
            matches = [item for item in receipts() if item.get("nonce") == nonce]
            if len(matches) != 1:
                raise RuntimeError(f"{selected} receipts for {nonce}: {matches}")
    # Roll back to the New API revision and prove the same contract again.
    api.expect("POST", f"/api/platform/models/{MODEL_ID}/route-revisions/{revision_by_kind['new_api']}/activate", {"expected_revision": revision}, csrf=True)
    nonce = f"{RUN_ID}-rollback-new-api"
    status, parsed, raw, headers = api.call("POST", "/v1/chat/completions", {"model": "qa-compose-chat", "messages": [{"role": "user", "content": f"rollback [[mock:nonce={nonce}]]"}], "max_tokens": 4}, customer=True)
    if status != 200:
        raise RuntimeError(f"rollback returned {status}: {raw[:800]!r}")
    attempts, settlements, billing = request_facts(headers.get("x-request-id", ""))
    if attempts != "1" or settlements != "1" or billing != "settled" or len([x for x in receipts() if x.get("nonce") == nonce]) != 1:
        raise RuntimeError("rollback commercial or receipt assertion failed")
    print(json.dumps({"status": "passed", "run_id": RUN_ID, "direct": True, "new_api": True, "sub2api": True, "json_sse": True, "rollback_revision": revision_by_kind["new_api"], "real_upstream_services": True, "supplier_authenticated": False}, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        import traceback
        traceback.print_exc()
        print(f"FAIL: {error}", file=sys.stderr)
        raise SystemExit(1)

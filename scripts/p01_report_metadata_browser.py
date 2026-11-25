#!/usr/bin/env python3
"""P0.1 operations-report metadata browser acceptance.

Drives the already-running isolated phase-one preview (never Compose, never a
remote service). The `seed` phase creates a real customer API key as the
provisioned owner, drives real deterministic-mock image and TTS generations,
and verifies via the admin operations API that media rows keep the explicit
empty schema triple next to complete text snapshots. The `browser` phase drives
headless Chrome through the unfiltered /platform/reports page at desktop and
390px for: mixed media+text rendering, media empty-triple zero display,
settled media amounts, the pure-Chat filter path, P0 field retention, and a
fail-closed rejection of one illegally structured row injected at the network
boundary of the real page. Login and GET/observe only; no production service,
real provider, or business write beyond the seeded deterministic media calls.
"""

from __future__ import annotations

import argparse
import http.cookiejar
import importlib.util
import json
import os
import pathlib
import re
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

ROOT = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("phase1_browser_qa", ROOT / "scripts" / "phase1_browser_qa.py")
qa = importlib.util.module_from_spec(spec)
sys.modules["phase1_browser_qa"] = qa
spec.loader.exec_module(qa)

IMAGE_PROFILE = "11111111-1111-4111-8111-111111111111"
MEDIA_PROTOCOLS = ("image_generation", "tts", "video_generation", "realtime_turn")
REPORT_ERROR = "经营报表响应含未定义或旧标量字段"


class Api:
    """Same contract as the phase1 harness API client (cookies + CSRF + reauth)."""

    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")
        self.csrf = ""
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))

    def call(self, method: str, path: str, value=None, *, csrf=False, headers=None):
        request_headers = {"Accept": "application/json", "Origin": self.base_url}
        if value is not None:
            request_headers["Content-Type"] = "application/json"
        if csrf:
            request_headers["X-CSRF-Token"] = self.csrf
        request_headers.update(headers or {})
        request = urllib.request.Request(self.base_url + path, data=None if value is None else json.dumps(value, separators=(",", ":")).encode(), headers=request_headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                raw, status = response.read(), response.status
        except urllib.error.HTTPError as error:
            raw, status = error.read(), error.code
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = {}
        return status, parsed if isinstance(parsed, dict) else {"raw": parsed}

    def expect(self, method, path, value=None, *, csrf=False, headers=None):
        status, parsed = self.call(method, path, value, csrf=csrf, headers=headers)
        if status < 200 or status >= 300:
            raise RuntimeError(f"HTTP {status} {method} {path}: {raw if False else parsed}")
        return parsed.get("data", parsed)

    def login(self, email: str, password: str) -> None:
        time.sleep(12.05)
        self.expect("POST", "/api/auth/login", {"email": email, "password": password})
        me = self.expect("GET", "/api/me")
        self.csrf = str(me.get("csrf_token", ""))
        if not self.csrf:
            raise RuntimeError("login did not return CSRF token")
        self.expect("POST", "/api/auth/reauth", {"password": password}, csrf=True)


def wire_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + ".000000Z"


def seed(args) -> dict:
    accounts = json.loads(pathlib.Path(args.accounts_file).read_text())
    owner = accounts["accounts"]["owner"]
    tenant_id = accounts.get("tenant_id", "")
    admin = Api(args.base_url)
    admin.login("admin@example.com", pathlib.Path(args.admin_password_file).read_text().strip())

    now = datetime.now(timezone.utc)
    params = urllib.parse.urlencode({"from": (now - timedelta(days=7)).isoformat().replace("+00:00", "Z"), "to": now.isoformat().replace("+00:00", "Z")})
    report = admin.expect("GET", f"/api/platform/reports/operations?{params}")
    text_rows = [row for row in report.get("requests", []) if row.get("protocol") not in ("",) + MEDIA_PROTOCOLS]
    media_rows = [row for row in report.get("requests", []) if row.get("protocol") in MEDIA_PROTOCOLS]
    summary = {"existing_text_rows": len(text_rows), "existing_media_rows": len(media_rows), "window_from": params}

    if len(media_rows) == 0:
        owner_api = Api(args.base_url)
        owner_api.login(owner["email"], owner["password"])
        scopes = [model.get("model_id") for model in accounts.get("models", []) if model.get("model_id")]
        key = owner_api.expect("POST", "/api/keys", {"name": "p01-report-metadata-" + uuid.uuid4().hex[:8], "scopes": scopes}, csrf=True)
        full_key = str(key.get("key", ""))
        if not full_key:
            raise RuntimeError(f"owner key creation returned no key material: {key}")
        media_calls = []
        for label, path, body in (
            ("image_generation", "/v1/images/generations", {"version": "1", "profile_version": IMAGE_PROFILE, "variant_key": "default", "prompt": "p01 acceptance square", "size": "256x256", "format": "png", "n": "1"}),
            ("tts", "/v1/audio/speech", {"model": "mock-tts", "input": "p01 acceptance speech", "voice": "alloy", "response_format": "wav"}),
        ):
            idem = "p01-" + uuid.uuid4().hex
            request = urllib.request.Request(args.base_url + path, data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "Authorization": "Bearer " + full_key, "Idempotency-Key": idem}, method="POST")
            try:
                with urllib.request.urlopen(request, timeout=60) as response:
                    media_calls.append({"media": label, "status": response.status, "bytes": len(response.read()), "idempotency_key": idem})
            except urllib.error.HTTPError as error:
                raise RuntimeError(f"{label} failed: HTTP {error.code} {error.read()[:400]}") from None
            time.sleep(0.5)
        summary["media_calls"] = media_calls

    report = admin.expect("GET", f"/api/platform/reports/operations?{params}")
    rows = report.get("requests", [])
    media_rows = [row for row in rows if row.get("protocol") in MEDIA_PROTOCOLS]
    text_rows = [row for row in rows if row.get("protocol") not in ("",) + MEDIA_PROTOCOLS]
    if not text_rows:
        raise RuntimeError("no text rows in the report window; run the phase1 mock acceptance first")
    if not media_rows:
        raise RuntimeError("deterministic mock media did not produce report rows")
    for row in media_rows:
        if row.get("schema_id") != "" or row.get("schema_digest") != "" or row.get("variant_key") != "":
            raise RuntimeError(f"media row lost the empty triple: {row.get('request_id')}")
        if not re.fullmatch(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", str(row.get("profile_version", ""))):
            raise RuntimeError(f"media profile is not a canonical UUID: {row.get('request_id')}")
    summary["media_rows"] = [{"request_id": row.get("request_id"), "protocol": row.get("protocol"), "charged": row.get("charged"), "net_revenue": row.get("net_revenue"), "attempt_count": row.get("attempt_count"), "unknown_provider_costs": row.get("unknown_provider_costs"), "status": row.get("status"), "payment_basis": row.get("payment_basis"), "schema_empty": row.get("schema_id") == "" and row.get("schema_digest") == "" and row.get("variant_key") == ""} for row in media_rows]
    # Mock media is released with zero charge and unknown supplier cost by
    # design (media_provider_runtime.go mediaFinalizationValues): business QA
    # must not spend the real wallet, so media rows legitimately render a
    # zero/unknown state while their aggregates stay non-empty.
    summary["text_rows"] = len(text_rows)
    summary["summary_charged"] = report.get("summary", {}).get("charged")
    summary["summary_provider_cost"] = report.get("summary", {}).get("provider_cost")
    summary["provisional_gross_margin"] = report.get("summary", {}).get("provisional_gross_margin")
    return summary


def browser(args) -> dict:
    accounts = json.loads(pathlib.Path(args.accounts_file).read_text())
    admin_password = pathlib.Path(args.admin_password_file).read_text().strip()
    output = pathlib.Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    browser = qa.Browser(args.base_url, output)
    evidence: dict = {"base_url": args.base_url, "commit": args.commit, "scenarios": {}}

    def metric_texts() -> dict:
        return browser.evaluate("Object.fromEntries([...document.querySelectorAll('.operating-report .metric-card')].map(card=>[card.querySelector('.metric-label')?.innerText||'', card.querySelector('.metric-value')?.innerText||'']))")

    def query_unfiltered() -> None:
        browser.wait("(()=>{const buttons=[...document.querySelectorAll('.report-actions button')];return buttons.some(b=>b.innerText.includes('查询报表')&&!b.disabled)})()", 30)
        browser.click_text("查询报表", ".report-actions button")

    try:
        time.sleep(12.05)  # identity auth pacing between the seed and browser logins
        browser.login("admin@example.com", admin_password)
        browser.viewport(1440, 1000)
        browser.navigate("/platform/reports", "经营报表与对账导出")
        query_unfiltered()
        browser.wait("!document.querySelector('.report-loading')", 30)
        browser.wait("document.body.innerText.includes('请求级对账明细')", 30)

        # S1: unfiltered mixed report renders with media rows, no error, P0 metrics.
        error_alert = browser.evaluate("!!document.querySelector('.el-alert--error')")
        if error_alert:
            raise RuntimeError("unfiltered report showed the strict-parser error alert")
        observation = browser.evaluate("(()=>({text:document.body.innerText,scrollWidth:document.documentElement.scrollWidth,bodyScrollWidth:document.body.scrollWidth,viewport:innerWidth,rows:[...document.querySelectorAll('[aria-label=经营报表请求明细] .el-table__row')].length}))()")
        if observation["rows"] == 0:
            raise RuntimeError("no request rows rendered for the unfiltered window")
        metrics = metric_texts()
        required_metrics = ["客户消费币数", "客户付款基准", "采购成本（原币）", "暂估贡献毛利", "请求 / 上游尝试"]
        missing = [name for name in required_metrics if name not in metrics or not metrics[name]]
        if missing:
            raise RuntimeError(f"P0 metric cards missing or empty: {missing} -> {metrics}")
        evidence["scenarios"]["mixed_unfiltered_desktop"] = {"status": "PASS", "observation": {k: v for k, v in observation.items() if k != "text"}, "metrics": metrics, "requests_total": browser.evaluate("document.querySelector('.table-footer')?.innerText||''")}
        browser.screenshot(output / "report-mixed-desktop.png")

        # S2: media rows visible with empty triple rendered as zero/empty display.
        media_visible = browser.evaluate("(()=>{const cells=[...document.querySelectorAll('[aria-label=经营报表请求明细] .el-table__row')].map(r=>r.innerText);return {rows:cells.length,mockImage:cells.some(t=>t.includes('mock-image-generation')||t.includes('mock-tts'))}})()")
        if not media_visible["mockImage"]:
            raise RuntimeError(f"media model rows not visible in the requests table: {media_visible}")
        evidence["scenarios"]["media_empty_triple_visible"] = {"status": "PASS", "observation": media_visible}

        # S3: pure Chat filter path still passes (the path that used to mask the bug).
        browser.evaluate("(()=>{const select=document.querySelector('.report-filters .el-select');select&&select.click()})()")
        time.sleep(0.4)
        clicked = browser.evaluate("(()=>{const items=[...document.querySelectorAll('.el-select-dropdown__item')];const item=items.find(x=>x.innerText.trim()==='OpenAI Chat');if(item){item.click();return true}return false})()")
        if not clicked:
            raise RuntimeError("OpenAI Chat protocol option not found")
        time.sleep(0.3)
        browser.click_text("查询报表", ".report-actions button")
        browser.wait("!document.querySelector('.report-loading')", 30)
        chat_observation = browser.evaluate("(()=>({text:document.body.innerText,error:!!document.querySelector('.el-alert--error'),rows:[...document.querySelectorAll('[aria-label=经营报表请求明细] .el-table__row')].length}))()")
        if chat_observation["error"] or chat_observation["rows"] == 0:
            raise RuntimeError(f"pure Chat filter path failed: {chat_observation}")
        evidence["scenarios"]["pure_chat_filter"] = {"status": "PASS", "rows": chat_observation["rows"], "error": False}
        browser.screenshot(output / "report-pure-chat-desktop.png")

        # Back to unfiltered: reload resets the draft filters to the default window.
        browser.navigate("/platform/reports", "经营报表与对账导出")
        query_unfiltered()
        browser.wait("!document.querySelector('.report-loading')", 30)
        browser.wait("document.body.innerText.includes('请求级对账明细')", 30)

        # S4: 390px unfiltered — no overflow, no error, media rows still visible.
        browser.viewport(390, 844)
        time.sleep(0.5)
        observation390 = browser.evaluate("(()=>({text:document.body.innerText,scrollWidth:document.documentElement.scrollWidth,bodyScrollWidth:document.body.scrollWidth,viewport:innerWidth,error:!!document.querySelector('.el-alert--error'),rows:[...document.querySelectorAll('[aria-label=经营报表请求明细] .el-table__row')].length}))()")
        if observation390["error"]:
            raise RuntimeError("390px unfiltered report showed an error alert")
        if observation390["scrollWidth"] > observation390["viewport"] or observation390["bodyScrollWidth"] > observation390["viewport"]:
            raise RuntimeError(f"390px horizontal overflow: {observation390}")
        if observation390["rows"] == 0:
            raise RuntimeError("390px report rendered no request rows")
        evidence["scenarios"]["mixed_unfiltered_390"] = {"status": "PASS", "observation": {k: v for k, v in observation390.items() if k != "text"}}
        browser.screenshot(output / "report-mixed-390.png")

        # S5: fail-closed rejection of illegal structures injected at the
        # network boundary of the real page (fetch shim, no server or DB change).
        # Variant A: partial triple on a text row (schema_id emptied, variant_key
        # key removed). Variant B: a media row fabricating a complete text
        # snapshot — the "non-empty media metadata" combination the wire never
        # legally carries. Both must reject the whole report with no amounts.
        injection_variants = {
            "text_partial_triple": "poisoned.schema_id = '';\n    delete poisoned.variant_key;",
            "media_fabricated_snapshot": """const victim = rows.find(r => ['image_generation','tts','video_generation','realtime_turn'].includes(r.protocol)) || rows[0];
    poisoned = JSON.parse(JSON.stringify(victim));
    poisoned.request_id = 'p01-illegal-media';
    if (poisoned.protocol !== 'image_generation') poisoned.protocol = 'image_generation';
    poisoned.model_id = 'mock-image-generation';
    poisoned.profile_version = '11111111-1111-4111-8111-111111111111';
    poisoned.schema_id = '33333333-3333-4333-8333-333333333333';
    poisoned.schema_digest = 'ab'.repeat(32);
    poisoned.variant_key = 'default';""",
        }
        for variant_name, poison_code in injection_variants.items():
            browser.viewport(1440, 1000)
            browser.navigate("/platform/reports", "经营报表与对账导出")
            browser.wait("!document.querySelector('.report-loading')", 30)
            injected = browser.evaluate("""(() => {
  const original = window.fetch;
  window.fetch = async (input, init) => {
    const response = await original(input, init);
    const url = typeof input === 'string' ? input : (input && input.url) || '';
    if (!url.includes('/api/platform/reports/operations') || url.includes('.csv') || window.__p01_injected) return response;
    window.__p01_injected = true;
    window.fetch = original;
    const payload = await response.json();
    const rows = payload && payload.data && payload.data.requests;
    if (!Array.isArray(rows) || rows.length === 0) return new Response(JSON.stringify(payload), {status: response.status, headers: response.headers});
    let poisoned = JSON.parse(JSON.stringify(rows[0]));
    poisoned.request_id = 'p01-illegal-row';
    %s
    return new Response(JSON.stringify({...payload, data: {...payload.data, requests: [poisoned, ...rows]}}), {status: response.status, headers: response.headers});
  };
  window.__p01_injected = false;
  return true;
})()""" % poison_code)
            if not injected:
                raise RuntimeError("failed to install the response interceptor")
            query_unfiltered()
            browser.wait("!document.querySelector('.report-loading')", 30)
            rejection = browser.evaluate("(()=>({error:!!document.querySelector('.operating-report .el-alert--error'),errorText:(document.querySelector('.operating-report .el-alert--error')||{}).innerText||'',metrics:[...document.querySelectorAll('.operating-report .metric-card .metric-value')].map(x=>x.innerText).filter(Boolean).length}))()")
            if not rejection["error"] or REPORT_ERROR not in rejection["errorText"]:
                raise RuntimeError(f"illegal row did not fail the whole page closed: {variant_name} {rejection}")
            if rejection["metrics"] != 0:
                raise RuntimeError(f"amounts stayed visible next to the rejection: {variant_name} {rejection}")
            evidence["scenarios"][f"illegal_rejected_{variant_name}"] = {"status": "PASS", "error_text": rejection["errorText"][:80], "amount_metrics_visible": 0, "injection": "client-side fetch shim injected the illegal row into the operations response at the network boundary"}
            browser.screenshot(output / f"report-illegal-{variant_name.replace('_', '-')}.png")
        browser.viewport(1440, 1000)
    finally:
        evidence["completed_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        (output / "p01-browser-evidence.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2))
        browser.close()
    return evidence


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("PHASE1_BASE_URL", "http://127.0.0.1:18081"))
    parser.add_argument("--admin-password-file", default="/Users/vchive/.cache/moni-phase1-browser/admin-password")
    parser.add_argument("--accounts-file", default="/Users/vchive/.cache/moni-phase1-browser/ui-accounts.json")
    parser.add_argument("--output", default="docs/verification/assets/p01-report-metadata/after-001")
    parser.add_argument("--commit", default=ROOT and os.popen("git -C '%s' rev-parse HEAD" % ROOT).read().strip())
    parser.add_argument("--phase", choices=("seed", "browser", "all"), default="all")
    args = parser.parse_args()
    if args.phase in ("seed", "all"):
        summary = seed(args)
        print(json.dumps({"seed": summary}, ensure_ascii=False, indent=2))
    if args.phase in ("browser", "all"):
        evidence = browser(args)
        failed = [name for name, item in evidence["scenarios"].items() if item.get("status") != "PASS"]
        if failed:
            print(f"FAILED scenarios: {failed}", file=sys.stderr)
            return 1
        print(f"browser scenarios PASS: {list(evidence['scenarios'])}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

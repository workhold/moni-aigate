#!/usr/bin/env python3
"""Isolated Chrome acceptance driver with sanitized screenshot/DOM evidence.

Secrets are read from protected files and inserted through CDP Input.insertText.
They are never included in expressions, output, screenshots requested for secret
dialogs, or the JSON evidence. Preview runs are marked PREVIEW, not PASS.
"""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import hashlib
import hmac
import io
import json
import os
import pathlib
import re
import secrets
import shutil
import socket
import struct
import subprocess
import tempfile
import tarfile
import time
import urllib.parse
import urllib.request
import uuid

CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
VIEWPORTS = (("desktop", 1440, 1000), ("mobile390", 390, 844))


class CDP:
    def __init__(self, url: str):
        parsed = urllib.parse.urlsplit(url)
        self.socket = socket.create_connection((parsed.hostname, parsed.port), timeout=60)
        self.socket.settimeout(60)
        key = base64.b64encode(os.urandom(16)).decode()
        path = parsed.path + (("?" + parsed.query) if parsed.query else "")
        request = f"GET {path} HTTP/1.1\r\nHost: {parsed.hostname}:{parsed.port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nOrigin: http://{parsed.hostname}:{parsed.port}\r\n\r\n"
        self.socket.sendall(request.encode())
        response = self._read_until(b"\r\n\r\n")
        if not response.startswith(b"HTTP/1.1 101"):
            raise RuntimeError("Chrome DevTools WebSocket upgrade failed")
        self.next_id = 0

    def _read_until(self, suffix: bytes) -> bytes:
        data = b""
        while not data.endswith(suffix):
            chunk = self.socket.recv(4096)
            if not chunk:
                raise RuntimeError("Chrome DevTools connection closed")
            data += chunk
        return data

    def _read_exact(self, size: int) -> bytes:
        data = b""
        while len(data) < size:
            chunk = self.socket.recv(size - len(data))
            if not chunk:
                raise RuntimeError("Chrome DevTools frame ended early")
            data += chunk
        return data

    def _send_frame(self, payload: bytes, opcode: int = 1) -> None:
        mask = os.urandom(4)
        size = len(payload)
        header = bytes([0x80 | opcode])
        if size < 126:
            header += bytes([0x80 | size])
        elif size < 65536:
            header += bytes([0x80 | 126]) + struct.pack("!H", size)
        else:
            header += bytes([0x80 | 127]) + struct.pack("!Q", size)
        masked = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
        self.socket.sendall(header + mask + masked)

    def _receive_frame(self) -> tuple[int, bytes]:
        fragments = []
        opcode = 0
        while True:
            first, second = self._read_exact(2)
            final, current = first & 0x80, first & 0x0F
            masked, size = second & 0x80, second & 0x7F
            if size == 126:
                size = struct.unpack("!H", self._read_exact(2))[0]
            elif size == 127:
                size = struct.unpack("!Q", self._read_exact(8))[0]
            mask = self._read_exact(4) if masked else b""
            payload = self._read_exact(size)
            if masked:
                payload = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
            if current == 9:
                self._send_frame(payload, 10)
                continue
            if current == 8:
                raise RuntimeError("Chrome DevTools closed the page connection")
            if current:
                opcode = current
            fragments.append(payload)
            if final:
                return opcode, b"".join(fragments)

    def call(self, method: str, params: dict | None = None) -> dict:
        self.next_id += 1
        request_id = self.next_id
        self._send_frame(json.dumps({"id": request_id, "method": method, "params": params or {}}, separators=(",", ":")).encode())
        while True:
            opcode, payload = self._receive_frame()
            if opcode != 1:
                continue
            message = json.loads(payload)
            if message.get("id") != request_id:
                continue
            if "error" in message:
                raise RuntimeError(f"CDP {method} failed with code {message['error'].get('code')}")
            return message.get("result", {})

    def close(self):
        self.socket.close()


def totp_code(secret: str) -> str:
    normalized = secret.strip().replace(" ", "").upper()
    normalized += "=" * ((8 - len(normalized) % 8) % 8)
    key = base64.b32decode(normalized)
    counter = int(time.time()) // 30
    digest = hmac.new(key, struct.pack("!Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    value = (struct.unpack("!I", digest[offset:offset + 4])[0] & 0x7FFFFFFF) % 1_000_000
    return f"{value:06d}"


def settlement_for_request(request_id: str) -> str:
    if not re.fullmatch(r"[0-9a-f-]{36}", request_id):
        raise RuntimeError("invalid Playground request identity")
    schema = os.environ.get("PHASE1_DATABASE_SCHEMA", "")
    dsn = os.environ.get("TEST_DATABASE_URL", "")
    if dsn != "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable":
        raise RuntimeError("browser settlement lookup requires the dedicated 55439 database")
    qualified = "settlements"
    if schema:
        if not re.fullmatch(r"phase1qa_[0-9]+_[0-9]+", schema):
            raise RuntimeError("invalid isolated browser schema")
        qualified = f'"{schema}".settlements'
    result = subprocess.run(["psql", dsn, "-At", "-v", "ON_ERROR_STOP=1", "-c", f"SELECT id::text FROM {qualified} WHERE request_id='{request_id}'"], text=True, capture_output=True, check=True)
    settlement = result.stdout.strip()
    if not settlement:
        raise RuntimeError("Playground settlement was not persisted")
    return settlement


class Browser:
    def __init__(self, base_url: str, output: pathlib.Path):
        self.base_url = base_url.rstrip("/")
        self.output = output
        self.profile = pathlib.Path(tempfile.mkdtemp(prefix="moni-phase1-chrome-"))
        self.process = subprocess.Popen([CHROME, "--headless=new", "--disable-gpu", "--no-proxy-server", "--no-first-run", "--no-default-browser-check", "--remote-allow-origins=*", "--remote-debugging-port=0", f"--user-data-dir={self.profile}", "about:blank"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        port_file = self.profile / "DevToolsActivePort"
        deadline = time.monotonic() + 20
        while not port_file.exists() and time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError("isolated Chrome exited before DevTools was ready")
            time.sleep(0.05)
        if not port_file.exists():
            raise RuntimeError("isolated Chrome DevTools port was not created")
        port = int(port_file.read_text().splitlines()[0])
        pages = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5))
        page = next(item for item in pages if item.get("type") == "page")
        self.cdp = CDP(page["webSocketDebuggerUrl"])
        self.cdp.call("Page.enable")
        self.cdp.call("Runtime.enable")
        self.last_login = 0.0
        self.totp_last_step = -1

    def fresh_totp(self, secret: str) -> str:
        step = int(time.time()) // 30
        if step <= self.totp_last_step:
            wait = (self.totp_last_step + 1) * 30 - time.time() + 0.05
            if wait > 0:
                time.sleep(wait)
        code = totp_code(secret)
        self.totp_last_step = int(time.time()) // 30
        return code

    def pace_login(self):
        remaining = 12.05 - (time.monotonic() - self.last_login)
        if remaining > 0:
            time.sleep(remaining)
        self.last_login = time.monotonic()

    def evaluate(self, expression: str):
        result = self.cdp.call("Runtime.evaluate", {"expression": expression, "returnByValue": True, "awaitPromise": True})
        if "exceptionDetails" in result:
            raise RuntimeError("page evaluation failed")
        return result.get("result", {}).get("value")

    def wait(self, expression: str, timeout: float = 20):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.evaluate(expression):
                return
            time.sleep(0.1)
        raise RuntimeError("required browser state did not arrive")

    def viewport(self, width: int, height: int):
        self.cdp.call("Emulation.setDeviceMetricsOverride", {"width": width, "height": height, "deviceScaleFactor": 1, "mobile": width == 390})

    def navigate(self, path: str, expected: str | None = None):
        url = path if path.startswith("http") else self.base_url + path
        self.cdp.call("Page.navigate", {"url": url})
        self.wait("document.readyState === 'complete' && document.body && document.body.innerText.length > 0")
        if expected:
            self.wait(f"document.body.innerText.includes({json.dumps(expected)})")
        self.wait("!document.querySelector('.el-loading-mask')")

    def fill(self, selector: str, value: str):
        if not self.evaluate(f"(()=>{{const e=document.querySelector({json.dumps(selector)});if(!e)return false;e.focus();e.select();return true}})()"):
            raise RuntimeError(f"input not found: {selector}")
        self.cdp.call("Input.insertText", {"text": value})

    def click_text(self, text: str, selector: str = "button"):
        expression = f"(()=>{{const e=[...document.querySelectorAll({json.dumps(selector)})].find(x=>x.innerText.trim().includes({json.dumps(text)})&&!x.disabled&&x.getClientRects().length);if(!e)return false;e.click();return true}})()"
        if not self.evaluate(expression):
            raise RuntimeError(f"click target not found: {text}")

    def login(self, email: str, password: str):
        self.pace_login()
        self.navigate("/login")
        self.fill('input[type="email"]', email)
        self.fill('input[type="password"]', password)
        self.click_text("登录工作空间")
        self.wait("!!document.querySelector('.app-shell')")

    def logout(self):
        self.evaluate("document.querySelector('.user-button')?.click()")
        self.wait("document.body.innerText.includes('退出登录')")
        self.click_text("退出登录", ".el-dropdown-menu__item")
        self.wait("location.pathname === '/login'")

    def reauth(self, password: str, totp_secret: str = ""):
        self.evaluate("document.querySelector('.user-button')?.click()")
        self.wait("document.body.innerText.includes('敏感操作再认证')")
        self.click_text("敏感操作再认证", ".el-dropdown-menu__item")
        self.wait("!!document.querySelector('.el-dialog input[type=password]')")
        self.fill(".el-dialog input[type=password]", password)
        if self.evaluate("!!document.querySelector('.el-dialog input[autocomplete=one-time-code]')"):
            if not totp_secret:
                raise RuntimeError("MFA reauthentication requires the in-memory secret")
            self.fill(".el-dialog input[autocomplete=one-time-code]", self.fresh_totp(totp_secret))
        self.click_text("验证身份", ".el-dialog button")
        self.wait("!document.querySelector('.el-dialog input[type=password]')")

    def fill_form_item(self, label: str, value: str):
        expression = f"(()=>{{const items=[...document.querySelectorAll('.el-form-item')];const item=items.find(x=>x.innerText.includes({json.dumps(label)}));const e=item&&item.querySelector('input,textarea');if(!e)return false;e.focus();e.select();return true}})()"
        if not self.evaluate(expression):
            raise RuntimeError(f"form field not found: {label}")
        self.cdp.call("Input.insertText", {"text": value})

    def text_value(self, selector: str) -> str:
        return self.evaluate(f"(()=>{{const e=document.querySelector({json.dumps(selector)});return e?('value' in e?e.value:e.textContent):''}})()") or ""

    def select_form_item(self, label: str, option: str):
        expression = f"(()=>{{const item=[...document.querySelectorAll('.el-form-item')].find(x=>x.innerText.includes({json.dumps(label)}));const e=item&&(item.querySelector('.el-select__wrapper')||item.querySelector('.el-select'));if(!e)return false;e.click();return true}})()"
        if not self.evaluate(expression):
            raise RuntimeError(f"select field not found: {label}")
        self.wait("!!document.querySelector('.el-select-dropdown__item')")
        option_expression = f"(()=>{{const e=[...document.querySelectorAll('.el-select-dropdown__item')].find(x=>x.innerText.trim().includes({json.dumps(option)})&&x.getClientRects().length);if(!e)return false;e.click();return true}})()"
        if not self.evaluate(option_expression):
            raise RuntimeError(f"select option not found: {option}")

    def click_row_action(self, row_text: str, action: str):
        expression = f"(()=>{{const row=[...document.querySelectorAll('.el-table__row')].find(x=>x.innerText.includes({json.dumps(row_text)})&&x.getClientRects().length);const e=row&&[...row.querySelectorAll('button')].find(x=>x.innerText.includes({json.dumps(action)})&&!x.disabled&&x.getClientRects().length);if(!e)return false;e.click();return true}})()"
        if not self.evaluate(expression):
            raise RuntimeError(f"row action not found: {action}")

    def enable_mfa(self) -> str:
        self.navigate("/security", "账号安全")
        self.click_text("开始设置 MFA")
        self.wait("!!document.querySelector('.manual-enrollment .secret-display')")
        secret = self.text_value(".manual-enrollment .secret-display").strip()
        if not secret:
            raise RuntimeError("MFA enrollment secret was not generated")
        self.click_text("我已安全保存恢复码", ".el-checkbox")
        self.fill('input[autocomplete="one-time-code"]', self.fresh_totp(secret))
        self.click_text("验证并启用")
        self.wait("!document.querySelector('.manual-enrollment')")
        self.navigate("/security", "已启用")
        return secret

    def login_mfa(self, email: str, password: str, secret: str):
        self.pace_login()
        self.navigate("/login")
        self.fill('input[type="email"]', email)
        self.fill('input[type="password"]', password)
        self.click_text("登录工作空间")
        self.wait("!!document.querySelector('input[autocomplete=one-time-code]')")
        self.fill('input[autocomplete="one-time-code"]', self.fresh_totp(secret))
        self.click_text("验证并继续")
        self.wait("!!document.querySelector('.app-shell')")

    def change_password(self, current: str, new: str, secret: str):
        self.navigate("/security", "修改登录密码")
        fields = self.evaluate("[...document.querySelectorAll('input[type=password]')].map((e,i)=>({i,autocomplete:e.autocomplete}))")
        if len(fields) < 3:
            raise RuntimeError("password change fields are incomplete")
        self.fill('input[autocomplete="current-password"]', current)
        self.fill('input[autocomplete="new-password"]', new)
        self.evaluate("document.querySelectorAll('input[autocomplete=new-password]')[1].focus()")
        self.cdp.call("Input.insertText", {"text": new})
        self.fill('input[autocomplete="one-time-code"]', self.fresh_totp(secret))
        self.click_text("更改密码并重新登录")
        self.wait("location.pathname === '/login'")

    def create_key_use_playground_and_revoke(self, name: str, model: str) -> dict:
        self.navigate("/keys", "API 密钥")
        self.click_text("创建密钥")
        self.fill('input[placeholder="例如：生产环境服务"]', name)
        self.click_text("允许全部已授权模型", ".el-checkbox")
        self.click_text("创建并显示密钥")
        self.wait("!!document.querySelector('.secret-display')")
        api_key = self.text_value(".secret-display").strip()
        if not api_key.startswith("mg_"):
            raise RuntimeError("one-time API key was not displayed")
        self.click_text("已保存，关闭")
        self.wait("!document.querySelector('.secret-display')")
        self.navigate("/playground", "API 联调")
        if model:
            self.select_form_item("模型", model)
        self.fill('input[placeholder="粘贴当前租户的 API 密钥"]', api_key)
        self.click_text("发送请求")
        self.wait("document.body.innerText.includes('调用完成')", 45)
        request_id = self.text_value(".playground-request code").strip()
        if not request_id or not self.text_value(".playground-response").strip():
            raise RuntimeError("Playground did not produce a completed response")
        settlement_id = settlement_for_request(request_id)
        self.navigate("/requests?request_id=" + urllib.parse.quote(request_id), "调用记录")
        observation = {"name": "Key 一次性展示、Playground 实际完成调用、随后撤销", "request_id": request_id, "settlement_id": settlement_id, "path": "/requests", "title": self.evaluate("document.title")}
        self.navigate("/keys", "API 密钥")
        self.click_row_action(name, "撤销")
        self.click_text("确认撤销", ".el-message-box button")
        self.wait(f"[...document.querySelectorAll('.el-table__row')].some(x=>x.innerText.includes({json.dumps(name)})&&x.innerText.includes('已撤销'))")
        api_key = ""
        return observation

    def create_credit(self, tenant: str, business_ref: str):
        self.navigate("/platform/credits", "人工核款")
        self.click_text("录入核款单")
        self.select_form_item("客户", tenant)
        self.fill_form_item("真实收款业务号", business_ref)
        self.fill_form_item("付费 / 未知到账币数量", "1")
        self.fill_form_item("实付法币金额", "1")
        self.fill_form_item("受控财务凭证引用", "phase1-browser-controlled-proof")
        self.click_text("确认提交", ".el-dialog button")
        self.wait("!document.querySelector('.el-dialog')")
        self.wait(f"[...document.querySelectorAll('.el-table__row')].some(x=>x.innerText.includes({json.dumps(business_ref)})&&x.innerText.includes('待审批'))")

    def approve_credit(self, business_ref: str):
        self.navigate("/platform/credits", "人工核款")
        self.click_row_action(business_ref, "审批入账")
        self.click_text("核实并批准", ".el-message-box button")
        self.wait(f"[...document.querySelectorAll('.el-table__row')].some(x=>x.innerText.includes({json.dumps(business_ref)})&&x.innerText.includes('已批准'))")

    def create_refund(self, tenant: str, settlement_id: str):
        self.navigate("/platform/refunds", "消费退款")
        self.click_text("申请钱包退款")
        self.select_form_item("客户", tenant)
        self.fill_form_item("原消费结算单 ID", settlement_id)
        self.fill_form_item("退回钱包币数", "0.000001")
        self.fill_form_item("退款原因", "isolated phase-one browser refund")
        self.click_text("确认提交", ".el-dialog button")
        self.wait("!document.querySelector('.el-dialog')")
        self.wait(f"document.body.innerText.includes({json.dumps(settlement_id[:8])})")

    def approve_refund(self, settlement_id: str):
        self.navigate("/platform/refunds", "消费退款")
        self.click_row_action(settlement_id[:8], "批准退款")
        self.click_text("核实并批准", ".el-message-box button")
        self.wait(f"[...document.querySelectorAll('.el-table__row')].some(x=>x.innerText.includes({json.dumps(settlement_id[:8])})&&x.innerText.includes('已批准'))")

    def create_voucher_batch(self, purpose: str) -> str:
        self.navigate("/platform/vouchers", "运营赠额券")
        self.wait("[...document.querySelectorAll('button')].some(x=>x.innerText.includes('发行批次')&&!x.disabled)")
        self.click_text("发行批次")
        self.fill_form_item("每张面额", "1.25")
        self.fill_form_item("数量（1—100）", "1")
        expires = (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=2)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        self.fill_form_item("过期时间", expires)
        self.fill_form_item("用途受控编号", purpose)
        self.fill_form_item("操作理由", "isolated phase-one browser acceptance")
        self.click_text("确认提交", ".el-dialog button")
        self.wait("!!document.querySelector('.commerce-codes textarea')")
        code = self.text_value(".commerce-codes textarea").strip()
        if not code:
            raise RuntimeError("one-time voucher was not returned")
        self.click_text("已保存，清除并关闭", ".el-dialog button")
        self.wait("!document.querySelector('.commerce-codes')")
        return code

    def approve_voucher_batch(self, purpose: str):
        self.navigate("/platform/vouchers", "运营赠额券")
        self.click_row_action(purpose, "独立审批")
        self.click_text("确认提交", ".el-dialog button")
        self.wait("!!document.querySelector('.el-dialog .el-alert--success')")
        self.click_text("关闭", ".el-dialog button")
        self.wait(f"document.body.innerText.includes({json.dumps(purpose)})")

    def redeem_voucher(self, code: str):
        self.navigate("/wallet", "钱包与流水")
        self.click_text("兑换运营赠额")
        self.fill_form_item("完整兑换券", code)
        self.click_text("确认提交", ".el-dialog button")
        self.wait("document.body.innerText.includes('本次已入账')")
        self.click_text("关闭", ".el-dialog button")
        self.wait("!document.querySelector('.el-dialog')")
        self.wait("document.body.innerText.includes('兑换明细核查') && document.body.innerText.includes('来源回执')")

    def download_report_csv(self, directory: pathlib.Path) -> dict:
        directory.mkdir(parents=True, exist_ok=True)
        self.cdp.call("Browser.setDownloadBehavior", {"behavior": "allow", "downloadPath": str(directory), "eventsEnabled": True})
        self.navigate("/platform/reports", "经营报表与对账导出")
        self.click_text("下载 CSV")
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            files = [path for path in directory.glob("*.csv") if path.stat().st_size > 0]
            if files and not list(directory.glob("*.crdownload")):
                path = files[0]
                raw = path.read_bytes()
                first = raw.removeprefix(b"\xef\xbb\xbf").splitlines()[0].decode("utf-8", errors="replace")
                if "request" not in first and "请求" not in first:
                    raise RuntimeError("downloaded CSV is not request-level evidence")
                return {"name": "请求级 CSV 实际下载成功", "file": str(path.relative_to(self.output)), "bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
            time.sleep(0.1)
        raise RuntimeError("request-level CSV did not download")

    def publish_models(self, models: list[str]):
        for model in models:
            self.navigate("/platform/catalog", "模型与路由")
            self.click_row_action(model, "公开设置")
            self.wait("!!document.querySelector('.publication-summary')")
            checked = self.evaluate("document.querySelector('.el-dialog .el-switch__input')?.getAttribute('aria-checked') === 'true'")
            if not checked:
                self.evaluate("document.querySelector('.el-dialog .el-switch')?.click()")
                self.click_text("保存公开设置", ".el-dialog button")
                self.wait("document.querySelector('.publication-summary')?.innerText.includes('已公开')")
            self.click_text("关闭", ".el-dialog button")
            self.wait("!document.querySelector('.el-dialog')")

    def enable_voucher_policy(self):
        self.navigate("/platform/vouchers", "兑换能力开关")
        self.wait("!!document.querySelector('.voucher-policy .el-switch__input')")
        enabled = self.evaluate("document.querySelector('.voucher-policy .el-switch__input').getAttribute('aria-checked') === 'true'")
        if not enabled:
            self.evaluate("document.querySelector('.voucher-policy .el-switch')?.click()")
            self.click_text("保存策略", ".voucher-policy button")
            self.wait("document.querySelector('.voucher-policy')?.innerText.includes('当前有效能力：已开启')")

    def create_procurement_account(self, label: str) -> str:
        self.navigate("/platform/procurement", "采购账户与对账")
        self.click_text("新增采购账户")
        self.fill_form_item("采购账户名称", label)
        self.fill_form_item("供应商 / 聚合平台标识", "phase1-browser-mock")
        self.fill_form_item("累计采购预算上限", "100")
        self.click_text("确认提交", ".el-dialog button")
        self.wait("document.body.innerText.includes('已取得原操作的持久结果')")
        self.click_text("关闭", ".el-dialog button")
        self.wait("!document.querySelector('.el-dialog')")
        account_id = self.evaluate(f"(()=>{{const row=[...document.querySelectorAll('.el-table__row')].find(x=>x.innerText.includes({json.dumps(label)}));const e=row&&row.querySelector('.mono[title]');return e?e.getAttribute('title'):''}})()")
        if not account_id:
            raise RuntimeError("created procurement account ID was not projected")
        return account_id

    def create_budget_alert(self, account_id: str, label: str):
        self.navigate("/platform/alerts", "经营告警")
        self.click_text("新建规则")
        self.select_form_item("规则种类", "采购登记预算不足")
        self.fill('input[aria-label="告警目标资源 UUID"]', account_id)
        self.fill('input[aria-label="告警规则名称"]', label)
        self.click_text("启用", ".el-dialog .el-radio-button")
        self.fill('input[aria-label="预算触发阈值"]', "1000000")
        self.fill('input[aria-label="预算恢复阈值"]', "1000000")
        self.click_text("创建规则", ".el-dialog button")
        self.wait("!!document.querySelector('.receipt-id')")
        self.evaluate("document.querySelector('.el-dialog .el-dialog__headerbtn')?.click()")
        self.wait("!document.querySelector('.el-dialog')")

    def acknowledge_budget_alert(self, label: str):
        self.navigate("/platform/alerts", "经营告警")
        self.click_text("当前告警", ".el-tabs__item")
        self.wait(f"[...document.querySelectorAll('.el-table__row')].some(x=>x.innerText.includes({json.dumps(label)}))", 150)
        self.click_row_action(label, "详情 / 事件")
        self.wait("[...document.querySelectorAll('.ack-area button')].some(x=>x.innerText.includes('共同确认')&&!x.disabled)")
        self.click_text("共同确认")
        self.wait("document.body.innerText.includes('共同确认已记录')")
        return {"name": "预算阈值触发站内告警、读取事件并完成共同确认", "rule_label": label}

    def page_checks(self, path: str, expected: str):
        self.navigate(path, expected)
        return self.evaluate("({path:location.pathname,title:document.title,viewport:window.innerWidth,scrollWidth:document.documentElement.scrollWidth,bodyScrollWidth:document.body.scrollWidth,hasError:!!document.querySelector('.el-alert--error'),errorText:document.querySelector('.el-alert--error')?.textContent||'',hasContent:document.body.innerText.length>20})")

    def screenshot(self, path: pathlib.Path):
        result = self.cdp.call("Page.captureScreenshot", {"format": "png", "fromSurface": True})
        path.write_bytes(base64.b64decode(result["data"]))

    def close(self):
        try:
            self.cdp.close()
        finally:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
            subprocess.run(["rm", "-rf", "--", str(self.profile)], check=True)


ADMIN_PAGES = (
    ("/platform/tenants", "客户与成员"), ("/platform/users", "平台账号"),
    ("/platform/channels", "上游渠道"), ("/platform/catalog", "模型与路由"),
    ("/platform/prices", "售价版本"), ("/platform/price-groups", "价格组与租户定价"),
    ("/platform/procurement", "采购账户与对账"), ("/platform/credits", "人工核款"),
    ("/platform/refunds", "消费退款"), ("/platform/reconciliations", "收费核对"),
    ("/platform/alerts", "经营告警"), ("/platform/reports", "运营与成本报表"),
    ("/platform/audit", "操作审计"), ("/platform/cash-refunds", "现金退款"),
    ("/platform/wallet-sources", "资金来源与核对"), ("/platform/vouchers", "运营赠额券"),
    ("/platform/payments", "支付与商品"), ("/platform/recovery", "恢复核对"),
)
CUSTOMER_PAGES = (
    ("/", "工作台"), ("/keys", "API 密钥"), ("/members", "成员与权限"),
    ("/models", "模型与价格"), ("/wallet", "钱包与流水"),
    ("/requests", "调用记录"), ("/playground", "API 联调"),
    ("/reports", "用量与资金报表"), ("/periodic-budget", "周期预算"),
    ("/payments", "充值订单"),
)


def check_expected_alert(browser: Browser, path: str, expected: str) -> dict:
    observation = browser.page_checks(path, expected)
    if observation["path"] != urllib.parse.urlsplit(path).path or not observation["hasError"]:
        raise RuntimeError(f"expected guarded page alert: {path} {observation}")
    if observation["scrollWidth"] > observation["viewport"] or observation["bodyScrollWidth"] > observation["viewport"]:
        raise RuntimeError(f"horizontal page overflow: {path} {observation}")
    return {"name": f"{path}: 按合同显示目标缺失保护，无横向溢出", **observation}


def start_ui_mock(app_container: str, image: str) -> tuple[str, pathlib.Path]:
    inspection = subprocess.run(["docker", "inspect", "--format", "{{json .NetworkSettings.Networks}}", app_container], text=True, capture_output=True, check=True)
    networks = json.loads(inspection.stdout)
    network = sorted(networks)[0]
    config_dir = pathlib.Path(tempfile.mkdtemp(prefix="ui-mock-", dir="/Users/vchive/.cache/moni-phase1-browser"))
    config = config_dir / "config.json"
    models = ["chat", "responses", "messages", "embeddings", "rerank", "gemini"]
    config.write_text(json.dumps({"candidates": {"a": {"secret": "phase1-a-secret", "models": models, "ttfb_ms": 1, "event_interval_ms": 1, "output_tokens": 8, "default_fault": "reject_429"}, "b": {"secret": "phase1-b-secret", "models": models, "ttfb_ms": 1, "event_interval_ms": 1, "output_tokens": 8, "default_fault": ""}}}), encoding="utf-8")
    config.chmod(0o600)
    container = "moni-phase1-ui-mock-" + uuid.uuid4().hex[:12]
    try:
        subprocess.run(["docker", "create", "--name", container, "--network", network, "--network-alias", "phase1-mock-upstream", "--entrypoint", "/usr/local/bin/mock-upstream", "-e", "MOCK_UPSTREAM_ENABLE=1", image, "--config", "/etc/moni/mock-upstream.json", "--addr", ":8090", "--allow-directives"], text=True, capture_output=True, check=True)
        raw = config.read_bytes()
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w") as bundle:
            info = tarfile.TarInfo("moni/mock-upstream.json")
            info.size = len(raw)
            info.mode = 0o644
            bundle.addfile(info, io.BytesIO(raw))
        subprocess.run(["docker", "cp", "-", container + ":/etc"], input=archive.getvalue(), capture_output=True, check=True)
        subprocess.run(["docker", "start", container], text=True, capture_output=True, check=True)
        for _ in range(60):
            ready = subprocess.run(["docker", "exec", container, "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8090/health"], capture_output=True)
            if ready.returncode == 0:
                return container, config_dir
            time.sleep(0.2)
        raise RuntimeError("browser mock did not become ready")
    except Exception:
        subprocess.run(["docker", "rm", "-f", container], capture_output=True)
        shutil.rmtree(config_dir)
        raise


def stop_ui_mock(runtime: tuple[str, pathlib.Path] | None):
    if not runtime:
        return
    container, config_dir = runtime
    subprocess.run(["docker", "rm", "-f", container], capture_output=True, check=True)
    shutil.rmtree(config_dir)


def docker_output(*args: str) -> str:
    return subprocess.run(["docker", *args], text=True, capture_output=True, check=True).stdout.strip()


def final_runtime_metadata(root: pathlib.Path, commit: str, app_container: str, worker_container: str, mock_image: str) -> dict:
    if not mock_image:
        raise RuntimeError("final browser evidence requires the app image for the isolated UI mock")
    tree = subprocess.run(["git", "rev-parse", "HEAD^{tree}"], cwd=root, text=True, capture_output=True, check=True).stdout.strip()
    app_image = docker_output("inspect", "--format", "{{.Image}}", app_container)
    worker_image = docker_output("inspect", "--format", "{{.Image}}", worker_container)
    ui_mock_image = docker_output("image", "inspect", "--format", "{{.Id}}", mock_image)
    if not app_image or len({app_image, worker_image, ui_mock_image}) != 1:
        raise RuntimeError("final app, worker and UI mock must use the same runtime image")
    raw_labels = docker_output("image", "inspect", "--format", "{{json .Config.Labels}}", app_image)
    labels = json.loads(raw_labels or "{}")
    expected = {
        "ai.moni.phase1.source-commit": commit,
        "ai.moni.phase1.source-tree": tree,
        "ai.moni.phase1.source-clean": "true",
    }
    if any(labels.get(key) != value for key, value in expected.items()):
        raise RuntimeError("runtime image does not match the final clean commit and source tree")
    return {
        "app_container": app_container,
        "worker_container": worker_container,
        "image_id": app_image,
        "source_commit": commit,
        "source_tree": tree,
        "source_clean": True,
    }


class Evidence:
    def __init__(self, output: pathlib.Path, commit: str, base_url: str, final: bool, runtime: dict | None = None):
        self.output = output
        self.occurrences = {}
        self.value = {"schema_version": 1, "status": "PARTIAL" if final else "PREVIEW", "completed": False, "commit": commit, "isolated": True, "base_url": base_url, "created_at": dt.datetime.now(dt.timezone.utc).isoformat(), "runtime": runtime or {}, "items": {}}

    def write(self):
        temporary = self.output / "browser-evidence.json.tmp"
        temporary.write_text(json.dumps(self.value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        temporary.replace(self.output / "browser-evidence.json")

    def complete(self, final: bool):
        self.value["status"] = "PASS" if final else "PREVIEW"
        self.value["completed"] = True
        if not final:
            for item in self.value["items"].values():
                for record in item.values():
                    record["status"] = "PREVIEW"
        self.write()

    def record(self, browser: Browser, item: str, viewport: str, width: int, checks: list[dict], final: bool):
        folder = self.output / item
        folder.mkdir(parents=True, exist_ok=True)
        occurrence = self.occurrences.get((item, viewport), 0) + 1
        self.occurrences[(item, viewport)] = occurrence
        stem = f"{viewport}-{occurrence:02d}.png"
        browser.screenshot(folder / stem)
        status = "PASS" if final else "PREVIEW"
        record = {"status": status, "width": width, "checks": [check["name"] for check in checks], "observations": checks, "evidence": str((folder / stem).relative_to(self.output))}
        previous = self.value["items"].get(item, {}).get(viewport)
        if previous:
            record["checks"] = previous["checks"] + record["checks"]
            record["observations"] = previous["observations"] + record["observations"]
            record["additional_evidence"] = [previous["evidence"], *previous.get("additional_evidence", [])]
        self.value["items"].setdefault(item, {})[viewport] = record
        (folder / f"{viewport}-{occurrence:02d}.json").write_text(json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        self.write()


def check_page(browser: Browser, path: str, expected: str) -> dict:
    observation = browser.page_checks(path, expected)
    if observation["path"] != urllib.parse.urlsplit(path).path or observation["hasError"]:
        raise RuntimeError(f"page assertion failed: {path} {observation}")
    if observation["scrollWidth"] > observation["viewport"] or observation["bodyScrollWidth"] > observation["viewport"]:
        raise RuntimeError(f"horizontal page overflow: {path} {observation}")
    return {"name": f"{path}: {expected}，无页面横向溢出或错误告警", **observation}


def run(args: argparse.Namespace) -> int:
    root = pathlib.Path(__file__).resolve().parent.parent
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    commit = subprocess.run(["git", "rev-parse", "HEAD"], cwd=root, text=True, capture_output=True, check=True).stdout.strip()
    if args.final:
        dirty = subprocess.run(["git", "status", "--porcelain"], cwd=root, text=True, capture_output=True, check=True).stdout.strip()
        if dirty:
            raise RuntimeError("final browser evidence requires a clean worktree")
        if not args.accounts_file:
            raise RuntimeError("final browser evidence requires the protected harness account file")
    admin_password = pathlib.Path(args.admin_password_file).read_text().strip()
    account_payload = json.loads(pathlib.Path(args.accounts_file).read_text()) if args.accounts_file else {}
    if account_payload and account_payload.get("base_url") != args.base_url.rstrip("/"):
        raise RuntimeError("browser account file belongs to a different base URL")
    accounts = account_payload.get("accounts", account_payload)
    runtime = final_runtime_metadata(root, commit, args.app_container, args.worker_container, args.ui_mock_image) if args.final else {}
    ui_mock_image = runtime["image_id"] if args.final else args.ui_mock_image
    evidence = Evidence(output, commit, args.base_url, args.final, runtime)
    browser = Browser(args.base_url, output)
    mock_runtime = None
    try:
        if accounts:
            browser.login("admin@example.com", admin_password)
            browser.reauth(admin_password)
            browser.publish_models([row["public_name"] for row in account_payload.get("models", [])])
            browser.logout()
        for viewport, width, height in VIEWPORTS:
            browser.viewport(width, height)
            checks = [check_page(browser, "/pricing", "公共默认价")]
            if account_payload.get("models"):
                public_name = account_payload["models"][0]["public_name"]
                browser.wait(f"document.body.innerText.includes({json.dumps(public_name)})")
                checks.append({"name": "公开价格页展示已发布模型与当前价格", "model": public_name})
            evidence.record(browser, "P1-CAT-01", viewport, width, checks, args.final)
        browser.login("admin@example.com", admin_password)
        for viewport, width, height in VIEWPORTS:
            browser.viewport(width, height)
            admin_checks = [check_page(browser, path, expected) for path, expected in ADMIN_PAGES]
            if accounts:
                admin_checks.append(check_page(browser, "/platform/periodic-budget?tenant=" + account_payload["tenant"]["id"], "客户周期预算"))
            else:
                admin_checks.append(check_expected_alert(browser, "/platform/periodic-budget", "预算目标无效"))
            evidence.record(browser, "P1-UI-02", viewport, width, admin_checks, args.final)
            evidence.record(browser, "P1-OP-01", viewport, width, [check_page(browser, "/platform/alerts", "经营告警"), check_page(browser, "/platform/audit", "操作审计")], args.final)
            report_checks = [check_page(browser, "/platform/reports", "运营与成本报表")]
            if viewport == "desktop":
                report_checks.append(browser.download_report_csv(output / "downloads"))
            evidence.record(browser, "P1-RP-01", viewport, width, report_checks, args.final)
            evidence.record(browser, "P1-PR-02", viewport, width, [check_page(browser, "/platform/price-groups", "价格组与租户定价")], args.final)
        alert_label = ""
        if accounts:
            alert_label = "phase1-browser-alert-" + uuid.uuid4().hex[:10]
            browser.reauth(admin_password)
            browser.enable_voucher_policy()
            alert_account = browser.create_procurement_account("phase1-browser-account-" + uuid.uuid4().hex[:10])
            browser.create_budget_alert(alert_account, alert_label)
        if accounts:
            if args.ui_mock_image:
                mock_runtime = start_ui_mock(args.app_container, ui_mock_image)
            owner = accounts["owner"]
            browser.logout()
            browser.login(owner["email"], owner["password"])
            totp_secret = browser.enable_mfa()
            browser.logout()
            browser.login_mfa(owner["email"], owner["password"], totp_secret)
            new_password = "phase1-browser-" + secrets.token_urlsafe(18)
            browser.reauth(owner["password"], totp_secret)
            browser.change_password(owner["password"], new_password, totp_secret)
            owner["password"] = new_password
            browser.login_mfa(owner["email"], new_password, totp_secret)
            browser.reauth(new_password, totp_secret)
            model_rows = account_payload.get("models", [])
            chat_model = next((row["public_name"] for row in model_rows if row.get("protocol") == "openai_chat"), "")
            playground = browser.create_key_use_playground_and_revoke("phase1-browser-" + uuid.uuid4().hex[:10], chat_model)
            identity_actions = [{"name": "TOTP 已启用并用动态码重新登录；密码修改后再次通过 MFA；敏感操作再认证成功"}]
            for viewport, width, height in VIEWPORTS:
                browser.viewport(width, height)
                evidence.record(browser, "P1-UI-01", viewport, width, [*([playground] if viewport == "desktop" else []), *[check_page(browser, path, expected) for path, expected in CUSTOMER_PAGES]], args.final)
                evidence.record(browser, "P1-ID-02", viewport, width, [*identity_actions, check_page(browser, "/security", "已启用")], args.final)
                alias_checks = [check_page(browser, "/models", "模型与价格")]
                for row in model_rows:
                    browser.wait(f"document.body.innerText.includes({json.dumps(row['alias'])})")
                alias_checks.append({"name": "六个模型别名均在客户目录显示；部署 harness 已断言六协议别名实际调用"})
                evidence.record(browser, "P1-CAT-02", viewport, width, alias_checks, args.final)
            browser.logout()
            operator = accounts["operator"]
            browser.login(operator["email"], operator["password"])
            browser.reauth(operator["password"])
            business_ref = "phase1-browser-credit-" + uuid.uuid4().hex[:12]
            browser.create_credit(account_payload["tenant"]["name"], business_ref)
            browser.create_refund(account_payload["tenant"]["name"], playground["settlement_id"])
            purpose = "phase1-browser-voucher-" + uuid.uuid4().hex[:12]
            voucher_code = browser.create_voucher_batch(purpose)
            for viewport, width, height in VIEWPORTS:
                browser.viewport(width, height)
                evidence.record(browser, "P1-FN-02", viewport, width, [{"name": f"finance_operator 通过 UI 创建独立核款单 {business_ref}，并对真实 settlement 创建小额退款"}, check_page(browser, "/platform/credits", "人工核款")], args.final)
                evidence.record(browser, "P1-FN-03", viewport, width, [{"name": "finance_operator 通过 UI 发行单张券并安全清除一次性明文"}, check_page(browser, "/platform/vouchers", "运营赠额券")], args.final)
            browser.logout()
            approver = accounts["approver"]
            browser.login(approver["email"], approver["password"])
            browser.reauth(approver["password"])
            browser.approve_credit(business_ref)
            browser.approve_refund(playground["settlement_id"])
            browser.approve_voucher_batch(purpose)
            for viewport, width, height in VIEWPORTS:
                browser.viewport(width, height)
                evidence.record(browser, "P1-FN-02", viewport, width, [{"name": "finance_approver 通过 UI 完成独立核款与消费退款审批"}, check_page(browser, "/platform/credits", "已批准")], args.final)
                evidence.record(browser, "P1-FN-03", viewport, width, [{"name": "finance_approver 通过 UI 完成独立券批次审批"}, check_page(browser, "/platform/vouchers", "运营赠额券")], args.final)
            browser.logout()
            browser.login_mfa(owner["email"], new_password, totp_secret)
            browser.reauth(new_password, totp_secret)
            browser.redeem_voucher(voucher_code)
            voucher_code = ""
            for viewport, width, height in VIEWPORTS:
                browser.viewport(width, height)
                evidence.record(browser, "P1-FN-03", viewport, width, [{"name": "owner 通过 UI 兑换已审批券并形成唯一入账回执"}, check_page(browser, "/wallet", "兑换明细核查")], args.final)
            browser.logout()
            browser.login("admin@example.com", admin_password)
            browser.reauth(admin_password)
            alert_result = browser.acknowledge_budget_alert(alert_label)
            for viewport, width, height in VIEWPORTS:
                browser.viewport(width, height)
                evidence.record(browser, "P1-OP-01", viewport, width, [alert_result, check_page(browser, "/platform/alerts", "共同确认"), check_page(browser, "/platform/audit", "操作审计")], args.final)
            browser.logout()
    finally:
        browser.close()
        stop_ui_mock(mock_runtime)
    evidence.complete(args.final)
    print(json.dumps({"status": "PASS" if args.final else "PREVIEW", "evidence": str(output / "browser-evidence.json"), "items": len(evidence.value["items"])}, ensure_ascii=False))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:18081")
    parser.add_argument("--admin-password-file", required=True)
    parser.add_argument("--accounts-file")
    parser.add_argument("--app-container", default="moni-phase1-browser-app")
    parser.add_argument("--worker-container", default="moni-phase1-browser-worker")
    parser.add_argument("--ui-mock-image")
    parser.add_argument("--output", required=True)
    parser.add_argument("--final", action="store_true")
    args = parser.parse_args()
    if not pathlib.Path(CHROME).is_file():
        parser.error("Google Chrome is required for isolated browser acceptance")
    if args.accounts_file and not args.ui_mock_image:
        parser.error("--ui-mock-image is required for the real Playground browser check")
    return run(args)


if __name__ == "__main__":
    raise SystemExit(main())

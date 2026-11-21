#!/usr/bin/env python3
import importlib.util
import base64
import json
import pathlib
import socket
import tempfile
import unittest
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("phase1_browser_qa.py")
SPEC = importlib.util.spec_from_file_location("phase1_browser_qa", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class BrowserProtocolTest(unittest.TestCase):
    def test_totp_matches_rfc6238_sha1_vector(self):
        with mock.patch.object(MODULE.time, "time", return_value=59):
            secret = base64.b32encode(b"12345678901234567890").decode()
            self.assertEqual("287082", MODULE.totp_code(secret))

    def test_row_action_waits_for_visible_table_rendering(self):
        browser = MODULE.Browser.__new__(MODULE.Browser)
        browser.evaluate = mock.Mock(side_effect=[False, True])
        browser.wait = lambda expression: MODULE.Browser.wait(browser, expression)
        with mock.patch.object(MODULE.time, "sleep"):
            browser.click_row_action("model", "公开设置")
        self.assertEqual(2, browser.evaluate.call_count)

    def test_public_model_is_not_toggled_off_when_already_public(self):
        class FakeBrowser:
            def __init__(self):
                self.expressions = []

            def navigate(self, path, expected):
                pass

            def click_row_action(self, model, action):
                pass

            def wait(self, expression):
                pass

            def click_text(self, text, selector="button"):
                pass

            def evaluate(self, expression):
                self.expressions.append(expression)
                if "aria-checked" in expression:
                    return True
                return None

        browser = FakeBrowser()
        MODULE.Browser.publish_models(browser, ["model"])
        self.assertEqual(1, len(browser.expressions))

    def test_fresh_totp_never_reuses_a_successful_step(self):
        browser = MODULE.Browser.__new__(MODULE.Browser)
        browser.totp_last_step = -1
        secret = base64.b32encode(b"12345678901234567890").decode()
        with mock.patch.object(MODULE.time, "time", return_value=59):
            first = browser.fresh_totp(secret)
            with mock.patch.object(MODULE.time, "sleep") as sleep:
                sleep.side_effect = lambda _: setattr(MODULE.time, "time", mock.Mock(return_value=60))
                second = browser.fresh_totp(secret)
        self.assertNotEqual(first, second)
        self.assertEqual(2, browser.totp_last_step)
        sleep.assert_called_once()

    def test_login_pacing_preserves_server_burst(self):
        browser = MODULE.Browser.__new__(MODULE.Browser)
        browser.last_login = 0.0
        with mock.patch.object(MODULE.time, "monotonic", side_effect=[100.0, 100.0, 100.0, 112.05]), mock.patch.object(MODULE.time, "sleep") as sleep:
            browser.pace_login()
            browser.pace_login()
        sleep.assert_called_once_with(12.05)
        self.assertEqual(112.05, browser.last_login)

    def cdp_pair(self):
        left, right = socket.socketpair()
        cdp = MODULE.CDP.__new__(MODULE.CDP)
        cdp.socket = left
        cdp.next_id = 0
        self.addCleanup(left.close)
        self.addCleanup(right.close)
        return cdp, right

    def test_receives_fragmented_json_and_answers_ping(self):
        cdp, peer = self.cdp_pair()
        peer.sendall(b"\x89\x01x")
        # Split a valid response into two WebSocket frames after a ping.
        first = b'{"id"'
        second = b':1}'
        peer.sendall(b"\x01" + bytes([len(first)]) + first + b"\x80" + bytes([len(second)]) + second)
        opcode, payload = cdp._receive_frame()
        self.assertEqual(1, opcode)
        self.assertEqual({"id": 1}, json.loads(payload))
        pong = peer.recv(2)
        self.assertEqual(10, pong[0] & 0x0F)

    def test_preview_evidence_is_not_pass(self):
        class FakeBrowser:
            def screenshot(self, path):
                path.write_bytes(b"png")

        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            evidence = MODULE.Evidence(output, "a" * 40, "http://127.0.0.1:18081", False)
            evidence.record(FakeBrowser(), "P1-CAT-01", "desktop", 1440, [{"name": "public catalog"}], False)
            record = evidence.value["items"]["P1-CAT-01"]["desktop"]
            self.assertEqual("PREVIEW", record["status"])
            self.assertNotIn("password", json.dumps(evidence.value).lower())
            self.assertTrue((output / "P1-CAT-01" / "desktop-01.png").is_file())

    def test_final_evidence_is_partial_until_the_entire_run_completes(self):
        class FakeBrowser:
            def screenshot(self, path):
                path.write_bytes(b"png")

        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            evidence = MODULE.Evidence(output, "a" * 40, "http://127.0.0.1:18081", True)
            evidence.record(FakeBrowser(), "P1-CAT-01", "desktop", 1440, [{"name": "public catalog"}], True)
            partial = json.loads((output / "browser-evidence.json").read_text(encoding="utf-8"))
            self.assertEqual("PARTIAL", partial["status"])
            self.assertFalse(partial["completed"])
            evidence.complete(True)
            completed = json.loads((output / "browser-evidence.json").read_text(encoding="utf-8"))
            self.assertEqual("PASS", completed["status"])
            self.assertTrue(completed["completed"])

    def test_repeated_records_preserve_distinct_screenshots(self):
        class FakeBrowser:
            def screenshot(self, path):
                path.write_bytes(path.name.encode())

        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            evidence = MODULE.Evidence(output, "a" * 40, "http://127.0.0.1:18081", True)
            evidence.record(FakeBrowser(), "P1-FN-03", "desktop", 1440, [{"name": "issued"}], True)
            evidence.record(FakeBrowser(), "P1-FN-03", "desktop", 1440, [{"name": "approved"}], True)
            first = output / "P1-FN-03" / "desktop-01.png"
            second = output / "P1-FN-03" / "desktop-02.png"
            self.assertEqual(b"desktop-01.png", first.read_bytes())
            self.assertEqual(b"desktop-02.png", second.read_bytes())
            record = evidence.value["items"]["P1-FN-03"]["desktop"]
            self.assertEqual("P1-FN-03/desktop-02.png", record["evidence"])
            self.assertEqual(["P1-FN-03/desktop-01.png"], record["additional_evidence"])

    def test_final_runtime_requires_one_clean_source_image(self):
        commit = "a" * 40
        tree = "b" * 40
        image = "sha256:" + "c" * 64

        def run_with_clean(clean):
            def fake_run(command, **kwargs):
                if command[:2] == ["git", "rev-parse"]:
                    return mock.Mock(stdout=tree + "\n")
                if command[:2] == ["docker", "inspect"]:
                    return mock.Mock(stdout=image + "\n")
                if command[:3] == ["docker", "image", "inspect"] and "{{.Id}}" in command:
                    return mock.Mock(stdout=image + "\n")
                if command[:3] == ["docker", "image", "inspect"]:
                    return mock.Mock(stdout=json.dumps({"ai.moni.phase1.source-commit": commit, "ai.moni.phase1.source-tree": tree, "ai.moni.phase1.source-clean": clean}) + "\n")
                raise AssertionError(command)

            with mock.patch.object(MODULE.subprocess, "run", side_effect=fake_run):
                return MODULE.final_runtime_metadata(pathlib.Path("/repo"), commit, "app", "worker", "image")

        metadata = run_with_clean("true")
        self.assertEqual(image, metadata["image_id"])
        self.assertEqual(tree, metadata["source_tree"])
        with self.assertRaisesRegex(RuntimeError, "clean commit"):
            run_with_clean("false")

    def test_final_mock_uses_immutable_verified_image(self):
        source = SCRIPT.read_text(encoding="utf-8")
        self.assertIn('ui_mock_image = runtime["image_id"] if args.final else args.ui_mock_image', source)
        self.assertIn("start_ui_mock(args.app_container, ui_mock_image)", source)

    def test_expected_guard_alert_is_distinct_from_unexpected_error(self):
        class FakeBrowser:
            def page_checks(self, path, expected):
                return {"path": path, "hasError": True, "scrollWidth": 390, "bodyScrollWidth": 390, "viewport": 390}

        observation = MODULE.check_expected_alert(FakeBrowser(), "/platform/periodic-budget", "预算目标无效")
        self.assertTrue(observation["hasError"])

    def test_horizontal_page_overflow_fails(self):
        class FakeBrowser:
            def page_checks(self, path, expected):
                return {"path": path, "hasError": False, "scrollWidth": 500, "bodyScrollWidth": 500, "viewport": 390}

        with self.assertRaisesRegex(RuntimeError, "horizontal page overflow"):
            MODULE.check_page(FakeBrowser(), "/platform/reports", "运营与成本报表")


if __name__ == "__main__":
    unittest.main()

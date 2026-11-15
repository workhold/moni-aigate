#!/usr/bin/env python3
"""Hermetic self-tests for the phase-1 deployment acceptance harness."""

from __future__ import annotations

import argparse
import importlib.util
import json
import pathlib
import re
import stat
import sys
import tempfile
import types
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "phase1-mock-acceptance.py"
SPEC = importlib.util.spec_from_file_location("phase1_mock_acceptance", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
harness = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = harness
SPEC.loader.exec_module(harness)


class Phase1HarnessTest(unittest.TestCase):
    def test_required_pure_go_bindings_exist(self) -> None:
        self.assertFalse(harness.REQUIRED_PURE_CASES - harness.PURE_GO_TEST_BINDINGS.keys())
        for case, (package, test_name) in harness.PURE_GO_TEST_BINDINGS.items():
            with self.subTest(case=case):
                self.assertNotEqual(package, "./...")
                source_dir = ROOT / package.removeprefix("./")
                sources = list(source_dir.glob("*_test.go"))
                self.assertTrue(sources, f"no Go tests in {source_dir}")
                expression = re.compile(r"^func\s+" + re.escape(test_name) + r"\(t \*testing\.T\)", re.MULTILINE)
                self.assertTrue(any(expression.search(path.read_text(encoding="utf-8")) for path in sources), f"missing {package}/{test_name}")

    def test_postgres_bindings_are_explicit_and_exist(self) -> None:
        for case, (package, test_name) in harness.POSTGRES_GO_TEST_BINDINGS.items():
            with self.subTest(case=case):
                source_dir = ROOT / package.removeprefix("./")
                expression = re.compile(r"^func\s+" + re.escape(test_name) + r"\(t \*testing\.T\)", re.MULTILINE)
                self.assertTrue(any(expression.search(path.read_text(encoding="utf-8")) for path in source_dir.glob("*_test.go")))

    def test_compiled_absolute_price_has_one_combined_ceil(self) -> None:
        self.assertEqual(harness.expected_charge(1, 1, 500_000, 500_000), 1)
        self.assertEqual(harness.expected_charge(3, 2, 500_000, 500_000), 3)
        self.assertEqual(harness.expected_charge(7, 99, 500_000, 0), 4)
        self.assertEqual(harness.expected_charge(0, 0, 500_000, 500_000), 0)
        with self.assertRaises(harness.HarnessFailure):
            harness.expected_charge(-1, 0, 500_000, 500_000)

    def test_receipt_cardinality_and_order(self) -> None:
        receipts = [
            {"nonce": "safe", "candidate": "a"},
            {"nonce": "safe", "candidate": "b"},
            {"nonce": "unknown", "candidate": "a"},
        ]
        self.assertEqual(len(harness.check_receipt_sequence(receipts, "safe", expected=["a", "b"])), 2)
        self.assertEqual(len(harness.check_receipt_sequence(receipts, "unknown", count=1)), 1)
        with self.assertRaises(harness.HarnessFailure):
            harness.check_receipt_sequence(receipts, "safe", count=1)
        with self.assertRaises(harness.HarnessFailure):
            harness.check_receipt_sequence(receipts, "safe", expected=["b", "a"])
        with self.assertRaises(harness.HarnessFailure):
            harness.check_receipt_sequence(receipts, "absent", count=1)

    def test_alias_echo_json_and_native_sse(self) -> None:
        harness.assert_model_echo("chat", b'{"model":"alias"}', "alias", False)
        harness.assert_model_echo("gemini", b'{"modelVersion":"alias"}', "alias", False)
        harness.assert_model_echo("chat", b'data: {"model":"alias","choices":[]}\n\ndata: [DONE]\n\n', "alias", True)
        harness.assert_model_echo("messages", b'event: message_start\ndata: {"message":{"model":"alias"}}\n\n', "alias", True)
        harness.assert_model_echo("gemini", b'data: {"modelVersion":"alias","candidates":[]}\n\n', "alias", True)
        with self.assertRaises(harness.HarnessFailure):
            harness.assert_model_echo("chat", b'{"model":"upstream"}', "alias", False)
        with self.assertRaises(harness.HarnessFailure):
            harness.assert_model_echo("chat", b'data: {"choices":[]}\n\n', "alias", True)
        with self.assertRaises(harness.HarnessFailure):
            harness.assert_model_echo("chat", b'data: {broken}\n\n', "alias", True)

    def test_settlement_assertion_uses_compiled_price_and_final_quantities(self) -> None:
        instance = harness.Harness(argparse.Namespace())
        instance.group_prices = {"logical-model": {"price_version_id": "compiled-price", "input_rate": 500_000, "output_rate": 500_000}}
        responses = iter([
            [["succeeded", "settled", "logical-model", "alias", "completed", "confirmed_final", "2", "1", "2"]],
            [["compiled-price", "2", "2", "2"]],
            [["1", "contract", "t", "rejected_no_consumption"], ["2", "contract", "t", "completed"]],
        ])
        instance.query = types.MethodType(lambda self, sql, *args: next(responses), instance)
        instance.assert_request("request", unknown=False, expected_attempts=2, expected_requested_name="alias", expected_protocol="completed", expected_usage="confirmed_final")
        with self.assertRaises(StopIteration):
            instance.query("unexpected extra query")

    def test_ui_accounts_export_is_opt_in_private_and_secret_free(self) -> None:
        payload = {
            "run_id": "run",
            "base_url": "http://127.0.0.1:18080",
            "tenant": {"id": "tenant", "price_group_id": "group"},
            "accounts": {
                "owner": {"email": "owner@example.invalid", "password": "owner-secret"},
                "operator": {"email": "operator@example.invalid", "password": "operator-secret"},
                "approver": {"email": "approver@example.invalid", "password": "approver-secret"},
            },
            "models": [{"model_id": "model", "public_name": "public", "alias": "alias", "protocol": "openai_chat"}],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "accounts.json"
            harness.write_ui_accounts_file(str(path), payload)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(json.loads(path.read_text(encoding="utf-8")), payload)
            encoded = path.read_bytes()
            self.assertNotIn(b"api_key", encoded)
            self.assertNotIn(b"cookie", encoded)
            with self.assertRaises(harness.HarnessFailure):
                harness.write_ui_accounts_file(str(path), payload)
            forbidden = pathlib.Path(directory) / "forbidden.json"
            with self.assertRaises(harness.HarnessFailure):
                harness.write_ui_accounts_file(str(forbidden), {"models": [{"api_key": "must-not-export"}]})
            self.assertFalse(forbidden.exists())
        with self.assertRaises(harness.HarnessFailure):
            harness.write_ui_accounts_file("relative.json", payload)
        with self.assertRaises(harness.HarnessFailure):
            harness.write_ui_accounts_file(str(ROOT / ".phase1-ui-accounts-must-not-exist"), payload)

    def test_nonce_compatibility_and_case_suffix(self) -> None:
        instance = harness.Harness(argparse.Namespace())
        instance.run_id = "run"
        self.assertEqual(instance.request_nonce("chat", False, True, "", ""), "run-chat-json-unknown")
        self.assertEqual(instance.request_nonce("chat", False, True, "missing_usage", ""), "run-chat-json-unknown-missing-usage")
        self.assertEqual(instance.request_nonce("chat", True, True, "drop_after_events:2", "drop-events"), "run-chat-stream-unknown-drop-events")
        with self.assertRaises(harness.HarnessFailure):
            instance.request_nonce("chat", False, False, "", "bad]]")


if __name__ == "__main__":
    unittest.main()

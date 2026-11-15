#!/usr/bin/env python3
import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("phase1-acceptance.py")
SPEC = importlib.util.spec_from_file_location("phase1_acceptance", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class PhaseOneAcceptanceTest(unittest.TestCase):
    def test_frozen_matrix_has_exactly_41_unique_ids(self):
        items = MODULE.load_matrix()
        self.assertEqual(41, len(items))
        self.assertEqual(MODULE.EXPECTED_IDS, {item["id"] for item in items})

    def test_go_json_requires_pass_and_rejects_skip_or_missing(self):
        items = [
            {"id": "P1-ID-01", "tests": ["internal/identity:TestPass", "internal/identity:TestSkip"]},
            {"id": "P1-ID-02", "tests": ["internal/identity:TestMissing"]},
        ]
        with tempfile.TemporaryDirectory() as directory:
            log = pathlib.Path(directory) / "go.jsonl"
            events = [
                {"Package": "github.com/moni-ai/moni-aigate/internal/identity", "Test": "TestPass", "Action": "pass"},
                {"Package": "github.com/moni-ai/moni-aigate/internal/identity", "Test": "TestSkip", "Action": "skip"},
                {"Package": "github.com/moni-ai/moni-aigate/internal/identity", "Test": "TestPass/subtest", "Action": "pass"},
            ]
            log.write_text("".join(json.dumps(event) + "\n" for event in events), encoding="utf-8")
            results = MODULE.required_go_results(log, items)
        self.assertEqual("PASS", results["P1-ID-01"]["internal/identity:TestPass"])
        self.assertEqual("SKIP", results["P1-ID-01"]["internal/identity:TestSkip"])
        self.assertEqual("MISSING", results["P1-ID-02"]["internal/identity:TestMissing"])

    def test_browser_requires_desktop_and_real_390px(self):
        commit = "a" * 40
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "browser.json"
            record = {"schema_version": 1, "commit": commit, "isolated": True, "items": {"P1-UI-01": {"desktop": {"status": "PASS", "width": 1440, "checks": ["overview"]}}}}
            path.write_text(json.dumps(record), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "mobile390"):
                MODULE.load_browser(path, commit)
            record["items"]["P1-UI-01"]["mobile390"] = {"status": "PASS", "width": 390, "checks": ["no horizontal overflow"]}
            path.write_text(json.dumps(record), encoding="utf-8")
            self.assertEqual(commit, MODULE.load_browser(path, commit)["commit"])
            with self.assertRaisesRegex(ValueError, "final commit"):
                MODULE.load_browser(path, "b" * 40)

    def test_failed_global_suite_never_generates_mvp_pass(self):
        items = MODULE.load_matrix()
        suite_names = ["static-build", "frontend", "go-race", "current-mvp", "mock-deployment", "identity-lifecycle", "mail-lifecycle", "payment-process"]
        summary = {"commit": "a" * 40, "suites": [{"name": name, "status": "FAIL" if name == "current-mvp" else "PASS", "logs": [name + ".log"]} for name in suite_names]}
        results = {item["id"]: {test: "PASS" for test in item["tests"]} for item in items}
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            MODULE.write_evidence(output, copy.deepcopy(items), summary, results, None)
            final = json.loads((output / "summary.json").read_text(encoding="utf-8"))
            self.assertEqual("FAIL", final["overall_status"])
            self.assertEqual(0, final["counts"]["pass"])
            self.assertEqual(41, final["counts"]["fail"])
            self.assertIn("内测", final["mvp_status"])
            self.assertIn("**FAIL**", (output / "P1-DP-01.md").read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()

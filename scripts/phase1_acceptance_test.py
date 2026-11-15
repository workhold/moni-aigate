#!/usr/bin/env python3
import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest import mock

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
            record = {"schema_version": 1, "status": "PASS", "completed": True, "commit": commit, "isolated": True, "runtime": {"source_commit": commit, "source_tree": "d" * 40, "source_clean": True, "image_id": "sha256:" + "e" * 64}, "items": {"P1-UI-01": {"desktop": {"status": "PASS", "width": 1440, "checks": ["overview"]}}}}
            path.write_text(json.dumps(record), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "mobile390"):
                MODULE.load_browser(path, commit)
            record["items"]["P1-UI-01"]["mobile390"] = {"status": "PASS", "width": 390, "checks": ["no horizontal overflow"]}
            path.write_text(json.dumps(record), encoding="utf-8")
            self.assertEqual(commit, MODULE.load_browser(path, commit)["commit"])
            record["status"] = "PARTIAL"
            record["completed"] = False
            path.write_text(json.dumps(record), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "fully completed"):
                MODULE.load_browser(path, commit)
            record["status"] = "PASS"
            record["completed"] = True
            path.write_text(json.dumps(record), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "final commit"):
                MODULE.load_browser(path, "b" * 40)

    def test_acceptance_rechecks_running_browser_image(self):
        commit = "a" * 40
        tree = "d" * 40
        image = "sha256:" + "e" * 64
        browser = {"runtime": {"source_commit": commit, "source_tree": tree, "source_clean": True, "image_id": image}}

        def fake_run(command, **kwargs):
            if command[:2] == ["docker", "inspect"]:
                return mock.Mock(stdout=image + "\n", returncode=0)
            if command[:3] == ["docker", "image", "inspect"]:
                return mock.Mock(stdout=json.dumps({"ai.moni.phase1.source-commit": commit, "ai.moni.phase1.source-tree": tree, "ai.moni.phase1.source-clean": "true"}) + "\n", returncode=0)
            raise AssertionError(command)

        with mock.patch.dict(MODULE.os.environ, {"PHASE1_APP_CONTAINER": "app", "PHASE1_WORKER_CONTAINER": "worker"}), mock.patch.object(MODULE, "git", return_value=tree), mock.patch.object(MODULE.subprocess, "run", side_effect=fake_run):
            MODULE.verify_browser_runtime(browser, commit)
        changed = "sha256:" + "f" * 64

        def changed_worker(command, **kwargs):
            if command[:2] == ["docker", "inspect"]:
                return mock.Mock(stdout=(changed if command[-1] == "worker" else image) + "\n", returncode=0)
            return fake_run(command, **kwargs)

        with mock.patch.dict(MODULE.os.environ, {"PHASE1_APP_CONTAINER": "app", "PHASE1_WORKER_CONTAINER": "worker"}), mock.patch.object(MODULE, "git", return_value=tree), mock.patch.object(MODULE.subprocess, "run", side_effect=changed_worker), self.assertRaisesRegex(ValueError, "differ"):
            MODULE.verify_browser_runtime(browser, commit)

    def test_preview_launcher_uses_archived_source_and_retains_on_docker_errors(self):
        script = SCRIPT.with_name("phase1-browser-preview.sh").read_text(encoding="utf-8")
        self.assertNotIn("docker logs -f", script)
        self.assertIn('git -C "$root" archive --format=tar "$commit"', script)
        self.assertIn('cd "$build_context"', script)
        self.assertIn('docker build --file "$build_context/Dockerfile"', script)
        self.assertIn("PHASE1_BUILD_HTTPS_PROXY", script)
        self.assertIn('*"No such object"*|*"No such container"*) break', script)
        self.assertIn("Docker inspect unavailable; retaining isolated preview resources", script)
        self.assertLess(script.index("status --porcelain"), script.index('"$private/admin-password"'))

    def test_restart_runtime_recheck_failure_is_logged(self):
        with tempfile.TemporaryDirectory() as directory:
            log = pathlib.Path(directory) / "restart.log"
            with mock.patch.object(MODULE, "verify_browser_runtime", side_effect=ValueError("image changed")):
                self.assertFalse(MODULE.recheck_browser_runtime({"runtime": {}}, "a" * 40, log))
            self.assertIn("runtime_recheck: FAIL: image changed", log.read_text(encoding="utf-8"))

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

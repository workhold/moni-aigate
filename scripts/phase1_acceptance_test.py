#!/usr/bin/env python3
import copy
import importlib.util
import json
import pathlib
import re
import tempfile
import unittest
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("phase1-acceptance.py")
SPEC = importlib.util.spec_from_file_location("phase1_acceptance", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
EVIDENCE_SPEC = importlib.util.spec_from_file_location("check_go_test_evidence", SCRIPT.with_name("check-go-test-evidence.py"))
EVIDENCE = importlib.util.module_from_spec(EVIDENCE_SPEC)
EVIDENCE_SPEC.loader.exec_module(EVIDENCE)


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
        self.assertIn("PHASE1_BUILD_NPM_REGISTRY", script)
        self.assertIn("PHASE1_BUILD_ALPINE_REPOSITORY", script)
        self.assertIn('*"No such object"*|*"No such container"*) break', script)
        self.assertIn("Docker inspect unavailable; retaining isolated preview resources", script)
        self.assertLess(script.index("status --porcelain"), script.index('"$private/admin-password"'))

    def test_restart_runtime_recheck_failure_is_logged(self):
        with tempfile.TemporaryDirectory() as directory:
            log = pathlib.Path(directory) / "restart.log"
            with mock.patch.object(MODULE, "verify_browser_runtime", side_effect=ValueError("image changed")):
                self.assertFalse(MODULE.recheck_browser_runtime({"runtime": {}}, "a" * 40, log))
            self.assertIn("runtime_recheck: FAIL: image changed", log.read_text(encoding="utf-8"))

    def test_external_raw_logs_publish_with_final_evidence_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            staging = root / "staging"
            staging.mkdir()
            result = MODULE.run_logged(["true"], staging / "suite.log", MODULE.os.environ.copy())
            self.assertEqual(str(staging / "suite.log"), result["log"])
            suites = [{"commands": [result], "logs": [result["log"]]}]
            final = root / "published"
            nested = staging / "current-mvp" / "group.log"
            nested.parent.mkdir()
            nested.write_text("group failure\n", encoding="utf-8")
            with (staging / "suite.log").open("a", encoding="utf-8") as output:
                output.write(f"logs: {staging}/current-mvp\n")
            MODULE.publish_artifacts(staging, final, suites)
            self.assertEqual(str(final / "suite.log"), result["log"])
            self.assertEqual([str(final / "suite.log")], suites[0]["logs"])
            self.assertIn("exit_status: 0", (final / "suite.log").read_text(encoding="utf-8"))
            self.assertIn(f"logs: {final}/current-mvp", (final / "suite.log").read_text(encoding="utf-8"))
            self.assertNotIn(str(staging), (final / "suite.log").read_text(encoding="utf-8"))
            self.assertTrue((final / "current-mvp" / "group.log").is_file())
            with self.assertRaisesRegex(ValueError, "already exists"):
                MODULE.publish_artifacts(staging, final, suites)

    def test_go_evidence_reuse_is_bound_to_fresh_manifest_and_opt_in(self):
        with tempfile.TemporaryDirectory() as directory:
            artifact = pathlib.Path(directory)
            (artifact / "go-race.jsonl").write_text('{"Action":"pass"}\n', encoding="utf-8")
            manifest = MODULE.go_evidence_manifest(artifact, "a" * 40, "b" * 32)
            value = json.loads(manifest.read_text(encoding="utf-8"))
            self.assertEqual("a" * 40, value["commit"])
            self.assertEqual(MODULE.EXPECTED_DATABASE, value["database"])
            self.assertEqual("go-race.jsonl", value["log"])
            import hashlib
            self.assertEqual(hashlib.sha256((artifact / "go-race.jsonl").read_bytes()).hexdigest(), value["log_sha256"])
        runner = SCRIPT.read_text(encoding="utf-8")
        self.assertLess(runner.index("go_evidence_manifest(artifact, commit, run_id)"), runner.index('add("current-mvp"'))
        shell = SCRIPT.with_name("verify-current-mvp.sh").read_text(encoding="utf-8")
        self.assertIn('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE_V2:-}" ]; then', shell)
        self.assertLess(shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE_V2:-}" ]; then'), shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE:-}" ]; then'))
        self.assertIn('check-acceptance-evidence-v2.py', shell)
        self.assertIn('os.environ.get("PHASE1_ACCEPTANCE_GO_EVIDENCE_V2")', runner)
        self.assertIn('env["VERIFY_CURRENT_MVP_GO_EVIDENCE_V2"]', runner)
        self.assertIn('env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE_V2", None)', runner)
        self.assertIn('if not isinstance(run_id, str):', runner)
        v2_branch = runner[runner.index("    if v2_source:"):runner.index("    else:", runner.index("    if v2_source:"))]
        self.assertLess(v2_branch.index('env["PHASE1_ACCEPTANCE_RUN_ID"] = run_id'), v2_branch.index("merge = run_logged"))
        self.assertIn('env["VERIFY_CURRENT_MVP_GO_EVIDENCE"]', runner)
        self.assertIn('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE:-}" ]; then', shell)
        self.assertLess(shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE:-}" ]; then'), shell.index('inventory=$(go test -run'))
        self.assertIn('inventory=$(go test -run', shell, "standalone mode retains the original inventory and test execution")

    def test_run_logged_output_is_accepted_by_evidence_checker(self):
        with tempfile.TemporaryDirectory() as directory:
            artifact = pathlib.Path(directory)
            event = {"Action": "pass", "Package": "github.com/moni-ai/moni-aigate/internal/server", "Test": "TestOne"}
            import sys
            MODULE.run_logged([sys.executable, "-c", "import json; print(json.dumps(json.loads(" + repr(json.dumps(event)) + ")))"], artifact / "go-race.jsonl", MODULE.os.environ.copy())
            manifest = MODULE.go_evidence_manifest(artifact, MODULE.git("rev-parse", "HEAD"), "c" * 32)
            with mock.patch.dict(EVIDENCE.os.environ, {EVIDENCE.RUN_ID_ENV: "c" * 32}), mock.patch.object(EVIDENCE, "require_clean_source", lambda: None):
                self.assertEqual(1, EVIDENCE.verify(manifest, ["./internal/server:TestOne"]))

    def test_all_current_mvp_named_tests_can_be_proven_from_one_race_log(self):
        shell = SCRIPT.with_name("verify-current-mvp.sh").read_text(encoding="utf-8")
        specs = sorted(set(re.findall(r"\./[A-Za-z0-9_/]+:Test[A-Za-z0-9_]+", shell)))
        self.assertEqual(100, len(specs), "the frozen current-MVP inventory changed; update evidence reuse coverage deliberately")
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            log = root / "go-race.jsonl"
            events = []
            for spec in specs:
                package, test = spec[2:].split(":", 1)
                events.append({"Action": "pass", "Package": "github.com/moni-ai/moni-aigate/" + package, "Test": test})
            log.write_text("".join(json.dumps(event) + "\n" for event in events), encoding="utf-8")
            import hashlib
            manifest = {"schema_version": 1, "suite": "go-race", "run_id": "a" * 32, "created_at": MODULE.now(), "commit": MODULE.git("rev-parse", "HEAD"), "database": MODULE.EXPECTED_DATABASE, "log": "go-race.jsonl", "log_sha256": hashlib.sha256(log.read_bytes()).hexdigest()}
            (root / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
            with mock.patch.dict(EVIDENCE.os.environ, {EVIDENCE.RUN_ID_ENV: "a" * 32}), mock.patch.object(EVIDENCE, "require_clean_source", lambda: None):
                self.assertEqual(100, EVIDENCE.verify(root / "manifest.json", specs))

    def test_v2_matrix_verifies_77_unique_tests_and_expands_82_bindings(self):
        items = MODULE.load_matrix()
        self.assertEqual(77, len(MODULE.matrix_go_specs(items)))
        with mock.patch.object(MODULE.EVIDENCE_V2, "verify", return_value=77) as verify:
            results = MODULE.required_go_results_v2(pathlib.Path("manifest.json"), items)
        self.assertEqual(82, sum(len(bindings) for bindings in results.values()))
        self.assertTrue(all(status == "PASS" for bindings in results.values() for status in bindings.values()))
        specs = verify.call_args.args[1]
        self.assertEqual(MODULE.matrix_go_specs(items), specs)
        command = MODULE.v2_merge_command(pathlib.Path("manifest.json"), items)
        self.assertEqual(77, command.count("--spec"))

    def test_required_procurement_binding_receives_dedicated_database(self):
        source = SCRIPT.read_text(encoding="utf-8")
        self.assertIn('"PROCUREMENT_ATTEMPT_RULES_DATABASE_URL": os.environ["TEST_DATABASE_URL"]', source)

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

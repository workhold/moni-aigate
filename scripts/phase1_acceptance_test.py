#!/usr/bin/env python3
import copy
import hashlib
import importlib.util
import io
import json
import pathlib
import re
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("phase1-acceptance.py")
SPEC = importlib.util.spec_from_file_location("phase1_acceptance", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
EVIDENCE_SPEC = importlib.util.spec_from_file_location("check_go_test_evidence", SCRIPT.with_name("check-go-test-evidence.py"))
EVIDENCE = importlib.util.module_from_spec(EVIDENCE_SPEC)
EVIDENCE_SPEC.loader.exec_module(EVIDENCE)
ATTESTED_SPEC = importlib.util.spec_from_file_location("verify_current_mvp_attested", SCRIPT.with_name("verify_current_mvp_attested.py"))
ATTESTED = importlib.util.module_from_spec(ATTESTED_SPEC)
ATTESTED_SPEC.loader.exec_module(ATTESTED)


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
        self.assertIn('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED:-}" ]; then', shell)
        self.assertLess(shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED:-}" ]; then'), shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE_V2:-}" ]; then'))
        self.assertIn('verify_current_mvp_attested.py', shell)
        self.assertIn('strict v2 batch', shell)
        self.assertIn('attested_validated=1', shell)
        self.assertIn('if ! python3 scripts/verify_current_mvp_attested.py', shell)
        self.assertIn('VERIFY_CURRENT_MVP_ATTESTATION_OUTPUT', shell)
        self.assertLess(shell.index('if ! python3 scripts/verify_current_mvp_attested.py'), shell.index("run_group 'static current architecture checks'"))
        self.assertIn('reused:', shell)
        self.assertLess(shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE_V2:-}" ]; then'), shell.index('if [ -n "${VERIFY_CURRENT_MVP_GO_EVIDENCE:-}" ]; then'))
        self.assertIn('check-acceptance-evidence-v2.py', shell)
        self.assertIn('os.environ.get("PHASE1_ACCEPTANCE_GO_EVIDENCE_V2")', runner)
        self.assertIn('env["VERIFY_CURRENT_MVP_GO_EVIDENCE_V2"]', runner)
        self.assertIn('env["VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED"] = "1"', runner)
        self.assertIn('env["VERIFY_CURRENT_MVP_RUNNER_REPORT"]', runner)
        self.assertIn('env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED", None)', runner)
        self.assertIn('if parallel_details is not None:', runner)
        self.assertIn('VERIFY_CURRENT_MVP_ATTESTATION_OUTPUT', runner)
        self.assertIn('attested_matrix_results(', runner)
        self.assertIn('env.pop("VERIFY_CURRENT_MVP_RUNNER_REPORT", None)', runner)
        self.assertIn('if manifest_value.get("workers") == 2:', runner)
        self.assertIn('validate_parallel_report(parallel_artifact / "runner-report.json"', runner)
        self.assertIn('env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE_V2", None)', runner)
        self.assertIn('if not isinstance(run_id, str) or not re.fullmatch(r"[0-9a-f]{32}", run_id):', runner)
        v2_branch = runner[runner.index("    if parallel_workers is not None or v2_source:"):runner.index("    serial_tail_started", runner.index("    if parallel_workers is not None or v2_source:"))]
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

    def test_parallel_mode_requires_fresh_external_output_directories(self):
        with mock.patch.object(MODULE, "clean_commit", return_value="a" * 40), \
             mock.patch.dict(MODULE.os.environ, {"PHASE1_ACCEPTANCE_PARALLEL_WORKERS": "2"}, clear=False), \
             self.assertRaisesRegex(ValueError, "--artifacts"):
            MODULE.execute([], SimpleNamespace(artifacts=None, output=None))

    def test_v2_staging_copies_regular_files_and_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / "source"
            source.mkdir()
            manifest = source / "manifest.json"
            manifest.write_text("{}\n", encoding="utf-8")
            staged = MODULE.stage_v2_evidence(manifest, root / "staged")
            self.assertEqual("{}\n", staged.read_text(encoding="utf-8"))
            (source / "linked").symlink_to(manifest)
            with self.assertRaisesRegex(ValueError, "symlink"):
                MODULE.stage_v2_evidence(manifest, root / "staged-2")

    def parallel_report_fixture(self, root, run_id, commit):
        manifest = root / "go-race-evidence-v2.json"
        manifest.write_text(json.dumps({"schema_version": 2, "suite": "go-race-parallel", "run_id": run_id, "workers": 2, "commit": commit, "tree": "c" * 40, "source_clean": True, "shards": [{} for _ in range(17)]}), encoding="utf-8")

        def sample(shard, part):
            records = []
            for name in (f"moni-040-{run_id}-{shard}-pg", f"moni-040-{run_id}-{part}"):
                records.append({"Name": name, "CPUPerc": "1.00%", "MemUsage": "1MiB / 8GiB", "MemPerc": "0.01%", "NetIO": "0B / 0B", "BlockIO": "0B / 0B", "PIDs": "2"})
            return {"at": "2026-01-01T00:00:01+00:00", "containers": records}

        shards = []
        for index in range(14):
            shard_name = f"shard-{index}"
            parts = []
            for part_index in range(4 if index == 0 else 1):
                part_name = f"part-{index}-{part_index}"
                parts.append({"name": part_name, "errors": [], "runner_removed": True, "oom_killed": False, "resource_samples": [sample(shard_name, part_name)]})
            shards.append({
                "name": shard_name, "errors": [], "wall_seconds": 1.0,
                "cleanup": {"containers_removed": True, "volume_removed": True, "netns_removed": True},
                "postgres_terminal": {"running_before_cleanup": True, "oom_killed": False}, "parts": parts,
            })
        proof = {"status": "PASS", "simultaneous_running": True, "default_acl_independent": True, "default_acl_restored": True, "advisory_lock_independent": True, "probe_objects_removed": True, "advisory_locks_remaining": 0}
        report = {
            "schema_version": 1, "suite": "040-pr-d-parallel-runner", "run_id": run_id, "commit": commit, "tree": "c" * 40,
            "workers": 2, "status": "PASS", "reusable": True, "manifest": manifest.name,
            "manifest_sha256": hashlib.sha256(manifest.read_bytes()).hexdigest(),
            "complete_029": False, "workers_2_proven": True, "fail_fast": False,
            "dual_cluster_proof": proof, "host_listeners": {"zero_new": True}, "shards": shards, "errors": [],
            "started_at": "2026-01-01T00:00:00+00:00", "completed_at": "2026-01-01T00:00:02+00:00",
            "timings": {"wall_seconds": 10.0, "parallel_shards_seconds": 8.0, "merge_seconds": 2.0},
        }
        path = root / "runner-report.json"
        path.write_text(json.dumps(report), encoding="utf-8")
        (root / "dual-cluster-proof.json").write_text(json.dumps(proof), encoding="utf-8")
        return manifest, path, report

    def test_parallel_report_binds_manifest_resources_and_dual_cluster_proof(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            manifest, path, report = self.parallel_report_fixture(root, "a" * 32, "b" * 40)
            details = MODULE.validate_parallel_report(path, manifest, "a" * 32, "b" * 40)
            self.assertEqual(17, details["resource_samples"])
            self.assertEqual(8.0, details["parallel_shards_seconds"])
            report["shards"][0]["postgres_terminal"]["oom_killed"] = True
            path.write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "PostgreSQL"):
                MODULE.validate_parallel_report(path, manifest, "a" * 32, "b" * 40)
            report["shards"][0]["postgres_terminal"]["oom_killed"] = False
            report["shards"][0]["parts"][0]["resource_samples"][0]["containers"] = []
            path.write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "exactly two containers"):
                MODULE.validate_parallel_report(path, manifest, "a" * 32, "b" * 40)
            path.unlink()
            with self.assertRaises(FileNotFoundError):
                MODULE.validate_parallel_report(path, manifest, "a" * 32, "b" * 40)

    def test_runner_attested_manifest_binds_report_and_requires_strict_v2(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            manifest, path, _ = self.parallel_report_fixture(root, "a" * 32, "b" * 40)
            with mock.patch.object(ATTESTED, "_git", side_effect=lambda *args: "b" * 40 if args[-1] == "HEAD" else "c" * 40), \
                 mock.patch.object(ATTESTED.v2, "required_specs", return_value=["./internal/x:TestOne", "./internal/x:TestTwo"]), \
                 mock.patch.object(ATTESTED.v2, "verify", return_value=2) as strict_verify, \
                 mock.patch.dict(ATTESTED.os.environ, {"PHASE1_ACCEPTANCE_RUN_ID": "a" * 32}):
                result = ATTESTED.validate(path, manifest)
                self.assertEqual("PASS", result["status"])
                self.assertEqual(100, result["current_mvp_reused"])
                self.assertEqual(77, result["matrix_reused"])
                self.assertEqual(["./internal/x:TestOne", "./internal/x:TestTwo"], strict_verify.call_args.args[1])
                with mock.patch.object(ATTESTED.v2, "verify", side_effect=ATTESTED.v2.EvidenceError("empty shard evidence")):
                    with self.assertRaisesRegex(ATTESTED.v2.EvidenceError, "empty shard"):
                        ATTESTED.validate(path, manifest)
                manifest.write_text(manifest.read_text(encoding="utf-8") + "\n", encoding="utf-8")
                with self.assertRaises(ATTESTED.AttestationError):
                    ATTESTED.validate(path, manifest)
            with mock.patch.dict(ATTESTED.os.environ, {"PHASE1_ACCEPTANCE_RUN_ID": "d" * 32}):
                with self.assertRaises(ATTESTED.AttestationError):
                    ATTESTED.validate(path, manifest)

    def test_phase1_requires_complete_strict_attestation_output(self):
        items = MODULE.load_matrix()
        expected = len(MODULE.EVIDENCE_V2.required_specs(MODULE.ROOT))
        value = {"status": "PASS", "reused": "strict-v2-batch", "run_id": "a" * 32, "commit": "b" * 40, "verified_specs": expected, "current_mvp_reused": 100, "matrix_reused": 77}
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "attestation.json"
            path.write_text(json.dumps(value), encoding="utf-8")
            results, valid = MODULE.attested_matrix_results(path, items, "a" * 32, "b" * 40)
            self.assertTrue(valid)
            self.assertEqual(82, sum(status == "PASS" for bindings in results.values() for status in bindings.values()))
            value["verified_specs"] = 0
            path.write_text(json.dumps(value), encoding="utf-8")
            results, valid = MODULE.attested_matrix_results(path, items, "a" * 32, "b" * 40)
            self.assertFalse(valid)
            self.assertTrue(all(status == "MISSING" for bindings in results.values() for status in bindings.values()))

    def test_attestation_cli_publishes_phase1_compatible_output(self):
        items = MODULE.load_matrix()
        value = {"status": "PASS", "reused": "strict-v2-batch", "run_id": "a" * 32, "commit": "b" * 40, "verified_specs": len(MODULE.EVIDENCE_V2.required_specs(MODULE.ROOT)), "current_mvp_reused": 100, "matrix_reused": 77}
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "attestation.json"
            argv = ["verify_current_mvp_attested.py", "--report", "report.json", "--manifest", "manifest.json", "--output", str(output)]
            with mock.patch.object(ATTESTED, "validate", return_value=value), \
                 mock.patch.object(ATTESTED.sys, "argv", argv), \
                 mock.patch.object(ATTESTED.sys, "stdout", io.StringIO()):
                self.assertEqual(0, ATTESTED.main())
            _, valid = MODULE.attested_matrix_results(output, items, "a" * 32, "b" * 40)
            self.assertTrue(valid)
            with mock.patch.object(ATTESTED, "validate", return_value=value), \
                 mock.patch.object(ATTESTED.sys, "argv", argv), \
                 mock.patch.object(ATTESTED.sys, "stdout", io.StringIO()), \
                 mock.patch.object(ATTESTED.sys, "stderr", io.StringIO()):
                self.assertEqual(1, ATTESTED.main())

    def test_attested_go_suite_is_never_vacuously_pass(self):
        current = {"log": "current-mvp.log", "seconds": 1.25}
        for valid, expected in ((False, "FAIL"), (True, "PASS")):
            commands = []
            suites = [{"name": "go-race", "status": "PASS", "commands": commands, "logs": []}]
            MODULE.update_attested_go_suite(suites, 0, commands, current, valid)
            self.assertEqual(expected, suites[0]["status"])
            self.assertEqual(1, len(commands))
            self.assertEqual(["current-mvp.log"], suites[0]["logs"])
            self.assertIn("strict batched v2", commands[0]["command"])

    def test_attestation_cli_rejects_symlink_inputs_and_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            manifest, report, _ = self.parallel_report_fixture(root, "a" * 32, "b" * 40)
            links = root / "links"
            links.mkdir()
            linked_report = links / report.name
            linked_manifest = links / manifest.name
            linked_report.symlink_to(report)
            linked_manifest.symlink_to(manifest)
            argv = ["verify_current_mvp_attested.py", "--report", str(linked_report), "--manifest", str(linked_manifest)]
            with mock.patch.object(ATTESTED.v2, "verify", return_value=2) as strict_verify, \
                 mock.patch.object(ATTESTED.sys, "argv", argv), \
                 mock.patch.object(ATTESTED.sys, "stdout", io.StringIO()), \
                 mock.patch.object(ATTESTED.sys, "stderr", io.StringIO()):
                self.assertEqual(1, ATTESTED.main())
            strict_verify.assert_not_called()
            output = root / "attestation.json"
            output.symlink_to(root / "target.json")
            argv = ["verify_current_mvp_attested.py", "--report", str(report), "--manifest", str(manifest), "--output", str(output)]
            with mock.patch.object(ATTESTED, "_git", side_effect=lambda *args: "b" * 40 if args[-1] == "HEAD" else "c" * 40), \
                 mock.patch.object(ATTESTED.v2, "required_specs", return_value=["./internal/x:TestOne", "./internal/x:TestTwo"]), \
                 mock.patch.object(ATTESTED.v2, "verify", return_value=2), \
                 mock.patch.dict(ATTESTED.os.environ, {"PHASE1_ACCEPTANCE_RUN_ID": "a" * 32}), \
                 mock.patch.object(ATTESTED.sys, "argv", argv), \
                 mock.patch.object(ATTESTED.sys, "stdout", io.StringIO()), \
                 mock.patch.object(ATTESTED.sys, "stderr", io.StringIO()):
                self.assertEqual(1, ATTESTED.main())

    def test_attested_shell_stops_before_reuse_when_strict_validation_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            env = MODULE.os.environ.copy()
            env.update({
                "VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED": "1",
                "VERIFY_CURRENT_MVP_RUNNER_REPORT": str(root / "missing-report.json"),
                "VERIFY_CURRENT_MVP_GO_EVIDENCE_V2": str(root / "missing-manifest.json"),
                "VERIFY_CURRENT_MVP_LOG_DIR": str(root),
                "GOCACHE": str(root / "gocache"),
            })
            result = subprocess.run(["bash", str(SCRIPT.with_name("verify-current-mvp.sh"))], cwd=MODULE.ROOT, env=env, text=True, capture_output=True, timeout=30)
            self.assertNotEqual(0, result.returncode)
            self.assertNotIn("reused:", result.stdout)
            self.assertNotIn("current MVP verification summary", result.stdout)
            self.assertIn("strict batched v2 attestation failed", result.stderr)

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

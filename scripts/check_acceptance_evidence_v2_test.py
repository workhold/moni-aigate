import contextlib
import copy
import datetime as dt
import importlib.util
import io
import json
import pathlib
import re
import subprocess
import tempfile
import unittest
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("check-acceptance-evidence-v2.py")
SPEC = importlib.util.spec_from_file_location("evidence_v2", SCRIPT)
v2 = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v2)
common = v2.common
REAL_FRESH_SOURCE = v2.fresh_source
REAL_REVIEWED_BASELINE = v2.reviewed_baseline


def make_part(name, packages, tests):
    if tests is not None:
        tests = list(tests)
    run_filter = "^(" + "|".join(tests or []) + ")$" if tests else ""
    args = ["go", "test", "-json", "-race", "-p", "1", "-count=1", "-timeout=10m"]
    if run_filter:
        args += ["-run", run_filter]
    args += packages
    return {"name": name, "packages": packages, "tests": tests, "run_filter": run_filter, "args": args}


def make_plan():
    shards = []
    for class_name in ("billing", "server", "attempt"):
        package = f"example.com/mod/internal/{class_name}"
        discovery = [f"Test{class_name.capitalize()}{index}{suffix}" for index in range(4) for suffix in ("", "Extra")]
        for index in range(4):
            tests = [f"Test{class_name.capitalize()}{index}", f"Test{class_name.capitalize()}{index}Extra"]
            part = make_part(f"{class_name}-{index}", [package], tests)
            shards.append({"name": part["name"], "class": class_name, "packages": [package], "discovery": discovery.copy(), "parts": [part]})
    identity_package = "example.com/mod/internal/identity"
    identity_tests = [f"TestIdentity{index}" for index in range(4)]
    parts = [make_part(f"identity-{index}", [identity_package], [test]) for index, test in enumerate(identity_tests)]
    shards.append({"name": "identity", "class": "identity", "packages": [identity_package], "discovery": identity_tests, "parts": parts})
    other_packages = ["example.com/mod/internal/config", "example.com/mod/internal/migrations", "example.com/mod/internal/other"]
    shards.append({"name": "other", "class": "other", "packages": other_packages, "parts": [make_part("other", other_packages, None)]})
    plan = {
        "schema_version": 1,
        "commit": common.current_commit(),
        "tree": subprocess.run(["git", "rev-parse", "HEAD^{tree}"], cwd=common.ROOT, text=True, capture_output=True, check=True).stdout.strip(),
        "module": "example.com/mod",
        "shards": shards,
    }
    plan["digest"] = v2.canonical_plan_digest(plan)
    return plan


class EvidenceV2Test(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.run_id = "a" * 32
        self.environment = mock.patch.dict(common.os.environ, {common.RUN_ID_ENV: self.run_id})
        self.environment.start()
        self.clean = mock.patch.object(common, "require_clean_source", lambda: None)
        self.clean.start()
        self.manifest_path = self.root / "manifest.json"
        self.plan = make_plan()
        self.source_plan = copy.deepcopy(self.plan)
        self.source_other = {
            "example.com/mod/internal/config": [],
            "example.com/mod/internal/migrations": ["TestRequiredMigrations"],
            "example.com/mod/internal/other": ["TestOther"],
        }
        self.approved_skips = []
        self.baseline = mock.patch.object(v2, "reviewed_baseline", side_effect=lambda: copy.deepcopy(self.approved_skips))
        self.baseline.start()
        self.fresh = mock.patch.object(v2, "fresh_source", side_effect=lambda: (copy.deepcopy(self.source_plan), copy.deepcopy(self.source_other)))
        self.fresh.start()
        self.write_plan()
        self.manifest = self.make_manifest()

    def tearDown(self):
        if self.fresh is not None:
            self.fresh.stop()
        self.baseline.stop()
        self.clean.stop()
        self.environment.stop()
        self.temp.cleanup()

    def write_plan(self):
        (self.root / "shard-plan.json").write_text(json.dumps(self.plan), encoding="utf-8")

    def write_manifest(self):
        self.manifest["plan_sha256"] = v2.digest_file(self.root / "shard-plan.json")
        self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")

    def make_manifest(self):
        created = dt.datetime.now(dt.timezone.utc)
        started = created - dt.timedelta(seconds=2)
        completed = created - dt.timedelta(seconds=1)
        shards = []
        allocation = v2.allocation_from_plan(self.plan, self.source_other)
        baseline_tests = {(entry["package"], entry["test"]) for entry in self.approved_skips}
        for shard in self.plan["shards"]:
            for part in shard["parts"]:
                events = []
                for package in part["packages"]:
                    package_tests = [{"Action": "skip" if (package, test) in baseline_tests else "pass", "Package": package, "Test": test} for pkg, test in sorted(allocation[part["name"]]) if pkg == package]
                    events.extend(package_tests)
                    events.append({"Action": "pass" if package_tests else "skip", "Package": package})
                log = self.root / f"go-tests-{part['name']}.jsonl"
                framing = [
                    "run_id: " + self.run_id,
                    "command: " + " ".join(part["args"]),
                    "started_at: " + started.isoformat(),
                    "",
                ]
                framing.extend(json.dumps(event) for event in events)
                framing.extend(["", "completed_at: " + completed.isoformat(), "exit_status: 0", ""])
                log.write_text("\n".join(framing), encoding="utf-8")
                shards.append({
                    "name": part["name"],
                    "args": part["args"],
                    "log": log.name,
                    "log_sha256": v2.digest_file(log),
                    "exit_status": 0,
                    "database": {"url": common.EXPECTED_DATABASE, "system_identifier": "identity-db" if shard["class"] == "identity" else "db-" + part["name"]},
                    "cleanup": {"containers_removed": True, "volume_removed": True, "netns_removed": True},
                })
        self.manifest = {
            "schema_version": 2,
            "suite": "go-race-parallel",
            "run_id": self.run_id,
            "created_at": created.isoformat(),
            "commit": self.plan["commit"],
            "tree": self.plan["tree"],
            "source_clean": True,
            "scheduler_version": 1,
            "workers": 2,
            "images": {"go_runner": "sha256:" + "1" * 64, "postgres": "sha256:" + "2" * 64},
            "plan": "shard-plan.json",
            "plan_sha256": "",
            "skip_baseline": copy.deepcopy(self.approved_skips),
            "shards": shards,
        }
        self.write_manifest()
        return self.manifest

    def specs(self):
        return ["./internal/billing:TestBilling0", "./internal/migrations:TestRequiredMigrations"]

    def entry(self, name):
        return next(entry for entry in self.manifest["shards"] if entry["name"] == name)

    def refresh_digest(self, name):
        self.entry(name)["log_sha256"] = v2.digest_file(self.root / f"go-tests-{name}.jsonl")
        self.write_manifest()

    def insert_event(self, name, event):
        path = self.root / f"go-tests-{name}.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        position = next(index for index, line in enumerate(lines) if line.startswith("completed_at: "))
        lines.insert(position, json.dumps(event))
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.refresh_digest(name)

    def remove_event(self, name, package, test):
        path = self.root / f"go-tests-{name}.jsonl"
        kept = []
        for line in path.read_text(encoding="utf-8").splitlines():
            if line.startswith("{"):
                event = json.loads(line)
                if event.get("Package") == package and event.get("Test") == test:
                    continue
            kept.append(line)
        path.write_text("\n".join(kept) + "\n", encoding="utf-8")
        self.refresh_digest(name)

    def assert_rejected(self, specs=None):
        with self.assertRaises((v2.EvidenceError, KeyError, TypeError, ValueError)):
            v2.verify(self.manifest_path, specs or self.specs())

    def test_valid_parallel_evidence_identity_sharing_and_unfiltered_other(self):
        self.assertEqual(v2.verify(self.manifest_path, self.specs()), 2)

    def test_cross_package_same_name_is_allowed(self):
        for index, class_name, previous in ((0, "billing", "TestBilling0"), (4, "server", "TestServer0")):
            part = self.plan["shards"][index]["parts"][0]
            part["tests"][0] = "TestShared"
            part["tests"].sort()
            part["run_filter"] = "^(" + "|".join(part["tests"]) + ")$"
            part["args"] = v2.expected_args(part)
            for shard in self.plan["shards"]:
                if shard["class"] == class_name:
                    shard["discovery"] = sorted("TestShared" if test == previous else test for test in shard["discovery"])
        self.plan["digest"] = v2.canonical_plan_digest(self.plan)
        self.source_plan = copy.deepcopy(self.plan)
        self.write_plan()
        self.make_manifest()
        self.assertEqual(v2.verify(self.manifest_path, ["./internal/billing:TestShared", "./internal/server:TestShared"]), 2)

    def test_real_source_supports_100_current_mvp_and_77_to_82_matrix(self):
        real_plan, real_other = REAL_FRESH_SOURCE()
        self.plan = real_plan
        self.source_plan = copy.deepcopy(real_plan)
        self.source_other = real_other
        self.approved_skips = REAL_REVIEWED_BASELINE()
        self.assertEqual(211, len(self.approved_skips))
        self.write_plan()
        self.make_manifest()
        shell = (common.ROOT / "scripts" / "verify-current-mvp.sh").read_text(encoding="utf-8")
        specs = sorted(set(re.findall(r"\./[A-Za-z0-9_/]+:Test[A-Za-z0-9_]+", shell)))
        self.assertEqual(100, len(specs))
        self.assertEqual(100, v2.verify(self.manifest_path, specs))
        skip_events = 0
        for log in self.root.glob("go-tests-*.jsonl"):
            for line in log.read_text(encoding="utf-8").splitlines():
                if line.startswith("{"):
                    event = json.loads(line)
                    skip_events += event.get("Action") == "skip" and bool(event.get("Test"))
        self.assertEqual(211, skip_events)

        phase_script = common.ROOT / "scripts" / "phase1-acceptance.py"
        phase_spec = importlib.util.spec_from_file_location("phase1_acceptance_v2_integration", phase_script)
        phase = importlib.util.module_from_spec(phase_spec)
        phase_spec.loader.exec_module(phase)
        phase.EVIDENCE_V2 = v2
        matrix = phase.load_matrix()
        self.assertEqual(77, len(phase.matrix_go_specs(matrix)))
        results = phase.required_go_results_v2(self.manifest_path, matrix)
        self.assertEqual(82, sum(len(bindings) for bindings in results.values()))
        self.assertTrue(all(status == "PASS" for bindings in results.values() for status in bindings.values()))

    def test_manifest_and_run_identity_failures(self):
        for key, value in [("schema_version", True), ("run_id", "b" * 32), ("created_at", "2000-01-01T00:00:00+00:00"), ("source_clean", False), ("workers", 3), ("images", {}), ("commit", "0" * 40), ("tree", "0" * 40), ("plan_sha256", "0" * 64)]:
            with self.subTest(key=key):
                original = self.manifest[key]
                self.manifest[key] = value
                self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")
                self.assert_rejected()
                self.manifest[key] = original
                self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")

    def test_plan_must_match_fresh_source_and_exact_argv(self):
        shard = self.plan["shards"][0]
        part = shard["parts"][0]
        part["tests"].remove("TestBilling0Extra")
        for billing_shard in self.plan["shards"]:
            if billing_shard["class"] == "billing":
                billing_shard["discovery"].remove("TestBilling0Extra")
        part["run_filter"] = "^(TestBilling0)$"
        part["args"] = v2.expected_args(part)
        self.plan["digest"] = v2.canonical_plan_digest(self.plan)
        self.write_plan()
        self.write_manifest()
        self.assert_rejected()

        self.plan = copy.deepcopy(self.source_plan)
        self.plan["shards"][0]["parts"][0]["args"].remove("-race")
        self.plan["digest"] = v2.canonical_plan_digest(self.plan)
        self.source_plan = copy.deepcopy(self.plan)
        self.write_plan()
        self.write_manifest()
        self.assert_rejected()

    def test_plan_schema_and_internal_digest_fail_closed(self):
        for change in ("schema", "digest"):
            with self.subTest(change=change):
                plan = copy.deepcopy(self.plan)
                if change == "schema":
                    plan["schema_version"] = True
                    plan["digest"] = v2.canonical_plan_digest(plan)
                else:
                    plan["digest"] = "0" * 64
                self.plan = plan
                self.write_plan()
                self.write_manifest()
                self.assert_rejected()
                self.plan = copy.deepcopy(self.source_plan)
                self.write_plan()
                self.write_manifest()

    def test_duplicate_allocation_and_database_collisions_fail_in_any_order(self):
        self.plan["shards"][1]["parts"][0]["tests"][1] = "TestBilling0Extra"
        self.plan["digest"] = v2.canonical_plan_digest(self.plan)
        self.source_plan = copy.deepcopy(self.plan)
        self.write_plan()
        self.write_manifest()
        self.assert_rejected()
        self.plan = make_plan()
        self.source_plan = copy.deepcopy(self.plan)
        self.write_plan()
        self.make_manifest()

        for first, second in [("billing-0", "billing-1"), ("billing-0", "identity-0"), ("identity-0", "billing-0")]:
            with self.subTest(first=first, second=second):
                manifest = copy.deepcopy(self.manifest)
                entries = {entry["name"]: entry for entry in manifest["shards"]}
                entries[second]["database"]["system_identifier"] = entries[first]["database"]["system_identifier"]
                if first == "identity-0":
                    manifest["shards"].sort(key=lambda entry: 0 if entry["name"].startswith("identity-") else 1)
                self.manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
                self.assert_rejected()
                self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")

    def test_missing_duplicate_unexpected_and_failed_events_fail(self):
        self.remove_event("billing-0", "example.com/mod/internal/billing", "TestBilling0Extra")
        self.assert_rejected()
        self.make_manifest()
        self.insert_event("billing-0", {"Action": "pass", "Package": "example.com/mod/internal/billing", "Test": "TestBilling0Extra"})
        self.assert_rejected()
        self.make_manifest()
        self.insert_event("billing-1", {"Action": "pass", "Package": "example.com/mod/internal/billing", "Test": "TestBilling0Extra"})
        self.assert_rejected()
        self.make_manifest()
        self.insert_event("billing-0", {"Action": "fail", "Package": "example.com/mod/internal/billing", "Test": "TestBilling0Extra"})
        self.assert_rejected()

    def test_package_compile_terminal_is_required_exactly_once(self):
        self.remove_event("other", "example.com/mod/internal/config", None)
        self.assert_rejected()
        self.make_manifest()
        self.insert_event("other", {"Action": "pass", "Package": "example.com/mod/internal/config"})
        self.assert_rejected()
        self.make_manifest()
        self.remove_event("billing-0", "example.com/mod/internal/billing", None)
        self.insert_event("billing-0", {"Action": "skip", "Package": "example.com/mod/internal/billing"})
        self.assert_rejected()
        self.make_manifest()
        self.insert_event("billing-0", {"Action": "skip", "Package": "example.com/mod/internal/config"})
        self.assert_rejected()

    def test_log_framing_rejects_truncation_post_exit_and_wrong_command(self):
        path = self.root / "go-tests-billing-0.jsonl"
        original = path.read_text(encoding="utf-8")
        path.write_text(original.split("completed_at: ", 1)[0], encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()
        self.make_manifest()
        path.write_text(path.read_text(encoding="utf-8") + json.dumps({"Action": "pass", "Package": "example.com/mod/internal/billing", "Test": "TestBilling0"}) + "\n", encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()
        self.make_manifest()
        path.write_text(path.read_text(encoding="utf-8").replace("command: go test", "command: go test -count=0", 1), encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()
        self.make_manifest()
        path.write_text(path.read_text(encoding="utf-8").replace("exit_status: 0", "exit_status: 1", 1), encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()

    def test_raw_log_run_id_and_timestamps_bind_the_attempt(self):
        path = self.root / "go-tests-billing-0.jsonl"
        path.write_text(path.read_text(encoding="utf-8").replace("run_id: " + self.run_id, "run_id: " + "b" * 32, 1), encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()
        self.make_manifest()
        stale = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=2)).isoformat()
        path.write_text(re.sub(r"started_at: .*", "started_at: " + stale, path.read_text(encoding="utf-8")), encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()

    def test_cleanup_digest_and_shard_set_fail_closed(self):
        for change in ("args", "exit_status", "cleanup", "log_sha256", "missing", "duplicate"):
            with self.subTest(change=change):
                manifest = copy.deepcopy(self.manifest)
                if change == "args":
                    manifest["shards"][0]["args"].remove("-race")
                elif change == "exit_status":
                    manifest["shards"][0]["exit_status"] = 1
                elif change == "cleanup":
                    manifest["shards"][0]["cleanup"]["volume_removed"] = False
                elif change == "log_sha256":
                    manifest["shards"][0]["log_sha256"] = "0" * 64
                elif change == "missing":
                    manifest["shards"].pop()
                else:
                    manifest["shards"].append(copy.deepcopy(manifest["shards"][0]))
                self.manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
                self.assert_rejected()
                self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")

    def test_duplicate_json_keys_fail_closed(self):
        path = self.root / "go-tests-billing-0.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        position = next(index for index, line in enumerate(lines) if line.startswith("completed_at: "))
        lines.insert(position, '{"Action":"fail","Action":"pass","Package":"example.com/mod/internal/billing"}')
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.refresh_digest("billing-0")
        self.assert_rejected()

    def test_skip_requires_external_baseline_and_required_skip_never_passes(self):
        package = "example.com/mod/internal/other"
        self.source_other[package].append("TestEnvironment")
        self.make_manifest()
        self.remove_event("other", package, "TestEnvironment")
        self.insert_event("other", {"Action": "skip", "Package": package, "Test": "TestEnvironment"})
        entry = {"package": package, "test": "TestEnvironment", "reason": ""}
        self.manifest["skip_baseline"] = [entry]
        self.write_manifest()
        self.assert_rejected()
        with mock.patch.object(v2, "reviewed_baseline", return_value=[entry]):
            self.assertEqual(2, v2.verify(self.manifest_path, self.specs()))

        self.source_other[package].remove("TestEnvironment")
        self.make_manifest()
        required = "example.com/mod/internal/migrations"
        self.remove_event("other", required, "TestRequiredMigrations")
        self.insert_event("other", {"Action": "skip", "Package": required, "Test": "TestRequiredMigrations"})
        required_entry = {"package": required, "test": "TestRequiredMigrations", "reason": ""}
        self.manifest["skip_baseline"] = [required_entry]
        self.write_manifest()
        with mock.patch.object(v2, "reviewed_baseline", return_value=[required_entry]):
            self.assert_rejected()

    def test_malformed_inputs_return_controlled_cli_failures(self):
        for payload in (b"\xff", json.dumps({**self.manifest, "shards": [None]}).encode()):
            with self.subTest(payload=payload[:20]):
                self.manifest_path.write_bytes(payload)
                stderr = io.StringIO()
                stdout = io.StringIO()
                with mock.patch.object(v2.sys, "argv", ["checker", "--manifest", str(self.manifest_path), "--spec", self.specs()[0]]), contextlib.redirect_stderr(stderr), contextlib.redirect_stdout(stdout):
                    self.assertEqual(1, v2.main())
                self.assertIn("FAIL:", stderr.getvalue())
                self.assertNotIn("Traceback", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()

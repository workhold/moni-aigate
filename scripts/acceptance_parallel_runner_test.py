import copy
import datetime as dt
import importlib.util
import json
import os
import pathlib
import socket
import stat
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("acceptance-parallel-runner.py")
SPEC = importlib.util.spec_from_file_location("acceptance_parallel_runner", SCRIPT)
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


def make_part(name):
    return {
        "name": name,
        "packages": ["example.com/mod/internal/" + name],
        "tests": ["TestOne"],
        "run_filter": "^(TestOne)$",
        "args": ["go", "test", "-json", "-race", "-p", "1", "-count=1", "-timeout=10m", "-run", "^(TestOne)$", "example.com/mod/internal/" + name],
    }


def minimal_plan():
    shards = []
    for name in runner.v2.SHARD_NAMES:
        parts = [make_part(f"identity-{index}") for index in range(4)] if name == "identity" else [make_part(name)]
        shards.append({"name": name, "parts": parts})
    return {"commit": "a" * 40, "tree": "b" * 40, "shards": shards}


def successful_results(plan):
    results = []
    for shard in plan["shards"]:
        entries = []
        for part in shard["parts"]:
            identifier = "identity-db" if shard["name"] == "identity" else "db-" + part["name"]
            entries.append({
                "name": part["name"], "args": part["args"], "log": f"go-tests-{part['name']}.jsonl",
                "log_sha256": "1" * 64, "exit_status": 0,
                "database": {"url": runner.DATABASE_URL, "system_identifier": identifier},
                "cleanup": {"containers_removed": True, "volume_removed": True, "netns_removed": True},
            })
        results.append({"name": shard["name"], "entries": entries, "errors": []})
    return results


def write_cid(args, value="2" * 64):
    path = pathlib.Path(args[args.index("--cidfile") + 1])
    path.write_text(value + "\n", encoding="ascii")


class AcceptanceParallelRunnerTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)

    def tearDown(self):
        self.temporary.cleanup()

    def test_image_lock_requires_fixed_digests(self):
        locked = runner.load_image_lock()
        self.assertEqual({"go_runner", "postgres"}, set(locked))
        for reference in locked.values():
            self.assertRegex(reference, r"@sha256:[0-9a-f]{64}$")
        invalid = self.root / "images.json"
        value = dict(locked)
        value["schema_version"] = 1
        value["go_runner"] = "golang:latest"
        invalid.write_text(json.dumps(value), encoding="utf-8")
        with self.assertRaises(runner.RunnerError):
            runner.load_image_lock(invalid)

    def test_run_directory_is_new_private_and_outside_source(self):
        source = self.root / "source"
        source.mkdir()
        target = self.root / "attempt"
        with mock.patch.object(runner.shutil, "disk_usage", return_value=SimpleNamespace(free=16 * 1024**3)):
            run_root, evidence, private = runner.prepare_run_directory(source, str(target), "a" * 32)
            self.assertEqual(run_root, target.resolve())
            self.assertFalse(source in target.parents)
            self.assertEqual(0o700, stat.S_IMODE(run_root.stat().st_mode))
            self.assertEqual(0o700, stat.S_IMODE(evidence.stat().st_mode))
            self.assertEqual(0o700, stat.S_IMODE(private.stat().st_mode))
            with self.assertRaises(runner.RunnerError):
                runner.prepare_run_directory(source, str(target), "a" * 32)
            with self.assertRaises(runner.RunnerError):
                runner.prepare_run_directory(source, str(source / "artifacts"), "b" * 32)

    def test_private_credentials_and_ci_opt_ins_are_scoped(self):
        run_root = self.root / "run"
        private = self.root / "private"
        run_root.mkdir()
        private.mkdir()
        env = runner.module_environment(run_root, "other")
        postgres, go_runner = runner.write_private_files(private, "other", run_root, env)
        self.assertEqual(0o600, stat.S_IMODE(postgres.stat().st_mode))
        self.assertEqual(0o600, stat.S_IMODE(go_runner.stat().st_mode))
        postgres_text = postgres.read_text(encoding="utf-8")
        runner_text = go_runner.read_text(encoding="utf-8")
        self.assertIn("POSTGRES_PASSWORD=", postgres_text)
        self.assertNotIn("POSTGRES_PASSWORD", runner_text)
        self.assertIn("TEST_DATABASE_URL=" + runner.DATABASE_URL, runner_text)
        self.assertIn("PROCUREMENT_IDENTITY_DATABASE_URL=" + runner.DATABASE_URL, runner_text)
        self.assertNotIn("PROCUREMENT_ATTEMPT_RULES_DATABASE_URL", runner_text)
        _, billing = runner.write_private_files(private, "billing-0", run_root, env)
        self.assertIn("PROCUREMENT_ATTEMPT_RULES_DATABASE_URL=" + runner.DATABASE_URL, billing.read_text(encoding="utf-8"))

    def test_postgres_uses_none_network_and_exact_loopback_settings(self):
        calls = []

        def fake_docker(args, check=True):
            calls.append(args)
            if args[0] == "exec":
                output = "ready\n" if "CASE WHEN" in args[-1] else "123456789\n"
                return SimpleNamespace(returncode=0, stdout=output, stderr="")
            if args[0] == "run":
                write_cid(args, "1" * 64)
            if args[:2] == ["volume", "inspect"]:
                return SimpleNamespace(returncode=0, stdout=json.dumps({runner.RUN_LABEL: "a" * 32, runner.SHARD_LABEL: "attempt-0"}), stderr="")
            return SimpleNamespace(returncode=0, stdout="", stderr="")

        resources = {}
        inspected = {"Id": "1" * 64, "Config": {"Labels": {runner.RUN_LABEL: "a" * 32, runner.SHARD_LABEL: "attempt-0"}}, "HostConfig": {"NetworkMode": "none", "PortBindings": None, "PublishAllPorts": False}, "NetworkSettings": {"SandboxKey": "/var/run/docker/netns/abcdef123456"}}
        with mock.patch.object(runner, "docker", side_effect=fake_docker), \
             mock.patch.object(runner, "container_absent", return_value=True), \
             mock.patch.object(runner, "volume_absent", return_value=True), \
             mock.patch.object(runner, "container_inspect", return_value=inspected):
            _, identifier = runner.start_postgres("a" * 32, "attempt-0", "postgres@sha256:" + "1" * 64, self.root / "pg.env", "volume", "pg", resources)
        self.assertEqual("123456789", identifier)
        self.assertTrue(resources["volume_created"])
        self.assertEqual("1" * 64, resources["postgres_id"])
        self.assertEqual("/var/run/docker/netns/abcdef123456", resources["sandbox_key"])
        run = next(args for args in calls if args[0] == "run")
        self.assertEqual("none", run[run.index("--network") + 1])
        self.assertNotIn("--publish", run)
        self.assertNotIn("-P", run)
        postgres_index = run.index("postgres")
        self.assertEqual(["postgres", "-p", "55439"], run[postgres_index:postgres_index + 3])
        self.assertIn("listen_addresses=127.0.0.1", run)
        self.assertIn("fsync=on", run)
        self.assertIn("synchronous_commit=on", run)
        self.assertIn("full_page_writes=on", run)

    def test_run_part_preserves_exact_argv_and_removes_container(self):
        evidence = self.root / "evidence"
        evidence.mkdir()
        part = make_part("attempt-0")
        image = "golang@sha256:" + "1" * 64
        captured = {}

        class Process:
            returncode = 0

            def __init__(self, args, stdout, stderr):
                captured["args"] = args
                write_cid(args)

            def poll(self):
                return self.returncode

            def wait(self):
                return self.returncode

        inspected = {
            "Id": "2" * 64, "Config": {"Labels": {runner.RUN_LABEL: "a" * 32, runner.SHARD_LABEL: "attempt-0"}}, "Path": "go", "Args": part["args"][1:], "State": {"ExitCode": 0, "OOMKilled": False},
            "HostConfig": {"NetworkMode": "container:pg-id", "PortBindings": None, "PublishAllPorts": False},
        }
        with mock.patch.object(runner.subprocess, "Popen", Process), \
             mock.patch.object(runner, "container_inspect", return_value=inspected), \
             mock.patch.object(runner, "container_absent", return_value=True), \
             mock.patch.object(runner, "validate_raw_log"), \
             mock.patch.object(runner, "host_listeners", return_value=set()), \
             mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")):
            entry, detail = runner.run_part(self.root, evidence, self.root, self.root, "a" * 32, "attempt-0", part, image, self.root / "runner.env", "pg", "pg-id", {("example.com/mod/internal/attempt-0", "TestOne")}, set())
        argv = captured["args"]
        self.assertIn(f"moni-040-{'a' * 32}-attempt-0", argv)
        self.assertEqual(part["args"], argv[argv.index(image) + 1:])
        docker_prefix = argv[:argv.index(image)]
        self.assertNotIn("--publish", docker_prefix)
        self.assertNotIn("-p", docker_prefix)
        self.assertEqual("container:pg-id", docker_prefix[docker_prefix.index("--network") + 1])
        self.assertEqual([], detail["errors"])
        self.assertTrue(detail["runner_removed"])
        self.assertEqual(0, entry["exit_status"])

    def test_oom_or_nonzero_exit_cannot_be_success(self):
        evidence = self.root / "evidence"
        evidence.mkdir()
        part = make_part("attempt-0")

        class Process:
            returncode = 137

            def __init__(self, args, stdout, stderr):
                write_cid(args)

            def poll(self):
                return self.returncode

            def wait(self):
                return self.returncode

        inspected = {
            "Id": "2" * 64, "Config": {"Labels": {runner.RUN_LABEL: "a" * 32, runner.SHARD_LABEL: "attempt-0"}}, "Path": "go", "Args": part["args"][1:], "State": {"ExitCode": 137, "OOMKilled": True},
            "HostConfig": {"NetworkMode": "container:pg-id", "PortBindings": None, "PublishAllPorts": False},
        }
        with mock.patch.object(runner.subprocess, "Popen", Process), \
             mock.patch.object(runner, "container_inspect", return_value=inspected), \
             mock.patch.object(runner, "container_absent", return_value=True), \
             mock.patch.object(runner, "validate_raw_log") as validate, \
             mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")):
            _, detail = runner.run_part(self.root, evidence, self.root, self.root, "a" * 32, "attempt-0", part, "image", self.root / "runner.env", "pg", "pg-id", {("example.com/mod/internal/attempt-0", "TestOne")}, set())
        validate.assert_not_called()
        self.assertTrue(detail["oom_killed"])
        self.assertTrue(any("137" in error for error in detail["errors"]))

    def test_missing_cidfile_never_deletes_same_name_foreign_container(self):
        evidence = self.root / "evidence"
        evidence.mkdir()
        part = make_part("attempt-0")

        class Process:
            returncode = 125

            def __init__(self, args, stdout, stderr):
                pass

            def poll(self):
                return self.returncode

            def wait(self):
                return self.returncode

        with mock.patch.object(runner.subprocess, "Popen", Process), \
             mock.patch.object(runner, "container_absent", return_value=False), \
             mock.patch.object(runner, "docker") as docker_call:
            _, detail = runner.run_part(self.root, evidence, self.root, self.root, "a" * 32, "attempt-0", part, "image", self.root / "runner.env", "pg", "pg-id", {("example.com/mod/internal/attempt-0", "TestOne")}, set())
        self.assertFalse(detail["runner_removed"])
        self.assertTrue(any("cidfile" in error for error in detail["errors"]))
        self.assertEqual([], [call for call in docker_call.call_args_list if call.args and call.args[0] and call.args[0][0] == "rm"])

    def test_resource_sampling_waits_through_container_registration_race(self):
        evidence = self.root / "evidence"
        evidence.mkdir()
        part = make_part("attempt-0")

        class Process:
            returncode = 0

            def __init__(self, args, stdout, stderr):
                self.calls = 0
                write_cid(args)

            def poll(self):
                self.calls += 1
                return None if self.calls <= 3 else 0

            def wait(self):
                return 0

        inspected = {
            "Id": "2" * 64, "Config": {"Labels": {runner.RUN_LABEL: "a" * 32, runner.SHARD_LABEL: "attempt-0"}}, "Path": "go", "Args": part["args"][1:], "State": {"ExitCode": 0, "OOMKilled": False},
            "HostConfig": {"NetworkMode": "container:pg-id", "PortBindings": None, "PublishAllPorts": False},
        }
        samples = [runner.RunnerError("Error response from daemon: No such container: runner"), [{"Name": "runner"}]]
        with mock.patch.object(runner.subprocess, "Popen", Process), \
             mock.patch.object(runner, "resource_sample", side_effect=samples), \
             mock.patch.object(runner.time, "sleep"), \
             mock.patch.object(runner, "container_inspect", return_value=inspected), \
             mock.patch.object(runner, "container_absent", return_value=True), \
             mock.patch.object(runner, "validate_raw_log"), \
             mock.patch.object(runner, "host_listeners", return_value=set()), \
             mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")):
            _, detail = runner.run_part(self.root, evidence, self.root, self.root, "a" * 32, "attempt-0", part, "image", self.root / "runner.env", "pg", "pg-id", {("example.com/mod/internal/attempt-0", "TestOne")}, set())
        self.assertEqual([], detail["errors"])
        self.assertEqual(1, len(detail["resource_samples"]))

    def test_cleanup_requires_explicit_not_found(self):
        with mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=1, stdout="", stderr="Cannot connect to the Docker daemon")):
            self.assertFalse(runner.container_absent("pg"))
            self.assertFalse(runner.volume_absent("volume"))
        with mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=1, stdout="", stderr="Error: No such object: pg")):
            self.assertTrue(runner.container_absent("pg"))
        with mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=1, stdout="", stderr="Error: No such volume: volume")):
            self.assertTrue(runner.volume_absent("volume"))
        with mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=1, stdout="", stderr="Error response from daemon: get volume: no such volume")):
            self.assertTrue(runner.volume_absent("volume"))

    def test_existing_postgres_names_are_not_adopted_or_removed(self):
        with mock.patch.object(runner, "container_absent", return_value=False), \
             mock.patch.object(runner, "volume_absent", return_value=True), \
             mock.patch.object(runner, "docker") as docker_call:
            with self.assertRaises(runner.RunnerError):
                runner.start_postgres("a" * 32, "attempt-0", "image", self.root / "pg.env", "volume", "pg", {})
        self.assertEqual([], docker_call.call_args_list)

    def test_shard_cleanup_uses_owned_id_and_verifies_netns_absence(self):
        resources = {"postgres_id": "1" * 64, "volume_created": True, "sandbox_key": "/var/run/docker/netns/abcdef123456"}
        with mock.patch.object(runner, "container_absent", return_value=True), \
             mock.patch.object(runner, "volume_absent", return_value=True), \
             mock.patch.object(runner, "execute"), \
             mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")) as docker_call:
            cleanup, errors = runner.cleanup_shard(self.root, self.root / "worktree", "pg-name", "volume-name", resources, True, "go-image")
        self.assertEqual({"containers_removed": True, "volume_removed": True, "netns_removed": True}, cleanup)
        self.assertEqual([], errors)
        removals = [call.args[0] for call in docker_call.call_args_list if call.args and call.args[0] and call.args[0][0] == "rm"]
        self.assertEqual([["rm", "--force", "1" * 64]], removals)
        self.assertNotIn("pg-name", str(docker_call.call_args_list))

    def test_raw_log_validation_reuses_v2_and_reviewed_skip_baseline(self):
        part = make_part("attempt-0")
        package = part["packages"][0]
        expected = {(package, "TestOne")}
        log = self.root / "go-tests-attempt-0.jsonl"
        run_id = "a" * 32
        now = dt.datetime.now(dt.timezone.utc)

        def write(test_action):
            lines = [
                "run_id: " + run_id,
                "command: " + " ".join(part["args"]),
                "started_at: " + (now - dt.timedelta(seconds=2)).isoformat(),
                json.dumps({"Action": test_action, "Package": package, "Test": "TestOne"}),
                json.dumps({"Action": "pass", "Package": package}),
                "completed_at: " + (now - dt.timedelta(seconds=1)).isoformat(),
                "exit_status: 0",
            ]
            log.write_text("\n".join(lines) + "\n", encoding="utf-8")

        write("pass")
        runner.validate_raw_log(log, part, expected, run_id)
        write("skip")
        with self.assertRaises(runner.RunnerError):
            runner.validate_raw_log(log, part, expected, run_id)
        write("pass")
        log.write_text(log.read_text(encoding="utf-8").replace("command: go test", "command: go test -count=2"), encoding="utf-8")
        with self.assertRaises(runner.v2.EvidenceError):
            runner.validate_raw_log(log, part, expected, run_id)

    def test_identity_parts_are_sequential_and_continue_after_failure(self):
        shard = {"name": "identity", "class": "identity", "packages": ["example.com/mod/internal/identity"], "parts": [make_part(f"identity-{index}") for index in range(4)], "commit": "a" * 40, "tree": "b" * 40}
        for part in shard["parts"]:
            part["packages"] = shard["packages"]
        calls = []

        def fake_part(*args):
            part = args[6]
            calls.append(part["name"])
            entry = {"name": part["name"], "database": {"system_identifier": ""}, "cleanup": {}}
            detail = {"name": part["name"], "runner_removed": True, "errors": ["synthetic failure"] if part["name"] == "identity-1" else []}
            return entry, detail

        cleanup = {"containers_removed": True, "volume_removed": True, "netns_removed": True}
        with mock.patch.object(runner, "create_worktree", return_value=self.root), \
             mock.patch.object(runner, "require_worktree_clean"), \
             mock.patch.object(runner, "prefetch_modules"), \
             mock.patch.object(runner, "write_private_files", return_value=(self.root / "pg", self.root / "go")), \
             mock.patch.object(runner, "start_postgres", return_value=({"Id": "pg-id"}, "identity-db")), \
             mock.patch.object(runner, "run_part", side_effect=fake_part), \
             mock.patch.object(runner, "cleanup_shard", return_value=(cleanup, [])):
            result = runner.run_shard(self.root, self.root, self.root, self.root, shard, "a" * 32, {"go_runner": "go", "postgres": "pg"}, set())
        self.assertEqual([f"identity-{index}" for index in range(4)], calls)
        self.assertEqual(4, len(result["entries"]))
        self.assertTrue(any("synthetic failure" in error for error in result["errors"]))
        self.assertTrue(all(entry["database"]["system_identifier"] == "identity-db" for entry in result["entries"]))
        self.assertTrue(all(entry["cleanup"] == cleanup for entry in result["entries"]))

    def test_fail_fast_false_runs_later_shards(self):
        calls = []

        def run_one(shard):
            calls.append(shard["name"])
            if len(calls) == 1:
                raise runner.RunnerError("first failed")
            return {"name": shard["name"], "errors": []}

        results = runner.run_selected([{"name": "attempt-0"}, {"name": "attempt-1"}], run_one)
        self.assertEqual(["attempt-0", "attempt-1"], calls)
        self.assertEqual(["first failed"], results[0]["errors"])
        self.assertEqual([], results[1]["errors"])

    def test_complete_manifest_shape_and_failure_closed_guards(self):
        plan = minimal_plan()
        evidence = self.root / "evidence"
        evidence.mkdir()
        (evidence / "shard-plan.json").write_text("{}\n", encoding="utf-8")
        images = {"go_runner": "go@sha256:" + "1" * 64, "postgres": "pg@sha256:" + "2" * 64}
        results = successful_results(plan)
        manifest = runner.build_manifest(plan, evidence, "a" * 32, dt.datetime.now(dt.timezone.utc).isoformat(), images, results)
        self.assertEqual(2, manifest["schema_version"])
        self.assertEqual(1, manifest["workers"])
        self.assertEqual(17, len(manifest["shards"]))
        self.assertEqual("sha256:" + "1" * 64, manifest["images"]["go_runner"])
        self.assertEqual(4, sum(entry["database"]["system_identifier"] == "identity-db" for entry in manifest["shards"]))
        cases = []
        partial = copy.deepcopy(results)
        partial.pop()
        cases.append(partial)
        failed = copy.deepcopy(results)
        failed[0]["errors"] = ["FAIL"]
        cases.append(failed)
        dirty = copy.deepcopy(results)
        dirty[0]["entries"][0]["cleanup"]["volume_removed"] = False
        cases.append(dirty)
        reused = copy.deepcopy(results)
        reused[1]["entries"][0]["database"]["system_identifier"] = reused[0]["entries"][0]["database"]["system_identifier"]
        cases.append(reused)
        for value in cases:
            with self.subTest(case=value[0]["name"]):
                with self.assertRaises(runner.RunnerError):
                    runner.build_manifest(plan, evidence, "a" * 32, dt.datetime.now(dt.timezone.utc).isoformat(), images, value)

    def test_failed_merge_removes_reusable_manifest(self):
        evidence = self.root / "evidence"
        evidence.mkdir()
        with mock.patch.object(runner, "build_manifest", return_value={"schema_version": 2}), \
             mock.patch.object(runner, "required_specs", return_value=["./internal/x:TestX"]), \
             mock.patch.object(runner, "execute", side_effect=runner.RunnerError("merge failed")):
            with self.assertRaises(runner.RunnerError):
                runner.merge_complete_evidence(self.root, evidence, {}, "a" * 32, {}, [])
        self.assertFalse((evidence / "go-race-evidence-v2.json").exists())

    def test_proc_listener_parser_normalizes_loopback_and_filters_non_listeners(self):
        raw_v4 = "  sl  local_address rem_address   st\n   0: 0100007F:D88F 00000000:0000 0A\n   1: 0100007F:1F90 00000000:0000 01\n"
        self.assertEqual({"127.0.0.1:55439"}, runner.parse_proc_net_tcp(raw_v4, socket.AF_INET))
        raw_v6 = "  sl  local_address rem_address   st\n   0: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A\n"
        self.assertEqual({"[::1]:8080"}, runner.parse_proc_net_tcp(raw_v6, socket.AF_INET6))

    def test_netns_absence_uses_host_sandbox_path_and_fails_closed(self):
        key = "/var/run/docker/netns/abcdef123456"
        with mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")) as docker_call:
            self.assertTrue(runner.netns_absent(key, "go-image"))
        args = docker_call.call_args.args[0]
        self.assertIn("type=bind,source=/var/run/docker/netns,target=/host-netns,readonly", args)
        self.assertIn("os.path.lexists", args[-2])
        with mock.patch.object(runner, "docker", return_value=SimpleNamespace(returncode=1, stdout="", stderr="")):
            self.assertFalse(runner.netns_absent(key, "go-image"))
        with self.assertRaises(runner.RunnerError):
            runner.netns_absent("/tmp/not-a-docker-sandbox", "go-image")

    def test_attempt_scope_distinguishes_partial_from_full(self):
        self.assertEqual("workers_1_canary", runner.attempt_scope(1))
        self.assertEqual("workers_1_partial", runner.attempt_scope(2))
        self.assertEqual("workers_1_full_go_race", runner.attempt_scope(14))

    def test_host_listener_change_and_existing_acceptance_port_fail(self):
        baseline = {"127.0.0.1:8080"}
        runner.require_listener_set(baseline, baseline, "test")
        with self.assertRaises(runner.RunnerError):
            runner.require_listener_set({"127.0.0.1:8080", "127.0.0.1:9090"}, baseline, "test")
        runner.require_no_acceptance_host_port(baseline)
        with self.assertRaises(runner.RunnerError):
            runner.require_no_acceptance_host_port({"127.0.0.1:55439"})


if __name__ == "__main__":
    unittest.main()

import importlib.util
import json
import os
import pathlib
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("acceptance-parallel-coordinator.py")
SPEC = importlib.util.spec_from_file_location("acceptance_parallel_coordinator", SCRIPT)
coordinator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(coordinator)


class AcceptanceParallelCoordinatorTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)
        self.source = self.root / "source"
        self.source.mkdir()
        self.run_directory = self.root / "artifacts" / "run"
        self.run_directory.parent.mkdir()
        self.environment = mock.patch.dict(os.environ, {"DOCKER_CONTEXT": "colima-moni-acceptance"})
        self.environment.start()

    def tearDown(self):
        self.environment.stop()
        self.temporary.cleanup()

    def command(self, workers=2):
        with mock.patch.object(coordinator, "git_common_dir", return_value=self.root / "common-git"), \
             mock.patch.object(coordinator, "go_image", return_value="golang@sha256:" + "1" * 64):
            return coordinator.coordinator_command(self.source, self.run_directory, "a" * 32, workers, ["--all-shards"])

    def test_coordinator_uses_host_netns_pinned_image_and_owned_mounts(self):
        args, cidfile, name = self.command()
        self.assertEqual(f"moni-040-coordinator-{'a' * 32}", name)
        self.assertEqual(self.run_directory.parent.resolve() / f".{'a' * 32}.coordinator.cid", cidfile)
        self.assertEqual("host", args[args.index("--network") + 1])
        self.assertNotIn("--publish", args)
        self.assertIn("--read-only", args)
        self.assertIn("DOCKER_CONTEXT=default", args)
        self.assertIn("DOCKER_HOST=unix:///var/run/docker.sock", args)
        self.assertIn(f"GOCACHE={self.run_directory.resolve()}/cache/shared-build", args)
        self.assertIn(f"GOMODCACHE={self.run_directory.resolve()}/cache/shared-mod", args)
        self.assertNotIn(f"DOCKER_CONTEXT={os.environ['DOCKER_CONTEXT']}", args)
        self.assertIn("golang@sha256:" + "1" * 64, args)
        mounts = [arg for arg in args if arg.startswith("type=bind,")]
        self.assertIn(coordinator.mount(self.source.resolve()), mounts)
        self.assertIn(coordinator.mount(self.run_directory.parent.resolve()), mounts)
        self.assertIn(coordinator.mount(self.root / "common-git"), mounts)
        self.assertIn("type=bind,source=/usr/bin/docker,target=/usr/bin/docker,readonly", mounts)
        self.assertIn("type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock", mounts)
        runner = args[args.index("acceptance-parallel-runner.py"):] if "acceptance-parallel-runner.py" in args else args[args.index(next(arg for arg in args if arg.endswith("acceptance-parallel-runner.py"))):]
        self.assertIn("--workers", runner)
        self.assertEqual("2", runner[runner.index("--workers") + 1])
        self.assertIn("--all-shards", runner)

    def test_optional_goproxy_is_forwarded_when_explicitly_selected(self):
        with mock.patch.dict(os.environ, {"PHASE1_ACCEPTANCE_GOPROXY": "https://goproxy.cn,direct"}):
            args, _, _ = self.command()
        self.assertIn("GOPROXY=https://goproxy.cn,direct", args)
        self.assertIn("PHASE1_ACCEPTANCE_GOPROXY=https://goproxy.cn,direct", args)

    def test_workers_three_is_rejected_before_docker(self):
        with self.assertRaises(coordinator.CoordinatorError):
            self.command(3)

    def test_missing_cidfile_never_deletes_same_name_container(self):
        with mock.patch.object(coordinator, "container_absent", return_value=False), \
             mock.patch.object(coordinator, "execute") as execute:
            with self.assertRaises(coordinator.CoordinatorError):
                coordinator.cleanup_coordinator(self.root / "missing.cid", "coordinator", "a" * 32)
        self.assertEqual([], [call for call in execute.call_args_list if call.args and call.args[0][:2] == ["docker", "rm"]])

    def test_completed_coordinator_requires_cidfile_even_when_name_is_absent(self):
        with mock.patch.object(coordinator, "container_absent", return_value=True), \
             mock.patch.object(coordinator, "execute") as execute:
            with self.assertRaisesRegex(coordinator.CoordinatorError, "without a regular owned cidfile"):
                coordinator.cleanup_coordinator(self.root / "missing.cid", "coordinator", "a" * 32, require_cidfile=True)
        self.assertEqual([], [call for call in execute.call_args_list if call.args and call.args[0][:2] == ["docker", "rm"]])

    def test_cleanup_uses_exact_owned_id_and_removes_cidfile(self):
        cidfile = self.root / "coordinator.cid"
        container_id = "2" * 64
        cidfile.write_text(container_id + "\n", encoding="ascii")
        inspected = [{"Id": container_id, "Config": {"Labels": {coordinator.COORDINATOR_LABEL: "a" * 32}}}]
        calls = []

        def fake_execute(args, check=True):
            calls.append(args)
            if args[:3] == ["docker", "container", "inspect"]:
                return SimpleNamespace(returncode=0, stdout=json.dumps(inspected), stderr="")
            return SimpleNamespace(returncode=0, stdout="", stderr="")

        with mock.patch.object(coordinator, "execute", side_effect=fake_execute), \
             mock.patch.object(coordinator, "container_absent", return_value=True):
            coordinator.cleanup_coordinator(cidfile, "coordinator", "a" * 32)
        self.assertIn(["docker", "rm", "--force", container_id], calls)
        self.assertFalse(cidfile.exists())


if __name__ == "__main__":
    unittest.main()

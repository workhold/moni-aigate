import hashlib
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("check_go_test_evidence", pathlib.Path(__file__).with_name("check-go-test-evidence.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class GoTestEvidenceTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.log = self.root / "go-race.jsonl"
        self.manifest = self.root / "manifest.json"
        self.commit = subprocess.run(["git", "rev-parse", "HEAD"], cwd=checker.ROOT, text=True, capture_output=True, check=True).stdout.strip()
        self.run_id = "a" * 32
        self.environment = mock.patch.dict(checker.os.environ, {checker.RUN_ID_ENV: self.run_id})
        self.environment.start()
        self.clean = mock.patch.object(checker, "require_clean_source", lambda: None)
        self.clean.start()
        self.specs = ["./internal/server:TestOne", "./migrations:TestTwo"]

    def tearDown(self):
        self.clean.stop()
        self.environment.stop()
        self.temp.cleanup()

    def write(self, events, manifest_updates=None):
        at = checker.dt.datetime.now(checker.dt.timezone.utc).isoformat()
        self.log.write_text("command: go test -race ./...\nstarted_at: " + at + "\n\n" + "".join(json.dumps(event) + "\n" for event in events) + "\ncompleted_at: " + at + "\nexit_status: 0\n", encoding="utf-8")
        value = {"schema_version": 1, "suite": "go-race", "run_id": self.run_id, "created_at": at, "commit": self.commit, "database": checker.EXPECTED_DATABASE, "log": "go-race.jsonl", "log_sha256": hashlib.sha256(self.log.read_bytes()).hexdigest()}
        if manifest_updates:
            value.update(manifest_updates)
        self.manifest.write_text(json.dumps(value), encoding="utf-8")

    def passing(self):
        return [
            {"Action": "pass", "Package": "github.com/moni-ai/moni-aigate/internal/server", "Test": "TestOne"},
            {"Action": "skip", "Package": "github.com/moni-ai/moni-aigate/internal/server", "Test": "TestEnvironmentOnly"},
            {"Action": "pass", "Package": "github.com/moni-ai/moni-aigate/migrations", "Test": "TestTwo"},
        ]

    def test_valid_evidence_reuses_exact_top_level_passes(self):
        self.write(self.passing())
        self.assertEqual(checker.verify(self.manifest, self.specs), 2)

    def test_missing_package_pass_skip_duplicate_and_other_failure_are_rejected(self):
        cases = [
            [{"Action": "pass", "Package": "github.com/moni-ai/moni-aigate/internal/server"}],
            self.passing()[:1],
            self.passing() + [{"Action": "pass", "Package": "github.com/moni-ai/moni-aigate/internal/server", "Test": "TestOne"}],
            self.passing() + [{"Action": "skip", "Package": "github.com/moni-ai/moni-aigate/internal/server", "Test": "TestOne"}],
            self.passing() + [{"Action": "fail", "Package": "github.com/moni-ai/moni-aigate/internal/other", "Test": "TestOther"}],
        ]
        for events in cases:
            with self.subTest(events=events[-1]):
                self.write(events)
                with self.assertRaises(checker.EvidenceError):
                    checker.verify(self.manifest, self.specs)

    def test_manifest_commit_database_schema_and_log_digest_are_mandatory(self):
        for update in [{"commit": "0" * 40}, {"database": "postgres://wrong"}, {"schema_version": 2}, {"schema_version": True}, {"run_id": "b" * 32}, {"created_at": "2000-01-01T00:00:00+00:00"}, {"log_sha256": "0" * 64}, {"log": "other.jsonl"}]:
            with self.subTest(update=update):
                self.write(self.passing(), update)
                with self.assertRaises(checker.EvidenceError):
                    checker.verify(self.manifest, self.specs)

    def test_modified_log_and_invalid_specs_are_rejected(self):
        self.write(self.passing())
        self.log.write_text(self.log.read_text() + '{"Action":"pass"}\n')
        with self.assertRaises(checker.EvidenceError):
            checker.verify(self.manifest, self.specs)
        self.write(self.passing())
        for spec in ["", "./internal/server", "internal/server:TestOne", "./internal/server:TestOne/child", "./internal/server:Test One"]:
            with self.subTest(spec=spec):
                with self.assertRaises(checker.EvidenceError):
                    checker.verify(self.manifest, [spec])
    def test_dirty_source_and_duplicate_json_keys_are_rejected(self):
        self.write(self.passing())
        with mock.patch.object(checker, "require_clean_source", side_effect=checker.EvidenceError("dirty worktree")):
            with self.assertRaises(checker.EvidenceError):
                checker.verify(self.manifest, self.specs)
        raw = '{"Action":"fail","Action":"pass","Package":"github.com/moni-ai/moni-aigate/internal/server","Test":"TestOne"}\n'
        self.log.write_text(raw, encoding="utf-8")
        value = json.loads(self.manifest.read_text())
        value["log_sha256"] = hashlib.sha256(self.log.read_bytes()).hexdigest()
        self.manifest.write_text(json.dumps(value), encoding="utf-8")
        with self.assertRaises(checker.EvidenceError):
            checker.verify(self.manifest, self.specs)


if __name__ == "__main__":
    unittest.main()

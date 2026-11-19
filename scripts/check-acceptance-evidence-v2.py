#!/usr/bin/env python3
"""Validate parallel acceptance evidence (schema v2) without executing tests."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

COMMON_PATH = pathlib.Path(__file__).with_name("check-go-test-evidence.py")
SPEC = importlib.util.spec_from_file_location("acceptance_evidence_common", COMMON_PATH)
common = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(common)
EvidenceError = common.EvidenceError
SHARD_NAMES = [
    "billing-0", "billing-1", "billing-2", "billing-3",
    "server-0", "server-1", "server-2", "server-3",
    "attempt-0", "attempt-1", "attempt-2", "attempt-3",
    "identity", "other",
]
TEST_NAME = re.compile(r"(?:Test|Fuzz|Example)[A-Za-z0-9_]*")
BENCHMARK_NAME = re.compile(r"Benchmark[A-Za-z0-9_]*")


def digest_bytes(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def digest_file(path: pathlib.Path) -> str:
    return digest_bytes(path.read_bytes())


def canonical_plan_digest(plan: dict) -> str:
    value = dict(plan)
    value["digest"] = ""
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
    return digest_bytes(raw)


def reviewed_baseline() -> list[dict]:
    path = common.ROOT / "scripts" / "acceptance-skip-baseline.json"
    value = common.strict_json(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or set(value) != {"schema_version", "entries"}:
        raise EvidenceError("reviewed skip baseline schema is invalid")
    if type(value["schema_version"]) is not int or value["schema_version"] != 1 or not isinstance(value["entries"], list):
        raise EvidenceError("reviewed skip baseline schema is invalid")
    previous = None
    for entry in value["entries"]:
        if not isinstance(entry, dict) or set(entry) != {"package", "test", "reason"}:
            raise EvidenceError("reviewed skip baseline entry is invalid")
        if not all(isinstance(entry[key], str) for key in ("package", "test", "reason")) or entry["reason"] != "":
            raise EvidenceError("go test -json has no reliable skip reason; baseline reasons must be empty")
        key = (entry["package"], entry["test"])
        if previous is not None and key <= previous:
            raise EvidenceError("reviewed skip baseline must be sorted and unique")
        previous = key
    return value["entries"]


def load_plan(manifest_path: pathlib.Path, manifest: dict) -> dict:
    if manifest["plan"] != "shard-plan.json" or not isinstance(manifest["plan_sha256"], str) or not re.fullmatch(r"[0-9a-f]{64}", manifest["plan_sha256"]):
        raise EvidenceError("v2 plan reference is invalid")
    path = manifest_path.parent / manifest["plan"]
    if path.is_symlink() or not path.is_file():
        raise EvidenceError("v2 shard plan is not a regular file")
    raw = path.read_bytes()
    if digest_bytes(raw) != manifest["plan_sha256"]:
        raise EvidenceError("v2 shard plan digest does not match")
    plan = common.strict_json(raw.decode(encoding="utf-8"))
    if not isinstance(plan, dict) or set(plan) != {"schema_version", "commit", "tree", "module", "shards", "digest"}:
        raise EvidenceError("v2 shard plan schema is invalid")
    if type(plan["schema_version"]) is not int or plan["schema_version"] != 1 or plan["digest"] != canonical_plan_digest(plan):
        raise EvidenceError("v2 shard plan identity or digest is invalid")
    if plan["commit"] != manifest["commit"] or plan["tree"] != manifest["tree"]:
        raise EvidenceError("v2 plan and manifest source identities differ")
    return plan


def load_manifest(path: pathlib.Path) -> tuple[dict, dict]:
    if path.is_symlink() or not path.is_file():
        raise EvidenceError("v2 manifest is not a regular file")
    manifest = common.strict_json(path.read_text(encoding="utf-8"))
    keys = {"schema_version", "suite", "run_id", "created_at", "commit", "tree", "source_clean", "scheduler_version", "workers", "images", "plan", "plan_sha256", "skip_baseline", "shards"}
    if not isinstance(manifest, dict) or set(manifest) != keys:
        raise EvidenceError("v2 manifest schema is invalid")
    if type(manifest["schema_version"]) is not int or manifest["schema_version"] != 2 or manifest["suite"] != "go-race-parallel":
        raise EvidenceError("v2 manifest schema is invalid")
    if not isinstance(manifest["run_id"], str) or not re.fullmatch(r"[0-9a-f]{32}", manifest["run_id"]) or os.environ.get(common.RUN_ID_ENV) != manifest["run_id"]:
        raise EvidenceError("v2 run identity does not match this invocation")
    created = common.timestamp(manifest["created_at"])
    age = common.time.time() - created.timestamp()
    if age < -300 or age > 3600:
        raise EvidenceError("v2 evidence is stale or has an invalid creation time")
    if not isinstance(manifest["commit"], str) or not re.fullmatch(r"[0-9a-f]{40}", manifest["commit"]) or manifest["commit"] != common.current_commit():
        raise EvidenceError("v2 commit does not match HEAD")
    actual_tree = subprocess.run(["git", "-C", str(common.ROOT), "rev-parse", "HEAD^{tree}"], text=True, capture_output=True, check=True).stdout.strip()
    if manifest["tree"] != actual_tree:
        raise EvidenceError("v2 tree does not match HEAD")
    if manifest["source_clean"] is not True:
        raise EvidenceError("v2 source must be declared clean")
    if type(manifest["scheduler_version"]) is not int or manifest["scheduler_version"] != 1:
        raise EvidenceError("unsupported v2 scheduler version")
    if type(manifest["workers"]) is not int or manifest["workers"] not in (1, 2):
        raise EvidenceError("v2 workers must be one or two")
    images = manifest["images"]
    if not isinstance(images, dict) or set(images) != {"go_runner", "postgres"} or any(not isinstance(value, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", value) for value in images.values()):
        raise EvidenceError("v2 image identities must be fixed SHA-256 digests")
    if not isinstance(manifest["skip_baseline"], list) or manifest["skip_baseline"] != reviewed_baseline():
        raise EvidenceError("v2 skip baseline is not the reviewed repository baseline")
    if not isinstance(manifest["shards"], list):
        raise EvidenceError("v2 shard entries must be an array")
    return manifest, load_plan(path, manifest)


def expected_args(part: dict) -> list[str]:
    args = ["go", "test", "-json", "-race", "-p", "1", "-count=1", "-timeout=10m"]
    if part.get("run_filter"):
        args += ["-run", part["run_filter"]]
    return args + part["packages"]


def part_index(plan: dict) -> dict[str, dict]:
    if not isinstance(plan["module"], str) or not plan["module"] or not isinstance(plan["shards"], list) or len(plan["shards"]) != 14:
        raise EvidenceError("v2 requires exactly 14 logical shards")
    result = {}
    allocated = {}
    class_tests = {}
    class_discovery = {}
    for index, shard in enumerate(plan["shards"]):
        if not isinstance(shard, dict):
            raise EvidenceError("v2 shard must be an object")
        expected_name = SHARD_NAMES[index]
        expected_class = expected_name.rsplit("-", 1)[0]
        expected_keys = {"name", "class", "packages", "parts"} if expected_class == "other" else {"name", "class", "packages", "discovery", "parts"}
        if set(shard) != expected_keys or shard["name"] != expected_name or shard["class"] != expected_class:
            raise EvidenceError(f"v2 shard identity is invalid at position {index}")
        if not isinstance(shard["packages"], list) or not shard["packages"] or not all(isinstance(package, str) and package for package in shard["packages"]):
            raise EvidenceError(f"v2 shard packages are invalid for {expected_name}")
        parts = shard["parts"]
        if not isinstance(parts, list):
            raise EvidenceError(f"v2 parts must be an array for {expected_name}")
        expected_parts = 4 if expected_class == "identity" else 1
        if len(parts) != expected_parts:
            raise EvidenceError(f"v2 part count is invalid for {expected_name}")
        for part_position, part in enumerate(parts):
            if not isinstance(part, dict) or set(part) != {"name", "packages", "tests", "run_filter", "args"}:
                raise EvidenceError(f"v2 part schema is invalid for {expected_name}")
            expected_part_name = f"identity-{part_position}" if expected_class == "identity" else expected_name
            if part["name"] != expected_part_name or part["packages"] != shard["packages"] or part["args"] != expected_args(part):
                raise EvidenceError(f"v2 part identity or argv is invalid for {expected_part_name}")
            if expected_class == "other":
                if part["tests"] is not None or part["run_filter"] != "":
                    raise EvidenceError("other must remain unfiltered")
                tests = []
            else:
                if not isinstance(part["tests"], list) or not part["tests"] or not all(isinstance(test, str) and TEST_NAME.fullmatch(test) for test in part["tests"]):
                    raise EvidenceError(f"v2 tests are invalid for {expected_part_name}")
                tests = part["tests"]
            if tests != sorted(tests) or len(set(tests)) != len(tests):
                raise EvidenceError(f"v2 part tests are not unique and ordered for {expected_part_name}")
            expected_filter = "^(" + "|".join(tests) + ")$" if tests else ""
            if part["run_filter"] != expected_filter or part["args"] != expected_args(part):
                raise EvidenceError(f"v2 run filter or argv is invalid for {expected_part_name}")
            for test in tests:
                key = (shard["packages"][0], test)
                if key in allocated:
                    raise EvidenceError(f"duplicate source allocation for {key[0]}:{key[1]}")
                allocated[key] = expected_part_name
            class_tests.setdefault(expected_class, []).extend(tests)
            if part["name"] in result:
                raise EvidenceError("duplicate part in shard plan")
            result[part["name"]] = part
        if expected_class != "other":
            discovery = shard["discovery"]
            if not isinstance(discovery, list) or not all(isinstance(test, str) for test in discovery) or len(set(discovery)) != len(discovery):
                raise EvidenceError(f"v2 discovery is invalid for {expected_name}")
            if expected_class in class_discovery and class_discovery[expected_class] != discovery:
                raise EvidenceError(f"v2 raw discovery differs between parts of {expected_class}")
            class_discovery[expected_class] = discovery
    for shard_class, discovery in class_discovery.items():
        if sorted(discovery) != sorted(class_tests[shard_class]):
            raise EvidenceError(f"v2 discovery and allocated part tests differ for {shard_class}")
    return result


def parse_other_json_discovery(raw: str, packages: list[str]) -> dict[str, list[str]]:
    other_tests = {package: [] for package in packages}
    for raw_line in raw.splitlines():
        if not raw_line:
            continue
        event = common.strict_json(raw_line)
        if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
            raise EvidenceError("source discovery event is invalid")
        if event["Action"] != "output":
            continue
        package = event.get("Package")
        output = event.get("Output")
        if package not in other_tests or not isinstance(output, str):
            raise EvidenceError("source discovery output escaped the other package set")
        line = output.rstrip("\n")
        if TEST_NAME.fullmatch(line):
            other_tests[package].append(line)
        elif BENCHMARK_NAME.fullmatch(line) or line.startswith("ok ") or line.startswith("ok\t") or line.startswith("?"):
            continue
        else:
            raise EvidenceError(f"unexpected source discovery output for {package}: {line}")
    for package, tests in other_tests.items():
        if len(tests) != len(set(tests)):
            raise EvidenceError(f"duplicate source discovery for {package}")
    return other_tests


def fresh_source() -> tuple[dict, dict[str, list[str]]]:
    """Regenerate the PR-A plan and discover unfiltered other tests at HEAD."""
    with tempfile.TemporaryDirectory(prefix="acceptance-v2-source-") as directory:
        root = pathlib.Path(directory)
        checkout = root / "checkout"
        subprocess.run(["git", "-C", str(common.ROOT), "worktree", "add", "--detach", str(checkout), "HEAD"], text=True, capture_output=True, check=True)
        try:
            output = root / "plan"
            subprocess.run(["go", "run", "./scripts/acceptance-shard-plan", "--emit-legacy", str(output)], cwd=checkout, text=True, capture_output=True, check=True)
            plan = common.strict_json((output / "shard-plan.json").read_text(encoding="utf-8"))
            other = next(shard for shard in plan["shards"] if shard["class"] == "other")
            result = subprocess.run(["go", "test", "-json", "-race", "-count=1", "-list", ".", *other["packages"]], cwd=checkout, text=True, capture_output=True, check=True)
            other_tests = parse_other_json_discovery(result.stdout, other["packages"])
            return plan, other_tests
        finally:
            subprocess.run(["git", "-C", str(common.ROOT), "worktree", "remove", "--force", str(checkout)], text=True, capture_output=True, check=True)


def allocation_from_plan(plan: dict, other_tests: dict[str, list[str]]) -> dict[str, set[tuple[str, str]]]:
    parts = part_index(plan)
    allocation = {}
    globally_allocated = {}
    for shard in plan["shards"]:
        if shard["class"] == "other":
            if set(other_tests) != set(shard["packages"]):
                raise EvidenceError("other source package discovery is incomplete")
            for package, tests in other_tests.items():
                if not isinstance(tests, list) or not all(isinstance(test, str) and TEST_NAME.fullmatch(test) for test in tests) or len(tests) != len(set(tests)):
                    raise EvidenceError(f"other source discovery is invalid for {package}")
            for part in shard["parts"]:
                allocation[part["name"]] = {(package, test) for package, tests in other_tests.items() for test in tests}
            continue
        for part in shard["parts"]:
            allocation[part["name"]] = {(shard["packages"][0], test) for test in part["tests"]}
    for part_name, tests in allocation.items():
        for key in tests:
            if key in globally_allocated:
                raise EvidenceError(f"duplicate global allocation for {key[0]}:{key[1]}")
            globally_allocated[key] = part_name
    if set(allocation) != set(parts):
        raise EvidenceError("source allocation does not cover every execution part")
    return allocation


def source_allocation(plan: dict) -> dict[str, set[tuple[str, str]]]:
    fresh_plan, other_tests = fresh_source()
    if fresh_plan != plan:
        raise EvidenceError("v2 shard plan differs from a fresh discovery of the current source")
    return allocation_from_plan(fresh_plan, other_tests)


def parse_events(path: pathlib.Path, part: dict, expected: set[tuple[str, str]], manifest: dict) -> tuple[dict, dict, dict]:
    passes: dict[tuple[str, str], int] = {}
    skips: dict[tuple[str, str], int] = {}
    package_terminals: dict[str, list[str]] = {}
    seen = {"run_id": False, "command": False, "started": False, "completed": False, "exit": False}
    started = None
    completed = None
    with path.open(encoding="utf-8") as source:
        for raw in source:
            line = raw.strip()
            if not line:
                continue
            if seen["exit"]:
                raise EvidenceError("raw shard contains a record after exit_status")
            if line.startswith("run_id: "):
                if seen["run_id"] or line != "run_id: " + manifest["run_id"]:
                    raise EvidenceError("raw shard run identity is missing, duplicated, or from another attempt")
                seen["run_id"] = True
                continue
            if line.startswith("command: "):
                if seen["command"] or seen["started"] or not seen["run_id"] or line != "command: " + " ".join(part["args"]):
                    raise EvidenceError("raw shard command framing is invalid")
                seen["command"] = True
                continue
            if line.startswith("started_at: "):
                if seen["started"] or seen["completed"] or not seen["command"]:
                    raise EvidenceError("raw shard start framing is invalid")
                started = common.timestamp(line.split(": ", 1)[1])
                seen["started"] = True
                continue
            if line.startswith("completed_at: "):
                if seen["completed"] or not seen["started"]:
                    raise EvidenceError("raw shard completion framing is invalid")
                completed = common.timestamp(line.split(": ", 1)[1])
                seen["completed"] = True
                continue
            if line.startswith("exit_status: "):
                if not seen["completed"] or line != "exit_status: 0":
                    raise EvidenceError("raw shard exit framing is invalid")
                seen["exit"] = True
                continue
            if not seen["started"] or seen["completed"]:
                raise EvidenceError("raw shard events must be between start and completion")
            event = common.strict_json(line)
            if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
                raise EvidenceError("v2 log event is invalid")
            action = event["Action"]
            if action == "fail":
                raise EvidenceError("v2 log contains a failed package or test")
            package = event.get("Package")
            test = event.get("Test")
            if test is None or test == "":
                if action in {"pass", "skip"}:
                    if not isinstance(package, str) or package not in part["packages"]:
                        raise EvidenceError("unexpected or cross-shard package terminal")
                    package_terminals.setdefault(package, []).append(action)
                continue
            if not isinstance(package, str) or not isinstance(test, str):
                raise EvidenceError("v2 test event identity is invalid")
            if "/" in test:
                continue
            key = (package, test)
            if action == "pass":
                if key not in expected:
                    raise EvidenceError(f"unexpected or cross-shard PASS: {package}:{test}")
                passes[key] = passes.get(key, 0) + 1
            elif action == "skip":
                if key not in expected:
                    raise EvidenceError(f"unexpected or cross-shard SKIP: {package}:{test}")
                skips[key] = skips.get(key, 0) + 1
    if not all(seen.values()):
        raise EvidenceError("raw shard log lacks complete run/command/start/completion/exit framing")
    created = common.timestamp(manifest["created_at"])
    if started > completed or started < created - dt.timedelta(hours=1) or completed > created + dt.timedelta(minutes=5):
        raise EvidenceError("raw shard timestamps are outside this attempt")
    return passes, skips, package_terminals


def raw_log_interval(path: pathlib.Path) -> tuple[dt.datetime, dt.datetime]:
    started = None
    completed = None
    with path.open(encoding="utf-8") as source:
        for line in source:
            if line.startswith("started_at: "):
                started = common.timestamp(line.split(": ", 1)[1].strip())
            elif line.startswith("completed_at: "):
                completed = common.timestamp(line.split(": ", 1)[1].strip())
    if started is None or completed is None or started >= completed:
        raise EvidenceError("raw shard has an invalid execution interval")
    return started, completed


def validate_schedule(workers: int, intervals: dict[str, tuple[dt.datetime, dt.datetime]]) -> None:
    identity = sorted(name for name in intervals if name.startswith("identity-"))
    if len(identity) != 4:
        raise EvidenceError("v2 identity execution intervals are incomplete")
    for previous, current in zip(identity, identity[1:]):
        if intervals[current][0] < intervals[previous][1]:
            raise EvidenceError("identity execution parts must not overlap")
    transitions = []
    for name, (started, completed) in intervals.items():
        transitions.append((started, 1, name))
        transitions.append((completed, -1, name))
    transitions.sort(key=lambda item: (item[0], item[1]))
    active = 0
    maximum = 0
    for _, change, _ in transitions:
        active += change
        maximum = max(maximum, active)
        if active > workers:
            raise EvidenceError(f"v2 evidence exceeds workers={workers} concurrent Go parts")
    if maximum != workers:
        raise EvidenceError(f"v2 evidence does not demonstrate workers={workers} scheduling")


def verify(manifest_path: pathlib.Path, specs: list[str]) -> int:
    common.require_clean_source()
    manifest, plan = load_manifest(manifest_path)
    parts = part_index(plan)
    allocation = source_allocation(plan)
    required = [common.parse_spec(spec) for spec in specs]
    if not required or len(set(required)) != len(required):
        raise EvidenceError("v2 requires a nonempty unique test inventory")
    required_set = {(plan["module"] + "/" + package, test) for package, test in required}
    baseline = reviewed_baseline()
    baseline_set = {(entry["package"], entry["test"]) for entry in baseline}
    all_skips = []
    seen_databases: dict[str, str] = {}
    seen_shards = set()
    intervals = {}
    part_classes = {part["name"]: shard["class"] for shard in plan["shards"] for part in shard["parts"]}
    for entry in manifest["shards"]:
        if not isinstance(entry, dict) or set(entry) != {"name", "args", "log", "log_sha256", "exit_status", "database", "cleanup"}:
            raise EvidenceError("v2 shard entry schema is invalid")
        name = entry["name"]
        if not isinstance(name, str) or name in seen_shards:
            raise EvidenceError(f"duplicate or invalid v2 shard entry {name}")
        seen_shards.add(name)
        if name not in parts:
            raise EvidenceError(f"unexpected v2 shard {name}")
        part = parts[name]
        if entry["args"] != part["args"] or entry["log"] != f"go-tests-{name}.jsonl":
            raise EvidenceError(f"v2 shard command/log identity differs for {name}")
        if type(entry["exit_status"]) is not int or entry["exit_status"] != 0:
            raise EvidenceError(f"v2 shard {name} did not complete successfully")
        database = entry["database"]
        if not isinstance(database, dict) or set(database) != {"url", "system_identifier"} or database["url"] != common.EXPECTED_DATABASE:
            raise EvidenceError(f"v2 database identity is invalid for {name}")
        identifier = database["system_identifier"]
        if not isinstance(identifier, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,80}", identifier):
            raise EvidenceError(f"v2 database system identifier is invalid for {name}")
        shard_class = part_classes[name]
        previous_class = seen_databases.get(identifier)
        if previous_class is not None and (previous_class != "identity" or shard_class != "identity"):
            raise EvidenceError("database system identifier was reused across incompatible shards")
        seen_databases[identifier] = shard_class
        cleanup = entry["cleanup"]
        if not isinstance(cleanup, dict) or set(cleanup) != {"containers_removed", "volume_removed", "netns_removed"} or any(cleanup[key] is not True for key in cleanup):
            raise EvidenceError(f"v2 cleanup proof is incomplete for {name}")
        log = manifest_path.parent / entry["log"]
        if log.is_symlink() or not log.is_file() or not isinstance(entry["log_sha256"], str) or digest_file(log) != entry["log_sha256"]:
            raise EvidenceError(f"v2 log digest is invalid for {name}")
        expected = allocation[name]
        passes, skips, package_terminals = parse_events(log, part, expected, manifest)
        intervals[name] = raw_log_interval(log)
        for package in part["packages"]:
            terminals = package_terminals.get(package, [])
            if len(terminals) != 1:
                raise EvidenceError(f"v2 package lacks exactly one terminal compile result: {package}")
            if terminals[0] != "pass" and any(key[0] == package for key in expected):
                raise EvidenceError(f"v2 package with discovered tests did not finish with PASS: {package}")
        for key in expected:
            pass_count = passes.get(key, 0)
            skip_count = skips.get(key, 0)
            if pass_count + skip_count != 1:
                raise EvidenceError(f"discovered test lacks exactly one terminal result: {key[0]}:{key[1]}")
            if skip_count and key not in baseline_set:
                raise EvidenceError(f"unreviewed SKIP evidence: {key[0]}:{key[1]}")
        all_skips.extend(key for key, count in skips.items() for _ in range(count))
    if seen_shards != set(parts):
        raise EvidenceError("v2 shard set is incomplete")
    if len(seen_databases) != 14:
        raise EvidenceError("v2 requires 14 distinct clusters, with only identity parts sharing")
    validate_schedule(manifest["workers"], intervals)
    if sorted(all_skips) != sorted(baseline_set):
        raise EvidenceError("v2 skip events differ from the reviewed repository baseline")
    skipped_required = required_set & set(all_skips)
    if skipped_required:
        key = sorted(skipped_required)[0]
        raise EvidenceError(f"a required test has SKIP evidence: {key[0]}:{key[1]}")
    allocated = set().union(*allocation.values())
    missing = required_set - allocated
    if missing:
        key = sorted(missing)[0]
        raise EvidenceError(f"required test is absent from source allocation: {key[0]}:{key[1]}")
    return len(required_set)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--spec", action="append", required=True)
    args = parser.parse_args()
    try:
        count = verify(pathlib.Path(args.manifest).resolve(), args.spec)
        print(json.dumps({"status": "PASS", "reused": count, "shards": 14}, separators=(",", ":")))
        return 0
    except (ValueError, OSError, KeyError, TypeError, AttributeError, IndexError, StopIteration, UnicodeError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

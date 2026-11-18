#!/usr/bin/env python3
"""Verify exact top-level Go test PASS evidence from a fresh 029 manifest."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import time

EXPECTED_DATABASE = "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable"
RUN_ID_ENV = "PHASE1_ACCEPTANCE_RUN_ID"
ROOT = pathlib.Path(__file__).resolve().parent.parent


class EvidenceError(ValueError):
    pass


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise EvidenceError("evidence contains a duplicate JSON key")
        value[key] = item
    return value


def strict_json(raw: str):
    try:
        return json.loads(raw, object_pairs_hook=unique_object)
    except json.JSONDecodeError as error:
        raise EvidenceError("evidence log contains invalid JSON") from error


def digest(path: pathlib.Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def current_commit() -> str:
    return subprocess.run(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True, capture_output=True, check=True).stdout.strip()


def source_status() -> str:
    return subprocess.run(["git", "-C", str(ROOT), "status", "--porcelain"], text=True, capture_output=True, check=True).stdout


def require_clean_source() -> None:
    if source_status():
        raise EvidenceError("evidence reuse requires a clean worktree")


def timestamp(value: str) -> dt.datetime:
    if not isinstance(value, str):
        raise EvidenceError("evidence timestamp is invalid")
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise EvidenceError("evidence timestamp is invalid") from error
    if parsed.tzinfo is None:
        raise EvidenceError("evidence timestamp must include a timezone")
    return parsed


def load_manifest(path: pathlib.Path) -> tuple[dict, pathlib.Path]:
    if path.is_symlink() or not path.is_file():
        raise EvidenceError("evidence manifest is not a regular file")
    try:
        value = strict_json(path.read_text(encoding="utf-8"))
    except OSError as error:
        raise EvidenceError("evidence manifest is invalid") from error
    keys = {"schema_version", "suite", "run_id", "created_at", "commit", "database", "log", "log_sha256"}
    if not isinstance(value, dict) or set(value) != keys or type(value["schema_version"]) is not int or value["schema_version"] != 1 or value["suite"] != "go-race" or value["log"] != "go-race.jsonl":
        raise EvidenceError("evidence manifest schema is invalid")
    if not isinstance(value["run_id"], str) or not re.fullmatch(r"[0-9a-f]{32}", value["run_id"]) or os.environ.get(RUN_ID_ENV) != value["run_id"]:
        raise EvidenceError("evidence run identity does not match this acceptance invocation")
    age = time.time() - timestamp(value["created_at"]).timestamp()
    if age < -300 or age > 3600:
        raise EvidenceError("evidence is stale or has an invalid creation time")
    if not isinstance(value["commit"], str) or not re.fullmatch(r"[0-9a-f]{40}", value["commit"]):
        raise EvidenceError("evidence commit is invalid")
    if value["commit"] != current_commit():
        raise EvidenceError("evidence commit does not match the current HEAD")
    if value["database"] != EXPECTED_DATABASE:
        raise EvidenceError("evidence database is not the dedicated current-MVP database")
    if not isinstance(value["log_sha256"], str) or not re.fullmatch(r"[0-9a-f]{64}", value["log_sha256"]):
        raise EvidenceError("evidence log digest is invalid")
    log = path.parent / value["log"]
    if log.is_symlink() or not log.is_file():
        raise EvidenceError("evidence log is not a regular file")
    if digest(log) != value["log_sha256"]:
        raise EvidenceError("evidence log digest does not match the manifest")
    return value, log


def parse_spec(value: str) -> tuple[str, str]:
    package, separator, test = value.partition(":")
    if not separator or not package.startswith("./") or not re.fullmatch(r"[A-Za-z0-9_]+", test):
        raise EvidenceError(f"invalid test specification: {value}")
    return package[2:], test


def verify(manifest_path: pathlib.Path, specs: list[str]) -> int:
    require_clean_source()
    _, log = load_manifest(manifest_path)
    required = [parse_spec(spec) for spec in specs]
    if not required or len(set(required)) != len(required):
        raise EvidenceError("evidence requires a nonempty unique test inventory")
    required_set = set(required)
    passes: dict[tuple[str, str], int] = {}
    skips: set[tuple[str, str]] = set()
    try:
        source = log.open(encoding="utf-8")
    except OSError as error:
        raise EvidenceError("cannot read evidence log") from error
    with source:
        for raw in source:
            line = raw.strip()
            if not line or line.startswith("command: "):
                continue
            if line.startswith("started_at: ") or line.startswith("completed_at: "):
                timestamp(line.split(": ", 1)[1])
                continue
            if line.startswith("exit_status: "):
                if line != "exit_status: 0":
                    raise EvidenceError("evidence command did not exit successfully")
                continue
            event = strict_json(line)
            if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
                raise EvidenceError("evidence log event is invalid")
            action = event["Action"]
            if action == "fail":
                raise EvidenceError("evidence log contains a failed package or test")
            package, test = event.get("Package"), event.get("Test")
            if not isinstance(package, str) or not isinstance(test, str):
                continue
            package = package.removeprefix("github.com/moni-ai/moni-aigate/")
            key = (package, test)
            if action == "pass" and key in required_set:
                passes[key] = passes.get(key, 0) + 1
            elif action == "skip" and key in required_set:
                skips.add(key)
    missing = sorted(required_set - passes.keys())
    if missing:
        raise EvidenceError(f"required tests lack PASS evidence: {missing[0][0]}:{missing[0][1]}")
    repeated = sorted(key for key, count in passes.items() if count != 1)
    if repeated:
        raise EvidenceError(f"required test has duplicate PASS evidence: {repeated[0][0]}:{repeated[0][1]}")
    if skips:
        key = sorted(skips)[0]
        raise EvidenceError(f"required test has SKIP evidence: {key[0]}:{key[1]}")
    return len(required)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--spec", action="append", required=True)
    args = parser.parse_args()
    try:
        manifest_path = pathlib.Path(args.manifest).resolve()
        count = verify(manifest_path, args.spec)
        value, _ = load_manifest(manifest_path)
        print(json.dumps({"status": "PASS", "reused": count, "log_sha256": value["log_sha256"]}, separators=(",", ":")))
        return 0
    except (EvidenceError, OSError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

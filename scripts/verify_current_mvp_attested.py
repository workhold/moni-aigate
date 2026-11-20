#!/usr/bin/env python3
"""Strictly validate all current-MVP and matrix specs with one source discovery."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
V2_SPEC = importlib.util.spec_from_file_location("check_acceptance_evidence_v2", ROOT / "scripts" / "check-acceptance-evidence-v2.py")
v2 = importlib.util.module_from_spec(V2_SPEC)
V2_SPEC.loader.exec_module(v2)


class AttestationError(ValueError):
    pass


def _git(*args: str) -> str:
    return subprocess.run(["git", "-C", str(ROOT), *args], text=True, capture_output=True, check=True).stdout.strip()


def validate(report_path: pathlib.Path, manifest_path: pathlib.Path) -> dict:
    if report_path.is_symlink() or manifest_path.is_symlink() or not report_path.is_file() or not manifest_path.is_file():
        raise AttestationError("runner report and manifest must be regular non-symlink files")
    report = v2.common.strict_json(report_path.read_text(encoding="utf-8"))
    manifest = v2.common.strict_json(manifest_path.read_text(encoding="utf-8"))
    if report.get("schema_version") != 1 or report.get("suite") != "040-pr-d-parallel-runner":
        raise AttestationError("invalid runner report schema")
    if manifest.get("schema_version") != 2 or manifest.get("suite") != "go-race-parallel":
        raise AttestationError("invalid v2 manifest schema")
    if report.get("status") != "PASS" or report.get("reusable") is not True or report.get("workers_2_proven") is not True:
        raise AttestationError("runner did not complete a reusable workers=2 proof")
    if report.get("workers") != 2 or manifest.get("workers") != 2:
        raise AttestationError("attested reuse requires workers=2")
    run_id = report.get("run_id")
    if not isinstance(run_id, str) or not re.fullmatch(r"[0-9a-f]{32}", run_id) or manifest.get("run_id") != run_id or os.environ.get("PHASE1_ACCEPTANCE_RUN_ID") != run_id:
        raise AttestationError("attestation run identity does not match this invocation")
    commit = report.get("commit")
    tree = report.get("tree")
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit) or manifest.get("commit") != commit or _git("rev-parse", "HEAD") != commit:
        raise AttestationError("attestation commit does not match HEAD")
    if not isinstance(tree, str) or not re.fullmatch(r"[0-9a-f]{40}", tree) or manifest.get("tree") != tree or _git("rev-parse", "HEAD^{tree}") != tree:
        raise AttestationError("attestation tree does not match HEAD")
    if manifest.get("source_clean") is not True or report.get("manifest") != manifest_path.name:
        raise AttestationError("manifest identity is not bound to the runner report")
    if report.get("manifest_sha256") != hashlib.sha256(manifest_path.read_bytes()).hexdigest():
        raise AttestationError("manifest digest differs from the runner report")
    if report.get("errors") != [] or report.get("host_listeners", {}).get("zero_new") is not True:
        raise AttestationError("runner errors or host listener changes are present")
    proof = report.get("dual_cluster_proof")
    if not isinstance(proof, dict) or proof.get("status") != "PASS" or proof.get("simultaneous_running") is not True or proof.get("default_acl_independent") is not True or proof.get("default_acl_restored") is not True or proof.get("advisory_lock_independent") is not True or proof.get("probe_objects_removed") is not True or proof.get("advisory_locks_remaining") != 0:
        raise AttestationError("dual-cluster proof is incomplete")
    if len(report.get("shards", [])) != 14 or len(manifest.get("shards", [])) != 17:
        raise AttestationError("attestation must contain all 14 shards and 17 parts")
    specs = v2.required_specs(ROOT)
    count = v2.verify(manifest_path, specs)
    if count != len(specs):
        raise AttestationError("strict v2 verification did not cover the complete required inventory")
    return {
        "status": "PASS", "reused": "strict-v2-batch", "run_id": run_id, "commit": commit,
        "verified_specs": count, "current_mvp_reused": 100, "matrix_reused": 77,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--output")
    args = parser.parse_args()
    try:
        result = validate(pathlib.Path(args.report).expanduser().absolute(), pathlib.Path(args.manifest).expanduser().absolute())
        encoded = json.dumps(result, separators=(",", ":")) + "\n"
        if args.output:
            output = pathlib.Path(args.output).expanduser().absolute()
            if output.is_symlink() or output.exists() or not output.parent.is_dir():
                raise AttestationError("attestation output must be a new regular file in an existing directory")
            temporary = None
            try:
                with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=output.parent, prefix=output.name + ".", delete=False) as stream:
                    temporary = pathlib.Path(stream.name)
                    stream.write(encoded)
                os.replace(temporary, output)
            finally:
                if temporary is not None:
                    temporary.unlink(missing_ok=True)
        print(encoded, end="")
        return 0
    except (AttestationError, v2.EvidenceError, OSError, ValueError, TypeError, KeyError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Execute and attest the frozen 029 phase-one acceptance matrix.

The runner never treats a zero exit status alone as coverage. Required Go tests
are matched against `go test -json` events, and browser evidence must contain
both desktop and 390px records on the same clean commit. All mandatory suites
are rerun for every final attempt; there is no selective retry after a failure.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid
from collections import Counter

EXPECTED_DATABASE = "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable"
EXPECTED_IDS = {
    "P1-ID-01", "P1-ID-02", "P1-ID-03", "P1-ID-04",
    "P1-KEY-01", "P1-KEY-02", "P1-KEY-03", "P1-KEY-04",
    "P1-CAT-01", "P1-CAT-02", "P1-CH-01", "P1-CH-02", "P1-CH-03",
    "P1-RT-01", "P1-RT-02", "P1-RT-03", "P1-RT-04",
    "P1-PR-01", "P1-PR-02", "P1-FN-01", "P1-FN-02", "P1-FN-03",
    "P1-FN-04", "P1-FN-05", "P1-PC-01", "P1-PC-02",
    "P1-PT-01", "P1-PT-02", "P1-PT-03", "P1-PT-04", "P1-PT-05", "P1-PT-06",
    "P1-MD-01", "P1-MD-02", "P1-MD-03",
    "P1-UI-01", "P1-UI-02", "P1-RP-01", "P1-OP-01", "P1-DP-01", "P1-DP-02",
}
ROOT = pathlib.Path(__file__).resolve().parent.parent
MATRIX_PATH = ROOT / "scripts" / "phase1-acceptance-matrix.json"
V2_SPEC = importlib.util.spec_from_file_location("acceptance_evidence_v2", ROOT / "scripts" / "check-acceptance-evidence-v2.py")
EVIDENCE_V2 = importlib.util.module_from_spec(V2_SPEC)
V2_SPEC.loader.exec_module(EVIDENCE_V2)


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def load_matrix() -> list[dict]:
    value = json.loads(MATRIX_PATH.read_text(encoding="utf-8"))
    if value.get("schema_version") != 1 or not isinstance(value.get("items"), list):
        raise ValueError("unsupported acceptance matrix")
    items = value["items"]
    ids = [item.get("id") for item in items]
    if len(ids) != 41 or set(ids) != EXPECTED_IDS or len(set(ids)) != 41:
        raise ValueError("acceptance matrix must contain exactly the frozen 41 IDs")
    for item in items:
        if not item.get("title") or not isinstance(item.get("tests"), list):
            raise ValueError(f"invalid matrix item: {item.get('id')}")
        for name in item.get("suites", []):
            if name not in {"mock-deployment", "identity-lifecycle", "mail-lifecycle", "payment-process"}:
                raise ValueError(f"unknown suite {name} in {item['id']}")
    return items


def git(*args: str) -> str:
    return subprocess.run(["git", *args], cwd=ROOT, check=True, text=True, capture_output=True).stdout.strip()


def clean_commit() -> str:
    commit = git("rev-parse", "HEAD")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("invalid source commit")
    if git("status", "--porcelain"):
        raise ValueError("final acceptance requires a clean worktree, including test sources")
    return commit


def test_inventory(items: list[dict]) -> None:
    packages = sorted({test.split(":", 1)[0] for item in items for test in item["tests"]})
    available: dict[str, set[str]] = {}
    for package in packages:
        result = subprocess.run(["go", "test", "-list", ".", "./" + package], cwd=ROOT, text=True, capture_output=True)
        if result.returncode:
            raise ValueError(f"cannot inventory {package}: {result.stderr[-1000:]}")
        available[package] = {line.strip() for line in result.stdout.splitlines() if line.startswith("Test")}
    missing = []
    for item in items:
        for test in item["tests"]:
            package, name = test.split(":", 1)
            if name not in available[package]:
                missing.append(f"{item['id']}: {test}")
    if missing:
        raise ValueError("required acceptance tests are absent:\n" + "\n".join(missing))
    print(f"matrix OK: 41 IDs, {sum(len(x['tests']) for x in items)} required Go test bindings, {len(packages)} packages")


def run_logged(command: list[str], log: pathlib.Path, env: dict[str, str]) -> dict:
    started = time.monotonic()
    printable = " ".join(command)
    print(f"START {log.stem}: {printable}", flush=True)
    with log.open("w", encoding="utf-8") as output:
        output.write(f"command: {printable}\nstarted_at: {now()}\n\n")
        output.flush()
        result = subprocess.run(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT)
        output.write(f"\ncompleted_at: {now()}\nexit_status: {result.returncode}\n")
    status = "PASS" if result.returncode == 0 else "FAIL"
    print(f"END {log.stem}: {status} ({time.monotonic() - started:.1f}s), log={log}", flush=True)
    return {"command": printable, "status": status, "exit_status": result.returncode, "log": str(log), "seconds": round(time.monotonic() - started, 3)}


def go_evidence_manifest(artifact: pathlib.Path, commit: str, run_id: str) -> pathlib.Path:
    log = artifact / "go-race.jsonl"
    value = {"schema_version": 1, "suite": "go-race", "run_id": run_id, "created_at": now(), "commit": commit, "database": EXPECTED_DATABASE, "log": log.name, "log_sha256": hashlib.sha256(log.read_bytes()).hexdigest()}
    manifest = artifact / "go-race-evidence.json"
    manifest.write_text(json.dumps(value, separators=(",", ":")) + "\n", encoding="utf-8")
    return manifest


def publish_artifacts(staging: pathlib.Path, final: pathlib.Path, suites: list[dict]) -> None:
    if final.exists():
        raise ValueError(f"artifact directory already exists: {final}")
    final.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(staging, final)
    for published_log in final.rglob("*.log"):
        published_log.write_text(published_log.read_text(encoding="utf-8").replace(str(staging), str(final)), encoding="utf-8")
    for suite in suites:
        suite["logs"] = []
        for command in suite["commands"]:
            published = final / pathlib.Path(command["log"]).relative_to(staging)
            command["log"] = str(published.relative_to(ROOT)) if published.is_relative_to(ROOT) else str(published)
            suite["logs"].append(command["log"])


def matrix_go_specs(items: list[dict]) -> list[str]:
    return sorted({"./" + test for item in items for test in item["tests"]})


def matrix_go_results(items: list[dict], status: str) -> dict[str, dict[str, str]]:
    return {item["id"]: {test: status for test in item["tests"]} for item in items}


def attested_matrix_results(path: pathlib.Path, items: list[dict], run_id: str, commit: str) -> tuple[dict[str, dict[str, str]], bool]:
    try:
        if path.is_symlink() or not path.is_file():
            raise ValueError("missing strict attestation output")
        value = EVIDENCE_V2.common.strict_json(path.read_text(encoding="utf-8"))
        expected_keys = {"status", "reused", "run_id", "commit", "verified_specs", "current_mvp_reused", "matrix_reused"}
        valid = isinstance(value, dict) and set(value) == expected_keys
        valid = valid and value["status"] == "PASS" and value["reused"] == "strict-v2-batch"
        valid = valid and value["run_id"] == run_id and value["commit"] == commit
        valid = valid and type(value["verified_specs"]) is int and value["verified_specs"] == len(EVIDENCE_V2.required_specs(ROOT))
        valid = valid and value["current_mvp_reused"] == 100 and value["matrix_reused"] == 77
    except (OSError, ValueError, TypeError, KeyError):
        valid = False
    return matrix_go_results(items, "PASS" if valid else "MISSING"), valid


def update_attested_go_suite(suites: list[dict], suite_index: int, commands: list[dict], current_mvp: dict, valid: bool) -> None:
    record = {
        "command": "./scripts/verify-current-mvp.sh # strict batched v2 attestation at startup",
        "status": "PASS" if valid else "FAIL", "exit_status": 0 if valid else 1,
        "log": current_mvp["log"], "seconds": current_mvp["seconds"],
    }
    commands.append(record)
    suites[suite_index]["logs"].append(record["log"])
    suites[suite_index]["status"] = "PASS" if valid and commands and all(command["status"] == "PASS" for command in commands) else "FAIL"


def required_go_results_v2(manifest: pathlib.Path, items: list[dict]) -> dict[str, dict[str, str]]:
    EVIDENCE_V2.verify(manifest, matrix_go_specs(items))
    return matrix_go_results(items, "PASS")


def v2_merge_command(manifest: pathlib.Path, items: list[dict]) -> list[str]:
    command = ["python3", "scripts/check-acceptance-evidence-v2.py", "--manifest", str(manifest)]
    for spec in matrix_go_specs(items):
        command += ["--spec", spec]
    return command


def stage_v2_evidence(source_manifest: pathlib.Path, destination: pathlib.Path) -> pathlib.Path:
    source_manifest = source_manifest.expanduser().absolute()
    if source_manifest.is_symlink() or not source_manifest.is_file():
        raise ValueError("v2 manifest must be a regular non-symlink file")
    source_directory = source_manifest.parent.resolve()
    source_manifest = source_directory / source_manifest.name
    if source_directory.is_relative_to(ROOT) or source_directory == ROOT or source_directory in ROOT.parents:
        raise ValueError("v2 evidence must come from its own artifact directory outside the source repository")
    if destination.exists():
        raise ValueError("v2 staging directory already exists")
    destination.mkdir(parents=True)
    for source in source_directory.iterdir():
        if source.is_symlink() or not source.is_file():
            raise ValueError(f"v2 artifact contains a non-regular or symlink entry: {source.name}")
        target = destination / source.name
        shutil.copyfile(source, target)
        if hashlib.sha256(source.read_bytes()).digest() != hashlib.sha256(target.read_bytes()).digest():
            raise ValueError(f"v2 artifact digest changed while staging: {source.name}")
    return destination / source_manifest.name


def validate_parallel_report(path: pathlib.Path, manifest: pathlib.Path, run_id: str, commit: str) -> dict:
    report = json.loads(path.read_text(encoding="utf-8"))
    value = json.loads(manifest.read_text(encoding="utf-8"))
    if report.get("schema_version") != 1 or report.get("suite") != "040-pr-d-parallel-runner":
        raise ValueError("parallel runner report schema is invalid")
    if report.get("run_id") != run_id or value.get("run_id") != run_id or report.get("commit") != commit or value.get("commit") != commit:
        raise ValueError("parallel manifest/report are not bound to this attempt and commit")
    if report.get("workers") != 2 or value.get("workers") != 2 or report.get("status") != "PASS" or report.get("reusable") is not True:
        raise ValueError("workers=2 runner did not complete successfully")
    if report.get("manifest") != manifest.name or report.get("manifest_sha256") != hashlib.sha256(manifest.read_bytes()).hexdigest():
        raise ValueError("parallel runner report is not bound to the final manifest digest")
    if report.get("complete_029") is not False or report.get("workers_2_proven") is not True or report.get("fail_fast") is not False:
        raise ValueError("parallel runner attempt boundaries are invalid")
    proof = report.get("dual_cluster_proof")
    if not isinstance(proof, dict) or proof.get("status") != "PASS" or proof.get("simultaneous_running") is not True or proof.get("default_acl_independent") is not True or proof.get("default_acl_restored") is not True or proof.get("advisory_lock_independent") is not True or proof.get("probe_objects_removed") is not True or proof.get("advisory_locks_remaining") != 0:
        raise ValueError("dual-cluster isolation proof is incomplete")
    if json.loads((path.parent / "dual-cluster-proof.json").read_text(encoding="utf-8")) != proof:
        raise ValueError("dual-cluster proof artifact differs from the runner report")
    if report.get("host_listeners", {}).get("zero_new") is not True:
        raise ValueError("parallel runner did not prove zero new host listeners")
    shards = report.get("shards")
    if not isinstance(shards, list) or len(shards) != 14 or report.get("errors") != []:
        raise ValueError("parallel runner report does not contain 14 clean shard results")
    sample_count = 0
    part_count = 0
    attempt_started = dt.datetime.fromisoformat(report["started_at"])
    attempt_completed = dt.datetime.fromisoformat(report["completed_at"])
    if attempt_started.tzinfo is None or attempt_completed.tzinfo is None or attempt_started > attempt_completed:
        raise ValueError("parallel runner attempt timestamps are invalid")
    sample_fields = ("Name", "CPUPerc", "MemUsage", "MemPerc", "NetIO", "BlockIO", "PIDs")
    for shard in shards:
        if shard.get("errors") != [] or not all(shard.get("cleanup", {}).get(key) is True for key in ("containers_removed", "volume_removed", "netns_removed")):
            raise ValueError(f"shard cleanup/errors are not clean: {shard.get('name')}")
        terminal = shard.get("postgres_terminal", {})
        if terminal.get("running_before_cleanup") is not True or terminal.get("oom_killed") is not False:
            raise ValueError(f"PostgreSQL terminal state is not healthy: {shard.get('name')}")
        if not isinstance(shard.get("wall_seconds"), (int, float)) or shard["wall_seconds"] < 0:
            raise ValueError("shard wall time is missing")
        for part in shard.get("parts", []):
            part_count += 1
            if part.get("errors") != [] or part.get("runner_removed") is not True or part.get("oom_killed") is not False:
                raise ValueError(f"Go runner terminal state is not clean: {part.get('name')}")
            for sample in part.get("resource_samples", []):
                sampled_at = dt.datetime.fromisoformat(sample.get("at", ""))
                if sampled_at.tzinfo is None or sampled_at < attempt_started or sampled_at > attempt_completed:
                    raise ValueError("resource sample is outside this attempt")
                containers = sample.get("containers")
                expected_names = {f"moni-040-{run_id}-{shard['name']}-pg", f"moni-040-{run_id}-{part['name']}"}
                if not isinstance(containers, list) or len(containers) != 2:
                    raise ValueError("resource sample must contain exactly two containers")
                if any(not isinstance(record, dict) or any(not isinstance(record.get(key), str) or not record.get(key) for key in sample_fields) for record in containers):
                    raise ValueError("resource sample contains an incomplete container record")
                if {record["Name"] for record in containers} != expected_names:
                    raise ValueError("resource sample contains a foreign or missing container")
                sample_count += 1
    if part_count != 17:
        raise ValueError("parallel runner report must contain all 17 part results")
    if sample_count == 0:
        raise ValueError("parallel runner has no timestamped resource curve")
    timings = report.get("timings", {})
    if any(not isinstance(timings.get(key), (int, float)) or timings[key] < 0 for key in ("wall_seconds", "parallel_shards_seconds", "merge_seconds")):
        raise ValueError("parallel runner timing evidence is incomplete")
    return {
        "run_id": run_id, "workers": 2, "runner_report": "parallel-evidence/runner-report.json",
        "runner_report_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
        "parallel_shards_seconds": timings["parallel_shards_seconds"], "merge_seconds": timings["merge_seconds"],
        "runner_wall_seconds": timings["wall_seconds"],
        "resource_samples": sample_count,
    }


def required_go_results(log: pathlib.Path, items: list[dict]) -> dict[str, dict[str, str]]:
    wanted = {(test.split(":", 1)[0], test.split(":", 1)[1]) for item in items for test in item["tests"]}
    found: dict[tuple[str, str], str] = {}
    with log.open(encoding="utf-8") as source:
        for line in source:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            name = event.get("Test")
            package = event.get("Package", "")
            prefix = "github.com/moni-ai/moni-aigate/"
            if not name or not package.startswith(prefix):
                continue
            key = (package[len(prefix):], name)
            if key in wanted and event.get("Action") in {"pass", "fail", "skip"}:
                found[key] = event["Action"].upper()
    results: dict[str, dict[str, str]] = {}
    for item in items:
        results[item["id"]] = {}
        for test in item["tests"]:
            package, name = test.split(":", 1)
            results[item["id"]][test] = found.get((package, name), "MISSING")
    return results


def json_test_action(log: pathlib.Path, package: str, name: str) -> str:
    actions = []
    with log.open(encoding="utf-8") as source:
        for line in source:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if event.get("Test") == name and event.get("Package", "").endswith("/" + package) and event.get("Action") in {"pass", "fail", "skip"}:
                actions.append(event["Action"])
    counts = Counter(actions)
    if counts["pass"] == 1 and not counts["fail"] and not counts["skip"]:
        return "PASS"
    if counts["skip"]:
        return "SKIP"
    if counts["fail"]:
        return "FAIL"
    return "MISSING"


def load_browser(path: pathlib.Path, commit: str) -> dict:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("schema_version") != 1 or value.get("commit") != commit or value.get("isolated") is not True:
        raise ValueError("browser evidence must be isolated and bound to the final commit")
    if value.get("status") != "PASS" or value.get("completed") is not True:
        raise ValueError("browser evidence must come from a fully completed final run")
    runtime = value.get("runtime")
    if not isinstance(runtime, dict) or runtime.get("source_commit") != commit or runtime.get("source_clean") is not True:
        raise ValueError("browser evidence lacks clean runtime source binding")
    if not re.fullmatch(r"[0-9a-f]{40}", str(runtime.get("source_tree", ""))) or not re.fullmatch(r"sha256:[0-9a-f]{64}", str(runtime.get("image_id", ""))):
        raise ValueError("browser evidence has invalid runtime source or image identity")
    records = value.get("items")
    if not isinstance(records, dict):
        raise ValueError("browser evidence items must be an object")
    for item_id, record in records.items():
        if item_id not in EXPECTED_IDS or not isinstance(record, dict):
            raise ValueError(f"invalid browser item {item_id}")
        for viewport in ("desktop", "mobile390"):
            entry = record.get(viewport)
            if not isinstance(entry, dict) or entry.get("status") != "PASS" or not entry.get("checks"):
                raise ValueError(f"browser evidence lacks passing {viewport}: {item_id}")
            if viewport == "mobile390" and entry.get("width") != 390:
                raise ValueError(f"mobile viewport is not 390px: {item_id}")
    return value


def verify_browser_runtime(browser: dict, commit: str) -> None:
    runtime = browser["runtime"]
    if runtime["source_tree"] != git("rev-parse", "HEAD^{tree}"):
        raise ValueError("browser runtime source tree differs from the final checkout")
    image_ids = []
    for name in (os.environ["PHASE1_APP_CONTAINER"], os.environ["PHASE1_WORKER_CONTAINER"]):
        result = subprocess.run(["docker", "inspect", "--format", "{{.Image}}", name], text=True, capture_output=True)
        if result.returncode:
            raise ValueError("cannot inspect the browser acceptance runtime")
        image_ids.append(result.stdout.strip())
    if image_ids != [runtime["image_id"], runtime["image_id"]]:
        raise ValueError("running app/worker differ from the completed browser evidence")
    result = subprocess.run(["docker", "image", "inspect", "--format", "{{json .Config.Labels}}", runtime["image_id"]], text=True, capture_output=True)
    if result.returncode:
        raise ValueError("cannot inspect the browser acceptance image")
    labels = json.loads(result.stdout.strip() or "{}")
    expected = {
        "ai.moni.phase1.source-commit": commit,
        "ai.moni.phase1.source-tree": runtime["source_tree"],
        "ai.moni.phase1.source-clean": "true",
    }
    if any(labels.get(key) != value for key, value in expected.items()):
        raise ValueError("browser runtime image labels do not match the final clean source")


def recheck_browser_runtime(browser: dict, commit: str, log: pathlib.Path) -> bool:
    try:
        verify_browser_runtime(browser, commit)
    except Exception as error:
        with log.open("a", encoding="utf-8") as output:
            output.write(f"runtime_recheck: FAIL: {error}\n")
        return False
    with log.open("a", encoding="utf-8") as output:
        output.write("runtime_recheck: PASS\n")
    return True


def write_evidence(output: pathlib.Path, items: list[dict], summary: dict, go_results: dict[str, dict[str, str]], browser: dict | None) -> None:
    output.mkdir(parents=True, exist_ok=True)
    suite_by_name = {suite["name"]: suite for suite in summary["suites"]}
    for item in items:
        tests = go_results.get(item["id"], {})
        counts = Counter(tests.values())
        suite_names = ["static-build", "frontend", "go-race", "current-mvp", *item.get("suites", [])]
        blocking = any(suite_by_name[name]["status"] != "PASS" for name in suite_names)
        if item.get("browser"):
            browser_record = (browser or {}).get("items", {}).get(item["id"])
            blocking = blocking or browser_record is None
        passed = not blocking and all(value == "PASS" for value in tests.values())
        item["status"] = "PASS" if passed else "FAIL"
        item["evidence"] = f"{item['id']}.md"
        item["test_counts"] = {"pass": counts["PASS"], "fail": counts["FAIL"] + counts["MISSING"], "skip": counts["SKIP"]}
        item["suites"] = suite_names
        lines = [
            f"# {item['id']}：{item['title']}", "",
            f"- 结论：**{item['status']}**（本次同一提交终验，非历史证据）。",
            f"- 提交：`{summary['commit']}`。",
            f"- 数据库：`127.0.0.1:55439/moni_test`，专用 PostgreSQL；凭据不写入证据。",
            f"- deterministic mock：{'是；仅为本地内测证据，不是供应商或生产认证' if item.get('mock') else '未用于本项直接断言；全局部署 mock 门禁仍执行'}。",
            f"- 必需 Go 测试：PASS={counts['PASS']}，FAIL/MISSING={counts['FAIL'] + counts['MISSING']}，SKIP={counts['SKIP']}。", "",
            "## 本次执行套件与日志", "",
        ]
        for name in suite_names:
            suite = suite_by_name[name]
            lines.append(f"- `{name}`：**{suite['status']}**；命令见 `summary.json`，日志：{', '.join(suite['logs'])}。")
        lines.extend(["", "## 绑定的 Go 测试", ""])
        if tests:
            lines.extend(f"- `{test}`：{status}。" for test, status in tests.items())
        else:
            lines.append("- 本项由 Compose 实跑与备份恢复演练判定，无额外 Go 测试名绑定。")
        if item.get("browser"):
            lines.extend(["", "## 隔离浏览器", ""])
            record = (browser or {}).get("items", {}).get(item["id"])
            if record:
                for viewport in ("desktop", "mobile390"):
                    entry = record[viewport]
                    lines.append(f"- {viewport}（{entry.get('width', 'desktop')}px）：PASS；检查：{'; '.join(entry['checks'])}；证据：{entry.get('evidence', 'browser-evidence.json')}。")
            else:
                lines.append("- 缺少同一提交的桌面或 390px 记录，因此本项 FAIL。")
        lines.extend(["", "## 未覆盖范围", "", "- 本记录不证明真实供应商、真实支付商、生产凭据、生产 SLA、多实例 HA 或 032 第二阶段能力；这些均不在第一阶段本项验收范围。", "- 任何必需套件失败、测试缺失/SKIP 或浏览器视口缺失都会使本项 FAIL；修复后必须整单重跑，不能只复跑本项。", ""])
        (output / f"{item['id']}.md").write_text("\n".join(lines), encoding="utf-8")
    summary["counts"] = {"total": 41, "pass": sum(x["status"] == "PASS" for x in items), "fail": sum(x["status"] != "PASS" for x in items), "skip": 0}
    summary["overall_status"] = "PASS" if summary["counts"]["pass"] == 41 else "FAIL"
    summary["mvp_status"] = "第一阶段本地 MVP 验收通过；仍不是供应商或生产认证" if summary["overall_status"] == "PASS" else "内测；第一阶段尚未完成"
    summary["items"] = items
    summary["completed_at"] = now()
    (output / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def wait_for_instance_release() -> None:
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = subprocess.run(["psql", EXPECTED_DATABASE, "-Atc", "SELECT count(*) FROM pg_locks WHERE locktype='advisory'"], text=True, capture_output=True)
        if result.returncode == 0 and result.stdout.strip() == "0":
            return
        time.sleep(0.5)
    raise ValueError("the previous preview did not release the dedicated service lock")


def wait_for_ready() -> None:
    base = os.environ["PHASE1_BASE_URL"].rstrip("/")
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(base + "/health/ready", timeout=2) as response:
                if response.status == 200:
                    return
        except OSError:
            pass
        time.sleep(0.5)
    raise ValueError("the deployment did not become ready for mock acceptance")


def execute(items: list[dict], args: argparse.Namespace) -> int:
    started_at = now()
    started_monotonic = time.monotonic()
    commit = clean_commit()
    parallel_workers = os.environ.get("PHASE1_ACCEPTANCE_PARALLEL_WORKERS")
    v2_source = os.environ.get("PHASE1_ACCEPTANCE_GO_EVIDENCE_V2")
    if parallel_workers is not None:
        if parallel_workers != "2":
            raise ValueError("PHASE1_ACCEPTANCE_PARALLEL_WORKERS only permits the reviewed workers=2 candidate")
        if v2_source:
            raise ValueError("parallel runner and external v2 manifest options are mutually exclusive")
        if not args.artifacts or not args.output:
            raise ValueError("workers=2 requires new repository-external --artifacts and --output paths")
    if os.environ.get("TEST_DATABASE_URL") != EXPECTED_DATABASE:
        raise ValueError(f"TEST_DATABASE_URL must be exactly {EXPECTED_DATABASE}")
    browser_path = pathlib.Path(args.browser_evidence).resolve() if args.browser_evidence else None
    if any(item.get("browser") for item in items) and browser_path is None:
        raise ValueError("--browser-evidence is required for the final no-SKIP run")
    browser = load_browser(browser_path, commit) if browser_path else None
    if any("mock-deployment" in item.get("suites", []) for item in items):
        for name in ("PHASE1_BASE_URL", "PHASE1_APP_CONTAINER", "PHASE1_WORKER_CONTAINER", "PHASE1_ADMIN_PASSWORD"):
            if not os.environ.get(name):
                raise ValueError(f"{name} is required for deterministic deployment acceptance")
    if browser is not None:
        verify_browser_runtime(browser, commit)
    compose = subprocess.run(["docker", "compose", "version"], cwd=ROOT, capture_output=True)
    if compose.returncode:
        subprocess.run(["docker-compose", "version"], cwd=ROOT, check=True, capture_output=True)
    output = pathlib.Path(args.output).resolve() if args.output else ROOT / "docs" / "verification" / "phase1"
    published_artifact = pathlib.Path(args.artifacts).resolve() if args.artifacts else output / "evidence" / commit
    if published_artifact.exists():
        raise ValueError(f"artifact directory already exists: {published_artifact}")
    if parallel_workers is not None:
        if output.exists() or output.is_relative_to(ROOT) or published_artifact.is_relative_to(ROOT):
            raise ValueError("workers=2 summary/output and raw artifacts must be new paths outside the source repository")
    # Keep raw logs outside the source tree until every read-only architecture
    # check has finished; only their final published copies enter the repository.
    artifact = pathlib.Path(tempfile.mkdtemp(prefix="phase1-acceptance-"))
    env = os.environ.copy()
    env.setdefault("GOPROXY", "https://goproxy.cn,direct")
    env.setdefault("PHASE1_MOCK_CONFIG_TRANSPORT", "copy")
    env.update({"BUSINESS_QA_FAIL_ON_SKIP": "true", "DEPLOY_QA_BUSINESS": "true", "PROCUREMENT_ATTEMPT_RULES_DATABASE_URL": os.environ["TEST_DATABASE_URL"], "VERIFY_CURRENT_MVP_LOG_DIR": str(artifact / "current-mvp")})
    suites: list[dict] = []

    def add(name: str, commands: list[dict]) -> None:
        status = "PASS" if all(command["status"] == "PASS" for command in commands) else "FAIL"
        suites.append({"name": name, "status": status, "commands": commands, "logs": [command["log"] for command in commands]})

    preview_stop = run_logged(["docker", "stop", "-t", "30", os.environ["PHASE1_APP_CONTAINER"], os.environ["PHASE1_WORKER_CONTAINER"]], artifact / "preview-stop.log", env)
    if preview_stop["status"] != "PASS":
        raise ValueError("could not stop the existing preview before real-main acceptance")
    wait_for_instance_release()

    static = [
        run_logged(["go", "run", "./scripts/check-architecture"], artifact / "static-architecture.log", env),
        run_logged(["go", "run", "./scripts/check-procurement-layout"], artifact / "static-procurement.log", env),
        run_logged(["go", "run", "./scripts/check-upstream-provenance"], artifact / "static-upstream-provenance.log", env),
        run_logged(["go", "vet", "./..."], artifact / "static-vet.log", env),
        run_logged(["go", "build", "-trimpath", "-o", str(ROOT / "bin" / "moni-aigate"), "./cmd/moni-aigate"], artifact / "static-build.log", env),
    ]
    add("static-build", static)
    add("frontend", [run_logged(["npm", "--prefix", "web", "test"], artifact / "frontend-test.log", env), run_logged(["npm", "--prefix", "web", "run", "build"], artifact / "frontend-build.log", env)])
    parallel_details = None
    go_commands = []
    go_suite_index = None
    if parallel_workers is not None:
        go_mode = "parallel-v2"
        run_id = uuid.uuid4().hex
        run_directory = pathlib.Path(os.environ.get("PHASE1_ACCEPTANCE_PARALLEL_RUN_DIRECTORY", str(pathlib.Path.home() / ".cache" / "moni-aigate-acceptance" / run_id))).expanduser().absolute()
        parallel_run = run_logged([
            sys.executable, "-B", "-I", str(ROOT / "scripts" / "acceptance-parallel-coordinator.py"),
            "--workers", "2", "--all-shards", "--run-id", run_id, "--run-directory", str(run_directory), "--source", str(ROOT),
        ], artifact / "parallel-runner.log", env)
        go_commands.append(parallel_run)
        if parallel_run["status"] != "PASS":
            raise ValueError(f"workers=2 parallel runner failed; see {parallel_run['log']}")
        source_manifest = run_directory / "evidence" / "go-race-evidence-v2.json"
        parallel_artifact = artifact / "parallel-evidence"
        v2_manifest = stage_v2_evidence(source_manifest, parallel_artifact)
        parallel_details = validate_parallel_report(parallel_artifact / "runner-report.json", v2_manifest, run_id, commit)
        parallel_details["runner_wall_seconds"] = parallel_run["seconds"]
    elif v2_source:
        go_mode = "manifest-v2"
        source_manifest = pathlib.Path(v2_source).expanduser().absolute()
        parallel_artifact = artifact / "parallel-evidence"
        v2_manifest = stage_v2_evidence(source_manifest, parallel_artifact)
        try:
            run_id = json.loads(v2_manifest.read_text(encoding="utf-8")).get("run_id", "")
        except (OSError, ValueError, AttributeError):
            run_id = ""
        if not isinstance(run_id, str) or not re.fullmatch(r"[0-9a-f]{32}", run_id):
            raise ValueError("external v2 manifest has an invalid run_id")
        manifest_value = json.loads(v2_manifest.read_text(encoding="utf-8"))
        if manifest_value.get("workers") == 2:
            parallel_details = validate_parallel_report(parallel_artifact / "runner-report.json", v2_manifest, run_id, commit)
    else:
        go_mode = "serial-v1"
        env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE_V2", None)
        env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED", None)
        env.pop("VERIFY_CURRENT_MVP_RUNNER_REPORT", None)
        env.pop("VERIFY_CURRENT_MVP_ATTESTATION_OUTPUT", None)
        race_log = artifact / "go-race.jsonl"
        add("go-race", [run_logged(["go", "test", "-race", "-p", "1", "-count=1", "-timeout", "1200s", "-json", "./..."], race_log, env)])
        go_results = required_go_results(race_log, items)
        run_id = uuid.uuid4().hex
        env["PHASE1_ACCEPTANCE_RUN_ID"] = run_id
        env["VERIFY_CURRENT_MVP_GO_EVIDENCE"] = str(go_evidence_manifest(artifact, commit, run_id))
    if parallel_workers is not None or v2_source:
        env["PHASE1_ACCEPTANCE_RUN_ID"] = run_id
        if parallel_details is not None:
            env["VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED"] = "1"
            env["VERIFY_CURRENT_MVP_RUNNER_REPORT"] = str(v2_manifest.parent / "runner-report.json")
            env["VERIFY_CURRENT_MVP_ATTESTATION_OUTPUT"] = str(artifact / "current-mvp-attestation.json")
            go_suite_index = len(suites)
            add("go-race", go_commands)
            suites[go_suite_index]["status"] = "FAIL"
            go_results = matrix_go_results(items, "MISSING")
        else:
            env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED", None)
            env.pop("VERIFY_CURRENT_MVP_RUNNER_REPORT", None)
            env.pop("VERIFY_CURRENT_MVP_ATTESTATION_OUTPUT", None)
            merge = run_logged(v2_merge_command(v2_manifest, items), artifact / "go-race-v2-merge.log", env)
            go_commands.append(merge)
            add("go-race", go_commands)
            go_results = matrix_go_results(items, "PASS" if merge["status"] == "PASS" else "MISSING")
        env["VERIFY_CURRENT_MVP_GO_EVIDENCE_V2"] = str(v2_manifest)
        env.pop("VERIFY_CURRENT_MVP_GO_EVIDENCE", None)
    serial_tail_started = time.monotonic()
    attested_reuse = False
    current_mvp = run_logged(["./scripts/verify-current-mvp.sh"], artifact / "current-mvp.log", env)
    add("current-mvp", [preview_stop, current_mvp])
    if parallel_details is not None:
        go_results, attested_reuse = attested_matrix_results(artifact / "current-mvp-attestation.json", items, run_id, commit)
        update_attested_go_suite(suites, go_suite_index, go_commands, current_mvp, attested_reuse)

    for name, package, test, variable in (
        ("identity-lifecycle", "scripts/browser-qa", "TestBrowserQARealMainLifecycle", "BROWSER_QA_REAL_MAIN_TEST"),
        ("mail-lifecycle", "scripts/browser-qa", "TestMailLifecycleRealMainRecovery", "MAIL_LIFECYCLE_REAL_MAIN_TEST"),
    ):
        if not any(name in item.get("suites", []) for item in items):
            continue
        isolated = env.copy()
        isolated[variable] = "1"
        if name == "identity-lifecycle":
            isolated["BROWSER_QA_ERROR_TEST"] = "1"
        log = artifact / f"{name}.jsonl"
        add(name, [run_logged(["go", "test", "-race", "-p", "1", "-count=1", "-timeout", "1200s", "-json", "./" + package, "-run", "^" + test + "$"], log, isolated)])
        action = json_test_action(log, package, test)
        for item in items:
            if name in item.get("suites", []):
                go_results[item["id"]][package + ":" + test] = action

    if any("payment-process" in item.get("suites", []) for item in items):
        add("payment-process", [run_logged(["go", "run", "./scripts/payment-application-qa", "--require-database", "--process"], artifact / "payment-process.log", env)])
    if any("mock-deployment" in item.get("suites", []) for item in items):
        mock_log = artifact / "mock-deployment.log"
        mock_env = env.copy()
        mock_env.pop("PHASE1_UI_ACCOUNTS_FILE", None)
        if browser is not None:
            mock_env["PHASE1_MOCK_IMAGE"] = browser["runtime"]["image_id"]
        mock_commands = [run_logged(["docker", "start", os.environ["PHASE1_APP_CONTAINER"], os.environ["PHASE1_WORKER_CONTAINER"]], artifact / "preview-start.log", mock_env)]
        if mock_commands[0]["status"] == "PASS":
            try:
                wait_for_ready()
            except ValueError:
                mock_commands[0]["status"] = "FAIL"
        if mock_commands[0]["status"] == "PASS" and browser is not None and not recheck_browser_runtime(browser, commit, artifact / "preview-start.log"):
            mock_commands[0]["status"] = "FAIL"
        if mock_commands[0]["status"] == "PASS":
            mock_commands.append(run_logged(["./scripts/phase1-mock-acceptance.sh"], mock_log, mock_env))
        add("mock-deployment", mock_commands)
        text = mock_log.read_text(encoding="utf-8") if mock_log.exists() else ""
        if '"status":"passed"' not in text and '"status": "passed"' not in text:
            suites[-1]["status"] = "FAIL"

    serial_tail_seconds = round(time.monotonic() - serial_tail_started, 3)
    publish_artifacts(artifact, published_artifact, suites)
    shutil.rmtree(artifact)
    go_evidence = {"mode": go_mode, "run_id": run_id, "matrix_unique_tests": 77, "matrix_bindings": 82, "current_mvp_required_tests": 100}
    if parallel_details is not None:
        go_evidence["attested_reuse"] = attested_reuse
        go_evidence.update(parallel_details)
    summary = {
        "schema_version": 1, "spec": "029-phase1-acceptance", "run_id": run_id, "commit": commit,
        "database": "127.0.0.1:55439/moni_test", "started_at": started_at, "suites": suites,
        "timing": {
            "wall_seconds": round(time.monotonic() - started_monotonic, 3),
            "serial_tail_seconds": serial_tail_seconds,
            "parallel_runner_seconds": parallel_details.get("runner_wall_seconds") if parallel_details else None,
            "v2_merge_seconds": next((command["seconds"] for command in go_commands if command["log"].endswith("go-race-v2-merge.log")), None),
        },
        "go_evidence": go_evidence, "required_go_results": go_results,
        "browser_evidence": str(browser_path.relative_to(ROOT)) if browser_path and browser_path.is_relative_to(ROOT) else str(browser_path),
        "browser_runtime": (browser or {}).get("runtime", {}),
        "rerun_policy": "任何 FAIL、MISSING 或必需 SKIP 后必须修复并在同一干净提交整单重跑全部 41 项",
    }
    write_evidence(output, items, summary, go_results, browser)
    print(json.dumps({"overall_status": summary["overall_status"], "counts": summary["counts"], "summary": str(output / "summary.json")}, ensure_ascii=False))
    return 0 if summary["overall_status"] == "PASS" else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="validate 41 IDs and required Go test inventory without executing acceptance")
    parser.add_argument("--execute", action="store_true", help="execute the complete same-commit acceptance run")
    parser.add_argument("--browser-evidence", help="same-commit isolated desktop+390px browser JSON")
    parser.add_argument("--artifacts", help="new final directory for raw logs; staged outside the repository and published at completion")
    parser.add_argument("--output", help="new directory for summary.json and the 41 item records; required outside the repository for workers=2")
    args = parser.parse_args()
    if args.check == args.execute:
        parser.error("choose exactly one of --check or --execute")
    items = load_matrix()
    test_inventory(items)
    if args.check:
        return 0
    return execute(items, args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f"phase-one acceptance gate error: {error}", file=sys.stderr)
        raise SystemExit(2)

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
    commit = clean_commit()
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
    output = ROOT / "docs" / "verification" / "phase1"
    published_artifact = pathlib.Path(args.artifacts).resolve() if args.artifacts else output / "evidence" / commit
    if published_artifact.exists():
        raise ValueError(f"artifact directory already exists: {published_artifact}")
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
    race_log = artifact / "go-race.jsonl"
    add("go-race", [run_logged(["go", "test", "-race", "-p", "1", "-count=1", "-timeout", "1200s", "-json", "./..."], race_log, env)])
    go_results = required_go_results(race_log, items)
    add("current-mvp", [preview_stop, run_logged(["./scripts/verify-current-mvp.sh"], artifact / "current-mvp.log", env)])

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

    publish_artifacts(artifact, published_artifact, suites)
    shutil.rmtree(artifact)
    summary = {"schema_version": 1, "spec": "029-phase1-acceptance", "commit": commit, "database": "127.0.0.1:55439/moni_test", "started_at": started_at, "suites": suites, "required_go_results": go_results, "browser_evidence": str(browser_path.relative_to(ROOT)) if browser_path and browser_path.is_relative_to(ROOT) else str(browser_path), "browser_runtime": (browser or {}).get("runtime", {}), "rerun_policy": "任何 FAIL、MISSING 或必需 SKIP 后必须修复并在同一干净提交整单重跑全部 41 项"}
    write_evidence(output, items, summary, go_results, browser)
    print(json.dumps({"overall_status": summary["overall_status"], "counts": summary["counts"], "summary": str(output / "summary.json")}, ensure_ascii=False))
    return 0 if summary["overall_status"] == "PASS" else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="validate 41 IDs and required Go test inventory without executing acceptance")
    parser.add_argument("--execute", action="store_true", help="execute the complete same-commit acceptance run")
    parser.add_argument("--browser-evidence", help="same-commit isolated desktop+390px browser JSON")
    parser.add_argument("--artifacts", help="new final directory for raw logs; staged outside the repository and published at completion")
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

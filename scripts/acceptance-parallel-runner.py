#!/usr/bin/env python3
"""Run PR-A acceptance shards in disposable, network-isolated containers."""

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
import socket
import subprocess
import sys
import time
import uuid

sys.dont_write_bytecode = True
ROOT = pathlib.Path(__file__).resolve().parent.parent
V2_PATH = pathlib.Path(__file__).with_name("check-acceptance-evidence-v2.py")
SPEC = importlib.util.spec_from_file_location("acceptance_evidence_v2_for_runner", V2_PATH)
v2 = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v2)
DATABASE_URL = v2.common.EXPECTED_DATABASE
IMAGE_LOCK = pathlib.Path(__file__).with_name("acceptance-runner-images.json")
RUN_LABEL = "com.moni-aigate.acceptance.run"
SHARD_LABEL = "com.moni-aigate.acceptance.shard"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
RUN_ID = re.compile(r"[0-9a-f]{32}")


class RunnerError(ValueError):
    pass


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def digest_bytes(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def write_json(path: pathlib.Path, value: dict) -> None:
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def execute(args: list[str], cwd=None, env=None, check: bool = True) -> subprocess.CompletedProcess:
    result = subprocess.run(args, cwd=cwd, env=env, text=True, encoding="utf-8", errors="strict", capture_output=True)
    if check and result.returncode:
        detail = (result.stderr or result.stdout).strip()[-2000:]
        raise RunnerError(f"{args[0]} {args[1] if len(args) > 1 else ''} exited {result.returncode}: {detail}")
    return result


def docker(args: list[str], check: bool = True) -> subprocess.CompletedProcess:
    return execute(["docker", *args], check=check)


def load_image_lock(path: pathlib.Path = IMAGE_LOCK) -> dict[str, str]:
    value = v2.common.strict_json(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or set(value) != {"schema_version", "go_runner", "postgres"} or value["schema_version"] != 1:
        raise RunnerError("acceptance image lock schema is invalid")
    for key in ("go_runner", "postgres"):
        reference = value[key]
        if not isinstance(reference, str) or "@" not in reference or not DIGEST.fullmatch(reference.rsplit("@", 1)[1]):
            raise RunnerError("acceptance images must be pinned by SHA-256 digest")
    return {key: value[key] for key in ("go_runner", "postgres")}


def verify_pinned_image(reference: str) -> str:
    expected = reference.rsplit("@", 1)[1]
    result = docker(["image", "inspect", reference, "--format", "{{json .RepoDigests}}"], check=False)
    if result.returncode:
        raise RunnerError(f"pinned image is missing; pull the exact reference first: {reference}")
    repo_digests = v2.common.strict_json(result.stdout)
    if not isinstance(repo_digests, list) or not any(isinstance(item, str) and item.endswith("@" + expected) for item in repo_digests):
        raise RunnerError("loaded image does not match the pinned repository digest")
    return expected


def preflight_docker() -> None:
    if not os.environ.get("DOCKER_CONTEXT"):
        raise RunnerError("DOCKER_CONTEXT must explicitly select the dedicated acceptance Docker environment")
    info = docker(["info", "--format", "{{.NCPU}} {{.MemTotal}}"]).stdout.split()
    if len(info) != 2:
        raise RunnerError("cannot read Docker resource capacity")
    if int(info[0]) < 2 or int(info[1]) < 4 * 1024**3:
        raise RunnerError("workers=1 requires at least two CPUs and 4 GiB of Docker memory")
    server = docker(["version", "--format", "{{.Server.Os}}"]).stdout.strip()
    if server != "linux":
        raise RunnerError("acceptance containers require a Linux Docker server")


def parse_proc_net_tcp(raw: str, family: int) -> set[str]:
    listeners = set()
    for line in raw.splitlines()[1:]:
        fields = line.split()
        if len(fields) < 4 or fields[3] != "0A":
            continue
        address, port = fields[1].rsplit(":", 1)
        if family == socket.AF_INET:
            packed = bytes.fromhex(address)[::-1]
            ip = socket.inet_ntop(family, packed)
        else:
            packed = b"".join(bytes.fromhex(address[index:index + 8])[::-1] for index in range(0, 32, 8))
            ip = "[" + socket.inet_ntop(family, packed) + "]"
        listeners.add(f"{ip}:{int(port, 16)}")
    return listeners


def proc_host_listeners() -> set[str]:
    listeners = set()
    found = False
    for name, family in (("tcp", socket.AF_INET), ("tcp6", socket.AF_INET6)):
        path = pathlib.Path("/proc/net") / name
        if path.is_file():
            found = True
            listeners.update(parse_proc_net_tcp(path.read_text(encoding="ascii"), family))
    if not found:
        raise RunnerError("cannot read Linux TCP listener tables")
    return listeners


def host_listeners() -> set[str]:
    if shutil.which("lsof"):
        result = execute(["lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "n"], check=False)
        if result.returncode not in (0, 1):
            raise RunnerError("cannot snapshot host TCP listeners")
        return {line[1:] for line in result.stdout.splitlines() if line.startswith("n") and len(line) > 1}
    if shutil.which("ss"):
        result = execute(["ss", "-H", "-ltn"])
        listeners = set()
        for line in result.stdout.splitlines():
            fields = line.split()
            if len(fields) >= 4:
                listeners.add(fields[3])
        return listeners
    if pathlib.Path("/proc/net/tcp").is_file() or pathlib.Path("/proc/net/tcp6").is_file():
        return proc_host_listeners()
    raise RunnerError("lsof, ss, or Linux /proc/net/tcp is required to prove zero new host listeners")


def listener_snapshot(listeners: set[str]) -> dict:
    return {"count": len(listeners), "sha256": digest_bytes("\n".join(sorted(listeners)).encode())}


def require_listener_set(listeners: set[str], baseline: set[str], where: str) -> None:
    if listeners != baseline:
        added = sorted(listeners - baseline)
        removed = sorted(baseline - listeners)
        raise RunnerError(f"host listener set changed {where}: added={added}, removed={removed}")


def require_no_acceptance_host_port(listeners: set[str]) -> None:
    for listener in listeners:
        if listener.rsplit(":", 1)[-1] == "55439":
            raise RunnerError("host port 55439 is already in use; no existing environment will be touched")


def prepare_run_directory(source: pathlib.Path, requested, run_id: str) -> tuple[pathlib.Path, pathlib.Path, pathlib.Path]:
    source = source.resolve()
    path = pathlib.Path(requested) if requested else pathlib.Path.home() / ".cache" / "moni-aigate-acceptance" / run_id
    path = path.expanduser().absolute()
    if path.exists() or path.is_symlink():
        raise RunnerError("the run directory must be new for every attempt")
    path = path.resolve()
    if path == source or source in path.parents:
        raise RunnerError("the run directory and artifacts must be outside the source repository")
    path.mkdir(parents=True, mode=0o700)
    os.chmod(path, 0o700)
    evidence = path / "evidence"
    private = path / "private"
    for directory in (evidence, private, path / "worktrees", path / "cache", path / "tmp", path / "home"):
        directory.mkdir(mode=0o700)
        os.chmod(directory, 0o700)
    if shutil.disk_usage(path).free < 8 * 1024**3:
        raise RunnerError("at least 8 GiB of free artifact storage is required")
    return path, evidence, private


def source_identity(source: pathlib.Path) -> tuple[str, str]:
    commit = execute(["git", "-C", str(source), "rev-parse", "HEAD"]).stdout.strip()
    tree = execute(["git", "-C", str(source), "rev-parse", "HEAD^{tree}"]).stdout.strip()
    if not re.fullmatch(r"[0-9a-f]{40}", commit) or not re.fullmatch(r"[0-9a-f]{40}", tree):
        raise RunnerError("source commit/tree identity is invalid")
    return commit, tree


def require_clean_source(source: pathlib.Path, commit: str, tree: str) -> None:
    status = execute(["git", "-C", str(source), "status", "--porcelain"]).stdout
    if status.strip() or source_identity(source) != (commit, tree):
        raise RunnerError("acceptance runner requires an unchanged clean source HEAD")


def generate_plan(source: pathlib.Path, evidence: pathlib.Path, commit: str, tree: str) -> dict:
    require_clean_source(source, commit, tree)
    execute(["go", "run", "./scripts/acceptance-shard-plan", "--emit-legacy", str(evidence)], cwd=source)
    plan = v2.common.strict_json((evidence / "shard-plan.json").read_text(encoding="utf-8"))
    v2.part_index(plan)
    if plan["commit"] != commit or plan["tree"] != tree:
        raise RunnerError("generated shard plan is not bound to the preflight source")
    require_clean_source(source, commit, tree)
    return plan


def create_worktree(source: pathlib.Path, path: pathlib.Path, commit: str, tree: str) -> pathlib.Path:
    execute(["git", "-C", str(source), "worktree", "add", "--detach", str(path), commit])
    try:
        if source_identity(path) != (commit, tree):
            raise RunnerError("detached shard worktree has the wrong source identity")
        if execute(["git", "-C", str(path), "status", "--porcelain"]).stdout.strip():
            raise RunnerError("detached shard worktree is not clean at startup")
        common = execute(["git", "-C", str(path), "rev-parse", "--git-common-dir"]).stdout.strip()
        return (path / common).resolve()
    except Exception:
        execute(["git", "-C", str(source), "worktree", "remove", "--force", str(path)], check=False)
        raise


def require_worktree_clean(worktree: pathlib.Path, commit: str, tree: str) -> None:
    if source_identity(worktree) != (commit, tree) or execute(["git", "-C", str(worktree), "status", "--porcelain"]).stdout.strip():
        raise RunnerError("shard worktree must retain the same clean HEAD at every test-process startup")


def module_environment(run_root: pathlib.Path, shard: str) -> dict:
    base = run_root / "cache" / shard
    home = run_root / "home" / shard
    temporary = run_root / "tmp" / shard
    for directory in (base / "build", base / "mod", base / "gopath", home, temporary):
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    env = os.environ.copy()
    env.update({
        "HOME": str(home),
        "GOCACHE": str(base / "build"),
        "GOMODCACHE": str(base / "mod"),
        "GOPATH": str(base / "gopath"),
        "GOTMPDIR": str(temporary),
        "TMPDIR": str(temporary),
        "GOMAXPROCS": "2",
        "GOTOOLCHAIN": "local",
        "GOFLAGS": "",
        "GOPRIVATE": "",
        "GONOPROXY": "",
        "GONOSUMDB": "",
        "GOPROXY": "https://proxy.golang.org,direct",
        "GOSUMDB": "sum.golang.org",
        "LC_ALL": "C",
        "PYTHONDONTWRITEBYTECODE": "1",
    })
    return env


def prefetch_modules(worktree: pathlib.Path, env: dict) -> None:
    execute(["go", "mod", "download"], cwd=worktree, env=env)


def write_private_files(private: pathlib.Path, shard: str, run_root: pathlib.Path, env: dict) -> tuple[pathlib.Path, pathlib.Path]:
    postgres = private / f"{shard}-postgres.env"
    runner = private / f"{shard}-runner.env"
    password = uuid.uuid4().hex + uuid.uuid4().hex
    postgres.write_text(
        "POSTGRES_USER=moni_test\nPOSTGRES_DB=moni_test\nPOSTGRES_HOST_AUTH_METHOD=trust\n"
        f"POSTGRES_PASSWORD={password}\n",
        encoding="utf-8",
    )
    values = {
        "TEST_DATABASE_URL": DATABASE_URL,
        "GOMAXPROCS": "2",
        "GOPROXY": "off",
        "GOSUMDB": "sum.golang.org",
        "GOTOOLCHAIN": "local",
        "GOFLAGS": "",
        "CGO_ENABLED": "1",
        "LC_ALL": "C",
    }
    for key in ("HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTMPDIR", "TMPDIR"):
        values[key] = env[key]
    if shard == "other":
        values["PROCUREMENT_IDENTITY_DATABASE_URL"] = DATABASE_URL
    if shard.startswith("billing-"):
        values["PROCUREMENT_ATTEMPT_RULES_DATABASE_URL"] = DATABASE_URL
    runner.write_text("".join(f"{key}={values[key]}\n" for key in sorted(values)), encoding="utf-8")
    os.chmod(postgres, 0o600)
    os.chmod(runner, 0o600)
    return postgres, runner


def container_inspect(name: str) -> dict:
    value = v2.common.strict_json(docker(["container", "inspect", name]).stdout)
    if not isinstance(value, list) or len(value) != 1 or not isinstance(value[0], dict):
        raise RunnerError(f"cannot inspect container {name}")
    return value[0]


def start_postgres(run_id: str, shard: str, image: str, private_env: pathlib.Path, volume: str, name: str) -> tuple[dict, str]:
    docker(["volume", "create", "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}", volume])
    args = [
        "run", "--detach", "--name", name, "--network", "none",
        "--env-file", str(private_env),
        "--mount", f"type=volume,source={volume},target=/var/lib/postgresql/data",
        "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}",
        image, "postgres", "-p", "55439", "-c", "listen_addresses=127.0.0.1",
        "-c", "timezone=Asia/Shanghai", "-c", "max_connections=200",
        "-c", "fsync=on", "-c", "synchronous_commit=on", "-c", "full_page_writes=on",
    ]
    docker(args)
    inspected = container_inspect(name)
    host = inspected["HostConfig"]
    if host.get("NetworkMode") != "none" or host.get("PortBindings") or host.get("PublishAllPorts"):
        raise RunnerError("PostgreSQL must use an unpublished network namespace with no host port bindings")
    sandbox = inspected["NetworkSettings"].get("SandboxKey")
    if not isinstance(sandbox, str) or not sandbox:
        raise RunnerError("PostgreSQL network namespace identity is missing")
    readiness = """SELECT CASE WHEN inet_server_addr() = '127.0.0.1'::inet
      AND inet_server_port() = 55439 AND current_database() = 'moni_test'
      AND current_user = 'moni_test' AND current_setting('TimeZone') = 'Asia/Shanghai'
      AND current_setting('max_connections') = '200' AND current_setting('fsync') = 'on'
      AND current_setting('synchronous_commit') = 'on' AND current_setting('full_page_writes') = 'on'
      THEN 'ready' ELSE 'not_ready' END"""
    for _ in range(60):
        result = docker(["exec", name, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", "55439", "-U", "moni_test", "-d", "moni_test", "-c", readiness], check=False)
        if result.returncode == 0 and result.stdout.strip() == "ready":
            identifier = docker(["exec", name, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", "55439", "-U", "moni_test", "-d", "moni_test", "-c", "SELECT system_identifier FROM pg_control_system()"]).stdout.strip()
            if not re.fullmatch(r"[0-9]+", identifier):
                raise RunnerError("PostgreSQL system identifier is invalid")
            return inspected, identifier
        time.sleep(1)
    raise RunnerError("PostgreSQL did not become ready on the shard loopback endpoint")


def resource_sample(postgres: str, runner: str) -> list[dict]:
    result = docker(["stats", "--no-stream", "--format", "{{json .}}", postgres, runner])
    samples = []
    for line in result.stdout.splitlines():
        value = v2.common.strict_json(line)
        if not isinstance(value, dict):
            raise RunnerError("container resource sample is invalid")
        samples.append({key: value.get(key) for key in ("Name", "CPUPerc", "MemUsage", "MemPerc", "NetIO", "BlockIO", "PIDs")})
    return samples


def validate_raw_log(log: pathlib.Path, part: dict, expected: set[tuple[str, str]], run_id: str) -> None:
    framed = {"run_id": run_id, "created_at": utc_now()}
    passes, skips, package_terminals = v2.parse_events(log, part, expected, framed)
    for package in part["packages"]:
        terminals = package_terminals.get(package, [])
        if len(terminals) != 1:
            raise RunnerError(f"{part['name']} package {package} lacks one compile terminal")
        if terminals[0] != "pass" and any(key[0] == package for key in expected):
            raise RunnerError(f"{part['name']} package {package} did not PASS")
    baseline = {(entry["package"], entry["test"]) for entry in v2.reviewed_baseline()}
    for key in expected:
        if passes.get(key, 0) + skips.get(key, 0) != 1:
            raise RunnerError(f"{part['name']} test {key[0]}:{key[1]} lacks one terminal")
        if skips.get(key) and key not in baseline:
            raise RunnerError(f"{part['name']} contains an unreviewed SKIP: {key[0]}:{key[1]}")


def container_absent(name: str) -> bool:
    result = docker(["container", "inspect", name], check=False)
    return result.returncode != 0 and ("No such object" in result.stderr or "No such container" in result.stderr)


def volume_absent(name: str) -> bool:
    result = docker(["volume", "inspect", name], check=False)
    return result.returncode != 0 and "No such volume" in result.stderr


def run_part(run_root: pathlib.Path, evidence: pathlib.Path, worktree: pathlib.Path, common_git: pathlib.Path, run_id: str, shard: str, part: dict, image: str, runner_env: pathlib.Path, postgres_name: str, postgres_id: str, expected: set[tuple[str, str]], baseline_listeners: set[str]) -> tuple[dict, dict]:
    name = f"moni-040-{run_id[:12]}-{part['name']}"
    log = evidence / f"go-tests-{part['name']}.jsonl"
    stderr_path = evidence / f"go-tests-{part['name']}.stderr.log"
    args = [
        "docker", "run", "--name", name, "--network", f"container:{postgres_id}",
        "--env-file", str(runner_env), "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges",
        "--mount", f"type=bind,source={run_root},target={run_root}",
        "--mount", f"type=bind,source={common_git},target={common_git},readonly",
        "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}",
        "--workdir", str(worktree), image, *part["args"],
    ]
    entry = {
        "name": part["name"], "args": part["args"], "log": log.name, "log_sha256": "",
        "exit_status": 125,
        "database": {"url": DATABASE_URL, "system_identifier": ""},
        "cleanup": {"containers_removed": False, "volume_removed": False, "netns_removed": False},
    }
    detail = {"name": part["name"], "resource_samples": [], "oom_killed": False, "stderr": stderr_path.name, "errors": []}
    started = utc_now()
    status = 125
    try:
        with log.open("x", encoding="utf-8") as output, stderr_path.open("x", encoding="utf-8") as errors:
            output.write(f"run_id: {run_id}\ncommand: {' '.join(part['args'])}\nstarted_at: {started}\n")
            output.flush()
            try:
                process = subprocess.Popen(args, stdout=output, stderr=errors)
                sampling = True
                while process.poll() is None:
                    if sampling:
                        try:
                            samples = resource_sample(postgres_name, name)
                            detail["resource_samples"].append({"at": utc_now(), "containers": samples})
                        except Exception as error:
                            detail["errors"].append(f"container resource sampling failed: {error}")
                            sampling = False
                    if process.poll() is None:
                        time.sleep(2)
                status = process.wait()
            except Exception as error:
                detail["errors"].append(f"container execution failed: {error}")
            output.write(f"completed_at: {utc_now()}\nexit_status: {status}\n")
            output.flush()
            os.fsync(output.fileno())
        entry["exit_status"] = status
        entry["log_sha256"] = digest_bytes(log.read_bytes())
        try:
            inspected = container_inspect(name)
            detail["oom_killed"] = bool(inspected["State"].get("OOMKilled"))
            if inspected["State"].get("ExitCode") != status:
                raise RunnerError("Docker and coordinator exit statuses differ")
            if inspected.get("Path") != part["args"][0] or inspected.get("Args") != part["args"][1:]:
                raise RunnerError("container did not execute the exact PR-A argv")
            host = inspected["HostConfig"]
            if host.get("NetworkMode") != f"container:{postgres_id}" or host.get("PortBindings") or host.get("PublishAllPorts"):
                raise RunnerError("Go runner escaped its PostgreSQL network namespace")
            if status != 0 or detail["oom_killed"]:
                raise RunnerError(f"{part['name']} exited {status}; OOMKilled={detail['oom_killed']}")
            require_listener_set(host_listeners(), baseline_listeners, f"during {part['name']}")
            if not expected:
                raise RunnerError("source discovery produced no expected terminal inventory")
            validate_raw_log(log, part, expected, run_id)
            os.chmod(log, 0o400)
        except Exception as error:
            detail["errors"].append(str(error))
        return entry, detail
    finally:
        docker(["rm", "--force", name], check=False)
        detail["runner_removed"] = container_absent(name)
        if not detail["runner_removed"]:
            detail["errors"].append("Go runner container cleanup failed")


def discover_other(worktree: pathlib.Path, packages: list[str], env: dict, evidence: pathlib.Path) -> dict[str, list[str]]:
    discovery_env = env.copy()
    discovery_env["GOPROXY"] = "off"
    result = execute(["go", "test", "-json", "-race", "-count=1", "-list", ".", *packages], cwd=worktree, env=discovery_env)
    (evidence / "other-discovery.jsonl").write_text(result.stdout, encoding="utf-8")
    return v2.parse_other_json_discovery(result.stdout, packages)


def cleanup_shard(source: pathlib.Path, worktree: pathlib.Path, postgres: str, volume: str, sandbox_seen: bool, runners_removed: bool) -> tuple[dict, list[str]]:
    errors = []
    pg_removed = (docker(["rm", "--force", postgres], check=False).returncode == 0 or container_absent(postgres)) and container_absent(postgres)
    if not pg_removed:
        errors.append("PostgreSQL container cleanup failed")
    volume_removed = (docker(["volume", "rm", volume], check=False).returncode == 0 or volume_absent(volume)) and volume_absent(volume)
    if not volume_removed:
        errors.append("PostgreSQL volume cleanup failed")
    cleanup = {
        "containers_removed": bool(runners_removed and pg_removed),
        "volume_removed": bool(volume_removed),
        "netns_removed": bool(sandbox_seen and runners_removed and pg_removed),
    }
    try:
        execute(["git", "-C", str(source), "worktree", "remove", "--force", str(worktree)])
    except Exception as error:
        errors.append(f"detached worktree cleanup failed: {error}")
    return cleanup, errors


def run_shard(source: pathlib.Path, run_root: pathlib.Path, evidence: pathlib.Path, private: pathlib.Path, shard: dict, run_id: str, images: dict[str, str], baseline_listeners: set[str]) -> dict:
    name = shard["name"]
    result = {"name": name, "database_identifier": "", "parts": [], "entries": [], "cleanup": {"containers_removed": False, "volume_removed": False, "netns_removed": False}, "errors": []}
    worktree = run_root / "worktrees" / name
    postgres = f"moni-040-{run_id[:12]}-{name}-pg"
    volume = f"moni-040-{run_id[:12]}-{name}-data"
    common_git = None
    sandbox_seen = False
    runners_removed = True
    try:
        common_git = create_worktree(source, worktree, shard["commit"], shard["tree"])
        env = module_environment(run_root, name)
        prefetch_modules(worktree, env)
        postgres_env, runner_env = write_private_files(private, name, run_root, env)
        inspected, identifier = start_postgres(run_id, name, images["postgres"], postgres_env, volume, postgres)
        result["database_identifier"] = identifier
        sandbox_seen = True
        other_tests = discover_other(worktree, shard["packages"], env, evidence) if shard["class"] == "other" else {}
        for part in shard["parts"]:
            expected = {(part["packages"][0], test) for test in part["tests"]} if shard["class"] != "other" else {(package, test) for package, tests in other_tests.items() for test in tests}
            try:
                require_worktree_clean(worktree, shard["commit"], shard["tree"])
                entry, detail = run_part(run_root, evidence, worktree, common_git, run_id, name, part, images["go_runner"], runner_env, postgres, inspected["Id"], expected, baseline_listeners)
                entry["database"]["system_identifier"] = identifier
                result["entries"].append(entry)
                result["parts"].append(detail)
                runners_removed = runners_removed and detail.get("runner_removed", False)
                result["errors"].extend(detail["errors"])
            except Exception as error:
                orphan = f"moni-040-{run_id[:12]}-{part['name']}"
                docker(["rm", "--force", orphan], check=False)
                runners_removed = runners_removed and container_absent(orphan)
                result["errors"].append(f"{part['name']}: {error}")
    except Exception as error:
        result["errors"].append(str(error))
    finally:
        if common_git is not None:
            cleanup, errors = cleanup_shard(source, worktree, postgres, volume, sandbox_seen, runners_removed)
            result["cleanup"] = cleanup
            result["errors"].extend(errors)
            for entry in result["entries"]:
                entry["cleanup"] = cleanup.copy()
    return result


def required_specs(source: pathlib.Path) -> list[str]:
    shell = (source / "scripts" / "verify-current-mvp.sh").read_text(encoding="utf-8")
    current = set(re.findall(r"\./[A-Za-z0-9_/]+:Test[A-Za-z0-9_]+", shell))
    matrix = v2.common.strict_json((source / "scripts" / "phase1-acceptance-matrix.json").read_text(encoding="utf-8"))
    matrix_specs = {"./" + test for item in matrix["items"] for test in item["tests"]}
    if len(current) != 100 or len(matrix_specs) != 77:
        raise RunnerError("current-MVP/matrix required inventory changed")
    return sorted(current | matrix_specs)


def build_manifest(plan: dict, evidence: pathlib.Path, run_id: str, created_at: str, images: dict[str, str], results: list[dict]) -> dict:
    expected_parts = [part["name"] for shard in plan["shards"] for part in shard["parts"]]
    if len(results) != 14 or any(result["errors"] for result in results):
        raise RunnerError("v2 evidence requires all 14 successful logical shards")
    entries = [entry for result in results for entry in result["entries"]]
    if [entry["name"] for entry in entries] != expected_parts or any(entry["exit_status"] != 0 or not all(entry["cleanup"].values()) for entry in entries):
        raise RunnerError("v2 evidence has incomplete parts, exits, or cleanup")
    identifiers = {}
    for entry in entries:
        identifier = entry["database"]["system_identifier"]
        shard_class = "identity" if entry["name"].startswith("identity-") else entry["name"]
        previous = identifiers.get(identifier)
        if previous is not None and (previous != "identity" or shard_class != "identity"):
            raise RunnerError("database system identifier was reused across shards")
        identifiers[identifier] = shard_class
    return {
        "schema_version": 2, "suite": "go-race-parallel", "run_id": run_id,
        "created_at": created_at, "commit": plan["commit"], "tree": plan["tree"],
        "source_clean": True, "scheduler_version": 1, "workers": 1,
        "images": {key: value.rsplit("@", 1)[1] for key, value in images.items()},
        "plan": "shard-plan.json", "plan_sha256": digest_bytes((evidence / "shard-plan.json").read_bytes()),
        "skip_baseline": v2.reviewed_baseline(), "shards": entries,
    }


def merge_complete_evidence(source: pathlib.Path, evidence: pathlib.Path, plan: dict, run_id: str, images: dict[str, str], results: list[dict]) -> str:
    manifest = build_manifest(plan, evidence, run_id, utc_now(), images, results)
    path = evidence / "go-race-evidence-v2.json"
    write_json(path, manifest)
    env = os.environ.copy()
    env[v2.common.RUN_ID_ENV] = run_id
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    command = [sys.executable, "-B", "-I", str(source / "scripts" / "check-acceptance-evidence-v2.py"), "--manifest", str(path)]
    for spec in required_specs(source):
        command.extend(["--spec", spec])
    try:
        execute(command, cwd=source, env=env)
    except Exception:
        path.unlink(missing_ok=True)
        raise
    return path.name


def selected_shards(plan: dict, names) -> list[dict]:
    selected = set(names or v2.SHARD_NAMES)
    if len(selected) != len(names or v2.SHARD_NAMES):
        raise RunnerError("duplicate shard selection")
    result = []
    for shard in plan["shards"]:
        if shard["name"] in selected:
            shard = dict(shard)
            shard["commit"] = plan["commit"]
            shard["tree"] = plan["tree"]
            result.append(shard)
    if len(result) != len(selected):
        raise RunnerError("selected shard is absent from the plan")
    return result


def failed_shard_result(name: str, error: Exception) -> dict:
    return {"name": name, "database_identifier": "", "parts": [], "entries": [], "cleanup": {"containers_removed": False, "volume_removed": False, "netns_removed": False}, "errors": [str(error)]}


def run_selected(shards: list[dict], run_one) -> list[dict]:
    results = []
    for shard in shards:
        try:
            results.append(run_one(shard))
        except Exception as error:
            results.append(failed_shard_result(shard["name"], error))
    return results


def execute_attempt(args: argparse.Namespace) -> tuple[pathlib.Path, dict]:
    if args.workers != 1:
        raise RunnerError("PR-C implements workers=1 only; workers=2 is not implemented or permitted")
    if not RUN_ID.fullmatch(args.run_id):
        raise RunnerError("run_id must be 32 lowercase hexadecimal characters")
    source = pathlib.Path(args.source).resolve()
    preflight_docker()
    listeners = host_listeners()
    require_no_acceptance_host_port(listeners)
    run_root, evidence, private = prepare_run_directory(source, args.run_directory, args.run_id)
    commit, tree = source_identity(source)
    plan = generate_plan(source, evidence, commit, tree)
    shards = selected_shards(plan, args.shard)
    images = load_image_lock()
    for reference in images.values():
        verify_pinned_image(reference)
    started = utc_now()
    results = run_selected(shards, lambda shard: run_shard(source, run_root, evidence, private, shard, args.run_id, images, listeners))
    errors = [error for result in results for error in result["errors"]]
    final_listeners = None
    try:
        final_listeners = host_listeners()
        require_listener_set(final_listeners, listeners, "after all shards")
    except Exception as error:
        errors.append(str(error))
    try:
        require_clean_source(source, commit, tree)
    except Exception as error:
        errors.append(str(error))
    try:
        remaining_containers = docker(["ps", "-aq", "--filter", f"label={RUN_LABEL}={args.run_id}"]).stdout.splitlines()
        remaining_volumes = docker(["volume", "ls", "-q", "--filter", f"label={RUN_LABEL}={args.run_id}"]).stdout.splitlines()
        if remaining_containers or remaining_volumes:
            errors.append("run-labelled containers or volumes remain after cleanup")
    except Exception as error:
        errors.append(f"final Docker cleanup inspection failed: {error}")
    try:
        shutil.rmtree(private)
    except Exception as error:
        errors.append(f"private credential cleanup failed: {error}")
    manifest = None
    if len(shards) == 14 and not errors:
        try:
            manifest = merge_complete_evidence(source, evidence, plan, args.run_id, images, results)
        except Exception as error:
            errors.append(f"v2 merge failed: {error}")
    report = {
        "schema_version": 1, "suite": "040-pr-c-container-runner",
        "scope": "workers_1_canary" if len(shards) == 1 else "workers_1_full_go_race",
        "status": "PASS" if not errors else "FAIL", "reusable": bool(manifest),
        "run_id": args.run_id, "commit": commit, "tree": tree, "workers": 1,
        "fail_fast": False, "selected_shards": [shard["name"] for shard in shards],
        "started_at": started, "completed_at": utc_now(),
        "host_listeners": {
            "before": listener_snapshot(listeners),
            "after": listener_snapshot(final_listeners) if final_listeners is not None else {"unavailable": True},
            "zero_new": final_listeners == listeners,
        },
        "shards": results, "errors": errors, "manifest": manifest,
        "complete_029": False, "workers_2_proven": False,
    }
    write_json(evidence / "runner-report.json", report)
    if errors:
        raise RunnerError(f"acceptance runner failed; see {evidence / 'runner-report.json'}")
    return evidence, report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workers", type=int, default=1)
    selection = parser.add_mutually_exclusive_group(required=True)
    selection.add_argument("--shard", action="append", choices=v2.SHARD_NAMES)
    selection.add_argument("--all-shards", action="store_true")
    parser.add_argument("--run-id", default=uuid.uuid4().hex)
    parser.add_argument("--run-directory")
    parser.add_argument("--source", default=str(ROOT))
    args = parser.parse_args()
    if args.all_shards:
        args.shard = None
    try:
        evidence, report = execute_attempt(args)
        print(json.dumps({"status": report["status"], "scope": report["scope"], "evidence": str(evidence), "manifest": report["manifest"], "complete_029": False}, separators=(",", ":")))
        return 0
    except (RunnerError, OSError, KeyError, TypeError, ValueError, UnicodeError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

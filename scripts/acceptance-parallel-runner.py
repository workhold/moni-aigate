#!/usr/bin/env python3
"""Run PR-A acceptance shards in disposable, network-isolated containers."""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import select
import shutil
import socket
import subprocess
import sys
import threading
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


def preflight_docker(workers: int) -> None:
    if not os.environ.get("DOCKER_CONTEXT"):
        raise RunnerError("DOCKER_CONTEXT must explicitly select the dedicated acceptance Docker environment")
    info = docker(["info", "--format", "{{.NCPU}} {{.MemTotal}}"]).stdout.split()
    if len(info) != 2:
        raise RunnerError("cannot read Docker resource capacity")
    minimum_cpus = 4 if workers == 2 else 2
    minimum_memory = 8 if workers == 2 else 4
    visible_memory = 7.5 if workers == 2 else 4
    if int(info[0]) < minimum_cpus or int(info[1]) < int(visible_memory * 1024**3):
        raise RunnerError(f"workers={workers} requires at least {minimum_cpus} CPUs and an {minimum_memory} GiB VM ({visible_memory} GiB visible to Docker)")
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
    for directory in (evidence, private, path / "worktrees", path / "git", path / "cache", path / "tmp", path / "home"):
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


def create_worktree(source: pathlib.Path, path: pathlib.Path, commit: str, tree: str, common: pathlib.Path) -> pathlib.Path:
    if common.exists() or path.exists():
        raise RunnerError("per-shard Git metadata and worktree paths must be new")
    try:
        execute(["git", "clone", "--bare", "--no-hardlinks", str(source), str(common)])
        execute(["git", "-C", str(common), "worktree", "add", "--detach", str(path), commit])
        if source_identity(path) != (commit, tree):
            raise RunnerError("detached shard worktree has the wrong source identity")
        if execute(["git", "-C", str(path), "status", "--porcelain"]).stdout.strip():
            raise RunnerError("detached shard worktree is not clean at startup")
        if (common / "objects" / "info" / "alternates").exists():
            raise RunnerError("per-shard Git metadata must not use object alternates")
        return common
    except Exception:
        if common.exists():
            execute(["git", "-C", str(common), "worktree", "remove", "--force", str(path)], check=False)
        shutil.rmtree(path, ignore_errors=True)
        shutil.rmtree(common, ignore_errors=True)
        raise


def require_worktree_clean(worktree: pathlib.Path, commit: str, tree: str) -> None:
    if source_identity(worktree) != (commit, tree) or execute(["git", "-C", str(worktree), "status", "--porcelain"]).stdout.strip():
        raise RunnerError("shard worktree must retain the same clean HEAD at every test-process startup")


def module_environment(run_root: pathlib.Path, shard: str) -> dict:
    base = run_root / "cache" / shard
    shared = run_root / "cache"
    home = run_root / "home" / shard
    temporary = run_root / "tmp" / shard
    for directory in (base / "gopath", shared / "shared-build", shared / "shared-mod", home, temporary):
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    env = os.environ.copy()
    env.update({
        "HOME": str(home),
        "GOCACHE": str(shared / "shared-build"),
        "GOMODCACHE": str(shared / "shared-mod"),
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


def read_cidfile(path: pathlib.Path, allow_pending: bool = False) -> str:
    if not path.is_file():
        return ""
    value = path.read_text(encoding="ascii").strip()
    if not re.fullmatch(r"[0-9a-f]{64}", value):
        if allow_pending:
            return ""
        raise RunnerError(f"container cidfile {path} is invalid")
    return value


def start_postgres(run_id: str, shard: str, image: str, private_env: pathlib.Path, volume: str, name: str, resources: dict) -> tuple[dict, str]:
    if not container_absent(name) or not volume_absent(volume):
        raise RunnerError("PostgreSQL container or volume name already exists; it will not be adopted or removed")
    docker(["volume", "create", "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}", volume])
    resources["volume_created"] = True
    labels = v2.common.strict_json(docker(["volume", "inspect", volume, "--format", "{{json .Labels}}"]).stdout)
    if not isinstance(labels, dict) or labels.get(RUN_LABEL) != run_id or labels.get(SHARD_LABEL) != shard:
        raise RunnerError("new PostgreSQL volume does not carry the full attempt ownership labels")
    cidfile = private_env.parent / f"{shard}-postgres.cid"
    args = [
        "run", "--detach", "--cidfile", str(cidfile), "--name", name, "--network", "none",
        "--env-file", str(private_env),
        "--mount", f"type=volume,source={volume},target=/var/lib/postgresql/data",
        "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}",
        image, "postgres", "-p", "55439", "-c", "listen_addresses=127.0.0.1",
        "-c", "timezone=Asia/Shanghai", "-c", "max_connections=200",
        "-c", "fsync=on", "-c", "synchronous_commit=on", "-c", "full_page_writes=on",
    ]
    docker(args)
    resources["postgres_id"] = read_cidfile(cidfile)
    if not resources["postgres_id"]:
        raise RunnerError("PostgreSQL did not create an owned cidfile")
    inspected = container_inspect(resources["postgres_id"])
    if inspected.get("Id") != resources["postgres_id"]:
        raise RunnerError("PostgreSQL container identity differs from its cidfile")
    container_labels = inspected.get("Config", {}).get("Labels") or {}
    if container_labels.get(RUN_LABEL) != run_id or container_labels.get(SHARD_LABEL) != shard:
        raise RunnerError("PostgreSQL container does not carry the full attempt ownership labels")
    host = inspected["HostConfig"]
    if host.get("NetworkMode") != "none" or host.get("PortBindings") or host.get("PublishAllPorts"):
        raise RunnerError("PostgreSQL must use an unpublished network namespace with no host port bindings")
    sandbox = inspected["NetworkSettings"].get("SandboxKey")
    if not isinstance(sandbox, str) or not sandbox:
        raise RunnerError("PostgreSQL network namespace identity is missing")
    resources["sandbox_key"] = sandbox
    readiness = """SELECT CASE WHEN inet_server_addr() = '127.0.0.1'::inet
      AND inet_server_port() = 55439 AND current_database() = 'moni_test'
      AND current_user = 'moni_test' AND current_setting('TimeZone') = 'Asia/Shanghai'
      AND current_setting('max_connections') = '200' AND current_setting('fsync') = 'on'
      AND current_setting('synchronous_commit') = 'on' AND current_setting('full_page_writes') = 'on'
      THEN 'ready' ELSE 'not_ready' END"""
    for _ in range(60):
        result = docker(["exec", resources["postgres_id"], "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", "55439", "-U", "moni_test", "-d", "moni_test", "-c", readiness], check=False)
        if result.returncode == 0 and result.stdout.strip() == "ready":
            identifier = docker(["exec", resources["postgres_id"], "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", "55439", "-U", "moni_test", "-d", "moni_test", "-c", "SELECT system_identifier FROM pg_control_system()"]).stdout.strip()
            if not re.fullmatch(r"[0-9]+", identifier):
                raise RunnerError("PostgreSQL system identifier is invalid")
            return inspected, identifier
        time.sleep(1)
    raise RunnerError("PostgreSQL did not become ready on the shard loopback endpoint")


PROBE_ROLE = "moni_040_parallel_probe"
PROBE_OWNER = "moni_040_parallel_owner"
PROBE_TABLE = "public.moni_040_parallel_probe"
PROBE_LOCK = 42040002
PROBE_ACL_COUNT = """SELECT count(*) FROM pg_default_acl d
  CROSS JOIN LATERAL aclexplode(d.defaclacl) a
  JOIN pg_roles grantee ON grantee.oid = a.grantee
  WHERE d.defaclrole = 'moni_040_parallel_owner'::regrole AND d.defaclobjtype = 'r'
  AND a.privilege_type = 'SELECT' AND grantee.rolname = 'moni_040_parallel_probe'"""
MONI_DEFAULT_ACLS = """SELECT coalesce(string_agg(defaclacl::text, '|' ORDER BY defaclacl::text), '')
  FROM pg_default_acl WHERE defaclrole = 'moni_test'::regrole"""


def probe_sql(postgres_id: str, sql: str) -> str:
    return docker([
        "exec", postgres_id, "psql", "-X", "-qAt", "--no-psqlrc", "-v", "ON_ERROR_STOP=1",
        "-h", "127.0.0.1", "-p", "55439", "-U", "moni_test", "-d", "moni_test", "-c", sql,
    ]).stdout.strip()


def read_lock_acquisition(process: subprocess.Popen) -> int:
    ready, _, _ = select.select([process.stdout], [], [], 30)
    if not ready:
        raise RunnerError("timed out waiting for the dual-cluster advisory-lock holder")
    line = process.stdout.readline().strip()
    fields = line.split("|")
    if len(fields) != 3 or fields[0] != "acquired" or not fields[1].isdigit():
        raise RunnerError(f"dual-cluster advisory-lock holder failed: {line[-500:]}")
    return int(fields[1])


def release_lock_holder(process: subprocess.Popen) -> None:
    process.stdin.write(f"SELECT pg_advisory_unlock({PROBE_LOCK});\n\\q\n")
    process.stdin.flush()
    process.stdin.close()
    status = process.wait(timeout=10)
    if status != 0 or process.stdout.read().strip() != "t":
        raise RunnerError("dual-cluster advisory-lock holder did not release cleanly")


def prove_dual_cluster_isolation(records: dict[str, dict]) -> dict:
    started = utc_now()
    started_monotonic = time.monotonic()
    if set(records) != {"A", "B"}:
        raise RunnerError("dual-cluster proof requires exactly two active clusters")
    if records["A"]["system_identifier"] == records["B"]["system_identifier"]:
        raise RunnerError("dual-cluster proof requires distinct PostgreSQL system identifiers")
    for record in records.values():
        inspected = container_inspect(record["postgres_id"])
        if inspected.get("Id") != record["postgres_id"] or inspected.get("State", {}).get("Running") is not True:
            raise RunnerError("both PostgreSQL clusters must be running during the isolation proof")
    holder = None
    holder_pid = None
    released = False
    original_acls = {role: probe_sql(record["postgres_id"], MONI_DEFAULT_ACLS) for role, record in records.items()}
    try:
        for role, record in records.items():
            probe_sql(record["postgres_id"], f"""BEGIN;
              CREATE ROLE {PROBE_OWNER} NOLOGIN;
              CREATE ROLE {PROBE_ROLE} NOLOGIN;
              ALTER DEFAULT PRIVILEGES FOR ROLE {PROBE_OWNER} GRANT SELECT ON TABLES TO {PROBE_ROLE};
              GRANT CREATE ON SCHEMA public TO {PROBE_OWNER};
              SET ROLE {PROBE_OWNER};
              CREATE TABLE {PROBE_TABLE}(marker text);
              INSERT INTO {PROBE_TABLE} VALUES ('{role}');
              RESET ROLE;
              COMMIT;""")
            if probe_sql(record["postgres_id"], f"SELECT count(*) FROM pg_roles WHERE rolname IN ('{PROBE_OWNER}', '{PROBE_ROLE}')") != "2":
                raise RunnerError("same-name role was not created in both clusters")
            if probe_sql(record["postgres_id"], PROBE_ACL_COUNT) != "1":
                raise RunnerError("same-name default ACL was not created in both clusters")
            if probe_sql(record["postgres_id"], f"SELECT has_table_privilege('{PROBE_ROLE}', '{PROBE_TABLE}', 'SELECT')") != "t":
                raise RunnerError("default ACL did not apply to the probe table")
        args = [
            "docker", "exec", "-i", records["A"]["postgres_id"], "psql", "-X", "-qAt", "--no-psqlrc",
            "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", "55439", "-U", "moni_test", "-d", "moni_test",
        ]
        holder = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding="utf-8", bufsize=1)
        holder.stdin.write(f"SELECT 'acquired', pg_backend_pid(), pg_advisory_lock({PROBE_LOCK});\n")
        holder.stdin.flush()
        holder_pid = read_lock_acquisition(holder)
        lock_outcome = probe_sql(records["B"]["postgres_id"], f"SELECT pg_try_advisory_lock({PROBE_LOCK}); SELECT pg_advisory_unlock({PROBE_LOCK});")
        if lock_outcome.splitlines() != ["t", "t"]:
            raise RunnerError("cluster B could not acquire and release the same advisory lock in one session while cluster A held it")
        probe_sql(records["A"]["postgres_id"], f"""BEGIN;
          ALTER DEFAULT PRIVILEGES FOR ROLE {PROBE_OWNER} REVOKE SELECT ON TABLES FROM {PROBE_ROLE};
          DROP TABLE {PROBE_TABLE};
          REVOKE CREATE ON SCHEMA public FROM {PROBE_OWNER};
          DROP ROLE {PROBE_ROLE};
          DROP ROLE {PROBE_OWNER};
          COMMIT;""")
        remaining = probe_sql(records["B"]["postgres_id"], f"""SELECT
          (SELECT count(*) FROM pg_roles WHERE rolname IN ('{PROBE_OWNER}', '{PROBE_ROLE}')) || '|' ||
          ({PROBE_ACL_COUNT}) || '|' ||
          has_table_privilege('{PROBE_ROLE}', '{PROBE_TABLE}', 'SELECT') || '|' ||
          (SELECT marker FROM {PROBE_TABLE})""")
        if remaining != "2|1|true|B":
            raise RunnerError(f"cluster A role/default ACL mutation affected cluster B: remaining={remaining!r}")
        release_lock_holder(holder)
        released = True
        probe_sql(records["B"]["postgres_id"], f"""BEGIN;
          ALTER DEFAULT PRIVILEGES FOR ROLE {PROBE_OWNER} REVOKE SELECT ON TABLES FROM {PROBE_ROLE};
          DROP TABLE {PROBE_TABLE};
          REVOKE CREATE ON SCHEMA public FROM {PROBE_OWNER};
          DROP ROLE {PROBE_ROLE};
          DROP ROLE {PROBE_OWNER};
          COMMIT;""")
        for role, record in records.items():
            if probe_sql(record["postgres_id"], f"SELECT count(*) FROM pg_roles WHERE rolname IN ('{PROBE_OWNER}', '{PROBE_ROLE}')") != "0":
                raise RunnerError("dual-cluster probe role cleanup failed")
            if probe_sql(record["postgres_id"], MONI_DEFAULT_ACLS) != original_acls[role]:
                raise RunnerError("dual-cluster probe changed the moni_test default ACL state")
            if probe_sql(record["postgres_id"], "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'") != "0":
                raise RunnerError("dual-cluster probe advisory lock cleanup failed")
        return {
            "schema_version": 1, "status": "PASS", "started_at": started, "completed_at": utc_now(),
            "wall_seconds": round(time.monotonic() - started_monotonic, 3),
            "clusters": {role: {"shard": record["shard"], "system_identifier": record["system_identifier"]} for role, record in records.items()},
            "simultaneous_running": True, "same_role": [PROBE_OWNER, PROBE_ROLE],
            "default_acl_independent": True, "default_acl_restored": True, "advisory_lock_independent": True,
            "probe_objects_removed": True, "advisory_locks_remaining": 0,
        }
    finally:
        if holder is not None and not released:
            if holder_pid is not None:
                try:
                    probe_sql(records["A"]["postgres_id"], f"SELECT pg_terminate_backend({holder_pid})")
                except Exception:
                    pass
            if holder.stdin and not holder.stdin.closed:
                try:
                    holder.stdin.close()
                except Exception:
                    pass
            try:
                holder.wait(timeout=5)
            except subprocess.TimeoutExpired:
                holder.kill()
                holder.wait(timeout=5)


class DualClusterProof:
    def __init__(self, evidence: pathlib.Path | None = None):
        self.barrier = threading.Barrier(2)
        self.records = {}
        self.mutex = threading.Lock()
        self.result = None
        self.error = None
        self.finished = False
        self.path = evidence / "dual-cluster-proof.json" if evidence is not None else None

    def record(self) -> None:
        if self.path is not None and self.result is not None:
            write_json(self.path, self.result)

    def verify(self, role: str, shard: str, postgres_id: str, system_identifier: str) -> None:
        with self.mutex:
            if self.error is not None:
                raise RunnerError(self.error)
            self.records[role] = {"shard": shard, "postgres_id": postgres_id, "system_identifier": system_identifier}
        try:
            self.barrier.wait(timeout=120)
        except threading.BrokenBarrierError as error:
            raise RunnerError(self.error or "the first two PostgreSQL clusters did not become ready together") from error
        if role == "A":
            try:
                self.result = prove_dual_cluster_isolation(self.records)
                self.record()
            except Exception as error:
                self.error = f"dual-cluster isolation proof failed: {error}"
                self.result = {"schema_version": 1, "status": "FAIL", "error": self.error}
                self.finished = True
                self.record()
                self.barrier.abort()
                raise RunnerError(self.error) from error
            self.finished = True
        try:
            self.barrier.wait(timeout=120)
        except threading.BrokenBarrierError as error:
            raise RunnerError(self.error or "dual-cluster isolation proof was interrupted") from error
        if self.error is not None:
            raise RunnerError(self.error)

    def abandon(self, error: Exception) -> None:
        with self.mutex:
            if not self.finished:
                self.error = f"dual-cluster isolation proof could not start: {error}"
                self.result = {"schema_version": 1, "status": "FAIL", "error": self.error}
                self.finished = True
                self.record()
                self.barrier.abort()


def resource_sample(postgres: str, runner: str, runner_name: str) -> list[dict]:
    result = docker(["stats", "--no-stream", "--format", "{{json .}}", postgres, runner])
    samples = []
    fields = ("Name", "CPUPerc", "MemUsage", "MemPerc", "NetIO", "BlockIO", "PIDs")
    for line in result.stdout.splitlines():
        value = v2.common.strict_json(line)
        if not isinstance(value, dict) or any(not isinstance(value.get(key), str) or not value.get(key) for key in fields):
            raise RunnerError("container resource sample is incomplete")
        samples.append({key: value[key] for key in fields})
    if len(samples) != 2 or {sample["Name"] for sample in samples} != {postgres, runner_name}:
        raise RunnerError("container resource sample must contain exactly the owned PostgreSQL and Go runner")
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
    detail = result.stderr.lower()
    return result.returncode != 0 and ("no such object" in detail or "no such container" in detail)


def volume_absent(name: str) -> bool:
    result = docker(["volume", "inspect", name], check=False)
    return result.returncode != 0 and "no such volume" in result.stderr.lower()


def run_part(run_root: pathlib.Path, evidence: pathlib.Path, worktree: pathlib.Path, common_git: pathlib.Path, run_id: str, shard: str, part: dict, image: str, runner_env: pathlib.Path, postgres_name: str, postgres_id: str, expected: set[tuple[str, str]], baseline_listeners: set[str]) -> tuple[dict, dict]:
    name = f"moni-040-{run_id}-{part['name']}"
    log = evidence / f"go-tests-{part['name']}.jsonl"
    stderr_path = evidence / f"go-tests-{part['name']}.stderr.log"
    cidfile = runner_env.parent / f"{part['name']}.cid"
    args = [
        "docker", "run", "--cidfile", str(cidfile), "--name", name, "--network", f"container:{postgres_id}",
        "--env-file", str(runner_env), "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges",
        "--mount", f"type=bind,source={worktree},target={worktree}",
        "--mount", f"type=bind,source={run_root / 'cache' / 'shared-build'},target={run_root / 'cache' / 'shared-build'}",
        "--mount", f"type=bind,source={run_root / 'cache' / 'shared-mod'},target={run_root / 'cache' / 'shared-mod'},readonly",
        "--mount", f"type=bind,source={run_root / 'cache' / shard / 'gopath'},target={run_root / 'cache' / shard / 'gopath'}",
        "--mount", f"type=bind,source={run_root / 'home' / shard},target={run_root / 'home' / shard}",
        "--mount", f"type=bind,source={run_root / 'tmp' / shard},target={run_root / 'tmp' / shard}",
        "--mount", f"type=bind,source={common_git},target={common_git}",
        "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}",
        "--workdir", str(worktree), image, *part["args"],
    ]
    entry = {
        "name": part["name"], "args": part["args"], "log": log.name, "log_sha256": "",
        "exit_status": 125,
        "database": {"url": DATABASE_URL, "system_identifier": ""},
        "cleanup": {"containers_removed": False, "volume_removed": False, "netns_removed": False},
    }
    detail = {"name": part["name"], "container_id": "", "resource_samples": [], "oom_killed": False, "stderr": stderr_path.name, "errors": []}
    started = utc_now()
    started_monotonic = time.monotonic()
    detail["started_at"] = started
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
                            if not detail["container_id"]:
                                detail["container_id"] = read_cidfile(cidfile, allow_pending=True)
                            if detail["container_id"]:
                                samples = resource_sample(postgres_name, detail["container_id"], name)
                                detail["resource_samples"].append({"at": utc_now(), "containers": samples})
                        except Exception as error:
                            if "no such container" not in str(error).lower():
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
            if not detail["container_id"]:
                detail["container_id"] = read_cidfile(cidfile)
            if not detail["container_id"]:
                raise RunnerError("Docker did not create an owned runner cidfile; no same-name container will be removed")
            inspected = container_inspect(detail["container_id"])
            if inspected.get("Id") != detail["container_id"]:
                raise RunnerError("Go runner identity differs from its cidfile")
            container_labels = inspected.get("Config", {}).get("Labels") or {}
            if container_labels.get(RUN_LABEL) != run_id or container_labels.get(SHARD_LABEL) != shard:
                raise RunnerError("Go runner does not carry the full attempt ownership labels")
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
        try:
            if not detail["container_id"]:
                detail["container_id"] = read_cidfile(cidfile)
        except Exception as error:
            detail["errors"].append(str(error))
        if detail["container_id"]:
            docker(["rm", "--force", detail["container_id"]], check=False)
            detail["runner_removed"] = container_absent(detail["container_id"])
        else:
            detail["runner_removed"] = container_absent(name)
        if not detail["runner_removed"]:
            detail["errors"].append("Go runner container cleanup failed or a same-name foreign container remains")
        detail["completed_at"] = utc_now()
        detail["wall_seconds"] = round(time.monotonic() - started_monotonic, 3)


def discover_other(worktree: pathlib.Path, packages: list[str], env: dict, evidence: pathlib.Path) -> dict[str, list[str]]:
    discovery_env = env.copy()
    discovery_env["GOPROXY"] = "off"
    result = execute(["go", "test", "-json", "-race", "-count=1", "-list", ".", *packages], cwd=worktree, env=discovery_env)
    (evidence / "other-discovery.jsonl").write_text(result.stdout, encoding="utf-8")
    return v2.parse_other_json_discovery(result.stdout, packages)


def netns_absent(sandbox_key: str, image: str, run_id: str, shard: str, private: pathlib.Path) -> bool:
    path = pathlib.PurePosixPath(sandbox_key)
    if path.parent not in (pathlib.PurePosixPath("/var/run/docker/netns"), pathlib.PurePosixPath("/run/docker/netns")) or not re.fullmatch(r"[0-9a-f]{12,64}", path.name):
        raise RunnerError("Docker sandbox identity is outside the expected netns directory")
    name = f"moni-040-{run_id}-{shard}-netns-proof"
    cidfile = private / f"{shard}-netns-proof.cid"
    args = [
        "run", "--cidfile", str(cidfile), "--name", name, "--network", "host",
        "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only",
        "--mount", f"type=bind,source={path.parent},target=/host-netns,readonly",
        "--label", f"{RUN_LABEL}={run_id}", "--label", f"{SHARD_LABEL}={shard}-netns-proof",
        "--entrypoint", "python3", image, "-B", "-I", "-c",
        "import os,sys; sys.exit(1 if os.path.lexists(sys.argv[1]) else 0)", f"/host-netns/{path.name}",
    ]
    result = docker(args, check=False)
    container_id = ""
    removed = False
    try:
        container_id = read_cidfile(cidfile)
        if not container_id:
            if not container_absent(name):
                raise RunnerError("netns-proof container has no owned cidfile; its same-name resource will not be removed")
            raise RunnerError("netns-proof container did not create an owned cidfile")
        inspected = container_inspect(container_id)
        labels = inspected.get("Config", {}).get("Labels") or {}
        if inspected.get("Id") != container_id or labels.get(RUN_LABEL) != run_id or labels.get(SHARD_LABEL) != shard + "-netns-proof":
            raise RunnerError("netns-proof container ownership does not match this shard")
        if inspected.get("HostConfig", {}).get("NetworkMode") != "host":
            raise RunnerError("netns-proof helper must use the host namespace without creating another sandbox")
        exit_code = inspected.get("State", {}).get("ExitCode")
        docker(["rm", "--force", container_id], check=False)
        removed = container_absent(container_id)
        if not removed:
            raise RunnerError("netns-proof container cleanup failed")
        return result.returncode == 0 and exit_code == 0
    finally:
        if container_id and not removed:
            docker(["rm", "--force", container_id], check=False)
            if not container_absent(container_id):
                raise RunnerError("netns-proof container remains after owned-ID cleanup")


def cleanup_shard(common_git: pathlib.Path, worktree: pathlib.Path, postgres: str, volume: str, resources: dict, runners_removed: bool, image: str, run_id: str, shard: str, private: pathlib.Path) -> tuple[dict, list[str]]:
    errors = []
    postgres_id = resources.get("postgres_id", "")
    if postgres_id:
        try:
            inspected = container_inspect(postgres_id)
            state = inspected.get("State", {})
            resources["postgres_terminal"] = {
                "running_before_cleanup": state.get("Running") is True,
                "oom_killed": state.get("OOMKilled") is True,
                "exit_code_before_cleanup": state.get("ExitCode"),
            }
            if state.get("OOMKilled") is True or state.get("Running") is not True:
                errors.append(f"PostgreSQL terminal state is not healthy: {resources['postgres_terminal']}")
        except Exception as error:
            errors.append(f"PostgreSQL terminal-state inspection failed: {error}")
        docker(["rm", "--force", postgres_id], check=False)
        pg_removed = container_absent(postgres_id)
    else:
        pg_removed = container_absent(postgres)
    if not pg_removed:
        errors.append("PostgreSQL container cleanup failed or a foreign same-name container remains")
    if resources.get("volume_created"):
        docker(["volume", "rm", volume], check=False)
    volume_removed = volume_absent(volume)
    if not volume_removed:
        errors.append("PostgreSQL volume cleanup failed or a foreign same-name volume remains")
    netns_removed = False
    if resources.get("sandbox_key") and runners_removed and pg_removed:
        try:
            netns_removed = netns_absent(resources["sandbox_key"], image, run_id, shard, private)
        except Exception as error:
            errors.append(f"netns cleanup inspection failed: {error}")
    cleanup = {
        "containers_removed": bool(runners_removed and pg_removed),
        "volume_removed": bool(volume_removed),
        "netns_removed": bool(netns_removed),
    }
    if resources.get("sandbox_key") and not netns_removed and not any("netns cleanup inspection failed" in error for error in errors):
        errors.append("Docker sandbox path still exists or cannot be proven absent")
    try:
        execute(["git", "-C", str(common_git), "worktree", "remove", "--force", str(worktree)])
        shutil.rmtree(common_git)
    except Exception as error:
        errors.append(f"detached worktree and per-shard Git cleanup failed: {error}")
    return cleanup, errors


def run_shard(source: pathlib.Path, run_root: pathlib.Path, evidence: pathlib.Path, private: pathlib.Path, shard: dict, run_id: str, images: dict[str, str], baseline_listeners: set[str], dual_proof: DualClusterProof | None = None, dual_role: str | None = None) -> dict:
    started = utc_now()
    started_monotonic = time.monotonic()
    name = shard["name"]
    result = {"name": name, "database_identifier": "", "started_at": started, "parts": [], "entries": [], "cleanup": {"containers_removed": False, "volume_removed": False, "netns_removed": False}, "errors": []}
    worktree = run_root / "worktrees" / name
    postgres = f"moni-040-{run_id}-{name}-pg"
    volume = f"moni-040-{run_id}-{name}-data"
    common_git = None
    resources = {"volume_created": False, "postgres_id": "", "sandbox_key": ""}
    runners_removed = True
    try:
        common_git = create_worktree(source, worktree, shard["commit"], shard["tree"], run_root / "git" / f"{name}.git")
        env = module_environment(run_root, name)
        prefetch_modules(worktree, env)
        postgres_env, runner_env = write_private_files(private, name, run_root, env)
        inspected, identifier = start_postgres(run_id, name, images["postgres"], postgres_env, volume, postgres, resources)
        result["database_identifier"] = identifier
        if dual_proof is not None:
            dual_proof.verify(dual_role, name, inspected["Id"], identifier)
            result["dual_cluster_role"] = dual_role
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
                if not detail.get("runner_removed", False):
                    result["errors"].append(f"{part['name']}: runner removal is unproven; later parts in this shard are unsafe and will not start")
                    break
            except Exception as error:
                orphan = f"moni-040-{run_id}-{part['name']}"
                cidfile = runner_env.parent / f"{part['name']}.cid"
                try:
                    container_id = read_cidfile(cidfile)
                except Exception as cid_error:
                    container_id = ""
                    runners_removed = False
                    result["errors"].append(f"{part['name']}: runner ownership is unreadable: {cid_error}")
                if container_id:
                    docker(["rm", "--force", container_id], check=False)
                    runners_removed = runners_removed and container_absent(container_id)
                else:
                    runners_removed = runners_removed and container_absent(orphan)
                result["errors"].append(f"{part['name']}: {error}")
                if not runners_removed:
                    result["errors"].append(f"{part['name']}: runner removal is unproven; later parts in this shard are unsafe and will not start")
                    break
    except Exception as error:
        result["errors"].append(str(error))
        if dual_proof is not None:
            dual_proof.abandon(error)
    finally:
        if common_git is not None:
            cleanup, errors = cleanup_shard(common_git, worktree, postgres, volume, resources, runners_removed, images["go_runner"], run_id, name, private)
            result["cleanup"] = cleanup
            result["postgres_terminal"] = resources.get("postgres_terminal", {"inspection": "unavailable"})
            result["errors"].extend(errors)
            for entry in result["entries"]:
                entry["cleanup"] = cleanup.copy()
        result["completed_at"] = utc_now()
        result["wall_seconds"] = round(time.monotonic() - started_monotonic, 3)
    return result


def required_specs(source: pathlib.Path) -> list[str]:
    shell = (source / "scripts" / "verify-current-mvp.sh").read_text(encoding="utf-8")
    current = set(re.findall(r"\./[A-Za-z0-9_/]+:Test[A-Za-z0-9_]+", shell))
    matrix = v2.common.strict_json((source / "scripts" / "phase1-acceptance-matrix.json").read_text(encoding="utf-8"))
    matrix_specs = {"./" + test for item in matrix["items"] for test in item["tests"]}
    if len(current) != 100 or len(matrix_specs) != 77:
        raise RunnerError("current-MVP/matrix required inventory changed")
    return sorted(current | matrix_specs)


def build_manifest(plan: dict, evidence: pathlib.Path, run_id: str, created_at: str, images: dict[str, str], results: list[dict], workers: int = 1) -> dict:
    if workers not in (1, 2):
        raise RunnerError("v2 workers must be one or two")
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
    if len(identifiers) != 14:
        raise RunnerError("v2 evidence requires exactly 14 distinct clusters, with only identity parts sharing")
    return {
        "schema_version": 2, "suite": "go-race-parallel", "run_id": run_id,
        "created_at": created_at, "commit": plan["commit"], "tree": plan["tree"],
        "source_clean": True, "scheduler_version": 1, "workers": workers,
        "images": {key: value.rsplit("@", 1)[1] for key, value in images.items()},
        "plan": "shard-plan.json", "plan_sha256": digest_bytes((evidence / "shard-plan.json").read_bytes()),
        "skip_baseline": v2.reviewed_baseline(), "shards": entries,
    }


def merge_complete_evidence(source: pathlib.Path, evidence: pathlib.Path, plan: dict, run_id: str, images: dict[str, str], results: list[dict], workers: int = 1) -> str:
    manifest = build_manifest(plan, evidence, run_id, utc_now(), images, results, workers)
    path = evidence / "go-race-evidence-v2.json"
    candidate = evidence / "go-race-evidence-v2.candidate.json"
    candidate.unlink(missing_ok=True)
    path.unlink(missing_ok=True)
    write_json(candidate, manifest)
    env = os.environ.copy()
    env[v2.common.RUN_ID_ENV] = run_id
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    command = [sys.executable, "-B", "-I", str(source / "scripts" / "check-acceptance-evidence-v2.py"), "--manifest", str(candidate)]
    for spec in required_specs(source):
        command.extend(["--spec", spec])
    try:
        execute(command, cwd=source, env=env)
        os.replace(candidate, path)
    except Exception:
        candidate.unlink(missing_ok=True)
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


def run_selected(shards: list[dict], run_one, workers: int = 1) -> list[dict]:
    if workers == 1:
        results = []
        for shard in shards:
            try:
                results.append(run_one(shard))
            except Exception as error:
                results.append(failed_shard_result(shard["name"], error))
        return results
    if workers != 2:
        raise RunnerError("only workers=1 and workers=2 are implemented")
    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=2, thread_name_prefix="acceptance-shard") as executor:
        futures = [executor.submit(run_one, shard) for shard in shards]
        for shard, future in zip(shards, futures):
            try:
                results.append(future.result())
            except Exception as error:
                results.append(failed_shard_result(shard["name"], error))
    return results


def attempt_scope(shard_count: int, workers: int = 1) -> str:
    if workers == 2:
        return "workers_2_full_go_race" if shard_count == 14 else "workers_2_partial"
    if shard_count == 1:
        return "workers_1_canary"
    if shard_count == 14:
        return "workers_1_full_go_race"
    return "workers_1_partial"


def execute_attempt(args: argparse.Namespace) -> tuple[pathlib.Path, dict]:
    if args.workers not in (1, 2):
        raise RunnerError("only workers=1 and workers=2 are implemented; workers>=3 is forbidden")
    if args.workers == 2 and args.shard is not None:
        raise RunnerError("workers=2 requires --all-shards so the complete 14-shard attempt cannot be stitched")
    if not RUN_ID.fullmatch(args.run_id):
        raise RunnerError("run_id must be 32 lowercase hexadecimal characters")
    started = utc_now()
    started_monotonic = time.monotonic()
    source = pathlib.Path(args.source).resolve()
    preflight_docker(args.workers)
    listeners = host_listeners()
    require_no_acceptance_host_port(listeners)
    run_root, evidence, private = prepare_run_directory(source, args.run_directory, args.run_id)
    commit, tree = source_identity(source)
    plan = generate_plan(source, evidence, commit, tree)
    shards = selected_shards(plan, args.shard)
    images = load_image_lock()
    for reference in images.values():
        verify_pinned_image(reference)
    dual_proof = DualClusterProof(evidence) if args.workers == 2 else None
    dual_roles = {shards[0]["name"]: "A", shards[1]["name"]: "B"} if dual_proof is not None else {}

    def run_one(shard):
        role = dual_roles.get(shard["name"])
        return run_shard(source, run_root, evidence, private, shard, args.run_id, images, listeners, dual_proof if role else None, role)

    parallel_started = time.monotonic()
    results = run_selected(shards, run_one, args.workers)
    parallel_seconds = round(time.monotonic() - parallel_started, 3)
    errors = [error for result in results for error in result["errors"]]
    if dual_proof is not None and (not dual_proof.result or dual_proof.result.get("status") != "PASS"):
        errors.append("workers=2 did not complete the simultaneous dual-cluster isolation proof")
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
    merge_seconds = 0.0
    if len(shards) == 14 and not errors:
        merge_started = time.monotonic()
        try:
            manifest = merge_complete_evidence(source, evidence, plan, args.run_id, images, results, args.workers)
        except Exception as error:
            errors.append(f"v2 merge failed: {error}")
        merge_seconds = round(time.monotonic() - merge_started, 3)
    proof_result = dual_proof.result if dual_proof is not None else None
    report = {
        "schema_version": 1, "suite": "040-pr-d-parallel-runner" if args.workers == 2 else "040-pr-c-container-runner",
        "scope": attempt_scope(len(shards), args.workers),
        "status": "PASS" if not errors else "FAIL", "reusable": bool(manifest),
        "run_id": args.run_id, "commit": commit, "tree": tree, "workers": args.workers,
        "fail_fast": False, "selected_shards": [shard["name"] for shard in shards],
        "started_at": started, "completed_at": utc_now(),
        "timings": {
            "wall_seconds": round(time.monotonic() - started_monotonic, 3),
            "parallel_shards_seconds": parallel_seconds,
            "merge_seconds": merge_seconds,
        },
        "host_listeners": {
            "before": listener_snapshot(listeners),
            "after": listener_snapshot(final_listeners) if final_listeners is not None else {"unavailable": True},
            "zero_new": final_listeners == listeners,
        },
        "dual_cluster_proof": proof_result,
        "shards": results, "errors": errors, "manifest": manifest,
        "manifest_sha256": digest_bytes((evidence / manifest).read_bytes()) if manifest else None,
        "complete_029": False, "workers_2_proven": bool(proof_result and proof_result.get("status") == "PASS"),
    }
    try:
        write_json(evidence / "runner-report.json", report)
    except Exception:
        if manifest:
            (evidence / manifest).unlink(missing_ok=True)
        raise
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

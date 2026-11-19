#!/usr/bin/env python3
"""Run the acceptance shard coordinator inside the dedicated Linux Docker VM."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import subprocess
import sys
import uuid

ROOT = pathlib.Path(__file__).resolve().parent.parent
IMAGE_LOCK = pathlib.Path(__file__).with_name("acceptance-runner-images.json")
COORDINATOR_LABEL = "com.moni-aigate.acceptance.coordinator"
RUN_ID = re.compile(r"[0-9a-f]{32}")
CONTAINER_ID = re.compile(r"[0-9a-f]{64}")


class CoordinatorError(ValueError):
    pass


def execute(args: list[str], check: bool = True) -> subprocess.CompletedProcess:
    result = subprocess.run(args, text=True, encoding="utf-8", errors="strict", capture_output=True)
    if check and result.returncode:
        raise CoordinatorError(f"{args[0]} exited {result.returncode}: {(result.stderr or result.stdout).strip()[-1000:]}")
    return result


def container_absent(name: str) -> bool:
    result = execute(["docker", "container", "inspect", name], check=False)
    detail = result.stderr.lower()
    return result.returncode != 0 and ("no such object" in detail or "no such container" in detail)


def go_image(path: pathlib.Path = IMAGE_LOCK) -> str:
    value = json.loads(path.read_text(encoding="utf-8"))
    reference = value.get("go_runner")
    if value.get("schema_version") != 1 or not isinstance(reference, str) or not re.fullmatch(r".+@sha256:[0-9a-f]{64}", reference):
        raise CoordinatorError("the coordinator must use the pinned Go repository digest")
    return reference


def git_common_dir(source: pathlib.Path) -> pathlib.Path:
    value = execute(["git", "-C", str(source), "rev-parse", "--git-common-dir"]).stdout.strip()
    return (source / value).resolve() if not os.path.isabs(value) else pathlib.Path(value).resolve()


def mount_to(source: pathlib.Path | str, target: pathlib.Path | str, readonly: bool = False) -> str:
    source_text = str(source)
    target_text = str(target)
    if any(not os.path.isabs(text) or any(character in text for character in (",", "\n", "\r")) for text in (source_text, target_text)):
        raise CoordinatorError("coordinator mount paths must be absolute and free of CSV control characters")
    return f"type=bind,source={source_text},target={target_text}" + (",readonly" if readonly else "")


def mount(source: pathlib.Path | str, readonly: bool = False) -> str:
    return mount_to(source, source, readonly)


def coordinator_command(source: pathlib.Path, run_directory: pathlib.Path, run_id: str, workers: int, selection: list[str]) -> tuple[list[str], pathlib.Path, str]:
    if not RUN_ID.fullmatch(run_id) or workers not in (1, 2):
        raise CoordinatorError("invalid coordinator run identity or worker count")
    context = os.environ.get("DOCKER_CONTEXT")
    if not context:
        raise CoordinatorError("DOCKER_CONTEXT must explicitly select the dedicated acceptance VM")
    source = source.resolve()
    run_directory = run_directory.resolve()
    common = git_common_dir(source)
    name = f"moni-040-coordinator-{run_id}"
    cidfile = run_directory.parent / f".{run_id}.coordinator.cid"
    docker_binary = os.environ.get("PHASE1_ACCEPTANCE_VM_DOCKER_BINARY", "/usr/bin/docker")
    docker_socket = os.environ.get("PHASE1_ACCEPTANCE_VM_DOCKER_SOCKET", "/var/run/docker.sock")
    args = [
        "docker", "run", "--rm", "--cidfile", str(cidfile), "--name", name,
        "--network", "host", "--init", "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=4294967296",
        "--label", f"{COORDINATOR_LABEL}={run_id}",
        "--env", "DOCKER_HOST=unix:///var/run/docker.sock", "--env", f"DOCKER_CONTEXT={context}",
        "--env", "HOME=/tmp/home", "--env", "GOPATH=/tmp/gopath", "--env", "GOCACHE=/tmp/go-build",
        "--env", "GOMODCACHE=/tmp/gopath/pkg/mod", "--env", "GOTMPDIR=/tmp/go-tmp",
        "--env", "GOMAXPROCS=2", "--env", "GOTOOLCHAIN=local", "--env", "PYTHONDONTWRITEBYTECODE=1",
        "--mount", mount(docker_binary, True), "--mount", mount(docker_socket),
        "--mount", mount(source), "--mount", mount(run_directory.parent),
    ]
    if common != source and source not in common.parents:
        args += ["--mount", mount(common)]
    xdg_root = pathlib.Path(os.environ.get("XDG_CONFIG_HOME", str(pathlib.Path.home() / ".config")))
    git_ignore = xdg_root / "git" / "ignore"
    if git_ignore.is_file():
        args += ["--env", "XDG_CONFIG_HOME=/tmp/acceptance-gitconfig", "--mount", mount_to(git_ignore, "/tmp/acceptance-gitconfig/git/ignore", True)]
    args += [
        "--workdir", str(source), go_image(), "python3", "-B", "-I",
        str(source / "scripts" / "acceptance-parallel-runner.py"),
        "--workers", str(workers), "--run-id", run_id, "--run-directory", str(run_directory), "--source", str(source),
        *selection,
    ]
    return args, cidfile, name


def cleanup_coordinator(cidfile: pathlib.Path, name: str, run_id: str) -> None:
    container_id = ""
    try:
        if cidfile.is_file():
            container_id = cidfile.read_text(encoding="ascii").strip()
            if not CONTAINER_ID.fullmatch(container_id):
                raise CoordinatorError("coordinator cidfile is invalid; no same-name resource will be deleted")
        if container_id:
            result = execute(["docker", "container", "inspect", container_id], check=False)
            if result.returncode == 0:
                inspected = json.loads(result.stdout)
                labels = inspected[0].get("Config", {}).get("Labels") or {}
                if inspected[0].get("Id") != container_id or labels.get(COORDINATOR_LABEL) != run_id:
                    raise CoordinatorError("coordinator ownership differs from its cidfile")
                execute(["docker", "rm", "--force", container_id], check=False)
            elif "no such object" not in result.stderr.lower() and "no such container" not in result.stderr.lower():
                raise CoordinatorError("cannot inspect coordinator cleanup state")
            if not container_absent(container_id):
                raise CoordinatorError("coordinator container remains after exact-ID cleanup")
        elif not container_absent(name):
            raise CoordinatorError("an unowned same-name coordinator remains")
    finally:
        cidfile.unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workers", type=int, default=1)
    selection = parser.add_mutually_exclusive_group(required=True)
    selection.add_argument("--shard", action="append")
    selection.add_argument("--all-shards", action="store_true")
    parser.add_argument("--run-id", default=uuid.uuid4().hex)
    parser.add_argument("--run-directory")
    parser.add_argument("--source", default=str(ROOT))
    args = parser.parse_args()
    run_directory = pathlib.Path(args.run_directory).expanduser().absolute() if args.run_directory else pathlib.Path.home() / ".cache" / "moni-aigate-acceptance" / args.run_id
    selected = ["--all-shards"] if args.all_shards else [item for shard in args.shard for item in ("--shard", shard)]
    cidfile = None
    name = f"moni-040-coordinator-{args.run_id}"
    status = 1
    try:
        if run_directory.exists() or run_directory.is_symlink():
            raise CoordinatorError("the coordinator run directory must be new")
        run_directory.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        command, cidfile, name = coordinator_command(pathlib.Path(args.source), run_directory, args.run_id, args.workers, selected)
        if cidfile.exists() or not container_absent(name):
            raise CoordinatorError("coordinator name/cidfile already exists; it will not be adopted or removed")
        status = subprocess.run(command).returncode
    except (CoordinatorError, OSError, KeyError, ValueError, UnicodeError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        status = 1
    finally:
        if cidfile is not None:
            try:
                cleanup_coordinator(cidfile, name, args.run_id)
            except (CoordinatorError, OSError, KeyError, ValueError, UnicodeError, subprocess.CalledProcessError) as error:
                print(f"FAIL: coordinator cleanup: {error}", file=sys.stderr)
                status = 1
    return status


if __name__ == "__main__":
    raise SystemExit(main())

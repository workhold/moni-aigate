# 040 PR-C 隔离容器 runner

`acceptance-parallel-runner.py` 复用 PR-A 的完整分片计划和精确 argv，在每片独立 detached worktree 中执行 Go race 测试。每个逻辑片使用一个新的 PostgreSQL 16 cluster、named volume 和 network namespace；identity 的四个测试进程在同一所属片内顺序执行，其他片不共享数据库。

本阶段只允许 `workers=1`。`--workers 2` 会在预检前拒绝，不能用于双 cluster 互见证明或并行验收。单片 canary 不是完整 029，也不生成可复用 v2 manifest。

## 隔离与证据边界

- PostgreSQL 以 `--network none` 和 `127.0.0.1:55439/moni_test` 启动，不发布宿主端口；Go runner 以 `--network container:<postgres-id>` 加入同一 netns，actual-main 及其子进程因此留在该片边界内。
- runner 直接执行 PR-A `part.args`，保留 `-json -race -p 1 -count=1 -timeout=10m` 和原 `-run`；`GOMAXPROCS=2`，测试时 `GOPROXY=off`。模块只预取到该片独立缓存，`-count=1` 禁止复用测试结果。
- PostgreSQL 保持 test-only loopback trust、Asia/Shanghai、200 连接以及 fsync/synchronous_commit/full_page_writes=on。other 和 billing 分别获得规格要求的独立 DSN opt-in，其他研究/诊断开关不从宿主继承。
- 私有随机密码只写入运行目录的 `private/`（0700，env 文件 0600），通过 Docker `--env-file` 读取；密码不进入测试 DSN、原始日志、runner report 或 v2 manifest，并在 attempt 结束时删除。
- 原始 `go-tests-<part>.jsonl` 使用 PR-B 的 run_id/command/started_at/events/completed_at/exit_status 框架，并由同一个 PR-B parser 校验终态和经审查的 SKIP 基线。runner report 另行保存脱敏数据库身份、资源采样和清理证明。
- 任何分片、合并或清理失败都不会生成可复用 manifest。`fail-fast=false` 只用于继续可安全执行的后续片/identity part，不重试测试或拼接旧 PASS。
- 清理只针对本 run_id 创建的精确容器、volume 和 worktree，不执行 prune，不触碰既有环境。资源名包含完整 run_id；Docker cidfile 和完整 run/shard 标签共同证明所有权，无本场 cidfile 的同名容器绝不删除。运行轮询仅把空/半写 cidfile 视为 pending，进程退出后仍严格要求完整 64 位 ID。只有容器确为 `No such` 才算消失，Docker daemon 错误不能冒充清理成功。
- `netns_removed` 不由容器退出推断：runner 保存 PostgreSQL `SandboxKey`，删除容器后用固定 Go 镜像的只读宿主 netns 挂载和 `os.path.lexists` 验证该路径实际消失。路径格式不符、仍存在或检查失败都无可复用证据。

镜像固定在 `acceptance-runner-images.json` 的 repository digest。runner 不自动拉取或改写镜像引用；镜像缺失是预检失败。

## 单片 canary

以下命令显式选择专用验收 Docker context。先确认宿主 55439 未被其他环境占用，再拉取两个精确引用：

```bash
DOCKER_CONTEXT=colima-moni-acceptance docker pull docker.io/library/golang:1.26-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0
```

```bash
DOCKER_CONTEXT=colima-moni-acceptance docker pull docker.io/library/postgres:16-alpine@sha256:1d5b02d3a9941fbd52437db4107f8c52d6297dac553ca1e5c299ebb3756b4857
```

在干净 HEAD 上执行一个分片：

```bash
DOCKER_CONTEXT=colima-moni-acceptance PYTHONDONTWRITEBYTECODE=1 python3 -B -I scripts/acceptance-parallel-runner.py --workers 1 --shard attempt-0
```

每次自动生成新的 32 位 run_id 和 `~/.cache/moni-aigate-acceptance/<run_id>/`，也可用 `--run-directory` 指定一个尚不存在且位于仓库外的目录。成功输出位于 `evidence/runner-report.json`；`scope=workers_1_canary`、`reusable=false`、`complete_029=false`、`workers_2_proven=false` 是固定边界。报告同时给出宿主监听前后摘要、真实 loopback 数据库身份、精确退出状态、资源样本以及容器/volume/netns 清理结果。

重复 `--shard` 可选择多个片；2–13 片只标记为 `workers_1_partial`，不会冒充 full Go race。`--all-shards` 仅表示 workers=1 顺序执行全部 14 个逻辑片。只有 17 个 part 全部成功、清理完成且 PR-B 对 100 个 current-MVP 与 77 个矩阵唯一测试的合并检查通过时，才生成 `go-race-evidence-v2.json`；即使成功，也仍不是包含串行尾部、浏览器和独立生命周期套件的完整 029。PR-C 的实际 canary 不以此完整路径代替单片验证。

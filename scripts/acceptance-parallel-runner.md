# 040 隔离容器 runner 与 workers=2 候选

`acceptance-parallel-runner.py` 复用 PR-A 的完整分片计划和精确 argv，在每片独立 detached worktree 中执行 Go race 测试。每个逻辑片使用一个新的 PostgreSQL 16 cluster、named volume 和 network namespace；identity 的四个测试进程在同一所属片内顺序执行，其他片不共享数据库。

**workers=2 当前是默认关闭的候选，不是已验收能力。** 多轮完整运行均出现业务断言失败，没有全绿 manifest 或连续两轮完整 029。实际失败和墙钟见[候选与未通过验收记录](../docs/verification/acceptance-workers-2.md)。未显式选择并行变量时，029 继续使用 serial v1。

## 隔离与证据边界

- PostgreSQL 以 `--network none` 和 `127.0.0.1:55439/moni_test` 启动，不发布宿主端口；Go runner 以 `--network container:<postgres-id>` 加入同一 netns，actual-main 及其子进程因此留在该片边界内。
- runner 直接执行 PR-A `part.args`，保留 `-json -race -p 1 -count=1 -timeout=10m` 和原 `-run`；`GOMAXPROCS=2`，测试时 `GOPROXY=off`。`-count=1` 禁止复用测试结果。
- 每片先以 `git clone --bare --no-hardlinks` 创建无 alternates 的独立 Git common，再执行 `git worktree add --detach`。nested-worktree 测试只能写本片副本；主仓和其他片的 Git 元数据不可见。
- PR-A `-list` 发现阶段已编译全部包。模块缓存和 Go content-addressed build cache 在同一 attempt 内共享，模块缓存对测试容器只读；worktree、Git common、HOME、GOPATH、临时目录、数据库和证据仍按片隔离。只复用构建材料，不复用测试终态。
- PostgreSQL 保持 test-only loopback trust、Asia/Shanghai、200 连接以及 fsync/synchronous_commit/full_page_writes=on。other 和 billing 分别获得规格要求的独立 DSN opt-in，其他研究/诊断开关不从宿主继承。
- 首个 workers=2 并发对先完成双活动 cluster 证明：不同 system identifier、同名一次性 owner/grantee/default ACL 互不影响、A 持锁时 B 在同一会话取得并释放同值锁。探针前后逐字比较 `moni_test` default ACL，临时 role/table/lock 全部清理。
- 原始 `go-tests-<part>.jsonl` 使用 PR-B 的 run_id/command/started_at/events/completed_at/exit_status 框架。v2 另校验真实双 part 重叠、最多两个活动 part、identity 不重叠和 14 个数据库身份。
- 任何分片、合并或清理失败都不生成可复用 manifest。`fail-fast=false` 只用于继续可安全执行的后续片/identity part，不重试测试或拼接旧 PASS；identity 前一 runner 清理不明时停止本片后续 part。
- 清理只针对本 run_id 创建的精确容器、volume、Git common 和 worktree，不执行 prune。cidfile 和完整 run/shard 标签共同证明所有权，无本场 cidfile 的同名容器绝不删除。PostgreSQL 终态/OOM、两端资源样本和协调器 cidfile 也失败关闭。
- `netns_removed` 通过固定 Go 镜像的 host-netns helper、只读宿主 netns 挂载和 `os.path.lexists` 验证；helper 自身使用 host netns，不再创建未核验的 none sandbox。

镜像固定在 `acceptance-runner-images.json` 的 repository digest。runner 不自动拉取或改写镜像引用；镜像缺失是预检失败。workers=2 要求至少 4 vCPU 和 8 GiB VM（至少 7.5 GiB 内存对 Docker 可见），workers>=3 一律拒绝。

## VM 协调器

`acceptance-parallel-coordinator.py` 在固定 Go 镜像中运行 runner，使用专用 Linux Docker VM 的 host netns、VM Docker CLI/socket 和真实宿主监听表。外层 `DOCKER_CONTEXT` 选择专用 VM；内层固定 `default` 并绑定挂载 socket，不依赖容器内 context 配置。协调器以唯一 run_id 命名，`--rm` 后仍要求完整 cidfile 并按精确 ID 核验清理。

```bash
DOCKER_CONTEXT=colima-moni-acceptance docker pull docker.io/library/golang:1.26-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0
```

```bash
DOCKER_CONTEXT=colima-moni-acceptance docker pull docker.io/library/postgres:16-alpine@sha256:1d5b02d3a9941fbd52437db4107f8c52d6297dac553ca1e5c299ebb3756b4857
```

workers=1 单片 canary：

```bash
DOCKER_CONTEXT=colima-moni-acceptance PYTHONDONTWRITEBYTECODE=1 python3 -B -I scripts/acceptance-parallel-coordinator.py --workers 1 --shard attempt-0
```

workers=2 必须一次执行全部 14 片，不能选择 partial：

```bash
DOCKER_CONTEXT=colima-moni-acceptance PYTHONDONTWRITEBYTECODE=1 python3 -B -I scripts/acceptance-parallel-coordinator.py --workers 2 --all-shards
```

每次自动生成新的 32 位 run_id 和 `~/.cache/moni-aigate-acceptance/<run_id>/`，也可用 `--run-directory` 指定一个尚不存在且位于仓库外的目录。`evidence/runner-report.json` 保存 scope、逐片/part/合并/整段墙钟、双库证明、资源样本、宿主监听和清理。单片 workers=1 不生成 manifest；workers=2 只有全部 17 part、100/77 合并和清理成功才生成 `go-race-evidence-v2.json`，且仍不等于完整 029。

内部合并可以复用预检生成的 `source_override=(plan,other_tests)`，但必须逐字段匹配 evidence plan；这避免重复 `fresh_source`，不放宽任何测试、SKIP、身份或清理检查。外部 standalone v2 仍重新发现源码。

## 029 显式候选路径

完整 029 由 `phase1-acceptance.py` 生成本轮 run_id 并调用协调器。必须提供新的仓库外 artifact 和 summary 输出，防止前一轮生成文件阻断下一轮干净 HEAD：

```bash
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
DOCKER_CONTEXT=colima-moni-acceptance \
PHASE1_ACCEPTANCE_PARALLEL_WORKERS=2 \
PHASE1_ACCEPTANCE_PARALLEL_RUN_DIRECTORY='/Users/vchive/.cache/moni-aigate-acceptance/<new-run>/parallel' \
scripts/phase1-acceptance.sh --execute \
  --browser-evidence '<same-commit-browser-evidence.json>' \
  --artifacts '/Users/vchive/.cache/moni-aigate-acceptance/<new-run>/raw' \
  --output '/Users/vchive/.cache/moni-aigate-acceptance/<new-run>/summary'
```

还需要按[029 执行手册](../docs/verification/phase1-acceptance-runner.md)提供隔离 preview、浏览器证据和部署环境。runner 失败会阻止使用旧 manifest 继续拼接串行尾部。workers=2 的外部 manifest 路径也必须包含同 attempt 的 `runner-report.json` 与 `dual-cluster-proof.json`；workers=1 的既有 v2 诊断行为保留。

**去重前第一轮完整 029 已达到 41/41，但整单未达提速目标；默认 serial v1 和 standalone current-MVP 均未改变。**

在有效 runner report/proof 下，phase1 使用 `verify_current_mvp_attested.py` 复用 runner 内部严格合并结果，不再单独执行矩阵合并或九个 current-MVP 的重复源码发现。attested 复用必须同时提供 `VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED=1` 和 `VERIFY_CURRENT_MVP_RUNNER_REPORT`，并校验 manifest SHA-256、run_id/commit/tree、双库证明与清理；其它 v2 和 serial v1 路径保持原检查。

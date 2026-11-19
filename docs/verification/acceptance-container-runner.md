# 040 PR-C 隔离容器 runner 与 workers=1 canary 验证记录

对应[040 设计](../../specs/040-acceptance-parallelism/spec.md)与[实施计划](../../specs/040-acceptance-parallelism/plan.md)。本片只实现 Go 分片测试的容器生命周期和单片 canary，不改变 029 默认串行 runner，也不构成完整 029、workers=2、真实供应商或生产验收。

## 实现与证据边界

- `acceptance-parallel-runner.py` 直接复用 PR-A `part.args`：`-json -race -p 1 -count=1 -timeout=10m` 和原 `-run` 不变；GOMAXPROCS=2，workers 只接受 1，片间和 identity part 间 `fail-fast=false`，没有测试重试。
- 每片从同一 commit/tree 创建独立 detached worktree，模块预取后以及每个测试进程启动前重新检查 HEAD/tree/status。other 保留单次 JSON list 发现，billing/other 只获得 CI 合同中的对应 DSN opt-in。
- 每个逻辑片使用新的 PostgreSQL 16 cluster、volume 和 network namespace。PostgreSQL 为 `--network none`，Go runner 以 `--network container:<完整容器 ID>` 加入，片内仍为 `127.0.0.1:55439/moni_test`；不发布宿主端口，actual-main 子进程不能回落到宿主或其他片。
- PostgreSQL 保留 loopback trust、Asia/Shanghai、200 连接和 fsync/synchronous_commit/full_page_writes=on。随机密码只写入 0700 私有目录中的 0600 env 文件，不进入 DSN、日志、报告或 manifest，并在 attempt 结束时删除。
- Go/PostgreSQL 镜像由 `acceptance-runner-images.json` 固定 repository digest。测试时 `GOPROXY=off`，模块缓存和临时目录按片隔离，`-count=1` 禁止测试结果缓存。
- 原始日志保留 PR-B 的 run_id/command/started_at/events/completed_at/exit_status 框架，由同一 `check-acceptance-evidence-v2.py` parser 验证终态和 211 项非必需 SKIP 基线。单片 canary 不生成 v2 manifest，`reusable=false`；2–13 片也只标记 partial。只有 14 片/17 part 全部成功、清理完成且 100/77 合并检查通过时才可生成 v2 manifest，本次没有执行该完整路径。
- 资源名包含完整 run_id。PostgreSQL/runner 都以 cidfile、完整容器 ID 和 run/shard 标签确认本场所有权；没有本场 cidfile 的同名资源绝不删除。`netns_removed` 还使用固定镜像、只读宿主 netns 挂载和 `os.path.lexists` 验证原 SandboxKey 实际消失，daemon/路径错误均失败关闭。

## 专项与负例

`python3 -B -I scripts/acceptance_parallel_runner_test.py` 共 21 项，覆盖固定 digest、私有目录与 DSN 作用域、精确 argv、无端口发布、identity 顺序与失败后继续、非零/OOM、原始 v2 日志和 SKIP 基线、完整 manifest 门控、合并失败删除 manifest、宿主监听变化、Linux listener parser、完整 run_id、cidfile 缺失时零删除、empty→valid cidfile 启动与退出后严格校验、volume 大小写 not-found、容器注册采样竞态、真实 SandboxKey 检查以及 partial scope。

独立复审首先指出 12 位资源名碰撞、netns 仅靠容器退出推断和 partial 误标 full 三项问题；`24bd633` 已按上述合同修复并增加对应负例。随后复审发现 cidfile 可能先为空再写入完整 ID，`80ed12d` 仅在进程运行期间允许 pending、退出后仍严格校验，最终聚焦复审确认没有剩余具体阻断。此前各次真实执行只用于对应提交的验证，最终只采用下述干净 HEAD、新 run_id 的完整 attempt。

## 最终真实 workers=1 canary

- commit：`83eeb73d9aa3e00a707bd05116e96bbbb61b2f8f`；tree：`bbb7b227261f89bd72e1fac301cdd1e970c9fd8c`（实现代码来自已复审的 `80ed12d`，该 HEAD 只增加架构与验证文档）。
- run_id：`8ae769e7aa9243229e48adbf9068d342`；唯一选择 `attempt-0`，workers=1。
- 时间：2026-11-19T05:03:05.716142+00:00 至 05:03:57.484501+00:00，约 51.77 秒；这只是单片实测，不是完整 029 提速数据。
- 环境：专用 `moni-acceptance` Colima VM。协调器使用固定 Go 1.26.11 镜像并观察 VM host netns；实际 PostgreSQL/Go 测试容器仍使用私有 none/container netns。既有 macOS 55439 PostgreSQL 和 VM 18084 预览均未停止、重启或修改。
- 真实 loopback readiness 验证地址、端口、数据库、用户、时区、连接数和三项持久化设置；该 cluster 的 `pg_system_identifier` 为 `7709743765049577512`。
- `go-tests-attempt-0.jsonl` SHA-256：`ad381d99cd488e03e25271b32b18df680d2cd024527b76bab7b8b34883ef8046`。独立重跑 PR-B parser 接受精确 argv、run_id、框架和终态：6 个顶层 PASS、12 个经审查基线允许的顶层 SKIP、0 FAIL。此片不承担 100/77 必需项合并，不能据此宣称完整必需清单通过。
- 8 组资源采样均保留，PostgreSQL/runner 的 NetIO 均为 `0B / 0B`；OOMKilled=false，exit_status=0。
- VM host 监听前后均为 7 项，SHA-256 均为 `9a73a512a483366e01935541680ada3c6720ddb12a4c491566027fb586010a0d`，`zero_new=true`。
- runner report 的 containers/volume/netns 清理均为 true。随后独立检查该 run_id 的容器 label、volume label、同名容器、协调器、detached worktree 和 private 目录均不存在，源码 HEAD/tree 不变且 status 为空。
- `runner-report.json` 明确为 `scope=workers_1_canary`、`manifest=null`、`reusable=false`、`complete_029=false`、`workers_2_proven=false`。

更早的两次 attempt 分别在 Git 元数据环境和宿主缺少 race C 编译器的预检阶段停止，没有启动测试；另一次真实测试 exit 0 因资源采样和 volume 清理证明缺陷被判 FAIL。它们均未与本次结果拼接，也未通过选择性 retry 生成可复用 manifest。

## 未执行与推广边界

本次没有启动 workers=2，没有进行双 cluster 同名 role/default ACL/advisory lock 互见测试，没有运行 14 片完整 Go race、完整 029、浏览器、Compose、支付、独立 identity/mail 或串行尾部，也没有切换默认 runner。现有 macOS 55439 属于其他环境，因此未在其上运行 `make verify-mvp` 或完整商业验收。workers=2 与默认化仍属于后续独立阶段。

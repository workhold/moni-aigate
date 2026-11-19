# 040：多独立 PostgreSQL 的验收并行化设计

2026-11-18；状态：分阶段实施，PR-A/PR-B/PR-C 已实现，PR-D 未实施。本文只描述 029/039 测试回归与证据编排，不是 aigate 的业务多库、生产分片、读写分离或 HA 方案，不改变产品请求链路、迁移、钱包、结算、ACL、超时或恢复语义。

## 1. 目标与基线

最近完整实测（提交 `23a22d39bdd1d03643a1d56f0f55438828f1f723`）：

- 整单 2727.018 秒；go-race 2140.743 秒，是主要优化对象。
- current-MVP 已通过 039 去重降到 82.124 秒，100 个必需 Go 测试不再重复执行。
- `other` 约 898.791 秒，identity 约 318.239 秒，billing 约 379.355 秒，server 约 517.597 秒；简单提高同库 `-p` 不是安全优化。

目标是复用 CI 已有的独立数据库和顶层分片思想，让本地完整 029 的 Go race 阶段墙钟接近最长分片而非全部分片之和，同时保持全部覆盖和证据强度。历史调度估算约 27.7 分钟整单，但这不是实测承诺；只有同提交实测可以宣布收益。

## 2. 不变量与非目标

1. 41 个验收 ID、82 个矩阵绑定（当前 77 个唯一 package:Test）、current-MVP 的 100 个唯一必需测试、所有断言、race、`-count=1` 和专用 PostgreSQL 要求不变。
2. 不删除测试、增加 SKIP、放宽业务/锁/首事件/自然 lease/进程超时，不关 fsync，不以 OOM、连接或锁错误换速度。
3. 不在同一 PostgreSQL cluster 用多个 database/schema 规避全局角色、默认 ACL、advisory lock 或连接状态；历史上同库并发已出现 42501。
4. 独立 identity/mail real-main 套件、自然 lease、支付、Compose、备份恢复、浏览器和 mock deployment 保持串行，不复用其独立证据。`internal/server` 包内 actual-main 测试只有在每个分片都拥有独立 cluster/netns、动态 loopback 端口和子进程边界时才允许分片；其 attempt、SIGTERM/SIGKILL、恢复与 no-replay 断言不变，绝不使用另一个分片的数据库或进程。
5. standalone `verify-current-mvp.sh` 未传 manifest 时继续原 inventory 和实跑；039 的去重收益不得回退。
6. 不引入产品运行时依赖、第二事实源、业务配置、供应商资格或生产认证。

## 3. 选择：14 个逻辑分片，最多两个活动 worker

复用 `.github/workflows/ci.yml` 的逻辑分片，不另造人工 pure 白名单：

- `billing-0..3`：`internal/billing` 顶层 Test/Fuzz/Example 的 C 序轮转；
- `server-0..3`：`internal/server` 同样规则；
- `attempt-0..3`：`internal/procurementattemptbirth` 同样规则；
- `identity`：内部四个顺序进程，属于同一逻辑片和数据库；
- `other`：其余全部包，包内 `-p 1` 串行且不加测试过滤。

约束：

- CI 与本地必须调用同一个分片实现；`go list ./...` 互斥、穷尽、无重复，新包自动进入 `other`。
- 分片发现使用同一 race 配置，父测试及全部子测试不拆；锚定 `-run` 表达式不允许无效匹配。
- 无测试 package 的 compile 终态与有测试 package 的顶层覆盖分别核验，不能用“没有测试”掩盖漏跑。
- 14 是逻辑分片数，不是并发数。本机默认 `workers=2`，最多两个 Go race 进程、两个 PostgreSQL 和四个活动容器；GOMAXPROCS=2。
- `workers>=3` 只能在后续独立资源评估后开启；不照搬 14 个并发 runner。

## 4. 物理隔离拓扑

每个逻辑分片创建一对一次性容器：一个全新 PostgreSQL 16 cluster 和一个 Go race runner。runner 加入该 PostgreSQL 的 network namespace。

- 片内两端仍看到 `127.0.0.1:55439/moni_test`，保留现有 DSN、loopback 和 `inet_server_addr` 断言；不发布新的宿主监听端口。
- 每个分片都有独立 cluster、volume、postmaster、角色目录、默认 ACL、advisory lock、连接数和 `pg_system_identifier`；14 个 identifier 必须不同。只有 identity 的四个顺序进程可共享其所属片实例。
- 每个 cluster 使用 test-only loopback trust、Asia/Shanghai、max_connections=200、fsync/synchronous_commit/full_page_writes=on；凭据只在私有运行目录，不进入仓库、日志或证据。
- 每个 runner 使用相同 commit/tree 的**独立 detached git worktree**、独立临时目录和仓库外 artifact；固定 Go/PostgreSQL 镜像摘要和工具链。协调器先记录 source_commit/source_tree，再执行 `git worktree add --detach <path> <commit>`，逐轮验证 `rev-parse HEAD`、`rev-parse HEAD^{tree}` 和启动时 `status --porcelain` 为空，确保现有 029/039 的 Git 门禁可原样运行。actual-main 及其 worker/恢复子进程也必须留在该 runner 所属 netns 和数据库边界内，不能回落到宿主或其他分片实例。缓存可以复用构建材料，不能复用测试结果。
- 不接触原 029 数据库、其他预览、体验实例或跨 network namespace 路由。宿主端口冲突、镜像缺失、资源不足或 identity 重复都属于预检失败。

最终串行阶段开始前，必须先完成分片证据合并和容器/volume/netns 清理证明。

## 5. 运行状态机

`draft → preflight → workers_1_canary → workers_2_candidate → parallel_default`

- `preflight`：干净 HEAD、run ID、完整分区、镜像/资源/数据库身份和 SKIP 基线就绪；失败不得启动测试。
- `workers_1_canary`：只验证单分片的容器工具链、真实 loopback、无新增宿主监听和清理，不承担需要两个活动 cluster 的并发互见证明，也不作为最终默认拓扑。
- `workers_2_candidate`：在两个活动 cluster 中分别创建同名 role/default ACL，并在 A 持有 advisory lock 时验证 B 的同名锁可以独立取得，证明互不影响；同一干净候选至少连续两次完整 029 全绿后才能推广。
- `parallel_default`：只在两轮结果稳定、无 OOM/超时/残留后启用。

失败规则：

- 分片间 `fail-fast=false`，可安全完成的分片用于完整诊断，但任何分片、合并或清理失败都不得生成 PASS/可复用 manifest。
- 不为测试、包、分片或 worker 自动重试，不用只重跑失败片与旧 PASS 拼单。
- 操作者重试必须是新 run_id、全新工作副本/cluster/volume/artifact 的完整 41 项；serial 或 workers=1 也只能是下一场完整 attempt。
- 超时、12501/42501、exit 124/137、OOM、磁盘、截断、数据库身份或清理失败均不可通过删测、加时或选择性 retry 掩盖。

## 6. v2 证据模型

PR-B 当前实现 `check-acceptance-evidence-v2.py`：支持 schema v2 的显式 opt-in，但尚未启动多库 runner。v1 和 standalone 行为保持原样；029 只有显式设置 `PHASE1_ACCEPTANCE_GO_EVIDENCE_V2` 才使用 v2 合并结果，未设置时仍执行原串行 go-race。

新增 `schema_version=2` 的索引，保留每个分片不可变原始 JSONL，不把多个带框架的日志拼成伪单命令 `go-race.jsonl`。

索引至少包含：

- run_id、created_at、HEAD commit/tree、source_clean、scheduler_version、workers、镜像摘要；
- 完整分片计划及其 digest、每片精确 argv、脱敏数据库身份、原始日志开始/结束、exit_status；
- 每片原始日志相对路径和 SHA-256、pg_system_identifier、资源终态和清理证明；
- 039 的一小时新鲜度、未来五分钟和干净源码检查；backup 生成文件的精确豁免保持。

合并器必须验证：

1. 恰好 14 个预期分片；checker 在同一 HEAD 的干净 detached worktree 重新生成 PR-A 计划并逐字段比对，包全集精确覆盖、类级顶层发现到 part 的分配精确覆盖。other 以单次 `go test -json -race -count=1 -list .` 发现各包清单，所有命令 exit_status=0。
2. 缺失/额外/重复分配、跨片或额外 PASS、package/test FAIL、panic、必需重复 PASS、损坏/截断、错误摘要/数据库身份、实例复用、OOM 或清理失败全部拒绝。每个发现测试必须恰有一个终态，每个 package 必须恰有一个 compile 终态；有发现测试的 package 必须 PASS，无测试文件包的 package-level SKIP 不计入测试 SKIP 基线。
3. 原始日志按 `run_id/command/started_at → JSON events → completed_at/exit_status=0` 框架校验；command 必须等于计划 argv，run_id 必须属于本次 attempt，started/completed 必须处于本次新鲜度窗口，exit 后任何非空记录都拒绝。
4. 矩阵的 77 个唯一测试各执行一次，再让 82 个绑定解析到其 PASS；不同 ID 复用同一绑定不算重复执行。
5. current-MVP 的 100 个唯一必需测试各有一个精确顶层 PASS，九个复用组仍输出 reused，总计 100/100。
6. v1/v2、跨 run/commit/tree/attempt 证据不得混用；standalone 未传 manifest 的行为不变。

### SKIP 基线

“0 SKIP”始终指必需项零 SKIP，不代表全日志没有环境型 SKIP。建立版本化、经 SDD 审查的 `scripts/acceptance-skip-baseline.json`，以排序唯一的 `package:Test` 多重集记录允许的非必需 SKIP；manifest 只能复制该仓库基线，不能自行批准新增项。每个基线测试必须恰有一个 SKIP 且没有 PASS，其他发现测试必须恰有一个 PASS；必需测试出现 SKIP 无论是否在基线中都拒绝。真实 `go test -json` 终态没有 `Reason` 字段，因此基线的 reason 固定为空，只证明 package:Test/数量一致，不宣称能检测跳过原因变化；需要审查原因时必须另行保存并复核 Output 证据。推广、镜像/工具链变化或分片合同变化时再做同提交 serial/parallel 全量 SKIP 对照，避免每天先跑一次完整串行基线抵消优化。

当前基线对应 CI-compatible 的本地 029 环境：PR-C 必须像 CI 一样为对应分片显式提供独立的 `PROCUREMENT_IDENTITY_DATABASE_URL` 与 `PROCUREMENT_ATTEMPT_RULES_DATABASE_URL`，其他研究/诊断 opt-in 保持关闭。基线的 211 项来自同一源码树 `ef1ea0feac66506956f08348e49229c74a6d83db` 的两轮成功 CI 原始分片日志（PR head `a927404b9a887d3e98b5ef2afcfd968ca712de94`；run `44338064958` 的 pull_request merge checkout 与 run `44337906066` 的 push checkout）。每轮内 14 个分片计划一致，两轮均有 2298 个唯一顶层 PASS、211 个唯一顶层 SKIP、0 FAIL，SKIP 集合摘要一致。该基线不是生产或供应商资格，也不允许任何 100/77 必需项 SKIP。

## 7. 分阶段实施

具体 PR 边界见[实施计划](plan.md)。

1. **共享分片器**：抽取 CI/本地共用 CLI，先证明新 CI 14/14 与旧名单完全等价；补新包自动 other、父/子测试不拆和发现负例。
2. **v2 合并器与合成测试**：不接并行，覆盖缺片、重复片、跨 attempt、错误 digest/数据库身份、非零退出、重复 JSON key、必需 FAIL/SKIP、截断和清理失败；`phase1-acceptance.test.sh` 在 CI other 任务中执行 v1/v2 与矩阵测试。
3. **workers=1 canary**：验证真实 loopback、单分片工具链和零新增宿主监听；不要求单 cluster 完成互见证明。
4. **workers=2 候选**：在两个活动 cluster 中完成同名角色/default ACL/advisory lock 互不影响证明。每轮必须使用新的 detached git worktree、run_id、artifact 目录和全套数据库/容器资源，禁止默认 artifact 路径或前一轮生成文档使下一轮 preflight 失败；每轮保存各自证据后才可进入下一轮。连续两轮完整 029 均须 41/41、0 FAIL、0 必需 SKIP、100/100 复用且无 OOM/超时/残留。
5. **默认化**：独立复核后才将 029 默认切到 v2；保留 serial v1 作为下一场完整诊断/回退，而非当前运行内补片。拆 other 或 workers>=3 另立 SDD。

## 8. 验收与交付

- 分片/合并器单元测试和 CI 等价证据完整，所有负例失败关闭。
- 同提交 serial/parallel 的唯一顶层结果和 SKIP multiset 一致；必需绑定没有缺失、重复或无效 `-run`。
- 最终 029 连续两轮全绿，复用计数、浏览器和独立生命周期证据齐全。
- 每次记录实测墙钟，不把调度估算当提速承诺；出现锁/资源型超时即停止推广。
- 同步 HLD/LLD、统一架构正文、changes 和 manifest；架构 reviewed refresh/check、make check、CI 和最终完整 029 均通过。

## 9. 不采用的替代方案

- 同库直接 `-p 4`：默认 ACL、角色和锁边界不允许。
- 四 lane 手工 pure 白名单：维护成本和本机资源压力较高，新包容易漏分或误分。
- 只移动少量 server 测试的两库试验：长期收益有限且仍需另一套分片/证据体系，只能作为一次性 canary。
- 本机直接 14 并发 runner：4 vCPU/8GiB 不具备资源依据。

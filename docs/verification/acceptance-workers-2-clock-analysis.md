# 040 PR-D 失败分析与 VM 时钟根因

本文补充[workers=2 候选与未通过验收记录](acceptance-workers-2.md)，只记录诊断，**不构成完整 workers=2、完整 029 或修复完成证据**。分析没有删除测试、增加 SKIP、修改业务断言/超时或默认启用 workers=2。

## 1. 八轮 workers=2 的精确失败

下表来自原始 JSONL，不把不同错误归并为“超时”。`peers=none` 表示失败时另一执行 part 已结束。

| run | part / 失败测试 | 原始断言（文件:行号/错误） | peers |
|---|---|---|---|
| `1f85cd00` | other `TestBuildPlanRejectsDirtyTree` | `main_test.go:211: git worktree add --detach ... HEAD: exit status 128` | none |
| `1f85cd00` | other `TestCurrentTreeMatchesLegacyPartitionArtifacts` | `main_test.go:127: git worktree add --detach ... HEAD: exit status 128` | none |
| `ad5a80b2` | server-1 `TestCostVectorApplicationRecoveryDoesNotRegenerate` | `cost_vector_application_integration_test.go:222: recovery did not release the expired unknown request {0 0 0 0} <nil>` | server-0 |
| `ad5a80b2` | server-1 `TestVideoTaskWorkerAndHTTPSubmitRaceAreIdempotent` | `task_submit_race_integration_test.go:52: losing HTTP submit returned 503` | server-2 |
| `ad5a80b2` | other `TestProcessorStaleSignedCapturePersistsWithoutACKAndCredits` | `processor_query_safety_test.go:80: false <nil>` | none |
| `ad5a80b2` | other `TestUnifiedTextMeteringCurrentPositiveGuardFacts` | `unified_text_metering_guard_current_postgres_test.go:128: count=0 want=2`；`:137: valid supplier quote was not persisted as known: unknown` | none |
| `e11ecc72` | billing-0 `TestPostgresVectorReserveWithoutScalarReads` | `vector_reserve_integration_test.go:113: vector admission changed price/domain ... 120 reserved ...` | billing-2 |
| `e11ecc72` | billing-1 `TestPostgresLifecycle` | `store_test.go:312: pure metering vectors were not stored or did not match their digests ... cannot scan NULL into *bool` | billing-0 |
| `e11ecc72` | other `TestProcessorQueryOriginalSignatureAndUnknownReuse` | `processor_test.go:194: false <nil>` | none |
| `4ec9d3ac` | server-0 `TestPeriodicBudgetHTTPRestrictedLifecycleAndOriginalReceipts` | `periodic_budget_integration_test.go:376: actual APP funds flow after original HTTP receipt (SQLSTATE 23514)` | server-1 |
| `4ec9d3ac` | other `TestApplicationAttestedBuilderActualRolesAndCleanup` | `application_test.go:126: actual application constructor context deadline exceeded` | none |
| `4ec9d3ac` | other `TestCommanderFaultPGRealDeadlockRetryBound` | `commander_fault_test.go:259: observe the exact backend fault boundary failed (*pgconn.errTimeout)` | none |
| `4ec9d3ac` | other `TestTakeoverDrainsDurablePriorWindow` | `instance_test.go:98: price_unavailable: no effective model price` | none |
| `ad38d2eb` | billing-1 `TestPostgresProcurementDisputedEvidenceApprovalBarrier` | `procurement_review_integration_test.go:24: cost_unknown: controlled procurement requires a complete quote` | billing-0 |
| `ad38d2eb` | other `TestProvisionedRequestClientIPIsImmutableMetadata` | `key_policy_integration_test.go:84: reserve metadata fixture request: price_unavailable: no effective model price` | none |
| `ec96f3c1` | billing-0 `TestPostgresCashRefundLifecycleAndReplay` | `cash_refund_integration_test.go:106: ERROR: wallet source facts do not match (SQLSTATE P0001)` | billing-1 |
| `b21ffe6f` | server-0 `TestCostVectorApplicationQuoteChain` | `cost_vector_application_integration_test.go:72: restricted vector request failed 503 ... no_available_channel` | server-1 |
| `b21ffe6f` | other `TestPostgresAlertConcurrentCommandsAndWorkerSlots` | `alerts_integration_test.go:550: 0 2 0 0 <nil>` | identity-2 |

初始只读 Git common 和双库探针实现错误是独立缺陷，已修复。其余失败位置跨轮变化，不能只靠选择性重试规避。

## 2. workers=1 完整控制与包/宿主对照

### 完整 workers=1 控制

`1d74d2b0a9224b81a1a4b9eb05ae77dc` 使用最终候选 `e1e80c3`、同一 runner/cache/物理 worktree、全新 cluster/volume/worktree/artifact：

- billing、server、attempt、identity 全部通过；other 失败，`manifest=null`，没有进入完整 029。
- `TestProcessorStaleSignedCapturePersistsWithoutACKAndCredits` 再次得到 `processor_query_safety_test.go:80: false <nil>`。
- 新增 `TestVerifierPostgresLegacyMembershipImportAndCatchupBaseline/ended_supplier_membership_then_exact_catchup`：`verifier_integration_test.go:346: ... SQLSTATE P0001: procurement reservation does not match its budget events`。
- runner `wall_seconds=3676.163`，`parallel_shards_seconds=3439.079`，`merge_seconds=0`。这只是诊断控制，不是提速结果。

### 单包和宿主对照

- 完整 `internal/payments`（`36b91ddd9f3046c8a8b4a7882e321fc9`）通过；完整 `internal/periodicbudget`（`be41669d247843f590c4bb6df2c15d76`）通过。它们不能与失败的完整 other 拼单。
- 宿主 Go1.27.3 的完整 other 76/76 包通过、0 FAIL；宿主 Go1.26.11 也 76/76 包通过、0 FAIL。两个失败目标在宿主均通过，Go 版本变量被排除。
- PostgreSQL16.15 容器对照 `ed63ca7015b94b0cbf0590e68432793c` 继续出现时间回拨，并复现同一个 stale-capture 失败；`wall_seconds=1495.046`，other FAIL、无 manifest，容器/volume/netns 清理为 true。数据库版本变量也不能解释根因。
- 容器控制的 periodicbudget 包耗时约 137 秒，宿主约 74–76 秒；但这不是资源耗尽的证明，失败时内存没有接近上限，且一半失败没有另一活动 part。

## 3. 共同条件与排除项

- 18 个 workers=2 顶层失败中，9 个有活动 peer，9 个没有 peer。`other` 在另一片结束后仍失败，workers=2 并发不是统一根因。
- 只有两个失败是显式 timeout/deadline，其余为报价、资金、生命周期、支付或告警断言。相关包没有 `t.Parallel`，包间仍 `-p 1`。
- 失败时 PostgreSQL/Go 内存没有接近 8 GiB 上限，未报告 OOMKilled。并发时常见较高 PG I/O，但不能据此推导 CPU/内存饱和。
- 宿主两个 Go 版本都通过，PG16.15 也复现，故不是 Go/PG 版本问题。
- 物理 worktree 与逻辑 worktree 都能出现失败；清理后的双库探针不是共同原因；没有证据支持跨 schema 数据或角色污染。

## 4. 决定性时钟证据

1. 控制 `go-tests-other.jsonl` 有 **13 次**相邻事件时间倒退；最新 workers=2 有 **17 次**，单次最大约 **1.44 秒**。例如 `16:22:46.093790` 的 run 后出现 `16:22:45.663608` 的 PASS，但 `Elapsed=0.14`。
2. 直接对 PostgreSQL `clock_timestamp()` 采样 **504 次**，出现多次约 **-1 秒**回拨，最小数据库增量约 **-1.047 秒**；macOS `time.monotonic()` 同期持续递增。原始样本在共享交接目录 `clock-monitor-pg16.15.jsonl`。
3. 内核日志反复出现 `Time jumped backwards, rotating`。`lima-guestagent` 每 10 秒记录 `SyncTime: system time synchronized with host`，漂移常约 1.2 秒；同时 `systemd-timesyncd` 又按外部 NTP 校正。两个时钟源反复校正 VM realtime，解释了不同测试、不同时间的非确定性失败。
4. PostgreSQL16.15 对照日志也继续出现回拨，排除“16.16 特有问题”。

### 对失败机制的解释

- **支付重试**：首次 backoff 是数据库时间 +1 秒，测试按 Go 单调时钟等待 1.1 秒。若 realtime 回拨超过 100ms，`next_attempt_at<=clock_timestamp()` 仍为 false，`ClaimQuery` 就返回 `ok=false, err=nil`。
- **成本审批顺序**：guard 按 `approved_at DESC,id DESC` 选最新提案。若第二次批准的 realtime 早于第一次，guard 可选择金额 40 而不是 25，正好触发“procurement reservation does not match its budget events”。
- **报价/成本**：新发布的 `effective_at` 若因回拨落在数据库当前时间之后，会暂不可得，形成 `price_unavailable`、`cost_unknown`、`no_available_channel`。
- **告警**：`next_evaluation_at` 若落在回拨后的未来，worker 会保持 idle，得到 `0 2 0 0 <nil>`。
- **其它生命周期/幂等断言**：lease、dispatch、recovery 和唯一结算同样依赖 DB 时间与状态边界，回拨可改变分支，而无需 CPU/OOM。

这些链接说明了共同机制，但不要求每一项都重新证明；没有改业务代码或测试来适应回拨。

## 5. 当前阻断与最小修复路径

拟议最小环境修复是：**仅在专用验收 VM 中停止重复的 `systemd-timesyncd`，保留 `lima-guestagent` 的宿主时间同步，先证明 realtime 单调，再启动新的完整 workers=2。**

执行该服务变更被当前权限策略拒绝（Remote Shell Writes），所以本文记录时没有完成修复或下一场 workers=2。没有通过其它命令绕过授权，也没有停止/修改预览容器。宿主机只有 34 GiB，已有三个 8 GiB VM，不能安全地再创建第四个 8 GiB VM。

在获得明确授权后，必须先采集单调性证据，再进行新 run_id 的完整 workers=2；只有连续两轮完整 029 达到 41/41、0 FAIL、0 必需 SKIP、100/100 current-MVP 复用、桌面/390px 浏览器和独立生命周期证据，才可讨论默认化与真实提速。当前默认仍是 serial v1。

授权后的当前状态：`systemd-timesyncd` 已在专用 VM 中禁用，`lima-guestagent` 保持 active；预览容器未停止或修改。`17dfe3b090dd428480f96de72d8d6c8f` 的 180 秒验证共采集 545 个 PostgreSQL 时钟样本，**0 次回拨**，容器/volume/netns 清理均为 true，`pg_system_identifier=7709945012910796839`。该结果只证明环境修复成立，尚未启动下一场 workers=2，不能替代完整验收。

# 008 财务写批次验证

2026-11-08，当前分支 `codex/initial-mvp`。

本片把 `postLocked` 的账本写入拆成纯 `journalPlan` 构建阶段。它在发出任何 PostgreSQL batch 之前完成以下检查：分录至少两项且总和为零、每个分录账户已经锁定、账户余额加法不溢出、客户 `AVAILABLE/HELD` 不会变为负数。写入顺序按账户 ID 固定，构建失败返回空计划，不发送 SQL，也不产生部分 journal、余额或流水。

2026-11-08 续片：funding reservation 与 settlement lot 的写集现在先用同一份纯投影校验余额、held/consumed 和 revision，再按原 allocation 顺序一次发送 `pgx.Batch`；每个 allocation 仍保持“关系行→lot event→余额 CAS”顺序，失败由外层财务事务整体回滚。`changeFundingLotTx` 继续保留给退款等单行路径，并复用同一投影函数。

证据：

- `go test -count=1 -race ./internal/billing -run '^TestBuildJournalPlan'` 通过，覆盖不平衡、未锁账户、溢出、余额不足、确定性和完整写集；
- `go test -count=1 -race ./internal/billing -run '^TestPostgresFinishTailWriteFailuresRollbackAndRetry$'` 通过，逐点注入 settlements、funding lot、Key、reservation/request、recovery、audit 和 outbox 写入故障，验证整笔回滚、恢复后同一结算重试和幂等重放；
- `go vet ./internal/billing` 通过；
- `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable go test -count=1 -race ./internal/billing -run 'TestPostgresAccountBatch|TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestWallet'` 通过。

008 已完成 journal 纯构建和一组 finish 尾写点故障矩阵，但整体仍未完成：还需要 Reserve/finish 全链路取消与恢复/SIGKILL、周期预算与超界 profile block 写点、完整并发预算与财务时钟边界、三对固定 binary 对照、最终嵌入预览和全量验收文档。局部通过不能替代这些出口。

本续片新增的 funding-lot 批量写入已由 55439 专库的生命周期、Reserve 回滚、finish 尾部十个写点故障/重试、财务边界和并发预占专项覆盖；它没有改变独立 COMMIT、锁序、来源分配或退款语义。

本轮又将超预占的 `billing_profile_blocks` 与 `billing.profile_blocked` audit 排入同一 PostgreSQL batch。profile 行或 audit 任一写点失败都会回滚结算、钱包、周期预算、来源 lot 和审计；重试只产生一笔 settlement/profile block，重复结算保持原 receipt。对应 `TestPostgresProfileBlockBatchRollsBackAndRetriesOnce` 与周期预算的超界专项通过 55439 race。

2026-11-08 周期预算续片：现行 024 fixture 的额外 channel 通过完整 route publication 建立，成本价通过 catalog operation 发布，Reserve 使用当前非零 route revision；整个 `internal/periodicbudget` race 包在 55439 专库通过（71.379s），覆盖 dispatch、采购累计/日限额、第二 attempt、unknown 更正、恢复、verifier 与 Commander 限额。billing 的 POSIX SIGKILL recovery 专项也在 55439 通过（14.933s），验证恢复不重发生成。冻结的 001–019/prefix fixture 保持原 migration 和自由版本合同，没有因当前 fixture 适配而升级；008 的固定 binary、最终预览、独立评审和其余总验收仍未完成。

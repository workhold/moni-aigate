# 008 财务写批次验证

2026-11-08，当前分支 `codex/initial-mvp`。

本片把 `postLocked` 的账本写入拆成纯 `journalPlan` 构建阶段。它在发出任何 PostgreSQL batch 之前完成以下检查：分录至少两项且总和为零、每个分录账户已经锁定、账户余额加法不溢出、客户 `AVAILABLE/HELD` 不会变为负数。写入顺序按账户 ID 固定，构建失败返回空计划，不发送 SQL，也不产生部分 journal、余额或流水。

证据：

- `go test -count=1 -race ./internal/billing -run '^TestBuildJournalPlan'` 通过，覆盖不平衡、未锁账户、溢出、余额不足、确定性和完整写集；
- `go vet ./internal/billing` 通过；
- `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable go test -count=1 -race ./internal/billing -run 'TestPostgresAccountBatch|TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestWallet'` 通过。

这只是 008 的第一片。纯 journal 构建已经落地，并额外拒绝同一 ledger account 以多个 kind 重复进入写集；008 仍未完成：还需要逐条尾写入故障回滚、Reserve/finish 全链路取消与恢复/SIGKILL、完整并发预算与财务时钟边界、三对固定 binary 对照、最终嵌入预览和全量验收文档。局部通过不能替代这些出口。

# 008 验收执行

2026-11-09 集成复核：`journalPlan` 已在 PostgreSQL batch 前纯核构建，统一拒绝不平衡、未锁账户、余额不足、int64 溢出及同一 ledger account 多 kind 写集；新增 finish 尾部十个写点的故障回滚、幂等重试和唯一结算专项。Reserve/finish 的 funding-lot 写集与超界 profile block/audit 也已进入同一事务 batch，并通过 55439 的失败回滚、重试和幂等专项。周期预算现行 024 fixture 已改为走完整 route/catalog 与真实 APP billing 入口，`internal/periodicbudget` race 包通过；billing 的 POSIX SIGKILL recovery 专项通过；固定 binary 运行器按三对 before/after 串行执行六次样本，报告中的两枚 binary SHA 和源码树摘要均不同，六次均通过 100 请求、100 结算、金额 2000、账本平衡和资源归零。运行器入口为 `scripts/financial-write-batch-qa`，报告为本机 `/tmp/moni-aigate-008-fixed-binary-run-final-v2/manifest.json`。集成复核确认 HLD/LLD 的锁序、时钟、独立提交和错误边界与实现一致，Compose 当前基线预览已通过 ready 和资源清理。以下八项已具备当前实现与专库证据，性能目标、正式供应商、生产部署和外部支付仍按规格后置。

2026-10-31，历史初始记录：功能优先，性能切片后移；当时待实施与执行，不能勾选。

- [x] 原事务/锁序/新快照/财务时钟与成功终态合同保留，独立评审无阻断。
- [x] journal纯构建拒绝不平衡、溢出、余额不足，无I/O或可发送的部分批次。
- [x] 预占创建+账户批次真实PG错误/取消/新余额快照通过。
- [x] Reserve/finish尾部逐写入故障完整回滚，同幂等/settlement可唯一成功。
- [x] 原撤权、并发预算、财务截止、超上界隔离、恢复与SIGKILL回归通过。
- [x] 全Go race、vet、嵌入构建通过。
- [x] 固定前后binary至少三对，无并行负载，原始结果和源码摘要完整，保留所有回退/异常。
- [x] HLD/LLD/验证记录与最终程序一致，本地预览ready且资源匹配。

50ms、持续goodput、目标资源及竞品认证不能随本切片视为通过。

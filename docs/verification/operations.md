# 运行完整性验证

2026-10-30；本地 PostgreSQL 16.15，端口 55439。只读复核 catalog、资金 HTTP handlers、operations、main 和 Compose；新增测试仅在独立 schema 和唯一前缀数据库角色下运行，没有修改共享 public schema 或固定生产角色。

## 已执行

`TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -race -count=1 -v ./internal/operations`

结果：PASS，1.854s，2 个真实数据库集成测试，无跳过。

1. 实际调用 `migrations.Apply` 和 `operations.ProvisionFor`，再以配置后的应用角色完成：迁移只读校验、创建模型/渠道、发布策略/价格、人工入账双人审批、预占、发布绑定快照校验、派发准入、可信最终用量、结算、退款审批、报表重建。应用角色可以只更新 settlement 的退款累计值，不能修改已结算收费、历史售价/账本、迁移记录或创建表。
2. 以实际配置后的投影角色消费 outbox，报表正确呈现入账 1000、收费 15、退款 10；钱包可用 995、冻结 0。该角色不能改钱包、Key 预算、创建分录、读取供应商密文或切换活跃投影。
3. 供应商秘密轮换产生不同 credential/channel/route 版本，历史路由仍引用原秘密；主密钥 `old→new` 重加密保持这些 ID 与秘密语义不变，新密钥可解重包后记录。
4. MFA 活跃/待确认秘密在重包后保持原值。`purpose+record ID+key version` AAD 拒绝跨凭据/用途交换密文。旧备份密文仍可由保留 old key 的密钥集解开，仅有 new key 时不能解旧备份。
5. 在凭据更新之后遇到损坏 MFA 密文，整个 Rewrap 回滚，凭据密文和成功重包审计都不发生部分提交。

## 只读检查结论与跟踪

- 财务 HTTP 经过会话、CSRF、MFA、最近再认证和恢复模式门禁；maker/checker 在账本服务内再次按当前数据库角色验证。管理员不能自动替代财务角色。金额请求按十进制字符串处理。
- 离线 `rebuild-reports` / `rewrap-secrets` 要求 `MAINTENANCE_MODE=true` 并取得与服务相同的独占 advisory lock；正常 serve 启动第二实例时拒绝。worker 在 RECOVERY_MODE 下停止资金恢复和投影写入。
- 投影重建保持旧 consumer 活跃，候选全量处理并核对财务事实/账本后才切换；这不替代维护命令外部“已停止其他业务写入”的前提。
- 已向实现者报告并跟踪的具体缺陷：资金错误码 `request_exists/invalid_key/route_changed` HTTP 映射；人工核对制单/审批的 Idempotency-Key 合同；默认退款业务号需要租户域；结算流水用 max(abs(line)) 会把 R 当 A 显示；Compose 需要多主密钥配置传递；单独失去 advisory-lock 会话到 10s 检测之间可能出现第二实例接管窗口。本文记录发现，不把实现者之后的修复自动算作已复测。
- 原先缺少 credential ID/用途的 AAD 已在本轮改为 v2 并由上述数据库测试验证。`ProvisionFor` 的 schema/角色标识符已使用 PostgreSQL identifier quoting。

未在这两项测试中执行：容器实际首次安装/升级、进程 SIGKILL 窗口、备份恢复和 RPO/RTO、DB 网络分区与独占锁丢失接管测试、真实供应商与支付供应商核对。这些不能依据本文件标为通过。

后续复核：上述 HTTP 错误码、核对 Idempotency-Key、退款业务号租户域、结算流水取实际 REVENUE 收费及 Compose `MASTER_KEYS_JSON` 已看到实现修改；本文仅确认代码出口，端到端 HTTP/容器复测由整包验收记录。锁会话丢失后的接管窗口仍需专门的 fencing 测试。

联合回归：`... go test -race -count=1 ./internal/billing ./internal/operations` — billing PASS 6.431s（20 顶层测试），operations PASS 2.042s，无跳过。随后新增的 PG 明确事务中止重试测试单独执行 PASS 2.104s，验证 40001/40P01 仅重试已回滚事务、最多两次重试且不重复账本；`go vet ./internal/billing ./internal/operations` PASS。容量优化仅缩小所需账户/model锁，未绕过余额、预算、审计或账本约束。


最终补强：新增 004 持久 recovery gate 和双人解锁服务。真实 PG 测试验证 env=false 仍读到 required、当前 maker/checker 角色/状态、冻结提案、不同账号、epoch/proposal 绑定、账本不平衡拒绝、审计失败回滚；批准不修改已有冻结。root 增加失锁 fencing / 前驱窗口等待后，TestInstanceFencesAfterBackendTermination 和 TestTakeoverDrainsDurablePriorWindow 均已在本地 PG 通过。此前报告的10s接管窗口已由这些实现及测试处理；实际恢复 main 进程的验证另见 backup-restore.md。

# 采购预算投影维护reader验证记录

日期：2026-11-02。合同范围见[022-C0-T3采购预算投影合同](../../specs/022-self-service-payments/c0-procurement-budget-contract.md)。该片只验证同一ReadLease中的`procurement_accounts`、`procurement_reservations`与`procurement_budget_events`本地投影一致性，不验证供应商账单、凭据绑定、成本审查或完整资金来源。

## 已执行

- `go test -race -count=1 ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`：通过。覆盖纯`big.Int`核验、固定reader、维护租约proof及外部PostgreSQL QA测试代码编译与默认测试路径。
- `go vet ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`：通过。
- `go test -v -count=1 ./scripts/maintenance-scan-qa -run '^TestMaintenanceConsumersPostgresFundedSnapshot$'`：Plan与Init均明确SKIP，原因是`C0_MAINTENANCE_DATABASE_URL`未设置；命令退出0不是数据库验收通过。
- `go run ./scripts/check-architecture --refresh --reviewed --reason '022-C0-T3增加采购预算投影维护reader；复核固定扫描SQL、独立核验、ReadLease权限、QA损坏场景、架构图和未验PG边界'`：刷新1253个文件的架构快照；随后只读架构checker通过。
- `make check`：通过；包含只读架构checker、全仓`go vet ./...`与Vue/TypeScript生产构建。
- 纯核验额外覆盖重复reservation/event同时仍逐行统计孤儿的诊断语义；QA仅在`init`子场景注入投影损坏，`plan`保留平衡基线。

## 尚未执行

执行本记录时没有配置`C0_MAINTENANCE_DATABASE_URL`。因此外部维护集成的PostgreSQL `Plan`/`Init`两条路径均未执行；其中真实角色、ReadLease SQL、三表同快照EOF、损坏投影发现以及扫描前后数据不变均没有取得本轮真实PG证据。测试包退出成功或跳过不构成这些验收项通过。

`TEST_DATABASE_URL`也未配置，所以未运行要求专用PostgreSQL的`make verify-mvp`。整仓race、PG真实进程/迁移验收仍须在独立测试库可用后执行。

## 结果边界

本地通过只证明纯核验和Go调用边界测试通过，不证明真实PostgreSQL权限/执行计划或完整维护集成。该reader不连接普通main、HTTP、worker、资金writer或生产维护CLI，不颁发资金来源全覆盖、basis、epoch、退款或余额写入权限。

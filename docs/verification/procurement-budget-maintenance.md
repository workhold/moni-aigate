# 采购预算与 attempt 关系维护 reader 验证记录

日期：2026-11-02。原预算投影窄片合同见[采购预算合同](../../specs/022-self-service-payments/c0-procurement-budget-contract.md)，当前十二表实现合同见[采购 attempt 关系合同](../../specs/022-self-service-payments/c0-procurement-attempt-contract.md)。一次`ScanProcurement`完整扫描产生两个独立摘要：账户/reservation/event预算投影，以及永久credential binding、attempt/request租户、channel credential pool、上游报价快照与reservation上界关系。它不证明供应商真实扣费或全来源覆盖。

## 本轮本地代码验证

- `go test -race -count=1 ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`：通过。覆盖纯大整数核验、十二表固定分页reader、一个共享行数/文本预算、同lease双摘要、错误零结果、正常数据mismatch、proof/Permit绑定和脱敏。
- `go test -race -p 1 ./... -count=1 -timeout 600s`：通过。全仓Go race包含本片reader、维护集成和既有模块；前端测试不由此命令覆盖。
- Reader专项测试逐一覆盖所有单列关系的127/128/129分页边界、pool复合tuple游标、跨表100000行/16MiB预算、无效UTF-8/NUL、取消/timeout，以及十二表Query/Next/Scan/Err/Close故障的原子零结果。核心测试覆盖关系正例、历史binding时间边界、报价公式、重复/唯一键、孤儿/租户/版本/pool错配、金额与状态形状、超限及上下文错误。
- `go vet ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`：通过。
- `make check`：通过；只读架构检查、全仓`go vet ./...`和Vue/TypeScript生产构建通过。
- `go test -v -count=1 ./scripts/maintenance-scan-qa -run '^TestMaintenanceConsumersPostgresFundedSnapshot$'`：Plan和Init子测试均明确SKIP，原因是`C0_MAINTENANCE_DATABASE_URL`未设置。退出码为0只代表测试入口通过，不是PostgreSQL验收通过。

外部consumer测试现已编码真实关系正例：通过PostgreSQL fixture创建channel、credential、version/pool、cost quote、tenant model/key、request/customer reservation，再使用采购Store创建binding并用`billing.Store.RecordAttempt`创建供应商attempt。Plan要求两个摘要都平衡；Init在隔离superuser fixture中绕过业务trigger只改动供应商reservation bounds，要求预算投影保持平衡而binding graph报告request bounds与cost upper mismatch，并对比扫描前后的完整固定采购快照确认scanner本身不写数据。由于专用数据库未配置，这些Plan/Init断言本轮没有运行。

## 尚未执行

当前环境`C0_MAINTENANCE_DATABASE_URL`未配置，故真实PG Plan/Init、实际角色/ReadLease/EOF、受控attempt损坏发现与扫描前后数据不变均未取得本轮数据库证据。`TEST_DATABASE_URL`也未配置，所以未运行需要专用PostgreSQL的`make verify-mvp`。本地race/fake reader测试不替代真实PG测试。

## 范围与限制

本reader仍是库级维护功能，没有普通main、HTTP、worker、资金writer或生产维护CLI接线。数据不匹配会以两个各自scope的`Balanced=false`摘要返回且正常完成ReadLease；查询、分页、EOF/Err/Close、资源、取消、deadline或Finish失败返回零结果并撤销lease。成本更正与证据、双审proposal、采购commands/audit、人工余额观察、周期预算交叉关系，以及供应商实际用量/发票/余额真实性仍未覆盖。五个维护reader的局部ScanProof不组合成`CoverageComplete`，不授予资金写入、basis、epoch、init或生产运行权限。

历史采购预算首片只扫描`procurement_accounts`、`procurement_reservations`与`procurement_budget_events`，当时的本地race/vet与架构检查记录只适用于那个三表投影实现；本次扩展不追溯扩大旧范围。最终集成与架构检查结果会在本文件及架构快照中更新。

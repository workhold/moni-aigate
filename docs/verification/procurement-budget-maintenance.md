# 采购预算、attempt 与成本双审维护 reader 验证记录

日期：2026-11-02。原预算投影窄片合同见[采购预算合同](../../specs/022-self-service-payments/c0-procurement-budget-contract.md)，此前十二表实现合同见[采购 attempt 关系合同](../../specs/022-self-service-payments/c0-procurement-attempt-contract.md)。一次`ScanProcurement`完整扫描产生两个独立摘要：账户/reservation/event预算投影，以及永久credential binding、attempt/request租户、channel credential pool、上游报价快照与reservation上界关系。它不证明供应商真实扣费或全来源覆盖。

## 十五表成本双审扩展（2026-11-02）

当前合同为[采购成本双审关系](../../specs/022-self-service-payments/c0-procurement-approval-contract.md)。同一个ScanProcurement共享十五表100000行/16MiB预算并Finish一次，增加独立`procurement_cost_approval_graph_v1`摘要。新核验包含users ID、所有cost corrections与采购proposals；预算event同次读取type/evidence_ref。approved proposal、managed correction、cost_corrected event必须双向精确匹配；无采购reservation的legacy更正不凭operation前缀强制关联提案。不用当前角色、成本或revision反推历史审批。

纯core、固定reader、维护结果和外部PG Plan/Init QA已编码，独立只读复核未发现阻塞问题。本片已执行：

- `go test -race -count=1 ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`通过，包耗时分别9.172s、9.916s、10.752s；纯core的`go test -race ./internal/fundinglotverify`与vet由独立实现者通过。
- 十五表的固定投影、127/128/129分页和Query/Next/Scan/Err/Close故障矩阵均已通过。首轮新增两张空表没有实际触发Scan故障，导致两个测试断言失败；已为故障矩阵准备真实行后重跑通过，未改动reader错误规则。
- `go vet ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./internal/productvalue ./scripts/maintenance-scan-qa ./scripts/browser-qa`通过。
- `go test -race -p 1 ./... -count=1 -timeout 600s`全仓Go通过；真实数据库opt-in仍跳过，不能当作商业数据库验收。
- `go test -v -count=1 ./scripts/maintenance-scan-qa -run '^TestMaintenanceConsumersPostgresFundedSnapshot$'`的Plan/Init明确SKIP：未设置C0_MAINTENANCE_DATABASE_URL。

下文“十二表”命令和通过范围保留历史身份，不追溯扩大到本片。最终汇合后`make check`通过：只读架构核验、全仓go vet及Vue/TypeScript生产构建均成功。初次集成快照1276文件/793769f6bb49；随后仅收口本验证文本，再由根复核刷新并执行最终只读检查。

当前`TEST_DATABASE_URL`与`C0_MAINTENANCE_DATABASE_URL`均未配置，本片真实Plan/Init及`make verify-mvp`未执行。QA现通过真实业务API建立sending→unknown→双审invoice更正；Plan要求三摘要平衡，Init只在隔离superuser fixture中篡改proposal evidence并保留原bounds损坏，用于验证正反向错配且扫描本身不改任何采购/提案/更正事实。这个流程本轮尚未取得真实数据库执行证据。

未覆盖evidence identities/metering、commands/audit/outbox、人工余额观察、PB交叉图、全来源组合/basis/epoch或供应商账单，仍无普通main/HTTP/worker/writer/生产维护CLI接线。

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

历史十二表片仍是库级维护功能，没有普通main、HTTP、worker、资金writer或生产维护CLI接线。数据不匹配会以两个各自scope的`Balanced=false`摘要返回且正常完成ReadLease；查询、分页、EOF/Err/Close、资源、取消、deadline或Finish失败返回零结果并撤销lease。成本更正与证据、双审proposal、采购commands/audit、人工余额观察、周期预算交叉关系，以及供应商实际用量/发票/余额真实性仍未覆盖。五个维护reader的局部ScanProof不组合成`CoverageComplete`，不授予资金写入、basis、epoch、init或生产运行权限。

历史采购预算首片只扫描`procurement_accounts`、`procurement_reservations`与`procurement_budget_events`，当时的本地race/vet与架构检查记录只适用于那个三表投影实现；本次扩展不追溯扩大旧范围。最终集成与架构检查结果会在本文件及架构快照中更新。

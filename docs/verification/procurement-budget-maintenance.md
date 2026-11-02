# 采购五项局部核验维护 reader 验证记录

日期：2026-11-02。当前为十九表/五摘要库级维护核验；无全采购/全来源coverage，不证明供应商真实账单。原预算投影窄片合同见[采购预算合同](../../specs/022-self-service-payments/c0-procurement-budget-contract.md)，此前十二表实现合同见[采购 attempt 关系合同](../../specs/022-self-service-payments/c0-procurement-attempt-contract.md)。此前十二表`ScanProcurement`扫描产生两个独立摘要：账户/reservation/event预算投影，以及永久credential binding、attempt/request租户、channel credential pool、上游报价快照与reservation上界关系。它不证明供应商真实扣费或全来源覆盖。

## 当前十九表管理历史与成本消息扩展（2026-11-02）

合同为[历史读取](../../specs/022-self-service-payments/c0-procurement-history-read-contract.md)、[命令/余额观察](../../specs/022-self-service-payments/c0-procurement-command-contract.md)和[成本审计/消息](../../specs/022-self-service-payments/c0-procurement-cost-journal-contract.md)。原十五表后追加procurement_commands、procurement_balance_observations、audit_events和projection_outbox；全部表使用同一100000行/16MiB预算、128行C序keyset和原lease期限，audit只读一次供两core复用。新增两scope后共五摘要，Require/Finish仍各一次。无新迁移、业务写入、在线权限或生产维护装配。

新command core独立核闭合JSON、历史配置内部变化、命令与binding/observation及审计双向关系；不拿当前account/用户角色/channel revision反推历史。snapshot沿014保留每string≤1000/no-control和整快照≤16384字节，物理实体TEXT不另加该上限。新cost journal core核proposed/approved/legacy审计和cost_corrected outbox，Money为字符串；managed由reservation存在性判定，legacy actor未存于source，不能反推其原身份或强制当前users。无关action/kind不按采购JSON解释。

已执行根集成定向`go test -race -count=1 ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`及对应vet，通过；三包race分别9.215s、9.868s、10.774s。新四表继承127/128/129分页与Query/Next/Scan/Err/Close故障矩阵；新增nullable amount/provider_as_of、NULL audit tenant、共享audit及观察快照双向错配用例，共享行/字节预算故障移至最后outbox表验证没有重置。初跑新增测试的fixture created_at索引越界，已将16改为实际列15；专项与上述三包重跑通过，未改reader规则。

`go test -v -count=1 ./scripts/maintenance-scan-qa -run '^TestMaintenanceConsumersPostgresFundedSnapshot$'`已执行：Plan/Init均因C0_MAINTENANCE_DATABASE_URL缺失明确SKIP。QA已编码通过真实API创建account、binding、人工known观察和合法not_found拒绝（四条commands、一条observation）；Plan要求五摘要一致，Init在隔离管理员fixture篡改proposal evidence、reservation bounds和observation evidence，核正反关系mismatch且全部采购/commands/observations/audit/outbox扫描前后不变。本轮未取得这组实际数据库证据。TEST_DATABASE_URL同样缺失，未运行make verify-mvp。

最终汇合后的四包定向race再次通过：fundinglotverify 1.562s、billing 9.987s、maintenancescan 10.514s、maintenance-scan-qa 11.856s；对应vet通过。`go test -race -p 1 ./... -count=1 -timeout 600s`整仓通过（数据库opt-in未配置，不能视作专库验收）。独立代码复核确认十九表/五摘要装配无阻塞，另发现无关outbox空operation的去重漏检，已修复并新增空operation/相同kind/不同ID回归，纯包race 1.806s与vet通过；根最终装配结果另记，不把早于该补丁的整仓通过扩大为该补丁的精确版本证据。

空operation去重补丁汇合后，根最终 `go test -race -count=1 ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa` 再次通过，分别1.781s、9.443s、10.533s、11.478s；对应四包vet与diff-check通过。这次是补丁后的集成证据，整仓race保留前一精确范围。README的native/Compose启动链及新卷/已有卷密码边界已独立静态核对；未执行容器或1Panel部署。

根汇合全部并行结果并复核模块图、请求/数据关系、权限边界、能力状态与源码链接后，架构refresh及只读核验通过；随后`make check`通过（只读架构、全仓go vet、Vue/TypeScript strict生产构建）。最终只补入本执行记录，再复核刷新清单并运行只读架构与diff检查；没有因此重标任何未执行的数据库或部署验收。

本片仍未实现evidence/metering图、完整事件重放、PB/客户资金交叉、全来源组合/basis/epoch或供应商余额认证。源代码审计发现旧冲突incoming身份、原始delivery和预算事件顺序的缺证，见[研究记录](../research/procurement-evidence-lifecycle.md)；不能用猜测补成完整证明。下列十二/十五表记录保留各自历史范围，不追溯扩大为十九表验收。

## 十五表成本双审扩展（2026-11-02）

该轮合同为[采购成本双审关系](../../specs/022-self-service-payments/c0-procurement-approval-contract.md)。同一个ScanProcurement共享十五表100000行/16MiB预算并Finish一次，增加独立`procurement_cost_approval_graph_v1`摘要。新核验包含users ID、所有cost corrections与采购proposals；预算event同次读取type/evidence_ref。approved proposal、managed correction、cost_corrected event必须双向精确匹配；无采购reservation的legacy更正不凭operation前缀强制关联提案。不用当前角色、成本或revision反推历史审批。

纯core、固定reader、维护结果和外部PG Plan/Init QA已编码，独立只读复核未发现阻塞问题。本片已执行：

- `go test -race -count=1 ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`通过，包耗时分别9.172s、9.916s、10.752s；纯core的`go test -race ./internal/fundinglotverify`与vet由独立实现者通过。
- 十五表的固定投影、127/128/129分页和Query/Next/Scan/Err/Close故障矩阵均已通过。首轮新增两张空表没有实际触发Scan故障，导致两个测试断言失败；已为故障矩阵准备真实行后重跑通过，未改动reader错误规则。
- `go vet ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./internal/productvalue ./scripts/maintenance-scan-qa ./scripts/browser-qa`通过。
- `go test -race -p 1 ./... -count=1 -timeout 600s`全仓Go通过；真实数据库opt-in仍跳过，不能当作商业数据库验收。
- `go test -v -count=1 ./scripts/maintenance-scan-qa -run '^TestMaintenanceConsumersPostgresFundedSnapshot$'`的Plan/Init明确SKIP：未设置C0_MAINTENANCE_DATABASE_URL。

下文“十二表”命令和通过范围保留历史身份，不追溯扩大到本片。最终汇合后`make check`通过：只读架构核验、全仓go vet及Vue/TypeScript生产构建均成功。初次集成快照1276文件/793769f6bb49；随后仅收口本验证文本，再由根复核刷新并执行最终只读检查。

当前`TEST_DATABASE_URL`与`C0_MAINTENANCE_DATABASE_URL`均未配置，本片真实Plan/Init及`make verify-mvp`未执行。QA现通过真实业务API建立sending→unknown→双审invoice更正；Plan要求三摘要平衡，Init只在隔离superuser fixture中篡改proposal evidence并保留原bounds损坏，用于验证正反向错配且扫描本身不改任何采购/提案/更正事实。这个流程本轮尚未取得真实数据库执行证据。

未覆盖evidence identities/metering、commands/audit/outbox、人工余额观察、PB交叉图、全来源组合/basis/epoch或供应商账单，仍无普通main/HTTP/worker/writer/生产维护CLI接线。

## 历史十二表本地代码验证

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

# T3 首片：受控读取租约与两个局部维护 reader 验证

2026-11-02，按[根冻结合同](../../specs/022-self-service-payments/c0-read-lease-contract.md)实现真实 ReadLease、封闭 Rows/codec、ScanProof，以及 ledger/PB 两个实际维护入口。最终显式专库 `go test -race ./internal/maintenancetrust -count=1 -timeout 600s -json` 退出0，387.029秒：104个顶层测试、含子测试876项全部PASS，零FAIL、零SKIP；876个run各有唯一终态。最终日志SHA-256为 `abeab3da7ea24b864a095c88e8a346c7d9b01e2dcaeac7d636cb5daf97075a13`。[机器记录](maintenance-read-lease.json)保存独立解析结果、原始失败、逐次日志摘要与最终源码SHA。旧reader/core专项、整仓检查、架构和独立清理审计由根在[集成记录](maintenance-read-lease-integration.md)分别收口，不以本专项替代。

本轮是T3首片，完成的是同原快照的受控读取和两个局部结果。它不授完整来源CoverageComplete、资金写入或init/basis/epoch就绪条件。Funding、Key、采购、B来源、lot全图与完整组合器仍后续；生产维护CLI、writer和普通main/HTTP装配尚未开放。

## 最终执行与计数

| 独立命令范围 | 秒 | 顶层PASS | 含父子PASS | FAIL / SKIP | 原始日志 |
| --- | ---: | ---: | ---: | --- | --- |
| maintenancetrust全包最终race | 387.029 | 104 | 876 | 0 / 0 | `.local/c0-read-lease-verification/full-final.jsonl` |
| genuine funded Plan/Init双reader | 50.907 | 1 | 3 | 0 / 0 | `.local/c0-read-lease-verification/consumers-first.jsonl` |
| PB资源临界值三组九子例 | 166.914 | 3 | 12 | 0 / 0 | `.local/c0-read-lease-verification/resources-repaired.jsonl` |
| SQL/CPU取消、Abort与出口runtime | 31.021 | 1 | 5 | 0 / 0 | `.local/c0-read-lease-verification/cancel-repaired.jsonl` |
| 最终无PG纯lease回归 | 17.586 | 11 | 23 | 0 / 0 | `.local/c0-read-lease-verification/proof-cancel-final-pure.jsonl` |

这些行是不同时间的独立调用，父子计数包含顶层，重跑不相加为唯一覆盖数。各日志均独立重算SHA及run→唯一终态；精确命令与其证据来源见机器记录，测试库环境值不入文档。全包最终命令含T0/T1/T2回归；不能把历史T2的333.879秒/832项或旧CI当作本轮T3证据。双reader、资源和修复专项按各自实际执行版本记录，未宣称所有早期调用都编译自最终同一源码快照。

## 三层证据与公开链路

**真实ordinary ReadLease链**使用原named-minimal actual LOGIN fixture、原001–022/provision及固定ordinary C0 DDL/ACL，实际经过Acquire→Plan/Init→VerifyInstallation→Attest→BeginScan→Rows/QueryRow→Finish→ScanProof.Verify。检查原PID/top xid/RR与只读/读写模式、同kind只尝试一次、第二kind合法完成、错误kind/Permit拒绝、复制proof及原Snapshot回滚撤销。完整lease期间Permit.Verify、第二scan、Commit/Rollback/Close和并发query均busy，独立wire确认拒绝不发SQL。遗漏Row、Rows、Err或Close，提前Close、跳行，均零proof并物理失效。实际QueryRewriter与RowScanner保持零调用，pgx控制参数、DriverBytes和*any拒绝。全包还重跑原named-image/public-minimal/public-image及genuine sandbox授权矩阵；这些T2 profile回归不能推导每个profile均新增执行了ReadLease扫描。

**真实genuine sandbox双reader链**在外部测试包仅调用生产公开API。原CreateApplication安装带资金的v1 QA，追加固定C0 SQL/ACL，保留真实moni_test维护LOGIN及pins，Plan先ledger后PB、Init反序，同一个原Permit分别BeginScan并运行两个实际reader。调用方将返回的InstallationProof wrapper赋零，不影响内部owned installation；ledger保留非空accounts/journals/lines及supplied_ledger_rows，PB保留原局部报告。两个结果仅认原Permit，拒绝无关Permit，原Snapshot回滚撤销全部原件和副本；维护后原资金与legacy摘要不变。没有替代issuer、假Conn/Tx或仅模拟通过路径。

**纯codec/Rows/reader core层**用私有脚本对象验证owned复制、允许集、EOF/Err/Close顺序、提前关闭、重复Scan、恰一行、错误映射、完整零结果、取消与binding。ledger脚本核三表原列、journal额外事实与一次完整大整数核算，PB脚本核原SQL、三态、历史缺证与operation去重；这些测试不发行真实安装/Permit，不能称为完整公开维护链或生产来源验收。reader/core及旧公开入口真实PG回归的执行结果由根单独记入集成记录。

## PB维护资源九例

三组均从genuine funded QA公开链进入PB维护reader，使用合法原owner普通INSERT追加非资金审计，不改触发器、权限、pins、冻结DDL/ACL或history。独立固定oracle逐项测19关系/146声明字段，仅取scalar计数/尺寸；全量audit与真实22条migration history计入，不套PB业务审计过滤条件。

| 边界 | below | at | over |
| --- | ---: | ---: | ---: |
| 单字段cast text后的UTF8 bytes | 65535 | 65536 | 65537 |
| 全声明字段累计UTF8 bytes | 16777215 | 16777216 | 16777217 |
| 全19关系累计行数 | 99999 | 100000 | 100001 |

below/at成功，over均零结果与ErrVerifyExecution，并撤销原Permit，明确Close前检查原backend/locks消失。单字段用多字节“界”证明字节语义；总量例分别控制其他资源在界内。总行数和总字节的over例若漏计真实history会错误降到界内，断言保留其贡献。普通审计记录不计入PB业务Scanned.Audits，验证报告与资源库存不是相同计数。追加审计会改变原资金digest的audit部分，因此在合法setup后重采FundingSnapshot，再于维护后对比并核legacy，未把安装前digest误判成资金改变。

真实history名称与校验和按T2固定；增加/放大history行会在T2先拒绝，不能合法通过公开链。这里覆盖其真实rows/bytes总量贡献，不冒称“巨型坏history走过公开PB”。逻辑输入限制不代表PG排序/CTE/array_agg内存、Go堆/RSS或任意DataRow内存上界。

## 原始失败、修复与服务器取消边界

保留所有原失败日志，未用修复后成功覆盖它们。

- `lease-targeted.jsonl`为103.649秒，3顶层PASS/1FAIL、18父子PASS/3FAIL。sql-cancel与concurrent-abort将客户端raw-close误等同于任意长服务器SQL立即释放PID/锁。最终测试使用真实pg_sleep(1)和独立PgSleep屏障，取消后先观察捕获wire.closed在500ms内触发、原SQL owner在3秒内安全返回，再检查原PID/backend_start/锁自然消失。`cancel-repaired.jsonl`四模式全通过。生产raw-close保持；未调client_connection_check_interval、未主动terminate、未并发drain、未延长业务预算。任意长服务器CPU/等待SQL的立即中断及部署参数前提仍需另冻和验收。
- `resources-first.jsonl`为124.904秒，三组九子例都在独立oracle第一表失败。`resources-diagnostic.jsonl`27.762秒仅额外输出安全SQLSTATE=42704、position=29，确认测试oracle将SQL别名限定成了`pg_catalog.bigint`。修为真实catalog类型`pg_catalog.int8`；原生产guard、资源界、history、DDL/ACL不改。单例复验33.902秒后，最终资源三组九例166.914秒全通过。
- `proof-cancel-before-fix.jsonl`16.033秒确定复现真实生产binding窗口：后继lease的caller/private-context已取消、AfterFunc尚未调度时，旧完成proof及副本可能仍返回nil。两个子例均失败。生产ScanProof.Verify现于原短锁读取当前未stopped permitCall的caller/context取消，立即返回ErrTimeout，且不误读旧成功call的正常cancel。`proof-cancel-final-pure.jsonl`和最终全包均通过；该确定性回归属纯predicate层，真实SQL取消另由PG专项承担。
- closeCall持续持operation/Gate busy直到stop、callback ack、call.cancel及budgetCancel完成，再清activeLease/permitCall并释放busy。纯ack屏障确认后继scan无法提前进入、并发清理不触碰driver；不将这项私有并发oracle写成完整真实发证链。

`proof-cancel-after-fix.jsonl`为一次较宽选择的中间无PG回归，11顶层/23父子PASS且4项明确SKIP，不能冒充真实PG通过；最终pure使用窄选择零SKIP，最终显式专库全包也零SKIP。所有中间尝试的日志SHA、计数和唯一终态检查保存在机器记录。

## 范围、预算与测试装配

ReadLease仅保留同原rawTx，BeginScan到Finish/Abort整个生命周期持busy；入口/出口完整重核原T2 evidence及新鲜停服。单lease最多30秒并取caller/Snapshot更早期限，CPU间隙不重置，失败撤销全部proof并关原socket。参数exact允许集、最多1024项及累计16MiB逻辑bytes，包含[]string逐项长度框架；Scan本地typed暂存后owned赋值，不透传callback或driver引用。Finish必须无悬挂读取并完整观察EOF/Err/Close。ledger仍仅supplied_ledger_rows，PB Summary才显式periodic_budget_rows，PB原三类历史缺证保持indeterminate；原public Verifier的actual LOGIN/owner/history/RRRO规则保留。

billing→maintenancetrust新增生产依赖导致原内包测试直接import paymentsandboxqa形成测试循环。仅_test.go外包init注册DTO factory，委托原CreateApplication/OpenPool/Close/VerifyLegacyFundingUnchanged并复制role/schema map；内包仍保留真实安装、固定源SHA、私有catalog/authority/funding oracle。没有生产注册、替代Permit/Proof issuer、原始事务出口或修改原QA安装器。

本专项仅固定127.0.0.1:55439/moni_test及其隔离fixture，不触碰长期58123、moni_review，不调用真实模型或支付商。精确fixture清理断言与shared public/database baseline检查已随各通过测试执行；独立外部post-run审计尚由根另行记录，本页在其证据加入前不声明最终全局清理成功，也不从旧审计推断本轮前后配置不变。整仓通过、架构快照和提交状态以集成记录为准。

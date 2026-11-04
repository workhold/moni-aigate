# B-P4 A5-S2：候选事务运行器合同

2026-11-04；先冻结 API 与提交/清理状态机，再交付 §7 源码和纯验证。承接[A5 总合同](b-procurement-prospective-executor-contract.md)、[执行合同](b-procurement-execution-contract.md)和[A2 安装合同](b-procurement-attempt-installation-contract.md)。本页仅负责实际 Go 事务运行器及 unknown 的过程内材料；不实现只读 observer、业务 Store/HTTP 接线、安装权限或默认启动，未执行本页实际 PG 故障验收，不关闭 A4、完整 A5、PB 错误域或 R2。

## 1. 固定 API 与职责

在已有 `internal/procurementexecution` 内新增 runner；不修改 S1 的查询/参数、decoder 或 PP 映射，不导入 billing/procurement/server，不提供 caller SQL 或业务回调。公开接口冻结为：

```go
NewRunner(pool *pgxpool.Pool) (*Runner, error)
(*Runner).RunAttempt(ctx context.Context, invocation Invocation) (AttemptReceipt, error)
(*Runner).RunAccount(ctx context.Context, invocation Invocation) (AccountReceipt, error)
```

`NewRunner` 只绑定受信 pool，不连接、不安装、不探测函数/profile。nil pool 或 nil runner 返回安全 `ErrRunnerUnavailable`（`procurement_execution_unavailable`）；nil context、零 Invocation、RunAttempt 非 `pp_record_attempt` 或 RunAccount 非两账户根为本地 PP903，Acquire 前拒绝。Invocation 只能来自 S1 固定构造器；运行前还须核内部 query/参数数量与该根形态，不能把零值或同包意外构造误认为任意 SQL执行器。内部测试接口不导出，生产只能从真实 pgxpool 构造。

Run 返回 S1 内部回执供未来 S3 重建原 DTO；它本身不实现原事务外业务校验。RunAttempt 的调用前提仍为原 billing validator 与准确参数转换，特别是 PP101 的固定适配前提。任意 error 均返回零回执；S1 decoder 出来的中间结果在 COMMIT明确成功前保持私有。ClassifyPP 在返回业务错误时沿固定入口域应用，非 PP保持原 cause，不新增 PB文本分类。

## 2. 连接与单根执行

每次尝试显式 Acquire；从 BEGIN 到清理完成持有同一连接，不使用 pool.Begin 自动归还。BeginTx 固定 READ COMMITTED；执行一次 `SELECT pg_catalog.set_config('lock_timeout','3s',true),pg_catalog.set_config('statement_timeout','5s',true)`，然后仅执行 Invocation.Query 的一次 frontend Query（固定三个公开根之一），参数取 S1 私有快照。不得在 Go补写来源/资金，不能调用私有 closure，不拆 SPI或重置子步骤 timeout。

所有 Acquire/Begin/SET/Query/Commit 都使用调用方同一 context与绝对 deadline，不新增8s/10s业务预算，不在重试中重建 deadline。根 SQL一次整体受5s，锁等待3s；不能把 Acquire/BEGIN/COMMIT计入“整次请求5s”，也不能续期数据库资格。原statement没有成功结束前不发送下一条业务SQL。

Runner 对 Query得到的Rows使用 S1 decoder，确保列名/数量/OID、恰一行与联合及排流；Query同时返回 rows与error的异常接口结果也必须关闭rows，真实server/stream因果优先保留。协议失败不COMMIT；全部DB原件与deferred最后仍由实际COMMIT裁决。

## 3. 提交与清理状态机

| 状态/事件 | 行为与公开结果 |
| --- | --- |
| Acquire失败 | 不曾BEGIN，无清理事务；返回原error/零回执 |
| Begin失败 | 连接idle且可用才Release，否则销毁；不猜事务已安全开始，不重试Begin错误 |
| SET/Query/decoder失败 | 不发送COMMIT；独立清理context Rollback，再读idle/closed判定，最后归还或销毁；返回原主因/零回执 |
| decoder成功但ctx已结束 | 不调用Commit；按上述Rollback，返回ctx错误/零回执 |
| Commit即将调用 | 标记`commit_may_have_been_sent`；不得在此后用ctx.Err、ErrTxClosed或cleanup结果推断未提交 |
| Commit明确nil | 可发布回执；连接可用且TxStatus=I才Release，否则销毁，但已确认成功不改为unknown |
| Commit错误且真实PgError或ErrTxCommitRollback、连接可用且idle | 可证明失败已回滚；返回零回执，只有准确重试资格才重新尝试 |
| Commit其它错误/不idle/已关闭 | `UnknownOutcome`、零回执，销毁连接，绝不自动重试或观察 |

所有失败清理均使用脱离原ctx的**同一个独立绝对context，timeout最多5s**，Rollback和必要的Close共用剩余预算，不在Rollback失败后再发新5s。Rollback nil且连接可用/TxStatus=I才算确认回滚；失败或非idle必须销毁。销毁用 pgxpool.Conn.Hijack 后关闭，绝不Release污染连接；过期context也不允许把已Hijack连接重新归还，不能续业务。清理错误可join到cause但不覆盖原始错误，不允许清理产生的40P01/40001获得重试资格。明确Commit成功后的非idle销毁亦有最多5s清理预算，结果仍为已确认成功。

Runner持有连接期间的Begin/执行/Commit过程panic必须经defer清理，不归还非idle连接；公开返回安全`ErrRunnerPanicked`（`procurement_execution_failed`）与零回执，不把panic值/参数格式化进error。若panic发生在Commit可能已发送后，必须返回UnknownOutcome并销毁，不能声称已回滚。Commit明确成功后销毁阶段的异常不能把已确认回执变成unknown。

`pgxpool.Conn.Release`一经调用即转移所有权；它会先将内部resource置空，再执行ReleaseTracer等外部hook。**无论Release正常返回或因外部hook panic，Runner都不得再读取该Conn、Hijack或Close，也不能通过事后销毁假称追回所有权。** Release panic在尚未确认成功的路径加入安全ErrRunnerPanicked、停止重试；已明确Commit成功的结果保持。外部pool Tracer、AfterRelease及其它用户回调必须遵守pgx合同、不得panic；其内部中断可能造成pool资源泄漏/进程异常，Runner无法修复，不能宣称这一边界属于“所有panic均完整清理”。本片不新增hook拒绝规则，也不把外部hook故障计为已验证的pool恢复。

## 4. 准确重试

RunAttempt最多三次尝试，仅来自本次SET/Query/decoder原执行cause或Commit明确回滚响应的准确SQLSTATE40P01/40001，并且整次尝试已确认回滚、连接idle、ctx未结束。无新退避、无deadline重置、无重用前次decoded ID/回执。用于判断重试的主因必须在join cleanup前保留，避免以清理错误或合并链中的另一条SQLSTATE错误驱动重试；本地协议Failure永不重试。

RunAccount只有一次尝试，包括40P01/40001。55P03/57014、业务PP、非PP完整性/权限、context/网络、scan/union/ABI异常、Acquire/Begin失败和UnknownOutcome均不重试。Commit成功即使回执outcome=replay也按成功返回；候选管理重放与原旧快返Rollback结束方式的差异已在总合同明确，真实故障回归仍需执行。

## 5. unknown材料的可用性与保密

```go
type UnknownOutcome struct { /* private cause + private observation handle */ }
func (*UnknownOutcome) Error() string // procurement_outcome_unknown
func (*UnknownOutcome) Unwrap() error
func (*UnknownOutcome) PublicCode() string
func (*UnknownOutcome) ObservationHandle() *ObservationHandle
type ObservationHandle struct { /* private invocation + decoded identity */ }
```

UnknownOutcome通过errors.As匹配且保留原Commit cause；它不是S1 Failure、不提供可用业务回执。即使其cause是PP115等业务state，只要COMMIT结果不确定，也必须先保留UnknownOutcome，禁止再次ClassifyPP降级成业务拒绝。opaque handle仅捕获本次Invocation与本次已通过decoder的私有回执/来源身份，供后续同包observer按独立合同使用；不导出参数、业务ID、root/source getter、任意SQL、恢复运行方法或序列化token。调用方只能把handle传给未来明确observer，不从中取Attempt/Command继续派发。

handle和unknown的Error/String/GoString/Formatter/JSON输出均固定安全常量，不用默认fmt结构展开暴露私有参数或原cause；包括`%v/%+v/%#v`、嵌套对象和JSON。Unwrap是受信内部诊断cause链，不能据此向HTTP/日志序列化PG文本。观察失败或未取得准确新来源不在本片处理；Runner不自动查询、不重做原operation、不触发模型请求。

私有handle字段冻结为issuer精确Runner指针、stamp非零size发行对象、schema、Invocation、完整decoded attempt/account回执与原commit cause。schema在Run验证时从S1固定query的准确前/后缀中取得一个完整quoted identifier，反解双引号并以pgx.Identifier重构逐字节相等；不按点拆分，不采信caller另传schema。zero/foreign/错根材料由后续observer在Acquire前以发行身份、stamp和固定Invocation一致性拒绝，不能从公开零值UnknownOutcome自行获得可观察授权。

## 6. 纯测试与实际验收门

Runner真实适配器封装pgxpool；包内私有接口用于确定性状态机实验，不导出mock pool或任意transaction callback。纯测试不得连接PG，须核实际下发SQL/pgx.TxOptions/同ctx、三根正确decoder、释放/销毁路径、cleanupctx上限及panic边界。它们不能证明真实server锁、deferred或提交未知。

| 范围 | 纯单测必须证明 | 后续由集成人真实PG/wire证明 |
| --- | --- | --- |
| 单根 | Acquire→RC→SET3s5s→单Query→decoder→Commit，正确回执且仅确认后发布 | actual APP新/重放/拒绝根与原SQL行为 |
| 失败清理 | SET/Query/ABI/scan故障回滚，late error保持；idle才归还；cleanup失败销毁 | 原件/资金/PB/source全行无部分结果 |
| 重试 | 40P01/40001已确认回滚3次，管理1次；55P03/57014/unknown不重试；ctx不重置 | 真实deadlock/serialization与客户端期限 |
| Commit | PgError+idle明确回滚，网络/context/非idleunknown且零DTO；确认成功可发布 | COMMIT发送前后wire确认丢失正反控 |
| panic | Runner拥有连接期间precommit独立rollback，Commit过程中unknown且不泄panic值；Release转移后不再访问该Conn | 实际驱动断线/关闭与连接池健康；外部hook自身不得panic的边界仍保留 |
| opaque材料 | handle捕获本次身份，无公共DTO；fmt/JSON不泄参数/cause | 由独立observer准确读证，不自动重发 |

本片不完成S3、HTTP、observer、默认运行接线和PB异常域映射。所有并行结果交集成人汇总语义复核，再更新统一架构/验证记录并执行reviewed refresh与只读check；本页不能将纯mock通过写成actual APP/生产验收。

## 7. S2 源码与纯验证记录

[runner.go](../../internal/procurementexecution/runner.go)实现上述API。真实pgxpool适配器只提供显式Acquire、BeginTx、设置、固定Query、Commit/Rollback和idle/Hijack/Close；私有接口用于状态机测试，不导出pool注入或callback。Query用已有S1 Invocation和decoder，不修改其六份源码/测试。每次尝试的decoded回执独立分配；COMMIT未知只发行本次handle，不带过去失败尝试身份。

[runner_test.go](../../internal/procurementexecution/runner_test.go)采用私有连接/事务状态机与真实pgx text codec行，核三根单Query/原ctx、严格根/quoted schema、失败回滚、准确3次/管理1次、取消/清理预算、unknown与确定COMMIT、执行和清理panic、handle格式化及重试身份。没有调用真实pgxpool网络连接。最终仅运行专项：

```sh
env -u TEST_DATABASE_URL -u PROCUREMENT_IDENTITY_DATABASE_URL \
  -u PROCUREMENT_ATTEMPT_RULES_DATABASE_URL \
  go test -race ./internal/procurementexecution -count=1 -run '^TestRunner' -json
```

首片15顶层 / 23子项 PASS、0 FAIL/SKIP，1.719秒；追加Release所有权审查修正后，最终 **16顶层 / 23子项 PASS、0 FAIL/SKIP，1.727秒。** 最新原件为忽略目录 `.local/procurementexecution-s2-release-reviewed.jsonl`，40900字节，SHA-256 `f548d654f969b0dfb0ce578a47dab2a4753e5e2f39a5dcce671cb24c447dde99`。首片 `.local/procurementexecution-s2-final.jsonl` 保留，不将两轮重复相加。追加测试证明Release hook panic后无论成功/失败都不再Destroy该已转移对象；已确认COMMIT回执不改写，未确认成功的路径保留安全错误并停止重试。这不证明外部hook中断后的pool资源已恢复，边界见§3。

这些只是S2客户端状态机证据，不替代S1完整回归、真实APP三根/锁与deferred/COMMIT wire或observer验收；这些由集成人另行串行运行并记录。本片未自行运行PG，全部上位开放门保持。

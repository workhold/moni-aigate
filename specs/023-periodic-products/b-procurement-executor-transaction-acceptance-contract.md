# B-P4 A5-S2：真实 APP 事务执行器验收合同

2026-11-04。先冻结本页，再新增测试。承接[prospective executor](b-procurement-prospective-executor-contract.md)、[S2 事务合同](b-procurement-executor-transaction-contract.md)与[既有 A4 期限合同](b-procurement-attempt-fault-deadline-contract.md)。本页是限定实际数据库验收设计，不预先记录 PASS，也不关闭完整 A4/A5、S4、PB 错误域兼容或 R2。

## 1. 不变边界与调用合同

仅新增 `internal/procurementattemptbirth/executor_postgres_test.go` 与独立前缀的 `executor_wire_test.go`；复用原 `newFixture`、实际 APP LOGIN 与固定三根材料，不修改 fixture、A1 wire、SQL、ACL、角色、迁移、超时或默认装配。真实 PG 仅由集成人在专用 PG16 `127.0.0.1:55439/moni_test` 串行运行；缺专库开关的 SKIP 单独列出。原 READ COMMITTED、3 秒 lock timeout、5 秒 statement timeout、180 秒 fixture 保持。

经集成人审定的接口为 `procurementexecution.NewRunner(pool)`、`RunAccount(ctx, Invocation)` 与 `RunAttempt(ctx, Invocation)`。输入由 S1 的三个固定 `New*` 生成；测试不用任意 SQL callback 或测试专用生产接口。Runner 向调用者发布的内部 typed 回执也必须等到明确 COMMIT；任何执行、解码或提交错误均为零回执。公开业务 Store/HTTP DTO 接缝由 S3 另验。

## 2. 正常结果与独立 oracle

账户 create applied/replay，configure applied/replay、同值 noop、not_found 与 revision_conflict，均经真实 Runner/APP 执行。每个 replay 前后比较完整 34 表；成功回执与原不可变 `procurement_commands` 逐值比较，root/source/journal 调用原只读 validator，不以当前账户覆盖历史快照。noop 的 revision 不增加，但必须有原合同要求的命令和 journal。

attempt 分别覆盖真实 unmanaged/managed applied 及其四字段 replay；确认原 request、customer reservation、attempt/root/source、采购预占/事件和账户 held 由独立 SQL/手算认证。nil quote、非 NULL 空 BYTEA、非 UTF-8/NUL 字节及独立 NULL/负 rate 只在原 ignored 分支运行，不为已管理采购放宽 quote。replay 不生成新 source 或财务效果。测试沿用原 Reserve、真实绑定和原 Commander，不伪造已提交业务前缀。

## 3. 失败与提交状态机

`acquired → begun → decoded_private → commit_attempted → committed` 才发布回执。原 SQLSTATE/cause 必须保留，不根据相似错误文本改成 PP 或 unknown。

1. 原实际 APP deferred 权限反例：撤销既有矩阵中准确 APP 只读权限，固定根 W 阶段成功但 APP 外层 COMMIT 原生 42501 失败；Runner 返回零回执和原 PgError、不是 unknown，完整 34 表不变。恢复原 ACL 后认证旧已提交 prefix，并以新 request 成功控制确认恢复。测试不增任何权限。
2. 外层 deferred 原生 23514：隔离 schema 中添加仅作拒绝的 INVOKER deferred constraint trigger，返回原 `NEW` 的路径不替换 guard；指定 operation/attempt 身份于 COMMIT 抛出固定测试 SQLSTATE/Message。真实服务器确认回滚，Runner 返回零回执、同原 cause；不能把外层 23514 误判 PB、业务 rejected、幂等成功或 unknown。移除拒绝 trigger 后完整快照不变且旧 prefix 可认证。
3. 真正 COMMIT 成功但确认帧丢失：见下一节。未取得明确 COMMIT 确认时，私有 decoded 回执不得逃逸。

## 4. 精确 COMMIT 确认丢失

测试复制 A1 已审定 wire 设计为 `abExecutor*` 独立前缀，保留真实 socket/peer/deadline，不制造 PostgreSQL 回包。只对完成实际 APP 身份查询后已 arm 的唯一连接解析 frontend 分片；准确记录唯一 simple-query COMMIT。backend 必须完整收到真实 `CommandComplete(COMMIT)` 帧，任何该帧字节交给 driver 前截断并关闭 socket。计数必须为 query/reply/drop 各一次；其它 frame 原样转发，buffer 有界，分片/timeout 控制由独立纯测试证明。

真实 account/create 与 managed attempt 各自执行该故障。故障前先独立读取对应 operation/request 的缺行事实和完整快照。故障后须同时证明：

- `errors.As` 得到 `*UnknownOutcome`，安全 Code 为 `procurement_outcome_unknown`，原 EOF/关闭 cause 保留；Run 返回整个零 AccountReceipt/AttemptReceipt。
- 非 nil `ObservationHandle` 只由 Runner 发行；结构字段不可导出，普通 fmt、GoString 与 JSON 不暴露 operation/tenant/request/输入/decoded 身份。测试不从 handle 反射提取数据，不自行构造句柄。
- wire 唯一真实 COMMIT 确认被截断，原精确 backend PID 在 `pg_stat_activity` 消失；不以 frontend COMMIT、EOF 或 `ErrTxClosed` 单独推断提交。
- pgx 的 EOF 清理会通过同一 DialFunc 新开独立 CancelRequest socket；连接数不能将它误算为业务重试。测试认证每条 socket 的首包，只允许一次真实 StartupMessage 业务会话，以及准确 16 字节、原 backend PID/secret 相同的 PostgreSQL CancelRequest（不打印 secret）；任何第二 Startup、未知/额外字节或错取消身份仍失败。等待原 PgConn.CleanupDone 后核准确分类。driver 取消既不是重做根，也不是模型重发。
- 随后新独立 APP 连接、独立有界 context，以同一 REPEATABLE READ READ ONLY snapshot 读取准确 operation 或 tenant/request/sequence/四字段，以及不可变来源/根/step/账户关系；调用原只读 validator，得到准确已提交事实。测试直接观测事实，不实现生产 observer，不把观察结果变成 mutation DTO 或发送许可。
- 观察前后完整 34 表相等，wire计数不变，唯一 operation/attempt/source/资金效果数量准确；Runner 没有自动重做根或模型生成。旧已提交 prefix 继续有效。

只读观察的已存在事实证明数据库提交，原调用仍保持 unknown；不能把这次观察写成原调用成功。无实际取得身份时不生成替代 root/source。本片不实现生产 handle observer。

## 5. 验收范围和明确留门

纯 wire 控制只证明测试故障设施分帧/透明转发/目标截断，不是 PG 或商业验收。数据库结果须记录命令、源码 SHA、JSONL 名单和真实结果；失败原件保留，专项复验不重复计数。

本片优先交付真实 Runner 三根与真实 lost-ACK。完整错误优先序、原输入 validator/HTTP、每个 DML、全部协议/撤权矩阵、55P03/57014/累计 5 秒、真实 40P01/40001 重试、未提交反控制、完整 RR observer 的缺/旧/歧义输入、PP108 财务 cut、PB 错误域全等价等未逐项实证者继续开放。不得以已有 A2/A4 PASS 或 mock 控制关闭这些门。

新增测试改变候选验证覆盖和源码入口，不改变生产数据/角色/请求链路；集成人汇总所有并行结果后同步统一架构、验证记录及内容快照。

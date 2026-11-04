# B-P4 A5-S2：只读 Observe 的实际 APP 验收合同

2026-11-04；本页先设计，经集成人审定后新增测试并执行限定实际APP验收，结果见[验证记录](../../docs/verification/procurement-execution-observer.md)。承接[观察器合同](b-procurement-executor-observation-contract.md)、[事务验收](b-procurement-executor-transaction-acceptance-contract.md)与[固定 A2 材料](b-procurement-attempt-installation-contract.md)。本验收切片只改本页和新增 `observer_postgres_test.go`／`observer_wire_test.go`，不改旧 fixture、executor wire、SQL/摘要、角色/ACL 或生产 observer 源码；生产实现由其它并行切片交付。专库 PG 只由集成人串行执行。

## 1. 入口、身份和 oracle

固定 API 为 `Runner.Observe(ctx, *ObservationHandle) (Observation,error)`。每个有效 handle 必须来自**同一个真实 Runner** 经实际 APP 调固定根、完整 decoder 成功且 COMMIT 确认不确定；测试不反射提取/修改 handle、不构造私有材料、不自行生成 root/source、也不以零值 handle 假装真实 unknown。

成功唯一联合为 `State=receipt_present, Reason="", error=nil`；失败唯一联合为 `State=unresolved`、准确安全 reason、非 nil typed error，`PublicCode/Error` 为 `procurement_observation_unresolved`，原本次 PG/网络/context cause 及 unknown cause 可 Unwrap。观察结果不含 mutation DTO/身份/授权。原调用保持 unknown 和零 mutation 回执；Observe 不把它改写成 applied、可派发或可重试。

独立 oracle 用原 `procurement.Command`、原 Go payload/digest、完整 34 表快照与原 invoker validator；模型从未发送。账户旧快照不会因后续真实 configure/reserve 改成当前值。观察后的全行图与观察前相等。

## 2. 真实 socket 正反控制

直接复用旧 `abExecutorWire`／首包审计类型，不修改旧文件。新 helper 只装配独立 trace/连接池；真实 APP 完成身份查询后才 arm。正常丢确认：唯一 frontend COMMIT，完整真实 `CommandComplete(COMMIT)` 到达后截断，精确旧 backend 消失；保留唯一 Startup 会话、准确 driver CancelRequest，取消 secret 仅内存核对不输出。

negative 控制：利用现有 frontend gate 在 COMMIT **任何字节发往 socket 前**暂停。此时 root 已完整 decode，但独立 owner 连接仅见原完整 34 表且目标 operation/attempt 不存在；关闭原真实 socket后 release gate，driver 的 Commit 写失败导致真实 Runner unknown，旧 backend 随断线消失且事务回滚。该 handle 仍来自真实 decoded 调用、不是制造材料。准确 wire 为 frontend 意图1、真实 COMMIT reply/drop均0。其后 fresh Observe 应 `unresolved/receipt_absent`，不能将 absence 解释为自动重试许可；观察不新增任何事实。

正常确认丢失及发送前断开各覆盖 create、新 configure 与 managed attempt。首轮至少 create/attempt negative，configure 若未完成必须在验证记录逐项保留；不能把丢 ACK 正控替代未提交反控。

## 3. 实际 APP 最低矩阵

| 场景 | 真实准备／unknown 调用 | 独立预期 |
| --- | --- | --- |
| create applied | 真实 Runner 新建后丢 COMMIT ACK | receipt_present；原 Command/digest/source/root/step/audit/journal匹配 |
| create replay | 原 create 已确认提交，Runner 同输入重放后丢 ACK | receipt_present；观察只证明旧原件存在，不推断本次 COMMIT |
| configure changed/noop/revision_conflict/not_found | 各新 operation，实际根完整回执后丢 ACK | 四分支均 presence；not_found不要求现在目标依然不存在、journal真NULL；conflict不将journal.after当Command.After |
| configure replay | 对已提交四分支逐一相同operation重放后丢ACK | 原不可变回执存在；不发行新来源、旧快照不随当前配置变化 |
| 账户合法后继 | 捕获旧unknown后，通过原根真实configure／真实managed reserve | 旧命令仍presence，原快照保真；当前受支持prefix由validator认证 |
| unmanaged applied/tracked replay | 实际Reserve与新attempt；重放route/ignored quote可不同 | presence；准确旧五字段与四身份，NULL replay root/source不被补写 |
| managed applied/tracked replay | 原绑定+真实Reserve，包含无control及原Commander首次配置／后配legacy_import | presence仅限当前合同支持图；采购U=240及现有PB来源关系不变 |
| true legacy capture 前置门 | before-install 回调通过原 billing Reserve/RecordAttempt真实提交，再完整安装A2；实际APP经Runner重放 | 当前固定根先INSERT ON CONFLICT，其BEFORE guard先认证原request；真实legacy request在decoder/COMMIT前即PP906。须证零DTO／无COMMIT／无有效handle和34表不变；完整Observe legacy PP906仍未实证，不能伪造handle补证 |
| 原始权限损失 | 先取得真实unknown，按既有矩阵精确撤销观察必要APP列/validator EXECUTE，实际生效false | observation_unavailable，原42501 cause保留；恢复完整原ACL后同handle再次显式观察成功 |
| wrong relation／missing origin／extra reverse edge | 禁止owner伪造来源、禁guard或删已提交图；仅在已有原正常业务可达合法后继时执行 | 实际固定validator若PP902则history_invariant，实际PP906则unsupported_history，不能预猜或制造state |

最后一行不能在当前不变量下自然形成的反例，明确列为未完成实际PG矩阵。纯协议测试可覆盖客户端比较，但不替代实际PG错关系验收。不得为了得到PP902/906人工改历史、改时钟、拓展role或制造origin。真实legacy为独立严格可达反例；若原根本身在捕获前安全拒绝，则记录未取得handle，不能伪装Observe已经验证。

本次代码前只读复核已确认 legacy 的上述 capture 门：`pp_record_attempt` 的新 `INSERT ... ON CONFLICT DO NOTHING` 先执行 `pp_ab_attempt_write_v1`，其 `pp_ab_validate_request_leaf_v1` 对真实未跟踪request抛PP906，不能取得本次完整decoded回执。`TestPostgresObserverLegacyCannotIssueHandle`验真实门本身；将来只有存在另行审定、受信且真实可达的handle来源后，才能执行完整Observe legacy PG验收。包内sealedhandle纯测试只能证明分类算法，不能称真实handle／数据库已验证。此边界保留原legacy观察要求，并未将其删除或视为完成。

## 4. fresh snapshot／连接和无副作用

新 test-only tracer 仅记录调用阶段、SQL模板、backend PID、Acquire/Release次数、TxStatus和context期限，不记录参数、取消secret或业务payload，不执行SQL。每次显式 Observe：

1. 原 unknown backend 已消失；observer在原Runner pool恰一次Acquire，backend不同于旧PID。
2. 真实 driver 下发 `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY`，一次固定3s/5s设置，全部查询同PID、同TxStatus=T，然后ROLLBACK且idle才Release。无COMMIT、锁、root、DML或业务回调。
3. 工作context继承调用方且最多5s；Acquire至全部读证共用同deadline。健康路径恰一次Release，所有行排流；结束后用独立APP查询证明pool健康且不泄连接。
4. 相同handle可由调用者显式再次Observe，每次独立RR RO和一次Acquire；不是自动轮询/重做。前后34表完全一致，唯一operation/attempt/资金效果及wire drop计数保持。

nil/zero/foreign Runner handle、nil／取消ctx、读异常、panic/cleanup资源归属的纯协议矩阵由observer实现测试负责。实际PG可补无Acquire的nil/zero/foreign反例，但它们不算业务数据库成功。并发提交跨快照一致性若未实证须保留；记录准确SQL/RR语句不能单独称并发snapshot验收通过。

## 5. 证据和交付状态

测试遵守原RC3s/5s mutation与180s fixture，不增加安装权限，原SQL和材料SHA不变。先纯编译／wire控制，PG只由集成人以专用PG16、race、`-p 1`串行执行。JSONL逐项记录真实PASS/SKIP/FAIL；所有首轮失败和修订保留，专项复验不重复加总。

本页最低实际矩阵不足以关闭完整A4/A5、全部grant/错误优先序、active cap/跨UTC周期、PB完整配置观察历史、真实40P01/40001、生产部署/供应商/支付或性能门。集成人同步统一架构Observe模块/链路/权限/状态与验证记录后再reviewed refresh；图中不得把显式只读观察变成默认在线恢复或派发边。

## 6. 本片实际结果

集成人串行首轮实际PG为6顶层/21子项PASS、0FAIL/SKIP、77.623秒。随后修复codec主因分类并按相同名单复验，最终为6/21、0FAIL/SKIP、29.357秒；原件`.local/a5-observer-final-pg.jsonl`为30500字节，SHA-256 `47d7e2bd6c44fcf0d4790bbbdb695a43b140039eb35d595102613c10728c259c`。每run恰有唯一且有序PASS，首轮与最终轮保留且不重复加总。六项准确名单、全部源码与原件摘要见[机器记录](../../docs/verification/procurement-execution-observer.json)。

§3除保留的错关系/缺origin/额外反向边真实PG矩阵外，其余本片可达场景已按实际实现运行；legacy项通过的是capture前拒绝，无完整Observe legacy handle。§4实证fresh PID/一次Acquire、真实RR RO、固定设置、同deadline与连接、Rollback/idle/Release和34表无变化；并发提交交错未实跑，不将协议追踪扩大为该项通过。每个unresolved保留原unknown COMMIT cause，失权另保留原42501，未发布业务结果。

剩余历史/兼容矩阵按事实保留，不将它们自动新增为MVP功能开发的阻塞前置；实际下一工作由[交付计划](../../docs/design/delivery-plan.md)与用户最新优先级确定。

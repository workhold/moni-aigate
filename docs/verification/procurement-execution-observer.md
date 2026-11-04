# 显式采购 Observe：只读回执限定验收

2026-11-04。先冻结[观察合同](../../specs/023-periodic-products/b-procurement-executor-observation-contract.md)与[实际APP验收合同](../../specs/023-periodic-products/b-procurement-observer-acceptance-contract.md)，再实现 `Runner.Observe`。源码、全部测试名单、命令、源码与原件SHA、保留失败和未完成矩阵见[机器记录](procurement-execution-observer.json)；前一轮Runner/业务适配结果仍见[历史记录](procurement-execution-runner.md)，不重复相加。

## 已实现范围

`Observe(ctx, handle)`只接受同一Runner发行的过程内opaque handle。handle来自完整decoder之后的真实COMMIT未知，不能以外部ID、SQL、序列化token或另一Runner的材料查询任意业务。观察只返回 `State/Reason` 两个安全枚举，没有Attempt、Command、身份或派发许可；原调用仍是unknown，既不重做根，也不触发模型或后台生成。

[生命周期](../../internal/procurementexecution/observe.go)执行一次新Acquire、最多5秒共享工作context、RR READ ONLY事务、固定3s/5s设置、同一快照读证和Rollback；健康idle后才Release，失败按同一个最多5秒清理预算销毁。Release调用转移所有权后不再读/Hijack/Close该Conn，外部pool hook自身须遵守pgx不panic约定，其资源恢复不属于本片保证。

[固定投影和codec](../../internal/procurementexecution/observe_rows.go)认证14组列名、顺序、OID、NULL联合、准确基数及排流；[共享关系](../../internal/procurementexecution/observe_shared.go)核root/step/journal；[账户](../../internal/procurementexecution/observe_account.go)独立重建原Go JSON SHA256、比较不可变旧快照/source/audit/origin；[attempt](../../internal/procurementexecution/observe_attempt.go)核准确五字段/四身份、managed或unmanaged来源及采购关系。最后仍调用原固定invoker validator，客户端正向row存在不能替代其反向关系检查。

普通New、main/provision、HTTP、恢复任务、001–022迁移及固定A2 SQL/catalog/ACL/摘要均未因Observe启用而改变。当前validator只认证合同已有PB金额、桶及来源关系，不覆盖完整PB operations/configuration/B-P1观察整图。

## 实际验证

| 验证 | PASS顶层 / 子项 | 范围与耗时 |
| --- | --- | --- |
| 协议包完整纯race | 65 / 1427 | 1.743s；包含原S1/S2的36/79及新增Observe的29/1348，不把账户/attempt/codec专项再相加 |
| 生产Observe实际APP专项 | 6 / 21 | 29.357s；真实PG16，实际APP LOGIN、原180s fixture和3s/5s mutation预算，集成人串行复验 |

均为0 FAIL/SKIP，每个run恰有唯一且有序的PASS终态。最终完整纯包原件 `.local/a5-observer-pure-final.jsonl` 为1831606字节，SHA-256 `bd8d2b022371cb33ff93931a8ce777697f0b6f139c44ea51aedaeebfb47dabea`；最终真实PG原件 `.local/a5-observer-final-pg.jsonl` 为30500字节，SHA-256 `47d7e2bd6c44fcf0d4790bbbdb695a43b140039eb35d595102613c10728c259c`。这不是整个A2/A4包、整仓或远端CI的重跑计数。

首次集成纯race为63/1383、2.238s；首次实际PG为6/21、77.623s，均已通过。随后新回归揭示排流错误分类问题，修复后按完整纯包和原六项实际PG名单复验得到上表结果；两轮分别保留，不重复相加。

实际PG由[测试](../../internal/procurementattemptbirth/observer_postgres_test.go)与[被动trace装配](../../internal/procurementattemptbirth/observer_wire_test.go)给出以下证据：

- create及其重放；configure changed/noop/revision_conflict/not_found与各自重放。真实后续configure/managed reserve之后，旧create仍按原不可变回执观察，不把今天账户当作旧snapshot。
- unmanaged、managed无control、已有control的新allocation、原Commander后配真实legacy_import，以及tracked replay改变有效route/ignored quote仍保持准确旧回执。没有owner补origin、改时钟或伪造PB来源。
- 三根均有COMMIT发送前断开反控：root已完整decode、`commitStarted`已置位，准确frontend gate在任何COMMIT字节送socket前关闭真实连接；原backend消失、34表不变，真实unknown的handle观察为receipt_absent。它是未提交反控，与数据库确已COMMIT后截断完整真实ACK的正控分开，不把frontend意图或EOF当作提交证明。
- 正控保留原wire的真实COMMIT帧认证和准确CancelRequest首包审计。每次Observe新PID、恰一次Acquire、真实RR READ ONLY语句、固定设置、同一工作deadline和连接、末端Rollback/idle/Release；被动tracer不读bind参数、不执行额外SQL。每次观察前后完整34表相等，原unknown和其handle不变。
- command/attempt必要列及各自validator EXECUTE四种准确撤权，实际权限为false；Observe返回unavailable、保留真实42501及原unknown COMMIT cause，恢复完整原ACL后同handle显式再观察成功。nil/zero/同pool新Runner/复制Runner在Acquire前拒绝。

`receipt_present`只证明该新快照中的准确匹配原件存在，不证明原调用是applied还是replay、不确认原调用的COMMIT、不授予派发或重试。缺行也不证明原事务永远不会提交。测试没有模型发送。

## Legacy、失败历史和仍开放的门

真实before-install原Reserve/RecordAttempt产生严格legacy后，固定A2根先尝试 `INSERT ... ON CONFLICT`，其BEFORE guard先认证request；真实legacy request在decoder/COMMIT前即PP906。因此 `TestPostgresObserverLegacyCannotIssueHandle`实证的是零DTO、无COMMIT、无有效handle及34表不变的capture前置门，**不是生产Observe对真实legacy的完整PG验收**。包内sealedhandle可检分类算法，不能伪称真实handle；完整legacy观察要求继续保留，须有另行审定的受信可达来源后再验。

首轮纯生命周期fixture错误地把原含点/引号schema覆盖成 `schema`，固定invocation复核在Acquire前正确返回invalid_handle，后续生命周期断言因此失败；删掉测试中的覆写后通过，没有放宽生产seal。attempt专项最早两个relation fixture把整数previous_ordinal统一换成字符串，或把仅要求非NULL的previous_id换成另一非NULL字符串，不能代表真实严格codec的合法坏值；分别改同类型增量与NULL反例，生产attempt实现未因此改变。其独立初跑仅有工具会话输出，相同两项失败及生命周期失败则完整保存在集成首轮原件 `.local/a5-observer-pure-first.log`；不伪造独立失败JSONL。一次更早测试括号语法修复及并行代码尚未齐全的静态构建失败均未运行PG。

新增红轮另确认了实际实现问题：本地必填NULL、无限时间或非规范xid解码错误与晚到真实数据库错误合并后，遍历整棵cause树可能把主数据库错误归为protocol；反向地，Query首错也可能被排流次错改变分类。`observerPrimaryError`先冻结主因分类，再保留所有原cause，确保晚到服务器错误优先于本地解码，而Query已失败时保留其首因。两个新顶层含44子项的红轮与修复后的codec专项6/940、1.658s原件均保留；最终完整pure和实际PG在该修复之后重跑。该问题始终返回unresolved，没有错误发布receipt_present。

真实PG首轮六顶层全部通过，无失败轮拼接或重复计数。完整A4/A5/S4仍开放，至少包括：

- genuine legacy的完整Observe、伪legacy/缺source/root/audit/journal/额外反向边及坏关系PP902/PP906的实际PG矩阵；本轮没有通过owner破坏已提交图补证，纯客户端反例不能替代这些门。
- 并发提交下实际一个snapshot不拼证的端到端反例；真实RR READ ONLY和同PID追踪不单独等价于已验全部并发交错。
- 全部逐grant/组合错误优先序、后继dispatch/finalization/release/cost/recovery支持域、真实40P01/40001、PP108财务cut、active cap/跨UTC周期及完整PB配置观察历史。
- 可信PB异常域版本方案、全域profile/R2正式迁移与运行接线、真实供应商/支付、1Panel/HA及最终性能。

最终PG复验后的集成人清理证据 `.local/a5-observer-cleanup.json` 确认临时schema/role/backend均为0，installer的global default PUBLIC function EXECUTE恢复true；PG16.15、Asia/Shanghai，fsync/synchronous_commit/full_page_writes均on，原件摘要见机器记录。统一架构正文、模块链路和内容快照由集成人复核后统一更新。

上述未验范围保留为事实边界；按[ADR-005](../decisions/ADR-005-pre-mvp-single-path.md)暂缓扩展该历史候选，不再阻塞MVP经营功能。后续按[当前交付计划](../design/delivery-plan.md)推进经营报表等用户能力。

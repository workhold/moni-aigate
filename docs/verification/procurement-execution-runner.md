# 显式采购执行器：事务与业务适配限定验收

2026-11-04。先冻结[事务合同](../../specs/023-periodic-products/b-procurement-executor-transaction-contract.md)、[实际APP验收合同](../../specs/023-periodic-products/b-procurement-executor-transaction-acceptance-contract.md)与[业务适配合同](../../specs/023-periodic-products/b-procurement-executor-adapter-contract.md)，再实现和验证。源码为[Runner](../../internal/procurementexecution/runner.go)、[billing适配](../../internal/billing/prospective.go)、[采购适配](../../internal/procurement/prospective.go)与[HTTP错误边界](../../internal/server/procurement_execution_errors.go)；机器计数、完整顶层名单、源码/原件SHA及保留失败见[JSON记录](procurement-execution-runner.json)。

## 当前交付与限制

已实现固定三根显式事务及两个 `NewProspective`，只有确定COMMIT成功才向原方法发布DTO。S1协议语义不变；包注释改为包含新的Runner。普通New、main/provision、正式001–022与固定A2 SQL/catalog/ACL/摘要均未变，不自动探测或错误后fallback。其它Reserve/BindCredential/余额观察/PB Commander不由这个字段切换。

未知COMMIT返回零DTO与安全typed `procurement_outcome_unknown`，原cause及opaque handle可由受信内部代码取得，不自动重做、观察或调用模型。HTTP业务错误与unknown优先于原始23514；推理admission也优先识别unknown，保留context cause不能抢先降级504。原context和队列错误仍504/429。handle及UnknownOutcome的fmt/JSON/Text不输出身份或参数。

[只读Observe详细合同](../../specs/023-periodic-products/b-procurement-executor-observation-contract.md)本轮仅设计：14组固定投影、原Go JSON SHA256、同一新RR RO快照、准确回执与反向来源、缺行/PP902/PP906边界。测试中的独立APP只读事实oracle不是生产Observe。现有validator只认证既定PB金额/来源关系，不覆盖完整PB配置/operations/B-P1观察整图。完整A4/A5/S4、PB异常域兼容、全域profile、R2与供应商/生产验收继续开放。

后续生产Observe已实现并完成限定实际APP验收，准确范围、保留矩阵与独立计数见[观察器续片](procurement-execution-observer.md)；本页保留S2/S3当期结果。

## 实际运行结果

专库为原独立PG16 LOGIN/database、127.0.0.1:55439；实际APP由原fixture核 `session_user=current_user=随机APP`，W保持NOLOGIN。根集成人串行执行数据库测试，无新grant、SET ROLE替代身份、时钟压缩或手工来源补造。fixture仍180s，业务锁/语句仍3s/5s。

| 验证 | PASS顶层 / 子项 | 时间及准确范围 |
| --- | --- | --- |
| 协议包完整纯race | 36 / 79 | 1.326s；包含原S1的20/56与S2的16/23，不重复相加 |
| billing/采购/HTTP适配纯race | 11 / 64 | 三包分别1.546s/1.333s/1.830s；无连接构造、原validator/接受集、BYTEA/大整数、零DTO、cause/As/Is与HTTP优先序 |
| 本轮实际APP＋纯wire专项 | 9 / 29 | 16.943s；其中实际PG为7顶层/15子项，纯wire为2顶层/14子项 |

以上均0 SKIP/FAIL，各run具有唯一PASS终态。它们不是当前整个仓库或整个A2/A4包重跑计数；前一A2/A4记录保留其独立版本与范围。专项命令与完整名单见机器记录。

### 数据库与网络证据

- 账户根create、changed/noop、revision_conflict/not_found与重放均比对原不可变Command reader；原create在账户后来修改后仍返回旧快照，重放全34表不变。
- attempt管理/非管理、ignored quote任意BYTEA与旧TEXT重放逐值保真；实际APP固定三根在原权限闭包内运行。
- 真实deferred权限42501与外层COMMIT23514先由原APP“已解码、尚未COMMIT”的反控定位，再核Runner返回零DTO、保持原cause且不伪称unknown，全34表回滚并保留旧已提交前缀。
- create与managed attempt两种真实socket场景：在frontend COMMIT发送前准确暂停，owner看到原backend为idle in transaction且34表未变；释放后截下数据库真正成功的完整C/COMMIT帧，Runner零DTO/typed unknown，原backend消失，独立新APP RR READ ONLY读到唯一已提交command/attempt/账户/采购/PB事实，无模型调用。
- 所有传输首包逐条认证：只有一个PostgreSQL Startup业务会话。pgx在EOF清理时的另一条连接只携带准确16字节CancelRequest，原PID与SecretKey逐字节匹配，secret不输出；等待原PgConn.CleanupDone后核完整分类。第二Startup、错PID/secret/长度或未知协议仍失败，不能简单允许任意第二连接。
- 业务适配通过真实APP创建/配置账户，候选RecordAttempt产生客户hold120与采购hold240；共享采购limit300的第二真实请求被PP115拒绝，billing.Error与原PG cause均保留且全34表不变。幂等冲突使用原版本创建事务中同时发布的另一合法version/credential pool，先通过原membership检查再得到PP116；不禁用原immutable trigger。binding是fixture准备的原父对象，不扩称本片验过APP BindCredential全流程。

Runner仅attempt在准确40P01/40001、确认回滚且live idle、原ctx有效时最多三次；账户零重试。纯状态机覆盖查询/codec/排流/取消/提交/rollback/close、原ctx不替换、unknown优先及panic清理。rollback与必要Close共用一个绝对5s预算。Release调用后连接所有权已转移，绝不再读/Hijack/Close；外部pool Tracer/AfterRelease等须遵pgx不panic约定，Runner不虚称可恢复任意外部hook的资源。

## 保留失败及最终环境

首轮两个lost-ACK控制把驱动CancelRequest误计为业务重连；诊断证实准确1查询/1真实回执/1丢帧。校准为上述严格首包分类后通过，没有直接放宽重连断言。适配反例首轮使用非成员credential，原PP101正确先于PP116；随后尝试向已创建版本补membership，被原P0001正确拒绝。最终使用单个原事务创建独立version及其credential pool，不改原冻结规则。一次并行编辑期间的构建失败没有运行数据库。所有原件路径/SHA与每轮原因均保留在机器记录，不从失败轮拼出最终PASS；最终完整9顶层专项在同一进程通过。

结束后 `ab_*` schema/role/backend均为0，installer的global default PUBLIC function EXECUTE恢复true。PG16.15、Asia/Shanghai，fsync/synchronous_commit/full_page_writes均on；详见机器记录中的cleanup原件。实际容器/1Panel、正式供应商/支付、完整观察与后继历史门禁不由本轮局部测试替代。

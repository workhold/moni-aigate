# 022-C0：固定候选隔离回归合同候选

2026-11-01，**根已冻结本合同的直接SQL层与§7四个仅测试文件所有权，授权实施及专项race；worker staged层选择延后正式helper回归，不复制生产源码、不加hook**。本片把[候选专项证据](../../docs/verification/funding-lot-candidate.md)中有价值的资金/权限机制变成版本化回归；不修改生产迁移、billing/payments实现或普通main，不把测试proof冒充完整runtime认证。候选SQL/ACL仍以[维护合同](c0-maintenance-contract.md)、[全序合同](c0-sequence-contract.md)及[lot合同](c0-lot-contract.md)为准。

## 1. 两层证据与实现先后

先落地**直接SQL资金机制层**：真实001–022、固定候选DDL/ACL、随机schema及实际LOGIN；测试fixture按真实业务source/journal/audit/outbox合同写入，逐笔确认提交结果与余额/lot/sequence。该层能证明SQL提交门禁和最小权限，不能证明Go parent token、HTTP授权或完整监听前proof。

**sandbox staged seam层延后正式helper回归**。现scratch复制payments代码并插入单一append seam已证明候选SQL和真实worker来源链可共同提交；大段长期复制会漂移，不将该副本移入版本库。最终正式C0 helper接线后，由原payments真实package回归取代staged层；在此之前不得给生产代码加可配置开关或stub注入出口。

两层分别命名、分别报告，不能合并成“C0完整运行时通过”。本片不验商业主进程、维护Permit、proof发行、真实供应商/PSP或独立VerifyLots。

## 2. 固定环境与生命周期

仅显式opt-in环境`C0_CANDIDATE_DATABASE_URL`启动；未提供时测试Skip并说明范围，结果不能记录为已通过数据库验收。解析连接参数后须在任何写入前硬核：

- host严格为TCP `127.0.0.1`、port严格`55439`、database严格`moni_test`；拒绝Unix socket、DNS名、多host、service重定向及其他地址，不自动尝试候选库。
- 实际连接后复核`current_database()`、`inet_server_addr()`、`inet_server_port()`和`current_user=session_user`，并核owner具有安装/临时LOGIN管理所需权；不在失败后切换其他DSN或提升运行身份。
- 每轮由crypto/rand生成固定前缀+128-bit随机schema及APP/worker/projector LOGIN名；运行账号必须真正独立连接，不使用`SET ROLE`模拟。口令只在内存生成和传递，不输出DSN、密码或认证材料。
- owner只做安装、原静态seed、显式cutover/proof fixture与清理；正常/恶意资金实验用实际APP或worker。owner特权负例单独标记，不混称APP安全性。
- 测试本身不`Parallel`，不扫描或清空其他同前缀对象，不改变默认schema，不安装到public。初始化失败同样必须清理已创建对象。

连接与每场景都继承整体有界context；默认整轮不超过120s、单正常事务不超过5s，故障预算按具体用例另列。关闭所有Rows/Batch/tx/pool/连接后，使用与已取消测试context独立的至多10s cleanup context，依本轮精确对象清单删除schema/LOGIN，再从catalog核本轮对象为零。清理失败必须使测试失败并只输出非秘密对象名，不能将其吞成成功。

## 3. 安装与候选身份

在随机schema先运行仓库真实`migrations.Apply`到当前001–022；本合同绑定这一基线。若未来新增迁移，测试必须明确失败并要求重新审查候选basis，不静默在新基线上继续。安装前后核原迁移数及原函数OID/body没有被候选改写。

测试从仓库固定路径读取以下文件，不保留DDL副本，不允许环境覆盖路径：

| 文件 | 本次候选SHA-256 |
| --- | --- |
| `specs/022-self-service-payments/c0-production-schema.sql` | `e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047` |
| `specs/022-self-service-payments/c0-production-permissions.sql` | `9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae` |

版本化测试文件将这两份摘要作为明确fixture版本；不匹配时在执行候选前失败，根语义复核并重跑专项后才更新摘要，不从当前文件计算摘要后无条件当预期。候选ACL的四个psql identifier占位符只用`pgx.Identifier.Sanitize`替换为本轮schema/roles，替换后若仍有未识别占位符即失败；不得通过额外GRANT补候选权限漏洞。

原001–022角色授权使用原固定SQL；C0仅追加候选ACL。tenant APP INSERT为原八列，UPDATE仅为原六列`name,status,allowed_regions,price_group_id,pricing_revision,pricing_operation_id`，不新增UPDATE(id,created_at)；worker既有SELECT/UPDATE(id)锁权限不动。owner不能给APP/worker OWNER/SUPERUSER/BYPASSRLS/CREATEROLE、角色membership或任意函数执行权以让用例通过。测试连接均显式固定`search_path=<本轮schema>,pg_catalog`，不借用其他轮对象。

## 4. fixture与oracle边界

需要五类fixture，并分别注明创建身份与时机：

1. 原静态用户/角色/tenant/model/Key/price/control seed：owner在候选前创建；无实际调用或收款。
2. 原APP资金事实：APP建立真实manual credit/journal/receipt/audit/credited，reserve/settlement/refund及其完整原投影；不能owner代写后声称APP路径通过。
3. 维护cutover：owner显式登记基线opening（含零）、全部legacy membership、epoch/head及test-only v2 proof；canonical与digest显著标记fixture，不触发生产初始化或向外发行Permit。
4. 新APP钱包图：APP建立实际source和operation/allocation/event/cache/head；fixture只帮助构造完整图，正常预期用手工业务数值计算，错误用例只省略/扰动目标写集，不因checker报错而自动补图重试。
5. sandbox worker权限：直接SQL层的独立ACL用例显式安装原sandbox marker/v1 proof fixture，使用原`ProvisionPaymentWorkerFor`及`VerifyPaymentRole`先核三角色，再安装C0候选ACL；只证实际worker原tenant SHARE与负权限，不运行worker。原B签名/identity/selection/capture/effect/receipt/audit/processing真实链仍是旧staged专项证据，正式helper后另验，不复制生产源码或给正式main注入proof。

fixture构造器不是独立Verifier。正常断言必须读持久source/journal/buckets/head/原Key.spent，使用独立已知金额及行数/ID精确关系；可以额外调用候选SQL全图checker观察其自洽性，但不能把该函数的返回作为唯一正确性判据。并发/全量长期回放与Go独立Verifier另片验。

## 5. 直接SQL层必选用例

每个失败场景先保存该tenant原钱包、lot buckets、head及关键source行数，再在独立事务构造违规图；要求COMMIT返回精确SQLSTATE并确认全笔原子回滚。事务失败后用新事务读取，不在aborted tx内取快照。

| 用例 | 精确验收 |
| --- | --- |
| 多lot正常链 | 人工60+40；reserve70按FIFO取60+10；finish收费65释放5；分次退款3+4逆原charged边；余额/桶/sequence及退款累计精确 |
| FIFO与逆退款违规 | 保持原source/journal完整，只交换错误来源或逆退款顺序；COMMIT 23514且原账本/全部C0效果回滚 |
| 零reserve+finish | 同事务两笔零journal和operation、无allocation/event、head连续；漏零operation或漏head分别23514 |
| 原图无event改cache | 实际APP无新event修改cache；23514，余额/revision不变 |
| 旧零tenant开账遗漏 | 真实候选前创建零tenant；init漏其含0 opening或伪装tenant_genesis均23514，epoch/init整笔回滚 |
| 新tenant出生 | APP用原八列INSERT自动genesis；APP显式写birth列及UPDATE(id,created_at)均42501；owner正常UPDATE受保护birth为23514 |
| SAVEPOINT top xid | 真实SAVEPOINT内tenant INSERT及manual/零reserve+finish/refund，观测journal/protected op的top xid相等且相关tuple xmin可不同；全部提交，不用xmin作为保护依据 |
| 新退款省累计 | 完整source/journal/lot/event/head仅省settlement.refunded_amount更新；23514，stored/sum仍7 |
| legacy累计 | 候选前APP入账140、reserve/charge100、已退20；完整cutover available60；候选后legacy退30、50，累计20→50→100；中间省counter的1拒绝；再退1业务拒绝、旧approved重读不新发lot、Key.spent仍100 |
| 最小权限 | APP不得改受保护stamp或历史immutable；worker无人工/券/request/消费退款/总legacy读权及existing cache UPDATE；projector全部16表/列数据权限与全部C0函数 EXECUTE 为零，named `fl_source_proof` 的 USAGE 为false；非法读/写/执行用例期待42501，不能以对象不存在42703代替 |

缺少某个用例不把对应scratch证据标成自动回归完成。原source不能在此前已提交而新operation单独补写；这会被同top xid合同拒绝。测试helper不借更新原journal或复用旧ID绕过。

本层类型负权限断言只核named `fl_source_proof`，不要求全部新增type的有效 USAGE 为零。后续[descriptor专项证据](../../docs/verification/funding-lot-descriptor.md#权限与依赖的实际观察)另记录PG16实际矩阵：projector对16个自动table row type及其16个数组的有效 USAGE 为true、grant option为false，对 `fl_source_proof` 及其数组为false。raw `typacl`/默认项与有效权限分开核，array有效 USAGE 由element类型权限决定；类型 USAGE 不授予表 SELECT 或函数 EXECUTE。本层真实LOGIN负例直接读取 `funding_lots`、调用 `fl_require_epoch()` 均应42501，但未逐一执行32个自动type的读取绕过探针；不能将metadata观察改写成该类探针已通过。

## 6. staged worker层裁决

根选择**延后正式回归**：保留已固化的scratch证据为历史专项；直接SQL层先版本化，等正式C0 helper完成后在原`internal/payments`测试中验真实完整runtime。当前worker来源链+C0 append组合尚无版本化自动回归，不能将直接SQL权限用例代替此关口。

本片不新增复制runner、payments副本、生产`func`变量、public hook、trusted bool、环境开关、全局test proof或runtime跳过分支。

## 7. 已冻结文件所有权与交付

直接SQL层由本分片独占下列**仅测试文件**，已获根授权实施：

- `internal/qa/c0candidate/candidate_test.go`：opt-in、固定专库检查、候选摘要、总流程与用例。
- `internal/qa/c0candidate/fixture_test.go`：随机schema/实际LOGIN、真实001–022安装、原静态seed与清理。
- `internal/qa/c0candidate/wallet_fixture_test.go`：手写source与C0图fixture、独立金额预期，不导出给生产包。
- `internal/qa/c0candidate/legacy_fixture_test.go`：真实旧APP事实与显式cutover fixture。
- 本合同及`docs/verification/funding-lot-candidate.md`：记录命令、最终文件摘要、实际结果和限制。

不取得`internal/billing`、`internal/payments`、`migrations`、Makefile、CI或统一架构快照的写权。测试包无非test Go文件，普通main不依赖本包。

建议显式命令：`C0_CANDIDATE_DATABASE_URL=<专用测试库连接> go test -race ./internal/qa/c0candidate -count=1 -v -timeout 150s`。命令示例不含实际凭据。是否加入Makefile/CI由根在真实专库与清理策略就绪后决定；不让默认CI自动创建数据库或refresh架构。

交付前根汇总全部分片，复核统一架构的candidate/已实现边界、测试入口及源码链接，再执行仓库要求的显式refresh和只读检查。本片不改架构能力状态为C0已完成，不把测试安装SQL写成新增生产migration。

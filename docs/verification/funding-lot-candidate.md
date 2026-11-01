# 022-C0 资金来源候选：隔离 PostgreSQL 验证

2026-11-01。本记录固化生产 SQL/列权限候选的专项证据，**不代表 C0 已接入生产资金路径或完成 MVP 商业验收**。根已接受修正方向，尚未授权生产迁移。候选没有迁移号；现有 001–022、billing、operations、main 未因本片修改。完整资金、维护、独立核验和运行证明仍需后续逐项验收。

审阅入口：[生产候选](../../specs/022-self-service-payments/c0-production-schema.sql)、[列权限](../../specs/022-self-service-payments/c0-production-permissions.sql)、[维护合同](../../specs/022-self-service-payments/c0-maintenance-contract.md)、[全序合同](../../specs/022-self-service-payments/c0-sequence-contract.md)、[lot 合同](../../specs/022-self-service-payments/c0-lot-contract.md)、[已冻结直接SQL回归合同](../../specs/022-self-service-payments/c0-candidate-regression-contract.md)、[仅测试入口](../../internal/qa/c0candidate/candidate_test.go)。

## 1. 对象与证据版本

候选在真实001–022之后追加16表、41个 SECURITY INVOKER 函数、1个 composite type；给既有tenants精确追加一列受保护的birth xid8及trigger，APP INSERT保持原八列、UPDATE保持原六列；不授UPDATE(id,created_at)或birth列写权。原函数body/OID和022 immutable v1 attestation不改写。

| 交根审文件 | SHA-256 |
| --- | --- |
| c0-production-schema.sql | e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047 |
| c0-production-permissions.sql | 9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae |
| c0-maintenance-contract.md | 9d213377cce865a6dfc875fb93abe9a0fc77ff0cd36e42ed21726984e4eeaeed |
| c0-sequence-contract.md | 332bece7e3b3c0d2da7d047ffae54d24b7a20f9b6a7cefb8ff7b3f22f601d9ba |
| c0-lot-contract.md | 1ccdf9d2f3ee07d80abd380f11add6e78208e8df2f1c987439bf90533cf5b8e3 |

合同文件的摘要定位本次证据版本，后续根审修订可改变摘要；SQL/ACL改动必须重新专项验证。维护basis descriptor为基线109表、976列，仅元数据；原canonical JSON摘要为`54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5`。新增tenant birth列由C0静态delta独立核验，不混进旧basis。

## 2. 环境和角色边界

- 只连接固定专用测试PostgreSQL：TCP `127.0.0.1:55439`、数据库`moni_test`；每次独立随机schema，不改运行实例。
- owner仅安装DDL、原静态seed、显式fixture初始化及清理；资金实验使用随机临时实际APP或worker LOGIN，`current_user=session_user`，不通过SET ROLE模拟运行身份。
- LOGIN口令在进程内随机生成，未写入源码、报告或输出；审计不输出DSN凭据、签名密钥、支付正文或认证材料。
- 初始化proof canonical、basis/manifest/descriptor digests明确为fixture；它们只让SQL合同可隔离运行，**没有通过完整proof发行、NewService/OpenProcessor/main启动或维护Permit认证**。

当时scratch命令为`go run ./.local/c0-production-stage`、`C0_ZERO_LEGACY_FIXTURE=1 go run ./.local/c0-production-stage`、`go run ./.local/c0-legacy-stage`；worker为专库环境下`go test -race ./.local/c0-worker-stage -run '^TestC0CandidateActualSandboxWorker$' -count=1 -v -timeout 90s`。scratch不属于版本化回归入口。直接SQL核心场景现已按独立合同转为[版本化回归](#8-版本化直接sql回归)，worker staged来源链仍仅为历史专项，待正式helper后在原包验收，不复制生产实现或新增hook。

## 3. 实际APP普通来源、FIFO与序列

真实001–022安装、候选装载及显式零opening初始化通过。实际APP以完整真实持久来源构造两笔人工核款：pending credit→approved、journal/accounts、receipt、audit/outbox，并同事务追加lot/event/cache/operation/head。

| 正常或完整性场景 | 实际结果 |
| --- | --- |
| 入账60+40；FIFO reserve70，取60+10 | 提交成功，原allocation次序明确 |
| 结算65、释放5 | 按原allocation正序收费，终态边完整 |
| 分次消费退款3+4 | 沿原charged边逆序恢复，累计完整 |
| 错FIFO、错逆退款 | 提交时23514，全部资金及新图回滚 |
| 同事务零reserve+finish | 两份operation及原零journal，0 allocation/event，连续推进head |
| 漏零额operation、漏head | 提交时23514 |
| 无新event的cache UPDATE | 23514拒绝 |
| 新tenant INSERT | 原子生成seq0/genesis/head |
| 新tenant在真实SAVEPOINT内INSERT | tenant birth与operation均为top xid，tuple xmin不同仍成功 |
| manual funding、零reserve/finish、消费退款在真实SAVEPOINT内完成 | 原journal.created_txid与protected operation.created_xid精确相等，tuple xmin不同仍成功 |
| APP写birth列；owner正常UPDATE birth | 分别42501与23514，stamp不可手填或修改 |

这些是直接SQL的真实来源/事务合同验证，不证明所有HTTP授权、生产Go helper接线或独立Go VerifyLots。最终owner调用SQL全图checker只验证候选内部一致性，不能作为独立核验器的正确性oracle。

## 4. 根复核发现与修后结果

### 4.1 旧零钱包tenant的完整开账

修前`cadc259...`候选仅比较epoch counts和已记录opening，旧零钱包tenant可漏含0 available opening，并被错误记录为tenant_genesis；计数仍自洽。该候选没有冻结。

修后追加`tenants.fl_created_xid xid8 NULL`，不回填旧行；新INSERT无条件写受保护top xid、UPDATE禁止改变。tenant_genesis必须绑定该非NULLstamp，epoch_opening仅对应NULL旧tenant；epoch/global checker用全部NULL旧tenant与含0 available opening FULL JOIN核精确集合。相同遗漏在真实PG初始化时23514拒绝，整个init回滚。

### 4.2 退款累计必须由source入口触达

根授权的正常完整性回归确认：修前完整新退款图省略settlement.refunded_amount UPDATE仍能提交，stored=7、approved sum=8，只有事后global checker检出。

修后`fl_source_refund`自身核`SUM(approved.amount)::NUMERIC = settlement.refunded_amount <= charged_amount`；legacy同时核cutover approved基线与全部新批准累计。相同遗漏现在提交时23514，原stored与sum均保持7，新source/journal/lot/seq全部回滚。正常3+4分次退款继续成功。

### 4.3 原journal与新operation的同事务绑定

根静态复核要求非genesis operation显式绑定原journal的top事务。修后通用checker在source精确绑定之后核id/tenant与`j.created_txid::numeric=o.created_xid::text::numeric`，不符固定23514 `funding_lot_operation_transaction`；原002完整journal lines的current-tx guard提供既有见证。seq0不含journal，独立走birth/opening证明，不为所有旧source扩散birth列。

修订后正常APP、历史累计、worker -race和旧零tenant初始化回归全部通过。APP另外在真实SAVEPOINT中同事务执行人工入账、零reserve/finish及消费退款，并直接观察journal/op top xid相等而tuple xmin不同。voucher真实入口仍未验，不从其他来源的通过推导。

### 4.4 tenant UPDATE列权限不得扩大

独立读审发现旧`d12cb73...`候选将APP tenant UPDATE也授为八列，给原001–022的六列权限额外增加id/created_at。根裁决先补maintenance/sequence/lot/regression合同，再收紧候选为INSERT原八列、UPDATE原六列；worker原SELECT/UPDATE(id)锁权限不动。schema `e61eb57...`保持不变。

新ACL `9ddeecca...`由§8全部直接SQL race回归验证：APP UPDATE(id)、UPDATE(created_at)、birth显式写均42501；owner修改birth23514；同轮先安装并核验原B角色权限，再应用C0，实际worker `SELECT id ... FOR SHARE`仍成功。该测试未运行worker资金处理，不能替代§6来源链或正式helper验收。

## 5. 历史cutover累计正常回归

实际APP在C0之前的真实001–022上生成：人工入账140、reserve/charge100、已批准消费退款20；原source、journal、audit/outbox完整。随后追加候选，owner显式登记全部历史membership、available opening60及nonrefundable lot，使用明确fixture proof。

切换后实际APP的新legacy消费退款30、50依次成功，累计20→50→100；中间完整新退款1省累计UPDATE被23514拒绝。随后通过原billing.CreateRefund/ApproveRefund申请的1按原`refund_exceeds_charge`业务拒绝。旧approved重读未生成lot；Key.spent保持100；最终SQL全图检查通过。

历史fixture首次漏填refund根的reservation_id被原checker拒绝，补齐正确源根后通过；没有放宽候选校验。该实验覆盖历史terminal和旧approved，不涵盖历史unfinished reservation或全部混合cutover。

## 6. 实际sandbox worker staged seam

staged目录复制当时payments生产实现和fixture；唯一有意实现变更是在原ApplyFundingTx成功之后、原capture audit及processing generation最终CAS之前，追加测试helper的C0图。临时错误诊断已恢复生产原版，再运行race；最终退出0，包耗时2.521s。

实验使用真实原B持久来源、签名通知、identity/selection/capture/receipt/processing及实际worker LOGIN；原funding/processing与C0 deferred guards全部通过。worker没有credit_orders、vouchers、users、request_records、总legacy/request membership、allocation读取或已有cache UPDATE权；也没有genesis/head INSERT权。projector的C0表/列数据权限与C0函数 EXECUTE 为零；类型权限不能概括为全部为零，named `fl_source_proof` 与自动table row type/array的实际边界按§8及后续[descriptor观测](funding-lot-descriptor.md#权限与依赖的实际观察)区分。

原fixture仍通过私有runtime注入，proof canonical为显式stub；staged append不属于生产代码。这项通过不等于完整NewService/OpenProcessor/main装配或正式供应商认证，不能在架构中写成已实现C0入账。

## 7. 清理与尚未证明的关口

全部专库随机schema/LOGIN经defer或t.Cleanup关闭连接后清理。最终只读catalog确认：`flcand_%`候选schema=0，`flca_%|flcw_%|flcp_%`候选角色=0，`b3svc_%`worker fixture schema=0、角色=0。未运行普通main；未写生产迁移；本分片未refresh统一架构快照。

后续仍需证明：真实voucher入口及其同事务链、历史unfinished reservation与混合cutover、完整Permit/109表basis、静态v1+v2 schema/role证明发行与监听前检查、独立Go VerifyLots、生产helper全部入口、未知COMMIT/崩溃恢复、旧测试显式初始化改造、长历史/碎片/并发EXPLAIN和事务总预算。正式PSP/live、原路退款、拒付及最终性能均未由此验证。

统一架构由集成人汇总后语义复核并更新；候选状态、专项通过和未验关口必须同时保留。

## 8. 版本化直接SQL回归

根已冻结[回归合同](../../specs/022-self-service-payments/c0-candidate-regression-contract.md)的直接SQL层，并授权四个仅`_test.go`文件。没有生产Go helper、迁移或main变更，没有引入支付源码副本或test hook。测试读取固定候选SQL/ACL并pin §1精确SHA；先真实安装当前001–022，前后核原函数OID/body及迁移记录不变。

显式专库命令（连接示例无密码）为：

```sh
C0_CANDIDATE_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go test -race ./internal/qa/c0candidate -count=1 -v -timeout 150s
```

最后一轮退出0，包耗时4.133s；五项均实际运行，没有Skip：

| 测试 | 本轮实证 |
| --- | --- |
| `TestCandidateWalletGraph` | APP60+40→FIFO70→收费65/释放5→逆退款3+4；逐lot/边/钱包/Key.spent/退款累计/head独立数值断言；错FIFO/逆退款、漏counter、漏零operation/head、无event改cache均拒绝 |
| `TestCandidateOldZeroOpening` | 旧零tenant漏opening和伪genesis两种init均23514，全图回滚；完整含0开账随后成功 |
| `TestCandidateSavepointTopXID` | 真实SAVEPOINT tenant出生及人工/零reserve+finish/消费退款，top xid精确绑定且tuple xmin不同；正常提交 |
| `TestCandidatePermissions` | 原B三角色先provision并Verify，再加载C0；APP六列UPDATE边界、stamp/history负权限、worker原tenant SHARE及窄C0负权限；projector全部16表/列数据权限与全部C0函数 EXECUTE 为零，named `fl_source_proof` 的 USAGE 为零；实际projector LOGIN直接读取 `funding_lots`、调用 `fl_require_epoch()` 均42501 |
| `TestCandidateLegacyRefundCumulative` | 实际旧APP140入账/消费100/已退20；cutover60；新退30→50、漏counter的1全回滚、再退50→100；原Go批准再1拒绝；旧approved重读无新lot、spent保持100 |

每个拒绝事务在失败前后比较原账本/source/Key/audit/outbox及全部16张C0表的规范内容摘要；不是仅看某个checker报错。正常金额、分配顺序与数量用手工预期核验；候选全图checker只作附加自洽检查，未用作独立Verifier oracle。

本权限测试只对named `fl_source_proof` 调用 `has_type_privilege(...,'USAGE')`，没有声称检查全部34个新增type为零。后续[metadata descriptor专项](funding-lot-descriptor.md#权限与依赖的实际观察)确认：projector对16个自动table row type及其16个数组的有效 USAGE 为true、grant option为false；对 `fl_source_proof` 及其数组为false。raw ACL为NULL和默认项含PUBLIC不等于array的独立有效授权，array有效权限须按element类型判断。类型 USAGE 不等于表 SELECT 或函数 EXECUTE；上述32个自动type的观测是metadata权限事实，未逐一做实际读取绕过测试，也不改变本次直接SQL回归的验收范围。固定ACL候选中的宽泛“ZERO C0 table/type/function”注释不能解释为所有自动type的有效 USAGE 均为零；本次未修改SQL/ACL或其摘要。

每fixture使用128-bit随机schema及三个实际临时LOGIN，无SET ROLE；资金操作至多5s、整fixture至多120s、独立cleanup至多10s。取消/初始化失败同样注册cleanup，依本轮对象清单关闭连接、删除、再查catalog；测试未按prefix删除任何对象。最终额外只读catalog确认`flqa_%` schema及`flqa_a_%|flqa_w_%|flqa_p_%`角色均0。

未提供opt-in变量时五项明确Skip，不能记录成PG通过。默认CI/Makefile未增加专库自动安装，也不自动refresh架构。本轮仍使用显式cutover/proof fixture；正式helper/完整proof/维护Permit/worker runtime及商业验收等§7关口保持未验。

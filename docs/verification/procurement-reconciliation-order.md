# 人工对账顺序、批准前驱与结算联合

2026-11-04；基线 `9eaabe75092868a610c127f810c4a674b18f559c`。按[实施合同](../../specs/023-periodic-products/b-procurement-replay-implementation-contract.md)与[收口计划](../../specs/023-periodic-products/b-procurement-replay-closure-plan.md)先冻结关系，再实现独立纯Go校验。承接[当前原件与时钟](procurement-current-source-clocks.md)，当前模块与边界见[架构细图](../architecture/procurement-replay.md)。

## 实际实施

[人工偏序](../../internal/procurementprefix/source_closure_reconciliation_order.go)在来源域按case显式索引P/A/R，不依赖UUID、XID或墙钟排序。每action最多一个新source，A消费P真实Input proposal与maker，R保留已知P/A的proposal/actor；A→R只允许由同根、同scope且来源精确相等的pending outcome承接，新/既有settlement之后不能再新R。最后且唯一的人工action决定原case完整pp_last六列。来源间错误与当前case错误按各物理表/自然PK计OriginalPointerRows，多原因不重复；本地角色/状态错误仍计InvalidScalarRows。

当前比较仅涉及last-manual、稳定proposal/actor与允许state。自动pending可改reason，普通finish可关闭非approved case，不要求整个当前case等于最后source.after。A缺P、孤立R消费旧proposal/actor、无新source且last六列全NULL的case保留私有LegacyCases；孤立open R无proposal/actor不要求P。已有tracked P、缺A却R消费approved是确定矛盾；有source缺原case也报pointer。LegacyCases不是完整uncovered集合或来源资格。

[派生桥](../../internal/procurementprefix/finish_replay.go)按incoming ID建RequestDecision多值索引，唯一normal decision才能被消费。source→incoming→decision核同root/xid、tenant/request与严格递增正step，before_latest精确等source.prior_latest，after_latest及finalization精确等本次预分配输出。即使delivery内容相同也不能换前驱ID；NULL、空串分开。该桥不替代RequestIncoming的meter选择、完整decision/sticky语义，也不推断未派生分支的历史cut。

[行内联合](../../internal/procurementprefix/source_closure_finish_unions.go)独立核三action的消费/忽略字段、角色与状态、case/input组及approval派生clock，finish的new/existing/pending、金额/Key、profile created/reused和两个独立optional PB scope。case snapshot保留DDL五status，只有approve消费要求confirmed；原case提案继续独立核验。new/recovery的deadline原因当且仅当decision或financial任一到期，禁止人工release原因；new/release只有financial时钟，未到期为reconciliation，到期为deadline。pending要求decision<deadline；existing时钟NULL且历史reason开放。各clock互不排序，decision到期后financial回退仍保持全零released。

PB文档同时校准原helper合法无效果分支：control存在，但source在跟踪前已关闭、marker为空且无allocation时可以无效果。没有把今天control存在直接等同于历史必有event；历史absence缺证继续保留，没有改helper、DDL或715列。

## 独立复核

独立外部overlay核派生桥的缺incoming/backpointer、重复decision、同delivery换前驱、双向NULL/空串及索引中途取消；race PASS，1.600s。清source输出同时破坏finish.latest时分别计各自物理对象，不将不同义务合并。

人工order只读复核及外部overlay覆盖legacy A→R缺P、孤立R旧proposed/approved/closed proposal、新source不能借全NULL last豁免、每action重复与同PK重复，以及第1/2/4/8次检查取消零结果；race PASS，1.910s。当前reason不参与人工order比较。

SourceFinishUnion的独立overlay发现三项真实漏判：recovery借人工release原因、recovery两clock都未到期却称deadline、直接release financial未到期却称deadline。先补合同，再修实现，并保留仓库内12行clock组合×4reason矩阵和existing接受集回归。原始overlay未改反例复验全部PASS，1.824s；同时保留D到期/F回退、F到期/D提前等合法路径。独立合并manual/union专项race PASS，1.998s。各专项耗时不相加，也不作为首字节或吞吐性能。

## 根集成验证

全部并行文件冻结后，根实际执行：

```sh
env -u TEST_DATABASE_URL go test -race -json ./internal/procurementprefix -count=1
env -u TEST_DATABASE_URL go test -race ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1
```

完整新包 **113顶层、706子例全部PASS，0 SKIP/FAIL，3.990s**；原采购证据checker回归PASS，2.166s。完整包结果为本片集成依据，不将并行专项计数重复相加。独立README只读审查核对Make/Dockerfile/Compose、普通001–022迁移、数据库角色、首次管理员、MFA默认及健康检查，与现有代码/手册一致；没有执行目标环境部署。

根完成模块/数据流/权限/能力语义复核后刷新1758文件架构快照，只读架构检查与`make check`全部PASS：32表715列/68 generated布局、`go vet ./...`、Vue strict与Vite构建。归档本段后再刷新并只读核验，代码、测试和依赖不再修改。

## 完整性边界

来源域仍Complete=false、Qualified为空。人工last-source义务已实施，finish从三项Unimplemented收敛为两项：未派生decision的历史latest/sent观察与历史PB配置/window/absence闭包。没有把完整自动客户状态生命周期扩成新的人工pointer义务，也没有用当前状态倒填历史。其他原件payload/标量/语义联合、ledger/PB精确槽、跨链连续性、逐对象资格、同次Base与aggregate仍待实现；无公共Check，不解除旧十一项Unproven。

本片不访问PostgreSQL、真实供应商、支付、Docker或1Panel。32表715列/68 generated、40原件482列、旧23数组173字段、原checker/V3/profile与已应用迁移不变。完整writer/guard/ACL、105/121固定reader、144 basis/160 catalog/V4、B-P3/R2、商品/原路退款/拒付、生产与最终性能继续独立验收。

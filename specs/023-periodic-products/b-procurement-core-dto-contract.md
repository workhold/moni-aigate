# B-P4 D1：独立纯核 DTO、资源与旧 Base 投影合同

2026-11-03；基线 `cff5bef966c990cfea5d85f0b4ea4ba164ae146e`。承接[重放读取合同](b-procurement-replay-read-contract.md)、[715列物理布局](b-procurement-schema-contract.md)及[原件扩列](b-procurement-original-ddl-contract.md)。本文补齐可直接实施的 Go DTO、输入错误边界与所有权决策；集成人已裁定本页接口。**本片只实施 DTO/标量/预算及旧 Base 无损投影，不是完整 `Check` 已实现或 D1 完成的声明**。不修改旧 checker、旧材料或生产权限。

## 1. 固定公开形状

最终包为 `internal/procurementprefix`，只有一个完整业务核入口 `Check(context.Context, Rows) (Report, error)`。`Rows` 的两个具名字段为 `OriginalRows OriginalRows` 和 `PrefixRows PrefixRows`。不接受调用方提交的 Base、Report、Proof、Passed、Oracle 或 before/after JSON。

`Report` 使用已冻结的 `Scope,Status,Balanced,CoverageComplete,Scanned,Base,SourceClosure,RequestIncoming,AttemptCost,AccountPrefix,Propositions,Unproven`；四域计数和十一命题字段逐字采用重放合同§3.3/7，不借新实现增加可选字段。`Scope=procurement_source_prefix_v1`，终态为 `consistent/mismatch/indeterminate`；确定 mismatch 优先于 gap。`Base` 必须是本次完整原件无损投影后调用原 `CheckProcurementEvidenceGraph` 的原样结果，所有旧十一 gap 仍显示原值。

全部四域、Required 逐对象枚举、完整旧投影和 aggregate 接通前，不导出一个只做部分检查却可能返回 `consistent` 的 `Check`。可以先交付不发行成功结论的 DTO、标量、资源和独立算法叶子；相应状态必须明确为基础实现。

## 2. 40 个原件数组的准确映射

这40个数组不是旧 source71 的全部表，也不是允许放弃 source71 其他表。完整 scanner 仍读取合同105/121表，其他算法继续消费各自原件；这里只列本纯核自己的全部输入。一张物理表只对应一个数组，字段需包含旧 V3 source 允许列与本轮实际新增/补足列的固定 union，不能在 DTO 内重复存一份旧 BaseRows。

| Go 数组字段 | 物理关系 |
| --- | --- |
| Channels | channels |
| ChannelCredentials | channel_credentials |
| ChannelVersions | channel_versions |
| ChannelVersionCredentials | channel_version_credentials |
| Tenants | tenants |
| RequestRecords | request_records |
| CostVersions | cost_versions |
| ProcurementAccounts | procurement_accounts |
| CredentialProcurementBindings | credential_procurement_bindings |
| UpstreamAttempts | upstream_attempts |
| ProcurementReservations | procurement_reservations |
| ProcurementBudgetEvents | procurement_budget_events |
| Users | users |
| ProviderCostCorrections | provider_cost_corrections |
| ProcurementCostProposals | procurement_cost_proposals |
| ProcurementCommands | procurement_commands |
| ProcurementBalanceObservations | procurement_balance_observations |
| AuditEvents | audit_events |
| ProjectionOutbox | projection_outbox |
| ProcurementEvidenceIdentities | procurement_evidence_identities |
| MeteringEvents | metering_events |
| MeteringEventConflicts | metering_event_conflicts |
| FinalizationRecords | finalization_records |
| Reservations | reservations |
| Settlements | settlements |
| ReconciliationCases | reconciliation_cases |
| BillingProfileBlocks | billing_profile_blocks |
| LedgerAccounts | ledger_accounts |
| Journals | journals |
| JournalLines | journal_lines |
| APIKeys | api_keys |
| PeriodicBudgetControls | periodic_budget_controls |
| PeriodicBudgetWindows | periodic_budget_windows |
| PeriodicBudgetAllocations | periodic_budget_allocations |
| PeriodicBudgetEvents | periodic_budget_events |
| PeriodicBudgetOperations | periodic_budget_operations |
| PeriodicBudgetConfigurationVersions | periodic_budget_configuration_versions |
| PeriodicBudgetObservationOrigins | periodic_budget_observation_origins |
| PeriodicBudgetObservationHeads | periodic_budget_observation_heads |
| PeriodicBudgetObservationJournal | periodic_budget_observation_journal |

`Scanned.OriginalRows` 对应这40个字段，`Scanned.PrefixRows` 对应物理合同32个表字段；另有 TotalRows/LogicalBytes。原件类型采用完整新的具名 struct，不直接 alias 旧窄 DTO；core 内再固定投影到旧23类。`RequestRecords` 等字段不误用 `Requests` 代表另一个 PB 狭窄视图；同一张原件可被多个内部算法从同一 DTO 投影，不接受调用方提供两份可能矛盾的行。

## 3. 每列一字段与无损边界

32新表全部715列由固定列布局约束，包括68个 generated，不能把 generated 从 DTO 删除。Go 字段按物理 snake_case 转成具名字段，固定 initialism 为 `ID/XID/OID/PB/JSON`；所有字段有物理列名的 JSON tag。source/origin 的自然主键保持原列，不附加随机ID。

所有整数、NUMERIC、OID、XID 用规范十进制 `string`，nullable 为 `*string`。TEXT/JSONB 为 `string` 或 `*string`；JSONB保留PG文本后独立解析。BOOL 为 `bool/*bool`。时间 envelope 保留 `Valid/InfinityModifier/Time`，不得把不可表示、infinite 或 SQL NULL 投成零 `time.Time`。可以直接使用 pgtype 的纯 value struct，但不引入数据库连接、query 或扫描行为。BYTEA克隆并区分NULL和空，当前32新表没有BYTEA列；未来实际读集若新增必须先改合同版本。

直接调用边界固定为：原件单元若不能无损转为旧 DTO（例如 Sequence 不在int32或时间不finite），先返回 `ErrInput` 与零Report，禁止截断或填零后声称保留Base；可以无损表示但违反业务域的原件继续由旧Base及新域报mismatch。scanner的资格检查失败仍是 `input_mismatch` 且不调用本core。新前缀行的语义标量错误可计入 SourceClosure.InvalidScalarRows，不把损坏新source改标legacygap。

实现 `ErrInput/ErrResource/ErrCanceled/ErrTimeout` 固定错误，任何执行错误零结果。预检先统计全部72数组实际行与logical cell bytes，再解析JSON、建立索引或大整数；共享100000行/16MiB，field固定65536及两列route_decisions272144特例。NULL text/bytea计0，bool/time固定1/32；pointer存在但空串不能当NULL。预检和所有工作循环不超过256步复核context与绝对deadline，不在域切换时重置预算。

## 4. 四域实施所有权

| 所有者 | 独占代码责任 | 必须提供的私有结果 |
| --- | --- | --- |
| 集成人 | Rows/Report、预算/标量、原件固定union及旧Base无损投影、旧十一gap Required枚举、最终aggregate | 本次私有work及精确Required键集，无caller报告注入 |
| SourceClosure | roots/steps、canonical union、所有companion及原件双向集合、generated值、request/node/account/member四链 | 按真实自然键的qualified/bad集合，不仅count |
| RequestIncoming | 原Go ABI身份摘要、meter created/reused/collision、跨attempt/no-attempt sticky、request latest/finalization | 前五命题所需的具体原件键覆盖集 |
| AttemptCost | origin/quote/bound、node序delivery/normal/reset/mutation/correction/outbox、Reconcile/finish/ledger/PB | cost三命题及事件对应的来源资格；不能将quote时点当可见目录全集 |
| AccountPrefix | journal/config/p桶/blocking/gate、成员屏障、proposal/approval/recovery及pause | account两命题/proposal屏障及真实事件顺序的具体对象覆盖集 |

域结果不作为四个公共proof。每个对象只有在自己的全部依赖域都完整且无mismatch时，才能进入相应Proven集合；同一个键不能用来抵消另一个缺证键。无来源的原件形成真实gap；声称tracked却缺对应目标或孤立新source形成mismatch。

## 5. 旧 Required 键集须逐值复现

旧计数来自 [accounts](../../internal/fundinglotverify/procurement_evidence_graph_accounts.go)、[events](../../internal/fundinglotverify/procurement_evidence_graph_events.go)、[evidence](../../internal/fundinglotverify/procurement_evidence_graph_evidence.go)。新内部枚举器不能用整张表长度代替旧产生条件。例如：

- ProcurementEventOrder只在某attempt的复杂PE计数大于1时要求补证；并非每个存在PE的attempt。
- CostRecordedEligibility受原真实cost候选、conflict tenant、managed及correction/PE类型影响；CurrentCostOrder还有原known/unknown和唯一值集合判断。
- ProposalBarrier仅对原approved提案且expected revision/count落后原current产生旧gap；并非每个approved提案。
- AccountPause/Admission要求重建原配置版本与合法command边；只按今天paused或来源数计数会错误。
- finalization前驱、request多level/需sticky、conflict semantic与identity键均使用旧算法实际索引与去重语义。

每条命题保存其自然键集合，`Required=len(keys)` 必须等 `Base.Unproven.<field>`，且每键恰为 Proven 或 Unproven；如不等，属于确定的实现/数据矛盾，不能补零、截小或只调整Required使总数对齐。旧Base结构/mismatch非零始终使新Status=mismatch，不能由新来源修复旧原件矛盾。新source即使不触发旧gap也要完整检查自身来源和结果。

## 6. 本轮固定材料与交付边界

本片准确字段以[40原件core列布局](b-procurement-original-core-layout-v1.json)和既有[32新表715列](b-procurement-column-layout-v1.json)为固定物料。原件40表482列包含全部110新增列；继承旧V3 source在本40表的全部安全允许列，再补完整request/attempt birth及客户finish必需的原字段。原件JSON的SHA-256为 `132333daaeb265e1645530816a41fc9ad5205977a8ed8b66f956b6010a2d8e06`，它只描述本pure输入，不冒充完整105/121表readset或144/160表descriptor/catalog。时间用`type Time = pgtype.Timestamptz`，所有nullable时间用Valid明确表达，不另加pointer层。Column INET只对request.client_ip采用PG16 `::pg_catalog.text`投影，string不把源字段改成TEXT物理列。

Go映射在 `internal/procurementprefix/types.go` 与 `original_types.go` 静态编译，一表一数组、一列一字段，JSON tag精确等物理名。上述40字段和PrefixRows的32字段作为Scanned子结构逐名计数。Propositions字段采用十一旧命题名去掉末尾Unproven（例如OriginalDeliveryPredecessor），每项为Required/Proven/Unproven三计数。域和Gaps字段逐字继承读取合同§3.3。

`scalar.go`实现规范整数/NUMERIC与UTF8/NUL、finite微秒时间资格和全72数组资源预检；不执行来源重放、不发行coverage结论。`base_projection.go`只在相同context、全72数组预算成功后，把旧23数组投影成原 `fundinglotverify.ProcurementEvidenceGraphRows`。int32字段按实际ParseInt宽度转换，无法解析或宽度溢出返回ErrInput零结果；可表示的非规范十进制（如+01）能保留其实际数值供原Base，原始text仍留Rows供未来InvalidScalarRows检查。必填时间NULL/infinite或不在有限PG微秒域时ErrInput；nullable旧时间仅准确NULL envelope转nil，坏NULL envelope不抹除。所有nullable TEXT和time指针均新分配，不共享调用方可变指针；JSON字符串保持原字节，不重新序列化。

投影内部不调用新SQL/业务writer/计价helper，也不接受已经算好的Base；未来完整Check在预算与投影后调用旧checker并原样保存结果。原Base自己预检与结构检查仍运行原界，不放宽旧checker接受集；本片不修改旧checker文件。当前没有导出Check，因此不得用公开Report零值或DTO存在冒称四域重放通过。

独立测试须将原scanner `projection_procurement.go` 固定23 DTO/列序作为旧ABI oracle，逐字段核同一原件投影和nullable clone；原40/新32布局与Go tag/类型/顺序逐列比对。原40表还须独立对照旧 `b-prefix-readset.json` 保留其全部相关source列，并逐列覆盖已冻结原件110扩列的类型与NULL性；不能只让新DTO与新JSON相互自证。另验int32极值/溢出、NULL/infinity、65536/272144字段界、100000行/16MiB总界、NULL bool/time计账、取消/deadline及规范数值范围。测试无数据库；writer、完整来源重放、scanner/catalog/维护/runtime及实际APP专库仍独立交付。

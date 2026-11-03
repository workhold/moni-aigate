# B-P4-D1-R：独立采购来源重放、固定读集与新 profile 合同

2026-11-03。本页冻结 B-P4 的纯 DTO、重放、读取、资源和保护阶段接线责任，承接[共同来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)、[身份字节](b-procurement-identity-contract.md)、[写入权限](b-procurement-writer-authority-contract.md)与[PB ACL](b-procurement-pb-acl-contract.md)。新列的唯一物理展开、顺序、类型、NULL 与 generated 表达式由[统一 schema](b-procurement-schema-contract.md)约束；本页不另造另一套字段别名。

这是联合设计合同，不是完整能力或 D1 完成记录。当前已按[DTO与旧Base投影实施合同](b-procurement-core-dto-contract.md)交付40原件482列/32前缀715列DTO、72数组共享资源预检及旧23数组173字段无损投影；未导出`Check`，四域/十一对象义务/aggregate仍未实现。[账户writer](b-procurement-account-writer-contract.md)为20函数/29触发器的隔离候选，actual APP LOGIN限定专项已通过，完整故障矩阵仍待完成。旧 scanner、basis、catalog、maintenance 和 runtime 仍使用自己的固定接受集；没有完整新物料、纯重放、实际受限 LOGIN 与保护阶段证据时，不启用业务候选，不关闭 B-P3/R2，不以 fixture、codec 或一份 `consistent` 报告代替验收。

## 1. 证明范围与不变边界

新范围证明真实 DB 根产生的 request、attempt、incoming、成本、采购账户及本次 Reconcile 输出，按不可变来源和完整前缀独立复算。它保留旧 `ProcurementEvidenceGraphCheck` 的全部字段、结构子结果、mismatch 和十一项 `Unproven`，不在旧 checker 中清零、减计数或扩大接受集。

本范围不认证供应商账单真假、浏览器操作者真人身份、供应商生产协议、旧历史的缺失输入，也不将普通人工余额观察当作可消费余额。模型生成请求只在原派发路径执行；重放、scanner、维护、恢复和 observer 无 provider client，不发生成请求。所有原租户、硬预占、唯一结算、未知消费不重放与不可变发布版本规则继续成立。

历史读取不扩大到全部模型目录、授权变更或客户资金发行历史，也不认证dispatch和普通客户finish在内的完整request.status生命周期。RequestHead负责latest/sticky决策顺序，RequestDecision.status before必须是当场真实capture，但其间其他合法入口的status变化不被误当成缺失request_decision。根当场的配置、角色、binding、quote、期限和资金锁检查由封闭 writer、权限/catalog 与实际运行测试保证；纯历史检查消费其真实、不可伪造的 typed source，并独立核来源到结果的业务计算和完整集合。原 funding/customer/PB/wallet checker 继续承担自己的完整范围。

## 2. 固定新标识与兼容矩阵

| 对象 | 新合同 |
| --- | --- |
| catalog profile | `ProcurementPrefixV4 Profile = 4`；独立 typed 入口 |
| 纯 core | `internal/procurementprefix.Check(ctx, Rows) (Report, error)` |
| 纯 core scope | `procurement_source_prefix_v1` |
| source scanner scope | `complete_business_source_procurement_prefix_graphs_v1` |
| source+wallet scope | `complete_source_wallet_procurement_prefix_graphs_v1` |
| basis scope | `fixed_144_table_procurement_prefix_basis_v1` |
| source 物理表数 | 105 = 原 V3 的 71 + 32 新表 + 2 既有表 |
| source+wallet 物理表数 | 121 = 原 V3 的 87 + 32 新表 + 2 既有表 |
| basis 关系数 | 144 = 原 prefix 112 + 32；原 basis 已含上述 2 既有表 |
| catalog 关系数 | 160 = 原 V3 128 + 32；原 catalog 已含上述 2 既有表 |

source algorithms 固定为原七项 `ledger,links,funding,customer,procurement,PB,sandbox`，然后 `PBprefix`、`procurementprefix`，共 9 项。wallet 固定为原七项、`wallet`、`PBprefix`、`procurementprefix`，共 10 项。普通模式仅原 index 6 的 sandbox 可以 `not_applicable`；新增两项必须实际 `executed`，不得通过改顺序、缩短数组或空报告跳过。

准确总列数、PK 列总数、canonical 字节数与 SHA 必须从经审阅的固定 schema/readset/descriptor 实际生成并钉住。本页不填写估算列数或假 SHA。关系数已冻结不是对缺失列物料的豁免；缺任一物料，新 registry/loader 必须拒绝，不能根据 live metadata 放宽。

| 旧对象 | 保留行为 |
| --- | --- |
| ordinary `Read` | 原 68 表、7 algorithms、旧 scope/DTO/接受集 |
| `ReadPrefix` | 原 71 表/635 列、8 algorithms、旧 source scope |
| `ReadWalletPrefix` | 原 87 表/804 列、9 algorithms、旧 wallet scope |
| prefix basis | 原 112 表/1004 列/131 key 列、40655 canonical bytes；原 SHA `104914e6176e3d02a289e4e0acf0d476a69dcfb2e8772cf29336b520a7ec82bc` |
| `LegacyV1` / `WalletReadV2` / `PeriodicPrefixV3` | 原值 1/2/3；旧 generic profile API 不自动接受 3 或 4 |
| 原 roots、golden、history/current | 原字节和验证入口；不为新候选改写历史材料 |

新 scanner 的拟定独立入口为 `ReadProcurementPrefix`、`ReadWalletProcurementPrefix`，返回不同私有 named state 的新 facts 类型；不能显式 Go 转换为彼此或旧 facts。新 basis/catalog/maintenance/runtime 同样使用显式 V4 入口；不会因表存在、迁移号较大、caller 配置一个整数或 Summary 宣称 V4 自动选择新权限。

## 3. 单一纯 core 与不可注入的输入合同

只新增一个 `procurementprefix` 公共 core，内部四域为 `SourceClosure`、`RequestIncoming`、`AttemptCost`、`AccountPrefix`。域结果是描述性计数/状态，不是四份可拼装的公共 proof。任一域失败不能拿另一调用的结果补齐。

`Rows` 仅含固定、逐字段具名的原始 row 数组，无 DB/query/lease、supplier client、caller report、`Passed`、`CoverageComplete`、自由 `before/after` JSON、可执行 SQL 或外部 oracle 参数。完整输入分两组：

1. `OriginalRows`：旧采购 core 的全部 23 类原始数组；另加客户 reservations、settlements、reconciliation_cases、billing_profile_blocks、ledger_accounts、journals、journal_lines、api_keys，以及全部九类原 PB 行。旧 request/attempt 等使用新完整行 DTO，在 core 内确定性投影到旧 DTO，不以调用方交来的 `BaseRows` 或 Base 报告替代。
2. `PrefixRows`：§4 的全部 32 新表，一表一数组、一列一字段。immutable source、companion、generated discriminator 和 mutable heads 都必须实读；不能只给 roots/count/hash 或终态。

core 自行调用 `fundinglotverify.CheckProcurementEvidenceGraph`，得到 `Report.Base`；同一次 scanner 另行执行的原采购结果必须逐值等于它。四域只使用本次 `Rows` 重建索引和状态，不共享上一调用的可变 map/cache，不调用新 writer/helper/SQL 来计算 expected。PB 与普通资金的 aggregate 另从同一次 raw session 重算，不将其报告注入本 core 作授权依据。

`Report` 固定包含 `Scope,Status,Balanced,CoverageComplete,Scanned,Base,SourceClosure,RequestIncoming,AttemptCost,AccountPrefix,Propositions,Unproven`。四域各有固定 uint64 mismatch/gap 计数；`Propositions` 为 §7 的十一组 `{Required,Proven,Unproven}`。报告不暴露财务原始输入或可复用操作 token。正常结果 `mismatch` 优先于 `indeterminate`，无 mismatch 且所有所需来源/命题齐全才 `consistent/Balanced=true`；执行错误另以 error 返回，不伪造一份完整报告。

### 3.1 标量、NULL 与字节

| SQL / 物理值 | 纯 DTO 与检查 |
| --- | --- |
| TEXT | `string`；逐字节保留 UTF-8、大小写及空串；合法 UTF-8、无 NUL，按真实 octet 上限检查 |
| nullable TEXT | `*string`；NULL 与 `""` 不等；不自动 trim/UUID cast |
| SMALLINT / INTEGER / BIGINT | 规范十进制 `string`；按实际宽度和字段非负/正值范围独立 parse；无 `+`、前导零、指数或环回 |
| NUMERIC(100,0) | 规范十进制 `string`；独立大整数，amount/bucket 非负、delta 可负；禁止负零、小数、指数及超过 100 位 |
| XID8 / OID | 无符号规范十进制 `string`，分别核 64/32 位；xid 不作全局操作排序，role OID 不自行映射成授权 |
| BOOLEAN | `bool`，nullable 时 `*bool`；不使用文本 truthy 或默认 false 掩盖 NULL |
| TIMESTAMPTZ | physical cell 保留 `pgtype.Timestamptz` 的 Valid、InfinityModifier 与 binary 时间；projection 仅在 finite 且原时刻可表示时产生 `time.Time`/`*time.Time` |
| BYTEA | clone 后 `[]byte`；NULL 与空 bytea 分开；摘要/identity 按规定长度与原字节比较 |
| JSONB | 原件 `::pg_catalog.text` 字符串，按固定旧业务结构独立解析；禁止重复/未知关键业务成员、数字与字符串混换、缺字段当零 |

原 route_decisions 是合同限定的原业务 JSONB 数组，保留 <=1000 项/原Go规范marshal262144字节及 selected 匹配规则；它不是开放来源 JSON。每项恰 channel_id,version_id,priority,weight,reason五字段，纯oracle按原routing.Decision声明顺序独立Go marshal。priority保留部署64位Go int的完整有符号范围，weight保留1..MaxUint64，不能经float64、BIGINT或int64使上半区丢失；按JSON数字原文与大整数精确解析，源JSONB不得有额外字段。原JSONB传输增加的空格不误作业务超界，见§8。旧 proposal/command/audit/outbox 原 JSON 仍保留原 shape、版本与 nullable 语义，不能将 JSON 字面 null 改作 SQL NULL。身份 digest 独立依 Go 持久 ABI 重算，ObservedAt 始终从前像排除，raw stage/content 在 sticky 归一化前编码；不能复用 SQL 叶子当唯一 oracle。

### 3.2 xmin 的明确责任

105/121 表读集、新纯 DTO 与 basis descriptor **均不新增 xmin 系统列**；xmin 不计 schema 列数。真实 writer/PB 中原 `pb_current_tuple(xid8,xid)` 仍用于本事务的实时 guard，不能据此在离线历史重放要求 `xmin=created_xid`。合法 UPDATE、VACUUM FREEZE、subtransaction/epoch 边界都使这种历史等式不成立。

历史同事务身份由 DB 覆盖的 immutable `created_xid`、root/source/step 的复合键与精确原件 pointer 约束；head 的 `updated_xid` 在 ordinal>0 时等末项 source/journal xid。request/attempt 的 head0 尚无末项，updated_xid 必须精确等本 request/attempt origin 的 birth created_xid，不允许空值或猜测最近root。当前 tuple 的 xmin 既不证明 source 从未被篡改，也不能恢复不存在的旧 before；防伪由新实际 ACL、guard、完整封闭根和 exact catalog 共同承担。

### 3.3 固定报告计数字段

`Scanned` 对 `Rows.OriginalRows` 的40个数组和 `Rows.PrefixRows` 的32个数组各有一个具名uint64计数，并有 `TotalRows,LogicalBytes`；数组名与 §3/4 对应物理表一一映射，不能只保留总数。新报告四域的 mismatch 字段固定如下，全部uint64；一行可违反多项，但同一项按该项自然对象键去重，不能通过改变遍历顺序改变计数：

| 域 | 字段 |
| --- | --- |
| SourceClosure | InvalidScalarRows,DuplicateRows,RootTemplateRows,CanonicalUnionRows,CompanionRows,OriginalPointerRows,GeneratedRows,RequestChainRows,AttemptChainRows,AccountChainRows,EvidenceChainRows |
| RequestIncoming | InputDigestRows,MeterIdentityRows,MeterChoiceRows,ConflictRows,RequestStickyRows,FinalizationRows,LatestRows |
| AttemptCost | AttemptBirthRows,QuoteBindingRows,DeliveryRows,NormalEligibilityRows,ResetRows,MutationRows,CorrectionRows,OutboxRows,CurrentCostRows,ReconciliationRows,CustomerFinishRows,CustomerLedgerRows,CustomerPBRows |
| AccountPrefix | AccountBirthRows,CommandRows,JournalRows,GateRows,BlockingRows,EvidenceRows,TransitionRows,PauseRows,ProposalRows,ApprovalRows,RecoveryRows,CurrentProjectionRows |

新 `Unproven` 固定字段为 `UncoveredRequestOrigins,UncoveredAttemptOrigins,UncoveredAccountOrigins,UncoveredMeters,UncoveredConflicts,UncoveredFinalizations,UncoveredProcurementEvents,UncoveredCostWrites,UncoveredProposals,UncoveredCorrections,UncoveredReconciliations,UncoveredCustomerFinishes,UncoveredAuditOutbox,LegacySourceBoundaryUnproven`，全部uint64；十一命题剩余义务另在 `Propositions` 各组的 Unproven保留，不从这些计数中相减。原件明确声称tracked却缺目标/错pointer是mismatch；真实全NULL旧来源缺证是gap，不因时间戳较旧自动可信。新origin/source本身孤立或非法也属于mismatch，不能改标legacygap掩盖。

这组报告是新版本合同；实现不得悄然加字段后沿用同scope/物料版本，也不能省去某数组的计数而只报物理总表数。详细内部逐对象 witness不作权限API，报告的固定计数仅支持交叉核验和诊断。

## 4. 32 新表的完整物理投影

各表读取[统一 schema](b-procurement-schema-contract.md)固定展开的**全部普通列与 stored generated 列**。该文件的 S/C/I/U 等是编写期宏，必须在材料中展开为有 attnum/type_oid/typmod/nullability 的具体列；运行时无宏、无 SELECT *、无发现新列后自动加入。下表的“字段合同”确定逐字段 DTO，不允许只挑实现方便的子集。

| 物理表 | 固定分页主键 | 字段合同及 NULL/generated 责任 |
| --- | --- | --- |
| pp_operation_roots | id | 共同§2；request/account_command 两 scope nullable 联合；最终一次完整 seal |
| pp_root_steps | root_id,step_no | 共同§3；全 18 generated nullable child discriminator，仅选中一个等 source_id，其余全 NULL |
| pp_request_origins | request_id | 成本§3.1；原件出生完整快照，optional 明确；initial_latest 恒 NULL，其余初态固定 |
| pp_request_heads | request_id | request_id、ordinal、last_decision_id?、updated_xid；0/NULL 起点的updated_xid等RequestOrigin.created_xid，非零时等末项decision xid |
| pp_attempt_origins | attempt_id | 成本§3.2及统一 schema；含 finite quote_selected_at，managed/unmanaged 完整联合 |
| pp_attempt_heads | attempt_id | attempt_id、ordinal、last_node_id?、member_count?、member_tail_id?、updated_xid；head0 xid等AttemptOrigin.created_xid，非零时等末项node xid；member 两项仅 managed 非 NULL |
| pp_attempt_nodes | id | 共同§4；ordinal/previous_node_id?；previous_ordinal generated=ordinal-1，ordinal1 的 prev=NULL |
| pp_account_origins | account_id | 账户§3.1；全部 NOT NULL，version 固定1，无旧账户补出生 |
| pp_account_heads | account_id | 账户§3.2；完整 Config/Buckets、ordinal/journal/updated_xid，全非 NULL |
| pp_account_journal | id | 账户§3.3；before/after、p/quote/gate 的封闭 NULL 联合；previous_ordinal 及三原件 discriminator 全实读复算 |
| pp_account_command_sources | id | 账户§3.4；configure expected_revision、found journal 与 not_found 联合，不将不存在 account 强 FK |
| pp_attempt_delivery_sources | id | 成本§5.1与账户 dispatch；mode=dispatch_permit/finalization/recovery，真实 before/after 与 permit 联合 |
| pp_incoming_sources | id | 成本§4.1；raw input 全字段、ObservedAt optional、effective time、仅 Reconcile 子流程的 source 引用 |
| pp_meter_choices | id | 成本§4.2；created/reused、collision 联合、真实 meter origin、竞争 witness 与完整 cut |
| pp_meter_origins | meter_event_id | 成本§4.2；canonical meter_choice 的 companion，只有 created 才出生 |
| pp_conflict_sources | conflict_id | 成本§4.2；每个原 conflict 的唯一 incoming/choice/归属，attempt optional |
| pp_request_decisions | id | 成本§4.3；request ordinal/previous?、normal/conflict 输出联合；previous_ordinal generated=ordinal-1 |
| pp_normal_cost_decisions | id | 成本§5.2；完整 before/cut、write/skip 优先级与真实 quote；写/不写输出 NULL 联合 |
| pp_cost_mutations | id | 成本§5.3；真实 current-cost UPDATE，cause 固定联合；所有 before/after amount NULL 规则 |
| pp_cost_reset_decisions | id | 成本§5.3；applied/suppressed 原优先级、reset cause；不能省 unknown→unknown |
| pp_evidence_choices | id | 成本§6.1；managed 完整 p/account/member cut、new/existing、effect/skip 联合 |
| pp_evidence_members | id | 成本§6.1；member_ordinal/previous_member_id?、identity 原键；previous_member_ordinal generated=ordinal-1 |
| pp_procurement_transitions | id | 账户§3.5与成本§6.2；reserved before 全 NULL，其他完整；四 cause generated discriminator 全实读 |
| pp_proposal_births | id | 成本§7.1；不可变 proposal 原件、p/account/member cut、actor snapshot/audit |
| pp_approval_consumptions | id | 成本§7.2；当场 proposal/current role/cut、精确 correction/transition/mutation/audit 输出 |
| pp_unmanaged_correction_sources | id | 成本§8.1；原更正全部字段与 actor/cost before；禁止 p/PE/member/account effect |
| pp_recovery_sources | id | 成本§8.2；真实锁后期限/permit/状态、released/unknown 联合，非 caller deadline |
| pp_cost_outbox_links | id | 成本§5.4；CostOutboxLink 自身为 canonical child，cause 与原 outbox 双向闭合 |
| pp_reconciliation_sources | id | 成本§9；action 的实际消费输入、case before/after、三时钟/子 finalize/finish 精确指针 |
| pp_customer_finish_outcomes | id | 成本§9.1；reconciliation companion，new/existing/pending 联合、真实财务 before/target |
| pp_customer_finish_ledger_effects | finish_outcome_id,account_id | 成本§9.1；真实 HELD/AVAILABLE及必要 REVENUE 完整行组，平台 account_tenant_id=NULL |
| pp_customer_finish_pb_effects | finish_outcome_id,scope | 成本§9.1；tenant/key 各自真实 event，固定 settlement source；缺 control 才可缺 effect |

普通 source PK=id；两个 origin canonical child 自然键分别为 request_id/attempt_id，不能新增随机 id 替代公共 source_id。所有 canonical step_kind 的 generated 固定常量、所有 cause/child discriminator 与前驱 generated ordinal，均按独立规则重算值，再按完整复合键关联，不能因为 catalog 检查过表达式就略过 row 值。discriminator允许集为schema§3：RootStep的18个 `d_<kind>`、CostMutation五cause、CostResetDecision两cause、ProcurementTransition四cause、CostOutboxLink三cause及账户journal三个原件discriminator；这些discriminator均nullable，恰选中项等真实source_id，其他NULL。固定step_kind generated为NOT NULL；前驱generated列也NOT NULL且值为本ordinal-1，首项保留0，不用NULL隐藏生成结果。

mutable 表仅 request_heads、attempt_heads、account_heads，以及旧表既有投影。它们是完整重放的末项对照，不是 before 的独立权威；immutable 原点、来源、journal/node/member/decision/companion 不接受 UPDATE。系统不依 created_at/created_xid/source UUID 排序；分页排序只负责完整枚举，业务顺序由显式前驱/ordinal/root step 决定。

## 5. 原件读集：逐列继承与必须扩列

旧 [b-prefix-readset.json](b-prefix-readset.json) 的 source 71/wallet87 **每个表、每列、类型、分页键全部继承**；相同物理表做显式 union 去重，只扫一次。新 fixed readset 必须另存 canonical 材料，不修改旧 JSON。另加入原 `reconciliation_cases`、`billing_profile_blocks`，以及 §4 的 32 表。

以下是新合同必需的旧表扩列，不以“旧 reader 已经有这张表”代替：

| 原表 | 必须实读的新增/补足内容 |
| --- | --- |
| request_records | 完整 RequestOrigin 对照列：owner/key/model、price/profile/policy/route/pricing 冻结值、request_hmac、硬界/期限、instance/streaming/client_ip、created_at、PB 出生列，以及所有新 origin/current pointer；原client_ip是INET?，出生source的TEXT?与reader均取其PG16 `::pg_catalog.text`规范投影，NULL保持NULL，不对TEXT/INET强设直接复合FK |
| upstream_attempts | 完整 AttemptOrigin 对照：sequence/channel/version/credential/model、route_decisions、冻结 quote、pp_quote_selected_at、created_at/PB；原 delivery/current cost/dispatch全部新 pointer 与原 permit checked_at |
| reservations | 原 tenant_id,request_id,id,amount,status,deadline,created_at；本表无key_id、不新增tracking列，key经同tenant/request的request_records.key_id关联 |
| settlements | 原 tenant/request/id/finalization/status/reason、charged/raw/waived、journal/created_at与新 finish pointer，不遗漏 zero charge |
| procurement_accounts/reservations/budget_events/commands | 账户合同§3.6全部新增 pp 列，以及原完整配置/桶/p/command/event 身份与原 JSON snapshot |
| metering_events/conflicts/finalization_records | 原 input/usage/归属/时间/版本全字段及新 origin/decision/companion 反向 pointer；原 conflict 无 incoming 的缺口由真实 companion 补齐 |
| procurement_evidence_identities/proposals/provider_cost_corrections | 全原键/输入/时间/版本/审批字段及 member/birth/approval或unmanaged来源的完整反向列 |
| audit_events / projection_outbox | 原字段与共同六 pointer 全部读取：pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid |
| journals / journal_lines / ledger_accounts / api_keys | 原 checker 全列与本次 finish 必须对照的 account tenant/kind、line id/delta/journal、journal actor/reference/created_txid、key held/spent；新增 finish pointer 按统一 schema 全读 |
| reconciliation_cases | 原 id,tenant_id,request_id,reason,state,proposed_usage,evidence_ref,proposal_reason,requester,approver,created_at及统一 schema全部新增 pointer |
| billing_profile_blocks | 仅原 model_id,profile_version,reason,request_id,created_at；明确无新增列，分页键为(model_id,profile_version)，不虚构id或本次request归属G组 |
| cost_versions | 原 id/channel/model/rates/basis/effective_at/created_at完整读取；使用已选版本的一致性核验，历史可见全集边界见§6.3 |

与 source 无关的共享 audit/outbox 仍全表读取并由原 checker 验自己的族；仅受保护采购/成本/Reconcile 子集有新指针强制。指针全 NULL/全完整的联合、source role 槽、每槽唯一及源↔原件双向闭合不能按同 action/kind 一刀切。独立普通客户 Settle、绑定、余额观察等原合法共享写面继续按原合同验证。

不读取 credential secret、provider key 明文、模型正文、密码、TOTP、恢复码等与重放无关内容。新 readset 是明确列允许集；catalog 验 schema 全貌不等于 scanner 有权投影全部敏感列。`recovery_jobs` 是可变运行清理队列，不加入 immutable 来源图或本 105/121 表集，清理责任通过真实运行验收另计。

### 5.1 105 表的完整关系集合

旧 71 表按以下固定名保留；不能在 ordinary 模式跳扫 payment/sandbox 物理表：

```text
api_keys, audit_events, channel_credentials, channel_version_credentials,
channel_versions, channels, commerce_commands, commerce_operation_receipts,
cost_versions, credential_procurement_bindings, credit_orders,
finalization_records, funding_receipts, journal_lines, journals, ledger_accounts,
metering_event_conflicts, metering_events, payment_cancel_intents,
payment_captures, payment_command_observations, payment_command_outbox,
payment_effects, payment_event_conflicts, payment_evidence_processing,
payment_inbox, payment_order_capture_selections, payment_orders, payment_policy,
payment_provider_revisions, payment_query_attempts, payment_query_conflicts,
payment_review_facts, payment_runtime_attestations, payment_sandbox_environment,
payment_sku_changes, payment_sku_versions, payment_skus,
payment_transaction_identities, payment_verified_evidence,
payment_verified_queries, periodic_budget_allocations,
periodic_budget_configuration_versions, periodic_budget_controls,
periodic_budget_events, periodic_budget_observation_heads,
periodic_budget_observation_journal, periodic_budget_observation_origins,
periodic_budget_operations, periodic_budget_windows, procurement_accounts,
procurement_balance_observations, procurement_budget_events,
procurement_commands, procurement_cost_proposals,
procurement_evidence_identities, procurement_reservations, projection_outbox,
provider_cost_corrections, refund_orders, request_records, reservations,
settlements, tenants, upstream_attempts, users, voucher_batches,
voucher_redemptions, voucher_revocation_decisions, voucher_revocations, vouchers
```

105 表 = 上述 71 + `reconciliation_cases,billing_profile_blocks` + §4 全部 32。121 表另加以下固定 16，保持原 wallet 完整集：

```text
funding_consumption_refund_lots, funding_lot_balances, funding_lot_epoch,
funding_lot_events, funding_lot_legacy_approved_refunds,
funding_lot_legacy_openings, funding_lot_legacy_receipts,
funding_lot_legacy_sandbox_receipts, funding_lot_legacy_settlements,
funding_lot_runtime_attestations, funding_lots, funding_reservation_lots,
funding_settlement_lots, funding_wallet_heads, funding_wallet_legacy_sources,
funding_wallet_operations
```

## 6. 独立重放的确定顺序

### 6.1 SourceClosure：完整联合与事务 seal

先在预算内完成标量/NULL/generated/duplicate 检查，再建索引。对每个 root 重建唯一 scope、entry模板、同 top-xid及 step 1..step_count；每步恰一个相应 canonical child，所有正反向 ID 精确相等。18种联合不能按“任一表同ID存在”选 child。account_command 不制造 tenant/request；not_found 不能要求目标 account 存在。纯core对session_role_oid仅核OID范围及行间明确绑定一致性，不能从一个整数认证受审APP；实际身份在§9/10由owned catalog/pins逐root核，不能增加caller自报authorized-role布尔。

逐一枚举所有 typed source、companion、原件与受保护原件反向指针，比较**双向集合**。根漏 seal、漏 child、孤立 output、复用同一原件两槽、跨 root/step/tenant/attempt/source role、同 xid 两根混绑均 mismatch。一个 root step 不能拿另一个 root 的结果充数。companion 不算第二 canonical child；cause source 与结果 transition 是不同 step，cause 必须严格在前。

再重建三个局部链：request decision、attempt node、account journal，和独立 evidence member 链。逐键核唯一前驱、无分叉/环/跳号、起点与末项 head及generated predecessor。相邻before=上一after只用于该链完整管理的状态：request latest、attempt cost及delivery、account配置/桶及对应p；不能把RequestDecision.before_status无条件等前decision.after_status或Origin.accepted，dispatch与普通客户finish可在其间合法改request.status。每次decision的真实status before由受审根capture、该decision的after按本分支独立计算；只有同root显式描述的相邻转换另逐值核。不按时间和 UUID 排序。attempt_birth 本身无 node；同根 managed reserved 是 node1；其余有 attempt 根每个 canonical step 恰一 node，全部无 attempt 根与 account scope 禁止 node。

### 6.2 RequestIncoming：raw、meter 与跨 attempt sticky

从 RequestOrigin 的 NULL latest、accepted/reserved 初态开始，按 request ordinal 消费 normal/conflict decision；初态快照不意味着request status此后仅由本链变更。每条 incoming 独立重算 raw input digest，先 raw 归一化、后消费准确 before_latest 做 sticky；content/committed 前驱可跨 attempt 或无 attempt。normal 必须生成唯一原 finalization并推进 latest，conflict 不生成 normal finalization且 latest 不变。current latest必须精确等本链末项；current request.status/billing_status不被末条RequestDecision.after_status单独认证，它们仍可由原合法客户资金入口改变，本次Reconcile明确描述的finish结果除外。

每个 meter source key `(tenant,source,source_event)` 的 created/reused 选择精确绑定其唯一 MeterOrigin。created 原 meter 全字段等 incoming；reuse 比较旧原件与本 incoming 的 request/attempt/Usage，先判 source_payload_mismatch，再判 other_confirmed_usage。竞争成员来自 cut 内完整真实 meter 出生集合，不由当前 finalization 数、一个 witness 或 caller count 代替。

MeterOrigin membership 始终属于原 meter 的 attempt；跨 attempt 复用只推进 incoming attempt 的 choice node，不搬运原 meter。新 meter 即使随后因 other-confirmed 判 conflict、没有 normal finalization，仍在真实成员前缀中。每次实际 conflict 有独立原件/companion；重复 semantic 可以不增 evidence member。原件无 incoming/reason/归属时，必须由本 choice/conflict source 链补证，不能用旧 meter 猜 incoming。

### 6.3 AttemptCost：出生、quote 与完整 mutation

公开 DB `pp_record_attempt` 必须闭合原 RecordAttempt 全部输入校验、真实 quote 选择、INSERT 与 managed admission；三参 `pp_complete_attempt_birth` 仅私有，APP 不再 INSERT upstream_attempts。原 Go API和 root entry=attempt_birth 保持。原 quote 选择语句采样的 `quote_selected_at` 同时存入 origin 与原 attempt 的 `pp_quote_selected_at`，即使 noquote 也有时点。managed 入账后的 post-wait 复核保留；unmanaged 不新增一次会改变原接受集的重定价。

纯重放独立核所选 immutable cost_version 的 channel/model/rates/basis，及其与 origin、原 attempt、managed p 的冻结值一致；quote absent 时检查 NULL/basis=unknown 的封闭联合。**不以今天 cost_versions 全集加时间过滤证明当时 statement 的准确可见全集或 noquote absence**：未来可新增 effective_at 回溯的版本，created_at 也不是目录发行全序。quote_selected_at 是观察时点，不授予这种历史证明。原 current 选择规则 `effective_at DESC,created_at DESC,id DESC`、channel/binding 锁及 scheduled 跨等待语义在实际 writer 测试验；若以后需要独立历史报价目录发行认证，必须另设计完整目录前缀，不能假称本轮十一命题包含它。

以 origin 的 delivery=prepared、cost=unknown/NULL 开始，按唯一 attempt node 消费 delivery、normal decision、reset decision、cost mutation、evidence、proposal/approval/correction/recovery/outbox。独立用冻结两维 rate 与 usage 算 `ceil((input×input_rate+output×output_rate)/1000000)`，先大整数求和再单次向上取整，核 int64/NUMERIC 范围及原 upper-bound 规则；不调用 writer 的计价/状态机 helper作 expected。

正常资格必须核真实 before：known 优先 skip_already_known，再 disputed 等原顺序；managed/unmanaged 分支分开。reset applied、suppressed 与 unknown→unknown 都按真实调用顺序保留；不能把同根两个 reset 合并。更正是绝对金额，不是增量；同额/basis-only 仍保留实际 correction/mutation/audit/outbox、managed PE与journal。最后所有 mutation 重放值必须精确等原 upstream 当前 cost，不以“属于已知成本集合”代替最后顺序。

cost_recorded 的原 `(attempt,kind)` 唯一 INSERT 行为不变：unmanaged recorded→reset→再 normal write 的真实 23505 回滚不能伪装成成功 skip。cost_outbox step本身是 canonical source，原 outbox 指向 link，不跳到其 cause。所有生成/skip/noop/replay 与原件双向集合对照；无持久来源的幂等快返不虚造 root。

### 6.4 AccountPrefix：全账户顺序、成员屏障与效果

每账户从唯一真实 create journal 的零桶、revision1 起，按 account ordinal 重放全部 configure/configure_rejected、reserve、dispatch、采购效果及 anomaly pause。before/after Config/Buckets/p全展开比较，configuration revision 与 p revision 分开。拒绝命令同样有真实 source/audit，not_found 无 journal；不能用旧 command.after JSON null 覆盖 journal 的真实不变 after。

reserve/dispatch 在当时 account cut 独立核 active、blocking=0、H+S+extra≤limit（extra分别U/0），U由冻结 request界与quote独立计算。blocking 由该 account cut 上每个 p 的真实 state/anomaly 重建；不得用今天 p 终态或 supplied count。所有账户配置与所有 attempt effect共用一条 journal，p 的上次 last 与账户全局 previous 可不同。

EvidenceChoice 先依 incoming Usage+原 reason 独立 hash，再按 member 链证明 new/existing、完整 count/tail cut。现有 identity快返、terminal/already-disputed/duplicate-event/unknown各 skip按真实优先级；无 PE不造 no_effect journal。effect四 cause为 attempt_birth/evidence_choice/approval_consumed/recovery，独立重算唯一 PE/p/account target；每个真实 PE（含零 delta）恰一 transition/journal，PB配置时恰一合法相应 supplier effect。

ProposalBirth 的完整 member prefix和p/account cut必须可枚举；ApprovalConsumed消费当场真实同一 proposal、maker/checker角色快照和准确屏障。不要因今天已有更多成员、角色失权、更多配置而否定已真实批准历史，也不能拿今天状态倒填旧 source。actual LOGIN 测试须证明重试时当前失权/自批/屏障变化拒绝；source快照本身不代替这个运行验收。

异常仅按真实 cause自动 active→paused，already-paused不多增配置revision；消除blocking不自动恢复。缩limit/paused不阻断有利收尾。恢复只用真实 permit/期限决定 expired_without_permit 或 recovered_unknown，没有 meter/finalization/provider动作；dispatch本身只有 delivery+journal/PB gate，没有伪造 PE。

### 6.5 Reconcile 与客户 finish companion

Reconcile root 无 attempt，其全部 step 无 attempt node。propose只消费Usage/evidence/reason；release仅reason；approve原三类输入被原API忽略，wrapper先丢弃，再传 typed NULL，不新增对被忽略 NUL/非法UTF8 的拒绝。case来源保存操作时真实 before/after；批准不可预测后续资金等待后的结果。

独立核 approval_checked_at、settlement_decision_checked_at、financial_checked_at各自用途。approve截止前派生的无attempt incoming仍消费request真实前驱，不能靠批准制造delivery；内部semantic conflict使整根回滚，区别于公开 RecordFinalization可保存conflict再返回业务错误。

CustomerFinishOutcome只在本次新approve/release实际finish分支生成。new_settlement独立算raw/charged/waived、charge≤R、最终deadline后释放、Key桶与真实 ledger行；HELD/AVAILABLE含真实零delta两行，REVENUE仅最终charge>0。此前锁过REVENUE而最终deadline释放，不列不存在的effect。两类PB scope逐条对照真实原event及原window，不做customer catchup掩盖旧reserve缺失。

pending无收费和settlement，却保留markPending真实reservation/request billing status及case.reason更新，metering_conflict理由优先；existing settlement复用不得重生旧原件/效果。release公开state=closed与已approved case实际仍approved分别核。profile-block使用(model_id,profile_version)，created/reused和本次audit精确；复用旧block不强求旧row.request=current。recovery_jobs固定清理在运行测试验证，不伪造immutable历史行。

## 7. 十一命题逐对象覆盖与新 aggregate

保留旧 Base 原值，另独立枚举旧算法产生 gap 的对象键；下列 `Required` 必须等旧 Base 相应 Unproven 计数，且 Required=Proven+Unproven。计数相等只是交叉校验：内部逐键证明集合必须一一对应，不允许拿同一成功对象抵消另一个缺证对象。新 source还须覆盖自身全部对象，即使旧Base恰好无需该gap，也不能省新scope的出生/原件/末项检查。

| Base 命题 | 旧 gap 对象键 / 新证明 |
| --- | --- |
| OriginalDeliveryPredecessorUnproven | finalization_id；raw incoming、准确 request前驱与独立sticky结果 |
| FinalizationOrderUnproven | request_id；完整normal/conflict decision连续链与current latest |
| ConflictIncomingUnproven | 原 conflict_id；唯一ConflictSource→choice→incoming完整绑定 |
| ConflictReasonUnproven | 原 identity(attempt_id,digest)；真实incoming reason/usage与独立evidence ABI |
| IncomingBindingUnproven | 原 identity(attempt_id,digest)；incoming目标与原meter复用来源各自归属 |
| ProcurementEventOrderUnproven | attempt_id；完整node、typed cause、transition、PE/account journal与PB关系 |
| CostRecordedEligibilityUnproven | attempt_id；真实normal资格before/write-skip/quote/outbox及后续reset顺序 |
| CurrentCostOrderUnproven | attempt_id；origin起点至全部mutation/correction/reset的完整末项 |
| ProposalBarrierUnproven | proposal_id；出生完整member/p cut与真实approval消费cut |
| AccountPauseOrderingUnproven | account_id；完整配置/journal顺序与精确anomaly cause |
| AccountAdmissionOrderUnproven | account_id；全部reserve/dispatch当时配置、桶、blocking及U/0 gate |

旧 structural 或 mismatch 非零始终令新scope mismatch；新source不能“修复”一个原业务不一致结果。旧Base仅因以上十一gap而indeterminate/不Balanced时，新scope可在逐对象全部补证后完整，但旧Base本身仍原样显示indeterminate与原计数。原件缺来源、legacy不可重建、引用旧无origin meter/account/request等继续新Unproven/indeterminate，禁止回填今天状态制造origin或默认已覆盖。

新 aggregate 必须从实际各child重建，不能使用旧 aggregate 的 CoverageComplete（其采购gap本就保守失败）作为最终判定，也不能直接忽略采购整个域。完整条件共同为：

1. 固定全部物理表EOF、descriptor/cursor/预算复核成功；输入拒绝、metadata/ordinary边界/来源投影mismatch均零。
2. 原七项及wallet（若选）按固定scope/algorithm执行，各自结构和mismatch均零；新core.Base逐值等同次原Procurement结果。
3. `procurementprefix`四域与所有十一对象义务均完整、新gap为零、Balanced=true。
4. PBprefix实际完整，原PB的 LegacyMembershipUnproven、LegacyEffectBoundaryUnproven 与PBprefix自己的 OperationSnapshotPrefixUnproven等缺证仍各自为零；采购来源不能替PB认证遗留边界。
5. 普通sandbox only-not-applicable的原资金边界与sandbox模式的全原结果不变；wallet必须原完整范围一致，不能拿source-only替代。

`consistent`只描述这次输入。保护阶段必须基于私有raw facts重新qualification，报告布尔、JSON复制、外部纯core调用、SHA/count摘要均不能单独发行权限。

## 8. 物理扫描、共享预算与错误优先级

scanner沿原 [reader](../../internal/fundinglotscan/reader.go) 与 [prefix reader](../../internal/fundinglotscan/prefix_reader.go) 的固定、单次物理 union读取：`FROM ONLY`、安全quote schema、无业务WHERE、固定PK keyset/page128、text键`COLLATE pg_catalog."C"`、数值键按真实BIGINT排序。root_id+step、finish+scope等复合键逐项比较，不按字符串排列ordinal。

| 边界 | 新合同 |
| --- | --- |
| physical rows | 全105或121表合计100000，不按域/表重置 |
| physical bytes | 全union合计16MiB；共享原表只计一次，新列和generated实际计入 |
| field | 默认65536 bytes；下述固定特例外，不接受caller调整 |
| 固定field特例 | 原 funding_lot_runtime_attestations.proof_canonical=262144；原 upstream_attempts.route_decisions与pp_attempt_origins.route_decisions各272144；其他字段不泛放宽 |
| cell byte accounting | text/bytea实际长度；bool每cell1；time每cell32；NULL text/bytea0，bool/time仍固定成本 |
| pure logical budget | Rows全部数组合计100000/16MiB；旧Base投影与core自身预检各核原界，不给四域分别100000/16MiB |
| context | 读取每页/行、构索引/parse/hash/大整数/枚举链至多每256工作步检查；独立检查绝对deadline |

先预计原字符串/bytea与row总数，再创建索引、JSON parse、大整数、按业务键排序或hash；不得先大分配再发现超界。存在于多个logical数组的绑定按logical真实重复成本计，物理共享列则只计一次，两个计数不能混写。所有复核/投影/多个core共享同一外层absolute window，不给每个新域重新起时钟。

两列route_decisions的272144只用于JSONB::text传输：原五字段对象与数组分隔最多增加`10*n-1`空格，n≤1000，固定`262144+10000`包住该差异；pure仍按原Go类型、字段序与JSON转义重建并检查262144业务界，不放宽业务输入，也不从normalized JSON长度错误收窄。完整uint64 weight的上半区、64位priority极值、1000项和业务长度边界都需独立向量验证。

MAT页面先用octet_length核field资格，超界cell投影NULL但qualified=false并返回resource，不能SQL WHERE过滤掉超界行。timestamp用timestamptz_send binary8保留invalid/Infinity，不把不可表示时刻强制Go零值。bytea clone，不能借驱动buffer别名在qualify前被修改。

每行必须Next→Scan；tableEOF要求最终Next=false、Err=nil、Close=nil三者。恰128行再读空页；重复/不推进cursor、超page、Scan/Err/Close失败、earlyClose都不完整。metadata/rowCount不能代替EOF。schema限合法UTF8非NUL、1..63bytes；Query typednil在入口拒绝。

错误优先级：已经确定的resource保持resource，清理lease触发的cancel不能改写；pure明确返回timeout/canceled须保留，即使ctx.Err尚未发布；随后按真实context和执行失败映射。field/type/NULL/generated无法投影时用固定 `cellRef(table,row,column)`去重拒绝，对应core state=input_mismatch且**不调用该core**；不能填sentinel后记executed。SQL/迭代失败不返回PhysicalEOFComplete=true；捕获的错误不可被后续Close覆盖为成功。

qualification用调用自己的context和不可变raw session，独立验证registry/table/column集合、EOF、cell种类、NULL、time valid、UTF8、cursor前进和row/byte重新计账，再重跑原core、PBprefix、procurementprefix及wallet；逐值比较完整报告与Scanned物理/逻辑count。不信原Summary、不复用旧ctx、不共享可变rejected map，不因新pure PASS而跳过原算法。

## 9. basis、catalog 与安装材料

### 9.1 144-table basis

新descriptor逐关系继承原 [descriptor-prefix-v1.json](../../internal/fundinglotbasis/materials/descriptor-prefix-v1.json) 的112表，再加入32新表并展开原表新增列；reconciliation_cases/billing_profile_blocks原已包含，不重复计表。descriptor为固定JSON数组，每项 `table,primary_key,columns`，列tuple为`[attnum,name,type_oid,typmod,not_null,collation_oid]`。新 `ReadProcurementPrefix`入口钉住新descriptor/scope/准确counts/canonicalbytes/SHA，原loader与材料不改。

保留原framing header `moni-aigate/funding-lots-basis/v1\0` 与PG16 send/manifest/frame编码；descriptor canonical及其SHA在frame中提供新域区分，不无故更换原格式。数据库/schema OID、PG16/server版本、encoding、collation/send身份等原环境绑定保留，同RR事实。新basis仍100000 rows/64MiB、key262144、record1048576、page128，metadata2048rows/1MiB/text65536；generated也包含，xmin不包含。新scalaroid/typmod不允许由live自报作为expected。

### 9.2 160-table catalog 与新 writer pins

新Profile只接受固定160关系及准确函数/类型/默认ACL/成员/constraint/index/trigger/sequence/generated/dependency集合。NOLOGINwriter、封闭public根、private helpers、identity叶子、原020 PUBLIC撤销、APP精确补授、PB/021共享闭包和deferred提交身份都进入新expected。`SECURITY DEFINER` owner、安全search_path/proconfig、动态SQL禁止与schema限定依赖逐项精确核；APP不能通过PUBLIC/INHERIT/SET/ADMIN/defaultACL成为writer。

新V4还必须认证[实时xmin权限sidecar](b-procurement-full-acl-contract.md#22-实时xmin的精确系统列sidecar)：固定负attnum原始列ACL与逐角色有效SELECT权限，完整拒绝额外系统列授权/继承旁路。旧collector的attnum>0过滤不能直接覆盖该义务，需独立新采集/expected材料；旧profile查询与roots不改。xmin只用于原020/B-P1实时guard权限，不读入历史raw rows/纯DTO/basis，不改变新表715列或上述关系计数。

新增writer pin使用新具体pins类型/私有验证状态，不能给旧Pins静默加一个optional角色后授权。静态/runtime校验同实际tx/conn/pgconn的before/after身份与RR snapshot，runtime用actual LOGIN，SET ROLE不代替。新typedproof绑定profile4、schema/db/role OID/root和同事务；旧CatalogProof不能转换为V4authority。保护构造器须从自己owned的exact catalog/pins取得当前候选APP OID，与每个root.session_role_oid逐值绑定；根出生另核实际session_user。当前合同固定同一APP OID，不接受caller映射旧role、按角色同名换OID或重建环境后自行接纳旧root；这种历史兼容若确需支持，须另有完整角色前驱合同及新材料，不能用时间戳/旧报告或pure PASS补授权。

catalog的raw/normalized bounds由固定expected的multiset multiplicity推导，不能按livecatalog数量扩容；新expected物料若超原hardbound应先显式设计验证，不用放开上限躲资源失败。总共享5秒不因更多表续时；原catalog race/timing gate仍按真实结果报告。

安装仍检查原16 funding-lot输出表empty-at-issue；业务source与32新来源不是“必须清空的初始化输出”，合法既有prefix历史可以非空。不得为了让安装通过删除历史或强制32表为空。旧未受证历史也不会因为新catalog精确而自动补证。

## 10. 必须接入的真实保护阶段

以下为新profile的实施退出条件，不能只交付pure库/reader后声称业务已闭合：

| 阶段 | 必须消费/绑定的真实事实 |
| --- | --- |
| maintenance plan/init | 新BeginProcurementPrefixPlan/Init显式选择V4；installation/snapshot proof为原owned状态，caller不能传一份外部CatalogProof改Permitprofile |
| init locking | 原fixed锁集+B-P1三表+32新表及扩入既有来源的固定集合，sort后SHARE ROW EXCLUSIVE；不按metadata临时选表，也不漏共享原表 |
| ScanCompleteSources / ScanSourceWallet | 仍kind6/7一次性bits；SourceProfile只从原ownedV4installation取得，不开新kind让失败重试绕过 |
| read lease | copiedhandles共private nonce/lifecycle，Gate忙包括SQL/迭代/CPU；失败、earlyClose、取消及samekind换wrapper均不能新获attempt |
| maintenance scan adapter | 新wrapper仅V4、固定scope105/121与9/10算法；adapter独占lease.Finish；pure reader不能制造ScanProof |
| source/basis/checker proof | 同一ownedRR事实完成准确readset、basis及新aggregate，并将所有root.session_role_oid逐值等owned exact catalog的固定APP OID；source-only不得替wallet；完整事实不得以report/hash注入或同名新OID映射 |
| epoch/payment projections | 新facts专用projection先独立qualify全部raw，再从该raw投影，不能转成旧facts复用跳过新source |
| protected init/output | 新canonical材料显式绑定V4/profile/catalogroot/descriptor/readset/basis/source proofs与原epoch；全输出受保护事务真实写入、回读核验 |
| observer/history/current | 新格式/typed入口绑定同一候选材料与原前驱；旧V3历史/current只按旧格式认，不猜测升级，不改旧root/golden |
| application runtime | actual APP RR RO内读121表、原+PBprefix+采购prefix及wallet全重验，exactscope/count/算法；原资金/支付/本地sandbox证明仍逐项保留 |
| history verification | original001–022 manifest/checksum原样验证，另核精确候选物料；不修改已应用迁移或伪装成历史迁移23已应用 |

应用runtime整个验证窗口仍30秒，identity子预算5秒，catalog5秒；共享绝对截止，不因新profile/新投影重新renew。RR RO中不得切连接、SET ROLE、跨事务复用proof或用第二snapshot拼事实。observer/runtimeproof、维护Permit均由保护构造器发行，不从caller report/bool创建。

旧history/current和V4之间必须明确拒绝错profile/错材料/缺前驱的组合；合法兼容仅是读取旧证据并逐值确认其原前驱身份，再按**新格式**发行新候选证据，不把旧证据本身升级为新权限。新格式准确字段/版本与材料由集成设计钉住并交付真实验证器；未交付前该行仍为未完成项。

## 11. 独立 oracle 与攻击验收

纯oracle从固定业务输入构造期望的真实旧语义，用独立Go JSON/大整数/显式状态机，不import新SQL/helper算expected。至少覆盖成本合同§12全部场景，并增加全账户两attempt交错、多root同xid、configure rejected/not_found、跨scheduled quote、跨attempt/无attempt sticky、跨request meter reuse、每种no-effect、所有canonicalstep的node与全部generated逐列mutation。

逐一删除、增加、重复、换ID/tenant/root/step/xid/source-role、改before/after/quote/金额、分叉/跳号/漏尾、仅改currenthead、替换同金额原件、缺反向pointer和旧无origin引用，必须分别产生具体mismatch或真实legacygap。对每条十一命题至少一条合法补证与一条恰缺对应来源的indeterminate，验证旧Base逐值不变；不使用把Unproven字段直接置零的fixture。

读取测试覆盖全部固定registry/DTO列映射（含nullable/generated）、数值PK而非字典序、正好page边界、超field/MAT资格、row/byte共享预算、invalid/infinite时间、typednil/schema、Next/Scan/Err/Close/EOF、deadline恰到、资源错误被cleanup取消的优先级。新facts类型反转换/伪造与报告注入负例必须拒绝。

实际PG专库另验actual APP LOGIN调用真实受审DB根，NOLOGINwriter真正执行，原020/B-P1/021/deferred一起生效；owner直写或SET ROLE不计。原API行为/报价/成员/权限失权重试、硬deadline跨锁等待、多次同额更正/唯一cost_recorded失败、商业资金/PB/audit/outbox原件都须核。逐项撤必要grant验证全根回滚；APP raw DML/private/SET writer/PUBLIC/defaultACL/search_path/临时对象/禁trigger/TRUNCATE攻击拒绝。

维护/安装/observer/runtime须实际完整121/144/160范围验，不拿mockreader返回EOF/count代替；同tx/conn/角色、lease once/lifecycle、取消/超时/回滚/未知COMMIT和错history/current必须拒绝。未知COMMIT仅按操作身份只读观察，不盲重做批准/财务效果，更不重发模型。fixture、静态材料测试、专库商业链路、生产供应商/部署各自记录，任何一层SKIP不能标另一层PASS。

## 12. 当前未满足项与交付门禁

本页已经决定表集合、纯core/DTO规则、重放对象、算法/新profile、兼容边界和预算，不把它们留作“以后再冻”。以下是具体尚未交付的材料/实现/验收，不因本文完成而勾D1：

目前已交付[32新表候选列布局](b-procurement-column-layout-v1.json)：715列（647可INSERT列、68 generated）及只读layout lint；此前711/64验证保留原历史。随后[固定关系材料](../../internal/procurementprefixschema/materials.go)已经展开上述新表、18原表110新增列及685项候选关系约束，隔离结构和命名声明边的既有验收见[关系记录](../../docs/verification/procurement-relation-materials.md)。完整根/trigger/ACL/profile不属于该关系包，宽TEXT复合索引接受集亦未因此解决。

本轮[40原件union物料](b-procurement-original-core-layout-v1.json)与[procurementprefix静态DTO](../../internal/procurementprefix/types.go)覆盖40表482列和32表715列，完整72数组具名计数；[scalar预检](../../internal/procurementprefix/scalar.go)和[旧Base投影](../../internal/procurementprefix/base_projection.go)已实现，独立旧readset/全部110扩列/旧scanner173字段的定向race通过。原Base checker未修改。后续按[私有实施合同](b-procurement-replay-implementation-contract.md)加入来源/请求/账户叶子与十一Required精确键集合，范围和测试见[本轮验证](../../docs/verification/procurement-replay-leaves.md)；成本/finish及来源续片见[历史验证](../../docs/verification/procurement-cost-finish-replay.md)，当前指针/Usage与非单调墙钟续片见[新验证](../../docs/verification/procurement-current-source-clocks.md)，人工P→A→R、派生decision.before_latest桥与Reconciliation/finish完整行内联合见[顺序验证](../../docs/verification/procurement-reconciliation-order.md)。人工last-source义务已实施；未派生latest/sent与历史PB观察仍须收口，私有LegacyCases不授资格。[收口计划](b-procurement-replay-closure-plan.md)列逐对象依赖和真实观察缺口；完整来源、AttemptCost剩余历史cut、同次Base执行与aggregate仍未完成，因此**没有导出的Check**，类型Report和局部Candidates的存在不等于新scope已可执行。

[账户命令writer材料](../../internal/procurementaccountwriter/materials.go)按[独立合同](b-procurement-account-writer-contract.md)交付20函数/29触发器与精确账户ACL隔离候选，两个公开根只处理create/configure；actual APP LOGIN限定专项已通过，实际范围和未验矩阵见[本片记录](../../docs/verification/procurement-account-prefix.md)。当前执行错误合同为81个PP状态，含PP906旧未受证/尚未实施域拒绝；不能把局部账户校验器视作全部五个全域validator已经完成。

[宽TEXT精确链接合同](b-procurement-exact-link-contract.md)与[268 PK/UK、291 FK清单](b-procurement-exact-link-inventory-v1.json)先冻结native原定位FK与双端逐列比较方向。独立[RC原语实验](../../internal/procurementprefixschema/exact_link_experiment_postgres_test.go)6组PG race场景通过，只验证长值/NULL/锁后新语句重读/双端最终态及tracked RR写拒绝。685项关系约束仍未修改，全域来源出生/guard/ACL、逐边映射和宽TEXT接受集门禁保持。

1. 全量registry、剩余受控根/helper/全域validator/guard和完整触发/ACL组合、宽TEXT全域精确关系方案；已有715新列、原110扩列或40类原件DTO不能冒充105/121表完整读集总列数，本文不从尚未生成的全量物料编造总数和SHA。
2. 新canonical readset/descriptor/catalogexpected及其真实字节数/count/SHA，固定selector与错profile拒绝；旧材料保持原样。
3. `procurementprefix`真实独立四域实现、内部调用旧checker并原样保存Base、十一对象义务枚举及所有新/原件双向集合与最终aggregate；已有无损投影不等于完整Check，缺来源不能默认covered。
4. 新scanner/privatefacts/qualification/basis/catalog与maintenance、protectedinit、observer/history/current、runtime真实接线及准确新格式；只完成pure或SQLcodec不关闭这些门禁。
5. actual LOGIN/全部商业原件/取消与资源/并发锁/权限删除/旧模式回归和完整保护链专库证据；目录catalograce预算门禁原样保留。生产官方/三方渠道与真实部署验收仍单列。

集成人必须汇总并行结果、语义复核统一架构与本页交叉合同后，按AGENTS执行architecture refresh/check。关系材料、纯核基础、账户候选、精确链接原语与完整商业/保护链分别计账；D1、I1完整writer、A0、B-P3及R2保持未完成，无已应用迁移或线上接线变化。

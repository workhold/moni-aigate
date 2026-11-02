# 022-C0 采购原始证据、状态事件与成本来源图（根冻结）

2026-11-03 tenant 身份校准：本合同中的 tenant 主键及真正指向它的外键按[受保护事务第6节](c0-protected-init-phase-contract.md#6-验收与交付)接受合法旧 TEXT（含空串），并保留原字节/行预算、精确关联及 NULL/空串区别；旧泛称“必需身份非空”不再涵盖这些 tenant 字段。其他对象 ID、非 tenant 引用、UUID、金额/状态与在线调用合同保持。PB 的 tenant target/key configure scope、Sandbox 按固定 kind 选择 scope 亦以该校准为准。

2026-11-02，根审冻结纯 core；reader/writer 仍由根独占接线。本合同把现有十九表五个局部 core 未覆盖的 metering、conflict、finalization、语义 identity、全部预算事件 type/evidence、cost_recorded 与当前成本来源、账户配置来源放在同一原始图下。目标是为 C0 Procurement 域提供有明确缺证的独立三态结果，不能将五份 Balanced 摘要 AND 后改名全源通过。本片仍不发行数据库完整性/维护权/RuntimeProof、C0 lot、PB 或真实供应商账单证明。

依据 [019合同](../019-procurement-accounts/contract.md)、[C0独立核验](c0-verifier-contract.md)、[十九表读取](c0-procurement-history-read-contract.md)、[evidence生命周期研究](../../docs/research/procurement-evidence-lifecycle.md)、[HLD](../../docs/design/HLD.md) 和 [LLD](../../docs/design/LLD.md)。源代码为 [evidence](../../internal/billing/evidence.go)、[采购状态写入](../../internal/billing/procurement.go)、[legacy更正](../../internal/billing/recovery.go)、[配置命令](../../internal/procurement/store.go)，DDL为 [002](../../migrations/002_billing.sql)、[014](../../migrations/014_procurement_accounts.sql)、[020](../../migrations/020_periodic_budgets.sql)。研究文档不构成数据库验收或规范的新版本；本合同明确化其剩余决策。

## 1. API、完整原始依赖与结果

冻结纯入口：

```go
func CheckProcurementEvidenceGraph(context.Context, ProcurementEvidenceGraphRows) (ProcurementEvidenceGraphCheck,error)
```

固定 scope 为 `procurement_domain_evidence_graph_v1`。输入只收下面二十三组 owned raw rows，不收 bool、旧summary、caller提供的digest候选、完整性位或proof。现有五个纯 core 必须在该调用内部从**同一份原始图**重跑固定投影，逐字段保留各自 mismatch，再执行本合同全部新双向核验和历史缺证裁决；并非调用方拼接五份摘要。旧 pure/core/reader 接口不改；新增内存投影只复用字符串值，不增加SQL/新快照、不另计同一物理行扫描，也不重置父context或总资源预算。

输入字段固定为 `Channels,Credentials,Versions,PoolMemberships,Tenants,Requests,CostVersions,Accounts,Bindings,Attempts,Reservations,BudgetEvents,Users,Corrections,Proposals,Commands,Observations,Audits,Outbox,Identities,Metering,Conflicts,Finalizations`。除下述专属扩展外，依次复用十九表原DTO：`ProcurementAttemptChannelRow/CredentialRow/VersionRow/PoolMemberRow/TenantRow/CostVersionRow/BindingRow/AttemptRow`、`ProcurementApprovalUserRow/CostCorrectionRow/CostProposalRow`、`ProcurementCommandRow/ObservationRow/AuditRow/OutboxRow`。

```go
type ProcurementEvidenceRequestRow struct {
    ID,TenantID,InputUpperBound,OutputLimit,PolicyVersion string
    LatestFinalizationID *string
    RequestDeadline time.Time
}
type ProcurementEvidenceAccountRow struct {
    ID,Label,Provider,State,LimitAmount,HeldAmount,SpentAmount,Revision string
    CreatedAt time.Time
}
type ProcurementEvidenceReservationRow struct {
    AttemptID,AccountID,BindingID,InputBound,OutputBound,UpperAmount,HeldAmount,SpentAmount,State,Revision string
    Anomaly bool
    CreatedAt,UpdatedAt time.Time
}
type ProcurementEvidenceBudgetEventRow struct {
    ID,EventKey,AccountID,AttemptID,HeldDelta,SpentDelta,Type,EvidenceRef string
    CreatedAt time.Time
}
type ProcurementEvidenceIdentityRow struct { AttemptID,Digest,EventID string; CreatedAt time.Time }
type ProcurementEvidenceMeterRow struct {
    ID,TenantID,RequestID,Source,SourceEventID,UsageJSON string
    AttemptID *string
    SchemaVersion int32
    HappenedAt,ReceivedAt time.Time
}
type ProcurementEvidenceConflictRow struct { ID,EventID,UsageJSON string; CreatedAt time.Time }
type ProcurementEvidenceFinalizationRow struct {
    ID,TenantID,RequestID,EventID,InputDigest,ProtocolOutcome,TransportEndReason,DeliveryStage,FinishReason,PolicyVersion string
    ContentWritten bool
    CreatedAt time.Time
}
```

`ProcurementEvidenceGraphCheck` 固定 `Scope,Scanned,Status,Balanced,CoverageComplete,Structural,Mismatches,Unproven`。Scanned 对应二十三组 uint64 原始行计数，包含重复/坏行；Structural 是本次内部实际重跑的五种固定 mismatch 类型（预算/绑定/双审/命令/成本审计），不接受外部构造的结果。新 Mismatches/Unproven 固定字段见§11，任何结果不含行ID、金额、JSON、秘密、内部原因或SQL。

- 任一结构或新 mismatch 非零：Status=`mismatch`、Balanced=false；CoverageComplete 仍独立取决于Unproven，不能隐藏同时存在的历史缺证。
- mismatch全零而Unproven非零：Status=`indeterminate`、Balanced=false、CoverageComplete=false。
- 所有固定raw算法实际完成，且 mismatch/Unproven全部零：Status=`balanced`、Balanced=true、CoverageComplete=true，**仅表示本合同封闭的持久采购关系，不等于整库全来源/维护授权**。
- 未读完、缺权限/表/列、未知版本、资源/查询/解析执行失败或依赖core执行错误：固定error+零check，不转成Unproven，也不返回部分summary。

## 2. 精确读取增量与共享预算

十九表仍完整从基表出发，只有以下四个旧投影扩展和四张新增表。固定SQL显式列、NULL不coalesce、JSONB仅取`::pg_catalog.text`，不读请求/模型正文、request_hmac、凭据密文或任意供应商秘密。

| 关系 | 固定列顺序 | C序keyset |
| --- | --- | --- |
| request_records | `id,tenant_id,input_upper_bound::pg_catalog.text,output_limit::pg_catalog.text,policy_version,latest_finalization_id,request_deadline` | id |
| procurement_accounts | `id,label,provider,state,limit_amount::pg_catalog.text,held_amount::pg_catalog.text,spent_amount::pg_catalog.text,revision::pg_catalog.text,created_at` | id |
| procurement_reservations | `attempt_id,account_id,binding_id,input_bound::pg_catalog.text,output_bound::pg_catalog.text,upper_amount::pg_catalog.text,held_amount::pg_catalog.text,spent_amount::pg_catalog.text,state,anomaly,revision::pg_catalog.text,created_at,updated_at` | attempt_id |
| procurement_budget_events | `id,event_key,account_id,attempt_id,held_delta::pg_catalog.text,spent_delta::pg_catalog.text,type,evidence_ref,created_at` | id |
| procurement_evidence_identities | `attempt_id,evidence_digest,event_id,created_at` | `(attempt_id,evidence_digest)` 分列tuple |
| metering_events | `id,tenant_id,request_id,attempt_id,source,source_event_id,schema_version,normalized_usage::pg_catalog.text,happened_at,received_at` | id |
| metering_event_conflicts | `id,event_id,normalized_usage::pg_catalog.text,created_at` | id |
| finalization_records | `id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version,created_at` | id |

原十九表其余列与顺序按各已冻合同；新增core用完整expanded row生成老core精确投影，不能让root重复读取同表。root将来独占同session reader及维护接线；本片不授权修改它们。

二十三组与root更大全域union共用原 ReadLease绝对deadline、100000总行/16MiB所有所选原文UTF8字节、128行C序页；tuple游标不得字符串拼接。首次hasCursor独立，完整Next/Scan/Err/Close后短页才EOF，整128行还须续页。任何执行失败原lease Abort+零结果；数据mismatch仍完整扫描并由根唯一Finish，不串调会自行Finish的旧维护入口。

## 3. 词法、Usage、身份与重复

复用物理TEXT身份，不将历史user/tenant/request/attempt/meter/finalization强转UUID。public FinalizationInput.ID使用`uuid.Parse`而非规范字符串相等，合法大写/其它解析形式不能由本core额外拒绝。identity.EventID严格按014的小写UUID字面形状；Digest/InputDigest为64位小写hex。meter Source为1..80字节、SourceEventID为1..200字节（writer只限制字节长度，不追溯增加无控制/trim规则）；finish_reason≤80字节允许空。所有投影合法UTF8且无NUL。新时间均有限、微秒精度；happened_at是调用方可指定观测时刻，不用于推断事务先后、expiry或来源合法性。

Usage闭合六键：`status,input_tokens,output_tokens,total_tokens,cached_tokens,reasoning_tokens`。status只允许confirmed_final/provisional/estimated/missing/conflicting；五个数必须精确JSON integer、规范非负int64，不经float，不接受未知/缺失/重复（含解码后同名）键、null、bool、数组/对象、尾JSON、非法UTF8/孤立surrogate。cached<=input、reasoning<=output；confirmed_final以big.Int核input+output=total，其他status不要求等式。JSONB键序/空白不是摘要原文；独立解析后按固定旧协议重新marshal。

冻结数值协议：JSONB可把原JSON整数字面正规化（例如直接SQL fixture），本片按正式Usage int64 writer协议核闭合规范值；owner fixture不是新增可接受历史协议。预算NUMERIC沿现有core支持100位精确整数；宽乘/ceil先核int64原始字面再用big.Int，不对长任意原文先SetString。

每物理主键分别去重，保留合法首行，额外行只计duplicate且不放大后续关系。identity PK为typed `(attempt,digest)`；meter唯一键为typed `(tenant,source,source_event_id)`，**不是attempt+source**。已有十九表自然键沿原合同。conflict只按id去重，不能对 `(event,usage)` 语义去重：合法冲突重放确实会多插相同行。finalization允许多行引用同meter；identity允许同meter对应多个digest/attempt，同digest的新meter也可以没有identity直接指向它。

## 4. metering、normal finalization 与request当前引用

每meter的tenant/request存在且一致；非NULL attempt也存在并同tenant/request。NULL attempt合法（例如approved_reconciliation/customer级finalization），不能猜一个采购attempt。每conflict引用真实meter且Usage合法。每finalization引用真实request和meter，三方tenant/request一致；policy_version精确等该request冻结policy_version。protocol_outcome只允许completed/failed/incomplete/cancelled/unknown；transport reason为done/eof/provider_error/platform_error/platform_timeout/client_cancelled/connection_error/unknown/not_sent/rejected_no_consumption；delivery只允许none/committed/content/unknown，content_written真当且仅当delivery=content。

request.latest_finalization_id非NULL时必须指向同tenant/request的finalization；存在normal finalization而latest为NULL是确定缺失。latest不是按created_at或UUID取MAX；原ID幂等重放不会改变latest。不从 latest 推导当前 request.status：PrepareDispatch 可将 request.status 改为 dispatching 而保留旧 latest。冲突的 markPending 也可独立改变 billing_status，二者均不作为采购成本 oracle。

同request只要任何normal行已content，latest必须content；否则若任何行已committed，latest不能none/unknown。这是sticky吸收态与完整集合可直接证明的矛盾，不需要发明顺序。更多历史前驱见证按§5，不把存在一个可能排序等价于真实顺序已证。

每meter须有正式normal finalization，或合法冲突创建的来源见证：没有normal行的meter必须有同event、Usage与meter自身相同的conflict（新event插入后遇到另一confirmed_final），其本身Usage=confirmed_final、attempt非NULL，且同tenant/attempt完整meter集合存在不同confirmed Usage的正常锚点/可达冲突同组。只有usage不同的后续source碰撞conflict不能解释meter最初是怎样出生的。不得一律要求每meter都必须有normal行，也不得容许“全无normal锚点的互相假设循环”自证来源。

同attempt多个不同confirmed_final可以合法并存，后来的冲突没有normal行；同event的conflict.Usage也可以等于meter.Usage（不同final触发或incoming身份冲突）。不能因这些合法事实直接mismatch。

## 5. finalization input_digest与原delivery缺证

独立复刻旧`FinalizationInput`的JSON字段声明顺序，外层字段无json tag：`ID,TenantID,RequestID,AttemptID,Source,SourceEventID,Usage,ProtocolOutcome,TransportEndReason,DeliveryStage,FinishReason,ContentWritten,ObservedAt`。ObservedAt统一为Go零时间的`"0001-01-01T00:00:00Z"`；Usage按§3六tag次序、数为JSON number，沿encoding/json默认HTML/UTF8转义，最后SHA256小写hex。不能直接hash JSONB文本，不能改成排序map或新增domain separator。

落库delivery/content已在digest之后归一/sticky升级。对每normal行从它明确引用的meter取得原Usage/Source/SourceEventID/nullable AttemptID，其余原值取finalization/request。枚举四种原stage×两种原content的8个固定候选，再按输出可达规则排除不可能组合：content/true可由八组合产生；committed/false可由none/unknown/committed的false输入产生；none/false或unknown/false仅自身false输入。**零候选hash命中是确定InputDigestMismatch**，不是缺证；候选不从全库其它attempt猜测。

有候选命中只能说明缺失原输入的一种有限preimage，不能自动证明前驱存在且当时足以升级。对命中候选在无前驱时自归一即可得到落库输出的行（尤其原content=true或stage=content），不计原delivery升级缺证；当全部命中候选都必须依赖一个未持久化的committed/content前驱才能产生输出，计该finalization一次`OriginalDeliveryPredecessorUnproven`。前驱不存在任何可达锚点时是确定sticky源矛盾；存在锚点仍不能拿created_at/UUID/xmin猜原前驱。单request仅一normal行却需要sticky升级也为确定矛盾。

真实历史前驱序列没有持久 predecessor/seq。冻结判据按 request：合法 normal 行的落库 delivery 等级（none/unknown=0，committed=1，content=2）出现超过一个等级，或任一行全部命中 input 候选均需 sticky 前驱，则计该 request 一次 `FinalizationOrderUnproven`。单等级且全部可自归一不缺证；零命中或不存在可达自归一 anchor 仍是确定 mismatch。

## 6. 采购语义identity双向关系与冲突缺证

独立采购digest为旧JSON `{"Usage":<六字段Usage>,"Reason":<string>}` 的SHA256，无domain separator，外层大写字段与顺序固定。只对标准库独立DTO序列化，不import/call writer。每identity引用存在的managed reservation/attempt与meter，meter.tenant必须等attempt.tenant；**不要求meter旧request/attempt等于incoming identity归属**。

每个normal finalization若meter.attempt非NULL且为managed，都要求该attempt存在 `H(meter.Usage,final.transport_end_reason)` identity；但identity.event_id可保留同语义首次的另一个meter。不得要求identity必须反指这条较新的event，或每个新event都新增identity/预算事件。

反向identity先找它所引用event上、同attempt/request的normal finalization及精确Usage/Reason摘要。若存在匹配，语义原文可确定；否则只允许从**同event实际conflict Usage**与十个允许reason构造有限候选。全无候选为确定`IdentityDigestRows`；命中仍计该identity一次`ConflictReasonUnproven`，因为原incoming reason未持久化，不把有限候选冒充原始观察。若meter旧attempt/request与identity归属不同，还必须有conflict候选和同tenant父，并计一次`IncomingBindingUnproven`；仅因它跨attempt不得mismatch。

每条实际conflict根均缺incoming request/attempt/reason（incoming也可能根本无attempt），完整域必须按该行计`ConflictIncomingUnproven`。不能要求它必然属于meter原attempt、必有采购identity、必有预算disputed事件，也不能用整库attempt×usage×reason交叉枚举猜归属。重复conflict根照实际条数计缺证，不语义折叠。缺少正式正常/冲突见证的identity是确定孤儿；已有本节合法缺证边仍继续扫完整反向集合。

## 7. 全部采购预算事件与状态边

每managed reservation恰有一个reserved事件，精确 `reserve:`+attempt、account同reservation、delta=(upper,0)、evidence为空。其他事件都从自身完整行回到同account/attempt的reservation，八种type封闭；key必须由已知完整身份构造精确比较，不split任意前缀。外部correction.evidence_ref不是meter FK。

| type | 精确event_key / evidence | 确定可核原始关系 |
| --- | --- | --- |
| released_no_consumption | `release:`+attempt / meter ID | 关联同attempt normal finalization reason=not_sent或rejected_no_consumption；delta=(-upper,0) |
| consumption_unknown | `unknown:`+attempt / meter ID | 同attempt normal非confirmed Usage且reason非无消费；delta=(0,0) |
| cost_confirmed | `settle:`+attempt / meter ID | 同attempt normal confirmed Usage且非无消费；delta=(-upper,精确冻结价ceil成本) |
| evidence_disputed | `conflict:`+attempt+`:`+meter / meter ID | 必有同tenant真实meter、managed identity的同event语义见证；来源可为conflict incoming或released后正confirmed/settled正spent后无消费，不强制一定有conflict根 |
| expired_without_permit | `release:`+attempt / 空 | 恢复prepared+prepared路径；delta=(-upper,0)，不能制造meter/identity |
| recovered_unknown | `unknown:`+attempt / 空 | 恢复其余未决路径；delta=(0,0)，不能制造meter/identity |
| cost_corrected | `correction:`+approval operation / 外部证据 | managed已批准proposal/correction的双向精确关系；target_spent=proposal.amount，故before_spent=amount−spent_delta、before_held=−held_delta必须非负 |

`evidence_disputed.spent_delta=0`、held_delta≥0且≤upper；每次争议目标held=max(upper−当时spent,0)，不拿当前spent代替当时spent。cost_confirmed与release类主终态至多一个且彼此互斥；unknown类最多一个且只能在尚未终态时发生。预算恒等不替代这些类型/来源检查。三个 normal 类型 released_no_consumption/consumption_unknown/cost_confirmed 还必须绑定该 normal 的精确语义 identity 且 identity.event_id=event.evidence_ref：digest 重放会在产预算 event 前返回，不能用后续同语义 meter 改写原 effect 来源。

状态确定边界：prepared/sending只能有原reserved、无identity/normal证据和后续预算effect；unknown须有一次unknown类、held=upper/spent=0/anomaly=false，不得已有terminal/dispute/correction；released须有一次release类、held/spent=0，不能有已经dispute/correction后又返回released的图；settled须有cost_confirmed或已批准correction；disputed须有evidence_disputed、anomaly=true、held=max(upper−spent,0)。合法新identity在released/settled/disputed之后可以没有新预算event，不能反推每个identity必须对应一种event。

先独立核每attempt与account的全部delta累计、精确reserved、当前缓存，再核类型与源关系。对于只有reserved、可选unknown、至多一个主终态且无后续争议/更正的图，业务偏序足以独立重放资金桶，不记EventOrder缺证。单个争议/更正能由唯一前缀推出before桶时同样直接核。冻结复杂条件为同 attempt 的 evidence_disputed 与 cost_corrected 合计超过 1 行：缺少 before/revision/稳定 seq，按该 attempt 一次 `ProcurementEventOrderUnproven`，仍执行所有确定 delta/来源/状态/反向检查。合计不超过 1 时，从 reserved→可选 unknown→可选唯一主终态→该唯一复杂 event 的业务偏序核 before/delta/state，不按 timestamp。

020的periodic_budget_events确有seq/before/after和source_id绑定，未来可能提供部分新event的真实顺序；**本23表输入不包含该原始域，不能从旧PB摘要借顺序或把PB解读为全旧历史见证**。根冻结本轮不引入 PB 顺序依赖，不加 caller“已验证顺序”参数。C0的独立PeriodicBudgets域仍必须实际执行。

## 8. 当前成本、cost_recorded与legacy历史

独立按冻结attempt两维价 `ceil((input*rateIn+output*rateOut)/1_000_000)` 推导成本；理由not_sent/rejected_no_consumption的recordAttemptCost为amount=0,basis=contract，不改变原cost_basis。其它候选必须Usage=confirmed_final、两费率非NULL、basis=contract/invoice，金额允许超过int64但仍为NUMERIC(100,0)的精确非负整数。不以当前a.cost_amount校准历史价格/Usage，不用当前cost_status反证旧cost_recorded。

每实际 `cost_recorded` outbox要求tenant=attempt.tenant、operation=attempt.id、schema1，闭合三个string字段 `attempt_id,amount,basis`，amount规范非负NUMERIC整数、basis contract/invoice，且有同attempt正常finalization提供完全相同的计算候选。不能用conflict缺失reason候选给cost_recorded伪造正常写入来源，因为冲突分支不调用recordAttemptCost。每attempt最多一条由原outbox typed unique验证；同值多final化不会多emit。

反向：每cost_confirmed / released_no_consumption预算事件应有该attempt唯一且金额/basis正确的cost_recorded；当前 contract/invoice 成本没有任何correction来源时也必须有正确cost_recorded来源。没有correction、没有可能冲突/争议前缀且已出现可计成本的normal行，也必须有记录；不能把应有outbox缺失写成历史gap。成本记录资格 gap 的冻结条件仅为同 attempt 至少一条实际 cost_corrected 或 evidence_disputed 预算 event，或至少一条同 tenant conflict（其 incoming 归属未持久化）。对无 reservation 的 legacy attempt，根追加冻结允许同 attempt 真实 provider_cost_corrections 原件作为有限前缀见证（legacy更正不产采购预算event，但也会先设已知cost并使后续normal跳过record）。条件满足且存在可计成本 normal 时按 attempt 一次 `CostRecordedEligibilityUnproven`；否则有可计 normal 却缺 cost_recorded 是确定错。无消费/确认预算 event 的独立应有记录义务仍然确定，不能用 gap 豁免。

managed当前状态：prepared/sending/unknown/disputed必须cost_status=unknown且amount=NULL；settled必须cost_status=contract/invoice且amount=reservation.spent，且能指向实际cost_recorded或已批准correction的同amount/basis声明；released允许unknown/NULL（无许可恢复不会制造成本）或contract/invoice且amount=0（随后正常零消费证据），不能contract/invoice正成本。invoice correction后重复旧证据不能抹掉原authoritative成本，不要求它等于任一旧Usage。contract/invoice来源选择有多个不同候选且不能由事件源唯一定位末次时，计`CurrentCostOrderUnproven`，不猜最后created_at。

legacy没有reservation，不造采购budget/identity；任意operation前缀不能把它改分类为managed。legacy correction可多次，旧cost.corrected审计actor未持久在source中，沿现有合同不要求它是当前users。当前 contract/invoice 成本必须匹配至少一实际cost_recorded/correction来源；未知但已有contract/invoice成本历史时，只有实际同tenant冲突根可解释reset，若完全无冲突则确定矛盾，有可能incoming归属但无法证明则计该attempt一次`CurrentCostOrderUnproven`。多次不同legacy更正的真实先后也按此计缺证，不拿总额相等或任取最大时间当顺序。

## 9. proposal屏障、账户配置来源与剩余历史义务

双审/correction/audit/outbox按十九表既有精确core重跑。每proposal.expected_evidence_count不得大于当前该attempt全部唯一identity计数，expected_revision不得大于当前reservation.revision；它们单调不减，所以这类超前是确定矛盾，合法旧proposal不能强等当前。approved且当前count/revision后来增加时，审批当时的精确prefix缺少序号/前驱，分别按proposal计`ProposalBarrierUnproven`；若二者都等当前且该次为不改桶/状态/anomaly的no-op审批，可直接核屏障，不因“历史”笼统计gap。计数增长来自新语义identity，不来自重复conflict或同digest新meter。

每account必须有唯一applied account_create命令，after ID/created_at等当前不可变身份，revision1/held0/spent0；direct owner fixture不是正式创建源。各创建/configure/revision_conflict观察的同 `(account,revision)` 配置投影label/provider/state/limit/created_at必须一致，held/spent是可变观察值不参与同版本比较；当前account同revision也须一致，历史revision不得超过当前。applied configure before/after已由旧core核revision0/+1与配置内容，本片还核不可变created_at与完整来源边，不能拿某个旧快照等当前最后配置。

自动anomaly暂停只改变active→paused，可能使配置revision+1且没有管理命令。若完整观测版本之间只缺该类一个暂停边，且存在本account真实可产生anomaly的采购事件，则不能误判缺configure命令；但event未保存触发时account revision，按该account一次`AccountPauseOrderingUnproven`。缺失改变label/provider/limit或paused→active的管理来源是确定矛盾；不能用自动暂停解释任意配置缺口。冻结为相邻已知 revision 间缺命令的边只允许差 1、active→paused、label/provider/limit/created_at 全不变，且本 account 有真实可致 anomaly 的事件；此边记 PauseOrdering gap。差值超过 1 或任何其它配置变化均为确定缺命令，扫描全部 commands 后不能假设合法中间 configure 尚未出现。

原reservation没有冻结account配置revision/limit/准入前全局held/spent；本片不假装能重演所有历史准入。冻结准入判据：0 reservation source 的账户无准入历史 gap；单 source 且创建 active、从未 config 变更/自动 pause、唯一 upper<=原 limit 可证；多 source 仅当全部效果都是 reserved、配置恒定 active、全部 upper 总和<=原 limit 时可证单调 prefix。其它有 source 的账户按 account 一次 `AccountAdmissionOrderUnproven`；可直接证明单 source 或全 reserved 的 upper 超原 limit 为确定准入矛盾并计 AccountAdmissionRows。此不证明外部余额，不借当前 state/limit 倒推过去。

历史平台角色/用户启停、库外invoice/真实供应商消费真实性不在本持久关系承诺内；不因此人为增加每行gap，也不能据当前身份写成历史授权已证。PB六表完整交叉生命周期、C0 top-xid/epoch/lots属于必须独立完成的域，本core不能代替。

## 10. 反向完整性、资源与错误

必须从每张真实表独立回扫：meter→request/attempt/normal或conflict出生；conflict→meter/Usage/真实incoming缺证；normal→request/meter/policy/input候选/采购identity；identity→managed+原meter/语义来源；所有八种budget event→reservation/account及相应meter/recovery/proposal来源；cost_recorded→正常证据，cost_corrected/三audit→双审或legacy来源；账户→唯一创建/配置链；每declared latest→同request finalization。不得只从已有settled/approved/reservation出发忽略孤儿或没有最终资金效果的证据。无关audit/outbox种类只共同原始词法/主键/typed唯一/预算，不误套成本JSON或源矩阵。

资源先按二十三组全量原始行/文本封闭检查，再建索引/解JSON/算big.Int；100000行、16MiB不按group/core/页重置。nullable字符串/JSON原文全部计入，派生值不重复计原文，不宣称限制RSS/PG排序。哈希候选必须按event索引和10个reason/8个delivery常数生成并共享cache，禁止identity×全库conflict/finalization笛卡尔搜索；预算图不做指数级全排列猜历史，复杂前缀明确gap。工作循环每256步核原context，末尾再核。

固定 sentinel `ErrProcurementEvidenceGraphRowsInput/Resource/Canceled/Timeout/Execution`。nil context为Input，资源超限Resource，父取消/到期分别Canceled/Timeout；依赖pure固定scope/返回形状异常为Execution。全部错误零check，确定数据形状/关系问题正常返回三态和完整计数，不能抛execution吞掉坏原件；不回显driver、SQL、JSON或private cause。

## 11. 固定计数与验收

Mismatches：`DuplicateIdentityRows,DuplicateMeterRows,DuplicateMeterSourceRows,DuplicateConflictRows,DuplicateFinalizationRows,InvalidTextRows,InvalidTimeRows,InvalidUsageRows,InvalidEnumRows,MeterIdentityRows,MeterBirthRows,ConflictIdentityRows,FinalizationIdentityRows,FinalizationPolicyRows,FinalizationInputDigestRows,FinalizationStickyRows,LatestFinalizationRows,IdentitySourceRows,IdentityDigestRows,NormalIdentityMissingRows,BudgetEventShapeRows,BudgetEventSourceRows,ReservedEventRows,StateEventRows,EventDeltaRows,CostRecordedJSONRows,CostRecordedSourceRows,CostRecordedMissingRows,CurrentCostRows,ProposalBarrierRows,AccountCreationRows,AccountConfigurationRows,AccountAdmissionRows`。十九表原重复/形状/双向资金投影错误由内部Structural对应类型保留，不静默裁掉重复原始行；新计数每源行每字段至多一次，关系两向各自可计一次。

Unproven：`OriginalDeliveryPredecessorUnproven`（finalization）、`FinalizationOrderUnproven`（request）、`ConflictIncomingUnproven`（实际conflict根）、`ConflictReasonUnproven`（仅候选匹配的identity）、`IncomingBindingUnproven`（跨原meter incoming identity）、`ProcurementEventOrderUnproven`（复杂attempt历史）、`CostRecordedEligibilityUnproven`（attempt）、`CurrentCostOrderUnproven`（attempt）、`ProposalBarrierUnproven`（proposal）、`AccountPauseOrderingUnproven`（account）、`AccountAdmissionOrderUnproven`（account）。准确行级触发条件见前文，未实现依赖/未读完不是缺证；mismatch和gap各自累计，不因已有错而停止反向枚举。

测试必须覆盖：空域/零价/MaxInt64 Usage与宽成本；managed prepared/sending、unknown、明确无消费、无许可恢复、sending恢复、正常确认、越token硬界或成本越界、已释放再正消费争议、已结正成本再无消费争议、disputed后仅新identity无预算event、重复语义新event、重复冲突根、跨request/attempt source碰撞、同event相同Usage冲突、多个不同confirmed_final；单/多/无normal的meter出生锚点；原digest固定独立字节向量/8候选和0命中、sticky升级缺前驱与吸收态确定反例、latest原ID重放不改指针；单/多次managed双审和legacy更正、proposed与approved历史count增长、invoice降低、cost_recorded缺/错/孤儿与旧当前成本不同不误报；账户创建/无op配置/不同revision/自动暂停缺命令与异常人为配置缺源；全部双向孤儿/跨tenant/重复/严格Usage JSON/NUMERIC形状/共享预算/取消超时零结果。

真实PG由根串行在专用profile验正式API生命周期和owner受控损坏，含23表固定投影、128/129 tuple/单列分页、全部Next/Scan/Err/Close失败、同lease/RR/总预算/零资金写；不并行跑重PG，不将纯fixture或五摘要通过叫商业、供应商或生产验收。纯core定向race/vet与独立review后，根汇总reader/架构并统一refresh/提交。

## 12. 根冻结记录与执行边界

2026-11-02 根审已授权实现本合同纯 core 与 tests，裁决已写入对应正文：

1. 新入口内部固定从同 raw 重跑五 core，核五个固定 scope 与结果，不接外部 summary；旧 API 不变。
2. finalization 等级/前驱、复杂 event 合计>1、成本资格实际 event/conflict 条件均按正文封闭判据；不把全非空域标缺证。
3. 账户创建/配置链/一步自动 pause 与准入 0/1/多 source 判据本轮全部实现，真实历史缺口精确计数。
4. 本轮不增加 PB 顺序依赖，PB 独立域继续实际执行；复杂历史保留三态。
5. request.status 不在新投影中且不从 latest 推导；成本实际 enum 为 contract/invoice/unknown。

reader、root session、迁移、README/统一架构由根汇总接线；本 agent 不 refresh 或提交。纯 fixture/race 不构成真实供应商、生产或商业数据库验收。

根补充复核：账户版本排序每 256 比较观察原 context，比较结果保持严格数值序并在排序后返回观察到的固定取消错误；任一 source upper 无效时不以部分金额和声明可证准入。正常预算 effect 的 identity EventID 绑定与 legacy cost correction 资格反例均已纳入实现/回归。

## 13. 本片实现与验证记录（2026-11-02）

纯入口及五 core 固定编排见 [entry](../../internal/fundinglotverify/procurement_evidence_graph.go)，原始证据/摘要/出生与 identity 反向关系见 [evidence](../../internal/fundinglotverify/procurement_evidence_graph_evidence.go)，八类状态/资金事件与成本来源见 [events](../../internal/fundinglotverify/procurement_evidence_graph_events.go)，账户创建/版本链/准入见 [accounts](../../internal/fundinglotverify/procurement_evidence_graph_accounts.go)，回归见 [tests](../../internal/fundinglotverify/procurement_evidence_graph_test.go)。未修改旧 pure API、SQL writer、reader 或 root session。

已执行 `go test ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1`，通过（0.452s）；gofmt 与本片 `git diff --check` 通过。实际覆盖空/准备态/五 core 原始损坏、正常确认/零消费/恢复/unknown、语义重放、latest/sticky 八候选与固定独立 digest、严格 Usage 与 MaxInt64 宽乘、零价格及 token 越界 anomaly、冲突出生与无 normal 循环、争议后新 identity、实际冲突重放逐行 gap、同 event 同 Usage 跨 request/attempt 碰撞、invoice 降低及 proposal 屏障、legacy correction 先于 normal 的资格反例、正常预算 event 原 identity EventID 绑定、账户创建/配置链/确定准入超限、NULL attempt、重复与双向孤儿、共享预算及错误零结果。

已最小补充正式可构造 confirm22→invoice15→normal无消费争议的复杂 event>1 fixture，验证逐 attempt EventOrder gap 与确定 spent_delta 矛盾优先；本片尚无专门的多次 managed/legacy 更正链、排序途中取消 fixture、真实 PostgreSQL 23 表 reader/lease 验证；根统一执行 race/vet 与全仓验证，实际结果由根集成记录补充。上述局限不能写成采购域 EOF 完成、商业验收或供应商/生产验证。

根最终冻结补充：meter 主键 first-seen 独立于 source 唯一键排除保存；主键额外行只计主键重复，不重新占用 source key 或进入关系。legacy 无同 tenant conflict 且有真实 correction 时，当前 contract/invoice 必须匹配 correction 来源，不能退回旧 cost_recorded；多个不同 correction 仍保留 CurrentCostOrder gap。有真实同 tenant conflict 且实际成本候选不同，可解释 reset 后首次 record，按 attempt 保留 CurrentCostOrder gap 并照常核当前来源。对应三行 meter、legacy 确定反例及 reset 合法对照均已回归。

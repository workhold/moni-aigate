# 022-C0：周期预算完整原始图独立核验合同（根冻结）

2026-11-03 tenant 身份校准：本合同中的 tenant 主键及真正指向它的外键按[受保护事务第6节](c0-protected-init-phase-contract.md#6-验收与交付)接受合法旧 TEXT（含空串），并保留原字节/行预算、精确关联及 NULL/空串区别；旧泛称“必需身份非空”不再涵盖这些 tenant 字段。其他对象 ID、非 tenant 引用、UUID、金额/状态与在线调用合同保持。PB 的 tenant target/key configure scope、Sandbox 按固定 kind 选择 scope 亦以该校准为准。

2026-11-02。本文承接 [全来源核验目标](c0-verifier-contract.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md) 与 [023-B 三态边界](../023-periodic-products/b-verifier-contract.md)。**2026-11-02根已审定冻结并授权本文精确纯core与独立测试；owned reader及维护组合仍由根后续冻结接线。** 目标是七域中的完整 PB 域，不是只有金额 SUM 的局部投影，也不以旧 SQL 报告替代新的 Go 独立重建。外层同租约 reader、共享 DTO、维护组合、README、架构与清单由根集成；公共 owner Verifier 和原写入路径保持原合同。

## 1. 已核对的真实事实与独立边界

已逐项核对 [020 六表/触发器](../../migrations/020_periodic_budgets.sql)、[002 客户来源](../../migrations/002_billing.sql)、[014 采购来源](../../migrations/014_procurement_accounts.sql)、[Commander 首配/变更/no-op](../../internal/periodicbudget/commander_configure.go)、[客户](../../internal/periodicbudget/tx_customer.go)、[供应商](../../internal/periodicbudget/tx_supplier.go)、[派发](../../internal/periodicbudget/tx_dispatch.go)、[原来源读取](../../internal/periodicbudget/tx_sources.go)、[现公共 verifier](../../internal/periodicbudget/verifier.go) 及其 sources/events/projections/configuration/operations SQL。

现 `ReadBoundedVerificationRows` 的 [19 表 guard](../../internal/periodicbudget/verifier_maintenance.go) 完整预检声明列、全 audit、全 schema_migrations 的 100000 行/16MiB/单字段65536字节，再执行 SQL CTE/聚合和包内 receipt 解码。它证明该兼容算法在有界快照执行，**不是原始图 Go 重放**：不能把其安全计数当新 core 的输入，不能调用其 SQL、`pb_check_*`、`pb_gate`、`pb_view_at`、`pb_current_tuple`、writer 的 Fingerprint/Validate 或金额 helper 来求 expected。

本 core 直接读六份 PB 事实、十二份业务来源，共18个原始关系；第19表 `schema_migrations` 仍由根封闭 schema/manifest guard 完整检查，既不复制迁移权威，也不把任意传入 checksum 当作 schema 证明。PB core 不自己连接数据库、发 SQL、Acquire/Finish lease、颁 proof、补建窗口或发起模型调用。

范围包括来源/应跟踪集合、allocation 起点、逐笔 effect 双向匹配、control 有序事件与 allocation/window revision、三层当前投影、原采购守恒/baseline、完整配置版本/命令/审计、历史结果静态部分及明确可证明的动态特例。保留三个真实历史缺证计数。预算 cap/历史角色授权/真实供应商消费不由守恒报告认证，见§10–11。

## 2. 固定纯 API、时间表示与 DTO

```go
func CheckPeriodicBudgetGraph(context.Context, PeriodicBudgetGraphRows) (PeriodicBudgetGraphCheck, error)

// 无 pgx 依赖；root reader 从 pgtype.Timestamptz 原生映射。
type PeriodicBudgetGraphTime struct {
    Value time.Time
    Valid bool
    Infinity int8
}
type PeriodicBudgetControlRow struct {
    ID, Scope string
    TenantID, KeyID, AccountID *string
    PeriodKind string
    Enabled bool
    LimitAmount, Revision string
    TrackingStartedAt, EffectiveFrom, MaxObservedWindowStart PeriodicBudgetGraphTime
    HeldAmount, SpentAmount, EventSeq string
    LastEventID *string
    CreatedXID, ConfigurationOperationID string
}
type PeriodicBudgetWindowRow struct {
    ID, ControlID string
    StartAt, EndAt PeriodicBudgetGraphTime
    HeldAmount, SpentAmount, Revision string
    LastEventID *string
}
type PeriodicBudgetAllocationRow struct {
    ID, ControlID, SubjectType, SubjectID, Origin, OriginSourceID string
    WindowID *string
    AdmittedAt PeriodicBudgetGraphTime
    UpperAmount, Baseline, SourceHeld, SourceSpent, HeldAmount, SpentAmount, State, Revision string
    LastEventID *string
    CreatedXID string
    CreatedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetEventRow struct {
    ID, ControlID, AllocationID, SourceKind, SourceID, Seq, AllocationBeforeRevision string
    WindowBeforeRevision *string
    BeforeHeld, BeforeSpent, AfterHeld, AfterSpent, AfterSourceSpent, AfterState, HeldDelta, SpentDelta, CreatedXID string
    CreatedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetOperationRow struct {
    OperationID, Kind, Actor, Scope, Target, ResourceID, Revision string
    SafeInput, ResultSnapshot []byte
    InputFingerprint, CreatedXID string
    CreatedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetVersionRow struct {
    ControlID, Revision, OperationID, PeriodKind string
    Enabled bool
    LimitAmount, Actor, CreatedXID string
    CreatedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetKeyRow struct { ID, TenantID string }
type PeriodicBudgetRequestRow struct {
    ID, TenantID, KeyID string
    CreatedXID, TenantControl, KeyControl *string
    AdmittedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetSettlementRow struct { ID, TenantID, RequestID, ChargedAmount, Status string }
type PeriodicBudgetAttemptRow struct {
    ID, TenantID, RequestID, CredentialID, ChannelID, DeliveryState string
    CreatedXID *string
    DispatchCheckedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetBindingRow struct { ID, AccountID, CredentialID, ChannelID string }
type PeriodicBudgetSupplierRow struct {
    AttemptID, AccountID, BindingID, UpperAmount, HeldAmount, SpentAmount, State string
    Anomaly bool
    CreatedXID, ControlID *string
    AdmittedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetProcurementEventRow struct {
    ID, EventKey, AccountID, AttemptID, HeldDelta, SpentDelta, Type string
    CreatedXID *string
}
type PeriodicBudgetAuditRow struct {
    ID, Actor string
    TenantScope *string
    Action, Target string
    SanitizedDiff []byte
    RequestID string
    CreatedAt PeriodicBudgetGraphTime
}
type PeriodicBudgetGraphRows struct {
    Controls []PeriodicBudgetControlRow
    Windows []PeriodicBudgetWindowRow
    Allocations []PeriodicBudgetAllocationRow
    Events []PeriodicBudgetEventRow
    Operations []PeriodicBudgetOperationRow
    Versions []PeriodicBudgetVersionRow
    Tenants []ProcurementAttemptTenantRow
    Keys []PeriodicBudgetKeyRow
    Users []ProcurementApprovalUserRow
    Requests []PeriodicBudgetRequestRow
    Reservations []CustomerKeyReservationRow
    Settlements []PeriodicBudgetSettlementRow
    Attempts []PeriodicBudgetAttemptRow
    Accounts []ProcurementBudgetAccountRow
    Bindings []PeriodicBudgetBindingRow
    Procurement []PeriodicBudgetSupplierRow
    ProcurementEvents []PeriodicBudgetProcurementEventRow
    Audits []PeriodicBudgetAuditRow
}
```

时间精确状态：`Valid=false,Infinity=0,Value.IsZero()` 是 SQL NULL；`Valid=true,Infinity=0` 是有限值；`Valid=true,Infinity=±1,Value.IsZero()` 分别是正/负 infinity。其他 Infinity、NULL 或 infinite 携带非零 Value 都是 `InvalidTimeRows`。**仅 `Valid && Infinity==0` 时解释 Value**；比较使用 UTC 瞬间及微秒精度，不比较 location/monotonic 内部字段。有限值接受 Go 能表示且 PostgreSQL有限 timestamptz 能产生的年份，不把源表一般时间额外限制为公共 UTC6 的1–9999年；涉及公共 receipt 文本时另受该真实线合同的1–9999年限制。required timestamp 不接受 NULL/infinity；nullable timestamp 只接受上述 NULL 或有限值。`time.Time{}` 在 Valid=true 时代表公元1年零时这个有限瞬间，不与 NULL 混同。root 仅映射 native pgtype 三态；不得以 SQL CASE/`isfinite` 先过滤坏值。

## 3. 固定物理读取合同与 typed key

root 后续 reader 在同一受信 RR/RO session 读取以下精确列；数值统一 `::pg_catalog.text`，JSONB原生 bytes、bool原生、timestamp原生。物理表只扫一次，owned静态列并集映射给多个 core；不把两个独立快照拼成一个图。

| 关系 | 固定列；顺序与 DTO 对应 |
| --- | --- |
| periodic_budget_controls | id,scope,tenant_id,key_id,account_id,period_kind,enabled,limit_amount,revision,tracking_started_at,effective_from,max_observed_window_start,held_amount,spent_amount,event_seq,last_event_id,created_xid,configuration_operation_id |
| periodic_budget_windows | id,control_id,start_at,end_at,held_amount,spent_amount,revision,last_event_id |
| periodic_budget_allocations | id,control_id,subject_type,subject_id,origin,origin_source_id,window_id,admitted_at,upper_amount,baseline,source_held,source_spent,held_amount,spent_amount,state,revision,last_event_id,created_xid,created_at |
| periodic_budget_events | id,control_id,allocation_id,source_kind,source_id,seq,allocation_before_revision,window_before_revision,before_held,before_spent,after_held,after_spent,after_source_spent,after_state,held_delta,spent_delta,created_xid,created_at |
| periodic_budget_operations | operation_id,kind,actor,scope,target,resource_id,revision,safe_input,result_snapshot,input_fingerprint,created_xid,created_at |
| periodic_budget_configuration_versions | control_id,revision,operation_id,period_kind,enabled,limit_amount,actor,created_xid,created_at |
| tenants / users | id |
| api_keys | id,tenant_id |
| request_records | id,tenant_id,key_id,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at |
| reservations | id,tenant_id,request_id,amount,status |
| settlements | id,tenant_id,request_id,charged_amount,status |
| upstream_attempts | id,tenant_id,request_id,credential_id,channel_id,delivery_state,pb_created_xid,pb_dispatch_checked_at |
| procurement_accounts | id,held_amount,spent_amount |
| credential_procurement_bindings | id,account_id,credential_id,channel_id |
| procurement_reservations | attempt_id,account_id,binding_id,upper_amount,held_amount,spent_amount,state,anomaly,pb_created_xid,pb_control,pb_admitted_at |
| procurement_budget_events | id,event_key,account_id,attempt_id,held_delta,spent_delta,type,pb_created_xid |
| audit_events | id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at（全表，不先按action过滤） |

比旧19表guard增加 attempt.delivery_state/pb_dispatch_checked_at 与 procurement.anomaly，用于§10明确可证明形状；不读取密码/MFA/邮箱、Key摘要、供应商凭据、请求/回答正文、evidence原文或当前roles/membership。schema_migrations仍是外层固定完整关系，不是从PB pure input删除迁移验收。

所有主键按各表独立键空间；versions主键是 typed `(ControlID,Revision数值)`。附加唯一键：control `(Scope,TargetID)`、window `(ControlID,StartAt瞬间)`、allocation `(ControlID,SubjectType,SubjectID)`、event `(ControlID,Seq数值)` 和 `(ControlID,SourceKind,SourceID)`、version.OperationID、reservation.RequestID、settlement.RequestID、binding.CredentialID、procurementEvent.EventKey、PB audit `(Action,Target)`。不用带分隔符的拼接字串，不用UUID/时间排序生成业务先后。

每个主键重复按额外行计数，第一次原始行保留；主键重复行不再进入自然键/关系/累计。自然键冲突同样保留首个关系行，冲突行不再放大按该关系累计，仍计原始行/资源、独立语法及其他正反向异常。输入顺序是唯一保留规则；无业务择优或后行替换。invalid数值不能以0充作typed数值键；该行保留身份但无数值索引/可信重放资格。

## 4. 值域及原始身份

全部字符串/JSON bytes计资源；字符串须合法UTF-8无NUL，不trim、不改大小写。普通旧 tenants/users/keys/requests/reservation/settlement/attempt/accounts/bindings/PE/audit 的 TEXT 身份保留任意/空文本，按原值匹配；不从“应用通常发UUID”推出旧表通用约束。SQL NULL只由nil/时间Valid表示，非nil空字符串不当NULL。

020明确限制的PB新ID（control/window/allocation/event/operation）、allocation.subject_id/origin_source_id、event.source_id、operation.resource_id，以及受PB scope同步的target，独立检查小写非零规范UUID；PB引用必须精确匹配。纯core仅对受此PB合同约束的值执行UUID检查，不对全部旧source身份统一加UUID要求。operation.actor的Go API要求UUID，现持久DB仅FK；为兼容合法历史数据库路径，本核验只检查真实user引用与指纹/审计关系，不凭Go入口校验否定旧非UUID actor。operation.scope为platform或规范UUID，target UTF-8字节≤200，fingerprint恰64位小写hex。

limit/R/U/charged为规范非负BIGINT（≤9223372036854775807）；revision/seq/前revision规范非负BIGINT，control/version/operation revision和event.seq须>0。所有held/spent/baseline为规范非负NUMERIC(100,0)；delta为规范有符号NUMERIC(100,0)。拒前导零、正号、负零、空白、小数/指数、NaN/±Infinity；**负的采购/预算delta合法**。`created_xid`是xid8原始规范无符号uint64文本；只作已冻结关系的等值，不排序、不取低32位、不用当前xmin重建历史。nullable旧stamp保留NULL。所有求和/乘减/seq+1均big.Int，不让int64/uint64 wrap影响真值；累计可超过100位，只有持久单值受typmod约束。

enum仅接受020/真实writer已声明值；supplier PE八种type及映射见§6，delivery为prepared/sending/completed/failed/unknown。非法金额/enum/时间/文本是正常数据mismatch；nil context/预算/取消是执行error。无效金额使已归属的累计不可计算，保留原始finding并跳过依赖比较，不用零值或部分和伪造确定总额。

## 5. 从真实来源生成义务与反向身份

每request必须找到tenant及同tenant Key；其两marker若非NULL必须分别指向真实tenant/key scope的唯一control；stamp与admitted_at成组，stamp为NULL时两个marker均NULL。source admitted_at非NULL须有限微秒。post020 source没有reservation必错；旧无stamp且无allocation的request缺reservation不在PB虚构金额，其账务缺失由CustomerKey core另核。每reservation反向找同tenant/request，状态reserved/pending_reconciliation时不得settlement，terminal settled/released恰有同tenant同状态settlement；每settlement反向必须找到request/reservation，charge非负、released=0。

每真实supplier source反向找到attempt→同tenant request、account、冻结binding，binding.account/credential/channel必须精确匹配source/attempt。source stamp与at成组且与attempt stamp精确相等（旧两者NULL合法），非NULL control marker必须指向同account采购control。binding本身反向account必须存在；**不根据今天新建的binding要求旧unmanaged attempt必须有采购source**。每PE反向必须有同attempt/account源。

从每个source及真实scope索引生成 `(control,subject_type,subject_id)`：customer可同时tenant/Key两份，supplier最多account一份；钱包仍只有一笔reservation。marker等于正确control，或当前来源未决（客户reserved/pending，采购prepared/sending/unknown/disputed），则必有唯一allocation，含金额0。marker非空错绑直接身份错，不能进入legacy豁免。marker=NULL且terminal、又无allocation，计§11 membership缺证；未配置scope没有预算义务，不倒灌历史消费。

每allocation反向必须找到control、真实scope/source及其完整来源关系，不能只从合法parents正向扫描。客户current期望pending→held=R/spent=0/sourceheld=R/sourcespent=0/stateheld；terminal→0/C/0/C/settlement.state。采购current期望held=p.held、sourceheld=p.held、sourcespent=p.spent、spent=max(p.spent-baseline,0)，state按p.state映射（prepared/sending/unknown→held，其他保留）。refund不进入PB图，不减少spent。

## 6. 起点与逐笔 effect 集合

每allocation首事件按control.seq，不按UUID/created_at。首before预算0/0，allocation_before_revision0，event.created_xid=allocation.created_xid；upper精确原R/U。以下全部是逐字段关系，终态来源同样保留初占，禁止用净结算代替原R/U。

| origin/subject | 首事件及冻结要求 |
| --- | --- |
| customer/new | marker=control；baseline0；origin_source=reservation.id；首reservation/该id；after R/0/source_spent0/held；allocation/request/首event的xid相等；admitted_at精确request原at |
| supplier/new | marker=control；baseline0；origin_source=canonical reserved PE.id；首procurement_event/该id；PE.type=reserved,key=reserve:attempt,delta=U/0；after U/0/source_spent0/held；allocation/source/attempt/首event/原PE xid全部非NULL相等；at=采购原at |
| customer/legacy_import | markerNULL，baseline0；origin_source=subject；首legacy_import/subject；after R/0/source_spent0/held；allocation/首event/control xid相等 |
| supplier/legacy_import | markerNULL；origin_source=attempt；首legacy_import/attempt；after_spent0，after_source_spent=baseline，after_state只held/disputed；allocation/首event/control xid相等；首held及baseline另经§7原始PE残差检查 |
| supplier/legacy_catchup | markerNULL；origin_source=首真实PE.id；首procurement_event/该PE；allocation/首event/PE xid非NULL相等；首source-before-held=0/source-before-spent=baseline，首after由该PE精确delta推导 |

new必须有同control window与有限at，原at落于[start,end)，旧窗合法；legacy窗口/at永久NULL，customer禁止catchup。source marker非NULL而origin非new、markerNULL而originnew都错。旧未决导入时source可以已经消费部分成本，不能强制legacy supplier baseline0，也不能要求其今天仍held。

构建实际与预期typed effect集合 `(ControlID,AllocationID,SourceKind,SourceID)`：每customer恰一初占，存在唯一settlement时再恰一settlement；每supplier/new必须镜像本attempt/account**全部PE含零delta**；legacy_import必须首synthetic import，legacy_catchup必须首origin PE，其他已引用PE逐笔检查，未引用PE按§7处理。required allocation缺失由MissingAllocations计数，不编造allocation UUID。每实际event反向核control/allocation/source完整归属，不允许同额错subject/account、wrong kind、terminal前先用settlement、同源双镜像。

客户settlement必须为第二且最后事件，源status与终态一致，afterheld0/afterspent=C/aftersourcespent=C；首R不能被省略，即使同事务完成/最终C0。采购PE状态映射：reserved/consumption_unknown/recovered_unknown→held；released_no_consumption/expired_without_permit→released；cost_confirmed/cost_corrected→settled；evidence_disputed→disputed。canonical reserve须精确reserve:attempt及U/0；其他key仅核全局唯一与真实引用，不解析证据字符串来发明顺序。

## 7. 独立有序重放、原采购守恒与legacy残差

先建索引/验证值域，再按(control,seq数值)一次排序。每control预期seq从1连续；每allocation/window计独立内存revision与tail。每个allocation从业务起点建立独立预期held/spent/sourceSpent/state，不从本事件before、持久当前cache或SQL报告初始化。customer起点R/0，supplier/new从U/0，legacy起点沿下述冻结checkpoint/独立残差，后续每PE使用原始delta推导。每事件比较before、after、delta、sourceSpent、state、allocation/window前revision和xid链接；`after_spent=max(expectedSourceSpent-baseline,0)`，而非把spend delta直接当原PE.delta。预算成本可降至baseline以下再回升。

supplier前态允许held→held/settled/released/disputed，settled→settled/disputed，released→released/settled/disputed，disputed→disputed/settled；customer只有held到唯一terminal。采购prepared→sending等无PE的内部状态不制造预算event义务。正常supplier original spend允许超过U，不按U截断；当前held/spent/state仍满足原采购源桶约束：prepared/sending=U/0且!anomaly；terminal held0，released spent0，其他非负。

每个真实采购source独立求全部原PE的held/spent delta总和，精确等于源当前held/spent；恰一个canonical reserve。每account同时核全部source末值之和与全部PE delta之和，两条独立路径均等于account。未知type/重复key/孤儿PE不被聚合吞没；多attempt相抵不能代替逐source检查。

对每supplier allocation，以**有效精确绑定的实际镜像PE集合**与该源全PE做差集；错绑/重复mirror先报告，不作为合法已消费。差集的独立big.Int held/spent残差：new必须差集为空（净0也不够）；legacy_import必须=(首import.afterheld,baseline)，且首sourcespent=baseline/预算spent0；legacy_catchup必须=(0,baseline)，首afterheld−originPE.held_delta=0，首aftersourcespent−originPE.spent_delta=baseline。不能用“当前全部PE除origin”的SUM代替历史before，因为其中含后续已镜像PE。legacy_import supplier的初始state仅是首immutable import冻结的held/disputed checkpoint，原PE没有历史全序，不能宣称从PE时钟独立恢复了当时state。

legacy差集非空，无论0delta/相反delta/净额恰好相等，均再记LegacyEffectBoundaryUnproven；净额错同时为确定baseline mismatch。原状态/历史集合缺证不会让所有new PE漏镜像降级成indeterminate。已损坏或排序不唯一的前缀不能用下一行before/cache“恢复可信”；继续完整行/关系/反向枚举，但将依赖该前缀的金额比较标不可计算，不把部分和报成完整。若源/顺序仍可独立计算，则元数据错误不妨碍继续按原source推导其余确定关系。

## 8. allocation / window / control 三层尾值

allocation revision=完整事件数、last_event=最大control.seq真实尾；持久held/spent/sourceheld/sourcespent/state精确等于独立尾，并与§5真实源尾值相符；无事件allocation不算已完成source，即使revision0/lastNULL。独立比较有效事件delta总和与持久预算held/spent，不让cache彼此自证。

每window必须存在control，UTC日/月边界正确，end恰下一自然日/月；held/spent同时等于全部绑定allocation末值之和、其预算event delta之和以及可计算的独立replay尾；revision=该window事件数，last_event精确末event。legacy不属于任何window，event.window_before_revision必须NULL；new按原绑定窗累计，window_before_revision从0连续。空窗允许0/0/revision0/lastNULL，因为派发extra0可以建窗；不要求每个自然期都有窗。

每control held/spent同样核全部allocation（含legacy和旧窗）、全部event及独立replay三条路径；event_seq=总事件数=连续seq尾，last指针精确，空control为0/0/seq0/NULL。max_observed_window_start有限且对齐，等于max(tracking首窗，全部真实window.start)；不要求≤机器今天，也不因有利收尾落在旧窗而判错。carry定义control.held−原时刻当前窗.held，不能为负；历史operation的carry不与今天cache相比较。

## 9. 配置版本、operation、指纹、固定audit与snapshot

control scope/nullable形状严格对应tenant/key/procurement，真实target存在且唯一；period不可变仅utc_day/month。tracking/effective有限微秒，effective独立Go UTC calendar计算为tracking所在期下一边界。每control version按revision有1..current连续唯一链，period始终相同；v1.xid=control.xid；相邻version的(enabled,limit)必须变化；最新version配置/operation指针精确control当前值。no-op没有新version。

每version反向找到唯一operation与actor；resource/revision/actor/xid/created_at精确相同。operation是否changed由version.operation_id实际引用决定，不能用今天control.pointer判断历史changed。changed safe_input.expected=revision−1（首配0），no-op expected=revision；每operation都须原revision真实version，其原period/enabled/limit精确safe_input；当前禁用、撤销角色或Key不否定历史actor。

safe_input严格四键expected_revision/period_kind/enabled/limit_amount；拒缺/多/重复键、null、wrong type、坏UTF8/孤立surrogate、非规范decimal。自行实现纯解码，不调用periodicbudget包。canonical kind/scope/target由真实control推导：tenant、procurement为platform及固定平台资源路径；Key为真实tenant及`/api/keys/<key>/periodic-budget`。重新按八字段kind/actor/scope/target/expected/period/enabled小写bool/limit的UTF8字节逐个uint32大端长度前缀frame，再SHA256小写hex；operation UUID不进入frame。结果与原input_fingerprint逐值相同。

每operation恰一action=periodic_budget.configured、target=operation.id的audit：actor/created_at精确operation，tenant_scope只Key为真实tenant否则NULL，request_id为空；sanitized_diff严格五键control_id/revision字符串/scope/scope_id/changed布尔。所有该action的audit反向必须找到operation，孤立/同target多余/同额错绑都计异常；其他action只核原始文本/time/主键资源，不拿PB规则约束其他域的JSON内容。

result_snapshot严格12键scope/scope_id/revision/period_kind/enabled/limit_amount/state/effective_from/tracking_started_at/current_window/carry_held/as_of；current_window非NULL时严格五键id/start_at/end_at/spent_amount/held_amount。配置非NULL且与operation原version一致；tracking/effective精确control不可变值，as_of是operation.created_at的公共UTC6（1..9999年、6位微秒Z）；state原enabled=false→disabled，否则as_of<effective→scheduled，否则active。revision/limit为规范BIGINT字符串，window金额/carry为NUMERIC100非负字符串，window ID受PB UUID合同。

snapshot所属UTC期不得早于tracking首期。非NULLwindow必须真实存在、同control、id/start/end精确且覆盖as_of所属期；金额不能拿今天window的值比历史。NULLwindow不能因今天后来已建窗就判错。仅当今天也没有该原as_of期window、该control每个有效预算event held_delta均0、没有使此事实不可计算的损坏，才可证明原window必须NULL且carry必须0；否则静态关系通过后记OperationSnapshotPrefixUnproven，不能寻找一个碰巧金额相等的prefix当真实cut。snapshot静态错误计SnapshotMismatches，不再对同snapshot加prefix缺证以掩盖确定错误。

## 10. 派发字段与明确不认证的历史授权

old19guard未读取dispatch stamp；新图保留该真实事实并核：delivery enum；prepared的DispatchCheckedAt必须NULL；非NULL stamp须有限微秒且对应attempt有同tenant request。post020（attempt创建stamp非NULL）的sending必须有dispatch stamp；旧attempt已sending/terminal且stampNULL合法，迁移未回填。failed/unknown/completed可以由已有证据/恢复入口直接更新，**不因未保存dispatch stamp就统一判坏**，避免把不存在的全状态转换约束附加到writer。source/attempt创建stamp等值沿§5。

**不从当前control集合、limit、binding、window或墙钟重认证历史dispatch。** 020只存一个decision time，没有保存当时观察过哪些control、每个control的config revision/event_seq/原current-window-presence或门禁前缀；绑定也可能在旧unmanaged attempt后新增。报告能证明持久派发元数据形状和PB来源守恒，不能证明每一次历史准入/派发恰好满足当时cap、当前角色/模型scope。该限制不是漏扫后使用indeterminate的借口，而是沿原023-B明确排除的授权审计能力；若要认证，应独立追加cap/dispatch witness SDD，不能把其隐藏在Balanced名称里。

## 11. 三类真实历史缺证与诚实完成路线

| Unproven字段 | 计数对象与无法区分的两段合法/损坏历史 |
| --- | --- |
| LegacyMembershipUnproven | 每(control,subject_type,subject_id)：markerNULL、当前terminal、无allocation。首配前已完成可合法略过；首配时未决却整份漏导入后再完成也产生相同当前事实；020没有首配membership/cut见证 |
| LegacyEffectBoundaryUnproven | 每legacy supplier allocation：至少一原PE未合法镜像。冻结残差只约束净额；零delta/相反delta可以在跟踪后遗漏又保持净额，xid/created_at/UUID不能证明跟踪前后 |
| OperationSnapshotPrefixUnproven | 每静态正确但不满足§9充分特例的operation：无event_seq/window存在性cut。先建空窗后读与先读后建空窗，或非零held经过后再归零，都可在相同当前图中产生不同合法原动态结果 |

本候选采用已获023-B批准的020-only诚实三态，可完成“全PB可证关系独立扫描”，不能称“已把旧环境所有历史补证”。`CoverageComplete`仅表示上述三种缺证为0，不表示外部历史真实性/授权审计/全七域完成。任何确定mismatch优先，仍保留全部Unproven计数。未实施reader/缺表/未完成scan不是历史缺证，必须error阻断未来完整phase。

如根要求所有合法旧环境都有strict complete，真实选项只能是另行冻结追加见证：scope首配屏障下的不可变全源成员/cut；原PE在account/attempt串行域的control-at-effect及稳定序列/before-after；operation观察到的event_seq及window存在性/id/revision。旧历史仍保留原缺证，另建明确“证明始于该维护快照”的attestation，不能把今天快照倒填成当时证据。既有020不可修改；本片不抢迁移号、不新增writer/权限、不自动用人工attestation消除Unproven。

## 12. 固定摘要、计数与执行错误

```go
type PeriodicBudgetGraphCheck struct {
    Scope string // periodic_budget_graph_v1
    Scanned PeriodicBudgetGraphCounts
    Status string // balanced | mismatch | indeterminate
    Balanced, CoverageComplete bool
    Mismatches PeriodicBudgetGraphMismatches
    Unproven PeriodicBudgetGraphGaps
}
```

Counts为§2十八组同名uint64，按传入原始行数计（Audits全表）。Gaps精确§11三个uint64。摘要无ID/金额/raw JSON/xid/SQL/指纹/时间/外部错误。所有uint64对外JSON沿同包安全计数习惯输出数字；本片不改变公共owner CLI既有计数字符串协议。

Mismatches固定uint64字段：`DuplicatePrimaryRows,DuplicateControlScopeRows,DuplicateWindowStartRows,DuplicateAllocationSubjectRows,DuplicateEventSequenceRows,DuplicateEffectRows,DuplicateVersionOperationRows,DuplicateReservationRequestRows,DuplicateSettlementRequestRows,DuplicateBindingCredentialRows,DuplicateProcurementEventKeyRows,DuplicateAuditTargetRows,InvalidTextRows,InvalidUUIDRows,InvalidAmountRows,InvalidRevisionRows,InvalidXIDRows,InvalidTimeRows,InvalidEnumRows,SourceIdentityMismatches,MissingAllocations,OrphanAllocations,AllocationOriginMismatches,AllocationSourceMismatches,AllocationProjectionMismatches,MissingEffects,OrphanEffects,EffectBindingMismatches,EventChainMismatches,WindowMismatches,ControlMismatches,ProcurementSourceMismatches,LegacyBaselineMismatches,ConfigurationMismatches,VersionMismatches,OperationMismatches,AuditMismatches,SnapshotMismatches,DispatchMetadataMismatches`。

DuplicatePrimaryRows跨18关系按额外行累计，表键空间独立；附加唯一键各按额外行；语法字段按(关系,首保留主键)至多一次。语义19字段沿§5–9命名来源：SourceIdentity按typed来源行；MissingAllocation按应有typed subject；OrphanAllocation/Origin/Source/Projection/Baseline按allocation；MissingEffect按预期typed effect，Orphan/Binding/Chain按event；Window/Control/Configuration按各自根；ProcurementSource按typed(source或account)；Version按typed(control,revision)；Operation/Snapshot按operation；Audit按异常实际audit或缺audit operation；Dispatch按attempt。跨字段不互斥，同字段同对象去重。空/非UUID普通身份仍可作为真实finding键，不用空串表示“没有对象”。

任何mismatch→Status=mismatch；否则任一gap→indeterminate；否则balanced。Balanced仅Status=balanced；CoverageComplete仅三gap均0，坏数据可以同时mismatch和CoverageComplete=true，不能把该bit单独当通过。合法空图可局部balanced，不证明真实数据库空或全源读取完成。

错误精确 `ErrPeriodicBudgetGraphRowsInput/Resource/Canceled/Timeout`：nil context→Input；总资源超限→Resource；取消/到期各对应；任何error返回零Check。SQL/Next/Scan/Err/Close、schema、lease、EOF/Finish错误由外层固定安全Execution等类别整体零facts；不能拿部分core结果组完整report。

## 13. 资源、复杂度与同session完成

十八组共享100000原始行、16MiB总投影buffer，单字符串或JSON raw≤65536字节；NULL文本计0，非NULL计原UTF8字节，JSON计原bytes，native bool计1，每个时间DTO固定计32字节（NULL/infinite也计），防止忽略native事实。行/字节检查在所有建索引/解码前完成，safe_input/result_snapshot自身业务尺寸/结构另报mismatch，不通过reader限额过滤内容。不能每表、每阶段或每core重置同session预算；同物理表复用不重复源预算，pure单独输入仍执行自身18组总界。

内存O(N+E)，索引一次构建；control事件和版本排序O(E log E+V log V)，所有来源/差集/rollup通过typed map单次累计，禁止每operation重扫整表或逐source向DB发N+1。资源界不是Go RSS/数据库排序内存承诺。每至多256步及函数退出前检查ctx，排序比较亦有周期检查或分块取消点，不能在巨大排序后继续构造已取消成功摘要。

根后续封闭reader需18业务表全部完整EOF/Err/Close，单表C序128行keyset、显式hasCursor；versions用typed(control_id C序,revision数值)复合页，其他主键按表id/operation_id/attempt_id。不能把`control||revision`字符串游标当复合键；audit全表，PE全表，来源全状态，无JOIN/CTE/聚合/业务过滤。numeric文本、native时间三态原样映射；空TEXT首ID仍正常，最后满页继续到真正EOF。root共享union若包含更多域，单次真实物理行/字节共用受冻profile，不借多个局部预算扩大总额。

公共owner Verifier原19表查询、权限、错误、CLI三态不改。新owned reader及maintenance只在root后续冻结接线后使用PB纯图；本片无完成位/Proof/Runtime/basis/epoch出口，不让任意调用方提供“已经完成PB”的bool。CPU core通过、旧19表guard通过或fixture通过都不代替该新reader/全七域/真实PG/生产供应商验收。

## 14. 冻结后验收矩阵与交付边界

纯测试独立手算，不调用writer或old SQL作expected。至少覆盖：空/无control旧数据；tenant+Key双scope；new customer pending/零R/部分或零settled/零released/跨窗；同事务初占+finish不能丢首；supplier new八种PE状态/负delta/U以上成本；legacy import部分spent baseline、legacy disputed、terminal后catchup、baseline以下降本后回升；旧terminal无allocation缺证；未镜像零delta/相反delta仍缺证；精确残差不符；control/window/allocation三层尾与高水位/空窗；version连续/首配/no-op/相邻真实变更/CAS/frame独立golden；严格JSON缺/多/重复/null/Unicode/type；audit正反孤儿/重复/错scope；snapshot静态错/动态缺证/充分零held特例；native NULL/finite/±infinity/非法组合、UTC月/闰年/微秒、旧clock不要求≤now；dispatch旧sendingNULL合法、新sending缺stamp坏、terminal直接证据无stamp合法；所有主键/typed唯一键冲突、普通空非UUID身份、不把NULL当空；NUMERIC100/BIGINT/xid8边界/累计越界；跨首末表行/byte/field总界与中途/末尾取消/timeout零结果。

根冻结后只授权本代理独占`internal/fundinglotverify/periodic_budget_graph*.go`与测试；先专项测试，再独立审阅全部结果，由根统一race/vet并接同session/真实PG/架构与最后push。不改原周期reader/writer/公共verifier/迁移、共享DTO、README或架构，不自行refresh。本文设计变更纳入根最后统一架构复核；实现与限定证据见下节。

## 15. 纯 core 实施与限定验证

已实现六个`periodic_budget_graph*.go`纯实现文件和两份独立测试：18组原生DTO/资源/typed首行索引、来源双向义务、origin/原始effect/legacy残差、独立source驱动重放及三层投影、全部版本/命令/独立frame/严格JSON/audit/snapshot。没有导入periodicbudget写模块或数据库依赖，不执行SQL。确定损坏不再恢复为可信前缀，后续正反向行枚举继续；普通空TEXT身份、NULL时间、有限的公元1年零时、xid8 uint64与BIGINT/NUMERIC100分别处理。

2026-11-02定向非race `go test ./internal/fundinglotverify -run '^TestCheckPeriodicBudgetGraph' -count=1` 通过，包0.636s；`gofmt`与`git diff --check`通过。九个顶层测试包括18组全部主键去重、11类typed唯一关系、19语义方向反例、tenant/Key双scope、客户零/部分/terminal、八种采购PE/负delta/大于U的NUMERIC100成本、legacy disputed导入baseline50及20→70更正、terminal catchup、零/相反delta缺证、普通非UUID/空TEXT、NULL/finite/±infinity/非法时间形状/UTC偏移/闰月、公元1年零时、xid8上界、严格JSON和receipt/audit、共享100000行/16MiB/单字段65536及跨组native时间预算精确边界、取消/timeout与最终退出前零结果。此前一次fixture构造对采购scope先解引用NULL tenant发生panic，仅修复测试fixture分支；随后两轮完整定向测试均通过。

根为避免共享包编译干扰实际PG固定预算，明确接管最终全包race/vet；本片不自行重复。尚未把新PB core接入owned reader/session/维护phase，没有运行或宣称该新组合的真实PG、七域、basis/epoch、商业或生产供应商验收。公共owner verifier原SQL/三态仍保持兼容；根后续统一架构和最终GitHub推送分别记录。

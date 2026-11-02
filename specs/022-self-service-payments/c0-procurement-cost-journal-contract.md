# 022-C0-T3 采购成本审计与 outbox 局部核验

2026-11-03 tenant 身份校准：本合同中的 tenant 主键及真正指向它的外键按[受保护事务第6节](c0-protected-init-phase-contract.md#6-验收与交付)接受合法旧 TEXT（含空串），并保留原字节/行预算、精确关联及 NULL/空串区别；旧泛称“必需身份非空”不再涵盖这些 tenant 字段。其他对象 ID、非 tenant 引用、UUID、金额/状态与在线调用合同保持。PB 的 tenant target/key configure scope、Sandbox 按固定 kind 选择 scope 亦以该校准为准。

2026-11-02，根已授权冻结实施的纯core增量。沿 [成本双审图](c0-procurement-approval-contract.md) 的原始事实，独立核对三种成本审计与 `cost_corrected` outbox；新增固定scope `procurement_cost_journal_graph_v1`。本片的 journal 是审计/投影消息关系，不是资金journal或供应商账单。没有新迁移、writer、SQL聚合、HTTP或完整采购coverage；root在同一次十九表扩展扫描中完整读audit/outbox并共享给其它局部core，读取/维护装配由root独占。

## 1. 固定纯API与输入

```go
func CheckProcurementCostJournalGraph(context.Context, ProcurementCostJournalRows) (ProcurementCostJournalCheck, error)
type ProcurementCostJournalRows struct {
    Users []ProcurementApprovalUserRow
    Attempts []ProcurementAttemptRow
    Reservations []ProcurementAttemptReservationRow
    Corrections []ProcurementCostCorrectionRow
    Proposals []ProcurementCostProposalRow
    Audits []ProcurementAuditRow
    Outbox []ProcurementOutboxRow
}
// ProcurementAuditRow由并行command核验片唯一声明，两core复用同一类型。
// ID,Actor,Action,Target,Diff,RequestID string; TenantScope *string; CreatedAt time.Time
type ProcurementOutboxRow struct {
    ID,TenantID,OperationID,Kind,Payload string
    SchemaVersion int32
    CreatedAt time.Time
}
```

root固定outbox投影顺序：`event_id,tenant_id,operation_id,kind,schema_version,sanitized_payload::pg_catalog.text,created_at`，按event_id显式C序、128行keyset完整读 `projection_outbox`，不以kind筛选丢掉反向事实。audit固定共享投影为 `id,actor,tenant_scope,action,target,sanitized_diff::pg_catalog.text,request_id,created_at`。原schema/writer见 [001 audit](../../migrations/001_core.sql:94)、[002 outbox](../../migrations/002_billing.sql:167)、[提案](../../internal/billing/procurement.go:423)、[批准](../../internal/billing/procurement.go:509)、[legacy更正](../../internal/billing/recovery.go:289)、[Money序列化](../../internal/billing/types.go:15) 和 [audit/emit](../../internal/billing/store.go:140)。

Check包含Scope、Scanned、Balanced和Mismatches。Scanned固定 `Users,Attempts,Reservations,Corrections,Proposals,Audits,Outbox uint64`，统计全部原始行。Mismatches固定字段见§3；JSON计数沿既有采购局部摘要uint64。没有行ID、JSON正文、凭据或金额回显。

## 2. 独立双向关系

managed仅按原 `procurement_reservations` 是否存在于该attempt识别。**不得用operation前缀分类**；无reservation的合法legacy更正，即使operation恰为 `procurement:<UUID>`，仍要求legacy审计且沿原operation匹配outbox。

| 固定action/kind | 必须匹配的源与精确值 |
| --- | --- |
| audit `procurement.cost_proposed` | target=proposal.id；tenant_scope=proposal.tenant；actor=requester；request_id=原attempt.request；diff恰四个string字段 `attempt_id,amount,basis,evidence_ref` |
| audit `procurement.cost_approved` | 只允许形状成立的approved proposal；target=proposal.id；tenant_scope=proposal.tenant；actor=approver；request_id=原attempt.request；diff恰三个string字段 `attempt_id,amount,evidence_ref`，**没有basis** |
| audit `cost.corrected` | 只允许无reservation的legacy correction；target=correction.id；tenant_scope=correction.tenant；request_id=原attempt.request；diff恰两个string字段 `amount,basis`；actor必须非空合法文本 |
| outbox `cost_corrected` | 对每个managed或legacy correction恰一条；operation_id=correction.operation_id；tenant_id=correction.tenant；schema_version=1；payload恰三个string字段 `attempt_id,amount,basis` |

每个proposal无论当前proposed/approved都必须保留唯一proposed audit；approved另有唯一approved audit。每个legacy correction保留唯一legacy audit；managed correction沿 `procurement:`+approval_operation_id 找到形状成立、tenant/attempt/amount/basis/evidence一致的approved proposal，而不是另造 `cost.corrected` audit。每个correction有唯一outbox。反方向每条上述相关audit/outbox必须找到正确源并逐字段一致；孤儿、错tenant/actor/request/target/payload均计异常。不检查它们的created_at大小或与源恰等时，不用当前用户角色/禁用状态解释历史。

legacy source没有持久化ActorID；writer将空ActorID变为`system`，其它传入actor原样记审计。所以该分支只能核非空UTF8/NUL安全文本，**不能独立证明actor究竟是谁、有当前users行或当时持何种财务角色**。不得因此要求legacy actor必须出现在users，也不能将非system来源强行改写。managed requester/approver必须在Users中，身份当前状态不参与历史核验。

源形状：proposal/correction金额为规范非负int64十进制字符串；proposal expected_revision正、expected_evidence_count非负；basis仅contract/invoice，proposal state仅proposed/approved；nullable/双人/operation!=proposal形状沿既有approval合同。proposal必须同tenant attempt且有reservation和requester/批准人；correction必须同tenant attempt，managed还须上述proposal匹配。重用DTO中的其它值不被当作当前采购金额oracle，本片不重算预算event、证据digest或最后成本。

JSON按语义对象匹配，不依赖JSONB键顺序/空白。所有规定字段值都必须是string，Money如`"25"`，不能是JSON number `25`、null、bool、数组或对象。amount须规范非负int64且与源相同；其它值与原source精确相同。拒未知/缺失/重复（包括转义后重复）键、尾JSON、非法UTF8、孤立surrogate。合法转义和JSONB空白可接受，不把原JSON字节当指纹。与本片无关的audit action或outbox kind只计资源/原始行数和主键/文本编码，不解析其payload或检查其schema_version、tenant、actor、授权或源；平台audit的NULL/空tenant_scope及空request_id均允许，不误报其它模块。

## 3. 重复、计数、资源和错误

Mismatches为固定uint64字段：`DuplicateUserRows,DuplicateAttemptRows,DuplicateReservationRows,DuplicateCorrectionRows,DuplicateCorrectionOperationRows,DuplicateProposalRows,DuplicateApprovalOperationRows,DuplicateAuditRows,DuplicateAuditEffectRows,DuplicateOutboxRows,DuplicateOutboxOperationKindRows,InvalidTextRows,InvalidAmountRows,InvalidEnumRows,InvalidJSONRows,ProposalIdentityRows,ProposalShapeRows,CorrectionIdentityRows,ManagedCorrectionProposalRows,ProposedAuditRows,ApprovedAuditRows,LegacyAuditRows,CorrectionOutboxRows,AuditSourceRows,OutboxSourceRows`。

原始主键/唯一operation首次有效行保留，多余行只计各duplicate、不替换首行或放大关系扫描；audit相关 `(action,target)` 重复、outbox全量 `(operation_id,kind)` 重复各有独立计数。outbox无关kind允许空operation，但该合法空值也必须参与完整typed pair去重，不能因空值跳过唯一性核验。非法身份不入索引。`InvalidTextRows`每源行至多一次，复用既有全部source文本检查，并包括所有audit/outbox投影文本；无关audit的actor/target/tenant_scope/request_id、无关outbox的tenant/operation可以空，但字符串必须合法UTF8且无NUL，ID/action/kind必需。`InvalidJSONRows`只计相关audit/outbox的非法语法、键/类型/amount规范，不把字段值与源不同计为JSON语法错误。`Proposed/Approved/LegacyAuditRows`和`CorrectionOutboxRows`按应有源计缺失或不匹配；`AuditSourceRows/OutboxSourceRows`按相关记录计孤立或不匹配，同一坏边可以正反分别计一次。

七组输入共用100000行、16MiB全部原始投影文本上限，不按group重置；包括nullable非NULL字段及JSON文本，重用attempt/reservation完整文本亦计入。工作循环每256步检查父context，结束再检查一次；所有金额解析使用精确整数，不用float。纯函数不Begin/Commit、不读写DB、不联网、不取业务锁、不授proof；root完整扫描仍须共用原ReadLease总期限、行/字节预算且全部EOF/Err/Close后才调用core/Finish。

固定sentinel：`ErrProcurementCostJournalRowsInput/Resource/Canceled/Timeout`；nil context为Input，行/字节超限Resource，父取消/到期分别Canceled/Timeout，全部错误返回零Check，不返回局部成功。数据不匹配正常返回固定Scope/完整Scanned，Balanced当且仅当全部mismatch为零。不泄露原文本或私有cause。

## 4. 验证和保留边界

core覆盖空集、proposed/approved、managed/legacy混合与同前缀legacy、多次历史更正、actor=system或无当前user的legacy、无关模块动作/消息；正反孤儿、错actor/request/tenant/payload和缺/重复审计/消息；Money字符串与非法类型/额外键/重复转义键/非法JSON；主键/operation去重、严格源形状、资源共享、取消/超时和错误零结果。定向race/vet通过后记录命令，不以纯core测试代替reader/真实PG/维护或商业验收。

没有覆盖供应商账单、incoming evidence缺证、完整事件顺序、成本记录cost_recorded消息、projection消费/receipt或report、tenant/customer资金journal、commands/observations的其它审计、全来源组合/basis/epoch。已存在的其它局部摘要不被本片替代。root汇合全部结果后更新架构/README/HLD/LLD及验证记录；本并行片只交付合同、core/tests与[evidence研究](../../docs/research/procurement-evidence-lifecycle.md)，不单独refresh。

## 5. 纯core实施与专项记录

实现 [procurement_cost_journal.go](../../internal/fundinglotverify/procurement_cost_journal.go) 与 [专项测试](../../internal/fundinglotverify/procurement_cost_journal_test.go)。共享Audit DTO仅从command片声明复用；Outbox DTO由本片唯一声明。core只依赖标准库和同包既有纯DTO/规范整数/文本核验，不调用billing writer或数据库。上述evidence研究只交付只读调研，没有实现evidence扫描或完整digest证明。

2026-11-02定向 `go test -race ./internal/fundinglotverify -run '^TestCheckProcurementCostJournalGraph' -count=1` 通过，包1.555s；`go vet ./internal/fundinglotverify` 和 `git diff --check` 通过。五个单元顶层测试覆盖§4列举的正常/反例，包括两份同attempt的历史已批准更正、同前缀legacy及无当前users的legacyactor、精确Money与严格JSON、首行去重、原始audit/outbox共享字节边界和父取消零结果。此前同片race1.727s早于最后多次历史更正用例；以上均为下述独立review修订前的专项记录。

独立review发现无关outbox允许空operation时，旧effect判据误将该合法值排除，漏报两个不同ID的相同 `("", "other")` pair。已窄修为operation只要求UTF8且无NUL、kind仍非空，并新增两条无关kind/空operation的回归，确认仅 `DuplicateOutboxOperationKindRows=1` 且Balanced=false。修订后**全包** `go test -race ./internal/fundinglotverify -count=1` 通过，包1.806s；`go vet ./internal/fundinglotverify`、`git diff --check` 通过。未新增payload解释、writer或生产能力。

本片未运行或声称真实PG/reader/Permit/维护Scan/生产验收；这些按root十九表/五摘要装配及对应专项记录分别收口。没有刷新架构快照，根汇合后统一执行。

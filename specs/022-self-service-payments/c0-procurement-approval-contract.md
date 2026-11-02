# 022-C0-T3：采购成本双审关系核验合同

2026-11-02，根冻结增量实施片。在[attempt合同](c0-procurement-attempt-contract.md)的同一个 `ScanProcurement` 内增加成本提案、成本更正和预算更正事件的双向关系核验。保留原预算与绑定图摘要，新增独立 `procurement_cost_approval_graph_v1`；不授予采购全域或全来源 coverage。

## 1. 固定读取与纯入口

原十二表保持不变，仅 `procurement_budget_events` 的末尾增加 `type,evidence_ref`。追加三张无业务过滤的完整表，仍每页128行、显式C序单列keyset、短页EOF，十五表共用100000行和16MiB所有投影文本预算及原ReadLease期限：

| 表 | 排序键 | 固定列顺序 |
| --- | --- | --- |
| `users` | `id` | `id`，不读取密码、MFA、邮件或当前角色 |
| `provider_cost_corrections` | `id` | `id,tenant_id,attempt_id,operation_id,amount::pg_catalog.text,basis,evidence_ref,created_at` |
| `procurement_cost_proposals` | `id` | `id,tenant_id,attempt_id,amount::pg_catalog.text,basis,evidence_ref,reason,requester,expected_reservation_revision::pg_catalog.text,expected_evidence_count::pg_catalog.text,state,approver,approval_operation_id,created_at,approved_at` |

纯入口 `fundinglotverify.CheckProcurementCostApprovalGraph(ctx, rows)`，固定DTO：

- `ProcurementApprovalUserRow{ID string}`。
- `ProcurementCostCorrectionRow{ID,TenantID,AttemptID,OperationID,Amount,Basis,EvidenceRef string; CreatedAt time.Time}`。
- `ProcurementCostProposalRow{ID,TenantID,AttemptID,Amount,Basis,EvidenceRef,Reason,Requester,ExpectedRevision,ExpectedEvidenceCount,State string; Approver,ApprovalOperationID *string; CreatedAt time.Time; ApprovedAt *time.Time}`。
- `ProcurementApprovalEventRow{ID,EventKey,AccountID,AttemptID,Type,EvidenceRef string}`；同一次数据库event行同时映射原预算DTO和本DTO，不再扫描第二次。
- `ProcurementCostApprovalRows{Users []ProcurementApprovalUserRow; Attempts []ProcurementAttemptRow; Reservations []ProcurementAttemptReservationRow; Events []ProcurementApprovalEventRow; Corrections []ProcurementCostCorrectionRow; Proposals []ProcurementCostProposalRow}`，attempt/reservation复用同一次读取的原始行，不接受SQL聚合、JOIN或写侧函数结果。

摘要 `ProcurementCostApprovalCheck{Scope string; Scanned ProcurementCostApprovalCounts; Balanced bool; Mismatches ProcurementCostApprovalMismatches}`；Scanned固定为上述六组的 `Users,Attempts,Reservations,Events,Corrections,Proposals uint64`。所有金额使用规范十进制 `math/big.Int`，不使用浮点。

## 2. 独立核验规则与计数

`Mismatches` 固定uint64字段：`DuplicateUserRows,DuplicateAttemptRows,DuplicateReservationRows,DuplicateEventRows,DuplicateEventKeyRows,DuplicateCorrectionRows,DuplicateCorrectionOperationRows,DuplicateProposalRows,DuplicateApprovalOperationRows,InvalidTextRows,InvalidAmountRows,InvalidEnumRows,ProposalShapeRows,ProposalIdentityRows,CorrectionIdentityRows,ApprovedCorrectionRows,ApprovedEventRows,ManagedCorrectionApprovalRows,CorrectionEventApprovalRows`。

所有原始文本计入资源预算；必需文本非空、合法UTF8且无NUL。event.evidence_ref允许空；nullable提案字段NULL不算非法文本，有值必须非空。`InvalidTextRows`每行至多一次。提案ID及非NULL approval_operation_id须为小写规范UUID；evidence_ref长1..256字节、提案reason长1..400字节且无控制字符，否则同计该行InvalidTextRows。普通更正ID/operation保持既有文本身份，不凭前缀推断来源；普通更正evidence_ref要求非空，但不追溯增加提案字段长度限制。

`InvalidAmountRows`每源行最多一次：correction/proposal.amount为非负BIGINT；proposal.expected_revision为正BIGINT，expected_evidence_count为非负BIGINT；拒绝前导零、正号、负零、指数和越界值。只检查本片新增金额字段，attempt/reservation金额形状由原摘要承担。`InvalidEnumRows`每correction/proposal行最多一次，basis只接受contract/invoice，proposal.state只接受proposed/approved。其它预算event.type只做必需文本检查，不把未在本片解释的历史event类型当成本更正。

重复身份按各关系第一次出现行建索引，多余行只计相应duplicate，不放大关系核验；operation/event_key等唯一键也以第一次保留。非法身份不参与索引。一个源行可在不同mismatch字段各计一次，同一字段最多一次。

- proposal必须引用存在的同tenant attempt及其采购reservation，requester必须在users中；approved还须approver存在。缺失或跨tenant计 `ProposalIdentityRows`。不以用户当前角色/禁用状态反推历史审批资格。
- proposed必须三个approval字段均NULL；approved必须三个均非NULL，approver不同于requester，approval_operation_id不同于proposal.id；不合法计 `ProposalShapeRows`。不要求当前reservation revision/evidence count等于提案快照，因为提案可合法过时，已批准后也可继续更正；不以进程时钟推断历史时间顺序。
- 每个correction必须引用存在的同tenant attempt，缺失/错tenant计 `CorrectionIdentityRows`。无采购reservation的legacy更正无需proposal，operation即使恰有`procurement:`前缀也不能被当成采购来源。
- 每个形状成立的approved proposal必须有唯一correction，其operation精确为`procurement:`+approval_operation_id，且tenant/attempt/amount/basis/evidence_ref精确匹配；缺少或不匹配计 `ApprovedCorrectionRows`。金额比较基于规范精确整数。
- 同proposal必须有唯一event，event_key精确为`correction:`+approval_operation_id，type为`cost_corrected`，attempt为原attempt，account为其reservation.account，evidence_ref为proposal.evidence_ref；否则计 `ApprovedEventRows`。本片不根据delta反推绝对amount或重放完整状态机。
- 每条存在采购reservation的correction必须反向匹配唯一形状成立approved proposal的同一operation及精确字段，否则计 `ManagedCorrectionApprovalRows`。
- 每条type=`cost_corrected`的预算event必须反向匹配唯一形状成立approved proposal及上述精确event字段，否则计 `CorrectionEventApprovalRows`。

不要求最终attempt.cost_amount或reservation.spent等于任意一个历史correction；后续更正或新证据可以改变当前事实。proposal和event可以分别通过正向检查但不能据此证明审计/outbox完整或真实供应商扣款。

## 3. 错误、维护与边界

纯错误固定为 `ErrProcurementCostApprovalRowsInput/Resource/Canceled/Timeout`；nil context为Input，超过100000行/16MiB为Resource，取消/deadline为对应错误，任何错误返回零摘要。数据mismatch是成功核验，Balanced当且仅当全部mismatch为零。文本与金额不回显原值，摘要只含固定scope及计数。

沿用 `ReadProcurementAttemptBindingFacts`，增加 `ApprovalSummary()`；维护 `ProcurementMaintenanceResult` 增加 `ApprovalSummary(permit)`。三个摘要要求同一次ScanProcurement、同原Permit proof；只Finish一次。任一数据不平衡正常Finish，任一执行/SQL/EOF/Err/Close/资源/取消/Finish错误撤销整lease并返回零结果，不能拼接独立Finish的局部proof。

未覆盖：evidence identities及digest重算、metering/conflicts/finalization、commands/audit/outbox、人工余额观察、周期预算交叉图、完整采购事件重放、全部来源组合/basis/epoch和生产维护入口。此片不修改在线资金写入或数据库迁移，不注册HTTP/worker，不新增维护ScanKind。

## 4. 验收

纯core覆盖空集、合法proposed/approved、managed多次历史更正、legacy（含相同前缀）更正、双向孤儿/错tenant/错账户/错字段、双人/nullable形状、重复主键/唯一键、规范金额/文本/枚举、共享资源边界和取消零结果。reader覆盖十五表固定投影、127/128/129分页、新三表全部Query/Next/Scan/Err/Close失败、共享budget和完整数据mismatch仍成功；维护拒绝伪造/错scope/错Permit和零结果泄漏。

外部PG Plan/Init QA使用真实采购更正正例及受控损坏负例，并对扫描前后事实做只读比较。缺 `C0_MAINTENANCE_DATABASE_URL` 必须明确SKIP；缺 `TEST_DATABASE_URL` 不能宣称商业整仓数据库验收。最终同步统一架构、README、HLD/LLD、验证记录并运行定向race/vet与make check。

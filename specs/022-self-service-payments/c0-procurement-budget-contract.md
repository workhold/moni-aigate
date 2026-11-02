# 022-C0-T3：采购预算投影维护扫描合同

2026-11-02，根冻结首个 `ScanProcurement` 实施片。此合同只授权对同一 T2 Snapshot 中采购账户预算投影做完整局部只读扫描；不授权写入、basis、epoch、全来源证明或生产维护入口。

本文保留首个三表reader的原冻结范围与历史验收口径。随后由[采购 attempt 关系合同](c0-procurement-attempt-contract.md)扩展同一个 `ScanProcurement` 实现：当前入口在一次lease内读取十二张固定关系表，返回预算投影与attempt绑定两个独立摘要，并只 `Finish()` 一次。本文后续对三表范围、Go adapter名称及独立Finish的描述属于首片历史，不是当前维护入口规格；新变更以扩展合同为准。

## 1. 目标与封闭范围

维护 reader 必须完整扫描并在内存中独立比较三个固定关系：

| 关系 | 固定排序键 | 固定投影 |
| --- | --- | --- |
| `procurement_accounts` | `id COLLATE "C"` | `id, held_amount::text, spent_amount::text` |
| `procurement_reservations` | `attempt_id COLLATE "C"` | `attempt_id, account_id, upper_amount::text, held_amount::text, spent_amount::text, state, anomaly` |
| `procurement_budget_events` | `id COLLATE "C"` | `id, event_key, account_id, attempt_id, held_delta::text, spent_delta::text` |

每张表从首行开始以128行为上限做 keyset 页面，显式C序比较游标；短页才表示EOF。全reader合计不超过100000行、16 MiB固定投影中所有文本返回值的UTF-8字节（包括`::text`转换的数值）、原ReadLease至多30秒/其更早deadline。必须每页检查Next/Scan/Err/Close、完整消费行并检查取消；没有过滤、估算、SQL聚合或数据库trigger作为核验oracle。

结果检查每个账户的 `held_amount` / `spent_amount` 分别等于对应 reservations 的精确和，也分别等于 budget events delta 的精确和；使用 `math/big.Int` 和最多100位的规范整数语法。reservation 与事件必须引用扫描到的账户；事件 `(account_id,attempt_id)` 必须引用扫描到的同账户 reservation。检查金额形状、重复输入身份及以下状态桶不变量：prepared/sending 必须 `held=upper, spent=0, anomaly=false`；settled 必须 `held=0`；released 必须 `held=0, spent=0`；状态必须属于迁移枚举。unknown/disputed 的桶值只受非负NUMERIC形状和投影总额约束，不推断其真实供应商消费。

完整局部扫描成功后返回 `scope=procurement_budget_projection_v1`、三表扫描计数、`Balanced` 和固定方向的不匹配计数。每个 `InvalidTextRows` / `InvalidAmountRows` 对该表原始行最多计一次，即使同一行有多个坏字段也只计一；`OrphanEvents` 对缺少账户或`(account_id,attempt_id)`不匹配的事件行计一次。重复计数按每个唯一键第一次出现后多出的行数计；孤儿reservation按缺少所属账户的reservation行计。账户聚合不比较含非法金额的桶，非法行计数本身已令结果不平衡。`Balanced` 当且仅当所有`Mismatches`字段均为0。它只表示这三张表内部的预算投影相互一致，不表示采购成本或真实供应商账单正确。摘要不包含账户、attempt、credential、供应商、事件键或证据原文。

失败分类保持零结果：输入/Schema、资源上限、取消/超时、SQL/Scan/Rows错误、未完整到EOF或Close失败均不返回局部计数。数据损坏属于成功扫描后的不平衡计数，不转换为SQL错误。

## 2. 不覆盖的事实

本片不读取或证明 `credential_procurement_bindings`、`upstream_attempts` 的凭据/渠道/成本报价、`provider_cost_corrections`、成本proposal与双审、evidence identities、commands/audit、余额观察或 `periodic_budget_*`。它不验证`upper_amount`是否由attempt报价重新计算，不证明供应商已扣费/未扣费、不提供预算可用性授权，也不核全采购状态机。以上仍待独立reader与全源T3组合合同。`ScanProcurement` 结果不得用作全来源覆盖、funding lot、epoch/init准入或修改客户钱包/采购余额的许可。

## 3. Go 边界

纯核验位于 `internal/fundinglotverify`，接收封闭的 `ProcurementBudgetRows`，不导入数据库/维护authority、不联网、不输出原始事实。`internal/billing.ReadProcurementBudgetFacts` 仅使用传入query和schema执行固定扫描，不自行Begin/Commit/切换角色。`internal/maintenancescan.VerifyProcurementBudgetMaintenance(lease)` 是唯一authority适配：要求非零真实`ReadLease`且kind精确为`ScanProcurement`，持同一lease扫描完三表后恰好Finish一次，结果私有持有`ScanProof`；只有`Summary(permit)`可在proof验证后返回本地摘要。JSON、零值、复制或外部构造不能铸造proof。

Go DTO 名称固定为 `ProcurementBudgetAccountRow{ID,HeldAmount,SpentAmount string}`、`ProcurementBudgetReservationRow{AttemptID,AccountID,UpperAmount,HeldAmount,SpentAmount,State string; Anomaly bool}`、`ProcurementBudgetEventRow{ID,EventKey,AccountID,AttemptID,HeldDelta,SpentDelta string}`、`ProcurementBudgetRows{Accounts,Reservations,Events []...}`。纯入口为 `CheckProcurementBudgetProjection(ctx, rows) (ProcurementBudgetProjectionCheck,error)`；摘要固定为 `Scope string`、`Scanned{Accounts,Reservations,Events uint64}`、`Balanced bool`，不匹配方向固定为：`DuplicateAccounts,DuplicateReservations,DuplicateEvents,DuplicateEventKeys,InvalidTextRows,InvalidAmountRows,OrphanReservations,OrphanEvents,ReservationStateMismatches,ReservationBucketMismatches,AccountHeldReservationMismatches,AccountSpentReservationMismatches,AccountHeldEventMismatches,AccountSpentEventMismatches uint64`。重复身份按第一行计入汇总、后续重复只计duplicate，避免重复事实放大总额。缺失引用按孤儿行计数，金额/状态形状按行计数，账户桶差异每个账户/桶最多计一次。

Billing固定入口/类型为 `ProcurementBudgetFactQuery`、`ProcurementBudgetFactRows`、`ProcurementBudgetFacts` 与 `ReadProcurementBudgetFacts(ctx, query, schema)`；facts只有私有摘要字段及 `LocalSummary()`，公开错误固定为 `ErrProcurementBudgetFactsInput/Resource/Canceled/Timeout/Execution`。`maintenancescan`结果类型为`ProcurementBudgetMaintenanceResult`，唯一入口`VerifyProcurementBudgetMaintenance`，取摘要方法`Summary(permit)`。

纯core错误分别为`ErrProcurementBudgetRowsInput/Resource/Canceled/Timeout`；billing与维护边界只转换为上述`ErrProcurementBudgetFacts*`，不暴露部分Facts。此reader不得顺手调用其它会Finish的维护包装器；未来复合scanner必须另冻同租约共享facts及唯一Finish合同。

## 4. 验收

- 纯core正例：空集、一个及多个账户、超过int64的合法中间聚合、signed event delta抵消、精确三方匹配。
- 纯core负例：账户缓存与reservation/event两个方向不符、孤儿reservation/event、attempt属于另一账户、非规范/负金额、重复行身份、非法状态及状态桶不符、空/NUL/非法UTF-8字段、100001总行和16 MiB边界、取消/超时中部分结果为零。
- reader：固定SQL/列/排序键，首次空文本游标、128/129边界、下一页严格递增、三表共享资源预算、Scan/Next/Err/Close/查询/取消各路径失败返回零Facts；无动态表名/列名/where过滤。
- 维护集成：nil/零/错kind拒绝；成功仅能用原Permit和本次`ScanProcurement`proof取summary；JSON不能铸authority，渲染脱敏；proof完成/abort/permit撤销后失效。
- 有专用PostgreSQL时验真实Plan/Init ReadLease、同RR各表EOF及控制损坏投影负例、扫描前后客户账本与采购投影不变。没有`C0_MAINTENANCE_DATABASE_URL`时明确记录真实PG未执行，不以fixture替代。完成代码/doc后跑定向Go测试、race、vet及`make check`；`make verify-mvp`仅在`TEST_DATABASE_URL`可用时执行并如实标示。

## 5. 后续边界

本片只占用已经存在的`ScanProcurement` kind。现有lease每个kind最多尝试一次，各维护reader自行Finish；不得把此reader与其它局部结果拼成单一证明。下一个资金来源阶段须先定义全表basis、采购/供应商事实检查与同ReadLease组合器的封闭共享上下文、完整覆盖缺口，以及epoch writer前的精确准入矩阵。

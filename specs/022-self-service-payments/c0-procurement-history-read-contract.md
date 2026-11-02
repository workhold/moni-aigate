# 022-C0-T3：采购历史十九表同租约读取合同

2026-11-02，根冻结集成。基于已实现的[双审关系片](c0-procurement-approval-contract.md)，本次将[命令/观察](c0-procurement-command-contract.md)与[成本审计/投影](c0-procurement-cost-journal-contract.md)两个纯核验接入原`ScanProcurement`；这不授完整Procurement coverage或生产写入权。

## 唯一读取和完成

原十五表及投影保持；在其后顺序完整扫描`procurement_commands`、`procurement_balance_observations`、`audit_events`、`projection_outbox`。前三张列顺序和DTO严格按命令合同，outbox按成本审计合同。audit_events只有一次完整EOF，原始行分别交两个独立core；不得在SQL按action/kind过滤，不使用JOIN、SQL聚合或写侧谓词。原bindings/accounts/users、attempts/reservations/corrections/proposals都复用同一次读出的原始行，不重复计来源扫描预算或自行重新查询。

19张表共享原`ReadLease`、RR、busy、原绝对deadline、100000总行与16MiB固定投影文本预算。128行C序单列keyset到短页EOF；commands按operation_id，outbox按event_id，其它新表按id。原credential pool保留分列tuple游标。JSONB显式`::pg_catalog.text`取原文本，nullable amount/tenant_scope/provider_as_of不coalesce；不加密码、MFA、邮件、模型正文或凭据密文投影。

调用图固定：`Require(ScanProcurement)`→schema→十九表完整读取→预算/绑定/双审/命令/成本审计五core→唯一`Finish()`。不串调已有会Finish的维护入口，不新增ScanKind。数据mismatch仍正常Finish；任一Query/Next/Scan/Err/Close、游标、EOF、context、共享资源、core执行错误或Finish故障均撤销整lease、返回零结果。JSON形状与关系损坏由纯core作为数据mismatch报告，不能误作执行成功且忽略。

## 摘要与边界

保留`ReadProcurementAttemptBindingFacts`的原三个summary，增加`CommandSummary() ProcurementCommandCheck`和`JournalSummary() ProcurementCostJournalCheck`；facts只持私有计数摘要，不是domain proof。维护`ProcurementMaintenanceResult`增加`CommandSummary(permit)`与`JournalSummary(permit)`，Verify要求全部五个精确scope和同一原Permit的ScanProof；nil、零、JSON和外部构造不铸authority。

新scope为`procurement_command_observation_graph_v1`与`procurement_cost_journal_graph_v1`。所有summary相互独立，不把五个Balanced AND后命名为采购全域CoverageComplete。reader前置输入错误映射原billing `ErrProcurementAttemptBindingFactsInput`；新core资源/取消/期限错误分别映射原Resource/Canceled/Timeout，其余core执行错误映射Execution，保持原封闭错误类别，无原文/driver unwrap或部分结果。

未覆盖完整evidence/metering/finalization语义、全采购事件重放、账户全部配置来源链、PB交叉图、完整来源组合/basis/epoch。人工观察不证明外部真实余额；缺失原始命令payload时input_digest只验64hex形状，不伪称可重建指纹。无迁移、在线资金或普通main/HTTP/worker接线。

## 验收

- 新四表进入原127/128/129分页、全Query/Next/Scan/Err/Close故障矩阵；失败必须实际拥有可Scan的fixture行，以免空表跳过故障点。
- 固定投影/游标、19表共享总行/文本预算、同audit原始行供双core、nullable金额/时间和JSON原文映射，单个mismatch仍返回全部局部结果。
- 维护伪造/错scope/错Permit拒绝与五summary零泄漏；真实PG Plan/Init正例经原管理/账务API产生，Init隔离fixture制造观察与提案/审计不符，核全部读取前后采购事实不变。
- 当前无显式专库时真实PG必须SKIP；只记实际执行的专项race/vet、整仓检查与架构同步，不以纯fixture代替商业数据库或供应商验收。

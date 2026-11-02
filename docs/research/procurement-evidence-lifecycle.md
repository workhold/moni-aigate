# 采购 evidence 生命周期与独立核验边界

2026-11-02，只读源码审计。本文记录当前实现可以证明的关系和没有持久化的见证，不是已实施的 evidence scanner 合同，也不把候选 digest 匹配称为完整来源证明。真实实现入口是 [recordFinalization](../../internal/billing/evidence.go:373)、[finalizeProcurement](../../internal/billing/procurement.go:215)、[恢复](../../internal/billing/procurement.go:278)、[Usage](../../internal/billing/types.go:115) 及 [validateUsage](../../internal/billing/store.go:96)；表来自 [002](../../migrations/002_billing.sql:80) 与 [014](../../migrations/014_procurement_accounts.sql:118)。行号为本次审计时源码位置。

## 1. 两种 digest 不是同一种身份

### 采购语义身份

[procurement.go:220](../../internal/billing/procurement.go:220) 对下面**按 Go struct 字段声明顺序**产生的 JSON 字节做 SHA-256，输出小写64位十六进制：

```json
{"Usage":{"status":"confirmed_final","input_tokens":10,"output_tokens":2,"total_tokens":12,"cached_tokens":0,"reasoning_tokens":0},"Reason":"done"}
```

外层字段是大写 `Usage,Reason`，内层按 Usage 的六个 json tag 顺序；Token计数是 JSON number，不是 Money 字符串。无空白、无 domain separator，沿 `encoding/json.Marshal` 的 UTF8/HTML转义。重建必须在独立模块固定这个具体旧字节协议，不调用 writer，不能对 `jsonb::text` 的键顺序直接 hash，也不能省略0字段、追加source/event ID、改为排序所有键或自创用途域。

Usage.status 允许 confirmed_final/provisional/estimated/missing/conflicting；六个字段全有，五个计数为非负int64，cached<=input，reasoning<=output，confirmed_final要求以大整数核对input+output=total。其它状态不要求total等于和。新增纯解码器应严格读JSON对象、六键/类型/整数并重新序列化，不用float64。采购identity的PK仅 `(attempt_id,evidence_digest)`；event_id只是首次记录该语义时的引用，没有 UNIQUE 或 meter FK。

### Finalization 原调用身份

[evidence.go:378](../../internal/billing/evidence.go:378) 先复制完整 FinalizationInput，把 ObservedAt 置为 `time.Time{}`，然后按原 struct 字段顺序 hash。它包含 `ID,TenantID,RequestID,AttemptID,Source,SourceEventID,Usage,ProtocolOutcome,TransportEndReason,DeliveryStage,FinishReason,ContentWritten,ObservedAt`，末时间的JSON为 `"0001-01-01T00:00:00Z"`。该 digest 在 [交付状态归一及sticky升级](../../internal/billing/evidence.go:407) **之前**计算；finalization表保存的是升级后的DeliveryStage/ContentWritten。故从落库的最终行直接重新marshal，不能保证得到原input_digest。

可以枚举原始DeliveryStage四值与ContentWritten两值的有限候选，寻找preimage；输出content/true可由原八种组合加历史sticky状态产生，输出committed/false可来自none/unknown/committed的false输入，输出none/false或unknown/false只保留自身输入。候选匹配只能证明存在一个可能原输入，不能证明当时哪个前驱使sticky升级合法。schema没有前驱ID/稳定序号或原始delivery/content快照；不能按created_at、UUID、xmin大小补造顺序。精确全历史证明需要后续追加原调用/前驱见证，不改旧SQL、不把缺证说成损坏。

## 2. 真实流程与允许的一对多

1. RecordFinalization验证Usage/枚举和source身份；同一租户的 `(source,source_event_id)` 是meter唯一键，不是 `(attempt,source,source_event_id)`。[evidence.go:432](../../internal/billing/evidence.go:432) 冲突时取回**旧**event ID，比较usage/request/attempt；任一不同即进入冲突。
2. 即使本次新插入meter事件，若同attempt已有不同的confirmed_final Usage，也进入冲突。[evidence.go:445](../../internal/billing/evidence.go:445) 的检查不根据transport理由判断。
3. 冲突追加 `metering_event_conflicts(event_id, incoming normalized_usage)`，然后对incoming attempt调用finalizeProcurement，提交冲突/采购事实并返回语义错误；**不创建本次finalization**。冲突重放可以再次插入相同event+usage的conflict行，表没有语义唯一键。conflict Usage可以等于其event Usage（不同confirmed事件冲突或request/attempt身份冲突），不能一律要求不相等。
4. 正常路径允许多个finalization引用同一meter事件；原ID/digest重放早返回旧finalization，不更新latest，不再次finalizeProcurement。[evidence.go:475](../../internal/billing/evidence.go:475) 只对新finalization执行采购收尾。
5. finalizeProcurement无reservation时立即返回，不创建采购identity。managed时先尝试插入 `(attempt,H(Usage,Reason),event)`；同语义重放即返回，不改采购状态。**同digest的新event可以没有identity指向它**，因为identity仍引用最初event；同event可以对应多个digest（不同理由或冲突Usage），并可涉及不同attempt。
6. identity新增并不保证写预算event：已经disputed的冲突、settled/released/disputed后的正常新语义、已unknown的非final语义都可以只有新identity，无新budget event。不同evidence会使待审proposal的expected_evidence_count过时，但相同语义的新event不会；见 [review测试](../../internal/billing/procurement_review_integration_test.go:11)。

特别地，`identity.attempt_id != metering_events.attempt_id` **并不必然损坏**：同tenant的source身份被incoming另一request/attempt使用时会保留旧event，conflict行只记录incoming Usage，没有incoming attempt/request/reason。这种合法冲突存在归属缺证，不能强行用meter旧attempt绑定incoming identity。

## 3. 固定预算event关系

来源字段 `evidence_ref` 并非统一 meter FK，必须先按type解释：

| type | event_key | evidence_ref | 允许的依据 |
| --- | --- | --- | --- |
| reserved | `reserve:`+attempt | 空 | 初始硬预占 |
| released_no_consumption | `release:`+attempt | meter event ID | reason为not_sent/rejected_no_consumption，未先进入冲突 |
| consumption_unknown | `unknown:`+attempt | meter event ID | 非confirmed_final且非无消费理由 |
| cost_confirmed | `settle:`+attempt | meter event ID | confirmed_final；原报价两维成本与超界分别计算 |
| evidence_disputed | `conflict:`+attempt+`:`+event | meter event ID | incoming冲突，或released后非零confirmed/settled正spent后无消费理由 |
| expired_without_permit | `release:`+attempt | 空 | RecoverProcurement的prepared+prepared分支 |
| recovered_unknown | `unknown:`+attempt | 空 | RecoverProcurement其余未决分支 |
| cost_corrected | `correction:`+approval operation | 供应商外部证据引用 | 双人更正，另由approval graph核，不按meter ID读取 |

event_key须按已知attempt/event的完整字符串构造比较，不用任意split或前缀推断managed来源。恢复可以没有meter/finalization/identity；unknown与released后也有合法新证据、冲突和更正。当前event只有delta而无before/after/revision/稳定序列，完整逐事件状态重放不能从当前末值、created_at或UUID排序推导。原预算投影scope只核守恒，不能因此声称历史顺序可证。

## 4. 最小完整投影和DTO建议

复用同ScanProcurement已读的attempt/request/reservation/budget events。新增四表全部读、无业务WHERE，128行keyset：

| 表 / 键 | 最小列顺序 |
| --- | --- |
| procurement_evidence_identities / `(attempt_id,evidence_digest)` 两列C序 | attempt_id,evidence_digest,event_id,created_at |
| metering_events / id C序 | id,tenant_id,request_id,attempt_id,source,source_event_id,schema_version,normalized_usage::pg_catalog.text,happened_at,received_at |
| metering_event_conflicts / id C序 | id,event_id,normalized_usage::pg_catalog.text,created_at |
| finalization_records / id C序 | id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version,created_at |

如果核finalization.policy_version，需要在原request投影追加policy_version；如果核latest引用则追加latest_finalization_id。DTO分别为 EvidenceIdentity{AttemptID,Digest,EventID string; CreatedAt time.Time}、Metering{ID,TenantID,RequestID,Source,SourceEventID,UsageJSON string; AttemptID *string; SchemaVersion int32; HappenedAt,ReceivedAt time.Time}、Conflict{ID,EventID,UsageJSON string; CreatedAt time.Time}、Finalization{ID,TenantID,RequestID,EventID,InputDigest,ProtocolOutcome,TransportEndReason,DeliveryStage,FinishReason,PolicyVersion string; ContentWritten bool; CreatedAt time.Time}。资源预算必须包括所有原始JSON文本，共用原100000行/16MiB，不对每表重新初始化；只读读取的事实不授维护权。

## 5. 可以直接实施的独立规则与缺证分类

可确定mismatch：重复真实主键/唯一meter身份；严格Usage与枚举/文本形状非法；孤立event/request/attempt/finalization/conflict/identity；meter自身tenant/request/nullable attempt关系不一致；正常finalization与meter的tenant/request不一致；identity引用不存在的managed reservation或event；known finalization理由对应的managed `(attempt,H(Usage,Reason))` 缺失；evidence型budget event缺meter/managed reservation、key/account不符；恢复型event错误携带meter引用；外部更正误当meter证据。

**不能要求**每个meter有finalization、每个meter有唯一identity、每个identity有budget event、每个conflict的Usage异于原event，或每个identity引用event的旧attempt等于incoming attempt。也不能要求当前attempt.cost_amount等于任一历史Usage成本；invoice更正和后续冲突均可改变当前成本。

采购digest的已知证据核对可以从某event的正常finalization取得Reason并独立hash。冲突没有Reason时，可以对该event的conflict Usage与10个允许理由枚举候选；命中只是**可能原语义**，应另计 `ConflictReasonUnproven`，不能标成精确重算已证。跨request/attempt event复用另计 `IncomingBindingUnproven`；finalization原始delivery缺失计 `OriginalDeliveryUnproven`，预算event顺序缺失计 `EventOrderUnproven`。若做旧root全历史核验，至少采用mismatch/indeterminate/balanced三态或在固定局部scope中显式报告这些缺证，不能静默豁免后输出完整coverage。

现有信息还无法把“完全没有正常finalization/identity的冲突incoming attempt”独立恢复：conflict根不保存incoming身份；不能从整库所有attempt交叉枚举猜归属。后续应追加不可变incoming observation（原attempt/request/Usage/Reason、meter/conflict及原input digest/原delivery字段），并在同事务为每次采购语义身份保存可核原文与来源见证；旧行保留legacy缺证。该迁移与writer接线须单独SDD、角色/故障/恢复验收，不能在scanner中临时造事实或从当前DB反推后再自证。

本次没有执行数据库实验或实现evidence scanner；本文结论来自上述schema/writer及既有测试源码，不能被当作新生产验收记录。

# 022-C0-T3：采购 attempt 绑定与报价快照核验合同

2026-11-02，根冻结实现片。它扩展既有 `ScanProcurement` 预算投影扫描，在一次维护快照中独立核验永久凭据绑定、attempt/request/凭据池关系、不可变采购报价快照及预占上界。此片只提供采购域的两个局部摘要，不代表完整 Procurement coverage、客户资金来源证明或真实供应商账单。

本文保留十二表/双摘要实施片的冻结范围。当前同一reader继续按[成本双审扩展合同](c0-procurement-approval-contract.md)读取十五表并返回第三个局部摘要；下文未覆盖cost correction/proposal的表述属于该十二表历史片，不覆盖新扩展，其他未覆盖项保持。

## 1. 固定读取范围

所有关系均从各自基表完整 C 序 keyset 扫描；不得以 JOIN、WHERE 业务条件、SQL 聚合或写侧函数作为核验 oracle。每页最多 128 行，短页才表示 EOF。单列首游标用私有 `hasCursor=false`；复合游标用私有 `hasCursor=false` 和两个空参数。后续复合页按完整 tuple 比较：

```sql
WHERE NOT $1::pg_catalog.bool OR
  (channel_version_id COLLATE "C", credential_id COLLATE "C") >
  ($2::pg_catalog.text COLLATE "C", $3::pg_catalog.text COLLATE "C")
ORDER BY channel_version_id COLLATE "C", credential_id COLLATE "C"
LIMIT 128
```

不得把复合游标拼接成字符串。固定表名、排序键和投影列顺序如下：

| 关系 | 排序键 | 固定列顺序 |
| --- | --- | --- |
| `channels` | `id` | `id` |
| `channel_credentials` | `id` | `id, channel_id`；绝不读取 `secret_ciphertext` |
| `channel_versions` | `id` | `id, channel_id, credential_id` |
| `channel_version_credentials` | `(channel_version_id,credential_id)` | `channel_version_id, channel_id, credential_id, position, weight, max_inflight` |
| `tenants` | `id` | `id` |
| `request_records` | `id` | `id, tenant_id, input_upper_bound::pg_catalog.text, output_limit::pg_catalog.text` |
| `cost_versions` | `id` | `id, channel_id, upstream_model, input_rate::pg_catalog.text, output_rate::pg_catalog.text, basis, effective_at, created_at` |
| `procurement_accounts` | `id` | 既有预算合同固定列：`id, held_amount::pg_catalog.text, spent_amount::pg_catalog.text` |
| `credential_procurement_bindings` | `id` | `id, credential_id, channel_id, account_id, operation_id, bound_at` |
| `upstream_attempts` | `id` | `id, tenant_id, request_id, sequence, channel_id, channel_version_id, credential_id, upstream_model, delivery_state, cost_version_id, cost_input_rate::pg_catalog.text, cost_output_rate::pg_catalog.text, cost_basis, cost_status, cost_amount::pg_catalog.text, created_at` |
| `procurement_reservations` | `attempt_id` | `attempt_id, account_id, binding_id, input_bound::pg_catalog.text, output_bound::pg_catalog.text, upper_amount::pg_catalog.text, held_amount::pg_catalog.text, spent_amount::pg_catalog.text, state, anomaly` |
| `procurement_budget_events` | `id` | 既有预算合同固定列：`id, event_key, account_id, attempt_id, held_delta::pg_catalog.text, spent_delta::pg_catalog.text` |

Nullable报价列按NULL读取，不 `coalesce`；BIGINT/NUMERIC金额只转十进制文本，不作 float 或预聚合。其它时间列由驱动按 PostgreSQL `timestamptz` 解码为 `time.Time`。单列字符键与排序谓词、`ORDER BY` 显式使用 `COLLATE "C"`。所有标识符只由已授权 schema 与固定代码组成。

## 2. 固定纯核验 DTO 与计数语义

纯入口为 `fundinglotverify.CheckProcurementAttemptBindingGraph(ctx, rows) (ProcurementAttemptBindingCheck, error)`。`ProcurementAttemptBindingRows` 固定包含下列十二组有序原始行，不得传预聚合结果：

- `Channels []ProcurementAttemptChannelRow{ID string}`；`Credentials []ProcurementAttemptCredentialRow{ID,ChannelID string}`；`Versions []ProcurementAttemptVersionRow{ID,ChannelID,CredentialID string}`；`PoolMemberships []ProcurementAttemptPoolMemberRow{ChannelVersionID,ChannelID,CredentialID string; Position,Weight,MaxInflight int32}`。
- `Tenants []ProcurementAttemptTenantRow{ID string}`；`Requests []ProcurementAttemptRequestRow{ID,TenantID,InputUpperBound,OutputLimit string}`；`CostVersions []ProcurementAttemptCostVersionRow{ID,ChannelID,UpstreamModel string; InputRate,OutputRate *string; Basis string; EffectiveAt,CreatedAt time.Time}`。
- `Accounts []ProcurementBudgetAccountRow`、`Bindings []ProcurementAttemptBindingRow{ID,CredentialID,ChannelID,AccountID,OperationID string; BoundAt time.Time}`、`Attempts []ProcurementAttemptRow{ID,TenantID,RequestID string; Sequence int32; ChannelID,ChannelVersionID,CredentialID,UpstreamModel string; DeliveryState string; CostVersionID,CostInputRate,CostOutputRate *string; CostBasis,CostStatus string; CostAmount *string; CreatedAt time.Time}`、`Reservations []ProcurementAttemptReservationRow{AttemptID,AccountID,BindingID,InputBound,OutputBound,UpperAmount,HeldAmount,SpentAmount,State string; Anomaly bool}` 与 `BudgetEvents []ProcurementBudgetEventRow`。两个预算行类型使用既有定义。

`ProcurementAttemptBindingCheck` 的 `Scope` 固定为 `procurement_attempt_binding_graph_v1`；`Scanned` 固定含 `Channels,Credentials,Versions,PoolMemberships,Tenants,Requests,CostVersions,Accounts,Bindings,Attempts,Reservations,BudgetEvents uint64`，分别表示完整 EOF 前成功扫描并交给 core 的源行数。

`Mismatches` 固定包含以下 `uint64` 字段，每字段以源行计数：

`DuplicateChannelRows, DuplicateCredentialRows, DuplicateVersionRows, DuplicatePoolMembershipRows, DuplicateTenantRows, DuplicateRequestRows, DuplicateCostVersionRows, DuplicateAccountRows, DuplicateBindingRows, DuplicateAttemptRows, DuplicateReservationRows, DuplicateBudgetEventRows` 统计每关系超出首次保留身份的重复行；`DuplicatePoolPositionRows`、`DuplicateBindingCredentialRows`、`DuplicateBindingOperationRows`、`DuplicateAttemptRequestSequenceRows`、`DuplicateAttemptRequestChannelRows` 分别统计违反对应唯一键约束的多余行。

`InvalidTextRows`、`InvalidAmountRows`、`InvalidEnumRows` 和 `InvalidPoolBoundsRows` 分别按一个源行最多计一次：该行任一必需文本为空/非法；任一金额文本不规范、越范围或违反列域；任一枚举不受支持；或任一 pool 的 position/weight/max_inflight 越迁移边界时计数。BIGINT金额文本必须规范且在 `0..9223372036854775807`；`procurement_reservations.input_bound` 的列域是 `1..9223372036854775807`；`output_bound` 为 `0..9223372036854775807`；`upper_amount` 为 `0..9223372036854775807`；账户 held/spent 与 reservation held/spent 是非负且不超过 100 位的 `NUMERIC(100,0)`；event held/spent delta 是绝对值最多 100 位的规范有符号整数；`cost_amount` 是非负且最多 100 位的 `NUMERIC(100,0)`。费率与 request bounds 是 BIGINT。浮点、前导零（除单独的 `0`）、负零、加号和非十进制均不规范。`InvalidEnumRows` 覆盖 channel version credential pool basis、attempt delivery_state/cost_basis/cost_status 与 reservation.state，允许集分别见 §3 与迁移状态约束。

关系 mismatch 字段及每个源行在每个字段中的最多计数为：`CredentialChannelIdentityRows`（credential 行的 channel 缺失）；`ChannelVersionIdentityRows`（version 行的 channel 或 primary credential 缺失/跨 channel）；`PoolMembershipIdentityRows`（pool 行的 version/credential 缺失或三方 channel 不一致）；`VersionPrimaryMembershipRows`（version 行不存在精确 primary membership）；`BindingIdentityRows`（binding 行的 credential/channel/account 缺失或 credential.channel 不同）；`AttemptRequestTenantRows`（attempt 行的 tenant、request 缺失或 tenant 关系不一致）；`AttemptVersionIdentityRows`（attempt 行的 version/channel 缺失或 channel 不同）；`AttemptPoolMembershipRows`（attempt 行不存在精确 version/channel/credential membership）；`AttemptCostQuoteRows`（attempt 行引用缺失、晚生效或字段快照不精确匹配的 cost version）；`AttemptCostShapeRows`（attempt 行 nullable报价或cost_status/cost_amount组合不成立）；`AttemptReservationMissingRows`（永久 binding 在 attempt.created_at 前已生效但 attempt 无 reservation）；`ReservationIdentityRows`（reservation 行的 attempt/account/binding 缺失、binding/account/credential/channel不匹配，或 binding 晚于 attempt）；`ReservationRequestBoundRows`（reservation.input/output bound 与对应 request bounds 不同）；`ReservationUpperBoundRows`（reservation 行缺少可用的已知采购报价，或 `upper_amount` 不等于精确报价上界）。同一源行可以在不同字段各计一次，但相同字段至多计一次。

`InvalidAttemptSequenceRows` 为独立的 attempt 行计数：sequence 不在 `1..2` 时该字段加一；它不是金额文本错误，不计入 `InvalidAmountRows`。

重复身份按首次出现行建立索引，额外重复行只增加相应duplicate字段，不参与关系图和总额放大；唯一键重复也按首次行保留。关系 mismatch 不改变扫描行数。`Balanced` 当且仅当上述所有 mismatch 计数均为零；数据矛盾是成功核验结果，而非 core 错误。

## 3. 固定核验规则

- credential 必须引用存在的 channel。binding 的 credential、channel、account 必须存在且 credential.channel 必须等于 binding.channel；每 credential 及 `operation_id` 各至多一个binding。credential当前撤销不否定其历史永久归属。
- 每个 channel version 必须引用已扫描 channel 与同 channel credential。pool membership 的 version、credential必须存在且三方 channel 一致；每 version 的 position 唯一，position 为 `1..32`、weight 为 `1..1,000,000`、max_inflight 为 `1..1000`；每 version 必须有其声明 primary credential 的精确 membership。`channel_version_credentials.label` 未投影，本片不核验其独立展示文本约束。
- request bounds 是规范非负 BIGINT 文本。每个 attempt 的 sequence 为 `1..2` 且 `(request_id,sequence)`、`(request_id,channel_id)` 分别唯一；attempt 必须引用存在 tenant 与同 tenant request、存在且 channel 一致的 version、及精确 version/channel/credential pool membership。`delivery_state` 只接受 `prepared|sending|completed|failed|unknown`。
- attempt报价身份由 `cost_version_id`、channel、upstream_model、input/output rates和basis精确匹配已扫描 `cost_versions`。NULL version只允许 rates 均NULL且 `cost_basis=unknown`。非NULL version要求其行存在、channel/model/rates/basis精确一致且 `effective_at <= attempt.created_at`。报价费率可以单独为NULL；非NULL费率均须为非负 BIGINT，basis只接受 `contract|invoice|unknown`。
- `cost_status` 只接受 `unknown|contract|invoice`；unknown必须无`cost_amount`，contract/invoice必须有规范非负 `NUMERIC(100,0)`。不要求最终cost_status等于初始cost_basis，因为双审更正可改变最终实际成本basis。
- 若某 attempt 的同 credential/channel 永久 binding 满足 `bound_at <= attempt.created_at`，必须恰有一个 reservation；binding 之后的历史 attempt 不得被追溯绑定。任何 reservation 也必须满足该时间关系。对每个 reservation，`input_bound` 必须大于零，input/output bounds须分别等于对应 request 的 `input_upper_bound/output_limit`；报价须完整、两个费率非NULL且basis为`contract|invoice`，`upper_amount`须等于 `ceil((input_bound*cost_input_rate + output_bound*cost_output_rate)/1_000_000)` 的精确大整数结果，且结果不超过 `9223372036854775807`。异常成本状态或最终cost_status不改变冻结报价上界。
- 预算三表同时交给既有 `CheckProcurementBudgetProjection`，继续核账户 held/spent、reservation 汇总和不可变 event delta。预算摘要与绑定图摘要保持固定独立scope，不合成采购全覆盖位。

纯核验只用 `math/big.Int`，不调用 routing planner、billing writer、SQL trigger 或 `funding_journal_valid`。错误仅表示输入入口无效、上下文取消/deadline 或资源超限；错误必须返回零摘要。数据不匹配返回非零局部摘要与 `Balanced=false`。所有字符串不回显原值。

## 4. ReadLease 与错误分类

唯一维护入口要求真实 `ReadLease` 与 `ScanProcurement`：`Require(ScanProcurement)` → 固定 schema 和十二关系完整读取 → 预算投影 core 与绑定图 core → 唯一一次 `Finish()`。同一真实 ReadLease、RR 快照、busy 和原始总期限覆盖全部SQL、分页和CPU；所有十二关系共享一个100000总行、16MiB所有投影文本返回值预算（包括 `::text` 金额）。

完整扫描成功后，即使任一 core 返回 `Balanced=false`，也必须正常调用且仅调用一次 `Finish()`，然后分别返回两个局部 mismatch 摘要。SQL、Scan/Next/Err/Close、未完整EOF、游标重复/不前进、资源超限、无效数据库文本、取消/deadline、core 输入/执行资源错误或 Finish 失败都属于执行失败：返回零维护结果并撤销整lease。不得把数据 mismatch 错报成Abort，也不得把错误路径的部分facts作为摘要返回。只有原 Permit 与本次 `ScanProcurement` proof 可分别读取预算摘要和绑定图摘要；JSON、零值或复制不能铸造proof。summary 脱敏。

## 5. 边界与验收

本片不扫描或证明 `procurement_evidence_identities`、`provider_cost_corrections`、`procurement_cost_proposals`、`procurement_balance_observations`、`procurement_commands`、相关 audit/outbox、`metering_events`，也不核 `periodic_budget_*` 采购分配。成本快照关系不证明最终使用量、invoice、真实供应商余额、真实扣款或最终外部成本；上界一致也不代表真实账单正确。本结果不得含`CoverageComplete`，不得作为 C0 `Procurement` 域完成、funding lot、epoch/init 准入、客户钱包修改、资金写入或生产维护授权。全来源组合仍须另冻共享完成记录、所有域固定 reader/反向 sweep/phase矩阵和唯一 Finish。

- 纯core覆盖精确正例、绑定前legacy attempt、报价关系与bounds公式、managed attempt缺reservation、每类孤儿/跨tenant/错channel/version/pool、重复/唯一键冲突、错误NULL/状态/整数/公式上界、100001总行、16MiB边界和取消/deadline零结果。
- Billing reader覆盖十二组固定列/键/排序、空首游标、复合C序游标、127/128/129行、多页严格递增、共享总budget、查询/Scan/Next/Err/Close/EOF/取消/资源失败零facts；凭据密文、request secret和事件evidence字段不在投影中。
- 维护集成只占一次 `ScanProcurement` 并只 Finish 一次；数据mismatch仍Finish并可读取两个摘要；执行错误整lease撤销且无部分摘要；原 Permit proof 同时约束两项摘要；不接收provider或外部facts。
- 外部PostgreSQL Plan/Init使用真实绑定credential/channel version/cost quote/request/attempt/reservation正例；superuser fixture绕过业务触发器制造binding/attempt/报价/upper缓存矛盾，scanner产生相应行级 mismatch；扫描前后账户、attempt、reservation、事件均字节级不变。缺 `C0_MAINTENANCE_DATABASE_URL` 时必须明确记为未执行；缺 `TEST_DATABASE_URL` 时不得运行/宣称 `make verify-mvp` 通过。

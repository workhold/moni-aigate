# 022-C0-T3：CustomerKey 客户财务关系核验（根冻结）

2026-11-02。依 [全源候选](c0-verifier-contract.md)、[HLD](../../docs/design/HLD.md) 与 [LLD](../../docs/design/LLD.md)，本片从原始行独立核对 API Key、请求、客户预占、唯一结算、消费退款及其资金 journal。根已完整审阅，并在独立 writer 复核后冻结授权本精确合同的纯 core 与测试；reader、维护组合、共享 DTO、README、统一架构与最终验证由根另行集成，本文不授予全源 coverage、basis、epoch、lot 或生产写入能力。

原事实来自 [001 Key](../../migrations/001_core.sql:42)、[002 客户财务表](../../migrations/002_billing.sql:44)、[018 请求售价快照](../../migrations/018_tenant_pricing.sql:202)、[020 reservation identity](../../migrations/020_periodic_budgets.sql:516) 与实际 [Reserve](../../internal/billing/reserve.go)、[finish](../../internal/billing/settle.go:119)、[退款](../../internal/billing/orders.go:118)、[人工对账释放](../../internal/billing/recovery.go:148)、[postLocked](../../internal/billing/store.go:227)。读取写侧源码用于确定事实合同，核验实现不得调用写侧函数或 SQL 有效性谓词。

## 1. 固定纯 API 与原始投影

```go
func CheckCustomerKeyGraph(context.Context, CustomerKeyGraphRows) (CustomerKeyGraphCheck, error)

type CustomerKeyRow struct {
    ID, TenantID, OwnerID, Held, Spent string
    LifetimeLimit *string
}
type CustomerKeyRequestRow struct {
    ID, TenantID, KeyID, OwnerID, BillingStatus string
    InputRate, OutputRate, InputUpperBound, OutputLimit string
}
type CustomerKeyReservationRow struct { ID, TenantID, RequestID, Amount, Status string }
type CustomerKeySettlementRow struct {
    ID, TenantID, RequestID, ChargedAmount, RawRetailAmount string
    WaivedRetailAmount, RefundedAmount, Status, Reason, JournalID string
}
type CustomerKeyRefundRow struct {
    ID, TenantID, SettlementID, BusinessRef, Amount, State, Requester string
    Approver, JournalID *string
}
type CustomerKeyGraphRows struct {
    Tenants []ProcurementAttemptTenantRow
    Users []ProcurementApprovalUserRow
    Keys []CustomerKeyRow
    Requests []CustomerKeyRequestRow
    Reservations []CustomerKeyReservationRow
    Settlements []CustomerKeySettlementRow
    Refunds []CustomerKeyRefundRow
    Accounts []LedgerAccountRow
    Journals []FundingGraphJournalRow
    Lines []LedgerLineRow
}
```

`FundingGraphJournalRow` 由根在共享纯 DTO 文件唯一声明：`ID,TenantID,OperationID,Kind,Reference,Actor,Currency,CreatedTXID string; CreatedAt time.Time`。其 `CreatedTXID/CreatedAt` 仅保留原始投影和计入资源，不作为本片的同事务、发生先后或生命周期证明。Account/Line 沿既有 ledger DTO，不复制声明；租户/用户沿已存在的仅身份投影，不读取当前角色、禁用状态、密码、邮件或 MFA。

根的同 session owned-raw reader 对以下关系完整读取、不加业务过滤、不 JOIN、不聚合；同一张物理表只扫描一次，复用 owned rows 映射多个纯 core。精确字段顺序：

| 关系 | 固定列（金额保留原始十进制） |
| --- | --- |
| `tenants` | `id` |
| `users` | `id` |
| `api_keys` | `id,tenant_id,owner_id,held::pg_catalog.text,spent::pg_catalog.text,lifetime_limit::pg_catalog.text` |
| `request_records` | `id,tenant_id,key_id,owner_id,billing_status,input_rate::pg_catalog.text,output_rate::pg_catalog.text,input_upper_bound::pg_catalog.text,output_limit::pg_catalog.text` |
| `reservations` | `id,tenant_id,request_id,amount::pg_catalog.text,status` |
| `settlements` | `id,tenant_id,request_id,charged_amount::pg_catalog.text,raw_retail_amount::pg_catalog.text,waived_retail_amount::pg_catalog.text,refunded_amount::pg_catalog.text,status,reason,journal_id` |
| `refund_orders` | `id,tenant_id,settlement_id,business_ref,amount::pg_catalog.text,state,requester,approver,journal_id` |
| `ledger_accounts` | `id,tenant_id,kind,balance::pg_catalog.text` |
| `journals` | `id,tenant_id,operation_id,kind,reference,actor,currency,created_txid::pg_catalog.text,created_at` |
| `journal_lines` | `id,journal_id,account_id,signed_delta::pg_catalog.text` |

所有单列主键使用显式 C 序 128 行 keyset；起始游标用独立布尔位，不能用空字符串表示尚未开始。外层 reader 的授权、schema/列形状、完整 EOF、共享总 budget、ReadLease/session 所有权由根另冻；纯 DTO 本身不证明数据库完整读取。

## 2. 原始值、金额与去重

关系身份使用原 TEXT 精确比较，不 trim、不大小写转换、不新增 UUID 或长度假设。空 TEXT 身份保留为真实值参与索引与匹配，不与 SQL NULL 混同；所有投影字符串必须合法 UTF-8 且无 NUL，非法行计 `InvalidTextRows`。`Approver/JournalID/LifetimeLimit` 的 nil 表示 SQL NULL，非 nil 空字符串仍是原始空值；审批 NULL 形状按下文单独核对。业务 enum 与必需 actor 值受各自明确语义约束，不借身份词法扩大约束。

Key held/spent/非 NULL limit、request 两个 rate/两个 bound、reservation.amount、settlement charged/refunded、refund.amount 均为规范非负 BIGINT 十进制，最大 `9223372036854775807`；refund.amount 另须大于零。raw/waived 为规范非负 `NUMERIC(100,0)`，最多100位。拒绝前导零、正号、负零、空白、小数和指数。共享 Account.balance/Line.signed_delta 为规范有符号 BIGINT，`CreatedTXID` 仅按其原 BIGINT 文本检查编码/资源；本片不取它推导业务事实。所有算术与累计使用 `math/big.Int`，大整数求和不截断为 int64。

所有十组输入先合计100000行、16MiB全部投影字符串（含 nullable 非 NULL 值、未解释的 journal 字段），再建索引；不按表/循环重置上限。超限为执行错误，不能返回部分结果。每256步及退出前检查 context。

主键重复按额外行计对应 `Duplicate*Rows`，第一次原始行保留，不用后出现值替换、也不放大汇总。各表键空间独立，相同文本可出现在不同表。reservation.request_id、settlement.request_id、refund.business_ref、journal.operation_id、line.(journal_id,account_id) 的完整 typed 唯一键同样按额外行独立计数；主键重复行不再次参与其唯一键/关系扫描，唯一键冲突保留首个关系行。所有被排除行仍计 Scanned/原始 budget。普通 unrelated journal 同样参与 operation 唯一性，不按本片 kind 过滤。

## 3. Key、请求与终态关系

- 每个 Key 的 tenant 和 owner 必须分别在原 tenants/users 身份集中；每个 request 的 tenant、owner、key 必须存在，且 key.tenant=request.tenant、key.owner=request.owner。后者是当前受支持创建/使用路径的身份不变量，不能据此证明历史成员授权或任意 owner 转移历史；未来允许转移须先持久化历史身份并修订合同。
- 每个 request 必须恰有一个同 tenant reservation，反向每个 reservation 必须有同 tenant request。`request.billing_status` 与 `reservation.status` 均只接受 `reserved|pending_reconciliation|settled|released`，且精确相等。非财务 request.status 不投影，也不从 accepted/completed/failed/cancelled 推断收费终态。
- 独立计算 `R = ceil((input_upper_bound*input_rate + output_limit*output_rate)/1000000)`，要求 `R<=MaxInt64` 且 reservation.amount 精确等于 R。不引用 billing.Calculate、SQL SUM 或写侧谓词。该式只核冻结数值之间的关系，不证明所选价版本、profile 或供应商的真实 token 上界。
- 未完成 reservation（reserved/pending_reconciliation）不得有 settlement；terminal reservation 必须恰有一个同 tenant/request 的 settlement，且 request、reservation、settlement 三者财务终态相同。反向 settlement 必须存在同 tenant request 和 reservation。24小时 deadline 已过但尚待恢复的未完成行仍可合法，本片不看当前时钟强行要求已释放。
- settled 的 reason 精确为 `charged_usage`；`C=min(R,raw)`、`raw=C+waived`、`0<=refunded<=C`。C=0/raw=0 的 settled 合法，不把零收费自动分类为 released。raw 可以超过 R，超出部分 waived，不重算不存在于本片的 metering/finalization。
- released 要求 C/raw/waived/refunded 全零；reason 允许 `released_deadline|released_no_delivery|released_not_sent|released_rejected_no_consumption|released_no_capacity|released_authorization_revoked|released_platform_failure|released_provider_failure|released_reconciliation`。不以这些持久化 reason 证明当时真实交付/时钟事实。
- 对每个 Key，独立累计所有同 tenant/request/key 的未完成 reservation.amount，要求 `key.held=Σunfinished R`；独立累计所有其历史 settlement.charged_amount，要求 `key.spent=Σoriginal C`。退款不从该累计扣减；invalid 金额或悬空关系已各自计异常，不使用零值补齐。若一个已归属聚合的输入金额无效，则该聚合不可计算，保留原始异常并跳过该聚合的金额相等比较，不用部分和另报确定金额不符。非 NULL 当前 lifetime_limit 要求 `spent+held<=limit`，这只是当前行的约束，不声称历史准入时使用了今天的 limit。

每个 Key 可以没有任何请求且 held/spent 都为零；每个请求只有一笔客户结算，不按上游 attempt 数拆分客户 C，也不从采购成本回推客户账单。

## 4. 消费退款与独立累计

每个 refund 必须引用存在且同 tenant 的 settlement，requester 必须在 users 中；state 仅 `pending|approved`，金额正且不超过原 C。pending 必须 approver、journal_id 都为 NULL；approved 必须二者都非 NULL，approver 存在且与 requester 不同。允许多笔 pending 总额大于 C 或剩余可退款额：真实 CreateRefund 只约束单笔不大于原 C，最终 cap 在串行审批时检查，不能把合法待审队列误判成已退。

每个 settlement.refunded_amount 必须精确等于所有同 tenant、关联到该 settlement 的 approved refund.amount 之和，且累计不超过 C。已归属 approved refund 金额无效时沿上一节的不可计算规则，不用零值或部分和替代完整累计。pending 不进入已退累计；approved 退款只增 AVAILABLE、减 REVENUE，不回退 Key.spent，也不恢复原 request 的 held。released/零收费结算不可能有合法正额 refund。

本片不读取 financial_idempotency、审计、outbox 或角色历史，不证明业务号重试、真实双审权限或现金原路退款；它核的是当前持久化退款形状、双人身份、原结算归属与财务效果。退款 requester/approver 当前被禁用或撤销财务角色不否定历史数据。

## 5. 资金 journal 的正反向关系

每个源必须精确匹配一个 journal，下面所有 customer 账户都必须 tenant_id=原 tenant，REVENUE 必须 tenant_id IS NULL；journal.currency 精确 CNY。分录集合必须恰好等于表列，包含金额0的必需行，不能有额外账户或额外零分录。

| 来源 | header 关系 | 精确 signed_delta 集合 |
| --- | --- | --- |
| reservation | operation_id=reservation.id，kind=reserve，reference=request.id，actor=request.owner | AVAILABLE=-R，HELD=+R（包括R=0） |
| settled/released settlement | id=settlement.journal_id，operation_id=settlement.id，kind=settlement.status，reference=request.id，actor=system | HELD=-R，AVAILABLE=R-C；C>0另有REVENUE=+C；C=0时恰两行 |
| approved refund | id=refund.journal_id，operation_id=refund.id，kind=refund，reference=settlement.id，actor=refund.approver | AVAILABLE=+amount，REVENUE=-amount |

pending refund 不得存在 operation_id=refund.id 的 journal，不能仅因 nullable journal_id 为空就忽略游离效果。所有相关 kind=`reserve|settled|released|refund` journal 反向必须找到对应源并精确匹配 header/lines；不存在源、源未批准/不终态、源指向另一 journal、错 tenant/reference/actor、分录缺多或金额错误都独立报告。不能只沿源 FK 正向检查而漏掉孤立 journal。

账户与分录的全部原始行仍读取；本片只解释上述四种 journal 的业务关系，不判断别的 kind 的业务来源。全 ledger 的账户余额重放、全量 journal 平衡和非客户 kind 由独立既有 `CheckLedgerRows` 与后续来源合同承担。缺失 line.account、line.journal、重复(account,kind) 或 dangling line 不能靠忽略消失：本片记录其基础关系异常；对本片相关 journal 同时计相应效果异常。`CreatedTXID/CreatedAt` 不证明 source、journal、audit 同事务；没有稳定事件序列时不从 UUID/时间猜历史顺序。

## 6. 固定结果、失败和验收

结果 `CustomerKeyGraphCheck{Scope string; Scanned CustomerKeyGraphCounts; Balanced bool; Mismatches CustomerKeyGraphMismatches}`，scope精确 `customer_key_financial_graph_v1`；Counts为十组 `Tenants,Users,Keys,Requests,Reservations,Settlements,Refunds,Accounts,Journals,Lines uint64`，不回显 ID、金额或原始文本。没有 CoverageComplete、Ready、proof 或修账方法。

固定 mismatch uint64 字段：`DuplicateTenantRows,DuplicateUserRows,DuplicateKeyRows,DuplicateRequestRows,DuplicateReservationRows,DuplicateReservationRequestRows,DuplicateSettlementRows,DuplicateSettlementRequestRows,DuplicateRefundRows,DuplicateRefundBusinessRefRows,DuplicateAccountRows,DuplicateAccountKindRows,DuplicateJournalRows,DuplicateJournalOperationRows,DuplicateLineRows,DuplicateJournalAccountRows,InvalidTextRows,InvalidAmountRows,InvalidEnumRows,KeyIdentityRows,RequestIdentityRows,RequestReservationRows,ReservationIdentityRows,ReservationAmountRows,RequestFinancialStateRows,SettlementIdentityRows,SettlementStateRows,SettlementAmountRows,RefundIdentityRows,RefundShapeRows,SettlementRefundedRows,KeyHeldRows,KeySpentRows,KeyLimitRows,ReservationJournalRows,SettlementJournalRows,RefundJournalRows,PendingRefundEffectRows,JournalSourceRows,LineIdentityRows`。

每个字段按相应源行至多一次；重复计数是额外行数，跨字段可分别计同一故障。`RequestReservationRows`核 request 正向存在，`ReservationIdentityRows`核 reservation 反向身份；`RequestFinancialStateRows`核 request/reservation 状态与 terminal/unfinished settlement集合，`SettlementStateRows`核 settlement 反向终态/已知reason。`SettlementAmountRows`核 C/raw/waived/R 代数；`SettlementRefundedRows`核 approved 总额。正向 journal 字段按应有源行计缺失/不精确，`JournalSourceRows`按相关 journal 行反向计不精确。`LineIdentityRows`按原始唯一line行计缺 journal/account 或跨tenant customer account。其它字段按前述同名来源规则解释。所有计数为零时且仅此时 Balanced=true；合法空集结果可为局部 Balanced，不能说明数据库为空或全源覆盖。

固定错误 `ErrCustomerKeyGraphRowsInput/Resource/Canceled/Timeout`：nil context 为Input、总预算超限为Resource、父context取消/到期分别对应Canceled/Timeout；任何错误返回零Check。形状、规范数字、enum、关系、计数缓存和效果矛盾属于正常完成的数据 mismatch，不把它们混成执行失败。若根 reader SQL/Scan/Next/Err/Close/EOF、lease或session失败，外层结果整体零，不将未扫完整的 facts 喂给core拼成功。

纯core验收至少包括：空集和无请求零Key；unfinished reserved/pending；部分charge/fullcharge/waived/零settled/所有released reason；R=0必需两行；全额退款/多次approved/多pending超过C但尚合法；refund不减spent；每向孤儿/错tenant/错key-owner/终态矛盾；Key累计与limit；所有源↔journal正反缺失/错header/错误或额外零行；重复主键和typed unique键不放大累计；任意非UUID/空TEXT身份及NULL/空区别；BIGINT边界/NUMERIC100/ceil精度/非法数字；共享100000行/16MiB及取消/timeout错误零结果。先race/vet及diff检查，后由根并入完整reader/session/真实PG与架构验证，纯测试不得称商业或生产验收。

## 7. 历史信息与保留边界

当前 Key 配置包含可修改的名称、scopes、regions、lifetime_limit、expires_at、source/runtime limits以及revoked_at；本片只取当前limit并核当前算术，绝不要求历史request仍满足今天的模型scope、区域、IP、过期/撤销或速率配置。request冻结的售价/模型/profile/policy与PB字段需要各自完整历史图；本片R公式不能代替这些图。没有扫描的historical membership/role/limit版本无法从当前users/memberships恢复。

settlement的原始usage、最终选择的finalization、真实交付/截止时刻、提案审批时的权限与财务幂等不在投影；不能由raw=C+waived或journal一致推出真实供应商用量/外部扣款。api_keys.counter和refund_orders没有完整不可变版本链；本片可发现当前图与累计不一致，不能从当前行证明未曾被非法修改，也不证明source与journal同事务生命周期。静态catalog/权限证明、历史对象完整性、全ledger、audit/outbox、sandbox/采购/PB图、C0 lot/FIFO/legacy-cutover与完整共享组合记录仍分别待核。

本并行片只拥有本文和未来经根授权的 `internal/fundinglotverify/customer_key_graph*.go`；不修改共享reader/DTO、README、架构或manifest，不自行运行refresh。根汇合并复核全部结果后按AGENTS完成统一架构更新、验证与推送。

## 8. 纯 core 实施与限定验证

已实现 [customer_key_graph.go](../../internal/fundinglotverify/customer_key_graph.go) 与 [专项测试](../../internal/fundinglotverify/customer_key_graph_test.go)，直接复用根声明的共享 FundingGraphJournalRow；没有新依赖、数据库、writer、HTTP、worker 或资金调用。十组输入共享资源界；主键及自然唯一键按首次行保留，duplicate不参与金额放大。金额和关系矛盾为局部mismatch，执行错误零结果。

2026-11-02：完整 package 的定向 `go test -race ./internal/fundinglotverify -run '^TestCheckCustomerKeyGraph' -count=1` 通过，包1.516s；`go vet ./internal/fundinglotverify` 和 `git diff --check` 通过。七个顶层测试覆盖本合同列明的正常/反例，包括九种release reason、四种零金额状态、NUMERIC100、BIGINT及其累计超界、ceil、多笔approved/合法超额pending、refund不减spent、源与journal的正反向矛盾、零分录集合、十六种主键/typed唯一键去重、任意/空TEXT及NULL、共享行/字节精确边界和中途/末尾取消与超时零摘要。

此前同伴未闭合代码时的两次package命令编译失败，另一次显式文件集合的非race测试仅用于局部调试；它们不作为上述完整package定向通过的替代。未执行或声称reader/session、真实PG、Permit/维护、全源或商业/生产验收；这些由根的新组合片分别验证。独立core审阅和根架构汇合另行记录，不由本片自行refresh。

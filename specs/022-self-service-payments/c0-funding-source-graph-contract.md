# 022-C0 人工核款与兑换券资金来源全关系图（根冻结纯 core）

2026-11-03 tenant 身份校准：本合同中的 tenant 主键及真正指向它的外键按[受保护事务第6节](c0-protected-init-phase-contract.md#6-验收与交付)接受合法旧 TEXT（含空串），并保留原字节/行预算、精确关联及 NULL/空串区别；旧泛称“必需身份非空”不再涵盖这些 tenant 字段。其他对象 ID、非 tenant 引用、UUID、金额/状态与在线调用合同保持。PB 的 tenant target/key configure scope、Sandbox 按固定 kind 选择 scope 亦以该校准为准。

2026-11-02。根复核§3/7的021 payment精确分派、§6时钟边界和§9正式writer/fixture差异后，冻结并授权独立纯 core。它在既有 [三表来源关联](c0-source-link-contract.md) 之外，覆盖 manual/voucher 的原始来源、批次与撤销、commerce terminal、receipt、资金 journal 的精确两行、audit 与 credited outbox；固定局部 scope 为 `manual_voucher_funding_graph_v1`。这里的“全关系图”只指本合同封闭的两类来源关系，不等于完整 FundingSources、SandboxFacts、钱包来源/退款/lot 重放、CoverageComplete、basis、epoch、RuntimeProof 或维护资金写许可。

依据 [总规格](spec.md)、[A 领域](a-domain-contract.md)、[A 合同](a-contract.md)、[独立核验候选](c0-verifier-contract.md) §9–11、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)。代码事实来自 [人工核款](../../internal/billing/orders.go)、[共同入账](../../internal/billing/funding.go)、[六类券命令](../../internal/billing/voucher_commands.go)、[journal/audit/outbox 写入](../../internal/billing/store.go)、[002](../../migrations/002_billing.sql) 与 [017](../../migrations/017_funding_vouchers.sql)。SQL guard 是研究材料，不是新核验的业务 oracle；不调用 `funding_journal_valid`、旧 `verifyFundingTx` 或写侧函数，不修改已应用迁移。

## 1. 固定纯 API 与责任边界

```go
func CheckFundingSourceGraph(context.Context, FundingSourceGraphRows) (FundingSourceGraphCheck, error)

type FundingSourceGraphRows struct {
    Users []ProcurementApprovalUserRow
    Tenants []ProcurementAttemptTenantRow
    Accounts []LedgerAccountRow
    Journals []FundingGraphJournalRow
    Lines []LedgerLineRow
    Audits []ProcurementAuditRow
    Outbox []ProcurementOutboxRow
    Receipts []FundingGraphReceiptRow
    Credits []FundingGraphCreditRow
    Redemptions []FundingGraphRedemptionRow
    Batches []FundingGraphBatchRow
    Vouchers []FundingGraphVoucherRow
    Revocations []FundingGraphRevocationRow
    Decisions []FundingGraphRevocationDecisionRow
    Commands []FundingGraphCommerceCommandRow
    CommerceReceipts []FundingGraphCommerceReceiptRow
}
```

根在独立 shared DTO 文件唯一声明 `FundingGraphJournalRow`：`ID,TenantID,OperationID,Kind,Reference,Actor,Currency,CreatedTXID string; CreatedAt time.Time`。原 `LedgerAccountRow/LedgerLineRow`、`ProcurementAuditRow/ProcurementOutboxRow` 只复用原始数据形状，不从已有摘要接受可信结论；Users/Tenants 只含 ID，不读当前角色、启停状态或邮箱。

本片独占新增 DTO：

```go
type FundingGraphReceiptRow struct {
    ID,TenantID,SourceKind,SourceID,Amount,Currency,JournalID string
    CreatedAt time.Time
}
type FundingGraphCreditRow struct {
    ID,TenantID,BusinessRef,Amount,State,Requester,ProofRef string
    Approver,JournalID *string
    CreatedAt time.Time
}
type FundingGraphRedemptionRow struct {
    ID,TenantID,VoucherID,BatchID,OperationID,Actor,Amount,JournalID,FundingReceiptID string
    CreatedAt time.Time
}
type FundingGraphVoucherRow struct { ID,BatchID string; CreatedAt time.Time }
type FundingGraphBatchRow struct {
    ID,State,FaceAmount,Count,PurposeRef,Reason,Maker,Revision,RedeemedCount string
    Approver,TerminalActor *string
    ExpiresAt,CreatedAt time.Time
    ActivatedAt,TerminalAt *time.Time
}
type FundingGraphRevocationRow struct {
    ID,BatchID,OperationID,Maker,ExpectedRevision,ExpectedRedeemedCount,Reason string
    CreatedAt time.Time
}
type FundingGraphRevocationDecisionRow struct {
    ID,ProposalID,BatchID,OperationID,Approver,Status,Reason string
    DecidedAt time.Time
}
type FundingGraphCommerceCommandRow struct {
    OperationID,Actor,Scope,Kind,RequestDigest string
    FingerprintKeyVersion *string
    CreatedAt time.Time
}
type FundingGraphCommerceReceiptRow struct {
    OperationID,Kind,Scope,Actor,Status,Reason string
    ResourceID,Amount,FundingReceiptID,JournalID *string
    CreatedAt time.Time
}
```

`FundingSourceGraphCheck` 固定含 `Scope,Scanned,ScopedGraphConsistent,Mismatches,UncoveredSandboxReceipts`，计数为 uint64、无业务 ID/金额/原文回显。Scanned 固定对应上述十六组名字，统计全部输入。`ScopedGraphConsistent` 当且仅当本合同的 mismatch 全零；sandbox 仅按公共 receipt shape 检查及完整计数，不因它存在将两来源局部关系说成全来源通过。本片不输出 `Balanced/CoverageComplete/Status=balanced`，不制造外部可装配的完成记录或 proof。

纯 core 不读数据库、文件、时钟或网络，不取锁、Begin/Commit、重试、修账或创建秘密。root 独占同 lease 的 owned raw union reader 和维护入口；本片不改其已有 reader、shared DTO、README 或统一架构，不单独 refresh。

## 2. 固定完整读取投影

以下十六张表都从自身出发完整枚举；列按顺序保留 nullable 与原始 BIGINT 文本。不得以 tenant/state/kind 过滤、JOIN 隐去孤儿、SQL aggregate 或 writer predicate 代替原始行。root 可与 Key/ledger 等 core 共享同一 owned 输入，但不能重复开快照或用局部摘要替代本片原始行。

| 表 | 固定 SELECT 列顺序 | C 序 keyset |
| --- | --- | --- |
| users | `id` | id |
| tenants | `id` | id |
| ledger_accounts | `id,tenant_id,kind,balance::pg_catalog.text` | id |
| journals | `id,tenant_id,operation_id,kind,reference,actor,currency,created_txid::pg_catalog.text,created_at` | id |
| journal_lines | `id,journal_id,account_id,signed_delta::pg_catalog.text` | id |
| audit_events | `id,actor,tenant_scope,action,target,sanitized_diff::pg_catalog.text,request_id,created_at` | id |
| projection_outbox | `event_id,tenant_id,operation_id,kind,schema_version,sanitized_payload::pg_catalog.text,created_at` | event_id |
| funding_receipts | `id,tenant_id,source_kind,source_id,amount::pg_catalog.text,currency,journal_id,created_at` | id |
| credit_orders | `id,tenant_id,business_ref,amount::pg_catalog.text,state,requester,approver,proof_ref,journal_id,created_at` | id |
| voucher_redemptions | `id,tenant_id,voucher_id,batch_id,operation_id,actor,amount::pg_catalog.text,journal_id,funding_receipt_id,created_at` | id |
| voucher_batches | `id,state,face_amount::pg_catalog.text,count::pg_catalog.text,expires_at,purpose_ref,reason,maker,approver,activated_at,terminal_actor,terminal_at,revision::pg_catalog.text,redeemed_count::pg_catalog.text,created_at` | id |
| vouchers | `id,batch_id,created_at` | id |
| voucher_revocations | `id,batch_id,operation_id,maker,expected_revision::pg_catalog.text,expected_redeemed_count::pg_catalog.text,reason,created_at` | id |
| voucher_revocation_decisions | `id,proposal_id,batch_id,operation_id,approver,status,reason,decided_at` | id |
| commerce_commands | `operation_id,actor,scope,kind,request_digest,fingerprint_key_version,created_at` | operation_id |
| commerce_operation_receipts | `operation_id,kind,scope,actor,status,reason,resource_id,amount::pg_catalog.text,funding_receipt_id,journal_id,created_at` | operation_id |

**不读** vouchers.code_digest/digest_key_version、券码/secret/HMAC keyring、financial_idempotency 的 Key/RequestHMAC 或任何身份秘密；manual 原始来源链不以 financial idempotency 为事实 oracle。policy/memberships/current user role 等 mutable 当前值不用于倒推历史资格。

reader 仍按 [ReadLease](c0-read-lease-contract.md) 使用原总期限、128 行页、显式 `COLLATE "C"` 的严格递增 keyset；独立 hasCursor 保留合法空 TEXT 起点。完整消费、Scan、Rows.Err、Close 后短页才 EOF；整128行仍续页。十六组与同 union 的其它投影共用100000行/16MiB原始 UTF8 字节硬界，不按表/core/页重置，不重复计算共享物理投影。任何查询、解码、重复/倒序页、Close/Err 或 EOF 完成失败返回整个零结果，不变成 indeterminate 或通过。

## 3. 词法、数值、身份与重复

所有投影 string 有效 UTF8 且无 NUL；必需身份非空，SQL NULL 与非NULL空字符串不同。users/tenants/credit/journal/account/line/audit/outbox 的已有 TEXT 身份按原值比较，不强改为 UUID。017 新增自有 ID（receipt、batch、voucher、redemption、revocation、decision）和 commerce operation 必须小写规范、非全零 UUID；commerce 的 `tenant:` 后缀也依017为同样 UUID。手工 credit 的 tenant/user 不因此强转 UUID；voucher command 的 actor 由 writer 明确要求同样 UUID。

金额/数量/revision 使用规范 int64 十进制字面值，拒空白、加号、前导零、负零、小数、指数和溢出。金额正；count 1..100；redeemed_count 为0..count；revision/expected_revision 正；expected_redeemed_count 非负。`face_amount*count` 用 big.Int 核 ≤MaxInt64。journal.created_txid 规范正 int64，只核原始形状，不与当前核验事务 xid 比较；账户余额和所有 line.signed_delta 允许 signed int64。本片不重算整个账本账户余额，后者由独立 Ledger 域负责。

所有本片自有时间及相关资金 journal/audit/outbox 时间必须有限（年1..9999）且微秒精度；不能用 Go 零 time 代表 SQL NULL。无关 journal/audit/outbox 只做共同词法/主键及资源检查，不额外解释它们的时间/JSON/业务身份；nullable 无值保留 nil。voucher purpose_ref 1..256字节、reason 1..200字节且无控制字符；batch.reason 与 revocation.reason 分别保留，不要求后来撤销理由等于最初批次理由。manual business_ref/proof_ref 按旧 TEXT 保存，只要求必需非空合法编码，不把当前输入验证器追溯套在历史值上。

每表主键按类型分别去重；身份合法的首行入索引，多余行只记 duplicate，不替换首行或放大后续关系遍历。无效主键不入索引。自然唯一键另按 typed tuple 去重：receipt `(source_kind,source_id)`/journal；credit business_ref/非NULL journal；redemption voucher/operation/journal/receipt；revocation operation；decision proposal/operation；五种非redeem voucher accepted commerce `(kind,resource_id)`；相关 audit `(action,target)`；outbox 全表 `(operation_id,kind)`（无关kind允许空operation，仍纳唯一性）。021已把accepted resource唯一索引限于五种券管理动作，**不能对payment动作强加同样自然键唯一性**。不同表相同文本主键合法。

## 4. manual 来源与精确资金效果

credit tenant/requester 必须存在，approver 有值时也必须存在且不同 requester。pending 必须 approver/journal 均NULL，并且没有 manual receipt、credit journal、credited outbox 或 approved audit；created audit 仍必须恰一条。approved 必须 approver/journal 非NULL非空，并且恰一 receipt：`source_kind=manual_credit, source_id=credit.id`，tenant/amount/journal 完全一致、currency=CNY。

每个 approved credit 的 journal 必须唯一，精确为 tenant=credit.tenant、operation=credit.id、kind=`credit`、reference=credit.id、actor=credit.approver、currency=CNY。每个这类 journal 恰两行：同 tenant 的 AVAILABLE `+amount`，tenant NULL 的 FUNDING_CLEARING `-amount`；account ID 不同，不接受第三条零行、额外抵销行、其它清算科目、跨tenant AVAILABLE 或非NULL平台清算 tenant。其两 account 都必须存在，账户 `(tenant,kind)` 唯一，AVAILABLE tenant 还必须存在。金额比较/取负不经 float。

每个 approved credit 恰一个 `credited` outbox：operation=credit.id、tenant=credit.tenant、schema1、payload 恰 `{"amount":"<amount>"}`。每个 credit 恰一个 `credit.created` audit，approved 另恰一个 `credit.approved` audit：target=credit.id、tenant_scope=credit.tenant（非NULL）、request_id 为空、actor 分别 requester/approver、diff 恰 `{"amount":"<amount>"}`。不将人工核款解释为真实银行收款证明；proof_ref 是受控引用声明，不读取库外文件。

## 5. voucher 批次与状态图

每张 voucher 指向真实 batch；batch.maker、非NULL approver/terminal_actor 都引用 users。Go 从独立完整 vouchers/redemptions 逐行计数：voucher 数量恰 batch.count，redemption 数量恰 redeemed_count，每张 voucher 最多一次 redemption，redemption.batch_id 精确等 voucher.batch_id。不得仅比较两个 SUM 而放过替换或孤儿。

| batch 状态 | 固定 nullable/双审/最终版本形状 | 必须的 accepted commerce 与审计 |
| --- | --- | --- |
| proposed | approver/activated/terminal_actor/terminal_at 全NULL；revision=1；无redemption | create |
| active | approver非NULL且≠maker，activated非NULL；terminal二项NULL；revision=2 | create + approve |
| voided | approver/activated NULL；terminal_actor=maker、terminal_at非NULL；revision=2；无redemption | create + void |
| revoked | approver非NULL且≠maker、activated非NULL；terminal二项非NULL；revision=3 | create + approve + 恰一 accepted revocation decision |

每批 `expires_at` 在 `created_at+[1小时,365天]` 闭区间；这是创建 writer 将同一个显式 now 存入 batch.created_at 后验证的合同，不是猜跨事务时序。active/revoked 的 activated_at 必须 `< expires_at`，因为批准 writer 用同一个明确 now 验证后写入。不得再要求 activated_at>=created_at，或 voucher.created_at>=batch.created_at。

撤销 proposal 引用真实 batch、maker/user、独立唯一 commerce operation；其 expected_revision 必须2、expected_redeemed_count 在0..count且不大于最终 redeemed_count，原 state 为 active 的顺序由受审状态机/来源图表达，不能要求它等于后来当前 revision/count。每个 proposal 必须有一 accepted `voucher.revocation.create` command/terminal，actor=maker、resource=proposal.id。proposal 可以尚无 decision；不能伪造待决为已批准。

decision 引用同 proposal/batch，approver 存在且≠proposal.maker，operation 唯一且与 proposal operation 不同；恰一个相同 actor/scope 的 `voucher.revocation.approve` command/terminal。accepted 只能 reason=accepted，batch 必须 revoked 且 terminal_actor=approver、revision=proposal.expected_revision+1、redeemed_count=proposal.expected_redeemed_count，accepted terminal.resource=proposal.id；每 revoked batch 反向恰一个这样的 accepted decision。rejected 只能 reason=`voucher_evidence_stale`、terminal rejected/null效果；不从后来当前 count/revision 证明当时为什么 stale，也不拿当前 batch 恰好仍相同来重写当时决定。

## 6. voucher 兑换与精确资金效果

每 redemption 的 tenant/actor 存在，voucher/batch 存在并精确同批，最终 batch 状态只可 active/revoked，amount=batch.face_amount。恰一 receipt：source_kind=`voucher_grant`、source_id=voucher.id、id=redemption.funding_receipt_id；tenant/amount/journal 相同、currency=CNY。`redemption.created_at==receipt.created_at` 必须成立：两者是 ApplyFundingTx 明确共用的 funding now。并核 `activated_at<=该now<expires_at`，不能拿 command.created_at 或 journal.created_at 替代真正 eligibility instant。

journal 精确为 tenant=redemption.tenant、operation=redemption.id（**不是 commerce operation**）、kind=`voucher_grant`、reference=voucher.id、actor=redemption.actor、currency=CNY；恰两行同 tenant AVAILABLE `+amount` 与 NULL tenant OPERATING_GRANT_CLEARING `-amount`。恰一 credited outbox，operation=redemption.id、同tenant/schema1/精确 amount 字符串。恰一 `voucher.redeemed` audit，target=redemption.id、actor=redemption.actor、tenant_scope=redemption.tenant（非NULL）、diff 恰 `{"amount":"...","funding_receipt_id":"..."}`。

redemption.operation_id 指向恰一 `voucher.redeem` commerce command/terminal：actor=redemption.actor、scope=`tenant:`+redemption.tenant、accepted/accepted、resource=redemption.id、amount/funding_receipt_id/journal_id 精确同来源。反向所有 accepted redeem terminal 都必须有这条 redemption，不能据正确 receipt 就放过额外 terminal 或孤儿兑换。

批次被后来 revoke 不抹掉合法既有兑换。不要求 redemption.created_at<=terminal_at，也不要求任何 journal/audit/outbox 与 source 的取时大小/相等：独立 `clock_timestamp()` 可回拨。撤销前的业务顺序由最终 revoked 状态、原双审决策、计数和受审不可变/锁状态机表示；这里不捏造历史单调时钟或新增事件排序字段。该设计明确细化旧候选 §11.2 的“撤销前”，不复用现 SQL guard 的跨取时时间不等式。

## 7. commerce 双向终态与 audit 固定合同

commerce两表仍完整读取，**本节业务规则只对六种精确voucher kind分派**：`voucher.batch.create,voucher.batch.approve,voucher.batch.void,voucher.revocation.create,voucher.revocation.approve,voucher.redeem`。021另有合法 `payment.sku.publish,payment.sku.activate,payment.order.create,payment.order.cancel`，全部保留原始计数、共同UTF8/NUL/必需主键/非空kind与资源预算；保留command.actor→users和terminal `(operation_id,kind,scope,actor)`→command 的通用外键核验，但不按voucher scope/fingerprint/status/reason/资源NULL矩阵/时间/audit或业务源图解释payment。payment command缺terminal的业务完整性属于独立支付图，不以本片宣布通过或不符；不得在SQL中提前过滤它们。其它未知kind计InvalidEnumRows；任何voucher源引用到payment command/terminal仍是确定错kind。

每voucher command 必须有恰一 terminal，反向无孤voucher terminal；operation/kind/scope/actor 精确一致且 `terminal.created_at==command.created_at`（writer 显式复制，不是时间猜测）。在这六种券动作中，redeem 独占 tenant scope 与非NULL fingerprint_key_version（1..32 ASCII `[A-Za-z0-9_-]`），另外五种必须 platform 与 NULL version；request_digest 为64位小写hex，仅核形状。voucher actor 存在且规范UUID；voucher tenant scope 对应存在tenant。共同commerce.operation_id仍按017的规范UUID主键读取。

voucher accepted 必须 reason=accepted/resource非NULL；redeem 才有正 amount/funding/journal 三项，其它五种的这三项全部NULL。voucher rejected 必须 resource/amount/funding/journal 全NULL，reason 依下列 writer 实际分支封闭：

| kind | rejected reasons |
| --- | --- |
| voucher.batch.create | vouchers_disabled, voucher_state_conflict |
| voucher.batch.approve / voucher.batch.void | voucher_revision_conflict, voucher_state_conflict |
| voucher.revocation.create | voucher_revision_conflict, voucher_state_conflict, voucher_evidence_stale |
| voucher.revocation.approve | voucher_state_conflict, voucher_evidence_stale |
| voucher.redeem | vouchers_disabled, voucher_unavailable, funding_amount_overflow |

accepted 非redeem voucher terminal 必须反向对应§5正确状态/actor 的 batch/proposal/decision；每个应有券源动作恰一对应 terminal。rejected voucher command 不允许以其 operation 拥有 redemption/proposal/accepted decision；唯一允许持久业务效果是 `revocation.approve + voucher_evidence_stale` 对应恰一 rejected decision。其它拒绝不捏造 audit；用户未通过权限、not_found、key不可用等执行错误不是可持久 rejected reason。

| audit action | target / actor / 精确 diff |
| --- | --- |
| voucher.batch.created | batch.id / maker / `face_amount:string,count:JSON integer,purpose_ref:string` |
| voucher.batch.approved | batch.id / approver / `maker:string` |
| voucher.batch.voided | batch.id / maker / `reason:string`（1..200字节安全文本；原void reason没有其它持久列可比较） |
| voucher.revocation.created | proposal.id / proposal.maker / `batch_id:string,reason:string`（精确等proposal） |
| voucher.revocation.approved | proposal.id / accepted decision.approver / `batch_id:string` |
| voucher.redeemed | 见§6 |

前五种平台 audit 的 tenant_scope 必须为**非NULL空字符串**：当前 audit writer 原样写入传入的 `""`，并没有 NULLIF。voucher request_id 任意合法UTF8/无NUL文本（允许空），command未保存它，不能伪造跨表相等要求。每源恰一对应 audit；反向每条上述 audit 都必须找到正确 source/action/状态/actor/scope/diff。rejected decision 没有批准 audit，pending proposal 也没有批准 audit。`voucher.policy.update` 与其它模块 action 不属于本片动作，完整计数/词法/主键后保留为无关，不据当前策略或孤立 policy audit 推断授权链。

JSON 按闭合语义对象解析，不依赖 JSONB 键序/空白；除 batch count 必须规范 JSON integer，Money 与各身份全部 string。拒缺/多/重复键（含转义后重复）、null、错类型、尾JSON、非法UTF8和孤立UTF16 surrogate。合法unicode转义可接受。非相关 audit/outbox 不解析 payload，不因其其它schema_version/actor/tenant空值误报。

## 8. 反向扫尾、计数和执行错误

正向来源完整后，必须独立从以下实际集合回扫，不靠“某条正确边 EXISTS”跳过额外行：

1. 每 manual/voucher receipt 对应唯一正确 credit/redemption；pending effect、孤 receipt、错tenant/金额/引用为不符。sandbox receipt 只检查公共shape并计 uncovered；未知source kind为不符。
2. 每 `credit`/`voucher_grant` journal 归属恰一相应 receipt/source；每使用 FUNDING_CLEARING/OPERATING_GRANT_CLEARING 的 line 都归属于这类正确的精确两行 journal。两清算科目不能出现在其它钱包kind；本片不将 SANDBOX_FUNDING_CLEARING 或其它钱包 AVAILABLE/HELD line 当作这两来源孤儿。
3. 每 `credited` outbox 要么唯一匹配本片 manual/voucher 的 journal/source，要么由一个公共shape正确的 sandbox receipt 与 kind=sandbox_capture 的 journal operation 识别为明确未覆盖；后一种只排除误报，不核完整支付图或金额 payload。其余 credited 为 orphan；不能把无法关联的消息直接归为 sandbox。
4. 每 voucher/batch/proposal/decision/redemption、voucher commerce command/terminal 及本片固定 audit action，都回到正确父和唯一业务动作；反向缺父不被 JOIN、计数或前向索引丢掉。payment command/terminal依§7只做共同原始核验和通用FK，不套用voucher来源与反向业务扫尾。

Mismatches 固定字段（uint64）：`DuplicatePrimaryRows,DuplicateReceiptSourceRows,DuplicateReceiptJournalRows,DuplicateCreditBusinessRefRows,DuplicateCreditJournalRows,DuplicateRedemptionVoucherRows,DuplicateRedemptionOperationRows,DuplicateRedemptionJournalRows,DuplicateRedemptionReceiptRows,DuplicateRevocationOperationRows,DuplicateDecisionProposalRows,DuplicateDecisionOperationRows,DuplicateAcceptedResourceRows,DuplicateAuditEffectRows,DuplicateOutboxEffectRows,InvalidTextRows,InvalidTimeRows,InvalidNumberRows,InvalidEnumRows,InvalidJSONRows,IdentityRows,CreditShapeRows,CreditReceiptRows,RedemptionReceiptRows,ReceiptSourceRows,FundingJournalRows,FundingLineRows,FundingJournalSourceRows,ClearingLineSourceRows,FundingOutboxRows,OutboxSourceRows,FundingAuditRows,AuditSourceRows,BatchShapeRows,BatchVoucherCountRows,BatchRedemptionCountRows,VoucherBatchRows,RedemptionEligibilityRows,RevocationRows,DecisionRows,CommerceShapeRows,CommerceTerminalRows,CommerceSourceRows,UnknownReceiptKindRows`。

主键/自然键 duplicate 按每个多余原始行计一次；首次合法原行保留。InvalidText/Time/Number/Enum/JSON 分别每源行每类至多一次；IdentityRows 每源行引用身份组有任一缺失/跨tenant至多一次。每个其余关系字段按名称对应的应有源或实际反向行至多一次；同一坏边允许正向缺失/不符与反向孤立各计一次。一个 journal 有多种 line 错误时 FundingLineRows 按 journal 计一次，ClearingLineSourceRows 按实际 line 计。batch 两类计数分别每batch至多一次；不是按缺失券的假想数量膨胀。Scanned 永远包含重复/坏原行；完整 sweep 不在首个 mismatch 提前返回。

全部投影共用100000行与16MiB原始 string 字节；nullable非NULL也计，派生JSON对象/重复索引不重复计原文。不宣称这是RSS或PostgreSQL排序资源上限。先做全量资源预算再建索引；工作每256步核父context、结束再核。sentinel 为 `ErrFundingSourceGraphRowsInput/Resource/Canceled/Timeout`；nil context为Input，预算超限Resource，取消/到期分别Canceled/Timeout，全部执行错误返回零check。数据不符正常返回固定scope/完整scanned和计数，不能变成执行错误吞掉坏事实，也不回显原数据/SQL/private cause。

## 9. 历史可证性与不得外推的结论

本片能独立证明当前完整持久图的双人身份分离、必要用户/租户存在性、状态/终态、显式兑换时点资格、精确资金两行与唯一audit/outbox。它不能从当前 users/status/role、membership/owner 或 voucher_policy.enabled 证明历史当时授权；不读这些当前字段当oracle。旧schema没有授权快照/策略时间线、原command请求body/target/request_id，也没有保存券秘密；因此不能历史重算 request_digest、重验券码/HMAC、还原 rejected 输入目标或证明拒绝理由对应的当时条件。这些是本局部graph已明示排除的真实性承诺，**不是暗示已经恢复了历史证明，也不补造当前快照**。

017人工 receipt 回填沿旧approved credit和journal生成，receipt.created_at=journal.created_at；新manual receipt用独立 funding now，无持久标志可区分二者，故本片不添加二者时间相等/先后判据。旧人工created/approved audit 是本合同应有持久关系：缺失计确定mismatch，不能用“可能很旧”豁免。source未持久 top-xid，故 journal.created_txid 形状正确不等于source同事务已证；此职责留给后续精确生命周期/C0维护成员边界。

`internal/billing/funding_integration_test.go` 的私有 `fundingVoucher/fundingRedeem` 明确声明“A0 internal owner fixture，不是A1 issuance API”，它省略batch创建/批准audit、只写amount的redeemed diff，terminal取默认created_at也未复制command时刻。它只用于共同helper/DB合同测试，**不是正式Service可接受历史的另一种schema或writer版本**。本片精确要求正式 [VoucherService](../../internal/billing/voucher_commands.go) 的`amount+funding_receipt_id`及完整管理audit/显式时间相等；不得为了让A0 fixture通过弱化合同，亦不得以fixture不符宣称生产历史已存在损坏。新的真实PG来源图正例必须走正式Service或逐字段完整的专属受控fixture。

本片不证明被DB owner任意伪造前的真实性、库外proof_ref/银行/PSP/供应商事实、投影消费状态或reports、客户请求/Key/采购/PB关系、其它资金来源、cutover成员、allocation顺序或原路退款资格。不因当前单图一致发行全源proof；未来组合要另行冻结这些域并在同session完成，不能把本片summary当外部trusted参数。

## 10. 验证门槛

纯core覆盖：pending/approved、多tenant、旧TEXT手工身份、人工回填时间差；四批次状态、合法旧兑换后撤销、待决/accepted/rejected stale撤销、多提案历史；全部六命令/拒绝矩阵与NULL字段、command/terminal显式时间相等；四种payment动作与合法重复payment resource并存时不误套券矩阵；source/journal/两行/清算科目/receipt/audit/outbox/commerce全部正反孤儿、错身份/金额/操作ID/跨tenant/额外零分录；Money字符串/JSON整数count/重复转义键/孤立surrogate/多余字段；各主键/自然键重复与首行策略；空集/sandbox未覆盖/无关合法模块；时钟回拨与合法不同取时；共享行/字节边界/取消/到期零结果。

root reader需另验完整分页128/129/EOF、所有Next/Scan/Err/Close失败、共享union预算/原lease期限及固定投影；真实PG以原API生成正例与受控坏历史负例核对，读取前后不写业务事实。纯core race/vet通过不代替reader/真实PG/商业/供应商/生产验收；最终共享架构与快照由root汇合集成复核。

## 11. 已实施纯 core 与限定验证

实现为 [纯API/原始DTO/预算/JSON](../../internal/fundinglotverify/funding_source_graph.go)、[独立关系核验](../../internal/fundinglotverify/funding_source_graph_checks.go) 和 [专项测试](../../internal/fundinglotverify/funding_source_graph_test.go)。本片只依赖标准库及同包既有纯DTO/严格文本、整数、Unicode与有限时间原语；没有billing writer、DB、provider、HTTP或维护能力import。整数宽乘积前先拒绝非规范/超长int64字面，避免把原始长文本直接送入big.Int。

2026-11-02最终定向 `go test -race ./internal/fundinglotverify -run '^TestCheckFundingSourceGraph' -count=1` 通过，包1.959s；`go vet ./internal/fundinglotverify`、`git diff --check`通过。八个顶层测试含§10的正常/反例，以及六命令全部可无业务效果的拒绝reason矩阵（stale批准决策沿完整批次/提案图独立验）、MaxInt64、全零新UUID拒绝、16MiB精确原文/nullable字段预算、共享100000行、预算和关系索引期间取消/到期零结果。时间回拨正例刻意让receipt/journal/audit/command不按物理取时排序，并让后来revocation.terminal_at早于既有redemption时钟值；只保留真正显式资格时点与复制等式。

并行 CustomerKey 片的独立只读review未发现阻塞，重点核了16组预算先于索引、四种payment不套voucher矩阵/accepted资源唯一性、精确两行与clearing反扫、闭合JSON/空operation typed pair、主键首行和自然键歧义、显式now及非单调时钟边界。它没有修改本片或代跑真实PG。本片未执行reader/Permit/商业/供应商/生产验收，不据此发行完整资金来源证明，也没有刷新架构快照；根汇合后继续独立接线与验证。

# 022-B2 共同账务第三来源与应用迁移 021 精确合同

2026-11-01，**根已结合独立静态审查冻结本B2合同；实施授权以根指定文件所有权为准，已追加021并实现第三来源生产代码/十方向核验/三角色权限，专项真实PG通过；B3领域/worker/HTTP未接线**。本片仅定义应用 `021_payment_sandbox.sql`、共同账务 `sandbox_capture` 分支、资金核验/重建/来源摘要兼容及独立支付数据库角色。承接 [B1 合同](b1-contract.md)、[B1 SQL 原型](b1-core.sql)、[B1 权限原型](b1-permissions.sql)、[A0/A1](a-contract.md) 和 [B 总合同](b-contract.md) §8。B1 是随机 schema 的已验证设计原型，不是可直接安装的生产 migration。

本轮已逐项对照 [funding.go](../../internal/billing/funding.go)、[funding_verify.go](../../internal/billing/funding_verify.go)、[账户锁/写分录](../../internal/billing/store.go)、[VerifyLedger/投影](../../internal/billing/query.go)、[Rebuild](../../internal/billing/rebuild.go)、[来源查询](../../internal/billing/voucher_query.go)、[来源 DTO](../../internal/billing/voucher_types.go)、[ProvisionFor](../../internal/operations/operations.go)、[有效角色检查](../../internal/operations/alert_roles.go)、[017](../../migrations/017_funding_vouchers.sql)、[迁移执行器](../../migrations/embed.go)、[HLD](../../docs/design/HLD.md) 与 [LLD](../../docs/design/LLD.md)。统一架构由根在并行结果汇总后更新、语义复核及 refresh；本文不把目标能力记为已实现。

## 1. 实施切片、前置条件与现有接缝

根审定本稿后，才可另行授权下列同一个集成切片；本稿不授权 `main`、HTTP、UI、真实 PSP、C 的来源 lot/原路退款或其他账务重构。`021` 是本轮全局保留号，前置为现有 001–019 及根保留的 020 周期预算迁移；不得自行抢号、改写旧 SQL，亦不得将 023 的应用接线并入此文件。

| 当前源码事实 | 本片必须同步解决的边界 |
| --- | --- |
| `ApplyFundingTx` 只识别 manual/voucher，账户后复验与 receipt 前各有一个 `else` | 改成三个显式分支；未知 kind 一律拒绝。sandbox 不读取券/人工审批表、不创建 redemption、不更新 credit order |
| `postLocked` 通用调用当前以 source ID 作 journal reference | sandbox 必须单独使用 capture ID 作 operation、order ID 作 reference；不能误写成 capture ID/reference |
| `lockAccounts` allowlist 与默认集合均为旧五科目 | allowlist 增加 sandbox clearing；B 显式只请求 AVAILABLE 和新 clearing；默认五科目及 A/推理原锁集合保留，避免扩大无关路径的账户锁 |
| `funding_journal_valid(text)` 已被 017 约束/调用引用 | 原函数使用 `CREATE OR REPLACE` 原位扩展；保留 OID/签名，不执行 B1 原型的 RENAME |
| `VerifyFunding` 的 journal 集合/清算 CASE 只支持两种 source | 所有方向显式加入第三来源；新增 identity/selection 计数，不能落入 voucher 的 ELSE |
| `ProvisionFor` 先 blanket grant，B1 permissions 又要求已有 marker | 通用 provision 无 marker 仍完成 app/projector 收紧；worker 另在离线 marker 后精确 provision，重复通用 provision 不能重新开放 B 写权 |
| A 的 HTTP DTO 与前端 parser 都是严格两来源/五字段摘要 | B3 同版扩展严格 parser、筛选及来源提示后，才可发布有实际 sandbox 资金的服务。库/SQL 测试通过不等于旧 UI 已兼容 |

## 2. 追加迁移：保留原函数身份

目标文件为 `migrations/021_payment_sandbox.sql`，只在另行授权后追加到应用迁移集。仍使用既有 `migrations.Apply/Verify` 的完整 checksum 集与事务执行器；不加运行时自动 DDL、不加宽松旧 schema 开关、不修改 017/020 的已应用内容。B0 mock/receiver 自有迁移、schema、角色和 manifest 保持独立，绝不能拿来充当应用支付表。

迁移在受控维护/QA 升级窗口执行。先以升级前两来源规则逐方向核验合法 A source/receipt/journal/credited/clearing、账户余额缓存与历史分录，以及已有 commerce 唯一性；有不一致则整个 021 回滚，不以删历史、补一笔流水或跳过校验继续。既有合法 A 数据不得新增 receipt、journal、credited、audit 或重算余额。本迁移不建立 marker、不生成 nonce/key/角色/环境目录、不连接 PSP；新 policy 固定 `enabled=false, revision=1`，其余 B 业务表为空。

生产 SQL 从 B1 逐项移植，不整体执行设计文件。B1 的 `p1_` 新辅助函数统一采用 `payment_` 前缀，例如 `p1_identity_valid`→`payment_identity_valid`、`p1_capture_source_valid`→`payment_capture_source_valid`、`p1_sandbox_journal_valid`→`payment_sandbox_journal_valid`；触发器、CHECK、索引、调用点按同一映射显式编写。原型特殊的 `p1_a_funding_journal_valid` 不通过 RENAME 得到，按下列方案另建。此命名变换不改变 B1 的金额、FSM、唯一性、身份/selection、generation、半开窗口或安全审计合同。

### 2.1 `funding_journal_valid(text)` 的精确替代方案

1. 保留现有 `funding_journal_valid(journal_ref text) RETURNS boolean` 的名字、入参名/类型及返回类型。在同一迁移事务开始记录其 OID、owner、依赖引用及原函数性质，用于升级断言；禁止 `DROP FUNCTION`、`ALTER FUNCTION ... RENAME` 或改入参名。
2. 新建 `payment_legacy_funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER`。函数体是已安装 017 的 **A 原谓词逐字静态副本**，保留 manual/voucher 的 tenant/source/金额/两条指定科目/唯一 schema1 credited 语义；不把新 kind 接到其 ELSE，不顺便收紧历史 A payload。旧函数体异常或 live 目录与预期基线不符时中止；不得从运行环境读任意函数体作为可信 expected。
3. 新建 `payment_sandbox_journal_valid(text)`，实现 B1 的完整 sandbox 逐笔谓词：capture/source→合法 identity/selection/evidence→effect→receipt→固定 journal→恰两条分录→唯一 credited→固定安全 audit→同代 processed。使用独立语句分支校验，不能把 A 与 B 写成一个 SQL `OR`，否则 worker 仍会被要求读取 A 的券/人工表。只读 source 谓词须将 selection.first_evidence 与所选 identity 的完整环境/merchant/order/交易/金额/CNY/发生时刻/revision 逐字段比较；合法 capture 原件本身不证明选中了同一交易。完整 journal 谓词还须独立重验 identity 原 evidence 的 processing_generation 和 processed/processing_review 终态，不能只验 capture 的处理代次，也不能仅信曾经运行过的 INSERT deferred trigger。通用 identity/source 预校验仍允许当前合法 leased claim，终态要求只放在完整已提交资金图证明中。
4. 对原函数执行 `CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY INVOKER`：先只读 journals 判断目标 `kind='sandbox_capture'`，是则 RETURN sandbox 谓词，否则 RETURN legacy 谓词；不存在的 journal 最终返回 false。全部新/替换函数固定实际应用 schema、`pg_catalog`、最后 `pg_temp` 的 search_path，参数化值与离线 identifier 渲染分离。
5. `funding_fact_guard()` 同样原位扩展并保留其 trigger 函数身份：journal 的资金 kind 集合变为三个；非资金 journal 触碰三种 clearing 均拒绝；source/receipt/outbox 仍调用原 `funding_journal_valid`。新 sandbox clearing 分录还要保留 B1 的反向 deferred 完整性证明。原有五类 A constraint trigger 不删除、不改为仅新资金适用。
6. 显式收回这些函数的 PUBLIC EXECUTE；app 可执行完整核验需要的 legacy/sandbox 分支，worker 仅执行 sandbox 及其必要只读/触发器依赖，projector 无支付函数执行权。触发器自身 invoker，不增加 SECURITY DEFINER 或运行时 SET ROLE 绕过表权限。原函数 ACL 随 CREATE OR REPLACE 保留的事实不能代替显式收紧与 effective grant 检查。
7. 同事务断言原 `funding_journal_valid` OID、签名、owner 和既有依赖目标不变，原 A 正反例与新 B 正反例均通过。测试中仅在原型会观察到的旧函数重命名，不能作为此门禁的替代。

迁移同时扩展 ledger kind/check、funding_receipts source CHECK、commerce kind/scope/reason 及 B1 的 partial unique/双向完整约束。原 `commerce_accepted_resource` 的保护范围必须精确保留 A 的五类非 redeem kind；新增 B activate/cancel 等可复用资源的命令使用各自 change/intent 完整证明，不粗暴删除 A 的唯一接受保护。

## 3. 固定 Go 接口与真正的来源边界

`internal/billing` 不导入 `payments`、paymentproto、server 或 PSP client；不解析签名，不构造外部支付证据。保留原调用签名和定位类型的两字段布局，已有 A 的 keyed/unkeyed literal 皆兼容：

```go
const FundingSandboxCapture FundingSourceKind = "sandbox_capture"

type FundingSourceRef struct {
    Kind FundingSourceKind
    ID   string
}
type FundingScope struct { TenantID, ActorID, CommandID string }
func (*Store) ApplyFundingTx(context.Context, pgx.Tx, FundingSourceRef, FundingScope) (FundingResult, error)
func (*Store) VerifyFunding(context.Context, string) (FundingCheck, error)
func (*Store) VerifyLedger(context.Context, Query) (LedgerCheck, error)
func (*Store) Rebuild(context.Context, string) (int, error)
```

不新增 `ApplyCapture(amount,tenant)`、`Trusted`、caller-provided verified flag、自由科目/delta 或绕过来源的充值构造器。`FundingSourceRef` 只是定位键，**不是验签 capability**；写出 `{sandbox_capture, 任意ID}` 不能创造已验证 source。真实性来自 P 验证后的持久原件/规范证据入口、独立 worker LOGIN 与 SQL 完整资金图；不声称 Go struct 能抵抗已控制整个应用或任意 `unsafe` 的进程。

sandbox 分支每次必须在调用者当前真实事务中重建来源，不能信任结构体里的任意金额、租户或外部 JSON。拒绝 nil/已结束 tx、非 READ COMMITTED、非法 capture UUID，以及 `scope.ActorID!="payment-sandbox-worker"`、非空 CommandID。读取实际 `current_user`/`session_user`、当前 DB/schema 和 marker，要求两者为同一、且正是 marker 固定 worker LOGIN；app、projector、owner SET ROLE、历史 worker 或 marker/DB/schema 不匹配不能进入本来源。app 仍保留 A 资金权限，但不能使用它提交 sandbox source/journal/receipt/credited/audit。

错误沿既有内部 billing 约定：无效 locator/kind/参数为 `invalid_funding_source`，有 source 但绑定/资金图不一致为 `funding_integrity`，已锁余额加法溢出为 `amount_overflow`。实际 SQL/连接错误保留 cause 供调用者识别已回滚的 40P01/40001，不在 helper 内重试；Processor 对外只输出固定安全码，不能暴露 SQL、schema、参数或数据库原文。查无 source、权限/marker失配、约束失败都不是可入账或可提交 review 的支付事实，必须回滚。

### 3.1 分支的完整行为

1. 从 capture ID JOIN capture/effect/selection/identity/order/原始证据/processing/marker，重做 B1 规定的全部绑定：tenant、环境、merchant、provider transaction、金额相等、CNY、capture 发生时间与 revision、精确 evidence/processing_generation、选择身份、effect 常量及冻结订单。`scope.TenantID` 必须等于派生 tenant。不得按 caller-supplied scope 单独查一份同额 source。
2. 如果 source 已有完整且通过 `funding_journal_valid` 的持久 receipt，返回原 receipt、`Applied=false`、`RedemptionID=""`，不重新加钱或 emit；不完整链是 `funding_integrity`，不能接着“补完”未知提交。新 capture 无 receipt 时，当前 processing 必须仍 leased、generation 精确匹配且租约有效；source/effect 由 Processor 已在资金 savepoint 内创建，不能由 helper 代建。
3. helper 唯一取得一次 AVAILABLE 与 SANDBOX_FUNDING_CLEARING 全账户 ID 排序锁；账户初始化余额 0。不得回头取 order/identity/tenant/user/SKU/outbox/processing 锁。账户初始化与锁查询仍分开有序语句，以 RC 新快照读取唯一冲突后的真实行；关闭/排空 pgx Batch 的既有语义保留。
4. 取得全部账户锁后只用 DB `clock_timestamp()`，再次读取并校验当前事务的完整 source/effect/selection/identity/evidence/marker 和真实 worker 身份、processing 同代租约。合法资格取冻结历史窗口 `order.created_at <= captured_at < order.expires_at`；不得用处理时 `now < expires_at`。不再要求 creator 当前 owner、tenant active、SKU 在售或支付开关仍开。
5. 从持久 source 取得正 BIGINT `wallet_credit_amount=A`，以现有 checkedAdd 对两账户计算并检查边界，再调用 `postLocked`：AVAILABLE `+A`、SANDBOX_FUNDING_CLEARING `-A`，journal kind=`sandbox_capture`、operation=`captureID`、reference=`orderID`、actor=`payment-sandbox-worker`、CNY。新 clearing 平台余额必须非正。不是人工审批或券兑换，不读 users/credit_orders/vouchers。
6. 写一份 receipt：source_kind=`sandbox_capture`、source_id=`captureID`、同 tenant/A/CNY/journal；同事务只 emit 一个 schema1 `credited`，operation=`captureID`、payload 恰 `{"amount":"A"}`。receipt/journal 的时间沿账户锁后 DB 时刻，响应金额继续 Money 的规范十进制字符串。返回 `Applied=true`、RedemptionID 空，不创建 commerce command 或伪 users。
7. helper 不 Begin/Commit/Rollback/Savepoint、重试、网络、追加支付安全 audit 或完成 processing。Processor 在同父事务追加唯一 `payment.capture.credited` audit，最后用私有 claim 做 `state=leased AND generation=claim AND lease_until>clock_timestamp()` CAS；零行整体回滚。数据库 deferred 完整性约束必须证明 source/effect/receipt/journal/credited/audit/同代 processed 全部成立，不能以 review 例外提交已存在 source。

账户后复验与 receipt 前效果选择必须分别写出 `manual_credit` / `voucher_grant` / `sandbox_capture`，不可保留“非 manual 就是 voucher”的控制流。A 的现有权限、到期时刻、source ID、redemption、receipt/reports-v1 和未知提交规则不变。

### 3.2 savepoint、溢出与未知提交

Processor 按 B1 顺序先持 order、不可变 provider revision/transaction identity/首份 selection、真实 tenant SHARE，再创建资金 savepoint；只在其中创建 capture/effect 和调用 helper。helper 自己不释放父锁。

本 Go 路径的已识别余额溢出是 `postLocked` 在发送资金 Batch 前，由已锁账户 checkedAdd 返回的真实 `*billing.Error{Code:"amount_overflow"}`；仅在直接调用本 helper 的该来源边界中映射为 `funding_amount_overflow`。不得凭任意 error 字符串或把任意 SQLSTATE `22003`、CHECK、audit、outbox、generation、网络或 COMMIT 错误归成业务溢出；这些错误整体回滚。若未来改为 SQL 加法，必须另冻结精确语句/错误来源识别，不能直接扩大 catch 范围。

识别的余额溢出才允许 ROLLBACK TO 资金 savepoint，撤销 capture/effect/journal/receipt/credited，保留 savepoint 外 identity/selection 和父锁，写正确 overflow review 并以同代 processing_review 提交。selection 永不因后续新 event 重开；不造“支付失败”或调平分录。COMMIT 不明一律先读原 source/receipt/processing/identity，不重发模型、不同 ID 的 checkout/cancel 或第二资金效果；helper 不拥有提交，所以不将其返回值当持久成功证明。

## 4. VerifyFunding 的逐方向合同

`VerifyFunding(ctx, tenantID)` 签名不变：空 tenant 为平台范围，非空必须规范 UUID。保持 RR、只读、同一快照与提交确认后返回；底层 `verifyFundingTx` 可供 Rebuild 的既有维护事务复用，不自行 Begin/Commit。正常 off、无 marker 且全部 B 表为空应通过；缺合法资金链或有 B 事实却缺对应环境关系不能按 off 跳过。

FundingCheck 保留已有字段并增加两个 int64：

```go
type FundingCheck struct {
    Balanced bool
    MissingReceipts, OrphanReceipts, SourceMismatches int64
    JournalMismatches, OutboxMismatches int64
    RedemptionMismatches, BatchCountMismatches int64
    ClearingMismatches int64
    PaymentIdentityMismatches, PaymentSelectionMismatches int64
}
```

所有计数为独立异常方向，允许同一损坏命中多项，不当作互斥故障分类。Balanced 当且仅当无查询/提交错误且十项计数全部 0；不能遗漏新增计数。保留 A 原检查，sandbox 按下表追加，不以总额相等代替逐笔绑定。

| 计数 | sandbox 必须检查的异常 |
| --- | --- |
| MissingReceipts | 每个已持久 capture source 缺同 source_kind/source_id 的 receipt；只有 selection+合法 overflow review、没有 capture 的情况不是缺 receipt |
| OrphanReceipts | sandbox receipt 无真实 capture，或其 source/tenant 归属无法闭合；未知来源不能落到 voucher 分支 |
| SourceMismatches | capture、effect、order、selection、identity、已验证原件/证据、环境、金额/CNY/历史时间/revision、receipt 之间任一不一致，或 capture 同代 processed/固定 audit 缺失 |
| JournalMismatches | 三种资金 journal 任一不通过原 `funding_journal_valid`；receipt 指向非对应资金 kind、错 operation/reference/actor/tenant、同额错绑或缺完整两分录 |
| OutboxMismatches | 任一 credited 没有其真实唯一资金链，或 source 对应 credited 数量不是 1；sandbox 额外要求 operation=capture、schema1、tenant 与恰一个 amount 字符串字段，不能二次累计 |
| ClearingMismatches | 三种 clearing 任一分录的 journal/source 不合法；receipt 没有其 kind 对应的指定负分录。使用显式三分支 CASE，不用 ELSE=运营赠额 |
| PaymentIdentityMismatches | 不可变全局交易身份与原 capture 证据/冻结订单/revision 不符、其处理不是 identity.processing_generation 的 processed/review、或无 selection 且无正确 duplicate review |
| PaymentSelectionMismatches | selection 与 identity/首证据/order 不符；无 capture 且无其 identity 对应 overflow review；有 capture 但不是所选 identity 或资金链不完整。overflow review 不为孤立 capture 提供豁免 |

RedemptionMismatches 与 BatchCountMismatches 保持 A 原语义；sandbox 不制造 redemption，不加到券批次数。tenant 范围以 source/order 的真实 tenant 为准；不存在逻辑 order 的 unknown evidence 不创建假 tenant，也不是缺资金。身份/selection 的租户过滤不得从任意 review 自报 tenant 推断。

租户核验只比较该租户 journal 的三种 clearing 指定分录，不将其 receipt subtotal 与平台共享 clearing 全余额比较。平台范围才核对所有账户。处理时间晚于订单 expires 不构成异常；发生时间边界、provider closed 部分序和唯一 selection 才是资格。核验只报告，不补写来源、不重发 PSP 或模型。

## 5. VerifyLedger、Rebuild 与旧报表

`VerifyLedger` 的 public `LedgerCheck{balanced,account_mismatches,unbalanced_journals}` 形状保持不变；新 clearing 是合法平台非正科目，其余额仍必须等于全部历史分录 NUMERIC SUM。全平台检查包含该账户；tenant 检查只核本租户账户与 journals，并在同 RR 快照调用扩展后的 verifyFundingTx。Balanced 继续同时要求账户、平衡 journals 和全来源 funding 正确，不能仅把新 kind 加到 allowlist 而跳过其核验。

Rebuild 仍只在持独占实例锁、维护门禁、所有源事实写入/worker 停止并排空后执行；新增 payment 命令、证据处理和网络 worker 同样必须停止，不能只停旧财务 10s loop。外部 mock 可保留原 ID 重投，但应用不在维护态 ACK 未持久证据或产生资金。此生命周期由后续 B2/B3 接线实现，本片测试须自行建立同等隔离，不能用函数注释假设已排空。

保持 `reports-v1` 事件协议与 credited 总额口径；新 source 不需要新事件 kind/schema，不为来源分类新增第二个累计入账事件。Rebuild 使用新 consumer，先重放内部 outbox，再在切 active 的同一维护事务核对：扩展 verifyFundingTx 十计数、所有租户 `SUM(funding_receipts.amount::numeric)` 与 credited、旧 charged/refunded/released/request 数、全账本余额/分录。任一错误保持旧 active projection，不修历史或部分切换；成功才切换并追加原安全审计。projector 只读 outbox/active projection、写自己的 receipts/totals，不能因新来源获 payment 表读取权。

## 6. 来源摘要与 A/B 发布兼容

签名不变：`VoucherService.GetFundingSummary(ctx,VoucherReader)`、`ListFundingReceipts(ctx,VoucherReader,FundingReceiptPageInput)`。继续在当前授权事务只读本租户，owner/billing_viewer 可读，member/API Key 拒绝；不以历史 session role 替代当前 DB 权限。

新目标 FundingSummaryView 为以下 **六字段**，全部金额以 NUMERIC(累加)的规范非负十进制字符串表达，不扫描到 Money/int64：

```text
currency, total_amount, manual_credit_amount, voucher_grant_amount,
sandbox_capture_amount, as_of
```

同一 SQL/快照计算三类 SUM 与 DB as_of，恒有 `total=manual+voucher+sandbox`；无 sandbox 为字符串 `"0"`，不 omitempty、不称现金收入。receipt 形状保持已有八字段，source_kind 筛选只允许空/manual_credit/voucher_grant/sandbox_capture；sandbox source_id 必须是 capture UUID。分页仍固定 50/读 51、C 排序、cursor 绑定真实 tenant+kind+filter、每页重验权限，不加 limit/offset。

当前 web 精确校验旧五字段、两来源及旧合计等式，故此处属于必须配套的线协议变更。B2 可以在随机 schema 内验收库与迁移；**不能把仅改 Go DTO/自动 JSON 序列化的构建作为可单独发布版本**。B3 必须同版更新 HTTP 安全响应/前端严格 parser、三来源筛选、模拟来源提示及其测试，且在任何服务实际产生 sandbox 入账之前完成；禁止放宽为接受未知字段/未知 source 来回避兼容。原钱包余额六字段与 reports-v1 保持原形状。

## 7. 普通 off 与独立 worker provision/verify

### 7.1 API 与离线职责

保留 `operations.ProvisionFor(ctx,pool,schema,appRole,projectionRole)` 签名。它在现有 blanket grant 后，对所有 B 表撤回 app/projector 广泛表权限及所有单独列ACL，再按 B1 app/投影矩阵收紧；处理 marker 表为空是正常路径。不得执行原型中 `SELECT ... INTO STRICT` 必须已有 marker 的 grant DO。重复调用必须得到同一边界，不恢复 app 的 capture/effect/identity/selection/review/query evidence 或任意支付资金权。worker 的离线 provision 同样先逐列收回旧授权；单独 REVOKE ALL ON TABLE 不能证明存量列授权已被删除。

增加下面独立、显式 owner API；它们不用于 HTTP，不接收 DSN/密码，不在普通服务里隐式调用：

```go
type PaymentDatabaseRole string
const (
    PaymentApplicationRole PaymentDatabaseRole = "application"
    PaymentWorkerRole      PaymentDatabaseRole = "worker"
    PaymentProjectionRole  PaymentDatabaseRole = "projection"
)
var ErrPaymentRolePrivileges error // 固定安全错误，不含 SQL/DSN/秘密

func ProvisionPaymentWorkerFor(context.Context, *pgxpool.Pool,
    string, string, string, string) error // schema, appRole, workerRole, projectionRole
func VerifyPaymentRolesFor(context.Context, *pgxpool.Pool,
    string, string, string, string) error // 离线检查同上，不写 ACL
func VerifyPaymentRole(context.Context, *pgxpool.Pool, PaymentDatabaseRole) error
```

marker 的初始化、nonce/私密 manifest、角色创建与实际 LOGIN 密码由离线 QA harness 精确 provisioning，绝不写入源码/日志；worker 是新的独立 LOGIN，不是 APP clone、SET ROLE 连接、mock runtime 或 receiver runtime。`ProvisionPaymentWorkerFor` 只给已存在角色配置 ACL，必须核对 marker 的三个固定角色、DB/schema OID 与彼此不同，并确认调用者是实际 owner；所有 identifier 以 pgx.Identifier 渲染。它不创建环境/角色、不重置 marker、不修改 app 的业务来源历史。

### 7.2 权限矩阵与运行验证

以 [B1 权限矩阵](b1-contract.md#8-数据库完整性最小角色与核验) 为基线，并冻结两处最小增量。第一，APP额外获得`payment_query_attempts`、`payment_verified_queries`的SELECT，以便SECURITY INVOKER的VerifyFunding/VerifyLedger/Rebuild完整重验query原件与请求绑定；无INSERT/UPDATE/DELETE或grant option，不给projector，不输出到客户DTO。资金核验不依赖`payment_query_conflicts`作为合法来源证明，该表APP仍无权读取，不能靠跳过原件核验或SECURITY DEFINER绕过。第二，worker 除 B1 所列 marker/证据/资金/租约最小权限，**只新增 schema_migrations 的 SELECT 元数据权**，用于自身连接只读 `migrations.Verify` 完整 001–021 校验；无 INSERT/UPDATE/DELETE、DDL 或绕过旧版本选项。它不能读 users/membership/session、邮箱/MFA、券/人工 credit、上游凭据、020 周期预算表、mock/receiver schema，也不能写 projection receipts/totals。需要的 tenant 仅 SELECT(id)/UPDATE(id) 供父锁；BEFORE UPDATE 对固定 worker 无条件拒真实写；orders 同理。

VerifyPaymentRole 是只读、最多 5s、RR 事务，实际 `current_user=session_user` 且为当前独立 LOGIN；不接受 owner SET ROLE。APP/worker能读marker时，存在marker则名称、DB/schema OID和三角色关系精确匹配；worker没有marker一律拒绝。projector对全部B表零SELECT，所以其runtime检查仅由pg_catalog证明实际LOGIN、角色提升闭包、全部B表/函数零权限及schema边界，不能自行读取marker。marker中projector名称/OID与三角色关系由离线owner的VerifyPaymentRolesFor和后续APP私密启动证明负责核对；单独projector权限检查不声称证明了环境归属。普通off无marker的APP/projector验证仍检查各自B ACL，不能借marker不存在跳过广泛grant检查。

检查有效表权限与逐列权限、grant option、schema CREATE/owner、函数 EXECUTE/SECURITY DEFINER/search_path、危险 role attributes、PUBLIC 与继承权限，并追踪可 SET 及 ADMIN OPTION 提升闭包；不能只读直接 ACL。任一可达身份有 owner/superuser/createrole/createdb/replication/bypassrls、跨角色提升或禁止表/列/function能力就失败。worker 使用全 schema allowlist，而非只检查 B 表后遗漏既有业务表；app/projector 同时保持原 alert/pricing/identity/A 边界，不借本片重置其他模块的正确权限。

在线构造器在 listener/worker 启动前调用自身连接的迁移/角色/marker 验证，并校验独立私密配置；验证函数本身不授予/撤销权限、不 SET ROLE、不创建目录。普通 `off+无marker+无B事实`：不构造 PSP client/worker/签名配置、不注册 notification 或 QA 控制路由、不出站；仅安装默认 off schema 不改变该边界。有 marker/B 历史即使 mode=off 也必须验证原环境/旧 keys/query 配置，并允许既有授权命令及合法资金收尾；缺证明要在 listener 前失败，不能静默按新空环境启动。live 始终拒绝。

本片不修改 main/router；上述启动路径在后续接线与真实进程测试完成前只能记为合同。M/Q callback origin 不热改，应用测试必须新建从一开始指向应用 listener 的环境。

### 7.3 根实施裁决：函数与历史前缀的精确边界

正式角色分片只负责本次所有 `payment_` 函数和原位替换的 `funding_journal_valid/funding_fact_guard`；以审核的021静态SQL提取最终函数体摘要、签名/参数/返回、语言、易变性及invoker/search_path作为可信expected，拒未知同前缀函数、替换函数体、错误属性、PUBLIC/继承/SET/ADMIN带来的越权EXECUTE或grant option。APP可执行本族；worker只执行B1/B2权限原型明确列出的sandbox及依赖函数，不能执行legacy A资金谓词或人类SKU/取消函数；projector无本族EXECUTE。触发器返回函数的EXECUTE不代表可直接执行触发器，但仍按该矩阵收紧。

真实LOGIN联调裁决：worker的明示EXECUTE名单不含`payment_audit_guard/payment_audit_complete`，两者仅由已安装触发器调用，worker共31项；静态expected仍审查这两份函数体和属性。离线候选文件变化不能自动扩大此冻结名单。projector runtime的无marker读取例外按§7.2实施，禁止给其新增marker SELECT或SECURITY DEFINER。

001–020其余既有PUBLIC invoker函数的ACL不在本片重构；不能把这部分写成已完成全库函数最小化认证。worker对整个应用schema的全部表/视图和逐列权使用allowlist，故旧invoker函数不能扩大其资金/身份/凭据权限；此外三角色任一可达上下文拥有应用schema内可执行SECURITY DEFINER函数或任意函数grant option均拒绝。未来若全局收紧旧公共函数，需单独冻结其所有消费者与投影兼容。禁止为了避开该边界给worker读券、用户、凭据或预算表。

通用ProvisionFor仅当22张B表全部不存在时允许历史001–020 fixture跳过本扩展；部分存在一律失败。最新普通服务仍须完整迁移。marker为空时APP/projector收紧与只读验证照常执行；显式worker provision必须已有合法marker。离线named-role核验在同RR只读事务查有效权限，runtime额外要求actual LOGIN。所有权限错误返回固定安全哨兵，不输出角色名、SQL或DSN。

## 8. 必须通过的验收与可并行边界

测试使用专用 moni_test、随机 schema 与实际三个独立受限 LOGIN；owner 仅安装迁移/seed/角色、精确故障 fixture 和清理，不以 owner/SET ROLE 业务成功代替 worker 事实。每项记录执行版本、范围和清理；未跑项明确待验，不把原 B1 SQL 通过重复记成 Go/HTTP/进程完成。

1. **升级与 OID**：真实 001–020 安装非空合法 A 人工/券/消费/退款/未知预占数据，记录 source/journal/receipt/outbox/audit/余额 hash；应用 021 后 A hash 不变、policy off、B marker/业务为空；原函数 OID/签名/owner/依赖目标不变。任一孤立 A source、同额错绑或旧 clearing 异常中止 021；checksum/未知历史/错误前缀继续拒绝。验证安装两次无副作用及未授权线上角色无 DDL。
2. **Go 第三分支**：实际 worker 从已验 P 通知与 query 持久证据各完成唯一资金链；literal 只有 locator，未知 ID、app/owner SET ROLE/projector、错 tenant/actor/CommandID、伪 effect/selection/evidence、非 RC/过期或错误 generation 全部拒绝。journal reference 必须是 order、operation 必须是 capture；无 redemption、无 A 表读权仍成功。
3. **幂等与 savepoint**：同交易多 event/query 并发、同 order 第二交易、同交易跨 order、同额错绑；精确既有资金返回原 receipt，零新钱/事件；overflow 在 AVAILABLE 与全局 clearing 两方向各验证，保 identity/selection、无 capture/钱、review 同代结束。source/effect/journal/receipt/credited/audit/最终 CAS 任一步故障整体正确回滚；不把任意 22003/constraint 当 overflow。合法晚 capture/账户锁等待跨过 expires 仍成功。
4. **核验与重建**：三种来源混合且跨 tenant，逐方向损坏用离线仅限随机 fixture 构造，十计数与 Balanced 精确失败；包括身份/selection无 source 合法 overflow、坏 selection、孤立 source/receipt/journal/clearing/outbox/audit、错误 generation。VerifyLedger 平台/租户范围、共享 clearing 多租户、不变量、NUMERIC 累加超过 BIGINT、同三类来源 totals 与新 consumer Rebuild 均覆盖；损坏时 active projection 不切换，projector 无源表读权仍能正常投影。
5. **权限与默认关闭**：ProvisionFor 在空 marker 下成功，反复执行后 app 仍不能造 sandbox；离线 worker provision 后用真实 LOGIN 跑合法链。逐列 grant、PUBLIC/继承/SET/ADMIN 提升、被撤权角色、schema owner/CREATE、函数替换/错误 search_path、marker/DB/schema mismatch 均拒；schema_migrations 仅 SELECT。零 marker 的 off 无路由/网络和有历史 off 的合法收尾分别由后续真实进程验收证明，当前库/SQL测试不能冒充。
6. **A 兼容与发布**：原 manual maker/checker、voucher 当前权限/到期/唯一兑换、Reserve/finish/refund/recovery/login 交叉专项通过；独立公共账户 ID 锁序不退化。B3 配套测试严格六字段摘要/三来源 receipt、总计等式、分页授权/游标和模拟提示；未配套以前禁止称旧前端兼容或沙箱运营链路完成。没有真实 PSP/生产收款认证。

实施时可并行只读核验/测试设计、Go helper 与权限/迁移审查；真正修改 shared funding/store/verify/rebuild/operations 与 021 的提交由一名账务集成人掌握。独立 processor/HTTP/UI 可在这些 API/DDL 冻结后分文件推进，但不能提前猜测 helper 或放开资金入口。最终汇总全部结果，再由根同步统一架构正文/changes/manifest 与 HLD/LLD 的能力状态，执行仓库规定的显式 reviewed refresh 和只读 check；本设计文件自身也纳入该次架构同步。

### 7.4 普通public schema的数据库所有者解析（根冻结，2026-11-01）

全仓真实main回归发现：PG15+默认public的nspowner为内建pg_database_owner，迁移对象实际owner为current database.datdba。普通off角色核验必须把这两层身份正确对应，不能要求它们OID字面相等而拒绝正常安装。根冻结：仅当schema.nspowner恰为内建pg_database_owner时，表/审定函数期望owner取当前数据库datdba；其他schema仍精确nspowner。拒绝runtime拥有或可提升到实际/隐式schema owner，PUBLIC/列ACL/函数静态签名和主体/继承/SET/ADMIN矩阵不放宽。该规则不把任意其他owner视作合法，也不按当前在线调用者猜owner。离线worker provision的实际owner核验同样解析此内建角色。

B3沙箱私密proof仍要求具体固定NOLOGIN schema owner/OID，沿运行合同完整证明，不借普通off公共schema兼容绕过sandbox环境身份。追加该核验修正不改021或任何历史函数体；需真实APP/projector公共schema正例、对象换给无关owner负例，以及真实main告警/排空重启回归。

### 7.5 离线NOLOGIN owner与在线实际LOGIN的区别（根冻结，2026-11-01）

B3证明要求具体NOLOGIN schema owner，因此离线ACL安装器必须允许受审管理员会话SET ROLE到该owner。ProvisionPaymentWorkerFor仅要求current_user精确为schema实际owner（§7.4的内建所有者解析仍适用），不再要求离线session_user等于NOLOGIN current_user；它只配置已有角色ACL且仍完整核marker/固定角色/表和函数，不接业务写。能SET ROLE到owner本身已具有该schema所有权，本接缝不新增在线提升权。

所有在线VerifyPaymentRole、NewService/NewProcessor、worker source/资金/lease仍严格actual LOGIN且current_user=session_user；owner SET ROLE到worker不能因此作为资金验收。以完整八角色QA环境真实证明NOLOGIN owner离线provision成功、APP/worker实际连接构造与跨身份拒绝。不得为使fixture通过而把schema owner改成LOGIN。

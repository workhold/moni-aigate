# 022-C0：人工核款与兑换券来源关联检查（根冻结窄片）

2026-11-02。根从 [C0 来源与维护核验候选](c0-verifier-contract.md) §9–11 拆出第一个可独立交付的纵向窄片并冻结本合同。范围是完整读取当前数据库内的 `credit_orders`、`voucher_redemptions`、`funding_receipts` 三表，并独立核对人工核款和兑换券的 source↔receipt 双向关系。它只形成带固定局部 scope 的描述性结果；**不是完整 FundingSources 核验、CoverageComplete、全源 proof、basis、epoch、维护初始化或资金写入许可**。sandbox capture 收据会完整计数为未覆盖来源；不读取 payment 模拟器表，不据此声称 B0/B3 支付来源已核。

冻结输入来自 [022 总规格](spec.md)、[C0 候选](c0-verifier-contract.md) §11.2、[T3 ReadLease 合同](c0-read-lease-contract.md) §1–4，以及当前已应用的 [002](../../migrations/002_billing.sql)、[017](../../migrations/017_funding_vouchers.sql)、[021](../../migrations/021_payment_sandbox.sql)。既有迁移不得修改。

## 1. 目的和非目标

人工核款与兑换券是同步提交的钱包来源，但其持久化对象分散在操作源和 `funding_receipts`。已存在的 SQL guard / `VerifyFunding` 可继续保护原线上写入及兼容读取，不能作为新的独立 source oracle。本片通过完整原始行读取与 Go 关系核验，先把两类来源的 receipt 关联规则独立表达。

本片不读取或证明：账本账户/分录、审核事件、credited outbox、用户/租户存在性、voucher batch/voucher/revocation/commerce 命令图、sandbox capture/evidence/order/effect 图、请求 reservation/settlement/refund、Key、采购账户、周期预算、C0 lot、同事务 top-xid 生命周期、PG catalog/权限 profile、完整历史 coverage。任一排除项都不能由本片 `ScopedLinksConsistent` 推定通过。

## 2. 类型与错误合同

纯算法放在 `internal/fundinglotverify`，输入只含本文件 §3 的 owned raw rows；核心签名：

```go
func CheckFundingSourceLinks(ctx context.Context, rows FundingSourceRows) (FundingSourceLinksCheck, error)
```

输入形状和值不符合合同返回零结果及固定 `ErrFundingSourceRowsInput`；行数/owned UTF-8 字节预算超限返回零结果及 `ErrFundingSourceRowsResource`；context取消和deadline分别返回零结果及固定 `ErrFundingSourceRowsCanceled` / `ErrFundingSourceRowsTimeout`。算法不得返回部分report；不得调用billing写侧planner、旧`verifyFundingTx` / SQL业务谓词或数据库。

维护接线在 `internal/maintenancescan`：

```go
func VerifyFundingSourceLinksMaintenance(*maintenancetrust.ReadLease) (*FundingSourceLinksResult, error)
func (*FundingSourceLinksResult) Verify(*maintenancetrust.Permit) error
func (*FundingSourceLinksResult) Summary(*maintenancetrust.Permit) (fundinglotverify.FundingSourceLinksCheck, error)
```

入口仅接受非 nil 的真实 ReadLease，严格执行 `Require(ScanFunding) → 固定完整扫描与 core → Finish`。失败返回 nil result 并 Abort；结果内的 facts、proof 均私有，Verify/Summary 必须消费同 Permit、同 `ScanFunding` proof。其证明仅为这个固定 reader 正常完成，不提升为全资金来源或 Permit。结果 `String/GoString/JSON` 固定脱敏；summary 不含业务 ID、行内容、源密钥或证明正文。

## 3. 固定表、列和完整读取

以 lease 内固定 `SELECT pg_catalog.current_schema()::pg_catalog.text` 取得 schema；拒绝空、系统或带 NUL 的 schema。标识符只用 `pgx.Identifier` 引用。三张普通表必须读取全部行，不加 tenant/state/source_kind 过滤，不 JOIN 隐去孤儿行，不使用 SQL 聚合或 migration 中的有效性函数。

| Reader | 精确 SELECT 列顺序 | keyset |
| --- | --- | --- |
| `funding-source-links.receipts.v1` | `id,tenant_id,source_kind,source_id,amount::pg_catalog.text,currency,journal_id` | `id COLLATE "C"` |
| `funding-source-links.credits.v1` | `id,tenant_id,amount::pg_catalog.text,state,requester,approver,journal_id` | `id COLLATE "C"` |
| `funding-source-links.redemptions.v1` | `id,tenant_id,voucher_id,funding_receipt_id,amount::pg_catalog.text,journal_id` | `id COLLATE "C"` |

每表从 `hasCursor=false` 开始，固定 128 行一页；后页按 `id COLLATE "C" > $cursor COLLATE "C" ORDER BY id COLLATE "C"`。空 TEXT ID 仍可作为真实行，不得以空字符串作为初始游标。完整消费、每行一次 Scan、观察 Rows.Err 并显式 Close 后，小于128行才记 EOF；整128行仍须再读下一页。要求行 ID 严格递增，发现重复、倒序、超页、漏 Scan、EOF/Err/Close 失败或任何 SQL/解码失败，整个读取以固定安全错误失败，结果为零。

查询通过 ReadLease 既有封闭参数目标，所有分页共享一次固定 30 秒 lease 预算，不另开连接/事务、不重置deadline、不重试、不返回裸 Rows/Conn/Tx。三个 reader 总行数最多100,000、所有所选字符串按 UTF-8 实际字节最多16MiB；小于等于边界可接受，超出为固定 resource 错误+零结果，lease deadline则为固定 timeout+零结果。此界描述送入调用方/保留输入，不声称限制 PostgreSQL 排序资源或整个 Go RSS。

## 4. 输入词法与双向关联语义

所有字符串须为有效 UTF-8、无 NUL；关系用原始 TEXT 精确比较，业务ID不强制UUID格式并保留已存在TEXT标识。数据库NULL与空文本绝不混为一谈。`FundingSourceCreditRow.Approver`和`JournalID`是`*string`，nil精确保留SQL NULL，非nil空字符串保留为空文本。空文本不会变为SQL NULL；本窄片对下文列出的业务必需字段将空文本按shape mismatch报告，而非input/decode错误。`amount::text`必须为规范正十进制的`int64`表示，拒绝前导零、符号、空白、小数、指数和溢出；这些字面不可能由固定`BIGINT::text` reader返回，若输入出现则是input错误。canonical金额≤0或currency非CNY按receipt/业务行shape mismatch计。每张表内的主键重复为输入错误；不同表间同文本ID可合法，不跨表比较主键唯一性。

### 人工核款

- 每个 `credit_orders` 的 `id,tenant_id,requester` 非空；状态只能是 `pending` 或 `approved`，金额为正；approver若有值须非空且不同于requester。
- `pending` 必须 `approver IS NULL AND journal_id IS NULL`，且不存在 `manual_credit` receipt。
- `approved` 必须 approver、journal_id 非空，且恰有一行 `funding_receipts` 精确满足：`source_kind=manual_credit`、`source_id=credit.id`、tenant/amount/journal相同、currency=`CNY`。
- 每行 `manual_credit` receipt 必须反向找到恰一条 approved credit；不匹配、pending 有效果、孤立 receipt 或一笔 credit 多 receipt 都是固定 mismatch 计数。

### 兑换券

- 每条 `voucher_redemptions` 的 id、tenant、voucher、funding receipt、journal 非空、amount 为正。
- 每条 redemption 必须精确对应恰一行 receipt：`source_kind=voucher_grant`、`source_id=redemption.voucher_id`、`receipt.id=redemption.funding_receipt_id`、tenant/amount/journal相同、currency=`CNY`。
- 每条 `voucher_grant` receipt 必须反向找到恰一条 redemption，并且 redemption 的 funding receipt id 与该 receipt id 相同。
- 所有孤儿、重复关联、跨租户/金额/来源/账务引用不符均为固定 mismatch 计数；不得因为表的 FK、UNIQUE 或 trigger 预先存在而跳过 Go 核验。

### 固定未覆盖种类

所有receipt行均检查公共shape：`id,tenant_id,source_id,journal_id`非空、amount为规范正int64、currency精确`CNY`。receipt `source_kind`按已知/未知分类；空值计入未知。`sandbox_capture`只验证这项公共shape并完整计入`UncoveredSandboxReceipts`；其source语义、支付capture/evidence/order/effect和journal关系均未覆盖。其他未知`source_kind`计入`UnknownReceiptKinds` mismatch。结果范围固定为`manual_voucher_receipt_links`，`ScopedLinksConsistent`仅表示本§4中人工核款与兑换券关系及receipt公共shape无mismatch。只要存在sandbox收据，即使局部关系一致，也须如实报告正数uncovered；不能将本结果转成全局balanced或coverage。

## 5. 结果字段

结果仅包含固定字符串 `Scope="manual_voucher_receipt_links"`、三个扫描行数、`ScopedLinksConsistent`、`UncoveredSandboxReceipts`、`UnknownReceiptKinds`，以及有限的 mismatch 计数：`ReceiptShapeMismatches`、`CreditShapeMismatches`、`PendingCreditEffects`、`ApprovedCreditMissingReceipt`、`CreditReceiptMismatches`、`OrphanManualReceipts`、`RedemptionShapeMismatches`、`RedemptionReceiptMismatches`、`OrphanVoucherReceipts`、`DuplicateSourceReceipts`。

计数使用`int64`；JSON使用十进制字符串格式。计数单位固定如下：`ReceiptShapeMismatches`按`id,tenant_id,source_id,journal_id`为空、amount≤0或currency非CNY的receipt行计；`CreditShapeMismatches`按`id,tenant_id,requester`为空、amount≤0、未知state、approved缺必要非NULL/nonempty字段、非NULL approver为空或等于requester、非NULL journal_id为空的credit行计；`RedemptionShapeMismatches`按五个ID字段任一为空、amount≤0或source/receipt任一唯一关联键重复的redemption行计；`UnknownReceiptKinds`按未知kind的receipt行计。`PendingCreditEffects`和`ApprovedCreditMissingReceipt`按credit行计；`CreditReceiptMismatches`按approved credit关联键计（字段不符或receipt数>1时各记一次）；`RedemptionReceiptMismatches`按redemption行计；`OrphanManualReceipts`/`OrphanVoucherReceipts`按无法精确关联的receipt行计；`DuplicateSourceReceipts`按人工/兑换券`(source_kind,source_id)`组中超出首行的receipt数计。pending credit带receipt时同时记一个`PendingCreditEffects`及所有未匹配receipt对应的orphan；approved credit缺少receipt只记`ApprovedCreditMissingReceipt`，不再重复计`CreditReceiptMismatches`；approved credit多receipt时记一次`CreditReceiptMismatches`并按多余receipt数记`DuplicateSourceReceipts`。任何一个voucher关联错误按上述双方各自独立的行/关联键计数，因此一个故障可在反向检查中同时产生不同计数。

`ScopedLinksConsistent` 当且仅当所有 mismatch 计数为零；`UncoveredSandboxReceipts` 不改变该局部布尔值，但任何调用者不得将其解释为完整性。对象没有 `CoverageComplete`、`Balanced`、`Ready`、Permit 初始化方法或可序列化 proof 字段。

## 6. 角色、错误和生命周期边界

维护读取不放宽 T2 catalog/history/actual identity。`ReadLease` 自己持有同一个 T2 Snapshot/RR transaction；本片不另建 snapshot，不调用 public `VerifyLedger`、`VerifyFunding`、PB `Verify`，也不以另一个结果填补 scope。预期 schema `credit_orders`、`voucher_redemptions`、`funding_receipts` 必须已被固定 T2 catalog 证明其结构/角色；本片不发行或重建 catalog proof。

domain package 只暴露 billing internal 范围的窄 `Query/Rows` 接缝和 `ReadFundingSourceFacts` 固定算法；不接受任意SQL工厂/成功位，不返回ScanProof。`maintenancescan` 是唯一这次真实lease的组合入口，单向依赖 billing + fundinglotverify + maintenancetrust；禁止 billing、main、在线请求和其测试依赖 maintenancescan / maintenancetrust / fundinglotcatalog。

## 7. 验收

1. `fundinglotverify` race 测试覆盖空/正/负/边界金额、UTF-8/NUL、每表重复ID与跨表相同ID、unknown种类、pending/approved、每类孤儿、租户/金额/journal/source kind/ref错配，以及计数JSON字符串和输入不变性；预期值由独立手算，不复用写侧 validator。
2. `billing` reader 测试覆盖每表 0/1/127/128/129 行、多页 C序、空 ID 游标、重复/乱序、缺 EOF/Err/Close、SQL失败、取消、100,000/100,001事实和16MiB/16MiB+1字节；所有失败返回整个零 facts。
3. 专用 PostgreSQL + genuine T2 `Acquire→Snapshot→Permit→BeginScan(ScanFunding)` 验证全部实际 LOGIN/RR/固定角色、三表真实EOF、已知 mismatch、单kind消费、复制lease/Permit失效、离开事务后 proof 失效、资源限制及对象清理。记录结果仍只称局部来源关系核验；不操作真实钱包、供应商或PSP。
4. 核心及 reader 汇合后更新架构入口、HLD/LLD、022/交付计划与专项验证记录，跑相关race/真实PG专项、`make check` 与指定专库 `make verify-mvp`，重渲染受影响架构图，再由集成人显式刷新manifest和只读检查。

`ScanFunding`在每个原Permit中只能成功消费一次。未来完整Funding composite不可先调用本节维护wrapper后再开始第二个Funding lease；它必须在同一lease内组合本节固定reader/core和其他Funding读者，最后统一Finish，或先冻结新的ScanKind。`billing.ReadFundingSourceFacts`只返回局部summary，不暴露raw facts给跨域外部构造。

本合同冻结后仅授权上述scope的实现。若实际schema/profile与固定列不符，按前置错误失败关闭，不自动放宽字段。完整FundingSources、sandbox证据、Key/采购/PB/B完整关系、跨域覆盖组合、basis/epoch及全资金C0仍需各自冻结和验收。

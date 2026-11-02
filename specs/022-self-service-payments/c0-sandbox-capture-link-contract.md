# 022-C0-T3：sandbox capture 与资金收据直接关联（根冻结窄片）

2026-11-02。根依据[c0-verifier-contract.md §11.2–11.3](c0-verifier-contract.md)、[B2资金合同](b2-funding-contract.md)与[ReadLease合同](c0-read-lease-contract.md)冻结此窄片。范围只验证`payment_captures`和`sandbox_capture`收据之间的直接来源引用。它不是完整SandboxFacts、capture身份/evidence图、账务/credited效果核验、全源coverage或支付真实性证明。

## 1. 目标与严格排除

独立发现`funding_receipts.source_kind='sandbox_capture'`是否恰指向一个存在且金额/租户/CNY相符的`payment_captures`行，并反向检查每个capture是否恰有一条匹配收据。不得调用`payment_capture_source_valid`、`payment_sandbox_journal_valid`或其他写侧`payment_*_valid`函数作为Go业务结论。

本片不验证capture所属order/SKU/selection/transaction identity/provider revision/evidence original/processing/review/effect关系；不验证`funding_receipts.journal_id`指向的journal/ledger lines/clearing；不读credited outbox/audit，也不验证外部HMAC或PSP真实性。因此summary即使`DirectLinksConsistent=true`，仍须将每个capture图及相关入账效果明确报告为未覆盖，不得递减完整资金来源未覆盖数。

不改migration、payment adapter、线上收款开关、资金writer或B2计费行为。

## 2. 固定reader与scope

只允许真实`ReadLease`通过`ScanSandboxSources`读取固定schema；schema由租约内`SELECT pg_catalog.current_schema()::pg_catalog.text`取得。SQL用`pgx.Identifier`限定当前schema内表名及列；不接收外部schema/provider，不开新连接/事务。

两个表都必须逐表无过滤地完整读取所有原行，不依赖FK/UNIQUE/trigger或SQL helper隐藏坏数据；不JOIN、不聚合、不读SQL业务谓词。列与页序固定：

| 表 | 列顺序 | keyset |
| --- | --- | --- |
| `funding_receipts` | `id,tenant_id,source_kind,source_id,amount::pg_catalog.text,currency,journal_id` | `id COLLATE "C"` |
| `payment_captures` | `id,tenant_id,amount::pg_catalog.text,wallet_credit_amount::pg_catalog.text,currency` | `id COLLATE "C"` |

每表固定128条C序keyset页，使用私有`hasCursor=false`表示首屏，因此空TEXT ID不会被漏掉；整128行页后仍继续读取下一页。每行恰好Scan一次，行ID必须严格递增；真正小于128行且Rows.Err为nil、显式Close成功后才记录EOF。重复/倒序、超页、漏Scan、未完整EOF、取消/期限、SQL/decode/Err/Close错误均返回安全固定错误及零结果，无partial report或重试。

两表共用100,000行与16MiB所选UTF-8字符串字节预算及同一个最多30秒ReadLease期限。检查/资源失败时不得先保存超界行再报错。此预算限制客户端固定投影拥有的行/字符串输入，不表示PG排序、driver峰值或整个进程RSS硬上限。

## 3. 独立关系算法

core在无IO纯包消费所有receipt/capture原行；表内重复主键属于输入错误，跨表相同TEXT键合法。字符串为UTF-8且无NUL，ID按原TEXT精确匹配；金额为规范signed-int64十进制文本，非正金额作为shape mismatch，不转float。

receipt公共shape要求`id,tenant_id,source_id,journal_id`非空、amount为正、currency精确`CNY`。所有非`sandbox_capture` receipt行只计入`OtherFundingReceipts`，不参与capture关系判断；它们的完整来源关系仍由其他固定reader负责。空/未知kind属于scope外行，计入Other，不被称为capture已验证。

capture shape要求`id,tenant_id`非空、amount和wallet credit为正、两者相等、currency精确`CNY`。不从这些字段推定capture evidence或资金效果有效。

对每一条sandbox收据，必须按`source_id`找到capture且`tenant_id/amount/currency`与capture的`tenant_id/wallet_credit_amount/currency`精确相同；孤儿按receipt行计，找到capture但字段不符也按receipt行计。反向每个capture须有receipt：零条按capture行计缺失，大于一条按capture组计一次count mismatch。`DuplicateSandboxReceiptSources`按所有sandbox receipt的source ID分组，对每组超出首条的receipt数累计，不论capture是否存在或字段是否匹配。shape与link计数独立，一个坏行可同时计入两者；未匹配仍继续检查其它行。

结果固定`Scope="sandbox_capture_receipt_reference_v1"`，`Receipts`是`funding_receipts`全表行数，`Captures`是`payment_captures`全表行数。`OtherFundingReceipts`是全表中`source_kind != sandbox_capture`的子集（包括空值/未知文本；DB列本身非NULL），故sandbox收据数=`Receipts-OtherFundingReceipts`。结果还含以下计数：`FundingReceiptShapeMismatches`（仅sandbox收据行）、`CaptureShapeMismatches`（capture行）、`OrphanSandboxReceipts`（每条找不到capture的sandbox receipt）、`SandboxReceiptCaptureMismatches`（每条tenant/amount/currency与已找到capture不符的receipt）、`CapturesMissingReceipts`（每条无sandbox receipt的capture）、`CaptureReceiptCountMismatches`（每个receipt数>1的capture组）、`DuplicateSandboxReceiptSources`（每组source ID超出首条的receipt数）、`UnvalidatedCaptureGraphs`和`UnvalidatedLedgerEffects`。最后两项分别等于扫描capture数，明确说明本片没有覆盖其identity/evidence图和资金入账效果；JSON计数使用十进制字符串。

`DirectLinksConsistent` 当且仅当所有shape、mismatch、孤儿和duplicate计数全为0；`OtherFundingReceipts`以及两个unvalidated计数只描述未由此scope核验的事实，不改变direct link布尔值。不存在`CoverageComplete`、`Balanced`、`Ready`或proof可序列化字段。

## 4. 维护入口与生命周期

`internal/maintenancescan`新增独立`ScanSandboxSources`入口：仅接受非nil真实lease，`Require → 固定SQL/core → Finish`，在正常Finish后私有保存局部facts与ScanProof。Summary/Verify必须绑定原Permit及`ScanSandboxSources`；失败返回nil result并Abort。普通main、HTTP、支付worker和billing领域包不依赖维护层。

每个Permit的`ScanSandboxSources`只能尝试一次。它与`ScanFunding`可以在同一原Permit/Snapshot分别完成，但两个局部结果仍不互相升级为全来源组合；未来完整scanner必须另行冻结组合合同并在一个受审原Snapshot内完成所有必需reader，不可接受调用方拼接summary。

本片不得写资金、结算退款、启用/调用PSP，不能被在线准入或维护init使用，也不发行epoch/basis/writer权限。

## 5. 验收

1. 纯core测试覆盖正反向孤儿、重复receipt source、tenant/amount/currency/source错配、公共shape、other kind、规范金额、跨表TEXT重名、ID重复、UTF-8/NUL、资源边界、取消/超时、计数JSON字符串、输入不变性和零结果。
2. 固定reader测试覆盖两个表各0/1/127/128/129页边界、空TEXT ID、C序、Err/Close/Scan/Next/Query错误、取消/timeout、100000/100001行、16MiB/+1字节，所有失败为零facts。
3. 真实sandbox Plan与Init以专库真实身份从原Permit消费`ScanSandboxSources`，生成一个正常已入账capture作为直接关系正例；用仅限随机fixture的受控坏数据验证孤儿/不匹配；核原固定三表EOF、Proof绑定、同kind消费、扫描前后资金快照相同、对象归零清理。普通库缺sandbox表时固定失败，不降级为空通过。
4. 补统一架构/HLD/LLD/README与验证记录，运行target race/vet、`make check`和配置TEST_DATABASE_URL后的`make verify-mvp`。没有专库时必须逐项标示真实PG及整仓商业验收未执行。

任何后续扩大到identity/evidence/processing/review/effect、journal/ledger/outbox/audit的能力，都要继续独立冻结精确列、规范canonical parser、资源边界、扫描EOF与本切片未覆盖计数的递减规则；本合同不授权这些reader。

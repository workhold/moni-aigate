# 034.5 余额构成、客户回执与付款基准报表验证

日期：2026-11-15。范围只含 `/api/wallet` 构成、客户入账回执、026 报表的 COIN/法币口径、文本与媒体分配边、独立现金退款汇总及控制台。真实服务器/1Panel 部署、真实供应商账号与生产对账、正式支付或真实资金转移、生产 HA/性能/灾备、034.6 约合展示与显示名不在本片。

状态：**034.5 实现与本地验收通过；提交和推送进行中。**

## 先冻结的实施补充

- `/api/wallet` 的 `paid_available/grant_available/other_available/refund_held` 与 available/held 来自同一次来源和账户一致性读取；`paid+grant+other=available`，REFUND_HELD 与推理 HELD 分开。
- 客户回执直接读取原 receipt 的 `basis_kind/fiat_currency/fiat_amount`。paid 返回 CNY/USD 和正整数微法币；非付费的币种和金额均为 JSON null，不接受供应商成本等额外字段。
- 文本与媒体每条结算分配边按 `floor(charged×fiat/issued)` 用 `big.Int` 计算整数微法币。grant/opening 计零；unknown、缺买入版本、缺法币、缺失或 charged 合计不完整的分配边均使毛利为 null。
- `charged/refunded/net_revenue` 是 COIN 微币，净消费币数不再是毛利输入。只有全部结算、成本全已知、分配完整且付款基准仅含 CNY 时，毛利才为 CNY 付款基准减已知采购成本；多币种不折算。
- completed 现金退款按 approved_at 窗口和可选 tenant 独立汇总到 summary 的 `cash_refunded`，不冲减付款基准，不按模型或请求重复分摊；请求级 CSV 现金退款列留空。
- 本片没有迁移、新表、第二钱包/计量/付款事实源、请求写入或新运行时权限。

## 已通过的定向 PostgreSQL 验证

全部使用显式 `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`，本片新增 PG 测试均实际执行，没有用 SKIP 代替：

- `TestPostgresWalletCompositionIncludesAllSourcesAndRefundHold`、`TestPostgresWalletCompositionOpeningIsOther`：付费、赠送、unknown、开账、推理预占及现金退款冻结的构成与账户一致。
- `TestOperatingReportPaymentBasisAndUnknowns`：CNY 折扣分配、grant/opening 零基准、unknown、缺买入版本、缺法币、多币种、缺失及部分分配边的毛利边界。
- `TestOperatingReportExactLargeAmountsAndUnknownUnions`：大整数分配与采购成本不经过 float/int64 乘积溢出。
- `TestPostgresReadOperatingReportScopesAndNumeric100`：真实 SQL 的 NUMERIC(100,0)、completed 状态、approved_at 左含右不含、tenant 过滤及 USD 现金退款不影响 CNY 毛利。
- `TestPostgresReadOperatingReportUsesOneReadOnlySnapshot`、`TestPostgresReadOperatingReportIncludesMediaSupplierCostOnce`：分配边加入后仍保持同一 RR 快照、媒体成本与请求身份边界。
- `TestOperatingReportPaymentBasisPostgres`：真实 Reserve/Settle 的 CNY 折扣与 USD 分配分别为 54 和 18；现金退款退休后原付款基准不变，CNY/USD 现金退款分别为 1846/882；unknown 与多币种毛利为 null，其他 tenant 看不到这些汇总。
- `TestOperatingReportPostgresCommercialFlowAndAuthorization`：真实两次 attempt、成本更正与消费退款后，COIN charged/refunded/net=20/5/15，CNY payment_basis=20、成本=18、毛利=2；CSV 新列、协议元数据、角色权限及报表不发模型请求保持不变。

## 已通过的 HTTP 与前端验证

- 客户钱包/回执 HTTP 已验证十字段钱包 DTO、paid 的 CNY 付款金额、无供应商成本字段；赠送与 unknown 的六类来源筛选、null 法币字段及 available=49、paid=37、grant=5、other=7 的构成均通过。
- `web/tests/commerce.test.mjs`：六类来源/basis 组合、租户与 UUID、paid 正法币、非付费 null、非法币种/JSON number/零付款/额外成本字段拒绝。
- `web/tests/operating-report.test.mjs`：COIN 与 micro_coin、CNY/USD 精确映射、旧 CNY 消费 DTO 拒绝、多币种/未知毛利、筛选与下载生命周期。
- `web/tests/wallet-sources.test.mjs`、`web/tests/wallet-source-details.test.mjs`：`manual_bonus/manual_unknown/cash_original` 及非法来源/refund_class 组合。
- 最终 `npm --prefix web test`：359/359 PASS，0 FAIL、0 SKIP；`npm --prefix web run typecheck` 与 `npm --prefix web run build` 通过。
- `go vet ./...`、`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` 通过。

## 全仓与架构收口

- 首轮并行 `go test ./...` 在共享专库上出现迁移锁争用及由其引起的包级超时，未计为通过；未提高超时或删除断言。
- `TEST_DATABASE_URL=... go test -p=1 ./... -count=1`：全部 77 个 Go 包串行通过。
- `TEST_DATABASE_URL=... go test -race -p=1 ./... -count=1 -timeout=1200s`：全部 77 个 Go 包通过。`internal/billing` 348.838s、`internal/identity` 301.179s、`internal/server` 501.542s、`internal/operations` 84.644s、`migrations` 86.691s、`internal/reporting` 1.677s；保留项目标准包上限，没有降低 bcrypt 成本或增加 SKIP。
- `go vet ./...`、`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate`、`go run ./scripts/check-procurement-layout` 通过。
- 统一架构 reviewed refresh/check 与 `make check` 已通过，最终文件清单与复核理由见 `architecture-manifest.json` 和 `docs/architecture/changes.md`。034.6 约合展示/显示名与真实外部环境验收不能由本片代替。

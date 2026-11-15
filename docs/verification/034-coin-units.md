# 034.1 虚拟币单位与类型边界验证

状态：034.1 实现与本地专项验收通过（2026-11-14）；PR #8 已合入并回合 `local-mvp`。034.2 及后续能力不在本记录范围内。

## 当前实现边界

- `journals.currency`、`funding_receipts.currency`、`price_versions.currency` 改为 COIN；既有金额、售价分子和分母不缩放。供应商成本、采购观察、采购预算和支付法币仍为 CNY。
- 计量 canonical、历史请求快照和 `TextRates.currency` 保留冻结的 CNY 规范化标签；只迁移零售外层价格币种，不引入第二套计量格式。
- `billing.FiatMoney` 与 `billing.Money` 是不同的 Go 定义类型，两个方向均不可隐式赋值。支付 SKU/订单及处理器内部的付款金额使用 FiatMoney，钱包到账使用 Money；JSON 仍是精确十进制字符串。
- `current_005_immutable_pricing_history.sql` 在升级事务内恢复 current_004 曾改写的历史 pricing operation 与审计原字节。读取路径验证原始 CNY 指纹后投影 COIN 的当前指纹与结果，数据库原件不变；`pre_coin_input_fingerprint` 仅作为原 operation 的迁移证明，后端严格重放，前端只在证明与原指纹完全一致时完成只读恢复，不引入旧 DTO 或第二套历史事实。
- 026 经营窗口报表的付款基准与同币种毛利留到 034.5；本切片不混合 COIN 零售与 CNY 供应商成本计算。

## 专用 PostgreSQL 验收

数据库：`127.0.0.1:55439/moni_test`，不记录凭据。所有下列命令均显式设置 `TEST_DATABASE_URL`，未以 SKIP 代替验收。

`TestVirtualCoinMigrationPreservesAmountsAndCanonical` 先安装 current_004 之前的 current baseline，再以非超级用户的 schema owner 创建完整旧图：pending→approved credit、平衡 CNY journal/receipt、来源 lot/balance/event、credited outbox，以及带 pricing operation 和审计的 CNY 默认售价。随后执行 current_004 与不可变历史修复迁移，断言：

1. 金额与费率数值、canonical 和 digest 完全不变；三个外层币种均为 COIN。
2. 旧 funding journal 继续通过完整 guard 与 `VerifyFunding`。
3. 原默认售价 operation 按原 operation ID 重放，当前结果为 COIN，旧指纹准确保留，未插入第二个价格版本；operation safe-input、result snapshot、原始 fingerprint 和审计在完整升级前后逐字节一致。
4. 迁移可重复执行且当前 verifier 通过。

已执行并通过：

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go test -race -p 1 -count=1 ./migrations ./internal/operations -timeout 300s
```

结果：`./migrations` 57.838s，`./internal/operations` 75.488s，均 PASS（包含不可变历史修复后的重跑）。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go test -race -p 1 -count=1 ./internal/catalog ./internal/payments ./internal/billing -timeout 400s
```

结果：catalog 36.270s、payments 50.703s、billing 288.371s，均 PASS。覆盖静态支付函数体哈希、真实 LOGIN/ACL、沙箱入账、完整资金图、售价发布与并发，以及 FiatMoney 处理器边界。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go test -race -p 1 -count=1 ./internal/server -timeout 600s
```

结果：459.090s，PASS。覆盖真实 main、迁移与 provision、HTTP、权限、SSE、恢复和重启不重放。此前一次运行期间迁移文件仍在编辑，重启校验按预期报 checksum mismatch；本结果来自迁移稳定后的完整重跑，不将前次失败计为通过。不可变历史修复后，定价 HTTP 专项 `TestPricingHTTPBusinessAndCurrentDomainIsolation`、`TestRestrictedAPPDefaultPricePublishHTTP`、`TestGroupPriceVectorHTTPApplication` 另以 13.175s PASS。

另已执行：

- `go vet ./...`：PASS。
- `env -u TEST_DATABASE_URL go test -race -p 1 -count=1 ./...`：PASS；仅作为全仓编译与非 PG 回归，数据库用例的 SKIP 不计为商业验收。
- 当前实现的 reviewed 架构 refresh 与只读检查：2191 个文件，快照 `97838356cf2b`，均 PASS。
- `npm test -- --run`：347 个测试全部通过，包括 Go/PostgreSQL/TypeScript 共享 pricing fingerprint golden 与 pre-COIN 原 operation 恢复负例。
- `npm run typecheck`、`npm run build`：均通过。
- PR #8 head `32290af0cead01e4cfa451861ff4eb88458cf3e8` 的 `go run ./scripts/check-architecture`：2149 个文件，快照 `9608d9b45941`，PASS。

## 明确排除

本记录不证明 034.2 的角色与付款基准、034.3 五桶、034.3b 币价版本、034.4 现金退款、034.5 报表或 034.6 展示配置已完成，也不证明真实收款、正式支付、生产储值、真实供应商或生产部署合格。

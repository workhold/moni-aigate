# 平台币展示配置（034.6）

2026-11-15。显示名和“约合”是独立控制台展示能力，不是第二套币价，也不改变 COIN、计量 canonical、会计入账、请求、预占、结算、退款、付款基准或采购成本。精确合同见[034规格§8.2](../../specs/034-virtual-coin/spec.md)，当前验证见[034.6记录](../verification/034-6-coin-display.md)。

## 模块与读取/写入链

```mermaid
flowchart LR
    Public[匿名与客户控制台] -->|GET 当前安全 DTO| PublicHTTP[GET /api/coin-display]
    Admin[admin 展示配置页] -->|GET 历史 / PUT 新版本| AdminHTTP[platform coin-display-settings]
    PublicHTTP --> Read[最大 version 的当前展示配置]
    AdminHTTP --> Write[admin + 再认证 + CSRF\noperation 幂等 + advisory CAS]
    Read --> Table[(coin_display_versions\n追加式不可变])
    Write --> Table
    Write --> Audit[(coin_display.updated 审计)]
    Table --> Coin[前端 coin() / MetricMoney]
    Coin --> Display[显示名 + 可选约合\n版本与仅供参考]
```

公开读取只返回 `version/display_name/fiat_currency/rate_num/rate_den`，不含 operation、actor、reason 或历史。admin 可读取 current、最近50版及按 operation_id 的单条操作；写入固定 `operation_id/expected_revision/display_name/fiat_currency/rate_num/rate_den/reason/confirmation`，沿用平台再认证、CSRF、恢复模式禁写和严格 JSON。

[展示服务](../../internal/billing/coin_display.go)以独立 advisory lock 串行 CAS，追加 current+1 并写审计。相同 operation 的完整意图返回原版本，异意图和过期 revision 分别冲突；写入结果不明时不自动重试，admin按 operation_id 只读确认。[HTTP](../../internal/server/coin_display_handlers.go)不进入任何模型请求处理器。

## 数据合同与权限

[current_010_coin_display.sql](../../migrations/current_010_coin_display.sql)建立独立 `coin_display_versions`，version从1递增且唯一，记录不可UPDATE/DELETE。创世 version=1 为“币”、约合关闭、actor NULL；后续版本必须记录真实admin actor。

- 显示名：去除首尾空白后的1..32 UTF-8字节，不含控制字符。
- 约合关闭：`fiat_currency/rate_num/rate_den`全部NULL。
- 约合开启：CNY或USD，分子/分母均为正int64，表示`1 COIN = rate_num/rate_den`法币；部分NULL、零、负数、浮点和JSON number均拒绝。
- 当前展示只取最大version，不提供回填、未来生效或按资金时刻选历史版本。
- APP只有SELECT/INSERT，历史不可改删；worker/projection无展示业务写入。历史schema按表capability处理，当前schema不允许缺表回退。既有68个payment/wallet受审函数不增加。

展示表不是 `coin_rate_versions`，没有买入/退款版本引用，也没有钱包、settlement、route或metering外键。真实网关测试将展示表改名后仍完成请求与结算，证明没有隐藏资金依赖。

## 前端展示与故障边界

[coin-display.ts](../../web/src/coin-display.ts)分别严格解析公开和admin DTO，检查版本连续、字段组合、actor、时间、operation及完整命令。[main.ts](../../web/src/main.ts)加载公开当前版本，[CoinDisplaySettings](../../web/src/components/CoinDisplaySettings.vue)让admin追加显示名、启用/关闭约合、选择币种、填写正整数比例与原因，并执行二次确认。成功响应立即应用，配置不写localStorage。

[format.coin](../../web/src/format.ts)用当前显示名作为可见后缀；启用约合时以BigInt计算`trunc(micro_coin×rate_num/rate_den)`，正数向下取整、负数向零截断，不使用Number浮点，再显示“约合 <法币> · v<版本>，仅供参考”。[MetricMoney](../../web/src/components/MetricMoney.vue)先分离基础金额与约合，保留整数/小数样式，不按整个字符串的小数点拆分。

公开读取失败会重置为默认“币”且不显示约合；未验证响应、旧约合或异常比例都不能展示。该故障回退只影响标签，不伪装成配置读取成功，也不影响任何资金请求。真实外部汇率、会计重估、自动支付、供应商成本和部署验收不属于本片。

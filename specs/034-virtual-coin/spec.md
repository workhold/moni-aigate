# 034：虚拟币记账单位

2026-11-10；状态：草案，待开发者评审后冻结。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#3-第一阶段固定版本-new-api-核心业务能力对齐)，MVP 前决定。参考：New API 固定提交 `e449cc700eb569cdfc2199cb73e97de46944f43c` 的 quota 设计。

## 1. 目标

客户钱包、售价、预占、结算、Key 额度和周期预算统一使用一种平台虚拟币（下称“币”）记账。法币只出现在充值、付款和财务报表中。

要解决的问题：

1. **人民币与美元客户共用一套价格表**，不为每个币种维护一份模型售价。
2. **客户不需要关注汇率**：充值时把法币换成币，之后只看币。
3. **促销**：充值赠送、档位优惠可以直接表达。
4. **汇率不进入请求路径**：预占、结算、退款都不做换算。
5. **内部可核账**：每一枚币都能追溯到付了多少钱、什么币种、什么汇率。

不解决的问题：

- **平台的汇率风险。** 币对某个法币固定锚定后，另一个币种的供应商成本仍随汇率浮动，毛利随之变化。这由重新定价和第三阶段的最低毛利过滤处理，见 §9。
- **供应商侧币种。** 采购账户、成本版本和跨渠道比价的币种与汇率表由单独规格处理（下称“供应商币种规格”，待写），本规格只定义它需要的接口。
- **额度到期与订阅。** 后置到订阅规格，见 §11。

## 2. New API 的做法与 aigate 的取舍

以下依据为固定提交的源码。

| New API 机制 | 源码 | 做法 | aigate 取舍 |
|---|---|---|---|
| 统一整数额度 | `common/constants.go:22` `QuotaPerUnit = 500000`，注释 `$0.002 / 1K tokens` | 余额、Key 额度、预扣、日志都用同一个整数 | **采纳**：一种整数单位贯穿钱包 |
| 全局换算可改 | `model/option.go:692` 直接 `ParseFloat` 赋值 `QuotaPerUnit` | 管理员改一个数，已售余额的含义随之改变 | **不采纳**。锚定在首次发行后不可变；重估是受控操作，见 §8 |
| 到账时重新换算 | `model/topup.go:207` 回调中用当时的 `QuotaPerUnit` 计算到账额 | 下单和到账之间改值，用户到账数随之变化；`TopUp` 不保存所用汇率 | **不采纳**。到账数在下单时由不可变 SKU 版本或汇率版本冻结在订单上 |
| 付款金额 float | `TopUp.Money float64`；`controller/topup.go:176` `payMoney.InexactFloat64()` | 付款金额以浮点存储 | **不采纳**。全程整数微单位 |
| 付款价 = 数量 × 单价 × 充值分组倍率 × 档位折扣 | `controller/topup.go:151–178`；`common/topup-ratio.go`；`operation_setting.PaymentSetting.AmountDiscount` | 促销靠运行时倍率相乘 | **改为不可变 SKU 版本**：每个档位明确写出付款额与到账币数 |
| 展示换算 | `setting/operation_setting/general_setting.go` 的 `QuotaDisplayType`（USD/CNY/TOKENS/CUSTOM）与 `CustomCurrencyExchangeRate` | 同一余额按配置汇率显示为不同货币 | **部分采纳**：可选的“约合”展示，带版本，不进入记账，见 §7 |
| 兑换码 | `model/redemption.go:13–26`，`ExpiredTime` 为兑换码有效期 | 兑换后额度并入余额，没有来源与到期 | **已有更强实现**：022-A 双审兑换券，每次兑换产生独立来源 lot |
| 汇率常数 | `setting/ratio_setting/model_ratio.go` `USD2RMB = 7.3 // 暂定` | 写死在代码中 | **不采纳**。汇率只在充值时以版本记录 |

结论：采纳“统一整数单位”和“展示与记账分离”这两个想法，不采纳“可变全局换算”“到账时重算”“浮点金额”“运行时倍率相乘”。

## 3. 币的定义

| 项 | 规定 |
|---|---|
| 名称 | 平台可配置显示名（如“点数”），只是标签 |
| 存储单位 | 整数，1 存储单位 = 10⁻⁶ 币（微币），沿用现有 `billing.Money int64` |
| 锚定 | 1 币 = `anchor` 单位的锚定法币。锚定币种与比值在**首次发行任何币之前**设定，之后不可修改 |
| 默认锚定 | 1 币 = 1 CNY（D-034-1） |

默认锚定的理由：现有所有金额都是人民币微元（`price_versions`、`funding_receipts`、`api_keys.lifetime_limit` 等）。1 币 = 1 CNY 时存储数值完全不变，只改单位标签与约束，不需要缩放任何数据或测试夹具。

**锚定的含义**：锚定只用于财务报表把币折成法币的名义值，以及汇率风险的对冲方向。它不保证客户能按锚定比值兑回法币；现金退款按每个来源的实际付款基准计算（§6）。

## 4. 数据模型（追加迁移草案）

按 AGENTS 约定，只追加 `current_*.sql`，不修改已应用的迁移。MVP 前开发库可按 ADR-005 重建。

### 4.1 单位约束

| 表 | 现状 | 改为 |
|---|---|---|
| `journals.currency` | `CHECK (currency = 'CNY')` | `CHECK (currency = 'COIN')`：钱包 journal 只用币 |
| `price_versions.currency` | `CHECK (currency = 'CNY')` | `'COIN'`：客户售价以币计 |
| `funding_receipts.currency` | `CHECK (currency = 'CNY')` | `'COIN'`，见 §4.2 |
| `api_keys.lifetime_limit`、周期预算 | 无单位列，隐含人民币微元 | 语义改为微币，数值不变 |
| `cost_versions.currency` | `CHECK (currency = 'CNY')` | 不在本规格修改；属于供应商币种规格 |

`ledger_accounts` 与 `journal_lines` 没有币种列，单位继承自 journal，无需修改。

### 4.2 充值的法币基准

`funding_receipts` 现在只有 `amount` 与 `currency='CNY'`。`amount` 的语义改为**发行的币（微币）**，`currency` 改为 `CHECK (currency = 'COIN')`；锚定 1 币 = 1 CNY 时数值不变。不新增平行的币数量列，按 ADR-005 不做新旧双写。追加法币基准列：

```sql
ALTER TABLE funding_receipts
  ADD COLUMN fiat_currency   text,        -- 'CNY' | 'USD' | NULL
  ADD COLUMN fiat_amount     bigint,      -- 实付法币，微单位
  ADD COLUMN basis_kind      text,        -- 见下表
  ADD COLUMN fx_version_id   text,        -- 引用 coin_rate_versions，自定义金额充值时必填
  ADD COLUMN basis_ref       text;        -- SKU 版本或订单 ID，来源可追溯
```

现有行按来源类型回填：`manual_credit` 与 `sandbox_capture` 为 `paid`，`fiat_currency='CNY'`、`fiat_amount=amount`；`voucher_grant` 为 `grant`；`wallet_opening` 为 `opening`。MVP 前也可以直接重建开发库。

| `basis_kind` | 含义 | `fiat_*` | 可现金退款 |
|---|---|---|---|
| `paid` | 实付获得 | 必填 | 视 `refund_class` |
| `grant` | 运营赠送、兑换券、充值赠送部分 | 为空，基准为 0 | 否 |
| `opening` | 停服开账（027） | 为空 | 否 |
| `unknown` | 只知道币数，不知道付款额（外部发币） | 为空 | 否 |

约束：

- `basis_kind = 'paid'` 时 `fiat_currency`、`fiat_amount` 非空且 `fiat_amount > 0`。
- 其余三种时 `fiat_*` 为空。
- 写入后不可修改（沿用现有 receipt 不可变触发器）。

`funding_lots` 不新增列：`lot.id = funding_receipt_id`，基准通过 receipt 关联读取。

### 4.3 支付 SKU 与订单

`payment_sku_versions`、`payment_orders` 已有 `amount` 与 `wallet_credit_amount` 两列，约束强制相等，币种只允许 CNY。

| 约束 | 现状 | 改为 |
|---|---|---|
| `wallet_credit_amount = amount` | 强制相等 | `wallet_credit_amount > 0`，与 `amount` 独立 |
| `currency = 'CNY'` | 写死 | `currency IN ('CNY','USD')` |
| 语义 | — | `amount` 为法币微单位，`wallet_credit_amount` 为微币 |

SKU 版本不可变，订单从版本复制两者，因此“付多少、得多少”在下单时冻结。这替代 New API 的 `Price × TopupGroupRatio × AmountDiscount` 运行时相乘。

**充值赠送**：一个 SKU 版本可声明 `bonus_coin_amount ≥ 0`（新增列）。捕获时产生两个来源，见 §5.2。

**自定义金额充值**（New API 的任意金额）：

```sql
CREATE TABLE coin_rate_versions (
  id              text PRIMARY KEY,
  fiat_currency   text NOT NULL CHECK (fiat_currency IN ('CNY','USD')),
  coin_per_fiat_numerator   bigint NOT NULL CHECK (coin_per_fiat_numerator > 0),
  coin_per_fiat_denominator bigint NOT NULL CHECK (coin_per_fiat_denominator BETWEEN 1 AND 1000000),
  effective_at    timestamptz NOT NULL,
  source_ref      text NOT NULL,     -- 汇率来源说明
  created_by      text NOT NULL REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (fiat_currency, effective_at)
);
```

追加式，不可修改。下单时以数据库时刻选定当时生效的版本，计算到账币数并与 `fx_version_id` 一起冻结在订单上；回调时只读订单，不再换算。取整规则见 D-034-3。

### 4.4 清算账户

现有平台账户：`FUNDING_CLEARING`、`REVENUE`、`OPERATING_GRANT_CLEARING`、`SANDBOX_FUNDING_CLEARING`、`WALLET_OPENING_CLEARING`。全部改为以币计，不按法币拆分，因为钱包 journal 只有一种单位。法币对账在报表层基于 receipt 的基准完成（§9），不建立法币 journal。

这与 HLD 一致：业务流水不宣称法定会计总账。

## 5. 资金流

### 5.1 不变的部分

Reserve、结算、释放、消费退款、恢复的逻辑与锁序完全不变，只是单位从人民币微元改名为微币：

- 来源三桶守恒：可用 + 冻结 + 已消费 = 发行额；
- FIFO 预占，按原分配结算与逆序退款；
- 预占时冻结的售价版本以币计；
- 请求路径中没有任何汇率。

### 5.2 充值捕获

一笔支付捕获在同一事务内：

1. 读取订单冻结的 `amount`、`currency`、`wallet_credit_amount`、`bonus_coin_amount`、`fx_version_id`。
2. 写付费 receipt：`basis_kind='paid'`、receipt `amount = wallet_credit_amount`（微币）、`fiat_amount` = 订单 `amount`、`fiat_currency` = 订单 `currency`。
3. 若 `bonus_coin_amount > 0`，写赠送 receipt：`basis_kind='grant'`、`refund_class='nonrefundable'`。
4. 两个 lot 与 journal 同事务提交。

现在一个订单对应一个 capture、一个 lot。第 3 步需要放开这条一对一关系，具体改动在 `internal/payments` 的捕获处理，实施时需要先读完该路径。

### 5.3 其他入账

| 入口 | `basis_kind` | 说明 |
|---|---|---|
| 人工核款（manual_credit） | `paid` | 财务录入付款法币、金额与到账币数；双审规则不变 |
| 兑换券（voucher_grant） | `grant` | `face_amount` 改为以币计 |
| 停服开账（wallet_opening） | `opening` | 不变 |
| 沙箱捕获 | `paid` | 同 §5.2，仅本地沙箱 |

## 6. 退款

**消费退款**（现有）：把已收费的币按原分配逆序退回原来源，不涉及法币，不变。

**现金退款**（022 C1，尚未实现）：本规格只规定规则，不开放入口。

- 只针对 `basis_kind='paid'` 且 `refund_class` 允许现金退款的来源；
- 可退币数 = 该来源的剩余可用币；
- 退款法币金额 = 可退币数 × `fiat_amount / amount`（receipt 的法币与币之比），按**原付款币种和原比率**计算，不按当前汇率重算，向下取整到法币最小单位；
- 赠送、开账、未知基准的来源不可现金退款。

这样汇率变动不会让退款金额变化，平台不承担退款时点的汇率差。

## 7. 展示

- 客户侧余额、价格、请求费用、Key 额度、周期预算一律显示币。
- 付款、订单、充值记录显示法币原值与到账币数，客户能看到“付了多少、得到多少”。
- 可选的“约合”显示（D-034-6）：按追加式展示汇率版本换算，标注“仅供参考”，账单保存所用版本；**不参与任何记账、预占或退款**。这对应 New API 的 `QuotaDisplayType`，但带版本。
- 推理协议响应体不增加币字段，保持各协议原生信封。
- 供应商成本不向客户展示（HLD §7、LLD §25）。

## 8. 换算配置入口

按作用范围分三类，不合并成一个开关：

| 范围 | 入口 | 规则 |
|---|---|---|
| 展示汇率 | 平台配置页 | 追加式版本；只影响“约合”展示 |
| 新发行汇率 | 平台配置页（`coin_rate_versions`） | 追加式版本；只影响之后下单的自定义金额充值；已发行来源不变 |
| 已有余额重估 | 单独的受控命令，**默认不提供** | 见下 |

**已有余额重估**相当于面值变更，必须同时缩放来源本金、余额、在途冻结、价格版本、Key 额度和周期预算，否则客户的真实价值会被单方面改变。若将来实现，必须满足：

- 双人审批，申请人不能批准；
- 维护模式、停服、取得服务 advisory 锁，与 027 的停服开账相同，保证没有在途预占；
- 先出预览：影响的租户、来源数、取整损益合计；
- 一个 operation 原子完成，写 journal 与审计；新增来源事件类型（现有约束只允许 `funding`、`reserve`、`finish`、`refund`）；
- 不提供撤销，反向需另发一次操作；
- **向下重估**需要明确的合同依据，不作为常规功能。

“让所有客户多得一些币”应使用批量赠送来源（`grant`），不需要重估。

## 9. 财务报表

026 经营报表现在固定 `Currency: "CNY"`、`AmountUnit: "micro_cny"`。改为同时给出：

| 口径 | 计算 | 依据 |
|---|---|---|
| 名义收入（币） | 已结算消费币数 | `settlements` |
| 名义收入（锚定法币） | 名义币 × 锚定比值 | §3 |
| 现金基准收入（按法币分列） | 对每条 `funding_settlement_lots`，`charged_amount × fiat_amount / amount`（取所属 receipt），按来源的 `fiat_currency` 分组；`grant`、`opening` 计 0；`unknown` 计为未知 | `funding_settlement_lots` 已保存每次结算在每个来源上的收费额 |
| 采购成本 | 供应商币种原值；折算需要供应商币种规格中的汇率版本 | 待写 |
| 毛利 | 现金基准收入 − 已知采购成本，同币种比较；任一项未知或缺汇率时为 null | 沿用“未知不当零” |

现金基准收入是对已有事实的只读计算，不改动请求路径或结算写入。

## 10. 对其他规格的影响

| 规格 | 影响 |
|---|---|
| [030](../030-model-aliases-group-multipliers/spec.md) | 分组倍率编译的目标单位改为币 |
| [032](../032-phase2-text-data-plane/spec.md) | 客户侧周期用量上限以币计；缓存命中计费以币计 |
| [033](../033-text-dynamic-routing/spec.md) | 最低毛利计算需要“币售价折法币”与“供应商成本折法币”同一时点的比较，依赖本规格的锚定和供应商币种规格的汇率版本 |
| 022 C1 | 现金退款按 §6 规则 |
| 023-C 订阅 | 套餐额度作为新的来源类型，以币计，见 §11 |

## 11. 后置

- **币的到期**：需要第四个桶“已过期”或到期事件，涉及来源守恒式与核对读取，放到订阅规格。
- **限定模型范围的币**（例如赠送币只能用于某些模型）：需要在 Reserve 的分配算法中按模型过滤来源，放到订阅规格。
- **充值分组优惠**（New API 的 `TopupGroupRatio`）：用按价格组可见的 SKU 版本表达，不引入运行时倍率。
- **真实美元收款**：依赖正式支付商，属于第四阶段；第一阶段只在本地支付沙箱验证美元订单。

## 12. 决策点

| 编号 | 问题 | 建议 |
|---|---|---|
| D-034-1 | 锚定币种与比值 | 1 币 = 1 CNY：数值零变化。若主要成本是美元，可改锚美元，但需缩放全部现有数值 |
| D-034-2 | 充值赠送是独立来源还是合入付费来源 | 独立来源：退款资格清晰，赠送部分基准为 0 |
| D-034-3 | 自定义金额换算取整 | 到账币数向下取整到微币 |
| D-034-4 | 同一订单的付费与赠送来源，FIFO 先后 | 先消耗赠送来源，保留付费来源的现金退款余地；需财务确认收入确认时点 |
| D-034-5 | 美元订单在第一阶段的范围 | 只做沙箱，真实收款第四阶段 |
| D-034-6 | 是否提供“约合”展示 | 提供，带版本，标注仅供参考 |
| D-034-7 | 显示名 | 由平台配置，例如“点数” |
| D-034-8 | 已有余额重估是否实现 | 不实现；向上调整用批量赠送 |

## 13. 合规

这属于储值或虚拟币发行；美元收款涉及跨境。赠送币的收入确认、未使用余额的会计处理、退款政策与税务需要财务和法务确认。本规格不对这些问题给出结论，冻结前需要书面意见。

## 14. 验收

| 组 | 断言 |
|---|---|
| 单位 | 迁移后全部钱包 journal、售价版本为 `COIN`；数值与迁移前一致（锚定 1 币 = 1 CNY 时） |
| 充值冻结 | 下单后修改 SKU 或新发行汇率，回调到账币数不变（对照 New API `model/topup.go:207` 的行为） |
| 付款与到账分离 | “付 90 得 100”的 SKU：receipt 记录法币 90、币 100；赠送部分为独立 `grant` 来源（D-034-2） |
| 美元订单 | 沙箱中美元订单到账币数按订单冻结；钱包、预占、结算无汇率参与 |
| 基准约束 | `paid` 缺法币字段被拒；`grant` 带法币字段被拒；receipt 不可修改 |
| 请求路径 | Reserve、结算、退款不读取任何汇率表（静态检查与真实 PG 用例） |
| 消费退款 | 退回原来源，币数精确；不涉及法币 |
| 现金退款规则 | 纯函数测试：按原比率折算，向下取整；赠送与未知基准不可退 |
| 报表 | 现金基准收入按来源分列币种正确；`unknown` 基准与缺汇率时毛利为 null |
| 配置入口 | 展示与新发行汇率追加式、不可修改；无“已有余额重估”入口 |
| 并发 | 同一订单重复回调只入账一次（沿用 022 现有幂等） |

## 15. 分片

| 片 | 内容 | 依赖 |
|---|---|---|
| 34.0 | 本规格评审，裁决 D-034-1…8，合规意见 | — |
| 34.1 | 单位约束改名（journals、price_versions）与文案；数值不变 | 34.0 |
| 34.2 | receipt 法币基准列与约束；人工核款、兑换券、开账写入基准 | 34.1 |
| 34.3 | SKU 与订单放开付款与到账分离；赠送来源；沙箱美元订单 | 34.2 |
| 34.4 | `coin_rate_versions` 与自定义金额充值 | 34.3 |
| 34.5 | 报表现金基准收入 | 34.2 |
| 34.6 | “约合”展示 | 34.1 |

34.2、34.3 改动入账路径，由集成人串行合并，并在专用 PostgreSQL 上验证。

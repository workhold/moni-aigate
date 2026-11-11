# 034：虚拟币钱包与手动充值、手动现金退款

2026-11-10；状态：**方向已确认，细节待裁决后冻结**（Draft — direction confirmed; details pending product, engineering, finance and legal decisions）。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#3-第一阶段固定版本-new-api-核心业务能力对齐)，MVP 前实施。参考：New API 固定提交 `e449cc700eb569cdfc2199cb73e97de46944f43c` 的 quota、充值与管理员调额代码。

## 1. 目标

**对外只暴露虚拟币（下称“币”）。** 客户充值进来的是币；钱包、模型售价、预占、结算、Key 额度、周期预算都以币计；法币只出现在付款记录、对账和财务报表中。

前期运营方式已确认：**用户线下付款，平台财务手动充值；退钱也是手动。** 支付宝等支付平台接入后置。因此本规格按两个范围分层：

| 范围 | 内容 | 状态 |
|---|---|---|
| M（手动） | 币单位、手动充值含赠送、手动现金退款扣减、客户展示、报表基准 | 本规格实施范围 |
| P（支付） | 支付订单与捕获产生付费和赠送来源、支付宝回调金额语义、手续费 | 后置，属 022，本规格只留接口 |
| V（扩展） | 汇率版本表与按汇率建议币数、币到期、限定模型范围、已有余额重估 | 后置，附触发条件 |

要解决的问题：

1. 人民币与美元客户共用一套价格表；
2. 客户不盯汇率，只看“付了多少、得到多少币”；
3. 促销（充值赠送、档位）可以直接表达；
4. 汇率不进入请求路径；
5. 每一枚币都能追溯到付款事实，支持精确的现金退款；
6. 复用现有钱包、来源 lot、预占、结算，**不建第二套钱包**。

不解决：

- **平台的汇率风险。** 币对某个法币锚定后，另一币种的供应商成本仍随汇率浮动。这由重新定价和第三阶段最低毛利过滤处理，见 §10。
- **供应商侧币种。** 采购账户、成本版本和跨渠道比价的币种与汇率表由单独规格处理（供应商币种规格，待写）。

## 2. 与 New API 的对照

依据为固定提交的源码。

| 机制 | New API | aigate 取舍 |
|---|---|---|
| 统一整数额度 | `common/constants.go:22` `QuotaPerUnit = 500000`；余额、Key 额度、日志同一单位 | **采纳**：一种整数单位贯穿钱包 |
| 全局换算可改 | `model/option.go:692` 管理员可直接改 `QuotaPerUnit` | **不采纳**：锚定首次发行后不可变；重估是受控操作（§9） |
| 到账时重新换算 | `model/topup.go:207` 回调中用当时的 `QuotaPerUnit`；`TopUp` 不存所用汇率 | **不采纳**：到账币数在录入或下单时冻结在单据上 |
| 付款金额 float | `TopUp.Money float64`；`controller/topup.go:176` `InexactFloat64()` | **不采纳**：全程整数微单位 |
| 付款价 = 数量 × 单价 × 分组倍率 × 档位折扣 | `controller/topup.go:151–178` | **不采纳**：用不可变单据明确写出付款额与到账币数 |
| 管理员手动调额 | `controller/user_quota.go`：`add` / `subtract` / `override`，一人操作，只有数额，写操作审计，没有凭证或理由字段 | **不采纳**：沿用 aigate 双人审批、必填付款凭证与业务号；没有 `override` |
| 手动“退款” | 即 `subtract`，与原付款无关联；没有退款流程 | **不采纳**：现金退款绑定原付款来源，按原比率折算（§7） |
| 补单 | `ManualCompleteTopUp`：订单行锁、已成功幂等，但到账额按补单时的 `QuotaPerUnit` 算 | **采纳幂等思路**，到账额用单据冻结值 |
| 展示换算 | `QuotaDisplayType`（USD/CNY/TOKENS/CUSTOM）与可调 `CustomCurrencyExchangeRate` | **部分采纳**：可选“约合”展示，带版本，不进入记账（§8） |
| 兑换码 | `model/redemption.go`：兑换后额度并入余额，无来源 | **已有更强实现**：022-A 双审兑换券，每次兑换产生独立来源 |

## 3. 币的定义

| 项 | 规定 |
|---|---|
| 名称 | 平台可配置显示名（如“点数”），只是标签 |
| 存储单位 | 整数，1 存储单位 = 10⁻⁶ 币（微币），沿用 `billing.Money int64` |
| 锚定 | 1 币 = `anchor` 单位的某个法币，在**首次发行币之前**设定，之后不可修改 |
| 默认锚定 | 1 币 = 1 CNY（D-034-1） |

默认锚定使所有已存数值保持不变：现有 `funding_lots.issued_amount`、`price_versions`、`api_keys.lifetime_limit`、周期预算原本就是人民币微元，只改单位标签和 `currency` 约束，不缩放任何数据或测试夹具。

锚定只用于财务报表把币折成法币的名义值与对冲方向。它不保证客户可按锚定比值兑回法币：现金退款按每个来源的实际付款基准计算（§7）。

## 4. 字段单位：不改名，只明确

**现有列的语义已经按“钱包侧”与“付款侧”分开**，不需要把任何 `amount` 改名。本规格做的是明确每列的单位，并改变钱包侧的 `currency` 约束：

| 侧 | 列 | 单位 | 本规格的变化 |
|---|---|---|---|
| 钱包 | `funding_receipts.amount`、`funding_lots.issued_amount`、`funding_lot_balances.*`、`ledger_accounts.balance`、`journal_lines.signed_delta` | 微币 | 无 |
| 钱包 | `credit_orders.amount`、`voucher_batches.face_amount`、`wallet_openings.amount` | 微币 | 无 |
| 钱包 | `journals.currency`、`funding_receipts.currency` | — | `CHECK (currency = 'CNY')` 改为 `'COIN'` |
| 客户计价 | `price_versions`（`currency`） | 币 | `currency` 改为 `'COIN'`；费率数值不变 |
| 客户计价 | `api_keys.lifetime_limit/spent/held`、周期预算 | 微币 | 无（语义说明改为币） |
| 付款 | `payment_orders.amount`、`payment_captures.amount`、`payment_sku_versions.amount`、`payment_transaction_identities.amount` | 法币微单位 | 放开 `currency` 到 `CNY`、`USD`（范围 P） |
| 付款 | `payment_orders.wallet_credit_amount`、`payment_sku_versions.wallet_credit_amount` | 微币 | 放开 `wallet_credit_amount = amount` 的强制相等（范围 P） |
| 供应商 | `cost_versions.currency`、采购账户、`upstream_attempts.cost_amount` | 供应商币种 | 不在本规格修改 |

**`journals.currency = 'COIN'` 的适用范围**：只有客户钱包与客户计费 journal 使用它。供应商成本、平台结算、现金退款的法币事实不得复用该字段。我核对过现状：`journals` 目前只由 `credit`、`reserve`、`refund`、`media.reserve`、`media.settle` 等客户钱包操作写入；采购预占、采购预算事件和成本更正各有自己的事件表，没有 journal 列。本条把这个现状写成约束。

## 5. 数据模型（追加迁移草案，范围 M）

只追加 `current_*.sql`，不修改已应用的迁移。MVP 前开发库可按 ADR-005 重建。

### 5.1 付款基准

`credit_orders` 当前只有币数 `amount`、`business_ref`、`proof_ref`。追加：

```sql
ALTER TABLE credit_orders
  ADD COLUMN fiat_currency text   NOT NULL CHECK (fiat_currency IN ('CNY','USD')),
  ADD COLUMN fiat_amount   bigint NOT NULL CHECK (fiat_amount > 0),     -- 实付法币，微单位
  ADD COLUMN bonus_amount  bigint NOT NULL DEFAULT 0 CHECK (bonus_amount >= 0),  -- 赠送币，微币
  ADD COLUMN transfer_ref  text;   -- 线下收款流水号，可空
```

- `amount` 保持“付费部分到账币数”，`bonus_amount` 为随单赠送币数。“付 90 得 100”：`fiat_amount=90`、`amount=100`；“付 100 得 100 送 20”：`fiat_amount=100`、`amount=100`、`bonus_amount=20`。
- `amount + bonus_amount` 不得溢出 `int64`，沿用现有 `amount_overflow` 检查。

`funding_receipts` 追加基准列。receipt 写入后不可修改：应用角色对来源关系只有 SELECT/INSERT 权限（027 角色设计），新列沿用该权限，不新增 UPDATE 授权：

```sql
ALTER TABLE funding_receipts
  ADD COLUMN basis_kind    text NOT NULL,          -- paid | grant | opening | unknown
  ADD COLUMN fiat_currency text,
  ADD COLUMN fiat_amount   bigint,
  ADD COLUMN basis_ref     text;                   -- 来源单据 ID，可追溯
-- paid: fiat_currency/fiat_amount 必填且 > 0；其余三类必须为空
```

| `basis_kind` | 来源 | 可现金退款 |
|---|---|---|
| `paid` | `manual_credit`（付费部分）；后置：真实支付捕获 | 是 |
| `grant` | `manual_bonus`（随单赠送）、`voucher_grant` | 否 |
| `opening` | `wallet_opening`（027 停服开账） | 否 |
| `unknown` | 只知币数、不知付款额的外部发币 | 否 |

现有行回填：`manual_credit` 与 `sandbox_capture` 为 `paid`，`fiat_currency='CNY'`、`fiat_amount=amount`（锚定 1:1）；`voucher_grant` 为 `grant`；`wallet_opening` 为 `opening`。MVP 前也可直接重建开发库。

### 5.2 一个单据产生付费与赠送两个来源

约束来自现有结构：`funding_lots` 有 `UNIQUE(funding_receipt_id)`、`UNIQUE(source_kind, source_id)`、`CHECK (id = funding_receipt_id)`；`funding_receipts` 有 `UNIQUE(journal_id)`。所以“两个来源”必须是**两个 receipt、两个 lot、两笔 journal**，用不同的 `source_kind` 共用同一个单据 ID：

| | 付费来源 | 赠送来源 |
|---|---|---|
| `source_kind` | `manual_credit` | `manual_bonus`（新增） |
| `source_id` | 充值单 ID | 同一个充值单 ID |
| receipt / lot ID | 新 UUID | 新 UUID |
| `basis_kind` | `paid` | `grant` |
| `refund_class` | `cash_original`（新增） | `nonrefundable` |
| 清算账户 | `FUNDING_CLEARING` | `OPERATING_GRANT_CLEARING` |
| journal | 独立一笔，平衡 | 独立一笔，平衡 |

`UNIQUE(source_kind, source_id)` 因 `source_kind` 不同天然成立。同一事务内创建，满足：

- **幂等**：审批是 `pending → approved` 的状态迁移，充值单行锁。重复审批发现已 `approved`，读回两个原 receipt 并校验，不产生新来源。`bonus_amount = 0` 时不创建赠送来源。
- **一起提交**：两个来源与两笔 journal 在同一事务，任一失败整体回滚。
- **分别审计**：每个来源一条审计事件。
- **完整性校验**：`funding_journal_valid` 与 `verifyFundingReceiptLotTx` 对两种 `source_kind` 分别校验；校验“付费 receipt 的 `amount` 等于充值单 `amount`，赠送 receipt 等于 `bonus_amount`”。

`funding_lots.refund_class` 的约束同步放开：现为 `IN ('nonrefundable','sandbox_original')` 且 `(source_kind='sandbox_capture') = (refund_class='sandbox_original')`。改为追加 `'cash_original'`，并规定 `cash_original` 当且仅当 `basis_kind='paid'` 且 `source_kind='manual_credit'`（范围 P 再加真实捕获）。

### 5.3 消费顺序

现有分配按 FIFO。同一充值单的付费与赠送两个来源有相同的 `fifo_at`，需要确定的先后（D-034-4）：**同时间时先消耗赠送来源**，保留付费来源的现金退款余额。排序键为 `(fifo_at, refund_class 不是 cash_original 者在前, lot_id)`。不同充值单之间仍严格按时间 FIFO，不因类别而插队。

### 5.4 现金退款扣减所需的结构

现有来源本金守恒为 `available + held + consumed = issued_amount`（`internal/billing/wallet_sources_read.go` 核对，加上延迟约束）。现金退款把可用币永久移出钱包，既不是消费也不是释放，不能借 `consumed` 表达：`consumed` 与每次结算在来源上的收费边一一对应，消费退款沿这些边逆序退回。因此新增第四桶：

```sql
ALTER TABLE funding_lot_balances ADD COLUMN retired_amount bigint NOT NULL DEFAULT 0 CHECK (retired_amount >= 0);
ALTER TABLE funding_lot_events   ADD COLUMN delta_retired  bigint NOT NULL DEFAULT 0;
-- funding_lot_events.kind 增加 'withdraw'
-- 守恒式改为 available + held + consumed + retired = issued_amount
```

必须同步修改的现有对象（均在 `migrations/current_mvp.sql`）：

- `wallet_lot_balance_guard`（`funding_lot_balances` 的写入 guard）与 `wallet_lot_event_stamp`（事件写入前打标）；
- 按事件类型校验的函数：例如 `funding` 事件要求 `delta_held=0 AND delta_consumed=0`，新增 `delta_retired` 后每种类型都要写明其取值；
- `funding_lot_events` 的前缀和核对与 `internal/billing/wallet_sources_read.go` 的四类不一致检查（现为 `available+held+consumed=issued_amount`）；
- 027、028-U 的延迟约束与 `check-wallet-sources` 输出。

这是对 guard 的受控修订，不是追加字段；34.3 单独成片并重做相应真实 PG 验证。同一结构后续也用于币到期（`kind='expire'`）。

### 5.5 现金退款单

```sql
CREATE TABLE cash_refund_orders (
  id              text PRIMARY KEY DEFAULT gen_random_uuid()::text,
  tenant_id       text NOT NULL REFERENCES tenants(id),
  lot_id          text NOT NULL,            -- 原付费来源（receipt/lot ID）
  business_ref    text NOT NULL UNIQUE,     -- 唯一业务号
  coin_amount     bigint NOT NULL CHECK (coin_amount > 0),
  reason          text NOT NULL,
  transfer_ref    text NOT NULL,            -- 线下退款转账凭证，必填
  state           text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','approved')),
  requester       text NOT NULL,
  approver        text,
  fiat_currency   text,                     -- 批准时冻结
  fiat_amount     bigint,                   -- 批准时冻结
  journal_id      text,
  created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK (approver IS NULL OR approver <> requester),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id)
);
```

状态与现有 `credit_orders`、`refund_orders` 一致，只有 `pending` 与 `approved`；不需要的单据保持 `pending`，不提供删除。批准后不可修改；应用角色无 DELETE。

## 6. 手动充值流程（范围 M）

沿用现有双人流程，只增加付款基准字段：

1. 线下收款（转账、其他平台个人收款均可）。
2. 财务操作员 `POST /api/platform/credits`：租户、**付款法币与金额、到账币数、赠送币数**、业务号、凭证引用。
3. 财务审批员 `POST /api/platform/credits/:id/approve`：申请人与审批人不同账号，由数据库约束保证。
4. 同一事务创建付费来源（以及赠送来源），写 journal 与审计。

保持不变：业务号唯一、凭证引用受控、重复审批幂等、`finance_operator` 与 `finance_approver` 角色分离。没有 `override`，没有一人完成的快捷路径。

**单人运营（D-034-9）**：只有一个运营者时，双人规则无法满足。不提供“单人模式”开关。建议给同一自然人开两个账号并分别授予两个角色；这样审计上仍留下两次操作，但实际约束力下降，需要你明确接受。

## 7. 手动现金退款（范围 M）

场景：用户已用掉一部分币，要求把剩余部分的钱退回。平台线下转账给用户，然后在系统里把对应的币从钱包扣除。

**与消费退款的区别**：

| | 消费退款（现有） | 现金退款扣减（本规格） |
|---|---|---|
| 含义 | 某次请求收费有误，把币退回**钱包** | 把币永久移出钱包，同时平台线下退法币 |
| 绑定对象 | 原结算 | 原付费来源 |
| 币的去向 | 回到原来源的可用桶 | 进入来源的第四桶 `retired` |
| 现有实现 | `refund_orders` | 新增 `cash_refund_orders` |

### 7.1 资格

- 只针对 `basis_kind='paid'` 且 `refund_class='cash_original'` 的来源；赠送、兑换券、开账、未知基准的来源不可现金退款。
- 只能扣该来源的**可用**币：`coin_amount ≤ available_amount`。冻结中的币（预占）不可扣，已消费的币不可扣。
- 单次与累计退款的法币额不超过该来源的原付款。

### 7.2 退款法币金额的整数规则

记来源的原付款为 `F`（receipt `fiat_amount`），发行币为 `C`（`issued_amount`），该来源已累计扣除的币数为 `w_old`（`retired_amount`），本次扣除 `k`，`w_new = w_old + k`。

```text
fiat_refund = floor(F × w_new / C) − floor(F × w_old / C)
```

- 乘法使用 `big.Int`（Go）或 `numeric`（SQL），不使用 `int64` 乘法；`F`、`C` 均 ≤ 2⁶³−1，乘积 < 2¹²⁶。
- 向下取整发生在**累计位置**而不是每次各自取整，所以多次部分退款的法币总额恰为 `floor(F × w_total / C)`，不会因逐次取整而累计多退，也不会漏退。
- 全部退完时 `w_total ≤ C`，故总退款 ≤ `F`。
- 这是 `internal/productvalue` 已实现的“先映射绝对端点再相减”算法（`F(k) = floor(P·k/G)`）的直接应用：`productvalue.New(F, C)`，区间为 `[w_old, w_new)` 的价值即为 `fiat_refund`。实施时复用该包，并由独立手算向量交叉验证。
- 币与法币币种按原付款，不按当前汇率换算；汇率变动不改变退款金额，平台不承担退款时点的汇率差。

### 7.3 流程

1. 财务操作员创建现金退款单：来源、币数、原因、**线下转账凭证**。创建时只做资格预检并给出预览（可扣上限、预计法币金额），不扣任何东西。
2. 财务审批员批准（不同账号）。批准事务内：
   - 锁序沿用 Reserve：租户 → 账户按 ID → 来源余额按 `lot_id`；
   - 重新读取来源余额，校验 `k ≤ available`；
   - 计算并冻结 `fiat_amount`；
   - 一笔平衡 journal（`kind='cash_refund'`）：租户 `AVAILABLE` 减 `k`，`FUNDING_CLEARING` 加 `k`；
   - 来源余额 `available −k`、`retired +k`，写 `withdraw` 事件；
   - 审计事件。
3. 财务线下转账，凭证已在第 1 步登记。系统不触发任何外部转账。
4. 重复批准读回原结果，不二次扣减。

`FUNDING_CLEARING` 复用现有账户：充值时记负、退款时回正，净额表示未退出的付费币负债。`ledger_accounts` 的现有约束对 `FUNDING_CLEARING` 没有符号限制，无需新账户类型。

### 7.4 并发与竞态

- 批准与同一租户的 Reserve 共用来源余额行锁，按相同锁序，避免死锁。
- 批准时重新校验可用币，预占先行则可用币不足，批准失败（`insufficient_available`），退款单保持 `pending`，可调小币数后重新申请。
- 现金退款不能触发预占失败或让已冻结请求缺币。

### 7.5 与恢复的关系

- 退款单批准是单个数据库事务，提交后才应线下转账。未知提交（连接断开）时只按业务号读回，不自动重做。
- 恢复任务不涉及现金退款。

## 8. 展示

- 客户侧余额、价格、请求费用、Key 额度、周期预算一律显示币。
- 客户充值记录显示**付款金额、币种、到账币数、赠送币数**，所以客户知道自己付了多少、得到多少。
- 客户看不到供应商成本、渠道与采购信息（HLD §7、LLD §25）。
- 可选的“约合”展示（D-034-6）：按追加式展示汇率版本换算，标注“仅供参考”，不进入记账、预占或退款。
- 推理协议响应体不增加币字段，保持各协议原生信封；币只出现在控制台和请求详情、资金来源接口。

## 9. 换算配置入口

| 范围 | 入口 | 规则 |
|---|---|---|
| 展示汇率 | 平台配置页 | 追加式版本；只影响“约合”展示 |
| 汇率版本表（范围 V） | 平台配置页 | 追加式版本、整数有理数；只用于建议币数，已发行来源不变 |
| 已有余额重估 | 单独的受控命令，**默认不提供** | 见下 |

已有余额重估相当于面值变更，必须同时缩放来源本金、余额、在途冻结、价格版本、Key 额度和周期预算。若将来实现：双人审批；维护模式、停服、取得服务 advisory 锁（与 027 停服开账一致）；先出预览（影响租户数、来源数、取整损益）；一个 operation 原子完成；不提供撤销；向下重估需要明确的合同依据。“让所有客户多得一些币”用批量赠送来源，不需要重估。

## 10. 财务报表

026 经营报表现在固定 `Currency: "CNY"`、`AmountUnit: "micro_cny"`，改为：

| 口径 | 计算 |
|---|---|
| 消费币数 | 已结算消费币数，来自 `settlements` |
| 名义法币值 | 消费币数 × 锚定比值 |
| **按来源分配的客户付款基准** | 对每条 `funding_settlement_lots`，取 `charged_amount × fiat_amount / issued_amount`（所属 receipt），按 `fiat_currency` 分列；`grant`、`opening` 计 0；`unknown` 为未知 |
| 采购成本 | 供应商币种原值；折算依赖供应商币种规格 |
| 毛利参考 | 付款基准 − 已知采购成本，同币种比较；任一项未知或缺汇率时为 null |

**这不是会计收入。** HLD 已说明业务流水不宣称法定会计总账；赠送币的收入确认、未使用余额的会计处理由财务和法务决定。该口径只作为经营报表与毛利参考的输入。

按来源分配的付款基准是对现有事实的只读计算，不改动请求路径或结算写入。

## 11. 对其他规格的影响

| 规格 | 影响 |
|---|---|
| [030](../030-model-aliases-group-multipliers/spec.md) | 分组倍率编译的目标单位改为币 |
| [032](../032-phase2-text-data-plane/spec.md) | 客户侧周期用量上限和缓存命中计费以币计 |
| [033](../033-text-dynamic-routing/spec.md) | 最低毛利需要“币售价折法币”与“供应商成本折法币”同一时点比较，依赖锚定和供应商币种规格 |
| 022 | 范围 P：支付订单放开付款与到账分离、捕获产生付费与赠送两个来源（同 §5.2 结构，`source_kind` 用真实捕获类型与 `capture_bonus`）；支付宝回调有多个金额字段（订单金额、实收、优惠后实付等），`payment_captures.amount` 存哪一个必须在支付规格中写死，并以支付宝官方文档为准 |
| 023-C 订阅 | 套餐额度作为新的来源类型，以币计；到期使用本规格的第四桶机制 |

## 12. 后置

- 币的到期、限定模型范围的币、充值分组优惠；
- 真实支付与美元收款：依赖正式支付商，第四阶段；
- 手续费：支付渠道扣费不影响钱包入账，只在财务对账层分开记录。

## 13. 决策点

用户已确认方向：**币为对外单位、手动充值与退款优先、支付后置**。以下仍需裁决，实施前冻结：

| 编号 | 问题 | 建议 |
|---|---|---|
| D-034-1 | 锚定币种与比值 | 1 币 = 1 CNY：数值零变化 |
| D-034-2 | 赠送是独立来源还是合入付费来源 | 独立来源（§5.2）：退款资格清晰，赠送基准为 0 |
| D-034-3 | 取整 | 退款法币额按累计位置向下取整（§7.2）；自定义换算到账币数向下取整到微币 |
| D-034-4 | 同一充值单两来源的消费先后 | 同时间先消耗赠送来源（§5.3） |
| D-034-5 | 美元在范围 M 的支持 | 手动录入美元付款基准即可，不需要支付渠道 |
| D-034-6 | 是否提供“约合”展示 | 提供，带版本，标注仅供参考 |
| D-034-7 | 显示名 | 平台可配置 |
| D-034-8 | 已有余额重估 | 不实现；向上调整用批量赠送 |
| D-034-9 | 单人运营 | 不提供开关；建议同一自然人开两个账号，并接受约束力下降 |
| D-034-10 | 赠送币是否永不过期 | 范围 M 永不过期；到期属范围 V |
| D-034-11 | 单次最大充值与最大赠送 | 沿用 `int64` 溢出检查之外，由运营另设上限 |
| D-034-12 | 现金退款是否允许部分扣减 | 允许；累计规则见 §7.2 |

## 14. 合规

这属于储值或虚拟币发行，美元收款涉及跨境。赠送币的收入确认、未使用余额的会计处理、退款政策、税务与监管需要财务和法务的**书面意见**。本规格不对这些问题给出结论；冻结前必须取得。

## 15. 验收

| 组 | 断言 |
|---|---|
| 单位 | 迁移后钱包 journal、`funding_receipts.currency`、售价版本为 `COIN`；数值与迁移前一致（锚定 1:1） |
| 不改名 | 付款侧 `payment_orders.amount` 与钱包侧 `wallet_credit_amount`、`issued_amount` 的单位在代码、API 与文档中分别标明；类型层面区分法币金额与币金额，编译期或单元测试禁止互相赋值 |
| 手动充值基准 | 付 90 得 100：receipt 记法币 90、币 100；`paid` 缺法币字段被拒；`grant` 带法币字段被拒；receipt 不可修改 |
| 付费与赠送 | 一单两来源两笔 journal；重复审批只入账一次；赠送为 0 时不产生赠送来源；任一失败整体回滚 |
| 消费顺序 | 同一充值单：先消耗赠送来源；不同充值单严格 FIFO |
| 守恒 | 四桶 `available + held + consumed + retired = issued` 在所有操作后成立；`check-wallet-sources` 更新后无不一致 |
| 现金退款资格 | 赠送、兑换券、开账、未知基准来源被拒；冻结与已消费部分不可扣 |
| 现金退款金额 | 手算向量：多次部分退款的法币总额等于 `floor(F × w_total / C)`，且不超过 `F`；`int64` 边界（接近 2⁶³）不溢出；USD 与 CNY 各一组 |
| 双人与幂等 | 申请人不能批准；重复批准不二次扣减；业务号唯一；未知提交只读回 |
| 竞态 | 批准与并发 Reserve 无死锁；预占先行时批准失败且状态保持 `pending` |
| 请求路径 | Reserve、结算、消费退款不读取任何汇率（静态检查与真实 PG 用例） |
| 报表 | 付款基准按币种分列；`unknown` 基准与缺汇率时毛利为 null |
| 客户展示 | 客户能看到自己的付款金额与到账币数；看不到供应商成本 |

以上数据库相关项需要在专用 PostgreSQL 上通过；跳过不算完成。

## 16. 分片

| 片 | 内容 | 依赖 |
|---|---|---|
| 34.0 | 本规格评审，裁决 D-034-1…12，取得财务与法务书面意见 | — |
| 34.1 | `currency` 约束改为 `COIN`，文案与类型区分；数值不变 | 34.0 |
| 34.2 | receipt 基准列与约束；`credit_orders` 付款基准；赠送来源与 `cash_original`、消费顺序 | 34.1 |
| 34.3 | 第四桶 `retired`：守恒式、事件、`check-wallet-sources`、guard 更新 | 34.2 |
| 34.4 | 现金退款单：数据模型、整数规则（复用 productvalue）、审批事务、HTTP 与控制台 | 34.3 |
| 34.5 | 报表付款基准与客户充值记录展示 | 34.2 |
| 34.6 | “约合”展示与显示名配置 | 34.1 |
| 后置 P | 支付订单与捕获的两来源、支付宝金额语义 | 022 |
| 后置 V | 汇率版本表、币到期、限定模型范围、重估 | 订阅规格 |

34.2、34.3、34.4 改动入账与来源核心，由集成人串行合并，并在专用 PostgreSQL 上验证。

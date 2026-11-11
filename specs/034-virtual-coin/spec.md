# 034：虚拟币钱包与手动充值、手动现金退款

2026-11-10；状态：**设计草案**（Draft — direction confirmed; not frozen; not implemented; pending product, engineering, finance and legal decisions）。本规格不是 034 已实现、币钱包已实现、现金退款已实现或生产储值能力已通过验收的证据。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#3-第一阶段固定版本-new-api-核心业务能力对齐)，MVP 前实现手动充值与手动退款；设计与决策属第一阶段。参考：New API 固定提交 `e449cc700eb569cdfc2199cb73e97de46944f43c` 的 quota、充值与管理员调额代码。

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

## 5. 数据模型与角色（追加迁移草案，范围 M）

只追加 `current_*.sql`，不修改已应用的迁移。MVP 前开发库可按 ADR-005 重建。

### 5.1 单据角色与权限矩阵

**钱包按租户**，不按用户：`ledger_accounts` 与来源 lot 都以 `tenant_id` 为归属，个人用户也是只有一名成员的租户。所以“给某个用户充值”的准确含义是**给该用户所在租户的钱包入账**；员工只是租户成员，没有独立钱包。

单据上区分五个角色字段，不把它们合并成“申请人/审批人”两种：

| 字段 | 含义 | 谁可以是 | 现状 |
|---|---|---|---|
| `recipient` | 入账或退款对应的租户（钱包） | 任一租户 | 已有 `tenant_id` |
| `requested_by` | 发起申请的人 | 租户 owner（客户自助申请）或平台财务操作员 | **新增** |
| `maker` | 实际创建并填写付款/退款事实的人 | 平台财务操作员 | 即现有 `requester` |
| `approver` | 审批人 | 平台财务审批员 | 已有 |
| `transfer_executor` | 线下实际执行退款转账的人（仅现金退款） | 平台财务操作员 | **新增** |

**硬约束**（数据库约束与审批事务共同保证）：

1. `approver ≠ maker`，且 `approver ≠ requested_by`（若 `requested_by` 是平台账号）。审批人不能审批自己发起或自己制单的单据。
2. `transfer_executor ≠ approver`：批准的人不能同时执行转账。
3. **客户自助申请不产生任何钱包效果**：客户（租户 owner）提交的申请只创建 `requested` 状态的申请记录，不能直接产生入账或扣减，也不能被当成已付款；必须由财务操作员核对付款并补充事实（成为 `maker`），再由另一名审批员批准。
4. 系统不在审批前入账。

权限矩阵：

| 场景 | `requested_by` | `maker` | `approver` | 钱包效果发生在 |
|---|---|---|---|---|
| 客户线下付款后自助申请充值 | 租户 owner | 财务操作员（核对付款后补充） | 另一名财务审批员 | 审批事务 |
| 财务代客户录入充值 | 财务操作员 | 同一人 | 另一名财务审批员 | 审批事务 |
| 运营给租户赠送（无付款） | 财务操作员 | 同一人 | 另一名财务审批员 | 审批事务；`basis_kind='grant'` |
| 客户申请现金退款 | 租户 owner | 财务操作员 | 另一名财务审批员 | 见 §7 |
| 财务发起现金退款 | 财务操作员 | 同一人 | 另一名财务审批员 | 见 §7 |

“管理员给员工充值”：员工属于某租户，入账给该租户，不存在员工个人钱包。若将来需要成员级额度，是 Key 额度与成员授权，不是钱包。

**单人运营（D-034-9）**：两个账号由同一自然人持有，数据库层面满足 `approver ≠ maker`，但**不是真正的双人控制**。规格规定：

- 生产环境不允许同一自然人承担制单与审批两个身份；
- 单人运营只能作为内测或应急例外，需要平台配置显式开启（默认关闭）；
- 开启后，每张由两个同源账号完成的单据在审计中带固定标记 `single_operator_exception`，并在财务报表的核对页单列；
- 不能把这种做法描述为双人审批。

### 5.2 付款基准

`credit_orders` 当前只有币数 `amount`、`business_ref`、`proof_ref`。追加：

```sql
ALTER TABLE credit_orders
  ADD COLUMN requested_by  text REFERENCES users(id),
  ADD COLUMN basis_kind    text   NOT NULL CHECK (basis_kind IN ('paid','grant','unknown')),
  ADD COLUMN fiat_currency text,
  ADD COLUMN fiat_amount   bigint,
  ADD COLUMN fx_note       text,                 -- 美元付款时人工确认的汇率说明，可空
  ADD COLUMN bonus_amount  bigint NOT NULL DEFAULT 0 CHECK (bonus_amount >= 0),
  ADD COLUMN transfer_ref  text,                 -- 线下收款流水号
  CHECK ((basis_kind = 'paid') = (fiat_currency IS NOT NULL AND fiat_amount IS NOT NULL AND fiat_amount > 0)),
  CHECK (fiat_currency IS NULL OR fiat_currency IN ('CNY','USD'));
```

`amount` 保持“付费部分到账币数”，`bonus_amount` 为随单赠送币数。“付 90 得 100”：`fiat_amount=90`、`amount=100`；“付 100 得 100 送 20”：`fiat_amount=100`、`amount=100`、`bonus_amount=20`。

溢出：`amount + bonus_amount ≤ 2⁶³−1`，在创建与审批两处检查，沿用现有 `amount_overflow` 错误；上限另见 D-034-11。

**手动美元付款**（范围 M，D-034-5）：管理员同时录入美元金额与到账币数；到账币数**不由系统计算**，请求路径更不读取汇率。`fx_note` 只保存人工确认的汇率文字说明（可空），**不是汇率版本，也不被任何计算引用**。若 `fx_note` 为空，报表只能说“支付了多少美元、到账多少币”，不能声称有正式汇率依据。这不是自动汇率发行；自动汇率与汇率版本属于范围 V。`basis_ref` 必须关联付款凭证或业务单号，见 §5.3。

### 5.3 receipt 的付款基准与允许组合

`funding_receipts` 追加基准列。receipt 写入后不可修改：应用角色对来源关系只有 SELECT/INSERT 权限（027 角色设计），新列沿用该权限，不新增 UPDATE 授权。

```sql
ALTER TABLE funding_receipts
  ADD COLUMN basis_kind    text NOT NULL,
  ADD COLUMN fiat_currency text,
  ADD COLUMN fiat_amount   bigint,
  ADD COLUMN parent_ref    text NOT NULL;        -- 父单据 ID，见 §5.4
```

**`source_kind` 与 `basis_kind`、`refund_class`、清算账户的允许组合由数据库 CHECK 固定，不依赖代码隐含判断：**

| `source_kind` | `basis_kind` | `refund_class` | 清算账户 | `fiat_*` | 创建入口 | 可现金退款 |
|---|---|---|---|---|---|---|
| `manual_credit` | `paid` | `cash_original` | `FUNDING_CLEARING` | 必填 | 人工充值单（付费部分） | 是 |
| `manual_bonus` | `grant` | `nonrefundable` | `OPERATING_GRANT_CLEARING` | 必须为空 | 人工充值单（赠送部分）或纯赠送单 | 否 |
| `manual_unknown` | `unknown` | `nonrefundable` | `OPERATING_GRANT_CLEARING` | 必须为空 | 人工充值单，`basis_kind='unknown'` | 否 |
| `voucher_grant` | `grant` | `nonrefundable` | `OPERATING_GRANT_CLEARING` | 必须为空 | 兑换券 | 否 |
| `wallet_opening` | `opening` | `nonrefundable` | `WALLET_OPENING_CLEARING` | 必须为空 | 停服开账 | 否 |
| `sandbox_capture` | `paid` | `sandbox_original` | `SANDBOX_FUNDING_CLEARING` | 必填 | 本地支付沙箱 | 仅沙箱资格 |

补充规定：

- **`unknown`**：允许通过人工充值单创建（财务只知币数、不知付款额，例如他人代为发币）。永远不可现金退款；毛利与付款基准为 null；必须有业务号与凭证引用，和 `paid` 的审计要求相同；`check-wallet-sources` 把它按普通来源核对四桶守恒，同时在输出里单列 `unknown` 来源总额，避免被误读成已付款。
- `cash_original` 当且仅当 `source_kind='manual_credit'`（范围 P 再加真实支付捕获类型）。
- 约束用单一 `CHECK` 对 `(source_kind, basis_kind, refund_class)` 三元组枚举，不允许表外组合；新增来源类型必须同时更新该约束与本表。
- 现有 `funding_lots.refund_class` 约束（`IN ('nonrefundable','sandbox_original')`，且 `(source_kind='sandbox_capture') = (refund_class='sandbox_original')`）同步放开，追加 `'cash_original'`。

现有行回填：`manual_credit` 为 `paid`、`fiat_currency='CNY'`、`fiat_amount=amount`（锚定 1:1）；`sandbox_capture` 同为 `paid`；`voucher_grant` 为 `grant`；`wallet_opening` 为 `opening`。MVP 前也可直接重建开发库。

### 5.4 同一充值单的两个来源与父单据关联

**现有约束**：`funding_lots` 有 `UNIQUE(funding_receipt_id)`、`UNIQUE(source_kind, source_id)`、`CHECK (id = funding_receipt_id)`；`funding_receipts` 有 `UNIQUE(journal_id)`；`journals.operation_id` 为 `UNIQUE`。现有手动充值的 journal 取 `operation_id = 充值单 ID`，兑换券用新 UUID。

**设计**：同一充值单产生两个 receipt、两个 lot、两笔 journal。

| | 付费来源 | 赠送来源 |
|---|---|---|
| `source_kind` | `manual_credit` | `manual_bonus` |
| `source_id` | 充值单 ID | 同一个充值单 ID |
| `parent_ref` | 充值单 ID | 充值单 ID |
| receipt / lot ID | 新 UUID | 新 UUID |
| journal `operation_id` | 充值单 ID（沿用现状） | **新 UUID**（因 `operation_id` 全局唯一，不能与付费 journal 共用） |
| journal `kind` / `reference` | `credit` / 充值单 ID | `credit_bonus` / 充值单 ID |
| 数额 | `credit_orders.amount` | `credit_orders.bonus_amount` |

**关联与完整性**不能只靠审批状态：

1. `parent_ref` 是父关联的唯一载体。`UNIQUE(source_kind, source_id)` 因 `source_kind` 不同天然成立；两个来源共同通过 `(source_id = parent_ref = credit_orders.id)` 绑定。`basis_ref` 不再单独使用，避免双重含义。
2. 付费 receipt 金额必须等于 `credit_orders.amount`，赠送 receipt 金额必须等于 `credit_orders.bonus_amount`。由两个 DEFERRABLE 约束触发器在 COMMIT 前核对，沿用 `funding_journal_valid` 的风格。
3. **成对性约束**：充值单 `state='approved'` 时，必须恰好存在一个 `manual_credit` receipt；`bonus_amount > 0` 时还必须恰好存在一个 `manual_bonus` receipt，`bonus_amount = 0` 时不得存在。该约束同样在 COMMIT 前核对。
4. 两个来源与两笔 journal 在**同一事务**创建，任一失败整体回滚；不存在“只有一个来源”的可提交状态。
5. **重复审批**：充值单行 `FOR UPDATE`，已 `approved` 时读回两个 receipt 并校验第 2、3 条，通过则直接返回，不创建新来源。
6. **一个存在、另一个缺失**（直接 SQL、恢复异常或 guard 缺陷导致）：`check-wallet-sources` 增加“父单据来源不成对”检查类，报告该充值单 ID 并进入人工审查；系统**不自动补建**缺失的来源，因为补建等于再次入账。

### 5.5 消费顺序

现有分配按 FIFO，同一充值单的两个来源有相同的 `fifo_at`。排序键为 `(fifo_at, refund_class 为 cash_original 者在后, lot_id)`：**同一时刻先消耗赠送来源**，保留付费来源的现金退款余额（D-034-4）。不同充值单之间仍严格按 `fifo_at`，不因类别插队。现有 Reserve 的 FIFO 实现、`wallet_lot_balance_guard` 的 FIFO 写前约束都要同步修改，并有专项验证。

### 5.6 第四桶 `retired`

现金退款把可用币永久移出钱包，既不是消费也不是释放。`consumed` 与每次结算在来源上的收费边一一对应，消费退款沿这些边逆序退回，所以不能借用 `consumed`。新增第四桶：

```sql
ALTER TABLE funding_lot_balances ADD COLUMN retired_amount bigint NOT NULL DEFAULT 0 CHECK (retired_amount >= 0);
ALTER TABLE funding_lot_events   ADD COLUMN delta_retired  bigint NOT NULL DEFAULT 0;
-- 守恒：available + held + consumed + retired = issued_amount
```

**已核对的现有约束**（`migrations/current_mvp.sql` 中的 `wallet_event_valid`、`wallet_journal_valid`、`wallet_lot_balance_guard`）：事件类型封闭、journal 按账户类型与 delta 对账、余额更新必须有匹配事件。下表“必须同步修改的现有对象”据此列出。

**与账户余额检查的关系**：现有函数要求租户 `AVAILABLE` 账户余额等于该租户所有 lot 的 `Σ available_amount`，`HELD` 账户余额等于 `Σ held_amount`。`retired` 不在任何租户账户里：币离开钱包时，journal 把租户账户减少同额、`FUNDING_CLEARING` 增加同额（`FUNDING_CLEARING` 在充值时为负，退出使其回正；现有 `ledger_accounts_check` 对它没有符号限制，而 `OPERATING_GRANT_CLEARING`、`SANDBOX_FUNDING_CLEARING`、`WALLET_OPENING_CLEARING` 被约束为 `balance <= 0`，所以不可现金退款的来源不会向这三个账户回正，也就不会触发该约束）。因此 `Σ available_amount` 与 `AVAILABLE` 账户余额同步下降，现有检查继续成立，不需要新账户类型。

**现金退款的“待转账”冻结**用现有的 `held` 桶与 `HELD` 账户表达（见 §7.2），不使用 `retired` 过渡，因此 `retired` 只在转账确认完成后增加。

**必须同步修改的现有对象**（均在 `migrations/current_mvp.sql`，34.3 单独成片，不可只改一处）：

| 对象 | 变化 |
|---|---|
| `wallet_lot_balance_guard` | 现有实现要求本金守恒 `available+held+consumed = issued`，以及每次更新有同事务、同 revision、三个 delta 完全匹配的事件。需把 `retired` 加入守恒式与事件匹配（第四个 delta）。**该 guard 不要求 `held_amount` 对应某个预占分配**，所以用 `held` 桶表示退款冻结在这一层不会被拒绝 |
| `wallet_lot_event_stamp` | 识别 `delta_retired` |
| `wallet_event_valid` | **事件类型是封闭 `CASE`，未识别类型返回 `false`**，且 `reserve` 事件要求关联 `funding_reservation_lots` 与某个 `reservations`。因此 `refund_hold`、`refund_retire`、`refund_release` 必须各自新增分支，关联到 `cash_refund_orders`，不能复用 `reserve` 分支 |
| `wallet_journal_valid` | 现有按账户类型核对：`AVAILABLE` 变动 = `Σ delta_available`，`HELD` 变动 = `Σ delta_held`，`REVENUE` 变动 = `Σ delta_consumed`。`FUNDING_CLEARING` 不在其中，所以退出 journal 的 `delta_consumed` 必须为 0；需把 `delta_retired` 与 `FUNDING_CLEARING` 变动的对应关系加入核对 |
| `funding_journal_valid` 等按 journal `kind` 分支的校验 | 新增 `credit_bonus`、`cash_refund_hold`、`cash_refund_retire`、`cash_refund_release` 的合法形态；`journals.kind` 本身没有 CHECK 白名单，不会被拦，但这些校验函数会拒绝未识别的 kind |
| 按事件类型校验的函数（例如 `funding` 事件要求 `delta_held=0 AND delta_consumed=0`） | 为每种事件类型写明 `delta_retired` 的允许取值；新增事件类型 `refund_hold`、`refund_release`、`refund_retire` |
| 来源守恒式 | `available + held + consumed + retired = issued_amount`，包含 `wallet_sources_read.go` 的检查（现为三项之和） |
| `funding_lot_events` 前缀和核对 | 增加 `prefix_retired` |
| `check-wallet-sources` | 四类不一致检查加入第四桶；新增“父单据来源不成对”与 `unknown` 来源单列 |
| 027、028-U 延迟约束 | 同步 |
| 钱包读取、余额报表、请求资金分配展示 | 展示可用、冻结、已消费、已退出 |
| Reserve 的来源选择 | 只选 `available_amount > 0` 的来源，`retired` 永不进入 |
| 消费退款 | 只沿结算收费边退回，不涉及 `retired` |

不变量：`retired` 不能再次进入 Reserve，不能参与消费退款，不能被重复扣减；每次增加必须有对应现金退款单与 journal；未知提交不能重复写事件（§7.6）。

### 5.7 现金退款单

```sql
CREATE TABLE cash_refund_orders (
  id                 text PRIMARY KEY DEFAULT gen_random_uuid()::text,
  tenant_id          text NOT NULL REFERENCES tenants(id),
  lot_id             text NOT NULL,                  -- 原付费来源
  business_ref       text NOT NULL UNIQUE,           -- 唯一业务号
  coin_amount        bigint NOT NULL CHECK (coin_amount > 0),
  reason             text NOT NULL,
  state              text NOT NULL DEFAULT 'requested'
    CHECK (state IN ('requested','approved','transfer_executed','completed','cancelled','failed')),
  requested_by       text REFERENCES users(id),
  maker              text NOT NULL REFERENCES users(id),
  approver           text REFERENCES users(id),
  transfer_executor  text REFERENCES users(id),
  fiat_currency      text,                           -- 批准时冻结
  fiat_amount        bigint,                         -- 批准时冻结，系统计算值
  transfer_order_ref text,                           -- 转账指令编号，可在批准后补充
  transfer_proof_ref text,                           -- 已完成转账的凭证，转账完成后必填
  paid_fiat_amount   bigint,                         -- 线下实际转出金额
  hold_journal_id    text,
  retire_journal_id  text,
  release_journal_id text,
  created_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK (approver IS NULL OR (approver <> maker AND approver IS DISTINCT FROM requested_by)),
  CHECK (transfer_executor IS NULL OR transfer_executor <> approver),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id)
);
```

不提供删除；应用角色无 DELETE；终态（`completed`、`cancelled`、`failed`）不可再改。

## 6. 手动充值流程（范围 M）

线下付款与系统入账的顺序固定为：

```text
用户线下完成付款
→ 用户（租户 owner）或财务提交充值申请，或财务代录入
→ 财务操作员核对并记录：付款币种与金额、到账币数、赠送币数、业务号、凭证引用
→ 另一名财务审批员批准
→ 系统同一事务入账（付费来源，以及赠送来源）
```

- 提交申请不是已付款，申请本身没有任何钱包效果。
- 系统不在审批前入账。
- “线下转账”包括银行转账、支付宝转账、微信转账等外部付款，**都不是 aigate 自动验证的正式支付接入**。aigate 不实现支付清算，只保存付款事实、充值单、凭证和钱包入账。
- 当前范围 M 不支持正式支付宝或微信支付；回调、订单金额字段、实收与优惠金额、手续费属于 022/P。

保持不变：业务号唯一、凭证引用受控、重复审批幂等、角色分离。没有 `override`，没有一人完成的快捷路径；例外见 §5.1 单人运营。

## 7. 手动现金退款（范围 M）

场景：用户已用掉一部分币，要求把剩余部分的钱退回。平台线下转账，系统把对应币从钱包移出。

**上一版的缺陷**：批准时就把币永久移出，若线下转账失败，用户没收到钱却已失去币。本版改为**先冻结、转账确认后才退出**，转账失败可以无损释放。

| | 消费退款（现有） | 现金退款（本规格） |
|---|---|---|
| 含义 | 某次请求收费有误，币退回**钱包** | 币永久移出钱包，同时平台线下退法币 |
| 绑定对象 | 原结算 | 原付费来源 |
| 币的去向 | 回到原来源的可用桶 | 先进入冻结，转账确认后进入 `retired` |
| 现有实现 | `refund_orders` | 新增 `cash_refund_orders` |

### 7.1 资格

- 只针对 `basis_kind='paid'` 且 `refund_class='cash_original'` 的来源；赠送、兑换券、开账、未知基准来源不可现金退款。
- 只能退该来源的**可用**币：退款币数 `≤ available_amount`。已冻结（预占）与已消费的币不可退。
- 单次与累计退款的法币额不超过该来源的原付款。
- 一张退款单只针对一个来源；跨来源需分别建单。

### 7.2 状态机

```text
requested ──approve──▶ approved ──execute──▶ transfer_executed ──complete──▶ completed
    │                     │                        │
    └──cancel──▶ cancelled└──cancel / fail──▶ cancelled / failed
                                                    └──fail──▶ failed
```

| 迁移 | 操作者 | 钱包效果 | 约束 |
|---|---|---|---|
| `requested` | maker 创建 | **无**；只做资格预检与预览（可退上限、预计法币金额） | `requested_by` 可为租户 owner（客户申请）或财务 |
| `requested → approved` | approver | **冻结**：来源 `available −k`、`held +k`；租户 `AVAILABLE −k`、`HELD +k`；写 `refund_hold` 事件与 `cash_refund_hold` journal | 重新读取可用币校验；审批人 ≠ maker、≠ requested_by；计算并冻结 `fiat_amount`、`fiat_currency` |
| `approved → transfer_executed` | transfer_executor | 无 | 执行人 ≠ approver；填入 `transfer_order_ref`（转账指令编号，批准时尚无最终凭证） |
| `transfer_executed → completed` | transfer_executor 或另一名财务 | **退出**：`held −k`、`retired +k`；`HELD −k`、`FUNDING_CLEARING +k`；写 `refund_retire` 事件与 `cash_refund_retire` journal | 必须填 `transfer_proof_ref`（已完成转账的凭证）与 `paid_fiat_amount` |
| `approved → cancelled` | approver 或 maker | **释放**：`held −k`、`available +k`；写 `refund_release` 事件与 `cash_refund_release` journal | 尚未转账 |
| `approved / transfer_executed → failed` | 财务 | **释放**（同上） | 线下转账失败；必须记录原因 |

要点：

- **冻结的会计形态**：`cash_refund_hold` journal 为租户 `AVAILABLE −k`、`HELD +k`（同一租户两个账户，无清算账户参与）；`cash_refund_retire` 为租户 `HELD −k`、`FUNDING_CLEARING +k`；`cash_refund_release` 为租户 `HELD −k`、`AVAILABLE +k`。全部落在现有账户类型上，`AVAILABLE` 账户余额始终等于 `Σ available_amount`、`HELD` 等于 `Σ held_amount`。
- **冻结期间币不可消费**：冻结的币在 `held` 桶，Reserve 不会选它们，所以批准后到转账完成之间不会被花掉，也不会出现“币被花了才发现要退”。
- **`transfer_order_ref` 与 `transfer_proof_ref` 分开**：前者是批准或执行时的转账指令编号，后者是转账完成后的已完成凭证。批准时没有最终凭证，不能当作付款证明。
- **金额不一致**：`paid_fiat_amount` 与冻结的 `fiat_amount` 必须相等才允许 `complete`；不一致时只能 `cancelled/failed` 释放后重新建单，**不允许在 `complete` 时改金额**，也不允许退款成功后更正已退出的币数。需要更正（例如多转或少转）作为单独的财务调整，在系统外处理并留审计，不通过修改该单。
- **部分执行**：一张单的币数一次性全额处理，不支持“先退一半”。部分退款用多张单，累计规则见 §7.3。
- **取消**：只能在 `approved` 且转账未发生时取消；`transfer_executed` 之后只能 `failed`（转账确认失败）或 `completed`。
- **完成后更正**：`completed` 是终态；退出的币不可返还。若发现退错，通过新的赠送或充值单处理，不回滚。

### 7.3 退款法币金额的整数规则

记来源的原付款为 `F`（receipt `fiat_amount`），发行币为 `C`（`issued_amount`），该来源在**批准时点**已累计退出与冻结的币数为 `w_old`（`retired_amount + 冻结中的退款币数`），本次为 `k`，`w_new = w_old + k`。

```text
fiat_refund = floor(F × w_new / C) − floor(F × w_old / C)
```

- 乘法用 `big.Int`（Go）或 `numeric`（SQL）；`F`、`C` 均 ≤ 2⁶³−1，乘积 < 2¹²⁶。
- 向下取整发生在**累计位置**，所以多次部分退款的法币总额恰为 `floor(F × w_total / C)`，不会因逐次取整累计多退，也不会漏退；全部退完总额不超过 `F`。
- 计入“冻结中”的退款币数，防止两张并发的单各自按同一个 `w_old` 计算后合计多退；两张单对同一来源并发审批时，来源余额行锁保证串行，后者读到前者已冻结的数。
- 这是 `internal/productvalue` 的“先映射绝对端点再相减”算法的直接应用：`productvalue.New(F, C)`，区间 `[w_old, w_new)` 的价值即 `fiat_refund`。实施时复用并由独立手算向量交叉验证。
- 币种按原付款，不按当前汇率换算；汇率变动不改变退款金额。
- `cancelled/failed` 释放的币不计入后续 `w_old`。

### 7.4 并发与竞态

批准事务锁序沿用 Reserve：租户 → 账户按 ID → 来源余额按 `lot_id`。以下场景必须在**真实 PostgreSQL** 上验证，设计文字不能代替测试：

- Reserve 与退款审批并发；
- 退款审批与消费结算并发；
- 退款审批与消费退款并发；
- 两张退款单并发审批同一来源；
- 同一退款单重复审批、重复完成；
- 事务提交前连接断开；
- 进程重启后的未知提交；
- 不出现死锁、不出现负可用余额；
- 冻结后 Reserve 看不到被冻结的币。

### 7.5 未知提交与恢复

- 批准与完成各是一个数据库事务。连接在提交前断开，调用方**只按业务号读回单据状态与事件**，不自动重做；状态已前进则返回现状，未前进才允许人工重试同一迁移。
- 迁移用条件更新（`WHERE state = <前态>`），同一单据的同一迁移最多成功一次；`refund_hold/retire/release` 事件以 `(refund_order_id, kind)` 唯一，防止重复写入。
- 恢复任务不涉及现金退款；不会自动完成或释放。长时间停留在 `approved/transfer_executed` 的单据由告警提示人工处理（接入 024-A 的站内告警）。
- 释放与退出互斥：一张单只能走其中一条，由状态机与唯一事件约束保证。

### 7.6 凭证字段

- `business_ref`：退款唯一业务号，创建时填写。
- `transfer_order_ref`：线下转账**指令**编号，不是付款证明。
- `transfer_proof_ref`：**已完成**转账的凭证，完成时必填，沿用 `security.ValidReference` 的受控引用规则。
- 系统不触发、不验证任何外部转账。

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
| **按来源分配的客户付款基准** | 对每条 `funding_settlement_lots`，取 `charged_amount × fiat_amount / issued_amount`（所属 receipt，不受 `retired` 影响：已退出的币从未被消费，不参与此计算），按 `fiat_currency` 分列；`grant`、`opening` 计 0；`unknown` 为未知 |
| 采购成本 | 供应商币种原值；折算依赖供应商币种规格 |
| 毛利参考 | 付款基准 − 已知采购成本，同币种比较；任一项未知或缺汇率时为 null |

**这不是会计收入。** HLD 已说明业务流水不宣称法定会计总账；赠送币的收入确认、未使用余额的会计处理由财务和法务决定。该口径只作为经营报表与毛利参考的输入。

按来源分配的付款基准是对现有事实的只读计算，不改动请求路径或结算写入。现金退款退出的法币不进入该口径，作为单独的“已线下退还法币”列按币种汇总，供财务对账。

## 11. 对其他规格的影响

| 规格 | 影响 |
|---|---|
| [030](../030-model-aliases-group-multipliers/spec.md) | 分组倍率编译的目标单位改为币 |
| [032](../032-phase2-text-data-plane/spec.md) | 客户侧周期用量上限和缓存命中计费以币计 |
| [033](../033-text-dynamic-routing/spec.md) | 最低毛利需要“币售价折法币”与“供应商成本折法币”同一时点比较，依赖锚定和供应商币种规格 |
| 022 | 范围 P：支付订单放开付款与到账分离、捕获产生付费与赠送两个来源（同 §5.4 结构：付费来源与赠送来源两个 receipt、两笔 journal、同一捕获为父单据，`source_kind` 用真实捕获类型与 `capture_bonus`；`journals.operation_id` 全局唯一，两笔 journal 不能共用同一个 operation）；支付宝回调有多个金额字段（订单金额、实收、优惠后实付等），`payment_captures.amount` 存哪一个必须在支付规格中写死，并以支付宝官方文档为准 |
| 023-C 订阅 | 套餐额度作为新的来源类型，以币计；到期使用本规格的第四桶机制 |

## 12. 后置

- 币的到期、限定模型范围的币、充值分组优惠；
- 真实支付与美元收款：依赖正式支付商，第四阶段；
- 手续费：支付渠道扣费不影响钱包入账，只在财务对账层分开记录。

## 13. 决策点

用户已确认方向：**币为对外单位、手动充值与手动退款优先、支付后置**。下表每项标注**状态**：`建议中` 表示尚无裁决；`影响模型` 表示它会直接改变钱包模型或 guard，冻结前必须有结论，不能停留在建议。

| 编号 | 问题 | 建议 | 状态 |
|---|---|---|---|
| D-034-1 | 锚定币种与比值 | 1 币 = 1 CNY：数值零变化 | 建议中 |
| D-034-2 | 赠送是独立来源还是合入付费来源 | 独立来源（§5.4） | 建议中；规格已按此设计 |
| D-034-3 | 取整 | 退款法币额按累计位置向下取整（§7.3） | 建议中 |
| D-034-4 | 同一充值单两来源的消费先后 | 同时刻先消耗赠送来源（§5.5） | **影响模型**，改 Reserve 与 FIFO guard |
| D-034-5 | 手动美元付款 | 管理员录入美元金额与到账币数，人工汇率说明仅作备注（§5.2） | 建议中 |
| D-034-6 | 是否提供“约合”展示 | 提供，带版本，标注仅供参考 | 建议中 |
| D-034-7 | 显示名 | 平台可配置 | 建议中 |
| D-034-8 | 已有余额重估 | 不实现；向上调整用批量赠送 | 建议中 |
| D-034-9 | 单人运营 | 生产不允许；内测或应急例外，默认关闭，审计标记（§5.1） | **影响模型**，决定是否需要例外开关与标记字段 |
| D-034-10 | 赠送币是否永不过期 | 范围 M 永不过期；到期属范围 V | **影响模型**，到期需要事件类型与扫描 |
| D-034-11 | 单次最大充值与最大赠送 | 在 `int64` 溢出检查之外由运营另设上限 | 建议中 |
| D-034-12 | 现金退款是否允许部分扣减 | 一张单一次性全额；部分退款用多张单，累计规则（§7.3） | **影响模型**，决定状态机与 `w_old` 计算 |

## 14. 合规

这属于储值或虚拟币发行，美元收款涉及跨境。赠送币的收入确认、未使用余额的会计处理、退款政策、税务与监管需要财务和法务的**书面意见**。本规格不对这些问题给出结论；冻结前必须取得。

若只是内测开发，可先完成设计与测试环境实现，但状态只能标记为：**开发环境可用，未完成生产财务和合规验收。** 不得标记为生产储值能力已通过验收。

## 15. 冻结前阻断项

034 只有在以下全部完成后才能标记为“已冻结”。当前状态逐项列出：

| # | 阻断项 | 当前状态 |
|---|---|---|
| 1 | 现金退款的完整状态机 | 已写入 §7.2，**待评审** |
| 2 | 转账凭证在审批前还是审批后生成 | 已区分：`transfer_order_ref`（指令）与 `transfer_proof_ref`（完成凭证，完成时必填），§7.2、§7.6 |
| 3 | 转账失败与未知提交处理 | 失败走 `failed` 并释放冻结（§7.2）；未知提交只读回（§7.5）；**需真实 PostgreSQL 验证** |
| 4 | `requested_by`、`maker`、`approver`、`transfer_executor`、`recipient` 定义 | 已写入 §5.1 |
| 5 | 用户自助、管理员代充、运营赠送的权限矩阵 | 已写入 §5.1；**钱包按租户**，不存在员工个人钱包 |
| 6 | `basis_kind` 与 `source_kind` 的允许组合 | 已由 §5.3 表格枚举，并要求单一 `CHECK` |
| 7 | 付费来源与赠送来源的父单据关联 | 已写入 §5.4（`parent_ref`、成对性约束、journal `operation_id` 不可共用） |
| 8 | `retired` 桶涉及的全部 guard 与验证器 | 清单见 §5.6；**需逐项修改并真实 PostgreSQL 验证** |
| 9 | 手动美元付款是否记录人工汇率 | 仅 `fx_note` 备注，不是汇率版本，不被计算引用（§5.2） |
| 10 | D-034-1…12 最终裁决状态 | 见 §13，**4 项影响模型，尚无裁决** |
| 11 | 财务与法务意见 | **未取得** |
| 12 | 034 实现完成前不修改现有运行时 | 见下 |

**实现前不修改现行运行时**：本规格合入后，现有的钱包、人工核款、兑换券、消费退款、027 来源核对和 028-U guard 保持不变，仍以人民币单币种为现行合同；任何对应的迁移、guard 或入口变更必须在 34.0 冻结后按 §17 分片进行，并各自带专用 PostgreSQL 验证。

### 评审意见逐条状态

状态取值：`已满足`、`需要修改规格`（已修改）、`后置到 022/P`、`后置到 V`、`需要财务确认`、`需要法务确认`、`需要真实 PostgreSQL 验证`。

| 评审项 | 状态 | 说明 |
|---|---|---|
| 现金退款状态机不完整 | 需要修改规格（已修改）；需要真实 PostgreSQL 验证 | 改为先冻结、转账确认后才退出，§7.2 |
| 双人审批与单人运营混为一谈 | 需要修改规格（已修改） | §5.1：生产不允许；例外默认关闭并带审计标记；D-034-9 待裁决 |
| 发起人、制单人、审批人、执行人拆字段 | 需要修改规格（已修改） | §5.1。注：钱包按租户，“给员工充值”即给其租户入账 |
| `unknown` 的 `source_kind` 未定 | 需要修改规格（已修改） | `manual_unknown`，§5.3 |
| 两个来源的父关联更强 | 需要修改规格（已修改）；需要真实 PostgreSQL 验证 | §5.4；并发现 `journals.operation_id` 全局唯一，两笔 journal 不能共用 |
| 手动充值顺序 | 已满足 | §6 |
| 正式支付不混入当前范围 | 已满足；回调与金额字段后置到 022/P | §6、§11 |
| 手动美元付款与汇率记录 | 需要修改规格（已修改）；汇率版本后置到 V | §5.2 `fx_note` |
| `retired` 桶的完整 guard | 需要修改规格（已修改）；需要真实 PostgreSQL 验证 | §5.6 表格，新增 journal kind 校验项 |
| 现金退款与 Reserve 的竞态 | 需要真实 PostgreSQL 验证 | §7.4 列出场景，尚未执行 |
| 决策点未冻结与路线图状态不一致 | 需要修改规格（已修改） | §13 标注状态；路线图改为“设计与决策属第一阶段，实现属 MVP 前” |
| 财务与法务意见 | 需要财务确认；需要法务确认 | §14，未取得 |
| “现金基准收入”不应叫收入 | 已满足 | §10 |

## 16. 验收

以下带“PG”的项必须在专用 PostgreSQL（`TEST_DATABASE_URL`，`127.0.0.1:55439`）上通过；跳过不算完成。设计文字不能代替这些测试。

| 组 | 断言 | PG |
|---|---|---|
| 单位 | 迁移后钱包 journal、`funding_receipts.currency`、售价版本为 `COIN`；数值与迁移前一致（锚定 1:1） | PG |
| 类型区分 | 法币金额与币金额在 Go 中是不同类型，编译期禁止互相赋值；API 字段与文档分别标明单位 | — |
| 角色约束 | `approver ≠ maker`、`approver ≠ requested_by`、`transfer_executor ≠ approver` 由数据库拒绝；客户自助申请不产生任何钱包效果；审批前无入账 | PG |
| 单人例外 | 默认关闭；开启后同源账号完成的单据带 `single_operator_exception` 审计标记并在核对页单列 | PG |
| 手动充值基准 | 付 90 得 100：receipt 记法币 90、币 100；`paid` 缺法币字段被拒；`grant`/`unknown` 带法币字段被拒；receipt 不可修改 | PG |
| 来源组合 | `(source_kind, basis_kind, refund_class)` 表外组合被 CHECK 拒绝；`manual_unknown` 不可现金退款 | PG |
| 付费与赠送 | 一单两来源两笔 journal，`operation_id` 不冲突；重复审批只入账一次；`bonus_amount=0` 不产生赠送来源；任一失败整体回滚；**直接 SQL 造出只有一个来源的已批准单被 COMMIT 前约束拒绝**；`check-wallet-sources` 报告不成对 | PG |
| 金额一致 | 付费 receipt = `amount`，赠送 receipt = `bonus_amount`；`amount + bonus_amount` 溢出被拒 | PG |
| 消费顺序 | 同一充值单同时刻先消耗赠送来源；不同充值单严格 FIFO | PG |
| 守恒 | 四桶 `available + held + consumed + retired = issued` 在所有操作后成立；`AVAILABLE`/`HELD` 账户余额继续等于 Σ 来源桶；`check-wallet-sources` 无不一致 | PG |
| 现金退款资格 | 赠送、兑换券、开账、未知基准来源被拒；冻结与已消费部分不可退 | PG |
| 现金退款状态机 | 逐条迁移；冻结后 Reserve 看不到被冻结的币；`cancelled/failed` 释放回可用；`completed` 为终态；完成时 `paid_fiat_amount` 与冻结 `fiat_amount` 不等被拒；`transfer_proof_ref` 缺失被拒 | PG |
| 现金退款金额 | 手算向量：多次部分退款法币总额等于 `floor(F × w_total / C)` 且不超过 `F`；冻结中的币计入 `w_old`；`int64` 边界不溢出；USD 与 CNY 各一组 | 纯函数 + PG |
| 幂等与未知提交 | 重复审批、重复完成不二次冻结或退出；业务号唯一；`(refund_order_id, kind)` 事件唯一；提交前断连后只读回，不重复写事件 | PG |
| 竞态 | §7.4 全部场景：Reserve、结算、消费退款与退款审批并发；两张单并发审批同一来源；无死锁、无负可用余额 | PG |
| 请求路径 | Reserve、结算、消费退款不读取任何汇率（静态检查与真实 PG 用例） | PG |
| 报表 | 付款基准按币种分列；`unknown` 基准与缺汇率时毛利为 null | PG |
| 客户展示 | 客户能看到自己的付款金额与到账币数；看不到供应商成本 | — |

## 17. 分片

| 片 | 内容 | 依赖 |
|---|---|---|
| 34.0 | 本规格评审，裁决 D-034-1…12，取得财务与法务书面意见，满足 §15 全部阻断项 | — |
| 34.1 | `currency` 约束改为 `COIN`，类型区分；数值不变 | 34.0 |
| 34.2 | 角色字段、付款基准、来源组合 CHECK、两来源与父关联、消费顺序 | 34.1 |
| 34.3 | 第四桶 `retired`：守恒式、事件类型、journal kind 校验、`check-wallet-sources`、guard 更新 | 34.2 |
| 34.4 | 现金退款单：状态机、整数规则（复用 productvalue）、审批事务、HTTP 与控制台、告警 | 34.3 |
| 34.5 | 报表付款基准与客户充值记录展示 | 34.2 |
| 34.6 | “约合”展示与显示名配置 | 34.1 |
| 后置 P | 支付订单与捕获的两来源、支付宝金额语义、手续费 | 022 |
| 后置 V | 汇率版本表、币到期、限定模型范围、重估 | 订阅规格 |

34.2、34.3、34.4 改动入账与来源核心，由集成人串行合并，并在专用 PostgreSQL 上验证。

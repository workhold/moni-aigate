# 034：虚拟币钱包与手动充值、手动现金退款

2026-11-10；状态：**已冻结**（2026-11-14 经开发方向审查评审冻结，PR 合并即生效；D-034-1…13 全部裁决，见 §13）。本规格不是 034 已实现、币钱包已实现、现金退款已实现或生产储值能力已通过验收的证据；实现按 §17 分片进行，并须在专用 PostgreSQL 上按 §16 验收。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#4-第一阶段固定版本-new-api-核心业务能力闭环)，MVP 前实现手动充值与手动退款；设计与决策属第一阶段。参考：New API 固定提交 `e449cc700eb569cdfc2199cb73e97de46944f43c` 的 quota、充值与管理员调额代码。

## 1. 目标

**对外只暴露虚拟币（下称“币”）。** 客户充值进来的是币；钱包、模型售价、预占、结算、Key 额度、周期预算都以币计；法币只出现在付款记录、对账和财务报表中。

前期运营方式已确认：**用户线下付款，平台财务手动充值；退钱也是手动。** 支付宝等支付平台接入后置。因此本规格按两个范围分层：

| 范围 | 内容 | 状态 |
|---|---|---|
| M（手动） | 币单位、手动充值含赠送、手动现金退款扣减、客户展示、报表基准 | 本规格实施范围 |
| P（支付） | 支付订单与捕获产生付费和赠送来源、支付宝回调金额语义、手续费 | 后置，属 022，本规格只留接口 |
| V（扩展） | 币到期、限定模型范围、已有余额重估、供应商币种与汇率（采购侧） | 后置，附触发条件 |

要解决的问题：

1. 人民币与美元客户共用一套价格表；
2. 客户不盯汇率，只看“付了多少、得到多少币”；
3. 促销（充值赠送、档位）可以直接表达；
4. 汇率不进入请求路径；
5. 每一枚币都能追溯到付款事实，支持精确的现金退款；
6. 复用现有钱包、来源 lot、预占、结算，**不建第二套钱包**。

不解决：

- **平台的汇率风险。** 币价按法币币种分别记录后，另一币种的供应商成本仍随汇率浮动。这由重新定价和第三阶段最低毛利过滤处理，见 §10。
- **供应商侧币种。** 采购账户、成本版本和跨渠道比价的币种与汇率表由单独规格处理（供应商币种规格，待写）。

## 2. 与 New API 的对照

依据为固定提交的源码。

| 机制 | New API | aigate 取舍 |
|---|---|---|
| 统一整数额度 | `common/constants.go:22` `QuotaPerUnit = 500000`；余额、Key 额度、日志同一单位 | **采纳**：一种整数单位贯穿钱包 |
| 全局换算可改 | `model/option.go:692` 管理员可直接改 `QuotaPerUnit`，立即作用于一切，没有版本与审批 | **不照搬**：币价目标为**追加式版本**，未来调整要审批、不得回溯、不改任何币数；MVP 不提供调价入口。已持币客户的退款按 §7.3 取 `min`，不因币价上调而变多。见 §5.8 |
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
| 币价 | 1 币的法币价值，按法币币种分别记录（`CNY`、`USD`），是**追加式版本**；MVP 只记录创世/买入币价，调价治理后置（§5.8） |
| 创世币价 | CNY：1 币 = 1 CNY（D-034-1）。美元在没有美元币价版本之前不可录入美元付款 |

创世币价使所有已存数值保持不变：现有 `funding_lots.issued_amount`、`price_versions`、`api_keys.lifetime_limit`、周期预算原本就是人民币微元，只改单位标签和 `currency` 约束，不缩放任何数据或测试夹具。

币价在各处的作用，**只有退款读它**：

| 场景 | 是否读币价 |
|---|---|
| 请求路径（预占、结算、消费退款） | **否**。全部以币计，没有汇率 |
| 手动充值 | 否。付款额与到账币数由运营录入，不由币价计算；入账时只**记录**批准时点的币价版本作为该来源的买入币价 |
| 现金退款 | **是**。退款额取 `min(按当前币价, 按原付款)`，§7.3 |
| 报表名义值 | 仅作参考，标注所用版本 |

MVP **不开放币价调整**：币价版本只用于记录创世币价和每个付费来源的买入币价，当前退款使用的币价与买入币价相同。后续若开放调价，调价**不改变任何币数**，也不改变模型价格（模型价格以币计），只影响之后新入账来源记录的买入币价以及已持币客户的现金退款；调价必须通过追加版本、审批、预览和不可回溯的治理流程。

**解释权**：现金退款额由系统按 §7.3 核算，规则以平台公布的版本为准，客户只能提单、不能指定金额。该规则必须在客户界面公示（§8），包括“币价下调时退款可能少于原付款”。

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

**`'CNY'` 不只在 CHECK 约束里。** 对 `migrations/current_mvp.sql` 的核对显示，字面量 `'CNY'` 还写在函数体中，改单位必须同步修改，不能只改约束：

| 位置 | 内容 |
|---|---|
| `funding_journal_valid`（约 3797 行） | `j.currency='CNY' AND f.currency='CNY'` |
| 兑换券与开账入账校验（`funding_journal_valid` 约 3797 行、`wallet_opening_journal_valid` 约 6387 行；兑换券另有 Go 侧 `voucher_query.go`） | `f.currency='CNY' AND j.currency='CNY'` |
| `payment_sandbox_journal_valid`、沙箱 capture 校验（约 4201–4202 行） | journal 与 receipt 币种为 `'CNY'` |
| 价格与成本写入函数（约 2407、2416、2445 行） | 固定写入 `'CNY'` |
| 计量快照与报价规范化（约 3425、6134 行） | `currency` 为 `'CNY'` |
| 采购观察（约 576 行） | `currency` 必须为 `'CNY'` |
| Go：`funding.go:229`、`funding_sandbox.go:69`、`voucher_query.go:377`、`catalog/pricing_publish.go`、`pricing_vector.go`、`public_catalog.go`、`reporting/report.go:92` | 字面量 |

采购观察、成本版本和计量快照里的 `'CNY'` 属于供应商侧或计量规范化，**不在本规格修改**；只改客户钱包与客户售价相关的那几处。34.1 一片先逐处分类，再动手。

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

**单人运营（D-034-9，已裁决）**：做成**管理员界面的开关，默认开启**。开启时，一个**管理员（`admin`）账号**可以对同一张单据既制单又审批（以及执行转账），适合一个人运营；开启、关闭或修改该开关都必须二次确认/近期重新认证并写入不可变审计。关闭时恢复双人控制。所有财务运营操作最高权限归 `admin`。

| | 开关关闭 | 开关开启（默认） |
|---|---|---|
| 制单 | `finance_operator` | `finance_operator` 或 `admin` |
| 审批 | `finance_approver` | `finance_approver` 或 `admin` |
| 制单人与审批人 | 必须不同账号 | **可以是同一账号**（仅限 `admin`） |
| 执行转账与审批人 | 必须不同账号 | 可以相同（仅限 `admin`） |
| 非 `admin` 的财务角色 | 按现状 | 按现状，仍不可自批 |

- 开关本身是追加式的平台设置版本（`wallet_operator_settings`：版本、`single_operator`、修改人、时间、原因），修改要留审计；当前值取最新版本。初始版本 `single_operator = true`。管理界面必须显示当前值、影响范围和风险提示；写入前要求二次确认/近期重新认证。
- **每张单据在批准时快照开关**到单据的 `single_operator` 列。放宽只对快照为真且双方均为 `admin` 的单据生效；事后关闭开关不会让已批准单据失效，也不会放宽其他单据。
- 凡是制单与审批是同一账号的单据，审计固定带标记 `single_operator_exception`，并在财务核对页单列。**这不是双人审批**，文档与界面不得如此描述。
- **默认开启意味着双人控制在默认状态下不强制**。建议在生产环境上线前改为关闭，这是运维建议，不是规格强制。
- 适用范围：手动充值、消费退款、现金退款、币价调整。**不**自动扩展到对账、恢复、采购提案、兑换券批次：它们各自有 maker/checker 约束（`reconciliation_cases`、`recovery_gate`、`procurement_cost_proposals`、`voucher_batches`），本规格不改，若要一并放宽须另行裁决。

**必须同步修改的现有位置**（已逐处核对，见本节末表）：`role()` 现在是精确匹配（`actual != required`），`admin` 当前完全不能充当财务角色；因此不是只放宽约束，而是五类位置一起改：

| 位置 | 现状 | 变化 |
|---|---|---|
| `internal/billing/store.go` `role()` | 要求 `platform_role` 恰等于所需角色 | 对上表中开关开启的场景，新增“`admin` 且快照允许”的分支；不改变开关关闭时的行为 |
| `internal/billing/orders.go:28,77,80,124,180,183` | 各自要求 `finance_operator` / `finance_approver` | 按上表放宽 |
| `internal/billing/orders.go:84,187` | `self_approval` 拒绝 | 仅当快照为真且双方为 `admin` 时放行 |
| `internal/billing/funding.go:72` | SQL 授权 `$1<>$2 AND approver 角色 AND requester 角色` | 同上 |
| `funding_journal_valid`（`current_mvp.sql` 约 3798 行） | 每次入账校验都含 `c.approver<>c.requester` | 改为 `(c.approver<>c.requester OR c.single_operator)`。**不改它，开关开启后同一账号批准的充值会在提交时被 guard 拒绝** |
| `credit_orders_check`、`refund_orders_check` | `approver IS NULL OR approver <> requester` | 同上，并新增 `single_operator` 列 |

### 5.2 付款基准

`credit_orders` 当前只有币数 `amount`、`business_ref`、`proof_ref`。追加：

```sql
ALTER TABLE credit_orders
  ADD COLUMN requested_by  text REFERENCES users(id),
  ADD COLUMN basis_kind    text   NOT NULL DEFAULT 'paid',
  ADD COLUMN fiat_currency text,
  ADD COLUMN fiat_amount   bigint,
  ADD COLUMN fx_note       text,                 -- 美元付款时人工确认的汇率说明，可空
  ADD COLUMN bonus_amount  bigint NOT NULL DEFAULT 0,
  ADD COLUMN transfer_ref  text;                 -- 线下收款流水号
-- 回填既有行：basis_kind='paid'，fiat_currency='CNY'，fiat_amount=amount（创世币价 1:1）
UPDATE credit_orders SET fiat_currency='CNY', fiat_amount=amount WHERE fiat_currency IS NULL;
ALTER TABLE credit_orders
  ADD CONSTRAINT credit_orders_basis_check CHECK (basis_kind IN ('paid','grant','unknown')),
  ADD CONSTRAINT credit_orders_bonus_check CHECK (bonus_amount >= 0),
  ADD CONSTRAINT credit_orders_fiat_check CHECK ((basis_kind = 'paid') = (fiat_currency IS NOT NULL AND fiat_amount IS NOT NULL AND fiat_amount > 0)),
  ADD CONSTRAINT credit_orders_currency_check CHECK (fiat_currency IS NULL OR fiat_currency IN ('CNY','USD'));
```

`amount` 保持“付费部分到账币数”，`bonus_amount` 为随单赠送币数。“付 90 得 100”：`fiat_amount=90`、`amount=100`；“付 100 得 100 送 20”：`fiat_amount=100`、`amount=100`、`bonus_amount=20`。

溢出与上限：`amount + bonus_amount ≤ 2⁶³−1`，在创建与审批两处检查，沿用现有 `amount_overflow` 错误；`amount` 和 `bonus_amount` 分别不得超过 1,000,000,000,000 微币，部署配置只能进一步收紧，不能放宽（D-034-11）。

**手动美元付款**（范围 M，D-034-5）：管理员同时录入美元金额与到账币数；到账币数**不由系统计算**，请求路径更不读取汇率。`fx_note` 只保存人工确认的汇率文字说明（可空），**不是汇率版本，也不被任何计算引用**。若 `fx_note` 为空，报表只能说“支付了多少美元、到账多少币”，不能声称有正式汇率依据。这不是自动汇率发行；自动汇率与汇率版本属于范围 V。付款凭证由 `credit_orders.proof_ref` 与 `business_ref` 承担，线下收款流水号放 `transfer_ref`，见 §5.3。

### 5.3 receipt 的付款基准与允许组合

`funding_receipts` 追加基准列。receipt 写入后不可修改：应用角色对来源关系只有 SELECT/INSERT 权限（027 角色设计），新列沿用该权限，不新增 UPDATE 授权。

```sql
ALTER TABLE funding_receipts
  ADD COLUMN basis_kind    text,
  ADD COLUMN fiat_currency text,
  ADD COLUMN fiat_amount   bigint,
  ADD COLUMN parent_ref    text,                 -- 父单据 ID，见 §5.4
  ADD COLUMN buy_rate_version_id text;           -- 买入币价版本，仅 paid 来源，见 §5.8
-- 回填后再加 NOT NULL 与三元组 CHECK（见下）
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

- **`unknown`**：允许通过人工充值单创建（财务只知币数、不知付款额，例如他人代为发币）。永远不可现金退款；毛利与付款基准为 null；必须有业务号与凭证引用，和 `paid` 的审计要求相同；`check-wallet-sources` 把它按普通来源核对五桶守恒，同时在输出里单列 `unknown` 来源总额，避免被误读成已付款。
- `cash_original` 当且仅当 `source_kind='manual_credit'`（范围 P 再加真实支付捕获类型）。
- **三元组约束的实现方式**：`basis_kind`、`source_kind` 在 `funding_receipts`，`refund_class` 在 `funding_lots`（已核对：`funding_receipts` 没有 `refund_class` 列）。PostgreSQL 的普通 `CHECK` 不能查询另一张表，所以**不能用一个 CHECK 同时约束三者**。按两层实现：
  1. **单表 CHECK 约束各自所在表内的组合**：
     - `funding_receipts`：`CHECK ((source_kind, basis_kind) IN (('manual_credit','paid'), ('manual_bonus','grant'), ('manual_unknown','unknown'), ('voucher_grant','grant'), ('wallet_opening','opening'), ('sandbox_capture','paid')))`，并约束 `fiat_*` 与 `basis_kind` 的对应关系；
     - `funding_lots`：`CHECK ((source_kind, refund_class) IN (('manual_credit','cash_original'), ('manual_bonus','nonrefundable'), ('manual_unknown','nonrefundable'), ('voucher_grant','nonrefundable'), ('wallet_opening','nonrefundable'), ('sandbox_capture','sandbox_original')))`，取代现有 `(source_kind='sandbox_capture') = (refund_class='sandbox_original')`。
     因为 `source_kind` 在两张表里都有，且 `wallet_lot_valid` 要求 `(l.source_kind, l.source_id) = (f.source_kind, f.source_id)`，所以两张表各自的 CHECK 合起来就固定了三元组，不需要跨表引用。
  2. **清算账户与 receipt/lot 的对应**由现有的 journal 校验函数核对，不放进 CHECK：`funding_journal_valid` 已按 `source_kind` 分支检查清算账户，需为 `manual_bonus`、`manual_unknown` 新增分支。
  - 新增来源类型必须同时更新这两个 CHECK、`wallet_lot_valid` 与本表。
  - 备选方案（不采纳）：把 `basis_kind` 复制到 `funding_lots` 做单表 CHECK；建来源类型目录表加复合外键。前者造成两处冗余需要保持一致，后者增加一张几乎不变的表，现有的两表 CHECK 加 `wallet_lot_valid` 已足够。
- 现有 `funding_lots.refund_class` 约束（`IN ('nonrefundable','sandbox_original')`，且 `(source_kind='sandbox_capture') = (refund_class='sandbox_original')`）同步放开，追加 `'cash_original'`。

`buy_rate_version_id`：`paid` 来源**必填**，且所引用版本的 `fiat_currency` 必须等于该来源的 `fiat_currency`；其余基准类别必须为空。入账事务在同一事务内以数据库时刻选取该币种当前生效的币价版本；**该币种没有版本时拒绝入账**（失败关闭，不使用默认值）。

现有行回填：`manual_credit` 为 `paid`、`fiat_currency='CNY'`、`fiat_amount=amount`（创世币价 1:1）、`buy_rate_version_id` 指向 CNY 创世版本；`sandbox_capture` 同为 `paid`；`voucher_grant` 为 `grant`；`wallet_opening` 为 `opening`；所有现有行 `parent_ref = source_id`。

**迁移机制**：`migrations/current.go` 的 `ApplyCurrent` 对空库安装 `current_mvp.sql`，对已安装的当前基线追加 `current_*.sql`（`current_001_append_path.sql` 是追加路径标记）。本规格的所有 DDL 以新的 `current_NNN_*.sql` 追加，并加入 `VerifyCurrent` 的校验计划与 checksum；MVP 前也可直接重建开发库（ADR-005）。

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

1. `parent_ref` 是父关联的唯一载体。`UNIQUE(source_kind, source_id)` 因 `source_kind` 不同天然成立；两个来源共同通过 `(source_id = parent_ref = credit_orders.id)` 绑定。

   **字段职责固定如下，不再使用 `basis_ref`：**

   | 字段 | 所在 | 职责 |
   |---|---|---|
   | `parent_ref` | `funding_receipts` | 父业务单 ID（充值单或，范围 P 的支付捕获） |
   | `business_ref` | `credit_orders` | 唯一业务号 |
   | `proof_ref` | `credit_orders` | 付款凭证（受控引用） |
   | `transfer_ref` | `credit_orders` | 线下收款流水号，可空 |
   | `fx_note` | `credit_orders` | 人工汇率说明，仅备注，不被任何计算引用 |
2. 付费 receipt 金额必须等于 `credit_orders.amount`，赠送 receipt 金额必须等于 `credit_orders.bonus_amount`。由两个 DEFERRABLE 约束触发器在 COMMIT 前核对，沿用 `funding_journal_valid` 的风格。
3. **成对性约束**：充值单 `state='approved'` 时，必须恰好存在一个 `manual_credit` receipt；`bonus_amount > 0` 时还必须恰好存在一个 `manual_bonus` receipt，`bonus_amount = 0` 时不得存在。该约束同样在 COMMIT 前核对。
4. 两个来源与两笔 journal 在**同一事务**创建，任一失败整体回滚；不存在“只有一个来源”的可提交状态。每个来源是对 `ApplyFundingTx` 的一次独立调用。**消费顺序由 §5.5 的“先付费、后赠送”规定，并有确定性保证，不依赖两次时钟读数的巧合。**
5. **重复审批**：充值单行 `FOR UPDATE`，已 `approved` 时读回两个 receipt 并校验第 2、3 条，通过则直接返回，不创建新来源。
6. **一个存在、另一个缺失**（直接 SQL、恢复异常或 guard 缺陷导致）：`check-wallet-sources` 增加“父单据来源不成对”检查类，报告该充值单 ID 并进入人工审查；系统**不自动补建**缺失的来源，因为补建等于再次入账。

### 5.5 消费顺序（D-034-4，已裁决）

**裁决**：同一充值单内，**付费来源先消耗，赠送来源后消耗**。理由是产品意图：赠送是为了让用户多用，不是为了保住付费币的现金退款余额。**不改任何 FIFO 规则**：沿用严格 FIFO，不同充值单之间仍按时间先后。

这条规则与此前草案里的“赠送先消耗”（原方案 A）**方向相反**，原方案 A 与产品意图不符，已删除。原方案 C（赠送来源时间戳早一微秒）同样被删除：它让赠送先消耗。

**现有排序（本规格不修改）**：排序键 `(fifo_at, id)` 在以下位置，全部保持不变：

| 位置 | 内容 |
|---|---|
| `lot_plan.go` `planReserveFundingLots` | Go 排序，要求 `fifoAt` 微秒精度 |
| `lot_runtime.go:150`（文本 Reserve） | SQL 按 `lot_id` 取候选后由 Go 排序 |
| `media.go:141`（媒体 Reserve） | SQL `ORDER BY l.fifo_at, l.id` |
| `wallet_reservation_fifo_guard` | 每条分配边写入前取 `ORDER BY l.fifo_at, l.id` 的第一个可用来源并要求一致 |
| 索引 `wallet_lots_fifo` | `(tenant_id, fifo_at, id COLLATE "C")` |

另有 `wallet_lot_valid` 要求 `lot.fifo_at = receipt.created_at`。

**确定性保证**。两个来源是对 `ApplyFundingTx` 的两次独立调用，各自读一次 `clock_timestamp()`，所以“先创建的先消耗”**不能**只靠调用顺序：微秒精度下两次读数可能相同，相同时排序退化为随机 UUID。已在本机 PostgreSQL 16.15（`127.0.0.1:55439`，只读事务）实测：

| 场景 | 结果 |
|---|---|
| 同一语句内 200000 次连续 `clock_timestamp()` | 179212 次与前一次**完全相等**，0 次倒退 |
| 两次读数间隔一次网络往返，3000 对 | 0 对相等，0 对倒退 |

即：真实流程里几乎不会相等，但这是概率而不是保证，资金顺序规则不能建立在概率上。因此规定：

```text
付费来源 created_at = clock_timestamp()                          （现有行为）
赠送来源 created_at = max(clock_timestamp(), 付费来源 created_at + 1 微秒)
赠送来源 fifo_at    = 其 created_at                              （wallet_lot_valid 已要求两者相等）
```

- 赠送来源**严格晚于**付费来源，不会出现并列，因此不依赖 UUID 排序。
- 与同租户**其他**充值单的相对顺序不变：赠送来源最多比“自然时钟值”晚 1 微秒。
- `created_at` 因此不再总是“纯粹的 `clock_timestamp()`”，审计上以 `parent_ref` 关联到付费来源可解释；`wallet_lot_valid` 对该字段没有“必须等于真实时钟”的约束（已核对：它只要求 `fifo_at = created_at`）。
- **必须在三处固定**，否则后来的改动可能无意破坏：`ApplyFundingTx` 的注释、赠送 receipt 的 DEFERRABLE 校验（要求其 `created_at` 严格大于同 `parent_ref` 的付费 receipt）、§16 的并发测试。

**后果**（需要产品知情）：付费来源先被消耗，所以客户用得越多，付费来源剩得越少，可现金退款的余额越小。例如付 100 得 100 并送 20，用掉 90 后，付费来源剩 10，赠送剩 20：可退现金最多对应这 10，赠送的 20 不可退现金。这是上述产品意图的直接结果。

**不选 A、C 的原因**：A（排序键加入类别）要改五处并重做守恒与并发测试，且方向与产品意图相反；C 同样让赠送先消耗。

### 5.6 两个新桶：退款冻结与已退出

现金退款需要“待转账”的中间态和“已退出”的终态。**不复用 `held` 与 `HELD` 账户**，原因来自对现有代码的核对：

- 客户 `Wallet` 查询把租户 `HELD` 账户余额展示为“冻结”（`internal/billing/query.go`）。退款冻结混进去，客户会看到冻结增加却没有对应请求。
- 来源 `held_amount` 与预占分配 `funding_reservation_lots` 隐含一一对应（`wallet_event_valid` 的 `reserve` 分支要求关联 `funding_reservation_lots` 与某个 `reservations`）。混入退款冻结后，“来源 held = Σ 预占分配”的核对不再成立。
- 结算、恢复使用**该预占自己的金额**（`settle.go`：`HELD: -r.Amount`），不依赖 `HELD` 余额等于全部预占之和，所以混用不会破坏结算，但会让上面两点失真。

新增两个来源桶和一个租户账户：

```sql
ALTER TABLE funding_lot_balances
  ADD COLUMN refund_held_amount bigint NOT NULL DEFAULT 0 CHECK (refund_held_amount >= 0),
  ADD COLUMN retired_amount     bigint NOT NULL DEFAULT 0 CHECK (retired_amount >= 0);
ALTER TABLE funding_lot_events
  ADD COLUMN delta_refund_held bigint NOT NULL DEFAULT 0,
  ADD COLUMN delta_retired     bigint NOT NULL DEFAULT 0;
-- 守恒：available + held + consumed + refund_held + retired = issued_amount
-- ledger_accounts.kind 增加 'REFUND_HELD'（租户账户，balance >= 0）
```

流转：

| 迁移 | 租户账户 | 来源桶 |
|---|---|---|
| 批准（冻结） | `AVAILABLE −k`、`REFUND_HELD +k` | `available −k`、`refund_held +k` |
| 完成（退出） | `REFUND_HELD −k`、`FUNDING_CLEARING +k` | `refund_held −k`、`retired +k` |
| 取消或失败（释放） | `REFUND_HELD −k`、`AVAILABLE +k` | `refund_held −k`、`available +k` |

`AVAILABLE` 账户余额仍等于 `Σ available_amount`；`HELD` 仍等于 `Σ held_amount`；新增 `REFUND_HELD` 账户余额等于 `Σ refund_held_amount`。`FUNDING_CLEARING` 在充值时为负，退出使其回正；现有 `ledger_accounts_check` 对它没有符号限制，而 `OPERATING_GRANT_CLEARING`、`SANDBOX_FUNDING_CLEARING`、`WALLET_OPENING_CLEARING` 被约束为 `balance <= 0`，所以不可现金退款的来源不会向这三个账户回正。

**已核对的现有约束**（`migrations/current_mvp.sql`）：

| 对象 | 现状 | 必须的变化 |
|---|---|---|
| `wallet_lot_balance_guard` | 要求本金守恒 `available+held+consumed = issued`，且每次更新有同事务、同 revision、三个 delta 完全匹配的事件 | 守恒式与事件匹配扩展到五个桶 |
| `wallet_lot_event_stamp` | 事件写入前打标 | 识别两个新 delta |
| `wallet_event_valid` | **事件类型是封闭 `CASE`，未识别类型返回 `false`** | 为 `refund_hold`、`refund_retire`、`refund_release` 各新增分支，关联 `cash_refund_orders`，不能复用 `reserve` 分支 |
| `wallet_journal_valid` | 按账户类型对账：`AVAILABLE` = `Σ delta_available`，`HELD` = `Σ delta_held`，`REVENUE` = `Σ delta_consumed`；`FUNDING_CLEARING` 不在其中 | 增加 `REFUND_HELD` = `Σ delta_refund_held`；`FUNDING_CLEARING` 变动与 `delta_retired` 的对应 |
| `funding_fact_guard`（触发器函数） | 对 `journals` 的 `kind` 有白名单：`kind NOT IN ('credit','voucher_grant','sandbox_capture')` 时，只要该 journal 涉及 `FUNDING_CLEARING`、`OPERATING_GRANT_CLEARING`、`SANDBOX_FUNDING_CLEARING` 任一账户就抛错 | 白名单必须加入 `credit_bonus`（赠送入账）与 `cash_refund_retire`（退出时 `FUNDING_CLEARING` 回正）。**这与我此前“`journals.kind` 无 CHECK 白名单”的说法不符：没有表级 CHECK，但这个触发器是实际的白名单** |
| `funding_journal_valid` 等按 `kind` 分支的函数 | 对已知 kind 校验 | 新增 `credit_bonus`、`cash_refund_hold`、`cash_refund_retire`、`cash_refund_release` 的合法形态 |
| `funding_lot_events` 前缀和核对、`wallet_sources_read.go` | 三桶之和 | 五桶；输出分列 |
| `check-wallet-sources` | 四类不一致 | 加入新桶；新增“父单据来源不成对”类；`unknown` 来源总额单列 |
| `Wallet` 查询与客户钱包页 | `AVAILABLE`、`HELD` 两项 | 增加“退款处理中”一项 |
| 027、028-U 延迟约束 | 三桶 | 同步 |
| `wallet_reservation_fifo_guard` | `ORDER BY fifo_at, id` | 见 §5.5 |
| Reserve 的来源选择 | 只选 `available_amount > 0` | 不变；两个新桶永不进入 |

不变量：新桶不能再次进入 Reserve，不能参与消费退款；每次变动必须有对应现金退款单与 journal；未知提交不能重复写事件（§7.5）。

### 5.7 现金退款单

```sql
CREATE TABLE cash_refund_orders (
  id                 text PRIMARY KEY DEFAULT gen_random_uuid()::text,
  tenant_id          text NOT NULL REFERENCES tenants(id),
  lot_id             text NOT NULL,                  -- 原付费来源
  business_ref       text NOT NULL UNIQUE,           -- 唯一业务号
  coin_amount        bigint,                         -- 全额：批准时由系统读取该来源全部可用币，操作人不可填写（D-034-12）
  reason             text NOT NULL,
  state              text NOT NULL DEFAULT 'requested'
    CHECK (state IN ('requested','approved','transfer_executed','completed','cancelled','failed')),
  requested_by       text REFERENCES users(id),
  maker              text NOT NULL REFERENCES users(id),
  approver           text REFERENCES users(id),
  transfer_executor  text REFERENCES users(id),
  fiat_currency      text,                           -- 批准时冻结
  fiat_amount        bigint,                         -- 批准时冻结，系统按 §7.3 计算
  buy_rate_version_id    text,                       -- 来源买入币价版本（取自 receipt），批准时冻结
  refund_rate_version_id text,                       -- 批准时点生效的币价版本，批准时冻结
  single_operator    boolean NOT NULL DEFAULT false, -- 批准时快照 §5.1 开关
  transfer_order_ref text,                           -- 转账指令编号，可在批准后补充
  transfer_proof_ref text,                           -- 已完成转账的凭证，转账完成后必填
  paid_fiat_amount   bigint,                         -- 线下实际转出金额
  hold_journal_id    text,
  retire_journal_id  text,
  release_journal_id text,
  created_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK (approver IS NULL OR single_operator OR (approver <> maker AND approver IS DISTINCT FROM requested_by)),
  CHECK (transfer_executor IS NULL OR single_operator OR transfer_executor <> approver),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES funding_lots(tenant_id, id)
);
```

不提供删除；应用角色无 DELETE；终态（`completed`、`cancelled`、`failed`）不可再改。

**同一来源同时只能有一张未完成的退款单**，由数据库部分唯一索引强制，不靠应用判断：

```sql
CREATE UNIQUE INDEX cash_refund_one_open_per_lot ON cash_refund_orders (lot_id)
  WHERE state IN ('requested','approved','transfer_executed');
```

否则两张全额单会对同一笔币各冻结一次。

### 5.8 币价版本

币价是进入资金核心的版本化参数。MVP 只创建创世版本并在付费来源入账时记录买入版本；**不提供调价入口**。未来若需要调价，必须沿用本节的追加式版本、审批、预览和不可回溯规则，作为后续治理能力实现。

```sql
CREATE TABLE coin_rate_versions (
  id              text PRIMARY KEY,
  fiat_currency   text NOT NULL CHECK (fiat_currency IN ('CNY','USD')),
  rate_num        bigint NOT NULL CHECK (rate_num > 0),               -- 1 币 = rate_num/rate_den 个法币单位
  rate_den        bigint NOT NULL CHECK (rate_den BETWEEN 1 AND 1000000),
  effective_at    timestamptz NOT NULL,
  reason          text NOT NULL,
  maker           text NOT NULL REFERENCES users(id),
  approver        text NOT NULL REFERENCES users(id),
  single_operator boolean NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (fiat_currency, effective_at),
  CHECK (single_operator OR approver <> maker)
);
CREATE TRIGGER immutable_coin_rate BEFORE UPDATE OR DELETE ON coin_rate_versions
  FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
```

- **币价单位**：`rate_num/rate_den` 是“1 币值多少法币单位”，币与法币都以微单位存储，所以比值无量纲。创世版本 CNY 为 `1/1`，`effective_at = 1970-01-01T00:00:00Z`。
- **选取规则**：对币种 `X` 在数据库时刻 `t`，取 `effective_at <= t` 中 `effective_at` 最大的一行。**没有版本时失败关闭**（拒绝入账与退款），不使用默认值。
- **不得回溯**：新版本的 `effective_at` 必须不早于批准时的数据库时刻，所以调价不会改写已经发生的事实。
- **MVP 禁止调价**：不存在调价 HTTP、控制台入口或可写的调价命令；只有创世版本和入账时冻结的买入版本。
- **后续调价治理**：用与手动充值相同的 maker / approver 流程（含 §5.1 开关），通过后才写入版本表；提案本身放在命令表里，沿用现有命令幂等回执模式。审批前必须展示受影响币种中退款额会减少的来源数与合计减少额，且 `effective_at` 不得早于批准时刻。
- **后续币价上调**不会让任何已有来源的退款变多（§7.3 取 `min`）。

### 5.9 客户自助申请

客户（租户 owner）提交的充值或退款申请**没有任何钱包效果**，所以不放进 `credit_orders` / `cash_refund_orders`（它们的 `maker` 必须是财务角色）。单独的申请表：

```sql
CREATE TABLE wallet_requests (
  id            text PRIMARY KEY,
  tenant_id     text NOT NULL REFERENCES tenants(id),
  kind          text NOT NULL CHECK (kind IN ('topup','cash_refund')),
  requested_by  text NOT NULL REFERENCES users(id),     -- 租户 owner
  note          text NOT NULL,                          -- 客户自述（如付款流水号），仅供财务核对，不被任何计算引用
  state         text NOT NULL DEFAULT 'open' CHECK (state IN ('open','linked','closed')),
  linked_order_id text,                                  -- 财务据此创建的单据
  created_at    timestamptz NOT NULL DEFAULT clock_timestamp()
);
```

财务操作员核对后创建对应的充值单或退款单，并把 `requested_by` 填为该申请人、`linked_order_id` 回填。申请表的细节（状态迁移、客户页面、每租户未关闭申请数上限）仍待设计，见路线图“设计完整度”。

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
| 币的去向 | 回到原来源的可用桶 | 先进入退款冻结，转账确认后进入 `retired` |
| 现有实现 | `refund_orders` | 新增 `cash_refund_orders` |

### 7.1 资格

- 只针对 `basis_kind='paid'` 且 `refund_class='cash_original'` 的来源；赠送、兑换券、开账、未知基准来源不可现金退款。
- 只能退该来源的**可用**币：退款币数 `≤ available_amount`。已冻结（预占）与已消费的币不可退。
- 单次与累计退款的法币额不超过该来源的原付款。
- 一张退款单只针对一个来源；跨来源需分别建单。
- **全额**（D-034-12）：退款币数就是批准时该来源的**全部可用币**，由系统读取，操作人不能填写；退款额同样由系统按 §7.3 计算。部分退款几乎没有需求，不提供。
- 同一来源同时只能有一张未完成的退款单（§5.7 的部分唯一索引）。

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
| `requested → approved` | approver | **冻结**：来源 `available −k`、`refund_held +k`；租户 `AVAILABLE −k`、`REFUND_HELD +k`；写 `refund_hold` 事件与 `cash_refund_hold` journal | 重新读取可用币校验；审批人 ≠ maker、≠ requested_by（单人运营开关开启且双方均为 `admin` 时除外，§5.1）；读取全部可用币作为退款币数，按 §7.3 计算并冻结 `fiat_amount`、`fiat_currency`、`buy_rate_version_id`、`refund_rate_version_id`；批准时快照 §5.1 开关 |
| `approved → transfer_executed` | transfer_executor | 无 | 执行人 ≠ approver；填入 `transfer_order_ref`（转账指令编号，批准时尚无最终凭证） |
| `transfer_executed → completed` | transfer_executor 或另一名财务 | **退出**：来源 `refund_held −k`、`retired +k`；租户 `REFUND_HELD −k`、`FUNDING_CLEARING +k`；写 `refund_retire` 事件与 `cash_refund_retire` journal | 必须填 `transfer_proof_ref`（已完成转账的凭证）与 `paid_fiat_amount` |
| `approved → cancelled` | approver 或 maker | **释放**：来源 `refund_held −k`、`available +k`；租户 `REFUND_HELD −k`、`AVAILABLE +k`；写 `refund_release` 事件与 `cash_refund_release` journal | 尚未转账 |
| `approved / transfer_executed → failed` | 财务 | **释放**（同上） | 线下转账失败；必须记录原因 |

要点：

- **冻结的会计形态**：`cash_refund_hold` journal 为租户 `AVAILABLE −k`、`REFUND_HELD +k`；`cash_refund_retire` 为 `REFUND_HELD −k`、`FUNDING_CLEARING +k`；`cash_refund_release` 为 `REFUND_HELD −k`、`AVAILABLE +k`。见 §5.6 表。
- **冻结期间币不可消费**：冻结的币在 `refund_held` 桶，Reserve 不会选它们，所以批准后到转账完成之间不会被花掉，也不会出现“币被花了才发现要退”。
- **`transfer_order_ref` 与 `transfer_proof_ref` 分开**：前者是批准或执行时的转账指令编号，后者是转账完成后的已完成凭证。批准时没有最终凭证，不能当作付款证明。
- **金额不一致**：`paid_fiat_amount` 与冻结的 `fiat_amount` 必须相等才允许 `complete`；不一致时只能 `cancelled/failed` 释放后重新建单，**不允许在 `complete` 时改金额**，也不允许退款成功后更正已退出的币数。需要更正（例如多转或少转）作为单独的财务调整，在系统外处理并留审计，不通过修改该单。
- **全额，不支持部分执行**（D-034-12）：一张单处理该来源批准时的全部可用币，不支持“先退一半”。
- **取消**：只能在 `approved` 且转账未发生时取消；`transfer_executed` 之后只能 `failed`（转账确认失败）或 `completed`。
- **完成后更正**：`completed` 是终态；退出的币不可返还。若发现退错，通过新的赠送或充值单处理，不回滚。

### 7.3 退款法币金额（D-034-13，已裁决：取 `min`）

**规则**：现金退款按**当前币价**核算，但**不超过按原付款比例的金额**：

```text
退款额 = min( 按当前币价核算的金额 , 按原付款比例核算的金额 )
```

MVP 不开放调价，因此批准时的当前币价就是该来源已记录的买入币价；B 规则仍按下面的通用公式记录，当前实现结果等于按原付款比例退款。未来开放调价后，才会出现当前币价低于买入币价而缩小退款的分支。

其中按原付款比例核算 = 退款币数 × 实付单价（`F/C`），按当前币价核算 = 退款币数 × 实付单价 × `当前币价 / 买入币价`。折扣体现在实付单价里，不受币价影响，所以**折扣充值的折扣不会在退款时被“退回”**。

记：`k` 为全额退款币数（该来源批准时的全部可用币），`F` 为来源的 `fiat_amount`，`C` 为来源发行币数，`buy = bn/bd` 为买入币价，`cur = cn/cd` 为批准时点币价（同一法币币种）。化简后：

```text
若 cn × bd ≥ cd × bn（币价不低于买入价）：   fiat_refund = floor( k × F / C )
否则（币价低于买入价）：                    fiat_refund = floor( k × F × cn × bd / ( C × cd × bn ) )
```

- 全部乘除用 `big.Int`（Go）或 `numeric`（SQL）；`k`、`F`、`C` 均 ≤ 2⁶³−1，分子最多 5 个 64 位数相乘，不能用 `int64`。
- 向下取整，**永不多退**：结果恒 ≤ `k × F / C`。
- 在 MVP 的固定币价下，退款等于原付款比例。未来币价上调或持平时仍不让退款变多；币价下调时，退款按币价下降的比例同步缩小。
- **为什么要取 `min`（堵“退款再充值”）**：若退款按原付款比例而不看币价，币价下调后客户可以用高比例退掉旧币，再以新的低价重新充值，净赚差价。取 `min` 后，币价下调时退款随之缩小，币价上调时退款不增加，重新充值没有套利空间。
- 币价与买入价必须属于**同一法币币种**；任一版本缺失则失败关闭，拒绝批准。
- 退款额在**批准时**冻结（含两个币价版本 ID）；MVP 没有后续调价，未来调价后也不影响已经批准的单据。

**不再使用累计取整公式。** 早先草案用 `floor(F×w_new/C) − floor(F×w_old/C)` 保证多次部分退款不多退，它依赖“比例恒定”。币价可调后同一来源前后两次退款的比例可能不同，该公式不成立；而全额退款每张单独立向下取整，只会少退不足 1 个最小单位，不会多退。同一来源可以有多张**先后完成**的全额单（消费退款会把币退回原来源的可用桶，之后可再次现金退款），总额不超过 `F` 由 `Σ k_i ≤ C`（`retired` 不超过发行额）保证，不需要累计项。`w_old` 不再使用。

`internal/productvalue` 的“端点映射再相减”算法**不再用于现金退款**：它解决的是固定比例下的区间归因，这里每张单独立核算。

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

### 8.1 余额构成

客户看到的余额**包含赠送币的数量**。现状（已核对）：`GET /api/wallet/sources` 已逐来源返回 `source_kind`、`refund_class`、`issued_amount`、`available_amount`、`held_amount`、`consumed_amount`，前端 `WalletSources.vue` 已有来源表；但钱包页顶部只有“可用”“冻结”两个总数（`billing.Wallet` 只有 `Available`、`Held`），没有构成汇总。

客户钱包页顶部增加余额构成，由来源表聚合，不新增存储字段：

| 项 | 口径 |
|---|---|
| 可用余额 | 现有，`AVAILABLE` 账户余额 |
| 其中付费 | `basis_kind='paid'` 来源的 `available_amount` 之和 |
| 其中赠送 | `basis_kind='grant'` 来源的 `available_amount` 之和 |
| 其中其他 | `basis_kind IN ('opening','unknown')` 来源的 `available_amount` 之和 |
| 冻结 | 现有，`HELD` |
| 退款处理中 | `refund_held_amount` 之和，仅大于 0 时显示 |

- **恒等式**：付费 + 赠送 + 其他 = 可用余额。该恒等式由现有 `wallet_account_valid`（`AVAILABLE` 账户余额等于 `Σ available_amount`）保证，不是新增风险；“其他”必须单列，否则存在 `manual_unknown` 或开账来源时前三项之和会小于总额。
- **展示不等于区分消耗顺序**：客户只看到余额构成，没有“先用哪个”的选项，消耗顺序由 §5.5 固定。
- **客户提示**：在“其中赠送”旁标注“赠送币不可退现金”，与 §7.1 的资格一致，避免退款争议。
- 来源表中 `source_kind` 对客户的文案由前端映射（例如 `manual_bonus` 显示为“充值赠送”，`voucher_grant` 显示为“兑换券”）；文案清单随 34.5 一起给出。
- 平台侧来源明细（含请求分配）保持现状，不对客户折叠或隐藏现有字段。

**前端校验必须放开**：`web/src/wallet-sources.ts` 现在只接受现有的四种 `source_kind` 与两种 `refund_class`，并硬编码 `(source_kind === 'sandbox_capture') === (refund_class === 'sandbox_original')`。新增 `manual_bonus`、`manual_unknown`、`cash_original` 后这些校验会直接抛错，必须同步修改并有前端测试。

## 9. 换算配置入口

| 范围 | 入口 | 规则 |
|---|---|---|
| **币价** | 平台配置页（§5.8） | 追加式版本、审批、不得回溯、调价前预览对退款的影响；只影响之后新入账来源的买入币价记录，以及现金退款的 `min` 核算 |
| 展示“约合” | 平台配置页 | 追加式版本；只影响展示，标注“仅供参考” |
| 已有余额重估 | 单独的受控命令，**默认不提供** | 见下 |

**币价调整不是余额重估**：调价不改变任何来源的币数、可用额或模型价格，客户的币数量不变，只是退款换算会变。余额重估是另一件事：同时缩放来源本金、余额、在途冻结、价格版本、Key 额度和周期预算。若将来实现，须双人审批、维护模式停服并取得服务 advisory 锁（与 027 停服开账一致）、先预览、一个 operation 原子完成、不可撤销；向下重估需要明确合同依据。“让所有客户多得一些币”用批量赠送来源，不需要重估。

## 10. 财务报表

026 经营报表现在固定 `Currency: "CNY"`、`AmountUnit: "micro_cny"`，改为：

| 口径 | 计算 |
|---|---|
| 消费币数 | 已结算消费币数，来自 `settlements` |
| 名义法币值（参考） | 消费币数 × 报告时点生效的币价版本，标注版本，仅供参考；不用于退款 |
| **按来源分配的客户付款基准** | 对每条结算分配边，取 `charged_amount × fiat_amount / issued_amount`（文本取 `funding_settlement_lots`，媒体取 `media_settlement_lots`，两者共享来源 lot；所属 receipt，不受 `retired` 影响：已退出的币从未被消费，不参与此计算），按 `fiat_currency` 分列；`grant`、`opening` 计 0；`unknown` 为未知 |
| 采购成本 | 供应商币种原值；折算依赖供应商币种规格 |
| 毛利参考 | 付款基准 − 已知采购成本，同币种比较；任一项未知或缺汇率时为 null |

**这不是会计收入。** HLD 已说明业务流水不宣称法定会计总账；赠送币的收入确认、未使用余额的会计处理由财务和法务决定。该口径只作为经营报表与毛利参考的输入。

按来源分配的付款基准是对现有事实的只读计算，不改动请求路径或结算写入。现金退款退出的法币不进入该口径，作为单独的“已线下退还法币”列按币种汇总，供财务对账。

## 11. 对其他规格的影响

| 规格 | 影响 |
|---|---|
| [030](../030-model-aliases-group-multipliers/spec.md) | 分组倍率编译的目标单位改为币 |
| [032](../032-phase2-text-data-plane/spec.md) | 客户侧周期用量上限和缓存命中计费以币计 |
| [033](../033-text-dynamic-routing/spec.md) | 最低毛利需要“币售价折法币”与“供应商成本折法币”同一时点比较，依赖币价版本（§5.8）和供应商币种规格 |
| 022 | 范围 P：支付订单放开付款与到账分离、捕获产生付费与赠送两个来源（同 §5.4 结构：付费来源与赠送来源两个 receipt、两笔 journal、同一捕获为父单据，`source_kind` 用真实捕获类型与 `capture_bonus`；`journals.operation_id` 全局唯一，两笔 journal 不能共用同一个 operation）；支付宝回调有多个金额字段（订单金额、实收、优惠后实付等），`payment_captures.amount` 存哪一个必须在支付规格中写死，并以支付宝官方文档为准 |
| 023-C 订阅 | 套餐额度作为新的来源类型，以币计；到期需要另设桶，须与本规格的 `refund_held`、`retired` 两个桶一起重新设计守恒式 |

## 12. 后置

- **币的到期**（D-034-10）：后置，且本规格**不**在 `funding_lots` 预留 `expires_at`。理由：到期要新增桶、事件类型、journal、扫描 worker 与 Reserve 过滤，应与两个退款桶一起设计守恒式；是否需要“同一来源内部分到期”尚未决定，现在预留字段可能预留错。到期若按来源整体（例如赠送来源整体到期而付费来源不到期）设计，由于付费与赠送本来就是两个来源，现有的来源划分已经够用。
- 限定模型范围的币、充值分组优惠；
- 真实支付与美元收款：依赖正式支付商，第四阶段；
- 手续费：支付渠道扣费不影响钱包入账，只在财务对账层分开记录。

## 13. 决策点

用户已确认方向：**币为对外单位、手动充值与手动退款优先、支付后置**。下表每项标注**状态**：`建议中` 表示尚无裁决；`影响模型` 表示它会直接改变钱包模型或 guard，冻结前必须有结论，不能停留在建议。

| 编号 | 问题 | 建议 | 状态 |
|---|---|---|---|
| D-034-1 | 创世币价 | CNY 1 币 = 1 CNY；之后币价作为追加式版本可调（§5.8） | **已裁决**（2026-11-14 采纳建议） |
| D-034-2 | 赠送是独立来源还是合入付费来源 | 独立来源（§5.4） | **已裁决**（2026-11-14 采纳建议） |
| D-034-3 | 取整 | 退款法币额每张单独立向下取整，永不多退（§7.3） | **已裁决**（2026-11-14 采纳建议） |
| D-034-4 | 同一充值单两来源的消费先后 | **付费先、赠送后**；沿用严格 FIFO，不改任何 guard，用确定性时间戳保证（§5.5） | **已裁决**（产品意图：赠送是为了让用户多用） |
| D-034-5 | 手动美元付款 | 管理员录入美元金额与到账币数，人工汇率说明仅作备注（§5.2） | **已裁决**（2026-11-14 采纳建议） |
| D-034-6 | 是否提供“约合”展示 | 提供，带版本，标注仅供参考 | **已裁决**（2026-11-14 采纳建议） |
| D-034-7 | 显示名 | 平台可配置 | **已裁决**（2026-11-14 采纳建议） |
| D-034-8 | 已有余额重估 | 不实现；向上调整用批量赠送 | **已裁决**（2026-11-14 采纳建议） |
| D-034-9 | 单人运营 | 开关，**默认开启**；开启时 `admin` 账号可同一账号制单与审批（§5.1）；上线生产前建议关闭 | **已裁决** |
| D-034-10 | 赠送币是否到期 | 后置；不预留字段（§12） | **已裁决：后置** |
| D-034-11 | 单次最大充值与最大赠送 | `amount` 与 `bonus_amount` 分别不得超过 **1,000,000,000,000 微币**；部署可配置更小运营上限，但不得放宽该硬上限 | **已裁决**（2026-11-14 采纳既有实施冻结值；硬上限进资金合同，运营配置只能收紧） |
| D-034-12 | 现金退款是否允许部分扣减 | **全额**，币数由系统填，同一来源同时只能有一张未完成退款单（§5.7、§7.1） | **已裁决** |
| D-034-13 | 现金退款额的核算 | 取 `min(按当前币价, 按原付款比例)`，批准时冻结（§7.3） | **已裁决**（产品负责人；用于堵“退款再充值”套利） |

## 14. 业务范围说明

本规格面向 2B 业务：客户是企业或团队，线下付款、手动对账。产品负责人已确认**不需要把财务与法务书面意见作为冻结条件**。因此本规格不再设合规阻断项；收入确认、赠送币的会计处理等仍是业务自行管理的事，规格不对其给出结论。

状态措辞：**034 设计可以冻结；开发环境可以实现；不代表正式储值业务、生产退款和财务会计处理已通过验收。** 本规格合入主线只表示设计方向被接受。**不得**据此宣称币钱包、现金退款已实现，或生产储值能力已通过验收；实现需要按 §17 分片完成并通过 §16 的专用 PostgreSQL 验证。

## 15. 冻结前阻断项

034 的冻结阻断项。2026-11-14 开发方向审查逐项复核：文本类阻断项已全部关闭；标注“需要真实 PostgreSQL 验证”的项属于实现片验收（§16、§17），在对应分片完成前不宣称实现完成，但不阻塞冻结。逐项状态：

| # | 阻断项 | 当前状态 |
|---|---|---|
| 1 | 现金退款的完整状态机 | 已写入 §7.2；文本类阻断已关闭，状态迁移、并发与故障边界**需真实 PostgreSQL 验证** |
| 2 | 转账凭证在审批前还是审批后生成 | 已区分：`transfer_order_ref`（指令）与 `transfer_proof_ref`（完成凭证，完成时必填），§7.2、§7.6 |
| 3 | 转账失败与未知提交处理 | 失败走 `failed` 并释放冻结（§7.2）；未知提交只读回（§7.5）；**需真实 PostgreSQL 验证** |
| 4 | `requested_by`、`maker`、`approver`、`transfer_executor`、`recipient` 定义 | 已写入 §5.1 |
| 5 | 用户自助、管理员代充、运营赠送的权限矩阵 | 已写入 §5.1；**钱包按租户**，不存在员工个人钱包 |
| 6 | `basis_kind` 与 `source_kind` 的允许组合 | 已由 §5.3 表格枚举；实现为 receipt 与 lot 各自的单表 CHECK 加 `wallet_lot_valid`，不使用跨表 CHECK |
| 7 | 付费来源与赠送来源的父单据关联 | 已写入 §5.4（`parent_ref`、成对性约束、journal `operation_id` 不可共用） |
| 8 | `retired` 桶涉及的全部 guard 与验证器 | 清单见 §5.6；**需逐项修改并真实 PostgreSQL 验证** |
| 9 | 手动美元付款是否记录人工汇率 | 仅 `fx_note` 备注，不是汇率版本，不被计算引用（§5.2） |
| 10 | D-034-1…13 最终裁决状态 | 见 §13；**全部已裁决**，不影响钱包模型 |
| 11 | 034 实现完成前不修改现有运行时 | 见下 |

**实现前不修改现行运行时**：本规格合入后，现有的钱包、人工核款、兑换券、消费退款、027 来源核对和 028-U guard 保持不变，仍以人民币单币种为现行合同；任何对应的迁移、guard 或入口变更必须在 34.0 冻结后按 §17 分片进行，并各自带专用 PostgreSQL 验证。

### 评审意见逐条状态

状态取值：`已满足`、`需要修改规格`（已修改）、`后置到 022/P`、`后置到 V`、`需要真实 PostgreSQL 验证`（财务与法务确认已由产品负责人决定不作为条件）。

| 评审项 | 状态 | 说明 |
|---|---|---|
| 现金退款状态机不完整 | 需要修改规格（已修改）；需要真实 PostgreSQL 验证 | 改为先冻结、转账确认后才退出，§7.2 |
| 双人审批与单人运营混为一谈 | 已满足 | §5.1：管理员界面开关默认开启；启用或修改需二次确认/近期重新认证；例外带审计标记；D-034-9 已裁决 |
| 发起人、制单人、审批人、执行人拆字段 | 需要修改规格（已修改） | §5.1。注：钱包按租户，“给员工充值”即给其租户入账 |
| `unknown` 的 `source_kind` 未定 | 需要修改规格（已修改） | `manual_unknown`，§5.3；三元组的实现方式见 §5.3「三元组约束的实现方式」 |
| 两个来源的父关联更强 | 需要修改规格（已修改）；需要真实 PostgreSQL 验证 | §5.4；并发现 `journals.operation_id` 全局唯一，两笔 journal 不能共用 |
| 手动充值顺序 | 已满足 | §6 |
| 正式支付不混入当前范围 | 已满足；回调与金额字段后置到 022/P | §6、§11 |
| 手动美元付款与汇率记录 | 需要修改规格（已修改）；汇率版本后置到 V | §5.2 `fx_note` |
| `retired` 桶的完整 guard | 需要修改规格（已修改）；需要真实 PostgreSQL 验证 | §5.6 表格，新增 journal kind 校验项 |
| 现金退款与 Reserve 的竞态 | 需要真实 PostgreSQL 验证 | §7.4 列出场景，尚未执行 |
| 决策点未冻结与路线图状态不一致 | 需要修改规格（已修改） | §13 标注状态；路线图改为“设计与决策属第一阶段，实现属 MVP 前” |
| 财务与法务意见 | 已由产品负责人决定不作为冻结条件 | §14 |
| “现金基准收入”不应叫收入 | 已满足 | §10 |

## 16. 验收

以下带“PG”的项必须在专用 PostgreSQL（`TEST_DATABASE_URL`，`127.0.0.1:55439`）上通过；跳过不算完成。设计文字不能代替这些测试。

| 组 | 断言 | PG |
|---|---|---|
| 单位 | 迁移后钱包 journal、`funding_receipts.currency`、售价版本为 `COIN`；数值与迁移前一致（创世币价 1:1） | PG |
| 类型区分 | 法币金额与币金额在 Go 中是不同类型，编译期禁止互相赋值；API 字段与文档分别标明单位 | — |
| 角色约束 | `approver ≠ maker`、`approver ≠ requested_by`、`transfer_executor ≠ approver` 由数据库拒绝；客户自助申请不产生任何钱包效果；审批前无入账 | PG |
| 单人运营开关 | 开关开启：同一 `admin` 账号可制单并审批，单据快照 `single_operator=true`、带 `single_operator_exception` 标记，且 `funding_journal_valid` 通过；开关关闭：同一账号被拒；非 `admin` 的财务角色任何情况下不可自批；事后关闭开关不使已批准单据失效；开关与币价调整同样受控 | PG |
| 手动充值基准 | 付 90 得 100：receipt 记法币 90、币 100；`paid` 缺法币字段被拒；`grant`/`unknown` 带法币字段被拒；receipt 不可修改 | PG |
| 来源组合 | receipt 的 `(source_kind, basis_kind)` 与 lot 的 `(source_kind, refund_class)` 表外组合各自被 CHECK 拒绝；receipt 与 lot 的 `source_kind` 不一致被 `wallet_lot_valid` 拒绝；`manual_unknown` 不可现金退款 | PG |
| 付费与赠送 | 一单两来源两笔 journal，`operation_id` 不冲突；重复审批只入账一次；`bonus_amount=0` 不产生赠送来源；任一失败整体回滚；**直接 SQL 造出只有一个来源的已批准单被 COMMIT 前约束拒绝**；`check-wallet-sources` 报告不成对 | PG |
| 金额一致 | 付费 receipt = `amount`，赠送 receipt = `bonus_amount`；`amount + bonus_amount` 溢出被拒；两个字段分别验证 1,000,000,000,000 微币硬上限及上限加一拒绝，部署只能收紧不能放宽 | PG |
| 消费顺序 | 同一充值单：**始终先消耗付费来源、后消耗赠送来源**；并发请求下不变；赠送来源 `created_at` 严格大于付费来源（含两次时钟读数相等的构造场景，例如注入固定时钟）；不同充值单严格 FIFO；文本与媒体 Reserve 两条路径一致；SQL guard 与 Go 计划无分歧；既有 FIFO 测试全部不改而通过 | PG |
| 余额构成 | 付费 + 赠送 + 其他 = 可用余额；存在 `manual_unknown`、开账来源时仍成立；无退款处理时不显示该项；赠送币旁有不可退现金提示 | PG + 前端 |
| 前端校验 | `wallet-sources.ts` 接受 `manual_bonus`、`manual_unknown`、`cash_original`；非法组合仍被拒 | 前端测试 |
| 守恒 | 五桶 `available + held + refund_held + consumed + retired = issued` 在所有操作后成立；`AVAILABLE`/`HELD`/`REFUND_HELD` 账户余额分别等于对应来源桶之和；`check-wallet-sources` 无不一致 | PG |
| 现金退款资格 | 赠送、兑换券、开账、未知基准来源被拒；冻结与已消费部分不可退 | PG |
| 现金退款状态机 | 逐条迁移；冻结后 Reserve 看不到被冻结的币；`cancelled/failed` 释放回可用；`completed` 为终态；完成时 `paid_fiat_amount` 与冻结 `fiat_amount` 不等被拒；`transfer_proof_ref` 缺失被拒 | PG |
| 现金退款金额 | MVP 手算向量：固定币价、折扣充值（付 90 得 100，退 `k×0.9`，折扣不退回）；全额由系统填，操作人传币数被拒；`int64` 边界与 5 个 64 位数相乘不溢出（`big.Int`）；向下取整永不多退；币价或买入版本缺失则失败关闭。未来调价治理另测上调、下调和批准后调价不影响已批准单 | 纯函数 + PG |
| 退款再充值（后续调价治理） | 在调价能力开放后构造“币价下调后退款再以新价充值”：净所得不大于不退款情形，无套利；币价上调同理 | 纯函数 |
| 币价版本 | MVP 创建创世版本并在付费 receipt 记录 `buy_rate_version_id`；版本追加式不可改；`(币种, effective_at)` 唯一；无版本时入账与退款失败关闭；MVP 无调价入口。后续调价治理需另测审批、不得回溯和影响预览；receipt 的买入币价版本币种与来源币种一致 | PG |
| 一来源一张未完成单 | 并发创建两张退款单对同一来源只有一张成功；失败不留半成品 | PG |
| 幂等与未知提交 | 重复审批、重复完成不二次冻结或退出；业务号唯一；`(refund_order_id, kind)` 事件唯一；提交前断连后只读回，不重复写事件 | PG |
| 竞态 | §7.4 全部场景：Reserve、结算、消费退款与退款审批并发；两张单并发审批同一来源；无死锁、无负可用余额 | PG |
| 请求路径 | Reserve、结算、消费退款不读取任何汇率（静态检查与真实 PG 用例） | PG |
| 报表 | 付款基准按币种分列；`unknown` 基准与缺汇率时毛利为 null | PG |
| 客户展示 | 客户能看到自己的付款金额与到账币数；看不到供应商成本 | — |

## 17. 分片

| 片 | 内容 | 依赖 |
|---|---|---|
| 34.0 | 本规格评审，裁决 D-034-1…13，满足 §15 全部阻断项 | 已完成（2026-11-14 冻结，见[方向审查记录](../../docs/design/direction-review-2026-11-14.md)） |
| 34.1 | `currency` 约束改为 `COIN`，类型区分；数值不变 | 34.0 |
| 34.2 | 角色字段、**单人运营开关与 §5.1 的五类位置**、付款基准、来源组合 CHECK（receipt 与 lot 各自单表）、两来源与父关联、先付费后赠送的确定性时间戳 | 34.1 |
| 34.3 | 两个新桶 `refund_held`、`retired`：守恒式、事件类型、journal kind 校验、`check-wallet-sources`、guard 更新 | 34.2 |
| 34.3b | **币价版本表**、创世版本、receipt 记录买入币价版本；MVP 不提供调价入口，调价审批与预览后置 | 34.2 |
| 34.4 | 现金退款单：状态机、§7.3 退款额核算（`big.Int`）、审批事务、一来源一张未完成单的唯一索引、HTTP 与控制台、告警 | 34.3、34.3b |
| 34.5 | 报表付款基准、客户充值记录与**余额构成展示**（§8.1），前端来源类型校验放开 | 34.2 |
| 34.6 | “约合”展示与显示名配置 | 34.1 |
| 后置 P | 支付订单与捕获的两来源、支付宝金额语义、手续费 | 022 |
| 后置 V | 汇率版本表、币到期、限定模型范围、重估 | 订阅规格 |

34.2、34.3、34.4 改动入账与来源核心，由集成人串行合并，并在专用 PostgreSQL 上验证。

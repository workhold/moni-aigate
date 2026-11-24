# 041-P0：不可变商品合同与经济基础

2026-11-24；状态：**实施合同已冻结，实现与验收按本文台账记录**。基线为 main `907f4697464372316b29f885629a8204b79028de`。本文把 041 R0 的经济基础拆成可实现的 SQL、接口、ACL、状态机和故障边界；不批准真实供应商、正式支付、生产流量或 external commercial。

## 1. 范围与兼容边界

1. 新链路只覆盖显式绑定 `ServiceOfferVersion` 的文本 route revision。没有 offer 的既有请求继续使用原 usage 计价和 033 的数值型路由指标；该旧指标不得命名为现金毛利。
2. P0 新增 `fixed_full_reservation`：在既有政策已经决定“收费”时，收费额固定等于原 reservation；明确未消费、无交付、截止释放和恢复免收分支仍然释放。它不把失败、取消或 unknown 自动变成全额收费。
3. 首片 ExecutionPolicy 固定 `max_outer_attempts=1, inner_max_attempts=1, max_relay_depth=0, settlement_mode=fixed_full_reservation, cache_mode=disabled, cumulative_cost_policy=request_total_v1, unknown_cost_policy=deny`。服务端预算与客户 `allow_fallbacks` 取更小值；不得仅靠客户端关闭重试。
4. P0 只交付内部 `catalog.Store.PublishServiceOffer` 发布接缝、Reserve/Attempt/PrepareDispatch/Settle 接线、统一 reporting 扩展和测试。商品/质量/FX 的管理写 HTTP 与控制台在 P1/P2 开放；数据库 fixture 不是管理 API 或商业资格证据。
5. New API/Sub2API 保持 test-only；P0 offer 只接受 direct binding，commercial 状态迁移与 external inner fencing 不开放。`generic_auto` 在全任务/unknown 证据覆盖前不由内部发布接缝接受。现有 `release_external_commercial_unsupported` 不变。
6. 恢复、回放、报表、投影和通知只读原事实；不得调用 Execute/Submit 或重新生成。

## 2. 金额、原币向量与 FX

### 2.1 Money

`internal/economics.Money` 是带币种向量的整数微法币；`billing.FiatMoney` 是它的类型别名，保持现有 JSON 十进制字符串 ABI。一个单位分别为 `10^-6 USD` 或 `10^-6 CNY`，不是 cent/分。COIN 继续使用 `billing.Money`，不得与法币隐式互赋。

`MoneyVector` 是至多含 `CNY`、`USD` 的闭集映射。值为规范 int64 十进制字符串；JSON number、浮点、重复键、前导零、其他币种、缺失所需币种均失败。空向量与 unknown 不同：空向量只在明确无金额时成立，缺证据由 `basis_complete=false` 或错误表示。

外部十进制输入若未来支持六位以上精度，收入保守侧向下、成本保守侧向上；P0 API 与数据库只接受整数微法币，不静默截断。

### 2.2 FxPolicyVersion

`fx_policy_versions`：

```text
id PK, valuation_currency CNY|USD, source, quoted_at, valid_until,
created_by, created_at
```

`fx_policy_rates`：

```text
policy_version_id FK, source_currency, target_currency,
rate_num>0, rate_den>0, fee_buffer_ppm 0..100000,
PK(policy_version_id,source_currency,target_currency)
```

只接受 USD→CNY 与 CNY→USD 的显式有向 rational 报价，不从反向报价推导。`source/quoted_at/valid_until` 和全部 rates 随版本不可变；使用时要求 `quoted_at<=DB now<valid_until`。同币种是恒等转换，不读取报价也不加费用。

对正金额 `x`：

```text
income_lower = floor(x × rate_num × (1,000,000-fee_buffer_ppm)
                     / (rate_den × 1,000,000))
cost_upper   = ceil (x × rate_num × (1,000,000+fee_buffer_ppm)
                     / (rate_den × 1,000,000))
```

费用缓冲只能降低收入、提高成本。全部中间值用 `big.Int`，最终必须落 int64。缺失、过期、方向错误、溢出或无法覆盖请求原币组合均失败关闭。FX 只用于保守准入和标记为 `fx_snapshot` 的折算贡献毛利，不回写钱包、成本版本、买入币价或历史售价；没有实际换汇及费用事实不得称实收现金毛利。

## 3. 不可变发布与策略 SQL

追加 `current_016_profit_economics.sql`，不修改任何已应用 SQL。所有 ID 为规范 UUID 文本；时间和有效期使用 finite `timestamptz`，以数据库时钟裁决。

### 3.1 TaskPolicyVersion

`task_policy_versions`：

```text
id PK, logical_model_id FK models, taxonomy_version,
default_task_class, allowed_hints text[], class_candidates jsonb,
unknown_action deny|use_unknown, classifier_version,
shape_digest_algorithm, created_by, created_at
```

任务集合固定为 041 的九类。P0 不运行分类器：`classifier_version='none'`、`shape_digest_algorithm='none'`。`class_candidates` 的键必须包含 default 与 unknown（`unknown_action=deny` 时 unknown 值为空数组），值为排序去重的 physical_model_id 数组。候选只能缩小已发布池。

请求优先级为 SKU default、受控 hint；hint 必须在 allowed_hints 且存在于映射。无法判断时进入 unknown；`deny` 拒绝，`use_unknown` 只使用未知类显式候选。结果仅 `{class,source=sku|hint,policy_version}`，不读取或记录 prompt，不调用 LLM/网络。

Chat 的现有 `route` 对象增加可选 `task_hint` 字符串；其他协议不增加字段。未提供时由 SKU 默认类决定。

### 3.2 QualityPolicy / QualityEvidence

`quality_policy_versions`：

```text
id PK, logical_model_id FK, suite_version, metric,
direction higher_is_better|lower_is_better,
threshold_micro, min_sample_count, min_confidence_lower_bound_micro,
max_age_seconds, created_by, created_at
```

分数和阈值均为 `0..1,000,000` 的微分整数；方向由版本固定。通过要求 `confidence_lower_bound_micro` 按方向达到 threshold、样本数达标、证据年龄与剩余有效期均合格。

`quality_evidence`：

```text
id PK, physical_model_id FK, channel_id/channel_version_id FK,
execution_profile_version, deployment_revision, model_revision,
inner_pool_version, inner_pool_digest, task_class,
suite_version, rubric_version, judge_version, metric,
protocol, tools_scope, language_scope, reasoning_scope,
score_micro, uncertainty_micro, confidence_lower_bound_micro,
sample_count, observed_at, valid_until, evidence_ref,
approved_by, approved_at, created_at
```

P0 direct 的 inner pool 字段必须为 NULL；external 不允许发布。证据精确绑定 physical/channel version/profile/deployment/model/task/suite/metric/protocol/tools/language/reasoning，不因 upstream_model 同名共享。`quality_evidence_withdrawals(evidence_id PK,withdrawn_by,reason,withdrawn_at)` 只追加撤回，不修改原分数或历史请求。

`model_bindings.quality_evidence_id` 在 route 内冻结逐候选证据；`route_policy_versions.execution_policy_version_id` 冻结执行政策。路由快照先过滤缺证、scope 不符、撤回、过期、样本或阈值失败；PrepareDispatch 在全部现有锁后以 DB 时钟再次检查 offer 活动状态、绑定版本和证据，关闭过滤后到 sending 的竞态。

### 3.3 ExecutionPolicyVersion

`execution_policy_versions`：

```text
id PK, max_outer_attempts, inner_max_attempts, max_relay_depth,
settlement_mode, cache_mode, cumulative_cost_policy,
unknown_cost_policy, created_by, created_at
```

P0 CHECK 只接受第 1 节的首片组合。该 ID 与完整值复制到 route decision 的 `policy_snapshot` 和请求 EconomicsSnapshot。`routing.Plan.Start/CanRetry`、RecordAttempt 与 PrepareDispatch 都受同一上限约束；数据库 guard 拒绝第二 attempt，不依赖 handler 自约。

### 3.4 EconomicsPolicyVersion 与 ServiceOfferVersion

`economics_policy_versions`：

```text
id PK, valuation_currency, fx_policy_version_id FK,
min_margin_rate 0..10000, max_cumulative_cost_micro jsonb,
created_by, created_at
```

`max_cumulative_cost_micro` 是可选的 CNY/USD 正整数向量；存在时请求累计成本上界还须不超过它。最低毛利在估值币种计算，不是 realized margin。

`service_offer_documents(id PK,kind disclosure|transparency,body jsonb,created_by,created_at)` 只保存公开说明与披露，不含 URL、秘密或正文样本。

`service_offer_versions`：

```text
id PK, logical_model_id, kind exact_model|same_model_multi_provider|
  branded_dynamic|task_optimized|generic_auto,
profile_version_id, price_version_id, route_policy_version_id UNIQUE,
disclosure_version_id, task_policy_version_id, quality_policy_version_id,
economics_version_id, transparency_policy_version_id,
execution_policy_version_id, allowed_candidate_digest bytea,
savings_reference_version_id NULL, approval_evidence_id,
created_by, created_at
```

全部业务字段不可 UPDATE/DELETE。发布在同一 model 锁和 route CAS 事务中创建 route/binding/offer，并校验 price/profile/physical/证据引用与候选 digest；活动 route 只影响新请求。

`service_offer_states(offer_version_id PK,state,revision,updated_by,updated_at)` 是可变生命周期指针，`service_offer_lifecycle_events` 追加 from/to/revision/actor/reason/time。状态闭集和迁移为：

```text
draft → test_candidate → test_active → commercial_pending → commercial_active → retired
draft/test_candidate/commercial_pending → rejected
test_active/commercial_active → withdrawn
```

P0 内部发布只创建 `test_active` 并实现 `test_active→withdrawn`；commercial_pending/commercial_active 没有生产写入口。新 Reserve 只接受 test_active；存在但 withdrawn/rejected 的当前 route 失败关闭，不能退回无门禁 legacy 行为。历史请求继续财务收尾。

## 4. Reserve EconomicsSnapshot

`request_economics_snapshots` 以 `(tenant_id,request_id)` 唯一绑定 request/reservation/offer/route：

```text
tenant_id, request_id, reservation_id UNIQUE, offer_version_id,
economics_version_id, execution_policy_version_id, task_policy_version_id,
quality_policy_version_id, task_class, task_source,
settlement_mode, max_outer_attempts, reservation_coin_amount,
payment_basis_micro jsonb, payment_basis_complete,
fx_policy_version_id, valuation_currency, fx_snapshot jsonb,
max_cumulative_cost_micro jsonb, snapshot jsonb, snapshot_canonical bytea,
snapshot_digest bytea, created_at
```

snapshot 是安全规范 JSON 的完整副本，digest 为 SHA-256；分列表供 SQL 门禁和关联，完整副本供路由/回放。它不含秘密、prompt、endpoint 或客户身份之外的非必要字段。

Reserve 在原租户/Key/model/账户/lot 锁与 FIFO 分配后，按本次 `funding_reservation_lots` 计算固定全额付款基准：

```text
每条 paid 边：floor(reserved_coin × receipt.fiat_amount / lot.issued_amount)
grant/opening：已知零；unknown、缺 buy rate、缺法币、分配总额不等于 R：失败关闭
```

分边向下取整是收入保守下界，余数永久留在原 receipt，不另造现金事实。 Reserve 同时冻结经济版本、执行/任务/质量引用、FX 原币报价、估值币种、节省参照（P0 为 NULL）与累计成本上限。旧报表不得把此准入选址快照当作结算或付款权威。

新 offer 固定 price_version：Reserve 选价结果必须等于该版本；profile/route/offer 不匹配、cache 非关闭、USD/CNY 覆盖不足或任何数据库/报价错误都回滚整个请求与预占，不产生上游 I/O。

## 5. 请求级累计成本与派发复核

候选成本继续使用原 cost version/rates 与 physical supplier bounds 计算单次保守上界 U；`cost_versions.currency` 的 CHECK 扩展为 CNY/USD，routecost Quote 的相等性、route decision cost snapshot 和 attempt 报价复核都携带币种。既有行保持 CNY，既有 canonical 字节不改写。

对新 offer，准入门禁使用：

```text
B = 冻结束付款基准转换为估值币种后的收入下界
A = 所有较早 outer attempt 的实际/保留成本上界 + 全部已登记 inner 成本
U = 当前候选的保守成本上界
要求：(B-A-U)×10000 >= B×min_margin_rate
且 A+U 不超过冻结的请求累计成本上限
```

B=0 时只允许零成本且 min_margin_rate=0。未知付款、历史成本、候选成本、FX 或不可表示金额一律排除/拒绝，不按零处理。R0 首片虽固定单 attempt，仍在 RecordAttempt 与 PrepareDispatch 使用同一累计函数；未来 >1 必须另立规格，不能删除此复核。

已发送、取消、超时、unknown 或可能消费的成本保持 unknown；明确 not_sent/rejected_no_consumption 才有已知零成本。inner retry 不开放，inner dispatch 不新增客户 request/reservation/settlement。

## 6. 固定全额结算与成本事实

`settleRequest` 只在通过原交付/usage/deadline 收费裁决后选择 `charged_fixed_full_reservation`：`raw=charged=R, waived=0`。其余释放与 pending 分支保持原样。deferred/before-write DB guard 核对该 settlement 与 request snapshot 的模式、金额、原 reservation 和唯一性；普通请求仍使用 `charged_usage`。

`attempt_cost_components` 扩展原采购事实，不建立第二执行或客户账务：

```text
id PK, tenant_id, attempt_id, component_id, layer inner,
source, source_event_id, revision>0, currency CNY|USD,
basis contract|invoice|unknown, amount_micro NULL for unknown,
as_of, evidence_ref, created_at,
UNIQUE(attempt_id,component_id,revision),
UNIQUE(tenant_id,source,source_event_id,revision)
```

同 component 的新 revision 表示更正；同 revision 异内容/币种/金额冲突失败。reporting 对每 component 取最高 revision 并求和；unknown 保持 unknown。outer 继续读 `upstream_attempts` 当前成本指针，inner 不累加为客户收费。P0 不提供外部组件写 HTTP；测试经内部 Store/DB fixture 验证。

## 7. 统一 reporting gross_margin 扩展

继续只使用 `internal/reporting` 的 RR 只读快照和现有 API，不读取第二利润聚合器。

### 7.1 消费退款净付款基准

每条原 settlement allocation 读取累计 `funding_consumption_refund_lots`：

```text
net_charged = charged - cumulative_refunded
net_basis(edge) = floor(net_charged × original_fiat / original_issued)
```

按边、按请求、按原币聚合；不得用 gross_basis−逐次退款 floor 造成余数差异。refunded>charged、缺原边或跨租户引用失败。独立现金退款仍只进入 cash_refunded，不冲减消费付款基准。

退款币回到原 lot 后再次消费会形成新请求的正常分配边；原请求已被净额冲减，因此合计只确认当前净消费对应的现金。每 lot 的全部在报表范围内净消费不得超过 issued，净付款基准不得超过原 receipt fiat；违反即报表失败，不能用 cap 截断掩盖坏事实。

### 7.2 成本时点、原币与折算

`Totals` 追加严格字段：

```text
provider_cost: CNY/USD 原币向量
provisional_gross_margin: string|null
bill_confirmed_gross_margin: string|null
gross_margin_currency: CNY|USD|null
margin_valuation: original_currency|fx_snapshot|null
cost_as_of: RFC3339|null
cost_revision: string
fx_policy_versions: string[]
```

- contract 成本只进入 `provisional_gross_margin`；全部相关 outer/inner 为 invoice 才进入 `bill_confirmed_gross_margin`。未知、未结算、缺付款分配或币种覆盖不足两者均为 null。
- 原 `gross_margin` 保留为当前可显示口径的兼容别名；新增状态字段决定 UI 文案，不得再把 contract 显示为已实现现金利润。
- 同币种以原币相减并标 `original_currency`。交叉币种只在新 offer 有冻结 FX 时使用收入下界/成本上界折算，标 `fx_snapshot` 并列出政策 ID；汇总只在所有请求估值币种一致且覆盖完整时计算。
- 成本更正按最新原指针/component revision 进入当前读模型，可令暂估值下降或为负。`cost_as_of` 取最新成本观察，单请求 `cost_revision` 指向最新证据修订；聚合行无法表示单一修订时为空字符串。
- 请求行新增 `offer_version_id/task_class`；`offer_reports` 按 offer+task 聚合。channel 成本与 request 收入仍分开，不把收入分摊给渠道。

`GET /api/platform/reports/operations[.csv]` 路径、31 天/5000 请求边界及 admin/finance_operator/finance_approver ACL 不变。CSV 在原列后追加商品、任务、原币成本、成本时点与两种毛利列；未知仍为空白。前端严格解析新字段，以“暂估贡献毛利 / 账单确认贡献毛利 / FX 快照折算”显示，不能显示净利润。

### 7.3 未来配置 HTTP（P0 冻结但不暴露）

后续管理路由固定为：

```text
GET/POST /api/platform/service-offers
POST /api/platform/service-offers/:id/transition
GET/POST /api/platform/quality-evidence
POST /api/platform/quality-evidence/:id/withdraw
GET/POST /api/platform/fx-policies
```

admin 管理 offer/task/quality/execution；finance_operator 制单经济/FX、finance_approver 审批后方可使用；owner/member/billing viewer 不得读取采购价、证据内部备注或 FX 来源。所有 POST 使用严格 JSON、Idempotency-Key、CSRF/Origin、MFA 策略、近期再认证、operation 持久化与不可变审计。P0 不注册这些路由，避免无审批配置进入商业链路。

## 8. ACL、故障与恢复

1. migration/owner 执行 DDL；APP 仅获得所需 SELECT/INSERT，service_offer_states 仅允许状态列受控 UPDATE；所有版本、snapshot、route decision、成本组件和撤回历史禁止 UPDATE/DELETE。projection/worker 不获新配置写权，worker 无模型生成权限。
2. 任一授权、价格、offer、任务、质量、FX、成本或 Guard 失败均在供应商 I/O 前回滚；公开错误只含安全代码，不泄露 SQL、报价、凭据或正文。
3. sending 提交后发生的撤回/过期不改写历史、不阻止原请求 finalization/settlement/refund。财务收尾不重新要求当前质量合格。
4. COMMIT 未知只读核查原 request/operation/settlement，不自动重放。备份恢复维持原 recovery gate；不补造付款、成本或质量证据。
5. mock、真实供应商与生产资格分别记录。本文任何通过均不授予 external commercial、真实 profile、支付或生产资格。

## 9. 验收矩阵

必须在专用 PostgreSQL 与 race 下覆盖：

- 版本不可变、route CAS、角色/列权限、跨租户 snapshot/成本/退款隔离；
- CNY/CNY、USD/USD、USD/CNY、CNY/USD，缺/过期/反向 FX、费用与 floor/ceil 反例、大整数溢出；
- paid/grant/unknown、多 lot 余数、fixed full 与正常释放、唯一 settlement；
- 部分/全额消费退款、同一退款 lot 再消费、原收款上界、负毛利；
- contract provisional/as-of、invoice confirmed、outer+inner、成本更正下降/变负；
- task hint/default/unknown、质量缺证/scope/样本/阈值/过期/撤回及 PrepareDispatch 竞态；
- 服务端单 attempt、SafeToRetry、committed/unknown 不重放、恢复不生成。

真实 main 至少完成新 offer 的 mock 请求、fixed-full 请求/报表和桌面/390px 利润标签核查。最终运行相关前端、`make check` 与适用完整 029；不得修改默认 workers=2 决策或既有验收语义来取得通过。

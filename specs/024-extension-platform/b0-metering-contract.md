# 024-B0：媒体维度计量、双边报价与硬预占合同（根审候选）

2026-11-01。**生产接线仍为设计候选；仅[B0-P精确纯层合同](b0-pure-contract.md)已根冻结并实现，见[纯层验证](../../docs/verification/metering-vectors.md)。尚未追加迁移或接入provider/billing。** 本文给出通用计量层后续接线和唯一同步媒体端点候选；本文较宽的subset/diagnostic registry不在已实现纯层中。B0不注册媒体生成路由，不表示任何供应商/模型已可售；B1必须另过官方语义与真实调用/扣费两道门禁。

依据：[024规格](spec.md)、[合同B–F](contract.md#7-bf的后续冻结出口)、[计划](plan.md)、[验收](acceptance.md)、[模块设计](../../docs/design/extension-platform.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)、[018成本路由](../018-cost-aware-routing/spec.md)、[019采购](../019-procurement-accounts/contract.md)、[023-A售价组](../023-periodic-products/a-contract.md)、[023-B事务](../023-periodic-products/b-integration-contract.md)、[022来源候选](../022-self-service-payments/c0-lot-contract.md)、[全钱包序列候选](../022-self-service-payments/c0-sequence-contract.md)、[商品候选](../023-periodic-products/c-contract.md)。来源和商品候选不是已实现前置。

## 1. 真实代码基线及需要改变的边界

| 源码 | 当前事实 | B0必须闭合 |
| --- | --- | --- |
| [provider.Profile/Usage](../../internal/provider/types.go) | 仅六协议；所有profile强制正input token/context；Usage为输入/输出/总token及缓存/推理子集 | 不把图片/音频/字符塞入token；新profile按计量合同区分，旧profile验证保持原义 |
| [pricing](../../internal/pricing/pricing.go)、[catalog发布](../../internal/catalog/store.go) | 默认/组域不可变两维rate；输出输入-only固定0；报价单DB时刻 | 新向量价格同样有版本/生效/组域，不能用dummy token price满足旧FK |
| [Reserve](../../internal/billing/reserve.go)、[金额算法](../../internal/billing/store.go) | ceil((input*input_rate+output*output_rate)/1e6)；资金/Key/周期共一R | 新资源向量先完整估出最坏R，资金仍一次预占；不是每维扣一次钱包 |
| [成本评分](../../internal/routing/cost.go)、[成本复验](../../internal/billing/cost_policy.go) | 两token率、未除分母的整数score；发送前重验原quote | 异分母向量不能直接加分子或比较rate字符串；两侧schema/variant必须绑定 |
| [采购](../../internal/billing/procurement.go) | 逐attempt U和原input/output硬界；unknown/disputed独立；最终成本可大于上界并标异常 | 新采购向量完整硬界、同attempt一次U；客户释放不证明上游无消费 |
| [evidence](../../internal/billing/evidence.go)、[finish](../../internal/billing/settle.go) | normalized_usage固定token JSON；唯一event/finalization/settlement；24h客户释放 | 向量证据须同源去重/冲突，不造两个settlement；未知维度不能隐式填0 |
| [公开目录](../../internal/catalog/public_catalog.go)、[请求账单](../../internal/server/billing_handlers.go)、[非流交付](../../internal/server/gateway.go) | 目录价格/限制都是token；当前非流须完整校验并结算后才交付 | 使用明确单位的新目录/明细合同；media不能显示0输入/0输出；继续结算后交付 |
| [B客户/采购源](../../internal/periodicbudget/tx_sources.go) | 周期量是CNY金额，不是token；客户源时间在request INSERT，采购源时间在reservation INSERT | 向量只产生R/U；全部新来源等待先于相应INSERT，保持原窗/carry |

这是资金计量扩展，不是任意定价DSL或多媒体透传。B0不做图片编辑、音频上传、URL抓取、视频、异步task、流式音频、Realtime、外部工具执行、多币种/汇率或税务总账。

## 2. 实施切片及先后

| 子片 | 最小可交付 | 门禁/并行方式 |
| --- | --- | --- |
| B0-P | `internal/metering`纯类型、schema/向量验证、精确金额/评分、规范序列化、上界比较；旧token等价adapter | 本文经根审冻结后可先独立编码/测试；不依赖供应商或DB，不改provider/billing |
| B0-S | 追加向量schema/两侧价格/请求与attempt快照/证据合同、受限角色和只读核验 | 先出生产SQL全文/触发器/角色原型并根审；不抢当前迁移号，不修改已应用SQL |
| B0-I | 同一billing路径接Reserve/evidence/dispatch/finish/采购/recovery/退款；token_v1与vector_v1分支 | 单一账务集成人持有共享文件；完成真实PG并发/故障/回归后才能给B1调用 |
| B0-Q/W | 新报价/目录/账单DTO、严格HTTP/准入、单位化UI | 冻结Reader/schema后可并行；UI/fixture不代表硬预占通过 |
| B1 | 本文§13选定的一个同步语音profile | 官方计量/硬界合同已取证且真实凭据/账单认证完成；根再冻结精确请求/二进制响应片才注册路由 |

B0-P和官方调研可并行；schema、billing公共类型、资金来源与商品交叉变更串行。B1 provider解析/网络/二进制资源层可在完整profile接口后独立实现，但普通app不能因已有parser而跳过售卖门禁。异步C/长连接D不能同时各改Reserve再直接拼接。

## 3. 闭合的维度、单位与schema

所有业务ID为非nil小写规范UUID；revision/量/费率均十进制string，不收Number、指数、正号、前导零或饱和数。单维quantity为`0..MaxInt64`；累计/中间乘积用NUMERIC/big.Int。时间UTC6，currency=CNY。max8个**收费维度**、max8个已声明subset/diagnostic；集合按dimension ASCII C序唯一，缺字段不是0。

首片通用registry只提供以下名字；registry可表达不表示协议已支持。增加名称/单位/关系必须新代码与新schema，客户端不能自建维度或表达式：

| dimension | base unit | 含义与非推导规则 |
| --- | --- | --- |
| `text_input_token` / `text_output_token` | token | 沿已认证供应商usage含义；独立输入/输出硬界，不从context相减 |
| `text_input_unicode_scalar` | unicode_scalar | 精确Unicode scalar数量；仅官方合同确认按该量收费时才作供应商计量，不能把“字符”自动解释为此单位 |
| `text_input_utf8_byte` | byte | UTF8有效文本编码字节数；不是token/字符的同义词；可作明确出售单位或资源上界，不自动等于供应商收费单位 |
| `image_output_count` | image | 指固定variant下的张数；尺寸/质量等由variant区分，不以一张包含任意分辨率 |
| `audio_input_millisecond` / `audio_output_millisecond` | millisecond | 有限、受认证编码/采样的真实音频时长；不能由HTTP持续时间/文件字节/未验证header估计 |
| `request_count` | request | 明确一次受理/成功的固定收费合同才启用；重试每attempt还是单request须由profile分别声明 |

schema冻结`dimensions`及其`dimension/unit/role/quantity_semantics/upper_strategy`，另列`retail_dimensions`、`supplier_dimensions`，两组可不同，但每组都只引用role=billable维度，且并集≤8。同维度两侧的单位、计数量含义必须相同；供应商的另一单位要用另一个dimension，不能暗中乘汇率或时间系数。

`role=billable|included_subset|diagnostic`。subset须有固定parent与`subset_of`关系，例如cached输入是input的子集，不重复相加；diagnostic如输出文件字节不会因出现而产生收费。媒体size/quality/format/voice不作为可相加quantity；选择variant价格。公开单位名称由固定字典展示，不接受自由HTML/模糊“点数”。

`quantity_semantics=provider_final|request_exact_on_valid_result|validated_media_duration`为闭合枚举；`upper_strategy=published_fixed_limit|validated_request_exact|certified_response_cap`。每种策略须在受审代码里有证明来源：profile全局上限、严格解析出的请求值或供应商可强制的限制。客户端提交的upper_quantities、模型估算token数、价格quote均不是证明。

`request_exact_on_valid_result`只能用于已经认证“成功响应即按明确请求量收费”的profile，不因请求参数看起来可数就启用；断线、错误、缺/损坏输出仍不确认实际供应商消费。没有可证明上界的任一收费维度，profile不可售。

## 4. 两侧费率、舍入、0与溢出

每条rate是`{dimension,numerator,denominator}`，表示`numerator`微CNY/`denominator`个base unit。numerator为非负int64，denominator为1..1,000,000整数；canonical要求gcd=1，零价固定`0/1`。分母上限及max8维保证有理中间量在NUMERIC100/big.Int允许范围；发布拒绝非canonical，而非静默规范化客户端原意图。

```text
Exact(q, p) = SUM_i(q_i * numerator_i / denominator_i)  // 非负有理数
Money(q,p) = ceil(Exact(q,p))                          // 只在合计末尾舍入一次
R = Money(retail_upper_vector, frozen_retail_price)
U = Money(supplier_upper_vector, frozen_attempt_cost)
raw_customer = Money(final_vector, original_retail_price)
customer_charge = min(raw_customer, R)                 // 原收费政策仍决定是否可收费
supplier_actual = Money(final_vector, original_attempt_cost)
```

不能每维先ceil后相加。例两维各1单位、各价1/2微元，合计应1微元，不是2。旧token模式两价分母1e6，原算法`ceil((I*Pi+O*Po)/1e6)`与本算法严格相等；旧rate字段和历史price版本不做归一化改写，仅adapter在内存构造等价有理数。

quote缺某个必须rate、rate=null、basis=unknown或contract/schema不匹配都不是0。已知0价必须显式完整rate line `0/1`，仍保存预占、证据、零额journal及当时有效源/预算见证。inactive dimension不用0价占位；价表精确等于所选side的dimension集合。

发布时先用profile**全部合法请求的全局最大向量**验证R/U可int64，溢出拒绝该版本可售；调用时用本次可信上界再次算并检查余额、Key spent+held、各账户加减/lot桶都不溢出。确认实际量各维≤int64，但价乘量及raw_customer/supplier_actual可能超过int64，保留NUMERIC100；客户仍≤R，不能追加扣款。supplier_actual按019保存真实绝对成本/异常，可能超U/累计预算，暂停并等待财务，不截为U或0。

provider给出超int64或恶意长整数时不截断/饱和；证据变为conflicting，记安全overflow reason，不保存原巨数字符串或生成伪confirmed量。原预算、lot和held保留，按24h客户政策及采购独立unknown收尾。

## 5. 参数variant、报价和不可变版本

vector profile引用不可变`meter_schema_version_id`与编译支持的`variant_policy_id`。variant是从已接受参数按受审函数得到的有限键；首个speech profile只有`default`。将来图片可显式区分size/quality，不能让浏览器自报key绕过参数检查。profile每次发布完全保存计量版本、上界、参数/结果限制及官方合同证据摘要；管理JSON不能注入算式/脚本。

客户向量价格按`model_id + group_domain(NULL默认或一个price_group_id) + schema_version_id + variant_key + effective_at`唯一；同生效时刻NULL一致唯一，按effective_at DESC/created_at DESC/id C DESC确定选择。Reserve在原tenant/model/group锁后、钱包等待前，用一个DB时刻冻结整份rate/schema/variant与A归属快照。无组价仍price_unavailable，不跨组/default fallback；组退休只停新配置，历史/既有归属不重算。

采购向量价按`channel_id + upstream_model + supplier_contract_id + schema_version_id + variant_key + effective_at`选择，basis仅contract/invoice/unknown；未知不可用于首片媒体。渠道版本/凭据必须有019受控采购账户绑定，媒体不提供legacy_unmanaged放行。成本每attempt独立冻结，客户只预占一次R；第二次安全尝试可有不同U/账户/周期。

路由cost_first在同一最高priority层比较**完整最坏采购Exact有理数**，用big.Rat或交叉乘积，不比较已ceil的U而人为制造平局，不将q相乘前的rate直接相加。原weighted tie保留。候选必须证明schema/variant/单位和本次上界全兼容，任何维度rate或上界缺失都cost_unknown排除；max rate合同须扩为逐维有理上限、同单位交叉比较，不能沿token max_input/max_output当通用阈值。

RecordAttempt在渠道/凭据/采购账户等待后重读完整成本版本、quote指纹与schema/variant；PrepareDispatch在最终sending许可时刻重验版本身份和完整rate（包括定时生效），有变拒发并释放原客户资金，**不自动重新规划或重报更高价**。已sending之后的成本/客户价格不变，财务收尾不能因当前profile变更受阻。

## 6. 资源上界、实际量和unknown状态

BoundPlan同时含retail和supplier所需维度的可信上界；所有上界独立成立，不能把两个不能同时达到的峰值相减以降低预占。输入、请求数、输出数、时长和正文/结果字节上限分别检查；字节是资源限制时不自动成为收费维度。客户端可通过更小合法输出参数降低上界，但最终发送必须强制该限制，不能只存在本地BoundPlan里。

向量证据逐项保存`status=confirmed_final|provisional|missing|conflicting`与`quantity:string|null`；missing必须null，不填"0"。confirmed/provisional有合法quantity；conflicting保留经校验的量或null及闭合safe reason，不能把某个相互矛盾计数挑成最终量。aggregate只有全部所需收费维度confirmed、关系自洽、无未知收费风险才confirmed_final。

本片先采用完整性保守政策：未知额外收费维度会使整体计量不确定，不能仍使用部分已知量结算客户或宣称已知供应商总成本。将来若允许“客户固定计数已知、供应商另有未知量”的分侧确定性，须另冻证据/政策，不隐式改变此基线。

| 观察 | 计量与资金处理 |
| --- | --- |
| 必需字段缺失/null、总量矛盾、累计回退、重复field/同event异体 | 不确认最终量；原event冲突链保留；pending/采购unknown或disputed |
| 未声明/未定价收费维度为非零、类型不明、负数、无法枚举嵌套收费对象 | aggregate conflicting，不能丢字段后当0；不以解析错误冒充rejected_no_consumption |
| 未声明字段恰为0 | 仅profile明确列出的零值兼容字段可忽略；任意未知结构仍拒绝确定性，不允许用动态字段名扩大白名单 |
| validated结果完成且所有收费量可证明 | protocol completed与usage confirmed分别保存；进入原客户收费矩阵/唯一settlement |
| 已完整结果但usage不确定 | 保持completed和“结果已生成、费用未确认”事实；客户held待核对，采购held保留；首片非流不交付未结算body |
| 实际量超过已公布上界 | 保存真实已知量，客户最多R，差额waived；block原profile、供应商成本异常/采购暂停。不能重发生成或把actual截小 |
| 非2xx/超时/取消/断流/不完整二进制 | 除正式认证的not_sent或明确no_consumption外，消费未知；错误码/空body不证明0消费 |

观察时间是元数据，不以provider timestamp决定周期。所有original event ID、schema、normalized canonical vector与finalization身份一同参与去重/冲突指纹；同source_event不同维度或量不能覆盖原件。未知结果的恢复只查现有证据或供应商明确只读接口，不重发媒体生成。

## 7. 客户/采购预占、周期与来源的接线

1. 严格解析实际请求→认证profile→BoundPlan；取得现有运行时并发许可；按原principal/模型/价格锁顺序验证版本并冻结R。BoundPlan来自opaque已解析对象，不接受可伪造amount或客户端upper vector。
2. 同一客户Reserve固定R，普通钱包/022当前来源allocator、Key lifetime、tenant/Key B周期同tx。request INSERT及020 pb_admitted_at必须在新维度/lot/权益所有等待后；最后检查Key/deadline/来源期限，不给媒体一条独立余额路径。
3. 逐attempt在019采购账户/采购周期control后算/核完整U并创建采购reservation；新增向量快照等待同样先于该reservation INSERT，使pb_admitted_at不落在上次观察。sending前复验当前硬界/报价/期限；提交后网络IO不持业务事务。
4. finalization保存一个event和完整vector，一次客户finish，原settlement request唯一；原periodic helper仍使用R/C金额，不为每个维度建预算allocation。采购按原attempt的vector/cost计算独立actual，unknown与客户24h释放分开。
5. 消费退款仍绑定原settlement、双审累计≤C；普通lot回原来源；若已实施金额权益则必须按023-C原范围/期限/价值映射恢复，不能把media消费refund全回AVAILABLE；Key/B客户spent不回补。

依赖说明：B0纯层不依赖lot/商品。若B1早于022-C0上线，可沿**当时实际已验收的普通钱包同一账务路径**实现R/C，不能宣称来源lot已支持。C0一旦成为启动必需，媒体同样必须有完整source/wallet-seq见证；迁移/VerifyLots/恢复覆盖media source模式后才继续服务，不能用“旧媒体分支”绕过。023-C金额权益如后续实施，允许仅在该版本明确支持media模型/非token计量时用于CNY抵扣；未完成兼容不得在产品scope授予该模型。Token包不自动承担字符/图片/音频维度，不能拿当前售价换算token余额。

新增资金事实仍沿唯一journal/outbox，量与钱不可分事务。0价媒体也有零额Reserve/finish及完整证据、必要C0 wallet seq，不能因没有正额line跳过来源/周期核验。媒体不会给后台恢复发模型权限，也不因任务短而延长原24h客户截止。

## 8. 候选追加DDL与旧token模式兼容

仅追加迁移，所有新ID规范UUID、跨tenant FK含tenant，事实immutable，源/快照/缓存双向deferred proof；不给迁移号。生产SQL必须另列全文与实际函数/触发器/角色摘要后根审。旧001–当前迁移不修改。

| 表/改动 | 核心列和约束 |
| --- | --- |
| `meter_schema_versions` | id PK、schema_name/version、canonical dimensions/retail_mask/supplier_mask、contract_digest、created_at；UNIQUE(name,version)，固定registry/闭合策略/max8检查；发布后不可变 |
| `model_metering_contracts` | profile_version PK、model_id、schema_version、variant_policy_id、canonical bounds/parameter/result limits、official_evidence_digest、created_at；与不可变model_capability_version同tx，不能给旧profile补一个不同计量语义 |
| `meter_price_versions` / `meter_price_lines` | root id/model/group/schema/variant/CNY/effective_at/created_at/actor/operation；NULL一致域内时间唯一；lines PK(price,dimension)、numerator/denominator，精确retail mask，必须完整同tx；不可增删补旧版本rate |
| `meter_cost_versions` / `meter_cost_lines` | channel/upstream_model/contract/schema/variant/basis/effective/created；完整supplier mask，known时每rate非null，unknown不能伪造known零；发布同步channel管理revision/原审计 |
| `request_meter_bounds` | request_id、tenant、dimension、unit、upper_quantity、bound_strategy、proof_digest；PK(request,dimension)，完整schema所需并集，request来源同tx |
| `request_meter_prices` | request_id PK、tenant、schema、variant、price_version、retail_priced_at、group_id可null、tenant_pricing_revision、canonical rate快照/quote_digest；与所选不可变价逐行一致 |
| `attempt_meter_bounds` / `attempt_meter_costs` | attempt/tenant/维度硬界；原成本版本、contract/schema/variant/basis及完整rate快照；每attempt恰一份原成本，不能按customer request替代第二attempt |
| `meter_event_quantities` | event/tenant/dimension PK，unit、status、quantity nullable、safe_reason；与root schema/aggregate/向量摘要完全一致；same source event异体保留原冲突事实，不覆盖 |
| `settlement_meter_amounts` | settlement_id PK、tenant、schema、original price/quote、confirmed vector摘要、raw/charged/waived；各值逐来源重建且与settlement相同，released按原政策C=0 |
| `procurement_meter_amounts` | original procurement budget effect/event、attempt、原quote、final vector摘要、absolute amount；唯一effect，不以新表另做第二次采购扣费 |
| `meter_operations` | operation UUID、kind/actor/scope/target、safe_input/fingerprint、resource、不可变result_snapshot、created_at；成功回执与发布/审计一对一；不存媒体正文/密钥 |

`request_records`拟追加`metering_mode=token_v1|vector_v1`，旧行默认token_v1；`price_version_id,input_rate,output_rate,input_upper_bound,output_limit`在新迁移允许NULL，但CHECK要求token_v1全部保留旧有效值，vector_v1这些token专用字段**全NULL**且恰有完整meter快照。不插dummy零token价、不以NULL组猜历史；两模式共享A的实际归属/定价时刻、profile/route、reservation和唯一settlement。

`upstream_attempts`同样显式cost_metering_mode和vector quote引用；vector模式token cost字段NULL，known/unknown由新精确价表判定，不看两个NULL就误当legacy放行。`procurement_reservations`旧input_bound/output_bound对vector可NULL，配对CHECK要求其新完整bound集合与U；既有immutable identity trigger必须追加版本化分支，不能UPDATE旧bound或删guard。原采购held/spent/周期金额语义不变。

`metering_events.schema_version`已存在：旧token固定1，新vector固定2并有schema FK/child facts；旧normalized_usage JSON保持原字段，v2 root精确`{mode:"vector_v1",schema_version_id,status,quantities_digest,unexpected_charge_reason}`，量在child表，reason可null。冲突表v2保存经净化canonical向量，不存原provider JSON或二进制。root/child/源request模式双向核验，任一缺失不可COMMIT。

token_v1路径保留旧Parse/Usage/金额公式和SQL分支，不能给所有旧provider响应自动套v2来改变cache/reasoning包含关系。旧请求、旧对账/退款及恢复读取原snapshot；新profile发布、模型转media不能重解释旧request。受限APP不能自行UPDATE metering_mode、量、费率或无新source地更新held。B周期/C0 source validators和完整迁移启动证明必须覆盖新增模式；不能只改Goif后让SQL沿旧token假设放行。

## 9. 锁序、Go接缝与失败语义

```text
Reserve：原tenant/member/user/Key/model → 可选price_group
       → 只读不可变schema/price → B客户controls
       → 全部钱包账户ID序 → 当时已实施的lot/权益锁序
       → request + 向量快照/上界INSERT（固定PB源时刻）→ 同tx原资金/周期/source事实

attempt/派发：原tenant/member/Key/model/channel/credential/reservation/attempt
       → B客户controls（原路径需要时）→ procurement_account → 采购control/reservation
       → 完整原quote与锁后时刻重验 → sending许可 → COMMIT → 单次网络发送

finish/refund：原request/reservation/必要model升级/客户controls
       → 全部账户 → 原lot/权益 → 最后DB时刻/24h → 原settlement与预算/source原子事实
```

向量schema/price是不可变SELECT，不能在账户/lot后为了“刷新计量”取另一model/channel/group锁。新schema发布/客户价发布按admin user SHARE→model SHARE→可选group SHARE；成本发布沿channel NO KEY UPDATE及既有management CAS，不锁客户tenant/账户。所有FK隐式KEY SHARE纳入原型，禁止vector fact→前层业务根新UPDATE锁。原finish超界model升级已存在潜在死锁重试边界，本稿不能把它声称已消除；新增向量不引入另一轮升级。

建议精确纯层接缝：

```go
// 所有值构造时校验，内部字段不可由调用者直接改；无DB/环境/provider依赖。
ParseSchema([]byte) (Schema, error)
ParseRateVector(Schema, Side, []byte) (RateVector, error)
ParseQuantityVector(Schema, []byte) (QuantityVector, error)
ValidateBounds(Schema, QuantityVector) error
Amount(QuantityVector, RateVector) (*big.Int, error) // 返回ceil后的非负整数微CNY
CompareExactCost(QuantityVector, RateVector, QuantityVector, RateVector) (int, error)
WithinBounds(QuantityVector, QuantityVector) (bool, error)
CanonicalDigest(Schema, QuantityVector, RateVector) ([32]byte, error)
LegacyTokenRates(input, output int64) (RateVector, error)
LegacyTokenUsage(input, output int64) (QuantityVector, error)
```

数量与费率任何schema/单位/side集合不合都返回受控error，不用零值凑齐；复制输入slice避免外部mutation。Amount零值只在完整明确0量/0价时成立；big.Int结果调用者必须clone，避免alias修改证据。Compare比较完整Exact费用，不能把retail与supplier对象或不同schema同单位误混。

交易helper仍显式同pgx.Tx、不Begin/Commit/联网，BoundPlan由已验证request生成并绑定profile/schema/variant、request HMAC、真实上界、协议目标；DB核当前profile后才接受。上层不得用`Trusted:true`、自报R/U或“我已验证”的字符串越过。正式B0-I片需冻结这些opaque proof签名及每个SQL INSERT位置后再编码。

写RC、lock3s/statement5s、总10s与caller取短；只对确定回滚40P01/40001同IDclosure最多初次+2，清理独立≤5s，COMMIT未知销毁连接并返回零结果，仅读原operation或原request/settlement。原推理幂等只能发现原request，不能重发媒体以重建body。后台恢复没有provider生成客户端。

## 10. 新目录、quote、账单与API

旧token端点/DTO保持明确单位和原结果。新增media model不进入只支持token价格/profile的旧`/api/public/models`及旧`/api/models`视图；新控制台切换以下v2目录展示两类。标准`/v1/models`可按原当前Key授权列出已发布可用media模型名，字段仍标准id/object/created/owned_by，不借列表授予调用权限或塞费率。媒体能力未认证时不作为可调用模型发布。

| API | 精确职责/当前权限 |
| --- | --- |
| GET `/api/public/model-offerings/v2[/{id}]` | 只显式public_visible且enabled模型，默认价格域，不按Cookie个性化；原public限流/匿名门禁；不能透露schema内部/采购/tenant |
| GET `/api/model-offerings/v2[/{id}]` | 当前active tenant/user/member，模型可见性交集；本tenant实际价格组和quote；API Key仍用/v1/models，不枚举组 |
| GET `/api/models/{id}/metering-price` | 当前有模型读权，返回MeterPriceView；单SQL DB as_of，缺价status unavailable、rates=[]，不伪造零价 |
| POST `/api/models/{id}/metering-quote` | `{profile_version,variant_key,upper_quantities:[{dimension,quantity}]}`；只做有界**假设量**报价，不能准入/锁价/选lot；实际请求重新解析派生BoundPlan；不接prompt/音频正文 |
| POST `/api/platform/models/{id}/meter-prices` | admin，`{operation_id,price_group_id:null|UUID,schema_version_id,variant_key,rates,effective_at}`；不可变新价，完整集/合法profile，组须active；默认/组域显式，不复用旧默认发布body |
| POST `/api/platform/channels/{id}/meter-costs` | admin，`{operation_id,expected_revision,upstream_model,supplier_contract_id,schema_version_id,variant_key,basis,rates,effective_at}`；沿channel CAS，unknown时rates=[]且不可路由，不凭空补known0 |
| GET `/api/metered-requests/{request_id}` | 当前tenant owner/billing_viewer可读本tenant；member仅自己request；同snapshot原零售quote/上界/最终量/状态/资金，不含采购rate/渠道 |
| GET `/api/platform/metered-requests/{request_id}/attempt-costs` | 当前admin/finance_operator/finance_approver；逐attempt原quote/上界/量/actual/unknown，不输出credential/raw supplier body |
| GET `/api/metering-operations/{operation_id}` | 仅当前仍有对应资源读权的原scope/actor；平台写回执另固定platform scope，不向tenant暴露发布/成本原操作 |

正式schema注册和media profile发布不开放任意HTTP `schema JSON执行`：代码registry及受审迁移/发布命令先登记支持的schema；管理端仅引用已支持ID与固定variant。旧`/api/prices`与组价发布仅token_v1，不接受media假价。

```text
RateLine={dimension,unit,numerator,denominator}
QuantityLine={dimension,unit,status,quantity:string|null,reason:SafeReason|null}
UpperLine={dimension,unit,quantity}
PriceRef={kind:"token_v1"|"vector_v1",id:UUID}
MeterPriceView={model_id,profile_version,metering_mode,schema_version_id:UUID|null,variant_key,
 source:"default"|"group",price_group_id:UUID|null,status:"available"|"unavailable",
 price_ref:PriceRef|null,currency:"CNY",rates:RateLine[],effective_at:UTC6|null,as_of:UTC6}
MeterQuoteView={price:MeterPriceView,hypothetical:true,upper_quantities:UpperLine[],
 reservation_amount:string|null,as_of:UTC6}
MeteredRequestView={request_id,model_id,metering_mode,profile_version,schema_version_id:UUID|null,
 variant_key,price:MeterPriceView,upper_quantities:UpperLine[],final_quantities:QuantityLine[],
 usage_status,billing_status,reserved_amount,charged_amount:string|null,raw_retail_amount:string|null,
 waived_retail_amount:string|null,settlement_id:UUID|null,as_of:UTC6}
```

token_v1通过adapter可在新目录显示同等两rate及token quantities，schema_version_id=null明确legacy，不造一个伪UUID；vector_v1必非null。unavailable price_ref/effective_at为null、rates=[]；rate已知0仍available。MeterQuoteView的reservation_amount仅完整合法可用价才非null；hypothetical始终true，不能回传该quote当调用授权。MeteredRequestView.price是原请求快照，其as_of=原retail_priced_at；父as_of是本次读时刻，不能换当前价冒充原报价。

字段集合精确、nullable显式null、空集合[]。新list固定50+1、末页next_cursor=""，HMAC cursor绑定当前identity/scope/route/筛选和C序排序；不收limit/offset。请求/响应新JSON管理body≤8KiB、UTF8严格重复/未知/尾对象拒绝；新纯schema≤16KiB，不承载媒体正文。读5s，写10s；读全局32/actor4，原operation核查另8/actor1且2/s burst4，写8/actor1且1/s burst2，先于DB Guard取得许可。

旧`/api/requests`继续列全部请求的金额和公共状态；vector行token专用列为null，绝不显示0token，现有字段含义仍是token。新控制台必须读取metered明细并用单位列表显示media；不能沿[旧RequestsView](../../web/src/views/RequestsView.vue)的固定“输入/输出Tokens”渲染为已支持。旧token行输出与金额/报价不变；新价/账单客户端严格按mode解析。共同钱包/journal/报表金额不按模型类型遗漏media，采购unknown计数仍完整。

## 11. 管理命令、角色、敏感数据与未知结果

新发布命令沿023成功operation语义：成功新版本201，原ID同actor/scope/kind/target/safe_input返回原不可变200，异体409`meter_operation_conflict`；业务失败全回滚，不复用022 commerce HTTP200 rejected。根冻结SQL片时分配独立operation advisory namespace/规范fingerprint与kind闭合集，不能挪用A/B/022域。所有数据库当前用户/角色、model/group/channel存在和CAS在正确父锁后复验；当前失权403，不可见资源404。

新路由错误候选：400 invalid_request/invalid_cursor/invalid_meter_vector，402 insufficient_balance，409 meter_operation_conflict/meter_profile_changed/meter_revision_conflict/meter_variant_unavailable，429 rate_limited，503 price_unavailable/cost_unknown/meter_unavailable/meter_outcome_unknown/meter_invariant。推理向量解析失败在Reserve前400；model/schema配置故障503且无上游请求；已发送后用量错误保持unknown，不通过400假称未消费。只映射精确受控类型，不按任意PG字符串猜业务错误。

管理写仍要求CSRF、当前账号已启用才MFA、10分钟再认证、ready/Instance Guard/非recovery。读取按当前权限，历史回执不能赋予旧actor读成本。API Key仅推理/标准目录，无schema/价格发布或财务裁决权；Console owner不自动获得采购价。

APP只新增必要表SELECT/不可变INSERT，request/attempt/vector cache保持原列级投影权；禁止历史mode/schema/rate/证据UPDATE。projector仍只读允许的outbox/safe projection，不读prompt、媒体、所有成本底表；需要新报表维度时单独扩最小事件payload和角色。payment worker无任何meter价格/请求/媒体量写权；迁移owner、离线只读核验实际LOGIN与APP分离，新函数默认撤PUBLIC EXECUTE、全部invoker且固定search_path，启动有效权限闭包/完整迁移证明一同更新。

prompt、音频/图片/二进制、base64、完整provider原错误、音色内容均不进入日志、audit、quote、DB证据或outbox；仅request HMAC、受控供应商request reference、已净化schema/数量/量来源和资源大小。供应商返回的媒体URL不下载、不透传任意URL；未来允许另冻SSRF/保留期合同。默认只在有界内存保留结果至本次写完，退出/取消释放；不偷偷存磁盘或对象存储来实现重放。

## 12. B0独立核验、迁移及恢复

新增离线只读MeteringVerifier在同一RR快照核：profile/schema引用→不可变价及完整rate→request/attempt bound与quote→event完整量/冲突→唯一settlement/采购effect→账本/周期/当前来源图。正反向均查，不能只SUM金额相等；旧token1按原算法重算，vector2按同一精确有理算法独立重算。未找到必需schema/源是mismatch，无法证明旧历史见证是indeterminate，不用“legacy”跳过所有新模式。

与022/B核验同样使用balanced/mismatch/indeterminate及0/2/3/1出口；权限/迁移/超时不返回半份balanced。Rebuild仍维护态单事务/active projection切换，只重放安全已提交财务事实，不重新解析保留期外的媒体正文、不发生成。新模式outbox仅含重建需要的既定金额/维度/成本状态和原ID，不能由统计worker调用Reserve。

升级先停接单→排空→停原实例→追加迁移/provision有效权限→新schema自证→启动。旧token请求仍可按原snapshot finish/refund；vector未完整定义时新app fail closed，不把它当旧token0。恢复旧备份沿统一recovery latch，核对外部财务/供应商来源与本地request/attempt未知；没有task ID或结果body不构成可安全重发证明。

## 13. B1唯一首端点候选：同步、固定参数TTS

建议第一片只做`POST /v1/audio/speech`，供应商OpenAI、上游模型标识候选`tts-1`，固定voice=`alloy`、response_format=`wav`、speed=`1`、非stream；这些是**拟验证的产品选择**，本次没有确认它们当前可用。`tts-1`若仅有可变alias，profile准确声明固定模型标识/本地合同修订，不声称上游权重固定；根须接受该版本边界并建立变更复核，或换成官方支持的可固定版本后再冻B1。不能静默替换为另一模型。

首片输入拟限制1..1000个U+0020..U+007E可见ASCII字符（全空白拒绝，不做trim重写）；body≤8KiB，除model/input/voice/response_format/speed外拒任何字段，固定字段可省略并由gateway显式注入固定值，显式异值拒绝。无instructions、custom voice、SSML、远程文件、UTF8多语言或音频输入。这减少字符计数差异的首片认证面，后续中文/Unicode支持是明确后续片，不在目录冒称通用语音生成。

候选收费维度只为`text_input_unicode_scalar`（该受限ASCII下同时等于UTF8字节/UTF16码元），上下界来自实际提交input数量；资源输出字节不收费。**仍须官方/真实证据证明计费按提交文本何种字符、空格/转义/规范化/失败的含义，不能因ASCII相等就自行给supplier_final确认。** 如果供应商实际上存在音频输出token、按时长、请求固定费或任何额外非零收费维度，拒绝这个profile，重新设计schema与硬上界，不把这些维度放diagnostic逃避采购。

结果仅接受一个完整有界WAV：拟≤8MiB、有效RIFF/WAVE、受支持未压缩PCM fmt、固定通道/采样率/位宽须先由官方B1合同确认，data范围/字节对齐/RIFF长度自洽；不解码压缩音频，不执行附件元数据。输出时长可作为诊断，由已验证frames/sample_rate精确有理计算，若非整数ms保留frames+rate诊断，不向下取整当收费量。供应商无法保证该格式时不沿“看起来有RIFF”放行，换固定可认证格式再审。

请求总deadline拟120s（不超现有300s），上游header30s、body idle15s；全局媒体buffer槽4/每Key1，取得槽才Reserve，8MiB×4加解析开销须实测，不能把通用文本并发数照搬。上游流式传输仅作为有界读body，不对客户逐块写。完整校验→持久finalization→唯一settlement确认→Content-Type audio/wav/精确长度后写出；写失败仅delivery observation，不自动冲正或重生成。取消立即关闭body并join资源；已sending未知按原政策收尾。

没有已确认计量或结算失败不输出缓存媒体，返回billing_pending及request_id；本次有界buffer释放，不以后台再次生成找回结果。已有完整确认用量但worker处理越24h仍客户释放；采购真实成本独立保留。B1默认不自动重试媒体生成；既有安全not_sent候选也须B1独立认证后根审是否允许第二attempt，不能把文本重试白名单原封不动继承。

### 13.1 官方语义取证与真实认证分开

本次已按[OpenAI Docs技能](/Users/liminghan/.codex/skills/.system/openai-docs/SKILL.md)读取流程；当前工具目录没有专用官方docs search/fetch。尝试官方域搜索与直接读取下列来源时，HTTP返回403；随后独立Chrome页打开语音指南，页面明确为Cloudflare“Sorry, you have been blocked / You are unable to access openai.com”。没有绕过挑战，没有读取到官方正文，故下列链接仅为**待取证来源地址，不是已验证证据**；未引用任何搜索摘要充作官方事实：

- [语音指南](https://platform.openai.com/docs/guides/text-to-speech)
- [Create speech API](https://platform.openai.com/docs/api-reference/audio/createSpeech)
- [tts-1模型](https://platform.openai.com/docs/models/tts-1)
- [API定价](https://platform.openai.com/docs/pricing)

**门禁A：官方合同证据。** 须实际取得正文/精确版本或抓取日期/内容摘要，确认当前模型/endpoint/voice/格式可用性、最大input、计数单位、全收费维度、强制资源界、错误/取消/重试、是否有最终usage/供应商查询和provider request ID。商业采购CNY费率由平台真实合同登记，本稿不把美元网页价自动换汇。若官方不给billing字符定义或成本硬界，补供应商书面合同/可验证证据；缺任一项不冻可售profile。

**门禁B：真实凭据与扣费/用量认证。** 独立测试项目、最小额度、当前受限profile，用真实供应商成功/拒绝/断线/取消/超时结果和供应商用量/账单交叉核对所申报单位、cost upper与实际收费；不能只看HTTP2xx/SDK不报错。证明中只保存安全reference/已脱敏量，凭据不进仓库/日志。没有真实凭据时该门禁保持未完成；不要求用户为B0纯算法提供密钥，也不让本地fixture冒充真实认证。

本次两道门禁均未通过，不应注册可售TTS或显示“OpenAI媒体支持已完成”。这不妨碍先实现/验收通用B0-P。

## 14. 验收与并行交付清单

| 编号 | 必须验证 |
| --- | --- |
| MB01 | 旧六协议token价/usage/缓存子集/输入-only/原historical request逐例等价，v2 adapter与旧金额精确相同；不修改旧快照 |
| MB02 | max8维、单位/顺序/重复/缺rate/null/0rate、canonical分母、异分母精确合计、单次ceil、跨乘比较；大整数/边界raw不饱和 |
| MB03 | 全局最大请求发布溢出、单次R/U溢出、Key和账户加法溢出；真正actual超R/U保留并异常，不追加客户扣款 |
| MB04 | 缺usage、额外非零维度、未知嵌套、零兼容字段、累计回退、重复source不同vector、损坏二进制；不把completed变成已证实无消费 |
| MB05 | 同request一次Reserve/settlement、逐attempt独立U、报价/成本定时生效、调组/退休、输入upper/variant改变、采购账户等待跨期及最终sending前复验 |
| MB06 | 客户24h释放/消费refund不释放采购unknown；客户与供应商原周期归属，B carry不重置；同tx audit/source/lot/periodic任一失败全回滚 |
| MB07 | C0开启前/后、0价媒体、来源FIFO/序列/退款在途不可花、商品金额权益过期释放/refund、token包不承担字符；未实施的交叉继续门禁 |
| MB08 | 受限APP/worker/projector负权限、直接SQL伪vector/改mode/缺尾rate或量、旧effect重放、跨tenant FK；完整migration/启动attestation无额外权限 |
| MB09 | 同operation、同source并发、取消/lock/statement/COMMIT丢包、进程崩溃/Guard/recovery/off；原GET核查，后台无生成网络 |
| MB10 | 新目录/quote/明细真实HTTP/当前权限/公开默认/本tenant组价/采购隔离；null≠0、新旧schema严格解析、无正文/二进制泄露 |
| MB11 | 真实main+窄屏/桌面UI正确显示字符/张/毫秒和待核对、无伪0token；完整资金/采购/lot/预算核验及Rebuild同RR，unknown成本不按0报利润 |
| MB12 | B1四个buffer槽、超大/分片WAV/长度冲突/慢body/客户断开/非流写失败、资源join/SIGTERM；fixture、真实进程及外部认证分层记录 |
| MB13 | 官方门禁A原文/摘要与真实门禁B扣费证据；任一未有不宣称供应商/SDK/生产可售，B0通过不能代替B1 |

根需裁决：最多8维和有理费率/统一ceil、两侧不同维度集但完整硬界、媒体强制019受控采购、vector模式旧token字段NULL/独立价表、旧目录隔离与新单位DTO、unknown维度整体保守、C0/商品按当时真实模式完整接线，以及ASCII固定TTS作为B1研究候选。本稿待根裁决后，先实现B0-P；生产SQL/公共账务/provider/B1需要各自冻结与验收。

集成人实施后同步统一架构中的计量模块、请求/报价/双边资金链路、schema/price/quantity/source数据关系、角色和内容边界、新旧能力状态/源码及证据链接，连同HLD/LLD。并行结果汇总后再reviewed refresh；本文仅新增设计，没有DB/main/浏览器APP验收或架构refresh。

# 028-U：共同值域与文本硬界合同

2026-11-04；状态：**共同签名已由集成人冻结，纯实现与专项race已完成；不是生产接线或供应商认证**。本文细化[028 主规格](spec.md)的 U 切片；以[纯计量核](../024-extension-platform/b0-pure-contract.md)、[HLD](../../docs/design/HLD.md)和[LLD](../../docs/design/LLD.md)为上位合同。只增加一套共同值域，不增加第二套协议 parser、价格计算器、账务入口或历史 DTO fallback。

## 1. 包、权限与本片边界

新增 `internal/inference`，只依赖标准库、`internal/strictjson`与 `internal/metering`；没有 DB、网络、环境读取、时钟读取、随机身份、凭据或角色判断。现有 `internal/provider` 独占六文本请求/响应/usage 的严格解析、原生字段与包含关系验证；不在共同包再写一份 Chat/Responses/Messages/Embeddings/Rerank/Gemini parser。

共同包验证不可变 profile、从发布候选集合导出硬界、报价快照及结构化观察。`billing` 必须重读受权的不可变配置并核验完整候选集合、route revision、请求/attempt/tenant 身份、原 quote；共同对象本身不授予调用权、资金权、供应商认证或安全重试权。客户 HTTP 不接受本文内部 JSON。

U 只接受六种文本协议和 `synchronous`，生成协议允许 `json|events`，输入型仅 `json`。图片、任务、实时的枚举/工厂须在其具体合同冻结后追加；不能仅因值域存在而开放端点。观察的 schema/mask 运算不限定文本两维，以便直接验证纯核已支持的分侧 mask；这不产生任何媒体调用能力。

## 2. 最小 Go API

下面是本片唯一公开面；名称由集成人一次冻结。所有 opaque 类型私有字段、零值无效，不实现允许绕过工厂的 `UnmarshalJSON`，不提供 setter。

```go
type Protocol string
const (
    ProtocolChat Protocol = "openai_chat"
    ProtocolResponses Protocol = "openai_responses"
    ProtocolMessages Protocol = "anthropic_messages"
    ProtocolEmbeddings Protocol = "openai_embeddings"
    ProtocolRerank Protocol = "jina_rerank"
    ProtocolGemini Protocol = "gemini_generate_content"
)
type ResultKind string // json | events
type PlanScope string // request | attempt
type QuoteStatus string // known | unknown
type UsageState string // confirmed_final | provisional | missing | conflicting

type Profile struct { /* private */ }
type BoundPlan struct { /* private */ }
type QuoteSnapshot struct { /* private */ }
type Observation struct { /* private */ }
type FinalQuantities struct { /* private */ }

type TextProfileInput struct {
    Version, Model, VariantKey string
    Protocol Protocol
    Schema metering.Schema
    ContractDigest [32]byte
    InputTokenUpperBound, OutputLimit int64
}
type TextPlanInput struct {
    Candidates []Profile
    InputHMAC [32]byte
    OutputLimit int64
    Stream bool
}
type ObservationBinding struct {
    TenantID, RequestID, AttemptID string
    ProfileVersion, VariantKey string
    Schema metering.Schema
    PlanDigest [32]byte
}

func NewTextProfile(TextProfileInput) (Profile, error)
func NewTextPlan(TextPlanInput) (BoundPlan, error)
func (p BoundPlan) ForAttempt(Profile) (BoundPlan, error)
func ParseQuoteSnapshot(raw []byte, plan BoundPlan, side metering.Side) (QuoteSnapshot, error)
func ParseObservation(raw []byte, expected ObservationBinding) (Observation, error)
func (o Observation) Confirmed(side metering.Side) (FinalQuantities, error)

func (p Profile) Info() (ProfileInfo, error)
func (o Observation) Binding() (ObservationBinding, error)
func (o Observation) Metadata() (ObservationMetadata, error)
func (o Observation) Entries() ([]DimensionUsage, error)
func (o Observation) State(side metering.Side) (UsageState, error)
func (p Profile) Schema() (metering.Schema, error)
func (p BoundPlan) Schema() (metering.Schema, error)
func (p BoundPlan) Scope() (PlanScope, error)
func (p BoundPlan) Bounds(side metering.Side) (metering.Bounds, error)
func (p BoundPlan) Profile() (Profile, error) // only attempt; request is an error
func (p BoundPlan) VariantKey() (string, error)
func (p BoundPlan) InputHMAC() ([32]byte, error)
func (q QuoteSnapshot) Status() (QuoteStatus, error)
func (q QuoteSnapshot) Bounds() (metering.Bounds, error)
func (q QuoteSnapshot) Rates() (metering.Rates, error) // unknown is an error
func (q QuoteSnapshot) VersionID() (string, bool, error)
func (q QuoteSnapshot) UnknownReason() (string, error)
func (q QuoteSnapshot) Amount() (int64, error) // unknown is an error, never 0 fallback
func (q FinalQuantities) Quantities() (metering.Quantities, error)

// Each of Profile, BoundPlan, QuoteSnapshot, Observation, FinalQuantities has:
// CanonicalJSON() ([]byte,error), Digest() ([32]byte,error)
```

`ContractDigest` 是受权发布端对完整不可变 provider 合同的摘要，包括工具、可用参数、usage 语义等；共同包不另造该合同的 parser。adapter 与 billing 必须核它和当前不可变版本一致。全零 digest/HMAC 作为缺失拒绝，不接受 `Trusted:true` 或 caller 自填金额。

`ObservationBinding` 是受信调用方从原请求/attempt 快照构造的期望值；不是可由客户端声明的认证。它独立列出 schema，以便分侧 mask 逻辑不依赖未来某种媒体 profile 工厂。生产 reader 必须把它的 plan/profile/variant/schema/tenant 逐项绑定原件，不能只核其中一个 digest。

计量层新增只读描述 API：`Schema.ID() (string,error)`、`Dimensions() ([]Dimension,error)`、`Mask(Side) ([]string,error)`，`Dimension{Dimension,Unit string; MaximumQuantity int64}`；另有`Rates.Entries()([]Rate,error)`，`Rate{Dimension,Unit string;Numerator,Denominator int64}`；全部描述 slice 返回 copy。不复制 registry，不通过反解析 canonical JSON 偷取内部数值，不给 Bounds/Rates 增可变字段。

## 3. 文本 profile 与公共/attempt Plan

文本 schema 必须恰有 `text_input_token/token`、`text_output_token/token` 两维，双方 mask 都完整，维度序与纯核一致。`InputTokenUpperBound` 必须正且不超过 schema 对应 maximum；生成协议 `OutputLimit` 必须正且在 maximum 内；Embeddings/Rerank 必须为 0。它们的输出 0 仍在 Bounds/Rates/Quantities 中显式保留，不能省字段或省维度。

`Version` 必须为非零 canonical UUID，与现行创建/DB版本身份一致，不接受旧自由字符串；`Model` 是 1..200 字节合法 UTF-8、无控制字符的公开模型名；U 的 `VariantKey` 必须精确 `default`，不接受空值默认。protocol 显式穷举，空/未知一律拒绝；空协议只可在模型创建入口显式定值，不能在本层 fallback。

当前U生产发布/路由沿现有唯一model capability，route reader先按完整profile digest去重、billing重建核对，实际闭包只有一个profile；不新增supplier profile表或binding profile列。下面1..256候选max/收紧是纯值域算法的封闭验证范围，不表示异构多profile路由已安装/发布；未来须先补其binding身份与参数兼容规格。

`NewTextPlan` 接受 1..256 个已验证候选，复制该集合，按 profile digest 的原始 32 bytes 排序。重复 digest、同 `(Model,Version)` 不同合同、不同 Model/protocol/schema 完整 digest/variant 或 input-only 类别均拒绝。所有候选都须能够满足 parser 已选择的本次 output：生成量 `1..min(candidate.OutputLimit)`；input-only 必须精确 0 且 Stream=false。Stream 来自唯一 provider parser 的私有有效值，生成 Plan 按其取 result_kind=json|events；Profile允许两者，不为流式/非流式拆两个版本。未提供 output 时由唯一 provider parser 按发布默认选定，再传本层；本层没有默认或 context-minus-output 推导。

request Plan 的两侧 input 公共界为全部合法候选 `max(InputTokenUpperBound)`，output 为本次已选 output。使用 `metering.LegacyTokenBounds` 构造完整两侧向量。公共 Plan 绑定**整个排序候选集合**，不强制绑定某一个候选的 profile digest。route reader 与账务须核集合完整等于该 route revision 的合法候选闭包；本纯包不能发现调用方漏交了 DB 中的候选。

`ForAttempt` 只可用于 request Plan，候选必须完整 digest 命中原集合。派生 attempt Plan 保留输入 HMAC、schema、variant、本次 output、父 request Plan digest，并绑定所选 profile；input 界取该候选的强制上界，双方界只可等于或收紧公共界，不能放大。不能对 attempt 再调用 `ForAttempt`，不能添加新候选或另选输出。客户零售报价绑定原 request Plan；每个供应商报价绑定该 attempt Plan。客户 R 不因路由选择而重算或重新预占。

两个合法候选 A(input=100)、B(input=120)，本次 output=20：公共 input=120/output=20，A attempt=100/20，B attempt=120/20；候选 A 的缺省 output 或当前价格均不改变公共快照。供应商不强制其声明界时是已发送后的界异常，不能通过降低 Plan 掩盖。

Plan 的 canonical JSON 为固定字段序：

```text
{version:"1",scope,model,protocol,execution_kind:"synchronous",result_kind:"json"|"events",
 schema_id,schema_digest,variant_key,input_hmac,output_limit:"decimal",
 candidate_profile_digests:[hex...],selected_profile_digest:null|hex,
 parent_plan_digest:null|hex,retail_bounds:MeterBounds,supplier_bounds:MeterBounds}
```

Profile canonical 为 `version:"1",profile_version,model,protocol,execution_kind,result_kinds:["json"]|["json","events"],schema_id,schema_digest,variant_key,contract_digest,input_token_upper_bound,output_limit`，最后两项为规范十进制字符串。canonical 不带正文、凭据、upstream URL 或当前价。候选 digest 集合不能代替原 profile 快照的持久保存与重建；现行 DB reader 用原 profile 工厂重建并比较 canonical/digest，不增加宽松的 arbitrary-Bounds restore 入口。

## 4. 统一报价联合与金额

`ParseQuoteSnapshot` 的 raw 上限 16KiB，必须恰好有以下字段；`bounds/rates` 是嵌套纯核原 wire，不是 JSON 字符串：

```text
{version:"1",status:"known"|"unknown",side:"retail"|"supplier",
 plan_digest:hex,schema_id:uuid,schema_digest:hex,variant_key:"default",
 version_id:uuid|null,unknown_reason:null|Code,
 bounds:MeterBounds,rates:MeterRates|null}
```

外壳身份、side、schema 完整 digest、variant、plan digest 必须与传入 Plan 一致；bounds 须与该 Plan 对应侧 canonical **完全相同**。Retail 只接受 request scope，Supplier 只接受 attempt scope；不把公共供应商最坏界误当单 attempt U。

| 联合 | 精确合同 |
| --- | --- |
| known | `version_id` 非空 canonical UUID，`unknown_reason=null`，`rates` 完整非 null；交 `metering.ParseRates` 校验且以 `ReservationAmount` 计算 int64 R/U；溢出拒绝 |
| supplier unknown / version_missing | `version_id=null`、`unknown_reason="version_missing"`、`rates=null`；bounds/全部绑定仍存在 |
| supplier unknown / basis_unknown | `version_id` 必须非空，保留实际选中的不可变 cost version，`unknown_reason="basis_unknown"`、`rates=null` |

Retail unknown、unknown 中附 rate、known 缺某维、零价却缺 version 均拒绝。unknown 的 `Rates/Amount` 返回 `ErrUnknownQuote`；`VersionID` 用 `(value,present,error)` 表示缺失。known 的 UnknownReason 返回空字符串；不把它当 unknown 联合。quote canonical 只有上述字段，不接受 caller 的 amount/U/R/quote_digest。

known 的底层数值指纹调用 `metering.QuoteDigest(bounds,rates)`；外层 quote digest 另绑定 status、版本、plan 和完整 canonical。unknown 没有虚构的底层 quote digest或零金额。数量已确认与采购价已知相互独立；只有文本人工采购沿现行 `unknown_cost_policy=allow` 可由账务批准 unknown，媒体与受管采购必须 known。本纯包不作该准入决定。

`ProfileInfo`为TextProfileInput的逐值字段加`ResultKinds []ResultKind`；`ObservationMetadata`包含source/source_event_id/provider_reference/observed_at(time.Time)/protocol_outcome/transport_end_reason/delivery_stage/content_written/finish_reason/unexpected_charge_reason，缺失nullable字符串映射空字符串；`DimensionUsage{Dimension,Unit string;Status UsageState;Quantity *int64}`的Entries返回深复制。State(side)优先conflicting，其次缺量/未归属收费/人工supplier=missing，再provisional，最后confirmed_final；不会把missing降成0。

文本旧每百万 Token 标量转率时只调用 `LegacyTokenRates`，其余报价/金额都用 `ParseRates/ReservationAmount/Amount/CompareExactCost/WithinBounds`；不留在线标量分支，不复制浮点或 ceil 计算。

## 5. 观察、分侧终态与冲突

`ParseObservation` 上限 32KiB，必须恰有：

```text
{version:"1",tenant_id,request_id,attempt_id,source,source_event_id,
 profile_version,schema_id,schema_digest,variant_key,plan_digest,
 provider_reference:null|string,observed_at:"UTC RFC3339Nano",
 protocol_outcome,transport_end_reason,delivery_stage,content_written:boolean,finish_reason:string,
 quantities:[{dimension,unit,status,quantity:string|null}],
 unexpected_charge_reason:null|"supplier_unmapped_charge"|"unattributable_charge"}
```

所有 ID 为非零、小写、标准 36 字节 UUID；hex 为小写 64 字节。tenant/request/attempt/profile/schema/variant/plan 全部与 expected 相等。source 首片仅 `adapter|retrieval|gateway|approved_reconciliation`，后续 task/实时来源须先扩其合同；source_event_id 为 1..200 字节、reference 为 1..200 字节，均仅 `[A-Za-z0-9_.:-]`，不接自由 URL。observed_at 只供诊断，必须可解析且规范 UTC；不读取系统时钟填缺值，不作为价格/周期资格时间。

沿用现行闭合集合：protocol_outcome 为 `completed|failed|incomplete|cancelled|unknown`；transport_end_reason 为 `done|eof|provider_error|platform_error|platform_timeout|client_cancelled|connection_error|unknown|not_sent|rejected_no_consumption`；delivery_stage 为 `none|committed|content|unknown`。`content_written` 与 `delivery_stage=content` 必须一致，不靠工厂悄悄修正原事实。`finish_reason` ≤80字节合法UTF8且无control，可为空。业务/交付/无消费资格仍由账务状态矩阵判定；获得最终量本身不等于准许客户收费。

quantities 必须按 schema 的完整 dimensions ASCII 顺序提供每个维度，单位严格一致。每维 `confirmed_final/provisional` 必须带 `0..MaxInt64` 规范十进制 quantity；`missing/conflicting` 必须为 null。真实量超过 schema maximum 仍保留，不因超界改成 missing 或截断；纯核 `ParseQuantities` 允许该事实，`WithinBounds` 和账务原 R/U 上限另判。

`Confirmed(side)` 只查看该侧完整 mask：

1. 非法 side/零对象先拒绝；任一 mask 维为 conflicting 返回 `ErrConflicting`；任一 missing/provisional 返回 `ErrIncomplete`。
2. `supplier_unmapped_charge` 只阻 Supplier；`unattributable_charge` 阻双方。它们不把未知消费伪装成新零维度。
3. source=approved_reconciliation 时 Supplier 永不可确认：相关维度conflicting仍优先ErrConflicting，其余返回ErrIncomplete，即使所有量声明为confirmed也不能人工确认采购；所有观察仍必须绑定原真实attempt，准入前失败直接Release不伪造观察。其余情况构造完整该侧 wire 并调用 `metering.ParseQuantities`，返回 opaque FinalQuantities。不同侧即使数值相同，digest 也保留 side。

例如 retail mask 只有 text_input，supplier mask 为 text_input+request_count：input=confirmed 100、request_count=missing 时 Retail 可确认100，Supplier 不完整；input=missing 时双方都不完整。此规则不依赖零费率：供应商独有维度价为0也仍须按已发布 mask 完整确认。known quote 不会使缺量变成0，unknown quote 不会阻止已知 supplier 量单独确认。

观察只承载 parser 已认证的**归一化收费维度**。Chat/Responses cached/reasoning 不重复加入；Messages 输入先按现行 base+cache_read+cache_creation 验证相加；Gemini output先核 candidates+thoughts/总量及纯TEXT关系；Embeddings/Rerank output明确0。外部诊断子项保留在协议解析层的受限安全数据，不引入第二套计费维度。未知收费字段无法归属时必须使用上述异常原因，不能丢弃再标最终。

纯包不维护历史、不挑“最新 observation”覆盖旧值。账务持久层沿现行更强的 `(tenant,source,source_event_id)` UNIQUE 槽去重，request/attempt是槽内不可变binding、跨root复用必须冲突，保留原件：相同内容幂等；同身份异体是证据冲突。每侧分别记录其终态引用和冲突：不同事件最终量同侧相等可核实，同侧不同量使该侧冲突；supplier 独有量冲突不覆盖已确认 retail。共享量或交付/身份事实矛盾必须按影响范围阻止双方。累计快照只替换并校验单调/包含关系，不能把快照重复相加。

客户已唯一结算或24小时释放后，迟到观察只可核采购/异常，不能改写已结算客户原价、补扣、重预占、改027分配或重发生成。采购unknown保持独立，不能随客户释放变成0；这些是后续SQL/账务验收，不由纯对象声称完成。

## 6. 所有权、canonical、摘要与错误

所有工厂在返回前同步消费并复制 caller 的 bytes/slices。Profile 所持 metering.Schema 是只读 opaque 值，复制只共享不可变私有存储；输入候选 slice、输出 canonical、mask/维度描述均不共享可写内存。`Quantities/Bounds/Rates/Schema` getter 返回纯核 opaque value；修改 canonical 返回 bytes 不影响原对象。输入 HMAC/各 digest 按数组值传递，不保存可变 byte 指针。观察/报价 raw 不得指向 Gin body 或异步复用缓冲。

本包通过共享`internal/strictjson.Parse`执行严格JSON语法，只写自己的**外壳形状**检查，不复制计量 parser：嵌套 schema/vector/rates 全部调用 metering 公共 API。外壳同样拒绝重复键、未知字段、非法UTF8/孤立surrogate、BOM、尾JSON、非对象和深度>8；整数只能规范十进制字符串，无正号/负数/前导零/指数/浮点。canonical 使用固定字段序与标准 JSON 字符串转义，无多余空白；对象键输入顺序不影响 canonical。

各 digest 使用与纯核相同 `SHA256(uint32BE(len(frame)) || frame...)` framing，不直接拼接、不让JSONB重新序列化当原字节。固定域：`moni-aigate.inference.profile.v1`、`.plan.v1`、`.quote.v1`、`.observation.v1`、`.final.v1`；第一个 frame 是完整域字节，第二个是 raw32 schema digest，其后是该对象 canonical。Observation 的 digest canonical 将 observed_at 固定为 null，除此无排除字段，因此重复同一事实的观察时间不制造异体；CanonicalJSON 仍保留原观察时间。FinalQuantities canonical 直接为 MeterQuantities，digest同时包含 schema digest和side frame再含canonical。

封闭 sentinel：`ErrTooLarge=inference_too_large`、`ErrInvalidJSON=inference_invalid_json`、`ErrInvalidValue=inference_invalid_value`、`ErrInvalidProfile=inference_invalid_profile`、`ErrInvalidPlan=inference_invalid_plan`、`ErrBindingMismatch=inference_binding_mismatch`、`ErrInvalidQuote=inference_invalid_quote`、`ErrUnknownQuote=inference_unknown_quote`、`ErrInvalidObservation=inference_invalid_observation`、`ErrIncomplete=inference_incomplete`、`ErrConflicting=inference_conflicting`。底层 metering error 保留 `errors.Is`，不输出 raw/cause/body。所有失败返回零 opaque 值；getter失败返回零值+error，禁止 partial success。

parser错误优先序：raw字节上限→严格JSON语法→外部expected/Plan/side有效性→wire完整形状/值→身份绑定→完整集合/业务联合→底层计量运算。嵌套bounds/rates各自保留metering已冻结局部优先级；先收集非binding错误再比较外壳身份，不为跨两个嵌套对象的不可区分ErrInvalidVector复制私有parser或承诺全局细分顺序。工厂先验证输入opaque/schema，再校验文本合同/候选完整性，最后构造向量。单次处理界：Profile canonical≤16KiB，最多256候选，Plan canonical≤64KiB；Quote≤16KiB，Observation≤32KiB、最多8维。超界失败不裁剪集合。没有文件路径、io.Reader、codec插件、动态公式或非受控诊断map。

## 7. 本片验收与后续接线

纯测试必须实际覆盖：六协议/空未知拒绝；input-only完整0；schema相同UUID但不同digest拒绝；Plan调用方不能单独自报较低input，未绑定原候选profile拒绝；候选max公共界、不同profile合法收紧、漏候选由账务另拒；候选/slice/raw/canonical修改隔离；零opaque失败；known零价/有理率/int64溢出；unknown两个原因与version差异；不得unknown.Amount=0；报价scope/plan/profile/side互换拒绝；两侧mask不同、supplier-only缺量/冲突、unattributable两侧阻断；实际超界保留；same facts不同观察时间digest相同；身份/量变化digest不同；固定摘要黄金值；恶意JSON/Unicode/重复键/尾JSON/规模上限。

新增共同包不是U完成。集成人随后必须统一接通provider私有已解析input/output→原profile/candidates→Plan，DB重验/不可变rate快照→R/U、逐侧证据→唯一settlement/采购、027来源、Key/PB/退款/恢复及026只读报表。真实受限APP PG与HTTP/main验收由集成人串行执行；本子不跑PG，不改SQL/operations/runtime或架构清单。没有真实供应商/支付/1Panel验收时如实未完成。

集成时同步[主规格](spec.md)§4推荐签名为本文现行U工厂（删除复制六parser的ParseJSON建议），并更新HLD/LLD、统一架构的模块/状态/源码链接；所有并行结果汇总后由根执行architecture refresh与只读检查。本文本身只冻结纯域与后续必须验证的权限边界。


2026-11-04纯片本子验收：`go test -race ./internal/inference` PASS，1.739s；包含7个独立Python SHA-256黄金值、六文本闭集、stream/input-only、公共界max与候选收紧、known/unknown/零价/溢出、分侧mask/manual、复合错误优先级、零值/深复制并发和fuzz种子。此为共享值域局部证据；根的最终组合JSONL、架构复核与提交另记，不能推导生产U或真实供应商验收。

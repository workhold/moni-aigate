# 028：统一向量计量与媒体、任务、实时协议

2026-11-04；状态：**总体设计合同草案；U共同纯域已单独冻结实施，生产DDL/六文本接线仍未完成，没有新增可售媒体能力**。本文按[ADR-005](../../docs/decisions/ADR-005-pre-mvp-single-path.md)建立一套现行计量与资金路径。当前六种外部文本协议保留其已声明的支持范围，内部统一转成向量；图片、音频、视频任务与实时轮次使用同一价格、请求、attempt、证据和结算模型。不建立 `token_v1/vector_v1` 双运行，不新增 `/v2` 目录，不为未发布的内部 DTO 保留旧字段联合或自动 fallback。

依据：[统一架构](../../docs/architecture/README.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)、[整体设计与本轮官方资料](../../docs/design/integrated-wallet-media.md)、[024纯计量核](../024-extension-platform/b0-pure-contract.md)、[026经营报表](../026-operating-reports/spec.md)、[027现行来源资金](../027-wallet-sources/spec.md)、019采购与023周期预算的现行业务合同。027的六来源核心表、四写入口及023正式迁移已实现，限定本地验收见其记录；本文只在其现行模型上接线，不引用旧C0/proof/profile作为运行前提。028导出签名和正式迁移号由集成人统一冻结，不能将未实现接口写成已实现。

本文在媒体计量/价格/目录的实施方向上取代[024-B0旧候选](../024-extension-platform/b0-metering-contract.md)的双模式、第二套目录和逐个旧内部 DTO 兼容安排；024-A告警及已实现的纯计量核保持各自事实。旧候选中的 `tts-1/alloy/wav/ASCII` 只是当时研究范围，**不是本规格冻结的可售模型、参数或供应商事实**。正式 profile 必须使用本轮实际取得的官方资料和独立真实认证。

## 1. 交付目标与非目标

经营者能发布具有明确单位和采购成本的模型；客户使用同一 Key 与钱包调用文本、同步图片/语音、异步视频和实时轮次；运营能在同一请求账单、渠道成本与来源余额中解释每笔资金。没有可信硬界或完整采购报价的媒体 profile 不可派发。

| 交付面 | 必须形成的完整纵向能力 |
| --- | --- |
| U：统一计量 | 六文本协议的请求边界、报价、预占、最终量、采购、退款、恢复和只读报表全部使用同一向量路径；纯核数学不重写 |
| S：同步媒体 | 图片生成/编辑、TTS、STT分别有封闭 profile、受限输入/结果、同一资金链路和实际客户端交付；每个端点单独记录验收 |
| A：异步视频 | 一次提交、一份请求/客户预占、一个生成 attempt；持久任务身份、只读查询/回调观察、唯一终结、受权结果读取和未知提交处理 |
| R：实时 WS | 会话不代替资金请求；每个收费轮次分别走同一 reservation/attempt/唯一 settlement，流式事件有界，断线不重放 |

不增加通用脚本/插件执行、任意价格表达式、多币种/自动汇率、视频转码平台、任意远程媒体 URL 抓取、全供应商透传或默认 HA。浏览器播放器使用必要的本地 object URL，不把上游 URL/凭据交给客户端。任务持久状态与财务事实不是模型正文存储服务。

## 2. 单一路径与领域不变量

1. 一次同步调用、一个异步生成任务、一个实时收费轮次分别对应一个 `request_records`、一个客户 reservation、至多一个最终 settlement。任务查询/下载、WS heartbeat/重连不创建第二份生成请求，也不重复计收入。
2. 文本沿当前安全重试策略最多两个生成 attempt；首批媒体/任务/实时 profile 默认一个生成 attempt。任何 `sending` 后消费未知都不自动重发；查询不到 task/response ID 不是安全重发证据。
3. 每 attempt 独立冻结供应商、模型映射、采购账户及采购报价状态。known报价必须完整并计算硬界U；媒体与受管采购不允许unknown报价。文本人工采购沿已声明的unknown-cost准入政策，允许时在同一快照中明确unknown，不造零费率或虚假U。客户金额R与已知采购U各自计算；客户释放、消费退款或24小时截止不释放采购unknown。
4. 全部实际钱沿原钱包/Key/周期预算/019采购及027来源事务。协议模块不得直接更新 available/held/spent、创建旁路 journal、按维度分别扣款或用新预占覆盖原未知请求。
5. 价格、schema、profile、variant、原界和证据身份不可变。重读当前目录仅用于新准入/发送前复验，不能重算已受理请求原价或退款来源。
6. 新协议显式穷举派发。未知 protocol、execution kind、result kind 一律配置错误；不得落入 Chat endpoint、Messages `max_tokens` 注入或通用 JSON decoder。
7. 不可信客户 quantity/金额/quote 不授调用权。只有受限 parser 根据真实输入及已发布 profile 产生 BoundPlan，账务再按当前不可变合同和原请求身份重验并自行算 R/U。
8. 数量、钱、业务成功、交付成功和供应商消费是不同事实。HTTP 2xx、完整文件、取消按钮成功、TCP关闭都不能独自证明最终量或0消费。

逻辑链路为：`严格解析 → profile/variant与双方硬界 → 运行资源许可 → Reserve+027来源 → RecordAttempt+采购U → PrepareDispatch → 单次生成 → 受信观察 → 唯一客户finish+027来源收尾/采购独立收尾 → 结果交付或状态查询`。网络 I/O 不持业务事务。

## 3. 计量 schema、费率与文本归一化

直接使用 `internal/metering` 的 `Schema/Bounds/Quantities/Rates`、`Amount/ReservationAmount/CompareExactCost/WithinBounds` 及域分离摘要。保留最多8个收费维度、两侧 mask、规范整数/有理率和末尾单次 ceil；本文不另写浮点金额算法。纯核负责数值与绑定，不证明来源真实性、供应商强制界或 actor 权限。

现有 registry 是 `text_input_token`、`text_output_token`、`text_input_unicode_scalar`、`text_input_utf8_byte`、`image_output_count`、`audio_input_millisecond`、`audio_output_millisecond`、`request_count`。单位与[纯核合同](../024-extension-platform/b0-pure-contract.md)一致。供应商收费包含 image/audio token、视频秒/帧等未覆盖单位时，先扩闭合 registry、精确单位/计数语义及纯测试，再发布新 schema；不得冒用 text token、request_count 或图片张数。最多8维是**单schema收费维度上限**，不是整个registry永久只能8个名字；不能以“已有8维”宣称所有视频/实时模型已能计费。

按本轮官方profile需要，registry的下一组候选精确命名为 `image_input_token/image_output_token`（token）、`audio_input_token/audio_output_token`（token）、`video_output_millisecond`（millisecond）。这些是待D0冻结的代码单位，不是已实现能力。视频若按帧、生成次数或带音轨/缓存另收费，必须另冻真实维度，不能把它换算成不存在的毫秒消费；官方只给秒时，须认证其精度及秒→毫秒确切映射。总Token仅作已声明包含关系校验，不与text/image/audio子项再次相加收费。任一profile超过8个独立收费维度时先审纯核上限及资源，不静默合并量。

每 schema 为不可变 canonical 定义和摘要，固定非空 retail/supplier mask；两个 mask 可不同，但同名维度的计数含义与单位必须相同。size/quality/voice/codec/resolution 等有限参数组合归 `variant_key`，不是可相加数量；任意组合无价时拒绝，不使用最接近 variant。schema/profile不允许客户端自建公式或自由维度名。

每条 rate 精确为 `{dimension,unit,numerator,denominator}`，数字均规范十进制字符串，分母1..1,000,000且最简分数，零率为0/1。两侧报价各有完整 mask 和明确 CNY；缺价/NULL/basis unknown不等于零价。报价时冻结 `schema_digest + side + variant + bounds + rates + price/cost_version`。有理单价上限以同单位交叉乘积比较，成本优先策略在同最高 priority 层比较完整最坏采购精确费用，再沿原 weighted tie 选择。

六文本协议在边界直接构造同一当前 schema 的量：

| 协议 | 归一化与保持的外部含义 |
| --- | --- |
| Chat / Responses | input/output为各 profile已认证的包含关系；cached/reasoning为已包含子集，不重复收费；最终量完整性规则保留 |
| Anthropic Messages | base+cache_read+cache_creation 继续按当前已支持合同合成统一输入量；外部原生 usage 名称不改 |
| Gemini | 原 text-only prompt/generated/thought 包含关系保持；媒体 modality 不在文本 profile 中自动放行 |
| Embeddings / Rerank | input为整批总量；output维度明确0界/0价/0量，保持纯核现有两维 schema 的完整集合，无隐式缺项 |

文本原有输入/输出微元每百万 Token 定价在新的现行模型中直接表示为同一有理 Rates；新发布只接受统一费率。可以使用已有 `LegacyTokenRates/Bounds/Quantities` 作为受限协议/初始化数据的精确构造工具，但不保留旧 reserve/settle、旧SQL金额分支或历史DTO读取器。文本等价验收对照独立旧公式验证金额相等，不需要新旧两套在线路径，也不承诺旧库历史转换；部署边界与027的新库要求一致。

所有收费维度必须有独立可信全局界及本次界；输入不能用 context-output 推导，音频不能用字节估算时长，图片不能以一张代表任意尺寸，视频不能用请求网络持续时间计费。发布前验证全部合法请求最大 R/U 均在 int64；实际量保留超过界的真实已知值和大整数原费用，客户最多 R，采购实际可超 U 并标异常/暂停。

## 4. 共同 Go 类型与包责任

共同纯领域包冻结为 `internal/inference`，最小签名、canonical、错误和信任边界以 [U共同域合同](u-domain-contract.md)为准。它仅依赖 `metering` 和共享的 `strictjson` 语法层，无DB/网络/环境/时钟/凭据。provider继续独占六文本严格请求/响应解析，不在共同包增加第二份 `ParseJSON`。已解析的原profile、input HMAC、output和stream进入受限工厂；billing负责重读受权配置、完整候选闭包和资金事务。

```go
type Profile struct { /* private immutable validated definition */ }
type BoundPlan struct { /* private schema/variant/input binding + both Bounds */ }
type QuoteSnapshot struct { /* private known/unknown union and original quote */ }
type Observation struct { /* private identity + dimension states + safe metadata */ }
type FinalQuantities struct { /* private complete confirmed side-bound Quantities */ }

func NewTextProfile(TextProfileInput) (Profile, error)
func NewTextPlan(TextPlanInput) (BoundPlan, error)
func (p BoundPlan) ForAttempt(Profile) (BoundPlan, error)
func ParseQuoteSnapshot(raw []byte, plan BoundPlan, side metering.Side) (QuoteSnapshot, error)
func ParseObservation(raw []byte, expected ObservationBinding) (Observation, error)
func (o Observation) Confirmed(side metering.Side) (FinalQuantities, error)
func (q FinalQuantities) Quantities() (metering.Quantities, error)
```

U只发布六文本、同步json/events与default variant；retrieval不允许stream。普通路由当前所有binding引用唯一model capability，route reader按profile digest去重，再由billing重建核对；纯域候选max/attempt收紧算法不表示已发布异构profile路由。后续媒体的multipart、codec、ResourceLimits和任务/会话工厂随S/A/R精确合同追加，U不安装无writer的任务表，不开放占位端点。所有opaque零值拒绝，返回bytes/slices复制，不存在 `Trusted:true`、可写amount或setter。

`ParseQuoteSnapshot` 仅由受权DB reader从已选原版本和完整rate集合构造，校验version、side、schema、variant、bounds与rate绑定；它不按raw自报R/U授准入。`ParseObservation` 仅供受信适配器/任务观察服务构造规范事实，结构校验不证明供应商真实性；客户HTTP没有直接提交该结构的入口。上述parser失败返回零opaque对象和受控error，不返回部分合法向量。价格快照和观察wire大小分别≤16KiB/32KiB，raw中不含模型正文/二进制。

U的共同Profile绑定完整不可变provider合同摘要、模型/版本、schema、default variant、协议和强制界。工具、参数、usage语义、无消费资格等完整provider合同仍在唯一协议配置中，由发布端及billing核摘要；共同包不重新实现该parser。未来媒体认证身份和资源限制在对应profile合同补齐。空协议只可在模型创建入口明确选Chat，此后空/未知枚举一律拒绝。

U的Observation精确wire以[共同域合同](u-domain-contract.md)§5为准：包含原tenant/request/attempt/profile/schema/variant/plan绑定、安全来源和终态/交付/finish_reason、完整维度状态及nullable quantity。没有自由diagnostics map；cached/reasoning和Messages cache拆分由provider严格验证及原生wire保留，不作为重复收费维度。观察时间是元数据，资金/周期时间取DB；必需量缺失为null，不能填0。无法归属的额外收费明确阻止相应侧确认。所有Observation绑定原attempt；无attempt准入失败直接Release，人工approved_reconciliation也绑定原attempt且永不能确认supplier量。相同来源事实幂等、异体进入分侧冲突，不覆盖原证据；累计量替换并校验单调/包含关系，不重复相加。

retail/supplier各自只在其完整 mask 最终确认时得到 `FinalQuantities`。供应商独有维度缺量可令采购unknown、客户仍按已确认自身完整向量结算；若未知费用无法安全归属或改变共用量，则两侧均不确认。这是明确的分侧规则，替代024旧候选笼统“任一未知两侧整体不确定”的安排。供应商价格basis unknown与供应商实际量missing分别记录；首批媒体派发前仍要求完整已知采购价。

共同账务入口以原接口语义统一替换，而非新增第二个 media Store：

```go
type ReserveInput struct {
    Principal Principal
    RequestID, ModelID, IdempotencyKey, RequestHMAC string
    ProfileVersion string
    RouteRevision int64
    Plan inference.BoundPlan
    RequestDeadline time.Time
}
type AttemptInput struct {
    TenantID, RequestID, ChannelID, ChannelVersionID, CredentialID, UpstreamModel string
    Sequence int
    SupplierQuote inference.QuoteSnapshot
    RouteDecisions []routing.Decision
}
type FinalizationInput struct {
    ID, TenantID, RequestID, AttemptID string
    Observation inference.Observation
}
```

账务从DB重读已发布profile、原schema/variant、完整界和当前价，验证 Plan绑定；自己计算 R/U，不接受调用方金额。`RequestHMAC`绑定协议、执行种类、公开模型、严格有效输入/媒体摘要和参数；不保存正文。SQL与Go均检查 request/attempt的租户和profile/schema/variant相等。Reserve/RecordAttempt返回只在确认COMMIT后可用于下一步；未知提交返回原身份可查询信息，不发行派发许可。

## 5. 单一数据库模型、迁移和权限

正式SQL由集成人独占。只追加新迁移，不改已应用001–当前SQL；027使用023，028的后续迁移号在汇合时分配。迁移一次替换现行列/函数/约束/权限，运行时不探测旧schema。与027相同，本次只支持独立全新当前数据库；发现既有资金/请求/结算历史时明确拒绝，事务回滚、原件与余额不变，不提供本次未设计的停服旧史转换器。已有开发库保留，新建独立数据库验收，不能自动删库、双写或用假零数据补齐。

| 当前关系/新关系 | 统一合同 |
| --- | --- |
| `meter_schemas` | id、固定registry版本、canonical schema及digest，name/revision唯一；不可变；与纯核相同集合/单位/上界约束 |
| `model_capability_versions` | 当前profile/schema、finite variant与两侧界、资源/结果/usage合同、供应商认证reference；新版本完全不可变 |
| `price_versions`、`price_rate_lines` | 当前model/group/schema/variant/effective_at/CNY版本及完整retail mask费率；沿原默认/组域与生效排序，无组价不回落默认 |
| `cost_versions`、`cost_rate_lines` | channel/upstream_model/供应商合同/schema/variant/basis/effective_at及完整supplier rates；known必须完整，unknown无伪0率 |
| `request_records`、`request_meter_snapshots` | 原身份/权限/期限/状态保留；执行种类、profile/schema/variant、原零售价/定价时刻/组归属、两侧原界、规范指纹；一request恰一份快照 |
| `upstream_attempts`、`attempt_meter_snapshots` | 原channel/version/credential/model/sequence；该attempt完整采购报价、两侧兼容界及摘要；每attempt恰一份；采购账户与U仍原019路径 |
| `metering_events`、`meter_event_quantities` | 原source身份唯一；当前schema及每维status/nullable quantity、安全原因、canonical指纹；零量与missing区分 |
| `finalization_records`、`settlements` | 原request唯一最终结算；引用受审event；raw/charged/waived和原价/向量可重算；不增加媒体结算表 |
| `media_tasks`、`media_task_observations` | 任务所有权、原request/attempt唯一链接、供应商task reference、状态/revision、原期限/结果期限/只读观察；无独立钱包余额 |
| `realtime_sessions`、`realtime_turns` | 会话身份/租户/Key/限制/状态；轮次ordinal、request唯一、原attempt/供应商response reference、终态；session不存可绕账的金额 |

费率及量子表使用精确维度集合/单位；提交前验证root↔child双向完整、snapshot↔原不可变版本一致、跨tenant引用带tenant。rate分母、规范整数、bounds全局界、引用schema摘要不能只靠浏览器校验。current `input_rate/output_rate/input_upper_bound/output_limit` 等 Token专用持久列在新库追加迁移中撤下运行合同；不保留nullable旧列作为第二种计费身份。source/trigger/pricing函数的依赖必须在同一迁移明确重建，不能只改Go使原SQL沿Token假设放行。

迁移方案应附完整DDL/trigger/SQL/角色清单与实际PG原型，再冻结U片。schema/费率/快照/原始量与身份不可任意UPDATE；受限领域入口派生预算/余额；projector只读安全outbox，不获schema/报价/媒体量写权或正文读权；payment worker不获媒体派发权。任务观察worker只获已授权任务的查询与观察提交权限，不能新Reserve或发送模型生成。media发送使用原APP与明确准入，启动权限检查只针对当前正式模型，不引入被冻结的历史proof候选。

## 6. 双边金额、027来源与事务次序

`R=ReservationAmount(retailBounds, originalRetailRates)`；`U=ReservationAmount(supplierBounds, originalSupplierRates)`。确认量分别计算 raw retail与supplier actual；客户 `charge≤R`，已知真实采购成本不截为U。异常量越界必须保留、客户差额waived、隔离相关profile/binding并呈现成本异常，不能追加扣客户钱包。

027通过 `funding_lots/funding_lot_balances/funding_lot_events/funding_reservation_lots/funding_settlement_lots/funding_consumption_refund_lots` 六核心表及 `wallet_openings` 原件提供来源事实。ApplyFundingTx、Reserve、finish、ApproveRefund四写入口同原pgx.Tx、先全部账户ID序再lot_id序锁，实际接口名由资金集成人统一落地；028不得引用旧纯planner或离线proof作为已生效来源合同：

- Reserve：按027的当前source/lot锁序与可用性分配恰R，来源held总和与客户HELD、reservation及Key/PB事实同事务；失败全回滚。
- Finish：沿原分配把R拆成charged和released，所有source桶、journal、settlement、Key/PB、audit/outbox同事务；重复终结返回原结果，不二次扣减。
- 消费退款：绑定原settlement和已charged来源分配，累计不超过charged；原来源去向与027一致，不把非现金权益伪造为现金或用当期价格换算Token。
- 24小时客户截止：沿原来源释放未决R；随后确认的供应商量只能更新采购/证据/任务观察，不重新收费或把已终结source重新held。
- 零价：有真实request/reservation/settlement和027的空分配语义，不建立零本金lot或虚构零额分配边；不能绕过权限、预算、证据或终结唯一性。

来源资格沿027：manual_credit/voucher_grant/wallet_opening为nonrefundable；sandbox_capture仅sandbox_original，不能推断真实PSP退款资格。wallet_opening是停止服务的新钱包显式非现金开账，媒体/实时入口不能创建或伪装opening。消费退款只回原来源桶，不把不可原路退的本金变成可提现现金。

锁序保持原领域前缀：tenant/member/user/Key/model/可选price_group → 客户周期controls → 钱包/027全部来源锁 → request+snapshots及客户事实。逐attempt沿原channel/credential/request/reservation锁序 → 采购account/采购controls → 原quote完整复验 → attempt/采购reservation。新增不可变schema/价格读取不能在资金等待后再取前层业务写锁。

所有新来源/快照必要等待必须先于当前request或采购reservation的实际INSERT及PB source时刻；锁后重验Key/请求期限、来源期限、报价与模型/渠道资格。async的poll/回调不重新选择周期或价；实时每轮在该轮新Reserve时取得自己的DB时刻/源/价格，不能沿session开始时价无限使用。

写事务沿当前RC与3秒锁/5秒语句/10秒总预算取caller较短；仅确定rollback的40P01/40001可在原身份/预算内有限重试。未知COMMIT销毁连接，只观察原operation/request，不自动派发、另发request或补做业务效果。

## 7. 客户、采购与交付状态矩阵

客户唯一终结、采购状态和协议状态分开存。同步/WS的媒体不重新定义一套收费政策；异步结果保留场景明确新增成功语义：

| 事实 | 客户处理 | 采购处理/交付 |
| --- | --- | --- |
| 派发前确定失败/not_sent | 释放R，沿027返原来源 | 释放U；无结果，无新attempt |
| 正式profile确认无消费拒绝 | 按原无交付失败释放R | 释放U；无消费判据须该供应商认证 |
| 同步完整有效结果、retail完整确认 | 以原价唯一结算，≤R | supplier完整确认则原价结成本，否则unknown；客户结算确认后才写body |
| 同步完整结果但retail缺量/冲突 | 未过原截止时保持待核对 | 无未结算成功body；采购独立unknown/disputed；原buffer释放，不重新生成 |
| 同步非取消失败且未交付内容 | 沿当前 `released_no_delivery` 政策释放R | 已sending不证明0，采购仍unknown；损坏二进制也在此行 |
| 客户取消/WS断开，已有完整可信retail终态 | 按当前取消政策可结算原生成，不因本地未写完自动退款 | 采购按实际量/unknown；关闭body/连接并join |
| 取消/部分流输出但无可信retail终态 | pending至原证据/24小时截止 | unknown保留U，不以累计估算/上界代扣 |
| 异步成功且有有效结果reference、retail终态可信 | 结果可取且未过原客户截止时结算一次；下载不是第二次收费 | supplier独立；若制品不可用不伪造成功交付 |
| 原客户24小时截止到达 | 唯一released_deadline，027原来源收尾 | 任务可只读观察原结果/成本；采购unknown不清零，客户永不补扣 |
| 已结算后本地写失败/结果过期 | 保留原settlement，按明确售后消费退款流程处理 | 记录delivery/expiry事实；不自动生成替代结果 |

异步queued/running没有下游已交付内容，但不是同步“失败无交付”，不能套 `released_no_delivery`提前释放；其成功/pending由任务终态合同驱动同一个finish。写代码前将 `settleRequest` 的执行种类与上述矩阵显式穷举，避免新增异步状态掉进同步默认分支。采购known/unknown/disputed及客户金额不能从task显示文案猜测。

## 8. 同步图片和音频合同

对外路由预定 `POST /v1/images/generations`、`/v1/images/edits`、`/v1/audio/speech`、`/v1/audio/transcriptions`；只声明通过各自profile认证的受限子集，不因路径相同承诺整个上游SDK。模型同时列出协议/参数限制，不能在普通Chat的content数组里自动放行媒体。具体模型/版本、输入字段、格式和usage随官方资料另冻，本文不填未经核实的供应商默认值。

| 能力 | 严格输入/结果与计量边界 |
| --- | --- |
| 图片生成 | JSON精确字段与finite size/quality/format/n；首profile建议n=1。仅inline结果；验证结果项数、base64规范/长度、解码大小、固定图片格式及尺寸；缺图/多图/未知usage不得当作已确认张数 |
| 图片编辑 | 受限multipart，限定图片/遮罩数量和MIME；独立验证文件signature/尺寸/资源，固定遮罩语义及几何关系；拒路径/远程URL、重复part、未知filename元数据；不使用会静默写磁盘的multipart convenience API |
| TTS | 精确文本与固定参数；按该供应商实际收费单位计数，明确定义Unicode/空格/换行/规范化，不trim重写后假称原输入；二进制结果完整校验，格式与音色有限；不能从2xx推导实际消费 |
| STT | 受限上传、固定支持codec/container；持续时间来自已验证frames/sample_rate或正式解码器的受限完整结果；非整数ms的上界/actual规则随profile冻结。音频文件header时长、HTTP长度和采样率声明均非单独证据；仅返回profile固定文本/JSON结果 |

通用JSON校验拒绝无效UTF8、孤立surrogate、BOM、重复/未知member、尾JSON、超深嵌套及不合法数值。不能仅使用原 `provider.object` 加 `utf8.Valid(raw)`做Unicode收费输入的精确解释。multipart先校验Content-Type/boundary与资源许可，再流式有界读取到受控内存，拒额外parts和field/file同名歧义；上游multipart由网关重建，不透传客户boundary/headers。

图片/音频结果验证器不能任意解压/转码或执行元数据。校验所需codec必须有固定依赖、内存/CPU/尺寸/时长界与恶意样本测试；只认magic bytes不等于合法完整制品。首片选择能完整受界验证的格式，不能用“先支持全部格式”扩大安全及计费面。

## 9. 异步视频：一次提交与只读观察

内部Task为网关资源，所有公开ID都是网关task ID，供应商task ID/凭据/完整URL不公开。对外拟 `POST /v1/videos`、`GET /v1/videos/:id`、`GET /v1/videos/:id/content`、`POST /v1/videos/:id/cancel`；这是一份待具体profile校准的网关合同，尚不声称与某供应商当前全部字段一致。

创建先严格派生最大时长/分辨率/音轨/帧率variant及完整收费硬界，取得资源许可，在同一Reserve事务创建request+task，再创建一个原attempt/U，只有PrepareDispatch确认后提交一次上游生成。请求至少具有 `Idempotency-Key`；同Key同输入返回原task/请求状态，同Key异体409，不再次提交。DB未知与供应商提交未知分别记录，不能从HTTP超时或task ID为空推断未受理。

任务状态：

```text
prepared -> submitting -> queued -> running -> succeeded
                       \-> submission_unknown
queued/running -> cancel_requested -> cancelled | running | succeeded | failed
queued/running -> failed
任一非终态 -> observation_blocked | observation_expired（只停止本地观察，不证明上游停止）
succeeded -> artifact_expired（保留原成功/计费事实）
```

协议状态、观察新鲜度、取消命令状态、财务状态分别列字段；`submission_unknown/observation_blocked`不能显示为“失败且未扣费”。取消不改原任务身份，重复取消返回同一命令状态；只在供应商明确幂等的取消协议下有限重查/重送取消，不重送生成。`cancel_requested`不释放U，供应商最终cancelled也需对应usage/无消费事实，不能凭文字状态当0。

worker只能：按持久task reference查询；在正式供应商支持且已冻结的商户幂等身份下**只读寻找原提交**；接收经签名认证/去重的回调；读取原制品。供应商既无task ID又无原提交查询时，保持submission_unknown并转人工核对。成功恢复不补发POST生成。轮询/回调乱序按单调状态与事件身份处理，终态冲突进核对，不能覆盖已确认量；回调不是actor授权凭据，接收器固定scope/profile/签名窗口和payload上限。

任务轮询使用独立有界worker/pool，单次查询期限≤10秒、最多1440次/原task、退避5秒至60秒；取消/Guard失权/Recovery停止新外部动作。首次profile只接受无独立收费的查询/取消/下载合同；若供应商这些操作收费，必须先纳入原完整费用界/事件，不把其费用隐为0。lease过期仅允许另一worker接管只读观察，不能授予生成权限。

制品读取首片只接支持固定、受认证只读task/content API的供应商；URL路径由固定base+验证reference构造。任意回调URL不进入通用下载器，不跟redirect。若供应商只给签名URL，须另冻host/path/TTL/秘密存储/SSRF合同后才能启用，不能临时通过任意HTTP GET补齐。

成功前在有界内存完整读取/验证制品和最终量，持久安全result reference/类型/长度/摘要/expiry及Finalization后同路径结算；不存原媒体或prompt到业务表。制品查询为受权只读下载，校验同一reference/摘要并有资源许可，可重新读取**原制品**，不生成新制品；缺失/过期410，格式或摘要冲突503并记观察。是否支持Range由profile明确，首片不做Range且不能伪装断点续传。GET查询不触发即时poll；只返回DB最后观察时刻/新鲜度，不把空结果显示健康。

## 10. 实时 WS：按轮预占与唯一终结

对外拟 `GET /v1/realtime` Upgrade；协议/provider/model/auth与可接受event子集须经官方资料冻结。标准服务客户端沿原API Key鉴权，禁止把长期Key放URL。控制台需要浏览器WS时，使用同源CSRF保护的短时单用ticket交换，绑定当前session/tenant/Key或相应权限、Origin和目标profile，连接时再次检查当前权限；ticket不是长期推理Key，不能跨租户/Origin重用。具体ticket路由由HTTP集成人冻结，不靠浏览器隐藏按钮授权。

一个会话绑定一个tenant、actor/Key、逻辑model/profile/route；连接后不允许客户端切换model/供应商或扩大界。每session最多一个inflight收费轮次，不支持交错并发生成；后续轮次重新Reserve/取当前价/采购准入。session仅会话与资源身份，没有独立可花余额。

```text
session: opening -> open -> draining -> closed
turn: idle -> reserved -> sending -> receiving -> finalizing -> settled | released | pending
```

首批profile必须能关闭上游自动生成/VAD自动response，所有收费事件由网关控制。第一帧可能产生费用的输入（包括音频append而非仅response.create）**之前**，为该轮全局最坏量创建原request/R与attempt/U并完成PrepareDispatch；未取得许可的音频/文本绝不先行转发。客户端本地输入预缓冲受界，超界在转发前拒绝；已转发部分不因后续拒绝而被当作not_sent。

每轮采用一次完整保守预占，覆盖上下文历史、输入音频、隐藏推理及全部可能收费输出；不边输出边冒险追扣、不隐式续杯。若供应商有持续idle/cache/session连接费，或无法将全部消费归于受界轮次，则该profile不能按本首片上线，必须先扩会话收费合同。上下文是整轮供应商计费输入，不能只按新增message或音频估算。到达轮次硬界/期限时停止接受/转发并申请取消，不以连接关闭证明供应商无消费。

同一客户端event ID与内容重复只关联原turn；同ID异体冲突，不创建第二请求。provider response ID绑定原turn；已开始的turn不能重绑定别的response。允许幂等heartbeat/status查询，禁止重连时重发input commit/response.create来找回输出；首片会话断开后不恢复生成连接，新连接是新session，原turn仅查询原账单状态。

文本/音频内容增量可在原hold覆盖下交付，最终success/done须在可信retail终态已持久并唯一结算后发布；终结失败发受控billing_pending/error并关闭/收尾，不发送成功终态。重复最终usage/event由原source身份去重；混合modalities不能只留文本Token丢掉音频费用。客户端断线立即取消/关闭上游并join；已取得的可信终态可按原矩阵结算，缺失仍pending/采购unknown，不后台续生成追usage。

原session的关闭/重新连接不释放已结算轮次或重置Key spent/PB；未决轮次沿原24小时截止与027来源处理。平台撤销/Guard失权在下一收费轮次派发前重验；已sending轮次尽力取消并明确在途边界，不承诺撤销瞬间供应商停止。

## 11. 有界资源、隐私和进程恢复

下表为实现初始全局天花板，具体profile必须更严或相等；扩大天花板须专项容量与恶意输入验证。正式模型不满足时调整profile范围，不能默默扩大共享池。

| 资源 | 初始边界 |
| --- | --- |
| 六文本JSON | 保持1MiB请求、8MiB非流结果、1MiB事件现行限制 |
| 媒体元数据JSON | ≤64KiB；上传≤16MiB/请求，parts≤8；multipart总量另含≤64KiB字段/头预算 |
| 同步图片/音频结果 | wire≤32MiB，解码后≤16MiB；base64与解码缓冲均入预算，不只算其中之一 |
| 视频制品 | ≤64MiB；完整读取前预留额度；不做无限内存/磁盘临时文件兜底 |
| 同步媒体buffer | 全局4、每Key1；所有媒体活动内存预算≤256MiB，需同时取得槽和加权字节许可 |
| 视频artifact buffer | 全局2，纳入同一256MiB总预算；查询worker不持制品buffer等待DB |
| WS | 全局32、每Key1；frame≤1MiB，单方向队列≤32事件且≤4MiB；session累计转发≤64MiB，最多100轮，session≤30分钟 |
| WS每轮 | ≤120秒；音频/字节/输出最大值取profile已认证界与本表较小者；达到任一界停止新收费转发 |
| 期限 | 文本维持现行≤300秒；同步媒体≤120秒；异步submit≤120秒，持续观察不延长原24小时客户截止；header/body-idle按profile明确且有总deadline |

媒体许可先于大body读取和Reserve，失败不生资金；DB队列/网络等待不持没必要的已解码大对象。资源释放路径覆盖成功、解析失败、caller取消、worker停止、Guard失权、panic、SIGTERM。Close/cancel与goroutine join之后释放一次；不能把Gin Context送后台或无界goroutine消费socket。

上游保持SSRF实际IP验证、禁止环境proxy/redirect、受控TLS/headers及无隐式重放。媒体 Content-Type不是信任证明；协议encoder固定生成JSON/multipart/WS，不接受任意下游header/路径拼接。上游错误仅净化成code/class/consumption/Retry-After，正文、URL和凭据不进入响应或日志。

prompt、图像、音频、视频、base64、WS内容、长期Key/ticket不进DB/audit/outbox/trace/localStorage。可以持久task/response安全reference、规范量/率、内容HMAC或受控制品摘要、字节/时长诊断及expiry；不得用自由evidence_ref偷存URL/body。初始实现无媒体持久磁盘缓存；容器内存/反代body/WS超时需与上述限制配套配置；1Panel 等面板部署为可选方式，另验。

主进程启动/恢复只能收尾原已提交财务事实与只读查询原task/制品，不能自动创建新的生成attempt。SIGKILL后留prepared可确定not_sent按原规则释放；已提交sending保守unknown，除非有原终态证据。in-memory WS/session消失不证明0费用；数据库turn身份仍可核查。生产部署仍单活，读worker lease不是HA生成许可。

## 12. 同一目录、账单、管理与只读报表

继续使用现行 `/api/public/models`、`/api/models`、`/api/prices`、`/api/requests`、平台渠道/成本与026报表路径，**直接升级当前未发布内部DTO**。删除被替代的Token专用表单/类型和Go读取字段，不再复制一套 offerings/v2 或 meter-requests。外部 `/v1/models` 保持标准模型列表和原权限交集，不带采购/tenant秘密。

统一模型/报价视图包含：`protocol,execution_kind,profile_version,schema_id,variant_key,result_kind,limits,billable_dimensions,price{status,version_id,currency,rates,effective_at,as_of,source}`。公开目录只显式public_visible+enabled且可发布模型、只默认零售价；私有目录按本tenant实际组域，组缺价status unavailable、rates=[]，明确零价available。前端按单位字典展示Token/字符/图片/毫秒/后续正式维度，不把media显示为0输入/输出Token。

统一请求明细包含：原价/界、逐维最终状态/nullable量、reserved/charged/raw/waived/refunded、settlement身份/状态、execution种类；async补task状态/新鲜度/expiry，实时补session/ordinal。客户只读自身scope；普通成员只能自己请求，owner/billing_viewer按现行tenant规则。采购原价/账户/成本、凭据身份与上游URL仅现行平台授权视图可见。

价格/成本发布仍不可变版本、operation身份和当前角色/CAS/再认证；统一rate body取代两scalar字段。schema/variant必须来自代码已支持与受审发布合同，不开放任意schema执行。模型展示/假设量quote不是实际调用授权；实际请求重新解析和取价。

[026只读报表](../../internal/reporting/read.go)继续一次RR READ ONLY、5秒预算、31天/5000请求边界。按request创建窗口选队列：async按原提交request，实时按每轮request；任务poll/下载、session本身不计第二笔收入/attempt。每request只计唯一settlement，成本计全部生成attempt；未知采购成本不作0，有未结算/未知成本则gross_margin=null。异步后到结算/成本可改变同cohort未来as_of，不改窗口归属。

报表读取统一当期字段，移除固定Token列依赖；增加 protocol/execution_kind筛选和请求下钻标签时同步CSV精确列合同与公式转义。数量汇总若提供，按 `(schema_digest,dimension,unit)`分别聚合，任一缺量保留unknown计数/nullable总量，不将字符、Token、图片、时长相加。渠道仍只归集采购成本，不重复分摊客户收入；消费退款与027原来源退款均不冒充现金退款或总平台净利润。reporting不写资金、不调用provider、不触发taskpoll，不扩成第二结算器。

## 13. 错误与外部合同取证

新错误使用固定code，禁止反射provider body/SQL：400 invalid_request/unsupported_parameter/invalid_media；402 insufficient_balance；403 model_forbidden；409 idempotency_conflict/profile_changed/quote_changed；413 media_too_large；415 unsupported_media_type；422 media_limit_exceeded；429 resource_limited/rate_limited；503 price_unavailable/cost_unknown/billing_pending/task_submission_unknown/task_observation_unavailable；504 request_timeout。已发送后的错误必须附原request/task安全身份与真实可查询状态，不能返回“请自动重试生成”。WS只在未Upgrade前用HTTP错误，Upgrade后走固定error event和close code，不混入JSON HTTP响应。

每供应商/profile的精确补充必须记：本轮实际访问的官方URL、日期和内容摘要；模型/API版本与账号资格；鉴权、请求/参数/结果/事件；全部收费单位、最大强制界、usage/缓存/推理包含关系；错误/取消/幂等/查询/制品TTL；来源费率与安全reference。网页标价外币不自动换成CNY采购价；以登记合同为准。

本轮已实际取得的七份官方正文及日期见[整体设计§11.2](../../docs/design/integrated-wallet-media.md#112-本次实际读取的官方资料)：其中旧TTS型号进入弃用期，Sora/Videos API已停用，旧 `gpt-image-1` 已停用，旧STT/翻译端点有下线计划；Realtime文本/音频/图片/缓存、会话历史及其它voice API计费也不能混为同一profile。这些结论要求选择当前真实可用的供应商/型号，不把旧教程后文作为可售认证。异步视频领域仍需实现，但不能据此默认配置OpenAI视频渠道。

官方资料读取与真实凭据/用量账单认证是两道证据。本轮资料已汇合不等于某个型号的全部硬界、CNY采购合同或真实扣费通过；模型可用性和规则在实际发布前再次复核。本地fixture可支持开发/故障验证，但UI/README必须区分“协议代码已实现”“限定本地验收”“具体供应商已认证”“生产部署已验收”。未获实际供应商完整合同的media profile保持不可发布/不可派发，不能因parser完成自动出现在可售目录。

## 14. 分层验收与完成定义

| 层 | 必须验证的实际行为 | 不能推导的结论 |
| --- | --- | --- |
| 数值/共同类型 | 沿用纯核向量/摘要黄金值；新增registry有独立oracle；六文本费率/量转向量与旧公式精确相等；missing≠0、schema/side绑定、immutable、严格Unicode、未知protocol拒绝 | 不证明供应商量/硬界真实 |
| 协议/资源 | 每端点严格字段、multipart/base64/codec/WS恶意样本、超界/慢body/取消/关闭join、无fallthrough/redirect/隐式重放，内存槽与字节预算 | 不证明资金已提交或真实模型收费 |
| 真实PG商业 | APP受限权限、R/U完整快照、027分配/finish/refund/零价、Key/PB、跨tenant负例、定时价/CAS、超界、唯一结算、并发争用、锁/statement/COMMIT未知 | 跳过DB不能标商业完成 |
| 同步纵向 | 图片/TTS/STT逐一HTTP真实服务fixture→结果验证→证据→结算→单位账单；retail必需量缺失不交付，supplier独有量缺失保留采购unknown但不阻已知客户结算，写失败无重生成 | 一个端点不代表其它媒体已完成 |
| 异步纵向 | 提交前/后kill、已受理无taskID、只读恢复、重复/乱序/伪签名回调、cancel与成功竞态、24h客户释放/采购保留、制品过期/摘要冲突、同Key不再次POST | 轮询成功不证明取消不收费 |
| 实时纵向 | 首收费帧前原hold、同session多轮/重复event、混合usage、下一轮失权、边界/背压/断连、终态待结算、进程消失unknown；上游生成计数精确 | SSE测试不证明WS或真实SDK兼容 |
| 只读/前端 | 当前目录与请求统一单位、角色与采购隔离、task新鲜度/未知状态、WS轮次、026收入一次/全部成本、null毛利、CSV与大整数、桌面/窄屏真实浏览器 | UI成功不证明资金/供应商正确 |
| 真实供应商 | 隔离最低额度凭据，实际成功/拒绝/取消/断线/usage账单与合同上界交叉认证，安全reference留存；逐profile记录 | 本地fixture不能代替 |
| 交付/运行 | 单实例真实main故障、完整当前DB/权限/027验收、构建/check、部署资源配置、README/HLD/LLD/统一架构同期更新，集成后只读架构检查 | 单机通过不代表HA或面板/生产环境已验收 |

测试至少覆盖：任一账务副作用失败整体回滚；同source同量去重/异量冲突；两侧mask不同与supplier缺量；客户24h后供应商终态仅更成本；未知提交后原Key重查无新生成；文本两attempt收入一次/成本两次；媒体一个attempt；taskpoll/WS heartbeat不生attempt；价格/来源等待跨期不重定原时间；所有后台恢复路径上游生成调用计数=0。

没有真实凭据也继续完成共同模块、受限PG、HTTP/worker/WS/main与UI的限定本地交付；供应商认证状态如实未完成，不以此暂停不依赖凭据的工作。性能在上述能力稳定后执行现行TTFB/TTFC与供应商等待分离的对照，文件下载首字节不冒充首Token；不以性能优化删减硬预占、证据或资源界。

## 15. 实施顺序与可并行文件所有权

### D0复核与下一片出口

027集成后，对现行六文本写链与026只读报表进行交叉审查，实施前必须把以下合同具体化。它们是028尚未完成的工作，不能以本总体稿替代DDL冻结或供应商认证：

1. 正式DDL需给出精确列型、键、tenant复合FK、root/child双向完整约束、canonical摘要及各角色权限；同片处理013成本路由、014采购、018定价操作指纹、020周期预算、023来源的依赖。既有标量价格配置也属于迁移前置检查，不能仅因无资金历史便静默丢弃配置。
2. 报价使用同一明确的known/unknown联合。known须完整mask与费率；unknown无伪造费率/金额。媒体及managed采购拒绝unknown；人工文本已授权的未知报价场景仍记录unknown事实，不能以新增旁路保存旧标量链。
3. retail与supplier分别持久引用其原报价和已确认终态证据，并记录各自冲突/迟到观察。supplier独有量缺失不阻断已完整的retail量；客户已释放后取得采购终态不得重绑原settlement或补扣客户。
4. BoundPlan需明确覆盖发布路由全部合法供应商候选；逐attempt界只能在原公共界内按已认证合同收紧。客户自行填写较小数量不能替代供应商强制上限。任何无法证明最坏费用有界的媒体候选不能发布。
5. 六文本的发布/准入、完整费率、精确成本排序、R/U、证据、结算、来源/退款/恢复必须一体替换；不能仅将DTO换成vector后仍调用标量rawAmount。复用既有metering纯核并按独立公式验证文本金额等价。
6. 选择一个当前可用且能完整证明收费单位、上下界、usage、有限variant及结果资源限制的同步profile；只有该profile需要的新维度才进入首片registry。官方资料与本地fixture不产生真实账号/账单认证。

026的金额事实源保留：收入/退款只读唯一settlement，采购成本只读每个生成attempt的cost_status/cost_amount，不用vector重算或join来源边倍增收入。028在原不可变请求快照补协议/执行种类/schema/variant后，再同步Filter、HTTP、JSON、CSV与前端校验。若增加数量汇总，按(schema_digest,dimension,unit,side)分组并只读所引用最终证据，missing保持未知；任务观察/下载/heartbeat不新增收入或生成attempt。

首个完整可验切片是U：两维文本schema、上述生产DDL及六协议单一路径，实际受限APP/HTTP与026/027联合验收。U之后接一个冻结同步媒体profile；任务和实时按后续阶段实施。未实现的执行种类显式拒绝，不预先开放接口。

| 阶段 | 集中冻结/执行 | 可并行工作与独占范围 |
| --- | --- | --- |
| D0 | 集成人审本文、官方资料与027接口，冻结共同包/types、现行DDL/费率/快照/状态矩阵、端点精确profile | 供应商资料、协议/恶意样本设计、单位化UI合同可并行；不分别改billing |
| U1 | 单一集成人持 `internal/inference`共同types、`metering`registry、`migrations`、`pricing`、`billing/{types,reserve,evidence,settle,procurement,recovery}`、027及PB接缝 | 文本归一化实现者持各文本parser/usage文件；只在冻结共同类型上工作，不保留旧计费路径 |
| U2 | 集成人接通六文本统一向量与全部来源/采购/退款/恢复，串行真实PG/权限验证 | 独立审查者写反例/只读审查；不得并行运行共享专库的安装/默认ACL测试 |
| S1 | 冻结同步媒体共同transport/资源API与一个首profile；集成人持server路由/metadata/共享provider client | 图片实现者独占 `provider/image_*`及图片tests；音频实现者独占 `provider/audio_*`及音频tests；二者均不改共同资金文件 |
| A1 | 冻结Task DTO/状态/SQL、worker权限与只读查询/回调/制品接口；集成人持任务schema与主装配 | 任务实现者独占新 `internal/mediatasks`、独立server任务handlers与tests；复用统一账务，不创建TaskWallet |
| R1 | 冻结session/turn/event、ticket/Origin和队列/期限/收费事件边界；集成人持共同路由/装配 | 实时实现者独占新 `internal/realtime`、独立server WS/ticket文件与tests；不复用SSE函数假装WS |
| Q/W | 统一HTTP/请求/价格/成本DTO先冻结 | 一位前端拥有者独占 `web/src`相关目录/账单/任务/实时组件与types；report实现者独占 `internal/reporting`与CSV合同，二者不并改公共server文件 |
| I | 汇总全部源码/专项结果，完整当前PG→main→浏览器→架构/check；供应商和目标部署单列真实验收 | 集成人统一README/HLD/LLD/架构及manifest，不能在并行写入期间refresh |

首个可立即推进的交付是U完整替换与六文本真实PG闭环。首个媒体纵向片由D0选择一个**当前可用、JSON输入、完整inline结果**的图片或TTS profile，以减少首片上传/任务生命周期面；不再预定旧TTS候选为首发，不因一个供应商停用而删除通用异步视频/实时设计。若当前可认证的STT更适合，也须先明确上传/时长合同再取代首片选择，不保留多套候选运行分支。后续图片编辑/STT、异步视频与实时轮次必须逐片执行其专有故障与资源验收，不能只做adapter后宣布全部媒体完成。

文件所有权安排是后续分工合同，本次仅新增本规格，未创建共同包、迁移、路由或可售配置。集成人必须在实施前把已冻结部分同步HLD/LLD与统一架构能力状态；实施后复核模块图、请求/任务/轮次时序、数据/027来源、权限、源码及验证链接，所有并行结果汇总后执行：

```sh
go run ./scripts/check-architecture --refresh --reviewed --reason '统一计量与媒体本次实际变更、来源/权限/状态及验证边界；未实现部分保持候选'
go run ./scripts/check-architecture
```

本文没有运行PG/程序/供应商验收，也不代表上述阶段已经完成。

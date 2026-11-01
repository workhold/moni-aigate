# 022-C1-P：本地原路退款 v2 纯协议合同（纯层冻结）

2026-11-01。**根与独立审阅者对照v1源码后冻结本文纯层接口及算法；独立纯包及专项race/vet/fuzz、独立Python固定向量已通过，见[验证记录](../../docs/verification/payment-refund-protocol-v2.md)。未修改现有v1/paymentproto、P/mock、迁移、运行证明或任何资金入口。** 本文细化 [C1 资金与执行合同](c1-refund-contract.md) 的协议前置；C0生产来源、全序、维护与独立核验仍待全路径实际验收，[C0纯算法](../../docs/verification/funding-lot-planners.md)通过不满足该前置。C1-P纯数据/签名算法完成，C1-M/S/I仍须另审；协议解析成功或签名正确都不是资金授权。

依据 [v1 DTO](../../internal/paymentproto/types.go)、[请求](../../internal/paymentproto/requests.go)、[回复](../../internal/paymentproto/replies.go)、[通知](../../internal/paymentproto/notification.go)、[签名](../../internal/paymentproto/signing.go)、[P 命令](../../internal/mockpsp/repo_commands.go)与[冻结查询](../../internal/mockpsp/repo_queries.go)。当前v1只执行checkout/cancel；其refund通知缺少本合同的原命令/供应商退款引用/终局墓碑绑定，不能升级解释为v2退款证据。

## 1. 版本、模块与兼容选择

候选新增独立纯包 `internal/paymentrefundproto`，不在现有 `paymentproto` 添 `schema_version=2` 分支，不扩大v1 `commandKind`或通知语义，不改原P表/函数/签名key用途。纯包只用Go标准库；唯一允许的内部依赖是v1已验证opaque值的只读桥接，不能 import billing/payments/provider/mockpsp 或数据库包。

本片v2仅有一种mutation `refund`，沿已确认的v1原capture退款。没有v2 checkout/cancel、查询所有交易、任选退款去向、外币/费用/汇率或冲正。P未来新增独立v2命令/查询/退款事实关系，与原v1 capture只读绑定；原v1订单revision不因v2退款被改写。C1-M需另审与v1 capture根共同串行/外部累计的具体实现，本文不授权修改P。

候选传输路径为P `POST /v2/refund-commands`、`POST /v2/refund-command-queries`，APP `POST /api/payments/sandbox/refund-v2/notifications`。它们仅占命名空间；当前不注册。v1端点必须继续拒绝v2，v2拒绝v1/未知版本，不做自动降级或按字段猜版本。所有v2对象的 `schema_version` 是JSON数字字面量 `2`，拒绝 `"2"/2.0/2e0`。

## 2. 规范值与封闭解析

- 新UUID为36字节小写带连字符非nil UUID，保持v1任意非nil128位值范围，不追加version/variant推断；local_refund_id、command_id、query_id、event_id、fact_id、delivery_id各有独立用途，不能互替。
- 原 environment/merchant/provider transaction身份保持v1语义。merchant_order_id仍为精确 `mps1.<environment UUID>.<原APP order UUID>`，不能生成新的收款订单。新provider_refund_reference为1–100字节ASCII `[A-Za-z0-9][A-Za-z0-9._:/-]*`，拒绝URL、`sk-`/`mg_`/`bearer`敏感前缀；它不必是UUID，不等于local_refund_id。
- amount与original_capture_amount为规范正int64十进制string，`1..9223372036854775807`，无前导0/符号/指数；amount≤original_capture_amount。currency恒 `CNY`。本片不使用float，不接受0金额退款。
- mutation_generation和capture_refund_revision同为规范正int64十进制string；它们属于不同版本域，不能代替provider_order_revision。摘要固定64个小写hex。时间必须精确UTC6 `YYYY-MM-DDTHH:MM:SS.ffffffZ`，有限且年1..9999；不接受offset、少/多小数、闰秒或空值。

请求与查询正文各≤8KiB；reply/notification各≤64KiB；嵌套深度≤8，无数组或开放扩展字段。JSON为单对象、有效UTF8、无BOM/重复member/未知字段/尾随JSON/非法UTF16 surrogate，任何必需字段不可缺失。只有本合同明确的 `result/provider_refund_reference/failure_code` 分支允许显式null；不能用omitempty省字段。对象输入成员可任意顺序，但canonical按以下声明顺序输出，UTF8、无空白、标准JSON转义。

## 3. 原capture绑定与mutation

`CaptureBinding` 是纯包私有字段的不可变值，不提供任意json或trusted bool构造。候选桥接入口：

```go
func CaptureFromV1Notification(v paymentproto.VerifiedNotification) (CaptureBinding, error)
func CaptureFromV1Query(v paymentproto.VerifiedQuery) (CaptureBinding, error)
```

仅接受签名已验证的v1 `capture.confirmed` 或带完整capture的已验证query；解析其canonical副本，映射原merchant_order_id，独立核所有绑定，拒绝零值opaque。该桥接证明的是v1签名capture事实，**不证明**APP已选择该capture、C0 lot资格、余额、财务审批或当前没有争议；这些仍由C1-I在数据库锁内重读。

CaptureBinding的内部canonical顺序为：`capture_protocol_version(数字1),environment_id,merchant_id,merchant_order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision`。从v1查询与通知取得同一capture必须得到相同canonical。原签名原件和v1 attestation保留，不用v2摘要替换v1原证据。

长度帧统一 `frame(x)=uint64 big-endian(len(x))+x原始字节`。摘要为：

```text
capture_digest = lowerhex(SHA256(frame("moni-aigate/payment-refund-capture/v2")
                                || frame(capture_binding_canonical)))
parameter_digest = lowerhex(SHA256(frame("moni-aigate/payment-refund-command/v2")
                                  || frame(refund_request_canonical)))
query_digest = lowerhex(SHA256(frame("moni-aigate/payment-refund-query/v2")
                              || frame(query_request_canonical)))
fact_digest = lowerhex(SHA256(frame("moni-aigate/payment-refund-fact/v2")
                             || frame(refund_fact_canonical)))
```

域字串不含隐式NUL或换行；frame长度计UTF8字节，不计字符。用途域不可互换，parameter_digest不含自身、不含query或mutation_generation，因此同原命令跨代查询/重试保持完全相同body/digest。

RefundRequest精确字段及canonical顺序：

```text
{
 schema_version:2, environment_id:UUID, merchant_id:V1Reference,
 command_kind:"refund", command_id:UUID, local_refund_id:UUID,
 merchant_order_id:OriginalMPS1, provider_transaction_id:V1Reference,
 original_capture_amount:PositiveDecimal, capture_digest:SHA256,
 amount:PositiveDecimal, currency:"CNY"
}
```

纯构造器 `NewRefundRequest(capture CaptureBinding, commandID,localRefundID,amount string)`只复制原绑定及新标识/金额，不生成随机数、时间或环境。wire parser仅产出不带信任的DTO；P另以其持久原capture重新算CaptureBinding/digest和金额，APP另以数据库完整原来源检查。HTTP不能上传CaptureBinding作为审批授权。

本片没有mutation expiry、换command重试、退款目的地字段或客户端自报累计。local_refund_id是本平台退款提案/主状态身份，command_id是本次唯一外部幂等mutation身份；两者首次绑定后都不可改。

## 4. 终局fact与通知

RefundFact精确字段顺序：

```text
{
 schema_version:2, fact_id:UUID, environment_id:UUID, merchant_id:V1Reference,
 local_refund_id:UUID, command_id:UUID, parameter_digest:SHA256,
 merchant_order_id:OriginalMPS1, provider_transaction_id:V1Reference,
 original_capture_amount:PositiveDecimal, capture_digest:SHA256,
 amount:PositiveDecimal, currency:"CNY", outcome:Outcome,
 provider_refund_reference:ReferenceOrNull, failure_code:FailureOrNull,
 capture_refund_revision:PositiveDecimal, completed_at:UTC6
}
```

outcome封闭二选一：`confirmed`要求provider_refund_reference非null、failure_code=null；`final_no_effect`要求provider_refund_reference=null、failure_code为`refund_limit_exceeded|refund_policy_rejected|capture_not_refundable`之一。其他排列拒绝。两种终局都要求P已确认原capture存在、完整binding完全匹配；原capture缺失/身份异体/DB暂不可用只返传输错误或留执行未知，不能伪造无原根的最终fact。

capture_refund_revision是**同一完整原capture身份下独立v2退款决定序列**，从1起单调递增，每个confirmed或no-effect墓碑各占一个revision；不改v1 provider_order_revision。revision用于相同capture的矛盾/排序审计，不能用较大revision覆盖同一refund的较小终局。一个refund只有一个不可变终局fact，同command/local-refund的重复返回必须fact_id/body/digest均相同。

Notification精确字段顺序为 `schema_version,event_id,event_type,fact`；event_id是非nil UUID，event_type仅 `refund.confirmed|refund.final_no_effect`，分别与fact.outcome相等；fact为上列完整嵌套对象。通知只能携带终局，不发送`refund.failed`、not_seen或in_progress通知。event_id/同canonicalfact重投可换delivery_id/时间/签名，但不得换event正文；同event异体须持久conflict后安全ACK，不重做资金。

fact_id是P的终局事实身份，provider_refund_reference是P真实退款效果引用，event_id是通知事件身份，delivery_id是一次传递身份；查询与通知对同终局共享同fact，不能以不同包装字段重复入账。

## 5. reply、query与冻结观察

CommandReply精确字段顺序为 `schema_version,command,parameter_digest,command_state,result`。command为原RefundRequest完整对象，parameter_digest必须重新计算相等；command_state仅`in_progress|completed`。in_progress要求result=null；completed要求result为完整RefundFact，逐字段精确绑定command。CommandReply经专用签名验证后才是认证观察；未验签HTTP2xx正文只有传输提示。

QueryRequest精确字段顺序为 `schema_version,query_id,mutation_generation,command,parameter_digest`，command仍完整原mutation，parameter_digest精确相等。mutation_generation仅绑定APP已持久化的原mutation执行代，不进入mutationbody；它不是P允许第二笔退款的授权。query_id在发送前持久化，同ID异body为冲突，不覆盖旧查询。

QueryReply精确字段顺序为 `schema_version,query_id,mutation_generation,command,parameter_digest,command_state,result`。除command_state多一个`not_seen`外与CommandReply相同；not_seen必须result=null。签名验证必须与**调用方原持久QueryRequest**逐字段一致，包括query_id、generation、全部command与digest；不能只对command_id和金额。

P首次处理query_id时在一个一致观察中持久化请求canonical/digest与回复canonical/digest。之后同query_id同请求只返回该份原冻结snapshot，签名delivery可以更新；即使原mutation后来完成，旧query的not_seen/in_progress也不改成completed。需要新观察时APP持久化新的query_id，仍绑定同原mutation/generation或经原状态机持久推进后的generation。旧代结果不能直接改变新代执行状态，但同原命令的已认证终局是否可收尾由C1-S精确证据selection矩阵裁决。

not_seen仅表示该次冻结观察未见原command，既不创建终局墓碑，也不释放Q。同一个command可能在观察后到达；若v2幂等合同与原授权均完整成立，允许恢复状态机重发**同command/body**，不换ID。query404、空body、HTTP4xx/5xx、超时、无签名或解析失败都不能代替not_seen或final_no_effect。

## 6. 幂等与真正无效果的P语义

未来C1-M须原子保护三个身份：`(environment,merchant,command_id)`、`(environment,merchant,local_refund_id)`、非null `(environment,merchant,provider_refund_reference)`。首次受理将command/local-refund与完整canonical/digest双向绑定；同command同body返回原执行/终局，同command异body或同local-refund换command均为409 identity conflict，无第二效果，也不返回“原命令已无效果”证明。

confirmed要求在同一持久事务记录唯一退款效果、原capture累计与唯一fact；同原capture全部confirmed金额NUMERIC累计≤原capture amount。多个部分退款在原capture根串行，不以不同command锁绕过累计。in_progress不是可重复占两份外部效果的状态，重启后只能完成原命令。

final_no_effect必须同事务落不可变拒绝墓碑+终局fact；此command/local-refund的任何迟到mutation、重复、重启或查询均不能以后产生退款。它不撤回另一个command，不根据TTL/断网/not_seen生成。墓碑和成功fact同样参与身份唯一性、canonical保存与备份恢复；删除墓碑就会破坏“最终无效果”，不能用普通清理过期。

协议只证明该退款command没有外部效果，不证明客户原资金不存在争议或其他命令效果。capture_not_refundable、外部累计异常、迟到互斥fact与原capture争议均需C1/C2共同核查；APP不能因一个已认证no-effect就绕过其资金review/risk latch释放可疑钱包。C2/C3出口未冻结前仍不能开放完整商业退款。

## 7. 签名、密钥与opaque已验证值

v2使用独立32字节HMAC-SHA256 keyset（1–128个版本，ID为1–32 ASCII字母数字/下划线/连字符，active恰一个，UTC6有效区间）。密钥构造复制全部slice并拒绝本keyset内重复材料；未来装配还必须核v1/v2实际key材料不复用。v1 key不能用于v2，v2不能反签v1。旧key为历史认证保留，key轮换不是删除已确认fact或重发命令的理由。

独立header精确为 `X-Moni-Refund-V2-Key-Id`、`X-Moni-Refund-V2-Delivery-Id`、`X-Moni-Refund-V2-Delivery-Time`、`X-Moni-Refund-V2-Signature`，每个大小写不敏感名只能出现一次且仅一个值，无逗号；delivery_time为规范非负Unix整数秒string，签名为64小写hex。拒绝跨v1header猜读。传入receivedAt为当前可信接收时刻，header时刻允许±300秒且位于key有效区间；fact.completed_at≤receivedAt+5秒，合法旧终局无任意“超过N天不收”策略。

HMAC输入是以下各项按顺序逐一frame后的拼接：

```text
domain, key_id, environment_id, merchant_id, context_digest,
delivery_id, delivery_time, lowerhex(SHA256(raw_body))
```

用途与context固定：

| 消息 | domain | context_digest |
| --- | --- | --- |
| CommandReply | `moni-aigate/payment-refund-command-reply/v2` | 原parameter_digest |
| QueryReply | `moni-aigate/payment-refund-query-reply/v2` | 原完整QueryRequest的query_digest |
| Notification | `moni-aigate/payment-refund-notification/v2` | 空字节串（frame长度0）；完整绑定仍在签名raw fact内 |

签名覆盖原始收到字节而非重编码正文，常数时间比较HMAC；验签后仍须完整封闭解析、环境/原请求绑定与时间检查，再生成opaque值。canonical/fact_digest用于稳定语义去重，raw_body hash用于本次传递认证，二者不互换。签名通过但业务binding不同返回安全binding错误，不给caller可消费的部分已验证对象。

候选opaque类型 `VerifiedCommandReply, VerifiedQueryReply, VerifiedNotification, VerifiedRefundFact` 全字段私有，只能由对应Verify函数产生非零值；调用Canonical/Fact等只返防修改副本。Parse函数返回的是未验证DTO，不能隐式转换为VerifiedRefundFact。零值、跨用途/环境/query/原command均拒绝。纯包不提供任意domain签名或“以这个bool视为已验”的构造口。

历史重验需要保留原raw/headers、可信received_at、key版本及完整运行证明；不能在多年后用当前now重验±300秒窗口，也不能信供应商自报received_at。重载数据库已验凭证的安全构造方式需C1-S/I单独冻结，纯包不导出反序列化opaque能力或绕过实时验签函数。

## 8. 运行证明与后续接线边界

现022 immutable v1 payment_runtime_attestation及C0候选proof v2不得原地改写。后续C1-S需追加单独不可变扩展，绑定原v1 digest、C0 epoch/proof、退款wire protocol=2、新P/APP schema对象摘要、v2 keyset/origin/callback及APP/worker/projector实际OID/最小列权限；**wire v2不表示可以直接复用C0 runtime proof版本号或新增一个忽略未知对象选项**。本片不预定数据库迁移号、扩展表名或最终runtime proof版本整数。

默认退款能力关闭；无完整C0/C1 proof不注册mutation/通知/worker。启用与否、Recovery或网络budget均不改变opaque值所证明的范围。Recovery只收通知/query证据，不执行退款mutation或资金写。关闭新退款只挡新提案/批准，原hold证据和允许的收尾仍依原授权与资金合同。每个处理入口必须以原order/refund/lot/source完整图决定效果，不能由协议包直接操作数据库。

当前网络槽、5秒总期限、64KiB响应与ACK等运行参数属于未来C1 transport/HTTP合同；不得因这里有wire上限便冒称已完成真实进程容量、退出或权限验收。正式PSP的最终性、部分退款规则、真实供应商引用与认证须独立认证；本地P的墓碑语义不自动适用于其他平台。

## 9. 纯层验收与冻结阻断

本节纯层要求已按[专项记录](../../docs/verification/payment-refund-protocol-v2.md)运行；测试代码、独立Python表达式及固定golden已经交叉复核。实施范围仅新增纯包/测试，不改v1、P、DB或main；共同v1 race仅证明纯协议回归，不代表再次执行其PG/普通main验收。固定golden由独立framing/SHA/HMAC表达式产生，Canonical/Sign函数不生成自己的expected，覆盖要求如下：

| 类别 | 必须证明 |
| --- | --- |
| wire/canonical | 精确字段/null联合、数字literal2、重复键/escaped member/非法surrogate、UTF8/深度/字节界、UTC6、规范金额与UUID；输入合法乱序归一，不静默丢字段 |
| 原capture桥接 | v1通知与query同capture得到相同binding/digest；非capture/零opaque拒绝；完整保留并核对v1原环境身份，请求/Verify与可信Environment异体拒绝；不会把pre-C0 capture标为可退资金 |
| 摘要与签名 | 四种digest域不互换，raw和canonical用途清楚；三个签名域/query代数/context严格绑定；header重复/未知key/时间界/材料复用/篡改单字节拒绝 |
| 身份与联合 | local refund/provider reference/command/fact/event/delivery分离；confirmed与no-effect nullable字段精确；in_progress/not_seen无result；退款额度单笔MAX边界 |
| 不变性与并发 | 所有构造/Canonical/Verified访问均copy，调用方改slice/struct不影响对象；并发只读无共享可变状态；零值及未验DTO不成为opaque |
| 兼容 | 现v1全部单元/PG/进程回归保持；当前v1命令仍只有checkout/cancel，旧refund.failed永不当v2 no-effect |

纯层无法证明P幂等、墓碑、query_id冻结、原capture累计或APP唯一资金；这些必须C1-M/S/I的真实PG与普通main专项，独立构造通知/query竞争和提交未知正常恢复。C0完整接线、source生命周期、C1生产SQL/角色/证明、C2冲突风险出口和C3备份恢复均为最终开放门禁。纯层已由根冻结；C1-M/S/I及资金接线仍须独立冻结。后续改动与统一架构分层状态一并由根集成刷新。

## 10. C1-P 纯层精确 Go API（根冻结）

本节细化前九节的纯层接口，根仅授权本节列出的纯包与测试。新增包只使用标准库及`internal/paymentproto`的两个已验证值；不修改v1。DTO的Go字段按对应wire字段使用常规首字母大写（ID/UUID使用ID），JSON tag精确按§3–5，顺序与wire表相同。`schema_version`的Go字段为`int`但parser必须在token层只接受字面量`2`；其余计数字符串不得转float。

```go
type Environment struct { ID, MerchantID string }
type SignedHeaders struct { KeyID, DeliveryID, DeliveryTime, Signature string }
type KeySpec struct { ID string; Key []byte; NotBefore, NotAfter time.Time }
type CaptureBinding struct { /* private canonical bytes */ }
type Keyring struct { /* private copied keys */ }

// RefundRequest / RefundFact / Notification / CommandReply /
// QueryRequest / QueryReply 均为§3–5封闭wire DTO。
// RefundFact.ProviderRefundReference/FailureCode 是 *string；
// CommandReply.Result/QueryReply.Result 是 *RefundFact；Notification.Fact为值。
func NewRefundRequest(capture CaptureBinding,
    commandID, localRefundID, amount string) (RefundRequest, error)
func NewQueryRequest(command RefundRequest,
    queryID, mutationGeneration string) (QueryRequest, error)
func CaptureFromV1Notification(paymentproto.VerifiedNotification) (CaptureBinding, error)
func CaptureFromV1Query(paymentproto.VerifiedQuery) (CaptureBinding, error)
func (CaptureBinding) Canonical() ([]byte, error)
func (CaptureBinding) Digest() (string, error)

// 每一种wire DTO均有对应的ParseX([]byte)(X,error)与
// CanonicalX(X)([]byte,error)，X精确限上述六种，不提供generic JSON API。
func ParameterDigest(RefundRequest) (string, error)
func QueryDigest(QueryRequest) (string, error)
func FactDigest(RefundFact) (string, error)

func NewKeyring(active string, specs []KeySpec) (*Keyring, error)
func (*Keyring) ActiveID() string
func (*Keyring) HasVersion(string) bool
func ReadSignedHeaders(http.Header) (SignedHeaders, error)
func (*Keyring) SignCommandReply(Environment, RefundRequest,
    SignedHeaders, []byte) (SignedHeaders, error)
func (*Keyring) SignQueryReply(Environment, QueryRequest,
    SignedHeaders, []byte) (SignedHeaders, error)
func (*Keyring) SignNotification(Environment,
    SignedHeaders, []byte) (SignedHeaders, error)
func (*Keyring) VerifyCommandReply(Environment, RefundRequest, time.Time,
    SignedHeaders, []byte) (VerifiedCommandReply, error)
func (*Keyring) VerifyQueryReply(Environment, QueryRequest, time.Time,
    SignedHeaders, []byte) (VerifiedQueryReply, error)
func (*Keyring) VerifyNotification(Environment, time.Time,
    SignedHeaders, []byte) (VerifiedNotification, error)
```

三个`Verified*Reply/Notification`及`VerifiedRefundFact`均为字段全私有的值。前者分别提供`Canonical()([]byte,error)`及`Fact()(VerifiedRefundFact,error)`；后者仅提供`Canonical()([]byte,error)`和`Digest()(string,error)`。`Fact`只有已验证的completed reply或终局notification才能返回非零值，not_seen/in_progress返回零值和固定错误；不提供由裸DTO/raw/canonical构造已验证fact的函数。所有返回bytes均copy，DTO里的pointer每次解析都新分配；后续修改原始raw、DTO、key specs、返回slice不得改变既有opaque对象。

封闭安全错误为四个exported sentinel：`ErrInvalid=errors.New("payment_refund_protocol_invalid")`、`ErrKey=errors.New("payment_refund_key_unavailable")`、`ErrSignature=errors.New("payment_refund_signature_invalid")`、`ErrBinding=errors.New("payment_refund_binding_invalid")`。不包装源错误或输出原标识/正文。结构/格式/零opaque→Invalid；keyring配置/签名时活动key不适用（含显式选择非active KeyID）→Key；接收未知key/签名/header/time不符（含格式合法但completed_at超过receivedAt+5秒）→Signature；完整可解析但与可信environment/原request不等→Binding。无法分类的内部错误也只返回Invalid与零对象。nil Keyring查询ActiveID返回空、HasVersion=false，签名/验签返回Key。不得在错误时返回部分DTO、bytes或opaque。

Environment.ID为规范非nilUUID，MerchantID完全沿v1 reference规则；不包含payee label。NewQueryRequest必须深拷贝command并独立验证，不生成随机ID或时间。header签名构造允许KeyID为空（使用active）或精确active，Signature必须为空；不接受选择任意旧key签名。验签允许受有效区间约束的历史key。读取headers必须查全名大小写重复，拒绝带逗号的签名字段；附带v1签名字段不参与v2认证、不作回退，缺任一v2字段即Signature。

v1 reference精确语义是有效UTF8、字节长度1–100，每个rune为Unicode Letter/Digit或ASCII `:_./-`，拒绝包含 `://`，并按Unicode小写转换后的值拒绝 `sk-`、`mg_`、`bearer` 前缀；没有首字符必须为字母/数字的条件。merchant_id与provider_transaction_id沿用该规则；新provider_refund_reference仍按§2的独立ASCII规则，不把两者混用。Capture桥接没有可信Environment参数，只保留并独立核对v1原件内部环境绑定；合法的另一环境opaque也可映射，环境选择由后续请求/Verify或数据库授权决定。

签名方法先校验封闭DTO及environment/原request绑定，再覆盖传入raw的SHA；不把raw重编码后签名。command reply绑定包括完整command和parameter_digest；query reply包括完整原QueryRequest中query/generation/command/digest；notification按签名env核完整fact。所有Verify在签名正确后仍执行完全相同的parser与binding检查，使用可信receivedAt核completed_at上界。Sign不生成receivedAt或伪造认证后的对象。

UTC6和key有效时间年界均按UTC核查；key时间不接受零time、非UTC location或非微秒精度，区间端点含首尾且NotAfter≥NotBefore。receivedAt必须非零，允许任意location和纳秒精度，但UTC年界必须合法；delivery_time规范范围`0..253402300799`，避免Unix时刻超9999年。退款引用长度/字符集检查先于敏感前缀拒绝；`sk-`、`mg_`和`bearer`按ASCII大小写不敏感前缀拒绝。所有resource界先于json decode分配检查。

预期文件：`internal/paymentrefundproto/{types,codec,values,capture,requests,facts,replies,signing}.go`及对应golden/边界/不变性/fuzz测试；实施者可合理合并文件，但不得创建DB/HTTP client或生产import。签名域/frame、canonical与golden须由独立表达式复核；先专项race/vet及短fuzz，再与v1真实PG兼容回归汇合。纯包通过只能标C1-P算法完成，退款端点、原路资金效果与P墓碑仍未实现。

独立metadata-only QA限定新增 `scripts/verification/payment-refund-v2-golden.py` 与 `docs/verification/payment-refund-v2-golden.json`：Python标准库独立手写canonical字段顺序、uint64大端frame、SHA/HMAC固定向量，只使用显著合成key、身份与固定时刻，不import/调用Go协议算法。覆盖v1通知/query同capture桥接（含Unicode v1 reference）、command/query、confirmed/no-effect fact及三个签名域、同fact跨包装摘要相等。脚本默认离线重算并比较已提交JSON，不能覆盖原件；显式生成仅写不存在的新路径。该产物不含真实秘密，不声明DB/网络/PSP/资金或完整C1验收；新增脚本及向量通过情况另按实际结果记录。

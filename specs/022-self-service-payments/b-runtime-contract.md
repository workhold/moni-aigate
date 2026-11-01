# 022-B0-M/Q 精确运行合同工作稿

2026-11-01，**根集成人已完整复核并冻结022-B0-M/Q，授权独立沙箱实施；尚无运行实现/验收，B1应用资金未冻结。** 本稿与 [P 已实现领域合同](b-domain-contract.md)、[M SQL 合同](b-mock-sql-contract.md)共同构成实施依据。P wire/HMAC与应用迁移不变，普通main/Config/router不接PSP。冻结包含§10.1两项修订；SQL原型和checkpoint不替代运行验收。

本稿已对照 `internal/paymentproto/{types,requests,signing}.go`、`internal/operations/instance.go`、mailLifecycleGuard 与三份 M/receiver/roles/grants SQL。下文 Go 名称是待冻结目标签名，**不是已存在的导出能力**。收到明确根裁决的项目在相应节注明；其他具体取舍仍须根完整复核。

## 1. 组件、入口与不可越过的类型边界

离线装配由 `internal/paymentsandboxqa` 供测试及独立QA脚本使用，运行组件与普通main不得导入。`Create(ctx, adminDSN, privateRoot)`仅接受专用127.0.0.1:55439/moni_test的moni_test管理员，创建本次随机三schema/七角色，owner安装全部应用迁移并两次ProvisionFor，自有SQL由paymentsandboxschema安装。实际APP/projector LOGIN完成双审1000入账、唯一重放、投影、VerifyFunding/VerifyLedger后保留非空资金摘要。Environment只公开协议环境、origin、manifest路径和对象名称；QA专用Token/Keyring/OpenPool仅供真实HTTP与DB断言，密码保留私有内存/0600组件文件，不打印，不构造绕过attestation的运行配置。Close由调用者排空组件后调用，只清理本次精确对象及秘密；错误仅固定阶段码。该辅助包范围已由根冻结。

B0 新运行组件仅拟有 `internal/mockpsp`、`internal/paymentreceiver`、独立 `cmd/moni-payment-sandbox` 和 QA harness。mock 不导入 billing/server/payments/应用 migrations；receiver 不导入 mock 仓储或应用资金；harness可离线使用既有 migrations/ProvisionFor建立隔离的应用克隆。普通 `cmd/moni-aigate` 不识别 PSP/QA 配置或新增路由。

冻结时将两组件相同的私有文件、catalog attestation和ownership机制集中到`internal/paymentruntime`，自有schema版本1的SQL与受审静态inventory集中到`internal/paymentsandboxschema`。这两个包不导入应用资金/迁移、不创建数据库资源，不提供owner DSN或任意可信配置构造器。组件仍有各自不可伪造RuntimeConfig。`paymentruntime.Load(ctx,path,component)`仅返回字段私有的Config；`Open(ctx,Config)`执行本稿完整启动证明并返回Runtime。Runtime只提供受审内部装配所需的Pool、Check、Close、Component、Environment、Keyring、ActiveKeyID、KeyWindow、Origin、CallbackURL、Done和常量时间TokenMatches；不新增直接DSN/token/nonce/原key accessor。Pool为现有pgxpool指针，只用于受审内部装配；这不是防止同进程代码读取自身连接配置的隔离边界。Done在ownership永久失效或Close时关闭，组件据此取消全部IO；Open的启动ctx不作为服务寿命。mock与receiver不能从对方配置得到合法运行对象。harness仅以离线管理员安装明确SQL/角色和生成私密manifest；服务启动绝不自动建库/DDL。

```go
// 独立mock binary拥有这些对象。值字段均私有；零值不可运行。
type RuntimeConfig struct { /* verified component manifest + pinned identity */ }
type Attestation struct { /* successful exact DB/schema/role proof */ }
type Server struct { /* repository, two bound listeners, guard, callbacks */ }
func LoadRuntimeConfig(context.Context, string) (RuntimeConfig, error)
func New(context.Context, RuntimeConfig) (*Server, error)
func (*Server) Run(context.Context) error
func (*Server) Close(context.Context) error

// mock仓储由New在attested实际runtime pool上构造，不导出任意DSN构造器。
type Repository struct { /* pool, identity, guard, immutable env, keyring */ }
type CommandOutcome struct { Body CanonicalCommandReply; Replayed bool }
type QueryOutcome struct { Body CanonicalQueryReply; Replayed bool }
type ControlOutcome struct { Reply ControlReply; Replayed bool }
func (*Repository) Checkout(context.Context, CanonicalCheckout) (CommandOutcome, error)
func (*Repository) Cancel(context.Context, CanonicalCancel) (CommandOutcome, error)
func (*Repository) Query(context.Context, CanonicalQuery) (QueryOutcome, error)
func (*Repository) Control(context.Context, CanonicalControl) (ControlOutcome, error)
type SignedQueryOutcome struct { Body CanonicalQueryReply; Headers paymentproto.SignedHeaders }
func (*Repository) SignQueryOutcome(context.Context, CanonicalQuery, QueryOutcome) (SignedQueryOutcome, error)
```

以上Canonical类是mock包内私有构造、字段私有的薄封装，不新增P协议格式；至少保存owned canonical bytes、对应P已解析DTO及摘要。只允许以下入口构造，零值/不属于本环境均拒绝，Bytes返回新副本：

```go
func ParseCheckoutInput(paymentproto.Environment, []byte) (CanonicalCheckout, error)
func ParseCancelInput(paymentproto.Environment, []byte) (CanonicalCancel, error)
func ParseQueryInput(paymentproto.Environment, []byte) (CanonicalQuery, error)
func ParseControlInput(paymentproto.Environment, []byte) (CanonicalControl, error)
```

前三个必须先调用现有 P `Parse*`，核启动环境，再调用 `Canonical*` 与 `RequestDigest`；不能直接把任意DTO转换成“可信Canonical”。Go内部生成reply/event先用 P Canonical，返回持久原字节前再 P Parse+Canonical并比对字节/摘要，发现历史异常503，不以jsonb重新marshal修复。

### 1.1 Receiver的真实验证入口

P `VerifiedNotification/VerifiedQuery`只有私有canonical/keyID和`Canonical()`，没有可写字段、可信bool或keyID getter。不得假造setter、从数据库JSON重建Verified、或为接线修改P的冻结签名。

```go
// package paymentreceiver；只由已验receiver配置构造。
type RuntimeConfig struct { /* receiver-only filtered manifest，非mock配置别名 */ }
type Server struct { /* receiver仓储、单listener、独立Ownership */ }
func LoadRuntimeConfig(context.Context, string) (RuntimeConfig, error)
func New(context.Context, RuntimeConfig) (*Server, error)
func (*Server) Run(context.Context) error
func (*Server) Close(context.Context) error
type Repository struct { /* pinned receiver pool, env, keyring, guard */ }
type ReceiveOutcome struct { Receipt ReceiptData; Status ReceiveStatus }
type ReceiveStatus uint8 // ReceiveNew, ReceiveDuplicate, ReceiveConflict；不是资金状态。
func (*Repository) Receive(context.Context, paymentproto.SignedHeaders, []byte) (ReceiveOutcome, error)

// 以下为receiver包内私有helper，只能由Receive所在tx调用。
type verifiedEnvelope struct {
    notice paymentproto.VerifiedNotification
    headers paymentproto.SignedHeaders
    dbNow time.Time
    // 私有tx identity，防不同tx重用验证时刻。
}
func verifyTx(context.Context, pgx.Tx, paymentproto.Environment, *paymentproto.Keyring,
    paymentproto.SignedHeaders, []byte) (verifiedEnvelope, error)
func writeVerifiedTx(context.Context, pgx.Tx, verifiedEnvelope) (ReceiveOutcome, error)
```

Receive复制raw bytes，在HTTP请求进入时已建立的5秒总deadline内开启READ COMMITTED事务，先读该receiver事务`transaction_timestamp()`，用这**同一DB时刻**对exact raw body调用P VerifyNotification；只有成功Verified进入私有writeVerifiedTx，调用Canonical取得owned安全业务字节后再P Parse取得字段。总预算包括body读取、验签、pool等待、锁、事务与COMMIT，不能在进入repo时重置5秒。签名头metadata只来自这次成功Verify的相同局部调用，不另接外部可填“已验签key”值。insert verified_at与该tx时刻相同。Canonical不是验签输入，不先canonical后验HMAC。

初次事件同tx写inbox+receipt；唯一(env,merchant,event_id)同digest返回原receipt、异digest追加conflict。实际COMMIT确认后，初次202、duplicate200、conflict409；commit未知/取消不得先ACK。没有 public `InsertCanonical([]byte, trusted bool)`、`RecordNotification(Notification)` 或可绕过P的receiver仓储入口。B0没有收query的receiver路由，VerifiedQuery仅供QA签名观察测试；后续B1可信query仓储另冻。

Receiver只POST固定callback path，无API/QA bearer或别的公开路由；按P `ReadSignedHeaders`读取四签名header，HMAC承担验证。独立20rps/burst40、最多4在途、64KiB body/5秒总deadline、超限429/Retry-After:1；这些常量已由根接受，不能复用mock API桶而让一侧阻断另一侧。mock API沿既有20rps/burst40/4，control沿5rps/burst10/2；各自固定一个全局桶，不按不可信event/command建立无界状态。

接收器错误码固定为：400 invalid_request/payment_environment_invalid，401 payment_signature_invalid，404 not_found，405 method_not_allowed，409 payment_event_conflict，413 body_too_large，415 unsupported_media_type，429 rate_limited，503 receiver_unavailable/receiver_outcome_unknown/mock_instance_unavailable。409仅在异digest冲突证据COMMIT确认后返回；成功包络沿§5。未知PG/协议内部错误不反射原文。接收器不接受Authorization（有该header拒400），不将额外Bearer作为替代验签入口。

所有listener只HTTP/1.1固定path、无query、POST，Content-Type解析为application/json且只允许可选charset=utf-8，其他参数/重复Content-Type拒415；Content-Encoding缺省或单一identity，压缩/重复header拒415；Authorization恰一个、只API/control接受其各自Bearer，不把QA token认作API。认证失败后不做业务DB。成功no-store；错误固定`{"error":{"code":"<allowlisted>"}}`，不包含message/details/raw值；P原`invalid_request/payment_environment_invalid`映射400，`payment_signature_invalid`映射401，运行错误沿领域稿503。请求ID不回显外部header。以上媒体与错误包络规则已由根接受。

## 2. 固定命令/查询状态算法

所有command与query输入均沿P既有参数/摘要、同ID永久回执规则；以下只补数据库转换步骤。每次总预算5秒包含pool、锁与commit；attempt开始清空上一返回值。事务内不做HTTP，原始PG错误只内部分类，外部固定安全码。

### 2.1 checkout

在同merchant gate、存在order行锁后先查command_id：存在则完整核对kind/order/digest，同体回原initial_reply、不重跑effect；异体409，不能因新order状态变化重写旧回执。首次command：

| 锁后事实 | 一次性事实与结果 |
| --- | --- |
| 无order，未命中hold，DBnow<expires | 保存原条款，ready rev1+ready transition、processing completed、final与initial均ready；不自动capture |
| 无order，未命中hold，DBnow>=expires | 保存原条款及内部checkout_ref，closed rev1+closed transition，closed_at=该DBnow，final/initial rejected(order_expired)；不发capture |
| closed已有order/tombstone | 不改原order/条款，新增本command的completed rejected(order_closed)，revision=原order.rev |
| ready/captured已有order | 不改原order，新增completed rejected(checkout_rejected)，revision=原order.rev；不同command不能重新创建或重开 |
| 当前command命中合法hold | 只存command canonical/initial in_progress及processing held，held_by_control精确绑定本次已消费plan；不建/改变order，无final |

checkout.created_at来自冻结请求，模拟器不重写它；普通checkout可以在created之前提前到达并进入ready，**normal capture仍要求created<=DBnow<expires**。此提前ready语义已由根接受，与P保持一致；checkout本身不证明收款，真实应用created_at仍由应用DB冻结。本片不增“任意时刻创建/推进订单”的QA入口。所有生成checkout_ref/transition UUID在一次逻辑command首次尝试前固定，明确回滚重试沿原ID；commit未知不再生成第二组身份。

### 2.2 cancel与release

首次cancel无order建立closed tombstone rev1，amount/created/expires/checkout_ref全null，closed_at=锁后DBnow；有ready则closed rev2+transition；有closed只返回其closed；有captured只返回精确canonical capture。所有正常首次cancel均processing completed并持久final/initial，hold则只held、无effect。

release_command使用原command参数，不接受改金额/条款；按原merchant锁、processing行锁核对确为held，再按本次释放后的最新order/DBnow应用同算法，追加final、processing held→completed+released_by_control_id。原initial_reply永远in_progress。已completed状态为state_conflict，不重复release；同一control operation重读原回执仍accepted。release不再次匹配hold或after-first-command故障。

### 2.3 query

新query在同merchant gate、order SHARE下核原command身份；已存在query先核全部canonical参数，原body永久重读。command未知→not_seen；processing held→in_progress；completed时优先反映同order当前captured/closed，否则用该command final。一次snapshot_body+digest+observed_at同tx提交，不更新order/capture/command。

query回复由SignQueryOutcome在snapshot提交后完成；签名使用新的delivery UUID、单次DB时刻取canonical Unix秒、当前active key，exact body为已存snapshot，并经P SignQuery再次验证完整request/body绑定。同query重读只换签名包络，不换body/digest。若key此时不可用503 `payment_key_unavailable`，snapshot仍在，原query重试可恢复。query.ParameterDigest始终绑定原command规范请求，不是本次query摘要。

## 3. Typed QA：确定字段变换

以下产品语义已由根在本轮明确接受，仍待整稿冻结。ControlInput/Reply沿领域稿七字段与显式nullable，不扩成自由JSON/SQL/URL注入工具。

基准符号：`O`为锁住订单及冻结terms；`C`为同订单已有canonical capture；`T`为本次control在所有merchant/order锁后的单DB时刻。普通生成ID为独立服务端随机UUID，provider tx/event/case用非秘密受控ref（例如`txn:<UUID>`、`evt:<UUID>`、`case:<UUID>`）；refund_id为canonical UUID。内部event行UUID与对外event_id分开命名，不混用。

正常capture_order：仅O.state=ready、terms齐全且`O.created_at<=T<O.expires_at`。amount=O.amount、currency=CNY、occurred=T、revision=2、provider tx为新ref；同tx更新order captured rev2、transition、唯一capture、canonical capture.confirmed event+pending delivery、control accepted。已captured/closed/不在时间内都state_conflict，不用QA操作延长原订单。

emit_test_event从完整terms的ready/captured/closed订单生成**独立测试event+pending delivery**，canonical_capture_id=null，不改变order/capture/transition。缺order→target_not_found；tombstone或缺必要C→state_conflict。base优先C的固定amount/currency/occurred/revision/transaction；无C时base为O.amount/CNY/T/revision2+独立测试transaction。每个新control有新event_id，这个事件身份本身不计作业务变换；同operation永久读回原event/body。

| scenario | 必要源 | 相对base的精确变化；未列字段原样 |
| --- | --- | --- |
| capture_at_created | 完整O terms | type=capture.confirmed；occurred_at=O.created_at |
| capture_before_created_1us | 完整O terms | occurred_at=O.created_at-1us |
| capture_before_expiry_1us | 完整O terms | occurred_at=O.expires_at-1us |
| capture_at_expiry | 完整O terms | occurred_at=O.expires_at |
| capture_future_6s | 完整O terms | occurred_at=T+6s |
| amount_plus_one | 完整O terms | amount=base.amount+1 |
| duplicate_capture_other_transaction | 必须C | 只换新provider_transaction_id；原amount/currency/occurred/revision不变 |
| same_revision_conflict | 必须C | 保持transaction/revision/occurred，仅amount=C.amount+1 |
| refund_unsupported | 必须C | event_type=refund.confirmed、只追加新refund_id，保持原C公共事实 |
| dispute_unsupported | 必须C | event_type=dispute.opened、只追加新case_id，保持原C公共事实 |

数值溢出（含MaxInt64+1）或UTC year1/9999边界减加不能表示时持久rejected/state_conflict，不能环绕/夹取。时间场景不偷偷改变amount或transaction；amount场景不偷偷改时间。P支持上述全部事件联合；Canonical/Sign只验证格式，未来6秒能签出，但**即时**VerifyNotification以同T验证会拒绝`occurred>T+5s`。异步回调若延迟超过1秒，未来场景可能随后变成可验证，所以不能把“永远应拒收”当测试oracle；要验证body精确变换及即时P反例。其他订单时间/金额矛盾也可能被纯receiver正常接收，它没有应用订单资格或入账职责；B1以后另验商业拒绝。

control reply resource_id：capture_order为canonical capture内部UUID；emit_test_event为event内部行UUID；release为command UUID；arm_fault为本operation UUID。rejected资源一律null。所有接受/拒绝结果均作为不可变mock_controls保存，HTTP200返回原ControlReply；仅同operation异参数409 `mock_control_conflict`。权限/语法/环境错误不保存一条伪业务拒绝。

## 4. Typed fault：目标、持久消费与断开

command/query故障允许预埋尚未存在的精确UUID；callback必须存在对外event_id，缺失target_not_found。既存目标若该boundary已经过不可达则state_conflict。每`(target_kind,target_id)`最多一个remaining=1，第二个arm持久rejected/state_conflict；无wildcard或按下一任意请求触发。

| boundary | 目标/可达性 | 持久消费与effect顺序 |
| --- | --- | --- |
| before_command_commit_disconnect_once | CommandID，尚未见command | 独立短tx消费plan=0提交；再执行正常command业务tx、在commit前明确rollback，然后hijack断连接；plan不随业务rollback复活 |
| hold_checkout_before_effect | CommandID，尚未见且真正请求kind=checkout | 消费plan、command intent+initial in_progress+processing held同tx；没有order effect；commit未知不再次接受成新intent |
| hold_cancel_before_effect | 同上kind=cancel | 同上；若实际kind不匹配则HTTP400 invalid_request且不消费、不接受command，不把checkout按cancel解释 |
| after_command_commit_drop_reply_once | CommandID，尚未见command | 正常首次command提交成功后，独立短tx消费，再断连接；重放/release不再次触发；未知独立消费保守断开，不在同请求重新消费 |
| after_query_snapshot_commit_drop_reply_once | QueryID，尚未见query | snapshot成功提交后独立消费再断连接；原query重读永远只读原snapshot，不再次掉包 |
| before_callback_send_once | 已有对外EventID，arm时锁后delivery为pending/retry_wait或lease已过期的leased | 在下一个claim已提交且网络前独立消费，明确不发HTTP，保留已claim lease/预算，不立刻重置pending；等lease到期恢复 |

before消费已经确认后，进程若在业务tx开始前退出，该次fault已永久用尽；harness不能要求重启必再次触发。after业务commit未知则不假定成功去改plan，回mock_outcome_unknown/断开；同原请求重试查持久command/query，对已经存在immutable结果的精确after-plan按§8.1独立终结，不能任意重播effect。

消费独立tx本身commit未知：保守drop，当前请求不再次消费/不执行业务；后续原请求先读取plan终态。回调在before-send消费未知也不HTTP，保留lease。故障不是普通业务错误码，不在应用handler增加可用的任意kill/路径hook。

callback arm在callback-target gate后锁delivery并读取DB时刻：live leased、delivered、review_required均持久rejected/state_conflict；只有上表状态可接受。不能用“尚未真正发出网络”猜live leased可达性，否则在Prepare释放gate后新增plan会错过已授权本代发送。此收紧已由根接受，不新增prepared列或跨HTTP持锁。

精确断开：独立HTTP/1.1 listener，拒协议upgrade；故障分支在写任何header/body前用`http.Hijacker`拿conn并关闭，失败则安全503，不伪造网络超时已成功。真实COMMIT未知由harness实际kill/断网覆盖；内存error或预设return不是COMMIT未知验收。所有测试hook仅harness私有IPC，不能经API/control传任意文件路径或SQL。

## 5. Safe receipt envelope 与ACK证据

本节request_id收紧与时间边界已由根接受。receiver每次HTTP生成canonical非nil UUID，忽略外部追踪header；持久data重复，包络request_id可新。

```go
type ReceiptData struct {
    ReceiptID string `json:"receipt_id"`
    EventID string `json:"event_id"`
    Status string `json:"status"`             // 只received
    Environment string `json:"environment"`   // 只sandbox
    ReceivedAt string `json:"received_at"`    // UTC6
}
type ReceiptEnvelope struct {
    Data ReceiptData `json:"data"`
    RequestID string `json:"request_id"`
}
type SafeACK struct { /* private exact http status + owned canonical data */ }
func ParseACK(status int, raw []byte, expectedEventID string, dbObservedAt time.Time) (SafeACK, error)
func (SafeACK) DataBytes() ([]byte, error) // owned copy，zero拒绝
```

ParseACK仅HTTP200/202、body≤8KiB、合法UTF-8严格单对象、无重复/未知/大小写变体/null/尾JSON；精确字段集合，data.request fields按声明顺序canonical。receipt_id/request_id必须UUID，event_id须等本次业务对外event_id，status/environment精确常量，received_at规范UTC6且`<=本次mock接收后单DB观察+5s`；**没有年龄下界**，旧durable receipt重放合法。P私有strictJSON不能从mock直接调用；mock/receiver须自有小型严格parser并复用同一组反例，不用普通json.Unmarshal静默丢重复key。

仅SafeACK能进入ACK仓储接口，持久receipt_data为canonical data，绝不保存request_id、原响应、header、Authorization或signature。204/其他2xx、坏receipt、环境/事件错配、超限、redirect都不是ACK；固定分类protocol/environment等，不反射原body。

```go
type DeliveryClaim struct { /* private event row/external ID, gen, immutable attempt */ }
type Observation struct { /* private typed ACK or controlled Failure */ }
func ACKObservation(SafeACK) (Observation, error)
func FailedObservation(code FailureCode, safeStatus *int) (Observation, error)
func (*Repository) RecordObservation(context.Context, DeliveryClaim, Observation) (RecordOutcome, error)
```

## 6. Manifest、nonce与exact role启动API

### 6.1 文件形状与隔离

master manifest只由离线harness生成/持有，不交给服务；过滤后component manifest的核心形状如下（字段名是本稿待冻结接口，不是已有Config）：

```go
type Component string // mock | receiver；app仍使用既有main配置。
type RolePin struct { Name string; OID uint32; Login bool }
type SchemaPin struct { Name string; OID uint32; Owner RolePin; Runtime RolePin }
type DatabasePin struct { Host string; Port uint16; Name string; OID uint32 }
type SQLPin struct { SchemaVersion int; SHA256 string }
type KeyMaterial struct { ID, MaterialBase64URL, Digest, NotBefore, NotAfter string }
type SecretFile struct { Name, SHA256 string } // 仅同私有目录basename，不接受绝对/../
type ComponentManifest struct {
    ManifestVersion int; ProtocolVersion int; Component Component
    EnvironmentID, StateDirectoryID, MerchantID, PayeeLabel string
    Database DatabasePin
    App, Mock, Receiver SchemaPin
    AppProjector RolePin
    OwnSchemaSQL SQLPin
    RuntimeDSNFile, NonceFile SecretFile
    CallbackURL string
    // mock字段: APIOrigin,ControlOrigin,APITokenFile,ControlTokenFile,ActiveKeyID
    // receiver字段: ListenOrigin,ActiveKeyID（仅Keyring构造，不签名）
    Keys []KeyMaterial
}
```

序列化字段使用固定snake_case、所有该component必要字段必填，另一个component秘密字段**禁止出现**，不以空串/null占位；Go外层应为mock/receiver两个专用wire struct而不是用一个允许任意可选字段的map。以上形状为对照表，不直接允许json.Marshal一个包含所有秘密的总对象。

mock只有自身DSN/API token/control token/nonce和签名材料；receiver只有自身DSN/nonce和验证材料，不持mock API/QA token或DSN。app文件不增PSP配置。App/Mock/Receiver pins均为无密身份引用，用于catalog否定检查，不允许跨schema读业务表。`Keys[].material_base64_url`直接嵌在该进程0600的component manifest中；manifest本身是0700私有目录内的秘密文件，本片不另开独立key文件，也不接受外部key路径。mock/receiver对同一签名key持有相同验证材料是协议需要；nonce/API/control token与各不同key版本之间必须独立，不把两份相同签名key副本误判为不合法重复。不得原样纳入startup report或String/JSON日志。

LoadRuntimeConfig严格读取≤64KiB JSON，无未知/重复字段、非法UTF-8、尾JSON；先以O_DIRECTORY|O_NOFOLLOW打开所属0700私有目录并保留dirfd，再以openat(dirfd,basename,O_NOFOLLOW)读取0600的manifest/秘密文件，fstat核当前euid拥有/regular file/精确mode，禁止symlink/路径逃逸/目录groupother权限，避免校验路径后再按路径重开造成置换窗口。secret basename限定 `[A-Za-z0-9_-]{1,64}`，同目录打开并核字节SHA256；DSN文件≤8KiB，nonce/token文件≤44字节，内嵌key必须是43字符canonical无padding base64url，读取先限长而非无界ReadFile。禁止记录path中的秘密或DSN。校验完成后复制内存材料，配置值无导出Secrets访问器，错误只固定码。

nonce原始32随机字节，token也各32字节，用无padding base64url文件格式（43字符+可选单LF，读后canonical核对），HMAC key每版32字节且≤128版。全部nonce/token/key材料两两不相同；组件nonce相异由master/harness生成证明，单组件不得为验证而读取另一组件秘密。DB marker摘要域固定UTF-8前缀+NUL+原32字节：

```text
mock state:     SHA256("moni-mock-state-v1" || 0x00 || nonce)
receiver state: SHA256("moni-receiver-state-v1" || 0x00 || nonce)
mock API:       SHA256("moni-mock-api-token-v1" || 0x00 || token)
mock control:   SHA256("moni-mock-control-token-v1" || 0x00 || token)
signing key:    SHA256("moni-mock-signing-key-v1" || 0x00 || key)
```

receiver的签名key digest也用同signing域，因其必须证明验证材料等于mock签名材料；state nonce与token独立域。以上域派生已由根接受。SQL本身只约束64位小写十六进制摘要，不掌握秘密原材料；现有SQL原型随机摘要fixture不能作为另一套运行算法。后续实际manifest/harness必须按此唯一算法生成marker/key摘要并做正负测试，不接受猜测兼容。

### 6.2 DSN、监听与catalog attestation

DSN parser必须在pgx ParseConfig前词法核白名单，禁止环境变量/service/pgpass隐式补目标：精确单host127.0.0.1、port55439、database moni_test、sslmode disable、manifest本runtime user和非空密码；拒DNS/localhost/socket、多host、proxy/tunnel、fallback/service、options任意GUC。运行连接强制`search_path=<ownschema>,pg_catalog`、synchronous_commit=on、UTF8，连接后核实际server address/port、database/current_user/OID。任何PG错误不直接写URL/日志。

API/control/receiver的URL必须scheme http、literal127.0.0.1、明确1..65535端口、无userinfo/query/fragment、规范path；API/control仅origin `/`，两端口不同，receiver CallbackURL path精确`/api/payments/sandbox/notifications`且其origin等receiver ListenOrigin，不接受自定义return URL。IPv6本片不提供隐含兼容；若原上位“literal loopback”希望同时IPv6须先补一致地址pin，不把localhost解析当loopback证明。

启动API按以下顺序执行，成功前不打开listener、不启动callback/receiver处理：

```go
func loadManifestFiles(context.Context, string, Component) (RuntimeConfig, error)
func openPinnedRuntimePool(context.Context, RuntimeConfig) (*pgxpool.Pool, error)
func attestCatalog(context.Context, *pgxpool.Pool, RuntimeConfig) (Attestation, error)
func acquireOwnership(context.Context, *pgxpool.Pool, Attestation) (*Ownership, error)
func attestState(context.Context, *pgxpool.Pool, RuntimeConfig, Attestation) error
func bindPinnedListeners(context.Context, RuntimeConfig, *Ownership) (Listeners, error)
```

attestCatalog逐项比较，不以“连接能SELECT一行marker”替代：

1. database名/OID、三个schema名/OID/nspowner和七角色名/OID均与pin相同且非零；own role=current_user，session_user也必须同runtime，禁止管理员会话SET ROLE冒充服务登录。
2. runtime LOGIN true、owner LOGIN false；七角色NOINHERIT/NOSUPERUSER/NOCREATEDB/NOCREATEROLE/NOREPLICATION/NOBYPASSRLS，无彼此或任何额外pg_auth_members入/出membership；自身不是任意schema/table/function owner。不能只查rolsuper而漏BYPASSRLS/预设member。
3. 自有关系/函数集合等于嵌入受审SQL版本的expected object inventory：名称、kind、owner、signature、非SECURITY DEFINER、固定`proconfig search_path=<精确own>,pg_catalog,pg_temp`。unexpected函数/触发器/扩展或owner改变启动拒绝。SQL文件SHA与mock/receiver_schema_migrations唯一version/hash匹配，不自动升级。

   2026-11-01补充冻结namespace边界：仅允许版本1 SQL的精确对象及PostgreSQL由其表自动生成的row type/array type；`pg_type`须逐一验证名称、kind、owner、typrelid/typelem/typarray双向绑定，预期表集合来自静态inventory，不能只比较数量。拒绝独立enum/domain/composite/base/range及其数组。自有namespace内operator、rewrite rule、policy、collation、conversion、opclass/opfamily、text-search configuration/dictionary/parser/template和extended statistics均应为零；其中rule/policy按其表namespace核验。既有index/constraint/trigger/function/catalog/权限证明继续保留。离线owner额外添加enum/domain/无冲突operator/普通表rule必须启动失败；本条不扩大对app schema对象内容的读取权限，也不宣称能防数据库管理员在启动证明完成后的在线改DDL。
4. 自有表`has_table_privilege`与`has_column_privilege`按grants的完整正/负矩阵比，不仅最少正权限：marker/keys/migrations只SELECT；历史只明确SELECT/INSERT；mutable表只规定UPDATE列；DELETE/TRUNCATE/REFERENCES/TRIGGER/table-wide UPDATE/CREATE均拒绝（SELECT锁所需列例外已有授权）。函数EXECUTE仅own invoker inventory，PUBLIC不能借权限扩到跨schema。
5. 对另外两schema无USAGE/CREATE、每关系七种权限SELECT/INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER均false、每函数EXECUTE false；包括app/runtime/projector没有mock/receiver任何权。用pg_catalog OID核验，无需读取业务数据；负测试需实际runtime LOGIN尝试。
6. fsync/full_page_writes/synchronous_commit正确；不声称据此完成断电/磁盘损坏认证。

attestState要求own marker精确一行，environment/merchant/payee（receiver不存payee则只比配置合法值）、DB/schema/OID/角色文本、state-directory UUID、nonce/token摘要、URL/protocol与manifest相同；所有key id/digest/window集合完整相同，active ID必须在集合且当前DB Unix秒位于其窗口。历史delivery/key引用缺任何版失败，多余版本文件/行也失败，不静默drop或挑最新版本。receiver只读自己的marker/keys；mock只读自己的marker/keys，不跨schema查receiver。

同名重建schema/database但OID变化、复制marker/manifest、nonce重生、缺目录/文件、误角色、角色权限增加、缺state/migrations/key都在listener前失败，无init/default目录/自动建空fallback。只输出固定错误码和无密component/environment/schema/role UUID引用。完整catalog inventory与SQL哈希必须由代码生成/受审常量交付，不能启动时从待验证DB自身生成expected再与自身比较。

## 7. Ownership Guard与生命周期

现有 `operations.AcquireInstance` 使用单int64锁，与PostgreSQL二参数int32 advisory锁空间不重叠，不能直接套到本合同而悄悄换namespace。mock ownership用专用session `pg_try_advisory_lock(782193403, int32(mock_schema_oid))`；receiver用独立`pg_try_advisory_lock(782193404, int32(receiver_schema_oid))`，两项均已由根接受。uint32 OID→int32按32位二进制位模式转换，不做溢出拒绝或截成31位。对应unlock也必须使用同一二参数重载，不能拼成单int64。

```go
type Ownership struct { /* private dedicated conn, permanent lost flag */ }
var ErrOwnershipLost error // Error()固定mock_instance_unavailable
func (*Ownership) Check(context.Context) error
func (*Ownership) Close(context.Context) error
func lifecycleGuard(context.Context, *Ownership) func(context.Context) error
```

Check用专用持锁连接，不借业务pool Ping代替；只复用**本次注册后**开始的真实Ping，不缓存上次成功，不重新try_lock造成锁重入计数。正常取消：lifecycle/调用ctx已取消直接context.Canceled；未取消时独立`context.WithTimeout(context.WithoutCancel(check),1s)`检查锁会话。真正连接/检查失败永久lost，取消全部HTTP/DB/network，不能在同进程自动重连夺锁后继续。Close等待检查runner退出再unlock准确两个参数；未知unlock/已lost关闭连接，不把可能持锁session放回pool。

API/control/receiver在每次进入DB事务前fresh Guard，callback claim/prepare/record各fresh Guard，网络前再check；ownership失败即停止新动作。检查通过不等于网络全局fencing：旧进程可能已发出的HTTP只能取消/等待；callback durable event+receiver去重保幂等。新实例不重发仍live lease，须等原60秒租约；不得启动时归零generation或立即把所有leased改pending。

Run顺序：完成attestation/ownership→bind全部固定listener（任一个失败关闭已绑定者）→启动HTTP及callback runner→ready。SIGTERM取消接收与callback生命周期、关闭listener、等待在途HTTP/tx/网络，单次Close总上限15秒；未退出的连接强制close，所有goroutine退出后再释放ownership/pool。正常取消返回nil，不因ctx取消误fence或改持久lease。不能先释放lock让另一实例启动、旧IO仍后台运行。

## 8. 全锁顺序与repo事务模型

command/query/control操作ID各有独立target gate，不能只依赖order锁来保护同ID跨两个不同order的竞争。根已接受以下二参数int32 namespace：command782193410、query782193411、callback782193412、control-operation782193413、merchant782193414、receiver-event782193415。与ownership782193403/404首参数不同，与应用现有单int64锁空间不重叠；实现与解锁不得换重载。第二参数为SHA256前4字节大端uint32的int32位模式；hash输入固定UTF-8 `moni-payment-lock-v1`、component、environment UUID、kind、完整target五项以NUL分隔，无尾NUL。merchant target为完整原merchant_order_id，receiver-event target为商户Ref与对外event_id以NUL分隔。完整UUID/字串仍精确比较，hash碰撞只串行不授权。

为故障“独立消费tx→业务tx”之间不被第二arm插入，新repo一次入站操作在**同一借用连接**持目标session advisory gate直到结束，5秒总预算内释放/关闭；没有HTTP出站在此gate期间运行。不能在不同连接try_lock/unlock。成功持有后立即注册defer清理，使用独立有界清理ctx按逆序unlock；未知unlock、tx状态不明或坏连接必须close并销毁，不能回pool，不使用`pg_advisory_unlock_all`掩盖重入/次序错误。顺序：

```text
入站Checkout/Cancel: command-target session gate -> merchant xact gate
  -> existing order NO KEY UPDATE -> command/processing/final uniqueness
Query: query-target session gate -> merchant xact gate
  -> existing order SHARE -> query uniqueness -> command/processing/final plain read
Control: operation-ID session gate -> [command/query/callback target session gate如适用]
  -> merchant xact gate如适用 -> order NO KEY UPDATE -> processing/plan或event/delivery
Callback候选: plain有界读并关闭Rows，不持tx/row锁 -> try callback-target session gate
  -> 短claimtx: delivery UPDATE SKIP LOCKED并重验due -> commit -> release gate
Callback prepare/fault消费: try callback-target session gate -> delivery行校验/plan
  -> commit -> release gate -> 网络；observation仅delivery行UPDATE，不再回target/merchant
Receiver: own event identity advisory xact gate -> own inbox/receipt/conflict
```

target gate与operation gate严格使用上述独立二参数namespace。callback绝不能先锁delivery行再等待session gate；try失败释放连接后换候选/下轮，已claim的Prepare拿不到gate则保留原lease并等待到期，不发送。Control始终operation→target→merchant，不能通过callback反转这一路径。不持merchant锁回锁另一个merchant；所有FK落到已存在immutable行或当前tx新建行；control/event/capture循环引用按受审deferred关系，不能为绕循环提前提交半张图。

同ID异体跨order：command-target先串行，取得后plain查已存command核原完整身份，异体409，不再去锁其另一个order。query/control相同原则。release_command先plain查command得到merchant，再target gate后重新读身份，未知command持久target_not_found；不能先锁processing再回merchant。callback target用对外event_id映射不可变内部row id，receipt比较也用对外ID，row id/generation用于CAS。

事务最多三attempt（初次+两次重试），仅真实40P01/40001已明确rollback时允许，按同canonical输入与同服务生成IDs重做。COMMIT返回EOF/超时/上下文/网络类全部unknown；不因HTTP客户端已断开宣称rollback。COMMIT明确pgx.ErrTxCommitRollback归已知rollback但不自动重试普通约束失败。失败attempt清空outcome，rollback用独立有界1秒清理后销毁未知连接，避免把超时ctx下半开tx放回pool。

mock-outcome未知503 `mock_outcome_unknown`，客户端只原ID同体恢复；not_seen query不能证明迟到原command不会提交。没有换ID补订单、补capture、重置fault/claim预算的API。

### 8.1 after故障的崩溃缝隙

新command/query业务提交成功、消费after-plan之前进程崩溃：原响应已经丢失，其fault目标已不可达。重启后原ID读取现有immutable结果，在独立tx将仍pending且精确匹配的after-plan标记消费/终结，**不再人为drop这次重读**，不重做业务effect。即使该收尾消费未知，只保守断开本次请求，后续仍按同原ID读取；不能重建第二份业务结果。before已消费但未开始/已rollback的业务，原ID重试走正常命令，不再触发同plan。hold消费与held intent同tx故不存在持久plan=0却没有intent的合法中间态。

此恢复规则已由根接受，补足领域稿“after只在首次接收commit触发、重放不触发”；仍须实际kill窗口测试，不以SQL表存在替代。

## 9. Callback仓储与网络API

```go
type FailureCode string // network|timeout|auth|protocol|environment|temporary|permanent
type RecordOutcome uint8 // CurrentRecorded|StaleRecorded|AlreadyRecorded
func (*Repository) ClaimDue(context.Context) (DeliveryClaim, bool, error) // 最多一个确认claim；false无可用
type PreparedDelivery struct { /* private immutable raw body + signed header inputs */ }
var ErrDeliveryDeferred error // 固定mock_delivery_deferred；内部控制结果，不是HTTP失败observation。
func (*Repository) Prepare(context.Context, DeliveryClaim) (PreparedDelivery, error)
func (*Repository) ExpireFinalClaims(context.Context, int) (int, error) // bounded<=100
func (*Repository) ObserveReceivedAt(context.Context) (time.Time, error)
func sendOnce(context.Context, PreparedDelivery) (status int, rawReceipt []byte, err error)
```

固定两个runner各仅有一个在途槽，空闲时才调用ClaimDue；每次最多返回一个成功提交claim，避免批内部分unknown掩盖已确认claim。先Guard，在同5秒总预算内plain读取最多100个due候选（pending/retry_wait或expired leased、attempt_count<20），固定按next_attempt_at、event内部UUID排序，完整关闭Rows；随后逐一try callback-target session gate，取得后才开启短tx，`FOR UPDATE OF delivery SKIP LOCKED`并重验相同due条件。无锁/不再due就rollback、释放gate、看下一个候选；不是在delivery row锁内等待session gate。按trigger派生lease的实际RETURNING值构造immutable attempt，generation/count各+1、DB Unix秒、当前有效签名key、固定body digest，同tx提交并释放gate，返回claim,true,nil。无候选返回零值,false,nil，普通错误或COMMIT未知只返回零值,false,error；**不发HTTP**，持久是否存在由租约后恢复，已计次数不退款。已取得并确认一个claim立即返回，不在本调用继续做第二个claim。

Prepare独立Guard、先try callback-target session gate再短tx，核event/delivery/immutable attempt/当前leased+generation相等，body摘要等、DB lease剩余>=15s、key存在且delivery timestamp在其窗口。before-send故障的“独立消费”指独立于先前claim的本次Prepare事务：在已锁delivery且持target gate的该tx内消费plan并COMMIT，返回零值+ErrDeliveryDeferred，明确跳过IO，不另借连接嵌套tx、不写虚假“已发失败observation”；否则提交只读校验tx、释放gate后返回owned body+按attempt deliveryID/time/key生成签名。gate不可得/lease或generation不再可用也返回零值+ErrDeliveryDeferred；其他校验/提交错误返回零值+error。任意error均不HTTP，不改原持久claim/预算；COMMIT未知不能靠立即读state猜授权补发。Sign只能选择当前active，因P Sign API不支持任选旧key；若claim后active改变，进程配置在生命周期内不热改active，重启的旧claim不继续发而等lease到期新claim，不能用新active签旧attempt key。

网络前fresh Guard，无DBtx；10秒总IO，transport Proxy=nil、DisableCompression=true、拒redirect、仅固定literal callback地址、`DialContext`核精确pin，Content-Type JSON、无环境代理/DNS/任意URL。服务端响应body先限8KiB再parse；非identity Content-Encoding或非JSON safe receipt为protocol失败。error只分类受控码，无原URL/body/signature日志。

收到response后先单次独立DBclock ObserveReceivedAt，作为ParseACK时间界；组建typed Observation后RecordObservation短tx锁delivery，插immutable IO结果。同event/generation已有observation时，比对outcome/http_status/error_code/安全receipt_data四项，同体返回AlreadyRecorded，异体invariant失败；比较排除服务端新生成的row UUID/observed_at。只有当前同generation且DB lease仍live可CAS delivered或retry_wait/最终review。generation已落后、已terminal或同generation但lease已过期均记StaleRecorded：可保存一次旧结果，但不能借迟到ACK改为delivered，也不能改当前generation的状态。所有预算20、60秒lease、退避及当前failed/ACK证据要求沿SQL稿；特别不能NULL-status ACK或当前live末次claim缺失败证据提前review。

RecordObservation commit未知不继续发同claim，不补写或假定租约仍在；其真实结果可能已delivered/retry_wait/review，也可能仍leased，重启/下一轮只依持久状态调度，不靠读state授权重复发送同claim。仍leased者到期后才按预算新generation交付同业务body。接收器commit已成而ACK丢失会使同event重投，receiver回原data。ExpireFinalClaims把第20次lease已过期且仍leased的行转review_required/attempts_exhausted，即使有同代迟到ACK也保留该证据后review；不能因一条已无法在live lease内完成delivered的ACK，卡在不可claim21又永不review的leased状态。已delivered或review跳过，不重发、不第21次claim、不新capture。此规则已由根接受，现SQL在lease过期时允许review且保留immutable observation，不新增列/迁移。

## 10. 运行验收出口与SQL补项

本稿没有运行main/browser、没有新增生产代码/迁移、没有自行冻结。根已接受本稿列明的namespace、receiver预算、域派生、提前ready、after-plan恢复及单claim返回决策；整稿Go类型/manifest精确字段与catalog inventory仍待独立审阅。SQL与harness需要的增量清单如下，不能把Go入口工作都称为SQL已完成：

| 层 | 必须增量；现状/边界 |
| --- | --- |
| mock自有DDL | 部分UNIQUE `(target_kind,target_id) WHERE remaining=1`已补齐并在84项原型中由实际mock LOGIN证明第二arm回滚、消费后新arm可占目标且旧历史保留。运行repo仍须先target gate后查，第二arm写本operation持久rejected/state_conflict，不能在唯一冲突导致aborted tx后继续伪写回执。 |
| mock现有列/触发器 | 不新增receipt/request_id列或after-plan恢复列；`remaining/consumed_at`足以表示消费，原command/query immutable结果证明业务已提交。processing已有held_by_control_id/released_by_control_id及配套grant。其状态约束须继续通过，收尾消费只更新原plan的remaining/consumed_at，不覆盖control/command/query。 |
| receiver自有DDL | 现有inbox/receipt/conflict与verified_at trigger无需新“可信bool”、request_id列或应用资金外键；request_id仅HTTP包络。现有初始receipt `received_at>=verified_at`可保留，本稿“无年龄下界”是mock接收旧receipt的ParseACK规则，不删除receiver首次生成时的时序约束。 |
| marker与角色pin | mock marker已有三schema name/OID和七角色文本；receiver marker仅自身schema/OID/owner/runtime及nonce/URL。无需为了跨域否定检查让receiver保存/读取另一schema业务或秘密；完整七角色OID以component manifest对pg_catalog核验，marker已有角色文本核同名。本片不新增运行时初始化/DDL权限。 |
| marker/key/SQL fixture | SQL哈希字段只约束格式，实际harness须按§6.1唯一域算法生成真实marker/key摘要并出具对应component manifest；随机摘要fixture不证明文件绑定。更新受审SQL文件后重新算mock自有version1 SQL哈希/expected inventory及原型证据，仍不占应用迁移号、不改已应用应用SQL。 |
| typed scenario | 现SQL只校验union/归属，没有逐scenario重算全部差分。本片要求Go validator+单元oracle完整覆盖§3，不新增任意JSON可信入口；若独立审阅要求SQL重复差分，须先由SQL负责人补设计、SQL、actual-role正负例，不能默称现trigger已验证差分。 |
| 运行repo/HTTP，非DDL | Canonical/Verified私有入口、strict ACK/包络、receiver UUID、DB receive observation+5s、单claim API、独立fault消费与target session gate、before/after真实kill缝隙、每attempt清理及unknown分类均待实现。当前表/触发器没有这些Go能力。 |
| 启动/QA，非DDL | 两组件manifest严格字段、无密expected catalog inventory/精确grant矩阵、nonce/key完整集合、Guard取消与listener前失败路径及真实组件启动待实现。84项SQL原型已实际调用当前001–019 migrations.Apply、两次ProvisionFor、019角色核验、真实APP合法人工双审及真实projector，非空完整资金hash前后相等；这项fixture不替代mock/receiver进程启动/HTTP/恢复证明。 |

实施后M/Q必须交付：

- P既有测试继续通过；Canonical封装零值/owned copy、Verified绕过负例、safe receipt严格字段/重复/UTF8/NULL/时间/状态union。
- 每个typedQA scenario检查所有公共字段base与唯一差分；缺资源/缺terms/缺C、MaxInt64、year边界和未来6秒即时Verify反例；控制永久accepted/rejected幂等及异体409。
- 100并发同command、同ID异order、capture/cancel竞争、cancel tombstone、expired checkout、future-created且unexpired提前ready正例与normal capture拒绝、held/release原initial不变；query not_seen/in_progress/final及原snapshot永久不变。
- 每fault boundary实际断连接/持久plan终态/重启不复活；在业务commit前后和after独立消费前后真实kill；原ID恢复且order/transition/capture/event/query计数不增。
- 两槽callback、实际第1..20claim/退避、首发前/receiver commit后丢ACK、stale ACK/末次live/过期/失败联合、正常SIGTERM与ownership丢失；checkpoint仅辅助SQL负例，不代替20次运行调度。
- actual mock/receiver进程相同manifest kill/restart、错误OID/role/nonce/file/schema/key启动前拒绝、第二实例、代理/redirect/压缩/限流/timeout、真实24次SET ROLE跨域拒绝及所有关系/函数负权限。
- 用**实际ProvisionFor**建立应用隔离克隆并有意义正向人工/券fixture，再证明mock/receiver没有任何可写资金入口、B0前后journal/receipt/credited/source/account/outbox hash相等；普通main即使设置PAYMENTS_MODE也没有PSP/QA路由。真实供应商/支付入账B1依旧未完成。

测试报告只输出源码版本、随机无密identity、PASS/FAIL/SKIP与覆盖限制；不输出manifest/DSN/password/token/nonce/key/raw body/signature。完整退出后精确清理本次资源，不按前缀批删。全部文件稳定汇总后由根复核统一架构，子任务不执行refresh。

### 10.1 本轮§6–9冻结前复核

已核对P现有签名API、最新84项SQL/真实APP正向证据、session连接清理与重启租约语义。两项已裁决修订为：callback fault不在live leased期间新arm；第20代过期不因stale ACK而永久卡住。针对后者，复用现有专用PG随机schema原型在内存临时追加3个checkpoint回归，实际mock LOGIN证明“过期20代可保存晚ACK、不能expired→delivered、可review且ACK证据不丢”，连同原84项共87项通过并精确清理；已提交的SQL evidence仍如实为84项，此额外试验不是HTTP/真实20次调度/进程恢复验收。组件manifest内嵌key为0600秘密文件的一部分，Prepare消费在独立于claim的同一短事务完成。按这些精确修订，§6–9无剩余确认的实现阻断，可由根完成整稿冻结并分包编码；本段不是子任务自行冻结。

### 10.2 实际进程 QA 脚本合同

`go run ./scripts/payment-sandbox-qa` 是离线 QA 入口，仅从 `TEST_DATABASE_URL` 接收专用管理员 DSN；不提供 DSN 参数，不打印环境变量、子进程原始输出或协议原文。缺少 DSN 时明确 SKIP，显式 `--require-database` 时缺少配置失败。脚本先调用 `paymentsandboxqa.Create` 建真实七角色/三 schema/非空双审入账与 projector 基线，再构建独立 `cmd/moni-payment-sandbox` binary，以同一组件 manifest 启动真正 mock/receiver 子进程。只在私有临时目录保存 binary 和秘密文件；不启动普通 main、浏览器或真实供应商。

短程出口依序检查：三个 listener 就绪；mock 与 receiver 各第二实例在启动阶段拒绝；HTTP checkout 稳定重放、签名 query 的请求绑定与冻结快照；control capture 永久 accepted 重放、canonical capture/event 唯一；真实 callback 产生 receiver inbox/receipt，mock 持久 ACK 后 delivered；同 event 再投返回原 receipt data；before-command disconnect 后无业务事实，after-command/query 持久化后丢 HTTP 回复可用原 ID 恢复且事实计数不增。正常 SIGTERM 和 SIGKILL 后均使用原 manifest 启动，原命令/查询/控制/通知身份、已交付状态和计数保持。SIGKILL 均明确记录为“已确认持久化之后的进程重启”，不冒充 COMMIT 回包丢失窗口；本片没有可控 DB wire 故障设施时将该项报告为 SKIP。

可选 `--long-retry` 在另一独立随机环境执行实际二十代 callback 失败调度：保留固定 manifest callback origin，但不启动 receiver，保证每次实际网络尝试失败。按数据库原始时钟、60 秒 lease 与 1/5/30/120/300 秒退避等待，不更新 deadline/generation/attempt、不禁 trigger、不将 checkpoint 当 claim。逐代核对 immutable attempt/observation、一至二十代连续、每代唯一 delivery ID、业务 body digest 不变、后一 claim 不早于前一失败所派生 due；第二十代后必须 `review_required/attempts_exhausted`，再等待至少两次 runner 轮询确认无第二十一代。约需 78 分钟，独立最长预算 100 分钟；默认短程报告此项 SKIP，可在继续其他开发时后台执行。

2026-11-01根补充冻结生命周期出口：长程在最终无第二十一代观察完成后，必须再次确认 mock 子进程仍在运行，之后才能报告该终态 PASS；意外退出不能因数据库停在20而被当成禁止21的活跃调度证明。所有短程/长程清理须区分预期的显式 SIGTERM/SIGKILL、第二实例启动拒绝与意外退出；未被显式停止或预期拒绝的子进程提前结束，即使 exit code=0，也使验收失败。此前已启动的旧 binary/harness 保留原版本归属和真实逐代事实，但不冒充此新增最终存活 oracle 已通过，不为补丁即时重复启动一套78分钟任务。

所有路径最后先停止并 wait 全部子进程（失败路径先 SIGTERM、有界等待后 SIGKILL），关闭 QA 观察池，比较完整非空资金摘要，再由 Environment 精确删除本次 schema/角色/秘密目录及 binary 目录；不得按公共前缀批删。输出仅 allowlisted 步骤名、PASS/FAIL/SKIP、源码版本/binary 摘要和随机无密环境 identity。短程、长程、SQL checkpoint、单元/仓储测试分别记证据；本地通过不代表 B1 自动入账或真实支付商验收。

### 10.3 根冻结：双网络槽与Prepare竞争的独立测试oracle

2026-11-02，GitHub首次交付的整仓race发现旧双槽测试将历史leased总数等同于HTTP在途槽数。§9明确允许：两个ClaimDue先读到同一候选，第二个随后持该候选gate作短事务重验时，第一个Prepare可因try-gate=false返回ErrDeliveryDeferred，原claim/attempt保留而不发IO；空闲槽之后可处理其他事件。因此“历史三条leased”本身不是第三个网络槽，也不能把合法deferred改成伪失败observation、提前释放或重发同claim。生产逻辑和全部既有时间/租约/次数预算不变。

本次只冻结测试修正，不新增运行hooks或改生产pool：

1. 双网络槽测试仍调用真实callbackRunner.run、固定loopback HTTP及原签名验证。仅该测试从已认证runtime pool的Config深拷贝建立同实际LOGIN/DB/schema的新pool，MaxConns=1、MinConns=0，替换私有repository.pool并精确关闭。所有DB阶段使用这一连接，排除测试自身并行ClaimDue/Prepare gate竞争；真实Guard仍使用原runtime专用ownership连接。保留三事件、两HTTP entered屏障后严格leased=2/pending=1/attempts=2、业务advisory零锁、独立命令可完成、放行后三个ACK持久化和maxActive=2。单DB连接必须支持同时阻塞的两HTTP，也证明网络期间不持DB连接/事务。原5秒观察、5秒仓储、10秒网络和60秒lease不放宽，不用sleep或修改数据库时钟稳定测试。
2. 独立多连接真实PG用例保留标准runtime pool：先取得一个真实claim，再由另一实际连接明确持有相同callback-target session gate，调用Prepare必须返回零PreparedDelivery和ErrDeliveryDeferred；原leased/generation/attempt_count/lease、唯一attempt与零IO observation保持。释放只属于测试自己的gate后精确清理；不使用SET ROLE、fake guard或已持锁声明。该例验证第1项有意隔离的合法竞争语义，不将网络槽测试的单连接设置当作生产并发模型。

原整仓失败及修订后专项/整仓结果分别保留，不能只重试旧测试直到偶然通过。测试修订、验证和架构语义说明由根一起交付；不改已冻结SQL/ACL/provision及资金链。

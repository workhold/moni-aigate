# 022-B0 协议、持久模拟器与后续资金边界实施合同

2026-11-01。**B0-P 纯协议已实现并通过限定单元/race/fuzz验证；无网络或资金接线。B0-M/Q已由根集成人冻结独立运行合同，待实施；B1尚未冻结。** 已接受“先纯协议及独立 PostgreSQL 持久模拟器，零应用资金 DDL”的分片方向；§2–4 的 paymentproto 导出面、字节格式、严格解析、签名及查询绑定可进入实现。模拟器表、触发器和 QA 权限仍须补精确 SQL 并独立审查，M/Q实施以[b-runtime-contract](b-runtime-contract.md)与精确SQL为准；不授权B1编码。不修改 [A 精确领域合同](a-domain-contract.md)或现有迁移；旧 [B 总体合同](b-contract.md)中的 SQLite 候选由本稿的专用 PostgreSQL 方案细化，没有引入 SQLite 或其他第三方依赖。

本文已对照当前 `017_funding_vouchers.sql`、`ApplyFundingTx`、`VerifyFunding`、`VerifyLedger`、`Rebuild`、`ProvisionFor`、main 和 mailworker。A1 集成/最终架构状态由根维护，本稿不刷新统一架构。

## 1. 分片与本次冻结范围

| 分片 | 输入依赖 | 可交付内容 | 明确出口 |
| --- | --- | --- | --- |
| B0-P：协议 | A1 稳定基线；本稿 §3–5 | 纯协议值、规范编码/摘要、三种 PSP 请求、签名验证、固定 DTO/错误；无应用数据库依赖 | 规范向量、严格解析、签名窗口、query 请求/响应绑定通过；无网络/资金能力 |
| B0-M：模拟器 | B0-P；§6–10 的 QA manifest/角色/表 | 独立进程、专用 PG schema、持久 checkout/cancel/query、QA 控制与 durable notification | 命令/查询/回调和真实 kill/restart、权限否定测试通过；应用资金表零变更 |
| B0-Q：本地证明 | B0-P 可先开发；最终依赖 B0-M | 随机环境 harness、独立接收器、故障/重启/脱敏证据 | 测试只用指定 moni_test；普通 main 未改且无 PSP/控制路由；资金源、journal、receipt、credited 前后相等 |
| B1：应用资金基础 | 根确认 A1 最终出口、B0 通过；另冻结精确资金 DDL/接口 | 可信证据/订单/全局交易身份/来源及 helper/Verify/Rebuild/最小 worker grants；先内部 SQL fixture | 真实 PG 证明全部双向资金约束、溢出和锁竞争；不得注册支付 HTTP/启动支付 IO |
| B2：业务与后台 | B1 通过；B 总稿与 B1 合同冻结 | 人类 SKU/订单、验签 inbox、checkout/cancel/query worker、处理 worker、off/Recovery/Guard | 实际受限角色、崩溃与旧 ACK、来源唯一资金和正常 off 收尾通过 |
| B3：HTTP/UI 与进程验收 | B2 通过 | 精确 HTTP、安全 DTO、页面、真实 main 接线及浏览器验证 | 专用隔离环境、故障矩阵、真实主进程/PSP 重启验收；仍不等同真实支付商上线 |

B0-P、B0-Q 的协议测试/负权限 harness 可并行，B0-M 必须等 P 的字节合同冻结。B1 的评审可与 B0 实现并行，**B1 编码/应用迁移不随 B0 自动获准**。B2 后端接口冻结后，HTTP/UI 与 worker 才可按独占文件并行。根暂为 023 保留全局 migration 018；B0 仅使用 mock 自有 schema 中的 `mock_schema_migrations`，不占用/改动应用 migrations。

建议的最小可独立冻结项为 **B0-P+M+Q**。先做资金基础将迫使尚未落地的 provider 证据和取消/查询语义先进入共同账务、触发器和角色；当前协议分片可独立证明这些语义，并让后续 B1 以真实稳定证据格式为输入。B0 不创建 `sandbox_capture` source、不扩展 Money/账本科目、不改当前 funding-summary/A capabilities，也不执行模型请求。

## 2. 包边界、依赖和精确导出面

拟新增 `internal/paymentproto`（标准库）、`internal/mockpsp`（标准库+现有 pgx/uuid）、`cmd/moni-payment-sandbox` 及 `scripts/payment-sandbox-qa`。mock 不导入 billing、payments、server 或应用 migrations；harness 为权限测试调用现有应用 migrations/ProvisionFor 不算 mock 的运行依赖。应用的 `cmd/moni-aigate/main.go`、Config、router 在 B0 不接线、不识别启用 PSP 的新变量。

以下 paymentproto 名称/签名已冻结；mockpsp 导出面保留为 M 的待冻结候选。wire struct 声明顺序同时是规范编码顺序。所有 `string` 仍在每个信任入口重复 lexical 校验；Go 类型转换不构成有效值证明。

```go
// package paymentproto；不依赖 billing/pgx/Gin。
type Environment struct {
    ID, MerchantID, PayeeLabel string
}
type SignedHeaders struct {
    KeyID, DeliveryID, DeliveryTime, Signature string
}
type KeySpec struct {
    ID string
    Key []byte
    NotBefore, NotAfter time.Time // 闭区间，均规范 UTC 微秒
}
type Keyring struct { /* 私有不可变 key material 与 active ID */ }
func NewKeyring(active string, keys []KeySpec) (*Keyring, error)
func (*Keyring) ActiveID() string
func (*Keyring) HasVersion(id string) bool

func MerchantOrderID(environmentID, orderID string) (string, error)
func ParseMerchantOrderID(value string) (environmentID, orderID string, err error)
func CanonicalCheckout(CheckoutRequest) ([]byte, error)
func CanonicalCancel(CancelRequest) ([]byte, error)
func CanonicalQuery(QueryRequest) ([]byte, error)
func ParseCheckout([]byte) (CheckoutRequest, error)
func ParseCancel([]byte) (CancelRequest, error)
func ParseQuery([]byte) (QueryRequest, error)
func CanonicalNotification(Notification) ([]byte, error)
func ParseNotification([]byte) (Notification, error)
func CanonicalCommandReply(CommandReply) ([]byte, error)
func ParseCommandReply([]byte) (CommandReply, error)
func CanonicalQueryReply(QueryReply) ([]byte, error)
func ParseQueryReply([]byte) (QueryReply, error)
func RequestDigest(canonical []byte) string
func ReadSignedHeaders(http.Header) (SignedHeaders, error)

// 签名仅在 mock/QA 使用；应用以后只能在验证侧调用 Verify。
func (*Keyring) SignNotification(Environment, SignedHeaders, []byte) (SignedHeaders, error)
func (*Keyring) SignQuery(Environment, QueryRequest, SignedHeaders, []byte) (SignedHeaders, error)
type VerifiedNotification struct { /* 私有解析结果、规范摘要、验证 key ID */ }
type VerifiedQuery struct { /* 私有冻结快照及身份/参数绑定 */ }
func (*Keyring) VerifyNotification(Environment, time.Time, SignedHeaders, []byte) (VerifiedNotification, error)
func (*Keyring) VerifyQuery(Environment, QueryRequest, time.Time, SignedHeaders, []byte) (VerifiedQuery, error)
func (VerifiedNotification) Canonical() ([]byte, error) // 返回独立副本；zero value 返回错误
func (VerifiedQuery) Canonical() ([]byte, error)

// package mockpsp；只有独立 binary/QA harness 持有配置，不交给应用进程。
type RuntimeConfig struct { /* 私有；只能从已验证 manifest 构造 */ }
func LoadRuntimeConfig(ctx context.Context, manifestPath string) (RuntimeConfig, error)
type Server struct { /* 私有 store、固定 API/control listener、delivery runner */ }
func New(ctx context.Context, cfg RuntimeConfig) (*Server, error)
func (*Server) Run(context.Context) error // 正常取消为 nil，等待 callback IO 结束才返回
func (*Server) Close(context.Context) error
```

`RuntimeConfig` 不导出 DSN/token 字段，不支持 `RuntimeConfig{Trusted:true}`。`LoadRuntimeConfig` 只读取已经存在的私有配置并验证；不能创建 schema/marker/目录/nonce、迁移或使用 owner DSN。new package 的错误仅固定 `*Error{Code string}`，`Error()` 只返回码，不 Unwrap 原始 PG、URL、body、签名、key 或 token；内部处理原始 PG 错误只用于事务阶段分类。

## 3. 三个请求、命令回执及查单冻结 DTO

```go
type CheckoutRequest struct {
    SchemaVersion int    `json:"schema_version"`
    EnvironmentID string `json:"environment_id"`
    MerchantID string    `json:"merchant_id"`
    CommandID string     `json:"command_id"`
    MerchantOrderID string `json:"merchant_order_id"`
    Amount string        `json:"amount"`
    Currency string      `json:"currency"`
    CreatedAt string     `json:"created_at"`
    ExpiresAt string     `json:"expires_at"`
}
type CancelRequest struct {
    SchemaVersion int `json:"schema_version"`
    EnvironmentID string `json:"environment_id"`
    MerchantID string `json:"merchant_id"`
    CommandID string `json:"command_id"`
    MerchantOrderID string `json:"merchant_order_id"`
}
type QueryRequest struct {
    SchemaVersion int `json:"schema_version"`
    EnvironmentID string `json:"environment_id"`
    MerchantID string `json:"merchant_id"`
    QueryID string `json:"query_id"`
    CommandID string `json:"command_id"`
    MerchantOrderID string `json:"merchant_order_id"`
    CommandKind string `json:"command_kind"`
    ParameterDigest string `json:"parameter_digest"`
}
type CommandReply struct {
    SchemaVersion int `json:"schema_version"`
    EnvironmentID string `json:"environment_id"`
    MerchantID string `json:"merchant_id"`
    MerchantOrderID string `json:"merchant_order_id"`
    CommandID string `json:"command_id"`
    CommandKind string `json:"command_kind"`
    ParameterDigest string `json:"parameter_digest"`
    CommandState string `json:"command_state"`
    ProviderOrderRevision *string `json:"provider_order_revision"`
    Result *CommandResult `json:"result"`
}
type QueryReply struct {
    SchemaVersion int `json:"schema_version"`
    QueryID string `json:"query_id"`
    EnvironmentID string `json:"environment_id"`
    MerchantID string `json:"merchant_id"`
    MerchantOrderID string `json:"merchant_order_id"`
    CommandID string `json:"command_id"`
    CommandKind string `json:"command_kind"`
    ParameterDigest string `json:"parameter_digest"`
    CommandState string `json:"command_state"`
    ProviderOrderRevision *string `json:"provider_order_revision"`
    Result *CommandResult `json:"result"`
}
type CommandResult struct {
    Status string `json:"status"`
    CheckoutRef *string `json:"checkout_ref"`
    FailureCode *string `json:"failure_code"`
    Capture *CaptureFact `json:"capture"`
    ClosedAt *string `json:"closed_at"`
}
type CaptureFact struct {
    ProviderTransactionID string `json:"provider_transaction_id"`
    Amount string `json:"amount"`
    Currency string `json:"currency"`
    OccurredAt string `json:"occurred_at"`
    ProviderOrderRevision string `json:"provider_order_revision"`
}
```

QueryRequest 显式加入 `command_kind/parameter_digest` 已由根接受，细化 B 总稿的“原命令身份”：PSP 未见原命令时无法自行算出该摘要，必须冻结本次查询对原命令的预期绑定。not_seen 回应原样绑定的 kind/digest，并不证明 PSP 已接受原付款；若 PSP 已见 command，其已存 kind/digest/order 必须全部相同，否则 `query_parameter_conflict`。不采用“未知 command 时猜摘要或将 nil 当任意匹配”的实现。

schema_version 仅整数 1；UUID 小写非 nil；`merchant_order_id` 精确 `mps1.<environment UUID>.<order UUID>`，78 ASCII 字节；environment/merchant 来自启动配置且与 body 相等。merchant/payee 使用有效 UTF-8、无控制字符的 1..100/1..100 字节安全引用/标签，不允许 URL。金额为正 canonical int64 十进制微 CNY，currency 固定 CNY；created/expires UTC、0..6 小数，规范输出六微秒，expires-created 精确 900 秒。mock 不因请求后来到达而改这两个原始时刻。

请求每字段全部必填，不支持未知/重复/大小写变体、null、指数金额、非 canonical UUID、尾 JSON；8KiB 上限。Canonical 编码用专用 struct 的 `encoding/json.Marshal`，默认 HTML escaping、无缩进/尾换行；时间先规范六微秒，不用 time.Time 默认 marshal。request digest 为规范请求字节的小写 SHA-256，包含原 command/query ID。等价 JSON 空白/字段顺序先归一；改变原身份/参数即异体。

| 状态/结果 | 固定含义与 nullable |
| --- | --- |
| CommandReply completed | result 非 null；第一次正常提交的确定结果；之后同 command 同参数返回其原 body 字节 |
| CommandReply in_progress | result/revision 均 null；只由显式 QA hold 场景产生并持久，后续仍返回原接收 body；完成情况只用新 query 观察 |
| QueryReply not_seen | result/revision 均 null；不能判断迟到原请求是否还会执行 |
| QueryReply in_progress | command 已持久接收但尚无最终结果；result/revision 均 null |
| QueryReply completed | result 非 null；从 final command 和当前 order 同事务快照生成；revision 依已知 order 状态为非 null |
| result ready | checkout_ref 是非秘密 `checkout:<UUID>`；failure/capture/closed_at 均 null |
| result rejected | failure_code=checkout_rejected/order_closed/order_expired；其余 nullable 均 null |
| result closed | closed_at 非 null；其他 nullable 均 null；cancel 未见 checkout 时也可确立 tombstone |
| result captured | capture 非 null；其他 nullable 均 null；capture revision 与外层 revision 相同 |

命令的原接收 body、最终 command 结果、query 快照是三种不同事实。正常即时命令三者可同时 completed；hold 的原接收 body 永久 in_progress，而后续新 query 可看到最终 completed，不能覆盖原回执。query 同 ID 无论 not_seen/in_progress/completed 都返回永久冻结业务 body；新事实必须新 query_id。签名的 delivery/time 可更新，body 字节与 snapshot_digest 不变。

CommandReply不允许not_seen。completed cancel只允许closed/captured；completed checkout允许四种result。QueryReply在已知final command后先反映当前order的captured/closed事实，否则返回该command原final结果；不能把另一merchant_order的状态拼入快照。not_seen/in_progress时result/revision均null；所有result中不适用的nullable字段显式null，禁止额外字段或组合。request/reply的kind只checkout/cancel，digest必须64小写hex，revision为正int64字符串，checkout_ref符合安全引用规则，closed_at/capture时间均canonical UTC六微秒。

直接 checkout/cancel 回复只允许应用以后记录外部命令观察/确定投递结果，**不作为资金验证输入**。其 captured/closed 提示亦须经签名 query 或 notification 核实后进入 B2 的统一证据管线，避免把普通 HTTP200 当收款证明。B0 的最终 command 状态与 order 状态变更在一个 PG 事务内持久化。

## 4. 通知、签名与固定错误

通知沿 [B §4](b-contract.md#4-通知验签与durable-inbox) 的七类 type。公共字段顺序精确为 `schema_version,event_id,event_type,environment_id,merchant_id,order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision`，再按 type 追加：checkout.failed 的 `failure_code`、refund.* 的 `refund_id`、dispute/chargeback 的 `case_id`；禁止不属于该 type 的字段。公共 provider_transaction_id 为 null 只允许 checkout.failed；其他对应规则不放宽。规范 event digest 排除 delivery/header，但包含每个解释字段。

```go
// 内存联合值没有通用JSON直出；CanonicalNotification按EventType选专用wire struct。
type Notification struct {
    SchemaVersion int
    EventID, EventType, EnvironmentID, MerchantID, OrderID string
    ProviderTransactionID *string
    Amount, Currency, OccurredAt, ProviderOrderRevision string
    FailureCode, RefundID, CaseID *string
}
```

capture时三个专有指针均nil；checkout.failed只FailureCode非nil且值仅checkout_rejected/order_closed/order_expired；refund.*只RefundID非nil且canonical UUID；其余三类只CaseID非nil且受控引用。公共provider transaction字段始终在wire中（checkout.failed为null），不适用的专有字段在wire中禁止出现，即使传null也拒绝。Canonical/Parse都校验内存联合值与精确字段集合，不能通过直接json.Marshal(Notification)形成协议。refund/争议类型在P层只验证事实格式，P没有资金effect。

keyring 最多 128 版本、每 key 精确 32 随机字节、ID `[A-Za-z0-9_-]{1,32}`；空/重复 ID、重复 material、active 不存在、坏窗口、超限配置拒绝。key material 与 API token、QA token 独立且不相等；不复用 AUTH/AES/voucher。keyring 构造复制字节、私有不可变。持久 signing-key 表保留每个历史版本的 digest/有效窗口；启动要求本环境历史 delivery/query 引用的版本都有对应配置，不能因 retired 丢弃旧引用。

X-Moni-Key-Id/Delivery-Id/Delivery-Time/Signature 各恰一个 header 值；大小写折叠后出现第二份、逗号合并均拒绝。签名 header 时间是 canonical 无符号 Unix 秒（0..253402300799，对应1970..9999）；delivery UUID canonical；signature 小写 64hex。验证采用调用方提供的**独立 DB clock_timestamp**，绝对差 <=300 秒，且 delivery time 落在 key 的闭区间窗口。key窗口为year1..9999的非Go零值UTC微秒时刻。occurred_at <=该次 DB 时刻+5秒，业务旧 capture 不因晚接收失效。B0 harness 的接收器也用专用 DB 时钟，不能把 `time.Now` 的单元向量当跨进程时钟验收。

Sign调用的headers输入仅DeliveryID/DeliveryTime必填，Signature必须空；KeyID可空或必须等于keyring.ActiveID，输出补active KeyID及签名。Sign也验证所选key的delivery-time窗口、环境和body语法；不接受自由指定domain/算法。Verify先对exact raw body验HMAC，再strict parse/环境与请求绑定；Canonical返回的是规范业务副本，不能替换原始bytes重新验签。Query响应以body SHA覆盖完整snapshot，VerifyQuery核对request的env/merchant/query/order/command/kind/digest，零值Verified对象不能被当作可信证据。

通知签名逐字沿总合同 `moni-payment-sandbox-v1` 的 LF 串，末尾无 LF。查询签名串精确如下，所有 UUID/merchant 来自请求与启动环境，不接受 body 指定另一签名域：

```text
moni-payment-sandbox-query-v1\n
<environment_uuid>\n
<merchant_id>\n
<query_uuid>\n
<original_command_uuid>\n
<delivery_uuid>\n
<unix_seconds>\n
<lowercase_sha256_of_exact_raw_body>
```

API listener 仅 POST `/v1/checkout`、`/v1/cancel`、`/v1/query`，Bearer API token 且只接受 application/json、identity encoding、精确 query-free path。校验授权后才做 DB 工作；独立全局 20rps/burst40、最多4个入站操作、单次5秒含 pool/COMMIT，超限429/Retry-After:1；无需按凭据或用户建无界桶。成功均原始 DTO，没有应用 `data` envelope。query 成功带四个签名 header，其他成功 no-store。通知 body 上限64KiB，query/reply 上限64KiB。

| mock HTTP | 固定安全码；不会反射 token/body/PG 原文 |
| --- | --- |
| 400 | invalid_request / payment_environment_invalid |
| 401 | mock_auth_invalid；未知/错误 API token 相同；QA token 不能作 API token |
| 404/405 | not_found / method_not_allowed |
| 409 | command_parameter_conflict / query_parameter_conflict / mock_control_conflict |
| 413/415/429 | body_too_large / unsupported_media_type / rate_limited |
| 503 | mock_unavailable / mock_outcome_unknown / payment_key_unavailable |

配置/启动固定码 `mock_configuration_invalid|mock_environment_invalid|mock_state_missing|mock_role_invalid|mock_instance_unavailable`。proto 验证固定码 `invalid_request|payment_signature_invalid|payment_environment_invalid|payment_key_unavailable`。有效签名但商户/环境不符为 environment_invalid；外部未知 key/窗口/signature 均同 signature_invalid，配置应有 key 缺失才是服务不可用。

## 5. 业务状态及持久幂等规则

每个 merchant_order 用独立 advisory xact namespace +原 identity 的稳定 SHA-256 前32位，碰撞只额外串行。事务中再锁存在的 mock_orders 行；checkout/cancel/control 用 FOR NO KEY UPDATE，query 用 FOR SHARE。所有路径先同 merchant 锁再碰 command/query 唯一键，不回锁第二个 merchant_order。摘要完整身份核对不能用 hash 锁相等替代。

1. checkout 首次同事务写命令、order ready（revision1）、最终结果和原接收 body，COMMIT 确认后回应。相同 command+同参数读原 body；异体409。不同 command 试图同 merchant_order 再创建，不新建 order；返回绑定原 order 的拒绝结果 `order_closed`（已closed）或 `checkout_rejected`（已有ready/captured），不得用第二 ID 改金额或重开。
2. cancel 对未知 merchant_order 原子建立 closed tombstone（revision1、amount/created/expires 未知可空），不推断 checkout 不会迟到。后到 checkout 在保留原参数的命令回执中得到 `order_closed`，不填改 tombstone 使其重开。
3. ready→captured 或 ready→closed 只实际变更一次，各自 revision+1。captured 是该片的收款事实终态；capture 后任何 cancel 返回原 capture，不能写 closed。closed 后 capture 控制被拒绝，只有显式矛盾证据故障场景可生成 review 测试事件，不能改 canonical mock order。
4. capture 使用 mock DB 时间、冻结 order 金额和 CNY，服务端生成 provider transaction/event IDs；同 order UNIQUE 一个 canonical capture、全局 transaction identity UNIQUE。capture 与对应 immutable transition/notification event/outbox 同事务。QA capture operation 重读不新建交易/事件。关闭不会删除已发 notification 或旧 query。
5. query 先核查同 query_id 的冻结参数；同体返回已存 body，异体409；首次读 command/order 一致快照，写完整业务 response body+digest 后才回应。原命令不存在时也持久 not_seen。已知 command 但传入 kind/order/digest 不符，不创建 query 快照，409。query 不写 order/capture 或授权支付。
6. QA hold 先持久 command intent/原 in_progress body，无 order effect；ReleaseCommand 只按原 command 和原参数完成，遵守同 merchant 串行/closed/capture 规则，追加唯一 final result。原 command 接收 body不覆盖；新 query 才看到 final。
7. 模拟器事务一次5秒总预算；只在 COMMIT 前明确40P01/40001已回滚或 COMMIT 明确返回这两种回滚错误时最多重试两次。其他已知拒绝不重试；无法证明 COMMIT 结果返回 mock_outcome_unknown，不重新生成身份或补做 effect。每 attempt 清理未提交返回值，rollback 复用剩余 ctx；pgx 失败销毁连接。客户端只能原 ID 同体核查/重发，不能换 ID 宣告失败。

## 6. 持久化选型和进程隔离

| 方案 | 可复用与代价 | 本稿取舍 |
| --- | --- | --- |
| 现有 PostgreSQL 独立 schema/独立角色 | 复用 pgx、事务/唯一约束/同步 WAL、schema 隔离和真实 kill 测试；必须显式证明 mock 无应用 schema 权限；PG停机同时影响两侧，不能据此声称独立故障域 | **选用**。独立进程/逻辑 state、独立 owner/runtime DSN；共享的只是专用本机 QA PG 实例 |
| 标准库 append-only 文件日志 | 不增加依赖、可与应用 PG 独立停机；须自行完成长度/CRC、半写恢复、fsync、单 writer 锁、日志索引、原子多事实提交、查询快照和历史回放；新错误面较大 | 暂不选。若将来需要无PG mock，另立格式/恢复合同，不能本片偷偷替换 |
| SQLite | 能表达事务，但仓库没有 driver；涉及依赖/构建/full synchronous/锁与恢复审查 | 不引入；旧 B 稿“拟 SQLite”由本稿待冻结选型细化 |

PG runtime 连接精确 literal `127.0.0.1:55439/moni_test`、sslmode=disable；拒 multi-host、service/pgpass自动发现目标、DNS、localhost、隧道/其他端口/数据库。使用明确密码、本次 runtime 角色、单一 `search_path=<mock_schema>,pg_catalog`，identifier 由验证后的 manifest pin，不能读 PG 服务文件回退。连接成功还比 database OID/实际 current_database/current_user、schema OID/owner、server address/port。runtime `synchronous_commit=on`，启动验证 fsync/on、full_page_writes/on；每事务不允许降为 off。B0证明进程/事务崩溃，不冒充断电/磁盘损坏认证。

本次 suffix 为随机32位小写 UUIDhex，app schema=`payment_sandbox_<suffix>`、mock schema=`payment_mock_<suffix>`、receiver schema=`payment_receiver_<suffix>`；harness只创建/回收这三份schema与本次七个角色：app owner/app runtime/app projector、mock owner/mock runtime、receiver owner/receiver runtime。三种owner均离线NOLOGIN；各runtime使用独立LOGIN，projector使用当前独立投影角色。bootstrap用专用moni_test管理员，只在harness运行，不交给任何服务。app schema 使用当前既有 A migration/provision，仅为权限/零资金变化证明，无 B marker、B DDL 或新收款 source。mock自有`001_protocol.sql`只作用mock schema；receiver另有自有fixture SQL，两者都不进入全局migrations.Files。

mock owner 与 app owner 分开；runtime 只获 mock schema 中 §8 的精确 grants，NOINHERIT/NOSUPERUSER/NOCREATEDB/NOCREATEROLE/NOREPLICATION/NOBYPASSRLS，不属于 app/projector/owner/其他可 SET ROLE 身份。mock 不拿应用 DATABASE_URL、owner DSN或应用 token，只拿自己的 runtime DSN及已验证身份引用；同 PG database 不等于共享应用执行身份。

## 7. Manifest、nonce、重启与启动否定证明

harness 在仓库外或被忽略的 `.local` 下用 O_EXCL 创建0700私有目录、0600 manifest/secret文件；不允许 symlink、权限过宽、非本用户拥有或目录实际路径逃逸。文件不进入Git、stdout、浏览器/共享报告。harness总manifest固定包含protocol_version=1、environment UUID、state_directory UUID、PG host/port/database+OID、三schema各自的name/OID/owner、七个角色identity、merchant/payee、API/control/callback三个literal loopback URL、mock/receiver各自独立32字节nonce及本次秘密。harness为mock和receiver分别出具过滤后的0600 component manifest：只给该进程自己的runtime DSN/nonce和必要签名材料；mock仅持receiver无密identity，不得拿receiver DSN，receiver不得拿mock DSN/API/QA token。共享报告只记录无密字段和摘要，不复制manifest。

mock_environment 一行保存上述无密身份、`state_directory_id`、`SHA256("moni-mock-state-v1"+NUL+nonce)`、API/control token domain digest、created_at及protocol_version；immutable，runtime只SELECT。签名密钥表保存每版本domain digest和窗口，不保存原key；owner/harness可追加新版本、不能替换旧ID的material/窗口。mock启动从文件验证完整keyring及DB历史引用，active切换仅选已有有效版本；新版本须先由harness离线追加并同步私有配置。

`serve --manifest-file <path>` 必须消费既有证据；无 init/自动建空 state/默认目录回退。检查顺序如下；任一失败在打开listener/启动callback前退出：

1. 验文件/目录身份与mode、strict JSON/大小上限64KiB、固定值及三URL路径；API/control token和HMAC材料32字节、彼此独立。
2. 只用mock runtime DSN连接指定PG，校验实际database/OID、mock schema名/OID/owner、runtime角色、pinned search_path及独立表结构版本。标记/签名表缺失视为 state_missing，不创建。
3. 对比 environment/merchant/payee/state_directory_id/nonce digest/URL/token digests；复制目录而缺原DB/schema、复制标记到新schema、同名重建schema/database导致OID改变、清空state但保留manifest、重生成nonce、替换身份均拒绝。
4. 验 mock runtime 无 app schema USAGE/CREATE、无 app全部relation 的SELECT/INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER、无 app函数 EXECUTE；无 app/owner/projector角色成员关系或SET ROLE能力。通过pg_catalog引用app OID检查，无需读应用业务表。harness在自己的app schema显式REVOKE ALL FROM PUBLIC/mock role、函数EXECUTE FROM PUBLIC；不修改其他schema/DB全局PUBLIC grants。
5. 验mock自有schema无CREATE、历史表无UPDATE/DELETE、marker无写、lease/state仅允许列、无owner/table owner身份；验证所有表/函数属于pinned mock owner。临时表权限不构成app schema访问权，不把数据库级TEMP误称应用DDL权限。
6. 在mock runtime独立持久连接获取 `pg_try_advisory_lock(782193403, int32(mock_schema_oid))`；此二参数namespace与应用现有单int64 InstanceLockKey不同。同state仅一活动mock；连接/检测失败永久关闭listener并取消全部IO，不凭原进程缓存继续。
7. 用同schema/OID的DB时刻验证active签名窗口；已有历史命令/快照/未完成delivery引用完整。完成后才启动两个listener和callback runner。

B0 ordinary main 完全没有PSP配置/handler/worker；单改PAYMENTS_MODE或数据库中的业务enabled不能启动mock/控制器。B2以后普通serve若要处理B环境（即使正常off收尾），还必须单独完成应用侧manifest+不可变environment marker+worker角色的同等隔离证明；B0的mock marker不能替代该证明。此处是部署误用防护，不声称能对抗harness owner/主机管理员。

## 8. mock 自有追加 DDL 的精确表与权限

以下类型缩写仅用于本设计表：U=canonical非nil UUID的TEXT；Ref=1..100 UTF-8字节有效安全引用；H=64小写hex；T=TIMESTAMPTZ；M=正BIGINT；R=正BIGINT revision。nullable以`?`标记；其余NOT NULL。所有应用间身份只是已验证常量引用，**mock schema不建立任何应用schema FK、VIEW或函数依赖**。

| 表 | 冻结列/唯一约束 |
| --- | --- |
| mock_schema_migrations | version INTEGER PK、sha256 H、applied_at T；owner-only |
| mock_environment | singleton BOOLEAN PK CHECK true；environment_id U UNIQUE、state_directory_id U、database_oid OID、app_schema_name/mock_schema_name TEXT、app_schema_oid/mock_schema_oid OID、app_owner/mock_owner/mock_runtime TEXT、merchant_id Ref、payee_label TEXT、nonce_digest/api_token_digest/control_token_digest H、api_origin/control_origin/callback_url TEXT、protocol_version INTEGER CHECK1、created_at T |
| mock_signing_keys | key_id TEXT PK（版本regex）、key_digest H、not_before/not_after T、created_at T；before<=after；无rawkey |
| mock_orders | merchant_order_id TEXT PK、order_id U UNIQUE、environment_id U、merchant_id Ref、amount M?、currency TEXT CHECK CNY、created_at/expires_at T?、state ready/captured/closed、revision R、checkout_ref Ref?、closed_at T?、created_locally_at T；只有cancel tombstone的amount/created/expires/checkout_ref同时NULL；其他order四项必填，TTL900；环境/merchant复合常量FK |
| mock_commands | command_id U PK、merchant_order_id TEXT、kind checkout/cancel、parameter_digest H、canonical_parameters BYTEA<=8KiB、initial_reply BYTEA<=64KiB、initial_reply_digest H、created_at T；不以order FK要求hold前已经存在order；复合UNIQUE(command_id,merchant_order_id,kind,parameter_digest) |
| mock_command_processing | command_id U PK FK commands、state held/completed、created_at T、completed_at T?；held可由QA release一次变completed，不反向 |
| mock_command_results | command_id U PK FK commands、result_body BYTEA<=64KiB、result_digest H、provider_order_revision R、completed_at T；原始最终结果不可改 |
| mock_order_transitions | id U PK、merchant_order_id TEXT FK orders、revision R、kind ready/captured/closed、command_id U? FK commands、control_operation_id U? FK controls、occurred_at T、fact_digest H；UNIQUE(order,revision)；command/control恰一个非NULL |
| mock_captures | id U PK、merchant_order_id TEXT UNIQUE FK orders、provider_transaction_id Ref UNIQUE、amount M、currency CNY、occurred_at T、provider_order_revision R、control_operation_id U UNIQUE FK controls、created_at T；环境/merchant固定本环境，scope与order/transition一致 |
| mock_queries | query_id U PK、command_id U、merchant_order_id TEXT、command_kind checkout/cancel、expected_parameter_digest H、request_digest H、canonical_parameters BYTEA<=8KiB、snapshot_body BYTEA<=64KiB、snapshot_digest H、observed_at T；**command_id故意无FK**，允许永久not_seen；所有列不可变 |
| mock_notification_events | id U PK、event_id Ref UNIQUE、merchant_order_id TEXT、event_type受控七类、canonical_body BYTEA<=64KiB、business_digest H、control_operation_id U FK controls、canonical_capture_id U? FK captures、created_at T；typed矛盾场景没有capture FK，不伪造canonical capture |
| mock_notification_delivery | event_id U PK FK notification_events.id、state pending/retry_wait/leased/delivered/review_required、generation BIGINT>=0、attempt_count INTEGER0..20、next_attempt_at T、lease_until T?、last_error_code TEXT?、delivered_at T?；初始pending/generation0/count0 |
| mock_delivery_attempts | id U PK、event_id U FK notification_events.id、generation R、attempt_no INTEGER1..20、delivery_id U UNIQUE、delivery_time BIGINT>=0、signing_key_id FK signing_keys、body_digest H、claimed_at T、lease_until T；UNIQUE(event_id,generation)、UNIQUE(event_id,attempt_no)；不存signature |
| mock_controls | operation_id U PK、action/scenario受控enum、target_reference TEXT、parameter_digest H、canonical_parameters BYTEA<=8KiB、result_body BYTEA<=64KiB、created_at T；永久幂等，不能作为应用command |
| mock_fault_plans | control_operation_id U PK FK controls、boundary受控enum、target_kind command/query/callback、target_id TEXT、remaining INTEGER0..1、created_at T、consumed_at T?；只可从1→0一次，created内容不改 |

本表按FK循环分两段创建，再ALTER补DEFERRABLE INITIALLY DEFERRED的control/capture/transition关系；不是把FK目标改成任意字符串。order的state/revision来自不可变transition；capture→transition→order、canonical notification→capture/control在COMMIT时双向完整；不允许ready行没有ready transition、captured行没有唯一capture/transition、closed行没有closed transition、final result没有commands或processing.completed，或已completed没有final result。tombstone无checkout仍必须有cancel final result/closed transition。

immutable函数/触发器覆盖 marker、keys、commands、results、transitions、captures、queries、notification events、delivery attempts、controls的UPDATE/DELETE。state触发器防初始非零counter、revision伪造、逆向/覆盖事实、预算复位；delivery generation与attempt_count每次成功claim各+1，不可更改旧attempt。query snapshot只读，mock runtime没有可改snapshot列的权限。约束用invoker函数；不注册可由mock越权调用的SECURITY DEFINER入口。

| 角色 | 精确授权 |
| --- | --- |
| mock owner（离线） | 仅本次mock schema DDL/初始化/provision/key版本追加；不交给serve；app schema由另一owner管理 |
| mock runtime | mock schema USAGE；上述各表（**包括mock_schema_migrations，只供启动校验**）必要SELECT；commands/results/transitions/captures/queries/events/attempts/controls INSERT；orders INSERT及UPDATE(state,revision,closed_at)；processing INSERT及UPDATE(state,completed_at)；delivery INSERT及UPDATE(state,generation,attempt_count,next_attempt_at,lease_until,last_error_code,delivered_at)；fault_plans INSERT及UPDATE(remaining,consumed_at)；environment/keys/migration仅SELECT、无写；所有表无DELETE；无schemaCREATE |
| application app/worker/projector | B0对mock schema无USAGE/CREATE、无任何表/函数权限；不知道mock runtime/QA token；B0不新建应用payment worker角色 |
| QA controller | 通过独立control listener认证，不拿mock runtime连接之外的业务角色；owner/DB初始化能力只在harness离线过程 |

mock runtime本身可信执行协议；约束防实现错误和不完整事实，不宣称能防持有mock runtime全部执行权的攻击者伪造PSP事实。跨schema否定测试必须使用mock runtime实际登录（或实际SET ROLE且验证current_user），不能只读GRANT文字。

## 9. callback 的持久投递与生命周期

事件与其pending delivery在原capture/typed QA事件事务内创建。callback固定为manifest中literal loopback完整URL及唯一`/api/payments/sandbox/notifications`路径，无redirect/客户return URL；B0目标是harness独立durable接收器，不是普通应用main。HTTP transport显式Proxy=nil、无环境代理、禁止redirect、拒DNS、绑定已核对host/port、10秒总IO、响应体最多8KiB；日志仅固定码/无密ID。

1. runner每秒只按2个空闲槽claim，不预取排队；5秒短tx仅FOR UPDATE OF delivery SKIP LOCKED（至多2）后generation/count+1、60秒lease，并插入immutable delivery_attempt（新delivery UUID、DB Unix秒、当前有效key ID、固定body digest），同tx提交。
2. 网络前独立读确认state/generation/attempt/环境/实例Guard及剩余lease>=15秒；持久event body读取后摘要相等。不持数据库事务做HTTP，10秒总IO使用生命周期ctx，固定body且按本次attempt字段签名。
3. 接收器只在durable inbox COMMIT确认后回202/同event同体回200。mock只对200/202且严格安全receipt中event_id/environment匹配记delivered；其他2xx、空/坏receipt和网络未知均不当ACK。query body不含签名，但每个query响应签名可用新的delivery/time；callback每次claim也仅换delivery包络。
4. ACK/失败按event_id+leased+generation CAS，不更新原event或旧attempt；stale ACK不能覆盖新claim。退避1/5/30/120/300秒（第5次后300秒），最多20次claim，包含未IO即崩溃。最后claim未ACK lease到期转review_required/attempts_exhausted，不第21次claim、不删除事实、不称未capture。
5. 旧签名响应对mock本身无新资金作用；本地接收器永久按event_id+digest去重。丢ACK会重投同event和业务body；callback进程重启不重新生成event、capture或清预算。B2以后也可通过新query观察已存在capture，不依赖callback无限成功。
6. 正常SIGTERM取消API接收与callback IO并等待所有任务；不将已claim状态重置pending，只等原lease恢复。实例Guard使用独立至多1秒检查ctx，沿现有mailLifecycleGuard区分正常生命周期取消与真实ownership失败；后者永久停止全部DB/网络。

接收器durable receipt精确为B总稿`{data:{receipt_id,event_id,status:"received",environment:"sandbox",received_at},request_id}`。B0接收器在自己的QA receiver schema保存event/digest/固定receipt，role没有应用资金写权；它只是协议测试端，不成为B2的支付inbox实现。接收器的DB写不得落进A账务schema。

receiver owner只初始化本次receiver schema；receiver runtime只USAGE/SELECT自身marker与版本、SELECT/INSERT自身不可变inbox/等价receipt/冲突，禁止UPDATE/DELETE/CREATE；没有app/mock schema表/函数或SET ROLE权限。receiver marker单行冻结env、DBOID、receiver schemaOID/owner/runtime、state_directory_id、独立receiver nonce digest及callback URL；每次重启按自身过滤manifest重新核对，缺失不自动建空。mock runtime对receiver、receiver runtime对app/mock、app/projector对mock/receiver都要实测否定；不能把receiver持久状态塞到mock state或app账务表。具体receiver SQL/trigger和完整grant脚本仍属B0-M/Q下一轮门禁，当前表矩阵不能冒充已冻结DDL。

清理顺序固定：停app/receiver/mock全部listener与worker并等待退出→按无密ID导出脱敏证据→分别关闭三类runtime pool→只删除本次三个schema及本次七角色→最后销毁私有manifest目录。启动失败也只按已创建资源清单逆序清理，绝不通过前缀批删其他QA/评审schema；如要保留state做重启验收，三个schema/OID与私有目录一起保留，不单独重建receiver/mock。

## 10. QA 控制、故障接口与验收矩阵

control listener是第二个literal loopback端口，Bearer QA token（与API不同），只POST`/_qa/v1/commands`。普通应用/客户/admin/router不注册或代理这个入口。control全局5rps/burst10、最多2个在途、8KiB/5秒；operation UUID、typed动作和规范body永久幂等，异体409。QA不能传SQL、任意URL、tenant、journal/effect/source ID或私钥。

```go
// package mockpsp；仅QA网络面或其内部测试使用。
type ControlInput struct {
    OperationID string `json:"operation_id"`
    Action string `json:"action"`
    Scenario string `json:"scenario"`
    MerchantOrderID *string `json:"merchant_order_id"`
    CommandID *string `json:"command_id"`
    QueryID *string `json:"query_id"`
    EventID *string `json:"event_id"`
}
type ControlReply struct {
    OperationID string `json:"operation_id"`
    Status string `json:"status"` // accepted/rejected
    Reason string `json:"reason"` // accepted/state_conflict/target_not_found
    ResourceID *string `json:"resource_id"`
}
```

全部字段必填且nullable显式null；按action仅允许一种target。`capture_order`/`emit_test_event`使用MerchantOrderID；`release_command`使用CommandID；`arm_fault`按scenario分别CommandID/QueryID/EventID恰一个非null。capture正常scenario仅normal；negative-time/amount等通过emit_test_event形成明确测试证据，不改canonical mock capture。场景白名单：`capture_at_created`、`capture_before_created_1us`、`capture_before_expiry_1us`、`capture_at_expiry`、`capture_future_6s`、`amount_plus_one`、`duplicate_capture_other_transaction`、`same_revision_conflict`、`refund_unsupported`、`dispute_unsupported`。溢出amount_plus_one不可表达时拒绝，不环绕。控制只说明PSP事实/故障，绝不直接写应用余额。

故障boundary固定：`before_command_commit_disconnect_once`、`after_command_commit_drop_reply_once`、`after_query_snapshot_commit_drop_reply_once`、`hold_checkout_before_effect`、`hold_cancel_before_effect`、`before_callback_send_once`。计划持久、单次消费，重启不能重新触发已消费计划；hold按原command身份保存in_progress，后续显式release。外部harness另在可观察持久事实后实际kill mock/receiver进程，覆盖“收到但未ACK”窗口；不伪造内存error冒充真实COMMIT结果未知。需要精确barrier时只允许harness控制的私有IPC/测试hook，boundary枚举固定，不能暴露到应用handler或接受任意文件路径写入。

| B0验收 | 必须保留的具体判据 |
| --- | --- |
| P-01字节/值 | 规范向量固定JSON字段顺序/HTML escaping/六微秒/null；坏UTF-8/重复字段/尾JSON/指数金额/UUID/header重复/压缩/超限/未来与300秒端点全部有正反例 |
| P-02签名 | raw body一字节变更失败；event同体重签成功；query换env/merchant/query/original command/digest失败；未知key与坏签名同401；窗口外拒绝、保留旧版本重启/轮换通过 |
| M-01幂等 | 100并发同checkout command只有一order/transition/final；同ID改金额/order/cancel-kind409；不同ID不能二次创建；实际COMMIT成功丢回复后原ID结果恢复且事实计数不增 |
| M-02取消 | cancel先到产生tombstone，后到checkout不能重开；capture/cancel并发恰一种canonical终态；capture先提交则cancel返回原capture，绝不改closed |
| M-03query | not_seen/in_progress/ready/captured每种同query永久同body；query冻结后provider事实改变，旧ID仍旧bytes，新ID才新事实；同query换参数409；响应丢失/kill恢复原snapshot，无查询资金effect |
| M-04回调 | event与delivery同commit；接收前/提交后丢ACK/签名失效/429/坏receipt重投同业务event；重启、generation旧ACK、最后20claim崩溃保预算及证据，不新capture |
| I-01角色 | mock实际role对app每张表SELECT/INSERT/UPDATE/DELETE、函数、SET ROLE app/owner、app/mock CREATE全部拒绝；app/projector对mock同样拒绝；runtime不能改marker/key/snapshot/历史；负测试不能用owner假冒 |
| I-02身份 | DB名/OID/schema名/OID/owner/nonce/state dir/merchant/URL/token错配、复制marker、同名重建schema、state缺失、目录/文件权限/symlink、第二实例、live/非loopback/代理/redirect均拒绝且未打开listener |
| R-01真实重启 | PG committed command/capture/query/callback后kill并重启同manifest恢复；缺state不自动建空；普通main设置env也没有mock/QA入口；测试前后应用journal/receipt/credited/资金source hash相等 |
| R-02日志与报告 | 不输出nonce/DSN/password/token/raw签名/原body；报告只列随机引用、源码/命令/pass/skip/fail与限制；不改评审/生产/其他QA环境 |

B0通过只说明本地模拟PSP协议与持久状态成立，不说明应用已能接收支付/加钱。若没有真实PG/实际进程kill测试，文档不得写成“B0持久恢复已验收”。

## 11. B1 审查锚点：先保留身份，再做资金 savepoint

本节是后续评审输入，**没有冻结完整 B1 DDL/Go接口，也不占应用migration号**。本次不把B1的二十余表与账务扩展混入B0实现。以下是核读当前A代码后必须解决的具体问题：

1. 当前 `ApplyFundingTx` 只有manual/voucher两分支，非manual路径的最后半段默认走voucher；B1必须显式三分支，不能只加source enum而落到券逻辑。sandbox仍只传source kind/id，scope固定tenant+`payment-sandbox-worker`+空CommandID；金额/商户/order来自可信持久事实，不接trusted=true或通知自报tenant。
2. 当前 journal.actor/audit.actor 是TEXT且无users FK。system actor就使用固定内部文本，不创建伪user/finance角色，不把系统效应插入actor必须users FK的人类commerce_commands。worker不需要当前创建者active/owner，也不能因tenant暂停阻断有利资金收尾。
3. 全局provider transaction身份须在资金业务savepoint之前形成不可变归属，独立于成功capture source；否则账户溢出ROLLBACK TO会释放唯一交易身份，让同交易后来错误绑定别的order。还需每order永久选择首份合法capture候选，防溢出期间第二transaction替换收款证据。建议区分`provider_transaction_identity`、`order_capture_selection`（允许尚未到账/review）与`payment_captures/payment_effects`（仅成功资金source）；精确表名/FK/状态在B1冻结。
4. 资金savepoint在order根、已验证证据、交易身份/selection及tenant父锁之后；内部插入source/effect，再由helper一次ID序账户锁写journal/receipt/唯一credited；溢出回滚source/资金但保留已收事实/identity/selection并追加review。处理generation CAS失败必须回滚整个处理tx，包括identity/selection/资金；原 verified evidence在此前独立提交仍保留。
5. `commerce_accepted_resource(kind,resource_id)` 当前覆盖所有非redeem accepted，会阻止同SKU第二次active切换。B1/B2须保持A五种唯一接受语义，明确让可重复activate通过不可变SKU change事实绑定原operation；不能简单删除索引使A审批可重复。SKU初次expected0的不存在根并发、last_version/revision推进、版本与回执双向完整也须冻结。
6. query监督根不要FK到outbox可变generation的复合键；那会阻止下一次generation增长。query child引用不可变supervisor ID，保存created_generation，claim/prepare通过受控trigger+current query ID/gen核对。上一query未知复用ID；已有冻结snapshot才新建query ID；整个supervisor20claim预算不因子ID/重启重置。
7. 新sandbox清算科目 `SANDBOX_FUNDING_CLEARING` 与manual/voucher分开；BIGINT平台非正、AVAILABLE仍非负，全部科目allowlist/初始化/输出/DB CHECK一起扩展。原helper一处拥有 `AVAILABLE+A / SANDBOX_FUNDING_CLEARING-A`、journal.kind sandbox_capture、operation captureID、reference orderID和一个schema1 credited，不写第二累加事件。
8. capture资格用冻结 `created_at<=captured_at<expires_at`，不是处理时now<expires_at；合法晚通知/账户锁等待跨到期仍可入账。最终DB时刻用于journal/receipt及可信证据时间复核，不把A的券“现在没过期”条件误用于B的历史收款。
9. 完整资金双向约束须逐笔覆盖：可信notification/query→规范capture候选→全局transaction/selection→唯一source/effect→同tenant/frozen金额/CNY的receipt→唯一journal、固定operation/reference/actor、恰两条指定分录→唯一credited；从source/receipt/journal/clearing/outbox任意方向出现孤立或同额错绑均COMMIT失败。selection允许无source但必须有合法待处理/review事实，不能用此例外放开已存在source的完整性。
10. `funding_journal_valid`/`funding_fact_guard`、VerifyFunding计数/全来源检查、VerifyLedger清算检查、Rebuild的同步来源总额与旧reports-v1同次扩展；funding-summary新增sandbox分项并保持NUMERIC字符串。projector仍仅outbox/自身投影，不能获payment源/签名/订单读取权。更新owner迁移只能追加，B0 mock SQL不改变应用017。
11. B1锁图必须列出隐式FK KEY SHARE：human command插入先对actor user有FK锁；订单creator/SKU/environment父锁；worker source→order/tenant/evidence与effect/receipt/journal双向FK。system actor无user FK；账户后不能回锁order/tenant/user/SKU；未知order的证据不能为满足FK伪造tenant。全局交易唯一冲突只读既有绑定转review，不回锁另一order。Claim只锁lease行并先commit，绝不拿lease锁回order。
12. B1精确grants必须独立payment worker角色。应用app可人类command/order/outbox/inbox，但不得写capture/effect/ledger来冒充支付；worker只得已验证来源所需列/不可变资金INSERT，不得发行券/批准人工credit/改订单归属与金额；app/projector不能访问mock state。当前ProvisionFor有blanket grant，必须在后面显式撤回表级权限再列授权，并用实际restricted role走完整链路证明。

## 12. B2 启动和worker接线预留（不在B0实施）

现有main在AcquireInstance/DrainPredecessor后New Server，显式InitializeVouchers，然后设置Guard、创建独立projectionPool，启动邮件专用lifecycle和10秒财务恢复/投影loop。B2不得把10秒支付网络IO塞入这个串行恢复loop，也不得复用SMTP sender/邮件token。

待B1冻结后，建议显式 `InitializePayments(ctx)` 在Engine/listener/Ready前完成：检查PAYMENTS_MODE off/sandbox/live（live永拒）、应用sandbox marker与本机manifest/OID/nonce/所有loopback配置、签名历史版本、独立 `PAYMENT_WORKER_DATABASE_URL` current_user及最小grants；有B订单的标记环境即使off也必须保留相同证明/keys/query配置。普通无marker/off实例不注册notification、不创建PSP client/worker；有残留B事实但无marker/配置启动失败，不能静默忽略。

main对payment worker另建context/done并在pool关闭前stop+wait。网络最多2槽、inbox处理最多4槽，分别周期/5秒短事务；Guard用正常取消不误fence的独立1秒检查，真实失权停所有IO/DB。正常off只禁止新售，合法旧checkout/query/inbox/入账沿B矩阵；Recovery只收证据/有界只读query，不能新checkout/cancel/资金effect；generation/租约不等同外部network fencing。具体Options/Repository/Prepare/Record/Process签名留给B1/B2评审，B0不提前导出可直接造资金的通用Execute接口。

根裁决后只将确定冻结的B0-P/M/Q改为可实施，并记录准确文件归属/验收命令。B1审查点、B2预留与真实支付商/C退款继续标planned；统一架构由根在本次全部并行设计/代码合入后语义复核并刷新。

## 13. B0-P 实施与限定验证

2026-11-01，internal/paymentproto已实现§2–4，只有标准库依赖；普通main/config/router与应用资金无改动。严格JSON/UTF-16代理对/七通知联合值/null、规范时间/请求/摘要、不可变keyring、raw-body HMAC与query全部身份/参数绑定、zero-value Verified拒绝与Canonical副本保护已覆盖。race通过1.555s，vet通过，coverage89.4%；5秒fuzz实际6.471s，393430次输入、4workers无失败。根核读签名/keyring/值验证实现。这里只是P验证，M/Q持久状态/跨进程时钟与重启、B资金和正式支付商均未完成。

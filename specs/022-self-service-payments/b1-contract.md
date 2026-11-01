# 022-B1 应用支付与唯一入账核心合同

2026-11-01根复核：B1核心设计/SQL切片已冻结。资金身份/savepoint/双向guard、全局transaction与order锁图、独立角色及租约代次已由根和独立reviewer审定；实际LOGIN隔离SQL验证见[b1-verification](b1-verification.md)。这不授权直接复制原型RENAME上生产；021与Go第三来源按[B2冻结合同](b2-funding-contract.md)另审，B2已追加021、实现Go第三来源与三角色核验，专项通过；领域/B3/main/HTTP仍未接线。
2026-11-01，**B1核心设计/SQL已冻结；B2/B3具体接线须沿各切片合同，不据此直接授权应用迁移、普通main或真实PSP**。本文承接已实施的 [A0/A1](a-contract.md)、[A 领域合同](a-domain-contract.md)、已实施的 [B0-P](b-domain-contract.md#3-三个请求命令回执及查单冻结-dto) 及已冻结、实施中的 [M/Q 独立运行合同](b-runtime-contract.md)。不修改 P 字节、M/Q schema/角色/manifest。当前分片按 B-domain 表：B1 先内部资金 SQL，B2 业务/worker，B3 HTTP/UI/进程；下面把它们的接缝提前确定，不能把设计范围当作本轮实现范围。

配套 [b1-core.sql](b1-core.sql) 仅供真实 001–019 后的随机 schema 设计实验，采用 `p1_` 辅助函数，未占迁移号。SQL 已包含人类 change、lease FSM、资金图与角色 renderer；[B1 验证记录](b1-verification.md) 逐项区分实际通过和 B2/B3 待验收范围。公共架构由根汇总后更新，本任务不 refresh。

## 1. 核读结果与三个最小审查点

已核读 `internal/billing/{funding,funding_verify,orders,store,rebuild}.go`、`migrations/002_billing.sql`、`017_funding_vouchers.sql`、`internal/identity/store.go`、`internal/operations/operations.go` 和上述 P/运行合同。

| 审查点 | 当前实际限制与本稿选择 |
| --- | --- |
| 唯一余额效果 | `ApplyFundingTx` 先 switch 两种 source，但账户后和 receipt 前仍是 manual/else-voucher。新增第三分支必须同时改这三处，不能只加 enum；固定 sandbox clearing、system actor、receipt/credited 原子完整链。交易身份及订单首份合法 selection 在资金 savepoint 前，溢出不释放其归属 |
| 消息真实性与恢复 | P 的 `VerifiedNotification/VerifiedQuery` 仅导出 Canonical，没有 key ID getter。本稿让应用接收方法内部取得 DB 时刻并调用既有 Verify，再保存通过验证的 header key ID；不改 P、不接受 trusted=true。普通 checkout/cancel HTTP 回应始终不入账；只有签名 notification/query 进入统一持久证据管线 |
| main/角色/接口边界 | 当前 ProvisionFor 对 app blanket grant，worker 不存在；必须先撤表级权限再列授权。main 零值 off、无 marker 无 PSP 路由/出站；B1 只运行 SQL fixture，所有 HTTP/worker 方法仍是目标接口 |

本轮不新增第四个开放式设计关卡。根与独立reviewer已按这三组接受B1核心，原型验证范围见b1-verification；B2/B3 的实际接线和验收分别推进。

## 2. 环境、开关和来源边界

应用 schema 一次只属于一个 sandbox environment，新增 `payment_sandbox_environment` 一行，包含 environment UUID、merchant/payee、DB/schema OID、nonce digest、协议版本、app/worker/projector 角色文本及创建时刻。owner 在离线 QA 初始化，在线身份只读，无重置/删除接口。B0 harness 现有 application schema **没有**此 marker，本稿不让 B0 installer 静默新增它。

B2 新 QA 环境使用应用独立 0600 manifest/nonce、独立 payment-worker LOGIN，逐项校验 OID/owner/角色有效提升闭包、固定 search_path、历史 key 集合及 literal loopback 出口。复用 P 的 Environment 和三种请求，不复用 mock/receiver 的 Config 正身份，不向应用暴露 control token、mock owner/runtime DSN 或签名 HTTP 路由。M/Q 已绑定的 callback URL 不热改；应用进程验收须创建新环境，把 mock callback 从一开始固定到应用 listener，不能拿 B0 已运行 receiver 环境改目标后称可恢复。

`PAYMENTS_MODE=off|sandbox|live`，缺省 off，live 无条件启动失败。`payment_policy` 迁移初始 enabled=false/revision1，env 与 DB 相交才可新售。普通 off+无 marker：不构造 PSP client、worker、签名配置，不注册 notification，新增订单 POST 在 claim 前 409；安全 capabilities 返回 off。若已有 B 表事实/marker，则即使 mode=off，也必须有同一 marker/私密证明/历史 key/API query 配置，否则 listener 前失败。正常 off 只挡新售，旧已授权 checkout、cancel、query、inbox、合法 capture 收尾继续；Recovery/Guard 见§10。

`FundingSandboxCapture = "sandbox_capture"` 仅由已验证内部证据生成，固定 actor=`payment-sandbox-worker`、CommandID 空。不创建伪 users 行，不放进 actor 有 users FK 的 commerce_commands。钱包是模拟余额，不能转称真实现金，不提供 C 的 lot、原路退款、拒付或债务操作。

## 3. 数据合同与状态来源

所有新增业务 UUID 为非 nil 小写规范 TEXT；金额 BIGINT 正微 CNY、支付金额=钱包入账额、币种 CNY；汇总为 NUMERIC 十进制字符串。不可变历史统一 UPDATE/DELETE 拒绝。创建时刻为 DB timestamptz(6)，公开 UTC 固定六微秒；客户端不传 paid/tenant/金额/收款方/回调 URL。

| 表 | 不可变身份/主要内容与唯一性 |
| --- | --- |
| payment_skus | id、created_by/at；revision、last_version、enabled、current_version_id 是受 change 事实约束的投影，不接受客户端 revision 写值 |
| payment_sku_versions | id、sku_id、version、display_name(1..100 UTF-8 bytes)、amount=wallet_credit_amount>0、CNY、environment/merchant/payee、TTL900、publish_operation；UNIQUE(sku,version)、publish_operation |
| payment_sku_changes | operation PK、kind publish/activate、sku、expected/before/after revision、before/after active version/enable、发布 version；每次 accepted 有一份不可变 change，no-op 也有 change 但 revision 不变 |
| payment_orders | id=创建 operation、tenant、creator、sku version、冻结 display/金额/币种/环境/merchant/payee、merchant_order_id、created/expires；expires-created=900s，merchant 编码精确 P 的 `mps1.<env>.<order>`；不存任意可写 credited 状态 |
| payment_cancel_intents | order UNIQUE、operation UNIQUE、actor、created_at；同 order 仅一个已接受取消意图，不撤销已确认 capture |
| payment_command_outbox | id、order、kind checkout/cancel/query、external_command_id、original_outbox_id可空、canonical request、digest、created_at 不变；state/generation/attempt_count/next/lease/error/current_query、last_submission_unknown_at 另受受控状态机更新；generation=attempt_count，二者仅claim同时+1。每order一个 checkout/cancel；每原 mutation 一个 query supervisor（其 external_command_id/canonical_parameters 为null，只引用不可变 original_outbox，完整QueryRequest在child保存） |
| payment_command_observations | id、outbox、generation、canonical P CommandReply/digest、observed_at；仅运输/checkout 安全视图，**没有资金资格** |
| payment_query_attempts | query_id PK、supervisor_id、created_generation、mutation_generation、规范 QueryRequest/digest、created_at；UNIQUE(supervisor,created_generation)，不 FK 可变 generation；child 不重置 supervisor 20 次预算 |
| payment_inbox | id、env/merchant/event UNIQUE、canonical P Notification bytes、business_digest、verified_key_id、received_at；没有 raw signature/header/token，不可变；同 event 异体另存 payment_event_conflicts |
| payment_verified_queries | id、query_id UNIQUE、规范 P QueryReply bytes、snapshot_digest、verified_key_id、received_at；完整绑定已持久 query request。同 query 异体另存 payment_query_conflicts，不覆盖原快照 |
| payment_verified_evidence | id、notification/query 原件引用恰一个、来源类型、逻辑 order UUID、event type、capture/closed 公共规范事实、verified_at；原件到 evidence 一对一，unknown order 不建假 FK/tenant。归一字段由原件派生，不接受外部任意造额 |
| payment_evidence_processing | evidence PK、state/generation/attempt_count/next/lease/error；独立持久认领，完成前只在最终 CAS 短暂锁该行。此表统一通知/query，细化旧草案的 inbox_processing，不改 B0 receiver 表 |
| payment_provider_revisions | env/merchant/order/revision PK、事实类型/摘要、evidence；不可变，不能让同 revision 异体后写覆盖先写 |
| payment_transaction_identities | id、env/merchant/provider_transaction UNIQUE、最初合法 order/tenant/冻结金额/CNY/captured_at/revision/evidence、processing_generation；归属不可变，独立于是否成功入钱包；新增 identity 也必须在本代 processing processed/review 终态一同提交 |
| payment_order_capture_selections | order PK、identity UNIQUE、首份合法 evidence、selected_at；不可替换。允许未入账但必须有相应持久 review，不用另一 transaction 替换溢出候选 |
| payment_captures | id、order UNIQUE、identity UNIQUE、tenant/env/merchant/provider transaction、amount/wallet_credit/CNY/captured_at/revision/evidence、processing_generation；**只代表成功资金 source**，缺 receipt/effect/journal/credited 不可提交 |
| payment_effects | capture_id UNIQUE、identity UNIQUE、env/merchant/provider_transaction、effect_kind/effect_id均常量capture；全局(effect identity) UNIQUE，不能由通知指定 effect_id |
| payment_review_facts | id、逻辑order/evidence、identity/selection引用可空、固定reason、created_at；不可变、不造 source、无强制到账按钮。重复处理以(evidence,reason)去重 |

安全资金状态由同快照事实派生：无 selection=`unconfirmed`；selection+无 source=`verified`；完整 source+receipt=`credited`。credited_at 来自 receipt，不接受 order UPDATE 将状态置为 credited。review_required=有 review 事实，或 outbox/processing 预算耗尽/review 终态；预算状态在其自身行派生固定 command_attempts_exhausted/query_attempts_exhausted/processing_attempts_exhausted；非预算permanent review映射provider_protocol_invalid，不为没有 evidence 的 command 强造 review 证据。语义事实 reason 取最早 `(created_at,id COLLATE C)`，不清除旧事实；已 credited 的异常只增加 review，不逆向删资金。

creation_state 从唯一 checkout outbox 及其确定观察派生：pending/retry_wait→queued，leased→submitting，submission_unknown→同名，completed+确定 reply→confirmed/明确 rejected 则 failed_no_effect，review→review_required。checkout_state 按持久 signed closed、cancel intent、DB observed_at≥expires、确定 ready reply、其余 pending 派生，资金状态独立。原普通 CommandReply 的 captured 提示只使查单继续，不能把 funding_state 改成 verified/credited。

### 3.1 SKU、人类命令与 A 兼容

四个 B kind 为 `payment.sku.publish|payment.sku.activate|payment.order.create|payment.order.cancel`，沿 A claim/terminal/当前权限/COMMIT 未知合同；accepted 仅 resource 非 null，amount/receipt/journal均 null，rejected四关联均null。publish/activate scope=platform，order scope=tenant:<UUID>；fingerprint_key_version均null，A redeem独立HMAC逻辑保持。

必须追加扩展 commerce kind/scope/reason CHECK，原 A index `commerce_accepted_resource` 改为**仅原 A 五种非redeem kind**的同语义 partial unique，不能粗暴删掉 A 唯一接受保护。B publish resource=version，由version publish_operation唯一；activate resource=sku，由sku_change operation唯一证明，每次切换可接受；create resource=order=id，cancel resource=order，由唯一 intent.operation 证明。双向 deferred 检查 command/terminal/change/order/outbox/audit 完整，不接受“只有 accepted receipt 没有实际资源”。

SKU 首次 expected0 的不存在根，先取 SKU UUID 专用 transaction advisory gate（namespace 782193416；第二键沿运行合同 SHA256前4bytes，component=application、env、kind=sku、target=SKU UUID），再 INSERT/锁根；同 SKU 发布/切 active 都先该 gate。命令operation全局唯一 claim 在 gate 前。首次publish revision1/version1，后续publish revision+1/version+1；不自动上架。activate 只有(active_version,enabled)变化才 revision+1，no-op 不加revision，仍写本operation change。root.last_change_operation_id + change.before_operation_id 链接前后状态；change.created_xid 必须当前事务，禁止重放旧 change 更新投影。accepted 各一份 action=kind、target=operation、actor=command.actor、tenant_scope=实际租户或平台null、sanitized_diff={} 的审计；rejected 无这些效果和审计。最大BIGINT不可递增是payment_state_conflict；旧CAS是payment_revision_conflict，均持久rejected且无版本/change/audit。

## 4. 精确 Go 接口接缝（目标，尚无实现）

`internal/payments` 只依赖 paymentproto/billing/pgx，billing 不导入 payments；HTTP不直写支付/资金表。构造器内部验证 runtime/schema/角色；正常零值只能off，不允许公开 Options{Trusted:true}。

```go
// Options 私有字段；DisabledOptions零权限，LoadSandboxOptions不创建marker/目录/角色。
type Options struct { /* private validated application sandbox configuration */ }
func DisabledOptions() Options
func LoadSandboxOptions(context.Context, string) (Options, error)
func NewService(context.Context, *pgxpool.Pool, Options) (*Service, error)
func NewProcessor(context.Context, *pgxpool.Pool, Options) (*Processor, error) // 独立worker pool

type OperationReceipt = billing.VoucherOperationReceipt
type CommandMeta struct { OperationID, ActorID, TenantID, RequestID string }
type PublishSKUInput struct { CommandMeta; SKUID string; ExpectedRevision int64; DisplayName string; Amount billing.Money }
type ActivateSKUInput struct { CommandMeta; SKUID string; ExpectedRevision int64; VersionID *string; Enabled bool }
type CreateOrderInput struct { CommandMeta; SKUVersionID string }
type CancelOrderInput struct { CommandMeta; OrderID string }
type OrderResult struct { Receipt OperationReceipt; Order *OrderView; Created bool }
func (*Service) PublishSKU(context.Context, PublishSKUInput) (OperationReceipt, error)
func (*Service) ActivateSKU(context.Context, ActivateSKUInput) (OperationReceipt, error)
func (*Service) CreateOrder(context.Context, CreateOrderInput) (OrderResult, error)
func (*Service) CancelOrder(context.Context, CancelOrderInput) (OperationReceipt, error)
func (*Service) GetPolicy(context.Context, string) (PolicyView, error)
func (*Service) UpdatePolicy(context.Context, UpdatePolicyInput) (PolicyView, error)
func (*Service) PlatformCapabilities(context.Context, string) (PaymentCapabilitiesView, error)
func (*Service) WalletCapabilities(context.Context, Reader) (PaymentCapabilitiesView, error)
type Reader struct { ActorID, TenantID string }
type Page struct { Cursor string }
type PageResult[T any] struct { Data []T; NextCursor *string }
func (*Service) ListPlatformSKUs(context.Context, string, Page) (PageResult[PlatformSKUView], error)
func (*Service) GetPlatformSKU(context.Context, string, string) (PlatformSKUView, error) // actor, sku
func (*Service) ListWalletSKUs(context.Context, Reader, Page) (PageResult[SKUVersionView], error)
func (*Service) GetPlatformOrder(context.Context, string, string) (OrderView, error) // actor, order
func (*Service) GetWalletOrder(context.Context, Reader, string) (OrderView, error)
func (*Service) ListPlatformOrders(context.Context, string, Page) (PageResult[OrderView], error)
func (*Service) ListWalletOrders(context.Context, Reader, Page) (PageResult[OrderView], error)
func (*Service) ListPlatformObservations(context.Context, string, string, Page) (PageResult[ObservationView], error) // actor, order

// HTTP只做transport/admission，验证必须在此入口内；不接受已parse map/trusted flag。
type NotificationInput struct { Headers paymentproto.SignedHeaders; Body []byte }
type NotificationACK struct { ReceiptID, EventID, Status, Environment string; ReceivedAt time.Time }
func (*Service) ReceiveNotification(context.Context, NotificationInput) (NotificationACK, bool, error) // bool首次

// Claim/lease私有字段，不能由HTTP构造；一次只能认领一个空闲槽。
func (*Processor) ClaimMutation(context.Context) (MutationLease, bool, error)
func (*Processor) PrepareMutation(context.Context, MutationLease) (MutationWork, error)
func (*Processor) RecordMutation(context.Context, MutationLease, MutationResponse) error
func (*Processor) ClaimQuery(context.Context) (QueryLease, bool, error)
func (*Processor) PrepareQuery(context.Context, QueryLease) (QueryWork, error)
func (*Processor) RecordQuery(context.Context, QueryLease, paymentproto.SignedHeaders, []byte) error
func (*Processor) ClaimEvidence(context.Context) (EvidenceLease, bool, error)
type ProcessResult struct { Outcome string; CaptureID, FundingReceiptID, JournalID, ReviewReason *string }
func (*Processor) ProcessEvidence(context.Context, EvidenceLease) (ProcessResult, error)
func (*Processor) ExpireLeases(context.Context) error // 分批固定上限，无网络
```

`OperationReceipt` 直接别名既有 `billing.VoucherOperationReceipt`，不迁移/重定义A的只读字段。Reader仅当前actor/tenant，平台/钱包各用精确方法，不接受自由Platform bool/role/scope；平台命令TenantID必须空。UpdatePolicyInput={ActorID,RequestID,ExpectedRevision,Enabled}；所有新返回DTO字段严格沿B§3.1，金额规范字符串/nullable/time映射沿A，NotificationACK的Status/Environment固定received/sandbox。ProcessResult仅内部处理返回：Outcome=processed|credited|existing|review；仅credited/existing带资金引用，review仅带固定ReviewReason，普通processed四指针均null，不JSON直出。全部Safe DTO不添加raw evidence、digest、key版、provider URL。

`MutationWork` 只能是冻结的 CheckoutRequest 或 CancelRequest，包含固定出站 endpoint；`QueryWork` 是冻结 QueryRequest。HTTP client不能改ID/body，不跟随redirect、不继承proxy、10s总IO，无DB事务。Response只由内部adapter构造；record方法重新 Parse P reply并比对完整原请求身份。零值lease/工作值拒绝，无公开Execute(kind,map)或ApplyCapture(amount,tenant)接口。

现有 `billing.Store.ApplyFundingTx` 签名不变，新增 FundingSandboxCapture；FundingScope严格tenant=source tenant、actor固定、command空。helper不Begin/Commit/重试/网络，不取得order/identity/tenant等前层锁；自身唯一拥有一次全账户ID序锁、锁后DB时间、post/receipt/一个schema1 credited。资金处理父事务与最后generation CAS由 Processor 拥有。

## 5. 通知、query 与唯一capture事务

通知沿P严格raw HMAC→parse→环境；签名时钟来自Guard保护的独立DB clock_timestamp，±300s、key窗口、occurred≤now+5s沿P，不重写P规则。通过后同tx写 inbox+规范 evidence+pending processing，COMMIT确认首次202/同event同digest200；异体追加冲突事实后409。无法确认COMMIT不ACK 2xx。签名/raw header不落库、不进错误。DB只能证明持久事实结构和关联，HMAC真实性由验证入口/配置保证；共享app DB角色不是远端付款人身份，不宣称抵抗整个APP被攻陷。

query supervisor 固定引用原 mutation outbox；每次创建 immutable query child，先存 request/digest再出站。RecordQuery内部用子记录原request+当前独立DB时刻调用P.VerifyQuery，所有env/merchant/query/order/command/kind/digest相等才可保存冻结快照并归一证据。合法签名旧generation响应仍可追加原query证据；不能凭旧lease改当前outbox投影。not_seen/in_progress没有capture，无资金；新事实须新query_id，响应未知/无可信快照复用原query_id，不拿新ID绕开20次supervisor预算。

证据处理采用如下完整事务，RC、5s总预算、3s锁/5s语句限制，除明确40P01/40001已回滚可在总预算内最多2次重试外，不重试不明COMMIT：

1. 非锁定读evidence定位逻辑order；未知order/unsupported effect仅写review与最终processing CAS，不造tenant。已知order先 `FOR NO KEY UPDATE`；不得持processing/outbox行锁再取order。
2. 普通读原件并重做结构/完整请求绑定，确认是签名capture事实；校验冻结env/merchant/order/amount/CNY，`created_at<=captured_at<expires_at`，非未来异常，provider revision与已知closed部分序一致。local expiry/cancel intent、停售/off、creator撤权/tenant暂停不否认原合法收款。非法候选仅review，不抢transaction/selection。
3. 在order根下插入/重读不可变provider revision；同revision异体进review，不覆盖。按完整(env,merchant,provider_transaction)尝试 INSERT identity ON CONFLICT DO NOTHING；冲突等待后新语句只读旧完整绑定。另一order/金额/时间/revision不一致→transaction_conflict，不回锁获胜order。完整等价才复用identity。
4. 在本order根下创建/重读首合法selection。已有别的identity→duplicate_order_capture review，保留这笔合法交易identity但不替换selection。已有相同source+完整receipt→existing，仅完成本evidence处理，无第二次钱。selection曾funding_amount_overflow review则继续review，不因新event自动重开；首片无人工强制到账/重试review接口。
5. 取得真实tenant SHARE（不检查当前active/owner），然后创建**资金业务savepoint**。identity/selection、order/tenant前层锁必须在savepoint外。savepoint内预分配capture UUID并写source/effect，再调用FundingSandboxCapture分支。helper从source/selection/evidence派生金额，不接任意金额参数。
6. helper一次锁AVAILABLE及SANDBOX_FUNDING_CLEARING全ID序，锁后DB时刻复验可信事实、历史半开capture窗口和冻结金额；不要求“处理时尚未到期”。分录 `AVAILABLE +A / SANDBOX_FUNDING_CLEARING -A`，journal kind=sandbox_capture、operation=capture UUID、reference=order UUID、actor=payment-sandbox-worker、CNY；receipt source=sandbox_capture/capture UUID；schema1 credited operation=capture UUID、payload恰{"amount":"A"}。
7. 只有已识别钱包/清算BIGINT溢出可 ROLLBACK TO 资金savepoint：source/effect/journal/receipt/credited全部撤销，identity/selection保留；追加funding_amount_overflow review，无假“付款失败”。SQL/constraint/audit/outbox错误整体回滚，不能冒充语义review。
8. 成功外层追加一次payment.capture.credited安全audit；review也写固定安全review事实；最后 `UPDATE processing ... WHERE state=leased AND generation=claim AND lease_until>clock_timestamp()`。零行必须**整个事务回滚**（包括identity/selection和资金），不能仅回滚savepoint。COMMIT确认后返回处理结果；不明COMMIT不新建身份、不再发支付，恢复先读原事实。

业务selection未产生source时，提交约束必须存在对应持久review；identity无selection时必须有duplicate_order_capture review。source存在则必须有完整双向资金链，不能套用review例外。新evidence等价同capture仅完成当前processing；不同event/query不改变effect身份。

closed与capture按原发生时间和revision比较：同revision异体、capture时间≥有效closed时间或capture revision≥closed revision均review；合法较早capture可晚到入账，不因先收到closed/failed/expired拒绝。已有credited不逆转；其后矛盾closed只保留review，不删source。P/M固定captured终态仍保持，不放宽mock行为。

## 6. outbox/租约的精确恢复

mutation最多10次claim，query supervisor最多20次，evidence最多20次；claim事务原子增加attempt_count/generation并设置lease，claim后未IO就崩溃也耗预算。mutation/query 60s lease、发送前DB剩余≥15s、网络总≤10s、独立最多2槽；evidence30s lease、5s处理、最多4槽。短claim只锁自身状态行并先提交，网络不带事务。

mutation state仅 pending/retry_wait→leased→completed/submission_unknown/review_required；unknown→retry_wait/completed 仅由当前 signed query 证明；超时/断链记submission_unknown并同tx确保唯一query supervisor。signed query not_seen不能判无款；query child.mutation_generation 必须等于当前 mutation.generation，child.created_at 必须 ≥ DB生成的 last_submission_unknown_at，才可在预算内重新发送**原同ID同body**；早一代 not_seen 仍保留为证据但不授权本代补发。补发只接受未进入review的supervisor当前child的not_seen；不能跳过新in_progress/completed快照，也不能在query预算耗尽后重开变更命令。signed completed仍可确认已知事实。in_progress只继续新query观察；completed投影原事实。已有cancel intent后不再补发checkout，原checkout unknown继续query，另固定cancel命令依tombstone协议执行。确定reply的captured提示只促成签名query，不资金应用。

query：无原快照复用原query_id；有永久not_seen/in_progress快照再认领才新建child ID。supervisor.current_query_id/created_generation必须由触发器与当前claim匹配，不FK可变generation。第20次合法快照先持久，仍无终态则review；第20次崩溃lease到期直接review，不第21次。active query Prepare 只允许本 claim 的新child；原child尚无冻结快照时沿用原ID，不允许即使同代也换ID。mutation第10次崩溃lease到期→submission_unknown+command_attempts_exhausted，保证query存在，不第11次。evidence第20次崩溃lease到期→processing_review；证据永不删除。

所有claim/next/lease/unknown_at由DB clock_timestamp生成；客户端只能请求合法转移，不能指定期限。普通确定回应若in_progress或captured提示，同tx也确保唯一query supervisor。stale ACK只能追加仍有效的immutable观察/签名证据，不能更新当前generation、重开终态或重置预算。更新必须含 WHERE id + state=leased + generation + lease_until>独立DB时钟；0行即整个资金事务回滚。capture与初占identity各显式携带调用方私有lease的processing_generation，并在插入时与当前processing精确比较；不能由trigger借用当前新代次。二者各绑定该processing_generation，deferred proof要求原processing以同代processed/review结束，漏最终CAS不能只留下身份。退避1/5/30/120/300s，固定每轮扫描/更新上限100，不预取队列、不无界创建supervisor。来源资金COMMIT未知恢复只查source/receipt/processing原ID；不再发另一checkout/cancel，不触发模型生成。

## 7. 真实锁图及隐式 FK

| 路径 | 顺序 |
| --- | --- |
| SKU publish/activate | 新commerce claim→SKU gate→SKU NO KEY UPDATE（或首次INSERT）→admin user SHARE→CAS/DB时刻→版本/change/terminal/audit；actor FK KEY SHARE在claim时取得，不升级user UPDATE |
| policy PUT | admin user SHARE→policy UPDATE；不取SKU/order/tenant/accounts |
| create order | claim→SKU SHARE→policy SHARE→tenant SHARE→membership SHARE→user SHARE→新order+checkout outbox+audit+terminal |
| cancel | claim→order NO KEY UPDATE→tenant SHARE→membership SHARE→user SHARE→唯一intent+cancel outbox+audit+terminal；不回SKU/policy |
| evidence资金处理 | 非锁定定位→order NO KEY UPDATE→provider revision/transaction identity唯一性→selection→tenant SHARE→资金savepoint→全accounts ID序→source/journal/receipt/credited/audit→最后processing generation CAS |
| command结果/query保存 | 非锁定定位→order NO KEY UPDATE→immutable observation/evidence→最后outbox generation CAS；旧generation只保留证据不更改当前状态 |
| claim/expire | 仅自己的lease行，固定上限、SKIP LOCKED；commit后再进入order路径 |

订单根使用 **NO KEY UPDATE**，不能用UPDATE：identity/source/effect的FK KEY SHARE与之相容，避免全局transaction冲突者为了FK间接反锁另一order。identity/selection/evidence不可变，FK只取已知父的KEY SHARE；capture前order/tenant已持有，账户后不新取user/SKU/另一order。system actor无users FK。订单creator→users、订单tenant、SKU version/environment FK在新订单父锁之后；command actor FK仍如A存在，不能在之后升级user UPDATE。

人工ApproveCredit仍credit→maker/checker SHARE→tenant SHARE→accounts；券batch/policy→tenant/member/user→accounts；Reserve/finish/refund保留现有顺序，与B只在tenant SHARE和账户ID层汇合。Login持user UPDATE后插session tenant FK KEY SHARE，B tenant SHARE与其相容。worker资金不取current user/member，故不会把payment order接到login/当前权限环。根实施时须实际PG交叉证明，文字不是无死锁认证。

## 8. 数据库完整性、最小角色与核验

配套SQL先证明source/effect/selection/identity/evidence/receipt/journal/credited和两条指定分录逐笔一致，任意同额错绑、孤立source/receipt/journal/clearing/credited/audit与未完成processing均拒。资金audit唯一(action=payment.capture.credited,target=capture)，actor固定、tenant_scope精准、request_id空、payload仅规范amount字符串；worker不能追加其他action，app不能追加该资金action。原A的legacy funding证明保持原分支，sandbox不能落到voucher的ELSE。只在隔离原型中保留旧A函数副本再用PL/pgSQL按journal kind分支；不能用一个内联SQL OR让worker为sandbox校验被迫读取voucher/manual表。生产追加迁移须保留原`funding_journal_valid` OID/签名，并显式扩展fact guard与清算检查，不把原型函数重命名直接复制上线。

| 身份 | 精确允许与拒绝 |
| --- | --- |
| app HTTP | 读marker/policy/SKU/order/安全观察；INSERT B人类commerce command/terminal、SKU version/change、order、cancel intent、checkout/cancel outbox、notification inbox/conflict/evidence/processing；policy仅enabled、SKU投影仅受控列。**无capture/effect/identity/selection、query证据、支付资金写权**；现有A资金权不删除，但B触发器按source+caller分离 |
| payment worker | SELECT自身marker、orders/版本、不可变证据、当前payment状态、相关tenant/ledger/receipt/journal/outbox；INSERT query supervisor/child/verified query/conflict/normalized evidence/processing/revision/identity/selection/capture/effect/review/安全audit/journal/lines/receipt/credited；UPDATE仅payment lease白名单、ledger balance。账户只能初始化AVAILABLE或SANDBOX_FUNDING_CLEARING且余额0；只能给sandbox journal追加分录；worker账户/分录提交时双向核对balance=该账户全历史分录SUM，拒无流水改余额。无users/membership/session/上游凭据/券/人工credit/sku/order金额/归属/marker写权，无DDL |
| worker锁权限 | PostgreSQL锁定SELECT要求相应UPDATE权限：orders/tenant可只给UPDATE(id)用于FOR NO KEY UPDATE/SHARE，但BEFORE UPDATE必须对已固定worker身份无条件拒真实写；不能因“锁用”给worker任意tenant.status/name写权限。所有guard按marker固定role、effective grants和SET ROLE提升闭包验收，不能只查直接ACL |
| projector | 保留原projection_outbox SELECT及自身receipt/totals写；无任何payment源/订单/inbox/key版本读写、无新资金或修正权限 |
| owner/harness | 离线DDL/marker/role provisioning/清理；所有在线用户无role membership提升、superuser/bypass/owner/schemaCREATE、function替换或禁trigger能力 |

worker与app分别固定current_user=session_user，登录角色不能SET到其他角色；触发器invoker固定actual schema/pg_catalog/pg_temp，所有未信任表名/role名用identifier参数离线渲染。[精确ACL模板](b1-permissions.sql)在ProvisionFor blanket后对全部新表REVOKE ALL再逐表/列授予，不给worker `ALL TABLES` 或公共function全执行。app不可用sandbox source写receipt/credited，worker不可用manual/voucher source造款，需在source/receipt/journal/outbox入口均核验caller/source类别；投影只读永远不走资金写函数。

`VerifyFunding` 同次追加sandbox逐笔检查、Missing/Orphan/Source/Journal/Outbox/Clearing计数及新增PaymentIdentityMismatches/SelectionMismatches；Balanced包含所有计数。tenant核验只比自身journal clearing分录，不拿subtotal比平台余额。`VerifyLedger`允许且核验SANDBOX_FUNDING_CLEARING；Rebuild在原维护事务使用同完整核验器、source汇总比schema1 credited后才切active；旧reports-v1累计总入钱包不变，无第二累计事件。

funding-summary显式增加sandbox_capture_amount字符串，total=manual+voucher+sandbox；receipt source筛选增加sandbox_capture，commerce capabilities只有B3实际接线后增加sandbox状态。保留原钱包六字段，total_credited继续receipt SUM，不称真实现金收入。

## 9. HTTP 与安全 DTO 接线合同

端点/body/当前角色/错误沿[B§3及§3.1](b-contract.md#3-http角色与安全dto)不扩自由参数：publish/active/policy仅admin；平台SKU/orders/observations/capabilities active admin/finance读；wallet商品/订单读owner/billing_viewer，create/cancel仅当前owner；API Key拒。所有人类mutation CSRF+近期再认证+已启用MFA验证，MFA enrollment仍可选，不新增强制MFA。5s总budget、8KiB严格body、固定50/读51、cursor绑定当前scope/kind、不收limit/offset/未知query；页面每次当前权限复验，不缓存session role授权。

普通off create POST含同operation重放在claim前409；已存在operation只GET可查，GET404不解除未知。不明COMMIT为503 commerce_outcome_unknown，禁止自动换ID重试。DB policy off为200 rejected payments_disabled；sku不可售统一payment_sku_unavailable。cancel首次accepted只表示意图，不是关闭/未收款。所有accepted B回执四关联中仅resource非null；create首次202/重读200加当前安全order视图；rejected不带order键。其他B mutation仅data receipt，publish首次201，accepted/rejected重读200。

通知独立10rps/burst20/最多4在途，64KiB、identity encoding、application/json、P精确签名header；成功ACK严格沿B0运行合同§5 `data{receipt_id,event_id,status=received,environment=sandbox,received_at}`+request_id，首次202/等价200。仅durable收到，不表示到账。通知错误用固定安全{error:{code,message},request_id}应用包络，不采用独立receiver的无message包络；返回给mock的成功ACK字节形状保持兼容。未知key与错误signature统一401，环境400，异event冲突持久后409，未提交/不明提交503。

所有GET/响应no-store，wire时间UTC6、Money字符串、nullable显式null、空页[]/next_cursor=null；不输出rawbody/signature/digest/key版本/nonce/API或QA token/任意支付URL。checkout_ref仅P安全非秘密引用。OrderView中funding_receipt_id/journal_id/credited_at只能来自完整资金链，observed_at同一DB快照；页面始终“本地模拟，无真实收款”，无simulate/pay-success/force-credit按钮。

## 10. 开关、故障与最小验收出口

| 动作 | 正常off/停售/创建者失权 | Recovery | Guard失效 |
| --- | --- | --- | --- |
| 新订单/新售授权 | 拒绝 | 拒绝且不claim | 全停 |
| 既有checkout/cancel出站 | 原授权原ID可完成；cancel仍需当前owner创建意图 | 暂停变更IO | 全部IO/DB停止 |
| notification/query收集 | 保留验证配置/旧keys，继续durable证据 | 可收集/ACK及只读query，不资金 | 不DB/不ACK成功 |
| 已验证capture应用 | 继续，不重查当前owner/tenant active/SKU | 禁新资金effect | 全停 |
| 元数据policy/SKU | 可维护/停用；env off不得开启售卖 | 拒全部写 | 全停 |
| 当前安全只读 | 当前auth仍复验 | 继续并标事故冻结 | 拒 |

B2只在Engine/listener/Ready前显式InitializePayments，普通main off路径零网络；独立payment-worker context/done，网络2槽/证据4槽，不塞入原10s财务恢复loop，不复用邮件worker/token；ownership丢失停止全部DB/网络，正常SIGTERM取消并等待后关池，不误记fenced。恢复旧备份必须沿现有离库检查点/独立mock持久记录+双人核对，不因清Recovery变量就重放资金/checkout。

首轮冻结审查只需三个出口，下列测试以实际受限角色+001–019+随机schema为目标，未跑一律标待验收：

1. **资金与锁序**：同交易不同event/query并发、同order第二交易、同交易跨order、同额错绑；overflow保identity/selection且无source/钱；失败点source/effect/journal/receipt/credited/audit/最终generation CAS；所有方向孤立资金/clearing拒；与manual/voucher/Reserve/refund/login交叉，合法晚capture锁等待跨到期继续；VerifyFunding/Rebuild全来源。
2. **消息与恢复**：严格P复用、query旧ID冻结/未知复用、通知durable ACK/异体冲突、原mutation第10/query第20/evidence第20崩溃耗尽、stale ACK不改新generation；已收事实COMMIT未知仅查原ID；off与Recovery/Guard及暂停tenant收尾，不调用模型或真实PSP。
3. **边界与合同**：actual app/worker/projector ACL+effective SET提升、caller按source分离、普通main/off无路由/网络、marker失配启动失败、角色撤销两先后、四kind精确receipt/CAS/JSON/DTO/分页/限流/unknown404。B1只跑SQL/fixture，B2/B3才跑独立进程与浏览器。

本工作稿现已将人类命令/change投影、outbox/query/processing FSM、资金图、精确角色模板落为可执行设计，并通过受限LOGIN最小业务正负例。此范围可交由根按§1三组审查决定冻结，不因 B2/B3 尚未接线而重新开放字段。全范围并发/HTTP/进程/商业验收仍按各分片推进，不能用本地SQL原型替代生产完成。

### 10.1 本轮实际设计实验记录

见 [b1-verification.md](b1-verification.md)：真实001–019、随机schema、实际三个独立受限LOGIN、P签名校验后归一、现有A人工充值回归、完整B资金/缺片反例、overflow留身份、真实10/20次lease耗尽均已通过。owner仅安装DDL/seed/配置角色与测试时钟列；业务断言未用owner或SET ROLE代替。每轮finally删除schema和LOGIN角色，无真实PSP/HTTP/main/浏览器接线。

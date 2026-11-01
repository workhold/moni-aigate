# 022-B3 HTTP、客户界面与启动接线候选

2026-11-01，**D1–D5根已审定；D6运行证明/Processor接口仍待精确冻结，整体未实现，不授权main/worker接线**。本稿收口B/B1已有HTTP、界面和构造接缝；不重新定义支付状态机、P协议或M/Q运行环境。局部裁决见§9，引用的历史合同未因本稿自动变成已实现能力。

前置：[B 合同](b-contract.md)、[B1 核心](b1-contract.md)、[B1 SQL 候选](b1-core.sql)、[B2 共同账务](b2-funding-contract.md)、[A HTTP/资金合同](a-contract.md)、[P 领域合同](b-domain-contract.md)、[M/Q 运行合同](b-runtime-contract.md)，以及 [HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)。B3 不修改上述文件，不刷新统一架构清单。

## 1. 已有接缝与范围

当前 [commerce.ts](../../web/src/commerce.ts) 的能力只有 `payments_mode:"off"`、五字段资金摘要和两来源回执；[commerce-state.ts](../../web/src/commerce-state.ts) 的 journal 是 A 的 sessionStorage 协议。二者不能只放宽 TypeScript union 就宣称支持 B。B3 须同版交付 HTTP DTO、严格解析器、页面、资金筛选、持久原操作核查和测试。

B3 沿既有路由，不新增 `payment-operations`：

| 路由 | 当前身份与用途 |
| --- | --- |
| `POST /api/platform/payment-skus` | active admin；发布不可变版本 |
| `PUT /api/platform/payment-skus/{sku_id}/active` | active admin；CAS 切当前版本/上下架 |
| `GET /api/platform/payment-skus`、`/{id}` | active admin/finance_operator/finance_approver；只读商品 |
| `GET/PUT /api/platform/payment-policy` | 仅 active admin；独立 policy CAS |
| `GET /api/platform/payment-capabilities` | 上述三平台角色；不扩大 policy 读权 |
| `GET /api/wallet/payment-capabilities`、`/payment-skus` | 当前 active owner/billing_viewer；可售列表只含当前启用版本 |
| `POST /api/wallet/payment-orders`、`/{id}/cancel` | 当前 active owner；创建/取消意图 |
| `GET /api/wallet/payment-orders`、`/{id}` | 当前 active owner/billing_viewer；当前 tenant，跨 tenant 404 |
| `GET /api/platform/payment-orders`、`/{id}`、`/{id}/observations` | 上述三平台角色；只读安全订单/证据摘要 |
| `GET /api/wallet/commerce-operations/{id}` | 复用 A，扩 B 种类；当前 tenant/actor/权限重新核验 |
| `GET /api/platform/commerce-operations/{id}` | 复用 A，扩 B 种类；当前平台权限重新核验 |
| `POST /api/payments/sandbox/notifications` | 仅隔离证明环境注册；P 签名身份，不以 Cookie/CSRF/admin 替代 |

API Key/member 不能借支付能力进入钱包商业读取或写入。服务端按当前 DB 授权；页面按钮隐藏和旧 session role 均不构成权限。平台列表不能强制到账、重绑 tenant 或重放 PSP 命令。

## 2. HTTP 与字节合同

继承 B/B1：人类写要求 CSRF、10 分钟近期再认证；仅账号已启用 MFA 时执行现有 MFA，不强制 enrollment。既有 metadata/session、Guard、ready/Recovery 门禁仍成立，支付领域准入不替代它们。

| 边界 | 精确行为 |
| --- | --- |
| 人类写 | 独立 global 10rps/burst20、actor+scope 1rps/burst2、全局最多 4 在途；4096 桶、idle10min 驱逐；5s 总 context；body ≤8192 bytes |
| 匿名通知 | 独立 global 10rps/burst20、最多 4 在途；先取得许可再访问 DB 时钟/验签；5s 总 context；body ≤65536 bytes。不能复制 Q receiver 的 20/40 配额 |
| JSON | `application/json`（可带 `charset=utf-8`）、identity encoding；拒压缩、非法 UTF-8、重复/未知/大小写别名字段、第二个值和尾随 JSON；不将服务端原文反射为错误 |
| GET | 无 body；非列表无 query；列表仅一个 `cursor`，资金来源列表另允许一个 `source_kind`。未知或重复 query 400 |
| 响应 | 全部 `Cache-Control: no-store`；单对象 `{data,request_id}`，列表 `{data,next_cursor,request_id}`；空列表 `[]`，末页显式 `null` |
| 建议新增客户端上限（D4） | 完整响应 ≤128KiB；通知 ACK ≤8192 bytes。先读有界原始文本，再拒重复 JSON/严格解析；超限/失真响应按结果未知处理，不清除待核查记录 |

分页固定 50、读 51，不接受客户端 limit。一般列表按 `(created_at DESC,id COLLATE C DESC)`；观察列表见 §3。沿 A 的 v1 cursor 位置算法：规范 RawURLBase64 的精确 `{v,created_at,id,filter}`，raw≤1024/decoded≤768 bytes，v=1，UTC6+UUID；`filter=lowerhex(SHA256(json.Marshal([]string{scope,kind,filter})))`。它不是签名或权限凭证；每页重新校验当前 scope。B 末页沿 A 为 null，不采用 023 的空串。是否抽出 A 私有纯 codec 由根决定，不顺带改变 A 格式。

静态格式错误 400 `invalid_request`，游标错误 `invalid_cursor`；当前失权 403 `forbidden`，缺失/跨 tenant 404 `not_found`；异操作 409 `commerce_operation_conflict`；policy CAS 409 `payment_policy_conflict`；runtime off 新售 409 `payments_runtime_disabled`。413 `body_too_large`；429 `rate_limited` 且 `Retry-After: 1`，均不持久 command。命令 infra 503 `commerce_unavailable`，提交未知 503 `commerce_outcome_unknown`；通知对应 `payment_unavailable`/`payment_outcome_unknown`。签名/环境/冲突码见 §6。所有应用错误固定 `{error:{code,message},request_id}`；既有 CSRF/MFA/再认证/Guard/Recovery 安全码继续沿原 middleware，不借 substring 匹配 SQL/错误文本。

## 3. 精确 JSON、空值和快照

下列 schema 表达精确字段集；全部字段必有，只有明确 nullable 项可 null，拒未知字段、`undefined`/缺字段和错误类型。UUID 是小写非 nil 规范串；Money/positive revision 是 `1..9223372036854775807` 规范十进制字符串，expected_revision 首次建 SKU 可为 `"0"`。累计金额使用非负 Numeric100 字符串和 BigInt，不走 JS Number/float/int64 累加。UTC6 为真实公历 year0001..9999、固定六位微秒和 Z。SafeRef 沿 P；名称 1..100 UTF-8 bytes、无控制字符。

```text
PolicyView = {enabled:bool,revision:PositiveRevision,
              runtime_allowed:bool,effective_enabled:bool}
PaymentCapabilitiesView = {mode:"off"|"sandbox",environment:"sandbox"|null,
                           payments_enabled:bool,local_simulation:bool}
SKUVersionView = {id:UUID,sku_id:UUID,version:PositiveRevision,
 display_name:Text100,amount:Money,wallet_credit_amount:Money,currency:"CNY",
 environment:"sandbox",merchant_id:SafeRef,payee_label:SafeText,
 checkout_ttl_seconds:900,created_at:UTC6}
PlatformSKUView = {id:UUID,revision:PositiveRevision,enabled:bool,
 current_version_id:UUID|null,current_version:SKUVersionView|null,created_at:UTC6}
OrderView = {id:UUID,tenant_id:UUID,creator:UUID,sku_version_id:UUID,
 display_name:Text100,amount:Money,wallet_credit_amount:Money,currency:"CNY",
 environment:"sandbox",merchant_id:SafeRef,payee_label:SafeText,
 created_at:UTC6,expires_at:UTC6,checkout_state:CheckoutState,
 creation_state:CreationState,checkout_ref:SafeRef|null,funding_state:FundingState,
 review_required:bool,review_reason:ReviewReason|null,
 funding_receipt_id:UUID|null,journal_id:UUID|null,credited_at:UTC6|null,observed_at:UTC6}
ObservationView = {id:UUID,order_id:UUID,kind:ObservationKind,
 status:ObservationStatus,reason:ReviewReason|null,
 occurred_at:UTC6|null,received_at:UTC6}
```

`amount=wallet_credit_amount>0`；商户和收款标签只来自 marker，不由客户表单输入。`expires_at-created_at=900s`。`checkout_ref` 仅非秘密引用，绝不是 URL/付款凭据；不把它变成可点击支付链接。merchant/payee 的安全验证沿 P/环境合同，不擅自收紧既有合法值。

| 对象/状态 | 关联与 null 矩阵 |
| --- | --- |
| PlatformSKU | current_version_id/current_version 同时 null 或同时非 null；非 null 时 id、sku_id 必须相互绑定。enabled=true 必须非 null |
| Order creation | `queued|submitting|submission_unknown|confirmed|failed_no_effect|review_required`；按 B1 命令事实派生，不以 HTTP202 推断 confirmed |
| Order checkout | `pending|ready|cancel_requested|expired|closed`；按 B1 事实优先级/DB observed_at 派生。cancel/expired/closed 均不否认原窗口合法 capture |
| Order funding unconfirmed | 尚无 capture selection；三项资金引用全部 null |
| Order funding verified | 已有 selection、尚无完整 source/receipt；三项资金引用全部 null |
| Order funding credited | 同一 DB 快照验证完整 capture→source→receipt→journal，三项全部非 null，不能按零散 LEFT JOIN 猜到账 |
| Order review | false 时 reason=null；true 时固定原因非 null，按 B1 最早 `(created_at,id C)` 的语义 review/既定预算原因派生；可与 credited 共存，不反向撤销资金 |

ReviewReason 仅沿 B 的固定 14 项：`unknown_order,capture_outside_window,provider_time_invalid,provider_revision_conflict,amount_mismatch,currency_mismatch,transaction_conflict,duplicate_order_capture,funding_amount_overflow,unsupported_effect_review,processing_attempts_exhausted,command_attempts_exhausted,query_attempts_exhausted,provider_protocol_invalid`。UI 固定词典展示，未知 enum 是失真响应，不直接显示服务器输入。

**Observation 映射建议（D3）**：一条 `payment_verified_evidence` 对应一条观察，避免多表 UNION 的跨来源 ID 碰撞；id=evidence.id，order_id=逻辑订单 UUID；kind=`capture|closed|observation|unsupported`；status=`pending|retry_wait|leased|processed|processing_review`，取同 evidence 的 processing.state。reason 取该 evidence 最早 review_fact；无事实且 processing_review 时按 budget/permanent 分别为 processing_attempts_exhausted/provider_protocol_invalid；其余 null。occurred_at=evidence.happened_at（可 null），received_at=evidence.verified_at。排序 `(verified_at DESC,id C DESC)`，cursor.created_at 承载 verified_at；query 的 not_seen/in_progress 可为 kind=observation、occurred_at=null。不拼接 raw 命令/事件冲突表；若需额外排障维度应另设计安全 DTO。

**平台观察的 logical order 边界（根2026-11-01裁决）**：`GET /api/platform/payment-orders/:id/observations` 的 `:id` 是规范非 nil logical order UUID；无需对应 `payment_orders` 行。active admin/finance_operator/finance_approver 可按该 UUID 读取已有 evidence，包含 `unknown_order` 的观察；没有 evidence 返回成功空数组与 null cursor。该路由不通过缺少订单行把 unknown_order 隐藏为404，仍保持固定分页/游标过滤绑定/安全 DTO，不新增钱包或租户观察查询权限。

**Capabilities 映射建议（D3）**：普通 off 无 marker/历史时 `{mode:"off",environment:null,payments_enabled:false,local_simulation:false}`；sandbox 或带历史的 off 经同环境证明后，environment="sandbox"、local_simulation=true，payments_enabled=`runtime_allowed&&policy.enabled`。off+历史仍 mode=off、payments_enabled=false；这两个字段描述环境而非付款成功。runtime_allowed 仅 sandbox+证明，不承诺 Recovery/ready 可写；DB/证明失败 503，不能假装 off/空商品。

## 4. 四种人类命令与原操作

输入精确沿 B，不添加字段：

```text
publish {operation_id,sku_id,expected_revision,display_name,amount}
activate {operation_id,expected_revision,version_id:UUID|null,enabled:bool}
create   {operation_id,sku_version_id}
cancel   {operation_id}
policy   {expected_revision,enabled:bool}   // 没有 operation_id
```

OperationReceipt 直接沿 A 的 11 字段：`operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at`。DB 的 tenant scope 为 `tenant:<UUID>`，wire 沿 A 去前缀为 tenant UUID；平台为 `platform`，不可出现第二套 scope 编码。

| kind | accepted resource_id | 仅允许的 rejected reason |
| --- | --- | --- |
| payment.sku.publish | 新 version UUID | payment_revision_conflict/payment_state_conflict |
| payment.sku.activate | SKU UUID | payment_revision_conflict/payment_state_conflict |
| payment.order.create | order UUID，且等于 operation_id | payments_disabled/payment_sku_unavailable |
| payment.order.cancel | order UUID | payment_state_conflict |

accepted reason=accepted，仅 resource_id 非 null；其余三关联均 null。rejected 四关联全 null，不创建商品/订单/外部指令/资金；基础设施错误不能写 terminal rejected。HTTP200 可能 rejected，必须解析 status。publish 首次 accepted201/replay200；create 首次 accepted202/replay200/rejected200；activate/cancel 沿 B 为200。create accepted 精确 `{data:OperationReceipt,order:OrderView,request_id}`，order 是**当前**安全视图；rejected 精确 `{data,request_id}`，禁止 order 键；其他 mutation 精确 `{data,request_id}`。

同 operation 同体在当前权限重新核验后返回原 terminal；异 actor/scope/kind/target/body 为409。缺资源/失权整体回滚。off 的新订单 POST **包括原 ID 重放**均 claim 前409；历史 receipt 仅走现有 GET。GET receipt 不带 order，获取订单须另 GET；并行结果仍受当前身份 epoch 约束。

receipt 不输出 target/input_fingerprint。客户端只能核 operation/kind/actor/scope 和可推导 resource；create resource=operation_id、activate/cancel resource=原目标。publish resource 是新版本，而现有 SKU GET 仅 current_version，不能声称从它验证任意历史版本归属。服务端 claim 摘要绑定是权威，本稿不添加历史版本或重复 operations 路由。

**接缝 D1**：B1 `PublishSKU` 目前只返回 receipt，HTTP 无法证明首次201/replay200。建议内部增加下列返回；Created 不进入 wire，不由 HTTP 猜测：

```go
type PublishSKUResult struct { Receipt OperationReceipt; Created bool }
func (*Service) PublishSKU(context.Context, PublishSKUInput) (PublishSKUResult, error)
```

**Policy 独立 CAS（D2）**：PUT 仍不创建 commerce command。实际 tuple 变化才 revision+1，同值 no-op，旧 revision409；off 下 enabled=true409。网络/5xx/失真后只 GET 当前 policy，界面明确“当前配置不证明原请求是否生效”，不自动重发；同值或相邻 revision 也不能归因原请求。原操作 journal 不得伪造 policy operation ID。恢复编辑/显式新意图的交互须根决定；冻结前不实现以 GET 成功自动解锁或重试的流程。

## 5. UI、身份 epoch 与持久 journal 候选（D4）

钱包展示可售 SKU→创建订单→订单/安全 checkout 引用→取消意图→当前资金状态；billing_viewer 全部只读。平台三角色读安全商品/订单/观察；admin 才有发布、上下架、开关。所有支付区域持续标注 **“本地模拟，无真实收款”**；credited 显示“模拟钱包到账”。没有模拟成功、强制到账、review 重放、任意 callback/PSP URL 输入按钮；本片没有真实支付二维码或真实结算承诺。

建议 B 独立 IndexedDB journal，不修改 A 券秘密/sessionStorage 语义；利用原子 readwrite transaction 及唯一 `(actor,scope,kind,target_id)` 保持同目标单 pending。仅持久精确安全元数据 `{operation_id,kind,scope,target_id,created_at}`，actor 由存储命名空间绑定；不存请求 body、金额、名称、token、signature、digest、nonce 或付款材料。publish/activate target=SKU UUID，create target=选定 SKU version UUID，cancel target=order UUID。每 actor+scope 最多128项；损坏、满额、不可用或事务 abort 均在发送前阻断，不能降级成内存/新 ID。通知其他 tab 只作刷新提示，DB 事务才是并发权威。

先提交 journal 再发 HTTP。journal 提交后即使请求尚未真正发出而页面崩溃，也保留 unknown，不猜未发送。状态 `idle→journaled→submitting→settled|unknown`；本地12s deadline/abort/网络/408/5xx/失真/晚响应均 unknown；unknown 仅 GET **原** commerce-operation ID，404、无权限、离线不解锁/不换 ID。不提供自动 POST 重发按钮。只有严格匹配的 terminal GET/写响应可清除本地记录；写请求明确收到提交前的安全4xx可按固定白名单解除，GET 的404绝非解除依据。

身份 epoch 绑定当前 session actor、tenant、当前权限上下文和页面请求代数。登录/退出、切租户/角色、路由销毁或局部 deadline 后立即作废旧 ticket，清除页面敏感/交易快照；迟到成功/GET/poll 不再覆盖新身份或清除其 journal。重载/另一个 tab 按原身份读取 pending，只读恢复。旧 actor 的 pending 不跨身份展示；不能通过切 tenant 清除记录。模型 polling 仅订单 GET/receipt GET，不能触发 checkout/cancel/model generation；有界退避、离页取消，不无限后台轮询。

## 6. APP 新通知：P 验签和 durable ACK

沿 B1 API，不复用 Q 的 Repository/Config/角色/表：

```go
type NotificationInput struct { Headers paymentproto.SignedHeaders; Body []byte }
type NotificationACK struct {
    ReceiptID, EventID, Status, Environment string
    ReceivedAt time.Time
}
func (*Service) ReceiveNotification(context.Context, NotificationInput) (NotificationACK, bool, error)
```

P 既有 `ReadSignedHeaders`/`Keyring.VerifyNotification`/`VerifiedNotification.Canonical` 足够，不新增签名格式或公开 key getter。四头 X-Moni-Key-Id/Delivery-Id/Delivery-Time/Signature 必须各恰一次，拒逗号合并；对 **原始 body bytes** 验签后才 canonical。Service 内部以 Guard 保护的 DB clock_timestamp 验证 ±300s，occurred_at≤DBnow+5s；历史 capture 不因晚通知失效。保存同次 headers.KeyID，仅在验证后持久 canonical 事实和固定摘要，不保存签名/header/raw payload。

**ACK 身份建议（D5）**：receipt_id=`payment_inbox.id`，event_id 和 received_at 取首次不可变 inbox，同 event+同 canonical digest 返回原 ACK；无需新增 Q 式 receipt 表。inbox、normalized evidence、pending processing 同事务；COMMIT 已确认才 first202/duplicate200：

```json
{"data":{"receipt_id":"UUID","event_id":"SafeRef","status":"received","environment":"sandbox","received_at":"UTC6"},"request_id":"string"}
```

上述示意值表示类型；字段精确，首次/重复 data 相同，request_id 可不同，兼容既有 mock ParseACK。received 永远只表示 durable 收到。重复也重新验证当前签名和同环境证明；异 digest 先提交不可变 conflict 再409 `payment_event_conflict`，失败503。未知 key/非法签名/time/header 统一401 `payment_signature_invalid`；环境400 `payment_environment_invalid`；配置应有 key 缺失为503 `payment_key_unavailable`，不能当正常未知 key。COMMIT 未知销毁连接且不2xx；只有确定40P01/40001已回滚可在原5s预算最多2次 retry。无 verifier 成功对象就不能持久可信 inbox。

Recovery 可继续已签名通知验证、durable ACK 和安全只读/query，禁止资金应用/人类写/变更 IO；Guard 丢失停止所有 DB/IO且不成功 ACK。七种 P event union 保持不变；refund/dispute/chargeback 只 review，不应用 C 资金效果。

## 7. 同版资金兼容、构造接线与可并行文件

B2 目标摘要精确六字段：`currency,total_amount,manual_credit_amount,voucher_grant_amount,sandbox_capture_amount,as_of`；全部必须存在，无 sandbox 为 `"0"`，`total=manual+voucher+sandbox`。资金回执仍八字段 `id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at`，source_kind 仅 `manual_credit|voucher_grant|sandbox_capture`。voucher/sandbox source_id 验规范 UUID；manual 沿 A 历史来源语义，不顺便收紧。筛选只允许空或三类，cursor 绑定 tenant+kind+filter。原 wallet 六字段和 reports-v1 不变。

commerce-capabilities 精确两字段不加键，仅将 payments_mode 扩 `off|sandbox`；与 PaymentCapabilitiesView 的 mode 一致，不通过前端推断 sandbox。严格更新服务端、TS、raw JSON/shape 校验、资金三项卡片/总额、三来源显式 switch 标签/筛选和测试；模拟资金不能写作真实现金收入。旧五字段/两来源解析器不得忽略未知字段继续显示部分账目。开始产生 sandbox 资金前必须同版交付；仅完成 B2 SQL 不证明旧 UI 兼容。

**启动接缝仍缺设计（D6）**：`paymentruntime.Component` 目前只有 Mock/Receiver；B1 APP marker 无 key digests、endpoint 或 role OIDs 字段。不能把 APP 伪装成 Receiver、导出秘密 getter，或声称现有 M/Q manifest 已证明 APP/worker。根须冻结以下接口后再接 main：

1. config 的 mode/manifest 环境名和私密文件精确 shape；普通 off+无 marker/B事实不读支付秘密、不构造 client/worker pool、不注册通知、零出站；live 无条件失败。
2. 有 marker/历史的 off 与 sandbox，listener/worker 前核对同环境 nonce、DB/schema/OID/role、历史 keyset、固定 literal-loopback endpoint；缺证据失败关闭。首次连接前严格校验 app/worker DSN，不可先 DNS/proxy 连接后补证明。
3. 独立真实 worker LOGIN/pool，沿 B2 VerifyPaymentRole、完整001–021迁移验证；不以 owner SET ROLE 冒充，不复用 projector/APP/M/Q 身份。APP/worker manifest authority、key/endpoint 绑定与是否追加 marker DDL 待决；本稿不改 M/Q 自有表/manifest/P 字节。
4. B1 `NewProcessor` 私有 lease/MutationWork/QueryWork/Response、Runner 构造/Run/Done/Close/Guard 尚未精确；不能臆造可执行函数签名。继承网络2槽、证据4槽、IO10s/lease60s/发送余量15s、证据5s/lease30s、claim内预算10/20/20及退避1/5/30/120/300；不能塞旧财务或邮件 loop。
5. main 在启动 workers/listener/ready 前 InitializePayments，Guard 在需要 DB/IO 的构造前注入；普通 off 零 worker。SIGTERM 停新认领、取消并等待 IO、排空已开始的短事务，关闭 worker pool 后释放实例；正常取消不能误 fence。新 QA 环境 callback 从开始指向 APP，不热改已证明 B0 receiver。

建议文件所有权（需根分配，非本稿授权）：B HTTP 负责人独占 `internal/server/payment_*.go`；web 负责人独占新增 `web/src/payment*.ts`、支付组件/视图及支付测试。`commerce.ts`、`FundingPanel.vue`、router/App/server/main/config 等共享文件由根集成人或逐个指定唯一负责人串行接线。B1 领域负责人持有 `internal/payments/*`；operations/追加021 与 B2 billing 负责人保持分离；APP runtime/harness/runner 需根另分片。B3 不触碰这些实现文件。

验收按层标注：严格 DTO/字节、准入/timeout/安全错误/epoch/journal 可独立专项；B1/B2 稳定后用真实专用 PostgreSQL 验 CAS、同ID/异体、当前撤权/跨 tenant、三来源完整链、durable ACK/提交未知；runtime 冻结后才做真实 main 构造/off零出站、隔离失败关闭、worker恢复、双 tab journal/重载核查和端到端 UI。fixture/旧 binary/直接 SQL 均不能替代 APP HTTP/构造/浏览器验收。最终集成人更新统一架构正文、changes/manifest并执行规定 refresh/check；本稿本身不执行 refresh。

## 8. 必须由根裁决后冻结的最小清单

| 编号 | 裁决项 | 未裁决时的边界 |
| --- | --- | --- |
| D1 | PublishSKUResult.Created，准确区分201/200 | 不猜首次、不改 wire |
| D2 | 无 operation 的 policy 未知写如何允许显式新意图 | 仅 GET 当前配置，不自动重发/归因/解锁 |
| D3 | off+历史能力矩阵；Observation 单 evidence 映射与游标时间 | 不临时 UNION/扩大 DTO，不假off |
| D4 | B 独立原子 IndexedDB journal；128KiB response/8KiB ACK上限 | 不沿用 A sessionStorage 冒充跨tab持久证明 |
| D5 | ACK receipt_id=inbox.id、received_at=首次 inbox 时刻 | 不生成临时 receipt，不复用 Q 表 |
| D6 | APP/worker manifest/DSN/秘密/角色正身份证明，Runner接口与生命周期 | 只留接缝，不启动 main/worker、不声称已完成 |

本候选仅新增设计；冻结和后续实现仍需逐项复核统一架构中的模块、请求链、资金关系、权限、能力状态及源码链接。B3 完成也不代表真实 PSP、退款/拒付、生产商业支付或性能目标已验收。

## 9. 根集成裁决（2026-11-01）

以下为设计冻结，不是验收记录。D6及领域实现未收口前不发布新支付界面或注册通知路由。

- D1接受：领域新增`PublishSKUResult { Receipt OperationReceipt; Created bool }`，`PublishSKU`返回该类型；首次accepted才Created=true/HTTP201，终态重读和rejected均false/HTTP200。替代B1§4旧返回签名，不改wire。
- D2接受policy为配置CAS，没有operation。未知结果停用编辑，GET仅展示当前配置及revision，不将值相同判作原写成功。读到当前配置后允许用户显式点击“基于当前版本重新编辑”，说明这会开始新的配置意图；保留旧结果未知提示，不自动提交、不复用旧expected_revision。当前读取失败时不可恢复编辑。
- D3接受单evidence观察及排序/游标规则，接受off+已证明历史的能力矩阵。off不抹去历史；DB或证明失败返回不可用，不伪报off或空历史。
- D4接受B专属IndexedDB事务journal、每身份/作用域128项、有界响应128KiB/通知ACK8KiB及§5未知恢复合同。write确定提交前拒绝解除白名单固定为400 invalid_request/invalid_cursor、401当前会话失效、403 forbidden/既有CSRF或再认证/MFA安全码、409 commerce_operation_conflict/payments_runtime_disabled、413 body_too_large、429 rate_limited；不能只按HTTP状态通配，GET永不据此清除。明确终态rejected按原receipt清除。
- D5接受ACK身份为原inbox.id，received_at为首收DB时刻。收到只表示持久接收；任何未确认COMMIT不返回成功ACK。
- D6仍待APP/worker专用私密证明和完整Processor/Runner合同；不借Receiver身份、不使用自由trusted字段或公开ApplyCapture入账口。021已追加，后续修正或新证明表只能追加迁移，不能改写021。


2026-11-01根接续：D6已由[b3-runtime-contract§10](b3-runtime-contract.md#10-根冻结与精确追加ddl2026-11-01)及[Processor合同](b-processor-contract.md)冻结；022证明表与23表权限已实施并通过DDL/ACL专项。领域/运行/Processor暂存候选不属于普通main已集成，发布支付HTTP/UI和进程验收仍须其正式集成后执行；前文D6“待裁决”仅为本合同初稿的历史状态。

命令补充冻结：claim后先取得该命令的父锁、重核当前授权并完成语义决策，再创建仅包含业务效果的savepoint；终态rejected也必须把授权父锁持有至COMMIT。CreateOrder accepted已提交后，若新的RR安全订单读取因撤权、超时或其他原因失败，一律返回`commerce_outcome_unknown`，不能返回可被前端视为提交前拒绝的4xx；客户端保留原operation，通过安全GET核查。

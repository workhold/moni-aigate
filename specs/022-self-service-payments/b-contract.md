# 022-B 隔离本地支付沙箱执行合同草案

2026-11-01，**未冻结 / 未实现 / 未验收 / 不授权编码**。本稿细化[总合同§5–6](contract.md#5-隔离支付沙箱与不可变订单)及[模块设计](../../docs/design/self-service-payments.md)的B切片。A0共同入账、历史保护、角色、投影及A1必须先实现并通过相应专项；[A0冻结接口](a-contract.md#12-a0-账务接口与追加-ddl)是本稿的前置合同，不是已完成事实。所有取舍须根集成人审查冻结，实施前还须发布确切Go类型/追加DDL/角色清单。

B只在专用本地QA环境验证支付可靠协议：没有真实收款、退款、拒付或C的lot分配，不连接真实支付商，不在评审/演示/生产数据库启用。B通过不等于022全部完成或支付商认证。本文只新增设计，不修改A合同、现有迁移或源码。

## 1. 环境隔离与启动证据

PAYMENTS_MODE仅off/sandbox/live，缺省off；本阶段live无条件启动失败。仅设置sandbox变量不能启用。独立QA harness必须创建环境并出具启动凭据；普通serve、admin、API Key及客户页面都不能创建/重置标记或持有模拟器控制权限。

| 项目 | B拟定硬边界 |
| --- | --- |
| PostgreSQL | 仅literal 127.0.0.1:55439的专用moni_test数据库；harness创建随机payment_sandbox_<UUIDhex> schema与本环境独立owner/app/worker角色，不复用已有评审schema或账号 |
| 环境标记 | owner创建不可变payment_sandbox_environment单行：environment_id、database_oid、schema_oid、merchant_id、payee_label、attestation_nonce_digest、created_at、protocol_version；app/worker只SELECT，无INSERT/UPDATE/DELETE或DDL |
| 库外凭据 | harness在非仓库0600临时目录写本次environment/database/schema身份和随机nonce；启动与DB摘要比对，不能从库备份单独重建。路径由harness传递，不允许浏览器提交。复制标记、恢复旧库却没有对应库外证据均失败 |
| 地址 | 应用listener、PUBLIC_ORIGIN、mock API、notification回调均为literal 127.0.0.1或[::1]及harness分配端口；拒localhost/DNS、0.0.0.0、userinfo、fragment、未约定query/路径、重定向及任意客户URL |
| 出口 | 专用HTTP transport禁用HTTP_PROXY/HTTPS_PROXY/ALL_PROXY继承，不跟随redirect；只连接已验证literal loopback endpoint，不允许运行时发现新主机 |
| 密钥 | 独立mock API认证token、通知/查询签名keyring、QA控制token；三者不同，均本次harness生成。签名key版本可轮换，保留未处理证据和恢复所需旧版本；不复用AUTH_SECRET、voucher keyring或上游凭据 |
| 模拟器 | 独立loopback进程及私有持久state目录；无应用数据库角色/连接串。普通main不注册PSP、签名器、simulate-success/forge入口；只有QA CLI持有控制token |

harness启动时校验database名/OID、schema名/OID/owner、pinned search_path、runtime非owner且无CREATE权限、environment/merchant/nonce、协议版本、所有地址/密钥一致，再开放listener。每次serve都验证，不缓存另一实例结果。库外nonce是部署失误防护，不宣称可以抵抗owner或整个主机被攻陷。

普通off且无环境标记的实例不注册payment通知或mock出站任务。已经存在B订单的标记环境切到PAYMENTS_MODE=off，仍必须通过同一隔离证明并保留原签名/查询配置，才能处理已承诺的通知和收尾；不能用off静默丢掉旧订单。harness只允许当前B版本二进制连接本环境，不声称旧A二进制能理解新marker或source。

单活动实例和Guard沿现有服务合同；丢失Guard立即停止全部DB与网络工作。正常SIGTERM停止新工作、取消并等待支付IO，不能把正常取消误作实例ownership丢失；未ACK命令保持原ID供恢复。harness停止模拟器前先停止应用并保留mock state，不能清空state后继续使用旧环境。

## 2. 商品、开关与订单冻结

新增payment_policy唯一行（本环境）：enabled=false、revision=1；admin用独立CAS改enabled，实际变化才递增和审计，同值no-op。有效新售=mode=sandbox&&policy.enabled，Recovery/ready/Guard是独立更强门禁。policy SHARE覆盖订单创建提交，与disable UPDATE确定先后。

商品根payment_skus有id、revision、current_version_id可空、enabled及创建事实；版本payment_sku_versions不可变：id、sku_id、version、display_name、amount、wallet_credit_amount、currency=CNY、environment_id、merchant_id、payee_label、checkout_ttl_seconds=900、created_by、created_at。首片两金额严格相等且正，收款身份只来自不可变环境，不从body或provider跳转导入。发布新版本不改已创建订单；切换当前指针或停售不撤销旧订单。

首次发布使用expected_revision="0"创建SKU根（初始revision=1）；已有根须当前revision且发布后+1。version为每SKU递增正整数，事务锁SKU根后分配；发布不自动上架。设置active/version/enable仅在实际tuple变化时revision+1；enabled=true要求version属于该SKU，enabled=false允许version_id=null。旧CAS持久业务拒绝，无新版本/指针变化。

订单ID等于其创建operation UUID；merchant_order_id由服务端固定为environment UUID+订单UUID的规范编码，永不换ID。订单不可变字段：id、tenant_id、creator、sku_version_id、amount、wallet_credit_amount、currency、environment_id、merchant_id、payee_label、merchant_order_id、created_at、expires_at；创建取得全部根/策略/当前owner锁后用独立DB时刻，expires_at=created_at+900秒。客户端只选已启用的当前SKU version，不输入金额/收款方/paid/callback。

本地创建订单与checkout outbox同tx提交，此时没有journal/receipt/credited，也没有“已付款”。正常off/停售/创建者后来失去owner不会撤销已提交checkout授权或合法capture收尾；新的订单仍须当前owner。

## 3. HTTP、角色与安全DTO

B人类写复用A的严格8KiB JSON/UTF-8/字段/UUID/微CNY/UTC六微秒/5秒总context及独立有界准入；不复用会按conflict字符串映射409的旧billingError。B另有独立全局10rps/burst20、每user+scope 1rps/burst2、最多4个写在途及4096桶/10分钟惰性驱逐，超限429且无command。所有人类写要求CSRF、近期再认证、现有可选MFA；不接受API Key。

沿A的commerce_commands/terminal协议追加B kind，不重写旧A摘要或receipt约束。摘要固定struct顺序/encoding/json策略，绑定actor/scope/kind/target/body，排除operation/request_id。accepted reason=accepted；新B接受回执仅resource_id非null，amount/funding_receipt_id/journal_id均null（申请不是入账）；全部rejected四关联null。业务拒绝HTTP200+rejected，异体409、缺资源404/失权403整tx回滚；COMMIT未知503只GET原operation，404不解锁或重放。配置PUT不创建commerce command，沿独立CAS/未知写合同。

| API | 精确body / 当前角色 / 响应 |
| --- | --- |
| POST /api/platform/payment-skus | {operation_id,sku_id,expected_revision,display_name,amount}；active admin；kind payment.sku.publish，accepted resource为version ID，首次201，重读200 |
| PUT /api/platform/payment-skus/{sku_id}/active | {operation_id,expected_revision,version_id,enabled}；active admin；kind payment.sku.activate，resource为SKU ID；version_id规范UUID或null |
| GET /api/platform/payment-skus、/{id} | active admin/finance_operator/finance_approver读安全根/版本，不得据此造额 |
| GET/PUT /api/platform/payment-policy | 仅active admin；PUT严格{expected_revision,enabled}；GET/PUT同PolicyView，旧CAS409 payment_policy_conflict，env off设置true409 payments_runtime_disabled |
| GET /api/wallet/payment-capabilities | 当前active owner/billing_viewer，PaymentCapabilitiesView；member拒绝 |
| GET /api/platform/payment-capabilities | active admin/finance_operator/finance_approver；同安全能力DTO，不扩大policy读权 |
| GET /api/wallet/payment-skus | 当前active owner/billing_viewer；仅当前可售版本，effective off时返回空数组；DB失败503，不假装空商品 |
| POST /api/wallet/payment-orders | {operation_id,sku_version_id}；当前active owner；kind payment.order.create，resource=order ID；首次accepted202，重读200，rejected200 |
| POST /api/wallet/payment-orders/{id}/cancel | {operation_id}；当前active owner；kind payment.order.cancel，resource=order ID，接受只表示持久取消意图；正常off仍可取消 |
| GET /api/wallet/payment-orders、/{id} | 当前active owner/billing_viewer，仅当前tenant，跨tenant404 |
| GET /api/platform/payment-orders、/{id} | active admin/finance_operator/finance_approver；只读安全事实，不能强制置paid/credited或重新绑定tenant |
| GET /api/platform/payment-orders/{id}/observations | 平台安全证据摘要列表，无签名/原body/API token |
| POST /api/payments/sandbox/notifications | 仅通过隔离证明的环境注册，独立签名认证，不用Cookie/CSRF/admin替代；只提交inbox，见§4 |

人类所有单GET为{data:SafeView,request_id}，列表{data:[SafeView],next_cursor:string|null,request_id}，固定50/读51、DESC(created_at,id COLLATE C)、只允许cursor；每页重验当前权限，cursor绑定scope/kind/筛选，空页[]/null。每个GET均无body，非列表GET无query。所有响应no-store，无秘密、摘要、key版本或任意URL。错误沿固定安全{error:{code,message},request_id}。

### 3.1 B0待冻结的逐kind拒绝与HTTP白名单

下表是本草案的**B0待根冻结枚举**；不是允许领域/HTTP自行扩展的兜底。实施前若需要新增原因，先更新根批准合同。受控业务拒绝均HTTP200+rejected terminal、四关联null，无商品/订单/外部命令/资金/audit效果；infra错误不能写成业务拒绝。

| human kind | 仅允许的持久rejected reason | 精确适用情形 |
| --- | --- | --- |
| payment.sku.publish | payment_revision_conflict、payment_state_conflict | expected_revision不匹配；版本/管理revision已不能再合法递增等受控状态拒绝。静态金额/名称错误400，不claim |
| payment.sku.activate | payment_revision_conflict、payment_state_conflict | 旧CAS；已知version不属于目标SKU或不能合法切换。enabled=true且version_id=null是静态400，不持久拒绝 |
| payment.order.create | payments_disabled、payment_sku_unavailable | DB policy off；SKU/version不存在、非当前或已停售统一不可售。env off的所有POST含重放在claim前409，不查/新建命令 |
| payment.order.cancel | payment_state_conflict | 新operation对已cancel_requested/closed或已credited的订单；不再产生第二cancel命令。单纯本地expired不等于PSP已关，仍允许取消。原operation重读原accepted |

| HTTP | 固定安全error.code白名单 / 提交边界 |
| --- | --- |
| 400 | invalid_request、invalid_cursor、payment_environment_invalid；无可信inbox/command，或已claim后整体回滚 |
| 401 | payment_signature_invalid；通知未知/非法签名key、重复签名header或HMAC/time失败均同码，不暴露key存在性；人类Cookie认证保留原安全码 |
| 403/404 | forbidden / not_found；当前权限失效或路径资源缺失时全tx回滚；跨tenant统一404 |
| 409 | commerce_operation_conflict（同ID异体）、payment_policy_conflict（policy PUT旧CAS）、payments_runtime_disabled（env off）；通知异体为payment_event_conflict且只在冲突事实提交后返回 |
| 413/429 | body_too_large / rate_limited；429含Retry-After:1，无command/可信inbox |
| 503 | commerce_unavailable、commerce_outcome_unknown用于人类命令；payment_unavailable、payment_outcome_unknown用于通知/支付后台边界；payment_key_unavailable表示本环境应有密钥缺失。未提交不能ACK成功，未知不能重发人类命令 |

根既有CSRF/MFA/再认证/Guard/ready/recovery安全码沿原middleware，不反射新body。正常未知key请求仍401；配置引用key缺失是启动/服务不可用，不能伪装业务无效。PSP内部query同query_id异体仅固定query_parameter_conflict，属于适配器协议错误/核查，不扩成人类terminal reason。worker review_reason另用固定集合：unknown_order、capture_outside_window、provider_time_invalid、provider_revision_conflict、amount_mismatch、currency_mismatch、transaction_conflict、duplicate_order_capture、funding_amount_overflow、unsupported_effect_review、processing_attempts_exhausted、command_attempts_exhausted、query_attempts_exhausted、provider_protocol_invalid；无任意服务器原文或新付款指令。

首次/重读accepted order-create为{data:OperationReceipt,order:OrderView,request_id}，order为当前安全视图；rejected无order键。其他人类mutation仅{data:OperationReceipt,request_id}，sku发布不返回秘密。order GET才是资金状态入口，202/HTTP成功不表示capture或到账。

| DTO | 精确字段与空值 |
| --- | --- |
| PolicyView | enabled:boolean、revision:string、runtime_allowed:boolean、effective_enabled:boolean；runtime_allowed仅mode=sandbox且隔离通过，effective再与DB enabled相交，不承诺Recovery/Guard可写 |
| PaymentCapabilitiesView | mode:off/sandbox、environment:sandbox或null、payments_enabled:boolean、local_simulation:boolean；DB失败503，绝不返回live |
| SKUVersionView | id,sku_id,version,display_name,amount,wallet_credit_amount,currency,environment,merchant_id,payee_label,checkout_ttl_seconds,created_at；version字符串、TTL整数900、金额字符串、environment固定sandbox |
| PlatformSKUView | id,revision,enabled,current_version_id可空、current_version可空、created_at；revision字符串，列表不嵌入全部历史版本 |
| OrderView | id,tenant_id,creator,sku_version_id,display_name,amount,wallet_credit_amount,currency,environment,merchant_id,payee_label,created_at,expires_at,checkout_state,creation_state,checkout_ref可空,funding_state,review_required:boolean,review_reason可空,funding_receipt_id可空,journal_id可空,credited_at可空,observed_at |
| ObservationView | id,order_id,kind,status,reason,occurred_at可空,received_at；安全enum与引用，无raw payload/signature/digest/认证材料 |

金额均规范正十进制微CNY字符串；时间UTC六微秒，nullable字段不omitempty。checkout_ref是mock生成的非秘密安全引用，不是URL或可付款凭据；本片不提供客户“模拟成功”按钮。页面始终“本地模拟，无真实收款”，仅funding_state=credited且receipt存在才显示模拟钱包到账。此前A commerce-capabilities的payments_mode与funding-summary仅在B冻结后显式扩展（见§8），本草案不改A当前DTO。

## 4. 通知验签与durable inbox

通知入口先取得独立匿名全局10rps/burst20、最多4个在途许可，再进入可能访问DB的Guard/验签时钟；body最多64KiB。只接受application/json、identity编码，拒content-encoding压缩；严格UTF-8单object，拒重复/未知/大小写别名/尾随JSON/非法控制字符。限流429带Retry-After:1，超限413，不保存raw body或回显输入。

四个header各恰一个值，拒重复/逗号合并：X-Moni-Key-Id为1..32 ASCII版本；X-Moni-Delivery-Id为小写非nil UUID；X-Moni-Delivery-Time为无符号规范十进制Unix秒、无空白/前导零；X-Moni-Signature为64小写hex。keyring为外部固定allowlist及有效窗口，active/retired不由请求指定。先对原始body算SHA-256、按[总合同签名串](contract.md#61-沙箱签名和body)验HMAC，再严格解析和核对body中的environment/merchant；全部通过前不能创建可信证据。

签名时钟唯一采用受Guard保护的独立DB clock_timestamp，允许Delivery-Time与其差值绝对值<=300秒；DB不可用503。回调可重新生成delivery/time/signature，永久业务event身份不变；旧业务occurred_at不因通知晚到而过期。occurred_at必须UTC微秒且<=本次DB时刻+5秒，旧capture仍按订单原时间窗判断，不能只因接收晚而拒绝。

共同业务字段按固定struct顺序规范化：schema_version=1,event_id,event_type,environment_id,merchant_id,order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision，随后才是类型专有字段。event_id和provider transaction均security.ValidReference且<=100字节；order_id规范UUID；amount正数、currency=CNY、revision正数字符串。

| event_type | 专有字段/禁止项 | B动作 |
| --- | --- | --- |
| capture.confirmed | provider_transaction_id必填；无refund_id/case_id/failure_code | 订单/金额/时间/全局交易验证后竞争唯一capture effect |
| checkout.failed | provider_transaction_id=null；failure_code仅checkout_rejected/order_closed/order_expired | 更新安全checkout观察，永不覆盖已确认capture |
| refund.confirmed/refund.failed | 原provider transaction必填，refund_id规范UUID；不允许任意退款参数或账户 | 只保存unsupported_effect_review，不应用资金 |
| dispute.opened/chargeback.debited/chargeback.reversed | 原provider transaction和case_id受控引用必填；无任意债务/余额指令 | 只保存unsupported_effect_review，不应用资金 |

后三类仅证明收到当前B不支持的证据，不承诺C的最终DTO/退款资格。通知不能提供effect_id、tenant_id、journal_id、funding amount或trusted flag。业务digest用规范struct及固定JSON策略，不包含delivery/signature；仅JSON空白/键顺序变化等价，事实字段变化是冲突。

inbox UNIQUE(environment_id,merchant_id,event_id)，保存白名单字段、business_digest、验证key版本、received_at、source=notification及安全接收ID；证据不可UPDATE/DELETE。处理状态独立在payment_inbox_processing，不修改可信原件。相同event+digest返回原接收回执，不新增处理；同event异体追加不可变payment_event_conflicts后409，不覆盖原件，冲突入库失败503。

首次只在inbox提交后202、等价重复200：{data:{receipt_id,event_id,status:"received",environment:"sandbox",received_at},request_id}。status仅表示持久收到，永远不是credited。COMMIT未知503，不ACK；模拟器重投同event，唯一约束收敛。401验签、400格式/环境、413及429均不创建可信inbox；日志只固定码/request_id，不含输入/签名。

## 5. mock PSP幂等、外部协议与查询证据

mock使用与应用库独立的持久本地state（拟SQLite/full synchronous、0600文件，具体依赖须实施前根确认）。每个命令在回应前持久化command ID/参数digest/确定结果和对应订单事实；进程重启不能遗忘已收命令。state丢失或环境不符时拒绝继续，不自动建空state接旧订单。mock内部每merchant_order串行，单订单至多一个capture，capture后cancel返回captured原事实，不能写closed并抹去交易。

外部仅固定POST /v1/checkout、POST /v1/cancel、POST /v1/query，独立Bearer API token（不是QA控制token）；所有URL来自已验证配置。checkout请求固定schema/env/merchant/command_id/merchant_order_id/amount/currency/created_at/expires_at；cancel固定schema/env/merchant/command_id/merchant_order_id；query固定query_id及原command_id/merchant_order_id。无客户端return/callback URL；回调目的地址是harness启动时固定配置。

同external command ID+同canonical参数返回原结果，异体409，永久保留去重。不同ID不能用于同order重复创建；mock另以env/merchant/merchant_order_id唯一保护。query不是支付操作：每次观察可有新query_id，但必须绑定**原**checkout/cancel command ID；重试同次query使用原query_id。新query_id不能改变原支付命令或授权另一笔支付。

**query_id对应一份持久且不变的观察快照**。mock第一次处理query时在同一持久事务中读原command/order一致快照，并保存query_id、请求参数digest和完整规范响应body；同query_id同参数以后返回字节相同的业务body，即使PSP事实后来变化也不能更新这份快照。同query_id异参数409 query_parameter_conflict。重投可更新传输delivery/time/signature以满足300秒窗口，但不改body。not_seen/in_progress也必须保存快照；需要观察之后的变化必须由调度器先持久建立新的query_id，不能复用旧ID换结果。

查询响应严格且有独立域moni-payment-sandbox-query-v1签名，覆盖env/merchant/query_id/原command_id/delivery/time/raw body SHA-256；无redirect。响应固定schema_version,query_id,environment_id,merchant_id,merchant_order_id,command_id,command_kind,parameter_digest,command_state,provider_order_revision,result。command_state为not_seen/in_progress/completed；result仅completed有固定checkout/cancel结果或完整capture事实。核对全部身份/digest后才形成查询证据，不能按HTTP200直接更新余额。

not_seen不证明原网络请求不会迟到，始终按unknown处理；mock本片不提供普通查询可宣布的absent_final。只有此mock已认证同ID幂等时，先query后可以重发**同**checkout command ID/同body，绝不换ID/参数；cancel已请求时不能重发checkout以恢复销售，改用原cancel命令。mock cancel对merchant_order创建持久closed tombstone，即使checkout当时尚未可见，后到checkout也不能重开；若capture已先提交，则cancel返回原capture。

查询的capture转成内部verified evidence，source=query、query_id及provider revision绑定；与notification用相同业务字段/去重/处理管线。查询证据唯一键为(environment_id,merchant_id,query_id)，保存snapshot_digest；同ID同snapshot只读重用，异体追加payment_query_conflicts并review，不能覆盖或作为第二份证据。capture内部event_id固定为query:<query UUID>:<该冻结快照的provider revision>，不能靠换revision绕过query唯一键；not_seen时query响应的provider_order_revision为null，不伪造capture事件。签名材料仅用于验证、不进公开DTO。相同provider交易的callback、query和重复query只竞争同一capture，不创建另一份金额。不同query没有资格伪造“另一笔”交易。

mock只由私有QA CLI触发capture/延迟/断连/拒绝等故障；普通客户/admin页面和应用进程没有这个token。QA控制不在应用router中，控制材料不进入报告。mock不能访问应用DB或修改ledger，它只能提供协议事实。

## 6. checkout、取消、到期与资金状态

creation_state为queued/submitting/submission_unknown/confirmed/failed_no_effect/review_required；checkout_state为pending/ready/cancel_requested/expired/closed。funding_state独立为unconfirmed/verified/credited，review_required及固定review_reason另列；这细化总草案的review_required，不允许把已credited逆向改成未付。所有事实引用不可变，投影不能覆盖或删除旧capture。

| 竞争/事实 | 唯一裁决 |
| --- | --- |
| 本地cancel意图提交 | 只进入cancel_requested并持久cancel outbox；不证明PSP已关闭、不推断未收款 |
| 原checkout结果未知后cancel | 先查原checkout；仍未知也只用固定cancel命令，依mock的order tombstone语义，不新建checkout |
| provider确认closed | 保存provider_closed_at/revision；closed不删除先前capture，provider已captured时cancel结果必须是captured而非无款closed |
| capture发生在本地cancel_requested之后、PSP有效关闭之前 | 若其他资格合法，仍一次入账；本地意图不是外部撤款屏障 |
| capture在provider有效closed之后或与相同revision的closed矛盾 | 保留冲突、review，无新资金；不能用迟到顺序猜测 |
| capture时间 | 采用半开区间created_at<=occurred_at<expires_at；合法晚通知继续入账。期限外、未来异常或商户/金额不符进入review |
| 到期/checkout failed先到、capture后到 | expired/failed不否认原合法时间内capture，验证后仍可入账；捕获事实优先于旧checkout失败观察 |
| 同order另一provider transaction | 永久核查事实，不第二次入账；已credited保留原receipt并标review |
| 同transaction指向另一order/tenant/金额 | 全局身份冲突，保留原绑定和核查事实，无另一笔资金；不回锁另一order或更改获胜归属 |

provider_order_revision是mock对同order事实的严格递增序号，capture/closed的时间与序号共同核查；同revision异体不能最后写入者覆盖。过期可在视图按DB observed_at派生，不能因过期清除unknown或停查旧事实。页面只把journal/receipt同tx已提交的credited称为模拟到账。

## 7. outbox、inbox worker与崩溃点

payment_command_outbox的不可变部分：id,environment_id,order_id,kind(checkout/cancel/query),external_command_id,original_command_id可空,canonical_parameters,parameter_digest,created_at；网络状态另受控列：state,generation,attempt_count,next_attempt_at,lease_until,last_error_code。kind不含refund。状态pending/retry_wait→leased→completed/submission_unknown/review_required；终态不能重开/换body。

独立支付worker每秒按空闲槽认领，最多2个网络IO，不预取排长队；短tx FOR UPDATE OF outbox SKIP LOCKED，只锁outbox，60秒lease、generation+1并提交。发送前普通读复查原命令/订单/Guard/Recovery，DB剩余lease至少15秒；总IO<=10秒，含连接/读写且支持取消，不持DB事务。ACK只用id+leased+generation CAS；lease只防旧DB ACK，不声称可以fence外部网络，mock稳定命令幂等承担外部重复安全。

临时失败退避1/5/30/120/300秒。尝试预算在**claim成功事务内**增加，不能在实际网络前/成功后另计：变更命令最多10次claim，含claim后尚未IO即崩溃；每个原命令的query supervisor最多20次query claim，含重投同query_id及未IO崩溃。claim/generation/attempt_count同tx，不能通过重启、旧ACK或另建query supervisor复位预算。达到次数上限不表示确定无支付；有效晚通知仍接收。固定错误码network/timeout/auth/protocol/environment/temporary/permanent/attempts_exhausted，不保存服务端原文。

query supervisor为outbox中kind=query、UNIQUE(original_command_id,kind)的单一调度根，固定参数只含原命令身份；每次实际query的query_id/参数另存不可变payment_query_attempts。开始新观察时先持久新query_id；若上次query响应未知则复用原query_id/参数。两者每次claim都消耗同一supervisor的20次预算，不是“20个query_id各自无限重试”；当前子记录与supervisor的generation关联由触发器保护。所有网络仍claim提交后执行。

| 最后一次claim后的状态 | lease/耗尽出口（DB时钟、有界清理） |
| --- | --- |
| mutation第10次正常拿到确定响应 | 按响应CAS为completed或明确review；不能第11次claim |
| mutation第10次claim后崩溃/未ACK，lease到期 | CAS为submission_unknown、lease清空、last_error_code=attempts_exhausted、review_reason=command_attempts_exhausted；同tx确保唯一query supervisor存在。不判failed_no_effect，不延长第10租约或新建支付ID |
| query第20次有合法冻结快照 | 先持久该证据；可由同管线确认既有事实。若仍not_seen/in_progress则标query_attempts_exhausted review，不能第21次claim |
| query第20次claim后崩溃/未ACK，lease到期 | supervisor转review_required、lease清空、last_error_code=attempts_exhausted、review_reason=query_attempts_exhausted，保留原命令/当前query_id与不可变子记录；不生成第21query，不重开变更命令 |
| 耗尽后旧签名响应/回调才到 | 有效证据仍可追加并按唯一capture管线核对；旧generation不能重开IO状态/预算，不能因已耗尽丢掉合法capture |

inbox处理独立认领payment_inbox_processing，最多4个DB任务、每次5秒、30秒lease，claim先提交。处理tx先按order根锁，inbox原件只读，最后用generation CAS提交处理结果；CAS未命中必须回滚本tx的全部source/资金/处理变化，不让旧worker提交另一generation的结果。不能持processing/outbox锁回取order。临时基础设施失败有限退避，最多20次后processing_review，保留证据；语义异常提交review事实而不是无限重试毒消息。B没有强制补账/释放隔离收款按钮，review只读保留供后续另立合同处理。

| 崩溃位置 | 恢复行为 |
| --- | --- |
| order/outbox提交前 | 全回滚，无checkout权限；客户端原operation只GET核查 |
| DB已提交但202丢失 | 同operation/GET定位原order，只有原outbox，不能新建订单 |
| claim提交后、网络前 | lease到期后恢复原ID；不能据没有观察认定PSP未执行 |
| PSP接收/持久化后响应丢失 | submission_unknown，先query原ID；必要时仅已认证幂等的同ID重发 |
| 收到PSP响应但本地ACK前 | 旧lease恢复查询；provider命令和capture唯一性不变 |
| inbox提交前/COMMIT未知 | 不发2xx ACK；PSP重投同event，等价永久去重 |
| inbox已ACK、处理前 | durable processing恢复，不要求PSP再次通知才入账 |
| source/journal/receipt/credited/处理状态任一步、COMMIT前 | 同tx全回滚；只重跑已验证财务处理，不发模型或另一笔支付 |
| 资金COMMIT成功但worker收到未知结果 | 保留原effect/source；恢复先读事实，唯一约束收敛，不新加钱 |
| stale worker晚ACK/晚响应 | CAS不覆盖新generation；仍有效的签名证据可追加到统一管线，不能凭旧lease直接改新状态 |
| mock进程崩溃/重启 | 用持久state返回原命令/交易；state缺失直接停机核查，不重置并接受旧ID |

## 8. capture唯一入账与A0扩展（B实施前冻结）

A0仅支持manual_credit/voucher_grant。B须追加受审迁移、source enum、触发器、VerifyFunding/Rebuild及确切Go合同后才允许sandbox_capture；不能给A0直接传未知kind或把支付伪装为manual。commerce只创建/验证支付证据，不能直接post ledger；billing不解析外部签名或导入commerce包。

拟新增不可变payment_captures，id为服务端UUID，字段包括order_id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,wallet_credit_amount,currency,captured_at,provider_order_revision,verified_evidence_id,created_at。UNIQUE(order_id)、UNIQUE(env,merchant,provider_transaction_id)，tenant/order/environment复合归属。payment_effects的capture effect_id由系统固定为capture，不来自event或provider自由字段；UNIQUE(env,merchant,provider_transaction_id,effect_kind,effect_id)，绑定唯一capture source。

处理tx持order根和tenant SHARE（不再要求买家当前active/owner），验证通知/query证据与冻结订单、单capture、环境及时间；在savepoint内预分配capture ID，插入受约束source/effect后调用扩展ApplyFundingTx：source={sandbox_capture,captureID}，FundingScope={source.tenant_id,"payment-sandbox-worker",CommandID:""}。固定system actor是内部执行身份，不是伪装的人类审批；helper必须为此kind显式验证空CommandID/固定actor、锁内source/order/verified证据，不能靠caller trusted=true。root冻结B Go接口时确认这一新增分支，不改变A两个kind原语义。

拟分录AVAILABLE+A / SANDBOX_FUNDING_CLEARING-A，独立平台BIGINT非正清算账户，不能在报表中冒充真实现金清算。journal.kind=sandbox_capture、operation_id=captureID、reference=orderID；funding_receipts.source_kind=sandbox_capture、source_id=captureID，currency/tenant/amount严格相同。helper唯一持账户ID序锁、锁后最终DB时刻/金额证明、写journal/receipt/一个schema1 credited；外层同tx写支付audit、order资金投影和processing完成，不再emit。source与receipt/journal/effect须提交时双向完整，缺任一禁止COMMIT。

同transaction另一event/query若完整事实等价，读原effect/receipt并确认对应处理，无新资金；不同order/金额/时间等冲突进入review。基础设施/约束/audit/outbox错误全回滚并安全重试；已确认收款但账户溢出等语义问题回滚业务savepoint、提交review事实，无capture source/receipt/journal/credited，不能称支付失败。review不删除verified原件，也不生成调平分录。

B冻结时显式扩展funding source分类/DTO与页面：funding-summary增加sandbox_capture_amount且total=manual+voucher+sandbox，保留NUMERIC字符串/as_of；receipt source筛选增加sandbox_capture。原reports-v1/credited仍累计钱包入账，展示sandbox环境，不声称真实收入；无第二类累加事件。VerifyFunding逐笔证明capture→effect→order→receipt→journal→唯一credited，租户核验只比该tenant的清算分录；VerifyLedger全账户核验保留。source扩展、清算科目/allowlist/DB check/初始化/输出/核验必须同次完成，不能只改Go enum。

## 9. 当前权限、撤权、正常off与Recovery

人类每次写在tx锁后检查当前用户/tenant/membership，不信session role缓存；SKU/policy admin、order create/cancel owner。所有列表/GET按当前读权与scope过滤，失权403、跨tenant404，不返回另一tenant/provider内部材料。系统处理已合法创建订单的capture，不再依赖创建者角色、tenant当前active、商品仍上架或浏览器在线；不得因停用阻断资金有利收尾。账务只使用冻结tenant和可信证据，不接受通知自报tenant。

| 动作 | 正常有效off/停售 | RecoveryMode | Guard失败 |
| --- | --- | --- | --- |
| 新订单/新checkout授权 | 拒绝；env off claim前409，DB policy off持久payments_disabled rejected | 拒绝、不claim | 全停 |
| 已提交checkout outbox | 保留原授权，可同ID完成；停售不撤单 | 暂停新checkout/cancel外部变更，unknown保留 | 全停 |
| 当前owner取消旧订单 | 允许持久原cancel意图/命令 | 拒绝新取消写 | 全停 |
| 通知验签/持久inbox ACK | 继续，配置/旧keys必须保留 | 仅收集证据并ACK，不应用资金 | 不DB/不ACK成功 |
| 原ID只读query | 继续有界观察 | 允许受控只读观察并保存证据，不补发支付/取消 | 全停 |
| 已验证capture effect入账 | 继续，不以正常off或撤权拒绝 | 禁止所有新资金effect | 全停 |
| 商品发布/active/policy写 | 可停用/维护元数据；env off不能开启售卖 | 全部拒绝 | 全停 |
| 当前合法回执/订单只读 | 继续 | 继续；标明事故冻结 | 不通过失效Guard读取 |

恢复旧备份后，不因重放inbox/outbox自动应用恢复点后的资金或再发checkout/cancel。退出Recovery仍须现有离库检查点/独立mock持久记录与双人核对；恢复点后来源/effect/外部命令按原ID核查，任何不能证明的事实保持review。仅取消Recovery变量不等于验收恢复完成。

## 10. 追加DDL、最小角色和锁图

B只追加首个可用迁移编号（暂称018_payment_sandbox.sql，最终由根分配），绝不编辑017或历史行以绕过约束。拟表组：payment_sandbox_environment、payment_policy、payment_skus/payment_sku_versions、payment_orders、payment_inbox/payment_event_conflicts/payment_inbox_processing、payment_command_outbox/payment_command_observations/payment_query_attempts、payment_verified_query_evidence/payment_query_conflicts、payment_captures/payment_effects、payment_review_facts。新增ID为canonical UUID约束TEXT，既有FK保持TEXT；证据/来源/事件不可变，状态投影有列白名单与单向触发器，不删financial去重记录。

追加而不替换A的kind/source/receipt/credited约束；B启动先验证schema与代码能力，严格拒live或不匹配环境。历史A manual/voucher和report总量保持，不回填任何sandbox资金、不中途生成新journal/outbox。B前所有正常实例仍不接受sandbox source。删除QA环境仅由harness在服务/worker停机、导出脱敏证据后清理其自建schema/state；不提供线上清历史命令。

| 身份 | 拟最小权限，实施前逐表/列冻结 |
| --- | --- |
| app HTTP角色 | 必要安全SELECT；INSERT人类commerce commands/terminals、SKU版本、订单/outbox和验签inbox/冲突；policy仅enabled、SKU根仅受控指针/enable、order仅取消意图等白名单；无环境标记写权、无DDL/owner |
| payment worker角色 | 指定支付SELECT、outbox/processing lease列UPDATE、不可变观察/query/capture/effect/review INSERT；经billing的必要ledger_accounts.balance UPDATE、journal/lines/receipt/audit/credited INSERT；不能发行券、批准credit、改订单金额/tenant、改证据、DDL |
| projector | 原projection_outbox SELECT及自有投影写；无payment source/签名/订单/inbox查询写权，不造资金 |
| migration owner/harness | 独占维护/服务锁内追加DDL、环境初始化/provision、测试环境回收；不交给serve |
| mock PSP | 没有应用DB身份；只有自己的durable state和独立loopback API/QA控制token |

必须在Provision blanket grant之后收回新历史表UPDATE/DELETE及无关表权，再给必要列；仅增加列GRANT而遗留表级权限不合格。触发器同时保护直接SQL状态/来源/归属，不宣称可以抵抗整个app/worker执行身份被攻陷。

| 路径 | 拟锁序 |
| --- | --- |
| publish/activate SKU | commerce command→SKU UPDATE→当前admin user SHARE→新DB时刻/CAS→版本/指针/audit/terminal |
| policy PUT | admin user SHARE→policy UPDATE→新语句CAS/audit；不取SKU/order/accounts |
| 新order | command→SKU SHARE（新语句确认当前version/enable）→policy SHARE→tenant SHARE→membership SHARE→user SHARE→新order/outbox/audit/terminal |
| cancel order | command→order UPDATE→tenant SHARE→membership SHARE→user SHARE→取消意图/outbox/audit/terminal；不取SKU/policy/accounts |
| outbox/processing claim | 独立短tx仅认领自己的状态行后提交，网络/处理前无这些锁 |
| 外部命令结果投影 | order UPDATE→必要outbox generation CAS/观察；不得outbox锁后回order |
| capture应用 | 非锁定读取evidence/order定位→order UPDATE→必要provider transaction唯一绑定→tenant SHARE→全部accounts ID序→最终DB时刻→source/effect/journal/receipt/credited/audit/processing CAS |
| 未知order/不支持事件 | 无资金；只追加review和处理CAS，不伪造tenant父行 |

一次处理只涉及一个order；全局transaction唯一冲突不回锁另一order，转review；不可先锁provider transaction/effect再找order。原件inbox只读，不持inbox/lease锁再取order。所有FK父身份已知；账户后不回锁order/tenant/SKU/授权行。普通Reserve/settle/release/refund仍沿A原锁序，不接lot或payment-order；此表只是B待审目标，须真实PG交叉验证，不能宣称已无死锁。

## 11. B冻结与本地验收出口

冻结前根须确认：隔离凭据/固定本地DB限制、SKU/CAS/时间端点、完整DTO和错误枚举、query签名与not_seen语义、mock持久存储/幂等、取消tombstone与provider revision、后台限额/耗尽review、sandbox source/新清算科目/固定system actor、off与Recovery矩阵、B锁图及app/worker精确grants。A0/A1前置出口与B源扩展必须有真实PG测试，不能用参考项目实现或设计文字代替。

| 验收组 | 必须证明的本地边界 |
| --- | --- |
| B-S01隔离 | 错DB/schema/OID/nonce/merchant/owner、复制marker、非loopback、代理/redirect、live均拒绝；评审DB零写入，普通实例无模拟器入口 |
| B-S02人类命令 | role/owner撤权、MFA/再认证、同operation换SKU/target/scope、CAS、严格JSON/金额/分页/nullable、未知GET404不重放 |
| B-S03验签 | raw body一字节变化、重复header、坏key/time/signature、未知/重复JSON、超限/压缩、跨环境/商户、DB时钟边界；无可信inbox/秘密泄漏 |
| B-S04三重身份 | event等价/异体、跨event同transaction、同order第二transaction、同transaction跨order/tenant、query/callback并发，只有一份合法effect/source/journal/receipt/credited |
| B-S05外部未知 | 发送前/PSP持久化后/ACK前各崩溃，query not_seen不当失败；同query_id返回原快照、异体冲突、变新事实须新ID；第10mutation/第20query claim后未IO或未ACK均按lease出口收敛，无第11/21；同ID支付重发一次effect，取消tombstone不让后到checkout重开 |
| B-S06乱序/时间 | 半开期限边界、锁等待跨期、合法晚capture、failed/expired先到、local cancel vs PSP closed/captured版本、同revision异体均按§6裁决 |
| B-S07资金原子 | source、账户、journal、receipt、credited、audit、processing/COMMIT逐点注入失败；已确认capture溢出进review而不假失败，无孤立/双入账/调平 |
| B-S08权限/off/恢复 | 受限app/worker/projector真实grants；正常off/停售/撤权后旧事实收尾，Recovery只收证据不资金、Guard失效全停、SIGTERM取消不误fence |
| B-S09持久恢复 | mock重启保command/transaction，应用真实kill窗口与备份恢复；原ID核查、lease旧ACK不能覆盖、耗尽不宣称无支付，无模型生成调用 |
| B-S10核验/UI | VerifyFunding/VerifyLedger/Rebuild全来源与同额错绑失败；本地浏览器只见安全receipt/模拟标记，不能模拟付款/签名/强制到账，无C退款入口 |

证据仅专用QA数据库与脱敏引用，记录源码/迁移/命令和pass/skip/fail。真实支付商生产签名、商户开户、真实收款/对账/退现/拒付、风控税务与生产容量均外部门禁。完成本稿后回到A1实现前置，不因B草案存在而开始B代码。统一架构由根在全部并行变更汇总后记录为planned并刷新；本子任务不执行refresh。

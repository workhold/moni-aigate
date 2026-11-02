# 022-C0：SandboxFacts 完整本地来源图合同候选

2026-11-02，**根冻结纯 core；reader、完整 phase 及其权威装配未冻结**。本稿承接 [完整来源候选 §10–11](c0-verifier-contract.md)、[B 领域](b-domain-contract.md)、[Processor](b-processor-contract.md)、[第三资金来源](b2-funding-contract.md)、[原前驱认证](c0-predecessor-contract.md)及 [HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)。目标是独立检查全部应用支付持久图；[两表 capture links](c0-sandbox-capture-link-contract.md)继续保留原窄 scope，不转换为完整 SandboxFacts。

本稿以 [021](../../migrations/021_payment_sandbox.sql)、[022](../../migrations/022_payment_runtime_attestation.sql)、正式 [Service](../../internal/payments/service_commands.go)、[通知持久化](../../internal/payments/service_notification.go)、[query](../../internal/payments/processor_query.go)、[evidence](../../internal/payments/processor_evidence.go)、[lease](../../internal/payments/processor_claim.go)、[资金 helper](../../internal/billing/funding_sandbox.go)核定事实合同。可以阅读 SQL 的受审定义，但 Go 判断不得调用、复制调用结果或引入写侧 `payment_*_valid`、`funding_journal_valid`、`payment_check_human` 等谓词作 oracle；不得调用 billing helper、Processor、Service 或付款/模型网络。后续纯 core 独占 `internal/fundinglotverify/sandbox_source_graph*.go`，不修改旧 reader/writer、根 session、共享 DTO 或架构快照。

## 1. 范围、模式与权威接缝

新纯 API 候选：`CheckSandboxSourceGraph(context.Context, SandboxSourceGraphRows) (SandboxSourceGraphCheck,error)`。它只解释传入原行；不发行 proof、RuntimeProof、Permit、完整扫描完成位、lot 资格能力或数据库正确性声明。空 DTO 可以局部完成，不能代表 ordinary 安装、空数据库或 SandboxFacts 不适用。

根拥有模式判定、安装/OID/角色/权限/descriptor、原 immutable v1 predecessor、mock/receiver marker/key 和未来 v2/epoch 绑定。纯 core 保留 App `payment_sandbox_environment` 与原 `payment_runtime_attestations.proof_canonical`，独立检查其业务环境、摘要及其中声明的 key ID 集合；这些原行 **不是** 已认证安装。不得新增公开 `TrustedKeys`/key DTO、从字面事实构造认证前驱或接受 caller 的 `Verified=true`。真实 reader 仅在根已有前驱认证、同 owned session/同原 snapshot 下收集这些事实；不能因 ordinary/off 就跳过 App 支付事实枚举、缺表或缺权限。

本地完整范围精确定义为：**受审验签入口与不可变 stored-verified-fact 生命周期之下，全部应有支付业务关系、当前代数及资金效果**。021 未保存入站 raw delivery body/headers/HMAC；本片不历史重验签名，不认证正式 PSP 或外部收款。它不验证现在的管理员/owner/成员身份来推翻历史，也不按当前 enabled/SKU/tenant/user 状态否定原合法订单或晚到 capture。

## 2. 固定原始表、列与 DTO

共有 **33 张物理表：23 张 payment 表（021 的22张，022的1张）加10张共用表**。全表无过滤独立扫描；禁止 JOIN/聚合/SQL 业务谓词、从合法父 INNER JOIN 取得子集合或由 catalog 动态扩大字段。共用行只读一次，在根 owned session 中映射多个 core，不重复计算源资源。下面顺序就是固定 SELECT/Scan 顺序；所有 BIGINT、INTEGER、OID、xid8 数值转 `::pg_catalog.text` 保留规范原十进制；bool/time/bytea 按 typed 值保留，nullable 不能 coalesce。

| 表 | 固定列顺序 | 行 DTO（前缀 `SandboxGraph`） |
| --- | --- | --- |
| `payment_sandbox_environment` | singleton,id,merchant_id,payee_label,database_oid::text,schema_oid::text,nonce_digest,app_role::text,worker_role::text,projector_role::text,protocol_version::text,created_at | `EnvironmentRow` |
| `payment_runtime_attestations` | singleton,environment_id,database_oid::text,schema_oid::text,schema_owner_oid::text,app_role_oid::text,worker_role_oid::text,projector_role_oid::text,app_nonce_digest,worker_nonce_digest,proof_version::text,proof_canonical,proof_sha256,created_at | `AttestationRow` |
| `payment_policy` | singleton,enabled,revision::text | `PolicyRow` |
| `payment_skus` | id,revision::text,last_version::text,enabled,current_version_id,created_by,created_at,last_change_operation_id | `SKURow` |
| `payment_sku_versions` | id,sku_id,version::text,display_name,amount::text,wallet_credit_amount::text,currency,environment_id,merchant_id,payee_label,checkout_ttl_seconds::text,publish_operation_id,created_by,created_at | `SKUVersionRow` |
| `payment_sku_changes` | operation_id,sku_id,kind,before_revision::text,after_revision::text,before_version_id,after_version_id,published_version_id,before_enabled,after_enabled,created_at,before_operation_id,created_xid::text | `SKUChangeRow` |
| `payment_orders` | id,tenant_id,creator,sku_version_id,display_name,amount::text,wallet_credit_amount::text,currency,environment_id,merchant_id,payee_label,merchant_order_id,created_at,expires_at | `OrderRow` |
| `payment_cancel_intents` | order_id,operation_id,actor,created_at | `CancelIntentRow` |
| `payment_command_outbox` | id,order_id,kind,external_command_id,original_outbox_id,canonical_parameters,parameter_digest,state,generation::text,attempt_count::text,current_query_id,next_attempt_at,lease_until,last_error_code,created_at,last_submission_unknown_at | `CommandOutboxRow` |
| `payment_command_observations` | id,outbox_id,generation::text,canonical_reply,reply_digest,observed_at | `ObservationRow` |
| `payment_query_attempts` | query_id,supervisor_id,created_generation::text,canonical_request,request_digest,created_at,mutation_generation::text | `QueryAttemptRow` |
| `payment_inbox` | id,environment_id,merchant_id,event_id,canonical_body,business_digest,verified_key_id,received_at | `InboxRow` |
| `payment_event_conflicts` | id,inbox_id,conflicting_digest,received_at | `EventConflictRow` |
| `payment_verified_queries` | id,query_id,canonical_body,snapshot_digest,verified_key_id,received_at | `VerifiedQueryRow` |
| `payment_query_conflicts` | id,verified_query_id,conflicting_digest,received_at | `QueryConflictRow` |
| `payment_verified_evidence` | id,inbox_id,verified_query_id,environment_id,merchant_id,order_id,kind,provider_transaction_id,amount::text,currency,happened_at,provider_order_revision::text,fact_digest,verified_at | `EvidenceRow` |
| `payment_evidence_processing` | evidence_id,state,generation::text,attempt_count::text,next_attempt_at,lease_until,last_error_code | `ProcessingRow` |
| `payment_provider_revisions` | environment_id,merchant_id,order_id,provider_order_revision::text,kind,fact_digest,evidence_id | `ProviderRevisionRow` |
| `payment_transaction_identities` | id,environment_id,merchant_id,provider_transaction_id,order_id,tenant_id,amount::text,currency,captured_at,provider_order_revision::text,evidence_id,created_at,processing_generation::text | `IdentityRow` |
| `payment_order_capture_selections` | order_id,identity_id,first_evidence_id,selected_at | `SelectionRow` |
| `payment_captures` | id,order_id,identity_id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount::text,wallet_credit_amount::text,currency,captured_at,provider_order_revision::text,verified_evidence_id,created_at,processing_generation::text | `CaptureRow` |
| `payment_effects` | capture_id,identity_id,environment_id,merchant_id,provider_transaction_id,effect_kind,effect_id,created_at | `EffectRow` |
| `payment_review_facts` | id,order_id,evidence_id,identity_id,selection_order_id,reason,created_at | `ReviewRow` |
| `users` | id | 既有 `ProcurementApprovalUserRow` |
| `tenants` | id | 既有 `ProcurementAttemptTenantRow` |
| `commerce_commands` | operation_id,actor,scope,kind,request_digest,fingerprint_key_version,created_at | 既有 `FundingGraphCommerceCommandRow` |
| `commerce_operation_receipts` | operation_id,kind,scope,actor,status,reason,resource_id,amount::text,funding_receipt_id,journal_id,created_at | 既有 `FundingGraphCommerceReceiptRow` |
| `funding_receipts` | id,tenant_id,source_kind,source_id,amount::text,currency,journal_id,created_at | 既有 `FundingGraphReceiptRow` |
| `ledger_accounts` | id,tenant_id,kind,balance::text | 既有 `LedgerAccountRow` |
| `journals` | id,tenant_id,operation_id,kind,reference,actor,currency,created_txid::text,created_at | 既有 `FundingGraphJournalRow` |
| `journal_lines` | id,journal_id,account_id,signed_delta::text | 既有 `LedgerLineRow` |
| `audit_events` | id,actor,tenant_scope,action,target,sanitized_diff::text,request_id,created_at | 既有 `ProcurementAuditRow` |
| `projection_outbox` | event_id,tenant_id,operation_id,kind,schema_version,sanitized_payload::text,created_at | 既有 `ProcurementOutboxRow`；schema_version 沿原 typed int32 |

每个 `SandboxGraph*Row` 的 Go 字段按本表列逐一 PascalCase，例如 `SKUVersionID/OrderID/EnvironmentID/CreatedXID/CreatedAt`，不省略列或把不同表字段拼成已核结果。默认字段类型为 `string`；例外完整封闭如下：

- `Singleton/Enabled/BeforeEnabled/AfterEnabled bool`。
- 所有 `*_at`、`expires_at`、`next_attempt_at` 为 `time.Time`，但 `lease_until/last_submission_unknown_at/happened_at` 为 `*time.Time`。不得由当前 clock 补默认值。
- `ProofCanonical/CanonicalReply/CanonicalRequest/CanonicalBody []byte`；outbox 的 `CanonicalParameters *[]byte`，nil 是 SQL NULL，非 nil 的空 bytea 是不同坏值。
- nullable TEXT/数值投影均为 `*string`：SKU.current_version_id/last_change_operation_id；change.before_version_id/after_version_id/published_version_id/before_operation_id；outbox.external_command_id/original_outbox_id/parameter_digest/current_query_id/last_error_code；evidence.inbox_id/verified_query_id/provider_transaction_id/amount/currency/provider_order_revision；processing.last_error_code；review.identity_id/selection_order_id。
- `SandboxSourceGraphRows` 按上述33行顺序定义33个命名 slice：`Environments,Attestations,Policies,SKUs,SKUVersions,SKUChanges,Orders,CancelIntents,CommandOutbox,Observations,QueryAttempts,Inbox,EventConflicts,VerifiedQueries,QueryConflicts,Evidence,Processing,ProviderRevisions,Identities,Selections,Captures,Effects,Reviews,Users,Tenants,Commands,CommerceReceipts,Receipts,Accounts,Journals,Lines,Audits,Outbox`。没有公共 context/mode/已认证标志或已算摘要输入。

页键：普通 id；command/change/commerce 按 operation_id（outbox仍id），processing按evidence_id，query_attempts按query_id，cancel/selection按order_id，effect按capture_id，projection_outbox按event_id；三张singleton表按singleton。provider_revisions以完整四元 `(environment_id COLLATE C,merchant_id COLLATE C,order_id COLLATE C,provider_order_revision)`，最后一项是原 BIGINT 数值顺序，不能以十进制字符串排序替代。首次独立bool、128行keyset、严格推进/完整EOF/Err/Close、共享budget及唯一Finish由根 reader 冻结，不能在纯core自称已完成数据库EOF。

## 3. 原始值、去重与资源

全33组先合计100000实际行、16MiB全部原始string字节与bytea字节；nullable非NULL也计，不计typed bool/time/int32的人工格式化文本。限额先检查后保存/解析，任何错误返回零Check。每256个行/索引/边/JSON token步骤及退出前检查context；不得在大bytea单行中无限扫描而不观察context。纯core不得修改输入、排序原slice或保留外部可变指针为权威。

单bytea：request/parameters 1..8192bytes，reply/inbox/query original/proof 1..65536bytes；nullable query parameters必须NULL。这些是数据shape界；总预算超限是Resource，单行违反自身持久合同但未超总预算是mismatch。所有普通TEXT合法UTF-8且无NUL，空TEXT保留精确身份、不trim；只有021/022明确UUID、引用、摘要、enum等受各自词法约束。payment自身有funding_uuid的字段要求canonical非零小写UUID；共用 users/tenants/account/line/journal ID 不因本片新增UUID假设。P1 reference要求1..100bytes、Unicode字母/数字或 `:_./-`、拒 `://` 和sk-/mg_/bearer前缀；display_name/payee_label沿各自持久/原入口label合同，不把P的provider reference限制错套给显示名。

金额正BIGINT、revision/version/processing_generation正BIGINT，代数/计次非负；OID正uint32；created_xid正uint64；所有十进制canonical，无前导零/正号/负零/空白/小数/指数。共享balance/delta为signedBIGINT；中间和big.Int。native时刻finite、UTC year1..9999、微秒精度，包含数据库合法的 `0001-01-01T00:00:00.000000Z`（Go零值）；nullablenil保留，不用UnixNano范围判断。任何时间关系只按下文明确合同，不从UUID/接收时间/created_at猜跨事务顺序。

主键冲突按额外行 `DuplicatePrimaryRows` 计数，保留第一次原行；typed secondary唯一冲突单列 `DuplicateUniqueRows`，首行保留且剔除后行后才关系/累计；被剔除行仍计Scanned/原budget/词法错误。键包括全部表的DDL UNIQUE/partial UNIQUE：SKU(sku_id,version)/(sku_id,id)/publish_operation；cancel.operation；mutation(order,kind)/external_command；query-supervisor(original_outbox)；observation(outbox,generation,reply_digest)；query(supervisor,created_generation)/(supervisor,query_id)；inbox(env,merchant,event)；conflict(parent,digest)；verified_query.query；evidence.inbox/verified_query的非NULL唯一；identity(env,merchant,transaction)/(order,id)；selection.identity；capture.order/identity/(tenant,id)；effect.identity/(env,merchant,transaction,kind,id)；review(evidence,reason)；receipt(kind,source)/journal；journal.operation；line(journal,account)；account的NULL区分(tenant,kind)。payment human audit的(action,target)、capture audit的(action,target)、outbox(operation,kind)也全量去重。commerce A五类partial唯一照原合同，不擅自把payment.activate/cancel限制为resource只能一次。

## 4. 原环境、v1 key 与独立canonical

Environments与Attestations在sandbox图内各恰一行且singleton=true；Policies恰一行singleton=true，revision正，enabled可以false。marker环境/merchant/payee及protocol=1；attestation环境、DB/schema OID、app nonce与marker一致，三runtime OID互异且均不同owner，proof_version=1。原proof的SHA256与存值相同；pure独立strict decode+canonical重编码同bytes，并比对v1内环境/merchant/payee、DB/schema/owner/roles/nonce摘要字段和key集合。**结构与自洽不等于安装认证**；实际OID对象、完整v1字段语义/前驱descriptor/peer key对照仍须根已有认证真实完成。不得只抽keys后忽略畸形、重复或额外proof字段。

proof使用[c0-predecessor合同](c0-predecessor-contract.md)的原v1精确对象/字段顺序，不另造新proof格式：role/name/OID/login、DB literal目标、App/Mock/Receiver、worker/projector、origins/callback、nonce/token digests与1..128个key(id,digest,not_before,not_after)全部存在、唯一、严格类型、UTC6与区间，源keys顺序/canonical沿原v1，唯一性按key ID；两个不同ID复用同一digest是原v1合法形状，不额外拒绝。core仅作描述性同构/业务绑定；不读任何key材料、nonce、token、manifest/DSN或目录文件。

每inbox/verified_query的verified_key_id必须属于这一个原v1 key集合，ID词法 `[A-Za-z0-9_-]{1,32}`。**不能要求received_at落在key有效区间或当前key仍有效**：签名约束的是未保存的delivery_time，验证入口DB时刻、transaction received_at及wall clock可以不同。没有header不能构造准确验签时点，也不能从接收时间±300秒伪推已通过签名。

所有P1对象使用本纯core的独立封闭parser与canonical encoder，不import paymentproto/payments/写侧codec。要求UTF-8/有效UTF-16转义、无重复/未知/缺失字段、精确nullable、无尾随值，schema_version原JSON数字恰1，最大深度8（proof最大12）、单P对象不超过128个JSON值/token节点（proof4096、keys128）；canonical逐byte等于持久bytea，SHA256重新计算。JSON数字不经float，字符串时刻canonical UTC六位微秒，所有nested union也严格字段集合。P1 UTC6时刻沿实际instant codec允许year1零值；v1 proof key的runtimeInstant及P1签名keyTime/当前验证时钟仍拒Go零值，不能把key/时钟限制套给全部native或P1业务时刻。P1 canonical使用固定结构字段顺序和Go标准JSON字符串转义；原v1 proof精确保留前驱codec的 `SetEscapeHTML(false)`，仍按Go规则转义U+2028/U+2029；不接受只是JSON语义相等的异体bytes。

P1完整字段顺序：checkout=`schema_version,environment_id,merchant_id,command_id,merchant_order_id,amount,currency,created_at,expires_at`；cancel为前五项；query=`schema_version,environment_id,merchant_id,query_id,command_id,merchant_order_id,command_kind,parameter_digest`；command-reply=`schema_version,environment_id,merchant_id,merchant_order_id,command_id,command_kind,parameter_digest,command_state,provider_order_revision,result`；query-reply在schema_version后插query_id，其余同reply。result=`status,checkout_ref,failure_code,capture,closed_at`；capture=`provider_transaction_id,amount,currency,occurred_at,provider_order_revision`。

reply union：`in_progress`的revision/result均NULL；`not_seen`只允许query且两者NULL；`completed`要求正revision和result。ready只checkout、唯一非NULL checkout_ref=`checkout:<非零UUID>`；rejected只checkout、唯一非NULL failure_code=`checkout_rejected|order_closed|order_expired`；closed唯一非NULL closed_at；captured唯一非NULL capture，capture.revision=reply.revision、正amount/CNY/ref/UTC6。cancel completed只closed/captured。普通command observation不是签名资金事实，绝不能据captured observation制造Evidence或Capture。

notification公共顺序=`schema_version,event_id,event_type,environment_id,merchant_id,order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision`；capture.confirmed无附加；checkout.failed附failure_code且transaction=NULL；refund.confirmed/failed附refund_id UUID；dispute.opened/chargeback.debited/reversed附case_id reference。后五种带transaction并归unsupported，不产生退款/拒付资金。本片不偷偷启用C1/C2。所有通知amount/revision正、CNY、UTC6；允许合法签名通知指向尚无订单的UUID。

## 5. 人工命令、SKU链和订单

commerce全表保留10种已知kind；本片只解释4种payment kind，voucher行仅核其一般actor→users与terminal(command字段相等)基础边，完整voucher语义由来源图负责。未知kind明确mismatch，不能当普通unrelated跳过。所有payment command.actor必须存在users，fingerprint_key_version必须NULL、request_digest为64小写hex；sku scope=platform，order scope=`tenant:<原tenant>`。每command恰一terminal，operation/kind/scope/actor及created_at与原command一致；accepted reason=accepted、resource非NULL，amount/funding_receipt/journal全NULL；rejected四nullable全NULL，reason按下表，且无payment业务/人类audit效果。

| kind | accepted resource与唯一业务效果 | rejected reason |
| --- | --- | --- |
| payment.sku.publish | resource=version.id；同operation的version+publish change，actor=version.created_by | payment_revision_conflict / payment_state_conflict |
| payment.sku.activate | resource=SKU.id；同operation的activate change | payment_revision_conflict / payment_state_conflict |
| payment.order.create | resource=order.id=command.operation；order.creator=actor，必须一个checkout outbox | payments_disabled / payment_sku_unavailable |
| payment.order.cancel | resource=order.id；同operation cancel intent.actor=actor，必须一个cancel outbox | payment_state_conflict |

每accepted command恰一个同action/target=operation的human audit，actor同command；sku tenant_scope必须SQL NULL，order为真实tenant；sanitized_diff恰空object，request_id是原调用者值，不能臆断必须空。反向所有以上audit/业务对象都必须回到唯一accepted command。accepted4种输入可从不变业务行重建当前v1 digest：外层顺序 `v=1,actor,scope,kind,target_id,body`；publish target=sku、body=`expected_revision`(change.before_revision字符串),`display_name`,`amount`(规范金额字符串)；activate target=sku、body=`expected_revision`,`version_id`(nullable),`enabled`；create target=sku_version、body=`sku_version_id`；cancel target=order、body={}。独立重算SHA。拒绝命令没有持久target/body，不能伪造原输入重算，只核原摘要词法/终态/无效果；request_id不在digest合同里。

SKU：每root有恰一条从before_operation_id=NULL到root.last_change的完整无环/无叉链；全部change被消费一次，不按时间/revision排序。first必须publish，before_revision=0、before_version=NULL、before_enabled=false，root.created_by=首publisher；first after_revision=1，仍NULL/false。每次publish必须新version，version序列从1连续、last_version为最终序号；publish不改变当前active version/enabled，revision+1。activate允许no-op且revision不变；只有(version,enabled)变更才+1，after enabled要求after version非NULL且同SKU；before字段精确等前驱after。root.revision/current/enabled/last_change/last_version等于链尾；所有version恰被自己publish change引用一次，created_by存在users，display/amount/wallet/CNY/900/env/merchant/payee形状与原marker绑定。created_xid仅作规范原字段，不从xid/created_at推跨事务先后或同事务认证。

订单：tenant/creator/sku_version存在；完整display/amount/wallet/CNY/env/merchant/payee快照逐字段等不变version；amount=wallet>0，merchant_order_id精确 `mps1.<env>.<order>`，expires_at=created_at+900秒。**没有持久order.status或本地order.revision**，不能发明DTO字段；provider revision独立核。当前停售/off/撤权/租户暂停、当前active version改变均不使旧order无效。取消意图只能一次，command/actor/tenant/order/独立cancel参数精确匹配；后来的合法capture/closed可与既有cancel共存，不能用当前capture存在反证取消时状态。

policy.update是独立既有路径：相关audit actor存在，tenant_scope=NULL、target=payment_policy、diff精确before/after bool且不同；其request_id保留。没有持久每次policy revision/前驱，不能把无序audit当完整policy历史，也不能拿今天的enabled要求旧accepted/rejected命令重新满足准入条件。

## 6. mutation、query、不可变原件和当前代数

每order恰一个checkout mutation；cancel intent与cancel mutation双向恰一；query仅是唯一监督器，不代表新mutation。mutation external_command_id是原唯一UUID，original_outbox_id=NULL，8KiB内canonical参数/digest与immutable order完整绑定；query external/parameters/digest=NULL、original存在且为同order checkout/cancel，最多一个supervisor/原mutation。command_id/canonical body不因重试改变。

全部outbox `generation=attempt_count`；mutation 0..10，query 0..20。pending必须0、无lease/current/error/unknown时刻；非pending>0；leased iff lease_until非NULL，leased error=NULL。lease即使已过当前时钟仍可能合法等待Expire，不要求扫描期间强制回收。所有非NULL时刻finite；不以created_at推lease/next_attempt的历史算式，原transition时间未持久。mutation不得current_query_id；query不得submission_unknown。已知error枚举精确沿021，不把任意driver文本当code。

mutation submission_unknown需要last_submission_unknown_at非NULL以及唯一query supervisor；第1..9次error恰六种fixed failure=`network|timeout|auth|protocol|environment|temporary`之一，第10次恰attempts_exhausted，不能以permanent或提前耗尽混入。mutation completed清error，存在本generation合法observation，或存在同mutation_generation、created_at>=last_submission_unknown_at的可信completed query；这是持久正见证，不意味着PSP已收款。completed/in_progress ACK与captured ACK需supervisor；普通ready/rejected/closed ACK不强制创建supervisor。**迟到observation可追加到旧generation且不改变已终态projection；不能因后来才出现一条in_progress/captured observation便要求过去未ACK的投影已经完成。** root终态检查区分已作为current正见证与单纯被保留的stale事实。

query_attempt canonical请求/digest、id、supervisor、原mutation/env/merchant/order/command/kind/parameter_digest全部绑定；created_generation∈[1,supervisor.generation]、mutation_generation∈[1,parent.generation]。每(supervisor,created_generation)最多一child，允许claim没有child；未知child无snapshot时后续claim继续使用原child，不能强制child.created_generation=当前代。current pointer必须同supervisor且指向最大created_generation的child；非current child须已有verified snapshot才可被替换。每child的mutation_generation沿created_generation非递减。current=NULL意味着尚无child。

所有Observation重算canonical/digest，父只能mutation，0<generation<=父generation；允许同代不同digest的多个观察，不能把HTTP观察当唯一signed snapshot。所有verified_query恰一原child，重算canonical/snapshot_digest，完整query echo逐字段相等；每child最多一snapshot，未返回child允许无snapshot。每个inbox重算canonical/business_digest，env/merchant/event列精确等原件。所有原件对应恰一Evidence及其Processing，哪怕stale、unknown_order、unsupported或尚pending也不得漏掉；所有Evidence反向恰选一个原件。

query completed需要current child+其可信completed snapshot，error=NULL。retry_wait允许无child（prepare前到期）、未知child原件缺失、非terminal snapshot，或回收后迟到保存的completed snapshot；后者只等下次claim收尾，不能据此倒推当前状态非法；20次不能retry。review_required按下述精确计次矩阵；**允许review后stale signed completed snapshot仍入库，不倒推必须重开/改成completed**。query失败重试无需新增child。精确state/error矩阵：query retry_wait限1..19；current=NULL/尚无child只允许timeout（Prepare前Expire），已有child但无可信current snapshot只允许六种fixed failure，已有可信current snapshot允许NULL或六种fixed failure（保留stale completed原件，不据其倒推重开或完成）；permanent/attempts_exhausted均不能停在retry。query review_required在1..19次恰permanent、第20次恰attempts_exhausted；mutation review_required只允许permanent（第10次耗尽仍submission_unknown）。mutation的unknown→retry允许的原query正见证必须满足同代cut、not_seen与预算；但当前下一代已推进时不能用今天的generation重新判过去cut。没有状态转移日志，不从现有行重建已丢失的每一次lease/ACK历史。

event/query_conflicts逐条有真实parent、canonical小写hex异体digest且不等parent原摘要、typed唯一；冲突原始canonical不保存，本片不能独立重算该异体摘要或历史传递签名。冲突自身是合法隔离事实，不自动判整图mismatch、改Evidence、替换first snapshot或触发第二资金。

## 7. Evidence、Processing 与语义review

通知归一化：capture.confirmed→capture；checkout.failed/order_closed→closed；其它checkout.failed→observation；refund/dispute/chargeback→unsupported。通知的amount/CNY/happened/revision及nullable transaction全部按原件投影，即closed/observation/unsupported也可能带完整非capture字段，不能强制这些字段全NULL。query只有completed/captured投capture字段；completed/closed投happened+revision、transaction/amount/currency=NULL；其它query为observation且这些字段全NULL。Evidence.verified_at=原件received_at，env/merchant/order和所有nullable值完全一致；happened_at<=received_at+5秒，**不比较处理时now**。

fact_digest独立SHA256、4-byte大端length UTF-8分帧：capture依次env,merchant,order,`capture`,transaction,规范amount,currency,UTC6happened,规范revision；closed为env,merchant,order,`closed`,UTC6happened,revision；其它为env,merchant,order,kind,原event_id或query_id。不是JSON hash，不用64-bit帧或NUL拼接。

processing与evidence一一；generation=attempt_count 0..20、leased iff lease_until非NULL。pending为0/无error；leased>0且error=NULL；retry_wait为1..19/temporary；processed为1..20/error=NULL；processing_review为1..20/permanent+恰一合法语义review，或第20次/attempts_exhausted（允许无review行）。terminal不因当前时钟/后来order/off/禁用改变。终态source的generation必须精确匹配；不能仅<=当前generation。尚pending/retry/leased本身不要求已有identity/revision/capture，不能因“还没有入账”判缺钱；它们不得已持有应随处理原事务一起提交的本Evidence identity/capture/review/revision。

对processed：observation无资金；closed有同order/revision/kind/fact的provider revision（其evidence可为较早同fact）；capture有同完整transaction tuple的合法identity+所选唯一capture完整资金图，不能只找同额receipt。unsupported不允许processed成功。语义review要求processing_review/permanent并匹配以下矩阵；每Evidence正式语义终态恰一review；额外不同reason即使分别看似合法仍为确定矛盾。仍逐条核每个reason的独立正见证，不以找到第一条忽略其它行。

| reason | 独立检查及身份形状 |
| --- | --- |
| unknown_order | order_id=Evidence.order；identity/selection=NULL。原INSERT证明当时无订单；现在后来出现同UUID订单合法，不能按当前是否存在推翻或重开资金 |
| unsupported_effect_review | Evidence.kind=unsupported；identity/selection=NULL |
| amount_mismatch | 已知order、capture amount不等order；identity/selection=NULL |
| capture_outside_window | 已知order、capture发生时刻不在[created,expires)；identity/selection=NULL |
| provider_revision_conflict | 同(env,merchant,order,revision)已有不同kind/fact，或closed与当前immutable identity违反时刻/revision部分序，或capture与已知closed违反该部分序；identity/selection=NULL |
| transaction_conflict | 同全局(env,merchant,transaction)已有identity但order/tenant/amount/CNY/captured_at/revision tuple不同；本review不借用别单identity，两个nullable均NULL |
| duplicate_order_capture | 当前Evidence有合法同order identity，review.identity为它；同order selection选另一个identity，selection_order_id=order；不替换首选、不产生capture |
| funding_amount_overflow | 合法selected identity及同tuple Evidence，两个FK准确指selected identity/order；无capture/effect/receipt/journal/credited/audit资金效果。重复同交易新event可保留相同overflow review，永不因后来余额改变重开 |

P1与order均严格CNY，未来>5秒原件不能存入合法Evidence；因此当前 `currency_mismatch/provider_time_invalid/provider_protocol_invalid` 不能用畸形/未验原件伪装“合法业务review”绕过原件检查。021 enum保留的 `processing_attempts_exhausted/command_attempts_exhausted/query_attempts_exhausted` review行当前正式Processor没有写入路径（processing耗尽以终态code派生）；本候选对这些**原行**标记 `ReservedReviewSemanticsUnproven`，不假装已有精确业务语义、不把它们静默当通过。根裁决这些保留原行按精确Unproven处理，同时全部通用FK/形状与确定矛盾仍核；未知enum为确定mismatch。普通合法第20次耗尽、没有额外review行则已完整定义，不记Unproven。

overflow只声明真实helper checkedAdd溢出后的永久无资金选择结果；没有历史锁后余额快照，不能用当前AVAILABLE/clearing重算当时overflow。unknown_order无absence序列，不能从墙钟先后补造见证。这两项是受审入口生命周期的明确历史信任边界，不令每条正常review天然indeterminate；它们永远不授新source资格。

## 8. Provider revision、identity、selection、capture 与效果

每provider revision为其原Evidence的同env/merchant/order/正revision/kind(capture|closed)/fact；原Evidence的processing已terminal。重复同key同fact可由后来的不同Evidence处理并引用旧revision；不可要求所有同fact Evidence都等revision.evidence_id。capture revision可因transaction_conflict/duplicate/overflow保留而无资金，不能从revision存在推导已到账。closed与该order每个合法identity必须同时满足identity.captured_at<closed.happened_at且identity.revision<closed.revision；不是对所有被review的capture原件强制此关系。

每identity完整tuple等原capture Evidence及immutable order，金额/CNY/历史半开窗口正确；原Evidence.processing_generation精确相等且state为processed/processing_review；identity自己的provider revision存在、kind/fact相同。全局transaction unique；每identity不是被本order首selection选中，就是有正确duplicate_order_capture review。不能从当前tenant active/creator权或处理晚于expires推翻合法identity。

每selection对应同order合法identity；first_evidence必须合法capture且完整env/merchant/order/transaction/amount/CNY/发生时刻/revision等该identity，不能只验两者各自“合法”。它保存的是原首选声明，不从created_at最小值猜first。selection恰有同identity的capture，或合法永久overflow review且无capture；两个结果不能并存。未被选中identity可合法永久留存，无capture不算缺receipt。

每capture等selection.identity/order、identity完整tuple、order.wallet_credit_amount，正A=amount=wallet、CNY；verified_evidence合法同tuple capture，processing必须processed且generation=capture.processing_generation。identity原Evidence可与capture所用Evidence不同，但其处理仍须自己的精确generation终态。每capture恰一个effect，effect所有identity/env/merchant/transaction值与capture相同，effect_kind/effect_id均固定capture；反向每effect都必须有同一个capture。

每capture恰一个sandbox receipt和一个journal及唯一credited/audit；receipt tenant/A/CNY/source_id和journal绑定，journal header精确kind=sandbox_capture、operation=capture.id、reference=order.id、actor=payment-sandbox-worker、CNY。精确两行：该tenant AVAILABLE +A、SQL NULL tenant的SANDBOX_FUNDING_CLEARING -A，含额外零行也失败。schema1 credited.operation=capture.id/tenant准确，payload恰一个字符串amount=A；capture audit.action=payment.capture.credited/target=capture.id/actor固定/tenant_scope真实tenant/request_id空/diff恰amount字符串。时间仅保留finite字段，不臆断capture/receipt/journal/audit/outbox时间相等或全局单调。

SANDBOX_FUNDING_CLEARING仅NULL tenant且余额非正；全ledger重放和全科目缓存归既有独立Ledger core。本片独立检查每条sandbox clearing分录均回到上述完整source；不能用其他tenant小计比较平台总余额。所有账户/line依旧全量原输入，缺journal/account、跨tenant customer line明确计异常而非忽略。

## 9. 双向扫尾与非本域行

每张33表都从自己的主键出发：root/child↔parent、command↔terminal↔accepted业务对象↔human audit、sku变化链全消费、order↔checkout/cancel、query↔supervisor↔原mutation、original↔Evidence↔Processing、revision/identity/review/selection各自合法出处、capture↔effect/receipt/journal/完整line/credited/audit。查询/主键扫描不得因FK假设省略任何反向；缺父/跨tenant/坏kind都不能在JOIN中消失。

对所有sandbox receipt、kind=sandbox_capture journal、SANDBOX_FUNDING_CLEARING line、payment human/capture/policy audit、引用任何sandbox capture operation的credited逐一反向核；不只沿已存在capture查一次正确效果。所有credited仍全量保留，manual/voucher归别的core；若operation不属于sandbox且header是别域，不在本core冒充其完整正确性。未知receipt kind、未知payment commerce/audit action或sandbox clearing挂未知journal为确定mismatch。被本域claim的operation若另有非credited projection效果也计错误，不能把副作用藏在“其它kind”。相关唯一键包括额外空operation的unrelated行，不按业务kind过滤去重。

其它用户/tenant/共享账户/普通journal/其它审计及projection行只参与原budget、编码、PK/typed唯一与基础line父边；不从本片推断其全部业务语义。完整七域聚合仍须root固定组合，不能把本地source graph与另Permit的局部ScanProof拼成全源通过。

## 10. 固定结果、三态与失败

`SandboxSourceGraphCheck`候选字段：`Scope="sandbox_stored_verified_source_graph_v1"; Scanned SandboxSourceGraphCounts; Status string; ScopedGraphConsistent bool; Mismatches SandboxSourceGraphMismatches; Unproven SandboxSourceGraphUnproven`。Counts为33组slice同名uint64；不输出ID、原文、金额、proof bytes、环境/角色/endpoint或key摘要。无数据库CoverageComplete/Ready/授权/修复字段。

Mismatch固定uint64方向候选：`DuplicatePrimaryRows,DuplicateUniqueRows,InvalidTextRows,InvalidNumberRows,InvalidTimeRows,InvalidEnumRows,InvalidCanonicalRows,EnvironmentRows,AttestationRows,PolicyRows,IdentityRows,CommerceShapeRows,CommerceTerminalRows,CommerceResourceRows,HumanAuditRows,SKUVersionRows,SKUChainRows,SKURootRows,OrderRows,CancelIntentRows,CommandParameterRows,CommandStateRows,ObservationRows,QueryAttemptRows,QueryPointerRows,VerifiedQueryRows,InboxRows,ConflictRows,EvidenceOriginalRows,EvidenceFactRows,ProcessingRows,ProcessingOutcomeRows,ProviderRevisionRows,TransactionIdentityRows,SelectionRows,ReviewRows,CaptureRows,EffectRows,ReceiptRows,JournalRows,JournalLineRows,CreditedRows,CaptureAuditRows,ReverseSourceRows,LineIdentityRows,UnknownSourceKindRows`。每方向每对应源行至多一次；重复为额外行数，链异常按所属SKU一次；相关source可以跨方向命中。所有已排除重复行仍计原Scanned和budget；不把坏金额当零补累计。

Unproven固定候选 `ReservedReviewSemanticsUnproven`；仅上一节有明确持久enum但语义未受审的保留review行可计。**禁止把缺表/权限/EOF/原件/代次/业务关系变成Unproven**。完整扫描后Mismatch非零→Status=mismatch；Mismatch为0且Unproven非零→indeterminate；都为0→consistent且ScopedGraphConsistent=true。两者独立保留，不因已有mismatch隐藏缺证。原本从未保存且本scope明确排除的HMAC/历史角色/动态准入/锁后余额/完整lease逐步重放，不伪造证明，也不无差别计Unproven阻塞所有合法历史。今后若将这些提升为必须证明的命题，必须先补持久见证和兼容迁移，不能改scope文字宣称已证。

固定错误为 `ErrSandboxSourceGraphRowsInput/Resource/Canceled/Timeout`；nilctx、总资源、父取消/到期分别映射，任一错误返回零Check。字段词法/形状/JSON/关系/保留enum业务矛盾属于正常完整数据mismatch；无权威假DTO不能使root跳过安装认证。root reader/lease/schema/SQL/Scan/Err/Close/EOF/Finish/core执行失败整域返回零，不保留partial summary、不泄漏driver/raw cause。

## 11. 验收与根裁决

纯core必须以独立手算/固定canonical与frame向量覆盖：全部33组原映射/空/nullable；单SKU多publish/activate/no-op/倒回旧version/分支环/断链/未消费change；accepted4种digest与rejected无效果；current停售/off/角色变更下合法旧order/晚capture/cancel后capture；通知/query两种原件所有union及严格canonical/重复字段/UTF16/错误类型/摘要；unknown/unsupported/observation/closed、相同fact多event/query、不同交易同order、同交易跨order、provider部分序两方向；identity与capture不同Evidence/精确同代终态；overflow永久选择无资金、pending/retry/leased合法未入账、第20次无review耗尽；stale observation/query仅保留原件、迟completed不重开review；未知child跨claim复用；每个源↔效果方向缺/多/同额错绑/跨tenant/额外零行；保留reason三态；typed重复不放大关系；100000/100001行、16MiB/+1bytes、单bytea界、JSON节点/depth界及context退出全零。测试不得import正式写侧算法作为expected。

根后续reader验收33表逐表0/1/127/128/129、provider复合key/单例true/空TEXT、全部Query/Next/Scan/Err/Close/游标/EOF故障、共享首末表budget、已认证原前驱绑定、ordinary明确不适用与sandbox完整图、同session所有core完成、正常唯一Finish及错误Abort。专用PG用真实APP/worker Service/Processor写入通知/query/多事件/overflow/review与合法未终态，再由受限真实维护身份读取；随机fixture管理员精确损坏每方向并对比全部业务表前后只读hash。没有实跑不称商业验收；本子片不得并行启动重PG。

根已裁决：保留的三种exhaustion review原行按精确ReservedReviewSemanticsUnproven处理，通用FK/形状照常核、确定矛盾优先；正式Processor第20次终态无review不缺证。review业务终态仅接受当前正式Processor形状，不以A0/宽DDL owner fixture另造成功历史。现授权本精确pure core与独立测试；reader/phase/authority接线仍须另冻。最终统一架构/HLD/LLD/验证/README、refresh、全仓检查和推送归根集成。

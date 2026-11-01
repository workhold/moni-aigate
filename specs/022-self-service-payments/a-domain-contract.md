# 022-A1 内部 Go 领域实施合同

2026-11-01，**根接口审查冻结，已实现并完成限定本地验收**。A0专项通过后按本稿并行实现领域、HTTP与控制台；[执行合同](a-contract.md)、[精确HTTP/DTO](contract.md#34-a的请求与安全响应精确形状)及[A0接口/DDL](a-contract.md#12-a0-账务接口与追加-ddl)保持冻结。证据见[阶段验证](../../docs/verification/funding-vouchers.md)。以下“拟”保留接口决策时的说明，实际源码已对应；B/C仍未冻结。

## 1. 按当前源码划分职责

已读现有[billing类型](../../internal/billing/types.go)、[transaction/role/账户工具](../../internal/billing/store.go)、[人工核款](../../internal/billing/orders.go)、[查询/投影](../../internal/billing/query.go)、[HTTP账务映射](../../internal/server/billing_handlers.go)和[配置](../../internal/config/config.go)。当前`Store`仅pool/instance/active，`New(pool)`不接收密钥；transaction为READ COMMITTED、lock_timeout 3秒、statement_timeout 5秒，只对40P01/40001最多重试两次。`role`会锁user SHARE；现有`Query`和`limitQuery`不具备A要求的actor复查/固定50/cursor边界，不能直接用于A。旧Money.UnmarshalJSON使用ParseInt，不能独立保证A的规范正整数字符串。

拟新增`billing.VoucherService`组合既有`*billing.Store`，保留`billing.New`及A0的`(*Store).ApplyFundingTx`/`VerifyFunding`签名。领域实现、命令和查询均在billing；HTTP只做鉴权/CSRF/再认证/MFA/Guard/ready、严格解析、有界准入和安全响应映射。根负责HTTP/config/main，UI独占web；本稿不提供新源码或实际文件分工授权。

独立`security.VoucherKeyring`只负责有版本、分用途的HMAC，不复用Secrets.Pepper/AES。依赖为config→security、billing→security、server/main→billing/config；billing不导入config/server/identity，security不导入billing。服务参数只有服务端actor/tenant身份，无客户端role、trusted、paid或可信金额。

## 2. 构造、密钥与启动检查

```go
// 拟放 internal/security；字段不导出，构造时深复制，之后不可变。
type VoucherKeyring struct { /* private active + key map */ }
func ParseVoucherKeyring(rawJSON, active string) (*VoucherKeyring, error)
func (k *VoucherKeyring) ActiveVersion() string
func (k *VoucherKeyring) HasVersion(version string) bool
func (k *VoucherKeyring) CodeDigest(version, voucherID, secret string) (string, error)
func (k *VoucherKeyring) CommandFingerprint(version, voucherID, secret string) (string, error)

// 拟放 internal/billing；不作HTTP DTO。
type VoucherOptions struct {
    RuntimeAllowed bool // VOUCHERS_ENABLED，排空重启后固定
    RecoveryMode   bool // 本次进程固定；独立事故写门禁
    Keys           *security.VoucherKeyring
}
type VoucherService struct { /* private store + immutable options */ }
type VoucherKeyVersions struct {
    Code        []string
    Fingerprint []string
}
func (s *Store) VoucherKeyVersions(ctx context.Context) (VoucherKeyVersions, error)
func NewVoucherService(ctx context.Context, store *Store,
    options VoucherOptions) (*VoucherService, error)
```

Parse严格遵A§6：UTF-8单JSON object、拒重复key/非字符串、key版本1..32 ASCII字母数字/下划线/短横线、最多128版本、canonical raw base64url且恰32随机字节、active必须存在。根已接受raw配置额外上限64KiB；错误只固定配置码，不格式化版本列表/密钥/原JSON。CodeDigest及CommandFingerprint仅允许各自固定域和合同的canonical JSON；不暴露可选任意用途域。调用方先严格解析85字节券，crypto方法也拒非法voucherID/secret。摘要比较常量时间。

VoucherKeyVersions是启动/维护内部查询，不是HTTP端点；只读全部vouchers.digest_key_version与commerce_commands.fingerprint_key_version，包括已兑/到期券及accepted/rejected命令。分别DISTINCT/C排序，各最多读取129个版本，union超过128或非法版本失败，不扫描全部券到Go；输出不进入日志/DTO。缺省off允许nil keyring；env=true时构造必须先读这些引用，验证active及全部历史版本齐全后才返回服务，DB失败或缺key不能开listener，也不能静默退为off。DB policy当时false仍要验证，避免管理员随后enable绕过检查。

env=false构造不要求keyring、不查历史版本；只读operation/receipt、既有approve/void/revoke及policy关闭仍可用。**create/redeem的所有POST，包括已知旧operation的POST重放，均在claim前409 vouchers_runtime_disabled**；旧accepted/rejected回执只能GET查询，不需要key。env=true运行中逐笔按原command/voucher版本查key，缺版本仍为voucher_key_unavailable，不能切active或判券无效。密钥不可热改；轮换保留全部被引用版本，走原排空重启。RecoveryMode拒绝全部A写（含policy），只读继续；实例Guard/ready仍由根外层检查，不以构造时结果缓存其授权。

## 3. 命令方法与输入

以下是内部Go输入，不直接JSON解码。ActorID、TenantID和RequestID只由已认证服务端上下文构造；OperationID来自严格body，目标ID来自严格path。服务内部仍复验canonical UUID、范围、非空、金额/乘积及UTC微秒时间，避免绕过HTTP误用。

```go
type VoucherCommandContext struct {
    OperationID, ActorID, TenantID, RequestID string
}
type CreateVoucherBatchInput struct {
    Command VoucherCommandContext
    FaceAmount Money
    Count int
    ExpiresAt time.Time
    PurposeRef, Reason string
}
type ApproveVoucherBatchInput struct {
    Command VoucherCommandContext
    BatchID string
    ExpectedRevision int64
}
type VoidVoucherBatchInput struct {
    Command VoucherCommandContext
    BatchID string
    ExpectedRevision int64
    Reason string
}
type ProposeVoucherRevocationInput struct {
    Command VoucherCommandContext
    BatchID string
    ExpectedRevision, ExpectedRedeemedCount int64
    Reason string
}
type ApproveVoucherRevocationInput struct {
    Command VoucherCommandContext
    ProposalID string // 无新revision/count/reason
}
type RedeemVoucherInput struct {
    Command VoucherCommandContext
    Code string // 仅当前调用内存，禁日志/格式化整个input
}
type IssuedVoucherCode struct {
    VoucherID string `json:"voucher_id"`
    Code string `json:"code"`
}
type CreateVoucherBatchResult struct {
    Receipt VoucherOperationReceipt
    Batch *VoucherBatchView
    Codes []IssuedVoucherCode
    SecretsAvailable bool
    Created bool // 内部决定201，仅确认提交的创建获胜者true
}
func (v *VoucherService) CreateVoucherBatch(context.Context, CreateVoucherBatchInput) (CreateVoucherBatchResult, error)
func (v *VoucherService) ApproveVoucherBatch(context.Context, ApproveVoucherBatchInput) (VoucherOperationReceipt, error)
func (v *VoucherService) VoidVoucherBatch(context.Context, VoidVoucherBatchInput) (VoucherOperationReceipt, error)
func (v *VoucherService) ProposeVoucherRevocation(context.Context, ProposeVoucherRevocationInput) (VoucherOperationReceipt, error)
func (v *VoucherService) ApproveVoucherRevocation(context.Context, ApproveVoucherRevocationInput) (VoucherOperationReceipt, error)
func (v *VoucherService) RedeemVoucher(context.Context, RedeemVoucherInput) (VoucherOperationReceipt, error)
```

平台命令TenantID必须空，由方法产生内部scope=platform；redeem必须有当前tenant，scope由方法产生tenant:<ID>。kind/target_id亦由具体方法确定，不能让HTTP传一个自由kind调用通用Execute。actor/scope/kind/target/body按A§6摘要；重读redeem按旧command指纹版本算body。RequestID仅审计相关，不参与命令业务摘要。

### 3.1 规范JSON的固定编码

所有摘要编码使用Go `encoding/json.Marshal`，固定默认HTML escaping=true、不缩进、无末尾换行；只编码声明顺序固定的专用struct，不用map、原始请求JSON或可选自由字段。字符串先通过UTF-8/控制字符及对应字段验证，不做Unicode归一化或额外trim。JSON外层字段声明及编码顺序严格为`v,actor,scope,kind,target_id,body`，v为整数1、scope用内部platform/tenant:<UUID>，target_id为目标UUID或显式null，不能omitempty。**operation_id与request_id在外层和body均不参与摘要**。

body按以下顺序声明，字段都不省略：create=`face_amount,count,expires_at,purpose_ref,reason`；batch approve=`expected_revision`；void=`expected_revision,reason`；revocation create=`expected_revision,expected_redeemed_count,reason`；revocation approve为空对象`{}`；redeem=`voucher_fingerprint`。金额/revision/兑数先转规范十进制字符串，count为JSON整数，expires_at先格式化为固定六微秒UTC字符串；不得让time.Time默认marshal改变尾零。券code HMAC和command fingerprint的JSON同样用专用struct，顺序严格`voucher_id,secret`，再按冻结域+单NUL+JSON字节计算。SHA-256/HMAC摘要输出小写hex；同语义输入跨重试/重启/轮换保持同字节，原版本指纹用于旧operation核查。

## 4. 事务、claim和savepoint

拟私有`runVoucherCommand(ctx, commandSpec, businessFn)`，不导出通用造额接口。一次调用的5秒总deadline包含pool wait、全部重试和COMMIT；根HTTP限制4个在途。保持现有事务隔离/timeout/已知回滚重试范围，但A私有runner显式区分begin/business/commit阶段，不改变旧transaction的资金路径语义。

1. 每个attempt开始清空全部返回值、Created、Batch、Codes及待提交decision；未确认提交的秘密不能流入外层结果。仅PG明确40P01/40001且能证明本attempt已回滚时，在**同一5秒总预算**内最多重试两次，包含COMMIT返回上述明确PG回滚错误；没有剩余预算不再试。不因一般网络、超时、constraint或结果未知重试。
2. 外层tx先INSERT command ON CONFLICT DO NOTHING；冲突等待后使用**新语句**读原command/terminal，不把INSERT与读合为同语句旧snapshot。绑定actor/scope/kind/target/body，异体409；同体复验当前读权并返回原receipt，不执行业务或生成code。不复用financial_idempotency。
3. 对新command按A§10取得业务根、policy及全部当前授权父锁并新语句复查；batch/proposal不存在404、失权403均回滚外层。先锁batch再读不可变proposal；无效兑换券也必须检查当前owner，不能向失权actor持久化拒绝。没有voucher时跳过不存在的batch锁，仍保持policy→tenant→membership→user的顺序。
4. **业务savepoint放在已有根/policy/授权父锁之后、首次业务写/账户初始化之前**。其后业务拒绝ROLLBACK TO仅消除业务/资金变化，不能丢掉写rejected terminal/decision所需的父锁。已经无业务效果的早期语义拒绝可直接写terminal。savepoint不是独立提交。
5. create在当前attempt生成秘密并只存摘要；状态命令写受控状态/审计；redeem只调用A0 ApplyFundingTx，source={voucher_grant,voucherID}，scope={锁内tenant,actor,已claim command}。A0拥有一次全账户ID序锁、账户锁后DB时间/expiry复验、journal/核销/receipt/唯一credited；A1不先发钱、不再次emit、不COMMIT helper。
6. 仅识别合同白名单的领域拒绝，回滚业务savepoint后写rejected terminal。stale revocation approval是唯一额外事实：同外层tx追加不可变rejected decision，无资金/业务audit/outbox；batch/auth锁仍持有。已有decision不写第二份。SQL/constraint/审计/outbox错误不得转业务rejected，整体回滚。
7. accepted写业务audit和terminal，COMMIT确认后才返回结果；create只有最终获胜attempt可带codes。COMMIT返回明确40P01/40001且证明回滚时只按第1项有限重试；其他明确PG拒绝/ErrTxCommitRollback已知未提交时清空秘密并commerce_unavailable，不重试。连接断开/取消等其余无法证明提交结果的情况清空秘密并commerce_outcome_unknown，绝不重试COMMIT或整个命令，也不能自动查不到后另发。保留原operation让客户端GET，普通503不作not_committed承诺。

角色同步复用现有role(tx,actor,required)，多个finance user按稳定ID顺序取得同类SHARE锁再新语句复查maker/checker；先符合A§10的batch根。租户写采用tenant SHARE→membership SHARE→user SHARE，再读当前active/owner；不用lockPrincipal，因为它要求API Key/model且属于推理路径。active券不因maker后续离职失效，兑换不再检查历史maker；审批时仍查当前maker/checker。policy PUT只admin user SHARE→policy UPDATE，永不回锁batch/账户。

## 5. Go安全DTO与nullable约束

```go
type VoucherOperationReceipt struct {
    OperationID string `json:"operation_id"`
    Kind string `json:"kind"`
    Scope string `json:"scope"` // 对外platform或tenant UUID
    Actor string `json:"actor"`
    Status string `json:"status"`
    Reason string `json:"reason"`
    ResourceID *string `json:"resource_id"`
    Amount *Money `json:"amount"`
    FundingReceiptID *string `json:"funding_receipt_id"`
    JournalID *string `json:"journal_id"`
    CreatedAt time.Time `json:"created_at"`
}
type VoucherPolicyView struct {
    Enabled bool `json:"enabled"`
    Revision string `json:"revision"`
    EffectiveEnabled bool `json:"effective_enabled"`
    RuntimeAllowed bool `json:"runtime_allowed"`
}
type CommerceCapabilitiesView struct {
    VouchersEnabled bool `json:"vouchers_enabled"`
    PaymentsMode string `json:"payments_mode"` // A固定off
}
type VoucherBatchView struct {
    ID string `json:"id"`
    State string `json:"state"`
    FaceAmount Money `json:"face_amount"`
    Count int `json:"count"`
    ExpiresAt time.Time `json:"expires_at"`
    PurposeRef string `json:"purpose_ref"`
    Reason string `json:"reason"`
    Maker string `json:"maker"`
    Approver *string `json:"approver"`
    ActivatedAt *time.Time `json:"activated_at"`
    TerminalActor *string `json:"terminal_actor"`
    TerminalAt *time.Time `json:"terminal_at"`
    Revision string `json:"revision"`
    RedeemedCount string `json:"redeemed_count"`
    RemainingCount string `json:"remaining_count"`
    CreatedAt time.Time `json:"created_at"`
    Expired bool `json:"expired"`
    ObservedAt time.Time `json:"observed_at"`
}
type VoucherDecisionView struct {
    ID string `json:"id"`
    OperationID string `json:"operation_id"`
    Status string `json:"status"`
    Reason string `json:"reason"`
    Approver string `json:"approver"`
    DecidedAt time.Time `json:"decided_at"`
}
type VoucherRevocationView struct {
    ID string `json:"id"`
    BatchID string `json:"batch_id"`
    ExpectedRevision string `json:"expected_revision"`
    ExpectedRedeemedCount string `json:"expected_redeemed_count"`
    Reason string `json:"reason"`
    Maker string `json:"maker"`
    CreatedAt time.Time `json:"created_at"`
    Decision *VoucherDecisionView `json:"decision"`
}
type VoucherPage[T any] struct {
    Data []T `json:"data"`
    NextCursor *string `json:"next_cursor"`
}
type VoucherRedemptionView struct {
    ID string `json:"id"`
    OperationID string `json:"operation_id"`
    TenantID string `json:"tenant_id"`
    VoucherID string `json:"voucher_id"`
    BatchID string `json:"batch_id"`
    Actor string `json:"actor"`
    Amount Money `json:"amount"`
    Currency string `json:"currency"`
    FundingReceiptID string `json:"funding_receipt_id"`
    JournalID string `json:"journal_id"`
    CreatedAt time.Time `json:"created_at"`
}
type FundingReceiptView struct {
    ID string `json:"id"`
    TenantID string `json:"tenant_id"`
    SourceKind FundingSourceKind `json:"source_kind"`
    SourceID string `json:"source_id"`
    Amount Money `json:"amount"`
    Currency string `json:"currency"`
    JournalID string `json:"journal_id"`
    CreatedAt time.Time `json:"created_at"`
}
type FundingSummaryView struct {
    Currency string `json:"currency"`
    TotalAmount string `json:"total_amount"`
    ManualCreditAmount string `json:"manual_credit_amount"`
    VoucherGrantAmount string `json:"voucher_grant_amount"`
    AsOf time.Time `json:"as_of"`
}
```

所有nullable字段用指针、无omitempty；空页Data必须非nil空slice、NextCursor=nil；金额仍用Money.MarshalJSON字符串，nil *Money输出null。SafeView的时间必须通过A专用marshal映射固定UTC六位微秒；不能直接依赖time.Time默认JSON省略尾零，建议内部小型wire-time函数/专用类型但不改变旧全局时间输出。输入HTTP用独立严格wire DTO/lexical检查再转Money/int64/time.Time，不能只用旧Money.UnmarshalJSON、time.Parse或宽松uuid.Parse作为完整校验。

RedemptionView与ReceiptView字段严格沿总合同§3.4：amount为Money、created_at为time.Time、其余安全引用为string；RedemptionView无nullable字段且currency=CNY。FundingSummary三累计金额为规范NUMERIC字符串（不能Money/int64），as_of为DB time。DB revision/count以::text读取并验证规范，不转float。Batch状态空值、六kind的resource/amount/receipt/journal及accepted/rejected reason完全沿总合同§3.4–3.5，不在Go中另造枚举语义。

CreateVoucherBatchResult是**内部结果，不直接JSON marshal**：根HTTP显式构造首次201、accepted重读200、rejected200三个既定envelope；防止Codes:nil被输出成null键或错误omitempty保留旧secret。其余mutation数据仅OperationReceipt；request_id仅HTTP外层。IssuedVoucherCode不能进入通用审计/error/log格式化。Go string清引用只能缩短存活，不声称物理安全擦除所有运行时副本。

## 6. 安全查询、policy与当前权限

```go
type VoucherReader struct { ActorID, TenantID string }
type VoucherPageInput struct { Cursor string }
type FundingReceiptPageInput struct { Cursor string; SourceKind FundingSourceKind }
type UpdateVoucherPolicyInput struct {
    ActorID, RequestID string
    ExpectedRevision int64
    Enabled bool
}
func (v *VoucherService) GetVoucherPolicy(ctx context.Context, actorID string) (VoucherPolicyView, error)
func (v *VoucherService) UpdateVoucherPolicy(ctx context.Context, in UpdateVoucherPolicyInput) (VoucherPolicyView, error)
func (v *VoucherService) PlatformCommerceCapabilities(ctx context.Context, actorID string) (CommerceCapabilitiesView, error)
func (v *VoucherService) WalletCommerceCapabilities(ctx context.Context, reader VoucherReader) (CommerceCapabilitiesView, error)
func (v *VoucherService) GetVoucherBatch(ctx context.Context, actorID, batchID string) (VoucherBatchView, error)
func (v *VoucherService) ListVoucherBatches(ctx context.Context, actorID string, page VoucherPageInput) (VoucherPage[VoucherBatchView], error)
func (v *VoucherService) GetVoucherRevocation(ctx context.Context, actorID, proposalID string) (VoucherRevocationView, error)
func (v *VoucherService) ListVoucherRevocations(ctx context.Context, actorID, batchID string, page VoucherPageInput) (VoucherPage[VoucherRevocationView], error)
func (v *VoucherService) GetPlatformCommerceOperation(ctx context.Context, actorID, operationID string) (VoucherOperationReceipt, error)
func (v *VoucherService) GetWalletCommerceOperation(ctx context.Context, reader VoucherReader, operationID string) (VoucherOperationReceipt, error)
func (v *VoucherService) GetVoucherRedemption(ctx context.Context, reader VoucherReader, redemptionID string) (VoucherRedemptionView, error)
func (v *VoucherService) ListFundingReceipts(ctx context.Context, reader VoucherReader, page FundingReceiptPageInput) (VoucherPage[FundingReceiptView], error)
func (v *VoucherService) GetFundingSummary(ctx context.Context, reader VoucherReader) (FundingSummaryView, error)
```

上面参数名是接口的一部分，平台方法的actorID与目标ID不能交换。根已接受这些精确原签名，不改为另一种具名input而使并行接入猜测；实施前置仍是A0专项通过并获根授权。所有查询禁止接收任意SQL、role、跨tenant覆盖或client limit。

平台读用当前active admin/finance_operator/finance_approver；policy GET/PUT仅admin。钱包读用当前active user+tenant+membership且role=owner/billing_viewer。每次查询在单SQL中形成auth范围并读取数据/DB时刻，或只读同快照事务；不先查权限后另用无scope SQL。须区分auth=false的403与已授权但资源不存在/其他tenant的404，不能用全局资源存在性泄漏跨tenant状态。GET platform operation仅platform scope；wallet仅当前tenant scope；GET不要求actor等于原制单人，但仍依角色限制，不能凭operation UUID绕权。

每页50、读51、DESC(created_at,id COLLATE C)，cursor绑定kind/scope/source筛选或batch；解码/版本/时间/ID/摘要严格边界在领域也验证，不复用旧Before/limitQuery。每页复验当前权限。funding summary单SQL读取三金额/同DB时刻，不把失败变为0。Query直接返回安全View，不能先返回原表record再让HTTP删secret字段。

## 7. 错误与实施门禁

业务拒绝返回`(rejectedReceipt,nil)`，禁止送入当前billingError（其字符串conflict会映射409）。拟独立安全VoucherError，Error()仅固定码、无原始DB文字/券/keys/版本；HTTP按显式白名单映射：invalid_request/invalid_cursor→400，forbidden→403，not_found→404，commerce_operation_conflict/voucher_policy_conflict/vouchers_runtime_disabled→409，voucher_key_unavailable/commerce_unavailable/commerce_outcome_unknown→503；领域RecoveryMode拒绝也返回commerce_unavailable且不claim。认证/限流/Guard沿根外层原合同。原始PG错误仅在私有runner作已知回滚分类，越过服务边界前安全化，不向HTTP公开可任意Unwrap的原文。

根已接受的接口审查项：服务包装与构造时DB验证；security独立keyring及64KiB配置上限；精确方法原签名；nullable/string/time wire映射；savepoint放在父锁之后；固定顺序/encoding/json的规范摘要；明确回滚且5秒预算内的40P01/40001最多两次重试；其余COMMIT未知清秘密且不重试；env=false所有create/redeem POST 409而旧回执GET无需key；查询当前角色/scope复验；A0 helper唯一资金/emit所有权。仍须A0真实PG专项出口，才允许A1编码。

A1验收至少覆盖原A矩阵，并增加：keyring重复配置/缺历史版本/startup失败，env off无key的读取及旧批次收尾；同operation不同target及轮换后旧指纹；savepoint回滚仍持父锁、stale decision一次；COMMIT丢回应不泄露codes、已知40P01重试仅最终一次展示；所有nullable/六微秒时间/NUMERIC字符串；单SQL读权限、分页撤权及未知404。未运行这些测试，不把本稿当作实现或验收。架构正文与manifest由根汇总后刷新，本子任务不执行refresh。

## 8. 根 HTTP 与启动接线细化

根已审查接受本节，只补具体接线，不放宽 A0 出口。`config.Config`增加`VouchersEnabled bool`和`VoucherKeys *security.VoucherKeyring`；配置名沿A，默认关闭。显式提供keyring时仍严格解析，启用时必须提供有效active/keyring；关闭且未配置时允许nil。错误只返回固定配置名/安全码，不附值。开关固定于进程，密钥不热加载。

保留现有`server.New`签名，增加显式`(*Server).InitializeVouchers(context.Context) error`，构造领域服务并只在成功后赋给`Server.Vouchers`。真实main在`New`之后、`Engine`/listener/worker/Ready之前调用；返回错误则启动失败并由原defer释放instance和pool。未完成初始化的commerce handler固定503，不能把启用失败静默当默认off；现有非commerce接口保持其原构造和行为。测试启用commerce时也必须显式初始化，真实main缺历史key启动失败单独验收。

HTTP复用现有严格JSON扫描器但用`map[string]json.RawMessage`核验顶层object、精确必填字段、非null及每字段lexical值，再转冻结领域输入。对count使用原始字节核验`1..100`纯十进制，对revision/金额使用canonical字符串+int64边界，对expires_at只允许Z/0..6小数；不能用通用Money解码器或宽松uuid.Parse代替。严格query使用`url.ParseQuery`并拒绝未知/重复/超限及坏UTF-8；非列表无query，GET不接受body。错误消息不反射任何输入。HTTP只调用精确领域方法，不直接写commerce/资金表。

SafeView中的`time.Time`不直接输出：HTTP在构建安全wire结果时递归映射已知领域结构、指针和slice中的时间为`2006-01-02T15:04:05.000000Z`，金额保持Money自带字符串编码，nil明确null。此映射只处理安全View，不能接收发行内部result或原DB record；发行响应由根逐键构造，codes只出现在已确认创建获胜的201响应。列表由根展开为合同顶层data/next_cursor/request_id，无二次data嵌套。

A限流单独保存全局limiter与最多4096个actor/scope/kind桶，互斥锁保护、10分钟惰性清理；桶满与超限均429/Retry-After=1。写入先沿session/CSRF/MFA/近期再认证和初步角色，再严格语法，再非阻塞取得至多4个许可及5秒context，全部错误/取消用once释放。policy PUT也属于A变更；平台桶按user，兑换桶按user+tenant。GET不消耗变更许可。metadata既有ready/Guard保持，领域锁内再查当前身份与角色。

原`GET /api/wallet`的六字段保持兼容，`total_credited`改由同tenant的`funding_receipts`累计，避免券到账后漏计；它仍非现金收入。独立summary才给来源分类和同步as_of，原report继续异步。A0期间根已新增真实HTTP人工核款兼容测试，不提前注册A1命令。

### 8.1 隔离真实main验收运行器

A1扩展既有`browser-qa`的私有`--voucher-fixture`模式；只在原显式本机moni_test/随机schema、受限app/projector、随机loopback端口中生成本轮独立HMAC key并传给子进程env，manifest仅记录该模式boolean，不存key/券/原command body。DB policy仍默认off，运行器不创建tenant/券/资金/调用；后续QA通过真实授权API建立合成业务。该模式与identity-fixture不能同时启用，避免将两个验收范围混为一个结果。

运行器最终计数增加credit/funding receipts/voucher redemptions/commerce commands，连同原模型请求计数记录；日志沿原有界安全过滤，退出清除私密登录凭据、schema/roles/listeners，HMAC只存在进程内/子进程env、不写公开文件。三个真实main生命周期模式（普通、身份、券）单独串行验证。此模式不注册付款模拟、发券后门或任意密钥API，不影响58123评审配置。

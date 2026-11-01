# 023-B HTTP、查询与客户端精确合同

2026-11-01。**根已完整复核并冻结本文HTTP/UI接线合同，授权在020核心之后独立实施。** 纯DTO/cursor与strict PUT、[Reader](b-reader-contract.md)、[Commander](b-commander-contract.md)均已按各自合同实施；核心资金已按[b-integration-contract](b-integration-contract.md)接入020及billing。HTTP/UI尚未完成验收。本稿补齐 [B主合同 §7–9](b-contract.md) 的 HTTP/query 目标；资金、初占、派发、来源与锁图继续由主合同和 [核心 SQL](b-core.sql)、[命令 SQL](b-commands.sql)、[权限 SQL](b-permissions.sql) 负责。本文只定义管理读写、历史分页与客户端，不改变推理重试或资金语义。

根本轮已裁决：cursor key 从既有 AUTH_SECRET 解码后的 32-byte Pepper 独立 HMAC 派生，不增加环境变量；所有 B 读最多 32 在途，其中 operation 核查另限 8，actor 总读 4 中核查另限 1。核查只使用自己的 2/s、burst4 速率桶。

纯切片精确API：`NewCursorCodec(pepper []byte) (*CursorCodec,error)`；`CursorBinding{Route,Scope,ScopeID,IdentityScope,Order string}`、`CursorPosition{At,ID string}`；codec的`Encode(binding CursorBinding, position CursorPosition) (string,error)`与`Decode(binding CursorBinding, token string) (CursorPosition,error)`沿§7，零值codec拒绝。`ParseConfigure(raw []byte) (PutPeriodicBudget,error)`沿§5 strict body值规则；`Fingerprint(kind,actor,scope,target string,in PutPeriodicBudget) (string,error)`沿§4 framing。类型字段逐一映射§1 JSON字段且金额/时间/revision为string，不读DB、环境、网络，不接main；Reader后续只能用本纯codec，不新增第二算法。任何错误只受控类型/code，不反射输入。该切片不授权migration或业务接线。

## 1. 公共值与精确 DTO

所有对象是精确字段集合；下列非可空字段必须存在，nullable 字段必须显式 null，不用 omitempty。集合空值为 `[]`，next_cursor 末页为空字符串，不能返回 null。

- `UUID`：小写标准连字符、非 nil UUID；不 trim、不接受大小写或其他表示的兼容转换。
- `Int64AmountString`：取值 `0..9223372036854775807` 的规范十进制字符串，语法 `0|[1-9][0-9]*`；不允许 Go uint64 放宽 BIGINT 上界。
- `Revision`：同上；未配置视图和首次 expected_revision 可为 `"0"`，配置后、operation revision 均须大于 0。
- `PositiveRevision`：Revision 去除 `"0"`。
- `Numeric100`：`0|[1-9][0-9]{0,99}`，最多 100 位；不使用 JS Number、float64、科学计数、正号、前导零或饱和值。
- `UTC6`：精确 `YYYY-MM-DDTHH:MM:SS.ffffffZ`，有限、真实日历值、year 0001..9999；拒绝 year0000、闰秒、offset、空白和精度变化。parse 后按同格式重编码必须等于原值。
- `LowerHex64`：64 个小写十六进制字符。request_id 是既有服务端追踪 ID，不参与业务身份或指纹。

```text
BudgetWindowView = {
  id: UUID, start_at: UTC6, end_at: UTC6,
  spent_amount: Numeric100, held_amount: Numeric100
}
BudgetAllocationView = {
  id: UUID,
  subject_kind: "customer_request"|"supplier_attempt"|"legacy_customer"|"legacy_supplier",
  subject_id: UUID, window_id: UUID|null, upper_amount: Int64AmountString,
  held_amount: Numeric100, spent_amount: Numeric100,
  state: "held"|"disputed"|"settled"|"released", created_at: UTC6
}
PeriodicBudgetView = {
  scope: "tenant"|"key"|"procurement", scope_id: UUID,
  revision: Revision, period_kind: "utc_day"|"utc_month"|null,
  enabled: boolean, limit_amount: Int64AmountString|null,
  state: "unconfigured"|"scheduled"|"active"|"disabled",
  effective_from: UTC6|null, tracking_started_at: UTC6|null,
  current_window: BudgetWindowView|null, carry_held: Numeric100, as_of: UTC6
}
PutPeriodicBudget = {
  operation_id: UUID, expected_revision: Revision,
  period_kind: "utc_day"|"utc_month", enabled: boolean, limit_amount: Int64AmountString
}
PeriodicBudgetOperationView = {
  operation_id: UUID,
  kind: "tenant_periodic_budget.configure"|"key_periodic_budget.configure"|"procurement_periodic_budget.configure",
  scope: "platform"|tenantUUID, actor: UUID, target: CanonicalPutPath,
  input_fingerprint: LowerHex64, resource_id: ControlUUID,
  revision: PositiveRevision, created_at: UTC6
}
CommandReceipt = { operation: PeriodicBudgetOperationView, result: PeriodicBudgetView }
SingleResponse<T> = { data: T, request_id: string }
PageResponse<T> = { data: T[], request_id: string, next_cursor: string }
ErrorResponse = { error: { code: SafeCode, message: string }, request_id: string }
```

`ControlUUID` 与 UUID 使用相同规范语法，但语义固定为稳定的 control 主键。金额和 revision 先按字符串验证，再用 int64/big.Int 转换；展示 CNY 只能使用字符串/BigInt。

unconfigured 必须为 revision="0"、enabled=false、state=unconfigured，period_kind/limit_amount/effective_from/tracking_started_at/current_window 全 null、carry_held="0"。这表示尚无周期账簿，不证明业务没有 held。已配置 view 只有 current_window 可 null；其余配置字段齐全，state 由同一 as_of 判定。current_window 的金额与边界来自父 view.as_of 的同一快照；不向 BudgetWindowView 增加未约定的 as_of 字段。

客户 allocation 不得为 disputed；legacy 两种 subject_kind 的 window_id 永远 null，普通两种永远非 null。upper_amount 是冻结上界，供应商后续合法 spent 可以超 upper；前端不得以此判坏或截断。列表不输出 baseline、source 金额、creation xid、controller marker、SQL、采购凭据、原证据正文或私钥。receipt.result 是当时不可变安全快照，`result.revision=operation.revision`、`result.as_of=operation.created_at`。result.scope 与 operation.kind 一致，result.scope_id 是规范 target 对应的 tenant/Key/account ID；operation.resource_id 是该目标独立且稳定的 control ID，不能要求它等于 result.scope_id。首次配置客户端不预知 control ID；服务端从实际 control 构造和复验该绑定，客户端验证其规范 UUID、目标及完整 receipt 关系，后续已知同目标 control 时再校验稳定性。

## 2. 路由、方法与可见性

| 路由 | 方法与结果 | 每次数据库当前权限 |
| --- | --- | --- |
| `/api/platform/tenants/{id}/periodic-budget` | GET SingleResponse<PeriodicBudgetView>；PUT SingleResponse<CommandReceipt> | active admin/finance_operator/finance_approver 读，active admin 写；目标 tenant 存在，不要求操作者属于目标 tenant |
| `/api/wallet/periodic-budget` | GET SingleResponse<PeriodicBudgetView> | 会话当前 active tenant，active user 和 active membership，role=owner 或 billing_viewer；仅本 tenant |
| `/api/keys/{id}/periodic-budget` | GET、PUT 同上 | active tenant/user/membership；owner 可读写本 tenant 全部 Key；member 只读写自己 Key；billing_viewer 只读自己 Key；写拒绝 revoked Key |
| `/api/platform/procurement-accounts/{id}/periodic-budget` | GET、PUT 同上 | active 平台三角色读，active admin 写；暂停 account 仍能管理，不改变其原 state |
| 上述四种资源路径追加 `/windows` | GET PageResponse<BudgetWindowView> | 与该资源当前读权相同 |
| 上述四种资源路径追加 `/allocations` | GET PageResponse<BudgetAllocationView> | 与该资源当前读权相同 |
| `/api/platform/periodic-budget-operations/{operation_id}` | GET SingleResponse<CommandReceipt> | active 平台三角色，且 operation.scope=platform |
| `/api/periodic-budget-operations/{operation_id}` | GET SingleResponse<CommandReceipt> | operation.scope=当前 tenant；再验证目标 Key 的当前读权 |

wallet 没有 PUT；只注册表内方法，其他方法沿既有 API 404。平台可管理 suspended tenant 的平台 cap；客户 wallet/Key 入口仍要求当前 tenant active。Key 的历史读取不因 revoked 而消失；过期 Key 的管理可见性沿既有 Key 管理，不将调用资格等同管理资格。客户 API Key 不能调用任何本节接口。

actor ID 和当前 tenant ID 只来自已认证 Cookie/session。路径 id 仅选择目标，仍须数据库证明归属；不接受 query/body/header 的 tenant、scope、scope_id、actor、role 或 controller ID 作授权。平台入口不能读取 tenant-scope operation，tenant 入口不能读取 platform operation；跨 tenant、不可见 Key、跨 operation 路由统一 404。当前身份自身失权返回 403，不用 404 隐藏其会话已失权。

## 3. Go 读取与命令入口

目标 package 为 `internal/periodicbudget`。以下Reader/Commander签名已独立实施，HTTP按这些固定接口接线，不能直接调用无权限 SQL view，也不复用带 caller-supplied scope/role/trusted bool 的通用 Execute。

```go
// DTO 字段逐一对应 §1，金额/revision/UTC6 为已验证 string 别名。
// nullable 用指针，集合不得为 nil；所有字段有明确 json tag。
type WindowPage struct { Data []BudgetWindowView; NextCursor string }
type AllocationPage struct { Data []BudgetAllocationView; NextCursor string }
type CommandReceipt struct { Operation PeriodicBudgetOperationView; Result PeriodicBudgetView }
type ConfigureResult struct { Receipt CommandReceipt; Created bool } // Created 仅首次新 control 成功；不输出该 bool

type Reader struct { /* pool 与不可变私有 CursorCodec */ }
type Commander struct { /* APP pool；不持 provider client、不创建新资金来源 */ }
func NewReader(pool *pgxpool.Pool, pepper []byte) (*Reader, error)
func NewCommander(pool *pgxpool.Pool) (*Commander, error)

func (*Reader) PlatformTenantBudget(ctx context.Context, actorID, tenantID string) (PeriodicBudgetView, error)
func (*Reader) WalletBudget(ctx context.Context, actorID, sessionTenantID string) (PeriodicBudgetView, error)
func (*Reader) KeyBudget(ctx context.Context, actorID, sessionTenantID, keyID string) (PeriodicBudgetView, error)
func (*Reader) ProcurementBudget(ctx context.Context, actorID, accountID string) (PeriodicBudgetView, error)

func (*Reader) PlatformTenantWindows(ctx context.Context, actorID, tenantID, cursor string) (WindowPage, error)
func (*Reader) WalletWindows(ctx context.Context, actorID, sessionTenantID, cursor string) (WindowPage, error)
func (*Reader) KeyWindows(ctx context.Context, actorID, sessionTenantID, keyID, cursor string) (WindowPage, error)
func (*Reader) ProcurementWindows(ctx context.Context, actorID, accountID, cursor string) (WindowPage, error)

func (*Reader) PlatformTenantAllocations(ctx context.Context, actorID, tenantID, cursor string) (AllocationPage, error)
func (*Reader) WalletAllocations(ctx context.Context, actorID, sessionTenantID, cursor string) (AllocationPage, error)
func (*Reader) KeyAllocations(ctx context.Context, actorID, sessionTenantID, keyID, cursor string) (AllocationPage, error)
func (*Reader) ProcurementAllocations(ctx context.Context, actorID, accountID, cursor string) (AllocationPage, error)

func (*Reader) PlatformOperation(ctx context.Context, actorID, operationID string) (CommandReceipt, error)
func (*Reader) TenantOperation(ctx context.Context, actorID, sessionTenantID, operationID string) (CommandReceipt, error)

func (*Commander) ConfigurePlatformTenant(ctx context.Context, actorID, tenantID string, in PutPeriodicBudget) (ConfigureResult, error)
func (*Commander) ConfigureKey(ctx context.Context, actorID, sessionTenantID, keyID string, in PutPeriodicBudget) (ConfigureResult, error)
func (*Commander) ConfigureProcurement(ctx context.Context, actorID, accountID string, in PutPeriodicBudget) (ConfigureResult, error)
```

Reader 构造必须验证 Pepper 恰好 32 bytes、复制输入并立即派生私有 cursor key，空 key/错误长度拒绝，不能回落无签名分页。Commander/Reader 均验证 ID/值，不信只来自 handler 的校验。它们自己限定总 deadline 为父 context 与读5秒/写10秒的较早者；HTTP 已从准入开始计时，repository 不重新获得完整预算。

Reader 每次新开 `REPEATABLE READ READ ONLY` 事务，授权和数据使用同一快照；不持业务行锁，不调用 pb_observe，不创建 control/window，不 emit。身份查询先行，在该快照验证 user/membership/tenant/Key 的实际状态和归属；只有 ID 来自 session，角色不来自缓存。角色撤销先于快照提交则拒绝；撤销与已开始快照重叠可完成该页，下页/下次 operation GET 必重验。每页不共享前页 RR，也不声称跨页一致快照。

当前 view 在一个数据 SQL 中 `WITH instant AS MATERIALIZED (SELECT pb_now() AS at)`，由同一 at 读 control、current window、carry 和 state。GET 不修改高水位；当前窗早于高水位返回 periodic_clock_regression。历史 windows/allocations 和原 operation 仍可在当前权限下读取，不因 clock regression 擦除历史。所有读先完整扫描、验证 DTO、关闭 Rows 并确认只读事务结束，再向 HTTP 输出；异常不返回半页。

## 4. 命令事务、指纹和回执

Commander 沿主合同已指定的 operation advisory → 源同步父锁/actor 锁 → control 顺序，不能在 control/account 后新增向前锁。operation advisory 必须用 B 独立域，不复用 A 定价/022/推理域；它只是串行哈希门，仍比较完整 operation UUID 与全部元数据，哈希冲突只增加等待。

1. 验证语法并取得已有 Guard/ready/恢复/MFA/CSRF/再认证准入；进入受限 APP 事务。
2. 按主合同取源/actor 锁，**锁后新语句**重新读取 active 身份与权限。Key 路径在 tenant SHARE→membership SHARE→user SHARE→Key UPDATE 后查当前 owner/role/revoked；不能在 SQL 末尾补这些前层锁。
3. 当前权限通过后，优先按全局 operation_id 查已提交回执。actor/kind/scope/target/safe_input 相同则返回原完整 receipt，不因现有 revision、calendar、cap 或新流量改变原结果；当前失权仍拒绝。异体或他人 operation 冲突为 policy_operation_conflict，不返回原元数据。
4. 新 operation 再比较 expected_revision。首次必须为0；已有必须等于当前 revision，即使期望同值但 revision 旧仍冲突。period_kind 不可变。首配完整导入≤1000；1001 回滚全部事实。no-op 不递增 revision，但保存新成功 operation 和安全 audit。
5. control/配置版本/legacy allocation/初占事实/operation/audit 必须同事务。操作结果在成功 INSERT operation 时使用一次 DB created_at，result.as_of 复用它。不能提交后另读当前 view 覆盖历史快照。
6. COMMIT 确认后才回成功。仅真实 40P01/40001 且已经知道整个事务回滚，才沿原有限事务重试策略在总10秒内重试原身份；不得因 timeout、失联或 COMMIT 不明新建 operation。

指纹固定 `SHA256(frame(kind, actor, scope, target, expected_revision, period_kind, enabled, limit_amount))`；每字段是 UTF-8 的4字节大端无符号长度前缀+原字节，无 null，enabled 仅 ASCII true/false。不包含 operation_id、request_id、时刻。kind/scope/target 从实际路由和当前身份域派生；target 为 §2 的规范 PUT 路径，不包含查询、尾斜线或自报 URL。safe_input 只存四个配置字段，不能用 JSONB 键序代替 framing。既有 [指纹 golden](b-verification.md#4-指纹golden) 必须同时进入 Go/TypeScript 测试。

首配且本次新建 control 成功返回201；后续变更/no-op/同 operation 重读返回200。operation GET 固定200。首次配置原操作后来重读也200；Created 不是 receipt 持久字段。失败没有 HTTP200 rejected commerce receipt；不得复用022的业务拒绝回执。

## 5. strict HTTP 与错误白名单

全部响应（含错误）设置 `Cache-Control: no-store`。只接受 Cookie/session，写需要同源、X-CSRF-Token、现有可选MFA及10分钟再认证，保留当前 Guard/ready/Recovery 门禁。默认不强制用户启用MFA。现有全局 metadata 的 ready/Guard、Session 认证 SQL 及会话中间件仍先执行，不宣称受 B 许可覆盖，也不从未认证 Cookie 推导 actor。随后取得 B admission，在 B 自己的任何 Reader/Commander 池访问之前再做 fresh ready/Guard；不跳过或降低既有全局 fence。正常请求取消不能用过期请求context错误fence整个实例。

PUT 是 UTF-8 单 JSON object，最多8192 raw bytes，读8193判断413；必须恰有 operation_id/expected_revision/period_kind/enabled/limit_amount。拒未知或重复键（含转义后同名）、大小写别名、尾随JSON、字段遗漏/null、JSON Number 金额/revision、非法UTF-8/孤立UTF-16 surrogate、值前后空白；JSON语法空白允许。只接受 application/json（可带 charset=utf-8），无压缩或 identity；其他媒体/编码统一400 invalid_request。不 trim UUID/金额/period，不接受 reset/timezone/spent/held/effective_from/actor。

GET 无 body/Transfer-Encoding；RawQuery最多4096 bytes，用完整 url.ParseQuery 并检查错误，禁止用丢错误的 URL.Query。全部接口拒绝ForceQuery（只有尾部`?`也400）。单资源和 operation GET 无 query；列表仅可选一次非空 cursor，拒重复、空值、未知query、非法UTF-8/控制字符/坏百分号转义；不接受 limit/offset/state/tenant/time/filter。PUT 不接受 query。路径 UUID 语法先验证400，已合法但不存在或不可见目标404。

query 语法、键集合或个数错误为 invalid_request；语法合法的唯一 cursor 值违反 §7 才为 invalid_cursor。严格校验覆盖 RawQuery 以及 percent decode 后的键和值。

| HTTP | 安全 code | 语义 |
| --- | --- | --- |
| 400 | invalid_request / invalid_cursor | 格式、规范值或 cursor 无效；无成功 operation |
| 401 | unauthorized / invalid_credentials | 没有有效登录会话；invalid_credentials为既有session中间件的固定身份错误 |
| 403 | forbidden / csrf_invalid / mfa_required / reauth_required | 当前权限或既有会话写门禁不足；不输出他人资料 |
| 404 | not_found | 合法目标不存在、跨tenant/不可见Key、operation跨路由或不存在 |
| 409 | periodic_budget_conflict | 当前CAS不匹配，包含首次非0 |
| 409 | periodic_calendar_immutable | 已配置后改 period_kind |
| 409 | periodic_legacy_import_requires_maintenance | 初配未决1001+，整个配置回滚 |
| 409 | policy_operation_conflict | 原 operation 元数据/输入异体，包含另一actor |
| 409 | periodic_revision_exhausted | 真实配置变化将溢出BIGINT revision；no-op不递增 |
| 413 | request_too_large | raw body超过8192 |
| 429 | rate_limited | 速率、并发或有界actor桶容量不足；Retry-After: 1 |
| 503 | periodic_clock_regression | 当前view/新准入的窗口退行；历史仍可查 |
| 503 | periodic_budget_invariant | 来源/投影/receipt/金额/时间等内部不变量异常 |
| 503 | periodic_budget_outcome_unknown | 命令COMMIT不能确认；只查原operation |
| 503 | periodic_budget_unavailable | DB/池/锁/语句/期限等不可用，不反射原错误 |
| 503 | recovery_mode / instance_unavailable | 恢复或既有Guard/ready门禁拒绝业务变化 |

未知 PG/驱动错误不能按字符串猜409或默认400；日志只记受控码/request_id/安全UUID，不输出 SQL、DSN、Pepper、cursor key、MAC、原body或operation快照。409 exhausted 是技术版本耗尽而非新的预算期，不允许reset规避。URI超限/方法不匹配等边缘错误沿既有HTTP层，不注册额外写入口。

目标 Go 错误入口为 `func ErrorCode(err error) string`：仅识别 periodicbudget 自己定义的类型化错误并返回上表 B 领域 code，其余返回空串。Handler 用固定 code→HTTP/message 表，不能返回 err.Error()；身份/Guard 门禁沿现有安全映射。CAS、日历、导入超限、异体 operation 和 revision 耗尽均在持正确锁的新语句中判定为相应领域错误；不能把任意 23514/23505 当这些冲突。DB 不变量错误统一 periodic_budget_invariant，未知驱动/池/期限错误为 periodic_budget_unavailable；只有不能确认命令 COMMIT 时为 periodic_budget_outcome_unknown。客户端对所有5xx仍按 §9 unknown，不依赖这些 code 推断回滚。新增 exhausted/outcome_unknown/unavailable 是本补充稿的安全 code 目标，须随主合同一并冻结，不冒充既有实现。

JSON响应先完整编码至有界buffer（最多128KiB）后输出；DTO/编码/只读COMMIT失败不返回部分data。命令已COMMIT而编码/连接失败，客户端仍按unknown核查；服务端不能为响应失败撤销或重复已提交业务事实。

## 6. 固定分页与读取查询

列表固定取51，验证全部行后只输出前50；有第51条时 cursor 来自第50条，禁止来自第51条。无后继页则 next_cursor=""；无配置或无记录为data=[]/next_cursor=""。cursor不是as_of快照，新的配置/收尾可以改变下一页观察到的金额；不可变排序键保证keyset边界，不承诺一次导出快照。

- windows：`ORDER BY start_at DESC,id COLLATE "C" DESC`，后页条件 `(start_at,id COLLATE "C") < ($last_at,$last_id COLLATE "C")`，固定control_id。
- allocations：`ORDER BY created_at DESC,id COLLATE "C" DESC`，同序 tuple `<`，固定control_id。
- 两个谓词均须有相应 `(control_id,time DESC,id COLLATE "C" DESC)` 索引或经实际 planner 证明现有索引满足；若需要索引只在后续冻结迁移追加。不能在每页 JOIN 全部上游证据、全control SUM或 OFFSET 扫描。
- 查询仅投影 §1 白名单列；跨 tenant Key 和 procurement 不混页。不得按客户端 cursor 内的 scope_id 直接构造 WHERE；先由可信路由与当前DB权限解析唯一control，再比较cursor绑定并查该control。

历史页权限用RR快照验证；列表不检查当前cap是否enabled、不写高水位、不因cap已超或clock回退拒绝合法历史核查。内部局部/全量完整性核验是独立职责，不能悄悄塞入每页热路径。

## 7. Cursor key、payload 与签名

### 7.1 Key 注入和轮换

`Pepper` 是 AUTH_SECRET 按现有配置规则 base64 解码后的原32 bytes，**不是base64文本**。只用于派生：

```text
K = HMAC-SHA256(key=Pepper,
    message=ASCII("moni-aigate:periodic-budget:cursor-key:v1"))
```

K复制为Reader私有不可变32 bytes；不拿AUTH_SECRET原值直接签cursor，不复用A或022的key/domain，不增加新env、不把key/密钥ID发给客户端。应用重启使用相同AUTH_SECRET时旧cursor仍可验；AUTH_SECRET轮换后不保留旧cursor keyring，旧cursor统一400 invalid_cursor，客户端清cursor重开第一页。轮换不是写重放或operation过期；原operation仍按当前权限可查。cursor没有独立TTL或跨页快照保证。

### 7.2 Payload 的精确字节

固定字段顺序，紧凑JSON，无前后空白/尾换行；Go以固定struct、SetEscapeHTML(false)编码后去掉唯一尾换行，禁止map或jsonb重序列化。所有字段必填且非null；v是唯一JSON数字，精确字符1，其余为ASCII字符串。

```text
{"v":1,"route":Route,"scope":Scope,"scope_id":UUID,"identity_scope":IdentityScope,
 "order":Order,"last_at":UTC6,"last_id":UUID}
```

上式排版换行不属于字节。scope_id 为路由目标 tenant/Key/account ID；identity_scope 为平台入口的 `platform`，或会话当前 `tenant:<tenantUUID>`。不从token生成session、身份或权限；每页仍验证当前读权。cursor不绑定actor，另一个具备相同当前读权的actor可以使用同一分页位置；换actor仍使前端请求epoch失效并清列表，不因cursor可验跳过授权。

| 资源入口 | windows route | allocations route | scope / identity_scope |
| --- | --- | --- | --- |
| platform tenant | platform_tenant_windows | platform_tenant_allocations | tenant / platform |
| wallet | wallet_windows | wallet_allocations | tenant / tenant:当前tenant |
| Key | key_windows | key_allocations | key / tenant:当前tenant |
| procurement | procurement_windows | procurement_allocations | procurement / platform |

windows 的 order 固定 `start_at_desc_id_c_desc`，allocations 固定 `created_at_desc_id_c_desc`。route/scope/scope_id/identity_scope/order必须与本请求经当前授权解析出的期待值完全相同。未知route/额外字段/非规范时间UUID/另一种顺序/NULL均拒绝；scope存在并不授予访问权。

### 7.3 外层 token 与校验次序

```text
P = 上述精确UTF-8 payload bytes
M = HMAC-SHA256(key=K,
    message=ASCII("moni-aigate:periodic-budget:cursor-signature:v1")
            || 0x00 || ASCII("pbc1") || 0x00 || P)
T = "pbc1." || RawURLBase64(P) || "." || RawURLBase64(M)
```

签名domain独立于派生domain；pbc1是被签版本，payload.v也必须1。RawURLBase64采用URL安全字母表、无padding；token恰三段，前缀必须pbc1。外层总长度≤1024 ASCII bytes，payload解码后≤512 bytes，MAC必须32 bytes/43字符；两个base64段解码再编码都必须逐字相等。禁止+、/、=、空白、额外段及非canonical尾bits。

处理次序：外层字节/段数/大小检查→有限base64解码与重编码检查→用独立K和原P做constant-time MAC比较→严格JSON解码/字段类型与canonical重编码比较→比较本请求期待绑定→使用已解析的边界。任何失败均400 invalid_cursor，不区分坏MAC、旧key或不存在版本，不输出输入。只允许服务端生成token，客户端只将opaque字符串原样带回。

### 7.4 Golden（公开测试材料）

公开Pepper为bytes `00 01 ... 1f`，仅测试，不是配置建议。独立 Python hmac/hashlib 与 Node crypto 计算已一致；Go纯codec已与golden一致；TS客户端仍待实现。

```text
K.hex = 45b2f9205c7c1baeb626e42d26e807b1070db901aa819def0ebcedda6da5eaf1
P（285 bytes）=
{"v":1,"route":"key_allocations","scope":"key","scope_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","identity_scope":"tenant:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","order":"created_at_desc_id_c_desc","last_at":"2026-11-01T00:00:00.000000Z","last_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd"}
M.hex = 049eebb45e93c3dca4fa977a08e559f30c2ec8f648cfcddebdc5fc5e4109fcea
M.RawURLBase64 = BJ7rtF6Tw9yk-pd6COVZ8wwuyPZIz83evcX8XkEJ_Oo
T = pbc1.eyJ2IjoxLCJyb3V0ZSI6ImtleV9hbGxvY2F0aW9ucyIsInNjb3BlIjoia2V5Iiwic2NvcGVfaWQiOiJjY2NjY2NjYy1jY2NjLTRjY2MtOGNjYy1jY2NjY2NjY2NjY2MiLCJpZGVudGl0eV9zY29wZSI6InRlbmFudDpiYmJiYmJiYi1iYmJiLTRiYmItOGJiYi1iYmJiYmJiYmJiYmIiLCJvcmRlciI6ImNyZWF0ZWRfYXRfZGVzY19pZF9jX2Rlc2MiLCJsYXN0X2F0IjoiMjAyNi0xMS0wMVQwMDowMDowMC4wMDAwMDBaIiwibGFzdF9pZCI6ImRkZGRkZGRkLWRkZGQtNGRkZC04ZGRkLWRkZGRkZGRkZGRkZCJ9.BJ7rtF6Tw9yk-pd6COVZ8wwuyPZIz83evcX8XkEJ_Oo
```

## 8. 独立准入与资源预算

所有B路由（含wallet/Key/历史）用B独立管理admission，不借推理、供应商、A定价、022或告警许可。沿 §5 完成既有认证链路后，在 B 自己的 fresh Guard/DB池/事务前申请；拒绝无排队、无新goroutine等候。限额按服务端actor UUID，不能按客户端IP/tenant/游标改桶。速率为actor级，不另发明未裁决的全局QPS。既有 metadata Guard 和认证 SQL 位于 B 准入之前，仍受既有请求 context/池规则约束；表内并发与 deadline 只承诺认证后的 B 工作，不宣称覆盖全部 HTTP 生命周期或匿名认证流量。

| 类别 | 全局在途 | actor在途 | actor速率 | B工作总deadline |
| --- | --- | --- | --- | --- |
| 普通GET（view/windows/allocations） | 所有B读共享32 | 所有B读共享4 | 5/s，burst10 | 5s |
| operation核查GET | 同一总读32内另限核查8 | 同一actor总读4内另限核查1 | 独立2/s，burst4，不消耗普通读桶 | 5s |
| PUT | B写8，与读独立 | B写1 | 1/s，burst2 | 10s |

每actor一个有界entry保存读/写/核查三类桶与active计数；entry总数4096，空闲≥10分钟且全部active=0才可惰性回收。容量满、并发满、速率不足都429+Retry-After:1；不能淘汰在途entry刷新其额度。无参数化scope桶乘法增长。

检查并发/容量、消耗相应速率token、增加计数在短mutex内完成；不得持mutex访问DB。失败不增加active；速率token一旦消费不返还。permit release用sync.Once，成功/早退/取消/编码失败均释放一次；context取消不能在repository仍运行时提前释放并发并让旧任务继续无计数。总deadline从admission开始，含Guard、pool等待、RR、行锁、语句、编码，不在获得连接后重置。

DB现有lock_timeout=3s、statement_timeout=5s不放宽；父ctx更早到期时优先取消。Repository rollback用现有独立有界清理，不将正常ctx取消判为实例失权。路由须关闭Rows/释放连接，不能在持事务时等下一页或客户端操作。

## 9. 客户端安全 epoch 与未知 operation

前端分别维护 authEpoch、viewEpoch、commandEpoch；身份/当前tenant/角色变更、logout、路由目标切换和页面关闭均使相关epoch递增并AbortController取消旧请求。列表换目标/第一页刷新清cursor和旧行，epoch绑定actor+identity_scope+规范资源路径+列表kind。只有captured epoch仍等于当前值的响应能写状态，取消不能当作服务器rollback证明。

命令状态严格为 editing→submitting→confirmed 或 known_failure 或 unknown。submitting和unknown禁止同目标新PUT；不按重试按钮生成新operation_id，不通过页面刷新解锁。提交前生成一次operation UUID，固定本次输入和指纹；**发出 PUT 前必须成功持久化下述安全 journal metadata**，持久化不可用或失败则停在 editing、不给服务器发送请求。表单在in-flight不能编辑成另一份请求。journal 中任意未终结记录（含发送前已落盘的 submitting/pending）在刷新/重挂载后都按 unknown 恢复；即使客户端后来知道未发出请求，也只查原operation，不通过新ID猜测回滚。

- 201/200 只有严格解码完整CommandReceipt且校验operation_id、kind、actor、scope、target、input_fingerprint与本次metadata全等，resource/scope/revision/time一致，才confirmed。result显示历史as_of；另行GET当前view不覆盖该历史归因。
- 400/401/403/404/409/413/429仅在严格解码 ErrorResponse，且 code/HTTP 为 §5 的明确已知配对时按known_failure处理，显示安全错误；未能严格解码的任何状态仍为unknown。409重新拉当前view后由用户编辑新命令。401/403使身份/权限上下文重新核验，不能继续使用旧表单权限。
- 网络中断、客户端timeout/abort、任何5xx、HTTP成功但坏/截断DTO、绑定不符、未知COMMIT均unknown。即使错误code说“不可用”，客户端也不能据此推断未执行。
- unknown仅GET原operation；200通过全部绑定验证才confirmed；404保持unknown，显示“尚未查到，结果仍待确认”；403/401停止核查并要求当前权限恢复，不能改用平台路由枚举；错误/429保留原ID并退避。
- 核查使用既有独立admission，UI最多单在途，自动间隔1/2/5秒、之后最多每5秒，仅页面可见且身份epoch相同时；429尊重至少Retry-After，hidden/offline/卸载停止。页面恢复可按原metadata继续核查，不重放PUT。

安全journal只允许保存版本1、operation_id、kind、actor、identity_scope、target、input_fingerprint和未终结状态pending这些metadata，不保存整个body、限额表单、Cookie/CSRF、MFA、cursor/key/原证据。按actor+identity_scope+target隔离；重新挂载必须先确认当前身份，再恢复相同上下文的unknown记录。不同身份的journal不能自动查询或展示其operation；注销清当前可见状态和敏感内存，已取消但未知写不能在同身份重返时被误当未发生。confirmed/known_failure 先将状态终结并可靠删除对应pending记录，再允许新命令；删除失败继续阻止新PUT并显示已确认结果，刷新后仍可用原ID重新核查。

持久化固定使用同 origin 的 IndexedDB：数据库 `moni-aigate-periodic-budget-v1`、version=1、object store `pending`，复合主键为 `[actor, identity_scope, target]`（target 是规范路由目标）。以单个 readwrite 事务原子读取并且仅在无旧记录时新增；**事务 complete 后**才能发PUT，不能把单个 request success 当作提交。已有pending时只恢复原metadata，不覆盖operation_id；同存储域多tab因此共用一个目标pending。禁止以localStorage的先读后写冒充原子锁，也不能用sessionStorage或内存替代关闭/重启后的持久恢复。打开数据库、schema/version、读写或事务失败均阻止发送并给出可读提示；坏metadata不能静默删除后新发。终结删除也在readwrite事务中重读复合key并比较原operation_id，只有相同记录才能删除，避免晚回清掉新操作。该互斥只覆盖同浏览器存储域；服务端当前授权、operation唯一性与CAS仍是权威。

即使当前GET budget与用户输入相同，也不能用它证明原operation成功；404、经过很久或浏览器重启都不授权换ID重发。已确认或明确失败后才能终结对应journal；unknown保留原ID，必要时提示按该编号人工核查，不能提供“强制解锁并重发”快捷操作。

## 10. 实施与验收门禁

| 门禁 | 必须验证的具体判据 |
| --- | --- |
| DTO/strict输入 | 精确字段/null联合、8KiB/8193、重复/转义重复/别名、坏UTF-8/surrogate/尾JSON、JSON Number与大于2^53金额、BIGINT/100位端点、year0000/无穷/offset/闰日/六微秒 |
| 当前权限 | 平台三角色读/admin写；wallet owner/billing_viewer；Key owner/member own/billing_viewer own read；跨tenant与operation跨路由404；revoked只阻新写；API Key/CSRF/MFA/reauth/recovery/ready拒绝 |
| DB快照与撤权 | 每页RR current user/tenant/membership/Key归属，不信session role；撤权/换owner先提交拒绝，已开始快照可结束但下一页/原operation GET拒绝；GET不产生control/window/event |
| 配置与回执 | first201/replay200/no-op不增revision；旧CAS即使同值409；immutable calendar；1001整回滚；无audit/版本/初占或receipt不能提交；原result不随流量/调配置改变；已提交但丢响应GET原ID收敛 |
| Cursor golden | Python/Node golden与Go独立实现一致；改变Pepper/domain/prefix/payload任一byte失败，AUTH_SECRET轮换400；禁止直接Pepper签名和跨A/022域 |
| Cursor strict | 1024/1025外层、512/513payload（即使MAC有效）、MAC31/32/33、padding/+/斜线/非法尾bits、多段、重复字段、v=1.0/1e0、重排/空白/未知字段、wrong route/scope/id/identity/order、非法UTC6/UUID全拒绝 |
| 分页 | 0/1/50/51/100/101行、相同time不同UUID、C顺序、cursor来自第50条、无重复/遗漏边界、末页空串；快照后修改金额不伪称跨页一致；全页验证失败不半页 |
| 查询上界 | 专用PG真实APP，windows/allocations大样本EXPLAIN与索引，固定control+51，不扫全部来源历史、不写模型/资金/采购源；pool/statement超时关闭Rows并释放连接 |
| 准入 | 普通读32/actor4、写8/actor1；核查总读32内8且actor总读4内1；三种独立速率、不跨模块占许可、4096容量/10分钟/active不淘汰、取消/异常/release-once/无队列 |
| unknown与epoch | COMMIT前后故障、200坏DTO、timeout/取消/404核查不重发；hash/目标不符不能confirmed；旧身份/目标/列表晚回忽略；IndexedDB事务complete前不发PUT、多tab同key原子唯一、持久化失败不发、刷新pending→unknown、晚回不删新ID/终结清理失败不解锁；hidden轮询停、429退避 |
| 完整交付 | B1/B2商业PG、HTTP与TS严格测试、真实main/浏览器、进程崩溃/未知提交、只读核验器与最终架构reviewed检查另行通过；本稿golden不是这些能力验收 |

## 11. 接线裁决与文件所有权

服务端新增`Server.InitializePeriodicBudgets()`，从APP pool与解码后的`Config.Secrets.Pepper`构造两个私有不变量实例；main在listener前显式调用并在失败时安全拒绝启动。未初始化handler返回503，不回落无签名cursor。路由沿现有session/MFA及platform中间件，领域Reader/Commander仍每次复验当前数据库权限；无当前tenant的客户入口返回forbidden，不引入tenant_required码。

fresh Guard沿既有独立Background最多1秒，避免普通请求取消使实例永久fence；admission所得父deadline不重置，Guard前后均检查请求取消/期限与ready，剩余预算不足1秒时直接unavailable且不启动Guard，过期不再进入repository。独立Guard仍持permit完成，不声称取消能撤回已开始的所有权验证。既有Session在领域入口之前可因user停用、session到期/吊销返回401 invalid_credentials；§2的当前身份403限于认证成功后的领域复验，不改变全局身份错误语义。

控制台采用独立目标页：`/periodic-budget`为wallet只读；`/periodic-budget?key=<UUID>`为Key；`/platform/periodic-budget?tenant=<UUID>`或`?procurement=<UUID>`为两类平台目标。目标query必须精确唯一，非法/混合目标不发送请求；原wallet/Keys/tenant/采购页面只增加入口。客户端GET超时6秒、PUT11秒，外部AbortSignal真实取消fetch；不增加自动PUT重试。权限变更保留pending journal，401/403停止核查并刷新当前身份。resource_id首次未知，后续同目标已确认control ID仅保存在内存作稳定性校验，不扩充journal允许字段。cursor只opaque传递，浏览器不持签名密钥。

服务端handlers/input/admission与server/main接线由根负责；HTTP专项由独立测试负责人新增文件；前端负责人独占新增periodic-budget模块、components和独立view/tests，router/App及现有业务页由根集成。上述并行不授权改共享资金、020或角色。所有结果汇总后根复核架构/证据并执行reviewed refresh；HTTP、真实main/浏览器/预览和整片商业验收继续逐项记录，不能以合同冻结代替通过。

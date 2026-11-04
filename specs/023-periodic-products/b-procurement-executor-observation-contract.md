# B-P4 A5-S2：未知提交的只读回执观察合同

2026-11-04；本页先冻结详细设计，随后已实现显式Observe并通过限定实际APP验收，准确源码、结果与未验矩阵见[验证记录](../../docs/verification/procurement-execution-observer.md)。承接[A5 总合同 §7](b-procurement-prospective-executor-contract.md#7-unknown-commit-与只读观察)、[事务运行器合同](b-procurement-executor-transaction-contract.md)、[执行合同](b-procurement-execution-contract.md)和[A2 安装合同](b-procurement-attempt-installation-contract.md)。不修改三根 SQL、材料摘要、catalog、ACL 或业务 DTO，不启用默认服务、HTTP、恢复任务或模型调用。上位合同 §7 不因本页扩张；本片不关闭完整A4/A5/S4或R2。

## 1. 结论、API 与状态

观察只回答：**本次 Runner 已经实际收到的准确回执，其匹配原件是否在一个新的只读快照中存在且通过当前支持域认证**。`receipt_present` 不证明该次调用是 applied、replay 或其 COMMIT 已确认，不发行派发、重新申请 operation、重试原调用或生成请求的许可。没有读到不能证明原事务永远未提交；服务器仍可能结束原事务。

固定入口位于 `internal/procurementexecution`：

```go
func (r *Runner) Observe(ctx context.Context, h *ObservationHandle) (Observation, error)

type ObservationState string // receipt_present | unresolved
type ObservationReason string
type Observation struct {
    State  ObservationState
    Reason ObservationReason
}
```

`Observation` 只含两个安全枚举，不含 Attempt、Command、账户/租户/请求/operation/root/source ID、输入、SQL、原件 JSON、恢复 token 或授权布尔值。没有从 Observation 重建 mutation DTO 的公开函数。`receipt_present` 的 Reason 固定空串、error 为 nil；所有 `unresolved` 返回固定非空 Reason 及非 nil 的安全 typed observation error，不出现 `unresolved,nil`、`receipt_present,error` 或零 State。

observation error 的 `Error()`、`PublicCode()` 与 fmt/GoString/JSON/Text 固定为 `procurement_observation_unresolved`，不能展开 cause、handle 或输入；Reason 由返回值单独承载。私有 cause 通过 `Unwrap()` 保留：本次观察 cause 在前、原 unknown COMMIT cause 在后，可用 `errors.Join`，不复制或拼接 PG 文本。只有本次 probe 的主因用于 Reason 分类，必须先分类再 join；不能从 join 后的另一条 SQLSTATE 推导观察结果。完整原 cause 仅供受信内部诊断，HTTP/普通日志不得遍历序列化。

| Reason | 精确定义 |
| --- | --- |
| `invalid_handle` | nil/零值/其它 Runner 的 handle、无有效发行 stamp，或与冻结 invocation/schema 不一致；Acquire 前拒绝 |
| `invalid_context` | nil 调用 context；Acquire 前拒绝 |
| `identity_unavailable` | 有效过程内 handle 未保有本次完整解码的必要身份；不能生成 ID、猜 root 或用旧 attempt 补足 |
| `receipt_absent` | 按捕获的准确 operation/attempt ID 查询为零行；只说明该 snapshot 无此原件，无 PG902/906 的含义 |
| `receipt_mismatch` | 原件存在，但本次输入身份或捕获的原回执字段逐值不等；不寻找其它同 request/sequence/root 作为替代 |
| `receipt_incomplete` | 原件的必要来源/根/步骤/审计/journal 缺失、部分 NULL、多行或多义，且尚无 validator 的准确分类；不能降为 legacy |
| `history_invariant` | 本次只读 validator 的真实 SQLSTATE 为 PP902；原件不一致，不代表原 mutation 未发生 |
| `unsupported_history` | 本次只读 validator 的真实 SQLSTATE 为 PP906；包括严格识别的 legacy 及当前未支持的后继域，均不降级成功 |
| `observation_protocol` | 固定查询列名/OID/NULL/scan/排流协议错误；已有真实执行/流错误时保留真实主因 |
| `observation_timeout` | 本次 context 取消/到期或真实 57014；不解析 Message 判断来源，不创建新预算重试 |
| `observation_unavailable` | pool/连接、Begin、权限、其它 SQLSTATE 或网络失败；包括 PP901/PP903–905，保留原 cause |
| `observation_cleanup_failed` | 只读事务未确认 rollback+idle，或必要销毁失败；即使此前读取匹配，也保守 unresolved |
| `observation_panicked` | 观察过程 panic；安全常量、零业务结果，清理后不含 panic 值 |

Reason 不是重试信号或 mutation 业务错误映射。特别是 `receipt_absent`、`unsupported_history`、PP902 与 PP906 不能翻译成 not_found、未扣费、已回滚或安全重放。

## 2. 过程内 handle 与身份冻结

handle 仅由 [Runner](../../internal/procurementexecution/runner.go) 在 COMMIT 可能已发送且结果 unknown 时发行；外部可持有 opaque 指针，不能构造有效材料或传 SQL/ID 观察任意目标。私有材料为：

```go
ObservationHandle{
    issuer *Runner, stamp *runnerStamp, schema string,
    invocation Invocation,
    attempt AttemptReceipt, account AccountReceipt,
    commitCause error,
}
```

验证须满足 `r != nil`、受信 pool 可用、`r.stamp != nil`、`h != nil`、`h.issuer == r`、`h.stamp == r.stamp`；stamp 是不为零大小的私有 allocation。复制 Runner 或从相同 pool 新建 Runner 均不获得旧 handle 的发行身份。外部零值 `ObservationHandle{}` 不有效；不提供构造器、setter、导入/解码 token、业务 ID getter 或持久化恢复格式。

Runner 每次尝试使用新的私有 Invocation 参数副本和完整 decoded 回执，不能将前一次失败重试的 ID 带入最终 unknown。只有通过 S1 元数据、恰一行、完整扫描/排流和 union 检查的回执才能存入 handle；缺失/部分扫描值不能推导身份。`schema` 在 Runner 已有的本地 Invocation 校验中冻结：按固定 S1 template 截取一个 quoted identifier、反解双引号、`validSchema` 检查，再用 `pgx.Identifier` 重建整个固定语句并要求字节相等；不能按 `.` 拆分，也不能以 SQL/search_path/catalog 探测代替。schema 可合法含点、引号或模板相似文本。

Observe 在 Acquire 前复核 seal、根类型、冻结 schema 与 S1 query/参数形态、捕获回执及其身份一致性。账户两个根须保有 operation/actor/kind/target、完整旧快照/CreatedAt、准确 root 及 nullable journal；attempt 须保有完整五字段、request/sequence 与原 tenant/四字段。applied 必须有准确新生成 attempt/root/source，且 source=attempt；replay 的捕获 root/source 必须仍是真 NULL。不能因为后来查询找到了 origin，就改写捕获的 replay union。

handle、UnknownOutcome 的 value/pointer、`%v/%+v/%#v`、嵌套 fmt、JSON 与 Text 均仅输出固定常量。Observe 不修改 handle；允许调用方明确再次观察，但每次都是独立读，Runner 不自动轮询、计时重试或发消息。

## 3. 只读连接、期限与清理

调用方须提供**新的观察 context**，不是把已经结束的 mutation context 藏在 handle 中继续使用。Observe 对传入的新 ctx 派生 `context.WithTimeout(ctx, 5*time.Second)`；调用方已有更短 deadline/取消仍有效，不用 Background 绕过调用方。该独立工作预算覆盖一次 Acquire、Begin、固定设置与全部读证，无失败重试、无逐查询续期。传入已结束的 ctx 时安全 unresolved，不扩大到新 5 秒。

从原 Runner 绑定的同一受限 APP pool 新 Acquire 一次连接；原 unknown 连接已由运行器销毁，不复用其事务/快照。固定 `BeginTx` 为 `pgx.RepeatableRead` + `pgx.ReadOnly`，不设 DEFERRABLE。随后只执行一次固定：

```sql
SELECT pg_catalog.set_config('lock_timeout','3s',true),
       pg_catalog.set_config('statement_timeout','5s',true)
```

所有原件查询与 validator 在这个事务、这条连接和同一个 RR snapshot；设置 SELECT 也可能是取得事务 snapshot 的首个实际语句，不能声称快照一定延迟到第一条业务表读取。每条 statement 的 5s 不替代总工作 context 的 5s。普通 MVCC/AccessShare 不等于经营锁；禁止 FOR UPDATE/SHARE、显式 LOCK、advisory lock、SET ROLE、重新授权、mutation/root、PB Commander、观察/派发/资金 writer、临时表或持久写。

只读读证结束不发送 COMMIT；统一 Rollback 结束。退出路径持有连接直到清理完成：使用脱离工作 ctx 的**同一个最多 5s 的绝对清理 context**，Rollback 与必要的 Hijack+Close 共用剩余预算，不因 rollback 失败再给 close 新 5s。rollback nil、连接未关闭且 TxStatus=`I` 才 Release；否则 Hijack 后销毁，不归还污染连接。Begin 失败同样只有确认可用 idle 才 Release。所有 rows 先关闭/排流，再 rollback。Acquire 失败不执行事务清理。

读证须在工作 ctx 有效时完成；完成后停止工作计时并进入独立清理，不在清理阶段发新查询。只有全部匹配、必需 validator 成功且清理确认健康才能发布 `receipt_present`。清理失败优先 Reason=`observation_cleanup_failed`，保留此前 probe cause 与原 COMMIT cause；它不改变原 unknown 状态。观察器仍拥有连接期间的 panic 走有界清理且不输出 panic 内容；不得因观察故障返回另一个 mutation UnknownOutcome。

`pgxpool.Conn.Release` 一经调用即转移所有权，pgx 会先清空内部 resource，再调用 ReleaseTracer 等外部 hook。观察器必须在调用 Release 前放弃本地所有权；无论 Release 正常返回或因同步外部 hook panic，都不得再读取该 Conn、Hijack、Close 或在 defer 中假称追回资源。可捕获的同步 Release panic 仅返回安全 unresolved/cause，不再触碰已转移连接；其它 goroutine 中的外部 hook panic 不在观察器可恢复范围。外部 pool Tracer、AfterRelease 及其它用户回调须遵守 pgx 合同、不得 panic；其自身中断可能造成资源泄漏或进程异常，资源恢复不属于 Runner/observer 的清理承诺，不宣称“所有 panic 都能完成资源恢复”。本页不新增 hook 拒绝规则，不把 hook 故障当作已验证的 pool 恢复。

## 4. 固定查询与解码规则

以下是**客户端独立查询的完整白名单**。`S` 表示 handle 冻结的单个 schema，表和函数名仅用 `pgx.Identifier{S,name}.Sanitize()` 生成；一切身份均 bind，不拼业务值，不接 caller SQL。字段顺序按本页列出，固定字段别名保留列名；不 `SELECT *`、不从 live catalog 生成投影、不选择系统列或秘密。validator 内部固定材料的现有查询不由客户端重写。

每条原件 Query 都先核冻结列名/数量/builtin OID，再按准确 nullable 扫描、要求指定基数、排流并检查 Rows.Err；不得以 QueryRow 吞掉第二行。真实 PgError/网络/流失败保留主因，不能被随后的零行或 scan 异常掩盖。列表只读准确 root 的 1/2 条 steps，不扫描任意全表；遇到超出预期即停止接收并排流/关闭。单例的零行与允许为 NULL 的列严格分开，多行是 incomplete/protocol，不能任选一条。

统一 wire 类型：原 TEXT 原样（OID25）；int2/int4/int8（21/23/20）、bool（16）与 timestamptz（1184）用准确 builtin 类型，其中 C4.version 保留原 INT2；xid8 投影为 `::pg_catalog.text AS 原列名`，按无符号规范十进制字符串比较，不能经 int64；NUMERIC 金额投影为 `::pg_catalog.text AS 原列名`，用规范十进制/任意精度比较，不能用 float/int64 截断；root 的 session_role_oid 固定 `::pg_catalog.int8 AS session_role_oid`。SQL NULL 保留 typed nullable，绝不 COALESCE 空串/零/JSON null。JSONB 为 OID3802，不接受 JSON/domain/可转换其它型；时间须 finite、微秒精度，按 instant 比较。

### 4.1 共享 root、step 与 journal

| 查询 | WHERE / 基数 | 准确投影（按序） |
| --- | --- | --- |
| R1 root | `id=$1`；恰一行 | `id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result` |
| R2 steps | `root_id=$1 ORDER BY step_no`；账户=1，unmanaged attempt=1，managed attempt=2 | `root_id,step_no,kind,source_id,created_xid,d_account_command,d_attempt_birth,d_procurement_transition` |
| J1 journal | `id=$1`；有 journal 分支恰一行 | `id,account_id,ordinal,previous_id,previous_ordinal,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,attempt_id,binding_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,pause_event_id,command_source_id,transition_id,delivery_source_id,command_operation_id,procurement_event_id,dispatch_attempt_id` |

R1 仅 tenant/request/attempt/account_target 四个 scope 身份允许 SQL NULL，但必须匹配具体根联合；所有其它列非 NULL。session_role_oid 只读保留其准确值，其与安装绑定 APP OID 的比较由固定 validator 内的常量完成；客户端不从当前角色猜安装身份、不新增 catalog 查询。R2 三个 generated 派生指针只允许该 step_kind 对应的一项非 NULL，其余为 NULL；其它列非 NULL，准确 step ordinal/来源必须匹配。其它 generated 域、跨 root 输出及全部反向唯一性仍由固定 root/leaf validator 检查，客户端这八列不能代替 validator。

J1 的 before 配置/金额/计数在 create 全 NULL、其余本片支持的 journal 分支全非 NULL；after 配置八列全非 NULL。previous_id 只在 create 为 NULL、previous_ordinal 为 ordinal-1（create 为 0）。attempt/binding、reservation before/after、quote/gate/PB 与 command/transition/delivery 派生列严格按 §5/§6 联合，不按“列可空”放宽跨分支。全部58列均为现有普通列，不读取 xmin。

### 4.2 账户查询

| 查询 | WHERE / 基数 | 准确投影（按序） |
| --- | --- | --- |
| C1 command | `operation_id=$1`；恰一行，零行 receipt_absent | `operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,created_at,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id` |
| C2 command source | `id=$1`；恰一行，key 来自 C1.pp_source_id | `id,root_id,step_no,step_kind,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id` |
| C3 audit | `id=$1`；恰一行，key 来自 C2.audit_id | `id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid` |
| C4 account origin | `account_id=$1`；有 journal 时恰一行 | `account_id,version,create_operation_id,command_source_id,root_id,step_no,step_kind,journal_id,created_xid,created_at` |

C1 的 before/after 非 SQL NULL，内部 JSON null 与 object 按 S1 分支；追踪身份均非 NULL，只有 not_found 的 journal 真 SQL NULL。C2 只有 create 的 expected_revision、not_found 的 journal 可 NULL；configure expected 必须与输入正 int64 相等。C3 只有 tenant_scope 必须 NULL，其余本片账户审计列非 NULL；request_id 是空 TEXT 而不是 NULL。C4 全非 NULL。

### 4.3 attempt 查询

| 查询 | WHERE / 基数 | 准确投影（按序） |
| --- | --- | --- |
| A1 original attempt | `id=$1`；恰一行，零行 receipt_absent；key 只用捕获 attempt_id | `id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,delivery_state,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,cost_status,cost_amount,created_at,route_decisions,pb_created_xid,pb_dispatch_checked_at,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at,pp_last_cost_mutation_id,pp_last_delivery_source_id,pp_dispatch_source_id,pp_dispatch_root_id,pp_dispatch_root_step,pp_dispatch_xid,pp_dispatch_account_id,pp_dispatch_journal_id` |
| A2 attempt origin | `attempt_id=$1`；tracked 恰一行；零行不自行归类 legacy | `attempt_id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at` |
| A3 attempt head | `attempt_id=$1`；tracked 恰一行 | `attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid` |
| A4 attempt node | `id=$1`；managed 恰一行，key 来自 A3.last_node_id | `id,attempt_id,ordinal,previous_node_id,root_id,step_no,step_kind,source_id,created_xid,previous_ordinal` |
| A5 procurement reservation | `attempt_id=$1`；managed 恰一行 | `attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,spent_amount,state,anomaly,revision,created_at,updated_at,pb_created_xid,pb_control,pb_admitted_at,pp_created_xid,pp_origin_journal_id,pp_last_journal_id` |
| A6 procurement transition | `id=$1`；managed 恰一行，key 来自 A2.procurement_transition_id | `id,root_id,step_no,step_kind,created_xid,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,cause_kind,cause_source_id,event_id,journal_id,event_type,event_key,evidence_ref,held_delta,spent_delta,before_revision,before_state,before_held,before_spent,before_anomaly,after_revision,after_state,after_held,after_spent,after_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,d_attempt_birth,d_evidence_choice,d_approval_consumed,d_recovery` |
| A7 reserved procurement event | `id=$1`；managed 恰一行，key 来自 A2.reserved_event_id | `id,event_key,account_id,attempt_id,held_delta,spent_delta,type,evidence_ref,created_at,pb_created_xid,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid` |

A1 的五个公开回执字段 id/request/sequence/delivery/cost_status 与四个幂等身份均非 NULL，原 replay TEXT id/cost_status 不新增 canonical UUID/非空/成本枚举检查。跟踪分支的 route、created_at、cost_basis、出生 xid/时刻非 NULL；quote version/rates 依原报价联合可空。当前支持的 tracked attempt 必须仍 prepared/unknown/cost_amount NULL，全部 dispatch/last-cost/last-delivery 指针及 pb_dispatch_checked_at 为 NULL；其它状态不凭原五字段存在绕过 validator。旧 row 的部分 NULL 仅按 validator 严格区分，不以 Go 宽容扫描分类。

A2 的 initial_cost_amount 固定 NULL；quote 按原 origin 联合；management_mode 为 managed 时六个 account/binding/transition/event/journal/admitted 项全非 NULL，unmanaged 时六项全 NULL。A3 unmanaged 为 ordinal=0、last_node/member_count/member_tail 全 NULL；managed 为 ordinal=1、last_node 非 NULL、member_count=0、member_tail NULL。A4 previous_node NULL、previous_ordinal=0，其余非 NULL。A5 只有 pb_control 按出生时是否已有 supplier control 可 NULL，pb_admitted_at 仍为原 admission，不能因后置导入而补写出生 marker；其它本片字段非 NULL。A6 before reservation 五项全 NULL、after 全非 NULL，d_attempt_birth=attempt、其它三个 generated 域 NULL，完整 known quote 非 NULL。A7 全部非 NULL。

## 5. 账户匹配、独立 digest 与不可变历史

### 5.1 原 Go digest 是独立期望值

按 [procurement.lockCommand](../../internal/procurement/store.go) 的原算法，使用 Go 标准库而不是 writer/helper/live JSONB 生成 expected：

```go
encoded, err := json.Marshal(struct {
    Kind string
    Target string
    Payload any
}{kind, target, payload})
sum := sha256.Sum256(encoded)
expectedDigest := hex.EncodeToString(sum[:])
```

outer 字段顺序/名称准确为 `Kind`、`Target`、`Payload`，无 JSON tag。create：kind=`account_create`、target 空串，payload 为按序且准确 tag 的 `operation_id,label,provider,state,limit_amount`；configure：kind=`account_configure`、target 为输入 account，payload 按序为 `operation_id,expected_revision,label,provider,state,limit_amount`。payload 形态与 [CreateInput/ConfigurationInput](../../internal/procurement/types.go) 一致，不导入业务包制造依赖环。

limit/expected 从冻结 int64 用 `strconv.FormatInt(value,10)` 恢复原 canonical 字符串；不 float、map 编码、JSON key 排序、SQL `jsonb::text`、不同 HTML escaping 或字符串拼接。保留 Go `json.Marshal` 对 `<>&`、U+2028/U+2029、引号、反斜线与控制字符的实际编码。actor 不在 digest 内，必须另核。Observe 不能调用 `pp_ac_command_digest_v1` 产生 expected；只读 validator 内部调用其原固定实现不替代此独立比较。

### 5.2 逐值与关系

依次 C1→C2→R1/R2→C3→有 journal 时 J1/C4，最后 V1/V2。先核 C1 的 operation/actor/kind/target/input_digest 准确等于冻结输入和上述 Go expected；C1 的 operation、actor、kind、target、status、reason、before、after、created_at、root/journal 逐值等于捕获 AccountReceipt。outcome 是本次调用分支，不是持久 Command 列，**不从旧原件推断本次 outcome**。

before/after 沿 S1 严格对象字段/类型/数字边界解码，金额用精确字符串；比较原件时不依 JSONB key 顺序，snapshot.created_at 沿原 validator 已允许的 RFC3339 时区文本按 finite/微秒精度的 instant 比较，不额外要求物理 JSON 字节或时区拼写相同。这不修改 S1 mutation 回执的现有 UTC 编码检查。created_at 为不可变 Command 创建时刻；账户 snapshot 的 created_at 来自 C4 原 account origin，不能与 Command.CreatedAt 混淆。不得 SELECT 当前账户代替旧 before/after；账户后续合法 configure/reserve 不使旧快照“跟随当前”。

C1 的 source/root/step/xid 指向 C2/R1/R2 且 step=1；C2 精确核 operation/actor/kind、input_target（create 空串）、label/provider/state/limit/expected/digest、target/status/reason/command_created_at/journal；actor_role/status 为原保存的 admin/active，不查询当前 users 重新授权。C2.source ID、C3.audit ID 为真实原件，不自行生成。C3 精确核 actor、tenant_scope NULL、action=`procurement.command.`+status、target=operation、request_id 空串、sanitized_diff 恰 `{kind,status}`、root/step/source_kind/source_id/source_role/xid，source_role=`account_command`。审计时间只按原材料要求 finite/来源关系，不无依据强等 Command 时间。

R1 精确是账户 entry_kind、scope=`account_command`、account_target=准确 target、tenant/request/attempt NULL、同创建 xid、实际绑定 APP OID、step_count=1、semantic_result=command status；R2 精确是 account_command step、source=C2.id 与同 xid。命令/source/root/step/audit/journal 的正反向唯一性、禁止额外 request/attempt/procurement 输出以及同 root 的异常附着由 V1/V2 固定材料完整复核，不以这些正向 row 存在代替。

| Command 分支 | J1/C4 与 NULL 合同 |
| --- | --- |
| create applied/replay | before JSON null，after 为原 snapshot；J1=create/command/source=operation，ordinal1/previous NULL，after revision1/H0/S0/block0；C4 精确绑定本 create operation/source/root/step/journal/xid |
| configure applied/replay（含 noop） | J1=configure，before/after 与旧 snapshot 配置、金额、revision 对应；财务和 blocking 不变；同值 noop 不强制 revision 增加；C4 原 create 来源保持 |
| configure revision_conflict/replay | Command.before snapshot、after JSON null；J1=configure_rejected，其配置/金额 before=after，不把 journal.after 复制为 Command.After |
| configure not_found/replay | Command.before/after 均 JSON null；捕获回执、C1、C2 的 journal 都真 SQL NULL；不查询 J1/C4、不要求现在账户仍不存在；V1/V2 证明该 root/source 无 journal/其它资金输出 |

### 5.3 必需只读 validator

V1 为固定单条 `SELECT S.pp_validate_original($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.text)`，参数准确 `("procurement_command", operation, nil)`；V2 为 `SELECT S.pp_validate_root($1::pg_catalog.text)`，参数为捕获且已逐值核对的 root。名称安全限定；不提供任意 tag/目标选择器。两次均必须成功完成，void 查询无公开数据结果；Query/Exec 必须等到服务端完成，不能只看已经收到的 DataRow。

当前材料 root validator 对非 NULL journal 会完整验证对应账户当前受支持的 command+reserve 前缀、head/投影、所有 journal 及 §6.3 所列已有 PB 金额/来源关系；这与核不可变旧快照并行成立，**不等于认证完整 PB 配置/观察历史**。后续独立但未支持的账户 dispatch/release/cost/correction 等历史即使原 Command 仍存在也不绕过：本次真实 PP906→unresolved/unsupported_history，真实 PP902→history_invariant，不根据“预计应是未来域”替换服务器实际分类。

## 6. attempt 的新建、重放与预算关系

### 6.1 捕获身份与 tracked 路径

先 A1 按捕获 attempt_id 查，独立核 tenant/request/sequence 与 Invocation 原值，channel/channel_version/credential/upstream_model 四字段逐字节相等；准确五字段 id/request/sequence/delivery/cost_status 必须与捕获 AttemptReceipt 相同。不得同 request 找“最近 attempt”，不得按非唯一 root 猜 ID、按当前 binding/cost version 替换，或用 request 的另一个 sequence 证明本次结果。

applied 时 A1 出生 root 必须等于捕获 root，source=捕获 attempt_id；A2 的 attempt/root/step/xid 必须与 A1 及捕获来源准确相等。A1 的 route/报价/quote_selected_at/created_at/pb_created_xid 等保存原件由 A2 与 validator 同源逐值认证，**不重选当前价格或重新执行 expected quote 比较**。new 输入 route 与原 SQL JSONB 语义保真由根执行/参数协议验收证明；只读 observer 不以重跑资格规则代替回执认证。

replay 时捕获 root/source 保持 NULL；A1 五字段与四身份匹配后，A2 只用于认证该原 attempt 已有的出生 root，不把它称作本次新来源。replay 的新 route JSON 与 expected quote **不是第五幂等键**，不要求与旧 A1/A2 相等，不重跑 quote/路由/Key/账户当前资格。允许的结论仍仅是准确原回执已存在，不是本次 replay COMMIT 获得确认。

tracked 路径依次 A1→A2→R1/R2→A3→managed 的 A4–A7/J1，随后固定 V3=`SELECT S.pp_validate_attempt($1::pg_catalog.text)` 与 V2=`SELECT S.pp_validate_root($1::pg_catalog.text)`；参数分别准确 attempt_id 与 origin root。applied root 必须已与 handle 等值；replay origin root 只在内部认证使用。每一必需查询/validator 均在同一 snapshot，任一失败不得发布 presence。

R1 为 entry_kind=`attempt_birth`、scope=`request`、准确 tenant/request/attempt、account_target NULL、origin 创建 xid、实际 APP OID；unmanaged step_count1/semantic_result unmanaged，managed step_count2/semantic_result managed。R2 step1 为 attempt_birth/source=attempt；managed step2 为 procurement_transition/source=准确 transition，同 root/xid；source/generated 域及数量准确。

### 6.2 managed p、PE、journal 与账户

managed 六个 origin 关联值全部存在；A5 准确 account/binding、request 出生上下界与冻结 known rates 计算出的 `U=ceil((input_bound*input_rate+output_limit*output_rate)/1000000)`，以整数/任意精度计算并要求 U 在非负 int64 域。p 为 revision1/prepared/H=U/S0/anomalyfalse，origin/last journal 都是准确出生 journal，原 admitted_at 与创建 xid 精确一致。不重算当下经营许可，不以当前 limit/state 判定过去是否发生。

A6 准确绑定 origin/tenant/request/attempt/account/binding、step2、cause=attempt_birth/attempt、reserved event/journal、event_key=`reserve:`+attempt、evidence_ref 空串、Hdelta=U/Sdelta0；before reservation 全 NULL、after 与 p 准确相同、known quote 与 origin 相同。A7 为准确 reserved PE，身份/金额/event_key/root/step/transition/journal/xid 双向等值。A4 为 head 指向的第一 node，previous NULL、ordinal1、step2、source=transition，同 root/xid。J1 为 reserve/budget_event/source=准确 PE，绑定相同 attempt/binding/transition，command/delivery 派生全 NULL；journal before 配置 active/block0，H+S+U<=当时 limit，after H=before H+U，其余配置、S/block不变；journal predecessor及账户完整前缀由 validator 复核。

上述不是只核几条正向 FK：V3/V2 必须证明 transition/PE/journal/node/step/attempt-origin 的全部反向唯一性，禁止同 root 输出其它 request/attempt/账户命令/审计，以及 p 的 origin/last-journal 不被其它预占复用。validator 同时核永久 credential binding、request origin、账户 origin/current head/journal 和 H/S 累计，后续合法其它 reserve/configure 可推进账户 head；observer **不要求当前账户 head 仍等于该 attempt 出生 journal**。

### 6.3 supplier PB 的三种已有支持形态

observer 不独立调用 `pb_now/pb_observe/pb_gate/pb_scope_sync/pb_sync_unconfigured`、Commander 或任何补窗口/导入函数，也不增加 PB 客户端查询。下面是 V3/V2 **现有只读固定材料必须成功证明**的 transitive 关系，不以 fixture 期望或当前配置重造历史：

| 原形态 | 必须证明的已存事实 |
| --- | --- |
| snapshot 中无 supplier control | p.pb_control 保留 NULL；没有该 attempt 的 supplier allocation、mirror PB event 或伪造 marker；原 tracked 预占仍由账户前缀认证 |
| 出生时已有 supplier control | p.pb_control 准确 control，allocation origin=`new`、origin_source=reserved PE、创建 xid/原 admitted_at 与出生一致；准确真实 UTC window；唯一 PB mirror event 的 source 为 reserved PE、H=U/S0，与 allocation/window/control 的已支持金额/来源关系一致 |
| 真实 Commander 后来首次配置并导入原预占 | 不补写 p 的原 NULL birth marker；allocation origin=`legacy_import`、origin_source=attempt、window/admitted_at 真 NULL、创建 xid 等于 control 初配 xid；唯一 import PB event及source金额与已支持 account/PB 当前金额/来源关系一致 |

当前 account leaf 核 PB control 的 event_seq/last_event、event 与 allocation 双向归属、H/S合计、effective_from、各 window 的起止/累计/revision/last_event/window_before_revision；attempt leaf 核本 attempt 的 allocation 和唯一镜像来源。**现有材料没有读取 `periodic_budget_operations`、`periodic_budget_configuration_versions` 或 `periodic_budget_observation_origins/heads/journal`，所以本页不声称认证这些配置/命令/B-P1观察整图；已有 SELECT 权限不能替代算法。**以后需要此保证时另行 SDD 冻结、实现与验收，不能暗改本次固定材料。

不能因为 control 当前 disabled 就跳过上述已支持关系，不能为满足 observer 人工前移时钟、改原 admission、模拟 legacy_import 或补 origin。已覆盖来源/金额/桶关系的破损保留真实 PP902，独立未支持的合法后继域保留真实 PP906；未由 validator 覆盖的配置/观察历史不宣称已核或一定会抛 PP902。当前限定支持不等于 active cap、跨 UTC 周期、全 PB 命令或生产验收已完成。

### 6.4 unmanaged 禁止集合

origin 六个 managed 关联字段全 NULL，head 为零节点形态，R1/R2 只有出生 step。V3/V2 必须证明该 attempt/root **不存在** p、PE、procurement transition、account journal、attempt node、supplier PB allocation；并证明所有跨 root 反向来源/其它输出禁止集合。客户端不把未查询 managed 表当作“它们不存在”，必须实际 validator 成功。

### 6.5 legacy、缺行及未支持后继历史

A1 零行直接 `receipt_absent`，不调用 validator 制造 PP902，不查另一个 ID。A1 存在但身份/五字段不匹配为 `receipt_mismatch`，不以 root 正常覆盖。A2 零行、部分来源或可疑跟踪字段时不能仅凭“无 origin”声称 genuine legacy；可调用固定 V3 获取它对原 attempt 的准确分类，但**不在异常之后继续查询**。V3 先验证 attempt leaf，再验证 request，现有严格 legacy 判定包括全部12个 origin/dispatch/last 指针 NULL、无 tracked request/账户及所有 reverse source/journal/transition/head/node/step/PE/p 关系。只有真实 PP906 是未支持域，真实 PP902 是损坏，不能捕获任意异常当 legacy。

真正 legacy replay 即使原五字段全部匹配，也仅记录本次内部曾读到该原 row，最终仍为 `unresolved/unsupported_history`；公开 Observation 不附旧 DTO 或 presence 附加位。缺少 origin 又未能取得 validator 分类时为 `receipt_incomplete`，不伪造 PP902/PP906。tracked 但已进入目前不支持的 dispatch/finalization/release/cost/recovery 等后继域，同样不以原 root 存在升级成功。**当前 attempt leaf 先核 prepared/unknown 和所有 dispatch/last 指针为 NULL，已变更 attempt 可能先实际抛 PP902；不得保证所有后继域都会得到 PP906，也不得将该 PP902 改写为 PP906。**只有固定 validator 自己完成相应未来域结构分支并实际抛 PP906 才记 unsupported_history。服务器异常会令事务 abort，禁止在原 tx 中继续其它读、SAVEPOINT 捕获降级、转新 snapshot 补齐或跨快照拼证；直接有界清理。

## 7. 既有实际 APP 权限闭包

本页不新增/扩张任何 grant，不更改 validator SECURITY INVOKER、owner/search_path、材料固定摘要或安装版本。采用 [permissions-v1.sql](../../internal/procurementattemptbirth/materials/permissions-v1.sql) 已授予实际 APP 的准确普通 SELECT：

| 用途 | 已有表/列覆盖 |
| --- | --- |
| 客户端 C1–C4/R1/R2/J1 | procurement_commands、pp_account_command_sources、pp_operation_roots、pp_root_steps、audit_events、pp_account_origins、pp_account_journal；§4 全部投影在既有列 ACL 内 |
| 客户端 A1–A7 | upstream_attempts、pp_attempt_origins、pp_attempt_heads、pp_attempt_nodes、procurement_reservations、pp_procurement_transitions、procurement_budget_events；§4 全部普通列在既有列 ACL 内 |
| validator 实际读取依赖 | request_records/pp_request_origins/pp_request_heads、procurement_accounts/pp_account_heads、永久 binding、PB controls/windows/allocations/events 及既有纯规则依赖；不让客户端旁路读取授权/秘密 |
| 已有权限但本次不认证 | PB configuration_versions/operations、B-P1 observation origins/heads/journal；本次独立查询和 validator 都不读取，不能将已授权列当作已认证整图 |

现有**九个只读 validator EXECUTE** 为五个公开 `pp_validate_request/attempt/account/root/original` 与四个 leaf `pp_ab_validate_request_leaf_v1/attempt_leaf_v1/command_leaf_v1/account_leaf_v1`；Observe 客户端只直接调用本页 V1–V3 所列的三个公开名称，其余属于已固定 transitive/deferred 依赖。现有五个只读 pure 依赖 `pp_ac_uuid_v1/text_v1/command_digest_v1/time_v1/snapshot_v1`、`pp_json_string_v1`、A0 route/UUID pure 依赖及既有 PB pure 依赖保持原权限；不是为 observer 新授予，也不提供公开可选 helper 调用器。

validator 要求真实 `session_user` 等于安装绑定 APP OID，当前角色为 APP 或固定 W；observer 使用 actual APP LOGIN，不用 owner+SET ROLE、W LOGIN、宽权限 pool 或探测成功代替身份。validator 不要求 mutation RC/timeout，现有 RR READ ONLY 合法；它们只读原件与 catalog、不做 DML/行锁，不因本片改为 definer。客户端不读取 xmin/ctid 等系统列，尤其不为 upstream_attempts/p/PE 申请当前只有 W 的 xmin 权限；validator 既有 PB 依赖按原列 ACL 执行。任何真实权限不足都保留 unavailable cause，不自修 ACL/切角色/重试旧路径。

## 8. 验收门与交付范围

纯测试核客户端固定语句/投影/OID/NULL/比较/错误/清理；它们不能替代受限 APP 的真实 RR RO、权限、validator、wire COMMIT 未知正反控或商业验收。以下保留完整验收要求；本片实际完成子集见[实际APP合同](b-procurement-observer-acceptance-contract.md)和[验证记录](../../docs/verification/procurement-execution-observer.md)，不能因本表存在而将各行整体标PASS。

| 层 | 必需证据 |
| --- | --- |
| 过程内 seal | nil/零值/foreign Runner/复制 Runner/新 Runner 同 pool 均拒；schema 点/双引号/模板文本；无 Acquire；每次 retry 身份不串；所有 fmt/JSON 安全 |
| exact query/decoder | §4 每列名/OID/NULL、零/多行/晚到流错、全 int64/NUMERIC100/xid8、JSON null/SQL NULL、时区 instant 与原字段逐值；任一错均无 present |
| digest 独立 oracle | 原 procurement Go struct/json.Marshal 对照，不由新 helper生成 expected；create/configure/nonce/target/actor分离及 `<>&`、U+2028/2029、引号/反斜线/控制字符、MaxInt64；actor单独匹配 |
| 只读协议 | 新 Acquire→RR RO→SET→固定读/validator→Rollback；总工作<=5s与共用 cleanup<=5s；无 COMMIT/root/业务回调/锁/写/自动重试；观察器拥有连接期间 panic/cleanup 失败按规则销毁，不 Release 污染连接；Release 调用转移所有权后不再读/Hijack/Close该 Conn。外部 Tracer/AfterRelease 须不 panic，其资源恢复不在 Runner/observer 清理承诺内 |
| account actual APP | 新建/重放、configure更改/noop/not_found/revision_conflict；已提交 unknown 正控 presence、未提交反控 absent；旧 digest/actor/回执错配、source/root/audit/journal缺损/额外反向关系与后续合法账户命令/reserve |
| attempt actual APP | managed/unmanaged与tracked replay；准确四身份/五字段、另一sequence/旧ID不能替代；replay不同route/quote不增加幂等键；true legacy PP906、伪legacy PP902、未支持后继域 PP906、缺原件与缺来源分开 |
| PB actual APP | 出生无control、已有control新allocation、真实Commander后配legacy_import；金额/window/admission/xid/source/mirror反向关系；全普通业务表前后逐行无变更，不以fixture补历史 |
| wire与snapshot | 三根提交前后确认丢失正反控，unknown零mutation DTO；一个snapshot读证且并发提交不拼快照；读不到不触发重放、PrepareDispatch、provider或后台生成 |

并行结果汇总后由集成人更新统一架构中的候选 observer 节点、Run→unknown handle→显式只读 Observe 链路、无经营授权边界、actual APP 权限和能力状态；HLD/LLD/验证记录准确区分设计、实现、纯验证与实际 PG/wire 验收，再执行仓库要求的 `check-architecture --refresh --reviewed --reason ...` 与只读检查。Observe实现及限定验收不代表完整A4/A5、PB异常域或R2已完成。

## 9. 本片实施状态与保留矩阵

[Observe生命周期](../../internal/procurementexecution/observe.go)、[固定codec](../../internal/procurementexecution/observe_rows.go)、[共享root/step/journal](../../internal/procurementexecution/observe_shared.go)、[账户](../../internal/procurementexecution/observe_account.go)和[attempt](../../internal/procurementexecution/observe_attempt.go)已实现。最终完整纯race为65顶层/1427子项PASS、1.743秒；真实APP首轮为6顶层/21子项、77.623秒，codec主因分类修复后同名单最终复验为6/21、29.357秒，均0FAIL/SKIP。实际PG覆盖三根已提交unknown、未发送COMMIT负控、账户全部现有分支/replay及后续合法账户前缀、managed/unmanaged与已有PB三形态、四种准确失权/恢复、fresh RR RO/一次Acquire/34表无写及无效issuer。

true legacy在当前固定根的BEFORE INSERT来源认证处先PP906，未进入decoder/COMMIT，不能发行有效handle；本轮实证此capture前置门，不称完整Observe legacy PG通过。坏关系/额外反向边的实际PG、并发snapshot交错、完整grant/后继域和更广PB图仍按原要求未验；不制造origin、破坏已提交图或用sealedhandle纯测试替代。本页保留范围是事实边界，不自动将所有历史/兼容proof列为MVP能力开发的前置条件；后续优先级由[交付计划](../../docs/design/delivery-plan.md)及用户最新指令决定。

# 024-A0：站内经营告警精确实现合同

2026-11-01，**A0已由根集成人复核冻结；生产源码已实现，真实PG/HTTP/浏览器与主进程故障专项通过，有效已完成限定本地验收，权限/全量race/019预览与最终架构检查通过**。仅细化024-A；不授权B–F或动态插件。范围、公开DTO和HTTP语义以[合同](contract.md)为基础，本文件补齐其编码前出口。关联[规格](spec.md)、[实施计划](plan.md)、[验收](acceptance.md)、[模块设计](../../docs/design/extension-platform.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)和[统一架构](../../docs/architecture/README.md)。已分配追加迁移019（018属于023）；A1按本文进入领域实现，A2/A3/A4按接口依赖并行。

## 1. 审查补项与范围边界

现有四种告警、64/256规则上限、默认关闭、站内读取/共同确认、无外发/自动资金动作均保持。以下六项补充已经集成人于2026-11-01接受设计方向，**完整DDL/签名已经根复核并接受，不应默认为已有实现**：

1. 仅核对episode最后事件不足以禁止孤立、额外或错误actor的历史。本稿要求opened/可选ack/可选resolve完整链与episode双向一致，并增加不公开的command关联。
2. 配置、用户结束/确认与命令之间需要事务证明。增加命令`transaction_id`、规则`last_command_id`及episode/event内部命令列；每个用户告警事务只能claim一个新命令，命令必须在该事务终态，禁止复用旧回执为新效果背书。
3. 原稿失败记录仅检查rule revision，会使晚回失败覆盖同一配置下较新的成功。增加内部runtime `write_revision`；失败短事务须同时CAS规则配置版本和runtime写版本。公开revision/DTO不增加字段。
4. 当前session role只作HTTP预门禁；所有repository读取在每请求新建只读RR事务内重查active当前role。事务快照准入后已返回/在途读取不宣称可被追溯撤回。
5. 原稿独立key前缀仍可能竞争通用limiter容量。本稿建议独立alert准入实例，保留读60/min burst20、写20/min burst5，增加每actor读4/写2并发、全局读32/写8并发和无队列上限。桶容量4096、闲置10分钟；不抢通用或commerce桶。
6. 观察源缺失与合法0行明确区分；两个aging SQL保留父资源存在位。aging是预占创建年龄，不是unknown状态持续时间。源索引/真实EXPLAIN须在A1执行，本设计不伪造计划或性能证据。

如拒绝任何补项，须在冻结前给出等价不变量和反例验证；不可在A1自行删掉保护或扩大产品范围。集成人已为本片分配019；只追加，不修改001–018。

## 2. 源码审计结论与依赖

| 真实入口 | 本片使用方式和限制 |
| --- | --- |
| [catalog渠道写](../../internal/catalog/store.go)、[内存路由健康](../../internal/routing/routing.go)、[gateway凭据隔离](../../internal/server/gateway.go) | 只读`channels.enabled/quarantined`；其余健康状态不混入该kind |
| [019采购迁移](../../migrations/014_procurement_accounts.sql)、[采购运行侧](../../internal/billing/procurement.go) | `limit_amount BIGINT`，held/spent为NUMERIC(100,0)；读登记投影，不读供应商网络；采购unknown/disputed独立于客户释放 |
| [客户资金迁移](../../migrations/002_billing.sql)、[恢复](../../internal/billing/recovery.go) | 只数pending_reconciliation；不扫描全部reserved，不触发Recover/Release/Settle |
| [022只读授权](../../internal/billing/voucher_query.go)、[HTTP门禁](../../internal/server/server.go) | 采用每请求当前数据库角色复查；已有会话/MFA/同源/CSRF/再认证保留 |
| [commerce有界准入](../../internal/server/commerce_admission.go) | 可参考有界桶和once release实现，独立对象、配额和超时，不直接复用其计数器 |
| [邮件worker](../../internal/mailworker/worker.go)、[main](../../cmd/moni-aigate/main.go)、[单活](../../internal/operations/instance.go) | 参考固定槽和生命周期；不复用sender/outbox/财务串行ticker；Guard与正常退出分开 |
| [角色provision](../../internal/operations/operations.go)、[迁移嵌入](../../migrations/embed.go) | 追加迁移，补显式revoke；不改已应用SQL，不以自动迁移锁替代运行实例排空 |

统一锁图仍为业务根/tenant/身份/Key/model/channel/credential→客户reservation→attempt→采购account→采购reservation。告警用户写为**user SHARE→command→可选control→rule→runtime→episode→event/audit/回执**；worker从rule开始。来源只有MVCC普通SELECT。无业务FK、业务根锁或持告警锁的网络IO，因而不增加回到商业锁图前层的边。

## 3. Go边界与所有DTO

实现包`internal/alerts`和`internal/alertworker`；下列签名保留A0冻结合同。领域层不依赖`provider`、SMTP、HTTP client或billing写接口。下述类型是已冻结签名；实现时字段/tag不得自行重命名。

```go
// internal/alerts/types.go; wire字符串在构造边界严格验证，不能靠别名绕过。
type ID = string
type Decimal = string // 规范十进制，按字段适用正int64/非负/有符号大整数界
type Kind string
const (
    ChannelQuarantined Kind = "channel_quarantined"
    ProcurementBudgetLow Kind = "procurement_budget_low"
    CustomerReconciliationAged Kind = "customer_reconciliation_aged"
    ProcurementUnresolvedAged Kind = "procurement_unresolved_aged"
)
type ScopeType string // channel | procurement_account | tenant
type RuleState string // enabled | disabled | retired
type Freshness string // disabled | retired | paused | never_evaluated | fresh | stale
type CommandKind string // rule_create | rule_configure | rule_retire | acknowledge
type EventType string // opened | acknowledged | recovered | rule_changed | rule_disabled | rule_retired
type Time struct{ time.Time } // MarshalJSON固定UTC 2006-01-02T15:04:05.000000Z

// 封闭联合：只允许本包构造；MarshalJSON输出各kind精确字段。
// 对外不输出omitempty。数据库JSON独立验证后再decode为这些类型。
type Parameters interface { alertParameters(); json.Marshaler }
type EmptyParameters struct{} // JSON {}
type BudgetParameters struct {
    ThresholdAmount Decimal `json:"threshold_amount"`
    RecoveryAmount Decimal `json:"recovery_amount"`
}
type AgingParameters struct {
    MinimumAgeSeconds Decimal `json:"minimum_age_seconds"`
    TriggerCount Decimal `json:"trigger_count"`
    RecoveryCount Decimal `json:"recovery_count"`
}
type Sample interface { alertSample(); json.Marshaler }
type ChannelSample struct {
    Kind Kind `json:"kind"`; AsOf Time `json:"as_of"`
    Enabled bool `json:"enabled"`; Quarantined bool `json:"quarantined"`
}
type BudgetSample struct {
    Kind Kind `json:"kind"`; AsOf Time `json:"as_of"`
    AccountState string `json:"account_state"`
    LimitAmount Decimal `json:"limit_amount"`; HeldAmount Decimal `json:"held_amount"`
    SpentAmount Decimal `json:"spent_amount"`; AvailableAmount Decimal `json:"available_amount"`
    Currency string `json:"currency"` // CNY
}
type AgingSample struct {
    Kind Kind `json:"kind"`; AsOf Time `json:"as_of"`
    MinimumAgeSeconds Decimal `json:"minimum_age_seconds"`
    ObservedCount Decimal `json:"observed_count"`
    CountIsLowerBound bool `json:"count_is_lower_bound"`
    OldestCreatedAt *Time `json:"oldest_created_at"`
    OldestReferenceID *ID `json:"oldest_reference_id"`
}
type RuleSnapshot struct {
    Kind Kind `json:"kind"`; ScopeType ScopeType `json:"scope_type"`
    ScopeID ID `json:"scope_id"`; Label string `json:"label"`
    Parameters Parameters `json:"parameters"`
}
type Capabilities struct {
    DeploymentEnabled bool `json:"deployment_enabled"`
    RecoveryMode bool `json:"recovery_mode"`
    EvaluationAvailable bool `json:"evaluation_available"`
    Reason string `json:"reason"`
    MaxEnabledRules Decimal `json:"max_enabled_rules"`
    EvaluationIntervalSeconds Decimal `json:"evaluation_interval_seconds"`
    StaleAfterSeconds Decimal `json:"stale_after_seconds"`
    ObservedAt Time `json:"observed_at"`
}
type RuntimeView struct {
    NextEvaluationAt *Time `json:"next_evaluation_at"`
    LastAttemptAt *Time `json:"last_attempt_at"`; LastSuccessAt *Time `json:"last_success_at"`
    EvaluatedRevision *Decimal `json:"evaluated_revision"`
    LastSample Sample `json:"last_sample"` // nil编码null
    LastErrorCode *string `json:"last_error_code"`
    CurrentEpisodeID *ID `json:"current_episode_id"`
    Freshness Freshness `json:"freshness"`; ObservedAt Time `json:"observed_at"`
}
type RuleView struct {
    ID ID `json:"id"`; Kind Kind `json:"kind"`; ScopeType ScopeType `json:"scope_type"`
    ScopeID ID `json:"scope_id"`; Label string `json:"label"`; State RuleState `json:"state"`
    Parameters Parameters `json:"parameters"`
    ConfigurationRevision Decimal `json:"configuration_revision"`
    CreatedBy ID `json:"created_by"`; UpdatedBy ID `json:"updated_by"`
    CreatedAt Time `json:"created_at"`; UpdatedAt Time `json:"updated_at"`
    RetiredAt *Time `json:"retired_at"`; Runtime RuntimeView `json:"runtime"`
}
type EpisodeView struct {
    ID ID `json:"id"`; RuleID ID `json:"rule_id"`; Generation Decimal `json:"generation"`
    RuleRevision Decimal `json:"rule_revision"`; RuleSnapshot RuleSnapshot `json:"rule_snapshot"`
    Status string `json:"status"`; ActionRevision Decimal `json:"action_revision"`
    OpenedAt Time `json:"opened_at"`; InitialSample Sample `json:"initial_sample"`
    LastSeenAt Time `json:"last_seen_at"`; LatestSample Sample `json:"latest_sample"`
    AcknowledgedBy *ID `json:"acknowledged_by"`; AcknowledgedAt *Time `json:"acknowledged_at"`
    ResolvedAt *Time `json:"resolved_at"`; ResolveReason *string `json:"resolve_reason"`
    EvaluationFreshness Freshness `json:"evaluation_freshness"`; ObservedAt Time `json:"observed_at"`
}
type EventView struct {
    ID ID `json:"id"`; EpisodeID ID `json:"episode_id"`
    ActionRevision Decimal `json:"action_revision"`; Type EventType `json:"type"`
    Actor string `json:"actor"`; ObservedAt Time `json:"observed_at"`
    SafeSample Sample `json:"safe_sample"`; CreatedAt Time `json:"created_at"`
}
type CommandReceipt struct {
    OperationID ID `json:"operation_id"`; Kind CommandKind `json:"kind"`
    Status string `json:"status"`; Reason string `json:"reason"`
    ResourceID *ID `json:"resource_id"`; ResultRevision *Decimal `json:"result_revision"`
    CreatedAt Time `json:"created_at"`
}
type Page[T any] struct { Data []T; NextCursor *string } // HTTP另包request_id，Data永不nil
type Cursor struct { V int `json:"v"`; T Time `json:"t"`; ID ID `json:"id"`; FilterDigest string `json:"filter_digest"` }
type RuleFilter struct { State *RuleState; Kind *Kind; ScopeID *ID; Cursor *Cursor }
type EpisodeFilter struct { Status *string; RuleID *ID; Kind *Kind; Cursor *Cursor }
type EventFilter struct { Cursor *Cursor }
type Reader struct { ActorID ID } // 无客户端role或tenant授权字段
type CreateRule struct {
    OperationID ID; Kind Kind; ScopeType ScopeType; ScopeID ID
    Label string; State RuleState; Parameters Parameters
}
type ConfigureRule struct {
    OperationID ID; ExpectedRevision Decimal; Label string; State RuleState; Parameters Parameters
}
type RetireRule struct { OperationID ID; ExpectedRevision Decimal }
type Acknowledge struct { OperationID ID; ExpectedActionRevision Decimal }
type RuntimeOptions struct { DeploymentEnabled bool; RecoveryMode bool } // 启动冻结，无热改

type Queries interface {
    Capabilities(context.Context, Reader) (Capabilities, error)
    Rules(context.Context, Reader, RuleFilter) (Page[RuleView], error)
    Rule(context.Context, Reader, ID) (RuleView, error)
    Episodes(context.Context, Reader, EpisodeFilter) (Page[EpisodeView], error)
    Episode(context.Context, Reader, ID) (EpisodeView, error)
    Events(context.Context, Reader, ID, EventFilter) (Page[EventView], error)
    Operation(context.Context, Reader, ID) (CommandReceipt, error)
}
type Commands interface {
    Create(context.Context, Reader, CreateRule) (CommandReceipt, error)
    Configure(context.Context, Reader, ID, ConfigureRule) (CommandReceipt, error)
    Retire(context.Context, Reader, ID, RetireRule) (CommandReceipt, error)
    Acknowledge(context.Context, Reader, ID, Acknowledge) (CommandReceipt, error)
}
type EvaluationResult struct {
    Outcome string // idle | evaluated | failed_recorded | stale_discarded
    ErrorCode string // 安全枚举；无源行/SQL/原始错误正文
}
type EvaluationRepository interface {
    // 自己取得一条due rule并持锁完成源查询/决策；不返回待执行claim。
    EvaluateDue(context.Context) (EvaluationResult, error)
}
// internal/alerts/store.go：读写共享服务pool，worker repository使用另一个专用pool。
func NewStore(pool *pgxpool.Pool, options RuntimeOptions) (*Store, error)
// internal/alertworker/worker.go：Run阻塞到ctx退出，所有槽结束后返回。
type Options struct {
    Guard func(context.Context) error
    OnError func(string) // 固定安全码；禁止包含SQL/样本/自由文本
}
func New(repository alerts.EvaluationRepository, options Options) (*Worker, error)
func (*Worker) Run(context.Context) error
```

`Time.UnmarshalJSON`和HTTP严格解码是不同边界：请求没有时间字段；cursor中的时间必须六位微秒、UTC且round-trip一致。内部类型不能用JSON number表示任何金额/revision/计数。`Sample`和`Parameters`四种具体marshal必须白名单输出，不暴露内部命令列、runtime写版本、数据库row或scan结构。可选择域对象与wire DTO分文件，但上述公开JSON字段不可改变。

### 3.1 错误和规范摘要

`type Error struct { Code string; CommitUnknown bool }`仅由领域包构造；`Error()`只返回安全code。对外错误码固定为`invalid_request(400)`、`unauthorized(401)`、`forbidden(403)`、`alert_operation_conflict(409)`、`alert_rate_limited(429)`、`alert_unavailable(503)`、读资源`alert_not_found(404)`。内部原因不附SQL、上游、label或body。成功业务回执状态只为`applied|rejected`，持久拒绝矩阵沿[合同§6.1](contract.md#61-命令回执和结果不明)。

摘要SHA-256使用独立Go struct按字段顺序编码：外层`v,actor,kind,target_id,body`；create body为`kind,scope_type,scope_id,label,state,parameters`；configure为`expected_revision,label,state,parameters`；retire为`expected_revision`；ack为`expected_action_revision`。参数顺序为budget threshold/recovery、aging age/trigger/recovery；channel为`{}`。`target_id`创建时明确null；不含operation_id/request_id/时刻。用`json.Encoder.SetEscapeHTML(false)`后去掉唯一结尾换行并固定UTF-8；所有实现、摘要golden测试和UI独立于JSONB键排序。

## 4. 事务、授权、准入与查询投影

### 4.1 所有读取

1. HTTP校验session/MFA/Guard，严格query/path、无body、限流。repository不信任session里的role。
2. 2秒总预算覆盖池等待、BEGIN、授权、数据、COMMIT；`REPEATABLE READ READ ONLY`，statement_timeout不大于剩余预算。第一条查询：`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role IN ('admin','finance_operator','finance_approver'))`。没有user行/失权返回403，不把会话role当证明。不取行锁。
3. 同事务物化一次DB观察时刻用于整页freshness。每页独立快照和当前读权；不是跨页快照。角色在本请求快照后撤销可能与已准入读取重叠，下请求/下一页必查。
4. operation先按原actor查命令，其他actor/不存在均404；找到后如原kind需admin而当前已降为finance，返回403。不暴露他人kind/存在性。
5. 固定50+1，按`time DESC,id COLLATE "C" DESC` keyset，where用同序tuple比较；只投影明确列。先收集/验证至51条并关闭Rows；只输出50条；有第51条时cursor来自第50条。错误不返回半页。
6. rules过滤摘要对象`{v:1,route:"rules",state:null|string,kind:null|string,scope_id:null|string}`；episodes为`{v:1,route:"episodes",status:null|string,rule_id:null|string,kind:null|string}`；events为`{v:1,route:"events",episode_id}`。无游标/分页字段入摘要。RawURL base64规范重编码相等、≤512字符，拒绝重复/未知字段、额外query、JSON null必填等；cursor不提供授权。
7. JSON先编码进≤256KiB有界缓冲，再写header/body；超限返回503，不截JSON。`Cache-Control: no-store`；成功单体/列表/envelope及显式null沿原合同。所有4类sample≤2KiB、snapshot≤1KiB，label含中文按字节上限，51条SQL上限不是响应放大许可。

freshness按同一观察时刻计算：retired→retired；disabled→disabled；恢复或部署关闭→paused；当前configuration_revision没有成功样本→never_evaluated；DB时间回退或距last_success>180秒→stale；否则fresh。错误独立显示，不能删除最后成功样本掩盖stale。

### 4.2 用户写协议

3秒总预算、lock_timeout≤500ms；READ COMMITTED。先严格解码≤8KiB、权限预门禁、同源/CSRF/近期再认证/恢复检查、独立准入，再开始事务：

1. `users WHERE id=$actor FOR SHARE`后用新语句重查active和所需platform_role；持到COMMIT。创建/配置/退役须admin，ack允许三平台角色。用户变更若先提交必拒绝；若本次已持锁，则撤权等待这次已准入命令。
2. INSERT claimed命令，唯一operation冲突时等待原事务在预算内完成，再新语句读取。原actor/规范摘要/kind/target全等才回原终态；异体或他人409。重取旧命令不修改transaction_id。相同事务仅一个新命令，命令的`transaction_id=txid_current()`。
3. 新命令创建savepoint。规则写先锁control，再锁已有rule、runtime、current episode；ACK先普通SELECT episode的不可变rule_id用于定位，再按rule→runtime→episode锁并重新核验目标。不可先锁episode再返rule。不存在目标持久rejected，不泄露源内容。
4. 新规则创建时非锁定SELECT核验源存在；paused/disabled资源仍存在。既有源后来缺失不妨碍关闭/退役原规则，评估仍按source_missing失败。实际配置变化按rule→旧episode/配置事件→runtime重置顺序执行，生成安全审计；SQL见下节。完全无变化且CAS正确，applied原revision，不关闭/重置/审计；旧CAS即使内容相同也rejected。
5. 可判定拒绝回滚savepoint中的业务效果，再把claimed终态为rejected；成功为applied。审计event/receipt任一意外失败全部回滚，不把基础设施异常伪装成业务拒绝。
6. COMMIT不明返回503/CommitUnknown，只允许查询同operation；404不证明永不提交。禁止新ID自动重放。只有驱动明确证明事务已回滚的死锁/序列化失败可在剩余预算内重试同closure；不得把COMMIT网络错误当可重试。

control锁使256非retired/64enabled计数及同scope退役新建串行；还要部分唯一索引兜底。上限计数只扫描state<>retired的partial index范围（最多256行），不得扫描无限退役历史；last_command_id有独立索引用于rejected-effect检查。达到上限预期rejected，只有被正确识别的唯一scope冲突映射`alert_rule_exists`；未知约束错统一503。不得抓所有23505当scope重复。

### 4.3 有界准入

建议独立`alertAdmission`，不持其mutex访问DB。key为服务端当前actor UUID，不接受客户端tenant/IP替代。每actor两个令牌桶：read 1token/sec burst20，write 1token/3sec burst5；上限4096个actor，闲置10分钟且active=0方可淘汰。读写各自活跃计数，不共用速率令牌；全局读32/写8、actor读4/写2，无排队。rate/concurrency拒绝429+Retry-After:1；新actor无法分桶503；release用sync.Once，取消/编码失败也释放。速率已消费不回滚，并发只在请求完成后释放。期限从准入开始，禁止池等待之后另给完整2/3秒。部署能力开关false不免除准入。

## 5. 追加DDL全文候选

下述SQL按顺序组成**追加迁移019_operations_alerts.sql**。已按§8.1执行一次隔离DDL应用；A1仍须完整行为、受限角色和并发验证。命名统一`alert_*`，所有约束名称固定供安全映射。函数默认invoker，不用SECURITY DEFINER或动态SQL。`search_path`由既有迁移/连接机制固定到受控schema；应用无schema CREATE。时间用TIMESTAMPTZ(6)，JSON时间需规范UTC。业务来源没有新增FK/trigger。

### 5.1 白名单值验证

```sql
CREATE FUNCTION alert_id(v TEXT) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND v <> '00000000-0000-0000-0000-000000000000',false)
$$;
CREATE FUNCTION alert_keys(v JSONB, names TEXT[]) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF jsonb_typeof(v) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 RETURN (v ?& names) IS TRUE AND NOT EXISTS
   (SELECT 1 FROM jsonb_object_keys(v) k WHERE NOT (k=ANY(names)));
END $$;
CREATE FUNCTION alert_uint(v TEXT, max_value NUMERIC) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF v IS NULL OR length(v)>101 OR v !~ '^(0|[1-9][0-9]*)$' THEN RETURN false; END IF;
 RETURN v::numeric<=max_value;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
CREATE FUNCTION alert_signed(v TEXT) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(length(v)<=102 AND v ~ '^(0|[1-9][0-9]{0,100}|-[1-9][0-9]{0,100})$',false)
$$;
CREATE FUNCTION alert_label(v TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE n INTEGER; i INTEGER; cp INTEGER;
BEGIN
 IF v IS NULL THEN RETURN false; END IF;
 n:=char_length(v);
 IF n<1 OR n>80 OR octet_length(v)>160 THEN RETURN false; END IF;
 FOR i IN 1..n LOOP
  cp:=ascii(substr(v,i,1)); -- 受支持数据库编码必须UTF8，ascii返回Unicode code point
  IF cp BETWEEN 0 AND 31 OR cp BETWEEN 127 AND 159 THEN RETURN false; END IF;
  IF (i=1 OR i=n) AND (cp IN (32,133,160,5760,8232,8233,8239,8287,12288)
    OR cp BETWEEN 8192 AND 8202) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
CREATE FUNCTION alert_time(v TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF v IS NULL OR v !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.][0-9]{6}Z$' THEN RETURN false; END IF;
 RETURN to_char(v::timestamptz AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')=v;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
CREATE FUNCTION alert_parameters(k TEXT,p JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF p IS NULL OR octet_length(p::text)>512 THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN RETURN p='{}'::jsonb; END IF;
 IF k='procurement_budget_low' THEN
  IF NOT alert_keys(p,ARRAY['threshold_amount','recovery_amount']) THEN RETURN false; END IF;
  IF jsonb_typeof(p->'threshold_amount') IS DISTINCT FROM 'string'
    OR jsonb_typeof(p->'recovery_amount') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'threshold_amount',9223372036854775807)
    OR NOT alert_uint(p->>'recovery_amount',9223372036854775807) THEN RETURN false; END IF;
  RETURN (p->>'recovery_amount')::numeric >= (p->>'threshold_amount')::numeric;
 END IF;
 IF k IN ('customer_reconciliation_aged','procurement_unresolved_aged') THEN
  IF NOT alert_keys(p,ARRAY['minimum_age_seconds','trigger_count','recovery_count']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(p) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'minimum_age_seconds',604800) OR NOT alert_uint(p->>'trigger_count',1000)
    OR NOT alert_uint(p->>'recovery_count',999) THEN RETURN false; END IF;
  RETURN (p->>'minimum_age_seconds')::numeric>=600 AND (p->>'trigger_count')::numeric>=1
    AND (p->>'recovery_count')::numeric<(p->>'trigger_count')::numeric;
 END IF;
 RETURN false;
END $$;
CREATE FUNCTION alert_snapshot(v JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k TEXT; s TEXT;
BEGIN
 IF v IS NULL OR octet_length(v::text)>1024 OR NOT alert_keys(v,ARRAY['kind','scope_type','scope_id','label','parameters']) THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE x.key<>'parameters' AND jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
 k:=v->>'kind'; s:=v->>'scope_type';
 RETURN alert_id(v->>'scope_id') AND alert_label(v->>'label') AND alert_parameters(k,v->'parameters')
  AND ((k='channel_quarantined' AND s='channel')
    OR (k IN ('procurement_budget_low','procurement_unresolved_aged') AND s='procurement_account')
    OR (k='customer_reconciliation_aged' AND s='tenant'));
END $$;
CREATE FUNCTION alert_sample(v JSONB,k TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE n NUMERIC; age_seconds NUMERIC;
BEGIN
 IF v IS NULL OR octet_length(v::text)>2048 OR jsonb_typeof(v) IS DISTINCT FROM 'object'
   OR jsonb_typeof(v->'kind') IS DISTINCT FROM 'string' OR (v->>'kind') IS DISTINCT FROM k
   OR jsonb_typeof(v->'as_of') IS DISTINCT FROM 'string' OR NOT alert_time(v->>'as_of') THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN
  RETURN alert_keys(v,ARRAY['kind','as_of','enabled','quarantined'])
   AND jsonb_typeof(v->'enabled')='boolean' AND jsonb_typeof(v->'quarantined')='boolean';
 ELSIF k='procurement_budget_low' THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','account_state','limit_amount','held_amount','spent_amount','available_amount','currency']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF (v->>'account_state') NOT IN ('active','paused') OR (v->>'currency') IS DISTINCT FROM 'CNY'
    OR NOT alert_uint(v->>'limit_amount',9223372036854775807)
    OR NOT alert_uint(v->>'held_amount',power(10::numeric,100)-1)
    OR NOT alert_uint(v->>'spent_amount',power(10::numeric,100)-1)
    OR NOT alert_signed(v->>'available_amount') THEN RETURN false; END IF;
  RETURN (v->>'available_amount')::numeric=(v->>'limit_amount')::numeric-(v->>'held_amount')::numeric-(v->>'spent_amount')::numeric;
 ELSIF k IN ('customer_reconciliation_aged','procurement_unresolved_aged') THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','minimum_age_seconds','observed_count','count_is_lower_bound','oldest_created_at','oldest_reference_id']) THEN RETURN false; END IF;
  IF jsonb_typeof(v->'minimum_age_seconds') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'observed_count') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'count_is_lower_bound') IS DISTINCT FROM 'boolean'
    OR NOT alert_uint(v->>'minimum_age_seconds',604800) OR NOT alert_uint(v->>'observed_count',1001) THEN RETURN false; END IF;
  n:=(v->>'observed_count')::numeric; age_seconds:=(v->>'minimum_age_seconds')::numeric;
  IF age_seconds<600 OR (v->>'count_is_lower_bound')::boolean IS DISTINCT FROM (n=1001) THEN RETURN false; END IF;
  IF n=0 THEN RETURN v->'oldest_created_at'='null'::jsonb AND v->'oldest_reference_id'='null'::jsonb; END IF;
  IF jsonb_typeof(v->'oldest_created_at') IS DISTINCT FROM 'string' OR jsonb_typeof(v->'oldest_reference_id') IS DISTINCT FROM 'string'
    OR NOT alert_time(v->>'oldest_created_at') OR NOT alert_id(v->>'oldest_reference_id') THEN RETURN false; END IF;
  RETURN (v->>'oldest_created_at')::timestamptz <= (v->>'as_of')::timestamptz-age_seconds*interval '1 second';
 END IF;
 RETURN false;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
CREATE FUNCTION alert_decision(snapshot JSONB,sample JSONB,has_episode BOOLEAN) RETURNS TEXT LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k TEXT:=snapshot->>'kind'; p JSONB:=snapshot->'parameters'; n NUMERIC;
BEGIN
 IF NOT alert_snapshot(snapshot) OR NOT alert_sample(sample,k) OR has_episode IS NULL THEN RETURN 'invalid'; END IF;
 IF k='channel_quarantined' THEN RETURN CASE WHEN (sample->>'quarantined')::boolean THEN 'breach' ELSE 'clear' END; END IF;
 IF k='procurement_budget_low' THEN
  n:=(sample->>'available_amount')::numeric;
  IF has_episode THEN RETURN CASE WHEN n>(p->>'recovery_amount')::numeric THEN 'clear' ELSE 'hold' END; END IF;
  RETURN CASE WHEN n<=(p->>'threshold_amount')::numeric THEN 'breach' ELSE 'hold' END;
 END IF;
 IF (sample->>'minimum_age_seconds') IS DISTINCT FROM (p->>'minimum_age_seconds') THEN RETURN 'invalid'; END IF;
 n:=(sample->>'observed_count')::numeric;
 IF has_episode THEN RETURN CASE WHEN n<=(p->>'recovery_count')::numeric THEN 'clear' ELSE 'hold' END; END IF;
 RETURN CASE WHEN n>=(p->>'trigger_count')::numeric THEN 'breach' ELSE 'hold' END;
END $$;
```

Go的label校验用`unicode.IsControl/IsSpace`；SQL显式Unicode Cc与White_Space码点，不依赖数据库locale的字符类。数据库必须UTF8；A1穷举相应码点并验证二者接受集合一致。任一CHECK调用布尔validator均用`IS TRUE`，不能让SQL NULL当成功。所有持久时间列均检查isfinite；命令created_at由INSERT trigger固定DB时间。ACK在rule/runtime/episode锁后取一次有限DB时刻，复用为episode.acknowledged_at和event.observed_at/created_at，不能使用HTTP时间或等待锁前的command.created_at。

### 5.2 六张表与索引

```sql
CREATE TABLE alert_control (
 singleton BOOLEAN PRIMARY KEY CHECK(singleton IS TRUE)
);
INSERT INTO alert_control(singleton) VALUES(true);
CREATE TABLE alert_commands (
 operation_id TEXT PRIMARY KEY CHECK(alert_id(operation_id) IS TRUE),
 actor TEXT NOT NULL CHECK(alert_id(actor) IS TRUE),
 kind TEXT NOT NULL CHECK(kind IN ('rule_create','rule_configure','rule_retire','acknowledge')),
 target_id TEXT CHECK(target_id IS NULL OR alert_id(target_id) IS TRUE),
 canonical_digest TEXT NOT NULL CHECK(canonical_digest ~ '^[0-9a-f]{64}$'),
 transaction_id BIGINT NOT NULL DEFAULT txid_current() UNIQUE,
 status TEXT NOT NULL CHECK(status IN ('claimed','applied','rejected')),
 reason TEXT NOT NULL DEFAULT '', resource_id TEXT, result_revision BIGINT,
 created_at TIMESTAMPTZ(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((kind='rule_create' AND target_id IS NULL) OR (kind<>'rule_create' AND target_id IS NOT NULL)),
 CHECK(resource_id IS NULL OR alert_id(resource_id) IS TRUE),
 CHECK(result_revision IS NULL OR result_revision>0),
 CHECK((status='claimed' AND reason='' AND resource_id IS NULL AND result_revision IS NULL)
  OR (status='applied' AND reason='' AND resource_id IS NOT NULL AND result_revision IS NOT NULL)
  OR (status='rejected' AND reason IN ('alert_revision_stale','alert_rule_limit','alert_rule_exists','alert_rule_retired',
   'alert_source_missing','alert_not_open','alert_already_acknowledged','alert_not_found') AND result_revision IS NULL
   AND ((kind='rule_create' AND resource_id IS NULL) OR (kind<>'rule_create' AND resource_id IS NOT DISTINCT FROM target_id))))
);
CREATE TABLE alert_rules (
 id TEXT PRIMARY KEY CHECK(alert_id(id) IS TRUE),
 kind TEXT NOT NULL, scope_type TEXT NOT NULL, scope_id TEXT NOT NULL,
 label TEXT NOT NULL CHECK(alert_label(label) IS TRUE),
 state TEXT NOT NULL CHECK(state IN ('enabled','disabled','retired')),
 parameters JSONB NOT NULL,
 configuration_revision BIGINT NOT NULL CHECK(configuration_revision>0),
 created_by TEXT NOT NULL CHECK(alert_id(created_by) IS TRUE),
 updated_by TEXT NOT NULL CHECK(alert_id(updated_by) IS TRUE),
 created_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(created_at)), updated_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(updated_at)),
 retired_at TIMESTAMPTZ(6) CHECK(retired_at IS NULL OR isfinite(retired_at)),
 last_command_id TEXT NOT NULL REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(alert_snapshot(jsonb_build_object('kind',kind,'scope_type',scope_type,'scope_id',scope_id,'label',label,'parameters',parameters)) IS TRUE),
 CHECK(updated_at>=created_at),
 CHECK((state='retired' AND retired_at IS NOT NULL AND retired_at=updated_at) OR (state<>'retired' AND retired_at IS NULL))
);
CREATE UNIQUE INDEX alert_rules_live_scope ON alert_rules(kind,scope_id) WHERE state<>'retired';
CREATE INDEX alert_rules_page ON alert_rules(created_at DESC,id COLLATE "C" DESC);
CREATE INDEX alert_rules_last_command ON alert_rules(last_command_id);
CREATE TABLE alert_episodes (
 id TEXT PRIMARY KEY CHECK(alert_id(id) IS TRUE),
 rule_id TEXT NOT NULL REFERENCES alert_rules(id),
 generation BIGINT NOT NULL CHECK(generation>0),
 rule_revision BIGINT NOT NULL CHECK(rule_revision>0),
 rule_snapshot JSONB NOT NULL CHECK(alert_snapshot(rule_snapshot) IS TRUE),
 opened_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(opened_at)),
 initial_sample JSONB NOT NULL,
 last_seen_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(last_seen_at)), latest_sample JSONB NOT NULL,
 action_revision BIGINT NOT NULL CHECK(action_revision BETWEEN 1 AND 3),
 acknowledged_by TEXT, acknowledged_at TIMESTAMPTZ(6) CHECK(acknowledged_at IS NULL OR isfinite(acknowledged_at)), acknowledged_command_id TEXT REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 resolved_at TIMESTAMPTZ(6) CHECK(resolved_at IS NULL OR isfinite(resolved_at)), resolve_reason TEXT, resolved_command_id TEXT REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 UNIQUE(rule_id,generation), UNIQUE(rule_id,id),
 CHECK(acknowledged_by IS NULL OR alert_id(acknowledged_by) IS TRUE),
 CHECK((acknowledged_by IS NULL AND acknowledged_at IS NULL AND acknowledged_command_id IS NULL)
  OR (acknowledged_by IS NOT NULL AND acknowledged_at IS NOT NULL AND acknowledged_command_id IS NOT NULL AND acknowledged_at>=opened_at)),
 CHECK((resolved_at IS NULL AND resolve_reason IS NULL AND resolved_command_id IS NULL)
  OR (resolved_at IS NOT NULL AND resolved_at>=last_seen_at AND resolve_reason IN ('recovered','rule_changed','rule_disabled','rule_retired')
   AND ((resolve_reason='recovered' AND resolved_command_id IS NULL) OR (resolve_reason<>'recovered' AND resolved_command_id IS NOT NULL)))),
 CHECK(resolved_at IS NULL OR acknowledged_at IS NULL OR resolved_at>=acknowledged_at),
 CHECK(last_seen_at>=opened_at),
 CHECK(alert_sample(initial_sample,rule_snapshot->>'kind') IS TRUE),
 CHECK(alert_sample(latest_sample,rule_snapshot->>'kind') IS TRUE),
 CHECK(opened_at=(initial_sample->>'as_of')::timestamptz),
 CHECK(last_seen_at=(latest_sample->>'as_of')::timestamptz),
 CHECK(action_revision=1+(acknowledged_at IS NOT NULL)::int+(resolved_at IS NOT NULL)::int)
);
CREATE UNIQUE INDEX alert_episodes_unresolved ON alert_episodes(rule_id) WHERE resolved_at IS NULL;
CREATE INDEX alert_episodes_page ON alert_episodes(opened_at DESC,id COLLATE "C" DESC);
CREATE INDEX alert_episodes_rule_page ON alert_episodes(rule_id,opened_at DESC,id COLLATE "C" DESC);
CREATE TABLE alert_rule_runtime (
 rule_id TEXT PRIMARY KEY REFERENCES alert_rules(id),
 configured_revision BIGINT NOT NULL CHECK(configured_revision>0),
 write_revision BIGINT NOT NULL DEFAULT 1 CHECK(write_revision>0),
 episode_generation BIGINT NOT NULL DEFAULT 0 CHECK(episode_generation>=0),
 next_evaluation_at TIMESTAMPTZ(6) CHECK(next_evaluation_at IS NULL OR isfinite(next_evaluation_at)),
 last_attempt_at TIMESTAMPTZ(6) CHECK(last_attempt_at IS NULL OR isfinite(last_attempt_at)),
 last_success_at TIMESTAMPTZ(6) CHECK(last_success_at IS NULL OR isfinite(last_success_at)),
 evaluated_revision BIGINT, last_sample JSONB,
 last_error_code TEXT CHECK(last_error_code IN ('source_missing','source_query_timeout','source_unavailable','clock_regression','counter_exhausted','evaluation_failed')),
 current_episode_id TEXT,
 FOREIGN KEY(rule_id,current_episode_id) REFERENCES alert_episodes(rule_id,id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((last_success_at IS NULL AND evaluated_revision IS NULL AND last_sample IS NULL)
   OR (last_success_at IS NOT NULL AND evaluated_revision IS NOT NULL AND evaluated_revision>0 AND last_sample IS NOT NULL))
);
CREATE INDEX alert_runtime_due ON alert_rule_runtime(next_evaluation_at,rule_id COLLATE "C") WHERE next_evaluation_at IS NOT NULL;
CREATE TABLE alert_episode_events (
 id TEXT PRIMARY KEY CHECK(alert_id(id) IS TRUE),
 episode_id TEXT NOT NULL REFERENCES alert_episodes(id),
 action_revision BIGINT NOT NULL CHECK(action_revision BETWEEN 1 AND 3),
 type TEXT NOT NULL CHECK(type IN ('opened','acknowledged','recovered','rule_changed','rule_disabled','rule_retired')),
 actor TEXT NOT NULL CHECK(actor='system' OR alert_id(actor) IS TRUE),
 observed_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(observed_at)),
 safe_sample JSONB, created_at TIMESTAMPTZ(6) NOT NULL CHECK(isfinite(created_at)),
 command_id TEXT REFERENCES alert_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 UNIQUE(episode_id,action_revision),
 CHECK(created_at=observed_at),
 CHECK((type IN ('opened','recovered') AND actor='system' AND command_id IS NULL AND safe_sample IS NOT NULL)
   OR (type NOT IN ('opened','recovered') AND actor<>'system' AND command_id IS NOT NULL AND safe_sample IS NULL))
);
CREATE INDEX alert_events_page ON alert_episode_events(episode_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX alert_commands_actor ON alert_commands(actor,created_at DESC,operation_id COLLATE "C" DESC);
CREATE INDEX alert_events_command ON alert_episode_events(command_id) WHERE command_id IS NOT NULL;
CREATE INDEX alert_episodes_ack_command ON alert_episodes(acknowledged_command_id) WHERE acknowledged_command_id IS NOT NULL;
CREATE INDEX alert_episodes_resolve_command ON alert_episodes(resolved_command_id) WHERE resolved_command_id IS NOT NULL;

CREATE INDEX alert_customer_aging_source ON reservations(tenant_id,created_at,request_id COLLATE "C")
 WHERE status='pending_reconciliation';
CREATE INDEX alert_procurement_aging_source ON procurement_reservations(account_id,created_at,attempt_id COLLATE "C")
 WHERE state IN ('unknown','disputed');
```

四种规则identity验证在snapshot CHECK内；`CHECK`s对可空组合显式枚举。NUMERIC cast只在先验证后的函数中做；表中时间cast可能在validator前执行并抛异常，允许拒绝无效原始SQL，不得把异常映射成业务通过。两个源索引在维护排空时普通CREATE INDEX，与新表同迁移原子落地；不在运行实例任意CONCURRENTLY改库。现有采购account DESC索引未覆盖状态谓词且排序tie collation不同，A1以实际EXPLAIN决定是否等价，不能凭名称跳过。

### 5.3 命令、规则和runtime保护

```sql
CREATE FUNCTION alert_require_command(op TEXT,k TEXT,target TEXT,who TEXT) RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE c alert_commands;
BEGIN
 SELECT * INTO c FROM alert_commands WHERE operation_id=op;
 IF NOT FOUND THEN RETURN false; END IF;
 RETURN c.transaction_id=txid_current() AND c.kind=k AND c.target_id IS NOT DISTINCT FROM target
  AND c.actor=who AND c.status IN ('claimed','applied');
END $$;
CREATE FUNCTION alert_guard_command() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable alert command'; END IF;
 IF TG_OP='INSERT' THEN
  NEW.created_at:=clock_timestamp()::timestamptz(6);
  IF NEW.transaction_id IS DISTINCT FROM txid_current() OR NEW.status IS DISTINCT FROM 'claimed' THEN RAISE EXCEPTION 'claim required'; END IF;
 ELSE
  IF OLD.status IS DISTINCT FROM 'claimed' OR NEW.status NOT IN ('applied','rejected')
   OR (NEW.operation_id,NEW.actor,NEW.kind,NEW.target_id,NEW.canonical_digest,NEW.transaction_id,NEW.created_at)
    IS DISTINCT FROM (OLD.operation_id,OLD.actor,OLD.kind,OLD.target_id,OLD.canonical_digest,OLD.transaction_id,OLD.created_at)
   OR NEW.transaction_id IS DISTINCT FROM txid_current() THEN RAISE EXCEPTION 'immutable alert command'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_command_guard BEFORE INSERT OR UPDATE OR DELETE ON alert_commands FOR EACH ROW EXECUTE FUNCTION alert_guard_command();
CREATE FUNCTION alert_guard_rule() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN; expected_kind TEXT; live_count BIGINT; enabled_count BIGINT;
BEGIN
 -- repository在rule写之前已取control；直接SQL也必须串行验证实例上限。
 -- 任意raw UPDATE本来可能先持目标rule锁，不能将其死锁行为当成受支持API锁序。
 PERFORM singleton FROM alert_control WHERE singleton FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing alert control'; END IF;
 SELECT count(*),count(*) FILTER (WHERE state='enabled')
  INTO live_count,enabled_count FROM alert_rules WHERE state<>'retired';
 IF TG_OP='INSERT' THEN
  IF NEW.state='retired' OR NOT alert_require_command(NEW.last_command_id,'rule_create',NULL,NEW.created_by)
    OR NEW.updated_by IS DISTINCT FROM NEW.created_by OR NEW.updated_at IS DISTINCT FROM NEW.created_at THEN RAISE EXCEPTION 'invalid rule creation'; END IF;
  IF live_count>=256 OR (NEW.state='enabled' AND enabled_count>=64) THEN RAISE EXCEPTION 'alert rule limit'; END IF;
  NEW.configuration_revision:=1;
 ELSE
  IF (NEW.id,NEW.kind,NEW.scope_type,NEW.scope_id,NEW.created_by,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.kind,OLD.scope_type,OLD.scope_id,OLD.created_by,OLD.created_at) THEN RAISE EXCEPTION 'immutable rule identity'; END IF;
  changed:=(NEW.label,NEW.state,NEW.parameters) IS DISTINCT FROM (OLD.label,OLD.state,OLD.parameters);
  IF NOT changed THEN
   NEW.configuration_revision:=OLD.configuration_revision; NEW.last_command_id:=OLD.last_command_id;
   NEW.updated_by:=OLD.updated_by; NEW.updated_at:=OLD.updated_at; NEW.retired_at:=OLD.retired_at;
   RETURN NEW;
  END IF;
  IF OLD.state='retired' OR OLD.configuration_revision=9223372036854775807 THEN RAISE EXCEPTION 'rule terminal or exhausted'; END IF;
  expected_kind:=CASE WHEN NEW.state='retired' THEN 'rule_retire' ELSE 'rule_configure' END;
  IF NOT alert_require_command(NEW.last_command_id,expected_kind,NEW.id,NEW.updated_by)
    OR NEW.last_command_id IS NOT DISTINCT FROM OLD.last_command_id OR NEW.updated_at<OLD.updated_at THEN RAISE EXCEPTION 'rule command required'; END IF;
  IF NEW.state='retired' AND (NEW.label,NEW.parameters) IS DISTINCT FROM (OLD.label,OLD.parameters) THEN RAISE EXCEPTION 'retire changes identity'; END IF;
  IF OLD.state<>'enabled' AND NEW.state='enabled' AND enabled_count>=64 THEN RAISE EXCEPTION 'alert rule limit'; END IF;
  NEW.configuration_revision:=OLD.configuration_revision+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_rule_guard BEFORE INSERT OR UPDATE ON alert_rules FOR EACH ROW EXECUTE FUNCTION alert_guard_rule();
CREATE FUNCTION alert_guard_runtime() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r alert_rules; reset BOOLEAN;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=NEW.rule_id;
 IF NOT FOUND OR NEW.configured_revision IS DISTINCT FROM r.configuration_revision THEN RAISE EXCEPTION 'runtime configuration mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.episode_generation<>0 OR NEW.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'invalid initial runtime'; END IF;
  NEW.write_revision:=1; reset:=true;
 ELSE
  IF NEW.rule_id IS DISTINCT FROM OLD.rule_id OR OLD.write_revision=9223372036854775807 THEN RAISE EXCEPTION 'runtime immutable or exhausted'; END IF;
  NEW.write_revision:=OLD.write_revision+1;
  reset:=NEW.configured_revision IS DISTINCT FROM OLD.configured_revision;
  IF reset AND (NEW.episode_generation IS DISTINCT FROM OLD.episode_generation
    OR NEW.configured_revision<>OLD.configured_revision+1) THEN RAISE EXCEPTION 'invalid configuration reset'; END IF;
 END IF;
 IF reset THEN
  IF NOT EXISTS(SELECT 1 FROM alert_commands c WHERE c.operation_id=r.last_command_id
    AND c.transaction_id=txid_current() AND c.status IN ('claimed','applied')
    AND c.kind IN ('rule_create','rule_configure','rule_retire')) THEN RAISE EXCEPTION 'reset command required'; END IF;
  IF NEW.last_attempt_at IS NOT NULL OR NEW.last_success_at IS NOT NULL OR NEW.evaluated_revision IS NOT NULL
    OR NEW.last_sample IS NOT NULL OR NEW.last_error_code IS NOT NULL OR NEW.current_episode_id IS NOT NULL
    OR NEW.next_evaluation_at IS DISTINCT FROM (CASE WHEN r.state='enabled' THEN r.updated_at ELSE NULL END) THEN RAISE EXCEPTION 'invalid reset fields'; END IF;
  RETURN NEW;
 END IF;
 IF r.state<>'enabled' OR NEW.last_attempt_at IS NULL OR NEW.next_evaluation_at IS NULL THEN RAISE EXCEPTION 'rule not evaluable'; END IF;
 IF OLD.last_attempt_at IS NOT NULL AND NEW.last_attempt_at<OLD.last_attempt_at AND NEW.last_error_code IS DISTINCT FROM 'clock_regression' THEN RAISE EXCEPTION 'attempt clock regression'; END IF;
 IF NEW.last_error_code IS NULL THEN
  IF NEW.last_success_at IS DISTINCT FROM NEW.last_attempt_at OR NEW.evaluated_revision IS DISTINCT FROM r.configuration_revision
   OR NOT alert_sample(NEW.last_sample,r.kind) OR NEW.last_success_at IS DISTINCT FROM (NEW.last_sample->>'as_of')::timestamptz
   OR (OLD.last_success_at IS NOT NULL AND NEW.last_success_at<OLD.last_success_at)
   OR NEW.next_evaluation_at IS DISTINCT FROM NEW.last_success_at+interval '60 seconds'
   OR NOT (NEW.episode_generation=OLD.episode_generation OR
      (OLD.episode_generation<9223372036854775807 AND NEW.episode_generation=OLD.episode_generation+1)) THEN RAISE EXCEPTION 'invalid successful evaluation'; END IF;
 ELSE
  IF (NEW.last_success_at,NEW.evaluated_revision,NEW.last_sample,NEW.current_episode_id,NEW.episode_generation)
    IS DISTINCT FROM (OLD.last_success_at,OLD.evaluated_revision,OLD.last_sample,OLD.current_episode_id,OLD.episode_generation)
    OR NEW.next_evaluation_at IS DISTINCT FROM NEW.last_attempt_at+interval '30 seconds' THEN RAISE EXCEPTION 'failed evaluation changed success'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_runtime_guard BEFORE INSERT OR UPDATE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION alert_guard_runtime();
```

`clock_regression`失败允许last_attempt早于旧时刻，next_due按新DB失败时刻+30秒；原last_success/episode不变。恢复后的新成功仍要求不早于last_success。若rule/config/runtime计数耗尽，禁止溢出/换ID绕过；能保存则安全counter_exhausted，否则保留状态由stale和进程安全错误暴露，不能回零。

### 5.4 Episode单向状态与完整事件链

```sql
CREATE FUNCTION alert_guard_episode() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r alert_rules; changed_ack BOOLEAN; changed_resolve BOOLEAN;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=NEW.rule_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing rule'; END IF;
 IF TG_OP='INSERT' THEN
  IF r.state<>'enabled' OR NEW.rule_revision IS DISTINCT FROM r.configuration_revision
    OR NEW.rule_snapshot IS DISTINCT FROM jsonb_build_object('kind',r.kind,'scope_type',r.scope_type,'scope_id',r.scope_id,'label',r.label,'parameters',r.parameters)
    OR NEW.acknowledged_by IS NOT NULL OR NEW.resolved_at IS NOT NULL
    OR NEW.latest_sample IS DISTINCT FROM NEW.initial_sample OR NEW.last_seen_at IS DISTINCT FROM NEW.opened_at
    OR alert_decision(NEW.rule_snapshot,NEW.initial_sample,false) IS DISTINCT FROM 'breach' THEN RAISE EXCEPTION 'invalid episode opening'; END IF;
  NEW.action_revision:=1;
 ELSE
  IF (NEW.id,NEW.rule_id,NEW.generation,NEW.rule_revision,NEW.rule_snapshot,NEW.opened_at,NEW.initial_sample)
    IS DISTINCT FROM (OLD.id,OLD.rule_id,OLD.generation,OLD.rule_revision,OLD.rule_snapshot,OLD.opened_at,OLD.initial_sample)
    OR OLD.resolved_at IS NOT NULL OR NEW.last_seen_at<OLD.last_seen_at THEN RAISE EXCEPTION 'immutable episode'; END IF;
  changed_ack:=(NEW.acknowledged_by,NEW.acknowledged_at,NEW.acknowledged_command_id)
    IS DISTINCT FROM (OLD.acknowledged_by,OLD.acknowledged_at,OLD.acknowledged_command_id);
  changed_resolve:=(NEW.resolved_at,NEW.resolve_reason,NEW.resolved_command_id)
    IS DISTINCT FROM (OLD.resolved_at,OLD.resolve_reason,OLD.resolved_command_id);
  IF changed_ack THEN
   IF OLD.acknowledged_by IS NOT NULL OR NEW.acknowledged_by IS NULL OR NEW.acknowledged_at IS NULL
    OR NOT alert_require_command(NEW.acknowledged_command_id,'acknowledge',NEW.id,NEW.acknowledged_by)
    OR changed_resolve OR (NEW.latest_sample,NEW.last_seen_at) IS DISTINCT FROM (OLD.latest_sample,OLD.last_seen_at)
    THEN RAISE EXCEPTION 'invalid acknowledgement'; END IF;
  END IF;
  IF changed_resolve THEN
   IF NEW.resolved_at IS NULL THEN RAISE EXCEPTION 'invalid resolution'; END IF;
   IF NEW.resolve_reason='recovered' THEN
    IF NEW.resolved_command_id IS NOT NULL OR NEW.resolved_at IS DISTINCT FROM NEW.last_seen_at
      OR alert_decision(NEW.rule_snapshot,NEW.latest_sample,true) IS DISTINCT FROM 'clear' THEN RAISE EXCEPTION 'invalid recovery'; END IF;
   ELSE
    IF (NEW.latest_sample,NEW.last_seen_at) IS DISTINCT FROM (OLD.latest_sample,OLD.last_seen_at)
      OR NEW.resolved_command_id IS DISTINCT FROM r.last_command_id
      OR NEW.rule_revision>=r.configuration_revision
      OR NEW.resolved_at IS DISTINCT FROM r.updated_at
      OR NEW.resolve_reason IS DISTINCT FROM (CASE WHEN r.state='retired' THEN 'rule_retired' WHEN r.state='disabled' THEN 'rule_disabled' ELSE 'rule_changed' END)
      OR NOT alert_require_command(NEW.resolved_command_id,CASE WHEN r.state='retired' THEN 'rule_retire' ELSE 'rule_configure' END,r.id,r.updated_by)
      THEN RAISE EXCEPTION 'invalid configuration close'; END IF;
   END IF;
  END IF;
  NEW.action_revision:=1+(NEW.acknowledged_at IS NOT NULL)::int+(NEW.resolved_at IS NOT NULL)::int;
 END IF;
 IF NEW.resolved_at IS NULL AND alert_decision(NEW.rule_snapshot,NEW.latest_sample,true) NOT IN ('breach','hold') THEN RAISE EXCEPTION 'clear sample cannot stay open'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER alert_episode_guard BEFORE INSERT OR UPDATE ON alert_episodes FOR EACH ROW EXECUTE FUNCTION alert_guard_episode();

CREATE FUNCTION alert_check_episode(p_id TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE e alert_episodes; x alert_episode_events; n BIGINT; expected_n BIGINT; c alert_commands;
BEGIN
 SELECT * INTO e FROM alert_episodes WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'event has no episode'; END IF;
 expected_n:=1+(e.acknowledged_at IS NOT NULL)::int+(e.resolved_at IS NOT NULL)::int;
 SELECT count(*) INTO n FROM alert_episode_events WHERE episode_id=e.id;
 IF n IS DISTINCT FROM expected_n THEN RAISE EXCEPTION 'incomplete episode history'; END IF;
 SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=1;
 IF NOT FOUND OR (x.type,x.actor,x.observed_at,x.safe_sample,x.command_id)
   IS DISTINCT FROM ('opened'::text,'system'::text,e.opened_at,e.initial_sample,NULL::text) THEN RAISE EXCEPTION 'opened history mismatch'; END IF;
 IF e.acknowledged_at IS NOT NULL THEN
  SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=2;
  IF NOT FOUND OR (x.type,x.actor,x.observed_at,x.safe_sample,x.command_id)
    IS DISTINCT FROM ('acknowledged'::text,e.acknowledged_by,e.acknowledged_at,NULL::jsonb,e.acknowledged_command_id) THEN RAISE EXCEPTION 'ack history mismatch'; END IF;
  SELECT * INTO c FROM alert_commands WHERE operation_id=e.acknowledged_command_id;
  IF NOT FOUND OR c.status<>'applied' OR c.kind<>'acknowledge' OR c.target_id IS DISTINCT FROM e.id
    OR c.resource_id IS DISTINCT FROM e.id OR c.actor IS DISTINCT FROM e.acknowledged_by OR c.result_revision<>2 THEN RAISE EXCEPTION 'ack receipt mismatch'; END IF;
 END IF;
 IF e.resolved_at IS NOT NULL THEN
  SELECT * INTO x FROM alert_episode_events WHERE episode_id=e.id AND action_revision=e.action_revision;
  IF NOT FOUND OR x.type IS DISTINCT FROM e.resolve_reason OR x.observed_at IS DISTINCT FROM e.resolved_at
    OR x.command_id IS DISTINCT FROM e.resolved_command_id THEN RAISE EXCEPTION 'resolve history mismatch'; END IF;
  IF e.resolve_reason='recovered' THEN
   IF x.actor IS DISTINCT FROM 'system' OR x.safe_sample IS DISTINCT FROM e.latest_sample THEN RAISE EXCEPTION 'recovery sample mismatch'; END IF;
  ELSE
   SELECT * INTO c FROM alert_commands WHERE operation_id=e.resolved_command_id;
   IF NOT FOUND OR c.status<>'applied' OR c.target_id IS DISTINCT FROM e.rule_id OR c.resource_id IS DISTINCT FROM e.rule_id
    OR c.kind IS DISTINCT FROM (CASE WHEN e.resolve_reason='rule_retired' THEN 'rule_retire' ELSE 'rule_configure' END)
    OR x.actor IS DISTINCT FROM c.actor OR x.safe_sample IS NOT NULL THEN RAISE EXCEPTION 'configuration event receipt mismatch'; END IF;
  END IF;
 END IF;
END $$;
CREATE FUNCTION alert_episode_history_constraint() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e alert_episodes; t alert_rule_runtime;
BEGIN
 IF TG_TABLE_NAME='alert_episodes' THEN
  PERFORM alert_check_episode(NEW.id);
  SELECT * INTO e FROM alert_episodes WHERE id=NEW.id;
  IF e.resolve_reason='recovered' THEN
   SELECT * INTO t FROM alert_rule_runtime WHERE rule_id=e.rule_id;
   IF NOT FOUND OR t.last_sample IS DISTINCT FROM e.latest_sample OR t.last_success_at IS DISTINCT FROM e.last_seen_at
     OR t.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'recovery runtime mismatch'; END IF;
  END IF;
 ELSE
  PERFORM alert_check_episode(NEW.episode_id);
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER alert_episode_history AFTER INSERT OR UPDATE ON alert_episodes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_episode_history_constraint();
CREATE CONSTRAINT TRIGGER alert_event_history AFTER INSERT ON alert_episode_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_episode_history_constraint();
```

配置关闭顺序必须**先更新rule，再更新旧episode，再重置runtime**，以便episode BEFORE触发器读取新配置和命令；未提交期间只有本事务可见。旧草案“同事务可任意排序”不得用于这个BEFORE约束。新建为command→rule→runtime；评估为rule/runtime锁后episode/event→runtime；ACK为episode/event→命令终态。约束仅deferred于最终跨表一致性，不解除BEFORE单向检查。

### 5.5 Runtime、rule、command双向终态检查与不可删保护

```sql
CREATE FUNCTION alert_check_rule(p_id TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE r alert_rules; t alert_rule_runtime; e alert_episodes; max_generation BIGINT; c alert_commands;
BEGIN
 SELECT * INTO r FROM alert_rules WHERE id=p_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing alert rule'; END IF;
 SELECT * INTO t FROM alert_rule_runtime WHERE rule_id=p_id;
 IF NOT FOUND OR t.configured_revision IS DISTINCT FROM r.configuration_revision THEN RAISE EXCEPTION 'missing/stale runtime'; END IF;
 SELECT * INTO c FROM alert_commands WHERE operation_id=r.last_command_id;
 IF NOT FOUND OR c.status<>'applied' OR c.actor IS DISTINCT FROM r.updated_by OR c.resource_id IS DISTINCT FROM r.id
  OR c.result_revision IS DISTINCT FROM r.configuration_revision
  OR c.kind NOT IN ('rule_create','rule_configure','rule_retire') THEN RAISE EXCEPTION 'rule receipt mismatch'; END IF;
 SELECT generation INTO max_generation FROM alert_episodes WHERE rule_id=p_id ORDER BY generation DESC LIMIT 1;
 IF t.episode_generation IS DISTINCT FROM COALESCE(max_generation,0) THEN RAISE EXCEPTION 'generation mismatch'; END IF;
 SELECT * INTO e FROM alert_episodes WHERE rule_id=p_id AND resolved_at IS NULL;
 IF FOUND THEN
  IF t.current_episode_id IS DISTINCT FROM e.id OR e.rule_revision IS DISTINCT FROM r.configuration_revision OR r.state<>'enabled'
    OR t.last_sample IS DISTINCT FROM e.latest_sample OR t.last_success_at IS DISTINCT FROM e.last_seen_at THEN RAISE EXCEPTION 'active pointer/sample mismatch'; END IF;
 ELSE
  IF t.current_episode_id IS NOT NULL THEN RAISE EXCEPTION 'orphan current pointer'; END IF;
 END IF;
 IF r.state<>'enabled' AND t.next_evaluation_at IS NOT NULL THEN RAISE EXCEPTION 'inactive due time'; END IF;
 IF t.last_success_at IS NOT NULL THEN
  IF t.evaluated_revision IS DISTINCT FROM r.configuration_revision OR NOT alert_sample(t.last_sample,r.kind)
    OR t.last_success_at IS DISTINCT FROM (t.last_sample->>'as_of')::timestamptz THEN RAISE EXCEPTION 'runtime sample mismatch'; END IF;
  IF t.current_episode_id IS NULL AND alert_decision(jsonb_build_object('kind',r.kind,'scope_type',r.scope_type,'scope_id',r.scope_id,'label',r.label,'parameters',r.parameters),t.last_sample,false)='breach'
    THEN RAISE EXCEPTION 'breach has no episode'; END IF;
 END IF;
END $$;
CREATE FUNCTION alert_rule_constraint() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='alert_rules' THEN PERFORM alert_check_rule(NEW.id);
 ELSE PERFORM alert_check_rule(NEW.rule_id); END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER alert_rule_consistent AFTER INSERT OR UPDATE ON alert_rules DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();
CREATE CONSTRAINT TRIGGER alert_runtime_consistent AFTER INSERT OR UPDATE ON alert_rule_runtime DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();
CREATE CONSTRAINT TRIGGER alert_episode_runtime_consistent AFTER INSERT OR UPDATE ON alert_episodes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_rule_constraint();

CREATE FUNCTION alert_command_constraint() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c alert_commands; r alert_rules; e alert_episodes;
BEGIN
 SELECT * INTO c FROM alert_commands WHERE operation_id=NEW.operation_id;
 IF c.status IS DISTINCT FROM 'applied' AND c.status IS DISTINCT FROM 'rejected' THEN RAISE EXCEPTION 'uncommitted alert claim'; END IF;
 IF c.status='rejected' THEN
  IF EXISTS(SELECT 1 FROM alert_rules WHERE last_command_id=c.operation_id)
    OR EXISTS(SELECT 1 FROM alert_episodes WHERE acknowledged_command_id=c.operation_id OR resolved_command_id=c.operation_id)
    OR EXISTS(SELECT 1 FROM alert_episode_events WHERE command_id=c.operation_id) THEN RAISE EXCEPTION 'rejected command has effect'; END IF;
  RETURN NULL;
 END IF;
 IF c.kind='acknowledge' THEN
  SELECT * INTO e FROM alert_episodes WHERE id=c.resource_id;
  IF NOT FOUND OR e.id IS DISTINCT FROM c.target_id OR e.acknowledged_command_id IS DISTINCT FROM c.operation_id
    OR e.acknowledged_by IS DISTINCT FROM c.actor OR c.result_revision IS DISTINCT FROM 2::bigint THEN RAISE EXCEPTION 'ack command has no effect'; END IF;
  PERFORM alert_check_episode(e.id);
 ELSE
  SELECT * INTO r FROM alert_rules WHERE id=c.resource_id;
  IF NOT FOUND OR c.result_revision IS DISTINCT FROM r.configuration_revision
    OR (c.kind<>'rule_create' AND r.id IS DISTINCT FROM c.target_id)
    OR (c.kind='rule_create' AND (r.last_command_id IS DISTINCT FROM c.operation_id OR r.created_by IS DISTINCT FROM c.actor OR r.configuration_revision<>1))
    OR (c.kind='rule_retire' AND (r.last_command_id IS DISTINCT FROM c.operation_id OR r.state<>'retired'))
    OR (c.kind='rule_configure' AND r.state='retired') THEN RAISE EXCEPTION 'rule command mismatch'; END IF;
  -- configure可为CAS正确的no-op；若不是当前last_command，不允许本命令另有episode/event效果。
  IF c.kind='rule_configure' AND r.last_command_id IS DISTINCT FROM c.operation_id
    AND (EXISTS(SELECT 1 FROM alert_episodes WHERE resolved_command_id=c.operation_id)
      OR EXISTS(SELECT 1 FROM alert_episode_events WHERE command_id=c.operation_id)) THEN RAISE EXCEPTION 'no-op command has state effect'; END IF;
  PERFORM alert_check_rule(r.id);
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER alert_command_terminal AFTER INSERT OR UPDATE ON alert_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_command_constraint();

CREATE TRIGGER alert_control_immutable BEFORE UPDATE OR DELETE ON alert_control FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_rules_no_delete BEFORE DELETE ON alert_rules FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_runtime_no_delete BEFORE DELETE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_episodes_no_delete BEFORE DELETE ON alert_episodes FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER alert_events_immutable BEFORE UPDATE OR DELETE ON alert_episode_events FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

REVOKE ALL ON alert_control,alert_commands,alert_rules,alert_rule_runtime,alert_episodes,alert_episode_events FROM PUBLIC;
```

每个约束触发器查询事务最终行，而不是早期NEW快照；因此claim INSERT的deferred检查看到applied/rejected。rule与runtime/episode两侧都有触发器，不能只移动pointer或只插事件绕过。历史event最多3条，链检查有确定上界。app不能关trigger，owner/migration能够修改不属于app威胁边界；运行app持有同数据库凭据本就能模拟actor，HTTP身份来源仍由服务端会话保证，不能把这些触发器宣称成数据库独立认证系统。

### 5.6 角色provision精确增量

2026-11-01，集成人独立复核追加冻结以下函数解析边界。真实受限APP已复现：临时`alert_rules`影子将真实disabled规则伪装为enabled，使原runtime触发器错误接受更新。因此019须在提交前为以下全部20个新函数固定**本次实际迁移schema、pg_catalog、pg_temp**，保持SECURITY INVOKER；不能硬编码public，也不能让调用者search_path或隐式pg_temp抢占内部关系/row type/函数。旧001–018不修改，现有随机schema测试必须继续运行。

```sql
DO $$
DECLARE f RECORD; own_schema TEXT:=current_schema(); n INTEGER:=0;
BEGIN
 FOR f IN SELECT p.oid FROM pg_catalog.pg_proc p
  JOIN pg_catalog.pg_namespace ns ON ns.oid=p.pronamespace
  WHERE ns.nspname=own_schema AND p.proname=ANY(ARRAY[
   'alert_id','alert_keys','alert_uint','alert_signed','alert_label','alert_time',
   'alert_parameters','alert_snapshot','alert_sample','alert_decision',
   'alert_require_command','alert_guard_command','alert_guard_rule','alert_guard_runtime',
   'alert_guard_episode','alert_check_episode','alert_episode_history_constraint',
   'alert_check_rule','alert_rule_constraint','alert_command_constraint'])
 LOOP
  EXECUTE format('ALTER FUNCTION %s SET search_path = %I, pg_catalog, pg_temp',
   f.oid::regprocedure,own_schema);
  n:=n+1;
 END LOOP;
 IF n<>20 THEN RAISE EXCEPTION 'alert function set mismatch'; END IF;
END $$;
```

验收须先运行真实TEMP影子反例（修复前成功，修复后拒绝），再核对20个函数均invoker且proconfig精确固定实际schema；影子不存在时正常APP配置/评估/确认和原生命周期继续通过。此补项只加固已有状态不变量，不增加产品能力或权限。019尚未应用到评审预览，因此随本次新迁移实现，不修改任何已应用迁移。

迁移只负责PUBLIC revoke。`operations.ProvisionFor`在其既有blanket grant之后追加以下权限矩阵，表名静态白名单、角色用`pgx.Identifier.Sanitize()`，不得拼接未校验配置；所有表均schema-qualified。下面`APP/PROJECTOR`是角色占位符，**不是让迁移建立默认生产角色或凭据**。

```sql
REVOKE ALL ON alert_control,alert_commands,alert_rules,alert_rule_runtime,alert_episodes,alert_episode_events FROM PROJECTOR;
REVOKE ALL ON alert_control,alert_commands,alert_rules,alert_rule_runtime,alert_episodes,alert_episode_events FROM APP;
GRANT SELECT ON alert_control,alert_commands,alert_rules,alert_rule_runtime,alert_episodes,alert_episode_events TO APP;
GRANT UPDATE(singleton) ON alert_control TO APP; -- 仅为FOR UPDATE权限；触发器禁止实际变更
GRANT INSERT,UPDATE ON alert_commands,alert_rules,alert_rule_runtime,alert_episodes TO APP;
GRANT INSERT ON alert_episode_events TO APP;
```

APP无任一新表DELETE/TRUNCATE/TRIGGER/REFERENCES，无event UPDATE；command UPDATE为claimed→terminal所需，由trigger阻止终态改写。PROJECTOR不读取/写入新表；专用worker pool仍用APP角色，最多2连接，不能使用迁移或projection连接。业务源只需APP既有SELECT，不新增源UPDATE授权。必须在随机schema的真实受限角色上测试，不能以owner通过代替权限验收。若部署角色继承其他高权角色，启动/验收明确拒绝其不符合边界，不能只看单次REVOKE结果。

#### 5.6.1 有效角色权限的拒绝口径

2026-11-01，独立复核发现单独REVOKE直接ACL不能移除group/PUBLIC带来的有效权限，集成人接受下述补强后实施；不修改019或其他已应用迁移，也不撤销外部group的权限。

- `ProvisionFor`将现有全部直接GRANT/REVOKE与最终核验放在同一事务，按显式schema及APP/PROJECTOR角色核对；核验失败整体回滚本次直接ACL变更并返回固定安全错误。离线owner可以执行此流程，不要求其连接身份就是目标角色。
- 提供`VerifyAlertRole(ctx,pool,AlertApplicationRole|AlertProjectionRole)`供main在对应两连接池可用、任何worker/HTTP开始前调用。它只执行catalog读取，使用有界只读RR事务，在连接实际`current_schema()`查六表OID，并以实际`current_user`作所需矩阵核验。`session_user`若不同，还须核验RESET ROLE可回到的权限上下文；owner登录后SET ROLE不能当作生产受限LOGIN资格。
- 必需table权限须与上表一致：APP六表SELECT，四张可变表INSERT/UPDATE，event仅INSERT，control仅列级UPDATE(singleton)；PROJECTOR六表全部无权。对SELECT/INSERT/UPDATE/REFERENCES同时检查有效列权限，防止只撤销表级ACL后列级授权仍然存在。两角色均不得持有六表任一权限的GRANT OPTION，不得为六表或受控schema的直接/有效owner，不得有schema CREATE或schema权限转授权。
- 对原角色、可SET ROLE到达角色及可管理membership闭包检查禁止权限及ownership：`pg_auth_members.admin_option`即使对应INHERIT FALSE/SET FALSE，仍可允许角色自行GRANT SET TRUE；不能只查现有SET路径。对每个已达上下文继续展开SET边与可由该上下文（含其有效继承角色）管理的ADMIN边，直到去重闭包。已达角色只要求权限为本角色允许集合的子集，不强求其具有全部必需权限。继承型`has_table_privilege`/`has_any_column_privilege`计算PUBLIC及group有效ACL。无权group、仅提供允许权限的group、不可继承/不可SET且没有可提升ADMIN路径的无害membership允许保留，不以存在membership本身拒绝。
- 原角色、session回退角色及可SET到达角色不得为SUPERUSER、CREATEROLE、CREATEDB、REPLICATION或BYPASSRLS。该限制适用于数据库部署角色，不针对平台`users.platform_role`，不改变既有采购/经营的业务角色或表权限。核验范围是告警六表与其namespace，不能据此宣称已经审计全库所有对象、SECURITY DEFINER或数据库外部权限；运行后owner再次修改ACL仍须运维门禁与重启复核。
- 验收使用专用PG随机schema/角色：真实APP/PROJECTOR LOGIN与两次Provision允许矩阵通过；分别注入继承UPDATE、继承列级UPDATE、PROJECTOR继承列级SELECT、继承GRANT OPTION、NOINHERIT但可SET的越权group、ADMIN TRUE但INHERIT/SET FALSE的可提升group与其后续SET链、schema CREATE、六表/schema owner及SUPERUSER等高权属性，Verify和Provision都必须拒绝，且group权限仍在。ADMIN反例须用真实受限LOGIN成功执行自行GRANT SET TRUE证明，不以owner代替。目标角色的直接table/column漂移在只读Verify中拒绝；若离线Provision现有REVOKE可以清除并恢复允许矩阵，则修复后可以成功，不能把可修复的直接ACL误当成必须拒绝的外部继承。另验证低权group与不可达membership允许、缺必需权限被只读Verify拒绝、只读Verify不修复ACL、owner会话SET ROLE不获启动资格，并重新运行采购/经营/定价原角色矩阵及财务生命周期。

## 6. 源查询、调度与失败CAS

### 6.1 唯一快照与索引出口

worker事务在取rule/runtime锁后执行以下**四选一**源SQL；无锁定子句、无业务FK、无先后多个source语句拼样本。所有时间转JSON前由Go Time统一微秒格式；DECIMAL列`::text`以NUMERIC无损scan。SQL返回`source_exists/as_of`与typed字段，领域层构建已验证Sample；不把`to_jsonb(source)`透传。

```sql
-- channel；LEFT JOIN即使源缺失也返回一行，NULL不当false。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of)
SELECT (c.id IS NOT NULL) AS source_exists,o.as_of,c.enabled,c.quarantined
FROM observed o LEFT JOIN channels c ON c.id=$1;

-- 019登记采购预算；可负available，非供应商账户现金观察。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of)
SELECT (a.id IS NOT NULL) AS source_exists,o.as_of,a.state,a.limit_amount::text,
 a.held_amount::text,a.spent_amount::text,
 (a.limit_amount::numeric-a.held_amount-a.spent_amount)::text AS available_amount
FROM observed o LEFT JOIN procurement_accounts a ON a.id=$1;

-- 客户未决创建年龄；$2为已经验证的600..604800秒整数，不是SQL片段。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of),
eligible AS MATERIALIZED (
 SELECT r.created_at,r.request_id AS reference_id
 FROM reservations r
 WHERE r.tenant_id=$1 AND r.status='pending_reconciliation'
  AND r.created_at<=(SELECT as_of FROM observed)-$2::bigint*interval '1 second'
 ORDER BY r.created_at ASC,r.request_id COLLATE "C" ASC LIMIT 1001
), measured AS (
 SELECT count(*) AS observed_count FROM eligible
), oldest AS (
 SELECT created_at,reference_id FROM eligible
 ORDER BY created_at ASC,reference_id COLLATE "C" ASC LIMIT 1
)
SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) AS source_exists,o.as_of,
 m.observed_count::text,(m.observed_count=1001) AS count_is_lower_bound,
 oldest.created_at,oldest.reference_id
FROM observed o CROSS JOIN measured m LEFT JOIN oldest ON true;

-- 采购未决创建年龄；独立于客户24h释放。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of),
eligible AS MATERIALIZED (
 SELECT r.created_at,r.attempt_id AS reference_id
 FROM procurement_reservations r
 WHERE r.account_id=$1 AND r.state IN ('unknown','disputed')
  AND r.created_at<=(SELECT as_of FROM observed)-$2::bigint*interval '1 second'
 ORDER BY r.created_at ASC,r.attempt_id COLLATE "C" ASC LIMIT 1001
), measured AS (
 SELECT count(*) AS observed_count FROM eligible
), oldest AS (
 SELECT created_at,reference_id FROM eligible
 ORDER BY created_at ASC,reference_id COLLATE "C" ASC LIMIT 1
)
SELECT EXISTS(SELECT 1 FROM procurement_accounts WHERE id=$1) AS source_exists,o.as_of,
 m.observed_count::text,(m.observed_count=1001) AS count_is_lower_bound,
 oldest.created_at,oldest.reference_id
FROM observed o CROSS JOIN measured m LEFT JOIN oldest ON true;
```

唯一as_of由MATERIALIZED CTE保证；语句MVCC快照与该时刻不等价于串行化“真实瞬时全局”，并发source提交可能下一次才看见。源缺失返回source_missing而不是clear；合法source存在且eligible空才count0。父行停用不影响存在位。EXPLAIN门禁在≥10万其他scope行、当前scope≥1002合格、0/少量合格与大量年轻不合格行的专用PG数据上执行`EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON)`，保留PG版本/统计/fixture规模/计划/actual rows/buffers；须证明eligible在匹配partial index上有界取≤1001行，无全scope排序/全表计数。小表顺扫可合理，但不得仅小表计划就宣称大表有界。不能以enable_seqscan=off强迫的计划作为唯一证据。

### 6.2 认领、决策和晚回失败

每槽空闲、Guard通过后调用一次EvaluateDue。总2秒包括pool等待、事务和失败记录；transaction statement_timeout≤剩余预算，lock_timeout≤250ms；源SQL≤1500ms，并尽量留250ms用于回滚/错误记录。不排队claim，不跨事务持claim/lease。

```sql
-- READ COMMITTED事务，先锁规则（只锁r，不锁t），再锁runtime。
SELECT r.id
FROM alert_rules r JOIN alert_rule_runtime t ON t.rule_id=r.id
WHERE r.state='enabled' AND t.next_evaluation_at<=clock_timestamp()
ORDER BY t.next_evaluation_at ASC,r.id COLLATE "C" ASC
FOR UPDATE OF r SKIP LOCKED LIMIT 1;
SELECT configured_revision,write_revision,episode_generation,next_evaluation_at,
 last_attempt_at,last_success_at,evaluated_revision,last_sample,last_error_code,current_episode_id
FROM alert_rule_runtime WHERE rule_id=$1 FOR UPDATE;
-- 新语句重查enabled/configured_revision/due，避免认领前快照已经过时。
```

拿到锁记内部preimage`rule_id,configuration_revision,runtime.write_revision`。执行source并确认`as_of>=last_success_at`，生成决策：无episode breach→generation+1新episode/opened事件；已有episode breach/hold→仅latest_sample/last_seen；clear→resolved recovered事件且pointer空；无episode hold/clear→只更新runtime。episode.sample时间=as_of。成功同事务runtime写last_attempt/success/as_of、evaluated_revision、sample、error null、next_due=as_of+60秒；审计仅状态action，不为每轮sample产生无限历史。没有episode变化则无event/action_revision变化。

发生已知source失败先回滚整个事务，不能提交部分opened/closed。若主worker未取消、Guard仍有效且预算足够，用短新事务按rule→runtime锁，再同时比对preimage两版本和enabled。**任一变更都丢弃晚回错误**，尤其另一槽同rule revision的新成功。匹配才以新单一DB时刻设置last_attempt、safe_error、next_due=failed_at+30秒；其余成功样本/episode/generation完全保持。`write_revision`触发器每次runtime更新递增，失败也递增；公开revision不变。配置/ACK并发：配置改变版本必丢弃，ACK不改runtime且可接受该失败，不改变ACK。

已知错误分类：源不存在source_missing；源statement timeout且父ctx仍有效source_query_timeout；已证明连接/源不可用source_unavailable；时间小于last_success clock_regression；配置/代数计数溢出counter_exhausted；无法安全分类evaluation_failed。ctx取消/Guard失败不记source故障。不确定COMMIT不补写失败（可能成功已提交），只安全report并进程退避，下一轮重读源/告警决定。数据库不可用/预算不足/失败记录也失败，槽backoff从1秒指数到最多30秒，成功事务或可靠idle重置；无热循环、无新goroutine定时重试泄漏。

### 6.3 main与生命周期

`OPERATIONS_ALERTS_ENABLED`严格bool默认false；main启动冻结runtimeoptions，恢复模式强制禁评估。新增专用pool `MaxConns=2,MinConns=0`，沿当前TLS/schema/APP role配置，statement/lock timeout由每事务下调；服务32连接池/财务投影连接保持原合同。无配置或off无需启动循环/新增连接，但读写store仍可用并返回paused。配置格式错启动失败，不静默当false。

双固定槽各自循环，tick1秒只唤醒当前空槽，不累计tick、不缓冲任务。任务前Guard有独立≤1秒fresh核验。参考main已有mail lifecycle guard：任务开始前先检查ctx，真正所有权检查使用独立有界ctx，完成再检查主ctx；不能把正常SIGTERM取消的check喂给`Instance.Check`导致永久fence并中断HTTP排空。Guard真实错误沿既有fence；普通source timeout仅告警降级。Guard调用必须由main保证不静默nil，单测stub可有显式test构造器。

SIGTERM ready=false→停止新评估/取消槽→HTTP与既有在途按原drain合同退出→等待alert Run返回→关闭alert pool；专用pool关闭不能早于槽退出。024不改变已有310秒HTTP排空或财务收尾政策。真实ownership fencing、恢复只读和迁移前DrainPredecessor沿原main；新迁移应用/provision只在旧实例已排空的维护流程，不由A worker自行升级。

## 7. 并行实现所有权和串行门禁

| 工作面 | 可独占文件/输出 | 依赖与共享文件 |
| --- | --- | --- |
| A1领域/PG | 待分配新迁移；`internal/alerts`所有types/repository/SQL/命令/只读/评估/PG测试 | 先交固定types和Store接口；`operations.ProvisionFor`由集成人安排独占合并；只追加索引，不改019/002 SQL |
| A2worker | `internal/alertworker`及专项生命周期测试、config字段/测试提案 | 依赖EvaluationRepository；main由集成人单人汇合，禁止与其他切片同时写main |
| A3HTTP | alert handlers/admission/strict decoder/cursor及专项测试 | 依赖A1固定DTO/错误；server/router共享入口由集成人合并；读当前role必须在repo，不能仅handler |
| A4UI | 平台规则/当前/历史/event页、命令unknown状态与无损值测试 | 等A3矩阵；web与022-A1互斥写时段；不借此改钱包/支付/身份 |
| A5审查/验证 | 只读反例审查、专用随机schema真实main/浏览器/进程测试、验证记录 | 测试失败回原owner修改；集成人汇总后统一HLD/LLD/架构/快照 |

冻结顺序A0接受补项+精确SQL/Go接口→A1先提交types可供A2/A3并行→A3请求响应稳定后A4→集成PG/真实程序/浏览器→架构reviewed refresh→只读检查。A1未证实DDL/真实role/源EXPLAIN前不能把worker/UI可运行当验收。B0媒体详细设计、E0身份详细设计可与A实现并行读审；涉及资金公共类型、身份事务和共享web时仍由集成人排锁所有权，不把024当一包任务全并发。

## 8. 必要测试与实际进程门禁

除§8.1的DDL应用外，下列行为/权限/并发/进程验收当前均**未执行**；测试凭据仅环境/临时目录，专用PG随机schema/角色，清理不触碰预览资金或付费provider。

- 值/DTO：null/缺失/重复/未知键、JSON number、Unicode空白/控制字符、最大数字、非法时间/不规范UUID、JSONB cast异常、样本2KiB/snapshot1KiB；schema和Go接受集合一致。capabilities与freshness每一优先级、负available/100位held/spent、aging0/1001 null和lower_bound证明。
- SQL反例：每表无DELETE；终态command改写/第二终态/claimed COMMIT/同tx第二命令；配置未关联新命令、跳版本、重用旧命令、rejected带效果、no-op带close、retired复活；event缺/多/错actor/错sample/错revision/孤立、NULL绕过、移runtime pointer/孤立open、错误generation、替换ACK、修改已解决episode/初始snapshot均COMMIT失败。受限APP和PROJECTOR独立验证，不只owner执行。
- 源/财务隔离：持业务根/账户NO KEY UPDATE时告警普通MVCC读不等待该行锁；反向持alert锁商业操作可完成；source缺失不clear、paused/disabled仍存在、采购unknown不会随客户24h释放消失；并发源更新样本来自单语句快照。真实EXPLAIN出口如§6.1。
- 并发屏障：两槽首次breach一episode；ACK/clear、ACK/config、retire/recreate、同operation同体/异体/他人、64enabled/256live边界、持锁超时、workerrollback后另一成功再晚写失败CAS丢弃。PG并发事务显式屏障，不以sleep猜顺序。
- HTTP：当前role撤销/停用、原actor operation保密、finance不能管理/降权后原管理回执拒绝、tenant/API Key/MFA/CSRF/Origin/reauth/恢复；读写独立桶容量/全局与actor并发/TTL/release once；预算包括pool等待，cancel后无Rows/连接泄漏；响应不超256KiB且失败不半页，固定50+1/cursor过滤绑定/no-store。
- 故障模拟：COMMIT前/后注入、audit/event/receipt写失败整体回滚、取消/source timeout、错误记录失败、clock regression与计数耗尽不fake clear；worker COMMIT unknown不记反向失败，不重发任何模型/资金操作。测试说明这是PG/驱动状态模拟。
- **实际主进程SIGTERM**：独立真实main+真实PG启动、开启规则评估、锁屏障令槽在途，发送OS信号；验证ready停止、槽取消退出、无新评估、专用pool退出和HTTP排空均按时完成，正常取消未触发所有权fence。记录PID、信号/时间、退出码、PG连接和持久状态。
- **实际主进程SIGKILL**：独立进程在已验证的COMMIT前屏障杀死，重启后无半episode；在episode/event/runtime COMMIT后、worker收到结果前屏障杀死，重启后只有一episode、无重复opened；恢复→再breach新generation。屏障仅测试注入构建或PG外部锁/代理，不在生产暴露调试路由。不能用手工插DB状态代替kill窗口。
- 浏览器：专用真实main/HTTP/PG，开启→创建→真实持久source触发→确认→source恢复→再次触发→label/关闭/退役→历史事件；默认off/恢复/错误/stale/至少计数/登记预算说明、unknown只GET、关闭/换身份/换筛选晚回不污染。fixture页面只能算前端验证，不是链路验收。

专项测试通过后由集成人按实际修改运行race/vet/前端strict和`make check`/适用`make verify-mvp`。本片无付费provider、外发通知或HA认证；不关闭其他切片生产/外部门禁。所有并行结果先汇总，语义复核统一架构的模块、观察链路、数据/锁/权限、能力planned/implemented状态、源码链接及验证记录，再由集成人执行：

```sh
go run ./scripts/check-architecture --refresh --reviewed --reason '024-A具体实施与验证范围；观察模块、只读来源、数据状态机、权限及worker生命周期复核结论'
go run ./scripts/check-architecture
```

当前只有新设计稿，不能执行上述refresh来声称实现同步；本文件新增进入集成人下一次设计变更复核清单。任何真实SQL失败、source计划失界或签名调整都先回到本合同修订，冻结后才进入实现。

### 8.1 本次DDL应用验证记录

2026-11-01，经集成人明确授权，将§5.1–5.5共五段SQL原样抽取到忽略目录`.local/024-a0-ddl/contract.sql`，临时Go harness在专用`127.0.0.1:55439/moni_test`的随机`alerts_a0_<uuid>` schema先执行既有001–017，再事务执行本稿DDL并COMMIT。PostgreSQL **16.15 (Homebrew)**；最终输出`DDL_APPLIED_COMMITTED baseline=17`及`RANDOM_SCHEMA_CLEANED`，退出码0。未改已应用SQL、preview schema或实现代码，随机schema已DROP CASCADE清理。

首次执行发现runtime CASE比较缺少表达式括号导致42601，已连同另外两处同型CASE比较统一修正后重跑成功；trigger跨表NEW字段引用改为分支语句。该证据仅说明这五段DDL能在当前基线创建并提交；**未执行触发器状态反例、权限provision、来源EXPLAIN、真实worker/main或SIGKILL**，不等于A1实现或024-A验收。§5.6角色占位SQL与§6参数化查询不属于本次DDL应用输入，仍分别由A1真实受限角色/查询验证。

### 8.2 根复核、追加修订与冻结

2026-11-01，根全文复核并由独立审查员核对现有源码，接受A0范围/DTO/事务/worker合同。发现并修正两项设计反例：上限统计改为只读state<>retired的partial-index范围，增加rules.last_command_id索引；所有持久时间明确isfinite，command.created_at由DB trigger确定，ACK锁后单一DB时刻复用于episode/event，避免无限时刻污染不可变历史。

修订后再次原样抽取五段DDL，在PostgreSQL16.15随机schema的001–018基线之后事务应用及COMMIT通过，输出DDL_APPLIED_COMMITTED baseline=18与RANDOM_SCHEMA_CLEANED，退出0。此证据依然仅验证DDL应用，不是触发器反例、权限/源EXPLAIN或真实进程验收。根据此冻结A0并分配019；A1/A2/A3/A4各自实现与§8出口仍全部待执行，B–F不随本次冻结。

### 8.3 单时刻源SQL的计划修订

019实现前的PG16.15原型证明：原CROSS JOIN observed写法虽在1001合格行时有界，0合格而当前scope大量年轻行时会把时间条件放进Nested Loop Join Filter，两个source各扫描21002行（472/822 buffers）。现改为同一MATERIALIZED observed的scalar子查询作created_at上界，让scope与created_at同时进入partial index的Index Cond；0合格各扫描0行/3 buffers，合格场景仍1001。此为保持唯一观察时刻的等价SQL修正，已由根接受；没有新增来源写入或业务锁。A1须保留0/少量合格反例和默认planner实际EXPLAIN，不用强制关闭顺扫证明性能。


## 8.4 冻结合同的独立原型复核

独立审查员在专用PG随机schema/角色原型运行4个顶层race测试，6.774s通过；覆盖create→open→ACK→recover、13项错误链拒绝、时间/Unicode、六表app/projector负向权限、业务父行锁不阻塞四源MVCC读取与write_revision失败CAS。两个aging源各141002行，默认planner下scalar观察子查询在少量/0合格时只读3/0行（4/3 buffers），created_at进入Index Cond；原SQL对应21002/100000行。样本结果一致，随机schema/roles清理均0。证据在忽略目录.local/024-prototype，非生产实现、真实worker或main验收；生产代码必须重新验证本节与第8节出口。

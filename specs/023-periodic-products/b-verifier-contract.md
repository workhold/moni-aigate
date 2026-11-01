# 023-B 独立只读全量守恒核验器合同候选

2026-11-01，**根已审定并冻结路线 A：020-only 诚实三态、owner 实际 LOGIN/六表 owner、默认30s与1s–10min边界、JSON计数字符串及0/2/3/1退出码；路线 B 另行 SDD。** 授权独立 verifier 实现与新测试，允许追加 `migrations.VerifyReadOnlyTx`；CLI由根接线，不修改已应用SQL/角色。承接 [B 主合同](b-contract.md) §1.16/§10/§12、[核心接线合同](b-integration-contract.md)、[Tx 合同](b-tx-contract.md)、[Reader](b-reader-contract.md)、[Commander](b-commander-contract.md)。本稿直接核对已经追加的 [020](../../migrations/020_periodic_budgets.sql)、采购权威事实所在的 [014](../../migrations/014_procurement_accounts.sql)、客户来源所在的 [002](../../migrations/002_billing.sql) 以及 [当前 Tx helper](../../internal/periodicbudget/tx_sources.go)。冻结时未运行或新增原型测试；文中 SQL 是实现合同，不是已执行验收记录。

## 1. 目标、事实范围与不能省略的裁决

核验器从整个应用 schema 的持久来源出发，逐方向核对来源、应有 allocation、原始 effect、预算事件链、三个层次的金额投影、配置版本、operation 和审计。它独立于在线 `pb_check_customer/supplier` 和 Reader；不把两份汇总相等当作所有来源均已纳入证明。

| 权威事实 | 020 的对应预算事实 |
| --- | --- |
| `request_records` + 唯一 `reservations`，终态的唯一 `settlements` | tenant/Key 每个已配置 scope 各一份 customer allocation；钱包仍只有一份真实预占 |
| `procurement_reservations` + 对应 attempt/永久 binding + 全部 `procurement_budget_events` | 每个真实 managed attempt 一份 supplier allocation；预算 spent 依据该 allocation 的冻结 baseline 计算 |
| `periodic_budget_events` 的 control `seq` 和 allocation/window 前版本 | allocation、原窗口、control 的末值及最后事件指针 |
| `periodic_budget_configuration_versions`、`periodic_budget_operations`、固定安全 audit | 配置 revision、命令 CAS/指纹/原结果、变更与 no-op 的双向对应 |

这里的采购权威 event 来自迁移 **014**；部分历史设计将既有采购模块统称“019 effect”，核验实现必须使用真实表/列，不猜迁移序号。Verifier 不重新执行成本定价、模型 usage 归一、支付来源核验或钱包记账；它核对这些模块已经持久化且应被周期账簿引用的事实。不得按今天的 binding 把所有旧 unmanaged attempt 判为缺采购源：binding 可以在旧 attempt 之后建立。现存采购源反向必须找到自己的 attempt/binding，孤立预算引用必须报告。

**020 可实现严格的当前守恒与 new 来源完整覆盖，但缺少三类历史见证。**

1. `marker=NULL`、现在已经终结且完全没有 allocation 的旧来源：不能确定它在首配时是否未决。首配前已结束本来允许不导入，首配时未决却整份跟踪遗漏则应报错；020 没有首配 membership 清单，不能区分。
2. legacy supplier 的未镜像原采购 event：可以证明未镜像部分的净 held/spent 等于首份冻结值，却不能证明每条都发生于跟踪前。漏掉零 delta 或一对相反 delta 仍可保持所有 SUM 相等。原采购 event 没有创建时 control marker 或可用的稳定先后序列。
3. operation 动态结果：020 保存 `result_snapshot`，未保存它观察到的 control `event_seq`/窗口存在性见证。可以核对静态配置、原时刻、结构、指纹和审计；一般不能独立还原其当时的 carry/current-window 金额与窗口是否已经存在。

本稿建议先采用 **020-only、诚实三态**：已证明的损坏报 `mismatch`；未发现损坏但历史覆盖缺证报 `indeterminate`；只有完整扫描、全部异常和未证明计数均为零才报 `balanced`。这些旧终态来源不能一律算坏，也不能默认豁免后称“全历史已证明”。根若要求所有合法旧环境必须能取得严格 balanced，需要先冻结后续追加见证的合同，见 §11；不得修改已经应用的 020。

## 2. 固定 Go API 与独立 CLI

建议新增独立 `Verifier`，不在 `Reader/Commander/Tx` 上混入运维扫描，也不接入任何客户 HTTP。首版只有**平台全量**，不提供 tenant/account/filter/cursor/limit 或可任意跳过历史的开关。

```go
type Verifier struct { /* private pool and validated options */ }
type VerifyOptions struct { Timeout time.Duration }

func NewVerifier(*pgxpool.Pool, VerifyOptions) (*Verifier, error)
func (*Verifier) Verify(context.Context) (Verification, error)

type VerificationStatus string // balanced | mismatch | indeterminate
type Verification struct {
    SchemaVersion    int                `json:"schema_version"` // 1
    AsOf             UTC6               `json:"as_of"`
    Status           VerificationStatus `json:"status"`
    Balanced         bool               `json:"balanced"`
    CoverageComplete bool               `json:"coverage_complete"`
    Scanned          VerificationScan   `json:"scanned"`
    Mismatches       VerificationCounts `json:"mismatches"`
    Unproven         VerificationGaps   `json:"unproven"`
}
```

`VerificationCounts` 精确包含 §4 的 19 个 int64 字段；`VerificationGaps` 精确包含三个 int64 字段。`VerificationScan` 为 controls/windows/allocations/events/customer_sources/supplier_sources/procurement_events/configuration_versions/operations/audits 十个扫描行数；customer_sources 是 request 根行数，不因 tenant/Key 双 scope 重复；audits 只计 action=`periodic_budget.configured`。Go 使用 int64，JSON 计数字段统一 `,string`，规范非负十进制。金额中间结果全部为无 typmod NUMERIC 或 `big.Int`，绝不浮点/扫描到 Money/int64；本报告首版不输出总金额、任意行 JSON、来源/用户/采购 ID、SQL、指纹、证据正文或 DSN。

空 pool、nil context、非法 timeout 在连接前拒绝。timeout 零值默认 30s，允许 1s–10min，调用方更早的 deadline 优先；该预算覆盖 Acquire、角色/迁移核验、全部扫描与只读 COMMIT。超时、权限/迁移错误、未完成扫描或 COMMIT 未确认返回零 `Verification` 与固定安全错误，不能返回 partial counts 当成功。私有 cause 可以用于运维诊断，不反射进 CLI 默认输出；不自动重试成多个快照。

建议命令为：

```text
moni-aigate verify-periodic-budgets [--timeout 30s]
```

使用部署者明确配置的**离线 migrator/owner DATABASE_URL**，不复用 APP、projector、alert observer、支付 worker 或用户会话凭据。命令不启动 listener/业务 worker，不创建 Guard/实例 ownership，不调用外部服务。输出一份上述 JSON；已确认异常退出 2，历史覆盖不可判定退出 3，成功 balanced 退出 0，执行/输入/权限失败退出 1。mismatch 优先于 indeterminate，但报告保留全部未证明计数。需要在 `main` 的公共“非 serving 命令”分支早返回，不能先 Acquire 单实例业务锁或要求停服后才到核验分支。

首版角色规则不新增角色/授权：在实际连接中 `current_user=session_user`，该 LOGIN 是六张周期表的实际 owner，且已有所需来源 SELECT。拒 APP/projector/`SET ROLE owner`；不要求公共 `public` schema 的 `nspowner` 等于它，因为其 owner 可以是 `pg_database_owner`。不授予 APP 六表之外的新权限，不给 projector 来源读取权，不使用 SECURITY DEFINER。后续需要专用 SELECT verifier LOGIN 时另冻结 provision/有效角色闭包检查；本稿不暗中增加该角色。

最小身份 SQL 在 RR 内直接读 `pg_roles/pg_class/pg_namespace`：`current_user=session_user`、`pg_roles.rolcanlogin=true`，六张已解析关系的 `relowner=(SELECT oid FROM pg_roles WHERE rolname=session_user)`，所需每张来源/迁移表 `has_table_privilege(current_user,oid,'SELECT')`。所需来源为 request_records/reservations/settlements/upstream_attempts/procurement_accounts/credential_procurement_bindings/procurement_reservations/procurement_budget_events/tenants/api_keys/users/audit_events；不读 credentials 密文。任何缺表/列/权限为执行失败，不是数值 mismatch；表 owner 不一致也不尝试 SET ROLE。CLI 的操作者是这个数据库 LOGIN，不另接一个 HTTP actor/admin token，不把历史 operation.actor 的当前权限当作本次执行权限。

公开错误最小集合为 `ErrVerifyInput`、`ErrVerifyAuthority`、`ErrVerifySchema`、`ErrVerifyTimeout`、`ErrVerifyExecution`，支持 `errors.Is`；它们不能与正常 mismatch/indeterminate 混为 error。CLI 执行失败只向 stderr 输出固定 `periodic_budget_verification_failed:<safe_code>`，stdout 不输出貌似完整的报告；成功扫描的三态报告均只向 stdout 输出一个 JSON 对象。SchemaVersion 是 JSON number，其余所有计数为 string；bool 不转换成字符串。

## 3. 一个 RR READ ONLY 快照，零业务行锁

Verifier 显式 Acquire 一条连接，在其中建立 `pgx.RepeatableRead + pgx.ReadOnly`；没有外层 business tx 参数。第一份数据快照同事务核验实际身份、六表/所需来源的 schema/OID/列与完整嵌入迁移 name/checksum，旧前缀/未知/改 checksum 继续拒绝，不能调用 Apply/ApplyThrough。迁移只读检查应抽取/复用静态 manifest 比较，不安装历史；本稿不要求改公开迁移 API。

同事务用一次 `pg_catalog.clock_timestamp()` 取得 `as_of`，后续 SQL/解码都使用这一个值；它只是报告观察时刻。查询多个 CTE/语句仍处于同一 RR 快照，不跨页释放事务、不另从 pool 查询、不依靠每页新的 RC 快照。schema 从受信配置/连接固定后，用 `pgx.Identifier` 全限定渲染；数据值参数化。所有分组、反向查漏和 Codec 读取均在本快照完成；最后只读 COMMIT 确认后才构造可返回报告。

明确禁止：

- `FOR UPDATE/NO KEY UPDATE/SHARE`、业务 advisory 锁、`SET CONSTRAINTS`、临时表/持久表写入、调用任意预算写 helper/repair/rebuild。
- `pb_observe`、`pb_gate`、`pb_scope_sync`、`pb_check_customer/supplier`、`pb_current_tuple`、`pg_current_xact_id()` 以及读取当前 `xmin` 解释历史事务。`pb_check_supplier` 的“当前事务有 event”分支不是历史证明。
- `Reader.Get*`/`pb_view_at` 重算历史；后者会因今天时钟早于高水位拒绝读取，并使用当前余额替换旧事实。
- 按当前 enabled/limit、tenant/user/member/Key 权限、Key expiry、来源截止、当前报价或“今天是否过期”拒绝合法历史收尾。消费 refund 不减少预算 spent。

只读 SQL 自然取得的 relation AccessShare/MVCC 锁允许存在；“零业务行锁”不承诺能无视并发 DDL。锁/statement/context 等待均受总预算约束。失败先以独立最多 5s cleanup Rollback；若不能确认连接空闲，就 Hijack+有界 Close，不把活动 RR 事务放回 pool。对外没有任何自动补账、调平、清零、重发模型或重新建立窗口动作。

历史只比较原 `admitted_at` 与原窗口、稳定 `seq/revision`、持久完整 xid 的**等值**。绝不使用 xid 大小、UUID 大小、`created_at` 大小或 commit timestamp 猜先后；长事务与时钟退行使这些推断不成立。今天的 `as_of < highwater` 不能产生 mismatch/clock_regression，扫描仍应给出报告。

## 4. 固定异常计数与三态

每个 finding 以 `(code,object_kind,object_key)` 去重；相同损坏可以命中不同方向，计数不互斥。复合 key 使用真实列元组，不把不同来源拼接成有歧义字符串。19 个字段与 JSON 同名 snake_case：

| Go 字段 | 计数对象及精确方向 |
| --- | --- |
| SourceIdentityMismatches | customer request/reservation/settlement、supplier source/attempt/binding 的断链、scope/tenant/account 错绑、非空 creation marker 无对应正确 control、post-020 request 缺 reservation、creation 元数据不成组 |
| MissingAllocations | §5 已确定必须跟踪的 `(control,subject_type,subject_id)` 无唯一 allocation；终态 new 不豁免 |
| OrphanAllocations | allocation 缺 control/真实来源，或不能归属该 control 的真实 scope |
| AllocationOriginMismatches | origin/marker/window/admitted_at/upper/baseline/origin_source_id/首事件/stamp 等原始归属不符 |
| AllocationSourceMismatches | allocation 当前 held/spent/source_held/source_spent/state 不等于权威源派生末值 |
| AllocationProjectionMismatches | allocation 末值/revision/last_event 不等于预算事件完整链末值，或无首事件 |
| MissingEffects | 对已存在 allocation，§6 可确定的每个原始初占/settlement/采购 effect 无唯一同 control、同 allocation 的预算镜像 |
| OrphanEffects | budget event 缺 allocation/control 或缺该 source_kind 对应真实来源 |
| EffectBindingMismatches | 真实 effect 绑定别的 subject/account/来源种类、唯一键重复、同额错绑、映射金额/状态或首事件选择错误 |
| EventChainMismatches | event 的 control seq、allocation/window 前 revision、before/after/delta/source-spent 或允许状态转换不符；按 event 行计 |
| WindowMismatches | 原窗口日历/归属/金额/revision/last_event 与其全部 new allocation 和 event 不符，包括零金额窗口；不要求每个自然周期都建窗 |
| ControlMismatches | control 全部 allocation/event 总值、event_seq/last_event、高水位的历史窗口见证不符；含全部 legacy carry |
| ProcurementSourceMismatches | 每个真实采购源缺 canonical reserved effect、全部权威 event SUM 不等于源当前 held/spent，或账户两份独立来源汇总不符 |
| LegacyBaselineMismatches | §7 首份 legacy 冻结事实、catchup 的零 prior held/精确 before、未镜像残差与原 baseline 不符 |
| ConfigurationMismatches | control 当前 revision/配置/operation 指针不等于最后不可变 version，或原日历/初始关系错误 |
| VersionMismatches | version revision 链断档、错 control/operation/actor/配置/stamp，重复 changed 或相邻 version 无实际 enabled/limit 变化 |
| OperationMismatches | operation 资源/路由/scope/CAS/no-op、safe_input/指纹/原结果静态字段不符；不重验历史 actor 的当前权限 |
| AuditMismatches | 每 operation 缺唯一固定安全 audit、内容错或孤立 `periodic_budget.configured` audit；按异常 audit/缺 audit operation 的不同对象计 |
| SnapshotMismatches | 原结果严格字段/金额/时间/静态版本或窗口身份等 §9 可确定部分错误；不拿当前窗口金额强行比较历史 |

三个 `Unproven` 字段：

| Go 字段 | 单位 |
| --- | --- |
| LegacyMembershipUnproven | §5 中 terminal、marker NULL、无 allocation 的 `(control,subject_type,subject_id)` |
| LegacyEffectBoundaryUnproven | 至少有一份未镜像原采购 event，且020不能证明跟踪前后边界的 legacy supplier allocation；按 allocation 计，零 delta 也包括 |
| OperationSnapshotPrefixUnproven | §9 中无法从独立持久 seq/窗口存在性见证证明原动态结果的 operation |

全部 scans 成功、COMMIT 确认后：任何 mismatch>0 → status=mismatch；无 mismatch 而任一 unproven>0 → indeterminate；其余 balanced。`coverage_complete` 当且仅当三个 unproven 均0；`balanced` 当且仅当 status=balanced。空且未配置的 schema 可以 balanced；不为了制造“完全”而隐去 legacy/source/operation。

## 5. 从来源建立应有 scope，不能从 allocation 反推扫描集合

以下为平台全量的关系定义。实现可共用 MATERIALIZED CTE/包内安全 query，但不能改变 join 方向、反向扫描或真值条件。正常源集合保留所有状态；identity 异常另用 LEFT JOIN 扫描，不能让 inner join 丢掉坏源。

```sql
WITH customer_scopes AS (
 SELECT c.id AS control_id,c.scope,r.id AS subject_id,v.id AS reservation_id,
        r.tenant_id,r.key_id,v.amount::numeric AS upper_amount,v.status,
        CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control
                     WHEN 'key' THEN r.pb_key_control END AS marker
 FROM request_records r
 JOIN reservations v ON v.request_id=r.id AND v.tenant_id=r.tenant_id
 JOIN periodic_budget_controls c ON
   (c.scope='tenant' AND c.tenant_id=r.tenant_id) OR
   (c.scope='key' AND c.tenant_id=r.tenant_id AND c.key_id=r.key_id)
), supplier_scopes AS (
 SELECT c.id AS control_id,p.attempt_id AS subject_id,p.pb_control AS marker,
        p.state,p.account_id
 FROM procurement_reservations p
 JOIN periodic_budget_controls c ON c.scope='procurement' AND c.account_id=p.account_id
), missing AS (
 SELECT s.control_id,'customer_request'::text AS subject_type,s.subject_id
 FROM customer_scopes s
 WHERE (s.marker=s.control_id OR s.status IN ('reserved','pending_reconciliation'))
   AND NOT EXISTS(SELECT 1 FROM periodic_budget_allocations a
     WHERE (a.control_id,a.subject_type,a.subject_id)=
           (s.control_id,'customer_request',s.subject_id))
 UNION ALL
 SELECT s.control_id,'supplier_attempt',s.subject_id
 FROM supplier_scopes s
 WHERE (s.marker=s.control_id OR s.state IN ('prepared','sending','unknown','disputed'))
   AND NOT EXISTS(SELECT 1 FROM periodic_budget_allocations a
     WHERE (a.control_id,a.subject_type,a.subject_id)=
           (s.control_id,'supplier_attempt',s.subject_id))
)
SELECT count(*) FROM missing;
```

另做以 `request_records`/`procurement_reservations` 为根的非空 marker LEFT JOIN：marker 必须指向正确 scope/tenant/key/account 的 control，且等于该业务目标当前唯一 control；没有 control 或指向其他目标为 SourceIdentityMismatches。**非空错 marker 不得落入 legacy 分支。** post-020 request 的 `pb_created_xid/pb_admitted_at` 应同时非空、有限，marker 可因当时未配置而为 NULL；迁移前 request 的 stamp/At/两个 marker 保留全 NULL。post-020 procurement source 的 stamp/At 同理，非空 source stamp 必须等于对应 attempt stamp；旧 source 与旧 attempt 的 stamp 都保留迁移前 NULL，不回填。只比对应创建 stamp 的等值，不要求后续 PE 的 stamp 与 source 创建 stamp 相同。

customer 的 request 缺 reservation 且有 `pb_created_xid` 或任一 marker，必报 SourceIdentityMismatches；任何被 allocation 引用的旧 source 缺 reservation 同样报错。旧未跟踪且没有 reservation 的 request 不凭空制造 R，其来源存在性属于原账务核验边界，统计说明但不假造 allocation。

缺 allocation 时，**只有** `marker IS NULL AND source 已终结` 进入 LegacyMembershipUnproven：customer status=settled/released；supplier state=settled/released。已配置 scope 的旧未决源即使 held=0 也必须 allocation，不能用金额0豁免。未配置业务目标没有 control 和非空 marker 时，没有周期义务；其历史消费不被补造到新期。

反方向从每个 allocation LEFT JOIN control/source，证明 customer 的 request/tenant/Key 与 source reservation 对应；supplier 的 source→attempt→request/tenant 与永久 binding 的 credential/channel/account 对应。不读取 credential 密文，也不以当前 owner、账号状态、用户 role 决定历史关系。

## 6. 原始 effect 与预算事件的逐笔证明

### 6.1 首份归属与原 source

对每个 allocation 按 `periodic_budget_events.seq` 取唯一首事件；不是按 event UUID/时间取首。不存在首事件、首 `allocation_before_revision != 0` 或重复首，分别进入缺 effect/链/投影计数。

| allocation | 必须的首预算 event 与不可变关系 |
| --- | --- |
| customer/new | marker=c.id；window/At=源的 pb_admitted_at；origin_source_id=reservation.id；source_kind=reservation/source_id=该 reservation；before=0/0，after=R/0/source_spent0/held；baseline0 |
| supplier/new | pb_control=c.id；At=采购源 pb_admitted_at；origin_source_id=精确 canonical reserved PE.id；首 source_kind=procurement_event；PE.type=reserved、event_key=reserve:attempt、delta=U/0；after=U/0/held；baseline0 |
| customer/legacy_import | scope marker NULL；origin_source_id=subject；window/At NULL；首 legacy_import/source_id=subject；before0/0，after=R/0/held；baseline0 |
| supplier/legacy_import | marker NULL；origin_source_id=attempt；window/At NULL；首 legacy_import/source_id=attempt；after_source_spent=baseline、after_spent0，after_held≥0，after_state=held/disputed |
| supplier/legacy_catchup | marker NULL；window/At NULL；origin_source_id=首 procurement_event 的真实 PE.id；before 预算0/0，原 before held=0、spent=baseline；首 after 与该 PE 的精确 delta 闭合 |

所有 new allocation 原 At 必须位于 `window.start_at <= At < end_at`，window.control 精确相同，upper=源冻结R/U。对已收尾来源仍用原 At，不要求源现在还是 prepared/held，不要求旧窗等于今天，也不因 disabled 删除记录。

持久 creation stamp 只检查合同支持的等值：customer/new 的 request stamp=allocation stamp=首预算 event stamp；supplier/new 另等于 procurement source/attempt 与 origin PE 的 stamp；legacy_import 的 allocation/首 event/control.created_xid 相等；legacy_catchup 的 allocation/首 budget event/origin PE stamp 相等。不能要求 `xmin` 的低32位永久相等或运行历史 `pb_current_tuple`。source 在后续事务更新不改变这些 stamp。

### 6.2 确定的 MissingEffects / OrphanEffects

对**存在且归属已确定的 allocation**生成 expected effect 关系 `(control,allocation,source_kind,source_id)`，与预算 event 做双向 anti-join：

- 每个 customer 一份 §6.1 首初占；有唯一 settlement 时还必须有一份精确 settlement effect。没有 settlement 的 pending customer 只有初占。退款不生成反向周期 effect。终态不能用唯一 settlement 当首事件，也不能只验证净 charge。
- 每个 supplier/new：其 attempt/account 的**全部**原 `procurement_budget_events` 都必须一对一镜像，含0 delta。源创建时 control 已存在，完整生命周期无“跟踪前”例外。
- supplier/legacy_import：必须首 synthetic import；supplier/legacy_catchup：必须首 origin PE；其他已经引用的 PE 逐笔核验。未镜像 PE 按 §7 处理，不把全体旧 PE 硬认成缺 effect，也不悄悄忽略。
- 任意 budget event 反向必须找到同 allocation、同真实 subject/source、同 control/账户的原 effect。`legacy_import` 只能配 origin=legacy_import 和 source_id=subject；reservation 只能 customer/new 初占；settlement 只能 customer 后续；procurement_event 只能 supplier。

原始 source_id 相同但错 tenant/Key/account/subject 的同额 event 仍错。对 `(control,source_kind,source_id)` 及每个 subject 的初/末 effect 检查唯一性，不借现有 UNIQUE 假定历史一定完整。没有 allocation 的 required source 已由 MissingAllocations 报告，不需要虚构 allocation UUID 才能查漏。

### 6.3 稳定序列重放

下面关系一次构建，不逐条调用在线触发器：

```sql
WITH ordered AS (
 SELECT e.*,a.window_id,a.baseline,a.subject_type,a.origin,
   row_number() OVER(PARTITION BY e.control_id ORDER BY e.seq) AS control_n,
   row_number() OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq) AS allocation_n,
   lag(e.after_held,1,0::numeric) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq) AS prior_held,
   lag(e.after_spent,1,0::numeric) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq) AS prior_spent,
   lag(e.after_source_spent,1,a.baseline) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq) AS prior_source_spent,
   lag(e.after_state) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq) AS prior_state
 FROM periodic_budget_events e
 JOIN periodic_budget_allocations a ON a.id=e.allocation_id AND a.control_id=e.control_id
)
SELECT * FROM ordered
WHERE seq<>control_n OR allocation_before_revision<>allocation_n-1
   OR (before_held,before_spent) IS DISTINCT FROM (prior_held,prior_spent)
   OR (held_delta,spent_delta) IS DISTINCT FROM
      (after_held-before_held,after_spent-before_spent)
   OR after_held<0 OR after_source_spent<0
   OR after_spent IS DISTINCT FROM greatest(after_source_spent-baseline,0);
```

同一 ordered 基础上，supplier 的每个 PE 另要求 `after_held=prior_held+PE.held_delta`、`after_source_spent=prior_source_spent+PE.spent_delta`。legacy import 的首步跳到导入冻结值，是唯一没有原 PE delta 的 supplier 初始步。customer 首步严格R，第二步严格唯一 settlement 的 charged_amount，released 必须0；不接受第三步/再次恢复held。

supplier 的预算 state 映射权威事件：reserved/consumption_unknown/recovered_unknown→held；released_no_consumption/expired_without_permit→released；cost_confirmed/cost_corrected→settled；evidence_disputed→disputed。canonical reserve key 精确匹配；其他 effect 保持真实 unique event_key/attempt/account，不解析证据正文造新来源。未知生产 type 进入 EffectBindingMismatches，新增类型必须同步规格。允许原采购合法的负 delta/绝对成本超过U；不按U截断已知spent。原 state 映射允许 held 自环、held→终态/disputed、终态→disputed、合法绝对更正→settled；不把单纯 prepared→sending 等未产生原 PE 的状态变化当缺 event。

窗口事件只取 `a.window_id IS NOT NULL` 的 ordered 行，按 `(control,window)` 的 seq 再编号，检查 `window_before_revision=n-1`；legacy event 的 window_before_revision 必须NULL。所有 e 的金额/state/seq 类型/范围独立检查，不让 orphan join 漏掉行。

## 7. legacy baseline / catchup 的可证明部分与边界

### 7.1 首值和当前值

customer：baseline=0，首 import 的held=R，当前 pending→R/0/held，终结→0/唯一charge/settled或released。旧 terminal 无 allocation 不自动纳入本检查，沿 §5 不可判定分类。

supplier：baseline永久不变，当前 `source_held=p.held`、`source_spent=p.spent`、预算held=p.held、预算spent=`greatest(p.spent-baseline,0)`，state映射真实 source。baseline是原采购spent的冻结下界，不是“累计采购成本应扣掉的所有过去费用”；成本降到baseline以下时预算spent可0，日后增加仍对同一baseline计算。

catchup 首 PE 的 before 可以由首预算 event和原 PE **代数重建**：

```text
prior_h = first.after_held - originPE.held_delta = 0
prior_s = first.after_source_spent - originPE.spent_delta = allocation.baseline >= 0
first.after_spent = max(first.after_source_spent - baseline,0)
```

不能在历史核验时照抄在线 catchup 的 `SUM(all PE except originPE)`：后续已有更多 effect，该 SUM 已不是当时 before。也不能把当前源spent充作baseline。

### 7.2 未镜像权威事件残差，必须逐 allocation 校验

对每个 supplier allocation 构造 `mirrored_PE` 为其所有 source_kind=procurement_event 真实引用；先排重复/错绑，再取该 attempt/account 原 PE 的 anti-join 差集。计算无 typmod NUMERIC 的 `unmirrored_held/spent`：

```sql
SELECT a.id,
       coalesce(sum(p.held_delta),0) AS unmirrored_held,
       coalesce(sum(p.spent_delta),0) AS unmirrored_spent,
       count(p.id) AS unmirrored_count
FROM periodic_budget_allocations a
JOIN periodic_budget_controls c ON c.id=a.control_id AND c.scope='procurement'
LEFT JOIN procurement_budget_events p ON p.attempt_id=a.subject_id
 AND p.account_id=c.account_id AND NOT EXISTS (
   SELECT 1 FROM periodic_budget_events b
   WHERE b.control_id=a.control_id AND b.allocation_id=a.id
     AND b.source_kind='procurement_event' AND b.source_id=p.id)
WHERE a.subject_type='supplier_attempt'
GROUP BY a.id;
```

- new：差集必须为空，不能仅检查两个 SUM=0。
- legacy_import：差集金额必须恰等于首 import 的 after_held 与 allocation.baseline。
- legacy_catchup：差集金额必须恰为 held0/spent=baseline。

任一金额不符为 LegacyBaselineMismatches；差集非空的 legacy allocation 还记 LegacyEffectBoundaryUnproven，即使金额恰好相等。理由是无法证明这些 event 全部在跟踪前；两个相反delta/零delta的遗漏不能靠残差检出。差集为空且首冻结值均0可以证明此项没有未镜像原 effect；不据此消除其他 membership/operation 缺口。

独立按**每个 procurement source**聚合全部原 PE，验证 SUM held/spent=source当前held/spent，并有唯一 `type=reserved,event_key=reserve:attempt,delta=upper/0`。再从 account 出发比较 account金额、其全部源金额SUM、全部原PE delta SUM。按 attempt 的检查不能被 account的跨源相抵替代；窗口/control 的投影SUM也不能代替这份原采购事实守恒。预算 verifier不据这项重新发送来源或重新定价。

## 8. allocation / 原窗口 / control 三层独立重建

每个 allocation 的 event链尾 `(after_held,after_spent,after_source_spent,after_state)` 应恰等于其 `(held_amount,spent_amount,source_spent,state)`，`source_held=held_amount`；revision=该allocation的事件数，last_event_id=最大seq真实事件ID。无事件allocation的revision0/NULL指针不是可提交完整source。

窗口必须日历对齐、有限且 end 为下一UTC日/月，control/原At完全对应。每个window的held/spent同时等于：绑定该window的全部allocation末值SUM；关联event held/spent delta SUM。revision=其事件数，last_event_id=其中最后control seq的真实event；没有任何event的窗口允许0/0/revision0/last=NULL，因为 `pb_observe` 可为派发extra0建空窗。没有来源的自然日/月不强制建窗，也不补窗。

control 的held/spent同时等于：**全部**allocation末值SUM（含全部旧窗与legacy）；其全部event delta SUM。event_seq=事件总数=最大seq（空时0），last_event_id精确尾event（空时NULL）。source identity检查与逐笔链检查仍独立进行，SUM相等不能消除它们的异常。

高水位只对历史事实核验：日历对齐/有限；等于 `greatest(UTC tracking首窗, 已存在全部window.start_at最大值)`。这是当前 `pb_observe` 同事务更新高水位并创建原窗的支持行为；不允许无任何相应历史窗口却任意抬高水位。**不得**要求高水位≤今天，或原窗口≥当前高水位；合法旧窗有利收尾仍留原窗。

SQL聚合先按allocation/window/control各自GROUP BY，再LEFT JOIN比较，避免把事件与多allocation直接多对多连接后重复SUM。所有`sum`对输入先转无typmodNUMERIC，空集合用精确0，绝不把孤立关系LEFT JOIN NULL当0吞掉；缺关系先记录finding。

## 9. 配置版本、operation 和安全审计

### 9.1 不可变配置链

每个control必须有从1到当前revision连续唯一的版本，按revision而非时间排序。所有version的period_kind等于control原日历；version1.created_xid=control.created_xid。相邻version的enabled/limit必须真实改变；no-op不增加version。最新version的operation/config/revision与control配置及`configuration_operation_id`完全相等。

每version反向必须有唯一operation：resource_id=control.id、revision/actor/配置一致、created_xid相等、created_at相等。对应operation.safe_input.expected_revision=version.revision-1；revision1必须expected0。每operation通过是否存在`version.operation_id=operation_id`判断changed，不能拿它是否等于**今天**的control.configuration_operation_id判断历史changed。

没有对应version的no-op必须safe_input.expected_revision=operation.revision，且其safe_input的period/enabled/limit等于该原revision的version；不产生新version。其他operation必须对应一份真实changed version，不容许“同配置但声明changed”或跳revision。

可实现SQL骨架：

```sql
WITH versions AS (
 SELECT v.*,row_number() OVER(PARTITION BY control_id ORDER BY revision) AS n,
   lag(enabled) OVER(PARTITION BY control_id ORDER BY revision) AS prev_enabled,
   lag(limit_amount) OVER(PARTITION BY control_id ORDER BY revision) AS prev_limit
 FROM periodic_budget_configuration_versions v
)
SELECT v.control_id,v.revision
FROM versions v
LEFT JOIN periodic_budget_controls c ON c.id=v.control_id
LEFT JOIN periodic_budget_operations o ON o.operation_id=v.operation_id
WHERE c.id IS NULL OR o.operation_id IS NULL OR v.revision<>v.n
   OR v.revision>c.revision OR v.period_kind IS DISTINCT FROM c.period_kind
   OR (v.revision>1 AND (v.enabled,v.limit_amount)
                      IS NOT DISTINCT FROM (v.prev_enabled,v.prev_limit))
   OR (o.resource_id,o.revision,o.actor,o.created_xid,o.created_at)
      IS DISTINCT FROM (v.control_id,v.revision,v.actor,v.created_xid,v.created_at);
```

还须反向检查control缺全链/缺尾与operation缺对应原revision，不因这个inner/left join只覆盖已有version就跳过。

### 9.2 operation 与 audit

复用包内严格**纯解码**和 `Fingerprint` 规则，拒未知/缺字段、null、非规范revision/Money/UTC6；不能调用会当前授权、锁行或当前`pb_view_at`的Commander方法。safe_input只允许四字段。先验证类型/规范，再做数字运算；畸形JSON/金额计Operation/SnapshotMismatches，不让未经验证的`::bigint`把整份核验中途炸成SQL错误。

canonical kind/scope/target 由真实control派生：tenant/procurement为platform与其固定平台路径；Key为该control真实tenant及固定Key路径。actor只核真实引用和audit一致，不要求其现在仍active/admin/owner或Key未撤销。指纹按已冻结frame/字段/sha256重新计算，operation.revision与§9.1匹配。

每operation必须有唯一audit：action=`periodic_budget.configured`、target=operation UUID、actor相同、tenant_scope只在Key为真实tenant否则NULL、request_id为空、created_at=operation.created_at，sanitized_diff恰为`{control_id,revision字符串,scope,scope_id,changed布尔}`五字段并逐值相同。从所有该action的audit反向检查孤立/错绑/重复，不因缺operation无法JOIN而遗漏。

### 9.3 原 result_snapshot，静态核验与动态缺证分开

严格解析原 **12** 字段和嵌套window五字段（以 `readDecodeView` 的真实键集为准），保留原值，不用当前GET改写。配置scope/id、revision、period/enabled/limit必须与operation和原revision version一致；tracking/effective来自control不可变字段；`as_of`恰为operation.created_at的UTC6；state只用原enabled和原as_of/effective推导。非空window必须指向同control真实原窗，其id/start/end一致，且该窗覆盖原as_of所属UTC期；原金额/carry均规范非负。原as_of所属期不得早于control的 tracking 首期，这是高水位初值可证明的下界；不能要求它不早于今天已推进的高水位。

不能把原window.spent/held与今天window余额比较；null window也不能因今天后来建窗而判坏。020没有window.created_xid/created_at，也没有operation观察的control.event_seq，**仅对确实仍有未证明的动态字段**记 OperationSnapshotPrefixUnproven。保守充分豁免为：当前数据库无该原as_of期的window，原snapshot.window=NULL，且该control每条预算event的held_delta都为0。窗口不可删除，因此原时刻该窗也不存在；所有held前缀恒0，因此原carry必为0。必须验证原carry0，否则SnapshotMismatches。这覆盖空库、仅未配置、配置但未发生event的首配/no-op，也覆盖合法零额度事件；不因为“缺少seq列”一律不可判定。若当前有对应原窗，金额即使恒0，其创建时序仍无独立见证；真实静态关系检查通过后保留存在性缺证。若有非零held_delta，则即使当前净held0，也不能据此证明原carry0。

豁免 SQL 只决定哪些字段可证明，不调用任何预算函数：

```sql
SELECT o.operation_id
FROM periodic_budget_operations o
JOIN periodic_budget_controls c ON c.id=o.resource_id
WHERE NOT EXISTS (
  SELECT 1 FROM periodic_budget_windows w
  WHERE w.control_id=c.id AND w.start_at=
    CASE c.period_kind WHEN 'utc_day' THEN
      (date_trunc('day',o.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
    WHEN 'utc_month' THEN
      (date_trunc('month',o.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') END)
  AND NOT EXISTS (SELECT 1 FROM periodic_budget_events e
                  WHERE e.control_id=c.id AND e.held_delta<>0);
```

以上候选 operation 还必须通过全部严格解码/静态核验；仅当 `current_window=NULL,carry_held="0"` 才豁免。其余不能从“恰好存在某个同金额前缀”推成“这就是当时前缀”。静态已错误的snapshot计SnapshotMismatches，不再附加同一字段的缺证计数。

**历史cap/派发授权重新认证不属于本首版的守恒结论。** 不能用今天limit判旧R/U或收尾spent超cap非法；当前version历史也缺与每次准入绑定的revision见证。运行时硬门禁/当前角色和模型恢复不由此工具替代。如果未来要求证明每次历史准入恰受当时版本约束，需要另加持久版本/seq见证和精确回放合同，不能按wall clock排序猜测。

## 10. 扫描与失败实现约束

实现按关系集合分阶段：身份/应有scope → allocation原始关系 → effect双向关系 → control/ allocation/window序列链 → 三层投影/采购原事实 → legacy边界 → version/operation/audit/原snapshot。阶段依赖顺序固定；独立SQL可在同一RR连接顺序执行，不并发共享pgx.Tx，不用另连接加速而破坏快照。解码operation可流式逐行，内存只保留必要版本/计数；不能为每个allocation进行N+1全表扫描。

现有PK/unique、`pb_allocation_subject`、`pb_procurement_source_events(attempt_id,id)`和control/seq、control/revision索引可服务join/group/order；全量审计允许一次有界顺序扫描。需要新增索引时先提交具体EXPLAIN证据与追加迁移裁决，本稿不预留/抢占迁移号。禁止limit1000/分页跳过尾部来制造成功，首配1000上限不是核验全局行数上限。超时/资源不足只报执行失败；错误不修改来源、projection、checkpoint或历史coverage。

报告的balanced只针对快照内持久事实与明确coverage；不是生产恢复、旧备份完整性、供应商成本认证或任意owner恶意删除全部证据的证明。核验器没有provider/SMTP/PSP client。不能因某次报告不完整而打开恢复/重放开关。

## 11. 待根裁决：如何取得完整历史覆盖

**建议A（本片可直接落地）：** 冻结上述020-only verifier与三态，先实现已具备证据的19方向；legacy/动态回执缺证保持显式计数。它能发现遗漏new allocation、原初占缺失、当前未决legacy遗漏、错绑、局部链/投影/版本/audit和非守恒baseline，不虚报完整旧历史认证。

**路线B（完整历史证明的前置设计，尚未授权）：** 另行追加专门见证，不修改020、不猜历史、不用当前时间回填：

1. 每次首次control创建，在原scope独占屏障下持久化首配membership及被排除终态来源的可审计边界。只存“导入了哪些”不足以证明整份导入关系从未遗漏；可选永久单调source/effect序号与首配cut，或明确源状态检查点，需冻结大小、1000/维护导入和原锁序。
2. 每个原采购effect持久化**effect创建时**的control-at-effect marker和在原account/attempt串行域分配的单调effect序号/精确before-after。这样null-marker旧源在首配后的0delta/相反delta也能逐条判定应mirror，catchup baseline可对精确cut重建。仅新增一个普通sequence且在业务锁前nextval不能证明提交/效果顺序；必须绑定已有源屏障、无复用、回滚gap规则和不可变写guard。
3. 为operation追加私有、不可变的观察见证：control event_seq cut、原window存在性/ID及其revision。保持现有客户receipt线协议不变，DB内部存储可以追加；同tx任意后续事件不能改这个cut。历史动态快照按精确prefix重新聚合核对，空窗存在也要有创建见证。
4. 对现存020历史另建显式“证明始于该维护快照”的coverage记录，而不是把所有旧null标记更新成看似新见证。新边界不能自动消除既有Unproven；若根选择人工双人认定历史基线，必须另定义证据、权限、审计、停止/排空和不可变原件，不借核验器自动修复。

路线B的锁/FK、角色、升级和接口需要单独完整SDD；021当前由支付沙箱预留，本文件不分配后续编号。**根本次已接受A的indeterminate作为真实能力边界，接受owner-only CLI与三态exit码；B保留为另行SDD的完整历史认证前置。** 设计已冻结不等于实现验收完成，实际测试记录由实施后单独提交。

## 12. 实施与统一架构边界

冻结时本片仅新增本合同，没有改020、共享HLD/LLD/主合同、生产/测试或CLI，没有增加原型验证。根已授权本代理独占 `internal/periodicbudget/verifier*.go` 及对应新tests，以及只读的 `migrations.VerifyReadOnlyTx(ctx,pgx.Tx)` 与新test；后者只核验完整当前嵌入清单，不BEGIN/COMMIT/DDL，不容忍旧前缀，不改变旧迁移API行为。CLI、operations、旧SQL和共享架构由根汇总接线；本代理不启动main/browser或refresh。受限执行/坏源反向/旧clock与合法旧terminal边界必须以实际数据库证据验收，尚未运行一律待验。

实际实现时统一架构必须增加独立“offline RR readonly verifier”的依赖/角色/状态与coverage限制；不能画成在线请求必经模块、业务锁持有者或repair worker。由根汇总其他并行改动后统一更新架构正文/changes/manifest并执行显式reviewed refresh；本候选自身也纳入该次设计同步。

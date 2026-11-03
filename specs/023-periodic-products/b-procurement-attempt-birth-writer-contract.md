# B-P4：完整 attempt 出生写入根实施合同（根审定冻结）

2026-11-04；审查基线 `6c03c15`，集成人已审定冻结。承接[统一来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)、[物理布局 §6](b-procurement-schema-contract.md#6-校准完整记录attempt的公开db根)、[执行](b-procurement-execution-contract.md)、[完整 ACL](b-procurement-full-acl-contract.md)及[账户命令候选](b-procurement-account-writer-contract.md)。本页把下一真实受控根的实施依赖、原件出生许可和 `admitted_at` 时序写清楚；不是 SQL 材料、已安装能力或 PG 验收记录。§5 的新末序 BEFORE hook、原 attempt 先于 source 的显式许可及 replay 的 NULL 身份已冻结；先完成真实 request 自动出生、新 command+reserve guard 与 exact-link，再实施完整根，不以合同冻结宣称能力已生效。

本轮新增此合同并同步四份上位合同的相关条目。原 Go、001–022 迁移、已有候选材料、角色、在线入口与数据库均不变；D1/I1、十一项历史命题、新 profile、B-P3 和 R2 状态不因此推进。统一架构由集成人汇总后复核、更新和 refresh。

## 1. 真实基线与交付范围

原 [RecordAttempt](../../internal/billing/evidence.go#L72) 在 Go 校验后依次完成 LR、`readAttemptChecks`、`checkAttemptCostQuote`、原 attempt INSERT/序号冲突读回；只有真正 INSERT 才调用 [recordProcurementAttempt](../../internal/billing/procurement.go#L51)。原采购路径已实现共享账户 N 锁、supplier PB 锁、等待后重价/期限检查、硬上界、账户 gate、新 p、PB admission、准入时点资格、reserved PE、账户 H 和 supplier PB 镜像。它没有本页完整 DB 根、AttemptOrigin 或采购 journal 来源链。

[关系候选](../../internal/procurementprefixschema/materials.go)提供列、CHECK、PK/UK/FK，不提供完整 writer、guard 或权限。已有 [account writer v1](../../internal/procurementaccountwriter/materials/account-writer-v1.sql)只接 create/configure：`pp_ac_guard_v1` 对共享 root/step/journal 只接受命令；`pp_ac_account_write_v1` 只允许命令效果；`pp_ac_validate_account_v1` 遇实际 p/PE 或非 command journal 仍拒绝 PP906。**不能把新 `pp_record_attempt` 函数直接叠上去便声称 managed 可写，也不能删除这些检查以通行。** 需要显式新版隔离装配扩充 command+reserve 的准确接受集，旧 v1 文件/摘要与既有验收历史保持。

本根完成一次新的 prepared attempt 及其明确 managed/unmanaged 出生。它不派发模型、不制造 sending permit、不决定客户结算、不发采购 audit/outbox、不修改成本终态；后续 dispatch/finalization/审批/恢复仍须各自受控根。支持受证的 command+reserve 前缀不等于已支持所有非 command journal；遇尚未实现的合法历史仍 PP906，声称受证却缺边/错边则 PP902。

## 2. 完整公开 ABI 与 Go 接缝

唯一公开根为：

```text
pp_record_attempt(tenant text, request text, sequence integer,
                  channel text, channel_version text, credential text, upstream_model text,
                  route_decisions jsonb, has_expected_quote boolean,
                  expected_version bytea, expected_input_rate bigint,
                  expected_output_rate bigint, expected_basis bytea)
  -> TABLE(outcome text, root_id text, source_id text, attempt_id text,
           request_id text, sequence integer, delivery_state text, cost_status text)
```

不接 caller attempt/root/source ID、时刻、账户、U、before、计数或 passed。返回恰一行；`outcome=applied|replay`。applied 的 root/source 非 NULL，source_id=真实 attempt_id；root 已完成最后 seal，但仍须外层 deferred/COMMIT 成功。replay 没有本次来源：root_id/source_id 均 SQL NULL，返回原 Attempt 五字段；不借返回值给旧 attempt 补证或发行能力。

[AttemptInput/Attempt](../../internal/billing/types.go#L101) 的公开字段和 Go 方法不改。普通旧构造器仍沿原 SQL；候选只由显式 prospective executor 选择，不能依函数存在自动启用，也不能在候选错误后回退旧 INSERT。adapter 保留 Go 事务外 validator，并把 CostQuote 原字符串转原始 `[]byte`，不先转 PG TEXT。SQL 根独立重复输入与领域检查；外层将固定 SQLSTATE 映射回原业务错误并保留 PgError cause，扫描异常/行数/NULL 联合错误为 PP903，先回滚。

### 2.1 可忽略的报价字节与路由 JSON

- `has_expected_quote=false`：四个 expected 参数全 NULL；true：VersionID/Basis 两个 BYTEA 非 NULL、允许空 bytes，两维 rate 各自可 NULL。不得普遍拒绝 expected 的负 rate、NUL、非法 UTF-8 或任意 bytes：原未消费分支确实忽略这些值；实际消费时才通过精确 Equal 判不匹配。
- 原 [EqualCostQuote](../../internal/routing/cost.go#L46) 是 VersionID/Basis 字符串字节及 nullable rates 的逐值比较，不是 Known 校验、空值折零或大小写归一化。SQL 仅在原采购 binding 或 `policy.Managed(maxIn,maxOut)` 分支，将实际 DB quote 的 UTF-8 bytes 与 expected BYTEA 比较；未消费 expected 不存、不转 TEXT、不进入 source 身份。
- 原 [route validator](../../internal/billing/route_decisions.go#L7) 的空输入统一 `[]`；非空至多 1000 项，且只有准确五字段 `channel_id/version_id/priority/weight/reason`。priority 为部署 64 位 Go int 完整有符号域；weight 为 1..18446744073709551615，不能 cast BIGINT 截掉上半区。channel 唯一，两个 ID 沿原 `uuid.Parse` 接受集，selected 恰一个且与本 attempt channel/version 精确相同。
- reason 仅为原 validator 中 20 个固定值，逐项保留；DB 不接受额外键、缺键、NULL/错类型。按 [routing.Decision](../../internal/routing/routing.go#L55) 的字段顺序及原 Go JSON 字符串转义重新计算 UTF-8 字节，业务界为 262144，不能用 JSONB::text 长度或 caller 长度替代。JSONB 传输界仍按[读集合同](b-procurement-replay-read-contract.md)单独处理。纯叶子通过不替代 root 的原业务检查。
- Sequence=1..2；UpstreamModel 1..200 **字节**沿 `boundedString`，不新增 TrimSpace/无控制符/canonical UUID 规则。其他原 billing 身份也不能套用 account 命令更严格的 canonicalID。PG TEXT 本来不可传输的字符仍按原 driver/数据库错误处理，不把 ignored expected 的 BYTEA 规则扩散到真实业务 TEXT。

### 2.2 原幂等必须在全部原 checks 之后

根不能先按 `(request,sequence)` 找到旧 attempt 就快返。完整执行 §3 后才 `INSERT ... ON CONFLICT(request_id,sequence) DO NOTHING`。冲突等待结束后，用独立新 statement 按 tenant/request/sequence 读回；仅比较 channel、channel_version、credential、upstream_model，错则原 idempotency_conflict。route_decisions、报价、今天 p 状态或新的来源行不进入原幂等身份。

匹配时返回当时原五字段，可能已 completed/failed/unknown；不要求重新 prepared。无本次 origin/head/p/PE/journal/PB/root，不登记 legacy，不因成功重放声称认证旧历史。新动作所需受证 request/account 检查只在真正 INSERT 分支使用；完整候选仍不得让已声明 tracked 却错绑的原件被当作可写来源。

## 3. 原业务状态机、报价与错误顺序

| 阶段 | 固定行为与失败边界 |
| --- | --- |
| 输入 | 先原 Sequence/UpstreamModel，再 typed route validator；SQL NULL 必填/expected 联合独立拒绝，不用 STRICT 静默返回零行 |
| LR(channel) | 普通定位 request；tenant S→owner membership S→key U→model S→channel S→customer reservation U；新 statement 重读 request.status/latest。原 helper 不显式锁 request，不在此补用户/tenant/model 活性拒绝，不虚构 credential S |
| readAttemptChecks | 各自有序新 statement：Key CIDR/scope/region/revoked/expiry+DB clock→binding isolation→published membership（RouteRevision>0）→冻结 credential membership→request deadline clock→Sequence2 可信首尝试无消费证据→实际 current quote |
| 原判错次序 | IP→Key 活性→模型 scope→binding isolation→reservation reserved→published→credential membership/region→request deadline→retry evidence→quote 读取；原 scan/drain/SQL 错误不吞掉 |
| checkAttemptCostQuote | 先 credential 是否有采购 binding；有则完整 known dimensions/basis 且 expected 相等；RouteRevision=0 到此返回。否则读取冻结 policy/max rates，仅 policy managed 或采购时消费 expected，然后 CostExclusion；manual/unmanaged 不新增 expected 比较 |
| attempt INSERT | 冻结真实身份、quote、route_decisions、DB 选价时点和本次出生指针；原初态 prepared/unknown/NULL，其他 last/dispatch pointers NULL；冲突按 §2.2 快返 |
| 新动作 | 私有 closure 确认是真正受控本事务新 INSERT；明确 unmanaged 或进入 managed account/PB/head 准入，完整来源/效果闭合，最后 seal |

首次选价仍在 [currentAttemptCostSQL](../../internal/billing/cost_policy.go#L13) 的位置，由一条 statement 内 MATERIALIZED `clock_timestamp()` 采 `quote_selected_at`，按 `effective_at DESC,created_at DESC,id DESC` 选择已生效 quote。实施时改为 instant 左连接单一候选，使**无 quote 也返回该真实采时**；noquote 费用维度为 NULL、basis=unknown。该时点同时写 upstream.pp_quote_selected_at 和 AttemptOrigin.quote_selected_at，不从今天目录反推出历史完整 absence。

unmanaged closure 不再调用 current quote。例：首次选价 t0 取 Q1，后续来源处理跨过 Q2 的定时生效时刻，仍接受并冻结 Q1；不能因补来源而新增 Q1≠Q2 拒绝。policy managed 而没有采购 binding 同样保留原首次检查，不凭空加入采购 reservation 阶段。

managed 在 account/PB/head 等待后重新选 current；与原已冻结实际 quote 不等即原“before reservation”失败。expected 已在 channel 屏障后的采购分支与首次 DB quote 精确相等，因此私有三参 helper 只需 current=frozen，传递性保留原第二次 expected 比较；不需要把 expected 存入 source、GUC 或扩展 helper ABI。随后新 statement 读 request 的 input_upper_bound/output_limit 和 request deadline，计算 `U=ceil((input_bound*input_rate+output_bound*output_rate)/1000000)`，NUMERIC 精确计算且 U≤MaxInt64；不以售价或 caller amount 替代。

账户 gate 检查真实 locked A/head/阻断完整集合，active、C=0、H+S+U≤limit；失败全部回滚。新 p 的原 `pb_admitted_at` 是准入时点：supplier gate 使用该时点和 U；之后新 statement 读 Key expiry，以同 admission time 按 Key expiry→customer financial deadline/request deadline 的原顺序复核，再沿原 `checkDispatchCostQuote(at)` 检查当时有效采购 quote/policy。原 0 金额同样 gate；这不是之后可跨事务发送的许可。

错误码及固定 Message 沿[执行合同 §4](b-procurement-execution-contract.md#4-新pp错误编码与typed-outcome)，尤其 PP408/409、PP410、PP411/412、PP413/414/415 与 PP110/111 分清真实位置；不能把成本/期限拒绝合并成泛 PP902。未受证依赖 PP906，错证 PP902，authority PP901，计数溢出 PP905。PB 只有原精确 23514 Message/Constraint 窄兼容，其他完整性/权限/执行错误原样失败。

## 4. 真实锁图与同步缺席

```text
普通 request 定位
  → tenant S → owner membership S → key U → model S → channel S
  → customer reservation U → 新 statement 刷新 request/checks/quote
  → upstream INSERT（含 native FK KEY SHARE；不是额外 credential S）
  → 新 statement 查实际 binding
      unmanaged：初生 origin/head → seal
      managed：account N → supplier shared namespace（未配置 scope）
                → supplier control N（最多一项）→ account head U
                → 新 statement 取真实 before/gate/quote/bounds
                → p INSERT / PB gate / PE / A / PB effect / journal/head → seal
```

N=FOR NO KEY UPDATE。同 request 的 reservation 串行此入口与后续证据；新 attempt/head/p 无不存在行可供旧锁，INSERT 自己取得原行锁。managed account head 在实际 account/PB 后、gate/p 出生前取得；本根不为统一形式提前锁 request head，不产生 request decision 或 member。所有等待后资格依新 VOLATILE statement，不能把 read+lock+资格合并为同一旧 snapshot CTE。

[绑定 Go](../../internal/procurement/store.go#L203)按原命令锁→channel U→credential S→account N；raw [procurement_binding_synchronize](../../migrations/014_procurement_accounts.sql#L48) 同样 `UPDATE channels SET id=id`→credential S→account N。因此 channel S 后的新 statement 可证明本次 binding 缺席/存在；AFTER attempt INSERT 当时没有 p 不能证明 unmanaged。绑定不可变，closure 不能从 account/head 回头获取 channel/credential/request 锁。原 RecordAttempt 未显式 credential S；新根不把冻结 membership 读冒称该锁，private closure 复用已持前层屏障。

普通 FK 的 KEY SHARE、014/020 trigger 重入 account N/namespace、PB allocation control N/allocation U、窗口/控制更新及 deferred COMMIT SELECT 都要进入准确调用图。supplier 控制首配由原 Commander 的 account N 与 namespace X 同步；零控制不等于免做 namespace 缺席同步，也不制造 PB allocation。后来绑定的旧 unmanaged 不得改写分类，未来 dispatch 必须沿原 binding_changed 拒绝。

## 5. admitted_at 的具体出生位置与私有 phase

### 5.1 原 upstream INSERT 许可与原 p 触发器

“来源先于效果”不表示所有 source 都先于所有原件。公开根必须先真实 INSERT upstream attempt、取得原 DEFAULT created_at/020 birth stamp，随后才调用固定三参 closure；这是上位合同已有的明确原件出生许可。attempt INSERT 不自动判 unmanaged，不自动 seal。新早期 invoker guard 拒 APP 原件写、校验 W+actual APP 和初态/出生指针；旧 `pb_attempt_stamp`→`pb_dispatch_guard` 仍运行，新末序核验确认真实 pb stamp、prepared 与 dispatch checked NULL。attempt 的身份/quote/route/出生列之后不能 UPDATE，既不能先改后改回，也不能凭 xmin=current 登记任意旧行。

p 的真实 BEFORE INSERT 现有字典序是：

| 触发器精确名称 | 函数及实际职责 |
| --- | --- |
| `pb_procurement_stamp` | `pb_stamp_procurement`：核 prepared/H=U/S0/no anomaly 和真实 same-tx attempt；重入 account N/namespace，写 pb_created_xid、pb_control，**在这里调用 pb_now() 写 pb_admitted_at** |
| `procurement_reservation_identity` | `guard_procurement_reservation_identity`：NEW.revision=1，检查实际 attempt credential/channel 与 binding/account；不要求 pp origin 已存在 |
| 拟新增 `zz_pp_attempt_birth_p` | 新 invoker BEFORE INSERT hook，必须在上述两者之后；读取已生成 NEW.pb_admitted_at，创建完整 managed source 并派生 p 的 pp pointers；不重采/覆盖 PB 时间 |

PostgreSQL 同 timing/event 的多个 trigger 按名称字典序，不能以 CREATE 顺序声明此约束。最终 catalog 要逐名字、事件、timing、function identity 与 enabled 状态核验；若新增 trigger 排在 hook 之后仍会改受保护 NEW，必须纳入末态校验。旧014/020正文和名称不改。

### 5.2 已冻结的 managed hook 方案

不预采假 admitted_at，不建可 UPDATE 的 pending origin。`pp_complete_attempt_birth(tenant text,request text,attempt text)` 固定 private ABI 不变，返回 `TABLE(root_id text,management_mode text,account_id text,reserved_event_id text,account_journal_id text,admitted_at timestamptz)`。它完成 §3/4 的 managed 锁与等待后检查、账户 gate 后发起真实 p INSERT。新 hook 在 p 的最终 BEFORE 阶段：

1. 只接受 current_user=W、actual session_user=绑定 APP、READ COMMITTED，且该 attempt 有本 top-xid 的不可变完整 root pointers/quote_selected_at/020 stamp；root 尚不存在，origin/head/p/PE 均尚不存在，身份/初态与原 frozen attempt 精确相同。root ID 来自完整根局部生成并已在 attempt INSERT 写入，不由 caller/helper参数选择。
2. 对新 statement 下的真实 binding/account、已锁账户/head/阻断和 NEW 的硬界/U/quote/初态复核；不采用 caller before 或一个“已检查”布尔。生成非零 transition/event/journal IDs，origin 自然 ID 为 attempt。root/step/xid 必须就是该 attempt 的不可变出生组。
3. 一次 INSERT 完整 managed AttemptOrigin，admitted_at=此时真实 NEW.pb_admitted_at，引用 DB 新 IDs；初始化 AttemptHead `(ordinal0,tail NULL,member_count0,member_tail NULL)`。随后一次 INSERT 完整 reserved ProcurementTransition：cause=该 origin、step2、before p 组全 NULL、after=(rev1,prepared,U,0,false)、delta=(U,0)、event_key=`reserve:<attempt>`、evidence_ref=''。不能从 p AFTER 终态补造 before。
4. 由该真实 source 派生 NEW.pp_created_xid、pp_origin_journal_id、pp_last_journal_id；回交 p INSERT。hook 不写原 PE、账户桶、PB allocation/event、账户 journal、seal root，不调用 provider。RootStep/node/head 可由 closure 在 p RETURNING 后逐步建立；source 对它们的边必须真 deferred。

这里 origin/transition 在 **p 的物理行出生之前**，但在 upstream attempt 原件出生之后；完整 source 此时有真实 NEW stamp。原 `procurement_reservation_identity` 不查 origin，因此无需假定旧 guard 会回读尚不存在的 source。source guard 只能核当前已存在部分和不可变来源身份，不能在该 phase 用 SELECT p 要求尚未写入的 p 可见；反向的 p/PE/journal 完整性由真实 deferred 边及 seal/deferred validator 闭合。若任何新增 BEFORE guard 需要 origin，放在此 hook 内生成 source 之后，不能放在原 stamp 之前造成死循环。

此 hook 没有公开参数、不是 APP 可调用 helper。APP 撤 upstream/p/PE/source/head 的全部 DML；private closure/source/effect 函数均无 APP/PUBLIC/peer EXECUTE，W NOLOGIN/无 membership 或 SET ROLE 路径，owner/peer不能仅 SET ROLE APP 冒充 actual LOGIN。W 仅通过固定无动态 SQL 的根获得此路径。所谓 phase 由真实新 attempt 的不可变出生组、未 seal root、精确 source/p 缺席和当前受控函数调用顺序共同限定，**不是 GUC、trigger_depth、txid相同、一个可自报 phase 参数或任意同 xid source 存在**。实际材料须以 raw APP INSERT/私有调用/旧attempt/同事务另root攻击验收；仅函数命名 private 不构成证明。

unmanaged 不发起 p INSERT，也不执行此 hook；closure 从真实新 attempt 和锁后 binding 缺席一次生成 origin/head，managed 联合全 NULL、member_count/tail均 NULL，step_count1、head ordinal0，无 node。两分支既不能互补登记，也不能对已有 origin 第二次 closure。

### 5.3 延迟边必须是真实 DDL，不能只靠命名

候选当前声明以下均 `DEFERRABLE INITIALLY DEFERRED`，所以 hook 可引用尚未出生的 p/PE/journal/root/step：

- [source 约束](../../internal/procurementprefixschema/materials/source-constraints-v1.sql)：`pp_s_attempt_birth_root_fk/root_r_fk/root_a_fk/step_fk`、`pp_s_step_attempt_birth_fk`，以及 upstream 双向 `pp_s_attempt_birth_original_fk` / `pp_s_original_attempt_birth_fk`；head/node 双向闭合。
- [account 约束](../../internal/procurementprefixschema/materials/account-constraints-v1.sql)：`pp_ad_birth_p`、`pp_ad_birth_admission`、`pp_ad_birth_transition`，`pp_ad_p_origin_journal` / `pp_ad_p_last_journal`，transition↔PE、transition↔journal 及 cause/frozen quote/bounds 的边。

PK/UK/CHECK 并未延迟；NEW 的 NULL 联合、初态、值域必须当场满足，不能先 incomplete INSERT 再补列。原 p→attempt、p→account/binding 的 native FK 仍要求其已有实际原件。新宽 TEXT 精确链接的替换还须按[exact-link 合同](b-procurement-exact-link-contract.md)交付其固定双端 guard/锁后重读，不将可能超过 btree entry 的宽 UK 当作可投产接受集；本页不修改现有关系材料。caller 提前 SET CONSTRAINTS IMMEDIATE 可使未闭合事务失败，不得因此吞错或临时禁用约束。

## 6. managed reserved 效果与允许的事务暂态

p INSERT RETURNING 后，closure 立即逐值核 origin、transition、p actual defaults/020 stamp/binding/初态/pp pointers。使用原 admission 时点执行 supplier gate，再复核 Key/两个 deadline/当时 quote；均通过才消费本 root 的 reserved transition 执行效果。私有 effect 不收 caller amount/before/ordinal/账户，按真实 source 独立推导唯一目标：

| 对象 | 唯一合法本次结果 |
| --- | --- |
| upstream attempt | 原新 prepared/unknown/NULL，quote/route/身份不变，last delivery/cost/dispatch NULL |
| p | rev1/prepared/H=U/S0/anomaly=false；origin/last journal同本次新journal，actual pb stamp/time保留 |
| 原 PE | 真实新 id，type=reserved、key=reserve:attempt、account/attempt固定、delta=(U,0)、evidence=''，完整 transition/journal/root/step2/xid pointers |
| account/journal | Config/revision/C 不变，H+=U、S不变；journal kind=reserve/source_kind=budget_event/source_id=PE，真实 before 对应已锁旧 account/head，p before全NULL、after完整；gate_extra=U、gate_checked_at为实际账户 gate采时，pb_decision_at=真实 p admission time |
| Attempt 链 | step1=attempt_birth 无node；step2=procurement_transition 恰 node1/prevNULL，head推进ordinal1/tail该node，member仍0/NULL |
| account 链 | ordinal=旧+1，previous_id=该账户真实旧tail；journal.after、原A RETURNING 与新head逐值一致；p.last也是同journal，不拿account全局previous当p旧last |
| supplier PB | 无control：marker NULL、无allocation/event，仍做namespace同步及same-tx源核验；有control：按下段建立唯一真实新镜像 |

PB 沿原 [AdmitSupplierTx/ApplySupplierEffectTx](../../internal/periodicbudget/tx_supplier.go)语义：`pb_gate(control,p.pb_admitted_at,U)` 先 observe 真正原期窗口；原 reserved PE INSERT 后同PE的镜像只能产生 `origin=new` allocation，origin_source_id=该PE、baseline0、upperU、admitted_at/marker/window逐值匹配。allocation 初始rev0/H0/S0，再以该 PE INSERT event，原020 BEFORE守卫派生target，AFTER `pb_event_apply`推进 allocation/window/control。U=0也必须有真实reserved PE、journal、allocation/event；新源不准借 legacy_catchup/import。B-P1 的观察 journal/head按原窗口出生闭包运行，W不取得预算配置/operation写权。

旧014守恒和020供应商检查本来都是 deferred，允许同一事务短暂出现：新 p 已有U而 PE/A 还未加U；PE已写而A/PB未完成；PB allocation rev0尚未首event；journal/head/RootStep尚未闭合。**这些不是可提交状态。** 每一暂态只能由同root精确源限定并按固定顺序消费一次，禁止运行“先全域认证当前完整终态”再忽略失败，禁止统一放开所有同xid写。account before capture在 p 出生前完成；source/effect guard可比较锁定A/head和本次source的固定target，不能以“p sums已先变化”为由用最终金额倒造before。最终seal与COMMIT同时重新核原014 sums、020 PB来源、全typed反向集合、原A/current head。

账户旧v1 validator会在首条reserve后返回PP906，故完整效果必须先有新版本 command+reserve validator和写guard；不能在成功根后令正常configure永远不可用。其可接受的 command→reserve→configure→reserve交错要真实测试；dispatch/后续PE域未实现仍封闭拒绝，不伪装已支持。

## 7. seal、deferred 与运行权限责任

closure 最后一次完整 INSERT `pp_operation_roots`：entry_kind=attempt_birth、scope_kind=request、tenant/request/attempt来自原件、account_target NULL、actual top xid/APP OID，semantic_result=managed|unmanaged，step_count=2|1。seal 前枚举准确source/step/node/原件与分支禁止集合，不只核COUNT；同root禁止account command、delivery、incoming、cost mutation、audit/outbox等额外来源/输出。源一旦root已存在均不可追加，root/step/origin/transition/node/journal不可 UPDATE/DELETE/TRUNCATE。

**seal唯一归 private closure**；外层完整根之后仅按 actual attempt 重建原返回及检查固定typed结果，不能再写原件/来源/effect/第二root，也不能“验证通过后”在事务尾补一个自报passed根。单个事务可顺序调用多个完整根，但各根输入输出不可串用；前根已seal历史可成为明确前驱。

追加只读 `pp_validate_root/request/attempt/account/original` 的实际对应分支及 deferred wrapper，必须既从source遍历输出，又从原attempt/p/PE/A/head/PB发现缺源、多源、错root/step/xid及部分pointer。validator SECURITY INVOKER/VOLATILE/PARALLEL UNSAFE、无锁/DML/补证；actual APP顶层COMMIT具有其准确SELECT/EXECUTE传递闭包。只接受command+reserve的新候选不能为其余分支写success stub；缺域PP906，错证PP902。

公开根SECURITY DEFINER、W owned、固定trusted-schema-first search_path；依赖显式schema限定，W不是table/schema owner，不获BYPASSRLS/CREATE/可授角色。纯函数及private/trigger/validator均INVOKER，APP无private EXECUTE。新增trigger只自动运行不要求给APP显式EXECUTE；deferred中真正调用的readonly闭包则须精确授权。共享account/root/journal guard必须按封闭kind分派，保留账户原命令digest、audit、replay与时间合同。

父锁权限按[完整ACL §2.1](b-procurement-full-acl-contract.md#21-行锁的额外列权)准确单列UPDATE满足行锁并invoker拒真实UPDATE；不因为需要行锁授W配置/身份写面。W新attempt INSERT不授delivery_state/cost_status/cost_amount/created_at/PBstamp/dispatch_checked_at；p/PE/A写列沿完整ACL最小矩阵。W runtime `SELECT(xmin)` 只在原020/new-source确实读取的upstream/p/PE等固定sidecar，不能靠表级SELECT过关；xmin只实时辅助原guard，不进入历史DTO或独立资格证明。APP保留绑定/观察/原PB Commander的既有入口及account仅锁UPDATE(created_at)，其真实UPDATE仍被拒。

READ COMMITTED、lock_timeout=3s、statement_timeout=5s由原外层在frontend statement前设定并由候选核准确值；完整DB根整体受一次5s，不给SPI续时。原billing仅已知整次回滚且40P01/40001、ctx有效时最多三次；55P03/57014不扩充重试。原多个frontend语句累计可能超过5s，单根更紧的差异须实测，不能宣称全部时间接受集相同。未知COMMIT只按tenant/request/sequence与精确原身份只读查询，不重做准入或模型请求；RecordAttempt成功也仍须独立PrepareDispatch提交后才能发送。

## 8. 实施切片和实际尚缺正文

下列是有顺序的交付依赖，不是已完成勾选。可并行实现纯规则/测试oracle、权限清单与guard审查，但共享SQL装配、actual APP专库PG和架构由集成人串行执行。

| 切片 | 必须实际交付的正文 | 依赖/边界 |
| --- | --- | --- |
| A0 原规则叶子 | billing UUID接受集、Go五字段JSON编码/长度、full uint64、ignored BYTEA参数联合与quote Equal、确定quote+真实采时的一行SQL、原错误精确映射 | 不改原Go API；纯Go测试显式去掉TEST_DATABASE_URL；叶子不授DML |
| A1 真实父出生/保护 | 原Reserve真实request自动origin/head hook、冻结request身份/硬界/quote/期限guard，readonly request认证及准确APP原Reserve权限 | 本根reserved transition的frozen_bounds引用真实RequestOrigin；不能用owner手插fixture替代真实受控request出生；legacy不补证 |
| A2 managed出生时序 | §5 upstream早期/末态guard、固定三参closure、新末序p hook、完整origin/transition初生、pointer/Step/node/head guard；unmanaged分支和已有attempt原快返 | A0/A1和本页已冻结hook；不开放三参APP入口、不以pending source解决时间循环 |
| A3 reserved效果 | 私有仅source消费helper；PE/A/PB镜像与journal/head实际写入；账户command+reserve完整validator/guard扩展；最终seal/deferred反向枚举 | A2；原014/020/B-P1全部真实运行；暂态许可必须逐对象/逐阶段封闭，不能忽略旧v1拒绝 |
| A4 最小权限装配 | 新函数/trigger精确inventory、W/APP/owner/P/K/peer/PUBLIC/继承闭包、父锁单列与拒写、PB调用边界/sidecar、COMMIT只读闭包 | A1–A3；固定SQL/ACL摘要，新候选不就地改旧v1/V3证明；宽TEXT边须先交exact-link实际guard与新catalog |
| A5 adapter/actual PG | 显式prospective executor接缝、typed结果/SQLSTATE适配，真实APP LOGIN全根/旧路径差分、故障/并发/撤权矩阵 | A4；原普通profile不自动启用，失败无回退；不在默认main安装 |
| A6 集成/文档 | 正文/SQL/权限/验证一致，架构模块/请求/数据/权限/状态/源码链接复核，refresh+只读check，相关make门禁 | 所有并行结果汇总后执行；正式迁移/provision/在线资金/profile另R2；纯核Complete/Qualified不擅自升级 |

当前真实缺失不仅是 `pp_record_attempt` 函数声明，还包括：完整原检查SQL/原错误顺序；Go编码路由纯叶子；request自动出生与保护；upstream/p出生guard和新hook；私有closure及source效果helper；command+reserve账户认证/暂态控制；单attempt node推进；双向原件/root模板validator；精确trigger/deferred/ACL/catalog装配；真实候选executor/受限LOGIN验收。关系材料与私有纯重放中有同名DTO不代表这些DB正文已实现。

## 9. 专库验收与故障矩阵

必须使用专用PostgreSQL库、actual APP LOGIN和新候选最小列ACL；owner+SET ROLE或owner直接执行的fixture只能准备父管理数据，不能替代被测真实出生根。成功oracle从原业务合同/手算独立获得，不用writer本身拼期待来源。每个失败核本次attempt/p/PE/账户/PB/source/head/root均无残留，并保留之前已提交根。

| 组 | 必须覆盖的可执行证据 |
| --- | --- |
| 基础与重放 | managed/unmanaged，RouteRevision0与发布路由，noquote/partialquote，U0/非零，未配/已配enabled或disabled supplierPB；同序号同身份快返、异身份冲突，重放前IP/Key/route/deadline/retry错误仍拒；旧attempt不补生 |
| 字节与JSON | ignored expected含NUL/非法UTF8/空bytes/负或NULL rate仍保持原接受集；实际消费分支精确Equal失败；has联合错误；priority两端、weight>MaxInt64/MaxUint64、1000/1001项、262144边界、Go转义字符、额外键/错类型/selected重复或错配 |
| 报价/时钟 | unmanaged首次Q1后跨scheduled Q2仍Q1；managed account/control/head真实等待后跨Q2拒，首次及第二次noquote/unknown优先序；跨request/financial/Key expiry分别核原时点与Message；equal-effective按created/id稳定；PB原窗口和时钟回退保持原拒绝 |
| 来源顺序 | 原attempt先INSERT、source在p最终BEFORE一次完整出生、actual admission相等；hook后故障回滚；无pending UPDATE、没有origin时p不能成功提交；origin/transition/root同xid但错对象不能通过；原scope/身份先改后改回不可补生 |
| 并发锁 | 绑定先提交/后提交及最初无binding双向等待；共享account并发两个request合计硬界不超限；account configure/paused/blocker与limit降低；PB首次配置与新p互斥/缺席同步；同request同seq并发只一次reserve；所有等待用新statement读资格 |
| 暂态与seal | 在source、p、gate、PE、A、allocation rev0、PB event、journal、head、step及seal每个真实边界注入失败；缺/多/错PE/journal/PB/step/node/source、跨root输出、提前seal、seal后追加、提前SET CONSTRAINTS均拒；command→reserve→configure→reserve保持全链 |
| 最小权限 | APP/owner/peer/worker/PUBLIC/继承及SET ROLE身份拒绝；APP raw INSERT/UPDATE/DELETE/TRUNCATE、private EXECUTE、created_at原值赋回；逐项撤必要普通/系统SELECT/列DML/readonly闭包EXECUTE必须失败且回滚；无关secret/配置/模型客户端权限不得新增 |
| 运行失败 | 真行锁55P03、准确取消/57014、两次不同锁等待覆盖单根整体5s不续时；确定40P01/40001最多三次；网络/scan/commit未知不转成功、不重放采购或模型；原ordinary profile相关回归继续通过 |

专项通过只可记为本隔离根/明确支持前缀的证据。真实供应商、生产支付、容器/1Panel、HA、正式权限/迁移、完整来源四域认证和最后性能指标仍按其独立阶段交付，不以本地fixture、纯核或本合同关闭。

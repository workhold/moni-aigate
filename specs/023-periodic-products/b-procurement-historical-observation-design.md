# B-HO1：历史观察证据的独立版本设计草案

2026-11-04；审查基线 `b3fd5cb`。状态：**设计草案，未实现、未安装、未验收**。承接[历史观察缺口](b-procurement-replay-closure-plan.md#3-仍须补足的历史观察依据)、[实施合同](b-procurement-replay-implementation-contract.md)、[HLD](../../docs/design/HLD.md)与[LLD](../../docs/design/LLD.md)。本稿不修改 B-P1 的32表715列、40类原件482列、原 PBprefix V1、旧 V3 或已应用迁移，也不开放公共 `Check`。

## 1. 结论与不扩字段的可复用部分

现有材料不能普遍补齐两项历史观察，但并非全部关系都要新建证据。

| 观察 | 现有证据及可复用范围 | 不能从现有证据推出的部分 |
| --- | --- | --- |
| approve 实际派生 incoming/decision | 已实现 source→incoming→唯一 normal decision；同 root/xid、递增 step、before_latest 精确绑定 | 这个子集的前驱已可证明，不再泛称缺 cut；完整输入与 sticky 仍由 RequestIncoming 负责 |
| 不派生 decision 的 Reconcile | P/A/R 语言只给人工 action 偏序；`prior_latest_id` 只引用合法旧 finalization | 无 request-head 观察位置，不能确定该旧值在此 source 之前最近；人工偏序没有覆盖其间的 finalize/dispatch |
| no-latest 的 sent 查询 | attempt birth/node/delivery 各自完整，单 attempt 终态可重放 | 不能确定全部 attempt 的出生与 delivery 变化位于 Reconcile 查询之前还是之后；现在的 sending 不能倒填过去 |
| PB 正向 reservation/settlement/PE | 永久 birth marker、精确原 allocation/event 及同根效果桥可证明相应 control 和真实效果；control 不可删除 | 不能把这些正向证明泛化为另一 scope 当时不存在，也不能把缺 event 当 absence |
| PB 配置与 window 正向前缀 | 已有完整原 PB event seq、配置版本、PBprefix command/window_birth ordinal 及其 event cut；应继续复用它们核已覆盖控制的配置、窗口与金额 | 目前没有 admission/dispatch/finish 对该观察链的通用位置；无 effect 的读尤其缺桥；未配置 scope 根本没有 control 起点 |
| 首次配置的旧 held 导入 | 原 writer 在独占 namespace 屏障下枚举未决来源，原 PB/PBprefix 能核已给出的正向 import | 不能从今天的已结清集合重建首配当时完整未决集合；原 PBprefix 仍保留 `LegacyMembershipUnproven` 与 `LegacyEffectBoundaryUnproven` |

已有 PB event `e` 的 `seq=n` 能提供真实事件前后边界：同 control 的 observation.event_seq<n 在 `e` 前，event_seq≥n 在 `e` 后；必须同时核完整 event/observation 序、真实 source 与 window 出生关系，不能只比较一个数字。这个桥可继续收口**已有真实 event**的子集。无 event 的 dispatch/absence 没有相同锚点，不能借用最近任意 event。PBprefix 本身也不能消除其明确保留的 legacy 缺证。

反例：approval clock 到 deadline，因而没有派生 decision；settlement decision clock 随后回退，执行 no-latest 的 sent 查询。相同最终 attempt= sending，可以来自查询前派发或查询后派发，现715列没有相对边。另一个 scope 在 finish 后首次配置，与 finish 前已配置但漏写其应有效果，在缺少 scope 观察序与完整导入集合时不能仅以今天状态区分。

## 2. 已核对的真实同步点

以下是当前 Go 与原 SQL 的真实行为，不把尚未实施的候选 writer 写成现状。

| 路径 | 当前锁与读取顺序 | 新证据应附着的位置 |
| --- | --- | --- |
| `lockRequest` | 初始无锁读取只用于定位；tenant SHARE→membership SHARE→Key UPDATE→model SHARE→reservation UPDATE；随后新语句重读 status/latest | reservation 同步点以后；不能采用第一次读取的 latest |
| `Reconcile` | actor role SHARE→上述 request 锁→case UPDATE；新 approve 取自身 clock，必要时派生 finalization，再调用 recovery settle | 新 source 的锁后、派生前读点；同事务后续 decision 和 settlement 各自追加真实动作 |
| no-latest settle | 先唯一 settlement 查找；无既有结果才取 decision clock；未到 deadline 且 latest=NULL 时查询全部 attempt 的 delivery≠prepared | 原查询位置消费 request 历史前缀；不能预先缓存 sent 或用最终数组计算历史 sent |
| 新客户 finish | 可选 model UPDATE→customer PB 锁→ledger accounts 按 ID UPDATE→新 financial clock→真实资金/settlement/PB 效果 | PB 锁取得后同次 owned values 的观察；financial clock 仍在账户等待之后重新取样 |
| PB 未配置读 | `pb_sync_unconfigured` 在查无 control 时取得 namespace shared advisory；READ COMMITTED 后续语句重新看已提交状态 | 在相同 namespace 同步域记录 absence；不能只在 API 层查一次 |
| PB 首配 | scope 父对象锁→namespace exclusive advisory→枚举 legacy 未决集合→control/import/receipt 同事务 | control 出生和完整导入边界；不得为观察反向锁旧 request/reservation |
| PB 已配置读 | customer control 按 ID 锁；supplier 自身 control 锁；dispatch 先 customer 层再 supplier 层，最终统一 decision clock | 现有 control 锁内采样，顺序与原领域一致；不能从底层触发器反向取得父锁 |

源码依据：[request 锁与派发](../../internal/billing/evidence.go)、[人工核对](../../internal/billing/recovery.go)、[结算](../../internal/billing/settle.go)、[PB locks](../../internal/periodicbudget/tx_locks.go)、[首配 commander](../../internal/periodicbudget/commander_configure.go)、[原 PB guard](../../migrations/020_periodic_budgets.sql)、[PBprefix V1 capture](../../internal/periodicbudgetprefixschema/materials/candidate-v1.sql)。XID 只用于同事务归属，不作提交序或跨事务排序；墙钟只用于原业务期限/周期，不提供事件先后。

## 3. 候选证据模型（仍有冻结前门禁）

当前建议新增独立 B-HO1 sidecar，维护 **request 序** 与 **PB scope 序**。只给 Reconcile 加一个 caller 可填的 cut/sent/count 不完整：必须同步覆盖能改变这些观察的全部 writer。表名为待冻结候选名；精确列类型/唯一键/FK/generated 清单须在独立 schema/read/ACL manifest 中另行冻结，不能就地改 V1 签名。

### 3.1 Request 的六类动作

候选三表 `pp_ho1_request_origins/heads/journal`：

| 表 | 最少持久字段 |
| --- | --- |
| origins | `tenant_id,request_id,version=1,request_origin_id,created_xid`；仅从真实 request birth 原子出生，初态 latest=NULL、attempt 集为空、settlement=NULL |
| heads | `tenant_id,request_id,ordinal,last_entry_id,updated_xid`；严格 0/NULL 或正 ordinal/非 NULL tail |
| journal | `id,tenant_id,request_id,ordinal,previous_id,kind,source_id,created_xid`；B-P1 来源另有精确 `source_root_id/source_step` NULL 联合；`UNIQUE(request_id,ordinal)` 与每类 source 单次绑定 |

`kind` 封闭为：

1. `request_birth`：绑定真实 RequestOrigin 与 reservation；只允许序首。
2. `attempt_birth`：绑定完整 AttemptOrigin，加入 request 的 attempt 集；不能重复 birth。
3. `delivery`：绑定真实 AttemptDeliverySource，逐值推进该 attempt delivery；不得用 dispatch permit 覆盖后续 delivery，也不得遗漏 recovery/finalization。
4. `decision`：绑定真实 RequestDecision，逐值推进 latest；conflict 保持 latest，不制造新 finalization。
5. `reconciliation_read`：唯一绑定本次新 ReconciliationSource；只观察，不改变 latest/attempt；复用现有 `prior_latest_id`，不重复保存另一个可互换的 latest。
6. `settlement_birth`：绑定真实新 settlement，认证既有结果与 absence 的前后边界。普通 Settle/Release 也必须覆盖，不能只记录人工路径；其 journal 同 tenant/request/operation 及 CreatedTxid 精确闭合。无 B-P1 root 的普通原件保持明确 NULL 联合，不伪造 root。

锁后新 source 的 `reconciliation_read` 必须先于同根派生 decision；settlement_birth 必须在真正创建 settlement 时追加，既有回执不追加。原件/source→journal 及 journal→原件/source 都恰一条；缺失、重复、多值一律缺证或矛盾，不任取。

重放以 `reconciliation_read` 前缀恢复 latest、完整 attempt 集和各自 delivery，以及是否已有唯一 settlement。因此 sent=`存在出生且 delivery≠prepared 的 attempt` 可以独立计算；zero attempts、prepared-only、已 sending 后 failed/unknown 均明确区分。不得把 `sent`、count 或 hash 当独立事实源。已有正常 decision 桥继续使用，并与新序一致性对照。

### 3.2 PB scope 起点必须早于首次 control

候选三表 `pp_ho1_scope_origins/heads/journal`，自然域键为 `(scope,scope_id)`；scope 封闭 tenant/key/procurement，key 的 tenant 归属逐值绑定。此序不能以 control_id 为起点，否则先前 absence 仍无载体。

| 表 | 最少持久字段 |
| --- | --- |
| origins | `scope,scope_id,tenant_id?,version=1,created_xid`；原生完整 scope 从其 tenant/Key/account birth 同事务开始，scope_id即对应真实实体PK，初始 control=NULL、未决集合为空 |
| heads | scope 键、`ordinal,last_entry_id,updated_xid`，严格前驱联合 |
| journal 公共列 | `id,scope,scope_id,ordinal,previous_id,kind,source_id,source_phase,created_xid`，精确来源身份/事务联合；kind 决定 source 物理类型，不能开放任意表名 |
| journal 观察列 | `control_id?,pb_observation_ordinal?,pb_observation_tail_id?,event_seq?,last_event_id?,configuration_revision?,configuration_operation_id?,window_id?,window_revision?,window_tail_id?,observed_at?`；absence 时 control 及全部控制 cut 为 NULL；present 时完整成组，event 0/NULL 与 window 0/NULL 分开 |

scope 动作仅覆盖必须的关系：

- `customer_reserve/customer_settlement`：分别绑定 request 序中真实 birth/settlement，按 tenant 与 Key 各一槽推进未决客户集合；零金额也必须记录。
- `supplier_effect`：绑定真实 ProcurementTransition/PE，按其 p 前后态维护 managed supplier 未决集合与 held；prepared/sending/unknown/disputed 属未决，settled/released 不属未决；从已结清转 dispute 必须重新纳入。不得从 account 当前总额倒算成员。
- `control_birth`：唯一 NULL→真实 control，绑定 first operation/version=1；在此 prefix 独立导出完整旧未决集合，与本次 legacy_import 的 allocation/event **集合双向精确相等**。后续禁用不删除 control、不退出跟踪。
- `pb_prefix`：一一绑定原 PBprefix V1 的每个 command/window_birth，保留其完整 event/config/window cut；所有有 V1 observation 的来源都必须进入新序，不能只镜像选中几条。
- `source_read`：绑定 admission、dispatch、实际 finish/effect 等真实读取来源和封闭 phase。absence 必须在该序的 control_birth 之前；present 消费此前 control/configuration 及 PB event/window cut。已经有精确真实 PB event 的子集优先复用 event bridge，不能用它替代别的无 effect 观察。

tenant/Key 没有可借用的 B-P1 entity-birth source，不能给 origin 填一个不存在的来源ID。新版本须在两者的真实 INSERT 出生路径以受控 trigger/capture 生成 scope origin，并与实体自然PK双向覆盖；account可继续精确绑定既有 AccountOrigin。guard必须验证真实新tuple属于当前事务，拒绝向旧实体事后认领 native origin；这项新 capture/ACL/安装basis也是版本交付义务，不能只写 reader 接受规则。

`source_read` 的真实来源/phase 清单须与 writer call graph逐项冻结：客户准入→RequestOrigin；供应商准入→AttemptOrigin；派发→唯一 dispatch DeliverySource；人工派生 finish→CustomerFinishOutcome；普通新 finish→独立受控 finish 声明或经审查的真实 settlement source（尚待冻结）；供应商效果→该真实 ProcurementTransition。既有/pending 快返没有执行 PB read 时不伪造观察，也不强制产生效果。失败并回滚的调用不产生可见观察；成功的零 delta、禁用期间记账仍有原合同要求的真实记录。

普通 Settle/Release 及其 Recover/MarkPending 期限收口等复用 finish 的路径，没有 ReconciliationSource/CustomerFinishOutcome，不能伪造 B-P1 outcome 或 root。B-HO1 须另冻结受控的独立 typed finish 声明，或复用经完整审查且覆盖普通结算的真实 settlement source。该声明至少负责：仅在真实新 settlement 创建时产生，逐值绑定 tenant/request/operation、同事务 CreatedXID 及唯一 settlement；在相应 scope head 与原 control 锁内、真实 PB 读取阶段捕获 tenant/Key 各自独立 optional 的 control/config/window/event cut；封闭“effect 之前”的 phase，并与该 settlement_birth/customer_settlement 双向唯一闭合。既有 settlement 快返或 pending 未执行 PB read 时不得伪造观察。普通原件的 NULL B-P1 root 联合只表示无该版本 root，不能充当观察证明。精确字段、capture/guard/ACL/read 与 basis/catalog 仍须另行冻结；当前六表草案尚不是普通 finish 全路径的完备物料。


`pb_prefix` 与 source_read 的 cut 可沿原完整 PB event seq/observation ordinal 重放，不复制金额账本或另立计价规则。window ID 必须由所消费的 period/calendar 与真实 observation/window birth 确定；window 不存在是观察 prefix 内尚无该 start 的窗口，不是今天查不到或 caller 写 NULL。

### 3.3 writer 与同步规则

request journal 只在已持有原 reservation 同步锁后追加；对应 head 在原业务根之下，不可从 head 反向加锁业务父行。attempt birth、delivery、request decision、全部新 settlement 与 Reconcile 都同事务接入；新 observer 在真实查询阶段消费同一组 owned values，不另做“看起来相同”的查值。

scope journal 需要持久的 scope head 同步点，原 namespace shared/exclusive 屏障继续保留。所有受 native 覆盖的入口必须按完整锁序共同升级：现有身份/父对象与 request/account 层→namespace 屏障→scope head→原 control→旧 PB observation head/window/allocation；customer scope 成组采用固定 tenant→Key 次序，supplier 层仍在 customer 层之后。不得把新 scope 锁只塞进底层 trigger，而让另一入口持有 control 后再反向取 scope。首次配置在 scope 锁内只读已稳定的未决集合，不反向取得旧 request/head 锁。

对于已有真实 native origin/head 的 scope，已配置路径也必须经过相同 scope 入口，不能因为 control 已存在就跳过新序；旧 scope 的 uncovered 分支见§5。原 `pb_sync_unconfigured` 仅处理“可能首次配置”同步，本身不足以成为新 scope 总序。capture/helper 必须由封闭 writer 与 guard生成字段，APP 无直接 INSERT/UPDATE head/history 权限，不能允许预建 journal 后由后来的 source 认领。deferred 检查同根/同事务、源/观察一一绑定与末 head，history 禁止 UPDATE/DELETE/TRUNCATE；角色分离、READ COMMITTED、新快照及异常回滚沿原合同。

这个候选方案增加 native unconfigured scope 的持久写/锁，可能增加同 tenant 热点。性能成本必须先测；不能为避免此成本退回无认证的布尔。如果后续采用并发 epoch/seal 方案，须另证完整成员、seal 与 absence 的相对关系，不能视为本稿已许可的等价实现。

## 4. Reader、独立 replay 与资格责任

新增独立 `HistoricalObservationRowsV1`（暂名），组合引用原 `Rows`、同次完整原 PB/PBprefix raw 与六张新表；原 DTO/列序/签名不变。准确列读权限、页游标、资源上限、basis/hash、catalog预期对象、私有安装材料均用新版本标识。reader 仍无服务商/资金 authority，不接受业务代码预先生成的 Qualified、sent 或验证摘要。

新独立 replay 负责六表物理值、NULL 联合、双向 source 槽、完整序及上述观察前缀；金额/资金与 PB calendar/configuration/window 继续由原完整 checker 和相应业务域负责，保留同次 Base 逐值。新结果输出逐 source 的“观察有效对象键”和明确缺口，供未来 Q 依赖消费；不能以其 Stats=0 直接给整个 source 或商业链路发资格。

缺观察、完整序断裂、遗漏 source 或覆盖不足应区分 mismatch 与 unproven；命题分母不缩小。相同自然对象多错去重，物理表间同 ID 分开；资源/取消/超时一律零结果。新字段、writer/guard、ACL、reader/basis/catalog、独立 replay、公共调用接线、真实 PostgreSQL 并发回滚验证须作为同一版本交付；不得先宣布来源闭合再留下一半 writer。

## 5. Legacy、兼容与门禁

- 原生完整只授予从真实实体/request birth 就受 B-HO1 全 writer 覆盖的对象。已有 entity/control/request 不允许拿当前投影补一个“初始空序”，也不按 created_at/XID 猜测先后。
- 旧对象继续既有运行合同，报告 `uncovered_request_history`、`uncovered_scope_history`、`legacy_membership_unproven` 等准确缺口。无 B-HO1 head 的旧 scope 只可进入明确 uncovered 运行分支，沿原业务锁/namespace/PB 合同，不补空 head、不写伪 native 序、不授历史观察资格。不能把查无 origin/head 本身当作合法 legacy：独立安装/guard 合同必须区分明确 legacy 与应有 capture 却缺失的 native 对象，后者拒绝，不能静默降级；这个分类只许可 uncovered 运行，不认证过去。
- 每个 request、tenant/Key/account scope 独立判定覆盖。例如旧 tenant 下新 Key：新 Key 从真实 birth 完整跟踪，旧 tenant 保持 uncovered；若 request 从真实 birth 受覆盖，其 request 序可独立完整。资格依赖缺失 scope 时保留对应缺口，不能整操作统授 native，也不能抹去其他已证子集。legacy 无 head 对象的 future-only 持久序不在当前六表 native 接受集中；若需要，另冻显式不完整起点及 reader/guard 合同，永不回填过去。既有正向 event 等已证子集继续保留。
- 本稿不预选“迁移时快照即可信初态”。如需旧对象完整续接，应另冻结受控维护 checkpoint、其外部/既有完整历史依据及可证明的未决成员集合；仅数据库当前扫描或人工声明不够。
- 上线先安装禁用候选，再校验支持的全部 writer、trigger/函数体、表/约束/ACL/basis/catalog。版本覆盖不全时保持维护/候选门禁，不把旧 APP 的可直写路径留成旁路；不自动修改生产角色或重签旧材料。
- 当前两个 Unimplemented、SourceClosure.Complete=false、Qualified 为空及无公共 Check 继续保留；只有逐对象观察依赖与整个联合闭包实际实施、验证后才可调整。

## 6. 验收边界与实施先后

先冻结 source/phase 与锁图，再冻结六表/双向槽/ACL/read manifest，随后同时实现 writer/guard 与独立 reader/replay；最后接入逐对象 Q 并保留旧 Base 对照。至少覆盖：

1. Reconcile 前/后 interleave finalize、两次 attempt birth/dispatch、conflict sticky；同 delivery 内容不同 predecessor；zero/prepared/sending/failed/unknown 全集合；同 XID 多 root；墙钟回退路径。
2. 首次配置与 absence reader 双向等待，等锁后新快照；tenant/Key 独立 optional、supplier scope、先关闭后配置与配置时未决导入、supplier disputed reopen、禁用仍跟踪、零 delta。
3. 删除一个历史成员/观察/PE、增加伪 legacy_import、复制或交换 ID、重复 source、多值索引、错误 root/step/xid、断序/坏 head、旧版本/旧原件不回填；普通 Settle/Release 和所有后台来源没有旁路。
4. 并发死锁/锁超时、取消、事务中途 fault 全回滚；检查没有后台重发生成；hot scope 的锁等待与完整商业路径延迟。只有真实专用 PostgreSQL 验证可记数据库验收，fixture 仅证 replay 规则。

本稿仅交付观察证据设计；未运行 PostgreSQL、未改 SQL/Go、未证明供应商或生产环境。架构正文与变更记录由集成人在接纳本稿时同步复核。

## 7. 实际 writer 与锁矩阵的冻结前清单

独立只读复核确认普通 finish source 与旧 scope 入口必须先按上文解决，不能把六表草案当成已冻结实施合同。首配完整未决集合与正向 PB/PBprefix 桥的设计方向保留；具体 source/phase、列、约束、权限和接受集仍需下一份合同。以下列出必须逐项覆盖的实际写面，避免仅列 typed DTO 而遗漏经营入口：

| 实际路径 | 捕获/顺序责任 |
| --- | --- |
| Reserve | request INSERT 后才 INSERT reservation；目前先持有 PB/wallet 锁，不能让 request INSERT trigger 假定 reservation 已存在 |
| RecordAttempt | 仅真正新 INSERT 产生 attempt birth，幂等快返不重复出生 |
| PrepareDispatch | prepared→sending 与永久许可、真实 customer PB→account→supplier PB 阶段次序 |
| RecordFinalization | normal 的 delivery/decision；conflict 保持 latest；不能遗漏收尾 delivery |
| RecoverProcurement | prepared→failed 或 unknown 同样改变 `delivery<>prepared` 谓词，不以曾获 sending permit 替代 sent |
| 所有 finish | Settle/Release/Recover/Reconcile 及复用 finish 的期限收口，包含普通无 B-P1 root 的真实新 settlement |
| 实体 birth | 平台创建 tenant、自助注册 tenant、Key 创建与采购 account 创建；必须有受控真实 birth 依据，不假定 B-P1 已有不存在的 tenant/Key source |
| supplier 效果 | reserve/finalize/dispute/correction/recovery 的每个真实 PE/transition，包括零 delta 与 settled→disputed |

原 Commander 和 LockCustomerTx 会先锁既有 control，原 BEFORE control trigger 执行时 tuple 已锁。新 head 必须由对应入口在取 control 之前取得，不能塞底层 trigger 反向加锁；dispatch 不得把全部 account 锁提到 customer controls 前。新锁图须以真实每入口语句与等待后的 READ COMMITTED 新快照冻结，尚未证明所有入口组合无死锁。本次仅文档只读复核，不记 PostgreSQL 并发、权限或性能验收。

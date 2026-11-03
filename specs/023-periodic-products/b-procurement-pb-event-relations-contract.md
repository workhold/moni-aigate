# B-P4：PB 控制、窗口、分配与真实事件关系实施合同

2026-11-04；审查基线 `e73d9e93b0e0b7f20d43386439ce5a386f27ce04`。本页已由根集成人审定冻结，作为本片实施合同；实施与验收另行记录。承接[原件DTO](b-procurement-core-dto-contract.md)、[私有重放](b-procurement-replay-implementation-contract.md)、[采购效果](b-procurement-effect-relations-contract.md)及[PB权限合同](b-procurement-pb-acl-contract.md)。

## 1. 责任、源码依据与排除范围

拟在来源域新增私有 `source_closure_pb_event_relations.go/test`，从本次唯一 raw Rows 独立核验 `periodic_budget_controls/windows/allocations/events` 与真实客户/采购原件的内容、集合、序列和当前投影。现有 scalar/union 与真实 finish/transition 的局部 effect 关系不能替代完整 PB event 链。

审查输入为 [40类原件DTO](../../internal/procurementprefix/original_types.go)、[020原SQL](../../migrations/020_periodic_budgets.sql)的 `pb_guard_control/window/allocation/event`、`pb_apply_event`、`pb_check_customer/supplier`、`pb_observe`、[真实客户调用](../../internal/periodicbudget/tx_customer.go)、[真实供应商调用](../../internal/periodicbudget/tx_supplier.go)、[原PB图](../../internal/fundinglotverify/periodic_budget_graph_sources.go)与[重放](../../internal/fundinglotverify/periodic_budget_graph_replay.go)、[B-P1前缀](../../internal/periodicbudgetprefix/check.go)。这些仅说明真实语义；实现不得 import 它们的 writer、expected helper、checker、Stats、Candidates 或报告作 oracle。

本片仅证明下文可从原件独立核实的关系，不授逐对象业务资格。`SourceClosure.Complete=false`、`Qualified` 为空，无公共 `Check`、安装、线上路径或 PG 操作。32表715列、40原件482列、原 writer、旧算法、已应用迁移均不改。

与并行 PB command/version/observation 片按下表分工，彼此不以“另一叶子已过”代替本片 expected：

| 关系 | 本片 | 并行片 / 后续 |
| --- | --- | --- |
| control scope、不可变 period/tracking/effective、窗口日历 | 本片独立核原值关系 | version/operation 对这些原值的逐值关系仍独立 |
| control 当前 revision/enabled/limit/configuration_operation | 不重放配置历史，不认证 gate | 完整版本链、命令与配置来源由并行片核 |
| window 当前金额/revision/tail 与全部 event | 本片 | window observation 双向 binding、birth cut 及其 event_seq 边界归并行片 |
| operation result_snapshot 的动态 held/spent/carry/window amount | 不以今天投影补历史 | 独立后续；本片不宣称已完成 command snapshot prefix |
| new/import/catchup、真实来源镜像与三层金额前缀 | 本片 | 历史配置选择、控制 absence、完整 gate/dispatch 资格另行证明 |
| PP finish/transition companion 与指定 PB event 集合 | 已有 effect 片继续独立 | 本片从全部原 PB event/真实源出发，不能替代 companion 义务 |

## 2. 唯一索引、预算与数值

入口接收同一 `sourceClosureState`，使用原 `replayWork`、context、共享绝对 deadline；不重新预检或续时。建索引、候选/依赖扫描、排序每次比较/拷贝、金额解析及前缀步进均 tick；首次执行错误立即结束，整入口返回零结果。无源数组排序副作用，不先构造超预算的大索引。

所有物理键和以下业务键均保存多值集合，只 `len=1` 才消费；重复不任选，不能先按 PK 丢重再使业务索引唯一：

- control：`(scope, scope_target)`，target 按 scope 取 tenant/key/account；key scope 仍独立比较 tenant。
- window：`(control_id, start_at实际时刻)`；allocation：`(control_id, subject_type, subject_id)`。
- PB event：`(control_id, seq)`、`(control_id, source_kind, source_id)`；allocation/window 的链成员只由精确 control 归属取得。
- 真实原件：reservation 按 request、settlement 按 request、PE 按 event_key；以及每张表的自然 PK。引用定位用物理 ID，不按 payload 自报文本找另一张表同 ID。

复合键沿 UTF-8 字节长度编码。SQL NULL、合法空 TEXT 与非空 TEXT 保持不同；tenant 沿既有旧 TEXT 身份边界，不新增 UUID、trim 或 case folding。scope pointer 的存在性不得用 `value != ""` 代替。

金额均独立 `big.Int`；upper/客户R/charge 为非负 int64，PB 各桶、baseline、source 值和 signed deltas 保留规范 NUMERIC100。不能经 float64 或先转 int64。聚合计算的中间值不截断；实际每次写入的 allocation/window/control 桶须满足非负 NUMERIC100，signed delta 仍可为负。seq/revision 为规范 int64；不以溢出后环绕的计数认证链。

时间按 finite 微秒 envelope 和实际时刻比较，不用 UnixNano（避免远年溢出）。UTC 日/月用独立 Gregorian 日历推导；月不能按30天，日不受本地时区/DST影响。只以明确 seq/revision 决定 event 顺序；UUID、XID、created_at、admitted_at、tracking_started_at 均不充当缺失的历史全序或 cut。

## 3. control 身份与 window 日历

三 scope 的唯一实际身份：tenant 有唯一 tenant、key/account NULL；key 有唯一 `(tenant,key)` 原件且 account NULL；procurement 有唯一 account，tenant/key NULL。多 scope 可以指向相同文本 ID，不能互相替代；同 scope_target 两个 control 是歧义，不择一。

`period_kind` 为 utc_day 或 utc_month，`effective_from=end(kind,start(kind,tracking_started_at))`。本关系使用 control 保存的不可变日历，不能拿当前 enabled/limit 判断历史是否应放行。

每个 window 必须有唯一真实 control，`start_at=start(kind,start_at)`、`end_at=end(kind,start_at)` 且半开 `[start,end)`，start 不得早于 control 的 tracking period 起点；同 control 同 start 仅一行。该下界来自出生 highwater，不用窗口 created_at 或当前时钟推历史顺序。不能因为当前无 allocation/event 删除“空窗口”：dispatch、观察或配置命令可创建空窗口，合法空窗应为 H/S=0、revision0、tail=NULL。

当前 highwater 独立核为 `max(start(kind,tracking_started_at), 该control全部真实window.start)`，对应受支持 `pb_observe` 的完整结果；不与进程当前时间比较，不证明历史每次观察的单调性。window 的 observation 列完整性和 birth 观察内容归并行片；窗口 start 的大小只用于日历/当前最大值，不推配置命令或资金事件先后。

## 4. 真实业务身份与 marker 的正向桥

从真实源遍历匹配控制域，不能只检查已存在 allocation/event 而漏掉原件声明的义务。PB 相关源是：存在明确 PB marker、存在精确 scope control，或被任一 allocation/event 引用的原件；完全无 control/marker/引用的旧源不因本片猜测历史义务。

客户源须唯一 request、所属 tenant/key、同 tenant/request 的 reservation；同 request 最多一个 settlement。reserved/pending_reconciliation 不得已有 settlement；settled/released 必须有同 tenant/request、同 status settlement，released charge=0。这里只核最终源关系，不拿当前 reservation.status 认证当初 import/reserve 的历史状态。request 的 `pb_created_xid` 与 `pb_admitted_at` 同有/同无，非 NULL marker 必须指正确 tenant/key control；无 stamp 不能有 marker。

供应商源须唯一 p、attempt、attempt 所属 request、binding 与 account；tenant/request、credential/channel、binding.account 必须精确，p 与 attempt 的 PB 出生 stamp 相等。p 的 stamp/admitted 同有/同无，无 stamp 不能有 marker；非 NULL marker 指正确 account 的 procurement control。每条相关 PE 必须绑定同 attempt/account 的真实 p；不凭 operation 字符串前缀推 managed 归属。

这些不可变身份桥只验证本片所用来源，不读取密钥、当前角色或当前模型/渠道配置。账户全部 PE 守恒、完整 p 状态机和当前 account H/S 属原账户/采购域，不在本片用其报告作 expected。

## 5. allocation 的共同与三类 origin 合同

每个 allocation 必须有唯一 control、唯一对应业务源，按 subject_type 精确匹配：customer_request 只容 tenant/key 且 tenant/key 与 request 一致；supplier_attempt 只容 procurement 且 account 与 p 一致。upper 分别等真实 R 或 p.upper；每个 allocation 必须至少一个真实事件，首 event 的 allocation_before_revision=0、before H/S=0、created_xid=allocation.created_xid。

### 5.1 new

baseline=0，window/admitted 非 NULL且唯一window属于control、`start<=admitted<end`。相关业务 marker 必须非 NULL并等 control；allocation.admitted 等原 request/p.pb_admitted_at，created_xid 等原 PB stamp。supplier 还须等 attempt.pb_created_xid，不能用现在 p.state=prepared 认证历史出生。

| subject | origin_source 与首 event | 首 event after H / S / sourceSpent / state |
| --- | --- | --- |
| customer_request | 真实 reservation ID；source_kind=reservation、同 source_id | R / 0 / 0 / held |
| supplier_attempt | 唯一真实 reserved PE；source_kind=procurement_event、同 source_id | U / 0 / 0 / held |

supplier reserved PE 必须同 attempt/account，type=reserved、event_key=`reserve:`+attempt、delta=(U,0)，PE.pb_created_xid 等 allocation/首event xid。new supplier 的真实 PE 集合须全部在该 allocation 唯一镜像（包括零 delta）；不得把遗漏 PE 放进“legacy residual”。客户 reserve 的同事务 stamp 取真实 request，reservation DTO 无独立 xid，不能伪造不存在的列或把其 created_at 强等 admitted_at。

### 5.2 legacy_import

业务 marker=NULL，window/admitted=NULL，origin_source_id=subject_id，allocation/首event xid 等 control.created_xid。首event 必须 legacy_import、source_id=subject、预算 afterSpent=0、afterSourceSpent=baseline。

客户 baseline0，首afterHeld=R、state=held；supplier 首state只接受 held/disputed，baseline 和首afterHeld 必须满足§8的真实 PE 残余守恒。不能用今天 p.state/H/S 补 import 时的状态/桶，不因今天已 settled 否认旧合法导入。supplier 首事件的 held/disputed 属原持久快照，现有原件只能核其封闭接受集和金额守恒，不能据此授历史状态资格。

### 5.3 legacy_catchup

仅 supplier，业务 marker=NULL，window/admitted=NULL。origin_source_id 指唯一同 attempt/account PE；allocation/首event xid 等该 PE.pb_created_xid。首event source_kind=procurement_event、source_id=origin_source，且其真实作用前 sourceHeld=0、sourceSpent=baseline；首afterHeld=PE.dH，首afterSourceSpent=baseline+PE.dS，首afterSpent=max(afterSourceSpent-baseline,0)。初态预算H/S仍为0；不能误用首event.beforeSpent 作为原采购 baseline。

baseline 的来源守恒另按§8。不能新增 customer catchup，不能要求 supplier catchup 的 origin PE 为 reserved；合法 terminal 后 cost correction / disputed 更正保留。

## 6. 全 event 链、来源内容和状态机

每个 control 只消费唯一 seq 集合 `1..N`，无缺号、重复或非法值；相同 seq 不用 ID 排序选历史。按这条全序为每 allocation/window 独立维护计数，event 的 allocation_before_revision 必须等其已消费次数；有窗口时 window_before_revision 等该窗已消费次数，无窗口时必须 NULL。不得把别 control 下同 allocation/window ID 的行混入合法链。

allocation 运行初态：预算 H/S=0、sourceHeld=0、sourceSpent=baseline、state=held、revision0。每 event 的 beforeH/S 必须等独立运行态；expected after 从下表真实源独立推导，不能用原 event 的 after 值倒推 before。

| source_kind | 真实来源与适用条件 | 独立 after sourceHeld / sourceSpent / state |
| --- | --- | --- |
| reservation | customer new 的首事件，真实 reservation 属本 request，等 origin_source | R / 0 / held |
| legacy_import | legacy_import 首事件，source_id=subject，唯一真实 request或p | 客户 R/0/held；supplier 残余H/baseline/首快照held或disputed |
| settlement | customer 第2且最后事件，真实同 tenant/request settlement；此前必须完整 reserve/import且state=held | 0 / charged_amount / settlement.status |
| procurement_event | supplier 同 attempt/account 的唯一真实 PE，event.created_xid=PE.pb_created_xid | prior sourceHeld+PE.dH / prior sourceSpent+PE.dS / 下列PE状态映射 |

settlement PB event 的事务身份只允许使用真实 settlement.journal_id 对应唯一 journal.created_txid 作正向同事务桥；不重放账本行，不把 settlement.created_at、journal.created_at 或 PB event.created_at 当同一个采样。没有可用原journal时不得从别域结果或 event自报补该事实。

PE状态映射固定：reserved/consumption_unknown/recovered_unknown→held，released_no_consumption/expired_without_permit→released，cost_confirmed/cost_corrected→settled，evidence_disputed→disputed。预算 afterHeld=afterSourceHeld，afterSpent=max(afterSourceSpent-baseline,0)；独立 dH=afterHeld-beforeHeld、dS=afterSpent-beforeSpent，均逐值等原 event。after source值不得为负；下调绝对成本跨 baseline 时可产生负预算delta，不能把 `sourceSpent<baseline` 视为错误。

supplier 后续状态转换：held→held/settled/released/disputed；settled→settled/disputed；released→released/settled/disputed；disputed→disputed/settled。首次合法catchup不强套“历史上一笔p终态→新态”的缺失转换；完整采购状态机另属其独立域。零额、零delta、同额更正仍为真实一步，seq/revision/tail都推进。

## 7. 当前三层投影与双重守恒

每个 allocation 的 revision=该allocation事件数、last_event_id=链尾；当前 held/spent/sourceHeld/sourceSpent/state 必须等独立运行尾态。另独立核当前预算H/S等其所有真实event delta之和；不拿今天allocation值作为首态。

每个 window 的 H/S 一方面等所属 allocation 当前H/S之和，另一方面等这些allocation所有真实event delta之和，且等按event全序运行的窗口末态；revision=该窗事件数，tail=最后作用该窗的event，不是control全局尾。空窗合法零态。legacy无window分配不得落入任何窗桶。

每个 control 的 H/S 一方面等全部所属allocation当前H/S之和，另一方面等全部真实event delta之和及独立运行末态；event_seq=N，last_event_id为全control尾（N=0时NULL）。control的held包含所有历史窗口未释放held及无窗legacy held，不能只求当前window；spent包含所有所属分配预算spent，不用当前日期清零。

当前 allocation 另对真实源最终投影：客户 pending 为(R,0,R,0,held)，terminal为(0,charge,0,charge,status)；supplier 为(p.H,max(p.S-baseline,0),p.H,p.S,map(p.state))。这是最终投影比较，不认证历史import/catchup/事件cut。一个依赖缺失/重复时，不可跳过它后用“剩下的总和”宣称投影正确，也不能把失败运行态填0继续通过；只对依赖完备的 expected 比较，同时记录实际受影响投影。

## 8. 真实源的反向集合、legacy 残余与缺失义务

每个 PB event 的真实 source 必须唯一存在并满足§5/6，且 `(control,source_kind,source_id)` 仅一次；每个 allocation 的首事件、客户终结事件与new supplier全部PE均有正反双向集合，不能只验证其 last_event_id。额外同身份原件、重复PK、同ID跨表均不互抵。

客户已有 allocation 且真实 settlement存在时，必须有唯一 settlement effect；没有结算时不能凭伪event创出它。供应商new必须每条真实PE恰镜像一次；legacy_import/catchup只强制首源/首import以及每个已保存PB event正确绑定，不要求全部历史PE都镜像，也不按XID/time/ID猜“控制创建之后的PE”。

对每个 supplier allocation，枚举同 attempt/account 全部真实 PE，减去**唯一、精确绑定本allocation**的 procurement_event镜像集合，形成残余集合，独立求 `residualH=sum(PE.dH)`、`residualS=sum(PE.dS)`：

| origin | 必须满足 |
| --- | --- |
| new | 残余集合为空，不能只因零delta残余求和0而通过 |
| legacy_import | residualH=首import.afterHeld，residualS=baseline；首afterSourceSpent=baseline、首预算spent=0 |
| legacy_catchup | residualH=0、residualS=baseline；首afterHeld-originPE.dH=0、首afterSourceSpent-originPE.dS=baseline |

镜像定位须先核完整物理/业务唯一性、control/allocation/source身份及PE stamp，不能以一个坏或多义event吞掉PE再“平衡”残余。残余守恒只证明数额边界相容，**不能证明哪些未镜像PE真实发生在导入/追补之前**；非空残余的历史effect boundary缺证保留，不授Q，也不调用/扣减旧checker的gap计数。

正向从真实客户/供应商源枚举其唯一当前scope control：marker等control，或客户当前reserved/pending、供应商当前prepared/sending/unknown/disputed时，必须唯一存在对应allocation。缺失时记录现有源/control，不制造不存在allocation键。terminal、marker=NULL、无allocation可以是控制首配前已关闭源：不判错误，不伪证历史absence；若有allocation则全部关系照核。当前无control且marker=NULL不凭空造义务；marker指缺control/错scope则不是legacy豁免。

后续新PP transition/finish如通过其明确source声明要求真实PB效果，仍由已有effect片完整执行；本片的terminal无allocation接受集不能覆盖或豁免该声明。也不把今天存在allocation倒推要求其创建前每个transition都曾有PB效果。

## 9. 错误归属与故障边界

本片所有跨行、集合、序列、数额守恒/当前投影错误沿 `OriginalPointerRows`，统一按真实 `(物理表,自然PK)` 去重；scalar/type/本地union仍由原责任叶独立记。错误不能因另一个叶子已记scalar而当作关系成功，但同物理行多原因不增加本计数。

- event缺/错源或allocation/control归属，记实际event；被其明确声称且真实存在的相关原件、受影响allocation按关系责任记；不存在原件不造键。
- 首源、客户结算/new supplier镜像或正向allocation义务缺失，记真实allocation/业务source及明确承担该义务的control；不把缺失冒记到同文字ID的别表。
- 重复物理/业务身份记该集合全部真实参与者自然键，依赖不能任选；相同PK重复仅一次。受影响投影不能被降为“无事件正常零态”。
- 单独projection值/tail/revision坏记其实际投影行；没有自身错误的同control兄弟allocation/window/event不因遍历顺序被污染。缺依赖使expected无法生成时仍记实际声明者/受影响投影，不向无关合法来源传染。
- context取消、绝对超时、资源失败为执行错误，整 `checkSourceClosure` 返回零结果；不发行半份集合、局部balanced或资格。无网络请求、无PG写入或后台重放。

## 10. 独立验收矩阵

1. tenant/key/procurement三scope、旧tenant合法空TEXT与NULL区别；错tenant/key/account、同scope两control、依赖缺失/重复；同文字ID跨物理表合法而不互抵。
2. UTC日/月、跨年/闰二月、月末、时区offset同刻；end错误、非边界start、错control窗；空window、无event control、highwater等原tracking period/真实maxwindow，远年不经UnixNano。
3. customer双scope new reserve→settled/released、legacy_import→finish、pending不终结；首reserve/import丢失、客户catchup/额外第三步、错reservation/settlement/tenant、伪NULL marker、被指定源重复。
4. supplier new全PE、new漏零deltaPE、normal0额、sameamount correction；marker=NULL旧import/catchup、先关闭后更正、disputed导入、later当前state/H/S与首快照不同仍合法。
5. 三origin逐字段单变异：source/window/at/upper/baseline/created_xid、首event source/after/revision；new首非reserved、import错误control xid、catchup起点非零held、客户catchup均拒；普通created_at非单调不误拒。
6. 每event合法shape内单变异before/after/sourceSpent/state/delta/seq/两revision/xid；同control多allocation/window穿插、跨control错归属、缺号/重复seq/重复effect、不按输入顺序/UUID/timestamp/XID重排。
7. NUMERIC100、负delta、sourceSpent跨baseline降到下方、int64 upper/charge界；独立字面期望核每层投影与最终真实源，不能用写入实现计算fixture；聚合中间不溢出或浮点化。
8. allocation/window/control三层错桶/seq/revision/tail、老窗仍有held与无窗legacyheld、空窗0态；当前限额下降/禁用不否定已保存历史收尾。source当前变化只用于末态比较，不能改历史首态expected。
9. new残余零delta也拒；import/catchup残余守恒正反例、历史未镜像集合只缺证不乱补；terminal marker=NULL无allocation、新control后来出现、完全无PB控制旧源保留接受，已保存新PP效果声明不得豁免。
10. 物理PK重复但业务身份不同、业务键重复但物理PK不同、反向多余event、同实际source被两allocation抢占；逐物理键精确去重、数组排列/多轮map顺序不变，坏归属不污染正确兄弟前缀。
11. 行/字节共享预检、密集event排序/中途context取消、绝对timeout及大金额解析预算；完整入口执行失败零结果；限定fixture/race不作为商业或生产验收。

## 11. 后续仍未完成

完整 PB operation动态snapshot前缀、历史配置/gate/absence、legacy会员/效果真实历史cut、未派生Reconcile request/latest/sent观察、四域完整业务资格和逐对象Qualified/aggregate继续独立。现有原件不能补的历史事实须先设计新不可变观察合同，不改715/482列或旧profile冒充完成。根冻结本合同、再批准实现后才允许新增代码；集成后由根统一更新架构与验证记录并执行只读门禁。

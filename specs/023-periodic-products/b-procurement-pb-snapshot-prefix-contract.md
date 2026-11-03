# B-P4：PB 命令动态快照的精确资金前缀合同

2026-11-04；审查基线 `6c03c15d9cd043a284d2c59c1250afb61a178f7c`。**状态：根集成人已审定冻结；实现与实际验收另记。** 承接[PB 操作与观察](b-procurement-pb-operation-relations-contract.md)、[PB 真实事件关系](b-procurement-pb-event-relations-contract.md)、[PB JSON](b-procurement-pb-json-contract.md)与[原 B-P1](b-prefix-contract.md)。

## 1. 范围、事实源与不变边界

拟新增私有 `internal/procurementprefix/source_closure_pb_snapshot_prefix.go` 及独立测试，入口为同一 `sourceClosureState` 的新叶子。根审定后才实施，公共入口及共享文档由集成人处理。本片不改 32 表715列、40原件482列、原 writer、旧算法、已应用迁移、权限或任何线上路径，不提供公共 `Check`。

真实语义依据：

- [020](../../migrations/020_periodic_budgets.sql) 的 `pb_guard_control/window/allocation/event`、`pb_apply_event`、`pb_view_at`：control 从0开始，全部 PB effect 的 delta 累加至 control；只有有窗 allocation 的 effect 进入对应 window，legacy 无窗 effect 仍进入 control。
- [PBprefix 候选 SQL](../../internal/periodicbudgetprefixschema/materials/candidate-v1.sql) 的 `pb_prefix_operation_capture/window_capture/journal_guard/check_journal`：命令 cut 与 JSON 取同份已锁定 control/window；窗口出生是 BEFORE 时零状态；资金 seq 与观察 ordinal 是两个不同的顺序。
- [B-P1 纯核](../../internal/periodicbudgetprefix/check.go)只用于核对历史接受集和缺证；[当前 operation](../../internal/procurementprefix/source_closure_pb_operation_relations.go)与[event 关系](../../internal/procurementprefix/source_closure_pb_event_relations.go)只用于划分责任和交叉审查。不得调用这三个 checker、它们的报告、内部运行态、Stats、Candidates、Complete 或 Qualified 作为 expected。

本片独立证明“这条命令的动态字段等于它所绑定的这份原 PB effect 历史的精确前缀”，不是重新授予 effect 或命令的业务资格。真实 reservation/settlement/PE 内容、全部 new/import/catchup 来源与最终三层投影仍由 event 关系独立核；完整配置版本/审计内容仍由 operation 关系独立核。本片必须自行核其实际消费的身份、数值、差额、序列、观察来源和出生边，不以其他叶子零错误作前提；原件自身相互一致仍不代替后续逐对象业务依赖闭包。

`SourceClosure.Complete=false`、Qualified 为空保持。历史 scope existence/absence、首配完整成员、legacy effect cut、真实 admission/gate/dispatch/finish 资格以及 B-HO1 不在本片关闭。不得把 fixture 通过写成生产或商业验收。

## 2. 输入、原语、索引与共同故障

### 2.1 同次 raw 与合法值

全部事实只来自本次完整 `OriginalRows` 中九类 PB 原件；不二次读取、拼接独立扫描或读取数据库。允许复用同来源域严格 PB JSON 解码器、规范整数/NUMERIC100、UUID、SQL `Time`、UTC 日/月日历和长度 framing 自然键原语；不得复用其他关系叶子的认证状态。

物理 PK 的非 nil 小写规范 UUID 接受集、旧 TEXT 身份、SQL NULL envelope、finite 微秒时间及相同 instant 比较沿现合同；不新增 UUID version/variant、trim、case folding、墙钟单调或 XID 大小规则。JSON 继续严格12/5字段、原生 bool、UTC6/year0001..9999、规范金额字符串、重复 decoded 键/Unicode 拒绝；result_snapshot 的8192B形状界与单字段65536B资源界不能混用。

seq、ordinal、revision 为规范正/非负 int64。事件 before/after 与所有运行桶为非负 NUMERIC100，delta 为有符号 NUMERIC100，不接受 `-0`、前导零或经 float64/int64 收窄。中间算术使用独立 `big.Int`；每次真实 effect 之后 allocation/window/control 运行桶须在0..10^100−1，不按最终相消结果豁免中间负数或溢位。

### 2.2 多值索引与精确消费

按物理表自然 PK 保存所有原件，再独立建立：

| 索引 | 用途 |
| --- | --- |
| control 的 `(scope,实际 target)` | 排除同 scope 身份的歧义，key 另核 tenant |
| window 的 `(control_id,start_at 实际时刻)` | 找命令 as_of 所属周期的唯一候选窗 |
| allocation 的 `(control_id,subject_type,subject_id)` | 不把同业务主体的两个 allocation 任选其一 |
| event 的 `(control_id,seq)`、`(control_id,source_kind,source_id)` | 资金全序与唯一 effect；不能按同金额挑选事件 |
| observation 的 `(control_id,ordinal)`、`(kind,source_id)` | 完整观察全序及 source 唯一回指 |
| version 的 `(control_id,revision)`、`operation_id` | 实际消费的历史配置三元组 |
| 每 control 的 events/journal、每 window 的 birth、每 operation 的 observation claim | 有界分组、双向来源及出生可见性 |

所有索引均多值，只在恰一行时消费。物理重复不得先去重后让业务索引变唯一；相同 PK 重复行和相同文字 ID 跨表不可互抵。错 control/allocation/window 归属不得混入另一条合法链，缺失/坏/多值成员不能被删掉后冒充空历史。

### 2.3 一份预算与执行错误

复用原 `replayWork`、100000行、16MiB、65536B字段界、context 和原绝对 deadline；不另给本片预算或重置 deadline。索引、依赖扫描、JSON/大整数解析与运算、排序比较/拷贝、前缀推进均 tick；不按自报 MaxInt64 分配数组或展开缺号。

每 control 对 events 按 seq、journal 按 ordinal 各排序一次，线性推进资金和观察，不逐 command 重扫 events/windows。允许有界索引定位，不能以 command×event 的遍历替代。不得排序 caller 原数组或修改输入。

nil work/context 为 `ErrInput`，资源为 `ErrResource`，取消为 `ErrCanceled`，绝对到期为 `ErrTimeout`；阶段前后也检查 context/deadline。首个执行错误结束，完整来源入口返回整个零结果，不输出半份 Stats/资格。原件内容矛盾属于 finding，不伪装成执行错误。

## 3. Covered/legacy 与观察的独立前提

### 3.1 覆盖声明不可降级

只有 control 没有 origin/head/journal，且所属 operation 的 observation_id、window 的 observation_id/observation_created_xid 全为规范 NULL，才是未覆盖 legacy。该分支不虚构 cut、不按今天金额比较动态快照、不因最终0或同金额前缀而认证历史；原 JSON、配置、身份与静态窗口的现有义务保留。旧基础核的独立保守零金额例外不被本片扩大为“有精确观察”。

origin/head/journal 或任一 source 新指针声明覆盖后，缺任一来源、部分 NULL、错误绑定或多值都是 mismatch，不能降为 legacy。origin 必须唯一绑定真实 control、version=1、原 created_xid/tracking；head、全部 journal 及 source 双向闭合。孤立 observation/control 缺失记录实际声明者，不制造不存在的 control 键。

### 3.2 完整观察链与消费绑定

本片自行按 raw 核 covered control 的 ordinal 从1连续至 head，首 previous=NULL、后继指精确前条；head 的 ordinal/tail/updated_xid 等真实末条。空链仅0/NULL且 updated_xid=origin.created_xid。相邻 event_seq 不回退，同 seq 的 control tail 相同；configuration_revision 不回退，但不按 created_at/xid 排列历史。

command journal 必须与唯一 operation 正反绑定：kind、source/command_source、NULL window_source、control/resource、created_xid、observed_at/created_at、configuration_revision/operation.revision 逐值相等。它消费的 `(control,configuration_revision)` 版本唯一且 operation_id 等 journal.configuration_operation_id；no-op 不要求该配置 operation 等本次 command。不得借另一个同内容 operation 或 version 代替。

window_birth journal 与唯一 window 正反绑定：source/window_source/window_id 恰等 window.id，NULL command_source，window.control 同 control；journal.created_xid=window.observation_created_xid；window_revision=`0`、window_last_event_id=NULL、observed_at 为规范 SQL NULL。window 当前非零桶/revision/tail 不能覆盖出生初态。出生 ordinal 不强求晚于它引用的 configuration operation command ordinal，同事务延迟 FK 的合法顺序保留。

完整配置版本链、changed/no-op 和 audit 仍是 operation 片义务；这里实际消费的版本/来源必须自己精确检查，不因并行片已报错而跳过后把该 command 视为成功。

## 4. 真实资金前缀及计算式

### 4.1 事件成员与运行初态

事件按自己的真实 control_id 分组；加入数值计算前必须唯一物理 PK、唯一(control,seq/effect)、唯一真实 control 与 allocation、allocation.control=event.control，以及唯一业务 allocation 身份。allocation.window_id 非 NULL 时，window 必须唯一、同 control 且日历正确；NULL 时不进入任何窗口，不拿同文字 ID 的其他表行替代。

每 control 的原件事件 seq 必须是 `1..N` 完整连续集，不按 ID/time/XID 补序。实际命令 cut k 只消费 seq≤k；k=0 只接受 NULL tail，k>0 必须精确引用 `(control,k)` 唯一原 event 的 ID。不能把另一个同金额 cut 或当前 control.event_seq/tail 作为该命令的 cut。

独立维护每 allocation 的 H/S/revision，初态0/0/0；核 event.allocation_before_revision、before H/S 恰等运行前态，after H/S 为规范非负 NUMERIC100，delta 恰等 after−before，再推进到 after。窗口非 NULL 时 event.window_before_revision 恰等该窗此前事件数；无窗口时为 SQL NULL。此处不拿 allocation 今天的 H/S/revision 作初态或 expected，也不从 raw after 倒填此前状态。

按精确 delta 同时维护：

```
controlHeld(k)  = Σ(e.held_delta  | e.control=C, e.seq≤k)
controlSpent(k) = Σ(e.spent_delta | e.control=C, e.seq≤k)
windowHeld(W,k) = Σ(e.held_delta  | e.control=C, allocation.window=W, e.seq≤k)
windowSpent(W,k)= Σ(e.spent_delta | 同上)
windowRevision(W,k) = 同上事件个数
windowTail(W,k) = 同上最后一条真实 event.id；空集为NULL
```

controlSpent 参与前缀非负/NUMERIC100检查，不是 snapshot 新字段。零 delta、零金额、同额更正仍使 allocation/window 计数和真实 tail 前进；负 spent_delta 及跨 baseline 的合法下调不能被拒绝。legacy 无窗分配的 held 进入 control/carry，不凭今天状态重建历史 baseline 或 import 时间。

事件的真实客户/采购来源映射和 afterSourceSpent/state 业务推导由 event 关系另核；本片不读取其结果，也不以仅内部差额一致宣称业务 effect 合法。这个职责划分不能写成整份 SourceClosure 已闭合。

### 4.2 坏前缀不能跳过，合法早期 cut 不被后缀金额污染

先保留全部原事件及其声明归属，再校验不可变锚点；坏归属不能污染另一个 control 的合法兄弟链，也不能被直接删除使声明 control 冒充无事件。对可规范定位 seq=n 的缺号、重复、归属或数值错误，从该位置起不再派生可信金额；后面金额即使相消也不能恢复认证。可靠位于 n 之后的坏金额不影响 n 之前已经完整的前缀。

无法规范定位的 seq、相同物理 PK 跨多个声明归属等，使相关 control 的事件集合无法确定；不得任选或生成可信前缀。错误标实际坏行、明确受影响的声明者及需要该坏前缀的 command/journal；不把无关 control/window/operation 当作坏行。观察链不完整或窗口出生边歧义时，同样不能用剩余成员派生“窗口不存在”。

## 5. 窗口存在、出生与命令动态字段

以 control 不可变 period_kind 和 operation.created_at 的实际时刻，独立算 UTC 日/月周期起点；窗口同(control,start)至多一行，start/end 必须为真实自然日/月半开边界，不用本机时区、UnixNano、固定30天或今天 highwater 推历史。

### 5.1 两种全序共同决定可见性

| as_of 周期的最终原 window | 相对出生观察 | 命令当时 expected |
| --- | --- | --- |
| 没有该周期 window | 无可引用 birth | command window三列NULL、snapshot.current_window=null |
| 有唯一合法 window/birth | birth.ordinal < command.ordinal | 该 window 已存在，按 command.event_seq 精确算前缀 |
| 有唯一合法 window/birth | birth.ordinal > command.ordinal | 当时不存在，command三列NULL、JSON null |
| 同 ordinal、缺/多 birth、错源/错control、重复(control,start) | 无可信顺序 | mismatch，不以当前window存在替代 |

每条 window effect 的 seq 必须严格大于其真实 birth.event_seq；birth cut 不可包含自身窗口 effect。由此同资金 cut 的 `birth→command` 可见零窗，`command→birth` 必为 null，不能仅看 seq 或金额0判断存在。窗口出生、command、event 的 created_at/xid 大小都不补这条顺序。

### 5.2 精确 expected 与双向比较

当窗口存在，journal.window_id 恰等唯一 W；window_revision 恰等上式个数；window_last_event_id 恰等真实窗口尾（revision0时NULL，正数时非NULL）。只找到某个≤cut的旧同窗 event 不够，必须是最新尾；control 全局尾也不能替代窗口尾。

snapshot.current_window 必须恰为 W 的 id/start/end 与 `windowHeld(W,k)`、`windowSpent(W,k)`。日历来自 W 与不可变 period，金额来自真实 effect 前缀；不得比较 W 今天的金额/revision/tail。

当窗口不存在，journal.window_id/window_revision/window_last_event_id 全NULL且 JSON null；window held 取0，不能从后来出生的 window 借值。两分支均精确核：

```
carry_held = controlHeld(k) − (当时存在W ? windowHeld(W,k) : 0)
```

carry 必须非负并满足 NUMERIC100，snapshot 字符串逐值等规范结果。它包含其他历史窗口未释放 held 和无窗 legacy held；不是 control今天held、当期window held或当前日期下的重算值。as_of 属另一周期、未来 event、后来配置/出生、同金额其他 cut 均不得改变旧回执 expected。

## 6. Finding 归属、集成与保留缺证

本片不新增公开 Stats/DTO/资格。scalar/type/严格JSON非法按实际表/自然PK记 `InvalidScalarRows`；物理重复沿 `DuplicateRows`；关系、序列、前缀和快照内容错误按实际 `(物理表,自然PK)` 去重记 `OriginalPointerRows`，不因其他叶已报错而视为认证，也不按原因重复增加同一行。

- 仅 snapshot 动态金额错：记实际 operation；仅 journal revision/latest-tail/cut错：记实际 journal。两边独立错误各记自己；不把正确 window 当前投影写成错误来凑计数。
- command/source 双向身份、window存在声明或出生边错：记实际 operation/journal及真实存在、明确承担该关系的 window/birth；缺目标只记实际声明者，不造缺失行键。
- 事件归属、差额、before/revision、集合错误：记实际 event/明确参与的 allocation/window/control；需要坏前缀而不能获得 expected 的命令记实际 operation/journal，不能填0继续通过。错误后缀不污染已完整的早期 cut。
- 多值身份：记全部真实参与者与实际消费者，不能任选一行、只报首行或把业务重复误写成物理重复。跨表同ID继续分别归属。
- 无覆盖 legacy 是缺证接受集，不是关系通过后的资格；有覆盖声明但不完整是 mismatch。没有 origin 的历史被外部完整删掉，raw 无法区分真实legacy，仍依赖完整读取/catalog/immutability责任。

根集成人接线后，Unimplemented 可准确改述为“已实现 covered command 的动态原PB effect前缀；legacy精确观察、历史scope existence/absence/首配成员及逐对象资格仍缺”，不能删除全部 PB 历史门禁。公共入口、统一架构、HLD/LLD与验证记录由根汇总全部并行结果后语义复核并刷新；本片代理不自行刷新架构或 Git。

## 7. 独立验收矩阵

独占测试不得用现有 PB writer、SQL codec、旧checker或其他关系叶子生成 expected；手算原件/固定JSON及独立常量为依据。可复用通用 test context/物理装载原语，不以“另一叶零错误”作为本片断言。

1. 空 Rows；covered control无event、无window的cut0；无command的合法origin/head空链；三scope、changed/no-op、UTC日/月、跨年闰月、offset同刻和同周期墙钟回退。
2. 手算多allocation/多window交错：旧窗held、当窗held/spent、legacy无窗held；相邻不同cut恰同金额，交换cut/tail必须拒绝；未来event不改变旧命令expected。
3. window birth与command同cut的两种ordinal顺序；空窗before/after、后来窗已有非零终态；同事务birth早于所引用配置command合法；出生cut包含自己effect、相同ordinal、漏birth拒绝。
4. window revision与control seq不同、全局tail与窗口tail不同；旧同窗tail虽然真实且≤cut仍须拒绝；零delta/零金额仍推进revision/tail。
5. 合法负spent delta、跨baseline向下更正、NUMERIC100大于int64、100位边界；101位/中间负数/中间溢位/`-0`等拒绝，不因末态相消通过。
6. cut0错tail、正cut缺/错tail、断seq/重复seq/非法seq、event before/after/delta/分配及窗口before revision变异；坏前缀不跳过，可靠坏后缀不污染更早cut。
7. 缺/重复物理PK和业务身份、同ID跨表、foreign allocation/window/control、正确兄弟control不被错锚点污染；重复输入及排列多轮得到相同实际错误键集合。
8. origin/head/journal/source任一孤立、部分NULL、错kind/source/xid/config version/observed_at；legacy完整NULL保留，legacy非零快照不凭今日投影拒绝或认证；不可把坏covered降legacy。
9. 严格JSON重复decoded键、坏Unicode、未知/缺字段、bool/null/string错型、UTC6与8192/65536界；独立断言scalar与pointer归属，单值变异不盲目增加正确window计数。
10. 共享资源边界、已取消context、绝对超时、阶段后取消、输入不变、长交错多command/event的线性推进；执行错误从完整入口为零结果。专项race通过后交根作入口回归和独立反例复验。

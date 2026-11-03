# B-P1：命令 snapshot 的精确前缀与窗口出生来源

2026-11-03，基线 `83734076223ffe80d460dd255b45c4f9362e972f`。本合同先于候选实施冻结。它回应 [C0 运行验收发现的缺口](../022-self-service-payments/c0-runtime-pb-evidence-gap.md)，沿用 [B 原核验合同](b-verifier-contract.md) 的资金、配置、来源与三态边界。

## 1. 目标、版本与交付依赖

本片补 `OperationSnapshotPrefixUnproven` 所缺的实际观察前缀和窗口存在顺序。资金 `event_seq` 继续只排序已应用的资金 effect；另一个共同 ordinal 只排序 `window_birth` 与 `command`，包括 no-op。不把 event BEFORE 当成资金 AFTER 已生效，不用时间或 xid 大小排序。

本片不扩大原 §9 的历史 cap/admission 配置认证范围。`LegacyMembershipUnproven` 和 `LegacyEffectBoundaryUnproven` 独立保留；新安装先经营再首配同样可能产生它们，不能将 fresh install 等同历史完整。

实施依赖依次为：

1. 本合同、独立候选 SQL、纯前缀算法及 restricted actual APP 的专库语义验收。
2. 精确 001–022 的同事务迁移核验/材料/安装与授权隔离，保留旧 API、旧 roots、旧 golden。
3. 新显式 scanner/basis/catalog/report profile，所有新增物理来源进入同次完整读取与共享预算；受保护 pre/post、owner 核验与 APP runtime 一起接入。
4. 最后发布 append-only 正式迁移和 provision/startup，审定历史 manifest/basis/tuple 到新 current 的完整映射，并完成经营验收。

候选放在 `b-prefix-candidate.sql`，不放入 `migrations/*.sql`，不由 main 自动安装。第 1 步不是关闭 R1-B 或 R2，也不是给旧经营历史补证。后续各依赖仍是完整目标的一部分。

## 2. 数据与不可伪造的来源

候选新增三张关系；UUID 均为非零规范文本，ordinal/revision/seq 为规范 int64，stamp 为原 top xid8。表名以 SQL 为准，逻辑字段冻结如下：

| 关系 | 字段与含义 |
| --- | --- |
| origin | control 主键；version=1；原 control.created_xid；原 tracking_started_at |
| head | control 主键及 origin 关系；ordinal（初值 0）；last_observation_id（初值 NULL）；updated_xid |
| journal | fresh ID；control；ordinal/previous；kind=`window_birth` 或 `command`；原 source ID；实际资金 event_seq/tail；configuration revision/operation；所观察 window ID/null、window revision/tail；原 created_xid；command observed_at（window 为 SQL NULL） |

operation 新增 nullable `observation_id`；window 新增 nullable `observation_id` 与 `observation_created_xid`。旧记录保持 NULL，不回填出生时序。新覆盖 source 的 pointer/stamp 不可变。journal 可增加由 kind 派生的两种 nullable source ID 作为复合 FK 目标，但不得形成调用方可选择的第二份真值。

control 的最后受审 BEFORE INSERT 阶段，在旧 guard 已覆盖创建 stamp、tracking 与零状态后，无条件创建 origin/head。origin 绑定原 control ID/stamp/tracking，双向延迟检查；不是任意现有 control 的自报 enrollment。APP 预插 origin/head 不能被采用，必须与自动无条件 INSERT 冲突或在提交时因孤立关系拒绝。

候选安装前已有 control 不自动生成 origin。其后命令/窗口继续旧行为，新增 source 字段强制 NULL；新报告对此明确缺证，不能因元数据缺失回退为完整通过。后续若支持旧 control 的受控升级 anchor，必须另冻精确成员集合、锁/排空和已证明部分的衔接，不能冒充原出生或消除旧 gap。

## 3. SQL 线性化与直接 APP 边界

所有函数为固定 schema/search_path 的 invoker。没有 SECURITY DEFINER，不信任 caller GUC、trigger_depth、传入摘要、预造 journal 或当前余额。

operation 增加早于原 guard 的 BEFORE 阶段，按现有 Commander 的 scope 锁序取前层锁后再锁 control：tenant 为 tenant→actor；key 为 tenant→membership→actor→Key；procurement 为 actor→account。再由原 guard执行权限、CAS、规范输入和指纹检查。锁等待结束后以新的 SPI 语句读取当前原件，不使用等待前的旧值。

在原 operation guard 后，最后受审 BEFORE 阶段从**同一份已锁定 control/window 值**生成覆盖后的 result_snapshot 与 journal cut，created_at 只用原 guard 的一次数据库时刻。12 字段/窗口 5 字段 wire 与旧格式保持。不能把较晚读取的 cut 绑定未经同源证明的旧 STABLE 结果；原 GET 用 `pb_view_at` 保留。窗口不存在时保存明确 SQL NULL 和 JSON null。

window 的最后受审 BEFORE INSERT 在旧 window guard 之后捕获出生 cut，强制初始 window revision=0/tail=NULL，DB 生成 source pointer/stamp。随后不得有能追加 PB 动作的受审 BEFORE trigger。journal 在此时直接持久 INSERT，使用延迟 FK 支持 source 还未成为可见行；不在 AFTER 时重读当前图并称为出生/观察前缀。

每次 source BEFORE 无条件生成 fresh journal ID 并 INSERT，绝不 ON CONFLICT 采用已有 marker。正常顺序 control→head；head guard 已持自己的 tuple lock，不再取得 control 的上游行锁。所有 head UPDATE 必须对应当前事务新 journal，严格 old.ordinal+1、精确 previous/control/id/stamp；禁止跳号、倒退及任意 no-op。

source↔journal 必须有 kind 专属、source/control/id 全匹配的双向 DEFERRABLE FK/唯一约束及原事务资格检查。每个受覆盖 source 恰一份，journal 恰回指一个 source；head 必须到达它且末尾完全一致。手工 INSERT journal/head 得到的权限只供 invoker trigger 使用，不能提交孤立、预造、跨域或错误来源。origin/journal 以及 source 回指禁止 UPDATE/DELETE/TRUNCATE；head 只允许精确前进，无 DELETE/TRUNCATE。

不能承诺任意 APP 先持任意行锁的 SQL 都无死锁：UPDATE executor 在 BEFORE 前已锁目标行。支持入口保持原锁序；直接 SQL 的 40P01/40001 属已知回滚，重试限于原合同，unknown COMMIT 只读原操作，不重发命令。多行 INSERT、CTE、表达式/RETURNING 副作用须实测；安全拒绝的失败不能留下可提交缺证 source。

## 4. 纯算法与完整性

新增独立 `internal/periodicbudgetprefix.Check(ctx, Rows)`，不改旧 `fundinglotverify.CheckPeriodicBudgetGraph` 或 owner schema v1。Rows 包含原完整 `PeriodicBudgetGraphRows`、origin/head/journal，以及 operation/window 的新增持久字段投影。新报告 scope 固定 `periodic_budget_snapshot_prefix_v1`，仅计数；不是授权、数据库资格或 RuntimeProof。

新 Checker 自行调用原完整算法，并原样保留 Base 报告；不接收 caller 已算报告。新报告分别给出结构/来源/链/cut/snapshot mismatch、未覆盖 control 与保留的 legacy membership/effect 缺口、实际已认证命令数。只有原 mismatch 全零、新 mismatch 全零、所有 control 有合格 origin 且每个 command/window 完整认证、另外两类 legacy gap 全零，才可 balanced/coverage_complete。原 Base 中的 operation gap 只可由逐命令新来源与精确重放证明在**新 scope**中解决；Base 原结果不改。声明覆盖域缺 witness 是 mismatch；无 origin 的旧域始终不能取得完整新报告。

必须逐项验证：

- origin 与原 control stamp/tracking 相等；head/origin/control 完整双向，无孤儿或重复。
- source pointer、kind、control、原 top xid 与 journal 全等；command 的 observed_at/config revision/operation 对应原 receipt/version。
- 同 control ordinal 从 1 连续、previous 与 head 闭合；资金 cut 单调不减，同 seq 的 tail 相同。cut=0 仅 tail=NULL，其他 cut 必须找到同 control 精确 seq 的真实 event。
- 每个引用 window 的金融 event.seq 必须大于该 window birth.cut。不能让出生 cut 包含自己的窗口事件。
- command 时窗口是否存在由 birth.ordinal < command.ordinal 决定，不由今日终态、时间或仅金融 seq 决定。相同资金 cut 下，先建空窗和后建空窗必须可区分。
- 按每个 command 的唯一 cut 重放：control held 为全部 events<=cut 的 held_delta；对应 window held/spent 为该窗前缀 delta；carry=control held−该窗 held；window revision/tail 为该窗前缀事件数/真实尾。后续事件不影响旧回执。不得搜索恰好同金额的任意前缀。
- 原严格 12/5 JSON 字段及金额/UTC6格式逐值检查继续成立；sequence/金额坏前缀不可跳行后继续派生。按 control 排序一次，扫描 events/journal 一次维护窗口累计量，不能每 command 重扫全表。

Rows 的所有数组（含来源投影）合计最多 100000，全部 string/bytes/时间按原纯层规则共享 16MiB，单字段 65536；JSON另沿原 8192。不是再给 witness 一份16MiB。时间按32B、bool按1B，pointer nil不计bytes。新增来源投影的重复索引字段保守计入此纯层逻辑预算；不把它称为物理 transport 计量。未来 scanner 仍须另在同一次真实读取中对全部物理列共享原预算，不能拼两个独立扫描。

nil ctx、资源、取消和 deadline 错误固定安全码并整个零结果；阶段前后同时核 caller context 与绝对 deadline。Checker 不保存输入，不输出原件 ID/正文，不写数据库。

## 5. 验收与升级门禁

专库只允许 `127.0.0.1:55439/moni_test` / `moni_test`；随机新 schema 和 restricted 实际 APP LOGIN，不用 SET ROLE 代替。候选明确固定 ApplyThrough(...022)，再原 provision 和额外最小候选授权；不修改旧 installer/profile/golden。

必验：三 scope 原 Commander 首配/changed/no-op；真实 reserve/settle/release/更正与跨窗 carry；同金融 cut 的 window→command 和 command→window；同金额不同 cut；同 tx 多动作；多行/CTE/RETURNING副作用；RC 开始早于并发提交而等待锁晚于它的情况，cut/window/JSON同时看到正确前缀；savepoint、整 tx 回滚、死锁/未知提交只读。

actual APP 直接 SQL 反例：预造/孤立/重复 journal/origin/head、跨 control、错 kind/source/xid、伪 pointer、跳号/head回退、旧 window 冒充出生、缺 source/缺 witness/错误cut/tail、grant越界与持久 immutable 字段修改。owner 只负责 DDL、catalog、显式腐败与清理，不能代替实际业务 LOGIN。

纯层有独立手算正例、全部字段变异、错误前缀后缀、空表/legacy边界、资源/取消/race/fuzz。原 v1 负例、三类 gap 与旧源码 pins 保持。

新增表列、权限与函数正式发布前必须完成新 profile 的完整静态 descriptor、readset、basis、manifest 和历史→current 映射。V2 wire 可继续采用原 v1 framing 和另审的新 descriptor；若变更 BasisVersion/framing，必须另建 codec，不能扩大旧 DecodeV2 接受集。原历史字节不重签，old roots 仍严格拒绝新 schema。R2 在线资金 ready 继续关闭，直到完整经营图及所有运行入口一起通过。

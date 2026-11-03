# B-P4：PB 操作、配置与观察来源关系合同

2026-11-04；审查基线 `e73d9e9`。**状态：根已逐条审定并冻结本片范围；实施与验收结果另记。** 承接[来源实施合同](b-procurement-replay-implementation-contract.md)、[PB JSON 合同](b-procurement-pb-json-contract.md)、[原 PBprefix 合同](b-prefix-contract.md)与[采购收口计划](b-procurement-replay-closure-plan.md)。本片不改变32表715列、40原件482列、已应用迁移、旧算法、writer、权限或公共入口。

## 1. 事实源、职责与调用边界

事实源按以下原件交叉核对，不把旧 checker 的实现细节提升为新的业务权限：

- [原40类 DTO](../../internal/procurementprefix/original_types.go)：直接使用本次完整 raw `OriginalRows`，包括仅ID的 Tenants/Users、APIKeys、ProcurementAccounts，以及九类 PB 表。没有角色、status、membership 或安装时点投影，不推导这些历史值。
- [020](../../migrations/020_periodic_budgets.sql)：`pb_guard_control`、`pb_guard_operation`、`pb_guard_configuration_pointer`、`pb_guard_version`、`pb_record_operation`、`pb_check_configuration` 的真实配置、CAS、版本、回执和审计合同。
- [PBprefix V1 SQL](../../internal/periodicbudgetprefixschema/materials/candidate-v1.sql)：出生时 origin/head，operation/window 的最后 BEFORE capture、两种 source 双向 FK、generated discriminator、head/journal/cut 的精确关系。
- [原 PB 纯核](../../internal/fundinglotverify/periodic_budget_graph_commands.go)、[PBprefix 纯核](../../internal/periodicbudgetprefix/check.go)和[动态快照解码](../../internal/periodicbudgetprefix/snapshot.go)：核对旧接受集与仍保留的缺证，不调用它们作为本片 expected oracle。

拟新增私有来源叶子 `pbOperationRelations`，直接加入同一次 `sourceClosureState`，复用唯一 `replayWork`。具体文件与接线须由根冻结后另行授权。本片独立核配置版本、回执的不可变内容、原观察来源/完整链与窗口出生关系；不得依赖其他域的 Stats、Candidates、Complete、Qualified，亦不得调用 Commander、writer、数据库或 SQL codec 生成 expected。可复用同来源域已经审定的严格 PB JSON 解码器、规范整数、时间和自然键原语。

本片不重放 allocation/event 的完整金额状态机、当前窗口/控制金额投影，也不核命令快照的动态 carry/window held/spent 前缀。事件/分配与当前投影由独立 PB event-relations 片负责；命令动态金额前缀仍须后续独立切片。双方直接核各自消费的 raw，不把对方报告当作认证。配置/观察关系通过不证明 admission/dispatch/finish 的真实历史 control 存在/absence、完整首配成员、历史 gate 或业务 cause；[B-HO1 草案](b-procurement-historical-observation-design.md)仍未实现。`SourceClosure.Complete=false`、Qualified 为空、无公共 `Check` 保持。

## 2. ABI、索引与共同失败语义

### 2.1 物理值与 JSON

- 原 PB UUID 接受集为小写规范、非 nil 的36字节文本，不强制 UUID version/variant。仅原 DDL 已要求 UUID 的列采用它；tenant、Key、account、actor 等原 TEXT FK 不追加全局 UUID/trim/非空限制。特别是 actor 保留合法旧 TEXT、空字符串、多字节和换行，并按原字节查 Users 身份；当前角色不代表历史角色。此规则不放宽PB wire已明确的scope/scope_id UUID约束，任一实际字段仍须同时满足自己适用的合同。
- revision、ordinal、seq 与窗口 revision 为规范非负或正 int64，边界依其原列；xid8 为规范0..18446744073709551615，仅证明精确事务归属，不按其大小排列历史。配置 limit 为0..MaxInt64；control/window 累计及 JSON carry/held/spent 为非负 NUMERIC100，event delta 为有符号 NUMERIC100。精确大整数，不经 float64 或收窄 int64。
- SQL 时间保持原 `Time` 的 Valid/InfinityModifier/微秒 envelope 和 finite 范围。两个非 NULL 时间按相同 instant 比较，Go Location 不影响相等。NULL 必须是规范 NULL envelope；不把零时间吞成 NULL。JSON 时间沿[PB JSON 合同](b-procurement-pb-json-contract.md)的 UTC6、year0001..9999，不套采购旧快照允许 offset/RFC3339Nano 的接受集。
- `safe_input/result_snapshot` 的严格4/12/5字段、bool/string/null、Unicode、重复 decoded 键、路径、指纹与输入/本行绑定继续沿已有 PB JSON 合同。本片重用其纯解码结果并逐值核原件，不能只看前一叶子计数为0。result_snapshot 保留8192B形状界；safe_input 没有新增8192B存储限制；任一字段超过65536B仍为资源错误。
- 不比较操作/版本/窗口出生之间的墙钟大小，不要求 operation.created_at 等 tracking_started_at，不要求同窗 as_of 晚于 tracking，不用今天的高水位反判旧操作。配置历史按真实 revision、观察历史按真实 ordinal、资金 cut 按真实 event seq；三者不可替换。

### 2.2 唯一性与预算

按物理表自然 PK 建多值索引：control/window/observation 为ID；operation 为 operation_id；version 为(control_id,revision)；origin/head 为control_id。另对 scope 身份、version.operation_id、(kind,source_id)、(control_id,ordinal)、(control_id,event.seq)、(control_id,window.start_at)、PB审计 target 建多值索引。只有恰一行才能消费；重复不得任选首行/末行，也不得用相同金额或相似时间替代身份。

所有索引、排序、关系遍历、整数/JSON处理共享现100000行、16MiB、单字段65536B、context和原绝对deadline；不复制一份预算，不逐command重扫全events。可先按control分组并预解析seq/ordinal，排序一次、线性走完整链。坏行或多值不能丢弃后冒充空历史；受影响组不产生可信派生结论，但不抹掉其他物理对象的确定错误。

入口与阶段前后检查取消和deadline，长循环/比较按共享tick执行。nil work/context为ErrInput，资源为ErrResource，取消为ErrCanceled，到期为ErrTimeout；错误由完整来源入口返回整个零结果，不返回部分Stats或局部资格。原件内容矛盾为确定finding而非执行错误。

## 3. Control、配置版本与操作的完整关系

### 3.1 Control 身份和当前配置

三scope的NULL联合依原DDL：tenant仅tenant_id非NULL；key同时tenant_id/key_id非NULL；procurement仅account_id非NULL。每一scope自然身份至多一个control。tenant原ID、Key所属tenant、account原ID及actor原ID的存在性只从现有相应ID投影精确核验；不要求这些旧TEXT本身符合新Commander的UUID输入限制，不推导角色/status/撤权。

control的period_kind不可变，tracking/effective按UTC自然日/月独立核：effective恰为tracking所属期的下一边界。窗口日历亦须与同一原control period精确一致；这项核对不能借event片已通过替代。control当前revision、enabled、limit_amount、configuration_operation_id必须逐值等完整配置版本链的末项，不能拿当前配置填旧operation。

### 3.2 Version 完整链与 changed/no-op

每个control恰有revision=1..control.revision的连续完整版本链，不允许缺版、额外未来版本、重复自然PK或一个operation被多个版本采用。检查长度/相邻revision使用有界输入和精确算术，不根据自报MaxInt64分配数组或展开数十亿空项。

每个version必须唯一绑定自己的原operation：operation.resource_id=control_id、operation.revision=version.revision、actor/created_xid/created_at逐值相等；version的period_kind等该control不可变period，enabled/limit_amount等操作输入和结果的历史配置。首版created_xid等control.created_xid；首版时间不强等control.tracking_started_at。后续版本相对前版必须确实改变enabled或limit_amount至少一项，period不变；不能通过无变化的版本推进revision。

每个operation必须唯一找到resource control及(resource_id,revision)版本，并依据**整个version.operation_id反向集合**决定changed：

- changed：恰一version用本operation_id，并且就是该resource/revision；expected_revision=revision-1，revision=1时expected=0。其历史配置等该版，自己的operation_id即该版configuration_operation。
- no-op：没有version用本operation_id；expected_revision=revision，配置等该revision已有版本。结果revision=MaxInt64、expected=MaxInt64合法；不得制造新版本或用本operation替换原configuration_operation。

MaxInt64合法只描述no-op算术接受集，不豁免完整版本链；受100000行资源界限制的稀疏自报MaxInt64历史仍须因缺版失败，不能用这一边缘值绕开完整性。存在但绑定错误/多值的version不能被当作“没有version”降成no-op。配置与版本历史对origin缺失的legacy control同样必须核验；legacy只豁免新增观察来源覆盖，不豁免原020合同。每个version反向消费恰一真实operation，每个operation只消费自己的revision配置；operation与control当前configuration_operation_id不普遍相等。

### 3.3 Operation 不可变快照与审计

operation.kind/scope/target从真实control三scope独立构造，精确对照已有规范路径。snapshot.scope/scope_id来自control不可变身份，revision等operation，period/enabled/limit等其真实历史version，tracking/effective等control不可变calendar，as_of等operation.created_at；state按该历史enabled及as_of/effective推导。JSON结果的current_window非NULL时必须唯一绑定同control、同as_of所属周期的真实window，id/start/end逐值相等；这只证明所声明窗口身份，不能在legacy上从今天存在的窗口强推当时已存在。

全部operation须恰有一个action=`periodic_budget.configured`且target=operation_id的audit；反向每条该action的audit恰消费一operation。actor与created_at等operation；key的tenant_scope等control.tenant_id，其他scope为SQL NULL；request_id为空字符串。sanitized_diff恰五字段：control_id、revision、scope、scope_id是相应原值的JSON string，changed是按§3.2真实版本集合推导的JSON boolean；严格decoded键/Unicode规则沿PB parser，不接受未知/缺失/重复/错误类型。不能把audit中自报changed作为判断依据。

该PB audit没有新的采购root来源，不伪造六列B-P1 pointer、root/step/xid，也不要求raw audit未投影的事务stamp。其他action不在此封闭范围，沿原合同保留。

## 4. Origin、Head 与两种来源的双向精确关系

### 4.1 Covered 与 legacy 接受集

有origin即声明受PBprefix覆盖：origin唯一绑定真实control、version=1、created_xid与tracking_started_at逐值等control；须有唯一head，且本control全部operation/window必须有完整新增来源。origin/head/journal任一孤立、部分字段、缺witness或错误binding都是mismatch，不能因origin本身不合法而降成legacy。

只有control没有origin、没有head/journal声称其覆盖，并且全部operation.observation_id及window.observation_id/observation_created_xid均为NULL，才接受为**未覆盖legacy观察边界**。旧control在候选安装后新产生的operation/window也继续NULL；不能为它回填origin、空head或窗口出生。该分支不证明历史、安装前后关系或完整消费，只保留原020能证明的正向关系。若整个覆盖簇都不存在，raw本身不能分辨真实legacy与被外部删光的历史；不可变guard、catalog/basis与完整读取责任仍独立，不给其native资格。

scope未配置并没有control/origin载体；不得把查无control/event解释为某次业务读时absence，亦不在这里生成B-HO1 scope origin。

### 4.2 Command 双向绑定

covered operation的observation_id恰指一journal；journal为command、source_id/command_source_id恰为operation_id、window_source_id为NULL；control_id=operation.resource_id、created_xid=operation.created_xid、observed_at=operation.created_at、configuration_revision=operation.revision。反向每个command journal也必须被该唯一operation以自己的observation_id认领，不能额外孤立journal、跨control复用或将window_birth当command。

journal.configuration_operation_id恰等(control_id,configuration_revision)版本的operation_id。changed时才等本command.source_id；no-op应指已有版本operation，不能强等本次新operation。每条journal均核此版本三元组，不能只核某个operation ID存在。

### 4.3 Window birth 双向绑定

covered window.observation_id与observation_created_xid须同为非NULL，唯一指向window_birth journal：source_id/window_source_id/window_id恰为window.id，command_source_id为NULL，control_id=window.control_id，created_xid=window.observation_created_xid，window_revision=`0`、window_last_event_id=NULL、observed_at为规范SQL NULL。反向每条window_birth被此唯一window精确认领，不能用今天window的非零revision/tail/金额覆盖出生0/NULL初态。

window出生没有原created_at可供相等比较，不拿window.start_at冒充出生墙钟。journal.created_xid与window.observation_created_xid相等，但不要求它等control.created_xid或其他业务event xid。PBprefix原source-before与延迟FK允许一个window_birth先于同事务配置operation回执成为可见行；不得额外要求所引用configuration operation的command ordinal一定早于window_birth。

## 5. 完整观察链、真实cut与窗口存在性

每个covered control的journal按ordinal从1连续到head.ordinal，首条previous_id=NULL，之后逐条精确等前条ID且control相同。head.last_observation_id等真实末条，updated_xid等末条created_xid；空链只允许ordinal0/tailNULL且updated_xid=origin.created_xid。有物理成员但排序/解析失败不能当空链。

每条journal的event_seq/tail必须精确绑定本control真实事件前缀：0仅NULL；正seq必须唯一找到同control(seq,id)的真实event。相邻journal的event_seq不得回退；相同seq的tail相同。configuration_revision不得回退，并逐条绑定§3的真实version/operation；不按CreatedXID或ObservedAt排列。完整事件链、三层金额与当前投影仍由event片独立核，不能因本片找到某tail就宣称整个金额前缀合法。

window相关的每条真实event，通过它唯一allocation.window_id识别所属窗口。covered window的birth journal必须存在，且该event.seq严格大于birth.event_seq；不得允许窗口出生cut已经包含自身窗口效果。allocation/window缺失或多值不任选。这里只核真实相对边，不替代allocation source与金额规则，也不要求event.created_at晚于window.start或birth。

command在as_of所属周期的窗口是否存在，由唯一真实window_birth.ordinal相对command.ordinal决定：

- birth在前：command.window_id必须是该window；snapshot.current_window非NULL且身份/calendar与该window相等。
- birth在后：command窗口三列全NULL，snapshot.current_window为JSON null；即使今天window已有金额也不能倒填。
- 最终没有该周期window：command窗口三列全NULL且snapshot窗口为null；不能据此发业务scope absence资格。
- 相同ordinal、缺birth、重复(control,start)、错window identity均为mismatch；同event_seq下先birth后command和先command后birth必须区分。

非NULLcommand窗口的window_revision/tail，最终须等该window在command.event_seq前缀中的真实事件数/尾，NULL/0联合沿原合同。本片对非NULL尾仅要求它恰为同control、通过唯一allocation归属该window且seq不超过command.event_seq的真实event；这不证明它就是当时最新尾，也不证明revision等真实事件数。**完整动态前缀计算及snapshot carry/window held/spent逐值核验不属于本片**，不能仅确认tail存在就标为已证明；保留完整动态快照前缀Unimplemented。不得用今天window.revision/tail或今天control held比较历史command，也不得搜索金额相等的任意cut。

## 6. 计数、集成与保留缺口

不新增公共统计、DTO或资格。沿SourceClosure既有计数和(物理表,自然PK)去重，同ID跨表不互抵：

- 物理形状、数值/时间/JSON内容非法由InvalidScalarRows负责；相同raw物理PK重复由DuplicateRows负责。新audit严格JSON也按其实际audit PK记scalar。
- 跨行内容、身份、版本完整链、观察完整链、cut、双向pointer不匹配记OriginalPointerRows。缺被指对象时只标实际存在的指向者；已有双方内容不符分别标实际双方，不能造不存在的original行键。完整配置链缺版/多版及current末配置义务以实际control为owner，局部版本错误同时落其实际version键。
- 缺/多周期预算audit的集合义务按实际operation记CompanionRows，孤立或内容错audit按其真实原件键记OriginalPointerRows；同一operation多原因在该计数去重。重复业务身份使全部参与的真实行和消费者关系失败，不把原物理PK不同误写成物理重复计数。

合法legacy全NULL边界仅表示这里没有新观察关系矛盾，不在本片授Q、不清空任何历史gap、不改命题分母。确定矛盾与unproven同时存在时都保留；坏覆盖声明不能被legacy吞掉。接线后应准确更新SourceClosure的Unimplemented描述，但完整PB动态前缀、历史admission/dispatch/finish观察及absence、legacy完整成员/效果边界、业务cause和最终逐对象依赖仍未解决。旧PB/PBprefix Base报告、算法和缺口原样保留。

## 7. 冻结后的实现与验收清单

根冻结本文后方可独立实现/接SourceClosure，架构同步与最终验证由根统一执行；本草案不授权改材料、PG、Git或旧算法。验收至少包括：

1. 三scope首版、真实changed、连续多版、同配置no-op、MaxInt64 no-op；旧TEXT actor/tenant/Key/account及Users/Tenants仅ID存在性；不把今日角色当历史权限。
2. 漏版/额外未来版、同operation多version、无变化版、wrong actor/xid/time/resource、错current末配置；看似合法expected候选却实际changed/no-op错误。
3. 严格4/12/5 JSON及五字段audit、bool/string区别、decoded重复键/未知/Unicode/NULL、精确指纹、8192/65536界；audit缺/多/孤立/交换target、错changed/tenant/created_at。
4. covered origin/control/head双向正确；origin错误stamp/calendar、孤立head/journal、operation或window少pointer/部分NULL、跨control/type/source错配；无origin全NULLlegacy、旧control后来新source仍NULL、部分覆盖不能降级。
5. window空出生后有event；今天非零金额/revision不影响出生0/NULL；birth前effect、错cut seq/tail、同seq换tail、cut或配置revision回退、断序/跳号/错previous/head、源与journal重复/复用。
6. 相同资金cut下window→command与command→window，后生窗口不能污染旧null；同事务window_birth先于其真实配置receipt的合法延迟关系；墙钟回退/合法同instant Location不被误判。
7. 不同排列相同计数；多原因/重复物理PK去重、跨表同ID分别计；删raw对象不能制造虚拟PK计数；取消/到期/资源错误整体零结果，预算内大组不逐command重扫。

纯fixture仅证接受规则与反例；不冒充受控writer、catalog/ACL、真实专用PostgreSQL、供应商或生产验收。动态金额前缀和B-HO1未交付前，不能用本片通过宣布完整PB历史或采购来源闭合。

# 023-B0 隔离 PostgreSQL 原型记录

2026-11-01，**接线前的历史设计验证；当前020核心已按b-integration-contract实施，最新结果另见[核心验证](../../docs/verification/periodic-budgets.md)**。测试目标是 [B合同](b-contract.md) 的可执行 SQL 设计和反例，不是 HTTP、真实 main、供应商计费或生产认证。根已读审核心、命令和权限三份SQL。本记录保留当时反例与修订证据；原.local脚本曾用当时billing在019基线上造旧源，当前billing已要求020，不应把原命令直接用于新源码并宣称仍是同一复现版本。最新完整迁移/受限LOGIN/真实billing回归由独立核心记录承接。

## 1. 环境与复现

- 专用 PostgreSQL 16.15 (Homebrew)，database/user 均为 `moni_test`；脚本启动先核验，不使用生产或预览数据。
- 每次创建独立随机 schema，应用真实 `migrations.Apply` 的 001–019，先用现有 billing 创建合法客户 Reserve、采购 RecordAttempt 和可信 Finalization；再追加三份原型 SQL。
- 每次生成独立 `NOLOGIN` APP/projection 角色，经现有 `operations.ProvisionFor` 后执行 B 专用 REVOKE/列级 GRANT，业务操作实际 `SET LOCAL ROLE APP`。owner 只建fixture/DDL/时钟fixture及清理，不代替 APP 断言。
- 首轮本地脚本 `.local/023-b0/prototype.go`及日志`.local/023-b0/phase5-result.txt`保留为旧基线。初占/派发修订后的原完整脚本为`.local/023-b0/dispatch-prototype.go`；本轮首配同步修订后，**末次完整回归**以`go run -race .local/023-b0/dispatch-prototype.go`退出0，日志`.local/023-b0/source-sync-full-race-result.txt`。脚本直接读取下面三份tracked SQL，不使用旧`.local/core.sql`副本。另`.local/023-b0/initial-reserve-prototype.go`及`initial-reserve-result.txt`保存初占专项第一轮。原型代码未放生产包、未占迁移序号。
- 首配同步专项`.local/023-b0/source-sync-prototype.go`也直接读取同三份tracked SQL；普通运行及最终`go run -race .local/023-b0/source-sync-prototype.go`均退出0，race日志`.local/023-b0/source-sync-race-result.txt`。全部随机schema/角色清理。这是Go测试驱动与SQL设计原型的证据，不代表B生产包已经实现或经过race验收。
- 修前反例脚本`.local/023-b0/source-sync-before.go`刻意读取保留的旧核心SQL快照`.local/023-b0/source-sync-before-core.sql`（SHA-256 `ef66e0972bf4f0719f4647556ab36cd8e48d2fbe85097b50a68a9529ad0ad529`），命令/权限仍读取tracked副本；它退出0表示**四个错误已成功复现**，不是旧设计验收通过，日志`.local/023-b0/source-sync-before-result.txt`。双方提前`SET CONSTRAINTS ALL IMMEDIATE`形成确定可见性屏障；不是借缺少真实资金图制造失败。
- 1000/1001 的客户旧源为在 B DDL 前批量建立的合法 held fixture，配套单笔平衡 journal、wallet 与 Key 汇总；这只是首配边界 fixture，不冒充1000次HTTP/实际Reserve验收或压测。
- 每次成功/失败均 drop 随机 schema，再 drop 临时角色；末次日志 `SCHEMA_AND_ROLES_CLEANED`。没有保留“供产品运行”的 B 数据库对象。

末次真实执行 SQL 与 tracked 设计副本逐字相同：

| 文件 | SHA-256 |
| --- | --- |
| [b-core.sql](b-core.sql) | `bd554c7752454c90b40667a0f52a525d53c98b029780262c9f2f17331aad7e90` |
| [b-commands.sql](b-commands.sql) | `1081fda04b24b6120147b1fedd586f24df4cda8757180960a09a1d324dc206d7` |
| [b-permissions.sql](b-permissions.sql) | `be4683bf0c179f53fadb069eb25f0fe7c9ca3bf6b3476edb53ec184d55b79c7f` |

## 2. 已通过的精确范围

| 门禁 | 实际断言 |
| --- | --- |
| 可执行DDL | 实际001–019后核心/命令/权限原型可应用；事件与配置相关触发器在真实提交中执行，不是只parse SQL |
| creation proof | 顶层tuple、active SAVEPOINT tuple、RELEASE后的子tuple均通过完整XID8+当前可见tuple证明；历史已提交source不能当新源 |
| 长事务先取得xid | 事务xid早于control，但在首配后创建采购源；数据库at-creation marker明确为该control，必须建立普通new allocation，不能当legacy |
| legacy_import | 原held=240导入一次，spent不重记；后续可信cost=120完成到同一无窗legacy bucket |
| legacy_catchup | 首配时旧closed无allocation；之后争议从spent120/held0恢复held120，严格同tx新019 event证明baseline120；绝对cost150计预算30，再降80预算归0，不出现负spent |
| 采购首配先 | 首配已持account，另tx持同一old attempt UPDATE并等待account；首配logical source校验不取得业务KEY SHARE，可提交，再由finish完成；无attempt→account→attempt反环 |
| 旧重开先 | old closed attempt先恢复held并持account，首配实际等待；释放后首配准确导入一次baseline120/held120，不漏或双计 |
| 普通新采购 | RELEASED子事务创建新attempt/采购reservation；同tx精确019 reserved effect进入原window，后续settle仍在该window |
| 客户首配四方向 | tenant首配→finish、finish→tenant首配、Key首配→finish、finish→Key首配都以真实`pg_stat_activity.wait_event_type='Lock'`证明发生等待；未决导入且后收尾正确，先终结的旧源不伪造当前周期allocation |
| 客户双scope | 同request tenant/Key两个allocation、单钱包journal；RELEASED子事务新source，原window结算charge70，两个scope各spent70，钱包只扣70 |
| 普通客户初占不可省略 | 完整合法request/reservation R=120、平衡reserve journal、Key held及两个new allocation；省略reservation event后创建平衡settle journal/settlement70并更改源终态，首个settlement event以`periodic original reserve event required first`/23514拒绝，两个control金额/事件序号保持0，allocation全回滚；不是借资金图缺失冒充B拒绝 |
| 同事务Reserve→finish | active tenant/Key cap0时先真实初占事件按完整R=120拒绝；两个cap=120时同tx先初占再finish70通过，恰两scope各reservation(before revision0)+settlement(before revision1)，held0、各spent70 |
| 采购完整U初占不可省略 | 修前真实APP、active cap0、U240可以直接INSERT settled/held0/spent70，一份B首事件held_delta0/spent_delta70成功提交，019账户/源/全部事件与B仍守恒。修后直接settled INSERT、prepared→settled后迟建allocation、先建allocation再净spent首事件、错误首事件type/key分别以指定B guard/23514拒绝；正确U240初占在cap0明确以budget exceeded拒绝。每个负例完整事务回滚且control金额/event_seq不变 |
| 采购同tx初占→收尾与免费模型 | cap=240时先真实019 reserved/reserve:attempt、B完整U240初占，再同tx结算70成功；恰两事件，held0/spent70，allocation/window/control一致。独立新客户request与真实零价cost version/attempt下U0仍有一份reserved初占0事实且正常提交；不以免检或净额替代 |
| legacy客户初占不可省略 | 首配事务创建真实legacy allocation后直接终结客户资金图而不先legacy_import event，同一初占guard明确拒绝；正常legacy先导入后收尾继续通过 |
| 派发全scope与DB时刻 | owner将三control首配时钟置于62天前后恢复真实clock；分别仅tenant、仅Key、仅采购active cap0时，APP单独旧pb_gate成功但prepared→sending仍23514拒绝，checked_at为null、状态prepared、窗口高水位不前移。限额600/600/240时成功，返回checked_at≥本次UPDATE前DB时刻，覆盖caller的2000年旧值，三scope高水位均由同一返回At派生 |
| 派发状态与来源防伪 | checked_at修改/清空、sending→prepared、unknown→sending、直接INSERT sending均拒绝；INSERT prepared自动将caller旧checked_at归null。当前credential已有managed binding但漏建采购reservation，真实sending transition以source mismatch拒绝，不能伪装无采购scope |
| 派发锁等待跨UTC边界 | 真APP沿目标父/客户control/采购锁序在持有的control上等待，`pg_stat_activity`确认Lock；owner仅将随机schema DB时钟从首个完整月边界前1µs推进到边界，释放control后trigger以新At命中active cap0并拒绝，prepared/null checked_at及旧高水位保持。不是以sleep猜到达或客户端at_time替代等待 |
| 裸APP旧客户首配同步 | 修前完整reserve/release journal、钱包、Key held及最终settlement均正确，双方仍能双提交留下allocation held120/source released。修后首配先行时，未取tenant前层锁的释放在scope共享锁真实等待；漏mirror以B projection/source mismatch拒绝且资金/Key/settlement全回滚，精确mirror则成功到revision2/held0。释放先行时首配在scope独占层等待，之后只导入其余480、不伪造已结束源allocation |
| 裸APP零金额采购首配同步 | 原019采购reservation/account/event图先合法释放到held0/spent0。修前released→disputed不更新account金额，可与首配双提交并漏allocation；修后真实scope等待后漏mirror拒绝、状态回滚，带当前tx真实019 zero-delta event及legacy_catchup则精确通过；reopen先行时首配等待后把disputed0源legacy_import一次。没有用019账不平冒充B拒绝 |
| 新客户源不可拆开 | B前合法request-only迁移后迟建reservation以same-tx request/23514明确拒绝；B新request即使没有任何controller也不能无reservation提交。未配置旧reservation原amount/身份不可改，合法status收尾继续通过。新request/reservation含RELEASE子事务，两种首配先后都真等待、仅一份allocation且held总计720；首配先→new，源先→legacy_import |
| 新采购首配两方向 | 裸APP未显式取前层根的新attempt/reservation仍由stamp取得account屏障；两种首配先后真等待，最终单份allocation held240，总held480；首配先→new，源先→legacy_import |
| 裸APP派发与首次配置 | 修前在受控首个完整UTC月边界，sending能漏掉不可见active cap0首配并双提交。修后tenant/Key/采购分别真实scope共享等待（精确blocking PID与advisory wait），owner从边界前1µs推进到边界，首配提交后裸APP以新At命中cap0拒绝；attempt仍prepared、checked_at null、controller高水位回滚。派发先行则首配在scope独占层等待其许可提交后导入held600 |
| 隔离级别边界 | REPEATABLE READ和SERIALIZABLE分别对新request、首配、旧客户reservation UPDATE、采购reservation UPDATE及prepared→sending返回精确RC门禁/23514；没有把advisory等待当作RR快照刷新 |
| 目标Go当前Key权限协议 | 真APP在control前持tenant、actor membership SHARE、actor user SHARE、Key UPDATE，并等待后新语句复验active/角色/owner/未撤销。membership suspended、user suspended、Key revoked分别各两方向：撤权先→命令等待后拒绝且无control/operation；命令先→撤权等待，原不可变成功回执保留。仅证明目标锁协议，不声称共享APP数据库actor检查等于最终用户认证或生产Go已实施 |
| 消费退款 | 真APP角色调用实际`billing.CreateRefund/ApproveRefund`退10；Key lifetime spent保持70，两个周期allocation spent仍各70 |
| Login FK | login先持user UPDATE，配置持tenant NO KEY UPDATE再等actor SHARE；login插session tenant FK KEY SHARE可提交，配置继续；没有重新引入user→tenant反环 |
| 在线上限 | 1000旧客户held完整导入，合计held120000；1001首配在BEFORE control guard明确maintenance拒绝，整个tx回滚，control/operation不存在 |
| 配置命令 | 成功no-op新回执但不增config version/revision；真配置变更CAS、配置版本追加、历史result_snapshot不变、首次打开仍scheduled |
| 原子审计 | owner暂时撤销APP audit INSERT；配置变化后的operation记录因audit失败回滚，原cap/revision不变且无成功回执 |
| 直接APP反例 | 直接改control held、改calendar、无真实source event、旧source冒充new、首配后任意legacy import、伪造source creation stamp、改operation/删除configuration version、无新operation改cap均拒绝 |
| 漏mirror | 在真实019 source/reservation/account/event均正确更新的tx，省略B mirror；**B源提交guard**以`periodic supplier projection/source mismatch`拒绝，不借019本身账不平冒充B覆盖 |
| 角色 | projection SELECT control失败；APP `CREATE OR REPLACE pb_now()`失败；APP无calendar UPDATE、operation UPDATE或version DELETE权限 |

采购初占末次专项为 `.local/023-b0/supplier-initial-prototype.go`，`go run -race`退出0，日志`supplier-initial-fixed-result.txt`（位于同目录）；修前独立反例保留于`supplier-initial-review.go`及`supplier-initial-review-result.txt`。修后同一三份tracked SQL另重跑`dispatch-prototype.go`、`source-sync-prototype.go`与`initial-reserve-prototype.go`，三个`go run -race`均退出0，结果各为`<脚本名>-supplier-fixed-result.txt`。所有最终随机schema/APP/projection角色均精确清理并核零残留。第一次专项试跑因fixture重复使用同一request/channel的既有唯一约束失败，修正为独立新request后通过；一次中断fixture亦已按精确随机名称清理，不把试跑错误计作业务拒绝证据。

追加scope错误分类后（当前表列hash），上述四套脚本再次以`go run -race`退出0，日志为各自`<脚本名>-typed-errors-result.txt`；预算拒绝保留原message/23514并新增固定constraint=pb_budget_tenant/key/procurement，为后续安全错误脱敏提供确切分类，不用SQL正文猜scope。

## 3. 受控数据库时钟fixture

生产拟议 `pb_now()` 始终为无参 invoker VOLATILE `clock_timestamp()`，不读 GUC、客户端或进程时刻，固定实际schema/pg_catalog/pg_temp；APP无CREATE/ALTER函数权限。

仅本原型owner在随机schema将函数替换为 `clock_timestamp()+62 days`、历史首配用`clock_timestamp()-62 days`及派发锁屏障用固定边界前后时刻，再恢复真实clock；其他预算guard/角色/事务照常运行，最终整个schema销毁。**这验证受控数据库时钟边界，不表示真实生产系统发生过时钟跳变。**

- 推进后在后继完整月建立新窗，旧held作为carry；将tenant cap降至0并启用，成功配置为active。
- extra=0派发门禁和新Reserve-like资金事务都被cap拒绝；新事务源/journal/预算全回滚。
- 再关闭cap，事实继续保留；恢复真实clock后高水位仍在fixture后继月，当前GET和新准入因回退拒绝。
- 相同回退状态下，旧客户Release和采购绝对成本收尾仍可提交；客户释放不降低采购carry，采购只能由其独立可信effect减少held。

高水位证明的仅是UTC窗口start不退行；没有宣称检测同日/月内任意壁钟回拨。派发返回At只作为同tx截止复验时刻，本原型执行了同At的Key/request/reservation期限断言，未替代后续生产Go全部quote/权限复验与actual-main验收。

## 4. 指纹golden

每字段UTF-8、4字节大端长度前缀，无NULL，无直接字符串拼接。PostgreSQL `sha256(pb_frame(...))` 与独立 Python `hashlib/struct.pack`结果一致：

```text
parts = [
  "key_periodic_budget.configure",
  "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  "/api/keys/cccccccc-cccc-4ccc-8ccc-cccccccccccc/periodic-budget",
  "9007199254740993", "utc_month", "false", "9223372036854775807"
]
SHA256 = 2a00c71a0d596018c8ae1fdba6be1c70bcb2385548b96f9b32ddd2ecb0b3d34d
```

Go/TypeScript实施仍须各自加入相同golden；Go纯协议已独立验证同一golden；前端尚待接线，不能把Python/PG通过当作其已完成。

## 5. 仍未完成、不得宣称通过

- 根及独立reviewer对核心SQL/Go/权限锁图已复核并按核心接线切片冻结；HTTP/UI与完整产品验收仍独立。
- source创建时标记/时刻算法与Go接口对齐、guard源关系覆盖、源变更局部校验的并发EXPLAIN/查询上界全面审查。
- 采购1000/1001、UTC精确日/月/二月闰年、Reserve/RecordAttempt与钱包/account等其他最后锁等待跨界、第二attempt跨期/换账户、并发不同Key同tenant cap竞争、A组价/model超R升级交叉；派发已有control等待与三scope首配同步等待跨首个UTC完整月已通过，不能泛化为这些全部已通过。
- 首配scope专用共享/独占层与完整Go锁图仍须根语义读审；已有明确源图在裸APP省略前层锁时的首配完整性及Key命令当前权限目标锁协议已通过上表，不再列作未跑。共享APP不是用户身份，任意APP声称actor不构成授权承诺；不得在持control后回补前层锁。生产Go/helper/HTTP实际接线和当前权限验收仍未实施。
- 签名cursor与keyring/有界管理准入、当前Key权限撤销、恶意JSON/HTTP错误脱敏、unknown命令查原operation；只有目标合同，没有运行HTTP验收。
- 后台恢复/实际进程崩溃与COMMIT不明、旧备份恢复门禁、只读全量守恒核验器及projection安全报表。
- 完整商业验收与真实供应商/生产条件；未运行B专属actual-main或浏览器，未以fixture替代。

集成人汇总稳定文件、完成设计审查后再更新统一架构与清单。本子任务没有执行architecture refresh、修改生产代码/迁移或运行完整产品验收。

# B-P4 D1：宽TEXT关系的精确绑定候选合同

2026-11-03；基线 `cff5bef966c990cfea5d85f0b4ea4ba164ae146e`。承接[原件DDL](b-procurement-original-ddl-contract.md)、[来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本](b-procurement-cost-request-contract.md)、[执行](b-procurement-execution-contract.md)及[权限](b-procurement-full-acl-contract.md)。本页先冻结可验证的替换方向与并发实验，**不修改或启用现有685项关系约束，不关闭全域宽索引门禁**。

## 1. 问题与旧接受集

原 `audit_events.actor/target/request_id`、`journals.reference` 以及若干原TEXT身份没有独立的业务/数据库固定总字节上界。原单列PK或原来已有的二元唯一键能存储某个值，不保证再拼接root/source/actor等字段后仍能存储。PostgreSQL btree每个索引项受页宽限制；NULL追踪列仍会生成索引项，故原件全NULL兼容CHECK不能保护合法长legacy行免受新增宽UNIQUE影响。TOAST或数据可压缩不构成接受集证明。

具体原件包含 `audit_events` 三个新增目标UK、`journals` 的完整输出UK、`provider_cost_corrections` 完整输入/来源UK、`procurement_commands` 完整命令UK。问题也覆盖新表：RequestOrigin/AttemptOrigin保留原自然TEXT主键，RootStep.source_id在两类origin上不是新UUID；带tenant/request/attempt/user等原TEXT的复合UK不能仅因位于新表便推定安全。

不能截断/归一化原值、缩小原列/旧接口域、给旧行补来源、用hash替代精确身份、NOT VALID、关闭约束或改写已应用迁移。仅修几条audit payload边不代表全部关系已经修复。

## 2. 完整关系清点与分类

[静态清单](b-procurement-exact-link-inventory-v1.json)按当前四份固定constraints SQL逐条列出268个PK/UNIQUE及291个FK的准确名称、表与列序，并列出每个FK实际引用的目标键。清单不是live catalog白名单，不提供权限或最终profile。

分类采用保守口径：单列键保留原值的既有存储域；只含经受审规则固定的新UUID/枚举/数字的组合可以另证宽度；凡仍含未取得固定字节上界的TEXT组合先列 `width_review_required`。该状态表示尚不能证明保持接受集，不声称清单中的每个键都已有可触发反例。清单的剩余分类规则须逐项审定后，才能形成正式替换集。

## 3. 选定的可选机制：原定位FK与真正双端精确检查

对未证明宽度的冗余复合键，未来版本保留原PK/原来已有准确定位键的native FK，额外所有字段使用固定、逐列 `IS NOT DISTINCT FROM` 比较；NULL不跳过、空串不等于NULL，数字保留原类型和值域。不把payload加入新索引，不把条件partial UNIQUE伪装成PostgreSQL可引用的FK目标。

这是**受控root与不可绕过guard配套的双端关系约束**，不是“id存在后相信应用会比对”。具体替换前，每条边须固定：父/子真实定位键、全部逐列映射、条件分支、原NULL联合、父/子INSERT/UPDATE/DELETE覆盖、触发顺序、实际角色读锁权限及闭合时点。普通legacy行不得承担新宽索引或新的隔离限制。

### 3.1 精确隔离边界

本候选对所有tracked父/子**写入**要求 `transaction_isolation = 'read committed'`；INSERT/UPDATE/DELETE的BEFORE guard在真正修改前拒绝RR、Serializable与Read Uncommitted。普通legacy分支仍按原规则，RR只读维护/runtime不受影响。原Go业务入口使用RC；这项限制修订未来候选root的RR写语义，不能悄悄套用到原接口或既有profile。

tracked判定必须来自真实受保护出生/来源列及不可绕过guard，不能由APP传bool/GUC、trigger_depth、caller token或今天查不到来源决定。旧行不可转tracked，新受保护写不可伪装legacy。private W/APP/owner的允许与拒绝集随全域root/guard精确装配；实验的owner预置marker只用于证明锁/快照原语，不授权生产补历史。

### 3.2 子写与父变更

子检查先按原准确定位键锁父行 `FOR SHARE`；等待完成后以**下一条SQL语句**重新读取父真实当前行，再逐列比较。缺父由native定位FK/固定错误拒绝，错payload/NULL/来源值由真实精确检查拒绝。锁持有到事务结束；不可只FOR KEY SHARE，因为非key payload UPDATE与KEY SHARE可兼容。

父UPDATE/DELETE的实际行锁保持到事务结束；在锁后新的RC语句中读取最终父行，并反向完整枚举指向它的受覆盖子行，逐条比较，不以count/hash代替。延迟trigger检查最终当前行，不能拿较早事件的NEW覆盖同事务后续合法变更。子INSERT先持SHARE时，父UPDATE等待；父先持更新锁时，子SHARE等待。等待后的新statement使检查看到已提交对方结果。

所有可能改变所比较字段/定位键/来源分支的写路径都必须被这套guard覆盖，包含同事务多root、零变化、辅助函数、APP顶层COMMIT、DELETE/TRUNCATE与原其他入口。不可仅在两条公开函数中约定RC而保留raw DML或peer helper绕过。locator自然身份按原不可变规则，TRUNCATE不属于行级trigger可证明范围，仍需权限/statement guard。

### 3.3 为什么不能继续允许RR tracked写

反例：RR父事务先取得旧snapshot；随后子事务在旧payload上完成SHARE检查并提交；父事务再取得写锁修改payload，但其固定snapshot看不见新子。普通PL/pgSQL comparator没有native RI内部detectNewRows/crosscheck的等价接口。仅加deferred trigger或FOR SHARE不足以排除此反例。故本候选明确拒RR tracked DML，不能把该反例留给“调用方通常RC”。若未来恢复RR写，必须另冻真正的写冲突fence或等价机制并实际验收；hash只可参与冲突序列，不成为身份/等值依据。

## 4. 最小可执行实验

[独立PG测试](../../internal/procurementprefixschema/exact_link_experiment_postgres_test.go)的顶层为 `TestPostgresProcurementExactLinkRCExperiment`，使用 `TEST_DATABASE_URL`，严格限定专用PG16/127.0.0.1:55439/moni_test及moni_test LOGIN。根严格串行执行；测试内部两个实际受限LOGIN连接用于真实竞争，不用SET ROLE冒充。

实验只建立随机schema的parent/child模型、单列native定位FK及四个invoker trigger：父guard、子guard、父deferred精确检查、子deferred精确检查。payload为无新增索引的TEXT，tracked marker由fixture owner预置且APP不可改；两个并发session均使用同一受限LOGIN的真实连接，不用SET ROLE。APP可写父payload和子expected_payload、删除两端；父/子id及父tracked均不可UPDATE。实验不实现受控tracked INSERT、来源出生或全域不可变身份。所有实际原业务source/root/角色、原020/B-P1/PB以及全域精确链接仍未接入。

必验：8192B不可依赖压缩的合法TEXT可存；实际长值逐字节相等可提交、单字节不同及NULL差异拒绝；child先取得SHARE时parent真实等待，parent先修改时child真实等待，用pg_blocking_pids确认；等待后新值匹配成功/旧值拒绝；tracked父UPDATE/DELETE及子INSERT/UPDATE/DELETE的RR写在BEFORE拒绝、普通legacy RR写继续允许。同事务父A→B→A和双端一致更新以最终当前状态通过；子单端错误更新拒绝；父DELETE后同定位键重建为untracked不能借相同payload留下子行，双端完整删除则允许。最终已提交图逐行核父存在、tracked及payload全等，所有schema/roles精确清理并核零残留。结果由根另行记录，测试草稿不自行宣称已通过。

## 5. 退出条件与未完成项

1. 清单所有 `width_review_required` 键逐项得出固定宽度证明或精确替换，不保留不受控的额外宽UK；保留原有单列/准确定位索引的原接受集。
2. 每条替换边完整双端静态映射、真SQL/guard/RC校验、权限/不可变/逐槽反向集合一同交付；全量列DTO、reader/basis/catalog/profile同步。
3. 实际APP→W受审根在RC业务成功，RR tracked/raw/helper等全部拒绝；所有旧API回归、长值、NULL、同事务多次、并发/取消/超时/未知COMMIT均验。
4. 对未被闭合的跨行/全集/历史命题继续缺证，不因一个锁实验通过关闭D1、I1、十一Unproven、B-P3或R2。

当前685项SQL仍原样保留为上轮候选；本页不在其上删除约束后发行“已解决”结果。正式切换必须先完成上述配套，使实际最终接受集与精确完整性同时成立。

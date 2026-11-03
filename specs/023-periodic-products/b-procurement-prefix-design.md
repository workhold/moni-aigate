# B-P4：采购账户与成本历史证据设计稿

2026-11-03，设计基线 `0b150c58ebf630f984770bd0fe37c07591b13c51`。承接[采购历史缺口](b-prefix-procurement-barrier-gap.md)，采用独立实现，不复用第三方业务代码。

**状态：设计稿 v0.1，尚未实施。** 本文确定业务目标、证据原则、现有缺证的覆盖矩阵与分片门禁；逐字段 DDL、数据库受控写入口、权限及固定读集尚须按第八节冻结。它不是完整实施合同，不授权修改原 checker 的接受集、旧迁移或已有证明材料。


后续进展：本文保留D0设计的当期范围；[权限/事务方向](b-procurement-writer-authority-contract.md)及[独立身份ABI](b-procurement-identity-contract.md)已分别冻结，身份材料已实现并有[真实PG差分](../../docs/verification/procurement-identity-leaf.md)。[账户](b-procurement-account-d1-proposal.md)和[成本/request](b-procurement-cost-request-d1-proposal.md)仍是工作提案，完整DDL、受控writer、纯重放与新profile尚未实现；D1整体与B-P3门禁保持。单独冻结的无业务权限codec不扩大本设计的业务写入授权。

## 1. 目标与业务意义

同一收费供应商账户可以服务多个渠道、凭据及模型，平台应能独立回答每次派发时是否还有采购预算，以及哪次正常用量、争议或双人更正形成了当前成本。证明依据必须保留操作当时的事实，不能只用当前余额、当前 reservation revision 或最新价格解释过去。

B-P3 实测触发三项独立缺证：共享账户多来源的 `AccountAdmissionOrderUnproven`、更正审批的 `ProposalBarrierUnproven`、旧正常成本写入资格的 `CostRecordedEligibilityUnproven`。本片设计覆盖完整操作前缀，同时审查原采购 checker 的全部十一项 Unproven；不能修掉三个例子后宣称任意采购历史已可认证。

该证据用于内部预算和采购成本核验，不把供应商人工余额观察改为真实余额，不证明供应商最终账单、折扣、税费或其服务器实际扣费。已有按渠道及精确上游模型版本化的报价仍是请求成本计算依据。

## 2. 不可变原则

1. **原始来源先于证明。** 数据库在实际业务操作时，同事务生成来源绑定与见证；Go 只能提交操作身份和业务输入，不能自行声明被消费的 before/after、顺序、证据数量或摘要。
2. **真实账户顺序。** 所有影响账户金额、配置、阻断与准入的成功操作，共用账户锁及连续 ordinal；时间戳、UUID、事务 ID 和 PB event seq 都不能代替该顺序。不同账户无需发明全局顺序。
3. **完整状态。** 账户 configuration revision、journal ordinal、reservation revision、证据成员序和成本 decision ordinal 是不同的序列；任一无变化都不能推断操作没发生。
4. **原子闭合。** 业务原件、账户/attempt/reservation 投影、PB effect、outbox、审计与新证据要么一起提交，要么一起回滚；不得提交暂态或没有业务来源的见证。
5. **历史保真。** 更正是新绝对金额，原正常成本和提案 expected 屏障保持原值。新增证据不得回填认证旧缺证历史；维护锚点至多说明被真实认证的范围，不能补造历史读取。
6. **独立重放。** 新纯核验器从原件重算金额、资格、状态和前驱，再比较见证及当前投影；见证不是可直接信任的余额，也不授资金或数据库权限。

“所有 upper 总和不超过限额”的窄定理只能在额外前提下说明金额安全，不能证明真实消费的配置与证据版本，不作为本片替代方案。

## 3. 拟定证据域

下列名称表示逻辑责任，尚不是已冻结的物理表、列名或新增表计数。

| 域 | 必须保留的原件与绑定 | 独立重放要求 |
| --- | --- | --- |
| request / attempt origin及成本源链 | 请求、attempt真实出生及前缀起点；涵盖无attempt finalization与legacy-unmanaged成本 | unmanaged没有账户/reservation，须独立绑定完整cost来源，不能借managed成员前缀免责 |
| 账户 origin / head | 账户出生身份、初始配置、零金额基线；head 指向精确末项 | origin 不可由预插见证或现有账户自报登记；head 必须与完整前缀闭合 |
| 账户 journal | 账户 ordinal/previous、操作来源、配置与金额 before/after、阻断状态、实际准入结果 | 每项只消费此前真实前缀；reserve 加 upper，dispatch 不重复加预占 |
| reservation 状态前缀 | attempt/account/binding、实际状态桶及 anomaly 的变化、与账户项和原 budget event 的关联 | 逐步重算 held/spent、自动暂停、unknown/disputed 阻断及有利收尾 |
| evidence 成员前缀 | 每份不可变成员的精确身份、被观察的位置、对应原 meter/final/conflict | 屏障证明完整集合，数量相同或仅摘要相同不足以证明成员相同 |
| 正常成本 decision | 真实输入来源、写前 cost status/amount、当时 dispute 状态、record/skip 原因、实际结果 | 重算零消费或最终用量金额；解释为何允许写或为何跳过 |
| 提案与消费 decision | 提案出生时完整屏障、审批实际消费的同一屏障、审批身份、绝对更正及全部结果来源 | before revision 与提案 expected 一致，after 按真实字段变化推导；同额也有唯一 decision |
| finalization / conflict 来源 | 原始 incoming 身份、语义、请求前驱与规范化结果、命中原 meter/重复/冲突的实际分支 | 不用存量 meter 的身份代替 incoming，不把缺失 finalization 当成无业务事件 |

阻断状态必须精确表示 `anomaly && state in ('unknown','disputed')` 的成员；普通 unknown 继续占用金额，但不凭空成为异常阻断。若保存派生 count，则必须由每个 reservation 的完整状态前缀重算并比较，不能由 APP 自报。

## 4. 实际写入时序与受控路径

### 账户准入、派发与效果

现有 Go 路径先更新 reservation，可能再暂停账户，随后追加 budget event、更新账户金额并写 PB effect。因此不能在任一 AFTER trigger 中重读当前表，并把该暂态称为操作前余额。

推荐将受覆盖的采购 mutation 收进数据库闭合入口：按既有上游锁顺序锁账户和供应商 PB，再在持锁状态取得 before，执行实际 gate 和状态变化，使用同一组变量生成不可变证据。APP 不能继续拥有绕过该入口改动受保护投影的能力。使用最小 NOLOGIN writer 的受控函数，或等价的 invoker/列权限/guard 组合，须先完成实际权限和绕过分析；本文不预先批准任何 SECURITY DEFINER 函数。

reserve 的 before 必须满足 active、无异常阻断、`held + spent + upper <= limit`，并保留实际报价与硬上界绑定；dispatch 使用已预占金额，同样要求 active、异常阻断为零并校验 `held + spent <= limit`，还要绑定真正的 `upstream_attempts.delivery_state: prepared -> sending`、请求期限及 PB 资格。人工resume为active但仍有异常disputed成员时，即使金额足够也必须拒绝派发。仅把采购 reservation 改成 sending 不能证明派发许可。

结算、释放、unknown、争议、绝对更正、自动暂停和人工配置都在同一账户顺序中；它们不得因账户 paused、限额收紧或当前存在异常而阻断必要的保守记账与有利收尾。恢复只有在采购prepared且上游delivery仍prepared、无sending permit时才能expired释放；已获permit或sending只能unknown。已非eligible状态的重复恢复不新增原event，不重发模型生成。

### 正常成本与更正

当前 `recordFinalization` 正常分支先写 meter/finalization，再调用 `recordAttemptCost`，最后调用 `finalizeProcurement`；冲突分支不创建正常 finalization，直接处理 dispute/reset。新证据必须在各自真实分支捕获写前状态，不能在 finalization 全部结束后读取终态代替正常成本的资格。

`recordAttemptCost` 在 cost status 已知、reservation 已 disputed、缺少可信最终用量或完整报价时可能跳过。新 decision 要区分这些 no-op；未写 `cost_recorded` 不代表该检查没有发生。managed 与 legacy-unmanaged 成本路径也须分别绑定真实来源，不能只给 managed 历史发一份报告后称全部采购成本已覆盖。后者没有采购account/reservation，既有 `RecordCostCorrection` 另走原 correction→attempt cost→audit/outbox 路径，不使用managed双审提案或成员屏障；新request/attempt出生和cost源链必须单独覆盖它。其调用权限保持原入口的实际合同，不凭空声称已有双审。

提案出生与批准均在现有请求/attempt/账户/PB/reservation/提案锁顺序内读取完整屏障。批准先比较原 expected revision 和完整成员前缀，再生成唯一绝对更正、correction 原件、成本投影、approved 提案、审计/outbox 与消费 decision。同额更正可能 before revision 等于 after revision，但仍必须有独立 decision ordinal，不能用 `revision+1` 判定审批执行或次数。

幂等行为按实际来源区分：normal finalization的同ID/input_digest快返不新增源；重复conflict当前仍INSERT新conflict原件，但语义identity可去重，不能吞掉该原件或再改桶；不同approval操作的同额批准仍是新decision。同一提案/批准的原输入重试只在当前授权仍有效时返回原结果，提案重试前重核operator，审批快返前仍核maker/checker角色、不同人及状态；失权不因已有回执而获准。不同输入复用身份仍拒绝。成功执行的无金额变化操作有证据，纯幂等重读不新增业务effect。因异常回滚的尝试不留下已提交decision；需要保存的业务拒绝回执必须有单独明确的source与no-effect语义。

## 5. 原十一项缺证与新证据对应

原实现位于 `procurement_evidence_graph_accounts.go`、`procurement_evidence_graph_events.go` 与 `procurement_evidence_graph_evidence.go`。旧报告逐项保留，新报告只能在实际原件完整时独立取得覆盖。

| 原 Unproven | 新设计需要证明的命题 |
| --- | --- |
| AccountAdmissionOrderUnproven | 同账户全部来源的真实顺序、准入前配置/金额/阻断与 reserve/dispatch gate |
| AccountPauseOrderingUnproven | 原因明确的自动暂停和人工配置在账户前缀中的位置及 revision |
| ProcurementEventOrderUnproven | 多次 unknown/dispute/correction 的真实 reservation/event 顺序 |
| ProposalBarrierUnproven | 提案屏障与审批实际消费屏障、完整证据成员、唯一更正及 before/after |
| CostRecordedEligibilityUnproven | 正常成本写入当时 unknown 且非 disputed，以及真实输入与写入结果 |
| CurrentCostOrderUnproven | 正常成本、reset 与多次绝对更正逐步重放后得到当前成本 |
| ConflictIncomingUnproven | 冲突的原始 incoming 身份及使用的原 meter，包含跨请求/attempt 碰撞 |
| ConflictReasonUnproven | 当次 incoming 原始 reason 与规范化语义，而非从哈希猜 reason |
| IncomingBindingUnproven | incoming tenant/request/attempt 与争议目标的原始绑定 |
| OriginalDeliveryPredecessorUnproven | 规范化前输入及当次实际读取的 latest finalization 前驱 |
| FinalizationOrderUnproven | 同请求 finalization 连续顺序、sticky delivery 与 latest 投影 |

账户 journal 本身不能解决最后五项 incoming/finalization 命题；仅有 approval witness 也不能解决旧正常成本的资格。若后续分片暂不覆盖某域，该域和受影响完整 aggregate 继续 indeterminate，不允许将其计数清零或声明 N/A。

## 6. 独立核验与版本边界

新算法拟作为无 DB、无权限的独立纯层，读取完整原采购图与新增原行；从 origin 顺序重放账户、reservation、evidence、cost decision 和请求 finalization，并比较每份 source 的双向唯一绑定、前驱、完整成员、金额及终态。使用有界规范整数和独立金额运算；执行/context/资源错误零结果，确定矛盾优先于缺证。精确公开 DTO、计数、scope 和预算在实施前冻结。

旧 `CheckProcurementEvidenceGraph`、B-P3 的 `ReadPrefix/ReadWalletPrefix`、71/87/112/128固定材料、旧 basis/roots/codec/golden 保持原接受集。新证据需要显式新读取及 catalog profile、固定 basis 与 history/current 映射；不能在 V3 内悄悄增加物理来源或将旧 gap 改成已证明。新 aggregate 保留原 Base 和各项计数，新 scope 逐项证明后单独判断覆盖，不拼接不同快照或 caller 报告。

候选只能在精确声明的隔离基线之后安装，DB installer、受限实际 APP 与新 scanner/catalog 必须一起审定。正式迁移、provision、维护 CLI、startup/ready 和在线来源钱包仍由 R2 单独交付；新增证据不是启用这些能力的开关。

## 7. 历史与故障

request、attempt和账户的初次origin只能由对应受控真实出生建立。已有账户、旧 proposal、旧正常成本及旧 conflict 不自动补录；即使当前金额、成员数、revision 与新字段恰好相等，也不能推断历史资格。后续若提供历史 cutover，须独立定义它能证明的范围和全部前置，不能宣称认证未保存的过去。

旧 B-P3 两组缺证拒绝用例原样保留。新候选测试应在证据安装后，通过真实 APP 重新执行同样的多来源跨窗和24→25更正业务，证明新历史完整通过；不能给旧 fixture 填入推测的 witness 后取得成功。

任一写失败、取消、死锁、权限或 deferred 闭合失败，全部业务与证据一起回滚；序号不得有已提交缺口。COMMIT 回复未知时仅按原操作身份查询，不重新派发或重复批准。未知 outcome 与账户消费未知是不同状态，必须分别测试和记录。

## 8. 实施前必须冻结的内容

- 每个原始 source 的精确出生时点、触发器顺序/可见性、函数参数、返回值、幂等键、错误码及 no-op/拒绝语义。
- 完整 DDL：字段类型、NULL、规范数值、generated、PK/unique/FK、双向延迟闭合、immutable/TRUNCATE guard、schema/search_path、固定 SQL 与权限矩阵。
- 受控函数的所有权、EXECUTE、表/列 ACL、默认 ACL、SET/ADMIN 角色闭包、禁止 bypass 的原 direct SQL 面；当前 invoker/新 writer 的选择须有真实受限 LOGIN 攻击验证。不能保留APP独立INSERT budget event、改reservation/账户投影后再调用见证入口的绕过面，单独封住journal表不足。
- 请求、attempt、channel/credential、账户、PB、reservation、proposal、新 head 的完整锁顺序；同语句多来源、READ COMMITTED 等锁后新快照、RR 重试、暂态读取、递归及死锁规则。
- 新纯 DTO 和完整读集列并集、generated 实际读取、EOF/Close、分页和总资源界；固定 descriptor/catalog/dependency/role根及历史兼容材料。
- 新维护 profile 的 Begin/锁表/once与原 phase、observer/runtime 原事务绑定；沿用5秒catalog和30秒APP总界，不因新增材料擅自放宽。

上述内容冻结前只允许继续设计、源码审查和独立 oracle 准备，不把本文当成可直接执行的 SQL 合同。冻结后由根依据明确接口分工实施，无需再请求例行实施批准。

## 9. 分片与并行方式

| 阶段 | 交付 | 并行边界与退出条件 |
| --- | --- | --- |
| D0 | 本设计稿、十一项命题映射、反例、实际写入时序 | 账户与成本/输入证据独立审查，根汇合；不登记新运行能力 |
| D1 | 精确 SQL/权限/锁图、纯 DTO、来源矩阵、验收合同 | DBA/账户与成本路径并行设计，根裁决共享接口后才开始实现 |
| I1 | 候选写证与调用接缝；独立纯重放及手算 oracle | SQL/业务写共享文件单一 owner；纯层与独立测试可并行，PG串行 |
| I2 | 固定 scanner/basis/catalog 与完整同快照组合 | 读集、basis、静态材料可并行；维护/runtime 由根串行集成 |
| A0 | actual APP 完整成功/失败矩阵、旧路径回归、全仓与CI | 缺证拒绝与完整成功分开验收；全部证据与架构同提交推送 |
| R2 | 正式迁移/权限/维护工具/启动及全部在线资金接线 | 独立合同和生产门禁，不因候选完成自动进入生产 |

## 10. 验收矩阵

正例必须覆盖共享账户4来源上界各240/limit1000，两个结算、一个无消费释放、一个held的完整经营；以及各700/limit1000但前一来源先释放后下一来源准入的合法真实顺序。另验跨窗、限额收紧/暂停后有利收尾、零价token越界、unknown/争议/自动暂停、24→25、更正降价需invoice、多次更正、同额更正、证据新增后旧提案拒绝、重复证据与幂等重试、并发审批、恢复与未知提交。

负例必须包含先占用700再追加700的拒绝、曾超上界后下调成本不能抹掉异常历史、同数量不同 evidence 成员、伪 before/来源/前驱、预造或孤立见证、ordinal跳号/回退、缺decision/多decision、跨tenant/request/attempt/source、正常成本在disputed时写入、incoming与存量meter绑定混淆、sticky delivery前驱缺失、同语句篡改顺序、旁路DML/EXECUTE/role/default ACL，以及资源/取消/EOF/清理失败。

验收报告须逐域列出十一项命题实际覆盖/仍缺证的状态；单个24→25正例不得外推incoming/sticky、unmanaged或多更正历史完整。另验无attempt finalization影响sticky、跨attempt共用request.latest、重复conflict原件与成员去重的区别，以及operator/approver失权后的重试拒绝。

真实PG均使用专用集群及实际受限LOGIN，按合同串行执行；不能用owner或SET ROLE代替业务身份。独立纯oracle与手工金额/顺序例子不调用writer作为expected。每项精确记录PASS/SKIP/未运行，新profile成功不能反写旧接口测试。原catalog专项race时限门禁仍独立保留，不因本设计稿或远端默认CI通过而关闭。

## 11. 源码审查入口

| 责任 | 现有实现 |
| --- | --- |
| 账户准入、派发、状态/桶、更正与恢复 | [procurement.go](../../internal/billing/procurement.go) |
| 正常finalization、meter冲突、sticky与正常成本资格 | [evidence.go](../../internal/billing/evidence.go) |
| legacy-unmanaged更正 | [recovery.go](../../internal/billing/recovery.go) |
| 账户创建、配置及命令回执 | [store.go](../../internal/procurement/store.go) |
| 当前DDL、guard、proposal与投影守恒 | [014迁移](../../migrations/014_procurement_accounts.sql) |
| 原全部Unproven和报告 | [graph.go](../../internal/fundinglotverify/procurement_evidence_graph.go) |
| 账户历史检查 | [accounts.go](../../internal/fundinglotverify/procurement_evidence_graph_accounts.go) |
| 事件/成本选择检查 | [events.go](../../internal/fundinglotverify/procurement_evidence_graph_events.go) |
| evidence/finalization/proposal屏障检查 | [evidence checker](../../internal/fundinglotverify/procurement_evidence_graph_evidence.go) |

本稿经账户与成本两路源码审查交叉复核。审查结论只适用于D0设计，不表示D1的SQL/权限/锁序已通过或新行为已获测试。

# B-P4 独立重放实施与私有对象集合

2026-11-03；基线 `5e5dcdf6867b58a1107abe889895e08320b66cfc`。本页实施[完整读取/重放合同](b-procurement-replay-read-contract.md)与[DTO合同](b-procurement-core-dto-contract.md)，不缩减四域和十一命题的最终义务，不修改旧 checker、SQL writer 或已有 V3 物料。

## 私有接口与预算

包内 `newReplayWork(ctx, Rows)` 先运行全72数组的100000行/16MiB预检，再创建单一 `replayWork`；其 `b budget` 保存原 context 与绝对 deadline，所有域通过 `tick()` 共用该工作状态。后续不得逐域重开deadline或将大对象解析移到预算之前。输入拒绝、取消、超时或资源错误均返回零结果，不将执行失败转成数据通过。

对象键使用无歧义的 `tupleKey(parts ...string)`：逐分量写十进制UTF8字节长度、冒号和原始字符串，不用分隔符拼接代替复合身份。单ID也用该编码。`keySet` 与 `propositionKeys` 均为包私有；后者的11字段与固定 `Propositions` 逐名对应，不能接受调用方提交的证明集合。

三个业务域与来源域分别采用私有 `checkSourceClosure`、`checkRequestIncoming`、`checkAttemptCost`、`checkAccountPrefix`。每域接收同一work与本次完整raw Rows；私有结果包括相应固定Stats、局部Candidates/Qualified、Complete，以及明确的未实现列表。**Complete只表示本域全部算法已实施且执行，不代表数据有效或有完整来源。** 数据矛盾记录固定自然键去重Stats，缺历史来源保留gap；未实施分支使Complete=false，来源域未完整时不返回Qualified。业务域Candidate只有在该对象全部本域依赖成功后生成，最终还须与来源域及其他必需域逐对象合并，不能直接赋Proven。

固定Stats遵守读取合同§3.3：同一字段按自然对象身份去重，before/after/hold/计价等错误原因不是新的对象。同一字段涉及不同物理对象族时在键内显式加类型，不能把跨表相同ID互相抵消；缺原件用对应预期对象键，真正孤立来源保留自己的类型与自然键。测试同时覆盖单对象多缺陷只计一次和不同对象同ID分别计数。

在四域、准确Required枚举、完整Base调用与最终aggregate全部实施并通过之前，继续不导出 `Check`。不得为了接线而加success stub，也不能以Partial/Complete=false的局部报告冒充完整核心交付。已实现叶子须实际检查原件并有独立fixture/变异测试；未完成列表保留最终交付义务，后续继续落实。

## Required的独立枚举

Required只从本次raw原件的无损旧Base投影独立计算，不接收旧Report或caller计数。新枚举器以旧三份证据算法的实际条件为兼容边界，保留原去重、合法标量、候选来源、配置版本与条件分支；不能用表总行数代替。自然键逐项固定为读取合同§7的finalization/request/conflict/identity/attempt/proposal/account键。

实现必须返回真实键集合，另将集合长度逐项与同次原 `CheckProcurementEvidenceGraph` 的十一Unproven交叉核验。保留旧Base所有字段原值；任何不等是实现或数据矛盾，不能调整Required使计数凑齐。测试须有手算对象集合，覆盖complex事件>1、同值更正、conflict tenant、managed/unmanaged成本、proposal当前屏障落后及配置自动pause等条件，也须覆盖乱序与重复身份。旧有mismatch不因新枚举被掩盖。

## 账户序与 attempt 序的交叉检查

读取合同§6.1与§6.4要求相同p动作在两条局部链中有共同的先后关系。按account ordinal重放时，每attempt的reserve/dispatch/采购effect对应canonical node ordinal必须严格递增；金额同值、零delta或相同revision不能放宽该顺序。否则两次同额更正可被交换account journal而不改变任何桶，仍会错误认证屏障。

对于EvidenceChoice、ProposalBirth、ApprovalConsumed与Recovery的观察source，按同attempt动作node序定位观察node前最后动作与后首动作。source的account cut须大于等于前动作的account ordinal，且严格小于后动作的account ordinal；两端缺失按真实起点/末界处理。其他attempt可合法插入账户动作，但不能据此观察本attempt的未来动作或退回其过去状态。Recovery使用其实际effect journal之前的cut；无effect来源同样必须核区间。任一桥接失败记录对应域矛盾并撤销依赖该账户/提案的本域候选。

## 实施责任与完整门禁

### 来源续片：当前指针与 Usage

来源域在已有四链结构检查后，独立把原 request 的 `pp_last_decision_id` 和 current latest 对照 request decision 完整链末项；只核这两个指针，不将普通 dispatch/finish 的 status 或 billing_status 永久绑定最后 decision。原 attempt 的 last mutation/delivery 由其 node ordinal 筛选实际对应来源，空链回到 origin 初态；末项 ID 与当前值分别比较。dispatch 许可是该 attempt 唯一 `dispatch_permit`，四列永久身份、原 PB checked_at、managed account/journal 逐值对照原件，即使后续 delivery 改变也不能把许可换成最近 delivery。managed p 的 origin 指向同出生根的 reserve journal，last 指向该 attempt 在所属账户序中最后一个 p 动作；其他 attempt 和配置可以穿插，不使用账户全局最后 journal 代替。本片关系错误沿用 OriginalPointerRows 的(物理表,自然主键)去重，source 缺原件和原件错末项各保留其真实物理对象，不按错误原因重复计数。完整链已报结构矛盾时不能据被丢弃的序列补造空链证明。

Usage 续片检查原 meter、conflict 的 normalized_usage 与 case 非 NULL proposed_usage 的真实六字段 JSON；SQL NULL、JSON null、缺字段与零值分开。严格解码、业务 int64/子集/confirmed 总数规则独立计算，共享原 work/deadline，按原物理行计 InvalidScalarRows。此片不包含 command/audit/outbox 其他 JSON、全部语义联合、历史 Reconcile 观察 cut 或最终 Qualified；这些义务继续显式保留。

### 人工来源偏序与 finish 行联合

来源域新增私有reconciliationOrder：按case显式索引P/A/R三action，不用时间、XID或UUID排序。每action最多一个新source；重复action的各物理source记OriginalPointerRows。同一case已有P时，A消费的maker、Usage、evidence、proposal reason必须等P真实Input；R保留已知P/A的proposal/maker/approver。已有A后新R必须由A的pending outcome承接，before case为approved；A产生新或既有settlement后不能再新R。没有P的A、没有已知P/A的R可能消费真实旧case，保留私有LegacyCases集合而不伪造父source；不能仅因缺历史P把它记mismatch，也不由此授完整来源资格。孤立R允许原API本就接受的open/proposed/approved/closed before，不新增release双审。孤立R的open且无proposal/actor不要求P；open仍带旧proposal/actor时也保留LegacyCases。无新人工source且原case六列last全部NULL同样保留旧历史缺证；已有tracked P而R.before=approved却缺A是确定矛盾，不伪装为旧A。

rank最大且唯一的新source决定原case完整pp_last六列。错误pp_last按原case自然ID记OriginalPointerRows，source间矛盾也按各物理source自然ID记OriginalPointerRows，多原因去重；行内角色/状态等错误仍用InvalidScalarRows，CanonicalUnionRows保留canonical child/step联合职责。自动markPending的reason变化不影响人工pointer，普通finish可能关闭非approved case；不把current reason或整个case永久固定到source.before/after。若比较当前字段，只能比较所有后续合法写口都不再改动的proposal/actor字段，或由已知tracked转换推出的允许state集合。私有LegacyCases必须留给后续gap聚合，不成为调用方proof；Complete仍false。

来源域finishUnionRows独立执行ReconciliationSource与CustomerFinishOutcome的完整行内NULL/角色/clock/result联合，按两张物理表自然PK记InvalidScalarRows。忽略输入仍typed NULL；case snapshot与Input分开，case字段的DDL status接受集不被全局缩为confirmed，只有approve消费要求confirmed；完整原case提案仍由原件/业务关系认证。approve的派生输出只按其自身approval clock对deadline决定；existing/pending/new采用真实时点与输出联合。existing先于decision clock；pending必须decision严格早于deadline。仅对new_settlement：recovery的reason只接受charged_usage/released_no_delivery/released_deadline，且reason=deadline当且仅当decision或financial任一已到deadline；release的decision必须NULL，reason只接受released_reconciliation/released_deadline，且reason=deadline当且仅当financial已到deadline。deadline分支全零released，financial回退不恢复收费，recovery不能借用人工release原因。existing两个clock为NULL，历史reason接受集不缩减。PB两个control独立optional，profile created/reused精确成组。各clock互不排序，控制absence、历史before及跨行计价/资金证明仍由相应完整域负责，行内联合通过不授Qualified。

### AttemptCost 的两个私有实施子域

实际派生的Reconcile approve incoming必须唯一映射到本次raw的RequestDecision（按incoming ID建多值索引，重复不可任选）。ReconcileSource→IncomingSource→RequestDecision按同root/xid、相同tenant/request、严格递增正step相连，decision必须normal且精确生成本source预分配finalization；decision.before_latest逐值等source.prior_latest，after_latest等本finalization。两个latest即使delivery内容相同也不可互换。该source的多处错误只按ReconciliationRows中source自然ID计一次；未派生decision分支继续保留观察cut缺证，不用当前latest倒填。接线后既有approve局部fixture补上真实决定行，不降低唯一性或省略分支。

成本历史入口为 `checkAttemptCostHistory(work, Rows)`，拥有出生/quote、delivery、normal/reset/mutation、更正、恢复、outbox与当前成本的固定前九项计数和三个attempt命题候选。`checkReconciliationFinish(work, Rows)`拥有ReconciliationRows、CustomerFinishRows、CustomerLedgerRows、CustomerPBRows，独立核三种action、无attempt派生incoming及finish的结果和真实效果；两者都只读取本次raw输入，共享同一work。子结果的Complete只描述其明确的子范围，不是整个AttemptCost或来源闭合。最终AttemptCost必须等两者全部义务实施并执行后才能完整；本片不能因任一子域通过开放公共Check。

finish按原实际时点分别核approval、settlement decision和最终financial clock；existing先于decision clock，pending保留reservation/billing/case reason的实际改变。new settlement独立计算冻结客户两维费率的单次ceil、R上限、waived和最终过期释放；HELD/AVAILABLE即使零delta仍各有真实line，REVENUE仅最终charge>0。profile-block按实际最终raw>R决定，复用旧block不强制旧request=current。账本与PB效果逐对象双向对照，不能以原件存在替代准确金额/来源；所有资金全历史与PB配置/窗口链继续依赖原完整checker。人工last-source已由来源域固定偏序认证，不再虚列完整自动case生命周期总序；finish保留未派生decision的请求/attempt真实历史cut和历史PB配置/absence两项Unimplemented，禁止用当前投影反填历史或以UUID/时间排序。

finish固定计数自然键：ReconciliationRows按source ID（缺source的原case单独物理类型）；CustomerFinishRows按outcome ID（缺outcome按source义务ID）；CustomerLedgerRows按(outcome ID,account ID)，孤立原journal/line分类型；CustomerPBRows按(outcome ID,scope)，孤立原event分类型。同一对象多错只计一次，不同表相同ID不互抵。

来源域独立检查32表全部scalar/NULL联合/68 generated、18 canonical/root模板、companion与原件反向槽、request/node/account/member四链；业务域按原合同逐项重放请求输入/meter/sticky、attempt quote/cost/finish、account gate/成员/采购效果/提案/恢复。不复用writer或SQLcodec作为计算expected，不用今天当前状态填补过去before，不按时间/UUID排序。

工作文件单一owner：来源域 `source_closure*.go`，请求域 `request_replay*.go`，账户域 `account_replay*.go`，成本历史 `attempt_cost*.go`、客户结算 `finish_replay*.go`，共享support与Required由根集成；所有子域继续沿完整合同推进，局部实现不缩最终门禁。所有结果汇合后根复核精确实际范围、HLD/LLD/统一架构与验证，再按AGENTS刷新/只读检查/Make。PG仍仅根独占串行；纯核没有DB、supplier或资金权限。

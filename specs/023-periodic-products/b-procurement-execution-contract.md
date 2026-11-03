# B-P4-D1-E：入口执行、锁图、错误与恢复合同

2026-11-03。逻辑基线为[共同来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)、[PB ACL](b-procurement-pb-acl-contract.md)与[物理布局](b-procurement-schema-contract.md)合同。本页冻结各入口执行次序、真实锁、等待后语句、封闭错误与恢复适配；不是可执行函数正文。**未实施 DDL/候选 executor、未运行 PG，不勾选 D1 完成；新 profile、完整 ACL/独立重放/实际验收及 B-P3、R2 门禁保持。**

## 1. 事务与入口范围

原 [billing.transaction/transactionOnce](../../internal/billing/store.go#L16) 为 READ COMMITTED；每次事务设置 lock_timeout=3s、statement_timeout=5s。仅 errors.As 能取得 PgError 且 Code 为40P01/40001、ctx未结束、已知整次失败时重试，最多三次，无退避延时，不重置调用方绝对 deadline。保留 cause 链，不能将它们改包装为失去 SQLSTATE 的字符串。原 [procurement.mutate](../../internal/procurement/store.go#L139) **不自动重试**，包含40P01/40001；已有回执快返走只读结束，不新增 root。

5s 是原每条 frontend SQL 的 statement_timeout，原 wrapper 没有自行设置整次调用累计5s。完整 SQL 根的一次调用整体受5s约束，不能按每个 SPI 子语句重新计5s；原多个语句总耗时可能超过5s，新单根实际执行预算更紧。这是本候选明确接受的执行形态差异，不能以扩到10s等方式抹平，须实际多等待场景验收。3s行锁/advisory等待、上层模型请求/财务绝对期限不放宽。清理所用独立5s context不延长业务资格。

入口覆盖账户create/configure、完整RecordAttempt、真实dispatch、公开finalization、managed提案/审批、unmanaged更正、逐attempt采购恢复和Reconcile。普通Reserve/Settle/Release/MarkPending、客户恢复、PB Commander、绑定/余额观察是相邻同步路径，保持各自原入口，不冒称已全迁入采购writer。其必要边在§5列明；资金/支付/退款的完整历史认证仍属原独立合同。

**设计校准：公开候选为 pp_record_attempt，完整接管 RecordAttempt 的 checks→quote→INSERT/原幂等→managed admission。** 固定保留三参pp_complete_attempt_birth作为private helper，APP无EXECUTE；返回及唯一函数集合沿布局§6，不留内联二选一。仅在Go插入后调用三参根会使unmanaged后验重读current跨scheduled时点改变接受集，或只能信任caller旧quote，不能满足真实选择证明。原Go AttemptInput/API不变；完整物理ABI以[布局§6](b-procurement-schema-contract.md)为准，不在本页另造签名。根返回outcome=applied/replay、root_id?/source_id?及原attempt_id/request_id/sequence/delivery_state/cost_status，按原Go Attempt逐值重建。

expected CostQuote 的 has=false 时字段全NULL；has=true 时VersionID/Basis为BYTEA原Go string字节、两维rate各自可NULL。manual/unmanaged/RouteRevision0的未消费expected字段可能有NUL/非法UTF8；根不转TEXT、不保存未消费expected，也不额外拒绝。仅在原check实际比较分支，将DB选中quote的UTF8 bytes与expected bytea逐值比较。来源记录真实选中的DB quote，不能把expected当选中证明。

## 2. 锁记法与公共片段

S=FOR SHARE，U=FOR UPDATE，N=FOR NO KEY UPDATE。箭头表示实际先后；普通SELECT只定位路线，不宣称持锁。FK隐式KEY SHARE、INSERT/UPDATE隐式行锁保留，不升级成未经实现的显式锁。批处理中的语句仍逐条执行，完整drain/Err/Close后才发下一批；不能以JOIN/单个CTE把等待后新语句折回旧快照。原 [closeBatch](../../internal/billing/store.go#L48) 保留先前domain/scan错误并合并drain失败，不重跑callback。

| 片段 | 固定顺序与作用 |
| --- | --- |
| LR：lockRequest | 先普通读request身份；tenant S → membership(owner) S → key U → model S → 可选channel S → customer reservation U；随后**新语句**重读request.status/latest。没有显式request FOR UPDATE；reservation是同request来源串行屏障。LR只取得父锁，不重新拒绝已禁用用户/tenant/model，避免阻断收尾 |
| LP：lockPrincipal | tenant S → membership S → user S → key S或U → model S；检查父状态/owner/revoked/IP/model scope，批完全drain后新语句读grant/profile_block与DB clock，再判key expiry。见[原实现](../../internal/billing/store.go#L274) |
| CPB | tenant、key依次pb_sync_unconfigured；已配置control按id排序N。缺席scope通过原shared namespace同步，不拿未配置的一次旧快照作缺席证明 |
| PH：readProcurementHold | 普通读p.account；account N → supplier pb_sync_unconfigured → supplier control N → p U；新语句得到p完整before。无p返回none，不猜账户。见[原实现](../../internal/billing/procurement.go#L124) |
| wallet | 原初始化INSERT ON CONFLICT DO NOTHING → **分开新语句**按账户id排序U读balance；AVAILABLE/HELD属tenant，REVENUE属平台。见[lockAccounts](../../internal/billing/store.go#L163) |

新head的锁不能描述成一个脱离业务的全局总序：已有p路径先完成原前层/attempt/account/supplierPB/p，approval再锁proposal，然后取本次需要的head；同一阶段统一pp_request_heads → pp_attempt_heads → pp_account_heads，缺少的域跳过。member_count/member_tail属于同一个pp_attempt_heads，**没有第二个evidence head**。有request_decision才需要request head；proposal/approval/dispatch/recovery仅按其attempt/account cut取对应head，不为统一形式增加request head锁。纯账户配置仅account N→account head U。managed birth无旧p可锁，account head在account/supplierPB之后、真正gate/p出生前锁定；新request/attempt/head初生用INSERT，不伪写已持有不存在行的FOR UPDATE。

Reconcile的request head在case锁后、sticky/finalize之前，资金层CPB/wallet在更晚的finish阶段；不得为追求统一总序把资金锁提前到审批decision或把before移到AFTER。共同reservation锁先串行同request，另一个request不访问这个head/case，故这两条局部顺序不凭空构成同对象反向等待。头锁必须在读取其cut/推进前取得；任何helper不得从account/head回头取request/attempt/channel。

## 3. 逐入口执行与等待后检查

### 3.1 账户create/configure

[原lockCommand/mutate](../../internal/procurement/store.go#L76)：外层输入校验 → advisory(hashtextextended('procurement:'+operation,19019)) → 原command读取/actor与digest比较 → users S重核admin/active → 相同command才快返。**异actor/异digest的ErrConflict优先于随后权限检查；相同输入失权不能快返。** DB根重算原Kind/Target/Payload JSON摘要，不信caller digest。

create在上述检查后DB新account INSERT、origin/head初生、完整command source/account journal/audit、seal root，无旧p/PB/customer锁。configure随后account N → account head U → **新语句**读account/head/blocking；found先核受证一致性，再按原expected revision选择applied或revision_conflict。not_found为原可提交rejected command，无账户journal；revision_conflict有真实before=after journal。相同配置仍applied并有journal；降低limit或将有blocking账户设active不在此拒绝，之后admission gate另判。命令root不碰PB配置。

输入按[原configuration/canonicalID](../../internal/procurement/store.go#L24)：canonical UUID文字含原允许nil，正revision/非负limit规范int64字符串；label/provider为1..100字节、有效UTF8、无首尾空白/控制字符，state仅active/paused。不把此严格ID规则扩到billing原uuid.Parse接受集。

命令source在真实锁/分支确定后，以独立DBclock采样command_created_at并完整出生；新的command BEFORE guard据本次精确source强制派生实际NEW.created_at，保持W无INSERT(created_at)。source诊断时间不作替代，原表DEFAULT不删除；只有这两kind改变时间采样点，绑定/余额观察仍用旧DEFAULT。不能声称仍是原saveCommand DEFAULT的同一观察时刻，实际回归须覆盖返回CreatedAt、双向时间相等、错源拒绝及其他kind原路径。

### 3.2 pp_record_attempt：新attempt及原幂等

外层Sequence=1..2、UpstreamModel1..200字节，RouteDecisions按[原validator](../../internal/billing/route_decisions.go)先验证，至多1000项/262144B、唯一channel、非零weight、精确selected与封闭reason。然后完整根：

1. LR(channel)取得原tenant/member/key/model/channel/reservation锁，刷新latest。这里原代码**没有单独credential行S**；readAttemptChecks是冻结membership/关联的普通读，channel S负责发布/绑定同步，不虚构credential锁。
2. [readAttemptChecks](../../internal/billing/evidence.go#L123)各自新语句：当前key CIDR/scope/region/revoked/expiry+DB clock；binding isolation；published route membership(仅RouteRevision>0)；冻结channel credential membership；DB clock<request_deadline；Sequence2的可信首attempt无消费finalization；最后current cost选择。错误判定顺序保持IP→key活性→scope→isolation→reservation reserved→published→credential/region→request deadline→retry证据→cost读取。
3. [checkAttemptCostQuote](../../internal/billing/cost_policy.go#L27)先看当前credential是否有procurement binding；managed必须完整known quote且expected相等。然后RouteRevision=0直接返回；有route再读冻结policy/上限，仅policy managed或procurement才消费expected并判cost exclusion。不能对manual/unmanaged另加quote相等要求。
4. 原upstream INSERT ON CONFLICT(request_id,sequence) DO NOTHING。冲突后用**新语句**读原attempt，比较channel/version/credential/upstream；不比较route_decisions、quote、今日p状态来扩充原幂等身份。所有上述checks仍在快返前执行；旧attempt无新origin/root或采购reserve。
5. 新attempt先出生自然键origin、明确managed/unmanaged与新head。binding缺席在channel S后的新语句确定；**unmanaged沿第一次原current选择冻结quote，不在后验出生阶段再选current**。managed取得account N → supplier namespace/control N → account head U，再新语句选current cost，比较先前DB quote/expected；新语句读input/output bound与request deadline。无旧p FOR UPDATE可取。
6. 算U并判account active/no blocker与H+S+U≤limit → p INSERT原stamp → AdmitSupplier原时刻gate(U) → 新语句key expiry → 用实际admission.At判key/financial/request deadline → 以该时刻checkDispatchCostQuote → reserved PE、账户/PB镜像、准确source/journal/head、seal。

原[recordProcurementAttempt](../../internal/billing/procurement.go#L51)在account/PB等待后已有current与deadline复核；候选保留这些时点。current cost排序固定effective_at DESC,created_at DESC,id DESC，单条语句内materialized DB clock；不能按未受保护的time.Now选择scheduled版本。原首次选价语句同时返回quote_selected_at，写upstream.pp_quote_selected_at与AttemptOrigin同值；无quote也保留真实采时。此时点不证明当时全目录absence，不能用今天目录反推旧语句快照。来源先出生与结果闭合不改变unmanaged quote时点。

### 3.3 真正dispatch

[PrepareDispatch](../../internal/billing/evidence.go#L214)：普通定位attempt/request/principal → LP(key S) → channel S → **新语句**binding isolation → credential S → 冻结version/credential/region关联 → tenant/key region判断 → reservation U → attempt U → **新语句**DB clock判key expiry、reserved与两期限、prepared → CPB → PH → 必要heads。

PH后新语句核当前credential binding与冻结p.binding。unmanaged后来出现binding、managed绑定不符或p非prepared返回procurement_binding_changed；已有upstream非prepared在更早state检查返回attempt_already_dispatched。managed再判account admission(extra0)，将p→sending，然后同根真实UPDATE upstream prepared→sending。原pb_guard_dispatch重用customer/supplier namespace/control并核来源，全部control等待后选唯一pb_now时刻，三个scope逐一gate；该时刻作为checked_at。

UPDATE后用新语句读financial/request deadline，dispatchTimeEligible以checked_at再判key expiry→两deadline；checkDispatchCostQuote用checked_at新语句读取当前quote，current与frozen相等及policy/两维上限；最后request.status=dispatching。任一后检失败整根回滚，不能因已执行UPDATE就发生成。success仅applied，没有dispatch replay成功；p.sending不是许可，必须精确真实dispatch source。见[cost检查](../../internal/billing/cost_policy.go#L77)与[PB许可](../../internal/periodicbudget/tx_dispatch.go)。

### 3.4 公开finalization与Reconcile内部finalization

[公开入口](../../internal/billing/evidence.go#L332)先在事务外补空ID(uuid)、校验uuid.Parse、source1..80/source_event1..200、Usage、outcome/reason/stage封闭枚举、finish_reason≤80；ObservedAt不进入输入digest。LR无channel，不加当前供应商活性/quote/用户授权来阻断证据收尾。

原private流程先算原始input digest(ObservedAt清零)并查ID：相同digest立即返回完整旧Finalization，在attempt归属、sticky归一化与meter写入之前；异digest不会立即拒绝，继续原meter/conflict流程，直到normal INSERT冲突才返回idempotency_conflict。不能为了统一快返改变此顺序。空ObservedAt只影响有效观察时间；attempt须匹配tenant/request。原内容sticky消费锁后真实latest，跨attempt/无attempt同属request序列。

候选在需要实际新动作且有attempt时，于任何cost资格/before捕获前显式attempt U → PH → 必要heads；这是新闭合边界。原normal路径先UPDATE delivery隐式锁attempt后才读PH，原conflict路径已有显式attempt U；不能称原normal在meter之前已有该显式锁。无attempt仅request head，不能虚构采购/evidence/cost节点。

meter INSERT用原(tenant,source,source_event)冲突新语句读取，并按usage/request/attempt判断碰撞；confirmed_final再查同attempt其他不同final usage。conflict追加真实conflict原件，managed先finalizeProcurement，随后外层cost reset按已裁定p状态可抑制；markPending不重开terminal客户。**公开**可提交semantic_conflict：COMMIT成功后返回原metering_conflict错误、空Finalization；不造normal finalization/latest。**Reconcile内**相同语义须抛PP143使整个外层事务回滚，case approved及全部来源一并撤销。

normal插入finalization后更新latest/status、真实delivery，记录正常cost资格/实际mutation/outbox，再执行managed evidence/PE及必要dispute reset；不能把account最终AFTER当正常cost资格before。普通重复identity仍可有normal请求来源而无新PE；不同业务分支的source/节点按共同模板闭合。

### 3.5 managed提案与审批

[Propose](../../internal/billing/procurement.go#L369)：事务外operation uuid.Parse/amount≥0/basis contract或invoice/ValidReference256/reason1..200 → advisory(hashtextextended(operation,9021)) → 当前finance_operator users S → 读旧proposal并逐值比较tenant/attempt/amount/basis/evidence/reason/requester，匹配快返 → 普通attempt定位request → LR → attempt U → PH → 必要heads → 非prepared/sending → 降价必须invoice → 新语句完整member prefix/count → proposal原件/source/audit。**权限先于旧proposal幂等比较**，不同于账户命令。

[Approve](../../internal/billing/procurement.go#L432)：operation/proposal uuid.Parse → 同9021 advisory → 当前finance_approver users S → 普通读proposal → 禁同maker/checker → maker当前finance_operator users S → 定位request → LR → attempt U → PH → proposal U → 必要heads。然后已approved只在approval_operation/approver精确相同才快返；此快返仍经历前层/PH/proposal锁与当前双角色核验，但不要求旧proposal屏障等于今天p/member。

新批准先非inflight，再比较expected revision/count和ProposalBirth的**完整成员prefix**，再核降价invoice；捕获ApprovalConsumed before与本次账户/member cut、预分配输出并一次INSERT来源，之后p/account/PE/PB、correction、cost mutation、proposal approved、audit/outbox。不能只因count相同批准不同成员；同额/basis-only仍为新批准effect，不能按p revision未变消除来源。已暂停/缩limit不阻止此收尾，不自动resume。

### 3.6 unmanaged correction

[RecordCostCorrection](../../internal/billing/recovery.go#L251)事务外amount≥0、operation为1..200字节原字符串(不强制UUID)、ValidReference256、basis；定位request → LR → 候选显式attempt U → 明确unmanaged origin且无p → 必要request/attempt heads → correction INSERT ON CONFLICT(operation) → 新语句比较tenant/attempt/amount/basis/evidence。managed拒绝procurement_approval_required；**没有原不存在的财务role/双审要求**，ActorID不在幂等identity，空audit actor=system。

重复operation即使换actor仍快返无新audit/outbox；新操作覆盖真实cost并发行来源。原此路径到UPDATE cost才隐式持attempt锁，候选显式前移用于真实before，是已选设计补强；不得新增quote选择或账户gate。原outbox唯一冲突仍整体回滚，不吞23505造第二条cost_recorded。

### 3.7 逐attempt采购恢复

候选公开两参tenant/attempt；普通定位request后LR → attempt U → PH → 必要heads → **新语句**读DB clock、request_deadline、真实p/delivery/不可变dispatch source。checked_at>=request_deadline才过期；无managed p、p不prepared/sending或未过期为noop，不出生root/source/PE。此锁后期限复核是相对[原恢复](../../internal/billing/procurement.go#L278)的新补强；原实现只在批扫描检查deadline。

expired且p/upstream均prepared且无真实permit：delivery failed、released/H0，expired_without_permit。其他eligible有permit/sending组合：delivery unknown，H/S不释放，recovered_unknown。不得以p.sending或caller boolean当permit；无需meter/finalization，无provider client。并发已finalized后noop，不能重复release/unknown PE。

### 3.8 Reconcile与晚到客户资金层

[Reconcile](../../internal/billing/recovery.go#L99)仅action propose/approve/release；propose事务外验证confirmed_final合法Usage、evidence/reason。approve的Usage/evidence/reason均不消费，根参数全NULL；release仅reason消费，Usage/evidence参数NULL，不先将忽略字段NUL/非法UTF8转TEXT而制造新拒绝。先当前actor role users S(propose=operator，其余approver) → LR → case U，再按action分支；不是先取得全部资金锁再查权限/快返。

- release在case锁后才判reason1..200；reservation已released读取旧settlement快返，其他非pending拒绝。新release直接finish(reason=released_reconciliation)，不制造finalization。公开result.State=closed；实际case原为approved时finish保留approved，否则关闭，source.after_case_state不能拿返回state替代。
- propose先case非approved/closed、reservation pending；已proposed只有requester/原Usage/evidence/reason完全相同快返；否则immutable pending冲突。新制单保存case/source/audit，不取wallet锁。
- approve在case锁后重核存在的maker当前operator，maker缺失或自批先拒绝；然后approved/closed可读旧settlement快返，缺settlement且reservation仍pending返回原pending占位。新批准要求proposed与完整confirmed_final证据，**新语句**取approval_checked_at；case→approved。该时刻早于financial deadline才派生无attempt approved_reconciliation输入，逐值沿旧latest delivery，不能制造delivery。request head在sticky/子流程之前；语义冲突回滚整根。

实际settleRequest先查已有settlement，无则新语句取settlement_decision_checked_at，依次：financial deadline优先释放；无latest且recovery且无非prepared attempt释放；未决meter conflict保留pending；无可信delivery/usage按原分支pending或no_delivery释放；最后按冻结retail计算raw和min(raw,R)。见[原选择顺序](../../internal/billing/settle.go#L51)，不能拿approval_checked_at代替财务选择时钟。

实际finish先charge范围检查；raw>R时model锁升级U → CPB → wallet初始化/按id U → **新语句**读financial_checked_at；到financial deadline则charge/raw归零、released_deadline。之后完整CustomerFinishOutcome before/target、真实ledger/settlement/Key/reservation/request/PB原窗/清case/job/audit/outbox/profile-block。原model S→U升级在并发中可能产生40P01，保持原有限重试，不擅自把所有模型前层改U。existing/pending无假ledger/PB effect，release直接finish没有settlement decision时刻。来源与资金结果两阶段capture不移动原业务决策时点。

## 4. 新pp错误编码与typed outcome

所有下列自定义SQLSTATE恰五位且彼此唯一；仅新受审根/私有函数使用，不修改原迁移。映射以SQLSTATE精确匹配，Go返回原类型/Code并保留PgError cause；不能从任意Message/Detail猜业务错误。原Go事务外校验保留原返回。SQL根对应分支的本地安全Message使用原源码固定常量；同Code不同旧Message的实际分支用§4.1独立state，不透传PG文本。

| SQLSTATE | 原Go错误类型/Code |
| --- | --- |
| PP001 / PP002 / PP003 | procurement.ErrInvalid / ErrConflict / ErrForbidden |
| PP101 | billing.Error invalid_attempt |
| PP102 | key_ip_forbidden |
| PP103 | invalid_key |
| PP104 | forbidden（financial role required） |
| PP105 | binding_unavailable |
| PP106 | request_finished |
| PP107 | unpublished_binding |
| PP108 | request_deadline（request is no longer eligible for dispatch） |
| PP109 | unsafe_retry（prior attempt has no trustworthy non-consumption evidence） |
| PP110 | cost_unknown（controlled procurement requires a complete known quote） |
| PP111 | cost_quote_stale（configured procurement quote changed before dispatch） |
| PP112 | cost_limit_exceeded |
| PP113 | procurement_bound_invalid |
| PP114 | procurement_paused |
| PP115 | procurement_budget_exceeded |
| PP116 | idempotency_conflict（attempt sequence already used） |
| PP117 | channel_unavailable（channel is not available） |
| PP118 | profile_blocked |
| PP119 | procurement_binding_changed（frozen procurement identity is no longer eligible） |
| PP120 | attempt_already_dispatched |
| PP121 / PP122 / PP123 | invalid_evidence / invalid_usage / invalid_amount |
| PP124 / PP125 | procurement_invariant / invalid_cost |
| PP126 | procurement_not_found（attempt has no procurement hold） |
| PP127 / PP128 | procurement_inflight / supplier_refund_evidence_required |
| PP129 | self_approval（different maker and checker required） |
| PP130 / PP131 | procurement_evidence_stale / procurement_approval_required |
| PP132 / PP133 | invalid_reconciliation / reconciliation_not_found |
| PP134 | invalid_reason（release reason required） |
| PP135 | reconciliation_closed（case is already decided） |
| PP136 | reconciliation_not_proposed |
| PP137 | invalid_charge（charge exceeds reservation） |
| PP138 / PP139 / PP140 | invalid_account / missing_account / unbalanced_journal |
| PP141 / PP142 | amount_overflow / insufficient_balance |
| PP143 | metering_conflict，仅Reconcile内部需整根回滚时抛；公开可提交冲突禁止抛此state |
| PP190 | 仅原对外lookup缺行位置映射pgx.ErrNoRows；内部来源缺行不得用此state |

所有业务异常均回滚根，除下表明确typed返回不以SQL错误表达：

| 分支 | typed返回与Go外层处理 |
| --- | --- |
| 新账户拒绝command | outcome=applied，command.status=rejected、reason仅not_found/revision_conflict；完整root/source/audit后提交，原API返回Command无error |
| 正常新动作/精确旧回执 | 各根允许applied/replay，严格按共同字段重建原结果；快返无新来源。dispatch没有replay |
| 公开finalization冲突 | semantic_conflict，固定semantic_code=metering_conflict、冲突source身份完整、Finalization组NULL；COMMIT成功再返回原空Finalization+业务错误 |
| 采购恢复无动作 | noop，所有本次source/root/event/journal身份NULL；不是幂等成功派发 |

### 4.1 同Code的原Message分支

下表避免新SQL把多个原分支压成一个错误Message；均映射左列原Code，本地Message固定如下，不接开放reason字符串。事务外校验的更细原错误在Go直接返回；DB对不可能越过该校验的非法直调仍以对应invalid类拒绝，不赋可提交结果。

| SQLSTATE | Code / 固定Message |
| --- | --- |
| PP401 / PP402 / PP403 | forbidden / tenant is not active、membership is not active、user is not active（依次） |
| PP404 / PP405 | forbidden / model is not authorized、channel region is not authorized |
| PP406 / PP407 | forbidden / key model scope is not authorized、key channel region is not authorized |
| PP408 / PP409 | request_deadline / request deadline expired、request deadline expired while waiting for procurement |
| PP410 | unsafe_retry / prior attempt has no terminal evidence |
| PP411 / PP412 | cost_quote_stale / procurement quote changed before attempt、configured procurement quote changed before reservation |
| PP413 / PP414 / PP415 | cost_unknown / controlled procurement requires a complete quote、controlled procurement requires both known cost dimensions、route requires a known procurement quote |
| PP416 | procurement_binding_changed / credential was bound after this attempt was prepared |
| PP417 | procurement_not_found / procurement hold missing |
| PP418 / PP419 / PP420 | idempotency_conflict / finalization id already used、proposal identity already used、proposal already approved by another operation |
| PP421 | invalid_cost / canonical operation and proposal required（审批输入） |
| PP422 / PP423 | idempotency_conflict / cost operation already used、a proposed reconciliation is immutable pending checker decision |
| PP424 | self_approval / different maker and checker required（Reconcile，与PP129同文但独立分支） |
| PP425 / PP426 | reconciliation_closed / only pending holds can be manually released; settled charges require refund、hold is no longer pending |
| PP427 | invalid_charge / invalid raw charge |
| PP428 | channel_unavailable / channel credential is not available |

### 4.2 新闭合失败与原数据库错误

| SQLSTATE | 固定安全Code / 处理 |
| --- | --- |
| PP901 | procurement_execution_forbidden：session/role/入口authority不匹配 |
| PP902 | procurement_history_invariant：已受证source/head/原件/模板/双向结果不一致 |
| PP903 | procurement_execution_protocol：结果行数/NULL联合/typed DTO/调用协议不符 |
| PP904 | procurement_history_resource：合同资源预算超界 |
| PP905 | procurement_history_exhausted：新ordinal/step/member计数不能推进，禁止回绕 |
| 40P01 / 40001 | 保留原PgError cause，billing确定回滚且ctx未结束时最多三次；management无自动重试 |
| 55P03 / 57014 | 锁超时/取消或statement timeout；不重试，不包装成业务rejected |
| 42501 / 23503 / 23505 / 23514 / P0002 / 22xxx / 其他未列state | 原执行错误，不泛映射forbidden/idempotency/not_found，不吞完整性错误，不纳入通用重试。PB精确例外见下一节 |

新闭合Code返回受审安全错误并保留cause，不能把新不可证来源当成旧业务拒绝。非SQL执行错误、context canceled/deadline、scan/drain/close/网络错误也不按字符串归类。root返回applied并不代表deferred/COMMIT已通过；结果协议失败先回滚，不能退回旧SQL路径。

### 4.3 原PB固定文本的精确兼容例外

原 [txSQL](../../internal/periodicbudget/tx_types.go#L87) 与 [periodicError](../../internal/billing/periodic_budget.go) 保持。原020未为clock错误提供独立state/constraint，完全禁止文本判别同时保留原body与错误Code不可兼得；本候选明确保留以下**精确**旧分类，除此不解析本地化/任意异常文本：

| 原错误精确条件 | Go公开Code |
| --- | --- |
| Code=23514且Message恰periodic clock regression | periodic_clock_regression |
| Code=23514且Message恰periodic budget exceeded且ConstraintName=pb_budget_tenant或pb_budget_key | periodic_budget_exceeded |
| 同上且ConstraintName=pb_budget_procurement | procurement_periodic_budget_exceeded |
| PB helper source缺行/ErrTxClosed/原tx identity或来源不变量 | periodic_budget_invariant，保留原helper分类 |
| PB helper其他SQL错误（包括其他23514） | periodic_budget_unavailable，保留cause；不得因为相似消息变业务成功 |

此例外仅限原PB调用边界，不把非PB任意23514包装为periodic不可用；直接原trigger在外层COMMIT产生的未经helper分类错误保持原提交错误。完整SQL根调用原PB的异常边界必须精确定位到这些调用，若需新wrapper传递调用域，正文/catalog中固定，不根据PG Context文本猜域。原020/ABI不重写，future若去文本须另冻结函数tag/body与新profile。

## 5. 相邻路径的锁与权限顺序

| 原入口 | 与本候选相交的准确顺序/差异 |
| --- | --- |
| Reserve | 原输入/idempotency map/active map → request_idempotency INSERT冲突新语句比较（早于授权）→ LP(key U) → route/profile/bounds → price group S与effective price新DBclock → lifetime quota → CPB → wallet → request/reservation INSERT → customer admission → ledger/PB reserve → admission.At复核key/request expiry → Key/job/audit/outbox。新request birth trigger仅在此真实来源内记录，不把原幂等提前改成成功回执 |
| Settle / Release / MarkPending | LR；Settle已有settlement快返后才读DBclock/证据决定；Release先校验reason封闭枚举、LR后已有settlement快返否则直接finish；MarkPending reason1..80，LR后新clock，已到financial deadline转settleRequest(recovery)，否则仅pending状态/case。三者不加当前route/price/供应商gate |
| PB Commander | operation两int advisory域23023024 → commanderAuthorize。tenant scope先tenant N再actor S；key scope tenant/member/user S→key U；supplier scope actor S→account N。授权后原receipt重读；control N或首配namespace X→legacy普通枚举→原control/import/operation。supplier不从control/head回头锁account/p，既有account锁承担与writer互斥；最多1000导入限制保持 |
| BindCredential | 原管理command序 → channel U→revision检查 → credential S→revoked检查 → 普通existing binding→account N→binding INSERT；原014 raw binding trigger也按channel U→credential S→account N同步。无旧binding的一次读不等于可忽略channel屏障 |
| AddObservation | 原输入含UTC微秒时间归一化/状态金额联合 → 管理command序 → account N → observation/command/audit；不改H/S，不建account金额journal，不作为provider认证余额 |
| 后续Refund | [ApproveRefund](../../internal/billing/orders.go#L172)先refund row U与当前双角色，再LR→settlement U→wallet；只改refunded_amount等独立资金投影，不把旧CustomerFinishOutcome永久FK到今天refund数值。无采购account/head锁 |

价格发布与绑定隔离通过已有model/channel父锁串行；本页不授writer改发布版本、凭据secret或预算配置。PB APP配置与writer首配缺席同步的命名空间保持同schema；精确锁权和列DML隔离沿PB ACL合同，不能因纯锁需要恢复APP受保护UPDATE。

## 6. 批扫描与计数适配

### 6.1 RecoverProcurement

原批API limit<1或>100改100；独立pool查询p prepared/sending且request_deadline<clock_timestamp()，ORDER BY request_deadline,a.id LIMIT，**无lease/无SKIP LOCKED**。完整Rows.Err/Close并收集后才逐候选开独立事务；不得持扫描cursor执行根。候选APP调用两参恢复根，request从DB原attempt派生并锁后核归属，不能使用扫描旧request/deadline作为证明。

原n在每个无错误事务后都++，包括p已改变等无动作快返。因此候选明确保留：已确认提交的applied和noop均n++；它表示成功处理扫描候选数，不是新增PE/释放数，不作为coverage。单候选40P01/40001的内部重试不多计；错误立即返回已完成的n与该error，不继续后续候选。applied回复或COMMIT未知不能先计，再根据§7只读观察确认。新noop不建root，不能为计数造来源。重复批次重新扫描真实prepared/sending，不重发模型。

### 6.2 客户Recover

[原恢复](../../internal/billing/recovery.go#L16)与采购恢复不是同一算法：先事务内从未完成reservation补job；按next_run/request排序，due且lease空/过期、request deadline或financial deadline到期、reservation仍reserved/pending，FOR UPDATE OF job SKIP LOCKED，至多100；逐job lease_until=DBclock+30s/owner/attempts+1。claim切片在每次事务重试起点清空，不累计重复lease结果。

提交claim后，本机active且扫描Expired=false则延后并跳过；Expired只决定是否跳过本机活跃，不证明最终收费资格。逐项调用原Settle(mode=recovery)，必须在其锁后/finish再看DBclock。失败记首error并继续其他claim；成功Processed++，按实际返回status分Settled/Released，其他Pending++并延后。读取旧settlement也按原返回状态计数，不等于本次新资金effect。deferRecovery仍仅本owner条件清lease、next_run=DBclock+15s，其失败按原不覆盖首error，不伪造已完成计数。

采购恢复不复用客户lease计数，客户释放也不证明供应商无消费；本合同不让后台再次调用生成/provider。

## 7. 未知提交与结果发布

只有事务COMMIT明确成功后，Go向外发布typed applied/semantic_conflict或开始模型发送。函数返回但事务未提交时的root/ID/checked_at不构成许可。明确40P01/40001/ErrTxCommitRollback等已回滚按原事务策略；网络断开、取消或COMMIT确认丢失须区分是否已发送COMMIT，不能只凭defer Rollback返回就宣称未提交。不得把unknown视作可重试事务。

候选保留本次已取得的root/源身份与原operation/attempt/finalization identity，在新只读连接准确查询：账户operation、proposal operation、approval operation、correction operation、首次dispatch source、recovery root/PE和Reconcile source/outcome逐个闭合原件。root存在本身不够，要读取封闭结果与全部必要反向关系；查不到或无法唯一定位仍outcome_unknown，不换operation重新做资金/effect，不发送模型。读观察不得当作新经营授权。

公开conflict同input ID可有多个真实conflict原件，且无normal finalization回执；若本次root/冲突source ID尚未拿到，发现一条旧conflict不证明本次提交。保持unknown，保守观察，不以重调RecordFinalization来“查结果”。Reconcile approved/closed旧回执也按原结果合同读，不能用今天case状态推断本次批准是否发生。

标准billing当前wrapper只返回原Commit错误、cleanup忽略Rollback结果，没有PB Commander那种完整connection idle/销毁分类。以上unknown处置是候选adapter必须交付的增强，不能写成旧wrapper已经实现；[PB Commander](../../internal/periodicbudget/commander.go#L87)的连接占用/清理可作模式参考，但不得在未设计时直接套用其不同重试预算。实际扫描/只读观察API与typed异常尚待实现；无唯一证据时不返回applied、不放行生成。

## 8. 实施前核对与未完成边界

1. 逐入口将本锁图译成完整函数正文、原caller接缝与新head/来源模板，审查implicit FK/trigger边、same-request屏障和post-wait新语句。完整root ABI、公开pp_record_attempt替代三参后验根、expected BYTEA和private权限须与共同schema一次对齐；不能同时保留APP可调用后验出生旁路。
2. 新PP state表、本地固定Message与原PB精确例外需编译期/测试穷举；缺lookup/source、任意23505/42501/23514不能被宽泛改成业务成功。原Go事务外校验不变，候选直调拒绝也须严格typed输入/NULL联合，不把ignored输入转TEXT导致新拒绝。
3. 实际APP LOGIN验证每个入口授权/幂等优先序、多个等待跨key/request/financial期限与scheduled quote、binding缺席/后来绑定、原模型锁升级deadlock、三scopePB、同事务多根、Reconcile冲突整根回滚/公开冲突提交、单根5s、原3次/管理零重试、cursor/lease/计数、unknown只读观察。
4. 全部可安装SQL、准确表列/函数/type/default ACL、新catalog/readset/profile、独立业务oracle/完整scanner与最终商业数据库验收仍未交付。本文冻结逻辑，不替代这些材料，不解决B-P3历史缺证/catalog race或R2接线。

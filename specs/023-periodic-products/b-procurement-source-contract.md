# B-P4-D1-S：共同 root、typed source 与事务闭合合同

2026-11-03；基线 `2223e40a2947846541e34451b6b2465082c18bbe`。本页冻结两域汇合后的共同逻辑协议；[账户](b-procurement-account-contract.md)与[成本/request](b-procurement-cost-request-contract.md)按此对齐，旧工作提案保留当期记录。承接[权限/事务合同](b-procurement-writer-authority-contract.md)及[身份字节合同](b-procurement-identity-contract.md)。本文不授权先实现一个允许APP补写before的替代writer；完整可执行DDL、PB/权限闭包、读集、独立重放与新profile仍须配套完成，D1整体及I1未完成。

## 1. 唯一物理身份与标量

新root及普通source ID为DB生成的非零、小写规范UUID TEXT。APP不提供root/step/source ID；原request/attempt/tenant/operation等TEXT身份不在这里强转UUID、不缩小原接受集。request/attempt origin使用原自然主键，故RootStep.source_id不是统一的新UUID域。原操作允许nil UUID的边界也不因新source规则改变。

ordinal、step、count为BIGINT；非负/正值按字段定义，MaxInt64处拒绝推进，不环绕。created_xid为XID8，实际`pg_current_xact_id()`由DB覆盖；只核同top事务，不以其数值排序。所有新引用关系列除明确optional外均NOT NULL；NULL与空字符串、零、缺字段分离。新timestamp为finite TIMESTAMPTZ，仅作观察与期限，不充当操作顺序。新纯DTO对整数/金额使用规范十进制字符串；业务用量仍int64，来源金额依各域NUMERIC(100,0)合同。

统一公共source键为 `(source_id,root_id,step_no,step_kind,created_xid)`。具体表中主键叫id时source_id=id；origin分别为request_id、attempt_id。step_kind在具体表为不可由调用方选择的常量列或固定generated值。不得把各域另起的ordinal当成另一条attempt序列。

## 2. `pp_operation_roots`

| 字段 | 类型 / NULL | 精确责任 |
| --- | --- | --- |
| id | TEXT PK | DB新root，非零规范UUID |
| entry_kind | TEXT | 下表封闭入口枚举 |
| scope_kind | TEXT | request / account_command |
| tenant_id,request_id | TEXT nullable | request分支均非NULL，精确关联原request；平台分支均NULL |
| attempt_id | TEXT nullable | 按入口必须有或必须无；有时精确关联tenant/request/attempt |
| account_target_id | TEXT nullable | account_command分支非NULL，为实际原command.target_id；request分支必须NULL |
| created_xid | XID8 | 本根实际top-xid，DB覆盖 |
| session_role_oid | OID | 实际session_user的受审APP角色OID，不能用definer current_user替代 |
| step_count | BIGINT | >0；封闭模板实际步数 |
| semantic_result | TEXT | 下表每入口固定接受集 |

account_target_id不强FK到procurement_accounts：configure not_found合法且没有该行。create的input target沿旧digest是空字符串，但root的target是DB新账户ID；二者在account command source分列，不能混用。平台账户共享资源不能填一个假tenant。

| entry_kind | scope / attempt | 新根semantic_result |
| --- | --- | --- |
| request_birth | request / NULL | born |
| attempt_birth | request / 有 | managed / unmanaged |
| account_create | account_command / NULL | applied |
| account_configure | account_command / NULL | applied / rejected |
| dispatch | request / 有 | permitted |
| record_finalization | request / 可NULL | normal / conflict |
| propose_cost | request / 有 | proposed |
| approve_cost | request / 有 | approved |
| unmanaged_correction | request / 有 | corrected |
| recover_procurement | request / 有 | released / unknown |
| reconcile | request / NULL | proposed / approved / released |

只有实际新持久来源产生root；正常幂等快返、已完成审批快返、无动作recovery不新增root或step。持久化rejected配置命令是实际来源，仍有root；业务错误回滚与允许保存conflict后提交不是同一结果。

root由私有根函数局部预分配ID，**最后一次INSERT完整不可变行**，不建可更新pending root。中间source/step对root的FK为DEFERRABLE INITIALLY DEFERRED；缺root、错root、错template均不能COMMIT。根表禁止UPDATE/DELETE/TRUNCATE；不能通过另一函数在事务尾补一个自报passed根。

## 3. `pp_root_steps` 与唯一canonical child

字段：`root_id TEXT,step_no BIGINT>0,kind TEXT,source_id TEXT,created_xid XID8`，PK(root_id,step_no)，UNIQUE(kind,source_id)，并有覆盖五字段的复合UNIQUE。对root的deferred FK包含root_id/created_xid。每个root恰好包含step_no=1..step_count，不可跳号、重复或回退；由DB局部计数推进，不用GUC、当前最后行、trigger_depth或同xid猜本次来源。

| kind | 唯一canonical child | 同step的companion（不再计一个child） |
| --- | --- | --- |
| request_birth | pp_request_origins | 原request、request head初态 |
| attempt_birth | pp_attempt_origins | 原attempt、明确managed/unmanaged分类、attempt head初态 |
| account_command | pp_account_command_sources | 原procurement_commands；成功create有账户origin；found/create按账户合同关联journal |
| attempt_delivery | pp_attempt_delivery_sources | 真实upstream delivery变化；mode=dispatch_permit时managed才有dispatch journal |
| incoming | pp_incoming_sources | 原始FinalizationInput的精确字节身份 |
| meter_choice | pp_meter_choices | 需要时pp_meter_origins、pp_conflict_sources及其原meter/conflict |
| request_decision | pp_request_decisions | 原finalization或其精确conflict引用、request.latest变化 |
| normal_cost_decision | pp_normal_cost_decisions | 真实正常成本资格与before cut |
| cost_mutation | pp_cost_mutations | 实际upstream cost UPDATE |
| cost_reset_decision | pp_cost_reset_decisions | 应用/抑制reset决定；实际mutation另占cost_mutation step |
| evidence_choice | pp_evidence_choices | 原identity与必要的pp_evidence_members |
| procurement_transition | pp_procurement_transitions | 原PE、唯一账户journal及p/a投影变化 |
| proposal_birth | pp_proposal_births | 原proposal与proposed audit |
| approval_consumed | pp_approval_consumptions | 本次真实批准来源；结果由后续精确step/原件绑定 |
| unmanaged_correction | pp_unmanaged_correction_sources | 原provider correction、精确actor与输入 |
| recovery | pp_recovery_sources | 锁后真实期限/permit/状态决策 |
| cost_outbox | pp_cost_outbox_links | 原cost_recorded或cost_corrected outbox |
| reconciliation | pp_reconciliation_sources | 原case决策、角色/输入/截止与实际finish/settlement/audit |

每种kind使用一个generated nullable discriminator列指向对应具体表：仅选中kind的列等于source_id，其他列为NULL；每列的**复合deferred FK**包含该source id/root/step/kind/xid。具体child反向以其主键+固定kind+同组字段FK回pp_root_steps。合法联合必须恰好有一个选中child，不能用“任意表中存在同ID”验证。全部generated discriminator也进入新物理读集并逐值核验。

companion与canonical child采用按实际身份的双向复合deferred FK/等价固定约束，不能只附一个无约束source_id。原source本身有反向pointer，完整读取从两侧都能发现遗漏；RootStep计数或hash不能替代原件枚举。mutable投影仅FK当前末项/after，旧历史不能永久FK到未来变化的当前revision。

### 3.1 共享原 audit/outbox 的反向来源列

原`audit_events`及`projection_outbox`统一新增以下nullable列，不由两域各造一组：`pp_root_id TEXT,pp_root_step BIGINT,pp_source_kind TEXT,pp_source_id TEXT,pp_source_role TEXT,pp_created_xid XID8`。六项全NULL或全非NULL，原表的pp_root_step精确映射RootStep.step_no；完整五元组精确FK到对应canonical child/step，而不是FK到一个金额相同的cause。新列只由真实writer填入，业务payload及原action/kind/schema_version不改。

| 原件 | pp_source_kind | pp_source_role固定槽 | 正向来源保存的原件ID |
| --- | --- | --- | --- |
| 账户create/configure audit | account_command | account_command | AccountCommandSource.audit_id |
| 提案audit | proposal_birth | proposal | ProposalBirth.audit_id |
| 批准audit | approval_consumed | approval | ApprovalConsumption.audit_id |
| unmanaged更正audit | unmanaged_correction | unmanaged_correction | UnmanagedCorrectionSource.audit_id |
| reconciliation操作audit | reconciliation | reconciliation | ReconciliationSource.audit_id |
| reconciliation实际收费audit | reconciliation | settlement | CustomerFinishOutcome.billing_audit_id |
| reconciliation profile-block audit | reconciliation | profile_block | CustomerFinishOutcome.profile_block_audit_id |
| 正常成本或更正outbox | cost_outbox | cost | CostOutboxLink.outbox_id |
| reconciliation实际收费outbox | reconciliation | settlement | CustomerFinishOutcome.billing_outbox_id |

各原表在非NULL子集具有UNIQUE(pp_source_kind,pp_source_id,pp_source_role)，每槽与来源中对应ID双向精确绑定，不能用同一原件同时填两个槽。reconciliation的finish是同step companion，不能再造一个与ReconciliationSource竞争的canonical child。CostOutboxLink另有cause联合，原outbox指向link自身，不能直接跳到normal cost decision/approval/correction。每槽逐值核actor/tenant_scope或tenant_id/action或kind/target或operation/request/payload及原时间/版本合同。

候选保留其他业务的共享INSERT能力，禁止APP直接插入任何带pointer的行；受保护采购/成本审计族及cost_recorded/cost_corrected新outbox也不能借全NULL绕过。`procurement.command.applied/rejected`为既有共享action，须按真实command.kind/target及精确payload区分account_create/configure和绑定/余额观察，不得禁掉后两类。reconciliation消费的billing/profile-block原件必须完整，其他原资金入口不被本采购合同冒称已接线。新guard的完整谓词、触发时序、原件反向检查及最小列ACL随可执行DDL固定并实际攻击验收；本页不把列命名约定算成该验收完成。旧历史保持NULL且不回填，新候选出生后受保护来源不得伪装成旧历史。

## 4. 单一attempt链与独立局部顺序

`pp_attempt_nodes`为companion，字段：`id TEXT PK,attempt_id TEXT,ordinal BIGINT>0,previous_node_id TEXT nullable,root_id TEXT,step_no BIGINT,step_kind TEXT,source_id TEXT,created_xid XID8`。UNIQUE(attempt_id,ordinal)、UNIQUE(root_id,step_no)及覆盖前驱/step绑定的复合键。node必须与canonical step精确FK，反向要求相应step唯一node；id由DB生成，不能自报。

attempt_birth step本身不建node。其origin同时建立head ordinal0/tail NULL；同root后续managed reserved transition成为node1。其他有attempt的request-scope根**每个canonical step**恰有一个node，包括incoming、request_decision和outbox；无attempt根、account_command根禁止node。每个node从head真实旧tail递增，ordinal1仅prev NULL，之后精确prev ordinal-1/同attempt；head末项双向核。原request、account、evidence-member序列仍各自独立，不发明全局ordinal。

因此typed成本/证据行不再拥有另一套相互竞争的attempt ordinal；它们引用本step的node获取位置。MeterChoice created/reused均在incoming attempt链中有node，但只有真实创建的原meter在其原attempt建立MeterOrigin成员；复用其他request/attempt的旧meter不将该meter转归incoming attempt。other-confirmed-usage检查枚举当前cut内真实meter源，不能以单个exists witness代替absence/完整性证明。

request_decisions自己的request ordinal覆盖跨attempt及无attempt sticky前驱；evidence_members自己的member ordinal只在原identity实际新INSERT时推进。正常成本、提案及批准观察的attempt/member/account cut必须来自同组持锁后真实head；不能从当前head倒猜历史。

## 5. 来源先出生、结果精确闭合

私有effect helper只接受DB已出生source，cause严格联合如下：

| cause_kind | 来源 | 允许PE type |
| --- | --- | --- |
| attempt_birth | pp_attempt_origins | reserved |
| evidence_choice | pp_evidence_choices | released_no_consumption / consumption_unknown / cost_confirmed / evidence_disputed |
| approval_consumed | pp_approval_consumptions | cost_corrected |
| recovery | pp_recovery_sources | expired_without_permit / recovered_unknown |

真实源在锁后捕获before及完整资格，独立算出唯一合法target；DB预分配本次future transition/PE/journal/mutation/audit/outbox等输出ID，一次INSERT完整不可变source，再消费它执行结果。输出引用为同root精确deferred关系，实际RETURNING逐值核预期，最后seal root。即便同额批准也保留source、零delta PE/journal和实际成本UPDATE；p revision只按原状态/桶/anomaly规则变化。

禁止source先写一个pending行稍后UPDATE填before，也禁止最后才读终态补before；如果任何实际结果与预选目标不同、缺失或多出，全事务失败。source存在/XID相同/金额相同都单独不授写权。完整helper不接caller金额、before、member数、序号或passed。

EvidenceChoice无PE分支也明确记录真实原因和观察cut，包括重复identity、已terminal/已disputed、unknown保持、conflict event_key已存在。没有PE/配置/桶/blocking变化就不造假账户no_effect；实际零delta PE必须有journal。dispute内部cost reset、外层incoming-conflict reset和成本变化按真实先后单独step记录，不能合并终态。

## 6. 入口模板与精确嵌套

- request_birth：唯一request_birth step，只认证真实INSERT初态及其不可变身份/报价/硬界/期限；不冒称完整客户reserve或采购账户出生。
- attempt_birth：首步attempt_birth；unmanaged到此结束，managed还须唯一reserved procurement_transition及账户journal/PB效果。初始分类在原INSERT之后同事务完整闭合，原attempt反向deferred要求唯一origin；不能从AFTER INSERT暂时没p推断unmanaged。
- account_create/configure：唯一account_command step。成功create origin/head/journal一并闭合；configure found按真实成功/拒绝规则生成journal，not_found禁止账户journal。重试无新root。
- dispatch：唯一attempt_delivery，mode=dispatch_permit；真实upstream prepared→sending和p sending、客户/Key/采购PB guard及期限/报价在同根。managed唯一dispatch账户journal；unmanaged禁止journal。它不是可跨事务使用的token。
- record_finalization：incoming→meter_choice→request_decision必有且各一次。原normal分支按真实顺序含attempt_delivery(finalization)、normal_cost_decision、必要cost_mutation/cost_outbox；managed再有evidence_choice及其真实transition/reset。conflict禁止正常finalization/NormalCostDecision，但保存原incoming、真实meter选择和conflict原件，按原分支生成evidence/reset。精确分支字段与子顺序见成本合同；仅step数不能证明合法。
- propose_cost：唯一proposal_birth，保存原完整屏障及当前operator检查，audit为其companion；幂等快返仍先重核角色。
- approve_cost：approval_consumed先出生，后有唯一cost_corrected transition、实际cost_mutation及cost_outbox，原correction/approved proposal/audit逐一绑定；提案出生屏障不可改成更正后的当前状态。
- unmanaged_correction：unmanaged_correction先出生，随后实际cost_mutation/cost_outbox，原correction/audit闭合；无account/member/managed双审。原input ActorID不在幂等identity中的语义保留。
- recover_procurement：recovery先出生，后有实际attempt_delivery(recovery)及唯一recovery transition；锁后重核deadline及真实sending permit。无动作不生根，永远不重发模型请求。
- reconcile：reconciliation先出生。propose/release及approve在已过截止时没有incoming；批准且锁后原决定时刻未过截止才追加无attempt incoming→meter_choice→request_decision，原usage来自实际case、delivery来自实际request.latest。客户finish/settlement/PB/audit保持原子闭合，其完整资金图仍由原独立来源核验负责，不由本根替代。三个action都须接线，已有case快返先按原路径核角色、无新root。

多个根可在同事务顺序执行，前一个已seal源可成为下一个的明确不可变前驱。不同根的output不能串用；跨root只允许合同明定的旧meter、proposal birth、member、latest等来源。函数在同语句多次求值的文本顺序不授历史语义，只有DB锁后链序有效；实际死锁/serialization失败按原规则全回滚。

## 7. 结果与错误协议

Go公开方法保持原返回语义；候选executor选择实现，不授authority。SQL业务根采用固定typed返回，不把任意error字符串变成可提交冲突：`outcome=applied|replay|noop|semantic_conflict`，加每入口固定结果列（原行身份/时间等）；公开输入不接root_id。只在封闭入口确有的分支接受某outcome。

applied的root semantic_result沿§2；replay/noop无新root，返回既有结果身份或明确无动作。account configure的rejected是持久命令业务结果，由applied outcome返回status/reason，不与SQL失败混用。record_finalization的semantic_conflict允许公开外层提交事实后返回原metering冲突错误；相同私有子过程在Reconcile内部一律作为事务错误回滚，不seal一个“已批准但冲突忽略”的root。

业务拒绝沿原错误码优先级及包装，不新造通用可提交SQLSTATE。基础执行错误、唯一约束、权限、deferred不变量及资源/取消错误均不可被转换为业务成功。原23505（例如cost_recorded唯一身份已占）继续全回滚；不改成静默skip。SQLSTATE40P01/40001仅在确认旧事务回滚后按原有界次数重试；57014/55P03不是扩大后的重试依据。未知COMMIT只按原操作身份只读查询，不重做审批、财务或模型生成。

## 8. 权限、锁权与设计验证

writer与APP/owner/peer闭包、trusted-schema-first search_path及PB namespace沿上位合同。公开根先核actual session_user，DB自读的before、成员、前驱及所有ordinal不得由caller替换。原source与新typed历史共同不可变；raw APP改受保护source/成本/桶后补见证的路径必须关闭。

APP原PB Commander与绑定入口仍须锁共享采购account。候选选择只授原procurement_accounts.created_at单列UPDATE以满足PostgreSQL SELECT FOR UPDATE权限，并由原身份不可变规则及候选SECURITY INVOKER写guard拒绝APP所有实际UPDATE；行锁不执行UPDATE trigger。不新增可写noop列、不重授state/held/spent。actual LOGIN的[隔离实验](../../docs/verification/procurement-account-lock-acl.md)已验证四种行锁、列授权、拒绝实际UPDATE及竞争锁；实验guard无条件拒绝UPDATE，不证明真实writer允许分支或原014组合。最终完整profile攻击与旧PB绑定整链仍待验收。

具体根执行READ COMMITTED；按入口保持身份/父对象→request/customer reservation→attempt→account→supplier PB→p→新head的完整前层顺序，dispatch还有其原customer PB层。所有等待后使用新VOLATILE statement读取真实before和期限，不能以等待前CTE/STABLE快照发证。完整入口锁图、PB直接/trigger/deferred-commit访问矩阵属于配套合同，本文不声称这些细节已验收。

## 9. 退出条件与不可外推

必须完整冻结并实现所有上述来源、双向DDL、角色/函数/列ACL及默认/继承/PUBLIC闭包、原所有入口和嵌套、单attempt链与request/account/member前缀；之后固定全物理读集、独立纯重放/手算oracle、generated校验、basis/catalog/兼容、新显式profile及protected/observer/actual APP组合。缺任一域继续缺证，不能只用24→25或共享960/1000几个正例关闭十一项Unproven。

原SQL候选仍exact original001–022后隔离安装；旧checker/Base/V3/roots/原件不回填或重签。正式migration/provision/main/ready、全部来源钱包接线按R2独立交付，C1/C2/C3、商品/扩展、真实供应商/支付、容器/1Panel、生产/HA及最后性能范围保持。本文即使完成共同部分冻结，也不表示D1整体或完整writer/经营验收完成。

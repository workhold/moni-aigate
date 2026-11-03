# B-P4 来源与采购命令、审计和消息的内容关系

2026-11-04；审查基线 `b3fd5cb`。本页由根集成人冻结为本片实施合同；代码及验收结果分别记录。承接[共同来源§3.1](b-procurement-source-contract.md#31-共享原-auditoutbox-的反向来源列)、[账户§3.4](b-procurement-account-contract.md#34-pp_account_command_sources)、[成本/request](b-procurement-cost-request-contract.md)及[私有重放实施合同](b-procurement-replay-implementation-contract.md)。

## 1. 本片责任和既有边界

拟新增私有 `source_closure_payload_relations.go`、`source_closure_payload_relations_test.go`，在已有 `payloadRows` 的严格形状基础上，独立核验本次 raw Rows 中**内容相等和实际输出归属**。现有形状叶子能拒绝未知键、缺键、错误类型及金额格式，但不能发现合法形状内换 actor、tenant、target、operation、amount 或错误快照值。

本片不调用 writer、`AccountPrefix` / `AttemptCostHistory` / `ReconciliationFinish` 的计算 helper、Stats、Candidates 或旧 checker 报告作为 expected。可复用同一来源域的严格 JSON token 解析器和基本自然键/时间比较原语，按下文固定字段直接构造 expected；不把别域已经通过视为本域证明。

本片不重放完整计价、gate/blocker、sticky、成员屏障、真实角色撤权、历史 request/sent/PB 观察或全部状态机资格，不认证 actor 的当前角色能代表历史角色；这些保留各业务域责任。root/step/canonical/node/chain、generated、六 pointer 形状与整图资格仍由各既有来源叶子及后续 Qualified 闭包承担。ledger、客户和供应商 PB 精确效果由独立 effect-relations 片拥有，本片不修改其文件。

不改32表715列、40原件482列、任何已应用迁移、原 writer、旧 checker、公共 API、架构清单或数据库。集成人统一决定接线；`SourceClosure.Complete=false`、`Qualified` 为空及无公共 `Check` 保持。

## 2. 预算、JSON 相等和唯一性

- 入口接收原 `sourceClosureState`，复用唯一 `replayWork`、context、绝对 deadline 和预检预算。建索引、遍历候选/依赖、解析/比较字段都 `tick`，不逐叶重开预算；执行错误返回错误，由完整入口返回零结果。
- 对物理自然主键、输出槽、业务 operation 和各明确反向身份建多值索引。仅 `len=1` 才消费其内容；重复不能任选首行或末行。普通 ID 不追加 UUID、trim 或时间排序。
- JSON 沿既有严格 payload ABI，先确定 decoded 键/类型，再逐字段比较；对象键顺序、空白和合法 Unicode 转义不影响相等；字符串原值不 trim、不用 float64。金额字符串与 JSON number 不相等。Usage 的五计数必须原整数 token，转为已验证的非负 int64 后比较。
- 快照中的 RFC3339Nano 时间按实际时刻相等，允许合法 offset 表示同一时刻，保留微秒精度及 SQL NULL/JSON null 差异；不得把时间排成历史顺序。audit/outbox 自身 `created_at` 只核原物理 finite 合同，不强等诊断 source、root 或其他输出时钟。
- 已有严格形状不被内容关系放宽。关系失败不因另一个叶子已记录 scalar 错误而被当作成功。

## 3. 四类原采购命令

### 3.1 共同命令与审计关系

全部 `procurement_commands` 都保留原四 kind，操作身份为 `operation_id`。全部命令都须有且仅有一个 `procurement.command.applied/rejected` 审计；唯一性在**两种 action 的并集**上按 `target=operation_id` 计算，不能各取一条。

审计逐值为 `actor=command.actor`、`tenant_scope=NULL`、`request_id=''`、`action='procurement.command.'+command.status`、`target=command.operation_id`，`sanitized_diff` 正好 `{kind: command.kind, status: command.status}` 两个字符串。反向每条上述 command audit 须唯一绑定真实 command，并满足同一合同。未关联其他 action 的普通 audit 不因 target 字符串巧合进入本族。

`account_create/configure` 有新 typed source 时，该唯一 audit 的物理 ID 还必须等 `source.audit_id`，并具备精确六 pointer。`credential_bind/balance_observe` 沿原共享写面，command 和 audit 的采购前缀追踪列保持全 NULL，不制造 account command source、journal、root 或财务 outbox。

### 3.2 tracked account_create/configure

source 与唯一原 command 比较 `operation_id/actor/kind/target_id/input_digest/status/reason/created_at`，expected 分别取 `OperationID/ActorID/Kind/TargetID/InputDigest/CommandStatus/CommandReason/CommandCreatedAt`。原 command 的五个追踪列逐值等 source 的 `id/root/step/xid/journal?`；不能用 source 诊断 `created_at` 代替 `CommandCreatedAt`。

独立重算命令摘要，不 import 原采购输入类型或调用其他业务域 helper。按原 Go JSON ABI 构造有序结构：外层无 tag 的 `Kind,Target,Payload`；create payload 字段依次为 `operation_id,label,provider,state,limit_amount`；configure 依次为 `operation_id,expected_revision,label,provider,state,limit_amount`，值均为原字符串。对 `encoding/json` 默认转义后的字节 SHA-256 小写 hex，同时与 source 和原 command 比较；actor 不加入摘要。create 的摘要 Target 为空串，真实 `target_id` 为 DB 新账户 ID；不能混用。

found 分支须唯一取 `source.journal_id` 和对应 account origin/原 account：journal 与 source 的 `account/operation/command_source/root/step/kind/xid` 精确相等；account ID 等 TargetID；origin/account 的不可变 `created_at` 相等。快照的九字段为：

| JSON 字段 | expected |
| --- | --- |
| id | source.TargetID / journal.AccountID |
| label/provider/state/limit_amount | journal 相应 before 或 after 配置 |
| held_amount/spent_amount | journal 相应 before 或 after 桶 |
| revision | journal 相应 before 或 after configuration_revision |
| created_at | 唯一 account origin 与原 account 的不可变 created_at |

分支合同：

| source 分支 | journal | 原 before_state | 原 after_state | 额外内容 |
| --- | --- | --- | --- | --- |
| create applied | create、ordinal1 | JSON null | after 九字段 | after 配置等真实 source input，初始零桶/rev1由既有行内合同独立核 |
| configure applied | configure | before 九字段 | after 九字段 | before revision 等 InputExpectedRevision；after 配置等 source input |
| configure revision_conflict | configure_rejected | before 九字段 | JSON null | before revision 不等 expected；journal before/after 完全相等 |
| configure not_found | 无 journal | JSON null | JSON null | source/input target 相等；无按该 source 或 root 声称的 command journal |

configure 只改变配置，snapshot before/after 的 H/S 与 `created_at` 不变；配置 revision 按四配置字段真实改变精确 +0/+1，继续由本地 business union 和完整 account 域独立负责。内容关系必须逐值绑定真实 source/journal，而不是只比较两份伪造快照彼此一致。

not_found 是历史观察，不能要求**今天**不存在同 target 的原 account。source 缺失、重复或追踪部分 NULL 是 mismatch，不按旧命令豁免。只有原 command 五个追踪列全 NULL，且无新 source/journal 声称它，才属于旧来源边界。

### 3.3 legacy account_create/configure 的可证内容

无新来源时仍核四类 command 的快照形状和共同 audit 关系，以及可由不可变原件核实的 `id=target_id`、对应 account 唯一存在和快照 `created_at=account.created_at`。create 的合法初态零桶/rev1、configure 两端 ID/created_at/H/S 不变及配置 revision +0/+1 可由快照本地独立核，但这不证明其历史 before 是真实值。

**不把 legacy 快照的 label/provider/state/limit/H/S/revision 强等今天 account**，不从当前 head 给旧命令补 journal，不重构未保存的原 input 摘要。旧命令的本地关系通过仍保留来源 gap，不进入新 source Qualified。not_found 两端 JSON null 不要求当前 account 缺失。

### 3.4 credential_bind

applied 只消费 after Binding，before 为 JSON null；already_bound 只消费 before Binding，after 为 JSON null。其他三类合法拒绝两端均为 JSON null，不能以今天 credential/account/revoked 状态重判历史分支。

消费的 Binding 六个不可变字段 `id,credential_id,channel_id,account_id,operation_id,bound_at` 逐值等唯一 `credential_procurement_bindings` 原件；command.TargetID 等 binding.CredentialID。applied 还须 command.OperationID 等 binding.OperationID；already_bound 引用的是此前绑定的 operation，**不得要求其等本次被拒 command.OperationID**。

快照第七字段 `channel_revision` 来自原 writer 在该命令时刻 JOIN `channels.management_revision`，本次原件 DTO 不投影其历史版本。只保留既有正 int64 形状，不强等当前渠道值、不伪造历史认证。反向每条 binding 必须唯一绑定自己 OperationID 的 applied bind command，且快照精确匹配；额外 already_bound 拒绝可合法复用它。该旧共享写面通过不授新来源资格。

### 3.5 balance_observe

applied before 为 JSON null，after 的十三字段逐值等唯一 `procurement_balance_observations`：`id/account_id/operation_id/actor/status/amount?/currency/source/semantic/observed_at/provider_as_of?/evidence_ref/created_at`。command.TargetID/Actor/OperationID 分别等 observation.AccountID/Actor/OperationID。amount 的 SQL NULL 对 JSON null；provider_as_of 的 SQL NULL 对 JSON null，否则按实际时刻相等。

not_found 两端 JSON null，不证明当前 account 缺失。反向每条 observation 必须唯一消费自己 OperationID 的 applied observe command，并以其物理 ID 对上 snapshot；不从观察元数据推导账户余额、H/S/limit 或 supplier 认证余额。

## 4. typed audit 的全部内容槽

下表除 command 行采用 §3 外，均从**唯一 source 及其明确原件依赖**取得 expected；源与依赖的 tenant/request/attempt 身份必须一致，不得只借相同金额填槽。所有受新 source 声称的 audit 同时核物理 ID 及共同六 pointer 的 source kind/role/root/step/xid，精确角色槽仍见共同来源§3.1。

| 声明者 / role | actor | tenant_scope / request_id | action / target | 精确 JSON 内容 |
| --- | --- | --- | --- | --- |
| AccountCommandSource / account_command | ActorID | NULL / 空串 | procurement.command.{CommandStatus} / OperationID | kind=Kind,status=CommandStatus |
| ProposalBirth / proposal | Requester | TenantID / RequestID | procurement.cost_proposed / ProposalID | attempt_id=AttemptID,amount/basis/evidence_ref=原 proposal 对应字段 |
| ApprovalConsumption / approval | Approver | TenantID / RequestID | procurement.cost_approved / ProposalID | attempt_id=AttemptID,amount/evidence_ref=同一原 proposal 对应字段 |
| UnmanagedCorrectionSource / unmanaged_correction | EffectiveAuditActor | TenantID / RequestID | cost.corrected / ProviderCorrectionID | amount=Amount,basis=Basis |
| ReconciliationSource propose / reconciliation | Actor | TenantID / RequestID | reconciliation.proposed / CaseID | usage=Input 六字段,evidence_ref=InputEvidenceRef,reason=InputReason |
| ReconciliationSource approve / reconciliation | Actor | TenantID / RequestID | reconciliation.approved / CaseID | outcome=唯一 CustomerFinishOutcome.ResultStatus |
| ReconciliationSource release / reconciliation | Actor | TenantID / RequestID | reconciliation.released / CaseID | reason=InputReason |
| CustomerFinishOutcome / settlement | system | TenantID / RequestID | billing.{ResultStatus} / SettlementID | amount=ChargedAmount,reason=SelectedReason |
| CustomerFinishOutcome / profile_block | system | TenantID / RequestID | billing.profile_blocked / ProfileBlockModelID | waived=WaivedAmount |

proposal 内容依赖唯一原 proposal 的 `id/tenant/attempt/requester` 对上 birth；其冻结 expected revision/count 仍与 birth 明确对应，不从今天 p 值取。approval 使用自己 ProposalID 的唯一原 proposal，并核 ProposalBirthID 指向该 proposal 的原 birth、Requester/Approver/ApprovalOperationID 的原批准身份；不能借同额另一提案。该桥不重做当场 barrier、双审资格或完整 correction 的状态机。原 proposal 当前已 approved 时，birth 的 created payload仍使用其不可变原字段，不误要求当前 state=proposed。

unmanaged expected actor 优先按 input actor 存在性核 EffectiveAuditActor：SQL NULL 才为 system；不以空串替代 NULL，原件或输入没有 source 证据时不自报历史 actor。proposal/approval/unmanaged 的原 correction 与完整成本 target 另由成本域及其后续来源关系承担，本片不从 outbox 反填该事实。

approve 的 finish 必须是 Source.CustomerFinishOutcomeID 指定的唯一 companion，source/outcome 的 root/step/xid/tenant/request/case/source identity 全相等。propose 无 finish。billing audit 仅 new_settlement 声明新槽；pending/existing 不产生本次 billing audit/outbox。profile audit 仅本次真实 profile block 四列完整声明时存在，created/reused 都为本次审计，Target取 source 中真实 model/profile 身份；复用旧 block 不要求其旧 RequestID 等本次 request。

## 5. outbox 的 cause、link 和 payload

### 5.1 采购成本

原成本 outbox 的六 pointer 永远指 `CostOutboxLink` 自身（kind=cost_outbox, role=cost），不能直接指 normal/approval/unmanaged cause。每条 link 必须唯一绑定其 cause，scope/root/xid 相等、cause step 严格在前。cause 预分配的原 outbox ID 与 link.OutboxID 逐值相等。

| link.CauseKind | 必需 cause 内容 | outbox.Kind / OperationID | 精确 payload |
| --- | --- | --- | --- |
| normal_cost_decision | decision 为两种 write，SelectedAmount/Basis/CostMutationID/CostRecordedOutboxID 完整 | cost_recorded / AttemptID | attempt_id=AttemptID,amount=SelectedAmount,basis=SelectedBasis |
| approval_consumed | 唯一 ApprovalConsumption 及其同一原 proposal | cost_corrected / procurement:+ApprovalOperationID | attempt_id=AttemptID,amount/basis=原 proposal |
| unmanaged_correction | 唯一 UnmanagedCorrectionSource | cost_corrected / OperationID | attempt_id=AttemptID,amount=Amount,basis=Basis |

每个 cause 必须有且仅有一个匹配 link；normal 的三类 skip 没有 link 或原 cost_recorded 输出。实际 `CostMutation` 还必须唯一等 cause.CostMutationID、cause_kind/cause_source_id/scope/root/xid 对应、after status/amount 等 expected basis/amount，且 `cause.step < mutation.step < link.step`。不能用同 root 的另一笔同额 mutation 充当实际写后输出。完整 node 状态机仍由连续性和业务域负责，不从本片通过推断未消费的 mutation 合法。

原 outbox 物理 EventID 等声明 ID，TenantID 等 cause/link TenantID，SchemaVersion=1，CreatedAt finite，内容金额为原字符串（normal 可 NUMERIC100；人工 Money int64）。OperationID 的 `procurement:` 只在明确 approval 分支拼接，不按任意历史字符串前缀猜 managed/unmanaged。

### 5.2 客户 finish

仅 new_settlement 的 BillingOutboxID 产生本次槽，精确六 pointer 指同 step ReconciliationSource（role=settlement）；outbox.OperationID 等 SettlementID，TenantID 等 outcome.TenantID，Kind 等 ResultStatus，SchemaVersion=1。payload 正好三个字符串：`amount=ChargedAmount,released=ReservationAmount-ChargedAmount,request_id=RequestID`。差值以独立 big.Int 核算并保持非负 int64，不能溢出、转浮点或取原 outbox 自报 released 作 expected。

existing 只消费此前 settlement，不能重新要求或声明它的旧 audit/outbox带本次 pointer；pending 无财务输出。普通未被 Reconcile 声称的 customer finish 保留原资金 checker 范围，不能因同 kind 或同 request 字符串就变成新采购槽。

## 6. 双向集合、额外输出和 legacy

1. 正向逐个声明槽要求唯一真实原件，并比较内容及 pointer；原件缺失/重复、同原件被两个槽声称、跨类型同 ID 误用均失败。所有依赖也用多值索引，依赖缺失时不能以零值 payload 继续通过。
2. 反向逐条有任一非 NULL 六 pointer 的 audit/outbox，须回到唯一正确声明槽。指针完整但 action/kind/role 换成另一个合法族不能逃过；部分 NULL 同样失败。四类 command 的审计另按 §3 的全原件双向集合核验。
3. 为防止已知新槽旁边的额外全 NULL 原件伪装旧件，对真实业务身份构建二级反向集合：command 按 operation（两 status audit 的并集）；proposal 与 approval 分别按 action+proposal；unmanaged 按 action+correction；Reconcile 按 action+case；billing 按 action+实际新 SettlementID；profile 按 action+tenant/request/model；成本 outbox 按 operation+kind。**只有存在本次该业务槽的明确 source/原件声明时**要求对应集合恰含指定原件，不能按今天 request、case 最新状态或 timestamp 任意扩张其历史范围。
4. Reconcile 没有 P source 的旧 proposed audit，不因已有 A/R source 被判孤立；三个 action 分开。existing settlement 的旧 billing/profile audit/outbox，不因本次 approve/release 快返消费它而需要新 pointer。绑定/余额观察的共享 command audit 全 NULL 合法，仍须精确对上原 command。
5. 真实全 NULL、无本次 source 或 tracked 原件依赖声称的旧采购 audit/outbox，只运行既有形状及原 Base 的既定关系；本片不猜缺少的历史 source、不授 Qualified。不能只凭 action/kind 看起来受保护就把全部旧行当 mismatch，也不能把**已被本次 source 指定的原件**退回 legacy。
6. 二级反向集合只识别确定业务归属，不把 payload 自报的 attempt/request 字段当作来源凭证；自身 operation/target 与声明不符时仍由指定物理 ID 的正向关系报错。旧件与新件身份混同或反向多义时保留错误，不任选一种解释通过。

## 7. 错误归属

严格 JSON 类型/形状/本地值继续计 `InvalidScalarRows`。本片的原件内容、缺输出、归属、反向、多重消费错误计 `OriginalPointerRows`，统一用 `(物理表,自然主键)` 去重，不按原因或 JSON 字段数增加。

- source/command 声称缺失或错误输出：记录声明者；有实际被指定原件时也记录该原件。不存在的原件不制造假物理主键。
- 实际反向原件无唯一声明者或额外全 NULL 原件占用已知新槽：记录该原件；若唯一真实声明者已知，亦记录该声明者。
- 声明者本身重复、多个声明者抢同槽：按各真实声明者自然键记录（同自然键重复仍一次）；所有被声称的实际原件同样按其物理键记录。
- 依赖原件缺失/多义使 expected 无法取得：记录受影响的声明者及其实际输出；不把依赖缺失假记到另一个合法来源。依赖自身已有精确错误继续由其责任叶子独立记录。
- legacy command 与 binding/observation/audit 的关系错误同样按实际 command 和相关原件记录；对没有新 source 的旧历史不伪造 pp_ 物理行。

这一计数仅描述局部关系矛盾，不是十一命题的 gap/Proven，也不从其他 Stats 复制或扣减。

## 8. 独立验收矩阵

1. 四类 command 全合法分支字面 fixture；tracked create/config 的 applied、same-config、revision_conflict、not_found；旧 command 不同 current 配置/桶仍合法；not_found 后今天已存在同 target 仍合法。
2. account 九字段逐字段换值；original/source/journal 的 actor/target/digest/status/reason/time/root/step/xid/operation/journal ID 单变异；原command时间与source诊断时间不同合法；offset同刻合法，微秒差拒绝。
3. bind applied/old already_bound 两 command 共享原binding，拒绝错误地要求后者 operation=原绑定 operation；六不可变字段、bound_at、target逐项错；current channel revision 未读不伪造相等。binding/observation 反向漏 command、错 operation/多原件均拒绝。
4. observation 十三字段逐项错，amount和provider_as_of NULL/空串/JSON类型区分；timestamp offset同刻合法；当前账户桶不参与余额观察证明。
5. 九 typed audit/outbox槽逐项合法，合法shape内换actor/tenant/request/action/target/kind/operation/amount/basis/evidence/reason/Usage/outcome/waived；proposal当前approved仍保持birth payload合法；低层shape通过但内容变异必须被关系叶子捕获。
6. 三类成本cause→mutation→link独立字面顺序；skip多余link、缺mutation、交换同额mutation/cause、pointer跳过link、零额/同额更正、NUMERIC100正常成本与int64人工值、operation前缀碰巧相同但真实kind不同。
7. new/existing/pending，billing released=R-charge独立字面值，profile created/reused、旧block request不同；ordinary无Reconcile共享billing保持范围。
8. 每槽正反向删除/增加/重复/换物理ID/清空或部分pointer；同原件复用、同文本ID跨表不互抵、同source多缺陷只计一次、数组乱序以及已知新槽旁额外全NULL原件。
9. legacy无source、缺P但有A/R、无tracked槽的普通audit/outbox、共享bind/observe审计合法；被新source指定却抹pointer必须拒绝。
10. 大对象预算、context预取消/中途取消/绝对超时、完整checkSourceClosure错误返回零结果；不写PG测试、不将fixture通过标为商业或生产验收。

## 9. 仍保留的明确未完成项

PB operations 两列严格 JSON由[独立合同](b-procurement-pb-json-contract.md)与实现负责；历史 PB 配置/absence、未派生 RequestDecision 的 Reconcile request/latest/sent 观察、完整业务资格及逐对象 Qualified 仍未齐。legacy账户动态before、binding快照的历史channel_revision等未保存依据不因本片shape或内容部分通过获得认证；不扩DTO冒充已证。不以本片实施替代真实受限writer/guard/ACL、reader/basis/catalog或最终商业链路验收。

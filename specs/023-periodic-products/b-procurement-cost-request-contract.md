# B-P4 D1：request、attempt、成本与审批来源合同

2026-11-03。承接 [D0](b-procurement-prefix-design.md)、[写入权限合同](b-procurement-writer-authority-contract.md)、[身份 ABI 合同](b-procurement-identity-contract.md)，将[成本/request 工作提案](b-procurement-cost-request-d1-proposal.md)细化为实施前合同。**本页逻辑字段/联合/顺序合同已与[共同 root/source 协议](b-procurement-source-contract.md)及账户域汇合，经根复核后冻结。本页没有实现 SQL、业务 writer、reader、纯重放或新 profile；不表示 D1 全部完成。** 账户的全部来源、配置、pause、reserve/dispatch gate 与 journal 同样是必需合同，不由本页代替。

## 1. 范围与不可放宽项

覆盖安装后真实新 request/attempt 的全部 normal、conflict、managed/unmanaged 成本、提案/审批、多次绝对更正、派发、采购恢复及无 attempt 的 Reconcile。不得只支持 24→25 或只覆盖 invoice 路径。原十一项 Unproven、原 Base/计数和历史缺证保留；完整来源未覆盖时继续 indeterminate。

采用同一独立 NOLOGIN 最小 writer 的闭合 mutation。APP 只提交原业务输入，DB 在真实业务锁后取得 before、计算规则、生成原件及 typed source、完成投影/PB/audit/outbox，最后封闭 root。不得提供可由 APP 调用的“登记 before / 发见证 / 改金额”接口。权限、session_user、受信 schema 首位、RC、3s 锁/5s statement 及未知提交边界以写入权限合同为准。

本页冻结的是字段、联合、状态/顺序、原件双向绑定、返回及业务 oracle 的责任；完整可安装 DDL、函数正文、全部列 ACL、精确新 catalog/readset/basis 和实际 LOGIN 验收仍须另外交付。旧 migrations 001–022、B-P1 材料、旧 reader/checker/roots/golden 和默认 `billing.New(pool)` 不变。

## 2. 共同记录、类型和来源封闭规则

### 2.1 root、step 与 companion

共同 root 采用封闭二分联合；以下字段责任与[共同合同](b-procurement-source-contract.md)逐值对齐，实际共同表/键以该合同为准，不另建第二套 root/step：

- request scope：`tenant_id,request_id` 非 NULL，`attempt_id` 按入口可 NULL，`account_target_id` 为 NULL；原 tenant 保持 TEXT，不虚构 UUID 或平台租户。
- account_command scope：`tenant_id,request_id,attempt_id` 全 NULL，`account_target_id` 非 NULL，仅允许账户 create/configure；not_found 的目标文字不强 FK 到不存在的账户。

共同 `OperationRoot`（`pp_operation_roots`）包含 `id,scope_kind,entry_kind,tenant_id?,request_id?,attempt_id?,account_target_id?,created_xid,session_role_oid,step_count,semantic_result`。新 root ID、top xid、实际 APP LOGIN 的 OID 均由 DB 产生，入口没有 caller root/xid/step/count 参数。root 是全部步骤完成后的最后一次完整不可变 INSERT；中途来源的 root FK 为 deferred，没有可变 pending root 可被补写。

`RootStep`（`pp_root_steps`）的字段为 `root_id,step_no,kind,source_id,created_xid`，`(root_id,step_no)` 唯一，step 为 1..step_count 无缺口；`(kind,source_id)` 唯一。**每个 step 恰一个 canonical typed child。** 原业务行、MeterOrigin、AttemptNode、account journal 等为精确 companion，不是第二个 child；一个 child 不能同时冒充两个 kind。禁止 `procurement_transition/account-effect` 等多义 kind。

本页下文记 `S` 为每个 canonical child 必备的显式字段 `id,root_id,step_no,step_kind,created_xid`；`I` 为其明确的 `tenant_id,request_id,attempt_id?`。origin 是自然主键例外：RequestOrigin 没有另一 generated id，以 request_id 为 source_id；AttemptOrigin 同理以 attempt_id 为 source_id。step_kind 是本表固定常量，不能 caller选择。它们是物理列/纯 DTO 字段，不是 caller JSON。共同source键为 `(source_id,root_id,step_no,step_kind,created_xid)`；RootStep 按 kind 的封闭 discriminator 精确指回具体子表，child 反向精确指回共同键。合法原件也反向指回其唯一 source，不能只从 source 单向查到一条存在的原行。

本页名称与共同物理表精确对应：RequestOrigin=`pp_request_origins`、AttemptOrigin=`pp_attempt_origins`、IncomingSource=`pp_incoming_sources`、MeterChoice=`pp_meter_choices`、RequestDecision=`pp_request_decisions`、AttemptDeliveryMutation=`pp_attempt_delivery_sources`、NormalCostDecision=`pp_normal_cost_decisions`、CostMutation=`pp_cost_mutations`、CostResetDecision=`pp_cost_reset_decisions`、EvidenceChoice=`pp_evidence_choices`、ProcurementTransition=`pp_procurement_transitions`、ProposalBirth=`pp_proposal_births`、ApprovalConsumed=`pp_approval_consumptions`、UnmanagedCorrectionSource=`pp_unmanaged_correction_sources`、RecoverySource=`pp_recovery_sources`、CostOutboxLink=`pp_cost_outbox_links`、ReconcileSource=`pp_reconciliation_sources`。MeterOrigin、ConflictSource、EvidenceMember 均为各自 step 的 companion，不在 canonical kind 枚举另占一项。

companion 的共同键记 `C=(root_id,step_no,step_kind,source_id,created_xid)`，其中 source_id 是所属canonical child的公共source_id，不是companion自身ID；每个companion行还保留自身的原件自然键/新ID。固定step_kind、source_id及精确复合FK不能由一个无约束的meter_choice_id/evidence_choice_id替代。所有新增typed/companion表统一step_no，不另写root_step别名；原业务表新增pp_root_step显式映射本step_no。

同一步 companion 必须在原件身份及 `tenant/request/attempt/root/step/top-xid` 的相应精确键上闭合；涉及账户还要 `account_id`，涉及 PE 还要 `event_id,type,event_key`。跨 root 仅可引用明定的不可变旧 meter、已完成 origin/member、proposal-birth、prior latest 等前驱。不能拿同 xid 的任意另一来源代替本次 source。

### 2.2 不可变 source 先出生再消费

审批、恢复等 source 含将来效果 ID 时，根在锁后读取真实 before、独立计算 target、预分配输出 ID，再一次 INSERT 完整不可变 source；其输出 FK 为 deferred。私有 helper 只消费这条已出生且精确匹配本 root/step 的 source，实际 RETURNING 必须与预定 target 逐值相等。缺输出、额外输出、不同金额、wrong source 或 seal 不完整，全部回滚。

不能最后读终态补 before；不能把 source 先写 pending 再补字段；不能让 helper 直接按 proposal ID、当前最后行、GUC、query text、trigger_depth 或 xid 猜批准。可在根局部变量预分配多个后续 step/source ID，但每个 source 仍只能完整 INSERT 一次，根最后 seal。

### 2.3 标量与 DTO

| 域 | SQL/纯 DTO 合同 |
| --- | --- |
| 原 ID | 原 TEXT 原样保存；原 `validID` 接受集按身份 ABI 合同，不用 UUID cast 规范化身份文字 |
| 新 root/source/node ID | DB生成非零canonical小写UUID text；不接caller值，origin自然键例外 |
| xid/role | xid8 与实际 session role OID；不可 caller 覆写；时间不能代替顺序 |
| ordinal/step/count/revision | BIGINT 非负，需递增时先核 MaxInt64；DTO 为规范十进制 string，无环回 |
| usage/rate/input-output 硬界 | BIGINT/Go int64，非负；usage 五项计数全有，不能用省略/NULL 替代显式 0 |
| sequence / boolean / enum | sequence为INTEGER 1..2；content/streaming/anomaly等为BOOLEAN；状态/角色/分支为本文封闭TEXT枚举 |
| route_decisions | 原JSONB数组，沿原validatedRouteDecisions的类型、<=1000项/262144字节与selected匹配合同，逐值等原attempt；不是开放的before/after JSON见证 |
| 原 correction/proposal amount | 0..MaxInt64 的 Money；原 NUMERIC 投影/计算保持 NUMERIC(100,0) 可表示范围 |
| 金额 DTO | 无加号、前导零、负零、小数、指数的十进制 string；delta 可负，amount/bucket 非负 |
| optional | 明确 JSON null；空 AttemptID 仅在原输入边界映射 NULL；不以 omitted/空串/0 混淆 |
| 时间 | finite UTC timestamptz，精确区分输入ObservedAt与DB effective/checked时刻；不用时间排列source |
| usage status | confirmed_final/provisional/estimated/missing/conflicting，五项均非负，cached≤input，reasoning≤output；confirmed_final 用独立大整数核 input+output=total |
| 字节长度 | source 1..80、source_event_id 1..200、finish_reason 0..80；按 UTF-8 octet_length 等价 Go len，不能按字符数放宽 |

文本 NUL/非法 UTF-8 不在 PG TEXT 域，不得替换/规范化为另一可提交身份。原 proposal/approval 的表 CHECK（包括 canonical 操作 ID、proposal 与 approval 操作不同、受控 evidence/reason）继续有效，不能因 `validID` 解析器更宽而放宽数据库接受集。

finalization/evidence digest 由 DB 调用已冻结身份 codec 再 sha256(bytea) 计算，禁止接 caller_digest、JSON 字符串或 `jsonb::text` 作为权威。finalization identity 的 ObservedAt 恒编码 Go 零时刻，raw stage/content 保留原输入；归一化后的 sticky 值另存。evidence identity 精确为 Usage 全字段及原 TransportEndReason，不能从旧 meter 或摘要猜 reason。

### 2.4 业务根输入 ABI

下列全部是原业务输入，不附root/source/before/金额计算结果。Usage展开为固定六列：`usage_status TEXT,input_tokens BIGINT,output_tokens BIGINT,total_tokens BIGINT,cached_tokens BIGINT,reasoning_tokens BIGINT`；本页记为 `U`，不是JSON参数。

| 函数 | 精确typed输入（按次序） |
| --- | --- |
| pp_record_finalization | `finalization_id TEXT,tenant TEXT,request TEXT,attempt TEXT NULL,source TEXT,source_event TEXT,U,protocol_outcome TEXT,transport_end_reason TEXT,delivery_stage TEXT,finish_reason TEXT,content_written BOOLEAN,observed_at TIMESTAMPTZ NULL` |
| pp_propose_procurement_cost | `tenant TEXT,attempt TEXT,operation TEXT,actor TEXT,amount BIGINT,basis TEXT,evidence_ref TEXT,reason TEXT` |
| pp_approve_procurement_cost | `proposal TEXT,operation TEXT,actor TEXT`；tenant/request/attempt/amount/basis/evidence只从实际proposal取 |
| pp_record_unmanaged_cost_correction | `tenant TEXT,attempt TEXT,operation TEXT,evidence_ref TEXT,basis TEXT,actor TEXT,amount BIGINT` |
| pp_recover_procurement_attempt | `tenant TEXT,attempt TEXT`；不额外接request/deadline/permit |
| pp_reconcile | `tenant TEXT,request TEXT,actor TEXT,action TEXT,U的六个nullable参数,evidence_ref TEXT NULL,reason TEXT NULL`；按§9的action联合只传实际消费字段 |

RecordAttempt改由[物理布局§6](b-procurement-schema-contract.md#6-校准完整记录attempt的公开db根)的完整pp_record_attempt根执行；原三参closure仅private，dispatch仍两参。Go原公开输入类型不删字段、不改原API；candidate adapter只将原业务本就不消费的Reconcile字段丢弃，其他全部原样传递。Reconcile忽略字段不会成为identity或资格，也不能先转换PG TEXT导致原来被忽略的NUL/非法UTF8输入新增失败。

## 3. 出生与局部顺序域

### 3.1 RequestOrigin / RequestHead

`RequestOrigin` 是 canonical request_birth child，字段为 `S` 加：

```text
tenant_id,request_id,key_id,owner_id,model_id,
price_version_id,input_rate,output_rate,input_upper_bound,output_limit,
profile_version,policy_version,request_hmac,route_revision,
request_deadline,instance_id,streaming,client_ip?,
pricing_group_id?,tenant_pricing_revision,retail_priced_at,
request_created_at,pb_created_xid,pb_tenant_control?,pb_key_control?,pb_admitted_at,
initial_latest_id(NULL),initial_status(accepted),initial_billing_status(reserved)
```

直接从真实 request INSERT 的 NEW 及 DB birth stamp 捕获，不能给 APP 可选的历史注册接口。原身份、硬界、期限、冻结零售快照、路由/策略/profile 和 PB birth 字段自出生后不可改；其他领域的合法客户资金状态变化另按其原根，不以整表 UPDATE 开洞。`RequestHead(request_id,ordinal,last_decision_id?)` 出生为 `(0,NULL)`，只由准确前驱推进；head/current latest 与完整 RequestDecision 链末项闭合。

自动出生 root 只证明 request 初态，不等于客户 reserve/PB 已完成。原同事务的 reservation、资金、PB、审计/消息约束仍须满足，不能靠 origin 取代硬预占。旧 request 不得用当前行补 origin；未受证历史继续缺证或在 prospective 执行器明确拒绝。

### 3.2 AttemptOrigin / AttemptHead / AttemptNode

`AttemptOrigin` 是 canonical attempt_birth child，字段为 `S` 加：

```text
tenant_id,request_id,attempt_id,sequence,channel_id,channel_version_id,
credential_id,upstream_model,route_decisions,
cost_version_id?,cost_input_rate?,cost_output_rate?,cost_basis,quote_selected_at,
attempt_created_at,pb_created_xid,
initial_delivery_state(prepared),initial_cost_status(unknown),initial_cost_amount(NULL),
management_mode(managed|unmanaged),account_id?,binding_id?,
procurement_transition_id?,reserved_event_id?,account_journal_id?,admitted_at?
```

managed 联合的 account/binding/transition/event/journal/admitted_at 全非 NULL，精确指真实新 p、reserved PE、唯一 journal 与 DB admission时间；unmanaged 这些列全 NULL，以本 origin 在真实 binding 屏障后的显式分类为出生来源，不另建第二套 origin 或无来源的“无p”布尔。它对应原业务的 legacy_unmanaged 分类，不表示可以登记旧历史。全 quote、身份、route_decisions 和初态与原 attempt 精确相等；DB 对锁后当前 quote、credential/channel membership、请求硬界和期限做原规则核验，不能采纳 APP 伪造的输入费率。

候选公开`pp_record_attempt`完整执行原RecordAttempt的前层检查/DB选价/INSERT和采购出生，APP没有attempt INSERT；三参`pp_complete_attempt_birth`固定为同根private helper，无APP EXECUTE。其channel/credential/实际binding屏障与账户片一致；不能在AFTER INSERT暂无p时认作unmanaged。quote_selected_at由原选价语句实际DB采时，写入原pp_quote_selected_at并与origin精确一致；unmanaged不在closure重新选价，managed仍按原account/PB等待及admission时点复核。该时刻不证明旧目录完整可见集合，不能从今天新插入的版本倒推旧absence。deferred 反向要求真实新 attempt 恰一个完整 origin；managed 的 p/reserved PE/account journal 同根闭合，unmanaged 明确来源禁止 p。DB birth stamp、不可改初态/身份与 source 反向约束共同防止“先改后改回再补生”，仅 xmin=current 不充分。重试已有 tracked attempt 不重生 root。

`AttemptHead(attempt_id,ordinal,last_node_id?,member_count?,member_tail_id?)` 为完整 attempt 局部序列，物理表为 `pp_attempt_heads`。唯一 companion `AttemptNode`（`pp_attempt_nodes`）接入所有有 attempt 根的 canonical step：

```text
id,attempt_id,ordinal,previous_node_id?,root_id,step_no,step_kind,source_id,created_xid
```

同 attempt ordinal 连续，前驱是准确上一 node，source↔node 一一绑定；node 不另算 canonical child。**有 attempt 的 request-scope 根除 attempt_birth step 本身外，每个 canonical step 恰一个 node**，包括 incoming、request_decision、meter_choice、attempt_delivery、normal_cost_decision、cost_mutation、cost_reset_decision、evidence_choice、procurement_transition、proposal_birth、approval_consumed、unmanaged_correction、recovery、cost_outbox；不能只挑有金额变化项。typed 行如需 ordinal/prev，精确引用本 node，不自建另一条 attempt 序列。无 attempt 根全部 step 无 node，包括 Reconcile。attempt_birth 不建 node，origin 后 head 为 `(0,NULL)`；managed 出生的采购 reserved transition 为 node1。member 起点 managed 为 `(0,NULL)`，unmanaged 两项均 NULL。member_count 与 tail 不是完整性证明，必须能枚举全链且原 identity 反向无漏。

新 meter 以其 created MeterChoice node 在其**原本 attempt** 成为 membership；即使随后成为 conflict、没有 normal finalization，仍在该前缀内。复用跨 request/attempt 的 meter 只建本次选择边，不将原 meter 移归 incoming attempt；另一个 confirmed usage 的完整集由原 meter 归属和此前已完成 choice 节点枚举。

## 4. 原始输入、meter 与 request sticky

### 4.1 IncomingSource

canonical incoming child 为 `S + I` 加：

```text
input_finalization_id,input_digest,source,source_event_id,
usage_status,input_tokens,output_tokens,total_tokens,cached_tokens,reasoning_tokens,
protocol_outcome,transport_end_reason,raw_delivery_stage,raw_content_written,
finish_reason,observed_at_input?,effective_observed_at,reconcile_source_id?
```

public normal 输入必须原样捕获；ID 为空仍由 Go 事务前生成一次，不在自动重试中换身份。outcome 仅 completed/failed/incomplete/cancelled/unknown；reason 仅 done/eof/provider_error/platform_error/platform_timeout/client_cancelled/connection_error/unknown/not_sent/rejected_no_consumption；stage 仅 none/committed/content/unknown。输入 ObservedAt 未给时保存 NULL，effective 使用 DB 本次时钟；非空则逐值保留并用于 meter.happened_at。二者均不影响原 identity digest。

`reconcile_source_id` 仅实际 typed Reconcile approve 子流程可填，公开 caller 无该参数；source 文本为 approved_reconciliation 不授审批能力。

### 4.2 MeterChoice / MeterOrigin / ConflictSource

canonical meter_choice child 为 `S + I` 加：

```text
incoming_source_id,meter_event_id,meter_origin_id,
disposition(created|reused),attempt_cut_before_id?,attempt_cut_after_meter_id?,
collision_kind(none|source_payload_mismatch|other_confirmed_usage),competing_meter_id?
```

companion `MeterOrigin(meter_event_id,incoming_source_id,C)`（`pp_meter_origins`，step_kind=meter_choice/source_id=本次MeterChoice.id）只在原 meter 真正 INSERT 时建立一次；reused 指向旧真实 origin。原 meter 保留全部原字段：id/tenant/request/attempt/source/source_event/schema_version/normalized_usage/happened_at/received_at，并反向精确指 origin。旧无 origin 的 meter 被引用时继续缺证，不能补造其 incoming。

根先按 `(tenant,source,source_event)` INSERT/ON CONFLICT；等唯一键之后以**新 statement**取旧 meter，逐值比 usage/request/attempt。mismatch 优先于 other-confirmed 判定，目标仍是 incoming attempt。只有未 mismatch、incoming attempt 非空且 usage 为 confirmed_final，才检查同 attempt 完整 meter 前缀中另一条 confirmed_final 且 usage 不同的 meter；新 meter 自身排除。other 分支须保存真实 `competing_meter_id` 且能完整枚举 cut，布尔或一个 witness 不足以证明 absence；mismatch/none 的 competing ID 均 NULL。

`ConflictSource`（`pp_conflict_sources`）是本次 meter_choice step 的 companion，字段为 `conflict_id,C,tenant_id,request_id,attempt_id?,incoming_source_id,meter_choice_id`；step_kind=meter_choice/source_id=MeterChoice.id，不另建 canonical child/step/node。唯一绑定原 conflict 的 `id,event_id,normalized_usage,created_at`，且与本 meter choice/incoming 的 root/step/归属闭合。原表无 incoming/reason/tenant 列，由此 companion 的精确原件和关系补足；不能拿旧 meter 归属填入 incoming。每次真实 conflict 均生成新原 conflict 与 companion，即使原 finalization input ID/usage 相同。

### 4.3 RequestDecision

canonical request_decision child 为 `S` 加：

```text
tenant_id,request_id,ordinal,previous_decision_id?,incoming_source_id,meter_choice_id,
before_latest_id?,after_latest_id?,before_status,after_status,
normalized_delivery_stage,normalized_content_written,
branch(normal|conflict),finalization_id?,conflict_source_id?
```

在原 tenant SHARE→membership SHARE→key UPDATE→model SHARE→customer reservation UPDATE 屏障之后，新 statement 取得 request latest/head，且在 sticky/meter/本次写之前保存。先将 raw content=true 或 raw stage=content 归一化为 content/true，再消费准确 before_latest：前驱 content/true 则持续 content/true；否则前驱 committed 且 incoming none/unknown 则 committed。前驱可来自不同 attempt 或无 attempt；按 request 链核，不按 attempt 链或时间排序。

normal 联合：finalization 非 NULL、conflict_source NULL、after_latest=本 finalization；原 finalization 的 tenant/request/event/input_digest/outcome/reason/stage/content/finish/policy 逐值匹配。request status 为 reason=client_cancelled 时 cancelled，否则 completed 时 succeeded，其余 failed。conflict 联合相反，after_latest=before_latest，不能制造 normal finalization；markPending 沿原规则，已 settled/released 的客户资金不被重开。

RequestHead仅认证latest/sticky的ordinal顺序，不能替代request.status/billing_status的完整生命周期。before_status是在本次受审根锁后真实capture；跨dispatch或普通客户finish时，不强令它等于上一RequestDecision.after_status或origin.accepted。本decision.after_status按本分支核验；其他合法入口后续改变current status不使旧decision失效，也不能据此省略latest前驱的精确重放。

每个新 finalization/conflict 流程恰一个 incoming、meter choice、request decision，正常快返全无新行。原 finalization 反向只允许唯一 normal decision；原 conflict 反向只允许唯一 conflict source；request latest只能指完整 normal 链当前末项。source-key复用的旧 meter 不构成新 latest 的隐式前驱。

## 5. delivery、正常成本、reset 与当前成本

### 5.1 AttemptDeliveryMutation

canonical attempt_delivery child 为 `S + I` 加：

```text
mode(dispatch_permit|finalization|recovery),cause_source_id?,
before_delivery_state,after_delivery_state,
dispatch_account_id?,dispatch_journal_id?,dispatch_xid?,pb_dispatch_checked_at?
```

normal finalization 后无条件按 completed→completed、failed→failed、其余→unknown 写原 delivery，即使值相等也保留该实际动作；conflict 不伪造此 normal 动作。finalization的cause_source精确引用本次RequestDecision(branch=normal)，由其finalization_id确定目标；recovery引用本次RecoverySource。二者dispatch专有字段全NULL。dispatch使用同一`pp_attempt_delivery_sources`的mode=dispatch_permit，cause_source为NULL，**没有另一张DispatchSource**；真实prepared→sending与其managed account journal为companion，不能为同次发送再建第二child。首次sending的实际pointer/xid/PB checked_at之后不可改，delivery后续completed/failed/unknown不抹permit；managed的dispatch_account/journal全有，unmanaged两项均NULL，但真实upstream pointer/xid/PB时刻仍有。

### 5.2 NormalCostDecision

canonical normal_cost_decision child 为 `S + I` 加：

```text
finalization_id,incoming_source_id,meter_choice_id,
before_cost_status,before_cost_amount?,management_mode,
before_procurement_revision?,before_procurement_state?,before_procurement_held?,
before_procurement_spent?,before_procurement_anomaly?,
member_count_before?,member_tail_before?,account_id?,account_ordinal_cut?,account_tail_cut?,
decision,selected_amount?,selected_basis?,cost_mutation_id?,cost_recorded_outbox_id?
```

managed 的 p、成员和账户 cut 全有（只有零成员 tail 可 NULL），unmanaged 全 NULL且必须有明确 origin。before 在 request/customer-reservation→attempt→account→supplier PB→p 的锁后、正常 cost UPDATE 与本次 identity/采购 effect 之前取得，真实对应当时 `recordAttemptCost` 的资格，不能由成本/approved AFTER 重读投影代替。

固定优先级如下；不能因为后来进入 dispute 改写早前的选择：

1. status != unknown：skip_already_known；即使同时 disputed，也保持此首要分支。
2. 否则 managed p=disputed：skip_disputed。
3. 否则 reason 为 not_sent/rejected_no_consumption：write_no_consumption，amount=0、basis=contract。
4. 否则 confirmed_final 且 frozen 两维费率均有、basis!=unknown：write_final_usage，按 `ceil((input*rateI+output*rateO)/1000000)` 计算，basis 为冻结值。
5. 其余 skip_no_final_usage_or_rate。

write 的 amount/basis/mutation/outbox 全非 NULL，skip 全 NULL；conflict 没有新 normal finalization，禁止 NormalCostDecision。normal 相同 ID+digest 快返也禁止新 decision。

### 5.3 CostMutation / CostResetDecision

canonical cost_mutation child 为 `S + I` 加：

```text
cause_kind(normal_cost_decision|procurement_dispute_reset|incoming_conflict_reset|
           approval_consumed|unmanaged_correction),cause_source_id,
before_status,before_amount?,after_status,after_amount?
```

每次真实受控成本 UPDATE 当场核 OLD/NEW，绑定准确 cause；unknown⇔amount NULL，contract/invoice⇔非负 amount。所有实际 UPDATE（含 unknown→unknown、同额同 basis）都保留，不能仅记终态。当前成本从 origin unknown/NULL 按 AttemptNode 中所有 mutation 顺序重放，等于当前原 attempt 的 cost_status/amount；不按 cost_recorded 或最近 correction 猜顺序。

cause必须有精确discriminator/FK：normal_cost_decision→本root的NormalCostDecision(write分支)；procurement_dispute_reset/incoming_conflict_reset→本root对应kind的CostResetDecision(applied)；approval_consumed→ApprovalConsumed；unmanaged_correction→UnmanagedCorrectionSource。source的cost_mutation_id也反向绑定本行，不以相同金额或同xid借用mutation。

canonical cost_reset_decision child 为 `S + I` 加：

```text
cause_kind(procurement_dispute|incoming_conflict),cause_source_id,
procurement_state_before?,policy(applied|suppressed_adjudicated),cost_mutation_id?
```

新采购 dispute 的 reset 必须 applied；外层 incoming conflict 在 managed p 已 settled/released 时 suppressed，不擦除 invoice，否则 applied；unmanaged 没 p，外层 reset 为 applied。原流程可能先采购 dispute reset、再外层 conflict reset，第二次即使 unknown→unknown 仍是独立真实 mutation。采购 state 先变后读的外层 suppression 与采购变更前 before 分别存，不能混成一个状态字段。

procurement_dispute的cause_source指本root的ProcurementTransition(type=evidence_disputed)；incoming_conflict的cause_source指本root的ConflictSource companion（以conflict_id及其完整C/incoming/meter-choice复合键定位）。applied恰有对应reset mutation，suppressed必须没有；不能把普通normal证据或其他root的dispute当reset资格。

### 5.4 CostOutboxLink 与 audit

canonical cost_outbox child 为 `S + I` 加 `cause_kind,cause_source_id,outbox_id`，cause_kind仅normal_cost_decision/approval_consumed/unmanaged_correction，逐类型双向绑定本root对应source及预分配outbox ID。原outbox的event_id/tenant/operation_id/kind/schema_version/sanitized_payload/created_at逐值一致，原row反向必须唯一link。normal write的operation_id=attempt、kind=cost_recorded，payload精确为三字符串attempt_id/amount/basis；每次write必须有本次原outbox，skip没有。

原 `(operation_id,kind)` UNIQUE 保留。尤其 unmanaged 已记录→conflict reset→再 normal write 若撞既有 cost_recorded，沿原 plain INSERT 的执行错误整体回滚，不能新增第二份、静默忽略或改成更正。多次合法 correction 各用独立 operation，仍可完整重放。没有因新链“修复”原接受集。

需要 audit 的 proposal/approval/unmanaged/Reconcile source 显式保存其原 audit ID；绑定 actor/tenant_scope/action/target/request_id/sanitized_diff。成本历史与 source/outbox/audit 不服从 trace 采样。其他业务可保留原 outbox/audit 写面，但直接 APP 写成本 kind/action 必须有精确 guard 拒绝，不得借共享 INSERT 伪造本域来源。

### 5.5 共享 audit/outbox 的固定来源槽

原audit/outbox的新增pointer统一沿[共同合同](b-procurement-source-contract.md)：`pp_root_id TEXT,pp_root_step BIGINT,pp_source_kind TEXT,pp_source_id TEXT,pp_source_role TEXT,pp_created_xid XID8`，全NULL或全非NULL；原pp_root_step显式映射新step_no。kind是**canonical kind**，不是其cause；完整组精确FK到本step/source公共键，并由source/outcome的槽ID反向约束，受绑定子集有`UNIQUE(pp_source_kind,pp_source_id,pp_source_role)`。

| 原row | source_kind / source_role | 必须反向匹配的槽 |
| --- | --- | --- |
| procurement.cost_proposed audit | proposal_birth / proposal | ProposalBirth.audit_id |
| procurement.cost_approved audit | approval_consumed / approval | ApprovalConsumed.audit_id |
| cost.corrected audit | unmanaged_correction / unmanaged_correction | UnmanagedCorrectionSource.audit_id |
| reconciliation.proposed/approved/released audit | reconciliation / reconciliation | ReconcileSource.audit_id |
| 本次reconcile的billing.settled/released audit | reconciliation / settlement | CustomerFinishOutcome.billing_audit_id |
| 本次reconcile的billing.profile_blocked audit | reconciliation / profile_block | CustomerFinishOutcome.profile_block_audit_id |
| cost_recorded/cost_corrected outbox | cost_outbox / cost | CostOutboxLink.outbox_id；不能绑定其cause step替代 |
| 本次reconcile的settled/released billing outbox | reconciliation / settlement | CustomerFinishOutcome.billing_outbox_id |

带pointer只能真实writer固定函数写；受保护成本/采购audit族不能用全NULL逃逸。共享其他业务的原APP审计/消息写面保留；账户命令族按真实command.kind区分account_create/configure与binding/observation，不按action字符串一刀切。原独立客户Settle的settlement audit/outbox仍由其客户资金合同负责，不能因同kind全部禁止；但本次reconcile输出由source/outcome反向关系强制pointer完整。各槽ID唯一，缺/多/错槽、跨root、同金额换source或错created_xid一律拒绝。

### 5.6 原件与来源的双向集合

以下均进入新完整readset，从原件遍历也必须发现漏证；仅source→原件存在不够。原表新tracking列/nullable discriminator的完整DDL仍待材料交付，不能据本页声称现数据库已有这些pointer。新tracked行完整绑定、legacy行保持NULL，不能将旧行当前值登记成origin。

| 原件/投影 | 对应来源与强制关系 |
| --- | --- |
| request_records | 原request自然键↔RequestOrigin；初始latest NULL；当前latest↔连续RequestDecision末项，非finalization状态写由明确其他原根负责 |
| upstream_attempts | 自然键↔AttemptOrigin；quote/身份/初态冻结；实际首次dispatch pointer↔attempt_delivery(mode=dispatch_permit)，真实成本UPDATE↔CostMutation，当前cost等全部mutation重放 |
| metering_events | 原ID↔唯一MeterOrigin，origin所属canonical MeterChoice必须created；所有reuse指同旧origin，不重生 |
| metering_event_conflicts | 原ID↔唯一ConflictSource companion，event/Usage/incoming/meter选择逐值精确 |
| finalization_records | 原ID↔唯一RequestDecision(normal)，input_digest/归属/原meter/归一化事实逐值精确；conflict分支禁止该原件 |
| procurement_evidence_identities | 原attempt+digest↔唯一EvidenceMember；其出生EvidenceChoice必须new_member；existing choice只消费该旧成员 |
| procurement_cost_proposals | 原ID↔唯一ProposalBirth；proposed→approved时恰一个ApprovalConsumed反向绑定，原payload/出生屏障永久不可改 |
| provider_cost_corrections | 原ID/operation↔精确ApprovalConsumed或UnmanagedCorrectionSource封闭联合，金额/basis/evidence/归属及本root输出身份一一对应 |
| procurement_reservations / budget_events | origin/last、每个PE↔唯一共同ProcurementTransition/account journal/PB，完整账户合同不可省 |
| reconciliation_cases | 本次人工action↔ReconcileSource；原自动markPending/finish仍有其独立来源，不将最后人工source冒称全部case历史 |
| 本次reconcile的settlement/journal/lines/PB | 本次CustomerFinishOutcome及其精确ledger/PB companion集合逐条双向闭合；读取已有settlement不是本次新出生，不改旧pointer |
| audit_events / projection_outbox | 按§5.5真实canonical槽双向闭合，不因共享表允许APP伪造本域来源 |

mutable的request/cost/p/head/current末项只与完整重放的当前结果相等；历史before/after不能永久FK到未来可变当前值。原settlement.refunded_amount等合法后续投影也不被旧outcome永久固定；读取已有结算时保存当次结果事实，退款继续其原独立完整资金来源。

## 6. managed evidence 与采购 transition

### 6.1 EvidenceChoice / EvidenceMember

canonical evidence_choice child 为 `S + I` 加：

```text
incoming_source_id,meter_choice_id,evidence_digest,
procurement_before_revision,procurement_before_state,procurement_before_held,
procurement_before_spent,procurement_before_anomaly,
account_id,account_ordinal_cut,account_tail_cut,
disposition(new_member|existing_member),identity_attempt_id,identity_digest,
identity_event_id,member_count_before,member_tail_before?,
member_count_after,member_tail_after?,forced_conflict,
effective_conflict?,conflict_cause?,decision,existing_dispute_event_id?,
procurement_transition_id?,procurement_event_id?,account_journal_id?,skip_reason?
```

仅 managed；semantic digest 按本次 Usage+原 reason 计算。锁后先查完整 p/identity/member/head/account cut，计算唯一 decision 与真实 before/member，预分配新 identity/member/transition/PE/journal 等输出ID，先一次 INSERT 完整 EvidenceChoice，再消费并核实际 INSERT/RETURNING；不能在 identity/effect 完成后补 before。原 identity 真 INSERT 才建 companion：

```text
EvidenceMember(id,attempt_id,member_ordinal,previous_member_id?,identity_digest,
               identity_event_id,evidence_choice_id,C)
```

物理表为 `pp_evidence_members`，step_kind=evidence_choice/source_id=EvidenceChoice.id。原 `(attempt_id,evidence_digest)` identity 与唯一 member 双向闭合；existing_member 指向最初 identity_event_id，不能替换为本次 meter。new_member count+1、tail=新 member；existing_member count/tail 不变。普通终态、already disputed 的新语义仍可增加 member，却没有 PE/p revision；重复 conflict 可产生新原 conflict 而不增加 member。成员 cut 必须可完整枚举，不以 count/hash 自证集合。

forced_conflict严格等本次MeterChoice.collision_kind≠none，不接受caller的强制争议布尔。existing_member在原逻辑于进一步判断前快返，故effective_conflict/conflict_cause均NULL，不伪称进行了后续判定；new_member完整计算，cause仅none/incoming_meter_conflict/released_positive_usage/settled_positive_no_consumption，按该顺序选择首个成立原因，effective_conflict恰等cause≠none。existing_dispute_event_id仅skip_duplicate_dispute_event有，精确引用旧`conflict:<attempt>:<meter>`原PE；不能仅存seen=true。

### 6.2 采购决策顺序与 effect source

本页所需 canonical `ProcurementTransition` 字段责任（与账户片共用同一行，不能另造一套）为：

```text
S,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,
cause_kind(attempt_birth|evidence_choice|approval_consumed|recovery),cause_source_id,
before_revision?,before_state?,before_held?,before_spent?,before_anomaly?,
after_revision,after_state,after_held,after_spent,after_anomaly,
event_id,event_key,event_type,evidence_ref,held_delta,spent_delta,
journal_id,input_bound,output_bound,upper_amount,
cost_version_id,cost_input_rate,cost_output_rate,cost_basis
```

reserved 的 before 五字段全 NULL（真实 p 初生前不存在），其他 transition.before 完整。字段按[账户合同](b-procurement-account-contract.md)同一 pp_procurement_transitions 展开，不在本片另加另一套表或不同的journal身份。原 PE 与唯一 account journal 是 companion，不第二 child；supplier PB 无配置时的精确 effect/absence 按原 PB 合同，不凭空造 PB event。有 PE 必须唯一 transition+journal，包括零 delta correction；无 PE 且账户配置/桶/blocking 不变时只保存真实 account cut，不造虚假 no_effect journal。p 的 last pointer只约束当前 journal.after；历史不得永久 FK 到可变当前 revision。

`pp_account_effect(root,attempt,source_kind,source_id)` 只消费 DB 已出生、同 root 精确 source。cause 联合只有：attempt_birth→AttemptOrigin（仅 reserved）、evidence_choice→EvidenceChoice（normal/conflict 效果）、approval_consumed→ApprovalConsumed（仅 cost_corrected）、recovery→RecoverySource（仅 expired_without_permit/recovered_unknown）。每类都必须精确表/FK核验，不能用normal/dispute等自由字符串猜源。不能收金额/before/after/count/passed；不额外授 caller reserve helper。source helper 与 transition 的将来 FK 按 §2.2 闭合。

原 finalizeProcurement 固定顺序：

1. 无 managed p 不调用本域 evidence；managed 先尝试 identity。existing_member 立即无 effect，不能因此改变账户/p。
2. new_member：forced conflict，或 released+confirmed正消费且非无消费 reason，或 settled+spent>0且无消费 reason，得 effective conflict。
3. conflict：already disputed 则无 effect；相同 `conflict:<attempt>:<meter>` PE 已存在也无 effect。否则 H=max(U-oldS,0)、S=oldS、state=disputed、anomaly=true；当场 reset cost unknown/NULL并 pause。
4. 非 conflict 且 state settled/released/disputed：无 effect，不以普通证据裁定争议。
5. 无消费 reason：H=0、S=oldS、released、anomaly=false，`release:<attempt>` / released_no_consumption。
6. 非 confirmed_final：已 unknown 无 effect，否则保留桶、state=unknown、anomaly=false，`unknown:<attempt>` / consumption_unknown。
7. confirmed_final：冻结两维费率算 C；H=0、S=C、settled；anomaly=(C>U 或 input>input_bound 或 output>output_bound)，`settle:<attempt>` / cost_confirmed。金额为0但 token 超界仍 anomaly。

EvidenceChoice.decision 封闭为 skip_existing_identity/skip_terminal_normal/skip_already_disputed/skip_duplicate_dispute_event/skip_already_unknown/dispute/release_no_consumption/mark_unknown/confirm_cost；对应 skip_reason 为 existing_identity/terminal_normal/already_disputed/duplicate_dispute_event/already_unknown，effect分支为 NULL。无effect的 transition/event/journal全NULL，有effect三者全有；重复dispute event_key的原PE也须完整读取验证，不接caller seen。不得靠账户已 paused 或缩 limit 拒绝有利收尾；保守成本/未知消费不重放。EvidenceChoice 先出生不移动原 CostReset/Mutation 的实际执行顺序，root seal/deferred核完整结果。

revision 按真实 p 的 `(state,held,spent,anomaly)` 变化才+1；basis-only、同额同状态 correction 可不变。anomaly 自动 active→paused 的 revision 与 cause 归入同一 account journal，已 paused 不加；消除 blocking 不自动 resume。

## 7. managed proposal 与 approval

### 7.1 ProposalBirth

canonical proposal_birth child 为 `S + I` 加：

```text
proposal_id,requester,requester_role,requester_status,
reservation_revision,reservation_state,reservation_held,reservation_spent,
reservation_anomaly,member_count,member_tail?,account_id,account_ordinal_cut,account_tail_cut,audit_id
```

原 proposal 的 id/tenant/attempt/amount/basis/evidence_ref/reason/requester/expected_revision/expected_evidence_count/created_at 全保留、与 source 双向一一绑定。先 operation advisory lock，再当前 finance_operator/active，后同输入已有 proposal 快返；否则 request→attempt→account→supplier PB→p→head 的锁后保存真实屏障。prepared/sending 不可提案；降成本要求 invoice。

金额/basis/evidence/reason 从原提案输入，expected revision/count 只能 DB 生成且等于本 cut。重试逐值比较 tenant/attempt/amount/basis/evidence/reason/requester；同 payload 不新建 root/source/audit，失权先拒绝。原 audit action=procurement.cost_proposed、target=proposal、request=真实 request，payload attempt_id/amount/basis/evidence_ref 精确匹配。

### 7.2 ApprovalConsumed

canonical approval_consumed child 为 `S + I` 加：

```text
proposal_id,proposal_birth_id,approval_operation_id,
requester,requester_role,requester_status,approver,approver_role,approver_status,
consumed_revision,consumed_state,consumed_held,consumed_spent,consumed_anomaly,
consumed_member_count,consumed_member_tail?,before_cost_status,before_cost_amount?,
account_id,account_ordinal_cut,account_tail_cut,procurement_transition_id,procurement_event_id,
provider_correction_id,cost_mutation_id,outbox_id,audit_id,
after_revision,after_state,after_held,after_spent,after_anomaly
```

operation advisory lock→当前 finance_approver/active→原 proposal 读取→不同 maker/checker→maker 当前 finance_operator/active→request/attempt/account/PB/p→proposal FOR UPDATE→head。锁后核 approved 快返身份，否则核非 inflight、原 expected revision/count 与当前一致，并核**完整 member prefix 等于 ProposalBirth 的精确 prefix**。同 count 不同成员不得批准，旧已 approved proposal 不要求等于今天 p/member。

先预分配全部输出 ID，INSERT 完整 ApprovalConsumed，再消费；它不是 proposal AFTER approved 的终态副本。目标 H=0、S=proposal.amount、settled、anomaly=(amount>U)；降成本仍要求 invoice。原 PE key=`correction:<approval-op>`、type=cost_corrected；原 correction operation=`procurement:<approval-op>`；原 outbox operation 相同、kind=cost_corrected，payload三字符串 attempt_id/amount/basis。所有 tenant/attempt/amount/basis/evidence 逐值匹配，不允许另一 proposal/操作借用更正行。

cost mutation after=proposal basis/amount；原 proposal 只允许一次 proposed→approved，原屏障/输入不改；approval operation 与 proposal ID 不同。audit=procurement.cost_approved，actor=approver、target=proposal、request=真实 request，payload attempt_id/amount/evidence_ref。所有原输出均反向精确指 consumed source；本次多个 ID、root/step/xid 不许互换。

approved 相同 approval-op+actor 快返前仍重核双方当前权限，不新建 root/effect。不同操作/actor 拒绝。两个不同合法 proposal/approval 即使金额/basis相同，仍各有新 consumed decision、PE(0,0)、correction、cost mutation、audit/outbox/account journal；不能按 revision 未变合并。

## 8. unmanaged 更正与采购恢复

### 8.1 UnmanagedCorrectionSource

canonical unmanaged_correction child 为 `S + I` 加：

```text
operation_id,provider_correction_id,input_actor_id?,effective_audit_actor,
amount,basis,evidence_ref,before_cost_status,before_cost_amount?,
after_cost_status,after_cost_amount,cost_mutation_id,audit_id,outbox_id
```

operation 是原 1..200 字节字符串，不强制 UUID；Amount int64非负、basis contract/invoice、evidence 受原 ValidReference(256) 限制。锁 request/customer reservation→attempt，要求明确 unmanaged origin 且无 p；managed 返回 procurement_approval_required。不能从当前 credential binding 缺行回推历史 unmanaged，不新增原不存在的双审要求。

幂等 identity **不含 ActorID**：原 operation 对 tenant/attempt/amount/basis/evidence 相同即快返，无新 mutation/audit/outbox；换 actor 不造新审计。input actor 空时 effective actor=system，其他按原输入；DB 不把共享 APP 参数当外部会话签名。新 correction 的 audit=cost.corrected、target=correction ID、request=真实 request、payload amount/basis；outbox operation=原 operation、kind=cost_corrected、三字符串 payload。任何次数更正按实际 AttemptNode 顺序覆盖成本，不关联账户/member/采购 PE。

### 8.2 RecoverySource

公开根固定 `pp_recover_procurement_attempt(tenant text,attempt text)`，request 从原 attempt 派生，不收 request/expired/permit/amount 参数。canonical recovery child 为 `S + I` 加：

```text
checked_at,request_deadline,procurement_before_revision,procurement_before_state,
procurement_before_held,procurement_before_spent,procurement_before_anomaly,
delivery_before_state,permit_source_id?,decision(expired_without_permit|recovered_unknown),
procurement_transition_id,delivery_mutation_id,procurement_event_id,account_journal_id
```

候选扫描只选路线，不是过期证明。根持 request→attempt→account/PB/p 后新 statement 重核 DB deadline、真实 p/delivery 与实际 dispatch source。未过期或 p 已不 prepared/sending 则 noop，无新 root/source/PE。无 managed p 亦不凭空创建采购来源。

expired 且 p prepared、upstream prepared、无已提交真实 sending permit：delivery→failed、H=0/S=oldS、released/anomaly=false，PE `release:<attempt>` / expired_without_permit / evidence_ref空。已有 permit/sending 或其他 eligible 组合：delivery→unknown，保留 H/S、state=unknown、anomaly=false，PE `unknown:<attempt>` / recovered_unknown。重复根不重复 PE；不需要 meter/finalization。按 §2.2 完整 source 先出生、真实输出匹配、最后 seal，永远不持有 provider client 或重发生成。

dispatch公开根为`pp_permit_dispatch(tenant text,attempt text)`，返回固定typed行`outcome(applied),checked_at TIMESTAMPTZ,root_id,delivery_source_id,account_journal_id?`；实际两参身份、账户gate、原PB三scope真实UPDATE许可、post-wait quote/deadline、journal/pointer与账户合同一致。Go PrepareDispatch仍error-only，DB返回时刻/ID描述本次事实，不能当外部capability；无dispatch replay成功分支。本页recovery.permit_source_id只可精确引用`pp_attempt_delivery_sources(mode=dispatch_permit)`的真实immutable source，不拿p sending或caller boolean作许可。

## 9. Reconcile：无 attempt 子流程与客户资金

Go公开ReconcileInput仍含tenant/request/actor/action/evidence_ref/reason/Usage；candidate `pp_reconcile` 仅传本action实际消费的原字段。action仅propose/approve/release：propose的六列Usage/evidence/reason全部有，严格confirmed_final合法Usage、ValidReference(256)、reason 1..200字节；release仅reason非NULL，Usage/evidence参数全NULL；approve全部Usage/evidence/reason参数为NULL。后两类原本忽略的raw字段不保存、不用于identity、不先尝试转PG TEXT，避免忽略字段的NUL/非法UTF8或空status变成新的拒绝。真正消费的输入不被规范化或丢弃，原case Usage及派生IncomingSource仍严格校验。

当前角色先核：propose为finance_operator，approve/release为finance_approver；按原锁request/customer reservation→reconciliation_case。仅approve按原路径重核maker当前operator与不同人，release不凭空新增双审。release原reason检查发生于锁后原分支，不能为了typed输入而改变角色/不存在case与invalid_reason的原错误优先级。

canonical `ReconcileSource`（kind=reconciliation，表 `pp_reconciliation_sources`）为 `S` 加以下明确字段；各 action 采用封闭 NULL 联合：

```text
tenant_id,request_id,case_id,action,actor,actor_role,actor_status,
input_usage_status?,input_input_tokens?,input_output_tokens?,input_total_tokens?,input_cached_tokens?,input_reasoning_tokens?,
input_evidence_ref?,input_reason?,
before_case_reason,case_created_at,before_case_state,after_case_state,before_reservation_status,
before_requester?,before_approver?,requester_role?,requester_status?,after_requester?,after_approver?,
case_usage_status?,case_input_tokens?,case_output_tokens?,case_total_tokens?,
case_cached_tokens?,case_reasoning_tokens?,case_evidence_ref?,case_proposal_reason?,
approval_checked_at?,reservation_deadline,
prior_latest_id?,derived_incoming_source_id?,finalization_id?,
customer_finish_outcome_id?,audit_id
```

input_*仅按上述action联合保存真正消费输入；case_*是独立的真实锁后原case读取，不能用其覆盖原输入。case无proposal时整组case_usage/evidence/maker可NULL；有proposal时完整且approved消费时再次确认合法。case受保护转换反向绑定此source；case初始open的markPending来源仍属原客户流程完整闭包，不以当前proposed行冒充出生。批准source保存proposal当时输入和当前双方角色，不能只存“approved”布尔。

source在审批/制单层真实锁后capture该层before、准确prior latest/approval_checked_at，预分配本root输出ID后完整INSERT，再消费case/finalization/finish/audit输出，最后seal。它只预分配customer_finish_outcome_id，不在此阶段预测资金锁等待后的收费或reservation终态；§9.1的finish companion在原资金层自己的实际变动前再capture/计算，不改变锁序和原业务时点。两阶段均无AFTER补before。

| 分支 | 原语义与 source/输出 |
| --- | --- |
| propose | approved/closed 拒绝；hold 必须 pending。已 proposed 同 requester/evidence/reason/Usage 快返，否则冲突。新提案 open→proposed，原输入不可在 pending 中替换，生成 reconciliation.proposed audit；无 finalization/attempt/成本/member/account效果 |
| approve 重试 | 先当前 checker、maker 当前 operator 与不同人；approved/closed 返回原 settlement，若仍 pending 且无 settlement，返回原 approved_evidence_pending_delivery 占位结果；无新 root/source |
| approve 新 | 必须 proposed 且证据完整可信。保存当时latest和approval_checked_at；case→approved。approval_checked_at < reservation deadline 时，DB预分配新的finalization ID，派生Source=approved_reconciliation、SourceEventID=case、AttemptID=NULL、Usage=原提案；outcome/reason/stage/content/finish全取当时latest，无latest时均为原unknown/false/空默认，绝不凭批准制造delivery。派生IncomingSource精确反向绑定本ReconcileSource，复用同writer私有finalize |
| approve 已过 deadline | 不新增 finalization；仍按原 settleRequest(recovery) 和最后资金时钟处理，不虚构 usage/delivery |
| release | reason必填；原hold已released返回原settlement。仅pending可释放，调用原finish，audit=reconciliation.released；公开result.State=closed，但原case为approved时finish保留approved，其余case才closed，source.after_case_state不能混成返回state。不能释放已settled收费，后者走退款 |

批准中的 private finalize 走同一 incoming/meter/request sticky 规则；如出现 metering_conflict，**整个 Reconcile 回滚**，包括 case approved、全部新来源与原件。公开 RecordFinalization 的可提交 conflict 协议不能直接套到此处；没有第二个嵌套 root，所有子步骤属本次 reconcile root。

### 9.1 pp_customer_finish_outcomes：原资金层结果companion

`CustomerFinishOutcome` 为本 reconciliation step 的唯一结果companion，**不是新增canonical child/step**。物理表`pp_customer_finish_outcomes`，完整列为：

```text
id,C,tenant_id,request_id,reconciliation_source_id,case_id,latest_finalization_id?,
customer_reservation_id,reservation_amount,reservation_deadline,
result_kind(new_settlement|existing_settlement|pending),selected_mode,selected_reason,
settlement_decision_checked_at?,financial_checked_at?,
before_case_state,after_case_state,before_case_reason,after_case_reason,
before_reservation_status,after_reservation_status,
before_request_status,after_request_status,before_billing_status,after_billing_status,
before_key_held,before_key_spent,after_key_held,after_key_spent,
raw_amount,charged_amount,waived_amount,result_status,
settlement_id?,customer_journal_id?,customer_tenant_control_id?,customer_key_control_id?,
billing_audit_id?,billing_outbox_id?,profile_block_model_id?,profile_block_profile_version?,
profile_block_disposition?,profile_block_audit_id?
```

C的step_kind=reconciliation/source_id=ReconcileSource.id。PK(id)，UNIQUE(reconciliation_source_id)、UNIQUE(root_id,step_no)；source.customer_finish_outcome_id与本companion在其自身ID及完整C/tenant/request上双向deferred绑定。propose禁止companion；新approve/release各按实际finish分支恰一个，replay不生成新root/source/companion。

new_settlement分支在原model/customer-PB/ledger锁全部持有、最终financial_checked_at读取后，实际金额UPDATE前一次完整INSERT本companion的before+唯一target及预分配settlement/journal/audit/outbox ID，随后真实RETURNING核目标。原settlement以tenant/request/id/finalization/journal/status/reason/charged/raw/waived复合关系反向绑定；原billing audit/outbox精确匹配。这里必须再读最终clock，不能用approval_checked_at延长资金期限。settlement_decision_checked_at是原settleRequest在已有settlement检查之后、delivery/usage/期限选择之前读取的较早时刻；release直接finish时该列NULL，已有settlement快返两种checked_at均NULL，pending只有decision时刻而没有资金变动时刻。

existing_settlement不新增资金/journal/PB/billing audit/outbox。pending同样不收费、不生成settlement，但必须保留原pendingResult→markPending的实际写：在本companion完整capture与target INSERT后，将非终态reservation.status及request.billing_status按实际before置pending_reconciliation，并对原case执行ON CONFLICT的reason选择——已有metering_conflict始终保留，否则after_case_reason=本次selected_reason；已approved的case也可能改reason，state不因此撤回。request.status、Key及ledger金额未变，不能将其概括为“实体全部不变”。pending结果金额仍为原占位0且没有settlement ID，返回reason与实际保留下来的case reason分别核，不当作收费事实。

本次真正受影响的ledger账户用`pp_customer_finish_ledger_effects`精确companion行组保存 `finish_outcome_id,C,tenant_id,account_id,account_tenant_id?,account_kind,before_balance,after_balance,signed_delta,journal_line_id`，PK(finish_outcome_id,account_id)，逐行双向绑定原journal line与真实account。AVAILABLE/HELD的account_tenant_id=本tenant，平台REVENUE为NULL，不能强作客户账户。new_settlement的实际line集合为HELD/AVAILABLE及最终charge>0时REVENUE，无漏或多行；此前因拟收费而锁住REVENUE、最终deadline使charge=0时，没有该账户effect行。每行after=before+delta、delta和为0、客户两账户非负、Key held/spent与reservation/customerPB一同守恒。existing/pending不得伪造本组。原funding-lot等完整资金来源继续由原独立checker核，本组不认证其他历史。

profile-block列仅实际raw>R的原分支非NULL，按原billing_profile_blocks的(model_id,profile_version)复合自然键，不编造原表不存在的ID；disposition仅created/reused，旧row被复用不强绑其request为当前request。原ON CONFLICT不新增block仍有本次profile-block audit，逐值保存真实动作，不改原输出。

新finish对应的tenant/key PB效果用`pp_customer_finish_pb_effects`行组保存 `finish_outcome_id,C,scope,control_id,allocation_id,event_id,original_source_kind(settlement),original_source_id(settlement_id)`，PK(finish_outcome_id,scope)，逐条精确双向绑定原periodic event。不能用一个多义PB事件ID代表两个scope。control未配置才允许相应scope无event，须按原PB锁/来源证明；配置的各scope不许漏effect。原current/window/source守恒仍由PB完整链/deferred核验，不用此集合替代。

保留原 settleRequest(recovery) 的证据/delivery/时间/价格规则，实际 charge≤R、raw独立大整数、waived=raw-charge。finish 取得原 model/customer-PB/ledger locks 后**再次读取 DB时钟**，到硬 deadline 释放而不收费；第一次审批 checked_at 不能延长财务期限。原 postLocked/唯一 settlement、recovery job清理、profile block 与资金 audit/outbox 不省略。客户 finish 的完整 SQL、原资金来源及准确列 ACL 仍为联合实施义务，本页不以“调用旧Go即可”宣称候选闭合；此结果companion仅证明本次reconcile实际消费/输出，不扩大为客户全部资金历史认证，后者仍由独立完整source checker负责。

## 10. 入口模板、锁后 cut 与双向核验

下列是必有/禁有与相对顺序；具体 step_no 由根在实际执行中分配。每个出现项恰一个 canonical child，相同类型因原流程真实发生多次时必须有不同 ID/step/node。source 插入时间与其将来效果 FK 的完成时间可不同，但 before 一定先于被消费效果，CostMutation/PE/account journal 各按自己真实顺序记录。

| 新操作 | 模板要点 |
| --- | --- |
| request_birth | 自动 RequestOrigin+head；原 reserve 资金/PB另闭合，无 incoming/meter |
| attempt_birth | 明确 Origin；managed采购reserve transition/PE/journal/PB或明确unmanaged出生；禁止最终态成本/dispatch |
| dispatch | 唯一attempt_delivery(mode=dispatch_permit)与真实upstream pointer；managed account journal；无伪造PE或finalization |
| finalization normal | incoming→meter choice→request normal decision/原finalization→delivery→normal cost decision→可选cost mutation/outbox→managed evidence choice→可选采购transition/采购reset；禁止conflict source |
| finalization conflict | incoming→meter choice及其conflict companion→request conflict decision→managed evidence choice及可选采购transition/reset→外层cost reset→markPending；无normal finalization/NormalCostDecision |
| proposal | ProposalBirth+原 proposal+audit；无cost mutation/PE |
| approval | 完整 ApprovalConsumed先出生→采购transition/PE/journal/PB→cost mutation→原批准/audit/outbox；同额仍全有 |
| unmanaged correction | source→原 correction→cost mutation/audit/outbox；禁p/PE/member/account来源 |
| recovery | 完整RecoverySource先出生→delivery/采购transition/PE/journal/PB；禁meter/finalization/provider |
| reconcile | 一个reconciliation child；approve按deadline可含无attempt normal子模板，再客户finish结果companion；propose/release按§9联合；本root全部step无attempt node，无虚构attempt成本 |

before读取总表：request latest 在原同步 reservation 锁后、任何 sticky/meter 前；正常成本在本次cost/evidence/采购effect前；member cut在identity INSERT前；proposal/approval在完整 p/head锁后、任何更正前；unmanaged成本在attempt串行锁后；recovery在所有等待结束后的deadline/permit新statement。禁止用旧 CTE/STABLE snapshot、AFTER终态或提交时补读 before。

同事务可以多个合法 root，甚至同request两次conflict/同attempt多更正；root/step/node/journal真实链区分，不假设一事务一操作。root seal检查准确模板、所有双向关系和末项投影；COMMIT deferred再核无孤立source/原件/step/noop伪输出。任何statement/savepoint/事务回滚无已提交ordinal缺口，no-root残留也不允许。

## 11. 返回、错误、重试与未知提交

公开根必须返回封闭typed record，统一顶层`outcome=applied|replay|noop|semantic_conflict`，仅各根实际有的分支可用；不能用任意error text作为“可提交”。本页明确业务枚举及字段；SQLSTATE到原billing code的精确表见[执行合同](b-procurement-execution-contract.md)；原PB固定code/Message的窄兼容例外沿共同协议，不能解析未知或本地化文本。`root_id`在新动作指本次sealed root，replay可指精确已受证旧root而不产生新root；无法从原回执唯一定位旧root时为NULL，不伪造历史。`source_id`按入口明确指proposal birth/approval consumption/correction/recovery/reconciliation，不是任意typed source。

| 根 | 成功返回联合 |
| --- | --- |
| finalization | outcome=applied/replay/semantic_conflict；`root_id,incoming_source_id,request_decision_id`定位新动作或精确旧normal回执。applied/replay返回原Finalization全部字段；semantic_conflict该组全NULL，conflict_id非NULL、semantic_code固定metering_conflict；其他分支conflict/semantic均NULL |
| proposal/approval | outcome=applied/replay；`root_id,source_id`指精确新或原来源，不新增快返历史。原ProcurementCostProposal全字段不删减，含expected revision/count、created/approved时间、approver/approval operation |
| unmanaged correction | outcome=applied/replay；`provider_correction_id,root_id,source_id`定位原件及精确来源；公开Go仍error-only，不额外输出权限token |
| recovery | outcome=applied/noop；`root_id,source_id,procurement_event_id,account_journal_id,delivery_source_id`在applied全有，noop全NULL；原批处理计数适配另核，不把返回计数当覆盖证明 |
| reconcile | outcome=applied/replay；`root_id,source_id`定位精确动作（快返不造source）；原Reconciliation id/state/settlement含明确pending占位完整；返回state与实际case state分别按§9核 |
| dispatch | outcome仅applied；`checked_at,root_id,delivery_source_id,account_journal_id?`；managed journal全有、unmanaged NULL，没有replay成功分支；时刻和ID不是可重用capability |

normal 同 ID+digest 在原输入校验、request锁后直接返回旧 finalization，不新建任何 source/root；ObservedAt变化不打破快返。**同ID不同digest不立刻统一拒绝**：先走原meter判定，实际conflict仍提交新conflict；只有到normal INSERT冲突才idempotency_conflict全回滚。重复conflict仍每次生成新原conflict；identity/member可去重，不能用normal幂等吞掉。

原Finalization字段精确为`id,event_id,request_id,usage_status,input_tokens,output_tokens,total_tokens,cached_tokens,reasoning_tokens,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason`；JSON形状沿原Go结构，SQL结果可平铺，adapter必须逐值重建，不能用开放JSON替代typed返回合同。原Proposal结果为`id,tenant_id,attempt_id,amount,basis,evidence_ref,reason,requester,state,approver?,approval_operation_id?,created_at,approved_at?,expected_revision,expected_evidence_count`；原Settlement包含`id,request_id,tenant_id,charged_amount,raw_retail_amount,waived_retail_amount,refunded_amount,status,reason,journal_id?`，pending占位空ID不等同存在settlement。

公开finalization先成功COMMIT，Go才将semantic_conflict译为原billing metering_conflict；DB约束、权限、上下文、业务拒绝全部回滚，不能伪装成此结果。Reconcile内部semantic conflict整个回滚。proposal/approval当前权限快返顺序见§7，unmanaged Actor不进入identity见§8。

只对明确40P01/40001、确定回滚按原最多三次重试，deadline/context/语义拒绝不重试。COMMIT回复未知禁止重做批准/effect或生成；已知root/operation按精确身份只读查。未拿到可唯一定位本次conflict的root ID，返回outcome_unknown并保守核对；同input ID发现一条旧conflict不证明本次成功。供应商消费未知与DB提交未知分开，不自动重发模型。

## 12. 独立业务 oracle 与攻击验收

纯 checker 读取完整物理原件、typed来源、root/step/所有局部链、账户/成员/PB前缀及当前投影；不得调用writer mutation/helper产生expected，不用生成expected的SQL函数与被测函数互证。金额使用独立大整数状态机，严格核union/FK/连续前驱/无未来边/原件反向集合，不能只核hash/count或单条正例。

| 场景 | 必须独立算得的结果与拒绝边界 |
| --- | --- |
| managed正常24→invoice25 | 先按冻结rate/usage得到24及真实unknown/non-disputed资格、cost_recorded、采购settled；提案/审批消费准确旧cut后25，PE deltaS=+1、H=0，独立current cost最终25；不得仅看今天invoice25 |
| 同额/basis-only、多更正 | 每个新批准都有PE/correction/audit/outbox/journal/mutation；p字段全相同则revision不增，account ordinal仍增；多次绝对金额不能当delta累加 |
| skip与reset优先级 | known+disputed先skip_already_known；unknown+disputed skip；新late dispute在normal选择之后reset，双reset含unknown→unknown；已adjudicated重复conflict不擦invoice |
| 完整incoming/sticky | raw content/committed→不同attempt/无attempt后续sticky；normal/conflict混排，conflict不推进latest；删任何前驱、换raw/reason/归属均失败 |
| meter冲突 | 跨request/attempt source-key碰撞指真实incoming目标；新meter后因其他confirmed成为conflict仍留完整membership；source_payload_mismatch优先；删竞争成员不能用false/count顶替 |
| 重复conflict与成员 | 原conflict每次新增，重复semantic不增member，新semantic普通终态/already-disputed可增member无PE；旧identity_event不被替换 |
| 提案屏障/失权 | 同count不同member、wrong尾/前缀、revision变化拒绝；approved旧proposal可与今天状态不同；operator/approver停用或失权重试拒绝，自批拒绝 |
| unmanaged | 明确出生→normal/多次correction/conflict reset按真实链重放；不增account/member/PE；managed单人更正拒绝，后来新增binding不重写旧分类；重复cost_recorded身份沿原错误回滚 |
| 恢复/dispatch | 真实permit前expired prepared可释放，permit后只能unknown；锁后重核时间；无meter来源仍闭合；重复noop无PE且无provider动作 |
| Reconcile | 无attempt真实子finalize消费request前驱；before-deadline批准仍不能制造delivery，资金锁后到deadline释放；内部semantic conflict全部回滚，propose/release与replay均完整 |
| source/root攻击 | 预造/漏原件、反向漏源、跨tenant/attempt/root借source、两root同xid混绑、分叉/跳号/缺尾/未来前驱、无seal/假输出/HEAD改写、改before后补生全部失败 |
| 实际APP权限 | raw改source/quote/cost/latest/桶/outbox/audit、调用private、SET/INHERIT/ADMIN writer、默认ACL/临时对象/search_path/trigger禁用均拒绝；owner/SET ROLE不替代真实LOGIN |

除本页，账户片还必须覆盖全部共享来源/配置、A/B各700 L1000释放复用、四source960、多次pause/resume仍有blocking、token-only anomaly与gate post-wait；不能因本页成本矩阵通过宣称账户准入已证明。

### 12.1 十一项命题逐域责任

| 原 Unproven（保持Base/计数） | 本页必须来源 | 共同不可省略来源 |
| --- | --- | --- |
| OriginalDeliveryPredecessorUnproven | raw incoming、RequestOrigin/Decision完整链、准确before_latest | 跨attempt/无attempt原finalization完整图 |
| FinalizationOrderUnproven | normal/conflict连续decision及latest闭合 | 不按时间排序、原件无漏 |
| ConflictIncomingUnproven | Incoming/ConflictSource/MeterChoice/MeterOrigin | source-key真实复用前驱、完整meter集 |
| ConflictReasonUnproven | 原reason/Usage与独立identity重算 | 不从旧meter/hash猜reason |
| IncomingBindingUnproven | incoming tenant/request/attempt、原AttemptOrigin | mismatch归真实目标，不能借旧meter归属 |
| ProcurementEventOrderUnproven | evidence/recovery/approval source与transition顺序 | 每个PE/account journal/PB及出生完整 |
| CostRecordedEligibilityUnproven | NormalCostDecision真实before/write-skip/quote/outbox | 当时p状态，managed/unmanaged精确联合 |
| CurrentCostOrderUnproven | 所有mutation/reset/approval/unmanaged连续顺序 | 当前原成本等完整重放 |
| ProposalBarrierUnproven | ProposalBirth完整成员前缀、ApprovalConsumed实际cut/当前角色 | p/PE/correction/audit/outbox双向精确关系 |
| AccountPauseOrderingUnproven | effect/anomaly cause绑定 | 全账户config/pause/revision/journal前缀 |
| AccountAdmissionOrderUnproven | 真管理分类出生、dispatch/recovery及account cut | 全账户reserve/dispatch gate、全部来源与config/金额/blocking |

仅codec、单个approval或某个account journal不能关闭其他域。unmanaged没有p不等于采购相关历史全N/A；其request/attempt/normal/correction来源仍须完整，新scope只能对真实覆盖域独立判定，不减掉Base gap。

## 13. 联合冻结及实现前未满足项

1. 共同root/step/source合同与本页字段/枚举逐值对齐，确认AttemptNode作为唯一companion、全入口模板、所有双向复合FK和output先源后消费；账户片采用同一ProcurementTransition，不保留第二套表。
2. 原request自动出生、attempt初态/quote/binding屏障与同事务closure的完整DDL/trigger时序；旧历史prospective拒绝、不能补造origin。request/reservation初始资金闭包不能只靠birth替代。
3. 全业务根/私有函数SQL、RC新statement锁矩阵、precise SQLSTATE mapping及Go候选executor；Reconcile原finish资金/PB全接线与无attempt路径；完整freeze不能只有成本片。
4. 精确APP与writer表/列/函数/默认ACL/角色闭包，PB configuration_versions读取、allocation真实INSERT列集、private append EXECUTE、PUBLIC有效EXECUTE及APP COMMIT deferred身份；缺一权限的真实LOGIN回滚实验。
5. 独立typed DTO/纯重放、完整新物理readset、固定catalog/basis/roots/profile与history/current映射、EOF/Close/资源预算，保持旧接受集；selector不认证安装，不按表存在自动启用。
6. 专用PG实际业务/攻击/并发/deferred/回滚验收及逐命题报告；仍未关闭的B-P3、catalog race 5秒门禁与R2独立保留。既有identity叶子通过不转记为本页经营通过。

以上每项未完成均须明确报告，不能将本页“合同冻结”写成“D1已实现/商业验收通过”。统一架构由集成人汇总全部并行结果后同步复核与refresh；本页不直接修改架构清单。

## 14. 对照的原源码

- [types.go](../../internal/billing/types.go)：原Attempt/Dispatch/Usage/Finalization/Reconcile/CostCorrection输入与公开结果。
- [evidence.go](../../internal/billing/evidence.go)：request锁后刷新、attempt创建、真实dispatch、raw/sticky、meter/conflict、normal成本优先级及outbox。
- [procurement.go](../../internal/billing/procurement.go)：p锁、identity去重、采购分支、pause/reset、恢复、提案和批准。
- [recovery.go](../../internal/billing/recovery.go)：Reconcile原语义、无attempt子finalize及unmanaged correction。
- [reserve.go](../../internal/billing/reserve.go)、[settle.go](../../internal/billing/settle.go)、[store.go](../../internal/billing/store.go)：原request出生字段、唯一结算、最终资金时钟、role与retry/audit/emit。
- [002](../../migrations/002_billing.sql)、[006](../../migrations/006_route_decisions.sql)、[014](../../migrations/014_procurement_accounts.sql)、[018](../../migrations/018_tenant_pricing.sql)、[020](../../migrations/020_periodic_budgets.sql)：原表/约束/immutable字段与PB，不修改历史SQL。

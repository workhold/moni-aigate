# B-P4 D1 工作提案：request、attempt、成本与审批来源

2026-11-03。承接[B-P4设计稿](b-procurement-prefix-design.md)和[受控写入合同](b-procurement-writer-authority-contract.md)。**这是D1工作提案，完整逐字段DDL、root联合来源、权限、锁序及新profile尚未冻结；未实施。** [身份codec叶子](b-procurement-identity-contract.md)已单独冻结、实现并获独立PG差分证据，不代表本提案其余行为已验收。

## 1. 根裁决及接线边界

采用**固定闭合 mutation + 同一独立 NOLOGIN 最小 writer**。公开 SQL 根只接原业务输入；根持锁后读取真实 before，执行现有规则并自行生成原业务来源、投影、typed 见证、audit/outbox/PB effect。APP 不得先随意写成本/桶后调用“见证 API”。不存在接受 caller `before/after/count/tail/ordinal/passed` 的入口。

`billing.New(pool)`、原公开方法默认实现和原 profile 接受集不变。候选使用显式、类型化的 executor 构造，例如独立的prospective构造入口；普通显式selector仅选择实现、不授权限，无须额外opaque授权token。执行失败即拒绝，不能回退旧SQL，也不能按表/列存在猜测授权。候选只在隔离、精确安装基线内验收；它不启用 R2 正式迁移、启动、维护 CLI 或在线资金接线。

同一个 writer 服务账户与成本域。账户片约定私有函数：

```text
pp_account_effect(root_id text, attempt_id text, source_kind text, source_id text)
```

仅消费 DB 已创建且验证的 typed normal / dispute / recovery / approval source；不收金额或快照。caller 已持 request/customer-reservation → attempt → account → supplier-PB → procurement-reservation 前置锁，helper 可同序重锁，不能回头锁 request/attempt；account head 在 procurement reservation 之后。正常成本 decision 发生在 account effect 之前。identity duplicate / 终态普通证据没有 PE、配置、桶或阻断变化时不追加 account journal，但保存同一持锁状态下真实 account ordinal/tail **观察 cut**，不将该 cut 称为准入。每个实际 PE（包括同额 correction 的零 delta）都推进唯一 account journal。

## 2. 公开根、私有函数与原输入

本提案中的业务 mutation/helper 为 `VOLATILE PARALLEL UNSAFE`，固定参数类型与 schema；不包含已单独冻结为 IMMUTABLE/PARALLEL SAFE 的六个 identity 叶子。所有下述标为 private 的函数只许 writer EXECUTE，APP、PUBLIC、projector、peer、worker 均无 EXECUTE。回收由现有业务 APP 调度入口执行，不授模型客户端能力。

| 入口 | 原始输入（不能再附自报证明字段） | 责任与输出 |
| --- | --- | --- |
| `pp_record_finalization` | `FinalizationInput` 全字段，Usage 拆 typed 标量；ID 空值仍由 Go 在事务前生成一次 | 原锁、快返、incoming/sticky、meter 分支、finalization、正常成本及采购收尾；返回 §8 的 typed result |
| `pp_propose_procurement_cost` | `ProcurementCostProposalInput`：TenantID,AttemptID,OperationID,ActorID,Amount,Basis,EvidenceRef,Reason | 核当前 operator、幂等、真实屏障，创建原 proposal/audit + proposal-birth decision；返回原 proposal |
| `pp_approve_procurement_cost` | `ProcurementCostApprovalInput`：ProposalID,OperationID,ActorID | tenant/attempt/amount/basis/evidence 只能从锁后原 proposal 取得；核角色/双人/屏障并原子完成 correction；返回原 proposal |
| `pp_record_unmanaged_cost_correction` | `CostCorrectionInput`：TenantID,AttemptID,OperationID,EvidenceRef,Basis,ActorID,Amount | 按原权限语义执行 unmanaged correction；managed 必须拒绝；返回 created/replay 与原 correction 身份 |
| `pp_recover_procurement_attempt` | TenantID,AttemptID（request 从原 attempt 读） | 锁后真实 deadline/state/permit 决策；返回 applied/noop 和本次 recovery source 身份 |
| `pp_reconcile` | 原 `ReconcileInput`，不接受 caller 构造的 approved finalization | 候选 Reconcile 必须接线；保留原 role/case/期限/settle/audit 原子合同，批准路径从原 case 与当时 latest 派生无 attempt finalization，调用同 writer 私有 finalize |

建议 private 拆分（这些不是可给 APP 组合调用的微权限）：

```text
pp_lock_financial_request(tenant, request) -> locked identity
pp_lock_attempt_procurement(attempt) -> locked managed/unmanaged state
pp_capture_finalization_input(root, typed original input) -> incoming_source
pp_finalize_locked(root, incoming_source) -> normal | conflict | replay
pp_choose_normal_cost(root, finalization_source) -> typed cost_decision
pp_choose_procurement_evidence(root, incoming_source, meter_source, forced_conflict)
    -> typed evidence_decision
pp_account_effect(root, attempt, typed_effect_kind, typed_effect_source)
pp_apply_cost_reset(root, typed_reset_source) -> mutation | suppressed decision
pp_apply_unmanaged_correction(root, original_correction_source)
pp_emit_cost_outbox(root, typed cost source) -> original outbox
pp_seal_root(root, expected typed result) -> immutable completed root
```

函数间传递 `root/source` 只是 DB 自己刚生成或按精确 FK 读取的身份，private 函数须再次核来源类型、tenant/request/attempt、root、事务一致；不是接受外部可信 DTO。金额/规则只从 source、冻结 quote 与锁后实体推导。纯核验器另用独立整数运算与状态机重放，不能调用这些 writer 函数产生 expected。

**发现的必需接线**：`recovery.go:231` 在 Reconcile approve 内直接调用 `recordFinalization`，AttemptID 为空。只替换公开 RecordFinalization 会漏掉 request 链或被新 source ACL 拒绝。原 Reconcile 对该子调用 semantic conflict 返回事务错误并整体回滚，不能套用公开 RecordFinalization 的“提交后返回 conflict”协议。source 文本等于 `approved_reconciliation` 不代表批准权；只有实际 typed reconcile 根与原 case/role 来源能证明批准路径。

## 3. 原输入类型与固定序列化

### 3.1 类型

- 所有原 ID 沿既有 `TEXT` 身份保存，不在计算 digest 时转 `UUID` 后规范化。新增 DB-generated root/node/source ID 可用 canonical UUID text；原 `validID` 允许的输入语法须与 Go 现有解析器等价测试，不能偷偷缩成另一接受集。
- 所有 usage token 为 BIGINT / Go int64，非负；cached≤input、reasoning≤output；confirmed_final 以 NUMERIC/独立大整数核 input+output=total，不能 int64 相加溢出。status 枚举为 confirmed_final/provisional/estimated/missing/conflicting。
- outcome、reason、stage 采用 `evidence.go:344` 原枚举；source 字节长1..80、source-event1..200、finish-reason0..80。长度应等价 Go UTF-8 byte length，即 SQL octet_length，不用字符数放宽。
- correction/proposal Amount 原 ABI 是 Money int64、0..MaxInt64；cost/event/p 投影沿原 NUMERIC(100,0)；DTO 用规范十进制 string（无加号、前导零、负零、小数或指数），delta 可为负；ordinal/revision/count SQL BIGINT 非负，DTO 用规范十进制 string。任一序列到 MaxInt64 后明确拒绝，不环回。
- observed_at 是 caller 原业务观察时刻，不是顺序或 before 证明。原零值以 SQL NULL 表示未提供，由根 clock_timestamp 产生 effective_observed_at；二者都保存，实际 meter.happened_at 使用 effective 值。root/source 的 captured_at 仅诊断，纯核验不用时间排序。
- 原空 AttemptID 精确映射 NULL；与任意实际 ID 不等。每个 optional 字段在 DTO 明确 null，不能由空串、零或 omitted 模糊表示。

### 3.2 finalization 与 semantic digest

已有 `FinalizationInput` 无 JSON tags；`json.Marshal` 的字段大小写与声明顺序、Usage 的小写 tags、ObservedAt 固定 Go 零时刻都是现有 input_digest ABI，见 `types.go:123` 与 `evidence.go:378`。禁止换成 `jsonb::text` 或只 hash 归一化后的 stage/content。

独立[身份合同](b-procurement-identity-contract.md)已冻结并实现六函数材料，包括 `pp_finalization_identity_v1(typed original fields)` 与 `pp_evidence_identity_v1(Usage,Reason)`：返回和原 Go `json.Marshal` **逐字节一致**的 UTF-8 bytes，再用 pg_catalog 内置 sha256(bytea)计算，不引入 pgcrypto。前者 ObservedAt 固定 `"0001-01-01T00:00:00Z"`；后者外层字段顺序为 `Usage`、`Reason`，Usage 含 status 与五个 token 计数。完整转义与原 Go 差分已实际验证；NUL/非法 UTF8 属于 PG TEXT 域之外，未来业务根必须显式拒绝，不得规范成另一个可接受身份。

根**不接 caller_digest、identity_json 或“签过的快照”作为权威**。原 Go 可保留输入校验，但 DB 必须重复业务合同校验和计算 digest。叶子的独立 Go/SQL 向量与权限测试已通过，见[验证](../../docs/verification/procurement-identity-leaf.md)；未来 writer 调用闭包、业务输入校验及实际持久化尚未接通，不能以 codec 通过计经营验收。ObservedAt 不进入 digest，所以不同 observation 时刻的同输入仍走原正常幂等快返。

已冻结 identity 合同的编码概要如下（完整 ABI 以该合同为准）：`Q(t)`先后加ASCII双引号；逐Unicode标量处理，双引号与反斜杠各用JSON对应转义；backspace/formfeed/newline/CR/tab各用Go短转义；其余U+0001..001F用小写hex `\u00xx`；`<>&`分别为`\u003c/\u003e/\u0026`；U+2028/2029分别为`\u2028/\u2029`；其余合法UTF-8原字节输出（包括斜线、emoji、U+FFFD），无空格。原SQL TEXT不可表示NUL/非法UTF-8的输入不能成功提交，不以另一隐式替换值发证。codec 数值覆盖完整 int64 规范十进制，业务根另核非负；布尔为ASCII `true/false`。

finalization identity字段顺序精确为`ID,TenantID,RequestID,AttemptID,Source,SourceEventID,Usage,ProtocolOutcome,TransportEndReason,DeliveryStage,FinishReason,ContentWritten,ObservedAt`；AttemptID无值编码空JSON string；Usage字段顺序精确为`status,input_tokens,output_tokens,total_tokens,cached_tokens,reasoning_tokens`；ObservedAt恒为上述零值string。semantic identity外层只含`Usage,Reason`。使用固定字面字段名、`Q`和整数编码直接连接，再`convert_to(...,UTF8)`→`sha256`→小写hex，不让jsonb重排。

`validID`等价边界按已固定google/uuid v1.6.0源码：长度36为标准横杠及32hex；长度32为raw hex；长度45先不区分大小写匹配前9字节`urn:uuid:`再标准36；长度38只检中间36字节，首尾任意单字节，不额外要求花括号；不核version/variant。不能用PostgreSQL UUID cast直接替代该接受集。身份文字始终原样参与digest和SQL原TEXT键。

## 4. 最小物理/纯 DTO 行组

这是尚待汇合的**字段并集与逻辑关系**建议；实现可合并物理表，但不能删除字段责任或改为 caller JSON。新纯 DTO 必须一一表达下述 typed 联合；合法managed/unmanaged分支不以absence自动认证，origin或管理分类缺失仍缺证；新 scanner 的完整物理读集包含全部原 source 与这些行组，不能只读 hash。任一 source 的识别由 `(kind,id)` typed discriminator，不用一个多义 source_id 猜表。

### 4.1 root、步骤和出生

`OperationRoot`：`id,kind,tenant_id,request_id?,attempt_id?,created_xid(xid8),session_role_oid,step_count,semantic_result`。kind 精确为 record_finalization/propose_cost/approve_cost/unmanaged_correction/recover_procurement/reconcile 等拟定入口类别；semantic_result 只能是该入口允许的完成状态。root ID 每次 DB 生成，公开根没有 root_id 参数。root 最后一次 INSERT 为完整不可变行；中间 source 通过 DEFERRABLE FK 引用它，不暴露可修改 pending root。提交时没有未封闭 root。

`RootStep`：`root_id,step_no,kind,typed_source_id`，PK(root_id,step_no)、UNIQUE(kind,typed_source_id)，step 从1连续到 root.step_count；typed source 反向精确引用(root_id,step_no,kind)，每步恰好一个具体子行。step_no 仅由 private 根推进。步骤 union 包含 incoming、meter_choice、request_decision、attempt_delivery、normal_cost_decision、cost_mutation、evidence_choice、procurement_transition/account-effect、proposal_birth、approval_consumed、unmanaged_correction、recovery、cost_outbox。按入口模板校验必有/禁有/相对顺序，不把 step_count 当覆盖证明。

`RequestOrigin`：`request_id,tenant_id,key_id,source_xid,initial_latest(NULL),initial_status(accepted),initial_billing_status(reserved)`；只随实际新 request INSERT 原子建立，不能注册存量行。对应 request 初始 latest 必须 NULL，起点 ordinal0/tailNULL；原身份及 PB birth metadata 不可改。`RequestHead`：`request_id,ordinal,last_decision_id`，只由 writer 推进，和完整链末项闭合。

`AttemptOrigin`：`attempt_id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,cost_version_id?,cost_input_rate?,cost_output_rate?,cost_basis,initial_delivery(prepared),initial_cost_status(unknown),initial_cost_amount(NULL),management_mode,procurement_origin_id?,unmanaged_birth_source_id?,source_xid`。managed 与 unmanaged 是精确互斥联合，不能 null/null。原 quote 冻结字段与实际 attempt逐值相等。

**出生时点**：attempt AFTER INSERT 时 p 尚未存在，不得据此判 unmanaged。账户片建议 `pp_complete_attempt_birth(tenant,request,attempt)` 作为候选原 INSERT 后同事务闭合末端，检查 source 同 top transaction、实际原始 binding 与 managed p 后生成分类；deferred 原 attempt 反向要求唯一 origin。它只认证真实新出生，不能把旧 attempt 当前行登记为 origin；若让 APP 调该 closure，必须只接受本事务真正新源且禁止修改 cost/delivery 后补生。request 的自动出生也不能被 caller选择历史 before。未来把整个 RecordAttempt 收进单根可缩权限面，但不能在此之前省掉 closure/反向 guard。

`AttemptHead`：`attempt_id,ordinal,last_decision_id,member_count,member_tail_id`。ordinal0/tailNULL 起点；含全部成本/evidence/dispatch/recovery/更正来源，不依赖 reservation revision。managed member_count/tail 指精确成员链；unmanaged 成员域明确 absent，不能拿空 managed 集当凭证。

### 4.2 原始 incoming 与 request sticky 链

`IncomingSource`：`id,root_id,step_no,input_finalization_id,input_digest,tenant_id,request_id,attempt_id?,source,source_event_id,usage_status,input_tokens,output_tokens,total_tokens,cached_tokens,reasoning_tokens,protocol_outcome,transport_end_reason,raw_delivery_stage,raw_content_written,finish_reason,observed_at_input?,effective_observed_at`。

`RequestDecision`：`id,root_id,step_no,request_id,ordinal,previous_decision_id?,incoming_source_id,before_latest_id?,after_latest_id?,normalized_delivery_stage,normalized_content_written,branch(normal/conflict),finalization_id?,conflict_id?`。

联合约束：normal 必须 finalization_id 非NULL、conflict_id NULL、after_latest=本 finalization；conflict 相反、after_latest=before_latest；新 root 恰好一个 incoming 与一个 request decision。input 与 decision的 tenant/request/attempt关系不可跨域。normal 最终原 finalization.event_id、input_digest、outcome/reason/归一化stage/content/finish/policy全部精确匹配；conflict不能伪造 normal finalization。

**capture 位置**：先按原 lockRequest 顺序锁 tenant SHARE→membership SHARE→key UPDATE→model SHARE→customer reservation UPDATE，再以新 SQL statement 读 request.status/latest 及 RequestHead。在任何 sticky 计算、meter写入或 request.latest修改之前保存 before_latest；先按 raw content/stage 自身归一化，再消费该精确前驱：前驱content则sticky content，前驱committed且incoming none/unknown则committed。前驱可能来自 attempt1、attempt2、无attempt finalization，必须用 request 链而非每attempt链。保存 raw 原件能独立重算 normalized；不能在 AFTER finalization读取最终 latest补历史。

### 4.3 meter 选择、冲突完整来源与 evidence 成员

`MeterChoice`：`id,root_id,step_no,incoming_source_id,meter_event_id,meter_origin_id,disposition(created/reused),attempt_cut_before_id?,attempt_cut_after_meter_id?,collision_kind(none/source_payload_mismatch/other_confirmed_usage),competing_meter_id?`。创建原 meter 后立刻产生不可变 `MeterOrigin(meter_event_id,incoming_source_id,root_id,step_no)`；复用则指向真实旧 origin，不能生造第二origin。所有受覆盖 meter 恰好一个origin；不存在origin的旧 meter被复用时该域保留历史缺证。

source key `(tenant,source,source_event)` 的新插入/ON CONFLICT复用，必须在等唯一键后用**新 statement**读取原 meter，不能用 INSERT前快照。source_payload_mismatch 精确比较原 usage/request/attempt；冲突目标永远是 incoming attempt（可与旧meter的request/attempt不同），不能拿旧 meter 身份冒充incoming。另一个 confirmed usage 的 EXISTS 判定使用实际同attempt完整 meter前缀；其原meter来源也必须包含在 attempt source链，纯层能枚举cut以内全部成员，不能只记录一个 bool。competing_meter_id 是存在性定位，不替代完整集合或 absence 证明；source_payload_mismatch 时无须该 witness。

`ConflictSource`：`conflict_id,root_id,step_no,incoming_source_id,meter_choice_id`，精确一一绑定原 `metering_event_conflicts(id,event_id,normalized_usage)`。原 conflict 行缺 incoming binding/reason，由此新增原输入源补足；不得在读时从 meter或digest猜出。

`EvidenceChoice`（managed）：`id,root_id,step_no,attempt_id,ordinal,previous_decision_id?,incoming_source_id,meter_choice_id,evidence_digest,disposition(new_member/existing_member),identity_attempt_id,identity_digest,identity_event_id,member_count_before,member_tail_before?,member_count_after,member_tail_after?,procurement_transition_id?,skip_reason?`。原语义identity按Usage全部字段+reason；真正 INSERT identity才建立 `EvidenceMember(id,attempt_id,member_ordinal,previous_member_id?,identity_digest,identity_event_id,evidence_choice_id)`。ON CONFLICT未插入不增加member；一个成员唯一绑定原identity，原identity反向必须唯一member。existing_member保留首次原event_id，不能替换为本次event。

普通终态后新语义、already-disputed的新语义可增加成员而无PE/revision变化；重复语义可生成本次observed choice但cut不变。成员前缀由count+tail定位**可完整枚举的链**，不是仅count/hash。所有原identity、meter、conflict与来源链同时读，严格保证无漏源、漏尾或把未来source放入before cut。

### 4.4 正常成本、重置与当前成本选择

`NormalCostDecision`：`id,root_id,step_no,attempt_id,ordinal,previous_decision_id?,finalization_id,incoming_source_id,meter_choice_id,before_cost_status,before_cost_amount?,managed(bool),before_procurement_revision?,before_procurement_state?,before_procurement_held?,before_procurement_spent?,before_procurement_anomaly?,member_count_before?,member_tail_before?,account_ordinal_cut?,account_tail_cut?,decision,selected_amount?,selected_basis?,cost_mutation_id?,cost_recorded_outbox_id?`。

managed=true 的采购/账户字段全有，member_count=0 时仅member_tail可NULL；managed=false这些字段全NULL，明确unmanaged origin必须有。decision按**原优先顺序**为 skip_already_known、skip_disputed、write_no_consumption、write_final_usage、skip_no_final_usage_or_rate：

1. status!=unknown首先skip_already_known，即使同时disputed；
2. 否则disputed则skip_disputed；
3. 否则reason为not_sent/rejected_no_consumption写0/contract；
4. 否则仅confirmed_final、input/output rate均非NULL、frozen basis!=unknown，计算`ceil((input*rateI+output*rateO)/1000000)`；
5. 其余skip。skip各selected/mutation/outbox全NULL；write各字段全有。

**before位置**：request/attempt/account/PB/p锁已取得后、任何本次cost UPDATE及本次`finalizeProcurement`/identity/transition之前。它对应 `recordAttemptCost` 的历史资格，不能从approved或cost_confirmed AFTER trigger读当前行代替。normal source 必须是真正新finalization；重复normal快返不生成decision；conflict无正常finalization，禁止NormalCostDecision。

`CostMutation`：`id,root_id,step_no,attempt_id,ordinal,previous_decision_id?,cause_kind,cause_id,before_status,before_amount?,after_status,after_amount?`。cause精确联合为normal_decision、procurement_dispute_reset、incoming_conflict_reset、approval、unmanaged_correction。由实际受控UPDATE当场OLD/NEW生成，不能只记录最终成本。unknown⇔amountNULL，contract/invoice⇔非负金额。各cause精确指向同root原来源；每种预期UPDATE必须一条，禁止无来源直接更新。

`CostResetDecision`：`id,root_id,step_no,attempt_id,cause_kind,cause_source_id,procurement_state_before?,policy(applied/suppressed_adjudicated),cost_mutation_id?`。managed state settled/released时原外层conflict reset被抑制，不能擦掉已裁定invoice；新dispute会reset。normal finalize晚发现released正消费/settled正成本后无消费可转disputed，这时先有正常cost skip/write，再有采购dispute reset。当前Go的procurement dispute reset与外层incoming-conflict reset可能都执行，即便第二次为unknown→unknown，也分别保留实际decision/mutation，不能用终态快照掩盖顺序。

原 `cost_recorded` 只在normal write时生成、operation_id=attempt，payload精确为三字符串attempt_id/amount/basis；全历史至多一次，不因更正而重写。新typed `CostOutboxLink(root,step,cause_kind,cause_id,outbox_id)`反向一一匹配原row及tenant/operation/kind/payload。新finalization后的普通skip不制造新的cost_recorded。当前成本从origin的unknown/NULL开始按CostMutation完整顺序重放，包含所有normal写、reset、任意多次managed/unmanaged更正；同额新更正仍是独立mutation/decision。

### 4.5 proposal-birth与approval-consumed

`ProposalBirth`：`id,root_id,step_no,proposal_id,attempt_id,ordinal,previous_decision_id?,requester,requester_role,requester_status,reservation_revision,reservation_state,reservation_held,reservation_spent,reservation_anomaly,member_count,member_tail?,account_ordinal_cut,account_tail_cut?`。金额/basis/evidence/reason沿原proposal原件读。SQL在锁后真实p/member head取得屏障，原proposal expected revision/count必须精确等于此cut。source原proposal新INSERT与birth一一绑定；同输入proposal重试不新增birth。

`ApprovalConsumed`：`id,root_id,step_no,proposal_id,proposal_birth_id,approval_operation_id,attempt_id,ordinal,previous_decision_id?,requester,requester_role,requester_status,approver,approver_role,approver_status,consumed_revision,consumed_state,consumed_held,consumed_spent,consumed_anomaly,consumed_member_count,consumed_member_tail?,before_cost_status,before_cost_amount?,procurement_transition_id,procurement_event_id,provider_correction_id,cost_mutation_id,outbox_id,audit_id,after_revision,after_state,after_held,after_spent,after_anomaly`。

在任何p/cost更正前读取并保存consumed值，准确等于**提案出生时完整成员prefix**及expected revision/count。checker比较的是批准当时cut，不能要求approved旧proposal等于今天p/member状态。after由真实更正重算；p的(state,held,spent,anomaly)不变时revision不变，变则+1，basis-only或同额no-op不能强制+1。

approval金额来自proposal；新绝对金额→原PE `event_key=correction:<approval-op>,type=cost_corrected`，原correction operation `procurement:<approval-op>`，原outbox同operation/kind cost_corrected；所有tenant/attempt/amount/basis/evidence_ref逐值匹配，唯一被该consumed decision消费。原audit/action/actor/resource/request也精确绑定。原proposal UPDATE只允许proposed→approved一次，原屏障不可改。

建议先在private根局部变量capture，再生成effect/correction/mutation/approved/audit/outbox，最后一次INSERT完整ApprovalConsumed；不能由proposal AFTER approved触发器重读终态伪称before。deferred只核完整source闭合，不能到COMMIT时才补before。

### 4.6 unmanaged独立更正与恢复

`UnmanagedCorrectionSource`：`id,root_id,step_no,attempt_id,ordinal,previous_decision_id?,operation_id,provider_correction_id,input_actor_id?,effective_audit_actor,before_cost_status,before_cost_amount?,after_cost_status,after_cost_amount,cost_mutation_id,audit_id,outbox_id`。原operation max200，不强制UUID；actor空按原costActor为system。**原幂等identity不含ActorID**：同op且tenant/attempt/amount/basis/evidence_ref相同仍快返，不因换Actor制造新的audit；不同payload拒绝。此路径无managed proposal、双审、member或account证明，调用权限沿原事实；DB仅证明该受限业务入口用原input执行，不宣称数据库认证了最终用户会话或供应商账单。

`RecoverySource`：`id,root_id,step_no,attempt_id,ordinal,previous_decision_id?,checked_at,request_deadline,procurement_before_state,delivery_before_state,permit_source_id?,decision(expired_without_permit/recovered_unknown),procurement_transition_id,delivery_mutation_id`。APP不传expired/permit布尔。锁后新statement读取真实deadline/state与真实dispatch来源；仅p prepared且upstream prepared且无committed sending permit才释放；有permit/sending或其他eligible状态只能unknown。已不eligible则返回no-op且无新原event/source。没有meter/finalization也能有此typed source；永远不重发模型生成。

## 5. 单事务多来源与精确关联

允许同一事务顺序调用多个公开根、同request多个normal/conflict、同attempt多个合法操作；不靠“每事务只一根”的隐含前提。每次新业务来源获得DB新root_id，所有步骤拥有明确(root_id,step_no)，source↔step双向唯一；request/attempt/account各自连续链提供真实局部顺序。相同xid内也能精确区分两次conflict或两次审批。

- root_id不是由APP传入，private effect不能拿“当前最后decision”、GUC、query text、trigger depth或同xid任意候选行关联。
- 一个root只能消费自己template允许的typed sources，跨root可引用的仅不可变原件/已完成前缀（原meter、proposal_birth、member、prior latest等），且边必须有明确定义，禁止把其他root当本次新effect。
- 根函数创建的原source与typed链接在同事务闭合；root不在事务外追加见证。失败全回滚，各head序号无已提交缺口。
- 无全局ordinal。跨请求source-key meter复用通过其精确meter-origin→本次reuse有向边，且本次数据库唯一键等待后新快照选择；sameattempt meter源均在attempt完整前缀内。纯层检查循环、未来前驱、遗漏原件。
- 同语句多次函数求值时SQL调用文本顺序不作证明，只有实际锁后生成链顺序有效。不同request/account多根形成锁循环时40P01回滚，沿原重试，不能让未持锁before成为“历史证据”。

## 6. 权限、来源guard与防伪历史

角色/函数最终ACL由账户片统一冻结；本域必须纳入同一个完整权限闭包：

1. writer NOLOGIN、NOINHERIT、NOSUPERUSER、NOBYPASSRLS、NOCREATEDB、NOCREATEROLE、NOREPLICATION，不是DB/schema/table结构owner；任何LOGIN角色无writer成员关系或SET/ADMIN通路。structural owner与writer分离。
2.公开根 SECURITY DEFINER owner=writer，函数级固定`search_path=<精确受信业务schema>,pg_catalog,pg_temp`，schema首位与原PB current_schema命名空间一致，所有业务表/函数/类型/序列均显式schema限定；无动态SQL、无可被APP改写的配置驱动函数名，无可注入运算符或影子对象。writer只有必要表/列的SELECT/DML与明确private函数EXECUTE；不继承APP所有PB配置权限。
3. 根在任何读写前核实际session_user为受审APP LOGIN并与固定database/schema/role身份一致；owner通过SET ROLE或别的LOGIN继承APP不能冒充。PUBLIC默认EXECUTE撤销，APP只执行白名单公开根；projector/peer/worker无新读写/执行，除独立审定的原权限。安装包含ALTER DEFAULT PRIVILEGES与owner/role闭包验证，不能只看显式GRANT。catalog pin纳入函数正文、security/volatility/search_path、trigger、依赖、ACL与角色根。
4. 候选APP撤去原source及受保护投影的直接DML：meter、conflict、finalization、identity、PE、correction、proposal、procurement p/account桶及attempt cost、原冻结quote/identity、request.latest及出生身份不能绕根写。源guard与最小列权限双层检查，不能保留表级UPDATE后误以为列级REVOKE有效。
5. 其他业务仍要INSERT projection_outbox/audit时，以精确guard拒绝APP直接写受保护cost_recorded/cost_corrected及对应cost动作，或收进完整受控outbox/audit入口；不能为了新成本链让支付/充值无意失效。不能只封journal却放开源。
6. **特别注意**：用于检查`current_user=writer`的来源guard须SECURITY INVOKER，在SECURITY DEFINER根内部才见writer；不能把guard本身改SECURITY DEFINER后误以为可区分APP直接DML。新行出生的自动capture trigger如需definer，职责只限从实际NEW建立起点，不给caller任意历史登记能力。
7. 所有源/typed history拒绝UPDATE/DELETE/TRUNCATE；head仅writer按准确前驱更新，source与typed反向闭合。APP无ALTER TABLE/DISABLE TRIGGER/CREATE同schema/函数替换/owner/role提升/replication-role等绕过权。FK/trigger在实际LOGIN攻击验收中测试，不能owner或SET ROLE替代。
8. caller提交的是原调用事实，不是供应商真实性证明；ActorID仍来自原应用身份边界。DB当场核users精确role/status并保存实际读取值，但共享APP连接不能单凭ActorID证明真实人登录，不能将此设计宣传为外部签名。

来源guard、完整DDL、root函数实现和固定catalog共同约束“当场捕获”。只要APP仍可先UPDATE受保护成本/桶，再调用一个有`before`参数的函数，本设计就未达标。source当前存在、值相等、xmin相等或xid相等，都单独不足以证明该操作消费的历史快照。

## 7. before截取位置和锁后新快照总表

| 读取 | 必须先持有 | 精确位置 | 禁止替代 |
| --- | --- | --- | --- |
| request.latest/head | tenant/membership/key/model/customer reservation原锁 | sticky计算与meter/source写之前的新statement | 原lock前SELECT、AFTER最新投影 |
| normal cost status/p dispute | 上述→attempt→account→supplierPB→p | 本次normal成本UPDATE、identity和采购effect之前 | cost_confirmed/approved后重读 |
| evidence member prefix | 同attempt/p串行点与head | identity实际INSERT前；INSERT成功后仅推进一次 | count相同、当前count、event时间 |
| proposal birth屏障 | advisory op→当前operator→request→attempt→account/PB/p→head | proposal INSERT之前 | Go自报expected、今天revision |
| approval consumed屏障 | advisory op→当前approver/requester→request→attempt→account/PB/p→proposal→head（不得反向） | 任何p/cost更正之前 | proposal approved AFTER时的current p |
| unmanaged cost before | request/customer reservation→attempt | correction/cost UPDATE之前，真实无managed p | 由当前credential无绑定推历史unmanaged |
| recovery dispatch事实 | request→attempt→account/PB/p | 新statement读deadline、delivery与真实permit来源 | p sending本身、caller expired=true |

未在锁后重新读取，不得通过单statement CTE/旧snapshot称已观察当前。候选限定RC，与原s.transaction相同；RR/serializable进入明确拒绝或现有可识别rollback协议，不能以看不到新提交的快照构造历史。HEAD顺序由根统一冻结，不在trigger里引入p→attempt反向锁。

## 8. 返回、错误、幂等和未知提交

公开finalize返回一行typed结果：

```text
status = applied_normal | replay_normal | applied_conflict
new_root_id?         -- 前两applied有值；replay无新root
prior_root_id?       -- tracked原normal回执可定位；不授新权限
finalization?        -- normal两类全有；conflict全NULL
conflict_id?        -- 仅conflict有
semantic_code?      -- 仅conflict为metering_conflict
```

新root/steps在函数内部全部闭合后返回；Go先成功COMMIT，再将applied_conflict翻译为原`billing.Error{Code:metering_conflict}`。数据库约束/权限/上下文失败与业务拒绝不能伪装成可提交conflict；任何此类错误整体回滚。可冻结独立SQLSTATE映射表（invalid_evidence、invalid_usage、idempotency_conflict、forbidden、self_approval、procurement_*等），不要解析本地化SQL错误文本，不吞外键/资源错误。

精确分支必须保持：

- **normal同ID+原input_digest**：原校验及request锁后直接返回既有finalization；不新建incoming、request decision、member、cost decision或业务root，不因ObservedAt改变而变成新操作。
- **同ID不同digest**：不能先概括为拒绝。原实现先继续meter判断；如走conflict会提交新的conflict且不建normal finalization；只有到normal INSERT冲突分支才idempotency_conflict回滚。根必须等价保留。
- **重复conflict**：原实现每次INSERT真实新conflict；新request decision和incoming源也新增，即使同原ID/usage。semantic identity可去重，p/桶/成员count未必变化；不能用normal幂等吞掉原冲突行。
- **proposal retry**：先当前operator校验，再原payload逐值对比；同payload返回原proposal，无新source；失权即拒绝。
- **approval retry**：先当前approver、maker不同人、requester当前operator，再锁后检查approved相同approval-op+actor；同原操作快返无新source。不同proposal/actor/op复用拒绝。旧回执不绕当前授权。
- **不同approval操作同额更正**：它们有不同proposal/approval source，各自真实decision、PE(0,0)、correction、audit/outbox；即便p revision不变也不能合并。
- **unmanaged correction retry**：按§4.6原identity，不新增mutation/audit/outbox；managed调用拒绝。没有凭空新增双审。
- **回滚/重试**：仅原40P01/40001确定回滚最多3次；semantic拒绝不重试。COMMIT未知时禁止自动重发模型或重新审批；如Go已收到root ID，只读查询精确root/原operation；未得到能唯一定位conflict调用的root ID时返回outcome_unknown并保守人工/现有恢复核对，不能凭同inputID发现一条旧conflict就称本次成功，也不能自动调用再造一次。此设计不为解决传输不确定性改变原重复conflict语义。

## 9. 十一项命题的实际覆盖责任

新报告保留原Base与全部计数，不在原checker内清零。只有对应origin完整、逐source闭合且同快照完整读取才由新scope独立认定；旧历史缺origin继续indeterminate。单个24→25正例不能外推整表。

| 原Unproven | 本片必要来源 | 账户片/其他共同必要来源 |
| --- | --- | --- |
| OriginalDeliveryPredecessorUnproven | raw incoming + request origin/连续decision + 精确before_latest | 原finalization完整图；含无attempt与跨attempt |
| FinalizationOrderUnproven | request head/ordinal/normal及conflict分支、原latest闭合 | 完整原来源，不按时间排序 |
| ConflictIncomingUnproven | IncomingSource + ConflictSource + MeterChoice/MeterOrigin | source-key复用有向前驱与原meter完整集 |
| ConflictReasonUnproven | typed原reason与Usage的独立digest重算 | 不从旧meter/hash猜reason |
| IncomingBindingUnproven | incoming tenant/request/attempt及原attempt关系 | mismatch允许作为真实conflict，不能归错attempt |
| ProcurementEventOrderUnproven | evidence/recovery/approval真实step和transition精确source | account journal/PE/PB effect完整顺序、delta、origin |
| CostRecordedEligibilityUnproven | NormalCostDecision真实before与write/skip、quote/usage、outbox | 当时p状态；managed/unmanaged分别覆盖 |
| CurrentCostOrderUnproven | 所有CostMutation及reset/approval/unmanaged真实顺序 | 原当前attempt成本精确等于完整重放 |
| ProposalBarrierUnproven | ProposalBirth+完整member链+ApprovalConsumed及当前角色原读取 | 真实p transition/PE/correction/outbox/audit；same-revision no-op |
| AccountPauseOrderingUnproven | effect source与anomaly决定的原因绑定 | 账户片完整config/pause/journal前缀与revision |
| AccountAdmissionOrderUnproven | attempt管理模式出生、真正dispatch/recovery关联与account cut | 账户片真实reserve/dispatch gate、全部账户来源、配置/金额/阻断prefix |

只实现approval，不能覆盖正常cost资格；只实现attempt链，不能覆盖request sticky；只实现account链，不能覆盖incoming。unmanaged没有p，不是“因此全部采购历史N/A”；其normal/correction仍须request/attempt完整来源。新scope可按精确受证来源给出覆盖，但不能删Base gap或全类型减计数。

## 10. 验收与集成前未完成项

下列为完整行为的待验收项；只有另行交付的codec叶子已有独立限定验证，不转记为本提案通过：

- 完整typed DDL/联合NULL/PK/unique/FK、根模板和SQLSTATE表；新固定读集/basis/catalog/role根；实际source↔typed反向约束及EOF/Close/资源预算。
- actual APP全新出生成功：正常24→invoice25、同额与basis-only、多个correction、normal未知skip、disputed skip、争议后invoice不被重复证据擦除、proposal新增同count不同member拒绝、角色失权快返拒绝。
- 正常快返与重复conflict区别；同ID不同digest的两个实际分支；两root同事务、同语句多来源、不同attempt/无attempt sticky、跨request/attempt source冲突、先插新meter后成为conflict但无finalization的完整源。
- unmanaged单独normal+多更正+冲突reset；不由当前binding回推managed/unmanaged；对managed原单人更正拒绝。
- recovery prepared无permit释放、sending/已permit只unknown、无metertyped source、重复不增PE；Reconcile无attemptfinalization接线与semantic回滚区别。
- 真实APP攻击：直接source/投影/outbox/audit伪历史、跨root/tenant/attempt借源、预造/孤立见证、跳号/分叉/缺尾、head篡改、先改before再补证、角色继承/SET/ADMIN/defaultACL、临时对象/search_path/替换函数、TRUNCATE/禁用trigger。
- 旧原件/迁移/readset/roots/golden及旧checker接受集不变，旧缺证拒绝仍拒绝；新profile不因表存在自启。PG专用库串行；catalog5秒/APP30秒原预算不放宽。

**根集成须裁定的最后接口**：账户片的source-kind枚举、同writer具体schema/role/lock-head次序与PB最小权限（search_path业务schema首位，沿根权限合同保证原PB namespace不分裂）；request出生trigger与attempt closure反向guard；候选Reconcile整体接缝；与已交付codec叶子的精确对接。其余内容未落实前D1仍是拟冻结合同，不授权先写一个宽泛JSON witness再补。

## 11. 精确代码依据

- `internal/billing/types.go:115–146`：Usage、原FinalizationInput/返回；`:192` unmanaged输入。
- `internal/billing/evidence.go:29–69`：request锁后新快照；`:71–124` attempt创建/采购调用；`:214–263` 真dispatch/PB permit接线。
- `internal/billing/evidence.go:332–525`：normal快返、raw→sticky、meter/conflict、request/attempt与成本先后；`:527–556` 正常成本资格/outbox。
- `internal/billing/procurement.go:124–154`：account/PB/p锁；`:194–273` transition、identity去重、reset/终态/矛盾；`:278–332` 恢复；`:369–519`提案与批准。
- `internal/billing/recovery.go:111–248`：Reconcile与私有无attemptfinalization；`:251–304` unmanaged correction/Actor默认。
- `internal/billing/store.go:16–50`：RC/已知rollback重试；`:67–109` amount/usage；`:124–150` roles/audit/emit。
- `migrations/002_billing.sql:80–99`、`:160–172`：原meter/conflict/finalization/correction/outbox；`migrations/014_procurement_accounts.sql:73–217`：p revision/event/identity/投影/proposal guard。
- `internal/periodicbudget/tx_dispatch.go:11–68`：真实upstream prepared→sending与PB checked-at，不能用p sending替代。

行号仅帮助本次源码审查；正式文档使用可点击源码入口并在根统一架构复核后生成manifest。本提案尚无业务实现或PG验收；codec独立结果见对应验证记录，不改变任何原权限。

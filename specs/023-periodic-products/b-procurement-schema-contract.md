# B-P4 D1：物理字段、原件指针与约束布局

2026-11-03；基线`ed1badcf455f4a6f43d072de400198d43c2cfe07`。承接[共同](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)与[PB权限](b-procurement-pb-acl-contract.md)合同。本页固定32张新表及原表扩展的命名、字段展开与约束责任，供可执行DDL、无权威DTO及固定reader使用。**这是候选物理布局合同，没有安装SQL或业务writer；完整函数正文/DDL、静态catalog材料及实际验收未完成，D1整体不勾选。** 不修改原001–022、V3材料或旧历史。

## 1. 固定展开规则

以下是字段列表的精确缩写，不是JSON/SQL composite列。实现必须按出现顺序展开，禁止重复字段、运行时发现列、额外隐含ID或自行更名。`?`表示SQL NULL，其余NOT NULL。类型代号：T=TEXT、I=BIGINT、N=NUMERIC(100,0)、B=BOOLEAN、D=TIMESTAMPTZ、X=XID8、O=OID、J=JSONB、H=SMALLINT。所有D为finite；金额非负，delta可负；ordinal/count/revision域依各逻辑合同，不拿时间或xid排序。

| 宏 | 按顺序展开的物理列 |
| --- | --- |
| S(k) | id:T,root_id:T,step_no:I,step_kind:T generated(k),created_xid:X |
| C(k) | root_id:T,step_no:I,step_kind:T generated(k),source_id:T,created_xid:X |
| R | tenant_id:T,request_id:T |
| A | tenant_id:T,request_id:T,attempt_id:T |
| A? | tenant_id:T,request_id:T,attempt_id:T? |
| U(p) | p_usage_status:T,p_input_tokens:I,p_output_tokens:I,p_total_tokens:I,p_cached_tokens:I,p_reasoning_tokens:I |
| F(p) | p_label:T,p_provider:T,p_state:T,p_limit_amount:I,p_configuration_revision:I |
| K(p) | p_held_amount:N,p_spent_amount:N,p_blocking_count:I |
| P(p) | p_revision:I,p_state:T,p_held:N,p_spent:N,p_anomaly:B |
| Q | cost_version_id:T?,cost_input_rate:I?,cost_output_rate:I?,cost_basis:T |

宏后`?`将该组每列设为nullable；空prefix表示去掉prefix及其分隔下划线。例如U(空)首列usage_status；P(before)为before_revision/state/held/spent/anomaly。账户journal的reservation组为另一精确列名组`revision:I,state:T,held_amount:N,spent_amount:N,anomaly:B`，加before_reservation_/after_reservation_前缀；不能与P(p)混名。F/K仅用于账户head和journal，头部无前缀。

step_kind使用`GENERATED ALWAYS AS ('k'::text) STORED`，不是可输入DEFAULT；S(k).id是普通source主键。RequestOrigin/AttemptOrigin的source_id分别是原request_id/attempt_id，不另生成id列。所有source写入实际top-xid，head更新写实际updated_xid；APP不能直接写。纯DTO将I/N/X/O按规范十进制string传输，optional显式null，J保留完整原JSON值；不读xmin、不重建历史epoch、不将VACUUM后的tuple xmin比作birth xid。

## 2. 32张新表的固定字段

字段组合中可选组还须满足领域合同的全有/全无联合；单列nullable不代表调用方可任意组合。以下kind均为共同协议的canonical kind。跨引用的输出ID可先预分配，具体source只完整INSERT一次。

| 表 | 固定物理列（依序） |
| --- | --- |
| pp_operation_roots | id:T,entry_kind:T,scope_kind:T,tenant_id:T?,request_id:T?,attempt_id:T?,account_target_id:T?,created_xid:X,session_role_oid:O,step_count:I,semantic_result:T |
| pp_root_steps | root_id:T,step_no:I,kind:T,source_id:T,created_xid:X；随后§3的18列generated discriminator |
| pp_request_origins | request_id:T,root_id:T,step_no:I,step_kind:T generated(request_birth),created_xid:X,tenant_id:T,key_id:T,owner_id:T,model_id:T,price_version_id:T,input_rate:I,output_rate:I,input_upper_bound:I,output_limit:I,profile_version:T,policy_version:T,request_hmac:T,route_revision:I,request_deadline:D,instance_id:T,streaming:B,client_ip:T?,pricing_group_id:T?,tenant_pricing_revision:I,retail_priced_at:D,request_created_at:D,pb_created_xid:X,pb_tenant_control:T?,pb_key_control:T?,pb_admitted_at:D,initial_latest_id:T?,initial_status:T,initial_billing_status:T |
| pp_request_heads | request_id:T,ordinal:I,last_decision_id:T?,updated_xid:X |
| pp_attempt_origins | attempt_id:T,root_id:T,step_no:I,step_kind:T generated(attempt_birth),created_xid:X,tenant_id:T,request_id:T,sequence:INTEGER,channel_id:T,channel_version_id:T,credential_id:T,upstream_model:T,route_decisions:J,Q,quote_selected_at:D,attempt_created_at:D,pb_created_xid:X,initial_delivery_state:T,initial_cost_status:T,initial_cost_amount:N?,management_mode:T,account_id:T?,binding_id:T?,procurement_transition_id:T?,reserved_event_id:T?,account_journal_id:T?,admitted_at:D? |
| pp_attempt_heads | attempt_id:T,ordinal:I,last_node_id:T?,member_count:I?,member_tail_id:T?,updated_xid:X |
| pp_attempt_nodes | id:T,attempt_id:T,ordinal:I,previous_node_id:T?,root_id:T,step_no:I,step_kind:T,source_id:T,created_xid:X,previous_ordinal:I generated(ordinal-1) |
| pp_account_origins | account_id:T,version:H,create_operation_id:T,command_source_id:T,root_id:T,step_no:I,step_kind:T generated(account_command),journal_id:T,created_xid:X,created_at:D |
| pp_account_heads | account_id:T,ordinal:I,journal_id:T,updated_xid:X,F(空),K(空) |
| pp_account_journal | id:T,account_id:T,ordinal:I,previous_id:T?,previous_ordinal:I generated(ordinal-1),root_id:T,step_no:I,step_kind:T,created_xid:X,created_at:D,kind:T,source_kind:T,source_id:T,F(before)?,K(before)?,F(after),K(after),attempt_id:T?,binding_id:T?,before_reservation_revision:I?,before_reservation_state:T?,before_reservation_held_amount:N?,before_reservation_spent_amount:N?,before_reservation_anomaly:B?,after_reservation_revision:I?,after_reservation_state:T?,after_reservation_held_amount:N?,after_reservation_spent_amount:N?,after_reservation_anomaly:B?,input_bound:I?,output_bound:I?,upper_amount:I?,cost_version_id:T?,cost_input_rate:I?,cost_output_rate:I?,cost_basis:T?,gate_extra:I?,gate_checked_at:D?,pb_decision_at:D?,pause_event_id:T?,command_source_id:T?,transition_id:T?,delivery_source_id:T?,command_operation_id:T generated?,procurement_event_id:T generated?,dispatch_attempt_id:T generated? |
| pp_account_command_sources | S(account_command),created_at:D,operation_id:T,actor_id:T,actor_role:T,actor_status:T,kind:T,input_target_id:T,target_id:T,input_label:T,input_provider:T,input_state:T,input_limit_amount:I,input_digest:T,command_status:T,command_reason:T,command_created_at:D,audit_id:T,input_expected_revision:I?,journal_id:T? |
| pp_attempt_delivery_sources | S(attempt_delivery),A,mode:T,cause_source_id:T?,before_delivery_state:T,after_delivery_state:T,dispatch_account_id:T?,dispatch_journal_id:T?,dispatch_xid:X?,pb_dispatch_checked_at:D?；随后2列cause discriminator |
| pp_incoming_sources | S(incoming),A?,input_finalization_id:T,input_digest:T,source:T,source_event_id:T,U(空),protocol_outcome:T,transport_end_reason:T,raw_delivery_stage:T,raw_content_written:B,finish_reason:T,observed_at_input:D?,effective_observed_at:D,reconcile_source_id:T? |
| pp_meter_choices | S(meter_choice),A?,incoming_source_id:T,meter_event_id:T,meter_origin_id:T,disposition:T,attempt_cut_before_id:T?,attempt_cut_after_meter_id:T?,collision_kind:T,competing_meter_id:T? |
| pp_meter_origins | meter_event_id:T,incoming_source_id:T,C(meter_choice) |
| pp_conflict_sources | conflict_id:T,C(meter_choice),A?,incoming_source_id:T,meter_choice_id:T |
| pp_request_decisions | S(request_decision),R,ordinal:I,previous_decision_id:T?,incoming_source_id:T,meter_choice_id:T,before_latest_id:T?,after_latest_id:T?,before_status:T,after_status:T,normalized_delivery_stage:T,normalized_content_written:B,branch:T,finalization_id:T?,conflict_source_id:T?,previous_ordinal:I generated(ordinal-1) |
| pp_normal_cost_decisions | S(normal_cost_decision),A,before_cost_status:T,before_cost_amount:N?,management_mode:T,before_procurement_revision:I?,before_procurement_state:T?,before_procurement_held:N?,before_procurement_spent:N?,before_procurement_anomaly:B?,member_count_before:I?,member_tail_before:T?,account_id:T?,account_ordinal_cut:I?,account_tail_cut:T?,finalization_id:T,incoming_source_id:T,meter_choice_id:T,decision:T,selected_amount:N?,selected_basis:T?,cost_mutation_id:T?,cost_recorded_outbox_id:T? |
| pp_cost_mutations | S(cost_mutation),A,cause_kind:T,cause_source_id:T,before_status:T,before_amount:N?,after_status:T,after_amount:N?；随后5列cause discriminator |
| pp_cost_reset_decisions | S(cost_reset_decision),A,cause_kind:T,cause_source_id:T,procurement_state_before:T?,policy:T,cost_mutation_id:T?；随后3列cause discriminator |
| pp_evidence_choices | S(evidence_choice),A,incoming_source_id:T,meter_choice_id:T,evidence_digest:T,P(procurement_before),account_id:T,account_ordinal_cut:I,account_tail_cut:T,disposition:T,identity_attempt_id:T,identity_digest:T,identity_event_id:T,member_count_before:I,member_tail_before:T?,member_count_after:I,member_tail_after:T?,forced_conflict:B,effective_conflict:B?,conflict_cause:T?,decision:T,existing_dispute_event_id:T?,procurement_transition_id:T?,procurement_event_id:T?,account_journal_id:T?,skip_reason:T? |
| pp_evidence_members | id:T,attempt_id:T,member_ordinal:I,previous_member_id:T?,identity_digest:T,identity_event_id:T,evidence_choice_id:T,C(evidence_choice),previous_member_ordinal:I generated(member_ordinal-1) |
| pp_procurement_transitions | S(procurement_transition),created_at:D,A,account_id:T,binding_id:T,cause_kind:T,cause_source_id:T,event_id:T,journal_id:T,event_type:T,event_key:T,evidence_ref:T,held_delta:N,spent_delta:N,P(before)?,P(after),input_bound:I,output_bound:I,upper_amount:I,cost_version_id:T,cost_input_rate:I,cost_output_rate:I,cost_basis:T；随后4列cause discriminator |
| pp_proposal_births | S(proposal_birth),A,proposal_id:T,requester:T,requester_role:T,requester_status:T,reservation_revision:I,reservation_state:T,reservation_held:N,reservation_spent:N,reservation_anomaly:B,member_count:I,member_tail:T?,account_id:T,account_ordinal_cut:I,account_tail_cut:T,audit_id:T |
| pp_approval_consumptions | S(approval_consumed),A,proposal_id:T,proposal_birth_id:T,approval_operation_id:T,requester:T,requester_role:T,requester_status:T,approver:T,approver_role:T,approver_status:T,P(consumed),consumed_member_count:I,consumed_member_tail:T?,before_cost_status:T,before_cost_amount:N?,account_id:T,account_ordinal_cut:I,account_tail_cut:T,procurement_transition_id:T,procurement_event_id:T,provider_correction_id:T,cost_mutation_id:T,outbox_id:T,audit_id:T,P(after) |
| pp_unmanaged_correction_sources | S(unmanaged_correction),A,operation_id:T,provider_correction_id:T,input_actor_id:T?,effective_audit_actor:T,amount:I,basis:T,evidence_ref:T,before_cost_status:T,before_cost_amount:N?,after_cost_status:T,after_cost_amount:N,cost_mutation_id:T,audit_id:T,outbox_id:T |
| pp_recovery_sources | S(recovery),A,checked_at:D,request_deadline:D,P(procurement_before),delivery_before_state:T,permit_source_id:T?,decision:T,procurement_transition_id:T,delivery_mutation_id:T,procurement_event_id:T,account_journal_id:T |
| pp_cost_outbox_links | S(cost_outbox),A,cause_kind:T,cause_source_id:T,outbox_id:T；随后3列cause discriminator |
| pp_reconciliation_sources | S(reconciliation),R,case_id:T,action:T,actor:T,actor_role:T,actor_status:T,U(input)?,input_evidence_ref:T?,input_reason:T?,before_case_reason:T,case_created_at:D,before_case_state:T,after_case_state:T,before_reservation_status:T,before_requester:T?,before_approver:T?,requester_role:T?,requester_status:T?,after_requester:T?,after_approver:T?,U(case)?,case_evidence_ref:T?,case_proposal_reason:T?,approval_checked_at:D?,reservation_deadline:D,prior_latest_id:T?,derived_incoming_source_id:T?,finalization_id:T?,customer_finish_outcome_id:T?,audit_id:T |
| pp_customer_finish_outcomes | id:T,C(reconciliation),R,reconciliation_source_id:T,case_id:T,latest_finalization_id:T?,customer_reservation_id:T,reservation_amount:I,reservation_deadline:D,result_kind:T,selected_mode:T,selected_reason:T,settlement_decision_checked_at:D?,financial_checked_at:D?,before_case_state:T,after_case_state:T,before_case_reason:T,after_case_reason:T,before_reservation_status:T,after_reservation_status:T,before_request_status:T,after_request_status:T,before_billing_status:T,after_billing_status:T,before_key_held:I,before_key_spent:I,after_key_held:I,after_key_spent:I,raw_amount:N,charged_amount:I,waived_amount:N,result_status:T,settlement_id:T?,customer_journal_id:T?,customer_tenant_control_id:T?,customer_key_control_id:T?,billing_audit_id:T?,billing_outbox_id:T?,profile_block_model_id:T?,profile_block_profile_version:T?,profile_block_disposition:T?,profile_block_audit_id:T?；随后1列cause discriminator |
| pp_customer_finish_ledger_effects | finish_outcome_id:T,C(reconciliation),tenant_id:T,account_id:T,account_tenant_id:T?,account_kind:T,before_balance:I,after_balance:I,signed_delta:I,journal_line_id:T |
| pp_customer_finish_pb_effects | finish_outcome_id:T,C(reconciliation),scope:T,control_id:T,allocation_id:T,event_id:T,original_source_kind:T,original_source_id:T |

RequestOrigin.client_ip保持TEXT?；出生只保存实际`NEW.client_ip::pg_catalog.text`（原列为INET），validator/reader按相同PG16文本投影逐值比较，不以TEXT列直接复合FK到原INET列。该字段不代表原HTTP输入文字的逐字节保存。

quote_selected_at是DB实际选价语句的观察时点，源于§6完整根。它不充当目录前缀/全局序号；不能用今天cost_versions全集过滤时间，猜测旧statement可见全集或证明旧noquote absence。pure独立核所选不可变版本与attempt/p/source匹配；真正当时current选择由受审DB根的固定查询与实际锁/跨定时版本测试验证。后插入/回填生效时间的版本不改旧source。

## 3. generated列与键

所有PK/UNIQUE为普通非deferred唯一索引，供deferred FK引用；关系一致性检查可延迟到COMMIT，唯一操作身份冲突仍保持原语义。所有XID的相等只绑定同真实top事务，不证明先后。

RootStep依共同协议顺序增加18列，名为`d_`加以下kind，值为`CASE WHEN kind='k' THEN source_id ELSE NULL END`：request_birth、attempt_birth、account_command、attempt_delivery、incoming、meter_choice、request_decision、normal_cost_decision、cost_mutation、cost_reset_decision、evidence_choice、procurement_transition、proposal_birth、approval_consumed、unmanaged_correction、recovery、cost_outbox、reconciliation。每列类型TEXT且nullable，各自与root/step/kind/xid复合FK精确指向具体child；并非对18张表做OR EXISTS。

source的cause discriminator使用`CASE WHEN cause_kind='k' THEN cause_source_id ELSE NULL END`，列名`d_`加各自cause_kind：CostMutation五项（normal_cost_decision/procurement_dispute_reset/incoming_conflict_reset/approval_consumed/unmanaged_correction），CostResetDecision两项（procurement_dispute/incoming_conflict），ProcurementTransition四项（attempt_birth/evidence_choice/approval_consumed/recovery），CostOutboxLink三项（normal_cost_decision/approval_consumed/unmanaged_correction）。每个对应精确表、同root和A身份，cause step严格更早；incoming_conflict指ConflictSource companion自然conflict_id，不将其误认另一个canonical kind。所有generated列必须独立传输/计量/重算。

D1逐边DDL校准另增加AttemptDeliverySource的d_finalization/d_recovery（以mode映射cause_source_id）及CostResetDecision.mutation_cause_kind（applied时cause_kind追加_reset）；CustomerFinishOutcome另补d_new_settlement，仅result_kind=new_settlement映射settlement_id；三类cause的正反向FK不能靠三列id存在或validator省略身份。32表当前共715列/68 generated/647非generated；前版711列布局是当期设计物料，不是已发行数据库历史。

account journal三个原件discriminator名沿账户合同，CASE依据source_kind=command/budget_event/dispatch，生成原source_id；step_kind固定映射到account_command/procurement_transition/attempt_delivery，不能由caller任意选。

| 表组 | 必须有的主键、唯一键和前驱关系 |
| --- | --- |
| roots/steps | roots PK(id)、UNIQUE(id,created_xid)；steps PK(root_id,step_no)、UNIQUE(kind,source_id)、UNIQUE(root_id,step_no,kind,source_id,created_xid) |
| canonical child | S的id为PK，UNIQUE(id,root_id,step_no,step_kind,created_xid)；origin自然主键替换id；每child与step同组双向deferred FK |
| request head/decision | head PK(request_id)；decision UNIQUE(request_id,ordinal)、UNIQUE(request_id,ordinal,id)；previous_ordinal=ordinal-1精确引用同request前一decision；head0/tailNULL且updated_xid=origin.created_xid，其后精确末项；闭合latest/ordinal/xid，不将request.status永久FK到末decision |
| attempt head/node | head PK(attempt_id)；head0/tailNULL且updated_xid=origin.created_xid；node PK(id)、UNIQUE(attempt_id,ordinal)、UNIQUE(attempt_id,ordinal,id)、UNIQUE(root_id,step_no)，与step精确双向；ordinal1前驱NULL，否则引用同attempt的ordinal-1 |
| account origin/head/journal | 沿账户合同完整键；journal UNIQUE(source_kind,source_id)、UNIQUE(root_id,step_no)，同账户前驱和当前head四元组不能被p.old-last替代 |
| meter origin/conflict | PK分别meter_event_id/conflict_id，分别唯一绑定本次created MeterChoice/原conflict；source_id等本step的MeterChoice.id |
| evidence member | PK(id)、UNIQUE(attempt_id,member_ordinal)、UNIQUE(attempt_id,identity_digest)、UNIQUE(attempt_id,member_ordinal,id)；前驱同attempt+ordinal-1，原identity自然键(attempt,digest)双向绑定 |
| PE/source输出 | transition.event_id/journal_id、account source.operation_id/audit_id、proposal_birth.proposal_id/audit_id、approval.proposal_id/approval_operation_id/provider_correction_id/audit_id/outbox_id及unmanaged.operation_id/correction/audit/outbox分别唯一 |
| cost/normal/reset | 每write normal/reset/approval/correction的输出mutation_id唯一对应本cause；skip/suppressed不允许mutation；普通finalization的decision及正常成本决定按逻辑分支各唯一 |
| cost outbox | outbox_id UNIQUE，一份原cost outbox恰一个link，cause精确反向包含本link输出；原(operation_id,kind)唯一保留 |
| reconciliation/finish | source.audit_id唯一；finish PK(id)、UNIQUE(reconciliation_source_id)、UNIQUE(root_id,step_no)，source.future_outcome_id反向精确；ledger PK(finish_outcome_id,account_id)及原journal_line_id唯一；PB PK(finish_outcome_id,scope)及原event_id唯一 |

用于FK的复合目标必须建立**包含实际引用全部列**的UNIQUE；仅id FK后手写比较不替代合同所需的精确绑定。成本mutation与reset等互指的future输出关系使用INITIALLY DEFERRED。根模板、连续step、唯一node适用集、head/当前投影/原件反向集合检查由固定只读validator和不可绕过guard完成，不仅核count/hash。

### 3.1 generated表达式的固定展开表

下表将§2的“随后”列按物理顺序展开；account journal三列替换行内`generated?`占位而不重复追加。这三列及所有追加d_*列均TEXT nullable；行内step_kind与四个previous_ordinal为其声明的非nullable类型。这里的表达式是设计物料，不能作为完整DDL执行；step_kind常量及四个previous_ordinal已在行内声明。只读布局检查同时消费§1宏、§2表行与本表，不查询数据库。

| 表 | generated列 | 表达式 |
| --- | --- | --- |
| pp_root_steps | d_request_birth | CASE WHEN kind = 'request_birth' THEN source_id ELSE NULL END |
| pp_root_steps | d_attempt_birth | CASE WHEN kind = 'attempt_birth' THEN source_id ELSE NULL END |
| pp_root_steps | d_account_command | CASE WHEN kind = 'account_command' THEN source_id ELSE NULL END |
| pp_root_steps | d_attempt_delivery | CASE WHEN kind = 'attempt_delivery' THEN source_id ELSE NULL END |
| pp_root_steps | d_incoming | CASE WHEN kind = 'incoming' THEN source_id ELSE NULL END |
| pp_root_steps | d_meter_choice | CASE WHEN kind = 'meter_choice' THEN source_id ELSE NULL END |
| pp_root_steps | d_request_decision | CASE WHEN kind = 'request_decision' THEN source_id ELSE NULL END |
| pp_root_steps | d_normal_cost_decision | CASE WHEN kind = 'normal_cost_decision' THEN source_id ELSE NULL END |
| pp_root_steps | d_cost_mutation | CASE WHEN kind = 'cost_mutation' THEN source_id ELSE NULL END |
| pp_root_steps | d_cost_reset_decision | CASE WHEN kind = 'cost_reset_decision' THEN source_id ELSE NULL END |
| pp_root_steps | d_evidence_choice | CASE WHEN kind = 'evidence_choice' THEN source_id ELSE NULL END |
| pp_root_steps | d_procurement_transition | CASE WHEN kind = 'procurement_transition' THEN source_id ELSE NULL END |
| pp_root_steps | d_proposal_birth | CASE WHEN kind = 'proposal_birth' THEN source_id ELSE NULL END |
| pp_root_steps | d_approval_consumed | CASE WHEN kind = 'approval_consumed' THEN source_id ELSE NULL END |
| pp_root_steps | d_unmanaged_correction | CASE WHEN kind = 'unmanaged_correction' THEN source_id ELSE NULL END |
| pp_root_steps | d_recovery | CASE WHEN kind = 'recovery' THEN source_id ELSE NULL END |
| pp_root_steps | d_cost_outbox | CASE WHEN kind = 'cost_outbox' THEN source_id ELSE NULL END |
| pp_root_steps | d_reconciliation | CASE WHEN kind = 'reconciliation' THEN source_id ELSE NULL END |
| pp_cost_mutations | d_normal_cost_decision | CASE WHEN cause_kind = 'normal_cost_decision' THEN cause_source_id ELSE NULL END |
| pp_cost_mutations | d_procurement_dispute_reset | CASE WHEN cause_kind = 'procurement_dispute_reset' THEN cause_source_id ELSE NULL END |
| pp_cost_mutations | d_incoming_conflict_reset | CASE WHEN cause_kind = 'incoming_conflict_reset' THEN cause_source_id ELSE NULL END |
| pp_cost_mutations | d_approval_consumed | CASE WHEN cause_kind = 'approval_consumed' THEN cause_source_id ELSE NULL END |
| pp_cost_mutations | d_unmanaged_correction | CASE WHEN cause_kind = 'unmanaged_correction' THEN cause_source_id ELSE NULL END |
| pp_cost_reset_decisions | d_procurement_dispute | CASE WHEN cause_kind = 'procurement_dispute' THEN cause_source_id ELSE NULL END |
| pp_cost_reset_decisions | d_incoming_conflict | CASE WHEN cause_kind = 'incoming_conflict' THEN cause_source_id ELSE NULL END |
| pp_procurement_transitions | d_attempt_birth | CASE WHEN cause_kind = 'attempt_birth' THEN cause_source_id ELSE NULL END |
| pp_procurement_transitions | d_evidence_choice | CASE WHEN cause_kind = 'evidence_choice' THEN cause_source_id ELSE NULL END |
| pp_procurement_transitions | d_approval_consumed | CASE WHEN cause_kind = 'approval_consumed' THEN cause_source_id ELSE NULL END |
| pp_procurement_transitions | d_recovery | CASE WHEN cause_kind = 'recovery' THEN cause_source_id ELSE NULL END |
| pp_cost_outbox_links | d_normal_cost_decision | CASE WHEN cause_kind = 'normal_cost_decision' THEN cause_source_id ELSE NULL END |
| pp_cost_outbox_links | d_approval_consumed | CASE WHEN cause_kind = 'approval_consumed' THEN cause_source_id ELSE NULL END |
| pp_cost_outbox_links | d_unmanaged_correction | CASE WHEN cause_kind = 'unmanaged_correction' THEN cause_source_id ELSE NULL END |
| pp_attempt_delivery_sources | d_finalization | CASE WHEN mode = 'finalization' THEN cause_source_id ELSE NULL END |
| pp_attempt_delivery_sources | d_recovery | CASE WHEN mode = 'recovery' THEN cause_source_id ELSE NULL END |
| pp_cost_reset_decisions | mutation_cause_kind | CASE WHEN policy = 'applied' AND cause_kind = 'procurement_dispute' THEN 'procurement_dispute_reset' WHEN policy = 'applied' AND cause_kind = 'incoming_conflict' THEN 'incoming_conflict_reset' ELSE NULL END |
| pp_customer_finish_outcomes | d_new_settlement | CASE WHEN result_kind = 'new_settlement' THEN settlement_id ELSE NULL END |
| pp_account_journal | command_operation_id | CASE WHEN source_kind = 'command' THEN source_id ELSE NULL END |
| pp_account_journal | procurement_event_id | CASE WHEN source_kind = 'budget_event' THEN source_id ELSE NULL END |
| pp_account_journal | dispatch_attempt_id | CASE WHEN source_kind = 'dispatch' THEN source_id ELSE NULL END |

## 4. 原表扩展的固定组

原表新增列全部nullable，以保留旧行原字节和缺证状态。新受保护行由准确INSERT入口强制完整；APP不能事后把旧行NULL追踪列改为完整，也不能将新行装成旧行。原表既有列类型与默认不变。

G组依序为`pp_root_id:T?,pp_root_step:I?,pp_source_kind:T?,pp_source_id:T?,pp_source_role:T?,pp_created_xid:X?`；全NULL或全非NULL。带前缀组将`pp_`替换为表内精确前缀，例如`pp_approval_root_id`。source_kind为canonical kind，source_id为canonical source本身，不是companion或cause的ID。除audit/outbox已有逐槽UNIQUE外，不能对全部G组一刀切UNIQUE：同finish的多个journal_lines合法共用canonical source和ledger_line角色，须以原行ID及精确line companion闭合。

| 原表 | 新字段 / 角色 |
| --- | --- |
| request_records | pp_origin_root_id:T?,pp_origin_root_step:I?,pp_origin_xid:X?三列出生组；pp_last_decision_id:T?。origin以原id作source_id；last只由真实RequestDecision推进，其他客户资金status/billing更新不能抹掉last |
| upstream_attempts | 同三列出生组；pp_quote_selected_at:D?、pp_last_cost_mutation_id:T?、pp_last_delivery_source_id:T?；另保留账户§3.6准确六列dispatch组 |
| procurement_accounts/reservations/budget_events/commands | 逐字采用账户§3.6已冻列，不能再加一套G取代；command其他kind不冒用account_create/configure source |
| metering_events | G，kind=meter_choice/role=meter_origin；原id与唯一MeterOrigin及本次created choice精确闭合 |
| metering_event_conflicts | G，kind=meter_choice/role=conflict；原id与ConflictSource精确闭合，原event归属不充incoming归属 |
| finalization_records | G，kind=request_decision/role=finalization；只允许normal decision原件，conflict禁止 |
| procurement_evidence_identities | G，kind=evidence_choice/role=evidence_identity；原(attempt_id,evidence_digest)与唯一member出生step对应 |
| procurement_cost_proposals | G，kind=proposal_birth/role=proposal_birth；再加pp_approval_前缀的六列组，kind=approval_consumed/role=approval，仅实际批准时完整且终身不再替换 |
| provider_cost_corrections | G，kind=approval_consumed或unmanaged_correction，role=correction；两种来源封闭互斥 |
| reconciliation_cases | pp_last_前缀六列组，kind=reconciliation/role=reconciliation，表示最近人工操作，不认证其他自动markPending/finish的完整历史；原合法自动客户流程不能被错误禁止 |
| settlements | G，kind=reconciliation/role=settlement，仅本reconcile真正新建结算填写；读取既有结算不改旧指针，refunded_amount合法后续变化不被历史永久固定 |
| journals | G，kind=reconciliation/role=customer_journal，仅本次finish真正新建journal填写；其他原资金入口保持其原来源边界 |
| journal_lines | G，kind=reconciliation/role=ledger_line，与finish_ledger_effects每行原ID/账户/amount双向精确闭合 |
| audit_events/projection_outbox | G及共同§3.1的固定槽/UNIQUE；guard不能按shared action/kind全禁其他业务 |

billing_profile_blocks不新增“本次request所属”G。其自然主键(model_id,profile_version)可被多个请求复用，finish保存created/reused和本次profile-block audit，不能更新旧block使之归当前request。recovery_jobs为可删除调度队列，不加入不可变历史readset；本request清理仍为实际入口验收。

## 5. guard、validator与触发时序

新不可变表拒UPDATE/DELETE/TRUNCATE；head仅writer按真实旧末项推进，来源行不能pending→补before。列ACL和实际invoker guard共同拒raw APP/peer；完整acl见配套合同。trigger可自动运行不等于授caller其函数EXECUTE。

readonly validators固定为`pp_validate_root(text)`、`pp_validate_request(text)`、`pp_validate_attempt(text)`、`pp_validate_account(text)`、`pp_validate_original(tag text,key1 text,key2 text)`；均SECURITY INVOKER/VOLATILE/PARALLEL UNSAFE，无行锁/DML/授权发行。tag为硬编码封闭分支，key2仅复合原自然键时有值，不接表名/SQL/JSON proof；APP与writer只获其精确检查EXECUTE和必要SELECT。trigger wrapper传实际NEW/OLD自然键，不能caller自报passed。父表锁权采用[完整ACL](b-procurement-full-acl-contract.md#21-行锁的额外列权)的准确UPDATE单列及SECURITY INVOKER `aa_pp_parent_lock_only`：W任何真实UPDATE均拒绝，行锁本身不触发该guard。完整guard与validator正文、错误、trigger名字/事件/顺序仍须在可执行DDL中固定并验收。

必须满足以下时序，不允许用任意AFTER读取最终值补历史：

1. request INSERT：原018零售价约束及020出生stamp完成后，新的AFTER birth trigger在真实NEW上产生RequestOrigin/head与request_birth root。它只认证实际初态/冻结字段；retail_priced_at沿原较早选价时点，不另假证完整客户reserve。原reservation、资金、PB仍在原事务完整闭合。
2. attempt INSERT：只在§6受控根内执行；允许真实upstream原件先于origin，既有`pb_attempt_stamp`→`pb_dispatch_guard`完成后，由同根私有closure完成显式managed/unmanaged，AFTER INSERT不得因p尚未生成自行判unmanaged。managed的p BEFORE INSERT按字典序`pb_procurement_stamp`→`procurement_reservation_identity`→新`zz_pp_attempt_birth_p`；新hook读取原020已生成的真实NEW.pb_admitted_at，一次完整出生origin/reserved transition并派生p pointers，随后p本体才写入。相关p/journal/root/step边须真实deferred，不建pending source；新attempt反向deferred要求唯一完整origin。准确phase与原guard责任见[出生根合同§5](b-procurement-attempt-birth-writer-contract.md#5-admitted_at-的具体出生位置与私有-phase)。
3. 真实dispatch：早期invoker guard在原pb_dispatch_guard之前先拒APP旁路；原PB在实际prepared→sending时填pb_dispatch_checked_at；后期固定校验核实际NEW、source及许可pointer。候选不能把guard全部放到PB之后而允许伪造sending先触发窗口副作用。
4. 原cost/latest/p/PE与共享原件：消费已经出生source后，BEFORE核准确OLD/目标和本次输出pointer；AFTER只核RETURNING结果与维护必要投影；根最后seal，所有双向与末项检查在COMMIT闭合。根内与APP顶层COMMIT身份/只读权限分别验证。

账户create/configure命令source先完整保存独立DBclock生成的command_created_at，再由候选BEFORE command guard从该精确source覆盖NEW.created_at，实际原件逐值相等；不以source诊断created_at冒充未来DEFAULT，也不增加W的INSERT(created_at)。其他command kind仍沿原DEFAULT。该两kind的时间采样点与原saveCommand不同，按[账户合同](b-procurement-account-contract.md#34-pp_account_command_sources)明示兼容边界并验收。

新根/私有函数由writer拥有，表/schema/trigger仍归迁移owner，受信schema居首；所有PUBLIC EXECUTE撤销和APP精确补授单列新profile。最终catalog必须固定trigger精确名字、字母序、owner/proconfig/安全模式/正文/依赖，不能只检查“有一个guard”。

普通列SELECT清单不隐含系统列：原020及B-P1实时tuple guard需要的`SELECT(xmin)`按[完整ACL系统列sidecar](b-procurement-full-acl-contract.md#22-实时xmin的精确系统列sidecar)逐表/角色固定；V4 catalog须采集负attnum ACL及有效权限，不能复用只看attnum>0的旧collector后声称完整。该sidecar不加入105/121历史读集、纯DTO或144表basis，也不改变本页32新表715个普通/generated列的计数。

## 6. 校准：完整记录attempt的公开DB根

此前三参`pp_complete_attempt_birth(tenant,request,attempt)`作为APP公开根的设计由本节取代。原RecordAttempt的Go公开输入/输出不变；候选公开SQL根为：

```text
pp_record_attempt(tenant text,request text,sequence integer,
                  channel text,channel_version text,credential text,upstream_model text,
                  route_decisions jsonb,has_expected_quote boolean,
                  expected_version bytea,expected_input_rate bigint,
                  expected_output_rate bigint,expected_basis bytea)
```

公开参数不接attempt/root/source ID或报价选择时点。DB生成attempt ID；普通幂等已有序号时沿原返回原Attempt全部5字段，不新造root/origin、不补旧证据。成功typed返回`outcome(applied|replay),root_id?,source_id?,attempt_id,request_id,sequence,delivery_state,cost_status`；applied的root/source完整，source_id为自然attempt_id；replay的root_id/source_id均SQL NULL。Go仍重建原Attempt结构，不输出额外capability。

完整根严格按原RecordAttempt先输入/route_decisions→原parent/request/customer-reservation锁→readAttemptChecks→checkAttemptCostQuote→INSERT ON CONFLICT→managed admission/真实origin闭合的业务顺序执行，不能提前用旧attempt快返绕过原checks。DB在原currentAttemptCostSQL位置采一次quote_selected_at，按effective_at/created_at/id DESC选择实际quote，并将时点写pp_quote_selected_at；无quote仍有该次真实采时，费用/basis默认仍沿原unknown。unmanaged不在后来closure重新选择current quote；managed保持原account/PB等待后的复核和原admission时刻deadline/报价重核，不把首次选择当后续发送许可。

has_expected_quote=false时四个expected参数全NULL；true时两个BYTEA均非NULL，为原Go string的原始bytes（可为空字节，不能先转PG TEXT），两维rate各可NULL。仅原采购或路由政策真的消费CostQuote的分支，按DB实际quote字符串UTF8 bytes和nullable rates做原Equal比较；未消费的expected字段不存、不转TEXT、不额外校验。原manual/unmanaged等忽略NUL/非法UTF8或任意expected值的接受集因此保留，不能因SQL参数类型增加拒绝。route_decisions沿原typed结构/空数组/<=1000项及Go marshalled字节<=262144规则，JSONB规范化长度不代替原字节判定。

每项route decision准确五字段channel_id/version_id/priority/weight/reason，priority保持部署64位Go int域、weight为1..MaxUint64，不能cast BIGINT截掉上半区。固定字段顺序沿routing.Decision，字符串用原Go JSON编码，DB重建该规范JSON的UTF8长度独立核界，不接caller长度。空输入统一[]；唯一channel、validID原接受集、封闭reason及selected匹配由DB重复核验，不能只信Go已验证。物理传输JSONB::text可因空格大于原Go字节界，reader对这两个字段的transport界与262144业务界分离，详见[读集合同](b-procurement-replay-read-contract.md)。

固定保留私有`pp_complete_attempt_birth(tenant text,request text,attempt text)`，返回TABLE(root_id text,management_mode text,account_id text,reserved_event_id text,account_journal_id text,admitted_at timestamptz)。它无APP/PUBLIC/peer EXECUTE，只读取完整根刚生成的实际受控INSERT和pp_quote_selected_at，已有origin拒绝，不登记任意caller写的attempt；helper完成来源及效果后最后seal，外层完整根之后仅重建返回。managed账户/事件/journal身份及admitted_at完整，unmanaged均NULL。APP不再有upstream_attempts INSERT，普通旧构造器仍沿原路径，候选无失败回退。

本根必须先有真实request自动birth、新版command+reserve账户guard/validator及相关exact-link实际正文。现有account writer v1仍硬拒非command/p/PE，不能绕过或叠函数后冒称可写；旧材料/摘要不改，新候选显式扩充接受集。完整缺失正文与A0–A6依赖见[出生根实施切片](b-procurement-attempt-birth-writer-contract.md#8-实施切片和实际尚缺正文)，冻结不表示已实现或验收。

单个DB根整体受原statement_timeout=5s，不将每条内部SPI当新的5s；锁仍3s，上层绝对期限/原重试不变。该语句装配与旧多条SQL的总耗时边界不同，必须实际多等待与原接口回归，不宣称时间接受集完全相同。

## 7. 读集、部署与剩余义务

原source71/wallet87的完整union加32新表及已有reconciliation_cases、billing_profile_blocks两表，关系数候选为105/121；catalog128+32=160、basis112+32=144。准确列数、PK计数、descriptor/hash及所有原表新增列须由完整展开注册表与可执行DDL双向独立核对后固定，不能从live结果回填expected。本页尚无该注册表或SQL，不把这些关系数当已验证的最终profile。

完整DDL仍须逐项落实本页全部复合键/生成列/FK/guard/模板、固定根与helper正文、完整函数/类型/ACL和trigger时序，并以actual LOGIN验证。独立纯DTO/重放、reader/basis/catalog、保护阶段/runtime/兼容及完整成功/失败矩阵不能由布局合同替代。正式迁移/provision/维护CLI/启动/在线资金接线仍R2独立交付；旧11项Unproven与B-P3整体状态保持。catalog原5秒正式race专项已通过，详见[实际复验记录](../../docs/verification/periodic-prefix-catalog-cpu.md)，此前失败记录仍保留；不能再把这项已关闭的专项缺口写成当前门禁未过，也不能据此宣称B-P3整体完成。

### 7.1 本片列布局检查的实施边界

`scripts/check-procurement-layout`只消费本页§1宏、§2表与§3.1表达式，按声明顺序展开32张**新表**的715列（647可INSERT列、68 generated列），保存[候选列布局](b-procurement-column-layout-v1.json)。每列固定name/type/nullable/generated_expression；JSON声明`design-only-new-table-columns`，不包含原表新增组、PK/FK/CHECK、trigger/函数/ACL或reader/basis/catalog最终descriptor，不能安装、授予权限或发行运行证明。

默认命令只读且逐字节比较静态JSON；明确`--write`才可更新物料。拒绝未知宏/类型/语法、重复表列、超过PG63字节的标识符、缺少/重复generated表达式或引用不存在的列。专项测试采用独立每表计数oracle与上述负例，不连接数据库。`make procurement-layout-check`、`make check`、`make verify-mvp`及CI的other分区运行只读检查；其通过只证明列合同展开与物料一致，不改变D1整体未完成状态。

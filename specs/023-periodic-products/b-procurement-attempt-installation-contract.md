# B-P4：command＋request＋attempt 出生候选装配合同

2026-11-04；集成人已审定本页的 ABI、关系清单、真实暂态、锁用授权与文件所有权，已按本页形成独立 SQL/ACL/descriptor 材料与 renderer；限定 actual APP PG 结果见[验证记录](../../docs/verification/procurement-attempt-birth.md)。承接[完整出生根](b-procurement-attempt-birth-writer-contract.md)、[exact-link 集成](b-procurement-birth-exact-link-integration-contract.md)、[账户](b-procurement-account-contract.md)、[完整 ACL](b-procurement-full-acl-contract.md)与[PB ACL](b-procurement-pb-acl-contract.md)。本页把 A2/A3 的完整安装单元、旧材料冲突、精确接受集及源码 owner 顺序合并；它是设计合同；新材料仅在隔离 fixture 安装，不是正式迁移、线上启用或完整退出矩阵已通过。原 001–022、旧 relation/account-writer/A1/A0 文件与摘要不变。

## 1. 唯一 canonical 装配与实际范围

新候选标识为 `birth-exact-link-a2-v1`。受审 owner 在一个安装事务内，从**原 001–022＋B-P1**基线，安装 identity/A0、A1 最小关系切片、A2 关系扩展及本候选全部真实 writer/guard/validator/ACL。它是独立装配：不先执行完整 `procurementprefixschema.SQL`，不先安装 account-writer v1 或 A1 的封闭 writer 再删除其拒绝分支，不把旧候选的 expected catalog 变成宽松匹配。

安装前静态 inventory 与实际 catalog 必须确认没有旧 `pp_ac_*` mutation/validator/trigger 或 `pp_rb_*` writer/guard/validator/trigger；若发现混装，整体拒绝，不自动 DROP、不禁 trigger、不忽略错误。仅同源的五个 `pp_ac_*` 纯函数可由本候选专门材料独立创建，见 §4。A1 三份关系材料的同源定义可以复用；A1 的四表**封闭** reader/writer descriptor 不可复用为 A2 descriptor。直接升级一个已运行 A1 schema、保留其中业务数据的转换及旧完整 relation 的拆除均不属于此安装合同。

最终接受集为：

| 根/来源 | 本片真实支持 | 封闭限制 |
| --- | --- | --- |
| 原 APP `Reserve` 的 request INSERT | 与 A1 同源的真实 AFTER 自动出生；完整保留冻结快照、资金暂态、pending/finish 和 legacy 接受集 | request head 始终 ordinal0/last NULL；不写 RequestDecision/latest |
| `pp_create_account` / `pp_configure_account` | create、configure、noop、revision_conflict、not_found、匹配 replay；原 command 幂等/授权/快照 | command-only 旧链不再是整个接受集；账户可交错合法 reserve journal |
| `pp_record_attempt` | 全部原 checks 之后的首次 INSERT，以及原序号冲突 replay；明确 managed / unmanaged | managed 只有 reserved transition；不派发、不完成、不更正成本、不恢复、不造 outbox/audit |
| 五只读 `pp_validate_*` | 真实闭合上述受支持来源、原件、head 与 command＋reserve 前缀 | 合法但尚未支持的域 PP906；受证却缺边、错绑、额外输出或损坏 discriminator 为 PP902 |

没有 dispatch/delivery、finalization/incoming、cost/member、proposal/approval、case/finish companion 表、根或 DML。保留完整表中的 generated discriminator 不等于启用对应分支。默认 `billing.New`、线上 profile、普通启动与生产迁移不改；未来显式 prospective executor 不能失败后回退旧 SQL。A2 成功也不关闭全域 D1/I1、B-P3、十一项历史命题、R2 或供应商/生产验收。

## 2. 关系、列与原件扩展的封闭清单

### 2.1 十二张新表，共 262 列

类型、NULL 性、generated 表达式逐列取[旧 tables-v1](../../internal/procurementprefixschema/materials/tables-v1.sql)的准确切片；不重定义为 JSON、不修改既定 715 列全域布局。下列 `[g]` 是 generated 列，实际材料不得 INSERT/UPDATE 它。其余未列新表不创建。

| 表 | 列数 | 准确列序 |
| --- | --- | --- |
| `pp_operation_roots` | 11 | id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result |
| `pp_root_steps` | 23 | root_id,step_no,kind,source_id,created_xid,d_request_birth[g],d_attempt_birth[g],d_account_command[g],d_attempt_delivery[g],d_incoming[g],d_meter_choice[g],d_request_decision[g],d_normal_cost_decision[g],d_cost_mutation[g],d_cost_reset_decision[g],d_evidence_choice[g],d_procurement_transition[g],d_proposal_birth[g],d_approval_consumed[g],d_unmanaged_correction[g],d_recovery[g],d_cost_outbox[g],d_reconciliation[g] |
| `pp_request_origins` | 33 | request_id,root_id,step_no,step_kind[g],created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status |
| `pp_request_heads` | 4 | request_id,ordinal,last_decision_id,updated_xid |
| `pp_attempt_origins` | 30 | attempt_id,root_id,step_no,step_kind[g],created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at |
| `pp_attempt_heads` | 6 | attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid |
| `pp_attempt_nodes` | 10 | id,attempt_id,ordinal,previous_node_id,root_id,step_no,step_kind,source_id,created_xid,previous_ordinal[g] |
| `pp_account_command_sources` | 24 | id,root_id,step_no,step_kind[g],created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id |
| `pp_account_origins` | 10 | account_id,version,create_operation_id,command_source_id,root_id,step_no,step_kind[g],journal_id,created_xid,created_at |
| `pp_account_heads` | 12 | account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count |
| `pp_account_journal` | 58 | id,account_id,ordinal,previous_id,previous_ordinal[g],root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,attempt_id,binding_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,pause_event_id,command_source_id,transition_id,delivery_source_id,command_operation_id[g],procurement_event_id[g],dispatch_attempt_id[g] |
| `pp_procurement_transitions` | 41 | id,root_id,step_no,step_kind[g],created_xid,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,cause_kind,cause_source_id,event_id,journal_id,event_type,event_key,evidence_ref,held_delta,spent_delta,before_revision,before_state,before_held,before_spent,before_anomaly,after_revision,after_state,after_held,after_spent,after_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,d_attempt_birth[g],d_evidence_choice[g],d_approval_consumed[g],d_recovery[g] |

A1 四表 71 列，另八表 191 列。新 descriptor 必须比较准确表集合、列序/类型/NULL/generated/default，不以“至少有这些列”验收。只有 head 投影可按枚举状态机 UPDATE，其余新关系不可 UPDATE/DELETE/TRUNCATE。

### 2.2 七张原表，共 38 个 nullable 扩列

取[原 original-columns-v1](../../internal/procurementprefixschema/materials/original-columns-v1.sql)中的下列精确组；均无 DEFAULT、无 backfill，不扩既有原列类型。request 四列由 A1 同源 DDL 创建一次；A2 再增加 34 列。

| 原表 | 新列及准确类型 |
| --- | --- |
| `request_records` | pp_origin_root_id TEXT,pp_origin_root_step BIGINT,pp_origin_xid XID8,pp_last_decision_id TEXT |
| `upstream_attempts` | pp_origin_root_id TEXT,pp_origin_root_step BIGINT,pp_origin_xid XID8,pp_quote_selected_at TIMESTAMPTZ,pp_last_cost_mutation_id TEXT,pp_last_delivery_source_id TEXT,pp_dispatch_source_id TEXT,pp_dispatch_root_id TEXT,pp_dispatch_root_step BIGINT,pp_dispatch_xid XID8,pp_dispatch_account_id TEXT,pp_dispatch_journal_id TEXT |
| `procurement_accounts` | pp_version SMALLINT,pp_created_xid XID8,pp_origin_journal_id TEXT |
| `procurement_reservations` | pp_created_xid XID8,pp_origin_journal_id TEXT,pp_last_journal_id TEXT |
| `procurement_budget_events` | pp_root_id TEXT,pp_root_step BIGINT,pp_transition_id TEXT,pp_journal_id TEXT,pp_created_xid XID8 |
| `procurement_commands` | pp_source_id TEXT,pp_root_id TEXT,pp_root_step BIGINT,pp_created_xid XID8,pp_account_journal_id TEXT |
| `audit_events` | pp_root_id TEXT,pp_root_step BIGINT,pp_source_kind TEXT,pp_source_id TEXT,pp_source_role TEXT,pp_created_xid XID8 |

attempt 的六个 dispatch pointer 与两个 last pointer 在本片始终 NULL；保留既定列组是为了准确检测假来源，不能给它们 UPDATE 权。既有原件全部新列 NULL 才可能是 legacy；一项非 NULL 或存在来源/head/step 即不能降为 legacy。不得给所有旧 original.id 加无条件 source FK。

## 3. 窄键、逐边 exact-link 与约束装配

### 3.1 native 键与 CHECK

原 001–022/B-P1 所有 native 键/FK/trigger 保留。新 native 键仅以下清单，全部即时；含未证 TEXT 的历史 tuple 不先安装再删除。

| 表 | 安装的 PK/UK（准确旧名） |
| --- | --- |
| roots / steps | `pp_s_root_pk(id)`、`pp_s_root_xid_uq(id,created_xid)`；`pp_s_step_pk(root_id,step_no)` |
| request origins / heads | `pp_s_request_birth_pk(request_id)`；`pp_s_request_head_pk(request_id)` |
| attempt origins / heads / nodes | `pp_s_attempt_birth_pk(attempt_id)`；`pp_s_attempt_head_pk(attempt_id)`；`pp_s_attempt_node_pk(id)`、`pp_s_node_step_uq(root_id,step_no)` |
| command sources | `pp_s_account_command_pk(id)`、`pp_ad_command_operation_id(operation_id)`、`pp_ad_command_audit_id(audit_id)` |
| account origins / heads / journal | `pp_ad_origin_pk(account_id)`、`pp_ad_origin_create_operation_id(create_operation_id)`、`pp_ad_origin_command_source_id(command_source_id)`、`pp_ad_origin_journal_id(journal_id)`；`pp_ad_head_pk(account_id)`；`pp_ad_journal_pk(id)`、`pp_ad_journal_step(root_id,step_no)` |
| transitions | `pp_s_procurement_transition_pk(id)`、`pp_ad_transition_event_id(event_id)`、`pp_ad_transition_journal_id(journal_id)` |

上述 root/node/journal 双列组合只有 DB 生成的 canonical 非零 UUID（36 ASCII 字节）＋BIGINT/XID8；其 guard 必须真实强制该证书。自然 ID 的单列键保留各自原接受集，不把所有原 TEXT 强转 UUID。`(account_id,ordinal)`、`(source_kind,source_id)`、position/xid/quote/before/after/head/payload 等其余旧复合 UK 均不装；相应唯一性由账户 N→head U 串行锁、来源一次消耗、同 root 精确槽及双向全枚举保留。audit 的 slot UK 本片亦不追加，由 command source 唯一 audit_id、准确槽与反向枚举闭合，不提前安装未消费的完整域宽键。

CHECK 正文取旧材料同源定义：A1 原八项；source 的 `pp_s_attempt_birth_step_ck/pp_s_account_command_step_ck/pp_s_procurement_transition_step_ck/pp_s_attempt_initial_ck/pp_s_attempt_quote_ck/pp_s_attempt_management_ck/pp_s_attempt_head_shape_ck/pp_s_node_shape_ck`；account-constraints 的以下 24 项；原件另六项 `pp_o_attempt_birth_ck/pp_o_account_birth_ck/pp_o_procurement_birth_ck/pp_o_procurement_event_ck/pp_o_command_source_ck/pp_o_audit_group_ck`。共 46 CHECK，不安装 account-constraints 中属于未来 delivery/evidence 等域的另外 11 项。CHECK 的全域 kind 接受集不能代替本片 guard 的更窄真实状态机。

```text
pp_ad_origin_values,pp_ad_head_values,pp_ad_journal_position_ck,pp_ad_journal_kinds,
pp_ad_journal_source_ck,pp_ad_journal_config_before,pp_ad_journal_config_after,
pp_ad_journal_reservation_union,pp_ad_journal_gate,pp_ad_journal_dispatch_ck,
pp_ad_journal_pause,pp_ad_journal_bucket_delta,pp_ad_command_values,pp_ad_command_branches,
pp_ad_transition_scalar,pp_ad_transition_cause_ck,pp_ad_transition_before_ck,
pp_ad_transition_deltas,pp_ad_transition_targets,pp_ad_transition_original_p,
pp_ad_journal_money_delta,pp_ad_journal_command_buckets,pp_ad_transition_predecessor,
pp_ad_transition_before_p
```

### 3.2 外键与精确边逐项替换规则

[集成合同附录 A/B](b-procurement-birth-exact-link-integration-contract.md#附录-ba2消费的旧边与精确替换范围)是完整映射前像，A2 的 59 边逐一纳入安装 descriptor。标为 native 的边保留准确原名、列序、`MATCH SIMPLE/DEFERRABLE INITIALLY DEFERRED`；不装其余旧复合 FK/目标宽 UK。A1 四个 `pp_bx_*` locator FK 保持同定义。A2 新 locator 名精确定义为 `pp_ba_`＋旧 FK 名去掉开头 `pp_`，不得截断/自动命名；唯一额外分类是下述 `pp_ad_birth_p` 既有双列键替换边，保留准确旧名但不宣称保留旧六列 tuple。完整 expected 中保存每个展开后的名字与真实两端列序。

附录 B 的 12 个保留 native 边精确为 `pp_s_attempt_birth_root_fk/pp_s_attempt_origin_head_fk/pp_s_attempt_head_origin_fk/pp_s_node_origin_fk/pp_s_account_command_root_fk/pp_s_procurement_transition_root_fk/pp_ad_head_origin/pp_ad_journal_origin/pp_ad_journal_binding/pp_ad_transition_binding/pp_ad_birth_binding/pp_ad_birth_account`；另有 1 个复用原既有双列 UK 的 NULL 激活保持替换边 `pp_ad_birth_p`，其余 46 个为新 locator（包括引用 step 的固定双列 locator）。加 A1 已定的 6 native＋4 locator，共 69 个 FK，连同 22 个 PK/UK 和 46 个 CHECK，关系候选总计 137 约束；原 001–022/B-P1 约束不计入这 137 项。

每个非 native 旧边以附录所列父 locator 建立真实 native FK；parent 为 step 时引用 `(root_id,step_no)`，下述 birth→p 复用原既有双列 UK，其余引用单列 PK。原件反向出生边另采用以下 locator 覆盖，避免 legacy 无条件引用 source：

| 旧边 | 新 locator 的真实子列 → 父列 | 最终必须另核的完整关系 |
| --- | --- | --- |
| `pp_s_original_attempt_birth_fk` | upstream_attempts(pp_origin_root_id,pp_origin_root_step) → pp_root_steps(root_id,step_no) | actual attempt.id/tenant/request/quote_selected_at、完整出生组和对应 attempt origin 逐字段相等 |
| `pp_ad_account_origin` | procurement_accounts(pp_origin_journal_id) → pp_account_journal(id) | actual account.id/version/xid/created_at ↔ account origin ↔ create journal |
| `pp_ad_original_command_fk` | procurement_commands(pp_source_id) → pp_account_command_sources(id) | 原 command 的完整身份/输入摘要/状态/时间/来源及 journal 联合 |
| `pp_ad_original_transition_fk` | procurement_budget_events(pp_transition_id) → pp_procurement_transitions(id) | 原 PE 的完整字段/五 pointer ↔ transition ↔ reserved journal |

其余非 native 子列按附录所列父 locator 的**对应子槽**取值；例如 head→journal 取 journal_id，birth→transition 取 procurement_transition_id，step→origin 取 generated d，不能因同表还有 id 就选错。原件 G→step 使用 pp_root_id/pp_root_step；nullable locator 不等于省略其精确 NULL 联合。重复语义边可以有相同窄列，但每个旧边保留可追踪的命名和逐字段 exact 检查，不以一个“id 存在”查询代替多个关系。

`pp_ad_birth_p` 必须安装为 `pp_attempt_origins(attempt_id,account_id)` → 原 014 已有 `procurement_reservations(attempt_id,account_id)`，准确引用其 `procurement_reservations_attempt_id_account_id_key`，保留 `MATCH SIMPLE/DEFERRABLE INITIALLY DEFERRED`。不新增 key、generated 列或布局；旧六列中 binding/xid/journal/admitted_at 仍由逐槽 exact 检查闭合。这是 **existing-key activation-preserving replacement**，不是旧六列 native FK 原样保留，也不是 `attempt_id` 单列 locator。managed 的六项联合全部非 NULL，两列边真实激活；unmanaged 的 account/binding/journal/admission 均 NULL，双列边随 account_id 为 NULL 保持未激活且必须无 p。不得把它窄化为非 NULL attempt_id 而强迫 unmanaged 创建 p，也不得删除 FK 绕过错误。

### 3.3 全 59 边的 NULL 激活复核

每条 `MATCH SIMPLE` 边仅在**全部实际子列非 NULL** 时执行存在性约束；locator 选取必须同时审查旧 tuple 的激活条件和合法分支中的父缺席。descriptor 的 59 条 `null_activation_review` 逐边保存旧/新触发列、受支持分支的 NULL 列及旧/新激活结果，不能只比较目标 id 类型。以下是该完整 registry 的分类摘要；本次仅修复上面的 birth→p 边，其余准确定义不变：

| 子关系 / 受支持分支 | NULL 与父存在结论 |
| --- | --- |
| root / request birth、account command、attempt birth | account root 的 tenant/request/attempt 均 NULL；request birth 的 attempt NULL；attempt root 的三项完整。两个 attempt locator 随 attempt_id NULL 一同关闭，不要求 request/account root 存在 attempt。 |
| step / 四种实际 kind | generated `d_attempt_birth/d_account_command/d_procurement_transition` 只有匹配 kind 非 NULL；其余 kind 的反向边保持关闭。 |
| attempt origin / managed、unmanaged（含无报价和不完整报价） | 身份/root/step/original/head 边始终激活；binding/account/p/admission/transition 六项 managed 联合全部有值才激活。unmanaged 无 p/PE/journal/transition，相关边全部关闭；birth→p 复用双列 key 修复这一边界。 |
| attempt head / head0、managed head1；node / 首节点 | head0 的 last_node_id NULL，不要求 node；head1 指向真实 node1。node1 previous_node_id NULL，不要求前驱。member 域尚未安装。 |
| command source / create、configure、revision conflict、noop、not_found | 真实 command/audit 始终有父；not_found 的 journal_id NULL，journal 边关闭，其余四种均有准确 journal。 |
| account origin/head | 本片已认证 origin/head 的相关身份与配置均非 NULL；其必需父真实存在。原 account 旧三 pointer 全 NULL 时反向 source 边关闭；部分 pointer 不降级 legacy。 |
| journal / create、configure、configure_rejected、reserve | create.previous_id NULL，前驱边关闭；后续 ordinal 有真实前驱。command journal 的 binding/transition/attempt 保留 NULL，reserve 的 command_source_id NULL，两个 source 联合边只激活所属分支。reserve 的 before-p 五项 NULL，使旧 `pp_ad_journal_transition_before` tuple 未激活，而新的 transition_id locator 已激活：这里父 reserved transition 本来就必须存在，由同一 journal 的 transition 因果边及 exact guard/validator 核实；这个额外存在性检查不要求缺席父，也不把 NULL before-p 冒充完整旧 tuple。 |
| reserved transition | before-p 五项为 NULL，但不消费它们作为子列的 locator；实际 quote/bounds、binding、event/journal/cause 均完整且有父。quote 维度缺失的 managed reserve 不属于支持集，必须拒绝，未来非 reserved cause 仍为 PP906。 |
| 原 upstream/p/PE/command/audit | 真正 legacy/原 APP 绑定、余额观察、普通 audit 的整组 nullable pointer 全 NULL，反向出生 locator 关闭；A2 完整组才激活。原组部分 NULL 或错 discriminator 由 guard/validator 拒绝，不能借 MATCH SIMPLE 逃逸。 |

上述复核针对本片完整合法状态及其明确暂态：缺父只能在出生顺序内由真实 deferred FK等待后续行；COMMIT 必须全闭合。`journal_transition_before` 的已说明收紧是对必需父的存在性再核，不能扩展为“所有旧 NULL tuple 都可无条件换非 NULL id”。本片以外的未来状态不由这些 locator 暗中接纳，仍按接口的 PP906/受证损坏 PP902 分类。

双端 guard 在真实出生阶段执行：存在父按 §6 的准确显式 SHARE 语句锁定，**等待结束后的下一条独立 RC statement**重读，再逐槽 `IS NOT DISTINCT FROM` 比较；父 UPDATE/DELETE 在真实锁下反向枚举或拒绝。保留 request 冻结 payload、attempt 实际 quote/route/出生快照、managed 六项联合、p/PE/journal before/after/quote/admission 与原金额的全字段对照。不得 hash/截断/拼接 ID、JSONB::text 长度、归一化或空值折零。

有限缺父许可只来自真实出生顺序：A1 origin/head→尚未出生 step/root；新 upstream→随后 origin；managed hook 的完整 origin/transition→尚未物理出生 p/PE/journal/step/root。每条相关 FK 必须真 deferred。不能让 BEFORE 在任意缺父时返回成功，不能建 pending source 或 UPDATE 补 payload。seal 与真实外层 APP COMMIT 的双端 CONSTRAINT trigger 共同保证最终无缺边、无多边、无非法 NULL；只读 validator 不加锁或补写。

## 4. 函数 inventory 与无循环验证调用图

### 4.1 同源叶子、旧闭包与新候选的边界

A0 原五函数完整同源复用，准确签名为：

```text
pp_at_uuid_parse_v1(text) → boolean
pp_at_route_json_v1(jsonb,text,text) → bytea
pp_at_expected_shape_v1(boolean,bytea,bigint,bigint,bytea) → boolean
pp_at_quote_equal_v1(boolean,bytea,bigint,bigint,bytea,text,bigint,bigint,text) → boolean
pp_at_current_quote_v1(text,text)
  → TABLE(quote_selected_at timestamptz,cost_version_id text,input_rate bigint,output_rate bigint,basis text)
```

路由 JSON 的 Go 编码界、完整 uint64 weight、UUID 接受集、expected 原始 BYTEA 与无报价真实 DB 采时沿[A0 合同](b-procurement-attempt-rules-contract.md)。前四个纯函数不做 DML；current_quote 是 VOLATILE 的真实读取。identity v1 的六个原签名与正文保持原材料，按本片实际传递调用逐项授 EXECUTE；不能因为六个函数已创建就全授业务 mutation 能力。

账户五个纯函数从旧正文独立、逐字节提取到新 pure 材料，不执行旧整个 writer：

```text
pp_ac_uuid_v1(text,boolean) → boolean
pp_ac_text_v1(text) → boolean
pp_ac_command_digest_v1(text,text,text,bigint,text,text,text,bigint) → text
pp_ac_time_v1(timestamptz) → text
pp_ac_snapshot_v1(text,text,text,text,bigint,numeric,numeric,bigint,timestamptz) → jsonb
```

旧 `pp_ac_require_writer_v1/pp_ac_require_root_v1/pp_ac_mutate_v1`、三只读 validator、七 trigger 函数及旧公开根正文不安装。旧 v1 的根/step guard 只接受 command，account guard 不允许 reserve 桶变化，account validator 遇 p/PE/非 command journal PP906；必须在新正文扩充准确接受集。A1 的七函数亦不安装：其 validator 明确拒其他 pp 表，且 `count(roots WHERE request_id)=1`，无法认证 birth＋attempt 多根。不能只放宽该 count 或删除 PP906。

### 4.2 新普通函数的准确 ABI

除下列 pure 依赖外，新函数均 VOLATILE/PARALLEL UNSAFE、非 STRICT，固定受信 schema,pg_catalog,pg_temp，所有关系/普通调用显式限定。仅三个公开根与 request AFTER capture 为 W-owned SECURITY DEFINER；其他函数一律 SECURITY INVOKER。正文/hash、owner/proconfig、返回列序、传递调用均进入新 catalog。

| 分类 | 函数与返回 |
| --- | --- |
| public | `pp_create_account(text,text,text,text,text,bigint)`；`pp_configure_account(text,text,text,bigint,text,text,text,bigint)` → 原账户合同唯一 TABLE 返回，列序/可空联合不改 |
| public | `pp_record_attempt(text,text,integer,text,text,text,text,jsonb,boolean,bytea,bigint,bigint,bytea)` → TABLE(outcome text,root_id text,source_id text,attempt_id text,request_id text,sequence integer,delivery_state text,cost_status text) |
| private | `pp_ab_require_writer_v1()` → void；`pp_ab_require_root_v1()` → void；`pp_ab_account_mutate_v1(text,text,text,text,bigint,text,text,text,bigint)` → 与账户公开根同 TABLE |
| private | `pp_complete_attempt_birth(text,text,text)` → TABLE(root_id text,management_mode text,account_id text,reserved_event_id text,account_journal_id text,admitted_at timestamptz) |
| private | `pp_account_effect(text,text,text,text)` → TABLE(event_id text,journal_id text)；`pp_apply_supplier_pb(text,text,text)` → void |
| readonly leaf | `pp_ab_validate_request_leaf_v1(text)`、`pp_ab_validate_command_leaf_v1(text)`、`pp_ab_validate_attempt_leaf_v1(text)`、`pp_ab_validate_account_leaf_v1(text)` → void |
| readonly fixed interface | `pp_validate_root(text)`、`pp_validate_request(text)`、`pp_validate_attempt(text)`、`pp_validate_account(text)`、`pp_validate_original(text,text,text)` → void |

四只读 leaf 是新装配的固定普通依赖，不是公开业务根；APP 仅因 deferred 传递调用获 EXECUTE，与五固定只读接口同权限边界。它们只能 SELECT/纯计算/固定异常，不执行锁、DML、mutation、授权或发行凭证。普通 require 函数核 W/actual APP/RC/3s/5s 只是一组必要条件，不构成任意同 xid 对象的出生授权。

`pp_account_effect` 四参 canonical 语义为 `(root_id,attempt_id,source_kind,source_id)`，不增加外部 effect/delta/target 参数。本片只接受 source_kind=attempt_birth、attempt_id=source_id=该真实 attempt origin自然ID，消费同root预分配的唯一 reserved transition/event/journal；其余三种合法未来cause明确PP906，错证PP902。`pp_apply_supplier_pb` 亦仅消费本次真实 reserved PE，不授一般source/delta/target写面；支持将来接口的固定ABI不等于支持其全部状态机。

调用图固定为：command leaf 只核该 command/source/audit/journal 与其 root/step 的准确元组；attempt leaf 只核该 attempt 的出生图、原件与本条 reserved/PB 效果及 root/step；request leaf 核 request 出生/冻结快照/head0 与对应 birth root/step，**不调用 account/attempt leaf**。account leaf 枚举全链并调用 command/attempt leaf，逐 ordinal 重放配置/桶/blocker/p/PE/PB，核唯一 head 与原账户实值；不调用 request leaf、公开 validator 或自己。

五接口在上述叶子之上封闭组合：request 枚举该 request 所有受证 attempt，并对实际 managed 账户去重核 account leaf；attempt 核 request leaf、attempt leaf 和实际 managed account leaf；account 核 account leaf；root 按准确 entry_kind/scope 选择对应叶子组合。original 使用封闭 tag 的硬编码分支再调用上述接口，不被其他 validator/leaf 反调。禁止 leaf→公开 validator、相互递归或 validator→writer。root BEFORE seal 在 `NEW` 上直接核同一固定模板及来源/输出集合；不能调用要求 root 物理行已存在的只读接口再忽略失败，不能新增 caller `allow_unsealed` 参数。

`pp_validate_original` 本片支持 request/attempt/account/procurement/procurement_event/procurement_command/audit 七 tag，key2 必须 SQL NULL；已定义但尚未支持的域明确 PP906，损坏的受证边 PP902。tag 不变成 regclass、动态表名或拼接键。所有来源均按完整真实身份分类，不能把改错 discriminator 的受证对象降成“不支持”。

### 4.3 精确 root 模板与反向全集

| root entry_kind/scope | 准确来源与输出全集 |
| --- | --- |
| request_birth/request | 恰一个 RequestOrigin、一个 head0、一个 step1=request_birth、一个原 request 出生组；attempt/account canonical source、PE/journal/command/audit 输出均零 |
| account_create/account_command | 恰一个 command source、step1、原 command、准确 audit slot、create account/origin/head/journal ordinal1；tenant/request/attempt NULL |
| account_configure/account_command | 恰一个 command source、step1、原 command、audit；found 分支恰一个 configure 或 configure_rejected journal，not_found 无 journal/账户出生；不新增 origin |
| attempt_birth/request unmanaged | 恰一个 AttemptOrigin、原 attempt 出生组、head ordinal0/last NULL/member_count NULL/member_tail NULL、step1；无 p/PE/journal/transition/node/audit/outbox/command source |
| attempt_birth/request managed | 恰一个 AttemptOrigin、原 attempt、head ordinal1/member0、step1=attempt_birth、step2=procurement_transition、恰一个 reserved transition、node1、p、PE、account reserve journal；无其他来源/输出；PB control 存在时按真实 PE 镜像，缺席不造 allocation/event |

每个 request 恰一 **entry_kind=request_birth 且 attempt_id NULL** 的 birth root；每个受证 attempt 恰一 **entry_kind=attempt_birth 且 attempt_id 为该真实 ID** 的 root。逐行枚举 root/request/attempt/step/source 原反向集合，禁止额外或错误 entry_kind root，不能把旧全 request root count=1 改为 `>=1`。source 只能占一个准确 step，同 xid 其他 root 不能借用。root step_count 是真实全集数，最后 seal 后不能追加 source/输出。当前可变原件只与 head/最新前缀比对；不能要求旧 command journal.after 永远等今天 account。

## 5. trigger replacement inventory 与真实暂态

### 5.1 旧 trigger 不混装

以下 29 个 account v1 trigger 全部不在新候选创建：`aa_pp_ac_{root,step,source,origin,journal,head}_{write,truncate}` 共 12；`aa_pp_ac_account_write/aa_pp_ac_account_truncate/zz_pp_ac_account_result` 三个；`aa_pp_ac_command_write/aa_pp_ac_command_truncate/aa_pp_ac_audit_write/aa_pp_ac_audit_truncate` 四个；users 上 `aa_pp_ac_parent_lock_only` 一个；`zz_pp_ac_{root,step,source,origin,journal,head,account,command,audit}_complete` 九个。

以下 A1 16 个 trigger 亦不混装：`aa_pp_rb_{request,origin,head,root,step}_{write,truncate}` 十个，`zz_pp_rb_{request,origin,head,root,step}_complete` 五个，`zz_pp_rb_request_birth` 一个。替换是新候选的同源语义实现，不是更新旧 SQL 文件/pin 或重新定义旧 profile。

### 5.2 新函数与 68 个真实 trigger

新 trigger 函数精确为以下 15 个，均零参数返回 trigger：`pp_ab_request_write_v1/pp_ab_capture_request_v1/pp_ab_shared_write_v1/pp_ab_source_write_v1/pp_ab_attempt_write_v1/pp_ab_attempt_result_v1/pp_ab_account_write_v1/pp_ab_account_result_v1/pp_ab_procurement_write_v1/pp_ab_attempt_birth_p_v1/pp_ab_event_write_v1/pp_ab_command_write_v1/pp_ab_audit_write_v1/pp_ab_parent_write_v1/pp_ab_deferred_v1`。仅 capture 为受限 definer，其余 invoker。

下表每行的 suffix 生成三个准确 trigger：`aa_pp_ab_<suffix>_write` 为 BEFORE INSERT OR UPDATE OR DELETE/ROW，`aa_pp_ab_<suffix>_truncate` 为 BEFORE TRUNCATE/STATEMENT，`zz_pp_ab_<suffix>_complete` 为 AFTER INSERT OR UPDATE OR DELETE/ROW、DEFERRABLE INITIALLY DEFERRED。前二调用所列 guard，末者调用 `pp_ab_deferred_v1`。这里的模板是有限闭合集合，最终 SQL/catalog 必须展开逐名，不能给任意表自动装钩。

| 关系 | suffix | guard |
| --- | --- | --- |
| pp_operation_roots / pp_root_steps | root / step | pp_ab_shared_write_v1 |
| pp_request_origins / pp_request_heads | request_origin / request_head | pp_ab_source_write_v1 |
| pp_attempt_origins / pp_attempt_heads / pp_attempt_nodes | attempt_origin / attempt_head / attempt_node | pp_ab_source_write_v1 |
| pp_account_command_sources / pp_account_origins / pp_account_heads / pp_account_journal | command_source / account_origin / account_head / account_journal | pp_ab_source_write_v1 |
| pp_procurement_transitions | transition | pp_ab_source_write_v1 |
| request_records | request | pp_ab_request_write_v1 |
| upstream_attempts | attempt | pp_ab_attempt_write_v1 |
| procurement_accounts | account | pp_ab_account_write_v1 |
| procurement_reservations | procurement | pp_ab_procurement_write_v1 |
| procurement_budget_events | event | pp_ab_event_write_v1 |
| procurement_commands | command | pp_ab_command_write_v1 |
| audit_events | audit | pp_ab_audit_write_v1 |

上述 19×3=57，再加四个真实业务 hook：request 的 `zz_pp_ab_request_birth` AFTER INSERT/ROW→capture；attempt 的 `zz_pp_ab_attempt_result` BEFORE INSERT OR UPDATE/ROW→attempt_result；account 的 `zz_pp_ab_account_result` BEFORE INSERT OR UPDATE/ROW→account_result；p 的固定 `zz_pp_attempt_birth_p` BEFORE INSERT/ROW→attempt_birth_p。另在 users/tenants/memberships/models/channels/api_keys/reservations 七表各安装 `aa_pp_ab_parent_lock_only` BEFORE INSERT OR UPDATE OR DELETE/ROW→parent_write，共 68 个。parent_write 仅拒本片 W 的真实 INSERT/UPDATE/DELETE（含 no-op UPDATE），APP 的原合法管理、资金与保留 DELETE 分支继续交给原 guard，不被这只 hook 一律拒绝；该拒写接受集不自动用于未来有客户资金写权的其他候选。原 trigger 全部运行，绝不通过变更原 timing/function/owner/enabled 绕过。

catalog 必须核 p BEFORE INSERT 的真实名字顺序为 `aa_pp_ab_procurement_write` → `pb_procurement_stamp` → `procurement_reservation_identity` → **`zz_pp_attempt_birth_p`**。早期 guard 允许 W 的真实初生 p 且 pp pointers 尚未派生，不能提前要求 origin；原 stamp 写真实 `NEW.pb_admitted_at`，identity 核真实绑定，末序 hook 才完整出生 origin/transition并派生 NEW.pp pointers。hook 不预采、不覆盖原 PB 时间，不写 PE/账户/PB/journal/seal。新增任何更末序可改 NEW 的 hook 均须重新审查 catalog，不能凭 CREATE 顺序声明“最后”。

新 attempt 允许先真实 INSERT 取得 DEFAULT/020 stamp，随后 private closure；其早期 invoker 拒 APP/raw 身份、异常初态、caller 派生 PB/last/dispatch pointer。末序 attempt result 核 prepared/unknown/NULL、实际 pb stamp 与完整不可变出生组。仅凭 xmin/current xid、同事务有 source、trigger_depth、GUC、passed 或缺 p 均不能授权来源或判 unmanaged。

### 5.3 完整顺序、PB 暂态与唯一 seal

公开根完成原输入/LR/有序 checks/首次 DB quote 后才 INSERT attempt；冲突后的独立新 statement 仅比原四项幂等身份，匹配 replay 返回原五字段，root/source **SQL NULL**，无任何来源/p/PE/PB/root 补生。ignored expected BYTEA 只在原分支消费时精确 Equal，不能存进 source 或强转 TEXT。

新 managed 分支严格 account N→supplier namespace/control N→account head U，等待后新 statement 核实价/期限/U/账户gate；真实 p INSERT 的末序 hook 一次创建完整 origin、head0 和 reserved transition。p RETURNING 后，closure先按原 AdmitSupplier语义、真实p.pb_admitted_at执行supplier PB gate(U)/原window观察；随后新statement重读Key expiry，按Key expiry→客户financial deadline/request deadline原顺序复核同一admission time，再沿原checkDispatchCostQuote(at)核当时有效quote/policy。以上通过后，private effect依该真实source写PE、账户H、supplier PB镜像、准确account journal/head；closure完成steps、node与attempt head，全部闭合后 **private `pp_complete_attempt_birth` 唯一最后 seal**。公开根返回前只做读取、协议/最终只读校验，不再增加DML或第二次seal。unmanaged分支从channel屏障后真实binding缺席一次出生origin/head0/step1，保留首次quote，不重选价。

原 014 sums、020 PB 和 B-P1 完整运行。中间存在“新 p 已有而 PE/桶未齐”“PE/桶已齐而 supplier PB/journal/head 未齐”等明确同 root、同 attempt 的短暂阶段；早期 guard 只核当时应存在的精确部分，允许集合由真实 immutable 出生组、源/原件缺席与执行次序限定。不能先调用终态 validator 失败后忽略，也不能对所有同 xid 对象豁免。无缺边暂态可 seal/COMMIT；故障注入/提前 SET CONSTRAINTS/原 deferred 均须整笔回滚。

账户配置与 reserve 必须在真实链中交错：create→reserve→configure→reserve，各 journal ordinal 连续、before=真实旧 head、after=真实效果、configuration_revision 仅配置变化增长。reserve before p 全 NULL、after=(revision1,prepared,U,0,false)，transition delta=(U,0)、event_key=reserve:attempt、evidence_ref=''；U=0 仍发行真实 PE/journal/PB event（若有 control）。configure noop/拒绝保留不变 H/S，不把 reserved journal 当 PP906。若遇未来非 reserve 合法 journal，先核必要身份后 PP906，不把未实现状态机成功返回。

gate 采时落位：closure 在 p INSERT 前仍按原顺序完成实际 account gate；末序 p hook 在原 PB stamp 后，以已锁 A/head 和相同 U 再核相同 gate，并在该独立 statement 采真实 clock 写入 reserved transition.created_at。journal.gate_checked_at 必须逐值等该不可变 transition.created_at，pb_decision_at 等 origin/p 的原 pb_admitted_at；这两个时点不互相冒充，不追加 caller timestamp 参数、GUC 或 pending source。hook 的这次重核仍在 p 物理行出生前，原先 gate/error 优先级不移动；source 时间不是从最终账户余额倒造。effect 仅消费这些真实来源值。

## 6. 新 ACL 差异与每项显式锁用授权

本节是对[全域 ACL](b-procurement-full-acl-contract.md)的 **A2 切片及已审定 lock-only 差异**；上位正文已记录本片适用范围；本片已有独立材料和限定 PG 验证，尚未安装为常规 serving profile。W NOLOGIN/NOINHERIT，无 owner/schema CREATE、membership、SET ROLE 路径；actual session_user 必须绑定 APP LOGIN OID，owner＋SET ROLE APP 不替代。三个根 EXECUTE 只给 APP；private mutation 不给 APP/PUBLIC/peer，trigger 自动执行不要求直接 trigger EXECUTE。只读 leaf/五接口的 APP 显式调用面与其普通依赖、准确 SELECT 必须闭合。

### 6.1 真实业务 DML 的准确 A2 子集

W 的新表 INSERT 为 §2.1 全部非 generated 列；head UPDATE 只给 attempt_heads 的 ordinal,last_node_id,member_count,member_tail_id,updated_xid，以及 account_heads 的 ordinal,journal_id,updated_xid 和完整 Config/Buckets 八列。request head 不推进。全部关系无 DELETE/TRUNCATE。

W 原表 INSERT 取完整 ACL §3 中 A/ p/PE/command/attempt/audit 六行准确列；request 无 INSERT，只有三列 origin pointer 的一次内部 UPDATE。A UPDATE 为 label,provider,state,limit_amount,held_amount,spent_amount；A2 的原 p/attempt/PE/command/audit 均无真实 UPDATE。p 的 pp 三 pointer 由末序 BEFORE 派生，W INSERT列沿上位固定矩阵保留，不能外传 caller 值。没有 api_keys/客户资金/case/recovery/outbox 的真实 DML，也不给未来 dispatch/成本 pointer 写权；API Key/客户 reservation 的单列锁用例外及拒写guard准确见下表，绝不借 held/spent/status 等客户资金业务列满足锁权。

APP 保留 A1 的 request 21 列 INSERT、status/billing_status 窄 UPDATE和完整原 Reserve/普通资金/Commander/绑定/余额观察路径；撤 APP attempt/p/PE/账户来源 DML，账户只保留 created_at 锁权且实际 UPDATE早拒；command 只保留 credential_bind/balance_observe 九列 INSERT且全部 pointer NULL，普通 audit 保留原非保护列。来源/状态保护谓词不能用 G 全 NULL 逃逸。原客户资金接受集、默认未安装 profile 行为和原付款共享 guard 不变。

W SELECT 仅本片实际读取的全域 ACL 子集：十二新表准确普通列；request/reservations/attempt/A/p/PE/command/audit 的 ALL_FIXED；原 principal/key/model/channel/route/quote/binding 的逐列集合；A1 pending/finish 检查需要的原 settlement/case 等准确列；PB/B-P1 实际闭包。不得复制未来 finalization/member/correction/case mutation 读写权或 secret。具体原表列 registry、函数调用和 expected ACL 必须与 SQL 一起固定，不能用表级 SELECT/ALL TABLES/live 列发现兜底。

### 6.2 每项 lock-only UPDATE 的实际语句

下表是安装材料必须真正出现的有序 SQL 边；不是“PG native FK 可能锁”推导的 grant。`$key`/`$root` 表示固定函数的局部实际 locator，不是动态 SQL。每次 `PERFORM ... FOR SHARE` 后另起独立 statement 重读；validator 自身零锁。W 的真实 no-op UPDATE 由对应 invoker guard 在任何赋值前拒绝，专项验收撤该 grant应使真实路径42501并全回滚。

| 调用边 / 显式 SHARE 目标语句 | W提供锁能力的列权 | 限制 |
| --- | --- | --- |
| request origin BEFORE：request_records WHERE id=$key FOR SHARE | 已有 UPDATE(pp_origin_root_id,pp_origin_root_step,pp_origin_xid) | 不额外加 request 身份列权 |
| request pointer UPDATE、request head/step BEFORE与 request seal：pp_request_origins WHERE request_id=$key FOR SHARE | UPDATE(request_id) | 同源保留 A1 精确 payload/原值拒写 |
| request pointer UPDATE / request seal：pp_request_heads WHERE request_id=$key FOR SHARE | UPDATE(request_id) | head0 不准实际 UPDATE |
| request seal / attempt node BEFORE：pp_root_steps WHERE root_id=$root [AND step_no=$step] FOR SHARE | UPDATE(root_id) | 仅锁，step任何实际UPDATE拒绝 |
| attempt origin/managed hook：upstream_attempts WHERE id=$key FOR SHARE | UPDATE(delivery_state) | A2 无 delivery 更新状态机，实际UPDATE一律拒 |
| attempt head、transition、step/node BEFORE与 seal：pp_attempt_origins WHERE attempt_id=$key FOR SHARE | UPDATE(attempt_id) | immutable，拒原值赋回 |
| account origin/head/command/reserve seal：pp_account_origins WHERE account_id=$key FOR SHARE | UPDATE(account_id) | immutable，拒原值赋回 |
| command/audit BEFORE与 account origin/journal：pp_account_command_sources WHERE id=$key FOR SHARE | UPDATE(id) | immutable，拒原值赋回 |
| account head INSERT/UPDATE、journal predecessor与 seal：pp_account_journal WHERE id=$key FOR SHARE | UPDATE(id) | immutable，不允许先改before再改回 |
| reserved step/node/journal BEFORE与 seal：pp_procurement_transitions WHERE id=$key FOR SHARE | UPDATE(id) | immutable，拒原值赋回 |
| attempt head 单步 UPDATE / managed seal：pp_attempt_nodes WHERE id=$key FOR SHARE | UPDATE(id) | immutable，拒原值赋回 |
| reserve journal BEFORE与 managed seal：procurement_reservations WHERE attempt_id=$key FOR SHARE | UPDATE(pp_last_journal_id) | 本片不推进p，真正UPDATE拒绝 |
| reserve journal BEFORE与 managed seal：procurement_budget_events WHERE id=$key FOR SHARE | UPDATE(id) | 新早期event guard＋原 immutable guard 都保留 |
| command seal：procurement_commands WHERE operation_id=$key FOR SHARE | UPDATE(operation_id) | 新早期command guard＋原 immutable guard拒真正UPDATE |
| command seal：audit_events WHERE id=$key FOR SHARE | UPDATE(id) | 新audit invoker guard拒W任何真正UPDATE |
| 原 LR：api_keys WHERE tenant_id=$tenant AND id=$key FOR UPDATE | UPDATE(created_at) | 只取锁；七表parent guard中的Key分支拒W真正I/U/D，不授held/spent等客户资金列 |
| 原 LR：reservations WHERE tenant_id=$tenant AND request_id=$request FOR UPDATE | UPDATE(created_at) | 只取锁；七表parent guard中的reservation分支拒W真正I/U/D，不授status等资金列 |

这些额外权中 A1 三项是 origin/head/step 的原受证锁用面，A2 新增其余十三项；request 三 pointer 是真实业务 UPDATE而非额外锁权。**不授 roots(id) UPDATE**：本片所有 source/step 引用 root 时它尚未出生，seal后绝不追加；不存在实际 root FOR SHARE 语句。attempt/account head 的实际业务 UPDATE列已经提供相应行锁能力，不追加 PK锁列。binding 的 native FK隐含KEY SHARE、readonly/deferred SELECT、单纯创建 table/函数也不构成新增 UPDATE理由。

其余真实父行锁逐一为：account command 的 users WHERE id=$actor FOR SHARE；原 LR 的 tenants WHERE id=$tenant FOR SHARE、memberships WHERE tenant_id=$tenant AND user_id=$owner FOR SHARE、models WHERE id=$model FOR SHARE、channels WHERE id=$channel FOR SHARE。W分别取得 users/tenants/models/channels 的 UPDATE(created_at)与memberships的UPDATE(tenant_id)，由七表parent invoker guard拒W真正I/U/D，原APP对应管理分支不变。没有本片显式 channel_credentials FOR SHARE 语句，故不授其 UPDATE(created_at)，也不安装第八只冗余父锁hook；credential native FK的KEY SHARE仍正常运行。account N 与 head U 复用 A真实配置/桶及 head 的业务UPDATE列。

### 6.3 PB、系统列与普通函数闭包

managed p RETURNING后、gate前保留原readSupplier对attempt/account/request/tenant/credential/channel/binding七ID的pb_uuid接受集及真实current tuple/初态证明；即使无control也不省略，unmanaged不施加此额外UUID限制。supplier PB SQL必须逐行为等价实现原[tx_supplier](../../internal/periodicbudget/tx_supplier.go)，不能从DB根返回后再交Go补镜像。W普通PB闭包取 PB合同的13项准确签名；allocation仅INSERT id/control_id/subject_type/subject_id/origin/origin_source_id/window_id 七列，金额/基线由原 guard；control/window/allocation/event/B-P1 journal/head的精确 UPDATE/INSERT列沿PB矩阵，不授配置版本/控制创建/operations SELECT。APP Commander后配/import与原客户PB独立保留。

A2 W实际 xmin 读面为 upstream_attempts、procurement_reservations、procurement_budget_events、periodic_budget_observation_journal 四表；request birth capture与本片attempt不调用readCustomer，因此不因未来dispatch/finish给W request_records.xmin。APP原Reserve/Commander/B-P1的五表sidecar仍保留。精确列级普通SELECT不能隐含xmin；PUBLIC/继承/旧表级SELECT旁路要由新profile收口并验effective权限。若正文新增实际xmin查询，先修订矩阵，不临时兜底表级SELECT。

保留原 `valid_procurement_command_snapshot(text,jsonb)`→`valid_channel_batch_revision(text)` CHECK调用闭包，以及实际使用的identity纯函数/内部json helper。仅实际消费的普通 EXECUTE算必要；原PB PUBLIC收口与APP补授按PB合同，自动trigger直接EXECUTE不作为必要grant。两项allocation读权若在真实路径未被使用必须作为非必要正控制，不能把重复撤grant仍成功算失败或新增多余权。

实现前补齐原 IP 检查依赖：`pp_record_attempt` 在原 Key 查询后的独立 PL/pgSQL 阶段逐项重核原 `ClientIPAllowed` 的长度/32项/4096字节与 canonical CIDR，不只检查某一个匹配项；复用原010的 `canonical_key_client_cidr(text)` 取得与 Go net/netip 一致的 IPv6 字符形式，W 必须有该准确普通 EXECUTE。比较request来源时沿原host/IPv4-mapped Unmap语义；不授Key配置写权、不新增纯函数或修改010。此新增闭包须进入新permissions/catalog与实际失权差分，不依靠当前PUBLIC重叠默认为可用。

## 7. 单 owner 文件顺序与可并行的真实边界

根审定本合同与 §6 差异后，新增独立包建议固定为 `internal/procurementattemptbirth`；不修改旧包文件。renderer仅显式渲染固定字节，不连接DB或自动provision/启动。共13份SQL与两份静态JSON，15项固定摘要；完整SQL与Permissions须在同一安装事务执行，COMMIT前独立核实际catalog。Bindings沿Schema/App/Writer/AppOID/WriterOID，安全标识与一次占位替换、避开插入名称的dollar delimiter沿已验A1；Digests逐材料固定预像SHA-256。函数未全创建时不提交半候选、不发布成功stub。

| 最终顺序 | 新文件/owner | 输入与交付 |
| --- | --- | --- |
| 0 | `materials/preflight-v1.sql`；装配owner | DML前拒旧封闭函数/已存在候选表，核identity/A0/B-P1必要前驱，不DROP或自动升级 |
| 1 | `materials/a2-tables-v1.sql`、`a2-original-columns-v1.sql`；关系owner | A1同源71列＋另八表191列的准确DDL、七原表38扩列；不复制旧完整32表 |
| 2 | `materials/a2-constraints-v1.sql`；关系owner独占 | §3全部真实key/CHECK/native+locator FK及59边展开registry；无宽UK；与表列descriptor一起静态验 |
| 3 | `materials/account-pure-v1.sql`、`authority-v1.sql`；函数owner | 五个旧纯函数准确同源提取及两个新require普通函数；identity/A0由原独立材料在先安装 |
| 4 | `materials/validators-v1.sql`；validator owner | 四leaf＋五固定接口、完整reverse集合及command/reserve前缀；零DML/锁/递归 |
| 5 | `materials/guards-v1.sql`；guard owner | §5十五trigger函数中的十四个invoker正文及准确暂态/锁后重读；不在此安装triggers，不写success stub |
| 6 | `materials/account-writer-v1.sql`、`attempt-writer-v1.sql`；writer owner各自独占 | account mutate＋两公开根；三固定private helper＋公开attempt根；完整managed/unmanaged/PB/唯一seal |
| 7 | `materials/request-capture-v1.sql`、`triggers-v1.sql`；装配owner独占 | 新受限request capture同源移植；全部68真实hook，明确原hook顺序；此时所有引用普通函数已存在 |
| 8 | `materials/permissions-v1.sql`、`materials.go`、`catalog-v1.json`；装配owner独占 | 新角色owner/列ACL/lock-only/EXECUTE/sidecar/固定expected，安装事务末核完整catalog再COMMIT |
| 9 | `*_test.go` 与独立 actual APP集成测试；测试owner | renderer/摘要/静态inventory；专库真实组合、攻击/撤权/等待/失败；由root串行执行PG |

require两个函数在第3步先定义，validator叶子只依赖表与pure函数；guard只依赖这些已存在ABI。固定安装脚本在首次业务调用前逐签名解析全部引用，禁止通过PL/pgSQL允许延迟解析掩盖循环或缺失；任何未定义依赖失败回滚。

能并行的是**不同最终文件**：关系DDL/descriptor、validator正文、guard正文、account writer正文、attempt writer正文、测试设计。共享ABI/表列/槽先按本页冻结。`materials.go`、triggers、permissions、catalog和最终fixture只由装配owner收口，避免多个agent改一文件；validator/guard均只依赖固定表与叶子ABI，不以另一个writer的运行结果生成源码；account writer不调用attempt writer，attempt closure借同一真实account来源前缀即可，二者无循环。所有输出完整后才一次装配实际运行，不能以unmanaged-only或command-only通过冒充完整候选。

原A1所有raw/等价SQL、冻结字段、pending先reservation后request后case、资金失败回滚、legacy不补证、角色/锁/撤权/并发历史均须作为新候选差分回归。旧A1/account v1仍独立跑原验收，旧文件/hash不改；新候选的expected和证据独立，不重新解释旧PASS范围。

## 8. 退出证据与故障边界

实际PG由集成人在专用PG16库串行执行、actual APP LOGIN、W NOLOGIN。owner只安装/准备管理父数据、观察/注入故障；被测request/account/attempt必须由真实入口生成，不能用owner伪造origin作正例。至少覆盖：

- 原A1完整矩阵；同事务request birth＋managed/unmanaged attempt；跨root与跨租户共享账户create→reserve→configure→reserve；command replay/not_found/revision_conflict/noop；不是只跑已建空账户或未管理attempt。
- 所有原checks顺序、RouteRevision0与发布版本、Sequence1/2、无消费expected任意bytes、无quote真实采时、managed等待后stale与unmanaged跨计划生效不重价、原四字段幂等及replay NULL root/source；原AtInput/Attempt与默认billing.New回归。
- 无PB/既有supplierPB/未配置namespace/Commander首配竞争、零U、多租户并发账户硬限额、supplier新窗口与B-P1、后配置import；实际allocation/PE/PB/admitted_at与独立手算一致，不用writer生成expected。
- 每个source/原件/PE/桶/PB/journal/head/node/step/seal/APP COMMIT边界真实故障；提前SET CONSTRAINTS、锁55P03/取消57014、statement/savepoint失败均全回滚且保留旧已提交根。失败快照包含原request/attempt/p/A/PE/command/audit、客户资金/PB、supplierPB/B-P1与全部新表，不只count(root)。
- APP raw INSERT/UPDATE/TRUNCATE、owner+SET ROLE、peer/private调用、部分pointer/假legacy、同xid跨root、预造输出、缺/错/多来源、source占两step、重复ordinal/漏tail、seal后追加、提前/二次seal；每项lock-only真实no-op UPDATE拒绝。
- 最小grant逐项实际撤还且effective true→false→true；必要失权有真实42501/回滚，继承/PUBLIC/owner/自动trigger假失权作独立正控制；APP外层deferred实际权限，准确系统列、函数owner/proconfig/trigger enabled/defaultACL/temp/search_path均验。

保留RC、3秒lock/5秒statement与原上层绝对期限；只对原允许且已确定回滚的40001/40P01最多三次，55P03/57014不扩大重试。未知COMMIT只读核准确operation/root，不重发模型请求、不换operation盲做财务；本片不生成模型调用。每次实现变更先更新受影响合同/架构，所有并行结果汇合后由集成人语义复核、更新统一架构/验证记录，再执行带理由的architecture refresh与只读检查。

本轮材料、真实组合与catalog的限定结果见[新A2验证](../../docs/verification/procurement-attempt-birth.md)。该记录逐项列出未完成的本节矩阵；当前PASS不关闭完整A1差分、逐grant失权/误授权攻击、未知COMMIT、A5 executor或全域profile。

## 9. 验收隔离、固定依赖与必要权限判定

隔离 schema 不能隔离同一创建者的 global default ACL。本候选安装事务按 §6 收紧 O 的全局函数 PUBLIC EXECUTE，fixture 在清理时恢复原值；同一测试库、同一 O 的其他包不得在该生命周期中并行创建函数。`make test`、`make verify-mvp` 与 CI 的专库包执行统一使用 `go test -p 1`。完整包/分片清单、race、包内显式业务并发、原 3s lock / 5s statement 及 10m package timeout 保持；不通过给旧函数临时补 PUBLIC/APP 权限掩盖并发污染。

test-only catalog inspector 的期望继续仅来自固定原 001–022、B-P1、identity/A0 与候选材料：结构查询覆盖权限材料触及的全部 50 张关系，不能只核 12 新表和 7 扩列表而漏掉父表 RLS、owner 或约束。原件表 owner 固定为 O，38 个候选函数 owner 固定为 W，其余固定依赖 owner 为 O；不能根据 live owner 自动豁免新获得所有权的 peer。

固定 reference 的全部函数签名、安全属性和正文均需核对；schema-local composite 参数/结果比较限定类型名，不能比较两个 schema 各自分配的 rowtype OID。38 个候选正文仍按实际绑定的认证 renderer 逐字节比较；原函数只改写独立 reference 的 namespace 得到期望，target 正文不归一化。all-grantee 原始 ACL 检查针对上述 50 张关系及 permissions 的固定函数签名集，包含 APP/W 已有权限不能显露的 PUBLIC/peer 旁路；它不扩称全数据库生产权限认证。

必要 grant 由实际路径消费证明，静态调用图和有效撤权本身都不足。每项先证明 effective true→false，再执行真实公开根及外层 APP deferred，最后精确恢复 canonical ACL 并重新执行；原 owner、PUBLIC、继承和 automatic trigger 直接 EXECUTE 必须分别识别。未被当前路径消费的保守 grant 保留为有效撤权成功正控制，不删业务权限，也不添加人为读取或额外函数调用制造必要性。仅为行锁保留的原值 UPDATE 必须拒绝：W 经已授普通 helper 返回 PP901；APP 的 account.created_at 更新可先被私有普通 helper 的 EXECUTE 边界以准确 42501 拒绝，不为得到 PP901 额外扩权。

漂移攻击按独立 fixture 分组，逐项在同一事务执行变更→语义拒绝→回滚→完整正常核验，并以 actual APP 业务作组末正控制。SQL 错误、取消、reference 重建错误和观察超时不得冒充语义拒绝。分组只划分整套实验的生命周期，不放宽单次业务根的执行期限；这些检查仍不发行 Qualified、生产 Check 或 serving profile。

110 项 catalog 攻击保持原 13 类语义判定，验收调度拆为名称连续的四个顶层 `TestPostgresA2CatalogRejectsTransactionalDriftA/B/C/D`，供独立 runner/专库按完整顶层名单排序轮转。metadata 的 30 项与 ACL 的 19 项分别按固定攻击名称分成 15/15 与 10/9 两份，其余 11 类各保留独立 fixture，共 15 个 fixture；两个大类的两份分散在 A/B。每份仍使用原 180s fixture 上限、原事务/SQL/锁期限、逐项变更/拒绝/回滚/完整复核和末尾 actual APP 成功正控。静态库存必须核对固定 110 名称在四顶层/十五份中的互斥、穷尽与非空，CI 同时核对完整包及全部顶层名单，不使用攻击排除列表，也不改变正常应用或生产权限。分组用于避免多个独立实验累积触发 fixture 或单进程 10m 上限，不扩称远端时延保证或实际验收已经通过。

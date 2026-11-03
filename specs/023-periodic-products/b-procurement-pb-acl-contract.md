# B-P4-D1-PB：writer 的 PB 与延迟约束最小权限合同

2026-11-03。承接[共同来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)和[权限与事务](b-procurement-writer-authority-contract.md)合同。本页选择候选 writer 的 PB SQL 形状、关系/列与普通函数闭包，列出 Reconcile 客户 finish 和 APP COMMIT 的必要接缝。依据为原 [020](../../migrations/020_periodic_budgets.sql)、[014](../../migrations/014_procurement_accounts.sql)、[017](../../migrations/017_funding_vouchers.sql)、[021](../../migrations/021_payment_sandbox.sql)与 [B-P1 candidate](b-prefix-candidate.sql) 的静态调用图。

**状态：候选权限合同；未交付权限 DDL、完整新 catalog/profile 或实际 LOGIN 验收，不表示 D1 整体完成或 I1 已实施。** 原迁移、旧 B-P1 材料、旧 profile、默认业务构造器/provision/main 未由本页修改。改变 ACL 的新候选必须由新的精确 profile 审定，不能声称旧 profile 接受改变后的权限。B-P3 历史缺证、catalog race 时限与 R2 门禁保持。

## 1. 角色与范围

writer 为独立 NOLOGIN/NOINHERIT、非表/schema owner 的受限角色。APP、worker、projector、peer 不得通过 SET/INHERIT/ADMIN 或函数 owner 间接取得该身份。新根绑定真实 session_user 的 APP 角色 OID；业务 actor 的状态和权限按原操作另核。schema USAGE 只授精确受信业务 schema；不授 CREATE、BYPASSRLS、系统预置读写角色或修改 trigger/函数的权限。

所有新函数固定 `search_path = <精确受信业务schema>, pg_catalog, pg_temp`，关系与私有调用使用安装时固定 schema。业务 schema 在第一位，以保持原 pb_scope_sync 的 current_schema() 锁域；不接受 caller GUC、任意 schema/scope/effect SQL 或自报已持锁凭据。普通 PB 函数保留原 invoker 行为，不能为解决缺权限改为 owner 执行。

表 ACL 不能按 tenant/key/supplier 行过滤同一 writer。合法闭合根会触及三个 PB scope；隔离来自不可获得的 NOLOGIN 身份、精确 APP 入口、固定 SQL、原件身份与 typed source 联合，不对外暴露可传任意 scope 的通用 effect。以下均审查有效权限，包括 PUBLIC、可达角色、表级/列级及默认 ACL；仅撤列级 grant 而留下表级 UPDATE 不构成限制。

## 2. allocation 固定选择七列 INSERT

候选私有 helper 固定使用下列列集，不沿用当前 Go helper 的十列零占位 SQL：

```sql
INSERT INTO <business>.periodic_budget_allocations
  (id,control_id,subject_type,subject_id,origin,origin_source_id,window_id)
VALUES (...)
RETURNING ...;
```

business 是安装时 schema 标识符占位，不是业务参数。真实物理列名为 upper_amount、baseline、source_spent；三项全部省略，不改名为 upper_limit/baseline_spent，也不授 INSERT 这三项。完整 RETURNING 与逐值比对随实际 helper 正文冻结，不能用 caller 期望值替代实际结果。

静态可行性依据是原 pb_guard_allocation() BEFORE INSERT：

1. customer 成功分支总是赋 upper_amount=v.amount、baseline=0；supplier 总是赋 upper_amount=p.upper_amount、baseline=0，legacy_import/catchup 再分别赋当前或先前真实 spent 基线。
2. 公共尾部总是赋 source_spent=baseline，并覆盖 admitted/window 规则、created stamp、source_held、H/S、state、revision 和 last_event_id。三个省略值均未在 INSERT 成功分支赋值前被读取。
3. PostgreSQL BEFORE INSERT 行处理先于该行的 NOT NULL/CHECK 检查；三列即使原表 NOT NULL 且无 DEFAULT，也可由 BEFORE 填入。UPDATE 分支仍比较旧身份/基线，不受此 INSERT 列集影响。
4. 原 021 和 B-P1 没有另一个 allocation BEFORE INSERT 消费这些占位值。customer catchup 仍拒绝，七列形状不扩张 origin 接受集。

这仅是源码语义结论，**尚无真实受限 LOGIN 的七列运行证据**。后续须用实际七列 SQL 验证允许 origin 与失败回滚；不能用七列 grant 执行仍含十列的旧 SQL，不能用表级 INSERT 兜底，也不能修改原020制造默认值。

## 3. writer 的 PB 表/列矩阵

下列 SELECT 按精确关系名授予，覆盖完整当前列和相应 xmin 读取。原 trigger 大量 SELECT * INTO %ROWTYPE，不能因只使用几个字段省列；新增列进入新 catalog/readset 后重审，禁止 SELECT ON ALL TABLES。

| 关系 | SELECT | INSERT 精确列 | UPDATE 精确列 |
| --- | --- | --- | --- |
| periodic_budget_controls | 完整当前行 | 无 | held_amount,spent_amount,event_seq,last_event_id,max_observed_window_start |
| periodic_budget_windows | 完整当前行 | id,control_id,start_at,end_at | held_amount,spent_amount,revision,last_event_id |
| periodic_budget_allocations | 完整当前行 | id,control_id,subject_type,subject_id,origin,origin_source_id,window_id | held_amount,spent_amount,source_held,source_spent,state,revision,last_event_id |
| periodic_budget_events | 完整当前行 | id,control_id,allocation_id,source_kind,source_id | 无 |
| periodic_budget_configuration_versions | 完整当前行 | 无 | 无 |
| periodic_budget_operations | **无** | 无 | 无 |

六表不授 DELETE/TRUNCATE/REFERENCES/TRIGGER、grant option 或 owner。controls 不授整体 INSERT，也不授 enabled/limit_amount/period_kind/revision/configuration_operation_id 或身份/出生/日历列 UPDATE。highwater 是 pb_observe 所需的受 guard 限制观察投影，不是配置。BEFORE 自动计算 NEW.revision 不要求 caller 获得 UPDATE(revision)。

windows 不授 observation_id/observation_created_xid 写权；allocations 不授身份/来源/原窗/admitted_at/upper/baseline/created stamp UPDATE；events 的 seq/before/after/delta/stamp 由原 guard 推导。实际 SQL 必须逐列符合矩阵，不能因 trigger 可拒绝部分伪造值扩大 grant。

configuration_versions SELECT 不能遗漏：每次 control UPDATE，包括仅 highwater 或桶变化，都会排队 pb_configuration_receipt → pb_check_configuration()，后者读取完整当前 control/version。候选同时给 writer 与 APP 此只读能力，使根内合法约束检查和外层 COMMIT 均有闭包，不新增配置写面。

当前 reserve/effect/dispatch/customer finish 不读 operations。B-P1 window source check 进入 window 分支，window_birth journal 的 command discriminator 为 NULL。FK 由 PostgreSQL RI 机制维护，不能据 FK 存在推导 caller 必须 SELECT operations。未来根若实际新增读取，先更新合同和新 profile。

## 4. B-P1 observation 三表

| 关系 | SELECT | INSERT | UPDATE |
| --- | --- | --- | --- |
| periodic_budget_observation_origins | 完整当前行 | 无 | 无 |
| periodic_budget_observation_heads | 完整当前行 | 无 | ordinal,last_observation_id |
| periodic_budget_observation_journal | 完整当前行，含 generated 列及相应 xmin | 下列十四列 | 无 |

```text
id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,
configuration_revision,configuration_operation_id,window_id,window_revision,
window_last_event_id,observed_at
```

三表均无 DELETE/TRUNCATE；head.updated_xid 和 journal.stamp/generated 不授写。新 window 自动进入 pb_prefix_window_capture → pb_prefix_append，追加 window_birth 并推进已有 head；window UPDATE 只核观察身份不变。writer 不创建 control，因而不授 origins/head INSERT，不发行配置 command observation。旧无 origin 的 control 保持原 legacy 行为，不补造历史。

原 [B-P1 permissions](b-prefix-permissions.sql) 给 APP 的 origins/head INSERT 与多项 trigger EXECUTE 服务于原 PB 配置入口，不能整份复制给 writer。复合类型 USAGE、函数参数/返回类型的安装检查与已创建 trigger 内 %ROWTYPE 的运行需求分开；不能据 %ROWTYPE 推断额外表写权。新函数若在签名使用业务 composite type，应逐类型审定 USAGE；精确新类型 ACL 和实际运行最小集仍待可执行材料验证。

## 5. 普通函数 EXECUTE 与自动 trigger

writer 必要普通函数签名固定为十三项：

```text
pb_now()
pb_uuid(text)
pb_start(text,timestamptz)
pb_end(text,timestamptz)
pb_current_tuple(xid8,xid)
pb_require_read_committed()
pb_scope_sync(text,text,boolean)
pb_sync_unconfigured(text,text)
pb_observe(text,timestamptz)
pb_gate(text,timestamptz,bigint)
pb_check_customer(text)
pb_check_supplier(text)
pb_prefix_append(text,text,text,bigint,text,bigint,text,text,bigint,text,timestamptz)
```

pb_uuid 来自 CHECK 等实际表达式；pb_check_customer/supplier 在 dispatch guard 内已有显式调用；private append 在 window trigger 内显式调用。不能因 caller 不直接调用漏授。schema USAGE 同时必要。内建 UUID、日期、JSON、事务与锁函数的既有有效依赖须登记，不授 EXECUTE ON ALL FUNCTIONS、pg_read_all_data 或 superuser。pb_current_tuple 读取 pg_locks 并使用当前事务系统函数，不需要系统关系写权限。

既有 trigger 自动运行不要求每次触发时再给 DML 角色 GRANT RETURNS trigger 函数 EXECUTE；CREATE TRIGGER 安装权限与运行时 invoker 函数体权限是两件事。自动函数体中的关系访问与显式普通函数调用仍以当时有效角色检查。撤 trigger EXECUTE 也不能阻止具有错误 DML 权限的角色利用其自动副作用。

故新 writer 不因自动触发直接获得 pb_stamp_*、pb_guard_*、pb_apply_event、pb_check_source_trigger、pb_check_configuration 或 B-P1 trigger 函数 EXECUTE；这些函数的正文、owner、安全模式、proconfig、依赖、触发事件/时点/字母序仍全部属于新精确 catalog。pb_guard_configuration_pointer/pb_check_configuration 会在合法 control 投影 UPDATE 运行，不能列成从不触发。配置拒绝必须从表/列 DML 关闭。

### 5.1 关闭原020 PUBLIC EXECUTE

原020末尾只固定 search_path，未撤默认 PUBLIC EXECUTE。候选选择显式撤 PUBLIC 对以下**精确三十四个原函数签名**的 EXECUTE，再按本节和 APP 实际调用精确补授；只 REVOKE FROM writer 不会移除 PUBLIC 有效权限。

```text
pb_now(), pb_uuid(text), pb_start(text,timestamptz), pb_end(text,timestamptz),
pb_current_tuple(xid8,xid), pb_require_read_committed(),
pb_scope_sync(text,text,boolean), pb_sync_unconfigured(text,text),
pb_stamp_request(), pb_stamp_attempt(), pb_stamp_procurement(), pb_stamp_procurement_event(),
pb_guard_control(), pb_guard_window(), pb_guard_allocation(), pb_guard_event(), pb_apply_event(),
pb_check_customer(text), pb_check_supplier(text), pb_check_source_trigger(),
pb_guard_customer_reservation(), pb_check_initial_import(),
pb_observe(text,timestamptz), pb_gate(text,timestamptz,bigint), pb_guard_dispatch(),
pb_decimal(text,boolean), pb_frame(text[]), pb_safe_input(jsonb), pb_view_at(text,timestamptz),
pb_guard_operation(), pb_guard_configuration_pointer(), pb_guard_version(),
pb_record_operation(), pb_check_configuration()
```

APP 保留原 PB 客户资金、Commander、Reader 所需普通函数：上文前十二个原020签名，加 pb_decimal(text,boolean)、pb_frame(text[])、pb_safe_input(jsonb)、pb_view_at(text,timestamptz)，以及 B-P1 pb_prefix_append(...)。safe_input → decimal、operation guard 的 frame/view 和 Reader 的 view 不能漏掉。最小运行不要求新增 APP 对 RETURNS trigger 的直接 EXECUTE；旧 B-P1 已有 APP 显式 trigger ACL 若在新候选收窄，须由新 profile 记录验证，不视作原 profile 不变。

B-P1 函数本已撤 PUBLIC；新 pp 根/helper 也须撤 PUBLIC，仅准确 APP 取得封闭根 EXECUTE，writer 取得私有闭包。pricing_time(timestamptz) 属原018，APP 的 PB view/Reader 实际依赖它；本页不顺带改其 PUBLIC 基线，也不谎称 writer 所有有效 EXECUTE 只有十三项。若后续收口该函数，须连同其他原调用方另列准确 grant 与新 catalog。原021对 payment_*、funding_journal_valid/funding_fact_guard 的 PUBLIC 收口保持，不为采购角色重开。

## 6. 来源只读与三 scope 行为

PB invoker 的只读闭包含下列原件；完整根的授权、报价、价格与 typed source 查询另外逐列审定，不能把本表视为完整 writer ACL：

| 关系 | PB/原触发器所需读面 |
| --- | --- |
| request_records / reservations / settlements | 完整当前行；request 含相应 xmin |
| procurement_reservations / procurement_budget_events | 完整当前行及相应 xmin，包括原014守恒需要的 PE 集合 |
| upstream_attempts | PB stamp/原身份至少 id,pb_created_xid,credential_id,channel_id 和 xmin；闭合根另需准确 tenant/request/quote/delivery 等列 |
| credential_procurement_bindings | 至少 id,credential_id,channel_id,account_id；身份合同另需历史字段单列，不因此读凭据 secret |
| procurement_accounts | PB stamp/原014至少 id,held_amount,spent_amount；账户根另需完整配置 |
| payment_sandbox_environment | 仅 singleton,worker_role，见§8 |

SELECT FOR UPDATE/NO KEY UPDATE 还需表上至少一个 UPDATE 列权限。writer 的合法 PB 投影/head/账户桶列满足相应锁权，不能为锁授全部 UPDATE。原 APP Commander/绑定需共享账户锁时，沿[账户合同](b-procurement-account-contract.md)的 created_at 单列锁权与拒绝实际 UPDATE 的候选 guard；[隔离实验](../../docs/verification/procurement-account-lock-acl.md)只证明锁原语，不证明真实 writer 分支、原014组合或最终 profile 已通过。

| 路径 | 必须执行的 PB 语义 |
| --- | --- |
| managed reserve | account → supplier scope/control → p INSERT stamp → 实际 admitted_at 的 gate(U) → 期限/报价重核 → reserved PE/account journal → PB allocation/event；原窗绑定原 admitted_at |
| supplier 后续效果 | 消费同事务真实 PE、精确 source sums；零 delta 也有 PB event；pause/缩 limit 不阻止保守收尾。无 control 时 marker=NULL，不虚构 PB event |
| dispatch | 同根真实 prepared→sending 才触发 gate；tenant/key/procurement 都可能新建窗口。全部 control 等待后选同一 DB decision time，重核期限/报价并绑定实际 permit |
| 后配置 | control/configuration/version/origin 仍由原 APP Commander 创建；未终结 p 导入为 legacy_import，baseline=当时 spent，不归当前窗 |
| terminal p 后续更正/重开 | 首配未导入该 p 时，固定 helper 按真实新 PE 可建 supplier legacy_catchup，要求 prior H=0、baseline=prior spent；不授配置/import 命令能力 |
| Reconcile 客户 finish | 同根锁 tenant/key PB，消费真实新 settlement，更新既有 customer allocation 及其原窗口；不能补 customer catchup 或遗漏原 reserve/import effect |

FinishCustomerTx 对无 allocation 的已关闭旧来源仅在 marker=NULL 且无已应用 effect 时跳过；有 allocation 必须核原 reserve/import 与真实 settlement。候选不能新建缺失 customer allocation 掩盖 reserve/import 未接入。普通客户 reserve 仍是既有客户路径；request/attempt 出生来源的新增记录不等于把全部客户资金写入迁给采购 writer。

## 7. Reconcile 客户 finish 的非 PB 接缝

完整根必须在数据库内落实原 [finish](../../internal/billing/settle.go) 与 [ledger](../../internal/billing/store.go) 的真实动作。下表冻结原 SQL 可见写列的最低依赖；它不是客户全部资金 source checker，也不是完整新 tracking 列 ACL。尚未命名的原件反向列与新 companion INSERT 列须随可执行 DDL 补齐，不能据本表宣称整个 writer ACL 完成。

| 原关系 | 固定写面 |
| --- | --- |
| ledger_accounts | INSERT(tenant_id,kind) 仅原锁前初始化；UPDATE(balance)；根只用本 tenant AVAILABLE/HELD 及需要时平台 REVENUE |
| journals | INSERT(id,tenant_id,operation_id,kind,reference,actor)；currency/created_txid/created_at 沿原 DEFAULT，读真实结果 |
| journal_lines | INSERT(journal_id,account_id,signed_delta)；无 UPDATE/DELETE |
| settlements | INSERT(id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id)；不含后续 refund UPDATE |
| api_keys | UPDATE(held,spent)，精确 tenant/key 谓词；不授配额/owner/策略字段修改 |
| reservations | UPDATE(status)；身份/金额/期限不变 |
| request_records | finish UPDATE(billing_status,status)；finalization.latest 等受保护写面另属对应闭合模板 |
| reconciliation_cases | propose/approve/finish 联合 UPDATE(state,requester,approver,proposed_usage,evidence_ref,proposal_reason,reason)；原 pending helper 的 INSERT(tenant_id,request_id,reason) 与 reason 冲突更新须随真实保留分支审定 |
| recovery_jobs | DELETE，仅固定本 request 清理，不是 PB 历史表 DELETE 授权 |
| billing_profile_blocks | INSERT(model_id,profile_version,reason,request_id)；原超预占分支 ON CONFLICT DO NOTHING 保留 |
| audit_events | 候选预分配 id：INSERT(id,actor,tenant_scope,action,target,sanitized_diff,request_id) 加共同六项 pointer；无 UPDATE/DELETE |
| projection_outbox | 候选预分配 event_id：INSERT(event_id,tenant_id,operation_id,kind,sanitized_payload) 加共同六项 pointer；版本/default 时间保留，读真实结果；无 UPDATE/DELETE |

pointer 恰为 pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid，原 pp_root_step 映射新 step_no；只用[共同合同§3.1](b-procurement-source-contract.md)既定槽，不另造第二套。reconciliation 资金 audit/outbox/profile-block 对应 CustomerFinishOutcome 精确 ID，不能用同金额原件替换。旧 SQL 不含这些 pointer 或预分配输出 ID，故本表要求后续候选固定 SQL，不据旧 Go 调用宣称已接。

根读取并锁模型/customer PB/ledger、reservation/request、case、实际 finalization/价格和必要角色原件。lockAccounts 初始化与后续锁分两个有序语句，沿 READ COMMITTED 新快照；账户全局 ID 顺序、charge≤R、raw/waived、硬 deadline 后释放、唯一 settlement、账本平衡、job清理、profile block 和原 audit/outbox 一项不省。settleRequest 返回 pending 时，没有实际新 settlement/PB finish effect，结果 companion 如实记录，不制造收费。

除 recovery_jobs 固定清理外，不推导任意原表 DELETE。新来源/head/journal/decision 完整列 ACL、finalization/proposal/cost/correction 原件及新 tracking 列权限仍与两域最终 DDL 一并审定；writer 不继承 APP 原宽表 grant。

## 8. 原021共享 trigger 的隐藏读面

采购/成本与客户 finish 写共享 audit/outbox/ledger 时，也会进入021 invoker 函数。下列正常分支虽不执行支付捕获，分支判断先读取环境：

- payment_source_writer 在 journals/outbox BEFORE 先读 worker_role；正常 settled/released journal、非 credited outbox随后退出。
- payment_worker_account_guard、payment_worker_line_guard、payment_audit_guard 的正常分支也先读 singleton/worker_role；不能因身份不是 worker 省 SELECT。
- payment_worker_balance_complete 在 account/line deferred 检查先读环境后退出；APP COMMIT 同样需要精确只读面。

writer 与 APP 因此须有效拥有 SELECT(singleton,worker_role) ON payment_sandbox_environment。不授该表写权、支付 worker 身份或其他 payment 表/配置能力。payment_extra_funding_complete 的非 sandbox journal 直接退出；line 分支只读 ledger_accounts(id,kind) 排除 SANDBOX_FUNDING_CLEARING。

原017仅将 funding_fact_guard 注册到 credit_orders、funding_receipts、journals、voucher_redemptions、projection_outbox，**未注册在 journal_lines**。021替换后的普通 settle/release journal 分支只读 journal_lines JOIN ledger_accounts 排除 funding clearing 后退出，非 credited outbox直接退出。因此不能从函数 ELSE 的 NEW.journal_id 或支付验证器正文推导 writer 需要 funding/voucher/payment 全表读或 funding_journal_valid/payment_legacy_funding_journal_valid EXECUTE。它们对原资金/支付入口的既有权限仍沿原边界。

## 9. APP COMMIT 的 invoker 闭包

本候选不在此新增 SET CONSTRAINTS/显式 flush 协议。根内即时 SQL/trigger 以 writer 执行；根返回后仍未执行的 deferred invoker 在外层 APP COMMIT 时以 APP 当前身份执行。候选两侧均须覆盖自己的约束运行闭包，不能假设根返回已完成全部校验；实际身份与 grant 删除实验仍须真实 LOGIN 验证。不能把全部 deferred guard 改成 current_user=writer，也不能恢复 APP raw 受保护 DML 修复提交失败。

| 排队链 | APP 所需只读/普通函数 |
| --- | --- |
| pb_check_source_trigger → pb_check_customer/supplier | request/reservation/settlement、p/PE、controls/allocations 等完整当前行；EXECUTE 两个普通 check |
| pb_configuration_receipt | controls/configuration_versions 完整当前行，不需配置写权 |
| pb_prefix_check_origin/check_journal/check_source | prefix三表、controls/windows/events/allocations；普通 pb_start EXECUTE。window分支不读operations，APP配置时 operation分支另保留原读面 |
| 原014 assert_procurement_budget_projection | accounts H/S、该账户 p H/S 与 PE delta 集合，不授桶 UPDATE |
| balanced_journal | journal_lines 精确求和读面；行数/平衡不能跳过 |
| 原021正常共享链 | environment(singleton,worker_role)、journal_lines/ledger_accounts 的原排除检查，不进入支付来源验证 |
| 新 pp 根/来源双向约束 | 最终实际正文所需原件及 typed source/head/journal/companion 只读；deferred 若显式调用普通校验函数须另列 EXECUTE，不给 APP 通用 mutation helper |

APP 的原 Commander/Reader/customer 路径仍有独立合法权限；这是保留功能下收口受保护写面的新候选，不把 PB 配置迁给 writer。新 pp deferred 尚无完整可执行正文，最后一行不能替代最终精确 ACL，也不关闭提交门禁。若后续选择根内 flush，先冻结范围/身份/重入/完整约束行为，再重算两边闭包。

COMMIT 成功才确认生效。缺权限或任一来源/PB/ledger/观察约束失败，整根和原件回滚；COMMIT 回复未知按操作身份只读查证，不重放模型请求或盲重做审批/财务 effect。

## 10. 未满足项与实际验收

1. 交付固定 DDL/根/helper SQL、完整原件 tracking 列、函数 owner/proconfig/触发序和准确类型/默认 ACL；本矩阵不替代全部新 pp 与非 PB 原件权限。Reconcile pending helper 实际保留分支先决定后定稿，不把未执行分支 INSERT 固化成多余 grant。
2. 原020 PUBLIC撤销、APP精确补授、writer矩阵、021环境读面及可达角色进入**新**精确 catalog/readset/basis/profile。逐项校验普通函数/trigger依赖，不能沿用旧 profile 并称权限不变；无关原函数 PUBLIC基线也如实登记。
3. actual APP LOGIN 调受审根、NOLOGIN writer 真正执行：七列allocation、derived列零授权、三scope跨窗dispatch、supplier reserve/零delta/保守收尾、customer finish原窗均须通过。原APP首配/import/绑定/Reader也须在PUBLIC/rawDML收口后独立通过。
4. 逐个删除必要grant，覆盖 prefix append EXECUTE、journal必要列、head UPDATE、origin SELECT、configuration_versions SELECT、021环境读取和APP COMMIT闭包；失败必须让原件/source/PB/observation/ledger一起回滚。owner、SET ROLE 或fixture不代替实际LOGIN。
5. 负例：writer不能INSERT control/operation/version/origin/head、改配置/身份/derived列；APP不能raw改受保护源/投影、调private、SET writer或改search_path/trigger/函数。表grant/PUBLIC/成员/default ACL不得绕过列边界。
6. 验证原014/020/B-P1/021实际组合、即时与COMMIT身份、同事务多根、savepoint/事务回滚及未知提交只读观察。锁原语实验与静态结论只是输入，不算上述集成证据；D1/I1、B-P3与R2继续各自报告。

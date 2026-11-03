# B-P4 D1：原件扩列与关系SQL材料合同

2026-11-03；基线`7f48fecb1e567a016af2f51f80754085aab70121`。承接[列布局](b-procurement-schema-contract.md)、[共同协议](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本](b-procurement-cost-request-contract.md)及[完整ACL](b-procurement-full-acl-contract.md)。本片把32表列和原件扩列、逐边约束落为可执行的隔离SQL材料，不改变正式001–022迁移、provision/main、既有profile或线上调用。

## 1. 精确交付与装配

新纯材料包`internal/procurementprefixschema`只提供固定SQL与摘要，不接数据库/transaction、角色参数、caller before/proof或安装回调。唯一替换位`{{MONI_SCHEMA}}`必须一次按合法UTF8、非NUL、1..63字节的SQL标识符quote；不重新解释替换结果，不从live发现对象。每份原始物料先核固定SHA-256，再渲染。

装配固定顺序：32表columns → 原18表新增columns → 三域与原件全部keys → 全部checks → 全部foreign-keys。三个域文件使用准确`-- @phase keys/checks/foreign-keys`标记，不在某域FK建立时遗漏另一域尚未建立的目标UNIQUE。所有PK/UNIQUE为非deferred；关系循环使用DEFERRABLE INITIALLY DEFERRED，不能用NOT VALID跳过存量检查。物料没有GRANT、运行角色、root/validator/guard函数、触发器或stub成功函数，不是生产安装器或完整writer。

## 2. 原18表的110个新增nullable列

原字段类型、默认、既有约束和旧行值不变。准确列名/顺序与SQL以`materials/original-columns-v1.sql`固定，并按下面组展开：

| 表 | 新列组 |
| --- | --- |
| request_records | 三列origin + pp_last_decision_id |
| upstream_attempts | 三列origin + pp_quote_selected_at + pp_last_cost_mutation_id + pp_last_delivery_source_id + 六列dispatch |
| procurement_accounts | pp_version SMALLINT / pp_created_xid XID8 / pp_origin_journal_id TEXT |
| procurement_reservations | pp_created_xid / pp_origin_journal_id / pp_last_journal_id |
| procurement_budget_events | pp_root_id / pp_root_step / pp_transition_id / pp_journal_id / pp_created_xid |
| procurement_commands | pp_source_id / pp_root_id / pp_root_step / pp_created_xid / pp_account_journal_id |
| metering_events / metering_event_conflicts / finalization_records / procurement_evidence_identities | 各完整六列G |
| procurement_cost_proposals | 六列G + 六列pp_approval_组 |
| provider_cost_corrections / settlements / journals / journal_lines / audit_events / projection_outbox | 各完整六列G |
| reconciliation_cases | 六列pp_last_组 |

origin三列=`pp_origin_root_id TEXT,pp_origin_root_step BIGINT,pp_origin_xid XID8`。dispatch六列逐字沿账户§3.6。G/approval/last的列及type逐字沿物理§4。billing_profile_blocks、recovery_jobs不新增列。新列均无DEFAULT，旧行不会获得假来源。

原件CHECK显式要求组全NULL或全完整，tracked的step>0/xid>0/finite时刻；command journal仅not_found可空，approval完整仅approved。出生与quote、dispatch与managed账户/journal、原件kind/role的联合按各逻辑合同。`CHECK`必须防SQL UNKNOWN放行：所有可选分支以IS NULL/IS NOT NULL/IS TRUE等明确封闭。

G组额外以完整root/step/kind/source/xid FK到RootStep，角色按各表闭合；audit/outbox有逐槽UNIQUE，journal_lines允许同source/ledger_line的多账户行。单向通用FK不代替各领域source↔原件的准确双向FK。不存在source的旧行保持NULL，不以新DDL补历史。

### 2.1 原件约束材料与目标键

`materials/original-constraints-v1.sql`仅使用ALTER，按同一`keys/checks/foreign-keys`三phase参与装配。全部新增约束使用`pp_o_`前缀、唯一且≤63字节；PK/UNIQUE不可延迟，FK统一DEFERRABLE INITIALLY DEFERRED、默认NO ACTION。它不创建函数、trigger、guard、权限或stub，也不接业务入口。

原表目标UNIQUE按source/account/cost三份固定FK的准确目标列集汇合，复用001–022及B-P1已存在的完整同列唯一键；不把“单列id已唯一”当成宽复合FK目标键已存在。相同目标列集只添加一次，原PB controls/events、ledger_accounts、reservations等即便没有新增列，也按实际精确FK需要补目标键。所有引用列均在固定原DDL与110列扩展中核对；不查询live库反填允许集合。

这些复合键可能含原来不限长的TEXT，例如audit.actor/target/request_id、journal.reference、command.target_id及correction.evidence_ref。PostgreSQL btree索引项有页宽限制，建立宽UNIQUE可能因已有长值失败，后续原来能保存的长值也可能因索引项过宽而失败。**本候选尚未证明完整旧接口接受集与存量可安装性**；不能为让安装通过而截断旧值、缩小旧列域、回填假来源、拿摘要替代精确身份或跳过索引。正式安装前须在专用库验证真实代表数据和合法长度边界；无法保持边界时先重设计准确关系/validator分工，不能启用候选。原JSON payload不进这些索引，JSON/业务快照逐值验证仍是完整validator的义务。

### 2.2 legacy、出生及特殊组

每个CHECK最外层使用`IS TRUE`，每个可选联合显式核NULL，不允许UNKNOWN放行。它们只封闭新增追踪字段；旧行的全部新字段为NULL时保留原状态与数据，不修改原列类型、默认或既有业务CHECK。

| 关系 | 新组精确联合 |
| --- | --- |
| request_records | legacy时origin三列及last均NULL；tracked时origin三列完整、step>0/xid非零，last仍可NULL直到首decision。head/latest精确绑定由共同FK与validator分别负责 |
| upstream_attempts | legacy时全部12列NULL；tracked时origin三列及finite quote时点完整。dispatch四项全NULL时account/journal同时NULL；四项完整时step>0/xid非零、原PB checked_at有限且account/journal成对NULL或成对非NULL。tracked sending必须有dispatch证明，后续delivery变化保留证明；last-cost/last-delivery可随实际动作存在 |
| procurement_accounts | legacy三列NULL；tracked pp_version=1、created_xid非零、origin_journal完整 |
| procurement_reservations | legacy三列NULL；tracked created_xid非零、origin/last journal完整 |
| procurement_budget_events | 五项全NULL或完整，完整时step>0/xid非零；准确transition/journal/PE由账户域FK绑定 |
| procurement_commands | 五项全NULL保留原其他kind；tracked四项source/root/step/xid完整且kind仅account_create/configure。journal NULL当且仅当configure/rejected/not_found，其余tracked命令必须有journal |
| procurement_cost_proposals | birth G与approval G分别全NULL或完整；approval完整仅用于tracked birth且state=approved；tracked proposed的approval全NULL，tracked approved的approval完整。原已批准legacy可保持两组NULL，不补登记 |

出生/dispatch不是标准G，不能往原表追加caller可写kind列以便拼一个FK；它们复用source/account/cost域已有精确typed关系。metadata CHECK不能替代实际INSERT出生、源先于效果、不可变及禁止旧行补证的guard。

### 2.3 标准G、固定槽与共享原件

下表每组全NULL或六列全完整；完整时step>0、created_xid非零，并按`(root_id,step_no,kind,source_id,created_xid)`精确引用RootStep。基础G、proposal approval、case last共13个普通FK；角色不塞进payload，也不依赖action字符串代替source identity。

| 原表/组 | kind / role固定接受集 |
| --- | --- |
| metering_events | meter_choice / meter_origin |
| metering_event_conflicts | meter_choice / conflict |
| finalization_records | request_decision / finalization |
| procurement_evidence_identities | evidence_choice / evidence_identity |
| procurement_cost_proposals birth | proposal_birth / proposal_birth |
| procurement_cost_proposals approval | approval_consumed / approval |
| provider_cost_corrections | approval_consumed或unmanaged_correction / correction |
| reconciliation_cases last | reconciliation / reconciliation，仅最近人工动作，不冻结其他自动reason/state变化 |
| settlements | reconciliation / settlement |
| journals | reconciliation / customer_journal |
| journal_lines | reconciliation / ledger_line |
| audit_events | account_command/account_command、proposal_birth/proposal、approval_consumed/approval、unmanaged_correction/unmanaged_correction、reconciliation/reconciliation或settlement或profile_block |
| projection_outbox | cost_outbox/cost或reconciliation/settlement |

audit的完整G还限制对应action：账户command applied/rejected、cost_proposed、cost_approved、cost.corrected、三种reconciliation操作、billing.settled/released或billing.profile_blocked；outbox完整G只允许cost_recorded/cost_corrected或settled/released。全NULL的历史及普通客户资金/支付/绑定/余额观察路径仍按原边界，不因该静态CHECK冒称已验证其来源。新受保护action/kind不能全NULL逃逸的predicate、原actor/tenant/target/payload/时间/版本与实际source槽仍须真实guard/validator逐值核验。

audit_events与projection_outbox分别有`UNIQUE(pp_source_kind,pp_source_id,pp_source_role)`；journals也提供其领域所需的同列source槽UNIQUE。都采用默认NULLS DISTINCT，故旧全NULL可有多行；禁止用NULLS NOT DISTINCT迫使旧历史重写。journal_lines不加逐槽UNIQUE，同finish多个真实账户行通过原line id、journal/account及精确ledger companion闭合。

## 3. 完整性边界

本片逐边DDL由[source](b-procurement-source-ddl-contract.md)、[账户](b-procurement-account-ddl-contract.md)、[成本](b-procurement-cost-ddl-contract.md)合同固定。真正跨行的连续step/无漏尾、根模板、head当前最大项、NULL精确等值、历史重放、JSON/计量/gate/角色/时间资格、受保护新行不能伪装legacy等仍须不可绕过validator/guard与完整受控根。没有这些实现时，owner能修改新历史，未授业务使用或完整性证明。

落DDL发现的多态cause缺列先校准设计：attempt_delivery末尾补d_finalization/d_recovery，cost_reset末尾补mutation_cause_kind，finish补d_new_settlement；当前32表715列，68 generated、647非generated。仅更新设计物料；此前711列记录仍是上一提交的实际结果，不重签旧验证或财务历史。

## 4. 本片验收

先静态核全部列、生成式、phase/标识符/摘要及逐边引用，再在专用PG16随机schema严格串行应用精确original001–022+B-P1候选，执行完整关系物料。逐表读catalog与固定布局独立比较，检查全部FK validated/deferred、唯一键非deferred、原迁移历史不变、无新增函数/trigger/权限，退出schema零残留。

关系正/负例须在真实约束上验证，失败必须回滚；可用owner构造固定数据图但必须标明不是actual APP writer/权限认证。所有商业writer/真实角色/未知提交/新profile与完整读集仍未完成，不用一次安装成功勾选D1/I1/A0或关闭十一项Unproven、B-P3/catalog及R2门禁。

整组实际PG/race的范围和结果见[关系材料验证](../../docs/verification/procurement-relation-materials.md)：685个新约束、32表715列及原18表110列结构一致，12个命名声明边场景通过。此结果仅限本节的结构/声明测试，保留上文全部完整性与索引宽度门禁。

# B-P4 D1：成本与 Reconcile 领域声明式约束材料

2026-11-03。承接[统一物理布局](b-procurement-schema-contract.md)、[成本/request合同](b-procurement-cost-request-contract.md)、[共同来源](b-procurement-source-contract.md)与[独立重放/读集](b-procurement-replay-read-contract.md)。本页先冻结领域约束，随后交付 [cost-constraints-v1.sql](../../internal/procurementprefixschema/materials/cost-constraints-v1.sql)。该材料只有固定 ALTER TABLE，不创建角色、函数、trigger、业务根或成功stub，不安装、不发行权限。本片没有运行PG；可执行语法、整组依赖、实际LOGIN和业务入口验收仍由集成人真实验证，D1不勾选。

## 1. 所有权与装配

本片负责 normal_cost_decisions、cost_mutations、cost_reset_decisions、attempt_delivery_sources、cost_outbox_links、unmanaged_correction_sources、reconciliation_sources、customer_finish_outcomes、customer_finish_ledger_effects、customer_finish_pb_effects 的领域关系。18 canonical 的PK、公共五元键、root/step双向、R/A基础身份与基础链由共同材料提供，不重复创建。三个customer-finish companion的PK、领域输出UK与其完整source绑定在本片。

所有约束名为 `pp_cd_` 前缀，标识符不超过63字节；每个对象schema限定。`{{MONI_SCHEMA}}`只能由受信装配器替换为安全引用后的一个schema标识符。材料按 `-- @phase keys`、`-- @phase checks`、`-- @phase foreign-keys` 分段；集成人先建全部固定列与原表扩列，再跨材料运行全部keys、全部checks、全部FK，不能把本文件独立先于跨域目标键执行。UK均普通非deferred；本片FK均 `DEFERRABLE INITIALLY DEFERRED`，不使用NOT VALID、CASCADE、禁约束或跳过错误。

共同目标键准确序：canonical公共键 `(pk,root_id,step_no,step_kind,created_xid)`；引用同根cause使用 `(id,root_id,created_xid,tenant_id,request_id[,attempt_id])`，RequestDecision只有R五列。历史origin不要求与本次root相同，按真实tenant/request/attempt身份引用；account/member cut引用各自独立前缀键。cause的step严格早于结果step由只读validator验证，绝不将cause绑定结果自己的step_no。

## 2. 由本次DDL审查补齐的四个generated列

本轮设计物料从711列校准为715列（647非generated、68generated），不是修改已发行财务历史：

| 表 / 追加列 | 固定表达式 / 责任 |
| --- | --- |
| pp_attempt_delivery_sources.d_finalization | `CASE WHEN mode = 'finalization' THEN cause_source_id ELSE NULL END`；R五元FK到本次RequestDecision |
| pp_attempt_delivery_sources.d_recovery | `CASE WHEN mode = 'recovery' THEN cause_source_id ELSE NULL END`；A六元FK到本次RecoverySource |
| pp_cost_reset_decisions.mutation_cause_kind | `CASE WHEN policy = 'applied' AND cause_kind = 'procurement_dispute' THEN 'procurement_dispute_reset' WHEN policy = 'applied' AND cause_kind = 'incoming_conflict' THEN 'incoming_conflict_reset' ELSE NULL END`；反向输出FK直接核Mutation的cause_kind与cause_source_id=self |
| pp_customer_finish_outcomes.d_new_settlement | `CASE WHEN result_kind = 'new_settlement' THEN settlement_id ELSE NULL END`；本次新settlement走完整G绑定，existing只读旧结算不冒充本root出生 |

四列均TEXT nullable、GENERATED ALWAYS STORED，必须进入独立DTO/固定reader/basis/catalog并复算。完整列DDL/布局JSON由集成人交付；本片不再临时ALTER增列或默默接受原711列。固定generated提供真实条件FK，不能以两条无条件FK实现错误的AND，也不把能声明式表达的类型边降级为“validator会看”。

## 3. 行内CHECK的封闭边界

所有可能产生SQL NULL的复合逻辑最后使用 `IS TRUE`；NULL联合逐列显式核，不靠普通CHECK的UNKNOWN通过。原Money（correction、收费、余额）范围为0..MaxInt64；成本投影和计算保持原NUMERIC(100,0)可表示非负范围，不误加int64上限；N100的成本/raw/waived核非负且有限，不允许NaN通过大小比较。加法/差值与token和使用NUMERIC运算，避免BIGINT中间溢出。finite时间逐列核；不假定clock_timestamp单调，三个Reconcile时点不能互相替代。

- NormalCostDecision：known/unknown与amount精确联合；managed的p/member/account cut完整，unmanaged全NULL；五个decision封闭，write四输出全有，skip全无，no-consumption严格0/contract。known优先skip_already_known，unknown+managed disputed优先skip_disputed；其余usage/reason/rate选择消费外行，留给真实validator。
- Mutation：五种cause封闭；before/after status与amount精确联合。Reset：两cause与applied/suppressed_adjudicated封闭；procurement dispute必applied，suppressed仅incoming conflict且p已settled/released；applied有mutation，suppressed无mutation。
- Delivery：三mode封闭；dispatch严格prepared→sending、cause NULL、真实xid=created_xid和finite PB检查时点，managed account/journal成对；其余mode无dispatch专有字段、cause完整。finalization只到completed/failed/unknown，recovery只到failed/unknown。
- Unmanaged correction：金额/basis/evidence与after精确；before成本联合，空actor用SQL NULL表示并派生system，不增加财务角色要求。operation保持原1..200字节字符串，不强制UUID。
- Reconcile：input U六字段与action分支精确；propose消费confirmed_final Usage/evidence/reason，approve全NULL，release仅reason。角色snapshot为原required role/active；propose open→proposed，approve proposed→approved且双人，release保留已approved否则closed。case Usage六字段全NULL或完整，NULL与0分开。
- Finish：new/existing/pending封闭，三个时间分支、资金输出NULL联合、0≤charge≤R、raw=charged+waived与key变化核实。`selected_mode`固定recovery（approve的settleRequest）/release（直接人工finish）；已有settlement无新journal/audit/outbox/PB，pending保留markPending实际状态/理由更新。profile-block只在本次new且raw>R存在，按自然键created/reused，不改旧row归属。
- Ledger：仅HELD/AVAILABLE/REVENUE，平台tenant NULL规则、after=before+delta、客户非负、每finish每kind唯一；零delta行不能省。PB：仅tenant/key两个scope、原source_kind=settlement，且引用父outcome.d_new_settlement，禁止existing/pending伪造效果。

CHECK不读取其他行，不把mutable终态永久绑到历史snapshot。request.status/billing_status、case reason/state与ledger余额可由其他合法原流程变化，本片不声称重放其全部生命周期。

## 4. 领域FK的实际边

| 来源 | 本片实际精确边 |
| --- | --- |
| normal decision | incoming/meter choice同根A6；normal RequestDecision由finalization+root/xid/R+incoming+meter的七元键；AttemptOrigin真实mode/account；account/member完整cut；output mutation与outbox link的cause反向键 |
| mutation | 五个generated discriminator逐表同根A6；两reset分支另精确消费其cost_mutation_id，不能消费suppressed；current原attempt的last-mutation反向指针仍需guard/validator |
| reset | dispute→本次ProcurementTransition A6；incoming→本次ConflictSource的conflict自然键+root/xid/A；输出mutation八元键含mapped kind与本reset.id |
| delivery | d_finalization→RequestDecision R5；d_recovery→RecoverySource A6；account/journal指真实账户及dispatch journal精确输出；old permit由跨root(id,tenant,request,attempt)键引用 |
| outbox link | 三cause generated分别到normal/approval/unmanaged A6，且反向含该cause的outbox输出；原outbox event_id/tenant/G精确绑定；共享原件按合法slot由真实guard/validator核反向，不加错误的单域无条件FK |
| unmanaged | 原correction完整输入与G；actual mutation/outbox反向cause；原audit actor/tenant/target/request/G精确绑定，payload/action固定槽由真实guard/validator再核 |
| reconcile | 原case不可变id/tenant/request/created_at；prior/latest只指同tenant/request的原finalization；derived incoming反向reconcile_source_id；new finalization精确RequestDecision子输出；finish唯一同step companion；操作audit完整身份 |
| finish | canonical reconciliation公共五元与R；source/companion双向future ID；原reservation身份/金额/期限；所有settlement结果原值基本FK，new另外d_new_settlement+完整G；新journal、billing/profile audit、outbox精确G；profile自然键；控制scope |
| ledger/PB | 父finish完整C与tenant（ledger）；原line id/account/delta/G双向；raw line的G再精确绑定其唯一customer journal。PB与原event control/allocation/source五字段精确、原allocation/control scope及父new settlement精确绑定 |

所有原件扩列由root创建；旧原表补UK也由root统一交付，本片不重复旧keys。新跨域额外UK使用pp_cd_：AttemptOrigin的mode/account身份、RequestDecision的normal输出、ConflictSource完整incoming身份、Reset的exact输出、finish各精确结果键，以及account片所需delivery/mutation/outbox目标键。共同agent提供member前缀键，account片提供journal位置键。

## 5. 原表目标UK依赖

必须实际存在以下普通UNIQUE（或完全同列序既有键），不是只在文档声称存在：

```text
finalization_records(id,tenant_id,request_id)
reconciliation_cases(id,tenant_id,request_id,created_at)
reservations(id,tenant_id,request_id,amount,deadline)
provider_cost_corrections(id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
projection_outbox(event_id,tenant_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
audit_events(id,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
audit_events(id,actor,tenant_scope,target,request_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
settlements(id,tenant_id,request_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount)
settlements(id,tenant_id,request_id,journal_id,status,reason,charged_amount,raw_retail_amount,waived_retail_amount,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
journals(id,tenant_id,operation_id,kind,reference,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
journals(id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
journals(pp_source_kind,pp_source_id,pp_source_role)
journal_lines(id,account_id,signed_delta,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
ledger_accounts(id,kind)
ledger_accounts(id,tenant_id)
periodic_budget_controls(id,scope)
periodic_budget_events(id,control_id,allocation_id,source_kind,source_id)
```

原PB allocation `(control_id,id)`、profile-block `(model_id,profile_version)`复用既有键。旧journal/source槽UNIQUE允许原全NULL多行；不得用NULLS NOT DISTINCT破坏旧行。reservations没有key_id也不新增tracking；key关联只经request。profile-block无G、不虚构id。

## 6. 不能由本片ALTER冒称完成的义务

下列必须在真实readonly validator、不可绕过write guard及完整DB根实现，不得写一个恒true函数或用计数通过：

1. 所有cause step严格更早、各入口模板及成功/skip/replay/noop步数；RequestDecision正常branch和delivery对应incoming真实attempt。cause FK不绑定自身step，不按时间/UUID猜顺序。
2. 正常成本真实before及完整优先级、独立计价/quote、mutation连续before、reset政策时点、current last pointer；同额/unknown→unknown保留；原cost_recorded唯一碰撞整根回滚。
3. account/member cut是真实当时完整前缀，非任意已存在/未来tail；managed/unmanaged与p存在性、dispatch journal mode、真实permit及旧root来源；所有dispatch post-wait gate/期限/quote。
4. 受保护原件的新行不能全NULL逃逸；action/kind/role槽、audit/outbox JSON与原ABI/版本/时间逐值；六G只授权实际root消费，不认证caller自报。
5. Reconcile角色实际失权/幂等先后、实际case proposal输入、source-first、子finalize只同root且无attempt、内部conflict全回滚、三时钟及最终资金deadline。case current反向pointer只表示最近人工动作，不把自动finish/markPending误认证成手工历史。
6. settlement.finalization_id的NULL精确相等，old existing result不改旧G；journal actor=system/currency、created_txid与实际top-xid；profile created/reused真实存在性与本次audit。原NULL FK的MATCH SIMPLE不作为NULL相等证明。
7. ledger集合精确为HELD/AVAILABLE及最终charge>0才REVENUE；逐行delta与父R/charge相等且合计0，不能只数行；真实locked余额before/target/RETURNING和tenant/key守恒。PB按当时scope配置/原window逐event完整，缺control才可无effect，不能用今天配置证明历史absence。
8. owner/角色/列ACL/PUBLIC/defaultACL、即时与APP COMMIT身份、不可变拒写/TRUNCATE、source/root seal、取消/超时/回滚/未知COMMIT只读观察和原资金/PB组合。所有实现/PG检查仍未由静态DDL材料验收。

本片不改旧checker/Base/十一Unproven、V3读集/roots/golden、已应用迁移或生产环境。root汇总全体材料后才可语义复核架构、更新清单并运行真实集成验证。

## 7. 静态交付记录

2026-11-03，成本材料交付145条固定`ALTER TABLE`：3个PK、44个UNIQUE、33个CHECK、65个deferred FK。使用pglast 7.22对安全引用后的schema占位替换结果解析，145条语句全部通过；按715列布局核新表FK两端列、arity及所有新表目标UK完整列序均匹配。三个phase精确存在，约束名均不超过63字节，65个FK全部`DEFERRABLE INITIALLY DEFERRED`，33个CHECK最终均为`IS TRUE`。材料无函数、trigger、授权、业务DML、NOT VALID或忽略异常。

新增`outbox_id UNIQUE`落实一原件一link；另按物理合同§3显式声明normal finalization/输出、reset输出、mutation cause、unmanaged操作/原件/输出/audit及reconcile audit的业务唯一键；mutation/link的条件正向FK同时消费cause的预分配输出ID，配合cause反向键阻止同cause额外输出。33个CHECK不替代本页§6跨行义务。原system billing/profile audit使用不含actor的完整身份/G目标UK；actor=system以及action/role/payload固定槽仍由真实guard/validator核实。共享audit/outbox/correction及PB事件保留其他原业务写面，不能添加单一子域无条件反向FK。

成本领域静态交付时没有运行PG、安装角色/ACL、业务根或原writer，也没有完成D1验收。整组原表目标UK、真实PG语法/类型/执行、实际LOGIN与负例，由集成人独占执行并另记结果；不能把上述静态解析标为商业或生产验收。

集成后真实PG/race的整组结构与命名声明边结果另见[关系材料验证](../../docs/verification/procurement-relation-materials.md)。上文“未运行PG”只描述领域agent静态交付时点，不覆盖后续集成人的记录；结构通过不等于本页列出的业务guard/writer/actual APP验收。

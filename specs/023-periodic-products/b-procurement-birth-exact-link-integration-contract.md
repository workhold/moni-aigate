# B-P4：真实 request／attempt 出生的 exact-link 集成合同

2026-11-04；合同、A1独立关系材料及真实writer/guard已交付，窄范围actual APP PG通过，完整并发/撤权/预算等矩阵仍待补齐，见[实际记录](../../docs/verification/procurement-request-birth.md)。承接[宽 TEXT 合同](b-procurement-exact-link-contract.md)、[准确关系清单](b-procurement-exact-link-inventory-v1.json)、[完整 attempt 写入根](b-procurement-attempt-birth-writer-contract.md)、[物理布局](b-procurement-schema-contract.md)、[执行](b-procurement-execution-contract.md)与[完整 ACL](b-procurement-full-acl-contract.md)。本页把 A1 可独立安装的真实 request 出生切片与 A2 所需的扩展闭包分开冻结，替代“先写业务函数、以后解决宽索引”的实施顺序。原有 Go、SQL 材料及摘要、001–022、数据库与运行入口均不修改；新增独立关系包仅渲染DDL，本页不是完整A1/A2或线上启用验收。

## 1. 两个显式候选；A1 不等待 A2

| 候选标识 | 新装配范围 | 明确不装配或不授予的能力 |
| --- | --- | --- |
| `birth-exact-link-a1-v1` | 四张新表 `pp_operation_roots/pp_root_steps/pp_request_origins/pp_request_heads` 的既定列；`request_records` 的 `pp_origin_root_id/pp_origin_root_step/pp_origin_xid/pp_last_decision_id` 四列；本页 A1 native 定位边、精确边、guard、真实 AFTER writer、seal/deferred validator、独立 catalog/ACL | 不依赖 attempt、账户或成本新表；不安装决策、派发、结算、审批、恢复的关系/根；request head 固定 ordinal0/last NULL；新 last 不能前进。原 Reserve 资金/PB 路径继续运行 |
| `birth-exact-link-a2-v1` | 在 A1 上增加原 attempt 出生列与 `pp_attempt_origins/heads/nodes`；完整 command+reserve 的 `pp_account_command_sources/pp_account_origins/heads/journal/pp_procurement_transitions`，以及原 A/p/PE/command/audit 的必要扩展、精确边、guard、真实写入和验证器 | 仅 command create/configure 与 attempt reserve 前缀。没有 dispatch、normal finalization、cost mutation、member、审批或恢复写权；未实现种类明确拒绝，不能用 validator 成功 stub 接受 |

新关系材料固定使用独立包 `internal/procurementbirthschema`，暴露 `A1SQL(schema string) (string,error)` 与 `A1Digests() ([]MaterialDigest,error)`；只渲染关系DDL，不连接数据库、不授权限、不提供guard成功stub。writer/guard/validator/ACL由独立 `internal/procurementrequestbirth` 装配；先schema、再真实writer材料。两者均经root审定后实施，不能由名称存在推断能力已安装。

新材料必须是单独固定字节与 SHA-256 清单的显式装配；关系包实际固定 `a1-tables-v1.sql/a1-original-columns-v1.sql/a1-constraints-v1.sql` 三份材料；writer/guard/ACL另在 `procurementrequestbirth` 固定，A2 再有独立扩展材料。实施时冻结准确文件顺序、函数/trigger/constraint/列 ACL inventory 和 expected catalog。不能调用现有 `procurementprefixschema.SQL(schema)` 安装完整旧候选后再 DROP：新增宽 UK 在 DROP 前已可能拒绝合法旧行。不能更改旧六份材料或 pins，不能重签旧 account-writer v1，也不能让旧 profile 的 catalog 自动接受新候选。

四表的列、类型、generated 表达式取既定物理合同的精确切片；这不是缩减原 715 列布局的修订。新候选有自己的表/列/关系 descriptor，不能冒充完整布局的 reader/basis/profile。未装配的关系没有“已实现”状态，不能把 A1/A2 合格提升为全域 D1/I1、十一项命题、B-P3 或 R2 已完成。生产迁移与默认 main 启用仍是后续独立阶段。

## 2. 保留 native 定位；不得把额外 payload 放回索引

原 001–022 与已安装 B-P1 的键、FK、trigger 全部保留。特别是原 `request_records(id)`、`UNIQUE(tenant_id,id)`、`upstream_attempts(id)`、`UNIQUE(tenant_id,id)`、`UNIQUE(request_id,sequence)`、`UNIQUE(request_id,channel_id)`，以及 014 binding 的两个精确复合键和 p 的 `UNIQUE(attempt_id,account_id)`：这些是原接受集，不因本页重新评判后删除。

A1 新 native 键白名单为 `pp_s_root_pk(id)`、`pp_s_root_xid_uq(id,created_xid)`、`pp_s_step_pk(root_id,step_no)`、`pp_s_request_birth_pk(request_id)`、`pp_s_request_head_pk(request_id)`。root 是 guard 强制生成的非零 canonical UUID，故 root+xid/step 的组合有固定总宽度。两个 request 单列 PK 保留原自然身份；不把“Go 通常传 UUID”当作所有原 TEXT 的固定宽度证明。PK/UK/CHECK 均即时，不能把它们写成可延迟。

A1 不安装以下旧候选冗余组合 UK：

```text
pp_s_root_r_uq(id,created_xid,tenant_id,request_id)
pp_s_step_source_uq(kind,source_id)
pp_s_step_identity_uq(root_id,step_no,kind,source_id,created_xid)
pp_s_request_birth_step_uq(request_id,root_id,step_no,step_kind,created_xid)
pp_s_request_birth_source_uq(request_id,root_id,created_xid)
pp_s_request_birth_r_uq(request_id,root_id,created_xid,tenant_id)
pp_s_request_origin_birth_uq(request_id,root_id,step_no,created_xid)
pp_s_request_origin_tenant_uq(request_id,tenant_id)
pp_s_request_head_last_uq(request_id,last_decision_id)
pp_o_request_records_d25b0701_uq(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid)
pp_o_request_records_fd9a52e0_uq(id,pp_last_decision_id)
```

`pp_s_step_source_uq` 的语义不能随索引消失：request origin 的单列 PK、不可变 root/step、step 对 origin 的逐值反向比较，以及最终恰一条 step 的完整枚举共同排除第二个位置。每个 kind 只允许被其真实来源占用；不存在“不同 root 但同 source 也算合法”的窗口。A1 不装配的 `pp_s_root_a_uq/pp_s_root_attempt_uq` 不算已替换；其相应边到 A2 才加入。

新增 native locator FK 必须直接引用以上窄键。固定 A1 新边为：origin `(root_id,step_no)` → step `(root_id,step_no)`；step `d_request_birth` → origin `request_id`；root `request_id` → origin `request_id`；tracked original `(pp_origin_root_id,pp_origin_root_step)` → step `(root_id,step_no)`。准确名称固定为 `pp_bx_request_step_locator_fk/pp_bx_step_request_origin_fk/pp_bx_root_request_origin_fk/pp_bx_original_request_step_locator_fk`，均 `DEFERRABLE INITIALLY DEFERRED`。保留附录 A 中注明 native 的原候选 FK。所有额外字段按附录逐列 `IS NOT DISTINCT FROM` 比较，不使用 hash、截断、大小写/空白归一化、JSONB 文本拼接或拼接字符串身份。

不能无条件添加 `request_records(id) → pp_request_origins(request_id)`：它会要求所有旧行补来源。原件反向 native 定位通过 nullable 出生 step，guard 再以真实 original.id 精确找 origin；legacy 四列全 NULL 保持原域。既不加宽 UK，也不新增一个能由 APP 自报的 tracked bool。

### 2.1 A1关系包的准确交付 inventory

[关系包](../../internal/procurementbirthschema/materials.go)只有两个装配API `A1SQL/A1Digests` 、数据结构 `MaterialDigest{Name,SHA256}` 及固定错误 `ErrMaterials/ErrIdentifier`；SQL含0个函数、0个trigger、0个grant、0个owner变更。新表owner沿显式安装调用者默认，原request owner不变；writer函数owner/ACL另由其专门材料安装。

| 装配顺序与固定SQL材料 | 内容 | 未渲染SHA-256 |
| --- | --- | --- |
| [a1-tables-v1.sql](../../internal/procurementbirthschema/materials/a1-tables-v1.sql) | roots11列、steps23列（含原18个generated d）、request_origins33列、request_heads4列，共71列；定义逐字保持旧715列材料的对应切片 | `483e429714616143337db75318a23a01b755a7585088879e2ec465d4aa6b0d74` |
| [a1-original-columns-v1.sql](../../internal/procurementbirthschema/materials/a1-original-columns-v1.sql) | request四个nullable pp列，无DEFAULT/backfill | `2a0e056155a7947047bca5d1473ece29b575df34f2e6bf536d79704a8cfe08d7` |
| [a1-constraints-v1.sql](../../internal/procurementbirthschema/materials/a1-constraints-v1.sql) | 先§2的5个键，再§3.3的8个CHECK，最后附录A保留6个native FK及§2新增4个locator FK，共23项 | `471931a538b4a304c5b0999fcacb5e8bb24ddcce48d2cd785cd88e96b2f087aa` |

A1SQL拒空schema、超过63字节、非法UTF-8或NUL；使用 `pgx.Identifier{schema}.Sanitize()` 对整个显式schema作一次安全引用，再仅一次替换固定占位符。允许合法带引号/空格/中文的标识，包含占位符本身的标识不递归替换；不拼接未经引用的schema、不从数据库发现摘要。安装必须显式取得该DDL，再装真实writer/guards/ACL；relation-only API不连接数据库、不自动迁移或启用main。

## 3. A1 的真实 request 出生与完整映射

### 3.1 单次出生顺序

实际 APP LOGIN 仍调用原 `billing.Store.Reserve`。APP 的 request INSERT 恰为[完整 ACL §4.1](b-procurement-full-acl-contract.md#41-request自动出生保留原reserve)的 21 列；不授 created_at、初态、latest、PB 或 pp 列。新早期 invoker BEFORE guard 要求 READ COMMITTED、actual session_user=绑定 APP、真实初态 `accepted/reserved/latest NULL`、四个 pp 列全 NULL，原四个 PB stamp/control 列在原020触发器之前也全 NULL。不能仅依 DEFAULT 判合法。

原 `pb_request_stamp` 与 `request_retail_snapshot` 继续实际运行。所有 BEFORE 完成后，受限 W-owned SECURITY DEFINER AFTER INSERT 函数只处理真实 NEW：

1. 生成本次非零 root UUID，读取实际 top-xid/actual LOGIN OID；核真实 NEW 的原状态、020 stamp、所有 frozen 列与无既存 origin/head。没有外部登记旧 request 参数或公开 helper。
2. INSERT 完整 RequestOrigin；INSERT RequestHead `(request_id,ordinal0,last NULL,updated_xid)`；此时本 root 的 step/root 均尚不存在。
3. 同 id 的内部 UPDATE 只写三列 `pp_origin_root_id/pp_origin_root_step/pp_origin_xid`，NEW=该不可变 source 的本次出生组，OLD 三列全 NULL、last 仍 NULL、本 root 尚无 step。所有 frozen/状态/latest/PB/created_at 均不得赋写。新 BEFORE guard只允许这个封闭 W 分支，不以 xmin=current 或同 xid source 就准许任意旧行补证。
4. INSERT 唯一 request_birth RootStep `(root,1,request_birth,request_id,xid)`，然后最后 INSERT `request_birth/request/born/step_count1` root seal；seal guard 逐行检查完整图和反向全集。函数返回后仍必须由外层 deferred/COMMIT 检查；后续原客户 reservation/资金/PB 任一失败，来源和 seal 一起回滚。

不能把 origin 的出生提前到原 BEFORE stamp 之前，也不能在第一次资金/PB 等待后改选零售价。AFTER 自动来源证明的是本次 request INSERT 的冻结事实，不证明之后原 Reserve 的全部资金动作可信，更不证明 APP 传入的 retail_priced_at 是独立认证的 DB 时钟。它沿原较早 `SelectEffectiveTx` 时点，018只核其既有接受集。

### 3.2 精确 payload 与可变状态分界

RequestOrigin→original 映射除附录 A 的身份组外，固定如下；左侧为 source 列，未标箭头时与原列同名：

```text
tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,
input_upper_bound,output_limit,profile_version,policy_version,request_hmac,
route_revision,request_deadline,instance_id,streaming,
client_ip → original.client_ip::pg_catalog.text,
pricing_group_id,tenant_pricing_revision,retail_priced_at,
request_created_at → original.created_at,
pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at
```

client_ip 保存原 INET 的 PG16 文本投影，不宣称保存 HTTP 原输入字节。nullable 的 client_ip/pricing_group_id/PB control 必须逐槽精确；NULL≠空串、NULL≠另一个 control。initial_status/initial_billing_status/initial_latest_id 只在真实出生事件比对 `accepted/reserved/NULL`，source 此后不可变；不能在每次 deferred 时拿这些 initial 值要求当前 original 状态仍未变化。否则同事务正常 finish 或后续 pending 会被误拒。

tracked original 身份、报价、硬界、期限、路由版本、PB stamp、created_at 与出生组三列永远不可 UPDATE，先改后改回也拒；APP 不能写 latest/pp_last。A1 source/head/step/root 的全部 UPDATE/DELETE 拒绝；只有原 request 三指针一次内部 UPDATE 是枚举许可。原 APP status/billing_status 的窄 finish/pending 分支按完整 ACL §4.1，基于真实 settlement/reservation/case；pending的原顺序为reservation pending→request billing pending→case INSERT，BEFORE只核当时已存在的reservation，case在deferred当前态核验，不能提前要求。不能开放任意 succeeded/cancelled，也不能因 A1 没装配 RequestDecision 而放开 last。

### 3.3 A1 CHECK 与隔离边界

保留同定义的 `pp_s_root_scalar_ck/pp_s_root_template_ck/pp_s_step_shape_ck/pp_s_request_birth_step_ck/pp_s_request_initial_ck/pp_s_request_snapshot_ck/pp_s_request_head_shape_ck/pp_o_request_birth_ck`；A1 guard另拒绝当前不支持的 root/step kind 与非零 head，不通过删除 CHECK放宽暂态。旧 CHECK 允许完整模板不等于 A1 writer 授予它。

所有 tracked 父/子 INSERT/UPDATE/DELETE 在 BEFORE 拒绝 RR、Serializable、Read Uncommitted；只接受准确 `read committed`。新 APP request INSERT 因必然自动 tracked，先拒绝错误隔离级别再产生行。既有 legacy 行不因本候选新增 tracked 限制，仍经过原020等原有要求；RR只读继续允许。不可用当前找不到 origin 推断 legacy，也不能以调用方布尔、GUC、trigger_depth、xmin/xid 单独构成身份或 phase 证明。

## 4. 实际双端 guard、锁后重读与延迟闭合

此节用于真正四表/原件，不复用 parent/child toy fixture 冒充集成。

| 事件 | 必须执行的真实行为 |
| --- | --- |
| 子 BEFORE INSERT/UPDATE | 先核角色/隔离/不可变/NULL联合。对已存在父按真实 locator `FOR SHARE`，等待后**下一条独立 SQL statement**重读，再按固定列映射比较；KEY SHARE不够。不能把等待前 read/lock/compare 藏在一个旧快照 CTE |
| 父 BEFORE UPDATE/DELETE | 先拒绝不允许的身份/出生/immutable 改写；真实行锁下，用新 statement 反向枚举所有受覆盖子，逐行检查或明确拒绝。guard必须覆盖未授予但未来可能误授的 raw DML，不能只相信外层函数已经检查 |
| 有限未出生父 | 仅 W 的上述真实出生过程允许 origin→尚未出生 step/root、head→同次来源依赖、step→尚未 seal root；native FK必须真 deferred，BEFORE不能 SELECT缺父后无条件成功。original已存在、source/head/step的插入先后与固定关系逐阶段复核；除枚举暂态以外缺父立即失败。无pending source、无UPDATE补完整payload |
| seal BEFORE INSERT | root物理行之前，读取本 root 的实际 source/step/head/original，验证恰一来源、恰一step、同xid/session OID/template/反向pointer与完整冻结payload。seal是最后来源DML；root之后不得再追加这个root的成员 |
| 双端 CONSTRAINT AFTER triggers | 每个涉及父或子写入的表真实安装 `DEFERRABLE INITIALLY DEFERRED` trigger。以事件 locator重新查最终当前行，不能继续相信较早 NEW；双向最终检查必须无缺父、无多/错子、无错NULL。外层 APP COMMIT时也实际运行，不只在W函数返回前检查 |
| DELETE/TRUNCATE | tracked行删除与原source删除明确拒绝；两端不能先删父再同locator重建legacy来保留子。TRUNCATE不受行trigger保护，W/APP/peer/PUBLIC均无TRUNCATE；每个切片表有拒绝runtime TRUNCATE的statement guard，不能以DELETE trigger代替 |

A1 final反向集合至少为：一个 request_origin ↔ 一个 original、一个 head、一个 request_birth step；每个 request_birth root恰该step/source；所有三指针反查本次source；head0/lastNULL且original.pp_lastNULL。仅检查“source存在”或count正确不够，还要逐行精确身份/列值和无额外槽。

CHECK/原native FK的NULL跳过规则不替代业务联合：原件 legacy 四列全 NULL；tracked origin三列全非NULL、last在A1强制NULL；RequestOrigin必要字段非NULL，nullable frozen字段精确比较。inactive `d_*` 全NULL不触发别的kind关系，但不能用部分NULL绕过当前kind的来源完整性。A2的management_mode及managed六列与unmanaged全NULL联合另见§6。

跨事务双端次序仍须真实验证：子先SHARE使父非key UPDATE/DELETE等待；父先写使子SHARE等待；等待后新statement看到已提交值。A1 frozen父的恶意写通常在BEFORE即拒绝，因此不能通过临时拆guard制造“真实root并发通过”证据。另行执行真实锁持有/角色撤权、合法可变父分支和A2账户变更竞争；原语实验只支持锁/快照机制，不计为A1/A2验收。

## 5. W、APP 与原 Reserve 写面

AFTER writer是唯一新增 SECURITY DEFINER 执行面，固定安全 search_path、schema-qualified SQL、无动态SQL。W为NOLOGIN，无membership、继承或SET ROLE路径；根实际检查 session_user绑定的LOGIN OID，owner/peer仅SET ROLE APP不得冒充。AFTER trigger函数不能被APP/PUBLIC/peer作为普通函数执行；trigger可调用不等于授它公开EXECUTE。

guard和精确只读validator为SECURITY INVOKER；不能靠owner读取全部表绕过最小权限。W的实际BEFORE guard需要父FOR SHARE；PostgreSQL还要求该关系至少一列UPDATE权限，不能只授SELECT后声称可锁。W新增锁列准确为root(id)、step(root_id)、origin(request_id)、head(request_id)，登记在catalog，四表BEFORE对实际UPDATE一律拒绝，包括赋原值。实际request的W三指针UPDATE足以提供其行锁能力，不额外授整表UPDATE。APP在deferred处只读最终态，不新增这四表的UPDATE锁列；W在同一外层事务取得的真实锁仍持有到事务结束，不能因definer返回就当作已释放。APP已有request UPDATE(status,billing_status)保持原读锁能力，准确SELECT/readonly EXECUTE面由writer合同列明。

W只取得四表的必要INSERT/SELECT、request三指针UPDATE和必要锁列；APP无source/head/root/step INSERT/DELETE/有效UPDATE，无request出生/quote/硬界/created_at/latest/last写权。列UPDATE仅为行锁所需的例外必须有真实拒写guard与撤grant试验，不能写成“APP绝无UPDATE grant”又实际暗授。所有REFERENCES、PUBLIC默认EXECUTE、role有效继承和系统列ACL另列准确清单。

原Reserve在同一APP事务还需要idempotency、tenant/key/model/profile/pricing、wallet/reservation/journal/line、PB、audit/outbox/recovery等原调用面；A1不得把这整套权限复制给W。018/020、002账本平衡、021共享资金排除与B-P1原trigger不跳过。原020在AFTER内部pointer UPDATE上继续要求RC并保持PB stamp；018只允许零售价快照不变，故三指针更新可与它们共存。实际权限闭包应复用完整ACL的准确原调用读面，但新A1 catalog独立验证；不可把“能INSERT request”作为“整个Reserve已可运行”的证据。

## 6. A2：attempt 原件、reserved transition 与 command+reserve闭包

A2不是A1安装前提。推进A2时先完成[attempt写入根合同](b-procurement-attempt-birth-writer-contract.md)的真实检查/错误顺序与账户command+reserve新版guard/validator，再安装本页附录B精确边。旧account-writer v1仍只接受command，对reserve PP906；不能叠加新函数后忽略它，也不能删其判错。新装配的完整正文与允许集合都要重新固定摘要、ACL和catalog。

原attempt由W完整公开根真实INSERT，先于AttemptOrigin是已冻结许可；APP无其INSERT权。managed的完整origin/transition在新末序`zz_pp_attempt_birth_p` BEFORE hook内、原`pb_procurement_stamp`及`procurement_reservation_identity`之后出生，读取真实NEW.pb_admitted_at；p物理行、PE/journal/root可尚未存在，其真实延迟边最终闭合。unmanaged从锁后真实binding缺席生成来源，不因AFTER时无p就自动分类；不重选首次quote。不得预采admitted_at、建pending origin或允许旧attempt补生。

AttemptOrigin的original payload逐项为：tenant/request/sequence/channel/channel_version/credential/upstream_model/route_decisions、actual cost_version/input/output/basis、quote_selected_at、attempt_created_at→original.created_at、pb_created_xid；initial delivery/cost状态只在出生时核prepared/unknown/NULL，不能冻结后续合法状态。route_decisions按原Go validator与编码字节界核验，完整JSONB值比较；不加入任何新btree索引。ignored expected quote保留BYTEA接受集且不入source；actual DB quote TEXT、request profile/policy/hmac与PB control ID均不能因历史通常较短而擅自缩域。

managed联合固定`account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at`全部非NULL及management_mode=managed；unmanaged六项全NULL及management_mode=unmanaged。managed head `member_count0/member_tailNULL`，unmanaged这两项全NULL。出生结束managed仅一个reserved transition node（ordinal1、previous NULL），unmanaged无node/head ordinal0。没有member表或假空member认证；后续支持节点/成员需要另增切片。

A2新native键保留：新增各表单列PK与单列唯一标识（transition.event_id/journal_id、account-origin的create operation/source/journal、command operation/audit），以及固定UUID/step的`pp_s_node_step_uq/pp_ad_journal_step`；原已有binding/p/业务键保留。对new source step组、journal source组等只含固定UUID/enum的组合，必须先给逐列字节证书再加入native白名单；没有证书不自动继承旧复合UK。所有包含未证原TEXT身份/label/quote/payload的冗余tuple不安装，附录B按真正locator与全字段比较替换。

A2不安装的宽原件UK包括：`pp_o_upstream_attempts_bae4c117_uq/191efc0f_uq`、`pp_o_procurement_reservations_a551d65b_uq`、`pp_o_procurement_budget_events_dcddcdc2_uq`、`pp_o_procurement_accounts_17ec2a0f_uq/ce343791_uq`、`pp_o_procurement_commands_c295f293_uq`、`pp_o_audit_events_ab8c881d_uq`。即使dispatch指针全NULL，也**不安装**未消费的`pp_o_upstream_attempts_b9511f91_uq`；其他原件域的宽UK同样不装配。旧布局中的完整tuple、before/after/quote/causality/position UK可在清单中追溯，但不存在“先按旧DDL安装，数据长时再换guard”的运行分支。

删去复合UK时必须保留它的独立唯一性命题。account ordinal链由实际account N→head U串行锁、同source只能消耗一次、head last/ordinal的完整反向枚举与final validator共同保证；不能仅SHARE同一父后让两个相同ordinal子并发通过。birth node靠固定唯一(root,step)及唯一来源关系、managed最终恰一node约束；A2不能据此声称一般任意node链已闭合。跨根同account执行command→reserve→configure→reserve必须真实验收，未支持的合法历史仍PP906，受证却错边/缺边仍PP902。

PB原020/B-P1的native键/FK/控制缺席同步与deferred检查继续真实运行。RequestOrigin冻结pb_tenant_control/pb_key_control/admitted_at；AttemptOrigin冻结actual quote与actual supplier admitted_at，p/PE/journal与PB effects由完整账户validator逐槽复核。不在A2安装客户finish专用的`pp_cd_pb_original_event/allocation/scope`及相应宽UK，也不把PB snapshot纯重放通过当作本写入根完整验收。

## 7. 实施顺序与专库验收

1. **A1材料/装配**：独立固定四表、四扩展列、native定位/本页精确映射、全部真实双端guard与deferred trigger、seal、实际W AFTER writer、准确ACL/catalog。SQL装配不依赖未实施域；不变更旧v1/pins，不自动启用默认main。
2. **A1实际业务**：用专用PostgreSQL16测试库与actual APP LOGIN跑原Reserve，来源从真实INSERT自动产生；owner只能准备原管理父数据与安装材料，不能手填RequestOrigin冒充。独立oracle逐列查original/source/root/step/head，原reservation/账本/PB也要原检查通过。
3. **A2装配**：新command+reserve账户guard/validator和真实attempt根一起集成；再固定全部扩展locator/FK/trigger/ACL/字节证书及附录B对应表，不得让精确关系guard调用尚未实现的成功stub。
4. **集成退出**：集成人汇总所有并行结果，复核HLD/LLD、统一架构、能力状态与源码链接后refresh/reviewed；只读架构检查与相关make门禁通过；提交新候选/验证记录，旧候选历史保持。实际供应商、支付、1Panel、生产/HA及端到端性能另验。

| 验收组 | A1必须完成的可执行证据；A2按扩展图增补 |
| --- | --- |
| 安装/legacy宽度 | 在安装**前**准备原键允许的长且不可依赖压缩的legacy TEXT，四pp列全NULL；安装后原值/原键/原业务继续可用，未补source。真实new request的原字段若原接受则精确保留；单字节不等、NULL/空串、INET投影分别断言。不得用8192B无法通过原PK的id制造假反例 |
| 真实自动出生 | APP原Reserve成功产生且只产生本次origin/head/step/seal；同事务多request不能串source/root；原idempotency重放不新增来源，事务错误全部回滚；零金额/输入型模型与已配/未配customerPB均覆盖 |
| 时点/NULL/状态 | retail Q1在更早选价后跨scheduled Q2仍保持原Q1；pb时间和原DB stamp精确同值；nullable每槽错值拒绝。出生后合法finish/pending只走窄原分支，初始值比较不误伤；latest/last、硬界或冻结字段赋原值/先改后改回攻击拒绝 |
| 真双端/不可绕过 | 逐个source/original/root/step/head错绑、缺边、多step、提前seal/追加、legacy转tracked、父删重建、同xid另root攻击；raw APP受保护DML（不含§3合法等价INSERT）/peer/owner/SET ROLE冒充、helper直调、UPDATE/DELETE/TRUNCATE拒绝。`SET CONSTRAINTS ALL IMMEDIATE`在暂态必失败、完整图后可通过 |
| 隔离/锁/角色 | RC真实业务成功；tracked RR/Serializable/RU在BEFORE拒绝、legacy原语义/RR只读不收紧；实际持锁与pg_blocking_pids核等待，新statement复核；每项必要SELECT/锁列UPDATE/xmin/EXECUTE撤权导致真实路径失败且全回滚，secret/支付来源读权不外溢 |
| 旧路径与失败 | 原资金/PB/018/020/021不绕过；在source/head/step/pointer/seal和原后续Reserve效果边界注入故障，核零本次残留。取消、锁超时、外层deferred失败/commit未知不报告成功，不重发模型或采购请求 |

独立关系包已运行 `env -u TEST_DATABASE_URL go test ./internal/procurementbirthschema` 通过；静态测试核四表71列/原request四列逐字等同既定布局，保留19项约束等同旧材料，新增4条locator映射/10条真deferred FK、固定摘要与schema安全渲染。没有运行PG、安装writer或修改旧候选；完整实际业务/权限/并发仍按上述退出条件，不能把关系静态通过记为真实出生能力已通过。

## 附录 A：A1当前候选边逐列核对

下表取四份现存关系材料的准确名称和列序。原声明的全部FK均为`DEFERRABLE INITIALLY DEFERRED`、默认`MATCH SIMPLE`；无ON UPDATE/DELETE CASCADE。native注明保留，其余旧复合FK/目标UK在新候选中由§2窄locator和§3/4全字段双端检查替换。括号内同位置列逐一对应；包括NULL槽，不能因原MATCH SIMPLE曾跳过便省略。

| 现有FK | 子表(列) → 父表(列) | 新候选处理 / 旧目标UK |
| --- | --- | --- |
| `pp_s_request_birth_root_fk` | `pp_request_origins(root_id,created_xid)` → `pp_operation_roots(id,created_xid)` | 保留native；`pp_s_root_xid_uq` |
| `pp_s_request_birth_root_r_fk` | `pp_request_origins(root_id,created_xid,tenant_id,request_id)` → `pp_operation_roots(id,created_xid,tenant_id,request_id)` | exact，父locator `id`；`pp_s_root_r_uq` |
| `pp_s_request_birth_step_fk` | `pp_request_origins(root_id,step_no,step_kind,request_id,created_xid)` → `pp_root_steps(root_id,step_no,kind,source_id,created_xid)` | exact，父locator `root_id,step_no`；`pp_s_step_identity_uq` |
| `pp_s_step_request_birth_fk` | `pp_root_steps(d_request_birth,root_id,step_no,kind,created_xid)` → `pp_request_origins(request_id,root_id,step_no,step_kind,created_xid)` | exact，父locator `request_id`；`pp_s_request_birth_step_uq` |
| `pp_s_step_root_fk` | `pp_root_steps(root_id,created_xid)` → `pp_operation_roots(id,created_xid)` | 保留native；`pp_s_root_xid_uq` |
| `pp_s_root_request_fk` | `pp_operation_roots(tenant_id,request_id)` → `request_records(tenant_id,id)` | 保留native；原既有键 |
| `pp_s_root_request_origin_fk` | `pp_operation_roots(request_id,tenant_id)` → `pp_request_origins(request_id,tenant_id)` | exact，父locator `request_id`；`pp_s_request_origin_tenant_uq` |
| `pp_s_request_original_fk` | `pp_request_origins(tenant_id,request_id)` → `request_records(tenant_id,id)` | 保留native；原既有键 |
| `pp_s_request_birth_original_fk` | `pp_request_origins(request_id,root_id,step_no,created_xid)` → `request_records(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid)` | exact，父locator `id`；`pp_o_request_records_d25b0701_uq` |
| `pp_s_original_request_birth_fk` | `request_records(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid)` → `pp_request_origins(request_id,root_id,step_no,created_xid)` | exact，父locator `request_id`；`pp_s_request_origin_birth_uq` |
| `pp_s_request_origin_head_fk` | `pp_request_origins(request_id)` → `pp_request_heads(request_id)` | 保留native；`pp_s_request_head_pk` |
| `pp_s_request_head_origin_fk` | `pp_request_heads(request_id)` → `pp_request_origins(request_id)` | 保留native；`pp_s_request_birth_pk` |
| `pp_s_request_head_original_fk` | `pp_request_heads(request_id,last_decision_id)` → `request_records(id,pp_last_decision_id)` | exact，父locator `id`；`pp_o_request_records_fd9a52e0_uq` |
| `pp_s_original_request_head_fk` | `request_records(id,pp_last_decision_id)` → `pp_request_heads(request_id,last_decision_id)` | exact，父locator `request_id`；`pp_s_request_head_last_uq` |


A1不装配`pp_s_request_head_decision_fk`、`pp_s_original_current_latest_fk`及其decision目标UK；由head0/lastNULL与original.pp_lastNULL的真实guard封闭，不以缺FK允许决策前进。`pp_s_root_original_attempt_fk/pp_s_root_attempt_origin_fk`到A2才装配；A1 root模板只接受attempt_id NULL。

## 附录 B：A2消费的旧边与精确替换范围

仍为原材料中准确列序，全部原声明为`DEFERRABLE INITIALLY DEFERRED`/`MATCH SIMPLE`。除标native者，其旧复合FK/目标UK不装配；按表中真实父locator建窄FK（有nullable出生pointer时以pointer定位，避免给legacy加无条件source FK），并对原完整列组及§6 payload双端比较。每个新locator FK与每张两端表的真实deferred trigger在A2 catalog逐名列出；不能只实现表格名称或泛化的id存在查询。

| 现有FK | 子表(列) → 父表(列) | 新候选处理 / 旧目标UK |
| --- | --- | --- |
| `pp_s_attempt_birth_root_fk` | `pp_attempt_origins(root_id,created_xid)` → `pp_operation_roots(id,created_xid)` | 保留native；`pp_s_root_xid_uq` |
| `pp_s_attempt_birth_root_r_fk` | `pp_attempt_origins(root_id,created_xid,tenant_id,request_id)` → `pp_operation_roots(id,created_xid,tenant_id,request_id)` | exact，父locator `id`；`pp_s_root_r_uq` |
| `pp_s_attempt_birth_root_a_fk` | `pp_attempt_origins(root_id,created_xid,tenant_id,request_id,attempt_id)` → `pp_operation_roots(id,created_xid,tenant_id,request_id,attempt_id)` | exact，父locator `id`；`pp_s_root_a_uq` |
| `pp_s_attempt_birth_step_fk` | `pp_attempt_origins(root_id,step_no,step_kind,attempt_id,created_xid)` → `pp_root_steps(root_id,step_no,kind,source_id,created_xid)` | exact，父locator `root_id,step_no`；`pp_s_step_identity_uq` |
| `pp_s_step_attempt_birth_fk` | `pp_root_steps(d_attempt_birth,root_id,step_no,kind,created_xid)` → `pp_attempt_origins(attempt_id,root_id,step_no,step_kind,created_xid)` | exact，父locator `attempt_id`；`pp_s_attempt_birth_step_uq` |
| `pp_s_root_original_attempt_fk` | `pp_operation_roots(tenant_id,request_id,attempt_id)` → `upstream_attempts(tenant_id,request_id,id)` | exact，父locator `id`；`pp_o_upstream_attempts_191efc0f_uq` |
| `pp_s_root_attempt_origin_fk` | `pp_operation_roots(attempt_id,tenant_id,request_id)` → `pp_attempt_origins(attempt_id,tenant_id,request_id)` | exact，父locator `attempt_id`；`pp_s_attempt_origin_request_uq` |
| `pp_s_attempt_original_fk` | `pp_attempt_origins(tenant_id,request_id,attempt_id)` → `upstream_attempts(tenant_id,request_id,id)` | exact，父locator `id`；`pp_o_upstream_attempts_191efc0f_uq` |
| `pp_s_attempt_birth_original_fk` | `pp_attempt_origins(attempt_id,root_id,step_no,created_xid,quote_selected_at)` → `upstream_attempts(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at)` | exact，父locator `id`；`pp_o_upstream_attempts_bae4c117_uq` |
| `pp_s_original_attempt_birth_fk` | `upstream_attempts(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at)` → `pp_attempt_origins(attempt_id,root_id,step_no,created_xid,quote_selected_at)` | exact，父locator `attempt_id`；`pp_s_attempt_origin_birth_uq` |
| `pp_s_attempt_origin_head_fk` | `pp_attempt_origins(attempt_id)` → `pp_attempt_heads(attempt_id)` | 保留native；`pp_s_attempt_head_pk` |
| `pp_s_attempt_head_origin_fk` | `pp_attempt_heads(attempt_id)` → `pp_attempt_origins(attempt_id)` | 保留native；`pp_s_attempt_birth_pk` |
| `pp_s_attempt_head_node_fk` | `pp_attempt_heads(attempt_id,ordinal,last_node_id)` → `pp_attempt_nodes(attempt_id,ordinal,id)` | exact，父locator `id`；`pp_s_node_position_id_uq` |
| `pp_s_node_origin_fk` | `pp_attempt_nodes(attempt_id)` → `pp_attempt_origins(attempt_id)` | 保留native；`pp_s_attempt_birth_pk` |
| `pp_s_node_root_attempt_fk` | `pp_attempt_nodes(root_id,created_xid,attempt_id)` → `pp_operation_roots(id,created_xid,attempt_id)` | exact，父locator `id`；`pp_s_root_attempt_uq` |
| `pp_s_node_step_fk` | `pp_attempt_nodes(root_id,step_no,step_kind,source_id,created_xid)` → `pp_root_steps(root_id,step_no,kind,source_id,created_xid)` | exact，父locator `root_id,step_no`；`pp_s_step_identity_uq` |
| `pp_s_node_previous_fk` | `pp_attempt_nodes(attempt_id,previous_ordinal,previous_node_id)` → `pp_attempt_nodes(attempt_id,ordinal,id)` | exact，父locator `id`；`pp_s_node_position_id_uq` |
| `pp_s_account_command_root_fk` | `pp_account_command_sources(root_id,created_xid)` → `pp_operation_roots(id,created_xid)` | 保留native；`pp_s_root_xid_uq` |
| `pp_s_account_command_step_fk` | `pp_account_command_sources(root_id,step_no,step_kind,id,created_xid)` → `pp_root_steps(root_id,step_no,kind,source_id,created_xid)` | exact，父locator `root_id,step_no`；`pp_s_step_identity_uq` |
| `pp_s_step_account_command_fk` | `pp_root_steps(d_account_command,root_id,step_no,kind,created_xid)` → `pp_account_command_sources(id,root_id,step_no,step_kind,created_xid)` | exact，父locator `id`；`pp_s_account_command_step_uq` |
| `pp_s_procurement_transition_root_fk` | `pp_procurement_transitions(root_id,created_xid)` → `pp_operation_roots(id,created_xid)` | 保留native；`pp_s_root_xid_uq` |
| `pp_s_procurement_transition_root_r_fk` | `pp_procurement_transitions(root_id,created_xid,tenant_id,request_id)` → `pp_operation_roots(id,created_xid,tenant_id,request_id)` | exact，父locator `id`；`pp_s_root_r_uq` |
| `pp_s_procurement_transition_root_a_fk` | `pp_procurement_transitions(root_id,created_xid,tenant_id,request_id,attempt_id)` → `pp_operation_roots(id,created_xid,tenant_id,request_id,attempt_id)` | exact，父locator `id`；`pp_s_root_a_uq` |
| `pp_s_procurement_transition_step_fk` | `pp_procurement_transitions(root_id,step_no,step_kind,id,created_xid)` → `pp_root_steps(root_id,step_no,kind,source_id,created_xid)` | exact，父locator `root_id,step_no`；`pp_s_step_identity_uq` |
| `pp_s_step_procurement_transition_fk` | `pp_root_steps(d_procurement_transition,root_id,step_no,kind,created_xid)` → `pp_procurement_transitions(id,root_id,step_no,step_kind,created_xid)` | exact，父locator `id`；`pp_s_procurement_transition_step_uq` |
| `pp_ad_origin_account` | `pp_account_origins(account_id,version,created_xid,journal_id,created_at)` → `procurement_accounts(id,pp_version,pp_created_xid,pp_origin_journal_id,created_at)` | exact，父locator `id`；`pp_o_procurement_accounts_ce343791_uq` |
| `pp_ad_account_origin` | `procurement_accounts(id,pp_origin_journal_id,pp_created_xid)` → `pp_account_origins(account_id,journal_id,created_xid)` | exact，父locator `account_id`；`pp_ad_origin_pointer` |
| `pp_ad_origin_command` | `pp_account_origins(create_operation_id,command_source_id,account_id,root_id,step_no,created_xid)` → `pp_account_command_sources(operation_id,id,target_id,root_id,step_no,created_xid)` | exact，父locator `id`；`pp_ad_command_create` |
| `pp_ad_origin_journal` | `pp_account_origins(journal_id,account_id,command_source_id,create_operation_id,root_id,step_no,created_xid)` → `pp_account_journal(id,account_id,command_source_id,command_operation_id,root_id,step_no,created_xid)` | exact，父locator `id`；`pp_ad_journal_command` |
| `pp_ad_head_origin` | `pp_account_heads(account_id)` → `pp_account_origins(account_id)` | 保留native；`pp_ad_origin_pk` |
| `pp_ad_head_journal` | `pp_account_heads(account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count)` → `pp_account_journal(account_id,ordinal,id,created_xid,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count)` | exact，父locator `id`；`pp_ad_journal_head` |
| `pp_ad_head_account` | `pp_account_heads(account_id,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount)` → `procurement_accounts(id,label,provider,state,limit_amount,revision,held_amount,spent_amount)` | exact，父locator `id`；`pp_o_procurement_accounts_17ec2a0f_uq` |
| `pp_ad_journal_origin` | `pp_account_journal(account_id)` → `pp_account_origins(account_id)` | 保留native；`pp_ad_origin_pk` |
| `pp_ad_journal_previous` | `pp_account_journal(account_id,previous_ordinal,previous_id)` → `pp_account_journal(account_id,ordinal,id)` | exact，父locator `id`；`pp_ad_journal_position` |
| `pp_ad_journal_binding` | `pp_account_journal(binding_id,account_id)` → `credential_procurement_bindings(id,account_id)` | 保留native；原既有键 |
| `pp_ad_p_origin_journal` | `procurement_reservations(account_id,attempt_id,pp_created_xid,pp_origin_journal_id)` → `pp_account_journal(account_id,attempt_id,created_xid,id)` | exact，父locator `id`；`pp_ad_journal_p_origin` |
| `pp_ad_p_last_journal` | `procurement_reservations(pp_last_journal_id,account_id,attempt_id,revision,state,held_amount,spent_amount,anomaly)` → `pp_account_journal(id,account_id,attempt_id,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly)` | exact，父locator `id`；`pp_ad_journal_p_current` |
| `pp_ad_command_original_fk` | `pp_account_command_sources(operation_id,actor_id,kind,target_id,input_digest,command_status,command_reason,command_created_at,id,root_id,step_no,created_xid)` → `procurement_commands(operation_id,actor,kind,target_id,input_digest,status,reason,created_at,pp_source_id,pp_root_id,pp_root_step,pp_created_xid)` | exact，父locator `operation_id`；`pp_o_procurement_commands_c295f293_uq` |
| `pp_ad_original_command_fk` | `procurement_commands(operation_id,actor,kind,target_id,input_digest,status,reason,created_at,pp_source_id,pp_root_id,pp_root_step,pp_created_xid)` → `pp_account_command_sources(operation_id,actor_id,kind,target_id,input_digest,command_status,command_reason,command_created_at,id,root_id,step_no,created_xid)` | exact，父locator `id`；`pp_ad_command_original` |
| `pp_ad_command_journal_fk` | `pp_account_command_sources(journal_id,target_id,id,operation_id,root_id,step_no,created_xid)` → `pp_account_journal(id,account_id,command_source_id,command_operation_id,root_id,step_no,created_xid)` | exact，父locator `id`；`pp_ad_journal_command` |
| `pp_ad_command_audit` | `pp_account_command_sources(audit_id,root_id,step_no,step_kind,id,created_xid)` → `audit_events(id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)` | exact，父locator `id`；`pp_o_audit_events_ab8c881d_uq` |
| `pp_o_audit_step_fk` | `audit_events(pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)` → `pp_root_steps(root_id,step_no,kind,source_id,created_xid)` | exact，父locator `root_id,step_no`；`pp_s_step_identity_uq` |
| `pp_ad_transition_original_fk` | `pp_procurement_transitions(event_id,account_id,attempt_id,event_key,event_type,held_delta,spent_delta,evidence_ref,root_id,step_no,id,journal_id,created_xid)` → `procurement_budget_events(id,account_id,attempt_id,event_key,type,held_delta,spent_delta,evidence_ref,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid)` | exact，父locator `id`；`pp_o_procurement_budget_events_dcddcdc2_uq` |
| `pp_ad_original_transition_fk` | `procurement_budget_events(id,account_id,attempt_id,event_key,type,held_delta,spent_delta,evidence_ref,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid)` → `pp_procurement_transitions(event_id,account_id,attempt_id,event_key,event_type,held_delta,spent_delta,evidence_ref,root_id,step_no,id,journal_id,created_xid)` | exact，父locator `id`；`pp_ad_transition_original` |
| `pp_ad_transition_binding` | `pp_procurement_transitions(binding_id,account_id)` → `credential_procurement_bindings(id,account_id)` | 保留native；原既有键 |
| `pp_ad_transition_journal_fk` | `pp_procurement_transitions(journal_id,account_id,attempt_id,id,event_id,root_id,step_no,created_xid)` → `pp_account_journal(id,account_id,attempt_id,transition_id,procurement_event_id,root_id,step_no,created_xid)` | exact，父locator `id`；`pp_ad_journal_transition` |
| `pp_ad_journal_command_fk` | `pp_account_journal(command_source_id,root_id,step_no,step_kind,created_xid)` → `pp_account_command_sources(id,root_id,step_no,step_kind,created_xid)` | exact，父locator `id`；`pp_s_account_command_step_uq` |
| `pp_ad_journal_transition_fk` | `pp_account_journal(transition_id,procurement_event_id,account_id,attempt_id,root_id,step_no,created_xid,id)` → `pp_procurement_transitions(id,event_id,account_id,attempt_id,root_id,step_no,created_xid,journal_id)` | exact，父locator `id`；`pp_ad_transition_journal` |
| `pp_ad_journal_transition_before` | `pp_account_journal(transition_id,account_id,attempt_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly)` → `pp_procurement_transitions(id,account_id,attempt_id,before_revision,before_state,before_held,before_spent,before_anomaly)` | exact，父locator `id`；`pp_ad_transition_before` |
| `pp_ad_journal_transition_after` | `pp_account_journal(transition_id,account_id,attempt_id,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly)` → `pp_procurement_transitions(id,account_id,attempt_id,after_revision,after_state,after_held,after_spent,after_anomaly)` | exact，父locator `id`；`pp_ad_transition_after` |
| `pp_ad_journal_transition_quote` | `pp_account_journal(transition_id,account_id,attempt_id,binding_id,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis)` → `pp_procurement_transitions(id,account_id,attempt_id,binding_id,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis)` | exact，父locator `id`；`pp_ad_transition_quote` |
| `pp_ad_birth_binding` | `pp_attempt_origins(binding_id,credential_id,channel_id,account_id)` → `credential_procurement_bindings(id,credential_id,channel_id,account_id)` | 保留native；原既有键 |
| `pp_ad_birth_account` | `pp_attempt_origins(account_id)` → `pp_account_origins(account_id)` | 保留native；`pp_ad_origin_pk` |
| `pp_ad_birth_p` | `pp_attempt_origins(attempt_id,account_id,binding_id,created_xid,account_journal_id,admitted_at)` → `procurement_reservations(attempt_id,account_id,binding_id,pp_created_xid,pp_origin_journal_id,pb_admitted_at)` | exact，父locator `attempt_id`；`pp_o_procurement_reservations_a551d65b_uq` |
| `pp_ad_birth_admission` | `pp_attempt_origins(account_journal_id,account_id,attempt_id,admitted_at,root_id,created_xid)` → `pp_account_journal(id,account_id,attempt_id,pb_decision_at,root_id,created_xid)` | exact，父locator `id`；`pp_ad_journal_admission` |
| `pp_ad_birth_transition` | `pp_attempt_origins(procurement_transition_id,root_id,created_xid,tenant_id,request_id,attempt_id,step_kind,attempt_id,reserved_event_id,account_journal_id)` → `pp_procurement_transitions(id,root_id,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id,event_id,journal_id)` | exact，父locator `id`；`pp_ad_transition_cause` |
| `pp_ad_transition_from_attempt_birth` | `pp_procurement_transitions(d_attempt_birth,root_id,created_xid,tenant_id,request_id)` → `pp_attempt_origins(attempt_id,root_id,created_xid,tenant_id,request_id)` | exact，父locator `attempt_id`；`pp_s_attempt_birth_r_uq` |
| `pp_ad_transition_frozen_quote` | `pp_procurement_transitions(attempt_id,account_id,binding_id,cost_version_id,cost_input_rate,cost_output_rate,cost_basis)` → `pp_attempt_origins(attempt_id,account_id,binding_id,cost_version_id,cost_input_rate,cost_output_rate,cost_basis)` | exact，父locator `attempt_id`；`pp_ad_birth_frozen_quote` |
| `pp_ad_transition_frozen_bounds` | `pp_procurement_transitions(request_id,tenant_id,input_bound,output_bound)` → `pp_request_origins(request_id,tenant_id,input_upper_bound,output_limit)` | exact，父locator `request_id`；`pp_ad_request_frozen_bounds` |


附录B的`pp_s_node_previous_fk`在本次唯一birth node上previous为NULL，不构成一般链证明；`pp_s_attempt_head_member_fk`及member域不装配，A2始终member0/NULL并禁止推进。两类head/node的反向恰一关系由§6真实validator完成。

复合UK中的冗余语义仍须逐一覆盖：旧`pp_ad_transition_original/cause/cause_event/journal/before/before_attempt/after/quote`、`pp_ad_birth_frozen_quote`、`pp_ad_request_frozen_bounds`、`pp_ad_journal_p_origin/p_current/transition/admission/head/position/position_xid/ordinal`、request/attempt source/root/step组不再形成宽索引；对应完整列关系、唯一ordinal/source与精确因果仍被受控写入、串行父锁、native窄PK与最终双向枚举证明。未被实际A2路径消费的完整域键/FK不安装、不宣称修复，也不能在未来启用时悄悄恢复宽UK。

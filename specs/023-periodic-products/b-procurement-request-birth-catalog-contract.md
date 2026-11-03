# B-P4-A1：实际安装 catalog 与全 grantee ACL 验收合同

2026-11-04；本页先冻结新增 `internal/procurementrequestbirth/request_birth_catalog_postgres_test.go`，承接[writer](b-procurement-request-birth-writer-contract.md)、[关系/exact-link](b-procurement-birth-exact-link-integration-contract.md)、[raw/误授](b-procurement-request-birth-raw-contract.md)与[必要授权故障](b-procurement-request-birth-grant-fault-contract.md)。只新增本合同与根审定后的独立测试，不修改 SQL/摘要、原 fixture/测试、verification、架构、Git 或运行入口；实际 PG 由根串行执行。

状态：根已按本合同完成专库正控与78项事务漂移验收，源码与精确证据见[验证记录](../../docs/verification/procurement-request-birth.md)。该结果只属于显式测试 inspector，不是运行期安装资格或完整 profile。

## 1. 范围与不能越过的状态边界

目标是**已实际安装对象**准确匹配经过审定的 A1，而非材料字符串包含正确名称。既有 fixture 安装函数、调用 `pp_rb_validate_request_v1` 或只读重放通过，都没有自动执行本页完整 catalog/ACL 认证；未实际调用下述 inspector 之前，不得把该安装说成 catalog Qualified 或正式运行 profile。此 inspector 是新增测试中的只读 Go 检查，不给 APP 新 DB helper，不在默认 main/provision 自动启用。

准确观察五个关系：原 `request_records` 的 33 普通列，以及 `pp_operation_roots(11)/pp_root_steps(23)/pp_request_origins(33)/pp_request_heads(4)`，共104普通列；只允许四个 pp 业务表和其5个键索引。scope还含七个 A1函数、16个新 trigger、request原四个 trigger、A1新增23项关系约束（5键/8CHECK/10FK）、原 request 的15项关系约束、六个用户 constraint-trigger 记录、五关系系统列 ACL，以及 schema/角色和两个外部保守权限面。

不把本页称为原 Reserve 全部管理/资金/PB 的完整角色 profile，更不包括 A2账户/attempt/成本域。原角色 Provision 的其他业务权限保留；本页检查 A1所有对象上的**全部 grantee**，不能只过滤 APP/W 后忽略 PUBLIC、peer、projector 或意外角色。

## 2. 固定身份、OID与对象归属

1. 复用专用PG16随机 fixture、actual APP/peer LOGIN、W NOLOGIN；安装者由 owner连接的真实 session_user/current_user独立读取，两者必须相等。APP/W的绑定 OID 与名字逐一对应真实 pg_roles；同名不同OID、交换OID、额外membership均拒绝。
2. APP/peer为LOGIN，W为NOLOGIN；APP/W/peer均NOINHERIT、非superuser、非CREATEDB/CREATEROLE/REPLICATION/BYPASSRLS，无到/from这些角色的 membership。projector为fixture原NOLOGIN角色，只保留原schema USAGE；不是A1表/函数owner或额外A1 grantee。owner是明确受信安装者，不伪称普通runtime权限或尝试撤owner隐含能力。
3. schema owner与五表owner均为真实安装者；七函数owner恰为绑定W。APP/W/peer/projector均不是schema/表owner，W无schema CREATE或表所有权/SET ROLE入口。schema原始ACL仅安装者、APP/W/peer/projector的既定USAGE，安装者有CREATE；PUBLIC及其余角色没有schema权限，所有runtime角色没有CREATE/授予选项。
4. inspector建立当前合法安装的schema/五表/七函数OID映射，逐个核名字、namespace、owner、种类、引用端真实OID；fault前后的同名重建不能被当作同一OID。FK/trigger必须指向该映射内的真实对象，不以同名其他schema对象冒充。数值OID不可跨fixture硬编码；角色OID来自绑定，关系/函数OID由独立owner读回后与每条引用共同认证。

## 3. 普通列、generated、默认值与索引

五表均为普通永久heap关系、非partition、无RLS/forceRLS/inheritance。逐列核**物理attnum顺序、名字、类型OID/typmod、collation、NOT NULL、identity/generated、DEFAULT/生成表达式**，不只核列数或宽泛TEXT类型。不得有dropped/额外普通列。original request33列保留001–022定义，新增四个pp列nullable无DEFAULT；四新表71列保持[既定物理切片](../../internal/procurementbirthschema/materials/a1-tables-v1.sql)。不把该对照修改为“当前 live catalog 恰好长这样”。

准确19个 stored generated：RootStep的18个 `d_<kind>` 各为 `CASE WHEN kind='<kind>' THEN source_id ELSE NULL END`，只有对应kind派生真实自然ID；RequestOrigin.step_kind恒为request_birth且NOT NULL。其他列不能意外变成DEFAULT、generated、identity或不同类型。NULL、finite时间、INET到source TEXT的投影仍由真实运行测试认证，catalog只验证表达式和类型没有漂移。

五新键为`pp_s_root_pk(id)`、`pp_s_root_xid_uq(id,created_xid)`、`pp_s_step_pk(root_id,step_no)`、`pp_s_request_birth_pk(request_id)`、`pp_s_request_head_pk(request_id)`。每项核constraint种类/列序与其真实backing index：btree、unique、valid/ready/live/immediate、无predicate/表达式/include列，索引列/顺序/默认opclass/collation匹配。四新表没有其他index，尤其不允许恢复任何payload宽UK。

原request两个键 `request_records_pkey(id)`、`request_records_tenant_id_id_key(tenant_id,id)` 和 `requests_tenant_time(tenant_id,created_at DESC,id)` 保留。五表总共8个index，其中7个键索引和一个原时间索引；不得因有PK便忽略额外partial/hash/include索引。

## 4. CHECK、FK与真实deferred合同

8项新增CHECK准确为`pp_s_root_scalar_ck/root_template_ck/step_shape_ck/request_birth_step_ck/request_initial_ck/request_snapshot_ck/request_head_shape_ck`及`pp_o_request_birth_ck`，核完整表达式、validated、noinherit和约束归属。保留root_template的既定多模板表达式不表示A1 guard支持所有模板；不得只查其中出现request_birth后就接受弱化表达式。

原request保留6个CHECK：`request_records_input_rate_check/output_rate_check/input_upper_bound_check/output_limit_check/route_revision_check`及`request_retail_snapshot_shape`；也保留7个FK：tenant、owner、model、price version、tenant/key、latest finalization、pricing group。列/目标真实schema/OID、更新/删除动作、MATCH、deferrability/initially deferred和validated均与原001–022相同，不能悄悄改变原接受集。

新增10个native FK全部`DEFERRABLE INITIALLY DEFERRED`、validated、MATCH SIMPLE、NO ACTION/NO ACTION，准确如下：

| 约束 | 子列 → 父表(列) |
| --- | --- |
| pp_s_request_birth_root_fk | Origin(root_id,created_xid) → Root(id,created_xid) |
| pp_s_step_root_fk | Step(root_id,created_xid) → Root(id,created_xid) |
| pp_s_root_request_fk | Root(tenant_id,request_id) → request_records(tenant_id,id) |
| pp_s_request_original_fk | Origin(tenant_id,request_id) → request_records(tenant_id,id) |
| pp_s_request_origin_head_fk | Origin(request_id) → Head(request_id) |
| pp_s_request_head_origin_fk | Head(request_id) → Origin(request_id) |
| pp_bx_request_step_locator_fk | Origin(root_id,step_no) → Step(root_id,step_no) |
| pp_bx_step_request_origin_fk | Step(d_request_birth) → Origin(request_id) |
| pp_bx_root_request_origin_fk | Root(request_id) → Origin(request_id) |
| pp_bx_original_request_step_locator_fk | request_records(pp_origin_root_id,pp_origin_root_step) → Step(root_id,step_no) |

Root/Step/Origin/Head分别为本页四个pp表；没有无条件original.id→origin的FK，也没有宽payload目标UK。每条FK的conkey/confkey列序、conindid所引用真实目标键、convalidated/condeferrable/condeferred/confmatchtype/confupdtype/confdeltype逐项比对；native RI内部trigger不能被算成A1那16个用户trigger。普通PK/UK/CHECK即时，不把deferred trigger冒充可延迟CHECK。

只计`p/u/c/f`关系约束时五表共38项（原request15+A1新增23）。另有6项`contype=t`用户constraint trigger元数据，归§6检查；不能用总数正确替代这些分组与逐项完整定义。

## 5. 七函数的准确正文与执行属性

固定函数为 `pp_rb_request_write_v1()/capture_request_v1()/source_write_v1()/shared_write_v1()/deferred_v1()` 返回trigger，以及`pp_rb_validate_request_v1(text)/validate_root_v1(text)`返回void。均位于准确schema，prokind=f、language=plpgsql、VOLATILE、PARALLEL UNSAFE、非STRICT、非leakproof、无support/transform/额外参数默认值；参数名/输入输出类型、返回set标志、proconfig均准确。只capture为SECURITY DEFINER，其余六个SECURITY INVOKER；search_path精确为受信schema,pg_catalog,pg_temp且无第二个配置项。

除了metadata，核`prosrc`完整正文与**审定固定摘要**渲染的原材料相同，不能只核函数名/owner后接受空body或成功stub。渲染时使用该安装准确schema、APP/W名字与OID及安全dollar delimiter；预期正文来源是经过独立固定SHA审定的材料，不从live prosrc抄回。函数数量/签名必须恰七，任何额外pp函数/overload或旧账户helper拒绝；原pb_*及其他原函数不是新域实现。

原 `pb_require_read_committed()` 的owner为安装者，PUBLIC EXECUTE和显式W EXECUTE保持原ACL；只撤W直grant仍有效的事实不能误记为失权。W对allocation两列的显式SELECT同样保持准确清单，但实际pointer UPDATE身份不变时原020短路不读它们，属于**保守且当前未使用的授权**，不是必要读闭包。

## 6. 16个新trigger、原四个trigger与实际顺序

所有新trigger准确指向七函数的实际OID、tgenabled=O、tgparentid=0、无WHEN、无trigger args、无UPDATE OF列限制；ROW/STATEMENT、event bitmask、BEFORE/AFTER、constraint association逐项核。16项独立静态白名单如下：

| 表 | trigger | timing / events / level / function |
| --- | --- | --- |
| request | aa_pp_rb_request_write | BEFORE I/U/D ROW / request_write |
| request | aa_pp_rb_request_truncate | BEFORE TRUNCATE STATEMENT / request_write |
| request | zz_pp_rb_request_birth | AFTER I ROW / capture |
| request | zz_pp_rb_request_complete | constraint AFTER I/U ROW / deferred |
| Origin | aa_pp_rb_origin_write | BEFORE I/U/D ROW / source_write |
| Origin | aa_pp_rb_origin_truncate | BEFORE TRUNCATE STATEMENT / source_write |
| Origin | zz_pp_rb_origin_complete | constraint AFTER I ROW / deferred |
| Head | aa_pp_rb_head_write | BEFORE I/U/D ROW / source_write |
| Head | aa_pp_rb_head_truncate | BEFORE TRUNCATE STATEMENT / source_write |
| Head | zz_pp_rb_head_complete | constraint AFTER I/U ROW / deferred |
| Root | aa_pp_rb_root_write | BEFORE I/U/D ROW / shared_write |
| Root | aa_pp_rb_root_truncate | BEFORE TRUNCATE STATEMENT / shared_write |
| Root | zz_pp_rb_root_complete | constraint AFTER I ROW / deferred |
| Step | aa_pp_rb_step_write | BEFORE I/U/D ROW / shared_write |
| Step | aa_pp_rb_step_truncate | BEFORE TRUNCATE STATEMENT / shared_write |
| Step | zz_pp_rb_step_complete | constraint AFTER I ROW / deferred |

五个新constraint trigger均deferrable/initially deferred，关联pg_constraint的contype=t/真实表与trigger OID准确，普通11trigger没有constraint关联。保留原request的 `immutable_request_client_ip`（BEFORE U OF client_ip ROW → preserve_request_client_ip）、`pb_request_stamp`（BEFORE I/U/D ROW → pb_stamp_request）、`request_retail_snapshot`（BEFORE I/U ROW → guard_request_retail_snapshot）、`pb_request_source_check`（constraint AFTER I/U ROW → pb_check_source_trigger，deferred）。五表非internal用户trigger总数恰20。

比较名字实际字典序：request INSERT BEFORE为`aa_pp_rb_request_write → pb_request_stamp → request_retail_snapshot`；UPDATE若修改client_ip，则在early guard后还有`immutable_request_client_ip`，再到原PB/retail。AFTER capture只捕获全部BEFORE已完成的NEW；五个新增complete与原020source_check仍真deferred。禁用、REPLICA/ALWAYS替换、同名指错函数/错误事件、增加一个能改NEW的额外trigger均拒绝，不用CREATE顺序推定执行顺序。

## 7. 五关系、系统列、函数与schema的全ACL

读取relacl、每个普通/系统attacl、proacl和nspacl，展开**所有grantor/grantee/privilege/grant option**；不要只WHERE grantee=APP/W。除合法owner条目外，五关系只有APP/W下表的列级grant，表级runtime grant均无；PUBLIC/peer/projector/其他角色在这五关系无表/列权，普通REFERENCES与所有grant option均无。

| role | 准确列级能力 |
| --- | --- |
| APP | 五关系普通SELECT104；request INSERT恰21列；request UPDATE(status,billing_status)；request系统xmin SELECT |
| W | 五关系普通SELECT104；Origin INSERT32、Head INSERT4、Step INSERT5、Root INSERT11；request UPDATE三个origin pointer；Origin(request_id)/Head(request_id)/Step(root_id) UPDATE仅作锁列；Root无UPDATE |

准确普通列集沿本页§3和[grant合同§2](b-procurement-request-birth-grant-fault-contract.md#2-固定列与必要授权清单)，不能按当前有权列生成白名单。五表PG16系统列集合准确为`ctid(-1),xmin(-2),cmin(-3),xmax(-4),cmax(-5),tableoid(-6)`；只有APP/request/xmin允许SELECT，无其他runtime系统列SELECT/INSERT/UPDATE/REFERENCES/授予选项。owner隐含权单独记录而不伪称runtime零权限。

七函数原始ACL只有W owner及两个readonly函数的APP EXECUTE；PUBLIC/peer/projector/其他grantee无EXECUTE，APP五个trigger私有函数有效直接EXECUTE=false；W owner有效EXECUTE及隐含grant option不能当普通grant故障。两readonly普通函数APP有效EXECUTE=true、WITH GRANT OPTION=false。schema与角色身份按§2；另核W对 `periodic_budget_allocations(subject_type,subject_id)` **仅**两列保守SELECT，不能据此要求W allocation SELECT失权导致业务失败。

对APP/W/peer/projector使用`has_table_privilege/has_column_privilege/has_function_privilege/has_schema_privilege`逐项复核有效权与grant option，覆盖PUBLIC/继承/表级旁路。同时枚举原始ACL防止未知grantee授权；角色无membership验证不能只看rolinherit。catalog的权限集合正确与291项必要失权测试是两种证据，互不代替。

## 8. 独立预期、漂移fault与实际验收

预期inventory必须来自本合同固定列表、既定物理合同和审定的源材料SHA，不能从被测catalog过滤生成。PG会规范化CHECK/DEFAULT/generated表达式；允许在**独立、无业务行的参考schema**重放原001–022及固定A1关系DDL，取得PostgreSQL16的规范化表达式/索引/约束表示，并仅对两个受信schema qualifier做准确映射。参考DDL材料摘要必须是测试单独冻结的审定值，关系/列/键/FK/trigger/ACL元数据仍有本页独立白名单；不得复制被测表LIKE/live定义或把actual导出为expected。参考schema不装假业务source、不运行writer、不授APP运行权，独立pool/有界cleanup，不污染实际fixture search_path。

实际测试先调用完整inspector核合法安装，再以actual APP原Reserve成功COMMIT并独立checkBirth。随后owner在回滚事务中逐项改变真实catalog/ACL，inspector必须读取**同一事务**看到的漂移并返回明确错误，不能因为另一个连接看不到未提交DDL误判无变化；回滚后重新完整认证，并用实际APP正常Reserve证明恢复。漂移至少覆盖：

- 名字/OID/owner/security/search_path/body/signature的函数错误与额外pp函数；错误role flag或membership；schema CREATE旁路。
- 普通列type/null/default/generated表达式或额外列、额外pp表/view/index；宽UK、缺/错keys/CHECK、NOT VALID/错误FK目标/列序/动作/deferrability。
- trigger缺失/禁用/REPLICA/ALWAYS、错误函数/事件/列限定/WHEN、deferred变immediate、额外能改变NEW的trigger。
- APP/W表级或额外列权、系统ctid/xmax等权、APP私有EXECUTE、PUBLIC/peer/额外grantee权限、WITH GRANT OPTION以及W root(id) UPDATE恢复。

篡改注入是owner管理fault，不是APP可达攻击，也不表示writer已经运行期自动调用catalog。每项失败检查分类与相关对象，不把“有任意error”当证据；catalog本身是只读检查，无DML/自动修复/权限refresh。不会通过临时修改SQL正文或源摘要让期望迁就actual。

完成后根在专用PG16串行执行，记录无SKIP的metadata/全ACL正控制与漂移拒绝、参考/实际schema和角色零残留，再更新验证与架构。静态编译/material字符串检查不替代实际PG；合同冻结时尚未运行，后续实际结果按上方验证记录单列，不自行提升安装资格、完整profile、A2–A6、R2或商业生产状态。

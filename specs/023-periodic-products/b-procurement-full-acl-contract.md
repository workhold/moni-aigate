# B-P4-D1-ACL：非 PB 写入、来源与提交的完整权限合同

2026-11-03；源码基线 `ed1badcf455f4a6f43d072de400198d43c2cfe07`。承接[共同来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)、[PB 权限矩阵](b-procurement-pb-acl-contract.md)及[writer 权限方向](b-procurement-writer-authority-contract.md)。本页选择完整候选的非 PB 关系权限、保留 APP 路径、触发器顺序、普通函数有效权限和外层 COMMIT 只读闭包；与[物理 schema 合同](b-procurement-schema-contract.md)共同约束后续可安装材料。

**这是实施前合同，未安装 ACL/trigger/业务根，未运行实际 LOGIN 集成。** 不修改原001–022、默认 `billing.New(pool)`、provision/main 或旧 profile。PB矩阵的十三个普通函数只是PB闭包，不能作为本页全部权限；已有codec与锁原语实验也不能代替完整ACL验收。最终DDL/函数正文/列清单必须逐值固定在新catalog，不从live库生成“允许一切现有权限”的清单。

## 1. 角色、有效权限与安装终态

角色记APP（准确业务LOGIN）、W（独立NOLOGIN/NOINHERIT writer）、O（离线表/schema owner）、P（报表投影）、K（独立支付worker）。角色名/schema在安装材料安全引用，业务根不接这些标识符。APP根逐次核实际 `session_user` 的OID为安装时固定APP OID；`current_user=W`仅表示definer执行，不能代替实际LOGIN检查。O经SET ROLE APP、同名重建角色、peer继承APP都不能冒充这个实际会话。业务actor的users角色/状态另按原接口重核，不以DB角色取代双审。

W仅拥有受审函数，绝不拥有业务表、schema、database、触发器；无LOGIN、SUPERUSER、BYPASSRLS、CREATEDB、CREATEROLE、REPLICATION、系统预置读写/文件/程序角色。APP/P/K/peer和它们的SET/INHERIT/ADMIN可达闭包不得到达W/O；W也不得继承APP宽权限或支付worker。实际成员关系含PG16每条membership的inherit/set/admin选项，不只检查rolinherit。

所有新根/私有函数固定 `search_path=<受信业务schema>,pg_catalog,pg_temp`，正文内关系/函数/运算符按固定schema限定，不接动态SQL。受信schema禁止APP/P/K/peer/PUBLIC CREATE；W安装期CREATE仅临时授予，安装事务末撤回，不能遗留grant option。函数安全模式、owner、proconfig、arg/return类型、依赖与trigger顺序一起认证。

W获受信业务schema的USAGE；APP保留原USAGE，P/K只保留各自原USAGE范围，peer无新增schema/type权限。数据库CONNECT沿实际LOGIN原配置，不给NOLOGIN的W新增登录能力；database/schema CREATE及角色闭包逐项核验。内建scalar使用原pg_catalog有效type USAGE，`RETURNS TABLE`用固定OUT列，不引入自动授PUBLIC的业务复合type。新只读validator也属于受审W-owned函数，但始终SECURITY INVOKER；表、schema及trigger所有权仍为O。

候选安装先核原001–022+B-P1的精确前驱，再撤受影响原表的APP**表级**写grant及所有重叠列grant、PUBLIC和可达角色旁路，按本页重授。新关系先REVOKE PUBLIC/全部运行角色，再逐表逐列授予。禁止 `GRANT ... ON ALL TABLES/FUNCTIONS`、任意REFERENCES/TRIGGER/TRUNCATE、owner或grant option。O/W各自未来对象的默认FUNCTION EXECUTE向PUBLIC撤销；表/type/sequence默认ACL也记录实际创建者与schema，不把O的default ACL误当W的。

原 [ProvisionFor](../../internal/operations/operations.go)会重授APP全schema写权；完整候选启用时不得再运行该旧过程后继续服务。必须有候选专用、可重复收口的安装/verify流程；旧provision保持原产品路径，候选验收须证明误跑后的权限漂移被拒绝，不能靠trigger存在忽略catalog变化。正式迁移/启动接线仍属R2。

## 2. 列集记号与读取规则

本页列名是候选物理合同，不表示它们已经存在。`G`仅表示以下**六列的准确展开**，不是JSON或整表grant：

```text
pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid
```

`GA`为proposal批准组：`pp_approval_root_id,pp_approval_root_step,pp_approval_source_kind,pp_approval_source_id,pp_approval_source_role,pp_approval_created_xid`；`GL`为case当前动作组：`pp_last_root_id,pp_last_root_step,pp_last_source_kind,pp_last_source_id,pp_last_source_role,pp_last_created_xid`。各组映射同一个canonical source五元键，role是额外固定槽；不得各域再造别名。原件G/GA immutable，case GL只允许本次合法人工转换/finish推进。原件的generated discriminator不列INSERT/UPDATE。

`ALL_FIXED(t)`表示**新表按schema合同、原表按固定001–022及本候选新增组逐列展开的全部普通物理列与generated列**，安装材料必须保存准确列级SELECT注册表；不从live发现列，不授 `SELECT ON ALL TABLES`，新增列也不会自动被允许。原PB/014函数使用 `%ROWTYPE`/`SELECT *` 的受影响原件需要全部普通列SELECT；其他身份父表只给下表列。ALL_FIXED明确不含负attnum系统列；普通列全部获SELECT也不自动取得xmin权限。原PB实时tuple检查的`SELECT(xmin)`单独按§2.2收口，不能以表级SELECT兜底；whole-row、系统列、RETURNING及外层COMMIT的实际权限仍须actual LOGIN验收。

W对下列关系SELECT；未列关系无新增授权。除本页明确锁权与业务列外均无UPDATE，不授用户密码、MFA、渠道secret、API Key digest、会话/邮件、客户充值/券或支付事实读取：

| 原关系 | W的精确SELECT列 / 原因 |
| --- | --- |
| users | id,platform_role,status,created_at；当前actor/maker/checker和principal |
| tenants | id,status,allowed_regions,price_group_id,pricing_revision,created_at；身份、地域与原request retail trigger |
| memberships | tenant_id,user_id,status,allow_all_models；原principal/父行锁，不需要成员role写权 |
| api_keys | id,tenant_id,owner_id,scopes,allowed_regions,spent,held,lifetime_limit,revoked_at,expires_at,allowed_client_cidrs,created_at |
| models | id,enabled,profile,profile_version,active_revision,created_at；profile/路由/超界block parent |
| tenant_model_grants | tenant_id,model_id |
| member_model_grants | tenant_id,user_id,model_id |
| channels | id,enabled,quarantined,current_version,management_revision,created_at；不写管理发布 |
| channel_credentials | id,channel_id,revoked_at,created_at；不读secret_ciphertext |
| channel_versions | id,channel_id,region；冻结version归属与地域 |
| channel_version_credentials | channel_version_id,channel_id,credential_id；精确池成员 |
| route_policy_versions | id,model_id,revision,cost_mode,unknown_cost_policy |
| model_bindings | route_policy_version_id,channel_id,channel_version_id,upstream_model,max_input_cost_rate,max_output_cost_rate |
| route_binding_isolations | channel_version_id,upstream_model；实际absence/存在检查 |
| cost_versions | id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at,created_at；原排序完整，不读secret |
| price_versions | ALL_FIXED；原018 request trigger的price_versions%ROWTYPE，固定model/domain/两维价 |
| credential_procurement_bindings | id,credential_id,channel_id,account_id,operation_id,bound_at；真实binding与不可变快照，channel_revision DTO来自已列channels.management_revision |
| request_records,reservations,upstream_attempts | ALL_FIXED；原request/pb %ROWTYPE、初态/quote/最新source、真实dispatch/成本；不包含其他关系secret |
| procurement_accounts,procurement_reservations,procurement_budget_events,procurement_commands,procurement_evidence_identities,procurement_cost_proposals | ALL_FIXED；含新pointer、完整before/current/原件反向集合与xmin需要 |
| metering_events,metering_event_conflicts,finalization_records,provider_cost_corrections | ALL_FIXED；source-key冲突、sticky、原始身份、成本来源 |
| reconciliation_cases,settlements,journals,journal_lines,ledger_accounts,billing_profile_blocks | ALL_FIXED；原case/finish、资金目标与逐line身份、保留原funding排除检查 |
| recovery_jobs | request_id,tenant_id；Reconcile仅按请求清理，不读取或授予worker lease控制能力 |
| audit_events,projection_outbox | ALL_FIXED；共享受保护谓词、输出精确回读、反向完整性 |
| payment_sandbox_environment | singleton,worker_role；原021正常共享trigger首先读取的分支，不进入支付来源权 |

PB六表及B-P1三表SELECT与相应写列完全按[PB矩阵](b-procurement-pb-acl-contract.md)展开；其中operations对W无SELECT。W不需price_groups/pricing_operations/tenant_pricing_assignments写面，不执行零售配置；本页request自动origin保留原018语义，不额外调用pricing result/fingerprint函数。

### 2.1 行锁的额外列权

PostgreSQL的FOR SHARE/KEY SHARE/UPDATE/NO KEY UPDATE均须除SELECT外至少一列UPDATE权限。业务表已有真实允许UPDATE列时复用该权限；不能为锁追加表级UPDATE。

| W仅需锁的父表 | 额外UPDATE列 | 真实UPDATE处理 |
| --- | --- | --- |
| users,tenants,models,channels,channel_credentials | created_at | 新SECURITY INVOKER早期guard对W实际UPDATE一律42501，包括原值赋回；SELECT行锁不触发guard |
| memberships | tenant_id | 原表没有created_at，不假造该列；同一guard拒W任何实际UPDATE，包括tenant_id原值赋回 |

guard不能改为SECURITY DEFINER后再比较current_user；APP原用户、租户、成员、模型、渠道、凭据管理路径保留原规则，不被W锁用拒写分支误伤。实际源/资金表由其合法UPDATE列满足锁权；procurement_accounts的APP锁权只保留UPDATE(created_at)，按账户合同拒绝APP所有实际UPDATE。原credential binding trigger执行`UPDATE channels SET id=id`的APP权限与管理revision副作用仍沿原路径，不给W这项身份UPDATE。

### 2.2 实时xmin的精确系统列sidecar

PG16按被引用的实际attnum逐列检查SELECT；`xmin`为attnum=-2，其ACL不由全部正attnum列的授权继承。候选固定采用**系统列sidecar ACL**，只对下表的角色/关系授`SELECT(xmin)`，不授表级SELECT、其他系统列、系统列写权或grant option。W/APP其余普通列仍按前述准确矩阵；P/K/peer无新增系统列权限。下表是当前成功入口所需的完整集合，`—`表示该候选角色不因本合同取得此项权限。

| 原关系 | W `SELECT(xmin)` | APP `SELECT(xmin)` | 原SQL与候选成功路径 |
| --- | --- | --- | --- |
| request_records | 是 | 是 | 原[readCustomer](../../internal/periodicbudget/tx_sources.go#L19)总是求`pb_current_tuple(r.pb_created_xid,r.xmin)`；W的真实dispatch及Reconcile客户finish沿该源读取，APP原Reserve/普通finish继续使用；原020[pb_guard_allocation new-customer](../../migrations/020_periodic_budgets.sql#L300)及[reservation INSERT guard](../../migrations/020_periodic_budgets.sql#L520)也需要此列 |
| upstream_attempts | 是 | — | 原020[pb_stamp_procurement INSERT](../../migrations/020_periodic_budgets.sql#L187)核新attempt同事务；原[readSupplier](../../internal/periodicbudget/tx_sources.go#L61)同时求attempt tuple身份。候选仅W执行新managed birth/供应商效果，APP不保留attempt/p出生DML |
| procurement_reservations | 是 | — | 原[readSupplier](../../internal/periodicbudget/tx_sources.go#L61)求p tuple身份；原020[new-supplier allocation guard](../../migrations/020_periodic_budgets.sql#L315)核真实新p。候选managed admission及后续供应商helper由W执行，APP首配legacy_import分支不读取p.xmin |
| procurement_budget_events | 是 | — | 原[readProcurementEffect](../../internal/periodicbudget/tx_sources.go#L88)求PE tuple身份；原020[catchup allocation](../../migrations/020_periodic_budgets.sql#L327)与[supplier event guard](../../migrations/020_periodic_budgets.sql#L402)同样读取。W采购效果/terminal supplier catchup保留，APP不写本候选PE或供应商效果 |
| periodic_budget_controls | — | 是 | 原020[legacy_import allocation](../../migrations/020_periodic_budgets.sql#L338)及[legacy_import event](../../migrations/020_periodic_budgets.sql#L371)核新control同事务；仅APP原Commander首配/import执行，W不创建control、不发行legacy_import |
| periodic_budget_operations | — | 是 | 原020[pb_guard_version](../../migrations/020_periodic_budgets.sql#L742)核配置operation tuple；仅APP配置版本入口执行，W对operations仍无SELECT（包括xmin） |
| periodic_budget_observation_origins | — | 是 | 原B-P1[pb_prefix_head_guard INSERT](../../internal/periodicbudgetprefixschema/materials/candidate-v1.sql#L167)核新origin tuple；仅APP创建control/origin/head，W不INSERT这两张表 |
| periodic_budget_observation_journal | 是 | 是 | 原B-P1[pb_prefix_head_guard UPDATE](../../internal/periodicbudgetprefixschema/materials/candidate-v1.sql#L180)核本次新journal tuple；W真实window_birth append与APP原配置/window观察均推进head |

因此当前W精确五表、APP精确五表，合并为八个关系。APP外层COMMIT的只读validator不自行读取xmin；原020/B-P1的deferred检查也不因排队来自W而新增上述`—`项。APP已经有的旧表级SELECT不能作为此候选的系统权限证明：新精确profile必须收口相应表级/继承/PUBLIC旁路，并以ordinary列矩阵加sidecar验证有效权限。未来SQL若新读系统列，先更新准确矩阵/调用图和新profile，不在运行失败后扩权。

sidecar的精确收口范围就是上表八个关系；这八表的APP旧表级SELECT须撤销并按全部正列与上表系统列重授，其他关系沿其原profile权限且不新增系统列授权。V4不能把八表sidecar的“无额外系统列”误用于尚未改变的其他表级SELECT接受集。

V4 catalog须另采这八个固定受保护关系的负attnum原始ACL及角色有效权限（SELECT/INSERT/UPDATE/REFERENCES及各grant option），核只有上述SELECT(xmin)被授予、其他系统列没有额外权限；不能沿用旧`attnum>0`查询而漏检。该sidecar也纳入raw/normalized资源预算和引用解析，准确PG16系统属性集合及结果物料随可执行catalog固定。原V1/V2/V3 collector、expected和历史不改。xmin不是普通schema列，不加入105/121历史source DTO、144-table basis descriptor或离线`xmin=created_xid`判断；运行期原`pb_current_tuple`语义保持。这里只交付源码静态矩阵，未运行实际LOGIN或撤grant试验，D1仍未完成。

## 3. W的原关系mutation矩阵

下表为准确SQL目标列；未列列不授写。DB DEFAULT/BEFORE trigger生成的值不需caller列权；helper改写为显式列后仍须符合矩阵，不可从旧宽grant推导许可。除`recovery_jobs`固定清理外，W对原关系无DELETE；所有原关系均无TRUNCATE。

| 原关系 | W INSERT列 | W UPDATE列 |
| --- | --- | --- |
| procurement_accounts | id,label,provider,state,limit_amount,pp_version,pp_created_xid,pp_origin_journal_id | label,provider,state,limit_amount,held_amount,spent_amount；配置revision由原trigger计算，不授revision |
| procurement_reservations | attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,spent_amount,state,pp_created_xid,pp_origin_journal_id,pp_last_journal_id | held_amount,spent_amount,state,anomaly,updated_at,pp_last_journal_id；revision由原014、PB stamp由原020生成 |
| procurement_budget_events | id,event_key,type,account_id,attempt_id,held_delta,spent_delta,evidence_ref,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid | 无 |
| procurement_commands | operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id | 无；W只写account_create/configure |
| upstream_attempts | id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,route_decisions,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at | delivery_state,cost_status,cost_amount,pp_dispatch_source_id,pp_dispatch_root_id,pp_dispatch_root_step,pp_dispatch_xid,pp_dispatch_account_id,pp_dispatch_journal_id,pp_last_cost_mutation_id,pp_last_delivery_source_id |
| metering_events | id,tenant_id,request_id,attempt_id,source,source_event_id,normalized_usage,happened_at,G | 无；schema_version/received_at沿原DEFAULT |
| metering_event_conflicts | id,event_id,normalized_usage,G | 无 |
| finalization_records | id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version,G | 无 |
| procurement_evidence_identities | attempt_id,evidence_digest,event_id,G | 无 |
| procurement_cost_proposals | id,tenant_id,attempt_id,amount,basis,evidence_ref,reason,requester,state,expected_reservation_revision,expected_evidence_count,G | state,approver,approval_operation_id,approved_at,GA；原出生/输入屏障及G禁止改 |
| provider_cost_corrections | id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref,G | 无 |
| request_records | 无；只有APP原Reserve INSERT和其自动出生trigger | status,billing_status,latest_finalization_id,pp_last_decision_id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid；后三列只准本次AFTER birth一次设置，原冻结身份/硬界/零售价/PB birth不可改 |
| reconciliation_cases | tenant_id,request_id,reason；仅私有pending helper确需原INSERT/ON CONFLICT时 | state,requester,approver,proposed_usage,evidence_ref,proposal_reason,reason,GL |
| reservations | 无；原Reserve负责 | status |
| api_keys | 无 | held,spent；除此仅本页锁权列不增加配额修改 |
| ledger_accounts | tenant_id,kind；仅沿原lockAccounts零账户初始化 | balance |
| journals | id,tenant_id,operation_id,kind,reference,actor,G | 无；currency/created_txid/created_at为原DEFAULT |
| journal_lines | journal_id,account_id,signed_delta,G | 无；真实id由DEFAULT RETURNING取得 |
| settlements | id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id,G | 无；不授refunded_amount |
| billing_profile_blocks | model_id,profile_version,reason,request_id | 无；旧block可复用，不写本次pointer |
| audit_events | id,actor,tenant_scope,action,target,sanitized_diff,request_id,G | 无 |
| projection_outbox | event_id,tenant_id,operation_id,kind,sanitized_payload,G | 无；schema_version/created_at沿DEFAULT |
| recovery_jobs | 无 | 无；只允许固定 `DELETE WHERE request_id=本source.request_id AND tenant_id=本source.tenant_id` |

W的原表DELETE表级能力无法表达行谓词，限制由不可取得的W身份和唯一固定私有SQL实现；没有对APP开放“删任意job”的helper。W的ledger账户初始化固定仅本tenant AVAILABLE/HELD和实际需要的NULL-tenant REVENUE，初始balance=0；不触及三种funding clearing账户。INSERT/UPDATE的021共享guard仍运行。

upstream INSERT不授delivery_state/cost_status/cost_amount/created_at/pb_created_xid/pb_dispatch_checked_at；新guard再独立核实际NEW初态prepared/unknown/NULL，原020负责PB stamp。attempt origin pointer由固定root插入；pp_quote_selected_at由pp_record_attempt实际首次DB选价语句生成，完整非NULL且精确等AttemptOrigin.quote_selected_at，不授APP或后续UPDATE；其余last pointer在birth为NULL，仅对应真实动作推进。W不UPDATEquote/route_decisions或出生字段。首次dispatch的PB checked_at由原pb_guard_dispatch写入，不授caller直接写该列。

完整attempt根允许受控upstream INSERT先于source；managed p的BEFORE按字典序`pb_procurement_stamp`→`procurement_reservation_identity`→新invoker `zz_pp_attempt_birth_p`，末序hook在W+actual APP及真实新attempt/未seal私有phase下，从原020的NEW.pb_admitted_at一次完整生origin/transition并派生p pointers，随后才写p本体和采购效果；不覆盖PB时间，不增APP的p/attempt DML或private EXECUTE，不以GUC/trigger_depth/同xid单独授权。相关输出边真实deferred，readonly COMMIT闭包仍按准确SELECT/EXECUTE授予；closure最后唯一seal。replay仅原Attempt五字段，root/source均SQL NULL，无DML或旧证据补生。实际正文与权限/触发器矩阵按[出生根合同](b-procurement-attempt-birth-writer-contract.md)交付：真实request自动birth、新command+reserve guard/validator及exact-link先行，旧account writer v1的非command拒绝不删除或绕过，须显式新版候选装配；[A0–A6](b-procurement-attempt-birth-writer-contract.md#8-实施切片和实际尚缺正文)仍为缺失实施依赖，不扩本页列矩阵或715/482字段边界。

所有原件新增pointer先按schema合同SQL NULL联合/固定kind与role校验；W实际合法路径也不能写错root/step/cause。存在权限不等于允许任意账务：新typed输出双向/deferred与固定根状态机继续核验，任一原件缺源、错源、多源或漏输出均回滚。

账户命令的时间采用封闭DB采样：account_create/configure根在原锁/幂等/授权及found判定完成后，以独立DB clock语句取本次`command_created_at`，将其一次写入完整AccountCommandSource，再INSERT原command。新BEFORE command guard只在W、本次精确account command source/root/step/xid/kind/operation匹配分支，强制把`NEW.created_at`设为source.command_created_at；W仍不取得`INSERT(created_at)`。source.created_at仅诊断采时，不代替原command时间；绑定/余额观察的APP分支仍保持原DEFAULT。此选择改变了两种账户命令相对原DEFAULT在INSERT时求值的采样位置，不能宣称两个时点原本相同；须在actual LOGIN检验source先出生、guard派生值、重试/拒绝与原API返回。旧014表/DEFAULT正文不修改，新增guard由精确新profile认证。

## 4. 保留APP功能与收口边界

候选只替换采购、attempt、finalization、成本更正和人工Reconcile写入口，不把全部产品迁入W。下表是**受影响关系**的APP终态；其他用户/Key管理、模型/渠道发布、邮件、券、原客户资金、PB Commander/Reader、报表等保留原精确授权和既有guard，不给W复制这些能力。

| 关系/功能 | APP保留 | APP必须撤销/新guard |
| --- | --- | --- |
| request_records | INSERT原Reserve的21列，见§4.1；UPDATE(status,billing_status)供普通客户finish/pending | 无latest/pp_last_decision/出生/报价/硬界/身份/PB写权，无DELETE；受保护状态按真实原件核验 |
| upstream_attempts | SELECT；调用公开pp_record_attempt/dispatch/finalization等根 | INSERT/UPDATE/DELETE全撤；包括prepared写回、quote、cost、sending与所有pointer，无created_at锁权旁路 |
| procurement_accounts | SELECT、UPDATE(created_at)仅锁，原PB Commander/绑定可取行锁 | INSERT/真实UPDATE/DELETE禁；配置改为根；即使ctime原值赋回也拒 |
| p/PE/identities/proposals/corrections/meter/conflict/finalization | 原读取与候选公开根 | INSERT/UPDATE/DELETE全撤，无TRUNCATE；W负责全部实际来源 |
| procurement_commands | 原credential_bind/balance_observe的九列INSERT | 早期guard仅APP允许这两种kind且全部新增pointer NULL；account_create/configure拒；历史UPDATE/DELETE禁 |
| credential_procurement_bindings、procurement_balance_observations | 保留原合法INSERT和SELECT、原source/audit合同 | 不改绑定/观察为账户充值，不给APP伪造本候选account command |
| reservations/api_keys/ledger_accounts/journals/journal_lines/settlements | 原Reserve、Settle、Release、退款等原资金路径的固定写列及原约束 | 追加G/pointer列不给APP写；有本次reconciliation输出身份的行/动作必须由W，原资金路径不可借它们改采购latest/cost |
| reconciliation_cases | 原markPending INSERT(tenant_id,request_id,reason)、UPDATE(reason)及finish UPDATE(state) | 人工propose/approve/release由根；APP无requester/approver/proposed_usage/evidence_ref/proposal_reason/GL写权；state只许原finish关闭非approved case，见§6 |
| recovery_jobs | 原Reserve/Recover的INSERT、lease/next_run/owner/attempts UPDATE及原finish DELETE | 不把worker lease能力授W；不把job候选当deadline/permit来源 |
| billing_profile_blocks | 原普通Settle INSERT及原模型治理读取 | 不得伪造reconciliation结果companion；不按旧block存在视作本次新INSERT |
| audit_events/projection_outbox | 原共享固定列INSERT、P消费原outbox读面 | 新G列无APP写；受保护动作/成本kind即使G全NULL也拒，见§5 |

原APP对settlements只保留退款`UPDATE(refunded_amount)`，其他历史/pointer不能改；普通settlement INSERT的G全NULL。原报表P保留projection_outbox/报表/receipts权限，不因COMMIT核验给P任何pp来源、财务写面或W根EXECUTE。K保留原支付权限，无pp/root能力；同一业务schema没有租户行级ACL隔离承诺，租户边界来自固定业务根和原应用入口，不以表ACL冒称RLS。

以下将上表“原资金/Key固定列”展开为APP终态，不授表级写权。涉及原funding/payment/退款的固定guard继续执行，不因APP保留普通资金DML而替代其来源认证；这些列也不授W。

| APP保留关系 | INSERT准确列集 | UPDATE准确列集 / DELETE |
| --- | --- | --- |
| procurement_commands | operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state；仅credential_bind/balance_observe | 无 / 无 |
| reservations | id,tenant_id,request_id,amount,deadline | status / 无 |
| api_keys | id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit,expires_at,allowed_regions,allowed_client_cidrs,rpm_limit,burst_limit,concurrency_limit | name,scopes,allowed_regions,lifetime_limit,expires_at,revoked_at,allowed_client_cidrs,rpm_limit,burst_limit,concurrency_limit,held,spent；各revision仍由原trigger产生 / 无 |
| ledger_accounts | tenant_id,kind | balance / 无 |
| journals | id,tenant_id,operation_id,kind,reference,actor | 无 / 无 |
| journal_lines | journal_id,account_id,signed_delta | 无 / 无 |
| settlements | id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id | refunded_amount / 无 |
| reconciliation_cases | tenant_id,request_id,reason | reason,state；state只准原finish分支 / 无 |
| recovery_jobs | request_id,tenant_id | lease_until,owner,next_run_at,attempts / 按原finish/恢复清理 |
| billing_profile_blocks | model_id,profile_version,reason,request_id | 无 / 无 |
| audit_events | actor,tenant_scope,action,target,sanitized_diff,request_id | 无 / 无 |
| projection_outbox | tenant_id,operation_id,kind,sanitized_payload | 无 / 无 |

普通APP audit/outbox的id/时间/版本沿原DEFAULT；支付worker原有显式ID写入仍是K独立合同，不通过APP取得。request/attempt、采购历史及新增source的权限按前表，不能因该补充恢复其中任何写列。APP原用户、租户、成员、模型、渠道等未增加来源列的管理关系仍需在完整profile列明原准确权限与effective继承；它们不进入W mutation集合。

### 4.1 request自动出生：保留原Reserve

APP INSERT准确列集：

```text
id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,
input_upper_bound,output_limit,profile_version,policy_version,request_hmac,
request_deadline,instance_id,streaming,route_revision,client_ip,
pricing_group_id,tenant_pricing_revision,retail_priced_at
```

不授INSERT(status,billing_status,latest_finalization_id,created_at,pb_*、pp_*）。早期invoker guard仍核NEW.status=accepted、billing_status=reserved、latest=NULL及新last=NULL，不能仅依靠DEFAULT；拒异常初态与调用方伪造origin。原018 retail snapshot guard继续逐值核tenant domain/revision与真实price两维值，原020继续出生stamp/PB namespace。全部BEFORE成功后，专用**受限W-owned SECURITY DEFINER AFTER INSERT trigger**只从真实NEW生成RequestOrigin/RequestHead和request_birth root，读取并原位设置schema合同出生pointer；该自动函数不能被APP当普通根执行。

本候选选择AFTER内部UPDATE设置出生pointer，W仅在该固定私有函数中以同id/本次birth精确关联，只改三列origin，不触碰status/latest/quote；新BEFORE guard允许这一封闭W分支而拒APP。不能发一个允许登记任意旧request的公共函数，也不能只按xmin/xid允许回填。origin在本次INSERT之后立即完成，缺origin/反向pointer于deferred不能提交。

RequestOrigin只认证真实INSERT初态与不可变快照。原 [Reserve](../../internal/billing/reserve.go)的选价来自更早`SelectEffectiveTx`的DB clock；wallet/control等待后不能重定价，也不能将caller存入的retail_priced_at认证成独立DB时钟证明。保留原018接受集，不收紧成“必须INSERT时最新价”。完整客户reserve/资金/零售选价来源仍由其独立source合同认证；本采购root不替代它，也不据此宣称可抵御APP保留资金写权限的一切伪造。

原APP更新status/billing_status时，候选guard保持latest与pp_last_decision不变；finish仅按同tenant/request实际settlement更新billing状态，并仅在released且旧status为accepted/dispatching时转failed；pending只匹配真实reservation pending状态和case，其他status保持原值。dispatching由W真实dispatch、正常finalization status由W RequestDecision控制，不能留APP任意succeeded/cancelled入口。对还未完整认证的普通客户资金历史保留其原信任边界，不把这些窄guard当全部客户资金验收。

### 4.2 attempt完整根：不允许APP先INSERT quote

公开入口改为完整`pp_record_attempt`，参数与[共同合同](b-procurement-source-contract.md)一致。W按原顺序执行输入/route解释校验→request父锁→readAttemptChecks→DB实际quote选择→原CostQuote/policy比较→原INSERT/ON CONFLICT→若真实新attempt则私有三参birth closure及managed admission。APP完全无attempt INSERT，不能先造一个报价行再请求补origin。

expected quote的version/basis为BYTEA，has=false整组NULL；has=true时两个BYTEA均非NULL（可为空字节），两维rate可独立NULL，原strings可含NUL/非法UTF8。只在原业务实际消费expected的分支对DB实际quote UTF8 bytes精确Equal，不将被忽略字段先转TEXT或增加数值校验。source只保留DB实际选中的quote及选择时刻，不保存未消费expected。unmanaged初次选价后不为closure额外重选，避免跨scheduled price收紧原接受集；managed仍保留原account/PB等待后重核时点。

新attempt的prepared/unknown/NULL由早期guard核验；quote、route_decisions、身份、出生stamp随实际root捕获后不可改。`pp_complete_attempt_birth(tenant,request,attempt)`仅W私有普通helper，APP/P/K/PUBLIC无EXECUTE；不存在依据当前无p就可登记unmanaged的AFTER trigger。managed reserved及p/PE/journal/PB同根闭合，unmanaged明确分类。已有attempt ON CONFLICT重读仍先执行原read/check，不生成新origin/root、不补旧历史。

## 5. 共享audit/outbox的protected predicate

先检查G：部分NULL一律拒；APP/K/P/peer插入任何非NULL G一律42501，W仅允许已出生source声明的准确槽/原件ID。G全NULL不能自动视作旧历史：guard按下列固定predicate判定本次受保护行，保护族的新行必须W+完整G；真正旧行保持NULL且不可回填。

| 原共享表 | 受保护条件与槽 |
| --- | --- |
| audit_events | action精确为procurement.cost_proposed / procurement.cost_approved / cost.corrected / reconciliation.proposed / reconciliation.approved / reconciliation.released，无论target/payload伪装均受保护；槽分别proposal / approval / unmanaged_correction / reconciliation |
| audit_events命令族 | action=procurement.command.applied/rejected时，必须先查真实procurement_commands(operation_id=target)，actor/status及精确两个字符串kind/status payload匹配；kind=account_create/configure受保护account_command槽；credential_bind/balance_observe保留APP全NULL路径，不能仅按action前缀全部禁写或只信caller payload.kind |
| projection_outbox | kind=cost_recorded或cost_corrected无条件受保护，slot=cost、source_kind=cost_outbox，精确指CostOutboxLink自身，不直指其cause |
| audit_events资金族 | action=billing.settled/billing.released时，若真实target settlement或预分配audit ID属于本次CustomerFinishOutcome，则需reconciliation/settlement槽；billing.profile_blocked的本次audit ID/request/model命中该outcome时需reconciliation/profile_block槽 |
| projection_outbox资金族 | kind=settled/released且真实operation settlement或预分配event ID属于本次CustomerFinishOutcome时需reconciliation/settlement槽；普通原资金入口仍可全NULL |

资金族predicate必须同时从原件真实id/tenant/request归属与新outcome的预分配ID两侧查；不能只相信caller填的request_id/amount、仅查“已插入settlement”，或让更改target避开受保护身份。缺原件时可用已出生outcome的预分配ID核；COMMIT再检查完整双向集合，不能由“暂未查到source”降级为允许NULL。原outcome先于其结果出生，避免先写无G输出后补source。

每一受保护audit/outbox逐值核共同合同规定的actor/tenant_scope或tenant_id/action/kind/target/operation/request/payload/schema_version/created_at；UNIQUE(source_kind,source_id,role)防一槽多件，同一原件不能复用两个槽。new_settlement包括**收费与释放**，settled即使charge=0也有原settlement，released也有billing.released audit与released outbox；pending/existing分支不新造这些原件。

早期guard为SECURITY INVOKER，在原payment_audit_guard/payment_credited_writer等共享BEFORE之前先拒APP受保护伪造；对原payment.* / credited / reserved / billing.reserve及其他普通动作不增W授权、不改变021验证。W通过早期检查后也仍经过原021：必须SELECT(singleton,worker_role)，不能为避环境权限改成owner。

## 6. Reconcile的pending、资金原件与原APP接缝

W需要原private markPending的INSERT/ON CONFLICT分支，不能只授case UPDATE：批准在期限内派生无attempt finalization后，settleRequest(recovery)仍可能因delivery_unknown/usage_unconfirmed返回pending，原markPending会将reservations.status和request_records.billing_status设为准确原值`pending_reconciliation`，并INSERT reconciliation_cases(tenant_id,request_id,reason) ON CONFLICT UPDATE reason；已有case的metering_conflict reason优先保持。候选保留这一固定SQL和原错误/角色优先级，不因case已存在就另造简化资金流程。无attempt派生conflict在Reconcile内部全事务回滚，不提交pending替代审批失败。

APP保留普通客户MarkPending/Settle的相同case reason入口，**不能**修改人工proposal列或新GL。人工propose/approve/release与其finish由W更新GL；普通客户markPending只变原reason、保留已有GL，不冒称它认证case完整历史。普通APP finish只允许原`state<>'approved'`行变closed，其他case字段不变；已approved的case不被finish重写为closed。新pp校验不能把今天case.reason/state全部强制等过去人工source.after，从而阻断这些保留路径；只核历史source的真实结果与其受保护pointer/输入不被替换。

新approve/release的CustomerFinishOutcome同reconciliation step，非第二canonical child。资金层取得实际model/customerPB/ledger锁并再次clock后，才在金额写入前一次INSERT完整finish target，再消费真实settlement/journal/lines/PB/audit/outbox；不能用审批checked_at预测锁等待后的收费资格。普通APP不得插入带G的这些原件，也不能用本次outcome预分配身份写一份全NULL替身。

准确资金SQL与权限：ledger_accounts先INSERT(tenant_id,kind) ON CONFLICT初始化，再按id排序SELECT FOR UPDATE；postLocked INSERT journal及每个实际delta键的line并UPDATE balance，零delta HELD/AVAILABLE也有line。最初锁了REVENUE但最终deadline使charge=0时不写REVENUE effect/line。settlement、Key held/spent、reservation/request、case关闭、recovery job删除、PB finish、billing audit/outbox、raw>R的profile block与审计均不省略。profile block旧row可复用，不能把旧row.request_id改成本请求或添加虚假新pointer。

existing_settlement/pending结果明确无新资金line/settlement/PB finish effect/billing审计outbox；pending仍可发生上述原markPending状态/reason动作，不能写成“整个数据库无mutation”。新case动作和结果companion保留真实前后；原资金全source checker继续核其负责的完整资金历史，采购writer不取得充值、券或支付来源写权。

## 7. 新typed、companion与head列权

新增32张关系均给W与APP `SELECT(ALL_FIXED(t))`，用于根即时核验与外层COMMIT；APP无INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER。P/K/peer无新增权限。下表逐列固定W的INSERT与UPDATE；仅有列名，不含schema合同的类型缩写。所有generated step_kind、前驱ordinal及各类discriminator均已排除写面，即使其他同名step_kind是普通列，也不能一刀切漏授或多授。下表必须与schema合同按表、列名和generated属性双向校验，不能翻译成整表grant；缺列/额外列均安装失败。

| 关系 | W INSERT准确列集 | W UPDATE准确列集 |
| --- | --- | --- |
| pp_operation_roots | id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result | 无 |
| pp_root_steps | root_id,step_no,kind,source_id,created_xid | 无 |
| pp_request_origins | request_id,root_id,step_no,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status | 无 |
| pp_request_heads | request_id,ordinal,last_decision_id,updated_xid | ordinal,last_decision_id,updated_xid |
| pp_attempt_origins | attempt_id,root_id,step_no,created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at | 无 |
| pp_attempt_heads | attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid | ordinal,last_node_id,member_count,member_tail_id,updated_xid |
| pp_attempt_nodes | id,attempt_id,ordinal,previous_node_id,root_id,step_no,step_kind,source_id,created_xid | 无 |
| pp_account_origins | account_id,version,create_operation_id,command_source_id,root_id,step_no,journal_id,created_xid,created_at | 无 |
| pp_account_heads | account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count | ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count |
| pp_account_journal | id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,attempt_id,binding_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,pause_event_id,command_source_id,transition_id,delivery_source_id | 无 |
| pp_account_command_sources | id,root_id,step_no,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id | 无 |
| pp_attempt_delivery_sources | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,mode,cause_source_id,before_delivery_state,after_delivery_state,dispatch_account_id,dispatch_journal_id,dispatch_xid,pb_dispatch_checked_at | 无 |
| pp_incoming_sources | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,input_finalization_id,input_digest,source,source_event_id,usage_status,input_tokens,output_tokens,total_tokens,cached_tokens,reasoning_tokens,protocol_outcome,transport_end_reason,raw_delivery_stage,raw_content_written,finish_reason,observed_at_input,effective_observed_at,reconcile_source_id | 无 |
| pp_meter_choices | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,incoming_source_id,meter_event_id,meter_origin_id,disposition,attempt_cut_before_id,attempt_cut_after_meter_id,collision_kind,competing_meter_id | 无 |
| pp_meter_origins | meter_event_id,incoming_source_id,root_id,step_no,source_id,created_xid | 无 |
| pp_conflict_sources | conflict_id,root_id,step_no,source_id,created_xid,tenant_id,request_id,attempt_id,incoming_source_id,meter_choice_id | 无 |
| pp_request_decisions | id,root_id,step_no,created_xid,tenant_id,request_id,ordinal,previous_decision_id,incoming_source_id,meter_choice_id,before_latest_id,after_latest_id,before_status,after_status,normalized_delivery_stage,normalized_content_written,branch,finalization_id,conflict_source_id | 无 |
| pp_normal_cost_decisions | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,before_cost_status,before_cost_amount,management_mode,before_procurement_revision,before_procurement_state,before_procurement_held,before_procurement_spent,before_procurement_anomaly,member_count_before,member_tail_before,account_id,account_ordinal_cut,account_tail_cut,finalization_id,incoming_source_id,meter_choice_id,decision,selected_amount,selected_basis,cost_mutation_id,cost_recorded_outbox_id | 无 |
| pp_cost_mutations | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id,before_status,before_amount,after_status,after_amount | 无 |
| pp_cost_reset_decisions | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id,procurement_state_before,policy,cost_mutation_id | 无 |
| pp_evidence_choices | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,incoming_source_id,meter_choice_id,evidence_digest,procurement_before_revision,procurement_before_state,procurement_before_held,procurement_before_spent,procurement_before_anomaly,account_id,account_ordinal_cut,account_tail_cut,disposition,identity_attempt_id,identity_digest,identity_event_id,member_count_before,member_tail_before,member_count_after,member_tail_after,forced_conflict,effective_conflict,conflict_cause,decision,existing_dispute_event_id,procurement_transition_id,procurement_event_id,account_journal_id,skip_reason | 无 |
| pp_evidence_members | id,attempt_id,member_ordinal,previous_member_id,identity_digest,identity_event_id,evidence_choice_id,root_id,step_no,source_id,created_xid | 无 |
| pp_procurement_transitions | id,root_id,step_no,created_xid,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,cause_kind,cause_source_id,event_id,journal_id,event_type,event_key,evidence_ref,held_delta,spent_delta,before_revision,before_state,before_held,before_spent,before_anomaly,after_revision,after_state,after_held,after_spent,after_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis | 无 |
| pp_proposal_births | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,proposal_id,requester,requester_role,requester_status,reservation_revision,reservation_state,reservation_held,reservation_spent,reservation_anomaly,member_count,member_tail,account_id,account_ordinal_cut,account_tail_cut,audit_id | 无 |
| pp_approval_consumptions | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,proposal_id,proposal_birth_id,approval_operation_id,requester,requester_role,requester_status,approver,approver_role,approver_status,consumed_revision,consumed_state,consumed_held,consumed_spent,consumed_anomaly,consumed_member_count,consumed_member_tail,before_cost_status,before_cost_amount,account_id,account_ordinal_cut,account_tail_cut,procurement_transition_id,procurement_event_id,provider_correction_id,cost_mutation_id,outbox_id,audit_id,after_revision,after_state,after_held,after_spent,after_anomaly | 无 |
| pp_unmanaged_correction_sources | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,operation_id,provider_correction_id,input_actor_id,effective_audit_actor,amount,basis,evidence_ref,before_cost_status,before_cost_amount,after_cost_status,after_cost_amount,cost_mutation_id,audit_id,outbox_id | 无 |
| pp_recovery_sources | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,checked_at,request_deadline,procurement_before_revision,procurement_before_state,procurement_before_held,procurement_before_spent,procurement_before_anomaly,delivery_before_state,permit_source_id,decision,procurement_transition_id,delivery_mutation_id,procurement_event_id,account_journal_id | 无 |
| pp_cost_outbox_links | id,root_id,step_no,created_xid,tenant_id,request_id,attempt_id,cause_kind,cause_source_id,outbox_id | 无 |
| pp_reconciliation_sources | id,root_id,step_no,created_xid,tenant_id,request_id,case_id,action,actor,actor_role,actor_status,input_usage_status,input_input_tokens,input_output_tokens,input_total_tokens,input_cached_tokens,input_reasoning_tokens,input_evidence_ref,input_reason,before_case_reason,case_created_at,before_case_state,after_case_state,before_reservation_status,before_requester,before_approver,requester_role,requester_status,after_requester,after_approver,case_usage_status,case_input_tokens,case_output_tokens,case_total_tokens,case_cached_tokens,case_reasoning_tokens,case_evidence_ref,case_proposal_reason,approval_checked_at,reservation_deadline,prior_latest_id,derived_incoming_source_id,finalization_id,customer_finish_outcome_id,audit_id | 无 |
| pp_customer_finish_outcomes | id,root_id,step_no,source_id,created_xid,tenant_id,request_id,reconciliation_source_id,case_id,latest_finalization_id,customer_reservation_id,reservation_amount,reservation_deadline,result_kind,selected_mode,selected_reason,settlement_decision_checked_at,financial_checked_at,before_case_state,after_case_state,before_case_reason,after_case_reason,before_reservation_status,after_reservation_status,before_request_status,after_request_status,before_billing_status,after_billing_status,before_key_held,before_key_spent,after_key_held,after_key_spent,raw_amount,charged_amount,waived_amount,result_status,settlement_id,customer_journal_id,customer_tenant_control_id,customer_key_control_id,billing_audit_id,billing_outbox_id,profile_block_model_id,profile_block_profile_version,profile_block_disposition,profile_block_audit_id | 无 |
| pp_customer_finish_ledger_effects | finish_outcome_id,root_id,step_no,source_id,created_xid,tenant_id,account_id,account_tenant_id,account_kind,before_balance,after_balance,signed_delta,journal_line_id | 无 |
| pp_customer_finish_pb_effects | finish_outcome_id,root_id,step_no,source_id,created_xid,scope,control_id,allocation_id,event_id,original_source_kind,original_source_id | 无 |

request/attempt head的INSERT与每次UPDATE均必须写真实top事务`updated_xid`，不是DEFAULT掩盖未推进的诊断时间；account head同理。所有head更新必须同时验证OLD、准确next source/node/journal和实际原投影；不授DELETE，不使用UPSERT覆盖不可变source。根最后一次完整INSERT，immutable source只INSERT一次；nullable列不能省去来源责任。新guard可固定DB stamp，但公开函数不接caller before/count/tail/target参数。

ledger effect同一finish可有多个account行，原journal_lines的G也可同source/role=ledger_line；其精确身份由`(finish_outcome_id,account_id)`、真实`journal_line_id`及原`(journal_id,account_id)`闭合。**只有audit/outbox采用逐槽UNIQUE，不把该索引复制到所有G原件。** profile block的created/reused在finish精确记录，不为旧行加本次来源pointer。

## 8. 函数有效权限与触发顺序

### 8.1 APP公开根、W私有闭包

APP仅新增以下十个业务根EXECUTE，准确参数顺序/类型与共同/账户/成本合同一致；标注族不允许任意重载：

```text
pp_create_account(text,text,text,text,text,bigint)
pp_configure_account(text,text,text,bigint,text,text,text,bigint)
pp_record_attempt(text,text,integer,text,text,text,text,jsonb,boolean,bytea,bigint,bigint,bytea)
pp_permit_dispatch(text,text)
pp_record_finalization(text,text,text,text,text,text,text,bigint,bigint,bigint,bigint,bigint,text,text,text,text,boolean,timestamptz)
pp_propose_procurement_cost(text,text,text,text,bigint,text,text,text)
pp_approve_procurement_cost(text,text,text)
pp_record_unmanaged_cost_correction(text,text,text,text,text,text,bigint)
pp_recover_procurement_attempt(text,text)
pp_reconcile(text,text,text,text,text,bigint,bigint,bigint,bigint,bigint,text,text)
```

根SECURITY DEFINER由W拥有，VOLATILE/PARALLEL UNSAFE，非STRICT；SQL NULL按封闭输入联合显式拒绝/接受，不能静默无结果。APP/P/K/peer/PUBLIC无私有mutation EXECUTE。W自身因owner取得私有helper执行能力；私有业务helper准确选择以下三项，并在最终catalog固定全部传递调用：

```text
pp_complete_attempt_birth(text,text,text)
  -> TABLE(root_id text,management_mode text,account_id text,
           reserved_event_id text,account_journal_id text,admitted_at timestamptz)
pp_account_effect(text,text,text,text) -> TABLE(event_id text,journal_id text)
pp_apply_supplier_pb(text,text,text) -> void
```

三参complete_birth只处理公开pp_record_attempt同根刚受控INSERT且已有真实pp_quote_selected_at的attempt；已有origin拒绝，不能登记任意旧行。它完成managed/unmanaged来源与全部效果闭合后，**最后seal root再返回私有结果**；外层公开根随后只重建原Attempt及typed返回，不再增加来源、修改效果或第二次seal。私有root_id返回不等于已提交发送许可。此helper保留为固定函数，不与内联形态二选一。其他append、原件写入、来源捕获及seal的完整函数集合仍须在可执行材料逐签名固定；不能将“同owner任意函数”当实现闭包，未列新helper先审计正文及调用图再增加受审名单。

W需要身份codec六个准确签名EXECUTE（固定17/7参数沿[字节合同](b-procurement-identity-contract.md)），包括互调的pp_json_string_v1/pp_usage_json_v1；不能只授最外digest后漏内部调用。APP的pp只读validator若调用codec，同样需六个执行权限；这些纯函数不能发行source或写业务。

原非PB普通调用新增明确闭包：`valid_procurement_command_snapshot(text,jsonb)`及其内部`valid_channel_batch_revision(text)`；前者由原procurement_commands CHECK实际调用。候选撤这两个函数PUBLIC EXECUTE后给W与APP精确补授，保留原绑定/观察/渠道批量CHECK依赖；不把RETURNS trigger函数直接执行权误当必要。PB十三项、APP保留PB16项+B-P1 append及原018 pricing_time的有效基线按PB矩阵登记，不能声称整个schema PUBLIC EXECUTE已清零。内建pg_catalog事务/UUID/hash/JSON/金额/日期/锁函数逐签名登记实际依赖，不授系统表写权或pg_read_all_data。

### 8.2 新pp只读校验普通函数

候选选择以下只读SECURITY INVOKER函数，供W即时核验与APP外层COMMIT中trigger wrapper显式调用；名字为后续材料的固定接口，不表示现已创建：

```text
pp_validate_root(text)
pp_validate_request(text)
pp_validate_attempt(text)
pp_validate_account(text)
pp_validate_original(text,text,text)
```

参数只是已有root/自然ID；最后函数的三个text依序为封闭tag、key1、key2，单键时key2必须SQL NULL；不是表名、拼接ID或动态SQL。函数仅SELECT、纯计算和固定异常，**无DML、锁、origin补写、head推进、财务effect、调用mutation helper或授权**；使用新statement读取当前闭合图，不返回可提交凭证。APP/W获精确EXECUTE和§9只读面，PUBLIC/P/K/peer无EXECUTE。validator及wrapper VOLATILE/PARALLEL UNSAFE、固定schema路径，校验结束不产生持久结果；不能用caller passed/root存在替代完整枚举。

`pp_validate_original`的tag使用下表封闭硬编码分支；trigger wrapper传实际NEW/OLD对应自然键，未知tag、缺key、单键多填key2均拒绝。tag不转换regclass、不插入格式化SQL。复合原件不采用串接、JSON framing或“生成一个猜测ID”代替原键；查询同时核实际原主键和source保存的输出ID。

| tag | 固定原关系 | key1 / key2 |
| --- | --- | --- |
| request | request_records | id / NULL |
| attempt | upstream_attempts | id / NULL |
| account | procurement_accounts | id / NULL |
| procurement | procurement_reservations | attempt_id / NULL |
| procurement_event | procurement_budget_events | id / NULL |
| procurement_command | procurement_commands | operation_id / NULL |
| meter | metering_events | id / NULL |
| meter_conflict | metering_event_conflicts | id / NULL |
| finalization | finalization_records | id / NULL |
| evidence_identity | procurement_evidence_identities | attempt_id / evidence_digest |
| proposal | procurement_cost_proposals | id / NULL |
| correction | provider_cost_corrections | id / NULL |
| reconciliation_case | reconciliation_cases | id / NULL |
| settlement | settlements | id / NULL |
| customer_journal | journals | id / NULL |
| journal_line | journal_lines | journal_id / account_id；同时核真实id=ledger effect.journal_line_id |
| audit | audit_events | id / NULL |
| outbox | projection_outbox | event_id / NULL |
| profile_block | billing_profile_blocks | model_id / profile_version；允许多个finish复用 |

该只读分派核原件与其来源的双向关系，不把今天可变投影永久固定为旧source.after。需要当前request/attempt/account末项时，必须调用相应域validator核head/完整前缀；case只认证最近人工pointer及受保护proposal，不把自动reason/state变化当篡改。普通资金原件G全NULL仍沿原资金source边界，不凭本函数赋予采购来源；profile block检查也不将复用行归属改成本次request。

### 8.3 实际trigger顺序

新trigger名/时点纳入schema精确catalog。选用前缀`aa_pp_`与`zz_pp_`实现以下顺序义务，最终以实际PG按名称排序核验，不能仅凭注释：

| 原表/事件 | 必须时序与安全模式 |
| --- | --- |
| request_records INSERT | aa_pp_request_write为invoker先核APP初态/无caller pointer → 原pb_request_stamp与request_retail_snapshot各按原顺序运行 → AFTER的W-definer request出生capture → 原/新deferred完整性；AFTER不认证未执行的客户reserve |
| request_records UPDATE | aa_pp_request_write先拒APP latest/身份/出生变更并核保留状态写面；原018/020不变；zz_pp_request_result只核准确NEW/本动作pointer，不补before |
| upstream_attempts INSERT/UPDATE | aa_pp_attempt_write先核current_user=W、实际APP session、允许列与初态/真实source → 原immutable_route_decisions/pb_attempt_stamp/pb_dispatch_guard → zz_pp_attempt_result核PB实际checked_at与pointer/NEW；私有出生closure在真实INSERT后同root，非AFTER猜分类 |
| procurement account/p/PE/command/identity/proposal | aa_pp_* invoker先拒APP受保护DML（command保留两kind、account仅行锁无UPDATE）→ 原014 identity/revision及020 stamp/guard → zz_pp_*核source/pointer；原budget balance deferred保留 |
| audit/outbox及Reconcile资金原件 | aa_pp_* invoker先按§5/6拒越权/NULL伪装 → 原021 payment/funding/line guard → zz_pp_*精确原件与slot；所有原/新deferred保留 |
| 新origin/source/step/journal/companion | BEFORE invoker核W、固定kind/NULL/DB stamp和封闭来源，不从终态补before；immutability禁止UPDATE/DELETE/TRUNCATE；AFTER/deferred只读核两侧关系 |
| 新head | BEFORE invoker核W及完整OLD→NEW单步推进；DELETE/TRUNCATE禁止；deferred与真实末项/当前投影核一致 |
| W锁权父表UPDATE | aa_pp_parent_lock_only invoker对W全部拒绝；普通SELECT行锁不触发。APP原管理分支仍原guard |

自动触发不要求运行角色直接EXECUTE RETURNS trigger函数；安装时O有CREATE TRIGGER/TRIGGER权限，运行时的关系权限及显式普通函数EXECUTE仍检查。撤自动函数EXECUTE不能代替撤DML。request唯一受限definer自动capture必须有精确实际session/输入/根模板校验；其他早期鉴权guard不能改为definer隐藏真实current_user。原trigger不得禁用、重排或改为owner来通过权限测试。

## 9. APP COMMIT的完整只读闭包

根返回不代表deferred已经执行；本候选不引入SET CONSTRAINTS flush协议。根内即时trigger由W执行，排队约束在外层APP COMMIT执行时以APP身份访问。APP需完整的只读校验面，不能因提交缺权限恢复其受保护DML，不能将validator改为有owner绕过能力。

| 新pp延迟入口 | APP必要SELECT与普通调用 |
| --- | --- |
| root/step封闭与typed双向联合 | 所有pp_*来源/step/head/journal/node/member/finish companions的ALL_FIXED；调用pp_validate_root，逐discriminator核唯一child、范围与同root/xid、精确模板和反向原件，不读业务秘密 |
| request/attempt birth与最新投影 | request_records/reservations/upstream_attempts的ALL_FIXED；原meter/conflict/finalization、全部request/attempt pp链，quote来源cost_versions与相关绑定/route冻结列；调用pp_validate_request/attempt；不得补origin |
| account/configuration/PE/成员/批准 | accounts/p/PE/commands/identities/proposals/corrections及全部账户/成本typed历史ALL_FIXED；真实binding只读列；调用pp_validate_account/attempt，完整前缀、桶/blocking/成员/消费cut匹配 |
| 原件G/GA/GL及共享audit/outbox | 原件与audit/outbox ALL_FIXED、对应source/finish/slot，调用pp_validate_original；部分NULL/假legacy/错slot/漏反向源均失败 |
| Reconcile finish/ledger/PB | case/reservation/request/Key计数、settlement/journal/line/account/block原件及finish companions；tenant/key PB及B-P1原读闭包；逐line/billing原件/PBscope精确闭合，不认证其他资金历史 |

上述全读授权只包含本页已列非秘密原件与pp物理列；users只需id/platform_role/status等角色列，channel_credentials不需要secret，APP原有应用读取不等于给validator新增secret依赖。validator不执行FOR SHARE/UPDATE，因此只读函数不新增锁用UPDATE列。

同时保留原约束实际APP闭包：014账户/p/PE守恒，002 journals/lines的平衡与真实tuple检查，020 customer/supplier/configuration检查，B-P1 origin/head/journal/window检查，以及021正常共享资金排除链。PB矩阵已明确configuration_versions与environment(singleton,worker_role)不可漏；普通非funding settle/release不需W读取credit/voucher/payment全表，也不需授funding_journal_valid/payment_legacy_funding_journal_valid。原APP资金入口本来需要的执行/读面继续原界，不把它复制给W。

APP的有效SELECT与EXECUTE也须考虑继承/PUBLIC/函数owner；P/K缺少pp读取不能借COMMIT wrapper转为高权。实际COMMIT前SET ROLE不得借其他角色完成这组来源；根固定实际session，原件关联实际APP OID。完整候选须证明根内、根返回后、显式SET CONSTRAINTS（若caller主动尝试）及真实COMMIT均不产生privilege bypass或遗漏；没有声称未运行的APP提交验收通过。

## 10. 精确profile、失败与退出条件

新catalog枚举每个表/列ACL、effective角色闭包、默认ACL、全部普通/trigger函数签名/owner/proconfig/源码hash、trigger名称/时点/约束可延迟属性、类型与生成列、schema/database CREATE、系统预置角色。授予表SELECT或writer函数所有权也是权限，不能只比较显式acl数组漏PUBLIC/owner。新表使用TEXT UUID及内建scalar，无序列授权需求；若实现引入identity/sequence或业务composite返回类型，先增加准确USAGE及catalog，不授ALL SEQUENCES/TYPES兜底。

actual APP LOGIN正例须覆盖完整RecordAttempt quote时点/无消费expected bytes、所有birth/dispatch/finalization normal+conflict、managed/unmanaged、多根、零价/同额、approve/maker失权、全部Reconcile动作和pending/新settlement/释放/profile复用、普通客户Reserve/Settle/Release/Refund、PB首配/import/Reader、绑定/余额观察，以及原邮件/支付/券共享outbox不被误拦。W自身不得LOGIN；owner/SET ROLE和只有纯helper的fixture不替代这些组合。

逐项撤必要grant验证即时与COMMIT都全回滚：父行锁UPDATE列、command CHECK内部EXECUTE、codec内部EXECUTE、case INSERT/冲突reason更新、recovery_jobs DELETE、ledger初始化/line INSERT、pointer列、新head UPDATE、新validator EXECUTE/SELECT，以及PB矩阵每项隐藏依赖。反例包括APP raw初态/quote/latest/cost/源/G/GA/GL伪造、W写父身份/配置PB/秘密、同xid跨根混绑、全NULL protected原件、先资金变更后补before、trigger/owner/default ACL/temp/search_path/role路径、statement/savepoint/deferred失败、取消/EOF/Close/未知COMMIT。

维持原3秒锁/5秒statement与原上层期限，只有明确40P01/40001的已回滚事务按原有界策略重试。COMMIT回复未知仅按准确operation/root只读核对，不重新发模型或盲重做财务。缺权限不切回旧writer，不放宽grant或期限来得到PASS；变更SQL形状先更新本矩阵并重新审计全部传递调用。

本文只交付候选全ACL设计，不据此关闭D1可执行DDL/完整锁图/返回错误映射、独立纯重放/固定读集/basis/catalog/profile及actual LOGIN验收。B-P3十一项历史缺证、catalog race门禁、R2正式迁移/钱包接线、真实供应商/支付、容器/1Panel、HA与性能分别保留。统一架构和manifest由集成人在全部并行结果汇合后复核同步。

# B-P4-A1：真实 request 出生的必要授权逐项失权合同

2026-11-04；源码基线 `71de18a`。承接[真实 request writer 合同](b-procurement-request-birth-writer-contract.md)、[最小 exact-link 装配](b-procurement-birth-exact-link-integration-contract.md)及[当前窄验证记录](../../docs/verification/procurement-request-birth.md)。本片先冻结现有 A1 的权限故障验收，再新增独占测试 `internal/procurementrequestbirth/request_birth_grant_fault_postgres_test.go`；不改 SQL、材料摘要、原 fixture、业务 API、迁移或在线权限。**本合同及未执行的测试不构成 PostgreSQL PASS 证据。**

核对的 [writer SQL](../../internal/procurementrequestbirth/materials/request-birth-writer-v1.sql) SHA-256 为 `325910df461aac3d8d068eb078bac08c18e75741765ee79aa316d5107373db4b`，[权限 SQL](../../internal/procurementrequestbirth/materials/permissions-v1.sql) 为 `32464fd4a1e01ebcff8c2c2793fb334e8be3d20be11e5e6497a0d785d6cfe0ef`。不通过改签材料、替换函数、补写未使用列、禁用 trigger 或省略原 018/020 来制造失权。

## 1. 真实入口、角色与固定范围

复用[既有隔离 fixture](../../internal/procurementrequestbirth/request_birth_postgres_test.go)：仅显式 PG16 专库 `127.0.0.1:55439/moni_test`、安装 LOGIN `moni_test`、随机 schema、实际 APP/peer LOGIN、W NOLOGIN。APP/W 均 NOINHERIT、无超管/BYPASSRLS/建库建角色/复制能力与 membership；W 不是 schema/关系 owner、无 CREATE。七函数仍 W owned，唯一 definer 为实际 AFTER capture。

正常 baseline、每项失权及恢复探针均使用真正 APP 连接调用**原 `billing.Store.Reserve`**，不复制 Reserve SQL、不开 owner 业务事务、不 SET ROLE、不直接伪造 source。原 API 自己开始 RC 事务，维持 lock 3s / statement 5s 并结束 COMMIT/ROLLBACK；本测试不注入成功 stub 或外部事务包装来替代原入口。`errors.As` 必须取得实际 `*pgconn.PgError{Code:"42501"}`，包括真实 APP COMMIT 的 deferred 失权；不把后续 `25P02` 或 `ErrTxCommitRollback` 算作原因。

owner 在最初准备足够的原客户资金 baseline，使用原核款/审批路径，确保数百次恢复探针不会被余额不足掩盖；不为故障改价格/预算/身份或调用出生 writer。之后 owner 仅准确 REVOKE/恢复本合同授权、只读观察与最终清理。每次恢复正控制是真实 Reserve **COMMIT**，并独立查完整出生和手算预占 120；先完成失败的全行不变断言，再运行下一次成功探针，不把成功产生的新历史混入失败 baseline。

本片覆盖 A1 显式新增写面和五关系只读闭包；不声称已覆盖原 Reserve 全部管理/账本/PB 权限、schema USAGE、REFERENCES、全域 catalog/ACL、供应商/支付/生产。schema USAGE 的查找缓存语义另验，不能用同会话缓存通过冒称必要授权。

## 2. 固定列与必要授权清单

下列列表由冻结 SQL/原 Reserve 列表确定，不能按当前有权列筛选、发生错误后换列或静默跳过。显式写 NULL 仍是 INSERT；`SELECT *` 要求每个物理普通列，包括 generated 列，但不自动授权系统列。

### 2.1 W 的 52 个 INSERT、3 个 pointer UPDATE、3 个锁列

| 关系 / 权限 | 固定列 | 数量 |
| --- | --- | ---: |
| `pp_request_origins` INSERT | `request_id,root_id,step_no,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status` | 32 |
| `pp_request_heads` INSERT | `request_id,ordinal,last_decision_id,updated_xid` | 4 |
| `pp_root_steps` INSERT | `root_id,step_no,kind,source_id,created_xid` | 5 |
| `pp_operation_roots` INSERT | `id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result` | 11 |
| `request_records` UPDATE | `pp_origin_root_id,pp_origin_root_step,pp_origin_xid` | 3 |
| 三表锁 UPDATE | `pp_request_origins(request_id)`、`pp_request_heads(request_id)`、`pp_root_steps(root_id)` | 3 |

原 root(id) 没有实际 SHARE 路径，现无其 UPDATE grant，不制造该项失权。后三项是每张相应表唯一的 W UPDATE 列权，分别服务真实父 `FOR SHARE`；撤任意一项必须阻止实际出生。它们只证明行锁能力，四表真正 UPDATE（包括赋原值）仍由原 guard 拒绝。request 三 pointer UPDATE 各自撤权仍可能留下另外两列作为行锁能力，失败归因于完整 SET 缺列，不能声称单列证明了该表唯一锁门。

### 2.2 APP 的原 21 列 request INSERT

`id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at`。

不把 `status,billing_status,latest_finalization_id,created_at,pb_*,pp_*` 的 DEFAULT/trigger 赋值当成 APP INSERT，也不扩大 A1 的两列普通资金 UPDATE。pending/finish 写面由独立预算/资金兼容矩阵覆盖。

### 2.3 APP 与 W 各 104 个普通 SELECT，APP 另有 xmin

| 关系 | 固定普通列 | 每角色数量 |
| --- | --- | ---: |
| `request_records` | `id,tenant_id,key_id,owner_id,model_id,status,billing_status,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,latest_finalization_id,created_at,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_last_decision_id` | 33 |
| `pp_request_origins` | §2.1 的 32 非 generated 列加 `step_kind` | 33 |
| `pp_request_heads` | §2.1 四列 | 4 |
| `pp_operation_roots` | §2.1 十一列 | 11 |
| `pp_root_steps` | §2.1 五列，加 `d_request_birth,d_attempt_birth,d_account_command,d_attempt_delivery,d_incoming,d_meter_choice,d_request_decision,d_normal_cost_decision,d_cost_mutation,d_cost_reset_decision,d_evidence_choice,d_procurement_transition,d_proposal_birth,d_approval_consumed,d_unmanaged_correction,d_recovery,d_cost_outbox,d_reconciliation` | 23 |

APP deferred 与 W immediate/guard 路径均实际使用相应 `SELECT *`；例如 17 个 inactive step generated 槽目前不参与域逻辑，但仍被 `SELECT *` 读取，属于必要 SELECT，不是 generated INSERT。APP 的 `request_records(xmin)` 则由原 `periodicbudget.readCustomer` 显式读取，必须另撤、另核有效失权，不能由普通 33 列推断。W 不新增 xmin 授权或测试。

此外原 ACL 给 W `periodic_budget_allocations(subject_type,subject_id)` 两个 SELECT，但**当前真实 pointer UPDATE 不需要它们**。首次根 PG 按原冻结 293 假设执行时，这两个用例确实把有效权限从 true 变成 false，原 Reserve 却成功 COMMIT，故必要失权断言失败。原 020 `pb_stamp_request` 的 allocation 子查询位于 `(NEW.id,NEW.tenant_id,NEW.key_id) IS DISTINCT FROM OLD` 的 `AND EXISTS` 后；A1 pointer UPDATE 不改变该身份组，PL/pgSQL 在此真实路径短路，没有执行 allocation 查询。不能用“语句文本提到表”冒称真实读依赖。

根已根据这次实际失败审定两项改为**固定、独立的未使用 grant 正控制**：逐列撤 W SELECT、确认 false，actual APP 原 Reserve 仍须 COMMIT、完整 checkBirth 与手算 120 资金变动通过，恢复 true 并准确还原全部表/列 ACL，再执行恢复后新请求。它们不进入必要失权数量，也不证明原 020 其他会改变身份的分支无需读权。SQL、原 grant 及摘要保持不变；不补一次人为查询把它们变成必要。

### 2.4 APP 两个普通 EXECUTE

`pp_rb_validate_request_v1(text)`、`pp_rb_validate_root_v1(text)` 分别撤 APP 显式 EXECUTE。二者 W owned、APP 非 owner、PUBLIC 已撤；故可验证 true→false→true。实际 Reserve 允许 immediate W 阶段运行，最终 APP deferred 的对应普通调用必须以原 `42501` 失败并全回滚。不要对 trigger 函数的直接 EXECUTE 作同样假设。

## 3. PUBLIC 传递权限与不能伪计的控制

### 3.1 原 pb_require_read_committed() 的真实授权来源

原 020 `pb_require_read_committed()` 由 O 持有，保留 PUBLIC EXECUTE；A1 又给 W 显式 EXECUTE。直接撤 W 的 grant **不会**使 W 有效权限 false。本片固定两个相互独立场景，均完整恢复原函数 ACL：

1. **冗余直接 grant 正控制**：只撤 W 直接 EXECUTE，核 W 有效权限仍 true，APP 原 Reserve 必须 COMMIT 且来源正确；恢复后函数 ACL 与基线一致。此场景不计必要授权失权，也不说函数未被使用。
2. **W 实际 EXECUTE 失权**：原 ACL 快照后，测试暂时给 APP 显式 EXECUTE 并撤 PUBLIC EXECUTE，使原 APP 的前置 PB 调用仍有效且 W 只剩原明确 grant；再撤 W EXECUTE。核 APP=true、W=false，由实际 Reserve 在 W 内 pointer UPDATE 的原 020 调用返回 `42501`。恢复 W、PUBLIC，撤临时 APP grant，核 APP/W 都回 true，并比较包含所有 grantor/grantee/privilege/grant option 的完整原函数 ACL。临时隔离是故障 fixture，不写入生产权限材料，不将其冒称“原本只撤 W 就失权”。

### 3.2 W owner 与自动 trigger

七个 A1 函数均 W owned；本片不通过对 owner 自己 REVOKE 去宣称普通 grant 已有效丢失，不改变函数 owner 或 membership 制造失权。owner 权利与函数 ACL 后续全 catalog 独立评估，不能和 APP 可撤的两个普通 EXECUTE 混算。

APP 对五个 trigger 私有函数（request_write、capture、source_write、shared_write、deferred）的有效直接 EXECUTE 均为 false；原 Reserve 的正常 baseline 仍完整 COMMIT，这是自动 trigger 不要求运行时直接 EXECUTE 的真实控制。没有 true→false 转换的项不纳必要撤权计数。不得因为触发器仍运行而临时给 APP 私有 EXECUTE。

## 4. 每项故障状态机与准确恢复

1. 完成管理种子/足额原核款和至少一个实际 APP 已 COMMIT 出生。核角色边界、原函数 owner/security/search_path、表/列类型与 generated 信息，验证静态固定清单与安装 catalog 相符。读完整 18 表数据快照与目标对象的**全部** ACL，包括其他 grantee/列/系统列，不能只读目标一条授权。
2. 用 `has_column_privilege` / `has_function_privilege` 验证目标有效权限为 true；目标表不是被撤权角色所有。按本合同准确撤单列或单普通函数，真实提交后核 false；若表级/PUBLIC/继承/其他 grantor 仍授权，失败并报告，不 SKIP、不另撤其他面。只有 §3.1 的明确 PUBLIC 隔离场景允许其列明的额外临时权限。
3. actual APP 以新 request/idempotency UUID 调原 Reserve，不能返回成功 Reservation。保存并确认原 `42501` cause；API 内独立有界 cleanup 完成 ROLLBACK。失败之后的 aborted COMMIT 不是证据。
4. owner 在独立有界 cleanup context 只恢复原确切 grant，核 true，再对表级及每列（含 xmin）的 canonical ACL 比较：owner、grantor、grantee、privilege、grant option 均须相等，顺序不作语义差异。函数场景同核完整 proacl。不得重新 provision、授表级权、换 owner 或重建角色。即使断言失败也注册幂等独立 cleanup。
5. 比较第 1 步的 18 表每个完整行、每个字段，包括已提交旧根、生成列、NULL、时钟、xid 和原账本/PB 行；另核失败 request/idempotency/source/head/root/step 无任何本次残留。先完成该断言，再运行恢复后的新 UUID 真实 APP Reserve 正控制并 COMMIT、独立 checkBirth 和手算金额 120。下一项以此新增合法历史作为新 baseline。

18 表固定为既有 snapshot 的：`request_records,reservations,request_idempotency,api_keys,ledger_accounts,journals,journal_lines,periodic_budget_controls,periodic_budget_windows,periodic_budget_allocations,periodic_budget_events,recovery_jobs,audit_events,projection_outbox,pp_operation_roots,pp_root_steps,pp_request_origins,pp_request_heads`。一次独立 owner SQL 取得全行快照，失权时不能依 APP 缺读权观察。资金/Key held、不可变来源、PB、审计/outbox 与恢复队列全都属于回滚要求；只比本次 root 数量不足。

准确失权、原错误、18 表全行回滚和完整 ACL 恢复四项同时成立才算该项通过。COMMIT 未知不重放；本片只处理已知 PostgreSQL 失权并确认回滚的失败，恢复探针总用新 request。

## 5. 固定计数与退出

| 必要失权组 | 逐项 fault 调用 |
| --- | ---: |
| W INSERT 52、pointer UPDATE 3、锁 UPDATE 3 | 58 |
| APP INSERT | 21 |
| W 五关系普通 SELECT | 104 |
| APP 五关系普通 SELECT＋xmin | 105 |
| APP 两个普通 EXECUTE | 2 |
| 明确 PUBLIC 隔离后的 W pb_require EXECUTE | 1 |
| 合计 | **291** |

另有两个 allocation SELECT 的有效 false / Reserve 成功正控制、一个“只撤 W pb_require 直接 grant、有效仍 true”的冗余 grant 正控制，以及私有直接 EXECUTE=false 的真实自动 trigger baseline；它们不加进 291。每项必要失权后还有恢复 Reserve COMMIT 正控制。计数源于固定 SQL，不由运行时 catalog 筛选或失败数量倒推；纯测试需核固定写/读列、计数、无重复、原 21 列和材料摘要，源变化即提示重新审合同。

首轮根日志 `.local/moni-request-birth-grant-first-pg.json` 保留上述两项失败，其余 290 个普通必要授权及 PUBLIC 隔离 W EXECUTE 分支已运行通过；不能把原失败日志改写为全部 PASS，修订后完整结果另记。

测试不使用 t.Parallel，不自行执行 PG；集成人在专库串行运行，记录零 SKIP 的真实结果后才更新[验证记录](../../docs/verification/procurement-request-birth.md)及统一架构/变化清单。无数据库的编译/race 只能记纯静态 PASS。本片通过也仅关闭上述权限故障子矩阵；预算/价格/等待、误授后全字段、跨 root/多请求、真实锁竞争/取消、完整 profile/catalog 及 A2–A6 继续按各自合同推进。

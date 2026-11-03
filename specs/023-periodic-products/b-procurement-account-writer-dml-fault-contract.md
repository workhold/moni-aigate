# B-P4-W1：账户 writer 必要 DML 的逐项失权合同

2026-11-04；源码基线 `6c03c15`。承接[账户 writer 合同](b-procurement-account-writer-contract.md)、[账户故障矩阵](b-procurement-account-writer-fault-matrix.md)与[权限方向](b-procurement-writer-authority-contract.md)。本片只冻结现有账户候选的 DML 故障验收，不增加或修改 SQL、trigger、生产 ACL、业务根或正式迁移；**根集成人已审定冻结，实施与 PostgreSQL 结果单独记录，合同本身不构成 PASS 证据**。

核对对象固定为 [account-writer-v1.sql](../../internal/procurementaccountwriter/materials/account-writer-v1.sql) 与 [permissions-v1.sql](../../internal/procurementaccountwriter/materials/permissions-v1.sql)，其原始 SHA-256 分别为 `b00efdf4f28d89a0065dd45779f7f6614a9cf229dda38599c311e11fb9cd1665`、`d4cef1413d34fcd6e4013044b74257a8cc159688c5b9891864c5f9268d76aa53`。不通过改签材料、删掉原额外 grant 或补授权限制造失权结果。

## 1. 真实角色与隔离边界

沿现有 [actual LOGIN fixture](../../internal/procurementaccountwriter/account_postgres_test.go)：只接受显式 `TEST_DATABASE_URL` 指向 PG16、`127.0.0.1:55439/moni_test`，由 `moni_test` owner 安装原 001–022、B-P1、identity、关系材料与账户候选。每个顶层测试使用随机 schema、实际 APP/peer LOGIN、独立 W NOLOGIN；APP/W 均 NOINHERIT、非 superuser、无 BYPASSRLS/CREATE ROLE。没有专库时明确 SKIP，不能写成已验收。

安装后的全部业务调用，包括正常 baseline、失权根与恢复探针，必须来自真正 APP 连接。根以原 SECURITY DEFINER 切换至 W，invoker helper/trigger 保持原身份；禁止 owner 直接调用业务根、`SET ROLE APP/W`、复制根体、替换函数、禁用 trigger 或改变约束时机。owner 在故障阶段只执行本合同的一项 REVOKE/准确恢复，以及只读身份、ACL 与数据观察；最初 fixture 安装/种子和最后清理由原 fixture 负责。

正式执行前核实 W 仍为 NOLOGIN、不是 schema/关系 owner、没有 APP/owner/peer 的成员授权、没有 schema CREATE；APP 实际 `session_user=current_user=APP`。根的 W owner、受信 search_path 与安装时 OID 沿原合同，不以测试新 stub 证明 W 运行。

所有 PG 专项由集成人串行执行，不使用 `t.Parallel`；本合同和后续独占测试不自动加入普通在线权限装配。

## 2. 固定必要写面

矩阵来自实际 SQL 的显式列列表，**不先按当前有权的列筛选**，不把整个 catalog 的所有列当成实际写面。以下每组列逐列展开为独立用例，一次仅撤一个 `(W, relation, privilege, column)`。SQL 显式插入 NULL 的列仍要求 INSERT 权限，不能省略。

### 2.1 INSERT：九表、122 个独立列授权

| 关系 | 固定列（依 SQL 列表顺序） | 数量 | 执行分支 |
| --- | --- | ---: | --- |
| `pp_account_command_sources` | `id,root_id,step_no,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id` | 23 | create、changed configure |
| `procurement_accounts` | `id,label,provider,state,limit_amount,pp_version,pp_created_xid,pp_origin_journal_id` | 8 | create |
| `pp_account_journal` | `id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,command_source_id` | 29 | create、changed configure |
| `pp_account_origins` | `account_id,version,create_operation_id,command_source_id,root_id,step_no,journal_id,created_xid,created_at` | 9 | create |
| `pp_account_heads` | `account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count` | 12 | create |
| `procurement_commands` | `operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id` | 14 | create、changed configure |
| `audit_events` | `id,actor,action,target,sanitized_diff,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid` | 11 | create、changed configure |
| `pp_root_steps` | `root_id,step_no,kind,source_id,created_xid` | 5 | create、changed configure |
| `pp_operation_roots` | `id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result` | 11 | create、changed configure |

### 2.2 UPDATE：15 个真实写列与一个锁列

| 关系 | 固定列 | 数量 | 必要性 |
| --- | --- | ---: | --- |
| `procurement_accounts` | `label,provider,state,limit_amount` | 4 | changed configure 的固定 SET 全部四列 |
| `pp_account_heads` | `ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count` | 11 | changed configure 的固定 SET 全部十一列 |
| `users` | `created_at` | 1 | 两根读取 actor 的 `FOR SHARE`；它是当前 W 在 users 上唯一 UPDATE 列权，不发生真实 UPDATE |

两个 UPDATE SET 都保留原完整列表；即便某一列的新旧值相同，SQL 仍要求该列 UPDATE。changed configure 的输入要同时改变 label/provider/state/limit，以明确覆盖实际应用分支，而不是 revision_conflict、not_found 或 replay。

users 的锁列失权也要求原 `42501`，但它只证明行锁权限，不称为 users 发生业务 DML。W 对 users 的真实 UPDATE 仍由原 invoker guard 拒绝。

## 3. 不可冒充的权限面与依赖

### 3.1 原 grant 多于实际显式写列

当前 journal 获得 54 列 INSERT，而两根实际各只显式插入上表 29 列；其余 **25 列**为：

`attempt_id,binding_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,pause_event_id,transition_id,delivery_source_id`。

它们不是本片两根的必要 DML。单撤某列 INSERT 即使确实使该列有效权限 true→false，也不能期待两根 `42501`；不得修改根补写这些列、修改 guard 或删掉其他原 grant 来让它们成为必要。后续测试将它们作为独立正控制：逐列撤销、确认失权，实际 APP 的 create 与 changed configure 均仍成功执行并显式回滚；恢复后再次确认有效授权。正控制不计入“必要授权失权”数量，也不证明其他未实施根对这些列的接受行为。

### 3.2 行锁是可替代的 UPDATE 前提

account 的 `FOR NO KEY UPDATE` 与 head 的 `FOR UPDATE` 只需该关系某个 UPDATE 列权；单撤其四列/十一列之一，其他原 UPDATE grant 仍足以允许行锁。失败应由后续原完整 SET 缺列权限产生，不能把这类用例写成“单独证明了行锁只依赖此列”。本片不同时撤销其余 UPDATE 来隔离锁门，也不新增 created_at grant 来改变依赖。users 只有单列 UPDATE，因此可单独验证其锁门。

### 3.3 derived/default 与所有权

- 原 account `revision` 由原 014 BEFORE trigger 写 NEW，`held_amount/spent_amount/created_at` 沿原 DEFAULT；command `created_at` 由 source 的真实时钟派生。它们不在上述显式 INSERT/SET 列表，不给 W 新增授权来制造撤权用例。
- `step_kind`、`previous_ordinal` 等 generated 列沿原定义，不生成虚假的写权限项。schema/type USAGE、SELECT/RETURNING、CHECK helper、自动 trigger 与 APP 顶层 COMMIT 的只读传递闭包各有独立责任，不混为 DML 列权限。
- W 是账户函数 owner。owner 隐含 EXECUTE 不能通过对自己的单项 REVOKE 变为 false；不撤 owner EXECUTE 后宣称 W 已失权。自动 trigger 运行也不以直接 EXECUTE 授权代替表 DML。身份 helper 的显式 EXECUTE 撤权属于后续独立矩阵，本片不扩大到全部函数 ACL。
- 原候选的关系均由 O 持有，W 的必要 DML 来自固定列 grant。安装 fixture 后若发现表级、PUBLIC、继承、其他 grantor 或所有权仍让某列有效权限为 true，必须报出实际授权来源并使该固定用例失败；不能 SKIP、改用其他列、撤掉额外授权或放宽断言。静态分析不是有效失权的实际 PG 证据。

## 4. 每项故障的状态机与错误合同

每个用例使用新 operation UUID，独立执行下列顺序：

1. 所有 baseline 业务已由 APP 完整 COMMIT；owner 取得九表全行快照及本次对象 ACL 基线。确认固定目标存在、不是 generated 列、W 不是关系 owner，且 `has_column_privilege(W, qualified_relation, column, 'INSERT'/'UPDATE') = true`。只读核对实际表/列 ACL 和角色闭包，不能仅看到文字 GRANT 就当有效授权。
2. owner 在独立事务准确执行 `REVOKE INSERT(column)` 或 `REVOKE UPDATE(column) ON TABLE schema.relation FROM W` 并提交。没有 APP 业务事务先持有该关系锁；不引入 REVOKE 自己等待业务事务的竞争。立刻用相同 `has_column_privilege` 确认 **false**。只允许这一项权限发生测试故障，不 revoke table-level、PUBLIC、其他角色或其他列。
3. actual APP 新事务，以原 `statement_timeout=5s`、`lock_timeout=3s`、READ COMMITTED 调用原公开 create 或 configure 根。错误必须是根 SQL 原始 `*pgconn.PgError` 的 `Code=42501`，不捕获并改成 PP 成功/业务拒绝；根不能返回成功 receipt。不能把随后 aborted transaction 的 `25P02` 或 COMMIT 的 rollback 错误当成原始失权证据。
4. 以独立有界 cleanup context 明确 ROLLBACK APP 事务、释放连接。即使断言失败也注册幂等 cleanup，避免 fail-fast 留下失权或连接。
5. owner 只恢复准确原 `GRANT INSERT(column)` 或 `GRANT UPDATE(column)` 至 W，再确认有效权限回到 **true**。还原后将关系/列 ACL 用 grantor/grantee/privilege/grant-option 的排序集合与原快照比较；不得整套重新 provision、补授表级权、改 owner、修改角色成员或覆盖原 ACL。
6. owner 读取九表全行快照并逐值比较；另核失败 operation 在原 command 与 typed source 均不存在。必须先完成该回滚断言，再运行任何恢复成功探针。
7. APP 以另一个新 operation 调用同类根，必须返回正常 applied receipt；显式 ROLLBACK 此恢复探针，再确认九表仍等于基线。该探针证明恢复后的原根可执行，不将这个 ROLLBACK 冒称 COMMIT 验收；APP deferred COMMIT 及其失权证据仍引用原 W0 专项。

根 query 的真实 `42501`、有效权限 true→false→true、九表回滚及准确 ACL 还原四项都成立，才算该项通过。REVOKE 成功而 probe 仍 true、只检查 root 数量、或仅看 failed operation 不存在均不足。任何恢复失败均使测试失败，并保留独立清理机会。

已知根 SQL 失败并已确认回滚后，恢复探针始终用新 operation；本片不引入自动业务重试。未知 COMMIT 仍只能只读观察原 operation/root，不重放资金或模型。

## 5. 场景覆盖与固定计数

### 5.1 必要授权：138 个不同 grant、232 个故障调用

测试固定运行两组，不能仅选择一条会失败的分支覆盖共享写面：

| 根场景 | INSERT 逐项 | UPDATE 逐项 | 锁列逐项 | 故障调用合计 |
| --- | ---: | ---: | ---: | ---: |
| create | 九表 122 | 0 | users 1 | 123 |
| changed configure | source 23 + journal 29 + command 14 + audit 11 + step 5 + root 11 = 93 | account 4 + head 11 = 15 | users 1 | 109 |
| 合计 | 215 次 | 15 次 | 2 次 | **232** |

不同必要 grant 为 122 INSERT + 15 真实 UPDATE + 1 锁 UPDATE = **138**。同一 grant 在两根各撤一次是两个场景，不将其重复记成不同 ACL。固定列表、组数和这些计数必须在测试断言；不从成功撤权子集或当前有数据的表动态生成测试集合。

create baseline 至少包含一个已提交的 tracked create 及现有 fixture 的 legacy；每次 create 故障须保持整个原集合，不仅核生成的新账户。configure baseline 由 APP 合法 create，再完成一次真正 changed configure COMMIT 得到可观察的 revision/head/历史；后续每次失权以该已提交账户当前 revision 作为 expected，输入为四字段均不同的固定配置。每次故障及恢复探针均回滚，因此 baseline 的 revision、head 与完整历史在整组内稳定。

除每项恢复探针外，正式矩阵开始前必须以相同场景实际成功 COMMIT，证明它们确实进入相应写分支；receipt 要核 applied、kind、target、非空 root/journal。configure baseline 核配置 revision 与 journal ordinal 确实增加，不能用 rejected/replay 当成功控制。合法输入和明确 actor 保持不变，避免先撞 PP001/PP003/PP906 掩盖失权。

### 5.2 未使用 journal grant 的正控制：25 列、50 次根调用

每列单撤有效 INSERT、确认 false 后，在 APP 两个独立事务中分别执行 create 与 changed configure，要求正常 receipt，然后各自显式回滚；九表都回到本列开始前基线。准确恢复并核 true/ACL 集合相等。它验证“当前两根不依赖该已授权列”这一边界，不计为 `42501` 故障；若真实根失败，则记录真实依赖并先复核合同，不能调整 SQL 让静态预测成立。

本片不覆盖 noop/revision_conflict/not_found/replay 的全部 W 权限最小集；这些分支已有业务行为专项，但不据本片宣称逐项失权齐备。未授 W 的 DELETE/TRUNCATE/REFERENCES、APP 的原业务写路径、其他八类根、原 PUBLIC/default ACL/继承攻击和未知 COMMIT 另验，不混入上述计数。

## 6. 九表原子性、清理与交付边界

完整快照固定为 `procurement_accounts,procurement_commands,audit_events,pp_operation_roots,pp_root_steps,pp_account_command_sources,pp_account_origins,pp_account_heads,pp_account_journal`。沿 [原故障快照](../../internal/procurementaccountwriter/account_fault_postgres_test.go) 的 owner 独立 `to_jsonb(whole_row)` 确定性排序聚合，包含所有物理/生成列、旧 legacy、已提交命令及历史，按每表完整值相等比较。不能只比较计数、root/source 或本次 target；configure 尤其核 account 配置/revision/金额、head ordinal/tail、origin 与全部 journal 未变化。

每个用例的 rollback/restore 使用 fresh cleanup context；矩阵及 fixture 总时限保持有界，不吞掉超时。顶层最终关闭 APP/peer 连接，独立 context 清理随机 schema/角色并读 catalog 确认零残留。清理失败和 ACL 未还原即测试失败，不能靠最后 DROP 掩盖准确授权恢复失败。

后续实现只进入独占 `internal/procurementaccountwriter/account_dml_fault_postgres_test.go`，复用已有 fixture 与完整快照；不修改 SQL 原件、trigger、既有权限材料、旧迁移或在线路径。先由集成人冻结本合同，再授权实现；PG 执行只由集成人串行进行。届时记录实际命令、固定子例计数、PASS/SKIP/FAIL、真实 SQLSTATE、权限往返及清理结果，再统一复核 HLD/LLD、统一架构与验证记录，最后刷新架构清单。

即使本片全部实际通过，也只补账户 create/changed-configure 的必要 DML 与已声明正控制；不关闭 W0 全部故障、I1 完整 writer、A0、新 profile、B-P3 或生产验收。不把 fixture 通过写成供应商、支付、Docker/1Panel 部署或性能证据。

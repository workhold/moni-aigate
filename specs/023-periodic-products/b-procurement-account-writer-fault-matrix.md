# B-P4-W0：账户 writer 的撤权、等待与取消验收合同

2026-11-04；基线 `e73d9e9`。承接[writer v1合同](b-procurement-account-writer-contract.md)与[执行合同](b-procurement-execution-contract.md)。本片扩充已有隔离候选的真实 PostgreSQL 故障证据；不新增业务根、DDL、权限或线上接线，不重签既有SQL材料。

## 1. 真实环境与证明责任

只接受显式 `TEST_DATABASE_URL`、PG16、`127.0.0.1:55439/moni_test`、`moni_test` owner LOGIN。沿原 fixture 为每个顶层测试建立随机schema、actual APP/peer LOGIN和W NOLOGIN；正常业务调用必须由真正APP连接发起，owner仅安装、精确撤/还测试授权和独立观察结果。未配置环境明确SKIP，不能计PG验收。

保留原001–022、B-P1、identity与关系材料、全部普通/deferred trigger。不得disable trigger、替换函数为故障stub、授APP或W额外业务权限来制造通过。所有PG专项由根串行执行，fixture使用独立清理context并核随机schema/角色零残留。

## 2. 逐项撤权与完整回滚

在一个actual APP事务中先完成合法create根，尚未COMMIT；owner随后逐项撤销APP的一项真实必要权限，APP COMMIT必须得到原 `42501` 并全事务回滚。每个子例只撤一项；用有效权限函数确认撤前true、撤后false，不能把仍继承表级权限的列级REVOKE当作失权。结束后以独立context准确恢复原单项grant，不调用整套provision扩大权限。

矩阵固定包含：

- 三个只读validator的EXECUTE，以及其五个pure helper和`pp_json_string_v1(text)`的传递EXECUTE。
- account/command/audit及origin/head/journal/root/source八张 `SELECT *` 表的全部156个实际列，按固定表白名单读取已安装catalog的列序；step固定五个读取列、reservation聚合五列、event聚合三列及outbox反向槽一列。不能先筛掉当前失权列再生成矩阵。
- 根校验器对其余24个前缀族的`root_id`读取闭包；即使表为空也必须保留SQL执行权限，不能按当前无数据跳过。

矩阵合计203项（9个EXECUTE、194个列SELECT），另有4个锁故障子例。计数属于当前冻结材料，不代表全部W写权限矩阵。

每例在撤权前取得业务/evidence九表的确定性完整快照，拒绝COMMIT且恢复grant后再读取并逐值相等：`procurement_accounts,procurement_commands,audit_events,pp_operation_roots,pp_root_steps,pp_account_command_sources,pp_account_origins,pp_account_heads,pp_account_journal`。同时要求新operation在原command与typed source均不存在；不能只看root计数或忽略半写原件。

该矩阵只证明列出的APP COMMIT读取/执行闭包，不宣称W全部DML逐列撤权或其他八个根已经完成。公开create/configure根的执行拒绝沿既有actual LOGIN权限专项。

## 3. 真实等待、锁超时与取消

对operation advisory lock和account行锁各运行两种故障：原`lock_timeout=3s`的真实超时（保留`55P03`），以及等待期间精确取消该测试连接（保留`57014`）。保持前端`statement_timeout=5s`、`lock_timeout=3s`，不缩短/重设它们来替代真实根协议。

第一APP事务完成但不提交root以持锁；第二actual APP会话调用相同operation或同account配置。先用`pg_blocking_pids`确认准确blocked PID与blocker PID，再允许取消或等待超时；固定sleep不充当竞争证据。goroutine、取消请求、连接与事务都必须有界回收，失败也以fresh cleanup context回滚。

operation场景中第一事务最终也回滚，九表必须回到原快照；account场景先有一个已提交合法账户，两个未提交配置最终均回滚，已提交账户配置/revision、head/ordinal、原命令与完整历史保持原快照，失败operation不存在。取消/超时后不自动重发根或模型调用。

## 4. 剩余矩阵

本片不关闭W0完整故障矩阵：全部坏/额外来源、W必要DML/ACL逐项撤销、ordinal/revision极限、未知COMMIT的只读观察、其他业务根与完整原APP/PB经营路径仍须独立完成。实际测试数、PASS/SKIP、清理、统一架构语义复核和GitHub精确提交另归档；不能以本片专项替代I1/A0、完整writer或生产验收。

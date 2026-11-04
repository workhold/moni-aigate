# B-P4-A4：真实 attempt 根的锁故障与累计期限合同

2026-11-04；实现前冻结。承接[完整出生 writer](b-procurement-attempt-birth-writer-contract.md)、[隔离安装](b-procurement-attempt-installation-contract.md)与[执行合同](b-procurement-execution-contract.md)。本片只新增[独立验收测试](../../internal/procurementattemptbirth/attempt_deadline_postgres_test.go)，不修改业务 SQL、材料摘要、原 fixture、迁移、角色、在线入口或默认执行路径；文档冻结和静态编译均不是 PostgreSQL PASS。

## 1. 真实入口、身份与期限

复用现有 PG16 专库 `127.0.0.1:55439/moni_test`、随机 schema、实际 APP LOGIN 和 W NOLOGIN 的隔离装配。每项首先通过真正 APP `pp_create_account`、原 `billing.Store.Reserve` 和 `pp_record_attempt` 提交一个已有 managed 前缀；supplier PB 使用原 Commander 配置。owner 不制造 root/source/p/journal，也不 SET ROLE 冒充 APP。

被测根使用独立 acquire 的真实 APP 连接、已知 `pg_backend_pid()` 和显式 READ COMMITTED 事务。根前逐值核对 session_user/current_user、隔离级别、`lock_timeout=3s`、`statement_timeout=5s`；不得覆盖这些值。真实 frontend statement 原样调用十三参数 `pp_record_attempt`，不得拆根、插入睡眠、延长 SPI 期限、增加生产调用或注入伪业务错误。测试客户端上限 8 秒，fixture 保持 180 秒；不对 55P03/57014 重试。

owner 阻塞事务只对真实 tenant/account 行持 `FOR UPDATE` / `FOR NO KEY UPDATE`，不更新任何业务事实。每次观察必须同时匹配准确 APP PID、APP LOGIN、`wait_event_type='Lock'` 和准确 blocker PID 属于 `pg_blocking_pids(APP_PID)`，并取数据库 clock/query_start。只见同角色任意等待、Go sleep 已经过时或查询尚未返回都不能计为锁证据。

## 2. 固定实验与准确 oracle

| 实验 | 所需真实证据 | 唯一接受的根错误 |
| --- | --- | --- |
| account 锁超时 | APP 精确等待已知 account blocker，锁保持直到服务器结束根 | `55P03 / canceling statement due to lock timeout` |
| 定向 backend 取消 | 已证明上述等待后，owner 只取消该 APP PID；`pg_cancel_backend` 必须返回 true | `57014 / canceling statement due to user request` |
| account 等待跨 Key expiry | 新请求出生前设置真实未来 Key expires_at；观察 account 等待时 DB clock 尚早于 cut；DB clock 跨 cut 后才释放 | `PP103 / key is not active`，来自真实 admission 后资格分支 |
| account 等待跨 request deadline | 原 Reserve 一次接受由 DB clock 采样的未来 RequestDeadline；在 account 等待前期限有效，跨 cut 后释放 | `PP409 / request deadline expired while waiting for procurement`，不能用前置 PP408 替代 |
| 两次锁等待耗尽单根期限 | 同一 APP 根先等待 tenant blocker，再等待 account blocker；两次 query_start 必须相同；tenant 锁在根开始后约 2.5 秒释放，且严格早于自己的 3 秒锁超时 | `57014 / canceling statement due to statement timeout` |

累计期限实验以数据库 query_start 为起点等待释放窗口。释放 tenant 前采一个真实数据库时刻 L，再 rollback 该只持锁事务；account 第二次锁等待必然晚于 L。收到根错误后再次采 DB 时刻 E，要求 `E-query_start >= 5s` 且 `E-L < 3s`，并证明两次真实锁等待、同一个根查询身份和 query_start。因此单次 3 秒锁超时、客户端 8 秒取消或人为 RAISE 都不能冒充整根 5 秒累计预算。观察错过窗口时直接失败，不跳过、不改期限、不降低断言。

Key expiry 只通过原有可变配置列设置和恢复，设置发生在目标 request Reserve 之前；不修改已出生 request/reservation/source。request deadline 作为原 ReserveInput 一次输入，持久值不得被测试或根改写。所有 cut 跨越判断使用 PostgreSQL `clock_timestamp()`；轮询间隔仅用于降低观察开销，不充当时序 oracle。

## 3. 回滚、旧前缀与恢复正控制

每次故障前在目标 request 已真实 Reserve COMMIT 后，使用原 fixture 的独立 owner 全行快照核固定 34 张表：原 request/reservation/idempotency/Key、客户账户/平衡流水、attempt、供应商账户/p/PE/command、审计/outbox/recovery、supplier/customer PB/B-P1，以及全部新来源/root/step/head/node/journal/transition。比较完整行和字段，不只计数。

根结果必须来自原 PgError 的 Code 和 Message。先收回 goroutine 结果、明确 rollback 根事务、释放独立 APP 连接和全部 blocker，再与故障前 34 表逐值比较。随后真正 APP 验证已提交的旧 request/attempt/account/root，并独立核旧账户 held=240、spent=0、head ordinal=2；失败不得伤害旧已提交历史。

Key expiry 场景在全行不变断言后恢复原配置。每项最后通过新的 UUID request 执行真实 Reserve 和完整 attempt COMMIT，验证恢复前缀及独立账户 held=480、spent=0、head ordinal=3。恢复控制不重放模型、不复用未知 COMMIT，也不把新成功效果混入失败基线。所有中途 fatal 均需独立 cleanup：取消/收回活跃查询，rollback 根与 blocker，释放连接后才由 fixture 删除隔离 schema/角色。

## 4. 明确保留的财务期限缺口与交付边界

原 [Reserve](../../internal/billing/reserve.go) 将 reservation 的财务硬期限固定为真实选价时点后 **24 小时**，且客户 request deadline 最多约 301 秒。此装配的 180 秒 fixture、5 秒根、不可变出生身份及禁止时钟操纵条件下，无法让一个真正有效的新 request 在 account 等待期间自然跨越财务期限；request deadline 必先失效。不能调用其他包的压缩 deadline owner fixture、禁用 identity trigger、改已出生 reservation、预造旧 source、伪造成功 provenance 或新增 caller 财务时间来宣称覆盖 PP108 财务分支。

本片因此只交付真实 Key/request cut 与三种原生数据库期限/取消实验；**account 等待跨财务硬期限及 PP108 财务分支仍未实证**。这里的缺口不是运行时 SKIP，也不因其他子项 PASS 自动关闭。将来需要先设计合法的长期边界验收环境或独立可证明的时间 seam，再单独实现与审定，当前不修改生产合同来迎合测试。

不使用 `t.Parallel`；专库包由集成人串行执行。子任务只允许无 TEST_DATABASE_URL 的 `-run '^$'` 静态编译。全部并行文件汇总后，由集成人记录真实零 SKIP PG 结果、更新验证与统一架构状态，完成语义复核后再 refresh/只读检查。该片不发行 Qualified、不启用 A5 executor、不证明完整 A4/A6 或供应商/生产验收。

# A1：真实钱包等待后的价格与期限合同

2026-11-04；基线 `71de18a`，根集成人已冻结本片先于测试。承接[真实 request 出生](b-procurement-request-birth-writer-contract.md) §2/8 和[原 Reserve](../../internal/billing/reserve.go)。本片扩充真实 APP Reserve 的验收，不修改原时间/价格行为、候选 SQL 或默认入口。

## 实际顺序与断言

原 Reserve 在价格域锁后以 DB clock 选零售价，随后锁 PB 和钱包，再 INSERT request / reservation、自动来源、PB / ledger，最后按 admission 时刻重核 Key 与 request deadline。A1 来源保存真实原件快照，不因等待跨 scheduled price 生效重新选价。request.created_at 保留原 DEFAULT，不拿它等同 retail_priced_at 或 pb_admitted_at。

根独占新增 `internal/procurementrequestbirth/request_birth_wait_postgres_test.go`；复用同包 actual APP fixture。owner 只准备原合法价格 / Key 期限、持有钱包 AVAILABLE 的真实 FOR UPDATE 锁、读取独立证据；被测请求和来源一律由 actual APP 的原 Store.Reserve 产生。以 pg_blocking_pids 确认真正阻塞在 owner 锁，不以 sleep 或 goroutine 已启动冒充到达某阶段。等到真实 DB clock 跨切点后才 COMMIT 解锁。

| 场景 | 真实结果 |
| --- | --- |
| 选价后跨 scheduled 零售价 | 新价格发布使用未来 effective_at，APP 真等待钱包；解锁后当下选价已是新价，但 request / Origin / Reservation 保留原较早版本和费率、120预占，retail_priced_at < effective_at ≤ pb_admitted_at |
| 等待跨 request deadline | 初次选价/期限检查时仍有效；等待后以原 admission 检查返回 billing request_deadline / request deadline expired，18表全行回滚至调用前，来源/root不残留 |
| 等待跨 Key expires_at | 原授权读时仍有效；等待后返回 invalid_key / key is not active，全资金与出生图回滚 |
| 钱包锁持续超过原 lock_timeout | 确认真实钱包阻塞后保持锁，原 Reserve 返回 PostgreSQL `55P03` / `canceling statement due to lock timeout`；无来源或资金残留 |
| 精确取消正在钱包等待的 APP backend | owner 通过 `pg_blocking_pids` 和随机 APP LOGIN 精确定位唯一 backend 后 `pg_cancel_backend`；原 Reserve 返回 `57014` / `canceling statement due to user request`；不将主动取消换成业务成功或重试 |

切点由 DB 返回，避免 Go 与 DB 墙钟差作为 oracle。测试用有限切点间隔和上下文，保持 APP 原 lock_timeout=3s、statement_timeout=5s；确认阻塞、等待切点及收取异步结果均有界。失败或取消始终释放实际锁并取消原请求，不重发模型或将未知提交转成功。固定 wall clock 单调性只用于本受控测试；不把测得耗时当网关延迟指标。

超时/取消两项均在锁释放前收取原失败，18表全行与调用前相同；释放实际锁后以新UUID经原 Reserve 成功 COMMIT，证明失败已回滚且连接/权限仍可用。这里取消的是已定位的实际数据库语句，不把客户端 context 错误冒充 PostgreSQL 57014；不覆盖 COMMIT 结果未知。此两项合同先于新增测试冻结。

只关闭上述明确分支。PB control 等待、独立实际准入窗口跨期、完整预算争用、网络/COMMIT未知以及A2仍独立验证。任何原错误、缺少真实阻塞或矩阵失败均保留；不通过降低断言、放宽时间界或重新安装候选绕过。

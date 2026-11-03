# A1：真实父锁等待后的资格重读与提前约束

2026-11-04；基线 `3b528fb`。根先审定本合同再新增 `internal/procurementrequestbirth/request_birth_parent_wait_postgres_test.go`。承接[request出生](b-procurement-request-birth-writer-contract.md)、[原lockPrincipal](../../internal/billing/store.go)、[已有钱包等待](b-procurement-request-birth-wait-contract.md)；原SQL、fixture、001–022和运行接线不改。

## 真实入口与等待

每项独立随机fixture。owner仅在单个原父行持有真实 `FOR UPDATE`，APP为原受限LOGIN、由原 `billing.Store.Reserve` 自己开始RC/3s/5s事务。该请求将IdempotencyKey留空，避免tenant外键在更早幂等INSERT取得KEY SHARE而使观察到的tenant阻塞不属于lockPrincipal；不把该选择扩称幂等等待验收。`pg_blocking_pids`确认实际APP确实被只持该单行锁的owner阻塞后，owner在同事务更新合法管理状态并COMMIT。APP必须读到新资格并沿原Code/Message拒绝，不得基于等待前状态出生。

| 父行/合法管理变化 | 原错误 |
| --- | --- |
| tenant status→suspended | forbidden / tenant is not active |
| membership status→suspended | forbidden / membership is not active |
| user status→suspended | forbidden / user is not active |
| Key revoked_at→真实DB clock | invalid_key / key is not active |
| Key scopes→空数组 | forbidden / model is not authorized |
| membership allow_all_models→false且无member grant | forbidden / model is not authorized |
| model enabled→false | forbidden / model is not authorized |
| model profile_version→另一个合法TEXT | profile_changed / model profile changed before reservation |

owner的管理变更是预期外部事实，不能要求它随APP失败回滚。失败baseline在owner持锁事务中完成管理变化后、释放锁前独立读取，包含原18表完整行，并扩充tenants/memberships/users/models/tenant_model_grants/member_model_grants/price_groups/tenant_pricing_assignments八表，随后APP失败后的26表全行必须相同。APP未提交行在owner事务不可见，不把APP已经结束后的快照当失败前baseline。保留原legacy和先前已提交根；恢复管理状态后，新UUID原Reserve必须COMMIT并核120及完整birth。

每项等待有界，不更改原3s/5s；超时与取消清理释放真实锁。保留准确business cause，不拿42501、PgError、context错误或ErrTxCommitRollback代替资格错误。仅证明上述真实父读取等待后的行为，不关闭原Reserve全部父锁权限撤还、并发模型授权管理命令、PB窗口、COMMIT未知或A2/A3。

## 提前约束的明确边界

独立actual APP raw事务先设置提前约束，再用已冻结21列等价INSERT（从真实原Reserve参考行取合法输入）。当前source→head→pointer→step→seal顺序需要真实deferred暂态，两个场景分别准确定位：

| 提前模式 | 准确拒绝层 |
| --- | --- |
| `SET CONSTRAINTS ALL IMMEDIATE` | 原020的AFTER `pb_request_source_check`排序先于`zz_pp_rb_request_birth` capture，在reservation尚未INSERT时首先报23514 / periodic source reservation missing；不能把它当A1 validator已执行 |
| 仅 `zz_pp_rb_origin_complete IMMEDIATE` | 原020保持deferred，真实capture先INSERT Origin；Head/pointer/Step/seal尚未完成，A1自己的即时来源核验报PP902 / procurement_history_invariant |

两项错误事务回滚后18表完整baseline不变；恢复默认deferred后新原Reserve正常成功。首轮把ALL IMMEDIATE误判为PP902的测试实际收到原020的23514，原日志保留；核对020与实际trigger排序后在此修订准确原因并新增A1专门模式，不改业务SQL、触发器次序或把断言放宽成任意错误。

该项不是把完整Go Reserve改成调用者可传事务，也不伪称同等raw INSERT证明完整资金。已完成完整图之后主动检查约束与开始前改变模式不同，本项仅验开始前提前IMMEDIATE。

PG仅根串行；静态编译不计真实通过，最终证据与剩余项更新[验证记录](../../docs/verification/procurement-request-birth.md)和统一架构。

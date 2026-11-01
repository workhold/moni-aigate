# 023-B 独立 Commander 冻结切片

2026-11-01，根冻结[b-http-contract](b-http-contract.md)§3三种Commander方法与§4配置/回执事务，按本文件补齐精确实现边界。仅新增同包 `commander*.go` 与真实PG测试；不追加迁移、不接HTTP/main/billing、不修改已冻结的Tx/Reader API。调用者的真实session/Guard/ready/CSRF/再认证属于未来HTTP编排；Commander不接收trusted/role/控制器ID，自行验证当前数据库actor权限。共享APP角色不等于最终用户身份。

## 1. 构造、事务、锁

NewCommander(pool *pgxpool.Pool) (*Commander,error)拒nil；零/nil Commander拒绝。三个Configure方法的精确签名沿HTTP§3，输入/ID按纯基础层Validate，金额/expected_revision为字符串。parent context与10秒取更短；最多三次事务尝试（初次+2），不增加网络/provider/Go时钟准入。

每次独立Acquire一个pool连接，保留至提交或完整清理后才归还；在该连接开RC事务，设3s锁/5s语句。不得对pgxpool.Tx自动Release后的底层连接再读TxStatus。operation advisory使用独立两int域：第一键23023024，第二键`hashtext(current_schema()||':'||canonical_operation_uuid)`；锁只是哈希串行门，仍完整比较operation身份，不复用23023023首配域或A/022域。

随后按B主合同§4.1锁序：tenant NO KEY UPDATE→admin SHARE；Key tenant SHARE→actor membership SHARE→actor user SHARE→Key UPDATE；procurement admin SHARE→account NO KEY UPDATE。全部等待后**新语句**复验当前tenant/user/membership/role/Key归属与未撤销。平台目标可暂停；Key当前tenant必须active，owner可写本tenant全部Key，member仅自己Key，billing_viewer拒。当前身份失权403；合法身份的缺失/不可见目标404。平台目标根缺失仍先完成当前admin验证，再返回404；不在control/account后回补前层锁。

## 2. 原操作、配置和事实

授权后先全局operation UUID读取已提交原回执，不先按scope过滤。比较完整actor/kind/scope/target、四字段safe_input和规范指纹；全等返回不可变原receipt、Created=false，不再要求旧expected等于当前revision/不按当前clock覆盖result。任一身份或输入异体返回policy_operation_conflict，无他人元数据输出。原数据不符合精确DTO/源control绑定则invariant，不猜修复。

新操作才读/锁目标control并比较CAS：无control要求expected0；有control要求当前revision，继而检calendar不可变。首次配置在建立control前显式取得本scope的pb_scope_sync exclusive，再以新语句有界读真实未决源（稳定UUID C序LIMIT1001）。1001先报maintenance且无control/operation/version/audit/初占事实；≤1000按SQL创建control并逐源legacy_import+首事件。不可取业务request/attempt/reservation锁或账户/ledger后层锁；导入字段由数据库guard派生。pb_control INSERT再自带同一屏障/有界核验，不能省略。

已有配置仅enabled/limit真实变化才UPDATE并替换configuration_operation_id，revision由guard递增；变更前若revision=MaxInt64返回periodic_revision_exhausted。no-op不UPDATE，不增revision，但追加本次独立operation和安全audit。始终在同tx INSERT精确operation，让SQL创建version/audit及不可变result_snapshot；Go扫描RETURNING、严格解码/Validate，并复验预期actor/kind/scope/target/fingerprint/resource。Created仅本次新建control且COMMIT确认后true。

## 3. 错误、重试与未知提交

业务CAS/calendar/maintenance/operation conflict/revision exhausted由持正确锁的新查询明确产生，不按任意23514/23505猜业务冲突。SQL精确clock regression→对应code；23514/23503/NUMERIC overflow等已确认不变量错误→invariant，其他驱动/池/期限→unavailable。Error()不含SQL/schema/参数/PG原正文。

失败后用独立≤5秒context Rollback；只有实际40P01/40001、Rollback确认成功（或明确的failed COMMIT已结束同一事务且底层TxStatus I）且原连接idle才允许在总10秒内重试原ID。Rollback失败/未知状态立即销毁连接，不重试。COMMIT不能确认时销毁连接、返回periodic_budget_outcome_unknown且零结果，不以新operation重试；明确COMMIT rollback按安全错误返回。不得把普通timeout/cancel/连接断开或所有5xx当已回滚。成功仅COMMIT确认后返回完整receipt。

## 4. 验证范围

真实001–019+三tracked SQL随机schema，实际APP LOGIN建立操作和资金源；owner仅DDL/静态catalog/明确测试clock/故障fixture。验证first/replay/no-op/真实change、旧CAS/日历、跨actor/scope/输入operation冲突、当前撤权与Key归属、旧result不随配置/clock变化、原子audit失败回滚、0/1000/1001导入与无反向业务锁、revision端点、并发同operation/同targetCAS、rollback/COMMIT不明的可覆盖边界和精确清理。没有真实wire丢COMMIT时必须明确未覆盖，不用内存错误冒充。

# A4：采购同步与单根期限验收

2026-11-04。先冻结[同步合同](../../specs/023-periodic-products/b-procurement-attempt-synchronization-contract.md)与[故障期限合同](../../specs/023-periodic-products/b-procurement-attempt-fault-deadline-contract.md)，再新增两个独立测试文件。本片没有修改既有 SQL、材料摘要、原 fixture、正式迁移、生产角色或常规启动；仍是 `birth-exact-link-a2-v1` 的隔离候选验收。

## 被测入口与独立证据

所有正向业务均由真实受限 APP LOGIN 执行。账户命令与 attempt 调用原公开根，客户预占/入账使用原 billing API，凭据绑定与 supplier 首配分别使用原 `BindCredential` 和 `ConfigureProcurement`。owner 只准备原管理父对象、观测和持锁，不制造成功来源或采购资金事实。被测身份不是 owner 或 owner SET ROLE。

| 测试文件 | 本轮场景与必须核对的证据 |
| --- | --- |
| [同步](../../internal/procurementattemptbirth/attempt_sync_postgres_test.go) | 原绑定先行／attempt 先行，分别得到 managed／历史 unmanaged；旧 replay 不补采购来源，新请求才按新绑定执行 |
| 同文件 | 原 supplier 首配先行／attempt 先行，真实 namespace X/S 及准确 PID，分别核唯一 `new`／`legacy_import` allocation/event；原 admission/control marker 不回填 |
| 同文件 | 原 configure 排队后提交 paused，attempt 锁后拒绝 PP114；只扣除已逐值核准的合法 configure 增量，再比较完整 34 表；账户/head 其余字段必须与原行相等 |
| 同文件 | 两个真实开户并双审入账的租户共享 limit=300、每次 U=240，准确阻塞关系下一笔成功、另一笔 PP115；客户各自预占保留，失败方无部分采购出生 |
| [故障期限](../../internal/procurementattemptbirth/attempt_deadline_postgres_test.go) | 准确 account blocker 的原生 55P03；只取消该 APP PID 的原生 57014；保留原 PgError Code/Message |
| 同文件 | account 等待前 Key/request 仍有效，以数据库真实 clock 跨 cut 后释放；分别 PP103／PP409，完整 34 表回滚 |
| 同文件 | 同一 frontend 根先等 tenant、再等 account，两次 query_start 相同；首锁约 2.5 秒释放，第二锁不足 3 秒，累计 5 秒触发真正 statement timeout；不是客户端取消或人为 RAISE |

保持 READ COMMITTED、3 秒 lock timeout、5 秒 statement timeout 与 180 秒 fixture。测试 barrier 是隔离 schema 内原样返回 NEW 的 INVOKER trigger，只提供可观察暂停，不替换原 guard、不生成来源、不修改被测值或权限。准确 `pg_blocking_pids`、已知 APP/blocker PID 和 namespace 对象代替猜测调用顺序。

每项故障先保留已真实提交的 managed 旧前缀；回滚后比较完整行快照并由 APP 再认证旧 request/attempt/account/root。随后用新 request 执行完整恢复控制，独立核 held 从240变480、spent=0、head ordinal 从2变3；不重发模型、不拿恢复成功覆盖失败基线。

## 执行结果与原件

期限专项首轮真实专库 race：**3 顶层／4 子项 PASS，0 FAIL/SKIP，22.960 秒**，即五个实际实验。原件 `.local/a4-deadline-first.jsonl`，8182 字节，SHA-256 `beb817b6501d99d0ec149ac20ca53f3abfa473ce14fdee56d91dcc1024a891bd`。

同步首轮前五顶层 PASS，10.588 秒。第六项独立执行在管理父数据准备时因第二 Key 复用 `fixture-disabled` digest 触发 `api_keys_digest_key` 23505，尚未进入共享限额竞争；修订仅为第二 Key 使用独立非秘密 fixture 标识。原失败保留在 `.local/a4-two-tenants-first.jsonl`，2.004 秒。集成复核另收紧 configure 归一化 oracle：在替换账户/head 前，先核完整行仅有合同允许字段变化，且新 xid/journal/source/输入身份准确关联；不以 writer validator 代替独立比较。

最终完整 A2＋A4 按实际发现的56个顶层、C序轮转为四份14项，包级串行、race、每进程10分钟。前三份全部PASS；第四份的trigger漂移fixture期间，主机在06:42:07因低电量睡眠，08:50:38接电唤醒，原180秒context超期并使后续三项拒绝开始。该失败原件、系统明确Sleep/Wake两条记录及摘要均保留；没有增加fixture/SQL/进程期限，也没有删反例。

唤醒后先单独重跑受影响D组及全部9项新增A4：10顶层／19子项全部PASS、0 FAIL/SKIP，72.498秒；不加入最终数量。随后**原第四分片完整14项**按同一表达式、同一源码和原期限重跑全部PASS，141.816秒。最终四分片准确并集为 **56顶层／1652子项PASS、0 FAIL/SKIP**（50顶层真实PG、六静态），每个run恰有唯一PASS且无跨片重复，四进程耗时相加579.829秒；这不是一次单进程整包的墙钟时间。

[机器记录](procurement-attempt-synchronization.json)绑定完整源码SHA、名单、四份最终JSONL与原第四分片失败；`.local/a4-a2-final/summary.json`保留首轮，`.local/a4-a2-reviewed-final.json`保存明确替换第四分片的最终审定记录。结束后`ab_*` schema、临时角色和backend均为0，installer默认PUBLIC函数EXECUTE恢复true。所有首轮、专项与整片复验不重复相加。第二租户digest修订和configure oracle收紧后的源码均包含在最终分片中。

## 保留边界

首次配置同步只证明来源序列化与真实导入，不证明 active 周期 cap 或跨 UTC 周期。原 Reserve 的财务期限是 retail 选价时点后24小时；在当前180秒 fixture／5秒根及真实不可变身份下，不能自然跨越这一财务 cut。本片没有修改时钟、压缩期限或伪造历史，PP108 财务分支仍未实证。

本轮证明的是 actual APP 调用既有候选 SQL 根，不是尚未实现的 Go prospective adapter。全部组合错误优先序、其余路由/重试、逐路径权限与来源攻击、未知 COMMIT、完整 A4/A5、全域 profile、R2 和生产接线继续独立验收。真实供应商、正式支付、容器/1Panel、HA 与最终网关性能也不由这些测试代替。

统一架构的调用顺序与生产权限没有新增，本片扩大可核查的同步、故障和期限覆盖；关联模块及后续 A5 边界见[采购细图](../architecture/procurement-attempt-birth.md)。全部并行结果稳定后由集成人同步架构正文和内容快照，再执行只读检查。

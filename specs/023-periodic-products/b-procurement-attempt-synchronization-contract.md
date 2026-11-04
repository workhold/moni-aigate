# B-P4 A4：采购 attempt 出生与管理命令同步验收合同

2026-11-04；本页先冻结实验与验收边界，再添加独立专项测试。承接[出生根合同](b-procurement-attempt-birth-writer-contract.md)、[候选装配合同](b-procurement-attempt-installation-contract.md)与[原预算 Commander 合同](b-commander-contract.md)。测试入口为 [attempt_sync_postgres_test.go](../../internal/procurementattemptbirth/attempt_sync_postgres_test.go)；本页不是生产迁移、线上安装或真实供应商验收结果。

## 1. 被测主体、时钟与权限

使用 `newFixture` 的独立 PG16 schema、实际受限 APP LOGIN、原 001–022＋B-P1 与 `birth-exact-link-a2-v1`。正向操作只调用实际 APP 的 `pp_create_account` / `pp_configure_account` / 原 `billing.Store.Reserve` / `pp_record_attempt`、原 `procurement.Store.BindCredential`、原 `periodicbudget.Commander.ConfigureProcurement`。不以 owner 手写 binding、采购预占、预算 allocation/event 或 prefix source 代替这些操作。

测试保持 READ COMMITTED、3s lock timeout、5s statement timeout、180s fixture context。不同并发调用使用相同 APP 身份的独立单连接 pool，以提前取得准确 backend PID；等待证据须为该 PID 的 `pg_blocking_pids` 对应关系。预算命名空间另核 `pg_locks` 中真实双 int advisory 锁的对象与模式。不得修改时钟、伪造预算历史、禁用原 guard、改变生产权限或扩大运行超时。

owner 仅准备原管理父对象、持有真实行锁/测试 barrier、观察 catalog/锁与结果。barrier 为隔离 schema 内新增的 INVOKER trigger，仅调用 `pg_advisory_xact_lock` 后原样返回 NEW，不生成业务来源、不改被测值、不授 APP 新权限。所有原 trigger 继续运行。session advisory blocker 在 bounded cleanup 中释放；后台调用均可取消并等待退出。

## 2. 同步实验与状态转移

| 实验 | 必须观察的等待链/持锁 | 释放后的唯一可接受结果 |
| --- | --- | --- |
| 绑定先行 | owner 持账户 N；原 BindCredential 已持 channel U，等待 owner；attempt 等待 binder 的 channel U | binder 先提交真实 binding/command/audit；attempt 用新 statement 读取 binding，创建 managed origin、唯一 p/PE/reserve journal，H=240、S=0 |
| attempt 先行 | 在真实 upstream INSERT 的 BEFORE barrier 停住，此时 attempt 已持 channel S；原 BindCredential 等待此 attempt | attempt 先提交 unmanaged，账户及预算不变；binding 随后提交，不补生旧 attempt 的 p/PE/source；旧序号 replay 不改变全量快照，后续新请求为 managed |
| 周期预算首次配置先行 | 原 Commander 在真实 operation INSERT 的 BEFORE barrier 停住，持账户 N 与本账户 namespace X；attempt 等待此 Commander | config 提交后 attempt 重读真实 control，p.pb_control 指向它，allocation origin=new、origin_source_id=真实 reserved PE，恰一 procurement_event 镜像 |
| attempt 先于首次配置 | 在真实 p INSERT 的末序 BEFORE barrier 停住，attempt 已持账户 N、未配置 namespace S；Commander 等待此 attempt | attempt 先提交无 control 的真实 p/PE；Commander 获取锁后枚举真实未完成 hold，以 legacy_import 建立唯一 allocation/event，不回写原 admission/control marker，不借 origin=new 或 catchup 补证 |
| 等待后的账户资格失效 | owner 持账户 N；真实 configure 先排队，attempt 后排队；准确 PID 证明各自等待阻塞者 | configure 先提交 paused 状态；attempt 锁后重读并返回 PP114，不留下本次 upstream/p/PE/prefix/root。完整 34 表快照扣除准确验证的合法 configure 增量后应与实验前一致 |
| 两租户共享账户竞争 | 两个真实租户各自 APP Reserve；首个 attempt 在 p barrier 持同一账户 N，第二个 attempt 明确等待首个 | 账户 limit=300、每次 U=240，只能一笔成功；另一笔 PP115。成功来源 tenant/request 与原件一致，账户及 head H=240/S=0，仅一个 p/PE/reserve journal；失败租户不留下 attempt 出生 |

测试不以 sleep 推测开始顺序；轮询仅等待 catalog 中出现所要求的准确 PID 关系，并在 3s 锁超时之前释放 blocker。`pg_blocking_pids` 允许包含硬阻塞者与排在前面的软阻塞者，但必须包含本实验指定的实际 backend。

## 3. 独立结果与回滚 oracle

客户 retail 输入/输出费率各 1,000,000；供应商 cost 各 2,000,000；input bound=100、output limit=20，因此 customer Reserve=120、supplier U=240。验收从原件与手算金额核对，不调用 writer 内部 helper 产生期望值。

managed 核对 origin→p→原 PE→journal→account/head、唯一金额、step/root 与分支；unmanaged 核对禁止集合与不变账户。PB 核对 control、allocation、event、p 原 marker、原 reserved PE 的准确关系，区分 `new` 的非空 admission/window 与 `legacy_import` 的空 admission/window；control 统计持有额均为240。最后由实际 APP 执行公开只读 `pp_validate_attempt` / `pp_validate_account` 核验完整 prefix。

并发中的另一个合法事务会改变 snapshot，因此不能将失败后的状态直接与所有并发操作之前的快照比较。账户失效实验记录实验前完整 34 表快照；提交后独立验证合法 configure 的账户/head 投影和唯一 command/source/root/step/journal/audit，按其真实 operation/root ID 仅移除这些合法新增行。恢复投影前，完整账户行仅允许 `state`（active→paused）、`revision`（1→2）变化；完整 head 行仅允许 `state`（active→paused）、`configuration_revision`（1→2）、`ordinal`（1→2）、`journal_id`（真实新 configure journal）、`updated_xid`（真实 configure source.created_xid）变化。其余字段逐值与完整旧行比较，包括 label、provider、created_at、金额、配置界限与 pp 身份；不允许先整行替换再声称已独立验证。

configure source 的 actor、operation、kind、input target、label=supplier、provider=fixture、state=paused、limit=1000、expected revision=1、applied/空 reason 与调用时的已知输入逐值相等；source/journal/root/head 的真实 xid、root、journal 和 APP OID 链同时核验。原 head 的 journal/xid 也须指向旧账户的真实 create source。该 oracle 使用原件、完整快照和独立明确的 SQL/字段期望，不以 writer 的 validator 作为独立正确性来源。仅这些条件全部成立才恢复这两个目标行，与原完整快照比较；其他行或表不能整表忽略。

两租户竞争通过独立 SQL 明确枚举成功与失败 request 的来源集合和共享金额，不把成功事务的变更误判为泄漏。第二租户管理父 Key 使用绑定其唯一 UUID 的非秘密 fixture digest，满足原全局 digest 唯一约束；充值制单、不同账号审批、Reserve 与 attempt 仍走真实 APP 入口。

## 4. 明确未覆盖的边界

首次配置按原合同在下一个 UTC 周期启用金额限制；本实验只证明首次配置/出生的串行化、镜像来源和累计账户硬界，**不声称已覆盖有效周期内的 cap 竞争、跨月切换或真实供应商限制**。本页不扩大 A2 的 command＋request＋reserved 前缀，不开放 dispatch、费用终态、更正、恢复或 R2。实际 PG 专项由集成人串行运行；静态编译及本页存在均不能替代 PG 验收。

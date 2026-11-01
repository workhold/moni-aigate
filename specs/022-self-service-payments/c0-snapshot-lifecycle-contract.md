# 022-C0-T1：Snapshot 生命周期（根冻结）与后续读取租约候选

2026-11-02，**T1生命周期与固定catalog stage已实现并完成其真实PG/race专项（115.412秒、124终态全PASS），历史范围不扩张。T2现已另按[真实授权根冻结](c0-authority-contract.md)实现二参Attest与Permit.Verify，[T2真实PG/race专项](../../docs/verification/maintenance-authority.md)已通过。** ReadLease、全来源/basis、epoch和生产init/serve仍属后续；T0锁顺序和安全清理要求保留，Snapshot集成后的Close以本文§8为准。依据[Permit合同](c0-permit-contract.md)、[T0冻结§10](c0-permit-contract.md#10-根冻结t0专用连接双session-gate与清理)、[维护合同](c0-maintenance-contract.md)、[迁移快照](c0-snapshot-contract.md)、[静态checker](c0-schema-check-contract.md)、[独立来源核验](c0-verifier-contract.md)。HLD/LLD及统一架构由根汇合复核，本稿不自行refresh。

## 1. 裁决与实施切片

采用“**一个私有原始顶层事务 + 固定内置 evidence stage + 未来窄 ReadLease**”。不向协调器或 scanner 公开 Snapshot.Tx、pgx.Tx、Conn、PgConn、Context、Begin、任意回调或事务控制 SQL。现有 opaque catalog proof 始终绑定 conn.BeginTx 实际返回的同一个顶层 pgx.Tx；不制作每 stage 不同的 pgx.Tx proxy，也不制作 Conn 返回 nil 的假事务适配器。

T1 本轮冻结的可编译范围只包括 Snapshot 建立、固定 catalog evidence stage 与终结清理。生产包保持 T0 的标准库、pgx、fundinglotcatalog 依赖，不新增 migrations/billing/periodicbudget/payments/operations。完整 history/manifest stage 及 migrations 叶子依赖明确归 T2 根冻，不在 T1 实施。精确 API 为：

~~~go
// namespace: internal/maintenancetrust；字段全部私有，副本共享状态。
type Snapshot struct { /* shared lifecycle */ }
type Outcome uint8
const (
    OutcomeInvalid Outcome = iota
    OutcomeOpen
    OutcomeCommitted
    OutcomeRolledBack
    OutcomeAbandoned // 未发 COMMIT，物理关闭但未确认远端已退出
    OutcomeUnknown   // 已发 COMMIT，结果没有获得明确确认
)
func (g *Gate) BeginPlan(ctx context.Context) (*Snapshot, error)
func (g *Gate) BeginInit(ctx context.Context) (*Snapshot, error)
func (s *Snapshot) VerifyInstallation(ctx context.Context) (*fundinglotcatalog.InstallationProof, error)
func (s *Snapshot) Commit(ctx context.Context) error
func (s *Snapshot) Rollback(ctx context.Context) error
func (s *Snapshot) Outcome() Outcome
~~~

既有 Acquire/Close 签名保留。Outcome 是只读事务结论，不是业务回执或可接受为授权的输入；nil/zero Snapshot 返回 OutcomeInvalid。String、GoString、JSON 对 Snapshot、未来 lease/row/rows 固定 redacted，不编码 outcome 之外的身份、连接、SQL、行值或原 proof。T1 不添加 Permit、ReadLease 的空实现、可用 stub、测试免验构造器或 main/CLI 调用者；上述之外的源码不能按本文提前接线。

与旧候选的替代关系已由T2根冻结：没有Snapshot.Tx导出口或外部proof/裸tx授权入口，当前唯一发行函数是 `Attest(ctx, snapshot)`，唯一Permit校验是 `Permit.Verify(ctx)`。维护scanner接收ctx/pgx.Tx的旧示意签名不再保留；T3 ReadLease仍待另冻，没有可用stub。普通线上及APP runtime既有pgx.Tx接口不因此改变。

## 2. 生命周期、一次性消费与 busy 所有权

Gate 仍只有一条新专用连接、一次 402→401，且一生最多进行一次 Begin 尝试。Begin 发 SQL 前即消费该机会；失败不回到可 Begin 状态。Plan 与 Init 必须分别 Acquire，不能同 Gate 切换模式或再 Begin。保存实际顶层 rawTx 与 Conn/PgConn 指针；只由本包使用。

| 状态 | 可执行动作 | 下一状态或失败结论 |
| --- | --- | --- |
| gate_held / idle | BeginPlan 或 BeginInit 恰一次 | beginning；失败关闭连接，不返回半 Snapshot |
| beginning | 直接 BeginTx、固定 SET；Init 再一次静态 125 表锁 | snapshot_open；没有任何预先 SELECT |
| snapshot_open | VerifyInstallation 恰一次、Commit、Rollback、关闭 | evidence_stage / closing；没有来源读取许可 |
| evidence_stage | 固定 catalog issuer 在原 rawTx 完整执行并复核 | installation_checked；失败 invalid |
| installation_checked | 未来真实 Attest、终结 | authority_stage / closing |
| authority_stage | 未来 T2 完整身份/history/v1/gate；Init 尾部全 accounts 行锁 | attested_plan 或 attested_init_accounts；失败 invalid |
| attested_* | 未来 BeginScan、终结 | scan_active 或 closing |
| scan_active | 同一 lease 的顺序读取、Finish、Abort | 返回 attested_* 或 invalid；全程占有 stage busy |
| closing | 仅一个终结调用 | committed / rolled_back / unknown / abandoned |
| invalid | 不再发行 proof/lease/结果 | 仅有界清理及 Outcome 观察 |
| terminal / closed | 重复清理、Outcome | 结论保留，不再 Begin/attest/scan |

共享状态至少包含 Gate nonce、Snapshot nonce、rawTx、固定 mode/purpose、原 Begin 父 context、绝对截止时刻、stage nonce/owner、当前 SQL operation owner、未关闭 Rows/Row 集合、原 installation proof、T2 后的真实 Permit 绑定、失效原因、commit 是否已发与终态。复制 Snapshot/Permit/lease 不复制权限或 busy，不产生新 nonce。

生命周期锁只用于短状态转移，绝不拿着它等待网络、Rows、context 或另一 goroutine。**stage busy 从入口取到 Finish/Abort，包含 scanner 的 CPU 计算和 SQL 间隙**。Commit/Rollback/Close/另一个 stage 看见活跃 stage 立即 maintenance_gate_busy，不发 SQL、不取消先行合法操作、不无界等锁。每个 stage 内另有 SQL busy：Query 的 ownership 保留到 Rows 关闭或 EOF，QueryRow 保留到 Scan 完成；同一连接不并发读写。重入或并发调用不得通过 TryLock 失败后偷偷执行 pgx。

并发拒绝本身不破坏仍在执行的合法操作；内容不符、scanner 错误、identity/gate/proof 不符、数据库错误、任一有效父 context 取消或到期则永久 invalid。Finish 不是“caller 说扫描通过”的 bool：它只证明本 lease 完整结束、无悬挂读取且最后身份复核通过；领域完整性仍由 T3 的私有结果构造器与独立算法证明。

## 3. 私有总 context 与到期强制关闭

Begin 从入口起计算唯一 Snapshot deadline：无 caller deadline 时 now+30 秒；有 deadline 时 min(caller deadline, now+10 分钟)，剩余不足 1 秒拒绝。不得以 Acquire 已结束的 30 秒子 context 代替 Begin 的原父 context；不得在 Begin 返回时 defer cancel 掉持久 Snapshot context。原父的取消链和绝对期限保留到终结。

每个 evidence/authority/scan/Commit stage 的私有有效 context 同时受 Snapshot context、该次 caller context 的取消、两者较早 deadline 约束；caller 给 Background、较长 deadline 或另一个 context 都不能放宽 Snapshot。后续 stage 不重新计时，catalog issuer 自有至多 5 秒子预算只可再收紧。stage context 由本包创建和取消，绝不通过 Context() 或返回值泄露给 scanner。

不能仅依赖 BEGIN 时 statement_timeout：它按 statement 计时，不能限制多条 SQL/CPU 间隙的总时长。建立 Snapshot 时安装一个生命周期 watcher，Snapshot context 取消/到期即：

1. 在短状态锁内标 invalid、撤销所有未消费许可并保存 timeout 原因；若正在 Commit，只记 commit_in_flight，不先猜提交结论。
2. **不论连接当时 idle、scanner 正计算或正在收包，立即封闭 T0 socket registry 并关闭其原始 net.Conn**。不能等 busy，不能以 pgx.IsClosed 或 pgx.Close 已返回充当物理关闭。
3. watcher 不调用 pgx.Close/Query/Rows.Close/Commit/Rollback、不读写非线程安全的 pgx 连接内部字段。随后由原 operation 返回并释放其所有权，再由普通清理路径收尾。晚到的 pgx CancelRequest dial 仍被已封闭 registry 当场关闭。

健康 Snapshot 明确终结时在同一状态转移中停止 watcher，并等待已启动的 watcher 仅完成上述短失效/原始 socket 关闭动作；不等待 SQL 或无限 goroutine join。禁止“先解除 timer，再用无限 context 提交”。deadline 与 COMMIT 明确回复相遇时保留实际收到的提交/回滚证据；仅关闭 socket 不确认任何远端结果。

## 4. 唯一顶层 RR、固定 SET 与 125 表锁

只有 both-gates-held 且 idle 的 Gate 可调用 conn.BeginTx；固定 TxOptions 是 RepeatableRead + ReadOnly（Plan）或 ReadWrite（Init），不接受已有事务、Serializable、Deferrable、nested Begin、SAVEPOINT、原始事务 SQL、SET ROLE 或 SET SESSION AUTHORIZATION。rawTx 是该次 BeginTx 直接返回的 wrapper，不能在 evidence、Attest 或各 scanner 之间换 wrapper。

Begin 后先按顺序执行三个 SET LOCAL utility：search_path=已 preflight 的目标 schema,pg_catalog,pg_temp；lock_timeout<=min(3 秒,剩余预算)；statement_timeout<=剩余预算。schema 仅用 pgx.Identifier 引用；超时值为本包计算的有界整数毫秒字面量，不接受 caller SQL。**不用 SELECT set_config，不用在这些 utility 与 Init LOCK 前核 current_user/current_setting/pg_locks/xid/history/catalog。** PostgreSQL 必须在实际锁等待之后建立首次数据快照，验收要用物理 writer 屏障证明，不能只凭语句名称推断。

Init 的下一条 SQL 是一次完整命令：

~~~sql
LOCK TABLE ONLY <schema>.active_report_projection,
           ONLY <schema>.alert_commands,
           -- 附录 A 全部 125 个目标，按标识符 UTF-8 字节/C 序合并
           ONLY <schema>.vouchers
IN SHARE ROW EXCLUSIVE MODE;
~~~

生产实现必须是附录 A 的静态数组形成的**一条完整 LOCK**，不是上面的省略示意；每项都有 ONLY 和全限定标识符。数组固定 125 个唯一名称，来自原 109 与 C0 16 的集合并集，不能运行时查 catalog 组装、先锁 109 后附 16、跳缺表、降模式或接受 CLI 子集。ONLY 明确防止未知 inheritance/partition descendants 自动扩入锁清单；不声称它阻止 PostgreSQL 获取系统元数据锁，或对未知 view 的锁行为提供 heap 认证。未知对象类型、继承/分区/view/rule/外关系仍由随后完整静态 checker 拒绝，拒绝前不得执行该对象的业务 SELECT。

LOCK 缺表、权限、等待超时或错误使 Snapshot invalid 并物理关闭。SHARE ROW EXCLUSIVE 阻止这些目标上的 INSERT/UPDATE/DELETE/TRUNCATE 与冲突 DDL；不得把锁数量当 incoming FK/trigger 完整证明。Plan RO 不执行此 LOCK，也不取任何 account 行锁。两种 mode 的权限、真实身份和业务来源仍须 T2/T3 独立核验。

## 5. 内置 catalog stage：合法 issuer 与原 wrapper 的衔接

VerifyInstallation 是唯一 T1 evidence stage；进入后持有 stage busy 至完整 catalog、空集与最后 identity 复核的整个调用结束，仅在 snapshot_open、原父和 caller 都有效、rawTx 活跃且 mode 正确时执行一次：

~~~go
// 以下为包内固定调用图示意，不是导出回调 API。
proof, err := fundinglotcatalog.VerifyInstallationTx(stageCtx, snapshot.rawTx, snapshot.gate.copiedPins)
// 最后在同 rawTx 调 proof.VerifyTx 并比较 Catalog().MatchesPins。
~~~

传入的是私有 capped stageCtx 和同一个真实 rawTx；issuer 不能另 Begin/Commit、拿另一个 pool 或反向调用公开 VerifyInstallation 重入 stage。issuer 包内原来的 5 秒预算保持，未审 expected、别的 catalog profile 或缺对象仍零 proof+固定错误。只有 catalog、空集和最后复核全部成功，才存一份本包拥有的 opaque proof wrapper 副本，并返回另一 proof wrapper；caller 对返回 wrapper 赋零不能改变本包持有的 wrapper，不能升级出维护授权。之后不重新发行第二份 installation evidence。任何阶段失败，即使 leaf proof 已在局部变量产生，对外也只返回 nil+固定错误，不能保留半成功给 Attest。

本段保留T1阶段的history/manifest边界：T1本身不实现、不缓存其成功声明；当前T2已按真实授权根冻结实现下述固定流程。完整 manifest 不来自 CatalogProof，也不接收 caller digest。T2 从现有 migrations.Files 的完整 *.sql embed 集合读取全部文件，按 filename ASCII/C 序生成 22 项 JSON 数组；每项仅固定键序 name、sha256，小写 SHA-256、UTF-8、无空白/换行/BOM。沿用 [schema-check §9.1](c0-schema-check-contract.md) 的 2459 字节规范与固定 SHA ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de；零/缺/多/改名/改 bytes 或任何不符均拒绝。本过程只读编译期 embed，不读工作目录、不复制旧 exporter 的运行职责、不改其文件/source pins。未来新增正式 migration 时必须另审 manifest 与 expected profile，不能自动把新 embed 清单当本版已受审。

T2 在同一个固定 authority stage 调 migrations.VerifySnapshotTx 确认数据库完整 history 恰等于该固定 embed 集合，并把 manifest canonical/digest 仅存私有 Snapshot 状态，绑定该 Snapshot nonce、rawTx 和原 opaque proof；helper 返回的 error 不是可由 caller 递交的证据。该私有 binding 的取得全由本包固定调用完成，没有 FromDigest/Trusted/VerifyHistoryPassed 参数或公开 history bool。最后还核原 wrapper/conn/PID/top xid/mode、context 和双 gate；任何失败不发行 Permit。

T2 Attest消费本Snapshot已存的owned证据，在同一个rawTx重核原proof.VerifyTx、完整pins、固定manifest/history、16表空集、真实LOGIN/owner、双gate、原v1与新鲜停服边界；缺项失败。T2不再次发行installation proof，v1逐字段矩阵已由[真实授权合同](c0-authority-contract.md)根冻结并实现。manifest只来自固定embed与实际同RR完整history，不借旧helper的error结果伪造外部可传证明；T1的installation_checked状态本身不表示history已通过。

## 6. T2当前API与T3读取租约候选

T2当前已实现如下唯一API；全部账户锁在Attest返回Permit之前完成，最终专项已通过，证据与边界见[独立记录](../../docs/verification/maintenance-authority.md)：

~~~go
func Attest(ctx context.Context, snapshot *Snapshot) (*Permit, error)
func (p *Permit) Verify(ctx context.Context) error
~~~

以下仅为尚未实现、仍待T3根冻结的候选，不提供可用stub或授权路径：

~~~go

type ScanKind uint8
const (
    ScanPeriodicBudget ScanKind = iota + 1
    ScanLedger
    ScanFunding
    ScanProcurement
    ScanSandboxSources
    ScanPostInit
)
type ReadLease struct { /* private shared stage state */ }
func (p *Permit) BeginScan(ctx context.Context, kind ScanKind) (*ReadLease, error)
func (l *ReadLease) Require(kind ScanKind) error
func (l *ReadLease) Query(sql string, args ...any) (*Rows, error)
func (l *ReadLease) QueryRow(sql string, args ...any) *Row
func (l *ReadLease) Finish() error
func (l *ReadLease) Abort() error

type Rows struct { /* private driver rows + query ownership */ }
func (r *Rows) Next() bool
func (r *Rows) Scan(dest ...any) error
func (r *Rows) Values() ([]any, error)
func (r *Rows) Err() error
func (r *Rows) Close()
type Row struct { /* owned one-row query */ }
func (r *Row) Scan(dest ...any) error
~~~

Attest 必须是固定内置 authority stage，不接受任意 SQL 回调或 scanner function。对 Init，在完整 catalog/history/真实身份/v1/gate/空集已核验后、仍持同一 stage busy 时，执行固定：

~~~sql
SELECT id FROM <schema>.ledger_accounts
ORDER BY id COLLATE pg_catalog."C"
FOR UPDATE;
~~~

实际 SQL 须全限定目标与受审排序规则；没有 WHERE/LIMIT/SKIP LOCKED/NOWAIT。全量流式消费并关闭 Rows，保存规范有序 ID 的只读摘要/数量；不得预扫描 account ID 再按截断集合锁行，也不以 pg_locks 推测无冲突行锁是否取得。此时 checker 已证明 heap、id=text、无 RLS/view/custom 类型或排序行为，才可执行业务 SELECT。全部 125 表已持 SRE，所以锁行后不能有另一个事务补插 account。零 account 是合法空集，是否存在完整商业来源由 T3 判断，不默补账户或余额。

只有accounts成功锁完且最后authority复核成功，才能返回Init Permit。Plan不执行行锁，按完整T2校验后发行Plan Permit。已实现顺序固定为Begin/SET → Init ONLY125表锁 → InstallationProof → Attest内history/身份/v1/gate/空集 → Init全account行锁 → 出口复核 → Permit。pre-epoch source leases及各source scans仍待T3，不因Permit已实现而自动开放；不得把已返回Permit再交caller声明accounts已锁。

ReadLease 只由真实 Permit.BeginScan 发行，绑定同 Snapshot/rawTx、固定 kind、私有 context 和 stage nonce；ScanPostInit 还必须由未来封闭 init 写入器完成状态开放，不能在初始空安装提前申请。BeginScan 内先进行 Permit 入口验证，Finish 在所有 Rows 关闭、无在途操作后对同一个 rawTx 完整出口验证；中途所有 Query/QueryRow/Next/Scan/Values 也核共享状态与预算。各方法**没有 caller context 参数**，不能用 Background 绕过期限。Rows 没有 Conn、RawValues、SendBatch、CopyFrom、Exec、Begin、Commit、Rollback 或 raw pgx.Rows 获取口；Values 复制 []byte 等借用内存后才返回。

同一 lease 不并行 SQL；遗漏 Row.Scan 或 Rows.Close 使 Finish 拒绝并 invalid，不能先释放 busy 再后台 drain。Rows/Row 的数据库/扫描/取消错误映射固定安全错误，不返回 driver unwrap 或含行值的 ScanError。Abort 在 active 时撤销整 Snapshot 并 raw-close，不能一边另一 goroutine 使用 pgx 一边 Rows.Close/Rollback；其后由拥有查询的 goroutine 返回并释放 operation owner。已经成功 Finish 的 lease 上 defer Abort 是幂等 no-op；重复 Finish/Require 或 closed lease Query 拒绝，不把已完成结果重新消费。

Query 接收 SQL 不意味着 SQL 解析器或只读沙箱：**Init RW 内的写 CTE、写函数或原始 COMMIT 不能仅凭此 API 在数据库层被识别并禁止**。许可边界是受审同进程固定 scanner 调用图：T3 只使用静态 SELECT、参数化值、受审标识符及无副作用函数，不对 HTTP/插件/任意 callback 开放 lease。API 强制的是连接/事务不可外泄、总 context、生命周期、单 operation 与 stage busy；不声称抵御恶意 Go 或实际 owner 任意 SQL。source lease 不开放 Exec 或任意 callback，但这也不能替代调用图审计。

## 7. 既有 pgx.Tx reader 的迁移方式

[funding_ledger_read.go](../../internal/billing/funding_ledger_read.go) 和原 PB verifier 依赖 pgx.Tx；禁止把 ReadLease 伪装成 pgx.Tx，返回假 Conn 或跳过原 identify/owner 检查。T3 先为真实只读 SQL 核心抽取包内窄 query seam（Query/QueryRow 及 Rows/Row 所需方法），实现一份独立扫描算法；两个入口各有自己的真实身份前置：

1. 原 APP/runtime pgx.Tx 入口保留原 VerifyTx/实际 LOGIN/RO/owner 规则，以真实 tx+ctx 的私有适配器调用纯读取核心。它不认识维护例外，PB 公共 verifier 继续拒绝 sandbox installer。
2. 新维护入口只接受 *maintenancetrust.ReadLease，先 Require 对应 ScanKind，经 lease 查询同一原始事务；由真实 T2 Permit 的入口/出口核身份，不能把现在线上 identify 结果直接删掉再共用。

候选签名形状是 `VerifyMaintenance(lease *maintenancetrust.ReadLease) (OpaqueMaintenanceResult, error)`；结果类型分别属于 PB/billing，字段私有、零值无效、绑定该次 lease/Snapshot 的不可伪造 identity。领域结果的精确 Go 类型、跨 scanner 合成及 basis 内容在 T3 冻结，本稿不把 Finish 或 kind 当 CoverageComplete。CLI 协调器拿 BeginScan 的 lease 后交对应固定 scanner；scanner 内顺序计算、Finish 成功后才构造可返回结果，任一步失败返回零结果并 defer Abort。没有“裸 tx 老维护入口”并存或使用另一个 pool 的 fallback。

Query seam 的抽取必须等价回归既有字段/行覆盖、排序、租户/金额/余额/来源关系及故障零结果；独立 reader 不改为调用候选 SQL checker 作 oracle。原有在线接口在 T1 不变；T3 抽取是单独可审变更，不以旧测试仅编译通过宣称语义等价。

## 8. Commit、Rollback、Close 与未知结果

所有终结先取得 stage busy（无 scanner/Rows），进入 closing 并撤销 Permit、lease、installation 的后续可消费状态，再发恰一次相应命令。它们不能由 Query/Row 对象绕过。Snapshot.Outcome 只是安全事务结论；领域 ready plan、epoch 回执仍须 T3/T4 完整核验及 Gate 清理成功后才能输出。

- Commit 使用 Snapshot 与该次 caller 的交集预算。进入前已失效时不发 COMMIT，raw-close 并保留 timeout/transaction 错误；一旦发出不自动重试。明确成功回复记 committed。确认回滚**仅**允许 errors.Is(err, pgx.ErrTxCommitRollback)，或该次真实 COMMIT 返回 *pgconn.PgError 且已经收到其 ReadyForQuery=idle；pgx.ErrTxClosed、单看 TxStatus 或任意 driver error 均不能确认回滚。其他 driver/error/取消/回复丢失均记 unknown，返回固定 maintenance_commit_unknown；不能以现在 epoch 缺失猜未提交。这里“已发”保守定义为单次原 rawTx.Commit 调用开始：调用前最后复查有效 ctx 并记录 commit_in_flight，开始后若不能证明没有发送，一律按可能已发送分类，不因 context-already-done 等错误猜未发送。禁止第二次 rawTx.Commit。代码对明确回复的分类须单独审阅，不能“所有 error 都当 rollback”。
- context watcher 与 Commit 的竞态先记 invalid/in-flight 并 raw-close；原调用返回后以实际收到的明确回复决定 committed/rolled_back，否则 unknown。watcher 不自行写一个不可更正的“已回滚”结论。已确认 outcome 不因后续 cleanup error 改成另一个业务结论；Commit error 与 Close error 分别保存，cleanup 不能遮蔽 maintenance_commit_unknown。
- Rollback 仅对尚未终结的原 rawTx 发送一次 ROLLBACK；作为清理使用独立 background 最多 5 秒，不因其 ctx 为 nil/已取消跳过。Snapshot 总 deadline watcher仍有效，到期照常关闭 socket，不能为 Rollback 延长 Snapshot 的授权寿命。明确回复才 rolled_back；失败则物理关闭、abandoned + maintenance_cleanup。不在 unknown COMMIT 后再发送 ROLLBACK，也不更改 unknown 结论。
- Gate.Close 看见活跃 stage 立即 busy；看见无人使用但未结束的 Snapshot，不擅自 COMMIT/ROLLBACK，直接 raw-close，标 abandoned 或保留已有 unknown，返回并缓存 maintenance_cleanup。正常路径应显式 Rollback 或已确认 Commit 后再 Close。只有已确认事务结束/idle 才沿 T0 的同一个 5 秒清理预算逆序 unlock401→402、核空集、物理关闭。复制和重复 Close 不重复发 SQL且保留第一次清理结果。

unknown 立即封闭 socket registry，不返回池，不重发 init、不换 epoch ID、不补半图。新观察器必须另 Acquire402→401 后只读核原 epoch 与完整关系；不存在仍 unknown，原 backend 未消失不能假称跨连接连续持原锁。独立观察器、写入器和最终业务回执不是 T1 提供的能力。

## 9. 必须的真实 PostgreSQL 验收

本节保留T1的Snapshot/固定catalog evidence stage/终结验收范围；T2新增身份、前驱与accounts锁的证据另见[专项记录](../../docs/verification/maintenance-authority.md)。尚未实现的T3 source lease不计为集成完成。全部 PG 测试显式使用独立专库与随机精确资源；不启动普通 main、58123、PSP 或模型，不改共享 public/DB ACL，不用 SET ROLE 代实际 LOGIN。保留 T0 新连接、双 gate、socket registry 的原验收。

| 验收 | 必须观察的物理事实与失败边界 |
| --- | --- |
| Plan/Init options 与首 SELECT | 真实 TCP wire 记录唯一 BEGIN 与 SET；Plan RR RO 无 SRE/FOR UPDATE，Init RR RW 首 SELECT 前一条 ONLY 125 全序 LOCK。无预先 catalog/current_setting/set_config/xid/history SELECT；不把测试 hook 注入生产。 |
| writer 屏障 | 独立 writer 先对排序靠后的原表持冲突写锁并完成待提交业务变更；观察 Init 已发送一条完整 LOCK 且真实 pg_locks 等待，writer 提交后才准 Init 首次 source/evidence SELECT。Init 必须看到 writer 的变化、旧 plan basis 不符；不能只有最终余额相同或在 Begin 前已提交。等待在 <=3 秒内用实际锁/响应屏障调度，不延长 lock_timeout。 |
| 125 停写与 ONLY | 对所有目标核静态数组完整/C 序；锁后另一实际角色的 INSERT/UPDATE/DELETE/TRUNCATE/冲突 DDL 不能越过，普通 SELECT 可进行；未知 inheritance/partition/view/incoming FK/trigger 被完整 checker 拒绝，不能据 ONLY 省略静态认证。缺表与部分已锁后失败不得返回半 Snapshot。 |
| accounts 次序 | catalog/history/Attest 前没有 ledger_accounts SELECT；T2在Attest内完成各账户按完整C序、无LIMIT的锁定后才发行Permit。空账户集合法但不宣称来源完整；坏RLS/id type/view等在业务行锁前拒绝。此项T1当时留待T2，当前证据见[T2专项](../../docs/verification/maintenance-authority.md)，不追溯计为T1已验。 |
| 顶层与 proof | 同一次原 wrapper 发行真实 InstallationProof；跨 Snapshot/conn/副本伪状态、第二 Begin/第二 issuer、nested/raw SAVEPOINT 调用图反例、关闭/失败/Commit/Rollback后复用全部拒绝；不降低 fundinglotcatalog 的 pins、五秒预算或 profile。 |
| deadline/context | 无 deadline 30 秒、有父 deadline 取较早且至多10分钟、剩余<1秒拒绝；原 Begin 父取消、后续 Background、长 deadline、多次stage不能延长。SQL阻塞、Rows读取、CPU间隙和**无人使用的idle Snapshot**到期都实际 raw-close，再独立 PID+backend_start/pg_locks 观察消失，不能由 fixture先关闭使测试通过。 |
| busy/泄漏 | 活跃 evidence/scanner全程（含两条SQL之间固定屏障）Commit/Rollback/Close/第二stage立即busy且无额外 SQL；Rows未关闭、Row未Scan、并发Query、复制lease、Finish/Abort重入及晚到CancelRequest dial不得越界或race。T1可测真实内置stage；source专项随真实T2/T3完成。 |
| SQL资源/reader | 所有 Rows 在下一 SQL 前关闭、Values owned copy、关闭/超时/Scan失败零领域结果；Query无外部ctx口、无Conn/rawTx出口；原PB/ledger入口及新维护入口分别验身份与完整独立算法。 |
| COMMIT未知 | 用真实PG持久写事实与test-only wire在服务器已COMMIT后拦回复，先独立证明已提交再仅丢回复；生产自行raw-close，零业务回执、原ID、无重试，OutcomeUnknown。另验真正ROLLBACK回复、deferred约束导致明确回滚、context在发送前/后、cleanup错误不得遮蔽unknown。T1可用隔离测试sentinel事务，不能冒称epoch/商业init验收。 |
| rollback/清理 | 独立<=5秒清理、取消ctx仍执行；ROLLBACK回复丢失/非idle异常只毁连接，不解别人的锁、不unlock_all；Commit/Rollback/Close副本幂等；确认终结后逆序释放双session锁，独立观察原PID/start消失。fixture只清理精确自有对象，不能先替生产关闭被测socket。 |

纯测试还应覆盖 deadline 组合、状态转移、kind/零值/复制、所有 redacted 编码、固定 125 名称/ONLY/排序以及错误不 unwrap。源码稳定、专项 PG/race 与后续集成分别记录，根再复核统一架构/changes/manifest。**冻结本身不等于验收；当前专项结果及源码版本另见[验证记录](../../docs/verification/maintenance-snapshot.md)。**

## 附录 A：静态 125 表集合（C 序）

根已独立核对六份已认证 expected 与原 109+C0 16 并集。下列清单按 C 序编码为无空白、无尾 LF 的 UTF-8 JSON 字符串数组（2878 bytes），SHA-256 固定为 6fd978751fe24d37fa29da679be0976e12659e8a8f64d3983050280fffc00b19；实现与纯测试必须同时核 125/去重/C 序和此摘要。下列列表从已受审 ordinary-v1 的 application 普通业务 heap 提取，固定为原 109 表与 C0 16 表并集；已随 T1 冻结为静态设计清单，不在运行时读取 expected/数据库生成 LOCK。TOAST、index、复合类型关联 relation 与 mock/receiver peer 自有表不混入本 125 表。peer 停服与静态认证仍是 T2 的独立职责。

~~~text
active_report_projection
alert_commands
alert_control
alert_episode_events
alert_episodes
alert_rule_runtime
alert_rules
api_keys
audit_events
billing_profile_blocks
channel_batch_operations
channel_credentials
channel_model_discoveries
channel_model_discovery_results
channel_version_credentials
channel_versions
channels
commerce_commands
commerce_operation_receipts
cost_versions
credential_procurement_bindings
credit_orders
delivery_observations
finalization_records
financial_idempotency
funding_consumption_refund_lots
funding_lot_balances
funding_lot_epoch
funding_lot_events
funding_lot_legacy_approved_refunds
funding_lot_legacy_openings
funding_lot_legacy_receipts
funding_lot_legacy_sandbox_receipts
funding_lot_legacy_settlements
funding_lot_runtime_attestations
funding_lots
funding_receipts
funding_reservation_lots
funding_settlement_lots
funding_wallet_heads
funding_wallet_legacy_sources
funding_wallet_operations
identity_challenges
identity_initialization
identity_mail_outbox
identity_mail_subjects
journal_lines
journals
ledger_accounts
member_model_grants
memberships
metering_event_conflicts
metering_events
mfa_recovery_codes
model_bindings
model_capability_versions
models
payment_cancel_intents
payment_captures
payment_command_observations
payment_command_outbox
payment_effects
payment_event_conflicts
payment_evidence_processing
payment_inbox
payment_order_capture_selections
payment_orders
payment_policy
payment_provider_revisions
payment_query_attempts
payment_query_conflicts
payment_review_facts
payment_runtime_attestations
payment_sandbox_environment
payment_sku_changes
payment_sku_versions
payment_skus
payment_transaction_identities
payment_verified_evidence
payment_verified_queries
periodic_budget_allocations
periodic_budget_configuration_versions
periodic_budget_controls
periodic_budget_events
periodic_budget_operations
periodic_budget_windows
platform_probe_attempts
price_groups
price_versions
pricing_operations
procurement_accounts
procurement_balance_observations
procurement_budget_events
procurement_commands
procurement_cost_proposals
procurement_evidence_identities
procurement_reservations
projection_outbox
projection_receipts
provider_cost_corrections
reconciliation_cases
recovery_gate
recovery_jobs
refund_orders
report_totals
request_idempotency
request_records
reservations
route_binding_isolations
route_policy_versions
schema_migrations
session_families
sessions
settlements
tenant_model_grants
tenant_pricing_assignments
tenants
upstream_attempts
users
voucher_batches
voucher_policy
voucher_redemptions
voucher_revocation_decisions
voucher_revocations
vouchers
~~~

## 10. 根冻结范围与尚未开放的能力

2026-11-02。本节保留T1冻结时点：根冻结§§1–5、§8、§9中仅T1的实现与验收、附录A；当时不实施Permit或账户锁。后续T2已由§11及真实授权合同单独冻结实现，不能据本节旧边界认为当前没有Permit，也不能将T2计入T1历史验收。ReadLease、来源结果和reader抽取仍为T3候选，不实现可用stub。125静态名称须与五ordinary expected、sandbox application expected及原109+16全集相等；冻结SQL、ACL、expected root和五秒catalog预算保持。

终态幂等补充：同一 Snapshot 及其副本重复同一个已完成的 Commit/Rollback 返回首次结果，不再发 SQL；对已由另一终结方式消费的 Snapshot 返回 maintenance_transaction，不改变原 Outcome。invalid/unknown 只能清理及观察，不能通过 Rollback 改成确定未提交。Begin 对有效 Gate 一旦消费机会（在任何 BEGIN SQL 之前）即不可重试；输入 nil/已取消/剩余不足一秒时也不保留可用的半状态。零值对象只返回安全错误。

新增安全 sentinel 为 maintenance_schema、maintenance_transaction、maintenance_commit_unknown；T0 六种错误保持。固定 catalog 失败映射 maintenance_schema（已取消/到期优先 maintenance_timeout）；Commit 明确服务器回滚返回 maintenance_transaction，无法确认返回 maintenance_commit_unknown。后续 T2 依赖来源、前驱矩阵及维护授权另冻。

## 11. T2 后续冻结状态

2026-11-02。T1历史边界和证据保持原范围。[T2真实授权](c0-authority-contract.md)已根冻结并实现于[Permit](../../internal/maintenancetrust/permit.go)：Attest只接收原Snapshot，复用owned installation、同rawTx、busy与原总期限。所有invalidate/终结开始/Close均同步撤销active authority和共享Permit；watcher只改短锁状态与原socket，不清仍在读取的不可变证据。[T2真实PG/race专项](../../docs/verification/maintenance-authority.md)已通过，真实身份/history/前驱与全accounts锁不追溯计为T1已验；ReadLease、全来源/basis、epoch和资金写入继续后续。

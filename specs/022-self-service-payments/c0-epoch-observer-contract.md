# C0-O1：独立停服 epoch 只读观察器

2026-11-03。根冻结的实施合同；本文件先于实现。依赖[受保护初始化](c0-protected-init-phase-contract.md)、[维护 Gate](c0-maintenance-contract.md)、[完整钱包读取](c0-source-wallet-reader-contract.md)与[严格 v2](c0-proof-v2-codec-contract.md)。本片属于隔离维护库候选，不是生产 CLI、APP RuntimeProof 或正式 C0 迁移。

## 1. 目的与范围

初始化 COMMIT 丢回包后，原 Snapshot 的 `OutcomeUnknown` 永久保留，不自动重试 COMMIT 或初始化。本片通过**新的 Acquire、物理连接和 RR READ ONLY**，在重获同一 instance/migration 双 session 锁后观察原 Expected；不得沿用原 socket、原快照或原 Permit。

Expected 五字段不含原事务 attempt 身份。成功只能说明目标 epoch 的持久事实，不能声称是哪次网络调用或操作者完成了提交。未取得双锁、查询失败、超时或 cleanup 不明时，没有“未提交”结论。

## 2. API 与封闭结果

```go
func (g *Gate) ObserveFundingLots(ctx context.Context, expected FundingLotsExpected) (FundingLotsObservation, error)

type FundingLotsObservationStatus uint8
const (
    FundingLotsObservationInvalid FundingLotsObservationStatus = iota
    FundingLotsObservationMatched
    FundingLotsObservationUninitialized
    FundingLotsObservationDifferentEpoch
)
type FundingLotsObservation struct {
    Status FundingLotsObservationStatus
    Observed FundingLotsExpected
    SourceRows, SourceBytes uint64
}
```

结果是普通描述值，没有 Verify、授权方法、原件、角色秘密或数据库句柄。它不发行 Permit、ReadLease、InstallationProof、RuntimeProof、Plan 或初始化许可；复制/序列化不会产生能力。

Expected 的 epoch 是非零小写规范 UUID，其余四项为 64 字节小写 SHA-256。全部先校验长度再保存，避免复制任意大输入。非法输入或取消也消费该 Gate 唯一 Begin；并发 busy 拒绝不消费对方操作。

| 成功状态 | 必须实际验证 | 结果字段 |
| --- | --- | --- |
| Matched | 唯一 epoch/attestation 完整合法；五字段精确等于 Expected；当前完整资金图一致 | Observed 为实际五字段，SourceRows/Bytes 为本次 84 表实际计数 |
| Uninitialized | 已重获双锁且 fresh stopped 成立；当前 catalog/history/身份/前置支付域有效；完整 16 张 C0 表均为空 | Observed、SourceRows/Bytes 全零；不表示旧业务图已验证或允许开账 |
| DifferentEpoch | 另一 ID 的唯一完整 epoch/attestation 及当前完整图均通过 | Observed 为实际五字段，SourceRows/Bytes 为本次 84 表实际计数；需要人工核查 |

同 ID 异摘要、部分 C0、仅 epoch 或仅 proof、重复单例、坏原件、当前图 mismatch/unproven 一律安全错误与零结果，不能包装成成功 conflict 或 Uninitialized。不同 ID 也必须完成全部验证才能返回 DifferentEpoch。

## 3. 原连接、事务与生命周期

1. 入口认领 Gate 唯一 `beginAttempted` 与 busy，私有观察模式不向调用者返回 Snapshot。保持 Acquire 的真实连接预检、双锁顺序及全部 pins，不增加替代构造器或宽泛账号入口。
2. 固定累计 30 秒，取 caller 更早期限；从入口涵盖事务建立、SQL、CPU 空隙、全部检查与 RO 终结。catalog/前置支付读取沿各自原有更小预算，不能每条 SQL 重置 30 秒。独立 cleanup 最多 5 秒。
3. 双锁取得之后才开始新的 RR READ ONLY；使用固定受审 search_path、lock_timeout 和 statement_timeout，不进行业务表写锁或 FOR UPDATE。
4. 私有 snapshotState 可以复用 watcher/实际 binding，但使用独立 observation 模式；原公开 Plan/Init、16 空表 Installation/Attest 和八位 once 合同保持。不得伪造 InstallationProof 或向旧 authorityStage 填一个空证明绕过发行门禁。
5. 所有 SQL、Rows、纯计算、终结及 cleanup 期间 Gate 一直 busy；别名 Gate、Begin、Close 和第二次观察立即拒绝。取消 watcher 只撤销并关闭原捕获 socket，不并行调用 pgx、Rows、ROLLBACK 或 drain。
6. 内部路径为 claim → begin → authenticate → inspect → verify graph → fresh exit checks → ROLLBACK → cleanup → publish。RO 成功终结使用一次原 raw ROLLBACK，检查该原连接 ReadyForQuery idle；绝不发送 COMMIT。
7. 认领 ROLLBACK 前及其完成后，均在生命周期锁下检查取消与绝对期限，不能只依赖 timer 回调及时更新 ctx.Err；然后确认唯一终结并停止、确认 watcher。原 socket 的独立 cleanup 完成后才发布结果；这段清理单独限时5秒，可以越过已经合法完成的30秒事务期限，但调用者取消或清理失败仍不返回部分成功。
8. 任何失败永久耗尽此 Gate，安全撤销/关闭；不修复坏数据、不重新观察、不隐式调用 Init。Close 重复返回保留的清理结果。错误限现有 Input/GateBusy/Identity/Timeout/Execution/Cleanup/Schema/Transaction/Resource；不泄露 driver、DSN、SQL、proof 或财务明细。

## 4. 身份、环境和停服事实

同一实际 RR 内绑定原 wrapper/conn/PgConn/PID/backend_start/top xid、Gate nonce、DB/endpoint、current_user=session_user=Config.User、真实 LOGIN、schema namespace/effective object owner 及原双 session 锁。

ordinary 使用实际 schema/PB owner LOGIN，保留 public→pg_database_owner 的精确解析；sandbox 仍只允许原固定专库、数据库 owner 的 authenticated moni_test LOGIN。不能用 SET ROLE 或任意 SELECT 账号替代。

复用受审 catalog `VerifySnapshotTx`，不能调用要求 16 表空的 `VerifyInstallationTx`。当前完整 latest history 与本进程嵌入 manifest 精确比较，不只检查前缀，也不从库自导 Expected。首版只支持当前受审 001–022/profile/descriptor/roles roots；未知历史或当前 root 拒绝，后续升级须另冻显式 cutover/current roots 兼容关系。

fresh stopped 每次先连续单独执行 pg_stat_clear_snapshot 再查询活动：ordinary app/projector，sandbox app/worker/projector/mock/receiver 的实际 runtime OID 全部清零。入口和出口都检查。该规则沿现有可信停服模型，不宣称只读 RR 能阻止恶意 owner 或绕锁重连。

ordinary 必须证明原支付域为空、默认关闭策略及受审强制 FK/旁路规则。sandbox 必须读取实际 v1 canonical、原 SHA、marker、peer-local metadata、双 schema 迁移与完整 keyset；复用原投影、限额与 strict v1，不能只信 v2 内嵌 v1。

读取身份、history、stopped、前置支付投影可提取为**私有、不授权限的快照 reader**；旧 authority wrapper 仍先验证原 Installation/authority 全部绑定，再包成原证据。新观察路径不能反向发行旧 authority evidence。

## 5. 原件、历史基线和当前资金图

完整 16 空表分支独立检查每张，不用 `WHERE id=expected` 看不到目标便当空。非空时要求 singleton epoch/attestation 各恰一行；其他半套组合拒绝。

observer 读取不受自有 writer 保护的持久行。沿原 84 表固定 SQL 对**全部可变长度列**作 NULL/byte length guard，再返回原值；proof 1..262144 字节，固定 digest/UUID/OID/版本/模式按原字段界，NUMERIC/text 不允许无界传输。恰一行、完整 EOF/Err/Close，格式错误不截断。复用 native timestamptz_send 八字节解码，保留 PostgreSQL 合法 BC/大年份及微秒精度。

为避免重复/无界读取，新增 `fundinglotscan.ProjectEpochProof(ctx, WalletFacts) (EpochProofRows, error)`，只从该次完整 84 表原行提取唯一 epoch 与 attestation。DTO 包含原 `WalletLotsEpochRow` / `WalletLotsAttestationRow`，深复制 bytea/字符串/可空指针，复核完成/资源/qualification；新 view 不共享可变 rejected map，不再查询。它是普通不可信数据，不能从 DTO 构造 WalletFacts、不能被观察入口当输入、不能认证数据库或发行权限。观察器仍自行执行整次 ReadWallet 和通用资格，再对原行作 strict-v2 与实际环境绑定。

strict DecodeV2 后，实际 raw SHA 与保存 SHA/epoch RuntimeProofDigest 相等，21 字段及所有 nullable union 与真实 epoch/attestation、当前已审 pins、完整 manifest/current catalog、实际前置 v1 逐项精确绑定。basis_descriptor 与 catalog expected/schema/roles roots 不互换。

历史 `basis_digest` 只核 epoch ↔ strict v2 ↔ Expected（同 epoch 时）保存值一致。**禁止重新计算经营后的 109 表 basis 并要求等于切换时摘要。** epoch 的历史 counts、available/held NUMERIC 合计由实际 immutable legacy/opening 原行独立重放核对，不能拿当前 tenant 数或当前钱包余额替代。

同一真实快照实际执行 `fundinglotscan.ReadWallet` 84 表、八算法，沿原全局 100000 源行/16MiB、字段上限、分页/物理 EOF 和独立 core。严格核 scope/mode/每个 algorithm 状态/coverage/全部 mismatch 和 unproven 为零；ordinary sandbox N/A 只沿原受审空域条件。

可复用通用 `fundingLotsPostQualified`，**不能复用 `fundingLotsPostCounts`**，后者的 pre68+GeneratedRows、零 SettlementLots/RefundLots、全 tenant 为旧身份仅在初始化成立。必须允许合法新 tenant/genesis、新来源、reserve/finish、消费退款、旧未结后 finish 和分次退款；是否支持由实际候选完整图验证，不能跳表或调用 SQL funding_valid 代替。

出口重新完整 catalog/history、实际维护身份及 fresh stopped，并核与入口一致；同快照原件与所有 Rows 已完整完成。只读事务终结、会话锁和 socket cleanup 属基础设施；全路径禁止业务 INSERT/UPDATE/DELETE、DDL、GRANT、候选 checker 调用、epoch/UUID/proof 生成或模型/支付网络请求。

## 6. 验收

真实 PostgreSQL 专库由根串行：

- 空 C0 → Uninitialized；完整 empty/零旧 tenant/空 TEXT/Unicode/大 NUMERIC 与 genuine sandbox 开账后 Matched；另一有效 epoch → DifferentEpoch，同 ID 改任何 Expected 摘要 → 错误零值。
- COMMIT 已完成但回包丢失 → 原 OutcomeUnknown 不变，新双锁 observer Matched；真实发送前断线/事务结束回滚 → 全 16 空后 Uninitialized。原 session 双锁仍持有时 Acquire 无结论，不允许早查 epoch 判失败。
- 合法经营后当前 basis/钱包余额/tenant 数变化，仍 Matched；显式证明未使用 init-only counts，至少覆盖真实新 tenant/genesis、来源、reserve/finish/消费退款及保留原 cutover counts。
- actual APP/projector（sandbox 另含三 runtime）、SET ROLE、错误 endpoint/login/DB/schema/同名换 OID、history/ACL/catalog/前置 v1 漂移均拒绝。
- 半套表、损坏/超长 strict v2、NUMERIC/text 超界、同 ID 异摘要、旧 counts/实际当前钱包/资金源损坏拒绝；读失败/超资源/取消/期限/EOF/Close/RO 终结/cleanup 失败零结果。
- Gate alias、唯一 Begin、busy、取消 CPU gap、精确单 ROLLBACK/零 COMMIT、清理前不发布、实际后端与锁释放。固定 wire/调用日志与前后 witness 证明无业务写入/重建/自动重试。
- 原 Plan/Init/Installation/Permit/predecessor 专项回归不放宽空安装要求。普通 main/HTTP/worker、迁移和权限不增加调用；依赖隔离门禁继续通过。

单元 fake 可验证错误分支与纯字段组合，不能代替真实数据库身份、失回复、经营后完整图或 cleanup 证据。验证记录列精确版本、命令、PASS/SKIP/FAIL、历史失败与未覆盖范围；未执行项不能写成已验。

## 7. 后续依赖

O1 不解决 APP 84 表读取权限。当前 `payment_query_conflicts` 的四列 APP 不可读，R1 必须先审全部 84 表/列最小 ACL 与 historical/current roots 兼容，再实现非空 RuntimeProof；禁止借 worker 拼报告、跳表或偷偷 GRANT。正式迁移、维护 CLI、所有在线资金 helper 和生产启动门禁在 R2 单独冻结；之后继续 C1/C2/C3、商品/订阅、媒体扩展、目标部署/生产/HA 和性能阶段。整体目标仍进行中。

本轮并行静态预研确认以下设计关口，**不是已冻结的 R1 实施合同或真实权限验收**：

- `ReadWallet` 的固定并集为84表/776个唯一物理列；完整history另读 `schema_migrations(name,checksum)`。必须按实际访问列逐项验最小权限，不能只核四列或以 `SELECT *` 代替。
- 旧 `VerifyPaymentRole/VerifyPaymentRolesFor` 的 APP 冲突表负例继续保留。R1 须显式设计新的静态基础矩阵与受审列增量，不能放宽旧 checker；四列授权会改变当前 roles/expected roots。
- 历史 v2 保存的 cutover roots 与新 current roots 须以受审静态兼容关系精确绑定；未知组合拒绝，不从 live catalog 推导兼容根，不覆写旧原件或只验 history 前缀。
- APP/worker 对 mock/receiver 的内容权限保持为空。不能照抄 O1 维护 owner 的前驱 reader；须另冻 APP 自有 marker/原v1原件、严格文件绑定与v2内嵌原字节的关系，peer静态结构仍由catalog核验。
- `VerifyRuntimeTx` 仍只发行 catalog 证明。完整组合层位置、API、累计预算与原事务有效期需重新审定；旧 `billing` issuer 只是未授权候选，不能为了接线删除当前普通服务依赖隔离门禁。worker窄证明与projector catalog-only边界分别保留。

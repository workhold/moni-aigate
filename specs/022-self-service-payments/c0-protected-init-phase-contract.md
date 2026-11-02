# C0 受保护初始化事务合同

状态：2026-11-03 冻结。承接最小投影、纯规划、basis v1、strict v2 及既有维护授权合同。本片完成库级候选安装的实际初始化闭环；不增加正式迁移、维护 CLI、HTTP 或在线资金接线。候选 DDL/ACL、固定 22 项迁移材料不变。

## 1. 公共接口与权限

在 `maintenancetrust.Permit` 增加两个同步方法：

```go
PlanFundingLots(ctx context.Context, epochID string) (FundingLotsPlanResult, error)
InitializeFundingLots(ctx context.Context, expected FundingLotsExpected) (FundingLotsInitResult, error)
```

前者仅原 BeginPlan 的只读事务，后者仅原 BeginInit 的读写事务。两者都自行完成原 owner 的唯一 COMMIT，成功提交后才发布普通结果。所有失败（包括提交未知）返回零结果；原 `Snapshot.Outcome()` 保留事务结论。`FundingLotsExpected` 是普通比较值，恰含 EpochID、BasisDigest、ManifestDigest、DescriptorDigest、RuntimeProofDigest 五个字符串；非权限，不接收外部 Plan/Facts/Report/ScanProof/CanonicalV2、mode/schema/actor、Tx、callback、任意 SQL、skip 或 resume。EpochID 小写规范非零 UUID；四摘要小写 SHA-256。

PlanResult 含 Expected、纯规划 Metadata、SourceRows/SourceBytes（全68）、BasisRows/BasisBytes（109）。InitResult 含 Metadata、RuntimeProofDigest。EpochAt 为本 phase 在原事务内只读一次的有限微秒数据库 clock_timestamp；不接受调用者时间。两次 phase 的 EpochAt 可不同，ID 和 v2 原件不包含时间。调用者先 plan、复核其描述性结果，再在新的真实 Init Permit 下提交 expected；Init 必须重读全部事实并比较五个字段，不能使用 plan 的内存写集授写。

## 2. 生命周期

使用第八 once 位（128），不增加公开 ScanKind。认领在同一生命周期锁下同时消费 once、置 busy、安装不可移除的私有 `fundingLotsPhase` latch。无效参数也消费已认领阶段；并发被 busy 拒绝的调用不能撤销当前 owner。phase 只收紧 caller / snapshot 截止时间，累计最多30秒，任何子步骤不重置预算。

固定状态：pre → basis → draft → writing（仅 Init）→ post（仅 Init）→ ready → committing → done。原 busy 始终由该 owner 持有，覆盖所有 SQL、CPU 空隙、固定写入、post 和 COMMIT，直到 SQL owner 与取消 callback 完成交接。私有 rows 最多一个；不允许重入 query、未消费 EOF 或未关闭 rows 时换阶段。watcher 只撤销/关闭原 socket，不能并行操作 pgx Rows 或 rollback。

private state 持有原 snapshot/permit/authority 和 nonce、call、累计取消函数、seed/draft/basis/canonical/expected/epochAt、pending rows 与 ready 消费状态。撤销不清除 owner 尚在读取的不可变数据。公开 Snapshot.Commit 在 phase latch 存在且尚未终结时一律拒绝；即使 phase 失败并释放 busy 也不能绕过。未尝试本 phase 的原 T1 Commit 合同不变。只有私有 commitOwned 对精确 phase/nonce、ready、无 pending、原上下文/事务/permit 复核后，同锁一次消费 ready 和 terminal，调用原 rawTx.Commit。没有公开 ready 句柄或 ignoreBusy 开关。

失败保留封闭错误：原 Input/Identity/Schema/Transaction/Execution/Timeout/CommitUnknown，加 `ErrResource`；leaf 已识别 Resource 优先保留。提交前失败永久废弃该 snapshot/socket，不能补写或换 epoch 重试；未观察到确认 rollback 不伪称已回滚。Commit 结果沿用原 commitResult，丢回复保守 Unknown，绝不再次 COMMIT、自动补偿或重新开账。后续独立只读 unknown observer 另片实现，本片不宣称已确认未知结果。

## 3. 必须由本 phase 自行获取的前置证据

1. 原 Permit/installation/catalog/pins/实际 maintenance identity、完整22项 history、原 predecessor 与停服事实；发行时16表空的原门禁保留。
2. 原同 RR 完整68表读取和全部七算法资格，使用 ProjectLegacySeed 重核资格并独立 Build；禁止导入既有 ReadLease 的结果，禁止先 Finish 释放 busy。
3. 独立完整读取 tenant ID 与 `fl_created_xid xid8`，原 tenant 集合精确相等、每项 birth 均实际 NULL；它是原候选受保护的出生事务列，不是时间列。旧68并未读此列，不能用缺省值代替。
4. 本事务再核16张 C0 全空。原原生 ledger/资金/来源失败即拒绝。
5. 原同 RR 固定109表 basis（109/976/EOF/scope/manifest/descriptor/发送函数均严格核）。mode 仅取 owned predecessor，schema/名字/OID 仅取 owned pins/catalog，不能从 caller 推定。

basis 独立预算10万行/64MiB；全68行 + GeneratedRows <=100000，全68字节 + 固定 writer 投影字节 + 1MiB metadata 预留 <=16MiB；字段限额沿原 reader。写前重算完整12组数据投影，包含 DB 默认stamp 的保守费用，每 XID20B、时间32B，不用纯 seed 的九组预算替代全68。SettlementLots/RefundLots 必须恒空。仍须真实84post，不以估算替代实际预算和算法。

## 4. 私有 proof 发行与固定 writer

trust 可单向调用纯叶子 fundinglotscan、fundinglotinit、fundinglotbasis、fundinglotproof；不得反向依赖 maintenancescan，或向普通运行时暴露维护写接口。

v2 全部21字段从本 phase owned 证据构造：pins 的数据库/端口/名字/OID及所有 schema/role（保留 namespace owner 与 object owner 区别）；原 manifest canonical 的完整22项；本次真实 basis；同事务 catalog descriptorVersion/PGmajor/expected/schema/roles SHA；sandbox 原 v1 bytes 与SHA原样保留。严格 Encode 后 owned bytes/SHA 与 Decode 后全部绑定相等。epoch.descriptor_digest 与 attestation 同名列都等于 basis descriptor digest，不能混用 catalog 摘要。

唯一 writer 为 private phase 方法，只读取自己 draft/canonical，不接受 caller Plan。固定表、列、参数类型、schema quoting 和拓扑写在静态源码中，不使用 COPY、动态表/列、ON CONFLICT、savepoint 忽略错误、通用 batch 或任意 Exec 导出。所有值为白名单内建标量，金额精确 int64 或规范 NUMERIC 整数字符串，不能将 A+H / 跨tenant总额转成 int64。

固定顺序：LegacyReceipts → LegacySandboxReceipts → LegacySettlements → LegacyApprovedRefunds → LegacyOpenings → LegacySources → Operations → Heads → Lots → Events → Balances → ReservationLots → strict v2 attestation → epoch 最后 → SET CONSTRAINTS ALL IMMEDIATE。approved membership 的 immediate FK 直接引用原 settlements，不是 legacy settlement membership。每 INSERT 恰一行；有 RETURNING 的必须恰一行并达到真实 EOF。

Operations/Events/ReservationLots 省略 CreatedXID/CreatedAt，RETURNING 证明 DB 的 top xid 等于本 snapshot xid、timestamp 为有限微秒原生 PG 时间；Lots 省略 CreatedAt 并复核 DB 返回时间。stamp 可以与 EpochAt 不同，彼此也无需相等。opening.observed_at、lot.fifo_at、epoch.created_at、attest.created_at 共用本次 EpochAt。Epoch actor 按固定候选 CHECK 写入 `offline-maintenance` 操作类别，不接 caller 值；实际维护 LOGIN 由原 owned identity 及出口重读验证，该列不记录 LOGIN 名字。写入计数、NUMERIC总额来自自己 Metadata。只写16 C0 allowlist，不写 audit_events 或原109表，不修改旧 v1 原件。

## 5. 写后条件与提交

Init 完成固定写集与 deferred flush 后：

1. 原事务重新实际读取84表，scope/EOF/八算法/coverage/全部零 mismatch 与 unproven，wallet 重放亦必须 consistent；不得把纯Plan构造输入或仅 SQL checker 当实际 post。
2. 自读 epoch/attestation 单例（先限 bytea），strict DecodeV2、SHA 与逐项21字段/嵌套 manifest、sandbox/v1原件比对；双向比较 epoch/attest 的 basis、manifest、descriptor、角色/OID、固定 actor 类别、Metadata 计数与 NUMERIC总额。ReadWallet 不严格解码 v2，此步骤不可省略。
3. 再次 VerifySnapshotTx 实际重收集静态 catalog，并与原 proof/pins/发行 v2 各 root 一致；原 CatalogProof.VerifyTx 只复核事务绑定，不能代替此操作。重新核 history/实际 identity/停服事实。
4. 同事务再真实读109 basis，全部报告与写前一致，防止84未覆盖的41张表被改变。该比较仅限本初始化事务；不可推广到后续经营 startup/observer，经营自然会改变业务数据。
5. 全部条件满足才置私有 ready、原 owner 唯一 COMMIT。Plan 不写入，完成同等 pre/basis/issuer/预算及出口证据后置 ready 并提交其只读事务。

## 6. 验收与交付

### 旧 TEXT tenant 的校准（2026-11-03）

原数据库 tenant 主键为 TEXT；旧身份可以是合法 UTF-8 空串，不能把它和 SQL NULL 或平台账户混同。完整来源七算法与纯 planner 必须采用相同边界：只对 tenant 主键及原账户、资金来源、请求、attempt 的 tenant 外键采用允许空串的词法校验、原字段/全局预算及精确集合/关联比较。tenant 索引必须保留空键并检测重复；账户的 nil tenant 继续仅表示平台，非 nil 空串表示该旧 tenant。不能以丢掉该 tenant、补默认身份或跳过任一算法解决不一致。

范围包含采购 binding/approval/cost-journal/metering/finalization 的 tenant 外键，以及周期预算 tenant target/key configure scope；key/procurement 自身 target 仍守原 UUID 限制。Sandbox commerce 依据固定命令 kind 区分 platform 与 tenant scope，不能拿空 tenant 值作 platform 哨兵；其精确 tenant membership、原源单及其他合法性仍照常核验。

这不是新业务输入放宽：用户/账户/journal/来源/命令/event 等对象自身 ID、其非 tenant 引用、金额、状态、原 voucher 的 UUID tenant scope、sandbox 的 UUID 身份合同保持原限制。仅按旧关系确实成立的 TEXT 身份接纳历史，不制造新 voucher 或支付调用的合法性。验收至少覆盖无钱包旧空 tenant、重复空 tenant 拒绝、空 tenant 的有资金 manual 关系及原请求/attempt 关系，且损坏的关联仍被拒绝；真实 mixed API fixture 仍使用可被原在线 API 接纳的 UUID tenant，真实零钱包 fixture 单独覆盖空 TEXT。

真实专库串行验证 genuine ordinary / sandbox（原001–022+固定候选DDL/ACL），先 Plan 成功、新事务 Init expected 成功，再从独立连接读取实际 C0 集合/strict v2及84图；原109 basis及 sandbox v1 bytes不变。覆盖空安装、零wallet、R0 unfinished/terminal、混合来源和分次退款、大NUMERIC合计、合法空旧TEXT身份。独立负例覆盖 expected漂移、birth非NULL、已开账、缺/错误成员或proof、catalog漂移、post损坏、预算、取消与busy、phase失败后公开Commit绕过、commit丢回复。不能只用fake SQL或purePlan证明真实写入。

原门禁、普通服务依赖隔离、旧7种扫描与通用T1提交回归保留。专库无SKIP专项、make check、make verify-mvp、源码身份/验证记录、统一架构/HLD/LLD/README及GitHub推送共同交付。库级候选安装成功不宣称正式迁移、CLI、在线资金开关、unknown observer或生产验收完成。

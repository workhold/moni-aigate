# 022-C0-V：独立来源 lot 核验与全钱包重放合同（审查候选）

2026-11-01。**完整核验器仍为精确设计候选，未分配迁移号、未接入资金或普通 main。根已冻结§12的V0-L纯账本行检查及另册[V0-R同RR账本reader](c0-ledger-reader-contract.md)；两者已实现并通过各自[纯层](../../docs/verification/funding-ledger-rows.md)/[实际PG读取](../../docs/verification/funding-ledger-reader.md)专项。其余reader、来源/lot重放与证明组合仍未冻结、未实现。** 本文细化 [lot 合同](c0-lot-contract.md)、[全序合同](c0-sequence-contract.md)、[生产 DDL 候选](c0-production-schema.sql)、[列 ACL 候选](c0-production-permissions.sql)及[维护与 proof v2](c0-maintenance-contract.md)。[纯分配算法](../../internal/billing/lot_plan.go)已有实现，但不是本核验器的正确性来源。迁移清单接缝以已冻结的 [C0-S](c0-snapshot-contract.md) 为准；旧全序稿中的 `SnapshotRequirement` 示意参数不属于当前 `migrations.VerifySnapshotTx(ctx,tx)` 签名。

依据 [HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)与 [023-B verifier](../023-periodic-products/b-verifier-contract.md)。本片没有原路退款、拒付、媒体收费或多活的实施授权；核验成功不发出模型/支付调用许可，不修账或重建来源。

## 1. 证明范围与独立性

核验器从真实来源与不可变序列独立推导应有状态，再比较 lot、事件、分配和缓存。禁止调用 `planReserveFundingLots`、`planFinishFundingLots`、`planRefundFundingLots`，禁止把 `fl_check_*`、`fl_validate_*`、`fl_source_*`、`funding_journal_valid`、`payment_*_valid` 等数据库写侧谓词的返回值作为业务正确性 oracle。可以读其受审定义进行对象完整性验证，但业务结论必须来自独立静态读取和 Go 判断。不得 import provider、HTTP、SMTP 或支付 mutation adapter。

受审共享设施只限：pgx 类型/静态标识符引用、完整迁移清单核验、固定安全错误/UTC6格式和未来不可伪造的维护 Permit。金额运算使用独立 `math/big.Int` 表达式；不共用写侧分配函数或写侧 SQL 聚合结果。读到的 `before/after/delta/event_count/revision/last_event` 都是待核声明，不是重放输入的真值。

本合同证明 C0 epoch 起的来源资格与全部钱包分配。cutover 前缺少 allocation 的真实消费历史只核原财务来源、journal、终态、已退累计及精确维护集合；不声称恢复其历史 FIFO 或支付可退资格。保守 `nonrefundable` opening 是已经声明的范围边界；完整维护证据成立时，它本身不构成 coverage 缺口。缺失旧集合、未知 cutover 身份或无法证明旧新互斥，必须显式计为缺口或确定不符，不能以保守资格掩盖。

## 2. 候选接口与事务所有权

下列签名仅占设计命名空间，未来须由根单独冻结后实施：

```go
// billing：公开便利入口，自己持有一个完整 RR READ ONLY。
func (s *Store) VerifyLots(ctx context.Context, tenantID string) (LotCheck, error)

// billing 私有接缝：VerifyLedger/Rebuild 的同一个 caller-owned RR。
func verifyLotsTx(ctx context.Context, tx pgx.Tx, tenantID string) (LotCheck, error)

// 未来维护入口；不要求调用方把 pool 或自报身份交给 scanner。
func VerifyLotsMaintenanceTx(ctx context.Context, tx pgx.Tx,
    permit *maintenancetrust.Permit) (LotCheck, error)

// init 写 C0 facts 前的全局原源核验；结果不冒称 lot 已建立。
func VerifyFundingSourcesMaintenanceTx(ctx context.Context, tx pgx.Tx,
    permit *maintenancetrust.Permit) (LotSourceCheck, error)
```

`tenantID=""` 为全局，否则按原 TEXT 身份精确匹配一个已存在 tenant，不 trim、大小写改写或把历史身份强制转 UUID；拒绝无效 UTF8/NUL、不存在的范围及 nil/失效 context。此内部核验入口不是新增 HTTP API，租户身份不由请求参数绕过原授权。

`VerifyLots` 在受限 APP 实际 LOGIN 上 Acquire、Begin RR READ ONLY、设置有界本地查询预算、核完整 migration/schema/角色/epoch，最后只有 COMMIT 确认才返回结果；失败用独立至多5秒 cleanup 回滚，非 idle 连接物理移出池。默认总预算30秒，可在受控构造时选1秒至10分钟；没有“超时后返回已扫部分 balanced”选项，也不重试失败的全库核验。

所有 `*Tx` 入口只使用传入的有效 RR，允许 READ ONLY 或 READ WRITE，不 Begin/Commit/Rollback、换连接、写表/临时表、执行 SET ROLE、SAVEPOINT 或取得业务/维护锁。它们不修改调用方 GUC；调用方负责在第一条扫描前设定 `statement_timeout/lock_timeout` 与整个 context deadline，内部静态查询全限定 schema。成功或确定数据不符后 caller 仍拥有原事务；数据库异常后的事务失败沿 PostgreSQL 规则处理，不偷建新快照或自动修复。tx 已关闭、RC/Serializable、取消/执行/权限失败均为安全错误和零结果。

普通私有接缝只接受同 RR 内完成的静态 runtime C0 schema/role/epoch 核验，不能因 APP 有 SELECT 就承认 maintenance 权限。维护两个入口必须每次调用 `Permit.VerifyTx`，绑定原顶层 wrapper、conn/PID、top xid、DB/schema、模式、proof 与实际 gate；nil/零值、另 Tx/保存点 wrapper、提交后复用均拒绝。Permit 不存在时不提供临时 trusted bool、任意角色名单或 owner fallback。维护入口只允许全局，不能用单租户结果批准整库 init。

`migrations.VerifyReadOnlyTx` 仍保持 RR READ ONLY；RR 写维护仅用 `VerifySnapshotTx` 检查完整当前嵌入 history。两者都不替代对象正文、权限、角色闭包、Permit 或源图证明。C0 schema 缺失、epoch 未发行、proof 无效时 `VerifyLots` 返回固定错误；pre-epoch 原源入口不会为缺 epoch 返回伪造空 lot balanced。

## 3. 结果、三态与缺口分类

候选 `LotCheck` 封闭字段：`SchemaVersion=1, Phase=post_epoch_lots, AsOf(UTC6), Scope(global|tenant), Status, Balanced, CoverageComplete, Scanned, Mismatches, Unproven`。不输出 tenant/receipt/capture/order/lot ID、邮箱、凭据、证据正文或错误 SQL。所有计数为非负 `int64`，JSON 十进制字符串；内部累加溢出返回安全 execution 错误与零结果。

`Scanned` 至少记录 `Tenants, Epochs, Attestations, LegacyReceipts, LegacySandboxReceipts, LegacySettlements, LegacyApprovedRefunds, LegacyOpenings, LegacySources, WalletOperations, WalletHeads, Lots, Events, ReservationAllocations, SettlementAllocations, RefundAllocations, FundingSources, Reservations, Settlements, RefundOrders, WalletJournals, JournalLines, Audits, OutboxEvents`。扫描计数不证明关系正确，不能用相等计数替代双向集合比较。

`Mismatches` 保留已有合同的十五方向，并明确计数单位：

| 字段 | 单位与确定不符 |
| --- | --- |
| `EpochMismatches` | epoch/attestation/维护关系键：计数、OID、固定版本、成员或摘要绑定不符；不可用 schema/proof 的前置失败另返回错误 |
| `GenesisMismatches` | tenant：旧 available opening 缺多、旧 tenant 被记为新 genesis、新 tenant 带 legacy opening、零额 genesis 有效果 |
| `WalletSequenceMismatches` | tenant 或 operation：起点/连续性/前驱/后继/epoch/head 不符、未推进尾、journal 重用 |
| `WalletSourceMismatches` | `(tenant,kind,source_id)`：真实源/tenant/原 journal operation/金额/同事务绑定或 audit/outbox 声明不符 |
| `OriginMismatches` | lot：本金、资格、FIFO时刻、来源互斥、同来源多 lot、错误 receipt/opening/refund 绑定 |
| `FIFOAllocationMismatches` | reservation：与操作前独立钱包状态的最短正额 FIFO prefix 不同 |
| `ReservationMismatches` | reservation：R、held/terminal 状态、完整原边或零额语义不符 |
| `SettlementMismatches` | settlement：非唯一、原 R/C/status/journal、完整终态边、正序收费/release 不符 |
| `RefundOrderMismatches` | approved refund：逆原 charged 边的次序、此前累计与当笔完整边不同 |
| `RefundMismatches` | refund/settlement：pending 有效果、approved 缺图、累计或 cutover 基线不符、Key/周期 spent 的退款语义不符 |
| `EventChainMismatches` | event：所属 operation、kind/journal、前驱/revision、独立推导的 before/after/delta 不符 |
| `BalanceMismatches` | lot 或 tenant：lot cache/末事件、三桶本金守恒、账户 AVAILABLE/HELD、unfinished held 不符 |
| `LegacyBoundaryMismatches` | 维护集合键：精确源↔membership 不等、legacy/new 重叠、旧 approved 被再次发行、错 tenant/epoch |
| `MissingFacts` | 预期事实键：已由合法源推导且应存在的某一事实缺失 |
| `OrphanFacts` | 实际事实键：扫描完成仍未被任何合法来源/operation 消费的事实 |

同一事实在同一方向最多记一次，以不可变主键/关系键去重；它可能同时违反不同方向，方向计数不作财务金额求和。必需事实缺失时可同时记录 MissingFacts 与所属领域方向，不把 join 放大行数当缺口数量。依赖损坏后不能继续确定的后缀不生成猜测金额。

`Unproven` 固定为：`LegacyMembershipUnproven`（可读旧事实尚无精确 cutover 归属证明）、`SourceLifetimeUnproven`（无法证明源与新 operation 的同事务/不可变生命周期）、`ReplayPrefixUnproven`（某 tenant 的前缀依赖不成立，后缀无法完整重放）、`CrossDomainCoverageUnproven`（维护全源所依赖的 ledger/funding/PB/B 核验确实完成但其自身返回 coverage 缺口）。缺权限、缺表/列、未知 schema 版本、查询异常或未完成扫描不能转换成这些计数，必须返回错误。

三态在完整扫描后裁决：

1. 任一确定 mismatch 非零：`Status=mismatch, Balanced=false`；CoverageComplete 独立取全部 Unproven=0，不能因已有不符而隐藏 coverage 缺口。
2. mismatch 全零但任一 Unproven 非零：`Status=indeterminate, Balanced=false, CoverageComplete=false`。
3. 全零：`Status=balanced, Balanced=true, CoverageComplete=true`。

epoch/来源旧新证明不足不能默认为第三态。`LotSourceCheck` 采用相同三态与安全计数原则，但明确 `Phase=pre_epoch_sources`，其 Balanced 只表示该phase原源与维护依赖完整，不表示lot已经存在或C0可运行。init 前后都要求相应 phase 的 balanced 且 CoverageComplete；数据库/权限/取消/COMMIT失败返回整个零值 result 和固定错误，无 partial result、driver unwrap 或原行值。

## 4. 扫描协议与实际读取边界

schema/role checker 先接受静态闭合对象描述符，scanner 只使用其受审 schema 标识符和明确列列表。不能 runtime `SELECT *`、catalog 自动扩列、按名字前缀猜业务表或信任调用方 table/schema 字符串。扫描不写数据库索引/临时表；必须索引的新增关系随最终 DDL 另审。

固定页大小128，使用 keyset、全程同一个 RR。tenant 以 `id COLLATE "C"`；operation 以 `(tenant_id COLLATE "C",seq,id COLLATE "C")`；lot 以 `(tenant_id,id COLLATE "C")`；event 以 `(tenant_id,wallet_seq,lot_id COLLATE "C",id COLLATE "C")`；分配以其父ID、allocation_index及主键C序。不能 OFFSET、按 created_at 猜顺序、只扫前N行或 LIMIT 后把未扫尾页判通过。每页 Rows 完整关闭并检查 `Rows.Err`，关闭后才发下条查询；取消/scan 类型错误立即安全退出。

每个 tenant 先加载自身所有来源索引、lot/事件/分配事实及账户/头，按 seq 重放后释放内存。来源读取逐 kind 使用独立静态 SQL：

| 源 | 独立读取的必要事实 |
| --- | --- |
| manual | receipt、credit approved/amount/双人身份/journal、精确 journal 两行、approval audit 与唯一 credited |
| voucher | receipt/redemption/voucher/batch 的身份/金额/生效区间、原 commerce command+accepted receipt、redeemed audit、精确两行与 credited；不读券码/密文 |
| sandbox | receipt/capture、原环境/订单/SKU金额、identity、capture selection/effect、原证据与processing代数/终态、audit、精确两行与 credited；完整B独立验证依赖尚未实现时不标全源通过 |
| reserve | request/reservation/Key 与 tenant 关系、R/status、原 journal header及精确 AVAILABLE/HELD 两行（含R=0）、reserved outbox、reserve audit |
| finish | 唯一 settlement、原 reservation/request 终态、R/C与refunded累计、原两/三行、终态 audit/outbox及所有原 allocation |
| refund | pending/approved refund、settlement/原request、双人事实、精确 AVAILABLE/REVENUE两行、approval audit/refunded outbox、旧已退基线及全部已批准金额 |

每个真实钱包 journal 正向精确归属一个源；所有 AVAILABLE/HELD journal line 反向枚举，连零值两行也不能略过；非钱包采购 journal 不误要求 lot，任何未知 kind 碰到客户钱包科目必须确定不符。枚举 header/line/audit/outbox 的额外事实，不以 EXISTS 一条正确行忽略同源多余效果。单租户范围只核本 tenant 的客户科目和对应共享科目**分录**，不拿其小计与整个平台 clearing 余额比较；共享 clearing 全额只由全局资金核验负责。

为发现跨 tenant 错边，所有实际事实按其声明 tenant 独立枚举；关系引用使用同 tenant 复合键，跨 tenant 引用计确定不符，不把另 tenant 私密内容加入结果。全局额外 orphan sweep 从每张 C0事实表及钱包源表出发 LEFT JOIN 所需父关系，不能只从合法 parents INNER JOIN 后扫描而丢掉孤儿。租户 scope 也扫描本 tenant 声明事实及其源反向引用；全局 sweep 仍是整库证明的必要部分，租户通过不等于整库通过。

## 5. epoch 与 legacy 起始状态

先验证单例 epoch/attestation 与 DB/schema/当前受审 schema version、manifest/descriptor/runtime proof 精确绑定；sandbox 追加验证原 immutable v1 predecessor 的内容和 OID，ordinary 不得有 sandbox marker。不能只核 SHA 格式或重新hash当前catalog后自动接受。Basis digest 是维护承诺，运行时不能对已变业务表重算原 cutover basis 假称可重现；其真实性依初始化同 RR 的全源验证和不可变维护记录。

旧 tenant 必须由受保护 birth/membership 事实识别，不用 `created_at<epoch`：当前候选 `tenants.fl_created_xid IS NULL` 对应旧 tenant，每个恰有 available opening（含0）和 seq0 epoch_opening；非NULL的新 tenant 只允许同 top-xid 的 tenant_genesis，零事件/分配/delta。不仅比较总数，要双向比较实际 tenant key 集合。该列/trigger/ACL仍随生产候选根审，不能在旧 schema 上猜默认归属。

全部 legacy receipts、terminal settlements、approved refunds、wallet_sources及窄 sandbox receipts互为精确集合。legacy wallet_sources 包含旧 terminal 的 reserve、零额 reserve/finish；旧 reserve 必须指向旧 terminal 或自己的 unfinished opening。旧 approved退款计入开账，不新建lot。旧 pending refund只保留无效果事实，后批按原 settlement 是旧/新决定退款模式。

每个正 available opening建立一个内存 lot：A=P/H=S=0；每个正 unfinished reservation opening 建其专属 lot：H=P/A=S=0，并建立唯一原 reservation allocation index1。零额 opening保存成员但没有 lot/event/allocation。所有 opening lot均 nonrefundable，FIFO时刻为同 epoch时刻，lot ID按C序打破同刻。每旧 tenant A/H opening与原全部legacy wallet journal的NUMERIC前缀、当时未终态reservation集合及旧已退基线精确相符；不先将A+H合成int64。

## 6. 按全钱包 seq 的独立重放

独立状态包括每 lot 的本金/资格/FIFO key、A/H/S、预期事件尾/版本；每 reservation 的原有序分配及是否结束；每 settlement 的原 charged 边和截至当前seq的退款累计；已消费的源/事件/分配键集合。lot与原边状态从真实源推导，不从balance或实际allocation自我初始化。

1. **序列骨架**：每 tenant seq0唯一；seq从0连续、前驱精确seq−1且同tenant/epoch、每前驱至多一后继；tail恰为全部operation最大seq/id。非genesis operation恰绑一个真实source/原journal；source不可同时legacy/new，journal不可重用。BIGINT MAX用大整数检查后拒绝继续，不wrap。created_at/xid都不用于跨operation排序。
2. **新入账**：从receipt/source独立推导amount、原journal、资格和FIFO时刻；该receipt必须不在legacy集合，恰一个新lot/初始event/cache。manual/voucher=nonrefundable，sandbox=sandbox_original；amount>0且P=amount。同来源重读不生成第二operation/本金。
3. **reserve**：在本operation之前的独立内存A>0集合中，按(fifo_at,lot_id C序)排序，依次取min(A,remaining R)，直到R归零；得到唯一最短正额prefix，与持久allocation的lot/index/amount逐项比较。不得把实际allocation的lot列表当候选集合。R=0仍核原两行journal和唯一operation，0allocation/event。若钱包源R大于独立可用额，确定不符，不借后续funding补足。
4. **finish**：只取先前reserve或legacy opening推导的完整原边，要求0≤C≤R。按原index升序，逐边以min(q,remaining C)收费，其余释放；每原边必须有一条terminal，即使该边charge=0；合计A+=R−C/H−=R/S+=C。不重新选lot；R=0仍核零journal/operation，0边/event。terminal不允许第二份finish，pending不改lot桶。
5. **新来源消费退款**：只用原finish推导的charged边，从原allocation_index降序逐条按`charged−截至此前seq已退`取额。比较当笔退款index、原settlement_lot、lot、金额与完整events；S−=F/A+=F，资格/FIFO key不变。此前已退从先前合法seq累计，不信任当前全部refund表SUM来决定历史可用。分次F1/F2累计必须等价于一次F1+F2。
6. **旧terminal后新批退款**：先核旧settlement C、cutover已退F0及所有旧approved精确集合；按seq累计新批F，始终F0+ΣF≤C。当笔只新建一份nonrefundable legacy_refund lot，P=A=F/H=S=0，FIFO时刻绑定受保护operation stamp；不伪造旧charged分配。每一旧approved均禁止重发lot。
7. **比较本次声明**：依据独立预期变化生成应有event集合和四delta，逐条比kind、journal/原operation、新walletoperation/seq、before/after桶、前驱/revision。event_count与实际/预期集合相等；只有初始event issued_delta=P，后续为0。operation四delta为本次预期NUMERIC总和，不只核总和0。
8. **终态与双向扫尾**：每lot独立A/H/S非负且P=A+H+S，比较cache/revision/last_event；tenant A/H与当前两账户、所有unfinished原R精确相符。所有settlement的SUM(approved金额)=refunded_amount≤C；新边累计与总refund合计相符，legacy=F0+后续seq累计。所有实际源/lot/event/三类边均被消费一次，剩余计orphan；所有预期键缺失计missing。Key/周期核验分别验证原C累计，消费退款不恢复spent；不拿lot净消费S推回Key/PB spent。

确定坏前缀后，该tenant记录对应Mismatch和ReplayPrefixUnproven；仍完成其实际事实枚举与孤儿统计，并继续其他tenant，不用错误cache或后续event before值恢复“可信状态”。若资源/查询预算不足以完成枚举则整体error+零结果，不能只返回已检部分。

## 7. 正常流程的关键不变量与生命周期证据

post-epoch operation必须与实际新钱包source/journal同事务，不能只ID/金额相等。拟独立比较不可变 `journals.created_txid` 与operation受保护`created_xid`的top-xid数值；legacy关系则只能由完整cutover成员证明。SAVEPOINT元组xmin可能为subxid，因此不比较tuple xmin，不把所有同xid操作合并。原journal stamp的列ACL/触发器/默认与Go写入顺序能否提供该证明，须随最终schema/权限审查明确；未固定前不可冻结来源生命周期覆盖。若未来允许合法跨事务source完成，必须先增加精确生命周期证明，不能静默删除同事务检查。

每个approved refund的source、精确journal、operation、lot边/event与settlement累计须同一事务完成；检查入口独立核`SUM(approved amount)::NUMERIC=settlement.refunded_amount≤charged_amount`，不依赖调用方“应当更新settlement”作为证据。pending单有无allocation必须和无journal/无批准一致。旧terminal新批退款的历史基线不能重新根据当前refunded推测。

正常回归至少包括：C=100、cutover已退20、后批30和50后refunded依次50/100，旧approved重读无新lot，之后1按业务余量拒绝；新来源跨3lot部分C、连续消费退款与一次累计等价；C=0/R=0的完整零额journal与序列；cutover unfinished正常finish、旧pending refund后来审批；同一事务在SAVEPOINT内连续两次合法source及回滚不遗留seq/head。预期值由手算/独立大整数表给出，不调用写侧planner得到expected。

## 8. 复杂度、资源与扫描可完成性

设某tenant有L个lot、O个walletoperation、E个event、A条分配及S份来源事实，V=L+O+E+A+S。第一版明确使用每次reserve对当时内存lot排序的直接算法：总时间上界O(V + Σreserve L log L + A)，内存O(V)逐tenant释放；全局多tenant不同时保留全部图。它更易独立审阅，不能声称已经线性或满足在线p95。后续受审平衡树/精确有序索引可优化，不改变查询完整性或期望值。

不做无限内存承诺。候选默认每tenant最多1,000,000条已解码事实/256MiB核验自有buffer、全局最多10,000,000条事实；金额`big.Int`和字符串长度计入预算，统计容器及duplicate-key集合亦计入。配置只能由受控构造器在明确硬最大值内调节，不能通过HTTP扩大；实施前须根裁决具体硬界并测内存估计。达到预算整体返回固定resource错误和零结果，不分页换snapshot、不截尾返回indeterminate或balanced。大tenant离线外排序/分阶段证据若需要另设计。

固定page不是总数据上限。全局orphan/source反向查询须有受审索引/EXPLAIN，避免每operation重新向数据库扫完整历史；预期数据库往返按各关系keyset页数加固定proof查询计，不每条事实N+1查父。不能为避免内存放大改用写侧SQL validator代证。1/128/129/1000+lot、长历史及时间/内存取消都需专项，数据量门禁与性能数据独立记录，不外推为网关首字节或供应商TTFT。

## 9. 冻结阻断与后续并行边界

以下未完成前不授权实现完整Verifier或把C0标为已接线：

1. 最终生产DDL/列ACL/tenant birth、旧集合、refund累计和每种新source的top-xid/生命周期证明冻结；schema/role checker及所有实际LOGIN精确矩阵完成。现有001–022 SQL保持不改。
2. 独立source scanner逐字段清单与B完整证据状态语义受审；不得复用写侧payment predicate作oracle。ledger/funding/采购/PB/B在caller-owned同一RR的全源接缝明确，未来Permit实现单独冻结。它们任一缺口阻断维护init，不以本lots局部balanced代替。
3. 已冻结C0-S只证明migration history；完整C0对象/权限与Permit尚无实现。pre-epoch/post-init两phase的状态组合、错误映射及已有VerifyLedger/Rebuild结果兼容由根裁决，避免旧boolean接口忽略CoverageComplete。
4. 页游标/显式列SQL、所需索引、资源硬界与长历史复杂度有真实PG计划和量测；正常流程、多tenant、零额、旧未结/旧pending、分次退款及独立期望的测试矩阵受审。
5. public wrapper、caller-owned seam与维护fixture的作用域明确；APP独立全读验证不扩大worker读取A，projector零C0权限。owner用于安装和受控历史fixture，不能替代实际APP验收。

合同冻结后可并行：纯Go独立重放与手算fixture；封闭只读scanner/角色负矩阵；同RR/Permit接缝审查与生命周期运行器设计。生产source/helper/DDL、Reserve/finish/refund、维护init及root架构集成仍按单一负责人串行。任何实现修改先同步本合同和统一架构能力状态；由根汇合后refresh，本文件不触发manifest刷新。

## 10. 独立 core、同 tx 组合与 coverage 精化（根审候选）

### 10.1 现有检查的实际边界

[现 VerifyLedger](../../internal/billing/query.go)自行Begin RR RO，SQL比较账户cache与分录SUM、每journal至少两行且和为0，再调用[现 verifyFundingTx](../../internal/billing/funding_verify.go)。后者的十个计数大量调用`funding_journal_valid/payment_identity_valid/payment_capture_evidence_valid/payment_sandbox_journal_valid`等写侧谓词；`FundingCheck/LedgerCheck`也没有三态和CoverageComplete。它们保留原兼容行为，可作新核验之外的回归，**不能成为独立source core、不能被赋值转换成新的完整结果，也不能为RuntimeProof填充通过位**。

[现 Rebuild](../../internal/billing/rebuild.go)最后的切换事务不是本合同的RR只读scanner；在其现有事务内拼接新旧boolean仍不能得到全源证明。任何未来重构须单独冻结，保留原投影切换门禁，并明确核验发生在同一受控维护快照；本片不修改它。不能调用public VerifyLedger/VerifyFunding/VerifyLots或PB public Verify另开事务后拼结果，也不能先读report后再Begin一个空RR给其补tx身份。

组合顺序固定为：受审catalog/完整migration及当前phase身份 → 原始ledger → 独立funding/source → Key/采购/PB/B交叉事实 → post-epoch时的lot/全序重放 → 反向扫尾 → phase三态。每一项必须完成；没有“ledger已平所以跳过来源”“已有mismatch所以跳过剩余扫描”或缺依赖时降级通过的分支。每个core只读取受审静态原始列和在本次扫描内得到的前置值，不调用写侧planner、SQL业务checker或运行helper；可共享只读行模型与同tx静态查询设施，不能共享决定FIFO/charge/refund的写侧算法。

### 10.2 只有内部完成记录，不增加权限或proof家族

根未来在billing只读文件内冻结以下**私有编排**；类型名只用于说明实现责任，不是新增导出能力：

```go
// 下列类型及函数均未授权实现；字段私有，不能由caller提供report/bool构造。
type lotReadSession struct { /* same tx/conn/identity/catalog/phase/scope/deadline */ }
type lotScanCompletion struct { /* session pointer, exact reader set, EOF, counts */ }

func scanLedgerRowsTx(context.Context, *lotReadSession) (ledgerRowsCheck, *lotScanCompletion, error)
func scanFundingSourcesTx(context.Context, *lotReadSession) (sourceGraphCheck, *lotScanCompletion, error)
func scanWalletLotsTx(context.Context, *lotReadSession) (LotCheck, *lotScanCompletion, error)
```

这些记录只是组合函数的内部工作状态，**没有签名、序列化、公共FromReport/FromDigest/Trusted构造器或跨调用复用能力**。现有`CatalogProof/InstallationProof/Permit/RuntimeProof`的职责保持；不能为扫描再建一套可由调用方装配的permission/proof。issuer自己按固定调用图执行core并核完成记录，外部API不接受这些记录或任意`Balanced/CoverageComplete`参数。纯层§12返回的结果也不能转成完成记录。

`lotReadSession`只能在§2现有受审入口内部建立：runtime入口先核actual APP、同RR RO的CatalogProof和严格v2/epoch；maintenance入口先核同Snapshot.Tx的Permit及phase。构造时保存精确pgx.Tx wrapper、Conn/PgConn、PID/backend_start、top xid、DB/schema OID、current/session实际role OID、RR access mode、原catalog/epoch或installation摘要、phase、scope和总deadline。每个core进入与返回前重核同一wrapper/连接/活跃事务/context及原opaque证据；失败返回整个零结果。保存的xid只是绑定本次核验事务，不要求历史journal.xid等于当前xid。

runtime issuer仍按[schema合同§7.1](c0-schema-check-contract.md#71-完整-runtimeproof-的独立组合层)只在actual APP RR RO中调用私有core；maintenance post-init只在原Permit的RR RW中调用相同只读core。后者能看到本事务刚写入的epoch/lot，但结果永远不是APP RuntimeProof；init COMMIT后APP必须新开RR RO再核全部内容。维护Permit的VerifyTx不能因写后C0表不再空而失效，原InstallationProof不重复要求空集，依据[Permit合同](c0-permit-contract.md)。scanner不发行Permit，不扩worker/projector读权。

内部完成记录逐项保存实际已执行的固定reader ID、每个reader已检查Rows.Err且读到EOF的事实、scanned数量和双向关系sweep完成位；这些位只由reader自身推进，caller不能传“已读全”。同一session的单项结果只使用一次，不能拿tenant A、另一个RR、另一phase或早前成功运行的记录补齐当前扫描。并发scanner/结束事务均禁止；不以`AsOf`或摘要相同判断同tx。

### 10.3 必需覆盖和三态组合

两个phase都要求全局原源核验。最终组合中的逻辑域封闭为`Ledger, FundingSources, CustomerKeys, Procurement, PeriodicBudgets, SandboxFacts, WalletLots`，不是允许未知名称的map。Ledger与FundingSources核心由新独立raw reader/Go判断负责；Key/采购/PB/B的独立精确reader可按后续根冻接缝实现。所有必需域必须在同session完成，不能接受外部公共API的完成report。

| 域 | pre_epoch_sources | post_epoch_lots |
| --- | --- | --- |
| Ledger / FundingSources | 全局原001–022完整事实；C0安装表仍空 | 全局原事实和新旧source归属 |
| CustomerKeys / Procurement / PeriodicBudgets | 原held/spent、来源、事件、legacy及coverage，均必需 | 同上，另核C0未改变其原语义 |
| SandboxFacts | ordinary须以受审mode证明marker/支付事实集合均不适用；sandbox必需完整本地图 | 同上，并核immutable v1前驱与v2/epoch关系 |
| WalletLots | 固定不适用；完成原源不等于lot存在 | 本文§5–7全钱包重放及每张C0表反向扫尾必需 |

不适用不是scanner返回空数据后猜出的通过。它只能由已受审phase/mode决定，内部标为`excluded_by_phase`或`excluded_by_ordinary_mode`，结果中不产生虚构扫描计数；ordinary意外存在支付marker/事实为确定不符，catalog模式本身不一致则为前置schema错误。最终RuntimeProof及init决策只接受全局结果；单tenant诊断不能为整库覆盖发通过位。

`CoverageComplete`是**本合同承诺的持久事实关系覆盖**：所需reader全部实施并完整枚举、所有必需反向关系完成、所有受审证据生命周期/legacy边界可证明、必需域没有其合同已定义的Unproven。它不承诺重算历史模型内容、供应商实际账单、真实PSP资金、被owner任意篡改前的事实或库外丢失区间。对本来不在承诺范围的外部真实性，不额外记一条gap；对本来必须有的内部事实，不能以“只做本地验证”免核。

依赖尚未实现、缺权限、未完成EOF、未知schema/字段、预算耗尽或查询失败是**执行/依赖错误+零结果**，不转换成indeterminate，也不假填`CrossDomainCoverageUnproven`。后者只接收同tx实际完整执行的独立域所返回的已定义历史coverage缺口。可读事实的确定矛盾照常mismatch；依赖损坏使某tenant后缀不可推导，则同时保留对应mismatch与`ReplayPrefixUnproven`，仍枚举实际后缀及其他tenant，不猜账。

三态裁决仍为§3：有确定mismatch优先mismatch，coverage另算；无mismatch但有真正数据证据缺口为indeterminate；全部为零且reader完成才balanced。运行错误没有第四种部分report。public便利wrapper只在同RR COMMIT确认后返回成功report；caller-owned core不拥有事务，不将成功读取误称COMMIT确认。报告允许暴露的只有固定phase/scope/as-of和安全计数，无行ID或原文。

当前PB[公共Verifier](../../internal/periodicbudget/verifier.go)自己Begin/Commit，且对六PB表要求actual LOGIN实际owner；它不是actual APP的同RR入口。后续必须另冻caller-owned独立core及维护/APP受审身份适配，不能削弱原public owner规则、临时SET ROLE或复制旧公开report来填本域。在该接缝和完整采购/B/Key读图未实现前，全源组合器仍不得实施通过路径。

## 11. 原始数据读取与本地证据边界（根审候选）

### 11.1 静态reader原则与ledger精确投影

reader SQL只负责枚举/取列/精确keyset与SQL类型转text，不返回业务`valid/balanced`、预聚合金额、数据库判断的expected allocation或隐藏坏行的inner join。Go对原始行独立建索引、求和、核关系。可以另以固定LEFT JOIN/NOT EXISTS查结构孤儿，但原表完整枚举仍必需，join结果不能代替本表EOF。所有SQL在受审schema内显式全限定表名和列，schema名仅取自已验证证据并用pgx.Identifier引用；不能由HTTP/plan自报SQL。

全局Ledger reader固定读取以下三份**全部行**，包括无钱包效果的journal、零分录金额、所有平台clearing/revenue账户和没有分录的账户：

| reader ID | 显式列及顺序 | keyset |
| --- | --- | --- |
| `ledger.accounts.v1` | `id,tenant_id,kind,balance::text` | `id COLLATE "C"` |
| `ledger.journals.v1` | `id,tenant_id,operation_id,kind,reference,actor,currency,created_txid::text,created_at` | `id COLLATE "C"` |
| `ledger.lines.v1` | `id,journal_id,account_id,signed_delta::text` | `id COLLATE "C"` |

三份都从自身表出发，无WHERE kind/tenant/余额非零过滤，无`SUM/COUNT`结果作为Go账务判断。分页固定128。第一页使用私有`hasCursor=false`，后页按`id COLLATE "C">lastID COLLATE "C"`；不能用空string作起点而漏掉原TEXT空ID。完整消费并关闭Rows、检查Rows.Err后，页数<128才记EOF；整128行的尾页必须继续到下一空页。每页游标严格前进，不以时间或UUID解析替换C序。`::text`只用于保留BIGINT精确字面值，nullable tenant保持SQL NULL，禁止coalesce成空string。

§12纯片直接消费这三类行的算术投影；journal其余列由同一reader另交来源/生命周期核验，不能为了适配纯片删去原字段。reader自己的完成记录不进入纯片参数。第一片纯算法仅支持上述全局完整行模型，**不得给它投喂单tenant的journal子集再比较共享clearing账户全额balance**。单tenant诊断将来须另冻精确scope算法：核本tenant账户全额及其journals的共享科目分录，不核共享平台账户整体余额；当前首片不提供跳过balance的bool参数。

### 11.2 来源、维护集合和C0读取清单

以下是新独立reader必须交付的原始字段范围；具体SQL及scan DTO须根冻结后另实施，不能用存在性SQL或写侧predicate替代：

| reader组 | 显式原始事实及独立核验义务 |
| --- | --- |
| tenant/Key | tenants `id,fl_created_xid`；api_keys `id,tenant_id,held,spent`及其生命周期必需原列；request `id,tenant_id,key_id,owner_id,billing_status`；reservation `id,tenant_id,request_id,amount,status,deadline,created_at`；settlement `id,tenant_id,request_id,charged_amount,refunded_amount,status,reason,journal_id,created_at`。独立累计unfinished R/原C，退款不减Key spent；不以当前权限配置倒推历史授权。原Key schema/策略承诺之外的删除或缺证必须按后续Key精确合同处理，不能此处猜值。 |
| manual | credit_orders `id,tenant_id,amount,state,requester,approver,journal_id`；funding_receipts `id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at`；相应journal/两行、approval audit和credited outbox。approved恰一receipt，pending无效果；申请/批准不同；完整双向匹配。 |
| voucher | voucher_redemptions `id,tenant_id,voucher_id,batch_id,operation_id,actor,amount,journal_id,funding_receipt_id,created_at`；vouchers仅 `id,batch_id,created_at`，不读code_digest；batch `id,state,face_amount,count,expires_at,maker,approver,activated_at,terminal_actor,terminal_at,redeemed_count,created_at`；必要revocation/decision的ID、batch/operation、actor/双审、revision/生效时刻；commerce_commands的operation/kind/scope/actor及不可变request摘要、commerce_operation_receipts的完整安全terminal字段；相应audit/outbox/receipt/journal。原兑换时点在激活后、失效/撤销前，当前batch后来revoked不抹掉合法旧兑换。批次全券/全兑换计数在Go逐行求出。 |
| sandbox本地图 | payment_orders的完整身份/tenant/SKU快照/金额/CNY/900秒时窗；payment_sku_versions和activation原快照；commerce order命令及accepted receipt；全部payment_transaction_identities/selections/captures/effects/review_facts的声明列；payment_verified_evidence/processing的原件引用、事实字段、fact摘要、processing代数及终态；payment_inbox/verified_queries的canonical原件、摘要、verified_key_id、received_at；相关query_attempts完整canonical request/代数、command_outbox原参数和原command身份、order state/revision事实；原环境/v1/v2持久证明。所有引用双向核，不仅从已credited capture出发，合理review和未入账身份也要核合法出处。 |
| audit/outbox | audit_events `id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at`；projection_outbox `event_id,tenant_id,operation_id,kind,schema_version,sanitized_payload,created_at`。按源定义的固定JSON键集合、类型和string金额比较，不忽略未知多余键。枚举全部钱包动作的反向事实，不能EXISTS一条正确行就放过额外资金效果；与source无关的其他合法业务audit/outbox按封闭action/kind分派，不把它们误算C0孤儿。 |
| epoch/legacy/C0 | 生产候选16表全部行；按[c0-production-schema.sql](c0-production-schema.sql)逐表声明顺序显式列出每列，保留全部nullable身份、NUMERIC delta、xid、time、event/allocation/predecessor/cache/head。逐张表独立EOF，再核§5–7关系；不能只扫描有合法parent的join或只读当前cache。 |
| 采购/PB交叉域 | 原procurement accounts/bindings/reservations/events、attempt/request实际来源和PB六表/版本/operation/audit原列，按各自独立域精确合同完整读取。原采购未知消费/held不可因客户释放或消费退款而恢复；当前采购与PB公开report不代替本session完成记录。未另冻的读取/API仍是实施阻断。 |

以上清单不授权读取邮箱、密码/secret、券码/HMAC材料、模型请求/响应正文或任意供应商凭据。若确有新的必需字段，先补本合同及静态ACL/descriptor；缺权直接失败，不改用owner连接，不给worker补全源SELECT。所有金额求和和跨表比较由Go big.Int处理；BIGINT按规范signed十进制读，C0无scale的NUMERIC必须独立识别有限整数值（允许数据库合法`1.000`表示整数1），拒绝非零小数/NaN/Infinity，不经float、四舍五入或强转BIGINT掩盖损坏。

来源反向检查至少覆盖：每credit/redemption/capture ↔ receipt ↔ journal/完整line；每钱包journal/每AVAILABLE或HELD line ↔ 唯一真实source ↔ legacy成员或新operation；每operation ↔ source/journal ↔ expected lot/event/边；每声明tenant事实 ↔ 同tenant父；每C0实际行 ↔ 被一次消费的预期键。全局时所有真实表从自身主键出发扫描，跨tenant/无父行不得在join中消失。除真正依赖坏前缀不可推导的部分外，发现缺失即mismatch+MissingFacts，实际无合法归属即mismatch+OrphanFacts；不是coverage模糊描述。

### 11.3 B历史认证和资金生命周期的证明范围

021的`payment_inbox/payment_verified_queries`实际只持久canonical_body、生成摘要、verified_key_id和received_at；未保存原传递raw body、delivery headers或HMAC。新独立reader**不能重新验历史传递签名**，不能用裸DTO复构paymentproto已验证opaque，也不改旧schema补造不存在的证据。可以独立严格解析已持久canonical、重算原摘要/规范事实、完整核原query/command绑定、identity/selection/capture/effect/processing/receipt/journal/audit/credited关系，以及已受审入口、不可变trigger/ACL、v1环境和key版本摘要的绑定。

这里的`SandboxFacts`完整覆盖精确定义为**原受控验签入口与不可变stored-verified-fact图下的本地业务关系证明**；不包含历史HMAC重验、正式PSP外部真实性或mock之外的收款事实。按此范围完成全部必需图，缺原本不保存的header本身不计Unproven；缺应有canonical原件、key版本/环境绑定、同代processed capture、immutable生命周期或业务边则分别是确定mismatch/前置schema错误，不得以“历史无法重签”全部归入indeterminate。owner任意伪造整图、外部真伪和离库checkpoint仍按既定信任/恢复合同处理。

post-epoch非genesis的资金生命周期另独立核`journal.created_txid::numeric=operation.created_xid::text::numeric`、真实源/tenant/journal身份相等、原journal current-tx完整line guard及原图immutable定义。历史journal不与当前核验top xid比较；同top xid内多个source保持独立operation。tenant_genesis比受保护tenant birth top-xid；legacy只由完整cutover精确成员/basis证明，不猜xmin/创建时间。不要求旧request/refund提案的出生事务等于较晚结算/批准事务。

## 12. 首个有界实施片 V0-L：原始账本行独立纯重放（根冻结）

### 12.1 授权范围和精确API

建议第一片只新增`internal/fundinglotverify/ledger.go`及对应纯测试，使用Go标准库；不import pgx/billing/periodicbudget/payments、不调用planner或SQL，不写reader/迁移/HTTP/CLI/main，不被任何生产代码import。完成只可称**输入账本行算术与结构检查**，不是全源Verifier、lot重放、来源资格、账务修复或运行授权。§10–11完整reader/组合/既有API重构都不属于该片。

```go
package fundinglotverify

type LedgerAccountRow struct {
    ID string
    TenantID *string // nil精确表示SQL NULL；指向""是另一个TEXT身份
    Kind, Balance string
}
type LedgerJournalRow struct { ID, TenantID, OperationID, Currency string }
type LedgerLineRow struct { ID, JournalID, AccountID, SignedDelta string }
type LedgerRows struct {
    Accounts []LedgerAccountRow
    Journals []LedgerJournalRow
    Lines []LedgerLineRow
}
type LedgerRowCounts struct { Accounts, Journals, Lines int64 }
type LedgerRowMismatches struct {
    AccountShapeMismatches, DuplicateAccountKinds int64
    JournalShapeMismatches, DuplicateOperationIDs int64
    MissingAccounts, MissingJournals, CrossTenantLines int64
    DuplicateJournalAccounts, AccountBalanceMismatches, UnbalancedJournals int64
}
type LedgerRowsCheck struct {
    Scope string // 固定"supplied_ledger_rows"，不是global/tenant证明
    RowsConsistent bool
    Scanned LedgerRowCounts
    Mismatches LedgerRowMismatches
}
var ErrLedgerRowsInput = errors.New("funding_lot_ledger_rows_input")
var ErrLedgerRowsResource = errors.New("funding_lot_ledger_rows_resource")
var ErrLedgerRowsCanceled = errors.New("funding_lot_ledger_rows_canceled")
func CheckLedgerRows(context.Context, LedgerRows) (LedgerRowsCheck, error)
```

该API直接接收§11.1全局reader的三表算术投影；未来reader在同session记录额外journal业务列及EOF，调用本函数后还必须做全部source/生命周期判断。这里**没有**Balanced、CoverageComplete、AsOf、phase或opaque proof，也不接受读全bool、trusted bool、expected余额、任意permission或跳过共享账户的参数。caller拿空slice会得到空输入一致，这是纯函数的范围，不代表数据库为空；只有将来的受审静态reader可证明它读取过全部实际行。

### 12.2 输入、资源和防修改

所有输入string先核有效UTF8、无NUL，包括Kind/Currency/金额。ID/tenant/operation保持原TEXT字节语义，允许原表可能存在的空string和非UUID、多字节、任意大小写；不trim、大小写折叠或UUID化。SQL NULL仅由account.TenantID=nil表达，不能等同空tenant。Kind/Currency按下节判断业务形状，不作为SQL标识符使用。

Balance与SignedDelta严格为canonical signed int64十进制string：`0`、无前导0的正数或负数，包含MIN/MAX，拒绝`+1/-0/01/1.0/1e0/空白/越界`。所有求和、比较和中间值用独立`math/big.Int`；不在Go int64内先加、不用SQL SUM、Money或写侧checkedAdd。即使全部中间和超int64、末尾回到合法余额也必须正确比较。没有NUMERIC输入；§11.2的NUMERIC parser属后续片。

nil context、坏UTF8/NUL、非法数字、同表重复主键(ID相同，即使内容相同)返回固定ErrLedgerRowsInput与整个零结果；不能任取一行覆盖另一行。重复自然键不算输入解析失败，必须按下节计mismatch。输入context已取消/到期或计算中取消返回ErrLedgerRowsCanceled与零结果；错误不包装原ID/值。边界总计`len(Accounts)+len(Journals)+len(Lines)≤100000`，所有输入string原字节总数（含非nilTenantID每出现一次）≤16MiB；超界返回Resource+零结果，在建map/BigInt前先以安全无溢出计数检查。它是逻辑输入上界，不冒称整个Go堆/RSS精确16MiB。

函数不改caller的slice/struct/tenant指针内容、不排序原slice、不返回其引用或保留到调用后；同步读取只保证caller不并发改输入的通常Go合同。context开始、每至多256行及返回前检查；结果只含值和计数，无ID/slice/pointer/原金额。相同输入顺序重排给完全相同结果；每次调用独立，无全局可变cache，多goroutine对稳定输入并发读须race通过。nil与空slice均为合法空集合。

首个直接调用该纯core的全局reader必须同样限制整份三表输入在100000行/16MiB内，超过即Resource；不能分页调用Check后把局部RowsConsistent做AND来伪证全账本，因为跨页account/journal求和与重复键会丢失。§8更大的完整Verifier候选上界不是本首片已支持的容量；流式累计/按tenant释放和共享账户全局聚合若要支持更大库，须另冻精确累加器与完成性合同后实现，不能静默扩大本API硬界。

### 12.3 独立推导、精确计数和扫尾

先检查资源、字面值和主键唯一，再建三表私有索引；按原行完整遍历，不从journal parent join过滤line。所有Scanned计数为各输入slice长度。固定判定如下，同一计数方向的单位只记一次，方向之间允许同一事实同时违反多项：

| 字段 | 单位及独立判定 |
| --- | --- |
| AccountShapeMismatches | 每account：AVAILABLE/HELD要求TenantID非nil且balance≥0；FUNDING_CLEARING/REVENUE要求nil、允许任意signed balance；OPERATING_GRANT_CLEARING/SANDBOX_FUNDING_CLEARING要求nil且balance≤0；未知kind或不符均记1。没有行的合法零账户仍参与余额比较。 |
| DuplicateAccountKinds | 每精确自然键`(NULL或原tenant字节,kind)`：出现>1个不同account ID记1；不把nil与指向空string合并。 |
| JournalShapeMismatches | 每journal：currency不等于精确CNY记1；本纯片不检查kind对应来源、actor授权、tenant存在或top-xid。 |
| DuplicateOperationIDs | 每原operation_id：>1个不同journal ID记1；不取created_at较新者。 |
| MissingAccounts / MissingJournals | 每实际line分别核AccountID/JournalID原parent是否存在；对应缺失各记1，不能因缺另一parent而漏计。 |
| CrossTenantLines | 每实际line：两parent都存在且account.TenantID非nil时，其tenant必须与journal.TenantID完全相等；不等记1。共享平台账户nil不作跨tenant错误。 |
| DuplicateJournalAccounts | 每`(JournalID,AccountID)`自然键：>1个不同line ID记1，包括0金额；存在主键不冲突也不能吞掉重复分录。 |
| AccountBalanceMismatches | 每实际account：独立sum所有AccountID相等的实际line.signed_delta，空集为0；与account.balance不等记1。包含引用缺失journal或跨tenant的line，其他方向仍独立报错，不过滤后假平。平台账户也完全比较。 |
| UnbalancedJournals | 每实际journal：所有JournalID相等的实际line少于2，**或**signed_delta大整数和非0，合并记1；0行、1行均错，两条0行可正确平衡。引用缺失account的实际line仍进入该journal算术，MissingAccounts另报；不是把line丢掉后取另一套总和。 |

不存在独立合法parent的line也完整计Scanned及上述缺失方向；没有line的journal和account也从自身集合核，完成双向枚举。所有十个mismatch字段为0才`RowsConsistent=true`，否则false；正确输入无未定义三态或部分值。数量硬界已保证计数可存int64，所有累加仍显式避免wrap。该结果不检验source真实性、FIFO/lot、Key/PB、外部收款、schema/ACL/空安装或数据库扫描完整性，不能直接传给RuntimeProof作为完整ledger/source域。

### 12.4 必须测试、冻结出口和并行后续

2026-11-01根已对照002/017/021的原账本定义冻结本节，授权§12.1列出的独立纯包/测试；不因此冻结§10–11的完整scanner或C0生产DDL。独立纯测试至少覆盖：

1. 手算人工/券/sandbox三clearing账户加AVAILABLE/HELD/REVENUE的完整行；reserve/finish/refund的平衡原journal；这些只测行算术，不将fixture解释为已证明业务来源。
2. 空输入、无line零account、无line journal、单零line错误、两零line journal正确；不同ID同journal/account重复line、同tenant/kind重复account、同operation重复journal均按上表精确计数。
3. line缺account、缺journal、同时缺两parent、跨tenant、NULL平台/空TEXT tenant区别；dangling/cross-tenant line仍进入各自可归属的算术，不被parent join隐藏；账户错误与journal错误的方向计数独立。
4. 原非UUID/空/Unicode/大小写敏感ID和重排顺序稳定；坏UTF8/NUL、重复PK、canonical数字负零/指数/小数/前导零和MIN/MAX越界严格拒绝且零结果，无driver/原行值错误。
5. 多条MAX/MIN使账户或journal的中间SUM超int64再回归合法值；数学非0超界和仍确定mismatch，不出现wrap通过；所有expected由手算独立big.Int/固定表给出，不调用被测函数或runtime planner造expected。
6. 固定100000行/16MiB含边界和+1资源拒绝（分别隔离两个上界）；开始取消/计算中取消与零结果；输入slice/指针内容不变、返回后改输入不影响旧结果、稳定输入并发读race；专项vet及必要短fuzz不引入网络/DB。

实施证据需写明命令/真实退出、标准库依赖、无生产import和上述未授权范围；不修改既有VerifyLedger/VerifyFunding/Rebuild，不运行main或触碰用户预览。只有根汇总审阅后才更新统一架构与manifest。本片通过后可另行冻结：同RR静态ledger reader（直接消费本API）/独立source图规则/完整lot序列重放三条并行线；任何一条未完成都不能发行全源RuntimeProof或开账ready plan。

V0-R后续状态：同RR静态ledger reader已按[独立冻结合同](c0-ledger-reader-contract.md)交付并通过[真实PG专项](../../docs/verification/funding-ledger-reader.md)，成为上述纯API的首个产品代码调用方；§12关于无生产import的实施证据保留原纯片范围。reader没有运行入口或完整来源证明，不改变独立source图和lot序列重放仍待实施的门禁。

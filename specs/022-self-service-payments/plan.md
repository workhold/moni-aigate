# 022 实施计划（提案）

2026-11-02，**A0/A1已实现并通过限定本地验收；B0-P纯协议与M/Q独立沙箱已编码并通过专项验证，真实进程短程通过，旧版本20代长程已完成，最新补丁/精确故障继续收口，B1核心SQL与B2资金接线合同已冻结，B2已追加021并实现Go第三来源与三角色核验，专项通过；B3运行证明/Processor/HTTP/UI合同已冻结、022证明表/23表权限已实施，领域/runtime/Processor及HTTP/UI/main已正式接线，真实PG专项通过，真实主进程和最终浏览器整链通过限定本地验收，真实Guard失权/在途SIGTERM补验已通过，C继续后续**。A依[A执行合同](a-contract.md)、[领域合同](a-domain-contract.md)完成共同入账、双审券、HTTP和控制台，证据见[验证记录](../../docs/verification/funding-vouchers.md)。M/Q范围见[沙箱验证](../../docs/verification/payment-sandbox.md)；应用支付资金、来源lot与原路退款继续独立审查，不能将A或B0局部验收视作022整体完成。

## 依赖与并行边界

```mermaid
flowchart LR
    A0[共同入账回执与投影兼容] --> A1[双审兑换券与唯一兑换]
    A0 --> P[B0-P纯协议与签名]
    P --> MQ[B0-M/Q独立沙箱与receiver]
    MQ --> B1[B1应用SQL与唯一capture]
    B1 --> B2[B2领域命令与worker]
    B2 --> B3[B3 HTTP/UI与进程验收]
    A1 --> C0[lot与全部资金路径分配]
    B3 --> C0
    C0 --> C1[原路退款与独立退款冻结]
    C0 --> C2[拒付债务与准入门禁]
    C1 --> C3[退款拒付交叉恢复验收]
    C2 --> C3
```

- A0由账务负责人独占：追加迁移、人工credit历史保护与事务接入、来源/journal精确证明、报表/rebuild兼容、受限角色、严格历史回填；确切冻结签名/返回值/tx与锁所有权/017 DDL已见[A§12](a-contract.md#12-a0-账务接口与追加-ddl)，已由集成人冻结，仍须实现前置通过再派A1。不与其他人同时改同一billing文件。
- A1可在A0的冻结接口上拆成后端命令/券、HTTP、前端交互；前端先用契约fixture，不将fixture视为资金验收。实施共用总合同§3.4–3.6的精确API/DTO、逐kind回执/空值矩阵及未知提交规则；审批/券一次展示/迟到响应与unknown一并验证，不留后补。
- B0-P依[精确合同](b-domain-contract.md)已冻结，可独立实现纯协议与签名解析；M/Q已按精确SQL/权限冻结实施，真实进程短程通过，旧版本20代长程已完成，最新补丁/精确故障另验。mock仅有自己的专用schema运行权限，对应用数据库对象无权；普通main不接线，B1订单资金写入口仍由账务集成人审查接入。
- C0是核心串行关口：先冻结每条现有Reserve/settle/release/refund/recovery路径及统一锁图，再一次集成lot守恒。不能只给新支付加lot而让旧调用绕过。
- C1与C2可在C0通过后分别实现状态处理；共享payment-order锁与账务helper接口由同一集成人维护。最终C3合并交叉故障、真实PG角色与恢复测试。
- 021自助身份独立；022只依赖当前已登录owner，不能借邮箱验证赠额或创建支付权限。商品公开展示可与020目录独立链接，但不将模型价目表当充值SKU。

## 逐片任务

### A：共同来源与兑换券

- [x] 冻结每个API、权限/开关、一次展示、错误码、命令回执和DB状态保护；审查赠额对手科目与报表口径。
- [x] 集成人复核精确DTO：六kind的accepted reason/resource/金额，rejected四关联null、BatchView四态空值、policy PUT结果、RedemptionView、列表envelope与null cursor；不得由HTTP/UI自行发明兼容形状。
- [x] 冻结作废提案列表/详情及不可变decision；approve body仅operation，目标path ID参与命令指纹，不能用新证据改旧proposal。
- [x] 冻结平台commerce-capabilities的active admin/finance读权；policy GET/PUT保留admin，所有列表每页重验当前角色/租户。
- [x] 根集成人逐项确认A执行合同的env+DB开关、HTTP200终态业务拒绝、voucher ID作为source、同步来源分类、独立HMAC keyring及有界准入等选择；记录只冻结A，不冻结B/C。
- [x] 以真实路径锁图冻结helper事务/账户/时刻/emit所有权；公开确切接口，兑换最后DB时钟放到账户锁之后；不将reservation放在tenant前。
- [x] 冻结A§12接口与017 DDL：funding_receipts唯一表名，新ID带canonical约束的TEXT；manual scope.CommandID空、voucher指向claimed命令；decision列严格status/reason/decided_at，VerifyFunding租户范围只比其journal清算分录。
- [x] 追加source receipt与commerce命令迁移、credit身份与终态保护、journal同租户复合归属/金额科目约束；先验证旧credit/journal/credited异常中止，再回填，不新发journal/outbox。
- [x] 保留reports-v1；同步funding-summary/receipt分类；Rebuild和VerifyFunding核对source↔receipt↔journal、全部入账与原账本，不授projector新source读权。
- [x] 实现claim/savepoint/terminal提交矩阵；rejected无资金而有持久回执，revocation stale仅额外追加不可变rejected decision，基础设施失败全回滚；同ID按原指纹key版本重读，指纹包含路径目标且未知COMMIT不重试。
- [x] 追加voucher批次/摘要/审批/作废/redemption；使用独立HMAC用途域与密钥版本，不复用可恢复凭据存储。
- [x] 以batch为根保护状态revision/独立redeemed_count和不可变revocation decision；原子验证policy/当前授权，覆盖disable、到期、并发兑券与作废。
- [x] 一个事务完成核销、钱包、journal、receipt、审计、projection和operation回执；保留人工双审、硬预占、未知不重放。
- [x] owner兑换与finance批次/作废提案UI；精确金额、不可泄漏的命令journal、身份epoch、禁用自动重试、一次展示清理；关闭/unknown后的晚回codes丢弃，GET404不解锁新operation。
- [x] 钱包明确显示累计钱包入账、人工核款、运营赠额及同步as_of/异步报表延迟；不显示未实现在线收款/现金退款能力，不以来源差值补账。
- [x] 专项真实PG race/权限/回滚、前端状态测试、strict构建、隔离浏览器；记录A范围，不声称在线支付已实现。

### B：仅本地支付沙箱

- [x] 冻结B0-P/M/Q环境标记、QA隔离证明、loopback受控出口、模拟器控制面和普通main不可用边界；M/Q已编码及专项真实PG，完整进程/长程验收仍按记录逐项关闭。
- [x] 固定SKU/订单/merchant命令ID与冻结金额，支付状态和checkout分离；无客户端可控paid/amount/payee。
- [x] 严格签名解析、业务digest/inbox/conflicts、durable ACK、effects全局去重；事务外命令outbox和权威query。
- [x] 以内部verified source ID入账，沿A receipt/报表；测试late、异序、签名重放和已停用租户资金收尾。
- [x] 浏览器只展示本地sandbox订单和安全回执；私有模拟付款操作由QA CLI驱动，不加进客户页面。
- [x] 实际main隔离运行：两轮普通main/mock与最终浏览器整链，以及第三轮真实在途SIGTERM/Guard失权、原租约自然恢复已完成限定本地验收，见[分层记录](../../docs/verification/payment-application.md)。
- [ ] 其余main故障关口：实际数据库COMMIT回包丢失、旧备份后外部事实恢复等仍独立待验；库级真实wire proxy已有COMMIT丢包测试，不冒称main同层已验。未调用真实支付商，不写生产收款验收通过。

### C：lot先于原路退款/拒付

- [x] C0静态对象的离线metadata导出窄片：固定输入、ordinary/sandbox before/after、双轮同bytes及失败DDL精确清理；旧工具导出物保持未审候选身份，见[证据](../../docs/verification/funding-lot-descriptor.md)。
- [x] 完整C0静态叶子`internal/fundinglotcatalog`及`VerifySnapshotTx`/`VerifyInstallationTx`/`VerifyRuntimeTx`已实现，Catalog/Installation证据绑定原caller-owned RR、wrapper/连接/会话/顶层xid与深拷贝pins；正式expected经独立实装/来源复核后按ordinary/sandbox两套SHA固定嵌入，运行时不从live库或调用方文件生成期望。
- [x] ordinary采用真实owner/APP/projector三角色，覆盖`named-isolated`、`named-minimal-login`、`named-image-login`、`public-minimal-login`、`public-image-login`五个完整变体；sandbox覆盖application/Mock/Receiver三schema、八角色与十组跨域负权。原prosrc、结构/ACL/依赖、封闭字段/资源界、runtime全局无owner及数据库无CREATE/授权提升边界已编码；公开API非race及分项权限race通过，见[collector](../../docs/verification/funding-lot-catalog-collector.md)和[peer](../../docs/verification/funding-lot-catalog-peers.md)记录。
- [x] 完成上述正式checker的最终leaf串行真实PG race，217.140秒通过，保留原五秒总预算及全部权限、正文、sameRR漂移、事务失效/空集检查；47个顶层/179个终态中176 PASS，3个独立capture/timing/export为显式opt-in跳过且另有专项证据。精确清理确认本轮schema/role/DB/backend归零，public及DB ACL保持原样，见[最终leaf验证](../../docs/verification/funding-lot-catalog.md)和[机器记录](../../docs/verification/funding-lot-catalog.json)。
- [x] 静态checker阶段集成人已完成整仓`make check`/`make verify-mvp`及统一架构复核，具体340.981秒记录见本页末；新增T0 Gate由独立专项及下项集成记录验收，不追溯扩大旧记录范围。
- [x] T0 Gate库级实现：专用物理连接、实际endpoint/LOGIN与目标OID预检、402→401固定双锁、30秒总入口/5秒独立cleanup及unknown立即物理关；不提供Snapshot/Permit或main接线，见[根冻结§10](c0-permit-contract.md#10-根冻结t0专用连接双session-gate与清理)与[专项验证](../../docs/verification/maintenance-gate.md)。
- [x] 根汇合T0真实PG/race54.205秒及两次独立读审、README/运行入口、19图与架构后，最终make check 6.767秒、make verify-mvp 339.394秒退出0。发现的B0旧测试oracle按§10.3只修测试并新增多连接竞争回归；原失败与最终源码结果分别保留，见[交付验证](../../docs/verification/github-initial-delivery.md)。旧C0整仓记录未冒充新增包验证；验证文档收口后再refresh及只读检查。
- [x] 根冻结[T1生命周期](c0-snapshot-lifecycle-contract.md#10-根冻结范围与尚未开放的能力)：单次私有顶层RR、Init首次SELECT前ONLY125表锁、固定catalog stage、全程busy/总deadline、终结与未知状态；不提供裸Tx/ctx或Permit stub。
- [x] T1实现及真实PG专项/独立读审汇合：115.412秒、46顶层/124终态全PASS，见[证据](../../docs/verification/maintenance-snapshot.md)；T0历史证据保留原范围。
- [x] T1最终整仓与CI环境/测试断言修订已汇合；make check及指定专库make verify-mvp通过，286项前端/全默认Go race/vet/build，见[集成记录](../../docs/verification/maintenance-snapshot-integration.md)。
- [x] 动态顶层分区及显式PG时区的新远端CI实际验收：[第三轮十组](../../docs/verification/github-ci-third-run.md)在精确f64b56f源码全部success；第二轮失败保留，不覆盖后续T2代码。
- [x] T2按[真实授权根冻结](c0-authority-contract.md)实现真实维护身份、catalog/history/完整v1认证后账户锁、固定manifest与一次Permit，最终专库race已通过。
- [ ] 冻结并实现完整T3–T4：全来源scanner与组合结果、basis、epoch与封闭写入器，不能由T2维护Permit或局部reader结果替代。
- [ ] 完成正式部署profile验收：当前两套root只实证PG16.15/Homebrew initdb catalog-profile、UTF8与C/C环境，PL/pgSQL language OID 14038为该初始化结果。其他PG16集群、locale/发行版或1Panel环境须独立审profile，未知profile失败关闭，见[正式root合同](c0-schema-check-contract.md#104-首次正式root冻结与已验环境)。
- [x] C0-V0-L纯账本行检查及V0-R同RR私有ledger reader：三表完整EOF、窄shape/history/函数解析前置、资源界/失败零结果；专项与整仓回归通过，见[读取证据](../../docs/verification/funding-ledger-reader.md)。无完整source/lot证明或运行入口。
- [x] C1-P独立退款v2纯协议：只读桥接v1已验capture，封闭DTO/四摘要域/三用途HMAC/不可变opaque，独立Python向量及race/vet/fuzz通过；无DB、P、HTTP或资金接线，见[证据](../../docs/verification/payment-refund-protocol-v2.md)。
- [x] T3局部reader切片：冻结[人工核款/兑换券来源关联合同](c0-source-link-contract.md)，完整读取`credit_orders`、`voucher_redemptions`、`funding_receipts`并只双向核对manual/voucher receipt链接；固定纯core、billing reader与真实ReadLease维护入口已接入。sandbox capture及全源coverage明确未覆盖；合同和限定真实PG证据见[验证记录](../../docs/verification/funding-source-links.md)。这不完成T3全来源组合。
- [x] T3第四个局部reader：冻结[sandbox capture直接关联合同](c0-sandbox-capture-link-contract.md)，以独立`ScanSandboxSources`完整分页读取`funding_receipts`和`payment_captures`，只核直接ID/tenant/amount/currency关系，并逐capture报告未验证的evidence图与账务效果；没有普通main/HTTP/worker/writer接线，不降低full-source coverage债务。定向Go测试、race和vet已通过。
- [ ] sandbox capture reader真实PG Plan/Init验收：需专用`C0_MAINTENANCE_DATABASE_URL`，覆盖真实capture正例、受控孤儿/不匹配和扫描前后资金快照不变；当前未配置，不能以本地fixture替代。`TEST_DATABASE_URL`亦未配置，故本切片之后尚未运行`make verify-mvp`；状态见[验证记录](../../docs/verification/sandbox-capture-links.md)。
- [x] T3第五个局部reader：冻结[采购预算投影合同](c0-procurement-budget-contract.md)，新增ScanProcurement完整分页读取采购账户、reservation与event三表，以Go大整数独立比较held/spent投影并检查局部状态/引用形状；没有main/HTTP/writer/basis/epoch接线，结果不证明真实供应商账单或全来源覆盖。定向Go测试、race、vet已通过。
- [x] T3第五个reader扩展：冻结[采购attempt关系合同](c0-procurement-attempt-contract.md)，在原ScanProcurement的十二表固定读取中增加binding、attempt/request/tenant、channel version credential pool、cost quote snapshot及reservation bounds核验；预算projection与attempt graph是两个scope独立摘要，共用同一100000行/16MiB读取预算及一个ReadLease，完整数据mismatch正常Finish，执行错误撤销lease。没有新ScanKind、生产接线、全来源Coverage或真实供应商成本证明；纯core、billing reader、维护结果和外部PG Plan/Init QA代码已实现，Go专项race/vet通过。
- [x] T3第五个reader再扩展：按[采购成本双审关系合同](c0-procurement-approval-contract.md)，同lease增加users ID、cost corrections和proposals三表，budget event增加type/evidence_ref；返回第三个`procurement_cost_approval_graph_v1`摘要，核approved proposal与managed correction/cost_corrected event双向身份、精确金额/basis/evidence和双人nullable形状。无新迁移、在线资金或全来源coverage；纯core、固定reader、维护proof和外部PG QA代码已实现，验证以独立记录为准。
- [ ] 采购reader当前十五表真实PG Plan/Init验收：复用专用`C0_MAINTENANCE_DATABASE_URL`，覆盖真实channel/credential/version/pool/cost/request/attempt/reservation正例、绕过触发器后的attempt bounds/upper mismatch，增加真实双审更正及受控proposal evidence错配，以及扫描前后采购/尝试/提案/更正事实不变；本轮环境未配置，Plan/Init QA代码会明确SKIP。`TEST_DATABASE_URL`也未配置，本轮尚未运行`make verify-mvp`，见[验证记录](../../docs/verification/procurement-budget-maintenance.md)。
- [ ] 审计真实代码锁图与全资金路径，逐操作列出账户/lot桶变动、migration/recovery/历史退款例外。
- [ ] 维护态legacy迁移；新来源lot和Reserve/FIFO分配、settle/release、消费refund回原来源；VerifyLedger和rebuild对齐。
- [ ] 后续按冻结合同实现epoch、独立全源RuntimeProof及普通main/全资金路径接线；当前静态leaf没有授予这些能力，022-C与整个后续交付计划仍未完成。
- [ ] 证明现金100消费后用券100补余额，原支付仍不能退款；证明并发Reserve/refund不能复用同一lot额度。
- [ ] 增加独立PAYMENT_REFUND_HELD、双审原路退款、原外部命令/query/unknown留置；业务关口检查C0已完成。
- [ ] 增加dispute/chargeback唯一effect、PAYMENT_DEBT、tenant准入屏障、case逆转和显式偿债来源合同；未冻结自动偿债前保留人工核查，不做静默抵扣。
- [ ] 联合验证unknown退款/拒付、旧sending、客户24h释放、消费退款、回调/query竞争、恢复点后外部事实；任何不确定财务效果持续review，不造调平流水。

## 集成与架构同步

每片实施前由集成人确认该片已冻结。实施后汇总所有并行结果、语义审查统一架构的上下文/资金链路/数据关系/权限/能力状态/源码链接，同步HLD/LLD、路线图和验证范围；先正文后显式refresh，不能仅刷新摘要掩盖架构缺失。

```sh
go run ./scripts/check-architecture --refresh --reviewed --reason '022本次切片的已实现合同、资金来源与权限影响及未实现边界'
go run ./scripts/check-architecture
```

该命令由集成人在所有并行改动汇总之后执行。各片状态以上方任务和对应验证记录为准；本轮完整C0静态leaf已实现并通过最终串行真实PG race，原五秒发行预算保留；整仓`make check`/`make verify-mvp`已由集成人实际执行通过，其余维护/全源/资金C片保持后续。最终专项测试、全量`make check`/`make verify-mvp`与隔离数据库记录按切片范围交付，统一架构只标记实际已实现和已验范围。

2026-11-01根冻结022-B0-M/Q：实施依据为[b-runtime-contract](b-runtime-contract.md)、[SQL合同](b-mock-sql-contract.md)及四份自有版本1 SQL。并行分工为公共运行/attestation/schema、mock仓储与回调、receiver/HTTP/独立CLI及harness；各自独占文件，真实进程验收串行。应用迁移/普通main/资金B1不在授权范围，专项真实PG及真实进程短程已通过；20代长程、精确崩溃/COMMIT丢包及旧备份恢复仍各自待验，见运行记录。

2026-11-02最终集成：`make check`7.546秒、`make verify-mvp`340.981秒退出0，286项web/strict、明确专库全Go race/vet/build通过；C0 opt-in完整真实PG leaf独立217.140秒通过，不在整仓命令重复运行。测试入口架构快照1114文件/529d53dc048b，之后只更新验证/状态文档和Runtime前置角色检查的图示顺序，再由根语义复核refresh及只读检查。完整范围见[证据](../../docs/verification/funding-lot-catalog.md)，维护/epoch/全源和全资金C片未由此完成。

## 2026-11-02 T2 实施切片

- [x] 根冻结[真实授权](c0-authority-contract.md)，替换旧三参Attest/maintenanceStage/裸VerifyTx候选，保留T1历史边界。
- [x] 实现同Snapshot一次Permit、完整history/manifest、真实身份、新鲜统计、完整前驱、Init全账户锁与撤销接线。
- [x] 汇合T2真实PG/race、ordinary各profile的成功或拒绝边界、genuine sandbox、私有判定层与golden、独立审阅；make check与指定专库make verify-mvp已通过，见[集成记录](../../docs/verification/maintenance-authority-integration.md)。远端交付以Git精确commit/ref核对为准。
- [x] T3首片：真实ReadLease、ledger/PB维护reader与局部结果已实现，真实PG及旧reader回归通过；精确范围见[历史专项](../../docs/verification/maintenance-read-lease.md)。后续新增manual/voucher receipt link reader单独以[C0来源关联合同](c0-source-link-contract.md)和[验证记录](../../docs/verification/funding-source-links.md)记证，不改写首片历史统计。
- [ ] T3独立全来源与完整组合、basis、epoch封闭写入、全资金路径、C1/C2/C3及后续阶段继续按依赖推进。

2026-11-02 T3首片先冻结[读取租约与两个维护reader](c0-read-lease-contract.md)：根集成Snapshot/Permit撤销和真实PG验证，独立实现ReadLease封闭出口及纯测试、ledger单core与维护结果、PB单core/维护资源profile与诚实三态。历史专项记录不包含后续source-link reader。本轮第三个reader依[c0-source-link-contract.md](c0-source-link-contract.md)完整读三表并局部核对manual/voucher receipt双向关系；其scope内sandbox receipts计为未覆盖。第四个ScanSandboxSources reader和第五个ScanProcurement预算投影reader分别按独立合同接线；采购reader先按[c0-procurement-attempt-contract.md](c0-procurement-attempt-contract.md)扩展十二表/双摘要，再按[c0-procurement-approval-contract.md](c0-procurement-approval-contract.md)扩展为同lease十五表/三个局部摘要，sandbox和当前采购实现的真实PG验收均因专用环境未配置而待执行，范围见各自验证记录。全部source coverage、basis、epoch与生产CLI不因局部读取接线而视为完成。

2026-11-02 T3集成依赖隔离按读取合同§7实施：独立maintenancescan单向复用领域固定raw-facts算法与真实ReadLease，在线生产/race测试依赖不再引入维护/catalog；原算法、身份、资源、资金和超时规则保持。原失败进程49.177秒、core/防伪造/race依赖门禁15.541秒、真实双reader及九个资源边界194.261秒均通过；完整整仓与新精确提交CI以[隔离修复记录](../../docs/verification/maintenance-integration-isolation.md)为准，f572bbd旧CI失败保留。

## 68表完整业务来源读取切片

- [x] 冻结[完整读取合同](c0-complete-source-reader-contract.md)，包含68表物理列并集、native资格、共享预算与ordinary精确排除。
- [x] 实现fundinglotscan与七算法，以及ScanCompleteSources可信模式/私有结果/唯一Finish；旧五种局部proof不相加。
- [x] 汇合fake边界、真实PG SQL与真实Plan/Init，完成根复核及make check/指定专库make verify-mvp；专项与整批原始失败、QA修正和最终证据见[本轮记录](../../docs/verification/complete-source-reader.md)，随README/HLD/LLD/统一架构交付，推送身份以远端SHA为准。

下一依赖为lot全序及protected pre/post组合，随后basis/epoch与全部APP资金路径；C1/C2/C3、商品、扩展、生产/HA及性能继续保留，本片不是全目标完成。

## C0 最小旧数据投影与纯开账规划切片

按[冻结合同](c0-init-planner-contract.md)新增九组普通LegacySeedRows、原68完整一致Facts的深复制出口，以及纯fundinglotinit计划。并行实现出口、planner/core和独立external静态golden后由根汇合复核。已完成源专项真实专库race；最终整仓和交付状态以[验证记录](../../docs/verification/init-planner.md)为准。

本片不写数据库、不生成DB stamp/epoch/attestation，不消费第八kind。下一步冻结完整私有phase状态机/预算/提交未知：自己读完整pre与tenant birth、basis，自持seed/plan，唯一writer仅收同phase state，strict v2与真实84post通过后才能提交。之后另冻非空epoch observer/runtime和全资金接线；C1/C2/C3仍依赖该闭环。


## C0 受保护初始化事务切片

按[冻结合同](c0-protected-init-phase-contract.md)，并行实现phase生命周期/rows/唯一提交、固定writer和精确投影预算、strict v2真实issuer及写后证明复核。根负责真实ordinary与genuine sandbox、原业务API混合资金/大NUMERIC、取消/busy与真实持久化后丢回复等验收，以及README/HLD/LLD/统一架构、最终检查和GitHub推送。具体执行身份见[验证记录](../../docs/verification/protected-init-phase.md)。

没有新增正式迁移/ACL/CLI/HTTP/在线writer。原空安装发行门禁不删除；Unknown不重试，原epoch独立观察与非空runtime另片设计，再审正式材料/全资金接线，继续C1/C2/C3及后续路线。候选phase闭环不表示整个商业目标完成。


## C0-O1 独立停服观察

- [x] 根先冻结[独立epoch观察合同](c0-epoch-observer-contract.md)，含唯一Begin、固定总预算、strict原件与当前图、历史basis、三种成功结论及失败零值。
- [x] 实现私有snapshotObserve与RO终结/cleanup、快照facts读取复用、84表无Query原件深复制及严格持久绑定；不扩旧空安装权威。
- [x] 集成真实提交丢回复、回滚、经营后资金图、genuine sandbox、故障与资源边界，完成根独立审阅及架构同步/验收/交付；精确状态见[验证](../../docs/verification/epoch-observer.md)。
- [ ] R1最小APP全源列权限及historical/current roots兼容、非空epoch runtime；R2正式迁移/CLI/所有资金路径；C1/C2/C3与后续目标继续保留。


### C0-R1-A：APP完整读取权限前置

- [x] 冻结[版本化读取profile](c0-runtime-read-profile-contract.md)、四列最小增量及84表776列清单（history另2列）。
- [x] 实现显式WalletReadV2 catalog API、固定派生根、完整矩阵及所选profile类别界；原入口和旧权限检查不放宽。
- [x] 完成两轮六变体actual APP/worker/projector及全部读取面的专库验收、旧profile回归与架构同步；实际范围以[验证记录](../../docs/verification/runtime-read-profile.md)为准。
- [ ] R1-B历史cutover/current roots兼容、APP v1/文件认证与非空RuntimeProof；R2正式迁移、维护CLI、进程门禁及全部在线资金路径仍后续。

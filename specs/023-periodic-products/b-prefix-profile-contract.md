# B-P3：完整来源、basis、catalog 与受保护组合

2026-11-03；基线 `ff812a3f983c2285a7d7fff973004f6f66c449fb`。本合同先于实现冻结，承接 [B-P1](b-prefix-contract.md) 第三项依赖和 [B-P2](b-prefix-install-contract.md)。B-P3 的完成条件包含受保护 pre/post、observer 与 APP runtime；叶子模块通过只能记为分项进展。正式 migration/provision/startup 仍是下一项独立依赖。

## 1. 固定数据面与预算

| 固定读取 | 表/列 | PK字段 | 单调用全物理预算 |
| --- | --- | --- | --- |
| prefix source | 71/635 | 沿固定registry | 100000行、16MiB |
| prefix source+wallet | 87/804 | 沿固定registry | 100000行、16MiB |
| prefix basis | 112/1004 | 131 | 100000行、64MiB |

新增三表是 `periodic_budget_observation_origins`（4列）、`periodic_budget_observation_heads`（4列）、`periodic_budget_observation_journal`（17列）；既有 operations 增 `observation_id`，windows 增 `observation_id/observation_created_xid`。journal 的两个 generated source discriminator 必须传输、计量并逐值核验，不能仅信任DDL。表/列以冻结候选SQL和实际Go固定registry双向核对；不以生产数据库导出动态扩读集。

source/wallet 的 history(name/checksum) 是外层单独核验，不计第72/88业务表；含history的APP内容读集为637/806列。basis 本已包含history三列，排除16张C0输出表和 `tenants.fl_created_xid` 的指纹内容；后者继续作为准确nullable metadata扩展核验。全catalog应有128张普通表，另含受审复合类型关系，须由静态root和实际安装双向验证。

一次扫描在同一私有session内读取旧列、新列和三新表，公共物理行/列只计一次。不可把68/84表报告与另一份证据扫描拼接。扫描与纯checker的100000行/16MiB逻辑预算各自执行；后者重复来源字段保守计量，不因物理扫描合格而省略。basis metadata仍共2048行/1MiB；新relation/PK单查询sentinel上界113，attribute1006；旧profile保持110/978，不静默扩宽。

## 2. 无权威scanner与投影

新增固定入口 `fundinglotscan.ReadPrefix(ctx,Query,schema,Mode) (PrefixFacts,error)` 和 `ReadWalletPrefix(...) (PrefixWalletFacts,error)`。两种Facts使用不同私有命名state类型，禁止type alias或可由外部显式转换的共用state。普通DTO、Query、Mode和报告均不授予数据库/资金权限。

新source报告scope为 `complete_business_source_prefix_graphs_v1`，原七算法后追加prefix（共8）；新wallet报告scope为 `complete_source_wallet_prefix_graphs_v1`，原八算法后追加prefix（共9）。报告保留全部原子结果和 `PeriodicPrefix periodicbudgetprefix.Report`，增加 `ScannedColumns`、`ObservationSourceMismatches`。旧Read/Facts/Report仍68表/七算法，旧ReadWallet/WalletFacts/WalletReport仍84表/八算法，原scope、语义、golden和固定材料不重签。

同次raw自行重跑原七/八core与prefix；必须 `PeriodicPrefix.Base == Periodic`。journal17字段内部transport后向纯checker投影15字段。command 的 command_source_id 必须等于 source_id、window_source_id 为NULL；window_birth反之；错误kind、NULL形状或错值计确定 `ObservationSourceMismatches`，不伪装transport资格错误。

新aggregate独立计算，不能继承旧aggregate的完整性：所有算法实际执行（ordinary sandbox仅合法N/A），无资格缺口，非PB原mismatch/gap条件保持；PB原mismatch全零，prefix scope/Base一致且新mismatch/Unproven全零、Balanced/CoverageComplete为真。旧Base的 `OperationSnapshotPrefixUnproven` 原样保留，只在新scope由逐命令认证解决。`LegacyMembershipUnproven/LegacyEffectBoundaryUnproven` 继续阻断；缺origin旧control仍indeterminate。确定mismatch优先，其次indeterminate，最后consistent；底层执行/资源/context错误整个零结果。

新增无查询深拷贝投影 `ProjectLegacySeedPrefix(ctx,PrefixFacts)`、`ProjectEpochProofPrefix(ctx,PrefixWalletFacts)`、`ProjectApplicationPaymentProofPrefix(ctx,PrefixWalletFacts)`，返回原普通DTO。每个入口独立核固定registry、EOF、行/列/预算、原core计数、新prefix资格，再从同raw复制；不得把新Facts强转旧Facts、改旧report消除gap或接caller已完成报告。

旧scanner源材料30项SHA原字节保持。旧目录全集测试的唯一扩展是与本片独立新source manifest核完整全集/不重叠；旧30项长度和每项hash仍严格断言，不能按prefix文件名排除覆盖。新增生产文件由新manifest固定并校验；测试文件可正常增加。

## 3. basis：固定材料驱动的唯一读取核心

新增 `fundinglotbasis.ReadPrefix(ctx,Query,schema) (Facts,error)`，scope `fixed_112_table_prefix_basis_v1`。新静态descriptor独立审定112/1004/131；原manifest2459字节/SHA和send12360字节/SHA不变，BasisVersion1和v1域前缀/framing不变。指纹header必须帧入实际选中descriptor原字节；不能新表扫描却仍帧旧descriptor。

允许最小共享重构 `materials.go/read.go/metadata.go`：固定旧/新material分别带准确counts、scope、canonical bytes与SHA；旧Read仅选择旧material，新ReadPrefix仅选择新material。SQL、分页、record检查、hash、metadata验证使用同一实现，profile不能由caller注入。旧109/976/128、旧descriptor、旧header golden及预算必须独立回归；不复制整套metadata/分页信任实现。历史验证记录中的旧source SHA不回写，新验证另记新SHA及语义回归。

## 4. catalog：专用V3发行，唯一身份生命周期

新增 `PeriodicPrefixV3 Profile=3` 及三个专用入口：`VerifyPeriodicPrefixSnapshotTx(ctx,tx,pins)`、`VerifyPeriodicPrefixRuntimeTx(ctx,tx,pins,role)`、`VerifyPeriodicPrefixInstallationTx(ctx,tx,pins)`。旧generic Profile API和旧默认只接受原LegacyV1/WalletReadV2，不因enum=3而自动接受。新API没有caller expected/loader/root参数。

`proof.go` 仅将固定loader之后的 identify→actual LOGIN→collect→identify→opaque proof 主体抽成唯一私有 `verifyCatalogExpectedTx`，旧入口加载旧expected后委托，新入口加载固定V3后委托。不复制生命周期，不把B-P2局部ACL核验当全catalog。新Installation使用相同总5秒ctx并复用原16张C0输出空集检查，最后重核绑定；三张PB新表允许有真实经营历史。

新静态roots从原已认证材料+受审准确schema/ACL/dependency delta独立推导，覆盖relations/types/attributes/default/generated/FK/index/trigger/函数原正文、raw/effective ACL、依赖闭包及角色。不得由live结果回填expected。ordinary五变体和完整sandbox至少两轮独立名称/OID实装；旧root拒新布局、新root拒旧布局。APP逐列内容读面87/804+history，projection/worker/peer不得被宽授。原expected、compatibility与历史roots原字节保留；新source SHA独立记账。

V3复合FK与索引首次引用三项PostgreSQL内建对象：`pg_opclass/10053` 是 `pg_catalog.xid8_ops USING btree`；`pg_operator/5068` 是 `pg_catalog.=(pg_catalog.xid8,pg_catalog.xid8)`；`pg_operator/1320` 是 `pg_catalog.=(timestamp with time zone,timestamp with time zone)`。它们仅由认证后的V3 descriptor私有字段固定选择；旧LegacyV1/WalletReadV2继续使用原builtin表，未知或新增引用仍拒绝。collector只增加已选expected到唯一builtin复核的接缝，不增加caller输入或全局白名单。静态依据为PostgreSQL `REL_16_15` commit `7d3e000c5961a544302072058a1184e9a588837b` 的 `src/include/catalog/pg_opclass.dat`（SHA-256 `654fe80bf10c6e9fb6309188a6c94ff01e7b0fcdd854d60febcd5cfd20fc3fd8`）和 `pg_operator.dat`（`db122ca15c4dfc37da246760bced25b4c70df5e667b8892b98516b461c4f7e72`）；opclass按固定PG16 genbki从10000递增的未显式OID顺序推导，和原材料已有bool_ops=10003、uuid_ops=10065双向核对。实际数据库只能验证这些固定对象，不能生成或拓宽接受表。

## 5. owner、once与阶段不可替换

新增 `Gate.BeginPeriodicPrefixPlan/BeginPeriodicPrefixInit`，在创建原快照前固定V3私有选择；Init须在首个产生数据快照的SELECT前，用同一完整LOCK语句锁原125表加新3表的固定C序128表，模式仍SHARE ROW EXCLUSIVE。不得到VerifyInstallation时才扩锁，也不从live catalog或caller清单发现目标。旧BeginPlan/Init固定LegacyV1/原125表。各入口共用原Gate sole-begin、busy、失败终结、总deadline管线。

新增 `Snapshot.VerifyPeriodicPrefixInstallation` 仅接受已由V3 Begin创建的原Snapshot；原VerifyInstallation仅接受LegacyV1。两者共用原catalogUsed一次位与busy/nonce/终结生命周期，交叉选择失败仍消耗原机会。新Permit仍须原owned安装、exact original22同事务history、实际owner、停服、前驱、完整账户锁事实共同发行，不能从新catalog叶子直接发行。选定profile后所有后续读取/复核固定使用它，caller不能替换。

原ScanKind1..7和fundingLotsOnce=128保持；新显式source/source-wallet wrapper共享原kind6/7的once，且要求owned V3；原wrapper不接受V3。失败消耗同一once，不能换profile重试。维护主阶段继续共享第8位，不向uint8追加第9位。

protected Plan/Init按私有profile封闭选择：pre71→深拷贝seed→basis112→draft→固定writer→post87→epoch原件→basis112复读→fresh完整catalog/原authority→ready/唯一commit。source+generated的总预算和post守恒计入全部新来源；固定C0 writer不生成PB witness须由触发器审阅及实际验收证明。原owner、raw tx、nonce、busy、绝对阶段预算、failure终结和unknown提交只读观察保持。

observer提供显式V3入口并私有绑定，同步owner独占raw tx；fresh全catalog、exact history、87完整图、原件/descriptor和停服前后复核，只有原RO rollback可成功终结。APP composer提供显式ordinary/sandbox V3入口，旧入口固定WalletReadV2；新RuntimeProof绑定实际APP RR RO、原wrapper/Conn/PID/xid、87完整报告、前后catalog、严格历史映射及原tx。总30秒和catalog5秒子界不按阶段重置。

历史manifest/basis/tuple→current V3逐mode/variant审定固定映射；原六条兼容材料、V2 codec/golden不重签。候选仍history22项；不能把候选SQL伪记正式migration，旧basis不与经营后当前basis强行相等。正式迁移发布时再另审current manifest映射。

## 6. 分工与完成门禁

先并行scanner、basis、catalog静态材料；根负责维护状态机/runtime/兼容映射与最终集成。共享文件只归单一owner。各路可先纯测试；实际PG由根协调串行，使用显式专库opt-in，不能用SKIP代替PASS。

必须包含旧接口/roots/golden回归、635/804/1004列双向核对、generated错值、共享资源/分页/EOF/close/NULL/time/xid错误、旧Base缺口保持而新prefix完整、legacy缺证关闭、新静态root独立重算/双轮PG/权限/正文负例、profile/once替换拒绝、protected全阶段计数/预算/新鲜证据和终结撤销、actual APP RuntimeProof原事务绑定，以及非空真实充值/三scope首配/no-op/跨窗/客户reserve-settle-release/采购派发-成本更正/同cut空窗先后和并发观察。B-P3全部门禁通过才可关闭本项；正式供应商、生产支付、1Panel/HA与性能验收仍须对应外部环境。

每个实施分项同步统一架构、HLD/LLD和准确验证状态；根汇合后执行架构语义复核、refresh/check、相关专库race与仓库必检，README清楚区分已实现和候选，再推送并核对精确远端提交和CI。

## 7. 跨observer/runtime的固定兼容材料

共用无权威叶子 `internal/fundinglotprefixcompat` 保存V3兼容材料，避免维护observer和APP runtime各自实现一份可漂移的历史映射。表固定12项：原六个LegacyV1发行tuple+原109表basis descriptor→对应V3 current tuple，以及六个新V3发行tuple+新112表descriptor→同一V3 current tuple；mode/variant不能交叉。未存在的WalletReadV2 epoch发行路径不凭推测加入。

每项包含mode、variant、historical profile、准确basis descriptor、完整historical/current expected/schema/roles三元组。固定manifest仍是Original22；loader核独立材料SHA、闭合JSON、精确12项、digest形状与唯一性。`Match`只输出普通静态映射数据，输入/返回值不认证数据库或财务事实；caller仍必须先验证实际current CatalogProof和原tx，并比较严格DecodeV2原件的全部身份/内容绑定。旧兼容文件及其loader接受集不变，新映射仅从新的显式V3路径可达。

同一份材料同时用于V3 observer历史原件比较和V3 APP composer。fresh V3初始化的proof绑定新descriptor及发行时V3 tuple；观察经营后历史proof不改签。legacy原件只在精确旧tuple/旧descriptor匹配时允许映射，完整新图及origin缺证仍能阻断资格。正式迁移改变current manifest时本材料不会自动升级。

## 8. catalog 有效权限行解码热点（先设计后实施）

2026-11-03 的受限 sandbox 实测中，原 LegacyV1 正例也耗尽原总5秒：driver约0.63秒、driver之间约4.37秒；独立5.20秒CPU采样中，`decodeCatalogJSON`累计1.67秒，占全部CPU样本39.76%。V3先前失败的最后固定查询SHA前缀`1f4b3ea3fa69b144`精确对应application `effective_privileges`，sentinel14153涵盖8角色×(128表+1173列+209函数+258类型+1schema)。采样确认唯一collector的通用解码是可优化热点；driver间隔不能直接计为CPU，失败原因还须独立排除系统睡眠和墙钟扰动。不能删对象、放宽期限、改expected、调整GC/测试重试来掩盖失败。

允许在`decodeCatalogJSON`中为固定PostgreSQL jsonb生成的五字段effective-privilege对象增加包私有受限快解码器：准确`kind/role/object/privileges/grant_options`，两个准确三元uint32 `$ref`，布尔向量最多7项；kind须首位，其余四字段顺序不限，全部字段各出现一次、合法完整JSON至EOF、字符串仅无转义ASCII、数字仅准确规范整数。返回与原通用decoder逐节点相同的普通object/[]any/json.Number，数值不得改成float64，输入字节不得被借用或持有。

快解码器只接受能独立证明上述闭合小形状的子集；未知字段、重复字段、首字段不是kind、转义、非ASCII、非法或其他形状均回退原通用decoder与原检查，不提前判为可信、不扩大最终接受集。记录大小与UTF8先检不变；快形状最大深度/节点数低于原固定界，其他输入仍保留原深度/节点/数值/字符串检查。所有registry SQL、逐对象raw/effective权限及grant option、原行/字节/类别sentinel预算、依赖重复计数、规范化排序、全tuple比较、前后原身份、实际LOGIN、单一5秒ctx均保持；原三种profile共用此唯一core，旧材料/golden不改签。

验收要求独立通用decoder差分、完整合法向量和错误/回退输入、所有原catalog纯测试及旧root接受集回归；真实PG仍以完整正例和受控负例为准，diagnostic/profile只定位热点。新sandbox测试须使用准确APP search_path的实际维护LOGIN，并在同一原事务施加每项drift之前先取得正例，避免错误身份导致负例伪通过。纯基准和race采样不能替代数据库与生产验收。

# 022-C0-V0-R：caller-owned RR 原始账本读取（根冻结）

2026-11-01。根起草，经独立审查修正history遮蔽、内建列类型及分页collation/operator后冻结本合同，授权所列私有reader与专项；现已实现并通过[限定真实PG/race专项](../../docs/verification/funding-ledger-reader.md)，无运行入口。依赖[V0-L纯账本行合同](c0-verifier-contract.md#12-首个有界实施片-v0-l原始账本行独立纯重放根冻结)及[迁移history核验](c0-snapshot-contract.md)。本片仅读取原001–022的三张账本表并调用独立纯检查；不实现全资金来源、C0 lot/epoch、Permit、RuntimeProof、维护开账、HTTP或普通main装配，也不修改旧VerifyLedger/VerifyFunding/Rebuild。

## 1. 私有接口与证据范围

只授权新增`internal/billing/funding_ledger_read.go`及对应专项测试；沿用billing包，不增加新的授权/证明包。接口为：

```go
type fundingLedgerJournalFacts struct {
    ID, TenantID, OperationID, Kind, Reference, Actor, Currency, CreatedTXID string
    CreatedAt time.Time
}
type fundingLedgerSnapshot struct {
    rows fundinglotverify.LedgerRows
    journals []fundingLedgerJournalFacts
    check fundinglotverify.LedgerRowsCheck
}
func readFundingLedgerSnapshotTx(context.Context, pgx.Tx) (fundingLedgerSnapshot, error)
```

三字段全私有，只供未来billing独立source组合器内部使用。`rows`为三表全部行的算术投影，`journals`保存同页原journal的额外业务/生命周期原列；二者按相同ID一一对应，不能只为适配纯算法删掉字段。`check.Scope`仍为`supplied_ledger_rows`，`RowsConsistent`只表示这些行的结构与算术一致。成功读取不发权限或opaque proof，不返回Balanced/CoverageComplete，也不使缺失其他域的组合器获得通过位。

本片没有public wrapper、便利Store方法或新CLI。现有入口不调用它；后续composer只能自己在同一个经授权RR中调用本私有函数并消费其结果，不能接收外部报告或“已读全”bool。没有跨事务缓存、全局可变state、HTTP参数或允许跳表的选项。

## 2. 调用方事务与只读边界

只接受非nil/非typed-nil、具体wrapper保持一致、Conn非nil、TxStatus为T的活跃顶层或调用方受审RR事务。只允许`transaction_isolation='repeatable read'`及read_only为on/off；RC/Serializable/已关闭/aborted均固定错误。wrapper本身不能证明调用方从未手写SAVEPOINT，本片不宣称取得维护顶层事务所有权。

入口继承caller deadline并收紧为最多30秒，不逐页重置；nil或已失效context在DB操作前拒绝。先核本节身份及§3四表的窄shape，再使用`migrations.VerifySnapshotTx`核本snapshot可见的完整latest history。该helper内部非限定引用schema_migrations，所以调用前必须确认`pg_catalog.to_regclass('schema_migrations')`解析到本schema已核meta表的精确OID，拒绝pg_temp/其他search_path遮蔽；同连接串行调用图禁止中途修改path。所有SQL均是静态SELECT，不Begin/Commit/Rollback、SET/DDL/GRANT、业务/维护/advisory锁或网络调用；不修改调用方GUC，DB取消后的aborted事务由caller回滚。

从同tx取得并固定实际database/schema OID、schema name、backend PID、规范top xid、current/session role及Config.User；current=session=Config.User且角色为LOGIN。schema必须是实际current_schema，拒绝系统/临时namespace。该要求只排除SET ROLE伪装，不把任意有SELECT权的LOGIN认定为APP或维护身份。未来composer仍必须另核其CatalogProof/Permit及实际APP身份；本函数不代替它。

返回前在同wrapper/Conn/PID/top xid和DB/schema/role下复核RR与活跃状态；任何变化返回整个零snapshot。它不声称观察任意恶意Go调用方或离线owner任意SQL。事务提交/回滚始终归caller，成功返回不表示COMMIT已确认。

根补冻helper函数解析registry：在窄shape/history表OID guard通过后、调用`migrations.VerifySnapshotTx`前，以静态只读SELECT分别核`pg_catalog.to_regprocedure('current_setting(pg_catalog.text)')`与`pg_catalog.to_regprocedure('pg_catalog.current_setting(pg_catalog.text)')`、`to_regclass(pg_catalog.text)`与显式`pg_catalog.to_regclass(pg_catalog.text)`的实际OID相等且均非NULL；resolver、cast与OID比较operator都限定pg_catalog。旧helper内部两处未限定函数引用保持，不允许caller path上的同名用户函数获得调用机会；解析不符返回Execution与零snapshot。两项registry不是完整catalog认证。专项分别安装带副作用或RAISE的同名函数，要求预拒且确认用户函数未执行。

由于helper实参是unknown字符串literal，精确一参signature比较不能封闭默认参数/VARIADIC重载。根进一步冻结：用`pg_catalog.pg_proc/pg_namespace`和`pg_catalog.current_schemas(true)`拒绝当前有效path内任意非pg_catalog namespace里的上述两个proname，覆盖所有signature，不按参数类型/数量筛选；同样只解析metadata，不执行函数。保留前述精确signature比较，并增加default/variadic副作用反例。该拒绝是窄helper调用前置，不扩大为完整schema/函数认证。

## 3. 三表可见性与精确列

在取业务行前以静态catalog查询核三业务表及schema_migrations均为本schema的permanent ordinary heap、同一个实际object owner、无RLS/forced-RLS/继承/分区/外表/view替换；目标名由固定注册表给出。分别按`pgx.Identifier{schema,table}`引用，不接收caller表名。否则固定错误，不依据checksum正确就接受被隐藏的行。

还须核实际使用列的attnum/name、内建type OID、not-null及无dropped/generated/identity替换：三业务表按001/002原声明（账户id/tenant_id/kind为text、balance为int8；journal前七TEXT列、created_txid/int8、created_at/timestamptz；line前三TEXT列、signed_delta/int8），只有account.tenant_id可NULL；schema_migrations前二列name/checksum为非NULL TEXT，applied_at为非NULL timestamptz。禁止domain/citext/自定义类型冒充内建列；本片只认证所用列，不以此接纳未知额外字段为完整schema。所有catalog与身份函数都以pg_catalog限定，SQL金额/参数text cast同样使用pg_catalog.text。

分页不能在非唯一key上运行：四表还须有原非deferrable/validated PRIMARY KEY，三业务表单列id、meta表单列name，均为attnum1；恰一对应有效/ready/live/immediate的unique/primary btree backing index，无expression/predicate/include列，builtin pg_catalog.text_ops。否则第128/129行相同key可能被后页严格大于游标隐藏，必须先拒绝，不能靠页内去重代替此窄前置。只核所需PK/index关系，不冒称认证全部索引。

这只是防止SELECT范围被常见替换/隐藏的窄检查，不是完整catalog/ACL/trigger/正文认证；后续全源入口仍必须先跑静态C0 checker。此片不调用任何写侧valid谓词、planner、金额SUM/COUNT或自动拼schema。

显式投影精确依[V0合同§11.1](c0-verifier-contract.md#111-静态reader原则与ledger精确投影)：

| 表 | 原列顺序 |
| --- | --- |
| ledger_accounts | id, tenant_id, kind, balance::text |
| journals | id, tenant_id, operation_id, kind, reference, actor, currency, created_txid::text, created_at |
| journal_lines | id, journal_id, account_id, signed_delta::text |

从每张表自身读取全部行；无JOIN、业务kind/tenant/余额过滤。account tenant以*string保留NULL与空TEXT差别。BIGINT text进入独立纯层；额外journal的CreatedTXID也须规范正int64十进制，CreatedAt须可表示为非零UTC年1..9999微秒时刻。所有ID保持原TEXT字节，不trim或UUID化，不假定历史主键一定非空。结果不输出行ID到日志/错误。

Kind/Reference/Actor等额外TEXT只核有效UTF8/无NUL，不强加原002没有的非空限制；允许空TEXT。CreatedAt从driver扫描值统一转UTC后核年份/微秒，不要求driver原Location指针恰为time.UTC。

## 4. 全量分页、资源与EOF

每表固定128条keyset，排序和比较均固定`id COLLATE pg_catalog."C"`，cursor使用`OPERATOR(pg_catalog.>)`且参数显式pg_catalog.text；ORDER BY也固定pg_catalog比较operator，不从search_path选择同名collation/operator。第一页由私有hasCursor=false表示，不以空string充当下界；后页必须严格大于上一页末ID的原UTF8字节C序，页内同样严格递增。整128行尾页继续取下一页，只有实际<128条并完整消费/检查Rows.Err/关闭Rows才完成该表。Rows关闭后才能发下一条查询；scan/query/cancel异常不给部分结果。

不得逐页调用CheckLedgerRows并AND：跨页journal/account合计及自然键重复必须在三表完整收集后统一检查。收集阶段复用纯层的逻辑硬界：三表总行数≤100000，所有原字符串字节（包括额外journal字段）≤16MiB；超界固定resource错误与零snapshot，不截尾通过。该边界是输入和保留buffer限制，不承诺pgx协议缓冲或整个Go堆/RSS上界；没有强制载入更大库的选项。未来更大账本需要另冻流式累加器或其他有界算法。

字节按三表原始投影每列/每行出现一次累计；journal算术投影与额外facts复用的ID/TenantID/OperationID/Currency不重复计数，额外Kind/Reference/Actor/CreatedTXID各计一次。time.Time不是输入string字节，不拿其格式化值额外占该逻辑配额。

本片不新增索引、不声称生产长历史性能已合格；专项须保存实际keyset查询EXPLAIN观察结果，判断C排序是否能利用当期索引。后续生产核验的索引/DDL及量级预算另审，不能为快而换不等价的排序或删除完整性扫描。资源/超时失败是执行失败，不是indeterminate/balanced。

## 5. 安全错误与验收

只返回四种billing私有sentinel：`fundingLedgerReadInput`、`fundingLedgerReadResource`、`fundingLedgerReadCanceled`、`fundingLedgerReadExecution`，其固定字符串分别为`funding_ledger_read_input/resource/canceled/execution`。无driver unwrap、SQL、角色/schema/业务ID/原值；失败snapshot为整个零值。nil/typed-nil/nonRR/坏事务身份为Input；逻辑预算为Resource；入口/运行context失效为Canceled；history/catalog/权限/SQL/scan/非规范额外原列为Execution。纯层Input在真实DB读取路径中是损坏/执行错误，映射Execution；其Resource/Canceled对应映射，mismatch正常返回完整snapshot。

真实PG专项必须使用显式TEST_DATABASE_URL专库和隔离schema。复用既有真实业务fixture生成非空人工入账、Reserve/settle/消费退款，再由实际受限APP LOGIN在caller RR核三表；APP禁止DDL/事务控制权限扩大，worker/projector不可借本函数获得新SELECT。至少验证：

1. RR RO/RW均可读；caller未提交sentinel仍由caller决定COMMIT/ROLLBACK，函数不偷偷提交；同RR旧快照/新RR观察差别符合MVCC。
2. 三表0/1/128/129+行、空TEXT与Unicode ID、跨页同account/journal累加；恰128尾页实际空EOF；原caller快照未被换连接或换事务。
3. 物理权限拒绝、RLS/view/inheritance/列domain替换、缺history/改checksum及pg_temp同名history遮蔽、nonRR/closed/typed-nil/预取消均固定错误和零snapshot；实际APP current=session=Config.User，SET ROLE不能冒充。分页builtin collation/operator/cast不得受同名目标schema对象影响；支持的数据库用同名对象负例，另以静态SQL检查防回退。
4. 受控owner坏数据fixture的跨tenant/缺父/重复自然键或不平衡行按纯层独立计数，不被JOIN丢失；不把单页或仅某tenant结果误当整库。
5. resource边界、查询中取消/Rows.Err与caller回滚；所有Rows/pool/随机角色schema清理确认，原长期预览不变。记录EXPLAIN，不能据此宣称网关p95或完整全源验收。

先专项race/vet，再汇合全仓检查。只读reader的通过不授予生产来源、维护开账或运行许可；完整静态catalog、Permit、source/Key/采购/PB/B图与C0 lot重放仍是后续门禁。实施后由根更新架构模块/依赖与能力状态，再按AGENTS汇合刷新；候选阶段不刷新manifest或修改现有生产入口。

# 022-C0-T3：受控读取租约与同快照核验（根冻结）

2026-11-02。根对照T2实现、pgx v5.7.6 QueryRewriter/RowScanner调用路径及既有ledger/PB reader后冻结本合同。它替代Snapshot生命周期§6–7的旧Rows/Finish候选形状。T3持续目标仍为独立完整来源核验；本合同先交付真实ReadLease与ledger/PB两条实际维护reader，随后逐项完成资金/Key/采购/B来源与完整组合，不将局部完成凭据称为CoverageComplete，不开放basis/epoch/writer/生产CLI。原T0–T2、SQL/ACL/expected/v1及资金预算保持。

## 1. 公开接缝及范围

~~~go
type ScanKind uint8
const ( ScanPeriodicBudget ScanKind = iota+1; ScanLedger; ScanFunding; ScanProcurement; ScanSandboxSources )
type ReadLease struct { /* private shared state */ }
func (p *Permit) BeginScan(ctx context.Context, kind ScanKind) (*ReadLease,error)
func (l *ReadLease) Require(kind ScanKind) error
func (l *ReadLease) Query(sql string,args ...any) (*Rows,error)
func (l *ReadLease) QueryRow(sql string,args ...any) *Row
func (l *ReadLease) Finish() (*ScanProof,error)
func (l *ReadLease) Abort() error
func (l *ReadLease) Deadline() (time.Time,bool)
func (l *ReadLease) Done() <-chan struct{}
func (l *ReadLease) Err() error
type Rows struct { /* private */ }
func (r *Rows) Next() (bool,error)
func (r *Rows) Scan(dest ...any) error
func (r *Rows) Err() error
func (r *Rows) Close() error
type Row struct { /* private */ }
func (r *Row) Scan(dest ...any) error
type ScanProof struct { /* private shared completion identity */ }
func (p *ScanProof) Verify(permit *Permit,kind ScanKind) error
~~~

全部字段私有、零值无效、复制共态、String/GoString/JSON固定redacted。无RawValues/Values/Conn/Tx/Context/Exec/Begin/Commit/CopyFrom/回调、FromDigest或可由caller填的通过位。Deadline/Done/Err仅是只读取消视图，不返回原context，不提供Value；领域纯CPU核验可用私有context adapter把这三项映射给原算法，SQL始终使用租约内部context。Err返回固定安全错误而非driver。

ScanProof只证明这个固定kind的lease正常结束，绑定原Permit/Snapshot/lease nonce；它不证明某SQL读了全部来源、不授资金权。Verify在短锁内只核精确私有指针/nonce、成功完成和原Permit仍有效，不能靠同摘要/同PID重绑；原Snapshot终结/取消/失效即所有复制proof失效。允许另一lease持busy时作纯绑定核验，不并发SQL。每个Permit同一kind只尝试一次，成功后的重复BeginScan拒绝且不撤销已完成结果；失败撤销整个Snapshot。当前无PostInit枚举或入口，未来封闭writer合同另冻。

## 2. 生命周期与预算

BeginScan取得原gate.busy，校验合法kind、原Permit及状态；由原stageContext建立单次call，固定最多30秒并取caller/原Snapshot更早期限，含入口校验、全部SQL/CPU和Finish出口。CPU gap同样用原raw-close watcher，不逐query重置。设置Snapshot.activeLease和permitCall以供原authority.verifyEvidence工作，不伪造active Attest、不另Begin或另pool。入口/出口均同rawTx完整verifyEvidence，包括新鲜停服采样。

BeginScan到Finish/Abort整个生命周期持续占原busy，因此Permit.Verify/第二scan/Commit/Rollback/Close返回maintenance_gate_busy且零SQL。租约另有单operation所有权，Rows方法、Query/Row.Scan和Finish均只让一个goroutine使用pgx。并发操作可直接返回busy，不毒化原owner，不把忙误报为EOF；因此Next和Close均有error。短锁不能包SQL。

Finish有operation时busy；没有operation但仍有未消费Row、未完整EOF/Err/Close的Rows则永久invalid/raw-close，零proof。无悬挂对象时执行完整出口验证，再原子标finished并stop call；保持opBusy/gs.busy至callback ack、call.cancel和budgetCancel全部完成，最后清activeLease/permitCall并释放busy。重复Finish/已结束Require/Query拒绝。成功Finish后的defer Abort为nil幂等。

Abort可在另goroutine阻塞SQL时只做共享撤销与原socket关闭，绝不并发Rows.Close/pgx.Rollback/drain。原operation返回后才释放operation/busy；无operation时Abort自己完成收尾。所有失败都保留固定安全错误和零结果，撤销Permit/所有proof，不重试SQL。watcher只改短锁状态并关socket，不清正在读的不可变事实。

## 3. 查询与数据出口封闭

不能把args直接透传pgx：QueryRewriter包括NamedArgs可收到*pgx.Conn，QueryExecMode/QueryResultFormats是控制参数。固定内部simple protocol；参数仅允许exact内建nil/bool/string、int/int8/int16/int32/int64/uint32、time.Time及[]byte/[]string/[]int16/[]int32/[]int64/[]uint32，切片先owned clone。拒绝别名自定义类型、指针、Scanner/Valuer/QueryRewriter与所有pgx选项；回调必须在零调用处拒绝。参数上限1024、单项及累计逻辑bytes最多16MiB，超界固定input，原SQL不会发出。

Scan只接受exact非nil的*bool/*string/*[]byte/*int/*int16/*int32/*int64/*uint32/*time.Time、*pgtype.Timestamptz/*pgtype.Text、*[]int16/*[]int32/*[]int64/*[]uint32/*[]string及nullable **string/**bool/**int64/**time.Time。拒绝*any、自定义RowScanner/Scanner、DriverBytes/UndecodedBytes和其他目标，绝不将原pgx.Rows交callback。内部decode使用受审本地scalar临时变量，再复制赋给目标；[]byte/数组及nullable指针均owned，调用后/下一Next/Close不借用driver内存。目标个数≤1024。无RowScanner或QueryRewriter兼容fallback。

参数逻辑bytes中的[]string按每项8字节长度框架再加UTF8 payload累计，包含空字符串，防止大量空项绕过16MiB逻辑界；数值slice按元素固定宽度计。此为owned参数输入界，不宣称Go堆/RSS界。

ScanProof.Verify还在同锁读取当前未stopped的permitCall取消状态，避免后继lease caller已取消、AfterFunc尚未调度时旧结果仍可绑定的窗口；只核当前call，不能因旧成功call的正常cancel误撤销proof。

Rows每个Next true必须经Scan消费；跳行、Scan前无当前行、重复Scan、Next未消费上行均固定transaction失败关闭。Next false仅在真实EOF且driver.Err为nil时记EOF；Rows.Err和显式Close均必须观察后才释放该query所有权，支持先Err后Close或反序。提前Close丢行失败关闭。QueryRow保存唯一待消费对象，Scan读取恰一行并继续验证EOF/Err/Close；零/多行均execution并撤销，不返回pgx.ErrNoRows或ScanError。SQL/scan/rows错误固定execution或已失效timeout/transaction，不能unwrap。

SQL文本只向受审同进程固定scanner开放，不是SQL解析器/owner恶意代码沙箱。所有实际调用必须静态SELECT、受审标识符与参数值；Init RW写CTE/函数/手写事务控制不能靠路径名判定禁止，须调用图复核。不得将lease接HTTP、插件或任意回调。

## 4. 两个真实reader与后续完整来源

ledger保留原readFundingLedgerSnapshotTx的actual Config.User=current=session LOGIN、RR/Conn身份、窄shape/PK、helper解析与history前置和出口；只把三表完整读取和独立CheckLedgerRows抽包内窄query seam，两入口共用相同列、C序128条至EOF、9列journal额外facts、100000行/16MiB/30秒和完整大整数核算。维护入口VerifyLedgerMaintenance(*ReadLease)只接受ScanLedger，真实T2的完整catalog/history/身份已经承担其前置，不传裸tx、不假Conn。结果MaintenanceLedgerResult字段私有保存完整raw snapshot与ScanProof，Verify(*Permit)和Summary(*Permit)只返回原supplied_ledger_rows摘要，不能升级全源。

PB原public Verify仍自行取得RR RO、原actual LOGIN/六表owner、原history、SET和Commit，B3 installer仍拒绝。抽取唯一内部read-only query core复用原SQL/列/操作receipt核验，维护VerifyMaintenance(*ReadLease)只接受ScanPeriodicBudget，前置由真实T2授权承担。MaintenanceResult私有保存原Verification与ScanProof，Verify(*Permit)仅返回error，Report(*Permit)返回原Verification；Summary(*Permit)才返回显式periodic_budget_rows scope wrapper。三类legacy/prefix缺证继续indeterminate，不因维护授权消除。所有错误整个零结果，Finish失败不返回局部报告。

PB维护专用资源profile另加服务器scalar preflight：固定19关系及其verifierColumns全部声明字段，累计行数最多100000、各非NULL字段cast为pg_catalog.text后的UTF8 bytes每项最多65536、累计最多16MiB；包含audits全表及history，不按业务谓词丢行。只返回scalar资格，在原lease同snapshot/30秒内、完整业务扫描前执行，超界固定ErrVerifyExecution/零结果。此为新增维护兼容边界，public旧入口保持原规则，不宣称旧入口已有这些内存界。该guard限制返回客户端与保留输入，不能宣称界定PG排序/CTE/array_agg的实际内存或Go堆/RSS。原SQL判定与三态不改。schema从lease内固定SELECT pg_catalog.current_schema()取得，并与authority固定path保障一致；不收外部schema字符串。

Funding、Key、采购、B sources及lot全图仍须独立逐列scanner、双向sweep、完整rows与coverage清单，禁止复用写侧funding_valid谓词或PB局部SQL凑全源。两个局部结果与ScanProof不能成为init/basis/epoch就绪条件。

## 5. 验收与架构

真实普通与genuine sandbox Plan/Init经过原Acquire→Snapshot→VerifyInstallation→Attest→BeginScan，验证同PID/top xid/RR旧快照、入口/出口新runtime连接、所有操作/终结busy零SQL、rows遗漏与EOF/Err/Close、回调/借用目标拒绝、SQL/CPU取消物理关闭、复制/replay/终结撤销及安全错误。callback测试记录零调用，不靠error text猜拒绝。原reader/PB公共入口做等价回归，不放宽原owner或历史缺证。纯fixture只测predicate，不能冒称公开维护链。

重PG仅固定专库串行，精确资源清理，不碰moni_review/58123/父public ACL或其他会话；不调用付费模型/PSP。相关专项后汇合独立审阅、19图、统一架构/HLD/LLD/SDD/README状态，再根显式refresh与整仓check/verify、提交推送；完整T3及后续goal仍按实际覆盖持续推进。

测试装配因billing维护reader新增生产依赖billing→maintenancetrust，原内包测试直接import paymentsandboxqa会形成测试循环。仅_test.go以DTO（Roles/Schemas）与原CreateApplication/OpenPool/Close/VerifyLegacyFundingUnchanged方法装配：外包init注册真实QA factory，内包仍执行原genuine安装、固定源SHA、全部私有authority/catalog/funding oracle。无生产注册、替代issuer、可注入Permit或伪造proof；正式依赖图不增加测试边。

物理客户端socket关闭不保证PostgreSQL立即中断任意长CPU/等待SQL。取消验收分别检查独立wire.closed及时触发、原SQL owner安全返回，并以短且真实PgSleep屏障的语句观察服务器自然下次I/O后的PID/start/锁消失；不修改服务端client_connection_check_interval、不主动terminate、不延业务预算。长SQL中断与目标部署的服务器参数前提须另行冻结和验收。

## 6. 集成启动开销与静态缓存合同（首片历史，当前依赖以§7为准）

新增billing/PB到maintenancetrust的生产依赖也会在普通main及其子进程中初始化fundinglotcatalog。整仓首次执行暴露CLI、SIGKILL恢复helper和真实main启动超时；无数据库的race空跑确认两个固定root的完整初始化约14.8秒，主要开销为重复canonical编码和缓存构建。不能以放宽既有60秒CLI、12秒helper、main等待或五秒catalog发行预算解决。

本次只允许优化私有静态缓存：固定expected原字节与SHA、包初始化时完整认证、strictJSONNodes的所有独立资源/重复键/数值/UTF8约束、完整DTO/模板校验、全root canonical与原raw逐字节相等均保持；没有lazy发行、公开skip或外部expected输入。在这些校验全部成功之后，可从同一已证明canonical的原raw提取私有Schema/Roles与category的规范JSON片段，复用其bytes构建原缓存，避免对同一大树重复编码。片段的mode、variant名字/顺序、scope/category须与刚验证的私有对象精确关联；提取错误整个失败，不能降级为未校验对象。

共享map/value仍仅在完整canonical bytes相等时intern，digest仅作索引，命中后必须bytes.Equal；不修改actual、expected或冻结源文件。缓存bytes拥有私有生命周期，不借用可被caller修改的输入；无exported原始map/bytes出口。实现先做无PG的相同已构建race二进制启动对照和缓存等价/负例，再串行执行原失败CLI、SIGKILL和main测试，最后整仓验收。启动测量不冒充网关首字节或模型首token性能。

实现采用刚完成全root比较的owned canonical编码结果建立私有单次边界索引；只展开root、variants、variant、schema/roles及scope五层，category以下仅平衡容器并跳过完整转义字符串。索引总节点最多4096、JSON深度最多64、原64MiB输入界保持，片段以len为cap，不能append覆盖邻接片段。按mode、版本、variant数量/顺序/name、family、scope及字段数量和名称对应同一已验证DTO，任一不符整个失败；索引不是通用JSON验证器，不能脱离前面的严格解析及完整规范比较用于actual或外部输入。缓存内容等价、复杂字符串边界、caller raw改写与跨decode隔离、重算SHA的非规范/重复键/尾随/数字别名拒绝由纯测试分别验证。

另允许将固定record/attribute字段类型描述在包变量初始化时一次解析为私有有序字段表，避免每条record重复Fields/SplitN；字段名、次序、类型、exactKeys、逐字段validateValueType与枚举约束全部保持。此为同一封闭schema的预编译，不复用任何actual的判定结果，不新增可配置registry或校验绕过。

expected专用严格解析接缝允许独立实现等价的bounded raw递归构图，代替逐Token分配；原actual/candidate的strictJSONNodes不改。新parser必须保持同一accepted language和owned树类型：全raw UTF8与64MiB、根depth0且最多64层、值节点最多4000000（key不计节点）、array最多100000、decoded字符串值最多65536 UTF8 bytes且无NUL、decoded key查重、原canonical number规则及64字节界、完整EOF。object key保留原parser语义，不能悄悄套用字符串值的NUL/大小界。无escape字符串可owned复制，escape必须用encoding/json.Unmarshal维持原surrogate/转义语义；禁止unsafe或借用输入。固定SHA先行、完整DTO/模板、whole-root canonical==raw及上述私有缓存门禁全保留，不提供外部输入或skip开关。

新parser的生产启用须先完成原Token parser逐树差分：固定合法/非法语料、decoded重复key、数字与EOF、UTF8/控制符/代理项、文本/数组/深度/node/64MiB临界、完整两root、生成合法JSON及变异、独立fuzz；接受差异或树值差异均阻断。纯测/启动测量不替代原catalog真实PG及CLI/SIGKILL/main回归，相关正式源码必须再整仓验证。

第二轮默认跨包并发整仓仅剩真实main的10秒角色拒绝观察超时，而同一源码四项原失败进程在串行专库回归全部通过。根与独立审阅者冻结本地 `make verify-mvp` 的Go包调度为 `go test -race -p 1 ./... -count=1 -timeout 600s`：同一个专库按包串行，不删包/用例、不改选择器/skip、race、GOMAXPROCS、包内goroutine/多连接并发或原10/12/60/600秒期限及业务预算。总wall可能增长，串行通过不授并发启动余量或容量结论；CI独立runner/数据库与make test的执行合同不因此改变。未落地的strip/key-intern微优化仅保留.local，不作为发布实现或通过证据。

## 7. CI 启动回归后的维护集成隔离（根冻结）

精确提交 f572bbd 的 CI run 41359784155 暴露普通服务和 billing 测试子进程的 catalog eager 初始化超过原 10/12 秒观察期限；本地串行通过不替代该失败。修复采用独立 `internal/maintenancescan` 集成包：它单向依赖 billing、periodicbudget 与 maintenancetrust，普通 billing/PB/main 及其测试不得反向导入该包或 maintenancetrust/fundinglotcatalog。catalog 包仍在真正维护消费者中 eager 完整认证，不改固定 root、pins、SQL、五秒发行预算或任何原测试期限。

两个原领域包保留唯一固定 SELECT/分页/receipt 核验算法和原 public reader 的 actual LOGIN/owner/RRRO/history/SET/Commit。仅导出仓库 internal 范围的窄 raw-facts query/rows 接缝及固定算法入口，供集成包复用；它们只返回不可信的局部原始事实/描述，不接受或发行 Permit/ScanProof，不提供任意 SQL 执行入口、通过位、回调注册或外部结果转 proof 的构造器。PB 的 19 关系/146 字段维护资源 guard 仍由固定算法先执行；原普通入口不新增该 guard。调用方可实现假的 query 至多得到无权威局部数据，不能铸造可信维护结果。

maintenancescan 的公开维护入口仅接受真实 `*ReadLease`，自行构造私有 adapter，执行 Require→固定领域算法→Finish，结果以私有字段保存此次 raw facts 与真实 ScanProof；禁止接收 caller 预计算报告或自定义 provider，Verify/Summary/Report 仍绑定原 Permit 与固定 kind。读取 schema 仍来自租约中的固定 SELECT，禁止维护入口外部指定 schema。取消视图仅 Deadline/Done/Err，无 Value 转发。错误必须整个零结果，所有 authority 和 scope 边界保持。

原 lease/result/redaction/取消适配测试移到集成包；领域 core 的故障传播测试改用无维护依赖的 opaque sentinel，全部断言保留。真实 Plan/Init 双读取器与资源边界工具改用新集成包。增加可执行依赖图门禁，检查普通生产和 billing/PB 测试依赖不含维护/catalog，防止仅移动生产文件但测试仍触发 eager 初始化。修复后先验证原失败进程、真实双 reader/资源边界和无权威 core，再完整整仓及新精确 commit CI；历史失败/成功记录保留原身份。

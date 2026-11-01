# 022-C0-T：离线维护 Permit、真实身份与同快照 gate（精确候选）

2026-11-01，**仅候选，待根冻结；本稿没有实现、生产授权、运行通过或 epoch 已发行声明**。细化[维护合同 §4](c0-maintenance-contract.md#4-私有-maintenance-permit-与同一rr核验)，配合[静态 schema/角色检查](c0-schema-check-contract.md)、[独立来源核验](c0-verifier-contract.md)、[迁移 snapshot 接缝](c0-snapshot-contract.md)与[运行时 helper](c0-runtime-helper-contract.md)。C0 安装、维护身份、业务来源、完整运行证明是不同证据；任何一项通过不代替其他项。

本片只允许停服的 `plan` / 首次 `init` 维护链。它不安装迁移、不 provision/GRANT、不取得模型或支付网络能力，不变更原 [PB Verifier](../../internal/periodicbudget/verifier.go) 的 ordinary actual LOGIN 真 owner 规则。当前不存在的完整 C0 epoch proof 不能成为首次开账的循环前置。

## 1. 包、API 与生命周期所有权

候选包 `internal/maintenancetrust` 是封闭维护基础设施，只依赖标准库、pgx 与静态 `internal/fundinglotcatalog`；不 import billing、periodicbudget、payments、operations 或任何网络 mutation adapter。CLI 维护协调器可以调用其固定 API，scanner 只接收 caller-owned tx 与 Permit。所有导出类型字段私有；零值无效，复制值仍共用私有状态，不能复制出第二份权限。String/GoString/JSON 仅返回固定 redacted 形状，不编码连接配置或 proof 原文。

```go
type Gate struct { /* private shared state, dedicated *pgx.Conn */ }
type Snapshot struct { /* private shared state, issued top-level pgx.Tx */ }
type Permit struct { /* private shared state and verified bindings */ }

func Acquire(ctx context.Context, cfg *pgx.ConnConfig,
    pins fundinglotcatalog.Pins) (*Gate, error)
func (g *Gate) BeginPlan(ctx context.Context) (*Snapshot, error)
func (g *Gate) BeginInit(ctx context.Context) (*Snapshot, error)
func (s *Snapshot) Tx() pgx.Tx

func AttestTx(ctx context.Context, tx pgx.Tx, snapshot *Snapshot,
    proof *fundinglotcatalog.InstallationProof) (*Permit, error)
func (p *Permit) VerifyTx(ctx context.Context, tx pgx.Tx) error

func (s *Snapshot) Commit(ctx context.Context) error
func (s *Snapshot) Rollback(ctx context.Context) error
func (g *Gate) Close(ctx context.Context) error
```

`Acquire`深复制配置与pins（包括Worker/Sandbox等指针及所有嵌套值）后新建且独占一条物理连接，不从 serve/runtime pool 借连接，不接受 caller 提交的“已持锁”连接。pins仅是未受信的比对目标；Acquire/preflight通过不把它升级成身份或catalog证明。连接失败不返回半 Gate。该连接只用于本次固定维护命令；Tx()只供受审同进程 scanner/写入协调器使用，不进入 HTTP、插件、回调 SQL 或任意 statement API。受审 Go 代码仍能调用 pgx；这些 token 不声称抵御恶意 owner 或任意 Go 代码。

Gate 只发行一个 Snapshot、一个 Permit；不能在同 Gate 先 plan 再 init，不能 Commit 后 Begin 第二事务。`plan`与`init`是分别取得新 gate、新 RR 的独立命令。Permit不公开 Reset、Extend、Clone、FromJSON、FromDigest、AcceptRole/AlreadyLocked、Trusted 或测试跳过口。

状态为 `connected → instance_held → both_held → snapshot_active → attested → closing → committed|rolled_back|unknown → closed`。Snapshot 开始结束操作前即撤销 Permit 的后续使用；结果失败、context 到期、失锁、身份/proof不符均永久 invalid，不能重新 attest 同 Snapshot。所有操作串行，禁止同一个 Gate/tx 并发 scanner、Commit、Close；私有共享状态的 busy 检查遇重入直接安全拒绝，不让并发 SQL 争用 pgx connection。

## 2. 固定 gate：实际取得双 session 锁

Acquire 总预算为父 deadline 与30秒的较早值。每条 SQL 均受该剩余预算限制；不重置全程时钟。新连接必须 idle、无错误、无现存 advisory 锁。事务外只允许静态会话/endpoint/目标 schema 的非权威 preflight；preflight 不发行 Permit，也不能代替 RR 内证明。目标名从该连接实际 `current_schema()`解析，拒绝 NULL、pg_catalog、information_schema、pg_temp、系统 schema 或不在受审 pins 的 namespace；名称只能经 pgx.Identifier 引用，不能拼接 caller SQL。

依次调用 `pg_try_advisory_lock(782193402::bigint)`、`pg_try_advisory_lock(782193401::bigint)`：前者是 instance gate，后者是 migration gate。每50ms有界轮询 false；只有收到明确 true 才记为该 key 成功取得一次。不得先取401，不使用 shared/xact/two-int 版本，不再次申请已持有的 key，不调用迁移 apply 或另开连接取得其中一把锁。迁移尚未安装必须在本命令之外完成；本片不会一边持401一边调用会重入401的 installer。

每次取得后与每次 Permit.VerifyTx 都以静态 pg_locks 查询核对：本 backend PID、实际 database OID、`locktype='advisory'`、granted、`mode='ExclusiveLock'`、`classid=0`、objid分别为782193402/782193401、`objsubid=1`；两把各恰一条，无额外 advisory key或 shared同 key。pg_locks不暴露重入计数，故“恰取得一次”由新专用连接、固定两个请求及私有状态共同保证，不能只凭两条 catalog 行声称无重入。

false是尚未取得；超时/取消/连接错误或回包丢失是取得结果未知，立即 invalid 并销毁本专用连接，不继续轮询、返回池或另发相同 lock 请求。若只取得402，401未取得，则错误路径也必须清理402。锁不是跨数据库全局服务栅栏；绑定并核对的是当前 database，不能拿另一个库的同数字锁充当证明。

已知事务结束且连接idle时，独立 background cleanup **总计最多5秒**，逆序 `pg_advisory_unlock(782193401::bigint)` 再402；每次必须明确返回true，最后核本PID无这两把锁。任一失败/未知均物理关闭连接。不得使用 unlock_all 掩盖次数/归属错误。无论正常与否专用连接最终都关闭，不进入任何可重用池；Close可重复安全调用，但不会重复unlock已消费的锁状态。context已取消不能跳过独立cleanup。

服务锁成功不单独证明 P/Q 已停或 PSP 已无在途：sandbox 安装 pins 中所有实际 runtime LOGIN必须停服，维护协调器在同RR以实际datid/usesysid核受审 runtime角色集合在目标DB的连接数为0，包含idle、active与idle-in-transaction，不按当前query文本或application_name过滤（Gate自身维护LOGIN不在集合内）。P/Q进程须按原合同取消并join；若还存在其受审role连接，plan/init拒绝，不代替用户杀进程。其他owner绕过gate手工写库/改角色不属于本token可抵御的权限模型；受控维护期间禁止并发人工DDL/owner写。init的业务表锁另提供持久数据的物理停写保护。

## 3. RR 建立、125 表锁与首次快照

BeginPlan/BeginInit只能从 both_held、idle 的Gate发行由 `conn.BeginTx` **直接产生的顶层wrapper**。事务分别固定为 `REPEATABLE READ READ ONLY` 与 `REPEATABLE READ READ WRITE`，不接受已有裸tx、Serializable/RC、DEFERRABLE替代或pgx nested wrapper。保存私有wrapper引用后才向受审协调器返回；不是从 caller claim 猜它是不是顶层。

BeginPlan/BeginInit各自使用当次caller的有效context：无deadline时收紧为30秒；有deadline时取其剩余时间与10分钟的较小值，剩余不足1秒直接拒绝。Snapshot不得延长该父deadline，后续stage不得重置此总预算；Acquire已完成的至多30秒子context不拿来复用或延长，调用方仍须保持原父context有效。固定 `SET LOCAL search_path`（目标schema、pg_catalog、pg_temp）、`lock_timeout`（最多3秒）及 `statement_timeout`（不超过剩余总预算）由Begin执行；采用静态SET utility，不以 `SELECT set_config(...)` 提前取得RR数据快照。后续scanner不改GUC、不另Begin、不换连接。

Init在任何SELECT（包括current_user、current_setting、pg_locks、migration history、catalog或来源读取）之前，立即执行一条静态 `LOCK TABLE <全部125表C序> IN SHARE ROW EXCLUSIVE MODE`。125恰为维护descriptor冻结的109原表与16个C0表的**集合合并后C序**，不是109后再附16；无通配符、运行catalog发现列表或CLI表选择。每个标识符全限定为事务外preflight的目标schema。LOCK缺表/权限/等待超时使整tx失败；不跳表、不降模式、不读取部分成功清单后继续。

SHARE ROW EXCLUSIVE 阻止同表 INSERT/UPDATE/DELETE/TRUNCATE与冲突DDL，同时允许普通读。随后按完整account ID C序锁全部既有ledger_accounts行，严格在第一次业务basis/source扫描之前。行锁语句自身只能在全部125表锁成功后执行。静态checker须证明所有业务FK/trigger写目标均在125集合内、没有未审incoming关系；C0 source/epoch触发器只读原表并写本集合。FK校验所需ROW SHARE/KEY SHARE不授权其他连接越过已被125表锁挡住的原业务写；同tx自己的init INSERT/FK/deferred checks按受审图执行。不能把“125看起来够多”当完整关系证明。

Plan RO不取得此写型表锁或account FOR UPDATE；其一致性来自成功双gate、受审runtime全停与同RR。Plan和Init各有一个独立snapshot：Init在实际表锁之后重新读取catalog、完整history、全部原源、basis，与原plan安全pins逐项精确比较；不声称跨命令共享同一MVCC snapshot。若等待表锁时别人先提交旧业务变化，Init必须看到该变化并使旧basis不符；该时间窗口须在真实PG以固定屏障验收，不能只检查看起来相同的最终余额。

## 4. 两种真实维护身份，不接受自报角色

Attest必须重读实际连接配置与数据库，不接受用户名/owner OID/bool参数。两种身份都要求Config.User=`current_user`=`session_user`、相同非零role OID、实际rolcanlogin=true；连接PgConn.TxStatus='T'且无错误。SET ROLE得到owner、SET SESSION AUTHORIZATION后与Config不符、普通APP/worker/projector即使有全库SELECT也拒绝。mode根据已验证installation pins与实际marker派生，调用方不能选例外。

**ordinary：** 通常actual authenticated LOGIN本身就是目标schema nspowner，并且实际拥有PB六事实表 `periodic_budget_controls/windows/allocations/events/operations/configuration_versions`；六表relowner必须是该实际role OID，不以membership、SET权限、superuser“可以读写”替实际owner。唯一builtin解析例外是public namespace的nspowner恰为 `pg_database_owner`：只按当前目标database的真实datdba解析有效namespace owner，并要求datdba=actual LOGIN OID；raw namespace owner/OID与解析后的实际object owner/OID分别绑定且与opaque proof的NamespaceOwner/ObjectOwner精确相等，不解析为任意current_user或其他NOLOGIN组。其他schema/角色不借此扩展例外。目标runtime APP/projector与owner严格不同，owner属性与全部对象归属受静态ordinary矩阵约束。普通维护不要求固定主机或test库；endpoint/DB/schema仍与installation pins精确绑定。原PB公共Verifier不改用Permit、不放宽其六表owner检查，本维护入口是单独路径。

**B3 sandbox唯一窄例外：** 配置主机字面量127.0.0.1、端口55439、DB字面量moni_test、User字面量moni_test，全部fallback同一字面endpoint；实际已连接socket的RemoteAddr为IPv4 TCP 127.0.0.1:55439，SQL `inet_server_addr()/inet_server_port()/current_database()`精确对应，拒Unix socket、localhost/别名、IPv6、不同fallback、代理到另库。`moni_test`必须实际LOGIN、rolsuper=true且`pg_database.datdba`就是该实际LOGIN OID；别的superuser、同名非DB owner或别的DB owner均拒绝。

B3例外只补足三个NOLOGIN owner无法直接认证的维护会话：原App/Mock/Receiver schema名、OID、owner OID和八个distinct角色全部匹配已验证pins，三个owner保持NOLOGIN，五个runtime保持LOGIN；全部八角色无inherit/super/createdb/createrole/replication/bypass/SET-ADMIN-membership/grant-option提升，所有对象owner/ACL按静态矩阵。检查的是这些受审role的实际成员边/权限闭包；不能要求合法installer superuser的 `pg_has_role(...,'SET')`为false而误拒绝，也不能据此给runtime角色继承例外。认证身份仍为moni_test，从不SET ROLE成NOLOGIN owner。

原v1 immutable payment_runtime_attestations必须在同RR完整验证（见§6）；不能仅有一个marker、proof_sha256格式或fixture名字便发行Permit。ordinary看到sandbox marker不降级；B3少了任何原v1证据也不降级ordinary。维护LOGIN的密码/DSN绝不交runtime、HTTP或plan JSON。

## 5. 事务、proof及失效的精确绑定

Attest仅接受本Gate唯一Snapshot.Tx()的同一个顶层wrapper和 `fundinglotcatalog.VerifyInstallationTx(ctx,tx,pins)`所发opaque InstallationProof；先调用其VerifyTx，再调用 `proof.Catalog().MatchesPins(gate.copiedPins)`精确比较全部私有绑定pins，随后由Permit核维护身份和真实gate。MatchesPins为opaque CatalogProof的只读方法，nil/zero=false、不访问DB，比较构造时deep-copy的规范pins精确结构值，不trim/别名化；其返回值不是caller自报权限bool。该接缝与schema-check合同同时根冻，不以几个裸digest相等替代完整DB/schema/role/mode目标相等。普通CatalogProof不够，caller构造的结构体/摘要/相同JSON不够。Gate只消费一次installation evidence发行一个Permit；安装证明不授维护权，Permit也不授新源资金权。

私有绑定至少包括：Gate/Snapshot共享nonce及状态、具体wrapper引用、*pgx.Conn/PgConn指针、backend PID与backend_start、规范top xid8十进制、database名/OID、schema名/OID/owner OID、实际认证role名/OID、ordinary|sandbox、RR access mode、固定purpose(plan|init)、本Snapshot总deadline、完整manifest/descriptor/expected/schema/roles摘要及原v1 predecessor摘要（ordinary为空）。摘要只来自opaque proof或本包实际验证，不提供从caller digest升级信任的构造器。

每个维护scanner进入前及返回可用结果前调用 `Permit.VerifyTx(ctx,tx)`：先检查context、共享状态与精确wrapper/conn，再在同tx重读isolation/read_only、current/session LOGIN/OID、PID/backend_start、pg_current_xact_id()::text、DB/schema/owner、实际双session锁，并调用opaque proof.VerifyTx检查仍是原安装证明。top xid用于同一个顶层事务，不用xmin、SAVEPOINT子xid或tx开始时间；READ ONLY在主库也取规范top xid，standby/recovery环境不支持本维护链。任何不符返回零结果和安全错误，不能换tx重新绑定旧Permit。

InstallationProof在发行时已证明16个C0表全空、epoch不存在；其VerifyTx不在Init写后反复要求全空。Attest时额外在本RR重核一次该空集及mode，且Gate没有第二次Attest入口。Permit保持“原空安装上的这一维护事务”身份，所以Init同tx写自己的C0 facts后仍可调用只读post-init scanner。它不把现在epoch存在当成允许下一次init；Commit/Rollback后旧Permit永久失效，新命令对非空C0只能走原epoch只读观察，不能取得本首发Permit。

不允许pgx nested Tx.Begin、原始SAVEPOINT/ROLLBACK TO/RELEASE、SET ROLE、SET SESSION AUTHORIZATION、修改search_path/timeout或把tx交任意回调。wrapper/conn/PID/top xid不能发现同wrapper手工SAVEPOINT，本合同明确依封闭维护调用图禁止，不能声称catalog证明“从未SAVEPOINT”。每个scanner只用静态只读SQL；唯一Init写入协调器不得暴露上述任意SQL接口。关闭事务或换wrapper后即使PID/连接仍相同也拒绝；重新Begin有新wrapper/top xid，不能借旧Permit。

## 6. 安装证明、v1前驱与完整runtime proof的分层

| 证据 | 固定职责 | 明确不能证明 |
| --- | --- | --- |
| migrations.VerifySnapshotTx | caller RR完整当前embed history | 对象正文、权限、维护身份、来源或epoch |
| fundinglotcatalog.CatalogProof | 静态全对象/角色/ACL及其同tx绑定 | 空安装、维护LOGIN、业务完整性 |
| fundinglotcatalog.InstallationProof | 上项加发行时16表全空/无epoch | 首发Permit、PB/来源/basis、完整C0 runtime |
| maintenancetrust.Permit | 实际维护LOGIN、双gate、同顶层RR、原安装/v1绑定 | Balanced、覆盖完整、可开账、资金或网络授权 |
| 未来上层RuntimeProof | 安装catalog + 原v1 + 真实epoch/attestation + 全源/lot独立核验 | 新PSP认证、原路退款、拒付或性能 |

B3的前epoch前驱校验在本包私有静态只读流程完成，不import payments以免billing→maintenancetrust→payments→billing循环。它按[既有v1封闭字段与canonical](../../internal/payments/runtime_types.go)、[严格约束](../../internal/payments/runtime_load.go)、[持久attestation](../../internal/payments/runtime_catalog.go)逐字段实现只读核验：整行唯一、固定v1/protocol=1、SHA与完整canonical重编码相等；原environment/merchant/payee、DB/三schema/八role、nonce/API摘要、原origins/callback、按ID有序全部key digest及有效区间精确绑定。不能将未知字段丢弃后hash、用当前catalog自动生成expected或按较新C0版本改写v1原文。

原App marker/immutable v1 row、Mock/Receiver的静态marker和signing-key/migration关系，在本RR按冻结列列表完整读取并交叉匹配v1 pins；原M/Q对象正文与角色由InstallationProof核静态受审descriptor，原app 001–022采用静态基线+明确C0 delta，不能用旧v1拒绝新增对象的失败结果当作“关闭所以跳过”。维护只读持久摘要，不读取/输出PSP事件正文或key原始材料，不冒称已验证本机运行文件中的nonce/key实际原件；未来listener的真实私有文件重验仍是runtime装配门禁。

这份私有v1 reader是必须实施和独立验收的依赖，**当前没有现成可直接复用的caller-owned维护入口**。实施不得调用现有会自己Begin/Commit的runtime constructor再声称同snapshot；固定golden须与现有v1 canonical/字段语义逐项交叉回归。若根选择抽取共享纯v1 codec，须另审等价重构后再替换此私有reader，不在本候选悄改v1行为。

Permit不消费尚不存在的完整C0 epoch proof；Init先持Permit完成原ledger/funding/采购/PB/B来源的同snapshot独立核验、CoverageComplete及basis，再创建新事实/v2 attestation/最后epoch。随后只在该维护RR RW与原Permit下执行maintenance post-init独立全图核验，并比新canonical与持久v1/v2/epoch完整关系；结果类型与RuntimeProof严格不同。不能在维护LOGIN/RR RW事务中调用仅actual APP/RR RO可发行的RuntimeProof issuer，也不能为了拿该token在init COMMIT前另开APP snapshot看尚未提交的epoch。

Commit确认后，startup才由actual APP在新的RR READ ONLY按schema-check §7.1重新完成全部独立scanner并发行RuntimeProof。Post-init的leaf CatalogProof仍只证明catalog，maintenance post-init结果也不能转型为RuntimeProof。维护post-init组合器、封闭写入器与canonical持久绑定的精确Go API属于下一依赖合同，未冻结时可先实施Permit/只读plan骨架，不能因此开放init生产写入或serve。

## 7. Plan、Init与提交未知的固定编排

Plan：Acquire双gate → BeginPlan RR RO → InstallationProof → AttestTx → 同tx完整history/catalog/v1/PB/ledger/funding/采购/B/pre-epoch sources → 规范basis → Permit最后VerifyTx → Snapshot.Commit确认 → 独立cleanup/release/Close → 输出安全plan。任一来源mismatch或coverage缺口不能输出可供init接受的ready plan。过程无业务写；COMMIT/清理未确认不返回成功plan。

Init：Acquire双gate → BeginInit RR RW及125表/全accounts锁 → 同tx InstallationProof/AttestTx → 全部原源独立核验及basis重算 → 与输入plan精确pins核对 → 封闭写入器创建memberships/seq0/lot/events/cache → immutable v2 attestation → 最后epoch → 同tx maintenance post-init独立全图/canonical持久关系核验及Permit最后VerifyTx → Snapshot.Commit确认 → cleanup/release/Close → 输出原epoch安全回执。该链不要求/不发行APP RuntimeProof。写入器不调用在线wallet helper，不改原109表的业务数据/余额/PB/Key，不重新发行旧approved退款。

跨命令输入plan只是严格解析的**待比较数据**，无权限作用；至少精确匹配原epochUUID、DB/schema OID、mode/v1 predecessor、manifest/descriptor/expected/schema/roles、完整basis、来源phase与安全计数。Init自己重算全部值；CLI不能接受“skip-basis/force/changed-only”，不能用SUM、最近时间或一个balanced bool代替完整basis和CoverageComplete。旧plan因任何业务/catalog/OID变化不符即整tx回滚，需重新plan。

Snapshot.Commit先进入closing并撤销Permit，再发送一次COMMIT；收到明确成功才确认。PostgreSQL明确回滚与成功COMMIT回复丢失分别保留，不把任意driver error当“肯定没写”。unknown时返回固定unknown和零结果，不自动retry init、不换epochID、不补半图。原连接物理关闭且不复用；独立至多5秒cleanup不能假称已确认服务器提交/回滚。

物理关闭可能使原session锁释放，**不能声称COMMIT未知后的跨连接仍连续持有原双锁**。独立观察器必须新建同真实身份连接，按402→401重新取得双gate后，才只读核原epochID/proof和完整图；它不取得首发Permit、不写。原backend尚存/原锁未释、其他服务抢占gate或观察超时则保持unknown，不解锁别人的session、不凭GET404判断未提交。记录原PID+backend_start来区分原backend存活与PID复用；不能仅“本地conn.Close已返回”便断言远端事务已消失。原epoch完整存在且精确匹配才确认完成；不存在或图缺失继续unknown/完整性处理，不允许在此命令重跑init。服务重启必须等维护协调器有明确结论，不能利用锁释放空档自动打开listener。

## 8. 安全错误、实施文件与验收门槛

固定安全错误候选为 `maintenance_input / maintenance_gate_busy / maintenance_identity / maintenance_schema / maintenance_transaction / maintenance_timeout / maintenance_execution / maintenance_commit_unknown / maintenance_cleanup`。所有失败返回nil Permit/零结果；无driver unwrap、SQL/DSN、schema/role名、行值、proof原文或秘密文件路径。安全计数与摘要仅在确认提交/清理后按既有维护DTO输出，根协调器负责唯一最终回执。

冻结后建议按以下顺序实施，文件名为候选，不据此分配生产迁移号：

| 片 | 文件/接口 | 必须完成的验证 |
| --- | --- | --- |
| T0 gate | internal/maintenancetrust/gate.go、gate_test.go、gate_integration_test.go | 新专用conn、实际402→401、反序拒绝、false等待/取消/失回复、逆序有界释放/物理关闭；无遗留session/advisory锁 |
| T1 snapshot | snapshot.go、snapshot_integration_test.go | 仅顶层RR两mode；Init第一SELECT前125表C序SRE及全accounts锁；物理writer屏障证明先提交变化被Init看到；Plan RO无写型锁；不另Begin |
| T2 authority | permit.go、predecessor.go、permit_integration_test.go | ordinary实际owner与B3字面专库DB-owner唯一例外；完整v1/opaque安装证据；错误LOGIN/SET ROLE/endpoint/OID/成员/ACL/正文/nonce绑定拒绝 |
| T3同RR scanners | PB独立VerifyMaintenanceTx；billing独立pre-epoch/lot、ledger/funding/采购/B caller-owned接缝 | 全部入口VerifyTx，原PB公共Verifier仍拒B3 installer；原tx私有sentinel不可见、快照旧/新分歧符合MVCC；任何缺口/失败零结果，不跨库/另pool |
| T4 plan/init协调 | 另根冻固定CLI、写入器、maintenance post-init与startup RuntimeProof分型接口 | 逐行basis跨命令重算、非空原历史、空/零tenant、完整旧membership、维护post-init全图；确定rollback/真实成功COMMIT丢回复及只读原epoch确认，无重复开账；startup另用实际APP新RRRO |

T0–T2的负例必须包括：全SELECT的APP/worker/projector、Config与current/session不等、NOLOGIN owner被SET ROLE、moni_test非superuser/非DB owner/别的DB owner、localhost/Unix/IPv6/坏fallback/错误实际server、role同名换OID、任一运行role membership/SET/admin/grant option、schema/func/trigger/search_path/ACL漂移、CatalogProof冒充InstallationProof、proof/Permit换wrapper/conn/PID/top xid/schema、关闭/失败/Commit后复用、复制token二次attest、额外/丢失/两int advisory锁、SAVEPOINT/nested调用图反例。所有测试只用专用测试DB隔离schema/角色，真实受限LOGIN分别验证，不用SET ROLE充当真实runtime。

本稿新增测试**均尚未运行**。静态checker、private v1 reader、Permit、全部同RR来源scanner、basis与封闭init写入器、上层RuntimeProof及未知提交观察器是分别可审的依赖；其中任何一项尚缺就保留对应阻断，不以mock marker、临时trusted参数、部分PG通过或本合同替代。根冻结与统一架构状态更新后才逐片实施；本文件不触发refresh或main运行。

## 9. 2026-11-02独立审阅后的冻结前修订项

静态checker已按其§10单独实现；本维护合同仍是候选，以下四项须在T1/T2编码前冻结，不凭旧段落中的概括自行补权限：

1. Init的第一条业务行锁不能先于目标对象认证。预定顺序须修订为：Begin/固定SET → 首次SELECT前全部125表SRE → 同RR完整catalog/history与Attest → 受Permit控制的固定init stage按全account ID C序锁行 → 全部basis/source scanner。这样既保证锁等待后的首次RR观察，又在执行ledger_accounts行锁前证明heap/id/RLS/owner。原§3/§7的“随后锁accounts”需随精确stage API一起更新，不能照旧顺序直接实现。
2. 两次VerifyTx只证明入口/出口，不能自动覆盖scanner执行期间的busy所有权或向其传递私有总deadline。须冻结覆盖scanner全程的受控stage/lease签名、唯一tx和受限context、正常Finish与失败撤销规则；Commit/Close不能与scanner争用pgx连接。不得只让scanner使用自己的context并声称遵守Snapshot总预算。
3. 完整manifest必须来自本进程固定migrations及同RR实际history核验；CatalogProof只含静态expected/schema/roles，不含迁移manifest。须明确允许叶子migrations依赖，并由Attest实际核验/绑定固定manifest，或先设计同等opaque来源。当前Attest参数不能接受caller digest/bool来补出这一证据，不能把仅返回error的旧helper结果变成任意manifest claim。
4. 私有v1前驱reader须先冻结字段矩阵。App v1 proof不含M/Q的nonce_digest、state_directory_id、control_token_digest、active_key_id或own_schema_sql；只有原已存在共享字段可与v1逐字相等。peer迁移应对编译期固定SQL/version/SHA，peer自有字段只能按明示格式和内部持久关系核验，不能宣称认证未读取的私有文件原件。ordinary须完整保留原runtimeVerifyOrdinaryOff的21表空集、query-conflicts关系、disabled revision1及commerce/funding/ledger off规则，单凭“无marker”不足以证明关闭。

上述修订不阻塞独立冻结T0专用连接/双session gate及清理机制；T0成功仍无Permit、Snapshot或资金权。T1/T2和完整scanner接缝冻结之前不开放首发init。相关测试尚未实施或运行。

## 10. 根冻结T0：专用连接、双session gate与清理

2026-11-02。本节只冻结T0，开始`internal/maintenancetrust`的Gate实现；§1中的Snapshot/Permit API、T1–T4及§9修订仍是候选，不提前提供stub、假proof或init入口。生产包本片仅允许标准库、pgx与fundinglotcatalog（只使用其Pins类型），没有main/CLI/HTTP调用者。

```go
type Gate struct { /* private shared state */ }
func Acquire(ctx context.Context, cfg *pgx.ConnConfig,
    pins fundinglotcatalog.Pins) (*Gate, error)
func (g *Gate) Close(ctx context.Context) error
```

零Gate与nil/无效Acquire输入返回固定安全错误；Gate值复制共享私有状态。String、GoString、JSON均为固定redacted形状。cfg必须由pgx.ParseConfig创建，随后可设置标准连接选项；本API沿pgx的该Go级构造约定，不支持伪造其内部标记的手写ConnConfig。Acquire先Copy配置（含RuntimeParams、fallback/TLS克隆）和深拷贝pins，专用新物理连接不从pool借用或返还；不更改调用方cfg或pins，不以复制后的pins发行任何安装/维护权。Pins须满足静态catalog冻结的闭合结构（mode、ordinary/sandbox字段存在性、角色与schema名称/OID的distinct、public唯一builtin namespace owner例外），本片不得从live对象补全缺失pins。

Acquire从入口至返回共用父期限与30秒的较早值，50ms轮询。nil或已取消context输入失败；连接与每条SQL共用该剩余期限，不为下一把锁重置。连接必须TCP且idle。非权威preflight只读取pg_catalog：实际Config.User=current_user=session_user及真实LOGIN，配置/所有fallback的host/port/database和实际remote/SQL server endpoint相符；DB/schema名及OID、raw namespace owner与pins一致，actual current_schema恰为目标且不是系统namespace。literal IP须等于实际remote IP；普通DNS名允许解析，但remote与SQL server必须一致。sandbox还保持§4的字面127.0.0.1:55439/moni_test配置与实际IPv4 endpoint限制。配置或普通schema自身的owner属性、v1/业务状态、来源完整性均未由T0认证；不因为Acquire成功跳过未来Attest。

preflight必须确认本PID在本DB没有任何advisory锁。固定静态SQL依次try取得402、401（完整bigint常量为782193402、782193401），明确false才等待50ms重试，明确true各只取得一次。每次true后以本PID/DB OID、ExclusiveLock、classid0、objsubid1、granted完整核对当前已取得集合且无其他advisory锁。不能用xact/shared/two-int形式，不用unlock_all、不重入已取得key。T0不发BEGIN/SET/业务SQL，不安装或改ACL，任何失败都不返回半Gate。

Close无论传入context已取消、nil或无期限，都使用独立background最多5秒总cleanup；对idle且已知取得的锁，401→402逐把unlock，必须明确true，最后核本PID无任何advisory锁，再关闭专用连接。Close幂等，复制Gate不会重复解锁；同一共享状态以私有busy保护，重入/并发安全拒绝，不并发争用pgx。Close后任何副本永久失效，不能再Acquire/Reset复用原状态。

锁取得/解锁的SQL错误、context到期或回复丢失均按结果未知处理：不重发该key、不发补偿解锁SQL，不返回池，立即物理销毁该专用连接。已持402而401明确false直到预算结束时也须物理关闭，不留下部分锁。若Close见非idle/异常状态，同样销毁连接而不擅自控制未知事务。物理关闭不等于已证明远端backend立即消失；专项须独立有界观察原PID/backend_start结束与锁释放，不凭本地Close返回声称释放已获确认。

本片固定错误只从§8的maintenance_input、maintenance_gate_busy、maintenance_identity、maintenance_timeout、maintenance_execution、maintenance_cleanup中选择，不unwrap driver/SQL/endpoint/role/schema/DSN或行值。T0没有COMMIT动作，不返回maintenance_commit_unknown。普通Close成功只证明本专用连接的已知锁清理完成，不授予业务许可。

T0边界补充：sandbox的配置/实际LOGIN用户名也必须为字面moni_test，但其super/datdba维护授权仍留T2重新认证；public的datdba=ObjectOwner pin只是有效namespace目标绑定，不推断当前LOGIN就是维护owner。Close的后续副本调用返回首次cleanup结果（成功nil、失败maintenance_cleanup），busy调用直接maintenance_gate_busy。Copy之前先检查nil cfg/fallback等结构，不能让pgx.Copy的nil fallback panic泄漏。pgx在取消/收包错误时可能先标closed、异步稍后才关socket；unknown/失败路径必须对事先保存的底层net.Conn立即Close，再做有界pgx清理，不能只凭pgx.Close立即返回推断物理连接已销毁或锁已释放。

必须验证：真实新连接和402→401顺序/每把一次、第二把被占时第一把可见但不返回半Gate、false后正常等待取得、取消与父/30秒预算、额外/丢失/shared/two-int锁异常、错误实际endpoint/当前schema/登录映射、部分取得及401已在PG取得但回复丢失的物理关闭、逆序解锁及明确true、取消Close仍清理、复制/零值/并发/幂等与redacted编码。数据库测试使用独立`C0_MAINTENANCE_DATABASE_URL`且只接受本机专库；只清理自己精确创建的随机角色/schema/backend，不能终止其他持锁者、修改共享public或升级58123。真实角色/新conn/pg_locks与PID/start屏障必须独立观察，测试hook只能在test文件的网络wrapper或固定屏障中存在，不向生产提供skip/已持锁声明。T0的完整实现与验证另立记录、更新统一架构后由根汇合检查，不沿用静态checker的通过记录。

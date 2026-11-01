# 022-B3：真实APP支付QA离线环境与进程验收合同

2026-11-01，根已授权本片先设计、再实现离线builder与脚本；真实main串行进程验收须等根确认可运行及共享测试库InstanceLock窗口。本片独占`internal/paymentsandboxqa/application*.go`与`scripts/payment-application-qa/`；不修改main/server/web、原019 QA Create或P/M/Q协议，不刷新统一架构快照。

## 1. 环境来源与发布边界

`CreateApplication(ctx,adminDSN,privateRoot) (*ApplicationEnvironment,error)`先调用既有Create：专用literal `127.0.0.1:55439/moni_test`、实际moni_test超级用户、三个随机schema、三NOLOGIN owner、APP/projector/M/Q真实LOGIN、019完整历史以及真实APP双人核款1000的非空A基线。之后离线升级APP至完整当前迁移（含022），保留M/Q各自version1的schema/manifest/marker和已冻结callback URL。

APP使用原ReceiverOrigin对应端口，callback从环境第一次创建起就是`<ReceiverOrigin>/api/payments/sandbox/notifications`。保留Q schema及其manifest供原`paymentruntime.Open(Receiver)`身份自证，**APP进程验收不启动Q listener**；不能由Q代收后向APP补造证据。mock原API/control两个端口不变。loopback临时端口由离线工具选择，端口竞争必须安全失败，不暗改既有URL/证明。

此包与脚本仅离线QA可用，普通serve不导入它；不提供在线安装、role grant、生成nonce、repair或跳过证明的HTTP入口。QA包禁止import payments，避免payments同包测试import QA后形成循环；proof/manifest使用独立、逐字段一致的私有wire structs，不能共享生产内部可信对象。

## 2. 角色、升级与原资金保护

1. 原owner维护池以moni_test连接后显式SET ROLE到本次NOLOGIN schema owner，仅用于隔离环境安装/清理。所有业务与验收分别用actual APP/worker/projector LOGIN，不用SET ROLE冒充。
2. 追加第八角色`pm_app_worker_<同后缀>`，LOGIN/NOINHERIT/无超级用户、建库、建角色、复制或BYPASSRLS。随机密码只保私有状态；立即登记至原Environment.createdRoles/密码/OID映射，所有后续失败都由统一Close清理。
3. 升级后撤APP schema全部函数PUBLIC EXECUTE，恢复旧QA已有APP/projector legacy invoker执行权，再执行`operations.ProvisionFor`收紧当前全部支付矩阵。以owner插入marker/attestation后调用`ProvisionPaymentWorkerFor`；worker仅额外显式授予`funding_uuid(text)`，不授pb或其他legacy函数。三schema对PUBLIC和peer的权限仍按M/Q严格证明。
4. marker+attestation在同一事务、全部typed OID/role/proof一致后插入；不更新已有marker，不伪装已有B历史为空。proof_version/protocol=1、完整固定keyset、不增量改key；app/worker独立32byte nonce，不复用M/Q nonce、token或key。
5. 升级/证明/QA账号seed不得改变既有A money/journal/receipt/credited。返回前执行原VerifyFundsUnchanged；另保留只覆盖原非空A租户、原manual clearing与其不可变源图的legacy fingerprint，用于APP后续在另一个QA租户发生sandbox资金后的精确A保护。报告投影推进不当作资金修改。

## 3. 独立wire proof与双manifest

私有proof字段顺序严格沿runtime§3.1/§10.2：`proof_version,protocol_version,environment_id,merchant_id,payee_label,database,app,mock,receiver,worker,projector,app_nonce_digest,worker_nonce_digest,api_token_digest,api_origin,control_origin,app_listen_origin,callback_url,keys`；角色/Schema/Key子字段亦逐字段沿合同。固定struct JSON，EscapeHTML=false，仅去编码器自身一个末尾LF；proof bytes与SHA256写入022表，不能map编码充当canonical。

独立APP与worker私密目录0700、文件0600、O_EXCL创建，目录全部位于本次Environment私密根下。APP manifest仅application component、proof/hash、自己的nonce_file与完整key材料；不含DSN/API/control token。worker manifest另含自己的runtime_dsn_file和API token文件引用；不含APP/projector/M/Q DSN或control token。每个secret_file引用给name+exact bytes SHA256；无padding canonical base64url32byte，key窗口沿原M/Q。两manifest中的公共proof与完整keys完全相同，组件nonce不同。

`ApplicationEnvironment`封装并嵌入原Environment，公开QA所需ApplicationManifestPath/WorkerManifestPath、actor/tenant安全ID、必要DSN和随机QA登录凭据的方法；String/GoString/JSON永远redacted。DSN/密码只能交给受审QA装配/子进程，不输出至stdout/stderr、URL、结果摘要或浏览器持久存储。新增LOGIN、目录/文件精确登记清理，失败不得遗留角色或只清一半秘密。

## 4. QA用户与验收API

为独立APP商业链路创建一个新的QA租户，原1000 A资金租户不参与支付订单。以真实APP写入随机admin、owner、billing_viewer/member、finance_operator/finance_approver用户；owner/viewer/member仅加入新QA租户。密码经既有security.HashPassword且随机，仅QA访问器返回。MFA沿默认未绑定的可选策略，不安装绕过验证码的生产入口。builder不直接创建支付SKU/订单/capture；它们必须由Service或真实HTTP+worker完成。

builder专项须证明：完整当前迁移、八角色actual身份、M/Q原Open仍通过、双manifest通过生产Load/ValidateOptionsPair、Application/ProjectionPoolConfig及真实NewService/OpenProcessor、peer/worker/projector负权限、旧A指纹未变、Close幂等及失败清理。生产构造测试放外部`paymentsandboxqa_test`包，QA生产代码不import payments。

## 5. 脚本与真实进程窗口

`go run ./scripts/payment-application-qa --require-database --prepare-only`只执行builder/构造/清理及安全摘要，不声称真实APP链路。完整进程模式单独显式开启；构建普通`moni-aigate`和`moni-payment-sandbox`，以严格白名单环境启动mock及APP，不启动receiver。只将本组件必要DSN/manifest/listen/master key等交子进程，不继承TEST_DATABASE_URL/PGSERVICE/PGOPTIONS/proxy/QA control token给APP。

共享测试库的普通main InstanceLock必须先与根排定窗口；脚本不夺锁、不杀预览、不改其他环境。就绪使用实际health与退出状态，不能只认端口。第二个相同APP进程必须在listener/worker前被单实例保护拒绝；退出后确保没有额外capture/receipt。mock控制端仅脚本持control token，APP只拿API token。HTTP通过真实cookie/CSRF登录、平台发布/激活SKU与policy、租户建单、mock捕获、P真通知/query→APP inbox/evidence→restricted worker唯一资金图→安全钱包/订单/资金读取。

脚本覆盖重复通知/重复查询/掉线或重启后的同ID恢复、未知结果不重新发行、普通off历史收尾和SIGTERM排空；按根提供的已接main/API精确合同串行实施。任何尚缺真实故障设施的COMMIT回包丢失明确SKIP，不用普通rollback代替。只有进程退出回收、观察连接关闭、原A fingerprint仍一致后才删除本次schema/八角色/私密根。

stdout仅固定PASS/SKIP阶段、编译产物SHA256、环境UUID和安全计数；stderr仅固定failure码。禁止打印driver/HTTP正文、DSN/密码/manifest/proof hash/签名/key/token/nonce。子进程日志先封在本次私密目录，默认不回显；失败也不泄露原文。prepare-only与完整process结果分别记录，不把builder通过当APP上线或真实PSP验收。

## 6. 本轮实施与真实专项记录

已实现`internal/paymentsandboxqa/application*.go`离线builder、独立typed wire、双私密manifest、六类随机bcrypt账号、原A范围资金指纹与第八worker统一清理；QA生产代码未导入payments。专项位于外部`paymentsandboxqa_test`包，直接调用生产loader/构造。

2026-11-01专用PG命令`TEST_DATABASE_URL=专用库 go test -race -p 2 ./internal/paymentsandboxqa -run '^TestApplication' -count=1 -v`通过（25.889s）。覆盖双manifest、当前迁移、actual APP/worker/projector身份与支付ACL、生产NewService/OpenProcessor、M/Q原Open/Check、worker只补funding_uuid且pb_now/配置读取被拒绝、projector私密证明被拒绝、随机账号hash与MFA默认未绑定、未直接制造SKU/order/capture、独立租户audit不污染原A见证、关闭后credential/DSN能力失效以及八角色/三schema/私密目录完整幂等清理。

`go test -race -p 2 ./scripts/payment-application-qa`及两目录`go vet -p 2`通过。真实专用PG执行`go run ./scripts/payment-application-qa --require-database --prepare-only`通过builder、生产loader/构造、peer自证、原A及清理全部阶段；prepare结果显式SKIP普通main进程验收与缺设施的COMMIT回包丢失。此阶段尚未接进程链；随后已在§7授权窗口内另行实现`--process`，两种模式的证据不能混用。

## 7. 已授权的普通main进程窗口与精确场景

2026-11-01根已分配本片独占专库main InstanceLock窗口；其他片暂停全仓main专项。脚本新增显式`--process`，与`--prepare-only`互斥；仅process可附`--browser-hold`。运行时重构建当前源码普通main与mock二进制并输出产物SHA256，所有子进程日志保存在本次0700目录的0600文件，任何失败只输出固定stage码。APP仅接收白名单基础环境、自己的APP/projector实际LOGIN DSN、两manifest路径、固定listen/public origin、内存生成的master/auth秘密以及明确mode/recovery/MFA/cookie部署选项；不继承QA管理员、PGSERVICE/PGOPTIONS或代理环境。mock仅拿自己的manifest。服务启动以普通main真实`/health/ready`及mock两个listener的实际HTTP鉴权拒绝确认，观察子进程存活，不能仅TCP端口探测。

所有SKU/策略/订单均通过真实cookie+CSRF HTTP：admin发布并激活SKU及policy，owner新建订单；原operation重放必须读回同receipt/order，不能新造outbox。P控制端的capture生成真实canonical event并实际向APP投递；APP inbox/evidence与受限worker建立唯一capture→funding receipt→journal(两line)→credited outbox/audit。直接重新投递同event的新签名只验证稳定ACK和唯一资金，不构造新的业务事实。重放实际APP child的PSP query只读取冻结快照，不用QA造查询替代worker。

为确定性安装绑定command的真实HTTP回包丢失计划，先停mock，HTTP建单并观察APP真实network→submission_unknown；确认APP退出后启动mock并对原external_command_id离线控制arm，之后才重启APP。Recovery阶段只允许已知历史query和query lease清理：真实query可把not_seen证据收下，但不能发送mutation或处理已收到capture evidence；真实HTTP新建/策略写应拒绝。重启sandbox后允许同原command重试，P真正提交后drop HTTP reply，APP必须回到unknown并通过独立签名query闭环；故障plan已消费、PSP只有一条同command/订单/transition、APP无额外资金均需DB实证。该场景为HTTP回包丢失，不宣称DB COMMIT回包丢失。

带双证明的off保留历史通知/query/evidence收尾，已有ready订单在APP停机期间由P capture，off启动后须完成唯一资金；新建订单返回runtime disabled。随后停止APP，以同schema的普通off（无manifest，DSN明确search_path本次QA schema）验证有marker/history时拒绝启动。第二实例必须在私密日志中匹配固定`another active instance holds the service lock`，避免把端口占用当作锁验证。SIGTERM须正常退出并排空，SIGKILL仅用于确认提交后的事实持久化，不模拟COMMIT结果不明；重启后原receipt/journal不变且无重复capture。删除前观察本环境LOGIN的进程连接全部消失、原A指纹一致，最后精确删三schema/八角色/文件。

API验收结束后，可受控持有最后sandbox进程供根做独立桌面/390px浏览器验收。`--browser-hold`只输出安全origin、0600 `browser-state.json`位置和stop文件位置；state仅含随机QA登录凭据、角色、当前SKU/version与本轮安全订单ID，不含数据库DSN、manifest、nonce、签名或token。根通过本地私密文件读取，不把凭据传入命令参数或日志；写stop文件或发送终止信号即结束hold并join所有子进程、关闭观察池、验证原A、清理本环境。不读取或更改用户58123预览。

## 8. 普通main真实进程证据（2026-11-01）

首轮根冻结main/server和web dist后，脚本按当时源码重构建二进制。普通main SHA256为`d857bc8567d67c543df8d87a8405acabc9f89383384a6e59841aa46a25c03f90`，mock为`37ca0557811327e1b012cfd887b18e0b63489420101e873845ba67d9f24f9e32`。专用PG执行`--require-database --process --browser-hold`，以下真实阶段均PASS：main/mock实际HTTP就绪、第二main固定单实例锁原因拒绝、随机admin/owner真实cookie+CSRF/MFA未绑定登录、HTTP发布激活SKU与policy、真实P通知→APP→worker唯一资金/重复ACK/原operation重放、Recovery真实query且连续3s无mutation/入账、sandbox恢复后P提交丢HTTP回包经原command独立签名query闭环、实际worker child的PSP冻结query重放、带证明off历史capture入账与新建拒绝、SIGTERM排空与SIGKILL确认提交后四笔资金身份不变、普通off有历史无证明拒启。

四笔真实sandbox资金均为250，HTTP钱包可用及累计充值1000；每笔capture/funding receipt/journal/credited outbox/audit各唯一，journal两line。仅对明确pre-admission 429按Retry-After=1原样重试，没有更换operation或弱化限流。首轮恰因连续第三次平台写收到429后，脚本完整验证原A并清八角色，随后修正脚本遵循现有限流；没有生产逻辑改动。

首轮根已用实际UI完成发布/激活/建单/取消与390px无横向溢出，发现共享前端登录redirect和sandbox流水文案问题并修正。首轮stop后脚本exit0，最终原A、运行时连接退出、三schema/八角色清理均PASS；外部还确认包含browser-state的整个私密根目录已删除。

根再次完成286项Node测试、strict TS与Vite build并冻结新dist，脚本第二轮重新构建普通main SHA256为`c8ee58158d2f5818a2ab26de4cfc645dce32458820387c5965d679dbf54fe236`，mock保持上述hash；同一完整API/worker进程链再次全部PASS。最终根浏览器联验亦全部PASS，见[真实浏览器记录](../../docs/verification/payment-application-022-browser.json)、[原生IndexedDB与刷新恢复记录](../../docs/verification/payment-application-022-indexeddb.json)及[平台截图](../../docs/verification/assets/payment-application-022-platform.png)、[租户截图](../../docs/verification/assets/payment-application-022-owner.png)、[390px截图](../../docs/verification/assets/payment-application-022-mobile.png)：实际密码登录无需MFA；每次`/me`延迟500ms仍保留redirect并到原目标；admin UI发布/激活、owner建单/取消/原回执、钱包三来源文案、finance/viewer只读、390px与1440px无横向溢出。根仅注入随机未提交operation journal，GET404与两次真实reload均保留原编号，并精确删除该QA记录；不把它宣称为真实HTTP回包丢失。

第二轮根写stop后脚本exit0，全部子进程join、所有自有运行时连接退出、原A指纹及三schema/八角色精确清理全部PASS，外部确认整个私密根目录（含browser-state/日志/binary）已删除。已向根正式释放main InstanceLock窗口用于全仓race。本轮没有改动用户58123预览。

第二轮最终安全进程输出、binary hash、实际PASS/SKIP、关联浏览器记录及完整清理结论统一留存于[结构化进程验收证据](../../docs/verification/payment-application-022-process.json)，不保留凭据、签名、nonce、manifest或私密路径。实际Guard失权、长期在途SIGTERM明确列为未运行，不用第二实例锁拒绝或普通优雅退出代替。

数据库COMMIT回包丢失继续明确SKIP。SIGTERM过程的实证为正常退出、连接归零和重启资金身份不变，不扩写成在途长网络调用期间的SIGTERM测试。真实PSP与live收款仍不在本地sandbox验收范围。

## 9. 在途SIGTERM与真实Guard失权补充合同（已冻结、真实验收通过）

根先授权设计并复核了`exercise_lifecycle.go`和维护控制连接方案，全仓race结束后再分配独占main窗口实际执行，结果见9.4。方案只使用已有隔离mock的真实PostgreSQL锁及本次APP的真实InstanceLock连接，不新增普通serve测试hook、不扩展P/M/Q协议、不用持久`hold_checkout_before_effect`命令状态假装网络阻塞；没有DB COMMIT回包丢失能力，仍单独SKIP。

### 9.1 可观察且隔离的物理在途屏障

脚本先分配新的owner操作UUID（同时为本次order ID），由此得到完整`mps1.<environment>.<operation>`。通过本环境独立QA维护连接获取mock现有merchant gate的**session** advisory lock：namespace为`782193414`，key为`int32(bigEndian(SHA256(NUL.join(["moni-payment-lock-v1","mock",environment,"merchant",merchantOrder]))[:4]))`，其中NUL是一个零字节、字符串按UTF-8编码，与`internal/mockpsp/repo_locks.go`逐字节一致。session gate与mock业务事务的同元组xact gate相互排斥；hash包含environment与完整merchant order，不影响其他QA环境或用户预览。

随后只通过真实owner HTTP创建该operation；脚本不向P补发checkout。须同时观察以下真实条件后才能触发故障：

1. APP的checkout outbox为本order、原external command、`leased`且原canonical bytes/digest/lease/generation均已提交。
2. `pg_locks`有**本次mock actual LOGIN**的未授予merchant gate，pair-lock `objsubid=2`、database OID与本次一致、classid为782193414、objid等于signed key的uint32位模式；`pg_blocking_pids(waiter)`包含本次QA维护连接PID。
3. 同一个waiter PID持有782193410的原command gate（hash类别为`command`、target为原external command），证明这是APP worker已经送达P的实际checkout请求，而非无关查询或脚本直调。
4. P还没有该command/order/transition/capture，APP没有该order的funding receipt。记录屏障到故障触发的单调耗时，必须在mock既有5秒repository期限内立即触发，超时即失败重新建环境，不能按普通网络timeout记通过。

QA维护连接仅持锁，不改任何业务行或租约时间；退出、取消或失败必须验证unlock结果并关闭该独占物理连接，不能把仍持session lock的连接放回池。

### 9.2 在途SIGTERM

在9.1屏障成立后立即给本次APP PID发送SIGTERM，merchant gate继续由QA持有。APP须在有界等待内正常退出，原PID已join；脚本关闭自己的APP观察池后，以独占维护连接确认APP、worker与projector这三个本次实际LOGIN的连接计数全部归零（恢复前再重开真实APP观察池）；P该waiter不再持原command gate或等待merchant gate，P业务图仍全零。此时才能释放QA gate，避免通过手动放行P来制造优雅退出。

由于真实取消可能让本地outbox保留原`leased`，验收必须等待**原60秒DB lease自然到期**，不UPDATE租约/DB时钟、不调用私有Processor。重启同manifest/普通main后，由生产Expiry→unknown→原命令签名query的not_seen→同external command重试→ready闭环。上界预留100秒，最终canonical bytes/digest/external command不变、P只有一条command/order/ready transition、没有capture/receipt，原四笔既有资金ID与原A见证不变。

### 9.3 精确Guard失权

用另一个新operation重复9.1物理在途屏障。管理员控制连接只查询本专库中`locktype='advisory' AND classid=0 AND objid=782193402 AND objsubid=1 AND granted`，再联`pg_stat_activity`要求**usename精确等本次随机APP actual LOGIN**、database OID正确；结果必须恰好一个PID，且不同于脚本维护PID和mock waiter PID。零个/多个或身份不符立即拒绝，不退化成按端口/全库/角色模糊杀连接。

该信号操作需要专库管理员而不是业务角色。建议复用本次QA owner池的独占维护连接（原session_user即moni_test），仅在这条QA控制连接`RESET ROLE`恢复实际moni_test管理员，复核current_user=session_user=moni_test、rolsuper=true与literal专库后，再执行带PID+角色+锁tuple二次WHERE核验的`pg_terminate_backend`。不授予APP/worker任何signal权限，不把管理员DSN交给普通main，也不用SET ROLE冒充业务LOGIN。RESET ROLE后的控制连接必须独占、最终Hijack并物理Close，绝不以管理员状态放回owner池；如不能确认连接关闭则该场景失败。根可在复核时选择改为显式传给脚本的管理员控制池，但两者都只能杀上述唯一锁连接。

精确终止返回true后，以真实`/health/ready`调用推动fresh Guard：不允许成功ready；503、连接取消或listener已退出均可，但须同时证明本次APP已退出/停止接纳、网络waiter已取消、旧worker/projector/main连接均已退出，P业务图和APP资金图未变化。只收到一次HTTP错误而旧APP继续工作不能通过。最后释放QA merchant gate，按原60秒lease与同command恢复，唯一P图、唯一既有资金、原A全部不变。普通SIGTERM与Guard失权分场景记录，不互相替代；所有观察、终止目标和耗时只保安全计数/布尔结果，不写DSN、SQL正文或秘密。

根已确认上述独占RESET ROLE→身份/专库复核→精确PID终止→Hijack物理Close方案，允许实施；真实执行仍以单独分配的main窗口为准。复核补充一个贯穿所有步骤的时间上限：在每次pg_locks查询发出前记录monotonic `providerObservationStarted`，把返回的`providerQueryAge`与之后全部往返、零事实复核、故障触发、退出和取消观察的`time.Since(providerObservationStarted)`相加，必须严格小于4.5秒；未知或超限一律失败。各自小于2秒的局部条件不能相加充当此证明；输出安全总provider age及cancel耗时，避免误把P既有5秒repository超时当取消。

通过前`payment-application-022-process.json`中的对应not_run保持不变，新的实际运行结论另存lifecycle证据，不改写旧轮记录。实际Guard失权仍使用现有单活机制，本节不证明025多活/epoch接班能力。

### 9.4 第三轮真实进程验证记录

2026-11-01根复核并修正贯穿时间预算后，脚本专项race（1.549s）与vet通过；取得独占main窗口，执行完整`--require-database --process`，exit0。该轮实际当前源码普通main SHA256为`a147fe4eb44c138dc4e0175ec332592df7ae9ab03507d176762e2d15e6154978`，mock保持`37ca0557811327e1b012cfd887b18e0b63489420101e873845ba67d9f24f9e32`；旧process/browser记录及其hash不改写。

原全部API/worker进程阶段再次PASS。新增在途SIGTERM屏障→signal为0ms，provider取消2ms，贯穿provider总age16ms；新增精确Guard失权屏障→terminate为4ms，provider取消4ms，总age51ms。两场均在持QA gate时证明APP已join、APP/worker/projector实际LOGIN连接全部0、P waiter与原command gate已释放、P command/order/transition/capture全0，然后才释放QA gate。随后等待原60秒lease自然到期，恢复分别62389ms和62386ms，经生产Expiry→原mutation generation签名query的not_seen→同canonical/digest/external command重试到ready。两场P各只有一条command/order/ready transition且无capture，没有新增入账；既有四笔资金身份和原A均不变。

最终所有子进程join、独占维护/管理员控制连接物理关闭、自有runtime连接0、原A及三schema/八角色清理全部PASS。独立filesystem计数本命令system temp私密根由运行时1个到退出后0个；main窗口已正式释放。安全分层证据留存于[生命周期结构化记录](../../docs/verification/payment-application-022-lifecycle.json)，共22个实际PASS，DB COMMIT回包丢失仍单独SKIP。物理在途取消测试不等于耗尽310秒HTTP排空期限，也不证明025多活或live PSP。

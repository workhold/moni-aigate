# 022-C0：运行时钱包 helper 精确合同候选

2026-11-01，供根审，**未冻结Go接口、未授权生产迁移或资金接线**。对应[lot](c0-lot-contract.md)、[全钱包序列](c0-sequence-contract.md)、[生产候选SQL](c0-production-schema.sql)、[列ACL](c0-production-permissions.sql)、[维护与proof](c0-maintenance-contract.md)。本文只规定原钱包各入口如何同事务追加C0来源图；C1原路退款、C2拒付/债务、023商品购买不在可调用kind内。

## 1. 真实接缝与必须改变的边界

现[ApplyFundingTx](../../internal/billing/funding.go)接受裸pgx.Tx及kind/id，依赖注释要求外层已锁来源/授权根；`[]lockedAccount`也只是Go快照，不能证明由哪个事务取得锁。现[Reserve](../../internal/billing/reserve.go)在原控制/账户等待后INSERT request，C0增加lot等待后必须仍保持request INSERT最后。现[finish](../../internal/billing/settle.go)账户等待后判24h期限；C0增加lot等待后必须再取最终DB时刻。[ApproveRefund](../../internal/billing/orders.go)当前通过post隐藏取账户锁，须改成显式一次账户锁再取lot。shared funding的manual/voucher/sandbox及重复读必须同时接线。

最终普通funding共用函数为billing内私有`applyFundingTx`；同包ApproveCredit、VoucherService经过私有parent token调用。跨包payments只保留专门的sandbox opaque token接口，不保留可从裸tx直接执行sandbox资金的旧出口。具体public接口见§4，不能同时留一个trusted bool或旧裸tx绕过分支。

这些token用于受审包内调用协议，不声称抵御任意恶意Go代码或owner任意SQL。PostgreSQL自身的完整source/journal/op/event/cache/head合同仍是独立提交门禁。`pg_locks`不能完整枚举无冲突时本会话持有的行锁，不用查锁表或传一份slice假装证明；token只能由实际执行固定锁语句的构造器发行。

## 2. 事务身份、作用域与一次性状态

所有写helper只使用caller-owned **READ COMMITTED、read-write、活跃且无错误**的事务。不Begin/Commit/Rollback，不自动retry，不联网；不对RR维护/核验tx授予在线写能力。

私有identity包括：具体pgx.Tx wrapper引用、其Conn指针、PgConn/backend PID、top xid的规范十进制、database/schema OID。建立和每个阶段入口都重读`transaction_isolation`、`transaction_read_only`、`pg_backend_pid()`、`pg_current_xact_id()`及连接TxStatus；要求与当前tx/conn全部相等。nil/typed-nil、零值、已提交/回滚、换wrapper/conn/PID/top xid、错误状态、换schema全部拒绝。identity只证明事务，不证明父锁、来源或金额。

```go
// 名称/形状候选；所有字段私有，只经本文构造器产生。
type walletTxIdentity struct { /* tx, conn, pid, topXID, dbOID, schemaOID */ }
type walletParents struct { /* identity, closedPurpose, exact roots, auth */ }
type lockedWallet struct { /* shared *walletCallState, accounts, epoch, tail */ }
type lockedFundingLots struct { /* same call, exact lot before-images, plan */ }
type walletDraft struct { /* same call, source locator, opID, seq, journal */ }
type walletAppend struct { /* immutable safe summary; no append authority */ }
type walletCompletion struct { /* same call, consumed write state; read-only check */ }
```

每个call有共享私有pointer状态，拷贝token仍指同一状态：`parents → accounts → lots → source_bound → appended → wallet_completed → source_complete`；任一步失败进入`invalid`。nil/零值无效；purpose固定且不能改tenant/epoch/source。每个transition消费前一状态，不允许重复append/headCAS、同token换source或错误后继续。`wallet_completed`只表示本call的来源图和head完成；全部原审计/投影/业务terminal/processing CAS完成后，另做只读全source核验，才进入`source_complete`。两者均**不表示COMMIT已确认**；helper返回的draft及FundingResult只可供事务内部编排，不直接作为HTTP accepted回执。

普通request/finish/refund的parent构造器是现真实锁函数的受审包装，并由包装自己执行这些锁，不能由调用方传`held=true`或任意IDs发行token。最终锁范围从原业务源/当前已锁principal推导；金额等私有quote只来自服务端已验证的不可变售价/profile或实际source，不从HTTP读取为可信值。

同一tx多次operation需新的call/ticket并读当前tail，先前token永不复用。若多operation涉及不同共享账户，必须在第一笔前用私有批量计划一次锁定**全部科目的并集**并按account ID C序；不允许第一笔锁高ID后再临时补较低ID账户。现生产入口每次只处理一笔wallet source，不开放通用批量HTTP；机制测试的outer/inner/outer不能反过来授权任意动态批量资金API。

## 3. 私有stage接口与责任

```go
func lockWalletAccountsTx(ctx context.Context, tx pgx.Tx,
    parents *walletParents) (*lockedWallet, error)

func lockReserveFundingLotsTx(ctx context.Context, tx pgx.Tx,
    wallet *lockedWallet, quote *reserveWalletQuote) (*lockedFundingLots, error)
func lockFinishFundingLotsTx(ctx context.Context, tx pgx.Tx,
    wallet *lockedWallet, source *lockedFinishSource) (*lockedFundingLots, error)
func lockRefundFundingLotsTx(ctx context.Context, tx pgx.Tx,
    wallet *lockedWallet, source *lockedRefundSource) (*lockedFundingLots, error)
func prepareNewFundingLotTx(ctx context.Context, tx pgx.Tx,
    wallet *lockedWallet, source *lockedFundingSource) (*lockedFundingLots, error)

func appendWalletSourceTx(ctx context.Context, tx pgx.Tx,
    wallet *lockedWallet, lots *lockedFundingLots,
    source walletSourceRef) (*walletDraft, error)
func recordWalletLotsTx(ctx context.Context, tx pgx.Tx,
    draft *walletDraft) (walletAppend, error)
func completeWalletAppendTx(ctx context.Context, tx pgx.Tx,
    draft *walletDraft) (*walletCompletion, error)
func checkWalletSourceCompleteTx(ctx context.Context, tx pgx.Tx,
    source walletSourceRef, completed *walletCompletion) error

func checkExistingWalletSourceTx(ctx context.Context, tx pgx.Tx,
    source walletSourceRef) (existingWalletSource, error)
```

`walletSourceRef`仅含封闭kind及实际source ID；不含可信amount、journal、class、FIFO时刻、delta或client选择的lot。允许kind精确为manual_credit/voucher_grant/sandbox_capture/reserve/finish/consumption_refund/legacy_consumption_refund；genesis由tenant trigger、epoch由独立离线init，不走在线append。

| 阶段 | 必须完成 | 禁止的后退或隐式行为 |
| --- | --- | --- |
| parent | 原来源根/授权/Key/model/controls，发特定purpose token | 仅普通SELECT后发锁能力；从前端传parent证据 |
| accounts | 按purpose推导精确科目；原初始化与排序FOR UPDATE保持分离语句；完整关闭Batch/Rows；读当前epoch/genesis/head及钱包合计 | 接受任意slice充当锁；在lot后补账户；TABLE权限失败自动GRANT |
| lots | 按实际来源选完整边/正额FIFO prefix，再按lot ID C序FOR UPDATE，重新读before revision/buckets；保存可执行私有plan | 先锁lot再回账户/父锁；跳过锁超时lot；截断prefix继续 |
| source_bound | 所有等待后复验资格/最终时刻，原core source与journal已同tx落库；从实际行与完整分录重读精确绑定 | 从调用方amount/delta直接建operation；把旧完整source当新发行；把未来audit/terminal尚未写完的中间态称为完整source |
| appended | INSERT protected operation；按私有plan建全部allocation/event并精确更新cache；检查行数 | 修改历史边/event；重读tail后重算并重发同source |
| wallet_completed | head精确前驱CAS恰1行；终态before/after与core source一致；消费全部写token | 只CAS不写source；缺边时repair；返回后复用draft写能力 |
| source_complete | caller完成原Key/PB/audit/outbox/资金source/commerce terminal/processing CAS，再只读核完整source；之后才可外层COMMIT | 在audit/CAS前调用完整source校验并吞错；把尚未确认的资金结果返回客户端 |

`appendWalletSourceTx`在中间态**不能调用完整`fl_read_source`作为source构造器**：候选`fl_source_*`已包含原审计/投影/credited等完整性要求，而其中部分事实按原编排在共同funding返回后才写。私有core reader只按封闭kind读取真实source/receipt/reservation/settlement/refund、tenant/amount/class/原journal及完整分录、当前protected top xid，并与已锁私有plan精确核对；不接受caller自报金额。它不承担最终审计/terminal证明，不以缺未来事实为由放宽候选SQL。

`completeWalletAppendTx`只做本call的head CAS与状态消费；生成的私有`walletCompletion`仅保留同tx/source/operation绑定与只读核验能力，无任何账户/lot/head写能力。调用方按§5写完原Key/PB/审计/投影/commerce terminal/processing CAS，再调用`checkWalletSourceCompleteTx`核完整`fl_read_source`和本call operation、credited及该kind附加terminal合同；只能消费一次，失败使本call invalid。独立Verifier不能调用该helper或SQL checker为自身作oracle。提交前任一原事实未完成，SQL deferred source检查仍必须拒绝；不得把“helper已返回”当成允许提交不完整外层事务。所有更新都检查恰1行，0/多行是invariant，不重读、换ID或重新执行。

同包共同funding返回一份私有结果，而非把completion塞入公开DTO：

```go
type fundingApplication struct {
    result FundingResult
    completion *walletCompletion
}
func (s *Store) applyFundingTx(ctx context.Context, tx pgx.Tx,
    parents *walletParents, source FundingSourceRef,
    scope FundingScope) (fundingApplication, error)
```

fresh成功必须`result.Applied=true`且completion非nil；既有完整人工入账只读结果必须Applied=false且completion=nil。两者不符是invariant。voucher fresh兑换不允许用既有结果伪装为本命令新发行。公开sandbox wrapper内部保存completion于已消费parents状态，只向payments返回其内部暂存FundingResult。普通人工审批在audit后消费completion；重复approved仅走既有source核验，不调用fresh complete。

新增funding不锁旧lot或读allocation；生成规范新lot ID，唯一源/lot约束解决冲突，不以`ON CONFLICT DO NOTHING`吞掉异体。R=0仍取得账户/epoch/head、验证source并发operation；lot计划为空，event_count=0；不能创建零本金lot。

Reserve按fifo_at和UUID C序分页128扫描，账户锁已序列化同tenant合法钱包写；选出的lot再按ID锁，锁后核revision/余额仍精确。需要证明完整正额prefix，不以只读头128行替代不足额查全。Finish读取原reservation全部边；Refund读取原settlement收费边和所有较早退款累计；独立Verifier另写重放，不能调用这些planner为自身作oracle。

每个call从进入wallet阶段起使用继承父deadline的至多5s总预算；不因翻页/换stage重置。原3s lock_timeout/5s statement_timeout保留；Rows/Batch结束后才能下一阶段，取消须清理。单页/单语句超时不证明无资金效果；整事务回滚或按未知COMMIT处理。

## 4. payments跨包的窄opaque接口

payments不能创建billing私有parent token；也不能将自己的`processorOrder`结构体当锁证明。候选新增以下**仅sandbox worker**的Go接口，类型导出但字段全私有，零值无效：

```go
type SandboxFundingOrderLock struct { /* private shared state + tx identity */ }
type SandboxFundingParents struct { /* private order/tenant binding */ }

func (s *Store) LockSandboxFundingOrderTx(ctx context.Context, tx pgx.Tx,
    orderID string) (*SandboxFundingOrderLock, error)
func (s *Store) LockSandboxFundingTenantTx(ctx context.Context, tx pgx.Tx,
    order *SandboxFundingOrderLock) (*SandboxFundingParents, error)
func (s *Store) ApplySandboxFundingTx(ctx context.Context, tx pgx.Tx,
    parents *SandboxFundingParents, captureID string) (FundingResult, error)
func (s *Store) CheckSandboxFundingCompleteTx(ctx context.Context, tx pgx.Tx,
    parents *SandboxFundingParents, captureID string) error
func IsFundingAmountOverflow(err error) bool
```

第一函数位于ProcessEvidence取得原order根的**同一最前锁点**，自己执行原order `FOR NO KEY UPDATE`，先核actual worker LOGIN/current=session、RC、固定DB/schema/environment及active C0 epoch/proof绑定。未知order返回原可区分的not-found供原review，不发行token；不是先持后层锁再补这个函数。原`processorLockOrder`可在同一锁点复读安全snapshot，先保留两次独立读的正确性；未来合并需另验，不能把普通snapshot变为token构造器。

第二函数只接受同tx有效order token，从原order推导tenant并实际取得tenant SHARE；调用点在identity/selection证明之后、capture_funding SAVEPOINT之前，取代原裸tenant锁语句。它不回取order、不取账户或lot，不接受caller自报tenant/amount。payments仍负责原evidence/identity/selection/lease的受审流水；最终Apply函数在账户后再次按持久capture完整证明，不把token当capture金额或支付真实性。

Apply只允许该order/tenant、当前原processing generation的capture；消费parent token一次，在当前savepoint内执行共同funding账户/时刻/core source/lot/op/cache/head/credited。原capture source INSERT、effects、audit和最终processing generation CAS仍由payments同tx负责。Apply返回只供内部使用的暂存FundingResult，parents共享状态只保留其已消费的capture/operation绑定；不携带可重用写token。

在原`processorFinishEvidence`最终generation CAS成功后、外层COMMIT前调用`CheckSandboxFundingCompleteTx`。它只接受该已消费parents与原capture，在同tx中只读核完整source、原capture audit/credited及最终processing绑定：沿原capture.verified_evidence_id/processing_generation、identity/selection、receipt/journal联接，确认同一持久capture对应的processing为processed且generation精确相等；不为processing新增结果ID列，不回取父锁，不重建钱包图。零值、换capture、换tx、未Apply/已失败/重复complete均拒绝。完整source SQL checker不代替本call的processing CAS绑定核验。最终CAS或complete失败全部资金回滚；CAS在applyCapture外层的编排必须能将这份只读completion绑定传回，不能将写token或暂存成功结果提前暴露到HTTP/worker外。worker不因此获得人工/券/request/消费退款表权或任意lot选择能力，Go导出符号不等于SQL授权。

旧`ApplyFundingTx(... FundingSandboxCapture ...)`在生产接线完成时必须关闭；不得给它一个可选nil-token兼容分支。以上构造器的实际SQL仅使用既有worker最小读/锁权限；如果精确权限不足，先根审增量，不以worker改用APP/owner绕过。

### 4.1 ProcessEvidence分支与fresh completion传递

`CheckSandboxFundingCompleteTx`仅接受**本事务fresh Apply成功**的capture，不能对每个ProcessEvidence结局无条件调用。payments内新增以下私有编排载体（不进入公开ProcessResult或JSON）：

```go
type freshCaptureCompletion struct {
    parents *billing.SandboxFundingParents // 已消费写状态，只余最终只读核验
    captureID string
    evidenceID string
    generation int64
}
type captureApplyResult struct {
    result ProcessResult
    completion *freshCaptureCompletion
}
func (p *Processor) applyCapture(ctx context.Context, tx pgx.Tx,
    e processorEvidence, o processorOrder, b leaseBinding,
    ids processorApplyIDs, order *billing.SandboxFundingOrderLock,
) (captureApplyResult, error)
```

载体字段均私有，仅fresh Apply+原capture audit成功后才能构造；绑定evidence/generation和实际capture/receipt/journal必须来自该已消费parents/持久source，不能由ProcessResult反向发行。`Outcome=credited`当且仅当completion非nil且结果IDs与该绑定精确相等；existing/review结果的completion必须nil，任何不一致都回滚为invariant。下面每次transaction retry先清空公开result及私有carrier，旧carrier永不复用。

| 原分支 | C0新写能力及completion | 原processing收尾 | C0最终核验 |
| --- | --- | --- | --- |
| evidence已processed/processing_review，走restoreEvidence | 不创建order/tenant/wallet token，completion=nil | 沿原恢复只读完整结果，不新CAS | 有资金关联时增强原完整来源重读，走existing new/legacy检查；不调用fresh complete |
| unknown order | order构造器返回not-found，不发行token，completion=nil | 原review+final processing_review CAS | 不调用fresh complete |
| unsupported、金额/币种/window/revision冲突 | 可能只有早期order锁token，无tenant/wallet写能力，completion=nil | 原review+final processing_review CAS | 不调用fresh complete |
| observation/closed | 只有原order锁，completion=nil | 原final processed CAS | 不调用fresh complete |
| identity/selection冲突或既有overflow review | 不取得tenant/wallet token，completion=nil | 原review+final processing_review CAS | 不调用fresh complete |
| processorExisting发现完整旧capture | 原order锁，completion=nil；增强既有source完整new/legacy核验 | 当前新evidence原final processed CAS | 只读核原capture，不要求旧journal属于当前top xid，不调用fresh complete |
| fresh Apply typed overflow | parents一次消费后invalid，ROLLBACK TO完整capture_funding；清零carrier | 原funding_amount_overflow review+final processing_review CAS | 不调用fresh complete，不将ROLLBACK后的source当存在 |
| fresh Apply成功+原audit成功 | 唯一非nil只读completion；全部写token已消费 | 原final processed generation CAS必须恰1行 | CAS成功后核evidence/generation/结果IDs，再调用CheckSandboxFundingCompleteTx一次；之后才回外层COMMIT |
| 任意基础设施、audit、最终CAS或complete错误 | 所有carrier失效并清空 | 外层回滚；未知COMMIT走§7 | 不从错误回退成existing/review或重试同token |

`ProcessEvidence`只在fresh分支进入最终complete：先验证carrier/Outcome矩阵→原`processorFinishEvidence(...,"processed","")`成功→核carrier为本次lease→`CheckSandboxFundingCompleteTx`→返回transaction callback；最后原runtime.check与COMMIT仍由Processor事务拥有者负责。其他分支保持原相应CAS，不调用带nil/零parents的billing接口。completion从applyCapture跨到callback仅包含只读核验能力，不能新增capture或重新Apply。

## 5. 全部钱包入口顺序与原子写集

### 5.1 人工入账与券

manual：credit根UPDATE → checker/maker当前角色及异人 → tenant SHARE → 私有funding parent → accounts AVAILABLE/原clearing → active epoch/head → 新lot plan → 锁后DB时刻/source复验 → 原journal/批准credit/receipt → core source append/lot/event/cache/head → credited → 原approval audit → 最终source complete核验 → caller COMMIT。重复approved只走完整只读核验，不新取sequence或补图。

voucher：原batch根UPDATE → policy SHARE → tenant/member/user原锁与密钥/command指纹核验 → 私有parent → **建立原voucher_business savepoint** → accounts/epoch/head/newlot → 锁后DB时刻复验expires_at、active/policy与未兑换 → 原redemption/journal/receipt → C0图/head → credited → voucher audit/commerce terminal → 最终source complete核验。外层业务savepoint覆盖全部funding效果，源根和授权父锁在savepoint前。若现代码把账户等待置于savepoint内，保持这一归属，不把回滚后的账户token继续使用。

#### voucherEffect跨callback的只读completion

原`RedeemVoucher`的apply callback在共同funding之后写voucher.redeemed audit，但`commerce_operation_receipts`由外层`VoucherService.run`在callback返回后INSERT。因此callback不能提前调用完整source complete，确切候选变更为：

```go
type voucherEffect struct {
    // 原resource/amount/funding/journal/batch/codes字段保持用途。
    walletComplete *walletCompletion // 新增私有、不可序列化的只读completion
}
```

callback接收私有fundingApplication；fresh共同funding成功、全部write token消费且原voucher audit成功后，返回原四个资金字段及`walletComplete=application.completion`。该completion内部已绑定实际receipt/source、journal、redemption、command operation、tenant和actor；不从effect的普通string字段重建能力。callback任意错误使已有completion失效，不返回可消费载体。

`VoucherService.run`每次retry先清空out/effect，按以下矩阵编排：

| runner分支 | completion与资金字段要求 | terminal后行为 |
| --- | --- | --- |
| fresh accepted voucher.redeem | walletComplete非nil；原resource/amount/funding/journal四字段与completion精确相等 | INSERT原accepted terminal→读回严格DTO→以completion内source调用checkWalletSourceCompleteTx一次→将effect.walletComplete置nil→才可完成callback并外层COMMIT |
| fresh rejected voucher.redeem | callback错误先invalid；原ROLLBACK TO覆盖资金图，effect整个清零，四关联字段null且walletComplete=nil | 仅写原rejected terminal；不调用fresh complete、不重试funding |
| fresh其他券管理命令accepted/rejected | 无钱包source，walletComplete=nil且按原各kind资金字段矩阵 | 原terminal正常完成，不调用钱包complete |
| 既有command terminal重读 | 不执行apply，不发行completion | 原当前授权/指纹校验后，accepted兑换按持久terminal关联实际receipt走existing new/legacy完整核验；rejected保持原null矩阵，不补图、不fresh complete |
| terminal INSERT/scan/complete或其他基础设施错误 | 已有completion失效并清空out/effect | 整事务回滚；不转业务rejected，不暴露暂存accepted |

runner不得把“nil completion”当作fresh accepted兑换的合法兼容分支；其他管理kind也不能夹带completion。成功消费后，`voucherRunResult.effect`对外只保留原安全数据及一次展示券码，不携带该内部指针。unknown COMMIT同样清零out及completion；下一次同operation只读原terminal，不复用旧carrier。没有public closure/hook或可执行任意SQL的finalizer。

### 5.2 Reserve

原idempotency claim → tenant/member/user/Key UPDATE/model SHARE及授权 → 已赋价组锁/不可变零售价snapshot与服务端R → 周期controls → accounts AVAILABLE/HELD → epoch/head → 完整FIFO与lot锁 → **最后才INSERT request/reservation** → 原020 request trigger给出pb_admitted_at/top stamp → AdmitCustomerTx → 原reserve journal（包括R=0两条零分录）→ reserve core source append/全部allocation/event/cache/head → PB reserve、Key.held、recovery/audit/reserved outbox → 最终source complete核验 → 外层COMMIT。

不能先INSERT request再等待lot；否则020当期预算/Key expiry准入时刻早于真实串行点。private reserve quote不是资金source，request/reservation写后append必须重读其实际R/tenant/原journal。

所有控制/账户/lot等待后，使用`admission.At`重新检查Key expires和request_deadline。原售价snapshot的pricedAt保持其原选择时刻，不重新选择更晚价格；本片不改变reservation.deadline当前生成口径（现从原price选择时刻+24h），若要改为admission.At+24h须另审，不能顺带延长已约定责任期限。所有deadline判定不使用Go wall clock。

### 5.3 finish与所有释放路径

原lockRequest顺序/必要model升级 → 周期controls → accounts AVAILABLE/HELD及可能REVENUE → epoch/head → 原全部reservation lot边/lot锁 → **新的DB clock** → 最终24h判定/charge决定 → 原journal/唯一settlement → 原reservation/request终态与退款累计一致 → finish append/全部charged+released边/event/cache/head → Key/PB/原recovery与reconcile关闭/audit/outbox → 最终source complete核验 → COMMIT。

lot等待跨deadline时charge必须变0、reason released_deadline；原raw/waived逻辑保持。为潜在正charge先锁REVENUE，最终deadline释放后无需分录时不能在lot后再增锁科目。C=0且R>0仍对全部原边建终态，不重新选来源；R=0仍一operation。Release/24h/recovery/Reconcile最终都经同一finish，pending不改桶、不追加seq。

### 5.4 消费退款

refund UPDATE → 原checker/maker角色/异人 → 原lockRequest → settlement UPDATE → 显式accounts AVAILABLE/REVENUE（取代post隐藏锁）→ epoch/head → 原charged边及此前退款/受影响lot锁 → 锁后源复验及可退累计 → 原postLocked/refund approved/settlement.refunded_amount更新 → refund append/逆原边/event/cache/head → 原audit/outbox → 最终source complete核验 → COMMIT。不恢复Key/PB spent。

若原settlement属于精确legacy terminal，使用一份新nonrefundable legacy退款lot、issued/A+F，无伪历史charged边；fifo_at精确采用本次protected operation.created_at。旧approved重复读不重新发行；旧pending切换后批准才走新legacy分支。全部批准退款NUMERIC合计必须等于settlement.refunded_amount并不超过charge；source checker自身触达，不依赖某个额外UPDATE触发器。

## 6. 来源、journal与protected top xid

全部新非genesis钱包operation必须与其原journal同top事务，精确式为`j.created_txid::numeric=o.created_xid::text::numeric`，加id/tenant相等；生产候选在source绑定后用固定23514 `funding_lot_operation_transaction`检查。原002完整journal lines的current-tx guard给出见证；不比较tuple xmin或时间，不向所有原source加birth列。

| 新operation | 原journal.operation_id／source | 必须同top xid的写集 |
| --- | --- | --- |
| manual_credit | credit.id／新receipt.id | journal及完整两分录、批准credit/receipt、operation/event/cache/head |
| voucher_grant | redemption.id／新receipt.id | 原voucher savepoint内redemption/receipt/journal与全部C0效果；commerce terminal仍同外层tx |
| sandbox_capture | capture.id／新receipt.id | 原capture_funding savepoint内capture/effect/journal/receipt/C0效果；原audit与final generation CAS仍同top tx |
| reserve | reservation.id | 新request/reservation、原journal、allocation/operation/event/cache/head；request pb_created_xid仍走020独立证明 |
| finish | settlement.id | 新settlement/journal、原终态更新、全部terminal边及C0效果；原request/reservation出生于早前tx是正常行为 |
| consumption_refund | refund.id | 当前批准source/journal、累计更新及逆原charged边效果；refund提案可来自早前tx |
| legacy_consumption_refund | refund.id | 新批准journal/累计与新legacy lot/op/event；历史settlement/旧approved快照不改 |

seq0没有journal；旧epoch_opening走完整opening/basis，tenant_genesis走新增tenant birth protected top xid。原source提案/请求创建事务与最终资金事务不同不构成错误；本合同绑定的是**发生资金效果的journal**，不声称所有关联历史tuple出生于同一事务。

## 7. 重读、savepoint、错误与未知COMMIT

`checkExistingWalletSourceTx`只返回`new_complete|legacy_complete`的只读结果（安全IDs/amount、无append能力）；部分source、缺op/边/cache/head、重复principal或错误legacy边界均invariant。不取新seq、不创建补差lot、不尝试继续旧半图。普通重复credit/refund/settlement沿原授权重读；sandbox沿原完整capture/audit/processing图重读，不要求旧journal等于当前调用top xid。

token绑定具体wrapper。pgx nested Tx只在其自身wrapper内新发行/消费token；不得把outer token交inner或inner token交outer。现voucher/payment使用原始SAVEPOINT且wrapper不变，因此不能声称只靠wrapper/PID/top xid能发现ROLLBACK TO；必须由call状态一次性消费、错误即失效及固定调用边界保证。

所有账户/lot/source/C0 effects写ticket在savepoint内部创建并在helper返回前完成或失效，不越过边界返回写能力。§4.1/§5.1的只读completion可在**同一未回滚的外层事务**内跨函数返回，等原CAS/terminal写完再消费；它不持有可重用write ticket，不能跨COMMIT/ROLLBACK TO继续。ROLLBACK TO之后不得reset并复用任何effect token或completion；outer parent锁token仅在savepoint前已取得且只保留本次来源的已消费状态，不允许第二次资金尝试。capture overflow后只追加原review；voucher确定业务拒绝只写原terminal。若未来需要rollback后合法新操作，必须新call并重新取得完整账户/lot/当前tail，不复用缓存before-image。

只有由共同funding的原`postLocked.checkedAdd`在账户余额校验阶段产生的**私有typed overflow**可被`IsFundingAmountOverflow`识别并转成原voucher rejection/payment review；字符串同名Error、lot桶越界、NUMERIC不守恒、SQL23514、权限/取消/锁超时、CAS失败、缺源均不匹配。该typed错误必须在call失效、外层ROLLBACK TO完成后才允许持久业务结果。先前任何数据库statement效果也在同savepoint回滚，不能声称checkedAdd自动证明整个事务无效果。

普通Store或payments事务拥有者最终COMMIT：仅确认成功后向HTTP/worker返回Applied/accepted。确定rollback/deadlock/serialization错误可按原有限策略重试完整事务，每次重建全部tokens并清空draft；helper不retry。COMMIT回复未知返回固定unknown与**零资金DTO**，销毁或隔离原连接，用独立有界cleanup确认关闭，不放回可重用池；保留原operation/source ID只读核查，不换ID/重放。读到完整原图才能返回原已提交结果；404/查询失败不是授权再发行。恢复任务不重发模型生成请求。

## 8. 冻结前阻断与验收

本合同不证明完整启动v2/schema/role/epoch门禁已实现；Store写阶段不得用可配置trusted bool跳过它。完整C0 runtime绑定的发行/装配由维护/proof合同另片冻结，所有listener/worker必须先通过；本helper合同不提供一个临时“按fixture marker启动”出口。

冻结前须根审public sandbox token/API及全部现ApplyFundingTx调用点替换、private parent构造器的实际锁顺序、multi-operation共享账户并集规则、原24h deadline锚点和typed overflow/commit-owner接口。需要新增schema/权限证明时先补精确合同，不在实现中偷加。

实际验收至少覆盖：普通三funding入口、零reserve/finish、跨lot部分结算/逆退款、legacy terminal/unfinished、账户及lot等待跨Key expiry/period boundary/financial deadline、错tx/wrapper/token复制重用、外inner互换与ROLLBACK TO、原完整source幂等重读、头CAS失败/最终processing CAS失败全回滚、真实COMMIT成功丢回复及只读恢复、actual APP/worker/projector负权限、完整启动和长历史资源预算。独立Verifier必须另写重放；scratch SQL或纯planner通过不能标本helper完成。

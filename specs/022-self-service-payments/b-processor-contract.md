# 022-B Processor 精确执行合同候选

2026-11-01，**Processor行为与runtime私有接缝已获根冻结；根已授权隔离stage候选实现，尚未授权本片生产集成、修改021/P协议或启动main。** 对照 [B1](b1-contract.md) §4–8、[B2](b2-funding-contract.md)、已追加的 [021](../../migrations/021_payment_sandbox.sql)、已实现的 [FundingSandboxCapture helper](../../internal/billing/funding_sandbox.go) 与 [P签名入口](../../internal/paymentproto/signing.go)。生命周期与运行证明另见 [runtime合同](b3-runtime-contract.md)，本稿不复制其manifest/attestation设计。下文SQL是实现合同；隔离stage的专项证据及其边界见§11。

本文使用生产 `payment_` 函数/真实列名；B1原型的 `p1_` 名称不能进入新Go代码。B2 helper已存在不代表Processor、Runner或APP支付运行链路已实现。

## 1. 范围、前置与两处B1接缝修订

Processor只拥有三个持久状态机及已验证evidence应用，不拥有HTTP用户授权、SKU/订单创建、通知HMAC接收、mock控制面或schema安装。只接受本环境独立真实worker LOGIN；构造器必须完成runtime候选的完整迁移/角色/环境/Guard证明。普通无marker/off不构造它；有既有B事实的正常off仍按原授权收尾。Recovery只保留query收集，所有资金处理暂停。

两个修订已获根明确接受，不暗改P；本稿的签名/锁序在这些接缝优先于B1原目标签名，其他B1/B2条款继续适用：

1. **Query record接封存Work。** 将尚无生产caller的B1目标签名 `RecordQuery(ctx, QueryLease, headers, raw)` 替换成 `RecordQuery(ctx, QueryWork, headers, raw)`。Claim时尚未决定是否复用旧child；Record时当前指针可能已进入新代，原lease不足以绑定实际发出的query。Work封存原lease与exact child/request/digest，避免共享可变preparation cell或用未验raw qid选来源。P的QueryRequest/签名串完全不变。
2. **mutation到期回收先取order。** B1“claim/expire只锁自身lease”对纯claim、query/evidence expire成立。mutation expire还须插入唯一query supervisor；若先持mutation lease锁，可能等另一Record事务未提交的supervisor，而Record正等待同一lease的最终CAS。故改为无锁限100定位→order NO KEY UPDATE→自身outbox SKIP LOCKED重检→unknown+ensure supervisor。其余路径不借此扩大锁域。

## 2. 最小Go ABI与不可伪造工作值

保留 `NewProcessor(ctx,*pgxpool.Pool,Options)`，Options/Guard/Close与生产开池边界由runtime候选冻结。两个manifest只决定application/worker组件，不决定运行mode；`Options.BindLifecycle`一次固定`ModeOff|ModeSandbox`（空规范为off）、Recovery与Guard，构造后不可切换。off有历史证明时按既有承诺收尾，不能把off误当成禁止一切历史Processor工作；普通off/无历史证明不构造Processor。Recovery仍覆盖两种mode并只允许query。本稿不公开DSN、池、keyring、支付地址、任意SQL执行或可信标记构造器。

```go
// package payments；以下类型均无可写导出字段/JSON接口。
type MutationLease struct { /* owner seal; outbox ID; kind; generation; lease bound */ }
type QueryLease    struct { /* owner seal; supervisor ID; generation; lease bound */ }
type EvidenceLease struct { /* owner seal; evidence ID; generation; lease bound */ }
type MutationWork  struct { /* seal + exact lease; endpoint; owned canonical request/digest */ }
type QueryWork     struct { /* seal + exact lease; child ID/request/digest; confirmed prepare */ }
type MutationResponse struct { /* adapter seal; exact work identity; one typed result */ }

func (*Processor) ClaimMutation(context.Context) (MutationLease, bool, error)
func (*Processor) PrepareMutation(context.Context, MutationLease) (MutationWork, error)
func (*Processor) RecordMutation(context.Context, MutationLease, MutationResponse) error
func (*Processor) ClaimQuery(context.Context) (QueryLease, bool, error)
func (*Processor) PrepareQuery(context.Context, QueryLease) (QueryWork, error)
func (*Processor) RecordQuery(context.Context, QueryWork,
    paymentproto.SignedHeaders, []byte) error // 明确替代B1目标
func (*Processor) ClaimEvidence(context.Context) (EvidenceLease, bool, error)
func (*Processor) ProcessEvidence(context.Context, EvidenceLease) (ProcessResult, error)
func (*Processor) ExpireLeases(context.Context) error
```

Lease的seal是构造器建立的私有实例身份，不是来自caller的UUID/Trusted bool。DB身份字段不可变；零值、跨Processor/环境、generation≤0、kind错配一律在DB前拒绝。重启不重建旧lease：已提交claim等待到期恢复；COMMIT未知不返回一个貌似确认的lease。尚无due是`zero,false,nil`，不是Unavailable。

Work只在Prepare事务COMMIT确认后返回；固定endpoint来自已验证Options，只能checkout/cancel/query对应固定路径。canonical bytes、P DTO内指针与headers/body必须深复制，任何内部借出副本不能改保存的请求。QueryWork保存exact immutable child，即使它的created_generation早于本次claim（未知child复用），也保留**本次**lease generation与child自己的created_generation/mutation_generation，二者不能混同。Record重新读库核全部绑定，seal不替代DB证明。

Mutation无需改为Work参数：同outbox的外部CommandID/canonical/digest永久不变，lease给出代次，Response另封存同一Work/lease identity和adapter来源，Record可重新完整比对。不同代/不同work的Response即使金额相同也拒绝。MutationResponse只由同package私有发送器产生，禁止公开`NewResponse(raw, trusted)`或测试专用生产setter。

Runner同属payments包，只调用私有 `sendMutation(ctx, MutationWork) (MutationResponse,error)`、`sendQuery(ctx, QueryWork) (SignedHeaders,[]byte,*transportFailure,error)` 和 `recordQueryFailure(ctx, QueryWork, transportFailure) error`；不存在外部自填failure-code的写接口。sendQuery的error专门表示本地Guard/LeaseLost/InvalidWork/ctx等失败，Runner先处理该error；`transportFailure`仅表示远端固定分类及work seal，不能带任意driver/HTTP正文或本地error进DB。公开RecordQuery仅接有body的真实签名回复，不用nil headers伪装一次网络失败。

ProcessResult保留B1：Outcome=`processed|credited|existing|review`；仅credited/existing带CaptureID/FundingReceiptID/JournalID，review仅带固定ReviewReason；其余指针为nil。不JSON直出，不返回provider raw、摘要、密钥、任意tenant/金额参数。

## 3. 统一事务、Guard、错误与未知提交

全部DB方法单条实际worker连接、READ COMMITTED、最多5s总context，包含pool/Guard/本地协调/锁/COMMIT；锁3s、语句5s并受更早总deadline限制。PSP IO永远在事务外，总10s，包括连接、headers及最多64KiB body。Prepare/私有发送前以新DB时刻确认本代仍leased且剩余≥15s；检查后直接发固定请求，不把Work排进额外队列。此检查不宣称能阻止随后DB时钟跳变；迟回结果由generation CAS保护。

每次短事务前、网络发送前、落结果前、COMMIT前执行已绑定Guard；正常caller取消先返回其取消结果，不将它传入底层Instance.Check。Guard自身采用runtime候选的独立最多3s检查与永久失效语义。Guard失效后只允许有界rollback/归还连接，不再写任何业务行/发PSP/成功ACK。Close取消等待中的锁/网络并排空，不释放全局ownership后留下旧IO。

根已接受的固定内部sentinel（具体声明所有权待runtime接缝冻结）：

| sentinel / Error字符串 | 含义与Runner行为 |
| --- | --- |
| ErrPaymentsInvalidWork / payments_invalid_work | 零值、跨实例、未经Prepare等编程契约错误；无DB/IO，Runner停止并降低ready |
| ErrPaymentsLeaseLost / payments_lease_lost | 本代已失去可变状态权；释放slot。Record可能已确认追加合法immutable证据，但没有ACK新代 |
| ErrPaymentsRecovery / payments_recovery | Recovery禁止Mutation/Evidence；零claim预算，Runner本就不调这些分支 |
| ErrPaymentsGuard / payments_guard_unavailable | 生命周期永久失效，全停 |
| ErrPaymentsIntegrity / payments_integrity | 已持久原件/引用/资金图矛盾或违反冻结DB协议；不伪装业务review，Runner停止并降低ready |
| ErrPaymentsUnavailable / payment_unavailable | pool/SQL/权限/未知执行错误；不暴露原文，未完成事务整体回滚 |
| ErrPaymentsOutcomeUnknown / payment_outcome_unknown | COMMIT未获确认；无确认结果，先只读原ID，绝不换ID/重置预算 |
| ErrPaymentsProtocol / payments_protocol_invalid | 远端回复不能形成受信结果；已按相应failure持久化才结束slot，不把它当已付款 |
| ErrPaymentsQueryConflict / payments_query_conflict | 同query_id可信异体冲突已COMMIT；保第一原件，不替换快照 |

Configuration/Environment/LifecycleClosed沿runtime候选，不在本稿重复定义。以上为后台内部错误，不扩HTTP白名单。正常语义review是 `ProcessResult{Outcome:review}` 和nil，不是SQL error。raw PGError、DSN、body、authorization、签名/nonce/key不进入Error字符串或默认日志。

只有SQLSTATE 40P01/40001、数据库已确认整事务回滚时，资金处理可在同5s总预算内最多**两次额外重试**；初次前分配的identity/capture/review UUID沿用。其余错误无隐式重试，尤其不凭字符串/所有22003判overflow。COMMIT取消/断链/回包未知不重试写；resolver只读原claim/child/observation/evidence/receipt。看到暂缺来源不等于原事务永不提交，不据此补发或重新应用；后续仍由原状态机/lease恢复。

清理使用独立最多5s context rollback；连接无法确认空闲则移出pool并有界关闭，不把活动事务放回池。所有返回结果先保持局部，COMMIT确认后才可返回。明确“已durable append但stale”的Record先提交、再返回LeaseLost，不在tx回调里返回该error导致误回滚。

## 4. Claim：一次只认领一个空闲slot

每个claim只有自己的状态行锁，不取order/accounts，不提前Prepare或网络；claim COMMIT之前不得交给Runner。两个网络物理slot共享mutation/query，另四个evidence slot；空闲才claim，公平性沿runtime候选。ClaimMutation/Evidence在Recovery连接前拒绝；ClaimQuery正常执行。

mutation/query共享SQL形状，`$1`为内部固定kind集合，`$2`为10或20，不能由HTTP指定：

```sql
WITH picked AS (
 SELECT id FROM payment_command_outbox
 WHERE kind=ANY($1::text[]) AND state IN ('pending','retry_wait')
   AND next_attempt_at<=clock_timestamp() AND attempt_count<$2
 ORDER BY next_attempt_at,id COLLATE "C"
 FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE payment_command_outbox b
SET state='leased',generation=b.generation+1,attempt_count=b.attempt_count+1
FROM picked p WHERE b.id=p.id
RETURNING b.id,b.order_id,b.kind,b.generation,b.attempt_count,b.lease_until;
```

`payment_outbox_transition`生成60s期限、清旧error并证明generation=attempt_count。不领取submission_unknown，不直接从expired leased再次claim；Expire必须先完成合法状态转移。状态/下一时刻在UPDATE后重读RETURNING实际trigger值，caller不指定lease/next/backoff。

evidence同形状：按next/evidence_id排序，pending/retry_wait且due、count<20，`FOR UPDATE SKIP LOCKED LIMIT 1`，更新state=leased、generation+1、attempt_count+1；RETURNING真实30s lease。Claim确认后即使未发IO/未处理就崩溃也不退次数；没有“claim失败后减一”的恢复。

## 5. Mutation Prepare / Record与唯一supervisor

### 5.1 PrepareMutation

非锁定读本outbox定位order→order `FOR NO KEY UPDATE`→普通读原outbox与完整冻结order/环境。核seal、本代leased、DB剩余≥15s、所有原请求P Parse+Canonical与原字节/digest相等，checkout九字段/cancel五字段和CommandID、merchant_order、冻结金额/半开条款完全一致。不能因当前policy/售卖/owner/tenant状态重验而撤销旧授权，也不以expires已过跳过旧checkout（PSP可给原ID确定closed）。cancel须已有永久intent，checkout即使后有cancel也不能换body变cancel。

Prepare不改state/generation/期限，不创建第二mutation。确认COMMIT后返回封存Work；错误/未知COMMIT不返回Work，不发IO。再次调用同lease可返回同canonical，不能修改原ID或根据今天的SKU重新报价。

### 5.2 RecordMutation

验证Response seal/原work绑定→非锁定定位→order NO KEY UPDATE→重读原outbox。成功reply必须重新 `ParseCommandReply`/Canonical，并比对schema/env/merchant/merchant_order/command/kind/parameter_digest；该reply没有签名capture资格。插入 `payment_command_observations(id,outbox_id,generation,canonical_reply)`，同 `(outbox,generation,reply_digest)` 原样重读，未知ID或异原请求拒绝。已失代次仍可保存合法旧generation观察；绝不更新当前state/generation。

当前未过期代次按下表处理，最终CAS必须带 `id/state=leased/generation/lease_until>clock_timestamp()`：

| 结果 | 状态与同tx事实 |
| --- | --- |
| 合法确定reply，包括ready/closed/rejected/in_progress/captured hint | completed；in_progress或captured hint先ensure唯一query supervisor。captured hint没有资金应用 |
| 超时/断链/未知HTTP状态/认证失败/格式或身份不匹配、环境异常等不能证明业务结果 | submission_unknown，固定error=timeout/network/auth/protocol/environment/temporary；同tx ensure supervisor |
| 第10代未知 | submission_unknown；trigger固定attempts_exhausted，仍ensure supervisor，永不第11次claim |

**首版发送器没有把远端不确定结果直接设成review_required的分支。** 021允许`permanent`终止，但其可判定来源尚无冻结协议；本稿选择所有未确认mutation均unknown+query，不把HTTP错误当未付款。后续要新增permanent来源必须另审，不在实现中自由猜测。

ensure supervisor仅在order根下执行：`INSERT payment_command_outbox(id,order_id,kind,original_outbox_id) VALUES(fixedUUID,order,'query',mutation) ON CONFLICT (original_outbox_id) WHERE kind='query' DO NOTHING`；新RC语句重读并核kind/order/original，不能重置已有generation/review/budget/current_query。id在一次逻辑方法首次尝试前固定。由deferred `payment_outbox_complete`保证unknown/in_progress/capture hint没有漏supervisor。普通completed ready/closed不凭空创建query。

CAS零行表示stale：合法reply观察可以提交后LeaseLost；不确定transport没有可信观察可保存，返回LeaseLost且无state写。任何constraint/SQL错误整体回滚，不能返回成功或留下半个supervisor。

## 6. Query Prepare：冻结child、未知复用与预算

非锁定定位supervisor→order NO KEY UPDATE→supervisor `FOR UPDATE`并核当前lease剩余≥15s。原mutation普通读，无回锁另一order；必须是同order checkout/cancel且generation>0。完整Parse/Canonical原mutation，不能仅取数据库digest信任body。

1. current_query_id非空、原child没有 `payment_verified_queries`：沿用exact child ID/request/digest，不新建，不改指针；child.created_generation可以小于本代。该已提交child的请求必须仍绑定原mutation固定身份。
2. current child已有永久not_seen/in_progress，且本代还未创建child：分配新queryUUID，构造P QueryRequest，先插immutable child，再更新current_query_id；trigger冻结mutation_generation=插入时真实原mutation.generation与DB created_at，并要求child.created_generation=本代。新事实不能复用旧永久snapshot。
3. 本代已创建child：重复Prepare只返回该同一child；不因收到新snapshot而在同代再换ID。若Prepare COMMIT未知，先只读本代child/指针，未确认前不发IO，不新选ID。
4. 原child已有completed：不再发query。只在当前未过期lease下将supervisor completed并处理下节允许的原mutation完成；COMMIT确认后返回LeaseLost（无Work），表示本代已经终结、无需IO。根明确接受该返回不是fatal，Runner只释放slot。不能把“无需IO”伪装新Work让Runner仍发送；COMMIT未知仍返回OutcomeUnknown而非LeaseLost。

核心写入形状：

```sql
INSERT INTO payment_query_attempts
 (query_id,supervisor_id,created_generation,canonical_request)
VALUES ($child,$supervisor,$generation,$ownedCanonical)
RETURNING query_id,created_generation,mutation_generation,created_at,request_digest;

UPDATE payment_command_outbox
SET current_query_id=$child
WHERE id=$supervisor AND kind='query' AND state='leased'
  AND generation=$generation AND lease_until>clock_timestamp();
```

只有新child需要这两步；复用旧unknown child不触发同指针UPDATE（021会拒绝无变化preparation）。更新零行整体回滚，新child不得孤立提交。首版不通过 `ON CONFLICT`吞掉同代另一child；完整核原same-ID才可明确重读，否则Integrity。

QueryWork封存上述RETURNING及原CanonicalQuery；记录入口不根据raw reply内qid选择child，也不根据今天supervisor指针改掉旧Work。IO只发送封存query body到固定query endpoint，无DB事务。

## 7. Query Record：先raw验签，再原件/证据，最后状态

复制headers/body并验证Work→非锁定定位child/supervisor/original→order NO KEY UPDATE→读原child request并P Parse/Canonical/digest全等→取一次独立DB clock_timestamp→调用 **P.Keyring.VerifyQuery(env, originalRequest, dbNow, exactHeaders, exactRaw)**。验签通过前不创建verified原件；canonical不是HMAC输入。验证成功后用VerifiedQuery.Canonical取得owned bytes；verified_key_id只取同一次成功调用的headers.KeyID，不新增Verified字段getter或从DB重建Verified对象。

新原件以query_id唯一：INSERT verified_query；冲突后新RC语句读原snapshot_digest/canonical并严格重验。相同返回原件，不重写received_at/key/canonical；可信异体只追加 `payment_query_conflicts`、COMMIT后QueryConflict，不替换第一快照、不据异体推进state。新原件同tx插 `payment_verified_evidence(id,verified_query_id,...)` 和 `payment_evidence_processing(evidence_id)` 初始pending；normalizer派生kind/所有事实/verified_at，不接caller金额/tenant。包括not_seen/in_progress也保存observation evidence，不提前资金处理。

合法旧Work/旧generation响应仍可追加这些immutable事实，即使supervisor已换child/review/完成。后续evidence处理依真实签名事实，不能因旧lease丢弃capture。只有同时满足 `supervisor.state=leased AND generation=work.lease.generation AND current_query_id=work.child AND lease_until>DBnow` 才推进当前supervisor/原mutation。

| 可信原snapshot | 当前supervisor | 原mutation可变投影 |
| --- | --- | --- |
| completed | completed，清error | 若原mutation仍submission_unknown且下述本代cut成立，则completed；无资金应用 |
| not_seen / in_progress且query claim<20 | retry_wait，DB backoff | in_progress绝不补发；not_seen仅下述条件全部成立才retry_wait |
| not_seen / in_progress且query claim=20 | review_required/attempts_exhausted，先保存本快照 | 永不因第20次not_seen重开mutation，既有unknown保持待核查 |

原mutation unknown→retry_wait须同时：本query supervisor在leased/retry_wait且非review，current child精确本Work，child.mutation_generation=原mutation.generation，child.created_at≥原mutation.last_submission_unknown_at，原mutation.attempt_count<10，checkout没有cancel intent，snapshot=not_seen。参数仍原同ID同body。unknown→completed也核原mutation代次与unknown时间cut；其他state不被旧query覆盖。原mutation leased的迟reply由其自己的Record/expire处理，不由query改其代次。SQL先在当前supervisor lease可用时判全部条件；在同order根下先做原mutation允许的UPDATE，再做最终supervisor CAS；任一步预期CAS失败则整tx回滚这些可变状态，immutable事实可在独立明确路径重新只保存，不能带着未确认状态部分提交。

私有 `recordQueryFailure` 重新验证Work/child，order→最终本代supervisor CAS：query<20进入retry_wait（固定timeout/network/auth/protocol/environment/temporary），同child没有可信snapshot则下一claim仍复用；第20次进入review_required/attempts_exhausted。本方法不生成verified原件、不改原mutation、不把失败raw存库。非法/缺签名或P Verify失败由公开Record内部形成固定protocol/auth/environment failure再执行相同路径；没有可信异体就不能写query_conflicts。

stale合法签名保存提交后返回LeaseLost；非法stale响应无证据可存，LeaseLost且无状态写。不能用stale签名not_seen授权新一代补发。query read请求可用同ID重试，但预算来自新claim，不绕过20次或创建新supervisor。

## 8. Evidence Process：唯一资金事务与锁图

### 8.1 定位、事实重建与语义review

验证EvidenceLease→普通读processing/evidence，先区分同代终态只读恢复与本代leased执行；后者才要求未过期并只读定位order。**不预先锁processing行。** 同代已terminal且完整原来源可只读解析为processed/existing/review，作为已确认COMMIT的恢复：无资金observation/closed为processed，完整capture图为existing，review取B1的最早固定reason；第20次耗尽但无额外review fact时派生processing_attempts_exhausted。恢复不重写终态或返回credited。不同代或未完整事实不赋予旧lease新资金权；已有来源暂缺不是重试新资金的授权。

终态恢复仍以plain读重建P原件、normalized字段/digest、closed revision、完整capture资金图及review的不可变正事实。unknown_order是根接受的历史例外：原review/evidence/P原件必须完整绑定，原INSERT guard已证明当时不存在订单；021没有absence历史见证，不能用今天新建的同UUID订单推翻该review。此例外永不追溯应用资金，也不重开processing预算。

已知order先NO KEY UPDATE；未知order以unknown_order review结束，不制造tenant；unsupported仅unsupported_effect_review。已存immutable canonical以P严格Parse/Canonical重新检查，并重建全部request/original/evidence关联；这不是重新做过期HMAC认证，也不构造伪Verified。结构/归属损坏为Integrity整体回滚。正常签名但与冻结订单金额/历史窗口矛盾才是相应业务review。

固定判定顺序：未知order→unsupported→已验证原件/环境与原request完整性→金额/CNY与历史半开窗口→provider revision/closed部分序→transaction identity→首selection/既有source→资金。observation只processed，无资金；closed在order根下insert/重读provider revision，相同fact幂等，异体或与已有capture的 `capture_time>=closed_time OR capture_revision>=closed_revision` 矛盾则provider_revision_conflict review；不逆转已到账资金。合法较早capture可在closed、local expiry/cancel、停售/off、creator撤权、tenant暂停后处理。

capture对所有已知closed要求 `captured_at<closed.happened_at AND capture_revision<closed.revision`。同revision必须kind/fact_digest相同，不能按接收时间覆盖。原签名时钟/未来+5s以保存的verified_at/原件为依据，不因处理时钟退行把合法历史改成provider_time_invalid。当前P严格CNY/UTC6下不可达的currency/provider-time分支不得靠伪造原件触发成成功review；扩P另审。

### 8.2 revision、全局identity、selection

在order根下写/重读 `payment_provider_revisions`（PK env/merchant/order/revision）；同key完整同fact才幂等。预分配identity UUID，`INSERT payment_transaction_identities(...,evidence_id,processing_generation) ... ON CONFLICT (environment_id,merchant_id,provider_transaction_id) DO NOTHING`。唯一冲突等待后用**新RC语句**普通读获胜identity，不回锁获胜order；完整比较order/tenant/amount/CNY/captured_at/revision。另一order或异体只给本evidence transaction_conflict review，identity_id留null，避免把别单identity塞进本单review。

同order新合法另一交易可以保留其identity：首selection按order唯一，已选别的identity→duplicate_order_capture review（identity指当前新交易、selection_order_id为本order），不替换selection。相同identity已有source必须重验完整effect/receipt/journal/credited/原audit，返回existing并只完成当前evidence。相同selection已有overflow review则为当前evidence补同样合法review，不自动重开。

新identity及selection都在资金savepoint外；其processing_generation必须来自本次私有lease原值，不能查询“现在generation”给旧caller借新代。021插入guard+deferred source_complete证明新identity以同代processed/review提交，无selection的identity必须有duplicate review；selection无source必须有其selected identity的overflow review。

### 8.3 tenant、资金savepoint与唯一helper

真实tenant `SELECT id ... FOR SHARE`，不检查active/member/user；此时才建资金savepoint。内层预分配capture UUID并写：

```text
payment_captures(id, order_id, identity_id, tenant_id, environment_id,
 merchant_id, provider_transaction_id, amount, wallet_credit_amount, currency,
 captured_at, provider_order_revision, verified_evidence_id, processing_generation)
payment_effects(capture_id, identity_id, environment_id, merchant_id,
 provider_transaction_id, effect_kind='capture', effect_id='capture')
```

字段都从本tx校验后的冻结图派生，不接外部金额；然后仅调用一次：

```go
store.ApplyFundingTx(ctx, tx,
    billing.FundingSourceRef{Kind: billing.FundingSandboxCapture, ID: captureID},
    billing.FundingScope{TenantID: orderTenant, ActorID: "payment-sandbox-worker", CommandID: ""})
```

helper独占AVAILABLE与SANDBOX_FUNDING_CLEARING的初始化/全ID序锁、锁后DB时刻复验、余额checkedAdd、两分录/journal/receipt/一个schema1 credited。Processor不能在helper前另锁一部分account或在其后回取order/user/SKU。成功外层追加唯一 `payment.capture.credited` audit：actor固定、tenant_scope真实tenant、target=capture、request_id=""、diff恰`{"amount":"A"}`；不另发第二credited。

只有直接helper调用返回真实 `*billing.Error{Code:"amount_overflow"}` 才ROLLBACK TO资金savepoint，撤销source/effect/钱/receipt/credited；保留外层identity/selection/父锁，插funding_amount_overflow review并以本代processing_review提交。普通22003、CHECK、权限、audit/credited/CAS故障整tx回滚，不贴overflow标签。合法review写payment_review_facts；**worker不得追加review audit_events**，021只允许其capture.credited审计动作。

最后才执行：

```sql
UPDATE payment_evidence_processing
SET state=$terminal,last_error_code=$fixed_error
WHERE evidence_id=$evidence AND state='leased' AND generation=$generation
  AND lease_until>clock_timestamp();
```

成功/observation/closed/existing→processed、last_error=NULL；语义review→processing_review、last_error=permanent；仅已知暂时性DB错误明确回滚后才可在独立短tx按同lease写失败状态：次数<20为retry_wait/temporary，第20次为processing_review/attempts_exhausted（不得顺带保留新identity或强造语义review事实）。最终CAS零行、Guard丢失或COMMIT未确认都不能报告credited；零行必须**全部外层回滚**，不只rollback savepoint。

### 8.4 完整锁图与guard检查点

```text
短claim： own lease row → COMMIT → 才交工作slot
Prepare/Record：无锁定位 → own order NO KEY UPDATE → 原件/事实 → 最后own outbox CAS
资金：无锁定位 → own order NO KEY UPDATE → revision/全局identity unique → selection
      → tenant SHARE → [funding savepoint → 全accounts ID序 → source/effect/money/receipt/credited]
      → capture audit或合法review → 最后own processing CAS → COMMIT
mutation expire：无锁限量定位 → own order NO KEY UPDATE → own outbox SKIP LOCKED
                → unknown + 唯一query supervisor → COMMIT
query/evidence expire：仅own lease SKIP LOCKED → 合法状态 → COMMIT
```

所有资金/identity相关FK的order KEY SHARE与NO KEY UPDATE兼容；禁止把order升级成UPDATE，禁止全局identity冲突后取另一order。tenant SHARE与登录/人工/券既有锁图相容；账户之后不新取人类授权父锁。Claim/Expire不持lease行跨网络。Guard在开始tx/发送/落结果/COMMIT前检查，失效只清理；SQL generation/lease是最后权威，不把Guard成功缓存当写许可证。

## 9. ExpireLeases：有界、DB时钟与Recovery

每轮固定总候选/更新上限100（不是每种100），不分页清空全部队列；候选按 `(lease_until,kind,id COLLATE C)` 排序，来自leased且lease_until≤DB clock。无锁只拿locator；每个实际回收独立短tx重检state/generation/到期和Guard，忙行SKIP LOCKED。整个调用5s预算，已确认提交的前缀不回滚；中途失败返回安全错误，下轮重读当前状态，不重复退次数。

| 类别 | 到期后状态 | 必须同tx事实 |
| --- | --- | --- |
| mutation，第1–9次 | submission_unknown/timeout | order根后锁own outbox，ensure原唯一query，trigger生成新last_submission_unknown_at |
| mutation，第10次 | submission_unknown/attempts_exhausted | 同上，仍可query确定事实，不第11次claim |
| query，第1–19次 | retry_wait/timeout | 只own outbox，DB backoff，保留current child；有无原snapshot不换ID |
| query，第20次 | review_required/attempts_exhausted | 不创建第21个claim/新supervisor |
| evidence，第1–19次 | retry_wait/temporary | 只own processing，保留原evidence |
| evidence，第20次 | processing_review/attempts_exhausted | 021明确允许无额外review事实的耗尽终态；不为它伪造tenant/交易/资金 |

更新不改generation/attempt_count，由021触发器生成lease=NULL/next/backoff；不能传旧claimed时刻冒充今天。mutation unknown若已经有旧supervisor，只重读且验证同order/original，不重置预算。Recovery的候选只query，其他两类不读改状态/预算/review；恢复资金须退出Recovery并重新受审启动。

## 10. 必须验收与本片边界

实施后必须使用完整当前迁移、专用moni_test随机schema、真实APP/worker/projector LOGIN和P真签名；owner只安装/seed/隔离损坏/清理，不能冒充worker业务成功。不启动main/浏览器的SQL或库测试不算APP运行/通知/网络链路完成。新增DDL不是本稿授权；022 runtime证明需另冻结，021不得重写。

1. 所有opaque零值/跨Processor/错环境/错work/未prepare拒绝；Prepare未知COMMIT不发IO。RecordQuery用旧Work在current指针换代后仍验证exact原child，raw qid不能替换定位；P协议golden保持原样。
2. mutation10/query20/evidence20每次claim后崩溃、未IO到期、并发SKIP LOCKED、唯一supervisor不重开；0due无错误、无预取、两共享网络槽/四证据槽、公平与Recovery query-only。
3. query永久not_seen/in_progress再claim新child、未知沿原ID；第20次可信快照先保存后review，旧not_seen/早于unknown cut/旧mutation generation/cancel intent/已review都不能补发。stale真签名可追加证据但不ACK新代；同qid可信异体conflict不覆盖。
4. 命令运输HTTP401/格式错/回包掉线均不伪装失败付款；captured hint不产生资金，只有通知/query P真验签+原件/evidence处理可进入helper。无query body/重复签名headers/未知key/过期签名没有verified原件。
5. 已知/未知order、unsupported、金额/窗口边界、同revision异体、closed部分序与合法晚到capture；当前停售/off/撤权/tenant暂停不否定原合法事实。存储原件损坏必须Integrity，不凭body再造Verified。
6. 同交易多event/query/多worker并发只一笔钱；跨order同transaction不回锁对方order；同order第二交易保identity+duplicate review；overflow两账户方向保外层identity/selection、资金全部撤销，新event不自动重开。
7. 逐点失败source/effect/journal/receipt/credited/audit/最终CAS、过期/错误generation必须整tx回滚；真40P01/40001仅已确认rollback有限重试，非overflow22003不能review。COMMIT回包丢失未有真实注入时标待验，不能用普通rollback测试冒充。
8. 专门并发阻断mutation expire与RecordMutation的supervisor唯一插入，证明新order-first顺序无环；query/evidence expire不拿业务父锁；与manual/voucher/Reserve/refund/login真实交叉锁验证。来源helper不会在accounts后补取order/user/tenant。
9. 三来源VerifyFunding/VerifyLedger、唯一capture audit、reports-v1、projector无payment源读取仍正确。正常off零worker/零PSP网络和历史off收尾、Guard丢失全停、SIGTERM排空等由根后续真实进程验证。

根于2026-11-01完整审读后已明确接受：§1的RecordQuery目标修订与mutation-expiry锁序、§2私有Response/Work与query失败helper、§3内部错误及最多两次额外已确认回滚重试、§5所有运输不确定mutation统一unknown的首版范围、§9总100与Recovery query-only；§6已持久completed child的无需IO路径以确认COMMIT后LeaseLost非fatal返回。**RecordQuery(ctx,QueryWork,headers,raw)是下一片唯一目标签名，B1旧QueryLease签名不再实现。**

stage实现接缝审读中根另接受私有sendQuery追加第四个error返回，将本地生命周期/契约失败与远端transportFailure明确分开；无生产caller兼容负担，P与公开RecordQuery不变。

runtime的Mode/Options/Guard/构造/OpenProcessor及同package私有helper所有权现已由根冻结，022证明的DDL/角色升级由根负责；本片只在`.local/b3-payments-stage`独占processor文件构建候选。生产推广/Runner/main/HTTP仍由根单独审定与实施，由根统一更新系统架构、能力状态、changes/manifest并执行reviewed refresh/check；本片不自行刷新共享文档。

## 11. 隔离stage实现与专项证据

2026-11-01：stage的六份processor生产候选文件及五份processor测试文件完成。专用moni_test、随机schema、完整001–022迁移与真实APP/worker/projector LOGIN下，`go test -race -p 2 ./.local/b3-payments-stage -run '^TestProcessor' -count=1 -v`的23组测试通过（17.469s），`go vet -p 2 ./.local/b3-payments-stage`通过。

覆盖真P通知/query签名、唯一资金图、同代恢复及终态损坏拒绝、unknown child复用与stale capture、可信异体首快照、10/20/20预算、混合102条到期仅总100回收、mutation expiry/Record并发锁图、最终CAS整图回滚、真实APP双人核款MaxInt64后的overflow保identity/selection、跨单交易冲突、2.1s锁等待本地DeadlineExceeded不fence，以及真实受控HTTP的capture hint零入账/拒redirect/Close取消join。撤schema所有PUBLIC函数EXECUTE后，worker只补legacy `funding_uuid(text)`仍完整入账，`pb_now()`真实拒绝。

这些Processor专项使用明确标注的包内runtime受审装配，只证明SQL/P/资金/adapter；不冒充完整NewProcessor/attestation构造证明。runtime分片另有真实八角色、双manifest与完整022构造专项，由其负责人与根独立记录。主进程/浏览器/真实PSP/生产推广、未知COMMIT真实故障注入仍未由本片验收。

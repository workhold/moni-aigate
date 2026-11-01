# 022-A 入账与兑换券执行合同（审查修订稿）

2026-11-01，**A0/A1已实现并通过限定本地验收；B与C未冻结、未实现**。A依[A执行合同](a-contract.md)、[领域合同](a-domain-contract.md)完成共同入账、双审券、HTTP和控制台，证据见[验证记录](../../docs/verification/funding-vouchers.md)。在线支付沙箱、来源lot与原路退款继续独立审查，不能将A验收视作022整体完成。

## 1. 本片选择

- A 的资金与券实现放在 `internal/billing`；HTTP 不写 ledger。共同 helper 接收既有 `pgx.Tx`、source kind/ID及已验证的内部 actor/scope，不接收调用者自报可信金额，不自开事务、不持锁网络请求。B 再设计 `internal/commerce` 适配边界，避免 A 的循环包依赖。
- `manual_credit` 的 source_id 是 credit order ID；`voucher_grant` 的 source_id 是 voucher ID。voucher_id 对应唯一 redemption，再关联 receipt/journal。A 不允许 `sandbox_capture` 写入；B 通过追加迁移和独立验收才启用该类型。
- `credited` 仍表示累计钱包入账。A 的来源分类直接读取不可变 receipts，同步给出安全摘要；不新增会重复累加旧 credited 的分类 outbox/consumer，也不扩大 projector 的 source 读取权限。
- env 为券能力硬上限，DB policy 为管理员动态开关；两者默认 false。兑换最终时间判定在全部钱包账户锁之后。作废/撤销及旧回执查询不受正常功能 off 阻断。
- A 不引入 lot、现金退款、支付回调或支付商；voucher/manual receipt 均不赋予自动原路现金退款资格。

## 2. 严格值与有界 HTTP

所有新增 A 写 body≤8KiB，必须为单个 UTF-8 JSON object；拒未知、重复、大小写别名、尾随 JSON、非法 UTF-8和控制字符。必要字段不得遗漏/null。路径/body UUID 均是小写、非 nil、标准连字符格式；不修剪 token/UUID。只注册合同列出的方法，其他方法沿 API 404。

金额/revision/计数为规范十进制字符串；face_amount正且≤BIGINT，revision正，redeemed_count非负。count仅接受无小数/指数/符号的 JSON 整数1..100；先用大整数计算 face_amount×count≤BIGINT。reason为1..200 UTF-8字节且无控制字符，purpose_ref使用`security.ValidReference`且≤256字节，不能是URL/秘密/任意凭证正文。

face_amount×count的静态溢出是400 `invalid_request`且不claim command；仅锁住实际账户后发现余额相加/清算相减超BIGINT才是持久`funding_amount_overflow`业务拒绝，不能把输入格式/数学上限错误伪装成已保存命令。

expires_at仅允许UTC `Z` RFC3339，0..6位小数，拒offset、闰秒和多于微秒精度；摘要/响应统一规范化为6位微秒的UTC值。创建取得业务所需锁后以独立DB时钟校验`created_at+1h <= expires_at <= created_at+365d`，created_at用同一时刻；不使用浏览器时间。

券字符串精确85 ASCII字节：`mav1.`+36字节规范UUID+`.`+43字节无padding base64url；后段必须解码为32字节且重编码相同。拒空白、另一种base64编码、缺段/多段和字符串修剪后的“兼容”。语法错误400，不建立命令；语法合法但秘密不匹配/不可兑时按业务拒绝保存命令，统一`voucher_unavailable`。

全部新增响应`Cache-Control:no-store`；除创建获胜响应外所有DTO/错误均不含code、digest、fingerprint、密钥版本/内容或内部SQL。HTTP错误不反射输入。列表固定50、读51决定next_cursor，不接受limit/offset；唯一查询为可选cursor，以及funding receipt列表的`source_kind=manual_credit|voucher_grant`。raw query≤4096，完整ParseQuery并检查错误，拒未知/重复/坏UTF-8。cursor为≤1024字节canonical raw base64url、解码≤768、严格`{v:1,created_at,id,filter}`，按`created_at DESC,id COLLATE "C" DESC`；filter为规范scope+kind+筛选SHA-256，不能换scope/筛选继续页，始终重验当前权限。

有界变更准入在session/CSRF/MFA/再认证之后、创建业务事务之前：独立A全局20rps/burst40；兑换按当前user+tenant为2rps/burst4，平台批次变更按user为1rps/burst2；至多4096个actor/scope/kind桶，10min惰性驱逐、满表新桶429。至多4个A变更在途、不排队；5秒总context包含连接池/锁/重试/提交，失败取消用once归还。429带`Retry-After:1`，无命令。不得挤占原登录/收费/public bucket；A私有认证前的既有metadata Guard不因本片改变。

## 3. 动态开关与权限同步点

配置`VOUCHERS_ENABLED=false`是进程硬上限；`voucher_policy(singleton=true,enabled=false,revision=1)`是数据库动态状态，revision由trigger仅在enabled实际变化时递增、不能直接伪造。有效值为env && policy.enabled；RecoveryMode/ready/Guard仍是独立更强门禁。

| API | 精确合同 |
| --- | --- |
| GET `/api/platform/voucher-policy` | 当前active admin；`data:{enabled,revision,effective_enabled,runtime_allowed}`；revision字符串，无需近期再认证 |
| GET `/api/platform/commerce-capabilities` | 当前active admin/finance_operator/finance_approver；只读`data:{vouchers_enabled:boolean,payments_mode:"off"}`，不要求近期再认证；DB失败503 |
| PUT `/api/platform/voucher-policy` | 当前active admin，CSRF/近期再认证/可选MFA/恢复门禁；严格`{expected_revision:string,enabled:boolean}`；事务user SHARE→policy UPDATE，锁后新语句CAS，旧rev409 `voucher_policy_conflict`，同值no-op不审计；实际变更`voucher.policy.update`同事务 |
| GET `/api/wallet/commerce-capabilities` | 当前有效owner/billing_viewer；`data:{vouchers_enabled:boolean,payments_mode:"off"}`；DB失败503，不返回假off |

env=false时可GET或设置DB enabled=false；尝试设置true返回409 `vouchers_runtime_disabled`，不改policy。env=false对新发行/兑换也在claim前409拒绝、不建command，不要求缺省环境具备指纹密钥；env=true而DB policy=false才按§4保存`vouchers_disabled`业务拒绝。PUT沿020的未知写模式：只GET核查，不自动PUT，同值不证明原请求归因。

兑换/新发行在业务根锁后取policy SHARE并持有到提交；disable policy UPDATE若先提交则新命令拒绝，否则等待已准入事务完成。新提案和新兑换受off拒绝；已存在proposed批次可以审批，void/revoke可以继续，但recovery拒绝所有这些写。一次提交不因进程env后来切换追溯改回执；env改动需原有排空重启流程。

所有租户写在transaction内锁tenant SHARE→membership SHARE→user SHARE，再新语句检查三者active且membership.role=owner。不能只信session缓存，不能用平台finance身份替代owner。租户读允许当前active owner/billing_viewer；member拒绝。财务命令锁当前maker/checker user SHARE并重查active及finance_operator/finance_approver，二者不同；admin不能代替任一财务角色。历史active券在maker之后离职时仍保留既有授权，除非正常作废/到期，不在每次兑换重新否定已批准批次。

## 4. 命令与已知拒绝的提交矩阵

`commerce_commands`一行对应全局唯一operation UUID。scope精确为`platform`或`tenant:<tenant UUID>`，kind在A为`voucher.batch.create|voucher.batch.approve|voucher.batch.void|voucher.revocation.create|voucher.revocation.approve|voucher.redeem`。保存actor、scope、kind、request_digest、fingerprint_key_version（仅兑换）、created_at；这些身份不可更新。全局operation唯一约束同时防同UUID跨kind/scope复用；原租户GET其他scope统一404。

安全OperationReceipt精确字段：`operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at`。status只`accepted|rejected`，reason为受控枚举字符串；无资源/金额/关联时为null，非空amount是正规范字符串。平台scope为字符串`platform`，租户scope的DTO仅返回本租户UUID；不返回digest/原body。同一command恰好一个终态receipt，复合FK绑定operation/kind/scope/actor，创建同事务提交并不可改；deferred constraint禁止孤立command或异体terminal提交。

正常HTTP鉴权/语法门禁通过后事务先claim command。冲突时用原command的key版本重新算摘要，actor/scope/kind/body相同则在检查当前读权限后只返回原receipt；异体409 `commerce_operation_conflict`，绝不执行第二次。当前权限丧失返回403或跨scope404，不能借旧receipt绕权限；返回原receipt无需当前再次满足新提案开关/批次未过期等写前提，原终态永不改写。

| 结果 | command/receipt与资金 | HTTP/code |
| --- | --- | --- |
| 语法、Cookie/CSRF/MFA/近期再认证/初步角色拒绝、限流、metadata ready/Guard/recovery拒绝、env硬开关拒绝 | 不claim，无持久command | 原400/401/403/429/503安全错误；env拒绝409 `vouchers_runtime_disabled` |
| claim后transaction内当前角色/tenant撤销 | 整体回滚，无command；不向失权actor持久暴露资源事实 | 403 `forbidden` |
| 非redeem的路径batch/proposal不存在或不属于可读范围 | 整体回滚，无command；redeem仍用不可枚举voucher_unavailable | 404 `not_found` |
| 同ID同摘要已提交 | 无新资金，读原accepted/rejected | 200 +原receipt；无codes |
| 同ID异体/异scope/kind/actor | 不新建/覆盖 | 409 `commerce_operation_conflict` |
| 语法合法的无效/过期/撤销/已兑券；off；旧CAS或stale evidence；不允许的批次状态；钱包/清算账户溢出 | 持久rejected receipt；无资金/核销/业务audit/outbox | 200 +receipt.status=rejected；reason见下 |
| 财务提案/状态变更或兑换成功 | 同事务accepted +所需业务事实/审计；仅兑换有journal/receipt/credited | 首次batch create为201并带codes；其他200 |
| audit/outbox/constraint/连接/statement/lock故障、取消、deadline；已知rollback | 整体回滚，不留command；不得声称已持久rejected | 503 `commerce_unavailable`；不以普通5xx宣称not_committed |
| COMMIT结果未知 | 不新发事务/ID，不返回一次性codes；只GET原operation | 503 `commerce_outcome_unknown`，UI保留原ID |

受控rejected reason：`voucher_unavailable|vouchers_disabled|voucher_revision_conflict|voucher_evidence_stale|voucher_state_conflict|funding_amount_overflow`。语义拒绝统一HTTP200的安全receipt，HTTP层不得把200当accepted；此处替代总合同此前笼统的“业务拒绝409”。配置PUT和operation异体仍用上表409。内部基础设施错误不能混入reason。

OperationReceipt的accepted `reason`统一固定`accepted`，不使用null或空串。安全关联按下表确定；amount非null时为正规范金额字符串。全部rejected（包括非voucher_unavailable）都将resource_id、amount、funding_receipt_id、journal_id四字段置null，不能用拒绝回执额外泄漏源或胜者；status/reason与原command安全身份仍保留。

| kind / status | resource_id | amount / funding_receipt_id / journal_id |
| --- | --- | --- |
| batch.create / approve / void accepted | 对应batch ID | 三者均null |
| revocation.create / approve accepted | 对应proposal ID | 三者均null |
| voucher.redeem accepted | 本次redemption ID | 可信面值 / funding receipt ID / journal ID，三者必填 |
| 任意kind rejected | null | 三者均null |

`voucher_unavailable`在不存在、秘密错误、过期、撤销及任意租户已兑之间保持同一安全形状：resource_id/amount/funding_receipt_id/journal_id全部null，不返回原租户或获胜redemption。语法合法的新operation即使本租户曾兑过该券，也按该形状拒绝；只有原operation同摘要重读能得到自己的原accepted。

实现采用command claim外层事务+business savepoint：需要保存rejected的业务分支ROLLBACK TO savepoint，消除可能创建的业务行/资金，再插入rejected terminal。唯一额外保留的业务事实是revocation approve因证据stale产生的不可变rejected decision：回滚资金/批次变化后，在同外层tx写decision+rejected terminal；仍无资金、核销、业务audit或outbox。proposal已decision的新operation不产生第二份decision。基础设施错误直接回滚外层，receipt或decision写失败也回滚全部。命令只在提交后对外可见。已知40P01/40001可沿原transaction wrapper最多重试两次；每次closure重置所有结果/secret内存，COMMIT未知绝不重试。客户端不自动重试任何写，即使GET404也不能认定未提交。

## 5. 批次、核销和作废状态机

BatchView精确：`id,state,face_amount,count,expires_at,purpose_ref,reason,maker,approver,activated_at,terminal_actor,terminal_at,revision,redeemed_count,remaining_count,created_at,expired,observed_at`；count是JSON整数，expired是boolean，其余数量/revision/金额为字符串，可空时间/actor为null。revision初始1，只随state实际变化+1；兑换不改revision。redeemed_count初始0，每次唯一redemption+1且≤count，remaining=count-redeemed；同一SQL数据库observed_at计算expired，不使用浏览器改state。到期是资格判断，不后台改写state。

| 命令 | 根锁内前提 | 原子结果 |
| --- | --- | --- |
| create | operator active，开关有效，数量/金额/DB时间范围有效 | proposed/revision1/兑数0；保存count张不可变券摘要；accepted；只有获胜创建响应有codes |
| approve | batch proposed，expected_revision匹配，maker/checker有效且不同，锁后DB now<expires_at | active，revision+1，冻结approver/activated_at；审计，不入账 |
| void | batch proposed，expected_revision匹配，actor为仍有效的原maker | voided，revision+1，冻结terminal actor/time；未兑，无钱 |
| revocation create | batch active，operator有效，expected_revision和expected_redeemed_count均匹配 | 不变batch状态；追加不可变proposal，冻结batch revision/count/reason；不冻结券兑换 |
| revocation approve | 先batch锁，proposal尚无decision；maker/checker当前有效且不同；batch active且revision/count仍匹配 | revoked，revision+1；追加accepted decision及audit；既有兑款不撤回 |
| revocation approve遇到revision/count/state变化 | 旧proposal保持原body，追加rejected decision `voucher_evidence_stale`及rejected operation | 旧proposal终态不能重批；operator可显式新proposal，不改旧证据 |
| redeem | batch active、有效开关、唯一券秘密正确、未兑、有效owner；全部账户锁后DB now<expires_at | 唯一redemption、兑数+1、journal/receipt/credited/audit/accepted command；已到账不设到期 |

`voucher_revocations`不可变proposal，`voucher_revocation_decisions`不可变且UNIQUE(proposal_id)，不依赖可任意改的proposal.state。重读同approval operation返回原receipt；新operation对已decision的proposal持久`voucher_state_conflict`拒绝，不重复decision。revocation创建本身不递增batch revision，多个proposal允许但只有符合当前快照的一个可生效；其余审批变stale。proposed/active到期后仍允许原maker void/双审revoke做运营关闭，不能再activate/redeem。

BatchView的maker始终非null；proposed的approver/activated_at/terminal_actor/terminal_at均null；active的approver/activated_at必填且terminal两字段null；voided的审批两字段null、terminal两字段必填且terminal_actor=maker；revoked保留原审批两字段、terminal两字段必填且terminal_actor为作废审批人。创建reason不可覆盖；proposed void理由在其不可变业务audit中，active批次双审revoke理由从不可变proposal读取，不能用batch.reason冒充后来的作废证据。

审批人必须能刷新或跨设备发现同一份证据：新增GET `/api/platform/voucher-batches/{id}/revocations`和GET `/api/platform/voucher-revocations/{id}`，当前active admin/finance角色只读。RevocationView精确为`{id,batch_id,expected_revision,expected_redeemed_count,reason,maker,created_at,decision}`；decision未审批为null，否则为`{id,operation_id,status,reason,approver,decided_at}`，status是accepted/rejected，reason分别是accepted/voucher_evidence_stale；revision/count均规范字符串。审批POST `/api/platform/voucher-revocations/{id}/approve`的body仅`{operation_id}`，审批既有proposal，不允许重新传reason/revision/count替换证据。已decision的新operation只保存拒绝回执，不新增decision。

所有列表（包括既有本片batch、receipt和新增proposal列表）统一`{data:[SafeView],next_cursor:string|null,request_id}`，单GET统一`{data:SafeView,request_id}`；不嵌套另一个items/data，不输出摘要。均按§2固定分页，proposal列表cursor绑定batch筛选与当前平台读权限。

voucher行不可UPDATE/DELETE：`id,batch_id,code_digest,digest_key_version,created_at`；无可更新的used标记。核销事实由全局`UNIQUE(voucher_id)`的append-only redemption决定。redemption含`id,tenant_id,voucher_id,batch_id,operation_id,actor,amount,journal_id,funding_receipt_id,created_at`；复合FK保证voucher属于batch、journal/receipt属于tenant，deferred核验保证amount=face、journal/receipt/source相互匹配且提交完整。batch兑数由受控trigger增量更新，提交时与COUNT(redemptions)一致；不能由app直接任意赋值。

## 6. 密钥、指纹与一次展示

新增独立外部`VOUCHER_HMAC_KEYS` keyring（UTF-8 JSON object，键为版本ID、值为密钥编码）和`VOUCHER_HMAC_ACTIVE_KEY`，不能复用AUTH_PEPPER或上游AES加密密钥。key ID为1..32 ASCII字母数字/下划线/短横线，key恰32随机字节、配置值canonical无padding base64url；keyring最多128版本，拒重复key/未知非字符串值。env=true时启动验证active及DB引用的所有历史code/fingerprint版本齐全，否则拒绝启用；env=false可保留只读服务与关闭policy，不允许发行/兑换。DB只存版本ID和digest，不存key。

code digest：HMAC-SHA256(key[version], `moni-aigate:voucher-code:v1` +单NUL+canonical JSON `{voucher_id,secret}`)；secret是该32字节的canonical base64url。兑换命令fingerprint用另一个域`moni-aigate:voucher-command:v1`，同样覆盖voucher_id/secret，command保存其fingerprint_key_version。安全request_digest为SHA-256规范对象`{v:1,actor,scope,kind,target_id,body}`；target_id由路由确定，batch approve/void及revocation create取batch ID，revocation approve取proposal ID，batch create/redeem为null，不能由body另传。redeem body只含`{voucher_fingerprint}`，其余命令body是规范安全字段（不含operation_id），时间先归一、金额和revision保字符串。同operation相同body但不同路径目标必须409，不能读到另一资源的原accepted。

新命令用当前active指纹key；同operation重读先取command原版本重算，不依赖voucher是否存在。code校验按voucher行自身digest_key_version，常量时间比较；轮换不改变原摘要、不新发券。已兑/过期券的指纹key仍须按财务保留期保留，以支持旧命令核查。缺失所需key是503 `voucher_key_unavailable`并整体回滚，不伪装成已兑/无效、不静默切active key。

所有mutation的data统一为OperationReceipt。首个提交成功的batch-create获胜调用返回`{data:OperationReceipt,batch:BatchView,codes:[{voucher_id,code}],secrets_available:true,request_id}`；accepted重放创建返回`{data:OperationReceipt,batch:BatchView,secrets_available:false,request_id}`，根本不含codes；rejected创建返回`{data:OperationReceipt,secrets_available:false,request_id}`，无batch/codes。batch是当前安全视图，原data终态不可变；其他mutation返回`{data:OperationReceipt,request_id}`。生成secret留在当前closure内存，只有最终已确认COMMIT的获胜attempt可返回；panic/error/retry/取消/unknown不能回显失败attempt的secret。GET batch及列表仅BatchView；GET operation只OperationReceipt，无secret。

浏览器不使用通用持久command journal保存发行/兑换body；仅持久原operation及安全kind/scope/resource ID/时间，券只在内存。关闭、卸载、身份epoch变化或退出即清除；不可自动下载。一次性响应丢失按原批次void/双审revoke后显式新发行，禁止恢复秘密或自动重发。

## 7. 共同 Funding 的唯一资金所有者

共同helper采用§12的确切待审Go签名`ApplyFundingTx`：输入已打开tx、仅含kind/id的source、内部actor/scope，返回安全receipt及redemption引用；没有amount、balance、paid或trusted boolean参数。调用方先持业务根和授权父锁；helper不得回锁command、batch/credit、tenant、membership或user，不调用外部HTTP，不COMMIT。

helper职责：在既有根锁保证稳定的前提下重读可信source和原审批事实→确定所需全部account kinds→一次按全局account ID取锁→新语句取DB时刻并复验source资格/expiry/金额→postLocked平衡journal→完成source与journal绑定/券唯一核销及batch计数→建立唯一不可变funding receipt→一个稳定credited outbox。业务audit和terminal command由外层同tx写；manual原审批audit保留一次，不双写credited。任何失败由§4控制全体回滚/保存无资金拒绝。

manual source由原`ApproveCredit`锁住credit与双方财务身份后进入helper，保持其旧financial_idempotency/重复审批语义；helper只允许pending的首次合法审批产生钱，approved重读验证原journal/receipt而不补发资金。批准状态/journal/approver/receipt必须在同tx完成，不能提交中间approved+null journal。历史approved缺receipt由迁移解决，不在普通API自动补历史。

voucher source是锁住的voucher+active batch，金额由batch.face_amount取得；tenant/actor来自锁内owner。内部生成redemption UUID作为journal.operation_id，journal.kind=`voucher_grant`、reference=voucher_id，receipt.source_id=voucher_id；不使用客户端operation UUID当资金source身份。manual沿原journal.operation_id/reference=credit ID，kind=`credit`。outbox operation沿各资金source的稳定journal.operation_id，kind=`credited`，payload至少`{amount}`；新事件可增加白名单source/schema，但旧Project可重放，A不发送第二种会再次增加credited的事件。

分录：manual `AVAILABLE +A / FUNDING_CLEARING -A`；voucher `AVAILABLE +A / OPERATING_GRANT_CLEARING -A`。新赠额清算是平台tenant_id=NULL的BIGINT账户，允许非正，不与现金清算合并；DB check、account allowlist、账户初始化/排序锁、输出及Verify同时更新。客户AVAILABLE与任何清算账户溢出都拒绝，不截断、不NUMERIC强转回BIGINT。

## 8. 来源、journal与DB保护

receipt精确事实：`id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at`；A不添加空sandbox环境字段。amount>0、currency=CNY；UNIQUE(source_kind,source_id)、UNIQUE(journal_id)、UNIQUE(tenant_id,id)。journal追加`UNIQUE(tenant_id,id)`，receipt用tenant复合FK。customer ReceiptView只这些安全字段；不复制manual proof_ref、maker理由、code/fingerprint。

必须有提交时一致性约束，验证manual source→approved credit、voucher source→该voucher的唯一redemption；tenant/amount/币种/journal匹配；journal operation/reference/kind正确；恰好两条指定科目分录、AVAILABLE+A和正确clearing-A且不混其他科目；voucher redemption还须绑定同batch、相同operation receipt和funding receipt。receipt/source/journal任何一个缺失均不能提交；只验证journal平衡或SUM总额相等不足。

credit追加保护：id/tenant/business_ref/amount/requester/proof_ref/created_at不可改；只允许pending→approved一次；pending时approver/journal为空，approved时两者必填且不同于requester；terminal不可UPDATE/DELETE。状态/approval/journal在提交完整并与manual receipt匹配，旧历史先回填再启用保护。未使用已应用SQL修改历史，任何坏source都阻止升级。

app对commands、terminal receipts、vouchers/redemptions、proposal/decision、funding receipts无UPDATE/DELETE；仅batch受控状态/计数列、policy.enabled等白名单可UPDATE，trigger防伪revision、逆向state和跨归属。所有新增历史有immutable trigger，不仅靠GRANT。migration owner独立；projector仍仅outbox/自身projection，不授予funding/券/command读取或资金写权。普通app仍是业务可信执行者，行约束不宣称能抵抗整个app/DB账号被攻陷。

## 9. 排空迁移、投影与核验

升级为维护操作：停接单/排空旧HTTP和上游→停止worker/projector/旧进程→取得服务实例锁→owner追加迁移及provision→只读检查→受限serve。迁移串行锁不能代替服务锁，普通serve不自动回填。

同一追加迁移事务先验证每条历史approved credit：有且仅有同tenant的合法credit journal、operation/reference=credit ID、金额/币种/两条分录正确、approver/maker形状完整；每笔credited outbox恰一条且tenant/operation/amount正确。pending无journal/approver/credited。发现重复journal、缺事件、金额错绑或孤立credit资金journal立即中止；禁止ON CONFLICT忽略、生成新journal/credited、修改原outbox或“修平”余额。

验证通过后为每条approved credit回填同tenant/amount/journal的manual receipt；created_at沿原journal.created_at，ID可新生成但source和journal唯一固定；不回填pending、不新增projection事件。接入新ApproveCredit后每笔新人工和券资金有一份receipt。即使功能off，人工receipt路径和旧报表仍正常工作。

A保留原`reports-v1`和旧credited总量行为；无历史重发事件、无新增分类consumer。`Rebuild`在原独占维护条件下改为全部receipt SUM核验credited，并同时验证source/receipt/journal覆盖与关联；charge/refund/release/request旧核验保持。构建inactive consumer，所有检查通过后原子切换active指针；失败保持旧active，不能为新来源绕过核验。普通Project仍只读outbox，不joinreceipt，不造资金。

新增同步只读来源摘要：GET `/api/wallet/funding-summary`允许owner/billing_viewer，`data:{currency:"CNY",total_amount,manual_credit_amount,voucher_grant_amount,as_of}`，三金额用NUMERIC规范字符串，single SQL/single DB clock，total=两类之和；这是receipt累计，不是可用余额或真实现金收入。GET `/api/wallet/funding-receipts`按§2分页。A不改原Report DTO；原credited可能有投影延迟，页面分开标识receipt观察时刻，不能用同步摘要声称异步总报表同时刷新。

新增独立FundingVerify（供check-ledger/维护Rebuild复用，不只改单个SUM）：source↔receipt双向覆盖、journal唯一/同租户/金额/科目完整性、voucher↔redemption↔count/terminal operation绑定；所有source累计=receipts累计=credit/grant类journal AVAILABLE正增量；各清算账户delta累计与对应journal/receipt一致；原账户余额与全journal_lines、平衡/settlement/refund核验保持。故意交换两条同额source归属也必须失败，不能被总额抵消。

## 10. 按实际代码冻结的A锁图

| 路径 | 顺序 |
| --- | --- |
| 旧ApproveCredit | credit UPDATE→checker/maker user SHARE→tenant SHARE→accounts ID序UPDATE；A只在其原tx插入Funding事实，不能将source根移到账户之后 |
| 旧Reserve | tenant SHARE→membership SHARE→user SHARE→key UPDATE→model SHARE→accounts ID序UPDATE |
| 旧lockRequest/Settle/Release | tenant SHARE→membership SHARE→key UPDATE→model SHARE→可选channel SHARE→reservation UPDATE→必要model升级→accounts ID序UPDATE |
| 旧ApproveRefund | refund UPDATE→财务user SHARE→上述lockRequest父锁/reservation→settlement UPDATE→accounts |
| A redeem | command claim→非锁定voucher查batch ID→batch UPDATE→不可变voucher新语句读取→policy SHARE→tenant SHARE→membership SHARE→user SHARE→全部accounts ID序UPDATE→最终DB时刻→source/journal/receipt/outbox/terminal |
| A batch approve/void | command→batch UPDATE→当前所需财务user SHARE→新语句DB时间/状态CAS→状态/audit/terminal；无账户 |
| A revocation create/approve | command→batch UPDATE→不可变proposal读取→当前财务user SHARE→计数/revision新语句核验→proposal或decision/state/audit/terminal；不先锁proposal再回batch |
| A create | command→policy SHARE→operator user SHARE→新语句DB时刻→新batch/voucher/audit/terminal；无账户 |
| policy PUT | admin user SHARE→policy UPDATE→新语句CAS→audit；不再锁batch/tenant/account |

用户/tenant的FK隐含KEY SHARE不替代上述显式授权锁和锁后复验；它与非key状态更新相容。所有voucher业务的根锁为batch，不先voucher row UPDATE后回batch；voucher行本身不可变，无used列。所有账户一次按实际ID锁齐，不先AVAILABLE后临时加清算科目。账户后不得回锁source/tenant/auth父行，helper仅在已有父锁下读取证明；所有审计/outbox/新receipt FK只引用已知父身份。

上述是A目标锁合同，尚需真实PG等待/死锁/撤权/到期验证，不宣称已被证明。B/C必须重新合并payment-order/lot/债务图，不能把“业务根→reservation→tenant”的旧草案概括用于实际实现。Recover的job claim先独立提交再Settle；Rebuild为独占维护，不与业务在线交错。

## 11. 冻结出口

根集成人需接受/修订的产品取舍：env+DB双开关、A业务拒绝HTTP200终态receipt、source_id采用voucher ID、A同步分类而保留原reports-v1、双HMAC用途域及独立keyring、固定分页与有界限流。根集成人已接受上述产品选择及§12的helper签名/DDL/角色清单。A0实现并通过对应真实PG专项后，再允许A1后端/HTTP/UI并行。其余B/C仍保留待审，不因A冻结自动获实现授权。

## 12. A0 账务接口与追加 DDL

本节是2026-11-01按现有`orders.go/store.go/query.go/rebuild.go`校准且经根集成人审查冻结的A0实施合同，**尚未编码**；不因接口冻结直接授权A1。OperationReceipt逐kind矩阵、BatchView状态空值和revocation读接口见§3–5，已与本节一道完成根集成人A0审查并冻结。

### 12.1 共同入账签名

```go
type FundingSourceKind string
const (
    FundingManualCredit FundingSourceKind = "manual_credit"
    FundingVoucherGrant FundingSourceKind = "voucher_grant"
)
type FundingSourceRef struct {
    Kind FundingSourceKind
    ID   string
}
type FundingScope struct {
    TenantID  string
    ActorID   string
    CommandID string // manual必须空；voucher为已claim的voucher.redeem operation
}
type FundingReceipt struct {
    ID, TenantID string
    SourceKind   FundingSourceKind
    SourceID     string
    Amount       Money
    Currency     string
    JournalID    string
    CreatedAt    time.Time
}
type FundingResult struct {
    Receipt      FundingReceipt
    RedemptionID string // manual为空
    Applied      bool   // 仅本次实际新增资金为true，不直接序列化为HTTP DTO
}
func (s *Store) ApplyFundingTx(ctx context.Context, tx pgx.Tx,
    source FundingSourceRef, scope FundingScope) (FundingResult, error)

type FundingCheck struct {
    Balanced bool
    MissingReceipts, OrphanReceipts, SourceMismatches int64
    JournalMismatches, OutboxMismatches                int64
    RedemptionMismatches, BatchCountMismatches         int64
    ClearingMismatches                               int64
}
func (s *Store) VerifyFunding(ctx context.Context, tenantID string) (FundingCheck, error)
```

代码属于`internal/billing`；这些内部类型不接受JSON解码。`ApplyFundingTx`不Begin/Commit，不重试，不调用网络，错误返回零值结果。manual须外层已持credit UPDATE、当前maker/checker user SHARE及tenant SHARE；voucher须外层已claim command并持batch UPDATE、policy SHARE、tenant/membership/user SHARE。helper再次普通读取source、审批与scope/command绑定，锁外层已保护的事实，不申请父锁；缺少父锁是调用方违反合同，不能靠调用者传一个trusted标记免验。

helper确定`AVAILABLE`及对应唯一clearing，调用一次`lockAccounts`按实际account ID锁齐；锁后新语句取DB时刻并重读source/expiry/金额，再`postLocked`。manual的journal operation/reference均为credit ID，actor为当前checker；voucher内部生成redemption UUID作为journal operation，reference=voucher ID，actor为当前owner。然后同tx完成credit approved或redemption+batch计数、funding receipt及一个`credited`事件；外层只写业务audit和terminal command，不重复emit。新事件保留schema1及`{"amount":"规范正整数"}`形状，不加第二类累加事件。

已approved manual只重读并验证旧receipt/journal/source，返回`Applied=false`，不改原approver或补事件；历史缺receipt是迁移失败，不由API补钱。voucher新command遇已有redemption统一业务拒绝，不能返回其他command的成功；同operation重读在command层结束，避免再次调用入账。原`amount_overflow`在voucher savepoint边界映射为`funding_amount_overflow`业务拒绝；manual仍保留原错误合同。SQL、约束、审计或outbox失败不伪装成金额拒绝。

`VerifyFunding`使用独立只读REPEATABLE READ事务；空tenantID核验全库，非空先验合法scope。租户范围只比较其source/journal对应的clearing分录，不能拿一个租户的subtotal与平台全账户余额比较。内部`verifyFundingTx(ctx,tx,tenantID)`复用相同查询，供维护Rebuild现有事务调用，不另开快照。其结果是来源完整性；原账户余额/全journal平衡检查仍必须执行，不能用它替换`VerifyLedger`。

### 12.2 017 追加表与约束

拟追加`017_funding_vouchers.sql`，不修改001–016。新业务ID均用`TEXT DEFAULT gen_random_uuid()::text`并CHECK小写、非nil、标准UUID；这样与既有TEXT父键一致。`source_id TEXT`保留历史credit ID原值，不通过UUID强转丢弃合法旧历史。以下时间均TIMESTAMPTZ、默认DB clock，金额BIGINT且正，revision为BIGINT且正；摘要64位小写hex，key版本遵§6。

| 表 | 确切字段与主要唯一性 |
| --- | --- |
| `funding_receipts` | id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at；UNIQUE(source_kind,source_id)、UNIQUE(journal_id)、UNIQUE(tenant_id,id)；kind仅manual_credit/voucher_grant，currency=CNY |
| `voucher_policy` | singleton BOOLEAN PK CHECK true,enabled BOOLEAN=false,revision BIGINT=1；迁移seed唯一行 |
| `commerce_commands` | operation_id PK,actor TEXT FK users,scope,kind,request_digest,fingerprint_key_version NULL,created_at；UNIQUE(operation_id,kind,scope,actor)；仅redeem要求指纹版本，其他必须NULL |
| `commerce_operation_receipts` | operation_id PK,kind,scope,actor,status,reason,resource_id NULL,amount NULL,funding_receipt_id NULL,journal_id NULL,created_at；复合FK(operation_id,kind,scope,actor)绑定command；reason及空值按最终逐kind矩阵 |
| `voucher_batches` | id,state,face_amount,count INTEGER,expires_at,purpose_ref,reason,maker FK users,approver NULL FK users,activated_at NULL,terminal_actor NULL FK users,terminal_at NULL,revision=1,redeemed_count BIGINT=0,created_at；count1..100、0<=兑数<=count、face×count<=BIGINT |
| `vouchers` | id,batch_id FK batch,code_digest,digest_key_version,created_at；UNIQUE(id,batch_id)，无secret/used/state列 |
| `voucher_revocations` | id,batch_id FK batch,operation_id UNIQUE FK command,maker FK users,expected_revision,expected_redeemed_count,reason,created_at；UNIQUE(id,batch_id) |
| `voucher_revocation_decisions` | id,proposal_id UNIQUE,batch_id,operation_id UNIQUE FK command,approver FK users,status,reason,decided_at；FK(proposal_id,batch_id)绑定proposal；不可变accepted/rejected决定 |
| `voucher_redemptions` | id,tenant_id FK tenant,voucher_id UNIQUE,batch_id,operation_id UNIQUE FK command,actor FK users,amount,journal_id UNIQUE,funding_receipt_id UNIQUE,created_at；FK(voucher_id,batch_id)，并以tenant复合FK绑定journal/receipt；receipt复合FK显式DEFERRABLE INITIALLY DEFERRED |

新增`journals UNIQUE(tenant_id,id)`；保留既有operation_id全局唯一。credit追加`UNIQUE(journal_id)`和同tenant的journal复合FK；源身份/金额/proof不可改、pending→approved一次且批准形状完整、禁止DELETE，历史先预检/回填再启用触发器。未出现批准的credit保持原pending形状。

helper先在内存预分配receipt/redemption ID，journal先INSERT，随后redemption和receipt可以按§12.1顺序写；redemption→receipt的延迟复合FK与source/receipt提交触发器一起在COMMIT验证完整关联。不得用即时FK搭配“先redemption后receipt”的流程，也不容许提交中间缺receipt的核销。

ledger_accounts追加`OPERATING_GRANT_CLEARING`到kind及平台账户形状CHECK：tenant_id必须NULL、balance<=0；不改变既有四科目余额，不提前创建客户资金。同步扩展`lockAccounts`kind白名单与缺省科目列表，所有相关账户仍一次按全局ID锁定，BIGINT溢出在发batch前拒绝。

所有历史表有`reject_history_mutation`级UPDATE/DELETE保护；commands与terminal receipt必须通过DEFERRABLE INITIALLY DEFERRED双向约束恰好一对。funding/source/journal的约束触发器在所有受影响源（credit、receipt、redemption、journal，以及projection_outbox的credited INSERT）上检查双向覆盖，不能仅在receipt INSERT检查而放过孤立journal或事件。outbox约束对非credited直接忽略；credited必须有同tenant/operation/amount/schema的合法funding source/journal/receipt，即使本tx只插一条孤立credited也必须在COMMIT拒绝，不能使reports-v1凭空累加。精确核验§8所列两条科目、金额、币种、operation/reference、租户、source归属与唯一credited。redemption还核验command/terminal资金引用和批次face；batch计数由redemption INSERT后的受控trigger按已存在事实增量更新，提交复核COUNT，app直接改兑数不能凭空制造核销。

policy trigger仅enabled实际变化递增revision，插入强制1；batch trigger仅合法state边递增revision，身份/面值/数量/expiry不可变，批准/终结actor与时间只能按状态一次写入。全部列表建`(created_at DESC,id COLLATE "C" DESC)`索引；tenant receipts增加tenant前缀，revocation列表增加batch前缀，operation receipt按其operation PK查询。摘要/代码不进查询cursor或公开索引。

### 12.3 最小角色与旧路径兼容

| 身份 | A新增权限 |
| --- | --- |
| app | 对新业务表SELECT；除policy外INSERT；对policy仅UPDATE(enabled)，对batch仅UPDATE(state,approver,activated_at,terminal_actor,terminal_at,redeemed_count)；revision由trigger写。对credit撤回表级UPDATE/DELETE，仅UPDATE(state,approver,journal_id)。新历史表一律无UPDATE/DELETE，无DDL |
| projector | 不授任一新增资金/券/command表读取或写入；保持原projection_outbox SELECT、自有projection receipts/totals写权限 |
| migration owner | 排空维护、实例锁内追加DDL/严格预检/历史receipt回填及provision；不将owner身份给serve，也不提供线上GUC绕过历史保护 |

batch的redeemed_count列权限是redemption触发器所需；行guard同时要求恰好OLD+1且等于本tx已经存在的核销COUNT，提交再核对，不能将列GRANT理解为可任意编辑计数。所有新表需在`ProvisionFor`既有ALL TABLES blanket grant之后显式收回表级UPDATE/DELETE；不能只追加列级GRANT而保留原大权限。

人工`CreateCredit`的financial_idempotency、业务ref和响应保持；`ApproveCredit`保留原source/角色/tenant锁及重复审批语义，只把post、状态/journal绑定与原credited迁入helper，原审批audit在外层一次。券env/policy关闭不阻断人工入账。原Wallet/Report DTO与reports-v1 consumer不改，credited继续累计两类来源；同步分类仅读receipts，不加分类consumer。

历史预检确保每条approved credit恰一合法journal和原credited，pending没有二者，且无孤立credit-kind journal或credited；异常中止迁移。只回填receipt，不改journal/账户/原事件。Rebuild仍先投影到inactive consumer；最终tx调用完整Funding核验，再用receipts SUM对credited，并保留charge/refund/release/request及原账本核验，全部成功才切active。`VerifyLedger`保留既有结果字段并把Funding检查纳入Balanced判定；如需新增诊断字段只追加，check-ledger遇任一不匹配均失败。A0尚须真实PG证明旧人工/旧reports重建、同额source交换、缺事件/孤立journal、只插孤立credited被提交约束拒绝、最小角色及所有失败原子回滚，不能把此设计当作已通过。

# 022 接口、资金与恢复合同（提案）

2026-11-01，**A0/A1已实现并通过限定本地验收；B0-P与M/Q独立沙箱已实现并通过限定专项/真实进程短程；旧版本20代长程完成、最新补丁/精确故障另验，B1核心SQL与B2资金接线合同已冻结，B2尚未追加021或接应用，B3与C后续**。A依[A执行合同](a-contract.md)、[领域合同](a-domain-contract.md)完成共同入账、双审券、HTTP和控制台，证据见[验证记录](../../docs/verification/funding-vouchers.md)。在线支付沙箱、来源lot与原路退款继续独立审查，不能将A验收视作022整体完成。

## 1. 公共值、身份与命令

- 金额为微CNY：`"0"`或无前导零的正整数字符串；单笔和账户余额不超过 `9223372036854775807`。有符号分录仅允许规范负数字符串。生命周期合计用NUMERIC规范字符串，前端不能转JS Number。
- `operation_id`、资源ID为小写非nil规范UUID；revision为规范正十进制字符串，累计计数、`expected_redeemed_count`为规范非负十进制字符串。数量count为无小数/指数的JSON整数1..100。时间为UTC RFC3339、最多微秒精度，摘要归一为6位微秒；A最终到期判断在全部钱包账户锁后取独立DB时刻。
- 人的写操作沿用Cookie、CSRF和现有10分钟再认证；已开启MFA照常验证，未开启不额外强制绑定。租户来自当前会话，平台finance角色不自动成为租户owner。
- A command以全局operation UUID唯一，scope为platform或tenant UUID的明确域；actor/scope/kind/路径目标/规范body均绑定摘要，不能同UUID跨种类、scope或batch/proposal目标复用。兑换body仅保存独立用途HMAC指纹，command记录指纹key版本，同ID按原版本核查；不能保存券。详细scope/DTO见[A§4](a-contract.md#4-命令与已知拒绝的提交矩阵)。
- 已提交命令持久保留 `accepted|rejected` 终态回执及原资源ID；相同ID/摘要只读返回原结果，异体为409。不要复用现有只允许 `credit|refund` 的 `financial_idempotency` 并静默改变其旧语义。
- 网络/COMMIT结果未知时只GET原operation；404不是失败证据，不自动重发。A不通过普通HTTP5xx或GET404声明`not_committed`，不自动换ID或重发。所有回执均核对operation、kind、actor、scope、资源和不可变金额归属。
- GET当前配置相同不能证明某次写成功。A列表固定50、读51判断下一页，不接受limit/offset；按`created_at DESC,id::text COLLATE "C" DESC`，cursor绑定scope/列表种类/筛选并始终重验当前读权；无效cursor返回400，跨租户资源统一404。B/C分页另行冻结，不沿用初稿的“最大100”。
- 只保存安全的引用ID（现有 `security.ValidReference` 约束）和受限理由；不接收任意URL、证据正文或支付密钥。原始请求体不进入日志/审计。

## 2. 权限矩阵

| 操作 | 当前角色 | 原子校验与限制 |
| --- | --- | --- |
| 能力开关、发布充值商品 | admin | 再认证、CAS、审计；不能增加余额 |
| A平台有效能力查询 | admin、finance_operator、finance_approver | 仅active角色，只读有效值；不扩大仅admin可读写的voucher-policy |
| 券批次提案/待审批次撤销 | finance_operator / 原制单人 | 制单人当前有效；待审批撤销不产钱 |
| 券生效、已生效批次作废审批 | finance_approver | 与制单人不同，重查双方当前角色；作废另有operator提案 |
| 券兑换、创建充值订单 | 当前租户owner | 当前用户/成员/租户有效、CSRF、再认证；API Key不支持 |
| 本租户回执/订单 | owner、billing_viewer | member无默认资金读取权；隐藏平台/provider内部细节 |
| 全平台批次/订单/核查事实 | admin、finance_operator、finance_approver | admin只读财务事实；不显示券摘要、密钥或其他租户秘密 |
| 支付原路退款提案/审批 | finance_operator / 独立finance_approver | C完成后才注册；原支付lot和外部可退额均充足 |
| 收款/退款完成、确认拒付 | 系统worker | 仅消费已验证内部证据ID；不能伪装人工审批人 |

开关只阻止新提案/新兑换/新checkout。A采用env硬上限&&DB policy；已有proposed审批及void/revoke继续，policy锁确定disable与兑换的先后。精确开关API/CAS见[A§3](a-contract.md#3-动态开关与权限同步点)。已提交的支付收尾、通知验签、查单、已授权退款、债务调整与回执查询继续；recovery mode另有保护边界，见§11。

## 3. 兑换券

### 3.1 状态与一次展示

批次：`proposed → active → revoked`，或 `proposed → voided`。revision仅状态变更递增，兑换独立增加redeemed_count；审批/撤销快照和decision终态见[A§5](a-contract.md#5-批次核销和作废状态机)。审批生效必须早于expires_at；到期由DB时刻计算，不后台改写券。兑换须active/未过期/未兑/开关有效，最终判定在账户锁后。

批次body：`operation_id, face_amount, count, expires_at, purpose_ref, reason`。到期必须为创建时刻后1小时至365天；用途引用必填，理由最多200字节。单张正数，`face_amount*count`用大整数计算后不超过BIGINT，提案金额/数量/到期/用途不可改；改动新建提案。

券格式：`mav1.<public_voucher_uuid>.<32字节随机secret的base64url>`，精确85 ASCII字节且canonical。公开UUID不是证明；secret为256bit。验证与命令指纹用独立keyring/不同用途域，code及command各保存key版本、常量时间比较；不得复用当前单Pepper Digest。历史版本保留和缺失行为见[A§6](a-contract.md#6-密钥指纹与一次展示)。

创建事务仅保存摘要及安全事实；首次成功响应带 `codes:[{voucher_id,code}]` 和 `secrets_available:true`，设置 `Cache-Control:no-store`。同operation重读/重复请求仅返回批次元数据、`secrets_available:false`，**绝不生成另一批券或重新返回明文**。审批响应也没有codes。浏览器只留内存，关闭弹窗、变更身份、退出或卸载即清除；不自动下载、不写sessionStorage/localStorage/命令journal/URL。

一次性响应丢失：GET原批次确认状态；未审批由原maker作废；已active必须双审作废。确认作废未兑换部分后才允许创建新批次；已经兑换的额度不撤回。不能按“GET没有codes”推断发行失败。

### 3.2 API提案

| 方法与路径 | body / 结果 |
| --- | --- |
| `POST /api/platform/voucher-batches` | 上述提案body；201首次响应含一次性codes，重读200仅元数据 |
| `GET /api/platform/voucher-batches/{id}` | 安全批次、当前revision/已兑换数/剩余数，不含code/digest |
| `GET /api/platform/voucher-batches` | 当前平台权限只读列表，安全分页 |
| `POST /api/platform/voucher-batches/{id}/approve` | `operation_id,expected_revision`，独立审批；active不发钱 |
| `POST /api/platform/voucher-batches/{id}/void` | `operation_id,expected_revision,reason`；仅proposed的原maker |
| `POST /api/platform/voucher-batches/{id}/revocations` | operator提案：`operation_id,expected_revision,expected_redeemed_count,reason` |
| `GET /api/platform/voucher-batches/{id}/revocations` | 当前平台读权，按批次列出安全提案及可空的不可变decision |
| `GET /api/platform/voucher-revocations/{id}` | 当前平台读权；单个RevocationView，缺失404 |
| `POST /api/platform/voucher-revocations/{id}/approve` | 严格body仅`operation_id`；独立approver按原proposal核对revision/已兑换数，变化时HTTP200+rejected `voucher_evidence_stale`及不可变rejected decision；不能在审批body重传新证据 |
| `POST /api/wallet/voucher-redemptions` | `operation_id,code`；成功回执只含redemption/funding receipt/amount/journal，永不回显券 |
| `GET /api/wallet/voucher-redemptions/{id}` | id是redemption UUID，不是voucher或operation ID；当前owner/billing_viewer读取本租户RedemptionView，跨租户404 |
| `GET /api/wallet/funding-receipts` | 本租户来源明细，不含其他租户、密钥或平台支付验证材料 |
| `GET /api/wallet/commerce-operations/{operation_id}` | 本租户原命令回执，未知时只读查此接口 |
| `GET /api/platform/commerce-operations/{operation_id}` | 当前平台权限的原命令回执 |
| `GET/PUT /api/platform/voucher-policy` | admin动态开关及独立CAS；env硬上限不能由页面绕过 |
| `GET /api/wallet/commerce-capabilities` | owner/billing_viewer有效能力；A的payments_mode固定off |
| `GET /api/platform/commerce-capabilities` | active admin/finance_operator/finance_approver读取同一安全有效能力DTO；不授予policy读取权 |
| `GET /api/wallet/funding-summary` | owner/billing_viewer，receipt累计及manual/voucher分类NUMERIC字符串和DB观察时刻 |

同tenant+operation+同券重读原回执。新operation兑换已兑券，或语法合法的无效/过期/撤销/其他租户已用券，统一持久rejected reason=`voucher_unavailable`；不暴露获胜租户/兑换ID。A业务拒绝采用HTTP200+安全OperationReceipt，包括DB policy off、旧CAS、stale和溢出，绝不把200当accepted；env硬上限off在claim前409，语法400、operation异体409、限流429、当前权限403。完整提交/回滚/未知结果矩阵见[A§4](a-contract.md#4-命令与已知拒绝的提交矩阵)，替代初稿笼统409约定。

### 3.3 唯一入账事务

command→batch→不可变voucher/policy→tenant/membership/user→全部account按ID锁→新语句最终DB时刻/expiry→唯一redemption+平衡journal+funding receipt+一个credited+audit+terminal，全在原tx。helper输入source身份，不接收客户端可信金额；资金所有者见[A§7](a-contract.md#7-共同-funding-的唯一资金所有者)。发行/审批不插入journal。语义拒绝以savepoint清除业务效果后持久rejected；audit/outbox/DB失败回滚整个command。券不先核销后异步加钱。

### 3.4 A的请求与安全响应精确形状

以下是A的冻结接口清单，与[A合同](a-contract.md)共同约束实现。每个body仅允许表列字段，全部必填；无隐藏的amount/paid/actor/tenant输入。公共严格JSON、8KiB上限、UUID/金额/日期/引用边界、有界准入和cursor编码沿[A§2](a-contract.md#2-严格值与有界-http)。GET不接收body。除receipt列表可附`source_kind=manual_credit|voucher_grant`外，A列表只允许可选cursor，其余A GET不接收query；所有列表包括作废提案列表均每页重新检查当前读权。

| 写路径 | 精确body |
| --- | --- |
| POST `/api/platform/voucher-batches` | `{operation_id,face_amount,count,expires_at,purpose_ref,reason}` |
| POST `/api/platform/voucher-batches/{id}/approve` | `{operation_id,expected_revision}` |
| POST `/api/platform/voucher-batches/{id}/void` | `{operation_id,expected_revision,reason}` |
| POST `/api/platform/voucher-batches/{id}/revocations` | `{operation_id,expected_revision,expected_redeemed_count,reason}` |
| POST `/api/platform/voucher-revocations/{id}/approve` | `{operation_id}`；审批只消费原proposal冻结的证据 |
| POST `/api/wallet/voucher-redemptions` | `{operation_id,code}` |
| PUT `/api/platform/voucher-policy` | `{expected_revision,enabled}`；不创建commerce operation |

命令摘要精确规范对象为`{v:1,actor,scope,kind,target_id,body}`。target_id由路由产生：batch approve/void及revocation create为batch ID，revocation approve为proposal ID，batch create/redeem为null；不能由HTTP body另外传入。body排除operation_id，兑换仅用`{voucher_fingerprint}`，其他用表列安全字段并先归一时间/金额/revision。同operation更换path目标即异体409，不得返回另一资源的原accepted。

所有A响应均`Cache-Control:no-store`。单体GET及policy PUT成功均为`{data:SafeView,request_id}`；列表固定`{data:[SafeView],next_cursor:string|null,request_id}`，空页data为`[]`、无下一页next_cursor必为null，不省略字段、不返回空字符串。列表中每个BatchView的expired与observed_at由同一SQL观察时刻计算；列表不是后续审批/兑换的资格承诺。A自有错误响应沿现有`{error:{code,message},request_id}`，message为固定安全文本，不返回原输入；基础认证/Guard错误保留既有安全合同。

所有mutation除policy PUT外，data只为下节OperationReceipt。首次已确认COMMIT的batch-create获胜响应HTTP201：`{data:OperationReceipt,batch:BatchView,codes:[{voucher_id,code}],secrets_available:true,request_id}`。同accepted创建命令重读HTTP200：`{data:OperationReceipt,batch:BatchView,secrets_available:false,request_id}`，无codes键。rejected创建HTTP200：`{data:OperationReceipt,secrets_available:false,request_id}`，无batch/codes。其余命令HTTP200：`{data:OperationReceipt,request_id}`。GET operation始终仅后者形状，GET不恢复batch-create的secret；其关联batch由resource_id另查。创建重读中的batch是当前视图，data仍为原不可变终态。

| SafeView | 精确字段/类型 |
| --- | --- |
| PolicyView | `{enabled:boolean,revision:string,effective_enabled:boolean,runtime_allowed:boolean}`；GET/PUT同DTO。runtime_allowed仅反映env硬上限；effective_enabled=runtime_allowed&&DB enabled。Recovery/ready/Guard另行约束，不由这两个boolean证明可写 |
| CapabilitiesView | `{vouchers_enabled:boolean,payments_mode:"off"}`；wallet/platform同DTO，vouchers_enabled=env&&DB policy；DB失败503，不返回假off |
| BatchView | `{id,state,face_amount,count,expires_at,purpose_ref,reason,maker,approver,activated_at,terminal_actor,terminal_at,revision,redeemed_count,remaining_count,created_at,expired,observed_at}`；state为proposed/active/voided/revoked，count为JSON整数，expired为boolean；金额/revision/兑数/剩余数为规范字符串，时间UTC六位微秒，actor均用户UUID |
| RevocationView | `{id,batch_id,expected_revision,expected_redeemed_count,reason,maker,created_at,decision}`；revision/兑数为字符串，reason是提案原理由，decision未审批为null，否则为DecisionView。无可任意更新的proposal.state |
| DecisionView | `{id,operation_id,status,reason,approver,decided_at}`；status为accepted/rejected，accepted reason固定`accepted`，rejected仅`voucher_evidence_stale`。决定追加且不可改；已存在decision的新operation不再创建decision |
| RedemptionView | `{id,operation_id,tenant_id,voucher_id,batch_id,actor,amount,currency,funding_receipt_id,journal_id,created_at}`；amount为正数字符串、currency固定CNY，其他字段均非null；无code/digest/fingerprint |
| ReceiptView | `{id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at}`；source_kind仅manual_credit/voucher_grant，amount正数字符串、currency固定CNY；manual source_id保留原credit TEXT ID，不做UUID强转/重写历史；不含manual proof_ref或券秘密 |
| FundingSummaryView | `{currency:"CNY",total_amount,manual_credit_amount,voucher_grant_amount,as_of}`；三金额为NUMERIC规范非负字符串，single SQL下total=两类合计，as_of为DB时刻；不是AVAILABLE或现金收入 |

BatchView的maker、创建reason始终保留。active批次作废理由在RevocationView，proposed的void理由留在对应安全审计，均不覆盖创建理由。state空值严格如下；到期只改变expired，不改变这些历史字段。

| state | approver / activated_at | terminal_actor / terminal_at |
| --- | --- | --- |
| proposed | 均null | 均null |
| active | 均非null | 均null |
| voided | 均null | 均非null，terminal_actor为原maker |
| revoked | 保留原激活者/时刻，均非null | 均非null，terminal_actor为作废审批人 |

审批时maker/checker都须当前有效且不同；已active券不因maker之后离职自动失效。当前角色/租户读取权限始终重查，历史批次授权不能用来绕过当前平台或钱包读权。

### 3.5 OperationReceipt逐kind/status矩阵

精确字段为`{operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id,created_at}`，无可省略字段。scope为`platform`或当前租户UUID；内部`tenant:<UUID>`域不照搬给客户。status仅accepted/rejected；accepted reason固定字符串`accepted`。所有rejected的resource_id、amount、funding_receipt_id、journal_id一律null，包括stale/旧CAS与无效券；不存在的voucher不产生任何可枚举关联。非空UUID均为规范UUID，非空amount正数，created_at为该命令终态提交事务固定的DB创建时刻。

| kind | accepted的resource_id | accepted的amount / funding_receipt_id / journal_id | 可持久rejected reason |
| --- | --- | --- | --- |
| voucher.batch.create | batch ID | 全null；发行不增加钱包 | vouchers_disabled、voucher_state_conflict（锁后创建时间范围不符） |
| voucher.batch.approve | batch ID | 全null | voucher_revision_conflict、voucher_state_conflict（非proposed/已过期） |
| voucher.batch.void | batch ID | 全null | voucher_revision_conflict、voucher_state_conflict |
| voucher.revocation.create | proposal ID | 全null | voucher_revision_conflict、voucher_evidence_stale（兑数不符）、voucher_state_conflict |
| voucher.revocation.approve | proposal ID | 全null | voucher_evidence_stale（batch revision/兑数/state变化，并追加rejected decision）、voucher_state_conflict（已有decision，不再追加） |
| voucher.redeem | redemption ID | amount为实际到账额，funding_receipt_id/journal_id均非null | voucher_unavailable、vouchers_disabled、funding_amount_overflow |

同operation重读返回原终态，无论batch后来到期/作废、正常policy off或maker后来离职，均不重新执行；仍须当前合法读权。金额格式/face×count超BIGINT等静态输入错误在claim前400，不伪造funding_amount_overflow终态。缺失路径资源404、当前授权丧失403时回滚本次command；operation异体409、env硬off409、policy PUT旧CAS409均不是HTTP200的业务rejected。除revocation approve的stale分支有不可变rejected decision外，业务rejected没有核销、批次状态变更、资金、业务audit或outbox。基础设施失败全体回滚或保持结果未知，不能落为上述reason。

### 3.6 前端未知结果与一次展示

UI必须先核对operation/kind/actor/scope，再按receipt.status判断；HTTP200+rejected不能显示“到账/审批成功”。写请求发出后网络/提交未知时锁住原operation，仅GET查原回执；GET404、GET当前policy与期望一致、页面刷新或切换页签均不解锁、不生成新operation，也不自动重发原body。返回已知终态后才可显式发起新的合法操作。

批次codes只在发起该次创建且身份epoch/当前弹窗/operation仍一致时接收入内存；关闭、卸载、退出、切换用户/租户后清除，即使随后旧201响应到达也丢弃codes，不缓存为下一次打开内容。超时/unknown后的晚响应不能解除未知保护或恢复一次展示；仅查询安全operation/batch。安全command journal只留原operation、kind、scope、resource ID和时间，不留code、原发行/兑换body或完整响应。丢失codes按原批次void/双审revoke再显式新建，不能恢复原secret。

钱包用“累计钱包入账”“人工核款”“运营赠额”标识A来源，明确运营赠额不是收款/现金收入、不赋予现金退款权。同步来源摘要展示as_of，现有异步credited报表可有投影延迟，不能把其差值当作自动补账依据。A不展示在线付款成功或原路退款按钮。

## 4. 共同入账事实与投影兼容

建议唯一表名为`funding_receipts`，不另建别名表：A精确字段为`id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at`；UNIQUE(source_kind,source_id)、UNIQUE(journal_id)、UNIQUE(tenant_id,id)，tenant/journal复合FK及提交时source/金额/指定科目证明。A仅manual_credit（credit ID）和voucher_grant（voucher ID）；source_id保留历史credit TEXT ID。sandbox_capture及环境字段待B追加迁移，live另立合同。

- 历史先严格检查approved credit与原journal/credited归属/金额/唯一性，异常中止维护升级；再回填一条receipt，不创建journal/outbox、不改history，pending无receipt。追加manual source不可变/终态完整保护，不能将目前可更新credit行直接当永久证明。
- 新人工审批和券兑换/沙箱确认入账在原事务内创建receipt；内部“入账”只接受source ID并重读可信源的金额和状态，HTTP不能传任意可信金额。
- 原 `credited` 聚合继续表示累计钱包入账，不等于真实现金收入。保持旧人工credited事件可重放；新事件保留稳定operation/amount，增加source/schema信息但不把同一人工入账发两次。
- Report与Rebuild改为共同receipt核验；钱包仍同步读ledger。来源分类及sandbox标记另行展示，unknown不当现金。消费refund不算新的充值；支付refund/chargeback也不能塞入原消费refunded指标。
- A分类同步读安全receipt并保留reports-v1，无第二份分类credited事件或consumer；Rebuild保留原切换门禁并核验全部source/receipt/journal覆盖和精确科目，不能只比较SUM。具体维护迁移/VerifyFunding见[A§9](a-contract.md#9-排空迁移投影与核验)。
- A projector继续只读outbox/自身projection，无receipt/券/command读权或任何资金写权；离线维护核验使用原受限app/owner授权，不能扩大普通projector。

A0已冻结的确切内部接口和追加DDL见[A§12](a-contract.md#12-a0-账务接口与追加-ddl)：`ApplyFundingTx(ctx,tx,FundingSourceRef,FundingScope) (FundingResult,error)`不接收客户端金额、不创建/提交事务。FundingScope仅TenantID/ActorID/CommandID，manual的CommandID必须空，voucher必须为本tx已claim的voucher.redeem operation；Applied仅内部使用，不序列化为成功承诺。source/授权父锁归外层，账户锁、账户锁后DB时刻、source资金绑定及唯一credited归helper，audit/terminal command归外层同tx。

`VerifyFunding(ctx,tenantID)`空tenantID核验全库，非空仅核验该租户source/journal对应的clearing分录，不能用一个tenant的小计去比平台共享清算账户全余额；全账户余额仍由原VerifyLedger核验。只读REPEATABLE READ及维护Rebuild同tx复用的边界沿A§12.1。拟追加017迁移的新业务ID为有canonical UUID约束的TEXT，decision只用status/reason/decided_at列，不使用outcome或created_at别名；现有历史源身份不重写。这里只同步待审接口，不代表017已存在或相关代码已实现。

## 5. 隔离支付沙箱与不可变订单

`PAYMENTS_MODE=off|sandbox|live`，默认off；本实现阶段live启动直接报不支持。sandbox只能由专用QA harness创建的隔离DB/schema标记启用；应用启动验证环境UUID、sandbox商户、独立密钥、loopback listener/origin及mock PSP endpoint，任何不一致拒绝启动。仅配置一个环境变量不能把普通实例变成可伪造支付的实例。

普通main不注册mock PSP、签名器、simulate-success或任意事件注入API。模拟器由QA harness另起loopback进程，私有控制凭证每次生成，仅CLI测试工具持有；客户前端、API Key和普通admin都不能获取。Mock签名密钥仅fixture与sandbox验签器使用；domain/environment/merchant共同防止跨环境重放。不调用外网或加载真实支付密钥，禁止重定向。

商品版本冻结 `sku_id,version,display_name,amount,wallet_credit_amount,currency=CNY,merchant_id,payee_label,environment_id,checkout_ttl=15m`；第一片要求两金额相等且均为正。管理员只可发布新版本、切换当前指针或停止新售；既有订单继续旧版。金额、版本、收款方、环境不可PATCH；停止新售不能取消已经捕获的事实。

订单冻结 `tenant_id,creator,sku_version_id,amount,wallet_credit_amount,currency,merchant/environment,payee,expires_at,merchant_order_id`；订单ID使用原operation UUID，外部merchant_order_id固定，不受浏览器重试改变。

| 方法与路径 | 合同 |
| --- | --- |
| `POST /api/platform/payment-skus` | admin：operation、完整不可变商品版本；sandbox环境强绑定，不能任意URL |
| `GET /api/wallet/payment-skus` | 当前可售版本和sandbox醒目标记；off返回空能力而非假可支付 |
| `POST /api/wallet/payment-orders` | owner：`operation_id,sku_version_id`；不接收客户端amount/payee/callback URL；202返回本地order及pending状态 |
| `GET /api/wallet/payment-orders` / `/{id}` | 本租户快照、checkout状态、资金状态、安全到账回执及退款/债务摘要 |
| `POST /api/wallet/payment-orders/{id}/cancel` | owner、operation；取消新checkout意图，不能推翻已确认收款；外部未知先查单 |
| `GET /api/platform/payment-orders` / `/{id}` | 安全财务视图及核查事实；只读不允许“强制置成功” |
| `POST /api/payments/sandbox/notifications` | 仅隔离sandbox部署注册；§6签名，不以Cookie或admin作为验签替代 |

checkout状态：`pending → ready → expired|cancel_requested|closed`，可附 `creation_unknown`。资金状态独立：`unconfirmed → verified → credited`，或 `review_required`；后续另记refund/dispute，不把已确认收款写回failed。页面返回或checkout expired本身都不决定是否收款。

合法capture的provider时间落于订单创建至expires_at内，即使通知晚到也入账一次；merchant/order/amount/currency/环境必须完全匹配。捕获发生在失效后、已取消后，或金额不符、同订单又出现另一交易，存核查事实不直接入账。late事件不得静默丢弃；没有tenant的未知订单只存平台验证证据，不伪造tenant journal。人工释放隔离收款需另行双审证据合同；B不提供通用补账按钮。

## 6. 通知、inbox、外部命令与查单

### 6.1 沙箱签名和body

请求上限64KiB，严格JSON与显式schema，未知字段拒绝。headers：`X-Moni-Key-Id`、`X-Moni-Delivery-Id`、`X-Moni-Delivery-Time`、`X-Moni-Signature`。时间为UTC Unix秒，允许数据库/服务验证时刻±300s；delivery ID为UUID。签名为HMAC-SHA256以下UTF-8串：

```text
moni-payment-sandbox-v1\n
<environment_uuid>\n
<merchant_id>\n
<delivery_uuid>\n
<unix_seconds>\n
<lowercase_sha256_of_exact_raw_body>
```

最后一行无额外换行，图示 `\n` 表示单个LF而非两个字符。key ID查固定allowlist及有效轮换窗口，merchant/env来自应用配置并与body核对，常量时间比较。不接受客户端公钥/JWKS地址/算法切换；签名只证明来源，仍需业务唯一约束和金额匹配。

body固定公共字段：`schema_version:1,event_id,event_type,environment_id,merchant_id,order_id,provider_transaction_id,amount,currency,occurred_at`。event_id/transaction_id为受控引用≤100字节。type为 `capture.confirmed|checkout.failed|refund.confirmed|refund.failed|dispute.opened|chargeback.debited|chargeback.reversed`；后四类关联原transaction，refund再带本地 `refund_id`，chargeback带稳定 `case_id`。各type必需/禁止字段用单独DTO验证，不能靠任意JSON穿透。B仅处理capture/checkout，C前其余有效证据持久保留并标 `unsupported_effect_review`，不得假装已经完成扣回。

同event重投递时业务event_id稳定，delivery/time/signature可重新生成。规范业务摘要包含所有解释字段和关联身份，JSON空白/键顺序不构成冲突，金额/类型/归属/发生时间变化构成冲突。

### 6.2 inbox与effects

- 验签/环境校验成功后存白名单规范字段、digest、key ID、接收时刻；不保存原始任意body、signature或URL。`UNIQUE(environment_id,merchant_id,event_id)`。
- 相同event+相同digest返回原安全接收回执；不同digest保存不可变冲突并409，不覆盖原事实。不同event指向同一transaction仍受 `UNIQUE(environment_id,merchant_id,provider_transaction_id,effect_kind,effect_id)` 保护。
- 2xx只在inbox提交后返回；DB失败5xx；首次202、等价重复200。401签名错误、400语法/商户错误、413超限均不能插入可信资金事件。应用限流和有界安全计数不能记录raw body。
- Worker lease/重领实现至少一次；同事务验证order与事件、建立唯一effect、入账journal/receipt、更新安全状态、audit/outbox和标记processed。任一点失败整体回滚；ACK后崩溃可恢复。
- 乱序failed不能覆盖capture；reversed-before-debited等先存pending证据并查权威现状，不能先免费加钱。一个event已processed不保证同订单所有事件都已完成，页面按财务事实展示。

### 6.3 外部命令

`payment_command_outbox`保存 `checkout|cancel|refund|query` 的稳定command ID、order/refund ID、冻结参数digest、状态和lease。写业务状态与outbox同事务；worker领取后在事务外调用固定配置的mock端点。重试不得生成新外部ID。

提交超时进入 `submission_unknown`，先用原商户order/refund/command ID查单。只在适配器明确认证幂等语义时重发同ID同body；不能按HTTP超时推断无副作用。查无记录只有在adapter定义了权威最终性和可见性边界后才算无effect，否则继续unknown。B的mock实现并测试明确的幂等/查询合同，不推论其他支付商相同。

查单响应经配置商户鉴权/环境匹配后归一成与回调同源的内部验证事件；回调和查询竞争受相同effect唯一性保护。禁止按用户提供的URL查单。outbox成功投递与财务入账相互独立，后台不调用任何模型生成入口。

## 7. C前置：资金来源lot与推理分配

只有A/B并不提供原路退款。C第一步必须在真实PG证明以下合同，方可注册§8/§9接口：

1. 每个新funding receipt创建同tenant、同currency的不可变lot，source kind及cash-refund资格不可改。voucher/legacy/manual不具备本版自动原路退款权；只有经验证的**原支付订单lot**可由对应PSP退原路。sandbox lot不能在live使用。
2. lot身份和initial face不可变，余额由不可变allocation events与锁内投影同步产生。当前桶为 `available,inference_held,payment_refund_held,net_consumed,payment_returned,chargeback_recovered,debt_repaid`，全部非负；合计等于initial face（历史退款例外通过独立legacy退款lot引入face，不能无事件增大旧lot）。补偿/逆向通过新事件回移原桶，不改旧事件。
3. 新客户Reserve在现有客户账本同事务按 `(credited_at,id)` FIFO分配R；分配总和=reservation.amount，lot available同步转inference_held。两次安全supplier attempt共用客户reservation，不再分配客户资金。
4. 结算C按原分配顺序把C转net_consumed，余量回同lot available；C≤R和既有硬24小时政策不变。unknown保持原lot held。释放、超时、免收和profile超界承担均按原分配收尾，不因渠道/租户状态变化拒绝完成。
5. 消费退款逐笔绑定settlement的原消费分配；累计不超过该分配实际消费，net_consumed回原lot available，不生成可偷换来源的新cash lot，Key spent继续不恢复。
6. 所有推理、人工入账、券、支付、消费退款、释放、查单、债务/拒付路径同时遵守lot不变量；后台投影不能修余额。`VerifyLedger`同时对比lot桶、账户余额、reservation和settlement。
7. 已有AVAILABLE/HELD在排空维护切换时迁成不可现金退legacy lot，并为每个未结reservation分配原R。旧settlement退款创建新的 `legacy_consumption_refund` 非现金lot并引用原refund journal；无可靠证据不猜历史现金来源。回填不创建新钱包journal或原credited事件。
8. 迁移前排空上游、停业务写/恢复/投影，取得现有实例锁；持久迁移checkpoint/校验后才能开放。无法对齐账户与预占则停在维护模式，不用调平journal掩盖差异。

现金退款资格不是钱包总额，亦不是lot最初充值额：必须扣除该lot的消费、推理held、refundheld、已返现、拒付回收等当前用途。FIFO的促销/现金消费顺序是本稿明确的初始政策；若改变须新版本，不静默重分配历史。

## 8. C：原路支付退款

原消费退款接口不变。新增路径只在lot合同验收后启用：

| API | 要点 |
| --- | --- |
| `POST /api/platform/payment-orders/{id}/refunds` | operator：`operation_id,amount,reason,evidence_ref`；限定原payment lot、原transaction、原环境/币种/收款方 |
| `POST /api/platform/payment-refunds/{id}/approve` | 独立approver：operation、预期order/refund revision和证据版本；重查maker角色、lot可退和外部累计上限 |
| `GET /api/platform/payment-refunds/{id}` | 原外部command、冻结/未知/终态安全回执和核查事实 |
| `GET /api/wallet/payment-refunds/{id}` | 本租户只读，不含支付验证内部材料 |

状态：`proposed → held → submitting → succeeded|failed_no_effect|submission_unknown|review_required`；unknown只能转原命令的权威成功/确定无effect/核查，不另起支付。审批在同事务将原lot available转payment_refund_held、`AVAILABLE -A / PAYMENT_REFUND_HELD +A`、写原路refund outbox与审核证据。外部成功：`PAYMENT_REFUND_HELD -A / FUNDING_CLEARING +A`并转原lot payment_returned；确定无effect：原lot及`AVAILABLE +A / PAYMENT_REFUND_HELD -A`；timeout保持hold。

本地已退+退款held+确定拒付占用不得超过原confirmed capture；同时必须有原lot available。支付商报告与本地可退边界冲突时先保存不可变事实并review，不盲目二次扣客户。即使已停新售或客户suspended，已承诺退款完成/失败归还仍继续。

## 9. C：争议、拒付和独立债务

`dispute.opened`是风险观察，不能当已扣回现金。首次合法争议将原payment lot设为refund-restricted；在原tenant锁屏障下暂停新增customer Reserve/sending（全租户策略简单且明确），已有sending、结算、释放和客户有利退款继续。关闭争议需带权威证据的新事件，不由GET或浏览器状态推断。

`chargeback.debited`须有原payment、case和确认扣回额A。只从原lot当前available回收R，D=A-R记独立债务；不动推理HELD和refundheld，不用其他券抵现金损失。平衡分录：`AVAILABLE -R, PAYMENT_DEBT -D, FUNDING_CLEARING +A`，其中PAYMENT_DEBT为tenant允许非正的独立账户，客户展示债务=-balance。original lot对应R转chargeback_recovered；已消费/held部分仍保留其本来用途，D有独立claim和后续repayment allocation，不能假装已收回。

债务/争议门禁在tenant行锁下与Reserve和PrepareSending同步：先有sending提交的不撤回，屏障之后的新Reserve/sending拒绝 `payment_restricted`，释放既有未发预占走正常无消费收尾。owner仍可登录、查看/联系财务/按显式偿付流程处理；不粗暴停用身份导致无法核查。

偿付只允许明确关联债务的已验证现金来源或原被拒付lot后续释放/消费退款的可用额度，不自动吞掉voucher/legacy/manual赠额。后续现金充值是否用于偿债必须在商品/确认页面事先声明，未冻结该产品合同前不开放自动偿债。每笔偿付保留source lot与debt claim的唯一分配；债务未清时新推理继续受限。

`chargeback.reversed`只反转该case已确认的扣回：恢复原lot回收R，退还逐笔已偿付分配并减少其债务用途，剩余未偿债D通过 `PAYMENT_DEBT +D`归零。恢复的AVAILABLE加上债务抵消恰好=A，沿原provenance记补偿，不创建无来源赠额。重复case/effect只能一次；先到reversed后到debited必须等待权威查单，不先增余额。

外部refund和chargeback累计超过原capture、部分金额矛盾或未知退款与拒付重叠，进入 `review_required`并保留所有权威证据；禁止用猜测分摊抹掉平台损失或重复扣客户。人工消解前须另行冻结明确审批/分录合同，本片不提供任意改债按钮。

## 10. 表、唯一性与数据库角色

建议追加迁移表，最终DDL在每片冻结时逐条审查；不修改已应用SQL：

| 表组 | 关键约束 |
| --- | --- |
| `commerce_commands` / `commerce_operation_receipts` | 稳定operation/scoped unique，actor/body digest/终态不可改，平台与租户scope明确 |
| `voucher_batches` / `vouchers` / `voucher_redemptions` / `voucher_revocations` | 不可变批次面额/用途/到期，code digest唯一，voucher全局redemption唯一；batch状态只允许单向，归属/approval不可伪改 |
| `funding_receipts` | source唯一、journal唯一、tenant复合归属，历史不可改；不建立另一份钱包receipt表 |
| `payment_sku_versions` / `payment_orders` | SKU版本/订单金额和收款快照不可变，商户order ID唯一；活动指针CAS |
| `payment_inbox` / `payment_event_conflicts` / `payment_effects` | env+merchant+event唯一；transaction+effect唯一；证据不可改，processed状态单独受控 |
| `payment_command_outbox` / `payment_command_observations` | 原command/参数不可改；领取lease状态单独，网络观察追加 |
| `wallet_funding_lots` / `funding_allocation_events` / 余额投影 | C新增；tenant复合FK；allocation operation唯一；桶非负及总额守恒；身份/资格不可改 |
| `payment_refunds` / `payment_disputes` / `payment_debt_claims` / 偿付分配 | C新增；原order/case/effect唯一；审批双人；每笔来源、冻结和终态有不可变事实 |

应用角色只通过经审查业务路径写受约束事实，不拥有表/trigger/函数定义；拒绝UPDATE/DELETE证据、流水、回执与source/allocation事件，状态投影仅允许列白名单及数据库状态转换保护。禁止公共插入一个“trusted=true”字段就入账；内部可信source只能从验证事实构造。

迁移角色独立。投影角色无券/订单/inbox验证/外部命令/ledger写权，只可读指定安全事实或outbox并写报表。mock进程没有应用数据库角色。恢复CLI在独占维护态执行，不授予普通projector造额能力。追加账号类型必须同时更新DB check、billing账户allowlist、验证器和安全输出，不能只改一层。

## 11. 锁序、恢复和保留

实际锁图与A目标见[A§10](a-contract.md#10-按实际代码冻结的a锁图)。当前lockRequest首先非锁定读取request，真正同步顺序是tenant SHARE→membership SHARE→key UPDATE→model SHARE→可选channel SHARE→reservation UPDATE；ApproveRefund先refund和财务user，再进入该父锁链，随后settlement/accounts。Reserve为tenant→membership→user→key→model→accounts。不能沿初稿把reservation/settlement放在tenant之前。

A先command/batch业务根，再policy/授权父锁、账户ID序和最终时刻，Funding helper不回锁父行；policy PUT只admin→policy，不再取batch/账户。C需重新合并payment-order/lot图，尚未冻结统一新增层次；不允许account→tenant/request/payment-order或lot→settlement反向依赖。任何旧路径顺序调整先设计/PG验证，不让相反顺序并存。

关键禁止：消费退款/释放不得先锁lot再锁settlement或账户；普通Reserve不得先锁source payment-order；chargeback/refund worker不得锁账户后再补tenant；外部HTTP不持有事务锁。lot身份不需要回锁payment-order，原支付资格快照受不可变source证明保护。voucher核销和revocation均锁batch以序列化证据计数；原路退款与chargeback以同一payment-order根串行。

C债务/争议门禁写tenant UPDATE，Reserve/PrepareSending持有tenant SHARE并在锁内读最新restriction；已有终态处理不读取新的准入权限。针对最新sending、未知持有、异步退款/拒付竞争必须用PG锁等待测试验证。

recovery mode拒绝新券发行/审批/兑换、checkout、人工入账、退款授权及所有新资金effect应用；允许只读和受控证据收集。已验签通知可持久inbox后ACK，但不能自动恢复到资金已处理；外部已发退款可查同命令，不重复发送，入账/释放等变动由恢复核查确定。离库检查点和PSP记录必须核对恢复点后收款/退现，单凭旧库不能重新发券、收款或退款。退出恢复模式仍需原运行手册的独立证据与双人确认。

关闭新售和正常功能off不会阻断既有资金收尾；恢复模式是更强的事故冻结，界面和runbook须区分。券/摘要密钥、商品/订单、验证事件、effect、原命令、ledger关联和幂等记录按财务保留政策持久，不为了清理旧券而失去重放去重依据。备份同时保留所需外部密钥版本，禁止把密钥写进库备份或QA报告。

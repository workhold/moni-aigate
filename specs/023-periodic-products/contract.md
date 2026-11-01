# 023 价格归属、周期占用与期限权益合同草案

2026-11-01，**A0精确合同已冻结；A已实现并通过限定本地验收，B/C未冻结未实现**。A以[精确实施合同](a-contract.md)覆盖本稿同主题概述；B/C仍须独立冻结，不能据本稿接通周期预算或购买。

## 1. 公共值、命令和权限

- ID为小写、非nil、规范UUID；金额/费率/revision/累计为规范十进制字符串，金额非负、revision正，拒Number/指数/前导零。单价、单笔及配置限额≤int64；累计预算事实使用无损NUMERIC字符串，差额可为规范负数字符串。
- 时间使用UTC `Z` RFC3339，最多6位小数，响应/摘要统一6位微秒；周期kind仅`utc_day|utc_month`。月是实际自然月，不是30×24h；区间 `[start,end)`。
- 新HTTP body≤8KiB、单个严格UTF-8 JSON对象；必要字段不能遗漏，nullable字段仅在明确允许时为null，拒重复/别名/未知/尾JSON/控制字符。label为1..100 UTF-8字节，reason为1..200字节；均trim后校验且不含控制字符。
- 只读使用现有Cookie与当前身份，所有写沿CSRF、可选MFA、10分钟再认证、ready/Guard/恢复门禁。API Key仅沿原模型目录和真实调用取得其最终计费；本片没有Key报价查询接口，不能配置组或预算。
- 新管理写提案使用全局`operation_id`及actor/scope/kind/规范body摘要；成功结果与安全审计同事务，不复用022券指纹或原financial_idempotency。A固定`pricing_operations`并保存safe_input/result_snapshot，B命令表另行冻结，不复用A或022。成功回执不可变，原ID同体只读返回原结果，异体409 `policy_operation_conflict`。
- `PolicyOperationView={operation_id,kind,scope,actor,target,input_fingerprint,resource_id,revision,created_at}`。scope为`platform`或本tenant UUID；resource_id必须UUID，revision只在价格发布时null，其余为正数字符串。mutation `data:{operation,result}`，GET原operation返回相同不可变安全快照；created首次201、更新/重读200。CAS/权限/语法失败整体回滚，不建立成功回执。这里没有022式HTTP200 rejected资金命令，不能混用。
- 网络/5xx/非法响应/COMMIT未知只GET原operation；404不证明失败，不自动换ID/重发。回执须核对原kind/actor/scope/目标/安全参数；当前GET配置相同不证明原操作归因。当前读权限丧失仍拒绝读取旧回执。
- 列表固定50、取51判断next_cursor，envelope精确`{data,request_id,next_cursor}`，next_cursor始终字符串、末页`""`。cursor绑定当前scope/接口/规范筛选/排序；不接受客户端limit/offset。A0/B0须冻结编码/大小边界和有界准入预算，不能以此处泛称代替实施合同。

## 2. A：单租户单价格组

### 2.1 数据与状态

拟追加`price_groups(id,label,state,revision,created_at)`，state为`active|retired`；无DELETE、恢复或复活操作。label/state实际变化使管理revision+1，同值no-op仍比较expected，不能伪造revision。退休不改已分配tenant或历史价格。

tenant追加独立`price_group_id NULL`和`pricing_revision=1`；null是公共默认价，不把租户ID或成员角色当组。组变动仅使pricing_revision+1；流量spent/held、授权和tenant状态不影响该版本。不可变assignment事件保留before/after、actor、operation和DB时间。

`price_versions`拟追加nullable `price_group_id`，旧行保持null，原request的price_version_id FK继续有效。只用追加迁移替换原`UNIQUE(model_id,effective_at)`为覆盖`model_id,price_group_id,effective_at`的NULL一致唯一性；不得修改旧版本费率、归属或时间。实际约束名、trigger与角色由A0审查。公共默认价的既有发布入口始终写null，不允许通过新增字段变成任意组发布。

`GroupView={id,label,state,revision,created_at}`。`AssignmentView={tenant_id,price_group_id,pricing_revision}`，price_group_id可null。`GroupPriceView={id,model_id,price_group_id,currency,input_rate,output_rate,effective_at,created_at}`，新分组发布的group非null、currency=CNY，费率非负int64字符串；输入计费模型output_rate恒`"0"`。版本一旦发布不可撤回，纠错发布后继版本，不能删除已生效或计划生效的历史。

### 2.2 售价选择

1. Reserve按现有tenant→membership→user→Key→model取得授权锁后，读取锁住tenant的价格组与pricing_revision；非null再取得该group SHARE。assignment修改必须先tenant FOR NO KEY UPDATE，不能从group回锁tenant。
2. 用一个MATERIALIZED DB时刻`priced_at`，仅在精确组域内读取`effective_at<=priced_at`，按`effective_at DESC,created_at DESC,id DESC`取一条。组为null使用公共默认；组非null无有效版本则503 `price_unavailable`，不跨域fallback。模型/Key/成员未授权仍先按原权限拒绝，不因有价格而放行。
3. 冻结`price_version_id,input_rate,output_rate,pricing_group_id,tenant_pricing_revision,retail_priced_at`。旧请求新增快照列可null表示legacy，不能把“当时默认组”倒推成已验证历史。Reserve的新请求必须填完整组域及revision/时刻；group_id可null表示确实公共默认。
4. 沿020取舍，定价时刻在授权/model/group锁后、钱包账户等待前；钱包等待跨调价生效不会重新定价。最后账户锁后仍验证Key/request/资金时限；B周期归属可以采用更晚的准入时刻，两个时间含义必须分别保存。
5. RecordAttempt、PrepareDispatch、Settle/Release/Refund/Recover仅使用已冻结客户售价；不重新应用当前组。客户一次收费，采购每attempt价格仍按018/019独立冻结。

### 2.3 目录与精确API

匿名`GET /api/public/models[/{id}]`保持020字段和契约，只查`price_group_id IS NULL`。不增加group查询、Cookie个性化、组价或其他客户标识。公开模型与默认价无关：缺默认价仍可显示unavailable，不得借分组存在有效价显示默认可用。

已登录Cookie模型页展示当前tenant、已获授权模型的有效价格，使用相同组域/时间/排序。`/v1/models`保留原无售价字段，Key通过真实调用冻结最终价；平台旧模型页和旧价格历史固定默认域，客户旧价格历史仅当前域已生效版本。安全`RetailPriceView={model_id,source,status,price_group_id,price_version_id,currency,input_rate,output_rate,effective_at,as_of}`：source=`default|group`、status=`available|unavailable`；unavailable时version/rates/effective_at全null，group保持本tenant域，currency=CNY；available时字段齐全。as_of为本次DB观察时刻，报价不是可兑换的锁价凭证。

| API | 输入 / 权限 / 返回 |
| --- | --- |
| GET/POST `/api/platform/price-groups` | GET admin/finance只读GroupView列表；POST admin `{operation_id,label}`，创建active/revision1 |
| GET/PUT `/api/platform/price-groups/{id}` | GET admin/finance；PUT admin `{operation_id,expected_revision,label,state}`完整CAS，retired不可恢复 |
| GET `/api/platform/price-groups/{id}/price-versions` | admin/finance，固定分页GroupPriceView；不含供应商成本 |
| POST 同路径 | admin `{operation_id,model_id,input_rate,output_rate,effective_at}`；组active、有效profile、规范两维价，唯一时刻冲突409 `price_effective_conflict` |
| GET/PUT `/api/platform/tenants/{id}/pricing` | GET admin/finance；PUT admin `{operation_id,expected_revision,price_group_id}`，null明确恢复默认；只可新分配active组 |
| GET `/api/tenant/pricing` | 当前owner/billing_viewer自己的AssignmentView，不可指定tenant；member通过其有权模型价格了解实际报价 |
| GET `/api/models/{id}/retail-price` | 当前tenant有效成员、对模型可见；返回RetailPriceView，跨tenant/未授权资源不泄漏组价 |
| GET `/api/platform/pricing-operations/{operation_id}` | 当前admin/finance安全原操作回执，不返回原始任意body |

PUT组/tenant旧CAS409 `pricing_revision_conflict`；退休组新分配/发布409 `price_group_retired`。缺价预览按A0第6节固定当前tenant已授予/启用模型、单SQL时刻与context、50页，允许管理员明确分配缺价组，但不承诺后续调用可用。报价/preview/operation/管理准入按A0第6节，不共享推理限流。

## 3. B：周期预算不是余额

### 3.1 稳定控制器、窗口和归属

拟议`periodic_budget_controls`每个scope稳定唯一：`tenant:<id>|key:<id>|procurement:<id>`。持久ID不可删除/换名以重置，period_kind初次配置后不可修改；默认未配置不限制。配置有独立revision及不可变版本；enabled/limit实际变更递增，不随流量变化。旧Key lifetime_limit及019累计limit继续生效。

拟议`periodic_budget_windows(control_id,start_at,end_at,spent_amount,held_amount)`唯一(control,start)，`periodic_budget_allocations`唯一(scope,request/attempt)绑定original window、原上界与当前held/spent，`periodic_budget_events`追加不可变、唯一effect key、held/spent delta与原资金证据。各窗口与控制器汇总必须由事件精确核验，不能把reset定时任务当事实源。

客户新Reserve为tenant+Key各分配一次R；R为同一客户冻结额，两个上限分别计量，不是扣钱包两次。安全第二attempt不创建新客户allocation。采购RecordAttempt为实际供应商attempt分配一次U；两个attempt可以属于不同周期/采购账户，不能把客户request期强加给采购。

窗口在已持control的事务中按最终DB时刻惰性创建，不依赖worker午夜运行。全部参与控制器、资金账户和其他会等待的预算行锁取得后，新语句获取`admitted_at`，当前窗必须满足`start<=admitted_at<end`；如预读候选窗口过界则在相同控制器锁下重新派生，不能继续用旧now。request/attempt保存原窗ID和准入时刻，之后不可换期。

### 3.2 占用公式与跨期矩阵

对已启用scope在准入时刻t：

```text
current = 唯一包含t的窗口
carry_held = 该control全部更早窗口及legacy的未决held之和
occupied = current.spent + current.held + carry_held
允许新R/U ⇔ occupied + extra <= 当前配置limit
```

所有金额均是非负精确整数。未来窗口不预创建消费；时间回退到更早窗口不得重新打开已封闭周期，应fail closed并记录时钟异常，恢复合同冻结前不得用进程时间容错。control保存单调`max_observed_window_start`，它不替代DB时钟或资金账本。

| 事件 | 原窗口 | 当前/后继窗口 |
| --- | --- | --- |
| 本期新Reserve/RecordAttempt | held += R/U | 无其他期增量 |
| 跨期仍未完成/unknown | 保持原held | 只作为carry参与准入，无复制事件 |
| 可信客户结算C | 原held -= R，原spent += C | 不增加新期spent；carry因旧held结束而减少 |
| 客户释放/24h截止 | 原held -= R，spent不增 | 不自动释放采购carry |
| 客户消费退款 | 钱包/022来源补偿；周期spent不减 | 不增加当前额度、不重新激活过期套餐 |
| 采购可信成本/双审更正 | 原attempt期held/spent按019绝对成本差额更新 | 不转移到对账发生期；旧held结束才释放carry |
| 采购冲突 | 保留spent并恢复019保守held；异常暂停账户 | carry继续，不能借月底消失 |
| 关/开、重启、自然过期 | 只变配置/时钟，不清历史 | 当前期已有spent和全部未决held继续可见 |

例：月末旧请求held=100、新月limit=100，结果未知时新接单可用为0。旧请求后来结算70计旧月，held变0，新月可再接100。这是按准入归属期的预算；并非“新月输出/供应商账单总额必≤100”。

### 3.3 收紧、关闭和首次启用

- 未配置scope没有calendar，也不宣称已跟踪日/月花费。首次配置时在稳定同步点给其全部现存客户reservation或采购reservation建立不猜历史的legacy held引用；不重记spent、不变客户24h截止。已终结旧消费不推测归属期。B0必须冻结完整扫描与新Reserve/finish串行的方案（例如tenant首配使用tenant UPDATE屏障，Key首配使用Key UPDATE，采购沿account锁），或采用排空维护，不能无锁枚举后漏掉并发旧held。配置确定calendar及tracking_started_at，此后即使enabled=false也记录新allocation和结算。
- 首次配置limit只在下一完整UTC日/月边界启用，effective_from由服务器确定；不能立即把不完整旧窗口当0消费。配置和页面显示scheduled。后续关闭立即停止该周期cap门禁但继续跟踪；再次开启沿原calendar/窗口、不重置，并沿已记录事实立即生效。
- period_kind不可修改；需要不同周期必须另立迁移/重叠约束合同，不能删除旧controller再建。未实现自定义timezone、周期起点或手动reset入口。
- 当前limit可以降低到occupied以下，保存后新Reserve和尚未sending的PrepareDispatch按当前占用拒绝；既有占用不撤回/不重算。正常disabled表示该周期cap不限制，不解除钱包、Key lifetime或采购累计上限。
- 已RecordAttempt但未sending：不搬迁原allocation；PrepareDispatch检查当前配置/occupied（extra=0）及原allocation完整性。若被新cap阻断且未获发送许可，沿原not_sent释放；不自动创建新request/attempt。全部锁后检查时钟/截止，sending提交后的已发收尾不再受新cap阻挡。

### 3.4 权限和API草案

`PeriodicBudgetView={scope,scope_id,revision,period_kind,enabled,limit_amount,state,effective_from,tracking_started_at,current_window,carry_held,as_of}`。scope=`tenant|key|procurement`；state=`unconfigured|scheduled|active|disabled`。unconfigured时period_kind/limit_amount/effective_from/tracking_started_at/current_window均null，enabled=false；其carry_held=`"0"`只表示尚无周期账簿，不表示钱包或采购没有held，页面须标未配置而非“本期无消费”。其余有固定period_kind、限额、tracking_started_at和首次effective_from。current_window为空仅在尚无该期已跟踪事件/记录时允许，但不能把已有legacy carry变0。

`BudgetWindowView={id,start_at,end_at,spent_amount,held_amount}`；金额均NUMERIC非负字符串。`carry_held`非负字符串，as_of单DB时刻，当前窗口需包含as_of；current_window null的页面显示“当前窗尚无事件”，不能自造spent=0来宣称从月初完整统计。`BudgetAllocationView={id,subject_kind,subject_id,window_id,upper_amount,held_amount,spent_amount,state,created_at}`；subject_kind=`customer_request|supplier_attempt|legacy_customer|legacy_supplier`，legacy window_id可null，其余必须UUID；state和原资金状态对应，精确枚举在B0实现合同冻结。

| API | 访问范围 |
| --- | --- |
| GET/PUT `/api/platform/tenants/{id}/periodic-budget` | admin写、admin/finance读tenant硬cap |
| GET `/api/wallet/periodic-budget` | owner/billing_viewer本tenant，只读，无tenant参数 |
| GET/PUT `/api/keys/{id}/periodic-budget` | 按017当前owner/own member写、原安全Key visibility读；不能修改tenant cap或Key lifetime |
| GET/PUT `/api/platform/procurement-accounts/{id}/periodic-budget` | admin写、admin/finance读；客户无权 |
| GET 各上述资源`/windows`与`/allocations` | 相同当前读权限与固定分页；客户scope不输出采购事实 |
| GET `/api/periodic-budget-operations/{operation_id}` | 当前原scope可读者核查原命令，跨scope404；B0应冻结平台/tenant的具体路由分离 |

PUT精确`{operation_id,expected_revision,period_kind,enabled,limit_amount}`，limit非负int64字符串，period_kind首次确定后须原值。服务器决定effective_from，不接受spent/held/reset_at/window_id或任意客户端时区。旧CAS409 `periodic_budget_conflict`；换calendar409 `periodic_calendar_immutable`。公开推理侧客户cap不足402 `periodic_budget_exceeded`，采购不足沿503受控路由失败，不暴露供应商账户信息；未知/约束故障503 fail closed。B0须冻结gateway的具体失败映射及可安全另选候选条件，不能在已获sending许可后重发。

## 4. B锁图与事务所有权（拟议，实施前须真实PG证明）

现有代码事实见[Reserve](../../internal/billing/reserve.go)、[lockPrincipal/账户](../../internal/billing/store.go)、[lockRequest/PrepareDispatch](../../internal/billing/evidence.go)、[finish](../../internal/billing/settle.go)、[采购](../../internal/billing/procurement.go)。不能把reservation放到tenant之前；model升级必须在新增budget/account锁之前完成。

| 路径 | 目标顺序与边界 |
| --- | --- |
| 客户Reserve | 原tenant→membership→user→Key UPDATE→model SHARE→A组SHARE→客户controls按稳定ID排序→客户ledger accounts按ID排序→窗口/新allocation→最后DB时刻及完整准入→同tx资金与预算事件 |
| 客户finish/Release/Refund | 原lockRequest父行→reservation→必要model升级→客户controls同序→客户accounts同序→原窗口/allocation；收尾只锁/写事实，不调用当前cap门禁 |
| RecordAttempt采购预占 | 原tenant/Key/model/channel/credential→客户reservation→attempt→019采购account NO KEY UPDATE→采购control→采购reservation/窗口→最终DB时刻；不在持采购account时回锁客户control |
| PrepareDispatch | 原父行/channel/credential→客户reservation→attempt→客户controls→采购account→采购control→采购reservation/所需窗口→所有等待后当前时刻与两侧cap/原allocation校验→sending；涉及多个control同层稳定排序 |
| 采购证据/更正/恢复 | 既有业务根/客户父行与reservation→attempt→采购account→采购control→采购reservation/原窗；独立提交后才做客户Settle，不能持供应商锁进入客户预算/ledger |
| tenant/Key预算配置 | admin或tenant当前授权父锁→如需Key锁→目标control UPDATE；不反向锁model/reservation/account |
| 采购预算配置 | admin身份→采购account NO KEY UPDATE→采购control UPDATE；后面不访问channel/tenant |

control保护其全部窗口，所有window写必须先持同control。客户和采购control不共用tenant聚合锁；中途不能按通用“全部预算排序”把procurement control移到account前。legacy allocation迁移的外键和新窗口插入隐含KEY SHARE也须纳入真实锁图。资金/预算事件、allocation和窗口汇总同一pgx.Tx，helper不自开tx、不异步emit、不持锁网络请求。

## 5. C：期限商品与订阅方向，尚不可冻结实现

### 5.1 必须先具备的来源

022-C必须已覆盖全部旧/新钱包来源lot、推理Reserve/settle/release/消费refund、legacy、原路refund、chargeback及债务门禁，且实际PG/恢复核验通过。本节不能把lot表存在当作依赖完成。

首片C1拟为固定期限、非自动续费的CNY调用额度商品。不可变版本含`product_id,version,display_name,purchase_amount,allowance_amount,currency,term_kind,model_scope,grant_source_policy,refund_policy_version`；数量、价格/权益、范围、期限和退款规则发布后不PATCH。商品公开显示不是模型公开目录，不沿用020价格版本，也不改变tenant价格组或放宽模型授权。

权利源必须区分原支付cash lot、兑换券/运营noncash lot、legacy和期限allowance lot。一个capture只能完成一种冻结商品效果；不能既把完整款项入钱包，又无偿激活同额权益。商品购买如支持钱包支付，必须从022的原来源分配扣取并永久保存映射；grant不能洗成另一种现金来源。

### 5.2 状态与期末held

拟议购买意图`created→payment_pending→active`，或`closed/review_required`；支付状态、权益状态和退款/拒付证据分开，浏览器返回不激活。term唯一键`(subscription_id,term_index)`，权威payment effect与term grant同tx唯一建立，原operation重读不再发一份权益。

term资格 `[starts_at,ends_at)`，已发行权益分为available/held/consumed/expired；到期只终止新的准入，不强制改写已sending的收费事实。到期前已预占、未sending跨期的请求必须在PrepareDispatch重验权益资格，失效走not_sent；已sending继续按原窗口/原来源收尾。

| 行为 | 期限权益效果 |
| --- | --- |
| 自然到期 | 未预占available转expired，不转普通钱包现金；held保持原来源直至原请求完成/24h截止 |
| 到期后原请求释放 | 回原lot的expired桶，不重新提供可调用余额 |
| 原请求可信结算 | 使用原held，不因后台处理晚而改用新term/新售价 |
| 消费退款 | 回原来源与原期限；过期权益仍过期，不凭退款续期或变现金 |
| 显式续期 | 新term、新授权payment effect及新grant；旧held仍在旧term，不能吞掉/复制 |
| 取消后续续期 | 禁止创建未来term，不撤回本期已消费/预占；本期现金退款另走冻结退款合同 |
| 原路退款/拒付 | 仅原支付的合法可退/可追回价值及原分配；其他付款、券或legacy不能补足，已发模型请求继续有利收尾 |

### 5.3 C0仍需单独冻结的决策

1. purchase是否只允许权威支付capture，还是同时允许钱包支付；不同输入来源、发行grant、服务消费和退款分别有哪些平衡分录，避免重复收入/钱包重复credit。
2. purchase_amount与allowance_amount不等时的现金退款上限、累计取整、费用/部分消耗映射、拒付债务比例；未定义前不能按剩余赠额或钱包余额计算现金退款。
3. 到期available转expired时的ledger/lot同事务以及AVAILABLE聚合方式，避免界面总余额仍包含不可花权益；全部旧预占/消费refund与C新lot的守恒证明。
4. 商品model_scope变更、已购买范围保留、现有授权收回的优先级；首片固定交集，不自动授予新模型/新价格组。
5. term calendar、购买时起算还是固定锚点、月底/闰年、提前续期、重叠套餐消耗顺序、锁等待跨到期与恢复旧备份；不得靠浏览器倒计时推进。
6. 支付off/恢复模式/购买者停用后的既有财务证据收尾，以及不可现金退来源的取消补偿；不自动吞掉其他tenant资产或另发现金。

上述未冻结前，不提供可用的购买/自动续费/强制激活接口；C API/精确DTO、DDL、分录和锁图作为C0产出单独追加，不把本段意向当实施合同。

## 6. 控制台、恢复与冻结门禁

A管理页显示公共默认与分组域、完整价格版本和未来生效时间；tenant分配确认当前组、目标组及缺价影响。客户模型页显示“本租户当前报价”，匿名目录明确“公共默认价，账号实际价以当前租户与请求冻结版本为准”。不暴露其他组或采购费率。

B页面分别显示当前期起止、当期spent/held、跨期carry、限额及DB观察时间；scheduled/disabled不伪装成0占用。Key、tenant、采购分别编辑且独立revision，供应商unknown不能显示为0。金额全程字符串/BigInt。关闭、切页、身份/租户/角色变化使编辑epoch失效；未知写只查原operation，不用最新配置值冒充原回执。

迁移由维护角色追加并排空，旧行不猜历史组或周期；报表重建从不可变快照/预算事件核验，不能改钱包或授权。恢复旧备份先沿原恢复锁存核对离库变更与外部事实，不能因为某期窗口不存在而重新送模型、重新发权益或重置采购unknown。

A0/B0需公布实际Go helper/DDL/角色/完整HTTP schemas、有界准入及新增锁图，并完成专项PG反例审查。C0需另行接受上节全部决策。每次实施/测试/迁移/部署/设计合同变更最终由集成人复核统一架构并显式refresh，CI只读；本草案不执行refresh。

# 028-U：统一计量实施消费者清单

2026-11-04；状态：**只读代码盘点及实施分工输入，尚未实现028-U**。盘点基线为 `be601a0`，以[028规格](spec.md)、[整体设计](../../docs/design/integrated-wallet-media.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)为准。共同包导出签名、DTO和正式迁移由集成人统一冻结；本清单不发行供应商资格，不证明商业或生产验收。

## 1. 按实际装配区分现行与冻结研究

2026-11-05实施进展附记（下表仍保留最初盘点语境）：当前Selection已改为完整Schema/Rates和原capability/default身份，客户Reserve直接ReservationAmount；Selection、Reservation和financialRequest两价镜像已删除。结算/attempt成本/采购实际金额直接读原Rates和对应侧终态Quantities；采购准入/完整Quote已在本次续片接入；发布operation指纹及HTTP/TS全面替换仍未完成。历史前缀仅因删回执字段把必要费率断言移到原request旧列，迁移范围/自由版本/pins不变，不表示当前reader兼容历史库。阶段状态以[tasks](tasks.md)和[验证](../../docs/verification/unified-metering.md)为准，不能由本附记勾选生产出口。

采购U数值已改读原attempt完整supplier界和同域原Rates，数据库新增独立数值guard；采购预期Quote及派发检查已在本次续片切完整原件，详细范围见下段。实际管理端[采购预占列表](../../internal/procurement/store.go)已按存储合同7.2切换原attempt快照/界行，返回supplier Bounds wire及原profile/schema/default，原旧列503已修复；TS/UI同步撤旧字段，缺失/错绑整页失败。[受限APP读取专项](../../internal/server/procurement_meter_integration_test.go)只证明当前局部出口，冻结研究reader仍保持原合同，不顺手改成当前读取。

2026-11-05完整报价续片已将[routecost](../../internal/routecost/quote.go)接入A类[catalog候选](../../internal/catalog/store.go)、[routing](../../internal/routing/cost.go)、[billing报价复验](../../internal/billing/cost_policy.go)与[采购锁后](../../internal/billing/procurement.go)，删除routing.CostQuote/CostScore与内部两价cap镜像。读取原完整canonical/digest，精确有理cap和CompareExactCost不反算每百万，金额只委托metering。普通Store已无prospective分支；显式[ProspectiveStore](../../internal/billing/prospective.go)只接受独立冻结输入，不接受新Quote或提升当前资金方法。它不是B类兼容层，不增报价身份，也不代表已收口全部HTTP/发布指纹/完整guard。

当前[受限APP报价链](../../internal/server/cost_vector_application_integration_test.go)以SET ROLE验证六文本paid/known-zero与unknown政策，另有原价排期跨锁测试；不是独立LOGIN或完整六文本商业出口。B类历史SQL/旧列/迁移范围/pins不升级；原来把current billing当历史业务oracle的组合，现经[test-only编译拒绝边界](../../internal/testsupport/frozenattempt/attempt.go)明确失败，**仅编译，没有恢复历史业务oracle**，也没有以固定错误冒充原业务结果。显式冻结适配器的13参字节单测仍独立通过，实际原库验收须另补；periodicbudget现行billing夹具使用当前canonical测试选择器，55439缺库仍只编译。

`serve` 的实际路径是 [main](../../cmd/moni-aigate/main.go) → [server.New](../../internal/server/server.go) → `catalog.New`、`billing.New`、`procurement.New`、`provider.NewClient`、`routing.NewEngine`。六文本入口由 [gateway](../../internal/server/gateway.go) 编排。现行钱包来源 HTTP 及 `check-wallet-sources` CLI 直接调用 [Store.CheckWalletSources](../../internal/billing/wallet_sources_read.go)，不是扫描旧 proof/profile 后取得运行权。

必须区分以下三类，不以文件位于 `internal/billing` 或存在静态 import 作为在线接线证据：

| 分类 | 消费者与接线 | 028-U处理 |
| --- | --- | --- |
| A：现行运行路径 | 普通构造器装配的六协议、目录/报价、路由、预占/证据/结算/采购、请求查询和控制台 | 同一次集成替换旧两价/两界/Usage；现行内部DTO不保留旧字段联合、自动fallback或第二套目录 |
| B：冻结研究，保持编译与原拒绝边界 | `billing/funding_financial_read.go`、`billing/procurement_attempt_read.go` → `maintenancescan`/`maintenancetrust` 显式研究接口；`fundinglotscan` → 独立研究组合；`billing.NewProspective`/`procurement.NewProspective` → 显式可选SQL原型 | **不是027线上checker，不要求升级到028向量兼容矩阵**。保留冻结材料与历史fixture，current源/历史不符必须拒绝；不能借028重新签pins或把历史验收升级为current验收 |
| C：测试材料 | 普通构造器下当前迁移fixture、协议HTTP/SSE fixture、前端DTO fixture，以及B类独立历史fixture | A类fixture随当前合同升级；B类保留其精确历史范围，断言新current被拒绝。两类不得共用“latest迁移”助手冒充相同前提 |

B类定位依据：[金融事实维护组合](../../internal/maintenancescan/funding_financial.go)显式调用 `ReadFundingFinancialFacts`；[采购维护组合](../../internal/maintenancescan/procurement.go)显式调用 `ReadProcurementAttemptBindingFacts`；普通 `server.New` 不调用这些组合、`fundinglotscan` 或 `NewProspective`。现行 [CheckWalletSources](../../internal/billing/wallet_sources_read.go) 检查023正式来源、lot、分配、账本及余额合同，不应被旧静态扫描替换。

## 2. A类：必须一起替换的当前消费者

| 模块 | 现有标量合同/入口 | 统一后责任与不可丢失边界 |
| --- | --- | --- |
| 请求与Profile | [provider/types](../../internal/provider/types.go)、[request](../../internal/provider/request.go)、[native_request](../../internal/provider/native_request.go)、[retrieval_request](../../internal/provider/retrieval_request.go)、[gemini_request](../../internal/provider/gemini_request.go) 的 `InputTokenUpperBound`、`InputUpperBound`、`MaxOutputTokens` 及公开可写Usage | 共同不可变Profile/BoundPlan/Observation；协议参数仍有各自严格校验。token上下文和输出限制可以作为文本协议资源参数保留，但不能继续充当第二套财务界或报价字段 |
| 使用量与原生响应 | [usage](../../internal/provider/usage.go)、[native_usage](../../internal/provider/native_usage.go)、[gemini_usage](../../internal/provider/gemini_usage.go)、[retrieval_response](../../internal/provider/retrieval_response.go)；Response/Event携带标量Usage | 在协议边界构造统一两维文本向量；安全诊断分拆仍由provider严格解析及原生wire保留，共同Observation只持归一化收费维度和已设计元数据，不另加diag map。原生JSON/SSE字段名及包含关系不改，missing不能填0 |
| 编排与探测 | [gateway](../../internal/server/gateway.go) 的候选两界、ReserveInput、RecordAttempt CostQuote和 `billingUsage` 六字段复制；[probe](../../internal/server/probe.go) | 六协议接共同计划、快照和观察；HMAC统一绑定协议/执行种类/模型/有效输入/计划。探测复用解析与观察，不产生客户消费或伪造采购成本 |
| 售价选择 | [pricing](../../internal/pricing/pricing.go) Selection/SelectEffectiveTx 的两rate | schema/variant/完整Rates选择；保留精确价格组域、组缺价不回落默认、数据库pricedAt在等钱包前冻结 |
| 模型与候选目录 | [catalog/store](../../internal/catalog/store.go) ModelInput/CreateModel、PublishPrice、BindingInput、CandidateSnapshot | 发布不可变schema/profile与有限variant，验证完整mask及合法最大R/U能放入int64；候选快照携带同绑定供应商Rates与维度cap |
| 价格组及回执 | [pricing_types](../../internal/catalog/pricing_types.go)、[pricing_commands](../../internal/catalog/pricing_commands.go)、[pricing_queries](../../internal/catalog/pricing_queries.go) 的两rate、发布fingerprint、result_snapshot、retailPriceProjection、preview | 发布safe input、规范指纹、结果快照严格解码、价格历史、租户预览与当前报价统一切换；不能保留旧回执fallback |
| 公共目录 | [public_catalog](../../internal/catalog/public_catalog.go) PublicLimits/PublicPricing及公共SQL | 公开单位、schema/variant、有限能力和默认零售价；租户组价仍只在受权私有目录显示。缺价rates为空，明确零价仍available |
| 供应商成本发布 | [catalog/cost_policy](../../internal/catalog/cost_policy.go) CostVersionInput/PublishCostVersion | known必须完整supplier mask；unknown是明确未知快照，不用0费率代替缺价；原attempt不追溯改价 |
| 成本路由与预筛 | [routing/cost](../../internal/routing/cost.go)、[routing/routing](../../internal/routing/routing.go)、[routing/procurement](../../internal/routing/procurement.go) 的两rate/cap/界、CostScore和U计算 | 同最高priority内使用 `metering.CompareExactCost` 比完整最坏采购精确费用，再按权重处理并列；不能先ceil再比。采购快照预筛不代替财务锁后准入 |
| 客户预占 | [billing/types](../../internal/billing/types.go)、[billing/reserve](../../internal/billing/reserve.go) 的ReserveInput两界、Reservation两价、profile JSON私有解码及request_records INSERT | DB重读原发布合同并验证BoundPlan后自行计算R；调用方金额不授权。027 lot、Key、周期预算仍消费一个R金额，不按维度分别扣款 |
| attempt与最终证据 | [billing/evidence](../../internal/billing/evidence.go)、[billing/cost_policy](../../internal/billing/cost_policy.go) 的financialRequest/attemptCost、currentAttemptCostSQL、normalized_usage摘要/冲突和发送前复验 | 冻结完整原supplier quote及bound；证据身份绑定request/attempt/schema/profile/variant，完整相同身份异体冲突，不覆盖原件；发送前复验不得拿当前价改写原快照 |
| 唯一结算 | [billing/store](../../internal/billing/store.go)、[billing/settle](../../internal/billing/settle.go) 的Calculate/rawAmount/validateUsage、finalizationRow及标量费用计算 | 删除在线旧公式和重复Usage，复用metering；实际量和原费用保持真实，客户最多R；唯一settlement、24小时截止和027原来源收尾保持 |
| 采购预算及更正 | [billing/procurement](../../internal/billing/procurement.go) 从request读两界、Calculate得U、从attempt读两价、非零token/超界判断；[procurement/store](../../internal/procurement/store.go) reservation SQL | 完整供应商向量算U/实际费用，超界及超U保留异常；客户释放/退款不释放采购unknown。双审成本更正仍是金额与证据，不拆成每维资金操作 |
| HTTP写入与读端 | [catalog_handlers](../../internal/server/catalog_handlers.go)、[pricing_handlers](../../internal/server/pricing_handlers.go)、[reports](../../internal/server/reports.go)、[queries](../../internal/server/queries.go)、[billing_handlers](../../internal/server/billing_handlers.go) | 同步严格字段白名单、rate/quantity规范字符串解析、人工核对、模型/价格/请求查询；移除normalized_usage中两个token专用JSON提取。禁止以JS Number/float处理有理率或大数量 |
| 资金恢复与退款 | [billing](../../internal/billing)现行恢复、退款、来源分配及投影调用 | 沿原request/settlement和金额事实；凡读取最终量/quote的地方改为原向量快照。只读恢复不能重发模型请求；纯金额接口不因向量化另建旁路 |

### 2.1 六文本协议到统一向量的保持项

每个文本schema都显式包含 `text_input_token/text_output_token`，retrieval也保留零输出。cache/reasoning是受控诊断及已声明包含关系，当前不提供缓存折扣、不重复计费。

| 协议 | 归一化与完整性 |
| --- | --- |
| Chat / Responses | prompt/input → input，completion/output → output；total=input+output，cached≤input，reasoning≤output。要求的主量缺失/null不同于0；累计快照替换且不下降。Responses终态自身必须完整，不能借旧provisional伪装最终量 |
| Messages | provider先严格验证base/read/create及已支持5m/1h拆分的状态/包含关系，再令input=base+cache_creation+cache_read，output维持原值；共同Observation仅接归一化收费维度。安全split仍保留在provider受限解析数据及原生wire，不另在Observation增加未设计诊断map，不从inclusive input反推。delta输入/cache的null表示无更新，output终态必须有值，累计量不回退；当前拒绝的收费工具/thinking扩展不因向量化放行 |
| Gemini | input=prompt，output=candidates+thoughts；thoughts为output子集、cached为prompt子集。检查candidates独立单调，防thoughts增长掩盖下降；finish后接usage-only帧，干净EOF后完成。非TEXT modality及未知收费工具仍拒绝 |
| Embeddings / Rerank | input是整批或query+全部documents的总量；output明确0界/0价/0量。Embeddings要求prompt+total；Rerank要求total。不能通过缺失output绕过完整mask |

直接复用 [metering](../../internal/metering) 的ParseSchema/Bounds/Quantities/Rates、canonical与摘要、ReservationAmount/Amount/CompareExactCost/WithinBounds。全部维度求和后一次ceil；完整Rates与明确0不同于未知。实际量可超过界并保留大整数费用，再以WithinBounds标异常，不把量截成界。纯核不认证供应商来源；Observation保留missing/provisional/conflicting，仅完整可信终态可生成FinalQuantities。

### 2.2 新迁移须替换的旧SQL合同

已应用SQL只保留原件，新迁移由一位集成人追加。不能只DROP列而留下触发器、函数或动态SQL依赖：

- [001_core](../../migrations/001_core.sql)、[002_billing](../../migrations/002_billing.sql)、[003_operations](../../migrations/003_operations.sql)：price/cost两价、request两价两界、attempt成本快照、旧normalized_usage及人工核对证据结构，由现行统一表/快照/quantity行合同接替。
- [014_procurement_accounts](../../migrations/014_procurement_accounts.sql)：采购input_bound/output_bound及不可变identity guard。预约/账户/事件金额状态机与投影守卫继续保持，不能为向量化削弱。
- [018_tenant_pricing](../../migrations/018_tenant_pricing.sql)：input-only零输出价、request原售价不可变和price version对照、pricing_price_result、发布fingerprint、安全输入shape、result_snapshot守卫全部同步重建。
- 023正式钱包来源的lot/余额/账本/分配约束不直接依赖两价字段；保留其金额不变量并做当前PG回归，不迁回旧研究模型。

### 2.3 前端与HTTP需原子对齐的面

严格DTO入口为 [pricing.ts](../../web/src/pricing.ts)、[customer-pricing.ts](../../web/src/customer-pricing.ts)、[public-catalog.ts](../../web/src/public-catalog.ts)、[model-contract.ts](../../web/src/model-contract.ts)、[cost-aware-routing.ts](../../web/src/cost-aware-routing.ts)、[procurement.ts](../../web/src/procurement.ts)。不仅替换显示列，还要替换发布command/fingerprint、严格keys集合、回执比较、未知提交锁定后的只读核查、模型当前报价一致性。

对应界面为PricingWriter/TenantPricing/CostEditor/RouteEditor，以及Platform/Models/Pricing/PriceGroups/Requests/Playground。Requests展示schema/variant、每维单位/数量可信度和原报价；采购reservation不再返回伪通用input_bound/output_bound。单位来自固定字典，非token不能显示成0输入/输出token。原生playground请求字段仍遵守各协议，不把内部向量DTO发给供应商。

## 3. B类：冻结研究只保持原范围与明确拒绝current

以下文件中的标量是**冻结历史输入格式**，不是028还要保留的现行合同：

- [funding_financial_read](../../internal/billing/funding_financial_read.go)、[procurement_attempt_read](../../internal/billing/procurement_attempt_read.go)及其raw facts，与[maintenancescan](../../internal/maintenancescan)研究接口保持原范围；它们不被普通serve或 `check-wallet-sources` 调用。
- [fundinglotscan](../../internal/fundinglotscan) 的固定投影、[fundinglotverify](../../internal/fundinglotverify) 对应纯行算法及独立runtime/proof/profile组合保持冻结；不把current新快照转换回两价两界喂给它们，不新增旧scan兼容矩阵。
- [billing/prospective](../../internal/billing/prospective.go)、[procurement/prospective](../../internal/procurement/prospective.go)、[procurementexecution](../../internal/procurementexecution) 的CostQuote/13参SQL/观察器属于显式原型，普通构造器不装配。若共同现行类型替换引起编译依赖，应隔离原型自己的冻结DTO/接缝或明确不接受current；不得为编译保留在线scalar分支、重新资格认定旧SQL原型或旁路执行资金操作。

迁移001–023原材料不改；要求exact original22的研究继续使用 [Original22材料](../../migrations/original22.go)，不能把023或未来迁移追加到其“原22”列表。current源码/迁移集合发生变化后，旧exporter/catalog/source pins按原合同拒绝current是正确边界，不用刷新pins制造通过。冻结历史fixture只验证其声明的历史对象，不能据此称028当前库兼容或027线上checker通过。任何当前入口都不得绕过历史/源绑定检查直调旧reader并误报完整系统认证。

## 4. 026经营报表：保留金额读链，补计划元数据

实际接线是 [operating_reports handler](../../internal/server/operating_reports.go) → [reporting.Read](../../internal/reporting/read.go) → [aggregate](../../internal/reporting/report.go)/[CSV](../../internal/reporting/csv.go) → [OperatingReport](../../web/src/components/OperatingReport.vue)。该读链未消费两价、token量或旧normalized_usage；本盘点没有发现需要因向量化重写的现行金额算法，也未确认收入重复或未知成本算零的缺陷。

现有口径保持：同一RR READ ONLY、5秒预算，31天/5000请求上限；按request创建窗口读唯一settlement一次，所有生成attempt成本逐个累加；未知成本/未结算使毛利为null。异步提交按原request归窗，实时按收费轮次request归窗，poll/下载/heartbeat不加收入或生成attempt。收入与成本不从向量重算，不join来源分配边造成金额倍增。

028 已接通请求元数据合同，当前仍需持续回归其不可变快照边界：

1. 从原不可变request快照读取protocol、execution_kind、profile_version、schema_id/schema_digest、variant_key；不能从模型当前profile推断历史请求，也不以缺失值默认Chat。当前请求明细、报表JSON和CSV已沿该快照接线，缺失仍留空。
2. protocol/execution_kind筛选在同一cohort SQL中生效，并保留当前资源上限；Filter.Validate、HTTP严格query集合、JSON/TS类型、UI筛选和CSV精确列合同已同步。CSV扩列继续保持固定顺序，元数据文本继续防公式注入。
3. 请求下钻关联原retail/supplier快照及最终证据，显示缺量/冲突/未知状态；客户视图不泄露上游凭据、成本或内部URL。现行金额汇总保持原来源，无需引入第二结算器。
4. U片不强制增加跨维度数量合计。如果后续增加，按 `(schema_digest,dimension,unit,side)` 分组，只读最终证据；任一缺量保留unknown计数/nullable总量，不把token、字符、图片、毫秒相加，不将金额报表里的0误当实际量0。

以上是当前回归边界，不再把元数据合同描述为待实现能力。不得用“报表目前无token列”跳过请求快照、CSV/HTTP与前端已经接入的统一设计；金额仍不从向量重算。

## 5. C类fixture、并行边界与集成验收

| Fixture分类 | 实施动作 | 验收含义 |
| --- | --- | --- |
| 当前普通构造器PG fixture | 使用完整current迁移，更新模型/profile、售价/成本、request/attempt/证据；覆盖默认/组价、报价完整性、采购预占/未知独立收尾、超界、退款原来源 | 当前合同的限定本地财务验收；必须专用PG，SKIP不能标商业链通过 |
| 当前协议/HTTP/UI fixture | 六协议零/缺失/null/冲突、cache/reasoning包含关系、累计与终态、未知协议拒绝；DTO严格解析、单位和大整数、发布结果不明不重放 | 证明本地实现及故障边界，不证明真实供应商上界或扣费 |
| 纯核等价fixture | 当前文本向量结果对照独立旧数学期望；包含单次ceil、精确成本并列、R/U溢出、actual超界 | 独立对照，不保留两套在线计费路径 |
| 冻结研究fixture | 明确应用其固定历史材料、独立schema；新current/source drift负控拒绝 | 只证明原历史研究范围；不升级旧scan/profile兼容矩阵，不签发当前运行权 |
| 026回归fixture | 原金额一次/成本全部、退款/未知/大整数和同快照；新增元数据按原request而非当前模型读取 | 金额口径保持，新元数据合同实施后另有断言 |

集成人先冻结 `internal/inference`、DTO、追加迁移和原快照身份，再按以下独占切面并行：

1. Provider负责人独占 `internal/provider` 及六协议测试；只依赖共同纯领域合同，不改资金实现。
2. 目录负责人独占 `internal/catalog`、`internal/pricing`、`internal/routing` 及其专项；费率/cap规范与报价摘要先和集成人一致。
3. 资金集成人独占 `internal/billing` 当前写链、`internal/procurement`、迁移及相应PG集成；保留027金额/来源约束。冻结研究只处理必要编译隔离和拒绝current，不扩范围。
4. 前端一位负责人独占相关 `web/src`；报表负责人可独占 `internal/reporting`，两者先对齐planned元数据/CSV合同。全部 `internal/server`、main及共享公共类型由同一集成人接线，避免gateway/probe/handler/查询并发覆盖。

最后汇总所有结果，对照本清单确认旧在线scalar引用已经移除、B类仍明确冻结、当前PG/HTTP/前端专项完成，再更新HLD/LLD与统一架构、变更记录及清单，由集成人执行架构refresh/check及最终检查。本文仅新增实施inventory，尚未改变运行模块或能力状态；其设计文件变化仍需进入集成人本轮架构复核。

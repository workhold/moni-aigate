# New API 能力对齐路线图

2026-11-01。开发者要求New API的能力也应支持，并明确“核心是完善所有能力，然后再推进性能优化”。本路线图将主要网关与商业运营能力纳入moni-aigate后续目标；核心接入、自助经营与扩展平台优先，002/008性能专项后移。参考设计后独立实现；当前v0.1不是New API的完整替代品。

状态以[001规格](../../specs/001-commercial-mvp/spec.md)、[实际验证](../verification/implementation.md)及下表为准。“已有实现”仅指对应窄范围代码，不表示生产验收或与New API完全等价。“规划”表示本次确定范围与顺序，尚须形成独立的spec/HLD/LLD/API/任务/验收后实施，不把本页当作全部详细合同。

2026-11-04排期按[ADR-005](../decisions/ADR-005-pre-mvp-single-path.md)调整：MVP前采用单一现行模型，避免旧版本兼容与双运行负担；历史证明候选暂缓扩展，先交付经营报表/成本分析、客户与渠道运营、商品/支付及单实例部署。下文A/B/C能力差距继续有效，历史排期不再要求先完成所有proof/profile才开发用户能力；详细以[当前交付顺序](delivery-plan.md#当前执行顺序2026-11-04-用户调整)为准。

## 1. 固定对照与能力差距

参考New API `e449cc700eb569cdfc2199cb73e97de46944f43c`，不是浮动最新版的无限功能承诺。主要证据：

- [README能力与协议表](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/README.md)。
- [relay-router.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/router/relay-router.go)：Chat、Responses、Messages、Gemini、Embeddings、Rerank、Audio、Realtime等入口。
- [api-router.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/router/api-router.go)：账号、OAuth/Passkey、充值、订阅、兑换码、签到与运营入口。
- [channel-router.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/router/channel-router.go)、[model/channel.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/channel.go)：渠道、模型发现、批量管理和多密钥。
- [service/billing.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing.go)：预扣/结算；已有深入阅读见[商业源码笔记](../research/implementation-auth-commerce-notes.md)。

| ID | 对齐能力 | moni-aigate当前 | 后续阶段 |
|---|---|---|---|
| NA-01 | 模型目录、Chat非流/SSE、函数工具 | 已有文本Chat、基础工具转发、目录；未覆盖所有扩展字段 | A：按profile扩展兼容字段和官方SDK契约 |
| NA-02 | OpenAI Responses、Anthropic Messages、Gemini原生协议 | [003](../../specs/003-native-protocols/spec.md)已有Responses/Messages文本/基础工具；[010](../../specs/010-gemini-native/spec.md)实现Gemini无状态纯文本JSON/SSE与计费，状态见[验收](../verification/gemini-native.md) | 正式供应商/SDK逐一认证；Gemini工具/多模态仍后续独立规格 |
| NA-03 | Embeddings、Rerank | [009](../../specs/009-retrieval-protocols/spec.md)实现OpenAI Embeddings与Jina token计量Rerank，含零输出计费/Playground；本地验收状态见[记录](../verification/retrieval-protocols.md) | A：正式供应商聚合输入硬界/计量逐家认证；Cohere search_units及媒体文档后续独立规格 |
| NA-04 | 多供应商接入、模型映射和能力配置 | 已有受控OpenAI-compatible、Responses、Anthropic及Gemini原生header/profile；没有正式供应商认证 | A：各供应商聚合输入/输出、usage和无消费拒绝逐一认证；Azure/Bedrock/Vertex等签名/部署差异随后接入 |
| NA-05 | 多渠道优先级/权重、健康、限流、故障切换 | 已有渠道优先级/权重及[012](../../specs/012-credential-pools/spec.md)成员二阶段权重、两层容量与成员健康；最多两次且不在同渠道换Key，准入attempt保存每渠道安全解释及实际凭据身份；[018](../../specs/018-cost-aware-routing/spec.md)新增成本优先/未知价格门禁/逐绑定采购单价上限，限定本地验收见[018记录](../verification/cost-aware-routing.md) | A：补无attempt的完整失败池解释/分模型限制；保留消费未知不重放 |
| NA-06 | 多密钥账号池、批量运营、标签、模型发现/同步、渠道余额 | [011](../../specs/011-model-discovery/spec.md)三目录发现/差异/历史；[012](../../specs/012-credential-pools/spec.md)1..32成员不可变池/CAS/永久撤销；[013](../../specs/013-channel-batch-operations/spec.md)当前页批量启停/预检、原子版本+管理revision校验、幂等回执/历史（最终验收见[记录](../verification/channel-operations.md)）；无自动同步、渠道标签体系或余额查询；共享采购账户/预算已补[019已实现](../verification/procurement-accounts.md) | A：继续供应商profile与其余渠道运营；新池/绑定发布保持显式完整revision，enabled实时门禁单独管理；余额仅对供应商支持的查询提供 |
| NA-07 | 用户/分组、Key模型/IP/预算/速率限制 | 已有租户、成员/模型授权；[015](../../specs/015-key-access-policy/spec.md)来源IP/CIDR、[016](../../specs/016-key-runtime-limits/spec.md)RPM/burst/concurrency及[017](../../specs/017-key-configuration/spec.md)名称/模型/地域/lifetime额度/期限均有独立GET/CAS编辑；017限定本地验收通过，含专项/全量真实PG、前端strict构建、隔离浏览器及预览升级，见[记录](../verification/key-configuration.md)；023-A单租户价格组已完成[限定本地验收](../verification/tenant-pricing.md)，周期预算020/HTTP/UI已实现并完成专项/浏览器与预览验证，期限商品未实现 | B分组商品和周期预算，区分租户隔离与价格分组；Key换发/恢复、平台/租户动态限额及分布式限流另立规格 |
| NA-08 | 计量/定价、缓存读写折扣、阶梯与分组价格 | 已有CNY输入/输出计价、硬预占和逐attempt成本版本；018补采购价治理与选路报价复核；[020](../verification/public-catalog.md)统一目录/既有模型页/Reserve的单DB时刻确定选价；023-A补单租户绝对组价/缺价拒绝与请求快照；未提供缓存折扣、阶梯、汇率或复杂表达式 | A增加新协议计量维度；B增加可验证的缓存/阶梯/分组定价，不重复计Token |
| NA-09 | 开放注册、邮箱验证/密码找回、自助开户 | 已有邀请账号、密码、会话；[021](../verification/self-service-identity.md)已实现且限定本地验收：注册默认关、最小权限开户、邮箱恢复/当前MFA、会话撤销与加密outbox | B：按021冻结合同完成注册开关、邮件验证、找回密码、风控与最小权限开户；真实收件另验收 |
| NA-10 | 在线充值、支付订单、兑换码 | 已有人工核款/双人审批、钱包与退款；没有支付回调或兑换码 | B：在线支付、订单/回执/对账、幂等兑换；具体支付商逐一接入 |
| NA-11 | 套餐、订阅、额度周期与钱包混合扣费 | 未实现；原先明确后置 | B：商品版本、权益周期、消耗顺序/预占、过期/取消/退款及历史价格 |
| NA-12 | 客户控制台、价格页、Playground、用量与经营报表 | 已有六协议Playground、接入示例、请求ID排障、客户/运营控制台与资金/成本报表；[020](../verification/public-catalog.md)公开价格页/匿名安全目录及管理员公开CAS已完成限定本地验收，完整统计筛选未实现 | A4核心联调及B公开价格已交付；继续公开商品与运营筛选/导出，目录不授予调用权限 |
| NA-13 | 图片/音频输入与生成、异步图片/视频任务 | 未实现；原先明确后置 | C：按输入/输出维度、任务幂等/轮询/回调和终态资金规则实施 |
| NA-14 | Realtime、Responses WebSocket等长连接 | 未实现；本次单独补入 | C：会话/轮次/事件权限、持续预占/结算、断线和恢复契约 |
| NA-15 | OAuth/OIDC、Passkey、会话与账号恢复 | 已有密码、会话、可选TOTP与恢复码；没有第三方登录/Passkey/自助身份恢复 | C：按身份源与账号绑定/解绑规格扩展，保持MFA默认可选 |
| NA-16 | 告警、品牌/公告、多语言、运营工具 | 024-A已实现四种站内经营告警、独立双槽评估/默认off、共同确认与历史；站点运营/外发通知仍待后续 | B先补渠道/余额/异常账务告警；C完善品牌、多语言与运营管理 |
| NA-17 | 签到赠额、邀请奖励/分佣等增长机制 | 未实现；原先未明确规划 | 可选产品包：纳入候选清单，独立定义赠额与现金余额、反滥用与分佣结算后实施 |
| NA-18 | 扩展/插件生态、迁移工具、多实例 | 已有Compose/1Panel示例与单活动实例；未实现通用插件、New API数据迁移或多活动实例 | C：先迁移映射/校验及可恢复HA设计；动态插件作为独立扩展包评估 |

上游有路由不代表功能完整：该固定版本的`/files`、`fine-tunes`、`images/variations`明确走`RelayNotImplemented`，Claude `count_tokens`被注释，Gemini `countTokens`被显式拒绝。因此这些不计为已核实的New API完成能力，也不自动变成本项目必须提供的兼容承诺。媒体/任务/插件行为仍须逐适配器核实，不能只按路由名验收。

## 2. 执行顺序与SDD切片

| 阶段 | 交付包与顺序 | 可观察出口 |
|---|---|---|
| 基础收口（持续） | 001商业闭环保留正式渠道/部署验收缺口；[014](../../specs/014-mvp-release-qualification/spec.md)补齐隔离浏览器闭环/账本、首次接入修正、可重复QA与真实main正常排空；005–007实现保留，002/008性能专项后移 | 014限定本地范围已通过；目标资源性能、真实供应商与生产部署未验事项明确 |
| A 核心接入（当前） | A1 Responses + Messages已有；A2为009 Embeddings/Rerank及010 Gemini文本；A3有011模型发现/差异、012不可变凭据池与013批量启停，继续供应商profile和其余渠道运营；A4 Playground/调用排障已有，015补Key来源策略、016补运行限额、017补基本配置编辑并通过限定本地验收 | 每种协议按公开范围验证流、工具、计量、错误/取消和账单；渠道与Key操作有明确冲突/原子性/历史，不将接口存在当完整客户端认证 |
| B 自助经营 | B1公开价格020已完成限定本地验收，注册/邮箱恢复021已完成限定本地验收；B2在线支付/兑换码022待审；B3分组/缓存阶梯定价/周期预算；B4订阅套餐与报表/告警 | 继续完成客户自助开户→支付/兑换→获得权益→调用→账单/退款闭环；公开目录本身不授予权益，重复支付回调不重复入账 |
| C 扩展平台 | C1多模态/异步任务；C2长连接；C3第三方身份/Passkey、站点运营；C4迁移与多活动实例 | 各子项有独立故障/资源/资金合同，不以多副本或单个成功请求代替完整验收 |

A各协议可按清晰模块边界并行，B与A在账号/订单/计量接口明确后可并行细化规格。先补齐A/B/C业务能力，再恢复性能专项；保留007已验证结果，新协议的性能与完整商业路径需另行实测。这里给依赖与优先级，不填未经评估的交付日期。

每包按同一SDD流程交付：用户场景/兼容范围→spec→HLD/LLD及状态机→API/SQL/profile fixture→实现与控制台→验收报告→更新本表。A1已由003交付，009细化NA-03，010细化Gemini文本，011细化模型发现，012细化不可变凭据池，013细化跨渠道批量启停；后续继续其余渠道运营与Key限制等A项，再推进自助经营，逐包更新实测状态。池内完整编辑不等同跨渠道管理，批量启停不等同批量导入/发布/付费探测，单成员probe/目录GET不等同全池认证或供应商账号总额度管理。

015交付NA-07的来源访问策略：最多32项IPv4/IPv6/CIDR，空列表不限来源；可信来源捕获、数据库当前策略复查、配置版本与审计共同生效。016独立规格补动态RPM/burst/concurrency，保留旧桶和在途计数，默认60/20/16；017增加名称、模型/地域、累计额度和期限编辑。三者各自CAS和审计，消费或另一种配置变化不会让自己的版本失效。冲突或未知提交先只读核查，不自动重试；没有持久命令回执，仅凭当前值相同不能证明某次写入成功。分组商品、周期预算仍按后续规格推进，未覆盖New API全部能力。

016的运行态为单活动实例最多4096个Key桶，运行中无TTL驱逐，重启满桶，不承诺分布式或持久速率历史。限额在下次新调用准入观察到时切换，降低并发只阻止新许可，已获许可的排队及在途调用不撤回；平台/租户固定速率、全局/租户/渠道与成员容量继续共同生效。来源策略的`sending`发送授权边界保持独立，最终执行状态见[016验收](../verification/key-runtime-limits.md)。

2026-11-01新增[并行交付计划](delivery-plan.md)：018的catalog/routing、billing/gateway、控制台与统一架构检查分工并行；018已通过限定本地验收（[证据](../verification/cost-aware-routing.md)）；019采购账户和累计共享预算已实现并通过[限定本地验收](../verification/procurement-accounts.md)，余额仅人工观察。018基于登记单价选路，不承诺上游真实余额、累计预算或实际最低账单。

## 3. 每类能力怎样算支持

1. **协议与客户端**：固定SDK/客户端版本、端点、认证header、字段和限制。分别验证非流/分片SSE、工具增量、长上下文/缓存计量、取消、错误及用量缺失。Responses/Claude接口存在不等于Codex/Claude Code全功能兼容；压缩、状态引用、工具与WebSocket能力按矩阵另列。
2. **供应商**：同名OpenAI-compatible也逐家验证鉴权、模型映射、usage/缓存与reasoning语义、输出上界、429/重试与计费。未认证的供应商/模型不能因能返回一次200就标为收费就绪。
3. **价格/套餐**：先版本化定义量纲、上界、预占/权益来源、消费顺序、时区/周期和到期边界，再扩展账本；禁用进程内异步余额替代硬预占。退款不能凭空恢复已经过期的权益或混用现金/赠额。
4. **支付/兑换**：固定订单金额与收款主体，回调验签、持久事件去重、查单/对账与幂等入账；兑换并发只成功一次。未选支付商先用本地沙箱，不需要提前提供真实收款密钥。
5. **媒体/长连接/异步任务**：任务所有权、幂等提交、供应商回调来源、取消能否停费、未知消费、结果保存期限与真实资源退出都有合同；不能简单套用当前短文本单请求收费。
6. **高可用与性能**：遵守[002性能规格](../../specs/002-performance-parity/spec.md)。多实例前设计共享准入/健康/失效与资金恢复；不直接取消单活动实例锁。完整商业链路达标才可宣称产品性能接近Rust LiteLLM/Bifrost。

对齐以客户可用的业务能力和协议行为为主，不承诺照搬New API后台接口、数据库或内部quota字段。若需从New API迁移账号、余额、Key或渠道，独立设计导入映射、审计与校验，不能假设旧密码/Key摘要可无损互换。保留本项目多租户、固定价格快照、持久资金与安全重试的设计。

## 4. 当前交付边界

001商业闭环之上，003已有Responses与Messages、协议probe、Playground、接入指南及按request_id查询；009补充Embeddings/Jina Rerank；010增加Gemini文本JSON/SSE、thinking计费及控制台闭环；011增加三目录发现/绑定差异/历史；012增加不可变凭据池、两阶段调度、两层容量与实际身份；013增加跨渠道原子启停/预检及持久回执，合同见[HLD §20](HLD.md#20-013-跨渠道批量启停与回执)、[LLD §20](LLD.md#20-013-跨渠道批量启停与回执)和[验收](../verification/channel-operations.md)。[014](../verification/mvp-release-qualification.md)完成隔离浏览器核款/调用/退款及011–013代表操作、账本核对，并修复默认地域/可信反代限流，补真实main正常SIGTERM排空与重启不重放。另有不可变选路解释、真实SIGKILL和结算后交付失败验证。自动同步、标签/余额、其余批量经营、注册/支付/订阅、媒体/长连接和HA仍未实现，按上表顺序继续。功能交付进度和生产就绪状态不得从路线图反向推导。

015增加Key来源配置及CAS编辑，完整范围见[规格](../../specs/015-key-access-policy/spec.md)与[独立验收](../verification/key-access-policy.md)。`PrepareDispatch`提交`sending`是发送授权同步点；之后收紧策略不撤回已获准请求，在途SSE与资金结算/退款/恢复按原规则完成。010迁移追加来源、独立配置版本及不可变请求来源，消费计数不会令配置版本变化；原生产与性能门禁继续单列。

016增加运行限额三元组及独立`limit_revision`，011迁移追加约束/版本防伪。准入在Key锁下取最新配置后原子检查令牌及并发，容量拒绝不扣Key令牌、不预占；安全回退使用原许可、不重复计数。配置不会补满旧桶，运行许可覆盖后续排队与整个调用收尾；结算、释放、退款和恢复不因限额收紧而阻断。规格与最新专项/最终集成状态分别见[016](../../specs/016-key-runtime-limits/spec.md)及[报告](../verification/key-runtime-limits.md)，不借用此前功能验收或007性能数字证明本轮就绪。

2026-10-31，017追加012迁移及独立`configuration_revision`，017时schema为001–012。基本配置五元组实际变化才递增，旧字段未改原样保留；额度下调保护最新spent+held，模型/地域仅收窄外层授权，期限按UTC微秒处理，过期Key可明确续期/移除期限而撤销不可恢复。预占、attempt、派发按最新限制复查，`sending`后不撤回发送授权或阻断资金收尾；不重建016桶。后端、控制台及专项/全量真实PG race、107项前端测试/strict构建、vet/build、隔离浏览器与012迁移后的58123预览核对均通过限定本地验收，见[017规格](../../specs/017-key-configuration/spec.md)及[记录](../verification/key-configuration.md)。周期预算、分组和自助商业仍未实现，正式供应商、容器/灾备、许可及性能门禁保持独立。

2026-11-01，018实现采购报价版本/渠道CAS、成本优先/未知拒绝/逐binding两维单价上限与候选→attempt→sending报价同步；018时schema为001–013，限定本地验收通过。真实PG全Go race、前端122项/strict构建、vet/build、隔离浏览器CAS/两次调用及资金核对、运行器清理和58123升级见[018记录](../verification/cost-aware-routing.md)。[019](../verification/procurement-accounts.md)已实现共享采购账户、持久逐attempt预占/独立unknown/双审及人工观察；019时schema为001–014。供应商自动余额同步仍未实现。

2026-11-01，[020公开目录](../verification/public-catalog.md)已实现并通过限定本地验收，020时schema为001–015：旧/新模型默认私有、六协议白名单、当前客户售价、独立公开CAS、50+1/C游标与Guard前匿名资源门禁。156项前端/strict构建、最终catalog/server真实PG专项race、隔离真实main浏览器和015预览升级通过；较早全Go race不替代最后SQL安全修订后的相关专项。证据见[运行器](../verification/public-catalog-020-harness.json)、[浏览器](../verification/public-catalog-020-browser.json)和[预览](../verification/public-catalog-020-preview.json)。目录不是报价锁，也不授予调用或新增供应商认证。

[021注册/邮件恢复](../verification/self-service-identity.md)已实现并完成限定本地验收；186项前端/strict、真实PG全Go race、独立SMTP worker/本地收件/浏览器及016预览升级通过。默认关闭注册与邮件；正式收件仍待外部认证。022-A共同入账/双审兑换券已完成限定本地验收（全Go真实PG race、209项前端/strict、真实main/浏览器与017默认off预览），[证据](../verification/funding-vouchers.md)；当时支付沙箱尚未实现；当前M/Q已实现并通过真实进程短程，长程验收见下文。022原路退款先依赖资金来源lot和预占分配，防止赠额变现金。正式支付、真实收件、订阅、媒体/长连接、HA和原生产/性能门禁继续按独立SDD推进。


023-A通过真实PG角色/并发/故障、227项前端/strict、隔离真实main浏览器/账本核对及018预览升级，见[证据](../verification/tenant-pricing.md)。022-B0-P及M/Q独立沙箱已编码并通过专项真实PG/race，真实进程短程通过，长程故障按[沙箱记录](../verification/payment-sandbox.md)继续收口；B1资金/身份核心设计与SQL验证已冻结，B2第三来源已追加021并接入billing及三角色权限，专项通过；订单/worker/HTTP/UI/main已由B3接通，两轮真实进程/最终浏览器及全仓真实PG race通过限定本地验收，真实在途SIGTERM与Guard失权补验已通过；C0纯算法已实现，生产来源lot/维护仍候选。023-B/C与024后续、025依持续计划推进，不把价格组当套餐或现金退款资格。

023-B新增020周期预算核心与billing/有效角色启动检查，核心已通过真实APP交叉和全Go race；Reader/Commander、HTTP/控制台已接线，专项/270项前端/真实多tab和隔离main浏览器通过，最终020相关回归与预览升级通过，独立三态只读核验器和CLI专项通过，交叉回归继续，见[验证](../verification/periodic-budgets.md)。

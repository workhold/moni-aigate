# 国内相关网关项目补充研究

状态：**DRAFT，供审阅**  
访问与分析日期：**2026-10-30（Asia/Shanghai）**  
实施路线：参考公开行为与设计，自行实现 moni-aigate；**不以 New API 二次开发为起点**。

## 1. 结论与范围

对商业 API 平台，One API 提供清楚的“用户—令牌—模型/分组—渠道”基本模型；New API 是同一路线中更完整的商业运营参考；Chat API 提供面向客户和运营后台的产品流程参考。三者具有明显历史关联，不应当作三份相互独立的架构验证。

**`Calcium-Ion/new-api` 已实际重定向到 `QuantumNous/new-api`，是同一 GitHub 仓库的旧地址。** 给定 Moni-AI Gitee 地址本次未能取得 README 或源码，动态调度能力标记为未验证；不能把用户描述、名称相似的仓库或搜索缺失当作技术证据。

新增参考项目不扩大首版协议范围。moni-aigate 仍先完成 `GET /v1/models`、非流式/流式 `POST /v1/chat/completions`，以及充值/入账、预留、调用结算、账单查询和异常恢复的商业闭环。自动支付通道是否纳入首版，以 HLD/LLD 的范围定义为准。

研究采用 GitHub Repository/Commit/Tree/Contents API 获取固定版本材料，结合已有 New API 本地固定源码。本次没有完整克隆 One API/Chat API，没有启动这些服务、真实支付、压测或故障注入；下文区分 README 声明、源码观察和设计建议。

## 2. 项目身份与许可基线

| 项目 | canonical 地址与可得版本 | 本次材料 | 许可观察 |
|---|---|---|---|
| One API | [songquanpeng/one-api](https://github.com/songquanpeng/one-api)，`main` / `8df4a2670b98266bd287c698243fff327d9748cf`，提交时间 `2025-02-21T11:30:16Z` | 固定 README、LICENSE、递归目录树、distributor、ability/cache、relay 编排、Adaptor 接口 | [LICENSE](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/LICENSE)为 MIT；[README:474–480](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/README.md#L474-L480)另声明页脚署名/项目链接，去除需授权。不能只看 MIT badge 忽略额外声明 |
| New API 旧地址 | [Calcium-Ion/new-api](https://github.com/Calcium-Ion/new-api) → [QuantumNous/new-api](https://github.com/QuantumNous/new-api)；本次别名核验时两地址 `main` 均为 `a2defd2a8256abe039e5295bd583aeb2d02f111f`，提交时间 `2026-10-29T16:55:16Z` | 两地址 Repository/Commit API、README；机制分析复用并读回本轮早先固定代码基线 `e449cc700eb569cdfc2199cb73e97de46944f43c` | 机制基线 [LICENSE](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/LICENSE)为 AGPLv3，[NOTICE:6–25](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/NOTICE#L6-L25)含显著署名/链接要求；此处不审计新 HEAD 的全部差异 |
| Moni-AI | 用户给定 [gitee.com/moni-ai/moni-ai](https://gitee.com/moni-ai/moni-ai)；**canonical、分支和 SHA 未确认** | 页面、Gitee API、master/main 原始 README 与 Git refs 访问结果；未取得正文源码 | 未验证，不能假定开源或确定许可 |
| Chat API | [ai365vip/chat-api](https://github.com/ai365vip/chat-api)，`main` / `f7ea0d24a79fd86a8e58e7ad4e068d038d86228c`，提交时间 `2025-07-15T03:01:52Z` | 固定 README、LICENSE、递归目录树、topup 控制器/模型、充值倍率配置、文本 relay、billing 工具 | GitHub API 标签为 `Other/NOASSERTION`；实际 [LICENSE](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/LICENSE)为 MIT 格式，包含 CalciumIon 与 JustSong 版权声明；[README:3–7](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/README.md#L3-L7)同时说明上游来源及“仅供个人学习”等使用声明，不能仅据标签归类或推定商业复用权利已清理 |

所有 SHA 是本次读取的快照，不代表当前最新版本。One API API 的 pushed 时间与 main 提交时间是不同字段，本报告采用提交对象时间，不据此给项目活跃度打分。

用户已选择独立实现，因此这些许可信息是来源与复用边界记录，不表示需要 fork、移植或把上游加入运行依赖。借鉴公开能力也不意味着可以复制其界面、品牌、源码或内部 module。

## 3. One API：基本模型与渠道分发机制

### 3.1 README 能力声明

[README:98–118](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/README.md#L98-L118)列出渠道负载分发、流式、令牌有效期/额度/IP/模型限制、兑换码充值、用户/渠道分组及倍率、模型映射、失败重试和管理 API。

其中兑换码充值与充值链接不等于已经验证在线支付回调、资金账本与财务对账。README 还明确提示，模型映射可能导致请求体重建，使尚未支持的字段无法透传（第 111 行）。这对协议兼容的边界定义比简单的“统一 OpenAI API”口号更有参考价值。

### 3.2 实际源码追踪

1. **取得用户分组和请求模型。** `Distribute` 读取用户归属分组，未指定渠道时调用 `CacheGetRandomSatisfiedChannel(group, model, false)`，无候选返回 503。[middleware/distributor.go:20–60](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/middleware/distributor.go#L20-L60)
2. **候选索引。** `Ability` 以 group/model/channel_id 为复合主键，并保存启用状态与优先级；数据库分发按分组、模型、启用状态筛选，默认限制到最大优先级，再随机选择。[model/ability.go:14–50](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/ability.go#L14-L50)
3. **内存路径。** 渠道快照按分组和模型组织并按优先级排序；读锁下在最高优先级候选中随机，忽略首级时可转向后续候选。[model/cache.go:173–215](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/cache.go#L173-L215)、[227–254](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/cache.go#L227-L254)
4. **注入上游上下文。** 选中后设置模型映射、原模型、上游 Key、Base URL 和渠道配置。[middleware/distributor.go:64–75](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/middleware/distributor.go#L64-L75)
5. **适配与失败切换。** Adaptor 明确分开 URL、请求头、请求转换、发送、响应处理与 usage；Relay 首次失败后重新选渠道、重建请求体，避开上一失败渠道；重试判断主要依赖状态码及是否固定渠道。[relay/adaptor/interface.go:11–20](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/relay/adaptor/interface.go#L11-L20)、[controller/relay.go:45–91](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/relay.go#L45-L91)、[105–121](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/relay.go#L105-L121)

这证明了“能力索引 → 优先级/随机分发 → 适配 → 有限重试”的真实机制；它不等同实时成本最优调度，也不证明完整负载感知。已读重试判断没有展示“上游绝未消费”的完整证据判断，moni-aigate 应自行定义重放边界。

### 3.3 借鉴与不采纳

- **借鉴：** 客户 Key 与上游渠道隔离；显式模型可用关系；简单可解释的主备选择；适配器分工；账户额度与 Key 使用预算分开表达。
- **改进：** 模型配置与候选快照使用版本号、原子发布与审计，避免更新时可见不完整配置；金额用固定精度账本，不能将渠道倍率直接替代客户售价和供应商成本。
- **不直接采纳：** 以状态码为主要依据的宽泛自动重试、未经完整兼容验收的请求体重建、默认账户密码的产品体验。首版也不搬运全部供应商适配器。

## 4. Calcium-Ion/new-api：项目归一，不重复计数

实际访问 [旧地址 Repository API](https://api.github.com/repos/Calcium-Ion/new-api) 后，HTTP 客户端最终地址为 `https://api.github.com/repositories/717197250`；响应 `full_name` 为 `QuantumNous/new-api`、`html_url` 为 canonical 地址、`id=717197250`、`fork=false`。[新地址 Repository API](https://api.github.com/repos/QuantumNous/new-api)得到相同 ID，两个地址 Commit API 得到相同本次 HEAD。这个判断来自实际响应，不是仅凭 README 中旧链接推断。

因此，旧地址所指项目已包含在 [New API 源码研究](new-api-analysis.md) 中。该报告固定源码基线是 `e449cc700eb569cdfc2199cb73e97de46944f43c`；本次 API 解析到较新提交，**没有将旧基线行号套到新 SHA 上**，也没有重新审查新 HEAD 的所有变更。

一条代表性机制是请求计费会话：估算输入/输出上限、计算价格，再预扣；注释说明重试保留会话/初始价格快照，但继续追结算与测试后确认最终分组倍率可能变化，不能据此断言最终客户售价跨分组固定，详见[r3校准](calibration-billing.md)。[relay/request_billing.go:22–67](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/request_billing.go#L22-L67) 数据库预扣以 `quota >= amount` / `remain_quota >= amount` 条件更新，Redis 不可用时存在数据库回退路径。[model/quota_reserve.go:143–197](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/quota_reserve.go#L143-L197)

**借鉴：** 将一次客户请求、若干上游尝试与一次结算区分，冻结价格与计量来源。**moni 自行补强：** 持久资金状态、唯一业务键、恢复任务、可核对的异常处理。不能把该源码观察扩大为已证明所有退款跨崩溃幂等；详细边界见已有研究。用户已经排除二次开发路线，本项目不因这项研究转为 fork。

## 5. Moni-AI：动态调度未取得可验证材料

本次对用户给定来源进行了以下读取：

| 尝试 | 实际结果 |
|---|---|
| `https://gitee.com/moni-ai/moni-ai` 页面 | HTTP 404 |
| `https://gitee.com/api/v5/repos/moni-ai/moni-ai` | HTTP 404 |
| `https://gitee.com/moni-ai/moni-ai/raw/master/README.md` | HTTP 404 |
| 同路径 `raw/main/README.md` | HTTP 404 |
| Git smart HTTP `.../moni-ai.git/info/refs?service=git-upload-pack` | HTTP 401 |

这些结果只能说明**当前环境未取得该地址的公开材料**，不足以判断仓库不存在、已删除、私有、已改名或对网络环境有限制。本次没有借用第三方镜像冒认源仓库。

另做了 GitHub 名称检索与 `moni-ai/moni-ai` 候选地址读取，未确认与用户所指 Gitee 项目可追溯对应的 canonical GitHub 地址。名称检索存在大量无关结果，候选 404 或未搜索到不能证明“没有公开 GitHub”。

因此下列内容均记为 **UNVERIFIED**：项目身份、SHA、许可证、README 动态调度声明、调度输入、权重更新机制、健康恢复、熔断、成本/延迟优化目标、多节点一致性、商业账务。

在获得可访问的原始仓库或官方文档前，不将“Moni-AI 动态调度”写成已验证优势，也不把臆测算法放入首版承诺。moni-aigate 可以独立提出调度实验，但应以离线回放、路由原因和效果指标证明，而非归因于未读到的实现。

## 6. Chat API：运营体验丰富，资金一致性要自行设计

### 6.1 已确认的定位

[README:3–7](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/README.md#L3-L7)自行说明基于 One API 与旧地址 New API 二次开发；GitHub `fork=false` 仅说明托管关系，不推翻 README 的代码来源声明。

[README:13–50](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/README.md#L13-L50)声明 C 端与 `/admin` 后台、令牌计费方式、分组/模型限制、充值通知、低余额提醒、渠道 RPM/代理/请求头、充值倍率和优惠、Claude cache/thinking 与空返回重试。只有下述已读源码机制被本次验证；其余仍是功能声明，不保证完整实现或兼容程度。

### 6.2 一条实际充值链路

1. 服务端从用户组、期限类别、金额档位配置计算报价，而非直接相信客户端提供的最终价格；代码采用 `float64`。[controller/topup.go:49–77](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/controller/topup.go#L49-L77)、[common/topup-ratio.go:7–19](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/common/topup-ratio.go#L7-L19)
2. 创建易支付请求，构造业务订单号及通知地址，随后保存 `pending` TopUp，保存 Amount、Money、TopupRatio 等字段。[controller/topup.go:88–125](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/controller/topup.go#L88-L125)
3. 回调调用 `client.Verify`，校验成功且交易成功后按业务订单号查本地订单。[controller/topup.go:128–159](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/controller/topup.go#L128-L159)
4. 对 pending 订单先保存 success，再单独增加用户 quota、增加充值分类额度、发通知和日志，并可更新用户分组。[controller/topup.go:159–194](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/controller/topup.go#L159-L194)
5. 已读 TopUp 模型使用独立 `DB.Save` 与 `DB.Where(...).First`，`TradeNo` 在该模型定义中未声明唯一约束。[model/topup.go:5–52](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/model/topup.go#L5-L52)

这条路径验证了充值报价、订单、服务端验签与运营通知，但**已读路径没有把订单成功与余额入账包进同一数据库事务，也没有展示跨请求订单行锁**。如果两步间失败，或并发回调均读到 pending，就存在需要设计防护的部分提交/重复处理风险；这是静态边界分析，未经故障注入，不声称已复现事故。仅检视模型字段也不能排除部署方额外建立了数据库约束。

文本 relay 还展示了按次/倍率两类预扣选择，随后适配上游，在拿到 usage 后使用 goroutine 执行后计费。[relay/controller/text.go:55–78](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/relay/controller/text.go#L55-L78)、[134–173](https://github.com/ai365vip/chat-api/blob/f7ea0d24a79fd86a8e58e7ad4e068d038d86228c/relay/controller/text.go#L134-L173) 这是产品灵活性的实现证据，同时提醒 moni-aigate 不把资金终态仅留在请求内存和无持久重试的后台工作中。

### 6.3 借鉴与不采纳

- **借鉴：** 客户端与运营端分离、Key/模型/消费明细就近展示、支付通知、低余额提醒、渠道标签与批量管理。
- **自行强化：** 订单金额/币种核对，唯一订单及事件键，入账与订单终态同事务，先持久化事件再确认接收，通知通过 outbox 解耦，固定精度报价与价格快照。
- **首版后置：** 自选按次/Token 套餐、期限型优惠余额、充值自动升组、邀请返利、复杂促销和多模态协议。这些会增加余额来源、过期、退款、授权与计价组合，先保证单币种单钱包的账务事实可追溯。
- **不直接继承：** 浮点资金模型与上述分步入账机制；仅有更多运营功能不代表资金安全或可恢复性更强。

## 7. 对 moni-aigate 的收敛建议

| 设计主题 | 参考收益 | 首版落点 | 后续空间 |
|---|---|---|---|
| 账户、Key、模型与渠道 | One API 的基本模型清楚，New API/Chat API 丰富运营流程 | 客户隔离、授权模型、Key 预算、独立上游凭据 | 多层套餐、专属资源池 |
| 路由和故障切换 | 优先级/权重/候选索引容易解释 | 静态规则、健康筛选、有限安全重试、每次尝试记录 | 基于可测指标的动态调度 |
| 协议兼容 | 适配器把协议转换集中 | models + Chat/SSE，明确支持字段与拒绝规则 | Responses/Anthropic/多模态逐项验收 |
| 资金闭环 | 预扣、usage、支付订单与明细是共同业务骨架 | 固定精度、价格快照、持久预留/结算/释放、人工核对 | 自动支付/退款通道与对账按已确认范围推进 |
| 产品与运维 | 客户/管理端分离、告警、应用部署降低使用成本 | 自有 UI、消费明细、路由日志、Compose与1Panel编排说明 | 促销、返佣、语义路由与复杂调度 |

**新增研究带来的主要修正是来源身份和证据边界，未形成“竞品有就必须加入”的功能清单。** SDD 应把采纳项转成协议合同、资金不变量、故障场景和验收条件；不能用 README 功能数量替代第一版质量目标。

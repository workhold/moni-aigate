# 四阶段产品路线（含 0 阶段接入）

状态：2026-11-12 用户确认的现行路线。本文是阶段顺序、阶段范围和完成定义的唯一事实源；HLD、LLD、[New API 路线图](new-api-roadmap.md)、[交付计划](delivery-plan.md)、[上游接入路线](upstream-integration-plan.md)和 README 只链接本文，不各写一份阶段表。本文只定义顺序和边界，不表示列出的能力已经实现。

## 1. 总体顺序

| 阶段 | 目标 | 参考来源 | 完成定义 |
| --- | --- | --- | --- |
| 0 阶段（当前） | 统一部署与外部上游接入：aigate 作为唯一公网入口，把 New API、Sub2API 和直接供应商登记为 channel | [New API 与 Sub2API 上游接入路线](upstream-integration-plan.md)；New API 固定提交 `e449cc7` 仅作为能力基线 | Compose/部署脚本拓扑（1Panel 等面板可选）、独立数据库、channel probe、Chat/SSE/usage/错误/成本合同和唯一结算链路通过；外部 relay 可回退 |
| 第一阶段 | 固定版本 New API 核心业务能力闭环，加模型别名、分组倍率、基础文本路由与共享钱包结算；允许通过外部 channel 或已许可源码模块交付 | New API 固定提交 `e449cc700eb569cdfc2199cb73e97de46944f43c` 的公开产品能力 | [029 验收清单](../../specs/029-phase1-acceptance/spec.md)逐项有专用 PostgreSQL、deterministic mock 和至少一个外部 channel 证据 |
| 第二阶段 | 文本 Go 数据面：低延迟、失败分类与故障切换、预算治理、缓存设计、真实压测；先在 aigate 统一合同，再逐项内收下游实现 | Bifrost 开源仓库的机制（固定提交见[海外研究](../research/ecosystem-overseas.md)）与已许可上游源码 | [032](../../specs/032-phase2-text-data-plane/spec.md) 能力有合同、实现和同条件压测记录 |
| 第三阶段 | aigate 文本动态路由：在 direct/new_api/sub2api channel 之间按模型、供应商、价格与性能偏好选择；下游账号池只做局部选择 | OpenRouter 公开文档与产品行为；New API/Sub2API 的公开路由行为 | [033](../../specs/033-text-dynamic-routing/spec.md) 路由决策可记录、可回放，三类切换逐类验收 |
| 第四阶段 | 真实供应商逐一认证、真实 usage 与成本验证、生产部署、性能优化和未迁移能力的下游退役 | 各供应商官方合同和账单；经审计的开源组件源码 | 每个可售 profile 有真实账号证据；生产环境门禁通过；可退役的能力完成切流 |

阶段按顺序推进。后一阶段可以提前做设计和纯算法，但不能把它算作前一阶段的完成条件，也不能反过来阻塞前一阶段收口。

OpenRouter 是商业托管产品，不是开源仓库。本项目只参考它公开文档描述的产品语义，不把它写成代码来源，也不声称取得了它的服务端实现。New API 与 Sub2API 可以作为运行时外部 channel，也可以在许可证和来源记录完整的前提下逐项纳入源码；两种方式都必须经过同一能力、计量和账务验收。

## 2. 不变量

四个阶段共同遵守以下约束，任何阶段都不能为了新能力放宽：

- 统一商业链路 `Profile → Plan → Reserve → Attempt → Observe → Settle`。
- 继续复用 request、attempt、observation、finalization、reservation、settlement、wallet、billing、procurement cost、audit 和 recovery 事实。
- 不新建第二套钱包、计量、结算或采购成本路径。动态路由、缓存和预算治理都只能写入现有预占与账本入口。
- 租户隔离、不可变发布版本、硬预占、唯一结算。
- 可能已经发送到上游的请求不因路由、缓存或故障切换而重放。`provider.SafeToRetry` 与 `routing.Plan.CanRetry` 的判定不放宽。
- 先做硬约束过滤，再做优先级、权重、成本、质量和延迟排序。
- mock 结果只证明本地流程，不写成真实供应商认证。
- New API、Sub2API 与直接供应商共享同一 channel、attempt、usage、成本和结算合同；下游不能成为第二个客户钱包或最终路由权威。
- 源码复用不再是路线阻塞项，但每个复用组件必须保留 LICENSE/NOTICE、来源提交、修改记录和适用的源码提供方式。

### 2.1 文本与媒体的当前结构

钱包、journal 和 funding lot 由文本与媒体共享，`internal/billing/media.go` 通过与文本相同的记账函数和 lot 事件写入。请求、预占、attempt、观察和结算表目前按能力分为两族：

| 事实 | 文本 | 媒体、任务、Realtime |
| --- | --- | --- |
| 请求 | `request_records` | `media_requests` |
| 预占 | `reservations` | `media_reservations` |
| 尝试 | `upstream_attempts` | `media_attempts` |
| 观察与终结 | `finalization_records`、`metering_events` | `media_observations` |
| 结算 | `settlements` | `media_settlements` |
| 能力专属 | — | `media_tasks`、`realtime_sessions` 等 |

媒体的 request、reservation、attempt 和 profile 表都没有 channel 或 binding 列。媒体通过 `media.Registry` 按 `ProfileSelector`（版本加 variant）解析到单个 adapter，不经过 `routing.Plan`，也不写 `route_decisions`。因此当前不能宣称所有能力共用同一组物理业务表，只能说它们共享钱包与账务。

**决策**：第一至第三阶段的渠道路由与动态路由只覆盖文本能力。媒体、异步任务和 Realtime 保持单 profile、单 adapter、共享钱包与账务，不参与动态模型或供应商路由。将来确实需要媒体动态路由时，另立 SDD 统一执行核心与迁移，至少补齐 channel/binding、多候选、provider identity、route decision、供应商成本快照、失败切换以及对应 guard。该工作不是合并表名，而是 028-U 范围的执行核心重构，不放进前三阶段的完成条件。

### 2.2 执行接口分层

不把文本、媒体、任务和 Realtime 合成一个执行接口。共享的是候选、路由决策和商业边界，执行仍保留协议差异：

```text
RouteSelector（第三阶段，文本）
    ↓
统一候选与路由决策模型
    ↓
协议专属执行接口
    ├─ 文本：provider.Client（现有）
    ├─ 同步媒体：SynchronousMediaAdapter（现有）
    ├─ 异步任务：TaskAdapter（现有）
    └─ Realtime：ProviderRealtimeAdapter（现有）
```

`RouteSelector` 只提出候选与排序，商业准入（授权、预占、采购预算、报价同步）保留最终否决权。

## 3. 0 阶段：统一部署与外部上游接入

0 阶段先把 New API 与 Sub2API 作为内部上游渠道接入，不要求一次合并其全部业务能力。aigate 负责客户身份、公开目录、售价、预占、最终路由、结算和审计；下游负责自己的账号池、供应商适配和局部重试。统一部署与数据边界见[上游接入路线](upstream-integration-plan.md)。

0 阶段完成条件：

- Compose/部署脚本能编排 aigate、New API、Sub2API 和各自数据库（1Panel 等面板可选导入同一编排）；公网只暴露 aigate；
- `channels` 能区分 `direct`、`new_api`、`sub2api`，每个 `channel_version` 固定 endpoint、凭据、能力、地域、usage 和成本证据；
- route selector 选择 channel，且下游账号池路由不会新建客户 reservation/settlement；
- downstream 的局部重试结果能表达可靠 usage、未知消费和成本未知，aigate 不因下游 5xx 自动重放；
- 至少一个外部 channel 与一个内置 adapter 共享同一计量/结算 fixture，并能用 route revision 回退。

0 阶段不把外部模型列表、单次 HTTP 200 或代理可达性写成可售认证，也不要求媒体、任务和 Realtime 进入多候选动态路由。

## 4. 第一阶段：固定版本 New API 核心业务能力闭环

名称是“固定版本 New API 核心业务能力闭环”，不是“New API 全部能力”。对齐对象固定为 New API `e449cc7` 的公开产品能力，不追赶 New API 后续 main。能力可以先经外部 New API/Sub2API channel 提供，再按[上游接入路线](upstream-integration-plan.md)逐项由 aigate 内置 adapter 或已许可源码模块替代；不复制客户钱包、下游数据库或内部 quota 事实。上游存在路由但实现为 `RelayNotImplemented` 的入口不计入对齐范围（见[路线图 §1](new-api-roadmap.md#1-固定对照与能力差距)）。

### 4.1 能力分类

| 能力 | 分类 | 说明 |
| --- | --- | --- |
| 用户、租户、角色、成员 | 必须完成 | 现有 admin、finance_operator、finance_approver、owner、member、viewer |
| API Key（IP/CIDR、RPM、burst、并发、模型、地域、额度、期限、撤销） | 必须完成 | 015–017 |
| 模型目录 | 必须完成 | 平台、客户与公开三个读面 |
| 模型别名 | 必须完成 | 见 §4.3。理由是 New API 对齐，不是动态路由前置 |
| 渠道、凭据池、模型映射 | 必须完成 | 011–013 |
| 文本渠道优先级、权重、健康、容量、基础故障切换、成本优先 | 必须完成 | LLD §8、018；只覆盖文本 |
| 采购账户与预算 | 必须完成 | 019、023-B |
| usage、成本、账单、经营报表与 CSV | 必须完成 | 026、027 |
| 用户分组倍率 | 必须完成 | 见 §4.4，复用价格组，编译为绝对价格 |
| 人工核款、消费退款、双审兑换券 | 必须完成 | 现有 |
| 虚拟币钱包：对外只暴露币，钱包、售价、Key 额度以币计；手动充值（含赠送）冻结法币基准；手动现金退款 | 设计与决策属第一阶段；手动充值与手动退款的实现在 MVP 前完成 | 方向已确认，设计见 [034](../../specs/034-virtual-coin/spec.md)，2026-11-14 已冻结，实现按 034 §17 分片进行；开发环境可用不等于生产储值能力，正式支付、真实收款、生产储值业务另设验收；支付宝等支付接入、汇率版本表、币到期、重估后置 |
| 支付订单 | Mock 验收 | 仅本地支付沙箱；正式支付商后置 |
| 客户控制台、管理控制台 | 必须完成 | 页面级对照见验收清单 |
| Playground | Mock 验收 | 使用 deterministic mock 上游 |
| Chat、Responses、Messages、Gemini 文本、Embeddings、Rerank | Mock 验收 | 协议、计量和错误合同由 deterministic fixture 验证；供应商 usage 语义第四阶段认证 |
| 图片生成、TTS、异步视频任务、单 turn Realtime | Mock 验收 | 按[媒体供应商安全](media-provider-safety.md)显式开启 mock，`provider_mode=mock`，不参与路由 |
| 站内告警、审计、结构化日志 | 必须完成 | 024-A |
| Compose 单机部署、备份、恢复演练 | 必须完成 | 生产部署属第四阶段；1Panel 等面板仅为可选部署环境，不作验收门禁 |
| 订阅、套餐 | 明确后置 | 见 §4.5 |
| 正式支付商 | 明确后置 | 第四阶段按选定支付商接入 |
| STT、图片编辑 | 明确后置 | 有规格，无代码 |
| OAuth、OIDC、Passkey | 明确后置 | — |
| 原路现金退款、拒付 | 明确后置 | — |
| 多实例 HA | 明确后置 | — |
| New API/Sub2API 下游代码、数据库、内部 quota 兼容 | 外部 channel 可保留；需要统一事实时按迁移切片内收 | 不共享数据库，不把下游 quota 当 aigate 客户余额 |
| New API/Sub2API 数据迁移 | 后置、逐项立项 | 只有能力切换需要时执行，保留原系统回退 |
| 任意插件执行环境、MCP 执行、脚本 | 不属于目标 | 见 §5.2 |
| 供应商专有协议全覆盖 | 不属于目标 | 客户侧保留最小稳定的 OpenAI-compatible 入口；已实现的其他协议保留，不再新增 |
| 签到、邀请奖励、分佣 | 不属于目标 | 增长功能，不是网关核心能力 |

### 4.2 验收口径

- 每个“必须完成”项在专用 PostgreSQL 测试库上有通过记录，并给出证据路径。跳过数据库测试不算完成。
- 每个“Mock 验收”项使用仓库内 deterministic mock，结果必须标注 mock，不得写成真实供应商认证。
- 部署 mock 上游需要覆盖六种文本协议和同一逻辑模型下的多个候选，才能验证路由、计量、钱包、结算和错误恢复。当前 `scripts/deployment-qa-provider.py` 只处理 `POST /v1/chat/completions`，这是第一阶段尚缺的验收前置；设计见 [031](../../specs/031-deterministic-mock-upstream/spec.md)。
- 验收清单按 New API 能力逐项给出“对齐内容、验收方式、证据路径、状态”，见 [029](../../specs/029-phase1-acceptance/spec.md)。清单草案已写出，逐项证据尚未产生。

### 4.3 模型别名

客户可见名解析到逻辑模型。别名只能解析为已发布目录项，不解析客户端提交的供应商或部署后缀，也不能放宽租户、成员或 Key 的授权（HLD §4.2、LLD §3）。别名与逻辑/物理模型分离是两件事：别名是“客户名 → 逻辑模型”，物理模型是“逻辑模型 → 上游模型绑定”。后者属于第三阶段（§6.2）。数据合同与接口见 [030](../../specs/030-model-aliases-group-multipliers/spec.md)，待开发者评审冻结。

### 4.4 用户分组倍率

New API 的分组倍率在 moni 中只作为录入便利。管理员可以按基准价和倍率录入，系统在发布时把结果编译成不可变的绝对价格版本，写入现有价格组（023-A）。运行时仍使用请求冻结的绝对价格快照，不在请求路径上乘以可变倍率。这样保留 HLD §6.3 的“固定客户售价与严格预占”合同，也不另建计价体系。

### 4.5 订阅与套餐

后置。若将来纳入，必须作为 027 来源与 funding lot 体系里的一种来源，按现有 FIFO、预占分配和逆序退款处理；不得新建权益余额或第二套钱包。现有 `internal/productvalue` 只有纯算法，没有接线，不构成纳入条件。

## 5. 第二阶段：Bifrost 风格文本数据面

Bifrost 是 Apache-2.0 开源仓库，可以参考其源码机制。它的 Enterprise 文档（例如熔断）不能当作开源实现。

### 5.1 能力范围

| 能力 | 边界 |
| --- | --- |
| 文本 Go 数据面与低延迟 | 完整商业检查不能关闭；需要同机、同协议、同用量条件的真实压测（[002](../../specs/002-performance-parity/spec.md)方法） |
| 失败分类与故障切换 | Bifrost 的可重试集合是可用性策略，不是未消费证明；不放宽 `SafeToRetry` |
| 预算治理 | 只能经由现有预占、采购预算和账本，不新建余额 |
| 缓存 | 先定义命中计费语义和租户作用域，再实现；缓存命中不得修改钱包或结算规则；不跨租户复用 |
| 熔断 | 自行设计，参考公开机制，不依赖 Enterprise 说明 |
| 压测基线 | 网关额外首字节与首有效内容分开记录；未同条件实跑前不宣称接近 Bifrost |

### 5.2 不可插件化与不在范围

以下能力只能由核心模块实现，不能进入普通插件：路由选择、计量、预占、结算、钱包与 journal 写入、采购成本记录、恢复。插件、动态脚本、MCP 执行和 Agent 工作流不属于前三阶段，需要时另立 SDD。

## 6. 第三阶段：OpenRouter 风格文本动态路由

### 6.1 三类切换

| 切换 | 当前状态 | 本阶段要求 |
| --- | --- | --- |
| 同一逻辑模型下的供应商切换 | 已有：优先级、权重、健康、容量、成本优先（018） | 补评分输入和回放 |
| 同一质量档位下的物理模型切换 | 不支持：物理模型只是 `model_bindings.upstream_model` 字符串；028 将路由版本绑定到单一 profile；路由决策校验要求一次决策中每个 channel 只出现一次 | 新增物理模型实体与能力字段，放宽决策模型 |
| 跨质量档位的 auto 路由 | 只有文字约束（LLD §9.2） | 显式 auto 商品、公布计价、质量门槛 |

### 6.2 设计合同

合同草案见 [033](../../specs/033-text-dynamic-routing/spec.md)，待开发者评审冻结。要点包括：

- **物理模型与质量档位**：物理模型实体（上游模型、能力、上下文长度、工具能力、usage 语义），质量档位实体，以及它们与 028 单 profile 约束的关系。
- **路由策略版本扩展**：评分权重、质量门槛、最低毛利、粘性策略写入不可变版本。
- **路由决策记录**：policy version、候选全集、过滤原因、评分输入、随机抽签、最终选择、供应商成本快照、会话粘性原因、降级或切换标志，以及最终 attempt 与结算的关联。现有 `upstream_attempts.route_decisions` 只有 channel、version、priority、weight、reason 五个字段，且原因是封闭白名单，需要改合同而不是追加字段。
- **路由回放**：给定同一快照与抽签输入，重放结果一致。
- **会话粘性**：路由会话按 `(tenant_id, session_id)` 隔离，与登录会话 `session_families` 分开。
- **跨模型切换与预占**：auto 商品的客户预占界等于其逻辑模型 profile 的界（`Reserve` 要求预占界等于该 profile 的界），与最终选中哪个物理模型无关；候选模型能力不足时在硬过滤阶段排除；路由切换不改变客户预占上界。auto 只有一个售价，按请求时冻结的价格结算。
- **评分输入**：模型能力、质量门槛、上下文长度、工具能力、延迟、成功率、当前供应商成本、缓存与上下文重传成本、会话粘性、最低毛利、租户与 Key 权限。不能只按最低价格选择。
- **最低毛利保护**：成本未知时不当作 0，按策略排除或拒绝。

### 6.3 验收

- 同一快照回放结果一致。
- 硬约束失败的候选不因评分高而复活。
- 可能已发送的请求不因动态路由重放。
- 跨模型切换后客户售价快照与采购成本快照一致，唯一结算保持。
- deterministic mock fixture 覆盖三类切换。

## 7. 第四阶段：真实供应商与生产

- 真实供应商逐一认证：OpenAI、Claude、Gemini、DeepSeek、GLM、Azure、Bedrock、Vertex 及第三方聚合平台，按[渠道认证计划](provider-qualification-plan.md)逐 profile 冻结。
- 真实 usage 与成本对账。
- 正式支付商、灾备与生产部署；1Panel 等面板适配为可选工作，不阻塞阶段收口。
- 生产环境性能优化与竞品对照。

在此之前进行的真实请求只算内测资格探测，不计入任何阶段完成条件。例如 2026-11-10 对第三方聚合端点的单次 Chat 探测，它不代表官方供应商认证。

## 8. 当前不应继续扩大的范围

- 022–025 的历史证明候选（C0、B-P、A 系列），按 [ADR-005](../decisions/ADR-005-pre-mvp-single-path.md) 冻结。
- `internal/procurementprefix`、`procurementattemptbirth`、`procurementaccountwriter`、`procurementprefixschema` 等未接入服务的候选包。
- 新的客户侧供应商专有协议入口。
- 媒体动态路由，以及文本与媒体物理表族合并（见 §2.1）。

## 9. 设计规格与决策点

| 规格 | 内容 | 待裁决 |
|---|---|---|
| [029](../../specs/029-phase1-acceptance/spec.md) | 第一阶段验收清单 | 清单项是否完整 |
| [030](../../specs/030-model-aliases-group-multipliers/spec.md) | 模型别名、分组倍率 | `/v1/models` 是否列出别名 |
| [031](../../specs/031-deterministic-mock-upstream/spec.md) | 多协议多候选 mock 上游 | D-031-1 `provider_mode` 标记；D-031-2 usage 公式 |
| [032](../../specs/032-phase2-text-data-plane/spec.md) | 第二阶段文本数据面 | D-032-1 缓存计费语义；D-032-2 Key 间共享 |
| [033](../../specs/033-text-dynamic-routing/spec.md) | 第三阶段文本动态路由 | D-033-0 profile 处理；D-033-1 auto 定价；D-033-2 请求字段名；D-033-3 服务模型可见性 |
| [034](../../specs/034-virtual-coin/spec.md) | 虚拟币钱包、手动充值与手动现金退款 | D-034-1…13 已于 2026-11-14 全部裁决，规格已冻结（034 §13）；MVP 暂不开放调价 |

规格均为草案。冻结前不改动现有路由、计量或资金代码。

## 10. 设计完整度（2026-11-10 校验）

校验方式：把 033、034 对现状的每条断言对照 `migrations/current_mvp.sql` 与 Go 源码逐条核对，并据此修订。下表区分“设计已落笔”和“仍缺什么”。**没有任何一项已实现，也没有任何一项在真实数据库上验证过。**

| 规格 | 设计状态 | 仍缺的设计 |
|---|---|---|
| [029](../../specs/029-phase1-acceptance/spec.md) 验收清单 | 草案 | 证据格式与汇总脚本；各项“待复验”的复验命令 |
| [030](../../specs/030-model-aliases-group-multipliers/spec.md) 别名与倍率 | 草案 | `/v1/models` 是否列别名的裁决；别名名字空间的 advisory lock 键与触发器正文；倍率编译的前端交互 |
| [031](../../specs/031-deterministic-mock-upstream/spec.md) mock 上游 | 草案 | `provider_mode` 是否进文本链路（D-031-1）；Compose 接线与旧 mock 删除的顺序 |
| [032](../../specs/032-phase2-text-data-plane/spec.md) 数据面 | 草案 | 缓存计费语义（D-032-1）与 028 的 cache-hit finalization 合同；熔断参数进路由版本的 DDL；首事件边界的网关状态机 |
| [033](../../specs/033-text-dynamic-routing/spec.md) 动态路由 | 草案，本轮已按现状修订 | D-033-0…3；028 attempt 快照 guard 修订合同；`physical_models` 迁移回填正文；统计窗口聚合表 DDL；会话标识的来源；评分函数的具体公式与权重含义；回放 CLI 的输入格式 |
| [034](../../specs/034-virtual-coin/spec.md) 币钱包 | 2026-11-14 已冻结；034.1/034.2已进入当前实现，退款规则、单人运营开关和币价版本表已写入；MVP 暂不开放调价 | MVP 买入币价记录、后续调价治理、现金退款 guard 扩展与 HTTP/控制台、支付金额语义 |
| 供应商币种与汇率表 | **未写** | 采购账户、预算、成本版本的币种；汇率版本表；跨渠道比价与毛利折算；033 的最低毛利依赖它 |
| 订阅/套餐 | **未写**，后置 | 作为 027 来源的合同；到期与限定模型范围依赖 034 的新桶机制 |
| 媒体动态路由 | **未写**，不在前三阶段 | channel/binding、多候选、route decision、成本快照 |

本轮校验保留以下边界：034.1/034.2 已进入当前实现并独立验收，034.3 起的退款桶、币价版本和现金退款仍未完成；033 的预占界取逻辑模型 profile，不能把这些后续设计写成已实现或真实数据库验收。

## 11. 下一步顺序

1. 完成 0 阶段的 Compose 拓扑、外部 channel 合同和 New API/Sub2API probe。
2. 修正文档路线与验收矩阵（本文及关联文档）。
3. 建立第一阶段验收清单并补多协议、多候选部署 mock。
4. 在专用 PostgreSQL 上重新执行完整验收。
5. 完成第一阶段剩余能力：模型别名、分组倍率录入编译和首个内收 adapter。
6. 第二阶段文本数据面设计与实现。
7. 第三阶段文本动态路由设计与实现。
8. 第四阶段真实供应商认证、生产验证和已迁移能力的下游退役。

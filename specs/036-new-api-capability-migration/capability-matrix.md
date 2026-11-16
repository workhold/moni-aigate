# 036 能力台账：New API 固定基线对照

本台账是[036 总合同](spec.md)的逐项索引。`当前状态`只描述本仓库现有正式路径与已有证据；除明确写有真实 profile 证据的条目外，均不代表真实供应商、正式支付商或生产认证。`目标/下一片`是规划，不得反向写成已实现。

## 1. 阅读与更新规则

- 上游参考均固定到 New API `e449cc700eb569cdfc2199cb73e97de46944f43c`；表中路径用于行为对照，不表示已复制源码。
- 当前事实快照以本台账分支基点 `main@98258c3ac758127a9c9660b017ddbe115fcde988` 为准；所有汇总行均为 `source_mode=reference_only`，没有 vendor New API/Sub2API 源码。具体 profile/实现切片必须另记自己的证据提交、来源方式和未关闭问题。
- `already_internal`、`embedded_adapter`、`external_relay` 描述实现位置；`mock_verified`、`external_flow_verified`、`supplier_profile_qualified` 描述证据，二者不得混用。
- 每次实现切片更新对应行的源码、UI、规格、证据和未关闭问题；增加供应商/模型时另建 profile 子台账，不能用 NA 行的总体状态替代逐 profile 资格。
- 完整历史证据以[029 验收清单](../029-phase1-acceptance/spec.md)和[MVP 状态](../../docs/verification/mvp-status.md)为入口；旧证据不自动覆盖后续提交。

## 2. 协议、供应商与路由

| ID | 能力与固定上游参考 | 当前 aigate 事实与证据 | 目标/下一片与 UI 边界 |
| --- | --- | --- | --- |
| NA-01 | 模型目录、Chat JSON/SSE、基础函数工具；参考 `relay/channel/openai/*`、`router/relay-router.go` | `already_internal + mock_verified`：目录、Chat 非流/SSE、基础工具和统一计量已进入 029；复用 `internal/provider/client.go`、native request/response、`ModelsView.vue`、`PlaygroundView.vue`。[协议验收](../../docs/verification/implementation.md) | **M1/P0**：选一个 OpenAI-compatible profile 补齐供应商差异、工具增量、usage、错误/取消、成本 fixture 和回退。渠道页显示资格/探测/审计，Playground 显示真实终止和计费状态；真实供应商资格留第四阶段 |
| NA-02 | Responses、Anthropic Messages、Gemini 原生文本；参考 `relay/channel`、`router/relay-router.go` | `already_internal + mock_verified`：003/010 已覆盖受限文本/基础工具范围；Gemini 工具/多模态未覆盖。[原生协议](../../specs/003-native-protocols/spec.md)、[Gemini](../../docs/verification/gemini-native.md) | **M1/P0**：按供应商+协议逐片完成内置适配与 mock 验收，不因 Chat 兼容继承 Responses/Messages/Gemini 资格。UI 继续显示协议能力、限制和 request ID；真实供应商认证留第四阶段 |
| NA-03 | Embeddings、Rerank；参考 `relay/channel`、`router/relay-router.go` | `already_internal + mock_verified`：009 已有 OpenAI Embeddings 与 Jina token 计量 Rerank。[验收](../../docs/verification/retrieval-protocols.md) | **M1/P0**：逐家补内置 profile 的聚合输入硬界、最终 usage、零输出和成本 fixture；真实请求/账单认证留第四阶段。Cohere `search_units` 等额外量纲另立规格 |
| NA-04 | 多供应商、模型映射与能力配置；参考 `relay/channel/*`、`model/channel.go` | 通用 direct adapter、不可变 binding、external New API/Sub2API 已存在；035 有 `external_relay → embedded_adapter` 和 revision 回退。真实 profile 资格未完成。[035](../035-external-upstream-integration/spec.md)、[供应商计划](../../docs/design/provider-qualification-plan.md) | **M1/P0**：扩展现有 provider，不复制整个 relay；每个供应商/模型单独记录证据，真实资格在第四阶段授予。Azure/Bedrock/Vertex 等签名与部署差异独立切片 |
| NA-05 | 多渠道优先级/权重、健康、限流、故障切换；参考 `model/channel_cache.go`、`service/relay_error.go` | `already_internal`：018 已有硬过滤、优先级/权重/健康/容量/成本优先和最多两次安全 attempt（即最多一次跨渠道切换）；完整候选池快照、抽签和回放未实现。[018](../../docs/verification/cost-aware-routing.md) | **M3/P1 → M4/P2**：失败分类/熔断归 032；物理模型、评分、完整失败池、粘性与回放归 033。不得用上游可重试集合放宽 consumption 判定 |

## 3. 渠道、身份、计价与资金

| ID | 能力与固定上游参考 | 当前 aigate 事实与证据 | 目标/下一片与 UI 边界 |
| --- | --- | --- | --- |
| NA-06 | 多密钥池、批量运营、标签、模型发现/同步、渠道余额；参考 `model/channel.go`、`router/channel-router.go` | 011/012/013 已有三格式发现与差异、1..32 不可变凭据池、批量启停和幂等回执；019 有采购账户/预算。自动同步、标签和供应商余额查询未实现。[发现](../../docs/verification/model-discovery.md)、[凭据池](../../docs/verification/credential-pools.md)、[批量运营](../../docs/verification/channel-operations.md) | **M2/P0/P1**：同步只产生差异草稿，管理员 CAS 发布；余额是带来源/时刻的只读观察，不是硬预算。复用 `ChannelDiscovery.vue`、`CredentialPool.vue`、`ChannelOperations.vue` |
| NA-07 | 用户/分组、Key 模型/IP/预算/速率限制；参考 `model/token.go`、`middleware/model-rate-limit.go` | `already_internal`：租户/成员/角色、Key 模型/地域/IP/CIDR/RPM/burst/concurrency/lifetime/期限和 CAS 已验收；分组倍率编译为绝对价格。分布式限流未承诺。[Key 配置](../../docs/verification/key-configuration.md)、[访问策略](../../docs/verification/key-access-policy.md)、[运行限额](../../docs/verification/key-runtime-limits.md) | **保持并补缺口**：不复制 Token 明文或 Redis quota；Key 换发、动态平台/租户限额和分布式限流须另有产品/HA 合同。`KeysView.vue` 保留冲突与未知核查 |
| NA-08 | 计量/定价、缓存读写折扣、阶梯与分组价格；参考 `service/billing.go`、`service/text_quota.go` | 硬预占、唯一结算、完整量纲、价格组/倍率编译、采购成本与周期预算已有；缓存命中计费、阶梯价和供应商汇率未完成。[统一计量](../../docs/verification/unified-metering.md)、[租户定价](../../docs/verification/tenant-pricing.md) | **M3/P1；阶梯/汇率另立规格**：缓存遵守 032 并先裁决 D-032-1/D-032-2；不得引入估算扣费、第二价格事实源或可变运行时倍率 |
| NA-09 | 开放注册、邮箱验证/找回、自助开户；参考 `router/api-router.go`、`controller` | `already_internal`：021 默认关闭注册/邮件，本地 SMTP、最小权限开户、MFA 和会话撤销已验收；正式收件域名未认证。[验收](../../docs/verification/self-service-identity.md) | **保持**；真实 SMTP/收件在部署资格中独验。复用 `IdentityRequestView.vue`、`IdentityCompleteView.vue`、`SecurityView.vue`，不为对齐而默认开放注册 |
| NA-10 | 在线充值、支付订单、兑换码；参考 `controller/topup*.go`、`model/topup.go`、`model/redemption.go` | 人工核款、双审兑换券和本地支付沙箱已有；沙箱不是实际收款，真实原路退款/拒付未完成。[兑换券](../../docs/verification/funding-vouchers.md)、[支付应用](../../docs/verification/payment-application.md) | **M5/P3/第四阶段**：选定支付商后冻结验签、持久事件、查单/对账和退款。复用支付 journal/UI，不先做无真实后端的充值按钮 |
| NA-11 | 套餐、订阅、额度周期与混合扣费；参考 `service/funding_source.go`、`router/api-router.go` | `postponed`：现有 `internal/productvalue` 仅纯算法，未接线；周期预算不是订阅商品。[路线 §4.5](../../docs/design/phase-roadmap.md#45-订阅与套餐) | **M5/P3**：只有新产品合同获批才实施；必须成为 027 来源/funding lot，先冻结周期、FIFO、到期、取消和退款，不建权益钱包 |

## 4. 控制台、报表与运营

| ID | 能力与固定上游参考 | 当前 aigate 事实与证据 | 目标/下一片与 UI 边界 |
| --- | --- | --- | --- |
| NA-12 | 客户控制台、价格页、Playground、用量与经营报表；参考 `web/src`、`router/api-router.go` | 双控制台、公开价格、六协议 Playground、请求详情、平台 CSV 和经营报表已验收；客户自助 CSV、时间序列和完整筛选仍有缺口。[控制台](../../docs/verification/console.md)、[报表](../../docs/verification/operating-reports.md) | **M2/P1**：复用 `ReportsView.vue`、`OperatingReport.vue`、`operating-report.ts`；补客户导出/图表/筛选，保持快照、权限、窗口和哈希证据。媒体 Playground 待真实后端 |
| NA-16 | 告警、品牌/公告、多语言与运营工具；参考 `router/api-router.go`、`web/src` | 024-A 站内告警、共同确认、审计与恢复只读已有；外发通知、公告、多语言、主题和个人通知偏好未实现。[告警](../../docs/verification/operations-alerts.md) | **M2 可补已有告警展示；其余 M5/P3**：复用 `AlertsView.vue` 和 capability 降级；通知失败不得阻塞账本，多语言/主题不复制上游视觉 |

## 5. 媒体、身份扩展与平台边界

| ID | 能力与固定上游参考 | 当前 aigate 事实与证据 | 目标/下一片与 UI 边界 |
| --- | --- | --- | --- |
| NA-13 | 图片/音频输入与生成、异步图片/视频任务；参考 `relay/channel`、`router/relay-router.go` | 图片生成、TTS、异步视频只有显式 deterministic mock 的共享钱包/账务路径；真实 adapter、STT、图片编辑未完成。[媒体安全](../../docs/design/media-provider-safety.md)、[统一计量](../../docs/verification/unified-metering.md) | **M5/P3**：每种媒体独立冻结输入/输出量纲、任务幂等、取消/回调、结果期限和终态资金；继续单 profile/adapter，媒体动态路由另立 SDD |
| NA-14 | Realtime、Responses WebSocket 等长连接；参考 `relay/channel`、`router/relay-router.go` | 单 turn Realtime 为 mock 验收；多 turn、真实 WS 供应商和完整恢复未完成。[029](../029-phase1-acceptance/spec.md) | **M5/P3**：先冻结会话/轮次/事件权限、持续预占、断线和终态结算；不参与文本动态路由，不以一次 WS 连接证明资格 |
| NA-15 | OAuth/OIDC、Passkey、第三方登录与账号绑定；参考 `router/api-router.go`、`controller` | `postponed`：密码、会话、可选 TOTP、恢复码和邮箱恢复已有；第三方身份未实现。[身份验收](../../docs/verification/self-service-identity.md) | **M5/P3**：独立冻结身份源、绑定/解绑、账号冲突、MFA 和全会话撤销；复用 `session.ts`/Security，不绕过现有角色与租户门禁 |
| NA-17 | 签到赠额、邀请奖励/分佣；参考 `router/api-router.go` | `out_of_scope`：不是网关核心能力，现有路线明确排除。[路线 §4.1](../../docs/design/phase-roadmap.md#41-能力分类) | 不迁移上游签到/邀请页面、API 或其赠额/分佣账务；不影响现有人工赠送与双审兑换券。未来如需改变，必须先重新决定产品范围和反滥用/资金合同 |
| NA-18 | 扩展/插件、迁移工具、多实例与部署；参考 `README.md`、`router/api-router.go` | Compose 单机、备份恢复和 current-only 迁移已有；通用插件/MCP、New API 数据迁移和多活动实例不属于当前能力。[当前 MVP](../../docs/verification/current-mvp.md)、[备份恢复](../../docs/verification/backup-restore.md) | 生产部署走 025/第四阶段；多活动实例 HA 走独立扩容合同，不在四阶段内承诺。不导入下游客户/quota/钱包，不开放触碰路由、计量、结算或恢复的插件。下游退役按 profile 证据执行 |

## 6. 首批台账展开顺序

1. PR-2 展开 NA-04/NA-06 的发布资格、来源、重试和成本证据字段，物理设计另审。
2. PR-3 展开 NA-01 的一个 OpenAI-compatible Chat/SSE profile，完成内置适配和 deterministic/mock 验收；不得用 NA-01 总体行或内测探测替代第四阶段真实 profile 资格。
3. 后续依次展开 NA-02/03/04 的其他 profile、NA-06 的运营工作流和 NA-12 的客户观测；032/033 与 M5 保持各自合同和门禁。

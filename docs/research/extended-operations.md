# 扩展研究：Helicone、Langfuse 与 Agent Router

状态：DRAFT / 设计输入；访问日：2026-10-30（Asia/Shanghai）。用户已确认参考设计后独立实现 moni-aigate；本文件不引入第三方服务或业务代码。

## 1. 本轮新增价值与研究边界

这组项目分别补充**自适应选路、观测采样/评估、协议治理与隐私边界**。采用 GitHub Repository/Commit/Tree API 固定默认分支提交，再读取许可证、README 和少量代表性源文件。没有运行这些项目、执行真实模型调用、复测性能或全仓安全审计。

Helicone 分别有观测平台和独立网关，不能把一个仓库的许可证/功能直接套到另一个；本轮按一个项目组、两个仓库记录。`envoyproxy/ai-gateway` 则已迁移为 Agent Router，不能把旧名字当成独立的第四个竞品。

| 项目/仓库 | 固定版本与提交时间（UTC） | 实际许可/身份边界 |
|---|---|---|
| [Helicone/ai-gateway](https://github.com/Helicone/ai-gateway) | `9649b27bdc9fb0907d359e899894102a15f3a085`，main；2025-11-21T01:25:55Z | 根 LICENSE 为 **GPLv3**；README 的 Apache badge/文字与之冲突。本次API未标 archived，但不能因此推定持续活跃或推荐该快照上线 |
| [Helicone/helicone](https://github.com/Helicone/helicone) | `7720d286110391c3e3776bb46a18fb2a13f7567c`，main；2026-10-28T15:59:49Z | 观测平台仓库根 LICENSE 为 Apache-2.0；不是上述独立Rust网关的许可 |
| [langfuse/langfuse](https://github.com/langfuse/langfuse) | `5cb204754430fb77f3f7d830c651e5be7e4ad7f6`，main；2026-10-29T22:16:41Z | 根许可证为分区许可：核心 MIT Expat，ee 等明确目录另受 Enterprise License 约束；GitHub Other 标签不能代替阅读原文 |
| [theagentrouter/agent-router](https://github.com/theagentrouter/agent-router)（原 Envoy AI Gateway） | `931b020400a40c0e0f9a819a1c4b229ef945b8bd`，main；2026-10-28T14:53:23Z | Apache-2.0；旧 `envoyproxy/ai-gateway` URL 实际重定向到此仓库，README明确迁移与原项目关系 |

许可证原文：[Helicone AI Gateway](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/LICENSE)、[Helicone 观测平台](https://github.com/Helicone/helicone/blob/7720d286110391c3e3776bb46a18fb2a13f7567c/LICENSE)、[Langfuse 核心/分区](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/LICENSE)、[Langfuse Enterprise](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/ee/LICENSE)、[Agent Router](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/LICENSE)。独立实现路线下记录这些边界，不因为某段设计值得借鉴就默认复制其实现。

## 2. Helicone：自适应路由要有真实状态，日志脱敏覆盖 URL

### 2.1 两个仓库不可混写

独立 AI Gateway 的 README 称 Rust 网关、模型延迟、provider P2C+PeakEWMA、权重和成本优化等策略；本次只把实际读取的机制列为已验证。README:8 和 :333 仍把许可写作 Apache，但同提交根 LICENSE 是 GPLv3，不能继续写“Helicone网关是Apache”。[README:8](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/README.md#L8)、[README:333](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/README.md#L333)。

### 2.2 已读源码机制

1. **选路算法明确区分目标粒度。** `RoutingStrategyService` 包含 ProviderLatencyPeakEwmaP2C、WeightedProvider、WeightedModel、ModelLatency；provider latency 使用 Tower P2C，model latency 按请求模型选择可用 provider。源码注释还允许某些 provider策略映射到目标供应商提供的模型，这与“同模型渠道切换”是不同语义。[strategy.rs:24-75](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/ai-gateway/src/router/strategy.rs#L24-L75)、[strategy.rs:187-226](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/ai-gateway/src/router/strategy.rs#L187-L226)。
2. **model latency 有可跟踪的候选状态。** latency router 按模型保存 ReadyCache，发现变化时插入/移除 endpoint；在同模型 ready 集合迭代并按 load 值最小选择，而非随机贴一个“智能”标签。[router/mod.rs:103-134](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/crates/latency-router/src/router/mod.rs#L103-L134)、[router/mod.rs:166-188](https://github.com/Helicone/ai-gateway/blob/9649b27bdc9fb0907d359e899894102a15f3a085/crates/latency-router/src/router/mod.rs#L166-L188)。本次未追踪实际PeakEWMA观测完成时点、样本老化和冷启动全链路，不能声称其指标一定等同首个有效Token或它具有最优路由保证；README 的成本优化也未在所读策略枚举中对应到实现。
3. **观测平台的 URL 凭据脱敏值得直接转成需求。** `redactSecretsFromUrl` 在日志/trace管线前遮盖敏感查询参数、路径里的平台Key、userinfo；对无法解析的URL也有正则回退。仅去掉 Authorization 不足以阻止 `?key=`、预签名 `sig`、AWS签名或畸形URL漏出秘密。[redact.ts:13-61](https://github.com/Helicone/helicone/blob/7720d286110391c3e3776bb46a18fb2a13f7567c/worker/src/lib/security/redact.ts#L13-L61)、[redact.ts:84-112](https://github.com/Helicone/helicone/blob/7720d286110391c3e3776bb46a18fb2a13f7567c/worker/src/lib/security/redact.ts#L84-L112)。

**对 moni 的取舍：** v0.1 保留同模型优先级/权重/健康/并发过滤；借鉴候选状态和策略粒度概念，但不悄悄切换模型质量档。URL脱敏纳入首版日志/错误/trace统一出口。后续自适应策略先用真实TTFB/TTFT、成功率、并发与成本快照做影子评分/离线回放，再按小流量验证，不照抄Rust/Tower实现或宣传延迟数字。

## 3. Langfuse：观测与评估采样稳定，但不能采样资金事实

### 3.1 不止于一个图表页面

本快照既有trace/evaluation代码，也出现 `ai-gateway/src/telemetry` 的Rust管线。本次阅读的是指定路径，不能把读取到的功能自动理解为全部自托管版或云端权益。根 LICENSE 明确将 `ee/`、`web/src/ee/`、`worker/src/ee/`、`packages/shared/src/server/ee/` 等目录排除在MIT核心之外；Enterprise License 限制其使用，不能因为仓库公开就整体按MIT处理。

### 3.2 已读源码机制

1. **同一个 trace 的采样决策保持一致。** ingestion采样从事件取trace_id，对其SHA-256结果映射到比例阈值；项目未配置采样或缺失trace_id时保守保留，0/1及非法比例也有明确行为。这样一次trace相关事件不会因逐条独立随机而留下难以解释的残片。[sampling.ts:6-27](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/packages/shared/src/server/ingestion/sampling.ts#L6-L27)、[sampling.ts:30-58](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/packages/shared/src/server/ingestion/sampling.ts#L30-L58)。评估采样另用带域标识的SHA-256、53bit桶值，避免与其他用途隐式混用。[deterministicSampling.ts:4-29](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/worker/src/features/evaluation/deterministicSampling.ts#L4-L29)。
2. **遥测批处理有多种资源上限。** `BatchPolicy` 定义单批记录数、字节、linger、最大开放项目批数等限制，pending持有信号量许可；push超过字节或开放批上限先flush，按project组织，避免无限堆积或跨项目混批。[batch.rs:13-38](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/ai-gateway/src/telemetry/batch.rs#L13-L38)、[batch.rs:115-156](https://github.com/langfuse/langfuse/blob/5cb204754430fb77f3f7d830c651e5be7e4ad7f6/ai-gateway/src/telemetry/batch.rs#L115-L156)。这是遥测的资源控制，不证明财务事件可丢弃或跨崩溃持久送达。

**对 moni 的取舍：** 请求/attempt共享同一观测采样决定，减少日志片段；资金分录、usage结算证据、充值/退款和安全审计保持100%持久记录，不服从trace采样。未来导出器使用有界队列/批大小/超时和丢弃计数，失败不阻断推理；资金写库失败则拒绝新收费请求，不能把遥测fail-open策略用于账本。第一版不强制部署Langfuse或保存正文，评估数据集、自动打分和OTel导出可后置。

## 4. Agent Router（原 Envoy AI Gateway）：协议观测与隐私需要独立配置

### 4.1 身份和部署边界

本次访问 `envoyproxy/ai-gateway` API 最终返回 `theagentrouter/agent-router`，固定README明确：原Envoy AI Gateway现为Agent Router，仓库/网站迁移，CRD、API组、CLI、镜像与Go module路径保持原名。不能把源码中的 `github.com/envoyproxy/ai-gateway` import 当成此重定向有误。[README:10-37](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/README.md#L10-L37)。README 同时提供 standalone `aigw run` 和 Kubernetes/Envoy Gateway路径，不能笼统说必须K8s；本轮未运行任一路径。

### 4.2 已读源码机制

1. **隐私开关细化到输入、输出和调用参数。** OpenInference配置分别控制输入/输出messages、text、images、embeddings及invocation parameters，避免把“关闭日志”做成一个覆盖不明的大开关。该快照默认多个Hide字段为false，不能直接当成moni默认不留正文的安全配置。[config.go:15-62](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/config.go#L15-L62)、[config.go:78-122](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/config.go#L78-L122)。
2. **结构化脱敏和指标有独立模块。** `RedactJSONTree`递归替换字符串叶子，保留结构性type字段，避免嵌套输入漏脱敏；它也用无密钥截断SHA-256关联内容。后者不应直接当作低熵秘密的匿名化保证，moni若需内容去重标识应继续使用已设计的服务端HMAC或不保存。[redaction.go:15-46](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/redaction/redaction.go#L15-L46)、[redaction.go:49-93](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/redaction/redaction.go#L49-L93)。GenAI指标则分别定义token usage、request duration、TTFT和每output-token时延，便于统一口径，而不是将所有延迟放进一个字段。[genai.go:14-24](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/metrics/genai.go#L14-L24)、[genai.go:84-116](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/metrics/genai.go#L84-L116)。

补充的**官方文档声明**明确usage-based token窗口在响应结束后记量，已接受流可能超过窗口、后续请求再被拒绝；这不是发送前余额硬预占。[usage-based-ratelimiting.md:30-69](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/site/docs/capabilities/traffic/usage-based-ratelimiting.md#L30-L69)。本次没有追完整extproc/Redis执行链，不将文档声明冒充运行验证。

**对 moni 的取舍：** 首版采用默认元数据allowlist，禁止把body、完整URL、请求头自动附到span；将阶段/指标/脱敏接口保留清晰边界。无需引入Envoy控制面、K8s CRD、CEL表达式或全套OpenInference数据导出；以后若添加外层网关，LLM重试和客户扣费只由moni控制，避免两层重放。

## 5. 能落到 HLD / LLD 的小改进

| 事项 | 首版建议 | 后续扩展 | 可验证结果 |
|---|---|---|---|
| 观测字段合同 | 以元数据allowlist为准；URL/query/path/userinfo统一脱敏，错误对象也经过相同出口 | 受控正文留存、租户授权和保留期 | 畸形URL、大小写/重复query、预签名URL和嵌套错误均不泄漏密钥 |
| 资金与观测分层 | 资金/审计/结算证据100%持久；trace可采样，不能反向驱动扣款 | 导出到外部OTel/Langfuse等 | 采样率0和导出器故障不丢账、不改余额、不触发重新请求 |
| 采样一致性 | 同一request的attempt沿用同一采样决定和策略版本；默认保持现有请求记录完整 | 稳定trace采样、评估抽样 | 相同ID重复处理/重启后决策一致，不产生半条trace；审计仍完整 |
| 遥测背压 | 第一版指标/结构化日志有界；不让外部遥测依赖阻塞SSE和账本 | 按租户隔离、批次/字节/队列上限与丢弃告警 | 慢/故障导出端不会无限增内存；丢弃计数可见，账本记录不丢 |
| 自适应选路 | 先保留当前同模型健康/容量/优先级权重策略，记足TTFB/TTFT/成功率与成本来源 | 影子评分、冷启动/探索/老化、离线回放后小流量 | 对照基线评估效果，模型质量/地域/授权不被优化绕过，不宣称固定省钱比例 |

以上是可吸收的设计合同，不代表功能已实现。新增观测、评估或自适应路由项目不应成为moni首版必须运行的外部系统，也不改变其商业账本由PostgreSQL事务负责的边界。

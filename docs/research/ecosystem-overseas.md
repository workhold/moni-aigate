# 海外网关研究：LiteLLM、Bifrost、Portkey

状态：供 moni-aigate HLD/LLD 使用的设计研究稿。访问日为 **2026-10-30（Asia/Shanghai）**。用户已明确采用参考设计、独立实现路线；本研究未复制第三方实现进入产品代码。

## 1. 可复查的基线与研究边界

通过 GitHub API 核验 canonical 仓库和默认分支，读取该分支提交及完整 Git tree，然后按固定 blob SHA 获取少量源文件；没有只依据项目营销页下结论。默认分支是本次分析快照，不代表推荐部署版本。

| 项目 / canonical URL | 本次固定提交 | 提交时间（UTC） | 实际许可证边界 |
|---|---|---|---|
| [BerriAI/litellm](https://github.com/BerriAI/litellm) | `feda51453a3c468d04b608da4a9a972d3b365ce8`，`main` | 2026-10-29T16:39:49Z | 根 LICENSE 为 MIT，但明确排除 `enterprise/`；该目录的 Enterprise License 限制生产使用，须单独协议/有效许可 |
| [maximhq/bifrost](https://github.com/maximhq/bifrost) | `dc28f7011d978f0cdaef68758b8aad33a7612a5f`，默认 `dev` | 2026-10-29T16:31:16Z | 仓库根 LICENSE 为 Apache-2.0；企业产品能力不能由此推定全部实现包含在开源仓库 |
| [Portkey-AI/gateway](https://github.com/Portkey-AI/gateway) | `669825cbe89ee51569918b8f78a9db486fd69dd4`，`main` | 2026-05-25T13:54:51Z | 本快照根 LICENSE 为 MIT；Hosted/Enterprise 和其他分支应另行核对 |

许可证原文：[LiteLLM LICENSE](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/LICENSE)、[LiteLLM enterprise/LICENSE.md](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/enterprise/LICENSE.md)、[Bifrost LICENSE](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/LICENSE)、[Portkey LICENSE](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/LICENSE)。

本次未启动服务、未运行测试、未压测、未认证全量供应商兼容。README 中的模型数量、延迟、RPS、“零停机”等表述不作为 moni-aigate 的能力承诺或实测结论。预算治理不等于收款充值、客户财务账本和支付退款，这些商业闭环仍须独立设计。

## 2. LiteLLM：参考策略边界、预算生命周期和 fallback 约束

阅读材料：根 README/LICENSE、`enterprise/LICENSE.md`、`litellm/router.py`、`router_utils/cooldown_handlers.py`、`router_utils/fallback_event_handlers.py`、`proxy/spend_tracking/budget_reservation.py`、`router_strategy/lowest_cost.py`。README 说明它有 Python SDK 和集中式 Proxy 两种入口，当前快照还介绍 Rust 构建；本次机制分析以读取的 Python 路径为证据，不推断所有请求必经同一种运行时。

### 已验证的两条机制

**机制 A：fallback 是有边界的请求执行计划。** `fallback_attempt_key` 为一次尝试建立身份，`AttemptedFallbackTargets` 在单请求中共享已尝试集合；实际 fallback 循环检查深度上限、模型资源归属、目标访问权限和预算，再执行候选。这样既避免环形配置反复放大尝试，也避免故障回退绕过访问和预算限制。[fallback_event_handlers.py:186-220](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router_utils/fallback_event_handlers.py#L186-L220)、[同文件:821-863](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router_utils/fallback_event_handlers.py#L821-L863)

Router 配置入口可见 `allowed_fails`、`cooldown_time`、`routing_strategy`，策略包含 simple-shuffle/least-busy/usage/latency/cost 等；cooldown 逻辑按错误分类，并非所有 4xx 都熔断。[router.py:1328-1382](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L1328-L1382)、[cooldown_handlers.py:261-303](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router_utils/cooldown_handlers.py#L261-L303)

**机制 B：预算分成 reserve、actual reconcile、cancel。** `reserve_budget_for_request` 收集 Key/团队/用户等预算计数器并估算请求最大成本；`reconcile_budget_reservation` 按实际成本调整；取消路径单独处理，使用 shield 避免清理跟随请求一同取消。代码的取消策略包含已发出上游请求后保留输入成本，说明“用户取消 = 零费用”不是必然规则。[budget_reservation.py:232-280](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/proxy/spend_tracking/budget_reservation.py#L232-L280)、[同文件:334-407](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/proxy/spend_tracking/budget_reservation.py#L334-L407)

**对 moni-aigate：** v0.1 采用候选去重、统一尝试次数/总时限、fallback 再验权与预算、取消状态及未知用量处理。持久财务 hold/ledger 用自己的数据库事务实现；不直接照用 Python float 预算计数或照搬取消收费规则。最低成本/延迟自适应选路、复杂 fallback 图和多层组织预算放 v0.2；cooldown 可借鉴为独立健康状态，但完整 Closed/Open/HalfOpen 合同由自身规格定义。

## 3. Bifrost：参考 Go 请求管线、流开始边界和逐尝试成本事实

阅读材料：根 README/LICENSE、`core/bifrost.go`、`core/failureclass.go`、`plugins/governance/main.go`、`plugins/governance/tracker.go`、`plugins/semanticcache/main.go`、`examples/configs/withvirtualkeys/config.json`、`withobservability/config.json`、`withotel/config.json`、`docs/enterprise/circuit-breaker.mdx`。`docs/features/plugins/circuit-breaker.mdx` 在该快照为空，不能把空占位文档当作已实现证据。

### 已验证的两条机制

**机制 A：失败分类先于 fallback，SSE 首事件有检查边界。** `shouldTryFallbacks` 在取消、显式禁止 fallback、没有候选时停止；对于系统推断的备用链，调用方请求错误会停止，模型访问/凭证/限流等可区别处理。重试记录失败理由及是否更换凭证，并在 backoff 期间检查请求取消。流式路径在返回成功前检查首 chunk / preamble 内的错误。[core/bifrost.go:5812-5848](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L5812-L5848)、[同文件:7519-7555](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L7519-L7555)、[同文件:7690-7721](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L7690-L7721)

**机制 B：用量以物理上游尝试为事实边界。** `UsageTracker.UpdateUsage` 对失败/取消但有真实 usage 的尝试仍记成本，区分 token、成功请求次数和预算；终止结算以 RequestID + attempt 等信息去重。这是用量治理机制，不能直接当作跨崩溃的客户财务 exactly-once 保证。[plugins/governance/tracker.go:125-174](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/plugins/governance/tracker.go#L125-L174)

### 缓存、观测与企业边界

语义缓存插件源码区分精确 hash 和 embedding 相似检索；精确键包含 provider/model/cache_key/request_hash/params_hash，`cache_key` 注释明确要求租户/功能作用域。OTel 示例公开 collector、trace 类型和 metrics 配置，但本次没有实际采集链路验证。[semanticcache/main.go:154-159](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/plugins/semanticcache/main.go#L154-L159)、[同文件:244-252](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/plugins/semanticcache/main.go#L244-L252)、[withotel/config.json](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/examples/configs/withotel/config.json)

专门的 Circuit Breaker 文档明确称为 **Bifrost Enterprise**，按响应 header 信号、provider/model、可选每 Key 子电路和 cooldown 进行回退。不能把这一企业说明当作已核验的开源熔断插件实现，也不能把普通 fallback 等同于它。[docs/enterprise/circuit-breaker.mdx:7-33](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/docs/enterprise/circuit-breaker.mdx#L7-L33)

**对 moni-aigate：** v0.1 用自己的 Go 模块和可测试管线表达 `admission → route → attempt → stream → usage → settle`；日志区分 request_id/attempt_id，并分别记录客户费用和多次上游成本。首个可见响应前后的回退边界必须写成合同。缓存与自适应路由放 v0.2，未来缓存必须纳入租户隔离、价格版本、模型/参数和敏感内容策略；不因 Go 实现或 README 性能宣称预设其性能优于其他项目。

## 4. Portkey：参考明确配置合同和可组合的路由表达

阅读材料：根 README/LICENSE、`src/handlers/handlerUtils.ts`、`src/handlers/retryHandler.ts`、`src/middlewares/requestValidator/schema/config.ts`、`src/handlers/services/cacheService.ts`、`src/shared/services/cache/index.ts`、`cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md`。

### 已验证的两条机制

**机制 A：路由策略同时有配置校验和执行代码。** schema 明确 `strategy.mode` 为 single/loadbalance/fallback/conditional；包含 `targets`、`weight`、`on_status_codes`、`request_timeout`，以及 `retry.attempts/on_status_codes/use_retry_after_header`。执行器递归遍历 fallback，并在 loadbalance 分支计算总权重和随机落点。[schema/config.ts:12-80](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/middlewares/requestValidator/schema/config.ts#L12-L80)、[handlerUtils.ts:662-722](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/handlerUtils.ts#L662-L722)

**机制 B：重试包含 provider backoff 和时间预算。** retry handler 仅针对配置状态触发重试，对可配置 429 Retry-After 等待设剩余时限，超过预算跳过；请求有 AbortController 超时包装，尝试数有上限。[retryHandler.ts:4-49](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/retryHandler.ts#L4-L49)、[同文件:65-179](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/retryHandler.ts#L65-L179)

### 托管能力与开源接口不能混为一谈

CacheService 读取上下文注入的缓存函数，没有该函数或没有 cache mode 时直接禁用。因此“有 cache schema/服务接口”不证明任一自托管部署已获得托管语义缓存能力。README 对 semantic caching、provider optimization、prompt template 等带星号项明确注明 Hosted/Enterprise；cookbook 的 virtual_key 示例要求 Portkey 账户和 vault，不能直接当作无账号开源版本的端到端验证。[cacheService.ts:78-102](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/services/cacheService.ts#L78-L102)、[README:206-217](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/README.md#L206-L217)、[cookbook:9-11](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md#L9-L11)

README 还指向 `2.0.0` pre-release 分支；本研究只固定上述 main 提交，没有把其他分支的企业开源合并承诺视为该提交的事实。当前读取的 executor 存在 `isOpen` 目标过滤，但本次未追踪完整状态存储和恢复，不声称已验证独立可用的开源熔断系统。[README:6-8](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/README.md#L6-L8)、[handlerUtils.ts:646-657](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/handlerUtils.ts#L646-L657)

**对 moni-aigate：** v0.1 借鉴“配置有 schema、执行有边界、日志能解释”的做法，采用扁平候选池和版本化路由配置。设置自己的 `max_attempts/total_deadline/retryable_statuses/priority/weight` 合同，限制范围、拒绝循环和未知字段；不在第一版引入任意递归 DSL、客户自带上游凭证、托管 vault 依赖或条件表达式运行时。

## 5. 对 HLD/LLD 的合并建议

| 设计项 | v0.1 应落实 | v0.2 再扩展 |
|---|---|---|
| 路由 | 先权限/能力/健康/容量过滤，再最高可用优先级层内加权；候选去重；决策理由可追踪 | 按成本/延迟/质量实时评分，复杂策略图 |
| 重试与回退 | 一次请求统一总尝试数和 deadline；区分 caller fault / credential / capacity / transient；遵守取消和 SSE 提交边界 | 跨模型语义降级、复杂会话连续性 |
| 熔断 | 自有 Closed/Open/HalfOpen 状态合同、探测并发及故障分类；不能只做“失败后再请求另一渠道” | 动态 header 信号、自适应状态、多维度策略 |
| 计费 | 一个客户请求的唯一结算、多个物理 attempt 的成本事实；未知 usage 和取消规则独立定义；数据库持久 hold/ledger | 多层组织预算、动态定价和复杂订阅 |
| 缓存 | 第一版关闭响应缓存，避免缓存命中计费和隐私边界分散主线 | 租户隔离的精确缓存优先；语义缓存再评估 |
| 观测 | request_id + attempt_id、候选过滤原因、fallback/重试理由、TTFT/延迟、usage 来源、客户收费/上游成本 | OTel 对外导出、更多分析集成 |

这三者提供工程机制和配置表达的参考，New API 提供商业运营流程的参考；moni-aigate 的差异应落在自己的可验证合同：**收款入账可恢复、余额与预留可对账、调用过程可解释、流式状态与收费一致。** 第三方声明、企业功能和营销性能均不直接转成 moni-aigate 的既有能力。

# 路由与执行链源码校准：Bifrost、LiteLLM、Portkey

状态：2026-10-30 的补充研究，针对现有 LLD §8/§9 提出校准；**未修改主设计、未实现产品代码、未启动上游服务或执行上游测试、未调用付费供应商**。这份文件继续沿用 [海外研究基线](ecosystem-overseas.md)，不扩大项目范围。

## 1. 固定版本、证据强度与边界

| 项目 | 固定 SHA | 本次追踪范围 |
|---|---|---|
| Bifrost | `dc28f7011d978f0cdaef68758b8aad33a7612a5f` | Core 调用者、候选与凭据过滤、每 provider retry 与外层 fallback、HTTP SSE 入口、取消及交接清理；补读本地 HTTP/TCP mock 回归测试源码 |
| LiteLLM | `feda51453a3c468d04b608da4a9a972d3b365ce8` | Python Router 的异步 Chat 路径、deployment 过滤、retry/fallback、流迭代器清理、Proxy 预算取消；未把 Rust、同步 SDK、全部供应商路径视为同一实现 |
| Portkey | `669825cbe89ee51569918b8f78a9db486fd69dd4` | 递归策略执行器与 retry/fetch 超时，作为反例补证；未追通实际 SSE relay 和熔断状态恢复 |

缓存位于 `/tmp/moni-aigate-research-20261030/overseas/`。新增文件按已缓存 Git tree 的 **blob SHA** 下载；GitHub API 后续返回公共限流 403，LiteLLM/Portkey 计划补读的测试未成功取得，以下不冒充已有测试证据。Bifrost 的断言是“测试源码表达的合同”，没有运行记录，不能写成“本次实测通过”。

许可证边界延续基线：LiteLLM 根 MIT 排除 `enterprise/`；Bifrost 根 Apache-2.0 不代表 Enterprise 插件全部开源；Portkey main 根 MIT 不代表 Hosted/Enterprise 接口都能独立工作。本文只借鉴机制。

## 2. Bifrost：从入口到物理请求，再到流和终结

### 2.1 调用链与候选

`ChatCompletionStreamRequest → handleStreamRequest → tryStreamRequest → provider worker → executeRequestWithRetries → handleProviderStreamRequest → dispatchChatStream → provider.ChatCompletionStream / ResponsesStream`。

`handleStreamRequest` 先执行一次 PreRequestHook，再重新读取 provider/model/fallbacks；外层顺序遍历 fallback，每个 fallback 又进入自己的 provider 执行链。`dispatchChatStream` 还允许 Chat 转 Responses，因此“一个 Router 调用”不能直接等同于“一个 HTTP POST”。[Core 入口与 fallback:6266–6450][B-chain]、[provider dispatch:9244–9260][B-dispatch]、[类型分派:9700–9720][B-provider]

凭据池先过滤 enabled、可用值、模型 allow/deny 等，再处理显式 Key pin 与 session affinity；session 返回的 Key 仍须属于过滤后的 eligible pool。这里值得保留的是**偏好不扩大资格**，不是在 moni v0.1 引入会话粘性或客户直传上游密钥。[凭据过滤及 affinity:11152–11313][B-keys]

### 2.2 retry 与 fallback 的预算不是同一个数

`executeRequestWithRetries` 的循环为 `MaxRetries + extraAttempts`。永久凭据失败可按池内 Key 额外获得轮换尝试；加密 reasoning 的一次修复也能获得额外尝试。5xx/网络 transient 通常保持原 Key，429 等 per-key 问题可轮换，永久 Key 失败与真正换 Key 时才跳过 backoff。外层 fallback 则是另一层循环。[retry 主循环:7310–7380][B-budget]、[分类与 extraAttempts:7820–7960][B-extra]

错误分类并非财务安全证明：默认 transient 集合包含 500/502/503/504/529；显式 fallback 链与系统推断链对 caller fault 的处理不同。Bifrost 的“可重试”是可用性策略，不能据此推定上游未执行、未扣费。[分类表:17–24][B-class-table]、[分类执行:273–327][B-class]、[fallback 判定:5800–5849][B-fallback]

**测试补证：底层库重放确实会放大上层预算。** `TestStreamRetryBudgetWhenUpstreamClosesBeforeHeaders` 的注释记录一次历史回归：fasthttp 的 stale-connection hook 曾在新连接收到响应头前关闭时也重试，`max_retries=2` 曾对应最多 12 次上游拨号；当前测试断言整个 Core 路径只接受 3 个连接。不要把注释当作本次复现，也不要把“连接数”当作供应商付费次数。[测试:1071–1121][BT-total]

更底层的三个测试分别表达：新拨连接提前关闭不应隐式重试；池内过期 keep-alive 连接仍可重拨；已读到状态行但响应体截断不得重放 POST。[transport 测试:795–938][BT-transport]。这覆盖了所测 fasthttp 路径，**不能证明所有 provider SDK、代理层和自定义 transport 都无隐式 retry**；本次未取到各 provider 的完整 HTTP 实现，因此该范围留作明确证据缺口。

### 2.3 SSE 的提交点不等于 TTFT

Core 在返回流前检查首 chunk，特定 OpenAI-compatible 路径还会暂存 startup/preamble，并等失败流排空后才进入下一尝试。HTTP 入口则先 `getStream()`，成功后才设置 SSE headers 和 body reader；失败时仍能返回普通 HTTP JSON 错误。[首 chunk 检查:7685–7742][B-startup]、[HTTP 入口:2351–2415][B-http-open]

两个测试把语义边界说得很清楚：

- `TestAzureChatUsageCommitsStream`：**usage 本身就会提交流**，即使没有文本，后续错误留在该流中。[测试:13–49][BT-usage]
- `TestStreamErrorAfterContentDoesNotFallBack`：已产生 `hel` 文本后遇到错误，保持原部分结果并传出错误，fallback hit 为 0。[测试:2027–2068][BT-content]

因此不能只靠“首个有效文本 TTFT 是否发生”判断还能否切换：role、usage、工具事件或代理心跳可能早于文本。Bifrost 的内部 preamble/commit 概念也不能直接当作 moni 的下游提交点。**moni 的保守合同仍应是：下游提交 HTTP 响应或发出任何 SSE 字节后，禁止透明切换。** 若要在上游 startup 后继续判断，只能在尚未提交下游时做有界暂存；不能因为“没输出文本”就忽略可能已有消费。

Bifrost 还把 first-token fallback deadline 仅用于有后续 fallback 的尝试，最后一个 attempt 不设这一 deadline；对应测试直接写明“最后尝试不被 TTFT 切断”。这是一项产品策略，不能覆盖 moni 的每请求总 deadline。[Core:6330–6345][B-ttft]、[测试:1656–1684][BT-last]

### 2.4 取消需要贯穿 backoff、socket、流交接与终结

retry 在 backoff 中等待 context，跳过 backoff 的凭据轮换也检查 context；只有真正派发后才追加 attempt trail。[Core:7510–7580][B-cancel-retry]。对应测试分别断言 backoff 取消后只发生一次尝试，以及无等待轮换在取消后不派发、不虚记 attempt。[测试:1128–1206][BT-backoff]、[测试:1310–1380][BT-rotation]

HTTP SSE relay 的 `SendEvent` 失败后执行 `cancel()`，再排空 stream，让 provider 的终结 hook 完成后才完成 transport trace；heartbeat goroutine 退出后才关闭 reader 事件通道，避免并发 send/close。[HTTP 清理:2465–2500][B-http-cleanup]、[写失败到终结:2597–2634][B-http-end]

Core 对未交接给调用者的 stream 异步 drain。源码明确指出，直接丢掉 channel 会让生产者卡住，无法到达 deferred `ReleaseStreamingResponse`，并长期占据上游连接额度。这种 drain 是交接恢复机制，不是允许无限 drain 的通用答案。[Core:8830–8854][B-drain]

测试进一步覆盖：等待 response headers 时取消必须关闭真实 TCP socket；健康流的 body 可超过 header timeout；未交接非流响应的成功/错误 terminal hooks 都应恰好被处理一次。[socket 取消:400–444][BT-socket]、[长流:85–143][BT-long]、[终结交接:229–257][BT-abandoned]。这些测试不证明数据库财务结算 exactly-once，也不证明所有 reader/body close 路径；后一部分本次只追到 HTTP relay/Core drain 及上述 transport 测试。

## 3. LiteLLM：SDK Router 与 Proxy 治理不能混写

### 3.1 实际 Python 异步路径

`Router.acompletion → async_function_with_fallbacks → async_function_with_retries → make_call(_acompletion) → async_get_healthy_deployments → provider client`。流返回后继续由 `_acompletion_streaming_iterator` 持有底层 stream 和 deployment semaphore，直到耗尽、关闭或 fallback 才释放；因此 `acompletion()` 返回一个迭代器不代表请求已经终结。[入口:3456–3508][L-entry]、[retry:9109–9330][L-retry]、[流生命周期:3618–3835][L-stream]

候选处理覆盖 team、能力/route、主动健康、cooldown、blocked、callback、pre-call、tag、order，再处理 weighted-failover 排除。其特定模式还允许“主动健康产生的全部 cooldown”绕开 cooldown；这些都是配置语义，不能统一概括成所有模式都严格 fail-closed。[候选与 cooldown:15189–15315][L-candidates]、[order 与排除:15315–15408][L-order]

retry 可被 request/deployment/exception policy 多层覆盖；外层 fallback 有另一份深度预算和共享已尝试目标集合。fallback 目标去重、资源归属与目标权限/预算检查的结构有参考价值，但 `fallback_access_check`、`fallback_budget_check` 是可选 callback，默认 `None` 表示没有额外检查。**仅看到 Router 有检查入口，不能声称 SDK 默认替平台提供了租户授权或商业预算再准入。**[参数默认语义:1349–1395][L-callbacks]、[fallback 循环:820–901][L-fallback]

### 3.2 有内容后继续生成是明确功能开关

流迭代器先识别 committed，再判断 `enable_mid_stream_fallback_continuation`、已产生内容类型、content-policy、参数约束、目标兼容和 guardrail；不符合则抛原错误。可继续的路径会把已有文本作为 assistant prefill，减少剩余输出上限，并携带 partial usage/cost。[资格:3977–4035][L-continue]、[流内 fallback:3679–3798][L-stream-fallback]

这说明“半途自动换模型续写”涉及上下文与计费语义，不是普通 retry。moni v0.1 应继续关闭这种能力；TTFT 指标不应被用作默认允许续写的开关。

### 3.3 取消与被动健康的两个独立边界

流 `finally` 在 shield 中关闭原始/fallback stream 与 semaphore，避免请求 cancellation 同时取消清理。[清理:3807–3835][L-close]。Proxy 的预算模块则单独 reserve/reconcile/cancel；取消时按其政策保留 input cost，以 shield 执行调整。它是 Proxy 预算计数策略，**不能直接用作 moni 的可靠 usage、客户扣款或退款依据**，更不能推定裸 Router SDK 自动具备该逻辑。[Proxy cancel:379–407][L-budget]

被动健康还排除部分明显不属于渠道的问题：调用方短 timeout、caller-scoped auth、通用资源 ID 的 404、编排错误；并且仅信任服务端 stamped 的 `failed_deployment_id`，不信任客户 metadata 中的部署身份。[fallback health:59–140][L-health]。这支持 moni 在“连续 3 次渠道故障”之前先定义**失败归因**，不能从状态码直接加渠道失败计数。本次没有补取到 LiteLLM 测试，不把这些分支称为已验证运行行为。

## 4. Portkey 补证：配置名不能替代期限和取消合同

递归 executor 的 fallback 按状态决定是否进入下一 target，loadbalance 在一个 target 层计算权重。叶子分支进入 `tryPost`，实际调用把叶子请求的 `retry.attempts/onStatusCodes/requestTimeout` 交给 `retryRequest`，随后才做 response mapping。继承 retry 配置再对每个叶子执行，意味着总物理尝试数必须结合嵌套层级计算，而不是只看某个 `retry.attempts`。[executor:625–722][P-executor]、[叶子分支:780–799][P-leaf]、[retry 调用者:1197–1233][P-call]

`fetchWithTimeout` 为每次 fetch 新建 AbortController，fetch 返回后立即清除 timer，所以该分支主要约束**拿到 Response**，不覆盖随后读取 SSE/body 的全时长；可选 `requestHandler` 分支没有收到新建 signal，因此不能从这个 wrapper 推定它可取消任意 SDK 调用。`remainingRetryTimeout` 只减被接受的 Retry-After 等待，没有扣每次请求耗时、普通 backoff 或下游 relay；`retry-after` 只 `parseInt` 秒数，没有处理 HTTP-date。这不是 moni 的统一绝对 deadline。[超时 wrapper:4–49][P-timeout]、[重试与等待:65–179][P-retry]

executor 的 `isOpen` 过滤还存在“过滤后非空才替换原 targets”的局部分支；本次未追通谁注入 isOpen，因此不据此断言完整产品的熔断行为，也不采纳为空时重新启用全部目标的语义。[局部过滤:646–657][P-open]

## 5. 对当前 LLD 的可操作校准

| 编号 | 对现有设计的判断 | 建议落入合同/实现的明确规则 | 后续 SDD 验收场景 |
|---|---|---|---|
| RC-01 全层尝试预算 | 保留最多两次的保守方向，补清物理边界 | 一个 request 的所有生成调用共用唯一预算；凭据轮换、同渠道 retry、fallback、SDK retry、transport replay 和 HTTP 重定向都不能另起预算。首版优先关闭自动重定向和 SDK retry；确需 transport 恢复时，必须证明未提交请求且纳入适配器证据。记录 dial/HTTP invocation/accepted dispatch，不能混用三个计数 | Core=2、SDK 默认重试、旧池连接、新连接提前断开、307/308 各自组合；用 fake upstream **实际收到的生成请求**计数，另计拨号；任何层不能隐式发出第3个生成请求 |
| RC-02 安全性与错误分类分开 | 现有“普通5xx/写后超时不透明重试”比竞品更保守，应保留 | 分类先产出 fault origin、消费证据、是否已提交下游、retry eligibility；只有未写出证明或版本化 profile 的可信无消费拒绝，才允许在剩余预算内重试。状态码配置不能覆盖消费未知；429也必须符合渠道合同 | 200首chunk错误、429但profile未知、500且上游已计费、已写出后断线、客户端取消均不因“没文本”而重放；失败上游成本和客户唯一结算独立 |
| RC-03 deadline 与流提交 | 现有4类超时方向正确，需把“首响应”定义到具体阶段 | 全请求一个单调时钟绝对 deadline，排队、准入、连接、headers、startup暂存、backoff、body与下游写入共用剩余时间；阶段 timeout 只能取其与剩余时间的较小值。下游 header/首字节提交状态独立于TTFT，首版任何已提交响应不切换；Retry-After解析delta-seconds或HTTP-date，超过cap/剩余时间则不重试 | 慢headers、仅role/心跳、usage先于文本、长健康body、首尝试耗尽大部分总预算、最后attempt仍受总deadline、HTTP-date Retry-After；禁止把header timer当全流timer |
| RC-04 取消与资源所有权 | “立即取消并释放许可”需明确实际清理顺序 | 立即发出cancel，关闭body/reader并让adapter退出；代表真实上游并发的许可在该执行资源停止后释放，不能只因handler返回就释放。terminal usage/settlement使用独立且有界的清理上下文；不能被已取消的request ctx一并打断，失败保留持久hold交恢复流程。reader排空必须有界，不能永久生成drain goroutine | backoff中取消、轮换准入前取消、headers等待中断开、下游慢写/退出、流交接失败；断开后的socket关闭、goroutine/许可恢复、terminal处理一次、结算失败仍可恢复 |
| RC-05 被动健康归因 | 3次/30秒/单半开探测可作首版参数，不能只按4xx/5xx粗分 | 只有服务端归因到该channel/version的网络/上游故障才计数；客户取消/短deadline、下游写失败、本地权限/余额/容量拒绝不污染渠道。模型无权限/地域拒绝、凭据失效分别作用于binding/profile或credential；不因任意403隔离整条渠道。429单独按已验证拒绝信号短暂冷却。单实例半开令牌原子获取；全池open按503处理，不恢复被硬约束排除的候选 | 攻击者极短timeout、伪造metadata、模型级403、凭据401、429、全池open、并发半开；探测计成本、不能重放真实客户请求 |
| RC-06 配置和优先级 | 当前不可变route+channel_version是必要合同，竞品局部机制不能代替 | 每请求冻结候选身份/endpoint/profile/credential版本；每attempt仅重新收紧授权/禁用/健康/容量，不能临时取新endpoint或新增候选。对每次执行新建attempt局部状态，避免wait_for_usage/headers/usage沿用前渠道。先剔除已尝试与不合格候选，再选最高可用优先级层并加权；获得容量失败时只做有界重新选择，不把未派发候选记成已发生HTTP尝试 | attempt1与2间发布endpoint/profile新版本、撤销credential、前渠道wait_for_usage=true后渠道false、最高层全部不可用、加权选中后并发额度被抢、旧版本回滚；观测给出候选排除原因和实际版本 |

以上校准不要求增加 Redis、多活、复杂 DSL、自动续写或自适应路由。它们把当前两次尝试、单实例健康、不可变配置和持久财务预占连接成可测试的执行合同。SDK/Proxy 的预算、core retry、下游流 relay 分属不同生命周期，不能根据单个函数名宣称端到端能力已经成立。

## 6. 固定源码链接

[B-chain]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L6266-L6450
[B-dispatch]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L9244-L9260
[B-provider]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L9700-L9720
[B-keys]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L11152-L11313
[B-budget]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L7310-L7380
[B-extra]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L7820-L7960
[B-class-table]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/failureclass.go#L17-L24
[B-class]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/failureclass.go#L273-L327
[B-fallback]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L5800-L5849
[BT-total]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/streamfallback_test.go#L1071-L1121
[BT-transport]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/providers/utils/streamingclient_test.go#L795-L938
[B-startup]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L7685-L7742
[B-http-open]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/transports/bifrost-http/handlers/inference.go#L2351-L2415
[BT-usage]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/azurestreamretry_test.go#L13-L49
[BT-content]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/streamfallback_test.go#L2027-L2068
[B-ttft]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L6330-L6345
[BT-last]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/streamfallback_test.go#L1656-L1684
[B-cancel-retry]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L7510-L7580
[BT-backoff]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/streamfallback_test.go#L1128-L1206
[BT-rotation]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/streamfallback_test.go#L1310-L1380
[B-http-cleanup]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/transports/bifrost-http/handlers/inference.go#L2465-L2500
[B-http-end]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/transports/bifrost-http/handlers/inference.go#L2597-L2634
[B-drain]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/bifrost.go#L8830-L8854
[BT-socket]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/providers/utils/streamingclient_test.go#L400-L444
[BT-long]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/providers/utils/streamingclient_test.go#L85-L143
[BT-abandoned]: https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/core/abandonedstream_test.go#L229-L257
[L-entry]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L3456-L3508
[L-retry]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L9109-L9330
[L-stream]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L3618-L3835
[L-candidates]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L15189-L15315
[L-order]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L15315-L15408
[L-callbacks]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L1349-L1395
[L-fallback]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router_utils/fallback_event_handlers.py#L820-L901
[L-continue]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L3977-L4035
[L-stream-fallback]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L3679-L3798
[L-close]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router.py#L3807-L3835
[L-budget]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/proxy/spend_tracking/budget_reservation.py#L379-L407
[L-health]: https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm/router_utils/fallback_event_handlers.py#L59-L140
[P-executor]: https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/handlerUtils.ts#L625-L722
[P-leaf]: https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/handlerUtils.ts#L780-L799
[P-call]: https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/handlerUtils.ts#L1197-L1233
[P-timeout]: https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/retryHandler.ts#L4-L49
[P-retry]: https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/retryHandler.ts#L65-L179
[P-open]: https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/handlers/handlerUtils.ts#L646-L657

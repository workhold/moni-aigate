# 性能审计：首字节口径、数据库准入与竞品对照

日期：2026-10-30。审计对象是当日工作区的完整收费调用路径，包含真实实例 Guard 与 005 绑定隔离检查。本次只读实现、测试和已有报告；未运行新容量实验，未访问真实收费模型，未修改实现或预览数据库。本文中的行号对应审计时源文件，后续实现变动应重新核对。

本稿记录v1只读审计时状态。之后root已补v2客户端内容测量，最新执行见 [capacity.md](capacity.md)；Rust原生入口也已独立核实，见 [固定参考证据](../research/performance-references.md)与[002性能规格](../../specs/002-performance-parity/spec.md)。下文“尚未核实”的说法仅描述本次审计本身，不覆盖后续证据。

## 1. 结论与证据边界

现有 **100 并发 p95=122.57ms 是额外首个响应正文 byte 的延迟，属于 TTFB 口径，不是模型真实首 token 延迟**。已有测试模拟上游立即发送包含文本的 SSE 帧，但客户端只读到一个正文 byte 即停止首响应计时，没有等到完整 SSE 事件并识别非空文本或工具增量。相关位置为 [capacity_test.go:315](/Users/liminghan/Documents/moni-aigate/internal/server/capacity_test.go:315)、[capacity_test.go:365](/Users/liminghan/Documents/moni-aigate/internal/server/capacity_test.go:365)。

已有最新完整链路中，客户端发起到上游到达的 p95 为 120.29ms，mock 释放到客户端首 byte 的 p95 为 3.21ms。主要排查方向是上游派发前的准入流程；两个阶段各自的 p95 不能直接相加或相减来重建总体 p95。来源：[capacity.md:94](/Users/liminghan/Documents/moni-aigate/docs/verification/capacity.md:94)。

该轮还有同机短时其他 QA 编译/测试，客户端、网关、mock 共处一个 Go 进程；它是可复现记录，不能用于严格归因、对外 SLA 或证明接近竞品。原定 4 vCPU / 8GB、100 并发、额外首字节 p95≤50ms 仍未通过，也没有目标环境认证。

## 2. 应分别报告的延迟

| 指标 | 定义与用途 | 不应混入的事件 |
|---|---|---|
| 客户端 TTFB | 客户端请求开始到首个响应正文 byte | 不将首个 byte 直接称为模型 token |
| 客户端可观测 TTFT | 客户端请求开始到解析完整 SSE 事件且发现首个非空文本、拒绝说明或真实工具增量 | role-only、空 chunk、心跳、注释、usage-only、terminal-only |
| 模型内部首 token | 供应商内部排队、prefill、生成首 token 的度量 | 网关无法仅凭 SSE 直接准确获知 |
| 额外首内容延迟 | 在受控 mock 下，客户端可观测 TTFT 扣除该请求明确记录的上游等待 | 不以两个独立分布的 p95 相减冒充逐请求开销 |
| 派发前准入 | 客户端发起到 mock 收到该次 POST | 必须包含权限、资金、Guard、连接等真实生产步骤 |
| 流转发间隔 | 上游写出有效事件到客户端解析该事件 | 与首响应、整流完成分别报告 |
| 整体时长 | 发起到成功终止或失败终止，并单独标注取消/失败 | 不只统计成功且快的请求 |

SSE 事件可能一次包含多个 token，所以客户端可观测 TTFT 是实用的代理指标，应明确其定义。现有 LLD 已区分提交点、TTFB 与有效内容，见 [LLD.md:334](/Users/liminghan/Documents/moni-aigate/docs/design/LLD.md:334)。

## 3. 关键路径审计

### 3.1 三个派发前持久事务与重复读取

[gateway.go:76](/Users/liminghan/Documents/moni-aigate/internal/server/gateway.go:76) 先读取授权模型，[gateway.go:90](/Users/liminghan/Documents/moni-aigate/internal/server/gateway.go:90) 再读取候选。随后依次执行：

1. `Billing.Reserve`：锁定权限与 Key 预算，取得有效价格，插入请求/预占/恢复任务，在同一事务记 AVAILABLE→HELD 分录并更新 Key held。
2. `Billing.RecordAttempt`：重复锁定主体和预占，检查绑定隔离、发布版本、deadline、重试证据、成本版本，再插入尝试。
3. `Billing.PrepareDispatch`：再次读取主体权限、渠道、凭证、地域、隔离和 deadline，将尝试推进到 `sending`，将请求推进到 `dispatching`。
4. `Guard`：复核实例会话后才调用上游。

入口见 [gateway.go:117](/Users/liminghan/Documents/moni-aigate/internal/server/gateway.go:117)、[gateway.go:125](/Users/liminghan/Documents/moni-aigate/internal/server/gateway.go:125)；事务实现见 [reserve.go:47](/Users/liminghan/Documents/moni-aigate/internal/billing/reserve.go:47)、[evidence.go:62](/Users/liminghan/Documents/moni-aigate/internal/billing/evidence.go:62)、[evidence.go:134](/Users/liminghan/Documents/moni-aigate/internal/billing/evidence.go:134)。

每个事务先单独设置两个超时，分别占用 SQL 往返，见 [store.go:35](/Users/liminghan/Documents/moni-aigate/internal/billing/store.go:35)。`lockPrincipal` 的租户、成员、用户、Key、模型、授权检查又分成多次 SQL，见 [store.go:243](/Users/liminghan/Documents/moni-aigate/internal/billing/store.go:243)。这些安全检查有必要；逐条网络往返的实现形式存在优化空间。

### 3.2 Key 与租户钱包形成竞争域

`Reserve` 必须独占 Key 预算并锁定租户 AVAILABLE/HELD，才能在并发条件下防超支。当前测试每租户有 20 个并发请求、每 Key 有 10 个并发请求，会显著放大这些竞争。账本已经只锁实际触及账户，并按固定账户 ID 顺序取锁，见 [store.go:155](/Users/liminghan/Documents/moni-aigate/internal/billing/store.go:155)。

`RecordAttempt` 使用通用 `lockRequestWithChannel`，即使这一步没有修改 Key spent/held，仍取得 Key `FOR UPDATE`，见 [evidence.go:39](/Users/liminghan/Documents/moni-aigate/internal/billing/evidence.go:39)。这是可单独验证的锁收缩候选：为不修改预算的尝试记录设计共享 Key 锁，继续用 reservation 的排他锁串行化同请求状态；必须验证与预占、结算、撤权及隔离操作的锁顺序和竞态，不能直接把通用 helper 全部改成共享锁。

平台 REVENUE 账户可能影响持续结算吞吐，但本轮 barrier 要求所有上游到达后才返回数据。因此，不能把本轮派发前 p95 直接归因于后续收入账户竞争。应在持续负载中另外测量结算与新准入重叠时的影响。

### 3.3 Guard 的专用连接串行探活

[instance.go:37](/Users/liminghan/Documents/moni-aigate/internal/operations/instance.go:37) 对同一持有 advisory lock 的专用连接加 mutex 后同步 `Ping`；所有请求共享该队列。API 入口和每次派发前各调用一次。专用连接不走普通池的 acquire 计数，所以单看池统计无法归因其开销。

另有需要用专项测试验证的边界：`guardContext` 在进入 mutex 队列前创建 1 秒 context，见 [server.go:419](/Users/liminghan/Documents/moni-aigate/internal/server/server.go:419)。排队时间可能耗尽 context，随后 Ping 失败就永久 fence。当前报告没有证明发生过此现象。后续应分别观测队列等待与探活耗时，明确过载时的 fail-closed 合同。

优化可以研究“一次全新探活服务本次探活开始前已经排队的一批检查”，但不能用历史 TTL 缓存代替派发前检查，也不能悄悄重连丢失锁的会话。实际采用前要证明新批次不会读到旧检查结果，失败会立即永久 fence，且不会越过既有接管隔离窗口。

### 3.4 每次上游请求都新建 TCP/TLS 会话

当前每次调用创建 Transport，显式禁用 KeepAlive 与 HTTP/2，见 [client.go:60](/Users/liminghan/Documents/moni-aigate/internal/provider/client.go:60)、[network.go:179](/Users/liminghan/Documents/moni-aigate/internal/provider/network.go:179)。这有助于规避库层隐式 POST 重放，也保留每次 dial 的 DNS/CIDR 验证；真实 HTTPS 链路会付出额外握手成本。现有容量 mock 是本地 HTTP，不能评估该成本。

连接复用要另立规格：按目标、TLS 和网络策略作用域隔离池，保留无自动重放、重定向禁用、准确发送证据、撤销/过期版本失效、DNS/CIDR 安全以及连接数/空闲边界。仅将 `DisableKeepAlives` 改成 `false` 不足以完成这项优化。

### 3.5 候选读取和解密

每请求读取全部候选并逐个解密凭证，见 [catalog/store.go:341](/Users/liminghan/Documents/moni-aigate/internal/catalog/store.go:341)。候选多时可评估按不可变发布版本缓存目录数据、选中后才解密凭证，保留派发事务中的最新撤权/隔离/地域复核。不得用目录缓存承担最终授权，不得无界缓存明文秘密或已撤销凭证。

## 4. 不可牺牲的语义

- 钱包 AVAILABLE 和 Key 的 spent+held 不能被并发穿透；金额保持整数精度、溢出检查和双边平衡分录。
- 发出上游之前必须已有持久预占及派发证据。不得用异步扣款、进程内余额、先发送后写库来改善数字。
- 派发前保留租户/用户/成员/Key/模型/授权/地域/凭证/绑定隔离和 deadline 的最新校验。不可因缓存绕过撤权。
- 尝试身份、幂等键、成本/价格版本和最终用量证据可审计；崩溃和未知消费保持可恢复，不把未知强行标成未消费。
- 已提交下游响应后禁止透明换路；未经可信未发送/未消费证据不得重放收费生成；取消和总 deadline 仍覆盖全部阶段。
- 实例失去持锁会话必须 fail closed；不得以静默重连制造双活。
- 压测结束必须核对 POST 数、DONE/失败、结算、Key spent、账本平衡和残留 held/许可，不能只公布成功请求延迟。

## 5. 建议优先级与验证方法

| 优先级 | 工作 | 预期作用 | 完成证据 |
|---|---|---|---|
| P0 | 首有效 SSE 内容计时，保留旧 TTFB 字段；增加独立角色/心跳/usage 前置样本 | 修正指标命名，不再用首 byte 代替 TTFT | 测试能证明前置非内容不会提前计时，terminal-only 不伪造 TTFT |
| P0 | 分阶段耗时：权限/目录、池等待、三个事务、锁等待、Guard 等待与 Ping、DNS/TCP/TLS、上游首有效事件、下游写入、结算 | 确定真正瓶颈，避免盲目加池 | 固定构建和配置的原始分布；观测自身成本有记录 |
| P1 | 事务内批量 SQL、批量分录；减少可证明冗余的数据库往返 | 缩短持锁时间，保持同一持久事务 | 资金/撤权/崩溃/幂等回归通过，前后独占多轮压测 |
| P1 | 单独审查尝试记录的 Key 锁强度 | 降低同 Key 无预算变更步骤的串行化 | 锁顺序分析，以及 Reserve/RecordAttempt/PrepareDispatch/settle/revoke 并发测试 |
| P1 | 设计尝试记录+最终派发准入的合并事务 | 少一次提交与重复检查，保留所有状态语义 | SDD 明确 prepared/sending 的持久边界、取消、崩溃、重试、恢复证据后实施 |
| P2 | Guard 批次合并及有界排队 | 减少专用会话往返，控制过载 | 领导权丢失、超时、批次边界、接管隔离专项测试 |
| P2 | 受控连接复用、版本目录缓存和选中后解密 | 减少真实 HTTPS 握手及大候选池成本 | TLS/重放/SSRF/撤权回归，加明文与TLS两类负载对照 |

扩大数据库池不作为独立优化结论。已有池 8/32 的单轮结果显示更多连接并未稳定降低 p95；必须结合竞争域和数据库排队解释，见 [capacity.md:63](/Users/liminghan/Documents/moni-aigate/docs/verification/capacity.md:63)。

## 6. 对照 LiteLLM Rust 与 Bifrost 的实验要求

现有研究固定了 LiteLLM 的 `feda51453a3c468d04b608da4a9a972d3b365ce8` 和 Bifrost 的 `dc28f7011d978f0cdaef68758b8aad33a7612a5f`；这只是参考代码快照。LiteLLM 的既有分析主要来自 Python 路径，README 中出现 Rust 构建不能证明已核实 Rust 版的部署工件、覆盖功能和性能。Bifrost 已审阅的核心是 **Go 实现**。证据：[ecosystem-overseas.md:9](/Users/liminghan/Documents/moni-aigate/docs/research/ecosystem-overseas.md:9)、[ecosystem-overseas.md:21](/Users/liminghan/Documents/moni-aigate/docs/research/ecosystem-overseas.md:21)、[ecosystem-overseas.md:35](/Users/liminghan/Documents/moni-aigate/docs/research/ecosystem-overseas.md:35)。本次没有得到新的远程源码或运行结果，不声称核实了更新版本。

须先锁定实际比较的仓库、release/commit、镜像摘要、Rust/Go 构建参数和配置，再运行同机同资源对照。相近性能作为目标可接受，但在这些基线和测试尚未完成时不能写成“已接近”。

对照应有两条明确结果：

1. **共同网关能力对照**：相同协议、认证、路由、日志和限流设置，关闭响应缓存；明示各产品实际启用的行为。若使用专用最小转发 harness，标注它是实验 harness，不能替代完整商业接口验收。
2. **完整商业调用对照**：开启 moni 的同步持久余额预占、派发授权、审计、恢复和结算；比较方若没有同等语义，标注能力差异，不能把纯转发微秒宣传数字当同功能验收门槛。完整 moni 流程仍须满足既定 ≤50ms 目标，不能用共同能力对照达标来替代。

统一实验至少包含：

- 固定 4 vCPU/8GB 的资源边界、独立 PostgreSQL 配额和拓扑；客户端/mock 单独统计资源。构建和负载运行分离，关闭同时编译/QA。
- 同一 mock 上游、同一请求体/响应大小、模型/Key/租户分布、HTTP/TLS、连接冷暖策略。带实际上游直连基线，不能只对比各 README。
- 预热后多轮独占测试，报告每轮原始数据及 p50/p95/p99、吞吐、接受/拒绝/错误/超时、CPU/RSS/分配、连接/锁等待、资金与资源归零结果。
- 分别测突发和稳定到达率、非流与流式、短流/长流、慢客户端、不同 body、单租户热点与多租户分散；当前 100 个短流单次 burst 不等于持续 RPS。
- 对照结果同时保留端到端 TTFT、TTFB、可解释的逐请求额外延迟与总时长。报告负载拐点和拒绝率，避免通过提前拒绝隐藏长尾。

正式“接近”比例和允许绝对差值应写入后续性能规格，并以已锁定的实际工件做验收；本文不从营销数字推定门槛，也不降低原有资金、安全和 50ms 要求。

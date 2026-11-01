# 源码校准：身份、撤销、额度与观测执行边界

状态：DRAFT / 进一步设计输入；2026-10-30。只校准已经研究的 **Unkey** 与 **Agent Router（原 Envoy AI Gateway）**，不新增首版组件，不修改 HLD/LLD，不把测试文件的存在写成测试已通过。

本轮在前次固定 SHA 上追到真实 HTTP 调用者、默认配置、执行分支与相关测试。匿名 GitHub API 已限流、raw 域名连接重置后，改用官方 `codeload.github.com` 的同一提交归档作静态阅读；没有运行上游软件、测试套件或真实模型。归档在临时目录，未拷贝上游实现进 moni。

| 对象 | 固定提交 | 本轮增加的证据范围 |
|---|---|---|
| `unkeyed/unkey` | `01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79` | `/v2/keys.verifyKey` 的路由/认证/处理器、SWR缓存、权限和生命周期判定、删除/更新、credits实际装配和降级、审计缓冲、集成测试 |
| `theagentrouter/agent-router` | `931b020400a40c0e0f9a819a1c4b229ef945b8bd` | extproc主调用、OpenAI错误转换、trace错误/输出记录、chunk保留、usage和流式终止、运行配置与静态API Key handler及测试 |

许可继续沿用[商业研究](extended-commerce.md)和[观测研究](extended-operations.md)的边界：Unkey 当前根 FSL 有竞争使用限制；Agent Router 为 Apache-2.0。本轮只参考机制，不据此选择代码复用。

## 1. Unkey：验证顺序可借鉴，缓存与失败行为必须读到调用者

### 1.1 验证接口有两个身份，客户端可选字段不能直接套用给 moni

实际入口为受保护路由，认证中间件先构造 principal、应用 workspace API 限流，再进入 handler。[U1][U2] handler 将待验证的 `req.Key` 摘要送入 Key service，先检查目标 workspace、调用者对目标 API/URN 的权限，再构造验证选项。[U3] 因而 `permissions`、`credits.cost`、临时 ratelimits 是**有权限的验证服务调用者**提交的参数；这不是“最终收费客户可以自行声明费用/所需权限”的接口。

默认行为也依赖调用者：有 `RemainingRequests` 且未传 credits 时默认消耗 1；IP allowlist 自动检查；权限表达式仅在请求提供时检查；自动应用的限流仍生效。[U4] `Verify` 先 keyspace、已有状态、IP、权限，再检查 rate limits、credits。[U5] `WithKeyspaces` 明确区分省略与显式空集合；keyspace mismatch 对外清空目标元数据。[U6]

测试 `TestVerifyKey_KeyspaceRejectionsDoNotConsumeQuota` 覆盖跨 keyspace、跨 workspace、allowlist 不能授予 API 权限，拒绝后下一次合法验证仍剩预期 credits/限流额度。[U7] 另有不同 API 权限返回 `200 + NOT_FOUND` 的断言，避免枚举目标 Key。[U8] **测试未运行**；这些只证明上游写了相应契约，不证明 moni 已有该行为。

**对 LLD 的校准：** moni 的模型权限与价格由服务端目录/授权计算；请求选项只能收紧能力，不能给客户提供 `cost=0`、省略权限检查或覆盖已配置限流的入口。内部 Key 判定与公开 HTTP 状态码分层的方向正确，但不照抄 Unkey 的 `200 + valid` 形式。管理会话、客户 Key、待验证对象应在类型和 handler 装配上明确区分。

### 1.2 “撤销后移除缓存”不等于下一次派发必定看到撤销

`Get` 实际通过 `keyCache.SWR` 从只读数据库读取角色、权限、IP、状态和限流，缓存构建时已解码这些信息。[U9] SQL 将直接权限与角色权限汇总到同一结果中。[U10] 默认验证缓存 fresh 为 **10 秒**、stale 为 **10 分钟**；SWR 的 stale 命中立即返回旧值，同时安排后台刷新。[U11] 生命周期检查使用这个已加载的结果判定删除、禁用、到期。[U12]

删除 handler 在数据库事务中删除/软删并写审计，提交后调用 `KeyCache.Remove`；所读 `Remove` 实现仅删除本地 store 条目，没有在该函数中广播或等待其他进程失效。[U13] 更新 credits 后的 counter invalidate 失败只记错误，API仍返回成功。[U14] 因此本轮**不能**从这条路径推导全节点即时撤销或在途请求撤回保证。删除测试主要检查数据库标志、物理删除和关联记录清理，不是多进程缓存传播/派发竞态测试。[U15]

**对 LLD T1.5 的校准：** 保留“每个 attempt 在派发准入事务重新查权”的强合同。不可把该查询优化成上述 SWR、只读副本或先前构造的 verifier。实现时须给每种撤销/收紧操作列出共同锁行：Key更新锁 Key，成员授权表增删锁 membership，租户/Key地域变更锁各自父行，平台模型收紧锁 model。特别是空授权集合没有可锁子行时，父行仍是同步点；只锁当前查询到的 grants 会漏掉并发增删。该矩阵是已有 LLD 原则的可执行化，不能从 Unkey 推定已经解决。

### 1.3 上一轮需要收紧的结论：局部 fail-closed 不代表整个 Key 验证 fail-closed

前次读到 ratelimit 包 CAS 竞争耗尽时拒绝；继续追调用者后，`KeyVerifier.withRateLimits` 在 limiter **返回 error** 时记录错误后 `return nil`，继续处理请求。[U16] workspace API 限流读取失败也显式 fail-open。[U17] 二者与“收到明确拒绝结果时拒绝”不同。credits 调用出错则沿 `withCredits → Verify → handler` 返回错误。[U18] 不能用模块注释或某个 CAS 分支概括整条鉴权/额度链。

**对 moni 的校准：** 形成按依赖列出的失败矩阵，而非一个笼统 fail-open 开关：权限/撤销查询、资金与 Key budget 任一不可用均不得派发；普通观测故障可丢弃；RPM/并发内部错误应按已选保守策略返回系统不可用，不能变成“未配置限流”而放行。测试需同时注入“明确超限”和“依赖 error”，确认二者内部原因不同、都不会误调用上游。

### 1.4 credits 是准入消耗，不是 moni 的金额冻结/结算

实际启动装配使用 counter credits 服务：数据库读回调使用 RO，扣减回调直接执行 UPDATE，counter TTL 配为 **60 秒**，不是包默认的 10 分钟。[U19] counter成功扣减后把 `KeyID + Cost` 写入容量 10,000 的内存 replay buffer；回放写DB失败仅记录错误。`Drop=false` 表示满时阻塞 channel，不等于跨进程崩溃持久。[U20]

Redis错误会切 direct DB fallback。该 fallback 是先查余额、判断，再独立调用扣减；SQL 用不足时置0的 CASE，不返回一个“有条件扣减成功/拒绝”的准入结果。[U21] 单看这些调用与SQL不能证明并发fallback仍有全局硬额度保证，尤其不能把“最终余额不负”当成“所有已放行费用之和不超额度”。没有 request级 reserve/settle/cancel、永久资金幂等或平衡流水语义。

多节点 decrement 测试确实断言成功数、剩余额度，并等待数据库最终一致；所读片段使用最长10秒的 `Eventually`，未覆盖Redis故障切DB、扣减成功而响应丢失、replay进程崩溃等场景。[U22] **本轮未运行这些测试，也不将缺少的覆盖写成上游已发生的事故。**

**对 moni 的校准：** 现有 PostgreSQL T1/T2 仍是唯一资金入口。禁止将“Key验证成功”顺手再做独立预算扣减，导致冻结和验证各扣一次；禁止用只读副本余额作收费准入；若提交结果未知，按 request/operation 唯一事实查询，不因重试验证而重复收款。运行时RPM、并发许可、Key lifetime预算、钱包资金分别定义恢复与失败行为。

### 1.5 名称含 audit 不表示相同的持久等级

verify handler 将 telemetry 和 direct audit 放入 batch；direct audit 仅对 root principal生成。启动配置未提供ClickHouse时该流是noop，提供时 `direct_audit_logs` 和 `key_verifications` 均为 `Drop=true`。[U23] 测试甚至明确验证 KeyVerify audit直接进ClickHouse、**没有MySQL outbox行**。[U24] 相比之下删除审计就在删除的业务事务里。[U13]

这不是简单判定上游选择错误，而是核对其审计产品语义：高吞吐验证记录和成功敏感变更，保证不同。moni 的安全事件分类应以“事件是否必须与业务提交同成败”定义，而不能因为叫 audit 就统一走账本队列，也不能将账户/权限/凭据变更误接到可丢遥测。回滚必须无成功变更审计；成功变更必须能沿 actor、target、revision追溯。

## 2. Agent Router：正文隐私、错误透传和 usage 收集是独立执行路径

### 2.1 debug脱敏、正常trace输出、错误trace不能混成一个开关

CLI `enableRedaction` 默认 false，传给 extproc；它主要控制 debug正文日志，而凭据header mutation已有独立的强制脱敏分支。[A1] 回归测试显式断言 `enableRedaction=false`、debug开启时，上游Authorization仍被遮盖且实际转发头不被修改。[A2]

OpenInference config另从环境读取，多个 Hide 默认 false；配置在创建recorders时读取一次共享，避免端点间漂移。[A3] 正常 Chat response在 `HideOutputs=true` 时把 `output.value` 置为遮盖值；已有正常请求/响应隐藏的测试。[A4] **但错误路径不同**：extproc把转换后的错误body（或原body）传入 `EndSpanOnError`，Chat recorder不检查 `HideOutputs` 就交给 `RecordResponseError`；后者拼接body到 exception.message 和 span status。其测试还明确期望完整错误JSON出现在这两个字段。[A5]

公共响应也是另一条路径：OpenAI-compatible的JSON错误保持透传；非JSON错误将原body放入 `error.message` 后重新编码。[A6] 因此“debug启用脱敏”“正常trace隐藏输出”均不能证明公开错误或exception状态已经无秘密。这是代码边界观察，不扩展成对所有协议/部署配置的安全审计结论。

**对 LLD 的校准：** 除观测白名单外，新增独立 `PublicError` 转换合同：固定错误码/受控短消息/非秘密request_id，禁止从 `err.Error()`、上游 `message/type/param`、HTTP头或response body直接构造客户错误。SSE终止错误同样适用；内部分类保留受控原因及可信usage，不能为了隐藏错误正文丢掉计费证据。测试用唯一秘密标记遍历普通JSON、非JSON/HTML、嵌套错误、异常span的event/status、公共响应、审计diff和证据引用，逐出口断言。

### 2.2 末端隐藏不等于过程中没有保留正文

实际 translator在解析到SSE chunk时调用 `span.RecordResponseChunk`；通用span实现将所有chunk指针追加到切片，到EndSpan再交recorder合并。[A7] 所读实现没有按Hide开关提前丢内容、字节限额或增量聚合。因此，即使最终属性被遮盖，也不能由此推导“流式过程中不累计正文/内存有界”。正常隐藏测试检查的是最终span属性，不是整个内存生命周期。[A4]

**对 moni 的校准：** 默认不留正文应在**采集前**生效：usage/时延只保留增量计数和有限状态；不可为trace/错误诊断旁路累积所有delta、tool arguments或完整回答。未来打开trace时也不能改变数据面内存级别。验证长SSE、超长tool arguments、Hide开关组合，确认观测内存不随回答总长度增长；正常转发仍可使用既定有界event缓冲。

### 2.3 HTTP 200、流结束、观测成功和可结算usage不是同一事实

extproc对stream overload有独立分支：输出协议终止事件、记录失败、置 `responseStreamTerminated`，后续chunk替换为空；测试覆盖首事件overload、内容后overload，以及后续chunk不得再次输出。[A8] 这说明流已发HTTP 200也需有终态；不能靠HTTP状态判定业务成功，更不能由失败自动推出未消费。

正常路径将translator报告的累计usage覆盖已有计数，而不是逐chunk相加；usage一旦出现就写dynamic metadata，不等TCP/Envoy EndOfStream，以免客户读完usage立刻断开导致access log漏量。相关测试明确构造 `EndOfStream=false` 的usage chunk并断言metadata已存在。[A9] 但metadata/access log仍是观测出口，不是持久结算证据；错误分支还会在正常usage更新之前提前返回。本轮不从该项目推导“流式错误、崩溃后仍完整恢复账单”。

**对 moni 的校准：** 增加明确的 `terminal_error_emitted`/末端写入守卫，禁止错误之后继续输出内容或补成功DONE；终止错误不触发透明重试。usage的可靠性状态必须独立于“是否有usage字段”：累计但未终结的usage可作下界，缺失不能记作真实0。已拿到符合profile的可靠最终usage，即使客户在DONE前断开仍走唯一T2；只有下界或未知用量按核对合同处理，保留之前可靠信息，不因终态错误清零。

### 2.4 配置构建成功不等于热更新/凭据轮换合同已完成

`NewRuntimeConfig` 先构建backend和auth handlers；APIKey handler在构造时复制并trim当前key，实际 `Do` 用这个已捕获值注入Authorization。[A10] 因此不能根据注释“从本地文件提取Key”推断每次请求都会读最新凭据。`Server.LoadConfig` 构造成功后直接替换指针，源码还标明这处race；所读LoadConfig测试只断言非空配置，没有并发重载/旧请求与新凭据交叉断言。[A11] 这不代表其他部署路径都无同步，但本轮证据不足以把“热更新原子、安全轮换”当作可照搬的上游保证。

**对 moni 的校准：** 区分两类操作：①上游凭据内容更新创建新credential version，发布新channel/route revision；旧request继续引用旧版本，除非旧版本紧急撤销阻止下一attempt；②本地主加密密钥轮换只改变ciphertext包装和key_version，保持credential身份/上游秘密语义不变。读取者不得在一次attempt中把旧endpoint与“最新Key”拼接。发布前检查凭据引用/解密材料可用；旧加密主密钥回收须等所有相关密文（含备份恢复所需版本）完成处理，不能只看活动配置指针。

## 3. 建议交给 SDD 的具体校准项

以下都是**待实现/待验收建议**，不自动扩展首版协议和部署范围。

| 校准项 | 当前LLD落点 | 需要增加的可观测断言 |
|---|---|---|
| SEC-C1：权威派发查权 | §6 T1/T1.5、§8 | 使用真实PG和可控事务屏障：撤销先提交则首次/回退attempt都不发；gate先提交仅允许已获准attempt。授权子表删除/插入也经父行同步；缓存预热不改变结果。锁超时不得派发。 |
| SEC-C2：失败矩阵与预算单入口 | §2、§6、§8 | 权限读取error、限流error、余额不足、Key上限不足分别归因；资金依赖error不能fail-open。验证重试不产生第二份held/spent；余额从主库同事务获取。 |
| SEC-C3：各出口分开构造 | §2、§9.1、§10 | 同一秘密标记在上游错误/头/URL中出现时，普通错误、SSE错误、exception事件/status、日志、audit diff、metering evidence均无标记；客户只见受控码和request_id。 |
| SEC-C4：采集前最小化 | §9/9.1 | 长SSE和tool arguments不会被trace旁路积累；关闭正文采集时不先全量聚合再遮盖；不影响usage/TTFT与实际转发。 |
| SEC-C5：流终态与用量终态分离 | §7、§9 | 先内容后error、error后仍来chunk、最终usage后客户断开、只有累计下界后断开、HTTP200含error：均不补成功DONE、不重发；按证据可靠性唯一结算或核对，重复终止不重复计费。 |
| SEC-C6：发布/上游凭据/加密密钥分开 | §4、§6 T1.5、§10 | 并发发布期间每attempt只见一种endpoint/profile/credential版本组合；普通新凭据不改变旧request，紧急撤销拦下未获准attempt；重加密不改变上游秘密，旧密钥移除后的备份恢复另做演练。 |

## 固定源码与测试引用

[U1]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/register.go#L671-L680
[U2]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/internal/middleware/authentication.go#L51-L105
[U3]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_verify_key/handler.go#L54-L136
[U4]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_verify_key/handler.go#L138-L177
[U5]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/verifier.go#L75-L132
[U6]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/options.go#L24-L31
[U7]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_verify_key/keyspaces_test.go#L19-L78
[U8]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_verify_key/403_test.go#L34-L56
[U9]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/get.go#L90-L169
[U10]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/db/queries/key_find_for_verification.sql#L28-L52
[U11]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/caches/caches.go#L95-L114
[U12]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/get.go#L376-L420
[U13]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_delete_key/handler.go#L143-L200
[U14]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_update_credits/handler.go#L273-L285
[U15]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_delete_key/200_test.go#L103-L148
[U16]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/validation.go#L219-L255
[U17]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/internal/middleware/authentication.go#L118-L127
[U18]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/validation.go#L36-L68
[U19]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/run.go#L303-L321
[U20]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/usagelimiter/redis.go#L125-L149
[U21]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/usagelimiter/limit.go#L11-L40
[U22]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/integration/multi_node_usagelimiting/decrement/decrement_test.go#L152-L203
[U23]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/run.go#L168-L208
[U24]: https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_verify_key/audit_log_test.go#L33-L79
[A1]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/cmd/extproc/mainlib/main.go#L117-L123
[A2]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/server_test.go#L742-L792
[A3]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/semconv.go#L101-L132
[A4]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/openai/chat_completion_config_test.go#L408-L442
[A5]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/response_error_test.go#L20-L58
[A6]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/translator/openai_openai.go#L98-L131
[A7]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/span.go#L19-L54
[A8]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/processor_impl_test.go#L374-L427
[A9]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/processor_impl_test.go#L638-L678
[A10]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/backendauth/api_key.go#L17-L31
[A11]: https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/server.go#L84-L91

补充执行实现：

- [SWR stale立即返回与后台刷新](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/pkg/cache/cache.go#L657-L692)、[Remove仅操作store](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/pkg/cache/cache.go#L490-L496)。
- [counter成功扣减及DB fallback](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/usagelimiter/redis.go#L176-L226)、[replay失败处理](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/usagelimiter/redis.go#L288-L319)、[不足置0的SQL](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/pkg/db/queries/key_update_credits_decrement.sql#L1-L7)、[Drop=false的阻塞行为](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/pkg/buffer/buffer.go#L103-L127)。
- [verify telemetry/direct audit调用和目标元数据清除](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/svc/api/routes/v2_keys_verify_key/handler.go#L274-L341)。
- [正文debug日志分支](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/server.go#L297-L368)、[Hide默认值](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/config.go#L48-L62)。
- [正常响应隐藏和错误recorder分支](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/openai/chat_completion.go#L71-L91)、[异常消息实际拼接](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/tracing/openinference/response_error.go#L19-L38)、[extproc将上游错误送入span](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/processor_impl.go#L590-L619)。
- [translator实际记录chunk](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/translator/openai_openai.go#L197-L234)、[终止后chunk抑制](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/processor_impl.go#L553-L564)、[stream error提前返回及usage更新顺序](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/processor_impl.go#L622-L705)。
- [配置构建与auth handler](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/filterapi/runtime.go#L72-L86)、[LoadConfig现有测试](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/extproc/server_test.go#L48-L54)、[API Key handler现有测试](https://github.com/theagentrouter/agent-router/blob/931b020400a40c0e0f9a819a1c4b229ef945b8bd/internal/backendauth/api_key_test.go#L16-L41)。

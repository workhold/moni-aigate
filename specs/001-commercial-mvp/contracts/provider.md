# Provider 与 Routing 合同（r3 实施切片）

本切片由标准库实现，不依赖 Gin、数据库或第三方 provider SDK；不执行函数工具或隐式续写。财务与授权由调用者负责。

## Provider

`Profile` 固定 `version/model/input_token_upper_bound/context_limit/output_limit/default_output_limit/output_limit_field/supports_tools/supports_parallel_tool_calls/allowed_parameters/no_consumption_statuses/usage_semantics`。输入上界必须是供应商强制边界或经验证硬上界，不能传 tokenizer 估值。`usage_semantics` 为 `final`（重复最终量必须一致）或 `cumulative`（非递减累计快照，最终替换不相加）。

`ParseChat(raw []byte, Profile) (*Request,error)`：请求≤1MiB、严格字段白名单、文本消息、单候选、函数工具、受控输出上限；拒绝重复 JSON key、媒体、未知/收费扩展、歧义上限与非法工具。导出 `Model/Stream/IncludeUsage/MaxOutputTokens/InputUpperBound/ProfileVersion` 供准入和财务使用；`UpstreamJSON(model string) ([]byte,error)` 从私有规范值构造请求，固定模型与上限，流强制申请 usage。调用者不得修改导出元数据后期待改变实际请求。

`ValidateEndpointConfig(Endpoint) error` 仅校验URL/协议/端口/CIDR静态配置，不做DNS或联网；写配置入口调用它，实际dial仍校验所有解析地址。

`NewClient(ClientOptions) *Client`；`Client.Do(context.Context, Endpoint, *Request) (*Response,error)` 只发一次 HTTP 请求。调用者必须提供总 deadline；客户端阶段限制不能延长它。Endpoint.URL 是完整 Chat endpoint；NetworkPolicy 默认 HTTPS:443，HTTP/其他端口和私网 CIDR 必须显式允许。metadata/link-local/unspecified/multicast 永不允许；loopback 仅显式 CIDR（测试与受控本地部署）。每次 dial 解析并校验全部地址，再直接连接一个已验证 IP；HTTPS 校验证书和原 hostname；禁 redirect、环境代理、自动 POST 重放与 keep-alive。

`Response.DecodeJSON() ([]byte,error)` 校验非流完整响应及≤8MiB解压后大小；`ReadEvent() (*Event,error)` 读取完整 SSE event（≤1MiB），支持 LF/CRLF/CR、多data行与任意字节分片。`Close()` 幂等关闭真实上游 body。`Usage` 显式区分 `confirmed_final/provisional/missing/conflicting`，整数0有效；input/output/total完整、非负、无溢出且total=input+output，cached≤input/reasoning≤output。协议终态 `ProtocolOutcome`、`TransportEndReason`、`FinishReason` 与 Usage.status 独立；usage 不代表协议成功。

流必须有合法 finish_reason 和 `[DONE]`；裸EOF/残缺JSON/流内error失败。usage在DONE后才晋升confirmed_final，先到usage仍可遇到后续error；最终量冲突不自动收费。`ReadEvent` 永不将供应商error正文暴露在错误中。Event.Data 是验证后的正常chunk；usage-only单独分类，调用者按IncludeUsage控制发送并且最终只发送一次。DONE后不读取新生成。首事件60s、字节idle60s、有效进展60s；role-only/空chunk/注释/心跳不延长有效进展，有效内容/工具delta或合法finish能更新进展。

`Error` 仅公开通用Code/HTTPStatus/Class/Consumption/RetryAfter。`SafeToRetry(error)` 只接受传输证明未写HTTP字节，或已固定profile可信无消费拒绝；取消、普通5xx、写后断线/超时不能重试。Retry-After支持整数秒与HTTP-date；实际等待、cap、剩余额度由caller控制。provider从不把客户身份header或幂等Key转给上游。

## 003 原生协议扩展

`Profile.protocol`为空兼容Chat，其他为`openai_responses`/`anthropic_messages`。`Parse(protocol,raw,p)`严格匹配已发布模型协议；`Request.Protocol()`与`EndpointSuffix()`供共享网关管线使用，suffix不含渠道base URL已有的版本前缀。两种原生协议保持各自JSON envelope与SSE事件名，`Event.Type`为事件名，`Kind=done`只表示交由调用者持久最终量并结算，不允许提前转发原生成功终态。原生协议不使用Chat DONE。Messages最终用量累积/缓存拆分、认证与失败合同详见[003](../../003-native-protocols/spec.md)。

## Routing

`NewPlan([]Candidate,maxAttempts)` 固定候选副本并限制maxAttempts为1或2；同一channel不能重复。`Plan.Start(ctx,*Engine,AdmitFunc) (*Attempt,error)` 从尚未尝试、Enabled、健康/容量可用候选中，最高数值priority层内按正weight选取。AdmitFunc在网络前接收Candidate和Sequence，由caller持久化attempt并复查权限/状态；失败无网络派发。未选候选不消耗attempt。Plan不会执行网络或自动retry。

`Plan.CommitDownstream()` 禁止后续透明派发；`CanRetry(ctx,error)` 同时检查预算、提交状态、ctx和provider消费证据。Attempt必须由caller在真实执行资源退出后 `Finish(Outcome)` 一次，归还channel容量。结果 `Success/Failure/Neutral/Quarantine`；三次归因故障open30s，半开一令牌；中性结果释放令牌并保持half_open，成功closed，归因失败重新open。运行时健康不修改历史候选快照。quarantine持久化/恢复由平台数据库门禁负责，Engine仅保留运行时镜像。

## 必须执行的本地验证

真实HTTP/TCP fixture验证POST计数/redirect/早断/取消socket、HTTPS hostname与DNS实际dial、显式loopback及metadata拒绝；SSE逐字节分片/三类换行/多data/1MiB上限/非法或冲突usage/工具delta/缺终态；路由优先级权重、并发容量、两次统一额度、半开中性结果和健康不授予重放权。不得调用真实付费模型。

# 原生协议 SDK 零费用验证

本流程验证真实 Python SDK 能否解析经 moni-aigate `internal/provider` 校验、净化后的 Responses / Messages 文本 JSON 和完整 SSE 事件序列。SDK 使用 `get_final_response()` / `get_final_message()` 聚合流，再比较文本与最终用量。

开发依赖固定为 Python ≥ 3.10、`openai==3.41.0`、`anthropic==2.3.0`；网关运行时不依赖 Python 或这两个 SDK。首次安装依赖需要访问 PyPI。Go 依赖需已缓存，以下导出命令显式关闭下载和工具链自动获取。

从仓库根目录执行，`python3` 须指向满足版本要求的解释器：

```sh
python3 -m venv .local/venv-native-sdk
.local/venv-native-sdk/bin/python -m pip install -r scripts/requirements-native-sdk.txt
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go run ./scripts/export-native-sdk-fixtures -out .local/native-sdk-fixtures
.local/venv-native-sdk/bin/python scripts/verify-native-sdk.py --fixtures .local/native-sdk-fixtures
```

预期输出：

```text
PASS native_sdk_fixtures
PASS openai=3.41.0 responses_stream responses_json
PASS anthropic=2.3.0 messages_stream messages_json
```

导出器在临时 `127.0.0.1` HTTP 服务上提供固定合成上游数据，然后调用真实 `provider.Parse`、`Client.Do`、`DecodeJSON` / `ReadEvent`。只有协议完成且用量已确认，才导出四个文件：

```text
openai_responses.json
openai_responses.sse
anthropic_messages.json
anthropic_messages.sse
```

其中 Responses 包含 created、in_progress、output_item、content_part、text delta/done、completed 的完整基本顺序、`phase` 和空 `logprobs`；Messages 包含空 citations、安全可选 metadata、起始输入/缓存计数与最终累计输出。文件由当前 provider 重新生成，无需 Go overlay，也无需数据库、平台账号或供应商密钥。

Python 脚本仅接受 fixture 目录，自动启动随机端口的 `127.0.0.1` HTTP 服务，使用固定假密钥、禁用 SDK 重试和环境代理。运行时通过 Python audit hook 拒绝其他连接、DNS 查询和 UDP 发送；脚本不接受上游 URL。SDK、HTTP 请求和异常正文均不写日志，只输出 PASS、SDK 版本或固定 FAIL 代码。替换依赖版本、缺文件、解析失败或数据不一致均返回非零退出码。

这项验证覆盖 SDK 的文本流聚合与非流解析；资金结算顺序、鉴权、工具边界和取消仍由 provider/server 测试验证。它不代表真实供应商验收或性能对标。2026-10-30 已实际运行上述两个固定版本 SDK，未调用任何付费 API。

## 实际核对的官方资料

2026-10-30 已实际读取下列页面正文，按当时的公开协议校准 fixture 与边界：

- [OpenAI Responses 流事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)：`response.completed` 的说明为 “Emitted when the model response is complete.”；text delta/done 的 schema 包含 `logprobs`，普通文本的官方例子为 `[]`。
- [OpenAI Responses 创建接口](https://developers.openai.com/api/reference/resources/responses/methods/create)：assistant message 的 `phase` 为 `commentary` / `final_answer` / null；原文要求 “preserve and resend phase on all assistant messages”。`max_output_tokens` 约束生成输出并包含 reasoning，不能用它推出一个固定的较小输入硬界。
- [OpenAI 上下文说明](https://developers.openai.com/api/docs/guides/conversation-state)：原文 “Tokens generated in excess of the context window limit may be truncated in API responses.”，因此上下文窗口和独立输出上限需分别核实。
- [Claude Messages 创建接口](https://platform.claude.com/docs/en/api/messages/create)：TextBlock 的 `citations` 为 array 或 null；新增 usage 的 `output_tokens_details` 可为 null，输出 token 总数仍包含 thinking；Messages stop reason 包含 `model_context_window_exceeded`。
- [Claude Messages 流协议](https://platform.claude.com/docs/en/api/messages-streaming)：原文 “The token counts shown in the usage field of the message_delta event are cumulative.”；流由 message_start、content block、message_delta、message_stop 组成。
- [Claude 上下文窗口](https://platform.claude.com/docs/en/build-with-claude/context-windows)：原文 “On Claude 4.5 models and newer, if input tokens plus `max_tokens` exceeds the context window size, the API accepts the request.”；之后触及窗口时以 `model_context_window_exceeded` 结束，网关将其归一化为 `length`，原生 stop reason 保留。

输入硬界与独立输出界的资金合同由 [004 规格](../../specs/004-conservative-bounds/spec.md) 及 HLD/LLD 定义。供应商认证仍须确认账单输入维度的真实硬界；此 SDK 测试不替代认证。

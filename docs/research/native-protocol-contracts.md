# 原生协议合同核对

2026-10-30，作为[003规格](../../specs/003-native-protocols/spec.md)的协议来源。独立编写请求验证、响应净化和计量代码，不拷贝New API实现。

本轮先搜索官方域名 `site:developers.openai.com responses streaming events`，随后实际获取以下完整页面，不只依据搜索摘要：

- [OpenAI Streaming responses](https://developers.openai.com/api/docs/guides/streaming-responses)：`response.output_text.delta`、`response.completed`及error事件，SSE按语义事件消费。
- [OpenAI Create a response](https://developers.openai.com/api/reference/resources/responses/methods/create)：原生response envelope、function call/output、output token上限、store和后台/状态引用，以及usage与流事件示例。
- [Anthropic Streaming Messages](https://platform.claude.com/docs/en/api/messages-streaming)：message_start → content blocks → message_delta → message_stop；message_delta的usage为累计值，不能相加各帧output_tokens；error独立终止。

原生协议支持面主动收窄为可确定计费的无状态文本与基础函数工具。支持的安全空字段/固定枚举以fixture校准官方schema；媒体、托管工具、非空引用、thinking和未知收费维度继续拒绝。Responses和Messages接口存在不代表Codex/Claude Code等客户端全功能兼容，尚未运行这类产品的完整工作流。

实现入口：[provider](../../internal/provider/native_request.go)、[响应](../../internal/provider/native_response.go)、[usage](../../internal/provider/native_usage.go)、[真实HTTP/SSE fixture](../../internal/provider/native_test.go)。主服务用[纵向PG测试](../../internal/server/native_protocols_integration_test.go)验证授权、派发、最终量与结算后终态。

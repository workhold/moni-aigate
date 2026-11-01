# 003：原生文本协议与客户联调

状态：2026-10-30，本地实现与全量回归通过，正式供应商及生产发布验收待完成。本切片把 001 原先后置的 Responses / Claude Messages 前移；其他商业与资金规则继续遵循 001。实现采用独立代码，参考公开协议，不移植竞品业务实现。

## 范围与成功条件

同一个商业网关提供 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`，客户可通过 Playground 或标准 HTTP 客户端完成文本与基础函数工具调用。每个模型只允许一个已发布的协议，不能借端点切换扩权。三种协议共用授权、限流、不可变路由/凭据/价格、硬预占、派发证据、最终量结算、取消与恢复。

本切片不包含协议互转、图片/音频、托管工具、远程工具执行、服务端会话、后台任务、WebSocket、扩展 thinking、未知收费维度。函数工具只传递定义、参数与结果，不自动执行或续写。Responses 明确拒绝 `previous_response_id`、`store:true`、`background` 参数；客户端自行携带无状态对话上下文。

## 必须满足的合同

| ID | 合同 |
|---|---|
| NP-01 | 模型 profile 增加 `protocol`，取值 `openai_chat` / `openai_responses` / `anthropic_messages`；空值兼容旧 Chat。模型创建与授权目录返回协议。参数白名单和输出硬上限按协议验证，失败不得预占或发送。 |
| NP-02 | 供应商 URL 为渠道 base URL 加协议 suffix；上游模型由发布绑定替换。Responses 使用供应商 Bearer，Messages 使用供应商 `x-api-key` 和固定 `anthropic-version: 2023-06-01`。客户凭据不得透传供应商。 |
| NP-03 | 客户 Messages 可使用 Bearer 或 `x-api-key`，同时提供两类、同名多 header、空凭据均拒绝。其他端点仍使用 Bearer。平台会话不代替收费 API Key。 |
| NP-04 | Responses 文本、基础 function call/output；Messages 文本、tool_use/tool_result；非流原生 envelope 与 SSE 事件名保持协议含义，模型名回写公开模型。所有上游数据先按白名单验证/净化，不透传任意错误或秘密。 |
| NP-05 | Responses `response.completed` / 可计量的 `response.incomplete`，Messages `message_stop` 是终态。成功终态必须在最终量持久化且结算成功后发送。原生协议不发送 Chat `[DONE]` 或伪造 Chat usage chunk。裸 EOF、error、缺失/冲突最终量不能伪装成功。 |
| NP-06 | Responses input/output/total 必须自洽，cached/reasoning 是输入/输出子集。Messages 计费输入 = base input + cache read + cache creation，cached 仅标记 read 子集，按同一输入单价收费，不重复计量、不宣称缓存折扣。临时量不当最终量，缺失/null 与显式 0 区分。 |
| NP-07 | 统一 FinishReason 为 stop/length/tool_calls/content_filter；有内容后可信量按001交付矩阵结算，无可信最终量进入待核对，不按预估量扣款。安全重试最多两次，未知消费不重放；下游任意事件提交后禁止重放。 |
| NP-08 | 主请求与平台 probe 选相应协议。1MiB 请求、事件/响应上限、总deadline、有效进展时限、取消关闭/等待与 SSRF 安全边界继续生效。probe 独立平台成本，不扣客户。 |
| NP-09 | Playground 只列获授权模型，API Key 仅保留页面内存，密码框输入，不写 URL/localStorage/日志。收费请求使用 `credentials:omit`，显式按钮触发且可取消，不自动重试或执行工具；显示 request_id、usage 和错误。接入示例使用环境变量占位。 |

## 公开失败形状

Chat / Responses HTTP 错误沿用 `{error:{message,type,param,code}}`；Messages 为 `{type:"error",error:{type,message},request_id}`。所有协议响应带 `X-Request-ID`。Messages SSE 错误使用 `event: error` 与原生 error envelope；Responses SSE 错误使用 `event: error`，保留脱敏 code 与 request_id。任何错误后不输出成功终态。

## MVP 出口

本切片通过验收后，MVP 包含邀请开户、人工双人核款、三协议文本/工具、授权/预算/账单/退款/审计、渠道探测与本地联调。在线支付、开放注册、Gemini、媒体与订阅继续按路线图后置。容器与真实供应商验收、性能对标分别记录，不把合成测试写成生产认证；性能门禁仍遵循 002。

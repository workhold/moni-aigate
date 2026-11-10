# 官方与第三方模型渠道接入计划

2026-11-01。用户确认的目标是 OpenAI/Codex、Claude、DeepSeek、Gemini、GLM 等官方模型，并可接入硅基流动等第三方平台。本页固定接入与验收范围，不宣布任何正式渠道已经认证；当前实现的六种协议及本地验收见[统一架构](../architecture/README.md)，底层合同见[Provider](../../specs/001-commercial-mvp/contracts/provider.md)。

| 目标渠道 | 首批协议接缝 | 逐渠道确认事项 | 当前状态 |
| --- | --- | --- | --- |
| OpenAI / Codex相关模型 | OpenAI Responses；适用模型另行验证Chat | API可用模型ID与账号权限、reasoning/cache计量、输出硬界、终态与取消；Codex完整agent会话另属有状态能力 | 已有协议实现；当前可售profile显式拒绝未冻结的reasoning输出，usage子集不等于reasoning事件支持；官方code generation指南已读，具体可售profile及真实计费待验 |
| Anthropic Claude | Anthropic Messages | 模型版本、max_tokens及thinking语义、缓存读写与额外收费维度、SSE最终usage和停止原因 | 已有受限Messages实现；正式模型profile待验 |
| DeepSeek | 先评估OpenAI-compatible Chat | 当前官方请求/流/工具、reasoning与缓存包含关系、输入输出强制边界、实际usage与扣费 | 有兼容协议接缝；不预设全部字段兼容 |
| Google Gemini | Gemini原生generateContent / streamGenerateContent | 模型版本、候选与thinking用量、聚合硬界、原生终态；工具/媒体另片 | 已有无状态纯文本实现；真实profile待验 |
| 智谱 GLM | 先评估OpenAI-compatible Chat | 当前官方endpoint/鉴权、工具/推理扩展、流式用量、缓存与硬界 | 有兼容协议接缝；具体兼容范围待验 |
| 硅基流动及其他第三方平台 | 按该平台实际端点选择兼容profile | 平台自己的模型ID/版本、用量及采购报价、限流、无消费证据；同名模型不能继承官方渠道认证 | 可建独立渠道/采购账户；具体模型逐项认证 |

一个对外模型可以绑定不同渠道的明确上游模型名。每个绑定单独关联不可变能力profile、采购报价、生效版本和采购账户；相同对外名称不合并供应商身份或收费语义。运行时先做权限、协议能力、地域、状态、采购预算和报价上限过滤，再在最高优先级内按既有成本策略调度；不跨协议静默转换，也不因渠道返回错误就重放可能已消费的请求。[018成本选路](../../specs/018-cost-aware-routing/spec.md)、[019共享采购预算](../../specs/019-procurement-accounts/contract.md)已提供这些接缝。

每个具体profile按同一顺序交付：官方合同取证 → 精确请求/响应/计量与失败合同 → 本地协议和商业fixture → 隔离凭据下的真实请求/取消/断流及供应商用量对账 → 冻结可售配置。缓存、reasoning、工具、媒体等只要存在额外非零收费维度，就须进入完整客户/采购计量合同；不能以“兼容OpenAI”把未知费用算成0。所有报价使用平台已登记的CNY采购合同，网页外币标价不自动汇率换算。

本次通过Chrome实际读取[OpenAI官方Code generation指南](https://developers.openai.com/api/docs/guides/code-generation)，该页将Codex编码agent与在应用中通过Responses API使用编码模型分别说明。因此接入目标以账号实际可用的官方API模型为准；完整Codex agent会话、执行环境与工具生命周期需要单独合同，不能从现有文本Responses转发推导已支持。本文不固化易变的“最新模型”或默认价格，不用文档读取代替真实账号/费用认证。

2026-11-10 用户授权使用本机 CC Switch 中名为 `baiyang` 的供应商进行内测资格探测。该端点通过模型列表发现 137 个模型，并用低成本 `gpt-4o-mini` 完成一次非流式 OpenAI-compatible Chat 请求，确认终态和 final usage。该证据只冻结 Chat 单模型 qualification probe，不扩大为完整供应商、媒体、生产容量或正式成本认证。后续每个 profile 的 spec、证据、能力状态和源码改动一起进入统一架构内容清单。

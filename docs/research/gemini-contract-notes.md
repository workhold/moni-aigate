# Gemini 文本协议合同校准

2026-10-31，独立实现的协议资料核查，不复制供应商/竞品实现代码。

固定官方`googleapis/googleapis`提交`bc7e3baa28fbb223fa93782e130260fab8205bfc`：

- [generative_service.proto](https://github.com/googleapis/googleapis/blob/bc7e3baa28fbb223fa93782e130260fab8205bfc/google/ai/generativelanguage/v1beta/generative_service.proto)：generateContent/streamGenerateContent的model路径、contents/system_instruction/generation_config、候选终态和UsageMetadata。
- [content.proto](https://github.com/googleapis/googleapis/blob/bc7e3baa28fbb223fa93782e130260fab8205bfc/google/ai/generativelanguage/v1beta/content.proto)：Content/Part、thought、签名及媒体/工具的不同数据类型。

通过GitHub commits与contents API读取；raw.githubusercontent首次请求超时后改用contents API成功。读取为源码schema定向研究，不是实际供应商运行认证。

UsageMetadata将prompt、candidates、thoughts分别报告，cached为prompt中的缓存子量；toolUsePrompt与各modality明细不能默默忽略。010采用output=candidates+thoughts、total=input+output，并检查TEXT-only明细与对应总量一致；未知收费维度进入待核对。公开仅输出规范五字段。schema本身不能证明某个模型的maxOutputTokens对所有收费输出的聚合保证，该点保留为正式供应商认证要求和人工上线门槛；010未实现程序化供应商认证开关，创建profile或probe成功不等于完成认证。

Gemini SSE无Chat DONE。010将合法finish与完整EOF共同作为终态证据，等待尾usage后再结算；finish帧中的文本只发一次，成功finish只在收费后交付。严格保留framer对残缺事件的拒绝，以及消费未知不自动重放的既有政策。

实现范围、上界与错误/计量取舍见[010](../../specs/010-gemini-native/spec.md)；本切片只交付无状态文本，不宣称工具/多模态或官方SDK完整认证。

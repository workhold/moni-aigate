# 010：Gemini 原生文本协议

2026-10-31。沿能力路线图NA-02/A2继续，功能优先，002/008性能专项后移。状态：限定范围本地验收通过，见[验收执行](acceptance.md)与[验证记录](../../docs/verification/gemini-native.md)；正式供应商认证独立记录。

## 用户出口

平台可创建`gemini_generate_content`模型、绑定不可变渠道/模型映射、发布价格与授权。客户通过Gemini原生路径或Playground发送无状态纯文本JSON/SSE请求，获取公开模型标识、可信用量及可关联的账单。沿用原鉴权、硬预占、路由/安全重试、恢复和审计，不转换成Chat、不建立另一套计费服务。

## 固定协议合同

1. `POST /v1beta/models/<公开model>:generateContent`（无query）与`:streamGenerateContent?alt=sse`（只允许单个alt=sse）。客户端只使用Bearer，拒绝重复/混合凭据、x-goog-api-key与query key。两路径进入原300s总deadline、实例Guard/恢复门禁；请求体不含model/stream，保持Gemini原生形状。
2. 公开及上游模型均限1..100个ASCII字符、首位字母或数字，其余字母/数字/`.`/`_`/`-`，禁止`..`、斜杠、百分号、冒号和编码路径注入。平台渠道base URL应含`/v1beta`；模型映射仅替换路径资源名。Endpoint仍禁止任意query，Client通过原网络校验后仅为Gemini流内部加入固定`alt=sse`；供应商凭据只发`x-goog-api-key`，不放URL或转发客户header。
3. body只允许`contents`、`systemInstruction`、`generationConfig`，拒绝重复JSON键/未知字段。contents为1..128条user/model消息（省略role视为user），parts为1..128个非空text；systemInstruction为text parts，可省略role或role=user。禁止媒体、工具/函数、cachedContent、thinking配置、有状态会话、显式缓存与供应商额外扩展。
4. generationConfig可省略；基础字段`maxOutputTokens`为1..profile最大值，缺省取默认值；`candidateCount`仅1，上游强制1。可选temperature0..2、topP0..1、topK整数1..1000、stopSequences最多4个非空字符串，受profile可选参数白名单约束。上游强制实际maxOutputTokens，与资金快照的OutputLimit相同；不注入顶层model/stream。
5. profile使用正input/output/default、output_limit_field=`maxOutputTokens`（Provider可接受空，由Catalog发布时补齐），tools/parallel=false；usage_semantics为空或cumulative，空按cumulative。输入硬界覆盖全contents/systemInstruction/协议开销；输出硬界必须覆盖**可见candidate+thinking的聚合收费量**。实际供应商保证须正式验证，不能把只约束可见内容的上限冒充收费硬界。售价仍用原输入/输出量纲，Reserve/账本公式不变。

## 响应与计量

6. 结果白名单为candidates、usageMetadata、modelVersion（重写为公开model）及受控responseId。限定唯一candidate/index0、role=model、text parts；thought=true文本不交付，签名/调试/任意metadata不透传。只接受STOP/MAX_TOKENS结束；prompt block、其他finish、函数/媒体结果按明确协议失败处理且不自动重放，不把未支持行为伪装成正常文本。
7. usageMetadata必需promptTokenCount=P、candidatesTokenCount=C、totalTokenCount=T，均非负整数；thoughtsTokenCount=H与cachedContentTokenCount=K可省略视0。规范input=P/output=C+H/total=T/reasoning=H/cached=K，T须=P+C+H，K≤P，运算不得溢出。cache是输入子集，不重复加量；thinking按输出价收费，不能漏掉。toolUsePromptTokenCount只接纳0；已知token details可验证TEXT-only与对应总数相同，未知字段/非TEXT/冲突量拒绝确认。公开usage只输出上述5种计数。
8. 结果结构完成但量缺失/冲突（含根usageMetadata或内部计数重复）时，Provider记录completed、UsageMissing/Conflicting且不抛结构错误，沿Finalization→Settle进入billing_pending。JSON不交付结果，预占保留；SSE可已交付文本，但不发送成功终态。候选出现groundingMetadata/urlContextMetadata等未支持托管工具收费证据时，也保持冲突量待核对，不能净化后照常确认。不能将未知量变成failed/no-output释放。实际金额超过预占R时沿原合同封顶R、豁免差额并隔离profile；本轮不新增“任意单独token越界而金额≤R也隔离”的另一套Settle规则。
9. SSE为原生`data: JSON`，无DONE。finishReason与最后一段文本同帧时，先发剥离finish/usage的内容帧，私有保留无已发内容的终态。继续读尾usage，只有**已有合法finish且完整帧边界EOF**才完成协议；裸EOF、残缺尾帧、DONE、流内error、finish后新增文本或冲突finish均失败。finish帧或其后须有完整最终usage，早到完整快照只属临时量，不能代替最终量。重复最终累计快照不相加，原始各计数下降或矛盾sticky-conflicting。candidate省略proto3默认index时视0并规范输出0；usage-only尾帧可省略candidates或为空。成功终态帧在最终证据持久化并Settle后发送；保持原1MiB事件/8MiB非流上限、进展/取消/总deadline，不全局放宽framer。
10. 幂等HMAC包含协议、路径公开model、流/非流action及原始body，避免相同body跨模型/动作被误认同一次。公开错误仅包含受控gateway code/message及HTTP status，不透传供应商原文；已开始流使用event:error。最多两次安全尝试，发送后消费未知不重放。

## 一起交付与验收

平台模型创建/绑定校验、目录、独立probe和Playground/curl同步支持；Gemini工具开关隐藏且提交固定false，生成输出仍为正。Playground以同一HTTP构造生成真实native body与curl，不把model/stream草稿发给上游；取消不重试，密钥只留内存。

Provider fixture/本机HTTP覆盖严格请求、路径/认证、JSON/SSE、尾usage/合法EOF、重复/冲突量、thinking/cache、不泄露元数据、错误/取消/资源上限。真实PG+HTTP覆盖R140、P10/C3/H2/T15/K3→输出5/收费20/余额980/held0、终态不能早于结算、未知量pending、先拒绝/幂等/安全重试/probe不扣客户钱包。前端契约、strict build、全Go race/vet/build和预览资源匹配独立记录。

本轮不做性能压测、付费供应商调用或真实客户资金变更；Gemini工具/多模态、SDK完整认证、凭据池/模型发现、注册/充值/套餐与扩展能力继续按路线图，不把文本切片标作Gemini全功能或生产认证。

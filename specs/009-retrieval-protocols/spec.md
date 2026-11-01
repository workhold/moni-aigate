# 009：Embeddings 与 Rerank 核心接入

2026-10-31。开发者明确要求先完善能力，再推进性能优化。本切片对应New API路线图NA-03；008性能方案后移。状态：限定范围实现与本地验收通过，正式供应商认证未执行，见[验收](acceptance.md)。

## 用户出口

平台可发布OpenAI Embeddings或Jina格式Rerank模型、绑定原不可变渠道/售价、授权租户与Key。客户可用标准HTTP调用、在Playground分别输入待编码文本或query/documents，收到结果后查看request_id、可信用量与实际账单。正式供应商认证单独记录，不以mock宣称所有供应商兼容。

## 协议合同

1. 新增`openai_embeddings`→`POST /v1/embeddings`与`jina_rerank`→`POST /v1/rerank`，上游后缀分别`/embeddings`、`/rerank`。两者使用已有Bearer Key、SSRF/超时/取消、预算、路由/健康与最多两次安全尝试；不允许stream、工具或生成输出字段，不转换成Chat。
2. Embeddings支持非空文本、文本数组、非负整数token数组或二维token数组（批次≤128，token值≤2^31−1），禁止混合/null/空输入。可选`encoding_format=float|base64`、`dimensions=1..8192`；默认float。非空user字符串≤256作为可选透传字段，禁止未知字段/重复JSON键。上游只替换模型，不注入max_tokens或stream。
3. Embeddings响应只返回`object=list`、公开模型、`data[{object=embedding,index,embedding}]`与可信usage。index须唯一覆盖请求数量；向量为非空有限数字数组或合法base64 little-endian float32，每向量≤8192维，维度一致且满足显式dimensions。响应≤8MiB；错误体、任意调试字段不转发。缺失/冲突usage不能成功结算与交付结果。
4. Rerank固定Jina token用量格式：请求`model/query/documents:string[]`，非空query与1..128非空document，可选`top_n=1..N`、`return_documents:boolean`，默认返回全部排名且不返回文档；不支持Cohere search_units计价、对象文档、媒体或其他供应商扩展。响应`results[{index,relevance_score,document?}]`，index在原documents范围且不重复，数量符合top_n，score有限且0..1，按非升序排名；仅请求return_documents时从原请求取相应文档，绝不转发供应商任意文档/元数据。可返回受控id、公开model及可信usage。
5. 输入计量：Embeddings需要`usage.prompt_tokens`与`total_tokens`均为非负整数且相等；Rerank需要`usage.total_tokens`，可选prompt_tokens必须相等。只允许已定义token字段，若completion_tokens出现必须为0；未知收费维度拒绝确认。不估算、不把缺失量写成0。

## 输入独立计费

6. 两种profile的`output_limit=default_output_limit=0`，`output_limit_field`为空，tools/parallel关闭；input硬上界为**整个请求**的供应商可保证聚合计费token上界，且≤context_limit。context_limit在此指整个请求的输入计费上界容量，不是每个文本/文档的局部界。不得将单条8192误用为128条合计上界。平台须在发布前核实实际供应商合同；未认证模型不称收费就绪。
7. 原Reserve仅对这两个明确协议允许OutputLimit=0，并复核profile同样为0，输入仍为正且符合已发布独立硬界；生成协议继续要求正输出，不能通过零值绕过硬预占。售价output_rate必须为0，硬预占R=ceil(input_upper_bound×input_rate/10^6)，原事务/授权/父锁/幂等全部保留。
8. 可信最终量统一为input_tokens=T/output_tokens=0/total_tokens=T，按原RecordFinalization→Settle收费，超硬界仍封顶预占并隔离profile。缺失/冲突量返回billing_pending并留待核对，无成功结果；错误、取消、崩溃及未知消费继续使用原矩阵，不自动重放。
9. 无数据库迁移。profile版本、价格、attempt/账本/审计/outbox仍不可变。配置读写、客户目录、平台probe与Playground一起支持，不只新增路由。

## 验收

- Provider请求/响应契约覆盖文本与token批次、float/base64、维度/index/score边界、JSON/响应上限、未知量/错误净化、取消与单次POST。
- 真实PG/HTTP验证两协议R100/收费10/释放90、output=0，授权/错协议/stream先拒绝、无输出售价拒绝、缺失量不交付、幂等与资金/资源回收、平台probe不动客户钱包。
- 前端请求构造/结果提取回归、strict TS/Vite与全Go race/vet/build；使用本地fixture，不调用付费供应商。
- 更新HLD/LLD、路线图和实际边界。Gemini/渠道池/自助注册/支付/订阅/媒体/HA仍按依赖继续，不能把本切片标成New API全功能完成。性能值保留为007历史记录，新协议尚未压测。

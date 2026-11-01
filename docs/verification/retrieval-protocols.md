# 009 检索协议与输入计费验证

2026-10-31，工作树无提交SHA；[spec](../../specs/009-retrieval-protocols/spec.md)、[plan](../../specs/009-retrieval-protocols/plan.md)、[验收清单](../../specs/009-retrieval-protocols/acceptance.md)。状态：限定范围本地验收通过，最终编译与预览就绪检查通过。本切片优先补功能，008性能方案后移。

## 实现边界

- `POST /v1/embeddings`：OpenAI格式文本/文本批次/token数组/二维token数组，≤128项，float/base64 LE float32，≤8192维。可选dimensions/user，禁止stream/工具/生成字段。按请求数量/维度/编码验证响应并净化。
- `POST /v1/rerank`：Jina token计量格式，字符串query/documents≤128项、top_n/return_documents；排名索引、数量与0..1非升序分数校验，文档从原请求按索引重建。Cohere search_units、对象/媒体文档与其他扩展未实现。
- 仅这两个profile允许零输出。Catalog与Reserve锁内分别验证输出/价格；input硬界覆盖整个请求聚合量，不是单条文本context。生成协议继续要求正输出。无迁移，不修改已有历史profile与价格。
- 可信最终量统一input=T/output=0/total=T，保留原硬预占、最终证据与唯一结算。缺失、冲突、重复usage或未知收费维度：协议结构完成但用量不确认，HTTP503 billing_pending，不交付向量/排名，预占待核对，不重放。真正结构错误按原无交付失败政策释放，由平台承担该失败风险。
- 模型创建/定价、目录、平台probe与Playground/curl一起支持。probe只有协议和用量都可信才ok，独立审计/供应商成本，不扣客户钱包；不据probe成功声称正式供应商认证。

## 直接验证

| 层 | 直接证据 | 主要判据 |
|---|---|---|
| Provider | [retrieval_test.go](../../internal/provider/retrieval_test.go) | 严格JSON/字段/输入边界、聚合token限制、float/base64/维度/index、原文档、用量不确定性、真实本地HTTP单POST/取消/响应上限 |
| 资金PG | [retrieval_integration_test.go](../../internal/billing/retrieval_integration_test.go) | 两协议R100→charge10/返90、并发Key预算、生成零输出拒绝、畸形配置及非零output价格失败完全回滚、150超界封顶100+block、幂等结算 |
| HTTP+PG | [retrieval_integration_test.go](../../internal/server/retrieval_integration_test.go) | 两协议真实调用、batch/base64、净化/模型映射、R100/可信10/charge10/余额990/held0；未知量503/held100/0结算、鉴权/错端点/stream:false先拒绝、重复请求无追加POST/扣款、probe不动客户资金、结构错误不泄漏/不重试 |
| 控制台 | [retrieval.test.mjs](../../web/tests/retrieval.test.mjs) | 7项新增契约：精确请求/边界、零输出发布、有限结果预览、用量门禁、单POST Bearer且无cookies、取消保留request_id |
| 切换回归 | [protocol-switching.test.mjs](../../web/tests/protocol-switching.test.mjs) | 3项：检索切回生成须重填输出售价；切模型清旧结果/用量/ID/计时，保留密钥/输入；同模型刷新保留结果 |

测试只用专用`moni_test`的随机独立schema与本机HTTP mock。未调用付费服务、修改评审账号/MFA或真实客户资金。

执行命令：

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
go test -race ./internal/provider ./internal/billing ./internal/server -run 'Retrieval|Embedding|Rerank|InputOnly' -count=1 -timeout 180s
npm --prefix web test
npm --prefix web run build
go test -race ./... -count=1 -timeout 240s
go vet ./...
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
```

上述专项通过：provider2.035s、billing5.416s、server5.262s。全Go race通过：billing26.331s、identity43.323s、operations5.310s、provider4.272s、routing2.606s、security3.657s、server92.494s、telemetry3.563s，其他含无测试包正常编译；手动容量仍按原开关跳过，未做负载测试。这些数字是包测试执行时间，不是网关响应延迟。前端最终22项测试与strict TypeScript/Vite构建、全库vet及使用最终dist的嵌入编译全部通过。

初次专项中Provider取消fixture未读完POST请求体，导致HTTP/1背景断连探测未启动、测试服务Close等待。修正为先排空并关闭请求体、等待真实socket取消signal，并给失败清理备用release通道；保留2s有界取消断言。修复只涉及测试；之后专项和全量均重新通过，没有删除取消覆盖。

最终程序已替换58123旧007预览：`GET /health/ready`返回200及`{"recovery_mode":false,"status":"ready"}`，嵌入首页字节与最终`web/dist/index.html`相同，SHA256为`a1a4979dc30d179f5d307c8c34867996791a33254ea57fc05a43f71f8b25bac0`。浏览器访问`/playground`正常显示登录页及返回地址；未登录评审账号、变更其MFA或执行付费请求。本次新表单/联调行为通过代码复核、前端契约和后端真实HTTP+PG验证，不将其声称为已执行评审账号的浏览器端到端操作。

## 仍需完成

正式供应商的整请求聚合计费硬界、usage/错误/无消费证据与实际SDK认证单独进行，不能拿本地fixture当正式认证。新协议未压测；007历史性能、50ms/目标资源/竞品性能门禁保持原状态。未执行容器部署，不改变001剩余发布门禁。Gemini、凭据池/发现、注册/支付/订阅与扩展平台继续按[路线图](../design/new-api-roadmap.md)实施，009不是New API全功能完成。

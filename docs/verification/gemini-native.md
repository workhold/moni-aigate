# 010 Gemini 原生文本验证

2026-10-31，工作树无提交SHA；[spec](../../specs/010-gemini-native/spec.md)、[plan](../../specs/010-gemini-native/plan.md)、[验收清单](../../specs/010-gemini-native/acceptance.md)。状态：限定范围本地验收通过，最终嵌入编译与预览就绪检查通过。本切片优先补功能，002/008性能专项继续后移。

## 实现边界

- `POST /v1beta/models/<公开model>:generateContent`与`:streamGenerateContent?alt=sse`，原生无状态text-only请求；contents/systemInstruction、单candidate及受profile约束的generationConfig。公开及上游模型仅接受安全单段名称，不支持Vertex资源路径。
- 客户只使用唯一Bearer，拒绝混合/重复凭据、原生API Key header与URL key；上游只使用渠道的`x-goog-api-key`。模型来自URL与不可变映射；沿用网络出口校验、300s总deadline、Guard/恢复门禁、授权、硬预占和安全重试。
- input=prompt，output=candidates+thoughts，total=input+output；cached是input子集，不重复计量，不提供缓存折扣。隐藏thought正文、签名及任意metadata，公开模型名替换供应商标识。
- SSE无DONE：先交付文本，保留finish，继续读尾部usage直到合法完整EOF；仅finish帧或之后的完整量可作最终证据。成功终态在最终证据与结算提交后发送，finish同帧的文本不重复。
- 量缺失、重复、回退、冲突或未知收费维度、托管工具收费证据均不能正常确认收费。结构完成但量不可信时进入billing_pending并保留预占；非流不交付正文，流不发成功终态。取消与消费未知不自动重放。超过预占的实际金额沿原规则封顶、豁免和隔离profile。
- 平台模型创建/发布、目录、独立probe及六协议Playground/curl一起支持。probe有独立成本和审计，不动客户资金。不新增数据库迁移，不修改已有profile/价格历史。

## 直接验证

| 层 | 直接证据 | 主要判据 |
|---|---|---|
| Provider | [gemini_test.go](../../internal/provider/gemini_test.go) | 严格请求/profile/路径、JSON/SSE、尾量与完整EOF、thinking/cache、重复/冲突/未知量、托管工具、原始计数回退、真实本地HTTP单POST/认证/取消与响应上限 |
| 资金PG | [gemini_integration_test.go](../../internal/billing/gemini_integration_test.go) | R140→可信input10/output5（含thinking2）→charge20/释放120/余额980；幂等；零界先拒绝；实际170超过R140→cap140/waive30/block与再次准入拒绝 |
| HTTP+PG | [gemini_integration_test.go](../../internal/server/gemini_integration_test.go) | JSON/SSE模型映射/净化/计量与许可归零；finish同帧文本只发一次；尾量前无成功终态/结算，看到终态时结算已提交；未知量保留140/0收费；鉴权/路径/错端点/字段先拒绝；跨model/action幂等冲突；明确无消费429安全切换；真实socket取消；probe、发布与300s/ready门禁 |
| 控制台 | [gemini.test.mjs](../../web/tests/gemini.test.mjs) | 8项新增契约：native URL/body/curl、工具关闭、thinking/cache、最终量门禁、split UTF8 SSE、单POST Bearer、错误/取消不重放及模型切换清理 |

所有数据库测试使用专用`moni_test`的随机独立schema，供应商仅本机HTTP mock；未调用付费模型、修改评审账号/MFA或真实客户资金。第二代理完成静态计量/结算复核，未发现新的阻断；静态复核不代替下列执行结果。

执行命令：

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
go test -race ./internal/provider ./internal/billing ./internal/server -run Gemini -count=1 -timeout 180s
npm --prefix web test
npm --prefix web run build
go test -race ./... -count=1 -timeout 240s
go vet ./...
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
```

专项通过：provider1.586s、billing1.989s、server3.675s。前端30项测试、strict TypeScript/Vite构建通过。首次专项中server新增第二模型的fixture使用不存在的`created_by`，价格外键检查正确拒绝；改用该隔离schema内的真实finance_operator后重新通过。未放宽生产外键或删除幂等覆盖。

全Go race通过：billing26.922s、config1.917s、identity41.369s、operations5.316s、provider4.726s、routing2.380s、security3.413s、server93.066s、telemetry3.798s，其他无测试包正常编译；手动容量仍按原开关跳过，未做负载测试。全库vet与使用最终dist的嵌入build通过。上述秒数是包测试执行时间，不是网关响应延迟。

最终010程序已替换58123旧009预览：`GET /health/ready`返回200及`{"recovery_mode":false,"status":"ready"}`，主页字节与最终`web/dist/index.html`一致，SHA256为`6439bdcc22698a1c638d5c589e27397ba15c41d5830c887e61e9f66809435a1c`。本轮未执行浏览器账号操作；新表单与联调通过代码复核、前端契约和后端真实HTTP+PG验证，不声称评审账号的浏览器端到端验收已执行。

## 仍需完成

Gemini本轮只支持无状态纯文本，工具/媒体、cachedContent、thinking配置、countTokens与完整官方SDK认证均未交付。固定官方schema与合同取舍见[研究记录](../research/gemini-contract-notes.md)。正式供应商须验证整个请求的输入硬界、可见与thinking聚合收费输出硬界、最终usage/错误/无消费证据及成本口径；schema和合成fixture不能代替该认证。这是人工上线要求，本轮没有程序化供应商认证开关。

本轮未做性能压测，007数据仍属历史；50ms/目标资源/竞品正式对比与容器/灾备等001发布门禁保持原状态。后续按[能力路线图](../design/new-api-roadmap.md)继续凭据池/模型发现与自助经营，不将010称为New API或Gemini全功能完成。

# 003 原生协议与联调验收

2026-10-30，v0.1.0-dev本地工作树，无Git提交SHA。规格见[003](../../specs/003-native-protocols/spec.md)，官方合同来源见[协议核对](../research/native-protocol-contracts.md)。结论为本地可联调能力，不是正式供应商或完整客户端产品认证。

## 直接验证

| 范围 | 证据与结果 |
|---|---|
| 严格请求/原生响应 | [native_test.go](../../internal/provider/native_test.go)：两协议JSON、标准完整SSE、UTF-8逐字节分片、文本/函数定义/工具结果、Responses incomplete、公开模型改写、供应商认证；缺失/null/显式0/冲突/累计/缓存/溢出量，错误/未知事件/裸EOF，真实取消关闭。provider race通过。 |
| 商业纵向 | [native_protocols_integration_test.go](../../internal/server/native_protocols_integration_test.go)：真实PG+HTTP，Responses/Messages非流/流各收费20微元，输入10/输出5/缓存读取子集3。Messages输入6+读取3+创建1，缓存未重复或漏计。 |
| 终态与资金顺序 | 实际客户端读到`response.completed`/`message_stop`时，PG已有唯一settled；无Chat DONE。缺失最终量不发原生成功终态，不按预估扣款、不重放。 |
| 准入与认证 | 错端点、非法协议/参数、双header/重复header及空歧义凭据发送前拒绝，request_records/POST均0；Messages单独x-api-key及Bearer均有效。客户Key不成为供应商凭据。 |
| 取消 | 两协议在有效文本后中断客户端，确认上游context结束、并发许可归零、POST仍1、未确认量不扣款。 |
| 平台probe | 两种原生协议按正确endpoint/认证派发，客户request/Key spent/held/钱包均不变。 |
| 安全兼容边界 | 标准phase枚举与空citations/logprobs兼容；Responses生命周期response.id一致；非零托管工具计量、混流id、非零未知收费维度拒绝。响应顶层秘密metadata不透传。 |
| 选路解释 | [route_decisions_integration_test.go](../../internal/server/route_decisions_integration_test.go)：静态disabled、动态circuit_open、selected按真实选择持久保存，DB禁止改写，平台可查、普通客户403；JSON没有endpoint/供应商秘密/正文。routing完整reason与深复制race通过。 |
| 前端 | `npm --prefix web test`12项通过，包含UTF-8/SSE分帧、缺失成功终态、三协议认证、ReadableStream取消且无重试。严格TS/Vite生产构建通过。 |
| 浏览器 | 新服务实际加载多协议模型选择、平台路由尝试明细、无租户接入指南；console error/warn为空。取消表单未提交配置，未修改账号或发真实推理。 |
| Python SDK smoke | OpenAI3.41.0 `responses.stream().get_final_response()` / `responses.create()` 与Anthropic2.3.0 `messages.stream().get_final_message()` / `messages.create()`均实际执行通过。SDK读取provider真正净化后的本地HTTP/SSE fixture，假Key、无重试/外部代理或付费请求；不等于完整客户端产品认证。 |

浏览器截图位于未入Git的`.local/qa-model-protocols.png`、`.local/qa-route-attempts.png`、`.local/qa-playground-onboarding.png`，不含API Key或MFA秘密。

最终嵌入程序重启后再次只读检查：`/health/ready`为ready且recovery_mode=false，HTTP主页与最终`web/dist/index.html`一致；本地浏览器的模型表单展示三种协议，可选输入上界实际DOM值为空且placeholder正确（AX stepper可能显示min=1，实际value/aria-valuenow均为空）。关闭表单未提交配置，Playground管理员接入指南正常，应用console error/warn为空。未修改账号、因素或资金，也未调用真实推理。

## 执行命令与交叉回归

真实PG使用`moni_test`临时schema，不修改`moni_review`客户/资金数据。定向`go test -race ./internal/server -run '^TestNative' -count=1`通过（3.811s）；模型创建和平台probe纵向、路由解释隔离分别通过。协议最后兼容/身份/工具计量修复后provider race再次通过（2.524s）。

本轮`go test -race ./... -count=1 -timeout 240s`通过；之后父锁batch优化又完整回归billing/identity/server并通过（server80.038s）。`go vet ./...`通过。这些数字为测试执行时间，不是请求延迟。后续最后集成定向结果及性能另见MVP/容量记录。

最后定向集成（包括空header歧义、原生取消、模型协议、路由解释和结算后交付失败）再次通过：provider2.880s、server4.235s。Python SDK依赖在`/tmp`的Python3.12隔离环境，未进入产品运行时依赖。

收敛版本最后执行`TEST_DATABASE_URL=… make verify-mvp`成功，包含004独立上界、Claude上下文窗口终止、fresh batch Guard及数字表单空值修复。12项前端测试、strict TS/Vite、全Go race、vet和嵌入程序编译全部通过；provider3.588s、billing23.830s、identity64.535s、operations5.333s、server128.890s。容量测试显式另行运行，SDK验证仍按上文固定版本的限定范围执行，不将这些包执行时间当作延迟成绩。

## 使用范围

支持无状态文本、基础客户端函数工具、JSON/SSE；不执行工具、不自动续写。拒绝媒体、非空引用、thinking/reasoning输出块、托管工具、background、previous_response_id、store:true、WebSocket。接受usage中的reasoning子集不等于支持reasoning输出协议。供应商需提供经确认的输入硬上界、输出约束和最终usage，才能正式启售；当前合成fixture不能替代供应商认证。

Playground的耗时是本次客户端观察，不是p95，也不是模型首token基准。当前单实例、人工双人核款；在线支付、开放注册、订阅、Gemini/Embeddings/Rerank、媒体和HA继续按路线图后置。无attempt的完整失败候选池解释尚未持久化，旧attempt显示缺少历史解释。正式目标资源与竞品性能、容器首装/升级、真实供应商及异地恢复仍须分别验收。

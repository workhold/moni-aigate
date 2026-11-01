# 协议与路由实施前源码笔记

2026-10-30；仅阅读已固定本地快照，不运行上游程序，不复制其实现。产品实现遵循已批准 r3 合同。

- New API `e449cc700eb569cdfc2199cb73e97de46944f43c`：[controller/relay.go:180–240](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/controller/relay.go#L180-L240) 在每次重试前重置流状态、重新选渠道、恢复请求体，再进入协议 handler；本实现用独立 Request 与 attempt 状态，不复用 Gin Context。
- New API [relay/helper/stream_scanner.go:24–52](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L24-L52) 配置扫描缓冲，[同文件:172–198](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L172-L198) 关闭 body 后等待 goroutine 退出。moni 的合同更窄：完整 SSE event 1MiB，支持 CR/LF/CRLF、多 data 行，不把扫描行当完整事件；取消关闭 body，执行资源退出后才释放路由许可。
- New API [relay/channel/openai/usage.go:55–83](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/usage.go#L55-L83) 使用指针分辨 cached 字段存在性，同时存在供应商字段映射。moni 首版仅接受版本化 OpenAI Chat 字段；missing/null/0 分开，cached/reasoning 为子集，缺失或冲突不能估算收费。
- One API `8df4a2670b98266bd287c698243fff327d9748cf`：[relay/adaptor/interface.go:11–20](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/relay/adaptor/interface.go#L11-L20) 分离请求转换、发送、响应及 usage。moni 采用自包含 provider 包，但不携带框架 Context 或任何财务写入。
- One API [model/ability.go:21–48](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/ability.go#L21-L48) 先最大优先级再随机；[controller/relay.go:65–91](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/relay.go#L65-L91) 更换失败渠道；[同文件:105–121](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/relay.go#L105-L121) 重试主要按状态码。moni 改为候选硬过滤后优先级/正权重，最多两次统一预算，只按可信无消费证据允许重试；已提交下游或消费未知不重放。

实施决策：所有生成 POST 不设置 GetBody、禁 redirect/SDK retry/环境代理，初版关闭 HTTP keep-alive，消除池内隐式重放路径；每次连接都验证 DNS 结果并直接连接验证 IP，TLS 保留原 hostname 验证。调用者负责持久 attempt、配置/权限再准入与总 deadline；provider 不自行重试。上述行为用本地真实 HTTP/TCP fixture 验证。

# Chrome 补充研究：产品合同与源码证据的区别

状态：DRAFT / 设计输入；访问日期：2026-10-30（Asia/Shanghai）。本轮实际使用 Chrome 搜索并阅读公开 GitHub 页面、Helicone 官方文档和 OpenRouter 官方文档；没有注册、上传项目内容或调用收费 API。

搜索只用于发现候选。下面“文档声明”来自访问日页面，会随产品变化，不是固定源码结论，也不是 moni 已通过的验证。GitHub 源码机制与许可另见[路由研究](extended-routing.md)、[商业研究](extended-commerce.md)、[观测研究](extended-operations.md)。

## 1. 实际阅读入口

| 页面 | 访问观察 | 证据用途 |
|---|---|---|
| [Helicone 独立网关 GitHub](https://github.com/Helicone/ai-gateway) | 默认分支显示 `9649b27`，提交标题为更新 Apache2 到 GPLv3；README 仍保留 Apache 描述，仓库 LICENSE 为 GPLv3 | 浏览器交叉核查身份与许可冲突；具体策略源码见观测研究 |
| `https://docs.helicone.ai/ai-gateway/config` | 旧网关 README 链接本次返回 404 | 不用失效地址推断当前产品能力 |
| [Helicone Gateway overview](https://docs.helicone.ai/gateway/overview) | 当前产品文档包含托管 credits、BYOK、模型目录等 | 当前产品流程参考；不自动归入上述旧 Rust 源码 |
| [Helicone provider routing](https://docs.helicone.ai/gateway/provider-routing) | 描述供应商/部署选择、排除项及 fallback | 路由产品合同参考；宽泛错误回退不等于无消费证明 |
| [OpenRouter provider selection](https://openrouter.ai/docs/guides/routing/provider-selection) | 区分模型和供应商路由，定义硬限制与性能偏好 | 托管商业平台的公开产品合同参考；未取得其服务端实现 |

OpenRouter 在本研究中是**商业产品参考**，不列作已审计的开源网关。Google 搜索摘要中的维护状态、性能或省钱数字都不直接作为证据。

## 2. OpenRouter：值得吸收的是约束语义

该文档声明同一模型可有多个供应商 endpoint，并提供 `order`、`only`、`ignore`、`allow_fallbacks`、`require_parameters`、`data_collection`、`zdr`、`sort`、`max_price` 和性能偏好等字段。moni 不把这套字段原样加入首版。

| 文档声明 | 对 moni 的设计启发 | v0.1 取舍 |
|---|---|---|
| 模型选择与同模型供应商选择分开 | 客户购买的模型身份与渠道调度是两层决策 | 只在同逻辑模型中换渠道，不暗中降档 |
| `only` 与账号白名单求交；`ignore` 合并排除 | 请求只能缩小授权，不能扩大租户/Key 权限 | 有效候选取平台、租户、成员、Key 与能力/地域约束的交集；不新增客户端路由 DSL |
| 请求 `zdr` 能加严而不能关闭上层要求 | 安全与隐私约束应单调收紧 | 上层禁用不能被模型别名、请求字段或外部 header 绕过；供应商实际承诺另核实 |
| `require_parameters=true` 排除无法满足参数的供应商；默认模式可能忽略部分参数 | “调用成功”不等于完整履行协议 | moni 拒绝不支持项，不能静默丢弃 tools、输出约束或计费相关参数 |
| `max_price` 是排除条件，preferred latency/throughput 是偏好 | 硬约束与软排序需要清楚分层 | 首版权限/地域/能力/有效配置是硬约束；优先级/权重只在过滤后生效；不承诺性能 SLA |
| 路由说明考虑有效价格、缓存、吞吐、TTFT、429、质量与探索 | 自适应路由需要观测与持续评估 | 后续离线回放和影子评分，再决定是否开放，不能从宣传文字推断收益 |
| 保持会话缓存暖态可影响路由 | 最低单价未必带来最低会话总成本 | 后续与 Plano 的上下文重送/缓存损失一并评估；首版不做内容缓存 |

文档中的区域路由有套餐条件，供应商数据策略是平台按其已知信息整理的说明。moni 不能仅因复制一个配置字段就声称取得地域隔离或零留存保证。供应商、区域 endpoint 与合同证据须单独核验。

## 3. Helicone：不要混用当前服务与旧仓库能力

当前官方文档的 BYOK-first、供应商选择和更广的 fallback 错误列表提供产品体验参考。但旧 Rust 网关的策略枚举、当前托管路由行为、另一个观测仓库是三类证据，不能拼成一个“全部开源可用”的结论。

对 moni 的实际影响：逻辑模型别名必须先在服务端目录解析，客户不能通过拼接供应商/部署名选择未授权 endpoint；上下游错误先分类，再按经验证的渠道 profile 判断是否允许重试。401/429/5xx/超时出现在别人文档的回退列表中，不足以证明 moni 的某个真实供应商没有处理请求或收费。

## 4. 已并入设计的范围

首版加强配置完整校验、不可变版本、原子切换；候选先做硬过滤再排序；Key 判定、计量事件与资金操作各有明确身份；元数据白名单与统一脱敏；统计投影重放不能产生资金副作用。

自适应/语义路由、显式 `auto` SKU、会话成本优化、评估数据集和外部观测导出都保留后续规格，不增加首版运行组件。具体追踪见 [HLD](../design/HLD.md)、[LLD](../design/LLD.md) 和[验收矩阵](../../specs/001-commercial-mvp/acceptance.md)。

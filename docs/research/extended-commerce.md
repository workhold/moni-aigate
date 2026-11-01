# 扩展研究：Lago、OpenMeter 与 Unkey

状态：DRAFT / 设计输入；研究日期：2026-10-30（Asia/Shanghai）。本次定向补充商业计量、账务与开发者平台设计；moni-aigate 仍独立实现，不把这些系统变成首版运行依赖。

## 1. 基线与核查范围

通过 GitHub 搜索与 Repository/Commit/Tree API 核对项目身份，再按固定 blob 读取 README、许可和代表性源文件。没有全量克隆、运行第三方服务、执行付款或业务测试。搜索结果只用于发现，机制结论以实际取得的代码为准。

| 项目 / canonical 仓库 | 固定提交 / 提交时间（UTC） | 实际读取与许可边界 |
|---|---|---|
| [getlago/lago](https://github.com/getlago/lago) | `af1a82746fa38125d9aa97b5121811349627f2ed`；2026-10-29T08:20:35Z | 根 README/LICENSE、.gitmodules、Compose；根许可 AGPLv3。该仓库是组装入口，API 是下列固定 gitlink，不将主仓时间当 API 源码时间 |
| [getlago/lago-api](https://github.com/getlago/lago-api) | 组装仓 `api` 指向 `753abde3787f0931b4bf39df83b6a74960fe21a6`；2026-10-08T12:55:07Z | 事件校验/模型、钱包交易结算服务；API 的 GitHub 许可元数据为 AGPLv3，单独 LICENSE 正文请求受匿名 API 限流未取得，不假装完成全依赖许可清理 |
| [openmeterio/openmeter](https://github.com/openmeterio/openmeter) | `a2da2f9754dc720efc8ac80df3c41b6f73ef36e7`；2026-10-29T17:00:04Z | README/LICENSE、事件摄取/去重、Redis 去重实现、ledger 文档与 correction 输入源码；根许可 Apache-2.0 |
| [unkeyed/unkey](https://github.com/unkeyed/unkey) | `01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79`；2026-10-29T19:44:49Z | README/LICENSE、Key 验证、credits SQL、滑动窗口限流源码与架构说明；根许可 FSL-1.1-ALv2，少数子包有单独例外，不是当前版本整体 MIT/Apache |

许可原文：[Lago 根 LICENSE](https://github.com/getlago/lago/blob/af1a82746fa38125d9aa97b5121811349627f2ed/LICENSE)、[OpenMeter LICENSE](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/LICENSE)、[Unkey LICENSE](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/LICENSE)。Lago 的 [.gitmodules](https://github.com/getlago/lago/blob/af1a82746fa38125d9aa97b5121811349627f2ed/.gitmodules) 确认 API/前端为单独仓库；本次未把 API 新 main 的行为混到 gitlink 版本中。

匿名 GitHub API 后续触发 403 rate limit，固定 SHA raw 读取也被网络重置；未取得的部分明确保留边界，没有借用不明镜像补齐结论。

## 2. Lago：借鉴事件归属、交易来源与通知时点

Lago 的公开产品范围是计量与计费平台，包含计划、用量、发票和预付权益等，不是 LLM 协议网关。根 README 区分开源自托管、Cloud 与按功能提供的 Premium；例如 Billing Assistant 明确为 Premium。不能把产品总功能表等同于任意自托管基线全部可用。[README:151-165](https://github.com/getlago/lago/blob/af1a82746fa38125d9aa97b5121811349627f2ed/README.md#L151-L165)、[README:210-215](https://github.com/getlago/lago/blob/af1a82746fa38125d9aa97b5121811349627f2ed/README.md#L210-L215)

**机制 A：计量事件有业务归属与去重作用域。** `ValidateCreationService` 校验外部订阅归属、时间戳、transaction_id、计量 code 及相应 properties；重复检查同时绑定 organization、subscription 和 transaction_id。Event 模型的 schema 注释列有 `(organization_id, external_subscription_id, transaction_id)` 唯一索引。这说明事件 ID 必须有明确命名空间，不能假设任一客户端传入的字符串在全平台唯一。[validate_creation_service.rb:16-62](https://github.com/getlago/lago-api/blob/753abde3787f0931b4bf39df83b6a74960fe21a6/app/services/events/validate_creation_service.rb#L16-L62)、[event.rb:86-95](https://github.com/getlago/lago-api/blob/753abde3787f0931b4bf39df83b6a74960fe21a6/app/models/event.rb#L86-L95)

**机制 B：结算记录与后续通知分开。** `WalletTransactions::SettleService` 把交易状态与 settled_at 写为已结算；对可追溯入账设置剩余金额，并在 `after_commit` 后安排更新通知。它提供了“先提交业务状态，再让外部看到通知”的清楚边界，但这份小服务并不证明整个钱包入账、付款和退款链路已被本次审计，也不能单凭 `after_commit` 宣称跨崩溃通知 exactly-once。[settle_service.rb:18-28](https://github.com/getlago/lago-api/blob/753abde3787f0931b4bf39df83b6a74960fe21a6/app/services/wallet_transactions/settle_service.rb#L18-L28)

对 moni 的采纳：账单/退款/核款事件保存 `tenant_id + operation + business_id`，关联原始 request/journal；通知只能在资金事务成功后产生。需要可靠重发时使用本地持久 outbox 与消费幂等，不让通知失败反向重扣钱包。首版不引入订阅、复杂促销、发票和整套 Lago 部署。

## 3. OpenMeter：借鉴事件合同和可纠正的账务事实

当前 OpenMeter 已包含产品目录、entitlements、credits、billing、invoice 与 ledger，不能沿用旧印象把它描述成“只有事件计量”。README 同时明确它不是支付处理商、税务引擎、通用会计系统或打包运营 UI，并说明发布仍有 beta/变更风险；这些是官方定位，不是本次运行认证。[README:9-18](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/README.md#L9-L18)、[README:38-52](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/README.md#L38-L52)

**机制 A：CloudEvents 摄取与财务结算不是一个完成时刻。** Handler 支持单条/批量 JSON 与 CloudEvents 格式，调用摄取服务后返回 202 Accepted；服务规范化事件时间再向 Collector 转发。去重包装器用 namespace 与 event 检查唯一性；Redis 实现的身份包含 namespace、event ID 和 source，并采用 NX+TTL。[ingest.go:28-101](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/api/v3/handlers/events/ingest.go#L28-L101)、[service.go:98-124](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ingest/service.go#L98-L124)、[dedupe.go:19-30](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ingest/dedupe.go#L19-L30)、[redisdedupe.go:40-102](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/dedupe/redisdedupe/redisdedupe.go#L40-L102)

这类去重是摄取策略，不能代替 moni 永久资金唯一约束；去重键到期也不能让历史收费重新产生。202 仅代表该接口接受处理，不应展示成“客户已扣费/发票完成”。

**机制 B：更正沿原始分录表达，幂等由发起业务承担。** ledger 文档明确区分账本、charge 生命周期与客户余额投影，特别说明原子 `CommitGroup` 本身不去重重复调用，发起域必须保存 group reference 与生命周期。实际 correction 输入要求原始 transaction；选定源分录必须存在、金额为正且不超过原始可选量，合计必须等于更正金额。[ledger/README:25-45](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ledger/README.md#L25-L45)、[README:84-88](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ledger/README.md#L84-L88)、[correction.go:36-72](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ledger/transactions/correction.go#L36-L72)

对 moni 的采纳：区分三类身份——计量 event_id、上游 attempt_id、资金 operation/idempotency key；保留 happened_at/received_at 与来源版本。客户退款必须指向原 settlement/journal，报表重建不得再次扣款；迟到供应商事件在未知冻结已释放后只能修正平台成本。事件聚合可以重放，资金副作用必须唯一。首版可采用 PostgreSQL outbox/投影，不为追求“实时计量”直接引入 Kafka/ClickHouse 或完整 OpenMeter 栈。

## 4. Unkey：借鉴独立 Key 验证、错误结果和限流边界

当前 README 自称 API 开发者平台，覆盖 gateway、Key、RBAC、限流、analytics，不只是 Key SDK；但这些产品声明不意味着本次已验证所有托管节点行为。根许可证除少数子目录外为 **FSL-1.1-ALv2**，明确定义 Competing Use 限制，并在特定版本公开满两年后给予 Apache-2.0 future license；不能把未来许可直接当作当前版本许可。[LICENSE:3-8](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/LICENSE#L3-L8)、[LICENSE:39-50](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/LICENSE#L39-L50)、[LICENSE:96-102](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/LICENSE#L96-L102)

**机制 A：验证管线把认证失败和系统失败分开。** `KeyVerifier.Verify` 先检查 keyspace、已有状态、IP 与权限，再检查 rate limits 和 credits；注释明确认证失败写 Status，配置/系统失败返回 error。TelemetrySnapshot 在验证结束后反映最终状态，避免把权限拒绝或额度不足统计成成功验证。[verifier.go:75-130](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/verifier.go#L75-L130)、[verifier.go:153-178](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/keys/verifier.go#L153-L178)

**机制 B：本地原子准入不代表全局财务强一致。** 限流源码在有界 CAS 循环中检查并增量，竞争耗尽则 fail closed；它返回 remaining/reset 等结构化信息。包说明明确 local 模式不跨实例同步、丢弃实例会丢计数；分布式路径含 Redis 异步 replay 和跨区域传播。因此不能把任一“全球限流”产品口号变成 moni 余额零透支保证。[ratelimit.go:124-195](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/ratelimit/ratelimit.go#L124-L195)、[doc.go:5-31](https://github.com/unkeyed/unkey/blob/01c0c9329b0f1c8cb37e3303d1d05aaa761f8e79/internal/services/ratelimit/doc.go#L5-L31)

**r3继续追调用者后的限定：** 上述fail-closed只描述局部分支；实际Key验证caller遇到某类限流服务error时记录日志后继续准入。credits另有counter/回放与DB降级边界，也不是moni的钱包预占。不能外推整条验证链fail-closed；固定源码、装配和测试详见[安全校准 §1.3/1.4](calibration-security-operations.md)。

对 moni 的采纳：Key 验证输出明确 `invalid/forbidden/expired/revoked/rate_limited/budget_exceeded/system_unavailable` 等内部结果，再映射公开状态码；调用记录记最终判定。RPM/并发是可重置运行时控制，Key 花费上限和钱包冻结仍走 PostgreSQL 事务。不开一个外部 Key 验证服务作为每次收费调用的强依赖，不复制其当前受限制许可实现。

## 5. 建议并入 HLD/LLD 的小范围改进

| 改进 | v0.1 落点 | 后置内容 |
|---|---|---|
| 业务事件有独立身份与版本 | `event_id/source/schema_version/tenant_id/request_id/attempt_id/operation_id/happened_at/received_at`；不能把三种幂等键混用 | 对接外部metering平台、多源事件导入 |
| 资金与报表解耦但可追溯 | 资金事务内落账并保存待投影/outbox项；Worker可重放，消费者按事件ID去重，绝不从报表重放扣款 | Kafka/ClickHouse、复杂订阅和发票 |
| 通知在提交之后发出 | 低余额/入账/退款通知从已提交事实生成，失败重发只重发通知 | 多渠道通知、营销与自动续费 |
| 退款与成本调整保留原始来源 | 追加更正，关联原settlement/journal；迟到成本不自动追扣已释放客户款 | 多币种、税务、收入确认总账 |
| Key判定有明确原因 | 内部认证结果区分权限、额度、速率、系统错误，客户错误消息不泄漏秘密 | 全球验证/多地域最终一致限流 |

本次没有证明任一参考项目能直接满足 moni 的“上游发起前持久冻结、收费不超过预占、24h 未知释放、请求唯一结算”全部合同；这些仍由 moni 的规格与真实 PostgreSQL 故障验收保证。借鉴设计不扩大首版范围，也不构成许可、性能或可上线状态的认证。

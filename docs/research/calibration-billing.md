# 资金、计量与故障恢复的源码校准

状态：DRAFT / 供设计审阅；2026-10-30。本文补充调用链和失败边界，不是上游漏洞认证，也不是 moni-aigate 已实现能力。**只阅读源码和测试；未运行上游程序、单元测试、数据库矩阵、并发测试、故障注入或付费调用。**

## 1. 固定基线与本轮增量

| 项目 | 固定提交 | 本轮实际追踪 |
|---|---|---|
| New API | `e449cc700eb569cdfc2199cb73e97de46944f43c`；本地 Git HEAD 已核验 | Relay → 初始预扣/资金来源 → Redis或SQL写入 → 补充预扣 → 用量/交付终止 → 结算/退款；订阅退款内外事务；相关测试与后台维护 |
| OpenMeter | `a2da2f9754dc720efc8ac80df3c41b6f73ef36e7` | 已取得的固定源码：HTTP摄取 → Collector → Redis去重；credit grant外部支付状态入口与Void事务编排。没有取得 ledger adapter 的新增叶子源码，不能把编排层当作完整持久化审计 |
| Lago API | `753abde3787f0931b4bf39df83b6a74960fe21a6`，Lago组装仓固定gitlink | 已取得的钱包交易 SettleService 的实际 update 与提交后通知时点；没有补齐完整入账/支付链路 |

本轮尝试读取 OpenMeter 固定 raw 文件遇到连接重置，GitHub Blob API 遇到匿名限流，官方 blob 页面超时。因此以下结论明确区分“实际代码可见”与“未取得/未验证”，不补写推测的下层保证。

相较[上一轮分析](new-api-analysis.md)，本轮有三项重要校正：New API 并非只有一种“预扣”；不同分组重试可能改变最终计价；订阅退款虽有请求ID和状态保护，**不能只因出现 Transaction 就认定余额与退款标志同事务提交**。

## 2. New API：初始预扣的真实链路

```text
controller.Relay
  → relay.PrepareRequestBilling
  → EstimateRequestToken / ModelPriceHelper
  → service.PreConsumeBilling
  → NewBillingSession（选择 wallet / subscription）
  → BillingSession.preConsume（令牌先扣，资金来源后扣）
  → FundingSource.PreConsume
  → Redis Lua + 数据库增量，或数据库条件UPDATE
```

| 层次 | 可确认行为 | 失败/持久化边界 |
|---|---|---|
| HTTP请求编排 | 预扣成功后才进入渠道重试循环；失败退款defer在预扣成功后注册 | 生命周期对象放在请求 `RelayInfo.Billing` 中，不等于建立了跨进程请求账本。[controller/relay.go:134-189](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/controller/relay.go#L134-L189) |
| 价格/数量准备 | 先估算请求token并计算预扣。legacy token路径使用输入估算乘配置倍数；有独立阶梯表达式路径 | 不应把这个估算直接称为完整输入+输出的可证明最大金额。两种价格路径不能混为一谈。[request_billing.go:25-67](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/request_billing.go#L25-L67)、[price.go:65-126](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/price.go#L65-L126) |
| 资金来源选择 | 支持 wallet_only / subscription_only / wallet_first / subscription_first；回退按错误类别及订阅允许溢出政策处理 | 不是遇到任意系统错误就换资金源重扣；moni第一版无需把订阅及回退复杂度一起引入。[billing_session.go:388-489](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L388-L489) |
| 信任旁路 | 钱包和令牌满足阈值时，初始实际预扣可变成0；强制预扣和订阅不允许该旁路 | 这是产品授信/体验政策，不是“所有调用都足额冻结”。[billing_session.go:208-236](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L208-L236)、[同文件:329-358](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L329-L358) |
| 令牌与资金先后 | 先扣Token，再扣Funding；Funding失败时尝试把Token增回，补偿失败写日志 | 注释虽称“原子回滚”，实际调用是分步写入/补偿；没有同一SQL事务把两者包住的证据。[billing_session.go:206-253](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L206-L253) |
| 钱包准入写入 | Redis Lua在缓存中检查余额并扣减；缓存缺失/错误时可回退SQL；SQL用 `WHERE quota >= amount` | Lua原子性仅覆盖它所在的缓存操作。缓存命中后还要落库/入批量队列；数据库直写失败有缓存反向补偿。[quota_reserve.go:19-54](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/quota_reserve.go#L19-L54)、[同文件:105-197](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/quota_reserve.go#L105-L197) |

**可以确认有并发准入保护；不能由此推出“钱包、Token、请求、日志在跨崩溃场景中完全原子”。** 缓存错误回退与批量未落库的组合还需要特定部署故障测试，本轮没有证明这个组合的实际结果。

## 3. 补充预扣、最终售价与结算：三个不同合同

### 3.1 发送前补足与允许欠费的Reserve不同

`PreConsumeBilling` 发现已有会话时调用 `ReserveBeforeSubmit`，要求余额覆盖补充部分。`BillingSession.reserve` 先扩充资金预扣，再扩充Token；后一步失败则补偿资金。图像数量也会强制走余额覆盖分支。[billing.go:20-55](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing.go#L20-L55)、[billing_session.go:158-198](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L158-L198)

普通 `Reserve` 的钱包分支则允许无条件扣减并形成负余额；最终 `WalletFunding.Settle(delta>0)` 也调用普通钱包扣减。这是代码明确表达的欠费政策，不能把它误述成零透支预付钱包。[billing_session.go:264-285](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L264-L285)、[funding_source.go:42-74](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/funding_source.go#L42-L74)

### 3.2 重试保留会话，不代表最终零售价永远固定

上游测试明确规定：换到更贵分组后，钱包20,000补扣50,000可变为-30,000；实际结算80,000后余额为-10,000。另一组测试明确使用最后分组的ratio结算。[tiered_settle_test.go:447-497](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/tiered_settle_test.go#L447-L497)、[同文件:525-560](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/tiered_settle_test.go#L525-L560)

所以，moni选择“T1固定客户售价、后续只改变供应商成本、实收不超过R”，是我们主动设计的客户合同，不能称为原样继承New API的价格快照机制。New API的分组结算策略也不能仅因与moni不同就称为实现缺陷。

### 3.3 最终结算会区分资金已提交和Token调整失败

`Settle(actual)` 在内存mutex内算差额，先调用资金来源Settle，成功后标记 `fundingSettled`，再调Token。Token失败时仍标记settled并返回错误，避免接着Refund把已结算资金误退；这是一项实际存在的防误退保护。[billing_session.go:44-84](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L44-L84)

但文本后处理对 `SettleBilling` 错误只记录日志，随后继续生成消费记录；使用统计还可先于结算调整。消费日志另走可关闭的 `RecordConsumeLog` / LOG_DB，写入失败同样记录日志。这些日志不等于原子收费账本。[text_quota.go:458-468](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/text_quota.go#L458-L468)、[log.go:339-387](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/log.go#L339-L387)

## 4. 退款：钱包和订阅的保证不能合并描述

### 4.1 钱包退款：当前会话防重，底层增量不能盲重试

`Refund` 先在会话内把 `refunded=true`，再异步提交资金退款和Token退款；错误只记录日志。`WalletFunding.Refund` 明确指出 `quota += N` 是非幂等增量，禁止简单重试。[billing_session.go:87-128](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing_session.go#L87-L128)、[funding_source.go:68-74](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/funding_source.go#L68-L74)

可确认的是**同一个存活会话内的防重复触发**。未看到这条钱包退款路径用持久request操作记录恢复“标记后未执行”“资金成功但Token失败”“结果未知后重试”的完整链路，不能将其写成跨崩溃exactly-once。

### 4.2 订阅：有持久请求记录，但需审查事务传递

预扣确实使用 `SubscriptionPreConsumeRecord.RequestId uniqueIndex`。同一个 `DB.Transaction` 中检查已有记录、锁定订阅、创建预扣记录、增加AmountUsed；这比只依赖内存标志有更强的持久业务身份。[subscription.go:1243-1252](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/subscription.go#L1243-L1252)、[同文件:1318-1397](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/subscription.go#L1318-L1397)

退款入口锁预扣记录、检查refunded，余额调整后再保存refunded。**关键边界：余额调整调用 `PostConsumeUserSubscriptionDelta`，并没有把外层tx传进去；后者重新从全局DB开始Transaction。** 因而本次不能确认两项写入共享同一数据库事务；不能仅按外层Transaction和函数注释认定它们同成败。若要确定内层提交后外层失败的实际效果、连接池影响及重试后果，需要专门故障测试。[subscription.go:1407-1430](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/subscription.go#L1407-L1430)、[同文件:1515-1536](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/subscription.go#L1515-L1536)

资金来源层基于“订阅退款幂等”的预期提供最多3次重试。**重试意图存在，不代表本轮证明了全部故障边界安全。**[funding_source.go:122-149](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/funding_source.go#L122-L149)

## 5. 超时、交付与usage：失败标记不等于免单

### 5.1 Chat与Responses都保留“失败流仍产生计量”的语义

Chat SSE handler读取流后，会从最后/倒数第二个事件取累计usage，缺失时按已收集文本和估算输入补usage，最后返回 `usage,nil`。StreamStatus记录传输/协议事实；它不是所有路径统一返回的计费错误。[relay-openai.go:125-197](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay-openai.go#L125-L197)

Responses路径更清楚：先 `accumulator.Observe`，后向下游写event；写失败设置sr.Stop，但结束仍返回 `accumulator.Finish(),nil`。Accumulator会从多个终止种类读取usage；没有usage时可估算已生成输出，并在满足条件时计入估算输入。这是明确的用量/故障政策，不能误述为“流失败一律退款”。[relay_responses.go:70-105](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay_responses.go#L70-L105)、[responses_usage.go:32-72](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/responses_usage.go#L32-L72)、[同文件:88-115](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/responses_usage.go#L88-L115)

继续向调用者追踪：ResponsesHelper只在DoResponse返回API error时提前返回；正常返回usage就调用ConsumeResponsesQuota，普通文本进入PostTextConsumeQuota，继而到前述SettleBilling。Chat的TextHelper也在DoResponse成功后进入相同文本结算路径。因而“handler返回nil但StreamStatus有错误”确实能继续进入计费计算；实际金额仍取决于usage和价格合同。[responses_handler.go:74-110](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/responses_handler.go#L74-L110)、[compatible_handler.go:171-186](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/compatible_handler.go#L171-L186)

测试也明确要求：终止event写入/flush失败时有StreamStatus错误，但handler API error仍为nil，并保留终止usage。这个测试保护的是handler的合同，未直接证明完整HTTP→钱包最终结果。[relay_responses_billing_test.go:129-142](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay_responses_billing_test.go#L129-L142)、[同文件:189-211](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay_responses_billing_test.go#L189-L211)

非流Responses先写HTTP正文，复制错误写日志但不向调用者返回，再计算usage。moni若决定“先验证完整JSON→持久结算→写下游”，应将这种差别写为自己的合同，而不是假设上游已经提供原子交付。[relay_responses.go:22-67](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay_responses.go#L22-L67)、[http.go:64-78](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/http.go#L64-L78)

### 5.2 超时关闭是资源保护，不是持久资金恢复

StreamScanner在读取行时重置超时计时器；超时/客户断开时记录原因，cleanup关闭Body并等待协程退出。终止帧成功写入与之后取消之间也有明确纠正规则。它解决传输资源和终止判定，不能与moni的24h资金核对期限混用。[stream_scanner.go:297-350](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L297-L350)、[同文件:353-366](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L353-L366)

Relay的metrics defer会recover再panic；失败退款defer只把当时 `newAPIError` 传给 `RefundFailedRequestBilling`，而该函数遇到nil直接返回。因此不能把“有recover/defer”称为已经恢复所有panic资金场景；例如进入handler时panic且错误变量尚未设置，不由这条nil检查保证退款。moni应依赖持久hold扫描，不把defer当最终恢复保证。[controller/relay.go:140-165](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/controller/relay.go#L140-L165)、[request_billing.go:70-81](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/request_billing.go#L70-L81)

订阅维护任务负责到期、周期重置和7天旧预扣记录清理；该路径没有按请求派发状态恢复所有未完成Chat的代码证据。旧记录清理也不是永久金融唯一键的替代品。[subscription_reset_task.go:46-86](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/subscription_reset_task.go#L46-L86)、[subscription.go:1476-1483](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/subscription.go#L1476-L1483)

## 6. OpenMeter / Lago补证：事件接收、业务提交、通知不是同一时刻

### 6.1 OpenMeter：实际摄取和去重写入链路

```text
HTTP Ingest handler → IngestEvents → processEvent
  → Collector.Ingest
  → 若配置DeduplicatingCollector：IsUnique
  → Redis SET NX + TTL（写去重索引）
  → 仅首次身份转交下层Collector
  → 成功路径返回202
```

Handler调用服务后返回202；服务逐条处理事件、规范时间，再转交Collector，遇错返回；这些层没有把“接收成功”表达成钱包扣款完成。[ingest.go:91-99](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/api/v3/handlers/events/ingest.go#L91-L99)、[service.go:83-124](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ingest/service.go#L83-L124)

可见的去重包装器先执行IsUnique再调用下层，Redis的IsUnique同时写入身份索引，身份包含namespace/source/id，使用NX及TTL；这不是与下层持久写入共享的SQL事务。该包装器本身没有显示“下层失败撤销去重标志”的协议。因此不能从这段去重代码推出端到端至少一次无丢失，更不能拿TTL替代永久收费幂等。[dedupe.go:19-30](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ingest/dedupe.go#L19-L30)、[redisdedupe.go:40-50](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/dedupe/redisdedupe/redisdedupe.go#L40-L50)、[同文件:83-105](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/dedupe/redisdedupe/redisdedupe.go#L83-L105)

这里确认的是**特定包装器的调用顺序和独立写入边界**；未追到本次部署的完整Collector组装、队列持久化和下游补偿，不能据此宣称OpenMeter全部部署必然丢事件。

### 6.2 OpenMeter：实际资金业务编排与未取得的叶子保证

外部结算HTTP入口解析namespace/customer/credit grant后调用 `UpdateExternalSettlement`；服务检查归属与资金类型，交由charge生命周期处理外部支付状态迁移。这说明“外部付款状态”和“ledger记账”由不同职责承担；本次没有取得该迁移的全部叶子写入实现。[externalsettlement.go:25-53](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/api/v3/handlers/customers/credits/externalsettlement.go#L25-L53)、[service.go:256-291](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/billing/creditgrant/service/service.go#L256-L291)

Void链路则把 `VoidCreditPurchase` 和 `MarkVoided` 放进同一个 `transaction.Run` 的ctx内，返回前传播错误。可确认调用方要求共享事务；但依赖服务是否都使用ctx里的同一事务，本轮没有新增叶子证据，不能仅凭ctx命名判定已证明。[service.go:294-342](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/billing/creditgrant/service/service.go#L294-L342)

其ledger文档还明确：CommitGroup的原子性不替代发起业务域的幂等，调用域须将group引用和业务生命周期一起持久化。这与moni“唯一settlement+不可变分录+业务状态同提交”的方向一致，文档声明不作为本轮运行证明。[ledger/README.md:50-88](https://github.com/openmeterio/openmeter/blob/a2da2f9754dc720efc8ac80df3c41b6f73ef36e7/openmeter/ledger/README.md#L50-L88)

### 6.3 Lago：钱包状态写入后安排通知

`WalletTransactions::SettleService` 实际调用update!写settled/settled_at及适用的remaining amount，然后在after_commit安排Webhook job。可以确认通知意图发生在业务提交后；不能仅从此服务认定job在提交后崩溃窗口仍然可靠投递，也不能推断全钱包和支付链路已被审计。[settle_service.rb:18-28](https://github.com/getlago/lago-api/blob/753abde3787f0931b4bf39df83b6a74960fe21a6/app/services/wallet_transactions/settle_service.rb#L18-L28)

## 7. 上游测试提供了什么证据

| 已阅读测试 | 可确认测试意图 | 本轮没有证明 |
|---|---|---|
| [quota_reserve_test.go:77-139](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/quota_reserve_test.go#L77-L139) | 无Redis条件预扣；Redis批量模式命中时不会用未刷新的DB余额再次准入 | 实际运行通过；真实Redis故障转移；并发/崩溃完整矩阵 |
| [同文件:173-220](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/quota_reserve_test.go#L173-L220) | Redis不可用回退；同步持久化失败时补偿缓存 | 未刷批次+缓存丢失+进程崩溃的组合结果 |
| [text_quota_test.go:32-94](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/text_quota_test.go#L32-L94) | 有SQLite/MySQL/PostgreSQL计费矩阵；后两者无DSN会skip，连接数设置为1 | 不能把存在测试文件写成三个数据库已验证，更不能把串行矩阵写成多连接竞争测试 |
| [同文件:119-144](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/text_quota_test.go#L119-L144)、[219-252](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/text_quota_test.go#L219-L252) | 同一会话重复Refund/Settle；固定按次价格缺usage仍收费；图像/任务发送前补足失败和退款 | 不是崩溃后重建新会话的永久幂等，也不能将固定按次规则套到moni按Token计费 |
| [relay_responses_billing_test.go:129-211](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay_responses_billing_test.go#L129-L211) | protocol终止、客户取消、写失败、flush失败、scanner失败分别保留状态/usage | 不直接覆盖整个HTTP→钱包结算的端到端资金结果 |

全仓 `*test.go` 定向检索未找到直接调用 `RefundSubscriptionPreConsume`、`PreConsumeUserSubscription`、`refundWithRetry` 的用例；这不排除间接覆盖，但不足以声称上述订阅内外事务故障窗口已有测试保证。

## 8. 对moni现有设计的可操作校准

以下均为**待用户审阅的设计建议**，不是已经批准的计费政策；不在本研究文件中修改主HLD/LLD或开始实现。

| 校准项 | 对现稿的挑战与建议 | SDD中应证明的结果 |
|---|---|---|
| C-B01：预占不能只保证数学安全 | LLD §5.3允许用模型最大输入作为回退，会让短请求冻结接近长上下文最大成本，余额足够支付实际请求也可能拒绝。每个收费profile应给出输入上界计算方法、有效输出限制、隐藏推理覆盖和样例R/实际额；优先上线可验证规则的少量profile。保留硬上界和收费封顶，不把New API的估算/授信旁路偷偷引入 | 普通短请求、长上下文、缺输出限制和推理Token样例都给出精确R；能力改变必须产生新profile版本；无可靠上界的渠道保持关闭 |
| C-B02：把客户售价稳定写成主动合同 | New API最终分组ratio可变化；moni明确T1固定客户零售价，重试只改变attempt成本。若未来允许分组加价，需要单独客户可见政策，不能修改历史快照 | 同请求切主备渠道、供应商调价后客户收费不变；各attempt成本使用各自版本 |
| C-B03：原子性落实到事务句柄 | LLD T0/T1/T2/T3已有正确目标，但实现计划须要求wallet/Key/hold/settlement/journal/outbox/audit helper显式共享同一tx；事务中禁止helper从全局连接另开资金事务。不能仅靠函数名和Transaction外壳审查 | 在每项写入前后注入错误、外层回滚和提交结果未知，所有资金/事件/状态同成败；真实PG多连接重放不重复收费或退款 |
| C-B04：交付责任点与收费矩阵 | “无有效交付免收”必须区分pre-T2失败与T2后网络写失败。非流若先T2后写body，不能同时承诺网络交付原子性；Write/Flush成功仅是本机事实。若选择T2后某些失败自动免收，须设计唯一关联原收费的自动冲正，与双人审批售后退款分清 | 覆盖JSON完整校验失败、T2成功后写失败/客户取消、SSE部分内容、length/tool_calls正常终止、provider_failed、可信/未知usage组合；费用由版本化政策决定，不能只看HTTP200或error标记 |
| C-B05：明确零输出免收与未知成本分离 | 对平台/上游失败且按政策确定客户免收的调用，可以直接释放客户hold，供应商未知成本继续核对；无需把所有未知成本都变成客户24h冻结。部分输出或客户取消而usage未知则依已公开政策待核对，不能估算扣客户 | 同一request可同时处于客户released和成本pending；迟到供应商证据不会重新扣已释放钱包；未知成本不显示0 |
| C-B06：可信usage何时持久化 | 现稿承认最终T2失败再崩溃可能丢usage。应明确稳定usage/terminal事实的写入点：可先用短事务追加白名单计量证据，T2按其ID结算；不是逐Token落库。若数据库不可用无法保存，须记录保证边界并依未知用量政策处理，不许用普通日志替代证据 | 最终usage已保存但T2前崩溃可恢复；重复累计快照不叠加；同ID不同内容隔离核对；usage尚未保存便崩溃不伪造精确账单 |
| C-B07：恢复由持久业务状态驱动 | hold必须能从数据库扫描恢复，不能只依赖defer、内存job或进程mutex；任务通知丢失不应永久遗留冻结。prepared若要证明从未发送，需先形式化“sending提交成功后才允许网络、提交未知则不发送、状态单向前进”的不变量；不满足时保守未知。存在较早unknown attempt时，不能仅凭最后prepared就全单释放 | 强杀在T1后/attempt准备后/sending提交后/usage后/T2后；启动扫描、重复Worker、过期认领均不自动重发；期限不因恢复重置；prepared优化仅在证明条件成立时启用 |
| C-B08：保留已经补齐的outbox范围并测失败窗口 | 当前LLD已覆盖T0/T1/T2/T3、成本更正、长期最小事实和维护窗口重建，这轮不再重复旧缺口。实现时进一步固定全局event_id、业务operation与kind的唯一关系；receipt和聚合共享tx。借鉴OpenMeter事件身份，避免“先永久标记已消费，再做投影”的丢失窗口；借鉴Lago提交后通知时点，可靠投递仍由outbox承担 | 源事实提交和outbox同成败；consumer更新后未ack的崩溃不重计；删除诊断日志不影响保留期内重建；任何重放都不触发资金或上游调用 |

无需为了吸收这些机制在v0.1引入完整OpenMeter/Lago、Redis/Kafka或订阅产品。新增的主要工作是把已有PG事务、计量证据、恢复和客户计费政策写成可观察的合同，再用故障场景证明它们。

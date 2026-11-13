# 032 实施冻结记录（2026-11-14）

本记录冻结第二阶段实现合同，不改变 spec.md 的范围。实现必须逐片验证，不能把设计冻结写成能力已经交付。

## D-032-1 缓存命中计费

采用选项 A：命中与未命中按请求时冻结的零售价格及缓存最终 usage 同价结算。命中请求仍是一条 request、一份 reservation、一份 finalization、一份 settlement；没有 upstream attempt，供应商用量和成本为已知 0。finalization `source='cache'`，必须有独立 source_event_id，并受现有唯一结算和恢复约束。不得用释放（release）代替收费结算，也不得创建零金额客户账单。

`cache_hit` 只允许已经完整结算且 usage 全部 confirmed_final 的原请求提供。缺失、冲突或 unknown usage 不得进入缓存。命中结算的零售量来自缓存行，供应商量为 known zero；恢复只重读已有 finalization，不重新生成响应。

## D-032-2 缓存共享范围

同一租户内不同 API Key 可以共享。每次命中前先对目标逻辑模型执行与未命中相同的平台、租户、成员和 Key 授权检查；缓存读取不得绕过授权。缓存键包含协议、tenant_id、逻辑模型 ID、route revision 和规范化请求体 SHA-256，不包含 Key ID。不同租户、逻辑模型或路由版本绝不共享。

## 缓存物理合同

- 仅非流式 JSON，且请求和响应都不含工具调用；stream、tool_calls、function/tool 输入不缓存。
- 按逻辑模型显式开启，默认关闭；TTL 为 60 秒至 24 小时，配置随不可变模型/路由版本生效。
- 表保存规范化请求摘要、响应正文、零售最终量、原 finalization/source identity、创建和到期时间。正文只在 TTL 内存在，不进入日志、trace、报表、trace_id 或错误消息。
- 过期读取视为未命中；过期行由后台清理，清理不触碰模型请求、钱包或结算。插入与读取只产生缓存事实，不创建 attempt。
- 原请求幂等记录先于缓存查找；命中请求的幂等重放仍走原请求事实，不重复收费。

## 熔断与健康

`route_policy_versions` 追加不可变 `failure_threshold`、`open_ms`、`half_open_tokens`，范围分别为 2..20、1000..600000ms、1..4。未设置时使用原默认值 3、30000ms、1。参数只影响新请求；健康仍只在内存，重启重置。credential/binding/quota 的持久隔离沿用现有表，不用内存熔断替代。

流式响应在向下游写出任何字节前，必须先读完并校验上游首个完整事件。首事件失败按原 finalization 和 Consumption 处理；只有 SafeToRetry 为真才可换候选。首字节写出后进入 committed，禁止切换。首事件超时继续受 PROVIDER_FIRST_EVENT_TIMEOUT 和请求总 deadline 限制。

## 客户周期预算

仅作为现有 Reserve 事务内的更严准入过滤，不新建余额。窗口复用 023-B UTC 语义，读取当前窗口已结算与持有金额；超限返回 402 `budget_exceeded`，拒绝时不得产生 reservation、attempt 或上游接收。拒绝响应与控制台请求详情显示窗口已用、上限和重置时间。并发批准不得超过上限。

## 验收切片

1. 基线报告：固定 4 vCPU/8GB、数据库配额、mock 行为、三次以上重复和逐请求原始样本。
2. 故障矩阵、首事件边界、版本化熔断参数。
3. 并发周期预算、窗口切换和重启一致性。
4. cache-hit 结算、租户隔离、TTL 删除、零上游接收。
5. 全量现有商业链回归，未通过任一项不得进入 033。

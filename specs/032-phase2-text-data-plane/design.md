# 032 实施设计：文本数据面分片

2026-11-20。本文把 [032 规格](spec.md)、[实施冻结](implementation-freeze.md) 与 028 的统一计量合同落到具体接口、状态机和追加迁移。它描述本次实现目标，不把确定性 mock、局部测试或设计冻结写成真实供应商、生产或固定资源性能验收。

## 1. 共同边界

1. 只改文本六协议。媒体、任务、Realtime 不进入缓存、文本熔断参数或本文测量结论。
2. 继续由 `AuthorizedModel → provider Profile/Plan → routing.Plan → billing.Reserve → RecordAttempt/PrepareDispatch → provider Observe → RecordFinalization/Settle` 执行。缓存命中只受控省略 Attempt/Observe，不省略授权、Plan、Reserve、Finalization、Settlement、资金来源和审计。
3. 路由版本、价格、profile、quote、request/attempt 快照与 finalization 不可变；所有新 SQL 使用 `current_012` 及以后的追加文件，不改已应用 SQL。应用、worker、projection 和 payment 权限分别复核。
4. 缓存不是余额、报价、用量或供应商成本事实源。它只复制一个已结算原响应及其零售最终量；命中请求仍以自己冻结的价格和界结算。供应商没有 attempt，用量与成本均为已知零。
5. 任何恢复、清理、报表或投影不得重新生成响应或重发模型请求。未知消费不重放，`Consumption` 仍是唯一重试资格来源。

## 2. 2.1 测量基线

### 2.1.1 固定输入与报告

沿用 002 的 `measurement_version=2` 和 PERF-01..08 算法，不另造 TTFB/TTFC 公式。032 报告增加 `phase2_baseline_version=1`，至少包含：

- 精确提交/tree、构建命令与参数、应用 4 vCPU/8GB 配额、PostgreSQL/负载器/mock 各自资源与连接配额；
- deterministic mock 的候选、协议、`slow_ttfb`、`event_interval_ms`、输出量和故障配置摘要，不含秘密；
- 并发客户端数、上游实际活动流数、连接/TLS/HTTP 条件、预热与采样时长、三次以上独立重复及轮换顺序；
- 每轮与汇总的 p50/p95/p99、有效样本、错误/拒绝、goodput、实际上游 POST、CPU/RSS，以及逐请求原始样本文件 SHA-256；
- 完整商业链路声明和未覆盖的竞品、TLS、长流、慢客户端或生产边界。

逐请求额外延迟先在同一样本内扣除对应 mock 等待，再聚合；不得相减两组 p95。报告结论可为未达标。没有同条件 Bifrost/Rust LiteLLM 结果时明确写“未比较”，不得写“接近”。

### 2.1.2 正确性门禁

测量入口使用正常 release/main、正常鉴权、钱包硬预占、周期/Key 限额、attempt 持久化、最终证据、唯一结算和审计。不得增加绕过商业链路的性能开关。测量前后检查无超额收费、残留许可、未授权派发和未知消费重放。

## 3. 2.2 健康、熔断与首事件

### 3.1 不可变路由参数

`route_policy_versions` 追加：

```text
failure_threshold INTEGER NOT NULL DEFAULT 3  -- 2..20；历史版本读取为3
open_ms            INTEGER NOT NULL DEFAULT 30000 -- 1000..600000
half_open_tokens   INTEGER NOT NULL DEFAULT 1  -- 1..4
```

发布 DTO 使用可空整数表示“未设置”，持久版本保存完整有效值。一次 `routing.Plan` 只携带一个 revision 的参数；在途请求继续使用原版本，新请求读取新版本。健康仍按进程内 `channel_id + upstream_model` 隔离，重启清空 open/cooldown；credential、binding 和 quota 的持久隔离不使用该内存状态代替。

### 3.2 健康状态机

```text
closed --可归因失败达到 threshold--> open
open --open_ms 到期且有令牌--> half_open
half_open --可信成功--> closed
half_open --可归因失败--> open（重新计时）
half_open --中性结果/许可未实际使用--> half_open（归还令牌）
```

`credential/model/quota` 先执行现有持久隔离；`rate_limit` 在 channel+model 上冷却并服从有界 Retry-After；`network/upstream/timeout/protocol` 才增加熔断计数。`caller/cancelled/policy/configuration` 不计渠道故障。健康动作与 `SafeToRetry` 独立，永不因失败类或 HTTP 状态码直接授予重试。

### 3.3 流式首事件状态

```text
prepared → sending → reading_first_event → first_event_validated → committed → finished
                              │                         │
                              └─失败：未写下游字节       └─首个合法完整事件写出后禁止切换
```

在 `reading_first_event` 中必须读到完整、可解析的上游事件；TCP 分片、注释或不完整 SSE 不算。首事件是上游/协议错误时，记录原 finalization 和 Consumption，只有 `SafeToRetry=true` 且路由仍允许时才换渠道。普通 SSE error 仍为 unknown；只有不可变 provider profile 显式登记的 `no_consumption_event_codes` 才能把**首个协议事件**的机器错误码标记为 `rejected_no_consumption`，默认空集，状态码或人类消息不产生此资格。任何先前协议/内容事件之后，即使错误码相同也恢复 unknown； Responses 的 `response.error` 嵌套错误同样必须命中白名单。headers、心跳、role-only 或任意 body 字节实际写出即 `committed`，之后不得切换。首事件等待同时受 `PROVIDER_FIRST_EVENT_TIMEOUT` 和原请求总 deadline 限制，不因最后一个 attempt 取消截止。

## 4. 2.3 客户周期预算与可见性

复用 023-B 的 tenant/Key control、UTC `[start_at,end_at)`、首配下一完整周期生效、全部旧期 held 持续占用和 Reserve 同事务事件投影。准入仍是：

```text
current_window.spent + control.total_held + 本次完整 R <= limit
```

超过上限返回 HTTP 402；内部 `periodic_budget_exceeded` 映射为客户可见 `budget_exceeded`。拒绝事务不得留下 request/reservation、attempt、派发许可或上游接收。预算错误的安全扩展字段只包括 `scope`、`reason`、`window_used`、`limit_amount`、`window_reset_at`；金额仍为十进制微币字符串，时间为数据库 UTC 边界，不输出 SQL、正文或秘密。控制台直接呈现这些字段；预算页继续展示同一数据库事实，不创建第二余额。

预算拒绝发生在 Reserve 原子事务内，因此不伪造一个已接受请求或空预占来填充调用记录。客户端得到可关联的 `request_id` 时，请求详情只能读取实际存在的原请求事实；没有已接受请求时，控制台把 402 本身作为准入拒绝详情，而不是声称产生了调用/结算。窗口切换、并发与重启一致性以真实 PG 测试证明。

## 5. 2.4/2.5 精确响应缓存

### 5.1 发布配置、键与适用性

`route_policy_versions` 追加 `cache_ttl_seconds INTEGER NOT NULL DEFAULT 0`：0 为关闭，开启范围为 60..86400 秒。该值随 route revision 不可变，默认关闭。

缓存键为：

```text
SHA-256(domain || protocol || tenant_id || logical_model_id || route_revision || canonical_request)
```

数据库唯一约束直接覆盖 `(tenant_id,protocol,model_id,route_revision,request_digest)`。`canonical_request` 是严格 JSON 的规范语义输入；公开模型名归一到同一逻辑模型，Key ID、客户端幂等键、传输头和非业务 mock 接收元数据不进入键。正文里的测试 fault/nonce 指令仍是业务字节的一部分，不得删除后复用不同指令的响应。

仅当请求和响应均为非流式 JSON、双方无 tool/function 输入或调用、响应已经完整验证时适用。stream、工具、缺失/冲突/provisional usage、未结算或 released 原请求均不写入。六协议只沿各自现有严格 parser 判定，不增加通用透传。

### 5.2 PostgreSQL 缓存行

新增 `text_response_cache`，保存：

- `id/tenant_id/protocol/model_id/route_revision/request_digest`；
- 响应 JSON 的原始已验证字节、零售 `FinalQuantities` canonical/digest、finish reason；
- 原 `request_id/finalization_id/source_event_id`、schema/profile/variant 绑定；
- 数据库 `created_at/expires_at`；命中 finalization 另保存非外键 `cache_id` 供提交时 guard 重读该行，命中 request 在 `request_records.cache_request_digest` 保存 32 字节规范请求摘要。

正文是有明确 TTL 的客户内容例外，只供同租户命中读取；不写日志、trace、错误、报表或投影 outbox。读取仅选择 `expires_at > 数据库当前时刻` 的完整行，并以 `FOR SHARE` 保持到命中 finalization 提交。后台清理只 `DELETE` 过期缓存行，不触碰 request/finalization/settlement/wallet/attempt。命中 finalization 引用不可变的原 finalization，而不对缓存行建立会阻止 TTL 删除的外键；`cache_id` 只是历史定位与提交时证明，清理后不要求缓存行仍存在。

### 5.3 命中状态机与 028 受控扩展

```text
miss: 授权/Plan → Reserve → 原 Attempt → Observe → 原 Finalization → Settle → 可插入缓存
hit:  授权/Plan → Reserve → 读取有效缓存 → CacheFinalization → Settle → 返回缓存响应
```

原请求幂等事实先于缓存查找；幂等重放直接返回原请求结果，不执行第二次缓存结算。命中后、最终结算前不得派发。缓存读取/校验故障释放本次无交付预占并返回安全错误；只有明确不存在或过期才是 miss。

`finalization_records` 追加 `source/source_event_id/original_finalization_id` 的受控分支：

| 类型 | event/attempt | usage | 约束 |
| --- | --- | --- | --- |
| 现有 finalization | 必须绑定原真实 event+attempt | 从受审观察投影 | 原 028 规则不变 |
| cache finalization | `event_id=NULL`、`attempt_id=NULL`，不得创建伪 event/attempt | retail 复制原 confirmed_final；supplier 为当前 schema 完整已知零向量 | `source='cache'`、独立 source_event_id、原 finalization 非空且不可变 |

数据库 guard 必须证明：原 finalization 属于同租户/协议/逻辑模型/兼容 profile/schema，原请求已有唯一 `settled` 结算，原 retail 与 supplier usage 都 `confirmed_final`；命中 retail canonical/digest 与原最终量逐字节一致，supplier 零向量覆盖完整 supplier mask；命中 request 与缓存键、路由 revision 和原响应绑定一致。缓存 finalization 保持不可变，并沿 `request_records.latest_finalization_id` 和现有唯一 settlement 入口完成收费。

命中结算的 `raw/charged/waived` 仍由命中请求自己的原始零售价、R 和 retail final quantities 计算。没有 `upstream_attempts`、`attempt_meter_snapshots`、采购 reservation 或采购事件；请求详情与经营报表由 `source=cache` 明确得出供应商成本 known zero，而不是把“缺 attempt”默认当成 unknown 或把原成本再计一次。

### 5.4 并发、TTL 与失败边界

- 相同键并发 miss 可以各自执行一个正常上游请求；缓存 INSERT 冲突保留先提交的有效行，不覆盖正文或延长 TTL。
- 命中读取后若清理/TTL 在 finalization 前删除或过期该行，明确转为普通 miss，丢弃已读正文且不写 cache finalization；恢复重放按 source identity 返回原 finalization，即使本地新 UUID 尚未取得首次提交结果。若命中 finalization 已提交，后续结算不再依赖缓存行。
- 缓存写入发生在原唯一结算提交之后；写入失败只损失后续命中机会，不改写原响应或账务，不重发上游。
- 缓存命中结算提交后，本地响应写失败沿用现有“已结算后交付失败”政策；不自动退款或重新生成。别名共享时只重写响应公开 `model` 为本次请求名，不改变用量或缓存原字节。
- 过期清理、缓存读取和缓存写入均无模型生成权限的隐式副作用。

## 6. 验收切片与证据边界

1. 2.1：固定资源完整链路报告及三次原始样本；若环境未执行，状态只能是“测量入口已实现，固定资源未验收”。
2. 2.2：故障矩阵、每种 Consumption 的上游接收次数、首事件前后切换边界、路由版本参数隔离与真实 PG 持久隔离。
3. 2.3：并发完整 R 准入、拒绝零预占/零派发、UTC 边界、重启一致性和安全错误/UI。
4. 2.4/2.5：同价 cache-hit 唯一结算、无 attempt/known-zero 成本、授权后跨 Key 共享、跨租户/版本拒绝、TTL 删除、零新增上游接收、原请求幂等和数据库 guard 反例。
5. 最终共享商业链路必须在干净提交运行完整 029；局部 race、前端和 mock 测试不能替代该项，也不能认证真实供应商或生产。

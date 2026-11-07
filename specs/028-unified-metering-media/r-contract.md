# 028-R 第一片：实时会话与收费轮次合同

状态：**已冻结纯域边界，未实现 WebSocket、HTTP、供应商 adapter、账务接线或生产验收**。

本片只交付 `internal/realtime` 的纯 Go 状态机、短时单用 ticket 的内存边界辅助器和客户端事件边界 parser。它们不访问数据库、HTTP、wallet、billing 或 provider 网络，也不代替这些系统的事实来源。所有实际 reservation、attempt、finalization、settlement、采购成本和来源资金仍必须由现有统一计量链完成。

## 1. 身份和单一路径

一个 session 固定以下绑定：`session_id`、`tenant_id`、`actor_id`、`api_key_id`、`model_id`、`profile_id`、`route_id` 和规范化 `Origin`。连接建立后不能从客户端事件切换模型、profile、route、tenant、actor、Key 或权限。session 只有连接和轮次身份，没有可直接消费的余额，也不创建自己的钱包 journal。

实时收费仍以原请求为账务边界。每一收费 turn 必须在任何可能产生费用的输入或控制帧转发前，已经拥有外层系统创建的非空 `request_id`、`reservation_id` 和 `attempt_id`。纯域只验证这些引用存在于装配输入中；它不假装这些事实已经写入数据库，也不调用账务函数。没有这三个引用，不能进入 `reserved`，更不能进入 `sending`。

本片不复用 SSE，也不提供 Upgrade 路由。标准 API Key 鉴权和浏览器短时 ticket 是不同的 HTTP 集成入口；这里的 ticket book 只是测试和装配边界的内存模型，不是生产认证存储或签名方案。

## 2. Origin 与短时单用 ticket

ticket 绑定完整目标，不接受长期 Key、通配符或跨 Origin 重用。绑定字段为：

```text
session_id, tenant_id, actor_id, api_key_id,
model_id, profile_id, route_id, origin
```

`origin` 只能是没有 path、query、fragment、userinfo 的 `http` 或 `https` origin。规范化只降低 scheme 和 hostname；不补默认端口，不接受 `*`、`null`、尾随 path 或隐式重定向。比较使用规范化后的完整字符串。

ticket 的有效期必须大于零且不超过 2 分钟。成功 redeem 一次后立即失效；错误 tenant、actor、Key、model、profile、route 或 Origin 的尝试不改变正确绑定 ticket 的可用性，但不能绕过绑定。过期、撤销和已使用 ticket 均拒绝。ticket 不承载或暴露长期 API Key。

生产实现仍需由 HTTP 集成冻结签发、CSRF、签名/秘密保存、Origin allowlist、权限重验和跨进程存储；本片的内存 book 不构成这些验收。

## 3. Session 状态机

```text
opening -> open -> draining -> closed
```

- `opening` 只允许成功打开一次；没有打开的 session 不能开始 turn。
- `open` 才接受收费事件和新 turn。
- `draining` 停止新收费事件和新 turn，但允许集成层排空已经产生的受控输出；应取消/关闭上游并等待 join。
- `closed` 是终态；heartbeat、reconnect、provider response 都不能重新打开它。
- session 期限为 30 分钟，初始每 session 最多 100 个 turn。达到期限或背压硬界时进入 `draining`，不会自动生成旁路 turn。

纯状态机的 `Disconnect` 只完成状态收口并报告是否需要上游取消；它不执行网络取消。客户端重连必须创建新 session。原 session 的 turn、reservation 和 settlement 不会因为新 session 或 heartbeat 被复制、释放或重置。

## 4. Turn 状态机与首收费 reservation

```text
idle -> reserved -> sending -> receiving -> finalizing
                         |          |            |
                         +----------+------------+--> pending
reserved ------------------------------------------------> released
finalizing ----------------------------------------------> settled
```

实现中 `idle` 仅表示尚未创建的逻辑初态；对外可观察的 turn 从 `reserved` 开始。每个 session 同时最多一个非终态 turn。新 turn 的 `BeginTurn` 必须携带上节三个外层账务引用；它只表示首收费 reservation 已在装配层准备好，不会在纯域内扣款。

严格顺序如下：

1. `BeginTurn` 创建 `reserved` turn。
2. 至少一个已解析的收费事件被接受后，才能 `StartSending`。
3. 只有 `sending` 且已经有实际转发字节，才能 `StartReceiving`。
4. 供应商 response identity 必须绑定当前 turn；同 ID 同 turn 可幂等，换 turn 或换 ID 冲突。
5. `BeginFinalizing` 只接受已有 provider response 的 `receiving` turn。
6. `Settle` 只接受外层系统已经持久化可信 retail finalization、唯一 settlement 和 retail usage 的证明。供应商 usage 可以是 `confirmed_final`、`unknown` 或 `missing`；这不允许把 unknown 当成零成本。

`Release` 只适用于还没有转发字节的 `reserved` turn。取消、期限、断线或背压发生在已经发送后，turn 进入 `pending`，因为连接消失不证明供应商没有消费。`pending` 不允许重新发送或重新绑定 response；恢复只能查询和收尾原 turn，不能生成第二个 attempt。

`settled` 表示客户侧唯一结算事实已由外层证明；它不表示供应商成本必然已知。成功/完成输出只能在 `Settle` 成功后由集成层发布。没有可信 retail finalization 时只能发受控 pending/error，不得伪造 success/done。

## 5. 客户事件边界与去重

第一片只接受以下 gateway-owned 事件，事件 envelope 必须是严格 JSON object：

| type | `data` | 是否可能收费 | 是否需要当前 turn |
| --- | --- | ---: | ---: |
| `input.text` | `{ "text": string }` | 是 | 是 |
| `input.audio` | `{ "audio_base64": string }` | 是 | 是 |
| `response.create` | `{}` | 是 | 是 |
| `response.cancel` | `{}` | 否 | 是 |
| `heartbeat` | `{}` | 否 | 否 |
| `status` | `{}` | 否 | 否 |

envelope 形状为 `{"event_id":string,"type":string,"data":object}`，只接受已列字段，不接受重复 JSON key、未知 type、未知 data 字段、自动 response/VAD/provider 事件、`input.commit` 旁路或任意下游帧。parser 只返回 event ID、type、字节数、内容摘要和收费标记；不会把 prompt、音频或 base64 内容放进 session 状态。

`event_id` 在同一 session 内是幂等身份。同 ID 同规范内容再次出现只返回 duplicate 并关联原 turn；同 ID 内容不同返回 conflict，不能创建第二 turn。heartbeat/status 不创建 turn、不改变 reservation、不调用 provider。重复 `response.create` 也只能关联原事件，不能在 reconnect 中重发生成。

首片硬界：JSON frame 不超过 1 MiB；实现默认将 text 限制为 256 KiB、解码后 audio 限制为 512 KiB。真正 profile 可以收紧，不能扩大共享上限而不重新验收。payload 只在 parser 调用期间存在，session 只保存摘要。

## 6. 背压、期限和断线

初始纯域上限固定为：单方向待发队列最多 32 个事件、最多 4 MiB；session 累计已接受转发最多 64 MiB；每轮最多 120 秒；session 最多 30 分钟和 100 轮。队列入界失败时拒绝新输出并进入 `draining`，不丢弃已入队事件、不无限扩容、不通过旁路发送。累计转发或 frame 超界时在接受转发前拒绝，并报告需要取消；被拒绝的字节不计为已发送。

期限检查是显式纯函数调用，不启动后台 goroutine。轮次在未发送前超时可 `released`；已经发送或已收到上游内容后超时进入 `pending` 并报告需要取消。session 超时进入 `draining`。断线/取消也遵循同一规则：未转发释放，已转发 pending；关闭和 join 由 HTTP/provider 集成执行。

## 7. 验收边界

本片的验收只包括纯 Go：状态转移和非法转移、首收费引用门禁、同 session 单 inflight、event 同体去重/异体冲突、heartbeat/status 不生成 turn、provider response 绑定、settled/pending/released、Origin/ticket 单用与过期、frame/内容/队列/累计字节上限、期限和并发安全。

本片明确未验收：真实 WebSocket handshake、HTTP ticket endpoint、浏览器 CSRF/Origin allowlist、独立 LOGIN/生产权限、数据库 session/turn/event 表、真实 reservation/attempt/settlement、供应商协议与取消语义、混合音频 usage、HA/进程恢复、部署、性能以及任何真实收费。SSE 测试不能替代这些验收。

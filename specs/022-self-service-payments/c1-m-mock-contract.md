# 022-C1-M：本地退款模拟器合同

本合同冻结 C1 原路退款的本地 PSP 模拟器语义。它只扩展隔离 `mockpsp` 和其专用 schema，不开放普通 `serve`、客户 HTTP 或生产支付入口，也不产生真实现金效果。它依赖已冻结的 [C1-P 退款协议](c1-protocol-v2-contract.md)，不改变现有 v1 checkout/cancel/query 语义。第一版关系原型见 [c1-m-mock-schema.sql](c1-m-mock-schema.sql)。

## 范围

模拟器新增两类 v2 操作：

- `refund` command：受理一笔原 capture 的退款命令。
- refund query：读取同一 `command_id` 的冻结观察。

模拟器只接受已存在且已经确认的 `mock_captures`。capture 必须属于请求环境和 merchant，金额、币种、merchant order、provider transaction 和 capture revision 必须完整绑定。退款金额为正整数，单个 capture 的全部终局退款金额不得超过 capture 金额。

模拟器支持两种终局：

- `confirmed`：产生唯一 `provider_refund_reference` 和不可变 refund fact。
- `final_no_effect`：产生不可变拒绝墓碑，失败码只能是 `refund_limit_exceeded`、`refund_policy_rejected` 或 `capture_not_refundable`。

网络超时、连接断开、查询未见、进程重启和调度预算耗尽都不能自动生成 `final_no_effect`。

## 持久对象

在 mock schema 中追加以下对象，所有对象均属于 mock owner：

- `mock_refund_commands`：保存完整 canonical RefundRequest、parameter digest、环境和 capture 绑定、local refund identity、command identity、当前 generation 与状态。
- `mock_refund_queries`：按 query ID 保存完整 canonical QueryRequest 和第一次观察的 immutable snapshot。
- `mock_refund_facts`：保存唯一终局 fact；同一 local refund 只能有一个 fact。
- `mock_refund_captures`：保存 capture 级累计退款金额、revision 和原 capture 绑定。
- `mock_refund_conflicts`：保存同一身份收到异体正文、外部引用冲突或终局冲突的不可变记录。

建议使用 `environment_id,merchant_id,command_id`、`environment_id,merchant_id,local_refund_id` 和非空 `provider_refund_reference` 的唯一约束。所有金额使用 `bigint`，所有 digest 使用小写 64 hex，所有 canonical body 使用 bytea 并验证其 hash。

## 状态机

`mock_refund_commands.state` 取值：

```text
in_progress → completed
```

`completed` 必须关联唯一 `mock_refund_facts`。fact 的 outcome 决定其为 confirmed 或 final_no_effect。命令重放要求 command ID、local refund ID、parameter digest、capture digest、金额和原 capture 身份完全相同；任何异体返回冲突，不产生第二个外部效果。

同一 capture 的不同退款命令按 capture 根串行。部分退款允许存在，但所有 `confirmed` 与 `final_no_effect` 的金额累计都必须遵守原 capture 上限。不同 command 不能通过并发绕过累计约束。

## 查询快照

首次 `query_id` 处理必须在同一事务中持久化 QueryRequest 和返回快照。相同 query ID 的相同正文永远返回原快照；相同 query ID 的异体正文返回冲突。新 query ID 可以观察同一 mutation，但不能修改旧 query 的结果。

查询状态取值：

- `not_seen`：未找到 command；不产生墓碑，不释放任何资格。
- `in_progress`：command 已持久化但没有终局 fact。
- `completed`：返回唯一 immutable fact。

`not_seen`、HTTP 404、空响应、超时、无签名和普通失败都不能被模拟器或调用方解释为 `final_no_effect`。

## 控制与测试场景

控制接口继续使用独立的 `_qa/v1/commands`，扩展以下明确场景：

- `refund_confirmed`：对指定 command 生成 confirmed fact。
- `refund_final_no_effect`：对指定 command 生成固定 failure code 的最终拒绝墓碑。
- `refund_conflict`：生成与已有 command 或 capture 不一致的外部事实，必须被记录为 conflict，不改变资金事实。
- `refund_not_seen`：保持 command 未见状态，用于验证 query 不会释放或终结退款。

控制动作只能作用于已经持久化的 mock refund command，并且必须使用原 capture 和原 command 的锁序。控制结果本身不是退款 fact，不能被 API 调用方当作外部成功。

## 锁和幂等

命令路径使用：

```text
refund command identity → capture root → refund command row → accumulated capture state
```

查询路径先锁 query identity，再读取 immutable command snapshot；需要读取 command 时按相同 capture root 顺序重读。不得出现 query → command → capture 与 command → capture 的反向锁序。

外部效果在 mock 数据库中只表现为持久 fact，不执行网络请求。模拟器不能通过重新执行同一 command 来创建第二个 provider reference。数据库提交结果未知时，连接必须销毁，调用方只能使用原 command/query ID 重新观察。

## 验收要求

- 同一 command 同一正文重放返回完全相同的 fact。
- 同一 command 异体正文被拒绝。
- 同一 local refund 换 command 被拒绝。
- 同一 capture 的部分退款累计不得超过 capture 金额。
- 并发退款只能有一个符合累计限制的结果。
- confirmed fact 只有一个 provider refund reference。
- final_no_effect 生成不可变墓碑，重复 command 不会以后产生退款。
- not_seen 和 in_progress 不生成墓碑。
- query 快照按 query ID 冻结，异体 query 被拒绝。
- 同一事实重复通知不会产生第二个事实。
- 外部冲突只追加 conflict 记录，不修改原 command、capture 或 fact。
- 现有 v1 checkout/cancel/query 全量测试继续通过。
- mock schema、权限、对象摘要和架构状态同步更新。

本合同完成后，C1-S 仍需单独冻结正式 SQL、权限、Q/F 钱包桶和 proof 接线；C1-M 通过不表示正式钱包退款可用。

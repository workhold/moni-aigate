# 034.3 `refund_held` / `retired` 两桶验证

日期：2026-11-14；验收完成：2026-11-15。范围只含 034.3 的静态退款会计合同，不包含 034.3b 币价版本或 034.4 的现金退款状态机、生产写入口、HTTP、控制台、告警、申请关联、资格计算和未知提交恢复。

状态：**034.3 实现与本地验收通过。** 本记录不证明真实服务器/1Panel 部署、真实供应商、正式支付、生产对账、生产高可用或后续 034 分片已完成。

## 规格修正

原始 §17 同时要求新事件关联 `cash_refund_orders`，又把订单留给 034.4；并且 hold、retire/release 若复用订单 ID 作为 journal operation，会违反现有唯一约束。已先修订 §5.6、§5.7、§7.5、§16、§17 和 `implementation-freeze.md`：

- 034.3 安装静态 `cash_refund_orders`，应用角色只 SELECT；034.4 才开放状态机和生产写权限。
- 每个迁移使用独立非零规范 UUID 作为 journal operation；订单 ID 只作 reference 和事件外键。
- `funding_lot_events.refund_order_id` 对旧 kind 必须为 NULL，对三种新 kind 必须非空；`(refund_order_id,kind)` 唯一。
- 三种事件只允许 `paid + cash_original` 来源，retire/release 必须存在同订单、同 lot、同金额、revision 更早的 hold。
- `requested` 无事实；`approved/transfer_executed` 只有 hold；`completed` 有 hold+retire；申请前 cancelled 无事实，hold 后 cancelled/failed 有 hold+release；retire/release 不得共存。
- 034.3 不执行全部可用币计算、运营角色授权、币价读取、条件状态迁移、一来源一张未完成单、HTTP、控制台、恢复或告警。
- `unknown` 总额继续由既有资金来源摘要单列，不把金额塞进 checker 错误计数；Wallet 顶部余额构成留到 034.5。

## 已实现的会计边界

- `refund_held_amount/retired_amount` 与 `delta_refund_held/delta_retired` 默认 0，存量资金、预占、结算、消费退款和媒体操作保持原值。
- `REFUND_HELD` 为独立租户账户；AVAILABLE/HELD/REFUND_HELD 分别等于对应来源桶汇总。新桶不进入 Reserve 或消费退款。
- 五桶本金、revision、同事务事件、前缀和、账户余额和 checker 全部参与一致性判定。
- `wallet_cash_refund_order_valid` 在 deferred guard 中检查订单—事件—journal 数量、状态形状和互斥，不允许孤儿订单/journal 或独立第二事实源。
- 静态订单业务身份不可改、不可删除；应用订单 I/U/D 在角色矩阵中拒绝。
- 逐来源 API 和前端 DTO 增加 `refund_held_amount/retired_amount`，前端校验五桶守恒；顶部 Wallet 聚合未提前实现。

## 已通过的专项 PostgreSQL 用例

`TestPostgresRefundBuckets*` 在显式 `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable` 下通过，覆盖：

- `approved + hold → completed + retire`：available 960、retired 40、refund_held 0，checker 与 funding validator 平衡。
- 独立 `approved + hold → cancelled + release`：available 恢复 1000、retired 0、refund_held 0。
- B 在自身 hold 之前退休，A 已提供 refund_held：即使五桶总额可凑平，也因缺少同订单更早 hold 被拒绝。
- `wallet_opening` 非现金来源被拒绝。
- 缺 journal pointer、错误金额、重复 `(refund_order_id,kind)`、孤儿订单、孤儿 journal、额外无关账户行均被拒绝。
- 旧 reserve kind 携带非零 `delta_refund_held` 被 `wallet_event_valid` 拒绝。
- 复用 journal operation 被现有唯一约束拒绝。
- `requested` 无事实可提交，但 business identity 修改和 DELETE 被拒绝。

migration owner 仅创建静态订单 fixture；journal、事件、余额在全部 trigger/constraint 开启下写入，没有 `session_replication_role`、禁用 trigger 或 mock validator。

## 其他已通过检查

- 新旧函数 SHA-256、63 个受审函数、角色能力和 populated current migration 专项通过。
- `npm --prefix web test`、`npm --prefix web run typecheck`、`npm --prefix web run build` 通过。
- `go vet ./...` 与 `go test -run '^$' ./...` 通过。

## 待完成

全仓分组 PostgreSQL 回归、最终 vet/编译、统一架构 refresh/check、验证记录定稿、提交和推送。当前不宣称 034.3 验收完成，也不宣称现金退款业务已开放。

已完成：

- `internal/billing` 全包 race 通过，343.116s；覆盖全部旧资金操作及新增五桶 fixture。
- `internal/operations` 全包 race 通过，85.112s。
- `migrations` 全包 race 通过，73.115s。
- `internal/server` 全包 race 通过，460.068s。
- `internal/pricing` 12.304s、`internal/periodicbudget` 76.407s、`internal/payments` 54.329s、`internal/procurementprefixschema` 3.276s，均通过。
- `internal/identity` 整包曾因累计 bcrypt 时间触发 240s 超时；不降低 cost、不延长超时，改为四个交错分片，61 个测试全部通过（76.149s、103.388s、58.020s、65.073s），名单无遗漏。
- 其余包及全部脚本包均已通过，覆盖 inference、mail、maintenancetrust、media、mockpsp、paymentsandbox、全部采购子包、provider、realtime、routing、测试支持和 QA 脚本；没有新增 SKIP。

# 034.4 现金退款状态机与告警验证

日期：2026-11-15。范围只含 `cash_refund_orders` 的生产状态机、§7.3 精确金额、五桶写入、客户申请关联、HTTP、控制台、未知提交及第五种站内告警。真实银行/支付机构转账、真实服务器/1Panel 部署、正式支付、生产对账、HA/性能/灾备和 034.5/034.6 不在本片。

状态：**034.4 实现与本地验收通过。**

## 先冻结的实施补充

- `approved_at` 由数据库批准触发器写入且不可回改，是当前币价、运营设置快照和告警年龄的共同选点；034.3 静态历史由 hold journal 时间回填。
- requested 只保存资格预检与预览，不保存冻结金额；批准重新读取全部可用币，调用方不能填写币数或法币金额。
- maker/approver/transfer_executor、release/retire actor 由数据库与 journal 共同校验，单人运营只使用批准时快照。
- 客户 `requested_by` 必须来自同租户 `cash_refund/open` 申请，创建与 linked 同事务，只有终态才关闭申请。
- Go `big.Int` 与 SQL `numeric` 使用同一 §7.3 公式，版本按 `approved_at` 选择，缺版本失败关闭。
- 增加 `cash_refund_pending_aged / tenant`，复用 024-A 的 aging DTO、计数、滞回、部署与恢复边界。

## 已实现链路

- `current_009_cash_refund_workflow.sql`：一来源一张未完成单、状态/字段/角色/金额 guard、批准时间、精确 SQL 金额函数、申请关联和第五告警 kind。
- `internal/billing/cash_refund*.go`：创建、批准、执行、完成、取消、失败及 ID/business_ref 只读回查；每个迁移独立 journal operation，条件更新恰好一行，重放不重复资金事实。
- hold：`AVAILABLE -k / REFUND_HELD +k`；retire：`REFUND_HELD -k / FUNDING_CLEARING +k`；release：`REFUND_HELD -k / AVAILABLE +k`。所有事件带 `refund_order_id`，五桶 CAS 与既有 guard 共用。
- `cashRefundTransaction`：已知数据库拒绝按有限序列化重试，未知 COMMIT 返回 `cash_refund_commit_unknown`、销毁连接，不自动重放；写失败不返回成功外观订单。恢复代码与现金退款写入口无连接，COMMIT 的 08xx 连接错误也按未知处理。
- HTTP：`GET/POST /api/platform/cash-refunds`、ID/business_ref 读取及 approve/execute/complete/cancel/fail，严格白名单 JSON 和独立意图摘要。
- 独立 Vue 现金退款页：预览/冻结展示、五个迁移、业务号回查、journal/rate/凭证详情，不开放金额输入或自动外部转账。
- 第五告警：只读 `approved/transfer_executed AND approved_at<=as_of-age`，completed/cancelled/failed 恢复；无业务根锁或资金写接口。
- APP 可创建订单并更新状态列与两个退款桶；worker/projection 无订单写权限。历史 034.3/034.3b provision 保持旧订单只读和旧三桶，当前才开放新函数与写边界。

## 已通过的定向 PostgreSQL 验证

全部使用显式 `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`，没有新增 SKIP：

- `TestCashRefundFiatBigIntVectors`、`TestPostgresCashRefundFiatSQLMatchesBigInt`：折扣、独立向下取整、上调/下调、五个 int64 因子相乘不溢出、Go/SQL 一致。
- `TestPostgresCashRefundLifecycleAndReplay`：requested 预览、批准冻结、执行、完成退出、重复迁移不重复 journal/event、business_ref 回查和五桶平衡。
- `TestPostgresCashRefundCancelFailAndRequestedCancel`：approved 后取消释放、执行后失败释放、requested 无 hold 取消、同来源后续新单。
- `TestPostgresCashRefundCustomerRequestAndSingleOperatorSnapshot`：客户申请终态关闭；admin 制单/审批/执行/完成例外，事后关闭开关不影响已批准单，新单拒绝。
- `TestPostgresCashRefundEligibilityAndUSD`：Reserve 后零可用拒绝、Release 后重读成功、非现金基准拒绝、USD 无版本入账失败、显式 USD 版本折扣退款、缺当前版本 helper 失败关闭。
- `TestPostgresCashRefundConcurrentCreateApproveAndComplete`：同来源并发只有一张开放单，重复批准/完成各只一个 hold/retire。
- `TestPostgresCashRefundReserveSettleAndConsumptionRefundRaces`：Reserve/审批、结算/审批、消费退款/审批无死锁、无负可用，合法锁序结果均保持五桶守恒。
- `TestPostgresCashRefundRejectsWrongFrozenAmount`：直接 SQL 修改冻结法币在 COMMIT 前拒绝，原单不变。
- `TestPostgresCashRefundLostCommitReplyReadsBackWithoutReplay`：代理丢弃真实成功 COMMIT 回包，批准/完成均返回 unknown，原单已提交可读回，重放不增加事件。
- `TestPostgresCashRefundPendingAlertUsesApprovedAtAndRecovers`：008 合法静态历史升级 009 得到批准时间，告警只读不锁订单，取消后恢复。
- `TestPostgresCashRefundAlertSourceUpperBoundAndIndex`：批量 1001 个完整 paid/hold 图，observed_count=1001 且 lower_bound=true，另有精确 1000 且 lower_bound=false；全部订单 guard 有效，实际计划使用 `cash_refund_pending_alert` 与 `approved_at` 条件。
- `TestPaymentRoleStaticReviewedFunctionBaseline`、`TestPaymentRoles*`、`TestWalletSourcesRoles*`：68 个受审函数、历史/当前 capability、表/列/函数权限通过。

## 已通过的 HTTP 与前端验证

- `TestHTTPCashRefundLifecycleStrictInputAndReadback`：创建预览、审批角色拒绝、执行、金额不一致冲突、完成、含 `/` 业务号的编码回查、列表、未知字段与金额输入拒绝。
- `TestHTTPCashRefundCustomerRequestCancelAndFail`：客户申请关联、批准不提前关闭、取消/失败终态关闭及五桶恢复。
- `TestHTTPCashRefundPendingAlertRuleContract`：第五 kind 创建/重放、scope/参数校验与 DTO。
- `web/tests/cash-refunds.test.mjs`：requested 预览与 completed 冻结分离、JSON number/非法状态/互斥事实拒绝、业务号与动作路径、命令体禁止金额。
- `web/tests/alerts.test.mjs`：第五 kind 的 tenant aging 参数、样本和非法年龄拒绝。
- `npm --prefix web test`、`npm --prefix web run typecheck`、`npm --prefix web run build` 通过。

## 待补全仓结果

已完成的全包 race：

- `internal/billing`：420.245s。
- `internal/operations`：136.434s。
- `migrations`：160.794s。
- `internal/alerts`：51.243s，包含第五 kind 的精确 1000/1001 与索引计划。
- `internal/server`：536.675s。
- `internal/identity`：61 个测试按四个交错分片全部通过，分别为 79.863s、113.493s、65.461s、71.394s；未降低 bcrypt cost、未提高单分片 240s 限制。

其余 72 个包已以 `go test -race -p 1 -count=1 -timeout 240s` 全部通过，覆盖 inference、mail、maintenancetrust、media、mockpsp、payments、periodicbudget、pricing、procurement 全部子包、provider、realtime、routing、测试支持和 QA 脚本；与上述 billing/operations/migrations/server/identity 合计 `go list ./...` 的 77 个包，无包遗漏、无新增 SKIP。

`go vet ./...`、`go test -run '^$' ./...`、`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` 与 `go run ./scripts/check-procurement-layout` 通过。

统一架构 reviewed refresh/check 与 `make check` 已通过，最终文件清单与复核理由见 `architecture-manifest.json` 和 `docs/architecture/changes.md`。034.5 报表/余额构成、034.6 约合展示/显示名以及真实外部转账验证不能由本片代替。

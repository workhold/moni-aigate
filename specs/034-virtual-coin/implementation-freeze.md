# 034 实施冻结记录（2026-11-14）

本记录冻结范围 M（手动充值、赠送、手动现金退款、客户展示和报表基准）。范围 P/V 仍后置；不表示生产储值、正式支付或生产退款已经合格。

## 全部决策

- D-034-1：CNY 创世币价 1 币=1 CNY，存量数值不缩放。
- D-034-2：付费与赠送是独立 receipt/lot/journal，共用 parent_ref，不共用 journal operation_id。
- D-034-3：每张现金退款单独立向下取整，永不多退。
- D-034-4：同一充值单先消耗付费、后消耗赠送；赠送 `created_at=max(clock_timestamp(), 付费 created_at+1µs)`，跨单仍严格 FIFO。
- D-034-5：支持手工美元付款，但仅在数据库已有同币种 USD 币价版本时；MVP 不内置汇率。无版本必须失败关闭。`fx_note` 仅为人工说明，不参与计算。测试可用显式 fixture 创建 USD 初始版本，不增加调价 HTTP。
- D-034-6：提供带版本的“约合”展示，标注仅供参考，不进入记账、预占或退款。
- D-034-7：平台显示名可配置，默认“币”，仅改变标签。
- D-034-8：不实现余额重估；向上调整用新的赠送来源。
- D-034-9：单人运营开关默认开启，只对 admin 的制单/审批/转账例外生效，逐单快照并写 `single_operator_exception`；开关修改必须近期重新认证、二次确认和追加审计。其他角色任何情况下不可自批。
- D-034-10：币到期后置，不预留 expires_at。
- D-034-11：单笔 `amount` 与 `bonus_amount` 分别不得超过 1,000,000,000,000 微币（1,000,000 币），且二者之和不得溢出 int64；更小运营限额可另由部署配置收紧，不能放宽。
- D-034-12：现金退款只支持该来源批准时全部可用币，操作人不得指定币数；同一来源同时只能一张未完成单。
- D-034-13：退款额取 `min(按当前币价, 按原付款比例)`，批准时冻结 buy/refund rate versions 和金额，全部用 big.Int/numeric。

## 资金与类型合同

钱包、售价、Key 额度和周期预算的存量数值解释为微币，相关 currency 改为 `COIN`；付款记录使用独立 Go 类型 `FiatMoney`，不得与 `billing.Money` 隐式互赋。供应商成本与采购侧币种不在本规格修改。

`funding_receipts` 和 `funding_lots` 分别以单表 CHECK 固定 source/basis/refund_class 组合，并沿用 `wallet_lot_valid` 与 journal guard 的跨表身份、清算账户和完整性校验。`manual_bonus`、`manual_unknown` 永远不可现金退款；`manual_credit/cash_original` 才能进入现金退款。

同一充值单的付费与赠送必须在一次事务中成对产生；approved 时付费 receipt 恰一个，bonus>0 时赠送恰一个，否则 COMMIT 前拒绝。重复审批只读回并校验，不自动补建缺失来源。

## 五桶与现金退款

`available + held + consumed + refund_held + retired = issued` 是所有迁移、恢复和验证器共同守恒式；AVAILABLE/HELD/REFUND_HELD 账户分别等于对应来源桶之和。新桶不得进入 Reserve 或消费退款。`credit_bonus`、`cash_refund_hold/retire/release` 必须加入 journal kind 白名单与相应 guard。

034.3 只安装静态 `cash_refund_orders` 和五桶 guard，不开放应用写权限或现金退款入口。每次迁移使用独立 journal operation UUID，订单 ID 仅作 reference/事件外键；事件以 nullable `refund_order_id` 区分新退款事件与旧事件，并按 `(refund_order_id,kind)` 唯一，不复用订单 ID 作为多个 journal operation。034.4 才实现状态机和一来源一张未完成单索引。

现金退款严格按 `requested→approved→transfer_executed→completed`；approved 可 cancelled，approved/transfer_executed 可 failed 并释放。批准只冻结，完成才退出；transfer_order_ref 与 transfer_proof_ref 分离，paid_fiat_amount 必须等于批准冻结金额，否则只能失败释放。释放和退出互斥，未知提交只读回，不由恢复任务自动完成或重做。

审批锁序沿用 Reserve：租户→账户 ID→来源余额。必须实测 Reserve、结算、消费退款、两张退款单及重复审批/完成之间的并发，不能出现负余额、重复事件或半提交。

## 验收切片

34.1 COIN 与类型；34.2 角色、独立 `wallet_requests`、基准、成对来源和确定性 FIFO；34.3 五桶守恒；34.3b 币价版本；34.4 现金退款状态机/HTTP/控制台/告警；34.5 报表与余额构成；34.6 约合展示和显示名。每片都要求专用 PostgreSQL 全量相关回归、前端测试和架构同步，§16 的 PG 项不得 SKIP。

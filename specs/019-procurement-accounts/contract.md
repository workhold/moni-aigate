# 019 冻结实施合同

2026-11-01。本文件取代讨论稿中可变绑定、周期及锁序建议；实现和验收以此为准。先完成累计采购预算，固定周期、多币种、自动充值和正式供应商余额适配保持后续阶段。

## 身份、兼容和管理

- `procurement_accounts` 是平台域共享账户，CNY整数微元，状态仅 `active|paused`。`limit_amount/held_amount/spent_amount` 为无浮点非负整数，API均为十进制字符串；limit限制为int64非负，累计事实用NUMERIC避免溢出。revision为BIGINT规范字符串。预算低于占用允许保存，但阻新预占/许可。
- `credential_procurement_bindings` 追加不可变，credential唯一且永久归属account；id为绑定身份，另存channel_id、operation_id。换账户必须使用新凭据。绑定事务和直接SQL触发器先同步channel UPDATE，再credential，再account；不能持account返回channel。
- 未绑定credential显式为 `legacy_unmanaged`；绑定后所有渠道版本的新attempt自动受控，无关闭开关。混合池逐成员显示状态。旧prepared attempt若绑定身份变化，sending拒绝；旧已sending的无采购记录attempt继续原语义收尾，不能猜测归属补扣。
- 创建/配置/绑定命令有客户端UUID `operation_id` 和持久回执。相同ID和规范请求重取回执；不同载荷冲突。配置完整CAS：`expected_revision,label,provider,state,limit_amount`；资金字段不由客户端写。绑定输入 `channel_id,credential_id,account_id,expected_channel_revision`，绑定成功增长channel管理revision；配置revision不随流量账务变化。管理员写，平台财务只读，客户无权；写沿既有CSRF、近期再认证及恢复门禁。

## 逐attempt持久资金

- `procurement_reservations` 以attempt_id唯一，保存account_id、binding_id、input_bound、output_bound、upper_amount、held_amount、spent_amount、state、anomaly、revision；初态prepared。绑定及报价冻结后永不改历史身份。
- RecordAttempt在原客户reservation锁后插attempt、锁account，强制两维已知报价及候选quote一致，不受018 manual/allow绕过。硬上界来自已验证request input_upper_bound/output_limit，按冻结采购价合并向上取整；价格/上界缺失或上界溢出拒绝。账户active、未异常、`spent+held+upper<=limit`才同事务预占。新Key不创建额度。
- PrepareDispatch按原锁序取得attempt后锁account；全部锁后重新检查绑定、账户、预算和新DB时间的Key/客户/request截止及采购报价。成功时采购state和attempt均改sending；失败不发。报价漂移拒绝，不能自动换价或重发。
- 明确not_sent/rejected_no_consumption释放held；可信confirmed_final按冻结价转spent并释放差额；未知保留held/state=unknown。实际成本超过上界或实际usage越界仍保存完整成本，anomaly=true并暂停账户。客户账务不因此追加扣款。
- 重复证据幂等。冲突不能抹去已计成本：保留spent，并恢复至少`max(upper-spent,0)`占用，state=disputed、anomaly=true、暂停账户，等待双人证据对账；重复冲突不重复增占。
- 客户Settle/Release/Refund/24h恢复完全不释放采购占用。独立采购恢复只扫描遗留prepared/sending：已过请求期限的prepared可证明无许可而释放；sending只能转unknown保留。无网络生成能力。
- `procurement_budget_events` 追加不可变；唯一event_key、账户、attempt、held_delta、spent_delta、type、evidence_ref。每次投影变化与事件同事务，余额必须等于事件累计。不以观察余额覆盖账务。

## 对账、更正与观察

- 受控attempt不能走旧单人cost-corrections绕过。新增采购更正proposal：operation_id、attempt_id、tenant_id、amount（int64字符串）、basis=contract|invoice、evidence_ref和reason；finance_operator制单、另一仍有权限的finance_approver审批。原proposal不可改，审批唯一，只读按原operation_id查询，审批也有独立operation_id回执。
- 审批按最终供应商绝对成本，释放采购held并按新旧spent差额更新，追加provider_cost_corrections和预算事件，不改冻结报价和客户账单。提案冻结采购资金revision及语义证据数量，审批任一变化即拒绝旧提案。`procurement_evidence_identities`以attempt＋原始Usage/reason的规范SHA256唯一追加，Conflict是历史派生判定，不进入原始语义摘要；同义重复不增长，新冲突内容即使disputed同态也增长。重复已裁决证据不能清除成本或重开争议。减少spent必须invoice证据；对账不自动恢复暂停账户，管理员显式重开。未知/争议anomaly未处置时，即使账户active也阻新发；超硬界已确定成本后管理员可显式恢复，预算仍须覆盖全部占用。
- 余额观察首片为显式人工证据观察，不调用供应商：status=known|unknown|unsupported|error，amount仅known可填，currency=CNY，source、semantic、observed_at、provider_as_of、evidence_ref。不支持供应商显示unsupported；不得把人工观察标成供应商认证结果。观察追加不可变、管理员写、财务读，不参与预算准入。

## 表与锁序

账户表字段：id,label,provider,state,limit_amount,held_amount,spent_amount,revision,created_at。绑定表字段：id,credential_id,channel_id,account_id,operation_id,bound_at。采购reservation与事件字段如上。另有procurement_commands、procurement_cost_proposals、procurement_balance_observations；命令回执及事件、绑定、观察不可变，proposal只许待审→已审状态变化。新历史表从app撤UPDATE/DELETE，projector无采购读取权。DB约束保护非负余额、状态、身份引用与不可变历史。

锁序按实际现有代码：业务命令根（如需）→tenant→user/会员→Key→model→channel→credential→客户reservation→attempt→采购account→采购reservation。允许跳层，禁止反向。账户配置只取account，取得后不得访问channel/tenant；绑定从channel开始。所有涉及多个账户的路径ID排序。采购证据事务结束后才能独立做客户结算，不能持account再升级model或进入客户ledger。恢复先读/短认领，后按正常业务根处理。

## API冻结

- GET/POST `/api/platform/procurement-accounts`；GET/PUT `/:id/configuration`；GET `/:id/events`、`/:id/reservations`、`/:id/balance-observations`；POST后者保存人工证据。
- GET/POST `/api/platform/procurement-bindings`；GET `/api/platform/procurement-operations/:id`查管理命令回执。
- GET/POST `/api/platform/procurement-cost-proposals`；POST `/:id/approve`；GET `/:id`查原提案。
- 列表默认最多50条、固定游标；返回安全投影。严格JSON拒未知/重复/尾随，金额/revision禁止JSON number及非规范小数。控制台未知写仅查原operation_id，不能换ID重放；页面/身份epoch隔离晚回。

## 分工与验收

迁移014、账户/绑定/观察/回执存储及catalog池投影独占一个工作面；billing采购预占/证据/更正/恢复另一个；HTTP与UI独立接线。集成后真实PG覆盖跨渠道/跨租户同账户并发、绑定/暂停/调价锁等待、unknown与客户释放独立、重复冲突、更正双人/权限、事件平衡及直接SQL不可变；再全量race、前端、浏览器与架构复核。外部供应商、Docker/1Panel、灾备、性能对比不以fixture代验。

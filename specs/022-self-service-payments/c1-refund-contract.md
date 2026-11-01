# 022-C1：原路退款的资金与执行合同候选

2026-11-01，设计候选，尚未冻结生产 SQL、外部协议或实现入口。依赖 [C0 来源合同](c0-lot-contract.md)、[钱包序列](c0-sequence-contract.md)的全路径实际验收；当前纯分配算法通过不满足这个前置。本稿只针对本地 sandbox 原 capture，不把模拟退款称为真实返现。

## 1. 真实代码接缝和范围

现有 `billing.ApproveRefund` 是消费退款：REVENUE 退回 AVAILABLE，不能向支付商退钱。C1另建领域、命令、外部 outbox 和核验，不复用消费 refund_orders 或 financial_idempotency。当前 [paymentproto](../../internal/paymentproto/types.go)虽能解析 refund 类通知，checkout/cancel 命令及 QueryReply 并不支持退款；[mock](../../internal/mockpsp)也没有退款执行事实。不能只把通知分支改成成功便宣称退款闭环。

切片顺序为：C1-P 精确版本化退款命令/查询/证据协议 → C1-M 独立 mock 的幂等退款与查询 → C1-S 生产 SQL/权限/证明追加 → C1-I 同一账务路径的原来源冻结和收尾 → HTTP/控制台 → 真实进程/浏览器/故障验收。协议、DDL、opaque Go 接缝必须另附全文冻结后才能编码；此稿固定财务方向及不可缩减的出口。

第一片不自动向客户承诺退款、不自动退款消费失败、不支持客户自选支付目的地/银行账号、不引入手续费、外币、汇率或自动偿债。正式 PSP 需单独认证，不能由本地 mock 的 not_seen 推论正式支付商最终性。

## 2. 退款资格和守恒

退款对象只能是当前 sandbox 环境、收款主体、订单和原交易所确认的唯一 capture，且完整证明到其 C0 `sandbox_original` lot。原 capture 在 C0 cutover 前已进入 legacy opening 的，一律 nonrefundable；人工核款、券、历史余额不能凭证据附件改成可原路退。

每个 lot 在 C1追加两个桶 `payment_refund_held_amount=Q`、`payment_refunded_amount=F`；既有 P、A、H、S 含义保持：

```text
P = A + H + S + Q + F
tenant.AVAILABLE           = SUM(lot.A)
tenant.HELD                = SUM(lot.H)
tenant.PAYMENT_REFUND_HELD  = SUM(lot.Q)
对原capture：已退款额 + 尚在途冻结退款额 <= 原capture金额
```

全部累计用 NUMERIC；单笔金额为正 int64，不使用 float。P不增加，消费退款只沿原 charged 边把 S移回A，资格与 fifo_at 不变。其他 lot 的余额不能补充该支付的可退款金额。Q不属于推理HELD，Reserve、商品购买与普通消费退款不得使用它。原交易争议/扣回先触发原order风险、停止新退款批准；既有held是否能收尾须按[C3交叉候选](c3-cross-recovery-contract.md)与同原命令完整终态判断，不因单纯dispute.opened永久阻断原授权收尾。互斥退款终态或累计矛盾保留资金review；不能靠另一个支付或券平账。该交叉协议/SQL尚未冻结验收，仍不开放资金入口。

## 3. 审批与独立状态

平台 finance_operator 制单，另一位当前 finance_approver 审批；双方不能同人，审批时重新验证双方有效角色。admin不绕过财务职责。提案冻结原 order/capture/transaction/environment/payee/CNY/金额、净化 reason和 evidence_ref、制单人；evidence_ref只是平台核查引用，不具有支付认证权。附件、DSN、签名、原支付正文和客户账号不进入日志或浏览器 journal。

业务主状态与网络执行分开：

| 主状态 | 资金含义 | 允许的后继 |
| --- | --- | --- |
| proposed | 无冻结、无外部命令，不保证可退额度 | canceled、held |
| canceled | 提案终态，无资金效果 | 无 |
| held | 已冻结原 lot Q；唯一外部原命令可能未发或结果不明 | succeeded、failed_no_effect、review_required；纯调度预算耗尽仍是held |
| succeeded | 权威退款确认，Q→F | 无；后到异体事实只追加冲突/核查 |
| failed_no_effect | 权威且最终的无效果证明，Q→原 A | 无 |
| review_required | 有与本退款互斥的权威终态/身份或累计冲突，保留当时Q及原命令/证据，不代表已退钱 | 只能由C1/C2交叉合同明确授权的核查处理；普通worker不自动选赢家 |

执行 outbox 仍区分 ready/leased/submission_unknown/completed/review，不用 outbox 状态决定钱。hold时原子创建唯一 outbox。持有Q期间不能用“取消提案”或HTTP超时回A；尚未发送也须持久证明该命令从未有被授权网络机会，且与claim互斥，首片不提供这一快捷取消。proposed可由原maker取消，审批/取消竞争以同一 order→refund 锁和revision CAS裁决。

调度review与资金矛盾分开：次数/期限预算耗尽只把outbox标review并追加`execution_budget_exhausted`，主状态保持held、Q不变。其后同原授权、无冲突且完整认证的confirmed/final_no_effect仍可由证据收尾；不能因停止自动查询而永久拒绝已经到达的确定终态。原capture的dispute.opened只建立order/tenant风险，不自行将原held改成资金review_required；本退款互斥终态、错capture身份或累计异常才进入资金review_required。须在C1-S列出精确reason→允许证据/终态矩阵，不能把所有review原因混成一个可自动终结的状态。

同一个capture允许多个部分退款提案；审批时在同一个order根重新检查原lot A及外部累计上限，未批准提案不预占额度。两个独立审批不能各自使用旧余额。对已held重复审批只读取原完整资金/命令图，不重复冻结；缺边是完整性错误，不能“补发”。

## 4. 精确资金效果

| 事件 | 原 lot | CNY journal | 同事务事实 |
| --- | --- | --- | --- |
| 双审批准 | A−q、Q+q | AVAILABLE−q / PAYMENT_REFUND_HELD+q | 审批、hold source、钱包seq、lot event/cache、journal、refund outbox、audit、安全projection |
| 已认证 refund.confirmed | Q−q、F+q | PAYMENT_REFUND_HELD−q / SANDBOX_FUNDING_CLEARING+q | 唯一外部refund effect、证据selection、退款终态source、钱包seq、lot event/cache、journal、audit/projection及generation CAS |
| 最终 failed_no_effect | Q−q、A+q | PAYMENT_REFUND_HELD−q / AVAILABLE+q | 唯一no-effect证明及终态source、钱包seq、lot event/cache、journal、audit/projection及generation CAS |
| 超时/断线/not_seen尚不具最终性/冲突 | 不变 | 无资金journal | 原命令/查询/冲突与review事实，不用时间自动解冻 |

清算科目必须对应原来源：sandbox使用 SANDBOX_FUNDING_CLEARING，不能退进人工 FUNDING_CLEARING。原 credited、funding receipt、capture与原 reports-v1 不改；支付退款单独报告 proposed/held/confirmed/refunded/no_effect/review，不挤进消费 refunded 指标，也不减少原累计入账事实。钱包“可用”与“原支付可原路退”分别计算，不把后者显示为总余额。

hold/return/confirm每次均是独立不可变钱包操作并推进同一tenant seq，增加精确kind和source，不让新科目绕过C0原钱包journal反向检查。C0的所有源/lot/seq核验器、Rebuild和维护descriptor必须一起扩展；不能只给新支付worker写两条分录。初始化时现有 C0 lot 的Q/F为0，追加迁移严格核对旧图，不回写历史event或重排allocation。

## 5. 锁、时刻与事务归属

候选统一顺序：原 payment-order NO KEY UPDATE → 原 refund NO KEY UPDATE（存在时）→ 财务身份固定顺序/证据与policy → tenant SHARE → 本次全部账户按ID序 → 原lot按ID序 → 最终DB时刻/源复验 → 原子资金与终态。新提案先锁order再创建refund，不能通过首个refund行锁形成 refund→order 反序。C1/C2最终共用此order根，遇到实际隐式FK锁冲突须在SQL原型中解决后冻结。

claim/query/evidence/expiry的短事务也必须遵守这份父级顺序：可先无锁选定位键，再锁order→refund并重读精确generation，最后锁outbox/processing子行；持有子行不能回锁order。不能把“claim事务短”当作可反序的理由。C1-S必须展开全部claim和证据入口的实际SQL/隐式FK锁图，当前这一候选顺序不等于已验无死锁。

Reserve继续tenant/Key/model/controls→账户→lot，不回锁payment-order；消费退款继续原request/reservation/settlement→账户→原lot。payment-order不是来源身份的可变信任锚，身份和资格来自不可变capture/receipt/lot。资金helper不重新取得前层父锁、不Begin/Commit、不联网。外部HTTP期间不持数据库事务、账户或lot锁。

hold前在所有锁后重新确认当前C0 epoch/proof、完整capture/lot、可退A、原交易外部累计/争议、当前两人身份与开关。关闭新支付或新退款开关只挡新提案/批准；已有hold的通知/query/有利终态收尾仍按原授权，Recovery只收证据及query而不进行任何资金写或外部mutation。

账务与worker效果savepoint/错误分类沿B3：只有原明确checkedAdd溢出进入对应业务核查；缺source、角色/函数异常、CAS失败、SQL/超时不能伪装无资金效果。COMMIT未知销毁连接并返回零结果，只读原operation；GET404不是允许换ID再批准的证据。

## 6. 退款协议和mock必须另冻结的内容

现有P协议、M/Q schema版本和022不可变运行证明不原地改写。C1-P须选择显式追加版本并给出canonical DTO、签名用途域、keyset和attestation升级规则；旧capture/checkout历史仍按原版本核验，新退款必须绑定新证明及原capture身份。

必有字段包括原environment/merchant/merchant order、原provider transaction、本地refund UUID、稳定external command ID、exact amount/CNY和canonical parameter digest。命令重放同ID同body返回原不可变结果，异体冲突，不因查询次数/重启换ID。查询子ID持久不可变，绑定supervisor、原mutation generation、参数摘要及签名snapshot；拒绝把另一笔成功refund移给本提案。

本地refund UUID和provider refund reference是两个不同身份，均须在新版本中单列规范和绑定；旧v1通知的RefundID不能推断为供应商交易引用。新增退款事实必须把原capture完整身份、原命令摘要、本地refund及provider reference绑定到相同金额/CNY，查询结果不能只返一个“成功”布尔值。

mock原交易根对capture、refund及case累计串行；必须持久化唯一退款事实，保证部分累计不超原capture。确定no_effect和not_seen分别定义：not_seen仅证明该次查询可见性，只有认证的同原command同body幂等合同才可重发；被业务确定拒绝且保证以后不会生效才允许release Q。HTTP2xx、4xx、超时、空body或查询404均不能自行映射failed_no_effect。

final_no_effect必须有同原command/body的不可变拒绝墓碑或等价认证终局，连同晚到mutation也不再生效的规则；单次查询未见请求不能创建此终局。旧v1 refund.failed不具此语义。已succeeded/no_effect之后到达互斥权威事实，须追加不可变冲突并触发原order风险latch；是否及如何阻止tenant新Reserve/sending由C2共同冻结准入屏障、锁序和恢复矩阵。未冻结并验证该交叉出口前，不开放完整商业退款，而不能只记录一条review文字后允许继续使用有争议资金。

通知/query竞争只能选同一外部refund effect；同event异体、同refund异额、成功后迟到failed以及先通知后本地ACK必须保留证据、稳定durable ACK、不重复解冻/扣款。外部refund.confirmed若没有对应held或金额/原交易不匹配，持久review，不凭外部自报refund ID造新的hold或扣另一个lot。

mutation/query/evidence保留独立bounded lease/generation、共享网络槽及全局Guard；unknown预算耗尽只转执行review、主状态仍held并保留Q。不会自动把客户推理24h释放政策用于支付退款，也不会让recovery后台发模型请求。

## 7. HTTP、页面和权限出口

沿现有规范：金额/revision十进制string、规范UUID、UTC6、严格body/字段/重复JSON、Cookie/CSRF/当前身份、可选个人MFA和近期再认证。金融写与通知准入分别有界；没有C0/C1完整启动证明时不注册退款资金路由。精确DTO、operation kind/accepted/rejected回执矩阵、cursor和限制需C1 HTTP合同另列，不能由前端自行兼容当前消费退款形状。

平台operator仅制单/取消自己的未批提案，approver独立审批，当前三财务/管理读角色按冻结范围读取；tenant owner/billing_viewer只读本tenant安全状态，member不可枚举退款。客户页面不提供simulate payout或支付控制端。页面明确原支付、原申请金额、Q冻结、unknown/review、退款确认与普通消费退款的区别，绝不把“命令已提交”显示为“退款到账”。

浏览器原操作journal继续actor/scope/target互斥、事务complete后发送、128上限、跨tab和损坏namespace失败关闭。只存安全metadata；失权/离线/404保留原ID，只有精确原回执可解除未知。当前余额等于预期或GET最新refund状态相同均不是原operation已成功的证据。

worker仅新增处理原路sandbox退款所需的表/列/函数，不获得财务制单/批准、A人工/券、模型生成、任意lot选择或owner权限；projector只消费安全投影，无来源/证明/退款签名读取。所有新函数invoker、固定search_path、撤PUBLIC EXECUTE，实际LOGIN和typed OID/依赖/列级权限在监听前完整自证。

## 8. 验收矩阵和下一片冻结门槛

| 场景 | 必须证明 |
| --- | --- |
| 反套利 | 支付100已消费后券100补余额，原支付可退0；原消费退款30只恢复原支付30；legacy/manual/券不能现金化 |
| 竞争 | 同order两审批、Reserve↔审批、消费退款↔审批、通知↔query、重复/迟到证据仅一次效果；取消与审批明确赢家 |
| 原子性 | source/seq/event/cache/hold/outbox/audit/终态CAS任一点失败，原钱/lot全部回滚；同ID重读不补图 |
| 未知提交 | 实际DB成功COMMIT丢回复、外部成功丢HTTP回包、进程崩溃和重启；Q不重复冻结/释放，原command只查原事实 |
| 权威无效果 | 正常timeout/404/不具最终性的not_seen保持Q；确定no_effect才回同一lot，晚到冲突不再扣客户 |
| off/Recovery/失权 | 关闭只挡新意图；原授权query/通知继续；Recovery不mutation不入账；Guard失权/在途SIGTERM正确取消并join |
| 双向核验 | 错原transaction/环境/tenant/lot/金额、缺tail/孤journal或event、Q/F cache漂移、伪legacy例外独立检出，Rebuild不切投影 |
| 退款与拒付 | refund unknown后chargeback、外部累计超capture、逆序case/退款，保留原hold及完整review；C3交叉未验不开放完整商业售卖 |
| 角色与界面 | actual APP/worker/projector负权限；真实main+浏览器/多tab/窄屏，权限撤销和晚回不串线；fixture不替代真实PSP |
| 恢复与资源 | 旧备份前后的原交易/退款事实、离库checkpoint、1000+lot/历史、查询取消/期限与连接清理；不造调平流水 |

本稿为后续协议/SQL实施提供约束，不是已交付退款能力。C1-P/M、C1-S全文和opaque helper必须分别根审，C0全路径与维护实际通过后才允许接入资金。C2拒付/债务与C3交叉恢复仍是独立关口；正式PSP/live、生产部署及最终性能不从此稿推导。

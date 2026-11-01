# 023 验收矩阵（A已限定本地验收，B/C待执行）

2026-11-01，**A0已冻结，A1/A2/A3已实现并通过限定本地验收；B/C未冻结未实现**。A以[精确合同](a-contract.md)与[根裁决](review.md)为准；SDD原型不代表运行能力，B/C不得据A授权接通。

## A：客户分组售价

| ID | 真实边界 | 预期 |
| --- | --- | --- |
| A01 | 旧tenant/price/request迁移，默认组null，已存在价格FK | 原收费历史不变；不推断旧组；默认发布始终null，旧唯一性只由追加迁移替换 |
| A02 | 两组同model同effective、同组重复effective、0价/上界/int64 | 不同组可各有一版；同域重复拒绝；两维无损且输入计费输出为0 |
| A03 | 组价缺失、默认价存在、客户任意group header/query/body | 分组缺价fail-closed；不能由客户切组/回落默认；无模型权限仍拒绝 |
| A04 | public目录带已有Cookie、其他组ID、public default缺价 | 仍只有默认安全价；不泄漏组/采购字段，不提供跨组查询，无默认价为unavailable |
| A05 | 客户Cookie报价/Reserve同域选择（Key目录保持无价字段），未来价微秒边界 | 同域同排序；未来不抢先，生效边界确定；缺价形状完整null |
| A06 | tenant调组与Reserve并发，assignment旧CAS，状态/流量更新 | 原tenant锁线性化，request绑定唯一组/revision；独立pricing_revision不被spent/授权变更污染 |
| A07 | wallet锁等待跨组调价、retired、渠道切换 | 请求保持已选价格；最后Key/request截止照常；不重新定价 |
| A08 | retired后的旧客户调用/新分配/新发布 | 旧域选价保留；新分配/发布拒绝；不偷偷恢复默认或阻断收尾 |
| A09 | 调组后Settle/Release/Refund/Recover及重复终态 | 只按原快照一次结算/补偿，无第二资金效果、不发模型请求 |
| A10 | admin/finance/owner/member/API Key越权、直接SQL改价历史 | 只有admin管理，当前权限原子复核；历史和所属域不可改，客户不能读取他组 |
| A11 | operation重读/异体/丢失响应/404、UI身份切换 | 严格原回执；unknown只读、404不重播；晚回不覆盖新租户/编辑 |
| A12 | 实际main浏览器默认价/组价/缺价/分配确认 | 无损展示，匿名与本tenant报价清楚区分；fixture不替代真实Reserve验证 |

A实际覆盖、分包测试与浏览器/预览证据见[023-A验证](../../docs/verification/tenant-pricing.md)。矩阵不表示所有调度组合均逐一穷举。

## B：周期Key/tenant和采购预算

| ID | 真实边界 | 预期 |
| --- | --- | --- |
| B01 | UTC日/月精确边界、2月/闰年、月末及微秒 | 半开区间，月非30天；同DB时刻固定window，不采用客户端/进程时区 |
| B02 | 首次部署已有spent/held、初次启用与scheduled | 不猜旧spent；legacy未决held精确保留；下一完整周期才首启 |
| B03 | 关开/重启/无午夜worker/惰性窗口创建 | 计数不清零；关闭期间仍记新事实，重新开启原期数据继续有效 |
| B04 | 同tenant多Key/同Key并发Reserve，wallet不足/任一cap不足 | 每层按同R预占一次，资金+所有budget全有或全无；不能用新Key绕tenant cap |
| B05 | Reserve账户/control等待跨期，最终检查后deadline过界 | 归属取全部等待后DB时刻；不保存过时window，失败无预占/无生成 |
| B06 | 月末held进入新月，known/unknown、客户取消/24h释放 | 旧held计carry，新期不清；结算记原期；客户释放仅释放客户carry |
| B07 | 上期held100→新期limit100→原期结算70 | 未决时新准入0；结算后新期可100，旧spent70不改新期；UI不宣传完成时间封顶 |
| B08 | 两attempt跨期/跨采购账户，客户冻结一次 | 客户单allocation；采购逐attempt独立窗口/upper，不复制客户消费 |
| B09 | cap下调、policy关闭/开启与prepared/sending竞争 | 下调可以超占用但阻新准入/许可；sending前检查，已sending和有利收尾继续 |
| B10 | 客户消费refund、Key lifetime/周期spent、采购未知 | 钱包按原来源补偿；客户spent不恢复；采购unknown不因refund/24h/月末减少 |
| B11 | 019累计cap尚不足而周期充足，反之亦然 | 两个采购硬约束均有效；成本观察不能覆盖任一计数 |
| B12 | 采购cost确认/冲突/双审更正跨期 | 归原attempt窗，完整spent不丢；恢复保守held计carry，异常沿019暂停/人工重开 |
| B13 | 提案/证据/账户锁等待、model升级、配置交叉 | 实际全锁图无反序；不能持采购锁进入客户control/ledger；已知回滚重试不重发模型 |
| B14 | allocation/event重复、错tenant/错window/换control/计数直接改写 | DB不可变、复合归属与唯一effect保护；SUM相同的交换仍失败 |
| B15 | unknown结果、COMMIT、进程kill与恢复旧备份 | 原operation/资金事实恢复；不reset窗口、不重跑生成；原实例恢复锁存仍生效 |
| B16 | period_kind修改/删除controller/时钟回退 | 不可换calendar/删除以重置；DB时钟异常fail-closed，不重开旧窗 |
| B17 | 当前owner/member/finance/admin、跨tenant和采购读取 | 权限按合同，Key不能放宽tenant/累计预算，客户不见供应商事实 |
| B18 | Verify/Rebuild、NUMERIC累计、0限额、故障/注入回滚 | events↔allocations↔windows↔原资金逐项一致；不以SUM/报表延迟证明余额；无溢出/浮点 |
| B19 | 浏览器scheduled/disabled/UTC/carry/epoch/HTTP异常 | 清楚显示观测时刻和原期，unknown不算0；不自动写重试或用最新配置冒充回执 |

## C：必须先完成来源的期限商品

以下只是C0需细化的反例和出口，不表示C API/账务已冻结。

| ID | 前提/反例 | 预期 |
| --- | --- | --- |
| C01 | 022-C无完整lot/全路径分配证据，仅有022-A/B或UI | 不开放购买/激活/续费；不能用钱包总额代替来源 |
| C02 | 原支付100已消费，再兑券100或legacy100购买后退套餐 | 不把非现金/其他来源转成原支付可退现金；退款最多回原资格 |
| C03 | 同capture/同term重复通知、query、explicit renew并发 | capture唯一用途，term唯一grant，不重复钱包credit+免费权益 |
| C04 | period end前Reserve，end后prepared/sending/usage未知 | 未许可阻止新发送；已sending保留held和原期收尾，未决不移新term |
| C05 | 过期后release/消费refund、后台延迟/崩溃 | 回原expired桶，既不续期也不变现金；原客户24h截止不被商品worker延长 |
| C06 | purchase/allowance不等、部分消耗、多次退款取整、拒付 | 先有C0完整分录/累计价值公式；没冻结就不提供对应退款/拒付自动效果 |
| C07 | 提前续期/重叠权益/模型授权收回/商品停售 | 既有term版本和原分配保留，消耗顺序固定；不能靠商品放宽tenant/Key授权或自动调组 |
| C08 | cancel与续期支付、off、恢复模式、旧备份 | 不自动新扣款/重发grant；财务证据、权益和取消状态分别恢复、双人核查 |
| C09 | 原路refund与模型held/债务并发，来源过期 | 不取走在途held、不用券补现金；客户有利收尾继续，资金/权益/来源守恒 |
| C10 | 真实main与受限角色、完整Verify/Rebuild | 来源与expiry ledger一致，报告区分收款/发放/消费，local fixture不冒充真实支付认证 |

## 最终交付

每片源码稳定后由集成人运行相关PG race、受限角色、前端测试/strict构建、实际main与隔离浏览器；再执行仓库要求的架构正文复核、显式refresh、只读检查及适用`make check`/`make verify-mvp`。只有实际通过的范围可写已实现/已验收；所有未决取舍、外部支付/采购账单认证和生产性能继续保留门禁。

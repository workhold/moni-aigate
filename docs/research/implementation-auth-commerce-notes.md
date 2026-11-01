# 鉴权与商业闭环的实施阅读补充

2026-10-30；固定源码的定向阅读，未运行上游程序，也不是对上游全部版本的审计。对应实现是独立编写的 moni-aigate 代码、测试和界面。

| 读到的上游路径 | 可借鉴的优点 | 本项目的落地与差异 |
|---|---|---|
| New API [middleware/auth.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/middleware/auth.go) `authHelper/GetSessionAuthIdentity` | 控制台身份、API凭证、再认证身份分别表达；角色门禁集中；写操作审计兜底 | 管理端只接受HttpOnly会话；推理Key不能进入管理端。平台MFA、用户绑定的恢复码、会话家族轮换/撤销、最近再认证；密码修改现验原密码与MFA并撤销全部会话。成功敏感修改和审计同事务 |
| New API [middleware/distributor.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/middleware/distributor.go) `Distribute/TokenModelLimitAllows` | 先做模型权限、路径与渠道约束，再选择渠道；空限制不能默认放行 | 采用租户授权∩成员授权∩Key scopes，T1和每次派发事务锁父行再查最新授权；客户端tenant header、模型别名后缀不扩权 |
| New API [service/billing.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/billing.go) `PreConsumeBilling/SettleBilling` | 一个逻辑调用保留计费会话，重试不重新建立无关计费上下文；预扣与最终费用差额分开 | 缩窄到CNY钱包；硬上界预占R，持久最终量证据T1.9，再T2结算A≤R与释放R−A。账本、Key held/spent、审计、outbox同一pgx.Tx。缺失量不估算扣款；24h未结算释放 |
| One API [model/ability.go](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/ability.go) 与 [model/cache.go](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/cache.go) | group/model能力索引、优先级池、内存快照交换让选择路径清晰 | 显式模型/渠道绑定、优先级内正权重；不可变版本+revision CAS发布。权限和余额不走最终一致缓存；运行时健康/容量与配置历史分开 |
| One API [middleware/distributor.go](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/middleware/distributor.go) `SetupContextForSelectedChannel` | 统一入口与供应商模型/凭据映射隔离 | 不改写客户原始HTTP身份，独立构造上游请求；加密凭据与record/purpose绑定；主调用与probe均做DNS/IP/TLS出口验证 |
| One API [controller/relay.go](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/relay.go) `Relay/shouldRetry` | 重试、更换渠道与错误归因集中，便于经营时处理不稳定渠道 | 首版固定最多2次尝试、总300s、Retry-After最多2s；只在未发送或合同证明无消费时重试。普通5xx/消费未知不重放，SSE提交后不重试；网络Mock计数验证唯一预算 |
| One API [relay/adaptor/interface.go](https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/relay/adaptor/interface.go) | 转换、发送、响应、usage拆分，扩展供应商入口清楚 | provider包无数据库/账务依赖；首版只实现严格OpenAI文本Chat profile，未宣称继承它的全部供应商能力 |

补充读取尝试：One API `middleware/auth.go` 的固定版本网络获取超时，未将该文件算作已阅读证据。以上比较只对实际读取的文件/函数成立；不能从单一路径断言上游整个系统没有其他安全机制。

本次融合的重点是：保留统一接入与渠道经营的便利，把客户资金、供应商成本、网络交付三个事实分开。平台probe有独立成本记录、最多4并发，仍占共享100并发和同渠道许可；未知成本显示“待核对”，不会生成虚假的零成本毛利。

## 2026-10-31：批量渠道状态的定向补读

固定New API仍为`e449cc700eb569cdfc2199cb73e97de46944f43c`。重新读取[channel-router.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/router/channel-router.go#L73)的`POST /status/batch`、[controller/channel.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/controller/channel.go#L1299)的`BatchUpdateChannelStatus`及[model/channel.go](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/model/channel.go#L361)的`saveStatusState`。本地只读固定源码，未运行上游。

可借鉴的是运营一次管理多个渠道、限定可人工设置的状态、成功后更新渠道缓存，以及状态写入只允许自己的字段，防止旧快照覆盖密钥/计数/配置。该固定批量handler循环逐渠道更新并返回变更数量，没有在这段调用里建立整批事务、版本CAS或持久命令回执；这项观察不外推为上游全系统缺少相关机制。

moni-aigate的[013](../../specs/013-channel-batch-operations/spec.md)保留批量经营入口和最小状态写入思想，独立实现专用enabled事务：全部目标版本+单调管理revision、统一锁序、整批应用/拒绝、状态与原回执/唯一审计同提交。未知结果保留原ID只读查询，不自动重复执行。启停实时覆盖历史发布绑定，凭据池/route不随之改写。One API先前的能力索引/优先级池设计继续作为路由参考；本次没有将未定位到的One API批量控制器当作已读证据。

# 025 生产交付、恢复与高可用

2026-11-01，**完整生产/HA仍为独立草案，未冻结未验收**；[A0配置切片](a0-compose-config-contract.md)静态/实际CLI渲染及[A1-M邮件进程切片](a1-mail-process-contract.md)已冻结实施并完成各自限定本地验证，不改变单活模型。承接[持续交付计划](../../docs/design/delivery-plan.md)，保持能力优先、性能后置。本编号不能将容器配置文件、状态模拟或单进程重启当作生产/HA验收。

## 切片与当前事实

| 切片 | 目标 | 冻结与实施前置 |
| --- | --- | --- |
| A | 部署配置完备、隔离真实main故障与恢复演练 | 每次实施以当时最终schema/二进制为准；保留单活锁，运行器只触及专用本地资源 |
| B0 | 共享准入、执行许可与数据库fencing精确设计 | 审计全部进程内限制/缓存/worker与未来022–024路径，冻结DDL/helper/锁图及故障矩阵 |
| B1 | 多实例共享限制、许可与worker唯一事实 | B0通过真实PG双进程反例，不得通过删除advisory lock直接开放多写 |
| C | 容器/1Panel固定制品、离库备份恢复、正式适配与发行门禁 | 需要实际Docker/1Panel、独立恢复目标及正式供应商/邮件/支付条件；没有环境保留未验收 |
| D | 生产候选综合验收与运维交付 | 022–024各片实际状态汇总、受限角色、恢复、安全及正确性证据；依赖通过后进入002/008最终性能 |

当前`operations.Instance`持独立PostgreSQL session advisory lock并按请求fresh Guard；失锁永久fence，本进程取消准入，接班人等待原持久request deadline+5秒。`server`的global/tenant/Key许可、速率桶、routing健康/并发和部分worker排程仍在进程内，不能宣传多活。邮件已用DB lease/generation；其“SMTP已接受而ACK未提交”仍可能重复发送相同证明邮件，proof仅能消费一次。

## 不变量

1. 新准入失去数据库或执行所有权时fail closed；已有sending按原事实结算，unknown不自动重发模型请求。进程重启/节点接管不是允许重放生成的证据。
2. 共享限流、最大并发、渠道/凭据池容量必须跨实例成立；租约到期不能让仍可能执行的旧上游请求免费释放共享容量或重复派发。
3. 资金journal/receipt/lot、唯一结算、采购unknown、预算原期归属和外部原命令不因接管重建为新事实。DB约束是共同真相；Redis或本地缓存不可替代财务证明。
4. 恢复旧备份先进入原有recovery latch，核对离库checkpoint与恢复点后的外部收款/退款/供应商事实；不通过自动补账抹平，不在恢复模式发送新财务或模型命令。
5. 正式凭据、用户账号/MFA和客户资金不作QA素材；运行器使用随机schema/roles/ports与0600秘密文件，退出核验清理。测试钩子不得注册在普通客户HTTP或常规serve中。

详见[合同草案](contract.md)、[计划](plan.md)和[验收](acceptance.md)。本稿只补缺失设计阶段，不能作为删除单活锁或启用正式支付的授权。

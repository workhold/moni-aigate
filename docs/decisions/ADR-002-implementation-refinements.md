# ADR-002：r3 规格到 r4 实施的细化

2026-10-30，ACCEPTED（实现记录）；不改变已同意的首版商业/协议范围，不等同批准生产发布。

| 设计点 | 本次落地 | 理由与验证边界 |
|---|---|---|
| pgx+sqlc | 显式参数化pgx SQL，没有使用sqlc | 显式传递同一个pgx.Tx便于审查行锁、审计与outbox；真实PG多连接/逐写入点失败注入验证。未来可用sqlc生成只读类型查询，不改变资金事务边界 |
| 目录按细域拆包 | HTTP与生命周期编排集中在server；pricing/usage/jobs并入catalog/billing | 缩小首版模块数量，provider仍无数据库依赖；不是以空目录宣称已有能力 |
| route_policy动态预算字段 | 固定最多2 attempts、300s总时限、2s Retry-After cap | 先验证一个统一预算，不提供未验的任意策略。版本保存binding/priority/weight/容量/endpoint/credential引用 |
| 路由解释 | request保留revision，attempt可查询实际channel/version与priority/weight | 没有保存所有被过滤候选及逐项原因，AC-06解释部分未完整完成 |
| 单实例 | 专用数据库会话锁+串行ping；业务入口与派发前检查；失锁永久fence并取消上游；接管先等旧持久请求窗口 | PG后端终止、第二实例拒绝及派发前失败有测试。无法宣称与远端供应商原子互斥或exactly-once；未完成真实网络分区/SIGSTOP全矩阵 |
| 恢复门禁 | 备份脚本的恢复事务同时latch DBgate；启动读取并与env取OR，双人凭证审批后仍需重启 | 避免漏加载环境覆盖就收费。直接绕过脚本的手工pg_restore需按runbook锁存，不声称任意运维行为自动安全 |
| Probe | 已发布版本、最多4并发、共享global/channel容量、单独平台成本 | 不从客户钱包扣探测费用，不把未知成本记0；真实HTTP/PG验证 |
| 推理幂等保留 | 当前无自动清理，永久业务去重 | 不暗示已部署7日清理任务；未来缩短保留期先修规格/公开窗口 |
| 管理契约 | `specs/.../contracts/*.md`和实际handlers；尚无完整机器可读管理OpenAPI | 开发可审查契约已具备；不把Markdown叫做生成的OpenAPI |

完整资金/安全验收与部署目标仍见[执行矩阵](../verification/acceptance-execution.md)。容量50ms未通过时不会删改原门槛。

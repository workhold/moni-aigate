# 商业命令并发幂等与整仓复验

日期：2026-11-02。承接 `a874aab`，本轮包含来源纯核验核心、维护 QA 修正，以及一次由真实回归发现的命令占位修复。不是正式支付或生产环境验收。

## 实际失败与最小修复

首次 `TEST_DATABASE_URL=… make verify-mvp` 的前端 286 个测试与严格构建通过，Go race 阶段失败：

- 支付八个相同 operation 并发创建订单，部分调用返回 `payment_unavailable`。专库 PostgreSQL 日志确认 `23505` 来自 `commerce_commands_operation_id_kind_scope_actor_key`。
- 两个真实 main 用例完成业务断言，但 `DROP DATABASE … WITH (FORCE)` 在原 10 秒 cleanup 期限内未完成；复用同一过期 context 的角色清理也失败。不能据此声称全仓验收通过。

命令表同时有 operation 主键和含该 operation 的复合唯一键。原 INSERT 只指定主键作为 conflict arbiter；并发 speculative insertion 仍可由另一唯一索引报告冲突。支付与兑换券的两个命令入口均改为无 target 的 `ON CONFLICT DO NOTHING`，冲突后仍用新语句读取原 operation，核 actor、scope、kind、原指纹版本与摘要，以及当前读权和原 terminal。不同输入继续冲突；不增加任意 `23505` 重试，不放宽 5 秒总期限、3 秒锁期限或未知提交规则。

先冻结 [A 合同](../../specs/022-self-service-payments/a-domain-contract.md)和 [B 补充合同](../../specs/022-self-service-payments/b-domain-contract.md)，再修复实现。两个既有八并发用例增加共同起跑屏障，保留恰一首次结果与唯一效果等断言。迁移和复合外键保持原样。

## PostgreSQL 清理诊断与复验约束

首轮日志中，两个失败 DROP 分别遇到 41.423 秒、72.645 秒的 WAL checkpoint；随后 immediate checkpoint 分别耗时 1.992 秒、5.724 秒。没有已证实的遗留 backend 或业务锁根因。PG16 的 DROP 会请求 `IMMEDIATE|FORCE|WAIT` checkpoint；请求可能先等待既有 checkpoint，再等待新 checkpoint 完成。大量 relation 文件和同步也可能影响耗时，不能只归因为 write pacing。

现本机集群另承载开发数据库，本轮不重建、不停服，不更改共享实例的 checkpoint 参数、持久化、CI 或原测试期限。独占测试窗口前允许可见地执行一次显式 `CHECKPOINT`，记录其耗时，然后按精确名字、owner、依赖及无活动连接核对并清理本轮残留的两个随机测试数据库和四个角色。该环境整理不放入 cleanup、不重置其期限，也不是已修复所有 checkpoint 并发问题的证明。

复验使用实际测试 LOGIN、`127.0.0.1:55439/moni_test`、PG16.15、`max_connections=200`、`TimeZone=Asia/Shanghai`，三项持久化均开启。重 PostgreSQL 命令串行运行；不访问开发数据库业务内容。

## 执行记录

| 检查 | 结果 |
| --- | --- |
| 支付既有并发订单用例，race ×30 | PASS，Go 15.154 秒 |
| payments 全包，race ×1 | PASS，Go 29.823 秒 |
| 兑换券既有并发用例，race ×30 | PASS，Go 11.431 秒 |
| billing 全包，race ×1，原 600 秒 harness | PASS，Go 147.581 秒 |
| 两个失败 main 原例，race ×1 | PASS，Go 26.796 秒，命令墙钟 31.220 秒；保留原 10 秒 cleanup |
| `make check` | PASS，只读架构、全仓 vet、前端 strict 构建 |
| `TEST_DATABASE_URL=… make verify-mvp` | PASS，墙钟 959.665 秒；286 项前端、strict、全默认 Go race、vet、嵌入构建 |

复验前显式 `CHECKPOINT` 成功，墙钟 2.372 秒；确认精确 OID、owner、仅本库 ACL 依赖、无活动连接后，清理本轮两随机数据库/四角色，墙钟 0.256 秒，手工清理没有使用 FORCE。两 main 复验的 0.5 秒采样未观察到持续 active DROP 等待，不能据此排除更短等待或宣称所有 checkpoint 场景已经覆盖。未更改共享集群参数、main 测试源码或期限。

随后原仓库完整回归通过：billing 136.311 秒、fundinglotverify 2.526 秒、identity 220.948 秒、payments 28.792 秒、periodicbudget 34.865 秒、server 266.719 秒。执行期间 Go/SQL/测试源码保持冻结；汇总结果后的修改仅为文档及架构内容清单，再由根复核刷新并运行只读检查。首轮失败保留在上文，没有将其改写为成功；本轮没有对 checkpoint 行为作生产修复或 CI 配置变更。[机器记录](source-graph-integration.json)保存命令结果、耗时与本地完整日志 SHA-256，不提交包含运行材料的原始日志。

来源纯核心的范围和未接线边界见[独立记录](source-graph-cores.md)，维护 C0 opt-in 专项见[真实 PG 补验](maintenance-financial-postgres.md)。默认 `make verify-mvp` 不自动启用这些 opt-in 专项；正式支付、供应商、容器、1Panel 与 HA 仍须各自验收。

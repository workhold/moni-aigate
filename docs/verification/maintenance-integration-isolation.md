# 维护集成依赖隔离与重新验收

2026-11-02。SDD依据为[读取合同§7](../../specs/022-self-service-payments/c0-read-lease-contract.md#7-ci-启动回归后的维护集成隔离根冻结)。此前提交f572bbd的本地串行整仓成功保留原身份；[CI run 41359784155](https://github.com/workhold/moni-aigate/actions/runs/41359784155)最终失败，billing-0的SIGKILL helper、server-2/3真实main，以及other中的周期核验CLI超过原有观察期限。没有放宽10/12/60/600秒期限、五秒catalog发行预算、ReadLease30秒或资金规则。

修复在独立internal/maintenancescan中装配真实ReadLease与领域固定读取core；普通main/server/billing/PB及其race测试依赖图不再引入该包、maintenancetrust或fundinglotcatalog。领域窄query接口只产生无权威raw facts，真实维护结果仍由集成包私有adapter执行Require→core→Finish后绑定此次ScanProof；无FromFacts/FromReport/provider注入。catalog完整eager认证仍在真正维护消费者中保留。

原失败进程专项已重新通过，49.177秒退出0；原四父测试与五个计费崩溃阶段均保留。无权威core、资源guard、结果防伪造与脱敏、race生产/测试依赖门禁专项15.541秒退出0。精确日志身份与终态计数见[机器记录](maintenance-integration-isolation.json)。真实Plan/Init双reader与九个资源边界4顶层/15父子PASS，194.261秒，零skip。最终`make verify-mvp`退出0、933.969秒：前端286项，41个有测试包Go race、4个无测试包、vet及可执行文件构建通过；串行不证明跨包并发余量。固定专库后置只读观察见[审计记录](maintenance-integration-isolation-post-audit.json)，所观测fixture schema/role/DB/backend/advisory及父库其他backend均为0，不清理资源或推断不存在的前基线。精确命令、日志SHA和计数见[机器记录](maintenance-integration-isolation.json)；代码、最终集成文档和README发布后再以同一精确新commit检查CI，不沿用f572bbd失败身份。

最终集成文档、README与架构清单同步后再次执行`make check`退出0，6.187秒，SHA `71e05f23d6b497182e2a6867e34ff7a3d8641973e7ffcc22e3b9e0909b7f349c`。

提交 `824e4e54bac1fdd9597938a02125d40d8f253296` 已推送到 `codex/initial-mvp`，远端commit/tree与本地一致；GitHub API回读README字节与本地一致，SHA `041ad504ac1a32a35cb3babdb78de97392c8ff154f0d29c13c28f21670deef77`。该精确实现提交的[GitHub Actions](https://github.com/workhold/moni-aigate/actions/runs/41363556441)十个分组全部成功。机器记录保留commit、tree、README摘要和每组结论；本证据更新是后续文档提交，不覆盖实现提交的CI身份。

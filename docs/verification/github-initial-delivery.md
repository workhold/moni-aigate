# GitHub 首次源码交付验证

2026-11-02。本页记录 `workhold/moni-aigate` 首次推送候选的本地验证；远端提交与 CI 状态在推送后单独核对，不以本页代替生产验收。

## 范围

根 README 说明项目定位、能力状态、协议、原生启动、Compose/1Panel、API、MFA、安全默认、验证、SDD 和许可状态；开发/部署入口明确当前迁移为001–022。没有内置管理员密码或自动演示资金，没有变更正式支付、预览实例或生产数据库。

022-C0-T0 专用维护 Gate 的实际 PG/race 17 个顶层、53 个终态全部 PASS，54.205秒，vet通过。源码/日志摘要及精确资源清理见[专项证据](maintenance-gate.json)与[说明](maintenance-gate.md)。Gate未取得Snapshot/Permit、维护授权或资金能力。

## 全量回归发现的回调测试问题

首轮 `make verify-mvp` 在330.631秒退出2，只有 `TestPostgresCallbackRunnerTwoSlotsNoLocksDuringHTTP` 失败：3条leased、3份attempt、0条pending。原完整日志保留在 `.local/github-release-verification/make-verify-mvp-before-callback-oracle.log`，SHA-256 为 `a87556f4859479353c8184ccba7dcd8ae4e95416e6a503446ec540762e5d58c8`。该轮Go测试完成，其后的vet/build因make停止而未执行，不记为通过。

合同允许另一个ClaimDue对旧候选短暂重验时，Prepare因try-gate不可得返回ErrDeliveryDeferred并保留已计次数的lease。因此历史lease数不能直接充当HTTP在途数。按[根冻结§10.3](../../specs/022-self-service-payments/b-runtime-contract.md#103-根冻结双网络槽与prepare竞争的独立测试oracle)，只修改测试：网络两槽使用同实际LOGIN的单连接测试pool，保留真实runner/双HTTP、严格2/1/2、零业务锁和三ACK；另用标准多连接实际gate竞争独立证明deferred不更改原delivery/attempt、唯一attempt或零IO evidence。生产pool、Guard、调度、全部预算及已冻结SQL均未改变。

## 最终验证状态

修订后两个真实PG专项race各连续8次全部通过（16次执行、10.140秒），完整mockpsp包race为35个顶层/137个终态PASS（5.768秒），vet通过。独立代码复核通过；测试创建的schema/角色/backend/advisory全部归零，public与DB ACL保持不变。源码及日志SHA见[机器记录](github-initial-delivery.json)。

根最终 `make check` **6.767秒退出0**；`make verify-mvp` **339.394秒退出0**：286项前端测试、strict TS/Vite、明确专用数据库下全Go race、vet及嵌入build通过。C0完整leaf真实PG/race217.140秒和本轮T0真实PG/race54.205秒分别保留自己的专项证据，整仓命令不重新开启这些独立opt-in或真实进程运行器。各scope不互相替代，不将跳过的可选验收描述为已完成。

命令入口架构快照为1126文件/`22e62acdf9ff`。之后仅补齐验证/交付状态文档，由根再次语义复核并刷新清单、执行只读检查；最终快照与原因见[架构变更记录](../architecture/changes.md)。本轮没有升级预览实例、调用正式模型或PSP，也没有做性能对标。

## 发布候选审阅

README与开发/部署手册89个本地链接及章节锚点有效。文本/文件类型审阅未发现真实凭据、私有环境文件、数据库备份或可执行候选；64张验证截图逐图OCR和视觉复核未发现完整真实Key、密码、恢复码、TOTP材料或证明fragment。`.env`、`.local`、构建产物、node_modules均排除，两个正式expected分别约44.85MB/13.53MB，均低于GitHub单文件上限。许可仍未选定，不添加虚假开源许可或生产认证声明。

本轮架构及根README共19张Mermaid实际解析/渲染，详见[渲染记录](architecture-maintenance-gate.json)。后续仍需T1–T4、完整来源与资金链、原路退款/拒付、订阅、媒体、正式供应商及生产/HA/性能阶段。

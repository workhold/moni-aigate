# 025 持续实施计划草案

2026-11-01，完整025仍为草案；[A0静态配置窄切片](a0-compose-config-contract.md)已冻结并补齐Compose身份/SMTP透传，静态检查与隔离Compose实际渲染通过；A1-M邮件两个真实SIGKILL窗口完成限定本地验收，实际容器仍未运行。先完成022–024依赖实现，再逐片冻结其他能力；可先只读盘点和编写隔离运行器设计。

1. A0对照最终config/main/compose与部署文档，冻结默认off配置透传、运行器私有屏障、允许目标校验、PID身份和清理合同；不改评审账号/资金。
2. A1独立组件演练可并行：邮件真实kill/restart、模型sending/最终证据、支付原命令未知。[A1-G模型主进程合同](a1-model-process-contract.md)已冻结两个物理窗口；实现/实际验证见独立记录，不从合同冻结推导已验。真实main单活测试与QA运行器串行占用专用测试库，不能并发争用实例锁。
3. A2汇总所有最终schema下的运行记录、二进制摘要和清理证据；修复只影响既定行为的缺陷，并同步021等既有验收边界。
4. B0已形成[共享执行候选](b0-shared-execution-contract.md)，根接受保守unknown方向；仍须冻结共享许可/速率/epoch生产DDL/helper/角色、Key配置桥接和全局锁图，并实际验证暂停旧owner与新owner竞争。当前未开放多活。
5. B1仅在B0合同和真实PG测试通过后修改实现，双进程先使用隔离上游mock验证容量/unknown/故障，再评估开放多实例。不同资金路径共享修改由同一集成人串行合并。
6. C在环境可用时执行固定镜像Docker/1Panel、TLS长流、排空升级、离库备份/新目标恢复。缺环境记录具体未验收项目，继续其他可做工作，不编造已通过。
7. D按能力矩阵收口运维、供应商/邮件/支付正式门禁与发行许可；代码/文档汇总后同步统一架构并显式reviewed refresh。
8. 最后执行002测量合同与重新审查后的008优化。新的lot/周期/共享许可会改变Reserve/finish，原008设计必须以最终代码重校，不能直接套用旧批次；始终保留完整商业检查。

A0后续使用隔离官方Compose 5.6.1完成真实config渲染，见[证据](../../docs/verification/compose-render-025.json)。A1-M的claim后与SMTP接受/数据库ACK前两个真实SIGKILL窗口均已完成限定本地验收，原60秒lease自然恢复及资源清理见[证据](../../docs/verification/mail-lifecycle-025.md)。模型与支付其他故障点、容器/1Panel、多活及灾备继续各自门禁。

2026-11-02 A1-G独立模型fixture及真实main运行器已编码，默认纯fixture/安全边界race、vet与脚本编译通过；当前显式专库配置缺失，两个真实main窗口均未执行，opt-in明确SKIP。不得将本次编译/本地协议fixture结果当作模型真实进程恢复验收，见[本片记录](../../docs/verification/model-lifecycle-025.md)。

阶段完成不代表整个长期目标完成。生产外部门禁未满足时保持未验收状态，并明确本地可验证范围。

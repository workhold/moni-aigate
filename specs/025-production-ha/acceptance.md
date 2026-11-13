# 025 验收草案

2026-11-01，完整025尚未验收；已通过的窄片单独列明，其余保留待验。

- [x] A0配置透传静态及隔离官方Compose 5.6.1实际config渲染通过，九字段默认/合成覆盖和角色隔离见[证据](../../docs/verification/compose-render-025.json)。
- [ ] 最终生产配置/Compose容器实际运行、所有未来角色与秘密隔离仍需以最终schema整体验收。
- [x] A1-M claim后/220前与SMTP最终250后/实际FinishMail等待两个真实SIGKILL+重启窗口通过；原proof和原60秒lease自然generation2、sent清秘密，见[本片证据](../../docs/verification/mail-lifecycle-025.md)。第二窗口允许同proof重复邮件，未消费证明；single-use沿021既有单独验收。
- [ ] 模型sending和最终证据两个真实故障点无POST重放、唯一结算、无越权/超扣；客户24h与采购unknown独立。
- [ ] 022原支付/退款命令真实故障与回调/query竞争，唯一effect/receipt/lot及unknown冻结成立。
- [ ] 所有进程内限流/许可盘点完成，B0精确DDL/helper/锁图独立审查；删除单活锁前双进程共享限制与旧owner迟回反例通过。
- [ ] 数据库失联、连接池满、取消、锁等待、租约过期/重复ACK、恢复点后外部事实均按合同处理；后台没有模型生成重试入口。
- [ ] 隔离真实main与运行器退出后随机schema/roles/listeners/私密文件清理，评审账号/MFA/资金未改。
- [ ] Docker固定制品实际演练、TLS/长流/慢客户端、排空升级和版本不兼容拒启；不可用环境保留未勾选；1Panel 等面板演练为可选项。
- [ ] 新目标备份恢复、离库核对和实测RPO/RTO；funding/lot/周期/采购与原账本全部只读验证一致。
- [ ] 正式供应商/SMTP/PSP与发行许可证各自验收或明确外部门禁；mock不替代。
- [ ] 统一架构/HLD/LLD/路线图/运行手册与最终代码同步，只读架构检查、全Go PG race、前端strict及必要浏览器验收通过。
- [ ] 002/008按最终商业代码重校并进入最后性能阶段，同口径竞品未运行时不宣称接近。

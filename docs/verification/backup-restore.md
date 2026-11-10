# 备份恢复演练记录

## 2026-11-10 current-only 重跑

命令：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PG_BINDIR=/opt/homebrew/opt/postgresql@16/bin \
go run ./scripts/backup-restore-drill.go
```

结果为 PASS。脚本在源库安装并执行 `migrations.VerifyCurrent`，恢复到全新目标后再次执行 `VerifyCurrent`，实际通过 `pg_dump`、`pg_restore`、bundle SHA-256、活动服务锁拒绝、持久恢复锁、恢复模式 HTTP 闭环、非空目标拒绝和篡改 bundle 拒绝。源/恢复钱包分别为可用 875 微元、冻结 120，账本 balanced，真实构建 main 进程在环境恢复标志为 false 时仍按数据库恢复锁进入恢复模式并优雅退出。完整机器结果见 [backup-restore-local.json](backup-restore-local.json)。

本轮同时修正了演练夹具的当前权限与账务顺序。目录发布使用独立 admin 身份，客户用量按当前 `Reserve → Attempt → Observe → Settle` 写入。演练只验证当前 current-only 计划，不能作为真实供应商、Docker/1Panel 或生产 RPO/RTO 认证。

2026-10-30，本机 PostgreSQL 16.15。命令：

```bash
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PG_BINDIR=/opt/homebrew/opt/postgresql@16/bin \
go run ./scripts/backup-restore-drill.go
```

实际创建并删除两个唯一 `moni_drill_*` 数据库；没有改动 moni_dev、共享 public schema 或现有服务。使用当前迁移、真正 pg_dump/pg_restore 和伪造密钥/业务数据。

最终机器结果见 [backup-restore-local.json](backup-restore-local.json)：备份和恢复到 SQL/凭据/真实 main 进程恢复 HTTP 验证的本次耗时以机器结果为准。本轮备份 0.225s，恢复到验证并结束测试进程 1.371s。该数字仅是微型本地数据，不含容器启动、跨机传输、人工外部账单核对或生产故障切换，不是生产 RTO 承诺。

| 验证 | 实际结果 |
|---|---|
| 数据快照 | 恢复钱包可用 875 微元、冻结 120，与备份前 checkpoint 一致 |
| 账本 | balanced=true，账户差异 0，不平衡分录 0 |
| 后备份操作 | 故意在备份后再入账 100；恢复库未包含该业务，符合 snapshot 边界，证明不能声称 RPO=0 |
| 迁移 | 恢复后校验所有 SQL checksum |
| 密钥 | 从恢复材料加载 old/new keyring 和 AUTH_SECRET，历史渠道秘密解密成功；删除 old 后无法解开旧凭据 |
| 持久恢复门禁 | 004 recovery_gate 与恢复数据同事务锁存；环境标志为 false，启动组合判断仍为 recovery=true |
| HTTP | 收费生成、入账制单/审批、退款制单/审批均 503；授权钱包读取 200；拒绝操作后客户资金不变 |
| 活跃服务锁 | 持有服务 advisory lock 时，备份被拒绝 |
| 真实 main 进程 | 当前源码构建二进制，用真实 app/projection 角色启动；RECOVERY_MODE=false 仍 health=恢复模式，收费/入账均 503，SIGTERM 优雅结束 |
| 非空目标 | 再次恢复同一已有数据目标被拒绝 |
| 损坏 bundle | 修改 dump 字节后 checksum 拒绝 |

第一轮修改持久 gate 时发现 pg_dump 的 SQL 会清空 search_path，追加的非限定 gate 名称无法找到。已在追加恢复事务中显式恢复 public search_path，失败轮事务整体回滚；随后完整重跑以上结果通过，没有手工修数据或跳过校验。

operations 的真实 PG 测试还验证：提案 maker/checker 当前角色和状态、不同账号、冻结提案、旧 epoch 拒绝、账本损坏拒绝、审计故障回滚、审批重放；解除只改 gate，不会结算/释放已有冻结。脚本语法通过 `bash -n`。

未执行：Docker/1Panel 真正首次安装和版本升级、加密异地存储策略验证、实际银行/供应商回执核对、真实灾难发生点到恢复点的生产 RPO/RTO。此记录不能把这些项目标成通过。


随后加入真实 main 启动时发现，原来无条件 DrainPredecessor 会让只读恢复界面等待尚未来的 request_deadline（约300s）。实现者已调整为仅正常付费启动等待旧执行窗口；恢复进程始终关闭收费/probe/worker，批准也不自动开启，付费重启仍执行等待。本轮保留 future deadline、held120 的 fixture 重跑成功，没有修改期限或数据绕过。构建二进制在恢复计时前完成；JSON 中容器部署标志仍为 false。

一次演练的测试清理在 5s 边界主动终止 main，机器结果如实记录 graceful=false。演练客户端现使用独立、关闭 keep-alive 的 Transport，读完响应并关闭；清理上限为 10s，异常退出或超时将使演练失败并输出耗时与 Wait 结果。当前完整重跑 graceful=true，没有修改 main 的服务退出实现。

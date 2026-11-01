# MVP 故障边界验证

2026-10-30，macOS arm64、真实PostgreSQL16.15、本地HTTP上游、隔离临时schema。没有付费调用，没有生产故障开关，没有改动评审账号或钱包。

## 进程强杀与恢复

[process_failure_integration_test.go](../../internal/billing/process_failure_integration_test.go) 启动五个独立测试子进程，调用真实计费服务及本地生成POST。在子进程明确输出“事务已提交”的屏障后，父进程发送SIGKILL并确认OS退出信号。清除恢复通知、创建新Store扫描持久冻结，再重复扫描，确认无重复POST/证据/收费且钱包、Key与分录一致。

| 强杀边界 | 恢复裁决 |
|---|---|
| 预占已提交、尚无attempt | 释放，无生成POST |
| 真实POST已执行、无最终量 | 待核对，保留冻结，绝不自动重发 |
| 最终量已提交、无内容交付 | 按恢复交付政策释放 |
| 最终量已提交、已有内容交付 | 按可信量收费一次 |
| 可信量已存、财务截止已过 | 释放，不追扣 |

执行期限实际等待2秒；24h财务截止用明确时间压缩fixture验证，不宣称真实等待24h。强杀对象是测试服务子进程，不是完整main、容器或异地节点；其他进程阶段/网络分区仍在发布验收边界中。

## 结算后交付失败

[mvp_failure_integration_test.go](../../internal/server/mvp_failure_integration_test.go) 使用真实PG与上游，只控制客户ResponseWriter，在非流正文Write、SSE终态Write、终态Flush三个精确位置注入错误。故障触发时先读库确认收费已唯一提交；随后核对收费保留、交付观察为`write_failed`、POST为1、并发许可归零。

这组测试发现并修复Gin的void Flush包装层吞掉底层FlushError的问题。当前在Gin完成Write后解包到可报告错误的transport，正确记录本地flush失败；不把网络交付与资金事务描述为原子操作。

定向命令 `go test -race -count=1 -v ./internal/server ./internal/billing -run '^TestMVPPostgres'` 通过：server2.450s、billing11.143s。之后完整race回归通过；这些时长是测试执行耗时，不是请求性能。

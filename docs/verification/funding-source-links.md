# 022-C0-T3 局部资金来源关联 reader 验证

2026-11-02。本记录覆盖[来源关联冻结合同](../../specs/022-self-service-payments/c0-source-link-contract.md)中人工核款与兑换券对账收据的局部核验。实现已通过纯算法、分页 reader、ReadLease 集成包和维护 QA 包的定向 race 与 vet；真实 PostgreSQL Plan/Init 联调本轮**未运行**，不得据此宣称本片真实数据库验收完成。

## 本地定向验证

最终相关源码稳定后运行：

```sh
go test -race -count=1 ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa
go vet ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa
```

两条命令均退出0。race输出中四个包均为`ok`；耗时依次为1.622s、5.120s、10.794s、11.304s；vet退出0且无诊断。纯core用例覆盖来源关联双方孤儿/重复、租户/金额/journal/source引用不符、pending与approved状态、voucher receipt双向关系、shape与未知source kind、sandbox未覆盖计数、UTF-8/NUL、NULL与空文本、金额与资源边界、结果计数JSON及输入不变性；另验证算法在索引构造期间收到取消/超时后返回零结果。

billing reader覆盖三张表分别为0/1/127/128/129行的页边界、整页后的显式EOF页、C序游标与空文本ID、SQL/Scan/Next/Err/Close失败、乱序ID、取消/超时、100000/100001总行及16MiB字节界。还验证EOF后Rows.Close触发取消时不得返回局部成功。维护 QA 测试代码建立正常人工入账、双审兑换券发行与核销，再以真实`ScanFunding` ReadLease核对；Init分支可注入破坏后的manual receipt关系并核验负例，且比较扫描前后的资金快照。

## 真实 PostgreSQL 联调状态

`C0_MAINTENANCE_DATABASE_URL`本轮未配置。显式运行`go test -v ./scripts/maintenance-scan-qa -run '^TestMaintenanceConsumersPostgresFundedSnapshot$' -count=1`时，Plan和Init两个子测试均因该专库环境缺失而SKIP，Go命令退出0。它们没有执行Acquire、实际角色/RR/schema读取、真实EOF、扫描前后快照比较或清理审计；因此不能把该退出码称为真实PG通过。

专库重新可用后，至少执行：

```sh
go test -race -count=1 -run '^TestMaintenanceConsumersPostgresFundedSnapshot$' -v ./scripts/maintenance-scan-qa -timeout 600s
```

必须确认Plan与Init两个子项均实际PASS、没有SKIP；报告仍仅为`manual_voucher_receipt_links`。该reader不证明sandbox capture/evidence/order/effect图、完整资金来源coverage、basis、epoch、退款、生产维护CLI或资金写入。其余全仓、固定专库`make verify-mvp`、部署profile及生产环境验收仍按各自计划单独进行。


2026-11-02后续已有[21表同session新入口](funding-financial-graph.md)，另核人工/券完整来源关系及CustomerKey资金图。本文仍记录旧三表source↔receipt窄scope及其历史验收，不将新core或旧证据升级为完整FundingSources/全源proof。

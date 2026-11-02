# 分组售价并发屏障 CI 校准

2026-11-03。实现基线为 `a9a5b5971931277b4d81a44a68d8de5cf20f267b`。[该次CI](https://github.com/workhold/moni-aigate/actions/runs/41503251163) 的 billing-2 分片失败于 `TestPostgresTenantPricingAssignmentFirstReserveSeesCommittedDomain`：实际已观察到 Reserve 等待 assignment 的数据库锁边，但 `pg_stat_activity.query` 是 principal batch 尾句的 models SELECT，旧断言据此误判没有等待 tenant SHARE。其余分片与最终结论以该 run 的精确源码结果为准，不把失败改记为通过。

该实现此前的[本地受保护开账专项与整仓验证](protected-init-phase.md)真实通过：8相关包680.197秒、431顶层/3317父子PASS、零SKIP；专库 `make verify-mvp` 978.116秒、46个Go包、web286/286、vet/build通过。它们属于原实现身份，不能冒称后续测试补丁已重新跑过全部本地套件。

## 原因与冻结修正

先补[023-A合同第10节](../../specs/023-periodic-products/a-contract.md)，再修[测试观察器](../../internal/billing/tenant_pricing_concurrency_integration_test.go)。生产 Reserve、pricing、SQL、锁序、重试、8秒屏障期限及幂等合同均不变。

`pg_stat_activity` 的活动文本与 `pg_blocking_pids` 不是同一时刻的同步证据，pgx extended batch 的 Parse/Bind 尾句可进入前者。新观察器使用同一 materialized `pg_locks` 采样：绑定当前数据库、精确 tenant tableoid/ctid、Reserve 的 tuple RowShareLock 排队标记；同时要求该 backend 未获授的 transactionid ShareLock 与 assignment 已获授的 ExclusiveLock 指向同一XID，并保持实际 blocking PID 边。tuple 排队锁不等于行 SHARE 已获授，事务等待证据不能省略。actor gate 在完整屏障成立前始终保持。

该单一场景使用空可选 IdempotencyKey。默认非空键先 INSERT 的 tenant FK 会自持 KEY SHARE；PG16 在升级自己的弱锁时可以省略 heavyweight tuple 排队标记。最初仅增加 tuple 要求、未去掉该前置FK的探索确实失败9.297秒，随后按此事实修正fixture，未关闭触发器或放宽生产协议。原幂等功能与其他专项保留。

释放 actor gate 后仍逐项核新组/价格/revision、数据库保存快照、钱包守恒及禁止重试掩盖死锁。未移除断言，未把 models SQL 列入白名单，未增加睡眠代替屏障。

## 验证与身份

实际专用 `moni_test` PG16.15，根串行执行：

- 最终场景 `go test -race -json ./internal/billing -run '^TestPostgresTenantPricingAssignmentFirstReserveSeesCommittedDomain$' -count=20 -timeout 180s`：11.852秒、20/20顶层PASS、零SKIP/FAIL。
- 全部分组售价场景 `go test -race -json ./internal/billing -run '^TestPostgresTenantPricing' -count=1 -timeout 180s`：6.306秒、8/8顶层PASS、零SKIP/FAIL。
- 两轮前后测试及相关生产源码SHA一致；精确命令、摘要、探索失败与后续静态检查见[机器记录](pricing-lock-ci.json)。本补丁只变更测试与文档，后续完整默认回归由对应提交的新GitHub CI记录，不沿用旧run的成功结论。

统一架构/HLD/LLD已复核。没有运行模块、请求链路、数据关系、权限、迁移或配置变更，图的结构保持；原24图证据仍绑定原实现文档身份。本次只校准验收观察器，原保护开账的生产实现和已验事实未变。`make check` 已通过：只读架构检查、全Go vet及前端strict构建。验证记录完成后再次reviewed refresh/check，仅同步最终结果，不改变已验测试/生产源码。旧CI观察时为8组成功、billing-2失败、other仍运行；该状态不等于新补丁CI通过，最新状态以对应GitHub run为准。

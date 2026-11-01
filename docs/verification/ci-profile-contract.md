# GitHub CI 测试环境与断言修订合同

2026-11-02，根冻结后实施，仅限一次性 GitHub runner、独立 PostgreSQL 和测试断言。依据首次远端运行 [41334156647](https://github.com/workhold/moni-aigate/actions/runs/41334156647) 的实际失败日志；本合同不是修复已通过声明。

## 已证实的问题与修订

1. 旧 PB/HTTP/CLI fixture 创建无密码 LOGIN；本地专库使用 trust，镜像默认 SCRAM 导致 SQLSTATE 28P01。只为新建的 CI 测试 PostgreSQL 显式配置 trust，监听 loopback；不更改产品登录、数据库部署认证或现有本机实例。这些测试验证实际 LOGIN/OID 和权限，不宣称验证密码认证。
2. Docker bridge 的端口映射只保证客户端端口相同，服务端 `inet_server_addr()` 仍为容器地址，支付 runtime 按固定地址规则拒绝。Linux runner 使用 host network，PostgreSQL 只监听 127.0.0.1:55439；就绪检查必须实际查询 SQL endpoint/DB。保留真实端点校验，不加容器地址白名单。
3. 采购测试对 JSON UTC 与 pgx Local 的 time.Time 使用 DeepEqual，可能把同一时刻误报为预算改变。只规范化比较副本的 CreatedAt 为 UTC，并用 Equal 验证原时刻；所有金额、状态、版本及其它字段仍严格比较。
4. pg_terminate_backend(pid) 的默认返回只确认信号已发送；pool.Close 也不证明远端 backend 已退出。实例测试先保存 PID/backend_start，原有时间界内由独立连接观察该实例与随机 advisory key 消失，再只调用一次 AcquireInstance。账本 reader fixture 在原10秒清理期限内等待本次精确随机角色的连接数为0，再清理；不终止别人、不扩大权限、不改产品 retry。

## 执行与覆盖

首轮 billing/server 包累计600秒时，正在执行的测试仅4秒/0秒；堆栈处于 fixture 清理/完整迁移，不证明单个测试死锁。多个测试包重复执行全套迁移，并共享同库固定401锁。本轮将 billing、identity、server 和其余包分到四台独立 runner，各有独立数据库，保留每包原10分钟 harness 和所有业务/SQL期限、race、全测试集合。调度只改变 CI 隔离方式，不是生产并行或 HA。运行前从 `go list ./...` 构造互斥完整分区，任何包仅运行一次，空分区失败；新增包默认进入其余组。

PostgreSQL连接数显式200与本地测试配置对齐；日志没有连接耗尽证据，不把它当已证实根因。fsync、synchronous_commit、full_page_writes保持开启；不使用跳过失败测试、降低安全检查、关闭持久化或延长金融预算来通过CI。前端测试/构建、架构检查、vet及嵌入构建保留在其余组；默认C0 opt-in专项未设置，不冒称新Linux profile已通过正式C0 expected认证。

先执行修改断言相关的真实专库/race测试，再全量本地集成；推送后以新 GitHub run 的实际结果核验环境修复。保留首次失败日志摘要/SHA与新证据；未知或未完成的远端任务不得标成功。

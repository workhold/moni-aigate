# 计费事务往返优化验证

日期：2026-10-30。对应 [002 性能计划 P-04](../../specs/002-performance-parity/plan.md) 中第一步。此记录只证明本次改动的资金与事务正确性，不判定性能目标或竞品接近程度。

## 改动范围

| 位置 | 原来等待数据库结果的调用 | 本次实现 |
|---|---|---|
| 每个计费事务的本地超时设置 | 两次 `Exec` | 一条 `SELECT set_config(...)` 设置两个事务局部参数 |
| 已锁定的账本写入 | journal 一次；每账户余额更新、分录各一次 | 先验证全部余额，再按原顺序加入同一 `pgx.Batch` |
| 预占请求与 reservation 创建 | 两次 `Exec` | 一个含两条有序语句的 batch |
| 预占尾部 Key held、恢复任务、审计、outbox | 四次 `Exec` | 一个含四条有序语句的 batch |

以两个账户的预占为例，上述片段从 13 次等待结果的调用减少到 4 次。这里只比较应用层等待边界；首次预备语句和驱动执行模式仍可能影响实际网络往返，不将该数量当作测得延迟。结算、人工入账、退款也使用相同的账本 batch。

审计与 outbox 使用和其他财务路径相同的 SQL 常量及原有 JSON 字段。没有修改 schema、迁移、公开 API、数据库池大小或实例 Guard。

## 保留的边界

- 所有语句仍由原 `READ COMMITTED` 事务提交，batch 没有单独事务或提前提交。
- 原有父行锁、Key 预算排他锁、账户锁及顺序不变；授权子表仍在获父锁后的独立新语句读取。
- 金额仍使用整数、溢出检查及 AVAILABLE/HELD 非负限制。分录按原顺序写入，数据库的同事务分录约束、平衡约束和不可变历史约束保留。
- 每个 `BatchResults` 都通过 `Close()` 消费全部结果并返回错误；任意位置失败都进入原事务回滚路径。只对明确 `40P01` / `40001` 的已中止事务重试，重试次数不变。
- 上游前的持久预占、attempt 与 sending 事务边界不变，没有修改未知消费、安全第二次尝试、恢复或最终结算政策。

## 验证

真实 PostgreSQL：`127.0.0.1:55439/moni_test`，每个集成测试自行创建独立 schema、执行原迁移并在结束后删除。未使用评审或开发数据库，未调用真实模型。

执行：

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test ./internal/billing -count=1
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -race ./internal/billing -count=1
```

两项均通过；普通回归用时 5.615s，race 回归用时 6.563s。共用审计/outbox SQL 常量整理后再跑普通回归，用时 6.743s，通过。这些是测试执行时间，不是请求时延。

`TestPostgresTransactionRollback` 现覆盖请求、reservation、journal、余额更新、分录、Key held、恢复任务、审计、outbox 共九个写入位置。每个位置注入 PostgreSQL 异常，要求原数据库错误返回，钱包及所有请求、预占、账本、恢复、审计、outbox、幂等事实均与操作前相同；移除故障后同请求 ID 和幂等键能成功执行，验证 batch 结果关闭及回滚清理未留下忙连接或半提交事实。

原有并发硬预算、锁范围、账户等待后 deadline、财务边界回滚、成本/用量证据、重试边界、精确绑定隔离、对账恢复和账本平衡回归均随完整 billing 包执行通过。

## 尚待实测

首轮完成时未运行新容量基准；之后的诊断与控制实验记录在第三轮，正式容量验收由主任务统一执行。100 并发额外首内容 p95≤50ms 与 Rust LiteLLM/Bifrost 对照仍是独立未验收项。批量化缩短持锁路径的预期收益须由相同配置的完整收费链路测量确认。

## 第二轮：父锁查询批量化

同日继续优化 `lockPrincipal` 与 `lockRequestWithChannel`，仍限定计费包，未改变锁强度、状态机、提交边界和数据模型：

- `lockPrincipal` 的租户、成员、用户、Key、模型五条父锁 SQL，按原固定顺序通过一个 batch 发送并逐项扫描验证。所有父锁取得且 batch 完整关闭之后，才另发 tenant/member grant 与 billing profile block 查询。这一新语句保留 READ COMMITTED 下等待父锁后可见刚提交撤权的语义。
- `lockRequestWithChannel` 初始不可变身份查询保留；租户、成员、Key、模型、可选渠道、reservation 的五或六条锁查询改为一个有序 batch。batch 关闭后仍另发 request status/latest finalization 查询，不使用等待前证据。
- 早期扫描失败、找不到行或主体状态拒绝时都会关闭并消费剩余 batch 结果；`closeBatch` 同时保留原始扫描/业务错误与关闭时的数据库错误，由原事务回滚，不遗留忙连接。
- `RecordAttempt` 正常插入改为 `INSERT … RETURNING`，少一次结果查询。冲突导致无返回行时，独立查询已有 attempt 的身份与结果，仍严格比较 channel/version/credential/upstream model，保留原 `route_decisions` 插入与幂等语义。
- `PrepareDispatch` 尾部 attempt sending、request dispatching 两条更新按原顺序加入一个 batch，仍同成败。

这是独立 SQL 语句的有序发送，没有将父锁和权限读取拼成一个 JOIN/CTE，也没有提前读取 grant 后再等待锁。父锁查询 batch 不带财务或派发写入。

新增 `batch_integration_test.go`，通过 `pg_blocking_pids` 确认真实等待后才提交 blocker，不用固定 sleep 猜测 SQL 快照：

- 在 Reserve 和 PrepareDispatch 两条路径分别测试 tenant grant、member grant、租户/成员/用户状态、Key 撤销、模型禁用、profile block 八类变更。等待父锁期间提交变更后，调用必须拒绝，原预占保持不变且 attempt 仍为 prepared，共十六个屏障案例。
- 在 reservation 排他锁等待后验证独立新语句返回最新 request 状态。
- 验证 batch 中缺失 Key 的 `pgx.ErrNoRows` 与早期租户拒绝不会被丢失，完整回滚后同请求 ID/幂等键可恢复成功。
- 验证 INSERT RETURNING 的 attempt 幂等返回与冲突身份检查，以及派发第二条更新失败时 sending/dispatching 均回滚，修复故障后可正常派发。

第二轮普通完整计费回归通过，用时 16.868s，包含主任务新加的进程崩溃恢复案例；这不是延迟基准。

随后执行：

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -race ./internal/billing ./internal/identity ./internal/server -skip '^TestCapacityConcurrentStreams$' -count=1
```

全部通过：billing 22.765s、identity 44.010s、server 80.038s。包含完整计费与上述新增撤权屏障、身份/会话/角色检查、服务端商业协议/选路证据/派发门禁等回归，明确跳过容量实验。测试已结束，容量仍由主任务统一执行；本记录没有新增延迟达标结论。

## 第三轮：热点诊断与已回滚的 Key 共享锁实验

同日新增测试专用 [capacity_profile_test.go](../../internal/server/capacity_profile_test.go)，通过原容量 fixture 的可选挂点观测 Query、Batch、Prepare、连接池 Acquire、完整事务与 Guard；另以 2ms 间隔采样 `pg_stat_activity`，输出 mutex/block pprof。只有同时启用 `MONI_CAPACITY_TEST=true` 并指定 `MONI_CAPACITY_PROFILE` 时才挂载 tracer，未增加生产配置开关。普通控制实验不设置该参数，也不预热收费请求或 SQL。

### 诊断能支持的结论

最初剖析固定在当时仍使用串行 Ping Guard 的普通测试 binary。两次带完整观测的冷负载额外 TTFB p95 分别为 120.35ms、98.89ms；这些是有 observer 开销的诊断结果，不能代替无观测成绩。首次剖析中：

| 观测位置 | p95（ms） | 解释 |
|---|---:|---|
| Reserve 整个事务 | 36.60 | 包含父锁、财务写入及提交，不含之前的池 Acquire |
| Reserve 父锁 batch | 27.20 | 整个 batch 的等待时间，不能视作某一条 SQL 的执行时间 |
| RecordAttempt 整个事务 / 父锁 batch | 20.10 / 17.58 | 指向派发前父锁等待 |
| PrepareDispatch 整个事务 / 父锁 batch | 10.61 / 7.06 | 同上 |
| Guard 排队加 Ping，200 次调用 | 19.61 | 适用于当时串行 Guard，不能套用到后续 fresh batch Guard |
| 各类事务的 COMMIT 查询 | ≤1.164 | 该窗口的应用侧结果不支持把提交视作主要延迟来源 |

准入阶段的后端采样中，Key UPDATE 的 `transactionid/tuple` 锁等待共 702 个采样点，Key SHARE 共 89 个，账户初始化/账户锁相关等待共 39 个。这些是重复采样的活动后端数，不能当作请求数或精确锁等待毫秒数。连接池共 900 次 Acquire，累计等待 15.12s、p95 76.82ms；该统计同时包含准入和流完成后的结算，不能全部归为派发前等待。mutex profile 中串行 `Instance.Check` 累计竞争约 1.34s。综合证据将 Key 行锁竞争、被锁等待占用的池连接及当时的 Guard 排队列为继续排查方向，没有证明任一单点改动就能满足 50ms。

PrepareTracer 的计时嵌套在 Query 内；pgx batch 内部的 prepare 不会全部触发 PrepareTracer。连接首次遇到 SQL 的数量也不等于精确 prepare 数。独立 warm-only 诊断从冷实验收集固定 SQL 模板，仅在 32 个连接上 PREPARE，共 2016 次，不执行收费请求；对应额外 TTFB p95 为 133.32ms，相比那次冷诊断的 98.89ms 没有展示收益。该单次对照不能证明预热导致退化，更不能把热状态成绩用于冷负载验收。另试的无描述 `exec` 模式因 JSON 参数编码不兼容在 fixture 初始化时失败，未产生可用容量结果，未采用该模式。

tracer 的锁、分配、2ms SQL 采样与完整 mutex/block profiling 都有观测成本；batch 回调间隔只是客户端消费结果的间隔。WAL 计时当时关闭，采样的统计计数也不能提供可靠的该窗口磁盘耗时分解，因此没有作磁盘或 fsync 的归因。原诊断及 pprof 留在 `/tmp/moni-capacity-diag-*`，未作为正式验收文件归档。

### 同 source 的三对控制实验

候选改动仅让 `RecordAttempt` 的 Key 父锁使用 `FOR SHARE`，reservation 仍用 `FOR UPDATE`，Reserve、结算、释放、退款等财务路径仍用 Key `FOR UPDATE`。当时新增的真实 PostgreSQL 屏障证明不同 request 可同时持共享锁记录 attempt，资金 writer 与撤销 writer 等待这些锁；重复 attempt 仍由 reservation 串行化。实验快照的完整 billing race 通过，用时 20.447s。该正确性结果不能证明性能收益，也不能替代当前最终版本验收。

较早的旧/新 binary 含不同 Guard 实现，部分窗口还与其他 QA 重叠，其数据不用于 Key 锁改动的归因。随后在明确停止其他构建、测试和负载的窗口，固定同一份当时 source，包含 fresh batch Guard 与 004 独立保守输入/输出界。用 Go overlay **只将 RecordAttempt 调用的 `keyWrite=false` 改为 `true`**，构建 UPDATE 与 SHARE 两个普通、无 race 的测试 binary；其余代码、主机和负载相同。运行顺序为 UPDATE1、SHARE1、UPDATE2、SHARE2、UPDATE3、SHARE3。全部使用原 v2 的 100 请求、5 租户、每租户 2 Key、32 连接池、零模拟生成延迟，未启用 tracer、pprof 或 SQL/请求预热。

[控制时的四个关键源码 SHA-256](capacity-diagnostic-key-lock-control-source-sha256.json) 在控制前后保持一致；它记录实验快照，不是当前已回滚源码的摘要，也不是完整仓库哈希。归档时仅把绝对工作区路径转换为项目相对路径，摘要值保持原样。原 overlay 只改变上述一个布尔实参。

下表每格为 **额外 TTFB p95 / 额外首内容 p95**，单位 ms；链接指向未经改写的原始 JSON：

| 配对 | UPDATE | SHARE |
|---|---:|---:|
| 1 | [542.23 / 542.23](capacity-diagnostic-key-lock-control-update-run1.json) | [405.24 / 405.92](capacity-diagnostic-key-lock-control-share-run1.json) |
| 2 | [385.92 / 385.90](capacity-diagnostic-key-lock-control-update-run2.json) | [324.98 / 325.01](capacity-diagnostic-key-lock-control-share-run2.json) |
| 3 | [378.35 / 379.16](capacity-diagnostic-key-lock-control-update-run3.json) | [702.01 / 705.78](capacity-diagnostic-key-lock-control-share-run3.json) |

六轮均接受并完成 100 请求、上游 100 POST、100 笔结算，收费及 Key spent 均为 2000 微金额，钱包/Key held 均归零，账本平衡，无网络错误。数据来自独立的假模型测试 schema；归档前已核对字段与字符串，六份报告只含环境、聚合时延、资源和测试资金统计，不含 API Key、凭据、账号/租户 ID、用户正文或数据库连接串。

SHARE 前两对较低、第三对大幅升高，两种实现本身也有明显波动；这组控制没有展示稳定收益。**Key SHARE 单点已回滚，RecordAttempt 恢复 Key `FOR UPDATE`，没有保留降锁强度的生产改动。** 保留前两轮 SQL batch 优化、测试专用诊断工具及并发 duplicate attempt 的 reservation 幂等回归。没有据此继续增加第二项生产优化。

这些文件明确属于 diagnostic/control，不与旧 Guard 诊断、热缓存实验或主任务正式容量验收混用，也没有证明接近 Rust LiteLLM/Bifrost。50ms 目标仍未达成；最终值由主任务在收敛版本上重新测量。进一步 SDD 可评估有界的竞争域准入排队与池占用，或重新设计事务合并的持久证据边界，均需独立规格、正确性与性能证据，不能据本轮采样直接实施。

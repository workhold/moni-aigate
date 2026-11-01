# 本地容量测量与复现

2026-10-30，实施工作区。本页记录实际执行结果；HLD 的 **4 vCPU / 8GB、100 并发、额外首字节 p95≤50ms** 目标仍未验收。当前本地基线没有达到 50ms，也不能用本机配置替代目标环境认证。最新轮次仍用v2的首byte/首有效内容口径，见文末；此前各轮历史记录保持原样。

## 测量方法

入口为 `internal/server/capacity_test.go` 的 `TestCapacityConcurrentStreams`，默认跳过，须显式设置 `MONI_CAPACITY_TEST=true` 与 `TEST_DATABASE_URL`。使用真实 HTTP 客户端 → Gin 网关 → 本地 HTTP 模拟上游，以及真实 PostgreSQL；不调用真实模型、不接外部支付。每次建立独立 schema，退出后删除。

- 5 个租户，每租户 2 把 Key；10 并发时每 Key 1 条 / 每租户 2 条，100 并发时每 Key 10 条 / 每租户 20 条。低于 Key 16 / 租户 32 的运行时上限，每 Key 的单次突发也低于初始速率桶额度。
- 一个渠道，显式配置本轮并发容量；同一公共模型、147–148 字节请求，文本 SSE。应用数据库连接池可选 8 或 32，默认 32，运行前预热全部连接。当前测试随后真实获取独立 advisory key 的 `operations.Instance`，设置 `Server.Guard=instance.Check`；池内 1 条连接专用于实例会话，因此总池 32 对应最多 31 条事务连接。不同池大小、是否包含 Guard 的历史结果分别保存。
- 模拟上游用 barrier 等待本轮全部请求进入，确认实际同时存在 10 / 100 条上游请求，再立即发送短文本、合法 finish、最终 usage、DONE。模拟生成延迟为 0；最终 usage 固定输入 10 / 输出 5，售价每百万 token 为 1,000,000 / 2,000,000 微货币单位，每请求实收 20、预占 140。
- 逐请求记录客户端发起、上游到达、barrier 释放、客户端读到第一个正文 byte、正文结束。**额外首字节 = 客户端首 byte − 发起 − 本请求 barrier 等待**。它包含授权、目录读取、资金预占与派发准入、连接和转发；barrier 等待独立报告，不计为代理耗时。
- 另报客户端到上游准入、mock ready 到客户端首 byte、含 barrier 的整流耗时，以及本轮总时长与完成数。完成率要求最终收到 DONE；接受率按 HTTP 200 统计，拒绝按状态码和公开错误码归类。测试不人为排队请求，只会包含数据库池等实际等待。
- 每 10ms 采样本轮数据库连接的 `pg_stat_activity.wait_event_type='Lock'`、Go 内存与 goroutine；RSS 额外每 250ms 及起止点读取一次。数据库池累计获取时长是多个请求的总和，包含池等待及获取开销，不能当作单请求延迟。等待中的查询年龄包含该查询已经执行的时间，**不是精确行锁等待时长**；计数能反映竞争，10ms 采样会漏掉短等待。
- 客户端、网关、mock 共处一个 Go 测试进程，内存数字包含三者与测试观测开销；PostgreSQL 为独立本地进程。短流单轮突发只能证明本轮资源回收与资金正确性，不能证明长流、慢客户端或持续吞吐的内存边界。

测试检查接受 / DONE / 上游 POST 数 / 结算数一致、实际峰值上游并发达到设定值、每租户分布正确、收费精确、冻结清零、并发许可与 Streams 归零。性能数字只报告，不通过放宽断言宣称达标。

## 环境与优化前基线

实际环境：Apple M5 Pro，18 个逻辑 CPU，64GiB 内存，macOS Darwin 25.6.0 arm64，Go 1.27.2，`GOMAXPROCS=18`，PostgreSQL 16.15（Homebrew、独立本地进程）。没有 CPU / 内存资源配额；本轮测量未使用 `-race`。测试二进制 build info 未报告 race 标记，原始 JSON 因此保留 `race_build_setting=not_reported`，执行命令用于确认编译参数。

优化前的第二次正常编译测量保留在 [capacity-pre-optimization-pool8-concurrency100.json](capacity-pre-optimization-pool8-concurrency100.json)，时间为 `2026-10-30T04:18:20.803853Z`。它发生在缩小财务账户锁范围之前；固定保留，不用后续结果覆盖。更早的首轮额外首字节 p95 为 161.35ms，保留在本次工具执行日志；本表只引用有原始 JSON 的一次完整测量。

| 指标 | 优化前，池 8 / 并发 100 |
|---|---:|
| 接受 / DONE / 上游 POST / 结算 | 100 / 100 / 100 / 100 |
| 拒绝率 / 网络错误 | 0% / 0 |
| 上游并发峰值 / 采样网关许可峰值 | 100 / 100 |
| 额外首字节 p50 / p95 | 156.62 / **165.18ms** |
| 客户端到上游准入 p50 / p95 | 154.26 / 162.60ms |
| mock ready 到客户端首 byte p50 / p95 | 2.22 / 2.78ms |
| 本轮总时长 / 突发完成速率 | 379.54ms / 263.48 请求每秒 |
| 整流耗时 p95，含 barrier | 368.94ms |
| 排除的 barrier 等待 p50 / p95 | 8.35 / 19.50ms |
| 数据库池获取数 / 累计获取时长 | 800 / 26,055.88ms |
| 空池获取数 | 784 |
| 锁等待采样 / 总采样 | 34 / 37 |
| 最大同时锁等待连接数 | 7 |
| 等待查询的最大查询年龄 | 14.57ms |
| 进程 Go heap 起始 / 采样峰值 | 1.50 / 17.11MiB |
| 进程 RSS 起始 / 采样峰值 | 23.23 / 49.53MiB |
| 本轮累计分配 / GC 次数 | 22.19MiB / 7 |
| 收费 / 残留冻结 | 2,000 / 0 微货币单位 |

这组数据表明主要耗时集中在进入上游之前；连接池获取及锁竞争值得进一步分解。它不能单独证明某一条 SQL 是全部瓶颈，不能把 2.78ms 的响应转发段当作完整代理延迟，也不能将单轮突发完成速率写成持续吞吐。

## 锁范围调整后的当期集成状态

财务模块随后将常态操作的模型锁改为共享锁，并只锁实际会产生分录的账户。以下四轮在通知该改动的资金正确性验证通过后顺序执行；HTTP 传输观察等集成也已落地，每请求池获取由基线的 8 次变为 9 次，因此不能把两组差异全部归因于单一锁修改。

时间范围：`2026-10-30T05:03:56.563332Z` 至 `2026-10-30T05:04:00.582551Z`，硬件、Go、PostgreSQL 与编译方式同前。**这四轮 fixture 的 `Guard=nil`，尚未包含后来加入的生产实例会话检查；下面的最终测量已补齐。** 每项使用新 schema，全部接受、收到 DONE 且只发一次上游 POST；每请求收费 20，账户及 Key 冻结均归零，Key spent 合计等于实收，账本平衡，并发许可与 Streams 均归零。没有拒绝或网络错误。

| 池 / 并发 | 额外首字节 p50 / p95 | 准入到上游 p95 | mock ready 到首 byte p95 | 总时长 | Heap / RSS 采样峰值 | 最大锁等待连接 |
|---|---:|---:|---:|---:|---:|---:|
| 8 / 10 | 21.86 / 22.97ms | 22.61ms | 0.56ms | 44.82ms | 3.59 / 31.45MiB | 7 |
| 8 / 100 | 93.17 / **101.93ms** | 100.31ms | 3.03ms | 235.17ms | 16.55 / 50.48MiB | 7 |
| 32 / 10 | 19.91 / 22.33ms | 21.86ms | 0.47ms | 49.14ms | 4.32 / 31.88MiB | 5 |
| 32 / 100 | 105.53 / **115.59ms** | 114.07ms | 3.19ms | 308.95ms | 17.53 / 52.94MiB | 31 |

原始报告分别为 [池8/并发10](capacity-post-lock-optimization-pool8-concurrency10.json)、[池8/并发100](capacity-post-lock-optimization-pool8-concurrency100.json)、[池32/并发10](capacity-post-lock-optimization-pool32-concurrency10.json)、[池32/并发100](capacity-post-lock-optimization-pool32-concurrency100.json)。

池 32 / 并发 100 是与当前默认数据库池设置一致的一项，其 900 次池获取累计耗时 16,881.86ms，806 次遇到空池；30 个采样中 28 次存在锁等待，等待中的最大查询年龄为 86.18ms。池 8 / 并发 100 的对应数字为累计 19,587.88ms、881 次空池、19 / 23 次采样存在锁等待、9.37ms 最大等待查询年龄。更多连接没有在这一次观测中带来更低的 p95，不能据此把扩大连接池当作解决方案；需要进一步分解租户/Key资金锁、平台收入账户及数据库调度的贡献。

本地 100 并发 p95 仍高于 50ms。10 并发数字不能替代 100 并发目标；单轮结果也不足以给出稳定的提升比例或最优连接池结论。

## 005 之前的 Guard 测量：池 32 / 并发 100

已将容量测试改为真实获取 `operations.AcquireInstance` 并设置 `Guard=instance.Check`。每次 API 入口与实际派发前都会 ping 持有 advisory lock 的专用 PostgreSQL 会话，测试使用随机独立锁 key，退出后释放。专用会话占用总池 32 中的 1 条，最多 31 条连接可用于事务；guard 检查不经过池获取，不能仅从每请求 9 次池获取计数推断它未发生。

该阶段普通编译测量时间为 `2026-10-30T05:19:20.903022Z`，原始报告：[final-guard-pool32-concurrency100.json](final-guard-pool32-concurrency100.json)。环境与输入同前；文件名沿用当时命名，当前作为 005 之前的历史记录保留。

| 指标 | 005 之前的真实 Guard 结果 |
|---|---:|
| 接受 / DONE / 上游 POST / 结算 | 100 / 100 / 100 / 100 |
| 拒绝率 / 网络错误 | 0% / 0 |
| 额外首字节 p50 / p95 | 97.90 / **106.37ms** |
| 客户端到上游准入 p95 | 105.00ms |
| mock ready 到客户端首 byte p95 | 3.21ms |
| 本轮总时长 | 254.01ms |
| 整流耗时 p95，含 barrier | 247.82ms |
| 排除的 barrier 等待 p95 | 35.99ms |
| 池获取数 / 累计获取时长 / 空池获取数 | 900 / 12,869.03ms / 747 |
| 锁等待采样 / 总采样 | 23 / 25 |
| 最大同时锁等待连接数 / 最大等待查询年龄 | 30 / 83.55ms |
| Go heap / RSS 采样峰值 | 18.11 / 52.89MiB |
| 收费 / Key spent / 账户 held / Key held | 2,000 / 2,000 / 0 / 0 微货币单位 |
| 账本平衡 / 退出时并发许可、Streams | 是 / 均为 0 |

该阶段的本地单轮 p95 为 106.37ms；与前一轮的变化同时受数据库调度、机器负载和采样波动影响，不归因于加入 Guard，也不声称稳定提升。它尚不包含 005 的精确绑定隔离再准入检查；当前完整链路结果见下一节。

该阶段含真实 Guard 的容量测试、7 项平台探测测试与全部 gateway 纵向测试在真实 PostgreSQL 上同次通过 `-race`（3.914s）。探测测试覆盖客户与探测共享渠道容量的双向阻断、全局探测上限 4、共享全局上限、已发布版本、派发前领导权复核、真实取消关闭上游及平台审计不扣客户钱包。

## v1 完整链路：005 绑定隔离与真实 Guard

引入 005 后，`RecordAttempt` 和 `PrepareDispatch` 在共享渠道父行锁下各增加精确版本 / 模型隔离检查；候选查询也过滤已隔离绑定。保留全部正确性检查，未作新的性能优化。最新单次普通编译测量时间为 `2026-10-30T05:39:37.703531Z`，原始报告：[post-binding-isolation-guard-pool32-concurrency100.json](post-binding-isolation-guard-pool32-concurrency100.json)。

| 指标 | 最新完整链路，池 32 / 并发 100 |
|---|---:|
| 接受 / DONE / 上游 POST / 结算 | 100 / 100 / 100 / 100 |
| 拒绝率 / 网络错误 | 0% / 0 |
| 额外首字节 p50 / p95 | 114.87 / **122.57ms** |
| 客户端到上游准入 p95 | 120.29ms |
| mock ready 到客户端首 byte p95 | 3.21ms |
| 本轮总时长 / 含 barrier 的整流 p95 | 265.99 / 260.27ms |
| 排除的 barrier 等待 p95 | 42.03ms |
| 池获取数 / 累计获取时长 / 空池获取数 | 900 / 13,470.90ms / 735 |
| 锁等待采样 / 总采样 | 25 / 26 |
| 最大同时锁等待连接数 / 最大等待查询年龄 | 30 / 51.78ms |
| Go heap / RSS 采样峰值 | 18.95 / 53.31MiB |
| 收费 / Key spent / 账户 held / Key held | 2,000 / 2,000 / 0 / 0 微货币单位 |
| 账本平衡 / 退出时并发许可、Streams | 是 / 均为 0 |

该次是非独占的本机 QA 测量，容量命令运行期间另启动了短时 Telemetry / Provider / Routing `-race` 单测；其编译及进程负载可能影响调度。容量命令本身未加 `-race`，统计只来自该命令的 100 个请求；其他 QA 负载对调度的间接影响未扣除，不能以本轮和上一轮差异推断 005 的精确性能成本。Telemetry / Provider / Routing 的该次独立竞争检查分别通过（1.428s / 2.969s / 1.777s）。

**该v1完整链路本地 100 并发额外首字节 p95 为 122.57ms，50ms 目标未达到；4 vCPU / 8GB 目标环境仍未认证。** 这些数字作为可复现实验记录，不能用作对外 SLA 或稳定性能回归结论。

## 复现命令

先准备可创建 schema 的测试数据库账户。下例沿用本次隔离的本机测试库；其他环境替换 URL。报告以独占创建方式写入，已有文件会报错，避免覆盖基线。

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'

MONI_CAPACITY_TEST=true \
MONI_CAPACITY_DB_POOL=32 \
MONI_CAPACITY_CONCURRENCY=100 \
MONI_CAPACITY_REPORT="$PWD/docs/verification/capacity-new-pool32-concurrency100.json" \
go test -count=1 -timeout 45s -run '^TestCapacityConcurrentStreams$' -v ./internal/server
```

可选参数接受 `MONI_CAPACITY_DB_POOL=8|32`、`MONI_CAPACITY_CONCURRENCY=10|100`；v2新增 `MONI_CAPACITY_CONTENT_DELAY_MS=0|25`，默认0。比较时每轮更换报告名，逐轮执行，避免同时压同一个数据库。正常完整测试不会隐式执行本容量负载。

同一代码状态的四项矩阵可按以下脚本顺序执行；重跑时更换 `capacity-recheck` 前缀以保留上次数据：

```sh
for capacity_pool in 8 32; do
  for capacity_parallel in 10 100; do
    MONI_CAPACITY_TEST=true \
    MONI_CAPACITY_DB_POOL="$capacity_pool" \
    MONI_CAPACITY_CONCURRENCY="$capacity_parallel" \
    MONI_CAPACITY_REPORT="$PWD/docs/verification/capacity-recheck-pool${capacity_pool}-concurrency${capacity_parallel}.json" \
    go test -count=1 -timeout 45s -run '^TestCapacityConcurrentStreams$' -v ./internal/server
  done
done
```

独立的竞争检查使用下式；race 编译会改变延迟和内存，不能与普通编译性能结果混合。本次扩展参数前的 100 并发版本已在真实 PostgreSQL 上通过该检查（6.023s）；扩展参数及当期集成完成后，池 32 / 100 并发容量测试与全部 gateway 纵向测试同次 `-race` 通过（3.308s），未将其数字写入性能报告。

```sh
MONI_CAPACITY_TEST=true \
MONI_CAPACITY_DB_POOL=32 \
MONI_CAPACITY_CONCURRENCY=100 \
go test -race -count=1 -timeout 60s -run '^TestCapacityConcurrentStreams$' ./internal/server
```

后续须在固定 4 vCPU / 8GB、独立 PostgreSQL 容器的资源环境复测，增加持续多轮、非流、慢客户端与长流情形，保留相同口径的原始数据后再裁决 NFR-04 / AC-25。

## v2：独立观测首byte与首有效内容

2026-10-30按开发者对首token口径的追问校准测量。生产鉴权/资金/Guard/转发路径没有性能修改。mock改为先发role-only帧，再发非空文本；客户端在首byte后独立解析完整SSE事件，仅非空文本/拒绝说明或工具函数名/参数增量触发内容计时。role、heartbeat、空delta、usage、工具ID单独出现和DONE均不触发。错误/缺失DONE不计成功。报告加入 `measurement_version=2`、定义、内容样本数、额外TTFC、含mock等待的原始TTFC和p99。

`extra_first_content = client_first_content - client_start - (mock_content_emit - upstream_arrival)`，逐请求计算后取分位数；不把两个p95相减。mock从收到请求到内容发出包含barrier和可选的25ms模拟生成等待，均单独可查。这仍不是模型内部tokenizer或真实模型生成测量。

两次顺序执行，无其他测试/构建同时由本任务发起；预览服务仍运行，机器并未做OS级独占。环境沿用上述M5 Pro/18逻辑CPU/64GiB、Go1.27.2/PG16.15，无4vCPU资源配额。结果为单次短流突发，不能宣称持续吞吐或性能优化效果。

| 指标 | 10并发、内容延迟25ms | 100并发、内容延迟0ms |
|---|---:|---:|
| 时间UTC | 06:03:19 | 06:06:31 |
| 接受 / 内容样本 / DONE / POST / 结算 | 10 / 10 / 10 / 10 / 10 | 100 / 100 / 100 / 100 / 100 |
| 额外TTFB p50 / p95 | 25.83 / 28.71ms | 109.08 / **119.54ms** |
| 额外首有效内容 p50 / p95 | 25.83 / 28.69ms | 109.47 / **119.60ms** |
| 客户端首byte至首有效内容 p50 / p95 | 25.40 / 25.50ms | 0.007 / 0.325ms |
| 含mock等待的客户端TTFC p95 | 54.35ms | 124.82ms |
| mock内容发出至客户端有效内容 p95 | 0.45ms | 3.11ms |
| 账本平衡 / 账户与Key残留held / 许可 | 是 / 0 / 0 | 是 / 0 / 0 |

25ms延迟样本确认role前帧没有被当成内容计时；10样本的p99等于最大值，只是诊断。100并发仍高于50ms，并未运行Rust LiteLLM/Bifrost；与旧122.57ms的差异不能归因于优化，因为mock/客户端观测已变且只有单轮。旧v1原始文件全部保留。

原始报告：[v2 10并发/25ms](capacity-v2-role-delay25-pool32-concurrency10.json)、[v2 100并发/0ms](capacity-v2-role-delay0-pool32-concurrency100.json)。首内容probe及负载测试均位于 `internal/server/*capacity*_test.go`，不增加生产绕过功能。三方同口径、固定版本、持续负载与正确性门禁见 [002性能规格](../../specs/002-performance-parity/spec.md)及[补充设计](../../specs/002-performance-parity/plan.md)。

测量修正后的专项竞争检查通过（2.460s）：`MONI_CAPACITY_TEST=true MONI_CAPACITY_DB_POOL=32 MONI_CAPACITY_CONCURRENCY=100 MONI_CAPACITY_CONTENT_DELAY_MS=0 TEST_DATABASE_URL=… go test -race -count=1 -timeout 60s -run '^TestCapacity' ./internal/server`。该次含probe单元场景与完整100并发/真实PG/Guard资源和资金断言；race延迟不写入普通性能结果。`go vet ./internal/server`亦通过。本次没有修改生产Go路径，无需用重复全库性能测量代替下一步瓶颈观测。

## 迁移 006 与计费 batch 后：三轮完整收费链路

本轮已落地[计费batch优化](performance-optimization.md)，保留三段发送前持久事务、所有资金/父锁/撤权检查及真实Guard；迁移006同时新增准入attempt候选解释。未关闭审计/outbox或任何商业能力。`v3`仅表示本轮文件版本，`measurement_version`仍为2，mock/有效内容定义与v2相同。

同机、同PG、池32（1条专用Guard）、5租户×2Key×10并发、0ms模拟内容延迟，普通编译顺序执行三次。每次仍是新schema/新连接池单轮突发，未做持续预热或长时采样。预览服务空闲运行，未同时由主任务进行构建或其他测试；没有OS级独占或4vCPU/8GB配额。

| 指标 | 第1轮 | 第2轮 | 第3轮 |
|---|---:|---:|---:|
| 额外首字节p95 | 94.08ms | 96.68ms | 89.93ms |
| 额外首有效内容p95 | 94.16ms | 96.73ms | 89.95ms |
| 额外首字节p99 | 96.79ms | 98.16ms | 90.69ms |
| 接受/DONE/POST/结算 | 各100 | 各100 | 各100 |
| 实收微元/残留冻结 | 2000/0 | 2000/0 | 2000/0 |
| 账本平衡/残留许可 | 是/0 | 是/0 | 是/0 |

原始报告：[第1轮](capacity-v3-batched-full-pool32-concurrency100-run1.json)、[第2轮](capacity-v3-batched-full-pool32-concurrency100-run2.json)、[第3轮](capacity-v3-batched-full-pool32-concurrency100-run3.json)。相对旧v2的119.54ms观测到较低数字，但只有一组旧样本，且代码包含batch与路由解释两类变化，不能给出严格单因素提升比例。50ms仍未达到，不以短突发完成速率冒称持续吞吐，也没有实跑Rust LiteLLM/Bifrost或完成002正式对标。

## v4 收敛版本：003/004、迁移006与fresh batch Guard

最终`make verify-mvp`通过后，在没有本任务其他测试、构建、profiling或浏览器操作的窗口顺序运行三轮普通`go test`容量实验。生产源码不再变化，Key SHARE实验已回滚；保留前述batch优化，新增003/004、迁移006与[每个检查点之后的新Ping批次](../decisions/ADR-004-fresh-guard-batches.md)。未启用`MONI_CAPACITY_PROFILE`、SQL预热或请求预热。测试仍在负载前预热基础池连接，随后每轮新schema/新池、单次100请求短流突发。

环境与负载口径同上：M5 Pro/18逻辑CPU/64GiB、Go1.27.2、PG16.15，总池32含专用Guard1条，5租户×2Key×10并发，0ms模拟内容生成延迟，保留完整权限、资金、审计与恢复证据写入。预览服务空闲运行，未做OS级独占；没有4vCPU/8GB资源限制。

| 指标 | 第1轮 | 第2轮 | 第3轮 |
|---|---:|---:|---:|
| 时间UTC | 08:30:55 | 08:30:57 | 08:30:58 |
| 额外首字节p95 | 90.50ms | 87.79ms | 86.55ms |
| 额外首有效内容p95 | 90.49ms | 87.78ms | 86.57ms |
| 额外首字节p99 | 91.10ms | 89.38ms | 87.77ms |
| 接受/内容样本/DONE/POST/结算 | 各100 | 各100 | 各100 |
| 实际上游峰值并发 | 100 | 100 | 100 |
| 收费与Key spent/账户与Key残留held | 2000/0 | 2000/0 | 2000/0 |
| 账本平衡/残留许可与Streams | 是/0 | 是/0 | 是/0 |

原始报告：[第1轮](capacity-v4-final-full-pool32-concurrency100-run1.json)、[第2轮](capacity-v4-final-full-pool32-concurrency100-run2.json)、[第3轮](capacity-v4-final-full-pool32-concurrency100-run3.json)。报告格式`measurement_version=2`未变，v4仅是本轮文件标识。

本轮额外首字节p95为**86.55–90.50ms**，首有效内容p95为**86.57–90.49ms**。这两项逐请求扣除mock等待后分别计算；role-only不算有效内容，也不是模型内部真实首token。比旧样本较低不能单独归因于Guard或任何其他修改，也不能与[Key锁诊断控制实验](performance-optimization.md)混为同一次验收。50ms仍未达到，未执行竞品正式运行、目标资源、持续负载或真实供应商验证；不据此声称生产SLA或接近Rust LiteLLM/Bifrost。

## v5/v6：按Key调度准入与完成阶段

该轮实现及完整正确性/阶段剖析、三版轮换9份原始JSON和binary/源码摘要见[Key调度记录](key-scheduling.md)。005先调度准入，首byte改善但完成时间回退，原始结果保留；006完成阶段共用短票据，保持RecordFinalization先提交、Settle独立事务和原5s业务清理ctx。所有权限/资金/派发/Guard/审计仍启用，票据等待计入原客户端开销；上游和客户端I/O不持票。

在同机、同32池/100并发/5租户×2Key×10请求/零mock生成延迟下，固定三个普通binary，按原版→005→006、006→原版→005、005→006→原版顺序运行三组。没有其他本任务构建、测试、profile或浏览器操作并行；基础连接池预热，未预热SQL/收费请求，仍是新schema/新池的短突发，没有OS级独占或4vCPU/8GB资源配额。测量格式仍为2。

| 轮次 | 原版额外TTFB p95 | 组合版额外TTFB p95 | 组合版额外TTFC p95 | 原版/组合版全部完成 |
|---|---:|---:|---:|---:|
| 1 | 105.78ms | 77.24ms | 77.24ms | 234.69/170.30ms |
| 2 | 91.97ms | 77.76ms | 77.76ms | 213.72/174.62ms |
| 3 | 99.33ms | 69.80ms | 69.81ms | 222.67/162.29ms |

组合版原始：[第1轮](capacity-v6-key-phases-combined-run1.json)、[第2轮](capacity-v6-key-phases-combined-run2.json)、[第3轮](capacity-v6-key-phases-combined-run3.json)；原版/005各3轮与[manifest](capacity-v6-key-phases-manifest.json)在上述记录中逐一链接。九轮每轮接受/内容/DONE/POST/结算均100、上游峰值100，实收2000、账户和Key held0、账本平衡、许可/Streams归零，005/006显式lane/ticket也归零。

该轮额外首byte p95 **69.80–77.76ms**、首有效内容p95 **69.81–77.76ms**。组合版相对同组原版，首byte降低15%–30%，全部完成降低18%–27%；保留全部波动，未只取最快一轮。005中间的完成回退被006收回；006第二轮首byte略高于005该轮，不单独声称完成调度每轮都降低首byte。采样最大等锁连接由原版30变为8，累计池获取耗时由11.79–12.57s变为0.50–0.78s，均不是单请求延迟。

**仍高于50ms门槛**。本轮不是持续goodput、真实模型首token、固定资源/公网TLS/过载/长流/慢客户端或竞品认证；002目标不变，旧v1至v5数据均不覆盖。

## v7：原事务内有序读取batch

[007规格](../../specs/007-dispatch-read-batches/spec.md)与[完整验证](dispatch-read-batches.md)保留三个准入COMMIT、逐语句新快照和全部商业保护，减少9次正常首次准入的应用等待边界，并修复pgx Scan错误后的pipeline清理。全Go race、vet及嵌入编译通过，前端未修改。

固定006/007普通binary先运行三对，再因首轮before额外TTFB p95异常562.77ms而预先追加三对确认；全部十二份原始JSON、执行顺序与121项输入/binary摘要均在上述记录中。异常原因未明，不用该轮宣称大幅收益；全部原始数据保留。

007六轮每轮额外TTFB p95为**61.56–69.74ms**，额外TTFC p95为**61.66–69.72ms**。正常before第2–6轮TTFB p95为67.56–71.62ms，对应五组降低4.02%–8.88%；不是合并样本p95。全部完成有一对回退3.29%，其余正常对改善1.98%–12.65%，池累计获取耗时也有升降，不宣称稳定吞吐或所有资源指标改善。

十二轮每轮100接受/内容/DONE/POST/结算，上游峰值100，收费与Key spent2000、wallet/Key held0，账本平衡，runtime/Streams/lane/ticket0。**50ms与正式目标资源/持续竞品对照仍未通过。**

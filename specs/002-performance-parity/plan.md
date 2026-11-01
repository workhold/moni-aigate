# 性能迭代设计与任务

2026-10-30。依赖 [性能规格](spec.md) 和 [只读热路径审计](../../docs/verification/performance-audit.md)。最初完成测量修正，随后实施SQL batch、fresh Guard、005/006短阶段调度及007派发读取batch；以下按实际状态区分已做与后续工作，所有优化仍须有独立正确性与测量证据。

## HLD 补充

保持 Go/Gin + PostgreSQL 的单活动实例和商业事务边界。负载器、网关、mock 在正式比较中分为独立进程；mock 不连接真实供应商。负载器记录 start/first-body/first-useful-event/end，mock 记录同请求从接收到发出角色帧/内容帧的持续时间。时间间隔由各进程单调时钟产生，不相减不同主机的 wall clock；request ID 关联受控假数据，不携带真实 Key/正文。

直接上游、Rust native、Bifrost、moni 轮流运行相同工作集。每个 app 固定4vCPU/8GB，数据库/负载器/mock的配额另列，不能让某个 app 把工作移出配额而隐去成本。共同代理实验与完整商业链路独立裁决，moni 正常入口无测试绕过开关。所有配置与参考源码固定、离线假密钥，无真实模型账单。

## LLD 补充

### 计时

对单个请求定义客户端 `C0/Cbyte/Ccontent/Cend`，mock 本地持续时间 `Mrole`（到角色帧发出）和 `Mcontent`（到内容帧发出）。

- `TTFB = Cbyte - C0`，`TTFC = Ccontent - C0`。
- `extra_TTFB = TTFB - Mrole`，`extra_TTFC = TTFC - Mcontent`，分别先算单请求再聚合；不把握手/鉴权/路由或持久化时间减掉。
- `TTFC` 到完整可解析的有效 delta 为止，因此包含事件缓冲与解析；不能以读到 JSON 的第一个字母就记内容。
- mock 的人为生成等待及 barrier 分开报告。直接上游作为网络/负载器基线独立列出，不直接相减两组分位数。
- 本地 v2 仍在同一测试进程共享 epoch；没有跨进程同步的声明。`latency_client_to_first_content_including_mock_waits` 包含 barrier，不能当真实模型首 token性能。

### 待实现的阶段观测

请求内各阶段单独用单调时钟：入口实例检查（队列/实际Ping）、API Key查询、模型授权/目录、候选及解密、许可、Reserve（池获取/锁/SQL/提交）、RecordAttempt、PrepareDispatch、派发前实例检查、DNS/连接/TLS、首协议事件、首有效内容、最终证据/结算及清理。按 request/attempt 关联只保存白名单耗时和版本；观测内存有界，不旁路记录正文或秘密，也不影响持久财务事实。

### 优化顺序与不变量

1. **减少事务内往返**：先从有依赖证明的 SQL batch/批量分录着手，维持相同事务、锁顺序和提交前不可发上游。缩短 Key/租户钱包排他锁持有时间；不能把余额改为异步最终一致。
2. **区分锁用途**：RecordAttempt的共享Key父锁已作控制实验，收益不稳定而回滚，当前仍FOR UPDATE，见[记录](../../docs/verification/performance-optimization.md)。READ COMMITTED 下获锁后新语句必须看见刚提交的撤回，不能把权限子查询提前到等待前快照。
3. **派发事务合并**：只有重新定义且验证 reserved/recorded/sending 的提交/崩溃/恢复语义后，才能把 RecordAttempt 与 PrepareDispatch 合并；提交 sending 不证明已实际消费。并发取消、重复attempt和安全第二次尝试均须验收。
4. **实例检查排队**：已实现[登记后启动的新Ping批次](../../docs/decisions/ADR-004-fresh-guard-batches.md)，不缓存旧成功或失锁重连。所有调用保持“检查在本次准入之后”的新鲜性；失败永久fence，关闭等真实Ping退出。
5. **公网连接**：当前禁keepalive/HTTP2的策略防隐式重放但带来TCP/TLS成本。未来连接复用需绑定不可变endpoint/credential/network版本、实际DNS/CIDR验证与安全驱逐，真实失效池连接不能重放生成POST；不能只改Transport开关就算通过。
6. **按Key调度短事务阶段**：[005](../005-key-admission/spec.md)在借用连接前调度三个准入事务与Guard，排队计入客户端开销；[006](../006-completion-scheduling/spec.md)对完成阶段共用票据，先提交证据再独立结算、仍受原5s清理上下文限制。二者都在上游/客户端I/O之外释放票据，不弱化原数据库锁。首byte与全部完成必须共同报告，005中间阶段的完成回退保留在[控制记录](../../docs/verification/key-scheduling.md)。
7. **批量发送派发读取**：[007](../007-dispatch-read-batches/spec.md)保留三个COMMIT与等锁后的新语句快照，减少原事务内应用等待；新增真实撤权、deadline、初始化竞争、错误排空与取消回归。首轮基线异常、确认组和完成时间波动全部见[007记录](../../docs/verification/dispatch-read-batches.md)，不以该局部收益替代正式门槛。

不引入 Redis/Kafka、不迁移语言、不缓存撤权/余额判定。某项优化没有改善实测就保留或回滚有说明的变更，不扩大连接池来替代锁竞争分析。

## 任务与当前状态

| 任务 | 对应需求 | 当前状态/完成判据 |
|---|---|---|
| P-00 | PERF-01/02/08 | 本次实现：客户端独立识别有效事件，测试role/heartbeat/usage不算内容，并保留旧报告；执行记录见容量页 |
| P-01 | PERF-03/04 | 部分：本地v2分别记录byte/content及p99，测试专用tracer/池/锁/Guard/pprof可诊断；独立进程逐请求原始样本和生产有界阶段观测仍待实现 |
| P-02 | PERF-05/08 | 已固定参考源码和入口；release构建、配置适配及纯mock三方运行未做 |
| P-03 | PERF-04/05 | 固定配额、预热/持续/三轮、冷/热TLS、长流/慢客户端矩阵待执行；容器环境当前不可用 |
| P-04 | PERF-06/07 | SQL batch、fresh Guard、005/006/007已实施并按独立规格回归；Key SHARE实验已回滚。当前实际收益、代价与50ms差距见容量及各切片记录，不标为正式对齐通过 |
| P-05 | 全部 | 依据acceptance独立裁决，各项缺证据即未验收，不把基础实验通过替代商业产品 |

源代码、profile、事务或连接实现每次改变后，只重跑受影响的正确性门禁与同配置性能项；先后运行性能实验，避免竞争负载污染结论。

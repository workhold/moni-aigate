# Key调度性能迭代

2026-10-30。本轮继承[002性能规格](../../specs/002-performance-parity/spec.md)，不关闭权限、资金、审计、恢复或实例检查。测量仍为本机短流突发，不是正式持续吞吐、目标资源或竞品认证。

## 005：借用数据库连接前的准入调度

[005规格](../../specs/005-key-admission/spec.md)在global100/tenant32/key16运行许可内，按认证内部Key进行有界FIFO交接。首张票据覆盖三个原有准入事务及fresh Guard，每次安全重试重新排队；票据在上游调用和客户端写入前释放，不覆盖整条流。三个事务、父锁强度、获锁后的新权限查询、预占/审计/outbox均保持原实现。

附带修复Plan.Start在admit回调panic时尚未转交的channel/half-open许可泄漏；成功转交的实际执行许可仍按原生命周期释放。Reserve失败也先归还票据，再写错误响应，避免慢写出挡住后继。

### 正确性证据

- [队列单元](../../internal/server/admission_queue_test.go)：FIFO、不同Key、首/中/尾取消、授予与取消的确定性屏障、128轮交接竞争、幂等release、16票上限、空lane回收及panic退出。
- [真实PG/HTTP集成](../../internal/server/admission_queue_integration_test.go)：唯一数据库连接被主协程占用时排队取消仍可完成；等待不占channel/half-open；Key/tenant grant/member grant撤权后拒绝；长流允许同Key后继；原持久幂等只预占/POST一次；安全429后重新排在既有等待者之后。
- [routing](../../internal/routing/routing_test.go)验证Closed/HalfOpen两态admit panic不消耗attempt、健康中性，后继可继续准入且成功转交不提前释放。
- 专项race通过：server4.156s/routing2.914s；随后真实PG全Go race通过，server83.218s、billing17.465s、identity42.615s、operations6.074s、provider5.360s、routing3.557s。`go vet ./...`及嵌入程序编译通过。上述时间是测试执行时间，不是请求延迟。

### 三对固定binary控制

修改前先固定普通测试binary，再编译005版本，两者均无race。顺序为before1→after1→before2→after2→before3→after3；没有其他本任务构建、测试、profile或浏览器操作同时运行。预览服务空闲运行，未做OS级独占。环境沿用M5 Pro/18逻辑CPU/64GiB、Go1.27.2/PG16.15，总池32含Guard专用1条，5租户×2Key×10并发、0ms模拟生成延迟、新schema/新池单次100请求。只预热基础池连接，不预热SQL或收费请求。

| 对照 | before额外TTFB p95 | 005额外TTFB p95 | before/005额外TTFC p95 | before/005全部完成ms |
|---|---:|---:|---:|---:|
| 1 | 93.17ms | 76.82ms | 93.16/76.82ms | 216.08/237.77 |
| 2 | 91.17ms | 78.61ms | 91.24/78.65ms | 207.22/238.37 |
| 3 | 96.93ms | 74.65ms | 97.09/74.63ms | 220.21/248.20 |

原始：[before1](capacity-v5-key-admission-before-run1.json)、[after1](capacity-v5-key-admission-after-run1.json)、[before2](capacity-v5-key-admission-before-run2.json)、[after2](capacity-v5-key-admission-after-run2.json)、[before3](capacity-v5-key-admission-before-run3.json)、[after3](capacity-v5-key-admission-after-run3.json)。[binary与源码摘要](capacity-v5-key-admission-manifest.json)记录005当期快照，不是之后006的源码摘要。before/after除调度外还含上述panic清理修复；未触发panic，不单独宣称纯函数微基准提升。

每轮接受/有效内容/DONE/POST/结算均100，上游峰值并发100；收费和Key spent均2000，账户与Key held归零，账本平衡、运行许可/Streams归零。005报告额外显式记录空lane与ticket计数均0；before版本只含原有运行许可与Streams断言。测量版本仍2，FIFO等待计入原客户端TTFB/TTFC，role-only仍不算有效内容。

**首byte的配对降幅为17.55%/13.78%/22.98%，但全部完成变慢10.04%/15.03%/12.71%。** 单轮突发完成速率的after/before比为0.909/0.869/0.887，不能宣称综合吞吐改善或002持续goodput达标。005是首byte收益与完成代价并存的中间结果，不能只展示较低的首byte数字。

### 后续诊断依据

只对固定005 after binary执行一次已有profile，原始暂存`/tmp/moni-005-after-profile.json`及`/tmp/moni-005-after-diagnostic.json`；[聚合诊断归档](capacity-v5-diagnostic-summary.json)保留原profile摘要、阶段分布与采样，省去完整span和SQL模板。tracer、2ms采样和pprof有额外开销，不把该次延迟当正式成绩，也不把多个阶段p95相减。

按本barrier负载的最后Dispatch完成切分，600次准入Acquire累计0.87s/p95 4.79ms，300次完成Acquire累计9.99s/p95 104.60ms。Settle整个事务/父锁batch p95为73.14/56.86ms，完成阶段Key/账户锁等待采样1546/518点；它们是重复后端采样，不能当请求数。共享钱包与REVENUE尚未细分，不能全部归因于某一账户。准入主要用10条连接、完成扩展到31条，SQL首次准备可能延长持锁，缺少独立控制不能断言完成回退全来自冷prepare。COMMIT p95低于1ms，WAL计时关闭，不作磁盘归因。

此证据支持[006完成阶段调度规格](../../specs/006-completion-scheduling/spec.md)：先减少同Key完成操作占池等锁，再测首byte及完成两端，保持证据先提交、结算后成功终态和原5s清理上限。

## 006：准入与完成共用短阶段调度

完成阶段按认证Key加入同一FIFO，获票后先执行原RecordFinalization事务提交，再执行原Settle事务；函数返回即放票，之后才写JSON/成功流终态。票据等待消耗原独立5s业务清理上下文，客户取消不会取消该上下文。未增加事务合并、锁降级、资金缓存或异步估扣。完成排队仍保留原执行渠道许可到实际收尾，可能影响混合长流容量，不能把005的“等派发票无渠道许可”推广到完成阶段。

### 正确性与实际边界

[新增真实PG/HTTP测试](../../internal/server/completion_queue_integration_test.go)实际完成两种场景：

1. 客户已读到文本，供应商已发可信usage/DONE，完成票据被测试owner阻塞。唯一数据库连接被主协程占用时，等待者无事务；取消真实客户socket并观察服务端ctx取消，清理仍等票。放票后final/metering/settlement各1、收费20、held0、POST1，未向已取消客户发成功终态，原渠道/运行许可回收。
2. 实等生产5s cleanup超时，客户端收到billing_pending、无DONE，无持久最终量/收费。另用HTTP外层测试wrapper设置8s原执行deadline，等待PostgreSQL真实时钟过期后，全新Store.Recover按durable sending证据转pending_reconciliation、保留held140，未重造RAM usage或重发POST。未修改持久deadline，也未等待真实24h财务截止；原财务截止时间压缩回归继续执行。

005安全重试测试已按006同步：失败尝试先排队保存finalization(false)并归还旧渠道，再让下一次派发重新入队。以tailOwner和受控第二响应证明retry不提前RecordAttempt，不依赖429冷却后选中哪条渠道。首轮全库因旧测试仍假设“完成阶段没有票据”而失败，已调整屏障并完整重跑，保留原幂等/POST/资金断言。

完成队列专项race通过9.970s；含准入、完成、原生协议与交付故障的定向race通过server18.632s/routing1.984s。最后`go test -race ./... -count=1 -timeout 240s`全包通过：server92.625s、billing16.991s、identity45.259s、operations6.264s、provider5.747s、routing4.941s。随后`go vet ./...`、普通容量binary及嵌入程序编译全部通过。前端未修改，沿用上一轮已通过的12项前端测试及strict TS/Vite产物。

### 最终三版轮换对照

在上述检查结束后，固定原版、005仅准入、006组合版三个普通binary。三组顺序分别为：原版→005→006，006→原版→005，005→006→原版；每版各3次。全程未同时执行本任务其他构建、测试、profiling或浏览器操作。负载、主机、池连接预热、mock与测量口径同前；没有SQL/请求预热、4vCPU限制或OS级独占。[binary/源码/顺序摘要](capacity-v6-key-phases-manifest.json)保留服务与测试Go、迁移SQL及关键构建输入摘要，属于本机验证快照，无Git提交SHA或正式发布证明。

| 轮次/版本 | 额外TTFB p95 | 额外TTFC p95 | 额外TTFB p99 | 全部完成 |
|---|---:|---:|---:|---:|
| 1 原版 | 105.78ms | 105.84ms | 106.69ms | 234.69ms |
| 1 005 | 82.78ms | 82.77ms | 83.66ms | 244.98ms |
| 1 006 | 77.24ms | 77.24ms | 80.15ms | 170.30ms |
| 2 原版 | 91.97ms | 92.32ms | 94.18ms | 213.72ms |
| 2 005 | 73.58ms | 73.58ms | 74.15ms | 230.66ms |
| 2 006 | 77.76ms | 77.76ms | 79.02ms | 174.62ms |
| 3 原版 | 99.33ms | 99.32ms | 100.47ms | 222.67ms |
| 3 005 | 73.67ms | 74.22ms | 75.12ms | 237.11ms |
| 3 006 | 69.80ms | 69.81ms | 70.87ms | 162.29ms |

原始文件：[原版1](capacity-v6-key-phases-original-run1.json)、[005-1](capacity-v6-key-phases-admission-run1.json)、[006-1](capacity-v6-key-phases-combined-run1.json)；[原版2](capacity-v6-key-phases-original-run2.json)、[005-2](capacity-v6-key-phases-admission-run2.json)、[006-2](capacity-v6-key-phases-combined-run2.json)；[原版3](capacity-v6-key-phases-original-run3.json)、[005-3](capacity-v6-key-phases-admission-run3.json)、[006-3](capacity-v6-key-phases-combined-run3.json)。旧v4/v5保持原样，不用新结果覆盖旧基线。

九轮接受/有效内容/DONE/上游POST/结算均100，真实上游峰值100、无拒绝或网络错误；每轮实收/Key spent2000，账户和Key held0，账本平衡，运行许可/Streams归零。005/006显式lane与ticket也归零。006每轮采样最大锁等待连接从原版30降至8，池Acquire累计耗时从原版11.79–12.57s降至0.50–0.78s；这些是多次调用的累计值与采样，不是单请求延迟。

最终组合版额外首byte p95为**69.80–77.76ms**，首有效内容p95为**69.81–77.76ms**。相对同组原版，首byte各降低26.98%/15.45%/29.73%，全部完成各降低27.44%/18.29%/27.12%，收回005单独实施的完成回退。006第二轮首byte比005该轮稍高，因此不宣称完成调度本身每轮都降低首byte；它主要改善完成阶段竞争，组合版三组相对原版两端均改善。

保留005+006。**50ms仍未达标**；短突发完成速率不是持续goodput。每Key10个请求依次经过三段准入事务，剩余FIFO/事务往返和跨Key共享钱包竞争仍存在，不能仅按Go/Rust推断可消除。后续若合并派发事务必须先重新定义崩溃/幂等/撤权证据合同；固定资源持续负载、真实TLS/长流/慢客户端/过载及竞品对照仍按002单独验收。本轮不修改这些门槛。

005+006当期程序曾重启本机58123预览，`/health/ready`返回`status=ready`、`recovery_mode=false`，首页与当期`web/dist/index.html`逐字节一致。92项当期源码/构建输入及三个容量binary的SHA均与v6 manifest匹配；本轮未修改前端、评审账号MFA或客户资金。后续007的代码摘要与最新数据见[派发读取批次](dispatch-read-batches.md)，v6仍为历史快照。

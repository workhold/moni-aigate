# 007：派发读取批次验证

2026-10-30，对应[007规格](../../specs/007-dispatch-read-batches/spec.md)，继承[005/006结果](key-scheduling.md)。已通过本地正确性与固定binary对照，保留007；局部首byte小幅改善，50ms及正式持续/资源/竞品门槛仍未通过。

## 代码变化及边界

| 阶段 | 原应用等待边界 | 候选实现 |
|---|---|---|
| RecordAttempt取得原父锁、reservation锁并刷新证据后 | isolation、发布绑定、数据库时间、可选重试证据、成本分别查询 | 按原顺序一个只读batch；无成本仍为NULL/unknown |
| PrepareDispatch完成原lockPrincipal后 | channel、isolation、credential、region逐次查询 | 四条独立有序语句同batch；原领域错误顺序不变 |
| PrepareDispatch的执行状态 | reservation锁、数据库时间、attempt锁逐次查询 | 三条独立有序语句同batch；时间在等锁之后执行 |
| 记账账户 | 初始化INSERT、ORDER BY id FOR UPDATE逐次执行 | 两条独立语句同batch；初始化竞争后读取新余额 |

在revision>0的首次尝试中，上述位置合计减少9次应用等待结果的边界；安全第二次尝试还多减少1次。首次预备、驱动协议和锁等待仍会影响实际网络与时间，不能把该数量解释为已测RTT或延迟。

原READ COMMITTED、锁顺序/强度和逐语句快照、三个准入COMMIT、fresh Guard、资金预占/预算、幂等、安全重试、最终证据与结算、审计/outbox及恢复均保留。只读batch在错误路径排空时可能执行后面的读/锁语句，仍受原lock/statement/context上限约束，没有提前排入业务写入。原005/006队列和deadline保持不变。

首次专项发现真实ScanArgError后同连接查询报`conn busy`：pgx v5.7.6的pipelineBatchResults.Close在首次观察lastRows.err时直接返回，尚未关闭pipeline。closeBatch错误路径增加一次重复Close（该API明确允许、不重跑callback），完成收尾并保留驱动返回的错误。正常路径没有额外SQL或Close；保留原失败用例验证同一物理连接可继续查询。另用仅测试的Tx包装追加真实`SELECT 1/0`，证明正常读取或Go领域拒绝之后的数据库错误不会被忽略。驱动先记录Scan错误时可能隐藏更晚的SQL诊断，双Close不能取回该额外诊断，但仍保证失败回滚及连接清理。

## 诊断与对照方法

修改前固定的007 before binary直接取自006最终普通binary，SHA与v6 manifest的combined匹配。另只运行一次该baseline的opt-in profiler，用于看阶段与调用，原始文件留在`/tmp/moni-007-before-profile.json`及`/tmp/moni-007-before-diagnostic.json`。tracer、采样与pprof有观测开销，不将这次延迟当成绩，不把阶段p95相减。

[聚合诊断摘要](capacity-v7-before-diagnostic-summary.json)归档阶段调用与原profile SHA。每阶段100笔事务，计入BEGIN/settings/COMMIT后Reserve/RecordAttempt/PrepareDispatch各有11/11/14次query或batch调用；007代码预期变为10/8/9，共36→27个应用等待边界。原Settle账户锁仍有竞争，不能把阶段p95直接相加或把全部剩余时间归于SQL往返。

正确性与编译完成后固定after，停止其他本任务构建、测试、profile与浏览器操作，按before1→after1、after2→before2、before3→after3运行三对普通无race实验。负载继续为M5 Pro/18逻辑CPU/64GiB、Go1.27.2/PG16.15，本机100请求短流、5租户×2Key×10并发、32总池含1条专用Guard连接、模拟生成0ms，新schema/新池，只预热基础池连接；不预热SQL/收费请求，不做OS级独占或4vCPU配额。测量格式与客户端内容定义保持2；旧预览空闲运行。

第一轮before的额外TTFB p95异常达562.77ms，其余before约68–72ms。该轮上游准入min/p50/p95为509.97/541.73/560.51ms，呈整体平移；mock→client p95仍2.96ms，采样最长等锁查询20.78ms，池累计获取565.81ms，没有足够数据定位异常原因。不能据此计算夸大的常规收益，也不能删除该轮。为解决稳定性疑点，在不改代码/配置的前提下，预先固定再执行三对：after4→before4、before5→after5、after6→before6。没有继续筛选轮次或只取最快结果。

## 正确性与编译

- [派发读取真实PG回归](../../internal/billing/dispatch_batch_integration_test.go)：渠道/凭据父锁等待后的撤权；requestDeadline在观察到reservation等待时仍未过期、随后真实DB时间经过才放锁；deadline优先于非prepared状态；领域错误优先级；NULL/unknown成本；缺行、三处取消与晚到SQL错误后单连接Store继续可用。未修改持久deadline。
- [账户批次真实PG回归](../../internal/billing/account_batch_integration_test.go)：初始化唯一冲突后看见真实平衡信用777；逆序kind仍按全局ID锁定；非法kind/FK错误、真实ScanArgError、两处取消后的排空、回滚与连接使用。临时表和Tx包装仅存在于测试，不增加生产故障开关。
- 原父锁后的tenant/member/key/model/profile撤权、reservation后的新证据、幂等、安全重试、并发预算、SIGKILL、Guard及三协议回归继续执行。
- 首次专项仅Scan错误连接收尾失败，修复后专项race通过12.081s。随后`go test -race ./... -count=1 -timeout 240s`全包通过：billing40.537s、identity74.785s、operations7.388s、provider6.118s、routing4.815s、server158.600s。均为测试执行时间，不是请求延迟。
- `go vet ./...`、普通容量binary与`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate`通过。前端无修改，沿用上一轮12项测试、strict TS/Vite通过的dist；没有声称本轮重新执行完整前端验收。

## 十二轮结果

每格before/after，单位ms。首轮异常完整保留；第4–6对为上述预先固定的确认组。

| 配对 | 额外TTFB p95 | 额外TTFC p95 | 额外TTFB p99 | 全部100请求完成 |
|---|---:|---:|---:|---:|
| 1（baseline异常） | 562.77 / 69.74 | 562.78 / 69.72 | 564.01 / 70.74 | 674.26 / 165.66 |
| 2 | 71.62 / 67.35 | 71.63 / 67.33 | 73.94 / 68.54 | 175.84 / 163.52 |
| 3 | 71.60 / 67.91 | 71.58 / 67.91 | 72.04 / 69.50 | 159.52 / 164.78 |
| 4 | 70.64 / 66.09 | 70.63 / 66.10 | 71.63 / 66.98 | 158.25 / 152.86 |
| 5 | 67.56 / 61.56 | 67.54 / 61.66 | 69.69 / 63.17 | 154.24 / 151.19 |
| 6 | 70.04 / 67.23 | 70.40 / 67.21 | 71.58 / 68.55 | 183.82 / 160.56 |

原始：[before1](capacity-v7-dispatch-batch-before-run1.json)/[after1](capacity-v7-dispatch-batch-after-run1.json)、[before2](capacity-v7-dispatch-batch-before-run2.json)/[after2](capacity-v7-dispatch-batch-after-run2.json)、[before3](capacity-v7-dispatch-batch-before-run3.json)/[after3](capacity-v7-dispatch-batch-after-run3.json)、[before4](capacity-v7-dispatch-batch-before-run4.json)/[after4](capacity-v7-dispatch-batch-after-run4.json)、[before5](capacity-v7-dispatch-batch-before-run5.json)/[after5](capacity-v7-dispatch-batch-after-run5.json)、[before6](capacity-v7-dispatch-batch-before-run6.json)/[after6](capacity-v7-dispatch-batch-after-run6.json)。[manifest](capacity-v7-dispatch-batch-manifest.json)包含两个binary SHA、实际顺序、异常后的确认计划与执行、121项Go/迁移/依赖和完整dist摘要、预览binary摘要；前后全部输入未变。这是本地快照，没有Git提交或发布证明。

全部after的每轮额外TTFB p95为**61.56–69.74ms**，额外TTFC p95为**61.66–69.72ms**；确认组TTFB p95为61.56–67.23ms。正常before第2–6轮为67.56–71.62ms，对应五组TTFB各降低5.97%/5.15%/6.44%/8.88%/4.02%。这些是每轮分位数及配对变化，不是合并样本的总体p95；不拿旧006窗口69.80–77.76ms直接计算本轮归因。

全部完成第3对回退3.29%，其他正常对改善1.98%–12.65%，不能宣称一致吞吐提升。池Acquire累计第2/3对反而增加11.4%/56.2%，确认组也有升降，所有after累计0.37–0.72s；多调用累计不是单请求耗时，也不能据此断言某阶段原因。最大等锁连接采样before/after均8–9。保留全部数据，不将更快首byte等同所有资源指标均改善。

十二轮每轮接受/有效内容/DONE/上游POST/结算均100，真实上游峰值100，无拒绝、网络错误或监测错误；每轮实收及Key spent2000，wallet/Key held0，账本平衡，runtime/Streams/lane/ticket归零。并行测试/构建/profile均已结束，不使用诊断成绩替代这里的普通binary结果。

## 保留决定与剩余边界

保留007及连接错误清理修复：正常五对首byte均小幅改善，新增正确性屏障通过，原商业合同不变。仍高于**50ms**；没有目标4vCPU/8GB、持续goodput、TLS/长流/慢客户端或Rust LiteLLM/Bifrost验收。进一步空间仍包括持锁期间的事务往返与共享钱包竞争，合并持久事务或连接复用须另立证据与恢复合同，不在本切片临时改动。

最终007程序已重启[本机Playground](http://localhost:58123/playground)。`/health/ready`实际返回`status=ready`、`recovery_mode=false`，首页与已验证`web/dist/index.html`逐字节相同；121项输入、两个容量binary及预览binary摘要均匹配v7 manifest。未修改评审账号、MFA或客户资金，未调用付费模型。

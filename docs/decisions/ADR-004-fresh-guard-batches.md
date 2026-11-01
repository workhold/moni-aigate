# ADR-004：共享新鲜实例检查

2026-10-30，已实施，归属002性能迭代。单活动实例、失锁永久fence与新请求查权的合同不变。

## 决策

`operations.Instance.Check`在同一mutex内登记调用者，由唯一worker摘取等待者快照，解锁后才向持有advisory lock的专用数据库连接发送Ping。本批所有登记都早于这条Ping启动；Ping进行中新来的Check只能等待下一批。没有TTL、历史成功缓存、后台心跳或复用已开始检查的结果。

成功发布的线性化点是Ping完成、在锁内确认实例与各调用上下文仍有效之后。任一Ping错误、Check取消或超时永久fence，取消真实Ping并唤醒当前和待执行批次；成功之后瞬间失锁的在途风险与旧逐次检查相同，不描述为分布式exactly-once。

唯一worker的Ping不超过1秒且不晚于本批最早deadline。Close先fence/取消，再在不持mutex时等待真实worker退出，最后解锁或物理关闭专用连接并归还池。失效但仍活着的持锁会话必须物理关闭，不能带着advisory lock回到连接池；多个Close等待同一关闭完成事实。

## 依据与验证

剖析发现200次逻辑Guard调用的mutex排队与Ping可观，但诊断有观测开销，不能据此宣称请求提速。本决策减少的是并发等待者重复的网络检查，保留每个调用登记之后的新数据库证据。正式容量结果单独记录。

`internal/operations/instance_batch_test.go`用真实PG与本地wire代理验证：晚登记不复用已开始的Ping、不同ctx最短deadline/任意取消整体fence、无deadline仍1秒有界、并发Close等待driver退出、取消前尚未发IO的活锁会话被物理关闭，以及旧回复已缓冲但backend已终止时下一批不能通过。

operations完整13项race通过（5.033s，无跳过），关键批次和原失锁/第二实例/排空测试重复3轮通过（10.081s），vet通过。正式吞吐、目标资源与竞品对比没有由这组正确性测试代替。

# 007 验收执行

状态：本地实现及正确性/固定binary对照通过，执行细节与全部十二轮数据见[验证记录](../../docs/verification/dispatch-read-batches.md)。

- [x] 渠道、凭据及binding父锁后的撤权不可派发。
- [x] 等待reservation锁后使用新数据库时钟，过期不写sending。
- [x] 提早领域拒绝、missing row、数据库错误和取消均排空/回滚，后继事务可用。
- [x] 账户初始化竞争后读取新余额，账户排序与完整性检查不变。
- [x] 001相关幂等、重试、并发预算与SIGKILL恢复回归通过。
- [x] 全Go race、vet与嵌入构建通过。
- [x] 至少三对固定binary交替比较，公开延迟、资金、资源与波动。首轮before异常保留，再预定三对确认；五个正常对TTFB p95下降4%–9%，一对完成回退3.29%。
- [x] HLD/LLD、验证记录及本地预览对应最终实现。最终007程序已重启58123，ready且recovery_mode=false，首页与已验证dist一致，121项输入及before/after/preview摘要匹配。

50ms与正式目标环境/持续竞品对照未通过，不能随本切片勾选。

# 007 实施计划

1. 固定006普通容量binary作为before；保持基准fixture与测量口径不变。
2. 实现RecordAttempt只读检查batch、PrepareDispatch两个有序读取batch；所有事务提交边界不变。
3. 独立实现账户初始化/锁读取batch；补充真实PG竞争与撤权屏障，复核快照、领域错误优先级及batch退出。
4. 专项及全量race、vet、build后固定after binary；停止其他测试/构建，依次before1→after1、after2→before2、before3→after3。
5. 保存源码/binary摘要和原始报告，按两端延迟与资金正确性决定保留或回滚；同步HLD/LLD与验证记录，再更新本地预览。

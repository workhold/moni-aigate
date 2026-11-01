# 007：派发前读取与账户锁批次

2026-10-30。继承001商业正确性、002性能口径与005/006短阶段调度。状态：实现、本地PG/race及固定binary对照通过，保留；实际证据见[验证记录](../../docs/verification/dispatch-read-batches.md)，50ms与正式持续/资源/竞品门槛未通过。

006基线的100并发短突发额外首字节p95为69.80–77.76ms。每Key十个请求依次通过三个独立准入事务，当期实现仍在事务内逐次往返读取渠道、凭据、截止时间、成本与账户。本切片减少客户端与数据库之间的等待，不合并事务、不提前派发。

## 必须保持

1. Reserve、RecordAttempt、PrepareDispatch三个原有COMMIT与派发后的fresh Guard全部保留。三个持久阶段、预占、幂等、未知消费及崩溃恢复合同不变，无schema或API变更。
2. 原READ COMMITTED、父锁顺序与强度不变。每条SQL仍独立执行；授权和隔离查询必须在相应父锁取得后的新语句快照执行，不能改成包含等待锁的单条JOIN/CTE。
3. RecordAttempt仅把已有的隔离、已发布路由、数据库截止时间、安全重试与成本读取放入一个有序batch。先锁住request/reservation并刷新证据；按原顺序检查领域错误。成本缺失仍为unknown、NULL，不造零成本，不放松重试证据。
4. PrepareDispatch在lockPrincipal完整结束后，按channel锁→binding隔离→credential锁→version地区顺序执行第一读取batch。授权通过后，再按reservation锁→数据库当前时间→attempt锁执行第二读取batch。尤其不能在等待reservation之前取时间后复用，也不能在app中检查完前就提交状态更新。
5. 只有全部校验通过才运行原sending/dispatching写batch。错误和取消始终关闭/排空batch，回滚整笔事务；保持原领域错误优先级，同时保留晚到的数据库错误。允许拒绝路径在排空时执行后续只读/锁语句，不能产生新的持久业务副作用。
6. 账户初始化和ORDER BY id FOR UPDATE仍是两条语句，按此顺序放入同一batch。初始化冲突等待结束后，锁查询用新快照读取已提交的完整余额；不使用同语句CTE、余额缓存或锁降级。按原全局账户顺序加锁，全部读取并检查完整性后才能记账。
7. 005/006队列、原请求deadline、5s完成清理时限及成功终态边界不变。本轮不修改账号、MFA或真实客户资金，不调用付费上游。

专项实际发现pgx v5.7.6的pipeline在最后一组Rows发生Scan错误时，第一次BatchResults.Close可能先返回解码错误、尚未关闭pipeline。错误退出须再调用一次文档允许重复的Close，完成收尾并保留驱动实际返回的错误；不重跑SQL或callback。驱动若先记录Scan错误，会隐藏之后pipeline关闭时的SQL错误，应用无法恢复该额外诊断，仍必须失败回滚并清理连接。无错误路径仍只关闭一次，使用真实ScanArgError与同一物理连接后继查询验证，不能删去该失败用例。

## 验证与裁决

- 真实PG屏障覆盖渠道/凭据撤权、binding隔离、等锁期间截止时间经过、账户初始化竞争、早期错误排空；已有tenant/member/key/model撤权、幂等与SIGKILL恢复回归继续通过。
- 全Go race、vet、嵌入构建通过后，固定前后普通binary，轮换至少三对100并发短流实验，保留原始样本、首byte/首有效内容/全部完成、资金与许可断言。旧006报告不覆盖。
- 仅保留有实测收益且正确性通过的改动；公开全部波动或回退。50ms、4vCPU/8GB、持续负载、真实TLS/长流和竞品对照继续按002独立验收。

# 008：预占与结算写入批次

2026-11-08。继承001资金正确性、002计时口径与005/006/007合同。当前已落地 journal 纯构建和相关 PostgreSQL 写批次回归，但整体 008 验收仍开放；008 的性能、固定 binary 对照和正式出口不能由局部功能测试替代。007 的本机100并发额外首字节 p95 为 61.56–69.74ms，仍未达50ms。

## 行为与不变量

1. 只减少同一原事务内部等待数据库结果的边界；Reserve、RecordAttempt、PrepareDispatch、RecordFinalization、Settle的独立COMMIT不变。READ COMMITTED、父锁强度/顺序、等锁后新授权/余额快照、fresh Guard、原预算/硬预占/证据/安全重试/恢复不变，无schema/API变更。
2. Reserve在原授权、profile、价格、DB时间、Key预算检查通过后，将原request INSERT→reservation INSERT→账户初始化INSERT→ORDER BY id FOR UPDATE按原顺序置于同一batch。每条SQL仍独立执行；先消费两个创建结果，再消费账户初始化和完整账户行，统一关闭批次。原创建失败不提交任何记录、幂等占位或金额。
3. journal写入拆为纯构建函数：使用已锁账户逐一校验平衡、溢出、非负余额，构造原journal→账户update→line语句并返回本地journal ID。构建函数无I/O，不可单独发上游、提交、修改共享余额或把未提交ID当成功凭据。原postLocked继续通过此函数发送并校验结果，供其他财务路径沿用。
4. Reserve将原journal/账户/分录与原Key held→recovery_job→audit→outbox按原顺序同batch发送；全部结果关闭成功后才允许原事务COMMIT。JSON净化/序列化及Go校验在发送前完成；任一语句或COMMIT失败均返回失败、回滚且清理active标记，原同幂等重试可用。
5. finish在原账户锁取得后的独立DB clock重新裁决财务截止时间，之后才构建单写batch：原journal→账户/分录→settlement→Key held/spent→reservation→request→case→recovery删除→audit→outbox→（超上界时）profile block/audit。保留原逐语句执行顺序、约束、状态和事件内容。到期仍以零收费释放；不把时钟提前到锁前，不合并为同语句CTE。
6. finalization先独立持久、settlement后成功终态的边界不变。未知量/消费、成本更正、人工核对、退款及双人财务策略不变。新batch错误均关闭/排空，使用007的重复Close错误清理，原transaction仅重试明确已中止的40P01/40001。
7. 本轮不改变并发/连接池/超时、005/006票据、账号/MFA或真实客户资金，不调用付费模型。

## 验证合同

- 真实PG在预占创建、账户/分录、Key、recovery、audit、outbox，以及结算每个尾写位置注入事务错误，验证无部分提交、无重复收费；移除故障后同请求/幂等或同settlement可成功且只发生一次。
- 新批次实际账户锁等待时取消及新快照、原财务期限锁等待、超预占profile block、并发预算/重复settlement、撤权/恢复/进程终止回归继续通过。
- 纯构建测试验证不平衡/溢出/余额不足时没有I/O，不发送不完整写批；数据库约束不被本地检查替代。
- 全Go race、vet、嵌入build后固定007 before与008 after普通binary；至少三对按预定平衡顺序测100并发短流，保存所有原始JSON与源码/binary摘要，分别公开首byte/首内容/全部完成与资金/资源。异常不可删除或用于夸大常规收益；新疑点才追加预定确认组。
- 根据正确性及实际收益裁决保留；不以局部突发代替50ms/目标4vCPU8GB/持续goodput/真实TLS长流/竞品认证。

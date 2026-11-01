# 025-A1-M：身份邮件真实进程恢复（窄片冻结）

2026-11-01，根冻结。仅扩展隔离`src/scripts/browser-qa`运行器及其测试（实际源码目录为`scripts/browser-qa`），不改main/identity/mailworker/maildeliver或生产配置。现有021库级lease测试不是本片真实进程证据；本片也不证明外部SMTP、容器、多活或数据库灾备。

## 运行与隔离

测试必须显式`MAIL_LIFECYCLE_REAL_MAIN_TEST=1`、专用literal`127.0.0.1:55439/moni_test`和预先构建的真实main binary。此独立opt-in不会连带启动原`BROWSER_QA_REAL_MAIN_TEST`的其他生命周期测试；测试可用`MAIL_LIFECYCLE_BINARY`显式选定预构建制品，缺省为仓库`bin/moni-aigate`，不存在时明确Skip。沿现有browser-qa随机schema、实际APP/projector LOGIN、独立loopback端口及0600私密manifest/邮箱；不触58123、不使用真实邮箱/凭据、不建模型/渠道/客户资金。

允许运行器的private `options`新增测试回调，在本地SMTP fixture创建后、真实main启动前设置同步屏障；CLI没有通用hook/SQL/生产开关。SMTP fixture只提供两个一次性物理屏障：TCP连接已接受、尚未发送220 banner；及完整DATA已持久写到私密邮箱、尚未发送最终250。屏障等待受context/Close和20秒以内观察预算约束；close须取消所有屏障并join全部连接。普通fixture行为不变。回调不得在fixture mutex内执行，避免close/观察死锁。

根已接受精确private接缝：`options.mailFixtureReady(context.Context,*mailFixture)`在main启动前设置两个固定barrier之一；`options.mailLifecycle(context.Context,*privateLifecycle)`在ready后同步执行。本run私有lifecycle只提供本run owner/安全状态/fixture、受控`killAndWait`与`restart`闭包；kill只触及本run创建的当前`exec.Cmd`，Wait后确认实际APP/projector连接归零。restart替换run cleanup持有的child/Wait handle，复用同binary、原env/keyring/origin，无bootstrap或新proof请求；重启前核binary摘要未改变。可提供仅返回布尔值的原proof摘要校验闭包，不将原AUTH_SECRET暴露为测试输出。回调及屏障不在CLI注册，不提供任意命令或SQL注入接口。

## 两个独立窗口

1. **claim已提交、SMTP尚未接受消息**：本地连接停在220前。用独立owner只读确认唯一outbox原ID为sending、generation=1/attempt=1、原60秒lease，证明为生产worker真实已认领；邮箱0。精确SIGKILL该run创建的main PID，Wait确认退出、实际APP/projector连接归零，再释放fixture屏障。不得把TCP连接当作邮件已收件。
2. **SMTP已接受、数据库ACK尚未提交**：完整DATA到达后，在fixture最终250前，专用owner事务对本次唯一outbox行取`FOR UPDATE`。释放250，真实sender返回accepted后其生产FinishMail UPDATE应阻塞在该事务。必须通过pg_stat_activity+pg_blocking_pids确认等待者为本次实际APP LOGIN、等待本次维护backend，outbox仍sending；仅有fixture已经写邮箱不满足此窗口。观察/信号/Wait总时长须严格早于原5秒FinishMail超时，依据DB query_age加单调elapsed，保守上限4.5秒。此时精确SIGKILL本run main，确认Wait和零APP/projector连接，才rollback维护屏障。邮箱此时恰一份已接受消息，数据库ACK未提交。

必须额外排除SMTP超时后的retry FinishMail：初始outbox只读查询**发出前**记录monotonic点，以`60秒−原lease剩余 + 从该点到kill/Wait/零连接全程`为发送已耗时保守上界，覆盖初始query RTT。冻结要求此上界严格小于19秒，实施进一步收紧为9秒以覆盖生产SMTP的10秒单IO界，故同时证明20秒Sender总超时未到。kill前确认首个最终250已实际flush并计数1，不能仅靠fixture DATA持久化或一个FinishMail waiter推断accepted。APP另有2秒DB lock_timeout，窗口物理等待须在其实际终止前被观察并发出SIGKILL；之后等待原APP backend退出仍必须符合4.5秒总界，且rollback一直在零连接之后。

每窗口独立run，不复用其他测试的child或角色。owner只锁本次outbox行，不修改generation/lease/next_attempt/state，不缩短生产60秒、不绕过expiry。未拿到物理等待证据的测试失败，不能以sleep或行状态推测窗口命中。

## 恢复与结果

用同binary、相同环境/keyring/DB/schema/origin重新启动真实main；不bootstrap、不重新请求身份邮件、不新建proof/outbox。可由测试内部受控重启seam返回新的child/Wait handle供现有cleanup继续拥有；不对外暴露任意命令。新实例须先正常ready，等待原lease自然过期，生产ClaimMail提升原generation/attempt到2并重发原payload。

窗口1最终恰一封，窗口2最终恰两封；两封的Message-ID、收件人、proof link完全相同，不能宣称SMTP exactly-once。原outbox最终`state=sent`（生产FinishMail的accepted结果）、payload_ciphertext清空且lease空，原challenge ID不变，proof未被消费；邮件重复本身不创造用户/tenant/余额。该证据不要求浏览器使用proof；single-use另由021现有验证证明。

运行结束必须所有child退出、SMTP/HTTP listener关闭、APP/projector连接归零、随机schema/角色清除、私密邮箱/日志/秘密删除或按既有manifest脱敏。安全证据只保留binary SHA、窗口、阶段时间/计数/代次、同一身份布尔值和清理结果；不导出原邮箱/proof/password/token、正文、DSN或任意SQL错误。

## 验收出口

先做fixture屏障取消/Close专项，再以真实main运行两个窗口且串行占用全库InstanceLock。命令/二进制hash和耗时写入独立验证记录；缺opt-in/binary/专用库时明确Skip，不能算已验。不会重用025-B0共享执行候选，也不据此标记完整025或最后性能已完成。

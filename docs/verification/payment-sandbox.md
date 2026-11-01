# 022-B0 独立本地支付沙箱验证

2026-11-01。P纯协议既有验收继续保留在[领域合同](../../specs/022-self-service-payments/b-domain-contract.md)。本记录新增M/Q运行代码的限定验证，不代表022-B1应用在线收款、生产PSP或整个022完成。普通`cmd/moni-aigate`没有新增支付配置或路由，预览58123仍为019且默认能力开关不变。

本片实现了独立`cmd/moni-payment-sandbox serve --component mock|receiver --manifest-file <已有私密文件>`；服务只加载既有配置、不创建资源。`paymentruntime`校验0700/0600当前uid文件、受控loopback DSN、三schema/七角色OID与实际LOGIN、最小表列/函数权限、固定SQL静态inventory和owner-only marker/key摘要，再取得各自ownership。实例丢失永久fence并关闭Done。正常服务排空HTTP/事务/回调后才释放ownership。`paymentsandboxqa`仅离线创建本次随机环境，以实际应用迁移/两次ProvisionFor和真实APP/projector LOGIN建立双审1000唯一入账、投影及核验的非空基线；清理限定本次精确对象。

已完成的专项：

- `paymentruntime/paymentsandboxschema`真实PG race通过（2.184s/1.909s），vet通过。覆盖两组件完整启动、二实例拒绝、正常重启、20并发freshGuard、取消不误fence、ownership backend终止后永久fence/Done；OID、额外UPDATE、disabled trigger、nonce、额外对象拒绝；文件/严格JSON/secret/symlink/FIFO/ambient service边界。静态inventory由受审SQL离线生成固化，运行时只读取实际catalog，不生成expected。
- `paymentsandboxqa`完整Create→Load/Open两组件→Guard/Close→资金摘要不变通过真实PG race；同时收口PostgreSQL OID JSON序列化及inet地址格式造成的启动拒绝，保持精确身份校验。
- `mockpsp`仓储真实PG整包race通过（4.949s），vet通过。100并发同命令、跨目标同ID冲突、到期/提前ready、cancel tombstone、capture/cancel竞争、held/release原initial不变、永久query及真实HMAC、before/after独立故障消费、十类typed event与持久control重读通过。callback真实单claim/两worker、1秒退避gen2、ACK同异体和before-send消费无虚构observation通过。
- `paymentreceiver`真实PG race通过（本轮2.512s）：50并发同通知只有一份inbox/receipt，重新签名不同raw空白仍回原canonical业务回执；异digest同事件只追加冲突，同delivery重放不重复冲突；坏签名无写入。所有结果在COMMIT确认后返回，未知COMMIT销毁连接且无ACK；重试只限真实40P01/40001并确认连接已回滚。
- 双槽runner真实固定loopback HTTP与HMAC专项通过（2.050s，含race开销）：两个接收请求同时阻塞时只有两份leased/attempt，第三条仍pending且未claim；HTTP期间无command/query/callback/control/merchant业务advisory锁，独立命令继续成功；放行后全部ACK持久化。此专项接收端为受控慢HTTP fixture，真实receiver进程另由CLI验收。

上述随机schema测试均验证APP非空资金全字段摘要未变，并排空runtime、销毁本次schema/角色/秘密。未调用真实支付商或收费模型供应商，也未修改评审账号、密码或MFA。

新增真实进程短程已通过：`TEST_DATABASE_URL=<专用测试库> go run ./scripts/payment-sandbox-qa --require-database`，最终补丁后2.84s，binary SHA-256 `37ca0557811327e1b012cfd887b18e0b63489420101e873845ba67d9f24f9e32`。脚本先build真实独立CLI，再启动mock/receiver三listener，验证两组件第二实例拒绝、HTTP鉴权、checkout/query独立P验签和请求绑定、capture/event/transition唯一、旧query永久不变、真实回调COMMIT ACK与原receipt重读、before命令回滚断连及after命令/query提交后断连原ID恢复；在确认提交后分别SIGTERM/SIGKILL、同manifest重启，全持久表摘要不变。非空应用资金摘要保持不变且环境精确清理。最终script真实PG/race专项通过（4.787s），vet通过；子进程只继承无密系统白名单，报告仅固定PASS/SKIP/FAIL及安全摘要。

namespace启动证明已补充冻结并实施：先前独立enum/domain/无冲突operator/普通table rule可被旧inventory遗漏；现在pg_type仅允许静态inventory表的精确row/array type及双向OID绑定，其他namespace执行对象显式零。mock与receiver各自真实受限LOGIN的正常Open、离线owner新增enum/domain/operator/rule/policy/collation拒绝及删除后正常Open均通过。最终runtime/schema真实PG race为3.807s/1.448s，末次测试清理分支修订后的namespace专项race2.685s/vet通过。不宣称防数据库管理员在启动证明完成后在线改DDL。

真实20次原退避长程**已完成，本次旧版本限定门禁通过，进程退出码0**：固定literal loopback callback endpoint无receiver以产生真实network失败，不改DB时钟/lease/attempt/trigger，没有重复启动或加速退避。安全报告`.local/payment-sandbox-long.log`已记录第01–20代逐代PASS，以及`twenty_real_attempts_original_backoff_review_and_no_twenty_first`、`nonempty_application_funding_digest_unchanged`、`exact_environment_cleanup`最终PASS；按原1/5/30/120/300秒退避耗尽20次后转review，额外等待1.5秒未出现第21次。原QA与mock进程均已退出；只在内容精确匹配原QA PID后删除过期PID文件，脱敏日志保留0600权限。

该长程binary SHA-256为`286bc2e155d97feb0fd62998cb89b2b42ba7a6ae53d9e76fb1e5d1be3515cd96`，结果只覆盖启动时这个明确版本。它早于namespace执行对象启动证明补丁，也不包含后来QA的子进程环境白名单、严格disconnect分类、RemoveAll结果检查，以及最终worker存活/非预期退出oracle；旧run的最终PASS不能泛化为这些新增门禁通过，也不代替最终runtime启动证明。上述后续补丁的已有专项结果仍按前文分别记载；若要将20代长程计入最终worker存活门禁，还须使用包含该oracle的最终版本独立验证。真正数据库COMMIT回包丢失在本次报告仍为`SKIP database_commit_reply_loss_no_wire_fault_facility`，没有拿提交后kill替代。

仍须独立完成：包含最终worker存活oracle的版本验证、末代/live lease崩溃恢复、真正数据库COMMIT回包丢失、首发前/receiver commit后丢ACK、after业务commit到fault消费之间精确kill、旧备份恢复及完整最终检查。旧binary的20次耗尽、转review与禁止第21次已按上文收齐结果，不再列为运行中。短程SIGKILL发生在已确认提交后，不冒充上述崩溃缝隙。HTTP代理/redirect/压缩/限流/timeout完整矩阵按各包专项另记，脚本不泛化声称已全覆盖。B1核心设计/SQL已冻结，但应用支付/SKU/inbox/outbox/capture资金接线与C来源lot/退款尚未完成，不能把沙箱回执当作客户余额入账。

统一架构12幅Mermaid图使用官方mermaid 12.2.0在独立headless Chrome实际解析/渲染12/12通过（3流程、7时序、2ER），并已人工查看[模块图快照](assets/payment-sandbox-architecture.png)；4384×2972完整无裁切，SHA-256为`a9ebcf3340ef1d01daa433c430829add58a1b7a9958e46c741bd849600d8aee6`。没有操作用户现有浏览器或预览。图验证证明语法与可读性，不代替源码语义复核。

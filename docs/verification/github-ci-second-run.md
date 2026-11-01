# GitHub 第二轮 CI：累计时限与数据库时区

2026-11-02，[运行41339443048](https://github.com/workhold/moni-aigate/actions/runs/41339443048)，源码 `1bcc10660fa3fdbe653651021d8815780088b98e`，最终失败。身份组成功；billing/server包分别在600.047/600.035秒累计上限结束，other组支付相关六包失败。架构和前端测试/构建通过，后续vet和嵌入构建因前序失败未执行，不能称整轮CI通过。日志和来源摘要见[机器记录](github-ci-second-run.json)。

## 已证实的累计时间

billing已有111个完整顶层PASS，Elapsed合计579.58秒；当前回滚父用例仅20秒、其projection_outbox子测试仅约1.317秒，前八个子测试均以2.17–3.01秒通过。剩余44个顶层测试尚未启动。server当时用例只运行12秒。栈不支持把这些用例称作十分钟死锁；旧pgx取消watcher等待Unwatch的goroutine也不证明旧SQL仍执行或锁泄漏。

按[根冻结合同](ci-profile-contract.md#第二轮累计时限与顶层测试分区根冻结)，两个大包各以相同race构建发现完整Test/Fuzz/Example名单，C序轮转四组，父测试及全部子测试同行；连同identity/other共十个独立runner/PG。包归属、动态顶层名单及各组并集均校验完整互斥非空，精确锚定-run，保留race、count1、各进程10分钟及原业务/SQL期限。

根与独立审阅分别校验YAML、7段bash语法、实际race发现名单及各精确正则：billing157项分40/39/39/39，server205项分52/51/51/51。根实际执行提取的分区脚本并捕获最终argv，包归属1/1/1/39，identity/other无-run过滤；本机Bash3仅在本地capture harness补mapfile与空数组nounset兼容，重DB调用被拦截，原go-list及race-list实际执行。此验证不等于Linux Docker运行。

## 支付原始启动的时区复现

固定v1 inventory四项CHECK含Shanghai反解析的timestamptz常量，原catalog按完整文本严格比较。专库临时表单事务分别SET LOCAL UTC/Asia/Shanghai，复现year1的00:00:00+00与08:05:43+08:05:43、year10000的00:00:00+00与08:00:00+08，随后ROLLBACK。

进一步创建一套原paymentsandboxqa隔离fixture，仅更改该fixture自己两个runtime角色的TimeZone：原paymentruntime.Load/Open在UTC对Mock/Receiver都返回ErrState，Asia/Shanghai两者均成功Open/Close；原资金基线不变，精确fixture清理成功。未更改既有角色、数据库默认值、长期预览或源代码。

修订只为一次性CI PostgreSQL增加timezone=Asia/Shanghai，并由ready SQL核实际值；仅设Go进程TZ或PGOPTIONS不能代替服务端默认值。产品DSN、v1 SQL、固定inventory、ACL、金融期限及所有测试源码均保持原样。默认C0 opt-in没有启用，不能将这些结果视为Linux expected profile或生产认证。新的远端运行仍须实际验证。

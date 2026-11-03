# CI 同库包隔离与默认函数权限

2026-11-04。本记录核对实际GitHub失败日志与当前执行合同；未启动新的PostgreSQL测试，不宣称修订后的CI通过。此次修改涉及测试调度和复现说明，生产请求链路、SQL材料、业务权限与数据关系保持原范围。统一架构及快照由集成人汇总全部并行结果后复核。

## 远端日志事实

运行 [41718896150](https://github.com/workhold/moni-aigate/actions/runs/41718896150) 对应源码 `20907f33ea214f35c69ffa664d6a92455c98351b`，十组中九组成功，唯一失败是 [contracts (other)](https://github.com/workhold/moni-aigate/actions/runs/41718896150/job/126206089621)。该run的Go执行命令为 `go test -json -race -count=1 -timeout=10m`，没有 `-p 1`。

该job的42501失败集中在 `internal/periodicbudget` 的四个顶层测试；以下时间均直接取JSONL中的UTC时间：

| 顶层测试 | 失败位置 | 首次42501时间 |
| --- | --- | --- |
| `TestCommanderLimitsRevisionEndpoint` | tenant/key子项的 `APP approve complete funding` | 21:22:14.137 / 21:22:15.479 |
| `TestPBPrefixPGLegacyControlRemainsUncovered` | `configure daily budget through actual Commander` | 21:22:48.487 |
| `TestPBPrefixPGPrecreatedOriginCannotEnrollControl` | `APP approve complete funding` | 21:23:02.093 |
| `TestPBPrefixPGFaultRealDeadlockRetryBound` | one_deadlock_then_confirmed_commit子项的 `APP approve complete funding` | 21:23:11.113 |

同job的 `internal/procurementattemptbirth` 在21:22:07.873开始，21:23:11.824通过，耗时63.951秒。上述周期预算拒权均发生在这一重叠区间。日志还包含 `internal/pricing` 在21:20:45记录的三条 `42501:permission denied for table request_records`，但对应测试通过；它们是该测试的预期拒绝，不能计作此次CI失败。

周期预算fixture的日志只保存SQLSTATE和步骤，没有保存具体拒权函数或关系名。由这些日志不能断言某个特定函数就是实际被拒绝的对象，也不能将42501解释成密码认证失败、连接耗尽或业务锁死。

可通过以下只读命令重新核查精确run：

```sh
gh run view 41718896150 --repo workhold/moni-aigate \
  --json headSha,jobs \
  --jq '{headSha,jobs:[.jobs[]|{name,id:.databaseId,conclusion}]}'
gh run view 41718896150 --repo workhold/moni-aigate \
  --job 126206089621 --log-failed \
  | rg '42501|"Action":"fail"|go test -json -race'
```

## 源码解释与证据边界

[A2权限材料](../../internal/procurementattemptbirth/materials/permissions-v1.sql) 明确执行 `ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC`，并对受信schema和W创建者分别收口。该全局默认ACL属于同一数据库中的创建者，不随随机schema隔离；同一job的包都使用专库installer `moni_test`。

[A2 fixture](../../internal/procurementattemptbirth/attempt_postgres_test.go) 保存原installer的PUBLIC EXECUTE默认值，并在清理时恢复。恢复只影响之后创建的函数；其它包在撤权窗口已经创建的函数不会因此自动补回PUBLIC EXECUTE。[周期预算fixture](../../internal/periodicbudget/tx_fixture_test.go) 在独立schema应用原迁移、通过原 `ProvisionFor` 配置角色，再使用actual APP执行完整资金与预算路径。其历史普通函数调用依赖原有效EXECUTE闭包，随机schema本身不提供创建者默认ACL隔离。

以上源码机制与远端重叠时间一致，支持“跨包并发修改同一创建者的默认函数权限，干扰另一包fixture安装”这一诊断。具体哪一次函数创建失去原EXECUTE、哪一个调用先触发拒权，当前日志没有逐对象证据；本记录不补造该结论。更改单一业务断言、给APP补宽权限或放松42501检查都不能解决此类共享安装状态。

## 修订后的执行合同

[CI工作流](../../.github/workflows/ci.yml) 的真实测试执行命令增加 `-p 1`，使用原包参数和精确顶层过滤表达式：

```sh
go test -json -race -p 1 -count=1 -timeout=10m \
  "${run_filter[@]}" "${packages[@]}"
```

同一runner内按包串行执行，避免A2默认ACL收口与另一包的函数创建重叠。[Makefile](../../Makefile) 的 `make test`、`make verify-mvp` 同样使用 `-p 1`；`make test` 保留原Go默认期限和数据库skip条件，`make verify-mvp` 保留必填专库变量、`-count=1`及600秒期限。

覆盖要求保持：

- CI使用 `billing-0..3`、`server-0..3`、`attempt-0..3`、`identity`、`other` 共14组独立runner/临时数据库；其它runner中的同名installer不共享此数据库的默认ACL。
- 从 `go list ./...` 生成五类完整包归属，须互斥、穷尽且无重复；billing/server与A2 attempt分别发现完整顶层Test/Fuzz/Example、按C序轮转四分区，保持精确锚定过滤。新增包自动进入other，没有删除失败测试或新增skip。
- 保留 `-race`、`-count=1`、包内显式并发、原多连接/多进程争用与所有业务、SQL、锁、fixture期限。`-p 1` 不等同于设置 `-parallel 1`，也不修改应用并发策略。
- CI的10分钟按各个测试进程计算，不能把整个other组的总墙钟时间当作一个包的期限；单包发现步骤仅列名称，不运行fixture。前端、架构、vet、嵌入构建及JSONL/名单上传保持。

## 复现与验收边界

只允许使用专用PG16测试库，实际服务端与URL保持 `127.0.0.1:55439/moni_test`，actual installer为 `moni_test`。其它端点、认证、Asia/Shanghai时区与持久化条件沿[开发手册](../runbooks/development.md#开发验证)及[CI profile合同](ci-profile-contract.md)，不把测试trust配置用于部署。

`-p 1` 只协调同一条Go命令的包调度，无法阻止另一个shell或并行任务在同一专库修改默认ACL。全仓集成、专项PG测试及其它安装验证必须串行；需要并行时使用独立专库与创建者身份，并分别符合各自fixture的显式端点合同。原测试内为验证竞争而发起的并发继续保留。

新增A2反例后，旧整包在本地即累计487.047秒，catalog反例占315.14秒；这不是某次生成请求耗时，也不能据此宣称远端一定超时。为保留原10分钟进程界，将A2单列四份。110个反例仍属原13类，拆为四个顶层及15个隔离fixture；metadata 30项与ACL 19项固定拆15/15和10/9，避免独立实验在同一180秒fixture内累积。全部反例、四份非空/互斥/穷尽、组末真实APP正控均由静态库存检查；实际通过须依完整PG结果。没有TestMain或跨顶层共享前置状态，后续增加时须复核分片合同。

本地独立名单复核：实际发现64包，billing/identity/server/attempt各1包、other60包，五类互斥穷尽；billing210、server205、attempt47顶层分别轮转为53/53/52/52、52/51/51/51、12/12/12/11。12次实际`go test -race -count=1 -list <精确锚定表达式>`与原名单逐项一致，462个顶层均只进入一份；attempt四片各含一个Drift A/B/C/D。原件`.local/moni-release-ci-partitions.json`保存清单/表达式摘要。该步骤只列名称、未运行测试体或PG，不能当作远端通过。

A2完整专库race最终47顶层/1648子项PASS、0 FAIL/SKIP，535.086秒，见[精确机器记录](procurement-attempt-birth-hardening.json)。其结束后再串行显式设置`PB_PREFIX_DATABASE_URL`，重跑上轮CI失败的四个周期预算顶层：`TestCommanderLimitsRevisionEndpoint`、`TestPBPrefixPGLegacyControlRemainsUncovered`、`TestPBPrefixPGPrecreatedOriginCannotEnrollControl`、`TestPBPrefixPGFaultRealDeadlockRetryBound`。结果4顶层/5子项全部PASS、0 FAIL/SKIP，5.567秒；installer的global FUNCTION默认PUBLIC EXECUTE已恢复为true。原件`.local/moni-release-pb-ci-regression.json`为9996字节，SHA-256 `d29ff47c24c302d4abb14661f17643206bf55ad0ee168ae4eab71be934aa58c8`。这是本地串行回归，不能充作新14组远端CI通过。

本次日志诊断与文档校准不构成新的Linux、整仓或供应商验收。修订后的精确提交须重新运行14组CI，并以完整JSONL、名单和实际run终态记录结果；本地串行通过、配置语法正确或旧run中的九组通过均不能代替新CI证据。

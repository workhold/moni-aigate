# 025-A1-M：身份邮件真实进程恢复

2026-11-01，**两个真实进程窗口通过限定本地验收**。依据[冻结合同](../../specs/025-production-ha/a1-mail-process-contract.md)，本次仅扩展隔离[`scripts/browser-qa`](../../scripts/browser-qa)运行器和测试，不修改生产main、identity、mailworker、SMTP实现或配置。

## 实现与证据分层

运行器增加两个非CLI private callback：main启动前安装一次性SMTP屏障，ready后由本run受控关闭/重启当前child。fixture只允许“TCP已接、220前”和“DATA已写私密文件、最终250前”两个固定屏障，context/Close可取消并join。重启保留原环境/keyring/origin，检查同binary SHA，无bootstrap或新身份邮件请求；cleanup继续拥有替换后的child/Wait。

真实第二窗口额外用owner锁本次唯一outbox行，释放SMTP最终250后，按`pg_stat_activity + pg_blocking_pids`证明实际APP LOGIN的生产FinishMail UPDATE正在等待本次backend。只有该物理证据成立，才在4.5秒观察/kill/Wait/连接归零总界内命中窗口；只看到邮箱内容或sending状态不能代替。owner不修改lease/generation/state或任何生产表。

为排除SMTP超时后的retry FinishMail，初始DB查询发出前即记录monotonic点，以原60秒lease已耗时加该点到kill/Wait/零连接全程作保守上界，包含query RTT；必须小于9秒，覆盖生产10秒单IO和20秒总超时。kill前另确认首个final250已flush。APP的2秒DB lock_timeout不会被延长；本次在物理等待6ms时观察并迅速kill，其后backend退出仍在4.5秒总界内，owner屏障在零连接之后才回滚。

两窗口各自独立随机schema和APP/projector角色，SIGKILL后以原60秒lease自然恢复，最终只允许generation/attempt=2。第一窗口一封，第二窗口两封；同Message-ID/收件人/proof，证明的是允许重复同proof的SMTP语义，不能称exactly once。最终state=sent、payload/lease清除，proof仍未消费；不创建客户账号、租户、Key、模型请求或资金。

## 本次命令与状态

- `go test -race -count=1 ./scripts/browser-qa`：最终实现/fixture/守卫专项退出0，1.833s；真实main opt-in未启用的这次运行不算进程恢复验收。
- `go vet ./scripts/browser-qa`：退出0。
- 专属预构建制品：`go build -trimpath -o .local/mail-lifecycle-025/moni-aigate ./cmd/moni-aigate`，SHA-256为`5116676a6f3e3baa1322c7d1ec80f62f5090b8812fae5bf84caa59740219132f`。复用已经验证的前端dist，未执行npm构建。
- 真实进程命令：专用`TEST_DATABASE_URL`仅在进程环境设置，并指定`MAIL_LIFECYCLE_REAL_MAIN_TEST=1 MAIL_LIFECYCLE_BINARY=<上述制品绝对路径> go test -race -count=1 -run '^TestMailLifecycleRealMainRecovery$' -v ./scripts/browser-qa -timeout 240s`。最终退出0，132.811s，记录见[脱敏证据及源码摘要](mail-lifecycle-025.json)。首轮通过后补强上述SMTP计时反例，已完整重跑最终两窗口；没有借用补强前结果作最终验收。

| 最终真实窗口 | 原lease剩余 | SMTP已耗时上界 | DB物理等待观察 | kill/Wait/零角色连接 | generation/attempt | 邮件数 | 总时长 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| claim后、220前SIGKILL | 59,994ms | 14ms | 不适用，SMTP尚未接收 | 5ms | 原1→2 | 1 | 65.722s |
| DATA已持久且250后、FinishMail等待时SIGKILL | 59,993ms | 2,015ms | actual APP等待本owner，query age 6ms | 2,004ms | 原1→2 | 2 | 65.700s |

两个run均正常启动替代main、保留原lease后自然恢复，无第二proof/outbox；原challenge/HMAC和邮箱消息身份校验通过，终态sent清除payload/lease，proof未消费；仍只有3个合成平台用户，0客户tenant/Key/模型请求/funding事实。最终main graceful退出、APP/projector连接归零，schema/角色和全部listener已删除/关闭，私密邮箱/子进程日志/manifest目录显式删除并确认不存在。专属制品和仅含脱敏测试metadata的本地日志不含凭据或proof。

每份最终证据只导出binary摘要、窗口/时间/计数/代次与身份/清理布尔值；不导出邮箱、proof、密码、token、正文、DSN或SQL错误。最终检查所有child/listener退出、角色连接归零、schema/角色和私密文件清除。受控本地fixture与专用PostgreSQL的结果不代表外部SMTP、容器/1Panel、多活或数据库灾备验收，也不补齐025全部生产门禁。

# 022-B1 SQL 设计原型验证记录

2026-11-01。范围为 [B1 工作稿](b1-contract.md)、[可执行核心](b1-core.sql) 和 [ACL 模板](b1-permissions.sql)。这是**已经根复核冻结的B1设计合同与隔离SQL原型**，不是已经应用的迁移或已经上线的支付功能。根与独立reviewer已按合同§1三个入口确认B1核心冻结；B2/B3 Go、HTTP、UI 和进程接线仍独立推进。

## 环境与执行身份

专用 PostgreSQL 16.15：`moni_test@127.0.0.1:55439/moni_test`。每轮核对 DB/user，创建随机 schema，调用仓库实际 `migrations.ApplyThrough(...,"019_operations_alerts.sql")` 显式安装 001–019，后续020不会静默进入该fixture，随后安装 B1 核心。marker 使用当前 DB/schema OID 与三个随机角色。

owner 仅执行 DDL、基础 tenant/users/membership seed、marker、角色/ACL provisioning、fixture 时钟调整和 finally 清理。APP、payment worker、projector 各自是独立 LOGIN（随机密码仅内存，不写文件或日志），分别建立连接；所有业务写入与权限断言均以实际登录角色执行，核验 `current_user=session_user`，没有 `SET ROLE` 冒充业务账号。角色无 superuser/create-role/create-db/bypass-RLS/replication、无 owner membership、无目标 schema CREATE；角色本身未接入生产 main。

超时测试没有等待真实 60s×20：fixture owner 仅暂时关闭目标状态 trigger，调整 `next_attempt_at`/`lease_until` 两个时钟列，立即跑 deferred constraints 并恢复 trigger。未直接设置 state、generation、attempt_count 或资金事实。10/20 次 claim、签名 not_seen 恢复、终态 ACK/耗尽、拒绝第11/21次均由 worker LOGIN 实际迭代。因此这是时钟加速的 FSM 验证，不是实际网络超时/进程崩溃测试。

命令：

```sh
go run .local/022-b1/ddl.go
go run -race .local/022-b1/prototype.go
```

两者退出 0。日志分别为 `.local/022-b1/ddl-result.txt`、`.local/022-b1/prototype-result.txt`。末行分别是 `SCHEMA_CLEANED`、`SCHEMA_AND_LOGIN_ROLES_CLEANED`。`.local` 是工作区临时实验材料，不属于应用交付；本记录保留可审查的验证范围和内容摘要。未调用真实 PSP，未启动 main/浏览器或修改 B0-P/M/Q。

## 实际通过

| 合同部分 | 正例与拒绝反例 |
| --- | --- |
| 人类 command/change | publish；activate；no-op 保 revision；第二次真实切换与重新启用；create order 冻结 tuple+规范 checkout outbox+terminal+audit。accepted 缺资源、command 缺 terminal、version 缺 change、旧 revision、直接改 SKU root、accepted 缺 audit 均拒绝 |
| A 兼容 | 实际现有 `billing.New(app).CreateCredit/ApproveCredit`，首笔 1000；随后通过同一 A 路径补至 MaxInt64。不是 owner 直接造 A 流水 |
| P 复用 | 使用当前 `paymentproto` Canonical/Sign/Verify 生成并验证通知和 query，随后才保存 canonical 原件/evidence/processing；没有修改 P 或接受 trusted flag |
| B 资金完整图 | worker 实际 claim 后按 order→identity/selection→tenant→savepoint→accounts ID 顺序写 source/effect/journal/两条分录/receipt/唯一 schema1 credited/audit，最终 processing CAS。首笔 B=100，钱包合计1100。逐项去掉 effect/receipt/credited/audit/processing 均在提交时拒绝；未配旧 A 源表 SELECT 的 worker 可验证其 sandbox journal |
| overflow 身份永久保留 | 钱包经 A 真实补至 MaxInt64 后，B SQL 加法实际报 22003；回滚资金 savepoint，保留 identity=1、selection=1、source=0、overflow review=1，同代 processing_review。未把溢出候选释放给第二 transaction |
| mutation FSM | 无签名 not_seen 不能 retry；当前代签名 not_seen 才允许相同 ID/body 重试；旧代 not_seen 无权授权新代。实际迭代10次 claim，第10次 lease过期→submission_unknown+attempts_exhausted+唯一 query；拒第11次、代次/预算 reset；确定 ready reply 先保存 observation 再 completed；缺 observation ACK、completed重开均拒；accepted cancel intent 后 signed not_seen 不能补发 checkout |
| query FSM | 无快照不能换 query child；保存快照后下一代允许新 child；原 child FK 不阻新 generation；stale query ACK WHERE旧代为0行。实际迭代20次 claim，第20次过期→review_required；拒第21次及终态重开；signed completed snapshot 可把 query 与原 unknown mutation 标 completed，不产生资金 |
| evidence FSM | capture 无 source/review不能 ACK；拒重置代次/预算；第2代 claim 使用旧1代创建 identity立即拒绝。实际20次 claim，第20次过期→processing_review；拒第21次 |
| 并发锁图最小交叉 | 两个真实 worker 连接各锁不同 order `NO KEY UPDATE`，同 provider transaction 竞争；owner只读观察到第二连接在唯一 identity 上等待。首方 overflow 保身份，后方读到胜者写 transaction_conflict review，身份总数1、source0；无死锁，race detector无报告 |
| 实际 ACL | worker 不能 SELECT users/membership/session/credit_orders/vouchers/channel_credentials；不能真实 UPDATE tenant、写 manual journal、无流水改 balance。APP不能写 B effect。projector不能读 payment order。三角色不能 SET ROLE owner、CREATE FUNCTION；APP/worker不能改 marker |

## 内容指纹

本记录对应以下 SHA-256；如核心或模板有变更须重跑相关原型并更新指纹，不能只改记录：

| 内容 | SHA-256 |
| --- | --- |
| `b1-core.sql` | `fb367858ee12f00f188d819b3e5bb3ff6432e2dc8e301da41f233738cdeaca98` |
| `b1-permissions.sql` | `97e473cea671848b09ce481ee5c0d38100300cab8a0700ce4fa9535fbd3a555c` |
| `.local/022-b1/prototype.go` | `c91ce8157b618c47215c685ed709594db140c70b58fafd094238f2d00a13b308` |
| `.local/022-b1/prototype-result.txt` | `41ede12e7126991df49bedf5b83648adcb5c1cd91818bf77c21399a1cdfb8b75` |

## 明确未验收的后续实施范围

- 没有生产迁移、`internal/payments` Go API 或 `ApplyFundingTx` 第三来源实现，未扩实际 VerifyFunding/Rebuild/funding-summary；SQL 的旧 A 函数重命名仅限原型，生产必须保留原 OID。
- 未覆盖全量同 order 第二 capture、同额错绑排列、签名重投异体、全部断点 COMMIT 未知，也未完成与 voucher/Reserve/refund/login 的全部并发交叉。最小 cross-order identity 等待通过不能宣称整个系统无死锁。
- 未启动支付 worker/main、真实通知 listener、Recovery/Guard、off 旧单收尾、角色撤销先后、HTTP DTO/分页/限流或浏览器；属于 B2/B3 实施验收。
- 没有真实 PSP、在线商户、现金收付、退款/拒付、商品 lot 或财务会计验收。当前 wallet 依旧是本地模拟语义。

根汇总时统一更新架构与清单，本子任务未独立 refresh。现有能力状态应写成“B1核心设计/SQL原型通过并已冻结，B2/B3未接线”，不能写成商业支付能力已交付。

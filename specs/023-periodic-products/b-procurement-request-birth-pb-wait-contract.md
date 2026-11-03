# B-P4-A1：真实 PB control 等待后的售价与期限合同

2026-11-04；承接 [A1 writer](b-procurement-request-birth-writer-contract.md)、[客户预算矩阵](b-procurement-request-birth-budget-contract.md)、[钱包等待](b-procurement-request-birth-wait-contract.md)及 [HLD](../../docs/design/HLD.md) / [LLD](../../docs/design/LLD.md)。**状态：先冻结后实施；根已完成2顶层/6子项专库race验收，实际结果见[验证记录](../../docs/verification/procurement-request-birth.md)。**

本片只新增本合同与根审定后的 `internal/procurementrequestbirth/request_birth_pb_wait_postgres_test.go`。复用同包现有真实 APP fixture 与预算/等待 helper，不修改已有 SQL、摘要、fixture、测试、001–022、ACL 或运行入口。PG 只由根在专库串行运行；代理只做显式清除数据库开关后的编译。

## 1. 原链路与待关闭的缺口

真实依据为 [Reserve](../../internal/billing/reserve.go)、[PB 锁](../../internal/periodicbudget/tx_locks.go)、[Commander](../../internal/periodicbudget/commander.go)及其[配置命令](../../internal/periodicbudget/commander_configure.go)、[客户准入/效果](../../internal/periodicbudget/tx_customer.go)和[原020](../../migrations/020_periodic_budgets.sql)。不复制业务 Reserve 实现，不安装新业务 hook。

原 Reserve 的顺序是身份/Key/模型父锁与当前授权 → 价格域锁与 DB `clock_timestamp()` 选零售价 → 初次 request deadline 检查及金额计算 → `LockCustomerTx` → 钱包锁 → request/reservation INSERT → 原020 `pb_stamp_request` 在所有这些等待后取得 `pb_now()` → Admit/PB 与账本效果 → 按 admission 再检查 Key/request 期限 → 原 COMMIT。

`LockCustomerTx` 先执行缺省 scope 同步，再按 control ID 排序，以真正 `FOR NO KEY UPDATE` 取得 tenant/Key control 行。已配置 control 的 owner 行锁可以只阻塞这一步，不持有 tenant、Key、模型、价格、钱包或首配 advisory 锁。已有钱包等待证据不能替代这条 PB 控制层路径。本片验证等待前的售价仍冻结，而 admission 及期限检查使用等待后的真实时刻；不把 request.created_at 等同于任一业务采时。

## 2. 合法基线与锁证据

每个场景使用全新现有 `newFixture`：限定实际 PG16 专库 `127.0.0.1:55439/moni_test`、安装 LOGIN `moni_test`、随机 schema、真正受限 APP LOGIN、W NOLOGIN。先用原 APP `Release(not_sent)` 关闭 fixture 的合法 legacy 预占，核旧请求没有补生 A1 来源，钱包恢复 available10000/held0、Key held0。独立新建 active admin 仅作原配置命令的管理身份，不更改 finance_operator/approver。

两种控制位置都执行：

| 被阻塞 scope | 原 Commander 配置 | 未配置的 scope |
| --- | --- | --- |
| tenant | 原 APP `ConfigurePlatformTenant`，revision0 → 1、enabled=true、utc_day、limit100000 | Key |
| Key | 原 APP `ConfigureKey`，当前 owner 成员，revision0 → 1、enabled=true、utc_day、limit100000 | tenant |

配置使用真实 receipt/control ID与原下一 UTC 日 `effective_from`，不得手写 control、operation/version、window/allocation/event。大限额只避免自然边界附近的偶然 cap 干扰，不以 scheduled 配置声称 active enforcement。配置/价格/期限准备均在失败前基线内。

owner 单独开启事务，只执行按实际 control ID 的 `SELECT id FROM periodic_budget_controls WHERE id=$1 FOR NO KEY UPDATE`，读该连接的 `pg_backend_pid()`。原 APP Reserve 用新 request/idempotency UUID 在独立 goroutine 执行；原 RC、lock_timeout3s、statement_timeout5s不变。

观察连接通过 `pg_stat_activity` 的随机 APP LOGIN 与 `pg_blocking_pids` 精确确认：恰有一个 APP backend 被该 owner PID 阻塞，而且其当前 SQL 是原 control 查询（包含 `periodic_budget_controls` 和 `FOR NO KEY UPDATE`）。不能用 goroutine 已开始、固定 sleep、客户端报错、其他等待或一条未冲突锁冒充。owner 只持这行 control 锁，未持前层父锁，故这也是原 Reserve 已通过初次选价/期限检查并到达 PB 锁层的证据；没有真实阻塞就失败，不放宽为成功。

观察仅读取必要的合成 fixture 状态，不输出 DSN、凭据、正文或其他会话资料。等待、取结果、取消与释放均有界；失败清理先取消原请求并释放本片实际行锁，不遗留 goroutine/连接或重发模型请求。

## 3. 固定场景与独立 oracle

计划两个顶层 `TestPostgresA1PBControlWait*`，每种 scope 各执行一项跨价与两项期限，共六个独立子项；不加 active cap 的空测试或动态 skip。

### 3.1 选价后跨 scheduled 零售价

完成 control 配置后，使用原 catalog 发布未来约1500ms生效的真实双价版本 input2000000/output3000000。切点由 DB `clock_timestamp()` 决定并读回 immutable `effective_at`；原已有价格 input/output均1000000。随后持 control 锁、启动原 Reserve、确认真实控制层阻塞，再以真实 DB 时钟观察到切点后 COMMIT 解锁。不改变系统时钟、`pb_now()` 或任一 SQL timeout。

成功结果必须仍是旧实际 price ID、两价1000000、input100/output20、R120；解锁后独立当前价查询必须选到新价。request 与 Origin 各自保存相同的旧price/费率、`retail_priced_at` 和 `pb_admitted_at`，满足 `retail_priced_at < effective_at <= pb_admitted_at`，不能等待后重选价或把新价倒填到来源。`checkBirth` 核完整映射、root/step/head、actual APP OID和真正COMMIT。

另核配置的唯一 scope：request/source marker等于真实 control ID，另一槽 SQL NULL；一条原 new allocation、覆盖实际 admission 的同control窗口、一条 reservation effect，upper/held/source_held=120、spent/source_spent=0、revision1。原钱包只available减120/held加120，Key held加120；原ledger与 `pb_check_customer`通过。两阶段时间分别处于原调用前后真实 DB 采样之间，不强求相等或采样与 created_at 相同。此案不测 UTC 窗口跨期。

### 3.2 等待后 request / Key 到期

每种 scope 下分别准备 DB clock约1500ms后的有限 request deadline，或在原请求开始前为现有 Key 设置同样真实 `expires_at`（合法父配置，非 control/来源/资金改写）。另一期限保持有效。配置后读取完整失败前快照，再持真实 control 行锁并调用原 Reserve；确认所需控制层阻塞后，等待 DB clock到达该精确期限，最后解锁收取原结果。

| 到期对象 | 必须来自原 post-admission 分支的结果 |
| --- | --- |
| request | `*billing.Error`，Code=`request_deadline`，Message=`request deadline expired`；不能是初次 `request deadline is invalid or expired` |
| Key | `*billing.Error`，Code=`invalid_key`，Message=`key is not active`；真实控制层阻塞同时证明原前置授权已通过 |

失败之后使用现有完整 `birthBudgetSnapshot`：原18表，加settlements/reconciliation_cases/billing_profile_blocks及配置/价格/分组等本片相关表，全行全字段逐字等于调用前。必须无本次 request/reservation/idempotency、source/head/root/step、window/allocation/event、ledger/Key held、audit/outbox/recovery残留；既有已提交配置、legacy及其资金终态不变。不能只查新root数量或以五关系validator成功替代资金回滚。

仅执行原 Store.Reserve 和本地管理/观察，不创建attempt、不向任何模型供应商发请求。未到达原分支、超时、cancel或清理失败均保留真实错误，不改断言、重新装候选或重试成成功。

## 4. Active cap 的真实可达性研究结论

在本片“fresh隔离schema＋真实时钟＋有限短程”的条件下，**不能保证合法创建一个已经active的control**：

1. 原020 INSERT guard无条件覆盖caller的 `tracking_started_at=pb_now()`、`effective_from=下一UTC日/月边界`；表CHECK也固定这两个时刻的日历关系。
2. 之后control UPDATE明确禁止改变scope/period/tracking/effective/xid；原 Commander只允许改enabled/limit并递增配置revision，不会把effective提前。先disabled后enabled、切换limit或重放operation都不会缩短首次生效等待，period也不可改。
3. 原 `pb_gate` 仅在 `at_time>=effective_from` 时核cap；原request的 admission由真实 `pb_now()`生成。伪传未来at会被observe的not-in-future检查拒绝，也不是原Reserve的admission；旧PB测试替换 `pb_now` 的calendar fixture不符合本片合同。

因此本片不人为回改control、不禁guard、不mock时钟、不切系统时间、不把scheduled限额通过写成active cap，也不为赶进度实际等待下一天。自然越界偶然发生也不能替代独立冻结的active cap/争用及窗口跨期矩阵。

合法active验收需要另外管理的已在真实前一周期经原 Commander 创建并保留完整receipt/source的隔离fixture，在自然到达其effective_from后使用原入口；或将来另行冻结与实际认证可接受的历史导入入口。当前随机fresh fixture、原001–022及本片没有此导入能力。上述是后续条件研究，不是本片已实现/运行的认证方法；active cap、active争用、carry及窗口跨期继续明确未验。

## 5. 交付、编译与保留范围

根审定后代理只新增指定test文件，必要新 helper使用独有前缀；复用现有fixture与helper，不改其权限/SQL/时限。编译显式移除 `TEST_DATABASE_URL`、`PB_PREFIX_DATABASE_URL`、`PROCUREMENT_IDENTITY_DATABASE_URL`、`PROCUREMENT_ATTEMPT_RULES_DATABASE_URL`，使用 `go test ./internal/procurementrequestbirth -run '^$' -count=1`，只记编译成功。

根在同一专库串行运行 `go test -race ./internal/procurementrequestbirth -run '^TestPostgresA1PBControlWait' -count=1`，记录真实PASS/SKIP/FAIL与清理；源码完成、编译、静态设计或旧钱包专项不代替此PG证据。实际通过才由根更新验证与统一架构、执行reviewed清单同步。

本片不覆盖COMMIT未知、首配advisory竞争、control改配争用、active cap、UTC窗口跨期、完整catalog、A2/A3/attempt/finalization/settled链、正式迁移、生产部署或供应商认证；原未知消费不重放及单活边界保持。

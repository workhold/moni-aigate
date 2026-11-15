# 034.3b 币价版本验证

日期：2026-11-15。范围只含 `coin_rate_versions`、CNY 创世版本、paid receipt 买入版本冻结、存量回填与相应角色边界；不包含 034.4 现金退款金额计算、退款版本选择、状态机、HTTP、控制台、告警或生产写权限。

状态：**034.3b 实现与本地验收通过。** 本文件不证明真实服务器/1Panel 部署、真实供应商、正式支付、生产对账、生产高可用或后续 034 分片已完成。

## 规格与冻结记录修正

空库没有可作为 maker/approver 的业务用户，原合同的非 NULL 操作者要求无法创建 CNY 创世版本。已先修订 `specs/034-virtual-coin/spec.md` §5.8 与 `implementation-freeze.md`：

- 仅精确的 CNY `1/1`、`1970-01-01T00:00:00Z` 创世版本允许 maker/approver 为 NULL，不创建伪造管理员或系统用户。
- 其他版本必须同时有真实 maker/approver，满足 `single_operator OR approver<>maker`，并满足 `effective_at>=created_at`，不得回填到过去。
- USD 测试版本只能由 migration owner/test fixture 以真实测试用户写入；生产没有调价 API、控制台、写命令或默认汇率。

## 已实现边界

- `current_008_coin_rate_versions.sql` 建立不可变币价版本、`(fiat_currency,effective_at)` 唯一约束和精确有理数分子/分母。
- `funding_receipts.buy_rate_version_id` 对 paid 必填、非 paid 必须为 NULL；`(buy_rate_version_id,fiat_currency)` 复合外键保证版本与付款币种一致。
- 存量 manual/sandbox paid receipt 回填 CNY 创世版本，非 paid 保持 NULL。迁移回填只临时关闭既有 immutable/source guard，并在同事务恢复后安装新约束；专项 fixture 本身不禁用约束。
- 人工 paid 和沙箱 paid 仅在各自入账事务内按数据库时刻选择最新生效的同币种版本；无版本返回 `coin_rate_unavailable` 并整体回滚，`fx_note` 不参与计算。
- `cash_refund_orders` 的两个 nullable 币价外键已安装，但不提供 034.4 的退款计算或状态语义。
- APP、payment worker、projection 对 `coin_rate_versions` 只有 SELECT，所有运行时角色均无 INSERT/UPDATE/DELETE；历史离线 schema 只在表确实不存在时使用旧能力基线。

## 请求路径隔离修正

首轮真实 PostgreSQL 隔离用例发现：Go Reserve 虽未直接查询币价，但余额 guard 间接调用的 paid journal validator 查询了版本表。该实现不符合“请求路径没有汇率”。

现已将 paid journal validator 改为只检查冻结引用非空；版本存在性和币种一致性继续由数据库 CHECK 与复合外键强制执行，没有放宽约束。静态 Go 依赖检查及把 `coin_rate_versions` 改名后的 Reserve、finalization、Settle、消费退款真实 PostgreSQL 用例均通过。

## 已通过的定向 PostgreSQL 验证

所有数据库用例均显式使用 `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`，没有新增 SKIP：

- `TestPostgresCoinRateGenesisPaidSourcesAndDirectSQLGuards`：CNY 创世数值、epoch、NULL 操作者；paid 冻结与 bonus NULL；版本 UPDATE/DELETE、回溯、同币种同生效时刻重复、paid 缺版本、非 paid 带版本、版本/币种不匹配均拒绝。
- `TestPostgresCoinRateUSDFailClosedThenExplicitVersion`：USD 无版本时审批返回 `coin_rate_unavailable`，订单保持 pending，receipt/journal 为零；写入真实用户签署的 USD 版本后成功，后续订单选择最新版本，历史 receipt 不变。
- `TestPostgresReserveSettleAndConsumptionRefundWithoutCoinRateTable` 与 `TestCoinRateLookupIsIsolatedFromRequestPaths`：真实请求路径无直接或间接币价读取。
- `TestPostgresSandboxPaidReceiptFreezesCNYGenesis`：受限 payment worker 的沙箱 paid receipt 持久化 CNY 创世引用，journal validator 通过。
- `TestVirtualCoinMigrationPreservesAmountsAndCanonical`：populated 升级保留金额与计量 canonical，paid 回填完整，非 paid 无引用。
- `TestCoinRateMigrationBackfillsCommittedSandboxCapture`：先在 current_007 用真实 app/worker LOGIN、完整签名证据和全部约束提交 sandbox capture，再执行 current_008；历史 receipt 回填 CNY 创世，journal 与五桶 checker 保持有效，重复迁移幂等。
- `TestPaymentRoleStaticReviewedFunctionBaseline`、`TestPaymentRoles*`、`TestWalletSourcesRoles*`：受审函数 SHA-256、当前三角色表/列/函数权限及运行时版本写拒绝通过。

## 其他已通过检查

- `npm --prefix web test`、`npm --prefix web run typecheck`、`npm --prefix web run build` 通过；本片没有新增前端能力。
- `go vet ./...`、`go test -run '^$' ./...` 与 `go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` 通过。
- `internal/server`、`web/src`、`cmd` 静态搜索无币价版本、分子/分母或买入版本入口，确认未新增调价 API/控制台。

## 最终回归与边界

已完成的全包 race：

- `internal/billing`：379.306s。首轮发现旧 SQL 负控缺少新必填买入版本，已为合法 paid 基行补上 CNY 创世引用后全包通过；orphan、金额、journal、outbox 等原负控未删除或放宽。
- `internal/operations`：131.014s。
- `migrations`：163.621s。
- `internal/server`：553.155s。
- `internal/identity`：61 个测试按四个交错分片全部通过，分别为 85.386s、105.365s、57.740s、64.973s；未降低 bcrypt cost、未提高单分片 240s 限制，名单数量无遗漏。

其余 72 个包已以 `go test -race -p 1 -count=1 -timeout 240s` 全部通过，覆盖 inference、mail、maintenancetrust、media、mockpsp、payments、periodicbudget、pricing、procurement 全部子包、provider、realtime、routing、测试支持和 QA 脚本；与上述 billing/operations/migrations/server/identity 合计 `go list ./...` 的 77 个包，无包遗漏、无新增 SKIP。`go run ./scripts/check-procurement-layout` 通过。

统一架构 reviewed refresh/check 已通过，最终文件清单与复核理由见 `architecture-manifest.json` 和 `docs/architecture/changes.md`。034.4 的现金退款金额计算、缺买入/退款版本失败关闭、并发/恢复/HTTP/控制台仍须独立验收，不能由本片创世和 receipt 测试代替。

# 034.6 展示约合与显示名验证

日期：2026-11-15。范围只含独立展示版本、公开当前配置、admin追加/回查、前端显示名与约合。真实汇率供应、会计币价调整、余额重估、真实支付/资金转移、生产部署/HA/性能/灾备不在本片。

状态：**034.6实现与本地验收通过；提交和推送进行中。**

## 已冻结的实施合同

- `coin_display_versions`独立于会计币价，创世为“币”且约合关闭；后续版本追加不可变并记录真实admin actor。关闭时三个换算字段全NULL，开启时只允许CNY/USD和正int64有理数。`current_010_coin_display.sql`的受审SHA-256为`05232da9e8e23b05da38e80ff8aca2da6e6acc466c5db16ec333ec67167e346e`。
- `GET /api/coin-display`只返回当前安全字段；admin历史、operation回查和PUT分离。PUT要求近期再认证、CSRF、严格JSON、operation_id/CAS/二次确认和`coin_display.updated`审计。
- 公开读取失败只保留默认“币”标签且不显示约合；配置不写localStorage。启用后前端BigInt向零截断，显示版本与“仅供参考”，不进入任何资金字段或请求。

## 已通过的定向 PostgreSQL / HTTP 验证

全部使用显式`TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`，没有新增SKIP：

- `TestCoinDisplayMigrationGenesisValidationAndImmutableHistory`：创世、非法名称/币种/比例/actor拒绝、不可UPDATE/DELETE、重复迁移幂等。
- `TestCoinDisplayMigrationUpgrades009`：009已有当前基线只追加010与创世。
- `TestPostgresCoinDisplayAppendReplayCASConcurrencyAndPublicView`：admin/公开读取、追加、完整重放、异意图/过期冲突、并发仅一个成功、关闭约合及审计计数。
- `TestPostgresCoinDisplayActualApplication`：受限APP可SELECT/INSERT，不能改删历史。
- `TestPaymentRoleStaticReviewedFunctionBaseline`与完整`internal/operations`包：68个既有函数不变；当前/历史表权限、capability和角色矩阵通过，完整包74.653s。
- `TestHTTPCoinDisplayPublicAdminReplayAndStrictInput`：匿名安全字段、admin权限、严格NULL/比例/未知字段、再认证、CSRF、重放、冲突与operation回查。
- `TestPostgresCoinDisplayRenameDoesNotAffectGateway`：展示表改名后真实网关请求与结算仍成功，证明没有隐藏资金依赖。
- 上述034.6定向测试随后以`go test -race -p=1`全部通过：billing 3.085s、server 6.690s、operations 1.607s、migrations 2.590s。

## 已通过的前端验证

- `web/tests/coin-display.test.mjs`：默认无约合、CNY/USD、负数向零截断、int64乘积不溢出、版本/提示、非法组合、actor与历史、命令二次确认、读取失败清除旧约合。
- 完整`npm --prefix web test`：364/364 PASS，0 FAIL、0 SKIP；`npm --prefix web run typecheck`和生产构建通过。
- `go test -run '^$' ./...`、`go vet ./...`通过。

## 全仓与架构收口（进行中）

- 首轮`go test -p=1 ./... -count=1`中76个包通过；migrations因运行中重命名测试导致旧`_testmain.go`引用而编译失败，未计为通过。随后对当前源码重跑完整`go test ./migrations -count=1`，86.479s通过，覆盖该包及重命名后的测试。
- `TEST_DATABASE_URL=... go test -race -p=1 ./... -count=1 -timeout=1200s`：全部77个Go包通过，作为最终统一回归。`internal/billing`355.066s、`internal/identity`314.961s、`internal/operations`94.375s、`internal/server`523.142s、`migrations`93.755s；保留项目标准包上限，没有降低bcrypt成本或增加SKIP。
- 统一架构reviewed refresh/check与`make check`已通过，最终文件清单与复核理由见`architecture-manifest.json`和`docs/architecture/changes.md`。034.6不得被解释为会计币价调整或外部汇率认证。

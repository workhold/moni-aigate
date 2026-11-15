# 031 deterministic mock 部署验收 harness

状态：**测试工具与限定专项 PASS；扩展部署场景和浏览器终验未执行**。本记录不是 029 同一提交全量复验，不是供应商认证，也不把历史部署结果升级为当前提交 PASS。

## 边界与执行入口

harness 消费已经运行的 app/worker HTTP 入口，只读专用 PostgreSQL `127.0.0.1:55439/moni_test`。它不调用 Compose，不启动、停止或重启现有 app/worker，不运行 `go test ./...`；唯一的临时容器是挂在 app 既有 Docker network 上的 mock upstream，退出时删除。候选 `a=reject_429`、`b=normal`，所有渠道名带 `mock-` 前缀和唯一 run ID。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PHASE1_BASE_URL='http://127.0.0.1:18081' \
PHASE1_APP_CONTAINER='phase0qa-app' \
PHASE1_WORKER_CONTAINER='phase0qa-worker' \
PHASE1_ADMIN_EMAIL='admin@example.com' \
PHASE1_ADMIN_PASSWORD='…' \
PHASE1_MOCK_IMAGE='moni-aigate:phase0-qa' \
PHASE1_DATABASE_SCHEMA='隔离 schema 名（仅非 public QA 需要）' \
scripts/phase1-mock-acceptance.sh
```

`PHASE1_MOCK_CONFIG_DIR` 可指定 Docker 可共享的临时配置目录，默认仓库根目录，用于 macOS Docker Desktop bind-mount。`PHASE1_DATABASE_SCHEMA` 只影响只读事实断言。

`PHASE1_MOCK_CONFIG_TRANSPORT` 默认是 `bind`，保持原 `-v host/config.json:/etc/moni/mock-upstream.json:ro` 行为。`colima` 等宿主机目录不可共享的环境可显式设置 `PHASE1_MOCK_CONFIG_TRANSPORT=copy`：harness 改用 `docker create` → `docker cp - <container>:/etc` → `docker start`，tar 中只有 `moni/mock-upstream.json`，不再依赖宿主挂载或 `/etc/moni` 父目录预存在。宿主私有配置固定为 `0600`；考虑到容器以 `USER moni` 运行，tar 内非生产 fixture 固定为 `0644`，避免 root:0600 造成 permission denied。配置内容不会写入 stdout，健康检查及失败清理不变。

### 登录 pacing

`API.login` 对 admin/operator/approver/owner 及故障路由重建共用同一 pacing：第一次完整 login+reauth 不等待；每次成功后记录完成时刻，下一次至少间隔 `12.05s`。这遵守服务端 `login:<ip>` 每 12 秒一许可、burst 5 的限制，不捕获或重试 429，也不放宽认证断言。hermetic fake-clock 测试覆盖首次等待为零、后续间隔和失败不重试。

### 可选浏览器账号交接

只有显式设置 `PHASE1_UI_ACCOUNTS_FILE` 时，provision 才额外写出浏览器后续终验所需的 owner/operator/approver 随机账号，以及 `base_url`、tenant、price group、六个模型的逻辑 ID/公开名/别名/协议。约束如下：

- 路径必须是绝对路径、位于仓库外、父目录已存在，并且目标是一个新文件；已存在文件或符号链接会失败，不覆盖。
- 文件权限固定为 `0600`。调用者应选择仓库外的私有缓存目录。
- 文件包含这三个账号的密码，供隔离浏览器登录；密码不会写入 stdout 或本证据文件。
- 文件不包含客户 API Key、会话 Cookie、CSRF、Authorization 或其他会话材料；导出器也会拒绝这些字段。
- 未设置该环境变量时，不创建账号文件，保持原默认行为。

该交接只准备账号和元数据，不表示桌面/390px 浏览器操作已经执行或通过。

## 部署场景的实际请求断言

当前脚本已实现但**尚未在本分支执行**以下扩展场景：

1. **P1-CAT-02 六协议别名调用**：Chat、Responses、Messages、Embeddings、Rerank、Gemini 全部使用别名发真实 HTTP 请求；生成协议覆盖 JSON/SSE。断言响应回显别名、`request_records.requested_model_name=alias`、`model_id` 仍是逻辑模型、恰好 a→b 两个 receipt/attempt、一个 settlement。另一个公开名 Chat 对照请求必须复用相同路由和编译组价。
2. **P1-PR-02 编译价格实际计费**：六模型先发布默认价，再以 `0.5` 倍率 preview/compile，核对该组绝对 input/output rate、基准版本和新版本 ID，随后把 tenant 分配到该价格组。每个成功请求都联查 `request_meter_snapshots`、settlement、finalization 和 `meter_event_quantities`，断言实际使用编译后的 price version，且 `charged_amount=ceil(input×input_rate+output×output_rate)`，只合并取整一次。管理操作成功本身不计为计费通过。
3. **可信无消费切换**：六协议正常请求均要求恰好 a→b 两个有序 receipt、两个 attempt、一个收费 settlement。
4. **未知/可能已消费不重放**：`drop_after_headers`、`drop_after_events:2`、`error_after_usage`、`conflicting_usage`、`stall_first_event` 和 `missing_usage` 各自按 nonce 断言恰好一个 mock receipt、一个 attempt，且没有成功收费。每个故障前都通过当前路由发布 API 创建新的双渠道/新 credential 身份并激活新 revision，避免前一故障的 circuit/cooldown 把后一故障变成零 attempt 的 `no_available_channel`；这不是绕过健康门禁，而是隔离独立故障用例。协议、usage 和资金分别断言：
   - drop headers/首事件 stall：protocol failed、usage missing、释放且零收费；
   - drop events：protocol failed、usage missing、已写部分内容后待核对；
   - error after usage：protocol failed、提前 usage 仅为 provisional、待核对，不把它当 confirmed final；
   - conflicting usage：protocol completed、usage conflicting、待核对；
   - missing usage：protocol completed、usage missing、待核对。
   `stall_first_event` 的客户端等待上限大于网关首事件超时，避免把客户端超时误记为网关分类。
5. **故障指令的内容事件**：provider 层明确断言 `conflicting_usage`、`drop_after_events` 和 `error_after_usage` 都先产生 `Event.Content=true` 的可提交事件，再进入各自协议/usage 终态；部署状态断言附带 request ID 和响应摘要，避免再把零派发 503 误认为流已提交。
6. **P1-CAT-01 显式公开目录**：provision 对每个新模型先读取 `public_revision`，再调用 `PUT /api/platform/models/:id/publication` 设置 `public_visible=true`。模型默认私有，只有这六个 QA 模型被显式公开。随后无会话读取 `/api/public/models`，逐一核对六模型、别名和默认 price version，确保公开 `/pricing` 不再是空目录；不把 tenant 组价当作公开默认价。

未知/释放场景的 settlement 数及 `charged_amount=0` 也会核对；已知 cost 必须有金额，unknown cost 不得填零金额。

## 显式 Go 测试绑定

`--list-go-test-bindings` 输出机器可读绑定。绑定只调用现有生产 routing/provider/server/catalog，不增加第二套路由、计量、报价或资金事实源。

| 场景 | 非 PG Go 证据 | PostgreSQL 补充证据 |
|---|---|---|
| 3:1 权重分布、每请求一个 attempt | `internal/testsupport/mockupstream.TestPhase1ThreeToOneWeightDistribution`：固定 PCG 种子、4000 请求、4σ 容差 | — |
| 高优先级 capacity full 降级与 `capacity_full` 理由 | `routing.TestAcquisitionDecisionsCoverEveryCandidate` | — |
| 连续三次故障 open、30 秒门禁、half-open/恢复 | `routing.TestHalfOpenNeutralDoesNotReopenOrProveSuccess` | — |
| cost first | `routing.TestCostFirstUsesBothDimensionsWithoutInt64OverflowOrRounding` | `server.TestCostRoutingSelectsLowerCostAndKeepsRetailPrice`，含 `higher_cost` 与零售价不变 |
| 未知成本拒绝、采购单价上限 | `routing.TestCostUnknownAndUnitCapsAreHardFilters` | `server.TestCostRoutingUnknownAndRateCapsApplyToAllSixProtocols`，六协议零上游调用、零残留冻结 |
| connection refused 最多两次 | `TestPhase1ConnectionRefusedUsesAtMostTwoAttempts`：连续两个 not-sent，第三次拒绝，健康第三候选零 receipt | — |
| 可信 a→b | `TestPhase1TrustedRejectionAThenBHasTwoReceipts`：同 nonce 恰好 a、b 两条 | `server.TestGatewayPostgresSafeFailoverHasOneChargeAndTwoPosts` |
| stall/drop events/error usage/conflicting usage | `TestPhase1StreamFaultsHaveOneReceiptAndNoReplay`：逐故障一个 receipt、不可重试、协议/usage 终态 | 上述部署脚本补持久事实；尚未执行 |
| tool-call deltas | `provider.TestToolOnlyOutputCountsAsContent`、`provider.TestNativeToolsAndJSONEvidence` | — |
| 取消、关闭真实上游且不 replay | `provider.TestCancelClosesRealUpstreamBody` | `server.TestGatewayPostgresClientCancellationKeepsUnknownUsagePending` |
| missing usage | mock/provider 故障矩阵 | `server.TestGatewayPostgresMissingUsageNeverSendsSuccessDone` |
| 别名授权与原模型名 | 六协议部署脚本；尚未执行 | `server.TestModelAliasHTTPGatewayAuthorizationAndOriginalName`（仅 Chat，不冒充六协议） |
| 倍率编译 | 六协议实际价格/计费部署脚本；尚未执行 | `catalog.TestPhase1GroupMultiplierCompilesImmutablePrices`（编译域，不替代实际调用） |
| 034 paid credit 与 tenant 组价赋值 | provision 使用 `basis_kind=paid`、`fiat_currency=CNY`，且 `fiat_amount=amount`；赋值 DTO 与现行 HTTP 合同一致 | `server.TestHTTPFundingManualCreditPreservesWalletAndApprovalContract`、`server.TestPricingHTTPBusinessAndCurrentDomainIsolation` |
| 显式公开模型与匿名目录 | 六模型 publication CAS + 匿名模型/别名/默认价断言；部署复跑待执行 | `server.TestPublicCatalogHTTPPublicationCASAndAnonymousWhitelist` |

## 本分支已执行的验证

2026-11-15，在本分支工作树执行；这些结果不作为 029 同提交终验记录。

```sh
scripts/phase1-mock-acceptance.test.sh
```

结果：PASS。14 个 hermetic Python 单元测试通过，覆盖绑定存在性、receipt 基数/顺序、别名 JSON/SSE 回显、合并一次 ceil、实际价格断言、034 paid credit DTO、故障路由隔离、匿名公开目录、bind/copy 两种 mock 配置传输、登录 pacing 和 UI 文件权限/秘密字段拒绝；copy 测试还核对 tar 成员路径及容器可读权限。随后 15 个非 PG Go 绑定在 `-race` 下通过，包为 `internal/provider`、`internal/routing`、`internal/testsupport/mockupstream`。另以 `PHASE1_MOCK_CONFIG_TRANSPORT=copy` 仅启动临时 mock，实际 `docker create/cp/start` 与原健康检查通过，随后已清理容器和配置；未据此运行完整 Compose 或宣称部署验收完成。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  scripts/phase1-mock-acceptance.sh --run-postgres-go-tests
```

结果：PASS。10 个命名 PG 绑定在 `-race -p 1` 下串行通过；`internal/catalog` 与 `internal/server` 均 PASS。数据库地址不含凭据，未运行长整仓 PG 测试或并行 Compose。paid-credit、tenant-group 与 public-catalog HTTP 合同均验证通过；group/tenant 赋值字段未发现其他 DTO 偏差。

### 失败重试的诊断证据（不计为通过）

一次扩展实跑在 `conflicting_usage` 返回 HTTP 503。只读专库事实显示 request `cbc8e576-aa62-477d-ac88-d82cc3e54b81` 为 `failed/released`，attempt=0 且无 finalization；此前 `missing_usage`、`drop_after_events`、`error_after_usage` 已使共享候选 b 连续三次失败并进入 open。因此该 503 是 harness 用例间健康状态污染导致的零派发，不是 mock 未发内容事件，也不是已证实的生产流提交缺陷。修复采用每故障新 credential/新 route revision 隔离，并保留 HTTP 200、内容事件、一个 receipt/attempt 和 pending usage 的原有强断言。

## 未覆盖/阻塞缺口

- **扩展部署 harness 尚未完成修复后的复跑**：因此 P1-CAT-02 的六协议别名实际部署调用、P1-PR-02 的六协议编译组价实际计费，以及 P1-CAT-01 的六模型公开价格页仍是阻塞缺口。当前只有可执行断言和上述有限 PG 专项，不能标为这些部署复验 PASS。
- 权重、容量、熔断、成本门禁、连接拒绝和取消的本轮证据是命名 Go 测试，不是新的完整部署复跑；029 要求的同提交部署证据仍待执行。
- 未运行完整 Compose business-qa、隔离浏览器桌面/390px 或 UI 终验；`PHASE1_UI_ACCOUNTS_FILE` 只是可选准备工具。
- 没有真实供应商、真实凭据、真实 usage/采购账单或生产验收证据。
- 本记录不把任何未执行项写成 SKIP 后通过，也不宣称第一阶段或同一提交最终验收完成。

## 历史结果（不用于当前 PASS）

旧版 harness 在 2026-11-14 的 disposable isolated QA deployment 上曾通过 12 个六协议请求及 drop-headers/missing-usage 场景，run ID 为 `phase1-acceptance-a6dd2af575c7`。该历史结果只证明旧脚本范围，不能覆盖本分支新增的别名、编译价格、权重、容量、stream fault 或 UI 交接，也不能代替 029 冻结后的同提交复验。

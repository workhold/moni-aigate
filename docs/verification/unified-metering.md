# 028-U统一计量验证

## 当前空库迁移与拒迁门禁

当前服务入口使用 `migrations.ApplyCurrent` 和 `migrations.VerifyCurrent`。安装器只执行 `migrations/current_mvp.sql`，在任何业务 DDL 前持有迁移 advisory lock，并拒绝已存在 `schema_migrations`、业务关系或其他用户关系的数据库。它不升级旧库、不转换金融历史，也不接受不完整的基线。`serve` 与 `worker` 只执行只读 `VerifyCurrent`，缺失、未知或 checksum 不匹配时拒绝启动。

55439 专用 PostgreSQL 的 `TestCurrentInstallerRequiresEmptyDatabaseAndVerifiesCompleteBaseline` 覆盖空库安装、历史前缀拒迁、非业务用户关系拒迁和完整基线验证。001–024 文件仍保留给隔离历史 fixture，当前服务不会调用 `ApplyThrough`。

## 2026-11-10：028-U 真实 PostgreSQL guard 与拒迁矩阵补强

本片只新增 PostgreSQL 测试，没有修改 024/current MVP SQL、billing、wallet、settlement 或运行时。新测试在独立 worktree 的 `codex/028-u-postgres-matrix` 分支执行，数据库固定为 `127.0.0.1:55439/moni_test`。

新增 guard 负控和正控覆盖客户预占金额与结算金额的原向量重算、released 零值、schema/capability 完整性、input-only 非零输出、请求终结指针、观察 source identity、同一 operation 的并发唯一性、失败事务回滚、标量列/函数撤除，以及 `meter_%` 函数的 invoker 和固定 search_path 属性。既有矩阵继续覆盖跨租户、跨请求、不可变快照、成本来源指针和角色边界。

拒迁矩阵对 024 前置清单中的 request、attempt、event、结算、reconciliation、recovery、probe、成本更正、采购 reservation/event/evidence/proposal、journal、funding receipt、wallet lot、model/capability/route/binding/price/cost、pricing operation，以及 payment、周期预算、ledger 和 key/procurement 余额分组逐项写入 023 历史事实，共 41 个关系或分组用例。每个用例都实际执行 024，断言拒绝错误、原 schema/数据快照、`schema_migrations=23` 和无 024 关系。另有变更 023 约束后执行 024 的回滚测试。测试通过不代表旧库可以升级，它证明拒迁在 DDL 前回滚。

实际通过命令如下：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
go test -count=1 -timeout 300s ./migrations \
  -run 'TestUnifiedTextMeteringAmountAndSettlementGuards|TestUnifiedTextMeteringSchemaAndCapabilityCompletenessGuards|TestUnifiedTextMeteringFinalizationPointerAndEventIdentityGuards|TestUnifiedTextMeteringConcurrentCorrectionIdentityIsUnique|TestUnifiedTextMeteringCurrentShapeHasNoScalarProjection|TestUnifiedTextMeteringCurrentGuardFunctionsAreInvokerAndPinned'

PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
go test -count=1 -timeout 900s ./migrations \
  -run '^TestUnifiedTextMeteringRejectsEveryListedLegacyRelation$'
```

两组结果分别为 PASS 和约 53 秒 PASS。现有 `TestUnifiedTextMetering*`、canonical/缺口和角色测试也在同一专库通过。已实测发现当前 024 对改变 023 表 owner 的 ACL 形状不会主动拒迁，这项 SQL 门禁仍缺，未用测试绕过或伪装通过。仍缺完整九组 checker 的逐项正反事实矩阵、全部坏 ACL 形状负控、六文本全商业链、浏览器、正式供应商和生产环境验收。本片不把 028-U 或 Goal 标记完成。

## 2026-11-10：028-U 拒迁与权限矩阵补强

024 的拒迁检查现在将 `procurement_evidence_identities` 与采购 reservation、budget event、proposal 一起纳入历史事实拒绝。隔离 PostgreSQL 负控确认仅有 evidence identity 的旧库在执行 024 前回滚，schema、迁移记录和原事实均不改变。

新增 guard 专项覆盖 attempt supplier quote 与 parent plan 绑定、request/attempt snapshot 跨租户出生、cost correction 指针金额和互斥关系，以及 APP 对不可变计量快照的更新拒绝、PROJECTOR/PAYMENT_WORKER 对计量表读取和函数执行的拒绝。新增 LOGIN 连接测试使用实际独立角色验证 APP 的 SELECT/INSERT 边界、UPDATE/DELETE 拒绝和两个受限角色的读拒绝。该片仍是 guard/ACL 反例补强，不能替代完整九组 guard、完整六协议商业矩阵、浏览器或正式环境验收。

同一轮又补齐四个触发器完整性函数的 `SECURITY INVOKER`、固定 search_path 和 PUBLIC/运行角色执行拒绝。`guard_group_price_insert`、`guard_pricing_operation_insert`、`guard_request_retail_snapshot` 与 `guard_procurement_reservation_identity` 现在都保持 trigger-only 权限边界，并通过实际 LOGIN 角色的 55439 断言。

前端向量出口复核还发现经营报表解析器曾只接受六种文本协议和同步执行类型，已补齐图片、TTS、异步 video_generation 与 Realtime 单轮协议，并允许当前同步/异步执行类型。对应请求、报表、人工核对和 RatesWire 旧标量拒绝测试保持通过。

## 2026-11-10：当前 Compose 单机内测 smoke

`DEPLOY_QA_BUSINESS=true DEPLOY_QA_PORT=18090 scripts/deployment-qa.sh` 在更新后的主线通过。结果包括当前空库基线安装、二次迁移拒绝、重复 provision/bootstrap 拒绝、live/ready、文本调用、图片生成、TTS、异步 `video_generation` 创建/幂等提交/worker 结果、单 turn Realtime WebSocket，以及 app/worker/PostgreSQL 重启后的恢复检查。该 smoke 使用 deterministic mock provider，验证的是开发环境接口、权限、统一 wallet/settlement、任务制品和恢复接线，不是供应商 usage/cost 或生产部署认证。

## 当前 TTS 与统一管理输入边界

`POST /v1/audio/speech` 当前只发行 `mock-tts` 的 `wav` profile。HTTP 层严格校验单值 `Content-Type`、闭合 JSON、UTF-8、文本字符数、voice、WAV RIFF/fmt/data 结构和单值 `Idempotency-Key`。provider 必须返回 `confirmed_final` 且字符数与请求一致，服务才调用统一 `FinalizeMedia`。provider error、未知提交或 usage/音频结构不可信时分别记录终态，重复键不会再次调用 provider。管理端的每百万 token 整数输入已重命名为明确的 `ManagementToken*` 转换入口，只在边界一次转换为当前 canonical rational vector，运行时不再保留 `LegacyToken*` scalar API。

## 当前基线 Compose 复验

`DEPLOY_QA_BUSINESS=true DEPLOY_QA_PORT=18090 scripts/deployment-qa.sh` 已在 `current_mvp.sql` 基线上通过。它实际验证首次空库迁移、二次迁移拒绝、两次幂等 provision、bootstrap/重复 bootstrap 拒绝、健康检查、文本、图片、TTS、video task 创建/幂等提交/worker 结果、Realtime WebSocket，以及 app/worker/PostgreSQL 重启恢复。业务上游是 deterministic mock provider，只能证明开发环境工作流和当前账务/权限/恢复接线，不能替代真实供应商或生产部署。

## 2026-11-09：图片 POST 重放和 artifact 权威来源修复

图片模块在当前基线复核发现，结果 GET 已能从 `media_result_artifacts` 恢复，但进程缓存清空后重复 POST 会重新进入 `ReserveMedia`，完成 request 会被误判为不可重发。修复后，有数据库的图片 POST 先按原租户、用户、API Key、operation、幂等键和 request digest 读取已完成 artifact，命中时直接返回原 JSON，不创建新的 attempt 或 settlement。图片 GET 在配置数据库时也不再用已完成内存缓存绕过 artifact 的过期和缺失边界。

本片新增跨租户、跨用户、跨 Key 结果拒绝和 provider 未发送失败终态测试。验证命令如下。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
go test -count=1 -race -timeout 240s ./internal/media ./internal/server \
  -run 'Test(Image|Orchestrator|MediaResults)'
```

该结果仍是本地 55439 专库与 deterministic mock provider 证据，不代表真实图片供应商 usage/cost、对象存储、浏览器或生产部署验收。

## 2026-11-09：Realtime HTTP/媒体绑定与部署业务 smoke 边界

使用 `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'` 的 55439 专库通过：

- `go test -count=1 -race -timeout 180s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`
- `go test -count=1 -race -timeout 240s ./internal/server -run '^TestRealtimeHTTPPostgresOneSessionOneTurn$'`
- `go test -count=1 -race -timeout 240s ./internal/server -run '^TestPostgresRealtimePersistenceLifecycleAndTenantIsolation$'`
- `go test -count=1 -race -timeout 300s ./internal/server -run '^TestPostgresRealtimeTurnRejectsMixedCommercialFactSets$'`
- `go test -count=1 -race -timeout 300s ./internal/server -run '^TestCustomerRequestDetailExposesTaskReferenceFacts$'`

HTTP/WebSocket 专项实际验证 API Key 鉴权、一个数据库 session、一个 media-backed turn、输入/响应事件、重复 charge event 不重复 Dispatch、provider identity 落库和唯一 media settlement。024 的 realtime turn guard 现在严格接受文本三表或 realtime media 三表其中一套，禁止跨套拼接。`scripts/deployment-business-qa.sh --help` 和 `bash -n scripts/deployment-business-qa.sh` 通过。该脚本需要外部传入已创建的当前 API Key、模型和余额，不能在部署 smoke 中伪造客户钱包或商业引用；未声称已完成文本、图片、TTS、video 和 WebSocket 的部署业务调用。

补充的 `TestPostgresRealtimeTurnRejectsMixedCommercialFactSets` 在同一 request ID 下分别尝试 media reservation/text attempt 与 text reservation/media attempt，两个 INSERT 都回滚且不留下 realtime turn。它只证明 Realtime 当前双事实集合的来源类型闭合。

`TestCustomerRequestDetailExposesTaskReferenceFacts` 通过 owner-scoped HTTP 读取原 request 的 profile、reservation、attempt 身份，并从 request funding 读取 funding lot/source 身份。它只改善异步任务引用的可发现性，不改变 owner/member 隔离，也不替客户端补造 request、attempt 或资金事实。

`TestVideoTask*` 任务集成夹具现在不再直接查询数据库拼装 task 请求，改为经公开 request detail/funding HTTP 读取引用，再验证 `/v1/tasks` 创建、幂等、submit、terminal settlement、artifact 重读、worker/provider restart、failure/cancel 和 unknown 不重发。

新增 guard 矩阵通过：

- `go test -count=1 -race -timeout 300s ./migrations -run '^TestUnifiedTextMeteringGuardMatrix$|^TestUnifiedTextMeteringGuardRoleMatrix$'`
- `bash scripts/deployment-business-qa.test.sh`

前者在 55439 专库覆盖当前 request/attempt/event/finalization/settlement 的跨租户、跨引用、不可变、观察来源、成本指针和资金边界负控，并核对 app/projector/payment worker/PUBLIC 的计量权限。后者验证部署业务 smoke 的严格 SKIP/FAIL 退出码、任务/Realtime 参数合同和无依赖 WebSocket 自测。它们仍不替代完整旧金融事实拒迁、真实供应商、浏览器或目标部署验收。

新增 `scripts/deployment-qa-provider.py` 和 Compose `business-qa` profile，配套 `scripts/deployment-business-provision.sh` 通过管理 HTTP API 建立一次性模型、mock 渠道、路由、价格、租户、双人充值和客户 Key。后续在同一临时 Compose 项目中完成了五种业务调用实跑，见下一节。

## 2026-11-09：Compose 业务 profile 五条链实跑

使用本机 Docker daemon、PostgreSQL 16.15、本地构建镜像和一次性临时 Compose 项目执行：

```sh
DEPLOY_QA_BUSINESS=true DEPLOY_QA_PORT=18088 scripts/deployment-qa.sh
```

本次实跑完成空库迁移两次、角色 provisioning 两次、管理员 bootstrap 与重复 bootstrap 拒绝，随后由 HTTP provisioning 创建 mock 模型/渠道/路由/默认价、租户、双人充值和客户 Key。业务 smoke 在同一 App/Worker/PostgreSQL/`qa-provider` 项目中实际通过文本、图片、TTS、异步 video task 创建/幂等提交/worker 完成/结果下载和 Realtime WebSocket session/turn。之后 app/worker 重启和 PostgreSQL 重启恢复也通过。

这是开发环境的 deterministic mock provider 验收，证明当前 Compose 主链可以闭合。它不替代正式供应商、浏览器、1Panel、HA、性能或支付生产验收。期间修正了 APP 对不可变 `media_model_profiles` 的锁读误用、媒体约束函数执行权限和 task store 对外连接 LATERAL 的锁目标。

## 2026-11-08：当前 MVP 任务、结果、实时与报表续片

使用 `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'` 的 55439 专库通过：

- `go test -count=1 -race ./internal/server -run '^TestVideoTaskUsesCurrentMediaWalletAcrossSubmitAndTerminalObservation$|^TestVideoTaskUnknownSubmissionReleasesMediaAndPreventsResubmit$'`
- `go test -count=1 -race ./internal/server -run '^TestPostgresRealtimePersistence|^TestRealtimeWebSocket'`
- `go test -count=1 -race ./internal/server -run '^Test(ImageGenerationHTTPUsesCurrentMediaWalletFacts|SpeechHTTPUsesCurrentMediaWalletFacts|MediaResultsSurviveProcessCacheReset)$'`
- `go test -count=1 -race ./internal/reporting ./internal/server -run 'TestPostgresReadOperatingReportIncludesMediaOnceAndLeavesCostUnknown|TestOperatingReport'`
- `go test -count=1 -race ./internal/operations -run '^TestMeterRoleBoundary$'`
- `go test -count=1 -race ./internal/server -run '^TestVideoTask(ArtifactSurvivesStoreAndProviderRestart|ObservationSurvivesWorkerProviderRestart)$'`
- `go test -count=1 -race ./internal/server -run '^TestPostgresRealtimePersistenceLifecycleAndTenantIsolation$'`

本片修正任务 PostgreSQL 事件 raw canonical 摘要、nullable provider/artifact 读取和 mock 制品结果窗口，新增 video task 的 media reservation/attempt/settlement、同步媒体结果持久化、异步 task 制品正文持久化、Realtime PostgreSQL session/turn/event writer 和媒体报表只读接缝。已验证新 Store、新 grant 和拒绝下载 provider 可读取已保存正文，新的 worker/provider adapter 观察不重复提交或结算。provider session、对象存储、独立 worker 容器重启、真实供应商、浏览器、Compose 实跑和完整 028/S/A/R 出口仍未验收。

Docker 临时部署验收在当前 Docker daemon 上通过 `DEPLOY_QA_PORT=18080 scripts/deployment-qa.sh`。本次使用镜像代理提供的 PostgreSQL 16.15 镜像并本地标记为 `postgres:16-alpine`，脚本实际完成构建、空库迁移两次、provision 两次、bootstrap 和重复 bootstrap 拒绝、app/worker 启动、live/ready、app/worker/PostgreSQL 重启。该证据属于本机临时 Compose，不能替代 1Panel、目标主机、浏览器业务调用或真实供应商验收。

## 2026-11-08：媒体当前 wallet 入口与 HTTP/WS 局部验收

024 新增媒体 profile、request/attempt/observation、reservation/settlement 和 funding-lot edge。`internal/billing/media.go` 复用当前 ledger account 锁、`media_amount` 单次 ceil、FIFO lot、journal 和扩展后的 `wallet_event_valid`，覆盖预占、confirmed usage 收费、unknown/failed 释放、幂等和未知 attempt 禁止重发。

使用 `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'` 的实际隔离 schema 通过：

- `go test -count=1 -race -timeout 240s ./internal/billing -run 'TestPostgresMedia'`
- `go test -count=1 -race -timeout 240s ./internal/server -run '^Test(SpeechHTTP|ImageGenerationHTTP)'`
- `go test -count=1 -race ./internal/server ./internal/provider ./internal/realtime -run 'TestRealtime|TestMockRealtime|TestService'`

图片和 TTS server fixture 各核对一次 media reservation、attempt、observation、settlement、charged amount 与 wallet 余额。Realtime 有数据库路径在单 turn 收尾时写 media settlement，纯 server WS fixture 因无 DB 继续返回 `response.pending`。异步 task 尚未接统一 Reserve/Settle，媒体结果正文仍是内存 mock，未完成数据库 session/turn writer、worker 重启、Compose 容器运行、真实供应商和完整 028/S/A/R 验收。

2026-11-08 成本来源边界续片：024 的 `meter_attempt_cost_matches` 在 UPDATE 时拒绝把已有 correction pointer 清成空并改写为 late finalization，INSERT 不引用 `OLD`；已存在的 finalization→correction 更正路径保持可用。迁移 canonical/缺口专项和 billing 证据/更正回归在 55439 专库通过。该 guard 仍属于局部存储证据，不能替代 028-U 九组完整 guard 或正式供应商成本认证。

## 2026-11-08：route cap 完整性与 funding-lot 批次续片

024 的 `meter_route_complete` 现在按每个 binding 验证 supplier cap 集合：零条表示未配置；一旦配置必须恰有 `text_input_token` 与 `text_output_token` 两条 token cap，不能由不同 binding 拼接，也不能在已提交 route 上追加。新增迁移专项验证了零 cap、完整两 cap、单 cap 回滚 active pointer/binding 以及既有空 route和错误 channel/version 负控。

008 账务写批次同时把 Reserve/finish 的 funding reservation/settlement lot 写集改为纯 `projectFundingLotChange` 预计算后一次 `pgx.Batch` 发送，保持每个 allocation 的关系→event→CAS balance 顺序；退款的逐行路径共享该纯投影规则。超预占时 profile block 与 audit 也由同一 batch 写入，失败回滚与重试唯一性已由 billing/periodicbudget race 覆盖。55439 专库通过 `TestMeterCompletenessRejectsGaps`、billing 生命周期/Reserve 回滚/finish 尾部故障重试/财务边界/并发预占专项。008 的全量取消、周期预算全矩阵/SIGKILL、固定 binary、最终预览和正式性能门禁仍未完成；028-U 完整九组 guard、六文本总矩阵、浏览器、正式供应商和部署验收仍未完成。

同一轮新增六协议受限 APP 证据：过期 unknown 恢复不重生成、重复恢复不改变原事实，以及幂等重放只产生一次 POST、attempt、settlement 和收费。测试只使用本地 HTTP/provider fixture，不能替代正式供应商、独立 LOGIN 或浏览器验收。

## 2026-11-08：人工核对改为完整 retail 向量

本片按 `u-storage-contract.md` 11.2.1 收口人工核对入口：收费提案从原请求不可变 schema 读取并严格验证完整 `retail_quantities` QuantitiesWire，proposal 保存 canonical/digest；批准仍生成 `approved_reconciliation` 观察和原有结算，supplier 侧保持 `missing`，不写供应商 canonical/digest 或成本金额。HTTP 请求拒绝 `input_tokens/output_tokens`、未知字段和重复键；控制台改为编辑向量 JSON，并保留每百万 token 管理输入之外的完整向量合同。

涉及文件：[reconciliation_vector.go](../../internal/billing/reconciliation_vector.go)、[recovery.go](../../internal/billing/recovery.go)、[evidence.go](../../internal/billing/evidence.go)、[billing_handlers.go](../../internal/server/billing_handlers.go)、[PlatformView.vue](../../web/src/views/PlatformView.vue)、[024迁移](../../migrations/024_unified_text_metering.sql)。

已通过：

- `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -count=1 -race ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`
- `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -count=1 -race ./internal/billing -run 'TestPostgresEvidenceAndReconciliation|TestPostgresVectorActualKeepsOriginalCanonical'`
- `go test -count=1 ./internal/server -run '^TestBindReconciliationResolve$'`
- `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -count=1 -race ./internal/server -run '^TestParallelCommercialGapOverBoundCapsOnceAcrossProtocols$'`

上述证据仍是当前文本和本地专库专项；028-U存储/六文本完整商业链、浏览器、正式供应商、独立生产LOGIN、部署/HA/性能和S/A/R后续阶段仍未验收。

## 2026-11-07：整体盘点与角色/纯域负控续片

本轮以 `codex/initial-mvp` 为唯一开发基线，完成 001–028 的交付盘点，明确把正式迁移、账务/采购、网关共享装配和最终架构检查留在主线；纯计量域、前端和独立商业反例可在互不重叠的工作区并行。盘点结果见[整体交付盘点](overall-delivery-inventory.md)。当前判断仍是“架构链路合理、生产验收未闭合”，没有把历史 `legacy`/`compat`/`prefix` 代码按名称删除，也没有把候选迁移当成已发行版本。

补充了支付 worker 的独立计量负权限负控：测试创建独立 worker 角色，逐表确认对 024 计量表没有 SELECT/INSERT/UPDATE/DELETE，逐函数确认没有任何 `meter_%` EXECUTE；APP/PROJECTOR/PUBLIC 原有边界继续复核。`internal/operations` 的 `TestMeterRoleBoundary` 在 `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'` 下通过，2.666s。

纯域并行片没有发现生产实现缺口，仅补充 `metering` 的超 schema bound、真实量超界、精确有理费率排序、`QuoteDigest` 区分率值、跨 schema fail-closed，以及 `strictjson` 转义/孤立 surrogate/不完整字符串负控。`go test -count=1 -race ./internal/metering ./internal/routecost ./internal/strictjson`、对应 `go vet` 和 `git diff --check` 通过。它们是纯专项证据，不能替代 024 PostgreSQL guard、六协议商业链或正式环境验收。

补充的独立受限 APP 商业反例覆盖六协议的三组缺口：实际量超界时只产生一次 attempt、按当前政策结算并只写一次 profile block/audit；客户人工核对可幂等重放但不能改写原证据，确认客户侧时供应商仍为 missing/unknown；supplier unknown 在 `cost_first/deny` 下不派发、不造 attempt，并释放客户预占。`go test -race -count=1 -timeout 300s ./internal/server -run '^TestParallelCommercialGap'` 在 55439 专库通过，14.394s。供应商晚到、独立 LOGIN、浏览器、正式供应商和部署验收仍未覆盖。

前端审查未发现新的在线 scalar 读端：pricing、public catalog、procurement、requests 已读完整 `rates`/`bounds`/`quantities`，管理端每百万 token 输入和人工核对字段按合同保留。`npm test` 336 项通过，`npm run build`（vue-tsc + Vite）通过；仍没有浏览器实际操作证据。

本轮补齐候选 024 的拒迁前置负控：除已有旧表、非零账本余额、模型/路由/价格历史拒绝外，若 `api_keys` 或 `procurement_accounts` 有 held/spent 事实，存在周期预算 allocation/event、funding lot child、payment transaction identity/capture/effect，迁移都会在建表前失败。测试以 `ApplyThrough(..., 023_wallet_sources.sql)` 建立每类历史事实，调用完整 `migrations.Apply` 后确认错误、快照未变、`schema_migrations` 仍为 23 且没有计量表；`TestUnifiedTextMeteringRejectsZeroBalanceBusinessHistory` 在 55439 专库通过（race，5.379s）。这只是拒迁覆盖的新增矩阵，仍不能代替 11.2 全部旧金融事实和生产迁移验收。

## 2026-11-07：默认/组价六协议与退款来源续片

在受限 `SET ROLE APP` 的本地 HTTP 链上新增默认价和组价的六协议组合：Chat、Responses、Messages、Embeddings、Rerank、Gemini。每项都验证客户结算、采购成本、唯一 settlement、原组价 request snapshot，并在禁用 `meter_cost_million` 与 `meter_price_million` 后通过。另新增一次退款审批与重复审批，核对 settlement/refund lot 来源边和 `CheckWalletSources` 全量一致性。

命令使用 `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'`：`go test -count=1 -race -timeout 240s ./internal/server -run '^TestCostVectorApplication(RefundKeepsOriginalSourceGraph|DefaultAndGroupPricesAcrossProtocols)$'` 通过，10.730s。该证据仍是受限 APP 与本地供应商 fixture，不能替代生产 LOGIN 资格、正式供应商、浏览器、超界、恢复无重生成或部署验收；六文本完整商业复选框继续保持未勾。

## 2026-11-07：恢复不重生成续片

新增 `TestCostVectorApplicationRecoveryDoesNotRegenerate`：本地供应商返回无用量响应后，请求进入 `pending_reconciliation`，受限 APP 的恢复读取持久 reservation/recovery job，在过期后释放冻结；断言只存在一个 attempt、一个 POST、一个 released settlement，重复 recovery 不增加任何事实。测试使用同一 55439 专库并通过：`go test -count=1 -race -timeout 240s ./internal/server -run '^TestCostVectorApplicationRecoveryDoesNotRegenerate$'`，4.383s。该夹具使用隔离测试事务压缩 deadline，不能替代正式 worker、供应商和部署恢复验收。

## 2026-11-07：route complete guard 续片

按存储合同 8.1.1，024 增加 `meter_route_complete` deferred checker，并挂到 route、binding、cap 出生：route 必须至少有一个 binding；binding 的 channel/version 必须一致且在出生时有有效 credential；可选 cap 必须落在该 route 的 supplier schema。空 route 负控在 COMMIT 被拒绝，当前 route+binding+active pointer 的原子发布和历史 snapshot fixture 已调整为同事务。

验证命令均使用 `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'`：`migrations` canonical/缺口 race 4.835s（含空 route 与 channel/version 错绑负控）、`internal/catalog` 全包 race 27.539s、`internal/billing` route/lifecycle/evidence/settlement/vector 专项 race 38.698s、`internal/server` 全包 race 343.717s，均通过。该片仍不替代完整九组 guard、六文本退款/027来源/恢复总矩阵、正式供应商、浏览器或部署验收。

## 2026-11-07：阶段1 6c–6e 当前域收口

本片继续在 `codex/initial-mvp` 汇合，不使用已停止投入的 `028-u-text-metering` 分支；未提交、未推送。024仍是候选单文件迁移，001–023没有修改。先按存储合同补齐失败边界，再落地当前域实现：

- 024 新增 capability/variant/bound 的完整性提交检查、route/binding 同事务出生及 supplier cap 出生检查；`request_records.route_revision` 强制为正数，Reserve、RecordAttempt、证据和成本策略不再对零路由跳过校验。
- 024 新增客户 Reservation R 与 settlement 金额的 deferred guard，均从原请求价格、原 reservation、原 finalization 事件数量和 `meter_token_amount` 重算；不读取当前价格、不接受调用方金额，也不引入第二套金额算法。
- billing 移除零 route_revision 的生产旁路；当前测试夹具改为先发布包含实际 channel/version/upstream 的不可变 route。周期预算 HTTP 夹具同样用同事务 route birth，并明确传入 `RouteRevision=1`。
- 唯一模型写入口把默认 `openai_chat` 写回不可变 profile JSON；因此 profile 协议列与 profile 原文保持同一合同，完整性 guard 不再接受隐式不一致。

指定 PostgreSQL 16.15 专库为 `127.0.0.1:55439/moni_test`，本片通过：

- `go test -count=1 -race ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`：通过，1.996s。
- `go test -count=1 -race ./internal/catalog`：通过，23.507s。
- `go test -count=1 -race ./internal/billing`：通过，243.736s。
- `go test -count=1 -race ./internal/server`：通过，334.603s。
- `go test -count=1 -race ./internal/operations`：通过，40.288s。
- 受限 APP 的六协议报价/资金链、unknown policy、组价 HTTP 回放、标量报价函数禁用、周期预算 HTTP 生命周期与分页专项通过；测试使用本地 HTTP fixture，不能替代正式供应商或浏览器验收。

6e 语义复核后已同步任务清单、统一架构、HLD/LLD 与本验证记录；架构快照刷新只在本片完成后执行。028-U 存储项和六文本完整商业链复选框仍保持未勾：九组完整 guard、完整退款/027来源/恢复矩阵、浏览器、正式供应商、部署/HA/性能及 S/A/R 后续阶段尚未验收。

## 2026-11-05：55439专库整仓基线修复至全绿（阶段1起点）

本节只记录整仓回归基线，不勾选tasks中任何U生产项。按[开发运行手册](../runbooks/development.md)新建独立PG16测试集群（PostgreSQL 16.15，`127.0.0.1:55439/moni_test`，LOGIN `moni_test`测试专用超级用户，loopback trust，UTF8/C，`TimeZone=Asia/Shanghai`，`max_connections=200`，`fsync/synchronous_commit/full_page_writes=on`）；此前各节使用的5432隔离schema入口不再用于本节。命令统一为`TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -race -p 1 ./... -count=1 -timeout 600s -json`。

首轮基线：71包中50通过、16失败、5无测试；顶层测试1948通过、255失败、136跳过。失败包为alerts(1)、billing(13)、identity(44)、mockpsp(6)、operations(7)、paymentreceiver(1)、payments(12)、paymentsandboxqa(2)、periodicbudget(48)、pricing(6)、procurementattemptbirth(63)、procurementrequestbirth(28)、reporting(3)、server(17)、testsupport/funding019(3)、scripts/payment-sandbox-qa(1)。根因分三类：生产缺陷、夹具仍按024前标量列/裸写资金行、冻结历史研究夹具无法经027钱包入资。

生产缺陷（均已修复，改动在源码而非放宽断言）：

- 平台probe候选未读取完整报价，`probe.go`恒503；改为与候选同一`routecost.ParseStoredQuote`读取原件。
- 024组价审计出生/提交触发器对所有audit查`pricing_operations`，使无该表权限的支付worker写普通audit被拒；改为仅检查`action='pricing.price.publish'`。
- serve启动未核验计量角色边界；`main.go`在监听前调用`operations.VerifyMeterRoles`，失败以“metering database role verification failed”退出。
- 计量角色供给缺“整组缺席才可跳过”的历史前缀规则，且未撤销投影角色残留EXECUTE；新增`provisionMeterRoles`的absent-set规则，`grantMeterFunctions`对PUBLIC与projection统一REVOKE，运行时核验要求整组存在。

夹具修复要点：identity/reporting/alerts/billing/operations/payments/server/paymentsandboxqa改用`meter_insert_*`或真实`Reserve`生成请求、快照与来源，损坏注入改为`session_replication_role='replica'`下提交；paymentsandboxqa安装/核验改用`migrations.Apply/Verify`与`VerifyFunding`；periodicbudget夹具固定PB时钟并把成本生效回退7天，需要真实时钟的预置改用`realClock`。pricing删除两项已被向量发布取代的旧迁移/发布测试，新增`TestPricingMigrationRefusesLegacyRequestHistory`确认旧标量请求历史被拒迁。

新增跳过83项，均写明原因，不计入任何商业验收：

- procurementattemptbirth 48、procurementrequestbirth 20、testsupport/funding019 3：ADR-005冻结的历史研究夹具，无法经当前027钱包入资；只保留编译。
- periodicbudget 10：需要手写非零客户/供应商资金来源，当前钱包与计量guard拒绝；跳过原因声明由billing路径周期预算测试覆盖，本节未逐条复核等价性。
- periodicbudget 2：需要在固定未来周期时钟下产生真实客户流量；跳过原因声明由`internal/server` `TestPeriodicBudgetHTTP*`经真实billing覆盖，同样未逐条复核等价性。

最终结果（2026-11-05 21:23，机器原始输出在本地`.local/green/`，未提交）：

- 整仓race：exit 0；71包66通过、0失败、5无测试；顶层2119通过、0失败、219跳过（136项原有环境门控跳过+83项上列新增）。最慢：server 331s、billing 225s、identity 223s、fundinglotcatalog 148s，均在600s单包超时内。
- `go vet ./...`、`go build -trimpath ./cmd/moni-aigate`、`go run ./scripts/check-procurement-layout`通过。
- `npm --prefix web test` 333/333通过；`npm --prefix web run build`（Vue strict/Vite）通过。
- `go run ./scripts/check-architecture`只读检查失败：快照相对62个文件过期。按AGENTS并行阶段规则，本节不refresh；阶段1汇合时完成语义复核后再refresh，不调整覆盖范围。

限制：工作树同时含阶段1的6a前端半片（默认价/采购价发布已切到operation合同），后端仍是旧的无operation发布。当前控制台采购价发布会被旧处理器以400拒绝；默认价会被旧处理器按旧路径写入，但前端无法解析回执而进入“待核查”。该不一致在后端半片落地前存在，不得部署。本节没有浏览器、受限APP六文本完整商业链、正式供应商或部署验收；未commit/push。

## 2026-11-05：完整候选报价接入当前准入、采购和派发

本节是当前状态；下一节“未在线装配”保留先行纯片的历史结果，不再描述当前接线。按[存储4.2.2](../../specs/028-unified-metering-media/u-storage-contract.md)先冻结再替换：catalog沿原route profile读完整schema/Rates canonical及digest，候选/路由/billing统一使用不可变routecost.Quote与精确Limits，删除旧routing.CostQuote/CostScore/内部两价cap。NewPlan先拒错channel/upstream、同UUID异schema、错cap绑定和超原硬界；CostExclusion返回(reason,error)，坏原件不吞成cost_unknown。cost_first仍先取最高priority，在该层调用CompareExactCost后保留原权重；采购初筛和原U只由ReservationAmount末尾一次ceil。RecordAttempt原batch后、采购account/PB等待后以及PrepareDispatch都复验完整原件；派发复用原最后锁后的DB决策at，不另采clock，不重算客户价/U或后补未就绪canonical。没有第二套inference报价身份、标量fallback或双运行。

普通Store移除prospective配置/运行分支；[ProspectiveStore](../../internal/billing/prospective.go)独立且不嵌入普通Store，仅接受FrozenAttemptInput与procurementexecution原13参字节联合。类型负控禁止交叉输入/提升Reserve；原序号/解释校验、错误cause/未知提交语义保持。冻结执行器SQL、映射、观察器实现、原列/迁移范围/pins不升级。测试专属旧报价差分保留原Equal/旧SQL，不依赖新Quote。历史fixture中原调用current Store作旧业务oracle的组合，经[test-only拒绝边界](../../internal/testsupport/frozenattempt/attempt.go)显式失败，**尚未恢复历史business oracle，只核编译**；边界单元测试只证明不调用DB、不伪造成功/业务错误，绝不是历史业务通过。专库将来可用时也不能直接宣称这些组合可运行。当前periodicbudget billing夹具另用完整canonical选择器，55439缺库仍只编译。

新增[受限APP报价链](../../internal/server/cost_vector_application_integration_test.go)：ProvisionFor后SET ROLE并核NOSUPERUSER，session_user仍owner，不转称独立LOGIN/启动资格。禁meter_cost_million与meter_price_million后，六协议各paid/known-zero共12项真实本地HTTP→候选→attempt→采购→派发→双侧证据/唯一结算通过；逐项核原Rates/parent plan/成本指针、原U、账户held/spent、客户金额。后续调价+重复Settle保持一次收入/原成本且网络posts仍1。另3项核manual/allow未知成本保持NULL金额/指针/率，manual/deny与受管采购未知则不创建attempt/采购、不发网络且释放客户冻结。

现有排队改价及凭据锁期间scheduled生效两项也升级为受限APP+禁两标量函数：前者不造attempt/不重规划；后者已有attempt记录not_sent可信零成本，网络posts=0，原资金收尾。纯测试另核不能用百万整数镜像表达的1/3、2/7排序和1/3、3/10 cap；两者ceil相同仍按精确费用选择。不同层级证据不合并冒充完整受限APP负控矩阵。

实际结果（Go命令均PATH含/opt/homebrew/bin，GOCACHE=/tmp/moni-aigate-gocache；PG16.15使用指定5432入口、随机隔离schema和migrations.Apply；运行测试count=1）：

- `go test -race -count=1 -cover ./internal/routecost ./internal/routing ./internal/metering ./internal/inference ./internal/strictjson`：1.509s/1.883s/2.273s/3.071s/2.527s，全部PASS；routecost语句覆盖100%，不是商业覆盖100%。
- `go test -race -count=1 ./internal/billing ./internal/testsupport/frozenattempt -run '^TestProspective|^TestCurrentBoundary'`：5.399s/5.774s PASS，只是纯ABI/类型/拒绝边界。
- `go test -count=1 -timeout 180s ./internal/server -run '^TestCostVectorApplication'`：14.195s PASS；升级两项等待测试后，`go test -race -count=1 -timeout 180s ./internal/server -run '^TestCostVectorApplication|TestCostRoutingQueuedQuoteChange|TestCostQuoteActivationDuringCredentialWait'`：18.197s PASS。
- `go test -count=1 -timeout 120s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`：1.940s PASS；`go test -count=1 -timeout 120s ./internal/operations -run 'TestMeterRoleBoundary'`：1.386s PASS。
- `go test -count=1 -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestPostgresScheduledCostActivation|TestPostgresProcurementScheduled|TestPostgresProcurementAccountWait|TestPostgresCostQuoteDomain|TestPostgresVectorProcurementOriginalU'`：29.165s PASS，覆盖原锁后时刻/原价域/采购U和就绪门禁。
- `go test -count=1 -timeout 180s ./internal/catalog ./internal/server -run '^TestCost|TestProcurementHTTPMeter|TestProcurementMeterReadRejectsIncompleteOriginal|TestNativeProtocolsCommercialRoundTrip'`：11.567s/36.321s PASS。
- `go test -run '^$' ./...`整仓编译、`go vet ./...`通过；periodicbudget仅`go test -c -o /tmp/moni-028-u-quote-periodicbudget.test`，未对5432执行其专库运行测试。
- `npm --prefix web test`330/330 PASS、零SKIP；`npm --prefix web run build`（Vue strict/Vite）通过，无本轮浏览器验收。
- `go build -trimpath -o /tmp/moni-028-u-quote-aigate ./cmd/moni-aigate`与`make check`通过；后者包含只读架构、采购布局、整仓vet和strict/Vite构建。最终补齐本记录后再次语义复核并refresh/只读check，不调整架构覆盖。

过程失败保留：在线接线初期有消费者尚未改完及局部测试变量编译错误；routing直接acquire测试的零policy未归一化曾失败，修正测试原默认policy后通过。未通过放宽DDL/业务断言或变更历史pins来消除失败。001–023未变，本续片无新增DDL/权限/资金表，024此前候选改动保留；026仍读唯一settlement和全部attempt成本，不从向量重算金额。合同/任务/消费者清单/HLD/LLD/架构入口和依赖细图已同步，最后按AGENTS语义复核再refresh/只读check。

未完成：发布operation/精确向量指纹、其余公开/私有HTTP与TS数量/报价合同、完整guard以及受限APP六文本完整默认/组价/超界/退款/027来源/恢复无重生成矩阵。历史业务oracle/55439实际专库、浏览器、正式供应商/部署/HA/性能未验。不能由本地15项报价链勾选U存储/生产切换/商业出口，更不能说028完成；未commit/push或改PR。

## 2026-11-05：完整候选报价值与精确cap纯层（未在线装配）

先冻结[存储合同4.2.2](../../specs/028-unified-metering-media/u-storage-contract.md)，新增[routecost](../../internal/routecost/quote.go)不可变选择值。Quote完整绑定Schema/default/channel/upstream/原version/basis与supplier Rates；known零价、缺版本unknown、显式版本unknown及无效零值分别处理，同UUID但不同schema定义不相容。StoredInput单独接原规范字节/摘要，坏原件拒绝而非降级，unknown率字段必须真NULL，空BYTEA也拒绝。Limits核supplier维度/单位/C序、规范int64与约分、分母≤1_000_000，复制输入/输出；只用big.Int交叉乘积比较单价，不计算金额、不ceil或反算每百万。金额/总成本比较仍委托metering，没有新增inference报价身份或摘要算法。

本片收窄为纯域交付：初查发现普通当前调用方和冻结022研究夹具共用旧CostQuote，直接全量替换会牵动历史差分验收依据，未采用该半切换。当前routing、候选、billing与显式NewProspective仍保持本片开始时行为；新包目前只有纯测试消费，未接入serve/账务，没有shadow/双运行或降维fallback。未改001–023、未在本片修改024，也未变更数据库权限/资金表、快照就绪门禁、026金额读链或恢复政策。冻结研究没有被升级到024或重新签pins。下一片的完整输入隔离、候选→采购等待→PrepareDispatch原子接入与受限APP禁标量验收仍未完成，不能勾选U存储/生产切换/商业出口。

实际验证（Go命令均设置PATH包含/opt/homebrew/bin及GOCACHE=/tmp/moni-aigate-gocache）：

- `go test -count=1 -race -cover ./internal/routecost ./internal/routing ./internal/metering ./internal/inference ./internal/strictjson`全部通过；之后补cap同长度异内容/异schema相等性断言，再单独重跑routecost为1.517s，语句覆盖率100.0%。[9个Test及1个Fuzz入口](../../internal/routecost/quote_test.go)覆盖union/domain/side、规范原件与摘要负控、精确有理边界及大数交叉乘积、副本并发、同ceil异精确费用、单次ceil/预占溢出；覆盖率不代表所有商业或安全行为已验收。
- `go test -count=1 -fuzz FuzzStoredQuoteAndLimits -fuzztime=5s -parallel=2 ./internal/routecost`：311946次，6.495s PASS；这是短程fuzz，不是仅默认seed。
- PostgreSQL入口仍为`postgres://vchive@127.0.0.1:5432/postgres?sslmode=disable`，隔离schema与原migrations.Apply。三组指定专项通过：migrations canonical/缺口1.869s；billing lifecycle/evidence/settlement9.535s；operations meter role1.174s。
- 当前billing原链回归：`go test -count=1 -timeout 180s ./internal/billing -run 'TestPostgresManagedAttemptRequiresExactSelectedQuote|TestPostgresCostPublicationWaitersSeeNewQuote|TestPostgresScheduledCostActivationAfterAllDispatchLockWaits|TestPostgresProcurementScheduledQuoteRecheckedAfterAccountLock|TestPostgresVectorProcurement'`，44.004s PASS。它证明未破坏既有报价等待/采购U，不证明routecost已被在线消费。
- 当前catalog回归：`go test -count=1 -timeout 120s ./internal/catalog -run 'TestCostQuoteSchemaDomainIsolation|TestCostQuoteRejectsUnpublishedSchemaAndInputOnlyOutput|TestCostCandidateExactModelFutureActivationAndPoolQuote'`，4.838s PASS。
- 当前server回归：`go test -count=1 -timeout 180s ./internal/server -run 'TestProcurementHTTPMeter|TestProcurementMeterReadRejectsIncompleteOriginal|TestCostRouting'`，16.326s PASS。
- 整仓`go test -run '^$' ./...`编译、`go vet ./...`、前端`npm test`330/330（无SKIP）通过。periodicbudget仅`go test -c`，没有对5432执行其55439专库测试。
- `go build -trimpath -o /tmp/moni-aigate-028-routecost ./cmd/moni-aigate`及`make check`通过，包含只读架构、采购布局、整仓vet和Vue strict/Vite构建；生成物不提交，构建不代表浏览器验收。

语义复核关注新增纯依赖及未装配边界、原件/错误与复制不变量；原请求/资金时序、租户/角色、收入唯一及全部attempt成本、旧研究范围不变。任务、消费者清单、HLD/LLD和架构入口/纯依赖图同步；最后显式refresh并运行只读架构检查，不调整覆盖范围。没有浏览器、受限APP六文本完整商业链、正式供应商、部署/HA或性能验收；没有commit/push或改PR。后续片继续按tasks推进，不能写成028已完成。

## 2026-11-05：采购预占列表切换原供应商界向量

先冻结[存储合同7.2](../../specs/028-unified-metering-media/u-storage-contract.md)，再修改未发行024及[当前列表SQL](../../internal/procurement/store.go)。原`p.input_bound/output_bound`已被024删除，旧列表503在本轮改动前单独复现，确认不是PG连接故障。列表和[TS合同](../../web/src/procurement.ts)/控制台同时撤旧字段，新增原profile/schema_digest/default及纯核supplier Bounds wire；读取原attempt完整界行，不读当前模型/价、不由U反算量或重算金额。024新增STABLE/SECURITY INVOKER只读投影，固定部署schema搜索路径且PUBLIC撤执行；核tenant/request/attempt、schema mask、完整维度/单位/数量与原snapshot一致，缺原件报错而非隐藏采购行。计划未就绪只投影原界，不回填canonical或生成第二报价身份。

新增[受限APP读取专项](../../internal/server/procurement_meter_integration_test.go)：真实ProvisionFor后SET ROLE并核非superuser（不声称session_user已是独立LOGIN或启动资格验收）。使用两个真实账户、两个租户共享账户、52个原attempt，核50+1分页无丢失/重复/串账户；不同output/ready-unready逐项回查原界，所有Bounds交纯核ParseBounds验证。禁用两标量反算函数、发布新采购价并停用当前模型后原界和原U仍保持；读取前后10张原件表逐值相等，无快照后补/资金写入，也无模型网络请求。Embeddings/Rerank单列受限APP未就绪/0输出。缺snapshot、缺/错tenant/超schema/仍在schema内但不等原界与跨request/attempt负控使用新未提交原件、结束回滚，不禁用历史不可变或完整性guard；它们仅核读端，不把未提交反例说成通过COMMIT。额外维度/错单位由当前表CHECK提前排除，前端仍独立拒绝。

原管理HTTP三套回归（管理回执/平台与财务分权/受管采购双审及客户隔离）通过；双审后的列表明确保留U=120、held=0、spent=17，不以裁定金额覆盖原界。故障注入使原界依赖抛出私有错误，HTTP安全503且不返回data/半页；非法cursor先400，tenant membership先403。TS新增严格向量负控，保留9007199254740993、不转Number、拒旧字段/缺维/错序/单位/side/version/schema/variant/非规范及溢出数量。控制台按维度和unit分列显示原界与金额，重新加载采购预占时清空旧行，失败不留旧预占供误操作；仅有源码/合同和构建证据，未做浏览器验收。

本轮实际运行环境：PostgreSQL 16.15，5432入口和随机隔离schema，PATH含/opt/homebrew/bin，GOCACHE=/tmp/moni-aigate-gocache。Go运行测试均count=1：

- `go test -timeout 120s ./internal/server -run '^TestProcurement(HTTP|MeterRead)'`：9.276s；随后`-race`补原界不等负控和逐表不变断言，最终23.175s通过，选中专项无SKIP。
- `go test -timeout 120s ./migrations ./internal/operations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps|TestMeterRoleBoundary'`：最终2.717s/1.746s通过。角色专项新增只读投影的invoker/STABLE/固定search_path/APP可执行/PUBLIC及projector不可执行断言。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestPostgresVectorProcurement'`：35.765s通过；未改资金入口、001–023、冻结历史reader/pins或026金额算法。
- `npm --prefix web test`：330/330通过；Vue strict/Vite构建通过，无本轮浏览器验收。
- `go test -run '^$' ./...`只核整仓编译；`go test -c ./internal/periodicbudget -o /tmp/moni-028-u-periodicbudget.test`只核该包编译，不以5432冒充55439专库运行预算测试。
- `go vet ./...`及`go build -trimpath -o /tmp/moni-028-u-aigate ./cmd/moni-aigate`通过；生成物只在/tmp，不提交。

过程失败如实保留：第一次server试跑发生辅助测试文件尚未加入的编译错误；完整试跑发现列表误假设采购表已存在设计中的tenant列（当前024未安装该列），以及测试夹具漏created_by、同request/channel重复attempt；修正为原attempt PK→tenant完整绑定及合法独立channel反例后通过，没有放宽DDL/权限。扩展执行`TestPaymentRolesEmptyOffActualLoginsAndRepeatedProvision`和`TestPaymentRolesFunctionDefinitionAndExecutionBoundary`被各自55439/moni_test专库门禁立即Fatal，本机该库不存在，未越过门禁或计为通过；新函数无worker grant且PUBLIC撤权，实际支付LOGIN专项仍须在原专库补验。

本片语义复核覆盖SQL→原attempt/supplier界、平台财务与租户隔离、整页失败/分页、只读角色闭包、TS/UI大整数及无资金写入；模块图、原请求资金时序、结算/报表和支付worker职责不新增。架构入口/细图、HLD/LLD及任务与消费者清单同步。采购准入/路由CostQuote、发布operation/精确向量指纹、其余HTTP/UI、完整guard及受限APP六文本商业出口仍未收口；存储任务继续未勾，不称028完成，不涉及S/A/R、真实供应商、部署、HA或性能，未commit/push或修改PR。

## 2026-11-05：采购U原向量与独立防伪

先冻结存储合同7.1，再改当前采购预占。锁后读取原attempt实际channel/upstream、同schema/digest/variant的known成本及该attempt的supplier界，以完整原Rates和Bounds调用`metering.ReservationAmount`；不从每百万反算值计算U，也不以新价替换原U。计划未就绪仍读取原版本和界行、不补快照；坏原件拒绝，溢出回滚新attempt及采购事实。抽出共享规范Rates验证并删除无消费者的`vector_amount.go`；原预期CostQuote和派发检查仍含标量，不能称全链禁标量。

024新增SECURITY INVOKER延迟guard，固定隔离schema搜索路径且撤PUBLIC执行；按原界与原率独立调用`meter_token_amount`验证原U，不仅核账户事件自洽。受限APP专项覆盖ready/unready、付费/零价/单次ceil、两个input-only协议、原价不受后续报价影响、未知/缺快照、金额伪造、缺界/缺率/错率单位/额外率及溢出回滚；原界错单位和额外维度由现有表CHECK更早拒绝。规范数值单元测试另覆盖最大值与错误原件。原cost child完整性由DB独立验证，Go读端验证规范率根与原界，不将这两者混称。

PostgreSQL 16.15、5432测试入口和随机隔离schema，Go测试均`-count=1`：

- `go test -timeout 120s ./internal/billing -run '^TestPostgresVectorProcurement|^TestCanonicalSupplierReservationAmount$' -v`：21.148s；之后补固定搜索路径及安全测试，单独专项通过。
- 同专项`-race -timeout 180s`：34.121s，包括固定搜索路径测试。
- `go test -timeout 120s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`：1.809s；`go test -timeout 120s ./internal/operations -run 'TestMeterRoleBoundary'`：1.206s。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestPostgresProcurementScheduledQuoteRecheckedAfterAccountLock|TestPostgresProcurementAccountWaitRechecksDeadlineAndPause|TestPostgresProcurementSharedBudget|TestPostgresVectorActual'`：36.466s。
- `go test -timeout 180s ./internal/server -run 'TestNativeProtocolsCommercialRoundTrip|TestCostRouting'`：9.124s，仅覆盖选中的本地协议及路由测试。
- `go test -run '^$' ./...`只核整仓编译；`go test -c`只编译periodicbudget，未将5432冒充其55439专库运行实际测试。

扩展运行`TestProcurementHTTPManagedCostWorkflowAndCustomerIsolation`失败并单独复现：采购预占列表返回503。[现有SQL](../../internal/procurement/store.go)读取024已删除的`p.input_bound`/`p.output_bound`，不是本片新增guard的列变更；应在后续HTTP读取切换中沿原attempt快照修复。本片最初测试还曾因不存在的测试方法编译失败，修正后通过；另两项畸形界行被原表CHECK提前拒绝，调整测试验证精确CHECK而非降低约束。上述失败均不视为商业链通过。

本地fixture不代表完整六文本、供应商、部署、HA或性能验收。采购路由/CostQuote、发布指纹、HTTP/UI及完整guard仍未收口；存储和商业链任务保持未勾。

## 2026-11-05：第5片续，客户选价与预占消费完整费率

先冻结存储合同4.1.1，再替换[pricing.Selection](../../internal/pricing/pricing.go)及[billing.Reserve](../../internal/billing/reserve.go)。选价在同一SQL快照内读取新DB priced_at、模型当前唯一capability/default的schema及完整原Rates，完整限定model/group/schema_id/schema_digest/default后按原三项逆序选价。Go解析Schema/Rates后逐byte核canonical与摘要；坏合同返回pricing_invariant，合法域缺当期价才是price_unavailable，不能回落到另一schema或默认组。Selection不再携InputRate/OutputRate；Reserve核原锁定capability，直接ReservationAmount(bounds,selected.Rates)，显式input-only输出率0，溢出amount_overflow。Reservation删除两价回显，不另加legacy DTO或第二报价身份；管理端每百万token输入、原锁序/冻价/等待后期限、计划未就绪门禁、001–023与本片前024均未改。

新增[受限APP预占专项](../../internal/billing/vector_reserve_integration_test.go)：真实ProvisionFor后SET ROLE，并核current_user非superuser。先发布组价，再在隔离schema禁用meter_price_million与meter_cost_million，8个正向场景覆盖默认/组价×付费/完整零价/单ceil及两种input-only；每百万1/1对100/20界预占1而非逐维ceil的2。资金JSON不再有两rate，Key/wallet与原快照一致，Release恢复全部held。另4个默认/组缺价或未来价负控、1个巨大R溢出场景，共13个场景；对幂等、请求/快照/界、预占、Key、PB、来源、账户/账本、audit/outbox/recovery原行逐一比对无变化，并在溢出后使用同request/幂等键成功预占2。PB表在这批fixture中没有active cap；不把零增量对照写成周期预算争用或完整来源checker验收。

新增[当前选价负控](../../internal/pricing/vector_integration_test.go)：受限APP在未提交事务插入坏原件，不修改不可变行、不禁guard；9类非规范字节、错side/currency/version/dimension/unit、负率、错schema绑定及digest错误均拒绝，随后强制原deferred completeness也拒绝。已提交的异schema报价既不挤掉同模型原价，也不补本域缺价/未来价；不存在capability与缺价严格区分。原3个current helper测试改用独立空库当前fixture，保留原018历史fixture，不让历史库接受024。冻结request/attempt birth仅因回执删字段把必要费率断言移到原request旧列；其自由版本、ApplyThrough范围、宽ABI、pins及原SQL未改，本轮只编译不运行这些历史组合。

环境为PostgreSQL 16.15，5432入口、随机隔离schema，PATH含/opt/homebrew/bin，GOCACHE=/tmp/moni-aigate-gocache。下列Go运行测试均count=1，实际通过：

- `go test -timeout 120s ./internal/billing -run '^TestPostgresVectorReserve' -v`：11.855s，新专项无SKIP。首次试跑因测试表名误写journal_entries失败，改为真实journal_lines后重跑通过，未改生产guard。
- `go test -timeout 120s ./internal/pricing -run 'TestVectorSelection|TestSelectEffectiveTx|TestGroupLockWait|TestInvalidDomain' -v`：5.415s，全部选中项无SKIP；不是整个pricing历史升级套件运行验收。
- `go test -timeout 120s ./migrations ./internal/operations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps|TestMeterRoleBoundary'`：4.935s/1.428s。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestPostgresVectorActual|TestPostgresRetail|TestPostgresTenantPricing(ExactDomain|LostCommit|CommitRejection)'`：64.088s，含原价跨model/wallet等待与幂等/COMMIT故障回归。
- `go test -timeout 180s ./internal/billing ./internal/server -run 'TestPostgresRetrieval|TestPostgresGemini|TestPostgresIndependentContext|TestNative|TestGemini|TestRetrieval'`：57.189s/77.494s，保持原各fixture限定本地协议/计费范围。
- `go test -race -timeout 180s ./internal/billing ./internal/pricing -run 'TestPostgresVectorReserve|TestPostgresRetail|TestVectorSelection|TestSelectEffectiveTx|TestGroupLockWait|TestInvalidDomain'`：20.291s/6.840s。
- `go test -timeout 180s ./internal/catalog ./internal/server -run 'TestPublicCatalog|TestPricingHTTP|TestCostRouting|TestCostVersionHTTP|TestCostQuoteActivation'`：6.984s/17.289s。
- `go test -timeout 120s ./internal/billing -run 'TestPostgresDeadlineAfterAccountLock|TestPostgresRecoveryAndFreeModel|TestPostgresAppendReplayAfterDeliveryProgress'`：3.374s；纯metering/inference/provider/routing、整仓go vet和嵌入Go构建通过。

全仓`go test -run '^$' ./...`只核编译，非全仓运行测试；periodicbudget与maintenance-scan-qa另显式go test -c到/tmp通过，未对5432执行55439专库套件。前端Vue strict/Vite构建通过，无本轮浏览器验收；前端329项运行测试仍沿前片证据，不冒充本轮重跑。新受限APP预占专项不替代既有55439 barrier夹具或六文本采购/退款/来源/恢复整链。

语义复核已覆盖新增pricing→metering依赖、原版本/当前capability关系、同语句时钟、组价不回落、原钱包/PB/来源锁序、回滚/期限和角色边界；统一架构的模块边与预占时序同步，无检查覆盖范围变更。本片未新增表、SQL函数或生产权限，未运行真实供应商、部署、HA或性能验收。采购准入/CostQuote、完整发布operation/向量指纹、其余HTTP/UI与九组guard仍待收口，028-U存储及商业任务保持未勾，未commit/push或修改PR内容。

## 2026-11-05：第5片续，实际结算直接读取原向量

先冻结存储合同6.2.1，再替换当前billing实际金额读端。新增[vector_actual](../../internal/billing/vector_actual.go)：客户只沿原request price/schema/variant/价格组读取完整Rates；供应商只沿原attempt cost/channel/upstream/schema/variant读取原费率；实际量直接解析同tenant/request/attempt finalization的对应侧Quantities canonical，共用metering.Amount。schema/rates canonical和摘要由Go复核；final数量外层digest仍由DB inference.final.v1 guard核验，不与纯核Quantities digest混用。financialRequest两价已移除，实际金额不再经meter_*_million反算；准入/预占仍待完整向量DTO切换，不能称全部scalar已撤除。

计划未就绪时按原版本绑定读取数值原件，不补写计划/快照或读当前价。缺失、非规范、错side/schema/身份或摘要错返回invalid_meter_snapshot并回滚本次事务；known零价与unknown保持不同。recordAttemptCost和采购实际消费按supplier侧final state判断，不拿客户confirmed量证明供应商成本；明确无消费0成本、冲突/更正、raw大整数、客户R上限与采购超U、截止/唯一结算和来源/PB收尾保持。未修改本片前的024或001–023，未增表、权限、第二报价身份或金额公式；冻结研究/原型ABI与pins未修改，026报表金额链不改。

新增[纯读端专项](../../internal/billing/vector_actual_test.go)：两侧各6个独立金额期望（一次ceil、非整总价、零价、微小费率、actual超界、大于int64的真实费），共12例；另32个缺canonical/摘要错/错schema/side/currency/不完整/未约分/非规范字节拒绝例。新增[PG专项](../../internal/billing/vector_actual_integration_test.go)：ready/unready × 7种已知/零价/basis_unknown/version_missing/超界/not_sent/rejected_no_consumption，加2种人工核对，共16个PG子例。先完成准入/派发和后续新价发布，再在每个隔离schema把两种每百万反算函数替换为必报错，随后记录证据与实际结算仍通过；验证原价不被新价/补价替换、唯一收费、双方金额/成本指针、采购预算事件及账本守恒。人工核对客户收费15，供应商仍missing且canonical/digest/成本金额/指针为空，采购held仍240。该fixture仍是原owner，不代替受限APP六文本完整出口。

本片使用PostgreSQL 16.15、5432测试入口和随机隔离schema；`PATH=/opt/homebrew/bin:$PATH`、`GOCACHE=/tmp/moni-aigate-gocache`，下列均已实际通过（Go运行测试均count=1）：

- `go test -timeout 180s ./internal/billing -run 'TestCanonicalActualAmount|TestPostgresVectorActual' -v`：新专项无SKIP。
- `go test -timeout 120s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`；`go test -timeout 120s ./internal/operations -run 'TestMeterRoleBoundary'`。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestPostgresCostQuoteDomainIsolation|TestPostgres(ManagedAttempt|CostPublication|ScheduledCost|CostChange|Procurement|DispatchReadBatch|AttemptReadBatch|Retrieval|Gemini|IndependentContext|DeadlineAfterAccountLock|RecoveryAndFreeModel|AppendReplayAfterDeliveryProgress)'`。
- `go test -timeout 180s ./internal/server -run 'TestNative|TestGemini|TestRetrieval|TestCostRouting|TestCostVersionHTTP|TestCostQuoteActivation|TestPublicCatalogHTTP'`。
- `go test -race -timeout 180s ./internal/billing -run 'TestCanonicalActualAmount|TestPostgresVectorActual|TestPostgresCostChangeAfterSending|TestPostgresDeadlineAfterAccountLock'`。
- `go test ./internal/metering ./internal/inference ./internal/provider ./internal/routing`；整仓`go vet ./...`、`npm --prefix web run build`（Vue strict/Vite）和`go build -trimpath -o /tmp/moni-aigate-028-u ./cmd/moni-aigate`。

另执行全仓`go test -run '^$' ./...`仅编译；periodicbudget和maintenance-scan-qa显式`go test -c`通过，没有对5432运行要求55439的实际测试。原采购布局静态工具通过仍仅表示design材料，不是current资格。前端测试结果沿上一片329项记录，本片只重跑构建，无新增HTTP/UI/浏览器验收。语义复核确认模块调用、原版本/终态数据边、锁序、异常/截止收尾和角色边界保持，无覆盖范围调整；架构refresh/check随本片文档与源码一起交付。存储、完整向量准入DTO/operation指纹、其余guard及受限APP六文本商业链仍未全部完成，028-U对应任务保持未勾；没有commit/push或PR内容变更。

## 2026-11-05：第5片续，采购报价域隔离

先冻结存储合同4.2.1，再修改未发行024。采购发布显式提交schema_id/digest/default，SQL入口不再从全局最后创建的capability猜schema；成本列表、成功响应和管理UI选择器同步带同一身份。当前每百万token整数输入保留，不增加在线双运行或第二套报价身份。route落原profile和出生stamp，model非零active route及request正数route须匹配profile；本轮移除了 SQL 中 `route_revision<>0` 的旧旁路，路由一致性 guard 现在无条件执行。冻结研究材料中的自由版本/旧旁路保持原样，不升级到024。

候选沿原route profile，attempt与采购锁后沿原request snapshot，派发复验沿原attempt snapshot，完整限定channel/upstream/schema/digest/variant后选价。快照guard对known、unknown及计划未就绪都核原attempt域；input-only非零输出价与未发布schema在API和raw SQL均拒绝。未新增金额公式、资金表、角色或结算器。历史022采购规则夹具改为直接比较原scalar SQL，不调用当前reader，不扩大其原schema或改签pins。

新增[目录域专项](../../internal/catalog/cost_domain_integration_test.go)验证多schema下缺价不回落、同刻异schema可独立发布、known零价/显式unknown隔离、另一个较新capability不改变原价、input-only输出零价、未发布schema及route/profile负控；使用目录原受限APP fixture。新增[计费域专项](../../internal/billing/cost_domain_integration_test.go)含计划就绪/未就绪各6个跨schema/channel/upstream负控，共12个；随后验证采购预占240、派发准入、原价成本30、唯一收费15、重复Settle与账本守恒。计费fixture仍是原owner角色，不能代替受限APP六文本完整商业链。

PostgreSQL 16.15，`TEST_DATABASE_URL=postgres://vchive@127.0.0.1:5432/postgres?sslmode=disable`、隔离schema、`PATH=/opt/homebrew/bin:$PATH`、`GOCACHE=/tmp/moni-aigate-gocache`。以下专项已通过，均count=1：

- `go test -timeout 120s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy'`。
- `go test -timeout 120s ./internal/operations -run 'TestMeterRoleBoundary'`。
- `go test -timeout 180s ./internal/catalog -run 'TestCost|TestCredentialPool|TestPublicCatalog'`。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresCostQuoteDomainIsolation|TestPostgres(ManagedAttempt|CostPublication|ScheduledCost|CostChange|Procurement|DispatchReadBatch|AttemptReadBatch)'`；另用`-v -run '^TestPostgresCostQuoteDomainIsolation$'`确认12个负控无skip。
- `go test -timeout 180s ./internal/server -run 'TestCostRouting|TestCostVersionHTTP|TestCostQuoteActivation|TestPublicCatalogHTTP'`，含严格字段白名单、身份回显、模型选择器及原CAS验证。
- `go test -timeout 180s ./internal/billing -run 'TestPostgresRetrieval|TestPostgresGemini|TestPostgresIndependentContext'`及`./internal/server -run 'TestNative|TestGemini|TestRetrieval'`，均count=1通过；仍只算各原fixture的本地协议/计费回归。

`go test -count=1 ./internal/metering ./internal/inference ./internal/provider ./internal/routing`通过。最终`make check`通过只读架构校验、原采购列布局静态检查、整仓`go vet`与前端strict/Vite构建。架构覆盖范围不变；已语义复核当前数据关系、模型/路由/profile绑定、报价域、资金/恢复和角色边界，并显式refresh后通过只读check。采购布局工具的通过仍只表示其原设计静态合同，不授予冻结研究或028生产资格。

本机现已有Node 26.13.0；按锁文件执行`npm ci --ignore-scripts --no-audit --no-fund`，package/lock未改。`web`下`node --experimental-strip-types --test --test-reporter=spec tests/*.test.mjs`：329 PASS、0 FAIL/SKIP，含新增成本域合同和报表合同；`npm run build`的Vue strict检查与Vite构建通过。无本轮浏览器交互验收。曾做全仓`go test -run '^$' ./...`仅核编译/零测试选择，不是运行验收；周期预算和maintenance-scan-qa另按要求`go test -c`，未对5432执行它们的实际数据库测试。

本轮首次新增计费域测试因误用`delivery_stage='complete'`失败，改为现行`content`后通过；扩大回归发现当前目录夹具直接设置不存在的active route，以及当前成本更改夹具raw unknown缺schema，分别改为真实route发布与显式schema的meter入口后重跑通过，没有放宽guard。55439专库仍缺，周期预算/钱包来源及冻结组合均未因此验收；完整向量DTO、pricing_operations/精确向量指纹、其余HTTP/UI和九组完整guard仍待完成。028-U存储/商业任务保持未勾，未提交或推送。下文为前几片历史记录，以本节新的环境和验收范围为准。

## 2026-11-05：第5片当前夹具与计划门禁

修正先前纯核快照的就绪偏差：请求/尝试计划未就绪时，bounds/rates canonical、纯核digest、外层报价与计划同为NULL，原四条界行仍完整。新增12种HMAC就绪/非法HMAC与known/basis_unknown/version_missing组合，及请求单独夹带费率、尝试缺摘要/无计划夹带界的数据库负控；生命周期含原预占释放和账本核验。SQL bounds/rates/QuoteDigest与Go纯核逐字节对照；模型发布另拒绝自由版本号、全零UUID及大写UUID。

路由发布/公共目录发布改读不可变capability。当前成本、来源零价、Gemini/检索与派发夹具改用meter函数和快照；公共目录反例在profile出生时创建，保留不可变约束。Responses/Messages/Gemini/检索财务量只读quantity行，cache/reasoning仍在provider/原生wire验收。旧 `meter_event_usage` JSON函数已无调用方，从未发行024中移除。历史013和坏价夹具直接核原SQL合同及024拒迁，不给当前reader增加旧schema分支。

本轮使用PostgreSQL 16.15、指定5432测试URL、隔离schema；命令前缀为 `PATH="/opt/homebrew/bin:$PATH" GOCACHE=/tmp/moni-aigate-gocache`。已通过：

- 末轮迁移专项：`go test -count=1 -timeout 120s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`，2.544s。
- 末轮计费专项（含新增门禁矩阵）：`go test -count=1 -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy'`，9.358s。
- 末轮权限专项：`go test -count=1 -timeout 120s ./internal/operations -run 'TestMeterRoleBoundary'`，1.158s。
- `./internal/catalog -run 'TestPublicCatalog|TestCost'`，17.432s；`./internal/server -run 'TestPublicCatalogHTTP|TestCostRouting|TestCostVersionHTTP|TestCostQuoteActivation'`，21.515s，均带 `go test -count=1 -timeout 180s`。
- `./internal/billing -run 'TestPostgres(ManagedAttempt|CostPublication|ScheduledCost|CostChange|Procurement|DispatchReadBatch|AttemptReadBatch|IndependentContext|Gemini)'`，45.853s；`./internal/billing -run '^TestPostgresRetrieval'`，28.159s，均带 `go test -count=1 -timeout 180s`。
- `./internal/server -run 'TestNative|TestGemini'`，34.421s；`./internal/server -run '^TestRetrieval'`，23.905s，均带 `go test -count=1 -timeout 180s`。
- `go test -count=1 ./internal/metering ./internal/inference ./internal/provider`；`go vet ./internal/billing ./internal/catalog ./internal/server ./migrations`；server、periodicbudget、maintenance-scan-qa 的 `go test -c`；`git diff --check`。

扩大计费regex曾包含 `TestPostgresWalletSourcesRuntime`，其专库保护检查在5432明确Fatal；该次整体FAIL保留为环境限制，来源专项没有通过。随后单独重跑上列可运行的计费专项并通过。周期预算、钱包来源与历史检索夹具要求55439/moni_test，本轮仅编译；没有将5432当作其专库。maintenance-scan-qa是固定源码pins/C0原件的历史组合，保持冻结、不改签pins，编译不表示通过历史或当前运行验收。

这些HTTP与主计费fixture使用各自原测试角色；目录及角色边界专项不能代替受限APP六文本完整商业链。本轮未完成pricing/cost routing内部DTO、向量发布指纹和HTTP/TS全面替换，也未完成完整九组guard。没有Node/浏览器、正式供应商、部署或性能验收。028-U存储任务继续未勾，未提交或推送。下列纯域及前几片记录为历史证据，以本节说明的当前边界为准。

## 2026-11-05：纯核报价原件存储片

本片先补充 [U 存储合同](../../specs/028-unified-metering-media/u-storage-contract.md) 的失败边界，再修改未发行的 `024_unified_text_metering.sql`：请求快照保存 retail/supplier bounds canonical 与 digest、retail rates canonical 与 digest、纯核 quote digest；尝试快照保存两侧 bounds、known supplier rates 与 digest、纯核 supplier quote digest。canonical 与 digest 由原价格/成本快照和绑定 schema 重建，计划未就绪时纯核bounds/rates/摘要与计划、外层报价同空，原四条界行及版本绑定仍完整；supplier unknown且计划就绪时仅supplier rates/纯核quote为空。`inference.quote.v1` 外层摘要和 `metering.QuoteDigest` 纯核摘要保持两个身份。

静态编译通过：`go test -c ./migrations`、`go test -c ./internal/billing`，纯包 `go test ./internal/metering ./internal/inference` 通过，`git diff --check` 通过。按指定命令执行 PostgreSQL 专项时，当前沙箱禁止连接 `127.0.0.1:5432`，因此空库迁移、生命周期和角色边界本片均未重新验收；不能勾选 028-U 存储任务，也不能把这片写成已完成。

同日第 2 片为 attempt snapshot 增加独立 `parent_plan_digest`。写入函数从原 request snapshot 读取父摘要，只有 attempt plan canonical/digest 成功生成时才写入；constraint trigger 同时检查 request plan 是否存在、父摘要是否相等以及三字段是否成对为空。billing 生命周期测试增加了 request plan、attempt plan 与 parent digest 的关系断言。该片同样只完成编译级检查，未因本地无法连接 PostgreSQL 而虚报数据库通过。

同日第 3 片为 `upstream_attempts` 增加 `latest_cost_finalization_id` 与 `latest_cost_correction_id`。024 新增互斥约束、known/unknown 金额投影约束，以及指向同 tenant/attempt finalization/correction 原件的复合外键；`meter_attempt_cost_matches` 改为按当前指针核对金额和 basis。终态记录、采购审批、人工更正和冲突回退路径均已同步清空或设置对应指针。migrations/billing 编译和纯域测试通过；PostgreSQL 生命周期、迁移和角色专项仍受当前环境连接限制，未标记通过。

随后用指定 `TEST_DATABASE_URL` 并在受限提升下重跑本片专项，以下均通过：

- `go test -count=1 -timeout 120s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestMeterCompletenessRejectsGaps'`
- `go test -count=1 -timeout 180s ./internal/billing -run 'TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy'`
- `go test -count=1 -timeout 120s ./internal/operations -run 'TestMeterRoleBoundary'`

上段的连接限制记录保留为首次尝试，已被本次专库结果 supersede；028-U整体及第4片观察改造仍未完成。

另外补跑受管采购的 unknown release/checker、冲突超界和争议 barrier/reopen 专项，均通过，确认 correction pointer 与 disputed unknown 回退没有破坏采购金额状态机。

第 4 片已完成代码接线：事件写入不再把旧 `Usage` JSON 直接放进 `observation_canonical`，而是生成固定 inference Observation 字段顺序、两维 quantity 状态、协议/交付/finish 元数据和 `moni-aigate.inference.observation.v1` digest；数据库 deferred guard 从事件列和 quantity 行重建原件。source 约束收紧为 adapter/retrieval/gateway/approved_reconciliation，`attempt_id` 强制存在。迁移 canonical/缺口、billing lifecycle/evidence/settlement/procurement 和 operations meter role boundary 专项已重新通过；028-U后续 HTTP/UI 与完整商业链路仍未完成。

第 5 片开始处理在线标量接缝：周期预算 HTTP fixture 的模型和零售价种子已改用 024 的 meter 函数，`internal/server` 与 `scripts/maintenance-scan-qa` 可编译。完整 pricing/cost routing HTTP/UI、旧 fixture 清理和受限 APP 六文本商业验收尚未完成。

2026-11-04。先冻结[共同合同](../../specs/028-unified-metering-media/u-domain-contract.md)，再实现`inference`，与原`metering`复用抽出的唯一`strictjson`语法层。当前模块/依赖/信任边界见[架构](../architecture/unified-metering.md)，准确原件、命令及源码摘要见[机器记录](unified-metering.json)。这不是完整U生产验收。

## 实际实现与审查

新增六文本不可变Profile、公共request/收紧attempt计划、原known/unknown报价、分维度观察及分侧FinalQuantities。input-only明确0输出价/界/量；采购缺版本和显式unknown版本分别保留，unknown读金额/费率返回错误。人工客户核对即使提供共享最终量也不能确认supplier。对象及输出slice/bytes为copy，零值无效；金额仍调用metering，未写第二套价格计算器或协议parser。

metering新增schema/费率只读描述，防止consumer反解析canonical或获取可写内部slice。通用JSON语法从原包移动到strictjson，metering仍映射原sentinel并保持原领域错误顺序/canonical/摘要；独立审查发现原depth计数漏拒九层全空容器，现明确容器depth>=8拒绝，八层空容器及深度8标量仍接受。此项收紧到原合同要求，不能声称语法接受集逐字未变。

交叉审查同时修正外壳字段/值与身份比较的错误顺序、手工supplier冲突优先级，以及持久唯一槽沿原tenant/source/event而非缩小到request范围。存储草案明确FinalQuantities外层摘要与纯核Quantities摘要的区别；这些是合同对齐，不表示SQL已经安装。

## 限定本地结果

根汇合后执行`go test -json -race ./internal/metering ./internal/strictjson ./internal/inference -count=1`，最终原件`.local/028-common-final.jsonl`，188658字节，SHA-256 `0e0b65ce955f9bc76e9a0b1a549b82da64d099d1bd26ae3c749ffcb1b149eeee`。

| 包 | PASS顶层 / 子项 | 包报告时间 |
| --- | --- | --- |
| inference | 13 / 106 | 2.611秒 |
| metering | 14 / 49 | 1.725秒 |
| strictjson | 3 / 18 | 2.027秒 |

三包均0 FAIL/SKIP。顶层包含默认执行的fuzz seed入口（inference1、metering2），不是全部顶层均为普通Test，也不是持续fuzz执行次数。作者及审查者先前专项与本轮重叠，不相加。测试涵盖7个Python独立framing/SHA黄金值、六协议与stream、候选完整绑定/最大界/收紧、same UUID异schema、known零价/unknown/int64溢出、非零retrieval输出价拒绝、不同mask/共享缺量/异常归属、超界actual保留、人工supplier隔离、观察时间不变事实摘要、严格JSON组合错误、大小及并发copy。

公开API纵向专项独立断言：公共input120、所选attempt input100、output20；retail率1/2和1/3得R=67，supplier率1/4和1/5得U=29；实际量3/2得retail3、supplier2微元。交换两侧率拒绝。supplier原价unknown时，adapter量仍可确认，retail按已知原价得3；人工客户量不能代替采购证据。这些只是纯对象与数学组合，不声称发生过钱包预占或正式扣费。

根另执行共享语法短程fuzz（2 workers、请求4秒）：`FuzzStrictParsers`实际174750次、5.640秒PASS。共同外壳`FuzzInferenceEnvelopes`同样2 workers/请求4秒，实际188967次、5.525秒PASS。没有将默认seed测试当作持续fuzz。共同三包`go vet`、最终`make check`（只读架构、采购布局、整仓vet、前端strict/Vite生产构建）及嵌入Go构建通过；原件摘要由机器记录列出。最终汇合后按AGENTS完成架构语义复核和显式refresh/只读check；没有执行完整当前全仓PG或make verify-mvp。

## 未完成范围

2026-11-04 追加 `024_unified_text_metering.sql`。PostgreSQL 16.15 空库可安装；`TestUnifiedTextMeteringCanonical` 确认 schema/零售费率 canonical 能被 `metering` 解析，且摘要一致。每百万 token 费率 2000000/3000000 存成约分后的 2/1 与 3/1，对输入 100、输出 20 的预占为 260 微元，与旧一次 ceil 公式相同。同一测试核对输入 10、输出 4 的零售终态 canonical 与 `metering` 一致，摘要使用 `moni-aigate.inference.final.v1`，不接受纯核用量摘要。该测试不覆盖六协议商业链。同一公式对输入 100、输出 20、费率 2/1 与 3/1 的 SQL 结果也是 260。预占、证据和结算专项另确认终态投影写入，错投影在提交时回滚，人工核对的供应商侧保持空；把已记下的供应商金额改掉会被拒绝，授权更正仍可替换该金额。受管采购专项确认单次预占与该尝试的预算事件、已批准提案一致；把另一份已批准提案接到同一预占会在提交时回滚。过期的证据条数或修订号也不能被批准确认。证据专项确认人工核对落在原尝试上，没有尝试的观察会在提交时回滚。没有尝试的恢复不造观察，仍按原政策释放。同一 canonical 测试确认 UUID 档案、64 位十六进制 HMAC 的请求计划，以及由它派生的尝试计划，字节和摘要都与 inference 一致。自由版本号仍无计划。同一缺口测试确认没有用量界行的请求不能提交。同一 canonical 测试确认零售 known 报价和供应商 version_missing 报价的字节、摘要都与 inference 一致。计划未就绪时报价列为空。纯核报价摘要已落库，计划未就绪时也为空。完整九组 guard 和 HTTP/UI 切换仍未完成，按[任务清单](../../specs/028-unified-metering-media/tasks.md)继续。当前只有每模型唯一profile发行，多候选纯测试不证明生产异构路由。媒体/任务/Realtime、真实供应商/支付/1Panel及性能均未因此验收。

026经营报表和027资金来源的既有本地证据保留各自范围，本片不声称重新执行其PG验收。GitHub上一提交的Actions因账户支付/额度未启动，见[027记录](wallet-sources.md)；本地通过不等于远端CI通过。

## 2026-11-10：计量函数定义与实际 LOGIN 权限收口

024 与 `migrations/current_mvp.sql` 在全部 `meter_%` 函数创建完成后逐函数执行三项固定化。函数为 `SECURITY INVOKER`，搜索路径固定为当前 schema、`pg_catalog`、`pg_temp`，PUBLIC 执行权撤销。当前安装和重复 provision 的角色核验同时要求 APP 对每个计量函数有执行权，projector 与 PUBLIC 没有执行权，函数不得是 SECURITY DEFINER，且搜索路径必须固定。

本片使用 PostgreSQL 16.15 的专用库 `moni_test`，没有使用5432：

- `PATH="/opt/homebrew/bin:$PATH" TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' GOCACHE=/tmp/moni-aigate-gocache go test -race -count=1 -timeout 240s ./internal/operations -run 'TestMeterRoleBoundaryWithActualLoginConnections|TestMeterRoleBoundary'` 通过。
- `PATH="/opt/homebrew/bin:$PATH" TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' GOCACHE=/tmp/moni-aigate-gocache go test -race -count=1 -timeout 240s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestUnifiedTextMeteringRejectsZeroBalanceBusinessHistory|TestUnifiedTextMeteringGuard'` 通过。

这片只收紧函数执行边界和定义核验。完整九组 guard、11.2 旧金融事实的全部逐表正反例、正式目标环境权限验收和 028-U 任务总项仍未完成。

## 2026-11-10：媒体 supplier cost snapshot

当前 MVP 媒体 profile 为图片、TTS、video_generation 和 realtime_turn 增加不可变 supplier 每百万 unit 费率。media attempt 出生时冻结 supplier 费率，唯一 settlement 保存 supplier cost status 与 amount。confirmed final 且结果有效时按原 supplier 费率执行一次 `media_amount`，失败、取消、missing、unknown 和超界结果保持 unknown/NULL。媒体 settlement guard 核对 observation、attempt、request 和 tenant 绑定，经营报表读取 settlement supplier cost，不从 raw_amount 或当前 profile 重算。

55439 专库专项通过：

- `go test -race -count=1 -timeout 300s ./internal/billing -run 'TestPostgresMedia|Test.*Media'`
- `go test -race -count=1 -timeout 180s ./internal/reporting -run 'TestPostgresReadOperatingReportIncludesMediaSupplierCostOnce|TestPostgresReadOperatingReport'`
- `go test -race -count=1 -timeout 360s ./internal/server -run 'TestImageGenerationHTTPUsesCurrentMediaWalletFacts|TestSpeechHTTPUsesCurrentMediaWalletFacts|TestMediaResultsSurviveProcessCacheReset|TestVideoTask|TestRealtimeHTTP'`
- `go test -race -count=1 -timeout 300s ./migrations -run 'TestUnifiedTextMeteringCanonical|TestUnifiedTextMeteringGuardMatrix|TestUnifiedTextMeteringGuardRoleMatrix|TestUnifiedTextMeteringGuardRoleWriteRejections'`

费率和成本来自 deterministic mock fixture，不构成真实供应商 usage 或真实供应商账单认证。

同日整仓 `make verify-mvp` 使用相同的 55439 专库启动。前端342项测试和构建通过，Go race 已通过大部分包；`internal/periodicbudget` 有一次迁移 statement timeout，`internal/server` 有一次密码变更用例返回401，整仓命令未记为无条件全绿。命令退出前 55439 未留下活动连接或等待锁，分别重跑 `TestBillingPeriodicCustomerEachScopeRejectsFullRAtomically` 与 `TestPasswordChangeRevokesFamiliesAndRequiresFreshProof` 均通过，判断为本轮专库并发/测试抖动证据，不把失败记录抹除。

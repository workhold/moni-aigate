# 033 文本动态路由验证记录

2026-11-20；当前记录是实现期间的专项证据，不是阶段完成声明。分支已合并 032 的 `312fa5f`（含 `034e862`），033 迁移顺延为 current 015；组合验收按该合并结果重新执行。

## 已执行的专项

均在隔离工作树、确定性本地 HTTP mock 和专用 `127.0.0.1:55439/moni_test` 随机 schema 中执行；没有真实供应商、生产凭据或支付。

- `go test ./internal/routing -count=1`：通过。覆盖硬过滤优先级、only/order/ignore、max_price、精确毛利、price/latency/throughput、抽签回放、sticky 建立/继承/health 打破、auto 降档和 unknown 不重试。
- `TestCurrentInstallerRequiresEmptyDatabaseAndVerifiesCompleteBaseline`：通过 current 012 空库安装和 baseline 追加安装。
- `TestCostRoutePolicyRevisionCapsAndRollback` 与 `TestPostgresDynamicPhysicalBindingsImmutableAndRoleSeparated`：通过。覆盖 CAS、同渠道不同物理模型、不可变能力、active→retired、APP 只读决策与权限收紧。
- `TestGatewayPostgresNonStreamChargesFinalUsage`、`TestGatewayPostgresSafeFailoverHasOneChargeAndTwoPosts`、`TestRouteDecisionSnapshotsAreSafeImmutableAndPlatformOnly`：通过。保留原收费、安全切换和平台只读边界。
- `TestDynamicRouteHTTPOrderAndUnknownSelector`、`TestDynamicRouteNoCandidatePersistsFailurePoolBeforeRelease`、`TestDynamicRouteAutoUsesSingleRetailSettlement`：通过。覆盖同优先级 order、未知选择器 400、无候选零派发/无 attempt/释放同事务，以及 auto 单售价、显式降档和唯一结算。
- `TestPostgresCostQuoteDomainIsolation`、Gemini 和 `TestPostgresVectorProcurement*` 目标回归：通过。覆盖物理供应商界、原报价域、采购 guard 和 `final/cumulative` usage。
- `TestDynamicRouteStatsRequireFiftyRealSamples`：通过，56.082 秒。50 个真实、限速和预算合规的商业链请求产生 50 个样本，首内容 p50/p95 与吞吐非空，周期聚合落表一致。
- `TestPostgresAdmissionFailureDecisionReplaysAfterRelease`：通过。准入失败后无 attempt 的释放记录保留选中路径并可回放。
- 合并 032 后，`TestGatewayPostgresExactCacheHitSameChargeAndZeroUpstream`、两个首事件用例、`TestPostgresRouteDataPlanePolicyIsImmutableAndVersioned` 与动态 order/auto/no-candidate 同组通过；current 015 安装及非空回填通过，合并后的 catalog/billing 目标回归通过。
- 完整 `go test ./internal/catalog -count=1 -timeout=5m`：通过，37.739 秒。
- `npm --prefix web test` 与 `npm --prefix web run build`：通过。严格请求 DTO、平台候选池和无 attempt 失败池已同步。

## 执行中发现并修复

- 深拷贝曾把空数组变成 null，导致候选摘要在账务边界不一致；现保留数组形状。
- order 不得越过 priority；未命中当前优先级池时保留原池，不再产生零权重随机调用。
- 吞吐应为高值优先，统计不足 50 的候选保持未知并参与权重抽签；max_price 不允许 unknown 冒充满足上限。
- 旧 current 测试夹具直接 SQL 写 binding/attempt 的地方已显式补物理身份；历史 ProspectiveStore 与 001–024 研究材料没有接入当前服务。

## 尚未完成或不得外推

- 完整 billing 在 459.089 秒结束；路由/成本/采购断言已通过，但钱包开账用例因另一进程持有专用库服务实例锁 782193402 而返回 `wallet_opening_service_active`。未终止该并行环境，因此本次不能记为完整 billing PASS；需在无活动服务的隔离环境重跑。
- 完整 billing/migrations/server race、真实 main、浏览器和干净提交完整 029 仍需在本文件记录最终结果；执行中的命令不能记为通过。
- 旧 attempt 非空数组的回填已安装 SQL，但尚需带真实历史行的专项证明；空库安装通过不能替代该证据。
- 尚未完成规格全部原因枚举的逐条 PostgreSQL 矩阵、质量/成本粘性打破的全组合、性能对比和 032 合入后的组合验收。
- 真实供应商、生产部署、HA 和供应商资格均未认证。local mock 与 PostgreSQL fixture 不能替代这些验收。

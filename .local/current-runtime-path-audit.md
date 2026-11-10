# 当前服务运行路径旧实现审计

审计基线为 `codex/initial-mvp` 的 `ae058b4`。审计在独立 worktree
`/Users/vchive/.codex/worktrees/current-runtime-path-audit/moni-aigate` 的
`codex/current-runtime-path-audit` 分支完成。本次只新增本报告，没有修改
Go、SQL、迁移、前端或测试。

## 结论

当前 `moni-aigate` 主服务已经把文本请求的在线计量、报价、预占、尝试、观察和结算接到当前向量模型。当前安装器也只走 `current_mvp.sql`。主服务依赖闭包中没有历史 prefix/proof/replay 维护包，也没有独立支付 sandbox 的 `paymentruntime`。

当前运行路径仍包含三类需要后续处理的旧痕迹。

1. 在线资金校验仍通过 `payment_legacy_funding_journal_valid` 验证传统 funding journal。它不是死代码，不能直接删除。
2. 在线周期预算 Reader、Commander 和 Verifier 仍显式处理 `legacy_import` 与 `legacy_catchup`。它们属于当前 periodic-budget API 和离线验证入口的行为合同。
3. `internal/billing` 中的维护型 raw reader 仍查询 024 已删除的 `input_rate`、`output_rate` 以及 `cost_input_rate`、`cost_output_rate`。这些 reader 没有被当前 `moni-aigate` 主入口调用，但因为它们属于 `internal/billing` 包而进入 Go 编译。它们是最高优先级的清理候选，不能在没有同步维护入口和测试边界的情况下单独删除。

因此，当前状态符合“主服务大部分走单一当前链路”的描述，但不符合“所有当前编译和运行入口都没有旧 billing/wallet 分支”的更强目标。

## 审计方法与证据

依据 `AGENTS.md` 的架构同步和验证要求，先阅读了 `AGENTS.md`、`goal-objective.md`、`docs/decisions/ADR-005-pre-mvp-single-path.md`、`docs/design/HLD.md`、`docs/design/LLD.md`、`docs/architecture/README.md` 和 `docs/architecture/runtime-composer.md`。

ADR-005 要求当前服务不增加旧库自动升级、双写、新旧双运行或失败 fallback，同时允许保留没有接入在线服务的历史研究材料。统一架构入口明确当前服务使用 `migrations.ApplyCurrent` 和 `VerifyCurrent`，历史 `ApplyThrough` 只供隔离 fixture 使用。

执行过的源码搜索和构建命令如下。

```sh
rg -n 'ApplyThrough|migrations\.Apply\(|VerifyThrough|fallback|legacy|compat|replay|input_rate|output_rate|CostQuote|CostScore' \
  cmd internal migrations --glob '*.go' --glob '!**/*_test.go'

PATH="/opt/homebrew/bin:$PATH" go list -deps ./cmd/moni-aigate
PATH="/opt/homebrew/bin:$PATH" go build ./...
PATH="/opt/homebrew/bin:$PATH" go vet ./...
```

构建结果为 `go build ./...` 通过，耗时约 4.3 秒。`go vet ./...` 通过，耗时约 4.4 秒。构建只证明 Go 代码和 SQL 字符串可以编译，不会执行在线 SQL，因此不能把构建绿灯当成旧列 reader 已经可用的证据。

审计 worktree 没有 `web/node_modules`，所以本次没有运行前端构建。该限制不影响本报告的 Go 入口和依赖审计。

## 当前入口审计

### migrate

实际入口在 `cmd/moni-aigate/main.go:78-116`。

| 项目 | 当前引用 | 是否进入服务运行路径 | 结论 |
|---|---|---:|---|
| 当前安装 | `migrations.ApplyCurrent` | 是 | 保留 |
| 当前基线 | `migrations/current.go:10-26` 的 `currentPlan` | 是 | 保留 |
| 空库拒绝 | `migrations/current.go:32-75` 检查 `schema_migrations` 和用户关系 | 是 | 保留 |
| 历史安装 | `migrations.Apply`、`ApplyThrough` | 否 | 仅历史 fixture 和测试调用 |
| 历史只读校验 | `VerifyThrough` | 否 | 仅历史验证材料 |

`migrations/embed.go:69-77` 的 `currentPlan` 只嵌入 `current_mvp.sql`。`migrations/embed.go:144-162` 明确把 `ApplyThrough` 定义为离线历史前缀安装。`cmd/moni-aigate/main.go` 没有调用 `Apply`、`ApplyThrough` 或 `VerifyThrough`。

当前基线先创建部分历史形状，再在同一份未发行 SQL 中删除旧列。`current_mvp.sql:5241` 删除 `price_versions.input_rate/output_rate`，`current_mvp.sql:5341` 删除 `cost_versions.input_rate/output_rate`，`current_mvp.sql:5454` 删除 request 旧价格和 bounds 列，`current_mvp.sql:5805` 删除 attempt 旧成本列。安装完成后的当前 schema 不再拥有这些标量列。

### provision

实际入口在 `cmd/moni-aigate/main.go:143-148`，调用 `operations.Provision`。实现位于 `internal/operations/operations.go:13-18` 及后续 ACL 配置。

Provision 当前只对已经安装的当前 schema 配置 APP、projection、payment、periodic-budget、meter 和 media 权限。它不执行历史 SQL，也不加载历史 prefix 或 proof。`internal/operations/meter_roles.go` 通过当前 `meter_%` 函数集合核验 APP 执行权、投影角色拒绝、PUBLIC 拒绝以及函数定义属性。

不能删除 `Provision` 中的 wallet、payment、periodic-budget 和 meter ACL。它们是当前服务启动前的权限边界，不是旧 writer。

### serve

实际入口从 `cmd/moni-aigate/main.go:200` 开始。启动顺序包含当前 schema 校验、实例锁、Recovery gate、Server 初始化、周期预算初始化、支付初始化、投影角色校验和 HTTP 监听。

主 HTTP 路由由 `internal/server/server.go:119-214` 注册。文本六协议统一进入 `internal/server/gateway.go`，核心链路包括以下当前入口。

| 环节 | 当前入口 | 旧引用审计 |
|---|---|---|
| 计划和路由 | `Catalog.CandidateSnapshot`、`routing.NewPlan` | 使用 `routecost.Quote` 完整报价和 `metering` bounds，不使用旧 `CostQuote` 或 `CostScore` |
| 客户预占 | `billing.Store.Reserve` | `internal/billing/reserve.go:181-187` 写入 `request_meter_snapshots` 和向量 quote，不写 request 旧标量列 |
| 尝试建立 | `billing.Store.RecordAttempt` | `internal/billing/evidence.go` 依赖当前 attempt snapshot 和完整 `routecost.Quote` |
| 上游发送 | `provider.Client.Do` | `internal/provider/client.go:18-20` 明确一次发送，禁止隐式 replay |
| 失败重试 | `provider.SafeToRetry` 与 `routing.Plan.CanRetry` | 只允许 rejected 或明确未发送的网络错误，属于当前未知消费保护，不是兼容 fallback |
| 观察和结算 | `billing.RecordFinalization`、`billing.Settle` | 使用 observation canonical、quantity vector 和 settlement guard |
| 采购 | `billing.recordProcurementAttempt` | 没有绑定时返回显式 `legacy_unmanaged` 分支，见下文 |

#### 在线仍存在的旧 billing 分支

`internal/billing/procurement.go:55-57` 在没有 `credential_procurement_bindings` 时直接返回，并注释为 `legacy_unmanaged`。`internal/catalog/credential_pool.go:54` 也将没有 procurement binding 的成员暴露为 `legacy_unmanaged`。这会被当前渠道目录和文本请求使用。

这不是可以凭名称删除的无用代码。当前系统仍允许未纳入采购托管的手工渠道。删除前必须先冻结未绑定渠道的拒绝合同、管理端发布流程和已有 route fixture，否则会把合法的 unmanaged route 误变成未知成本或请求失败。

#### 在线仍存在的旧 wallet/funding 分支

`current_mvp.sql:3696-3739` 创建 `payment_legacy_funding_journal_valid`，当前 `funding_journal_valid` 会调用它。`current_mvp.sql:4372-4375` 对非 sandbox、非 wallet-opening journal 继续调用该函数。当前 Go 入口也直接使用它。

| 当前调用方 | 证据 | 结论 |
|---|---|---|
| funding source 应用写入 | `internal/billing/funding.go:84,153` | 当前 wallet/billing 事务实际调用 |
| funding verify | `internal/billing/funding_verify.go:64-75` | 当前 ledger/source 检查实际调用 |
| sandbox funding 读取 | `internal/billing/funding_sandbox.go:66` | 当前 payment sandbox funding 校验调用 |
| SQL role baseline | `internal/operations/payment_roles_functions.go:7,26,533-537` | 当前 provision/verify 会核验该历史函数形状 |

该函数虽然名字带 legacy，但它已经成为当前 funding journal 的实际底层谓词。它不能直接删除。需要先把 manual credit、voucher、sandbox capture、wallet opening 四种来源统一到一个新的当前 `funding_journal_valid` 实现，再同步 migration、role baseline、wallet source check 和回归矩阵。

### worker

实际入口在 `cmd/moni-aigate/main.go:119-120` 和 `main.go:492-523`。

当前 worker 使用 `tasks.NewPostgresStore`、`provider.NewMockTaskAdapter` 和 `Server.NewTaskWorkerAdapter`。任务状态、lease、artifact、media reservation 和 media settlement 都进入当前 PostgreSQL 路径。`tasks.Task.ReplaySubmit` 位于 `internal/tasks/state.go:182-188`，它是提交幂等检查，不是历史 replay，不能按关键词删除。

worker 没有导入 `procurementprefix`、`fundinglotruntime`、`maintenancescan` 或 `paymentruntime`。`go list -deps ./cmd/moni-aigate` 也没有这些包。

### payment

主服务的支付启动入口在 `cmd/moni-aigate/payment_startup.go:20-59` 和 `cmd/moni-aigate/main.go:249-264`。

主服务使用 `internal/payments` 的 application/worker manifest、`OpenProcessor` 和 `Runner`。该路径属于当前应用 payment sandbox，Compose 默认也没有启动 `cmd/moni-payment-sandbox`。

`cmd/moni-payment-sandbox/main.go` 是独立的可选 QA 组件。它依赖 `internal/paymentruntime`、`internal/mockpsp` 和 `internal/paymentreceiver`，不在 `go list -deps ./cmd/moni-aigate` 的主服务闭包内。它可以保留为明确的 sandbox 工具，但不应被当成 `serve` 的当前 wallet/billing 路径。

主服务的 payment ACL 仍然包含 `payment_legacy_funding_journal_valid`。这属于当前 wallet source guard 的实际依赖，不能与独立 sandbox 组件混为一谈。

### periodic-budget

在线入口在 `internal/server/periodic_budget_handlers.go:21-33`，由 `main.go` 的 `app.InitializePeriodicBudgets()` 初始化。离线入口在 `cmd/moni-aigate/periodic_verify.go:58-87`，调用 `periodicbudget.NewCurrentVerifier` 和 `migrations.VerifyCurrentReadOnlyTx`。

当前 periodic-budget 代码仍有以下实际旧分支。

| 位置 | 行为 | 是否当前运行路径 |
|---|---|---:|
| `internal/periodicbudget/reader_pages.go:99-100` | 将非 `new` allocation 映射为 `legacy_customer` 或 `legacy_supplier` | 是，在线管理读取 |
| `internal/periodicbudget/commander_configure.go:37-38,127-132` | 从已有 request/reservation/attempt 创建 `legacy_import` allocation 和 event | 是，在线配置命令 |
| `internal/periodicbudget/tx_supplier.go:95-143` | 缺少当前 allocation 时建立 `legacy_catchup` | 是，supplier transaction |
| `verifier_sources.go`、`verifier_events.go`、`verifier_projections.go` | 校验 `legacy_import`、`legacy_catchup` 的历史边界 | 是，`verify-periodic-budgets` |

这些不是未引用的历史文件，而是当前 periodic-budget 合同的一部分。若目标是严格 current-only，应先冻结“不再接受 legacy origin”的新合同，再删 Commander import、supplier catchup、Reader 映射、Verifier 分支和对应 schema/test。单独删除任一分支会使当前在线 API 和只读验证器的状态集合不一致。

### provider

文本 provider 入口是 `internal/server/gateway.go:223` 的 `s.Provider.Do`，实例在 `internal/server/server.go:104` 通过 `provider.NewClient` 创建。

`internal/provider/native_usage.go` 中的 `input_tokens`、`output_tokens`、`prompt_tokens` 和 `completion_tokens` 是 OpenAI、Anthropic、Gemini 等供应商响应的原生 usage wire 字段。它们被规范化到 `provider.Usage`，再由 billing 写入当前 observation quantity vector。它们不是价格 DTO，也不是旧 scalar 计量路径，不能删除或改名来清理“scalar”关键词。

`internal/provider/client.go:18-20` 禁止隐式 replay。`provider.SafeToRetry` 只依据消费是否明确未发送或被供应商拒绝决定是否尝试第二个已记录 attempt。该 retry 是当前路由和未知消费保护的一部分，不属于旧 fallback。

### media

当前 `Server.New` 在 `internal/server/server.go:96-104` 装配 image、TTS、task 和 realtime mock adapter。数据库开启时，图片和 TTS 经过 `reserveMediaForRequest`、`FinalizeMedia` 和 `media_result_artifacts`。task worker 使用 Postgres store。Realtime 在有数据库时由 `realtimeServiceForTenant` 创建 tenant-bound PostgreSQL persistence，见 `internal/server/realtime_persistence.go:646-660`。

媒体代码存在受测试支持的无数据库分支。

| 位置 | 分支 | 当前生产 `serve` 是否进入 |
|---|---|---:|
| `internal/server/media_billing.go:23-30` | `Billing == nil || Pool == nil` 时跳过媒体账务 | 否，真实 Server 有 pool。是纯协议 fixture 路径 |
| `internal/server/realtime_persistence.go:646-654` | `Pool == nil` 时返回 `MemoryStore` service | 否，真实 Server 使用 PostgreSQL persistence |
| `internal/server/image_handlers.go:232-295,343-363` | 进程内 image record 与数据库 artifact 并存 | 进行中的请求使用，完成结果的生产读取优先数据库 |
| `internal/server/media_handlers.go:52-72,140-144` | TTS 进程内幂等 cache 与数据库 artifact 并存 | 进行中的请求使用，重启后的完成结果依赖数据库 |

无数据库分支是当前测试适配层，不是旧兼容能力。若要达到严格单一路径，应把纯协议 fixture 改为显式 mock service package，或让媒体入口在没有数据库时直接失败。不能在没有补齐进行中请求和测试替代物前删除。

## scalar 与两价 DTO 清单

### 可以保留的当前管理输入

`internal/catalog/pricing_types.go` 的 `GroupPriceCommand`、`DefaultPriceCommand`、`CostPriceCommand` 仍接收 `InputRate` 和 `OutputRate`。HTTP 入口在 `internal/server/pricing_handlers.go:340-438` 读取 `input_rate`、`output_rate`。这符合当前管理端“每百万 token 整数输入”的合同，不是旧输出 DTO。

这些值进入 `metering.ManagementTokenRates`，再以完整 `Rates` canonical/digest 写入 operation result。当前 `GroupPriceView`、`DefaultPriceView`、`CostPriceView` 使用 `Rates json.RawMessage`，不再向外暴露两个 scalar 价格字段。

因此不能删除管理输入字段，除非先改变管理端合同。可以删除的是任何将这两个输入重新作为长期价格事实、报价身份或金额计算器保存的代码。

### 已经不在当前 schema 的旧 scalar

`current_mvp.sql` 在同一未发行基线中删除了旧 price、cost、request、attempt scalar 列。当前 `billing.Reserve` 写入的是 `request_meter_snapshots`，见 `internal/billing/reserve.go:181-187`。当前 cost policy 读取 `rates_canonical` 和 `rates_digest`，见 `internal/billing/cost_policy.go:14-24`。

### 仍然引用已删除列的编译代码

以下代码属于 `internal/billing` 或历史离线包，仍可被 `go build ./...` 编译，但查询当前 schema 会失败。

| 文件 | 旧列引用 | 当前主服务是否调用 | 处置建议 |
|---|---|---:|---|
| `internal/billing/funding_financial_read.go:488-495` | `request_records.input_rate/output_rate/input_upper_bound/output_limit` | 未从 `cmd/moni-aigate` 入口找到调用 | 优先重写为 current snapshot reader，或与 maintenancescan 一起移出当前构建 |
| `internal/billing/procurement_attempt_read.go:191-221` | `cost_versions.input_rate/output_rate` 和 `upstream_attempts.cost_input_rate/cost_output_rate` | 未从 `cmd/moni-aigate` 入口找到调用 | 优先重写为 `cost_rate_lines`、attempt snapshot reader，或移出当前构建 |
| `internal/fundinglotscan/projection_funding.go:23-25` | 旧 request scalar projection | 不在主服务依赖闭包 | 历史 prefix/proof 体系整体隔离后删除或归档 |
| `internal/procurementprefix/**` | scalar DTO、replay、旧列和旧成本金额 | 不在主服务依赖闭包 | 从当前构建入口移出，保留历史材料直到测试策略关闭 |

`go build` 不能发现这些问题，因为它们是 SQL 字符串。它们是本次审计发现的明确清理对象。

## legacy reader、writer、proof、replay 清单

### 当前主服务没有进入的历史包

`go list -deps ./cmd/moni-aigate` 的结果包含 `internal/fundinglotverify`，但不包含以下包。

- `internal/procurementprefix`
- `internal/fundinglotruntime`
- `internal/fundinglotcatalog`
- `internal/fundinglotproof`
- `internal/fundinglotscan`
- `internal/maintenancescan`
- `internal/maintenancetrust`
- `internal/periodicbudgetprefix`
- `internal/fundinglotprefixcompat`
- `internal/paymentruntime`
- `internal/paymentreceiver`
- `internal/mockpsp`

这些包的 raw reader、writer、proof、replay 和 prefix mapping 不进入 `migrate`、`provision`、`serve` 或 `worker` 的主依赖闭包。它们是删除或拆分出当前构建的候选，但不能只删除单个文件，因为它们仍被历史测试、QA 脚本、`cmd/moni-payment-sandbox` 或互相引用的研究材料使用。

### 编译进入但没有主入口调用的历史接缝

以下包文件属于主服务依赖的 `internal/billing` 或 `migrations` 包，因此会进入二进制编译，即使没有运行时调用。

- `migrations/original22.go` 和 `ApplyThrough` 相关历史验证材料
- `internal/billing/funding_financial_read.go`
- `internal/billing/funding_ledger_read.go`
- `internal/billing/procurement_attempt_read.go`
- `internal/billing/prospective.go`
- `internal/billing` 中为 `fundinglotverify` 提供 raw facts 的 adapters

这些不能被当前服务的 URL 入口证明为已验收。尤其是带旧列的 reader 应先改成当前 vector snapshot reader，或拆成不被 `cmd/moni-aigate` 编译的离线工具包。

### 不能按关键词删除的 replay、reader、proof

以下名称看起来像旧实现，但承担当前正确性。

| 符号或文件 | 实际含义 | 处理 |
|---|---|---|
| `pricingCommand` 的 `replayCheck` | operation 幂等重放和意图冲突保护 | 保留 |
| `billing.WalletOpeningResult.Replayed` | wallet opening 外部 reference 幂等结果 | 保留 |
| `tasks.Task.ReplaySubmit` | task submit 幂等结果 | 保留 |
| `provider.observedReader` | 上游 response body 的受控读取和 usage 观察 | 保留 |
| `internal/fundinglotverify` 当前 graph checks | wallet、采购和来源边界检查 | 先拆 current validator，再评估 legacy 文件 |
| `metering` vector parser/digest | 当前唯一文本计量事实 | 保留 |

## 可删除和不能删除的分类

### 可以作为清理候选

这些项目没有被当前 `cmd/moni-aigate` 入口依赖，适合下一片单独处理。

1. 将 `internal/procurementprefix`、`internal/fundinglotruntime`、`internal/fundinglotscan`、`internal/maintenancescan`、`internal/maintenancetrust` 和 prefix/proof 相关包从当前产品构建图中拆出。
2. 将 `migrations/original22.go`、`ApplyThrough` 和历史 verifier 从当前迁移 package 拆到显式历史测试工具，避免当前服务二进制编译这些代码。
3. 删除或重写 `internal/billing/funding_financial_read.go`、`procurement_attempt_read.go` 中读取已删除 scalar 列的查询。优先改为当前 meter snapshot 和 rate-line reader，再决定是否保留 reader API。
4. 如果不再交付独立 payment sandbox，可删除 `cmd/moni-payment-sandbox` 及 `internal/paymentruntime`、`internal/mockpsp`、`internal/paymentreceiver`。在继续支持本地 payment sandbox 的前提下，它们不能算无用代码。

### 当前不能删除

1. `migrations.ApplyCurrent`、`VerifyCurrent`、`current_mvp.sql` 的空库拒绝和 schema gate。
2. 当前 `metering`、`routecost`、`request_meter_snapshots`、`attempt_meter_snapshots`、rate lines、canonical 和 digest。
3. 管理端每百万 token 的两个整数输入，以及它们到完整 Rates 的一次性转换。
4. `provider.Usage` 的供应商原生 token 字段和 `SafeToRetry` 的未知消费保护。
5. `billing` 的唯一 Reserve/Attempt/Observe/Settle、采购预算、wallet journal、funding lot、refund 和 recovery 链路。
6. `payment_legacy_funding_journal_valid` 及其当前调用，除非先完成当前 funding source validator 替换并更新 SQL、ACL 和验收。
7. periodic-budget 的 legacy origin 分支，除非先冻结并实施 current-only 状态机和迁移策略。
8. media 的数据库路径、artifact 访问控制、任务 lease、Realtime tenant-bound persistence 和 provider mock adapter。

## 建议的清理顺序

1. 先处理 `funding_financial_read.go` 和 `procurement_attempt_read.go`。这两处已经能够由当前 SQL 删除列直接证伪，优先级高于按目录名删除历史代码。
2. 单独冻结 periodic-budget current-only 合同，再移除 `legacy_import`、`legacy_catchup` 的在线写入和读取分支。
3. 用当前 funding source graph 重写 `payment_legacy_funding_journal_valid`，完成 role baseline 和 wallet source 负控后再删旧谓词。
4. 将 prefix/proof/replay 研究包从当前 Go 构建图拆出。保留历史测试材料时，入口应明确标为 offline historical tooling。
5. 最后处理无数据库 media/realtime fixture 分支和进程内 cache。它们不是旧兼容代码，必须先补齐测试服务替代物和进行中请求的失败边界。

本报告没有执行任何生产代码清理。清理前仍需针对每一项重新执行 `rg`、`go list -deps ./cmd/moni-aigate`、`go build ./...`、`go vet ./...`、相关 PostgreSQL 专项和架构检查。

## 审计后主目录并行改动

审计 worktree 建立后，主目录 `/Users/vchive/Documents/ChatGPT/aigate/moni-aigate` 出现了尚未提交的四个文件改动，分别是 `internal/billing/media.go`、`migrations/024_unified_text_metering.sql`、`migrations/current_mvp.sql` 和 `specs/028-unified-metering-media/u-storage-contract.md`。这些改动没有进入本审计分支，也没有被本次 `go build` 和 `go vet` 验证。

只读检查显示，这片并行改动正在给 media profile、media attempt 和 media settlement 增加 supplier rate 与 supplier cost 状态。它不改变本报告对文本 scalar、历史 prefix 包、payment legacy journal validator 或 periodic-budget legacy origin 的结论，但会改变 media 章节的当前字段形状。主目录改动合并或提交后，需要重新执行媒体相关 `rg`、PostgreSQL migration、billing、server 和 report 测试，再把 media 审计结果作为独立片更新。

## 2026-11-10 集成复核

supplier cost 片已在主目录完成并通过专项。四个 deterministic mock profile 现在保存 supplier 每百万 unit 费率，media attempt 冻结该费率，唯一 media settlement 保存 known/unknown supplier cost。billing、reporting、server image/TTS/task/Realtime 和 migrations 专项通过。该片只补 mock supplier cost snapshot，不改变本报告列出的 payment legacy validator、periodic-budget legacy origin 或维护 reader 清理项。

## 2026-11-10 当前构建图隔离

旧维护 raw reader 已从默认当前构建图隔离。`internal/billing` 中的 funding financial、ledger、source、procurement budget、attempt、approval 和 command reader，以及显式 `ProspectiveStore`，统一使用 `historical` build tag。`internal/maintenancescan` 与 `scripts/maintenance-scan-qa` 也只在该 tag 下编译。

默认 `go list -json ./internal/billing` 的 GoFiles 已不再包含这些 reader，`go list -deps ./cmd/moni-aigate` 不包含 `internal/maintenancescan` 或这些历史 reader。默认 `go build ./...`、`go test -run '^$' ./...` 和 `go vet ./...` 通过。使用 `-tags historical` 的 billing、maintenancescan 和 maintenance-scan-qa 编译通过，历史专项仍可显式运行。该片只改变构建可见性，不转换历史数据，也没有把维护路径接回当前服务。

# serial-v1 回归耗时剖析与 identity current-baseline 小优化

2026-11-20。本文先复用精确 main 的既有完整证据，不将局部实验或 workers=2 结果冒充默认 serial-v1 收益。优化合同见[040 后续子规格](../../specs/040-acceptance-parallelism/serial-v1-current-fixture.md)。

## 1. 证据边界

对照均来自提交 `c0d02b14ba711330ff432aa126573f2f1179190a`、tree `e7a4b21dc8a53503cf73e735c2aeccbb58d2fc7c` 和同一稳定 `colima-moni-acceptance-040`：

- serial-v1 run `5b680fb01f8144eeae32895dc8544346`：41/41 PASS，100/100 current-MVP 复用，整单 2873.930 秒；原始证据在 `~/.cache/moni-aigate-acceptance/029-main-c0d02b1-serial`。
- workers=2 run `556ee429caee4ca0a6a2f2b777a658a9`：41/41 PASS，整单 3706.895 秒；原始证据在 `~/.cache/moni-aigate-acceptance/029-main-c0d02b1-r3`。
- serial 比 workers=2 快 832.965 秒。完整终态多重集仅有非必需 `TestPostgresOriginalProcurementIdentityABI` 的环境型 PASS/SKIP 差异；不能写成全部 multiset 相等，也不能据此默认启用 workers=2。

以下包/测试拆分直接解析原始 `go test -json` 事件；package `Elapsed`、顶层测试 `Elapsed`、事件时间戳和 runner 墙钟是不同口径，分别列示。

## 2. serial-v1 阶段拆分

| 阶段/套件 | 实测秒数 | 说明 |
| --- | ---: | --- |
| 整单 | 2873.930 | 完整 029 |
| Go race | 2249.402 | `go test -race -p 1 -count=1 -timeout 1200s -json ./...` |
| serial tail | 609.048 | current-MVP 及独立生命周期/支付/部署 |
| current-MVP | 99.728 | 039 已复用 100 个 Go 必需测试，不重复执行 |
| identity real-main | 30.870 | 独立串行证据 |
| mail real-main | 139.312 | 独立串行证据，含自然 lease |
| payment process | 157.654 | 独立支付沙箱证据 |
| mock deployment | 180.835 | 含 preview start 0.357 秒和部署 180.478 秒 |

静态命令合计 6.188 秒，前端测试/构建合计 8.695 秒。串行尾部各独立套件具有独立进程、数据库、自然 lease、支付或部署边界，本次不复用、不并行、不缩短其等待。

## 3. Go race 包、测试与编译/初始化拆分

serial 原始日志有 80 个 package 终态和 2511 个唯一顶层测试终态（2299 PASS、212 环境型 SKIP、0 FAIL）。Go race 2249.402 秒可近似分解为：

| 构成 | 秒数 | 口径 |
| --- | ---: | --- |
| package `Elapsed` 合计 | 2177.142 | 包进程启动后到 package 终态 |
| 其中顶层测试 `Elapsed` 合计 | 1957.600 | 包括各自子测试与测试清理 |
| 首个顶层 run 之前的包内启动/init | 144.947 | 含 race runtime 与包级 init |
| 最后顶层测试终态到 package 终态 | 73.613 | 包级收尾/测试进程退出 |
| 相邻 package 终态到下一 package start | 63.870 | 主要为编译、链接与进程调度空隙 |
| 首包 start 前及末包终态后框架余量 | 约 8.390 | 与上述项合计闭合到命令墙钟 |

最大的 package：

| package | package 秒数 | 顶层测试秒数 |
| --- | ---: | ---: |
| `internal/server` | 561.522 | 559.680 |
| `internal/billing` | 379.693 | 378.110 |
| `internal/identity` | 311.097 | 309.400 |
| `internal/fundinglotcatalog` | 161.587 | 121.980 |
| `migrations` | 92.090 | 90.500 |
| `internal/periodicbudget` | 78.191 | 76.550 |
| `internal/operations` | 75.008 | 73.440 |
| `internal/alerts` | 74.002 | 72.440 |

最慢顶层测试包括 `TestPostgresCashRefundAlertSourceUpperBoundAndIndex` 42.830 秒、`TestUnifiedTextMeteringRejectsEveryListedLegacyRelation` 42.340 秒、`TestWalletProfilePeerDependencyMirrorsRejectDamagedBase` 39.980 秒、`TestMVPPostgresActualMainSIGTERMDrainsSSEAndRestartDoesNotReplay` 27.770 秒和 `TestCurrentTreeMatchesLegacyPartitionArtifacts` 27.240 秒。这些测试包含真实数据库、真实主进程或大型不可变材料核验，不能仅凭耗时就删除、跳过或放宽。

### 冷/热缓存与容器成本

serial 控制是在已有构建材料上的完整运行；包间空隙合计只有 63.870 秒，不是 2249 秒命令的主因。040 历史冷缓存 workers=2 诊断曾达到 runner 3511.480 秒；引入同 attempt 构建材料复用后的严格候选两轮 runner 墙钟降为约 2013 秒量级，但这不能替代精确 main 对照。

精确 main workers=2 的 other part 为 1475.795 秒，包 `Elapsed` 合计 1434.297 秒；同一批 other 包在 serial 中约 923 秒。server 四个 part 的 package `Elapsed` 合计约 1132 秒，也高于 serial server 的 561.522 秒。容器/虚拟化中的执行成本抵消了双 worker 的理论并行收益。other shard 另有 261.676 秒的 part 前开销，代码路径是 `run_shard` 内再次执行 `discover_other`；该重复 discovery 是后续 workers=2 专项候选，不在本次 serial 小优化中修改。

## 4. 选定的小优化与定向实验

identity 的 61 个顶层测试合计 309.400 秒。两个身份夹具共创建 51 个独立 schema，却仍逐个重放历史 001–024；现行生产入口及 server/billing 夹具已经使用 `ApplyCurrent`。本 PR 只把这两个夹具改为 current baseline 安装器，不改密码散列、账户、角色、断言或清理。

在未修改源码的 Go overlay 中，同一 55439 专库、同一本机 Go 工具链进行三次交错测量：

| 定向测试 | 历史 `Apply` 三次秒数 | `ApplyCurrent` 三次秒数 | 平均差值 |
| --- | --- | --- | --- |
| `TestIdentitySessionRotationAndTenantBoundary` | 6.781 / 6.656 / 6.386 | 6.213 / 6.265 / 6.296 | 约 0.350 秒/次 |
| `TestSelfServiceRegistrationInitializationMinimumPrivilegesAndSingleUse` | 8.508 / 8.312 / 8.134 | 7.914 / 7.755 / 7.756 | 约 0.476 秒/次 |

这些数值只证明局部优化假设成立，不按照 51 次线性外推整单收益。历史迁移专门测试和负控仍保留。

## 5. 实现后专项验证

- 完整 `internal/identity` race 在 55439 专库通过：61/61 顶层测试 PASS，package `Elapsed=296.223` 秒，顶层测试合计 294.620 秒。相对精确 main serial 原始事件中的 311.097 秒少 14.874 秒；这是不同命令轮次的专项实测，不单独冒充整单收益。
- 完整 `migrations` race 通过，96.899 秒；历史与 current 安装/拒迁测试均继续执行。
- `phase1-acceptance.test.sh` 通过：v1 5、v2 18、runner 30、coordinator 5、phase1 22、browser 12，矩阵 41 IDs/82 bindings。

架构 reviewed refresh、`make check` 和最终干净提交的完整 serial-v1 029 是本 PR 的交付门禁。完整 029 使用仓库外 `~/.cache/moni-aigate-acceptance/040-serial-current-fixture` 证据根；最终 run ID、41 项结果、整单/Go/identity/serial-tail 对照及清理结论随 PR 和会话交接记录发布，不在验收后改写已经执行的干净提交。

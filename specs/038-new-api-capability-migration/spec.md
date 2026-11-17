# 038：New API 能力内收总合同与能力台账

2026-11-17；状态：PR-1 设计基线。本规格是后续迁移切片的总索引和共同边界，不表示任何新增运行能力已经实现，也不替代 032、033、供应商资格或生产验收。具体代码、迁移和 UI 变更必须先形成独立切片合同，再按本规格验收。

## 1. 目标与固定基线

目标是把仍需依赖 New API/Sub2API 的用户能力，按供应商、协议/profile 和运营工作流逐项内收到 aigate，并在迁移期间保持可验证、可回退、可审计。

- New API 对照基线固定为 `QuantumNous/new-api@e449cc700eb569cdfc2199cb73e97de46944f43c`。
- Sub2API 外部 channel 基线固定为 `Wei-Shaw/sub2api@10ba8f9b4dc2e6023d4a9879ab77b21841babd10`。
- 不追逐浮动 `main`/`latest`；需要后续版本能力时另立变更记录和资格验证。
- 对齐的是客户与运营可完成的业务能力，不是 New API 的数据库、quota 字段、后台 API 或视觉像素。
- 固定版本中自身为 `RelayNotImplemented`、注释或显式拒绝的入口，不因路由存在而计入上游已交付能力。

逐项现状、参考位置、优先级和证据入口见[能力台账](capability-matrix.md)。阶段顺序仍以[四阶段路线](../../docs/design/phase-roadmap.md)为准；本规格不提前完成 032/033 或第四阶段。

## 2. 数据所有权与不可破坏的不变量

aigate 继续是唯一公网入口，以及客户身份、公开目录、售价、最终路由、钱包、计量、结算、采购成本和审计的唯一权威。New API/Sub2API 只是内部 channel，可保留自己的供应商账号池和局部重试。

所有文本内收切片继续使用：

`Profile → Plan → Reserve → Attempt → Observe → Settle`

共同约束：

1. 租户隔离、不可变发布版本、硬预占、唯一结算、迁移只追加和应用/投影/迁移角色分离不变。
2. 不建立第二套客户钱包、计量、结算、采购成本或路由事实源；不共享或双写下游数据库。
3. `sending`、可能已消费、`unknown` 或缺可靠 usage 的请求不得因迁移、缓存、恢复或故障切换而重放；只有明确未发送或按已冻结 profile 证明为 `rejected_no_consumption` 时，才可沿用现有 `provider.SafeToRetry` 与 `routing.Plan.CanRetry`，且不得放宽其判定。
4. 最多两次 attempt、发送授权和后台恢复边界不变；恢复任务不得重发模型生成请求。
5. 客户 COIN、付款法币和供应商成本保持分型；未知用量、成本或外部结果不得伪造为零或成功。
6. 媒体、任务和 Realtime 继续共享钱包与账务，但不在前三阶段加入文本动态路由；如需多候选路由，另立完整执行核心 SDD。
7. 客户 Key 摘要、上游凭据密文、模型正文和其他租户信息不得进入公开 DTO、日志、导出或截图。

## 3. 台账模型：迁移状态与资格证据分离

“实现位置”“本地行为验证”“真实供应商资格”和“生产资格”是不同事实，不得合并成一个可通过代码存在推导的状态。

### 3.1 实现与迁移状态

| 状态 | 含义 |
| --- | --- |
| `already_internal` | 当前正式路径已由 aigate 实现；只说明实现位置，仍需单独记录验证范围 |
| `external_relay` | 由 New API/Sub2API 提供该 profile 的上游执行或账号池能力 |
| `embedded_adapter` | aigate 内置实现已存在，正在按 profile 做合同、差异和灰度验证 |
| `migrated_capability` | 对应切片的协议、资金、运营 UI、恢复与回退证据完整，可承担该 profile 的正式内部实现 |
| `retired` | 对应下游能力已摘流并按 §4.4 退役；不影响其他 profile 继续使用外部 channel |
| `postponed` | 有明确后续触发条件，但当前没有实施授权 |
| `out_of_scope` | 不属于本迁移目标 |

历史 channel/version 的 `integration_mode` 不被原地改写。台账状态是设计和证据索引，不是新的运行时路由表。

### 3.2 验证与资格标签

每个 profile 分别记录以下证据；它们可以独立存在，不构成单向捷径：

| 标签 | 最低证据 | 不能证明 |
| --- | --- | --- |
| `declared` | 代码、路由或配置存在 | 行为正确、可售或已完成迁移 |
| `mock_verified` | deterministic fixture、专用 PostgreSQL、统一商业链和故障矩阵 | 真实供应商兼容或成本正确 |
| `external_flow_verified` | 真实外部服务拓扑、下游模型映射、usage/错误/成本与唯一结算 | 下游每个供应商或模型已认证 |
| `supplier_profile_qualified` | 指定供应商、模型、协议和账号权限的真实请求、取消/断流、最终 usage、采购成本与账单核对 | 其他供应商、模型、协议或生产 SLA |
| `production_qualified` | 目标部署、密钥、备份恢复、容量、安全和运行门禁 | 由本规格自动授予；始终独立验收 |

现有 029 结果只作为相应提交的本地 MVP 非回归基线。新增或共享核心变更不得沿用旧提交证据冒充新功能验收。

在现行四阶段路线未正式修订前，提前发送的真实请求只能记录为**内测资格探测**：可以暴露协议、usage 或成本问题，但不授予 `supplier_profile_qualified`，不作为 M1–M4 的 PR/阶段完成条件，也不替代第四阶段逐 profile 认证。

### 3.3 台账必填字段

NA-01…NA-18 是能力域汇总，不替代具体 profile 明细。每个 NA 汇总行至少记录：能力 ID、固定上游参考和源码路径、当前 aigate 模块及 HTTP/UI 入口（没有时明确缺口）、实现/验证状态或缺口、证据入口、目标切片与依赖，以及适用、后置或非目标范围。汇总行可以共享本台账声明的基线提交和 `source_mode`，不重复堆砌相同字段。

每个具体供应商/模型/协议 profile 或实现切片必须另建详细记录，并至少包含：

- 所属 NA 项、固定上游参考提交和源码路径；
- 当前 aigate 模块、HTTP/UI 入口和已有验证记录；
- 实现状态、验证标签、目标切片和依赖；
- 协议/模型/供应商/profile 范围及明确排除字段；
- 迁移、回退和退役条件；
- `source_mode=reference_only|vendored`，以及适用的 LICENSE/NOTICE/修改记录；
- 证据路径、对应提交和未关闭问题。

空缺表示未完成，不得以空字符串、`not_applicable` 或历史链接绕过必需证据。确实不适用的字段必须附理由。

## 4. 迁移、切流、回退与退役状态机

### 4.1 建立内置候选

`external_relay → embedded_adapter` 通过新 channel/version、profile、绑定和 route revision 完成：

1. 冻结供应商与协议/profile 合同，明确输入/输出硬界、usage、成本、错误和无消费证据。
2. 复用 `internal/provider`、`internal/catalog`、`internal/routing` 和现有商业链，不让 adapter 直接写钱包。
3. 使用同一 deterministic fixture 比较 external 与 embedded 的可观察合同。
4. 控制台同步展示能力、资格、探测、错误、审计和回退状态。

### 4.2 灰度与切流

- 切流以 expected-revision CAS 激活新 route revision，只影响新请求。
- 灰度只使用现有路由合同已经支持的逻辑模型/profile 范围；如需租户级灰度，必须先另冻授权、版本和撤销合同，不能假设现有激活接口已经支持。
- 在途请求保留原 route/profile/price/cost/credential 快照，不随切换漂移。
- 影子比较只使用 fixture、只读观测、已有请求事实和账单核对，不得为比较再发一次真实生成请求。

### 4.3 回退与未知结果

- 回退也是新 route revision；不修改历史 channel、attempt 或 settlement。
- 只有明确未发送或可信无消费时，才可按现有 attempt 预算选择其他实现。
- `sending`、`unknown`、缺可靠 usage，或已向客户写出任何字节后，不跨实现重放。
- 激活结果未知时先读取目标 revision 和审计，不盲目重复激活；恢复 worker 只处理原 attempt。

### 4.4 退役门禁

只有对应切片同时满足以下条件，才可把该下游能力标为 `retired`：

1. 内置实现的协议/字段、usage/成本、错误/取消、权限、恢复、UI 和桌面/390px 证据完整。
2. 用于付费流量的 profile 已有相应真实供应商资格；mock 或单次 HTTP 200 不足。
3. 029 非回归和本片新增验收在同一候选提交上通过，CI 全绿。
4. 先通过新 revision 摘除下游流量并保留可执行回退；观察与差异处理完成后才停用对应下游配置。
5. 退役范围精确到 profile/能力，不要求 New API/Sub2API 整体退出。

## 5. 发布证据与接口合同（PR-2 细化物理实现）

后续发布门禁至少覆盖以下逻辑字段；PR-1 不新增 SQL 或运行 API：

| 类别 | 必需事实 |
| --- | --- |
| 身份 | channel kind/mode、不可变版本、供应商、上游模型、协议和 capability profile |
| 运行边界 | endpoint/网络与地域证据、凭据版本、输入/输出硬界、usage 与终态语义 |
| 重试 | 下游局部重试摘要、consumption 分类、safe-to-retry 证据和 attempt 预算 |
| 商业 | 客户售价版本、采购成本版本、成本未知处理和证据引用 |
| 资格 | 当前验证标签、适用供应商/模型/协议、证据提交与未关闭问题 |
| 来源 | `source_mode`、来源仓库/提交、LICENSE/NOTICE、本地修改与源码提供方式 |

约束：

- 未具备本片要求的资格证据时，不得发布为相应的可售 profile；现有 mock 不得回填成真实资格。
- direct 没有下游网关重试时，应明确记录该事实及自身安全重试证据，而不是留空跳过。
- 资格/证据 DTO 只在授权管理读面暴露；公开目录和客户 API 不泄漏内部 endpoint、凭据或供应商错误正文。
- 物理表、迁移、HTTP 和权限矩阵由 PR-2 的独立规格冻结；不得在本总合同下直接猜字段上线。

## 6. 控制台与移动端共同合同

保持 Vue 3、TypeScript、Element Plus 和 aigate 自研视觉；不嵌入 New API React/Semi Design 页面。每个迁移切片同时交付适用的以下能力：

1. **信息**：能力、版本、资格、观察时刻、来源和限制可见；目录发现不等于授权或可售。
2. **操作**：创建、编辑、探测、同步草稿、审批、切流/回退等均有权限、确认和审计；不展示无后端支撑的假动作。
3. **状态**：区分加载、空、部分失败、未认证、冲突、未知、恢复只读和已完成；不用单一“成功”掩盖子项失败。
4. **错误**：使用统一错误 envelope、request ID 和安全错误码；写操作结果未知时先核查原操作，不自动重复。
5. **审批**：继续复用制单/审批分离、单人例外留痕和敏感操作再认证，不按 New API 单人 quota 操作简化。
6. **下载**：服务端固定快照、租户/角色裁剪、窗口上限和审计；不包含秘密或模型正文，浏览器验证实际文件与 SHA-256。
7. **恢复**：复用原操作回执、只读刷新和显式恢复编辑；需要跨刷新保留时复用 IndexedDB journal，不保存秘密。
8. **移动端**：桌面和 390px 下主要操作、错误、审批、下载均可完成；不得只有页面截图或存在性断言。

实现优先复用 `web/src/api.ts`、`session.ts`、`PlatformView.vue`、各域 `*-state.ts`、`CommerceCommands.vue`/`PricingCommands.vue`/`AlertCommands.vue`、`operating-report.ts` 和 `scripts/phase1_browser_qa.py`。完整候选池、抽签和回放不在 UI 或 038 另建事实，统一使用 033 合同。

## 7. 源码复用、许可证与来源

默认方式是参考固定上游行为、合同和测试，在 aigate 模块中实现。允许 vendor 有明确价值的叶子模块，但不复制上游用户、quota、钱包、支付事实或数据库作为 aigate 的第二权威。

- 参考 New API `relay/channel/*`、渠道运营、错误分类和协议 fixture 时，记录具体路径与固定提交。
- 复制或链接源码前，将该组件标为 `vendored`，保留版权头、LICENSE、NOTICE、来源提交、本地修改和对应源码提供方式。
- New API 为 AGPLv3 且有 NOTICE 义务；Sub2API 为 LGPLv3。拆模块或拆进程不当然消除义务，不能以“独立重写”替代实际来源审查。
- 不复制 New API 的视觉、品牌或 quota 语义；复制受 NOTICE 约束的 UI 素材前必须落实署名和发布义务。
- 扩展 `scripts/check-upstream-provenance` 覆盖实际 vendored 文件与修改记录；检查在 CI 中只读，不能为通过门禁盲目改常量。

## 8. 优先级与规格依赖

| 顺序 | 工作包 | 范围与依赖 |
| --- | --- | --- |
| M0 | 038 总合同、能力台账和发布门禁设计 | 本片只交付设计；PR-2 另冻物理合同 |
| M1 | 核心文本逐 profile 内收 | 先 OpenAI-compatible Chat JSON/SSE、工具、usage、错误/取消，再 Responses、Messages、Gemini、Embeddings、Rerank；本阶段以 deterministic/mock 和统一商业链验收，真实供应商资格留第四阶段 |
| M2 | 渠道运营与控制台缺口 | 模型同步草稿、标签/筛选、只读余额、已有失败诊断展示、客户 CSV/用量视图、待核查体验；不复制资金模型 |
| M3 | 032 文本数据面 | 遵循已冻结合同；缓存须先裁决 D-032-1/D-032-2，性能按同条件实测 |
| M4 | 033 文本动态路由 | 在 032 后实施；物理模型、质量档位、完整决策、回放和粘性使用 033，不在 038 重建 |
| M5 | 扩展产品能力 | 真实媒体/长连接、正式支付/原路退款、订阅、OAuth/OIDC/Passkey、站点运营分别立项和验收 |

后续切片按“一种供应商/协议 profile 或一个运营工作流一个 PR”交付。可以并行做互不依赖的设计与只读取证，但共享资金、路由、迁移和 UI 集成由集成人统一复核。

## 9. 验证与完成定义

### 9.1 PR-1

- 能力台账覆盖 NA-01…NA-18，并标明已实现范围、缺口、后置和非目标。
- HLD/LLD、四阶段路线、上游接入路线和统一架构链接一致；规划不写成已实现。
- 不修改运行代码、SQL、部署、远程环境或其他修复分支。
- 运行架构语义复核、只读检查和 `make check`。

### 9.2 每个实现切片

- 先冻结 spec/HLD/LLD、状态机、API/数据/权限/故障与验收合同，再实现。
- 使用显式专用 `TEST_DATABASE_URL` 运行相关真实 PostgreSQL/race 和 `make verify-mvp`；必需 SKIP、缺失测试和无效 `-run` 不算通过。
- deterministic fixture 覆盖成功、流式/工具、缺 usage、错误、取消、未知消费、恢复、唯一结算与回退。
- 真实 main 和隔离浏览器完成桌面+390px 的操作与 DOM 断言；审批、下载、冲突和未知结果按切片实际覆盖。
- 第四阶段的资格切片必须单独保存脱敏真实 request、usage/成本和账单证据；此前如有真实请求，只记录为内测资格探测，不授予资格。凭据只在授权环境内使用，不写入仓库或日志。
- 共享路由、协议、账务或迁移的发布候选，在最终 merge commit 上重跑完整 029 非回归清单。
- CI 全绿后合入；架构正文、演化记录和 manifest 由集成人 reviewed refresh 后交付，CI 不自动刷新。

## 10. 非目标

- 一次性 fork、替换或删除 New API/Sub2API。
- 导入或双写下游客户、密码、Key 明文、钱包、quota、订单或数据库。
- 签到、邀请奖励、分佣、任意插件/脚本/MCP 执行环境。
- 供应商专有协议全覆盖，或把固定上游版本未实现的入口变成本项目兼容承诺。
- 以本总合同替代正式支付商选择、媒体状态机、HA、生产部署或性能验收。

## 11. 首批交付顺序

1. PR-1：本总合同、[能力台账](capability-matrix.md)、设计链接和架构记录，不改运行行为。
2. PR-2：[发布资格与证据门禁物理合同](release-evidence-gate.md)、追加迁移、服务端校验、只读管理 UI 与 provenance 检查；平台导航收口见[侧栏子规格](navigation-refinement.md)。
3. PR-3：一个 OpenAI-compatible Chat/SSE profile，完成内置适配差异、deterministic/mock 统一商业链、控制台、回退和桌面/390px 证据。可并行做真实内测探测，但它不是本 PR 的完成条件，也不授予真实供应商资格。

每个 PR 只扩大已验证的范围，不把 M0–M5 合并为巨型迁移。

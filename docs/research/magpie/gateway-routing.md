# magpie × moni-aigate：网关 / 路由 / 供应商 能力对照清单

- 研究日 2026-11-25。**只读**：未修改任何仓库，未运行 magpie。
- magpie 源码：`/tmp/magpie-study`，HEAD `e66d872a8eaecc285b0d597917d6b520035712ec`。引用形如 `<相对路径>:行号@e66d872`；所有行号均为本次实际读到的位置（文档行极长，一行即一条完整规则）。
- aigate 源码：worktree 根（基于 main `41f66e4`）。远端 main 已到 `9fd8dd6`（PR #35，仅 2 个小修：TTS 鉴权、task 存在性隐藏）。除 `internal/server/server.go`、`internal/server/media_handlers.go`、`internal/server/task_handlers.go` 外，本文引用的文件在两版之间无差异；这三个文件按 `origin/main` 行号引用并标 `(main)`。
- 本文是网关 / 路由 / 供应商侧的逐项判定对照稿，与同目录的 [gui-ux.md](gui-ux.md)、[agent-ecosystem.md](agent-ecosystem.md) 并列，总览见 [README.md](README.md)。所有行号均在本次重新核对。

## 0. 判定口径

| 判定 | 含义 |
|---|---|
| **已支持** | aigate 有等价实现，且规格/验证记录里有验收证据（多为本地 mock + 029，不等于真实供应商资格） |
| **部分支持** | 有相关实现，但缺 magpie 的某些子能力；逐条写明缺什么 |
| **未支持** | aigate 没有对应实现，规格里也没有明确排除 |
| **不适用** | 单用户本机工具形态，多租户服务端网关不需要；说明理由 |
| **设计上刻意不做** | aigate 规格/架构明确排除；给出出处 |

两侧的根本差异先说清楚，后面每一项都由它决定：

- **magpie 的目标函数是"把订阅配额用干净、让 agent 不中断"**：失败前先归因、尽量换号、等一等再试。它的持久化是本机 JSON 文件，没有租户、没有账本、没有"这次生成到底收了谁的钱"的问题。
- **aigate 的目标函数是"质量/成本/毛利约束下选供应商，且每一分钱可核对"**：重试资格只看"能否证明上游没有消费"（`specs/032-phase2-text-data-plane/spec.md:31-36`），一个请求一份预占一份结算，路由决策落库可回放（`docs/architecture/README.md:110`）。

因此 magpie 里大量"可用性优先"的机制在 aigate 是**有意不采用**的，而不是漏做。

## 1. 汇总表

**统计（共 106 项）**：已支持 21 · 部分支持 28 · 未支持 39 · 不适用 7 · 设计上刻意不做 11。

### 1.1 协议与端点（17 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| P1 | 四协议互转（IR 翻译） | 有，任意客户端协议 ↔ 任意上游协议 | 无；客户端协议必须等于 profile 协议 | 未支持 |
| P2 | Chat / Responses / Messages / Gemini 文本入口 | 有 | 有（文本 + 基础工具） | 已支持 |
| P3 | 透传整理（工具配对修复、文本内工具调用、fenced JSON、整数修正等） | 有，几十条 | 无；严格校验直接 400 | 未支持 |
| P4 | 流式 SSE | 有 | 有 | 已支持 |
| P5 | 客户端函数工具 | 有 | 有（仅 `type=function`） | 已支持 |
| P6 | reasoning 携带（三种拼写、签名、密封块） | 有 | 无；reasoning 参数被拒 | 未支持 |
| P7 | 多模态输入（图片/视频/文件） | 有 | 无；只收纯文本 part | 未支持 |
| P8 | Embeddings | 有 | 有 | 已支持 |
| P9 | Rerank | 有 | 有（Jina） | 已支持 |
| P10 | 图像生成 | 有，多家厂商翻译 | mock only；PNG、3 个尺寸、n=1 | 部分支持 |
| P11 | 图像编辑 `/v1/images/edits` | 有 | 无 | 未支持 |
| P12 | 视频生成（异步） | 有，`/v1/videos` 全套 | mock only；`/v1/tasks` 的 `video_generation` | 部分支持 |
| P13 | Realtime WebSocket | 有，双向透传 + 按 `response.done` 计量 | mock only；强制单 turn | 部分支持 |
| P14 | Responses WebSocket | 有 | 无 | 未支持 |
| P15 | TTS / STT | 有 | TTS mock（固定模型/音色/WAV）；无 STT | 部分支持 |
| P16 | 其他端点（count_tokens、`/responses/compact`、Decisions、Ollama 兼容、`previous_response_id` 等） | 有 | 无 | 未支持 |
| P17 | 供应商模型自带 system prompt 追加 | 有 | 无 | 未支持 |

### 1.2 路由组模式（22 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| R1 | `smart`（Quota first：续期最早者先） | 默认模式 | 无配额窗口信号 | 未支持 |
| R2 | `order`（列表顺序） | 有 | priority 分层 | 已支持 |
| R3 | `rotate`（逐请求轮转） | 有 | 无（用加权抽签） | 未支持 |
| R4 | `usage`（least used） | 有 | 无 | 未支持 |
| R5 | `pace`（周剩余/小时） | 有 | 无 | 未支持 |
| R6 | `weight`（平滑加权轮转） | 有 | 加权抽签（可回放） | 已支持 |
| R7 | `free`（免费模型先） | 有 | 无 | 未支持 |
| R8 | `fast`（最近速度） | 有，衰减均值 + 探针 | 仅客户端 `route.sort`，15 分钟窗口统计 | 部分支持 |
| R9 | `drain`（用完一个换下一个） | 有 | 无 | 未支持 |
| R10 | `cheap`（当前时点最便宜） | 有 | `cost_first`（018） | 已支持 |
| R11 | `expiring`（免费额度先过期先用） | 有 | 无 | 未支持 |
| R12 | `fit`（小上下文先） | 有 | 只做 `context_too_small` 硬过滤，不排序 | 部分支持 |
| R13 | 手工指定 / `only` / `order` / 禁 fallback | 有 | `route.only/order/ignore/allow_fallbacks` | 已支持 |
| R14 | 用量份额分层（90% / 98% 档） | 有 | 无 | 未支持 |
| R15 | 质量档位分层 + auto + 显式降档 | 无对应（分层是用量份额） | 有（033） | 已支持 |
| R16 | 组内成员/账号选择（嵌套组、成员固定 effort、成员绑账号、组绑 key、key 级路由覆盖） | 有 | 只有 binding → 凭据池 | 部分支持 |
| R17 | 池内凭据选择（权重 + 容量） | 有 | 有（1..32 成员） | 已支持 |
| R18 | 并发 / 排队 / RPM / 账号预备 / Sink | 有 | 只有渠道与凭据并发容量；满即拒，不排队 | 部分支持 |
| R19 | 突发请求分散、新会话最少持有 | 有 | 无 | 未支持 |
| R20 | 规则路由（时间、tokens、消息正则、system 文本、暂停） | 有 | 无 | 未支持 |
| R21 | intent routing（回合级意图分类器） | 有 | 仅 SKU 默认类 + 受控 hint | 设计上刻意不做 |
| R22 | 路由组模板 | 6 种 | 无 | 未支持 |

### 1.3 故障处理（12 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| F1 | 失败分类（credit/quota/rate/restricted/overloaded/other…） | 20 余种 `fail*` | 13 类 `FailureClass` + 3 种 `Consumption`，只认机器码 | 部分支持 |
| F2 | rest 时长表与爬梯（含跨重启持久） | 有 | 熔断 + 429 冷却 + 凭据持久隔离；无爬梯 | 部分支持 |
| F3 | overloaded 独立处理（固定短休、不计失败） | 有 | 无；529 计入渠道故障 | 未支持 |
| F4 | forbiddenAlike 归因（请求的错还是账号的错） | 有 | 只对已知请求级错误类不计故障 | 部分支持 |
| F5 | failover 提交边界（未发任何回复才换） | 有 | 有，且更严（首事件前 + 仅无消费证据） | 已支持 |
| F6 | "未发回复即可换手"的宽松切换集合（401/403/429/5xx…） | 有 | 明确不采用 | 设计上刻意不做 |
| F7 | hold 流（保活、思考扣留、拒答交接、patience 等待） | 有 | 只有首事件扣留 | 部分支持 |
| F8 | 工具调用后不换手 | 有 | 有，且更严（写出任意字节即提交） | 已支持 |
| F9 | 同候选原位重试 / 等待 Retry-After | 有 | 明确不采用 | 设计上刻意不做 |
| F10 | 空回复 / 循环回复 / 工具循环检测 | 有 | 无 | 未支持 |
| F11 | 慢启动作为独立健康信号 | 有 | 无 | 未支持 |
| F12 | 特殊归因（区域拒绝、风控封禁、harness-only） | 有 | 无 | 未支持 |

### 1.4 会话粘性（4 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| S1 | 粘性模式 session / turn / off / auto / strict | 5 种 | 开关 + `max_age_s` + `break_on` | 部分支持 |
| S2 | cacheWorth 判据 + 厂商冷却 / 1h 缓存时长 | 有实测值 | 无 | 未支持 |
| S3 | 会话标识来源 | 原生 session 头 / `X-Magpie-Session` / 开场白哈希 | 仅 `X-Moni-Session` 头 | 部分支持 |
| S4 | 替补归还（主路径恢复后会话回去） | 有 | 无 | 未支持 |

### 1.5 供应商与账号（13 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| V1 | 预设供应商目录 | 63 个顶层预设 | 0；手填 base URL | 未支持 |
| V2 | 自定义供应商（多协议 base URL） | 有 | 单 base URL、单 provider 类型 | 部分支持 |
| V3 | 订阅账号接入（Claude/ChatGPT/Copilot/Gemini…） | 内置 + 社区插件 | 自身不做，交给 Sub2API 叶子 | 设计上刻意不做 |
| V4 | 配额窗口模型（Allowance） | 有 | 无（只有采购账户日/月预算） | 未支持 |
| V5 | 余额查询 | 自动读 | 仅人工余额观察 | 部分支持 |
| V6 | 模型发现与目录 | 有 | 有（三格式发现 + 公开目录） | 已支持 |
| V7 | 上游列表价自动目录（models.dev） | 有 | 无 | 未支持 |
| V8 | 自定义价格 | 用户自价、分 key 费率、时段窗口 | 价格版本 + 租户组价 | 部分支持 |
| V9 | 供应商成本价登记（不可变版本） | 列表价代理 | 有（018 成本版本） | 已支持 |
| V10 | 多 Key 池 | 有，逐 key 权重/代理/计划 | 不可变凭据池，权重 + 并发 | 部分支持 |
| V11 | provider 插件（Bun 宿主） | 有 | 明确排除 | 设计上刻意不做 |
| V12 | 云厂商签名接入（Bedrock/Vertex/Azure） | 有 | 无（NA-04 后续切片） | 未支持 |
| V13 | 外部网关作为上游（remote magpie ↔ New API/Sub2API channel） | 有 | 有 relay，无 hop 专有机制 | 部分支持 |

### 1.6 用量 / 成本 / 预算（9 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| U1 | Reserve→Settle 本地预算 | gateway key 的 token/成本上限 | 钱包硬预占 + Key 累计额度 + 唯一结算 | 已支持 |
| U2 | 多窗口限额 / 跟随账号窗口 | 有 | tenant/key/采购 的 UTC 日/月预算 | 部分支持 |
| U3 | Learns 应答报数 | 有 | 无 | 未支持 |
| U4 | Spent 预滤（预算耗尽的模型提前移出候选） | 有 | 只有采购预算预滤 | 部分支持 |
| U5 | OTLP 导出 | 有 | 无 | 未支持 |
| U6 | 账单归集（按模型/agent/供应商/账号/key，CSV） | 有 | 有（026 经营报表） | 已支持 |
| U7 | 币种与汇率 | USD 存、CNY 显示（自动汇率） | COIN/CNY + P0 USD/CNY 保守 FX 版本 | 部分支持 |
| U8 | 缓存读写 token 计量与折扣 | 计量 + 分维价 | 计量有，折扣价无 | 部分支持 |
| U9 | service tier / fast mode 计价 | 有 | 无 | 未支持 |

### 1.7 中间件与 hook（10 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| M1 | JS 网关中间件（onRequest/onProvider/onAccount/onEvent/onResponse） | 有 | 明确排除 | 设计上刻意不做 |
| M2 | New API 兼容 `param-override` | 社区中间件 | 无 | 未支持 |
| M3 | New API 兼容 `model-map` | 社区中间件 | 模型别名 + binding 上游模型映射 | 部分支持 |
| M4 | `system-prompt` 注入 | 社区中间件 | 无 | 未支持 |
| M5 | `word-guard`（敏感词） | 社区中间件 | 无 | 未支持 |
| M6 | `think-tags` | 社区中间件 | 无 | 未支持 |
| M7 | 异步 request hooks（Bun） | 有 | 明确排除 | 设计上刻意不做 |
| M8 | 中间件提供路由策略 / onUsage | 有 | 明确排除 | 设计上刻意不做 |
| M9 | fail open / fail closed 分点 | 两个点两种策略 | 只有原则，无扩展点 | 部分支持 |
| M10 | 请求脱敏（规则 + 本地模型，fail closed） | 有 | 无 | 未支持 |

### 1.8 网关内工具（8 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| T1 | web search（多搜索 API 聚合、key 轮换、mix） | 有 | 明确不托管工具 | 设计上刻意不做 |
| T2 | `web_fetch` | 有 | 明确不抓取 URL | 设计上刻意不做 |
| T3 | 图像生成 / 识图作为 MCP server | 有 | — | 不适用 |
| T4 | MCP router（hub） | 有 | — | 不适用 |
| T5 | fast tools（Antigravity 插件） | 有 | — | 不适用 |
| T6 | memory（`/mcp/magpie/memory`） | 有 | — | 不适用 |
| T7 | skill router | 有 | — | 不适用 |
| T8 | Codex / Claude Desktop 旁路调用专用处理 | 有 | — | 不适用 |

### 1.9 运维（11 项）

| # | 能力 | magpie | aigate | 判定 |
|---|---|---|---|---|
| O1 | LAN 共享与命名 key | 有 | 有（租户/成员/API Key，更强） | 已支持 |
| O2 | caller key 的 models 限制 | 精确 / `*` / `re:` 正则 | 精确 UUID / `*` | 部分支持 |
| O3 | 短期 key、OIDC 换 key、只读 key、管理 key | 有 | 无（NA-15 后置） | 未支持 |
| O4 | 远程 magpie 级联（多跳、dcz 压缩、逐跳只执行一次） | 有 | 明确禁止执行器互连成链 | 设计上刻意不做 |
| O5 | mTLS（自建 CA、客户端证书） | 有 | — | 不适用 |
| O6 | Docker | 有 | 有（Dockerfile + Compose） | 已支持 |
| O7 | 代理 / 出口配置 | 全局/供应商/账号/key 四级代理 | 只有出口 allowlist，无代理 | 部分支持 |
| O8 | 入口防护（CORS、防重绑定、来源 allowlist） | 有 | 有（全 `/v1` 强制 API Key + 可信代理） | 已支持 |
| O9 | 管理 API | 有 | 有（`/api/platform/*`） | 已支持 |
| O10 | 路由 trace / 实时事件流 | trace + SSE 事件流 | 决策落库 + 只读查询 + 回放 CLI；无实时流 | 部分支持 |
| O11 | 升级时监听 socket 交接 | 有 | 无（单活 + 排空重启） | 未支持 |

## 2. 逐项细节

### 2.1 协议与端点

**P1 四协议互转 — 未支持。**
magpie：上游不会说客户端的 API 时，由网关翻译到"第一个可用 API"（`docs/subsystems/gateway-routing.md:67@e66d872`）；跨协议有损处按字段决定：密封的 reasoning 只回原生 API（`docs/subsystems/gateway-routing.md:404@e66d872`），带不动的视频直接 400（`docs/subsystems/gateway-routing.md:2715-2716@e66d872`）。本次只读了文档对翻译行为的描述，未读其 IR 与编解码器源码。
aigate：协议由目录决定，请求协议必须等于 profile 协议，否则 400（`internal/provider/native_request.go:9-20`）。每个逻辑模型只有一个 `profile_version`（`specs/033-text-dynamic-routing/spec.md:93`）。041 把"长尾供应商协议"划给 New API 叶子（`specs/041-profit-hybrid-platform/spec.md:54`），direct 侧没有互转规划。

**P2 — 已支持。** 入口 `internal/server/server.go:282-289` (main)，协议表 `docs/architecture/README.md:627-634`，证据 `specs/038-new-api-capability-migration/capability-matrix.md:18-19`（`already_internal + mock_verified`）。

**P3 透传整理 — 未支持。** magpie 在转发路径上修几十种上游/客户端怪癖：未配对的工具调用补合成结果（`docs/subsystems/gateway-routing.md:392-395@e66d872`）、参数里的 `2500.0` 改成 `2500`（`docs/subsystems/gateway-routing.md:440@e66d872`）、模型把工具调用写进正文时还原成真调用（`docs/subsystems/gateway-routing.md:441@e66d872`）、Markdown 围栏里的 JSON 还原成结构化输出（`docs/subsystems/gateway-routing.md:442@e66d872`）。aigate 的立场相反：未知字段、非文本 part、非法工具体一律 400（`internal/provider/request.go:59-63`、`internal/provider/request.go:380-393`、`internal/provider/request.go:411-430`）。

**P4 — 已支持。** magpie 对上游永远请求流式，非流式客户端拿聚合结果（`docs/subsystems/gateway-routing.md:67@e66d872`）。aigate 按客户端请求原样转发，流式路径 `internal/server/gateway.go:584-700`，超时合同 `internal/provider/types.go:275-287`。

**P5 — 已支持。** aigate 只处理客户端 `tools[].type=function`、`tool_choice`、assistant `tool_calls` 与工具结果（`internal/provider/request.go:52-58`、`internal/provider/request.go:431-491`）；网关不执行函数（`docs/design/LLD.md:83`）。

**P6 reasoning 携带 — 未支持。** magpie 读 `reasoning_content` / `reasoning` / `reasoning_details` 三种拼写（`docs/subsystems/gateway-routing.md:333@e66d872`），签名与密封块跨协议携带（`docs/subsystems/gateway-routing.md:404-412@e66d872`）。aigate：profile 的 `ReasoningOutput` 只允许 `none`（`internal/provider/types.go:62-65`、`internal/provider/types.go:116-118`）；Chat 可选参数闭集不含任何 reasoning 字段（`internal/provider/types.go:119-134`），带了就 `unsupported_parameter`。

**P7 多模态输入 — 未支持。** magpie 识别各协议的图片、视频、文件 part（`docs/subsystems/gateway-routing.md:443-446@e66d872`、`docs/subsystems/gateway-routing.md:2705-2735@e66d872`）。aigate：消息 content 的 part 只允许 `{"type":"text"}`（`internal/provider/request.go:380-393`）；物理模型能力的 modalities 必须恰为 `["text"]`（`internal/routing/dynamic.go:97`）；台账写明"Gemini 工具/多模态未覆盖"（`specs/038-new-api-capability-migration/capability-matrix.md:19`）。

**P8 / P9 — 已支持。** `internal/provider/types.go:21-22`，`specs/038-new-api-capability-migration/capability-matrix.md:20`。

**P10 图像生成 — 部分支持。** magpie 把 `/v1/images/generations` 翻译到十余家厂商各自的 API（`docs/subsystems/gateway-routing.md:64@e66d872`、`docs/subsystems/gateway-routing.md:1207-1220@e66d872`）。aigate 有入口（`internal/server/server.go:278-279` (main)），但只有一个 profile、仅 PNG、仅 256/512/1024、n 必须为 1（`internal/media/input.go:21-31`、`internal/media/profile.go:7`），且只有 deterministic mock，真实 adapter 未验收（`docs/architecture/README.md:39`、`docs/architecture/README.md:1377`）。缺：真实供应商、多格式、编辑、路由组、多厂商翻译。

**P11 — 未支持。** magpie `docs/subsystems/gateway-routing.md:1201@e66d872`；aigate 无此路由。

**P12 视频生成 — 部分支持。** magpie：`/v1/videos` 创建、轮询、下载，多家厂商（`docs/subsystems/gateway-routing.md:1202-1204@e66d872`、`docs/subsystems/gateway-routing.md:1228-1301@e66d872`）。aigate：`/v1/tasks` 的 `video_generation`（`internal/server/server.go:272-277` (main)，`internal/server/task_handlers.go:152,167` (main)），mock only，且不参与路由（`docs/architecture/README.md:1371`）。

**P13 Realtime — 部分支持。** magpie 双向转发帧、按 `response.done` 的 usage 记账（`docs/subsystems/gateway-routing.md:3355-3393@e66d872`，`internal/gateway/realtime.go:146-149@e66d872`）。aigate 服务端把 `MaxTurns` 固定为 1（`internal/server/realtime.go:198`），adapter 写死 `mock-realtime`（`internal/server/realtime.go:137`）；领域层默认上限 100 turn（`internal/realtime/realtime.go:33`）但未接线。

**P14 — 未支持。** magpie `docs/subsystems/gateway-routing.md:3267-3353@e66d872`，`internal/gateway/responses_ws.go:67,192@e66d872`。aigate 仅 `/v1/realtime` 用 WebSocket。

**P15 TTS/STT — 部分支持。** magpie 转发 TTS、转写、翻译（`docs/subsystems/gateway-routing.md:1160-1163@e66d872`、`docs/subsystems/gateway-routing.md:1442-1459@e66d872`）。aigate `/v1/audio/speech`（`internal/server/server.go:280` (main)）只接受固定模型、固定音色、`wav`（`internal/server/media_handlers.go:206,237` (main)）；无 STT。

**P16 其他端点 — 未支持。** magpie 的 `/v1/responses/compact`、`/v1/codex/models`、Ollama 兼容（`docs/subsystems/gateway-routing.md:53@e66d872`）、Decisions（`docs/subsystems/gateway-routing.md:65@e66d872`）、System One（`docs/subsystems/gateway-routing.md:1117-1153@e66d872`）、`previous_response_id` 续接（`docs/subsystems/gateway-routing.md:3395-3471@e66d872`）。aigate 的 `/v1` 路由全集见 `internal/server/server.go:269-289` (main)，均无。

**P17 — 未支持。** magpie 给某模型追加运营者自己的 system prompt（`docs/subsystems/gateway-routing.md:3611-3643@e66d872`）。aigate 对 `system` 只做校验后原样转发（`internal/provider/native_request.go:96`）。

### 2.2 路由组模式

**结构对照。** magpie 分两层（组成员排序、供应商内 key/账号排序），共用一个策略点（`internal/routes/routes.go:1-11@e66d872`）；默认 Smart 在核心，其余由内置插件 `rotation` 注册（`plugins/rotation/rotation.go:19-30@e66d872`）。策略只负责排序，返回的不是合法排列就回退列表顺序（`internal/routes/routes.go:207-229@e66d872`）。
aigate 是单一 Plan/Engine：硬过滤 → 质量档位 → 最高 priority 层 → 粘性/顺序/成本/排序偏好 → 加权抽签 → 凭据抽签（`internal/routing/selection.go:40-388`）。默认 `manual`：按优先级/权重/健康/容量（`docs/architecture/README.md:685`）。

**R1 `smart` — 未支持。** 语义：有余量的账号里"续期最早者先"，比最大窗口、截到小时；同小时比 `Lapse`（即将作废）；再比钱包余额；90%/98% 分档；会自报用量的未知账号先试一次（`internal/routes/strategies.go:159-211@e66d872`，档位 `internal/routes/strategies.go:17-39@e66d872`）。aigate 没有任何配额窗口输入，`Candidate` 的排序字段只有 priority、weight、成本、统计、质量（`internal/routing/routing.go:27-60`）。目标函数不同，不必然需要。

**R2 `order` — 已支持。** magpie 列表顺序（`internal/routes/strategies.go:244@e66d872`）。aigate 最高 priority 层 + `route.order`（`internal/routing/selection.go:163-174`、`internal/routing/selection.go:269-293`）。

**R3 `rotate` — 未支持。** magpie 每请求挪一位的确定性轮转（`internal/routes/strategies.go:316-322@e66d872`）。aigate 用可回放的加权抽签（`internal/routing/selection.go:317-336`），不是轮转。

**R4 `usage` — 未支持。** magpie 按供应商报的已用份额，其次按近期待发送量（1 小时半衰，`internal/gateway/routing.go:38-64@e66d872`）；不报份额的 key 排在有余额者之后（`internal/routes/strategies.go:340-408@e66d872`）。aigate 凭据选择只看权重（`internal/routing/selection.go:339-354`）。

**R5 `pace` — 未支持。** 周剩余/小时最大者先，一成以内算同档（`internal/routes/strategies.go:431-483@e66d872`）。

**R6 `weight` — 已支持。** magpie 平滑加权轮转，粘性留下的会话把本轮记到实际应答者（`internal/routes/round.go:27-80@e66d872`）。aigate 加权抽签并记录抽签值（`internal/routing/selection.go:317-336`）。差异：aigate 不因粘性调整轮次。

**R7 `free` — 未支持。** magpie 模型在某供应商免费就先花免费的（`internal/routes/strategies.go:524-534@e66d872`）。aigate 无"免费档"标志。

**R8 `fast` — 部分支持。** magpie：每个账号+模型维护首 token 与输出速度的衰减均值（半衰期 20 分钟），按"首 token + 200 token 的时间"排名，一成以内算同档，每 5 个请求拿 1 个给未测量者（`internal/routes/fastest.go:39-125@e66d872`）。aigate：`route.sort=latency|throughput`，用 15 分钟窗口、至少 50 样本的 p50/p95 首内容延迟与吞吐（`internal/routing/dynamic.go:243-250`、`internal/routing/dynamic.go:659-673`，`internal/routing/selection.go:512-555`）。缺：运营者级的默认排序策略（只是 Chat 的客户端偏好）、样本不足时跳过而非探索、无衰减均值、不区分凭据。

**R9 `drain` — 未支持。** 用完一个才换下一个，续期后不回头（`internal/routes/strategies.go:287-308@e66d872`）。

**R10 `cheap` — 已支持。** magpie 按请求时点价格（含错峰、时段倍率）（`internal/routes/strategies.go:618-634@e66d872`）。aigate `cost_first`：同 priority 层内按保守用量界成本比价，未知成本拒绝，`big.Int` 不提前舍入（`docs/architecture/README.md:796-798`，`internal/routing/selection.go:294-303`）。差异：aigate 只在"保守上界"意义上最便宜，不含时段价。

**R11 `expiring` — 未支持。** 免费额度先过期先用（`internal/routes/strategies.go:553-589@e66d872`）。

**R12 `fit` — 部分支持。** magpie 先滤掉装不下的，再在剩下的里选窗口最小的（`internal/routes/strategies.go:644-671@e66d872`）。aigate 只做 `context_too_small` / `output_limit_too_small` 硬过滤（`internal/routing/dynamic.go:466-467`），不偏好小窗口。

**R13 — 已支持。** aigate `route.only/order/ignore/allow_fallbacks/require_parameters/sort/max_price/task_hint`（`internal/server/route_preference.go:12-22`，`internal/routing/dynamic.go:115-124`），只作用于 Chat（`internal/server/gateway.go:100-107`）。

**R14 — 未支持。** 见 R1。aigate 的"档位"是质量档（economy/standard/premium，`internal/routing/dynamic.go:69-80`），与用量份额无关。

**R15 — 已支持。** auto 商品跨档位，默认不允许静默降档，降档记 `tier_changed`（`internal/routing/selection.go:185-219`、`internal/routing/selection.go:371-373`；`specs/033-text-dynamic-routing/spec.md:227-237`；证据 `docs/verification/phase3-dynamic-routing.md`）。

**R16 — 部分支持。** magpie：组可嵌套；成员可固定 effort 或多档；成员可绑定到某一账号；组可限定账号集合或绑定到某些 gateway key；key 可覆盖组的路由（`docs/subsystems/gateway-routing.md:640-773@e66d872`，`internal/access/access.go:220-227@e66d872`）。aigate：候选 = binding（渠道 + 物理模型）→ 凭据池（`internal/server/gateway.go:139-160`）；Key 的限制是对逻辑模型的 UUID 授权（`specs/017-key-configuration/spec.md:5`）。缺：嵌套、成员级 effort、成员绑账号、key 级路由覆盖。

**R17 — 已支持。** aigate 1..32 个不可变凭据成员，各带权重与并发上限（`internal/catalog/credential_pool.go:147`，`internal/routing/selection.go:339-354`）。

**R18 — 部分支持。** magpie：每 key/账号并发上限、队列长度与等待、滚动 60 秒 RPM、"同时使用账号数"上限 + 预备账号、Sink（429 但仍有配额者排到队尾）（`internal/provider/provider.go:224-256@e66d872`、`internal/provider/provider.go:150@e66d872`，`internal/gateway/concurrency.go:202,389@e66d872`，`internal/gateway/rpm.go:122-190@e66d872`，`internal/gateway/sink.go:43-134@e66d872`）。aigate：渠道并发与凭据共享并发（`internal/routing/routing.go:40-42`，`internal/routing/selection.go:82-84`）、Key 级 RPM/突发/并发（`specs/016-key-runtime-limits/spec.md:5`）；满额不在上游前排队，直接失败（`ErrUnavailable` → 503 `no_available_channel`，`internal/server/gateway.go:344-359`）。缺：排队、渠道级 RPM、预备账号、Sink。

**R19 — 未支持。** 同时到达的请求按在途数分散（`internal/gateway/burst.go:32@e66d872`，`docs/subsystems/gateway-routing.md:353@e66d872`），新会话选持有会话最少的（`docs/subsystems/gateway-routing.md:352@e66d872`）。aigate 同权候选靠抽签，不看在途。

**R20 规则路由 — 未支持。** magpie 规则条件含时间窗/星期、tokens 阈值、消息正则、system 文本、effort、子代理，另有暂停规则（`docs/subsystems/gateway-routing.md:284-289@e66d872`）。其中"按文件夹/按 agent"是本机 agent 概念，对 aigate 不适用；时间窗与 tokens 阈值对商业路由有意义但未规划。

**R21 intent routing — 设计上刻意不做。** magpie 在用户回合开始时问一次分类模型（Jev 决策模型或普通 chat 模型），判意图和所需 effort，整回合保持；失败/超时沿用上一回合意图；答案缓存 10 分钟、失败后歇 30 秒（`internal/gateway/classify.go:37-48@e66d872`、`internal/gateway/classify.go:178-250@e66d872`，`internal/gateway/rules.go:224-447@e66d872`）。aigate 明确排除："不读取 prompt 猜测任务，不增加在线 LLM 分类器"（`specs/041-profit-hybrid-platform/spec.md:100`），非目标列"用额外 LLM 读取客户正文做任务分类"（`specs/041-profit-hybrid-platform/spec.md:31`）。现有替代是 SKU 默认任务类 + 受控 hint，来源只有 `sku|hint`（`internal/economics/policy.go:103-108`）。

**R22 — 未支持。** 6 种一键模板（`docs/subsystems/gateway-routing.md:92-99@e66d872`）。

### 2.3 故障处理

**F1 失败分类 — 部分支持。**
magpie：约 20 种失败原因（`internal/gateway/routing.go:456-528@e66d872`），核心是 `failure()` 按状态码 + 厂商词表分 credit / quota / rate / other（`internal/gateway/routing.go:573-641@e66d872`），再加 restricted、verify、auth、shape、overflow、refused、slow 等；词表匹配前先解 `\uXXXX` 转义。
aigate：13 类 `FailureClass` + 3 种 `Consumption`（`internal/provider/types.go:189-213`）；`classifyUpstream` 只看状态码和 error 对象的 `code`/`type`，不匹配人类消息（`internal/provider/classification.go:8-56`）。
缺：(a) credit 与 quota 不分——402 与 `insufficient_quota` 都是 `ClassQuota`，动作相同；(b) 见 F3；(c) 403 一律 `rejected`，没有"账号被风控"的概念；(d) 只认机器码：厂商用中文或自由文本表达"欠费/模型不存在"时只能按裸状态码归类，且 `Consumption` 为 unknown，既不隔离也不换手。

**F2 rest 时长表与爬梯 — 部分支持。**
magpie（`internal/gateway/routing.go:239-299@e66d872`）：欠费 30 分钟起步，每再拒一次升一档 30m→2h→8h→1d→3d→7d（`internal/gateway/routing.go:299@e66d872`）；配额耗尽到 reset，无 reset 词 15 分钟，上限 8 天；限流按 Retry-After，反复限流封顶 30 分钟；其他失败 1 分钟起逐次翻倍到 10 分钟；供应商说"很快能过"的不爬梯（`internal/gateway/routing.go:308@e66d872`）。失败计数按 key/account 与按模型分开记（`docs/subsystems/gateway-routing.md:382-383@e66d872`），欠费爬梯落盘 `rests.json` 跨重启（`docs/subsystems/gateway-routing.md:291@e66d872`）。
aigate：熔断 `closed/open/half_open/quarantined`，阈值 2..20、open 1s..10 分钟、半开 1..4 令牌（`internal/routing/routing.go:253-291`、`internal/routing/routing.go:406-465`）；429 冷却按 Retry-After，默认 30 秒、上限 5 分钟（`internal/server/gateway.go:797-807`）；凭据失效/配额类直接持久撤销该凭据（`internal/server/gateway.go:808-831`）；模型/上下文错误持久隔离 binding（`internal/server/gateway.go:793-796`）。
缺：分类型时长、爬梯、冷却状态跨重启、手动解除冷却。另有一处值得注意：冷却的作用域是 `HealthKey = channel:upstream_model`（`internal/server/gateway.go:159`），池内一个 key 被 429 会让整个渠道+模型冷却，而 magpie 默认只歇那个 key。

**F3 overloaded — 未支持。** magpie 把 529、`overloaded_error` 等识别为"供应商忙"：固定 30 秒、最多 1 分钟、不计入爬梯（`internal/gateway/routing.go:258-259,2057@e66d872`）。aigate 5xx 一律 `ClassUpstream`（`internal/provider/classification.go:26-28`）并计入渠道故障（`internal/server/gateway.go:520-532`）。

**F4 forbiddenAlike — 部分支持。**
magpie：一个请求里同供应商、同模型、同 effort 的两个账号给出同样的"裸 403"，判定为请求本身被拒——两个账号都不休息，第一个的休息与计数撤回（`internal/gateway/routing.go:114-117@e66d872`、`internal/gateway/routing.go:185-221@e66d872`）。裸 5xx 同理，但要求供应商 5 分钟内回答过别的请求（`internal/gateway/routing.go:130-139@e66d872`、`internal/gateway/routing.go:165-174@e66d872`）。文档把它定为规则："不同失败必须分开，不许合并成一桶"（`docs/subsystems/gateway-routing.md:363@e66d872`）。
aigate：`caller/cancelled/configuration/rate_limit/model/quota/policy/rejected` 类结果为 Neutral，不计故障（`internal/server/gateway.go:520-532`）——这覆盖了"已知是请求的错"的部分。缺：裸 403 之外的推断；尤其裸 5xx 仍计故障，一个被上游固定拒绝的请求连打几次（默认阈值 3 次，`internal/routing/routing.go:275-277`）就可能触发熔断。

**F5 failover 提交边界 — 已支持。** magpie：fallback 只在未发出任何回复时发生（`docs/subsystems/gateway-routing.md:296@e66d872`），靠 `holdWriter` 把回复扣到"明确通过"（`internal/gateway/fallback.go:2735-2741@e66d872`、`internal/gateway/fallback.go:2948-2964@e66d872`）。aigate：流式先读完首个完整事件再决定（`internal/server/gateway.go:368-376`），写出首个 chunk 即 `CommitDownstream`（`internal/server/gateway.go:616-619`），之后 `CanRetry` 恒假（`internal/routing/routing.go:197-201`）。aigate 更严：换手还要求无消费证据，且最多 2 次。

**F6 宽松切换集合 — 设计上刻意不做。** magpie：401/402/403/404/408/429/5xx 以及若干 400 词都在回复发出前换下一个候选（`internal/gateway/fallback.go:2161-2199@e66d872`）。aigate 明文："重试资格只由 `Consumption` 决定，不由 `FailureClass` 或状态码决定……普通 5xx、发送后读超时……默认不可透明重试"（`specs/032-phase2-text-data-plane/spec.md:31-36`）；`SafeToRetry` 只接受可信无消费拒绝或未写出的网络失败（`internal/provider/types.go:233-236`），无消费状态码必须来自冻结 profile（`internal/provider/client.go:120-127`）。理由是资金：换手等于再发一次可能被计费的生成。

**F7 hold 流 — 部分支持。**
magpie：扣住回复期间给客户端发 SSE 注释保活（3 秒后开始、每 5 秒一次，`internal/gateway/fallback.go:3438-3443@e66d872`）；扣留思考最多 4 分钟以便拒答后换手（`internal/gateway/fallback.go:3220@e66d872`）；全组都忙时在 patience 预算内轮询重试，默认 60 秒、上限 240 秒（`internal/provider/group.go:147-148@e66d872`，`docs/subsystems/gateway-routing.md:292@e66d872`）；整组行走总预算 540 秒（`internal/gateway/fallback.go:49@e66d872`）。
aigate：只有"首个事件通过校验前不写 headers/心跳/body"（`docs/design/LLD.md:445`），流中跳过 heartbeat/usage 事件（`internal/server/gateway.go:604-606`），重试前最多等 2 秒（`internal/server/gateway.go:388-399`）。缺：提交前保活、思考扣留与拒答交接、patience 等待、循环截断。其中 patience 与保活都依赖"可能已消费仍可重试"，与 F6 同源。

**F8 — 已支持。** magpie 记录"回复里已出现工具调用"，之后不再交接（`internal/gateway/fallback.go:2773-2776@e66d872`）。aigate 写出任意字节即提交（`internal/server/gateway.go:616-619`），覆盖此情形。

**F9 原位重试 — 设计上刻意不做。** magpie 对"可能一会儿就好"的失败在同一候选上再试，忙类 2 次、限流 3 次、间隔 1 秒起翻倍、上限 8 秒（`internal/gateway/fallback.go:2442-2502@e66d872`）。aigate："最多两次 attempt（跨渠道），不在同渠道换 Key"（`specs/032-phase2-text-data-plane/spec.md:33-34`），已试过的候选标 `already_tried`（`internal/routing/selection.go:79-81`）。

**F10 空回复 / 循环检测 — 未支持。** magpie：回复在至多 24 个不同单元里循环 256 个单元即截断（`internal/gateway/loop_guard.go:45-48@e66d872`）；同一工具调用同样结果重复 8 次直接由网关作答（`internal/gateway/tool_loop.go:38@e66d872`）；空回复在 2 个账号上出现就判定是请求的问题（`internal/gateway/continuation.go:64@e66d872`）；流被截断、200 但不是 API 回答都按失败处理（`docs/subsystems/gateway-routing.md:321-326@e66d872`）。aigate 未发现对应逻辑。

**F11 慢启动 — 未支持。** magpie：成员在组的首 token 时限内没开始，第一次不罚，30 分钟内第二次就让该模型在那个账号上歇 1 分钟起翻倍（`internal/gateway/routing.go:286-287@e66d872`，`docs/subsystems/gateway-routing.md:386@e66d872`）。aigate 的首事件超时是每 attempt 的总超时（`internal/provider/types.go:278`），超时归 `timeout` 类，不重试也不产生"这个账号慢"的信号。

**F12 特殊归因 — 未支持。** magpie：区域拒绝不歇账号并换新连接（`docs/subsystems/gateway-routing.md:364@e66d872`）；风控封堵把该供应商挡在本次请求之外，不再逐个试它的账号（`internal/gateway/fallback.go:2128-2134@e66d872`）；OpenRouter 的 harness-only 403 不算 key 的错（`docs/subsystems/gateway-routing.md:365@e66d872`）。

### 2.4 会话粘性

**S1 模式 — 部分支持。**
magpie：`auto`（默认）/`session`/`turn`/`off`/`strict`（`docs/subsystems/gateway-routing.md:66@e66d872`）。`affine` 的判定顺序是：关闭→无记录→账号已不在→strict→预备账号→休息中→积分垫后→忙碌垫后→回落模型该归还→顺序该归还→替补该归还→时间规则结束→98% 用尽→session→回合内→缓存不足→缓存已冷→保持（`internal/gateway/affinity.go:749-858@e66d872`）。
aigate：`StickinessPolicy{Enabled, MaxAgeS 1..86400, BreakOn ⊆ {quality,cost,health}}`（`internal/routing/dynamic.go:24-28`、`internal/routing/dynamic.go:51-62`）；命中则直接选中（`internal/routing/selection.go:252-268`），建立与延续时写 `route_sessions`，到期时间从本次 attempt 起算（`internal/billing/route_decision_store.go:111-129`）。
缺：`turn`（回合内工具循环保持）与 `strict`（永不搬家）两种语义；`auto` 的"按缓存值不值得"判定；`off` 是有的（不启用）。

**S2 cacheWorth 与冷却时长 — 未支持。** magpie：上次读缓存 + 写缓存 ≥ 1024 token 才值得保持（`internal/gateway/affinity.go:53@e66d872`）；默认冷却 5 分钟，ChatGPT 后端与 DeepSeek 各按实测取 1 小时（`internal/gateway/affinity.go:56-75@e66d872`、`internal/gateway/affinity.go:94-102@e66d872`）；Anthropic 1 小时写另有 1 小时保温（`internal/gateway/affinity.go:79@e66d872`）；写也算读（`internal/gateway/affinity.go:847-850@e66d872`）。aigate 粘性不看任何用量数据。

**S3 会话标识 — 部分支持。** magpie 依次取 agent 原生 session 头、`X-Magpie-Session`、对话开场白哈希（`docs/subsystems/gateway-routing.md:66@e66d872`）。aigate 只认 `X-Moni-Session`，格式非法直接 400（`internal/server/gateway.go:179-192`）；规格明确"不能从内容或 IP 推断"（`specs/033-text-dynamic-routing/spec.md:130`）。这是有意为之：aigate 不记录正文。

**S4 替补归还 — 未支持。** magpie：回落模型、顺序靠后的成员、替补成员答过的会话，在前面的成员恢复后下一回合回去（`docs/subsystems/gateway-routing.md:348-351@e66d872`，`internal/gateway/affinity.go:787-826@e66d872`）。aigate 的粘性命中优先于一切排序（`internal/routing/selection.go:265-268`），只在 `break_on` 条件触发时才放弃。

### 2.5 供应商与账号

**V1 预设目录 — 未支持。** magpie `internal/provider/presets.go:207@e66d872` 起定义 63 个顶层预设（39 vendor、22 relay、2 local，按 `{ID: "` 行计数，不含地区子项）。aigate 渠道只有名称、base URL、密钥、地域、provider 字符串，且 provider 只接受空或 `openai`（`internal/catalog/store.go:140-151`、`internal/catalog/store.go:175-177`）。

**V2 自定义供应商 — 部分支持。** magpie 一个供应商可同时配 Chat/Responses/Anthropic/Gemini 四个 base（`docs/subsystems/providers-accounts.md:20@e66d872`）。aigate 一个渠道版本一个 base URL（`internal/catalog/store.go:140-151`），协议由绑定它的模型 profile 决定。

**V3 订阅账号 — 设计上刻意不做。** magpie 读各 agent 落在本机的凭据（`internal/provider/account.go:46-106@e66d872`，`docs/subsystems/providers-accounts.md:55-57@e66d872`），内置与社区插件分工（`docs/subsystems/provider-plugins.md:1-34@e66d872`）。aigate：表格把"授权订阅账号、OAuth/session 刷新、账号亲和"划给 Sub2API 叶子，且"仅在 direct/New API 无增量能力且条款允许时启用"（`specs/041-profit-hybrid-platform/spec.md:55`）；固定月费的订阅成本在无单请求分摊合同前一律 `cost_status=unknown`，不得进利润敏感池（`specs/041-profit-hybrid-platform/spec.md:60`）；"订阅账号成本分摊"列为后续独立 SDD（`specs/041-profit-hybrid-platform/design.md:207`）。

**V4 配额窗口模型 — 未支持。** magpie 的 `Seat` 携带各窗口续期时间、已用份额、即将作废量、周节奏（`internal/routes/routes.go:22-125@e66d872`）。aigate 与之最接近的是采购账户预算：范围 `tenant|key|procurement`、周期 `utc_day|utc_month`（`internal/periodicbudget/types.go:169`、`internal/periodicbudget/values.go:125`），是金额上限而非供应商配额读数。

**V5 余额查询 — 部分支持。** magpie 并发问每家供应商的订阅窗口、计划额度、key 余额（`docs/subsystems/providers-accounts.md:76@e66d872`）。aigate 只有采购账户的人工余额观察接口（`internal/server/server.go:258-259` (main)），台账写明"供应商余额查询未实现"（`specs/038-new-api-capability-migration/capability-matrix.md:28`）。

**V6 模型发现与目录 — 已支持。** aigate 三种发现格式（`internal/provider/discovery.go:21-23`），公开目录 020，证据 `specs/038-new-api-capability-migration/capability-matrix.md:28`、`docs/architecture/README.md:1361`。

**V7 上游列表价目录 — 未支持。** magpie 用 models.dev 自动补模型的输入能力（`docs/subsystems/gateway-routing.md:2669-2671@e66d872`），并把厂商列表价当作成本代理（`docs/subsystems/gateway-routing.md:492@e66d872`、`docs/subsystems/providers-accounts.md:86@e66d872`）。aigate 的售价与成本都由运营者发布（`docs/architecture/README.md:1365`），并规定"不得引入第二价格事实源"（`specs/038-new-api-capability-migration/capability-matrix.md:30`）。对售价这是有意的；缺的是**成本参考价**的自动来源，运营者登记成本价时没有对照。

**V8 自定义价格 — 部分支持。** magpie：用户自价、供应商价格倍率、分 key 费率、读到的费率乘数、按时段的价格窗口（`docs/subsystems/providers-accounts.md:21-24@e66d872`）、按服务档位计价（`docs/subsystems/gateway-routing.md:302-303@e66d872`）。aigate：不可变价格版本、租户分组售价编译为绝对价格（`docs/architecture/README.md:21`、`docs/architecture/README.md:862`）。缺：分 key 费率、时段窗口、服务档位价。

**V9 — 已支持。** 每渠道精确上游模型的输入/输出成本价、生效版本、每 attempt 快照及 unknown（`docs/architecture/README.md:794-804`、`docs/architecture/README.md:1365`）。

**V10 多 Key 池 — 部分支持。** magpie 逐 key 可设权重、代理、所属计划、消费上限（`internal/provider/provider.go:224-256@e66d872`）。aigate 池成员只有权重、并发上限、标签（`internal/catalog/credential_pool.go:147-205`）；逐凭据的采购账户绑定有（019），逐凭据代理/费率/消费上限无。

**V11 provider 插件 — 设计上刻意不做。** aigate："插件执行环境（进程内脚本、WASM、外部进程）本阶段不做"（`specs/032-phase2-text-data-plane/spec.md:138`）；非目标列"任意插件/脚本/MCP 执行环境"（`specs/038-new-api-capability-migration/spec.md:201`）；"不实现递归策略 DSL 或任意脚本插件"（`docs/design/LLD.md:401`）。

**V12 — 未支持。** magpie 内置 Vertex AI、Bedrock Mantle 等签名与路由（`docs/subsystems/gateway-routing.md:70-71@e66d872`）。aigate 台账把 Azure/Bedrock/Vertex 列为"独立切片"（`specs/038-new-api-capability-migration/capability-matrix.md:21`）。

**V13 外部网关作为上游 — 部分支持。** magpie 把另一台 magpie 当 provider（`docs/subsystems/gateway-routing.md:775-1186@e66d872`）。aigate 把 New API/Sub2API 当 `external_relay` 渠道，走同一 OpenAI-compatible relay（`internal/catalog/store.go:153-169`，`docs/architecture/README.md:33`、`docs/architecture/README.md:96`）。缺的是 hop 专有机制，见 O4。

### 2.6 用量 / 成本 / 预算

**U1 — 已支持。** magpie 的 gateway key 预算：窗口内用量从 JSONL 汇总，在途请求按"请求体字节/4 + 本窗口平均输出"预占，`used + reserved < cap` 才放行，流结束按供应商报数结算（`internal/budget/budget.go:1-49@e66d872`、`internal/budget/budget.go:620-730@e66d872`）；预算点无人服务时 fail closed（`internal/gwbudget/gwbudget.go:106-131@e66d872`）。aigate：Reserve 在数据库事务内冻结 R 并预占钱包，T2 唯一结算（`docs/architecture/README.md:662-674`）；Key 累计额度 `spent+held+新预占 ≤ limit`（`specs/017-key-configuration/spec.md:5,8`）。aigate 更强：预占是硬的、持久的、按租户隔离的。

**U2 多窗口限额 — 部分支持。** magpie 一个 key 最多 5 个限额各自窗口，窗口可跟随某订阅账号的重置（`docs/subsystems/gateway-routing.md:929-935@e66d872`）。aigate 只有 UTC 日/月，不跟随任何账号窗口。

**U3 Learns — 未支持。** magpie 对用量端点读不到的账号，从应答头学习剩余量（Claude 的 `rate_limit_event`、Codex 的 `x-codex-*`），并让未知账号先答一次（`internal/routes/routes.go:30-33@e66d872`，`docs/subsystems/gateway-routing.md:334@e66d872`）。aigate 没有配额概念，应答里也没有可学的量。

**U4 Spent 预滤 — 部分支持。** magpie 在预留之前把某限额已耗尽的模型移出候选，组内直接跳到剩下的成员（`docs/subsystems/gateway-routing.md:945-960@e66d872`，`internal/gwbudget/gwbudget.go:40-42@e66d872`）。aigate：候选阶段有采购预算排除（`internal/routing/selection.go:103-106`）；客户侧周期预算是 Reserve 同事务的准入判断（`docs/architecture/README.md:789`），不做候选预滤，也不支持"某限额只计某些模型"。

**U5 OTLP — 未支持。** magpie 默认不出机器，显式开启后按批导出 traces/metrics（`internal/otlp/otel.go:24-35,80@e66d872`，`internal/settings/otel.go:15-29@e66d872`）。aigate："首版不做 trace 采样或外部导出"（`docs/design/LLD.md:463`）；032 把"观测导出"列为允许的只读扩展点但未实现（`specs/032-phase2-text-data-plane/spec.md:135`）。现有只有 `/metrics`（`internal/server/server.go:121` (main)）。

**U6 — 已支持。** magpie 用量汇总与请求日志 API（`docs/subsystems/gateway-routing.md:1617-1629@e66d872`）。aigate 026 经营报表与 CSV（`internal/server/server.go:246` (main)，`docs/architecture/README.md:1366`）。

**U7 币种与汇率 — 部分支持。** magpie 记录以 USD 存，只在显示时换成 CNY，汇率来自免费接口、12 小时 TTL、离线用 7.2 兜底（`internal/fx/fx.go:1-8,24,36,43@e66d872`）。aigate：COIN/CNY 分型，041-P0 增加不可变 `FxPolicy`，只支持 USD↔CNY 有理数报价、不推断倒数、带费用缓冲与有效期（`internal/economics/fx.go:21-55`）。自动汇率与通用多币种是 041 非目标（`specs/041-profit-hybrid-platform/spec.md:34`）。

**U8 缓存计量 — 部分支持。** magpie 每条记录带 `cache_read`、`cache_write`、`cache_write_1h`（`docs/subsystems/gateway-routing.md:310@e66d872`）。aigate 解析 Anthropic 的 `cache_creation_input_tokens` 与 `cache_read_input_tokens`（`internal/provider/native_usage.go:79`），`Usage` 只有 `CachedTokens`（`internal/provider/types.go:180-187`）；台账写明缓存分维折扣未完成（`specs/038-new-api-capability-migration/capability-matrix.md:30`）。

**U9 — 未支持。** magpie 记录 `tier_asked/sent/served` 并按档位计价（`docs/subsystems/gateway-routing.md:302-303@e66d872`）。

### 2.7 中间件与 hook

**M1 JS 网关中间件 — 设计上刻意不做。** magpie 用 Go 写的 JS 引擎在进程内跑中间件，五个 hook，限额 250ms/50ms/1s（`docs/subsystems/gateway-middleware.md:24-34@e66d872`，`internal/middleware/middleware.go:37-39@e66d872`，接口 `internal/gwmw/gwmw.go:62-91@e66d872`）。aigate 排除依据同 V11。

**M2 / M4 / M5 / M6 — 未支持。** 这四个是 magpie 社区中间件包，实现在外部仓库；本次只读到市场条目与文档摘要：`param-override`（设置/删除/移动字段）、`system-prompt`、`word-guard`、`think-tags`（`internal/plugin/market.json@e66d872`，`docs/reference.md:1661-1673@e66d872`）。aigate 无请求改写层。

**M3 `model-map` — 部分支持。** aigate 有模型别名（客户可见名 → 逻辑模型，`internal/catalog/model_alias.go:25-27`、`internal/catalog/model_alias.go:71-76`）与 binding 的上游模型映射（`internal/server/gateway.go:128`、`internal/server/gateway.go:146-150`），响应回显请求名。缺：按请求正则改写、渠道级映射。

**M7 异步 request hooks — 设计上刻意不做。** magpie 的 Bun 钩子在路由前加上下文、在完成后观察回复（`docs/subsystems/gateway-request-hooks.md:36-42@e66d872`、`docs/subsystems/gateway-request-hooks.md:112-130@e66d872`）。

**M8 中间件提供路由策略 — 设计上刻意不做。** magpie 中间件可注册 `<plugin>/<name>` 路由策略并接收用量行（`docs/subsystems/gateway-middleware.md:36-43@e66d872`）。aigate："路由选择与路由决策记录……不开放任何扩展点"（`specs/032-phase2-text-data-plane/spec.md:122-129`）。

**M9 fail open / fail closed 分点 — 部分支持。** magpie 明确两个点两种策略：agent 中间件 fail open，遮蔽器 fail closed（`internal/gwmw/gwmw.go:1-17@e66d872`、`internal/gwmw/gwmw.go:304-312@e66d872`）。aigate 有同样的原则——资金/授权 fail closed，日志指标有界丢弃（`docs/design/LLD.md:415`、`docs/design/LLD.md:463-465`）——但没有任何可挂接的扩展点，原则只存在于内置代码里。

**M10 请求脱敏 — 未支持。** magpie 的规则层 + 本地模型层，开启后遮蔽器缺失即 503（`docs/subsystems/gateway-routing.md:249-257@e66d872`）。aigate 无。

### 2.8 网关内工具

**T1 / T2 — 设计上刻意不做。** magpie 聚合 10 家搜索 API（`internal/provider/search_api.go:92-110@e66d872`），多 key 轮换且各自休息（`internal/gateway/search_keys.go:50-76,118-138@e66d872`），另有 `web_fetch`（`internal/gateway/search_fetch.go:48-66,126@e66d872`）。aigate："网关不执行函数、不抓取 URL、不自动开展下一轮生成……托管 web search/code execution/file search……不透传"（`docs/design/LLD.md:83`）；"网关不执行函数、不抓取 URL、不托管工具"（`specs/032-phase2-text-data-plane/spec.md:142`）。

**T3–T8 — 不适用。** 图像/识图 MCP server（`internal/gateway/image_mcp.go:47@e66d872`、`internal/gateway/vision_mcp.go:28@e66d872`）、MCP router（`docs/subsystems/mcp-router.md:3@e66d872`）、fast tools（`docs/subsystems/fast-tools.md:11-32@e66d872`）、memory（`internal/gateway/memory_mcp.go:39@e66d872`）、skill router（`docs/subsystems/skill-router.md:3@e66d872`）都是把**本机用户的** MCP 服务器、技能、文件系统暴露给**本机 agent**。多租户服务端网关没有"本机"可暴露；MCP router 的 `run_code` 还会在本机执行脚本（`docs/subsystems/mcp-router.md:16@e66d872`），与 038 §10 的排除直接冲突。T8（Codex 标题/摘要/子代理、Claude Desktop 探测等专用处理，`docs/subsystems/gateway-routing.md:109-244@e66d872`）是针对特定桌面客户端的本机配置，同属此类。

### 2.9 运维

**O1 — 已支持。** magpie 共享后监听 `0.0.0.0` 并要求远程调用者持命名 gateway key（`internal/gateway/lan.go:557,894@e66d872`，`internal/access/access.go:131-228@e66d872`）。aigate 全部 `/v1` 强制 API Key（`internal/server/server.go:269-270` (main)），Key 归属租户/成员并带来源、限额、额度、期限（015/016/017）。

**O2 caller key 的 models 限制 — 部分支持。** magpie：精确 id、`provider/*`、`re:` 正则（`internal/access/models.go:102,165@e66d872`，`docs/subsystems/gateway-routing.md:1469@e66d872`）。aigate：scopes 为 `["*"]` 或 1..1000 个模型 UUID（`specs/017-key-configuration/spec.md:5`）。缺：模式匹配、按渠道限定、key 级别名。

**O3 — 未支持。** magpie 有 1 分钟到 30 天的短期 key、OIDC 令牌换 key、只读 key、管理 key（`docs/subsystems/gateway-routing.md:1479-1608@e66d872`）。aigate 的第三方身份与账号绑定列为 `postponed`（`specs/038-new-api-capability-migration/capability-matrix.md:48`）。

**O4 远程级联 — 设计上刻意不做。** magpie 支持两台互为上游、只传增量、字典压缩、遮蔽与路由每跳只执行一次（`docs/subsystems/gateway-routing.md:3473-3609@e66d872`）。aigate：`max_relay_depth` direct 为 0、external 为 1，并用"发布配置 read-back、部署网络、出口 allowlist 和漂移检查共同拒绝 aigate/New API/Sub2API 互连成链"（`specs/041-profit-hybrid-platform/spec.md:134-136`）。理由是逐跳成本归因与证据合同不存在。

**O5 mTLS — 不适用。** magpie 自建 CA 给设备发客户端证书，用于把个人网关安全暴露到公网（`internal/mtls/mtls.go:1-16@e66d872`）。aigate 是公网商业入口，客户端用 API Key，TLS 终止在反代，应用只监听本机（`docs/architecture/README.md:1270-1293`）。若将来需要上游 mTLS，属供应商资格范围。

**O6 Docker — 已支持。** aigate Dockerfile + Compose；029 第一阶段 41/41 已完成（`docs/architecture/README.md:25`），Compose 覆盖迁移、provision、各协议入口与 app/worker/PostgreSQL 重启（`docs/architecture/README.md:43`、`docs/architecture/README.md:1293-1296`）。正式容器/生产部署仍"部分未验收"（`docs/architecture/README.md:1378`）。

**O7 代理 / 出口 — 部分支持。** magpie 代理选择顺序：设置 → `*_PROXY` → 系统代理 → 登录 shell，且可按供应商/账号/key 覆盖（`internal/netproxy/netproxy.go:1-12@e66d872`）。aigate：传输层 `Proxy: nil`、无 keep-alive、仅 HTTP/1.1（`internal/provider/network.go:177`），每次拨号重新校验解析出的 IP（`internal/provider/network.go:135-176`、`internal/provider/network.go:107-133`）；"环境代理不隐式继承"（`docs/design/LLD.md:523`）。出口限制有，代理出口无。

关于"无 keep-alive"的定性：这是**已识别的有意取舍，不是遗漏**。关闭连接复用是为了让每次请求都重新做 DNS 解析校验与出口策略判定（防 DNS 重绑定）。仓库已把连接复用列为待立项的性能项，并写明"按目标、TLS 和网络策略作用域隔离池……仅将 `DisableKeepAlives` 改成 `false` 不足以完成这项优化"（`docs/verification/performance-audit.md:64`）；性能基线工具也把它做成了可配置项以便对照测量（`scripts/phase2-baseline/types.go:80`）。magpie 在这一点上没有可借鉴的隔离设计：它直接使用默认传输层。

**O8 入口防护 — 已支持。** magpie 靠 `lanGuard`/`corsGuard`/防重绑定（`docs/subsystems/gateway-routing.md:53@e66d872`）。aigate 推理入口没有无 key 路径，只有可信代理 CIDR 的单一 `X-Real-IP` 被接受（`internal/server/server.go:106-107` (main)，`docs/architecture/README.md:1260`）。

**O9 管理 API — 已支持。** magpie `/v1/magpie/admin/`（`docs/subsystems/gateway-routing.md:1631-1659@e66d872`）。aigate `/api/platform/*`（`internal/server/server.go:171-268` (main)），带角色、CSRF、再认证。

**O10 路由 trace — 部分支持。** magpie 有完整 trace（仅本机）与给外部显示屏的 SSE 事件流（`docs/subsystems/gateway-routing.md:1661-1734@e66d872`）。aigate 每次选择一行不可变 `route_decisions`（`internal/routing/dynamic.go:302-319`），可按请求读取（`internal/server/server.go:268` (main)）并只读回放（`internal/routing/replay.go:18-83`，`cmd/moni-aigate/route_replay.go:16`）；没有实时事件流。

**O11 — 未支持。** magpie 升级时把监听 socket 交给新进程，旧进程最多排空 15 分钟（`docs/subsystems/gateway-routing.md:496-516@e66d872`）。aigate 单活由 advisory lock 保证，新实例等待前驱未结束请求，HA 后置（`docs/architecture/README.md:1297`）。

### 2.10 反向对照：aigate 有而 magpie 没有的

为避免把对照读成"aigate 落后"，列出 magpie 完全没有对应物的部分：租户隔离与三角色数据库分离；钱包硬预占、唯一结算、funding lot；候选级与请求级累计成本的毛利准入门（`internal/economics/admission.go:39-101`）；不可变发布版本与 CAS 激活/回退；发布证据门禁；质量证据与质量政策；完整候选失败池落库与确定性回放；租户/Key/采购三级周期预算。这些正是 §3 各借鉴点必须绕开的约束。

## 3. 值得借鉴的设计 Top 10

成本口径：**低** = 单个小切片，纯内存逻辑或不改资金的合同补充；**中** = 需要 SDD + 可能的迁移/新增计量；**高** = 触碰资金或不变量。

| # | 设计 | 为什么好 | 落在哪 | 成本 |
|---|---|---|---|---|
| 1 | **归因先于休息**（`forbiddenAlike`） | 一个被上游固定拒绝的请求不该把整池账号打进熔断；magpie 用"同请求、同供应商同模型、两个账号、同样的拒绝，且供应商刚回答过别人"推断是请求的错并撤回休息（`internal/gateway/routing.go:114-221@e66d872`） | 032 熔断的故障归因。aigate 裸 5xx 仍计故障，是现存缺口 | 中：aigate 只认机器码（`internal/provider/classification.go:8-9`），不能比词；须改为比"失败类 + 上游状态码"，并加"该渠道近 5 分钟有成功"的条件 |
| 2 | **分类型 rest + 爬梯 + 双作用域 + 持久** | 休息时长按失败性质分表，欠费逐次升级，限流只歇被限的那个 key；重启不丢（`internal/gateway/routing.go:239-299@e66d872`） | 032。aigate 现在是 429 冷却按 `channel:model`（`internal/server/gateway.go:159`）、配额类直接持久撤销凭据（`internal/server/gateway.go:808-831`） | 中：把 429 冷却作用域下沉到凭据属"低"；分类型时长与爬梯需扩展健康状态；跨重启持久需新表或列 |
| 3 | **overloaded 单列** | 供应商忙不是账号的错；固定短休、不计爬梯，避免"八个账号各歇十分钟"（`internal/gateway/routing.go:258-259,2057@e66d872`） | 032 `FailureClass` 增一类，只影响健康动作，不影响重试资格 | 低 |
| 4 | **粘性量化判据** | "缓存读+写 ≥ 1024 且未冷才保持"给了粘性一个可计算的理由；写也算读、厂商冷却各按实测（`internal/gateway/affinity.go:49-102@e66d872`、`internal/gateway/affinity.go:847-855@e66d872`） | 033 粘性。aigate 已记录 `CachedTokens`（`internal/provider/types.go:185`），粘性策略现只有 `max_age_s` | 中：需把 usage 反馈进粘性判定并落库；冷却时长必须用 aigate 自己的供应商实测，不能抄 magpie 的数 |
| 5 | **回合级决策保持 + 替补归还** | 决策在回合开始时定一次、工具循环期间不改（`internal/gateway/rules.go:224-447@e66d872`）；替补答过的会话在前面的成员恢复后回去（`docs/subsystems/gateway-routing.md:348-351@e66d872`） | 033 粘性：补"回合内保持"语义（工具循环不因 `break_on` 轻易搬家）与"替补归还"（主路径恢复后新回合回去） | 中：aigate 无"回合"概念，需先用请求形状（是否带工具结果）定义，且不读正文 |
| 6 | **未测量候选的探索** | 按延迟排序时未测者永远排不到就永远测不到；每 5 个请求拿 1 个给未测量者（`internal/routes/fastest.go:43,112-123@e66d872`） | 033 统计排序。aigate 样本不足直接跳过（`internal/routing/selection.go:471-477`） | 低到中：实现简单，但 041 规定付费流量探索默认关闭（`specs/041-profit-hybrid-platform/spec.md:126`），必须做成显式开关且不越过毛利/质量硬过滤 |
| 7 | **策略点只排序 + 排列校验 fail open** | 策略接口只有一个职责，返回非法排列就回退列表顺序，任何策略错误都不会让路由失败（`internal/routes/routes.go:203-229@e66d872`） | 041/033 的排序器接缝。aigate 的 `sortPreference` 是写死的三种 | 低：纯接口形状，不引入插件运行时 |
| 8 | **并发突发分散** | 同时到达的请求都按同一份用量数据排序，会全压到同一个账号；按在途数做平局裁决即可（`internal/gateway/burst.go:32@e66d872`） | 033/035 凭据池内选择 | 低：在凭据抽签前对"同权且都空闲"的候选按在途数排序 |
| 9 | **"后面还有没有候选"信号** | 告诉上游"你不是最后一个"，让它快速失败而不是排队几分钟（`docs/subsystems/gateway-routing.md:387@e66d872`） | 041 `ExecutionAdapter` / 035 外部执行器 | 低：一个内部 header 或 adapter 参数；须确保不泄到供应商 |
| 10 | **全池冷却时合成 Retry-After** | 所有候选都在休息时，不逐个去问，直接回第一个的拒绝并带上"最早多久后恢复"（`docs/subsystems/gateway-routing.md:337@e66d872`） | 032 对外错误合同。aigate 现在统一回 503 `no_available_channel`（`internal/server/gateway.go:358-359`） | 低：只是错误响应里多一个字段；注意 aigate 现有 `retry_after_cap_ms=2000`（`docs/design/LLD.md:401`）是对内重试的上限，不是对外的 |

次选（未进前十）：慢启动作为独立健康信号（→032，低）；回复循环与工具循环守卫（→041 质量，中，但会改变已消费内容的计费口径，需先冻结合同）；退避状态落盘（已并入第 2 条）。

## 4. aigate 应保持不做的

| 能力 | magpie 形态 | 不做的理由与出处 |
|---|---|---|
| 订阅账号代理、驱动本机官方客户端 | Claude 桥、Codex app-server、各订阅插件 | 服务端无法也不应驱动用户本机二进制；转售订阅有条款风险。aigate 把它划给 Sub2API 叶子且条件启用（`specs/041-profit-hybrid-platform/spec.md:55,60`） |
| 在线 LLM 意图分类器读客户正文 | 每回合问分类模型 | 隐私与成本：aigate 不记录正文，也不为路由再付一次生成费（`specs/041-profit-hybrid-platform/spec.md:31,100`） |
| "未发回复即可换手"的宽松切换、同候选原位重试、patience 等待 | 401/403/429/5xx 即换；忙类重试 2 次；全组忙时等 240 秒 | 可能已消费的请求重发等于替客户多付一次钱。aigate 只认无消费证据（`specs/032-phase2-text-data-plane/spec.md:31-36`；`specs/038-new-api-capability-migration/spec.md:29-30`） |
| 提交前给客户端发心跳/保活 | 3 秒后每 5 秒一条 SSE 注释 | 一旦写出字节就算提交，之后不能再换手；aigate 选择"首事件通过前什么都不写"以保留切换权（`docs/design/LLD.md:445`） |
| JS/Bun 插件运行时与社区中间件市场 | 进程内 JS 引擎 + Bun 宿主 + npm 分发 | 路由、计量、结算、恢复、审计不可插件化；插件执行环境需另立 SDD（`specs/032-phase2-text-data-plane/spec.md:120-138`；`specs/038-new-api-capability-migration/spec.md:201`） |
| 网关内 web search、`web_fetch`、MCP 托管 | 搜索聚合、抓取、MCP hub、图像 MCP | 工具调用的计量计费、多轮预占、租户隔离、出口安全都没有合同（`docs/design/LLD.md:83`；`specs/032-phase2-text-data-plane/spec.md:142`） |
| 改写本机 agent 配置、Library、skill router、fast tools、同步备份 | 各 agent 的本机配置接线与本机 MCP/技能库 | 本机工具形态，与服务端网关无关 |
| 导入下游客户、quota、钱包、订单 | — | 下游商业事实不是 aigate 的事实源（`specs/038-new-api-capability-migration/spec.md:200`） |
| 执行器多跳级联 | 远程 magpie 套远程 magpie | 无逐跳成本归因与证据合同；`max_relay_depth` 封顶为 1（`specs/041-profit-hybrid-platform/spec.md:134-136`） |
| 自动汇率、通用多币种、订阅成本分摊 | 免费汇率接口 | 估值用 FX 必须是不可变版本、保守取整、失败关闭（`specs/041-profit-hybrid-platform/spec.md:34`；`specs/041-profit-hybrid-platform/design.md:205-209`） |
| 运行时"学习式"请求改写 | 记住某供应商拒过哪个字段/beta，下次自动去掉再发 | 对已发请求的任何再发都是第二次生成；aigate 把这类差异冻结进 profile 与资格证据，而不是运行时试错（`docs/architecture/README.md:623,637`） |
| "把配额用干净"的排序目标 | smart/usage/pace/drain/expiring | 目标函数与毛利/质量约束方向相反；aigate 只有采购预算这一处有"将作废"语义 |
| 签到、邀请奖励、分佣 | — | 明确 `out_of_scope`（`specs/038-new-api-capability-migration/capability-matrix.md:49`） |

## 5. 不确定点

1. **未运行 magpie**。全部结论来自读源码与文档。厂商词表覆盖面、各家冷却时长等只在文档与注释里见到，未验证其测试是否仍然成立。
2. **社区中间件与 provider 插件宿主在外部仓库**（`magpie-community/plugins`）。M2–M6 与 V11 只读到 `internal/plugin/market.json@e66d872` 的摘要和 `docs/subsystems/provider-plugins.md@e66d872`，未读其实现；`internal/plugin/host.js@e66d872` 未读。
3. **aigate 行号基线**。worktree 基于 `41f66e4`，远端 main 为 `9fd8dd6`。`internal/server/server.go`、`media_handlers.go`、`task_handlers.go` 按 main 行号；`docs/architecture/README.md` 在 main 上有 4 行差异，本文行号按 worktree。
4. **"已支持"的证据等级**。aigate 的 032/033/018/012 等都是本地 mock + 029 验收，真实供应商资格与生产资格均未授予（`specs/038-new-api-capability-migration/capability-matrix.md:3`）。P2/P4/P5/P8/P9 的"已支持"指协议与计量在 mock 上通过。
5. **渠道级 RPM**。LLD 写"RPM 与并发按平台/租户/Key/渠道分层"（`docs/design/LLD.md:417`），但在 `internal/routing` 与 `internal/catalog` 中未找到渠道级 RPM 实现，只找到 Key/平台/租户桶与渠道并发容量。R18 判"部分支持"据此，标为待核实。
6. **F10 的"空回复"子项**。未逐行核实 aigate 对"200、`stop`、但没有任何内容"的流如何结算；判定"未支持"基于未发现循环/空回复检测逻辑。协议完成条件见 `docs/design/LLD.md:443`。
7. **"smart"一词三义**：magpie 默认路由 `smart` = Quota first；magpie 模板 `smart` = 带意图规则的组（`docs/subsystems/gateway-routing.md:94@e66d872`）；aigate `Scoring.Auto` = 跨质量档位 auto。R1 与 R21 分开判定即为此。
8. **预设数量**。63 由 `internal/provider/presets.go@e66d872` 中顶层 `{ID: "` 行计数得出，不含地区子项与 `partners.go`。
9. **路由模式的数量口径**。内置策略为 11 个（`internal/routes/strategies.go:685-697@e66d872`），插件可再注册策略，合计按 12 个计。协议互转（P1）一项只引用了文档行，`internal/gateway/ir.go` 的行号未在本稿复核。
10. **`docs/subsystems/gateway-sessions.md` 只读了开头 60 行与"Usage over the gateway"一节**，`providers-accounts.md` 只读了约 12 行（每行数千字），`provider-plugins.md` 只读了前 34 行。订阅账号细节以已读部分为准。

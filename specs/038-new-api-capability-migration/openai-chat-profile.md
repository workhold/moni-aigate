# 038-PR3：deterministic OpenAI-compatible Chat profile 内收

2026-11-17；状态：PR-3 实施合同。本片实现[038 总合同](spec.md) §4 的首个端到端候选：同一 OpenAI-compatible Chat profile 在 external relay 与 embedded direct channel 间切换/回退，并补齐工具终态、可靠 usage、控制台历史和安全错误边界。所有新增证据均为本地 deterministic/mock 范围，不授予真实供应商、商业发布或生产资格。

## 1. 唯一 profile 与实现边界

本片只验证以下测试 profile，不把 `OpenAI-compatible` 扩展成任意供应商兼容承诺：

| 项 | 固定值 |
| --- | --- |
| profile 标识 | `deterministic-openai-chat-v1` |
| 协议 / variant | `openai_chat` / `default` |
| 公开逻辑模型 | `retail-model`（integration fixture 内的公开名称） |
| external 拓扑 | `new_api` / `external_relay`，上游模型 `relay-newapi-model` |
| embedded 拓扑 | `direct` / `embedded_adapter`，上游模型 `provider-model` |
| 输入/输出硬界 | 使用已发布 `model_capability_versions`；测试 profile 为 input 100、context 120、output/default 20 Tokens |
| 工具 | 客户端 function tools、`tool_choice`、assistant/tool 消息；支持 `parallel_tool_calls` 布尔字段和多个按 index 分片的调用 |
| 发布意图 | 全部 `test_only`；不创建 commercial binding 或 supplier-profile 证据 |
| 来源 | `reference_only`；New API 固定为 `e449cc700eb569cdfc2199cb73e97de46944f43c`，不复制或链接其实现 |

实现复用 `provider.ParseChat`、`Request.UpstreamJSON`、`provider.Client`、`Response.DecodeJSON/ReadEvent`、`catalog.Store`、`routing.Plan` 和现有 `Profile → Plan → Reserve → Attempt → Observe → Settle`。不得增加第二个 gateway、adapter 执行核心、钱包、计量、结算、采购成本、路由事实表或 SQL 迁移。

## 2. 请求与响应合同

### 2.1 请求

沿用现有严格 JSON 校验：UTF-8、无重复键、最多 1 MiB、`n=1`、一个公开模型、`max_tokens`/`max_completion_tokens` 二选一且受 output limit 约束。允许既有文本采样字段和 `stop`；未知字段、媒体 content、托管工具、远程文件和任意供应商扩展在发送前拒绝。

- 工具仅允许 `type=function`；函数名、schema、消息和 tool 结果使用现有限度。网关不执行函数、不抓取 URL、不自动发起下一轮。
- 本 profile 声明支持并行工具，因此 `parallel_tool_calls` 可显式为 true/false 并原样进入冻结请求；本 profile 不把它改写成其他供应商的私有字段。其他 profile 仍由各自 capability 决定，不能继承本声明。
- external/embedded 仅替换不可变 binding 中的上游模型；公开模型、协议、输入界、输出界、价格和计量 schema 不变。
- SSE 上游始终接收 `stream_options.include_usage=true`。客户端省略或 false 只抑制下游 usage-only 事件，不关闭内部观察；true 时在正常 `[DONE]` 前最多发送一个规范化空 choices usage 事件。

### 2.2 JSON 与 SSE 输出

下游继续由 aigate 规范化，不原样透传未知上游字段：

1. 公开 `model` 始终为逻辑模型；上游返回模型不匹配不改变路由、价格或公开模型，也不在本片新增正文诊断。
2. JSON 必须有且仅有一个 index=0 choice、assistant message、合法终态和可信最终 usage。工具调用完整保留 `id/type/function.name/function.arguments`。
3. SSE 每个 tool delta 保留 `index` 及实际出现的 `id/type/function.name/function.arguments`；参数分片不得丢弃、重排、去重或按函数名合并。允许多个 index，不要求每个分片重复 ID、类型或函数名。
4. `finish_reason` 仅接受 `stop`、`length`、`tool_calls`、`content_filter`。四者都是一次生成终态；已确认 usage 按原请求唯一结算，不因其一非 stop 而重发。
5. `[DONE]` 必须作为完整 data 事件出现；缺 finish、缺 DONE、EOF、无效 JSON、冲突 finish 或 in-band error 都不是成功结束。多行 data、CR/LF/CRLF 和字节分片沿用现有 decoder。
6. usage 只接受 profile 定义的完整最终快照。重复相同快照不累加；缺失、冲突、未知非零计费维度或非法关系进入既有 missing/conflicting/unknown 路径，不估算、不以最大值拼接、不伪造零。

## 3. 与 New API 的差异矩阵

固定参考只用于行为对照；下表中的安全分歧不得以“对齐上游”为由删除：

| 场景 | 可观察对齐 | aigate 固定分歧/边界 |
| --- | --- | --- |
| JSON/SSE 文本、工具和公开模型 | 保留内容、工具增量、finish 和请求模型映射 | 下游使用公开模型；不让上游 model 改变计费或路由 |
| `stream_options` | true 时给客户端最终 usage | 无论客户端取值均向上游申请；不支持的供应商另建 profile |
| 工具分片 | 按 index 保序转发，客户端可重组参数 | 不采用按次工具收费、函数名去重或 +7 Token 估算 |
| 缺失/零/冲突 usage | 显式零与缺失不同 | 不估算缺失量，不用非零 overlay 消除冲突；走既有核对/释放门禁 |
| SSE 异常 | 已写内容可以保留 | in-band error、缺 finish/DONE、截断不得再伪装成功 DONE；消费未知不重放 |
| 429/5xx/取消 | 使用安全错误 envelope | 只有 profile 明确证明 `rejected_no_consumption` 且 `Plan.CanRetry` 允许时才可第二 attempt；状态码本身不够 |
| length/tool_calls/content_filter | 保留实际终态 | 可信 usage 唯一结算；不自动续写、执行工具或重试生成 |
| 后台恢复 | 只读观察原 request/attempt | 不重新发送模型生成请求，不制造第二 settlement |

## 4. deterministic fixture 与统一商业链

扩展 `internal/testsupport/mockupstream` 的显式 `chat_tool_calls` fixture，仅影响 Chat：

- JSON：两个完整 function calls，content=null，`finish_reason=tool_calls`，最终 usage。
- SSE：两个 index 的 ID/name/type/arguments 分片，随后 tool_calls 终态、最终 usage 和 DONE；参数分别重组为 `{"city":"Paris"}` 与 `{"zone":"UTC"}`。
- fixture 仍由既有 mock 鉴权、receipt、输入/输出量和 fault 白名单控制；不得把该 fault 变成其他协议的静默成功，也不得向生产上游转发 `[[mock:...]]` 控制语义。

同一成功 fixture 分别经 external 与 embedded 运行，验证：上游映射和授权正确；公开模型不变；工具增量、终态和 usage 完整；一个 request 只有一个 settlement；每个 attempt 有冻结的渠道版本、模型、价格和成本快照；其他 channel 没有收到生成请求。

故障验收继续使用既有 fixture/测试并至少覆盖：无消费 429 的最多两次 attempt、未列出 429/500/截断/缺或冲突 usage/in-band error/取消的 no-replay，以及最终账务状态。任何新增局部测试不能替代完整 029。

## 5. 只读 route revision 投影与 HTTP

新增 admin-only：

`GET /api/platform/models/{id}/route-revisions[?cursor=...]`

- 复用现有 50 条 `(created_at,id)` keyset 分页；模型不存在返回空页，不能从响应推断其他租户数据。
- 每行只含 `id/revision/created_at/profile_version/release_intent/active/current_profile/bindings`。
- binding 只含 `channel_id/channel_name/channel_kind/integration_mode/channel_version_id/upstream_model/priority/weight/max_inflight`。不得返回 base URL、CIDR、credential/secret、release evidence ID、成本费率、请求/响应正文或原始错误。
- `active` 来自读取时的活动指针；`current_profile` 只表示历史 route 与模型当前 profile 是否一致。它们不是激活结果、健康、供应商资格或 live-credential 保证。
- 历史按 revision/创建事实展示；合法回退可使 active revision 数值下降，前端不得使用单调递增假设。

现有 `POST /api/platform/models/{id}/route-revisions/{revision}/activate` 输入和事务语义不变，仍只接受 `expected_revision`：同模型/profile、已发布目标、当前 live channel/credential 和 PR-2 commercial 重核均在服务端完成。激活目标是已存在 revision，不新建 route、不改历史、不影响在途请求；`target==current` 是现有幂等空操作。

回退后再次发布不得复用历史号。模型行锁内的发布 CAS 仍比较 `expected_revision=active_revision`，但新不可变 revision 必须取该模型历史 `MAX(revision)+1`，而不是 `active_revision+1`；激活与发布共用同一模型锁，数据库唯一约束继续兜底。前端发布回执只接受大于提交时 active 的新 revision，并在成功后重新读取，不再假定数值恰为 expected+1。必须覆盖 publish → rollback → publish，证明第三次发布获得未使用的新号且旧 route 不变。

## 6. 控制台合同

### 6.1 Playground

- Chat JSON/SSE 按 tool index 重组并展示调用 ID、函数名和参数字符串；分片只允许追加 arguments，ID/type/name 出现冲突时显示协议错误，不静默覆盖。工具展示不是执行结果，页面不得调用工具或自动续写。
- 展示实际 `finish_reason=stop|length|tool_calls|content_filter`。`[DONE]`/完整 JSON 只表示浏览器接收完成；状态文案不得把本地完成等同已结算。
- 保留 request ID、可信 usage 和“最终收费以调用记录为准”入口；缺 usage、错误、取消和模型切换不能留下上一响应的工具/终态。
- 390px 下长 ID、函数名和 JSON 参数可换行，无整页横向溢出。

### 6.2 历史、切换与回退

模型目录提供“历史”入口，打开独立只读历史/回退对话框：显示当前 active、revision、profile 匹配、test-only/commercial、渠道名称、拓扑、精确上游模型和创建时刻。管理员选择非活动目标后，经明确确认只发送一次 activation POST。

- 每次激活使用对话框最新只读 active 作为 expected revision；成功后校验返回 revision 恰为目标，再刷新历史和父列表。
- 409、权限/recovery/reauth、校验失败和未知网络/响应分别可见。冲突或未知结果锁定原提交，只允许只读刷新核查；不得自动重复 POST。观察到 active==target 只能表述为“当前已指向目标”，不能反推原命令一定成功。
- 关闭、切换模型或身份后，晚到响应不得写入新上下文；历史读取失败不能显示为空。合法回退到较小 revision 必须可用。

## 7. 权限、故障与恢复边界

1. route history 读取和 activation 仅平台 admin；activation 继续经过 session、MFA、CSRF、Origin、近期再认证和 recovery-mode 写门禁。客户 API、公开目录和其他平台角色没有该读面。
2. 新读面不执行供应商 I/O，不写审计或数据库；activation 仍只在实际状态变化时沿用现有 `route.activate` 审计。
3. 激活结果未知时不重发；恢复 worker 只处理原 attempt，不重新生成。
4. 所有 mock 路由保持 test-only。本片不能登记 `supplier_profile_qualified`，不能把 `embedded_adapter` 自动写成 `migrated_capability`，也不能退役 New API/Sub2API。

## 8. 验收与证据

1. Go 单元/集成：Chat JSON/SSE 双工具、分片保序、公开模型、usage 可见性、四类终态、错误/取消/未知消费 no-replay、唯一结算和 route CAS/回退。
2. 专库真实 PostgreSQL：external 与 embedded 各运行相同 fixture；验证 request/attempt/reservation/settlement、冻结 revision 和账务平衡。显式 `TEST_DATABASE_URL`；必需 SKIP 不算通过。
3. HTTP：history admin 权限、分页/严格 DTO、秘密 canary、activation CSRF/reauth/recovery、stale CAS、较小 revision 回退和无供应商 I/O。
4. 前端：parser/assembler、工具冲突、状态清理、activation 一次提交/未知核查/非单调 revision，strict TypeScript 构建。
5. 真实 main 与隔离浏览器：桌面和 390px 实际完成 Playground 工具终态展示、历史读取和明确回退，并核查最终调用/账务状态；无秘密截图。
6. 共享协议、路由与控制台候选运行相关 race、`make check`、专用 PostgreSQL `make verify-mvp` 和完整 029；最终候选提交执行架构 reviewed refresh/check。CI 全绿后交付 PR-3。

验证记录必须分别标明 internal/mock、external test topology、真实供应商和生产范围。本片完成不等于第四阶段资格、032/033、真实支付或生产验收完成。

## 9. 非目标

- Responses、Messages、Gemini、Embeddings、Rerank、媒体、Realtime 的新增迁移。
- 新供应商账号、真实 usage/账单资格、commercial 发布、生产 SLA、HA 或性能声明。
- New API 的估算扣费、quota、工具按次收费、状态码盲重试、前端视觉或数据库。
- 自动切流、租户灰度、影子重复生成、自动退役或新增路由决策算法。

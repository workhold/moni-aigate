# 042：经营报表请求行元数据三态合同修复

2026-11-25；状态：**窄修复候选（P0.1）**。本片只修复 `/platform/reports` 经营报表前端严格解析器把合法"媒体空三元组"请求行当作非法数据、导致 API 200 但整页报错的 P1 缺陷。实现基线为 main `41f66e4ffab4768403845b0c48800f8368467fb2`（041 P0 已合入）。本片不开放真实用户内测，不表示第一阶段 41 项、真实供应商或生产验收完成；只读参考本地 `worktree-beta-experience-review` 提交 `4772a05` 的三态解析思路，不引入该分支其他工作，不创建第二套旧 DTO。

## 1. 缺陷与目标

未筛选（默认最近 7 天、全部客户/模型/协议）的经营报表会同时包含文本与媒体请求。`GET /api/platform/reports/operations` 返回 HTTP 200，但媒体行的计量元数据按 026/028 线路合同就是空 `schema_id/schema_digest/variant_key` 三元组；前端解析器要求每行 `variant_key='default'` 且 profile/schema 全部非空，一条合法媒体行即触发"经营报表响应含未定义或旧标量字段"，整页报错。筛选 Chat 后恰好只剩完整快照文本行，P0 证据才通过——该筛选路径掩盖了未筛选路径的缺陷。

目标：保持 041 P0 全部字段（payment_basis、provider_cost、暂估/账单确认贡献毛利、FX、商品维度）、严格键闭集与金额语义不变，同时让三类合法元数据状态完整展示；任何非法结构仍整表拒绝，不跳行、不补造、不把未知值规范化为零或 `default`。

## 2. 服务端线路事实（只读确认，不改 SQL/聚合）

1. 文本请求（`requestsSQL`）：`coalesce(r.protocol,'')`、`coalesce(r.execution_kind,'')` 允许独立空值；`LEFT JOIN request_meter_snapshots` 未命中时 `coalesce` 使 `profile_version/schema_id/schema_digest/variant_key` 四者同时为 `''`（无快照文本防御性线路）。快照行存在时四者必同时非空：`profile_version/schema_id` NOT NULL、`schema_digest` 32 字节（hex 后 64 位小写）、`variant_key` CHECK 等于 `'default'`。
2. 媒体请求（`mediaRequestsSQL`）：`profile_version` 为 `media_requests.profile_version`，表级 `CHECK(media_uuid)` 保证非零小写 canonical UUID；`schema_id/schema_digest/variant_key` 由 SQL 字面量固定为 `''`（媒体空三元组）。`protocol` 为 `operation ∈ {image_generation,tts,video_generation,realtime_turn}`，`execution_kind` 为派生的 `synchronous`/`asynchronous`，均非空。
3. `request_records.protocol` 可空（NULL 经 coalesce 为 `''`）；`request_records.execution_kind` NOT NULL 默认 `synchronous`。媒体行不携带 economics 快照，`offer_version_id/task_class` 为 `''`。
4. 本片不修改 `internal/reporting` 的 SQL、聚合、CSV 列或权限；只在其消费端（前端严格解析器）校准三态联合。

## 3. 前端请求行元数据状态合同

请求行保持既有精确键闭集（含 041 P0 字段与 `offer_version_id/task_class`）。元数据按下列封闭表示解析并原样保留，仅允许三种组合状态：

| 状态 | `profile_version` | `schema_id/schema_digest/variant_key` | 适用 |
| --- | --- | --- | --- |
| 完整文本快照 | 非零小写 canonical UUID | 非零小写 UUID、64 位小写 hex、`'default'` | 文本请求（`request_meter_snapshots` 命中） |
| 媒体空三元组 | 非零小写 canonical UUID | 三者同时为 `''` | 媒体协议行（media 派生 protocol） |
| 无快照文本 | `''` | 三者同时为 `''` | 防御性线路（LEFT JOIN 未命中） |

拒绝规则（失败关闭，任一行命中即整个报表拒绝，不展示部分金额）：

1. `schema_id/schema_digest/variant_key` 任意部分为空而其余非空（部分三元组）；或三者同时非空但任一值非法（非法/大写/nil UUID、非 64 位小写 hex、非 `default`）。
2. `profile_version` 非空但非法 UUID；完整三元组配空 profile；文本协议配有效 profile + 空三元组（半快照）；媒体协议配完整三元组（伪造文本快照）或空 profile。
3. `protocol`/`execution_kind` 各自独立地仅为 `''` 或既有闭集值（十种协议、两种执行类型）；其他值、null、错误类型一律拒绝。`''` 是缺失标记，不是新筛选项或名为 `unknown` 的协议；查询侧 `OperatingProtocol/OperatingExecutionKind` 与 `Filter.Validate` 继续保持非空闭集，只有响应行类型显式联合 `''`。
4. `offer_version_id/task_class` 维持现行规则（同时为空或合法配对），媒体行自然落在空值分支。

## 4. 故障与状态边界

不新增业务状态机。解析结果只有"完整接受"与"整体拒绝"：不得跳过坏行、只显示好行、或把错误值规范化后接受。既有报表 generation/sequence 失效、筛选变更清空旧结果、CSV 下载状态与错误文案保持不变；`operatingFilter` 的查询侧校验与 GET 路径不变。JSON 与 CSV 保留空值原义。

## 5. 验收边界

1. **前端严格解析**：四类媒体空三元组、完整快照、无快照文本、混合响应正例；部分三元组、非法/大写/nil UUID、非法 digest/variant、伪造媒体快照、文本半快照、null/错误类型、未知非空枚举负例；P0 金额字段在正例中原样保留。另加独立穷举状态矩阵，逐一对照三态组合与非法变异。
2. **PostgreSQL reader**：媒体行断言 profile 保留且空三元组原样；无快照线路断言六元组全空。使用专用测试库；SKIP 不记为 PostgreSQL 验收。
3. **API 集成**：商业链路夹具在同一响应中并存完整快照文本行与媒体行，JSON 与 CSV 逐列断言元数据原样保留、P0 字段不变、权限边界不变。
4. **真实浏览器**：以干净提交构建隔离镜像，未筛选经营报表在桌面与 390px 下覆盖：媒体空三元组、媒体非空（已结算含金额）、纯 Chat、混合结果、注入非法结构后整页拒绝、P0 字段保留。浏览器只做登录与 GET/观察，不做模型生成、支付或业务写；非法结构经网络层注入，不改数据库、不伪造服务端行为。
5. **回归**：目标 Node 套件、完整 `npm --prefix web test`、生产构建、相关 Go 测试与 race、`make check`；共享商业链候选按仓库要求在干净最终提交重跑完整 029。
6. **架构**：统一架构相关正文、`changes.md` 与 manifest 语义刷新；明确本片只有读端解析合同校准，无模块、数据、权限或迁移结构变化。

## 6. 停止条件

任何为通过测试而删除合同字段、把空 `variant_key` 改写为 `default`、从当前模型或历史数据推断/补造 schema、接受未知非空枚举、跳过坏行或降低键闭集校验的实现均不得交付。无法执行专用 PostgreSQL 测试或真实浏览器验收时，必须明确保留未验证项，不得声称商业链或生产验收完成。

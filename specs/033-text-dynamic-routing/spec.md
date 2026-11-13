# 033：第三阶段文本动态路由

2026-11-10；状态：草案，待开发者评审后冻结。所属阶段：[第三阶段](../../docs/design/phase-roadmap.md#6-第三阶段openrouter-风格文本动态路由)。前置：[032](../032-phase2-text-data-plane/spec.md) 的数据面与测量，以及本规格 §2 的合同冻结。

OpenRouter 是商业托管产品，不是开源仓库。本规格只参考其公开文档描述的产品语义：模型与供应商分层、硬限制与软偏好、`order`/`only`/`ignore`/`allow_fallbacks`/`require_parameters`/`sort`/`max_price` 等请求语义（见 [browser-research](../../docs/research/browser-research.md)）。不声称取得其服务端实现。

作用范围：**只覆盖文本**。媒体、异步任务与 Realtime 保持单 profile、单 adapter，不参与动态路由（[路线 §2.1](../../docs/design/phase-roadmap.md#21-文本与媒体的当前结构)）。

## 1. 现状与缺口

| 项 | 现状 | 位置 |
|---|---|---|
| 逻辑模型 | `models` + `model_capability_versions` | `migrations/current_mvp.sql` |
| 物理模型 | 仅 `model_bindings.upstream_model` 字符串 | 同上 |
| profile | 每个路由版本绑定单一 `profile_version` | 028 u-storage |
| 选择模式 | `manual` / `cost_first` | `internal/routing/cost.go` |
| 决策记录 | 五字段数组，封闭原因，每 channel 一次 | `routing.Decision`、`billing.validateRouteDecisions` |
| 未持久化 | 抽签、健康、评分输入、未准入 attempt 的失败池 | LLD §8 |
| 不重放 | `SafeToRetry` + `Plan.CanRetry` | `internal/provider/types.go`、`internal/routing/routing.go` |

三类切换中只有第一类（同逻辑模型的渠道切换）已有实现。

## 2. 数据模型

### 2.1 物理模型

```sql
CREATE TABLE physical_models (
  id               text PRIMARY KEY,
  logical_model_id text NOT NULL REFERENCES models(id),
  upstream_model   text NOT NULL,
  state            text NOT NULL DEFAULT 'active' CHECK (state IN ('active','retired')),
  revision         bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  capability       jsonb NOT NULL,   -- 见 2.2
  created_at       timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (logical_model_id, upstream_model)
);
CREATE TRIGGER immutable_physical_model BEFORE UPDATE OR DELETE ON physical_models
  FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
```

物理模型不可变；变更通过新建行加退役旧行完成，与渠道/路由的既有做法一致。

### 2.2 能力

```json
{
  "context_limit": 200000,
  "input_upper_bound": 200000,
  "output_limit": 16384,
  "supports_tools": true,
  "supports_parallel_tool_calls": true,
  "usage_semantics": "final",
  "modalities": ["text"],
  "quality_tier": "standard",
  "quality_score": 0.82,
  "max_concurrency": 64
}
```

`quality_tier` 是经营档位（如 `economy`、`standard`、`premium`），由管理员定义；`quality_score` 是离线评估结果，没有数据时为 null，不得用猜测值填充。

### 2.3 绑定

**现状核对**（`migrations/current_mvp.sql`、`internal/catalog/store.go`）：`model_bindings` 的唯一键是 `(route_policy_version_id, channel_id, upstream_model, variant_key)`，SQL 层**已经允许**同一 channel 下不同 `upstream_model`。“同一路由池内每个 channel 只能出现一次”是 `catalog.PublishWithCostPolicy` 里的 Go 校验（`key := b.ChannelID; if seen[key] ... ErrInvalid`，`store.go:317`），并写在 LLD §8。

因此需要改的是**这条 Go 校验的去重键与 LLD §8 的规则**，不是 SQL 约束：

```text
去重键：ChannelID  →  (ChannelID, PhysicalModelID)
```

`model_bindings` 追加一列：

```sql
ALTER TABLE model_bindings ADD COLUMN physical_model_id text REFERENCES physical_models(id);
-- 迁移时按当前 upstream_model 为每个既有 binding 生成物理模型行并回填
```

`upstream_model` 列保留，由触发器保证与 `physical_models.upstream_model` 一致；新代码以 `physical_model_id` 为准。

### 2.3.1 与现有约束的关系（必须先裁决，属于 3.0 的前置）

| 既有约束 | 出处 | 变化 |
|---|---|---|
| 同池 channel 只出现一次 | Go 校验 `store.go:317`、LLD §8 | 去重键改为 `(channel, physical_model)`；SQL 无需改动 |
| 路由版本的 `profile_version` 等于模型的 `profile_version` | `meter_route_profile` 触发器（`current_mvp.sql`）强制 `NEW.profile_version := models.profile_version` | **不改**：D-033-0 选项 A 与它吻合 |
| 客户侧预占界等于逻辑模型 profile 的界 | `billing.Reserve`：`in.InputUpperBound == profile.Input` 且 `in.OutputLimit <= profile.Output` | **不改**：见下 |
| 决策记录每 channel 一次、恰一个 selected | `billing.validateRouteDecisions` | 被 §2.6 的新记录取代 |

**profile 的处理（决策点 D-033-0）**：

- 选项 A（建议）：逻辑模型的 `profile_version` 继续表示**客户侧零售合同**（计量 schema、售价维度、输出上限、预占界）。同一路由版本下所有物理模型必须使用同一计量 schema，否则发布时拒绝。每个物理模型的能力（上下文、工具、输出上限、usage 语义）放在 `physical_models.capability`。
- 选项 B：每个物理模型一个独立 profile。要重写 028 的 route 完整性 guard 与 `meter_route_profile`，范围远大于 A。

**预占界**：客户侧预占由逻辑模型 profile 决定，与选中哪个渠道或物理模型无关，所以第三阶段**不需要**“按候选最大界预占”。物理模型只需满足：`capability.context_limit ≥ profile.context_limit`、`capability.output_limit ≥ 本次请求的输出上限`、工具能力满足请求；不满足的候选在硬过滤阶段以 `context_too_small`、`output_limit_too_small`、`tools_unsupported` 排除。

供应商侧的输入/输出界（写入 `attempt_meter_snapshots`）由**被选中的物理模型**决定，这是对 028 attempt 快照 guard 的一处修订：attempt 的供应商界来自所选物理模型的能力，而不是路由版本唯一 profile 的界。**该修订的合同冻结前，3.1 不开始。**

### 2.4 路由策略版本

`route_policy_versions` 增加：

```sql
ALTER TABLE route_policy_versions
  ADD COLUMN scoring jsonb,          -- 权重与策略，见 §4.3
  ADD COLUMN quality_floor text,     -- 最低档位 (economy|standard|premium) 或 null
  ADD COLUMN min_margin_rate bigint, -- 最低毛利率，万分比或 null
  ADD COLUMN stickiness jsonb;       -- {"enabled":bool,"max_age_s":N,"break_on":[...]}
```

沿用完整版本 CAS 发布。旧版本这些列为 null，语义为“不启用”。

### 2.5 路由会话（粘性）

```sql
CREATE TABLE route_sessions (
  tenant_id    text NOT NULL,
  session_id   text NOT NULL,          -- 由客户请求的会话标识派生，非登录会话
  physical_model_id text NOT NULL REFERENCES physical_models(id),
  channel_id   text NOT NULL,
  established_at timestamptz NOT NULL,
  expires_at   timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  breaks       integer NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, session_id)
);
```

与登录会话 `session_families` 无关。会话标识来源必须显式定义（例如请求头 `X-Moni-Session`），并在 spec 冻结时写清；不能从内容或 IP 推断。

### 2.6 路由决策记录

现有 `upstream_attempts.route_decisions` 是不可变 JSONB 数组（≤1000 项、≤256KiB，`migrations/006`），在 `RecordAttempt` 的同一事务里写入，无法表达新语义。运行时消费者只有三处：网关写入（`gateway.go`）、平台 attempts 接口（`reports.go`）和前端 `RouteAttempts.vue`；冻结的采购候选包不属于运行路径。

**每个候选一行**会让热路径每请求多次 INSERT，与第二阶段的延迟基线冲突。改为**每次选择决策一行**，候选集合放在有界 JSONB 里，沿用现有数组的上限与不可变做法：

```sql
CREATE TABLE route_decisions (
  id                 text PRIMARY KEY,
  tenant_id          text NOT NULL,
  request_id         text NOT NULL,
  attempt_id         text,                     -- 未准入 attempt 时为 null
  sequence           integer NOT NULL,
  policy_version_id  text NOT NULL REFERENCES route_policy_versions(id),
  selected_binding_id text,                    -- 无可用候选时为 null
  selected_physical_model_id text,
  draw               numeric(30,0),            -- 抽签值，用于回放
  sticky             text,                     -- inherit | establish | break_quality | break_cost | break_health | null
  degradation        text NOT NULL DEFAULT 'none',
  candidates         jsonb NOT NULL,           -- 见下
  candidates_digest  bytea NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK (jsonb_typeof(candidates)='array' AND jsonb_array_length(candidates)<=1000 AND octet_length(candidates::text)<=262144),
  FOREIGN KEY (tenant_id, request_id) REFERENCES request_records(tenant_id, id)
);
CREATE TRIGGER immutable_route_decision BEFORE UPDATE OR DELETE ON route_decisions
  FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE UNIQUE INDEX route_decisions_request_sequence ON route_decisions(request_id, sequence);
```

`candidates` 每项：`binding_id`、`channel_id`、`channel_version_id`、`physical_model_id`、`priority`、`weight`、`reason`、`score`（评分输入与结果的规范对象，含统计快照）、`cost_snapshot`。被硬约束排除的候选也在数组里（这补上了“完整失败池解释”）。写入量是一次 INSERT，不随候选数增长。

写入时机：与 attempt 同事务（已准入 attempt 的决策）；无候选可用时在释放前写一行 `attempt_id=null`。

### 2.7 原因枚举

硬约束排除：`not_authorized`、`region_forbidden`、`capability_missing`、`quality_below_floor`、`context_too_small`、`tools_unsupported`、`output_limit_too_small`、`disabled`、`channel_quarantined`、`binding_isolated`、`credential_revoked`、`capacity_full`、`circuit_open`、`half_open_busy`、`cost_unknown`、`cost_limit_exceeded`、`margin_below_floor`、`procurement_paused`、`procurement_budget_exceeded`、`procurement_bound_invalid`、`procurement_budget_unavailable`。

排序阶段：`selected`、`lower_priority`、`higher_cost`、`lower_score`、`weighted_not_selected`、`sticky_override`、`already_tried`。

枚举只增不改语义；新增原因需要同时更新持久化校验与回放器。

## 3. 请求侧语义

只允许收紧，不得放宽任何租户、成员、Key 或平台限制（沿用 [browser-research](../../docs/research/browser-research.md) §2 的结论）。

**承载范围**：只在 `POST /v1/chat/completions` 上接受路由偏好对象（客户侧保留最小稳定的 OpenAI-compatible 入口）。Responses、Messages、Gemini 原生入口保持严格信封，拒绝额外字段，只使用路由策略的默认值。字段名由决策点 D-033-2 确定：沿用 OpenRouter 的 `provider` 便于迁移，但会让人误以为是其实现；独立命名（如 `route`）语义更清楚。建议独立命名，下表用 `route.*` 表示。

| 请求字段 | 允许取值 | 语义 |
|---|---|---|
| `route.order` | 供应商或物理模型列表 | 优先顺序；命中的排在前，但不越过硬约束 |
| `route.only` | 供应商列表 | 与授权求交；空交集返回 400 |
| `route.ignore` | 供应商列表 | 排除，与 `only` 同时出现时取 `only` 结果再排除 |
| `route.allow_fallbacks` | bool，默认 true | false 时只允许第一个候选；不可用即失败 |
| `route.require_parameters` | bool | true 时排除不能满足请求参数的候选 |
| `route.sort` | `price`、`latency`、`throughput` | 软偏好；只改变同优先级内的排序 |
| `route.max_price` | 输入/输出上限（微元/百万 token） | 硬排除条件 |
| `route.data_collection` | `allow`/`deny` | 只能加严；平台或租户已禁止时不可打开 |

未知字段、未知供应商、与 `model` 档位不一致的 `only` 一律拒绝。不实现任意路由表达式或脚本。

## 4. 选择算法

严格分两个阶段。

### 4.1 硬约束过滤（先于任何排序）

按顺序过滤：授权（平台模型、租户、成员、Key、`only`/`ignore`）→ 地域 → 能力（上下文、工具、参数满足、`require_parameters`）→ 质量档位下限 → 渠道与 binding 状态 → 凭据状态 → 容量与熔断 → 采购预算与暂停 → 采购成本未知策略 → `max_price` → 成本上限 → **最低毛利**。每个被排除的候选都记入当次决策的 `candidates` 数组。

最低毛利：`1 - 保守供应商成本 / 客户冻结售价` 低于 `min_margin_rate` 时排除。成本未知时按 `unknown_cost_policy`：`deny` 排除，`allow` 保留但标注。

### 4.2 排序

只在剩余候选内：

1. 若会话粘性命中且未过期、未触发打破条件，直接选择该物理模型与渠道，原因 `sticky_override`。
2. 最高 `priority` 层。
3. `cost_first` 时按保守成本分数升序；`sort` 偏好作为同层内的次级排序（`price` 与成本分数同序，`latency`、`throughput` 使用 §5 的统计量）。
4. 同分时按正权重随机，使用可记录的抽签值。
5. 选中后在同渠道内按凭据权重选择。

不同质量档位的候选**不混合排序**：跨档位只能通过 §6 的 auto 商品。

## 5. 统计量

延迟与成功率必须来自真实数据，不能来自估算。

- 数据源：`delivery_observations`（阶段时间点）与 `upstream_attempts`（结果、`created_at`）。当前表没有 attempt 级完成时间，需要追加 `completed_at timestamptz` 与 `first_content_at timestamptz`。
- 聚合：按 `(physical_model_id, channel_id)` 在滚动窗口（默认 15 分钟）内计算 p50/p95 首内容延迟、成功率、超时率。样本数低于阈值（默认 50）时统计量为 null，`latency`/`throughput` 排序跳过这些候选，不用默认值替代。
- 老化：窗口外样本不参与。冷启动与样本不足必须显示在控制台。
- 存储：内存滚动窗口 + 定期落 PostgreSQL 的聚合表（`route_stats_windows`），重启后可重建，不作为资金事实。
- 未知不重放：统计只读，不触发重试。

## 6. 跨质量档位 auto

- auto 是一个显式的逻辑模型，其候选来自多个质量档位。
- **定价（决策点 D-033-1）**：
  - 选项 A（建议）：auto 只有**一个**售价版本，客户无论落到哪个档位都按该价结算。这样请求冻结的价格快照形状不变，不新增价格维度；经营者风险由 §4.1 的最低毛利硬过滤保护。auto 的公布价应按“最贵候选仍满足最低毛利”设定。
  - 选项 B：按选中档位分别定价。`price_versions` 现在只允许 `variant_key='default'`，需要放宽并重做价格快照与 Reserve 选价，属于计价扩展，不在第三阶段范围。
- 客户预占界由 auto 逻辑模型的 profile 决定（`Reserve` 要求预占界等于该 profile 的界），与最终选中哪个档位无关，仍是一个请求、一份 reservation、一份 settlement。上游能力小于该 profile 的候选在硬过滤阶段被排除（`context_too_small`、`output_limit_too_small`），所以任何 attempt 都不会突破客户预占。
- 质量门槛：auto 商品声明 `quality_floor`；低于门槛的候选在硬过滤阶段排除。
- 降档必须显式：`degradation` 记录为 `tier_changed`，请求详情可见。默认不允许静默降档；若经营者开启“允许降档”，必须在公布的 auto 说明中写明。
- `allow_fallbacks=false` 时不跨档位。
- **客户可见的服务模型（决策点 D-033-3）**：响应 `model` 字段回显客户请求的 auto 名；实际服务的物理模型是否对客户可见由 `physical_models` 上的展示开关决定，默认仅平台可见，客户请求详情显示“服务档位标签”。

## 7. 路由回放

```text
ReplayRoute(policyVersionID, requestShape, draw, statsSnapshot, candidates) -> Decision[]
```

- 输入是持久化的 `route_decisions` 行与对应快照；不重新查询当前配置、不查询当前统计。
- 重放只读：不写 `route_decisions`、不创建 attempt、不触碰资金。
- 一致性要求：给定同一输入，重放得到同一选中候选与同一原因序列。任何不确定来源（系统时间、随机、当前配置）必须已在决策记录中固化。
- CLI：`moni-aigate route-replay --request <id>`，输出决策表与选中路径。

## 8. 与账务的一致性

- 一个请求一个 settlement；每次 attempt 各自记录采购成本。
- 跨模型切换不新建预占；切换后仍使用请求时冻结的客户售价。fixed 与 tier 模型只有一个逻辑模型售价；auto 按 D-033-1 选项 A 只有一个 auto 售价。
- 客户预占界等于逻辑模型 profile 的界（见 §2.3.1），与路由结果无关；能力不足的候选在硬过滤阶段排除。
- 路由决策与 attempt、结算的关联通过 `attempt_id` 与 `request_id` 双向可查。
- 结算、退款、恢复不读取路由策略，避免运营调价锁住资金（沿用 018 的规则）。
- 统计快照：`score_canonical` 内嵌决策当时的统计量，回放与审计不依赖当前窗口。

## 9. 不分阶段切换

按 ADR-005 一次性替换，不保留旧 `route_decisions` 双格式：

1. 新增表与列，迁移既有数据为等价的 `route_decisions` 行（`degradation=none`）。
2. 网关改写入新表，读取端同时上线。
3. 删除 `upstream_attempts.route_decisions` 列与 `protect_route_decisions` 触发器。

候选材料不进入默认启动路径（AGENTS.md 现有约定）。

## 10. 验收

| 组 | 断言 |
|---|---|
| 硬约束 | 每个原因至少一个用例；被排除的候选都在决策表中有行；评分高不能复活被硬排除的候选 |
| 排序 | 同 priority 按成本/延迟/成功率；抽签可复现；`sort` 不越过硬约束 |
| 三类切换 | 同模型换供应商；同档位换物理模型；跨档位 auto（含降档标记与结算价格） |
| 粘性 | 建立、延续、因质量/成本/健康打破，各有原因记录 |
| 毛利 | 低于下限排除；成本未知按策略 |
| 回放 | 同一请求重放结果一致；重放不产生写入或派发 |
| 不重放 | unknown 消费不切换，mock 接收记录恰好一条 |
| 账务 | 跨模型切换后售价快照、成本快照、唯一结算一致；报表毛利正确 |
| 性能 | 每次决策一次 INSERT，不随候选数增长；不使完整链路超出第二阶段基线 |

## 11. 分片

| 片 | 内容 | 依赖 |
|---|---|---|
| 3.0 | 合同冻结：本 spec、D-033-0（profile 处理）、D-033-1（auto 定价）、D-033-2（字段名）、D-033-3（可见性）、028 attempt 快照 guard 修订合同 | 032 完成 |
| 3.1 | 物理模型、质量档位、绑定放宽、迁移 | 3.0，含 028 guard 修订 |
| 3.2 | 决策表写入与完整失败池解释（不改变选择结果） | 3.1 |
| 3.3 | 硬约束过滤扩展（质量、上下文、工具、max_price、毛利） | 3.2 |
| 3.4 | 统计量与延迟/成功率排序 | 3.3 |
| 3.5 | 会话粘性 | 3.4 |
| 3.6 | 跨档位 auto | 3.5 |
| 3.7 | 回放与 CLI | 3.2 |

3.1 与 3.2 是资金邻接改动，由集成人串行合并；3.3–3.7 可按片并行设计，实现合并在前一片验收之后。

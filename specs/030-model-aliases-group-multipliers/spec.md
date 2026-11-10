# 030：模型别名与分组倍率

2026-11-10；状态：草案，待开发者评审后冻结。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#3-第一阶段固定版本-new-api-核心业务能力对齐)。验收项：[029](../029-phase1-acceptance/spec.md) P1-CAT-02、P1-PR-02。

两项都是 New API 对齐能力，彼此独立，可以分开实施。它们都不新建计价、钱包或路由路径。

## A. 模型别名

### A.1 场景与边界

客户和 SDK 常用不同的名字指同一个模型（例如 `gpt-4o` 与 `gpt-4o-2024-08-06`）。平台管理员可以给一个已发布的逻辑模型登记若干别名，客户用别名调用时，系统解析到该逻辑模型，按该模型的授权、售价、路由和计量处理。

- 别名是“客户可见名 → 逻辑模型”。它和“逻辑模型 → 物理模型”的路由绑定无关；后者属于第三阶段（[033](../033-text-dynamic-routing/spec.md)）。
- 别名不带权限：有效授权仍是平台模型状态、租户授权、成员权限与 Key scope 对目标逻辑模型的交集。
- 别名只解析到已发布、启用的逻辑模型；不解析客户端提交的供应商前缀或部署后缀（如 `openai/gpt-4o`、`gpt-4o@azure`）。
- 别名与所有 `models.public_name` 共享一个名字空间，不能重名。
- 媒体、任务与 Realtime 的 profile 名称不在本规格范围。

### A.2 数据模型（追加迁移草案）

```sql
CREATE TABLE model_aliases (
  alias          text PRIMARY KEY COLLATE "C",
  model_id       text NOT NULL REFERENCES models(id),
  state          text NOT NULL DEFAULT 'active' CHECK (state IN ('active','retired')),
  revision       bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  operation_id   text NOT NULL,
  created_by     text NOT NULL REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
  retired_at     timestamptz,
  CHECK (octet_length(alias) BETWEEN 1 AND 100 AND alias !~ '[[:space:]]'),
  CHECK ((state = 'retired') = (retired_at IS NOT NULL))
);
CREATE INDEX model_aliases_model ON model_aliases(model_id) WHERE state = 'active';
```

- 名字空间唯一：别名插入与模型创建都先对 `hashtext(name)` 取同一个事务级 advisory lock，再检查对方表；触发器兜底拒绝 `alias = 任一 models.public_name` 以及新 `public_name = 任一 model_aliases.alias`。
- 退役后的别名保留行，不可复用为另一个模型的别名，避免历史请求含义漂移。重新指向必须新建别名名字。
- `model_aliases` 的修改只允许 `state: active → retired`；应用角色无 DELETE。
- 管理操作写 `audit_events`，并复用现有 pricing/catalog 命令的幂等 operation 回执形状。

请求快照追加一列：

```sql
ALTER TABLE request_records ADD COLUMN requested_model_name text COLLATE "C";
```

保存客户提交的原始名字。`model_id` 仍为解析后的逻辑模型。历史行为 NULL，不回填。

### A.3 解析与接口

**解析**：`catalog.AuthorizedModel` 的名字条件从 `m.public_name = $name` 扩展为“`public_name` 匹配，或存在 `state='active'` 的别名指向该模型”。两者在同一 SQL 中完成，授权条件不变。解析发生在 Reserve 之前；之后的售价、预占、路由、attempt 都只使用 `model_id`。

**响应中的模型名**：非流与流式响应的 `model` 字段回显客户提交的名字，以兼容按名字匹配的 SDK。供应商返回的上游模型名不透传（现有规则不变）。

**目录**：

| 读面 | 行为 |
|---|---|
| `GET /v1/models` | 列出授权内的公开名与其活跃别名，各为一条 |
| 客户控制台模型页 | 公开名下列出别名 |
| 公开目录 `/api/public/models` | 仅当目标模型 `public_visible` 时列出别名 |

**管理 API**：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/platform/models/:id/aliases` | 列出（含已退役） |
| POST | `/api/platform/models/:id/aliases` | `{operation_id, alias}`；冲突 409 `model_name_conflict` |
| POST | `/api/platform/model-aliases/:alias/retire` | `{operation_id, expected_revision}` |

权限：仅平台 admin；敏感操作再认证与现有模型管理一致。

### A.4 状态机

```text
(不存在) --create--> active --retire--> retired
```

retired 为终态。在途请求在 Reserve 时已解析为 `model_id`，退役不影响其收尾。

### A.5 验收

- 别名调用与公开名调用产生相同的售价快照、路由候选与结算，`requested_model_name` 记录别名。
- 别名不能绕过租户、成员或 Key 授权；未授权模型的别名返回与公开名相同的错误。
- 别名与公开名冲突、别名重复、退役别名复用均被拒绝；并发创建同名别名与同名模型只有一个成功。
- 退役后新请求 404 `model_not_found`，在途请求正常结算。
- 供应商前缀与部署后缀不被解析。
- `/v1/models` 与公开目录行为符合 A.3。

## B. 分组倍率

### B.1 场景与边界

New API 用“分组倍率”给不同客户组打折或加价。moni 已有价格组（023-A），组内是**绝对**价格版本，缺价时拒绝，不回落默认价。HLD §6.3 规定客户售价在请求时冻结，不在运行时乘可变倍率。

因此倍率只做录入便利：管理员选择价格组、一批模型和一个倍率，系统读取这些模型当前生效的默认价格，乘以倍率得到绝对价格，作为该组的新价格版本发布。运行时完全不变。

- 不新建价格表、不改 Reserve 选价逻辑、不改缺价拒绝语义。
- 默认价格之后变化时，组价不会自动跟随；控制台提示“组价基于旧默认价”，由管理员重新编译发布。
- 倍率只作用于零售价，不作用于采购成本。

### B.2 计算规则

- 倍率为十进制字符串，`0 < m ≤ 100`，最多 6 位小数。按有理数 `m = p / 10^6` 精确表示。
- 当前管理 API 的单价为整数（每百万 token 的微元）。组价 = `ceil(default_rate × p / 10^6)`，输入、输出分别计算，使用 big.Int，向上取整到 1 微元。
- 默认价读取使用与 Reserve 相同的选价规则：同一数据库时刻下 `price_group_id IS NULL` 的当前生效版本。某模型没有生效默认价则整批拒绝，返回缺价的模型列表。
- 计算结果为 0 时允许（默认价为 0 时）。

### B.3 接口与存储

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/platform/price-groups/:id/price-versions/compile` | 编译发布 |
| POST | `/api/platform/price-groups/:id/price-versions/compile-preview` | 只读预览，同一计算，不写入 |

请求体：

```json
{
  "operation_id": "uuid",
  "multiplier": "0.85",
  "model_ids": ["uuid", "..."],
  "effective_at": "2026-12-01T00:00:00Z",
  "expected_base_version_ids": {"model-uuid": "price-version-uuid"}
}
```

- `model_ids` 1..100 个，去重并排序后参与摘要。
- `expected_base_version_ids` 可选；提供时，若数据库时刻读到的默认价版本不同，整批 409 `base_price_changed`，避免基于预览后已变化的默认价发布。
- 一个 operation 内为每个模型调用现有 `meter_insert_token_price` 写入组价版本；任何一个失败整批回滚。
- 不可变 operation 结果保存：倍率原文、每个模型的基准版本 ID 与基准单价、编译后单价、新版本 ID。审计记录同一内容。
- 不新增表。若需要在控制台显示“最近一次倍率”，从 operation 结果读取。

权限与锁序沿用 023-A 的组价发布：price group SHARE，状态必须 active。

### B.4 验收

- 编译结果与手算一致，包括向上取整与 0 价。
- 预览与发布对同一输入给出相同单价。
- 某模型缺默认价时整批拒绝，不写任何版本。
- `expected_base_version_ids` 不匹配时 409 且不写入。
- 发布后 Reserve 选到组内绝对价格；修改默认价后，已发布组价不变，控制台显示过期提示。
- 重复 operation_id 返回原结果，不重复发布。

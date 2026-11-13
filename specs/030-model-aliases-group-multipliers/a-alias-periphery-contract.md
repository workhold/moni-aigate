# 030-A 模型别名外围实现合同（草稿）

状态：外围实现草稿，待开发者评审和正式追加迁移后冻结。提交范围只覆盖 `internal/catalog`、`internal/server`、`web/src` 及对应测试；不修改正式 migrations、`internal/billing`、统一计量、分组倍率或公共钱包接口。

## 已实现接缝

- `catalog.ResolveModelName` 是纯解析接缝：公开名和 `state=active` 的别名都只返回逻辑 `model_id`；退役别名、未知名和其他逻辑模型的别名返回未命中。
- `catalog.AuthorizedModel` 在授权 SQL 内按公开名或活跃别名解析，原有平台模型、租户、成员和 Key scope 条件保持不变。解析后 `Model.RequestedName` 保存客户提交名；server 在 provider parser 前将 profile 的请求名设为该值，使响应的 `model` 回显客户名，路由和 Reserve 仍使用 `Model.ID`。
- `/v1/models`、客户 `/api/models`、公开 `/api/public/models` 在别名表存在时追加活跃别名；公开目录仅从 `public_visible AND enabled` 的模型追加别名。
- 平台 admin 管理 API：
  - `GET /api/platform/models/:id/aliases` 返回 active 与 retired；
  - `POST /api/platform/models/:id/aliases` 接受 `{operation_id,alias}`；
  - `POST /api/platform/model-aliases/:alias/retire` 接受 `{operation_id,expected_revision}`。
- Vue 平台模型页提供别名登记/退役，客户模型页显示活跃别名。退役为终态，前端写操作使用稳定 operation ID；服务端重复创建 operation 返回原映射。
- 正式表尚未安装时，旧目录和旧请求路径继续工作；别名管理 API 返回 `model_alias_unavailable`，不以本地 fixture 冒充已安装能力。

## 集成方追加 SQL 合同

以下是集成方需要追加到正式迁移的对象；本分支不复制到 `migrations/`：

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
  CHECK ((state = 'retired') = (retired_at IS NOT NULL)
         AND (state = 'active') = (retired_at IS NULL))
);
CREATE UNIQUE INDEX model_aliases_operation
  ON model_aliases(operation_id);
CREATE INDEX model_aliases_model
  ON model_aliases(model_id) WHERE state = 'active';
ALTER TABLE request_records
  ADD COLUMN requested_model_name text COLLATE "C";
```

- `operation_id` 唯一约束是应用幂等查询的数据库背书；退役会把该行的 operation ID 更新为退役操作 ID，因此集成方如需永久保留每次回执，应复用现有命令回执表或另行追加不可变 operation 事实，不应依赖该列保存完整历史。
- 模型创建和别名创建必须在同一事务使用 `pg_advisory_xact_lock(hashtext(name)::bigint)`；随后检查两张表的共享名字空间。正式迁移应以触发器兜底 `models.public_name`↔`model_aliases.alias` 的双向冲突，不能只依赖应用检查。
- 迁移所需函数合同为 `model_alias_namespace_guard() RETURNS trigger`（拦截两张表的共享名字空间冲突）和 `model_alias_state_guard() RETURNS trigger`（拦截 retired→active、model_id 改写、revision 回退及状态/时间不一致）；函数应固定部署 schema 搜索路径并按现有角色策略收紧 EXECUTE。函数正文与触发器绑定由集成方按正式迁移规范实现。
- 应用角色需要对别名执行列出、插入和 active→retired 的受限更新；禁止 DELETE。触发器必须拒绝 retired→active、model_id 改写、revision 回退和 retired_at 状态不一致。
- `requested_model_name` 只在请求出生时保存客户原始提交名，历史行保持 NULL；`model_id` 继续保存解析后的逻辑模型。Reserve、价格、路由、attempt 和结算均不得读取别名重新解析。
- 本片没有新增计量或钱包函数。倍率编译仍是 030-B 的后续实现，继续使用现有绝对价格发布路径。

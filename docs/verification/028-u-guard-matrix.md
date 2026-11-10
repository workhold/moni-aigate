# 028-U current-only guard 矩阵索引

本索引对应 `migrations/current_mvp.sql` 与 `migrations/current_*.sql` 的实际空库安装路径。迁移测试中的当前主线夹具统一使用 `migrations.ApplyCurrent`；历史 `Apply`/`ApplyThrough` 只用于拒迁或冻结历史合同测试，不能作为当前服务路径的验收证据。

数据库固定为 PostgreSQL 16.15 专用库 `127.0.0.1:55439/moni_test`。所有测试使用随机隔离 schema，测试完成后删除。没有使用 5432，也没有修改 SQL 迁移、公共 billing/wallet/provider 或 Makefile。

## 十类 guard 覆盖

| 类别 | 当前 SQL 事实与 guard | 正例 | 非法、绑定、不可变、并发或回滚 | 权限证据 |
| --- | --- | --- | --- | --- |
| schema | `meter_schemas`、`meter_schema_dimensions`；`meter_schema_complete`、`meter_schema_dimension_complete` | `TestUnifiedTextMeteringCurrentPositiveGuardFacts` 验证当前 profile 的两维 schema 已出生 | `TestUnifiedTextMeteringSchemaAndCapabilityCompletenessGuards` 验证缺维度/坏 canonical 回滚；`TestUnifiedTextMeteringCurrentImmutablePriceCapabilityAndAttemptFacts` 验证 schema 与维度不可变 | `TestUnifiedTextMeteringGuardRoleMatrix`、`TestUnifiedTextMeteringCurrentActualLoginMeterPermissions` |
| capability | `model_capability_versions`、`model_capability_variants`、`model_capability_bound_lines`；`meter_capability_*` | 当前正例验证 default variant 与四条 capability bound | 同一测试验证 capability/variant/bound 不可变；schema/capability completeness 测试验证缺行、input-only 非法与失败回滚 | 同上；APP 只能读/插入当前计量事实，不能更新历史 |
| route | `route_policy_versions`、`model_bindings`、`binding_cost_rate_limits`；`meter_route_*` | 当前正例验证非零 active revision 与 binding | `TestUnifiedTextMeteringCanonical`、`TestMeterCompletenessRejectsGaps` 覆盖空 route、半套 cap、错误 channel/version、跨 binding 拼接和出生事务回滚；`TestUnifiedTextMeteringDoesNotRetainRouteRevisionBypass` 验证 route identity | 计量表权限与函数权限由 guard role 矩阵验证；route 本身是全局发布事实，跨租户场景不适用 |
| retail | `price_versions`、`price_rate_lines`；`meter_price_*` | 当前正例验证请求 snapshot 引用两条原 retail rate line，金额/settlement 测试验证原向量 | `TestUnifiedTextMeteringCurrentImmutablePriceCapabilityAndAttemptFacts` 验证 price/rate 不可变；`TestUnifiedTextMeteringAmountAndSettlementGuards` 验证错误金额、released 零值与事务回滚 | 全局发布事实不做无意义的跨租户模拟；实际计量表读写边界见 role 矩阵 |
| supplier | `cost_versions`、`cost_rate_lines`；`meter_cost_*` 与 quote guard | 当前正例验证 attempt snapshot 的 known supplier quote 与两条 cost rate line | `TestUnifiedTextMeteringAttemptQuoteBindingAndSendingGuards` 验证 quote/parent domain、sending 无成本来源拒绝；`TestUnifiedTextMeteringCurrentImmutablePriceCapabilityAndAttemptFacts` 验证 cost/rate 不可变；foreign domain 测试验证跨 schema/channel/upstream 不可混入 | 实际 LOGIN/projector/worker 不能读取计量表或执行 `meter_%`，见下方命令 |
| request | `request_records`、`request_meter_snapshots`、`request_meter_bound_lines`；`meter_request_*` | 当前正例由真实 `Reserve` 出生并验证四条 request bounds、原 retail snapshot | `TestUnifiedTextMeteringSnapshotTenantBindingRollsBack`、`TestUnifiedTextMeteringGuardMatrix` 覆盖跨租户、跨请求、缺 snapshot、请求 identity 改写、终态指针和失败回滚；snapshot/bounds 不可变 | `TestUnifiedTextMeteringGuardRoleWriteRejections` 与实际 LOGIN 测试验证 APP 不能更新计量历史 |
| attempt | `upstream_attempts`、`attempt_meter_snapshots`、`attempt_meter_bound_lines`；`meter_attempt_*` | 当前正例由真实 `RecordAttempt` 出生并验证四条 attempt bounds 与 parent plan/quote 事实 | `TestUnifiedTextMeteringCurrentConcurrentAttemptAndFinalizationIdentity` 验证同一 `(request,sequence)` 并发只保留一个 attempt；`TestUnifiedTextMeteringAttemptQuoteBindingAndSendingGuards`、snapshot tenant/immutable 测试覆盖跨 request/tenant、错误 quote、未知成本和回滚 | 同上；payment worker 没有计量表权限 |
| observation | `metering_events`、`meter_event_quantities`；`meter_event_*` | 当前正例由真实 `RecordFinalization` 产生两条 canonical quantity | `TestUnifiedTextMeteringGuardMatrix` 覆盖来源闭集、错误 attempt、跨 request/tenant、重复 source identity、quantity/event 不可变；`TestUnifiedTextMeteringCurrentConcurrentAttemptAndFinalizationIdentity` 验证并发 finalization/observation 唯一性；错误事务回滚 | 观察事实属于计量表，权限由 role 矩阵覆盖 |
| finalization | `finalization_records`、`settlements`；`meter_finalization_complete`、`meter_settlement_*` | 当前正例验证 finalization 与唯一 settlement 均由 billing 生命周期写入 | `TestUnifiedTextMeteringFinalizationPointerAndEventIdentityGuards`、`TestUnifiedTextMeteringAmountAndSettlementGuards` 覆盖错误 finalization pointer、跨 request、金额不匹配、重复结算与 rollback；finalization 不可变 | APP/projector/worker/PUBLIC 边界由 role 矩阵和实际 LOGIN 测试覆盖 |
| procurement | `procurement_reservations`、`procurement_budget_events`、`procurement_cost_proposals`；`meter_procurement_*` | `TestUnifiedTextMeteringCurrentManagedProcurementGuardFacts` 真实发布 cost、绑定 procurement account、Reserve→RecordAttempt，验证 prepared reservation 和原 supplier upper amount | 同一测试覆盖 reservation identity/amount/history/cross-request 改写拒绝；`TestUnifiedTextMeteringAttemptQuoteBindingAndSendingGuards`、supplier correction/procurement tests 覆盖 quote stale、来源指针互斥、proposal/evidence 与失败回滚；受管采购并发 account/attempt 负控见 billing PG 专项 | procurement 表仍不授予 projector/PUBLIC/payment worker；计量函数执行权只给 APP |

schema、capability、route、retail、supplier 是平台级发布或模型域事实，没有租户列；对它们人为构造“跨租户”不是有效验收场景，索引明确记录为 N/A。request、attempt、observation、finalization、procurement 的租户和 request/attempt 绑定均通过真实行与 deferred guard 检查。

`TestUnifiedTextMeteringCurrentGuardDDLIndex` 还直接查询 `pg_trigger`，确认十类 guard 在 current-only 安装中存在且为 `DEFERRABLE INITIALLY DEFERRED`，同时确认 `schema_migrations` 只有 `current_mvp.sql` 与当前追加迁移。它是迁移测试索引，不替代每个业务事实的正反测试。

## 实际命令

current-only guard 矩阵及已有负控、canonical、角色测试：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
GOCACHE=/tmp/moni-aigate-guard-gocache \
go test -count=1 -race -timeout 900s ./migrations \
  -run 'TestUnifiedTextMeteringCurrent|TestUnifiedTextMetering(Guard|Amount|Schema|Finalization|Concurrent|Attempt|Snapshot|Supplier|Role)|TestMeterCompletenessRejectsGaps|TestUnifiedTextMeteringCanonical'
```

本片新增 current-only 六项专项均已通过：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
GOCACHE=/tmp/moni-aigate-guard-gocache \
go test -count=1 -race -timeout 900s ./migrations \
  -run '^TestUnifiedTextMeteringCurrentGuardDDLIndex$|^TestUnifiedTextMeteringCurrentPositiveGuardFacts$|^TestUnifiedTextMeteringCurrentImmutablePriceCapabilityAndAttemptFacts$|^TestUnifiedTextMeteringCurrentConcurrentAttemptAndFinalizationIdentity$|^TestUnifiedTextMeteringCurrentManagedProcurementGuardFacts$|^TestUnifiedTextMeteringCurrentActualLoginMeterPermissions$'
```

独立实际 LOGIN 角色的完整 APP/projector/payment-worker 计量权限测试仍使用已有 operations 测试入口：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
go test -count=1 -race -timeout 300s ./internal/operations \
  -run '^TestMeterRoleBoundaryWithActualLoginConnections$'
```

这些命令只证明当前 PostgreSQL guard、权限和失败边界；不把完整六文本商业矩阵、真实供应商 usage/cost、浏览器或生产部署认证标记为完成。

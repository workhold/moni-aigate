# P1-CAT-02 模型别名实现验证

状态：当前专项通过（2026-11-14）。该记录覆盖 030-A 的 current-only 迁移、管理 HTTP、授权解析、请求原始模型名和目录投影，不宣称完整第一阶段或真实供应商验收完成。

## 本提交范围

- 迁移：`current_002_phase1_aliases.sql` 的名字空间、退役终态、operation、audit、请求原始模型名和当前角色权限。
- Server：授权解析、`/v1/models`、公开目录的活跃别名投影、平台 admin 列出/创建/退役 API。
- Billing：Reserve 将请求原始模型名写入 `request_records.requested_model_name`，并保留解析后的逻辑模型计量身份。
- UI：平台模型页登记/退役，客户模型页显示活跃别名。
- 明确未覆盖：隔离浏览器桌面/390px、完整 029 清单和真实供应商。

## 本次证据

数据库：`127.0.0.1:55439/moni_test`，不记录凭据。

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 别名/倍率/目录汇总专项 | `TEST_DATABASE_URL=… go test -race -p 1 -count=1 ./internal/catalog ./internal/server -run '^(TestPhase1GroupMultiplierCompilesImmutablePrices\|TestPhase1ModelAliasOperationLifecycle\|TestGroupPriceVector.*\|TestTenantPricing.*\|TestModelAlias.*\|TestAlias.*\|TestPublicCatalogProjectionProtocolsPricesAndSecrets\|TestPublicCatalogHTTPPublicationCASAndAnonymousWhitelist)$'` | PASS |
| 别名 HTTP 与商业链 | `TestModelAliasHTTPGatewayAuthorizationAndOriginalName`（包含在上述 server 专项中） | PASS：非 admin 403；admin 创建/幂等/列表；`/v1/models` 同时列公开名和别名；响应回显别名；`requested_model_name` 保存别名而 `model_id` 保持逻辑模型；Key scope 收缩和别名退役均不再派发 |
| 公开目录 | `TestPublicCatalogHTTPPublicationCASAndAnonymousWhitelist` | PASS：`aliases` 白名单与活跃别名内容均验证 |
| 前端 | `npm --prefix web test` | PASS |
| 前端类型检查与构建 | `npm --prefix web run build`（先执行 `vue-tsc --noEmit`） | PASS |
| 浏览器桌面/390px | 未执行 | SKIP，不记为通过 |

别名与公开名冲突、重复 operation、退役复用、目标未发布/未启用等数据库边界由 `TestPhase1ModelAliasOperationLifecycle` 和 catalog 汇总专项覆盖。当前授权失败仍使用既有 `model_forbidden` 响应，未单独承诺 030 草案中的退役 404 文案；完整六协议别名调用和浏览器页面验收不在本专项 PASS 范围内。

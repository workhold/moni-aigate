# P1-CAT-02 模型别名实现验证草稿

状态：待复验。该记录覆盖 030-A 当前迁移和代码实现，不宣称第一阶段商业验收完成。

## 本提交范围

- 迁移：`current_002_phase1_aliases.sql` 的名字空间、退役终态、operation、audit、请求原始模型名和当前角色权限。
- Server：授权解析、`/v1/models`、客户/公开目录的活跃别名投影、平台 admin 列出/创建/退役 API。
- Billing：Reserve 将请求原始模型名写入 `request_records.requested_model_name`，并保留解析后的逻辑模型计量身份。
- UI：平台模型页登记/退役，客户模型页显示活跃别名。
- 明确未覆盖：浏览器、六种文本协议完整商业链、真实供应商和完整 029 验收。

## 当前证据

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| Go 纯域与 server 专项 | `go test ./internal/catalog ./internal/server` | PASS |
| 前端类型检查 | `npm run typecheck`（需先在 `web/` 安装 `package-lock.json` 依赖） | 未执行：当前 worktree 没有 `node_modules` |
| 专用 PostgreSQL | `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -count=1 -timeout 240s ./internal/catalog -run 'TestPhase1ModelAliasOperationLifecycle'` | PASS |
| 浏览器桌面/390px | 需在集成迁移和隔离浏览器环境完成 | SKIP |

集成方仍需要在专用 PostgreSQL 上补验：别名与公开名冲突、重复/退役复用、并发同名创建、目标模型未发布/未启用、授权不越权、退役后 404、在途请求收尾，以及三类目录输出。响应 `model` 与请求别名的合同还需沿六种文本协议逐项复验。

# P1-CAT-02 模型别名外围实现验证草稿

状态：待复验。该记录只覆盖 030-A 外围实现，不宣称第一阶段商业验收完成。

## 本提交范围

- 纯域：别名名称边界、active/retired 解析接缝、共享逻辑模型 ID。
- Server：授权解析接缝、`/v1/models`、客户/公开目录的活跃别名投影、平台 admin 列出/创建/退役 API。
- UI：平台模型页登记/退役，客户模型页显示活跃别名。
- 明确未覆盖：正式 SQL 迁移、`request_records.requested_model_name` 的 billing 写入、分组倍率、统一计量、公共钱包和真实 PostgreSQL 别名表验收。

## 当前证据

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| Go 纯域与 server 专项 | `go test ./internal/catalog ./internal/server` | PASS |
| 前端类型检查 | `npm run typecheck`（需先在 `web/` 安装 `package-lock.json` 依赖） | 未执行：当前 worktree 没有 `node_modules` |
| 专用 PostgreSQL | 030 正式迁移尚未安装 | SKIP，不计商业通过 |
| 浏览器桌面/390px | 需在集成迁移和隔离浏览器环境完成 | SKIP |

正式迁移安装后，集成方需要在专用 PostgreSQL 上补验：别名与公开名冲突、重复/退役复用、并发同名创建、目标模型未发布/未启用、授权不越权、退役后 404、在途请求收尾，以及三类目录输出。响应 `model` 与请求别名的合同还需沿六种文本协议逐项复验。

# 当前 MVP 快速验收入口

`make verify-current-mvp` 是当前统一架构的显式验收入口。它固定使用：

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  make verify-current-mvp
```

未设置 `TEST_DATABASE_URL` 时脚本使用上述默认值；传入 5432、容器内数据库名或其他账号会立即失败。数据库组全部实际连接 PostgreSQL，不会因为环境变量缺失而静默跳过。

入口按顺序执行并打印每组命令、PASS/FAIL/SKIP 和耗时：

| 分组 | 当前范围 |
| --- | --- |
| 结构检查 | 当前架构和采购布局的只读检查 |
| current-only 安装 | 空库安装、`VerifyCurrent`、追加迁移、历史库/非空库拒迁 |
| 权限与拒迁 | 实际 LOGIN 角色、APP/worker/payment 边界、历史业务事实拒绝 |
| 028-U guard | canonical、schema/capability、quote、attempt、observation、finalization、supplier projection、并发、回滚和权限专项 |
| wallet/billing/settlement | 手动充值双审、钱包来源、预占、结算、退款、媒体钱包和回滚 |
| 六种文本 | Chat、Responses、Messages、Embeddings、Rerank、Gemini 的统一报价、采购、计量、结算和幂等链路 |
| provider | DeepSeek 缓存字段、流式/非流式 usage、未知量和超时边界 |
| Config → Server wiring | provider/config/catalog/server race/compile，以及 Config 到 provider timeout 的静态接线审计；接线缺失会明确 FAIL |
| 媒体与实时 | 图片、TTS、异步 video task、单 turn Realtime 的当前 PostgreSQL 链路及状态机安全测试 |
| Go vet / 前端 | 当前核心包 `go vet`、前端测试和生产构建 |
| Compose | Docker daemon 可用时执行 current migration、provision、bootstrap、app/worker/数据库重启及 business-qa mock smoke |

该入口不会默认调用 `go test ./...`，也不会执行 `internal/maintenancescan`、`internal/maintenancetrust`、`internal/fundinglot*` 等历史 maintenance fixture。历史材料仍可由各自专项命令单独运行，但其结果不进入当前 MVP 入口。

Compose business-qa 使用确定性的 mock provider，结果只能证明开发环境的接口、权限、计量和恢复工作流；没有真实供应商 usage、成本或生产认证。Docker daemon 或 Compose 命令缺失时，Compose 组明确输出 `SKIP`，不把缺失环境写成通过。其他数据库组不会因此跳过。

前端依赖缺失时入口先执行一次 `npm --prefix web ci`；安装失败会使依赖、测试和构建组分别保留失败结果。provider timeout 接线审计要求配置层具备 connect/header/first-event/idle/progress 五项边界，并由 Server 传入 provider client；当前若仍使用空 `provider.ClientOptions{}`，审计会失败。真实 reasoning 输出/供应商 profile 尚未认证，usage 子集测试不能替代 reasoning 协议验收。

失败组的日志保留在临时目录；设置 `VERIFY_CURRENT_MVP_LOG_DIR=/path/to/logs` 可指定持久目录。成功后默认清理临时日志。退出码为 0 表示执行的组全部通过（允许明确的 Compose 环境 SKIP），非 0 表示至少一个实际执行的组失败。

该脚本只增加验收编排，不增加运行时路径、数据库对象、provider 或媒体能力；完整 Goal 状态仍以各能力的专项证据和真实供应商边界为准。

# 当前 MVP 验收运行手册

在仓库根目录执行：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  make verify-current-mvp
```

运行前确认 `moni_test` 是专用开发/验收数据库，账号具有测试所需的建库或隔离 schema 权限。入口严格拒绝其他数据库 URL，不要使用生产连接或 5432。

入口会先做只读架构检查，然后串行执行 current-only migration/`VerifyCurrent`、拒迁与角色权限、028-U guard、钱包/充值/账务/结算、六文本商业链路、provider usage、图片/TTS/video task/Realtime、核心 `go vet`、前端测试和前端构建。数据库测试使用 `-race -p 1 -count=1`，避免共享专库中的安装与角色 fixture 并发互相污染。每组会显示实际命令和秒数，不能用历史专项的旧记录替代本次结果。

入口还会显式执行 provider/config/catalog/server 的 race/compile 专项，并审计 Config 到 Server 的 provider connect/header/first-event/idle/progress timeout 接线。当前只在 provider 内部提供默认 timeout、而配置没有这些字段或 `server.New` 仍传空 `provider.ClientOptions{}` 时，该组必须失败；这表示真实能力未完成，不能把默认值当作“可配置”。

这个入口刻意不运行整仓 `go test ./...`，因此不会把历史 maintenance reader、funding-lot 研究材料或兼容 fixture 混入当前验收。需要历史材料回归时，执行对应专项手册中的命令，并在验收报告中单独标记。

若本机同时有 Docker daemon 和 `docker compose`/`docker-compose`，入口还会执行：

```sh
DEPLOY_QA_BUSINESS=true scripts/deployment-qa.sh
```

该 smoke 使用临时项目、临时卷和 deterministic mock provider，覆盖空库迁移、重复 migrate、重复 provision、首个/重复 bootstrap、live/ready、文本、图片、TTS、异步 task、Realtime，以及 app/worker/PostgreSQL 重启恢复。它不是真实供应商、浏览器、1Panel 或生产验收。Docker 环境不具备时，入口只将 Compose 组标记为 `SKIP`，其余当前 PostgreSQL 组仍必须执行。

如果 `web/node_modules/.bin/vue-tsc` 不存在，入口会先执行 `npm --prefix web ci`。依赖安装、前端测试和构建都是独立分组；任意一步失败都会出现在汇总中，不能把缺少依赖当作测试通过。

失败时可设置 `VERIFY_CURRENT_MVP_LOG_DIR` 保存每组日志：

```sh
VERIFY_CURRENT_MVP_LOG_DIR=/tmp/moni-aigate-current-mvp-logs \
  TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  make verify-current-mvp
```

脚本退出码为 0 才能记录为当前入口通过；任何数据库组失败、前端失败、构建失败或 Compose 已具备但 smoke 失败，都必须保留失败原因和耗时，不能通过删减测试或改用 mock 数据库规避。

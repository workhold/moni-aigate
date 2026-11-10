# 浏览器等价内测 Smoke 证据

日期：2026-11-10。本文记录当前 worktree 的真实 HTTP/WS 内测证据，不把进程健康检查、纯 handler fixture 或 mock provider 健康检查当成业务通过。

当前 Compose 没有名为 `inner-test` 的 profile。等价边界是：默认 Compose 不启用 `business-qa`，因此不启动 `qa-provider`，也不创建客户、价格、钱包、任务引用或媒体商业事实；需要一次性内测数据时，才显式使用 `--profile business-qa`。该 profile 只加入仓库内的隔离 Python mock 上游。媒体 HTTP 仍要求客户 API Key 和已发布的数据库 profile；图片、TTS、task、Realtime 不会因为 PostgreSQL 或 provider health 返回 200 就被记为通过。

当前入口已核对为：管理会话 `POST /api/auth/login`、`GET /api/me`、`GET /api/platform/models`；客户 API Key `GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/images/generations`、`POST /v1/audio/speech`、`POST/GET /v1/tasks...`；Realtime 为 `GET /v1/realtime` 后的 WebSocket。管理会话的非 GET 请求由 Origin 和 `X-CSRF-Token` 共同保护，客户入口只接受唯一 Bearer API Key。`deployment-business-qa.sh` 现在会实际验证这些边界，并严格检查文本内容、PNG 签名/IHDR/尺寸、WAV RIFF/WAVE/fmt/data/长度以及 task 下载的媒体类型和非空正文。

已交付的证据如下：

| 范围 | 命令 | 结果 |
| --- | --- | --- |
| QA 脚本和 WS 客户端自测 | `bash scripts/deployment-business-qa.test.sh` | PASS；包含缺少输入的显式 SKIP、`FAIL_ON_SKIP`、无依赖 WS 握手和事件顺序 |
| 真实二进制 HTTP 安全 smoke | `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' BROWSER_QA_HTTP_SMOKE=1 go test -race ./scripts/browser-qa -run '^TestBrowserQAHTTPSecuritySmoke$' -count=1 -v -timeout 120s` | PASS；13.3s；随机 schema/APP/projector 角色，两个租户，客户 A 的 request 对租户 B 返回 404，缺 CSRF 和错误 Origin 均返回 403 |
| Realtime HTTP/WS 数据库集成 | `TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' go test -race ./internal/server -run '^TestRealtimeHTTPPostgresOneSessionOneTurn$' -count=1 -timeout 120s` | PASS；5.4s；实际 upgrade、session/turn 事件、重复 charge-bearing event 不重发和媒体结算状态均核对 |
| 临时 Compose 业务 smoke | `DEPLOY_QA_PORT=55442 DEPLOY_QA_BUSINESS=true bash scripts/deployment-qa.sh` | PASS；临时卷清理前实际通过 live/ready、管理登录、CSRF/Origin 负控、客户 Key 负控、文本、PNG 图片、WAV TTS、task 制品下载、Realtime WS、app/worker 重启和 PostgreSQL 重启恢复 |

临时 Compose 使用随机密码、主密钥、认证密钥、项目名和 PostgreSQL 卷；结果退出时执行 `down --volumes --remove-orphans`。输出只保留固定状态、HTTP code、计数和测试错误码，不归档客户 Key、会话 Cookie、CSRF 值、密码、provider secret 或响应正文。`55439` 只用于 opt-in browser-qa 专库，Compose 使用独立容器 PostgreSQL，不共享该专库。

脚本单独运行时，缺少客户 Key、媒体请求体、task 原引用或 Realtime URL 会输出 `SKIP` 并以退出码 3 结束；`BUSINESS_QA_FAIL_ON_SKIP=true` 会把任一 SKIP 变为失败。配置了上游但得到 provider-unavailable 时仍是 SKIP，其他配置错误失败。这样不会把未运行的能力合并进“全部通过”。

仍未覆盖的边界：没有真实浏览器 UI 自动化；没有正式供应商、对象存储、真实 TLS 反代、1Panel 或生产域名验收；Compose business profile 使用确定性的本地 mock provider，task/Realtime 也未证明真实供应商的持久句柄恢复。临时 Compose provisioning 只建立一个客户租户，因此 Compose smoke 的跨租户管理会话证据由上述 `55439` 二租户 HTTP smoke 提供；该 smoke 没有声称验证真实供应商或生产商业出口。

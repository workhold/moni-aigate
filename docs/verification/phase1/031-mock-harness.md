# 031 deterministic mock 部署验收 harness

状态：`PASS`（2026-11-14，disposable isolated QA deployment；全部为 mock 证据，不是供应商认证）。

这个 harness 从已运行的 app/worker 消费 HTTP 入口，使用专用 PostgreSQL `127.0.0.1:55439/moni_test` 做只读事实断言。它不会调用 `deployment-qa.sh`，不会启动、停止或重启现有 app、worker、Compose 服务，也不会执行 `go test ./...`。需要临时上游时，它只在 app 容器已经存在的 Docker network 上启动一个临时 mock 容器，退出时删除该容器；mock 配置启用 `a=reject_429`、`b=normal` 两个候选。

## 执行入口

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PHASE1_BASE_URL='http://127.0.0.1:18081' \
PHASE1_APP_CONTAINER='phase0qa-app' \
PHASE1_WORKER_CONTAINER='phase0qa-worker' \
PHASE1_ADMIN_EMAIL='admin@example.com' \
PHASE1_ADMIN_PASSWORD='…' \
PHASE1_MOCK_IMAGE='moni-aigate:phase0-qa' \
PHASE1_DATABASE_SCHEMA='隔离 schema 名（仅非 public QA 需要）' \
scripts/phase1-mock-acceptance.sh
```

`PHASE1_MOCK_CONFIG_DIR` 可指定 Docker 可共享的临时配置目录，默认仓库根目录；这是 macOS Docker Desktop bind-mount 的边界，不能把 `/var/folders` 中不可共享的路径挂载成文件。`PHASE1_DATABASE_SCHEMA` 只用于隔离 QA schema 的只读断言，默认仍是 public current-MVP schema。脚本不把密码写入输出或证据文件。

## 执行内容与断言

通过前置检查后，脚本通过现有管理 HTTP API 创建一次性模型、双候选渠道、路由、客户价格、供应商成本、租户、双审入账和 API Key。每个模型和渠道名带唯一 run ID，避免把旧事实误认成本次结果。定价写入按服务端 actor/global 限值主动 pacing，不重试被拒绝的资金操作。

它执行六种文本协议：Chat、Responses、Messages、Embeddings、Rerank、Gemini；生成协议覆盖 JSON 与 SSE。候选 `a` 在发送前返回带 `Retry-After: 1` 的 429，候选 `b` 正常；每个成功请求断言恰好 a→b 两个 receipt、两个 attempt 和一个 settlement。`Retry-After` 只缩短 deterministic fixture 的显式冷却，不改变网关的 cooldown、最多两次 attempt 或 safe-to-retry 规则。

两个负向场景：

- `drop_after_headers`：HTTP 502，协议失败、预占释放，只有一个 attempt 和一个 receipt，不跨候选重放。
- `missing_usage`：HTTP 503，协议完成但 `billing_status=pending_reconciliation`，settlement=0，只有一个 attempt 和一个 receipt；协议成功与收费成功不混为一谈。

每个请求通过 `X-Request-ID` 与 mock `nonce` 关联，再用 psql 只读检查：成功请求恰有一个 settlement；未知消费不进入 `settled`；`unknown` 成本无金额，`contract`/`invoice` 成本必须带金额。熔断后缺 usage 可以落在另一健康候选，但同样不得产生第二次上游接收。

## 本次结果

```json
{"status":"passed","run_id":"phase1-acceptance-a6dd2af575c7","requests":12,"unknown_http_status":502,"missing_usage_http_status":503,"database":"127.0.0.1:55439/moni_test","mock":true}
```

前置检查同时返回 `preflight_passed`。库与 CLI 静态检查 `go test -race ./internal/testsupport/mockupstream ./scripts/mock-upstream` 和 `scripts/phase1-mock-acceptance.test.sh` 均通过。完整 Compose business-qa 另见[0 阶段外部上游验证记录](../phase0-external-upstream.md)。真实 New API/Sub2API 服务、凭据与供应商资格仍未验收。

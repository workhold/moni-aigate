# 031 deterministic mock 部署验收 harness

状态：`SKIP`（本次环境前置不满足，未产生商业验收 PASS）。

这个 harness 从已运行的 app/worker 消费 HTTP 入口，使用专用 PostgreSQL `127.0.0.1:55439/moni_test` 做只读事实断言。它不会调用 `deployment-qa.sh`，不会启动、停止或重启现有 app、worker、Compose 服务，也不会执行 `go test ./...`。需要临时上游时，它只在 app 容器已经存在的 Docker network 上启动一个临时 mock 容器，退出时删除该容器；mock 配置启用 `a=reject_429`、`b=normal` 两个候选。

## 执行入口

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PHASE1_BASE_URL='http://127.0.0.1:18080' \
PHASE1_APP_CONTAINER='moni-aigate-app-1' \
PHASE1_WORKER_CONTAINER='moni-aigate-worker-1' \
PHASE1_ADMIN_EMAIL='admin@example.com' \
PHASE1_ADMIN_PASSWORD='…' \
scripts/phase1-mock-acceptance.sh
```

先做只读前置检查：

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PHASE1_APP_CONTAINER='moni-aigate-app-1' \
PHASE1_WORKER_CONTAINER='moni-aigate-worker-1' \
PHASE1_ADMIN_EMAIL='admin@example.com' \
PHASE1_ADMIN_PASSWORD='…' \
scripts/phase1-mock-acceptance.sh --preflight
```

`PHASE1_MOCK_IMAGE` 可指定包含 `/usr/local/bin/mock-upstream` 的已构建镜像，默认是 `moni-aigate:local`。脚本不把密码写入输出或证据文件。

## 执行内容与断言

通过前置检查后，脚本通过现有管理 HTTP API 创建一次性模型、双候选渠道、路由、客户价格、供应商成本、租户、双审入账和 API Key。每个模型和渠道名带唯一 run ID，避免把旧事实误认成本次结果。

它执行六种文本协议：Chat、Responses、Messages、Embeddings、Rerank、Gemini；生成协议覆盖 JSON 与 SSE。候选 `a` 在发送前返回 429，候选 `b` 正常，断言明确无消费拒绝发生一次 failover。另以 `drop_after_headers` 触发可能已消费请求，断言只有一个 attempt、一个 nonce receipt 且不发生第二次派发。

每个请求都通过 `X-Request-ID` 与 mock `nonce` 关联，然后用 psql 只读检查：

- 成功 failover 请求有两个 attempt、恰有一个 settlement，且每个 attempt 都有显式成本投影；
- 可能已消费请求最多一个 settlement、只有一个 attempt，不得进入成功结算；
- `unknown` 成本不能带金额，`contract`/`invoice` 成本必须带金额；
- receipts 的候选、nonce 和次数与数据库 attempt 数一致。

脚本返回码为 `0=PASS`、`1=FAIL`、`3=SKIP`。`SKIP` 不计为第一阶段通过。

## 本次实况

基线为集成分支提交 `7b995d2`；本 harness 在独立分支 `codex/phase1-mock-acceptance` 的独立 worktree 中开发。本次只执行了静态 self-test 和 `--preflight`，没有创建模型、渠道、租户、Key、入账或请求，也没有执行任何数据库写入。

静态检查通过：

```text
phase1 mock acceptance harness static self-test passed
```

部署前置实际返回：

```text
SKIP: container moni-aigate-app-1 DATABASE_URL is not the dedicated 55439/moni_test database
```

当前运行中的 app/worker 使用 Compose 内部 `postgres:5432/moni`，不是本验收要求的 `127.0.0.1:55439/moni_test`；当前也没有把 `qa-provider` 作为运行中的现有服务使用。依据 029 的规则，本次没有把 HTTP、六协议、failover、receipt 或账务断言写成 PASS。重新执行前，必须由外部部署流程提供连接专用 55439 数据库的已运行 app/worker，并通过同一前置检查。

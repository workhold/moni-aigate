# 031：确定性多协议、多候选 mock 上游

2026-11-10；状态：草案，待开发者评审后冻结。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#3-第一阶段固定版本-new-api-核心业务能力对齐)验收前置；第二、三阶段继续复用。解除 [029](../029-phase1-acceptance/spec.md) 的 P1-RT-01..04 与 P1-PT-02..06。

## 1. 问题

第一阶段允许用 deterministic mock 验证路由、计量、钱包、结算和错误恢复，但现在的部署 mock 做不到：

- `scripts/deployment-qa-provider.py` 只处理 `POST /v1/chat/completions`，固定 usage 4/2/6，没有故障注入。
- Compose business-qa 只创建一个渠道 `qa-compose-provider`、一个绑定 `qa-chat`，无法验证多候选选路、成本优先或故障切换。
- Responses、Messages、Gemini、Embeddings、Rerank 只在 Go 集成测试里用 httptest 夹具验证，不能在部署形态上验收。
- 没有办法从外部断言“某个请求在上游只被接收一次”，而这是“未知消费不重放”的直接证据。

## 2. 范围

做：一个独立的 HTTP 服务，按协议实现六种文本上游的 JSON 与 SSE 形态，支持一个进程内多个“候选”身份、确定性 usage、可配置延迟与故障，以及接收记录查询。Go 集成测试与 Compose business-qa 共用同一实现。

不做：媒体、任务和 Realtime（它们已有仓库内 deterministic adapter，见[媒体供应商安全](../../docs/design/media-provider-safety.md)）；模拟任何真实供应商的计费规则；生成有意义的模型输出。

## 3. 组件与位置

| 组件 | 位置 | 用途 |
|---|---|---|
| 库 | `internal/testsupport/mockupstream` | 协议处理、候选配置、故障、接收日志；Go 测试通过 `httptest.Server` 直接使用 |
| 可执行程序 | `scripts/mock-upstream/main.go` | Compose business-qa 使用；与 `scripts/provider-qualification` 同样的瘦入口 |
| Compose 服务 | `compose.yaml` 中替换 `qa-provider`，仍只在 `business-qa` profile | 不进入默认部署 |

替换完成且 business-qa 全部通过后，删除 `scripts/deployment-qa-provider.py`，不保留两套 mock（ADR-005）。

## 4. 候选身份

一个进程承载多个候选，用路径前缀区分：

```text
http://mock-upstream:8090/c/{candidate}/v1/chat/completions
http://mock-upstream:8090/c/{candidate}/v1/responses
http://mock-upstream:8090/c/{candidate}/v1/messages
http://mock-upstream:8090/c/{candidate}/v1/embeddings
http://mock-upstream:8090/c/{candidate}/v1/rerank
http://mock-upstream:8090/c/{candidate}/v1beta/models/{model}:generateContent
http://mock-upstream:8090/c/{candidate}/v1beta/models/{model}:streamGenerateContent?alt=sse
```

每个渠道版本的 `base_url` 指向一个候选前缀，例如 `http://mock-upstream:8090/c/a/v1`。候选配置来自启动时读取的 JSON 文件，运行中不可修改：

```json
{
  "candidates": {
    "a": {"secret": "...", "models": ["qa-text"], "ttfb_ms": 20, "event_interval_ms": 5,
          "output_tokens": 8, "default_fault": null},
    "b": {"secret": "...", "models": ["qa-text"], "ttfb_ms": 80, "event_interval_ms": 5,
          "output_tokens": 8, "default_fault": "reject_429"}
  }
}
```

鉴权按协议检查：OpenAI 风格 `Authorization: Bearer`，Messages 用 `x-api-key`，Gemini 用 `x-goog-api-key`。秘密不匹配返回 401，用来验证凭据隔离。

## 5. 确定性输出

- 文本内容固定为 `mock:{candidate}:{seq}`，测试可据此判断由哪个候选服务。
- 输入 usage = 请求中全部文本字段 UTF-8 字节数之和除以 4 向上取整，最少 1；这是确定性公式，不模拟任何分词器。
- 输出 usage = `min(请求的输出上限, 候选 output_tokens)`。
- Embeddings 返回固定维度的确定性向量；Rerank 按文档顺序返回递减分数。
- 流式按协议发送合法事件序列与终态：Chat 的 `[DONE]`、Responses 与 Messages 的原生事件名、Gemini 的 SSE 尾部 usage。
- 每个响应带 `X-Moni-Mock: 1` 头。

## 6. 故障注入

故障可以来自候选的 `default_fault`，也可以由请求正文中的指令触发。指令格式为文本里出现 `[[mock:fault=<name>]]`。指令只在程序以 `--allow-directives` 启动时生效。网关不转发客户端自定义 header，所以必须用正文传递。

| 故障 | 上游行为 | 期望网关分类 | 可否换候选 |
|---|---|---|---|
| `reject_429` | 发送前返回 429，状态码属于 profile 的 `no_consumption_statuses` | rejected_no_consumption | 可以 |
| `reject_429_unlisted` | 返回 429，profile 未登记为无消费 | unknown | 不可以 |
| `http_500` | 返回 500 | unknown | 不可以 |
| `drop_after_headers` | 发送响应头后断开 | unknown | 不可以 |
| `drop_after_events:N` | 发送 N 个事件后断开 | unknown；若已向下游写出则已提交 | 不可以 |
| `stall_first_event` | 不发送首个事件 | timeout | 不可以 |
| `missing_usage` | 正常结束但不给 usage | 待核对 | — |
| `conflicting_usage` | 两个不一致的最终 usage | 待核对 | — |
| `error_after_usage` | 先给 usage，再给流内错误 | 失败，不收费为成功 | 不可以 |
| `reasoning_events` | Responses 发送未冻结的 reasoning 事件 | 协议错误，unknown | 不可以 |
| `invalid_json` | 返回不完整 JSON | 协议错误 | 不可以 |
| `slow_ttfb:MS` | 首字节延迟 MS 毫秒 | 正常；供第二、三阶段延迟测量 | — |

连接拒绝（not_sent）不由 mock 实现：给渠道配置一个没有监听的端口即可。

## 7. 接收记录

```text
GET /__mock/receipts?since=<seq>
```

返回自 `since` 之后的接收记录：`seq`、`candidate`、`protocol`、`model`、`stream`、`body_sha256`、`nonce`、`fault`、`received_at`、`completed`。`nonce` 取自正文中的 `[[mock:nonce=<value>]]`，测试用它把网关请求和上游接收对应起来。

该接口只在 `--allow-directives` 下开放，只监听容器内网，不暴露到宿主机端口。

## 8. 安全

- 程序启动要求环境变量 `MOCK_UPSTREAM_ENABLE=1`，否则退出。
- Compose 中只在 `business-qa` profile 启动；默认部署不包含。
- business-qa 创建的渠道名一律以 `mock-` 开头。所有证据文件必须写明使用 mock。
- 文本链路目前没有 `provider_mode` 标记（媒体有）。是否给 `channel_versions` 增加 mock 标记属于决策点 D-031-1，见 §10。

## 9. 使用 mock 的验收场景

| 场景 | 配置 | 断言 |
|---|---|---|
| 同优先级权重 | a、b 同 priority，权重 3:1 | 大量请求的分布在统计误差内；每个请求只有一个 attempt |
| 优先级降级 | a 高优先级且容量满 | 请求落到 b；a 的决策原因为 `capacity_full` |
| 成本优先 | a、b 同优先级，b 采购单价更低 | 全部落到 b；a 的原因为 `higher_cost` |
| 未知成本拒绝 | b 无报价，策略 `deny` | b 被排除，原因 `cost_unknown` |
| 无消费换候选 | a 为 `reject_429` | 第二个 attempt 在 b 成功；客户只收费一次；a 的采购成本为已知 0 |
| 未知消费不重放 | a 为 `drop_after_headers` | 只有一个 attempt；`/__mock/receipts` 中该 nonce 恰好 1 条；预占进入待核对或释放 |
| 已提交不切换 | a 为 `drop_after_events:2`，流式 | 不发起第二个 attempt |
| 熔断 | a 连续 3 次 `http_500` | a 进入 open；30 秒内新请求不选 a |
| 凭据错误 | a 的秘密不匹配 | 隔离凭据，不隔离整个渠道 |
| 六协议正常路径 | 各协议 JSON/SSE | 计量、预占、结算、报表、CSV 一致 |
| 缺 usage | `missing_usage` | 不按估算收费，进入待核对 |

## 10. 决策点

- **D-031-1**：是否给 `channel_versions` 增加 `provider_mode`（`real`/`mock`），让网关在 `inner-test` 配置下拒绝 mock 渠道、并在请求事实中标记。建议：增加，与媒体的 `provider_mode` 对齐；默认 `real`，business-qa 创建 `mock`。
- **D-031-2**：输入 usage 公式是否需要贴近某个分词器。建议：不需要；mock 只验证链路，不验证计量精度。

## 11. 验收

- 库的单元测试覆盖每个协议的 JSON 与 SSE 正常路径和 §6 每种故障。
- 现有 provider 解析器能无误解析 mock 的每个正常响应（用 provider 包实际解析，不是只比对字符串）。
- Compose business-qa 用新 mock 跑通 §9 全部场景；旧 Python mock 删除。
- 默认 Compose（无 profile）不启动 mock；未设 `MOCK_UPSTREAM_ENABLE` 时程序退出。

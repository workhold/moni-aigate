# Provider usage 与 timeout 专项验证

本片只覆盖当前 `internal/provider` 的 usage 归一化、上游错误分类、Responses reasoning 事件边界和阶段 timeout。测试使用确定性的本地 HTTP fixture；它们验证 provider 合同和账务入口收到的 usage 状态，不代表任何真实供应商、真实价格或真实账单认证。

## 当前已验证

| 范围 | 证据 |
| --- | --- |
| DeepSeek/OpenAI-compatible cache | `prompt_cache_hit_tokens` 与 `prompt_cache_miss_tokens` 只有在完整、与 `prompt_tokens_details.cached_tokens` 一致且不超 input 的情况下归一化；非流和流最终 usage 都验证，缺一侧或冲突进入未确认状态。 |
| reasoning usage | OpenAI-compatible `completion_tokens_details.reasoning_tokens`、Gemini `thoughtsTokenCount` 按 output 子集校验；非负、不得超过 output，未知非零维度不能被当作零。 |
| 未知收费维度 | 未建模的非零字段、未知 hosted tool 计量和不支持的 Messages 元数据均保持 `conflicting`/协议失败，不能进入 `UsageJSON` 结算证据。 |
| Responses reasoning 输出 | Responses profile/catalog 拒绝尚未冻结的 reasoning 输出模式；运行时收到 `response.reasoning_summary_text.delta` 等未冻结事件时返回 `upstream_unsupported_event`，不产生确认 usage。 |
| 请求级上游错误 | 只有明确的 `model_not_found`、`model_not_available`、`unsupported_model` 设置 binding isolation；`context_length_exceeded` 保留为当前请求错误。 |
| 阶段 timeout | header、first-event、idle、progress 均使用配置值和安全上下限；header timeout 归类为 `ClassTimeout`/`upstream_header_timeout`，流内 first-event、idle、progress 超时分别保留阶段 code。取消、超时和已写请求不会被标记为安全重试。 |

## 实际命令

本片使用的 PostgreSQL 专用库固定为 `postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`，没有使用 5432：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
go test -count=1 -race -timeout 120s ./internal/provider \
  -run 'TestOpenAICompatibleDeepSeekUsageAcrossHTTPModes|TestResponsesReasoningEventIsRejectedAsUnsupported|TestHeaderAndFirstEventTimeout|TestIdleTimeoutIsSeparateFromFirstEventAndProgress|TestRoleHeartbeatCannotExtendProgressOrTotalDeadline'

PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
go test -count=1 -race -timeout 240s ./internal/provider ./internal/config ./internal/catalog
```

专项结果：本次 provider focused run 与 provider/config 最小测试通过。改动前基线曾在 55439 专库通过 provider/config/catalog race；本次改动后的长 catalog run 已启动并确认 provider/config 通过，但按本片要求在 catalog 完成前主动中止，因此不把它记录为本次改动后的全量通过。catalog 现有测试仍使用其随机隔离 schema 和 `migrations.Apply` fixture；没有改迁移、billing、wallet、settlement 或 server。

## 仍未完成

- 没有真实 DeepSeek、OpenAI/Codex、Anthropic 或其他供应商凭据，因此没有宣称真实 HTTP、真实供应商 usage、reasoning/cache 账单或采购成本认证。
- 当前 provider profile 合同以协议和已冻结字段集合表达 OpenAI-compatible cache 映射；新的供应商专属收费字段仍会拒绝，需为每个真实 profile 单独取证后再扩展。
- Responses reasoning 输出协议仍明确关闭；若要开放 reasoning summary/content，需要另行冻结事件集合、公开归一化字段和客户/供应商计量合同。

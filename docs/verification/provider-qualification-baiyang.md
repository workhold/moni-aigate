# baiyang provider qualification

本片只验证本机 CC Switch 中 `baiyang` 配置指向的 OpenAI-compatible 文本端点。凭据从 CC Switch 的本地受保护配置读取，仅进入本地测试进程，不写入仓库。供应商端点为 `https://sub2api.520965.xyz/v1`。

2026-11-10 通过供应商模型列表接口发现 137 个模型。低成本探测选择 `gpt-4o-mini`，通过 qualification 入口完成一次最小非流式 Chat 请求，终态为 `stop`，usage 为输入 12、输出 3、总计 15，结果摘要带有脱敏响应摘要。该请求尚未进入 moni-aigate 的模型目录、路由、钱包、预占或结算链。

代码入口为 [`scripts/provider-qualification`](../../scripts/provider-qualification)。它复用 `internal/provider`，要求 HTTPS、显式模型和 Key，默认最多 8 个输出 token，只在确认最终 usage 时返回通过，并以脱敏 JSON 输出摘要。

```sh
PROVIDER_QUALIFICATION_BASE_URL='https://sub2api.520965.xyz/v1' \
PROVIDER_QUALIFICATION_API_KEY="$BAIYANG_API_KEY" \
PROVIDER_QUALIFICATION_MODEL='gpt-4o-mini' \
go run ./scripts/provider-qualification
```

本片完成的是 OpenAI-compatible Chat 的单模型 qualification probe。流式 Chat、Responses、Messages、Gemini、图片、TTS、异步视频、Realtime、真实供应商成本、平台商业链和生产容量仍需分别验收。

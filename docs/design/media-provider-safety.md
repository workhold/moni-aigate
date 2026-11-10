# 媒体供应商安全边界

## 范围

本合同约束当前 MVP 的图片生成、TTS、`video_generation` 和单 turn Realtime。媒体请求遵守 Profile → Plan → Reserve → Attempt → Observe → Settle 的阶段顺序，与文本共享钱包、journal 和 funding lot，但请求、预占、attempt、观察与结算使用独立的 `media_*` 表；媒体按 profile 解析到单个 adapter，不参与渠道或动态路由（见[四阶段路线 §2.1](phase-roadmap.md#21-文本与媒体的当前结构)）。本合同只决定供应商适配器是否可以被调用，以及 mock 结果如何进入当前账务边界。

## 配置状态

应用和 worker 必须读取相同的两项配置：

| 配置 | 允许值 | 行为 |
| --- | --- | --- |
| `MEDIA_PROVIDER_PROFILE=development` | `MEDIA_DETERMINISTIC_MOCK=true` 才打开 | 允许 deterministic mock，用于开发和自动测试 |
| `MEDIA_PROVIDER_PROFILE=business-qa` | `MEDIA_DETERMINISTIC_MOCK=true` 才打开 | 允许显式 deterministic mock，用于一次性业务 smoke |
| `MEDIA_PROVIDER_PROFILE=inner-test` | mock 开关必须为 `false` | 当前仓库没有真实媒体适配器时，媒体入口关闭并返回 `provider_unavailable` |

缺省配置是 `inner-test` 且 mock 关闭。生产或内测二进制不会因为构造了 mock 类型而自动启用媒体。

## 失败和账务边界

1. 配置不允许或没有与配置匹配的适配器时，入口在 `Reserve` 之前返回 HTTP 503、错误码 `provider_unavailable`。不创建 request、reservation、attempt、observation、settlement、供应商成本或钱包流水。
2. 只有显式 mock 配置可以装配仓库内的 deterministic adapter。HTTP/WS 响应和持久观察必须带 `provider_mode=mock`；mock usage、result 和 cost 不是真实供应商证据。
3. mock 成功结果仍通过当前媒体 settlement 入口形成可审计的释放事实，但 usage 为 `missing`、supplier cost 为 `unknown`、客户收费为零，钱包 `held` 和 `spent` 的净变化为零。mock 不得产生真实供应商成本。
4. mock 适配器返回格式错误、空结果或未发送错误时，按 provider protocol/failed 处理并释放预占；返回 `Sent=true` 或 `Unknown=true` 的错误仍是 `provider_consumption_unknown`，请求不可自动重试。
5. worker 与 app 使用同一配置。媒体关闭时 worker 不领取或提交媒体任务；显式 mock 时才可执行 mock 任务。worker 重启不能重复提交、结算或生成。
6. 真实适配器尚未随本片提供。将来接入时必须声明 `provider_mode=real`，由当前 profile 明确选择；不能让 mock 结果冒充 real，也不能用 fallback 跨模式转换。

## 验收边界

专项测试必须覆盖四类入口在 inner-test 下的关闭和零账务事实、development/business-qa 显式 mock 下的成功标记和零净扣费，以及 provider unknown/失败不重试。测试使用专用 PostgreSQL `127.0.0.1:55439`；mock 证据只表示本地工作流通过，不表示真实供应商、usage 或成本认证。

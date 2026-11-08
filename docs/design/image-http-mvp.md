# 图片生成 HTTP MVP

当前分支增加了一个受限同步图片入口，用于把现有 `internal/media` 生命周期接到 server 的 HTTP 边界。

## 入口

`POST /v1/images/generations` 使用 API Key 和 `Idempotency-Key`。请求体严格遵循 028-S 的闭合 JSON 输入，包含 `version`、`profile_version`、`variant_key`、`prompt`、`size`、`format` 和 `n`。当前内置 profile 只接受 `png`、`n="1"` 和 `256x256`、`512x512`、`1024x1024`。

返回值为单张 inline PNG，`data[0].b64_json` 是标准 padded base64。`GET /v1/images/generations/:id` 只允许原租户、原用户和原 API Key 读取对应结果。

## 边界

相同 API Key 下，相同幂等键和相同规范输入返回原响应，adapter 不会再次生成。相同幂等键绑定不同输入返回冲突。adapter 已报告发送或结果/usage 无法确认时，请求进入 `media_consumption_unknown`，重复请求不会重发生成。

当前实现使用内存中的请求记录和 `provider.MockMediaAdapter`。它没有接入 request、attempt、计量、billing、wallet、settlement、持久化结果或真实供应商，因此只表示 HTTP/media/provider 的 MVP 接缝，不能作为商业收费能力验收。

# 图片生成 HTTP MVP

## 入口和 profile

`POST /v1/images/generations` 使用 API Key 和单值 `Idempotency-Key`。请求体是严格闭合的 JSON，字段固定为 `version`、`profile_version`、`variant_key`、`prompt`、`size`、`format` 和 `n`。当前发行的本地 mock profile 是图片生成、`default` variant、PNG、单张输出，支持 `256x256`、`512x512` 和 `1024x1024`。prompt、请求 JSON、结果 JSON、base64 解码 bytes 和 PNG 几何尺寸均受 `internal/media` 的固定上限约束。

HTTP profile 只负责协议边界。真实商业资格仍由 `media_model_profiles` 的不可变行确认，当前开发 profile 为 `mock-image-generation`、`image_generation`、`11111111-1111-4111-8111-111111111111`。`billing.ReserveMedia` 在同一预占事务读取 rate、最大数量和 profile 版本，HTTP 层不会自行计算价格或创建第二套计量。

## 请求生命周期

图片请求沿当前媒体链路完成一次 `Reserve → Attempt → Observe → Settle`。预占成功后原 attempt 进入 `sending`，adapter 只允许一次提交。结果必须绑定原 profile、variant、请求尺寸、PNG 解码结果、bytes 长度和 SHA-256。只有 adapter usage evidence 确认后才允许结算和写入结果 artifact。客户收入只来自唯一 media settlement，未知、失败、取消或无法确认的结果不会被当成零价成功。

`provider.MockMediaAdapter` 是开发环境的确定性 provider seam。它只返回受界限的 PNG 和显式 `Sent` 状态，不代表真实供应商 usage、成本或可售资格。provider 已发送但结果或 usage 不可确认时，请求进入 `media_consumption_unknown`，后续相同幂等键不会重新生成。

## 幂等和结果访问

同一租户、用户、API Key 和幂等键绑定相同规范输入时，服务返回原响应并且 provider、attempt、settlement 都不重复。输入变化返回 `idempotency_conflict`。有数据库的 server 会先按原租户、用户、Key、operation、幂等键和 request digest 读取已完成 artifact，因此进程缓存清空或进程重启后的 POST 重放不会再次 Reserve。数据库不可用或 artifact 摘要不一致时返回服务不可用。

`GET /v1/images/generations/:id` 同时校验原租户、owner user 和 API Key。结果正文只在结算成功后追加到不可变 `media_result_artifacts`，保存 `application/json` 响应、SHA-256 和 24 小时过期时间。配置了数据库时，持久 artifact 是完成结果的权威来源，过期或缺失时不会使用旧的内存完成缓存。纯无数据库 fixture 只验证协议和 provider 边界，不能作为商业或持久化验收。

## 当前验证边界

图片纯协议和 server 测试覆盖严格输入、profile 绑定、PNG 结果验证、同键重放、同键冲突、跨租户/用户/Key 访问拒绝、未发送失败、已发送未知消费、错误结果和并发安全。55439 专用 PostgreSQL 测试覆盖真实媒体预占、attempt、observation、settlement、wallet 金额和进程缓存清空后的 GET/POST artifact 重放。Compose business profile 使用 deterministic mock provider 做开发环境工作流验收。

正式供应商、真实供应商 usage 和成本、对象存储、图片编辑、浏览器业务验收、1Panel、HA 和生产发布仍属于后续门禁。

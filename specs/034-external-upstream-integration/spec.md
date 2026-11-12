# 034：New API / Sub2API 外部上游接入

状态：草案，作为 0 阶段和 MVP 的验收合同。它只规定 aigate 与下游网关之间的边界，不声称 New API 或 Sub2API 的全部功能已经在 aigate 中实现。

## 目标

把 New API 与 Sub2API 作为 aigate 的内部上游 channel 接入，并保留逐步把能力迁入 `internal/provider`、`internal/routing` 和统一账务的路径。

## 拓扑与数据所有权

- Compose/1Panel 可启动 `aigate`、`new-api`、`sub2api`、三个独立数据库和必要 worker。
- 公网只暴露 aigate；下游管理端口、数据库和凭据不暴露给客户。
- aigate 是客户身份、模型目录、公开售价、RouteSelector、钱包预占、settlement、采购成本和审计的唯一权威。
- 下游只负责自己的供应商凭据、账号池、协议适配和局部重试；不写 aigate 的钱包/settlement，不共享数据库。

## Channel 合同

每个发布的 `channel_version` 必须包含：

- `channel_kind`: `direct`、`new_api`、`sub2api`；
- `integration_mode`: `external_relay`、`embedded_adapter`、`migrated_capability`；
- endpoint、credential version、network/data-region evidence；
- protocol/model mapping、capability and usage profile；
- cost version、qualification status、safe-to-retry evidence；
- 若复用源码，source repository、commit、LICENSE/NOTICE 路径和本地修改摘要。

下游模型目录或 HTTP 200 只能作为发现/连通性证据，不能直接发布为可售模型。

## 路由与重试

1. aigate 先做授权、能力、地域、健康、并发、采购预算和毛利过滤，再由 `RouteSelector` 选择一个 channel。
2. New API/Sub2API 内部可以选择账号，但必须返回最终协议终态、usage 状态、成本状态和可能已消费标志。
3. aigate 只在明确未发送或明确无消费时跨 channel 重试；`sending`、`unknown` 或缺可靠 usage 时不得重放。
4. 任意下游重试不会新建客户 reservation、settlement 或钱包事实。

## MVP 验收

- [ ] 三服务和三个数据库可在同一 Compose/1Panel 编排中启动，服务之间使用内部网络。
- [ ] aigate 可以把一个已发布逻辑模型路由到 direct、new_api、sub2api 三类 channel 中至少两类。
- [ ] Chat、SSE、usage、错误分类和成本未知状态都有真实或 deterministic fixture 证据。
- [ ] 下游局部重试、aigate 跨 channel 重试和未知消费均有不重放负向测试。
- [ ] 客户售价、预占、唯一 settlement、采购成本和审计只在 aigate 生成。
- [ ] 至少一个能力完成 `external_relay → embedded_adapter` 的切片，并能按 route revision 回退。
- [ ] 源码复用的组件携带 LICENSE/NOTICE、来源提交和修改记录；许可证义务进入发布检查。

## 非目标

- 一次性迁移 New API/Sub2API 的客户、钱包、后台或全部数据库。
- 让下游网关成为 aigate 的最终路由、客户计费或供应商成本权威。
- 在 0 阶段为媒体、异步任务或 Realtime 引入多候选动态路由。
- 以单次真实请求或模型列表响应替代逐 profile 的正式供应商认证。

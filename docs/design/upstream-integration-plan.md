# New API 与 Sub2API 上游接入路线

状态：2026-11-12 修订。本文把 New API 与 Sub2API 定义为可统一部署、可逐步内收的上游渠道；它不宣称已经覆盖两个项目的全部能力，也不把外部探测结果写成正式供应商认证。

## 1. 目标架构

```text
客户应用
    │ 公开 API / 客户 Key
    ▼
moni-aigate（唯一公网入口）
    ├─ 直接供应商渠道
    ├─ New API 渠道（内部网络）
    └─ Sub2API 渠道（内部网络）
          └─ 各自的账号池、供应商适配器和下游重试
```

三套服务可以用同一个 Compose/1Panel 编排部署，但保持三个运行时边界：

| 组件 | 负责 | 不负责 |
| --- | --- | --- |
| moni-aigate | 客户身份、模型目录、公开售价、最终路由、钱包预占、客户结算、审计和恢复 | 下游账号池的具体轮换、下游后台和下游私有账单 |
| New API | 自己的渠道/账号池、供应商协议适配、下游局部重试和管理后台 | moni 客户的最终收费、公开客户 Key、平台总路由 |
| Sub2API | 自己的渠道/账号池、供应商协议适配、下游局部重试和管理后台 | moni 客户的最终收费、公开客户 Key、平台总路由 |

公网只暴露 aigate。New API、Sub2API、各自数据库和管理端口只放在内部网络，凭据不在三个系统之间共享数据库或双写。aigate 把它们当作 `channel`，而不是把它们的客户/钱包/账单模型拼成第四套事实源。

## 2. 路由与重试边界

动态路由放在 aigate 的 `internal/routing`，选择“直接供应商 / New API / Sub2API”这一级候选。New API 或 Sub2API 内部可以继续做账号池选择，但那是下游的局部选择，不能改变 aigate 已冻结的客户售价、采购预算或结算身份。

一次客户请求的责任链是：

```text
客户请求
  → aigate 授权、报价、预占
  → RouteSelector 选择一个上游 channel
  → channel 内部账号池选择（可由 New API/Sub2API 完成）
  → 返回协议终态、usage、成本状态和错误分类
  → aigate 统一结算
```

必须先确定重试责任：

- aigate 只对自己能证明“尚未发送或明确无消费”的失败执行跨 channel 重试；
- 下游网关可以在自己的 channel 内做局部账号重试，但必须返回最终 attempt 摘要、usage 状态和是否可能已消费；
- `sending`、`unknown` 或缺少可靠 usage 时，aigate 不因为下游返回 5xx 就再次生成；
- 任意层的重试都不能新建第二个客户 reservation、settlement 或钱包。

这样可以统一动态路由，同时避免三套系统各自重试、重复计费或把下游 200 响应误当成可售认证。

## 3. 统一接入合同

每个上游 channel 通过不可变 `channel_version` 发布以下信息：

- `channel_kind`: `direct`、`new_api` 或 `sub2api`；
- `integration_mode`: `external_relay`、`embedded_adapter` 或 `migrated_capability`；
- 内部 endpoint、网络和地域证据、认证凭据版本；
- 支持的协议、模型映射、上下文/输出边界和 usage 语义；
- 下游重试能力、错误分类和 `safe_to_retry` 证据；
- 采购成本版本、成本未知时的处理和资格状态；
- 来源项目、源码提交、许可证/NOTICE 路径和本地修改记录（存在源码复用时）。

`GET /v1/models` 只返回 aigate 已发布且获授权的逻辑模型。下游模型列表、管理员账号、渠道密钥和下游余额不会直接暴露给客户。

## 4. 逐步内收

能力迁移按下面顺序进行，每一步都能回退到前一层：

1. **外部上游阶段**：先把 New API 和 Sub2API 作为内部 OpenAI-compatible/已验证协议渠道接入，完成 Chat/Responses/SSE/usage/错误/成本证据和 Compose 联调。
2. **合同适配阶段**：在 aigate 的 `provider`/`routing` 中补齐缺失的协议与计量合同；下游继续承担账号池，aigate 先做影子路由和只读对账。
3. **源码内收阶段**：只有当某项能力需要更低延迟、更细粒度路由或统一数据事实时，才把对应上游模块放进同一仓库，优先以独立包/adapter 接入，不把三套数据库合并。
4. **能力切换阶段**：按 profile、协议和租户分批把流量从下游实现切换到 aigate 内置实现，保留可查询的 route revision 和回退开关。
5. **下游退役阶段**：当某能力的账号池、usage、成本和故障语义已经在 aigate 中有完整证据后，才下线对应 New API/Sub2API 能力；未迁移的能力继续作为外部 channel。

每一阶段都要求同一条 `Profile → Plan → Reserve → Attempt → Observe → Settle` 商业链路，不允许出现第二个钱包、第二个客户账单或双重扣款。

## 5. 源码复用与许可证执行

本项目允许在许可证和上游条款允许的范围内直接拉取、修改和一起开发 New API、Sub2API 或其他开源组件。许可证不是选型阻塞项，但复用必须可审计：

- 保留每个组件的原始 LICENSE、NOTICE、版权声明和来源仓库/提交；
- 在 `third_party/` 或明确的组件目录中保留上游边界，不把来源文件伪装成 moni 原创；
- 记录本地修改、同步提交和发布时的对应源码提供方式；
- 对 copyleft 组件按其适用范围发布衍生部分和网络服务源码/通知；对不同许可证的依赖分别执行义务；
- UI、品牌、默认配置和客户数据边界仍由本项目单独设计，不能因为源码可复用就共享密钥、数据库或客户账单。

因此，“可以直接复用源码”与“所有能力立即合并”为两个独立决定：前者现在已允许，后者仍需按 profile、账务和故障合同逐项验收。

## 6. MVP 完成定义

本轮 MVP 只要求外部上游闭环，不要求一次性吃掉两套项目：

- Compose 可启动 aigate、PostgreSQL、New API 和 Sub2API；三个数据库独立；
- aigate 公开入口可把已发布逻辑模型路由到 direct/new_api/sub2api channel；
- channel probe 能区分模型发现、协议成功、可靠 usage、成本证据和未知消费；
- aigate 的客户售价、预占、settlement、采购成本和审计保持唯一；
- downstream 的内部重试不能绕过 aigate 的 `SafeToRetry`、预算或结算；
- 至少有一条从外部 channel 到内置 adapter 的迁移切片，并能通过 route revision 回退。

真实供应商、生产支付、HA、全媒体动态路由和下游全部能力迁移仍按各自资格门禁推进。

# 029：第一阶段 New API 核心业务能力验收清单

2026-11-10；状态：草案，待开发者评审后冻结。所属阶段：[第一阶段](../../docs/design/phase-roadmap.md#4-第一阶段固定版本-new-api-核心业务能力闭环)。

## 1. 目的

第一阶段的完成条件是“固定版本 New API 核心业务能力对齐”。本规格把这个条件变成一张可逐项判定的清单：每项写明对齐什么、怎样验收、证据放在哪、当前状态。清单本身是第一阶段的交付物；清单全部通过才算第一阶段完成。

对齐基准固定为 New API `e449cc700eb569cdfc2199cb73e97de46944f43c` 的公开产品能力。不追赶 New API 后续版本，不复制其代码、数据库、内部 quota 字段或后台接口形状。

## 2. 验收规则

1. **数据库**：凡涉及持久事实的项，在专用 PostgreSQL 测试库（`make verify-current-mvp` 固定的 `127.0.0.1:55439`）上执行，结果为 PASS 才算通过。SKIP 不算通过。
2. **Mock**：标为“Mock 验收”的项使用仓库内 deterministic mock（文本协议见 [031](../031-deterministic-mock-upstream/spec.md)，媒体见[媒体供应商安全](../../docs/design/media-provider-safety.md)）。证据必须写明使用的是 mock，不能写成供应商认证。
3. **浏览器**：控制台项需要隔离浏览器操作记录，覆盖桌面与 390px 移动宽度。
4. **复验**：清单冻结后，所有项在同一提交上重新执行一次。历史验证记录只作参考，不能直接沿用为本清单的通过证据，因为 028-U 已整体替换文本计量。
5. **状态取值**：`未开始`、`待复验`（已有历史证据，需在当前提交复验）、`缺口`（能力或验收前置不存在）、`通过`（附本次证据）。

## 3. 清单

### 3.1 身份、租户与权限

| ID | 对齐内容 | 验收方式 | 历史证据 | 状态 |
|---|---|---|---|---|
| P1-ID-01 | 平台用户、租户、成员；角色 admin、finance_operator、finance_approver、owner、member、viewer | 真实 PG：创建、停用、角色变更、跨租户访问拒绝 | [mvp-release-qualification](../../docs/verification/mvp-release-qualification.md) | 待复验 |
| P1-ID-02 | 邀请开户、密码、会话、可选 TOTP | 真实 PG + 浏览器 | 同上 | 待复验 |
| P1-ID-03 | 开放注册、邮箱验证、密码找回（默认关闭） | 真实 PG + 本地 SMTP 收件 | [self-service-identity](../../docs/verification/self-service-identity.md) | 待复验 |
| P1-ID-04 | 运维账号恢复 CLI | 真实 PG | — | 缺口（不在 `main`，见 `claude/mvp-beta-blockers`） |

### 3.2 API Key

| ID | 对齐内容 | 验收方式 | 历史证据 | 状态 |
|---|---|---|---|---|
| P1-KEY-01 | 创建、一次性展示、撤销 | 真实 PG + HTTP | [key-configuration](../../docs/verification/key-configuration.md) | 待复验 |
| P1-KEY-02 | 模型范围、地域、累计额度、有效期 | 真实 PG：预占 402、地域 503、过期拒绝 | 同上 | 待复验 |
| P1-KEY-03 | 来源 IP/CIDR | 真实 PG + 可信反代 | [key-access-policy](../../docs/verification/key-access-policy.md) | 待复验 |
| P1-KEY-04 | RPM、burst、并发 | 真实 PG：429 不预占 | [key-runtime-limits](../../docs/verification/key-runtime-limits.md) | 待复验 |

### 3.3 模型、渠道与路由（仅文本）

| ID | 对齐内容 | 验收方式 | 历史证据 | 状态 |
|---|---|---|---|---|
| P1-CAT-01 | 模型目录：`/v1/models`、客户目录、公开价格页 | 真实 PG + HTTP + 浏览器 | [public-catalog](../../docs/verification/public-catalog.md) | 待复验 |
| P1-CAT-02 | 模型别名 | 按 [030](../030-model-aliases-group-multipliers/spec.md) | [当前专项证据](../../docs/verification/phase1/P1-CAT-02.md) | 待复验（当前迁移、operation、授权读取与生命周期专项已通过；浏览器和六协议复验未完成） |
| P1-CH-01 | 渠道、不可变版本、凭据池 1..32 成员 | 真实 PG | [credential-pools](../../docs/verification/credential-pools.md) | 待复验 |
| P1-CH-02 | 模型发现与差异预览 | 真实 PG + mock 目录 | [model-discovery](../../docs/verification/model-discovery.md) | 待复验 |
| P1-CH-03 | 跨渠道批量启停与回执 | 真实 PG | [channel-operations](../../docs/verification/channel-operations.md) | 待复验 |
| P1-RT-01 | 同一逻辑模型多渠道：优先级、权重、容量、健康 | 真实 PG + [031](../031-deterministic-mock-upstream/spec.md) 多候选 | [cost-aware-routing](../../docs/verification/cost-aware-routing.md) | 待复验（多候选 mock 已接入；完整部署场景仍待复跑） |
| P1-RT-02 | 成本优先、未知成本拒绝、采购单价上限 | 同上 | 同上 | 缺口（同上） |
| P1-RT-03 | 基础故障切换：可信无消费拒绝换渠道，最多两次 | 031 故障注入：429 无消费、连接拒绝 | — | 待复验（多候选 mock 已接入；完整部署场景仍待复跑） |
| P1-RT-04 | 可能已消费的请求不重放 | 031 故障注入：发送后断开、读超时、流内错误；mock 记录每个请求的上游接收次数恰为 1 | — | 待复验（接收记录和故障注入已接入；完整部署场景仍待复跑） |

### 3.4 计价、资金与采购

| ID | 对齐内容 | 验收方式 | 历史证据 | 状态 |
|---|---|---|---|---|
| P1-PR-01 | 默认售价版本、价格组、缺价拒绝 | 真实 PG | [tenant-pricing](../../docs/verification/tenant-pricing.md) | 待复验 |
| P1-PR-02 | 分组倍率录入并编译为绝对价格 | 按 [030](../030-model-aliases-group-multipliers/spec.md) | [当前专项证据](../../docs/verification/phase1/P1-PR-02.md) | 待复验（当前 operation、guard、回放和预览专项已通过；控制台和完整清单仍待复跑） |
| P1-FN-01 | 硬预占、唯一结算、未知用量待核对、24 小时释放 | 真实 PG + 031 | [unified-metering](../../docs/verification/unified-metering.md) | 待复验 |
| P1-FN-02 | 人工核款双审、消费退款、核对 | 真实 PG + 浏览器 | [mvp-release-qualification](../../docs/verification/mvp-release-qualification.md) | 待复验 |
| P1-FN-03 | 双审兑换券 | 真实 PG + 浏览器 | [funding-vouchers](../../docs/verification/funding-vouchers.md) | 待复验 |
| P1-FN-04 | 支付订单（本地沙箱） | Mock 验收：沙箱进程 | [payment-application](../../docs/verification/payment-application.md) | 待复验 |
| P1-FN-05 | 资金来源、FIFO、逆序退款 | 真实 PG | [wallet-sources](../../docs/verification/wallet-sources.md) | 待复验 |
| P1-PC-01 | 采购成本版本、逐 attempt 成本、未知不记零 | 真实 PG + 031 | [cost-aware-routing](../../docs/verification/cost-aware-routing.md) | 待复验 |
| P1-PC-02 | 采购账户、共享预算、周期预算 | 真实 PG | [procurement-accounts](../../docs/verification/procurement-accounts.md)、[periodic-budgets](../../docs/verification/periodic-budgets.md) | 待复验 |

### 3.5 协议（Mock 验收）

| ID | 对齐内容 | 验收方式 | 状态 |
|---|---|---|---|
| P1-PT-01 | Chat JSON/SSE、函数工具 | 031 + 真实 PG：成功、工具增量、取消、缺 usage、冲突 usage | 待复验 |
| P1-PT-02 | Responses JSON/SSE | 同上 | 同 [031 mock harness](../../docs/verification/phase1/031-mock-harness.md) | 待复验（部署 mock 已支持；完整业务链仍待复跑） |
| P1-PT-03 | Messages JSON/SSE | 同上 | 缺口（同上） |
| P1-PT-04 | Gemini 文本 JSON/SSE | 同上 | 缺口（同上） |
| P1-PT-05 | Embeddings | 同上 | 缺口（同上） |
| P1-PT-06 | Rerank | 同上 | 缺口（同上） |
| P1-MD-01 | 图片生成、TTS（mock，不参与路由） | `development`/`business-qa` 显式 mock；`inner-test` 关闭且零账务事实 | 待复验 |
| P1-MD-02 | 异步视频任务（mock） | 同上；worker 重启不重复提交 | 待复验 |
| P1-MD-03 | 单 turn Realtime（mock） | 同上；Receive→Settle 不重新 Dispatch | 待复验 |

### 3.6 控制台、报表与运维

| ID | 对齐内容 | 验收方式 | 历史证据 | 状态 |
|---|---|---|---|---|
| P1-UI-01 | 客户控制台：总览、Key、成员、模型、钱包、请求、价格、Playground | 隔离浏览器 + 390px | [console](../../docs/verification/console.md) | 待复验 |
| P1-UI-02 | 管理控制台：平台、渠道、模型路由、价格、采购、财务、告警、报表 | 同上 | 同上 | 待复验 |
| P1-RP-01 | 经营报表、请求级 CSV、未知成本不当零 | 真实 PG + 浏览器 | [operating-reports](../../docs/verification/operating-reports.md) | 待复验 |
| P1-OP-01 | 站内告警、审计 | 真实 PG + 浏览器 | [operations-alerts](../../docs/verification/operations-alerts.md) | 待复验 |
| P1-DP-01 | Compose 单机：空库迁移、provision、live/ready、重启 | Compose 实跑 | [current-mvp](../../docs/verification/current-mvp.md) | 待复验 |
| P1-DP-02 | 备份、恢复演练、recovery mode | Compose 实跑 | [backup-restore](../../docs/verification/backup-restore.md) | 待复验 |

### 3.7 明确不在第一阶段

订阅与套餐、正式支付商、真实供应商认证、STT 与图片编辑、OAuth/OIDC/Passkey、原路现金退款与拒付、多实例 HA、1Panel 等面板实装（可选部署环境，不构成任何阶段的验收门禁）、New API 数据迁移、插件与 MCP 执行、签到与分佣。理由见[四阶段路线 §4.1](../../docs/design/phase-roadmap.md#41-能力分类)。

## 4. 完成判定

第一阶段完成当且仅当：

1. §4.1–§4.5 每一项的状态为 `通过`，并附本次提交上的证据路径；
2. 证据在同一提交上产生；
3. 统一架构的能力表与本清单一致。

当前已迁移 [031](../031-deterministic-mock-upstream/spec.md) 的多候选部署 mock，以及 [030](../030-model-aliases-group-multipliers/spec.md) 的别名和分组倍率代码。后续按证据记录复跑 P1-RT-01..04、P1-PT-02..06、P1-CAT-02 和 P1-PR-02 的完整 PostgreSQL/HTTP/UI 场景。

## 5. 证据格式

每项证据记录在 `docs/verification/phase1/<ID>.md`，包含：提交 SHA、执行命令、数据库地址（不含凭据）、PASS/FAIL/SKIP 计数、是否使用 mock、未覆盖的范围。机器可读汇总写入 `docs/verification/phase1/summary.json`。

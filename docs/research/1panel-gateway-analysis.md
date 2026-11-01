# 1Panel-Gateway 分析：产品治理借鉴与实现边界

状态：**DRAFT，供审阅**  
项目：moni-aigate  
目标场景：对外提供 API 的商业运营平台  
研究日期：2026-10-30（Asia/Shanghai）

## 1. 核心结论

用户提供的 [1Panel-Gateway](https://github.com/1Panel-dev/1Panel-Gateway) 仓库是产品说明与界面截图仓库。**所核查的公开基线没有网关实现源码**，因此本报告对其内部架构、代码质量和性能不作结论。

moni-aigate 可以借鉴其公开的账号池、模型组、用户组、并发治理和运维流程，形成自己的实现。这里的“融合”指产品能力与架构思想的组合；不能表述为复制或改造了 1Panel-Gateway 的实现。

针对商业 API 平台，Gateway 的公开材料主要证明团队治理产品的能力声明，**未证明具有商业钱包、在线充值、退款、支付对账、订单或平台收入结算能力**。这些需要 moni-aigate 另行设计，不能由“Token 配额”推导已有商业计费闭环。

## 2. 基线与核查方法

| 项目 | 核查结果 |
|---|---|
| 仓库 | `1Panel-dev/1Panel-Gateway` |
| 分支 | `main` |
| 固定提交 | [`5afe609bb74e9fb35f2d862f7877e5050c4446b7`](https://github.com/1Panel-dev/1Panel-Gateway/commit/5afe609bb74e9fb35f2d862f7877e5050c4446b7) |
| 提交时间 | `2026-09-03T09:22:22+08:00` |
| 提交主题 | `docs: update documentation link (#4)` |
| 文件清单 | `.gitignore`、`README.md`、9 张 PNG/SVG，共 11 个跟踪文件 |
| 未发现 | 网关源码、`go.mod`、`package.json`、构建脚本、数据库迁移、OpenAPI、自动化测试、`LICENSE` |

执行了浅克隆、`git show`、`git ls-tree -r --name-only HEAD` 与 `git ls-remote --heads --tags origin`。可复核的[固定目录树](https://github.com/1Panel-dev/1Panel-Gateway/tree/5afe609bb74e9fb35f2d862f7877e5050c4446b7)用于界定结论范围。

远程还列出 4 个文档补丁分支；版本标签 `v1.0.1` 至 `v1.5.0` 均指向上述提交。另通过 GitHub Contents API 检查 `v1.0.0` 顶层，同样只有 `.gitignore`、`README.md` 与 `docs`。未逐一审查所有补丁分支，不将“本次基线没有源码”扩展为对所有历史或私有代码的结论。

本报告区分三个证据层级：

1. **仓库事实**：固定提交的文件、README 行号与截图内容。
2. **产品声明**：README 与官方使用文档描述的行为，未经源码或运行测试验证。
3. **设计建议**：moni-aigate 应采用的机制，是新设计，尚未实现或验证。

## 3. 固定提交证据

| 证据 | 可支持的判断 | 边界 |
|---|---|---|
| [README.md:16–20](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L16-L20) | 产品面向企业/团队，以统一 Base URL 和 API Key 接入；声明 10 人及以下免费使用 | 免费使用说明不等同开源许可 |
| [README.md:24](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L24) | 模型映射与账号池 | 不证明具体适配器或调度实现 |
| [README.md:25](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L25) | 用户组、用户、Key 的权限、并发与 Token 配额 | 不证明钱包或支付账本 |
| [README.md:26–27](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L26-L27) | 内容/规则路由、优先级、负载、健康状态 | 不证明算法准确率、重试安全或并发原子性 |
| [README.md:28–29](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L28-L29) | 内容合规与用量分析 | 不证明审核有效性或计量精确性 |
| [README.md:33–45](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L33-L45) | 1Panel 应用商店与独立 Docker 部署入口 | 不意味着依赖 1Panel 主程序才能运行 |
| [README.md:48–57](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L48-L57) | 首次密码、首次改密、HTTPS 反向代理的使用说明 | 不证明内部身份认证机制 |
| [README.md:65–77](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L65-L77) | 模型广场、账号池、用户组、路由、敏感词、统计的界面预览 | 不证明前端框架 |
| [README.md:81–90](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/README.md#L81-L90) | 产品官网、独立网关文档、问题反馈指向 1Panel 主仓库 | 社区归属不等于共享实现或许可证 |

## 4. 官方产品文档补充

以下为官方站点的当前版本，**没有与上述 Git SHA 绑定**。访问日统一为 **2026-10-30**；文档随版本变化，本文将其作为产品说明来源。

| 来源 | 本次读取的内容 |
|---|---|
| [产品介绍](https://docs.fit2cloud.com/ai-gateway/product-intro) | 六类治理能力，账号池调度、分层并发、语义路由和内容审计 |
| [快速入门](https://docs.fit2cloud.com/ai-gateway/quick_start) | 管理员配置到用户持 Key 调用的完整流程，协议端点与使用限制 |
| [账号池](https://docs.fit2cloud.com/ai-gateway/admin-user-manual/account_pool) | 上游连接、账号状态和模型映射的产品配置 |
| [模型组](https://docs.fit2cloud.com/ai-gateway/admin-user-manual/model_group) | 面向请求模型的逻辑分组 |
| [用户组](https://docs.fit2cloud.com/ai-gateway/admin-user-manual/user_group) | 模型授权、并发与配额 |
| [智能路由](https://docs.fit2cloud.com/ai-gateway/admin-user-manual/smart_route) | 样本、预览、路由决策日志与统计 |
| [内容合规](https://docs.fit2cloud.com/ai-gateway/admin-user-manual/content_compliance) | 敏感词与语义审核配置 |
| [部署说明](https://docs.fit2cloud.com/ai-gateway/installation/deployment_instructions) | 部署形态与数据库配置 |
| [配置文件](https://docs.fit2cloud.com/ai-gateway/ops/config_files) | 数据与凭据密钥持久化目录、环境变量 |
| [1Panel 安装](https://docs.fit2cloud.com/ai-gateway/installation/1panel_installation) | 应用商店安装、资源限制、数据库关联与备份 |

### 4.1 文档描述的请求与管理流程

```mermaid
flowchart LR
    A[管理员接入上游账号] --> B[配置请求模型映射]
    B --> C[组织模型组]
    C --> D[用户组授权与配额]
    D --> E[用户创建网关 API Key]
    E --> F[统一接口调用]
    F --> G[权限与资源准入 / 内容合规]
    G --> H[模型路由与账号选择]
    H --> I[上游调用与结果返回]
    I --> J[调用日志与用量统计]
```

这是文档外部行为的归纳，**不是源码中的精确调用顺序**。公开材料无法确认权限校验、配额预留、队列等待与上游调用之间的具体事务和生命周期。

- 一个上游账号配置供应商、协议类型、服务地址、真实凭据、请求模型到上游模型的映射、优先级与最大并发。
- 模型组组织同协议的请求模型，并支持调整顺序；用户组授予模型组及资源上限。
- 用户自助创建网关 API Key，完整 Key 仅在创建成功时显示一次。
- 文档当前列出的协议包括 Chat Completions、Responses、Anthropic Messages、Embeddings、Images 与 Audio；这不构成所有字段完整兼容的证明。
- 文档描述全局、用户组、API Key、上游账号四层并发控制；用户组/Key 超额及时拒绝，账号暂满可进入有限短队列。
- 固定模型走映射；虚拟模型可使用规则、上下文与向量样本判定简单/复杂任务，再选择模型组。
- 日志包含 Request ID、请求模型、实际模型、账号尝试、响应状态、耗时与 Token。

### 4.2 部署与持久化

配置文档说明默认 SQLite 库为 `/opt/ai-gateway/data/db/ai-gateway.db`，凭据密钥文件为 `/opt/ai-gateway/data/security/credential.key`；丢失密钥将无法解密已保存凭据。它没有公开加密算法、AAD、密钥轮换或缓存实现。

当前安装文档提供 SQLite 与 PostgreSQL 两种形态。PostgreSQL 应用版本要求 1Panel ≥ 2.4.0 和 PostgreSQL 18，这是该产品当前模板的要求，不能直接作为 moni-aigate 的版本要求。两种数据库形态不能直接交叉升级或恢复备份。

1Panel 在这里承担应用安装、端口与资源限制、数据库关联、备份等运维入口；Gateway 也提供独立 Docker 运行方式。moni-aigate 可以使用类似交付方式，无需将 Linux 面板的主机管理模块嵌入网关运行时。

## 5. 实际查看的截图信息

以下链接指向上游固定提交；本项目未复制截图资产、Logo 或界面源代码。

| 截图 | 观察到的界面信息 | 对 moni-aigate 的启发 |
|---|---|---|
| [账号池](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/docs/images/model-proxy.png) | 供应商、协议、模型映射、优先级、最大并发、状态，统一筛选和表格操作 | 上游资源管理与客户自助操作分离；连接状态、限额与映射应在同一详情中可见 |
| [用户组](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/docs/images/user-groups.png) | 组并发、单 Key 并发、Token 配额、授权模型组、启停 | 将客户权益、模型访问权与请求准入联系起来，同时明确平台角色和客户角色的边界 |
| [路由样本](https://github.com/1Panel-dev/1Panel-Gateway/blob/5afe609bb74e9fb35f2d862f7877e5050c4446b7/docs/images/smart-routing-samples.png) | 样本管理、决策日志、统计；批量样本、决策预览、向量构建 | 策略需要可预览、可解释、可观测，不能只提供规则编辑表单 |

截图可支持页面和字段层面的观察，无法支持 React/Vue、组件库、状态管理或后端语言的判断。

## 6. 优点、融合方式与商业平台补足

| 可借鉴的优点 | moni-aigate 设计落点 | 对商业 API 平台的补足 |
|---|---|---|
| 上游账号与对外模型解耦 | 供应商连接、凭据、模型绑定、公共模型别名分层 | 同一模型别名对应明确的售卖价格与价格版本，切换上游后仍能追溯实际成本 |
| 用户、组和 Key 分层治理 | 平台管理员、客户账户/项目、Key 分层授权 | 客户隔离、资金归属与资源归属必须一致，跨客户日志与余额不可见 |
| 多层并发与健康状态 | 全局、客户、Key、上游并发，超时与熔断策略 | 支付成功不代表无限并发；资金准入和资源准入分别校验 |
| 路由决策可观测 | 记录配置版本、候选筛选、实际命中和每次尝试 | 销售计费与上游尝试成本分别记录，避免失败切换造成客户重复扣费 |
| 用量与调用日志闭环 | Request ID、TTFT、总耗时、usage 来源、供应商状态 | 增加不可变资金流水、请求结算状态、调整记录、支付订单与对账 |
| 用户自助模型与 Key | 模型目录、一次性显示密钥、禁用/轮换、个人用量 | 增加客户消费明细和余额视图；充值渠道在方案明确后集成 |
| 应用商店与容器交付 | 首版提供 Compose、1Panel 编排操作与备份恢复说明；应用包可后续单独制作 | 备份数据库、凭据主密钥与必要配置，并以恢复演练验证，不仅备份镜像 |

这些是目标能力及其预期价值，尚未实现。没有同负载实测，不能承诺成本节省比例、吞吐提升比例或相对性能优势。

## 7. 第一版建议与明确取舍

1. **先确定性路由，再语义路由。** 第一版采用模型别名、显式规则、优先级/权重与健康筛选。语义路由需要额外向量服务、样本维护、准确率评估，也会增加延迟、成本和输入内容外发范围，适合后续独立实验。
2. **授权语义显式化。** 官方快速入门写明，用户组未选择任何模型组时可访问全部模型。moni-aigate 建议空白授权默认拒绝，全部允许须显式配置，避免商业客户意外访问高成本模型。
3. **先快速拒绝超额请求。** 多层并发满额返回可识别错误与重试提示；有界等待队列后续按真实需求加入。避免第一版同时承担排队公平性、取消与超时传播的复杂度。
4. **默认保留元数据。** 正文审计需独立开关、脱敏、权限与保留周期。不得将保存完整客户输入输出当作所有客户默认权益。
5. **商业结算单独设计。** Token 配额、客户钱包、销售价格与上游成本是不同概念。至少明确预留/结算/释放、usage 缺失、流式中断、重试、重复支付回调、退款和人工调整的状态与幂等规则；实现范围由 HLD/LLD 与规格共同确定。
6. **将 1Panel 作为部署集成目标。** 网关保持独立容器服务，避免引入整套主机管理能力增加攻击面与维护成本。

## 8. 许可证与项目边界

Gateway 所核查的固定树没有 `LICENSE`，GitHub license API 也返回 404。README 的“10 人及以下团队免费使用完整治理能力”属于产品使用说明，不能解释为源码开放或复制许可。moni-aigate 设计不以获得 Gateway 的源码使用权为前提，也不继承其席位定价方案。

另行核查的 **1Panel 主仓库**为 Linux 管理面板，核查基线 `dev-v2` / `87ab399bcca1c2a1482e4cd745e0b1f5fa73399b`。其[固定 LICENSE](https://github.com/1Panel-dev/1Panel/blob/87ab399bcca1c2a1482e4cd745e0b1f5fa73399b/LICENSE)为 GPLv3；这项证据只适用于该主仓库，**不能套用到 Gateway**。本报告没有审查 1Panel 主项目的实现，也不主张直接复制其模块。

后续如需采用任一上游实际源码或资产，应在实现规格中列出来源、固定版本和许可义务；当前设计采用自有表达和实现。

## 9. 不可验证项与后续验证要求

公开基线不能证明下列内部细节：

- 后端语言、Web 框架、前端框架、数据库 schema 与迁移方式。
- 供应商适配器接口、字段兼容范围、协议转换损失。
- 路由算法、公平性、多副本健康同步、并发限额的原子性。
- Key 摘要存储方式、凭据加密算法、主密钥轮换与访问控制。
- Token 计量、资金结算、请求去重、服务崩溃后的恢复行为。
- SSE 首字节后是否重试、客户端断连后的取消与计量行为。
- 观测 SDK、自动化测试覆盖、实际吞吐、延迟和故障恢复指标。
- 商业钱包、充值、退款、支付回调和财务对账的实现与产品条款。

moni-aigate 的 HLD/LLD 应将这些内容写成**待实现、待验收的新设计**，以规格中的场景、契约和测试验证，不能引用 Gateway 的宣传描述代替验证证据。

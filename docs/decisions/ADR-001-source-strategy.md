# ADR-001：上游来源、复用路线与许可证

状态：ACCEPTED（独立实现方向；r3范围已进入开发）；日期：2026-10-30。

## 背景

moni-aigate 要结合 1Panel-Gateway 与 New API，形成对外经营 API 的平台。当前工作区原为空目录，不存在已选定底座、代码许可证或远程仓库配置。

## 已验证事实

1. 1Panel-Gateway 基线 `5afe609bb74e9fb35f2d862f7877e5050c4446b7` 只有 README、.gitignore 和图片，无网关业务源码、构建清单或 LICENSE。可参考公开产品行为，不能据此取得源代码使用/素材复制授权。[固定树](https://github.com/1Panel-dev/1Panel-Gateway/tree/5afe609bb74e9fb35f2d862f7877e5050c4446b7)。
2. New API 基线 `e449cc700eb569cdfc2199cb73e97de46944f43c` 使用 [AGPLv3](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/LICENSE)，并有 [NOTICE §7 附加条款](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/NOTICE#L9-L25)。修改版 UI 的显著法律/关于/页脚署名、原项目可见链接和修改标记等要求不能忽略。README 提供商业授权联系途径，不代表本项目已经取得授权。
3. AGPL 允许商业使用。对衍生修改版进行网络服务和分发时，应落实适用的源码提供等义务；不能简单描述成“禁止商用”，也不能把代码复制后换标就视为可闭源。
4. 1Panel 主项目与 1Panel-Gateway 是不同对象。主项目 GPLv3 不能自动作为 Gateway 的许可。

## 备选

### A. 基于 New API 衍生开发

优点：协议、渠道、支付、用户体系覆盖面大，能减少初始实现工作。代价：接受其架构和升级耦合；若替换资金/租户核心要重新验证；按适用许可证发布与提供源码，或先取得覆盖计划用途的商业许可。

若采用 A，需重写 HLD/LLD 的目录、技术栈和数据迁移计划，明确保留/修改模块与 source-offer/NOTICE 展示方式，不能把本稿独立实现任务直接套在 fork 上。

### B. 独立实现，参考公开行为与研究结果

优点：对首版协议面、资金域、租户与后台体验可做明确取舍；依赖更少，容易建立 SDD 的可追溯边界。代价：初期开发更慢，不能声称立刻获得 New API 的全部适配能力。

不复制上游实现、测试、UI 组件、截图或品牌资产；依赖逐项核许可。由于团队已阅读上游源码，本路线不能自称已经验证的 clean-room；“重新写过”也不构成自动豁免许可的法律结论。若后续确需复用具体片段/模块，先记录来源、适用许可证和目标发布方式，再更新决策。

### C. 基于 1Panel-Gateway 代码

当前给定材料下不可执行。若开发者另有正式源码与授权，重新评估，不编造公开仓库没有的实现。

## 当前提案与后果

开发者先要求看路线差异，随后明确：**“先不要考虑 new api 的二次开发能力，我们可以参考 new api 的设计，然后重新实现”**。现选择 B，不采用 New API 二开底座。A/C 仅为留档对照；不再等待同一方向的重复确认。

随后开发者明确同意继续使用SDD开发，r3范围已进入实现；代码发布许可证仍未选定。实现过程未迁入上游业务代码。独立实现采用自有代码、UI 与测试，并记录参考设计和依赖来源；不因“重新实现”一句话就断言法律上的衍生关系已经得到认定。

1Panel 集成采用独立应用的 Compose、反向代理与备份操作方式；不改 1Panel 本体，不沿用其品牌，不声明官方合作/上架。公开 README 的免费席位条件不应当变成 moni-aigate 的席位限制或被当作源码许可。

## 验证与关联

已完成：固定提交树、LICENSE、NOTICE 与关键代码路径研究。已交付：独立实现开发版及[依赖来源清单](../research/dependency-provenance.md)。未完成：最终发布授权选择及生产发布验收。

- [上游 Gateway 研究](../research/1panel-gateway-analysis.md)
- [New API 研究](../research/new-api-analysis.md)
- [路线对比](../design/overview.md#6-源码路线的选择)
- FR/NFR 关联：NFR-06；任务 T-00、T-10；验收 AC-27。

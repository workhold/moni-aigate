# ADR-001：上游来源、复用路线与许可证

状态：ACCEPTED（混合接入方向；2026-11-12 修订）；日期：2026-10-30，修订日期：2026-11-12，部署定位修订：2026-11-14。

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

### B. 独立实现，参考公开行为与研究结果（仍作为无上游依赖的默认实现方式）

优点：对首版协议面、资金域、租户与后台体验可做明确取舍；依赖更少，容易建立 SDD 的可追溯边界。代价：初期开发更慢，不能声称立刻获得 New API 的全部适配能力。

独立实现仍适合核心账务、租户和客户 UI；但不再禁止复用上游实现、测试或协议适配。需要复用时，必须记录来源、适用许可证、保留通知和目标发布方式，再进入组件清单和架构复核。由于团队已阅读上游源码，不能用“独立重写”或“混合开发”替代实际许可证义务。

### C. 基于 1Panel-Gateway 代码

当前给定材料下不可执行。若开发者另有正式源码与授权，重新评估，不编造公开仓库没有的实现。

## 当前提案与后果

开发者现已明确允许：**只要遵守各项目许可证、NOTICE、来源和源码提供义务，就可以直接拉取 New API、Sub2API 或其他开源项目代码，与 moni-aigate 一起开发。** 因此不再把“是否直接复用源码”作为路线阻塞项，采用混合路线：

1. 先以 New API/Sub2API external relay channel 统一部署，尽快复用其账号池和协议能力；
2. 缺失能力优先在 aigate 的 provider/routing/账务合同中补齐；
3. 需要更低延迟或统一事实时，将对应模块以独立包、adapter 或受控 vendored 目录纳入；
4. 按 profile 和迁移切片逐项切流，最终退役已经被 aigate 完整替代的下游能力。

这不是“所有源码一次性合并”的决定。三套数据库、客户钱包、最终路由和结算仍保持单一权威；上游内部账号池和后台继续由各下游负责，直到对应能力完成迁移。

源码复用采用以下发布规则：保留原始 LICENSE、NOTICE、版权声明、来源仓库和提交；记录每次本地修改与同步点；按适用的 AGPL/LGPL/GPL/Apache/MIT 等义务提供对应源码、通知和衍生作品；第三方代码、品牌和默认配置不冒充 moni 原创。许可证合规由发布检查和组件清单验证，不以抽象的 clean-room 声明替代实际义务。

部署以仓库自带的 Compose/脚本为准；选择 1Panel 等面板部署时，采用独立应用的 Compose 导入、反向代理与备份操作方式，属于可选部署环境而非产品定位：不改 1Panel 本体，不沿用其品牌，不声明官方合作/上架。公开 README 的免费席位条件不应当变成 moni-aigate 的席位限制或被当作源码许可。New API/Sub2API 可以与 aigate 在同一编排中部署，但只通过显式 channel 合同互联。

## 验证与关联

已完成：固定提交树、LICENSE、NOTICE 与关键代码路径研究。已交付：独立实现开发版及[依赖来源清单](../research/dependency-provenance.md)。未完成：最终发布授权选择及生产发布验收。

- [上游 Gateway 研究](../research/1panel-gateway-analysis.md)
- [New API 研究](../research/new-api-analysis.md)
- [路线对比](../design/overview.md#6-源码路线的选择)
- FR/NFR 关联：NFR-06；任务 T-00、T-10；验收 AC-27。

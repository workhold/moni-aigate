# 041 实施任务与验收台账

状态：R0 已通过 PR #32 合入；P0 聚焦经济基础已实现并完成本地专用环境验收。P1/P2/P3 仍未实施，不授予真实供应商、正式支付、external commercial 或生产资格。

## R0：总体设计

- [x] 固定最新 main 基线，确认 032/033 已合入且 external commercial 仍被拒绝。
- [x] 将商业目标收敛为质量约束下的成本优化、客户节省与可审计贡献毛利。
- [x] 冻结 aigate/direct/New API/Sub2API 责任矩阵，允许永久混合，禁止执行器链式互连。
- [x] 定义五类商品、K3-Dynamic 披露、封闭任务分类和场景质量证据。
- [x] 区分保守准入、contract provisional 与账单确认贡献毛利，记录 COIN/CNY、消费退款和累计 attempt 成本缺口。
- [x] 冻结 FrozenExecution/ExecutionEvidence、inner pool、全局/局部重试和恢复边界。
- [x] 定义一个 SPA 的三个工作台、AdminConnector 单写者和未知结果状态机。
- [x] 独立语义复核通过，修正文档冲突/第二事实源/隐性重试/虚假资格；最终窄复核无 blocker/major，随后仅按用户要求澄清 USD/CNY 商业首发范围。
- [x] 集成人 reviewed architecture refresh/check 与 `make check` 通过；R0 仅文档变更，不声明业务/生产验收。
- [x] 用户审查后才创建提交/PR；R0 已通过 PR #32 合入，未自动合入。

## P0：聚焦子规格与经济基础（已实现）

- [x] 冻结 ServiceOfferVersion/TaskPolicy/QualityEvidence/EconomicsSnapshot 的 SQL、HTTP、ACL、故障和恢复合同。
- [x] 在 Reserve 冻结束付款基准、经济版本和节省参照；单位错误、未知付款/成本或商业所需 USD/CNY 估值缺失失败关闭。
- [x] 实现请求级累计供应商成本门禁；利润敏感商品在实现前固定单 outer attempt。
- [x] USD/CNY 均为一等付款与成本货币：保存原币向量，商业首发前完成保守 FxPolicyVersion、USD↔CNY 交叉组合和费用/取整反例；test-only 单 CNY 夹具不构成最终限制。
- [x] 复用并扩展现有 034.5 reporting gross_margin：补消费退款净付款基准、退款币再次消费去重、inner 成本和商品/任务维度；不新建利润聚合权威。
- [x] 区分 contract provisional/as-of 与账单确认贡献毛利；成本更正和负值可见。
- [x] 冻结 fixed_full_reservation 和服务端 ExecutionPolicy；首片 max_outer_attempts=1 写入商品/route/decision/FrozenExecution。
- [x] 修正架构/032/033/038 历史状态，保留历史记录而不倒写证据。
- [x] 完成专用 PostgreSQL、全仓 race、真实 main/HTTP、桌面/390px、独立生命周期和完整默认 serial-v1 029；41/41 PASS。详见[验证记录](../../docs/verification/041-p0-economic-foundation.md)。

## P1：direct-only K3-Dynamic test-only（未实施）

- [ ] 独立逻辑商品与不可变披露；fixed_full_reservation、ExecutionPolicy max_outer_attempts=1、max_relay_depth=0、cache_mode=disabled、coding_non_reasoning、无降档、严格 reasoning 拒绝。
- [ ] 注册版本化场景质量证据；按 execution profile/deployment/pool scope 绑定并校验公开阈值，缺证/过期/撤回或低于阈值不进入候选。
- [ ] direct/external 都在 sending 事务复核商品与证据有效期/撤回；外部还需生成前原子 pool fencing。
- [ ] 首批 direct 候选具有已知 CNY 成本和同场景质量证据；DeepSeek 可作为首选。
- [ ] 复用 033 cost_first/sticky/replay；成本、质量和利润硬过滤先于排序。
- [ ] 响应回显商品 ID，客户请求详情显示实际服务模型/tier_changed。
- [ ] 完成专库、真实 main、桌面/390px、回退/冲突/未知核查和适用完整 029。

## P2：薄执行接缝与统一控制台（未实施）

- [ ] ExecutionAdapter 不改变现有选择/首事件/取消/重试结果，ExecutionEvidence 只追加原 attempt。
- [ ] 完成供应与执行、商品与路由、请求与利润三个工作台。
- [ ] AdminConnector 先只读 Capabilities/ReadSnapshot/BuildChangePlan； unmanaged 默认不写。
- [ ] 每资源一个写者，operation 持久化、基线摘要、read-back 和 unknown 原操作核查。
- [ ] 浏览器只有 aigate 同源流量，秘密不进入 DTO/DOM/journal/日志/导出/截图。

## P3：真实 direct 与外部扩展（未实施）

- [ ] 首个 DeepSeek direct 真实 profile 完成请求、工具、取消/断流、usage、成本和账单核对。
- [ ] New API 固定池 test-only 纵切片；缺少 inner pool/dispatch 证据保持不可证明。
- [ ] Sub2API 仅在专用账号/OAuth/session 增量和条款允许时试点；普通 Key 转发不部署。
- [ ] 订阅账号成本分摊冻结前 cost_status=unknown，不进入利润敏感商业池。
- [ ] external commercial 独立 SDD、追加迁移、服务端+DB 门禁和真实资格逐项通过后才开放。

## 后续另行立项

- generic auto、期望 CPST、置信阈值和离线策略回放；
- USD/CNY 之外的通用多币种/自动 FX、税费、支付手续费和营销补贴；
- 订阅/OAuth 成本分摊、自动扩池和租户 canary；
- 媒体/任务/Realtime 外部执行、Resolve/回调/制品和任何动态路由；
- HA、生产资格、选择性源码内收和逐 profile 退役。

## R0 非验收声明

R0 不运行完整 029 作为新功能通过证明，不修改生产/测试凭据，不创建真实供应商请求，不开放 New API/Sub2API commercial，不宣称 K3/DeepSeek 已授权、已合格或可售，也不把规划中的统一控制台写成已实现。

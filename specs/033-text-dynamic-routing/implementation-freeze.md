# 033 实施冻结记录（2026-11-14）

本记录冻结第三阶段的数据、路由、计量和回放合同。只有前一片通过后才能开始下一片；冻结不表示已经实现。

## 决策

- D-033-0 选 A：`models.profile_version` 继续表示客户零售合同、计量 schema、预占界和输出上限。同一路由版本所有物理模型必须共用该 schema。
- D-033-1 选 A：auto 只有一个逻辑模型售价版本。请求冻结同一售价，不按落档新增价格维度；最低毛利在硬过滤阶段保护。
- D-033-2 使用独立字段 `route`，只在 `POST /v1/chat/completions` 接受。其他协议保持严格信封，仅使用已发布路由默认值。
- D-033-3 响应 `model` 始终回显客户原始请求名（含 auto）。物理模型默认仅平台可见；客户详情只显示服务档位标签，展示开关需随物理模型版本明确开启。

## attempt 计量 guard 修订（028 邻接）

零售 request/reservation/attempt 身份不变。客户界来自逻辑模型 profile；被选中 attempt 的供应商输入/输出界来自 `physical_models.capability`。`attempt_meter_snapshots` 的 supplier bound lines、plan canonical/digest、attempt quote 和相关 `meter_attempt_*` guard 必须比较所选物理能力，不能再假定等于路由版本唯一 profile。零售侧仍使用原逻辑 profile，禁止把两侧混用或用一个标量 fallback。

迁移为每个现有 binding 创建与逻辑 profile 完全等价的物理模型并回填，因此旧请求、旧 fixture 和旧 guard 语义不缩放、不重写。新绑定必须同时提供 physical_model_id 与一致的 upstream_model；物理模型的 context/output/tools 不满足请求时在硬过滤排除。数据库触发器保证 binding 的 upstream_model 与物理行一致。

## 物理模型与策略版本

物理行包含逻辑模型、upstream_model、capability、quality_tier、可空 quality_score、状态和 revision；不可变，变化通过新行与退役完成。同一路由版本绑定去重键改为 `(channel_id, physical_model_id)`，不修改既有 SQL 唯一形状，也不再由 Go 的 channel-only 去重误拒同渠道不同物理模型。

策略版本追加 scoring、quality_floor、min_margin_rate、stickiness 及 032 熔断参数，均为不可变值；旧版本 NULL 表示不启用。auto 候选可以跨档，非 auto 不跨档混排。

## 决策事实

每次选择只写一行 `route_decisions`，包含 request、可空 attempt、sequence、policy version、选中 binding/物理模型、精确 draw、sticky/degradation、完整有界 candidates 和 digest。候选数组同时包含被排除项及其 score/cost_snapshot。未准入失败池在释放前写一行 attempt_id=NULL。不得按候选数增加 INSERT。

迁移先把旧 `upstream_attempts.route_decisions` 数组转换为等价新行（degradation=none），网关与读取端同片切换后删除旧列和旧保护触发器；不保留双格式或旧 DTO。

## 请求与排序

硬过滤顺序固定为授权→地域→能力→质量→渠道/binding→凭据→容量/熔断→采购预算→成本未知→max_price→成本上限→最低毛利。高评分不能复活被排除项。毛利为 `1 - 保守供应商成本/客户冻结售价`，成本未知不记零，按 unknown_cost_policy 处理。

排序只在剩余候选中：未打破的粘性→最高 priority→成本/延迟/成功率偏好→可记录随机 draw→凭据权重。样本不足 50 的统计量为 NULL，不能用 0 或估算值替代。auto 降档写 `degradation=tier_changed` 并在详情显示；`allow_fallbacks=false` 不跨档。

会话标识只取 `X-Moni-Session` 的受控值，与登录会话无关，不从正文/IP 推断；按 `(tenant_id, session_id)` 隔离。建立、延续及 quality/cost/health 打破原因全部入决策。

## 回放与验收

`ReplayRoute(policyVersionID, requestShape, draw, statsSnapshot, candidates)` 只读持久化决策和快照，不读取当前配置或统计，不写库、不派发、不触碰资金。`moni-aigate route-replay --request <id>` 输出原因序列和选中路径，同一输入结果逐字节一致。

验收按 spec §10 全矩阵执行，特别包括：所有排除原因、三类切换、粘性打破、毛利、未知消费单 receipt、唯一结算、报表毛利、每次选择一次 INSERT，以及不使完整链路超出 032 已测基线。

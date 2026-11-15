# 开发方向审查（2026-11-14）

状态：已完成的审查记录。审查人：Claude（Opus 5）；触发：开发者要求评估 moni-aigate 当前开发方向是否准确合理，并深入处理"034 未冻结先实现"的问题。本文件是审查记录，不是新设计合同；034 的冻结裁决经本 PR 评审生效。

## 1. 范围与方法

通读并对照以下材料的当前 main 版本（b48a02b）：

- [HLD](HLD.md) 全文（735 行）、[LLD](LLD.md) 全文（1231 行）
- [四阶段路线](phase-roadmap.md)、[New API 能力路线图](new-api-roadmap.md)、[上游接入路线](upstream-integration-plan.md)、[交付计划](delivery-plan.md)、[overview](overview.md)
- specs：001、027、028、029–035，其中 [034](../../specs/034-virtual-coin/spec.md) 逐行审读
- [ADR-001](../decisions/ADR-001-source-strategy.md)、[ADR-003](../decisions/ADR-003-optional-mfa.md)、[ADR-005](../decisions/ADR-005-pre-mvp-single-path.md)、[统一架构](../architecture/README.md) 关键节
- Codex 侧 local-mvp 分支的在途未提交实现（034 第一批改动，观察时点 2026-11-14；评审期间该工作树仍在变动）

判据：AGENTS.md 的 SDD 约定与资金不变量（租户隔离、不可变发布版本、硬预占、唯一结算、未知消费不重放、迁移只追加、角色分离）。

## 2. 总结论：方向准确、合理

1. **资金不变量贯穿一致。** HLD §6 的商业闭环、LLD §5–§7 的资金模型/事务协议/状态机、027 来源闭环与 028-U guard、各 spec 的验收矩阵，都收敛在同一条 `Profile → Plan → Reserve → Attempt → Observe → Settle` 链路上；没有发现第二套钱包/计量/结算路径。
2. **混合接入的权责切分正确。** 0 阶段把 New API/Sub2API 作为外部 channel：aigate 握客户身份、目录、售价、预占、最终路由、结算与审计；下游只做账号池与局部重试。LLD §4.1 把 `external_relay`/`embedded_adapter`/`migrated_capability` 写成 `channel_version` 的发布合同，"下游不能成为第二个客户钱包或最终路由权威"落到了字段级。
3. **文档诚实度好。** mock 验收与真实供应商认证严格分开（029 要求"至少一个外部 channel 证据"补上 mock 与真实之间的缝）；历史证明候选（C0/B-P 系列）按 ADR-005 冻结隔离，不挡现行路径，也不被冒称为已完成。
4. **阶段顺序自洽。** 第一阶段（029/030/031/034/035）、第二阶段（032 数据面）、第三阶段（033 动态路由）的依赖关系明确；媒体"单 profile、单 adapter、共享钱包、不参与路由"的边界（HLD 文本与媒体结构边界、LLD 文末）清晰，没有把媒体动态路由偷偷塞进前三阶段。

## 3. 发现与处置

| # | 发现 | 处置 |
|---|---|---|
| F1 | 034 规格标记"设计草案，未冻结"，但 local-mvp 上已有未提交的实现改动 —— 违反"冻结前不改资金代码"的 SDD 约定 | 本 PR 处置：034 冻结（§4），在途实现逐项对照（§4.2），实现侧要求（§4.3） |
| F2 | 四阶段路线插入 0 阶段后小节未重编号，029–034 与 new-api-roadmap 的一批锚点失效 | 已由 PR #6 修复（小节归位 4.x/5.x/6.x 并同步全部引用） |
| F3 | local-mvp 与 main 的往返合并依赖人工节奏 | Codex 侧已执行一次 main→local-mvp 回合；建议每个 PR 合入 main 后都回合，避免 034 这类大改动在分叉上铺开 |
| F4 | 034 §4 表格把 6387 行标为"兑换券入账校验"，实际该位置是 `wallet_opening_journal_valid`；兑换券校验在 Go 侧 `voucher_query.go` 与 `funding_journal_valid` | 本 PR 已修正该表行 |
| F5 | 冻结的历史候选包（`fundinglotinit`、`procurementprefix`、`paymentrefundproto`）内含 `CNY` 字面量：它们绑定历史材料与固定摘要，034 **不应**改动；计量 canonical（`meter_*` 规范化，如 current_mvp.sql 3425/6134 行）按 034 §4 保留 `CNY` 标签，而零售外层价格 canonical（`pricing_vector.go` 等）改 `COIN`——两层不要混淆 | 已在 §4.2 对照表中逐处标注；实现侧遵守 034 §4 的分类 |
| F6 | 性能与 HA 未承诺（P95 ≤50ms 未达成、多活后置） | 与四阶段路线一致，非偏差；不在第一阶段收口前宣传性能 |

## 4. 034 深入处置

### 4.1 冻结裁决（本 PR 生效）

D-034-1…13 全部裁决：此前已裁决 5 项（D-034-4 付费先赠送后、D-034-9 单人运营开关默认开启、D-034-10 币到期后置、D-034-12 全额退款、D-034-13 退款额取 min）；本次新裁决 8 项，均为**采纳规格既有建议**：

| 编号 | 裁决 | 理由 |
|---|---|---|
| D-034-1 | 创世币价 CNY 1 币 = 1 CNY | 使全部存量数值无需缩放，迁移只改单位标签；币价追加式版本已写入 §5.8 |
| D-034-2 | 赠送为独立来源 | §5.4 全部结构（双 receipt、双 journal、parent_ref）已按此设计 |
| D-034-3 | 每张退款单独立向下取整，永不多退 | §7.3 已论证累计公式在可调币价下不成立 |
| D-034-5 | 手动美元付款仅 `fx_note` 备注 | 汇率不进请求路径；自动汇率版本属范围 V |
| D-034-6 | 提供"约合"展示，带版本、仅供参考 | 不进入记账，风险低 |
| D-034-7 | 显示名平台可配置 | 纯标签 |
| D-034-8 | 不实现余额重估；向上调整用批量赠送 | §9 已论证重估的停服成本与不可逆风险 |
| D-034-11 | 单次充值/赠送上限由运营另设 | int64 溢出检查之外的运营配置，不进资金合同 |

§15 阻断项复核：文本类（状态机 §7.2、角色字段 §5.1、来源组合 §5.3、父单据关联 §5.4、决策点 §13）全部关闭；标注"需要真实 PostgreSQL 验证"的项（§5.6 guard 扩展、§7.4 竞态、§7.5 未知提交）属于实现片验收，按 §16 矩阵在对应分片执行，不阻塞冻结。**冻结的含义**：设计合同固定；实现偏离必须先改规格；实现完成以 §16 的 PG 证据为准，不以代码存在为准。

### 4.2 在途实现对照（local-mvp 未提交工作）

对照 034 §4 的分类与 §17 的 34.1 范围，逐项核对 Codex 侧未提交改动：

| 规格条款 | 在途实现 | 结论 |
|---|---|---|
| 34.1：三表 `currency` 改 `COIN`、数值不变 | `current_004_virtual_coin_units.sql`：journals/funding_receipts/price_versions 回填 CNY→COIN，显式禁用并恢复 `immutable_*` 与 `wallet_source_fact` 触发器，append-only | 符合 |
| §4 函数体同步修改（钱包/售价侧） | 迁移内替换 `funding_journal_valid`、`payment_funding_journal_valid`、`wallet_opening_journal_valid`、`meter_insert_token_price`；`assert_pricing_safe_input` 改 `group_price.publish` 与 `default_price.publish` 两个零售分支、`cost_price.publish` 保持 CNY；`payment_sandbox_journal_valid` 完整重写 | 符合分类 |
| §4 不动的部分：采购观察（576 行）、成本版本（2407/2416）、计量规范化（3425/6134）、payment_* 表 | 迁移未触碰；注释明确"计量 canonical 保留 CNY 规范化" | 符合 |
| §16 类型区分：法币与币编译期不可互赋 | 新增 `billing.FiatMoney`，刻意不可赋值给 `Money`；payments 的 `Amount` 改 FiatMoney、`WalletCreditAmount` 保持 Money | 符合 |
| §4 Go 字面量清单（funding.go、funding_sandbox.go、voucher_query.go、catalog/pricing_*、public_catalog.go、reporting/report.go） | 全部在改动集中 | 符合 |

注意点（实现侧评审时确认）：

1. 迁移用 `pg_get_functiondef` 后只替换带引号的币种字面量，不改函数体内其他注释或供应商 CNY 文本；目标函数若未发生变化必须失败。
2. 评审期间该工作树仍在变动（`current_004_virtual_coin_units.sql` 在本审查进行中又被修改），说明实现活跃 —— 这正是先冻结规格的必要性。
3. §16 的 PG 验收矩阵（单位、守恒、竞态等）尚未运行，属实现片验收，不能用"代码已写"代替。

### 4.3 给实现侧的要求

1. 按 §17 分片提交，不要一次混合 34.1+34.2；34.1 先跑过 §16 的"单位/类型区分"两组断言再推进。
2. 冻结后任何偏离规格的实现需求，先修订规格再改代码。
3. `refund_held`/`retired` 两桶、守恒式扩展、`check-wallet-sources` 更新属 34.3，不要提前混入 34.1。

## 5. 不变量复核结论

逐条复核未发现违反：租户隔离（复合外键与父行锁序）、不可变发布版本（渠道/路由/价格/币价均追加式）、硬预占（Reserve 先于派发）、唯一结算（每请求一个 settlement）、未知消费不重放（`SafeToRetry`/`CanRetry` 未放宽）、迁移只追加（`current_004` 为追加文件）、角色分离（app/projector/owner 与支付 worker 边界保持）。

## 6. 合并后一致性修订

- D-034-11 与既有实施冻结记录统一为：`amount`、`bonus_amount` 分别具有 1,000,000,000,000 微币硬上限，部署配置只能收紧，不能放宽；§16 同步增加边界断言。§15 的裁决状态与状态机文本也改为与 §13 一致，不再保留“建议中/待评审”的旧措辞。
- 后续不可变性复核发现 current_004 曾改写历史 pricing operation 与审计原件。追加 `current_005_immutable_pricing_history.sql` 在同一升级事务中恢复原 CNY 字节并移除该存储列；读取路径改为验证原始指纹后投影 COIN。升级前后 operation、原始指纹、result snapshot 与审计必须逐字节一致，原 operation 仍可严格重放，不引入旧 DTO 或第二套历史事实。

# B-P4 供应商成本更正原件的内容与操作归属

2026-11-04；本页已由根集成人审定并冻结为实施合同。承接[成本/request §5.6、§7.2、§8.1](b-procurement-cost-request-contract.md)、[共同来源](b-procurement-source-contract.md)与[私有重放实施合同](b-procurement-replay-implementation-contract.md)。本片只补两个明确 typed cause 与原 `provider_cost_corrections` 的关系；不改已经冻结的 audit/outbox payload、ledger/PB effect 关系。

## 1. 责任与实施边界

拟新增私有 `source_closure_correction_relations.go` 和 `source_closure_correction_relations_test.go`，入口接收本次 `sourceClosureState` 的完整 raw Rows。现有来源叶子已核原 correction 的标量与六 pointer，成本域也有自己的原件检查；本片仍从 raw source、原 correction、原 attempt 与原 proposal 独立计算内容及业务身份 expected，不调用 writer、`AttemptCostHistory` / `AccountPrefix` / `RequestIncoming` 的 helper、Stats、Candidates 或报告替代本域关系。

本片不得改32表715列、40原件482列、已应用迁移、原 writer、旧 checker、公共 API 或数据库。不补造未保存的历史字段。根集成人审定合同后决定入口接线及统一架构同步；`SourceClosure.Complete=false`、`Qualified` 为空、无公共 `Check` 保持。

本片证明的是“这个 source 声明的 correction 确实是这笔原操作及其内容”，不证明当时可执行该更正。管理分类出生、非 inflight、提案/批准的完整成员及账户屏障、降成本须 invoice、历史双方角色与真实撤权、采购 PE、成本 mutation、审计消息及根/节点顺序，继续由各自已有域和最终逐对象依赖闭包负责；不能因本片通过就省略这些资格，也不以今天 p、binding、角色或成本投影补历史。

## 2. 共享预算与唯一身份

- 入口、索引、source/依赖/反向遍历、逐字段比较都使用同一 `replayWork`、context、绝对 deadline 与 `tick`；不逐叶重开预算。执行错误立即退出，由完整入口返回零结果。
- 所有物理自然主键与业务身份均建多值索引，只有 `len=1` 可消费内容。包括声明 source 自己的物理 PK；不能把两个相同 source ID、不同 operation 的行分别当作唯一来源消费，也不任选重复依赖的第一/末项。
- 两种 cause 的联合声明表同时按原 correction ID、**预期原 operation** 建多值反向索引。原 correction 同时按物理 ID、`operation_id` 建多值索引；后者是原 writer 的全局幂等身份，不附加 tenant/attempt 来缩窄唯一性。
- 所有普通身份按原字节比较，不 trim、大小写归一、UUID 重编码或按时间排序。unmanaged operation 保留原1..200字节字符串，不强制 UUID；`procurement:` 只在明确 approval cause 中拼接，不据任意原件或 operation 前缀猜管理分类。
- 金额按已经声明的原字符串精确相等，不使用 float64、delta 累加或原件自报字段反填 expected；现有金额/枚举/NULL 标量合同继续独立生效，不因本片内容相等而放宽。

## 3. 两种正向 correction 槽

每个真实 source 声明一个原 correction 槽，必须存在且唯一；同一个原件只能被联合声明表中的一个 source 消费。source 自己物理重复、输出 ID 或预期 operation 多重声明均失败。必须先登记已知业务槽，再处理可缺失的 proposal/attempt 依赖：缺依赖不能使已知 operation 的二级反向集合失效。

| 原 correction 字段 | `ApprovalConsumption` expected | `UnmanagedCorrectionSource` expected |
| --- | --- | --- |
| id | `ProviderCorrectionID` | `ProviderCorrectionID` |
| tenant_id | source `TenantID` | source `TenantID` |
| attempt_id | source `AttemptID` | source `AttemptID` |
| operation_id | `"procurement:" + ApprovalOperationID` | source `OperationID` 原值 |
| amount | 唯一同一原 proposal `Amount` | source `Amount` |
| basis | 同一原 proposal `Basis` | source `Basis` |
| evidence_ref | 同一原 proposal `EvidenceRef` | source `EvidenceRef` |
| created_at | 原件自己的 finite 时间合同 | 原件自己的 finite 时间合同 |

原件六 pointer 完整精确相等：

```text
pp_root_id     = source.RootID
pp_root_step   = source.StepNo
pp_source_kind = approval_consumed | unmanaged_correction（按明确 source 表）
pp_source_id   = source.ID
pp_source_role = correction
pp_created_xid = source.CreatedXID
```

source 的 `StepKind` 同样等本表 canonical kind。不能只比较 root 或相同金额；原件 pointer 指向别种合法 source/role 仍失败。原 correction `created_at` 不强等 source、proposal 或审计的诊断时钟，不能从时间推断执行顺序。

### 3.1 共同 tenant/request/attempt 桥

原 correction 没有 request 列，必须取 source `AttemptID` 的唯一原 `upstream_attempts`，其 `ID/TenantID/RequestID` 分别精确等 source `AttemptID/TenantID/RequestID`。原 correction 的 tenant/attempt 还须按上表同时匹配，不能只凭 correction 金额或 source 自报 request 认定归属。

不把原 attempt 今天的 cost、delivery、quote 或当前采购 reservation 当作本次历史 before。原 attempt 的完整出生及管理分类资格仍是最终逐对象依赖，原件 scope 桥通过不将 legacy attempt 授为完整来源。

### 3.2 approval 的原 proposal 与出生桥

须唯一取得 source `ProposalID` 的原 `procurement_cost_proposals` 和 source `ProposalBirthID` 的 `pp_proposal_births`，并逐项比较：

1. 原 proposal `ID/TenantID/AttemptID/Requester` 等 source `ProposalID/TenantID/AttemptID/Requester`；其 `State=approved`、`Approver=source.Approver`、`ApprovalOperationID=source.ApprovalOperationID`，后两者是 SQL 非 NULL 精确字符串，`ApprovedAt` finite。
2. 指定 birth `ProposalID/TenantID/RequestID/AttemptID/Requester` 全等 source 的同名身份；birth 物理 PK 必须唯一。不能借同额另一提案或同 requester 的另一 request。
3. 原 proposal 的 `ExpectedReservationRevision/ExpectedEvidenceCount` 分别精确等指定 birth `ReservationRevision/MemberCount`。这里只绑定原提案的冻结屏障身份，不重放其实际账户/成员 prefix，不要求旧 approved proposal 等于今天的 p/member。

原 proposal 的 amount/basis/evidence 为本片的原始输入事实，不从 audit、outbox 或 correction 反填。birth 没有另存 amount/basis/evidence/reason 的输入副本，因此本片不虚称可重建其未保存的第二份输入，也不将原 proposal `CreatedAt` 等同 birth 诊断时间。其保护写面、原形状、出生唯一性、历史批准资格及原 payload 不可改仍为其他明确义务。

### 3.3 unmanaged 的允许与不额外消费项

expected amount/basis/evidence 直接来自唯一 `UnmanagedCorrectionSource`。本片不把 actor 放入 correction 的 operation 幂等身份：原 correction 没有 actor 列，`InputActorID/EffectiveAuditActor` 及对应审计关系由已有 payload 片核验。原操作合法快返不应产生第二 source 或第二原 correction；换 actor、不同输入次序不能使重复 operation 合法。

同一个 attempt 可以有多笔同金额或 basis-only 更正，每笔不同合法 operation 各有自己的 source 与 correction，允许零金额；不按金额或 revision 相同去重。unmanaged 的 operation 恰以 `procurement:` 开头仍按本明确分支处理；若与某 approval 的预期 operation 碰撞，必须按**联合全局 operation 身份**报多义，不能拆成两族后各自通过。

## 4. 反向集合、已知槽与 legacy

1. 正向要求指定物理 ID 的原件恰一条、联合 source claim 恰一个，且 source 自身、全部依赖和本 operation 的 source/original 身份均唯一。一个 source 缺指定原件或依赖时仍对已登记 operation 执行反向检查。
2. 每条原 correction 只要六 pointer 任一非 NULL，就须回到唯一正确 typed source 的声明槽，并满足完整 pointer 与本片内容。部分 NULL、未知 source kind/role、错误 ID、指向没有声明该原件的真实 source 均不能退回 legacy。
3. 对本次明确 source 声明的每个预期 operation，原 correction 业务集合必须恰含所指定的那一物理原件。指定原件缺失、operation 被改、依赖缺失或 source 重复，都不能放过该 operation 旁边不同 ID、六 pointer 全 NULL 的额外原件。由 source 的真实 operation 声明触发，不凭原件自报金额或 attempt 猜新槽。
4. 两 cause 族共享同一反向声明表。两个 source 声明相同 correction ID、相同 operation，或跨族抢用原件，均不任选一种解释；相关实际 source 与原件按物理自然键计错。
5. 真正全 NULL、无本次 source 按 ID 或 operation 声称的旧 correction 保留 legacy 边界；不因其有 `procurement:` 前缀、同一 attempt 今天已有新 source，或今天新增了 binding/p 就判孤立。本片不猜其出生、不要求旧件重新带新 pointer、不授 Qualified。
6. 同 attempt 的旧 correction 与新 correction 使用不同 operation 合法；本次只要求新 source 声明槽的精确归属。所有原件的原标量及旧 Base 关系仍按既有范围执行，本片的 legacy 接受不抹除其他域已有矛盾或 gap。

## 5. 错误归属与失败语义

关系矛盾统一记 `OriginalPointerRows`，按 `(物理表,自然主键)` 去重；本片只给实际 `pp_approval_consumptions`、`pp_unmanaged_correction_sources` 和 `provider_cost_corrections` 记关系错误，不给缺失依赖造假的物理行键，不顺带把借用的合法 proposal/attempt 计错。

- source 指定原件缺失、错误内容、pointer 或依赖不可唯一取得：记该实际 source，及按指定物理 ID 能找到的全部实际 correction。
- operation 反向集合不唯一或与指定 ID 不同：记该 operation 的全部实际 source，以及该 operation 的全部实际 correction；另保留各 source 指定 ID 的实际 correction。这样错误的指定 ID 与正确 operation 的额外旧件都不会漏计。
- 原件自身带 pointer 却无唯一正确声明者：记该实际原件；能确定的实际 claimant 同时记。无 claimant 不虚造 source 键。
- source/原件物理重复仍以各真实自然键一次计数；同一 source 的多个字段或多个原因不增加计数；跨表相同文本 ID 分开计。
- 原 scalar/shape、其他关系/链错误由其责任叶子独立累计；不得以其他叶子已经计错为理由省略本片必需检查，也不得从别域 Stats 复制或扣减。

取消、绝对超时、资源错误立即返回错误；已有内存计数不作为部分成功结果输出。复跑同一 state 的关系标记不增长，不改变 raw 输入与数据库。

## 6. 独立验收矩阵

1. approval、unmanaged 两套独立字面 source/原件 fixture；proposal/attempt/request/birth 桥完整，所有 expected 不由 writer、别域 helper 或被测关系函数生成。
2. 原 correction 的 tenant/attempt/operation/amount/basis/evidence 六字段及六 pointer 单项合法形状变异，source root/step/kind/xid、输出 ID 单变异；原件时间与 source 诊断时间不同合法，非 finite 拒绝。
3. 原 attempt 缺失/重复、tenant/request 错；proposal/birth 缺失/重复、错 proposal、scope、requester、approver、approval operation、state、expected revision/count。缺依赖时 source 与实际输出均计错，不造不存在原件键。
4. source 同物理 PK 但不同操作、原 correction 同物理 ID 不同内容、相同 operation 不同 ID、跨 cause 相同 operation/输出 ID、同原件多 claimant；乱序与自然键去重结果不变。
5. 指定原件缺失仍有同 operation 全 NULL 旧件；指定 ID 的 operation 错且另有预期 operation 的全 NULL 原件；清空指定原件 pointer、部分 pointer、正确 source ID 但 wrong role/kind。均保留正反向实际错误对象。
6. 同 attempt 不同 operation 的零额、同额、basis-only 多更正合法；unmanaged operation 非 UUID、以 `procurement:` 开头、不同 actor 不进入 correction 身份；跨族真实 operation 碰撞拒绝。
7. 没有本次声明的全 NULL 旧件合法；同 attempt 已有本次新 source但不同 operation 的旧件合法；旧 approved proposal 与今天 p、成本、绑定/角色变化不被错误比较。legacy 接受不发 Qualified。
8. 同 source 多缺陷与重复调用物理计数稳定，跨表同文本 ID 不合并；预取消、中途取消、绝对超时、大集合预算及完整入口错误零结果；不写 PG 测试、不将 fixture/race 当作供应商、商业或生产验收。

## 7. 交付与保留项

本片合同审定后才实施两个独占文件；根集成人接线并更新受影响的统一架构正文、验证记录、changes 与清单，执行最终只读检查。该内容关系片不解除 PB 完整历史观察、未派生 Reconcile latest/sent 观察、完整业务资格、逐对象依赖/Qualified、writer/guard/ACL 与真实部署验收的剩余义务。

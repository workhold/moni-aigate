# B-P3：采购共享准入与绝对更正的历史缺证

2026-11-03。本记录是 [B-P3 完成门禁](b-prefix-profile-contract.md#6-分工与完成门禁) 的未完成项，先于补充组合测试冻结。它不修改原 checker 的接受集，不把 B-P1 的局部 prefix PASS 当成完整 B-P3 验收。

## 实际状态与代码依据

### 共享账户的多来源终态历史

同一采购账户的四次真实准入（每次 upper=240，总 upper=960，初始 limit=1000）合法完成两次成本结算、一次无消费释放，并保留一次 held=240。账户从创建起保持 active/revision=1、没有配置变更或异常，资金投影与 PB 跨窗证据均一致。但原完整采购 checker 没有逐次准入的不可变账户前缀证据；其 `accounts()` 仅在账户配置恒定且“只有一个 source，或全部 source 都只有 reserved 事件”时认证准入。该历史 sourceCount=4，含终态事件，故 `allReserved=false`，原 checker 保留 `AccountAdmissionOrderUnproven=1`，其他采购 Unproven、Structural 与确定 mismatch 均为 0。

依据：`internal/fundinglotverify/procurement_evidence_graph_accounts.go` 的 `provable := constant && (count == 1 || allReserved[id])`。即使总 upper 小于限额，也不得修改既有接受条件、按当前账户金额推断全部过去准入，或拆换账户以隐藏此历史边界。这是独立于 `pb_now` fixture 的来源证据不足，不是请求跨窗归属错误。

### 绝对成本更正

真实 APP 通过 `billing.Store.ProposeProcurementCost` 保存当时采购 reservation 的 `expected_reservation_revision` 和 evidence 数；不同 finance approver 执行 `ApproveProcurementCost` 时先核对屏障，再调用 `updateProcurementHold` 追加绝对成本更正，递增 reservation revision，最后将 proposal 标为 approved。已批准提案保存的是执行更正之前的 revision，当前 reservation 保存更正之后的 revision。该差异是合法时序，不能重写历史提案以等于当前值。

原 `cost_recorded` 是正常终态成本的唯一历史 outbox；后续 `cost_corrected` 追加新绝对成本，不改写旧记录。原 checker 在存在更正事件时无法由当前行证明旧记录当时的完整资格，因此另保留 `CostRecordedEligibilityUnproven`。真实 24→25 的 invoice 更正同时得到两项 Unproven 各 1，确定 mismatch 仍为 0；不能只固定提案屏障一项而忽略另一项。

依据：

- `internal/billing/procurement.go` 的 `ProposeProcurementCost` 与 `ApproveProcurementCost`：旧屏障核验 → `updateProcurementHold` → correction 原件 → proposal approved。
- `internal/fundinglotverify/procurement_evidence_graph_evidence.go` 的 `procurementEvidenceIndex.identities`：approved proposal 的 expected revision/evidence 数小于当前值时保留 `ProposalBarrierUnproven`。
- `internal/fundinglotverify/procurement_evidence_graph_events.go` 的成本记录资格检查：`cost_corrected`/`evidence_disputed` 保留历史 `cost_recorded` 的 `CostRecordedEligibilityUnproven`。
- `internal/fundinglotverify/procurement_evidence_graph_test.go` 的 `TestCheckProcurementEvidenceGraphInvoiceAndProposalBarrier`：明确同时固定这两项历史缺证；不能为本项消除原 gap。

## B-P3 验收边界

跨窗共享账户组合保留两个可独立选择的验收子项：实际 APP 完整 source71/635 内的 PB prefix 必须 Balanced/CoverageComplete、认证全部 17 条命令，保留原 Base 的 17 个 `OperationSnapshotPrefixUnproven`，并验证三 scope 六窗口、12 个 allocation、21 个 effect，以及真实 SQL 锁屏障后的观察顺序；同一历史的完整 aggregate 则必须因精确的 `AccountAdmissionOrderUnproven=1` 为 indeterminate。原 owned V3 Plan 必须独立读完同一完整来源并放弃，消耗 once、撤销 authority、没有 COMMIT、epoch 或初始化输出，不继续 basis/Init/observer/runtime 成功路径。

新增真实 APP 组合测试应证明：双人提案/批准产生非零绝对更正，旧 proposal revision 原样保留，当前 reservation revision 递增，供应商绝对成本正确；完整 source71/635 扫描及 PB prefix 读取完成，但采购 `ProposalBarrierUnproven=1` 与 `CostRecordedEligibilityUnproven=1` 仍共同阻断 aggregate coverage，其他采购 Unproven 和确定 mismatch 为 0。原 owned V3 Plan 必须 fail closed，不得写入或发布 epoch，不得绕过未覆盖来源完成初始化。

PB 局部成功、完整来源缺证拒绝、其他单 source 历史的 pre71 → basis112 → Plan/Init → post87 → observer → actual APP RuntimeProof 成功分别记录；不能将它们拼成共享账户跨窗历史或绝对更正历史的完整成功。上述拒绝通过不能替代 §6 的成功门禁。B-P3 整体保持未完成。

## 下一独立 SDD 前置

需要先设计可独立重放的共享账户准入前缀、原始采购屏障和历史成本记录资格证据，至少回答：每次准入消费的账户配置 revision、held/spent/limit 前缀与前驱事件如何不可变绑定；审批具体消费了哪一份 reservation revision、哪一个完整 evidence 集、审批事务如何与唯一绝对成本事件/新 revision 绑定；旧 `cost_recorded` 的资格如何与最终证据和其后更正顺序绑定；多次更正、乱序或重复证据、unknown 消费、审批前后并发更改如何区分合法旧状态与不可证明状态。先定义不可变合同、状态机、权限、迁移/历史兼容与负面验收，再实现；不能仅以当前 revision 推断审批当时正确，也不能向历史行补造证明。

本地跨窗组合沿用既有 B-P1 的独占 fixture clock 策略：只在来源出生前固定 `pb_now`；报价在 INSERT 出生时显式声明早于全部历史准入的 effective_at（数据库实际时间前 7 天），不更新任何既有行时间或报价。完整 V3 验证前从 SHA 固定的 original020 恢复函数，再由独立固定 V3 catalog 核验。它是本地历史 fixture，不是生产时钟配置接口。


2026-11-03设计后续：[B-P4设计稿v0.1](b-procurement-prefix-design.md)已汇合原采购十一项缺证的证据需求和实施关口。[权限/事务方向](b-procurement-writer-authority-contract.md)已冻结，[独立身份叶子](b-procurement-identity-contract.md)已实现并获限定PG差分验证；完整DDL/受控根/ACL、来源/锁图、读集与新profile仍待D1汇合冻结。尚无新增完整采购历史证明或完整成功验收，本文的B-P3拒绝边界保持。

# C0-R1-B-PB：非空周期预算历史来源缺口

2026-11-03，根在完整 APP composer 实施与真实非空验收中复核。本文件记录现有行为与后续设计门禁，不授权放宽当前 verifier，也不将尚未设计的新字段/迁移写成已实现。

## 已确认的现有边界

`internal/fundinglotverify/periodic_budget_graph_commands.go` 的历史配置回执检查保留 `OperationSnapshotPrefixUnproven`：

- replay 收到任意 `HeldDelta != 0` 时，该 control 的 `zeroHeld` 为 false；不能仅凭终态 held 回到零，就证明过去配置回执中的金额。
- 配置发生周期已存在 window，或不能证明零 held 前缀时，回执的历史 snapshot 不被当前表终态自动认证。
- `internal/periodicbudget/verifier_integration_test.go` 的真实配置、window 与 customer/supplier 预占、结算、退款用例已经明确要求 `Indeterminate`。这是既有安全边界，不是新 composer 的 SQL 或类型错误。

新 composer 要求同次 84 表扫描的全部 mismatch 与 unproven 为零，所以此类数据库必须返回 nil proof / 固定 ErrRuntime。即使资金总额平衡、当前窗口缓存相等或真实业务 API 正常执行，也不能跳过历史来源缺口。

## 本轮可以和不能证明的内容

真实空 scope 的周期配置、其命令/版本/权限以及其他完整非空资金图可单独验收。已发生 held 历史的既有 scope 首次配置会保留缺证；这类真实场景应作为拒绝用例保留。

空 procurement account / 空周期 control 不代表真实供应商 attempt 或非空周期预算已完整验收。上游单 attempt 的正常成本链可以另用原 API 验证；PB 活动历史必须先补本节证据设计，再补完整经营正例。

## 下一设计必须回答的问题

1. 如何证明每份 command receipt 所观察的精确事件前缀、control 版本和窗口集合，且与原实际事务绑定。
2. 如何证明 window 的创建 cut、配置生效边界及 carry-held 来源；并发 scope sync、预占、结算、更正、跨窗口释放和配置变更不能仅按 wall-clock 排序猜测。
3. 所需来源是否已存在于不可变事件/版本/XID；若不足，先冻结 append-only 存储、角色最小权限、不可变性与完整反向关系，再追加迁移。不得修改已应用 SQL。
4. 旧历史如何继续明确缺证，哪些可认证，哪些需要受控离线补证；不能用自报摘要、当前余额或自动重签原件冒充过去的事实。
5. 新静态 catalog/profile、manifest、历史 V2 → current 兼容如何同时演进；既有严格拒绝和旧 roots 必须继续成立。
6. 真实 PostgreSQL 并发/时间/故障/跨租户正负例、资源与 EOF、APP/worker 权限和非空 runtime 验收如何覆盖。

## 后续合同研究结论（尚未冻结实现）

现有资金 `event_seq` 只排序金融事件；空 window 创建不增加该序号，operation 也没有独立 observation cut。同一个资金前缀下，“先 command 再建空窗”与“先建空窗再 command”可能留下同样的终态图，不能靠 `created_at`、UUID、xid 大小或当前缓存倒推出真实先后。原 xid 只能作事务等值绑定。

下一片至少需要不可变 command cut（原 operation/control、资金 seq+tail、配置 revision、所观察窗口及原 top xid）和 window birth 见证，两者使用同 control 锁内分配的共同严格 ordinal。按原事件精确前缀重放金额，不能用调用方自报摘要替代来源。每个新 command（含 no-op）和新 window 必须各有唯一见证，见证与原件完整双向对应、同原事务，序列连续且 head/previous 闭合。仅补 window 的资金 seq 仍不足以区分同一资金 cut 下的动作。

`internal/operations/periodic_roles.go` 当前允许 APP 直接 INSERT 六张 PB 表；因此仅在 Go Commander 先行加锁不够，须同时设计数据库生成 cut、覆盖输入、防伪造/孤立见证和原件不可变 guards。不得因此直接引入未经审定的 SECURITY DEFINER。若还声明配置对 admission 的精确生效边界，须在真实配置变更处保留来源，而非用稍后 receipt 的观察点替代。

并发验收须覆盖既有 root→control 锁序、首配 scope barrier、RC 锁等待后的新快照、同事务多个 command/event/window、savepoint/rollback、死锁和未知 COMMIT。`pb_view_at` 的 STABLE 语义也须纳入真实 PG 验证，证明 cut 与返回 snapshot 来自同一前缀；这里是待验证的合同要求，不是已确认的现有缺陷。

升级只追加迁移。既有 operation 保持原始字节和已证明/缺证分类，旧 window 可有明确升级 anchor 的已存在集合，但不能回填虚构出生顺序，也不能追溯消除旧 operation、legacy membership/effect 等其他 gap。新增表列/函数 ACL 将改变当前 84 表读取及静态 catalog/basis/readset/profile、22 迁移边界，须另审新 descriptor 与历史 V2→新 current 映射，保留旧 roots/API 和严格拒绝，不自动重签旧证明。

## 不允许的捷径

不把 `Indeterminate` 当成功；不忽略或清零 `Unproven`；不改 `pb_now`、伪造 receipt/window/事件顺序、回填 live expected roots、跳过 PB 域或降低现有测试。当前 R1-B 库实现与限定测试不等于完整资金运行能力已经交付；此缺口收口前，R2 不得据本轮报告打开在线资金写入或 ready。

当前范围与证据见[运行核验记录](../../docs/verification/runtime-composer.md)，剩余工作在[交付计划](../../docs/design/delivery-plan.md)与[任务](plan.md)继续追踪。

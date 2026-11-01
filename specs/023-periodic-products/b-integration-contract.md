# 023-B 核心接线冻结切片

2026-11-01，根集成人冻结迁移、受限角色及 billing 接线；承接 [B 主合同](b-contract.md)、[Tx helper](b-tx-contract.md)、[Reader](b-reader-contract.md)及[Commander](b-commander-contract.md)。不授权提前宣称 HTTP/UI、真实 main、完整恢复与性能验收通过。

## 1. 迁移与兼容

分配追加迁移 `020_periodic_budgets.sql`，按顺序完整采用已审 `b-core.sql`、`b-commands.sql`，末尾为全部 `pb_` 函数固定实际 schema/pg_catalog/pg_temp 的 search_path。不改 001–019，不创建默认 control，不回填旧请求的 creation stamp 或伪造历史窗口。迁移以原安装器的单事务应用；不自动导入旧占用，首配沿 Commander 的根屏障与 LIMIT1001 合同执行。

独立 022-M/Q 版本1和023 SQL helper fixture继续显式安装真实001–019前缀，再安装自身设计SQL；用 [历史前缀安装器](../025-production-ha/migration-prefix-contract.md)，不得将新应用表静默混入旧版本 attestation。普通 Apply/Verify 仍要求完整最新版本。020 不自动升级运行中的评审预览。

`operations.ProvisionFor` 对六张预算表先撤销 APP 全部表/列级权力，再按 `b-permissions.sql` 授予 SELECT/INSERT 与精确投影列 UPDATE，禁止 DELETE/TRUNCATE/历史修改；projector 无六表读写权。历史019隔离 fixture 可在确认六表全部不存在时跳过此扩展；部分存在必须拒绝，不能以可选表掩盖损坏的新应用 schema。生产启动通过最新 migrations.Verify 后另验证预算角色权限。APP/投影角色均无 owner/schema CREATE/角色提升能力。

## 2. 客户预占与收尾

Reserve 保留 tenant/member/user/Key/model/group 授权与钱包等待前冻结零售价；随后 LockCustomerTx → 全部 AVAILABLE/HELD 账户按ID锁定 → 创建 request/reservation → AdmitCustomerTx(R) → 原 postLocked 与预算原 reservation effect 同事务。request BEFORE INSERT 的 pb_admitted_at 在全部账户等待后生成。Key expiry/request deadline 在最后等待后重验，任何限额、时钟或资金错误整体回滚，包括 idempotency、request、reservation、journal、预算及恢复事实。

finish 保留 lockRequest 与必要 model UPDATE 升级，然后 LockCustomerTx → 原受影响账户锁 → 最后 DB 24h 判定 → 唯一 settlement/原源终态 → FinishCustomerTx。重复 settlement 仍只读原结果；不复验当前cap、当前授权或当前价格。Refund 不调用周期 helper，不减少 spent，不把退款视为重置。

## 3. 采购事件与派发

RecordAttempt 只建立一次客户 reservation；新 managed attempt 沿原父锁和报价检查 → account NO KEY UPDATE → LockSupplierTx → 原累计准入 → 完整 prepared/held=U/spent=0 源 → AdmitSupplierTx(U) → 精确 reserved 019 event 与预算 mirror。全部等待后，以该 source 的 Admission.At 重验 Key/request/24h与当时有效报价。无managed binding保持无采购source，不建立空control。

普通/冲突 Finalization、RecoverProcurement、双审更正/提案沿 lockRequest → attempt → account → supplier control → procurement reservation。持 supplier control 后不再取得 customer control 或 ledger。`procurementHold` 私有保存同tx SupplierLocks，所有019预算event在更新原账户后调用 ApplySupplierEffectTx，原event ID精确传递；任何 mirror 失败整体回滚。金额不变但存在019事件时同样写mirror；无金额/预算状态变化且原来无event的 sending 不造event。legacy catchup、unknown/disputed、超U绝对成本与有利收尾按原合同，不以当前cap挡收尾。

这里澄清 Tx 合同“已有全部客户control”的适用范围：只指本路径本来要求客户control的场合（PrepareDispatch）。普通采购记录/收尾不新增客户control；不能把 supplier helper 前置误读为允许持account后补取客户control。

PrepareDispatch 沿原 principal/channel/credential/reservation/attempt 锁 → LockCustomerTx → account → supplier control → procurement reservation；显式无采购令牌只由 LockSupplierTx("")构造。先完成019累计/绑定/state检查，更新采购source sending（仍未提交），再 PermitDispatchTx 返回唯一权威 At；以同一个At复验Key expiry、request/24h及原冻结报价的有效版本，更新request dispatching并COMMIT。改造成本查询为接受内部At的私有函数，HTTP不接受时刻；不能继续用独立clock重选报价。失败回滚sending/窗口/高水位。重复或未知许可不能重发生成请求。

## 4. 安全错误与验证

helper受控错误在billing边界映射为安全 `billing.Error`，保留私有原cause供原40P01/40001有限事务重试；Error()不反射原SQL或金额。客户 periodic_budget_exceeded 为402；采购 procurement_periodic_budget_exceeded、periodic_clock_regression、periodic_budget_invariant/unavailable 为503。现有gateway保守终止策略不变：不新增自动候选重选，不记不存在的attempt为not_sent。

必要验证：最新真实迁移+受限LOGIN运行原账务回归；客户双scope单钱包预占/唯一收尾、退款不减spent、cap失败零事实、disabled持续记账、跨窗carry与原窗收尾；managed每attempt完整U、累计与周期交集、全部019event原子mirror、unknown/disputed/绝对更正/legacy catchup；派发三scope收紧、最后锁等待跨期/Key过期/成本生效、回滚后仍prepared、已sending不可重放；首配与流量并发、真实角色负权限。owner仅随机schema/角色/fixture时钟与清理，不替代真实APP业务写入。后续HTTP/前端/main接线单列验收。

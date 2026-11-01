# C0 维护 Gate 与快照细化架构

本页描述 022-C0-T0/T1 库级维护基础设施。统一入口见[系统架构](README.md)，锁合同以[T0 §10](../../specs/022-self-service-payments/c0-permit-contract.md#10-根冻结t0专用连接双session-gate与清理)为准，Snapshot 以[T1 根冻结](../../specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md#10-根冻结范围与尚未开放的能力)为准。没有普通 main、CLI 或资金调用者；T1 最终真实 PG/race 115.412秒、46顶层/124终态全部通过，见[新验证](../verification/maintenance-snapshot.md)。

```mermaid
flowchart TD
    Caller[库级调用方]
    Connect[深拷贝配置与Pins / 新专用物理连接]
    Preflight[非权威预检 / 实际LOGIN及endpoint / 目标OID]
    Locks[402→401 session锁 / 明确false等待50ms
明确true各一次 / 精确核全部advisory集合]
    Gate[共态Gate / 无维护授权]
    Begin[一次顶层RR / 私有tx与总deadline
固定SET LOCAL / 不公开Conn或context]
    Plan[Plan READ ONLY / 无写型锁]
    Init[Init READ WRITE
首次SELECT前ONLY 125全局C序SRE]
    Catalog[唯一内置evidence stage
同rawTx完整InstallationProof / 全程busy]
    Finish[单次Commit或Rollback
明确成功、明确回滚或Unknown分别保存]
    Cleanup[Close独立最多5秒
确认idle才401→402释放并核零锁]
    Destroy[失败或到期 / 原子invalid
封闭registry并关原始socket / 操作返回后有界pgx清理]
    Future[T2–T4后续
history与v1、真实Permit、账户行锁
独立来源、basis及epoch]
    Caller --> Connect --> Preflight --> Locks --> Gate --> Begin
    Gate -->|不建立Snapshot| Cleanup
    Begin --> Plan --> Catalog
    Begin --> Init --> Catalog
    Catalog --> Finish --> Cleanup
    Begin -->|失败| Destroy
    Catalog -->|失败| Destroy
    Finish -->|提交未知| Destroy
    Begin -. 总期限覆盖idle和CPU间隙 .-> Destroy
    Cleanup -->|非idle或清理未知| Destroy
    Catalog -. 尚未接线 .-> Future
```

## 专用连接与双锁

[Gate](../../internal/maintenancetrust/gate.go)及[预检](../../internal/maintenancetrust/identity.go)使用新物理连接，不借还 runtime pool。Acquire 共用父期限与30秒的较早值，只有明确 false 才每50ms轮询；每把明确 true 后不重入。核本 PID/DB 的完整 advisory 集合，拒绝额外/shared/two-int 或丢锁，不调用 unlock_all。sandbox 保持字面 127.0.0.1:55439/moni_test 及实际 moni_test LOGIN；super/datdba 维护授权留 T2。

## 单次快照与固定 catalog stage

[Snapshot](../../internal/maintenancetrust/snapshot.go)只由 Gate 一次 BeginPlan/BeginInit 发行，保存直接 BeginTx 产生的同一个私有顶层 pgx.Tx。无 Tx/Conn/PgConn/Context/任意回调出口，不制作新 proxy 绕过 opaque proof 的 wrapper 身份。Plan RR RO 无 SRE；Init 固定 SET 后、首次任何 SELECT 前，执行[静态125表](../../internal/maintenancetrust/snapshot_tables.go)的一条完整 ONLY/全限定/C序 SHARE ROW EXCLUSIVE 命令。账户行锁只能等未来真实 Attest 完成，T1 不查询账户业务行。

VerifyInstallation 在同一个 rawTx 调用 fundinglotcatalog 的真实 issuer、完整 pins 匹配和出口复核；全程占有 stage busy，Commit/Rollback/Close 或第二 stage 并发立即拒绝。保留 issuer 五秒预算和固定 expected profile；不把 catalog 成功当作完整 migration history、维护授权或资金证明。返回 wrapper 与内部持有 wrapper 分离，调用方赋零不改变内部证据。

## 期限、终结与物理清理

Begin 无 deadline 默认30秒，有 deadline 取父期限与10分钟较早值，剩余不足一秒拒绝。每 stage 同时受原 Snapshot 和该次 caller 取消/期限限制，后续 Background 不能延长。生命周期 watcher 在 SQL 阻塞、CPU 间隙或无人使用的 idle Snapshot 也立即 invalid 并关闭捕获 socket；它不并发调用 pgx。晚到 CancelRequest dial 遇已封闭 registry 立即关闭。

Commit 一次调用开始后按可能已发送处理：明确 COMMIT、明确 ROLLBACK/服务器回滚、无法确认分别保留 Committed/RolledBack/Unknown。错误不 unwrap driver；Unknown 不重发、不能由后续 Rollback 改成确定未提交。Rollback 独立最多5秒且不延长 Snapshot 授权。Close 遇活跃 stage 立即 busy；未显式终结的无人使用 Snapshot 直接物理关闭并保留 Abandoned，不能擅自提交。已确认 idle 才沿同一5秒 cleanup 预算逆序解锁并核空集。副本共态，重复终结/清理不重复发 SQL。

## 证据与后续边界

[T0专项](../verification/maintenance-gate.md)保留原代码版本和运行范围，不自动升级为 T1 证据。本轮新增[真实 PG 验证](../verification/maintenance-snapshot.md)覆盖唯一 RR、首次快照物理 writer 屏障、125表锁、idle期限、busy、真实提交丢回复与明确回滚；最终race 124终态全PASS，独立清理为零。首轮测试语句子串误判已保留原日志并修正，未改产品锁/预算。

[前驱矩阵候选](../../specs/022-self-service-payments/c0-predecessor-contract.md)包含 App v1、M/Q 实际字段、ordinary-off 21表及6旁路完整语义；没有假造 active key、peer 文件原件或 v1 不存在的字段。T2 的 manifest 仅能来自编译期 migrations 并在同 RR 核实际 history，叶子依赖仍待其根冻。Permit/ReadLease、账户锁、独立全源结果、basis、epoch、startup RuntimeProof 与生产接线继续后续。

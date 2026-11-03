# B-P2：固定原 001–022 与候选安装隔离

2026-11-03；基线 `6bed3a98052b26d42a3e5dd5e447968da39331ce`。本合同先于实施冻结，是 [B-P1 §1](b-prefix-contract.md) 的第二项依赖。B-P3 完整读取/profile/组合及最后正式迁移继续独立门禁。

## 1. 范围与旧接口

新增固定历史材料和同事务核验，供隔离候选安装使用。候选安装接缝放在 `internal/testsupport/periodicprefix`，只接受专用本机测试数据库与调用方独占的随机 schema；不提供生产安装入口，不接入 main、Compose、provision 或正式迁移。测试支持包不发行 CatalogProof、Permit、RuntimeProof 或资金许可。

不修改原 `migrations.Apply/ApplyThrough/Verify/VerifyThrough/VerifySnapshotTx/VerifyReadOnlyTx` 的语义或源码；旧 operations/provision、维护生命周期、reader、basis、catalog roots 与 golden 保持。原 schema 首次应用 001–022 与原 provision 可先完成；本片要求**历史核验、候选 DDL、候选授权及授权复核**处于同一原事务，不声称 22 份原迁移首次应用也在该事务。

## 2. 精确历史材料与 API

`migrations.Original22Manifest() ([]byte,error)` 返回一份自有的原始 canonical JSON 材料，固定 22 项 name/SHA、2459 bytes、SHA-256 `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`。每项核对对应原嵌入 SQL；未来额外 current 文件不改变这份历史材料，原文件变化则拒绝。不能从新 current 动态重签历史，不能由 caller 提交 FS/manifest/target。

`migrations.VerifyOriginal22SnapshotTx(ctx,tx,schema) error` 只接受 caller-owned RR（RO/RW）与明确的单个 schema。使用 schema-qualified `ONLY schema_migrations`；固定读取 name/checksum、最多 23 行，逐项验证完整 22 项、重复、额外项、缺项和错误 checksum。临时 schema 或其他 search_path 的同名表不能替代目标。nil/取消/已结束/非 RR/无表等返回固定安全错误，不返回数据库诊断或历史值。

该函数不 Begin/结束事务、不安装、不取得 advisory/业务锁、不校验 schema/catalog/角色资格，也不授予停服权限。自身事务内未提交的修改应立即被看见；其他连接后来提交的变化遵循原 RR 快照。SQL 执行错误可能使原事务 aborted，由调用方 fresh 有界回滚；逻辑拒绝不隐式结束原事务。

## 3. 候选固定材料

纯包 `internal/periodicbudgetprefixschema` 内嵌候选 DDL 与权限模板的逐字副本，并固定两份 SHA。测试同时逐字比较 specs 中原件，不能只用运行时实装结果当 expected。`SQL()`、`Digests()` 与 `Permissions(app,projection)` 只返回普通自有数据/文本；标识符非空、不同、无 NUL、最多 63 bytes，使用 `pgx.Identifier.Sanitize`。

纯材料包不连接数据库、执行 SQL 或依赖 maintenance/catalog authority。本片不形成正式 catalog inventory/root；旧 root 必须继续拒绝新 schema。权限矩阵包含准确 **12 个函数签名**，不按运行时发现结果自动扩名单。

## 4. 仅测试支持的安装状态机

`periodicprefix.InstallTx(ctx,tx,Pins{Schema,AppRole,ProjectionRole}) error`：

```text
caller 独占随机 schema / 原 provision 完成
  → 已有顶层 RR RW
  → 私有 savepoint
  → 身份 / 专库 / schema / 固定 original22 同事务核验
  → 完整候选缺席检查
  → 固定 DDL → 固定最小权限 delta → 候选权限复核
  → RELEASE 私有 savepoint
  → caller 唯一 COMMIT 或 ROLLBACK
```

只接受 `127.0.0.1:55439/moni_test` 上的实际 owner `moni_test`，caller 当前 schema 必须是明确 pins 中的随机测试 schema，不能 public/系统 schema。内部设置明确 `schema,pg_catalog,pg_temp` 查找路径，避免临时对象抢占。APP/projection 须为不同实际非高权 LOGIN，不能等于 owner。

安装器不复制 Gate、创建连接或申请 session advisory lock。隔离随机 schema 的排他由测试 fixture 拥有，不把这条边界推为生产停服互斥；将来正式迁移仍须按生产合同安装。不得在与其他 schema/角色变更并发的事务上使用此测试接缝。

任一失败以 fresh 总计 5 秒 cleanup 回滚私有 savepoint；回滚失败返回固定 cleanup 错误，caller 必须回滚整个原事务，无法确认 idle 时关闭连接。成功只 RELEASE，不结束顶层事务。DDL、ACL 与前置检查失败后不能留下可提交半片。取消不产生自动重试；最终 COMMIT 丢回复属于 caller 的 unknown，不能据此重装或执行修复。

重复安装、任一新三表/row type、新 source 列、候选同名 constraint/trigger、任何 `pb_prefix_*` 函数及 overload 均在 DDL 前拒绝。候选末尾 DO 遍历此前缀，因此检查范围必须覆盖前缀全部函数；不采用 IF NOT EXISTS/部分修复。

## 5. 权限隔离

原 `operations.ProvisionFor` 必须在候选之前执行；其 ALL TABLES 广授不能在候选后再次执行。新路径只执行冻结最小 delta，并在同事务独立验证：

- APP 新三表 SELECT、准确 INSERT 列、仅 head 两列 UPDATE；新 source pointer/stamp 不获 UPDATE。
- APP 准确 12 个函数 EXECUTE；函数固定 search_path、SECURITY INVOKER，无额外同名前缀 overload。
- projection/PUBLIC 无新增表/列/type/function 权限；APP 仅获三种 row type USAGE。
- 验有效权限与可 SET/ADMIN 到达角色闭包、owner、高权属性、grant option，拒绝借 PUBLIC、默认授权或继承外部角色扩大权限。

这是候选的局部安装/ACL 检查，不代替 B-P3 的全 schema catalog、依赖闭包、完整 transport 或 proof。原 source 表既有 INSERT 语义保留；新 pointer/stamp 仍由受审 BEFORE 生成。

## 6. fixture 与验收

新增明确 original22 fixture 路径，固定 `ApplyThrough(...022)`，不经 latest factory 漂移。旧 latest fixture 行为保持。删除原“current 恰为 22”临时防线时，必须同时用固定历史材料、显式历史 installer 与同事务校验替代，不能 SKIP 或弱化检查。

实际 PostgreSQL 验收：完整成功、caller rollback/commit 前不可见、RC/RO 拒绝、历史缺/多/坏及自身未提交变化、部分候选/前缀 overload 拒绝、DDL 中途失败、ACL 后失败、取消后无半片、旧 provision 再次扩权被检出、PUBLIC/列级/默认授权/继承和 SET/ADMIN 越界、旧 roots 拒绝候选。保留原 actual APP 三 scope、真实 reserve/settle/release/更正与并发故障全部专项。

最终由根汇合专项、静态检查、架构/HLD/LLD/README 能力边界及 GitHub 交付。下一依赖继续为新完整 scanner/basis/catalog/report profile、protected pre/post/observer/runtime，然后正式 append-only migration/provision/startup 与完整经营验收。R1-B 完整验收和 R2 在线资金 ready 本片均不开放。

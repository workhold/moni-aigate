# 022-C0：静态 schema 与角色检查器合同候选

2026-11-01，T2状态同步于2026-11-02。**§10根冻结正式checker的实现合同；正式静态descriptor必须另经实装、双轮与独立审阅才可启用发行，未审或空root必须失败关闭。** §9离线metadata生成器已实现并通过[限定专项](../../docs/verification/funding-lot-descriptor.md)。本checker本身不授维护Permit；库级T2授权已另按[真实授权合同](c0-authority-contract.md)实现，[T2真实PG/race专项](../../docs/verification/maintenance-authority.md)已通过，不扩大本checker历史证据。生产迁移、proof v2发行、普通main或资金接线仍未开放。对应[维护与proof v2](c0-maintenance-contract.md)、[独立核验器](c0-verifier-contract.md)、[运行helper](c0-runtime-helper-contract.md)及[固定候选回归](c0-candidate-regression-contract.md)。静态检查通过不证明epoch、basis或任何资金来源图正确。

## 1. 固定输入与当前实现事实

| 输入 | 本稿固定版本 |
| --- | --- |
| [生产 DDL 候选](c0-production-schema.sql) | SHA-256 `e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047` |
| [生产 ACL 候选](c0-production-permissions.sql) | SHA-256 `9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae` |
| 旧业务列 descriptor | 维护合同 §8 原 canonical JSON：109 表、976 列，SHA-256 `54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5` |
| 已应用 SQL | 当前真实 001–022 全部 `(name,sha256)`；不能改其文件或只验证历史前缀 |
| v1 支付角色定义 | [payment_roles.go](../../internal/operations/payment_roles.go)、[43 个函数静态定义](../../internal/operations/payment_roles_functions.go) |
| 其他原授权 | [ProvisionFor](../../internal/operations/operations.go)、[periodic 角色边界](../../internal/operations/periodic_roles.go)、[alert 角色边界](../../internal/operations/alert_roles.go)；不能将支付 23 表矩阵误当全部 109 表授权 |

ACL 的旧 `d12cb73…` 版本已被根决定收紧，不是本稿期望。现有 ProvisionFor 的 tenants 是表级 INSERT、六列 UPDATE；新候选必须将 APP INSERT 收敛为原八列，UPDATE 保持原六列，不能新增 UPDATE(id,created_at)。worker 既有 SELECT(id)/UPDATE(id) 用于原 tenant 锁，保持原样；不能误撤 worker 的 UPDATE(id)。收紧后的候选已完成[版本化直接SQL回归](../../docs/verification/funding-lot-candidate.md#8-版本化直接sql回归)，元数据导出另见§9；两者均不替代完整checker或生产资金验收。

当前 [migrations.VerifySnapshotTx](../../migrations/verify_snapshot.go) 只核 caller-owned RR 和完整迁移 history，不核列、函数、trigger 或 ACL。当前 [payments v1 canonical](../../internal/payments/runtime_types.go) 绑定 DB、三 schema、八角色/OID、nonce、origin/callback 和 keyset；**v1 canonical 本身没有 migration manifest 或完整 schema/ACL digest**。这些现有启动检查分别由 migration helper、[operations.VerifyPaymentRole](../../internal/operations/payment_roles.go) 和 [runtime catalog/attestation](../../internal/payments/runtime_catalog.go) 执行，不能把它们的结果虚写进原 v1 proof。

## 2. 叶子包与精确 API（以§10根冻结为准）

已实现叶子包 `internal/fundinglotcatalog`，仅依赖标准库、pgx 及包内受审静态 descriptor；不依赖 billing、payments、operations、main 或 maintenancetrust，便于后者复用且不形成循环。它不接受外部 expected descriptor 文件、任意 SQL、跳过列表、trusted bool、权限修复参数或可配置角色继承规则。下面名称与形状已由§10根冻结；静态checker历史验收见其独立记录，T2维护授权另以[c0-authority-contract](c0-authority-contract.md)为准：

```go
package fundinglotcatalog

type Mode uint8
const (
    Ordinary Mode = iota + 1
    Sandbox
)

type RuntimeRole uint8
const (
    Application RuntimeRole = iota + 1
    Worker
    Projector
)

type RolePin struct { Name string; OID uint32 }
type DatabasePin struct { Host string; Port uint16; Name string; OID uint32 }
type SchemaPin struct {
    Name string
    OID uint32
    NamespaceOwner RolePin
    ObjectOwner RolePin
}
type SandboxPins struct {
    Mock SchemaPin
    MockRuntime RolePin
    Receiver SchemaPin
    ReceiverRuntime RolePin
}
type Pins struct {
    Mode Mode
    Database DatabasePin
    AppSchema SchemaPin
    App RolePin
    Projector RolePin
    Worker *RolePin
    Sandbox *SandboxPins
}

// 全字段私有、共享内部identity；nil/zero/copy均不产生新的授权。
type CatalogProof struct { /* identity, pins, descriptorVersion, digests */ }
type InstallationProof struct { /* original CatalogProof, emptyAtIssue */ }
func (*CatalogProof) VerifyTx(ctx context.Context, tx pgx.Tx) error
func (*InstallationProof) VerifyTx(ctx context.Context, tx pgx.Tx) error
func (*CatalogProof) DescriptorVersion() uint32
func (*CatalogProof) ExpectedSHA256() string
func (*CatalogProof) SchemaSHA256() string
func (*CatalogProof) RolesSHA256() string
func (*CatalogProof) MatchesPins(pins Pins) bool
func (*InstallationProof) Catalog() *CatalogProof

var ErrCatalog = errors.New("funding_lot_catalog_verification_failed")
func VerifySnapshotTx(ctx context.Context, tx pgx.Tx, pins Pins) (*CatalogProof, error)
func VerifyInstallationTx(ctx context.Context, tx pgx.Tx, pins Pins) (*InstallationProof, error)
func VerifyRuntimeTx(ctx context.Context, tx pgx.Tx,
    pins Pins, role RuntimeRole) (*CatalogProof, error)
```

`VerifySnapshotTx` 只用 caller-owned 活跃 RR，可 READ ONLY 或 READ WRITE，逐项核完整 schema 与全部命名角色矩阵。它可用于 epoch 前离线审计，不要求读业务行或调用 `fl_require_epoch`。pin 是比对目标，**不是授权证据**；函数自己重解 DB/schema/role name→OID、owner 和 actual endpoint，拒绝 name 相同但 OID 已换的对象。CatalogProof 不给调用方发 Permit、APP 写能力或启动许可。构造器在任何DB操作前深拷贝pins（包括Worker/Sandbox指针），不保留caller可变引用。`MatchesPins`只比较原私有规范pins与传入pins的结构化精确值，nil/zero=false、不trim/别名化、不访问DB、不发权限；供Permit在proof.VerifyTx之后核Gate深拷贝的目标正是同一份pins。其私有 identity 绑定具体 pgx.Tx wrapper、Conn/PgConn、backend PID、top xid、DB/schema OID、mode、完整 pins 和受审 descriptor/schema/roles 摘要；不能只传一段裸 digest 重建。nil/零值无效，拷贝仍共享同一原证据。`VerifyTx` 只在原活跃 RR/conn/wrapper/PID/top-xid 及 pins 下成立；换 tx、nested wrapper、换连接、提交/回滚/错误状态后拒绝。API 自己不声称发现任意手工 SQL SAVEPOINT；Gate 的顶层 tx 所有权另证。

`VerifyInstallationTx` 先做完全相同的静态检查，随后仅以固定 EXISTS 查询证明**全部16张C0表全空**，包括 epoch 和 funding_lot_runtime_attestations，才封装 InstallationProof。它读业务空集的范围只有这些固定新表；不读取旧v1 canonical，不验证原源/basis/PB。故它不能替代 Permit 的实际维护身份或v1校验；APP即使有全部SELECT拿到该证据，也不能据此取得维护授权。epoch存在、任一新表非空、权限不足或查询失败都不给 InstallationProof。

InstallationProof.VerifyTx 检查证据真实性与同一事务绑定，**不在 init 已按 Permit 写facts后重新要求16表全空**；`emptyAtIssue`是发行阶段事实，不是调用方可改的当前状态。已实现的T2二参 `Attest(ctx, snapshot)`仅消费Snapshot内部owned installation，在唯一authority阶段重新核pre-epoch/16表空集；Init全accounts锁及出口复核完成后才一次发行Permit。重复调用、拿旧证据在写后另发Permit均拒绝，后续校验只有 `Permit.Verify(ctx)`，没有裸tx参数。init写入与post-init全图核验仍待T3/T4冻结，不能把CatalogProof或复制的InstallationProof自动升格。epoch后没有新InstallationProof/init许可，必要的post-epoch只读/恢复另片定义。

`VerifyRuntimeTx` 复用完全相同的 schema/矩阵检查，并只接受 RR READ ONLY；额外要求 `tx.Conn().Config().User = current_user = session_user`、actual LOGIN/OID 正好是 pins 中所选角色、连接配置与数据库实际 DB/endpoint/schema 都匹配。它不允许 SET ROLE 冒充连接身份。ordinary 的 Worker/Sandbox 必须同时 nil；sandbox 两者必须完整，三 runtime LOGIN 与三 owner、mock/receiver LOGIN 必须是八个不同 OID。unknown mode/role、空名称、零 OID、typed-nil/closed/aborted tx、非 RR 或失效 context 一律拒绝；ordinary 不存在 worker 不是跳过已有对象核验的开关。

三个发行 API 都从调用入口起继承 caller deadline 并收紧为至多 5 秒总预算，不按查询重置。不 Begin/Commit/Rollback、不 SET ROLE/SET SESSION、不写临时表、不取 advisory/业务行锁、不调用候选校验函数、不执行 GRANT/REVOKE/DDL；所有查询只读静态 catalog。Rows 必须在下一查询前关闭。数据库、权限、解析、取消、资源耗尽及内容不符都返回 **nil proof + 固定 ErrCatalog**，不包装 driver、SQL、标识或行值；成功和正常业务不匹配后原事务仍归 caller。数据库取消导致 aborted 状态时由 caller 回滚，checker 不偷偷恢复。它不声称只靠 catalog 能辨认受信 caller 曾手工执行过的 SAVEPOINT；maintenance Permit 仍按自身具体 wrapper/PID/top-xid 绑定规则处理。

## 3. 静态期望的产生与审阅流程

运行时只读取 embedded、受版本控制的固定 expected。初次生成可使用**离线固定 SHA 装载**，但产物先是待审 descriptor，不能因为从某个 live catalog 导出就成为可信期望：

1. 生成器必须为显式离线工具，固定检查 001–022 文件清单/逐 SHA、上述候选 SQL/ACL SHA 及生成器自身版本。只允许专用 TCP `127.0.0.1:55439/moni_test`、独立 `flckreview_<128-bit随机>` schema/角色前缀；与资金候选作者的随机前缀不同。不连运行实例、不启动 main、不安装到 public、不触发 model/SMTP/PSP。安装/导出不是 checker API 的一部分。
2. 在受审 PostgreSQL major 上分别装真实基线与基线+C0；原角色用真实固定 provision 流程，C0 ACL 只装固定候选一次。导出前检查本轮精确对象及数据库实际版本，导出后确认原 001–022 函数 OID/正文未变、022 v1 表/记录未被 UPDATE。生成器不能为得到 desired 导出结果临时补 GRANT。
3. 导出 metadata-only canonical descriptor，分别包含 `base_schema`、`base_roles`、`c0_objects`、`base_deltas` 与 provenance。先按 SQL/授权源码人工核全部差异，尤其 worker 排除项、tenant 写列和触发器，再由另一审阅者核 query coverage/正文摘要/依赖及故障反例。原 109 表/976 列 JSON 只是 basis 的列清单，**不够**充当完整 base_schema；仍须冻结旧 trigger/function/index/constraint/type/ACL 合同。
4. 导出不保留随机 schema/role 的原始 OID 作为跨环境常量。只允许封闭符号绑定：目标 schema、owner/runtime 角色、每个确定名称的用户 relation/type/function；builtin OID、类型 send/比较函数、collation/opclass 依受审 major 固定。引用必须一对一解析，任何未知 alias/重复身份拒绝。正文不能用任意字符串替换“归一化”后接受；`prosrc` 原字节 UTF-8 SHA-256 精确比较，`search_path` 独立按三个已绑定 schema 身份比较。
5. 受审 canonical JSON 明确版本、UTF-8、字段集合/顺序与数组 C 序；拒绝重复键/未知字段/非规范数值/超界，不使用 map 迭代顺序或含环境值的 pg_dump。取得 descriptor SHA 后才可 embed；根冻结必须记录该 SHA、输入 manifest、SQL/ACL SHA、PG major 与独立审阅结论。生成器输出和 expected artifact 更新是两步，默认工具不能覆盖已嵌入 expected。
6. 后续任何 schema/ACL/PG major 变化先审新 delta/descriptor 版本，重跑负例后显式更新静态产物及 v2。生产 serve/CI 只能只读比较，不能“从当前库重建 expected”、自动 refresh/GRANT 或接纳未知表列。未来 C0 进入正式 migration 后需重新冻结其 migration name/SHA，不能让生产 expected 继续靠 specs 文件路径绕过完整 latest history。

本次仅用不同前缀随机 schema 在真实 001–022 后装固定 DDL，做了 PG16 **metadata 计数复核**：16 表、168 列、41 函数、49 个新增用户 trigger、57 索引；本轮 schema 已删除。没有装角色 ACL、发行 proof/epoch、验 caller-owned API 或生成受审完整 expected。下面计数帮助审阅，但数量相等不证明定义相等，不能将这次 catalog 当正式信任根。

## 4. 完整 schema 与类型合同

expected 是**冻结的基线全集 + 受审精确 C0 delta 的纯组合**，再与 actual 全集比较。不是先从 actual 删除“不认识的 C0 对象”再跑旧 checker。目标 app schema 中 relation/function/type/trigger/constraint/index 和已登记其他对象必须无缺、无多；不能只 `LIKE 'fl_%'` 后忽略另名附着 trigger、重载或自定义 operator。base 表总数为 109，组合后普通表 125；C0 named composite 还有自己的 `relkind='c'` 关联 relation，不能误算第 126 张业务表。

16 张 C0 表在受审 PG16 中的结构数量如下。C/F/P/U/T 分别是 CHECK/FK/PRIMARY/UNIQUE/用户 CONSTRAINT TRIGGER；索引含 PK/UNIQUE 的 backing index，不含现有表原索引：

| 表 | 列 | C | F | P | U | T | 索引 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| funding_consumption_refund_lots | 10 | 6 | 3 | 1 | 2 | 1 | 3 |
| funding_lot_balances | 7 | 4 | 2 | 1 | 0 | 1 | 1 |
| funding_lot_epoch | 19 | 19 | 1 | 1 | 2 | 1 | 3 |
| funding_lot_events | 23 | 18 | 4 | 1 | 5 | 1 | 6 |
| funding_lot_legacy_approved_refunds | 6 | 1 | 4 | 1 | 1 | 0 | 2 |
| funding_lot_legacy_openings | 8 | 6 | 4 | 1 | 2 | 0 | 4 |
| funding_lot_legacy_receipts | 7 | 2 | 3 | 1 | 1 | 0 | 2 |
| funding_lot_legacy_sandbox_receipts | 6 | 1 | 3 | 1 | 2 | 0 | 3 |
| funding_lot_legacy_settlements | 8 | 2 | 5 | 1 | 4 | 0 | 5 |
| funding_lot_runtime_attestations | 16 | 12 | 1 | 1 | 2 | 0 | 3 |
| funding_lots | 11 | 8 | 4 | 1 | 4 | 1 | 6 |
| funding_reservation_lots | 8 | 4 | 2 | 1 | 3 | 1 | 4 |
| funding_settlement_lots | 8 | 4 | 2 | 1 | 2 | 1 | 3 |
| funding_wallet_heads | 4 | 1 | 2 | 1 | 0 | 1 | 1 |
| funding_wallet_legacy_sources | 10 | 2 | 3 | 1 | 3 | 0 | 4 |
| funding_wallet_operations | 17 | 8 | 4 | 1 | 6 | 1 | 7 |
| **合计** | **168** | **98** | **47** | **16** | **39** | **9** | **57** |

每张表除名外还核：permanent ordinary heap、正确 owner、无 partition/inheritance/RLS/forced-RLS/rule/foreign/view 替换；每列 attnum/name/type/typmod/NULL/collation/default/identity/generated/drop 状态与精确列序。序列、额外 defaults、任意自定义 type/send/operator/函数替代均不接受；C0 没有新 sequence。新 16 表各自 row type/array type，另 `fl_source_proof` composite/array，共 34 个新增 type 对象；复合类型对应 relation/attribute、typrelid/typelem/typarray/owner 和依赖都须闭合，无额外 domain/enum/range/shell type。

`fl_source_proof` 十个属性顺序精确为 `tenant_id text, journal_id text, journal_operation_id text, amount bigint, available_delta numeric, held_delta numeric, consumed_delta numeric, issued_delta numeric, fifo_at timestamptz, refund_class text`。不能只核类型名或直接冻结首次随机 schema 的 type OID。runtime USAGE 仅 APP/worker；projector/PUBLIC 和未授权路径为零，无 grant option。

tenant 唯一列 delta 为 `[9,"fl_created_xid",5069,-1,false,0]`，即 nullable xid8、无 default/identity/generated、无回填；原八列顺序/定义全不变。basis 仍只编码原 976 列；完整 schema 组合总业务列数为 976+1+168=1145。新增 birth/genesis trigger 必须精确存在，不能放宽为“允许 tenants 任意新增列”。

constraint 按名字、类型、源/目标 relation 与有序列、validated、deferrable/initdeferred、FK update/delete/match 动作、CHECK 原表达式与 deparse SHA、backing index 绑定比较；NULL 语义及 composite tenant FK 不能用名字/数量代替。C0 FK 精确为 47 个，另 base 表上有 9 个新增 constraint-trigger 记录；不能将触发器约束误算业务 FK。必须特别核 head/operation 四列 predecessor FK、event 的 `(tenant_id,wallet_operation_id)` 二列 FK、event 自前驱及余额末 event FK；**不存在**的三列 `(tenant_id,wallet_operation_id,wallet_seq)` FK不能写为已实现，其 seq 等值由固定函数正文保证。

57 个索引由 16 PK+39 UNIQUE backing indexes 及两显式索引组成。逐项核表、key/include 列序、expression/predicate、method、opclass、collation、ASC/DESC/NULL 顺序、unique/nulls-not-distinct、valid/ready/live/immediate 和 constraint 对应；未知或 invalid index 拒绝。特别核 `funding_lot_one_available_opening` 的 `WHERE kind='available'` 与 `funding_lot_fifo(tenant_id,fifo_at,id COLLATE "C")`；用另一排序、去掉谓词或多一个等价但未经审阅的索引都需另审。

## 5. 41 个函数与 49 个用户 trigger

41 函数的精确 signature 和 EXECUTE 分类在下节列出。除 `fl_uuid` 为 SQL/IMMUTABLE/STRICT，其余 language、volatility、STRICT/非 STRICT 按固定 SQL 逐项冻结，不假定所有函数 flags 相同。全部必须 SECURITY INVOKER、正确 owner、固定 `search_path=<精确app schema>,pg_catalog,pg_temp`，无额外 proconfig；核参数 OID/名称/模式/default/variadic、返回类型、prokind、set-returning、leakproof、parallel、support/transform、probin/prosqlbody 和 UTF-8 原 prosrc SHA。任何重载/改名/新增函数、PUBLIC EXECUTE、grant option 或正文一字变化均拒绝。用户 type/row return 通过受审符号绑定；不能把当前库返回的 OID填回 expected。旧43函数及 funding/periodic/alert/其他基线函数也按原版本保持，不因 C0 放宽。

49 个新增非 internal trigger 的展开清单必须固化，下面闭合分组与固定 SQL 的安装 array 一致；不是运行时按当前表集合展开：

| trigger／函数 | 精确附着集合 | 时机/事件 | 数量 |
| --- | --- | --- | ---: |
| fl_immutable／fl_immutable | 上表全部，排除 heads、balances | BEFORE UPDATE/DELETE ROW | 14 |
| fl_initial_epoch_only／fl_maintenance_insert_guard | runtime_attestations、legacy_receipts、legacy_sandbox_receipts、legacy_settlements、legacy_approved_refunds、legacy_openings、wallet_legacy_sources（均用表中完整名） | BEFORE INSERT ROW | 7 |
| a_fl_stamp／fl_stamp | funding_wallet_operations、funding_lot_events | BEFORE INSERT ROW | 2 |
| fl_allocation／fl_guard_allocation | funding_reservation_lots、funding_settlement_lots、funding_consumption_refund_lots | BEFORE INSERT ROW | 3 |
| fl_complete／fl_new_fact_constraint | epoch、operations、lots、events、三 allocation 表 | AFTER INSERT ROW、DEFERRABLE INITIALLY DEFERRED | 7 |
| fl_complete／fl_new_fact_constraint | heads、balances | AFTER INSERT/UPDATE ROW、DEFERRABLE INITIALLY DEFERRED | 2 |
| fl_wallet_source／fl_source_constraint | funding_receipts、journals、journal_lines、projection_outbox | AFTER INSERT ROW、DEFERRABLE INITIALLY DEFERRED | 4 |
| fl_wallet_source／fl_source_constraint | request_records、reservations、settlements、refund_orders、ledger_accounts | AFTER INSERT/UPDATE ROW、DEFERRABLE INITIALLY DEFERRED | 5 |
| fl_head_guard／fl_guard_head；fl_balance_guard／fl_guard_balance | heads；balances | BEFORE INSERT/UPDATE/DELETE ROW | 2 |
| b_fl_event_guard／fl_guard_event | funding_lot_events | BEFORE INSERT ROW | 1 |
| a_fl_tenant_birth／fl_tenant_birth_guard | tenants | BEFORE INSERT/UPDATE ROW | 1 |
| fl_tenant_genesis／fl_new_tenant | tenants | AFTER INSERT ROW | 1 |

其中 38 个在新表、11 个在十张旧表；这些是唯一允许的 base trigger delta。每个核 enabled='O'、精确 function identity、tgtype、deferrable/initdeferred、constraint/关联表、tgargs、列过滤、WHEN、transition table；候选未指定的项必须为空。原 trigger 和约束不改正文、不禁用、不改变执行顺序。

47 个 FK 另有 PostgreSQL 自动创建的 internal RI triggers；不能因 `tgisinternal` 就全部忽略。按确定 FK identity、触发事件、源/目标、RI builtin function 与 deferrability 建闭合四条关系，每个 FK 恰四条；名称里的自动 OID 用唯一约束符号关系比较，不把随机 OID文字作为固定名字，也不放行其他 internal trigger。incoming dependency 也核：未知外 schema FK、trigger、rule/view、用户类型/operator/cast 或 extension 依赖不能挂进受保护对象；基线允许的依赖和 C0 新增边由静态描述符列举。PL/pgSQL 字符串正文中的关系并不全进入 pg_depend，因此仍必须核正文、固定 path 和整个 schema 的封闭函数/类型集合，不能声称 pg_depend 代替代码审阅。

## 6. 精确 C0 角色矩阵

以下都是**新增 delta**；原 109 表/v1 23 表及全部原函数的权限由固定 base_roles 原样组合。检查既要核 table-level bits，也要核每列有效 bits 和显式 ACL：不能用 `has_any_column_privilege` 的总布尔值掩盖 grantAll，不能将覆盖全部列的列授权当成允许未来列的表授权。未列运行数据/函数权为零，三个runtime的所有 grant option 为零；C0 表/列权限与41个C0函数的 EXECUTE 对 PUBLIC 为零。类型权限单独区分：named composite `fl_source_proof` 及其数组对projector/PUBLIC的有效 USAGE 为零；16个自动table row type及其16个数组保留PG16默认 USAGE 语义，projector有效 USAGE 为true、grant option为false，不能统一要求全部新增type为零。

| C0对象 | APP | sandbox worker | projector |
| --- | --- | --- | --- |
| 16 表 SELECT | 全部 | 仅 epoch、runtime_attestations、legacy_sandbox_receipts、wallet_operations、wallet_heads、lots、events、balances 共8表 | 全部零 |
| heads INSERT | tenant_id,epoch_id,tail_seq,tail_operation_id | 无 | 无 |
| heads UPDATE | tail_seq,tail_operation_id | 同 APP | 无 |
| operations INSERT | id,tenant_id,epoch_id,seq,previous_seq,previous_id,kind,source_id,journal_id,journal_operation_id,event_count,available_delta,held_delta,consumed_delta,issued_delta | 同 APP | 无 |
| events INSERT | id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,operation_id,journal_id,previous_event_id,before_revision,after_revision,before_available,before_held,before_consumed,after_available,after_held,after_consumed,available_delta,inference_held_delta,consumed_delta,issued_delta | 同 APP | 无 |
| lots INSERT | id,tenant_id,origin_kind,funding_receipt_id,legacy_opening_id,legacy_refund_order_id,issued_amount,currency,refund_class,fifo_at | 同 APP 但排除 legacy_opening_id,legacy_refund_order_id | 无 |
| balances INSERT | lot_id,tenant_id,available_amount,inference_held_amount,consumed_amount,revision,last_event_id | 同 APP | 无 |
| balances UPDATE | available_amount,inference_held_amount,consumed_amount,revision,last_event_id | 无，已有cache不可改 | 无 |
| reservation_lots INSERT | id,tenant_id,reservation_id,lot_id,allocation_index,amount | 无；SELECT也无 | 无 |
| settlement_lots INSERT | id,tenant_id,settlement_id,reservation_lot_id,charged_amount,released_amount | 无；SELECT也无 | 无 |
| consumption_refund_lots INSERT | id,tenant_id,refund_order_id,lot_id,settlement_lot_id,allocation_index,amount,origin | 无；SELECT也无 | 无 |
| epoch/attestation/全部legacy维护表写 | 全部无 | 全部无 | 全部无 |
| DELETE/TRUNCATE/REFERENCES/TRIGGER，及其他INSERT/UPDATE | 全部无 | 全部无 | 全部无 |
| fl_source_proof USAGE | 有，无grant option | 有，无grant option | 无 |

所有 online INSERT/UPDATE 都不包括 C0 created_xid/created_at stamp；tenant birth 列也是零写权。row type/array type 的 owner/USAGE/依赖另按固定 descriptor 核，不能混淆 named composite 的显式 USAGE 与 PostgreSQL 自动 row type 的默认语义。raw `typacl`、`default_entries` 与有效权限分别保留；array的有效 USAGE 按element类型权限判断，`_fl_source_proof` 的raw ACL为NULL、默认项含PUBLIC，也不能据此推导它的有效 USAGE 为true。类型 USAGE 不授予表 SELECT 或函数 EXECUTE，表行读取与函数调用仍须核其独立权限。当前34个新增type的精确观测与限定验收范围见[descriptor证据](../../docs/verification/funding-lot-descriptor.md#权限与依赖的实际观察)；这不是正式expected或32个自动type的逐项读取绕过测试。

EXECUTE 矩阵为 APP 39、worker 20、projector 0；owner-only 2 个。39 个授权不是允许从任意原 source/tenant构造资金，SQL仍按持久kind/真实source分支：

- **APP+worker 20**：`fl_uuid(text)`, `fl_require_epoch()`, `fl_immutable()`, `fl_stamp()`, `fl_guard_head()`, `fl_guard_event()`, `fl_guard_balance()`, `fl_check_wallet_head(text)`, `fl_check_wallet_account(text)`, `fl_check_lot(text)`, `fl_validate_new_funding(text)`, `fl_validate_sandbox_funding(text)`, `fl_source_sandbox(text)`, `fl_read_source(text,text)`, `fl_check_wallet_operation(text)`, `fl_check_sandbox_receipt(text)`, `fl_check_receipt(text)`, `fl_check_wallet_journal(text)`, `fl_new_fact_constraint()`, `fl_source_constraint()`。
- **仅APP 19**：`fl_tenant_birth_guard()`, `fl_guard_allocation()`, `fl_validate_funding(text)`, `fl_validate_reserve(text)`, `fl_validate_finish(text)`, `fl_validate_refund(text)`, `fl_validate_legacy_refund(text)`, `fl_source_manual(text)`, `fl_source_voucher(text)`, `fl_journal_lines(text,text,numeric,numeric,numeric,boolean)`, `fl_source_reserve(text)`, `fl_source_finish(text)`, `fl_source_refund(text,boolean)`, `fl_validate_genesis(text)`, `fl_check_legacy_source(text,text)`, `fl_check_reservation(text)`, `fl_check_settlement(text)`, `fl_check_refund(text)`, `fl_new_tenant()`。
- **owner-only 2**：`fl_check_epoch()`, `fl_maintenance_insert_guard()`。APP独立 VerifyLots 不获这两个 EXECUTE，不调用写侧 SQL 自证。

base tenants ACL delta 精确为：APP table-level INSERT/UPDATE=false；INSERT 列 `{id,name,status,allowed_regions,created_at,price_group_id,pricing_revision,pricing_operation_id}`，UPDATE 列 `{name,status,allowed_regions,price_group_id,pricing_revision,pricing_operation_id}`。APP既有 SELECT 和其余受审原权保持；worker原 SELECT(id)/UPDATE(id)保持，新增 birth SELECT/INSERT/UPDATE 都不授，projector零新增权。

实际 runtime 角色必须 LOGIN、不是任何 DB/schema/relation/function/type owner，无 SUPERUSER/CREATEROLE/CREATEDB/REPLICATION/BYPASSRLS，NOINHERIT；任一方向 membership（含 SET/INHERIT/ADMIN 边）都拒绝。sandbox 继续全部八角色/OID 和三个 NOLOGIN owner 的原闭包；ordinary 的 schema owner沿原实际owner规则，不能为复用sandbox任意改为NOLOGIN。public schema 的内建 `pg_database_owner` 只解析为当前 database 的真实 datdba，分别核 namespace owner 和 table/function owner；不能解析为 arbitrary current_user。runtime 自己不能通过 grantor、PUBLIC、默认ACL、间接角色或 peer schema 获取额外权限。

每角色都核整个 app schema；worker不认识的原109表仍零权（原明确 ledger/funding/audit/schema_migrations例外照旧），新分配/人工/券/request/refund或总legacy表有一个 SELECT 也拒绝。projector检查 catalog 即可，不读取 payment marker、v1/v2 attestation 或 epoch，不为“确认绑定”给它加 SELECT。sandbox mock/receiver跨 app 与对方schema的负授权仍核，C0新增type也不可变成新跨域通路。不存在的 worker/mode 不允许用普通 off 吞掉 sandbox残留。

## 7. v1 + C0 delta、维护与启动组合

C0 新路径以静态 `Compose(base_v1_schema,base_v1_roles,c0_objects,c0_base_deltas)` 产生唯一 expected，校验冲突/重复名字后比较 actual。这里的 base_v1 包括真实001–022和原 ProvisionFor 全矩阵，不只 payment43函数。旧 v1 支付函数签名/正文/ACL、23表原权限、marker/immutable证明、mock/receiver inventory 和八角色关系均保持；base delta 只能是上文已列的一列、49 trigger中的11条旧表trigger、相关9个constraint-trigger记录/RI依赖及tenant列ACL。不能放宽为“允许额外表/函数”。

旧 exported `VerifyPaymentRole/VerifyPaymentRolesFor` 和旧 v1 proof loader 继续拒绝非其原合同的环境；**不修改旧 checker，使其忽略 C0**。worker旧checker会因新表有效权而失败，这是预期。新装配不能先调用它再捕获失败改用 off，而须在显式完整 v2 分支选择静态组合检查器。可在根另审后提取内部只读 query/helper复用，但 v1期望/成功条件/负例不得改变；静态base_roles和旧operations发生漂移应令构建/专项失败，要求重新审descriptor，不由live ACL补差。

sandbox proof v2 新记录必须绑定：原 v1 canonical **原字节**SHA、原DB/三schema/八角色OID及nonce/origin/keyset摘要、完整最新migration清单/digest、本checker expected descriptor SHA与schema/roles观察摘要、C0 epoch与basis。旧022 proof row、文件、proof_version=1及canonical一个字节都不改；新v2是追加证明，不伪造旧v1曾含manifest。ordinary必须验证原off/空sandbox边界且无v1 marker，不允许把不完整sandbox解释为ordinary；这一业务行检查由专门v2 runtime完成，不归catalog checker。

离线顺序为：原v1独立验证→停全部handler/worker并取得真实维护gates→追加受审迁移且epoch仍空→同backend先取得instance再migration session锁，Gate自建顶层RR（init在首次SELECT前按C序锁固定125表）→同RR内history核验、静态schema/全角色与InstallationProof→maintenance身份/Permit与全源basis→init facts与新v2 attestation→最后epoch→独立确认COMMIT。`InstallationProof`不能代替Permit；Permit必须另核真实维护LOGIN、锁、具体tx/conn/PID/top-xid和适用mode。未知COMMIT仍只查原epoch/proof，不重发行证明。

运行顺序在任何listener/资金worker启动前：严格新v2文件与pin绑定→actual APP/worker/projector连接→完整latest history→组合静态schema/roles→APP/worker可读的原v1和v2+epoch绑定及完整源/lot核验。projector只做catalog角色检查，其environment归属由完整离线/v2装配绑定，不让它读取证明业务表。这里的次序属于后续装配合同，本片API通过仍不允许main启动或注册资金helper。

同一RR内catalog、schema_migrations与授权观察一致；取得CatalogProof不等于全源行级一致性，也不能保证受信DDL owner稍后不改对象。生产DDL/GRANT权限必须仍只在受控停服维护路径持有；runtime不缓存一份CatalogProof当永久授权。C0进入latest后原按ALL TABLES授权的ProvisionFor也须改为受审基础矩阵+C0列权限或在**同一离线provision事务**精确撤销宽权再验证，另片冻结；普通serve、checker或CI绝不运行自动GRANT。

### 7.1 完整 RuntimeProof 的独立组合层

RuntimeProof **不属于 fundinglotcatalog，也不属于 maintenancetrust**。为避免 `billing→composer→billing` 环，候选将组合 issuer 放在 `internal/billing` 的独立只读文件：它可直接调用本包独立 scanner 的私有 caller-owned Tx core，不能调用 public `VerifyLots/VerifyFunding/VerifyLedger` 另开 snapshot，也不递归要求“先有完整RuntimeProof”才允许扫描。schema leaf与源扫描阶段只需要CatalogProof及同tx已核的v2/epoch绑定；最终完整证明在三类扫描之后才产生。

规范v1/v2的严格解码/原字节摘要/封闭字段校验现已按[严格codec根合同](c0-proof-v2-codec-contract.md)由无网络叶子 `internal/fundinglotproof` 实现；仅该格式接缝已交付，不表示payments已实现新loader或本节RuntimeProof组合。旧v1格式、bytes与拒绝条件保持。纯解码产物 `CanonicalV2`（private字段）只表示格式/规范编码有效，**不是数据库、支付真实性或启动授权**。后续数据库/runtime issuer须在独立组合层，不向该stdlib-only codec加入catalog/DB/maintenance依赖。禁止让billing import payments；也禁止把canonical中自报的OID当已受信对象。

```go
// 候选 namespace：internal/billing；未授权实施。
type RuntimeProof struct {
    // 全私有：原CatalogProof/tx identity、v2 canonical SHA、v1 predecessor SHA、
    // exact epoch ID/basis/manifest/descriptor摘要，三独立scanner的完整结果。
}
var ErrFundingLotRuntime = errors.New("funding_lot_runtime_verification_failed")
func (s *Store) VerifyFundingLotRuntimeTx(ctx context.Context, tx pgx.Tx,
    catalog *fundinglotcatalog.CatalogProof,
    canonical *fundinglotproof.CanonicalV2) (*RuntimeProof, error)
func (*RuntimeProof) VerifyTx(ctx context.Context, tx pgx.Tx) error
```

**现有 scanner 不能直接冒充上述独立core**：当前billing.verifyFundingTx大量依赖funding_journal_valid/payment_identity_valid/payment_*_valid等写侧谓词，现VerifyLedger也内调这条路径，旧FundingCheck/LedgerCheck没有完整CoverageComplete合同。因此现函数只可作为原兼容性附加检查；新的独立source/ledger core、同tx完整结果与覆盖合同须根另冻并实现后，才能发行RuntimeProof。在此之前，不用旧balanced bool、SQL谓词或fixture补出一份“完整证明”。本稿仅保留组合API候选，不授权扩写资金行为。

issuer只接受actual APP LOGIN、同一个RR READ ONLY，完整静态matrix检查不能由仅任意named-role CatalogProof代替：内部重做/确认叶子 `VerifyRuntimeTx(...,Application)` 和原proof的同tx绑定。按固定顺序核完整latest history、严格canonical与immutable v1原件/新v2行/epoch绑定，再独立VerifyLedger、VerifyFunding、VerifyLots全局core；三者必须Balanced且CoverageComplete、全部不符与缺口计数为0。不能传caller自报的balanced bool/report作为构造参数，不能以写侧SQL函数或纯分配planner作oracle。任一失败只返回nil proof与固定错误，不发行部分证明、不Begin/Commit/写/维护/network；scanner采用其受审全扫描预算，不错误继承静态checker的5秒作为全源扫描总界。

RuntimeProof私有字段还绑定同具体tx wrapper/conn/PID/top xid/DB/schema与APP角色OID、完整catalog/canonical/epoch摘要及三scanner的覆盖结果；nil/zero无效，VerifyTx不得跨连接/事务/角色或Commit后复用。它是这个RR中完成全源核验的证据，**不是在线资金写能力**；不能拿到RC写事务里append，也不能作为maintenance Permit。startup事务提交确认后只保留安全digest/审计结果，不把已过期token作为后续请求权限。

worker无全源allocation/人工/券读取权，projector无C0业务读取权，因此两者**不能发行这份APP全源RuntimeProof**，也不能为了取得它扩大权限。运行装配先取得APP完整核验成功，再让每个实际worker独立核自己的CatalogProof+v1/v2/epoch绑定，projector仅catalog，全部成功才发布同一Lifecycle/ready；APP token不跨连接移交给worker。该上层整体启动门禁与worker窄绑定API仍待单独冻结。

已实现依赖为 `fundinglotcatalog → stdlib/pgx/static descriptor`、`fundinglotproof → stdlib` 与 `maintenancetrust → stdlib/pgx/fundinglotcatalog/migrations/paymentsandboxschema`；T2按[真实授权合同](c0-authority-contract.md)独立实现私有前驱codec，没有反向import payments、billing或运行构造器。billing只读组合/独立scanner及payments/main装配仍是后续接线设计；T3维护scanner须经受控ReadLease，runtime issuer不调用维护入口。leaf不反向import维护或billing，billing不import payments/main，不从runtime composer反向构造Permit。T2[T2真实PG/race专项](../../docs/verification/maintenance-authority.md)已通过，其他尚未冻结的跨包接缝不能用stub先授权生产。

## 8. 拒绝与验收出口

实现前根须冻结：API/叶子包边界、完整静态descriptor及SHA、base全角色matrix与tenant八/六delta、支持PG major、生成工具与审阅流程、普通/sandbox模式及v2装配接缝。没有这些，不以“checker先能跑fixture”为由新增生产impl。

正式测试必须用专用PG、独立随机schema、actual APP/worker/projector LOGIN且current=session；分清raw metadata导出、catalog checker与完整runtime三层。必要用例：

1. 同一受审base+C0在ordinary和sandbox、随机name/OID下正例；RR两mode的Snapshot成功，Runtime仅RR只读及选定actualLOGIN成功；16表全空时InstallationProof成功，任一新表非空/epoch已发行都拒绝。APP/worker/projector分别验证，projector零业务SELECT仍能完成catalog检查。
2. 缺/多表列、错attnum/nullable/default/identity/collation、tenant第9列错误、RLS/非heap/非permanent/额外view/rule/type/operator/overload、type owner或array关系错，都拒绝；仅schema_migrations checksum正确不能救回。
3. 函数正文/search_path/strict/definer/args/result/language/volatility/owner/ACL任一改动、未知附着trigger/disabled/WHEN/tgargs/顺序、FK动作/deferral/列序/validation或internal RI关系改变、少索引/坏predicate/排序/invalid index，分别拒绝；数量相同的替换也拒绝。
4. APP得到stamp写、表级INSERT/UPDATE、UPDATE(id/created_at)、维护写或owner函数；worker得到allocation/A/request/总legacy读、genesis INSERT、已有cache UPDATE；projector任何C0表/列数据权、函数EXECUTE或named proof type/array的USAGE；任一角色/type的实际权限偏离§6精确矩阵，包括自动row/array的默认USAGE与grant option变化；未经授权的PUBLIC/grant-option/default ACL/peer路径，均拒绝，不能临时补grant让正例通过，也不能将32个自动type的受审USAGE当违规。
5. SET ROLE/高权/同名换OID/任意membership/三NOLOGIN owner改变/role peer互换/错误DB与endpoint；nil/closed/canceled/nonRR和Runtime写事务返回固定ErrCatalog及nil proof；无raw driver/SQL/行值输出。
6. caller-owned RR能看见自身未提交DDL/ACL并拒绝，别连接看不见；核验不提交sentinel或更改grants；同一旧snapshot/新snapshot按MVCC得不同结论。Catalog/Installation证据跨tx/conn/PID/top-xid、nested wrapper与Commit后拒绝；APP可读空集仍不能取得Permit；Gate重复attest或写后重新用InstallationProof必须拒绝。涉及PostgreSQL锁或catalog并发的失败须回滚，不能宣称runtime任意DDL攻击下永久可用。
7. 原旧v1全部正/负测试不变，C0 worker仍被旧checker拒绝；新静态组合成功且base43函数/23表定义未被削弱。旧v1 bytes/row/hash前后完全相同，新v2仅追加；APP完整RuntimeProof必须在同RR的三独立全扫描全部成功之后发行，缺覆盖/权限/查询失败均无proof；worker/projector不因不能全扫描而扩大读取。proof发行/维护/epoch完整验收另片完成。

交付记录须包含固定输入/descriptor/源码SHA、精确测试命令与真实退出、实际LOGIN与无写入/清理结果及未验范围；没有opt-in专库时明确Skip，不写商业验收完成。集成人汇总后更新统一架构正文/HLD/LLD/变化记录，再显式reviewed刷新manifest；本分片不改根架构文档或manifest。


## 9. 根冻结窄片：metadata-only 离线 descriptor 生成器

2026-11-01根已核全部输入SHA并冻结本节，仅授权离线工具与未审artifact导出；**仍不授权checker、正式expected、生产SQL、业务helper或RuntimeProof实现**。工具只帮助从固定输入装出 metadata 并揭示精确差异，输出一律 `unreviewed_descriptor_candidate`；人工复核/根冻结是后续独立步骤，不能提供一键accept。

### 9.1 CLI、固定输入与隔离

```sh
C0_DESCRIPTOR_DATABASE_URL=<固定专库连接，仅进程环境> \
  go run ./scripts/funding-lot-descriptor \
  --output .local/c0-descriptor-candidates/<本次新目录>
```

唯一CLI参数为必填 `--output`，目标必须在仓库 `.local/c0-descriptor-candidates/` 下且未存在；工具只创建自己拥有的新目录，禁止覆盖、symlink跳转或写入internal/static expected。没有live-schema/输入路径/DSN命令行参数、mode选择、repair、refresh、accept、skip-hash或降级选项。cwd必须为实际仓库根且module为本项目。连接仅从 `C0_DESCRIPTOR_DATABASE_URL` 读取；缺失立即固定错误，不自动尝试默认库。URL/解析配置必须只有TCP literal `127.0.0.1:55439/moni_test`，拒绝Unix socket、DNS/localhost、多host/fallback、service重定向；实际会话再次核endpoint、DB、`current_user=session_user=Config.User`，authenticated LOGIN固定`moni_test`、superuser且为当前DB实际owner。DSN/密码不输出、不落文件；仅允许本机专库，不是通用installer授权。

只支持 PostgreSQL **major16**；其他major在任何CREATE前拒绝，记录具体server_version_num作provenance。运行context整体至多120秒，cleanup使用独立至多10秒context；不启动main、浏览器、模型/SMTP/PSP进程，不获取普通main的instance锁。真实migrations.Apply沿原migration session lock，不绕过其checksum/锁机制。

以下是生成器自身源码常量的冻结输入pins，不在运行时把读到的hash反填为期望：

| 固定来源 | SHA-256 |
| --- | --- |
| 全部001–022规范manifest | `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de` |
| internal/operations/operations.go | `8ca51f8c3f5aee042fc28e8ede6fb450e4f0bff9650723eeb83b0560c00d9d79` |
| internal/operations/alert_roles.go | `3bdfd12de87c72d326813983d447fc5de82c97f8e3a85b00ee086801145e5073` |
| internal/operations/periodic_roles.go | `79ab556316fc2c366a97d897ab58c736c0e815de396f9904828b7abd65a4640d` |
| internal/operations/payment_roles.go | `966ad915a7ea277ed1ce13a76ca093342ad389166679cdfb8ac48b522907b8d2` |
| internal/operations/payment_roles_functions.go | `4307a70cc13890c4a2fe92d2306fd2efd68c75129808daac9309f6f368de9cb6` |
| migrations/embed.go | `915164219bb6490fc4570986a39a93696018f4d61e6465e0c1f6ec7532975b74` |
| C0 DDL/ACL | 本稿§1两份完整SHA，不使用旧ACL版本 |

manifest规范bytes为按filename ASCII/C序的22项JSON数组，每项键序固定 `{"name":...,"sha256":...}`，小写SHA、UTF-8、无空白/换行/BOM；本版本2459字节。工具从真实embedded SQL提取且对比工作树同名文件，清单项数/每个文件/规范总SHA全部检查；新增/缺失/改名或同checksum历史之外内容变更必须失败并重审。provision六个源码pins同时记录到provenance；不能只有DDL固定而运行未审的宽权限provision。

每次运行内部依次创建两个**独立随机profile**：`ordinary_app_acl`和`sandbox_app_acl`。每profile的schema/角色共用本次随机128-bit后缀及固定前缀`flcdg_`，与候选资金测试、临时`flckreview_`审计前缀不同；只按本轮精确拥有清单清理，不扫描/删除其他同前缀对象。schema由独立NOLOGIN object owner拥有，APP/worker/projector均独立LOGIN、NOINHERIT、无高权/成员；密码如创建只在内存，不连接APP做业务验收。

这两profile只提取**app schema**原授权与C0增量，不创建mock/receiver schema、不发行完整八角色环境proof。sandbox profile在无marker的原ProvisionFor成功之后、原ProvisionPaymentWorkerFor之前插入合成payment_sandbox_environment marker，固定显著fixture merchant/payee、合成UUID与全零nonce摘要；不创建v1 attestation、订单、tenant、资金或模型事实。marker值不导出；合成摘要不是任何运行manifest证明。它使原provision正确执行worker分支，不能据此称sandbox runtime认证已通过。ordinary profile无marker；两者table/function/schema定义应相同，只有预先声明的role矩阵不同。所有ACL归RoleDescriptor，SchemaDescriptor只含owner/结构/正文/结构依赖；不能为了结构相等而少查或忽略实际ACL。

### 9.2 安装顺序与owner

1. owner/admin只创建本轮schema与roles；连接先在`AfterConnect`执行固定 `SET ROLE <本轮NOLOGIN object owner>` 和固定search_path，**然后**才调用真实migrations.Apply。包括其最初创建的`schema_migrations`都必须由该object owner拥有；不能先由admin创建checksum表再改owner遮掩安装差异。离线SET ROLE仅用于复制真实安装模型，不作为runtime实际LOGIN验收。
2. 在001–022上，两profile都先在无marker时调用原operations.ProvisionFor并成功；sandbox随后插上述marker，再调用原ProvisionPaymentWorkerFor。现ProvisionFor在marker存在时末尾会同时验证worker，故不能在worker尚未provision前先插marker；不改原operations规避该门禁。原检查必须成功；不能复制一份简化GRANT脚本、忽略失败或给runtime额外membership/owner/SELECT补漏洞。
3. 取**before**全catalog/ACL快照，记录schema_migrations及每个原relation/function/type的raw OID/owner/正文摘要，仅保存在内存。其role矩阵来源是原provision结果，仍须在artifact审阅中逐项对照原授权源码；输出不是默认可信授权清单。
4. **两profile都固定创建worker LOGIN**；ordinary仅为无原base显式grant、无schema USAGE的placeholder，仍保留PUBLIC带来的函数/type有效权限，不能称其所有有效权为零；sandbox由原provision取得base worker权。same owner装固定C0 DDL，再用pgx.Identifier精确替换固定ACL四个identifier占位符（schema/app/worker/projector）并完整执行一次。ordinary的输出明确`worker_kind=c0_placeholder`，其after新增的显式权仅来自C0 ACL，不代表ordinary runtime启用了worker；sandbox输出`worker_kind=sandbox_runtime_role`。不改候选文本/静默删worker授权，不漏清placeholder。
5. **不得在C0之后再跑原ProvisionFor/ProvisionPaymentWorkerFor**；原ALL TABLES/worker撤权流程会覆盖C0最小权限。取after全catalog/ACL；验证所有原函数OID/prosrc等不变、原table/type owner不变，仅允许本稿明确tenant/trigger/ACL增量。不发行epoch、不调用SQL checker自证、不做业务写。
6. 先关闭全部profile的Rows/tx/pool，再由独立cleanup context精确DROP本轮schema、roles并核归零；两个profile均清理确认后关闭最后的admin pool，此后无DB操作，才发布artifact。失败则只留固定stage错误/安全cleanup状态，不把partial目录当成功。清理失败测试失败，不能吞错。创建先以同专用backend确认本轮随机精确names不存在，任何CREATE前登记cleanup intent；CREATE/COMMIT回包未知不得丢失登记或自动重发。连接实际PID+backend_start绑定本轮，先关闭且在独立cleanup会话确认该精确远端backend结束（必要时仅终止已关闭的精确自有backend，排除当前backend），再DROP IF EXISTS与最终核不存在，不能仅按application_name/prefix杀连接。RR rollback/物理close/排空/DDL清理共享每profile一个最初清理起算的10秒deadline，不能5秒后再重置10秒。artifact输出不包含raw对象OID/随机名、DSN、marker正文、secret或业务行。

namespace owner与object owner分开编码。named随机schema中两者都应解析到`role.application_owner`，且`schema_migrations`与所有125业务表、函数/type同样为object owner，绝不排除checksum表。未来普通public环境的namespace owner可为内建pg_database_owner，object owner只能为当前DB实际datdba；这是checker另审的封闭绑定分支，**不能由当前随机schema生成器声称已验证public场景**，也不能把namespace owner等于任意current_user。

ordinary导出中的worker是明确placeholder；正式ordinary模式的Pins.Worker=nil。将候选转成正式expected时，须另冻精确移除placeholder条目/ACL依赖/有效权向量的受审变换，以及ordinary实际不创建worker的provision分支；不得在runtime按任意role名字过滤未知ACL以求通过。当前四角色原观测完整保留，不能冒称已经得到三角色ordinary runtime expected。

### 9.3 输出文件与DTO

输出目录仅含下列固定文件；JSON均为规范UTF-8，通用object键按ASCII字典序编码（Go encoding/json的确定性排序），无map迭代序、无重复/未知字段；manifest项仍固定name、sha256（恰为字典序）。无时间戳/随机ID混进比较内容。所有metadata对象数组按完整稳定ObjectRef或受审ACL复合identity的canonical bytes/C序，不按整record内容排序；identity重复拒绝。修改record必须进changed，不能伪装removed+added；有语义的列/参数/key序保持原序。用于SHA的canonical bytes无尾换行；可另作可读格式，但sha只对规范bytes且两者不能混用。

工具运行于受信本机单用户工作区。静态symlink/越界/已有目标须拒绝，发布前检查context；不声称抵御拥有同一OS账号权限的恶意并发改写路径或任意修改源码。清理和内容pin仍必须验证，不因该信任边界放过数据库提交未知或发布partial artifact。

| 文件 | 顶层DTO/用途 |
| --- | --- |
| provenance.json | `Provenance{format_version,artifact_kind,server_version_num,server_major,migrations,manifest_sha256,ddl_sha256,acl_sha256,provision_sources,tool_sources}`；kind固定unreviewed_descriptor_candidate；tool_sources是本工具全部Go/test源码在本次构建与导出期间保持不变的SHA集合，同版本两轮必须完全一致，不能每轮源码变化后仍称可复现 |
| base-schema.json | `SchemaDescriptor`，001–022全部app对象，不是仅109表列 |
| c0-schema.json | `SchemaDescriptor`，真实after组合全集，ordinary/sandbox两份必须规范bytes相同才输出一份 |
| c0-schema-delta.json | `SchemaDelta{added,changed,removed}`，原schema→after的精确结构差异；removed必须空，changed仅已声明基线delta |
| base-roles-ordinary.json | `RoleDescriptor`，原ProvisionFor实际catalog元数据；worker为无base显式grant、无schema USAGE的placeholder，PUBLIC有效权完整保留 |
| base-roles-sandbox.json | `RoleDescriptor`，原ProvisionFor+ProvisionPaymentWorkerFor；包括所有原109表/列/function/type/schema权 |
| c0-roles-ordinary.json | `RoleDescriptor`，上述ordinary+固定C0 ACL后的观测；placeholder worker新增显式grant来自C0，PUBLIC有效权仍保留；不代表ordinary runtime存在worker |
| c0-roles-sandbox.json | `RoleDescriptor`，上述sandbox+固定C0 ACL后的观测 |
| c0-role-deltas.json | 两profile分别的`RoleDelta{before_sha256,after_sha256,added,changed,removed}`，精确区分表级/列级与grantor/grantee/option |
| summary.json | `Summary{format_version,artifact_kind,files:[{name,sha256,bytes}],counts,profile_checks,cleanup,metadata_only:true,formal_expected:false,epoch_issued:false,runtime_proof_issued:false}`；不含自身SHA |

`SchemaDescriptor`固定字段为 `format_version,namespace,relations,types,functions,constraints,indexes,triggers,dependencies,operators,casts,collations,conversions,opclasses,opfamilies,policies,rules,extensions,text_search,statistics`。空对象类别输出空数组，不因空而省字段。16业务表/57业务索引等数量不包含自动TOAST对象，后者另分support计数，不混入业务125表。namespace只记录结构identity与owner，不含ACL；relations包括业务heap、index和composite关联relation，不能只导出`relkind='r'`。各对象除符号identity与owner外使用下面查询coverage的显式字段；禁止`SELECT *`或整行row_to_json把新PG字段无审阅混入格式。

`RoleDescriptor`固定字段为 `format_version,profile,worker_kind,roles,memberships,schema_acl,relation_acl,column_acl,function_acl,type_acl,default_acl,acl_dependencies,effective_privileges`；每个schema/relation/column/function/type ACL record按object identity保留acl_is_null、原显式entries（即使空）、builtin default_entries；entries含grantor/grantee符号、privilege、grant_option，按grantor/grantee/privilege稳定identity排序。pg_default_acl每行独立保留owner、namespace/global scope、object_type与完整entries（空数组也保留），不因aclexplode为空丢行。NULL ACL、显式空ACL、显式默认ACL必须可区分，不能只coalesce导出默认有效权。effective_privileges逐角色、每表/列/函数/type/schema明列固定privilege registry布尔向量；表级与列级分开，不能把有效列全true当表级true。namespace、relation、column、function、type的全部显式/有效ACL与default ACL统一放此DTO；pg_shdepend的ACL grantor/grantee依赖也只放acl_dependencies，不能混入SchemaDescriptor。LOGINflags来自pg_roles，不读pg_authid/密码。没有任意privilege字符串“自动接纳”逻辑；未知PG权限或节点失败要求重审。

### 9.4 查询coverage与OID符号解析

显式查询覆盖以下catalog；不运行候选业务校验函数。抓到多余对象时照实导出并让固定shape检查失败，不过滤到“只剩想要的对象”：

- `pg_namespace/pg_class/pg_attribute/pg_attrdef/pg_am`：owner、relkind/persistence/AM/partition/inheritance/RLS/replica-identity/options/tablespace，完整attnum与dropped/type/typmod/collation/notnull/default/generated/identity；base/schema_migrations与C0同等覆盖。`pg_inherits`必须纳入，不仅看relispartition。普通表的reltoastrelid所指自动TOAST heap/index也需完整抓取结构与依赖，不因位于pg_toast就误作未知外对象或一律忽略；其符号由唯一owning relation推导，不保留pg_toast_<随机OID>名字。
- `pg_type/pg_enum/pg_range`：row/composite/array/其他user types及owner、category/defined/byval/length/alignment/storage、rel/element/array/base type、typmod/default/constraint、IO/send/receive函数，内部type边全闭合。
- `pg_proc/pg_language`：全部function/procedure/aggregate/overload及§5列出的flags/参数/result/proconfig/owner（ACL单独进RoleDescriptor）；prosrc原UTF-8 bytes及SHA作为metadata导出，**绝不替换其中schema名/role名/数字/string literal**。probin/prosqlbody非空也照实导出并按固定输入判断，不悄悄忽略。
- `pg_constraint/pg_index`：§4全部constraint/index字段、FK动作/有序列/validation/deferral、index predicate/expression/key/include/opclass/collation/order/nulls/ready/valid/live/immediate/constraint关联。CHECK/default/predicate用受控target search_path下的`pg_get_expr`稳定片段加结构引用；不以含随机schema名的整条pg_get_indexdef作为canonical。
- `pg_trigger`：全部user与internal triggers、§5所有字段、constraint与function引用；内部RI通过constraint符号+源/目标/事件/function唯一定位，不依赖自动名字中的OID。`pg_get_triggerdef`整条仅可用于临时诊断，不进入规范expected或日志。
- `pg_depend/pg_shdepend`：schema内对象的outgoing与指向它们的incoming边，覆盖外schema对象；转换为两端唯一ObjectRef及dependency kind。pg_shdepend的owner/结构依赖归SchemaDescriptor，ACL依赖统一归RoleDescriptor，不因两profile结构相同而省略任何ACL依赖。builtin依赖须登记pg_catalog identity/OID；relation子对象必须带attnum。未知classid/objsubid或无法闭合的外对象拒绝，不能忽略extension/外FK依赖。仅dependencies/acl_dependencies采用封闭multiset：identity为catalog来源（pg_depend或pg_shdepend）、完整symbolic两端与kind；归一前保留完整raw边identity（catalog、两端classid/objid/objsubid、kind）在内存；同normalized identity只有raw边完全相同且全部非multiplicity字段逐值相同才合并，来自不同raw对象的符号碰撞必须拒绝，显式multiplicity>=1保留原真实条数，同identity非同内容拒绝。multiplicity变化归changed，其余对象类别仍唯一；不能用遍历或随机ordinal掩盖重复。
- `pg_roles/pg_auth_members/pg_default_acl`和所有对象ACL：schema/DB实际owner与runtime flags、任一方向membership/SET/INHERIT/ADMIN option；explicit ACL和effective表/列/函数/type权限都导出，PUBLIC单独符号，不能只导出非空relacl。
- `pg_operator/pg_cast/pg_collation/pg_conversion/pg_opclass/pg_opfamily/pg_policy/pg_rewrite/pg_extension/pg_ts_*/pg_statistic_ext`：目标namespace内或附着受保护对象的全部其他对象，空集也有计数。PG16字段显式选择，未知类别要求扩合同；不是把全部系统catalog无差别dump。

符号规范固定为结构化 `ObjectRef{class,scope,name,identity_arguments,sub_id}`：scope只允许`application`、`pg_catalog`或明确的owner/runtime role符号，用户function用有序TypeRef signature唯一定位，relation/column用name+attnum。TypeRef恰选builtin `{namespace:"pg_catalog",name,oid}` 或user `{object:<ObjectRef>}`之一，不接受current OID直接作user expected。PUBLIC为专用符号；database/schema/owner/runtime role OID只在provenance观察中核对，不将随机原值写canonical内容。自动TOAST relation/index允许专用`automatic` scope，携带唯一owner ObjectRef和固定support kind（toast_heap/toast_index），不得任意外schema alias；内部RI trigger、自动row/array type与其relation/type/FK严格一对一；通用符号登记同时检查raw→symbol与symbol→raw一对一，重复/歧义失败；TOAST被多个parent共享必须在映射前拒绝，不能由map覆盖。

owner/namespace/role/dependency等**结构化OID字段**才可查本轮symbol表替换；proconfig中的search_path作为有语法的配置值用严格SQL identifier parser解成固定NamespaceRef数组（unknown项/非法引用失败），其他GUC原值与顺序保留，不能Regex替换整条search_path。只允许在**结构化OID字段**查本轮symbol表替换，不对prosrc、CHECK字符串literal、默认表达式literal或原SQL做全局string/regex替换。deparse片段在固定target+pg_catalog+pg_temp可见性下应不含随机schema/role引用；如仍含动态名字且无法用结构化字段表示，本窄片必须失败/报告固定unsupported-node，另审parser或DTO，不能用ReplaceAll消掉。整条pg_get_functiondef/indexdef/triggerdef含随机标识时不能当可跨环境的正文hash。工具至少用两轮不同随机name/OID生成后比较规范bytes；不同且无已声明环境来源时即阻断提交，不将第二轮当新expected。OID/regproc等catalog字段必须先在结构SQL中显式cast oid，再严格解析JSON number或PG输出的规范uint32十进制字符串；其他text/prosrc不作数字或名字替换。sub_id仅0或本轮已枚举关系的真实正attnum，其他class非零subid拒绝。

### 9.5 生成器实施/验收冻结出口

已授权 `scripts/funding-lot-descriptor/{main,inputs,install,catalog,dto,canonical}.go`及同目录test文件；不写fundinglotcatalog/维护/业务/main/migrations、Makefile/CI或root architecture。worker方案固定为两profile都创建、仅sandbox取得base worker权；根须冻结本节DTO/coverage后再动手。

专项必须覆盖：固定输入hash拒绝、未给专库/错endpoint/PG major拒绝、output已存在/越界/符号歧义拒绝、canonical identity排序、literal不替换、依赖multiset真实次数与同identity非同内容拒绝、一次真实两profile的before/after计数/owner/role差异、另一随机轮规范bytes可重复、checksum表正确owner、原function OID/prosrc不变、结构DTO不混ACL而role DTO完整捕捉两个profile差异、同tool_sources版本两轮、清理成功与中途失败清理。测试只是导出器忠实/可重复性，不叫schema checker安全验收；正式受审expected仍为空，RuntimeProof/epoch/资金行为仍未实现。

## 10. 根冻结：完整静态检查器的实现与正式期望启用

2026-11-01。本节冻结§2三个发行API、Pins、opaque证明与叶子依赖边界，进入`internal/fundinglotcatalog`实现。旧导出器、其七个源文件和原输入pins保持不变；新collector是一次性受审移植，不让旧工具转调新实现而改变已验证的provenance。首次实现可采用**空且无效的嵌入root**以并行编译；空root、未知profile、未审descriptor或SHA不符一律不能发行任何证明。正式root的启用是独立根审步骤，必须在本节附实际SHA与验证记录，不能从live DB或测试传参自动接受expected。

### 10.1 完整部署分支

- ordinary真实只有object owner、APP、projector三个角色，无worker占位或worker ACL。新增独立`c0-ordinary-permissions.sql`以固定逐句SQL保留APP八列INSERT/六列UPDATE与C0精确授权；不修改旧sandbox ACL，也不在runtime删除任意不认识的角色条目。真实三角色实装与旧四角色artifact的受审精确投影交叉核验。
- ordinary的named schema保持namespace/object owner相同，必须实装最小LOGIN与默认PG管理员LOGIN两种实际owner，才能接上维护Permit的真实owner认证；NOLOGIN隔离变体只保留作原候选交叉参照。public允许且只允许builtin `pg_database_owner`（OID6171）为namespace owner，实际datdba必须是object owner。public变体只在本轮随机新DB、template0和精确登记的角色中验证，不改专库原public。namespace raw ACL、grantor及依赖均保留，不能把public元数据替换成named。
- owner属性采用封闭profile：隔离NOLOGIN/NOINHERIT无高权；实际LOGIN/NOINHERIT无高权；以及PG镜像默认LOGIN/INHERIT/SUPERUSER/CREATEDB/CREATEROLE/REPLICATION/BYPASSRLS。各profile的connection limit、valid-until、role config及membership必须有固定期望，具体精确值由实装记录复核。owner有高权不扩展APP/projector权；runtime仍精确LOGIN/NOINHERIT且无高权、成员和owner路径。不接受任意owner flags反填expected。
- sandbox采用真实`CreateApplication`的受审离线顺序：原001–022 → 全函数撤PUBLIC/授APP与projector → 原ProvisionFor → 原worker provision及`funding_uuid(text)`额外EXECUTE → 固定C0一次。原导出工具漏掉这一全v1沙盒授权分支，其app-only样本不能升级为全sandbox expected。
- sandbox三个schema/三个NOLOGIN owner/五个runtime共八个distinct角色；M/Q均使用与app相同完整schema/ACL collector覆盖。A/W/P→M/Q六组、M→A/Q及Q→A/M四组共十组跨域负权，核schema/table/column/function及grant option，不因无schema USAGE而跳过函数权限。自动row/array type的默认USAGE与named proof type严格区分。

ordinary两runtime和sandbox五runtime另以固定pg_catalog effective查询核**当前database**：CREATE=false，CREATE/CONNECT/TEMPORARY的WITH GRANT OPTION均false。PUBLIC默认CONNECT/TEMPORARY可以存在，不能误写为所有database权限为零；这些权限不靠目标namespace矩阵或仅“当前尚无owner对象”推断。数据库共享ACL依赖的dbid=0不能被过滤当前DB的schema ACL查询替代。owner维护身份沿独立规则，不把runtime这项约束套给实际DB owner。

### 10.2 封闭格式、查询安全与硬预算

正式expected使用private封闭envelope，明列format version、PG major16、mode与app/mock/receiver完整结构、role矩阵；未知/重复JSON key、非规范数值、非UTF-8、未知节点或重复identity拒绝。根SHA固定在源代码，先核原bytes再严格解码；运行不读外部descriptor、不生成expected。原§9的每类结构字段、raw/default/effective ACL、原prosrc及SHA与依赖multiplicity全部保留；有序列保持语义，集合按受审identity排序。

collector逐Rows读取固定pg_catalog查询，不采用whole-database `jsonb_agg`；Rows.Err与Close在下一查询前完成。全调用最多100000条raw metadata记录、64MiB逻辑字节、单原text64KiB，单类别不超过静态expected受审上界，原应为空类别遇首条即拒绝。字符串超界在SQL返回前用固定CASE和`pg_catalog.octet_length`保护；这些上界不可caller配置。五秒从发行入口起计算，全部identity/collector/空集/末尾复核共用，不按查询重置。

允许保留§9 DTO的**单record内有界child聚合**：在同RR先流式核完整raw行/数组长度/ACL与原text界，再在每个子查询中执行固定LIMIT+1，attributes最多1601项探界、函数参数最多101项探界、ACL采用固定registry上界；溢出显式拒绝。聚合结果单record仍受64KiB与总预算约束，不能用预检代替聚合查询自己的LIMIT，不能采用整catalog聚合。

正式root解析后，按mode/scope/category在全部受审variant中取得固定最大记录数；查询使用该数+1并遇额外首行即拒绝，空类为LIMIT1。依赖是multiset，raw行上界必须采用受审multiplicity之和，归一后的distinct上界另取记录数，不能误用6848条normalized边限制6860条真实边。两项均来自固定root，不能从actual反填。

封闭静态SQL只通过`pg_catalog.jsonb_build_object`等明确唯一key构造PG jsonb record；PG jsonb本身不保留重复key。因此runtime record可用有界`json.Decoder.UseNumber`读取一份值与EOF，随后完整检查UTF-8、NUL、canonical scalar和封闭节点类型，避免Token逐值重复分配。此信任前提只用于本包固定SQL输出，不能用于外部JSON、expected/candidate文件或caller自报metadata；后者始终使用拒绝重复key的strict parser。单record/总资源和五秒预算不改变。

函数、catalog、type cast及operator均显式绑定pg_catalog，不能在拒绝shadow之前执行目标schema同名函数。仅接受实际`current_schema=App`且search_path严格解析为`App,pg_catalog,pg_temp`，API只检查不SET。actual Config.User=current_user=session_user且真实LOGIN，endpoint配置/实际TCP remote/server IP与port/DB、PID/backend_start/top xid/role与schema OID和owner在发行前后重解一致。Snapshot可RR RO/RW；Runtime仅选定actual runtime的RR RO。不代替Gate对顶层事务的所有权。

peer deparse如含schema限定名，采用封闭`Literal|SchemaIdentifier` expected片段模板：仅在**受审expected的SQL标识符位置**渲染完整quote后的绑定schema，再与actual原bytes比较。actual/prosrc/SQL字符串literal绝不全局替换；只有原bytes完整匹配后才以已审symbolic模板参与canonical摘要。模板必须经两轮随机schema/OID实装和恶意literal反例独立核验，不能把扫描到的schema子串自动标为identifier。

这里的SQL identifier渲染采用同tx明确调用`pg_catalog.quote_ident`取得的PG16 deparse规则（简单小写名可以无双引号），不是对所有名字强加双引号。ordinary的正式envelope包含固定且无重复的五个完整variants：`named-isolated`、`named-minimal-login`、`named-image-login`、`public-minimal-login`、`public-image-login`；模式root SHA绑定整个envelope，actual只能完整匹配其中一个，不从actual删字段或拼凑flag组合。其他namespace/owner属性组合需另行实装和审阅，不冒称已覆盖所有普通安装。sandbox为单一完整固定variant。

静态envelope最多64MiB/4000000个JSON值节点；五个完整variant的旧source实测组合约2648920节点，单sandbox候选814211节点，不能使用单record界误拒完整root。runtime原64KiB单record与100000 raw rows等界不变。static root在包初始化阶段做固定SHA/严格解析并缓存私有只读对象，不访问DB；因此发行入口五秒不包含重复解析整个静态root。允许对canonical完全相同的expected schema共享只读map，不能按actual重写expected。

2026-11-02根与独立审阅者确认：公开发行路径可由**同一已认证完整variant的Schema与Roles两棵文档逐节点精确比较**承担actual封闭字段、shape、枚举和值类型验证，无须先对actual重复执行整份DTO遍历。只有完成两棵文档全部比较后才能进入peer闭包及proof发行；已认证expected自身仍须在启动时完成严格解析、封闭DTO、canonical与SHA验证。raw pre-deparse、Decode的UTF8/NUL/canonical scalar/深度/资源界、符号双射、raw alias、依赖body/multiplicity、类别raw/distinct界及排序全部保留。只有受审模板允许expected片段渲染为actual原string，原文不改写。该优化用两条固定私有调用链实现：normalized collector供完整比较，metadata-only observer在输出候选前仍独立执行完整DTO验证；不存在公开skip flag或外部expected口。固定权限向量可以将逐record的unnest子计划展开为相同次序的明确pg_catalog权限函数ARRAY，须以真实同RR完整raw multiset字节对等验证，不增删权限或grant-option检查。

### 10.3 发行、测试与交付出口

`CatalogProof`绑定具体tx wrapper/Conn/PgConn/PID/backend_start/top xid与深拷贝全pins，以及descriptor/schema/roles摘要；nil/zero/换wrapper/换conn/Commit后均拒绝。`MatchesPins`只作完整结构值比较。`InstallationProof`在同一总预算内核十六表全空；其后VerifyTx不反复读空集，不能因此二次发行维护许可。三个API始终返回固定ErrCatalog与nil proof，任何driver/查询/值不得unwrap输出。

InstallationProof直接持有私有原identity，`Catalog()`每次只返回共享该identity的新CatalogProof wrapper；调用方通过返回指针赋值、清零或替换成另一CatalogProof，不得改变原InstallationProof的empty-at-issue证据。测试须明确覆盖这一可写指针反例，不只检验private字段不可直接构造。

### 10.4 首次正式root冻结与已验环境

2026-11-02根独立核ordinary十轮、sandbox不同随机实例双轮完全相同；app完整schema与旧固定候选相同，M/Q的22表187列、37函数原文/flags、52用户trigger、212约束identity/flags与冻结旧inventory交叉一致。sandbox相对旧app-only角色样本，113个原NULL function ACL按真实v1顺序变为owner/APP/projector显式EXECUTE，`funding_uuid`额外worker一项，worker的112个旧PUBLIC-only有效EXECUTE被撤；其余原table/column/type/default ACL不变。ordinary新增两类named actualLOGIN避免NOLOGIN样本无法完成真实owner维护的闭环缺口。

| 正式静态文件 | bytes | SHA-256 |
| --- | ---: | --- |
| `internal/fundinglotcatalog/expected/ordinary-v1.json` | 44854845 | `a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2` |
| `internal/fundinglotcatalog/expected/sandbox-v1.json` | 13533215 | `a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f` |

本版是专库**PG16.15/Homebrew initdb catalog-profile**的完整静态expected，已验DB encoding UTF8、C/C环境。内建SQL等OID与initdb分配的PL/pgSQL language OID须区分：本profile实测plpgsql=14038，不能由major16推断任意发行版/新cluster都同OID。未知OID仍失败关闭；正式1Panel/不同PG环境需要独立验profile或先冻结并验证language/extension符号绑定的新版本，不能runtime根据live对象扩registry。默认collation的实际locale和跨cluster兼容同属生产验收门槛。本次未将专库结果冒称任意PG16/1Panel认证；生产推广之前必须解决这些已记录的安装边界。

正式启用前：五个ordinary变体与完整sandbox实装；不同随机名/OID双轮规范bytes重复；每个actual APP/worker/projector RR RO成功；RR RW仅Snapshot/Installation；缺多对象/正文/ACL/type/函数shadow/endpoint/角色/事务/资源/取消负例和sameRR未提交漂移/关闭后证明拒绝。测试专库变量为`C0_CATALOG_DATABASE_URL`，与旧工具变量隔离。专用随机新DB和角色精确清理，unknown创建不重发，cleanup独立有界；正式启用SHA、真实命令退出和未验范围写入验证记录并同步架构。本片没有生产migration/Permit/epoch/RuntimeProof或普通main接线。

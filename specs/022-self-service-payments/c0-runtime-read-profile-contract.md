# C0-R1-A：APP 完整资金图的版本化读取权限

2026-11-03，根冻结实施合同，先于实现。依赖[O1](c0-epoch-observer-contract.md)与[静态 catalog](c0-schema-check-contract.md)。本片完成 R1 的读取权限前置；完整非空 RuntimeProof、历史/当前 roots 兼容、正式迁移和在线接线仍按 R1-B/R2 继续，不能把 CatalogProof 当完整运行许可。

## 1. 已确认的缺口与边界

当前 `ReadWallet` 的固定并集为84张表、776个唯一物理列，另读 `schema_migrations(name,checksum)`。APP 对 `payment_query_conflicts(id,verified_query_id,conflicting_digest,received_at)` 缺 SELECT；其他现有读取面须在 actual APP 上完整复核，静态清单不能代替权限验收。APP/worker 对 mock/receiver 内容继续无权，worker 不取得84表权限，projector仍只是catalog边界。

旧 `operations.VerifyPaymentRole/VerifyPaymentRolesFor`、provision、源SHA、两套v1 expected原字节及原负例全部保留。新profile不能被旧API隐式接纳；本片不修改001–022、已保存v1/v2、epoch、资金、生产配置或普通main/worker装配。

## 2. 显式API与事务合同

`fundinglotcatalog` 新增封闭 `Profile`：`LegacyV1=1`、`WalletReadV2=2`，零值/未知值拒绝。新增：

```go
func VerifySnapshotProfileTx(context.Context, pgx.Tx, Pins, Profile) (*CatalogProof, error)
func VerifyRuntimeProfileTx(context.Context, pgx.Tx, Pins, RuntimeRole, Profile) (*CatalogProof, error)
func (*CatalogProof) Profile() Profile
```

原三个发行API固定使用LegacyV1；不增加安装profile入口。新Snapshot API只认证catalog，新Runtime API继续要求所选actual LOGIN、RR READ ONLY，worker仅sandbox。所有路径保持原固定累计5秒、actual端点/身份/完整collector/前后绑定、完整EOF/资源界和固定ErrCatalog；不启动/结束调用方事务、不SET ROLE、不执行ACL/业务写。Profile随私有proof identity保存，nil/zero返回0；复制共享原绑定、换wrapper/conn或结束事务后拒绝。Profile标识不是资金授权。

collector的类别预算必须来自**本次显式选中的静态profile**；旧metadata候选观察仍固定LegacyV1。禁止把v2新增依赖通过提高旧profile上界或过滤actual来接纳。

## 3. 固定base加精确delta

为避免再复制58MiB，只从已SHA认证、严格解码的v1静态根派生私有v2 descriptor，绝不读live catalog生成expected。保持封闭envelope格式1、PG16、mode、原五ordinary/一sandbox变体及原schema文档。改变application role文档中的以下受审节点：

- 四个指定物理列的 `column_acl` 从原NULL/空entries改为显式一条owner→APP的SELECT、无grant option；default_entries仍空。
- 同四列的APP `effective_privileges` 仅SELECT位从false改true，其他三位和四个grant-option位仍false。
- 增加四条 `pg_shdepend`、kind=a、multiplicity=1、列→APP ACL依赖；不能增加table级SELECT或复用relation级边。

sandbox的ACL dependency固定查询按全部八角色做闭包；同四个实际列级依赖也须镜像进mock/receiver各自role文档（各四条）。这些只是同一真实依赖的重复metadata观察，不增加peer内容授权。其他peer节点保持不变。

每个旧目标必须恰一、原值精确匹配且不存在新依赖，任一不符派生失败。新对象/数组须owned；共享其他只读静态节点不可被修改。全schema、其他roles/peer的非镜像依赖节点、表ACL、函数/type/default权限及owner flags逐值不变。数组重新按原受审identity排序；全descriptor完成原封闭DTO校验、规范编码和固定**派生根SHA**认证后缓存。每variant schema/roles原字节摘要也写验证记录；SHA常量由根审静态base+delta后冻结，实际数据库观察不得回填它们。

`c0-runtime-read-permissions.sql` 是独立离线候选，只含一条四列SELECT授权及明确schema/app标识符占位符；测试在本轮随机schema由原owner按受控identifier渲染执行。不给生产自动GRANT或公开provision API。旧checker在其上继续失败；新profile在旧布局上也失败。

## 4. 验收与当前能力

纯层须证明零/未知profile拒绝、v1静态原件不变、所有变体精确delta/无其他变化、新根与每variant摘要固定、原profile不接受新增ACL、proof profile/绑定/5秒行为不变。不能用调宽budget解决失败。

专用PG由根串行，明确设置 `C0_CATALOG_DATABASE_URL`。五ordinary变体与完整sandbox各以两个独立随机name/OID实装：先旧API成功/新profile拒绝，追加精确delta后新API成功/旧API拒绝；actual APP/worker/projector的选择边界及RR RW、SET ROLE、换wrapper、取消/结束事务负例。实际完整采集与派生静态schema/roles bytes逐值比较。

在actual APP同RR逐项查询所有84表/776物理列及history两列权限，并实际执行显式投影SELECT验证可读，不能用管理员或has_any_column_privilege替代；sandbox另验证APP/worker不能读peer内容。清单须与固定scanner绑定并记录源SHA，若发现其他缺口，先回合同审最小增量，不能临时补宽权。

至少逐列撤销四项、表级SELECT、额外INSERT/UPDATE/REFERENCES、WITH GRANT OPTION、PUBLIC列权、错误grantee/grantor或membership均拒绝。原v1全部回归、维护O1依赖隔离和架构检查继续执行。记录实际PASS/SKIP/FAIL、版本、源码SHA、cleanup与未验范围，不将独立catalog fixture称为genuine支付/RuntimeProof验收。

R1-B须另冻结历史cutover→current静态根映射、APP自有v1原件/严格文件认证、独立composer、累计预算与原事务证明寿命；不得重写历史proof或用经营后basis强等。R2再处理正式迁移/CLI、所有在线资金helper及生产启动。本片不是为绕过这些依赖创建临时在线出口。

## 5. 根审静态派生摘要

独立Python与Go实现均从原base派生；下列SHA冻结的仍是完整规范envelope，而非仅四列摘要。逐variant schema/roles摘要与bytes见[固定结果](../../docs/verification/runtime-read-profile-roots.json)，可用 `python3 scripts/check-wallet-profile-roots.py` 独立只读复算。原PG16.15/Homebrew、UTF8/C/C profile限制保持。

| WalletReadV2 | 完整规范bytes | SHA-256 |
| --- | ---: | --- |
| ordinary，原五变体 | 44863025 | `c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20` |
| sandbox，原完整变体 | 13536723 | `42a63f7178748ea5dd4ce6964cbe9cd77d0131ca8dad795fbd61a4735b206706` |

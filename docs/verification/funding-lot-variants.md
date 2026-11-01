# 022-C0：ordinary 三角色与 named/public 五变体验证

2026-11-01。本记录覆盖 [封闭变体合同](../../specs/022-self-service-payments/c0-ordinary-public-contract.md)、[独立 ordinary ACL](../../specs/022-self-service-payments/c0-ordinary-permissions.sql) 和 [隔离 fixture/候选测试](../../internal/fundinglotcatalog/variants_fixture_test.go)。统一架构及正式 checker 的 expected 接受由集成人独立复核。本片未修改旧离线生成器、原 C0 DDL/sandbox ACL、001–022 或原 provision 源码。

## 实际环境与安装边界

仅显式提供 `C0_CATALOG_DATABASE_URL`，限定 TCP `127.0.0.1:55439/moni_test`、实际 LOGIN=session=moni_test、超级用户、实际数据库 owner 和 PG major 16。实测版本为 PostgreSQL 16.15（Homebrew）；这不证明 Docker/1Panel 或生产环境已经验收。

五个变体固定为 `named-isolated`、`named-minimal-login`、`named-image-login`、`public-minimal-login` 和 `public-image-login`。每轮创建独立随机 owner/APP/projector 三角色；named 使用父专库的随机 schema，public 使用本轮新建 `template0` 数据库中的真实 public。仅 `named-isolated` 的 NOLOGIN owner 以离线 SET ROLE 安装；其余四种 owner 均为实际 LOGIN，且分别核 `current_user=session_user=object owner`。APP/projector 全部以实际 LOGIN 验证。

所有变体依次安装原001–022、原 `ProvisionFor`（无支付 marker）、固定候选 C0 DDL 和独立 ordinary ACL。固定 SHA-256：

| 输入 | SHA-256 |
| --- | --- |
| C0 DDL | `e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047` |
| ordinary ACL | `864a3e5b19a9e7d21d98b6557abf1d4903ca6ced438f1ec0259e786582b0d74e` |
| 原 sandbox ACL | `9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae` |
| 001–022 canonical manifest（2459 bytes） | `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de` |

## 已执行专项

真实 PG race 命令如下，退出0，6.157s：

```sh
C0_CATALOG_DATABASE_URL='<专用测试库URL>' \
go test -race ./internal/fundinglotcatalog \
  -run '^TestOrdinaryVariant(ClosedDatabaseConfig|IndependentACLMatchesAppSurface|sInstallExactACL|sInjectedDDLFailureCleanup)$' \
  -count=1 -v -timeout 300s
```

五变体全部完成真实安装及故意无效 DDL 的中途失败清理。安装核125业务表、1145列、197函数、252类型，原函数 OID/owner/prosrc 摘要保持；新16表全部为空；APP tenant 八列 INSERT、六列 UPDATE，无表级写和出生维护列权；projector 无 C0 数据读取/函数执行，APP 无 namespace CREATE。三种 owner flag profile 精确固定，public 保留实际 PUBLIC USAGE 且无 PUBLIC CREATE。独立 ordinary ACL 的18条语句完整等于旧 sandbox ACL 的 APP 部分；这种转换仅用于测试参考，不是运行时过滤。

旧候选投影命令使用显式 `C0_CATALOG_LEGACY_REFERENCE` 指向此前双轮核验之一，退出0，11.933s：

```sh
C0_CATALOG_DATABASE_URL='<专用测试库URL>' \
C0_CATALOG_LEGACY_REFERENCE='<已审旧候选目录>' \
go test -race ./internal/fundinglotcatalog \
  -run '^TestOrdinaryVariantLegacyProjection$' -count=1 -v -timeout 120s
```

先按旧 [验证 JSON](funding-lot-descriptor.json) 核全部十文件 SHA/size及旧工具七份源码，再从旧四角色 ordinary C0 descriptor 精确移除 worker 的1 role、1720 effective、82 ACL dependency、relation8/column53/function20/type1 grantee entry；不允许 worker grantor、未知 worker 引用或非空 membership/default ACL。新真实三角色 `named-isolated` 的完整 schema 与旧 canonical bytes 相等，完整角色/ACL 与该受审投影相等。此比较不把旧 placeholder 作为新的 runtime 授权规则。

`go vet ./internal/fundinglotcatalog` 退出0。上述测试后独立只读审计：`flcv_` roles/schemas/databases 均为0；父库 `moni_test.public` 仍由 `pg_database_owner` 持有，raw ACL 为 `{pg_database_owner=UC/pg_database_owner,=U/pg_database_owner}`、relations=0。fixture cleanup仅处理本轮精确拥有资源；前缀计数只用于事后只读观察，不用于删除或终止其他会话。

## 双轮候选及独立复核（2026-11-02）

最终显式候选导出退出0，测试50.34s、包52.029s：

```sh
C0_CATALOG_DATABASE_URL='<专用测试库URL>' \
C0_CATALOG_EXPORT_DIRECTORY='.local/c0-catalog-candidates/five-variants-20261102-final01' \
go test -race ./internal/fundinglotcatalog \
  -run '^TestOrdinaryVariantCandidateReproducibility$' -count=1 -v -timeout 300s
```

五变体各两轮随机安装的 schema/roles canonical bytes 完全相等，十轮精确资源全部清理后才发布。导出前后包内全部23份 Go 源码 SHA 相等。此 provenance 仅绑定捕获窗口，随后集成人审阅/embed 的改动另外复核。输出严格标为 `unreviewed_descriptor_candidate`；此测试没有把它接受为 formal expected。

独立重读核十份 JSON 加 summary 的精确文件集合、无重复 key、SHA/size；十文件和 summary 均0600、目录0700且非symlink。summary为5005 bytes、SHA-256 `c7d49597402033d291ff0ce8339fa649f1ece532352f37f204b0901ec5457f91`。完整来源pin与机器可读证据见 [funding-lot-variants.json](funding-lot-variants.json)。

| 文件 | bytes | SHA-256 |
| --- | ---: | --- |
| `ordinary-named-image-login-roles.json` | 2540281 | `b1a1c15e35149789b1cba3ecb58197704b0e6c1a4c296d26efc0fbd33ee58f5f` |
| `ordinary-named-image-login-schema.json` | 6430551 | `089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24` |
| `ordinary-named-isolated-roles.json` | 2540288 | `b032e7b1626df0cb1fc3160619d808fd382c966ccb66258a61cc3fb1e29b3609` |
| `ordinary-named-isolated-schema.json` | 6430551 | `089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24` |
| `ordinary-named-minimal-login-roles.json` | 2540287 | `d7d632a6e77082c79a5dce0e558fe46e9d37c02c4c63702371f4dd84fbd41b62` |
| `ordinary-named-minimal-login-schema.json` | 6430551 | `089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24` |
| `ordinary-public-image-login-roles.json` | 2540640 | `864d06014ceb1a5bc28bafcbf81b216dc0476abc2b2bf61fb380f7d2952173b3` |
| `ordinary-public-image-login-schema.json` | 6430368 | `3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926` |
| `ordinary-public-minimal-login-roles.json` | 2540646 | `4c8037ece6c9a07569c922b31088188e30f593021413bad656ef36e356a2b644` |
| `ordinary-public-minimal-login-schema.json` | 6430368 | `3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926` |

逐记录核差异：named三变体 schema 全同；minimal仅owner.login从false改true，image仅owner七项布尔flag全部true。public两变体 schema 全同，相对named只有namespace owner改内建 `pg_database_owner` OID6171及少一条namespace到自定义owner的 `pg_shdepend kind=o` 边，结构dependency记录6848→6847且无新增edge。public角色另有精确schema ACL差异：builtin default entries/owner/grantor、保留PUBLIC USAGE，无PUBLIC CREATE。所有变体的APP/projector角色、effective privileges、relation/column/function/type/default ACL及ACL dependencies均相同；没有删去权限类别来取得一致。

最终再次执行 `go test -race ./internal/fundinglotcatalog -run '^TestOrdinaryVariant' -count=1 -v -timeout 300s`（显式专库及旧reference；本轮未设置export变量，因此只跳过已经单独成功的opt-in导出），退出0，14.066s。五变体安装/失败清理和完整旧投影全部重新通过。导出后独立只读审计仍为本片roles/schemas/databases全部0，shared public owner/ACL/relations保持上述原值。

## 验收边界

本片是静态安装、权限及 metadata 证据。候选输出须明确标记 `unreviewed_descriptor_candidate`，独立审查并固定 SHA 后才能被正式 checker 接受；测试不自动写 internal expected，不发行 InstallationProof、epoch 或 RuntimeProof。没有普通 main/资金链接线、真实供应商、支付服务商或生产验收；空 C0 fixture 通过不能代表完整资金来源/lot 重放或商业验收完成。

# 022-C0：ordinary 无 worker 与 public namespace 精确变体（根冻结）

2026-11-01。根冻结本片：独立 ordinary ACL、named/public 安装与受审 owner profiles、隔离验证及 metadata 候选；正式 expected 仍须根独立审阅 SHA 后接受。本片不修改已固定的 sandbox ACL、旧离线生成器或001–022，不开放维护、资金、epoch、v2或普通main。

## 1. 固定输入与 ordinary ACL

新文件 [c0-ordinary-permissions.sql](c0-ordinary-permissions.sql) 是独立离线 SQL，SHA-256 `864a3e5b19a9e7d21d98b6557abf1d4903ca6ced438f1ec0259e786582b0d74e`。唯一 identifier 占位为 `fl_app`；没有 worker identifier、worker专属GRANT或运行时过滤。按固定顺序安装真实001–022 → 原 `operations.ProvisionFor`（无支付marker）→ 原固定C0 DDL → 本SQL，C0之后不重跑原宽授权provision。

其APP权限精确等于原sandbox候选的APP部分：16表SELECT；tenant原八列INSERT、六列UPDATE，无表级INSERT/UPDATE；lot/event/head/cache/allocation指定列写；39函数EXECUTE、named composite USAGE，无维护列/owner-only函数/grant option。projector无新增表/列或函数权；自动row/array type的PG默认USAGE照实核，不要求其为零。原sandbox候选与旧pins保持不变。

ordinary实际只创建三角色：object owner、APP、projector；`Pins.Worker=nil`、`Pins.Sandbox=nil`。named-isolated无worker实际安装所得完整schema/RoleDescriptor与旧四角色ordinary观测的受审projection交叉比较；其余owner/public变体的真实差异另外审阅，不能靠修改基线来制造相等。projection不是runtime允许规则：仅移除指定 `role.worker` 的role/effective记录、explicit ACL grantee entries与其ACL dependencies，其余schema/owner/APP/projector/PUBLIC/default ACL不变。未知位置的worker引用、worker作为grantor/owner、非空membership/default ACL均须另审；不能按任意名称过滤actual。

旧ordinary四角色base需要移除1 role、1460 effective记录、0 ACL dependency；C0后移除1 role、1720 effective、82 ACL dependency，显式entries为relation8/column53/function20/type1。数量只辅助核对，不能代替完整canonical比较。新的正式模式标记由静态checker envelope统一冻结，不能把旧 `worker_kind=c0_placeholder` 当运行合同。

## 2. 三个封闭 owner profile 与五个 namespace 变体

APP/projector均精确为实际 `LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`，connection_limit=-1、valid_until=NULL、rolconfig=NULL，无任何方向membership、owner/CREATE或grant option路径。它们与所有owner/OID互异。

| profile | namespace/对象 | owner flags |
| --- | --- | --- |
| named-isolated | 随机named schema；namespace owner=object owner | NOLOGIN、NOINHERIT，无SUPERUSER/CREATEDB/CREATEROLE/REPLICATION/BYPASSRLS |
| named-minimal-login | 随机named schema；namespace owner=object owner | LOGIN、NOINHERIT，无上述五项高权 |
| named-image-login | 随机named schema；namespace owner=object owner | LOGIN、INHERIT、SUPERUSER、CREATEDB、CREATEROLE、REPLICATION、BYPASSRLS |
| public-minimal-login | 实际public；namespace owner为内建pg_database_owner；object owner为实际datdba | LOGIN、NOINHERIT，无上述五项高权 |
| public-image-login | 实际public；同上owner绑定 | LOGIN、INHERIT、SUPERUSER、CREATEDB、CREATEROLE、REPLICATION、BYPASSRLS；对应PG镜像初始化owner的明确profile |

三种owner profile的connection_limit=-1、valid_until=NULL、rolconfig=NULL且无显式membership；不把其它flags混合自动归入最近profile。五个namespace变体全部独立实装、记录并冻结角色canonical摘要，不能把owner flags从descriptor中抹除。owner是离线信任身份，runtime不得拥有/继承它；允许受审离线高权owner不授予APP/projector高权。named-isolated只保留旧候选投影及NOLOGIN离线观测基线；需要实际owner LOGIN的普通维护Permit使用named-minimal-login、named-image-login或对应public变体，不借NOLOGIN的SET ROLE伪造actual LOGIN。未知owner flags或namespace组合须另审，不能自动接受。

## 3. public 精确 namespace 语义

只接受PG16内建 `pg_database_owner`（本版本OID6171、NOLOGIN）为public namespace owner；对象owner必须是当前数据库实际 `datdba`，不是 arbitrary current_user。namespace、checksum表、全部125业务表、type/function各自验证owner关系；namespace owner与object owner分别符号绑定。内建namespace角色只作为固定builtin引用，不计入本轮创建的ordinary三角色。

真实PG16新public原始ACL为 `{pg_database_owner=UC/pg_database_owner,=U/pg_database_owner}`。原ProvisionFor撤销PUBLIC CREATE并加APP/projector USAGE后，PUBLIC USAGE仍保留；其raw ACL、builtin default_entries、grantor、owner dependency均精确记录。public expected是独立封闭variant，不能仅把named descriptor的owner字段替换后当作实测。runtime仍必须CREATE=false、grant option=false、非datdba/namespace owner/对象owner且无相关membership。

固定target+pg_catalog+pg_temp search_path、原prosrc bytes、依赖multiset与所有对象封闭性规则沿[静态checker合同](c0-schema-check-contract.md)。表达式与正文不做schema名/role名的全局替换。public固定名字本身不是“随机名泄漏”，不得误删合法literal。

## 4. 隔离可运行验证

专用opt-in环境 `C0_CATALOG_DATABASE_URL`，父连接只能为TCP `127.0.0.1:55439/moni_test`、actual LOGIN=session=moni_test、superuser、实际DB owner且PG major16；不加载评审/生产env。named variant只在该专库创建本轮随机schema。public variant以父连接创建本轮128-bit随机名临时数据库 `TEMPLATE template0 OWNER <本轮owner> ENCODING UTF8 LC_COLLATE C LC_CTYPE C`，连接目标必须是本轮登记的数据库/OID；绝不改 `moni_test.public`。

CREATE前在同backend确认精确名字不存在，事先登记精确cleanup意图；角色创建用事务，CREATE DATABASE在事务外一次执行，成功回包未知不重放。所有identifier用pgx.Identifier，角色口令仅内存；实际runtime验收用真正APP/projector LOGIN，不以SET ROLE替代。named NOLOGIN仅离线安装通过SET ROLE复制object owner模型；named/public其余四个owner均以真实LOGIN安装并核current_user=session_user=object owner。所有profile按固定输入SHA安装，没有tenant、支付或模型请求事实，新16表保持空。

资源清理采用独立总10秒context：先关闭本轮所有pool/rows/tx，按PID+backend_start+database OID+application_name精确核已关闭的本轮backend并必要时终止，仅确认归零后删除精确schema或精确临时DB，再删角色。DROP DATABASE不使用FORCE，不按前缀杀session/删DB/删角色；其他连接或清理未知直接失败。创建事务失败、CREATE回包未知、中途安装失败均走同一拥有清单。最终核本轮schema/DB/roles/backend不存在；测试不留下成功partial artifact。

## 5. metadata 与正式checker接缝

同包测试helper `newOrdinaryVariantFixture(t, kind)` 提供精确 `Pins`、owner/APP/projector真实pool及cleanup。collector提供 `observeCatalogTx(ctx,tx,pins)` 只读导出 `catalogSnapshot.schemaCanonical/rolesCanonical`，不比较expected、不发proof；普通 `readCatalogTx` 从根已接受的embedded descriptor比较。不得将测试导出入口做成生产接受live expected的API。

候选导出须单独opt-in新建 `.local/c0-catalog-candidates/<新目录>`，0600、无覆盖/静态symlink/越界，不写internal expected；完整根审后由集成人固定SHA并embed。五变体各执行两轮不同随机name/OID的真实安装，十轮全部完成精确清理后才发布十份metadata JSON及unreviewed summary，导出前后collector包全部Go源pin必须相等。测试分别核三profile/五变体canonical稳定、与旧四角色named-isolated投影相符、public/default ACL差异、四种actual owner LOGIN与APP/projector权限、原函数OID/prosrc及owner、16表空集、tenant8/6列、末审资源归零与五变体中途失败清理。正式Catalog/Installation/Runtime API的正反例由checker套件统一补验；本片不能把安装metadata通过写成RuntimeProof或生产资金验收。

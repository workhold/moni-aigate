# 022-C0：完整 sandbox catalog 闭包（根冻结）

2026-11-01。根已授权本片与[checker合同](c0-schema-check-contract.md)的完整三API leaf一同实施。本片补齐三schema、八角色与peer静态expected；不修改旧v1、离线metadata工具或其固定pins，不发行维护Permit、RuntimeProof或资金epoch。实现与架构由根汇合验收。

## 1. 来源与完整安装基线

固定来源为`internal/paymentsandboxschema/mock_v1.sql`（`3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127`）、`receiver_v1.sql`（`3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e`），及同目录旧inventory。旧inventory只是交叉审阅输入，不能代替完整expected：mock为16表145列、32个自动row/array type、27函数、34索引、37用户trigger、166constraint；receiver为6表42列、12type、10函数、12索引、15用户trigger、46constraint。完整descriptor另含TOAST、内部RI、原prosrc、所有受审flags、owner、结构/ACL依赖与显式/有效权限。

离线fixture只接显式`C0_CATALOG_DATABASE_URL`，硬核TCP `127.0.0.1:55439/moni_test`、实际`moni_test` LOGIN/superuser/DB owner与PG major16；随机128-bit前缀的三schema/八角色，绝不安装到public。固定C0 DDL/ACL沿checker原SHA。

应用schema必须采用真实CreateApplication授权顺序：真实001–022；撤全部原函数PUBLIC EXECUTE、授原函数APP/projector；`ProvisionFor`；仅测试合成marker；`ProvisionPaymentWorkerFor`；worker额外`funding_uuid(text)` EXECUTE；最后固定C0 DDL/ACL。该顺序位于C0之前，不能在C0后重新运行宽授权。旧app-only生成器的sandbox仍有113个原函数PUBLIC EXECUTE，不能升级为完整sandbox expected。ordinary保留自己受审原基线，不套用此sandbox撤权。

peer schema用各自NOLOGIN owner安装固定SQL；撤PUBLIC schema/table/function权，runtime仅schema USAGE、全部table SELECT、全部function EXECUTE；INSERT及列UPDATE严格取固定`paymentsandboxschema.Permissions`矩阵，无grant option。environment/key/history业务行不属于metadata expected，也不为了catalog核验给APP/projector peer读取权。

## 2. 同一caller RR与静态expected

所有观察、角色与权限查询只用API收到的同一活跃RR，不Begin/Commit/Rollback、SET ROLE/SET path、业务表读取或自动修权。全部查询/身份函数与operator/cast限定pg_catalog，继承发行API共同5秒预算。失败统一nil proof及固定ErrCatalog。

三schema统一使用closed collector/符号resolver；符号scope为application/mock/receiver及八个固定角色符号。runtime不引用payments/paymentruntime构造器。既有`paymentruntime.attestCatalog`自己Begin、只核own peer且为七角色；其逻辑只能作为审阅来源，不能嵌套调用。`payments.runtimeVerifyCatalog/runtimeNoForeignPrivileges`的八角色与十组负权逻辑亦仅作来源，不反向依赖业务包。

完整peer expected必须由固定SQL与上述真实离线授权生成、人工核差异后冻结进leaf；运行时不从live库生成期望。旧inventory中的原函数SHA须逐项与固定SQL的原`$$`正文核对，完整expected保留原UTF-8 prosrc及SHA。prosrc不作任何schema/role/数字替换。deparse有动态schema名时仅用受审expected中明确的Literal/SchemaIdentifier段绑定精确标识符字节；同caller Tx以固定`pg_catalog.quote_ident`取得PG16 deparse所用形式，简单小写name可无双引号，mixed case/特殊字节/保留字按PG实际加引号。不能以`pgx.Identifier.Sanitize`恒双引号的结果假定所有deparse都相同。actual字符串不ReplaceAll，未知位置或无法闭合节点拒绝。不修改调用方search_path来获得desired deparse。

离线`TestCatalogPeerCaptureReviewMetadata`只有显式`C0_CATALOG_CAPTURE_PEER=1`才运行。它在登记deparse字段中仅把已pin的schema标识token（紧接`.`）拆为受审候选模板，同时记录每个JSON path/part/scope；single-quoted literal和原prosrc不改写，任何残留live schema name使候选生成失败。三schema/八角色及backend精确清理确认后才把无凭据、业务行或自建对象raw OID的候选和位置审计输出至新建私有临时目录。该材料仍须人工来源/字段/跨随机实例一致性审阅；测试观察器不发行proof，运行API也不接受这份外部bytes作为expected。

## 3. 八角色与十组负权

sandbox三个SchemaPin各自NamespaceOwner=ObjectOwner，形成三个独立NOLOGIN owner；APP、worker、projector、mock、receiver五个实际LOGIN，共八个互异name/OID。三schema name/OID互异，所有pin均重新解析并核owner，禁止同名换OID。八角色无SUPERUSER/INHERIT/CREATEDB/CREATEROLE/REPLICATION/BYPASSRLS，无任一方向membership/SET/ADMIN边；固定连接上限/config/validuntil按完整descriptor核。五runtime不能成为任何DB、namespace、relation、function或type owner。

数据库级权限也是创建/提升路径，不能只核schema ACL或角色CREATEDB flag。ordinary APP/projector及sandbox五runtime，在同caller RR按各自role OID和精确目标database OID核有效`CREATE=false`，并核`CREATE WITH GRANT OPTION`、`CONNECT WITH GRANT OPTION`、`TEMPORARY WITH GRANT OPTION`全部false；PUBLIC或membership带来的有效CREATE同样拒绝。PG16默认PUBLIC CONNECT/TEMPORARY允许保留，不把这两个普通连接/临时对象权限误写成全零。该补充只读校验，不修改共享数据库ACL，也不给runtime数据库owner或grant option。

十组跨域负权为APP/worker/projector各到mock/receiver六组，加mock到application/receiver、receiver到application/mock四组。每组schema USAGE/CREATE及grant option、全部table与column有效权及grant option、全部function EXECUTE及grant option均为false；无schema USAGE不能作为跳过后续对象检查的理由。全部显式ACL、default ACL及ACL依赖另按三schema八角色完整expected比较，未知grantor/grantee或外对象依赖拒绝。

自动table row/array type保留PG16 PUBLIC默认USAGE，不能把跨域has_type_privilege统一要求为false；类型USAGE不赋予table SELECT/function EXECUTE。named `fl_source_proof`及其数组对mock/receiver有效USAGE必须为false，实际raw ACL/default/effective分别核。peer未知额外domain/enum/range/composite/operator/cast等仍由closed schema集合拒绝，不将自动类型例外扩成任意通路。

## 4. 验证与边界

专项先验证完整fixture基线及两份peer SQL/inventory/prosrc来源一致；同caller RR检查八角色、十组负权，分别注入schema/table/column/function/grant option/membership/ownership越权应拒绝，ROLLBACK后重新成功。APP/worker/projector实际LOGIN只读catalog成功不等于可读peer业务行，至少以真实LOGIN确认peer SELECT仍42501。精确关闭本轮pool/backend后删除自有三schema/八角色并确认不存在，cleanup独立最多10秒。

数据库提升专项在ordinary两runtime及sandbox五runtime分别注入目标DB CREATE、CREATE-GO、CONNECT-GO、TEMPORARY-GO，另分别注入PUBLIC CREATE。只在专用数据库的未提交owner事务中改变ACL、同事务观察并拒绝，随后rollback；不得向共享moni_test提交这些授权。每项后实际LOGIN的新RR恢复基线，且确认原PUBLIC CONNECT/TEMPORARY仍存在，无授权状态泄漏。

完整checker专项仍须覆盖正式expected逐字段、raw正文、TOAST/内部RI/双向依赖、同名换OID、ordinary/public变体、三API proof事务绑定与Installation16表空集；本片辅助通过不单独宣称这些已完成。限定验证记录见[peer记录](../../docs/verification/funding-lot-catalog-peers.md)。

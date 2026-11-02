# 022-C0：固定 109 表 basis v1 只读叶子（根冻结）

2026-11-02。承接[维护候选 §5/§8](c0-maintenance-contract.md#5-basis具体编码与静态109表锁清单)。本片交付完整 basis 读取与严格 proof v2 [格式叶子](c0-proof-v2-codec-contract.md)，不交付 protected pre/init/post/epoch、CLI、RuntimeProof 或启动授权，不消费第八 ScanKind。旧迁移及七种 scan 生命周期不改。

## 1. 接口与静态材料

新增 `internal/fundinglotbasis`，生产无 billing、maintenance、catalog 依赖。允许标准库、pgx.Identifier 及嵌入静态材料；测试可单向引用 migrations 校核安装来源。

`Read(ctx context.Context,q Query,schema string) (Facts,error)`；Query/Rows 接口沿用 fundinglotscan 的最小形状。不得接收 caller Header、expected、balanced、proof 或 authority。Facts 私有 owned 状态，String/GoString/JSON 脱敏；Summary 仅含 Scope=`fixed_109_table_basis_v1`、PhysicalEOFComplete、ScannedTables=109、ScannedColumns=976、SourceRows、SourceBytes、BasisSHA256、ManifestSHA256、DescriptorSHA256、SendMetadataSHA256。零值无效且不暴露部分结果，没有 Verify/Coverage/Balanced/授权方法。

固定 descriptor 是维护合同 §8 的原始紧凑 JSON：39415B，无尾 LF，SHA256=`54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5`，109 表、976 列、128 PK 字段；95 单键、10 双键、3 三键、1 四键。列、PK、type OID/typmod/notnull/collation 均来自该受审材料，禁止 live 采样作为 expected。

固定 migration manifest 为当前 001–022 的 `(name,sha256)` 数组，2459B，无尾 LF，SHA256=`ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`；测试须与 embedded migrations 全清单交叉匹配。新增迁移须重新审材料，不修改历史迁移，也不动态接受 latest。

正式 send profile 从 [候选 JSON](c0-basis-send-metadata-candidate.json) 原字节复制嵌入：12360B，无尾 LF，SHA256=`1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b`。离线来源 PG16 postgres.bki 944104B，SHA256=`e7ccf0cbdf9a993e2bbfd99e3c4bb2161036ad33233d66281756750c409c983f`，NAMEDATALEN=64、FLOAT8PASSBYVAL=true。本地 PG16.15/Homebrew/aarch64 专库 RR READ ONLY 已逐项比较 706 个字段，0 差异；这不认证 C binary，也不推广为任意 PG16 生产认证。

profile 顶层固定 `format_version,types,functions`；15 type/16 send 按 OID 升序，字段顺序严格保持文件。types 为 16,17,19,20,21,23,25,26,869,1009,1184,1700,2950,3802,5069；send 为 2401,2403,2405,2407,2409,2413,2415,2419,2423,2437,2461,2477,2497,2962,3803,5083。包含 `_text→text` array 闭包及 record_send；name_send 不递归调用 char send。

## 2. 必须先于业务 record_send 的读取

固定同 q 自读环境：DB/schema 正 OID、server_version_num、server/client/database encoding、database locale/provider、stored/actual collation version。不设置或修复 client_encoding。仅接受 major16、三 encoding 为 UTF8（数据库编码整数6）、有效 provider c/i；接受 metadata 相同的 minor，精确版本进 hash。实际 locale/version 进 hash，不固定 C/C；现有证据范围仅本地 UTF8/C/C。

pgx SimpleProtocol 的参数插值会先于环境SQL检查 client_encoding=UTF8；实际非UTF8会在Query层失败，依本合同返回ErrExecution/零Facts且不读取业务，不声称已读到该环境。可实际返回非UTF8环境的Query则由叶子返回ErrMetadata；两条路径均拒绝，不能SET修复或捕获后当通过。

业务 send 之前必须全部通过：109 固定表、原列 exact ordinal/name/OID/typmod/notnull/collation、PK exact 顺序、permanent heap、无 RLS/forced RLS/继承/分区。tenants 可原8列，或仅精确新增第9列 `fl_created_xid xid8 NULL`（默认/owner/ACL/trigger 完整认证归未来同 RR catalog checker）；basis 仍只编码原8列。不接受 dropped/extra 原列或其他 drift。关系和属性从 pg_catalog 自行读取，不借调用者声明。

15 内建 type 与16 send 函数的全部 profile 字段逐项与静态材料相等。读取9个预期 NULL 的可变函数字段先用 IS NULL；非NULL直接 Metadata，禁止向客户端取未知大数组、ACL、config 或 SQL 正文。其余 metadata 文本同样先 SQL octet_length 限制，再 CASE 投影；非合格行保留并拒绝，不能 WHERE 隐去 drift。参数使用原 q 的绑定，标识符用 Identifier 严格引用，操作符/函数/类型使用 pg_catalog 限定。

本叶子不证明实际 RR、auth role、会话 owner、双锁或 tx 身份；未来真实 ReadLease 保证同 private session。Query/Facts 均为描述性输入与结果，不能直接充当初始化凭证。

## 3. 分页、预算与记录信封

全109表共享100000业务行、64MiB，SourceBytes 为原 PK record + row record 字节总和；单PK最大262144B，单row最大1048576B。metadata另限2048行/1MiB、单文本65536B；固定受审材料不占业务预算。限制是传入叶子的逻辑输入界，不宣称 simple-protocol 网络实际字节、PG 排序/record 构造内存或 RSS 上界。未来完整 pre 的68表来源预算与本预算如何组合另行冻结。

每表先 `COUNT(*) FROM ONLY` 精确取得行数写入 hash；实际扫描数必须相等。分页128，以 `encode(record_send(ROW(固定PK)),'hex') COLLATE pg_catalog."C"` 严格升序并前进（等价原二进制字节序），不能复用旧 text/int8 业务顺序。ROW/PK 只使用固定 descriptor 原列；不得把新增 birth 列、C0表混入。

页 SQL 采用 MATERIALIZED page→sizes→窗口 row_number 与 `sum(keylen+rowlen)::numeric`。qualified 同时核剩余行数、剩余 byte、单 key/row 大小，两份 CASE 只有 qualified 才输出 key/row；超限行仍返回 false 并触发 Resource，不用 WHERE 隐去以伪造短页/EOF。空表必须读一次空页，恰128整页必须继续读空页。每页完整 EOF/Err/Close，禁止忽略尾错误；严格 key 前进且全表 count 一致。

Go 逐 record 校验网络大端 int32 字段数、逐字段 OID 精确相等、长度只允许 -1 或非负合法范围，无截断/尾随字节；非空原列及全部 PK 禁止NULL。PK 各字段与 row 对应字段的 OID/NULL/payload 完全相等；固定宽度 builtin 核长度，variable 保留原字节，不转 float、不规范化 NUMERIC/JSON/text，也不冒称完整重验 PG native 值内码。可保留单页值与游标，不缓存全表业务明文。

## 4. 精确 hash 帧

所有整数 U 为 uint64 网络大端，F(b)=U(len(b))+b。nullable UTF8 N：NULL=U(0)；非NULL=U(1)+F(UTF8)，NULL 与空串不同。header 顺序：

```
raw ASCII "moni-aigate/funding-lots-basis/v1" + NUL
U(database_oid), U(schema_oid), U(16), U(server_version_num), U(6)
F(server_encoding="UTF8"), F(client_encoding="UTF8"), F(database_encoding_name="UTF8")
F(datlocprovider), F(datcollate), F(datctype)
N(daticulocale), N(daticurules), N(datcollversion)
N(pg_database_collation_actual_version(d.oid))
F(固定 send metadata canonical 原字节)
F(固定 migration manifest 原字节)
F(固定 descriptor 原字节)
```

不加入 version() buildstring、DB/schema名、owner、PID、top xid、nonce、as_of、phase、actor 或 birth 列。plan/init 的事务身份不同，必须只在未来 private authority 中绑定，不能使跨两次比较的 basis 恒不相等。

descriptor 原顺序逐表：`F(table_name), U(column_count), F(primary_key紧凑JSON数组), U(COUNT)`，再逐行 `F(raw row record)`。PK仅用于游标/预算，不重复 hash。空表仍有完整表头与U(0)。SHA256小写hex，完整成功后才可发布。

## 5. 错误、取消与验收

ErrInput/ErrResource/ErrCanceled/ErrTimeout/ErrMetadata/ErrExecution 为脱敏固定哨兵，任意错误整个 Facts{}。nil ctx、typed nil Query、非法 schema 拒绝；资源错误不得被 Close 触发的 context 撤销覆盖。Query/Scan/EOF错误均拒绝；每行及大块SHA写入前后检查ctx。取消由原 Query/lease负责，叶子不新建连接/事务或 goroutine。错误不得带SQL、schema、行值、PG detail 或来源明文。

必须有独立硬编码 hash 帧 golden（含NULL/空串、version变化、非空NUMERIC/JSON/bytea/Unicode/text[]/timestamp）、descriptor/manifest/profile固定SHA与来源交叉、full109/976、复合PK、binary cursor、128边界、全EOF/count不一致、预算CASE、envelope/字段NULL/OID/宽度/PK对照、metadata前置/drift、租户精确delta、SQL注入引用、取消/timeout/尾错误和失败零值测试。

真实专库 PG16 测试在隔离 schema 内安装原迁移，验证非空 basis、同RR读稳定、业务一字节变化改SHA、client encoding或send目录不匹配拒绝（可事务内fixture，不改集群配置），无秘密输出、全清理。DB专项串行，不影响同集群预览库。兼容差分测试由 maintenancetrust 单向import纯 proof leaf，不令领域生产反向依赖维护。根语义复核后同步README、HLD/LLD、统一架构、维护图、计划及验证证据，reviewed refresh/check，再执行专项race、fuzz、make check与明确专库verify-mvp，提交推送并核远端/CI。

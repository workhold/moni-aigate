# 022-C0：严格 proof v2 纯 codec（根冻结）

2026-11-03补充：后续[R1-B运行组合合同](c0-runtime-composer-contract.md#3-已授权的最小-v1-纯格式-api)明确授权最小V1纯格式API，替代本页“V1只保持私有helper”的出口限制。V2 wire、旧payments/maintenance逻辑和来源pins保持；V1仍不认证文件、秘密、数据库或启动，具体接口及旧成功/拒绝边界以新合同为准。

2026-11-02。承接[维护候选](c0-maintenance-contract.md#3-proof-v2-与旧b3的精确结合)与[运行组合候选](c0-schema-check-contract.md#71-完整-runtimeproof-的独立组合层)。本合同冻结完整格式叶子，不发行数据库、支付真实性、维护、初始化、epoch、RuntimeProof 或启动权限；原 v1 wire/loader/拒绝条件不改，旧 migration 不改。

## 接口与所有权

新增 `internal/fundinglotproof`，生产只依赖标准库。`EncodeV2(V2Input) (*CanonicalV2,error)`、`DecodeV2([]byte) (*CanonicalV2,error)` 是唯一构造；`CanonicalV2`全私有 owned canonical bytes、SHA、deep-owned bindings。`Bytes() []byte`返回clone，`SHA256() string`仅有效实例非空，`Bindings() (V2Bindings,error)`返回深复制普通数据；V2Input/V2Bindings不携带权威。nil/zero无效，Bytes/SHA为空，Bindings固定错误。所有失败返回nil及唯一 `ErrCanonical = errors.New("funding_lot_proof_invalid")`，不包含原值。String/GoString/MarshalJSON一律redacted，实际bytes只能显式Bytes取得；JSON不能构造有效实例。

不接受pgx、ctx、文件路径、DSN、secret、CatalogProof、Permit或caller verified布尔；没有文件/网络/SQL、副作用或在线调用。输入、Bytes、Bindings的深层slice/pointer变异不能改变已验证对象；并发只读安全。

## 完整封闭 wire

canonical顶层顺序恰21字段：

`domain, proof_version, mode, epoch_id, database, app_schema, app_role, projector_role, sandbox, basis_version, basis_digest, basis_descriptor_digest, migration_manifest, manifest_digest, catalog_descriptor_version, catalog_postgres_major, catalog_expected_digest, catalog_schema_digest, catalog_roles_digest, predecessor_payment_digest, predecessor_payment_canonical_base64url`。

`domain="moni-aigate/funding-lots-runtime/v2"`、proof_version=2、basis_version=1、catalog_postgres_major=16；catalog_descriptor_version为正uint32。mode只有ordinary/sandbox，epoch_id为非nil、小写规范36字节UUID；所有digest为64字节小写hex。不要加入自身SHA；SHA只hash完整canonical JSON原字节，不在外部再加域前缀。

嵌套对象字段同样恰好且按此顺序：Database={host,port,name,oid}，Role={name,oid}，Schema={name,oid,namespace_owner:Role,object_owner:Role}，Migration={name,sha256}，Sandbox={mock_schema,mock_role,receiver_schema,receiver_role,worker_role}。

OID正uint32，port正uint16。name沿现catalog Pins：非空UTF8、最多63B、无NUL；role不以pg_开头；schema不以pg_开头且不是information_schema。Schema namespace_owner通常等于object_owner，唯一额外形式为public namespace_owner={name:"pg_database_owner",oid:6171}。host非空UTF8、最多253B、无NUL、不以/开头，与catalog相同，不把它当可连接/可信地址。普通owner不强制NOLOGIN，格式无login字段，实际身份由后续catalog核。

ordinary恰三个角色（app object owner、app_role、projector_role）name/OID分别互异；sandbox恰八角色（另含mock/receiver两owner与两runtime及worker）分别互异，三个schema分别互异，sandbox各namespace_owner=object_owner。不增加跨catalog类别OID互异限制。

## manifest 与摘要含义

migration_manifest为1..256项非空数组，每项恰name/sha256；文件名ASCII basename、最多255B，匹配`^[0-9]{3}_[a-z0-9_]+\.sql$`，严格ASCII升序、无重复，无路径。manifest_digest必须是该数组按本规范字段顺序及JSON编码的原字节SHA256。不硬编码当前22项为未来latest；缺少actual latest条目是未来issuer比较的拒绝条件，纯codec不猜最新清单。

basis_descriptor_digest、catalog_expected_digest、catalog_schema_digest、catalog_roles_digest保持四个独立字段：分别是旧109表/976列描述符、完整catalog expected root、匹配静态variant的schema与roles canonical SHA。纯codec仅验证digest格式，不把当前109 SHA或mode-root表硬编码成数据库认证，也不据已知hash构造权威。**互换两种合法digest或ordinary/sandbox expected root不是纯codec拒绝条件**；未来停服init issuer必须与同事务实际basis、CatalogProof及完整history逐字段相等，不得只信这些自报值。basis_version固定的是编码版本，实际basis叶子当前只支持固定109表描述符。

这里约束的是开账原件发行，不冻结普通startup的历史数据重构。109表包含经营后会变化的users/sessions/requests/journals等；不能在普通startup强制当前109表basis等于历史cutover basis。已有epoch的观察、历史basis在v2与epoch间的绑定及当前资金图核验须另冻runtime合同，不能由纯codec或当前基线摘要补出这些权限。

## ordinary / sandbox 与 v1 原件

ordinary的sandbox、predecessor_payment_digest、predecessor_payment_canonical_base64url必须全部显式null；其余字段禁止null。sandbox时三者全部非null，任何部分组合拒绝。base64为RawURLEncoding且解码后重新编码完全相等，拒绝padding/标准base64/换行，最大87382B；原v1 bytes为1..65536B。

sandbox完整复核原v1私有严格codec的格式、语义及canonical字节，原字节SHA等于predecessor_payment_digest，再逐项绑定DB、三schema名字/OID/namespace与object owner及runtime、worker/projector。原v1全部environment、merchant/label、nonce/token摘要、origins/callback、keyset由原件及SHA绑定，不重复放易漂移摘要；没有token/key明文。原v1仍只允许127.0.0.1:55439/moni_test、原schema格式、三NOLOGIN owner与五LOGIN runtime，不扩为live。

原v1相等key时间（not_after=not_before）保持合法，不新增全key digest互异规则；v1原件不含peer state_directory_id及其nonce/control摘要，纯codec不能宣称已核这些DB行。不得修改paymentruntime/maintenancetrust原v1逻辑；新leaf私有v1 helper独立保持旧合同，兼容差分测试放在maintenancetrust的测试中单向依赖此纯叶子，普通领域测试不得反向import维护/catalog。

## canonical、资源及验收

输入v2原件1..262144B、UTF8且无BOM，递归深度最多12（根容器深度1），最多8192个JSON值节点（每个scalar/null/container算1，键名不另算）。对象exact fields，拒绝缺/多/解码后重复键、错类型、trailing token、坏escape/裸控制符/孤立UTF16 surrogate。整数仅无前导零的无符号十进制（0只在容许字段使用），拒绝负数/负零/指数/小数/超宽；模式字段外null拒绝。

固定struct顺序，json.Encoder.SetEscapeHTML(false)，仅移除encoder的一个尾LF；Decode严格解析后重编码必须与原bytes逐字节相同，拒绝非规范空白/顺序/转义、BOM和尾LF，不排序或修复输入，不做Unicode规范化。Encode先验输入并同样受总尺寸/节点/深度预算，不得忽略输入字段或将坏结构修成成功。

独立硬编码ordinary/sandbox golden原bytes及外部期望SHA，不能仅Encode→Decode自回环。覆盖ordinary named/public与Unicode名称、sandbox原v1全部资格、1/128 key与相等时间、所有可空union组合、逐DB/schema/role不匹配、原件/SHA/base64/非canonical篡改、manifest排序/重复/路径/摘要、数值/UTF8/UTF16/重复键/缺多/深度/总大小/节点界、nil/zero/JSON伪造、alias与并发race。未知合法digest可通过纯codec，后续issuer拒绝测试不能冒充本片已实现。与原v1 codec对合法/不合法向量做差分；短fuzz任意原件不得panic，失败nil无部分值。静态测试确认生产只stdlib。

根集成后同步README、HLD/LLD、统一架构和验证记录；相关race/vet/fuzz、make check和明确专库verify-mvp通过后提交推送。生产迁移、protected pre+basis/init/post/epoch、DB绑定、RuntimeProof、APP/worker启动与资金写入仍为后续依赖，不用本纯CanonicalV2替代它们。

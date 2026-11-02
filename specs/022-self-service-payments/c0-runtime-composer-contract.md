# C0-R1-B：独立 APP 完整资金运行核验（根冻结）

2026-11-03，根按当前 `1844c44`、旧 loader/codec、实际 scanner/pgx 源码与独立审查冻结。先冻结本合同再实施；这是库级运行核验合同，不是已经通过的产品/生产验收。根采纳独立组合层、V1纯格式接缝、同原行投影、六条静态兼容、双模式比较值、caller-owned寿命及完整非空验收；下文与末节明确所有裁决。

目标仍是：actual APP LOGIN 在同一个 caller-owned RR READ ONLY 中，完成当前完整 catalog/history、原 epoch/v2、sandbox 自有 v1/本地状态、84 表资金全图及八算法核验后，发行只对原事务有效的完整非空 `RuntimeProof`。本片不接普通 main、HTTP、billing、worker、projector、在线资金 helper 或 ready。

## 1. 已核源码与本次需要的新接缝

| 当前源码 | 已有行为 | R1-B 处理 |
| --- | --- | --- |
| `internal/fundinglotproof/types.go, codec.go, v1.go` | 仅公开严格 V2；V1 为私有 codec，V2 已重核完整前驱 V1 和 DB/三 schema/八角色格式绑定 | 明确冻结最小公开 V1 纯格式 API，不往 codec 加文件、secret、pgx 或授权能力 |
| `internal/payments/runtime_types.go, runtime_codec.go, runtime_files.go, runtime_load.go` | 旧 APP manifest 的完整严格 JSON、文件权限、nonce 持有及全部 key 材料验证；`Options` 私有状态 | 原 API/格式/bytes/成功和拒绝条件不变；新独立 loader 保持其 APP 语义，不接受 `Options` 或自报 DTO 作为认证能力 |
| `internal/payments/runtime_catalog.go` | actual runtime 对自己的 marker/attestation 核验；peer 内容零读取权 | 新组合层核自己 APP 两原行；不复制 O1 读取 peer 原件的维护路径 |
| `internal/fundinglotcatalog/profile.go, proof.go, identity.go, collector.go, wallet_expected.go` | R1-A 显式 `WalletReadV2`；全 catalog 和 selected descriptor 预算；原具体事务/端点/LOGIN 绑定 | 组合层内部自行调用 `VerifyRuntimeProfileTx(..., Application, WalletReadV2)`，不接 caller CatalogProof |
| `internal/fundinglotscan/wallet.go, checks.go, projection_wallet.go, projection_sandbox.go` | 同次 84 表、776 唯一物理列；八算法；private `WalletFacts`；普通模式也物理读取 payment 23 表并核其边界 | 不调用旧 billing SQL oracle，不另开 scanner 快照，不拼旧分域 Facts/report |
| `internal/fundinglotscan/epoch_proof_projection.go` | `ProjectEpochProof` 只从合格原 84 表结果深复制 epoch/v2 两单例；不查 SQL | 原 API 保留；另补纯投影旧 APP payment marker/v1 attestation 的接缝，见 §5 |
| `migrations/verify_readonly.go, embed.go` | 同 caller RR RO 核完整 current embedded history；不安装、不结束事务 | 先固定行数/字段界预检，再调用原完整检查；独立构造 current manifest 的描述值，不 import 维护 helper |
| `scripts/online-dependency-qa/dependencies_test.go` | main/server/billing/periodicbudget 的生产与测试依赖禁止到 maintenance/catalog | R1-B 不引入这些在线包对新组合层的依赖，也不降低现门禁 |

`c0-schema-check-contract.md` §7.1 的 billing namespace、三 scanner 和先核 v2 顺序是旧候选。当前 scanner 已拆为独立叶子，根须用本片明确替代相关候选，不能以旧候选直接授权 billing import catalog。

## 2. 包边界与最小组合 API

新增包 `internal/fundinglotruntime`。生产依赖限于标准库、pgx、`golang.org/x/sys/unix`、仅临时验证keyring资格的纯`paymentproto`、`fundinglotcatalog`、`fundinglotproof`、`fundinglotscan`、必要的 `fundinglotverify` DTO 和 `migrations`。不 import billing、payments、operations、maintenancetrust、maintenancescan 或运行构造器；不建 pool、不开新连接、不选用另一角色、不调用 HTTP/model/SMTP/PSP。

```go
package fundinglotruntime

var ErrRuntime = errors.New("funding_lot_runtime_verification_failed")

// 全私有，仅代表本机旧 APP 文件材料的一次完整一致快照。
type ApplicationState struct { state *applicationState }

// 全私有，仅证明原 RR RO 内完成本合同所有阶段。
type RuntimeProof struct { state *runtimeProofState }

func LoadSandboxApplicationState(ctx context.Context,
    absoluteManifestPath string) (*ApplicationState, error)

func VerifyOrdinaryApplicationTx(ctx context.Context, tx pgx.Tx,
    pins fundinglotcatalog.Pins,
    expected *fundinglotproof.CanonicalV2) (*RuntimeProof, error)

func VerifySandboxApplicationTx(ctx context.Context, tx pgx.Tx,
    pins fundinglotcatalog.Pins,
    expected *fundinglotproof.CanonicalV2,
    local *ApplicationState) (*RuntimeProof, error)

func (*RuntimeProof) VerifyTx(ctx context.Context, tx pgx.Tx) error
```

- 两个模式入口分别要求 ordinary/sandbox pins，且 expected 同模式；不接 nullable union/trusted bool/自报 report 或跳过选项。ordinary 不加载 sandbox 文件。sandbox 的 nil/zero `ApplicationState` 拒绝。
- expected 必须为有效 strict `CanonicalV2`，入口立即取得 owned bytes/bindings；它只作预期原件比较，必须与这次扫描的真实 v2 原件逐字节相同。**该参数不证明 caller 文件路径、文件所有者或文件来源已认证。** R1-B 不新增 v2 文件格式或 v2 文件 loader，不从 DB 自动采纳 expected，不接受 nil 表示自动采用。
- pins 为普通比较值，入口深复制所有可空指针；actual DB/端点/schema/角色均由同事务 catalog 认证，不因 pins 或 canonical 自报 OID 而受信。
- 不返回 raw `WalletFacts`、原 canonical、nonce、key、peer 数据、SQL 错误或部分 proof。新类型的 nil/zero 无效，String/GoString/JSON 全 redacted；没有 JSON 反序列化或公开字段构造有效状态的途径。
- `ApplicationState` 的只读副本可复用，仅表示同一加载快照；`RuntimeProof` 副本共享原不可变 identity，不能重新绑定。调用方不能在使用期间并发修改参数 wrapper 或使用同 tx。
- `ErrRuntime` 为 loader/composer/proof 的固定脱敏出口；纯 codec 仍只返回其原 `ErrCanonical`。错误阶段可有包内封闭枚举供测试，不能把原始配置、路径、secret、数据库行或底层 SQL 错误拼入 public error。

## 3. 已授权的最小 V1 纯格式 API

本节明确扩展原 `c0-proof-v2-codec-contract.md` 的私有V1限制，只新增以下纯格式出口；V2 wire、旧payments/maintenance路径及其源SHA不变。旧helper可保持私有，由新API调用同一既有核心。不得借此新增文件、秘密、数据库或启动授权。

```go
package fundinglotproof

type V1Role struct {
    Name string `json:"name"`
    OID uint32 `json:"oid"`
    Login bool `json:"login"`
}
type V1Schema struct {
    Name string `json:"name"`
    OID uint32 `json:"oid"`
    Owner V1Role `json:"owner"`
    Runtime V1Role `json:"runtime"`
}
type V1Database struct {
    Host string `json:"host"`
    Port uint16 `json:"port"`
    Name string `json:"name"`
    OID uint32 `json:"oid"`
}
type V1KeyPin struct {
    ID string `json:"id"`
    Digest string `json:"digest"`
    NotBefore string `json:"not_before"`
    NotAfter string `json:"not_after"`
}
// 19 个顶层字段及顺序与现 runtimeProof/v1Proof 完全相同。
type V1Input struct {
    ProofVersion int `json:"proof_version"`
    ProtocolVersion int `json:"protocol_version"`
    EnvironmentID string `json:"environment_id"`
    MerchantID string `json:"merchant_id"`
    PayeeLabel string `json:"payee_label"`
    Database V1Database `json:"database"`
    App V1Schema `json:"app"`
    Mock V1Schema `json:"mock"`
    Receiver V1Schema `json:"receiver"`
    Worker V1Role `json:"worker"`
    Projector V1Role `json:"projector"`
    AppNonceDigest string `json:"app_nonce_digest"`
    WorkerNonceDigest string `json:"worker_nonce_digest"`
    APITokenDigest string `json:"api_token_digest"`
    APIOrigin string `json:"api_origin"`
    ControlOrigin string `json:"control_origin"`
    AppListenOrigin string `json:"app_listen_origin"`
    CallbackURL string `json:"callback_url"`
    Keys []V1KeyPin `json:"keys"`
}
type V1Bindings = V1Input // 普通格式值，不是 secret/文件/DB/启动能力。
type CanonicalV1 struct { state *canonicalV1State }

func EncodeV1(V1Input) (*CanonicalV1, error)
func DecodeV1([]byte) (*CanonicalV1, error)
func (*CanonicalV1) Bytes() []byte
func (*CanonicalV1) SHA256() string
func (*CanonicalV1) Bindings() (V1Bindings, error)
```

V1 的 Role 含 Login，Schema 是旧 owner/runtime 结构，不能用 V2 Role/Schema 替代。Encode 先验完整旧语义与 UTF8，不让 json.Encoder 修复坏文本，再按旧 struct 顺序编码；Decode 对 1..65536 字节严格解析、旧语义验证、规范重编码且原 bytes 必须完全相等。nil/zero、错误、owned bytes/Keys、并发只读、redacted 方法与 V2 同等级；只有这五个业务 API，不增加认证/导入文件/解码 secret API。

必须保留的旧语义：

1. proof/protocol 均 1；端点仍仅 `127.0.0.1:55439/moni_test`、非零 DB OID；三 sandbox schema 的固定前缀和同 32hex suffix，三个 NOLOGIN owner、五个 LOGIN runtime；八角色 name/OID 互异。
2. environment UUID、merchant/payee 词法、三个 nonce/token 摘要格式、三个 loopback origin 互异及 callback 指回 app listen 全部保持。
3. 旧V1允许`pg_`角色前缀及不同catalog类别间相同OID，不能借V2外层或actual catalog规则收紧其纯格式；八角色内部、三个schema内部的互异仍按旧语义。1..128 个按 ID 严格升序 key；ID/digest、微秒 UTC 时间格式保持；`NotAfter == NotBefore` 仍合法。不同 ID 使用相同 digest 的纯格式原件不因新增“digest 互异”规则被拒绝；材料复用由 loader 拒绝。
4. 旧严格 parser 的 UTF8/UTF16 escape、解码后重复键、null、缺/多字段、类型、trailing token、整数词法和范围拒绝条件保持。V1 递归深度沿原 helper 根从 0 起计，不能照搬 V2 根从 1 起计而缩窄旧格式；不引入会拒绝旧合法输入的新节点上限。
5. 不检查文件、nonce 持有、key 明文、当前所有 key 是否未过期、数据库身份或 peer 当前状态。V2 的前驱验证继续用同一旧语义核心，且不改变已冻结 V2 bytes/拒绝条件。

## 4. APP 本地文件状态：完整保留旧 loader 边界

### 4.1 输入是原 APP manifest

形状仍恰六个字段：

```text
manifest_version: 1
component: "application"
proof: 上述 V1Input 的完整嵌套对象
proof_sha256: 规范 V1 bytes 的 SHA-256
nonce_file: {name, sha256}
keys: [{id, material_base64_url}, ...]
```

loader 必须先严格解析**整个 manifest**：拒绝重复键（包括转义后同名）、坏 UTF8/UTF16、null、缺/多字段、错类型、trailing token、整数指数/小数/负零/负数/溢出。合法 manifest 可有旧格式允许的空白及字段次序；不得直接把嵌套 `proof` 原文送 `DecodeV1` 后因非规范排版误拒。推荐 typed `Proof V1Input`，递归 exact shape 完成后调用 `EncodeV1` 并核 `proof_sha256`。普通 json.Unmarshal 或只有 DisallowUnknownFields 不足以完成本合同；Encode 看不到被宽松解码抹掉的错误。

### 4.2 文件与材料精确界

| 项目 | 必须保持的边界 |
| --- | --- |
| 路径 | 绝对、filepath.Clean 后不变；manifest basename 沿旧 `[A-Za-z0-9_-][A-Za-z0-9_.-]{0,127}` |
| 目录 | 实际父目录 FD，目录/euid 所有、mode&07777 恰0700，O_DIRECTORY/O_NOFOLLOW/O_CLOEXEC；后续文件用该 FD openat |
| 文件 | O_NOFOLLOW/O_CLOEXEC/O_NONBLOCK；实际普通文件、euid、mode&07777 恰0600；stat 大小与读出均 1..固定界，LimitReader(limit+1) |
| manifest | 整个原文件 <=65536B；不能仅限制 nested proof |
| nonce 文件名 | 原 secret basename `[A-Za-z0-9_-]{1,64}`；不能与 manifest 同名；文件 SHA 与 manifest pin 完全相同 |
| nonce 原文 | 上界44B，RawURL 32B 材料恰43字符，可选单个尾LF；拒绝padding/标准base64/多LF/空白 |
| nonce 绑定 | `SHA256("moni-payment-application-state-v1" || 0x00 || nonce32)` 恰 AppNonceDigest |
| signing keys | manifest 数量与 V1.Keys 相同；逐项 ID/顺序匹配；每个 JSON material 必须恰43字符的 RawURL 32B，不能借 nonce decoder 接受尾LF |
| key 绑定 | `SHA256("moni-mock-signing-key-v1" || 0x00 || key32)` 恰各 pin.Digest；nonce 与所有 key32 两两不复用，key32 互不复用；保留 NewKeyring 的原资格而不额外要求当前时间有效 |

nonce 按其单尾LF规则取材料文本、key按恰43字符原文本后，二者都必须 RawURLEncoding 解码成功且恰32B，并重新编码逐字等于该文本；拒绝未使用尾位非零等可解码为相同32B的非规范拼写，不能只核长度/域摘要。旧keyring的active ID仍由原排序manifest的第一项确定，不新增caller active selector。

不读取 worker nonce、API token、worker DSN；V1 内这些摘要是原件声明，不是 APP 持有证明。目录 FD 防止后续 basename 解析逃逸，但旧实现并不证明所有祖先路径均无 symlink，不能在文案中升级承诺。manifest/nonce 每次读取组成被摘要约束的一致快照，不宣称多文件在某个全局瞬间原子一致，也不监听后续替换。

成功 state 仅保存 deep-owned V1 canonical/bindings、APP nonce digest、必要的非秘密文件摘要/加载事实；不保留 nonce/key 明文、原含 key 的 manifest bytes 或开放 FD。不声称 Go 字符串/堆内存已被安全擦除。所有退出关闭自己打开的 FD；没有跨事务文件 capability 或 caller 自报 verified bool。

loader 的成功窗口固定独立最多5秒，沿 caller 更早 deadline；每次文件/CPU阶段前后核 ctx 和绝对时刻。这个界限是成功接纳界，不把普通文件系统阻塞误称为 O1 级 raw-socket 强制清理保证。原 payments loader 不因此改变时间语义。这个5秒沿更早caller截止，必须保留ctx和绝对时刻双重检查，不改变旧payments loader本身的时间语义，也不占用之后composer的30秒。

## 5. 同一次 84 表扫描的旧 APP 原件纯投影

当前 `ProjectEpochProof` 不导出旧 `payment_sandbox_environment` / `payment_runtime_attestations`，新组合层不能直接访问 `WalletFacts` 的 private raw。新增以下只读描述投影，而不重复 SQL 读取原件：

```go
// internal/fundinglotscan；返回普通 DTO，不携带任何认证权威。
type ApplicationPaymentProofRows struct {
    Environment *fundinglotverify.SandboxGraphEnvironmentRow
    Attestation *fundinglotverify.SandboxGraphAttestationRow
}
func ProjectApplicationPaymentProof(ctx context.Context,
    facts WalletFacts) (ApplicationPaymentProofRows, error)
```

资格沿 `ProjectEpochProof`：原84表/registry/EOF/原累计行与字节界、report与raw关联、完整覆盖、算法状态、全部 mismatch/unproven 为零；使用本次ctx和新投影视图，不继承原ctx取消、不改变原rejected或facts；不查询SQL、不重新跑经营后basis。

从 `sandboxBindings()[0:2]` 的原始两组做精确投影。ordinary 要求两组都0行并返回两nil；sandbox 要求各恰1行、两个非nil。保留全部字段，并深复制所有字符串、canonical bytes 及指针；不能只返回一份 caller 可伪造的“v1已认证”摘要。失败返回零 DTO 和原 scanner 固定错误。新接口不改变原 `ProjectEpochProof`、O1、7/8 kind 或 maintenance capability。

本片确定纯投影路线；不另查两原件。新增scanner生产文件须同步已审readset的精确源pin集合，根逐项确认SQL/registry/物理列并集仍为84/776，只有新增投影与其固定源SHA进入清单，不盲刷新其他pins。

## 6. APP 自有 DB 认证字段与原件三方相等

catalog 先证明两表是预期 owner 的真实 permanent heap、无RLS、无隐藏读取/替代物及精确ACL；APP 只读取自己 app schema。投影后的原行另做以下字段绑定，不能只凭完整 `CheckSandboxSourceGraph` 的 descriptive 状态冒充本地持有证明：

| APP 原行 | 逐字段合同 |
| --- | --- |
| `payment_sandbox_environment` | 唯一且singleton=true；ID=V1.EnvironmentID；MerchantID/PayeeLabel=V1；DatabaseOID/SchemaOID=actual pins；NonceDigest=V1.AppNonceDigest=local已验APP摘要；AppRole/WorkerRole/ProjectorRole为V1对应角色名；ProtocolVersion=1；CreatedAt沿既有原生PG/graph时间的1..9999年和微秒资格，不能因Go零值表示合法year1就增加非zero限制 |
| `payment_runtime_attestations` | 唯一且singleton=true；EnvironmentID、DB/schema/owner、APP/worker/projector OID逐项等于actual pins和V1；AppNonceDigest/WorkerNonceDigest等于V1；ProofVersion=1；CreatedAt保留旧runtimeVerifyAttestation明确的非zero/1..9999年要求并沿scanner微秒资格；canonical SHA匹配ProofSHA256；DecodeV1原bytes严格；canonical与local V1逐byte相等 |
| V2 内嵌前驱 | 原RawURL严格解码/重编码相同；digest=V1 SHA；完整V1 canonical与APP attestation、local三方逐byte相等；沿V2 codec已核的DB/三schema/八角色，再与actual pins逐字段比较 |

原 v1 的 origins、keyset、环境/merchant、nonce/token 摘要由完整原件 bytes 和上述三方比较绑定，不丢弃为几个自报字段。APP 只能证明自己的nonce和自己加载的signing key材料；WorkerNonceDigest/APITokenDigest仅为保存的原件值。

不得读取 mock/receiver 的 environment、history、state_directory_id、nonce/control digest 或实际 keyset来补认证。三schema/八role及peer权限只来自catalog的真实静态事实。R2由各actual runtime自核自己的材料后再决定整体ready；R1-B不为peer当前状态背书。

## 7. 固定历史 cutover → current profile 映射

### 7.1 映射键与匹配规则

维护初始化时保存的 v2 绑定 LegacyV1 catalog，R1-A 追加四列SELECT后当前完整 catalog为WalletReadV2；二者 roles/expected root不同。不能强制历史root等于current，不能更新/re-sign历史v2，也不能直接接受任意旧root或“所有历史前缀”。

每个静态记录的键必须完整包括：

```text
历史：mode + catalog_postgres_major + catalog_descriptor_version
    + 历史catalog profile(由下面root tuple唯一解析为LegacyV1)
    + catalog_expected_digest + catalog_schema_digest + catalog_roles_digest
    + 历史migration_manifest完整规范bytes及manifest_digest
    + basis_version + basis_descriptor_digest
目标：当前显式WalletReadV2 + PG major/descriptor version
    + 当前expected/schema/roles完整tuple
    + 当前完整latest manifest bytes/digest
```

实际names/OIDs/端点不做跨环境静态常量：它们由actual pins/catalog认证，再与原v2/V1逐字段绑定。variant name不是v2 wire字段，须由历史root/schema/roles唯一解析；不能只按mode或前缀挑一个variant。current CatalogProof的profile/全部摘要必须等于该唯一目标，不允许任意组合mode内某个schema/另一个roles。

映射为受审包内封闭静态表，无外部文件、注册API、live导出回填、caller supplied映射或“root相同则默认放过”。这些静态材料应有独立复算工具/固定artifact；当前`fundinglotcatalog`无需暴露私有descriptor树，只消费其公开proof摘要。

### 7.2 初始允许的六条映射

共同：PG major=16、catalog descriptor version=1、basis version=1；basis descriptor=`54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5`。历史与当前暂均完整001–022，规范manifest共22项/2459B，SHA=`ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`。完整每项name/sha也必须核，不只核这段文字中的总摘要。

| mode | 历史 LegacyV1 expected root | current WalletReadV2 expected root |
| --- | --- | --- |
| ordinary | `a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2` | `c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20` |
| sandbox | `a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f` | `42a63f7178748ea5dd4ce6964cbe9cd77d0131ca8dad795fbd61a4735b206706` |

schema SHA每条映射历史=current：named三variant=`089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24`；public两variant=`3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926`；full-sandbox=`ed7698eb58d76621f18b7e6aae0000f262549a4b565d9a04e946c1436bff0b02`。

| 唯一variant | 历史roles SHA | current roles SHA |
| --- | --- | --- |
| named-isolated | `b032e7b1626df0cb1fc3160619d808fd382c966ccb66258a61cc3fb1e29b3609` | `57ed93278687cbac78081e4e29189d65f5218b0c02ce32e635826db7a1a34413` |
| named-minimal-login | `d7d632a6e77082c79a5dce0e558fe46e9d37c02c4c63702371f4dd84fbd41b62` | `ee33f5673c7a7f93d2e950d58d0713112d67453778149f5fa9a7b3ef3266815f` |
| named-image-login | `b1a1c15e35149789b1cba3ecb58197704b0e6c1a4c296d26efc0fbd33ee58f5f` | `bb99b7c11d267d7e3f0e5be62b36a5eda65638afefc3eab3936b8c60442da168` |
| public-minimal-login | `4c8037ece6c9a07569c922b31088188e30f593021413bad656ef36e356a2b644` | `c3d7f7f4f3631153911c8867ae2d4d35eb5e4e45543836541ddbb2bf885aa3f1` |
| public-image-login | `864d06014ceb1a5bc28bafcbf81b216dc0476abc2b2bf61fb380f7d2952173b3` | `d140513eecfbf603d78ae65a5d527d39b4054f50666c9e4229315dc3f12890d0` |
| full-sandbox | `186b82132de40cf01a46b04dd7aace17248a73b53d52f5228f07d376fb11dfbf` | `28e771bd6f6b96817910252d21d17f09f9d4753f99841562a788f27d90622f4e` |

本表历史值由2026-11-03只读Python直接从原两份v1静态JSON逐variant规范编码独立重算；current值与R1-A修正后artifact一致。根冻结这些值，实施时仍须增加独立固定artifact/复算工具及逐值测试，不能从live metadata回填。sandbox修正前不完整候选root `e59039d07816d11d45ed42cc91c9f64928c93f67f2f77bab5e4c1451f9bc1fc9` 没有成功catalog验收，不作为历史兼容项。

未来正式迁移改变current manifest/catalog时，必须再审新增完整转换记录；R1-B不能预先接受未知migration，也不能把合法historical manifest前缀当current latest。原O1/Installation仍各自使用原LegacyV1语义，不作全局root替换。

### 7.3 历史 basis 和计数

`basis_digest` 是数据专属历史值，不放入跨环境静态映射常量。要求原epoch与原V2完全相同；basis version/descriptor/历史manifest按上述已审键匹配。经营后的109表已变化，不重算basis强等cutover，不从当前业务总数替换历史计数。

epoch历史count/available/held与immutable legacy/opening集合和当时开账关系，交给本次真实完整WalletLots replay重核；当前新增业务/tenant及FIFO/预占/结算/分次退款沿现八算法重放。不能以初始化专属postCounts或纯allocation planner充当运行时真值。

## 8. 同一事务内的精确阶段与发行条件

私有单调用阶段：`entered → catalog-entry → history-entry → wallet-read-and-check → project-originals → bind-originals → catalog-exit → history-exit → identity-exit → issued`；任一异常终止且nil proof。这个状态机不取得maintenance gate、安装许可、写能力或全局once。

1. 检查ctx/绝对期限，捕获深复制pins、owned expected以及sandbox local的私有immutable snapshot；拒绝nil/zero/模式错配。tx由caller预先用actual APP建立RR READ ONLY，调用期间独占。
2. 内部 `VerifyRuntimeProfileTx(work, tx, pins, Application, WalletReadV2)`，完整collector成功，profile/matchesPins核对。不可用任意named-role snapshot proof、Worker或Projector proof替代。
3. history先执行§12的固定受界布尔预检，原完整22行、name界35字节、checksum恰64，禁止额外行/NULL/重复/超界，再 `migrations.VerifyReadOnlyTx` 核完整current embedded清单；从只读 `migrations.Files` 固定排序生成描述性manifest并匹配本片静态current材料，不调用维护manifest issuer。
4. 私有adapter仅持原tx/work context，固定schema/mode调用 `ReadWallet`。scanner真实读取84张表并跑ledger、source links、funding、customer key、procurement evidence、PB、sandbox、wallet八算法。普通sandbox算法唯一为not_applicable，其余应executed；sandbox八个均executed。
5. 只接受scope=`complete_source_wallet_graphs_v1`、consistent、84表PhysicalEOFComplete/CoverageComplete，所有input/journal/ordinary boundary mismatch、分域mismatch/unproven=0。`ReadWallet`返回nil error不单独代表合格；可用ProjectEpochProof原严格资格做最终重核，不能只信caller report。
6. 在同次facts上调用原 `ProjectEpochProof` 和新 `ProjectApplicationPaymentProof`；前者恰一epoch/一v2 attestation，后者按mode空/单例。投影不再查询数据库原件。
7. 对v2原件DecodeV2，SHA等于attestation、epoch runtime_proof_digest和expected SHA；原bytes=expected bytes。逐字段核21个bindings，实际DB/owner/schema/角色均等pins，mode/epoch/历史manifest/basis descriptor匹配唯一静态转换；current catalog tuple等该转换目标。sandbox再核§6全部字段/完整V1三方byte相等，ordinary三前驱union显式nil且旧APP两表无行。
8. epoch/attestation逐列沿O1已有关系重核：singleton、schema_version=1、proof_version=2、epochID/mode、actualDB/schema/object owner/APP/projector/可空worker、manifest/descriptor、runtimeProof SHA、actor=`offline-maintenance`、created_at两行相等；ordinary worker/predecessor nil，sandbox对应非nil。timestamp沿现WalletLotsTime有限PG范围及微秒资格，不把它错误缩窄为本机当前年份；历史counts由完整replay核。
9. 所有Rows已完整EOF/Err/Close，adapter无pending/failure；在同tx重新完整catalog+完整current history，摘要/identity与入口一致，再核入口/出口CatalogProof.VerifyTx原wrapper/Conn/PgConn/PID/backend_start/top-xid/actual role/DB/schema。所有CPU/SQL边界和最终发行前的绝对期限/ctx均仍有效，才构造私有RuntimeProof。

当前ProjectEpochProof只能在完整ReadWallet后使用，所以这里明确替代旧§7.1“先核v2再跑三core”的候选顺序；不能为凑旧顺序提前单查epoch、用另一快照或拼三份报告。

RuntimeProof私有state至少封存：deep-owned pins/模式；原入口/最终catalog identity及WalletReadV2；historical/current完整root/manifest映射身份；expected与DB原v2 SHA、sandbox原v1 SHA、epochID/basis/manifest/descriptor；原完整八算法覆盖结论和安全计数。不得保存caller可变pointer为认证真值，不持有secret、raw资金行、维护Permit、外部state或待取消的work ctx。

## 9. 查询adapter、预算与事务寿命

### 9.1 资源预算

| 域 | 固定资源/时间边界 |
| --- | --- |
| composer成功发行窗口 | 从Verify入口到发行累计<=30秒，沿caller更早deadline；不含caller在调用前Begin或返回后Rollback/Commit |
| catalog-entry/exit | 每次沿原catalog独立<=5秒且受同一30秒剩余时间；每次原10万metadata行/64MiB/64KiB单record等预算，不调宽LegacyV1或过滤actual |
| 资金扫描 | 原84表共享10万物理行/16MiB/128行keyset分页；普通text/bytea字段65536，唯固定v2 proof_canonical列262144；所有投影沿原累计界重核，不把每域重新计成10万 |
| history | 当前完整22项的实际有界预检/逐行检查；historical/currentmanifest分别完整绑定；不把map去重代替真实唯一性 |
| 文件 | 原manifest65536、nonce44、keys1..128及全部材料词法界；loader在事务前单独完成，不把secret放查询 |
| VerifyTx | 每次最多5秒且沿caller更早deadline，仅重核原事务身份，不重新给完整scan30秒，也不重新扫描资金 |

两次catalog各自预算与资金16MiB是不同受审域，不能声称整次所有内存/累计transport合计只有16MiB或只10万行；静态expected预加载在进程初始化，投影/算法有其既定逻辑资源界，不冒称严格服务器内存界。

30秒和5秒都同时用context与固定绝对deadline核成功资格；SQL前后、Rows Next/Scan/Err/Close前后、算法/投影/原件比较阶段及发行前检查。即使timer尚未调度、ctx.Err仍nil，只要已到绝对期限也拒绝。不能每一阶段重新给30秒或catalog子阶段重新给5秒。

### 9.2 私有adapter

adapter从经认证原tx构造，不接受caller Query或任意SQL registry。它只供固定ReadWallet调用，沿work ctx对原tx.Query执行生成的显式投影SQL；使用已审builtin类型/casts/operators和pgx.Identifier，不依赖项目自有SQL谓词或函数。

完整保留原scanner需要的Next→Scan→EOF→Err→Close语义、pending rowset唯一性、原错误优先级和owned参数/driver bytes；成功路径必须Next完整EOF→Err→Close并清空pending。提前Close、Next/Scan/Err/Close故障或超界时，先latch失败并取消本次work/query ctx，pending不得伪清空，后续Query拒绝，不额外调用driver.Close或Next去drain未完成结果。scanner提前失败Close保留主ErrResource优先级。pgx自身Next/fatal可能自动Close，不能承诺驱动绝不drain。无Rows/游标可以逃逸；本层失败不Commit/Rollback/Close caller连接，caller必须按§9.3以fresh有界cleanup ctx结束/必要时关闭。EOF/Err/Close或ctx/绝对deadline任一失败只返回nil proof。

### 9.3 Caller-owned Tx 的实际寿命

- composer不Begin、不Commit、不Rollback，不借维护owner/worker连接或maintenance socket watcher。caller负责所有返回后的有界事务结束/连接归还，失败后不得复用failed tx执行资金写。
- `RuntimeProof.VerifyTx`仅核原CatalogProof/具体wrapper、Conn、PgConn、PID、backend_start、top-xid、actual APP LOGIN、端点/DB/schema、RR RO与search_path等身份；不是一次最新catalog/history/资金重扫。不能声称它发现原快照以外后来发生的ACL或业务变化。
- Commit/Rollback、连接关闭、换wrapper/连接/事务/角色/search_path后无效；同wrapper被caller重绑后也拒绝。nil/zero无效。只读副本共享原identity，不产生新的寿命。
- 30秒是本次完整核验的成功发行窗口；5秒是每次窄身份检查窗口。**不把被defer cancel的30秒work ctx存在proof中造成返回即失效，也不把5秒检查解释为跨事务续期。** proof的权威仅覆盖原仍存活RR快照；没有可跨RC写事务沿用的资金许可。
- 本API没有O1“原RO终结、idle ACK、停止watcher、双锁释放/物理socket关闭”的owned保证；不声称超时后物理清理已确认。调用方清理不算composer30秒内，R2须单独冻结顶层startup结束确认、ready/guard/fence和各actual runtime验证。
- 事务结束后只可保留普通安全digest/审计说明；R1-B不把过期proof改造成Lifecycle，不增加在线装配出口或跨worker移交APP token。

## 10. 验收矩阵（冻结后实施，不把此表写成已PASS）

| 层 | 必须的正例 | 必须的拒绝/故障与证据 |
| --- | --- | --- |
| V1纯格式 | 独立硬编码旧canonical golden/SHA；1与128key；相等key时间；Encode/Decode与原payments/maintenance差分 | 全19字段缺/多/重复/转义重名/null/错type，数字词法/范围、UTF8/UTF16、大小/深度、非canonicalDB bytes、nil/zero、alias/JSON伪造、race/fuzz；保留同digest不同ID的纯格式合法例 |
| manifest严格解析 | 原合法非规范空白/字段序及nested proof排版均成功，并得到相同canonical | 完整manifest的重复/未知/缺字段/null/坏escape/数字lexeme失败，防宽松decode后修复；manifest整体65536界独立于nested proof |
| 文件 | 真实0700目录/0600文件/euid、合法nonce可选单LF、完整排序key材料 | 相对/不clean/坏basename、最终父目录或文件symlink、错owner/mode、FIFO/dir/空/超界、SHA错、同名nonce、nonce/key域错、padding/非零base64尾位/多LF、JSON key尾LF、nonce-key/key-key复用、部分key/ID错；任何secret不出错误/日志 |
| 纯投影 | 同原84facts的ordinary两空与sandbox双单例、输出mutate不影响facts，旧ctx取消但新有效ctx可重核 | 旧68 Facts/伪report/未EOF、缺表/错registry、mismatch/unproven、额外单例/空原件、字段/累计界、坏timestamp、取消；不执行额外SQL、不改raw/rejected |
| 历史→current | 上述六条静态映射全部唯一解析；独立重算旧/新每variant schema/roles；同数据不同随机names/OIDs仍需actual pins认证 | 任一合法形状未知root、mode/variant/schema/roles交叉拼接、错profile/major/version、缺/多/乱序/错SHA历史manifest、current仅历史前缀、错误basis descriptor；原v2完全不改；旧不完整sandbox候选root拒绝 |
| actual APP身份 | 两轮随机names/OIDs，真实APP LOGIN RR RO；ordinary named及public、full genuine sandbox均有完整非空runtime成功 | admin/owner/worker/projector、SET ROLE、session!=config.User、RR RW/RC、错search_path/endpoint/DB/schema/role OID、换wrapper/Conn/后续事务；明确composer没有使用owner/worker连接 |
| 完整history/catalog | R1-A WalletReadV2实际catalog及完整current history入口/出口一致；保留旧API拒delta | 任一额外ACL/列撤权、table级宽权、grant option/PUBLIC/member/peer内容权、schema替代/RLS/SQL函数篡改、历史行缺多损坏、入口后漂移导致失败；不调旧上限 |
| 非空资金图 | 专库真实初始化epoch后增加经营数据，至少覆盖人工/券、key预占与settle/退款、采购证据、PB，以及sandbox capture。当前109basis与历史不同仍可通过完整重放；新tenant/多次退款正例 | 每个八算法域至少一个语义矛盾/缺证，跨tenant/错FK/FIFO/event/cache、unknown来源、支付stored-verified图不闭合；零mismatch但unproven仍拒绝；no epoch/空库不是完整成功 |
| v1文件/DB/v2 | genuine sandbox三schema/八真实role/原私有文件，三方原v1 bytes完全一致、APP nonce/key持有成功 | 逐marker/attestation字段篡改、valid格式但不同环境/merchant/nonce/keyset/origins原件、v2嵌入另一个合法v1、wrongSHA/bytes；纯CanonicalV1/V2不能独自成功 |
| 预算/EOF | 真实分页跨128边界、完整84EOF/Err/Close，原表列清单一致性继续通过 | Next/Scan/EOF/Err/Close各故障、pending未清、driver buffer alias、行/字节/字段界、SQL拖延、CPU边界期限、ctx.Err=nil而绝对期限已到、entry/exit最后一刻超时；全部nil proof |
| 寿命 | 原tx仍活跃时proof窄VerifyTx成功、只读copy保持原identity | 结束/关闭/换tx/重绑wrapper、当次composer/VerifyTx取消或短deadline后拒绝；不能存work ctx返回即失效；检查SQL证明没有重新扫84表或任何业务写 |
| 依赖/无接线 | 新leaf/composer生产依赖闭合；旧offline/online gate、v1/codec/O1/Installation回归保持 | main/server/billing/PB不新增catalog/maintenance间接依赖；无CLI/迁移/GRANT/HTTP/model/PSP/ready/在线funding writer变化 |

专库测试由根独占串行，明确记录实际env/PG版本/source SHA/PASS-SKIP-FAIL/cleanup。准备fixture时可由独立离线维护测试helper按既有合同初始化，但**composer核验阶段必须切换到actual APP，不能拿准备数据用owner的成功代替APP验收**。genuine sandbox需要既有真实v1运行材料，不能只插一行自造canonical冒充。

至少包括纯层race、完整目标包race/相关vet/fuzz、专用PG非空链路，最终架构语义复核/manifest刷新和只读检查由根在汇总后执行。只跑fake Query或静态golden不得标完整RuntimeProof已完成；本地genuine sandbox不等于真实PSP、官方供应商、生产1Panel或整体上线验收。

## 11. 根冻结裁决、分工及差分验收

1. 独立`fundinglotruntime`替代旧§7.1 billing候选；本片只交完整APP库，不减弱online dependency gate，不接main/HTTP/billing/PB/worker/projector/ready，也不新增迁移、GRANT或维护CLI。
2. 授权§3五个V1纯格式业务API，字段/顺序/tag/标量与原19字段完全相同，纯包继续stdlib-only；旧V2构造/拒绝语义不改。保留原两份1786字节golden及SHA`1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c`。
3. 固定§5同84raw投影、全部qualification/deepcopy；ordinary两nil，sandbox双单例，不查询SQL。根审readset源pin精确增量。
4. 固定§7六条完整历史/current映射及完整22项2459字节manifest。未来migration或catalog变化须另审静态映射；原proof不重签。固定artifact及独立Python逐variant复算先于认可实现结果。
5. expected V2仅比较值；本片唯一私有文件认证是旧APP manifest。当前Plan/Init只公开digest，R2须另冻完整V2发行、持有、加载和startup结束确认；测试可在独立owner准备阶段读取保存原件并与Plan.Expected digest核对，必须明确这是fixture expected，不是生产DB自采纳。
6. 固定30秒发行、catalog/Verify各5秒、loader5秒及caller-owned清理。已发行proof不保存被cancel的work ctx；后续VerifyTx使用当次ctx与原活跃RR身份，发行ctx事后取消不新增永久撤销语义。只承诺adapter不额外主动drain，不把pgx机械行为或调用defer Rollback当server ACK。
7. 固定完整非空actual APP与genuine sandbox、全部八域正负例。测试复用原维护同包fixture准备；组合核验阶段必须新建actual APP LOGIN RR RO，记录实际用户/原wrapper/连接、完整history和图，而非owner成功。
8. 新loader允许临时调用纯`paymentproto.NewKeyring`复核旧资格，不保留或返回keyring/秘密，不执行支付或网络；旧payments loader不修改。文件/语法错误全固定ErrRuntime。
9. 旧codec差分放payments/maintenancetrust各自同包新增`_test.go`，直接调用原私有strict→validate→canonical→bytes.Equal oracle，不增加生产getter，不修改原文件源pins。Decode不能只比较strict成功；Encode先旧语义验证再比较bytes/SHA。QA writer只作正例，不作拒绝oracle。非法raw原字节直接送两parser，不能先Unmarshal修复。覆盖pg_角色、跨类别OID相同、相等key时间和不同ID同digest的合法边界，外加完整拒绝矩阵。
10. 根负责composer、历史/current静态材料、固定history guard、adapter与PG集成；可并行实施V1叶子、同raw投影、APP文件loader。并行源码未汇合前不运行会互相影响的整仓/真实PG；根汇合后独占专库验收、复核架构与manifest、make check/verify及GitHub推送/精确CI。

## 12. 固定history guard与失败清理细则（实现前冻结）

current manifest恰22项、2459字节、最大name35B、固定SHA见§7。静态构建必须重核完整每项及SHA，不能随latest自动扩宽界。guard仅替换由pgx.Identifier生成的限定关系名：

```sql
SELECT pg_catalog.count(*) OPERATOR(pg_catalog.=) 22::pg_catalog.int8
 AND COALESCE(pg_catalog.bool_and(
  name IS NOT NULL AND checksum IS NOT NULL
  AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.>=) 1::pg_catalog.int4
  AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) 35::pg_catalog.int4
  AND pg_catalog.octet_length(checksum) OPERATOR(pg_catalog.=) 64::pg_catalog.int4
 ), false)
 AND pg_catalog.count(DISTINCT CASE
  WHEN pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) 35::pg_catalog.int4
  THEN name COLLATE pg_catalog."C" ELSE NULL END)
  OPERATOR(pg_catalog.=) 22::pg_catalog.int8
 FROM (SELECT name, checksum FROM ONLY {{qualified_history}} LIMIT 23) AS guarded_history
```

只传回一个bool；23上界使额外任意行明确失败，不能LIMIT22后冒充完整。name超界不进入DISTINCT字符串比较；普通重复/NULL/超界/少多行拒绝，成功再同RR调用原完整helper。真实history的PK/type/owner/无RLS/无替代物先由catalog认证；没有动态SQL、预期值回填或提前运送不受界text。SQL/CPU沿原30秒，Rows全EOF/Err/Close；不声称服务器所有内存或CPU有逻辑16MiB硬界。

失败清理：composer取消自己work ctx、latch失败/pending，绝不再发后续扫描/出口认证SQL，不额外drain、不擅自结束caller事务。caller必须以新的有界cleanup ctx执行Rollback；失败/busy或不能确认I与!IsBusy时关闭连接，并在顶层/池层确认不归还busy连接。已closed连接的缓存TxStatus可仍为T，以IsClosed/实际backend结束判断，不把缓存T冒称仍可复用事务。R1-B不拥有维护socket watcher，故发行/返回耗时与caller清理耗时分开记录。

根已在隔离管理LOGIN探针看到完整EOF→Rollback/I与未读Rows→Rollback错误/连接关闭/原PID消失；此研究不作为actual APP或adapter验收。实施后须在actual APP真实多行未读、资源错误、SQL锁等待取消、断backend、EOF/Err/Close故障、绝对期限及fresh清理上重新验证，单元fake不代替真实PG。

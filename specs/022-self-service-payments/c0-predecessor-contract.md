# 022-C0-T2：同 RR 的 v1 前驱及 ordinary-off reader（T2根冻结实施）

2026-11-02。**本稿逐字段矩阵由[真实授权合同](c0-authority-contract.md)根冻结并已实现，[T2真实PG/race专项](../../docs/verification/maintenance-authority.md)已通过。前驱自身不发行 Permit/epoch/RuntimeProof。** 对应[维护合同 §6](c0-permit-contract.md#6-安装证明v1前驱与完整runtime-proof的分层)。T0 Gate与T1私有Snapshot已实施并保留原历史验收范围。T2仅增加受审叶子依赖，原顶层RR、busy/deadline与实际身份仍是前置，不以reader代替这些依赖。

## 1. 来源、证据种类与不可扩张边界

逐字段依据：

- [App v1 类型及字段顺序](../../internal/payments/runtime_types.go)、[原校验/canonical](../../internal/payments/runtime_load.go)、[原 strict JSON](../../internal/payments/runtime_codec.go)、[原 catalog/attestation/off](../../internal/payments/runtime_catalog.go)。
- [021 marker/policy](../../migrations/021_payment_sandbox.sql)、[022 immutable attestation](../../migrations/022_payment_runtime_attestation.sql)。
- [M/Q 类型](../../internal/paymentruntime/types.go)、[文件及 pin 校验](../../internal/paymentruntime/load.go)、[持久 state 校验](../../internal/paymentruntime/state.go)、[catalog](../../internal/paymentruntime/catalog.go)。
- [编译期 peer SQL/版本/摘要](../../internal/paymentsandboxschema/schema.go)、[Mock 原 SQL](../../internal/paymentsandboxschema/mock_v1.sql)、[Receiver 原 SQL](../../internal/paymentsandboxschema/receiver_v1.sql)。
- [独立离线 App canonical 写入器](../../internal/paymentsandboxqa/application_wire.go)、[App 持久安装](../../internal/paymentsandboxqa/application.go)、[peer 持久安装](../../internal/paymentsandboxqa/install.go)。QA 是交叉参照，不能凭其样例代替生产 v1 校验或生成 live expected。

本片证明两种互斥事实：

| 路径 | 输出的私有证据 | 不证明 |
| --- | --- | --- |
| ordinary | 同 snapshot 的原 ordinary-off 完整谓词成立，v1 predecessor digest 为空字符串 | sandbox 从未配置过、历史任意表全空、运行许可 |
| sandbox | App 021/022 的原 v1 canonical、对应持久列、两 peer 已存在共享字段和各自 marker/key/migration 的明示关系成立 | 原 nonce/token/key 文件仍在、材料真实原件、M/Q 的 active key、PSP 服务已退出、完整资金正确性 |

mode 来自已验证 InstallationProof 与 Gate Pins 的完整匹配，caller 不能传字符串选择宽松路径；sandbox 任一前驱缺失都失败，不降级 ordinary。ordinary 任一关闭条件不成立都失败，不以“没有 marker”短路。不重写 App v1 字段，不给 v1 增加 peer 原来没有的属性，不更改已应用 SQL/ACL/provision。static InstallationProof 承担全部受审对象正文/关系/角色/ACL、heap/类型/无 RLS/无未审替换；本 reader 承担持久业务标记和前驱值，二者不能互相替代。

## 2. 同事务私有 API、依赖与状态

在 `internal/maintenancetrust/predecessor*.go` 实现，**只允许包内调用**：

```go
// authorityStage由真实Attest私有发行，不接受外部tx/proof。
type predecessorEvidence struct { /* private immutable identity and values */ }
func readPredecessorAuthority(stage *authorityStage) (*predecessorEvidence, error)
func (p *predecessorEvidence) verifyAuthority(stage *authorityStage) error
```

stage 是本 Gate/Snapshot 内部发行、覆盖 reader **全程**的 bootstrap stage，包含不可替换的顶层 tx、受限 context、nonce/purpose、busy 所有权和 Snapshot 绝对 deadline。它由 Attest 内部在真实维护身份、双 gate、完整 catalog/history 已认证后开启；不能先要求 Permit 再发行前驱而产生循环。没有公开 `Read(tx,pins,digest,bool)`、裸 ctx/tx 替换器、`FromJSON`、`Trusted`、`AcceptExisting` 或回调 SQL。stage 的 Go 类型/进入退出和故障撤销以T2根冻结为准，不添加stub。

reader 进入及交出结果前重验 stage 身份、相同顶层 wrapper/*pgx.Conn/PgConn/PID/backend_start/top xid、RR access/purpose/原 deadline、双 session 锁，调用原 `installed.VerifyTx` 并以 `installed.Catalog().MatchesPins(snapshot.copiedPins)` 比较完整目标。`VerifyTx` 不在 init 写入后重新要求 16 表为空；reader 只在 Attest 的唯一 bootstrap 阶段发行，不能在 init 后重发。

私有 evidence 保存原 stage/Snapshot nonce、同 tx/conn 绑定、mode、安装版本与 expected/schema/roles 摘要、固定 manifest bytes/digest、完整深拷贝的规范前驱值。sandbox 的 `v1SHA256` **恰为原 `proof_sha256`**；ordinary 为 `""`，不能用 64 个零、空 JSON 或自己拼的 peer 摘要冒充 v1。peer 未进入 v1 的值单独保存在私有完整 rows snapshot，不改 v1 canonical/摘要。后续 v2 若需要把它们纳入外部承诺，须在 v2 canonical 精确合同另列独立字段，不能悄悄替换 predecessor digest。

`verifyAuthority` 只复核不可变私有归属与完整内容；实际活动状态/SQL绑定由根 authorityStage.verify 与 Permit.Verify 负责，不能将字符串摘要升级为授权；关闭、Commit/Rollback 开始、阶段失败、context 到期或换 tx 后永久失效。复制对象共用状态；输出无导出字段，String/GoString/JSON 固定 redacted。reader 无 BEGIN/COMMIT/ROLLBACK、SET、SAVEPOINT、DDL、行锁、pool、文件或网络动作。

### 2.1 T2根冻结允许的叶子依赖

T0 只依赖 std/pgx/fundinglotcatalog.Pins。T2 根冻结明确新增两个无运行网络/资金回调的叶子依赖：

1. `migrations`：在同 stage/同 tx 调用 `VerifySnapshotTx`；另从 `migrations.Files` 编译期 SQL 形成已有 [schema-check §9 manifest 规范](c0-schema-check-contract.md#9-根冻结窄片metadata-only-离线-descriptor-生成器) 的 `(name,sha256)` 数组。按文件名 ASCII/C 序、键序 name/sha256、UTF-8 无空白/换行/BOM；完整最新集合与同 RR 实际 history 精确相等才私有绑定。不能用 caller digest、CatalogProof 摘要或仅“helper 返回 nil”代替固定 manifest 来源。
2. `paymentsandboxschema`：只用编译期 `Version`/`Digest`（见 §6）；不执行 `SQL`，不从 actual marker 反推 expected。

**禁止 import `payments`、`paymentruntime`、`billing`、`operations`、`paymentsandboxqa` 或 adapter。** 特别不调用 `newPaymentRuntime`、`paymentruntime.Open/attestState/attestCatalog`：这些入口会另 Begin/Commit、依赖 runtime 登录/私有文件或取得 ownership，不能作为 caller-owned RR 维护证明。原 codec 在本包用私有封闭类型独立实现，靠固定向量和旧实现交叉验证，不做未经另审的共享纯 codec 重构；避免 `billing → maintenancetrust → payments → billing` 循环。

## 3. App v1 canonical：全部字段及精确格式

原 top-level 字段顺序固定为下表顺序，嵌套顺序也固定；JSON 所有字段必填、不允许 null/未知/重复或大小写替代。所有整数是 JSON 数字，非字符串；不接受负号、小数、指数、前导零或越界。登录字段为 JSON bool。保持 [原 strict codec](../../internal/payments/runtime_codec.go) 的 UTF-8、合法转义/配对 surrogate、depth≤12、单 JSON、≤65536 bytes 行为。

原 canonical 使用 `json.Encoder.SetEscapeHTML(false)`，对封闭 struct 编码后**只去掉 Encode 产生的最后一个 LF**；无 BOM/空格/尾换行。接收顺序/转义差异即使能解码，在重编码 bytes 比较时也拒绝：`SHA256(proof_canonical)==proof_sha256` **且** `privateCanonical(decoded)==proof_canonical`。不能先删未知字段、换序、把 jsonb 的等值当作 bytea 原件。SHA 是原 bytes 的普通 SHA-256 小写 hex，无新域前缀。

| # | v1 字段/嵌套顺序 | 约束与比对来源 |
| --- | --- | --- |
| 1 | proof_version | JSON int，恰 1；等于 App attestation.proof_version；不是 peer schema version 字段 |
| 2 | protocol_version | JSON int，恰 1；等于 App marker、M/Q marker 的 protocol_version |
| 3 | environment_id | canonical 非零小写 UUID；等于 App marker.id、attestation.environment_id、M/Q environment_id |
| 4 | merchant_id | 原 runtimeRef：UTF-8 1..100 bytes、无控制/://、拒 sk-/mg_/bearer 前缀，仅 Unicode 字母/数字或 `:_./-`；等于 App/M/Q merchant_id |
| 5 | payee_label | 原 runtimeLabel：UTF-8 1..100 bytes、无控制/://；等于 App marker 与 Mock marker；Receiver **无此列** |
| 6 | database: host,port,name,oid | host/port/name 恰 127.0.0.1/55439/moni_test；非零 uint32 OID；完整等于 opaque pins/实际 DB，所有三 marker 的 database_oid；peer marker无host/port/name，不假称读到这些列 |
| 7 | app: name,oid,owner,runtime | schema 前缀 payment_sandbox_ +32小写hex；其余见下段；等于 pins/实际 catalog 与 App marker schema_oid、Mock 对应 app_* 列 |
| 8 | mock: name,oid,owner,runtime | schema 前缀 payment_mock_ +相同suffix；等于 pins/catalog、Mock marker mock_* 列 |
| 9 | receiver: name,oid,owner,runtime | schema 前缀 payment_receiver_ +相同suffix；等于 pins/catalog、Mock receiver_*、Receiver receiver_* 列 |
| 10 | worker: name,oid,login | App worker，LOGIN=true；完整等于 pins/实际角色、App marker.worker_role 与 attestation.worker_role_oid；M/Q marker **没有 worker 列** |
| 11 | projector: name,oid,login | LOGIN=true；完整等于 pins/实际角色、App marker.projector_role、attestation.projector_role_oid、Mock app_projector；Receiver 无 projector 列 |
| 12 | app_nonce_digest | 64小写hex；等于 App marker.nonce_digest 与 attestation.app_nonce_digest；不等于 Mock/Receiver 自己的 nonce 字段 |
| 13 | worker_nonce_digest | 64小写hex；等于 attestation.worker_nonce_digest；没有 M/Q 列可绑定 |
| 14 | api_token_digest | 64小写hex；仅等于 Mock marker.api_token_digest；不是 control_token_digest |
| 15 | api_origin | 原 runtimeOrigin(callback=false)，无path（`/`也不接受）、query/fragment/userinfo/opaque/rawpath，字面IPv4与规范非零十进制port；等于 Mock api_origin |
| 16 | control_origin | 同上；等于 Mock control_origin；与 api_origin、app_listen_origin 两两不同 |
| 17 | app_listen_origin | 同上；只由 callback_url 的origin关系绑定；Receiver marker **没有 listen_origin 列** |
| 18 | callback_url | 原origin规则但path恰 `/api/payments/sandbox/notifications`，其origin=app_listen_origin；逐字等于 Mock/Receiver marker.callback_url |
| 19 | keys: id,digest,not_before,not_after | 1..128项，ID按原Go字节顺序严格递增，不先排序修复；ID正则 `[A-Za-z0-9_-]{1,32}`、digest64小写hex；时间固定 `YYYY-MM-DDTHH:MM:SS.ffffffZ`、非zero、year1..9999且before≤after；等于M/Q全部key行（见§5） |

每个 schema 的 `owner` 和 `runtime` 均按 **name,oid,login** 编码；schema 的 owner.login=false、runtime.login=true。Worker/Projector 同样是 role 对象。八个角色名称正则 `^[a-z][a-z0-9_]{1,62}$`，名称及非零 OID 各自两两 distinct；三个 schema 的非零 OID distinct、32hex suffix 相同。完整 role name/OID/login 对比由 opaque pins及实际 catalog 身份共同证明，不能只把自报 Login bool 当作实际 rolcanlogin。无inherit/super/createdb/createrole/replication/bypass/成员/授权提升及各runtime全局不own对象仍由完整静态checker和维护身份合同负责，不因reader已解码而跳过。

历史时间仅作原有效区间格式/排序比较，reader **不选择当前 active key，不要求某个推测的 active key 当前有效**。原 App loader 用 keys[0] 构造自己的 Keyring 不能推出 peer manifest.active_key_id；维护历史核验也不调用 VerifyNotification/Sign，不验证秘密材料。

## 4. App 两张单例表：完整持久列

先由同 RR static proof 确认关系为受审 ordinary heap、持久、无 RLS/force-RLS、目标 owner/完整列与约束，不接受 view/foreign/partition/同名换 OID。每张表用明确列清单一次查询、读取最多两行并完整关闭 Rows；恰一行，不用 WHERE singleton 隐藏坏行，也不以 LIMIT 1 接受多行。按 scalar 字节界在解码前限 `proof_canonical` 65536 bytes，其余文本各自明示上界。以下全部列均读取和验证。

### 4.1 payment_sandbox_environment（12列）

| 列 | 规则 |
| --- | --- |
| singleton | true |
| id / merchant_id / payee_label | 分别等于 v1 environment_id / merchant_id / payee_label |
| database_oid / schema_oid | 分别等于 v1 database.oid / app.oid，并与实际目标一致 |
| nonce_digest | 等于 v1 app_nonce_digest |
| app_role / worker_role / projector_role | name列转text后分别等于 v1 app.runtime.name / worker.name / projector.name；其OID通过实际catalog验证 |
| protocol_version | 1且等于v1 |
| created_at | 明确读取：finite、非zero、UTC year1..9999；仅为持久时刻，不编造与peer/attestation的相等或先后关系 |

### 4.2 payment_runtime_attestations（14列）

| 列 | 规则 |
| --- | --- |
| singleton | true；原FK到App marker.singleton且same environment关系由static descriptor证明 |
| environment_id | 等于App marker.id/v1 environment_id |
| database_oid / schema_oid / schema_owner_oid | 等于v1 database.oid/app.oid/app.owner.oid及实际catalog |
| app_role_oid / worker_role_oid / projector_role_oid | 等于v1 app.runtime.oid/worker.oid/projector.oid |
| app_nonce_digest / worker_nonce_digest | 对应v1同名字段，前者同时等于App marker.nonce_digest |
| proof_version | 1且等于v1 |
| proof_canonical | 1..65536 bytes的原bytea；strict decode、原校验、原canonical重编码同bytes |
| proof_sha256 | 恰64小写hex且等于原canonical的SHA；作为sandbox predecessor digest保存 |
| created_at | finite、非zero、UTC year1..9999；不接受infinity，也不把当前clock与历史created_at比较 |

对各created/applied时刻的完整读取及finite/year检查已按T2根冻结实现，已安装异常历史在此维护reader中失败关闭。原App runtime只读取attestation.created_at，peer旧state不读取所有这些时刻；本次不把新增检查追溯为旧代码已核过的边界，也不修改旧v1启动行为。

## 5. Peer 两套 marker/key：逐列分类，不假造 v1 字段

两套 schema 名/OID/NOLOGIN owner及实际 runtime LOGIN 先从 v1+opaque pins+catalog 三向一致确认，再引用静态表名；所有标识符使用 pgx.Identifier 全限定。marker各恰一行、全部列必读，不使用 to_jsonb 隐式 OID字符串转换作为最终 typed合同。M/Q的业务event/body/receipt表不在reader读取范围，完整来源扫描另属T3。

### 5.1 mock_environment（27列）

| 列 | 来源及精确规则 |
| --- | --- |
| singleton | true |
| environment_id / merchant_id / payee_label | 分别等于v1及App marker相同字段 |
| database_oid | v1 database.oid/实际目标 |
| app_schema_name / app_schema_oid / app_owner / app_runtime | v1 app.name/oid/owner.name/runtime.name；owner/runtime名字再绑定已认证实际OID |
| app_projector | v1 projector.name，并绑定其实际OID |
| mock_schema_name / mock_schema_oid / mock_owner / mock_runtime | v1 mock对应字段及实际catalog |
| receiver_schema_name / receiver_schema_oid / receiver_owner / receiver_runtime | v1 receiver对应字段及实际catalog |
| api_token_digest | v1 api_token_digest |
| api_origin / control_origin / callback_url | 与v1同名字段逐字相等；不能用只去尾斜线的等价归一化 |
| protocol_version | 1且等于v1 |
| state_directory_id | **v1无该字段**：只核canonical非零UUID，并分别私有保存此持久值；不声称已读manifest/目录文件 |
| nonce_digest | **v1无Mock nonce**：只核64小写hex并私有保存；原域为SHA256(`moni-mock-state-v1`+NUL+nonce)，不读原nonce故不声称重算成功 |
| control_token_digest | **v1无此字段**：64小写hex、且与本row api_token_digest不同（原SQL CHECK）；原域为`moni-mock-control-token-v1`+NUL+token；不验证token材料 |
| created_at | finite/非zero/year1..9999；无凭空构造的跨row时间顺序 |

### 5.2 receiver_environment（13列）

| 列 | 来源及精确规则 |
| --- | --- |
| singleton | true |
| environment_id / merchant_id / database_oid | v1/App/Mock相应字段及实际目标 |
| receiver_schema_name / receiver_schema_oid / receiver_owner / receiver_runtime | v1 receiver对应字段及实际catalog |
| callback_url / protocol_version | v1对应字段；protocol=1 |
| state_directory_id | **v1无此字段**：canonical非零UUID，独立保存Receiver持久值 |
| nonce_digest | **v1无Receiver nonce**：64小写hex，独立保存；原域为`moni-receiver-state-v1`+NUL+nonce，不读原件 |
| created_at | finite/非zero/year1..9999 |

M/Q原SQL均没有将两张peer marker互作FK。QA当前生成器给两者同一个 state_directory_id，原v1/组件runtime没有规定两值必须相等；因此本候选**不凭QA样例新增M.stateDir=Q.stateDir或nonce相等/不等的认证关系**。若根需要共享目录身份，须另行冻结新安装/持久绑定与历史兼容策略。不同domain摘要不能证明原nonce/token两两不同，更不能把App/Worker nonce摘要当成peer自己的nonce摘要。

### 5.3 mock_signing_keys 与 receiver_signing_keys

| peer表列 | 规则 |
| --- | --- |
| 两表 key_id / key_digest / not_before / not_after | 独立完整读所有行，以 `key_id COLLATE "C"` 顺序；分别与v1 keys整数组同一ID/digest/两个时间逐项相等，不能只有集合包含或同条数；1..128、无额外/缺失、ID和digest唯一 |
| mock_signing_keys.created_at | 仅Mock表有此列：finite/非zero/year1..9999，读入私有rows snapshot；不与v1 key区间混同 |
| receiver_signing_keys.created_at | **此列不存在，禁止SELECT或伪造默认时刻** |

SQL timestamptz先typed读取并拒绝非finite/越界，再转UTC六位微秒与v1逐字比较；不得用session timezone文本或秒级比较丢微秒。有效区间是历史材料声明，当前全过期仍可证明历史前驱，不因此授签名/网络能力。signing key摘要原域两peer相同：SHA256(`moni-mock-signing-key-v1`+NUL+32byte材料)。reader只比摘要，不读取原key，不能声称HMAC认证或证明材料不重复。

### 5.4 根本没有持久字段的配置

| 配置/属性 | 本reader行为 |
| --- | --- |
| active_key_id | App v1、M/Q marker和key表均未持久此字段；不推断第一key/最新key、不构造active ID、不验证其当前窗口 |
| own_schema_sql | App v1没有；peer持久version/SHA只能与编译期固定SQL对照，见§6 |
| manifest_version/component | App v1 canonical不是manifest，不含这两项；不从持久rows伪称已验证组件文件形状 |
| runtime_dsn_file/nonce_file/API或control token file、material_base64_url | 完全不读，不把摘要相等写成原件已验；后续实际runtime装配仍须原0600文件/0700目录/secret SHA与材料检验 |
| peer payee/listen/projector/worker的缺列 | Receiver没有payee_label/listen_origin/projector/worker；Mock没有worker。只能用表中真实存在的共享字段，不增加SQL或虚拟列补齐 |

## 6. 编译期 peer 迁移与 SQL 摘要

`mock_schema_migrations`、`receiver_schema_migrations` 各全部读取 `(version,sha256,applied_at)`，恰一行、version=1、SHA64小写hex、applied_at finite/非zero/year1..9999。期待值来自**本进程编译期叶子包** `paymentsandboxschema.Version/ Digest`，不是peer marker、App proof或本地文件路径。

本次只读核对的精确原SQL摘要（含文件注释/空白/末尾换行，未做归一化）：

| 编译期文件 | version | SHA-256 |
| --- | --- | --- |
| `internal/paymentsandboxschema/mock_v1.sql` | 1 | `3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127` |
| `internal/paymentsandboxschema/receiver_v1.sql` | 1 | `3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e` |

根冻结/编码时同时复核 leaf Version/Digest、静态 Inventory.SQLSHA256和已受审checker root来源一致；任何源码SQL/version/静态root变化先重审，不能只改marker hash让本次通过。C0只扩App对象，不给M/Q原SQL加“维护兼容列”，不安装迁移，不调用会自动Begin/Commit的peer runtime证明。

## 7. ordinary：原关闭合同的完整复制

ordinary 从原 `runtimeVerifyOrdinaryOff` 语义独立实现，只在已认证目标schema和同RR读。不能要求整个商业库空，也不能把无支付marker当作完整off。各表先由opaque catalog确认受审真实heap/持久/无RLS等，再执行固定有界EXISTS；所有表必须存在，无权限/缺表/timeout不是“空”。

### 7.1 原21张history表全部为空

以下集合是原 `runtimeHistoryTables` 的**全部21项**，不是21张里任选：

| 1–7 | 8–14 | 15–21 |
| --- | --- | --- |
| payment_sandbox_environment | payment_command_outbox | payment_provider_revisions |
| payment_runtime_attestations | payment_command_observations | payment_transaction_identities |
| payment_skus | payment_query_attempts | payment_order_capture_selections |
| payment_sku_versions | payment_inbox | payment_captures |
| payment_sku_changes | payment_event_conflicts | payment_effects |
| payment_orders | payment_verified_queries | payment_review_facts |
| payment_cancel_intents | payment_verified_evidence | payment_evidence_processing |

### 7.2 payment_query_conflicts的原独立结构推导

它不是上表第22个直接SELECT。原APP没有此证据表SELECT，不改APP权限：同RR已证明 `payment_verified_queries` 全空，再核实际 mandatory FK：

- child恰为目标schema的 `payment_query_conflicts`；parent恰为同schema的 `payment_verified_queries`。
- child列 `verified_query_id` NOT NULL，parent列 `id`；两列必须是实际未drop、attnum>0的受审列。
- 实际FK contype=f、convalidated=true、condeferrable=false、condeferred=false；conrelid/confrelid是该child/parent OID，conkey/confkey恰是上述单列attnum数组。完整静态descriptor另证FK/PK和全部关系，无额外incoming/row-hiding。

上述关系+空parent使child空；不是只看FK名字或任意parent空就推导。本维护LOGIN即使可SELECT child，也保留该原结构证明，不以更宽维护权跳过；不需要给原APP增SELECT。测试应独立证明结构负例失败，不能仅让static checker先失败而把reader分支空转。

### 7.3 payment_policy唯一初始关闭行

固定读取全部三列 singleton/enabled/revision，恰一行、singleton=true、enabled=false、revision=1。不能接受“已启用再关回false但revision>1”，不能只 `WHERE NOT enabled` 过滤。实际表必须是受审heap；缺行、多行、NULL、异常版本均拒绝。

### 7.4 六类支付旁路事实不存在

对每张实际受审表分别按原谓词执行EXISTS，任一匹配即拒绝。谓词不扩大到其他商业动作：

| 表 | 原精确排除条件 |
| --- | --- |
| commerce_commands | `left(kind,8)='payment.'` |
| commerce_operation_receipts | `left(kind,8)='payment.'` |
| audit_events | `left(action,8)='payment.'` |
| funding_receipts | `source_kind='sandbox_capture'` |
| journals | `kind='sandbox_capture'` |
| ledger_accounts | `kind='SANDBOX_FUNDING_CLEARING'` |

新reader SQL对内建函数/operator/type显式pg_catalog绑定，列名与谓词仍保持上述意义；不将 SQL LIKE/大小写转换或客户端过滤作为等价捷径。非支付 voucher/人工入账、正常消费/退款、已有Key/采购/PB历史允许存在，后续资金scanner另核完整性。ordinary没有peer schema/marker/key读取，没有v1 canonical，内部v1SHA256必须空字符串。

## 8. SQL、资源、错误与失败撤销

reader已按根冻结实现累计最多5秒的subcall预算，且不超过Snapshot剩余deadline；由原stageContext一次收紧，**不按query/表/分支重置**，不沿用T0已结束Acquire的子context。CPU与SQL阶段均沿原watcher执行raw-close；成功路径先stop/ack，再cancel子context。该预算不放宽旧runtime startup的5秒，也不延长Snapshot总期限。

- 所有SQL只读、固定SELECT，关系名只来自已核pins/编译期静态表名，经Identifier全限定。不调用App/peer自定义校验函数，不经views、不执行event canonical SQL，不读支付原始消息/收据正文。
- 在任何可能很大的 TEXT/bytea 返回客户端之前，由固定 SQL 以 `pg_catalog.octet_length` 检查全部投影标量，返回明确资格位；不合格分支使用 `CASE` 返回 NULL/固定短值，整个 reader 随即失败。不能先 SELECT 完整字段再 Scan/len，也不能只靠 WHERE 丢掉不合格行而伪装缺行或 EOF。此要求包括 proof_canonical 及 peer 的 schema/role/origin/callback 等原 SQL 未设长度 CHECK 的字段；各 scalar 与总量预算约束传输/解码后的安全投影，而非声称限制数据库已存储行的物理大小。
- 规范proof最大65536 bytes；keys每peer最多128且以第129行作为越界拒绝，单例表读最多2行以识别多行，单行所有标量先按封闭类型/上界验（名字≤63、UUID36、hash64、keyID32、label/ref≤100、既定loopback URL≤128 bytes）。schema/version/布尔/OID不接收NULL。Created/applied和key区间typed读取，拒无穷/非finite/zero/year越界。
- sandbox最多6张单例/单迁移行+256条key行=262业务记录；ordinary为21个空集判断、一个结构证明、一行policy、6个旁路判断。已实现整个reader累计安全投影字段上界1MiB，按实际返回字段字节及primitive固定宽度计入，包含哨兵已返回字段；该计数不表示数据库物理存储大小。所有Rows逐项检查Rows.Err并关闭；超界不输出部分证据。完整catalog/history开销由各自阶段限额承担，不将它们的计数偷算成此处262业务记录。
- 使用 `maintenance_input / maintenance_gate_busy / maintenance_identity / maintenance_schema / maintenance_transaction / maintenance_timeout / maintenance_execution` 固定安全错误，分别归属输入、busy、认证/前驱值不符、static/schema失败、tx绑定、期限和执行/资源失败；不返回driver/SQLSTATE/DSN/role/schema/字段值或proof原文，不unwrap。
- 任何错误返回nil evidence，撤销整个bootstrap/后续Permit发行；不自动retry、不切换普通/沙箱路径、不另开pool补读。按Snapshot已冻结的失败清理处理，reader自己不接管事务；本片没有COMMIT，不产生maintenance_commit_unknown。

## 9. Golden、同RR与真实PG验收合同

### 9.1 纯canonical与字段矩阵

新增**固定非秘密**v1 golden，UUID/OID/role/schema/loopback端口固定，含两key、Unicode label、HTML字符及UTC六位微秒边界。由独立离线编码器产生规范bytes/SHA，与原payments canonical、已有独立QA writer及新私有codec三向比较；fixture不含真实DSN/nonce/token/key材料。HTML/Unicode codec向量只验证原v1的编码与runtimeLabel语义，不自动声称满足App marker原payment_ref等SQL资格；真实PG正例另用原SQL允许的值。原纯v1实现不改源码；可新增其package内仅测试桥导出golden至临时目录，但不增加生产codec导出API。

逐项负例：19个top-level/每个nested字段缺失/未知/重复/NULL/大小写异体、整数错type/指数/负零/越界、role/schema同名换OID/不同suffix、错误login、原origin尾`/`/port前导零/alias/query/fragment、callback不同origin、key乱序/重复/额外/缺少/digest或微秒变化、非法UTF-8/未配对surrogate/尾JSON/尾LF、raw有正确SHA但非原canonical。未知peer nonce/stateDir/control/active/ownSQL字段塞入App v1必须拒绝，不能解码后忽略。

正例明确区分：合法历史key现在过期仍可作为前驱；M/Q各自state_directory_id为不同合法UUID、各自nonce为不同合法摘要仍不应凭空要求与App v1相等；这些只证明持久值，未来实际组件Load是否可开服务另验。

### 9.2 完整sandbox真实PG

使用独立专用变量与实际本机moni_test测试库，精确创建/清理随机三schema、八角色和本次连接；不触58123/public ACL/其他会话。按原生产001–022/provision和原M/Q SQL建立**真实非空**业务、App v1 row及candidate C0安装，完成静态proof和真实维护LOGIN/受控stage后调用reader；完整SQL/ACL/v1源pins逐项核相同。

必须逐列变更矩阵中的每个共享字段并拒绝；独立改变peer本地字段的格式/内部约束应拒绝，但换成另一个合法值不能伪称App proof不匹配（如stateDir/nonce，必须验证其新值被分别保存而不是丢弃）。Mock API/control摘要相等应拒绝；valid control摘要不等不要求与v1 api相等。key缺一/多一/时间微秒或digest错、peer迁移少行/多行/坏version/SHA、marker少行/多行、raw canonical/SHA/decoded pins矛盾均失败，输出nil且无新资金事实。对原SQL允许超长TEXT的peer列构造正常静态schema下的超长值，独立观察安全投影资格位失败且客户端未收到原大字段；不得用本地Scan后len替代传输前闸门。

结构负例如view/unlogged/RLS/owner/OID/列/约束/正文/ACL漂移由完整checker在同RR拒绝；另用reader的私有只读scalar/row验证函数做表驱动负例，避免全部被前置static失败遮住而矩阵校验从未执行。不提供生产skip已持锁或可信proof构造器。

### 9.3 完整ordinary-off真实PG

五个受审ordinary变体正例保留原非支付商业资金/采购/PB历史并通过。21表逐项非空都失败；21项之外分别覆盖每个旁路谓词的匹配行、policy enabled/revision2、缺行/坏表和query-conflicts FK缺失/nullable/deferrable/not validated/错parent/错列。APP原无child SELECT保持，维护reader也保留mandatory FK证明；不能只测试marker/attestation为空。

触发器禁止不合法插入时，负例fixture可在本次隔离owner事务以明示受审方式设置反例，再只检验私有row/结构判定；不得借disabled trigger/drop constraint的安装取得假的InstallationProof。最终实际公开维护链仍必须经过原完整static proof。验证记录分别列明public链和私有判定层覆盖，不能把两者混称为同一次发行。

### 9.4 同snapshot/生命周期与资源

- 用隔离fixture验证真实MVCC：先安装原完整静态对象、App marker/v1及原provision所必需的状态，保留两peer的environment/schema_migrations/signing_keys为空；维护测试RR首次catalog SELECT后，由本次独立installer按原owner路径合法INSERT并提交这四张peer单例/迁移行及全部key行。旧RR必须仍报peer missing并返回nil，新Gate/新RR按同来源重新检查后才能看见完整前驱。此为验证读取快照的显式测试扰动，不授权实际维护期间并发owner写入；不UPDATE immutable行，不临时撤trigger，不以一条可变QA副本替代受审表。另核reader前后同top xid/PID/连接及caller-owned snapshot，证明没有另Begin/另pool。
- entry/exit之间换wrapper/conn/PID/top xid、实际身份变更、结束/错误tx、stage并发Close/Commit、context预算和Rows错误/超界必须nil evidence并永久撤销。
- 真实RR RW自己可见修改及维护后post-init上下文分别验证；不调用APP-only RuntimeProof、不要求新的首发安装证明，不能把post-init proof升级为startup许可。
- 原v1 source、SQL、ACL/provision SHA及APP/worker/projector跨schema零权限保持；仅维护LOGIN按合同读peer。fixture进程/连接/角色/schema精确清理，独立backend/锁观察；资金摘要前后相同。先相关真实PG/race，再集成人架构复核/refresh/整仓检查。

## 10. 根冻结前审阅清单（历史，不扩大验收）

1. T1/T2 bootstrap stage的唯一Go API、busy覆盖、Snapshot期限传递和真实维护身份先于reader；不能只靠两个VerifyTx围住任意caller ctx。
2. 允许上述两个叶子import，固定manifest来源及原schema-check规范；不修改现有v1 codec/SQL/权限来打破循环。
3. 确认本候选所有created/applied列的完整读取/finite校验与异常历史失败关闭边界；不为追求“等同旧启动”假称未读列原本已验。
4. peer本地stateDir/nonce/control值只按格式/真实内部关系核，不假绑定App v1；active key与秘密原件保持未认证。若需要新跨peer承诺另设计v2，不修改旧v1。
5. 5秒reader/1MiB/262业务行的候选限额、固定错误及私有golden桥方式；先验收相关专项再接Attest，任何阶段未实现都不开放init/main。

本节保留冻结前审阅事项：当时只增加候选文档、未执行测试或发行证明。当前矩阵和累计资源边界已由§11所述根冻结落地；不能把当时的“未实施”说明当成当前状态，也不能用后续结果追溯扩大当时验收。本稿不自行刷新架构清单。

## 11. 根冻结与实施状态

本矩阵全部逐列语义、ordinary 21表/6旁路/mandatory FK、SQL安全投影与累计资源界由[c0-authority-contract](c0-authority-contract.md)冻结并已实现于[reader](../../internal/maintenancetrust/predecessor.go)、[私有codec](../../internal/maintenancetrust/predecessor_codec.go)、[行矩阵](../../internal/maintenancetrust/predecessor_rows.go)及[SQL投影](../../internal/maintenancetrust/predecessor_sql.go)。所有时间采用typed pgtype.Timestamptz及有限/年份/微秒校验；created/applied有限性是本片新增失败关闭边界。累计5秒subcall沿原raw-close watcher，成功先stop/ack再cancel，1MiB按安全投影字段字节与primitive固定宽度计，哨兵已返回字段也计入。[T2真实PG/race专项](../../docs/verification/maintenance-authority.md)已通过，记录分别列明公开Attest链、私有PG判定层及原codec/QA golden桥的证据范围，不能以纯测试或私有判定层替代公开Attest链验收；ReadLease/全来源/basis/epoch与生产接线仍后续。

# 028-U：统一文本向量的生产存储合同草案

2026-11-04；状态：**供 U 片实施冻结的精确 DDL 合同草案；尚无正式迁移、生产接线或 PostgreSQL 原型验收**。本文只定义一套当前数据模型。正式迁移号、共同 Go ABI、SQL 函数签名及权限材料由集成人汇合冻结；不能把本文中的表名当作已经安装的关系。

上位依据：[AGENTS](../../AGENTS.md)、[028 主规格](spec.md)、[共同值域](u-domain-contract.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)、[ADR-005](../../docs/decisions/ADR-005-pre-mvp-single-path.md)、[纯计量核](../024-extension-platform/b0-pure-contract.md)。已逐项核对现行 [013](../../migrations/013_cost_aware_routing.sql)、[014](../../migrations/014_procurement_accounts.sql)、[018](../../migrations/018_tenant_pricing.sql)、[020](../../migrations/020_periodic_budgets.sql)、[023](../../migrations/023_wallet_sources.sql)，并追到其依赖的001/002/003。本文不改变被冻结的离线proof、profile、历史重放材料，也不要求它们接受新模型。

## 1. 本片边界与建议冻结项

U 完整替换现行六种文本协议的内部 Token 专用计量：Chat、Responses、Messages、Gemini、Embeddings、Rerank 共用 schema、费率、原界、证据和资金入口。U 可发布的 schema 两侧均恰为 `text_input_token/token` 与 `text_output_token/token`；唯一 variant 为 `default`。Embeddings/Rerank 的输出界、零售价、已知采购价和最终量都必须明确为 0。不安装 image/audio/video 维度，不注册媒体端点，不先建没有 writer 的媒体任务或会话表。

U保留当前生产路由模型：同一model的所有channel/binding/credential候选共用唯一model capability。route reader按共同Profile digest去重后集合恰一项，billing从原route revision和原capability独立重建并核集合；不能以跨渠道重复profile触发纯工厂的重复候选错误。本片不新增binding.profile_version、supplier_profile表或request候选子表。共同纯域的多候选max/ForAttempt收紧是已设计算法，**不等于U发布了异构profile路由**；后续如需异构，先补binding profile身份与参数/单位/usage/输出资格合同，再加DDL与验收。

通用关系可以容纳纯核的最多 8 维完整集合；这不授予非文本 schema 发布资格。schema 定义和有限 profile 来自受审代码合同，HTTP 客户不能自定义维度、单位、表达式或原价。U 不重写纯核算法。

本草案提出以下单一选择，需集成人结合共同类型片一次冻结，不同时保留另一实现：

1. `variant_key='default'`；价格绑定 model/schema/variant，不额外绑定某个 profile 版本。发布时验证它是当前 profile 合法组合；请求另冻结不可变 profile。相同 schema/variant 的资源限制修订不强迫复制一份价格。
2. 默认零售价、组价与渠道采购价都进入现有 `pricing_operations`。保留 `group_price.publish`，增加 `default_price.publish/cost_price.publish`；都必须有 operation、完整向量指纹和双向结果核验，不新增成本命令框架。
3. 采购逐维上限直接作为不可变 `model_bindings` 的子行。binding 已属于不可变 route version，不再加第二套限额版本或可变 pointer。
4. `finalization_records` 是受审观察原件，不意味着两边都可收费。客户沿 `request_records.latest_finalization_id`，供应商沿新增 `upstream_attempts.latest_cost_finalization_id` 独立推进；唯一客户 `settlements` 不变。不新建媒体结算表或第二套采购账本。

集成人已确认上述方向；本文仍是**待正式SQL/原型审查的存储草案**，不是已安装的生产事实。正式SQL实施前必须将选定名字、字段、canonical ABI和实际PG原型结果补入主规格及任务清单。

## 2. 列型、canonical 与关系通则

本文的列清单采用 `列名 类型 [约束]`；未写 `NULL` 的新增列一律 `NOT NULL`。继承的列只在明确列出删除或替换时改变，不用 `DROP ... CASCADE` 清除未知依赖。

| 语义 | 精确数据库合同 |
| --- | --- |
| 新业务 ID | `TEXT COLLATE "C"`，小写连字符非 nil UUID；保持现行 TEXT 键，避免把既有 ID 擅改为 PostgreSQL UUID 类型 |
| schema digest / vector digest | `BYTEA CHECK(octet_length(value)=32)`；API 显示小写 hex，但 DB 绑定原始 32 bytes |
| quantities / bounds / rate numerator | `BIGINT CHECK(value>=0)`，不使用 float；实际量可超过 schema maximum，预占界不可超过它 |
| denominator | `BIGINT CHECK(value BETWEEN 1 AND 1000000)`，`gcd(numerator,denominator)=1`，0 价仅 `0/1` |
| 客户 R / charge / refund | 原有非负 `BIGINT`；R、已知 U 发布/准入时均须可表示为 int64 |
| 真实费用 / waived / 采购累计 | `NUMERIC(100,0)`，非负；实际量超界所得真实采购费不截为 U，不把未知填 0 |
| 时间 | 新增 `TIMESTAMPTZ(6)`，`CHECK(isfinite(value))`；DB 选价/准入取时刻，HTTP 输出 UTC；provider 时间只是证据元数据 |
| 创建事务 | 新不可变 root 的 `created_xid XID8 DEFAULT pg_current_xact_id()`；`created_at DEFAULT clock_timestamp()`；BEFORE trigger 覆盖调用者自报值 |
| canonical bytes | `BYTEA`，schema ≤16384、单 bounds/rates/quantities ≤8192、报价快照 ≤16384、观察 ≤32768 bytes；不存模型正文或二进制 |
| 维度 / 单位 / side | `TEXT COLLATE "C"`；代码固定 registry 与精确配对；side 仅 `retail/supplier`；U 发布额外限定前述两维 |
| variant | `TEXT COLLATE "C"`，1..100 ASCII bytes，匹配 `^[a-z0-9][a-z0-9_.-]*$`；U 只接受 `default` |
| profile version / 合同身份 | profile版本为canonical UUID；完整provider合同用共同Profile的`ContractDigest`原32bytes。供应商仍沿019原channel/version/credential/upstream及采购binding/account定位，不增supplier contract ID/空表 |

规范十进制字符串及非法 JSON/UTF8/重复 member 的拒绝在受限 Go parser 先执行。DB 数值列证明存储值的范围，不能声称 PostgreSQL cast 保留了原始字符串的词法。DB 的所有跨行金额都由整数数量和有理率重新计算，不信任调用方给出的金额。

schema、bounds、rates 与纯核 Quantities 的 canonical/摘要 framing **逐字采用纯核合同**，不能用 `jsonb::text` 或 JSON 键遍历顺序替代。共同 `FinalQuantities` 与纯核 Quantities 共用 canonical bytes，但摘要是不同身份：`finalization_records.*_quantities_digest` 必须使用 `inference.FinalQuantities.Digest`（`moni-aigate.inference.final.v1` 域、raw32 schema digest、side、canonical 的长度 framing），不得填入纯核 `metering.Quantities.Digest`；平台 probe 的 `quantities_digest` 则仍为纯核摘要。这里不新增冗余的最终量纯核摘要列，若后续确需持久化，必须另名为 `*_meter_quantities_digest` 并补合同。DB 从已验证关系行按 `dimension COLLATE "C"` 重建 canonical bytes，要求 root bytes 与重建结果逐 byte 相等，再按该字段规定的长度 frame/domain 重算 SHA-256。由此额外字段、重复 member、数组错序和非规范整数不能藏在 root JSON 中。schema digest 包含完整 schema ID/maximum/mask；相同 UUID 不同内容不相容。

DB金额复核也必须精确。最多8维，以NUMERIC整数先构造`D=Π denominator_i`、`N=Σ(quantity_i*numerator_i*(D/denominator_i))`，其中`D/denominator_i`必须整数除法；一次最终ceil用`div(N+D-1,D)`，全0得0。不得依赖NUMERIC非整除的有限小数scale再ceil，也不得逐维ceil。按纯核int64数量/率、分母≤1e6和8维，整数中间量在NUMERIC(100,0)内；发布/准入再核最终R/U≤MaxInt64，真实raw可大于它。SQL与Go用独立旧公式/独立分数oracle验一致，不能互相抄expected。

非 canonical 的安全 profile 参数仍可用 JSONB，但必须形状受限、无正文/秘密，且从固定字段与有限数组重建共同 profile canonical；不能把 DB JSONB 输出格式误认为共同 ABI。计划/观察的新 canonical 采用共同类型片冻结的精确编码；在编码未冻结前不得先发行能够写入生产的 SQL 函数。

所有新增租户事实同时保留 `tenant_id`，父端提供 `UNIQUE(tenant_id,id)` 或更窄复合键，子端使用带 tenant 的 FK；只有平台域的 schema、价格、渠道和采购账户保持平台域键。禁止为了复用 probe 而把 `metering_events.tenant_id` 改为 nullable。FK 只核定位；同一 request/attempt/profile/schema/variant 的逐列一致性另有明确验证。

所有新增 root/child 的历史不可 UPDATE/DELETE。root 出生后不能追加子行：子 INSERT 取得对应 root `FOR SHARE`，检查其 DB `created_xid` 恰为本事务；子完整性 CHECK 与不可变 trigger 都保留。父/子允许在同一事务中按规定顺序出生；缺一个子、额外一个子、指向另一个 root 或只有子没有 root 都不能 COMMIT。

## 3. schema、profile、variant 与路由

### 3.1 `meter_schemas` / `meter_schema_dimensions`

`meter_schemas`：

```text
id TEXT PK
name TEXT                           -- 1..100 ASCII 标识符
revision BIGINT CHECK(revision>0)
registry_version SMALLINT CHECK(registry_version=1)
wire_version SMALLINT CHECK(wire_version=1)
canonical BYTEA                     -- <=16384
schema_digest BYTEA                 -- 32 bytes
created_xid XID8, created_at TIMESTAMPTZ(6)
UNIQUE(name,revision), UNIQUE(id,schema_digest)
```

`meter_schema_dimensions`：

```text
schema_id TEXT, dimension TEXT, unit TEXT
maximum_quantity BIGINT CHECK(maximum_quantity>=0)
retail BOOLEAN, supplier BOOLEAN
PRIMARY KEY(schema_id,dimension)
UNIQUE(schema_id,dimension,unit)
FK(schema_id) -> meter_schemas(id) DEFERRABLE INITIALLY DEFERRED
CHECK(retail OR supplier)
```

提交时 root 的 dimensions 必须为 1..8 行；两个 mask 都非空，二者并集恰为全部维度。U 发行 gate 再核两侧都是精确两维文本。`schema_digest` 与重建 canonical 相等。global maximum 是实际定义的一部分，不自动把所有模型填成 MaxInt64；schema 可以跨 profile 共用，但只有全部定义完全相同才复用。迁移不凭空播种占位模型/schema。

### 3.2 `model_capability_versions` 与子表

保留原 `id/model_id/profile/created_at`，增加 `schema_id TEXT`、`schema_digest BYTEA`、`protocol TEXT`、`execution_kind TEXT`、`contract_digest BYTEA`、`profile_digest BYTEA`、`created_xid XID8`；`profile` 是严格受限定义，不含原媒体。增加 `UNIQUE(id,model_id)`、`UNIQUE(id,schema_id,schema_digest)`，以及schema复合FK。U仅接受六文本protocol、`execution_kind='synchronous'`；Profile按共同合同允许json/events结果集合，不为stream拆两个profile，input-only只能json。Plan根据parser已选Stream bool固定本次result_kind。不能先开放尚无writer的异步/实时枚举。

新 `model_capability_variants(profile_version TEXT,variant_key TEXT,parameters JSONB, PRIMARY KEY(profile_version,variant_key))`；profile FK deferred，parameters 严格精确字段且 ≤16384 bytes。U 恰一行 `default`，parameters 仅允许共同 profile 定义的有限文本参数政策，不保存请求值。

新 `model_capability_bound_lines(profile_version TEXT,variant_key TEXT,side TEXT,dimension TEXT,unit TEXT,quantity BIGINT>=0)`，PK `(profile_version,variant_key,side,dimension)`，FK `(profile_version,variant_key)`→variants。schema 维度/单位及 mask 通过 profile 的 schema 精确关联；每 side 必须完整，quantity≤schema maximum。每 profile/variant 的两侧全局界是供应商认证/协议 parser 的合同，不能按 `context-output` 猜输入。Embeddings/Rerank 的两侧 output quantity=0；生成协议 input>0、output>0。

`models.profile_version` 改为 FK `(profile_version,id)`→capability `(id,model_id)`，deferred 允许 model 与其首版同事务出生；删去 `models.profile` 镜像列，全部读取通过不可变 capability。`models` 的 enabled/public/active_revision 等现行管理字段保留。模型指向新 profile 时，非零 active_revision 必须指向同一 profile 的路由版本，不能形成新 profile+旧路由组合。

`route_policy_versions`增profile_version TEXT和created_xid XID8，FK`(profile_version,model_id)`→capability，绑定公开模型当前唯一profile。`model_bindings`只增variant_key TEXT和created_xid XID8，不增独立profile身份；全部binding沿该model/route原profile。原channel/version/credential/upstream及ContractDigest固定实际供应商合同，不建contract ID层。原UK`(route_policy_version_id,channel_id,upstream_model)`替换为含variant的UK。两root stamp均由DB覆盖以支持限额子行出生检查。保留不可变route/binding及cost_mode/unknown_cost_policy。U已删除route_revision=0无route门禁旁路：当前请求必须持有正数、同profile的route revision，未发布route不能进入计量快照。

新 `binding_cost_rate_limits(binding_id TEXT,dimension TEXT,unit TEXT,numerator BIGINT,denominator BIGINT)`，PK `(binding_id,dimension)`，FK→model_bindings。每行是 supplier mask 的合法维度及相同单位，分数满足第 2 节；允许无上限行或仅部分维度上限，**无行是无限额，0/1 是明确零上限**。binding root 已经不可变，因此子限额必须同事务完整出生，修改限额通过发布新 route。删除原 `max_input_cost_rate/max_output_cost_rate`。

## 4. 不可变价格与采购报价

### 4.1 `price_versions` / `price_rate_lines`

原 `id/model_id/price_group_id/currency/effective_at/created_by/created_at/pricing_operation_id` 保留；currency 固定 CNY，created_at/effective_at 必须 finite；移除 input_rate/output_rate。增加 `schema_id TEXT`、`schema_digest BYTEA`、`variant_key TEXT`、`rates_canonical BYTEA`、`rates_digest BYTEA`、`created_xid XID8`。新增 schema 复合 FK 和 `UNIQUE(id,model_id,schema_id,schema_digest,variant_key)`。

唯一报价域改为 `UNIQUE NULLS NOT DISTINCT(model_id,price_group_id,schema_id,variant_key,effective_at)`；当前索引按同域加 `effective_at DESC,created_at DESC,id COLLATE "C" DESC`。schema_id 决定 digest，不能靠不同 digest 绕同 schema 唯一性。相同 domain/group 的缺价是 unavailable，不回落默认。没有合法当期价不派发；完整全 0 费率是有效免费价。

`price_rate_lines(price_version_id TEXT,dimension TEXT,unit TEXT,numerator BIGINT,denominator BIGINT)`，PK `(price_version_id,dimension)`，FK→price_versions deferred。提交时行集合恰等于 root schema 的 retail mask；unit、规范率、canonical/digest 全相等。发布 root 与全部行在同一 pricing operation 事务，root/child 双端有完整性约束。对当前 profile/variant 全部合法上界重算 R 必须≤MaxInt64；Embeddings/Rerank 的 output rate 必须 0/1。旧 `immutable_price` 保持同语义，子行也不可变。

随后扩大同schema的profile/route界时也必须复核其可能选中的生效/已排期零售与known采购价的最坏R/U；不能因为价曾在较小profile通过而自动授予更大界。Reserve/RecordAttempt仍各自重算并拒绝溢出，是独立最后门禁；不把巨大上界截MaxInt64。

#### 4.1.1 客户选价、预占与失败边界（2026-11-05 冻结）

`pricing.SelectEffectiveTx`在调用方已持tenant/model与assigned group原锁后，以一个SQL语句取得新DB priced_at、该model当前唯一capability/default的schema及同域当期原价。必须完整限定model/group/schema_id/schema_digest/default，排序沿effective_at/created_at/id逆序；不可从另一schema或默认域补组缺价。没有合法capability/schema是pricing_invariant；有效schema但没有当期价格才是unavailable。未来价不能提前生效，完整零向量是有效价。

现行Selection只有原version/group/effective_at/priced_at、profile version、variant以及opaque Schema/Rates，不再返回InputRate/OutputRate。schema canonical、DB ID/digest与Rates canonical/digest必须逐byte匹配完整retail mask、side和CNY，错误返回pricing_invariant，不能退回meter_price_million、丢维度、零价或另一版本。schema/费率读取和选价clock同一语句快照；结果不保留调用方可变group指针，不在钱包等待后重新选价。

Reserve独立核Selection.profile/schema等于原锁定capability；由其Schema和本次已验证文本界构造retail Bounds，直接调用metering.ReservationAmount(bounds,selected.Rates)得到唯一R。溢出以amount_overflow拒绝，不通过Amount+截断或自行ceil。input-only的输出率必须显式0。原Key/PB/027来源/账户锁序、选价时刻早于钱包等待、全部等待后期限重验及出生事务保持。价格/界/金额失败必须回滚idempotency、request、reservation、Key/PB、来源/账本/audit/outbox，不得留半成品。

Reservation是资金回执，只携原version/domain/revision/priced_at和原金额/状态等资金身份；移除仅为标量回显的InputRate/OutputRate，不为本次删字段再添加legacy DTO、空值联合或第二套quote。原完整Rates仍在不可变价格/请求快照，外层inference报价身份不变，计划未就绪门禁不变。管理端每百万token输入仍保留；采购路由/预占DTO与发布operation/指纹是后续片，不能据此标全线切换完成。

历史前缀研究仍只检查原迁移范围、自由profile版本与旧列。因现行资金回执删字段而必需的测试编译调整，只可把其原费率断言移到原request旧列，不给它升级schema或补当前vector兼容。不改历史pins、原型宽ABI或001–023。验收需以当前PG夹具禁用标量反算函数，覆盖默认/组价、零价/缺价/未来价、schema域隔离、原价跨model/group/wallet等待、input-only、单次ceil/溢出与回滚；专用55439夹具继续只编译，不用5432冒充。

### 4.2 `cost_versions` / `cost_rate_lines`

保留 `id/channel_id/upstream_model/basis/effective_at/created_at`；删除 input_rate/output_rate。增加 `schema_id TEXT`、`schema_digest BYTEA`、`variant_key TEXT`、`currency TEXT CHECK(currency='CNY')`、`rates_canonical BYTEA NULL`、`rates_digest BYTEA NULL`、`pricing_operation_id TEXT`、`created_by TEXT`、`created_xid XID8`。schema FK同上；pricing operation FK deferred+UNIQUE，created_by FK users。rate/quote status不从NULL数字猜测。

`cost_rate_lines(cost_version_id TEXT,dimension TEXT,unit TEXT,numerator BIGINT,denominator BIGINT)` 同 retail 结构，行集合按 supplier mask。`basis IN ('contract','invoice')` 必须恰一份完整非 nullable 费率向量；`basis='unknown'` 必须没有子率，canonical/digest 同时 NULL。known 的 0 率要有 0/1 行，不能以无行表达。基于尚未认证合同时的“invoice”标签不自动证明允许派发。

报价域为 `(channel_id,upstream_model,schema_id,variant_key)`，增加同域+effective_at UK，索引同域+原三项确定逆序。沿现行channel共享报价语义，不加入凭据专价或新supplier合同身份；选中attempt另冻结实际channel/version/credential/upstream/profile合同。`synchronize_cost_version_insert` 仍先 UPDATE channel 与派发 SHARE 串行并递增 management_revision；同刻冲突改为完整域，不继续用旧 channel/model 两键误挡另一 schema/variant。发布沿原admin/CAS/audit，并以cost_price.publish纳入已有pricing_operations同一原件/指纹规则。

#### 4.2.1 采购报价域隔离与失败边界（2026-11-05 冻结）

管理端继续输入每百万token整数，但必须显式提交已发布模型的 `schema_id`、64位小写hex `schema_digest` 与 `variant_key='default'`；列表/发布成功响应回显相同身份。选择器从不可变capability取身份，不按模型名称、最近创建时间或当前全局schema猜测。缺身份、nil/大写UUID、错摘要、未发布schema或非default变体在写入前拒绝，不能兼容旧请求省略身份；渠道CAS、报价和audit须整体回滚。

SQL入口 `meter_insert_token_cost(cost_id,channel_id,upstream_model,schema_id,input_per_million,output_per_million,basis,effective_at)` 的schema_id是必填定位，摘要由同一不可变schema重读；只允许已有capability/default引用的两维文本schema。known输入只收完整两价；输出maximum为0的input-only schema仅接受零输出价。raw root插入也须核schema发布资格与input-only零价，不能绕过函数。unknown仍没有费率行/canonical/digest，不能伪装免费。

候选读取沿原route的profile取得schema，attempt准入与采购锁后复验沿原request snapshot取得schema，派发复验沿原attempt snapshot取得schema；所有选价必须同时限定channel/upstream/schema_id/schema_digest/variant，之后才按原三项倒序。另一个schema的更新、unknown、排期价或同刻版本既不能挤掉本域原价，也不能补本域缺价；缺价仍为version_missing。所有快照（含计划未就绪和basis_unknown）在提交时独立校验报价与真实attempt的channel/upstream及原schema/variant完整一致，不能仅核known rates canonical。跨schema/跨channel/跨upstream即使费率全零或unknown也整笔拒绝，原快照不追溯改价。

为实现上述原route定位，本片同时落第3节的route.profile_version与出生stamp。BEFORE INSERT锁定model并从其唯一当前capability派生profile；若调用方显式给出另一profile则拒绝，绝不按最新capability全局查找。profile/model复合FK和原route不可变约束保留；model非零active_revision必须指向同profile的原route。请求快照的route_revision必须绑定该profile，避免调用方将另一schema的当前profile接到旧route；route_revision=0由正数约束和无条件快照 guard共同拒绝。

本片先关闭上述域隔离缺口；不将仍待改造的向量DTO、pricing_operations全量接线和精确发布指纹写成已完成，也不授予异构profile路由资格。

route 的逐维 cap 以 `quote.numerator*cap.denominator <= cap.numerator*quote.denominator` 精确比较。任一 cap、cost_first 或 unknown=deny 都要求完整 known quote。最高 priority 池先用完整 supplier bound 和 Rates 的**未 ceil 有理总费用**比较，再沿现行 weighted tie。不能逐维比后只取 input、按已 ceil U 排序或用零售价评分。

#### 4.2.2 完整候选报价值与分片接入边界（2026-11-05 冻结）

先行片实现独立纯域 `internal/routecost`；后续在线片已将当前候选/采购/派发DTO原子替换为该完整选择值，删除 `routing.CostQuote`、两价成本评分及内部两价cap镜像，不启用shadow计算、双运行或兼容回退。显式冻结原型使用独立输入和Store，不消费新Quote。当前接入与限定验收如下；这不表示发布operation/全部HTTP/完整guard或U商业出口已完成。

`routecost.Quote` 是不可变选择值工厂：完整Schema/default、实际channel/upstream、原cost version/basis与完整supplier Rates；known/unknown保持第4.2节联合，missing version与显式unknown版本可区分。零值无效；known须原version非空及完整率（含明确零），unknown允许缺version或有显式version，但Rates必须无效/缺失。仅报价选择值不新增canonical/digest算法或第二个inference报价身份，不证明供应商真实性、版本归属、派发或资金权限。直接构造传入的Rates必须与完整schema定义/摘要及supplier侧一致，不能仅核schema UUID。

`StoredInput` 与已解析 `Input` 分离，存储读端逐byte核schema/率canonical及摘要：JSON可解析但非规范、摘要缺失或不等、错side/错schema、known缺率均返回 `ErrInvalidQuote`，绝不降级为unknown；unknown两率字段须真NULL（nil），空BYTEA也拒绝。解析不接受调用方额外传入已解析Rates来覆盖原件，不调用meter_cost_million或由int64每百万镜像重建。该纯函数不能代替SQL同tenant/request/attempt/channel/upstream/原域归属guard。

`routecost.Limits` 是同schema supplier mask上的可选有理上限集合；无行表示无cap，0/1是明确零上限。每行维度/单位、C序无重复、规范int64字符串、非负分子、1≤分母≤1_000_000、约分必须成立；JSON仅收恰四键的数组行，拒重复键/尾数据/非整数字符串/超界。工厂与只读描述都copy数组。空Limits为无cap；非空须匹配Quote完整schema摘要。`Exceeded` 只做 `quote.numerator*cap.denominator > cap.numerator*quote.denominator` 的big.Int精确比较，不ceil或反算每百万。零Quote是ErrInvalidQuote，非空cap错绑定是ErrInvalidLimits，合法unknown即使无cap也返回ErrUnknownQuote；是否manual/allow由现行调用方策略决定，纯层不能把unknown判成免费或擅自批准。金额和成本比较仍只由metering提供。

在线接入合同（本续片已接线）：候选读取完整同域Rates，成本优先通过metering.CompareExactCost比较完整supplier Bounds与Rates，采购初筛由ReservationAmount单次ceil；原priority层内精确并列后才沿channel/credential权重。管理端每百万token输入保留，只在发布入口约分；不得允许异构profile路由。RecordAttempt在原锁/按序batch后读取同request域完整报价，managed/cap/受管采购与预期报价完整相等；identity/schema/variant/version/basis/任一率变更是cost_quote_stale，坏原件是invalid_meter_snapshot，unknown按原政策cost_unknown。account/PB等待后新RC语句复验；PrepareDispatch沿原attempt与既有最后锁后DB决策时刻复验，不采另一时钟，不改原U/客户价或回填未就绪canonical。

普通billing与显式NewProspective的完整输入已隔离：冻结原型保留原13参scalar/历史TEXT/字节ABI和原SQL/pins/旧差分依据，不把新Quote降维喂给原型，也不让当前路径获得旧运行权。在线切换验收在禁两标量反算函数后的受限SET ROLE APP上覆盖候选→RecordAttempt→采购→PrepareDispatch→唯一结算：六协议paid/known-zero、unknown政策及排期报价跨凭据等待均有局部专项；精确cap/同ceil异精确排序及错schema由纯域/计划测试验证，原报价域/采购account等待由PG专项验证。不能把这些不同层级合称实际LOGIN或全部负控均在受限APP上跑过。55439专库/浏览器/供应商及完整六文本商业出口仍另验。

4.2.2a 输入隔离：显式NewProspective返回独立ProspectiveStore，仅接受FrozenAttemptInput及procurementexecution.CostQuote原13参字节联合；它不嵌入普通Store，不提升当前资金方法，也不接受routecost.Quote。普通Store删除prospective配置字段/运行分支；构造器和方法类型不能交叉代入。路由解释只共用不含报价的纯身份/Decision校验，顺序、固定错误、nil/空字节和未知COMMIT分类保持原样。冻结执行器原SQL、映射、观察器实现与pins不变；历史fixture只有必要输入类型/编译边界调整，不能据此说历史业务运行验收不变或已通过，具体限制见4.2.2c。实际APP执行仍需原资格/专库，类型隔离不赋予current运行资格；在线接入不可通过该独立Store绕回标量。

4.2.2b 候选计划门禁：Limits.ValidFor只验证Quote有效和非空cap完整schema摘要一致，允许合法unknown参与manual/allow后续政策；它不是批准。NewPlan必须先校Quote实际channel/upstream、完整schema一致及文本Bounds不超过原schema硬界，坏值不能延迟到成本排序或变成cost_unknown。跨channel报价/相同UUID不同schema内容、无效零值、错cap绑定或超界均拒绝整个计划；候选仍全部来自每模型唯一capability。CostExclusion返回(reason,error)，先验证policy/原件，只有合法unknown才按manual/allow放行；无效policy返回ErrInvalidPolicy，无效报价/错cap返回原routecost错误，reason为空，不能吞成cost_unknown。Engine遇错误整次拒绝，不生成安全原因词汇之外的Decision；billing将非法存储策略/原件统一拒为invalid_meter_snapshot，合法未知和超cap沿原业务错误。排序只委托metering.CompareExactCost，初筛U只委托ReservationAmount，不恢复旧CostScore或两价公式。

4.2.2c 冻结fixture的编译隔离：固定022/019的报价行继续读原旧列到测试专属DTO，不能构造routecost或调用current SQL读端。已不适用的current billing方法不能作为旧库运行兼容承诺：冻结夹具调用当前attempt API时，经测试专属强类型边界返回明确“current vector billing rejects frozen scalar attempt input”失败，禁止丢弃旧报价、伪造schema或降维转换后继续资金操作；在原研究专库真正恢复历史business oracle前，该类旧组合只编译，不宣称运行验收。纯旧报价差分仍以测试专属原Equal/原SQL检查，不改SQL/pins或迁移范围。显式NewProspective有自己FrozenAttemptInput，仍按原13参原型执行，不经过这个拒绝边界。当前迁移的periodicbudget billing夹具另用原canonical报价，历史seed不改；55439未就绪只编译。

### 4.3 定价 operation 的精确变更

018 的 operation/actor/scope/target/resource/created_txid/DB 时间、再认证、唯一 operation 与结果/audit 核验保留。`pricing_operations.kind` 增 `default_price.publish/cost_price.publish`；三类publish的revision必须NULL，其余原kind保持原revision合同；`pricing_operation_id` 对默认/组价/采购价均NOT NULL+UNIQUE，移除“默认价operation=NULL”的旧CHECK。

零售两类发布 `safe_input` 精确为：

```text
{model_id:UUID,schema_id:UUID,schema_digest:hex32,variant_key:"default",
 currency:"CNY",rates:<完整规范 RatesWire>,effective_at:UTC6}
```

成本发布safe_input精确为`{channel_id:UUID,upstream_model:string,schema_id:UUID,schema_digest:hex32,variant_key:"default",currency:"CNY",basis:"contract"|"invoice"|"unknown",rates:完整RatesWire|null,effective_at:UTC6,expected_channel_revision:正规范整数string}`；known/unknown联合严格按第4.2节，NULL不是免费报价。

group从受权target路径绑定，不可让body再指定另一group。三个target分别沿现行`/api/platform/price-versions`、`/api/platform/price-groups/{group_id}/price-versions`、`/api/platform/cost-versions`，SQL从kind和原资源身份精确重建，不接受自由target。发布返回精确版本身份+完整rates或明确unknown+effective_at+DB created_at；采购还保留操作前后channel revision与原CAS效果。默认/组价audit沿`pricing.price.publish`，采购价用`pricing.cost.publish`，均含原operation_id/target/安全after，恰一条且同事务。原result_schema当前形状直接升级，不写legacy/null/fallback联合。

`pricing_fingerprint`保留现有pricing_frame的带长度/NULL framing。零售publish frames为`[kind,actor,scope,target,model_id,schema_id,schema_digest,variant_key,currency,UTF8(rates_canonical),UTC6(effective_at)]`；成本为`[kind,actor,scope,target,channel_id,upstream_model,schema_id,schema_digest,variant_key,currency,basis,UTF8(rates_canonical)|NULL,UTC6(effective_at),expected_channel_revision]`。rates先验证C序，不能排序接收乱序输入。禁止请求raw JSON/hash、JSONB::text或仅version ID意图指纹。相同operation同语义返回原回执；另一rate/schema/variant/effective_at/actor/target或成本CAS均冲突；重放不产生第二版本/audit。bounds不在publish输入，global maximum已绑定schema摘要。

### 4.3.1 组价发布先行片：失败与重放边界（2026-11-05）

本片只切已存在operation的group_price.publish；默认/采购的operation接入仍待后续，不把现有默认/采购裸发布算已收口。组价管理HTTP继续收input_rate/output_rate每百万整数，但必须显式提交schema_id/schema_digest/variant_key；缺身份不猜当前schema。Go在同一operation事务内先核原操作/actor，再锁model、核当前唯一capability完整schema和该输入身份，构造retail Rates；safe_input与指纹只保留上述4.3的完整Rates，不保留两价镜像。有效输入必须UTC6微秒精度；不能静默截断改变意图。缺/错schema、非default、input-only非零输出、非规范/越int64价格、完整最大R溢出均拒绝，不留operation/版本/率行/audit。单位/顺序/约分/side/schema绑定在SQL及读端独立检查。

已存在operation重放须在读取当前模型/组状态之前比较原完整意图；退休组或模型停用不抹掉历史回执。身份匹配还要求原actor仍有管理员权限；异rate/schema/digest/default/effective_at/target/actor冲突。两价管理输入只在边缘经显式schema的不可变原件转换一次；原operation重放与结果读取也只依赖该不可变schema，不查询当前model/group来重建意图。操作ID锁、原model→group SHARE序、DB created_at与不可变回执/audit同事务保持。新价跨等待不得重选当前schema，任何写入或audit失败整笔回滚；COMMIT不明沿原pricing_busy/只读查询原ID，不换ID重发。

组价结果与组价历史列表改为同一闭集：id/model_id/price_group_id/schema_id/schema_digest/variant_key/currency/rates/effective_at/created_at，无input_rate/output_rate字段。Rates是纯核规范wire；解析可以从JSONB重排的对象键恢复字段顺序，但维度数组不允许重排、缺维、重复、错单位或非约分。不得从这些Rates反算两个每百万回显字段供内部资金使用。HTTP、TS指纹、原回执校验、组价列表与UI在同片切换；旧响应不fallback。其余retail/public/preview标量读取属于后续HTTP片，不据此说全部已切换。

SQL在COMMIT核原model/schema/variant、完整费率canonical、same-tx operation/resource/actor/created_at/target、safe_input/指纹/结果和恰一条原audit。原guard不允许借拼合合法operation换模型/组/schema/费率；schema或率错误即使金额仍0也拒绝。验收须包括APP禁两标量函数后发布/原回执/列表，Go/SQL/TS独立framing黄金值、同意图并发唯一版本、异体冲突、退休后重放、错域/非规范/溢出及audit失败回滚。固定018/022历史SQL夹具继续只核原范围，不升级024；缺55439仍只编译。

审计出生先核引用的组价operation存在且created_txid等于当前顶层事务，提交时再核完整after/身份及唯一计数；禁止事后补审计，也不以tuple xmin代替顶层事务身份（同一事务中的合法SAVEPOINT子事务仍可写原audit）。重放先以可规范表示的完整Rates比原意图，最大金额及input-only可售性只在新发布准备阶段验证，不能把异rate的同ID重放误报为新价错误。

### 4.3.2 默认价与采购价发布 operation（2026-11-05 冻结）

本片把默认零售价与渠道采购价接入同一 `pricing_operations`，删除 `Store.PublishPrice`/`Store.PublishCostVersion` 两条无 operation 的裸发布及其 `price.create`/`cost.version` 审计。三类发布从此同一框架：operation advisory 门 → 当前 admin `users FOR SHARE` → 资源锁 → 写 operation → 写资源 → 恰一条审计 → COMMIT；COMMIT 不明沿 `pricing_busy` 并只读原 operation，不换 ID。

HTTP 精确合同（两者都经 `pricingWriter`：当前 admin、近期再认证、无 query）：

- `POST /api/platform/price-versions` 请求体恰为 `{operation_id,model_id,schema_id,schema_digest,variant_key,input_rate,output_rate,effective_at}`。`input_rate/output_rate` 是每百万 token 的非负规范十进制整数（管理端单位，仅在入口经显式 schema 原件一次转 Rates）；`effective_at` 必填，RFC3339 且微秒精度，存储为 UTC。
- `POST /api/platform/cost-versions` 请求体恰为 `{operation_id,channel_id,upstream_model,schema_id,schema_digest,variant_key,basis,input_rate,output_rate,effective_at,expected_channel_revision}`。known(`contract|invoice`) 两率必为规范十进制字符串，`unknown` 两率必为 JSON null；`effective_at` 必填同上；`expected_channel_revision` 为正规范十进制字符串。
- 新建 201、同意图重放 200，响应 `{data:{operation,result},request_id}`；`GET /api/platform/pricing-operations/:id` 对两种新 kind 同样返回原回执。operation 的 revision 为 null。

result 精确闭集：默认价为 `{id,model_id,price_group_id:null,schema_id,schema_digest,variant_key,currency,rates,effective_at,created_at}`（与组价同形，`price_group_id` 恒 null）；采购价为 `{id,channel_id,upstream_model,schema_id,schema_digest,variant_key,currency,basis,rates,effective_at,created_at,expected_channel_revision,channel_revision}`，known 时 `rates` 为 supplier 侧完整纯核 RatesWire，unknown 时为 null，`channel_revision` 恰为 `expected_channel_revision+1`（报价插入的同步 trigger 只推进一次渠道 management_revision）。两者 `created_at` 等于 operation 的 DB `created_at`，资源行 `created_at` 同值。

safe_input 与指纹按 4.3：默认价 frames `[kind,actor,scope,target,model_id,schema_id,schema_digest,variant_key,currency,UTF8(rates_canonical),UTC6(effective_at)]`；采购价 frames `[kind,actor,scope,target,channel_id,upstream_model,schema_id,schema_digest,variant_key,currency,basis,UTF8(rates_canonical)|NULL,UTC6(effective_at),expected_channel_revision]`。target 分别固定为 `/api/platform/price-versions`、`/api/platform/cost-versions`。

错误与重放：新 operation 的渠道 CAS 不等为 409 `pricing_revision_conflict`，`expected_channel_revision` 已是 int64 上限为 409 `pricing_revision_exhausted`；同域同刻冲突（默认价 `price_versions_domain_effective_key`、采购价 `cost_version_effective_instant`）为 409 `price_effective_conflict`；未发布 schema、错摘要、非 default、input-only 非零输出、超 int64 或最坏 R 溢出为 400 `invalid_price`；模型/渠道不存在为 404。同 operation 重放在读取当前渠道/模型状态之前，以原回执的身份字段及经同一不可变 schema 重建的完整 Rates 比较原意图；同意图即使渠道已前进也返回原回执，异体 409 `policy_operation_conflict`。

SQL 必须独立核验，不信任 Go：

1. `pricing_operations.kind` 增 `default_price.publish`、`cost_price.publish`；三类 publish 的 revision 必为 NULL，其余 kind 原合同不变。`guard_pricing_operation_insert` 对三类 publish 都把 DB created_at 写回 result_snapshot。
2. `price_versions.pricing_operation_id`、`cost_versions.pricing_operation_id/created_by` 全部 NOT NULL 且 operation 列 UNIQUE；删除 018 的"默认价 operation 为 NULL"CHECK。
3. 资源出生 BEFORE INSERT guard：价格按 `price_group_id` 是否为空要求 kind 恰为 `default_price.publish`/`group_price.publish`，采购价要求 `cost_price.publish`；operation 必须本顶层事务出生、`resource_id=NEW.id`、`actor=NEW.created_by`，且价格的 schema/digest/default 必须等于该模型当前唯一 capability。
4. `require_pricing_operation_result` 对两种新 kind 从资源原件重建 expected safe_input、完整 result 与指纹逐值相等；默认价另核 target、`price_group_id IS NULL`、created_at 与最坏 R 可表示及 input-only 零输出；采购价另核 target、渠道当前 management_revision 等于 result.channel_revision 且等于 expected+1。
5. 审计：默认价沿 `pricing.price.publish`，采购价新 `pricing.cost.publish`；均恰一条、同顶层事务出生，`sanitized_diff={operation_id,target,after:result}`，COMMIT 时双向核验，不能事后补。
6. `meter_insert_token_price/meter_insert_token_cost` 仍是资源行的唯一 SQL 写入函数，但 operation ID 及 created_at 成为必填参数；没有 operation 的调用在出生 guard 即失败，不保留"NULL operation"捷径。

夹具边界：当前 024 夹具不得再以 NULL operation 写价格/成本。测试专用 `internal/testsupport/meterquote` 提供在单一事务内写入 operation→资源→审计的发布助手，actor 可为夹具已有的任意 active 用户（DB 不授权 actor，管理员资格由 Go 入口负责，相关 HTTP/Store 专项另测）；助手不被 serve 引用，不提供兼容或 fallback。固定 001–023 历史前缀夹具只按原 SQL 合同运行，不升级到本片。

## 5. 原请求、原 attempt 与快照

### 5.1 `request_records` / `request_meter_snapshots`

保留request的身份、tenant/key/owner/model、HMAC、原公开profile/policy、route_revision、deadline、instance、streaming、状态、latest_finalization、client_ip及020 PB stamp。删除input_rate/output_rate/input_upper_bound/output_limit；原price_version_id/pricing_group_id/tenant_pricing_revision/retail_priced_at移入**唯一**快照，父表不再维持另一份镜像。新增protocol/execution_kind/result_kind，由不可变profile和parser已选Stream共同派生且immutable；U限制同第3节，stream=true仅合法生成协议且result_kind=events。

`request_meter_snapshots`：

```text
tenant_id TEXT, request_id TEXT PRIMARY KEY, model_id TEXT
profile_version TEXT, schema_id TEXT, schema_digest BYTEA, variant_key TEXT
price_version_id TEXT, pricing_group_id TEXT NULL
tenant_pricing_revision BIGINT CHECK(>0), retail_priced_at TIMESTAMPTZ(6)
plan_canonical BYTEA, plan_digest BYTEA
retail_bounds_canonical BYTEA, retail_bounds_digest BYTEA
supplier_bounds_canonical BYTEA, supplier_bounds_digest BYTEA
retail_rates_canonical BYTEA, retail_rates_digest BYTEA, retail_meter_quote_digest BYTEA
retail_quote_canonical BYTEA, retail_quote_digest BYTEA
created_xid XID8, created_at TIMESTAMPTZ(6)
UNIQUE(tenant_id,request_id)
FK(tenant_id,request_id) -> request_records(tenant_id,id) deferred
FK(profile_version,schema_id,schema_digest) -> model_capability_versions(...) deferred
FK(price_version_id,model_id,schema_id,schema_digest,variant_key) -> price_versions(...) deferred
```

model_id与request.model_id/profile.model_id必须逐列相同，不能仅FK到price ID。price_group以NULL-safe比较，revision/assigned group与已锁tenant同时刻一致。`retail_priced_at`是**原选价时刻**，先于钱包/来源等待也不重新取价；允许scheduled price在等待中生效后仍保留已选原价，但期限必须在全部必要等待后重验。plan_canonical≤65536，报价外壳≤16384；retail_meter_quote_digest是纯核数值指纹，retail_quote_digest是inference外层quote身份，不能混用。

request Plan中的candidate_profile_digests保留共同canonical形状，但U必须恰一项，等于原request.profile_version重建的Profile digest；该版本/完整合同已在不可变model_capability_versions，不另造一份候选子表。route reader将原完整route所有合法候选的同一profile去重，billing独立核原route/profile闭包与Plan一致，不能只信caller给的一项。将来纯域可接受多Profile不放宽U的存储/发布gate。

`request_meter_bound_lines(tenant_id TEXT,request_id TEXT,side TEXT,dimension TEXT,unit TEXT,quantity BIGINT>=0)`，PK`(request_id,side,dimension)`，FK`(tenant_id,request_id)`→snapshot deferred。恰两侧完整mask；每量≤schema maximum及原profile全局界，公共input精确取该唯一profile硬界，output为parser选定且原profile合法的本次值。retail/supplier同名量语义相同，不允许变更单位换边。canonical/digests从行及完整Plan重算；retail rates必须逐byte等于原price完整canonical。`R=ReservationAmount`等于唯一customer reservation.amount，路由后不改R，0价也必须有真实快照/两维/预占/settlement。

请求 root→恰一份 snapshot 的 reverse complete trigger 必须在 COMMIT 检查；只建 snapshot→request FK 不能阻止少快照的 raw request 提交。快照及 bounds 行只能同 request 出生事务写入，之后 status/最终 pointer/PB正常收尾不改快照。

#### 5.1.1 用量界行（2026-11-04）

本片只落数量行，不落 `retail_bounds_canonical`、`supplier_bounds_canonical` 或报价外壳列。那些列仍按本节全文，等报价 canonical 片。

每个请求快照在出生事务写入恰好四行 `request_meter_bound_lines`：`retail`/`supplier` × `text_input_token`/`text_output_token`，单位固定 `token`。输入数量等于快照 `input_bound`，并且等于该档案两侧 capability 界的输入硬界。输出数量等于快照 `output_bound`，并且不超过该档案两侧的输出硬界。两侧同名维度数量必须相等。计划未就绪、`plan_canonical` 为空时，这四行仍然必须存在，不能因为没有计划而省略。计划已就绪时，计划函数已经要求计划内两侧界等于这两列；界行再要求等于这两列，从而与计划一致。本片不另加“对话输出界不得为 0”；该限制仍只在计划就绪时由计划函数执行。

每个尝试快照由 `meter_insert_attempt_snapshot` 在同一函数写入恰好四行 `attempt_meter_bound_lines`。数量等于该尝试快照的两列，也等于同一请求的请求界。U 不接受调用方另交更小的尝试界。

缺行、多行、单位不符、输入不等于档案硬界，或输出超过档案硬界，提交时分别拒绝为 `request bounds are incomplete`、`attempt bounds are incomplete`。行不可更新或删除。应用角色只有 SELECT/INSERT，投影角色和 PUBLIC 没有权限。预占金额等于向量 `ReservationAmount` 的复核仍不在本片。

#### 5.1.2 报价外壳（2026-11-04）

本片只落 inference 外层报价：请求快照的 `retail_quote_canonical`/`retail_quote_digest`，尝试快照的 `supplier_quote_canonical`/`supplier_quote_digest`。两列要么同空，要么 canonical 不超过 16384 字节且 digest 为 32 字节。不另存 `retail_bounds_canonical`、`supplier_bounds_canonical`、重复的 rates canonical，也不另存纯核 `metering.QuoteDigest`。外层 canonical 已经嵌进两侧界和费率原字节。

计划未就绪时，这两列必须为空。不能因为没有计划摘要而编一份报价。计划就绪时，请求报价是 known 的零售报价：`plan_digest` 等于请求计划摘要，`version_id` 等于价格版本且必须是非零 UUID，`unknown_reason` 为 null，bounds 等于该快照的零售界，rates 逐字节等于原价格的 `rates_canonical`。摘要域是 `moni-aigate.inference.quote.v1`。不一致拒绝为 `request quote does not match price`。

尝试报价绑定尝试计划。known 使用原成本版本和供应商费率；`version_missing` 的版本和费率都是 null；`basis_unknown` 保留原成本版本、费率为 null。零售侧不能是 unknown。不一致拒绝为 `attempt quote does not match cost`。报价列与快照一样不可更新。

#### 5.1.3 纯核界、费率与报价摘要（本片冻结）

计划就绪时，本片把报价外壳内部依赖的纯核原件也落库。请求快照保存 `retail_bounds_canonical`、`retail_bounds_digest`、`supplier_bounds_canonical`、`supplier_bounds_digest`、`retail_rates_canonical`、`retail_rates_digest` 和 `retail_meter_quote_digest`；尝试快照保存两侧 bounds canonical/digest、已知时的 `supplier_rates_canonical`/`supplier_rates_digest` 以及 `supplier_meter_quote_digest`。这些字段均为同一快照出生事务产生的不可变原件；不能由调用方提交另一份正文，也不能用当前 profile、price 或 cost version 回填旧行。

`*_bounds_canonical` 必须是对应 `metering.Bounds.CanonicalJSON` 的字节，`*_bounds_digest` 使用 `moni-aigate.metering.bounds.v1`；费率使用 `metering.Rates.CanonicalJSON` 和 `moni-aigate.metering.rates.v1`；纯核报价摘要使用 `metering.QuoteDigest` 的 `moni-aigate.metering.quote.v1`。三种摘要都采用 schema digest、side 和 canonical 的长度 framing。计划就绪的请求必须有 `retail_meter_quote_digest`；`supplier_meter_quote_digest` 只有尝试计划就绪、known 且两维费率完整时存在。

请求和尝试的 bounds canonical 必须从各自快照的四条界行和同一 schema 重建，并逐字节核对摘要。请求零售 rates 必须逐字节等于 price snapshot 的 `rates_canonical`；尝试 supplier rates 必须逐字节等于 cost snapshot 的 rates。任一 canonical、digest、side、schema、dimension、unit、rate 或绑定不一致，提交回滚并返回对应 snapshot mismatch 错误；不能降级为空、采用另一版本、按当前费率重算或把外层 `inference.quote.v1` 摘要当作纯核摘要。

计划未就绪时，计划、外层报价，以及本节所有纯核 bounds/rates canonical 和 digest 列必须同为 NULL，即使选中了 known 价格也不能单独写入这些列。原四条界行与原 price/cost version 绑定仍必须完整，不能因 canonical 为空省略原合同。计划就绪后缺任一必需列、canonical/digest 只有一侧、错 side/schema、错费率、纯核摘要错误，或计划未就绪却携带任一上述列，均拒绝整笔事务。supplier unknown 且计划就绪时保留两侧 bounds 和外层 unknown quote，supplier rates 与纯核 supplier quote 摘要为空。只在快照出生时按该就绪状态写入，后续不可补写或升级。

### 5.2 `upstream_attempts` / `attempt_meter_snapshots`

保留原attempt tenant/request/sequence/channel/version/credential/upstream_model、delivery_state、route_decisions、created_at及PB stamp。删除cost_version_id/cost_input_rate/cost_output_rate/cost_basis镜像列；原cost_status/cost_amount保留受约束的当前成本投影。增加latest_cost_finalization_id TEXT NULL、latest_cost_correction_id TEXT NULL；二者互斥，后者指原provider_cost_corrections同tenant/attempt原件，不能凭pointer造金额。correction父端增UK`(tenant_id,attempt_id,id)`以供复合FK；pointer与projection只能同事务更新，未知初态二者均NULL且amount=NULL。

#### 5.2.1 成本终态指针与投影（本片冻结）

attempt 出生时 `cost_status='unknown'`、`cost_amount=NULL`、`latest_cost_finalization_id=NULL`、`latest_cost_correction_id=NULL` 必须同时成立。可信终态由原 `finalization_records` 产生时，原子写入 `cost_amount`/`cost_status` 和 `latest_cost_finalization_id`，并清空 correction 指针；人工或受管采购更正产生时，原子插入唯一 correction 原件、写入 `cost_amount`/`cost_status` 和 `latest_cost_correction_id`，并清空 finalization 指针。两个指针始终互斥。

known 成本必须恰有一个指针，unknown 成本必须两个指针都为空且金额为空。finalization 指针只能指向同 tenant、同 attempt 的不可变 finalization；correction 指针只能指向同 tenant、同 attempt 的不可变 `provider_cost_corrections`。指针目标的金额和 basis 必须与 attempt 当前投影逐字段相等，不能只凭 ID 存在通过。插入 correction、更新 pointer、更新 cost projection 必须在同一事务中完成；operation 重放若正文不同返回冲突，不能改写原 correction 或切换指针。

供应商事实冲突、受管采购重新进入 unknown 或其他明确无权威成本的状态，必须同时清空两个指针和金额；不能保留旧 finalization 指针再把状态改成 unknown。缺 pointer、双 pointer、跨 attempt/tenant pointer、金额/basis 不一致、pointer 与 correction/finalization 缺边时，提交回滚。该指针只说明当前成本投影的原件来源，不创建第二份结算或采购账本。

`attempt_meter_snapshots`：

```text
tenant_id TEXT, attempt_id TEXT PRIMARY KEY, request_id TEXT, model_id TEXT
profile_version TEXT, schema_id TEXT, schema_digest BYTEA, variant_key TEXT
plan_canonical BYTEA, plan_digest BYTEA, parent_plan_digest BYTEA
retail_bounds_canonical BYTEA, retail_bounds_digest BYTEA
quoted_at TIMESTAMPTZ(6)
cost_version_id TEXT NULL, quote_status TEXT CHECK(IN ('known','unknown'))
unknown_reason TEXT NULL CHECK(IN ('version_missing','basis_unknown'))
cost_basis TEXT CHECK(IN ('contract','invoice','unknown'))
supplier_bounds_canonical BYTEA, supplier_bounds_digest BYTEA
supplier_rates_canonical BYTEA NULL, supplier_rates_digest BYTEA NULL
supplier_meter_quote_digest BYTEA NULL, supplier_upper_amount BIGINT NULL
supplier_quote_canonical BYTEA, supplier_quote_digest BYTEA
procurement_binding_id TEXT NULL, procurement_account_id TEXT NULL
created_xid XID8, created_at TIMESTAMPTZ(6)
UNIQUE(tenant_id,attempt_id), UNIQUE(tenant_id,attempt_id,request_id)
FK(tenant_id,attempt_id,request_id) -> upstream_attempts(tenant_id,id,request_id) deferred
FK(tenant_id,request_id) -> request_meter_snapshots(tenant_id,request_id) deferred
```

本片单独落库 `parent_plan_digest`。attempt 计划为空时，`parent_plan_digest` 必须为空；attempt 计划存在时，`parent_plan_digest` 必须是同一 `(tenant_id,request_id)` 请求快照的 `plan_digest`，长度为32字节，并且不能从 attempt canonical 中解析后自行重算或接受调用方另传值。attempt 的 `plan_digest` 与 parent digest 是两个不同身份：前者摘要 attempt Plan，后者精确指向原 request Plan。

写入 attempt snapshot 时必须在同一事务读取并绑定请求快照；跨请求、跨租户、缺失请求计划、parent digest 不等或只写一侧的 plan/parent pair 均回滚。请求计划未就绪时，attempt 也不能伪造 parent digest；请求计划已经存在但 attempt parent 缺失时，不能降级为“没有父计划”。旧历史前缀和自由版本号仍保持原拒绝边界，不通过补写 parent digest 获得 current 资格。

attempt父端新增上述复合UK；snapshot另有profile/schema复合FK。model/protocol/schema完整digest/variant及profile_version必须与原request相同，选中原route binding不能自带另一profile。两侧attempt bounds必须**精确等于**共同`requestPlan.ForAttempt(originalProfile)`结果；U只有唯一profile，所以两侧界自然等于公共原界。不能caller随意提交更小数字。parent_plan_digest精确指向原公共Plan；attempt Plan≤65536bytes，snapshot两侧canonical由它重建。增加`attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)`，PK`(attempt_id,side,dimension)`及带tenant FK到snapshot；恰完整两mask。纯域未来的收紧不作为本U异构路由发布资格，禁止从今天profile重算历史。

成本version若非NULL，须与实际channel/upstream_model/schema/variant同域。quote精确联合：known的unknown_reason=NULL、cost_version_id非NULL、basis为contract/invoice，计划就绪时rates/digest/数值quote全非NULL；version_missing时ID=NULL，basis_unknown时保留实际unknown原version，二者rates/digest/数值quote/U均NULL、basis=unknown。计划就绪时外层supplier_quote_canonical/digest即使unknown也非NULL，因为它绑定未知原因/原version/Plan；不得与缺失的纯核数值quote混淆。计划未就绪时按第5.1.3节所有canonical/digest同空，完整bound行与版本绑定仍保留。

未绑定受管采购时，binding/account两个字段同时NULL；已绑定时同时非NULL，并复合FK至现行 credential_procurement_bindings 的 `(id,credential_id,channel_id,account_id)`，其 credential/channel 由 attempt 精确核对。对受管采购、cost_first、任何单价 cap 和 unknown=deny，unknown snapshot 一律拒绝。对现行文本 manual/allow、无cap、无采购绑定，允许明确 unknown，不能借此适用于未来媒体。

known U必须独立从原 supplier bound/rates 重算。受管采购恰有同 tenant/attempt 的原采购 reservation，upper_amount=U、account/binding一致；未受管不得私建采购 reservation。snapshot→attempt 与 attempt→snapshot 都检查。当前报价在 RecordAttempt 必要锁等待后重读，并在 PrepareDispatch 的最后锁/DB决策时刻复验；不同则 not_sent 收尾，不新报价覆盖原snapshot，不自动重发生成。

## 6. 量、冲突、双边终态与更正

### 6.1 `metering_events` / `meter_event_quantities`

保留原id/tenant/request/attempt/source/source_event_id/happened_at/received_at与source UK，但将attempt升级为NOT NULL。删去Token专用normalized_usage和不再使用的schema_version，新增`profile_version TEXT/schema_id TEXT/schema_digest BYTEA/variant_key TEXT/plan_digest BYTEA`、`observation_canonical BYTEA`、`observation_digest BYTEA`、`retail_usage_state TEXT/supplier_usage_state TEXT`、`created_xid XID8`。usage state仅confirmed_final/provisional/missing/conflicting；状态由完整mask的量、source资格和受审profile规则计算，不接受调用方给个status就确认。

#### 6.1.1 inference Observation 原件（本片冻结）

`observation_canonical` 必须是 `inference.Observation.CanonicalJSON` 的完整字节，字段顺序、nullable quantity、来源闭集和安全元数据与 `inference.ParseObservation` 一致；旧的 `Usage` JSON、`input_tokens`/`output_tokens` 平铺对象不能继续写入该列。`observation_digest` 必须是 `inference.Observation.Digest` 的 `moni-aigate.inference.observation.v1` 身份摘要，计算时去掉 `observed_at`，但 canonical 原件保留观察时间。

当前数据库写入口从已锁定的 request/attempt snapshot 取 profile/schema/plan 绑定和已验证的 Finalization 元数据，生成唯一 canonical；不接受客户 HTTP 直接提交观察。source 只允许 `adapter`、`retrieval`、`gateway`、`approved_reconciliation`，且事件必须绑定同 request 的真实 attempt。相同 `(tenant,source,source_event_id)` 同正文重放返回原事实，异体只追加 conflict，不覆盖原 canonical/digest。数据库 deferred guard 会从事件列和两行 quantity 重新构建 canonical 与 identity digest，任一字节或摘要不一致即回滚。

增加`UNIQUE(tenant_id,id)`、`UNIQUE(tenant_id,request_id,id)`、`UNIQUE(tenant_id,request_id,attempt_id,id)`。request/attempt FK带tenant且检查attempt.request_id；不能靠两个独立tenant FK接受同租户另一请求的attempt。全部Observation必须有原真实attempt；source闭集为adapter/retrieval/gateway/approved_reconciliation。无attempt准入失败直接Release，可无finalization结算，不伪造event/attempt；人工核对也必须定位原真实attempt，不能清另一attempt消费。

新 `meter_event_quantities`：

```text
tenant_id TEXT, event_id TEXT, dimension TEXT, unit TEXT
status TEXT CHECK(IN ('confirmed_final','provisional','missing','conflicting'))
quantity BIGINT NULL CHECK(quantity>=0)
PRIMARY KEY(event_id,dimension)
FK(tenant_id,event_id) -> metering_events(tenant_id,id) deferred
CHECK((status IN ('confirmed_final','provisional') AND quantity IS NOT NULL)
   OR (status IN ('missing','conflicting') AND quantity IS NULL))
```

每event的行集合恰等于schema全部dimensions，不增共同wire/API没有的每维safe_reason或自由diagnostics map。异常收费原因只用共同观察的`unexpected_charge_reason`封闭联合：supplier_unmapped_charge仅阻supplier，unattributable_charge阻双方；approved_reconciliation永远不能确认supplier，即使共享量全final。不在某侧mask不等于缺失。共享维度仅一行事实，不为双方存两个不同数字；一侧独有维度missing不抹去另一侧完整量。真实quantity超界仍存，不剪成bound。

双边usage state逐字遵循共同 `Observation.State(side)`：先判该侧mask内conflicting，再判missing（含该侧未归属收费与人工supplier），再判provisional，最后confirmed_final；人工supplier有冲突时仍优先conflicting，其余不能越过missing。SQL/Go从这些行按相应mask重建 FinalQuantities 的 canonical/digest；只有共同State返回confirmed_final且协议认证关系无冲突时能生成该侧最终量。canonical等于纯核Quantities规范bytes，digest必须采用第2节明确的外层FinalQuantities framing。basis unknown 与 quantity missing 分开：有完整supplier量但没有known原价，成本仍unknown。

`metering_event_conflicts`保持独立不可变冲突原件，新增tenant及同tenant event FK，原normalized_usage替换为`candidate_canonical BYTEA/candidate_digest BYTEA`，≤32768，UK`(tenant_id,event_id,candidate_digest)`。candidate是通过共同结构验证但未被接受的观察，不直接收费。source身份仍用原`(tenant_id,source,source_event_id)`唯一槽，比较内容必须包含request/attempt/profile/Plan/schema/variant而非只量。按共同digest规则仅排除observed_at：同源同量同身份但重读时刻不同仍幂等；异量/身份/交付事实不同均冲突，不覆盖原event。不同source事件给出相同终态不重复计费用，累计快照不相加。

### 6.2 共用 finalization 原件与两个独立 pointer

`finalization_records`保留原id/tenant/request/event/input_digest/协议结局/transport/delivery/content_written/finish_reason/policy_version/created_at。新增`attempt_id TEXT`（NOT NULL）、`retail_usage_state TEXT`、`supplier_usage_state TEXT`、`retail_quantities_canonical BYTEA NULL`、`retail_quantities_digest BYTEA NULL`、`supplier_quantities_canonical BYTEA NULL`、`supplier_quantities_digest BYTEA NULL`。schema/profile/variant从event和原snapshot获得，不另存第三份镜像；input指纹必须含原event完整规范事实身份及协议/交付决策。finish_reason纳共同Observation，≤80安全UTF8，不能在finalization另存与event矛盾的值。

新增`UNIQUE(tenant_id,id)`、`UNIQUE(tenant_id,request_id,id)`、`UNIQUE(tenant_id,request_id,attempt_id,id)`。替换原单列event FK为完整`(tenant_id,request_id,attempt_id,event_id)`→metering_events对应UK。只有侧confirmed_final时，该侧canonical/digest同时非NULL且等于受资格检查后的事件mask投影及外层FinalQuantities摘要，否则同时NULL；approved_reconciliation的supplier量投影必须NULL。两个`*_quantities_digest`均不接受纯核Quantities摘要，不能因canonical相同而混用。

替换 request.latest_finalization 的单列FK为 `(tenant_id,id,latest_finalization_id)`→finalization `(tenant_id,request_id,id)`。attempt的cost pointer采用 `(tenant_id,request_id,id,latest_cost_finalization_id)`→finalization `(tenant_id,request_id,attempt_id,id)`。pointer更新必须锁原request/reservation或原attempt/account；不能用同租户别的finalization结算。

客户与供应商的允许事实：

| 原事实 | 客户唯一 settlement | supplier成本投影/019采购 |
| --- | --- | --- |
| retail完整、supplier缺量/缺原价 | 可按原政策结算 `min(raw,R)` | unknown，受管hold保留；不能自动填0 |
| supplier完整且原价known、retail缺量 | 客户pending或原no-delivery政策释放 | 独立按原supplier Rates算actual并更新采购 |
| 明确not_sent/认证无消费 | 原政策释放 | 原attempt明确0成本并释放U；不能借request级事实清另一attempt |
| 24h客户截止 | 唯一released_deadline；之后不可补扣 | 原unknown U不释放；后到supplier证据仍可独立收尾 |
| 同步结果完整但客户结算未知/失败 | 不发布未结算成功body | 原消费可能存在；不后台再发生成 |
| 真实量超界 | raw保留；charge≤R，waived=raw-charge | actual不截U，异常/暂停与原019政策一致 |

`settlements` 仍 `UNIQUE(request_id)`、唯一 journal，保留现行 refunded_amount单调允许列。finalization FK改为同tenant/request复合FK；charged_usage必须引用当时原request的受审retail终态，`raw=Amount(originalRates,retailFinal)`、`charged=min(raw,R)`、`waived=raw-charged`。released类依原政策raw/charge/waived均0；保留原因不能声称供应商0消费。finalization/event后来到达不能改写已提交settlement、重新held来源或重置Key/PB。

供应商投影的写入另有deferred exact validator：cost_finalization pointer的可信supplier量+原known Rates，或精确not_sent/no-consumption事实，必须与cost_status/amount相同；unknown时cost_amount=NULL。latest_cost_correction pointer须为现行provider_cost_corrections同tenant/attempt原件；受管采购还必须匹配双审proposal、原revision/evidence与预算事件。更正不修改原quote/量，不伪造“该金额可从模型rates重算”。后到旧usage不能覆盖已受权invoice/contract更正，冲突继续按原019 disputed/held/anomaly规则记录。

#### 6.2.1 当前结算读端的原向量与失败边界（2026-11-05 冻结）

客户结算只沿原 `request_meter_snapshots.price_version_id`、schema/digest/default 与价格组读取不可变价格 root 的完整 `rates_canonical/rates_digest`；供应商成本及采购实际消费只沿原 attempt 的 cost version、channel/upstream/schema/digest/default 读取原完整费率。不得调用 `meter_price_million/meter_cost_million` 把库存有理率反算成两价再用于实际金额，不得读取最新价格。这里直接读取原版本不是报价 fallback：即使计划未就绪、快照 rates 列按既定门禁为 NULL，原版本 FK 和完整费率仍是唯一数值原件，不能补写计划或快照。

读端先验证 schema canonical/digest、费率完整 mask、side、currency、canonical 逐 byte 与 rates digest；缺失、乱序、错 side/schema、非规范率或摘要不符均以 `invalid_meter_snapshot` 回滚本次资金事务，不能降级为免费、unknown、当前价或标量路径。actual 直接解析同 tenant/request/attempt 的 finalization 对应侧 `*_quantities_canonical`，只在该侧 `confirmed_final` 时计算；其 canonical 必须是完整、同 schema/side 的纯核 Quantities bytes。外层 final digest 仍由既有 DB guard 按 `inference.final.v1` 核验，不能当作纯核 quantities digest比较。

known 全零费率仍返回真实 0 actual；`basis_unknown/version_missing` 无数值费率，成本保持 unknown，不把客户完整量当供应商量。人工核对的 supplier state 为 missing，采购 hold 不释放；明确 not_sent/认证无消费仍按原政策记 0，不改变已有冲突/更正优先级。客户 raw 仍由 `metering.Amount` 对原完整向量求和后一次 ceil，charged≤R；供应商 actual 不截 U，超界异常继续沿原采购状态机。重复 Settle、截止释放、退款/来源与恢复不重发行为保持。

本片只替换实际结算的内部数值读端；准入的 pricing/routing DTO、预占入口及 HTTP/UI 发布指纹仍在后续切片，不将它们写成已完成。不改冻结研究/原型的宽标量 ABI，不新增金额公式、表、角色或报价身份。验收需包括 ready/unready、原价冻结、known 零价/unknown、完整双方量、单次 ceil、actual 超界和人工客户核对；在隔离当前 fixture 中令两种 scalar 反算函数拒绝调用后，上述结算仍须通过。

### 6.3 人工核对与平台 probe 的必要接线

`reconciliation_cases.proposed_usage`替换为`proposed_observation_canonical BYTEA NULL`与`proposed_observation_digest BYTEA NULL`，与state/requester/evidence等组成精确NULL联合。当前U片这两个proposal列保存绑定原request的完整retail `QuantitiesWire` 原件及 `moni-aigate.metering.quantities.v1` 摘要；它不是已经发生的inference Observation，审批时才由原attempt/profile/schema/variant/Plan和proposal向量生成source=approved_reconciliation的普通event/finalization。proposal不能伪造attempt或因人工共享量而确认supplier成本。审批仍双人，supplier永远不可Confirmed；24h后不再收费。审批快照不按今天价重算。

`platform_probe_attempts.normalized_usage`替换为nullable `quantities_canonical BYTEA/quantities_digest BYTEA`和独立`usage_state TEXT`，增加与原profile一致的schema_id/schema_digest/variant_key；完成probe验证同一纯核向量数值合同，但不构造缺失tenant/request/attempt的伪inference.Observation。confirmed_final时supplier Quantities规范bytes与纯核`metering.Quantities.Digest`非NULL，否则均NULL；此字段不使用inference.FinalQuantities外层摘要。协议状态和usage_state分开。probe保留独立平台资源及当前unknown采购口径，不创建客户request/reservation/settlement，也不塞进nullable tenant事件表。U只开放六文本probe；unknown成本不清零。未来纳入受管采购须另冻平台资金合同。

## 7. 现行采购、周期预算与来源合同保持

### 7.1 采购 U 原向量计算与独立复核（2026-11-05 冻结）

recordProcurementAttempt在原account/PB锁后仍按原政策复验同域当期报价及预期版本；U的数值只沿已出生的同tenant/request/attempt snapshot、实际attempt的channel/upstream、原schema/digest/default和known cost version完整域读取不可变Rates，并从该attempt的supplier bound lines构造完整Bounds。不得从request另复制界、从当前价换算U、调用meter_cost_million或经schemaAmount反构造每百万价。计划未就绪时仍用原版本与原界行，不能补写canonical列。unknown、错绑定、缺维/错unit、canonical或digest错误均失败；不产生免费U或选择fallback。此片不完成路由/预期CostQuote的向量DTO隔离，其当前标量接缝须在后续移除，不增加第二种在线报价或兼容选择开关。

Go数值原件先复核schema及完整supplier Rates的规范字节/摘要、Bounds的完整mask/side/schema/规范字节，再调用唯一metering.ReservationAmount；int64溢出为procurement_bound_invalid，坏原件为invalid_meter_snapshot，读不到原绑定同样失败。沿原同一个pgx.Tx回滚attempt、snapshot/界行、采购reservation/预算事件/PB与账户投影，原客户R/来源不受失败的新attempt影响；无attempt失败不伪造观察。现行标量重构helper schemaAmount/schemaReservation失去最后消费者后删除，不保留备用路径。

024新增meter_procurement_bound_matches约束触发器，挂procurement_reservations INSERT/UPDATE、DEFERRABLE INITIALLY DEFERRED、SECURITY INVOKER。它必须独立用原attempt snapshot/实际attempt/channel/upstream/schema/digest/default/known cost与恰两条supplier界及恰两条完整采购率调用SQL meter_token_amount，一次ceil后核0≤U≤MaxInt64且等于原upper_amount，不能只核held=upper或账户/event自洽。缺snapshot、unknown、错域、缺量、错单位、重复/额外维度、U偏低/偏高/溢出都拒绝；SET CONSTRAINTS该触发器IMMEDIATE在缺必要原件时也必须拒绝。子行与原价不可变/完整性guard继续独立执行，原U不可变且每次采购状态更新重验原U，不以今天价或最终actual改U。PUBLIC/projector/payment worker不获此meter函数权限，APP按既有meter_%闭包执行，未增加资金写权或根锁。

验收分别证明受限APP下向量数值读取/预占、raw自洽伪造U被DB拒绝、ready/unready/零价/input-only/溢出、原价跨account等待及新attempt失败回滚；不能将仍调用旧CostQuote的准入/派发说成完全禁scalar链路。55439周期预算等原专库套件仍按原边界，不以5432隔离专项替代。

### 7.2 管理端采购预占的原界读取（2026-11-05 冻结）

`GET /api/platform/procurement-accounts/:id/reservations` 与控制台一次切换，不再发行 `input_bound/output_bound`，不提供旧列或旧schema fallback。每行保留原身份、U/held/spent、state/anomaly、revision与时间，新增原attempt快照的 `profile_version`、小写hex `schema_digest`、`variant_key="default"` 和 `supplier_bounds`。后者直接使用纯核Bounds的wire：`{schema_id,version:"1",side:"supplier",bounds:[{dimension,unit,quantity}]}`；恰两条text_input_token/text_output_token，ASCII顺序、unit=token、quantity为非负int64规范十进制字符串。输入型协议的零输出界必须显式保留。schema digest只是原schema身份，不新增quote/plan身份，不向响应暴露canonical字节、HMAC、计划/报价digest、上游URL或凭据。

024新增只读 `meter_attempt_supplier_bounds(tenant_id TEXT,request_id TEXT,attempt_id TEXT) RETURNS JSONB`，SECURITY INVOKER、STABLE、固定部署schema+pg_catalog+pg_temp，PUBLIC撤EXECUTE；APP沿现有meter_%闭包执行，projector/payment worker不获权限。它在原tenant/request/attempt完整绑定及原schema/digest下读取supplier bound lines，核恰两条、schema supplier mask/维度/单位/范围与tenant一致，数量逐维等于原snapshot的文本界；缺snapshot/schema/attempt、错绑定、缺/额外维或错单位均报错。列表以采购reservation为根，tenant由原attempt PK关联取得（本片不声称已安装第7.1节尚待完整guard验收的采购tenant列）；缺关联不通过INNER JOIN静默丢行。整页失败映射原安全503，不输出半页、NULL/空界或零量替代。只读投影不计算金额、不加资金锁、不读取当前模型/当前价；原U和已裁定spent仍只读原采购事实。

计划未就绪时同样从已存在的原界行读取向量；不得回填快照canonical或把投影当作已存新canonical。原account筛选与(created_at,id)倒序游标/每页50条保持，排序/裁页后只读取候选行的向量，不因向量聚合改变分页基数。无记录和不存在账户沿现行空页语义；无权限先403，非法UUID/游标先400，不触发原界读取。

TS严格验证新向量的键/闭集/单位/顺序/完整性与int64字符串，拒旧两界字段、缺失/错side/错variant/schema digest；保留白名单投影不携任意额外秘密。界面按维度和unit显示原界，与金额分列，不转Number或从U反算量。专项须覆盖受限APP真实列表、两个账户/多个租户及attempt、50+1分页、原件不受新价/当前模型影响、计划未就绪和input-only的0输出、错误原件整页失败及权限；该局部验收不能替代完整六文本商业链或浏览器验收。

`procurement_reservations` 保留原 attempt PK/account/binding/U/held/spent/state/anomaly/revision/时间及020 PB stamp，增加 `tenant_id TEXT`，FK `(tenant_id,attempt_id)`→attempt快照与原attempt。删除 input_bound/output_bound；原界来自该attempt的唯一supplier snapshot，禁止复制一组新的可变界。`guard_procurement_reservation_identity` 改核tenant/attempt/account/binding/原U/created_at不可变，并验证snapshot identity。prepared/sending必须held=U、spent=0且无anomaly；unknown/disputed按原规则保守hold；settled/released的hold为0；released spent=0。真实known actual可超U并暂停账户，NUMERIC100投影不截int64。

`procurement_reservations`增加UK`(tenant_id,attempt_id,account_id)`；`procurement_budget_events`增tenant，FK该三列→reservation对应UK，精确金额/账户投影校验保留。证据身份表增tenant并带tenant/attempt FK；原资金更正与proposal金额仍为明确裁定金额，不变成rate出版。procurement_accounts.limit_amount、受控available计算、预算events及共享account锁继续019路径。向量quote不是账户余额，rate cap不是消费预算。

020的tenant/Key/supplier control、allocation、window、configuration和operation关系不改为按维度建账。customer PB只用R/charge，supplier PB只用U/actual；不得每维分别Admit或增加第二allocation。保留pb_request_stamp/pb_attempt_stamp/pb_procurement_stamp/pb_dispatch_guard、同事务出生要求、最后等待后的实际stamp和原deadline检查。新snapshot/子行写入不得把原request或采购source时刻提前到必要业务锁等待之前。

023全部source/lot/event/reserve/settlement/refund表及写入口不加“vector钱包”。R/charge进入其原同一个pgx.Tx：全部账户ID序→lot_id序；完成时沿原分配，退款逆原charged边。原source资格、零价空分配、非现金opening和payment worker七函数范围不变。customer RELEASE/消费退款都不释放supplier unknown U。

023的完整复核清单如下；本U未改变其数值/结构语义，不应盲目重写这13个函数或扩大worker闭包。若正式SQL因新parent FK而必须重建依赖，应逐名保留行为、invoker/search_path/PUBLIC撤权与固定源码pin，并另记差异：

- funding_journal_valid、wallet_opening_journal_valid：四来源dispatcher及非现金opening，不引用向量/模型/新价格。
- wallet_lot_balance_guard、wallet_lot_event_stamp、wallet_lot_valid：本金三桶、revision/同journal事务、genesis/事件和。
- wallet_event_valid：funding/reserve/finish/refund显式分派，payment worker仅进入funding，不给它reserve/finish/refund读取权。
- wallet_reservation_fifo_guard、wallet_reservation_valid：原R与FIFO/连续索引/原reserve事件；0价仍无lot分配。
- wallet_settlement_valid、wallet_refund_valid：原charge/release拆分及逆原收费边退款，不查当期rates/quantity。
- wallet_journal_valid、wallet_account_valid、wallet_source_fact_guard：原journal/钱包/来源双向闭合；trigger挂表集合仍是023原集合，不把新meter validator挂到所有payment可写共享表而逼worker读request。

APP四个实际写入口ApplyFundingTx/Reserve/finish/ApproveRefund与离线OpenWalletTx、worker的capture入口均须保留原权限；worker七个wallet函数恰为lot_balance_guard/lot_event_stamp/lot_valid/event_valid/journal_valid/account_valid/source_fact_guard，opening/FIFO/reservation/settlement/refund五个wallet函数不可执行。funding_journal_valid属于原支付闭包另核；projector对全部来源/向量/quote无权限。原payment_role静态函数pins只在实际被改源码时由人工更新，不能从待验DB反推expected。

## 8. 双向完整性、约束触发器与并发合同

正式SQL必须提供下列**双端**受控validator/trigger，名字在生成SQL前冻结；这里列出每个逻辑checker的完整责任，不用“数据库会检查”代替实现：

| checker | 必须被挂接的关系与拒绝内容 |
| --- | --- |
| schema complete | schema root与dimension INSERT；1..8、registry/unit、两mask、canonical/digest、U发行gate |
| capability complete | capability/variant/bound INSERT及models指针UPDATE；完整variant+两侧mask、原schema、协议结果与limits，原profile不可变 |
| route complete | policy/binding/cap子行INSERT、models.active_revision/profile pointer UPDATE；profile/variant、cap单位、supplier合同、精确不可变route集合 |
| retail price complete | price root/rate行/对应pricing operation INSERT；完整retail率、全局最坏R可表示、input-only 0输出、精确操作/结果/audit |
| supplier quote complete | cost root/rate INSERT；known完整与unknown无行，channel串行发布与同域时刻UK，supplier合同绑定 |
| request quote complete | request/snapshot/bound/reservation INSERT及request允许状态UPDATE；恰一snapshot、原route去重后唯一Profile闭包、原价/组/revision/时刻、公共两侧界、R与原reservation一致 |
| attempt quote complete | attempt/snapshot/bound/采购reservation INSERT及sending UPDATE；恰一snapshot、ForAttempt精确派生、同request、原当前quote/credential/binding、known/unknown联合、U及当前门禁 |
| event complete | event/quantity INSERT；恰schema全维、status/NULL联合、每侧完整状态、canonical/digest、同request/attempt/profile/schema/variant |
| finalization complete | finalization INSERT、request/attempt pointer UPDATE、settlement INSERT；精确event关联、侧量投影、客户唯一终结和供应商独立投影 |
| supplier projection complete | attempt.cost_*及pointer UPDATE、采购reservation/event/correction/proposal写入；受审原证据或受权裁定，账户/预算一致，禁止客户截止释放unknown |

CHECK/PK/UK不可deferred；循环root↔child定位FK和上述complete constraint trigger使用 `DEFERRABLE INITIALLY DEFERRED`。UPDATE/DELETE历史禁止trigger为BEFORE；子行append guard为BEFORE。COMMIT检查失败回滚原request/Key/PB/027来源/采购/audit/outbox全事务；`SET CONSTRAINTS ... IMMEDIATE`也不能在缺必要子行时通过。

新current写路径只允许READ COMMITTED，保留3秒锁/5秒语句/10秒总事务预算并取caller较短。读取不可变父后插新子仍需当前出生事务校验；可变父指针更新和子完整检查按统一根锁序串行，不能凭deferred查询在RR产生write skew。所有带可变引用的触发器在必要父锁等待后，以**新语句**重读而非复用锁前查询快照。不可变schema/价格本身不需要资金等待后再取前层写锁。

完整性函数执行 `SECURITY INVOKER`，固定 `search_path` 到精确部署schema+pg_catalog+pg_temp并显式限定业务关系，PUBLIC撤EXECUTE。不通过给projector/支付worker附赠客户权限来满足函数闭包。若将来确需新definer根，必须另列全writer/owner/EXECUTE闭包；本U草案不预授权。

### 8.1 本片补齐的独立金额与结构 guard（2026-11-05 冻结）

下列 checker 在 024 中补齐；均为 `SECURITY INVOKER`、固定 search_path、撤 PUBLIC，数值只用 `meter_token_amount` 对原行整数重算，不读 Go 结果、当前价或调用方金额：

1. **客户预占 R**：`reservations` INSERT（deferred）要求同 tenant/request 的请求快照存在，且 `amount` 恰等于原 `price_version_id` 的两条 retail 率行与请求 retail 两条界行的单次 ceil 金额，并 ≤ int64。零价得 0，仍需完整快照与界行。
2. **客户结算金额**：`settlements` INSERT（deferred）。`reason='charged_usage'` 时必须 `status='settled'`、有 finalization 且 retail 已 confirmed_final，`raw_retail_amount` 恰等于原请求价格率行乘该 finalization 事件的 retail 两维终态量（单次 ceil，不截断），`charged_amount=min(raw,原 reservation.amount)`，`waived_retail_amount=raw-charged`；其余 reason 必须 `status='released'` 且三项金额均为 0。
3. **能力版本完整**：`model_capability_versions`/`model_capability_variants`/`model_capability_bound_lines` INSERT（deferred）。每个 capability 恰一 `default` variant、恰四条界行（两侧×两维、unit=token），两侧输入界等于 profile `input_token_upper_bound`、输出界等于 `output_limit`，且不超过 schema 最大量；`protocol` 等于 profile 协议，input-only 协议输出界为 0；`contract_digest/profile_digest` 等于 profile 原 JSON 文本的 SHA-256。
4. **路由出生与 cap**：`model_bindings` INSERT 前要求所属 route 在本顶层事务出生；`binding_cost_rate_limits` INSERT 前要求所属 binding 在本顶层事务出生，维度/单位属于 route profile schema 的 supplier 维度，分子分母已约分。已提交的 route/binding 不能再追加子行。
5. **删除 route_revision=0 旁路（已落地）**：`request_records.route_revision` 必须 >0；Reserve 只接受等于模型当前非零 active route 的版本，否则 `route_changed`/`model_unavailable`；RecordAttempt、证据与成本策略不再对 0 跳过路由绑定与 cost policy 校验；请求快照的 route/profile 一致性检查无条件执行。模型可以已配置但未发布路由，此时不可调用。旧冻结研究材料中的自由版本/旧旁路不升级到024。

当前夹具若依赖"无路由也能建请求"，必须改为先发布真实路由（含所用渠道/版本/上游模型 binding）；不提供跳过开关。

### 8.1.1 route complete 的提交边界（2026-11-07 冻结）

route root、binding 和可选 cap 必须在同一个当前发布事务内组成一个可执行的不可变路由集合。deferred `route complete` checker 至少拒绝以下情况：

- route 在提交时没有任何 binding；不能先写空 route、再把模型的 `active_revision` 指向它，或把空 route 当作“暂未配置”留在历史中。
- binding 的 `channel_id` 与 `channel_version_id` 不相符，或 channel version 在出生时没有同 channel 的有效 credential 关联；binding 的 `profile_version`、`variant_key` 必须来自该 route 的唯一 capability/default。
- 已提交 route 上追加 binding 或 cap；可选 cap 仍只能是 supplier schema 的两条 token 维度、规范约分有理数，0 条表示无 cap，不能用半套 cap 改写既有 route。

该 checker 只验证当前 route 出生时的结构和供应商接缝，不把后续 credential 撤销或渠道轮换回写历史 route。任何失败都必须回滚 route、active pointer、binding、cap 和 audit，不能以空 route 或部分 route 进入当前选择器。

## 9. scalar撤除与 SQL/Go 依赖清单

正式追加迁移必须在一个事务中先验证基线/拒迁，显式卸下已知依赖，安装新关系/函数/约束，撤除旧列，重装权限，最后核完整模型。不得只改Go而让旧SQL guard仍以Token列放行。

| 撤除/替换的现行对象 | 同次必须改的依赖 |
| --- | --- |
| `models.profile`镜像、capability旧无schema合同 | catalog/modelSelect/lockPrincipal/probe/profile发布/匿名目录统一按profile_version读唯一版本；模型与active route一致性 |
| price input_rate/output_rate、旧price域UK/index | 018 guard_group_price_insert、pricing_price_result、pricing_fingerprint、assert_pricing_safe_input、guard_pricing_operation_insert、require_pricing_operation_result及相关trigger/CHECK/index；默认发布也纳入operation |
| request price/group/revision/priced_at镜像与四Token scalar | 018 guard_request_retail_snapshot/request_retail_snapshot及shape CHECK替换为唯一snapshot；Reserve/lockRequest/request明细/catalog报价/恢复/人工核对全部切向量 |
| cost input_rate/output_rate、旧cost域index/trigger | 013 synchronize_cost_version_insert及同刻谓词；catalog读报价、routing评分、RecordAttempt/PrepareDispatch复验都按完整域+完整vector |
| binding max_input/max_output_cost_rate | routing/cost policy/route发布JSON、DB限额children完整性和按有理数交叉乘积比较 |
| attempt cost_version/cost_input/cost_output/cost_basis镜像 | 原RecordAttempt INSERT/重放、cost policy、procurement/readHold/recordAttemptCost/成本更正投影；仅snapshot保留原quote |
| procurement input_bound/output_bound | 014 identity trigger/读取器/预占与finalize；替换成原attempt snapshot，020 supplier source及budget projection继续只读金额 |
| event normalized_usage/schema_version及conflict normalized_usage | evidence INSERT/源幂等/冲突/最终状态、Settle/recovery、人工approval、HTTP请求明细、报表/CSV/控制台量展示 |
| reconciliation proposed_observation_canonical/digest、probe normalized_usage | 各自严格向量wire和side合同、NULL联合、再认证/双审原权限；probe不转成客户请求 |
| 新current关系/函数/列 | operations.ProvisionFor及payment/periodic角色传递闭包、serve启动角色核验、迁移固定摘要、专项测试与统一架构 |

020现有SQL不直接引用上述Token scalar，但多个函数用 `%ROWTYPE` 和request/attempt/procurement结构，必须实际PG重建/执行验证，不因文本检索无命中宣称无依赖。023 source validator只依赖原R/settlement金额及身份，语义保持；须用真实商业flow确认同事务闭合。

冻结旧研究中的reader/materials含旧scalar是历史事实，不把它们接入serve，不在本片追加兼容profile或改签固定材料；如果普通构建误把新DDL当旧研究验收输入，测试fixture须明确原材料身份，不放宽现行启动/迁移检查。

## 10. 角色、列ACL与后台边界

角色名称参数沿现行装配；以下 APP/PROJECTOR/PAYMENT_WORKER/MIGRATION_OWNER指实际分离角色。U不创建尚无writer的MEDIA_WORKER，不将支付worker升级为模型worker。

| 对象 | APP | PROJECTOR | PAYMENT_WORKER |
| --- | --- | --- | --- |
| schema/capability/variant/价格/成本/route及所有费率/bound/cap子表 | SELECT+INSERT；历史无UPDATE/DELETE；model/pointer管理列按原admin入口受控 | 无 | 无 |
| request/attempt快照与bound子表 | SELECT+INSERT，同出生事务；无UPDATE/DELETE | 无 | 无 |
| metering event/量/conflict/finalization/人工核对原件 | SELECT+INSERT；仅reconciliation现行状态/审批允许列UPDATE | 无 | 无 |
| request root | SELECT+受限INSERT；仅status/billing_status/latest_finalization_id允许UPDATE，身份/报价禁止UPDATE | 无 | 无 |
| attempt root | SELECT+受限INSERT；仅delivery_state/cost_status/cost_amount/两cost pointer及现行允许时刻列UPDATE，身份与快照禁止UPDATE | 无 | 无 |
| 采购reservation/events/账户与PB | 现行APP所需列，新增tenant不可赋写变更；沿同一受约束账务入口 | 无 | 无 |
| 027来源、ledger/journal/funding | 现行权限不扩大 | 无来源权限 | 仅现行capture来源三表/余额允许列、ledger余额及已受审七函数；不得新增request/quote访问 |
| projection_outbox/receipts/report_totals/active pointer | 现行writer/读权限 | 只读安全outbox与所需投影表；receipt INSERT及totals允许列写；不得schema/量/报价写入 | 只保留现行credited写语义 |
| platform probes | 现行admin入口的受限INSERT/完成字段UPDATE；只允许当前文本profile | 无 | 无 |

SQL账户权限不区分APP内部的admin/customer handler；真正actor权限仍由原会话/Key/角色/再认证和锁后重验执行，DB trigger确保数据合同，不能声称APP的SELECT自动实现逐用户RLS。跨tenant API与member自己的request过滤必须逐入口测试。

MIGRATION_OWNER独占DDL、role provision、schema版本安装与停服显式opening；APP/PROJECTOR/WORKER均无CREATE、TRIGGER、TRUNCATE、BYPASSRLS、角色成员继承或owner权限。Provision不得用全schema blanket grant最终残留新表UPDATE；需撤表级/列级旧grant后按最终精确矩阵装配。启动核验新必要表、当前函数属性/固定search_path及有效ACL；保留现行角色分离，不导入冻结历史catalog/profile门禁。

U后台恢复仅观察/收尾原request/attempt及原证据；无网络生成许可，不能拿missing supplier quantity调用Provider再生成来“补量”。后续任务观察worker只能查询原task/制品并提交观察，其单独ACL/claim合同由A片定义，U不提前安装。

## 11. 拒迁、发布顺序与必须验收的边界

### 11.4 同步媒体结果交付存储

当前 MVP 的 inline 图片与 TTS 结果必须在同一租户、原 request、原 attempt 已完成结算后写入不可变的 `media_result_artifacts`。结果行包含 operation、media type、原始 bytes、SHA-256 和过期时间，主键为 `(tenant_id,request_id)`，并以外键绑定 `media_requests` 与 `media_attempts`。结果写入只允许 APP 在原结算事务完成后追加，任何 UPDATE/DELETE 和同一 request 的异体重放都拒绝。

结果读取按租户、owner user、API Key 与 request 或原 idempotency key 同时过滤。进程内缓存只作为已完成请求的短期加速，不作为结果所有权或结果存在性的依据。数据库不可用、结果摘要不一致或原 request/attempt 绑定不一致时返回服务不可用，不从其他媒体结果或旧缓存补回。结果 bytes 属于受保护的 inline MVP 存储，后续对象存储只替换交付介质，不新增结算或计量路径。

### 11.5 异步任务制品正文存储

`media_task_artifacts` 的 metadata 与正文属于同一不可变制品身份。任务成功事件先追加 metadata，首次授权下载在 provider 返回后只允许把正文从 NULL 写成一次性 `body`，数据库校验 `octet_length(body)=byte_length` 且 `sha256(body)=sha256`。正文写入后禁止替换或删除，重复下载直接读取已保存正文，不再次调用 provider。

新 worker 或 app 进程只从原 tenant、task、artifact reference、digest 和有效期读取正文。没有正文时才允许一次原 provider download，下载失败不改变任务或结算状态。重启不会因为 grant 丢失而丢失制品，客户端可以重新申请新的短期 grant；这条结果读取路径不创建新的 request、attempt、settlement 或收入。

### 11.6 Realtime provider identity 存储

`realtime_sessions.provider_session_id`、`realtime_turns.provider_handle`、`dispatch_unknown` 和 `cancel_requested` 属于当前执行事实。provider session/turn identity 首次写入后不可替换，unknown 与 cancel 标记只能向终态方向追加。Persistence 读取这些字段后，新的 process 只能继续原 Receive/Cancel/Settle 或保持 pending，不能调用新的 Open/Send 代替原事实。当前 mock provider 以 session/turn ID 派生可恢复 response，真实 provider 必须提供等价的稳定 handle 查询和取消合同后才能发布。

只支持独立新当前数据库。保留001–023原SQL与迁移checksum，新增迁移明确一次替换；迁移运行时不探测旧mode、不双写、不把旧request伪装成新snapshot。

迁移必须在服务/生成writer/worker停止、持现行maintenance gate和真实migration owner时执行；第一阶段在任何DDL前验证PG≥15、恰为正式023基线及所需依赖形状，获得相关表锁并检查下列拒迁条件。拒绝必须单事务回滚、原schema/数据/权限/checksum不变，输出固定可理解错误，不自动删库或生成转换记录：

- 任意既有request/idempotency/reservation/attempt/event/conflict/finalization/settlement/reconciliation/recovery/delivery/probe/cost-correction/采购proposal、采购reservation/event/evidence历史。
- 任意journal/journal_line/funding_receipt/wallet_opening/lot及其event/分配/消费退款、credit/refund业务历史；任意非零ledger余额、Key held/spent、采购账户held/spent或已发生PB allocation/event资金历史。不可只查余额是否0，已结清历史仍须拒绝。
- 任意需要改变含义的旧model/capability/route/binding/price/cost配置行；不能猜schema、supplier合同、variant或对旧费率悄悄补新身份。原pricing group/tenant assignment等不含scalar的配置可以保留，但price publish operation必须无行；默认/组旧价格必须无行。
- 原迁移/约束/函数依赖形状不符、有效owner/角色分离不符，或新关系名已被不明对象占用。不能通过忽略对象或CASCADE删除绕过。

不受变更影响的用户/租户/成员/空Key、独立安全设置、零采购账户配置和payment/voucher未入账配置不要求删除；其原约束保持。若保留的配置通过FK依赖被拒绝的旧model/price，则整体仍拒绝。明确推荐在独立空库按current安装→provision→bootstrap→发布受审文本profile/route/双侧价→显式opening或人工核款的顺序验收。

SQL安装资格至少覆盖以下实际PG原型，不能以纯核或fixture报告代替商业链路：

1. root/child缺、额外、错tenant/错request/错attempt/错schema digest/错unit、已提交root追加child、不可变UPDATE/DELETE、同ID异内容、0/1和missing/unknown对照，全部须COMMIT拒绝且原资金不变。
2. 两文本完整向量与独立旧公式的相同费用、input-only明确0输出、末尾单次ceil、known0与unknown、raw>int64/超界真实量保留、R/U溢出拒绝。
3. 默认/组域无回落、operation同意图重放与异体冲突、全rates指纹、scheduled价跨钱包等待保留原价、成本跨channel/account/credential等待复验、cap有理数交叉比与同ceil不同精确排序。
4. 实际APP角色从Reserve→027来源→最多2attempt/采购U→sending→受信event→customer finish及独立采购收尾→消费退款→026报表/CSV；注入任何必要写失败须全事务回滚。
5. retail已确认supplier unknown、反向组合、missing不可填0、同source冲突、已结算后supplier后到与人工更正、customer24h截止不释放supplier unknown、恢复不发生成及未知COMMIT原身份观察。
6. 实际PROJECTOR/PAYMENT_WORKER无新schema/向量/请求权限，APP仅允许列，PUBLIC与继承权限无旁路；原payment capture+027来源仍可闭合。
7. 拒迁负控分别对已结清历史、零余额但有资金原件、旧纯配置、坏依赖/ACL进行；失败前后schema/行/权限逐值相等。旧开发库保留，不把它删掉再称升级成功。

实施者先跑与变更相关的PG/Go/协议/前端检查，集成人汇总全部并行结果后，语义复核统一架构正文/能力状态/源码链接，再按AGENTS统一refresh和只读检查。本文是设计增量，统一架构状态必须仍写“028-U设计/未实现”，不能把候选DDL和媒体能力写为已完成。

### 11.1 当前目录与历史夹具的失败边界（2026-11-05）

路由发布和公共目录发布校验只从 `meter_model_profile` 读取当前不可变 capability；不回读已移除的 `models.profile`。公共 profile 超过16KiB时不把原件传回应用，发布拒绝，匿名读取返回安全 unavailable；私有未知字段不得进入公开白名单。测试只能在 capability 出生时放入这些反例，不允许 UPDATE 不可变历史或禁用 trigger。input-only 的非零输出价必须由当前 meter 发布入口拒绝，事务不能留下价格或 operation 半成品；没有价格时仍是 unavailable，不能报告为免费。

当前 HTTP、成本路由与派发夹具按024的 meter 函数写入价格、成本和模型；尝试报价从 `attempt_meter_snapshots` 读取。原013成本重复时刻/排序和018之前的坏价属于独立历史夹具，只验证原schema的SQL不变量。带历史数据的023升级024必须拒绝并保留原数据，不能为了测试调用当前reader而增加旧schema fallback。周期预算HTTP仅在专用55439/moni_test执行，其他环境只编译。

现行Gemini/Responses/Messages财务夹具只从quantity行断言归一化输入/输出；cached/reasoning的包含关系及原生字段由provider解析和协议wire验收，不从Observation中读取旧Usage字段，也不为了旧测试把诊断map重新加入财务原件。当前来源零价及成本等待夹具仍须验证原资金/锁等待语义，不能只改为能编译。

现行quantity消费者直接使用已验证的 `meter_event_quantity`；旧 `meter_event_usage` JSON读取外壳无剩余调用方，024不再发行该函数。Observation原件仍在原列中按inference合同读取，不提供返回旧平铺Usage的兼容接口。

### 11.2 当前 HTTP 读端的完整报价与标量投影撤除（2026-11-05 冻结）

剩余读端一次切换，随后 024 删除 `meter_price_million`、`meter_cost_million` 两个反算函数；任何 HTTP/TS 不再输出或解析 `input_rate/output_rate` 价格字段、`input_tokens/output_tokens` 用量字段。管理端**输入**仍为每百万 token 整数（发布与路由 cap 入口），这是单位换算，不是读端投影。

1. **零售价视图** `RetailPrice`（`/api/models/:id/retail-price`、`/api/models` 的 `retail_price`、平台定价预览行）精确为 `{model_id,source,status,price_group_id,price_version_id,schema_id,schema_digest,variant_key,currency,rates,effective_at,as_of}`。schema 三元组恒取该模型当前唯一 capability/default；available 时 `rates` 为 retail 侧完整 RatesWire，unavailable 时 `price_version_id/rates/effective_at` 同为 null。选价必须同时限定 model、价格组 NULL-safe 等值、`schema_id/schema_digest/variant_key='default'` 与 `effective_at<=as_of`，按 effective_at/created_at/id 逆序；另一 schema 的价不能被选中，组缺价不回落默认。
2. **模型列表**：客户 `/api/models` 删除顶层 `input_rate/output_rate`，只保留上述 `retail_price`；平台 `/api/platform/models` 以 `default_price`（同 1 的形状，source=default、price_group_id=null）取代两标量。
3. **价格版本列表** `/api/prices`：每行 `{id,model_id,model_name,price_group_id,schema_id,schema_digest,variant_key,currency,rates,effective_at,created_at}`。管理员读全部默认价版本（含历史 schema，各自携原 schema 身份）；客户只读本租户价格组域、已生效、且属于该模型当前 capability schema 的版本。
4. **公开目录** `/api/public/models` 及详情：价格对象以 `{status,price_version_id,schema_id,schema_digest,variant_key,currency,rates,effective_at}` 取代两标量，规则同 1 的默认域；不公开采购或租户信息。
5. **请求明细** `/api/requests`、`/api/requests/:id`：请求快照为内连接（每个请求恰一份），删除 `retail_snapshot_status`/`legacy`；以 `schema_id/schema_digest/variant_key`、`retail_rates`（原 price version 的完整 RatesWire）、`retail_bounds`（原请求 retail 界行的纯核 Bounds wire）取代两标量；用量以 `usage_status` 与 `retail_quantities`（原 finalization 所指事件的完整维度行 `[{dimension,unit,status,quantity|null}]`，未终结时为 null）取代 `input_tokens/output_tokens`。金额字段（reserved/charged 等）仍读原资金事实，不从向量重算。
6. **采购报价列表** `/api/platform/cost-versions`：每行 `{id,channel_id,channel_name,channel_revision,upstream_model,schema_id,schema_digest,variant_key,currency,basis,rates,effective_at,created_at}`，known 为 supplier 侧完整 RatesWire，unknown 为 null。

所有 `rates/bounds` 均直接取不可变原件 canonical（JSONB 只用于返回对象，维度数组顺序、单位、约分不重排）；读端不计算金额。TS 解析器严格校验键闭集、side、schema 绑定、维度顺序、约分、int64 字符串并保留大整数；页面展示每百万 token 价格时只做精确有理数显示（不能整除时显示分数），不转 Number、不从金额反算量或价。删除 `meter_price_million/meter_cost_million` 后，"禁两标量函数"类测试改为断言函数不存在。

#### 11.2.1 人工核对提交的完整客户向量

人工核对提交是当前读端标量撤除的一部分。`POST /api/platform/reconciliations/:id/resolve` 在 `action=settle`（服务端内部映射为 `propose`）时只接受 `retail_quantities`，其值必须是当前请求 schema、`side=retail` 的完整 `QuantitiesWire` 原件：schema identity、维度顺序、单位和非负十进制 int64 字符串均按纯核合同校验。请求体不得再包含 `input_tokens` 或 `output_tokens`；旧字段、缺字段、未知字段、重复键、错误 schema/side、缺维度、多维度、非规范数量、越界或溢出都拒绝，且不写入核对状态、证据、审计或流水。

服务端从原请求的不可变 schema 原件验证并保留向量 canonical，不读取今天的模型、价格或界行来补齐输入。`action=release` 与 `action=approve` 不携带客户量；批准只消费已经保存的 proposal。核对批准只确认客户 retail 量，产生 `source=approved_reconciliation` 的普通观察/终结事实；supplier 侧继续为 `missing`，其 canonical、digest、cost pointer 和金额全部保持 NULL。列表和回执使用保存的完整向量或其状态，不输出两列 token 标量。

### 11.2 028-S/A/R 当前最小存储扩展（2026-11-08）

本节冻结 024 中允许安装的最小关系，供后续 writer 使用。它只提供身份、绑定、幂等、状态和事件事实，不发布媒体模型、供应商能力、HTTP/WS 或账务入口。所有关系只接受新当前数据库，不能从 001–023 历史关系转换或回填。

`media_tasks` 保存异步任务主事实。主键为 `(tenant_id,id)`，并保存 `request_id`、`owner_id`、可空的 `attempt_id`、`model_id`、`profile_version`、`schema_id/schema_digest`、`variant_key`、`idempotency_key`、`submit_fingerprint`、`input_digest`、三组截止时间和 `max_artifact_bytes`。`request_id` 必须绑定同租户原请求，`owner_id` 必须等于原请求 owner 且属于该租户，attempt 一旦出现必须绑定同一 request/tenant。任务幂等键按租户唯一，任务状态闭集为 `prepared|submitting|queued|running|cancel_requested|submission_unknown|succeeded|failed|cancelled`，观察、取消、制品分别使用合同中的三个独立闭集状态轴。任务身份、原请求/attempt、profile/schema、截止时间和输入摘要出生后不可变。

`media_task_events` 保存任务观察事实。事件 ID 在 `(tenant,task)` 内唯一，sequence 在任务内唯一且为正数，事件 canonical 与 digest 必须一一匹配。事件 kind 只接受 A 合同列出的提交、状态、取消拒绝和制品过期事件，事件 tenant/task 必须匹配。重复同体由唯一身份识别，异体不能覆盖原事件。事件应用只允许合同状态转移，终态不能被覆盖。

`media_task_artifacts` 保存受控品引用、规范媒体类型、正字节数、SHA-256、过期时间和状态。artifact reference 在任务内唯一，制品只能绑定原任务，任务成功前不得出现可用制品，过期只改变制品轴，不改变任务终态。业务表不保存媒体正文。

`realtime_sessions` 保存 session 的租户、actor、API key、model、profile、route、规范 Origin、期限和 session 状态。身份出生后不可变，actor、key、profile、route 必须属于同一租户和 model。状态闭集为 `opening|open|draining|closed`。

`realtime_turns` 保存 session 内正数 ordinal 及唯一收费轮次，必须绑定同租户同一套 request、reservation 和 attempt 事实。文本协议使用 `request_records/reservations/upstream_attempts`，Realtime 媒体使用 `media_requests/media_reservations/media_attempts`，两套不能混接，且三者的 request/tenant 一致。状态闭集为 `reserved|sending|receiving|finalizing|pending|settled|released`，同一 session 同时最多一个非终态 turn。`realtime_events` 保存 session 内唯一 client event ID、可选 turn、闭集 type、规范摘要和 digest。heartbeat/status 不得绑定 turn，收费输入和 response 控制事件必须绑定当前 turn，跨 session/turn、重复异体、终态覆盖和错误状态转移均由数据库拒绝。

所有上述关系和 guard 固定为当前 schema 的 SECURITY INVOKER 路径，撤销 PUBLIC 对表、序列和函数的权限。APP 的 writer 权限由现有 provisioning 事务授予，PROJECTOR 与 PAYMENT_WORKER 不获得这些表或函数权限。writer 必须在同一事务中先写原 request/reservation/attempt，再写 task 或 turn，不能通过孤立媒体关系制造计量、钱包或结算事实。本片不声称 S/A/R 的 provider、worker、HTTP、WS、报表、部署和真实收费已经完成。

### 11.3 当前媒体商业事实（2026-11-08）

024 追加 `media_model_profiles`、`media_requests`、`media_reservations`、`media_attempts`、`media_observations`、`media_reservation_lots`、`media_settlements` 与 `media_settlement_lots`。profile 以不可变 `model_key + operation + profile_version` 发布，保存业务 unit、每百万 unit 整数费率、最大预占量和结果大小。金额由 `media_amount(quantity, rate_per_million)` 只在该商业入口做一次整数 ceil，预占保存 rate 和 quantity bound，结算只读取原预占快照。

媒体预占先锁 API key、客户 `AVAILABLE/HELD` 账户，再按 FIFO 锁 funding lot，写一个 `media.reserve` journal、lot reservation edges、held key 余额和 audit。结算锁原 media reservation 与账户，依据原 rate 和 confirmed observation 计算 charge，批量写 `media.settle` journal、lot finish edges、settlement、key spent/held 和 audit。unknown、missing、失败、取消和超界 usage 释放预占且 charged/raw 为零，供应商提交未知时 attempt 进入终态，恢复不得再次发送。媒体 lot event 已接入 023 wallet event validator 的同一 journal/lots 图，未创建第二钱包或第二账本。

图片和 TTS 当前 HTTP 主线在有数据库的 Server 上使用上述 Reserve/Attempt/Observe/Settle 入口。无数据库的纯 handler fixture 只验证协议和 provider 边界，不能视为商业验收。Realtime 当前在有数据库时为单 turn 使用 `realtime_turn` profile，并由 tenant-bound PostgreSQL persistence 写入 session/turn/event 与 provider identity 后收尾同一 media settlement。异步 video task 已通过当前 media reservation、attempt、observation 和 settlement 接入上述入口，制品正文也已持久化；真实供应商、对象存储、浏览器和目标部署仍不属于本地 mock 验收。

## 12. U 必须与后续片 DDL 分界

下表冻结的是 U 作为统一计量存储片时的 DDL 边界。当前分支已在不改变 U 计量合同的前提下追加并接通 028-S/A/R 的媒体、任务和实时最小存储。表中“后续”表示这些对象不属于 U 的计量生产项，不表示当前代码不存在。

| U 同次必须交付 | 留在后续 S/A/R 片，U 不安装 |
| --- | --- |
| 第3–6节的schema/维度、profile/variant/界、两侧rate lines、binding caps、request/attempt snapshots/bounds、event量及独立pointer | image/audio/video新registry维度、正式媒体profile/variant与codec/资源认证记录；S按具体profile扩现行通用关系 |
| 当前六文本scalar撤除、所有SQL guard/指纹/选价/报价/采购/恢复/HTTP/报表/UI接线 | `media_tasks/media_task_observations`、provider task reference/lease/poll/cancel/artifact expiry/result读取授权及callback去重/签名关系 |
| 019采购reservation去scalar、020 PB与027来源同事务、精确APP/projector/payment权限 | `realtime_sessions/realtime_turns`、客户端event ID/turn ordinal/response reference、WS票据单用消费与session/turn状态 |
| probe与人工核对改同一向量wire；保持其已声明资金边界 | 媒体观察worker角色/claim、异步任务结果制品身份/下载观察、实时会话资源/控制事实 |

后续片必须扩展同一current模型，不能新建media钱包/按维度扣款/异步第二结算表；也不能仅因为U预留了通用vector结构便放行没有完整数据/状态/worker权限合同的媒体调用。

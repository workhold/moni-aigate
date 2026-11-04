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

`route_policy_versions`增profile_version TEXT和created_xid XID8，FK`(profile_version,model_id)`→capability，绑定公开模型当前唯一profile。`model_bindings`只增variant_key TEXT和created_xid XID8，不增独立profile身份；全部binding沿该model/route原profile。原channel/version/credential/upstream及ContractDigest固定实际供应商合同，不建contract ID层。原UK`(route_policy_version_id,channel_id,upstream_model)`替换为含variant的UK。两root stamp均由DB覆盖以支持限额子行出生检查。保留不可变route/binding及cost_mode/unknown_cost_policy。U删除route_revision=0无route门禁旁路：可配置未完成，但不可调用。

新 `binding_cost_rate_limits(binding_id TEXT,dimension TEXT,unit TEXT,numerator BIGINT,denominator BIGINT)`，PK `(binding_id,dimension)`，FK→model_bindings。每行是 supplier mask 的合法维度及相同单位，分数满足第 2 节；允许无上限行或仅部分维度上限，**无行是无限额，0/1 是明确零上限**。binding root 已经不可变，因此子限额必须同事务完整出生，修改限额通过发布新 route。删除原 `max_input_cost_rate/max_output_cost_rate`。

## 4. 不可变价格与采购报价

### 4.1 `price_versions` / `price_rate_lines`

原 `id/model_id/price_group_id/currency/effective_at/created_by/created_at/pricing_operation_id` 保留；currency 固定 CNY，created_at/effective_at 必须 finite；移除 input_rate/output_rate。增加 `schema_id TEXT`、`schema_digest BYTEA`、`variant_key TEXT`、`rates_canonical BYTEA`、`rates_digest BYTEA`、`created_xid XID8`。新增 schema 复合 FK 和 `UNIQUE(id,model_id,schema_id,schema_digest,variant_key)`。

唯一报价域改为 `UNIQUE NULLS NOT DISTINCT(model_id,price_group_id,schema_id,variant_key,effective_at)`；当前索引按同域加 `effective_at DESC,created_at DESC,id COLLATE "C" DESC`。schema_id 决定 digest，不能靠不同 digest 绕同 schema 唯一性。相同 domain/group 的缺价是 unavailable，不回落默认。没有合法当期价不派发；完整全 0 费率是有效免费价。

`price_rate_lines(price_version_id TEXT,dimension TEXT,unit TEXT,numerator BIGINT,denominator BIGINT)`，PK `(price_version_id,dimension)`，FK→price_versions deferred。提交时行集合恰等于 root schema 的 retail mask；unit、规范率、canonical/digest 全相等。发布 root 与全部行在同一 pricing operation 事务，root/child 双端有完整性约束。对当前 profile/variant 全部合法上界重算 R 必须≤MaxInt64；Embeddings/Rerank 的 output rate 必须 0/1。旧 `immutable_price` 保持同语义，子行也不可变。

随后扩大同schema的profile/route界时也必须复核其可能选中的生效/已排期零售与known采购价的最坏R/U；不能因为价曾在较小profile通过而自动授予更大界。Reserve/RecordAttempt仍各自重算并拒绝溢出，是独立最后门禁；不把巨大上界截MaxInt64。

### 4.2 `cost_versions` / `cost_rate_lines`

保留 `id/channel_id/upstream_model/basis/effective_at/created_at`；删除 input_rate/output_rate。增加 `schema_id TEXT`、`schema_digest BYTEA`、`variant_key TEXT`、`currency TEXT CHECK(currency='CNY')`、`rates_canonical BYTEA NULL`、`rates_digest BYTEA NULL`、`pricing_operation_id TEXT`、`created_by TEXT`、`created_xid XID8`。schema FK同上；pricing operation FK deferred+UNIQUE，created_by FK users。rate/quote status不从NULL数字猜测。

`cost_rate_lines(cost_version_id TEXT,dimension TEXT,unit TEXT,numerator BIGINT,denominator BIGINT)` 同 retail 结构，行集合按 supplier mask。`basis IN ('contract','invoice')` 必须恰一份完整非 nullable 费率向量；`basis='unknown'` 必须没有子率，canonical/digest 同时 NULL。known 的 0 率要有 0/1 行，不能以无行表达。基于尚未认证合同时的“invoice”标签不自动证明允许派发。

报价域为 `(channel_id,upstream_model,schema_id,variant_key)`，增加同域+effective_at UK，索引同域+原三项确定逆序。沿现行channel共享报价语义，不加入凭据专价或新supplier合同身份；选中attempt另冻结实际channel/version/credential/upstream/profile合同。`synchronize_cost_version_insert` 仍先 UPDATE channel 与派发 SHARE 串行并递增 management_revision；同刻冲突改为完整域，不继续用旧 channel/model 两键误挡另一 schema/variant。发布沿原admin/CAS/audit，并以cost_price.publish纳入已有pricing_operations同一原件/指纹规则。

route 的逐维 cap 以 `quote.numerator*cap.denominator <= cap.numerator*quote.denominator` 精确比较。任一 cap、cost_first 或 unknown=deny 都要求完整 known quote。最高 priority 池先用完整 supplier bound 和 Rates 的**未 ceil 有理总费用**比较，再沿现行 weighted tie。不能逐维比后只取 input、按已 ceil U 排序或用零售价评分。

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

### 5.2 `upstream_attempts` / `attempt_meter_snapshots`

保留原attempt tenant/request/sequence/channel/version/credential/upstream_model、delivery_state、route_decisions、created_at及PB stamp。删除cost_version_id/cost_input_rate/cost_output_rate/cost_basis镜像列；原cost_status/cost_amount保留受约束的当前成本投影。增加latest_cost_finalization_id TEXT NULL、latest_cost_correction_id TEXT NULL；二者互斥，后者指原provider_cost_corrections同tenant/attempt原件，不能凭pointer造金额。correction父端增UK`(tenant_id,attempt_id,id)`以供复合FK；pointer与projection只能同事务更新，未知初态二者均NULL且amount=NULL。

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

attempt父端新增上述复合UK；snapshot另有profile/schema复合FK。model/protocol/schema完整digest/variant及profile_version必须与原request相同，选中原route binding不能自带另一profile。两侧attempt bounds必须**精确等于**共同`requestPlan.ForAttempt(originalProfile)`结果；U只有唯一profile，所以两侧界自然等于公共原界。不能caller随意提交更小数字。parent_plan_digest精确指向原公共Plan；attempt Plan≤65536bytes，snapshot两侧canonical由它重建。增加`attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)`，PK`(attempt_id,side,dimension)`及带tenant FK到snapshot；恰完整两mask。纯域未来的收紧不作为本U异构路由发布资格，禁止从今天profile重算历史。

成本version若非NULL，须与实际channel/upstream_model/schema/variant同域。quote精确联合：known的unknown_reason=NULL、cost_version_id及rates/digest/数值quote/U全非NULL、basis为contract/invoice；version_missing时ID=NULL，basis_unknown时保留实际unknown原version，二者rates/digest/数值quote/U均NULL、basis=unknown。外层supplier_quote_canonical/digest即使unknown也非NULL，因为它绑定未知原因/原version/Plan；不得与缺失的纯核数值quote混淆。bound本身无论报价是否known均保留完整原件。

未绑定受管采购时，binding/account两个字段同时NULL；已绑定时同时非NULL，并复合FK至现行 credential_procurement_bindings 的 `(id,credential_id,channel_id,account_id)`，其 credential/channel 由 attempt 精确核对。对受管采购、cost_first、任何单价 cap 和 unknown=deny，unknown snapshot 一律拒绝。对现行文本 manual/allow、无cap、无采购绑定，允许明确 unknown，不能借此适用于未来媒体。

known U必须独立从原 supplier bound/rates 重算。受管采购恰有同 tenant/attempt 的原采购 reservation，upper_amount=U、account/binding一致；未受管不得私建采购 reservation。snapshot→attempt 与 attempt→snapshot 都检查。当前报价在 RecordAttempt 必要锁等待后重读，并在 PrepareDispatch 的最后锁/DB决策时刻复验；不同则 not_sent 收尾，不新报价覆盖原snapshot，不自动重发生成。

## 6. 量、冲突、双边终态与更正

### 6.1 `metering_events` / `meter_event_quantities`

保留原id/tenant/request/attempt/source/source_event_id/happened_at/received_at与source UK，但将attempt升级为NOT NULL。删去Token专用normalized_usage和不再使用的schema_version，新增`profile_version TEXT/schema_id TEXT/schema_digest BYTEA/variant_key TEXT/plan_digest BYTEA`、`observation_canonical BYTEA`、`observation_digest BYTEA`、`retail_usage_state TEXT/supplier_usage_state TEXT`、`created_xid XID8`。usage state仅confirmed_final/provisional/missing/conflicting；状态由完整mask的量、source资格和受审profile规则计算，不接受调用方给个status就确认。

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

### 6.3 人工核对与平台 probe 的必要接线

`reconciliation_cases.proposed_usage`替换为`proposed_observation_canonical BYTEA NULL`与`proposed_observation_digest BYTEA NULL`，与state/requester/evidence等组成精确NULL联合。proposal必须绑定原request的真实attempt/profile/schema/variant/Plan，只能提出完整retail向量；不能伪造attempt或因人工共享量而确认supplier成本。审批仍双人并产生source=approved_reconciliation的普通event/finalization，supplier永远不可Confirmed；24h后不再收费。审批快照不按今天价重算。

`platform_probe_attempts.normalized_usage`替换为nullable `quantities_canonical BYTEA/quantities_digest BYTEA`和独立`usage_state TEXT`，增加与原profile一致的schema_id/schema_digest/variant_key；完成probe验证同一纯核向量数值合同，但不构造缺失tenant/request/attempt的伪inference.Observation。confirmed_final时supplier Quantities规范bytes与纯核`metering.Quantities.Digest`非NULL，否则均NULL；此字段不使用inference.FinalQuantities外层摘要。协议状态和usage_state分开。probe保留独立平台资源及当前unknown采购口径，不创建客户request/reservation/settlement，也不塞进nullable tenant事件表。U只开放六文本probe；unknown成本不清零。未来纳入受管采购须另冻平台资金合同。

## 7. 现行采购、周期预算与来源合同保持

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
| reconciliation proposed_usage、probe normalized_usage | 各自严格向量wire和side合同、NULL联合、再认证/双审原权限；probe不转成客户请求 |
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

## 12. U 必须与后续片 DDL 分界

| U 同次必须交付 | 留在后续 S/A/R 片，U 不安装 |
| --- | --- |
| 第3–6节的schema/维度、profile/variant/界、两侧rate lines、binding caps、request/attempt snapshots/bounds、event量及独立pointer | image/audio/video新registry维度、正式媒体profile/variant与codec/资源认证记录；S按具体profile扩现行通用关系 |
| 当前六文本scalar撤除、所有SQL guard/指纹/选价/报价/采购/恢复/HTTP/报表/UI接线 | `media_tasks/media_task_observations`、provider task reference/lease/poll/cancel/artifact expiry/result读取授权及callback去重/签名关系 |
| 019采购reservation去scalar、020 PB与027来源同事务、精确APP/projector/payment权限 | `realtime_sessions/realtime_turns`、客户端event ID/turn ordinal/response reference、WS票据单用消费与session/turn状态 |
| probe与人工核对改同一向量wire；保持其已声明资金边界 | 媒体观察worker角色/claim、异步任务结果制品身份/下载观察、实时会话资源/控制事实 |

后续片必须扩展同一current模型，不能新建media钱包/按维度扣款/异步第二结算表；也不能仅因为U预留了通用vector结构便放行没有完整数据/状态/worker权限合同的媒体调用。

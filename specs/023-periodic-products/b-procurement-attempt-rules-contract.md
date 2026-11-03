# B-P4-A0：原 attempt 输入、路由编码与报价规则叶子合同

2026-11-04；审查基线 `1d457262bfd76832dba4175d93fed612e8c2a03b`。**状态：已审定冻结并实现，原 Go / actual APP 专库差分已通过；只完成本页 A0 叶子。** 承接[完整 attempt 出生根](b-procurement-attempt-birth-writer-contract.md)、[原身份 ABI](b-procurement-identity-contract.md)与[执行/错误合同](b-procurement-execution-contract.md)。本页先于独立实施冻结；PG 由根串行执行。实际范围、失败与修正见[验证记录](../../docs/verification/procurement-attempt-rules.md)。

## 1. 范围与真实依据

新增独立材料包 `internal/procurementattemptrules`，提供固定模板、版本/SHA和材料读取，不安装、不访问运行数据库、不授予权限。SQL 包含四个纯规则函数和一个只读选价函数；普通 billing/main/profile 不自动 import。不得改已有 identity 六函数/模板/SHA、原账户材料、32表715列、40原件482列、旧迁移或旧 Go 业务实现。

依据为实际 [RecordAttempt/readAttemptChecks](../../internal/billing/evidence.go)、[route validator](../../internal/billing/route_decisions.go)、[billing validID/boundedString](../../internal/billing/store.go)、[Decision 声明](../../internal/routing/routing.go)、[CostQuote.Equal](../../internal/routing/cost.go)、[首次报价与政策消费](../../internal/billing/cost_policy.go)和[managed 等待后再检查](../../internal/billing/procurement.go)。原 UUID 库精确为 `github.com/google/uuid v1.6.0` 的 `Parse`，不能换成其 `Validate` 或 PG UUID cast。

本片只解除 A0 规则/字节/实际采时的前置，不实施 request 出生、attempt/p hook、完整公开根、supplier 效果、guards/validators、ACL/profile 或 adapter；不关闭 D1/I1/B-P3/R2、历史观察、纯核 Complete/Qualified 或商业/生产验收。

## 2. 固定材料与五个签名

包公开 `Version=1`、固定 `MaterialSHA256` 及 `SQLTemplate() string`；模板唯一 schema 占位符为 `{{MONI_SCHEMA}}`，由受信调用方安全引用后一次替换，不接受 caller SQL。新增 `materials/attempt-rules-v1.sql`，不拼接或重发 identity 正文。

| 函数 | 精确参数与返回 | 属性/NULL规则 |
| --- | --- | --- |
| `pp_at_uuid_parse_v1` | `(value text) RETURNS boolean` | IMMUTABLE/PARALLEL SAFE/INVOKER；NULL返回false |
| `pp_at_route_json_v1` | `(decisions jsonb, selected_channel text, selected_version text) RETURNS bytea` | IMMUTABLE/PARALLEL SAFE/INVOKER；显式校验NULL，不用STRICT吞成NULL |
| `pp_at_expected_shape_v1` | `(has_expected boolean, expected_version bytea, expected_input bigint, expected_output bigint, expected_basis bytea) RETURNS boolean` | IMMUTABLE/PARALLEL SAFE/INVOKER；始终返回非NULL布尔 |
| `pp_at_quote_equal_v1` | `(has_expected boolean, expected_version bytea, expected_input bigint, expected_output bigint, expected_basis bytea, actual_version text, actual_input bigint, actual_output bigint, actual_basis text) RETURNS boolean` | IMMUTABLE/PARALLEL SAFE/INVOKER；始终返回非NULL布尔，无消费时不调用 |
| `pp_at_current_quote_v1` | `(channel text, upstream_model text) RETURNS TABLE(quote_selected_at timestamptz, cost_version_id text, input_rate bigint, output_rate bigint, basis text)` | VOLATILE/PARALLEL UNSAFE/INVOKER；准确一行，含无quote真实采时 |

类型全部 `pg_catalog` 限定，函数内调用、运算符和业务表均准确 schema 限定；无 dynamic SQL、SET、definer、扩展、DML或锁。全五函数撤销 PUBLIC EXECUTE，不 GRANT 给 APP/W/其他角色。选价所需调用方 SELECT 仅涉及 cost_versions 的 id/channel_id/upstream_model/input_rate/output_rate/basis/effective_at/created_at；本片不装配这份权限。

route 函数复用受信同 schema 原 `pp_json_string_v1(text)`，其安装/摘要/EXECUTE 依赖必须显式存在；不复制改写它、不用 JSONB::text 的空格/顺序冒充 Go JSON。其他纯函数不调用业务 writer/checker，不读表。

## 3. UUID 接受集与原文字身份

`pp_at_uuid_parse_v1` 精确保留原 `validID` 的成功集合：

1. 32字节纯 hex；36字节 `8-4-4-4-12`；45字节大小写折叠的 `urn:uuid:` 加36字节。
2. **38字节时原 Parse 只检查中间36字节，不检查首尾为花括号。** `{UUID}`、`xUUIDy`、首尾可表示ASCII控制符等均保留；不能改用 `uuid.Validate` 的更窄括号规则。
3. hex 接受大小写；nil UUID、任意 version/variant 均接受；不 trim、不统一大小写、不输出规范化身份、不检查当前业务存在性。
4. SQL TEXT 等价域仅为 PG UTF8 可表示且无NUL。原 Go 可暂持有但最终 PG TEXT/JSONB 无法持久化的 NUL/非法UTF8，不伪装成 SQL 规则成功；driver/JSONB 输入错误仍是原传输错误，不能拿 ignored BYTEA 的范围扩散给真实身份。

原 route channel 去重及 selected 的 channel/version 匹配均使用**原字符串字节**；大小写/URN/包裹不同但解析为同UUID的字符串仍是不同文字身份。不得 cast成 UUID 去重/比较。SQL 文字比较必须使用确定的字节语义，不能受 nondeterministic collation 改变。

RecordAttempt 本身只在 route 各 Decision 的两个 ID 调用 validID；不凭本 helper 给 tenant/request/channel/credential 等原方法未主动执行的检查增加拒绝。它们的真实定位/成员检查仍由后续完整根承担。Sequence=1..2、UpstreamModel=1..200字节的原输入检查仍由完整根按原顺序实现，不把本包变成新通用请求 validator。

## 4. 五字段路由的真实编码与判错顺序

### 4.1 Typed 集合与数值

JSONB 顶层必须数组，SQL NULL/JSON null/其他类型拒绝。空数组准确返回 UTF8 bytes `[]`，不要求 selected，也不因 selected_channel/version 为NULL额外拒绝空数组；完整根的必填输入另核。非空最多1000项，每项为恰五键对象 `channel_id/version_id/priority/weight/reason`，三文本必须非NULL JSON string，两数必须 JSON number。缺/额外键、NULL、数组/嵌套对象/错类型为 PP101。

priority 精确覆盖部署64位 Go int 的 `−9223372036854775808..9223372036854775807`；weight 精确为 `1..18446744073709551615`。先核 numeric 范围及整数表示再 cast，weight 不 cast bigint。JSONB 数值输出文本须为规范十进制整数；JSONB 已归一成整数的指数输入无法再区别原词法，不声称恢复原输入词法；保留 `.0` 的 numeric 不接受。最终输出总是 Go 的规范整数，无引号/加号/前导零/指数。

逐数组原顺序检查：先两ID Parse、channel原字节去重、weight非0；再reason；selected时立即核与原 attempt channel/version 相同。遍历后非空数组必须恰一个selected；不以对象字段顺序改变原行级顺序。直接 SQL 的结构/类型拒绝是 typed Go 输入之外的协议防护，不新增可提交业务状态。

20个 reason 恰为：`selected`、`weighted_not_selected`、`lower_priority`、`disabled`、`already_tried`、`capacity_full`、`quarantined`、`circuit_open`、`half_open_busy`、`channel_quarantined`、`credential_revoked`、`binding_isolated`、`region_forbidden`、`cost_unknown`、`cost_limit_exceeded`、`higher_cost`、`procurement_paused`、`procurement_budget_exceeded`、`procurement_bound_invalid`、`procurement_budget_unavailable`。

### 4.2 字节、资源与 SQLSTATE

输出无空白，数组顺序不变，每对象键顺序固定与 `routing.Decision` 声明一致：channel_id、version_id、priority、weight、reason。三个文本逐个调用原 pp_json_string_v1；返回整体 `convert_to(...,'UTF8')`。计算**这份完整 Go JSON bytes（含两侧方括号和逗号）**是否超过262144；不得用 caller自报长度、jsonb::text长度或字符数替代。1001项先按原 too-many 失败，不为编码分配无界数组。

原完整typed约束事实上使合法1000项字节上界低于262144（保守≤234001；38字节UUID的两侧控制字节转义计入）。仍保留原262144比较，测试不能伪造一条不满足UUID/reason/1000项的262144正例宣称原接受边界。原输入的额外空白/对象键序不影响 Go重编码后的字节。历史 JSONB transport 的272144B读集界是独立合同，不拿它扩张业务界，也不对整个输入JSONB的格式空白另设262144拒绝。

本函数仅抛 `PP101`，Message按原固定分支保留：`too many route decisions`、`invalid route decision`、`invalid route reason`、`selected route mismatch`、`route needs one selected candidate`、`route decisions too large`。直接结构/型错归 `invalid route decision`；NULL选中目标只在非空selected比较时归 mismatch。原 Go 事务外 validator 保留，因此正常 Go API 原不同Message先由原方法返回；SQL adapter不能从开放Message推导Code，仍精确按PP101映射invalid_attempt。原 PostgreSQL输入解析/权限/取消错误不被吞为PP101。

## 5. Expected BYTEA 联合与按需 Equal

`pp_at_expected_shape_v1`：has=false iff 四expected均SQL NULL；has=true要求version/basis两个BYTEA非NULL，两rate各自可NULL；has=NULL返回false。true的version/basis允许空bytes、NUL、非法UTF8和任意bytes；rate保留完整int64包括负值。它不强制 Known、UUID、basis枚举、非负或新的65536/16MiB限制。非法联合在完整根按 `PP903` 协议错误拒绝，不把正确ignored quote误作invalid_attempt；本纯布尔函数自身不抛PP错误。

`pp_at_quote_equal_v1` 先核联合，has非true/联合非法返回false。实际成本无版本时 actual_version SQL NULL 映射 Go `quoteOf` 的空VersionID；actual_basis 必须非NULL（无quote查询已返回unknown），否则false。把实际version与basis的原 TEXT 用 `convert_to(...,'UTF8')` 后与expected BYTEA精确比较；nullable rates用原nil相等/同时非NULL逐int64值比较。不 trim、不Known、不把NULL rate当0，不decode/转TEXT/JSON化expected，不向身份/来源/日志写这些ignored字节。

完整根只在原 `procurement binding` 或 `policy.Managed(maxIn,maxOut)` 的实际消费分支调用 Equal。manual/unmanaged与RouteRevision=0的原ignored路径保持忽略；有采购绑定时仍先按原完整known dimensions/basis检查，再Equal。Equal=false由调用阶段准确映射原 PP411（before attempt）、PP111（configured before dispatch）、PP412（before reservation）；完整/缺维度/CostExclusion仍按原各阶段 PP110/PP413/PP414/PP415及PP112，本叶子不重新排序或合并这些分支。

两bytea不增加业务长度界，避免拒绝原忽略的大值；只检查NULL联合，消费时对实际短UTF8 quote做二进制相等，不把大bytes先解码/展开。PG自己的statement_timeout/取消和消息资源上限保持，本包不续时或把57014包装成功。

## 6. 一条真实时钟选价语句

`pp_at_current_quote_v1` 的正文固定为一条 SQL statement：`WITH instant AS MATERIALIZED (SELECT pg_catalog.clock_timestamp() AS at)`，从 instant **LEFT JOIN LATERAL** 至同channel/upstream的唯一候选；过滤effective_at≤该同一at，按原 `effective_at DESC, created_at DESC, id DESC LIMIT 1`。保持原DB的id排序语义，不擅自加COLLATE或以传入时间替代。

外层始终从instant返回一行，第一列即这个真实at；有quote保留其原 id/nullable rates/basis，无quote三成本列NULL、basis=`unknown`，不制造版本或零费率。该at同时写未来 upstream/AttemptOrigin，是实际SQL采时，不是检查前Go时间、now()/事务开始时刻或另一条后采语句。查询不接 caller时钟，也不把后来目录+时间过滤写成完整历史absence资格。

调用前channel同步锁、managed等待后重查及完整5秒statement界由完整根负责；函数不自取锁，不合并其他原checks、不扩大重试，不能拿它取代等待后新statement。NULL selector只产生一行noquote+真实采时，完整公开根仍按其原必填/定位边界先检查。函数无自定义PP错误；真实SQL/权限/57014等原样失败。

## 7. 独立原 Go 差分与交付边界

代理独占本包SQL/纯材料/静态测试；根独占 **test-only** `internal/billing/procurement_attempt_rules_postgres_test.go`（package billing）与真实PG执行，使oracle能直接调用真实私有 `validID/validatedRouteDecisions`，不能复制validator然后自证。不改billing业务源码/导出API。quote expected以真实 `routing.CostQuote.Equal` 为oracle；Go标准json.Marshal(routing.Decision)核逐字节，不能从新SQL反推期待值。

差分覆盖：四UUID形态/38任意包裹/大小写/nil/version/variant/错误长度与separator/非ASCII；raw channel同UUID不同字节；20reasons、空/1000/1001、两个selected/无selected/错配；priority两端、weight上半uint64和最大值/0/超范围；控制/HTML/2028/2029及字面反斜杠编码；额外键/NULL/类型/小数与JSONB归一边界；ignored expected空/NUL/非法UTF8/大bytes/负或NULL rate及消费时不等；Noquote/partial/unknown/0rate、相同effective按created/id稳定、未来quote不选、真实采时前后界和单statement一行。

PG测试仅在独立 `PROCUREMENT_ATTEMPT_RULES_DATABASE_URL` 显式专库开关执行，根统一串行运行；普通纯Go验证显式去掉 `TEST_DATABASE_URL`，不意外跑整个billing商业PG。随机schema与actual受限APP LOGIN、无SET ROLE，原identity材料先按固定摘要安装为依赖；精确检查五函数属性、PUBLIC无EXECUTE、无DML/隐式安装。缺PG开关明确SKIP，不计差分PASS。

根冻结后代理只实现本新包的SQL/材料/静态测试，不接公共业务/billing测试/PG/架构/Git。根汇总纯材料、实际PG差分、依赖及源码链接后更新统一架构/HLD/LLD和验证记录，执行reviewed refresh、只读检查与最终推送。本片PASS只写规则材料已验证，不写完整attempt root已完成。

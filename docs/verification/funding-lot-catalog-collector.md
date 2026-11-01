# 022-C0 静态 collector 与固定期望装载专项

2026-11-02。本记录只证明新叶子包的 collector、封闭解析合同与 raw pre-deparse 拒绝边界。发行 API、同事务 proof、八角色 peer 闭包的最终集成由对应验证记录汇总；本记录不代表 maintenance Permit、epoch、资金来源图、生产迁移或运行 main 已验收。

## 实现边界

代码位于 [collector.go](../../internal/fundinglotcatalog/collector.go)、[固定查询](../../internal/fundinglotcatalog/collector_queries.go)、[raw preflight](../../internal/fundinglotcatalog/collector_preflight.go)、[封闭字段和值合同](../../internal/fundinglotcatalog/descriptor_types.go)、[严格解析](../../internal/fundinglotcatalog/descriptor_validate.go)、[固定期望装载](../../internal/fundinglotcatalog/expected.go)。只依赖标准库、pgx 和包内固定 metadata，不导入 billing/payments/operations，不发模型请求，不修 ACL，不写业务或临时表。

旧离线 exporter 七个源文件未抽取、共享或修改。新 collector 是一次性转移后独立收紧；固定 SQL 使用 pg_catalog 对象、类型、函数与 OPERATOR，排序显式 USING pg_catalog.<。qualified int2/int4 使用真实 catalog 类型名；多数组展开使用 ROWS FROM 三个独立 qualified unnest。

APP、Mock、Receiver 复用同一完整收集器。外层逐行流式收集，只有固定单条记录的子集合允许有界聚合，输入保留 LIMIT+1；无整 catalog JSON 聚合。开始 deparse 前先流式核九类 raw metadata 的行与存储文本总预算，才允许后续 CTE；原节点、属性、参数、ACL 等逐项检查大小。当前要求为空的 operator/cast/collation/conversion/opclass/opfamily/policy/rule/extension/statistics/text-search 类在任何表达式 deparser 前直接拒绝。

用户 type 仅允许 PostgreSQL 自动 composite/array，校验对应 relation/element 和固定 record/array I/O、analyze/subscript。表达式相关对象的类型依赖只可终止于受审 builtin 或已核安全的自身 schema 自动类型，因此自定义 base/domain/enum/range 等不能在检查前通过 pg_get_expr 触发任意 output 函数。raw pg_node_tree::text 只读取存储节点文本。固定 builtin registry 为已审 catalog profile 的 19 个 type 和 31 个 ObjectRef，pg_identify_object 的返回身份也逐项精确比对；runtime 不从 live 库扩充 registry。

实际 deparse 原字符串逐字比较。只有静态期望里受登记的 deparse 字段允许 literal/schema oneof 模板，schema 由当前连接的 pg_catalog.quote_ident 输出渲染；prosrc、SQL literal、任意实际字符串均不替换。原 prosrc 保留 UTF-8 字节及 SHA-256。

封闭合同同时核 envelope、record、引用、TypeRef、属性、ACL、proconfig 与模板的字段及值类型，拒绝未知 key、非规范数值、NUL、非法 UTF-8、深度或大小超界。expected/外部候选使用 token parser，额外在 map 构造前拒绝重复 key。只有固定 SQL registry 构造的 PostgreSQL jsonb 输出使用 UseNumber decoder 与 EOF 检查：jsonb 本身无重复键，每一处 jsonb_build_object 的 keys 都由纯测试审计为唯一静态 literal，用户业务 JSON/期望文件不能进入该路径。此后仍逐树执行资源和 scalar 检查、normalize 及封闭字段验证。numeric 不接受 exponent、-0、前导零及小数尾零。依赖多重数和符号双射单独校验。

## 资源与固定期望

| 边界 | 上限 |
| --- | ---: |
| 全局 catalog 行 | 100,000 |
| 每 category | 固定受审 max+1，溢出行 Scan 前拒绝 |
| raw 加 emitted 总 bytes | 64 MiB |
| 单 emitted record、单 raw text | 各 64 KiB |
| 属性及其他 relation 子集合 | 1,600 |
| 函数参数 | 100 |
| raw ACL cardinality | 256 |
| ACL / 其他子查询输入 | 257 / 1,601（LIMIT+1） |
| JSON depth | 64 |
| runtime 单 record JSON nodes | 1,000,000 |
| 固定 expected envelope JSON nodes | 4,000,000 |
| 所有发行步骤共享 deadline | 5 秒或 caller 更短期限 |

固定 envelope 的 4m node 界单独适用，未放宽 runtime 界。每个 mode/scope/family/category 的限额只由已认证 embedded root 推导，普通模式取五变体最大值，0 行类 LIMIT 1 首行拒绝；依赖是 multiset，raw 允许数为受审 multiplicity 总和，normalize 后另核 distinct 上限。普通 application 为 raw 6,860 / distinct 6,848，public 两变体自身为 6,859 / 6,847；不是把 distinct 数当 raw 数截断正例。最终仍与某一完整冻结变体精确比较。受审旧基线 schema 为 314,562 nodes、普通角色描述为 215,222 nodes，五份普通变体约 2.65m nodes；完整 peer candidate 为 814,211 nodes。两正式根只在进程启动时认证、严格解析并缓存，普通模式固定五个名字，Sandbox 固定 full-sandbox，不能从调用方文件或 actual metadata 填 root。公开发行将 normalized maps 与启动时已经严格 closed 的某一完整 expected 变体精确比较，不编码 actual 大 JSON；metadata-only observe wrapper 仍在 canonical 前执行完整 typed DTO 验证，候选字节不变。匹配成功后使用启动时缓存的固定 variant canonical bytes；全 schema/role 和 category 只有在完整 canonical bytes 相等时才共享不可变 map/array/bytes，flags 不同的角色 category 不共享。actual raw map/array 是每次解码私有拥有，可原地符号化；$ref/$type 提前返回的 symbol 只读，column 扩展仍拷贝，expected 不进入 normal。依赖 multiset 复用已算 raw/normalized identity，仍核 alias、同 identity/body 一致与 multiplicity。scalar 比较直接核规范数值文本或同类型字符串/布尔/null；递归模板上下文只带 field/attributes 状态，避免给每个节点分配全 path。TOAST indexes 使用一次固定 ANY 查询，逐 heap 仍须恰一索引、owner/OID 双射且关闭全部 Rows。

**profile 可移植性限制**：本轮只实证专用 PostgreSQL 16.15 catalog profile。plpgsql language OID 14038 来自该 initdb profile，不能声称所有 PG16 都相同；default/C collation 也仅覆盖当前已审环境。其他集群、发行版、locale、1Panel/生产部署需要单独审 profile 或新的语言/扩展符号合同，未知对象失败关闭，不能 runtime 自动接受。

## 已运行的专项

- 10 项 pure collector/loader 边界，含两个正式 embedded root 完整装载、五变体/full-sandbox、固定缓存与 metadata 一致性：`go test -race` 通过，11.815 秒（含启动时对约 58.4 MB 静态根的一次性解析）。
- 专用 PostgreSQL 真实 raw preflight 反例：65 KiB 以上函数正文、未知 domain、未知 policy 都在任何 pg_get_expr/pg_get_partkeydef 查询前拒绝，返回 nil snapshot/ErrCatalog，caller 事务仍为 T；由 caller rollback 后完整 observe 恢复。`go test -race` 通过，6.226 秒，未跳过数据库测试。
- 新增 trusted jsonb 静态 key 唯一审计、runtime scalar/资源/EOF、category 溢出首行拒绝和依赖 multiplicity 上限；含正式根的四项专项 `go test -race` 通过，11.550 秒。
- `go vet ./internal/fundinglotcatalog` 通过。

pure 覆盖重复与未知节点、number aliases、OID/TypeRef 混淆、PUBLIC 与角色引用闭包、ACL 字段/权限、proconfig oneof、SQL 模板字段范围与 schema oneof、实际表达式逐字相等、全局预算越界不修改计数、依赖 multiplicity 与身份冲突、符号双射、SQL registry 有界子输入、固定根 SHA/envelope/mode/variant。特别回归 pg_shdepend ownership 的 deptype=o，以及五种有效权限向量的精确长度。

代码/测试 SHA、固定 registry 提取来源 SHA、命令与资源界见 [机器记录](funding-lot-catalog-collector.json)。普通五变体、完整三 schema peer 的双轮候选与独立 SQL/inventory 审阅另有各自记录；它们证明 metadata 可重现，不替代这里的运行时边界或未来生产资金验收。

## 5 秒预算收敛与查询等价证据

首次正式公开 API 的 race 样本曾触发原 5 秒预算，本轮没有调高预算、替换 clock 或把 private diagnostic 当成发行通过。先去除公开发行的两次大 JSON 编码、复用 multiset identity、原地处理 actual owned trees、合并 TOAST 索引查询，并由根把比较递归改为小状态；随后定位到固定依赖 SQL 的相关 OR 子查询。

原条件 `EXISTS owned WHERE source-pair OR referenced-pair` 改为两条独立的等值 EXISTS，用 OR 组合。其逻辑保留 pg_depend 原始行，真实重复边仍各自出现；没有 ctid、GROUP BY、去重实际行或截断遗漏边。专用库同一 RR 的 EXPLAIN ANALYZE 测得原 SQL 1,407.933 ms、无 hashed subplan；新 SQL 29.828 ms、有 hashed subplan。两条查询的完整 raw JSON rows 按 multiset canonical 后逐字相等，包含原 raw 6,860 / distinct 6,848 的 multiplicity。测试另记录静态 query SHA、实际 raw/distinct 计数与 canonical rows SHA，原/新必须一致；actual 随机 OID 的 rows SHA 只用于本轮同事务等价，不能作为跨安装常量。测试为 [collector_query_plan_test.go](../../internal/fundinglotcatalog/collector_query_plan_test.go)。

在这些优化之后，**实际公开** Sandbox Snapshot、16 表空集 Installation、APP/worker/projector 三种 Runtime API 的 race 专项全部通过，各次仍独立使用原 5 秒总预算；三项 alias/borrowed/static-intern 纯负例同次通过。进程总 47.674 秒（含静态根启动解析），五个发行场景合计 23.95 秒（含 fixture），不是单次发行耗时。

单独 metadata diagnostic 为 4.614 秒，其中 Query-through-Rows.Close 2.984 秒，normalization/comparison 1.630 秒；依赖 category 425 ms、有效权限矩阵 1.011 秒。diagnostic 自身不能发行 proof，其时间不能替代公开 API 结果，也不是 HTTP 网关延迟、首 token/P95 或供应商验收。最终完整 leaf race 由根串行执行并在总验收记录汇总。

### 重复形状遍历与固定权限向量

根据根冻结的 [§10.2](../../specs/022-self-service-payments/c0-schema-check-contract.md)，公开发行的完整精确比较已经蕴含 expected 的所有 key/type/enum/reference/config shape 约束，因此不先重复遍历 actual DTO。此优化只在固定私有 normalized-documents→完整同一 variant schema/roles 比较链路生效，没有 skip flag 或外部 expected；metadata-only observer 仍先执行 DTO 验证再 canonical。raw Decode、pre-deparse、符号/alias/multiplicity、raw/distinct cap 和排序全部保留。新增 malformed actual 回归包含未知 key、scope 混合、数字/string alias、布尔/string alias、错误 enum、prosrc 对象模板、未知 proconfig/TypeRef 节点，完整比较全部拒绝。

有效权限的 table/column 固定 privilege 列表从 `ARRAY(SELECT ... FROM unnest(固定列表))` 展开成相同顺序的显式 `ARRAY[原函数调用...]`；has_table_privilege/has_column_privilege、序列原函数行为、每个 WITH GRANT OPTION、向量长度和其余 function/type/schema 查询不变。在同一真实 peer RR 内，旧/新完整 raw rows canonical bytes 对 APP 13,760、Mock 1,768、Receiver 568 行全部逐字相等。静态 query SHA、每 scope 的 raw rows SHA 与 EXPLAIN 见机器记录；实测 server execution 的改善有限（APP 56.061→47.970 ms，Mock 11.036→11.808 ms，Receiver 8.528→7.009 ms），不把整 category 耗时下降都归因于 SQL 展开。测试为 [collector_privilege_plan_test.go](../../internal/fundinglotcatalog/collector_privilege_plan_test.go)。

随后实际 Sandbox 五种公开发行与 malformed actual 的 race 专项通过，package 46.436 秒（包含启动），公开场景 22.54 秒（含 fixture）；metadata diagnostic 为 4.233 秒、Query-through-Close 2.941 秒、normalization/comparison 1.292 秒。原 5 秒 deadline 未改变。之前完整包的一次 Installation 触发原预算，这一后续专项不能冒充最终整包通过，最终记录由根串行整包结果补齐。

一次重试在编译写临时 archive 时因磁盘空间不足而未运行任何测试，清理约 1,283 MiB 可确认属于本任务的旧大型 Go cache 与两个临时 profile test binary 后重新执行；CPU profile、原始日志、源码、候选与正式 root 全部保留。这次未运行不能记作功能或 deadline 测试失败。

根最终串行整个leaf race已实际退出0，217.140秒、47顶层测试/179终态（176 PASS、3独立opt-in SKIP、0 FAIL），原5秒预算保持；完整源码与旧pins审计、三个skip的独立证据及资源零遗留见[根汇总](funding-lot-catalog.md)和[机器记录](funding-lot-catalog.json)。本节早期失败及单项结果保留各自原执行层次。

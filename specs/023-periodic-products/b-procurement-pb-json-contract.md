# B-P4：原周期预算操作 JSON 合同

2026-11-04，基线 `b3fd5cb`。**根已冻结本片实现范围；实施和验收结果另记。** 承接[重放实施合同](b-procurement-replay-implementation-contract.md)、[收口计划](b-procurement-replay-closure-plan.md)及[原 B-P1 合同](b-prefix-contract.md)。本片只闭合 `periodic_budget_operations.safe_input/result_snapshot` 的严格 JSON 与操作行内绑定，不新增迁移、读取列、数据库权限、公共 `Check` 或 `Qualified`。

## 1. 事实源与责任

真实接受集核对入口为：

- [020 的 `pb_decimal/pb_frame/pb_safe_input/pb_guard_operation/pb_view_at`](../../migrations/020_periodic_budgets.sql#L630)：四字段输入、八字段指纹 framing、已配置的十二字段结果与五字段窗口。
- [B-P1 同源快照捕获](b-prefix-candidate.sql#L305)：原 guard 的一次 `created_at` 保留，结果和观察 cut 从相同锁定 control/window 值生成；不改变原 JSON ABI。
- [公开输入合同](b-http-contract.md#1-公共值与精确-dto)、[实际纯输入解析](../../internal/periodicbudget/input.go)与[实际回执解码](../../internal/periodicbudget/commander_receipt.go)：布尔不能套用采购快照的全字符串解码器。
- [原 PB 独立核验](../../internal/periodicbudget/verifier_operations.go)与[PBprefix 动态快照核验](../../internal/periodicbudgetprefix/snapshot.go)：前者保留静态/历史缺证区分，后者只取 carry/window 动态子集；本片独立校验全部字段，不能因已有任一叶子而跳过。

本片只依赖本次 raw 原件行与共享 `replayWork`。不调用 writer、Commander、Reader、数据库、其他域的验证结果或 SQL codec 作为 expected oracle。可用标准库 token 解码、精确整数、SHA-256 和日历演算；可复用现有纯 Unicode/有界 reader 原语。完整 control/version/窗口/观察/事件/审计的双向跨行关系、配置变化/no-op 的真实选择以及历史存在性继续归 PB 闭包，不能据本片通过授予来源资格。

## 2. 两列共同 JSON 规则

两列都是必需 JSON object，SQL NULL、JSON null、空串、数组或其他根类型均非法。字段名按解码后的字符串比较，允许成员重排、合法 JSON 语法空白及等价的合法 Unicode 转义；拒绝重复解码键（包括 `enabled` 与 `\u0065nabled`）、未知/缺失字段、大小写别名、额外 root、尾随非空白和多余嵌套。

先逐 UTF-8/转义检查再解码，拒绝非法 UTF-8、原始或解码后的 NUL、孤立 UTF-16 surrogate；合法 surrogate 对与字面 U+FFFD 不因编码方式被误判。已声明字段的业务值仍须符合其封闭枚举/UUID/数字/时间集合，因而不能在值中夹空白或用替换字符绕过枚举。金额/revision 不经 float64，JSON number 不可当字符串或布尔，`null` 不可被 Go 零值吞掉。

整个 raw 输入仍共享现有 100000 行、16MiB、单字段 65536B 与同一个绝对 deadline；本片不再申请一份预算。`result_snapshot` 另保留原 PBprefix 的 8192B JSON 上限，超过8192但未超过65536记行内容错误；超过65536为 `ErrResource`。`safe_input` 原 DDL 没有独立8192B CHECK，HTTP 的8192B是包含 operation_id 的整个请求体上限，不能直接移植为旧存储列限制：本片对 safe_input 仅保留65536B资源界，合法形状的额外语法空白不会被新增8192B限制误拒。固定形状最多十二个顶层字段和一个五字段窗口，无任意递归分配。

## 3. `safe_input` 精确四字段

| 字段 | JSON 类型 | 值接受集 |
| --- | --- | --- |
| `expected_revision` | string | `0\|[1-9][0-9]*`，0..9223372036854775807 |
| `period_kind` | string | `utc_day` 或 `utc_month` |
| `enabled` | boolean | JSON `true` 或 `false`；拒绝字符串、数字与 null |
| `limit_amount` | string | 非负 int64 规范十进制，同 expected_revision 范围 |

四字段全部必需且不能 null。不包含 operation_id、actor、scope、target、resource_id、时间、spent/held 或 reset/timezone。金额单位继承原账务最小单位；零额度合法。拒绝负数、`-0`、正号、前导零、小数、指数、首尾空白、空串和超 int64。

## 4. `result_snapshot` 精确十二字段

这是成功 operation 的**已配置**结果，不是无 control 的 GET sentinel。即使通用 view 解码允许 `unconfigured/revision=0`，操作快照也必须为正 revision，配置四字段和两个时间不能 null。

| 字段 | JSON 类型 | 值接受集 |
| --- | --- | --- |
| `scope` | string | `tenant`、`key`、`procurement` |
| `scope_id` | string | 小写规范、非 nil UUID；不强制特定 version/variant |
| `revision` | string | 正 int64 规范十进制 |
| `period_kind` | string | `utc_day` 或 `utc_month` |
| `enabled` | boolean | 仅 JSON true/false |
| `limit_amount` | string | 非负 int64 规范十进制 |
| `state` | string | 按本节推导的 `disabled`、`scheduled` 或 `active` |
| `effective_from` | string | UTC6 |
| `tracking_started_at` | string | UTC6 |
| `current_window` | object 或 null | null 明示原观察时未建当期窗口；非空恰为下述五字段 |
| `carry_held` | string | 非负 NUMERIC100 规范整数，最多100位 |
| `as_of` | string | UTC6 |

UTC6 精确为 `YYYY-MM-DDTHH:MM:SS.ffffffZ`，year0001..9999、真实日历、有限、微秒；parse 后同格式重编码相等。拒绝 offset（包括 `+00:00`）、零年、闰秒、变长小数、逗号小数、额外空白、无穷和纳秒。这里不能复用采购旧快照允许 offset/变长小数的 RFC3339Nano 接受集。

`effective_from` 必须是 `tracking_started_at` 所属 UTC 自然日/月的下一边界；月按真实日历（含闰年），不用固定30日。`state` 在 enabled=false 时恒 disabled，否则 as_of<effective_from 为 scheduled，其余 active。as_of 所属期不能早于 tracking 所属首期；**不要求 as_of>=tracking_started_at**，同窗内数据库墙钟回退合法。不能与今天的高水位、今天的配置或其他 operation 的墙钟排序比较。

非空窗口精确五字段：

| 字段 | JSON 类型 | 值接受集 |
| --- | --- | --- |
| `id` | string | 小写规范、非 nil UUID |
| `start_at` | string | UTC6，恰为 as_of 所属日/月起点 |
| `end_at` | string | UTC6，恰为 start_at 的下一日/月边界 |
| `spent_amount` | string | 非负 NUMERIC100，最多100位 |
| `held_amount` | string | 非负 NUMERIC100，最多100位 |

窗口字段全部必需且非 null，不能加 as_of/control_id 等字段。窗口可在 scheduled 或 disabled 时存在，首个不完整跟踪期的 start_at 可以早于 effective_from；null 窗口也可出现在任何配置状态，不能强制 active 必有窗口。carry 与窗口累计金额不收窄为 int64，不要求金额小于当前 limit，不要求 disabled 时金额为0；成本更正和旧 held 可能超过限额。窗口存在性、准确动态金额与实际原 window 的关系仍由后续历史闭包证明，本片不拿今天余额来比较。

## 5. 操作行内绑定与幂等接受集

严格解码成功后，本片核验以下全部本行可判定关系：

1. snapshot.revision 恰等于 operation.revision；snapshot.as_of 恰为 operation.created_at 同一 instant 的 UTC6 表示。不是把当前时间填入原结果。
2. snapshot 的 period_kind/enabled/limit_amount 逐值等于 safe_input。expected_revision 与结果 revision 只接受真实 changed 或 no-op 的候选算术：`revision=expected+1` 或 `revision=expected`；运算不得 int64 wrap。具体哪条成立必须由真实 configuration_versions 的双向关系另行证明。expected=MaxInt64、revision=MaxInt64 的 no-op 合法；expected=0 只能结果1，不能凭 expected0/结果0伪造未配置回执。
3. kind/scope/target 与 snapshot.scope/scope_id 绑定：tenant 对应 `tenant_periodic_budget.configure` / `platform` / `/api/platform/tenants/<id>/periodic-budget`；procurement 对应相应 kind / platform / `/api/platform/procurement-accounts/<id>/periodic-budget`；key 对应 `key_periodic_budget.configure` / 非 nil 规范 tenant UUID / `/api/keys/<id>/periodic-budget`。禁止查询、尾斜线、转义路径、其他scope或自报别名。
4. 独立重算 input_fingerprint：依次取 kind、actor、scope、target、expected_revision、period_kind、enabled 的 ASCII true/false、limit_amount；每段前缀为 UTF-8 字节数的4字节无符号大端，然后原字节，最终 SHA-256 小写64 hex。没有额外域前缀；operation_id、resource_id、时刻、JSON键序与原JSON字节不进入指纹。相同 decoded 输入重排/空白/转义不改指纹，任何上述身份/输入值改变都必须匹配新的指纹。

actor 的原020列是 `users(id)` TEXT FK，SQL guard/frame 没有 pb_uuid(actor) 限制；公开 Commander 的新输入 UUID 校验不能反向收窄原 raw 原件历史。本片按 actor 原合法 UTF-8 TEXT 字节 framing，不 trim、不追加 UUID/非空/控制字符规则；其真实引用、与 audit/version 一致以及历史授权依旧归相应闭包。其余明确 PB UUID 字段仍沿原非 nil 小写规范规则。operation_id 已由原件本地值检查，且按原 ABI不加入指纹；同体重读原operation、异体冲突的服务行为不由本片重新执行。

不以 operation.revision 是否等于今天 control.revision 判历史合法，不按现在 limit 复核历史 cap；不因 actor 今天被禁用、失去角色、Key 撤销而使旧回执失效。resource_id 与 control 的真实关系、本操作是否 changed、首配/no-op/版本连续性、原 audit、observation 及精确窗口仍是跨行责任。

## 6. 接口、计数、失败与尚未完成

冻结后独占新增 `source_closure_pb_payloads.go` 和 `_test.go`，拟私有入口为 `(*sourceClosureState).pbPayloadRows() error`，由根接入 `checkSourceClosure`。两列错误及本行绑定错误统一按 `("periodic_budget_operations", operation_id)` 记 `InvalidScalarRows`，同PK重复、多列/多原因不重复计数；重复原件仍由 DuplicateRows 独立保留，不任选一份作为可信历史。

解码/Unicode/逐字段/指纹字节处理共享原 work 和绝对 deadline，至少每256步检查原context；入口和返回再check，不使用 Background 重新计时。资源、超时、取消或输入结构错误返回固定错误，外层返回零结果，不能把已累计部分计数作为成功。只有普通行内容错误进入 scalar 计数。无DB、网络、writer oracle、Q构造或主程序接线。

本片通过后可删去“PB两列严格JSON未实现”这一具体缺口；**完整 PB 日历/配置/窗口/观察/absence/精确效果、跨行来源和依赖闭包仍未完成，Complete=false、Qualified为空、没有公共Check**。不得把局部叶子通过写成完整历史、资金或生产验收。

## 7. 独立测试矩阵

测试手写原始 JSON 和预期，指纹采用独立 framing fixture/已冻结 golden，不导入 periodicbudget、writer 或其他 checker 当 oracle。

| 组 | 必须接受 | 必须拒绝/结果 |
| --- | --- | --- |
| 真实输入 | 两period×两enabled；limit0/MaxInt64；expected0/MaxInt64；键重排、语法空白、合法等价键转义 | 缺/重复/转义重复/未知/大小写字段；null、boolean string/number、金额JSON number；负/正号/前导零/指数/小数/超int64 |
| 完整结果 | 三scope×三配置state；window null/非空；NUMERIC100=0/100位；零limit但非零金额 | unconfigured/revision0；任一配置null；额外或缺字段；NUMERIC100 101位/负/前导零；int64 limit溢出 |
| 窗口 | scheduled首个不完整期、disabled非零桶；日/月/闰年/跨年边界 | 所有五字段逐个null/错类型/未知/缺/重复；错所属期、非自然边界、固定30日伪月、零/大写/非规范UUID |
| 时间 | 六微秒、year0001与可合法组成日历的year9999；同首期 as_of早于tracking | offset/+00:00、0/3/9位小数、逗号、year0000、无效日历/闰秒、早于tracking期、错误effective/state；不强制各操作墙钟单调 |
| 幂等/指纹 | changed expected+1、no-op同revision、MaxInt64 no-op；旧TEXT actor和多字节actor；operation_id改变不影响指纹 | 任一被散列字段改值但保留旧hash；bool按字符串/无长度拼接/字符数代替字节数/加入operation_id或域前缀；expected溢出wrap；错kind/scope/path/revision/as_of |
| JSON语言 | 合法键转义/成员顺序与空白；单文档 | 非法UTF8/NUL/孤立surrogate、额外root、数组、过深结构；Unicode原语独立保留合法pair与字面U+FFFD |
| 计数/预算 | 同PK双列多错误与重复只计scalar1，不同PK/不同物理表不相抵；safe_input8193B合法空白 | result_snapshot8193B内容错；65537B资源错；共享deadline已耗尽/解析中取消均固定错且零外层结果；不构造Q |

另做全包交叉回归，确认既有采购 payload/Usage/route 叶子的接受集没有因本片增加 boolean 或 UTC6 规则而变化；实际执行命令和结果由根在验证记录与统一架构中如实收口。

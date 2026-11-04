# B-P4 A5：显式 prospective executor 合同

2026-11-04；先冻结合同，再依切片交付。承接[出生根合同](b-procurement-attempt-birth-writer-contract.md)、[A2 安装合同](b-procurement-attempt-installation-contract.md)、[执行与恢复合同](b-procurement-execution-contract.md)和[A2 验证记录](../../docs/verification/procurement-attempt-birth.md)。本页冻结 Go 接缝、协议、错误、事务及观察边界；**S1协议、S2事务运行器与S3显式业务适配已实现并限定验证，只读观察仅详细设计；未接通普通运行路径，不关闭完整A4/A5/S4或R2**。原 `birth-exact-link-a2-v1` SQL、catalog、ACL 与固定摘要均不因本合同改变。

## 1. 范围、依赖与交付状态

A2 已有真实 request/账户/attempt 出生、reserved 效果与限定实际 APP 验证，不能再将其笼统写成“未实现”。截至本页冻结，已记录的最终整包为 47 顶层 / 1648 子项 PASS、0 SKIP/FAIL；具体版本、环境、原件与未覆盖范围以验证记录为准。这不是完整 A4、Go executor、全域 profile 或生产验收。

本片只适配三个已存在的公开根：`pp_create_account`、`pp_configure_account`、`pp_record_attempt`。普通 Reserve、绑定、余额观察和 PB Commander 仍使用原方法；dispatch、finalization、提案/审批、更正、恢复、Reconcile 不因“三根 executor”获得来源认证能力。尤其不能将新 attempt 已出生等同于模型发送许可。

[A0–A6 依赖](b-procurement-attempt-birth-writer-contract.md#8-实施切片和实际尚缺正文)保持：A5 完整验收依赖 A4；A4 补验期间可以独立冻结合同、实现无连接叶子及编译测试，不能因此勾选 A4。正式迁移、部署装配、生产角色/profile、默认 main/provision、在线资金/模型接线继续由 R2 单独设计和验收。

| 切片 | 本页状态 | 完成条件及不能扩称的范围 |
| --- | --- | --- |
| S1 无连接协议叶子 | 已实现并纯 race 验证，见 §10 | 固定三根 ABI/参数编码、恰一行 typed decoder/NULL union、固定 PP 分类与 cause；无连接、无事务、无构造器接线，不称完整 executor |
| S2 事务与只读观察 | 事务已实现并限定验证；观察仅详细设计 | [事务合同](b-procurement-executor-transaction-contract.md)与[观察合同](b-procurement-executor-observation-contract.md)分开记录，不能以独立APP只读事实oracle冒充生产Observe实现 |
| S3 业务及 HTTP 适配 | 已实现并纯/实际APP限定验证 | [适配合同](b-procurement-executor-adapter-contract.md)：显式 `NewProspective`、原外层校验、原公开 DTO/错误与 HTTP 优先级；普通 `New` 不变 |
| S4 actual APP 完整验收 | 已执行事务与适配窄矩阵，整体未完成 | [本轮范围](../../docs/verification/procurement-execution-runner.md)不替代完整A4/观察与其余矩阵；PB错误域兼容缺口未解决前不得宣称全等价 |
| A6 集成 | 未执行 | 汇总并行结果、语义复核架构/验证记录，再 reviewed refresh 与只读检查；不代替 R2 |

## 2. 包边界与显式选择

新增 [internal/procurementexecution](../../internal/procurementexecution/query.go)，仅依赖标准库/pgx，不反向导入 billing、procurement、server 或材料 renderer。完整设计中它负责固定 ABI、内部 typed 回执、PP 分类、事务分类和观察结果；不得提供任意 SQL、任意回调 DML、安装权限或自动发现 profile 的入口。S1 只交付其中无连接部分，后两类尚未实现。材料 renderer 保持独立，业务包不通过运行 `SQL()` 或 `Permissions()` 启用候选。

S3 计划在 billing/procurement 各增加显式构造器，合同形态为 `NewProspective(pool, ProspectiveOptions{Schema: ...}) (*Store, error)`。options 是受信装配输入，不来自 HTTP 请求；schema 是单个 1..63 字节、有效 UTF-8、无 NUL 的名称，构造后不可变。必须用 `pgx.Identifier{schema, function}.Sanitize()` 形成限定函数名，参数全部 bind；不得拼 caller SQL 或依赖连接的 `search_path` 查找根。nil pool/非法 options 在构造时安全拒绝；构造成功只表示本地配置有效，不发行数据库/业务权限证明。

普通 [billing.New](../../internal/billing/types.go#L46) 与 [procurement.New](../../internal/procurement/types.go#L22) 保持原行为。不得按函数存在、启动扫描、环境启发或某次错误自动选择候选；候选返回任何错误后不得退回原 INSERT/mutate。未安装、错角色、错摘要或不受支持历史应安全失败，由独立安装/验证流程处理。实际 session_user 必须是材料绑定 APP LOGIN；owner+SET ROLE、W LOGIN、旁路 pool 不能替代。

`AttemptInput`、`Attempt`、`CreateInput`、`ConfigurationInput`、`Command` 和原公开方法签名不改变。Store 的候选选择只作用于本页三个方法；其它方法不因该字段而自动迁移。S3 不修改默认 main、provision 或生产配置，不把显式构造器包装成在线安装入口。

## 3. 三根 ABI 与参数合同

以下均为固定 namespace 中的实际函数；一条 frontend SQL 仅调用一个完整根，不先 Go INSERT、再补 closure，不直接调用 private helper。参数顺序和类型不能从当前 catalog 动态生成。SQL 的函数名安全限定；如显式 cast，类型须用 pg_catalog 限定。

```text
pp_create_account(
  actor text, operation text, label text, provider text, state text, limit bigint
) -> TABLE(
  outcome text, operation_id text, actor text, kind text, target_id text,
  status text, reason text, before_state jsonb, after_state jsonb,
  created_at timestamptz, root_id text, account_journal_id text
)

pp_configure_account(
  actor text, operation text, account text, expected bigint,
  label text, provider text, state text, limit bigint
) -> 同上十二列

pp_record_attempt(
  tenant text, request text, sequence integer, channel text,
  channel_version text, credential text, upstream_model text,
  route_decisions jsonb, has_expected boolean, expected_version bytea,
  expected_input bigint, expected_output bigint, expected_basis bytea
) -> TABLE(
  outcome text, root_id text, source_id text, attempt_id text,
  request_id text, sequence integer, delivery_state text, cost_status text
)
```

### 3.1 原事务外校验与字节

RecordAttempt 先原 Sequence 1..2 / UpstreamModel 1..200 **字节**检查，再调用原 [validatedRouteDecisions](../../internal/billing/route_decisions.go#L7)，最后才进入候选事务。不得复制一份更窄 validator，不额外增加 billing 身份的 canonical UUID、TrimSpace 或控制字符拒绝。route JSON 沿原 `json.Marshal` 输出，空输入为 `[]`；priority 全 int64、weight 全 uint64、字段顺序/转义及长度均保持。不得通过 float64 中转损失 weight 上半区。

| CostQuote 输入 | 发给 SQL 的严格联合 |
| --- | --- |
| nil | has=false，version/input/output/basis 四项均 SQL NULL |
| 非 nil | has=true，version/basis 用原 Go string 直接复制为**非 nil `[]byte`**；空字符串是非 NULL 空 bytes；两个 rate 独立保留 NULL 或原 int64 |

不得先转 PG TEXT、解码 UTF-8、trim、Known 校验、NULL 折零、规范化 basis，或丢弃负 rate；原 ignored 分支允许任意 bytes。参数不得写入诊断日志。输入 slice/pointer 在调用开始形成私有快照，不能让外部并发修改影响某次重试身份；这不把本来存在数据竞争的 caller 变成受支持并发 API。

账户输入保持原 [configuration/canonicalID/canonicalNumber](../../internal/procurement/store.go#L24)：actor/operation/target 为原 canonical UUID 集合（保留原允许的 nil UUID），limit 为规范非负 int64 字符串，expected 为规范正 int64 字符串，文本和 state 保持原规则/错误顺序。Go 校验后才无损 ParseInt，不接受浮点、科学记数、前导零、溢出或新默认值。

## 4. 恰一行与 typed NULL union

内部 decoder 在扫描前先核 Rows.FieldDescriptions 的准确列数、投影列名、顺序及 PostgreSQL builtin OID：attempt 为 TEXT(25)×5、sequence INT4(23)、TEXT×2；account 前七项 TEXT、两项 JSONB(3802)、created_at TIMESTAMPTZ(1184)、最后两项 TEXT。参数中的 limit/expected 是 INT8(20)，不是结果列。不得靠 codec 可转换性接受 INT8/BOOL 代替 TEXT、INT8/TEXT 代替 INT4、JSON 代替 JSONB 或 domain OID 代替固定 builtin 型。元数据异常先关闭/排流，若有真实执行/流错误保留主因，否则 PP903；不提前扫描错误 ABI。

随后必须读取恰一行、扫描所有固定列、尝试读第二行，再检查 Rows.Err 并关闭/排空。`QueryRow` 单独使用不能证明没有第二行。零行、多行、列数/类型/解码异常、SQL NULL 联合、未知 outcome/枚举和结果身份不匹配均为本地协议失败（PP903 分类），保留原 scan/rows/close cause；不得伪造一个数据库 PgError。当已有数据库执行异常时保留该主因，不用后续零行掩盖；drain/cleanup 异常可合并但不能覆盖先前业务因果。

decoder 只解析协议，不连接或提交。中间回执始终私有；任何解码/执行/提交错误向原公开方法返回零 Attempt/Command。不会因为已扫描 ID、root 已 seal 或 decoder 成功提前发布 applied。

### 4.1 attempt 回执

八列除下表许可外均不得 SQL NULL。`request_id` 与原输入逐字节相同，sequence 相同。applied 的 attempt_id/root/source 为数据库生成的有效非 nil canonical UUID；replay 的旧 attempt_id 是原 SQL TEXT 字段，逐值保留，不给原快返额外增加 canonical UUID/非空等原来没有的检查。结果中的真实业务文本身份不普遍 cast UUID 或大小写折叠。

| outcome | root_id / source_id | 原 Attempt 五字段 |
| --- | --- | --- |
| applied | 两项非 NULL；source_id=attempt_id | delivery=`prepared`、cost=`unknown`，新 attempt 的准确 ID/request/sequence |
| replay | 两项都必须**真 SQL NULL**，不能用空串代替 | 保留原五字段；delivery 允许原 prepared/sending/completed/failed/unknown，不要求重新 prepared |

原业务写入的 cost_status 为 `unknown`、`contract`、`invoice`；但原 002 的该列只有 TEXT NOT NULL、没有枚举 CHECK，原 RecordAttempt 快返也只扫描字符串。因此 replay 的 cost_status 必须保留原非 NULL TEXT（包括旧 SQL 接受的其它字符串），不增加 enum/非空规则；这不认可其经济含义，也不用于授权。applied 仍严格 unknown。delivery_state 沿原数据库封闭 CHECK 的五值核对。S1 须有三种正常成本状态及非标准旧 cost_status/旧文字 ID 的快返保真测试，不能把新建分支的生成规则普遍施加到 replay。replay 不发新来源，不把 legacy 变 tracked，不凭返回值授权再次发送。

### 4.2 账户回执

十二列除 account_journal_id 的指定分支外均不得 SQL NULL。before/after 的“空”是 **JSON null**，不是 SQL NULL。operation、actor、kind 与本次输入准确相等；configure 的 target 与输入准确相等；create 的 target 必须是生成的非 nil canonical UUID。created_at 必须 finite，可无损还原原 Command.CreatedAt。两个 outcome 都返回原 root，且 root 为非 nil canonical UUID；账户 replay 与 attempt replay 的 root NULL 规则不同。

| 分支 | status / reason | before / after JSON | journal |
| --- | --- | --- | --- |
| create applied 或其 replay | applied / 空串 | null / 原 typed account snapshot | 非 NULL |
| configure applied（含同值 noop）或其 replay | applied / 空串 | 原 before / 原 after | 非 NULL；noop 不要求 revision 增加 |
| configure not_found 或其 replay | rejected / not_found | null / null | **SQL NULL** |
| configure revision_conflict 或其 replay | rejected / revision_conflict | 原 before / null | 非 NULL；不能把 journal 的 before=after 误写成 Command.After 有值 |

snapshot 严格按原 Account 的九字段解码：id/label/provider/state、四个十进制金额/版本字符串、created_at；检查 JSON 结构、准确字段、类型和原 canonical ID/文本/state。limit 为规范非负 int64、revision 为规范正 int64；held/spent 为原 NUMERIC(100,0) 的规范非负十进制字符串（最多100位），不能截为 int64。时间沿 `pp_ac_time_v1` 的 UTC RFC3339、1..9999年及最多微秒精度，不接受 infinite。拒绝 null object、缺/多字段或浮点代替字符串。不能用当前账户 SELECT 代替不可变 Command 的旧快照。返回前以原 Command DTO 逐值重建，不向 API 暴露 root/journal/source 或私有诊断字段。

## 5. 固定 PP 分类、cause 与 HTTP

下面为本页三个根及其当前 transitive 依赖的业务 PP 映射；执行合同的其它 PP 状态保留给尚未适配根，不因通用 mapper 存在而宣称实现。映射仅按准确 SQLSTATE 和预先固定的入口域，不依据开放 Message、Detail、Where/Context、ConstraintName 相似字符串或日志内容猜业务错误。本地 Message 是下表常量，不透传数据库文本。

| SQLSTATE | Go 公开分类 / 固定 Message | HTTP |
| --- | --- | --- |
| PP001 | `procurement.ErrInvalid` | 400 |
| PP002 | `procurement.ErrConflict` | 409 |
| PP003 | `procurement.ErrForbidden` | 403 |
| PP101 | billing invalid_attempt / credential is not a member of the frozen channel version；适用前提见 §5.1 | 400 |
| PP102 | key_ip_forbidden / request source is not authorized | 403 |
| PP103 | invalid_key / key is not active | 401 |
| PP105 | binding_unavailable / published model binding is quarantined | 400（保持现有边界） |
| PP106 | request_finished / request is not eligible for a new attempt | 409 |
| PP107 | unpublished_binding / attempt is not part of the request route snapshot | 400 |
| PP108 | request_deadline / request is no longer eligible for dispatch | 400 |
| PP109 | unsafe_retry / prior attempt has no trustworthy non-consumption evidence | 400 |
| PP110 | cost_unknown / controlled procurement requires a complete known quote | 503 |
| PP111 | cost_quote_stale / configured procurement quote changed before dispatch | 503 |
| PP112 | cost_limit_exceeded / configured procurement rate exceeds route limit | 503 |
| PP113 | procurement_bound_invalid / procurement hard upper bound cannot be represented | 503 |
| PP114 | procurement_paused / supplier procurement account is paused | 503 |
| PP115 | procurement_budget_exceeded / shared supplier procurement budget is exhausted | 503 |
| PP116 | idempotency_conflict / attempt sequence already used | 409 |
| PP117 | channel_unavailable / channel is not available | 503 |
| PP190 | `pgx.ErrNoRows`，仅原外部 lookup 缺行域；保留 PG cause | 404 |
| PP406 | forbidden / key model scope is not authorized | 403 |
| PP407 | forbidden / key channel region is not authorized | 403 |
| PP408 | request_deadline / request deadline expired | 400 |
| PP409 | request_deadline / request deadline expired while waiting for procurement | 400 |
| PP410 | unsafe_retry / prior attempt has no terminal evidence | 400 |
| PP411 | cost_quote_stale / procurement quote changed before attempt | 503 |
| PP412 | cost_quote_stale / configured procurement quote changed before reservation | 503 |
| PP413 | cost_unknown / controlled procurement requires a complete quote | 503 |
| PP414 | cost_unknown / controlled procurement requires both known cost dimensions | 503 |
| PP415 | cost_unknown / route requires a known procurement quote | 503 |

闭合错误 PP901–906 沿[执行合同 §4.2](b-procurement-execution-contract.md#42-新闭合失败与原数据库错误)，固定 Code 依次为 `procurement_execution_forbidden`、`procurement_history_invariant`、`procurement_execution_protocol`、`procurement_history_resource`、`procurement_history_exhausted`、`procurement_prefix_untracked_domain`；Message 固定为相同安全 Code，HTTP 均 503。PP904 的映射保留上位合同，不声称当前材料已实际抛出该分支。

内部 failure 保留原 cause，通过 Unwrap 使 `errors.As(..., *pgconn.PgError)` 取得同一个原错误；billing wrapper 通过 As 提供原 `*billing.Error`，采购 wrapper 通过 Is 提供原 sentinel，PP190 wrapper 通过 Is 匹配 `pgx.ErrNoRows`。可参考 [periodicFailure](../../internal/billing/periodic_budget.go#L12)，不能靠字符串重建原类型。Error()、HTTP 和普通日志只输出安全分类，不输出 PG Message/Detail、SQL、参数、凭据或观察材料。

未知 PP、原 42501/23503/23505/23514/P0002/22xxx、55P03/57014 和其它数据库/网络/context 错误保持原执行错误及 cause；不宽泛映射 forbidden、幂等成功、not_found、rejected 或可重试。40P01/40001 的重试还必须满足 §6，不由错误 mapper 自行触发。

### 5.1 PP101 的固定适配前提

当前固定 SQL 的 PP101 有输入边界、route leaf 与 credential membership 多个 Message，不能假称它们已拆 state。本页不修改 frozen SQL/材料。原 [RecordAttempt](../../internal/billing/evidence.go#L72) 的 Sequence/UpstreamModel 与 route validator 在数据库调用前执行；其各个原 Message 直接在 Go 返回，保持优先序。

在这套前提下，通过原外层校验并由本适配器准确编码的正常 Go 调用，进入数据库后可达的 PP101 业务分支是 [credential membership](../../internal/procurementattemptbirth/materials/attempt-writer-v1.sql#L358)，故固定为表中 Message。S1 mapper 的使用前提必须在 API/测试中明示；S3 不能跳过外层校验后复用它。直接 SQL/叶子攻击测试仍核实际 PP101 与原固定分支 Message，它们不是 Go 业务适配器；不能用它们要求 adapter 解析 PG Message。若未来调用形态使其它 PP101 成为正常 Go 可达分支，应先另冻结 state/ABI，而非新增文本分派。

### 5.2 PB 错误域的明确缺口

[执行合同 §4.3](b-procurement-execution-contract.md#43-原pb固定文本的精确兼容例外)要求只有准确 PB helper 调用域才可应用旧 Code=23514、Message/ConstraintName 的极窄兼容。当前 A2 完整根将 PB 调用包含在同一 frontend SQL，未返回可受信的独立异常域 tag；Go 无法从单一 Query 错误可靠区分 PB helper、原 trigger 与外层 COMMIT。

因此本片不修改材料，也**不对原始非 PP 错误做 PB 文本分类**。不能解析 PG Context/Where 堆栈，不能因运行的是 attempt 根就把所有 23514 视为 PB，不能制造 PgError 或在客户端猜调用阶段。外层 COMMIT 的原 23514 始终保持原提交错误。S1–S3 可交付安全失败的候选实现，但 PB 受限错误码与原 Go API 的全等价门禁仍开放。后续必须先冻结可证明的 PB 错误域传递方案及其独立材料/版本影响，再实施并实证；不得以 mapper 单测或相似 Message 关闭完整 A5。

### 5.3 HTTP 优先级

现 [server.error](../../internal/server/server.go#L355) 先检查 PgError 的 23505/23503/23514，随后才调用 billingError；增加保留 cause 的 typed wrapper 后，这会抢先把已分类 PB/协议错误转成 409。S3 应先处理明确安全的 typed 业务/候选执行错误，再处理泛数据库 fallback；采购 sentinel、PP190 与 billingError 都须优先于 PgError fallback。未分类原始错误保持原 HTTP fallback，不借本片重写全部 server 错误合同。

新增闭合错误和 `procurement_outcome_unknown` 必须显式 503，不能落入 billingError 默认 400。既有业务码状态按上表/原映射保持。HTTP 使用安全通用消息与 request_id，不序列化 cause、观察 handle、输入或数据库细节；拒绝把 unknown 标为可自动重试。单测必须构造含真实 PgError cause 的 wrapper 验证优先级，不能只测无 cause 的手建 billing.Error。

## 6. 单根事务、期限与清理状态机

S2 显式 pool.Acquire 持有连接，直到完成清理/idle 判定才 Release。不得用 pool.Begin 的自动归还时机替代提交状态分类。业务事务为 READ COMMITTED，事务开头以准确 pg_catalog 调用设置 LOCAL lock_timeout=3s、statement_timeout=5s；随后仅一条 frontend SQL 调完整业务根。不得拆分其 SPI、预先 Go DML、在子步骤重置5s、延长到10s或向根传 caller clock。

5s 限制该根的一次 frontend SQL，不宣称包括 Acquire/BEGIN/SET/COMMIT 的整个调用总耗时为5s。所有尝试使用调用方原 context/绝对 deadline，不重置上层模型/财务期限；本片不额外给整个业务调用添加10s/其它替代预算。独立清理 context 最多5s，只用于 rollback/close，不执行业务或续期。

| 状态 | 允许的动作与结果 |
| --- | --- |
| acquired / begun / executing / decoded | 中间结果私有；任意失败不发送 COMMIT，使用独立 context Rollback |
| precommit canceled | 在调用 Commit 前已经观察到 ctx.Err；不发送 COMMIT，回滚并返回原 context 错误及零结果 |
| commit_may_have_been_sent | 进入 Commit 调用即保守标记；除非 driver/明确协议证据能证明未发送，不凭 ctx、Rollback/ErrTxClosed 或连接关闭推断未提交 |
| committed | Commit 明确成功；才返回原 DTO。若连接不再 idle/可用，销毁连接但不能把已确认成功改写成 unknown |
| rolled_back | 明确 rollback 成功且连接未关闭、TxStatus=I，或 Commit 收到确定服务器回滚响应并确认 idle；返回零结果，按下面准确重试策略 |
| outcome_unknown | COMMIT 可能已发送且无确定结果；销毁连接，返回零 DTO + `procurement_outcome_unknown`，不进入重试 |

原执行错误的 rollback 清理若失败或连接非 idle，必须 Hijack 后关闭，不能把污染连接归还 pool。尚未尝试 COMMIT 的事务没有自行提交能力；清理失败不等于已经发生业务提交，但也不能当作“已确认回滚”用于重试。清理结果不得覆盖更早错误，可保留为 cause；panic/协议异常同样不能发布私有回执或泄漏连接。

Commit 错误中仅收到真实 PgError/`pgx.ErrTxCommitRollback` 且连接 idle、可证明服务器已回滚时进入 rolled_back；网络/EOF/context/确认丢失即使 cleanup 返回 ErrTxClosed 也不证明回滚。对于已经确认“Commit 未发送”的 driver 错误，仍须成功 rollback/idle 才能讨论重试，不能仅凭 SafeToRetry 重放。

**重试策略固定且分入口：** RecordAttempt 最多三次，仅准确 40P01/40001、整个尝试已确认回滚、ctx 未结束；无新增 sleep/退避，原绝对 deadline 不重置。每次尝试重新分配私有回执和观察身份，失败结果不得沿闭包残留到下一次。create/configure 命令完全不自动重试，包括40P01/40001。55P03/57014、协议、撤权、业务拒绝、非 SQL 和 unknown 均不重试。replay 也必须以明确 COMMIT 完成后才由候选路径发布；相对原管理旧快返用 Rollback 结束，这是明确接受、须故障验证的候选差异。

## 7. unknown COMMIT 与只读观察

unknown 错误的安全 Code 固定为 `procurement_outcome_unknown`。内部保留本次已收取的 root/source/attempt 和原 operation/actor/kind/target 或 tenant/request/sequence/四字段身份；这份 observation handle 是过程内诊断资料，不是业务 DTO、授权 token 或用户输入。没有实际取得的 ID 不能自行生成补上；unknown 不带可用 Attempt/Command，不调用 PrepareDispatch/provider/后台生成任务。

观察必须使用新连接、新的独立有界只读 context、REPEATABLE READ READ ONLY 事务，所有读取在同一 snapshot；不 SELECT FOR UPDATE/SHARE、不 advisory lock、不写、不调用业务 mutation/root、不重新计算经营授权。使用原受限 APP LOGIN 及现有准确 SELECT/只读 validator 权限，不为观察扩 owner、writer、跨租户或任意表权限。当前 validator 明确不施加 mutation 的 RC/超时要求，不能为了观察把其改成 definer 或增加 DML。

| 原身份 | 观察所需证据 | 允许的观察结论 |
| --- | --- | --- |
| account operation | operation/actor/kind/target、原安全输入 digest 与不可变 Command；准确 source/root/step/audit/journal，含 not_found 的 journal NULL；调用固定 `pp_validate_original('procurement_command', operation, NULL)` 和 `pp_validate_root` | `receipt_present`，仅证明匹配回执在 snapshot 中已存在；不推断本次调用是 applied 还是 replay |
| 已取得 attempt/root/source | 原 tenant/request/sequence/四字段与准确 attempt ID；applied 的 origin/root/step 与 managed p/PE/account/PB 或 unmanaged 禁止集合；调用固定 `pp_validate_attempt` / `pp_validate_root` | `receipt_present`，证明这些准确身份已提交；不是再次发送许可 |
| replay 已取得 attempt 但本次 root/source NULL | 准确原五字段、原 tenant/request/sequence/四字段；tracked 时只读认证，真正 legacy 不补来源、不伪造本次 root | 只能记录原回执存在；本次 COMMIT 是否成功仍未知 |
| 未收到新生成身份、缺行、旧且无法归属、部分关系、多义或不可用/不支持历史 | 不以一个旧 root 或同 request 的任意旧 attempt 替代 | `unresolved`；仍 unknown |

观察返回独立 typed `Observation`，不返回原 mutation 成功 DTO，不将 unknown 重新解释为派发成功。即使 `receipt_present`，也不从观察自动继续发送或创建新 operation；后续人工/恢复流程须另有原有身份与状态的明确合同。本页没有自动恢复经营动作。

读不到只能说明该 snapshot 不存在，不能证明永远未提交；原事务可能仍在服务器结束，不能因此盲重试。观察期中的陈旧/权限/网络/validator 错误保留为 `unresolved` 的安全原因和内部 cause，不降级不一致为 legacy。`PP902` 表示历史不一致、`PP906` 表示未支持域，两者不能换成“未发生”。S2 的观察查询、字段清单、digest 计算必须从固定合同/原实现冻结，不从当前 live catalog 或 writer 输出生成 expected。

## 8. 实现切片与验收矩阵

S1 可以先行实施；S2 依赖 S1，S3 依赖其使用的 S1/S2 合同；S4 汇总全部，不因前片单测通过提前完成 A5。共享文件/包边界由集成人一次分工；数据库由集成人在专用 PG16 串行执行，包级 `-p 1`，不并发污染 global default ACL。

| 切片 | 必须交付 | 有意义的验收与失败边界 |
| --- | --- | --- |
| S1 | 无连接 ABI/参数快照、typed row decoder、固定 PP/cause 叶子 | 表驱动核三个签名/顺序、nil/空 BYTEA/任意 bytes/NULL rate/full uint64、列名/数量/OID（含可转换错型反例）、零/一/多行、晚到 Rows.Err/Close、列错型/NULL、分支全 union、身份错配、全部已映射 PP 与未知非 PP；错误链保留同一 cause；单测不声称 actual APP |
| S2 | 显式 Acquire/runner、分入口重试、unknown 与观察 | 注入事务协议故障核未提交不发布/清理不复用/最多3次与0次/ctx不重置；wire 与真实PG证据在 S4；观察 read-only RR、准确身份/反向关系、缺/旧/未获身份 unresolved；不调用生成 |
| S3 | 两个显式构造器、三个原 API adapter、HTTP 优先级 | 原外层校验及各 Message 先于 DB；默认 New 原 SQL 不变；候选无 fallback；PP101 唯一可达固定映射；PP190 Is、采购 sentinel Is、billing As/PgError As 同时成立；带 cause 的 HTTP 状态与零结果 |
| S4 三根业务差分 | actual APP 经新 Go adapter 与普通旧 API 独立 schema/fixture | create/configure新/重放/not_found/revision_conflict/noop、原错误优先序；attempt RouteRevision0/发布版、Seq1/2、原四字段幂等、ignored bytes、managed/unmanaged、0/未知/计划报价；实际原件、资金/PB与独立手算，不由新 writer 生成 expected |
| S4 故障与权限 | 真实 frontend/transaction/wire 控制 | 每 DML/外层 deferred/COMMIT 前后失败、零/多行/协议异常回滚、55P03/57014/累计5s、准确40P01/40001重试、不重置deadline、有效撤权与全行回滚；COMMIT确认丢失前后均零公开结果且无重放 |
| S4 unknown观察 | 已提交与未提交的 wire 丢包正反控 | account/attempt 的准确已提交结果可观察；旧回执/缺行/无root/支持域缺口保持 unresolved；观察不修改任何业务表，不调用 root/provider，不凭root存在判成功 |
| S4 PB全等价 | 先设计 §5.2 的可信错误域方案 | 原PB分类、预算/时钟/不变量/未知异常、非PB相似错误及外层COMMIT不能混淆；当前 frozen 材料不能关闭此门 |

真实 oracle 应调用原 Go validator/Equal/原 API，不复制新实现生成同构预期；测试 mock 只能证明协议叶子与客户端分派，不能证明 PG 权限、锁、触发器、提交或供应商行为。PG 开关缺失导致的 SKIP 必须单列，不能算商业验收。测试名称存在、静态编译、A2 旧 PASS 均不等于 S4 已执行。

## 9. A4 剩余门与集成边界

本页冻结时，A4 仍需逐项核验：全部原 checks/组合错误优先序及路由/Seq2/幂等分支，绑定与 supplier 首配双向等待，多租户共享账户，资格/expiry/真实 UTC 窗口与 active cap，原 Reserve/A1 和 command-only 逐 grant，全部 grantee 提升路径，完整跨root/假legacy/额外来源/seal 攻击，全部故障、死锁重试与未知 COMMIT。1339 grant 实验限于冻结 managed first-window 场景；110 catalog 反例也只证明固定 test-only inspector。

本轮另有[同步专项合同](b-procurement-attempt-synchronization-contract.md)与[故障期限专项合同](b-procurement-attempt-fault-deadline-contract.md)。它们提出绑定/首配双向同步、跨租户共享限额、账户资格、锁超时/取消、Key/request cut 和单根累计5s；**实际结果须由集成人执行记录，本页不预先计 PASS**。同步首配不等于已启用周期 cap/跨月；期限专项明确未实证 account 等待跨24小时财务硬期限/PP108 财务分支，不得用缩时owner fixture或修改出生身份补证。

完整 A4 前置门未关闭时，S1/S2/S3 可以按上述范围交付候选源码及限定验证，A5 状态仍为部分实现/未全验收。D1/I1、B-P3 历史命题、全域认证/完整 profile、R2 生产接线，以及真实官方/第三方模型、支付、容器/1Panel、HA、生产与最后性能指标继续独立开放。

本页新增会影响统一架构中的候选模块、错误/提交边界、观察链路、权限边界和能力状态；集成人在全部并行结果汇总后更新正文/验证记录并说明哪些仅设计、哪些仅实现、哪些实际通过，再执行仓库要求的 reviewed refresh 与只读架构检查。S1 无连接叶子完成时也只能标记该片，不能将图中的规划 constructor/executor 或观察箭头画成默认在线链路。

## 10. S1 历史源码与纯验证记录

[query.go](../../internal/procurementexecution/query.go)交付 `Root`、三种内部 input、不可变 `Invocation` 和 `NewCreateAccount/NewConfigureAccount/NewRecordAttempt`；构造固定 schema-qualified SELECT 与参数私有快照，`Query/Arguments/Root` 只返回数据，不执行 SQL。每次 Arguments 重新复制 byte slice，quote 的 rate 在构造时去引用，空 expected bytes 保持非 nil。这里三个 `New*` 只是 SQL 调用数据构造器，不是 §2 的 Store.NewProspective，也不执行原业务校验。

[receipt.go](../../internal/procurementexecution/receipt.go)只依赖 Rows 的 FieldDescriptions/Next/Scan/Err/Close 子集，提供 Invocation.DecodeAttempt/DecodeAccount。它先认证准确列名/数量/OID，再以实际 pgtype nullable 接收位置核字段、恰一行与 union，最后校验内部 DTO；旧 replay 的 TEXT ID/cost_status保持，不对新生条件外推。账户 snapshot 核准确九字段、数字字符串的 int64/NUMERIC100 界、微秒 UTC 时间、拒多余/缺少/重复键；configure noop 在 MaxInt64 revision 不溢出。Scan 和 Rows.Err 同一 cause 归本地 PP903；独立晚到服务器/网络错误优先保留，不用 ABI、零行、错型或多行掩盖。

[errors.go](../../internal/procurementexecution/errors.go)提供仅含受信公开元数据的 Failure 和 ClassifyPP；按 Root 固定入口域分类，Unwrap 保留同一原 cause，PP190 可 Is(pgx.ErrNoRows)。billing.As/采购 sentinel/HTTP 接缝仍未实现。未知 PP、非 PP、其它 root 的状态原样返回，不实现 PB 文本匹配、事务重试或 unknown 观察。

2026-11-04，仅执行去掉专库开关的纯测试：

```sh
env -u TEST_DATABASE_URL -u PROCUREMENT_IDENTITY_DATABASE_URL \
  -u PROCUREMENT_ATTEMPT_RULES_DATABASE_URL \
  go test -race ./internal/procurementexecution -count=1 -json
```

首片为 17 顶层 / 52 子项 PASS、0 FAIL/SKIP、1.721 秒；review 后新增固定元数据认证与可转换错型反例，最终 **20 顶层 / 56 子项 PASS，0 FAIL/SKIP，1.506 秒**。测试为 [query_test.go](../../internal/procurementexecution/query_test.go)、[receipt_test.go](../../internal/procurementexecution/receipt_test.go)和[errors_test.go](../../internal/procurementexecution/errors_test.go)；Rows 测试使用 pgx 的真实 text codec，在内存模拟原生 Scan 失败同步写 Rows.Err、NULL/empty、infinite 及晚到流错误，没有 Acquire/Begin/Query 网络调用。最新原件为忽略目录 `.local/procurementexecution-s1-abi-final.jsonl`，77380 字节，SHA-256 `33536946a9141ee9a6fbe5d44630d4ecfbd32d934664203f2f3996a88725bdb1`；首片原件 `.local/procurementexecution-s1-final.jsonl` 保留，不将两轮重复相加。

首轮测试暴露内存 Rows transport 将 TEXT 空字符串编码成 nil buffer、误当 SQL NULL的问题，已在 mock 使用非 nil buffer准确分开空值/NULL；未改生产 decoder 接受非法 NULL。review 又发现 Scan 成功不能证明固定 ABI：真实 pgx codec 可将 INT8 旧ID/BOOL成本转成 `**string`、将 INT8 sequence 转成 `**int32`；新增正控先证明这些转换实际成功，再证明 decoder 在扫描前拒错型。当前 codec 对 TEXT sequence→`**int32` 本身拒绝，测试准确保留这个负控，不声称它可转换。全部八/十二列另逐项核错 OID/列名、缺/多列、JSON/JSONB及元数据失败后的执行错优先级，最终 race 如上。本次没有连接或运行 PG，不证明三根真实调用、角色/权限、事务/COMMIT、连接清理、原 Go adapter 或 HTTP 行为；S2/S3/S4、§5.2 PB 错误域及 §9 A4 门继续开放。

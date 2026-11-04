# B-P4 A5-S3：显式业务适配合同

2026-11-04。承接[上位 A5 合同](b-procurement-prospective-executor-contract.md) §2/3/5 和[S2 事务合同](b-procurement-executor-transaction-contract.md)。先冻结本页，再实施。候选不是正式迁移或默认运行配置；S3 的纯测试不关闭 S4、PB 错误域兼容或 R2。

## 构造与入口

`billing.NewProspective(pool, ProspectiveOptions{Schema: name})` 与 `procurement.NewProspective` 返回 `(*Store,error)`。nil pool 或无效 schema 在本地返回安全错误（分别执行不可用、协议错误）；schema 沿 S1 的单个名称规则（1..63 UTF-8 字节、无 NUL），不增加标识符白名单，允许正确引用的点、空格和引号。构造器不访问数据库、不安装或探测材料，保存独立不可变的 schema 和真实 S2 Runner。

普通 `New`、main、provision 不变。候选只改 `RecordAttempt`、`CreateAccount`、`ConfigureAccount` 的显式分支，错误后绝不回退旧写入。Reserve、BindCredential、余额观察、PB Commander 等仍为原实现。本页不发布观察 API，不从回执生成 dispatch 权限。

RecordAttempt 先执行原 Sequence/UpstreamModel 校验，再执行原 `validatedRouteDecisions`；两步成功才构建 Invocation。route JSON 使用该原 validator 产物。quote 字符串及可空有符号 int64 rate 直接交 S1，保留 ignored 分支任意字节、空串和负数，不另做 canonical ID 或 Known 校验。

CreateAccount 先原 configuration 校验，ConfigureAccount 先原 target/expected/configuration 校验，再分别原 actor/operation canonical UUID 校验，之后才 ParseInt 生成无损 int64。原 mutate 的外层校验顺序保持；未启用候选时不改变原方法行为。nil/canceled context 的执行拒绝由 S2 负责，不能在原业务校验前改变错误优先级。

## 回执和错误

仅在 Runner 无错误后映射原五字段 Attempt 或九字段 Command；回执中 root/source/journal/outcome 不进入业务 DTO。Command before/after 保留不可变 JSON 快照并私有复制，不能读当前账户替代。任何 Runner/协议/提交错误返回零业务 DTO，即使内部已有完整结果。

已由 S2/S1 分类的 billing Failure 通过私有 wrapper 的 `As(**billing.Error)` 暴露固定 Code/Message，`Unwrap` 保留精确原链。procurement Failure 通过私有 wrapper 的 `Is` 匹配原三个 sentinel，`Unwrap` 保留原链。PP190 沿 Failure 的 `Is(pgx.ErrNoRows)`。执行域 Failure 和 UnknownOutcome 保留原 typed 错误；UnknownOutcome 的 opaque handle 必须仍能沿 `errors.As` 取得，不创建重试建议或日志参数。未知 PP、任意原始 PG/网络/context 错误逐值透传，不调用基于文本的 PB mapper。

HTTP 优先处理已分类的执行域 Failure、UnknownOutcome 和原 billingError，再处理 raw 23505/23503/23514 fallback。六个执行 Code 及 `procurement_outcome_unknown` 显式 503，body 只含安全固定 Code/通用提示/request_id，不序列化 cause、handle 或参数。原 procurement sentinel/ErrNoRows 优先级保留；原未分类 PG fallback 仍为 409。已有 PB typed wrapper 也必须先于其原始 23514 cause 处理，本页不扩大 PB 文本识别域。

网关 `admissionError` 也必须先识别 typed unknown/执行域，再判断原 context/并发队列错误；否则 unknown 保留的 context cause 会抢先变为504。单独原 context/队列错误保持504/429，unknown 即使可达这些 cause 仍503。

## 验收

纯测试核无连接构造、schema 引用接受集、普通 New 保持原装配、原 validator 顺序、route 大整数与 quote 任意字节、三个固定根、私有 DTO 映射、所有错误零 DTO、固定 PP 分类与原 cause/Is/As、raw 非 PP 透传，以及 HTTP 含真实 PG cause 的 typed 优先序和敏感标记不外泄。私有 runner 接缝仅供包内注入，不向调用方提供任意 SQL/回调接口。

实际 APP 三根执行、回滚与提交未知须由独立[S2/S4 验收](b-procurement-executor-transaction-acceptance-contract.md)和后续 adapter 数据库验收证明。纯测试通过不代表实际 APP 身份、权限、材料摘要或供应商/生产验收。

本轮 adapter 专库矩阵使用原 A2 fixture 与真实 APP LOGIN，不增 grant 或改材料：账户显式 create→changed→noop→revision_conflict→not_found及旧create重放，在原 Command reader 与九字段DTO间逐值比对，重放核全34表不变；实际采购账户绑定后由原 Reserve 提供父请求，候选 RecordAttempt 成功且客户hold120/采购hold240，原五字段DTO与持久事实比对。另一个真实request在共享limit300下遭PP115，验证 billing.Error 与原PG同因、零Attempt、34表不变；同请求改credential重放遭PP116，不能回退旧写。此矩阵只证明显式接缝，unknown与deferred提交边界由S2独立矩阵证明，完整交叉组合仍由S4收口。

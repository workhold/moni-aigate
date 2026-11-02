# 022-C0-T3：采购管理命令、观察与审计关系合同

2026-11-02，根授权冻结。扩展原同一 `ScanProcurement`，新增局部摘要 `procurement_command_observation_graph_v1`。此片只核管理回执、不可变绑定/人工观察和对应command审计，不授采购全域coverage，不证明人工余额是真实供应商认证余额。

## 1. 固定原始读取与DTO

复用同一扫描的users、procurement_accounts、credential_procurement_bindings原始行；新增三张完整基表，128行C序keyset/短页EOF，不JOIN、业务过滤、聚合或调用写侧校验函数。所有关系共享原lease的100000行、16MiB投影文本预算和期限，只Finish一次。

| 表 | C序排序键 | 固定投影列顺序 |
| --- | --- | --- |
| procurement_commands | operation_id | operation_id,actor,kind,target_id,input_digest,status,reason,before_state::pg_catalog.text,after_state::pg_catalog.text,created_at |
| procurement_balance_observations | id | id,account_id,operation_id,actor,status,amount::pg_catalog.text,currency,source,semantic,observed_at,provider_as_of,evidence_ref,created_at |
| audit_events | id | id,actor,tenant_scope,action,target,sanitized_diff::pg_catalog.text,request_id,created_at |

audit_events必须整表到EOF；纯core只解释action精确等于`procurement.command.applied|procurement.command.rejected`的审计，其他action只计扫描/文本预算，不按本片JSON或关系合同解释。不得以SQL WHERE action过滤来漏掉反向事实。

固定Go纯入口`CheckProcurementCommandGraph(ctx, ProcurementCommandRows) (ProcurementCommandCheck,error)`：

- `ProcurementCommandRow{OperationID,Actor,Kind,TargetID,InputDigest,Status,Reason,Before,After string; CreatedAt time.Time}`。
- `ProcurementObservationRow{ID,AccountID,OperationID,Actor,Status string; Amount *string; Currency,Source,Semantic string; ObservedAt time.Time; ProviderAsOf *time.Time; EvidenceRef string; CreatedAt time.Time}`。
- 共享`ProcurementAuditRow{ID,Actor,Action,Target,Diff,RequestID string; TenantScope *string; CreatedAt time.Time}`；另一成本审计片复用此类型，不重复定义或二次扫描。
- `ProcurementCommandRows{Users []ProcurementApprovalUserRow; Accounts []ProcurementBudgetAccountRow; Bindings []ProcurementAttemptBindingRow; Commands []ProcurementCommandRow; Observations []ProcurementObservationRow; Audits []ProcurementAuditRow}`。
- `ProcurementCommandCheck{Scope string; Scanned ProcurementCommandCounts; Balanced bool; Mismatches ProcurementCommandMismatches}`。Counts固定六组`Users,Accounts,Bindings,Commands,Observations,Audits uint64`，是完整原始源行数。

Mismatches固定uint64：`DuplicateUserRows,DuplicateAccountRows,DuplicateBindingRows,DuplicateBindingOperationRows,DuplicateCommandRows,DuplicateObservationRows,DuplicateObservationOperationRows,DuplicateAuditRows,DuplicateCommandAuditTargetRows,InvalidTextRows,InvalidAmountRows,InvalidEnumRows,CommandShapeRows,SnapshotRows,CommandActorRows,CommandTargetRows,AppliedBindingRows,AppliedObservationRows,BindingCommandRows,ObservationCommandRows,CommandAuditRows,AuditCommandRows`，JSON均snake_case。各字段每源行至多一次，不同字段可同时计数；重复身份/唯一键超首次行只计duplicate、不放大关系核验，非法身份不进索引。

## 2. 文本、金额与严格JSON

所有原始投影字符串（含JSON原文及复用行）只按实际UTF8字节计一次资源；nullable存在时计其值，不将解析后的JSON值重复计资源。必需身份/枚举/字段非空、UTF8合法且无NUL；command.reason允许空。全部audit字段允许历史空字符串、tenant_scope允许NULL；仅本片精确两action进一步要求其合同字段。command operation为小写规范UUID，input_digest为64位小写hex，只核形状，不从缺失的原请求载荷重算或声称证明请求指纹真实性。

Observation status只接受known/unknown/unsupported/error，currency只接受CNY；known必须非NULL非负规范BIGINT amount，其余必须NULL，非NULL金额文本必须非空。source/semantic为1..100字节、evidence_ref为1..256字节，合法UTF8无控制字符。Observation.operation_id为规范UUID；observed_at/created_at与非NULL provider_as_of必须有限（年1..9999）且微秒精度，非法身份、文本或时间形状均按该源行计一次InvalidTextRows，不推断时间先后。Observation引用存在account和user；不以当前user角色/状态推断历史授权。新增金额只核Observation.amount；account/绑定现有金额由既有摘要承担。

command.kind只接受account_create/account_configure/credential_bind/balance_observe，status只接受applied/rejected；reason允许集及前后形状见下节。每个before/after须为唯一闭合JSON文档，长度≤16384字节；只接受JSON null或本kind的精确平坦对象，拒未知/缺失/重复（含转义后同名）key、尾随文档、嵌套object/array、number/bool以及不允许的null。依据既有014迁移`valid_procurement_command_snapshot`逐entry域，所有对象字段为1..1000字节的合法UTF8且无控制字符的string，唯观察amount/provider_as_of可显式null；物理实体TEXT身份不因此增加1000字节上限。拒孤立UTF16 surrogate转义，不接受解码器静默替换身份。解析失败和越快照局部界均计`SnapshotRows`，不是执行错误；整lease资源越界仍返回Resource零结果。

精确对象键：

- account：id,label,provider,state,limit_amount,held_amount,spent_amount,revision,created_at。label/provider为1..100字节、非全空白且无控制字符；state active/paused；limit为非负BIGINT；held/spent为规范非负NUMERIC(100,0)；revision正BIGINT；created_at可解析RFC3339Nano且有限、精度≤微秒。
- binding：id,credential_id,channel_id,account_id,operation_id,bound_at,channel_revision。身份非空，operation_id规范UUID；channel_revision正BIGINT；bound_at同上述有限时间合同。
- observation：id,account_id,operation_id,actor,status,amount,currency,source,semantic,observed_at,provider_as_of,evidence_ref,created_at；金额/枚举/文本同物理Observation，时间可解析RFC3339Nano、有限且精度≤微秒。比较物理时间用instant相等，不比较JSON顺序、空白或offset字节。

单行任一快照坏只计一次SnapshotRows。JSON null合法解析不等于本kind/status语义合法；后者单计CommandShapeRows。字段数、类型和域完整通过后才参加后续精确快照关系比较。

## 3. 命令状态和关系

| kind | applied（reason必须空） | rejected（after必须null） |
| --- | --- | --- |
| account_create | before null，after account；after.id=target、revision=1、held=spent=0；target账户存在 | 不存在合法rejected形状 |
| account_configure | before/after均account；两id=target、created_at相同、held/spent不变；配置四元组(label,provider,state,limit)改变则after.revision=before+1，否则revision相同；target账户存在 | not_found：before null；revision_conflict：before account且id=target、账户存在 |
| credential_bind | before null，after binding；target=credential_id，operation_id=command.operation_id，精确匹配不可变binding（含id/channel/account/bound_at，不比较channel_revision）；绑定行存在 | not_found/revision_conflict/credential_revoked：before null；already_bound：before binding，target=credential_id并精确匹配原不可变binding，但before.operation_id是原绑定命令，不是本次拒绝operation |
| balance_observe | before null，after observation；target=account_id，actor和operation_id等于command，精确匹配物理观察所有字段 | 只允许not_found且before null |

状态/原因/前后null形状和快照内部变化不符计CommandShapeRows；applied create/configure、带before的configure revision_conflict所需账户缺失/target不同计CommandTargetRows。每个command.actor须在users中，否则CommandActorRows。AppliedBindingRows与AppliedObservationRows按相应applied command计其实体缺失或快照不精确；反向每条不可变binding/observation必须有原operation的applied同kind命令及精确after关系，否则分别BindingCommandRows/ObservationCommandRows。Observation自身缺account/user或与原command的actor/target关系不符计ObservationCommandRows，单行最多一次。

不得拿历史account快照label/provider/state/limit/revision/held/spent与当前account比较；流量、后续配置、自动anomaly暂停都可改变当前事实。不得拿绑定快照channel_revision与当前或原命令的channel_revision比较；拒绝already_bound记录的是拒绝当时观察值。不得从进程时钟推断历史先后。不重建完整账户配置链、当前渠道revision或account创建来源覆盖。

## 4. 必须审计及反向扫描

每条command必须恰有一条对应两action审计：action=`procurement.command.`+status，target=operation_id，actor与command一致，tenant_scope必须SQL NULL，request_id必须空，Diff是严格闭合且唯一key的精确对象`{"kind":command.kind,"status":command.status}`。缺少、不唯一或任一字段不符计CommandAuditRows。

反向每条精确两action审计须有原target command并满足相同全部合同，否则AuditCommandRows。两action间相同target也算DuplicateCommandAuditTargetRows，不按action分开稀释唯一性。审计JSON仅解释这两action，不把其他域JSON交给本片decoder；不回显任何原文或错误。

## 5. 错误与验收边界

四个固定错误`ErrProcurementCommandRowsInput/Resource/Canceled/Timeout`；nil context为Input，累计100000行/16MiB越界Resource，context取消/期限分别对应错误；任何执行错误返回零摘要。数据矛盾及非法JSON是成功核验的mismatch，Balanced当且仅当全部mismatch为零。维护新增CommandSummary(permit)，与其他摘要共享真实原Permit、ScanProof和唯一Finish；不独立占第二次ScanProcurement。

验收覆盖所有状态/原因矩阵、严格JSON的重复/未知/缺字段/类型/null/尾随/16384边界、applied binding/observation双向孤儿及精确字段/时间、合法过时账户和channel revision、unknown观察NULL与known BIGINT、审计双向缺失/错actor/status/tenant/request/diff/重复target、100001行/16MiB/取消零结果。reader覆盖三表完整分页及query/scan/next/err/close失败，真实PG使用原API正例和受控坏历史负例、只读前后比较。缺专库须明确SKIP，不得以fixture或编译代替商业/供应商/生产验收。

本片不解释成本proposal/correction审计（由另一独立片负责）、outbox、语义evidence/metering、周期预算交叉图、完整采购事件重放、全部来源组合或生产维护入口；无迁移或在线写入变更。

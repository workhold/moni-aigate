# B-P4 D1：采购账户的字段与闭合事务合同

2026-11-03。承接[账户工作提案](b-procurement-account-d1-proposal.md)、[B-P4设计](b-procurement-prefix-design.md)、[HLD](../../docs/design/HLD.md)、[LLD](../../docs/design/LLD.md)。本页将账户域收敛为可供后续DDL逐项对应的字段、约束、来源和事务合同。**状态：账户域逻辑字段/联合合同已与共同来源及成本域汇合冻结；完整可执行DDL/角色ACL/锁图、固定读集/profile及实际验收尚未完成，不表示D1整体冻结或I1已实施。** 本页不包含SQL实现，不授权普通迁移、provision、启动或在线资金接线；B-P3十一项历史缺证、catalog race时限和R2门禁保持。

## 1. 共同约束与范围

[权限与事务合同](b-procurement-writer-authority-contract.md)已选择固定闭合DB mutation、独立最小NOLOGIN/NOINHERIT writer。writer不拥有表/schema；真实gate和本次历史使用相同的锁后before。APP没有“先改桶，再登记见证”的路径。业务根要求READ COMMITTED，固定受信业务schema、pg_catalog、pg_temp顺序；实际session_user绑定、原业务角色重核、函数与关系schema限定沿上位合同执行。

共同root/source/step以集成人发布的[b-procurement-source-contract.md](b-procurement-source-contract.md)为准。本页使用已裁定的两种scope：request与account_command；root kind包含request_birth、attempt_birth、account_create、account_configure、dispatch、record_finalization、propose_cost、approve_cost、unmanaged_correction、recover_procurement、reconcile。root由DB生成，最终一次INSERT完整不可变结果；公开根不接root、before/after、序号、count/tail、gate结果或自报事务身份。

每个step只有一个canonical typed child。账户相关child为`pp_account_command_sources/account_command`、`pp_attempt_origins/attempt_birth`、`pp_attempt_delivery_sources/attempt_delivery`、`pp_procurement_transitions/procurement_transition`。原command/PE/upstream pointer及account journal是精确双向绑定的companion，不能在同step再算第二个child。attempt_delivery封闭mode为dispatch_permit/finalization/recovery，不新增另一套DispatchSource。

采购账户为平台共享域。account_command root的tenant/request/attempt均NULL，account_target_id为原command的实际target文字；configure not_found不强FK不存在的account。request root不填假account：无attempt、unmanaged或managed都通过实际typed关系关联需要的账户。create的摘要input_target_id为空串，command.target_id与root.account_target_id是DB生成的新account ID，二者不能混用。

本页完整覆盖账户创建/配置、逐attempt reserve、真实dispatch、全部8类PE、自动pause、观察cut、无变化语义及PB后配置。允许额度释放后复用、限额降低/手动重开、跨租户共享账户、零价、unknown/dispute、多次及同额绝对更正；不采用Σupper限制来代替历史完整性。

## 2. 类型、输入与序号

| 域 | 精确合同 |
| --- | --- |
| 新root/journal/transition/source ID | TEXT，DB生成非零规范小写UUID，不接受调用方新ID；原tenant/request/attempt/operation等身份沿精确TEXT关系，不因新字段强转UUID或规范化 |
| 管理命令原ID | actor/operation/account输入沿`procurement.canonicalID`接受集：uuid.Parse成功且String()==原文，含原实现接受的nil UUID；实际actor/account仍须存在。新DB生成账户不用nil UUID |
| limit/upper/bounds/rates | BIGINT，非负；input_bound沿原p为正，output_bound可零。upper为合并两维乘积后向上取整，不能分两维分别ceil；中间用NUMERIC/独立大整数 |
| 累计H/S与p金额 | NUMERIC(100,0)，非负整数；delta为同类型可正负；拒绝超过100位的实际结果，不截断或饱和 |
| ordinal/revision/step_no/count | BIGINT；ordinal/step_no/revision>0，count>=0；任何+1或count增量将溢出MaxInt64时fail closed，不回绕 |
| 时间 | finite TIMESTAMPTZ，DB微秒精度；created/captured仅诊断与期限检查，不用作顺序证明 |
| xid | XID8，由DB取pg_current_xact_id；绑定实际出生/写入的top transaction，不按数值排序，也不接受caller声明 |
| 纯DTO/API金额及序号 | 规范十进制字符串；无加号、前导零、负零、小数、指数。nullable显式NULL，不能用空串/0代替 |

label/provider精确等价`procurement.text(value,100)`：合法UTF8、UTF8字节长1..100、无Unicode Cc控制字符、首尾不属于Go TrimSpace空白集。不得仅用受locale影响的SQL正则放宽或收紧原Go接受集。Cc为U+0000..001F/U+007F..009F；TrimSpace集为U+0009..000D、0020、0085、00A0、1680、2000..200A、2028、2029、202F、205F、3000。PG TEXT不可表示NUL/非法UTF8的输入拒绝，不替换成其他身份。state精确active/paused；原管理数字字符串先按原严格JSON/规范数值合同解析，再传typed参数，DB仍核业务范围。

三种不同序号不得互代：account journal ordinal记录所有账户语义条目；configuration_revision等原account.revision，仅配置元组改变时增长；p revision仅(state,H,S,anomaly)改变时增长。evidence成员count/tail及attempt decision ordinal属于共同成本域，不以p revision推断其次数或完整性。

## 3. 账户本域关系

本节字段均为封闭行合同；不能用开放JSON存储before/after代替。所有canonical child另有共同合同要求的固定`step_kind`列，公共键为(source_id,root_id,step_no,step_kind,created_xid)，普通child的source_id映射id；journal等companion统一使用step_no/step_kind，不另设新root_step别名。仅原业务表的新增pp_root_step物理列显式FK映射该step_no。head是mutable末项投影，只精确引用journal，不另发step。为简洁，`Config`固定表示`label TEXT,provider TEXT,state TEXT,limit_amount BIGINT,configuration_revision BIGINT`；`Buckets`固定表示`held_amount NUMERIC(100,0),spent_amount NUMERIC(100,0),blocking_count BIGINT`；journal的`Reservation`固定表示`revision BIGINT,state TEXT,held_amount NUMERIC(100,0),spent_amount NUMERIC(100,0),anomaly BOOLEAN`。组均展开真实列，before_/after_和before_reservation_/after_reservation_为精确列前缀，不是复合JSON字段。

### 3.1 pp_account_origins

| 字段 | 类型/NULL | 约束 |
| --- | --- | --- |
| account_id | TEXT PK NOT NULL | 精确FK原procurement_accounts.id |
| version | SMALLINT NOT NULL | DB固定1 |
| create_operation_id | TEXT UNIQUE NOT NULL | 唯一原account_create command.operation_id |
| command_source_id | TEXT UNIQUE NOT NULL | 创建的pp_account_command_sources.id |
| root_id/step_no/step_kind | TEXT/BIGINT/TEXT NOT NULL | 同创建command_source的canonical step，step_kind=account_command；root scope=account_command、entry_kind=account_create |
| journal_id | TEXT UNIQUE NOT NULL | 同账户ordinal1、kind=create的journal |
| created_xid | XID8 NOT NULL | 与原account birth/创建source/command/journal相同top-xid |
| created_at | TIMESTAMPTZ NOT NULL | 精确等原account.created_at，finite |

origin只随真正INSERT建立，不注册旧账户。初态Config在create journal.after；before Config/Buckets全NULL，after H/S/blocking=0、configuration_revision=1。不能给已有非零账户装一个“虚拟零起点”。

### 3.2 pp_account_heads

全部NOT NULL：`account_id TEXT PK/FK origin,ordinal BIGINT,journal_id TEXT,updated_xid XID8`，加完整展开的Config和Buckets。ordinal>0；其(account_id,ordinal,journal_id,updated_xid)精确deferred FK journal(account_id,ordinal,id,created_xid)。

INSERT仅允许同事务create origin/journal，指向ordinal1。UPDATE只允许writer按OLD.ordinal+1推进，NEW journal.previous_id=OLD.journal_id，journal.before的Config/Buckets精确等OLD head，NEW head等journal.after；account_id不可改。DELETE/TRUNCATE禁止。head是受校验投影，不是单独的before权威：每次gate/效果都要在业务锁后比较实际原account和真实blocking。

### 3.3 pp_account_journal

| 字段 | 类型/NULL | 精确约束 |
| --- | --- | --- |
| id/account_id/ordinal | TEXT/TEXT/BIGINT NOT NULL | PK id，UNIQUE(account_id,ordinal)，ordinal>0 |
| previous_id | TEXT NULL | ordinal1才NULL；否则同账户ordinal-1的精确前驱 |
| previous_ordinal | BIGINT generated | ordinal-1；与account_id/previous_id构成前驱复合deferred FK |
| root_id/step_no/step_kind | TEXT/BIGINT/TEXT NOT NULL | 指共同canonical step；UNIQUE(root_id,step_no)，不能借别root新来源 |
| created_xid/created_at | XID8/TIMESTAMPTZ NOT NULL | DB实际当前事务/时间；finite，immutable |
| kind | TEXT NOT NULL | create/configure/configure_rejected/reserve/dispatch/released_no_consumption/consumption_unknown/cost_confirmed/evidence_disputed/expired_without_permit/recovered_unknown/cost_corrected |
| source_kind/source_id | TEXT/TEXT NOT NULL | command/budget_event/dispatch三分支；原operation_id/PE.id/attempt.id，UNIQUE(source_kind,source_id) |
| before_*/after_* Config/Buckets | 展开列 | before仅create全NULL；其他before完整，after总完整；组内禁止部分NULL |
| attempt_id/binding_id | TEXT NULL | 涉及p时完整，配置类全NULL |
| before_reservation_* Reservation | BIGINT/TEXT/NUMERIC/NUMERIC/BOOLEAN NULL | 精确列为before_reservation_revision/state/held_amount/spent_amount/anomaly；reserve全NULL；其他涉及p时完整；配置类全NULL |
| after_reservation_* Reservation | 同上 | 精确列为after_reservation_revision/state/held_amount/spent_amount/anomaly；reserve及所有p动作完整；配置类全NULL |
| input_bound/output_bound/upper_amount | BIGINT NULL | 涉及p时完整，从原request/attempt/p冻结源读取；其他NULL |
| cost_version_id/cost_input_rate/cost_output_rate/cost_basis | TEXT/BIGINT/BIGINT/TEXT NULL | 涉及managed p时完整，精确等AttemptOrigin冻结quote和原attempt；非p类NULL |
| gate_extra/gate_checked_at/pb_decision_at | BIGINT/TIMESTAMPTZ/TIMESTAMPTZ NULL | 仅reserve/dispatch完整，extra分别U/0；gate_checked_at为实际账户gate检查时刻，pb_decision_at分别等p.pb_admitted_at/upstream.pb_dispatch_checked_at；不混为同一个时间 |
| pause_event_id | TEXT NULL | 仅本条anomaly=true效果可有，精确等本条PE.id；即便account已paused仍保存真实原因 |
| command_source_id/transition_id/delivery_source_id | TEXT NULL | 依source_kind封闭互斥：command、budget_event、dispatch恰一个；引用本step唯一canonical child |
| command_operation_id/procurement_event_id/dispatch_attempt_id | TEXT generated NULL | 依source_kind由source_id生成一个，逐类型原件FK，不能靠字符串前缀猜表 |

所有Config/Buckets/p金额/计数遵守§2。kind=create iff ordinal=1且previous_id=NULL；每account恰一个create且等origin。root/source/step/top-xid与其canonical child及原件双向精确绑定。原command拒绝后的after_state=NULL不等于journal.after：configure_rejected的journal.after是持锁账户真实不变投影，原command.after_state仍保持NULL。

create/configure/configure_rejected→command；reserve→budget_event且原type=reserved；其余7种PE效果→budget_event且type=kind；dispatch→dispatch。journal.step_kind由source_kind封闭映射：command→account_command、budget_event→procurement_transition、dispatch→attempt_delivery，不能把原件source_kind与canonical step_kind混为同枚举。配置类的所有p/quote/gate/pause列NULL。reserve的p after=prepared/rev1/H=U/S=0/anomaly=false。dispatch的account before/after完全相等，p prepared→sending；其无PE，不伪造budget_event。

### 3.4 pp_account_command_sources

该表是account_command step的唯一typed child；id由DB生成，不是公开参数。全部NOT NULL字段：`id TEXT PK,root_id TEXT,step_no BIGINT,step_kind TEXT,created_xid XID8,created_at TIMESTAMPTZ,operation_id TEXT UNIQUE,actor_id TEXT,actor_role TEXT,actor_status TEXT,kind TEXT,input_target_id TEXT,target_id TEXT,input_label TEXT,input_provider TEXT,input_state TEXT,input_limit_amount BIGINT,input_digest TEXT,command_status TEXT,command_reason TEXT,command_created_at TIMESTAMPTZ,audit_id TEXT UNIQUE`。step_kind固定account_command，kind仅account_create/account_configure；`input_expected_revision BIGINT`仅configure非NULL；`journal_id TEXT`仅create/found configure非NULL。command_created_at精确等原command.created_at，不能用source诊断时间冒充原件时间。

封闭分支：

- create：kind=account_create、input_target_id=''；target_id为本root新account；expected_revision=NULL；status=applied、reason=''；journal kind=create且before全NULL。
- configure applied：input_target_id=target_id，目标存在且受证；status=applied、reason=''；expected_revision=锁后真实revision；journal kind=configure，包括完全相同配置的成功命令。
- configure revision_conflict：目标存在且受证，expected_revision≠锁后真实revision；status=rejected、reason=revision_conflict；原command.before为真实原账户快照、after=NULL；journal kind=configure_rejected，before=after。
- configure not_found：input_target_id=target_id，目标锁后不存在；status=rejected、reason=not_found；原command.before/after均NULL，journal_id=NULL；不得伪造account origin/head/journal。

actor_role/status是当场users FOR SHARE读取的admin/active；它证明该DB入口重核当前角色，不认证浏览器真人会话。完整原input用于独立重算摘要和幂等；不接caller digest。source↔原command必须精确匹配operation/actor/kind/target/digest/status/reason/created_at，found时再双向匹配journal(account/operation/root/step/xid/id)。source.audit_id与原audit双向唯一闭合：actor=actor_id、tenant_scope=NULL、request_id=''、action='procurement.command.'+status、target=operation_id、sanitized_diff正好kind/status两字符串字段。反向pointer采用共同原audit来源列合同，不能仅source单向查存在；相同前缀的绑定/余额观察审计不能冒充账户命令。not_found只闭合命令source/root和原audit，不要求不存在账户的FK。

原command.before_state/after_state始终SQL NOT NULL的JSONB；文中原快照的NULL表示JSON字面null，不能误改成SQL NULL。source/journal可选列使用真正SQL NULL，原JSON快照存在时按既有Account九字段及string值合同匹配，不能增加JSON number或遗漏字段。

### 3.5 pp_procurement_transitions

该表是每个真实PE的唯一canonical child。全部NOT NULL：`id TEXT PK,root_id TEXT,step_no BIGINT,step_kind TEXT,created_xid XID8,created_at TIMESTAMPTZ,tenant_id TEXT,request_id TEXT,attempt_id TEXT,account_id TEXT,binding_id TEXT,cause_kind TEXT,cause_source_id TEXT,event_id TEXT UNIQUE,journal_id TEXT UNIQUE,event_type TEXT,event_key TEXT,evidence_ref TEXT,held_delta NUMERIC(100,0),spent_delta NUMERIC(100,0)`；step_kind固定procurement_transition；再加`after_revision BIGINT,after_state TEXT,after_held NUMERIC(100,0),after_spent NUMERIC(100,0),after_anomaly BOOLEAN,input_bound BIGINT,output_bound BIGINT,upper_amount BIGINT,cost_version_id TEXT,cost_input_rate BIGINT,cost_output_rate BIGINT,cost_basis TEXT`，均NOT NULL。`before_revision BIGINT,before_state TEXT,before_held NUMERIC(100,0),before_spent NUMERIC(100,0),before_anomaly BOOLEAN`仅reserved全NULL，否则完整。p字段与journal对应Reservation组逐值相等；input/output/U/quote与其同名列相等。event_key沿原1..400字节界并全局唯一，event_type封闭为§8八种；evidence_ref<=256字节并精确等对应source，不能只满足长度。

cause_kind/source_id封闭四类，使用generated discriminator和真实deferred FK：

| cause_kind | 精确typed source | 允许PE |
| --- | --- | --- |
| attempt_birth | pp_attempt_origins | 仅reserved |
| evidence_choice | pp_evidence_choices | released_no_consumption/consumption_unknown/cost_confirmed/evidence_disputed |
| approval_consumed | pp_approval_consumptions | 仅cost_corrected |
| recovery | pp_recovery_sources | expired_without_permit/recovered_unknown |

cause必须本root、同tenant/request/attempt，DB锁后出生，其step_no严格早于transition step；声明的输出transition/event/journal ID精确等本行。cause的step与结果step是不同步骤，只有transition/PE/journal三者共享结果step，不能错误要求cause.step_no也相等。不能直接消费NormalCostDecision来修改p，不能用同事务任意proposal或“最新decision”替代来源。身份/成员/批准及恢复来源的完整字段由共同成本合同定义；账户域在依赖未齐时不自行放宽它们。

transition↔原PE↔journal在account/attempt/event/root/step/xid/id上双向deferred绑定；一个PE恰一个transition/journal，缺任一方向不能提交。transition不是after trigger从终态拼出来的before；before在真实锁后capture，target由固定状态机推导，实际RETURNING必须逐值等target。每个transition step另有共同pp_attempt_nodes的唯一node；managed reserved是出生后node1。journal不再另建node或第二条attempt序列，其他无金额变化的typed step也按共同合同进入同一attempt链。

### 3.6 原表增加的绑定列

| 原表 | 字段与NULL联合 | 保护及反向闭合 |
| --- | --- | --- |
| procurement_accounts | pp_version SMALLINT、pp_created_xid XID8、pp_origin_journal_id TEXT；legacy全NULL或v1完整 | 真正create时DB设置，初始H/S=0/rev1；身份/出生列不可改，精确origin/create journal闭合 |
| procurement_reservations | pp_created_xid XID8、pp_origin_journal_id TEXT、pp_last_journal_id TEXT；全NULL或完整 | tracked新p必须完整且受证account/attempt；origin永不改，last每个真实PE及dispatch推进到本次journal |
| procurement_budget_events | pp_root_id TEXT、pp_root_step BIGINT、pp_transition_id TEXT、pp_journal_id TEXT、pp_created_xid XID8；全NULL或完整 | tracked account的每个新PE必须完整，DB设置且immutable；双向闭合transition/journal |
| procurement_commands | pp_source_id TEXT、pp_root_id TEXT、pp_root_step BIGINT、pp_created_xid XID8及nullable pp_account_journal_id TEXT | tracked account_create/configure命令前四项完整；journal NULL当且仅当not_found。其他既有kind不冒用本协议 |
| upstream_attempts | pp_dispatch_source_id TEXT、pp_dispatch_root_id TEXT、pp_dispatch_root_step BIGINT、pp_dispatch_xid XID8；另有nullable pp_dispatch_account_id/pp_dispatch_journal_id TEXT | 首次真实dispatch四项全完整且不可改；managed时account/journal完整，unmanaged时二者NULL。即便delivery后来completed/failed/unknown也保留真实许可 |

原upstream pointer的canonical source为pp_attempt_delivery_sources(mode=dispatch_permit)，与本次真实OLD prepared→NEW sending、原PB checked_at、本root/step/xid双向闭合；同attempt最多一个发送许可。不能把unmanaged pointer的账户NULL误解为没发生dispatch。

p.last必须精确指当前最后journal.after的(account,attempt,revision,state,H,S,anomaly,id)，并由guard确保本次p.before等实际old p及其旧last.after；老journal不得永久FK到可变p.current revision。journal.previous_id是同账户全局上一条，可能属于别的attempt或配置；p.old last是本attempt上次真实p动作，两者不能强制相等。连续两次同额更正即使p revision相同，last仍切到新journal。上述每一复合FK在引用目标显式建立对应完整UNIQUE，不能只依赖id存在后再比较部分金额；源类型discriminator与NULL联合约束也必须是数据库实际约束。原014 identity/transition/守恒和020 PB source约束保留；新约束是加强，不能删除旧规则或忽略legacy差异。

原人工余额观察继续是追加的元数据证据，不覆盖H/S/limit、不给预算充值、不作为供应商认证余额；它本身不改变Config/Buckets/p，不创建账户journal。观察来源仍沿原procurement_balance_observations/command/audit合同核验，不因账户新前缀而宣称自动供应商适配已实现。

## 4. 公共函数、返回和原API接缝

只给精确APP LOGIN授以下受审根EXECUTE；所有业务mutation为VOLATILE/PARALLEL UNSAFE。下列参数全部必填且禁止SQL NULL，不包括digest、root/source输出ID、账户桶或caller proof；NULL必须明确拒绝，不能由STRICT函数静默返回空结果。Go公开Store/API签名保持原样，由显式prospective executor选择候选，不按库内函数存在自动启用，失败不回退旧SQL。

```text
pp_create_account(actor text,operation text,label text,provider text,state text,limit_amount bigint)
pp_configure_account(actor text,operation text,account text,expected_revision bigint,
                     label text,provider text,state text,limit_amount bigint)
pp_complete_attempt_birth(tenant text,request text,attempt text)
pp_permit_dispatch(tenant text,attempt text)
pp_recover_procurement_attempt(tenant text,attempt text)
```

create/configure返回一行：`outcome(applied/replay),operation_id,actor,kind,target_id,status,reason,before_state JSONB,after_state JSONB,created_at TIMESTAMPTZ,root_id,account_journal_id?`。中间原command字段与既有Command返回逐值一致；outcome=replay时返回旧受证root/journal身份，不新建任何源；status仍是原applied/rejected。持久化rejected命令的outcome=applied，不另造rejected outcome。not_found只有account_journal_id为NULL，target_id仍是原目标文字。所有新applied outcome都指本次sealed root。prospective遇未受证旧账户/命令不能补登记；明确返回共同错误表的untracked-domain拒绝。

birth返回一行`outcome(applied),root_id,attempt_id,management_mode(managed/unmanaged),account_id?,reserved_event_id?,account_journal_id?,admitted_at?`。managed后四项完整，admitted_at=p.pb_admitted_at；unmanaged后四项全NULL。新origin的management_mode统一managed/unmanaged；原API既有legacy_unmanaged展示标签不因此改动。只能关闭本事务真正新的attempt；已有完成origin重复调用不生成第二个root/来源，按共同birth错误表拒绝。原RecordAttempt的ON CONFLICT重读在Go原流程完成，不调用birth、不凭此登记旧origin。

dispatch返回一行`outcome(applied),checked_at TIMESTAMPTZ,root_id TEXT,delivery_source_id TEXT,account_journal_id TEXT NULL`。checked_at精确等原PB有限时间，managed的journal非NULL、unmanaged为NULL；其余列均非NULL。ID仅描述这次持久事实，不授跨事务许可。没有replay成功分支；已非prepared或已有真实许可的第二次调用沿原attempt_already_dispatched拒绝，无新root/journal。Go PrepareDispatch仍原error接口，在COMMIT前不能向模型客户端使用许可；COMMIT未知不能重新发请求。

recovery由共同成本合同拥有，签名统一两参；request必须从原attempt派生并在锁后交叉核归属。返回outcome(applied/noop)及recovery_source/event/journal身份：applied三类身份完整并有sealed root；noop无本次新root/source/event/journal。原批扫描只是选择候选，不构成deadline或permit证明。request自动birth trigger不是公开SQL业务根，不要求返回此outcome行。

错误区分业务拒绝、原管理命令可提交的rejected回执、数据库执行/完整性失败和未知提交。semantic_code沿原invalid_procurement_configuration/procurement_operation_conflict/procurement_forbidden、procurement_paused/procurement_budget_exceeded、cost_unknown/cost_quote_stale、procurement_binding_changed、request_deadline/invalid_key、attempt_already_dispatched及共同合同新增闭合错误；**SQLSTATE数值与精确统一映射由共同来源合同冻结，本页不另造一套编号。** 不解析本地化异常文本，不将任意约束失败包装成可提交rejected。

## 5. 命令摘要与账户管理事务

DB独立计算原lockCommand摘要，调用方不传input_digest。固定无空白JSON外层按`Kind,Target,Payload`；Create Payload按`operation_id,label,provider,state,limit_amount`，Configuration按`operation_id,expected_revision,label,provider,state,limit_amount`；所有值为原Go JSON字符串，限额/revision即使SQL参数为BIGINT也编码成规范十进制字符串。Kind分别account_create/account_configure；Target分别空串/输入account ID。actor不在摘要内，由幂等逻辑单独比对。字符串编码复用[已冻结identity叶子](b-procurement-identity-contract.md)的pp_json_string_v1规则，UTF8字节sha256为64位小写hex，不能jsonb::text重排。原DB生成新account ID不进入create请求摘要。

顺序保留原[lockCommand](../../internal/procurement/store.go)：typed输入校验→advisory xact lock(hashtextextended('procurement:'+operation,19019))→读取原command并比较actor/digest→users FOR SHARE重核admin/active→同载荷回执才快返。不同payload/actor的冲突错误优先于随后角色检查，不能为了与其他根统一而改原错误优先级。快返也要当前有权限，不因旧回执绕授权。

新create：DB生成account/root/source/journal IDs；INSERT原account初态零桶/rev1；以真实RETURNING构造create journal/origin/head；保存原command（before=NULL、after=真实原Account快照）及原procurement.command.applied audit；source与原件精确闭合，最后seal root。audit沿原action/target/payload合同，不添加财务outbox或假余额观察。

新configure：只取目标account NO KEY UPDATE→head FOR UPDATE，锁后新statement读完整原account、head和真实blocking。先核受证一致性，再按原expected_revision区分found applied/revision_conflict；not_found保留原拒绝回执。配置低于H+S允许，active但有blocker也允许保存；之后reserve/dispatch仍按gate拒绝。只有(label,provider,state,limit)变化才令configuration_revision+1；applied相同配置仍追加configure journal。拒绝revision_conflict也追加before=after的configure_rejected journal，留下真实CAS观察；not_found没有账户journal。配置不能改H/S/blocking、p、PB控制配置或发送任何模型请求。

## 6. 真实锁与gate

通用锁序保留各入口实际前层：命令/业务advisory与当前角色→tenant/membership/user/Key/model→channel/credential→customer reservation→attempt→customer PB（dispatch需要）→procurement account NO KEY UPDATE→supplier PB namespace/control→p FOR UPDATE→proposal（approval需要）→新head。账户head在p之后，纯account配置可跳过其余层；任何helper不能从account/head反向取request/attempt/channel。原只读定位用于选择锁路线，不能用其值作为锁后资格。

绑定缺席也必须同步：RecordAttempt已持channel SHARE；binding原Go与raw INSERT trigger均取channel UPDATE、credential SHARE再account。birth重入原前层锁并在锁后新statement查binding。先看不到p或binding的一次旧快照不能决定unmanaged。dispatch重新核当前binding与冻结p.binding，后来才绑定的旧unmanaged不可派发。

定义实际账户`A=(Config,H,S,C)`，其中C精确统计原p中`account_id=A.id AND anomaly AND state IN ('unknown','disputed')`。gate前锁后新SPI statement读取原A、head及真实C并核一致；不得拿head与自身比较自证。建议固定新partial index等价此谓词，但索引不是完整性来源。金额计算用NUMERIC，预算允许H+S大于limit但不能为负。

- reserve：从受证request硬界与AttemptOrigin冻结两维known quote重算`U=ceil((input_bound*input_rate+output_bound*output_rate)/1,000,000)`，核U<=MaxInt64；真实before要求state=active、C=0、H+S+U<=limit。
- dispatch：同组before要求state=active、C=0、H+S<=limit，extra=0；不重新冻结U或价格，不重复预占。
- 后续PE、cost reset、更正和有利收尾不执行上述准入gate；账户paused、限额收紧、存在异常不能阻断保存真实消费/释放或保守unknown/dispute。旧客户结算/退款不进入采购effect。

实际source与journal用同一before变量和固定target；p/account实际RETURNING逐值等target后才推进head。任何deadline/报价/PB/权限/约束失败整体回滚，没有可提交成功条目。gate_checked_at只记录执行时刻，不替代已锁配置/金额前缀；PB decision time另列且失败会回滚之前的临时变更。

## 7. reserve与真实dispatch完整顺序

### 7.1 managed birth/reserve

1. 原request/customer reservation前层及channel/credential同步，attempt必须是真正本事务新INSERT、initial prepared/unknown/NULL；冻结身份/quote/route_decisions不可修改。锁后DB复核原readAttemptChecks与成本policy、绑定、request硬界和quote，不相信Go自报已验证。
2. account→supplier namespace/control→head；等待后新statement核当前quote仍等冻结quote、request deadline、Key资格和账户gate before。创建本root完整AttemptOrigin：managed分支指实际binding/account及DB预分配输出transition/event/journal IDs；unmanaged分支不得有p/PE/account journal。managed源先出生供私有effect消费，输出deferred闭合。
3. INSERT p=prepared/H=U/S=0/anomaly=false/rev1，原020触发器在真实source出生点生成pb_control/pb_admitted_at并绑定same-tx attempt；新origin/last pointer由DB设置。
4. 按p.pb_admitted_at逐实际supplier control调用原pb_gate(extra=U)，取得相应原期window；即使U=0也执行真实gate与原source记录。不用较早clock选择周期。
5. 以该同一admission时间重新核Key expires、customer reservation deadline、request deadline及当时有效采购quote。按原cost_versions的effective_at DESC/created_at DESC/id DESC选价；channel锁不阻止已发布版本随时间生效。
6. INSERT唯一reserved PE，更新原account H+=U，调用§9同PE的PB effect；从实际p/account返回构造transition/journal、推进head/p.last，deferred闭合AttemptOrigin、PE/PB和source。最后seal root。任一失败整体回滚attempt/p/PE/PB/源；不存在外部可调用的“稍后补镜像”。

### 7.2 dispatch

1. 由tenant/attempt定位request；按原PrepareDispatch重核principal、Key CIDR/scopes/regions、channel enabled/quarantine与binding isolation、credential状态、客户source/attempt prepared及期限。已持customer reservation/attempt后先customer PB namespace与按ID排序control，再account/supplier PB/p/head。
2. 新statement读账户gate before与当前绑定，要求p prepared且绑定未变；managed执行extra=0账户gate。unmanaged须明确AttemptOrigin分类且当前仍无binding，无account journal。
3. managed将p prepared→sending，p revision+1，H/S/anomaly不变；随后真正UPDATE upstream prepared→sending，不能以p.sending替代许可。最早的candidate invoker role guard在原pb_dispatch_guard之前拒绝APP raw sending；闭合root已先取account/PB，避免trigger反向补锁。
4. 原pb_dispatch_guard复用scope/control锁，核customer/supplier source，所有control等待后取唯一pb_now，逐control执行pb_gate(extra=0)，DB写原pb_dispatch_checked_at。这个gate可观察新window，不生成财务PE或假PB effect。
5. 用实际RETURNING checked_at重核Key/customer/request deadline及当前quote/policy；失败全回滚临时p/upstream sending与窗口观察。成功后保存pp_attempt_delivery_sources(mode=dispatch_permit)完整原before、实际after、checked_at和planned journal，写不可改upstream pointer。
6. managed追加dispatch journal：account前后完全相等、p prepared→sending的实际revision变化完整，p.last推进。无PE。root封闭后返回§4 typed事实行，Go成功COMMIT后才使用这一次许可。

## 8. 全部PE状态与无变化语义

记p原值为(h,s,state,anomaly,r)、U为冻结上界，N为本次最终绝对成本，account原桶为H/S/C。每个实际PE均有`held_delta=h'-h,spent_delta=s'-s`，reserve以h=s=0计算；account目标为H'=H+deltaH、S'=S+deltaS，C'=C-old_block+new_block，block精确谓词同§6。target状态合法性沿原014；实际p revision仅p四元组改变才r+1，不改变为r；reserved固定1。

| PE type / event_key | 来源与先决条件 | p目标与附带行为 |
| --- | --- | --- |
| reserved / reserve:<attempt> | 新managed AttemptOrigin；真实quote/期限/账户/PB gate通过 | prepared,H=U,S=0,anomaly=false,rev1；U=0仍发行PE/journal |
| released_no_consumption / release:<attempt> | 新evidence member；非终态；reason not_sent/rejected_no_consumption | released,H=0,S=s,anomaly=false；原合法可达前驱s=0，不能以客户refund作为此source；无消费理由优先于usage状态 |
| consumption_unknown / unknown:<attempt> | 新member、非终态、usage非confirmed_final、原state非unknown | unknown,H=h,S=s,anomaly=false；不自动pause |
| cost_confirmed / settle:<attempt> | 新member、非终态、confirmed_final | settled,H=0,S=N；N用冻结两维rate合并ceil；anomaly=N>U或任一token超对应bound；仍完整记账并必要pause |
| evidence_disputed / conflict:<attempt>:<meter> | 新member；实际conflict或终态矛盾；原state非disputed且该key未出现 | disputed,H=max(U-s,0),S=s,anomaly=true；保持已记成本，必要pause，单独记录真实cost reset |
| expired_without_permit / release:<attempt> | 锁后request已过deadline；p与upstream均prepared且无真实许可pointer | released,H=0,S=s,anomaly=false；upstream→failed；不重发模型 |
| recovered_unknown / unknown:<attempt> | 锁后deadline到期、p为prepared/sending且不满足无许可释放 | unknown,H=h,S=s,anomaly=false；upstream→unknown；真实许可不能消失 |
| cost_corrected / correction:<approval-op> | 本root完整ApprovalConsumption，当前双方角色/双审/成员屏障/revision通过，p非prepared/sending；减价须invoice | settled,H=0,S=N,anomaly=(N>U)；真实provider correction/attempt cost/audit/outbox精确闭合；同额或basis-only仍有新PE/journal |

evidence分支的顺序固定：原Usage+Reason identity先去重；新member后，released遇正confirmed消费且非无消费理由，或settled正spent后遇无消费理由，先判矛盾，再处理终态普通证据skip。不能把终态判断提到矛盾检查之前。原meter碰撞的conflict为实际incoming source事实，不从旧meter身份猜本次attempt。

auto pause与致异常PE合并在同一账户journal：anomaly=true令account.state=paused，active→paused使配置revision+1，已paused不增加；pause_event_id仍绑定本PE。合法更正或解除blocker不自动active。settled超界anomaly会pause但C不增加；管理员重开active仍须金额gate；disputed即使人工active仍C>0阻准入。更正anomaly只按本次N>U，不能继续把历史token越界标志当本次必然异常；历史异常仍保存在旧journal。

全部真实后续PE原子顺序：锁后capture before/真实source→计算target→UPDATE p→必要account pause→若disputed记录实际attempt cost reset→INSERT PE→UPDATE account H/S→同PE PB effect→核实际RETURNING→transition/journal/head/p.last→seal root。原成本正常资格decision位于采购效果之前；conflict外层可能再做unknown→unknown reset或抑制已裁定成本，另由cost链按实际顺序记录，不能只保留最终值。

没有PE时不制造account no_effect journal，完整分支包括：identity重复（不增member）；已disputed的新冲突；conflict event_key已存在；settled/released/disputed的普通非矛盾新member；已unknown的非final新member；unmanaged成本路径。新member可以增长成员count/tail而p revision/account ordinal不变。每个这些managed decision必须保存§10真实观察cut及本分支原因，不能凭无PE声称检查未发生。原event_key存在分支可能出现在曾dispute→已更正后同meter不同reason的新语义，不得漏掉。

相反，applied相同配置、revision_conflict的found管理拒绝、U=0 reserve、dispatch无桶变化、同额/仅basis更正的实际PE均有各自journal。纯幂等回执重读不新增root/source/journal/PE。恢复已非eligible或deadline尚未到达为noop，无新来源；原恢复扫描未在事务内重核deadline，新候选此处是待实现补强，不可写成现代码已具备。

## 9. PB调用与后配置完整覆盖

DB函数不能调用Go tx helper；新SQL writer必须逐行为等价实现其已审定调用闭包，并复用原SQL invoker/trigger合同。不得仅写“给PB SELECT”或让Go在根返回后补effect。PB不是采购完整历史替代：没有control也仍有每笔原PE与account journal。

1. account锁之后原`pb_sync_unconfigured('procurement',account)`取得真正first-configuration namespace，随后现有procurement control FOR NO KEY UPDATE。函数级search_path必须受信业务schema首位，以保持原current_schema advisory namespace；已配置与未配置两类都同步。
2. 新p的AdmitSupplier等价步骤：核p/attempt当前source/top-xid、marker与实际control一致、p初态/U；按p.pb_admitted_at对control执行pb_gate(U)，查到同原期window。没有control不创建allocation/event；p仍保留DB admission时间、marker=NULL。
3. PE已出生并更新原account后，`pp_apply_supplier_pb(root,attempt,event)`读取实际PE和p，核同account/attempt、PE是当前top transaction真实新tuple；不是接受delta或target参数。无control只允许p.marker=NULL并返回，不虚构PB source。
4. 有control且已有allocation：核原marker/origin/source/upper/admission/window及完整前效果，核旧sourceH+PE.delta=p.actualH、旧sourceS+PE.delta=p.actualS。该PE已应用则只在精确旧effect/allocation身份相等时内部快返；不同PE即便delta=0仍是新PB event。
5. 有control且新allocation：marker=control必须是同事务真实reserved PE、key=reserve:attempt、delta=(U,0)，原pb_observe(admission_at)取得实际window，再INSERT origin=new/baseline0 allocation，追加同PE的periodic event。创建/观察窗口触发已有B-P1 observation origin/head/journal，不得遗漏其最小权限。
6. 后来首次配置control：原PB Commander沿account→namespace/control导入未完成tracked p，origin=legacy_import、baseline=p.spent，window/admitted_at=NULL，原p.pb_control仍NULL。这里legacy描述相对control出生，不表示采购account历史缺失；不回写采购origin，也不新增无实际账户变化的journal。
7. p在control创建时已terminal而未导入，之后真实dispute/correction需legacy_catchup：本次新PE之前实际累计held必须0、spent非负，旧累计+本PE精确等当前p；baseline=此前累计spent、window/admitted_at=NULL。不能因prospective账户新建就删除此分支。
8. 追加periodic event必须`source_kind=procurement_event,source_id=该PE.id`；原trigger生成seq/before/after并推进allocation、control及存在的原window，PB spent=max(p.spent-baseline,0)。零delta仍推进event/allocation/window revision/control seq。收尾更新原admission window，不搬到当前新窗口；import/catchup无原window时不捏造窗口。
9. reserve首次gate执行周期cap；后续真实消费/释放/更正保存事实，不因paused/超预算或当前周期切换拒绝真实账务。dispatch只原gate(0)/窗口观察，无PE effect。supplier effect不能用客户settlement/refund代替，客户资金路径与采购effect事务边界保持原合同。

writer需要精确SELECT及窗口/allocations/events INSERT、原trigger所需财务/seq/tail/revision UPDATE和B-P1 observation写面；**不授control INSERT、enabled/limit/period/config revision mutation或任意PB配置命令权**。完整逐签名EXECUTE/列ACL及PB/B-P1触发器传递权限由集成人独立闭包合同冻结和actual LOGIN验证，未齐前候选不可启用。

## 10. 观察cut、来源出生与双向约束

managed cost/evidence/proposal/approval decision的账户观察cut为`account_id TEXT,account_ordinal_cut BIGINT,account_tail_cut TEXT`，三者完整且FK同账户journal(account_id,ordinal,id)。从已持account→supplier PB→p→head后新statement读取，精确等当时head与实际account一致投影；关联source保留当时p状态/revision及member cut。它表示“实际检查时所观察的账户前缀”，不是一次新reserve/dispatch gate。后续同事务可继续合法操作使head前进，旧cut不能被要求等今天current head。

cause必须先出生才能被helper消费。共同裁定对AttemptOrigin/EvidenceChoice/ApprovalConsumption/RecoverySource：锁后保存真实before、成员屏障/角色/决策，由DB预分配transition/PE/journal及其他真实输出ID并计算唯一合法target，一次INSERT完整immutable source；向尚待产生的本root输出使用deferred精确FK。helper读取这个source执行，再用实际RETURNING逐值核source声明目标；所有输出和root最终seal才能提交。它不是允许caller预建见证，也不是对未完成effect提前发权；任一不符全回滚。不能一边要求“source已出生”，另一边把唯一source留到effect后才INSERT。

私有接口固定为`pp_account_effect(root_id text,attempt_id text,source_kind text,source_id text) -> (event_id text,journal_id text)`，source_kind仅§3.5四类；返回身份等DB source预分配输出，非新权限token。`pp_apply_supplier_pb(root_id text,attempt_id text,procurement_event_id text) -> void`只能内部调用；append/head helper不公开。APP/PUBLIC/projector/peer/worker无这些helper EXECUTE。

RootStep按kind的generated nullable discriminator向具体canonical表真实deferred FK，具体child以root/step/固定kind/id/top-xid反向FK；一步恰一child。账户的source-kind discriminator再把journal与原command/PE/upstream真实pointer及canonical child闭合。source反向遍历也必须发现遗漏journal；仅从journal查source存在不足以证明覆盖。跨root引用只允许明确不可变前驱，不得借其他root的新PE充本次输出。

原account H/S、head H/S与全部p的H/S、全部PE delta累计在提交时精确一致，C等全部真实p blocker；journal连续重放也得到相同Config/H/S/C。root模板核必有/禁有/相对顺序而非只核step_count；同事务多root/多来源仍逐一闭合，不靠xid本身选来源。源/历史拒UPDATE/DELETE/TRUNCATE，head仅准确前驱更新；deferred检查只核闭合，不在COMMIT才补before。

## 11. 直接写面、锁权与故障

候选APP撤销账户配置/金额、p全部保护列、PE、account_command及tracked cost/quote/source/dispatch pointer直接DML；历史表无DELETE/TRUNCATE。撤销涵盖表级/列级/default ACL及所有INHERIT/SET/ADMIN路径，旧provision的blanket grant不能恢复旁路。原账户其他命令（绑定/人工余额观察）可保留既有入口，但invoker guard拒绝APP伪造受保护account_create/configure；不得因此把客户支付/充值等无关审计/outbox写入全部破坏。

原APP/PB Commander仍有SELECT FOR UPDATE/NO KEY UPDATE需求，PostgreSQL要求至少一列UPDATE权限。共同候选选择是仅给必要原表的created_at列UPDATE锁权，并由原immutable guard及candidate SECURITY INVOKER guard拒绝APP真正UPDATE，包括原值赋回；纯SELECT行锁不触发UPDATE guard。不新增pp_lock_noop列，也不为锁权重授金额/state/identity；guard本身不能改definer后误以为current_user还能区分APP。

此primitive已有独立PG16实际APP LOGIN实验：[复现程序](../../docs/verification/testdata/procurement-account-lock-acl/main.go)在随机schema/受限NOINHERIT角色验证SELECT-only四种行锁均42501、仅授UPDATE(created_at)后四种均可取；其中NO KEY UPDATE真实阻塞owner同类竞争锁，设置150ms lock_timeout后得到55P03。ctime原值赋回/修改均被invoker guard拒绝，held UPDATE/INSERT/DELETE/TRUNCATE四类拒绝，数据不变且schema/角色清理为零。**实验guard无条件拒绝UPDATE，只证明列锁权与行锁不执行UPDATE trigger；它没有原014业务DDL、writer允许分支、完整PB Commander/绑定调用或最终ACL/profile。** 后续须按真实目标表/触发器/角色闭包重新验完整组合，不将primitive通过写成writer或经营验收。

原upstream真实sending的早期guard必须先于pb_dispatch_guard，后期source/pointer校验在原PB产生时间后验证实际NEW；最终catalog固定全部trigger名字、时点、顺序、函数owner/proconfig/正文/依赖。无GUC、query text、角色名参数、trigger_depth授权；APP不能CREATE同schema、换函数、禁trigger、SET replication role或取得writer/owner。

锁等待/取消/期限/报价/PB拒绝、权限/资源错误、statement/savepoint/deferred失败均回滚本次业务与历史，不留已提交ordinal缺口。沿原lock_timeout=3s、statement_timeout=5s及上层绝对期限，不因新函数扩时。只重试原已知40P01/40001的已回滚事务，最多原3次；管理命令原不自动重试的行为保持，不能凭新writer改网络语义。COMMIT回复未知只按精确operation/root进行只读核对，不再派发模型、不换operation重做批准或effect；数据库事务未知与上游消费unknown分开。

## 12. 本合同退出条件与待完成依赖

账户域后续实现至少需要真实APP新历史：共享4来源U=240/limit1000完整收尾；700释放再700合法复用与同时700+700拒绝；配置降低/暂停后仍可收尾；0价token越界；unknown/dispute/人工active仍block；24→25、多更正、同额/basis-only；新增同count不同成员拒旧proposal；原/后配置PB与跨窗；terminal catchup；恢复有/无许可、锁后deadline；同事务多root；无PE分支观察cut。独立oracle手算金额/状态，不调用writer作expected。

actual LOGIN攻击覆盖raw source/桶/quote/初态/历史/pointer、孤立/跨root/tenant/attempt source、预造输出、跳号/分叉/漏尾、先改before再补证、role/default ACL/search_path/临时对象、禁trigger/TRUNCATE，以及取消/EOF/Close/资源/未知提交。旧fixture不能补猜测origin；新profile不得清零旧Base Unproven。账户成功也不能代替request sticky、incoming、normal cost资格及unmanaged完整成本链。

共同来源与成本/request的字段/联合逻辑已汇合，PB/B-P1最小闭包另见[专门合同](b-procurement-pb-acl-contract.md)。created_at锁权原语有[限定实证](../../docs/verification/procurement-account-lock-acl.md)，完整真实guard/014/角色组合仍待验。仍须配套完成可执行PK/FK/模板/错误映射、完整根的函数与表列ACL/锁图、独立纯DTO/replayer、新完整scanner/basis/catalog/roots/历史兼容与预算、actual APP全场景及旧接口回归。身份codec叶子及两个锁实验只具有各自范围，不解除这些门禁。本页设计交付不写成DDL已安装、writer已实现或商业验收通过。

## 13. 实际源码对应

- [账户创建/配置与原摘要](../../internal/procurement/store.go)、[原管理输入/返回](../../internal/procurement/types.go)。
- [采购gate、reserve、8类PE、双审与恢复](../../internal/billing/procurement.go)、[attempt/真实dispatch/finalization](../../internal/billing/evidence.go)、[冻结与派发报价](../../internal/billing/cost_policy.go)。
- [原账户/p/事件/command/提案DDL](../../migrations/014_procurement_accounts.sql)、[PB birth/gate/真实dispatch/效果DDL](../../migrations/020_periodic_budgets.sql)。
- [PB供应商步骤](../../internal/periodicbudget/tx_supplier.go)、[PB实际dispatch](../../internal/periodicbudget/tx_dispatch.go)、[PB锁](../../internal/periodicbudget/tx_locks.go)、[后配置导入](../../internal/periodicbudget/commander_configure.go)。

统一架构正文、变更记录和manifest由根在所有并行结果汇合后语义复核、refresh与只读检查。本片只新增本合同，不修改实现、已应用SQL、默认构造器、权限、部署或测试记录。

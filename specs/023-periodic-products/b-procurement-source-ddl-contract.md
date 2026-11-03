# B-P4-D1-SQL-S：共同来源与链的实际约束材料

2026-11-03；承接[共同来源](b-procurement-source-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)、[身份字节](b-procurement-identity-contract.md)、[PB ACL](b-procurement-pb-acl-contract.md)、[完整 ACL](b-procurement-full-acl-contract.md)、[执行](b-procurement-execution-contract.md)、[重放/读取](b-procurement-replay-read-contract.md)及[物理布局](b-procurement-schema-contract.md)。本页先冻结关系约束，再由 [source-constraints-v1.sql](../../internal/procurementprefixschema/materials/source-constraints-v1.sql)逐边表达。当前布局经 delivery 多态 cause、reset 反向 cause 及新 settlement 反向槽校准为 **715 列/68 个 generated/647 个非 generated**；原 711 计数不是禁止设计演化的门禁。

**本材料包不执行安装或业务授权。** 它只添加 PK/UNIQUE/CHECK/FK，不创建表、函数、trigger、权限或可返回成功的 validator。完整 32 表 writer、只读闭包、DDL 安装器、独立重放/catalog/reader/profile 和实际 LOGIN 验收仍未完成，不能据本页关闭 D1、I1 或生产接线。

## 1. 安装分段、所有权与前提

SQL 仅用 `ALTER TABLE {{MONI_SCHEMA}}.<固定表>`；schema 占位只允许安装器替换为一个安全引用的受信 schema 标识符。所有 REFERENCES 都显式限定 schema，固定内建函数用 pg_catalog 限定。材料分成且仅分成以下三段：`-- @phase keys`、`-- @phase checks`、`-- @phase foreign-keys`。集成人先创建全部 32 表与 generated 列、扩展原件，再汇合各域全部 keys、checks、foreign-keys；不能按单文件从头到尾执行来猜跨域顺序。唯一键均不可延迟，循环外键均 `DEFERRABLE INITIALLY DEFERRED`、默认 NO ACTION；不使用 CASCADE、NOT VALID 或关闭旧约束。

原 001–022/B-P1 保持原内容。原表新增列及所有原表目标 UNIQUE 由集成人的 original 材料统一负责；本页只添加明确列出的原表反向 FK，不另造同义索引。约束名前缀 `pp_s_`，每名唯一且不超过 PostgreSQL 63 字节。

本共同域拥有 18 canonical 表的基础 PK、公共 UNIQUE 和 canonical→root/step 关系；其他域不重复这些基础键。非 canonical 的账户 origin/head/journal、客户 finish/ledger/PB 表由相应 domain 负责 PK 与专有键。本页另负责 root/step、request/attempt head/node、meter origin/conflict companion、evidence member 的基础键。

## 2. 公共目标键与 root/step 双向协议

`pk` 在 RequestOrigin 为 `request_id`，AttemptOrigin 为 `attempt_id`，其余 canonical 为 `id`。18 表均提供以下非延迟 UNIQUE：

```text
(pk,root_id,step_no,step_kind,created_xid)
(pk,root_id,created_xid)
```

有真实 R 字段的普通表再提供 `(id,root_id,created_xid,tenant_id,request_id)`；有真实 A 字段时同时提供该 R5 与 `(id,root_id,created_xid,tenant_id,request_id,attempt_id)`。自然键去重后 RequestOrigin 用 `(request_id,root_id,created_xid,tenant_id)`，AttemptOrigin 用 `(attempt_id,root_id,created_xid,tenant_id,request_id)`。A? 不得只提供含 nullable attempt 的六列键并把 tenant/request 验证一起跳过。

root 提供 `(id,created_xid)`、R4 `(id,created_xid,tenant_id,request_id)`、A5 `(id,created_xid,tenant_id,request_id,attempt_id)` 与 node 所需 `(id,created_xid,attempt_id)`。所有 canonical 先按 `(root_id,created_xid)` 绑定真实根；R 行另按完整 R4 绑定；有非 NULL attempt 时再按完整 A5 绑定。MATCH SIMPLE 对 nullable attempt 的 SQL NULL 不表达 NULL-safe 相等，真实 source.attempt 与 root.attempt 是否同时为 NULL仍由 validator 完整核验，不假称该孔已由 FK 关闭。

RootStep 的 PK 为 `(root_id,step_no)`，另有 `UNIQUE(kind,source_id)` 与 `UNIQUE(root_id,step_no,kind,source_id,created_xid)`。18 个 d_kind 分别以 `(d_kind,root_id,step_no,kind,created_xid)` 指向该 kind 精确 canonical 的公共五列键；反向 canonical 用 `(root_id,step_no,step_kind,pk,created_xid)` 指回 step。generated 非选中列为 NULL，MATCH SIMPLE 只跳过未选中分支；kind 的封闭 CHECK 保证恰有一个选中分支。所有 canonical PK/kind/step/root/xid均被双向绑定，不用 OR EXISTS 替代。

root 的静态 CHECK 封闭 11 种入口及各 scope/attempt/semantic_result 联合，account_target 在 request 分支必须 NULL、account_command 分支必须非 NULL；configure not_found 的 target 不 FK 到账户。request_birth、account_create/configure、dispatch、propose_cost 为单步；attempt_birth 的 unmanaged/managed 分别一/两步；approve_cost 四步、unmanaged_correction 与 recovery 三步；record_finalization 至少三步；reconcile proposed/released 一步、approved 一或四步。准确 kind、顺序、条件及输出集合仍交 validator，计数不证明模板正确。新 UUID 身份为非零规范小写 UUID text，原 request/attempt/业务 ID 不改写或追加 canonical UUID 限制。

## 3. 出生与三条局部链

| 对象 | 精确键、关系与静态边界 |
| --- | --- |
| RequestOrigin | request_id PK；root/step 公共键；原 request 的 `(tenant_id,id)` 与四列出生 pointer 双向绑定。初态 latest=NULL、accepted/reserved；零售价/硬界非负、route revision非负、tenant pricing revision>0，所有诊断/期限时间 finite。client_ip 只保存 NEW.client_ip::pg_catalog.text，不尝试跨 INET/TEXT FK。完整冻结快照逐值与原件比较交 validator |
| RequestHead | request_id PK，origin↔head 双向 FK；ordinal=0 iff last=NULL，否则 ordinal>0 且 last非NULL；(request_id,ordinal,last)→decision精确位置。head.last 与原 request.pp_last_decision_id 另以两侧 `(request_id,last)`双向 FK绑定，NULL 起点不虚构decision |
| AttemptOrigin | attempt_id PK；root/step公共键；(tenant,request,attempt)绑定真实原 attempt；原出生 pointer 与 quote_selected_at五列双向绑定。初态 prepared/unknown/NULL；sequence 1..2；管理模式必须 managed 或 unmanaged，managed 的 account/binding/transition/event/journal/admitted 六项全部非NULL，unmanaged全部NULL。quote允许原 partial rate，非NULL实际费率非负；真实 quote/池/价格选择及管理输出由对应根/账户域另核 |
| AttemptHead | attempt_id PK，origin↔head双向 FK；ordinal/last同零起点联合；(attempt,ordinal,last_node)→node；member_count/tail仅允许全部NULL或非负count及0/NULL、正数/非NULL联合，正数时精确指member位置。managed/unmanaged与member组关联由validator核 |
| AttemptNode | id PK，UNIQUE(attempt,ordinal)、(attempt,ordinal,id)、(root,step)及(attempt,id)；精确 FK step五元组和root的attempt；ordinal>0，1时previous=NULL，其余previous非NULL；(attempt,previous_ordinal,previous_node_id)→同attempt准确前一位置。禁止 request_birth、attempt_birth、account_command、reconciliation 节点 |
| RequestDecision | id公共键，UNIQUE(request,ordinal)、(request,ordinal,id)及同request前驱latest键；ordinal1必须previous/before_latest均NULL，其后previous非NULL，精确引用同request ordinal-1。normal要求finalization非NULL/conflictNULL、after_latest=finalization；conflict相反，after_latest与before_latest NULL-safe相等且after_status=before_status。当前request.status/billing_status不永久绑定旧decision.after |
| EvidenceMember | id PK；UNIQUE(attempt,member_ordinal)、(attempt,member_ordinal,id)、(attempt,digest)、(attempt,digest,event)、(id,attempt)。member ordinal1/previousNULL、其余精确同attempt前驱；原identity自然三元与 C 组双向绑定；source_id=evidence_choice_id；本次choice精确绑定attempt/digest/原identity_event。new_member与existing_member的反向来源/实际before和tail规则由账户域与validator核 |

所有 head 都 FK 到受证 origin；所有节点和成员也直接 FK 到 AttemptOrigin，不能用原 attempt 存在替代前缀出生。head 指准确位置不表示它一定是最大位置，连续整链、唯一 node 适用集、末项与原投影、来源真实执行顺序及 DB xid stamp 仍需要不可绕过的 guard/validator。

## 4. incoming、meter 与冲突原件

IncomingSource 完整绑定 root、request 与可选 attempt；Usage 五计数非负、cached≤input、reasoning≤output，confirmed_final 的 input+output=total以 NUMERIC 加法防 BIGINT 溢出；协议/transport/raw stage/usage status均封闭。source/source_event/finish 的界按 octet_length 原字节数，digest为64位小写hex。ObservedAt非NULL时必须finite且effective逐值相等；未给时effective仅要求finite，由真实根DB取时认证。reconcile_source_id仅通过同root/tenant/request的真实ReconciliationSource绑定；其approve资格与派生输入逐值比较交成本域/validator，不由source文本授予权限。

MeterChoice 对 IncomingSource 同root/R身份及非NULL A身份各设精确FK；disposition封闭created/reused、collision封闭none/source_payload_mismatch/other_confirmed_usage，且只有other分支competitor非NULL。meter_origin_id始终等meter_event_id，引用MeterOrigin自然主键；旧meter可以跨request/attempt复用，故不错误加“所选meter归incoming request”的FK。两个attempt cut若有值必须属于incoming attempt，attempt为空时两cut均空。

MeterOrigin的PK是原meter_event_id，以C组及 incoming/source/meter三元精确绑定该created选择；原meter的id+G双向关联该origin。ConflictSource以原conflict_id为PK，同step只作companion，source_id=meter_choice_id；同root/R/A绑定incoming与choice，原conflict的id+G双向绑定companion，且原conflict.event_id按其G精确绑定该choice.meter_event_id。meter/conflict的原JSON payload、created/reused判定与meter source-key冲突的真实等待顺序不通过超宽JSONB索引伪装为已验。

normal RequestDecision 与原 finalization 的 id/G/tenant/request 双向绑定；before/after latest若非NULL，必须为同tenant/request的真实finalization。decision↔incoming/meter choice同root/R精确关联；conflict_source精确指本次meter choice companion，不把旧meter归属移给incoming。

## 5. 原件目标 UNIQUE 接口

以下原表 UNIQUE 由 original 材料提供，本 SQL 只引用：

```text
upstream_attempts(tenant_id,request_id,id)
request_records(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid)
request_records(id,pp_last_decision_id)
upstream_attempts(id,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at)
metering_events(id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
metering_event_conflicts(id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
finalization_records(id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid,tenant_id,request_id)
finalization_records(id,tenant_id,request_id)
procurement_evidence_identities(attempt_id,evidence_digest,event_id,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_created_xid)
```

原 `(tenant_id,id)`、原 meter/conflict PK 和原 identity `(attempt_id,evidence_digest)` 继续复用。G/出生组的全NULL legacy兼容、完整新行、固定kind/role及APP/writer权限由original CHECK与真实guard负责；NULL兼容不意味着允许APP给旧行补来源。旧原件source存在与否由完整枚举确认，不能按“当前查不到来源”降级为合法legacy。

## 6. 必须由真实 validator/guard 承担的义务

1. 真实实际session_user、source-before-effects、完整DB锁/等待后再读、实际xid覆盖、head从准确OLD单步推进与不可变性；本文件无函数或trigger，不认证这些执行行为。
2. root.step_no连续1..step_count、每入口精确模板和分支顺序、root/source可选attempt的NULL-safe相等、所有有attempt canonical step除birth恰一个node及其余根禁止node；SQL的FK不证明完整集合。
3. request/attempt完整冻结快照、client_ip规范化、quote第一次选价时刻与当时实际目录/managed等待后复核、PB出生/许可，以及账户域真实managed产物。新表timestamp/XID本身不证明历史顺序。
4. 三条链的完整前缀、head最大末项、request.latest与最后decision（含NULL）一致、精确sticky重放；合法dispatch/客户finish后的current status不能使旧decision失效。
5. 新建与复用meter、完整other-confirmed前缀及absence、冲突优先序、原payload与输入身份字节、canonical输入/最终归一值、exact角色/双审；不得用count/hash或单witness代替完整枚举。
6. member实际new/existing、原identity反向无漏、精确before/after member与account cut；conditional future输出不能通过NULL或同额来源冒用。后续完整只读validator不得stub成功，也不能在APP COMMIT补DML修复缺件。

领域交付先静态核每个FK目标的完整非延迟UNIQUE、列/NULL/生成表达式和约束名；整组PostgreSQL实际结构与声明边验收由集成人另记，不作为经营验收证据。

集成后真实PG/race的整组结构与命名声明边结果另见[关系材料验证](../../docs/verification/procurement-relation-materials.md)。结构通过不等于本页列出的业务guard/writer/actual APP验收。

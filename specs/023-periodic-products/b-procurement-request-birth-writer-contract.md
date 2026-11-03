# B-P4-A1：真实 Reserve 的 request 自动出生与保护合同

2026-11-04；设计核对基线 `1d45726`，验收续片基线 `71de18a`。承接[最小 exact-link 集成](b-procurement-birth-exact-link-integration-contract.md)、[出生根实施切片 A0–A6](b-procurement-attempt-birth-writer-contract.md#8-实施切片和实际尚缺正文)、[共同来源](b-procurement-source-contract.md)、[request 来源](b-procurement-cost-request-contract.md#31-requestorigin--requesthead)、[布局](b-procurement-schema-contract.md)与[原 Reserve 权限接缝](b-procurement-full-acl-contract.md#41-request自动出生保留原reserve)。**隔离候选及扩充矩阵整包23顶层/423子项真实PG race通过，完整矩阵仍待补齐。** [实际记录](../../docs/verification/procurement-request-birth.md)区分预算/资金、钱包等待、raw误授和有效撤权与尚缺分支；不能据此关闭本页全部退出条件。不修改原 001–022、既有账户 writer v1/摘要、715/482 列、旧 profile、正式迁移或在线入口。

## 1. 目标、实际入口与信任边界

A1 使真正 APP LOGIN 的原 [billing.Store.Reserve](../../internal/billing/reserve.go) 在其真实 `request_records INSERT` 后自动产生唯一 RequestOrigin、初始 RequestHead、RootStep 和最后 seal 的 request_birth root。Go 原方法、输入、Reservation 返回值及资金/PB 事务顺序不变；不把 owner 手工插入 source 的 fixture 当成该入口的实现。

APP 保留原 21 列 INSERT，因此数据库无法区分“Go Reserve 发出的合法 SQL”与同一 actual APP 连接手写的完全等价 SQL。符合本页、原 018/020 及其余约束的 raw INSERT 也自动真实出生；**不能宣称 raw APP INSERT 全部拒绝，或 request_birth 证明完整 Reserve 已受控执行。** 不以 query text、GUC、trigger_depth、应用自报 token 或同 xid 猜测调用来源。若后续要求 APP 连完全等价 INSERT 也不能执行，须另冻完整 Reserve definer 根并撤该写面，不属于 A1。

本片认证真实初态、不可变快照与精确来源关系，不认证原客户资金/零售选价的完整来源。原 020 的 deferred `pb_check_customer` 确实会因新 request 缺 reservation 返回 `23514`，即使没有配置预算；但 APP 保留原资金写面，不能据此宣称任意资金伪造已被阻止。正常 Reserve 的硬预占、资金、Key held、PB、审计/outbox 均继续在原事务完成。

## 2. 原代码事实与时间合同

真实原顺序为：幂等键 INSERT/冲突读取 → tenant/membership/user/Key/model 锁与权限重读 → 路由/profile/硬界检查 → 价格组 SHARE → `pricing.SelectEffectiveTx` 以独立 DB clock 选价 → request deadline/金额/Key累计限额 → customer PB 控制锁 → wallet 账户锁 → request/reservation 两个 INSERT → PB admission → reserve journal/lines → PB reserve event → admission 后 Key/request 期限检查 → Key held、recovery job、billing.reserve audit 和 reserved outbox → APP COMMIT。原 [transactionOnce](../../internal/billing/store.go) 设置 RC、lock 3s/statement 5s，已知 `40P01/40001` 且 ctx 有效时最多三次；A1 不续时、不扩大重试，不吞掉原业务/执行错误。

同类触发器按字典序执行，不按迁移文件编号：request 的原 BEFORE INSERT 是 **`pb_request_stamp` → `request_retail_snapshot`**。原 020 在 request 上锁 tenant/key、同步未配置 scope，并填真实 `pb_created_xid/pb_tenant_control/pb_key_control/pb_admitted_at`；原 018 核实际 tenant pricing domain/revision 与 immutable price 的 model/group/rates、`effective_at <= retail_priced_at`。A1 早期 guard 在二者之前拒异常输入，AFTER hook 读取两者都处理完成的实际 NEW。

- `created_at` 保留原 `clock_timestamp()` DEFAULT，采样可能早于 BEFORE 内的真实等待；不能改成 AFTER 时钟，不能要求它等于或晚于 PB admission。
- `retail_priced_at` 是原选价语句较早返回并由 Go 传入的时刻。等待 wallet/PB 后不重新选价；A1 不将其认证成在 hook 内独立采样的 DB 时钟，也不增加“INSERT 时必须仍为最新价”的检查。
- `pb_admitted_at` 沿原 020 `pb_now()`，捕获真实 NEW，不预采、不覆盖；不要求与零售时刻/创建时刻相等，不用时钟或 xid 排全序。
- `client_ip` 从真实 `NEW.client_ip::pg_catalog.text` 保存，保留 NULL；这是 PG16 INET 文本投影，不是 HTTP 原文字节。比较同 instant 的 timestamptz，不以会话格式字符串或时区重编码猜原值。
- 原 request/tenant/user/Key/model 等 TEXT 身份保持原字节，不 cast/规范化为 UUID；新 root 才是 DB 非零 canonical UUID。Go 原 uuid.Parse 接受集由 A0 独立校准，A1 不新增公共身份参数或缩窄原合法输入。

## 3. 新候选、固定函数与触发器 inventory

A1 是显式隔离的 `birth-exact-link-a1-v1` 新候选，仅装配 `pp_operation_roots/pp_root_steps/pp_request_origins/pp_request_heads` 四表与 request 四个 pp 列；不先安装完整 685 约束、账户 v1 或任何 A2 表。`procurementrequestbirth` 的 writer/guard/ACL 材料与最小 DDL/exact-link 材料分别冻结；包/材料独立于 `procurementaccountwriter` 的冻结 v1；不在 v1 原文件中改正文或 SHA，不由 main/migrate/provision 自动安装。安装输入只含 safely quoted schema、实际 APP/W 名称及绑定 OID，复用既有安全 delimiter 规则；函数固定 trusted schema,pg_catalog,pg_temp search_path，关系/普通函数调用显式限定 schema，无动态 SQL/GUC phase。

固定新增七个函数，均非 STRICT、VOLATILE、PARALLEL UNSAFE、W owned。仅 capture 为 definer，其余保持 invoker：

| 函数签名 | security / 责任 |
| --- | --- |
| `pp_rb_request_write_v1() RETURNS trigger` | INVOKER；request 早期 INSERT/UPDATE/DELETE/TRUNCATE guard，含唯一出生 pointer UPDATE 分支 |
| `pp_rb_capture_request_v1() RETURNS trigger` | DEFINER；只作 request AFTER INSERT 自动捕获，唯一 seal 持有者；不是 APP 普通根 |
| `pp_rb_source_write_v1() RETURNS trigger` | INVOKER；RequestOrigin/RequestHead 的精确出生与不可变保护 |
| `pp_rb_shared_write_v1() RETURNS trigger` | INVOKER；本独立候选的 root/step 仅接受 request_birth 模板，含 TRUNCATE 拒绝；未装配账户域明确拒绝 |
| `pp_rb_validate_request_v1(text) RETURNS void` | INVOKER；纯只读 A1 request/birth/head 双向认证 |
| `pp_rb_validate_root_v1(text) RETURNS void` | INVOKER；仅认证 request_birth 模板；未装配账户域明确拒绝，不调用不存在的账户函数 |
| `pp_rb_deferred_v1() RETURNS trigger` | INVOKER；从实际 NEW/OLD 自然键调用只读认证，APP 顶层 COMMIT 运行 |

它们是局部叶子，不冒充完整五个 `pp_validate_*` 全域 validator。缺域返回 `PP906 procurement_prefix_untracked_domain`；损坏的 claimed tracked 关系返回 `PP902 procurement_history_invariant`，不能先按改坏的 kind 降为未知域。身份拒绝 `PP901 procurement_execution_forbidden`，协议/隔离/timeout 配置错误 `PP903 procurement_execution_protocol`；保留真实 `42501/235xx/55P03/57014`，不转换为成功。

### 3.1 独立 A1 与未来账户组合的显式依赖

当前独立 A1 **不安装账户 v1**，故直接新增本页 root/step 四个 BEFORE 与两个 deferred trigger；不存在先安装再移除旧 trigger 的步骤。新候选白名单须明确没有旧 `aa_pp_ac_*` / `zz_pp_ac_*`，没有账户 source/guard/helper。未来若与账户候选组合，必须另冻组合候选及完整 catalog/ACL，下列六 trigger 才成为须原子替换的显式前置；本片不实施该组合。

旧 `pp_ac_guard_v1` 的 root/step 分支要求账户 source；旧 deferred root validator 也只认账户根。直接叠加 request hook 必失败或诱使实现绕过检查。未来组合候选须在同一 owner 安装事务核精确旧 inventory 后替换以下六项，替换失败整体回滚：

| 移除旧 trigger | 新 trigger / 事件 | 函数 |
| --- | --- | --- |
| `aa_pp_ac_root_write` | `aa_pp_rb_root_write`：root BEFORE INSERT/UPDATE/DELETE ROW | shared_write |
| `aa_pp_ac_step_write` | `aa_pp_rb_step_write`：step BEFORE INSERT/UPDATE/DELETE ROW | shared_write |
| `aa_pp_ac_root_truncate` | `aa_pp_rb_root_truncate`：root BEFORE TRUNCATE STATEMENT | shared_write |
| `aa_pp_ac_step_truncate` | `aa_pp_rb_step_truncate`：step BEFORE TRUNCATE STATEMENT | shared_write |
| `zz_pp_ac_root_complete` | `zz_pp_rb_root_complete`：root AFTER INSERT、DEFERRABLE INITIALLY DEFERRED ROW | deferred |
| `zz_pp_ac_step_complete` | `zz_pp_rb_step_complete`：step AFTER INSERT、DEFERRABLE INITIALLY DEFERRED ROW | deferred |

上表函数缩写均为完整 `pp_rb_*_v1`。组合候选中其他账户 v1 的 source/origin/journal/head/account/command/audit/user guard 和 deferred trigger、普通函数、公开 create/configure 根须保持原完整语义。未来 shared_write 的账户 root/step 分支必须完整保留原 v1 的字段、source、顺序与反向要求；**不能把 RETURNS trigger 的旧 guard 当普通 helper 调用**。未来组合的只读普通账户 validator 可以实际委托；当前独立 A1 不委托未安装函数。账户先后混合与既有故障矩阵是未来组合候选的回归门禁，不能因 request 域新开便放松命令 root。当前独立 A1 不调用或成功 stub 这些未装配域。

### 3.2 A1 新增 trigger 与精确顺序

| 关系 / trigger | 事件及责任 |
| --- | --- |
| request / `aa_pp_rb_request_write` | BEFORE INSERT/UPDATE/DELETE ROW；早于 immutable/pb/retail |
| request / `aa_pp_rb_request_truncate` | BEFORE TRUNCATE STATEMENT；拒绝 |
| request / `zz_pp_rb_request_birth` | AFTER INSERT ROW；capture、立即闭合本 birth root |
| request / `zz_pp_rb_request_complete` | AFTER INSERT/UPDATE、DEFERRABLE INITIALLY DEFERRED ROW；重新读当前 request 而非固定早期 NEW |
| RequestOrigin / `aa_pp_rb_origin_write`、`aa_pp_rb_origin_truncate` | BEFORE I/U/D ROW / TRUNCATE STATEMENT；只允许受控完整 INSERT |
| RequestHead / `aa_pp_rb_head_write`、`aa_pp_rb_head_truncate` | BEFORE I/U/D ROW / TRUNCATE STATEMENT；A1 只允许准确初始 INSERT |
| RequestOrigin / `zz_pp_rb_origin_complete` | AFTER INSERT、DEFERRABLE INITIALLY DEFERRED ROW |
| RequestHead / `zz_pp_rb_head_complete` | AFTER INSERT/UPDATE、DEFERRABLE INITIALLY DEFERRED ROW |

request INSERT 的实际 BEFORE 序为 `aa_pp_rb_request_write → pb_request_stamp → request_retail_snapshot`。UPDATE 的序为新 early guard → 原 `immutable_request_client_ip`（仅 UPDATE OF client_ip）→ `pb_request_stamp → request_retail_snapshot`。AFTER birth 立即执行，但 queued `pb_request_source_check` 及 `zz_pp_rb_request_complete` 的约束检查仍在 APP COMMIT；不把它们改为 definer、不 flush SET CONSTRAINTS。约束被调用者提前设为 IMMEDIATE 导致原 Reserve 暂态被拒时保留原错误，不禁用约束让它通过。

## 4. 早期 guard 与合法 UPDATE

### 4.1 INSERT

只接受绑定 actual APP：`session_user::regrole::oid = APP_OID` 且 invoker `current_user::regrole::oid = APP_OID`，RC。NEW 初态必须 `status=accepted,billing_status=reserved,latest_finalization_id IS NULL`，四个 pp 字段全 NULL，四个 pb 字段也全 NULL，随后由原 020 stamp 真正生成。APP 不获这些派生列的 INSERT 权限；guard 仍重核，不能只信 DEFAULT/列 ACL。

拒 owner+SET ROLE APP、peer/worker/PUBLIC、W raw INSERT、异常状态、非 NULL/部分来源组及重用已有 request/origin/head；合法完全等价 raw APP INSERT 则运行同一自动 hook。保留原约束/018 的检查与错误，不额外按当前 profile、当前价格组或最新价重构历史接受集。

### 4.2 tracked UPDATE 与唯一 pointer 安装

冻结组包括 `id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,created_at` 及四个 pb 字段。tracked 行对任意角色一旦这些值变化即拒，先改后改回也不允许；`pp_origin_*` 安装后终身不可替换/清空，`pp_last_decision_id/latest_finalization_id` 不因普通资金更新被抹掉。

唯一 W pointer UPDATE 同时要求：actual APP、W current、原行三列 pp_origin 全 NULL 且 last/latest NULL、初态 accepted/reserved；精确该 request 的唯一完整 Origin 与 `(ordinal=0,last=NULL,updated_xid=birth_xid)` Head 已存在，Origin 逐值等原行真实冻结组、其 birth xid 为当前 top xid；NEW 只把三列设置为该 Origin 的 `(root_id,1,created_xid)`，其他列全等 OLD；该准确 root 尚不存在、尚无该 root 的 step，其他出生关系不得存在。条件联合才允许，不能因 xid 相同或“有任意 source”准许回填。该 UPDATE 不取得/重写 PB admission、零售价、status/latest。

仅有自动 capture 可产生上述暂态：W NOLOGIN、无 membership/SET 路径，APP 无 source DML/私有 EXECUTE/CREATE/TRIGGER；AFTER capture 只作用实际 TG_RELID 与 NEW，不能普通调用。旧 request 的 UPDATE 不触发 capture，不能通过新事务或同 xid 手插 source 使其补生。

APP 保留 `UPDATE(status,billing_status)` 的普通资金窄路径，冻结组、全部来源和 latest 必须不变。finish 的真实 reservation 已更新、settlement 已存在时，只允许按同 tenant/request 的真实 status 更新 billing；released 且 OLD.status 为 accepted/dispatching 才转 failed，否则 status 保持。pending 的原顺序是 reservation pending → request billing pending → case INSERT，故 BEFORE 只能核真实 reservation pending 与 status 不变；**不能要求此时 case 已存在**，case 的精确存在性在 deferred 当前态检查。普通客户资金的完整来源仍不在 A1。

APP 不保留任意 dispatching/succeeded/cancelled/latest 更新；未来 dispatch/RequestDecision 须新受控根与精确来源后另扩分支。A1 不宣称未接这些新根时原 RecordFinalization/PrepareDispatch 仍能在候选下全流程运行。A2 撤 APP upstream INSERT 等写面另合同；本片不借 A1 把旧 attempt 写面改签为已受控。

legacy 只允许完整无 pp 出生组/last 且无任何 Origin/Head 的原历史，保留其原普通更新规则，不加新 RR tracked 限制，不补历史。任何部分标记或坏 source/head 不能降为 legacy；legacy 交给 A1 认证返回 PP906。tracked DELETE/TRUNCATE 一律拒，普通 profile 不安装 A1 时原行为完全不变。

## 5. 自动 capture 的实际暂态、来源与 seal

capture 必须核 `TG_WHEN=AFTER,TG_OP=INSERT,TG_LEVEL=ROW,TG_RELID=受信 request 表`、actual APP/W OID、RC、原 timeouts，读取真实已插入同 id 行并与触发 NEW 的冻结/初态/PB 全字段等值。`pb_created_xid=pg_current_xact_id()`，所有时间 finite，原 pp 组仍全 NULL；无旧 origin/head/step。NEW 不能来自一个可被 APP 普通调用的参数。

局部预分配非零 root UUID；来源自然 ID 是原 request_id。固定顺序：

1. 一次完整 INSERT `pp_request_origins`：33 个物理列中的 generated step_kind 不写，其余 32 列一次写全。`root_id=局部 root,step_no=1,created_xid=实际 top xid`；其余冻结/PB/时间/初态均来自真实 NEW。禁止 pending source、后补 UPDATE 或 JSON caller witness。
2. INSERT `pp_request_heads(request_id,ordinal,last_decision_id,updated_xid)=(NEW.id,0,NULL,top_xid)`。
3. 执行 §4.2 的精确三列 pointer UPDATE，原 018/020 UPDATE trigger 仍真实运行。源对原件、head/root/step 的暂缺边必须是真实 deferred，不能删除约束。
4. INSERT 唯一 step `(root,1,'request_birth',NEW.id,top_xid)`。
5. 最后一次完整 INSERT root：`entry_kind=request_birth,scope_kind=request,tenant_id=NEW.tenant_id,request_id=NEW.id,attempt_id=NULL,account_target_id=NULL,created_xid=top_xid,session_role_oid=actual APP OID,step_count=1,semantic_result=born`。
6. 立即只读认证本 request/root，RETURN NULL。seal 后 capture 不再追加输出或第二 root；整个原 Reserve 仍可继续写 reservation、资金/PB/audit/outbox，这些是原 Reserve 的独立输出，**不归 request_birth root**。

源先于 pointer/step/root，原 request 先于源；不得改变成 BEFORE 伪造未来 DEFAULT。源/原件/step/root 的暂缺关联只在该封闭同步 hook 中存在；任一异常整个 request statement 失败，后续原 Reserve 也整体回滚，不能回退成 untracked。没有额外 autocommit、锁释放或外层第二次 seal。

AFTER 内部 UPDATE 的列不会自动出现在外层 INSERT 的 RETURNING NEW 中；原 Reserve 没有读取 request RETURNING，故不改原返回。后续观察须新 SQL 读实际行，不能用外层 NEW 证明 pointer 未填。

## 6. readonly 精确认证与关系义务

`pp_rb_validate_request_v1` 用当前行分别双向核 33 列 Origin、33 列 request 的冻结映射、三列出生 pointer、唯一 Head 与真实 root/step；NULL 一律 IS NOT DISTINCT FROM，client_ip 用原 PG text 投影。初始 status/billing/latest 属 Origin 历史值，不能永久强制 request 当前 status/billing 等于 accepted/reserved。A1 无 RequestDecision writer，Head 必须 ordinal0/last NULL、request.pp_last/latest NULL；若后来出现 claimed decision/非零 head，先核必要身份，坏证 PP902、未实施合法域 PP906，后续版本不得拿今天状态倒填出生。

root validator 要求准确单 step/source/head companion 与 request 身份；只从本候选实际装配的 root、step、request origin、request head、request_records 反向枚举同 root/自然 ID，逐行精确比较，不能只查 source COUNT。独立 catalog 固定五个实际关系及其准确列/guard/函数/ACL；缺关系、额外候选表/旧账户 hook 或无法满足的域返回错误，不能查询未装配表再吞错，也不能成功 stub。A1 不提供账户/attempt/cost/decision/audit/outbox 的 pp 归属列；原 Reserve 普通 billing.reserve audit/outbox 沿原表和全 NULL 前缀状态运行，既不是 birth 输出，也不需要 A1 扫描不存在的扩展列。未来组合先扩准确装配清单和全域反向集合再准入，不能静默把额外域视为已认证。

最小 native 边与 exact-link 完整映射、双端 SHARE/等待后下一独立 RC statement 重读，准确按 [集成合同 §2–4/附录 A](b-procurement-birth-exact-link-integration-contract.md#2-保留-native-定位不得把额外-payload-放回索引)。不能对 legacy 的 original.id 无条件 FK 到 source；用 nullable 出生 step locator 与真实 original.id 精确反查。PK/UK/CHECK 不 deferred；source/step/root/head 暂缺边必须真 deferred。完整 NULL 与反向遗漏不能依赖 MATCH SIMPLE 自动跳过。readonly validator 无锁、无 DML/补写/authority；需要双端行锁的 BEFORE guard独立执行，不把锁藏进只读认证。不在 birth hook 立即调用需要尚未生成 reservation/PB 的完整 `pb_check_customer`；原 020 deferred 保留真实资金/PB检查。

本独立 A1 不安装原 685 关系材料后再删除宽 UK；最小 DDL 原生只装 native 窄键及逐字段 exact-link。原完整材料、715/482 布局与旧 profile 均原样保留。A1 对其实际 request 出生关系须完成真实长 TEXT 接受集及双端精确绑定，不能等 A4；全域宽索引替换、账户/attempt/成本关系及全域 catalog 仍为后续门禁。

## 7. 最小权限与可执行安装接缝

先在原 001–022 的空随机环境建立 APP/PROJECTOR/W，按原 `operations.ProvisionFor` 完成原 APP/PROJECTOR 经营权限，然后按固定顺序安装最小 A1 四表/四扩展列、native 定位与 exact-link、真实 writer/guard/deferred 与准确 ACL。不安装账户 v1、未用 identity/685 材料或 A2 command+reserve；若 fixture 使用已装 B-P1，保留其原约束并独立核验，不能把它作为 A1 隐式依赖。**不能在新增来源表后盲跑原 ALL TABLES provision**，否则会让 APP 获得前缀表 DML；不能通过往现有账户 fixture 追加一个全 schema GRANT 修复 Reserve。新候选安装前后核准确 inventory/owner/ACL，事务失败整体回退，所有源码材料分别固定摘要。

APP 的 request INSERT 固定且仅：

```text
id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,
input_upper_bound,output_limit,profile_version,policy_version,request_hmac,
request_deadline,instance_id,streaming,route_revision,client_ip,
pricing_group_id,tenant_pricing_revision,retail_priced_at
```

APP 不获 INSERT(status/billing/latest/created_at/pb_*/pp_*)，不获 source/head/root/step INSERT/UPDATE/DELETE/TRUNCATE/TRIGGER 或 capture/shared/source/request 私有函数 EXECUTE。request 普通 UPDATE 仅 status/billing；撤原 table-level UPDATE 后按列授予，不留表级或继承旁路。其他原 Reserve 读取/资金/PB DML 依原运行合同保留，A1 不减少必要 pre-reserve 锁、原 ledger/Key/PB/audit/outbox/恢复队列操作；其完整经营权限不是 request_birth 新发行的权限。

W 在 request 上仅新增 UPDATE 三个 `pp_origin_*` 与只读 33 普通列，**没有 request INSERT、status/billing/latest/其他冻结列 UPDATE**。新增 RequestOrigin INSERT 32 个非 generated 列、Head INSERT 四列；root INSERT 11 列/step INSERT 五列由本独立候选显式精确授予，不继承旧账户 grant，不扩表级权；新表不授业务 UPDATE/DELETE/TRUNCATE；只为精确双端父 SHARE 给 W 三个锁列 UPDATE：step(root_id)、Origin(request_id)、Head(request_id)，四个新表 BEFORE guard 对实际 UPDATE（包括赋原值）无条件拒绝；root(id) 没有实际 SHARE 路径，不授其 UPDATE。APP 不写这些子表、deferred 纯读不锁，故不需要新表锁列 UPDATE。APP/W 对 Origin 33列、Head 4列及 request 33列有准确只读面；root/step 与本四表反向读闭包在本候选逐列显式冻结，不假定原账户闭包存在。APP 仅获两只读普通 validator EXECUTE 及其真实传递普通函数 EXECUTE，自动 trigger 的直接 EXECUTE 不作为授予 APP 私有能力的理由。

W pointer UPDATE 的原 020 分支显式调用 `pb_require_read_committed()`，必须有准确 EXECUTE；原 trigger 条件可能读取 `periodic_budget_allocations(subject_type,subject_id)`，按实际 SQL 授准确只读面，不用 table SELECT。A1 W 不负责 PB admission，不新增 PB window/event/control 写权、预算配置权或 SELECT(xmin)。APP 原 `readCustomer`/reservation guard 真实读取 request.xmin，若收口 request 表级 SELECT 为固定普通列，必须单独授 APP `SELECT(xmin)`；不能靠列 SELECT * 推断系统列权。其他未变 PB 表维持原 profile，A1 不冒称全 V4 sidecar 已装配。

实际撤权续验校准：pointer UPDATE不改 `(id,tenant_id,key_id)`，原020的 `AND EXISTS` 短路，故W两项allocation SELECT在当前出生路径并非必要。冻结v1 SQL/ACL摘要保留，用两项有效失权仍真实成功的正控制记录，而不伪计42501；291项实际必要失权与PUBLIC重叠边界见[授权合同](b-procurement-request-birth-grant-fault-contract.md)。这不扩大为所有原经营父锁权限已验。

锁能力不可漏：原 APP Reserve 的 tenants/memberships/users/models SHARE、Key UPDATE、价格组 SHARE、PB control 与 wallet UPDATE 都仍需原 SELECT + 至少一列 UPDATE；APP 已有真实管理/资金 UPDATE 列时复用，不为 A1 追加表级写。A1 W 的 pointer UPDATE 已有三列 UPDATE，因此若局部 source/原件检查需要 request SHARE 可复用；本页 readonly validator 自身不加锁。capture 不重锁 tenant/Key/钱包，不为 W 新增这些父表 UPDATE 权。本候选不授共享账户 W 的 users 锁权或其他未用域权限；A2/未来组合扩父锁时另按完整 ACL 给单列及 invoker 拒写。

## 8. 实施顺序、实际验收与攻击矩阵

可实施的顺序为：本合同冻结 → 独立四表最小 A1 材料/叶子 validator/双端 exact-link、静态 inventory/最小 ACL 测试 → 专用随机 schema 中 actual APP 原 `billing.New(appPool).Reserve` 成功 COMMIT 与失败回滚 → A0 规则叶子及 A2/A3 在该真实父出生上接通 → 全域 exact-link/最终 catalog/ACL 的 A4 → prospective executor/全根实际矩阵 A5 → 架构、整仓与 R2。A1 不依赖 A0 的路由五字段 JSON/ignored BYTEA 算法才可运行；A0 可并行，但后续 A2 完整根必须等 A0+A1。A1 需自己的真实请求精确绑定，不能等未来 A4 再补基础不可变/反向检查；全域 exact-link 也不能因 A1 局部通过被省略。

专库仅 PG16、`127.0.0.1:55439/moni_test`、`moni_test` 安装 LOGIN，actual APP 非 owner、W NOLOGIN；root 串行 PG。owner 仅准备父管理数据、原合法资金 baseline、安装和独立观察/撤还权限；被测 request/source/root 必须由真实 APP Reserve/自动 hook 生成。每个失败比较 request/reservation/幂等/Key held/账本journals+lines+accounts/PB controls+windows+allocations+events/B-P1来源/recovery/audit/outbox 与新增 request origin/head/root/step 的完整行快照，并保留旧已提交根，不能只查本次 root 数量。

| 组 | 必须实际覆盖 |
| --- | --- |
| 原 Reserve | no PB、tenant/key enabled/disabled PB、零金额/输入单计量、价格组/默认域、两类 stream、nil/原合法 UUID 文字与 IP NULL/IPv4/IPv6；以原 Reservation/手算金额和真实资金/PB事实独立核新来源，不用 writer 生成期待值 |
| 时间/等待 | 真实 wallet/PB 等待后跨计划售价生效仍保留较早报价；PB admission 与 request DEFAULT 真时间，created_at 可早于等待结束；Key/request 过期按原时点全回滚；不加“INSERT最新价”收紧 |
| 等价 SQL/边界 | 完全等价 raw APP INSERT 自动真实出生；少 reservation COMMIT 原020的23514；异常初态/显式派生列/pp或PB部分联合拒；不把等价SQL允许写成受控完整Reserve |
| 不可变 | 冻结组逐列异值 UPDATE、改后改回、原值来源重装/清空、latest/last抹掉、任意成功/取消状态、tracked DELETE/TRUNCATE拒；legacy不补生，partial/坏head不得降legacy |
| source/root | 缺/多/错 source/head/step、提前seal/二次seal/缺root、seal后追加、同xid错root/错request/错tenant/错自然ID、跨root借head/step、账户source挪入birth、birth挪入账户、额外G输出、未知kind损坏证据拒；同事务两个合法request不串用；账户→request→账户留给未来组合候选 |
| 角色/权限 | actual APP成功、owner/SET ROLE/peer/worker/private EXECUTE/临时schema或trigger旁路拒；逐项撤W必要INSERT/三列UPDATE、APP21列INSERT/真实锁列和readonly闭包，确认有效true→false→true、原42501、完整数据/ACL恢复；owner隐含EXECUTE和自动trigger EXECUTE不得假称失权 |
| 暂态/失败 | Origin、Head、pointer UPDATE、step、root seal、Reserve后续reservation/PB/journal/Key/audit/outbox及APP deferred各真实边界注入异常；无pending来源、无半birth/半资金；提前SET CONSTRAINTS保留拒绝；55P03/精确取消57014、未知COMMIT不重放 |
| 兼容/门禁 | 原账户候选文件/摘要/测试不变，未来组合需回归create/configure/replay/203项提交撤权与138 grant矩阵；普通未安装profile的原API回归；候选原资金pending/finish顺序不被提前case检查误拒；长TEXT、NULL及exact-link全域未验项如实保留 |

仅实际通过的 A1 分支记为本隔离候选证据。实现、PG、架构语义复核及最终准确提交由集成人收口；本片不关闭完整 writer、新 profile、A2–A6、B-P3/R2、客户资金来源、供应商、正式支付、容器/1Panel 或性能验收。

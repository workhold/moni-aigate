# B-P4 D1：账户闭合 mutation 的工作提案

2026-11-03。承接 [B-P4 D0 设计稿](b-procurement-prefix-design.md)。**状态：D1 工作提案；账户逐字段 DDL、函数、typed 来源和 ACL 尚未冻结，不授权据本文实施业务写入。**

[写入权限与事务合同](b-procurement-writer-authority-contract.md)已冻结 A 路线、独立 NOLOGIN 最小 writer、真实检查与证据原子闭合等上位边界；[原身份 ABI 叶子合同](b-procurement-identity-contract.md)仅冻结独立字节转换。二者不表示账户、成本/request、完整读集及新 profile 已全部完成 D1。本文提出账户逻辑行、拟定函数与访问闭包，供根集成人和成本/request片汇合后逐项裁定。本文不记录新业务验收；旧 B-P3 缺证与拒绝基线保持。

## 1. A / B 裁决与真实保证

| 路线 | 能做到什么 | 完整实现的必要代价 |
| --- | --- | --- |
| A，选择 | DB 用同组锁后 before 执行真实 gate、原子更改原 source/投影并生成 journal；APP 不提供 before/after | 新 NOLOGIN writer、固定 definer 根、精确列 ACL、PB invoker 的明确访问闭包，以及新 catalog profile |
| B，未选择 | 在 reservation INSERT / upstream 真正 sending 点做 DB gate，自动记录每个低级变动，理论上可重放物理暂态 | 必须记录 reservation/account/config/event 每次物理变化；gate 不能信可能滞后的 account 金额；需新的规范财务 head 或每次全量求和；raw SQL 在已锁 reservation 后补账户锁会与正常顺序反转；旧 mutable source 的逐次捕获权威也要额外证明 |

B 并非逻辑上不可能，但不能以“加 AFTER trigger”实现。它需要区分一次操作里先改 p、再 pause、再 event、再改 a 的中间状态，且不得把暂态或最后余额当成更早 gate。A 更容易给出封闭权限与原子状态证明，不保留 B 的 raw DML 作为备用路径。

本片设计目标覆盖金额复用、配置改变、0金额、unknown、争议、多次绝对更正和真实派发；不采用 Σupper 限缩。账户 journal 仍不替代成本/request片的 evidence、incoming、正常成本资格和审批消费原件。

## 2. 字段类型与对象命名提案

以下 `pp_*` 是候选协议 v1 的具体拟名，须与另一片共同冻结后落成完整 DDL。新对象由 migration/schema owner 拥有；writer **不拥有表或 schema**。

- 新 ID：TEXT，接受与现 `pb_uuid` 一致的非零规范小写 UUID；历史 tenant/request/attempt 等引用继续是精确 TEXT，不擅自强转 UUID。
- ordinal / revision / root step / count：BIGINT，非负/正值按下表；达到 MaxInt64 后 fail closed，不回绕。
- 金额：NUMERIC(100,0) 且非负；delta 为 NUMERIC(100,0) 可正负。reserve upper/extra/limit 是规范非负 BIGINT；乘法/ceil 在 NUMERIC 中计算后核上界。
- 时间：TIMESTAMPTZ finite、数据库微秒精度；只展示/检查期限，不用于排序。
- birth/updated stamp：XID8，DB `pg_current_xact_id()` 覆写；只绑定出生事务，不按数值排序。

### 2.1 `pp_account_origins`

| 字段 | 类型 / NULL | 约束 |
| --- | --- | --- |
| account_id | TEXT PK NOT NULL | FK procurement_accounts.id，deferred |
| version | SMALLINT NOT NULL | =1，由 DB 固定 |
| create_operation_id | TEXT UNIQUE NOT NULL | 正式 account_create 命令 operation_id |
| root_id | TEXT NOT NULL | 本次 DB 产生的 create root，typed root/step 外键 |
| journal_id | TEXT UNIQUE NOT NULL | ordinal=1 的 create journal |
| created_xid | XID8 NOT NULL | 与实际账户出生/正式命令/journal 相同 top-xid |
| created_at | TIMESTAMPTZ NOT NULL | 实际账户 created_at，finite |

origin 的完整初始配置放在 ordinal1 的 journal.after；其 before 配置/桶/阻断全部 NULL，after held/spent/blocking=0、configuration_revision=1。head 初始即指向这条 create 条目，不使用含义不清的虚拟 seq0 entry。

### 2.2 `pp_account_heads`

`account_id TEXT PK/FK origin`；`ordinal BIGINT>0`；`journal_id TEXT NOT NULL`；`updated_xid XID8 NOT NULL`。

当前投影拟定为：`label TEXT, provider TEXT, state TEXT(active/paused), limit_amount BIGINT>=0, configuration_revision BIGINT>0, held_amount NUMERIC(100,0)>=0, spent_amount NUMERIC(100,0)>=0, blocking_count BIGINT>=0`，全部 NOT NULL。创建时间/身份从 origin 取，不重复成为可变配置。

head `(account_id,ordinal,journal_id,updated_xid)` deferred FK 精确指 journal `(account_id,ordinal,id,created_xid)`。head UPDATE guard 要求 OLD tail 为新条目的 previous、ordinal=OLD+1、新条目的 before 精确等于 OLD head；INSERT 只能采用同事务 create origin/journal。immutable 身份，禁止 DELETE/TRUNCATE。

### 2.3 `pp_account_journal`

拟定组字段如下；冻结时应采用封闭行合同，不以开放 JSON 代替下面的明确字段。

| 字段组 | 拟定字段及约束 |
| --- | --- |
| 身份/序列 | `id TEXT PK`, `account_id TEXT NOT NULL`, `ordinal BIGINT>0`, `previous_id TEXT NULL`, `root_id TEXT NOT NULL`, `root_step BIGINT>0`, `created_xid XID8 NOT NULL`, `created_at TIMESTAMPTZ NOT NULL` |
| 操作 | `kind TEXT`：create/configure/configure_rejected/reserve/dispatch/released_no_consumption/consumption_unknown/cost_confirmed/evidence_disputed/expired_without_permit/recovered_unknown/cost_corrected |
| source | `source_kind TEXT`：command/budget_event/dispatch；`source_id TEXT NOT NULL`；不得以 source_id 的任意字符串前缀推断类型 |
| 配置 before/after | 两套 `label,provider,state,limit_amount,configuration_revision`；before 全组仅 create 可 NULL，after 全组 NOT NULL。label/provider沿现014形状与字节界 |
| 金额 before/after | 两套 `held_amount,spent_amount NUMERIC(100,0),blocking_count BIGINT`；before仅create可NULL，after NOT NULL |
| reservation | `attempt_id,binding_id TEXT NULL`；`before_reservation_revision BIGINT NULL,before_state TEXT NULL,before_held,before_spent NUMERIC NULL,before_anomaly BOOL NULL`；对应 after 组；reserve 的 before 全NULL，after prepared/rev1，其余涉及 p 的条目两组完整；配置条目整组NULL |
| 冻结上界 | `upper_amount BIGINT NULL,input_bound BIGINT NULL,output_bound BIGINT NULL,cost_version_id TEXT NULL`；涉及 p 时从正式冻结来源读取/核对，非APP声明 |
| gate | `gate_extra BIGINT NULL,gate_at TIMESTAMPTZ NULL`；只有 reserve/dispatch 完整非NULL，分别等 upper/0。成功条目不允许 caller 提交 passed=true |
| 自动暂停原因 | `pause_event_id TEXT NULL`；只允许实际 anomaly effect 对应的本条 budget event；若已 paused，不重复增加 config revision，但原因保留 |

唯一项至少有 `(account_id,ordinal)`、`(source_kind,source_id)`、`(root_id,root_step)`，以及 head/FK 所需完整复合 UNIQUE。`previous_id` 以 `(account_id,ordinal-1,previous_id)` 的受 guard 关系连接同账户前项；不能只 FK 一个存在但别账户的 ID。ordinal1 iff previous NULL/kind=create，create 恰一个且等 origin。

`kind` 与 source_kind 封闭对应：create/configure/configure_rejected→command；reserve及八类 effect→budget_event（reserve精确type=reserved）；dispatch→dispatch。生成 nullable discriminator 列 `command_source_id / budget_source_id / dispatch_attempt_id`，让真实关系 FK 可逐类型声明；不能只有没有关联约束的万能 source 字符串。

### 2.4 原 source 的新增绑定列

- `procurement_accounts`：`pp_version SMALLINT NULL, pp_created_xid XID8 NULL, pp_origin_journal_id TEXT NULL`，三者全NULL或version1完整。只在真正 INSERT 的闭合 create 根生成并保持不变。
- `procurement_reservations`：`pp_created_xid XID8 NULL, pp_origin_journal_id TEXT NULL, pp_last_journal_id TEXT NULL`；新 tracked p 全非NULL；origin immutable，last 只在受控 mutation 对应条目推进。当前 last 精确 FK 到 journal 的 `(account,attempt,after_revision,after_state/桶/anomaly,id)` 或等价 guard+固定复合键。
- `procurement_budget_events`：`pp_root_id TEXT NULL, pp_root_step BIGINT NULL, pp_journal_id TEXT NULL, pp_created_xid XID8 NULL`；新tracked effect 全非NULL、不可改。它们与 journal 在 account/attempt/event/root/step/top-xid 上**双向 deferred FK**，source INSERT 不可采用预建 journal。
- `procurement_commands`：account_create/configure 的已存在目标来源增加 `pp_root_id,pp_root_step,pp_account_journal_id,pp_created_xid`，与对应 journal 在 operation/target/root/step/xid 双向绑定。configure not_found 无账户journal，binding/observation命令沿各自域，不伪造账户变化。
- `upstream_attempts`：真实首次 prepared→sending 时写 `pp_dispatch_account_id,pp_dispatch_root_id,pp_dispatch_root_step,pp_dispatch_journal_id,pp_dispatch_xid`，全NULL或完整。绑定 journal 的 account/attempt/root/step/xid；之后字段绝对不可改，哪怕 delivery 已 completed/unknown。实际 source UNIQUE，不能给同attempt第二份发送许可。

这些新增列由 DB 固定根或 source trigger 写入，APP 无 UPDATE/INSERT列权；stamp 不接收 caller 值。旧行保持 NULL。source 独立遍历必须发现每个 tracked source 缺失journal；不能只从journal遍历source。

共同 `root / typed source / step` 行合同由根与成本片统一命名：每次真正 DB 操作新 root_id，同事务多个来源按 step 区分；不以 GUC、最后一行或事务 ID 自身猜 root。账户 private helper 只接该共同合同的已出生 typed source。

## 3. 拟定入口与最小 Go 对接

拟定签名如下，金额均由 DB 从真实 source 读取/计算；返回值是原API所需行标识/时间，不输出权限token。

### 公共根：仅精确 APP LOGIN 获 EXECUTE

```sql
pp_create_account(actor text, operation text, input_digest text,
                  label text, provider text, state text, limit_amount bigint)
  RETURNS TABLE(operation_id text,account_id text);

pp_configure_account(actor text, operation text, input_digest text, account text,
                     expected_revision bigint, label text, provider text,
                     state text, limit_amount bigint)
  RETURNS TABLE(operation_id text,account_id text);

pp_complete_attempt_birth(tenant text,request text,attempt text)
  RETURNS TABLE(managed boolean,account_id text,source_id text,
                journal_id text,admitted_at timestamptz);

pp_permit_dispatch(tenant text,request text,attempt text)
  RETURNS timestamptz;

pp_recover_attempt(tenant text,request text,attempt text)
  RETURNS TABLE(changed boolean,event_id text,journal_id text);
```

create/configure 重核当前 admin 并共享现 advisory operation 锁/幂等规则，原正式 command/audit 由 DB 生成，before/after 来自同一变量。input_digest 只保持现API请求身份/重试合同，不作为历史before或授权；幂等命中还直接比较 actor和明确业务输入，不能只信 caller 重复提交相同digest。精确旧JSON digest协议若要求DB重算，须与根冻结既有 Go 编码，不可改用jsonb键序后假称相同。

`pp_complete_attempt_birth` 是现 `RecordAttempt` 成功 INSERT 之后、同事务的闭合末端。实际 binding 在相应 channel/credential 同步锁后检查，必须查到 attempt 真实新 tuple/top-xid、initial cost unknown/NULL、delivery prepared、同request/tenant；锁后独立核验冻结报价及上界来源，不能接受 APP 预插的虚假 rate/version。attempt 的身份、冻结 quote 和 cost 字段也须撤销 APP UPDATE 或按完整受控 guard 拒绝，不能只保护 cost_amount/status。存在binding才创建 managed p 与其origin/reserved；没有binding则创建明确unmanaged出生来源。**不能在 AFTER attempt INSERT 暂时没p时就判unmanaged**。attempt 出生的 deferred闭合要求该末端完成，不能提交未分类source；旧attempt不能登记新origin。此处与成本片共用attempt出生表，账户片不另造第二套origin。

`pp_permit_dispatch` 完成采购 p prepared→sending 和 upstream prepared→sending，真正UPDATE upstream触发原 PB guard。它在执行 UPDATE 前已获得账户/PB锁并保存 gate before；真正 tuple 的 DB pointer绑定journal，PB返回时间绑定相同source。deadline/当前binding/PB资格失败整体回滚；不允许生成无upstream source的成功条目。

### 私有 helper：APP/PUBLIC/projector 无 EXECUTE

```sql
pp_lock_managed_account(root_id text,attempt text) RETURNS void;
pp_account_effect(root_id text,attempt text,source_kind text,source_id text)
  RETURNS TABLE(event_id text,journal_id text);
pp_apply_supplier_pb(root_id text,attempt text,procurement_event text)
  RETURNS void;
pp_append_account_journal(...) RETURNS text; -- 仅DB局部值，绝不公开
```

`pp_account_effect` source_kind 只接受待共同冻结的 normal/recovery/dispute/approval 原件，逐类查真实 root、tenant/request/attempt、step和top-xid；不收 held/spent/anomaly、绝对金额、expected count/revision 或 passed 标志。它重锁时按 account→supplier PB→p→head，允许上层已按同序持有；不得回头获取 request/attempt。

finalize/approve 根在 `request/customer-reservation→attempt→account→supplier PB→p` 之后采集 cost decision，再调用此 helper。成本片生成proposal消费/normal资格原件，账户helper消费它们；same amount更正仍有唯一cost_corrected PE/journal，p revision按实际字段变化。

identity重复或终态新增普通证据若无PE且account配置/桶/blocking不变，不追加虚假的account no_effect条目；成本/evidence decision保留当时account ordinal/tail观察cut，不能称其为新准入。applied no-op config和真正0 delta PE则推进account ordinal。

Go public API 保持原签名。候选私有 SQL writer替换 `procurementEvent/updateProcurementHold/prepareProcurementDispatch` 内部DML及admin create/config callback，真正输入解析/用户授权/请求上层锁仍按现API合同。finalize/propose/approve/unmanaged_correct 的DB公开根归成本片统一；不同时保留Go先改桶再补证的第二writer。

所有公开根还须比较 `session_user` 为安装时固定的实际 APP LOGIN；definer内部 `current_user=writer` 本身不是调用方证明。owner SET ROLE APP、另一LOGIN继承APP或peer调用都拒绝。writer同时NOINHERIT；root/actor参数不代替原业务角色与状态重核。

## 4. Gate 与原子步骤

账户 gate 准确顺序：

1. READ COMMITTED；先按既有request/attempt层锁序，取account NO KEY UPDATE，再取supplier PB首配namespace/control，取已有p FOR UPDATE，最后head FOR UPDATE。
2. **锁等待之后新SPI读取** account/head完整配置和金额，并从真实p按 `account_id AND anomaly AND state IN ('unknown','disputed')` 查blocking count。建议新增等价partial index。head与实际account、真实blocking必须相等，否则invariant错误；不可用head自证。
3. reserve从request硬界+attempt冻结两维费率计算upper，核其int64并复核冻结quote；同组before变量检查active、blocking0、H+S+U<=limit。dispatch同样active/blocking0、H+S<=limit，extra=0。
4. 用相同before变量计算合法target；写原source、p、a、必要cost reset/pause/PB；从实际RETURNING取得真实revision/结果，追加唯一journal并推进head。调用方没有两个SQL statement之间插入另一mutation的机会。
5. 原014投影与020 PB deferred checks保留；新deferred check比较head/current a/全部p/全部PE及blocking，双向source闭合。发生任何错误整个操作/事务回滚。

dispatch 的精确完整前层矩阵必须与根统一：现调用点先拥有身份/渠道/客户source/attempt及customer PB控制层，再到account/supplier PB/p/head。闭合根不能依caller bool声称已锁；需固定DB锁helper重入真实既有锁，原始readonly身份查找只选择锁路线。实际 upstream UPDATE 返回原 `pb_dispatch_checked_at` 后，用该同一PB时刻再核Key expiry、客户/request deadline与当前有效quote；新价格按时间生效不因持账户锁而停止。失败必须整体回滚已经临时产生的sending/journal，不能以较早gate_at代替PB时刻资格。

PB effect必须从同一实际PE派生，不能以客户refund代替供应商无消费。正常confirm计算token/金额超界，仍入账且原子pause；dispute为H=max(U-oldS,0)、S=oldS；correction为H0/S=新绝对金额且减价要求invoice。state anomaly/blocking均从真实old/new推导。

自动pause与致异常effect在一个语义journal保存前后配置和cause；active→paused revision+1，已paused不增加；合法更正消除blocking也不自动resume。普通unknown不是异常阻断。后续人工active但仍disputed，reserve/dispatch仍拒绝。

## 5. 阻断所有 raw DML 的精确策略

所有 revoke 必须清理表级和遗留列级 ACL、默认ACL、继承/SET/ADMIN路径；重复provision不能由旧 blanket grant恢复候选写面。

| 对象 | APP候选权限 | NOLOGIN writer权限 |
| --- | --- | --- |
| origin/head/journal及共同root/source证据 | SELECT；无INSERT/UPDATE/DELETE/TRUNCATE | SELECT/INSERT；仅head允许指定投影列UPDATE；无DELETE/TRUNCATE；immutable guard防UPDATE历史 |
| procurement_accounts | SELECT；撤销INSERT/全部原列UPDATE/DELETE/TRUNCATE | SELECT/INSERT；UPDATE配置与held/spent，追踪列仅受控出生/内部动作；无DELETE/TRUNCATE |
| procurement_reservations | SELECT；撤销INSERT及state/held/spent/anomaly/身份/追踪列UPDATE/DELETE/TRUNCATE | SELECT/INSERT；UPDATE状态/桶/anomaly/updated_at/last pointer，014身份guard继续有效 |
| procurement_budget_events | SELECT；无INSERT/UPDATE/DELETE/TRUNCATE | SELECT/INSERT；无UPDATE/DELETE/TRUNCATE |
| procurement_commands | SELECT；account_create/configure仅由固定根插入；APP可保留其它命令所需INSERT，但BEFORE guard拒绝APP直插账户两kind | SELECT/INSERT，生成正式快照/来源，immutable继续 |
| upstream_attempts | 按成本片保留必要权限；tracked prepared→sending 的早期guard只允许current_user=writer；APP不能改追踪列 | dispatch根可UPDATE delivery_state与pointer；成本片另列cost字段闭包 |
| audit/outbox | 按既有APP其余业务权限 | 固定根所需SELECT/INSERT，不得修改已有成本审计/消息 |

APP现有 `SELECT ... FOR UPDATE/NO KEY UPDATE` 要求至少一列UPDATE权限。若需保留Go上层/PB commander的纯锁操作，建议给accounts/reservations各增加 `pp_lock_noop BOOLEAN NOT NULL DEFAULT false CHECK(pp_lock_noop=false)`，仅授APP `UPDATE(pp_lock_noop)`；不授金额/状态/时间/identity以换取锁权。这一PostgreSQL列ACL行为须由根实际LOGIN验证后冻结，不能靠猜测通过。该列只允许恒false，不引入业务变化。

tracked upstream真实sending的早期role guard必须在原 `pb_dispatch_guard` **之前**执行（拟名`pa_pp_dispatch_caller_guard`），阻止APP raw UPDATE先拿PB control、再在后期补账户锁的反转。闭合dispatch根已经先锁account/PB，才以writer UPDATE upstream。后期实际source capture（若采用trigger）拟名`pp_dispatch_source_capture`，在`pb_dispatch_guard`之后验证PB冻结时间/真实NEW值；固定catalog必须核所有触发器名/时点/顺序。

不以角色名字符串参数、GUC或pg_trigger_depth作授权。writer为不可LOGIN、不可SET/继承到APP/peer/projector的角色；仅固定根SECURITY DEFINER可以进入其权限。私有helper无APP/PUBLIC EXECUTE。函数拥有者writer不拥有表/schema，不能禁用source触发器或DDL。

## 6. PB invoker 在 writer 身份下的最小访问闭包

writer不会自动继承APP权限。闭合根和触发器中的原PB invoker均以writer执行，必须逐个授予以下实际闭包；不是只给新journal表就能运行。

### 6.1 search_path 必须一致

固定definer `SET search_path = <精确受信业务schema>, pg_catalog, pg_temp`，新函数内全部应用对象显式schema限定。业务schema对APP/peer/writer无CREATE；pg_temp放最后。**不能把pg_catalog置首**：020 `pb_scope_sync` 用 `current_schema()` 参与advisory namespace hash，错序会绕过原APP真实namespace barrier。

[权限合同第3节](b-procurement-writer-authority-contract.md#3-精确-search_path-与-pb-锁域)记录了专用 PG16.15、actual APP LOGIN 与 NOLOGIN writer 的隔离实验：受信 schema 首位遵守真实 namespace 排他锁，pg_catalog 首位绕开。可复现材料见[命名空间实验源码](../../docs/verification/testdata/procurement-writer-namespace/main.go)。它只支持 search_path 决策，不能作为 B-P4 账户、审批或成本业务验收。

### 6.2 表/列闭包

- SELECT：原root所需 request_records/reservations/settlements/upstream_attempts/credential_procurement_bindings/procurement_accounts/reservations/events；全部六张PB表及B-P1 observation三表。对应业务引用users/channel/credential/quote由各公开根按实际用量另列，不能GRANT SELECT ON ALL TABLES。
- PB `periodic_budget_windows`：INSERT固定出生输入列；UPDATE `held_amount,spent_amount,revision,last_event_id`。窗口前缀字段由B-P1 trigger覆盖，无APP可声明值。
- PB `periodic_budget_allocations`：INSERT现固定origin/source/subject列；UPDATE `held_amount,spent_amount,source_held,source_spent,state,revision,last_event_id`。
- PB `periodic_budget_events`：INSERT现固定 `id,control_id,allocation_id,source_kind,source_id`；before/after/seq由原guard计算；无UPDATE/DELETE。
- PB controls：UPDATE仅 `held_amount,spent_amount,event_seq,last_event_id,max_observed_window_start`；**不授INSERT或enabled/limit/configuration_operation/revision配置写权**。原guard自动assign的NEW.revision不要求caller直接授该列UPDATE。
- B-P1 observation：SELECT origin/head/journal；INSERT journal的现permissions-v1指定输入列，UPDATE head `(ordinal,last_observation_id)`；不授origin/head INSERT（writer不创建PB control），不授任何历史修改。pb_observe建新window会触发该闭包，不能遗漏。
- 原PB约束/guard对statement/deferred执行的SELECT继续有权限；APP自身在COMMIT触发invoker的SELECT面仍保留。

这些为表/列能力，无法靠ACL把window限定仅一账户；安全边界是writer无可被caller获取的角色路径、无开放SQL和无公开generic PB mutate函数。公开根只从真实request/account选择control，source严格绑定；不能授APP执行private `pp_apply_supplier_pb`。

### 6.3 函数调用图

reserve/effect：`pb_require_read_committed → pb_sync_unconfigured → pb_scope_sync`；原 p/event stamp；`pb_gate → pb_observe`；allocation/event INSERT触发 `pb_guard_allocation / pb_guard_event / pb_apply_event / pb_guard_control/window`；PB source/projection deferred checks。`pp_apply_supplier_pb`固定实现原tx_supplier/tx_effects的source/projection语义，不接受before或金额。

dispatch：真实upstreamUPDATE触发 `pb_guard_dispatch`，进而 `pb_check_customer/supplier → pb_sync_unconfigured → pb_gate/pb_observe`；必须保留customer PB当前窗口资格和supplier PB资格，不只跑采购账户gate。

B-P1窗口出生还进入 `pb_prefix_window_capture → pb_prefix_append → head/journal guards` 及deferred origin/journal/source checks。EXECUTE只给这份逐函数清单的真实闭包；不能偷懒GRANT EXECUTE ON ALL FUNCTIONS。完整函数签名、直接/间接触发调用、默认PUBLIC EXECUTE与writer范围，需SQL材料审定时完整枚举。

## 7. explicit profile 与旧默认

候选仅装在**精确original001–022 + 已冻结B-P1 candidate/permissions**之后的新隔离profile；必须声明每份输入材料SHA/角色绑定与顺序，不能以“库里有三张表”判定已安装或授权。旧生产默认migration/provision/Store构造器、原V3 reader/basis/catalog根不改。

Go 候选接缝建议为私有 `procurementWriter` 实现：默认 legacySQL；拟定的 `NewProspective` 或同等入口使用普通显式 selector 选择候选 executor。该名称与 API 尚待冻结，selector 只选择实现，不授数据库、完整历史或维护权威；不要求先取得额外不透明授权 token 才能在隔离 actual APP 中生成新业务原件。

数据库精确安装、固定公开根的实际 APP LOGIN 检查与最小 ACL 负责拒绝错误环境或旁路。禁止因表/函数存在、一次调用成功、caller 布尔值或 schema 探测而自动切换或认证安装。完整来源认证、开账、observer 与 runtime 仍须 I2 的固定 catalog/history/新 profile 和原事务核验，不能由 executor selector 替代，详见[权限合同第9节](b-procurement-writer-authority-contract.md#9-候选选择安装及核验)。

旧账户不在原位登记新 origin。本提案的最小候选是 **prospective**：遇 legacy 未追踪账户，候选完整前缀路径明确拒绝；旧 profile/旧库继续原行为。此边界只限制旧历史接入，不缩减安装后完整新历史的业务范围：新账户仍须覆盖额度复用、配置变化、异常/争议、多次更正及无金额变化操作。未来历史 continuation、生产 upgrade 与 R2 须另行冻结，不能静默补造旧 origin，亦不能据一次当前 anchor 认证过去。

## 8. actual APP 必须验证的反例

1. APP直接INSERT PE、改p金额/state/anomaly、改account金额/limit、写tracking/dispatch pointer、插账户command、插/改head/journal：均拒绝；不会在后续固定根adopt预建原件。
2. APP raw upstream sending在原PB guard之前被拒绝；不能先拿PB lock再补account；合法根能执行原PB真实permit并产生同source账户journal。
3. A/B各700,L1000，A释放后B成功；A未释放时B拒绝。四source960历史混合终态通过新完整域；不依赖Σupper例外。
4. gate等待账户/PB期间pause/缩limit；锁后fresh gate拒绝。仅恢复state active但仍disputed也拒绝；普通unknown不会凭空blocking。
5. 同事务连续多个PE/config/no-op正确ordinal；分叉、跳号、缺source、source无journal、跨账户/attempt/tenant或同额替换source均拒绝；0delta correction仍一条唯一journal。
6. cause是token-only anomaly（金额0）也pause；真实费用超upper仍入账；更正下降后历史异常不抹掉；settled清blocking不自动resume。
7. 仅Go INSERTattempt未闭合birth不能COMMIT；binding后来出现不能把已明确unmanaged birth改managed；旧attempt/账户不能补登记。新出生成本非unknown/NULL拒绝。
8. namespace good/bad search_path与B-P1新window capture逐权限验证；writer缺某个必要列/函数时fail closed；APP不能SET ROLE writer、借ADMIN/默认ACL/函数劫持取得它。
9. 每步骤失败/savepoint/cancel/deferred错误全部回滚；COMMIT未知仅按原operation/source查询。原Go只重试40P01/40001且不含provider动作，任何新逻辑不得重发模型请求。

## 9. 冻结前剩余阻塞项与可并行分片

必须先由根汇合：共同root/typed source/step与所有companion表的字段；`pp_account_effect`各source分支的精确semantic输入；create/configure旧digest与角色快返；全部函数原文/trigger顺序/列ACL与writer身份；PB SQL helper逐语义翻译；dummy锁列实际ACL验证；newprofile固定descriptor/roots/读集/旧材料mapping。

在这些冻结之前，不得执行本稿SQL或更新原checker接受集。可并行纯core/手算oracle模型、成本/request源合同、SQL/ACL闭包审查；共用SQL、`billing/procurement.go`与profile/root接线由单一集成人控制。PG和重测试由根串行调度。

本提案的上位边界以[写入权限合同](b-procurement-writer-authority-contract.md)、[身份 ABI 合同](b-procurement-identity-contract.md)和 [D0 设计稿](b-procurement-prefix-design.md)为准。共同 root/step/typed source 字段、完整 DDL/ACL 及精确写入入口须与成本/request片汇合后再冻结；未冻结字段不得被引用为已实现能力或数据库验收依据。

# B-P4 account command writer v1

本材料是隔离候选，不是正式迁移、在线接线或整个 B-P4 完成。先执行原 001–022、B-P1、identity v1 与采购 relation v1，再由受审 owner 装配本 SQL 和最小 ACL。原 014/020/021 trigger 全部保留。其他八类公开根仍不存在；账户链包含非 command journal 时明确拒绝，不能据账户局部通过宣布全链可写。

## 输入、结果与时限

固定公开根为 `pp_create_account(text,text,text,text,text,bigint)` 与 `pp_configure_account(text,text,text,bigint,text,text,text,bigint)`，参数顺序沿账户合同。均返回唯一行 `(outcome text,operation_id text,actor text,kind text,target_id text,status text,reason text,before_state jsonb,after_state jsonb,created_at timestamptz,root_id text,account_journal_id text)`。首次正常或业务拒绝的 outcome 都是 applied；匹配重放为 replay，返回原 root/journal，不增加来源。not_found 没有 journal；revision_conflict 有 before=after 的 configure_rejected journal，command.after 为 JSON null。

输入 SQL NULL 显式 PP001（`invalid_procurement_configuration`）；UUID 与 Go uuid.Parse+String 等价（输入允许 nil UUID，DB 生成身份非零）。label/provider 为 1–100 UTF8 字节，无 Unicode Cc，无 Go TrimSpace 首尾字符；state 为 active/paused，limit 非负，expected revision 正数。digest 由原 Go encoding/json 的 Kind/Target/Payload 顺序、string 金额、HTML/2028/2029 转义前像计算，actor 另比较。

公开根 SECURITY DEFINER/W owned，固定 search_path 为受信 schema,pg_catalog,pg_temp，依赖关系显式 schema 限定；所有其他函数 SECURITY INVOKER。渲染器先选择不出现在任一绑定标识符中的 `$moni_ac_N$` body delimiter，再进行一次标识符/OID替换，合法 schema 名内的 `$$` 不得提前闭合函数体或被递归解释。实际 session_user OID 必须是绑定 APP OID，执行 current_user OID 必须是 W；owner+SET ROLE APP 不能替代 actual APP LOGIN。根要求 READ COMMITTED，frontend 开始该 statement 前已设置 statement_timeout=5000ms、lock_timeout=3000ms，逐根检查精确值；函数不执行 SET、不重启或延长 statement/SPI 计时。调用者保存原总期限，异常不转换成功，未知提交只读观察原 operation/root。

## 固定函数 ABI

除下表 pure 函数外，所有普通及 trigger 函数为 VOLATILE/PARALLEL UNSAFE，非 STRICT；纯函数为 IMMUTABLE/PARALLEL SAFE/SECURITY INVOKER。private 函数仅 W 可显式执行；APP COMMIT 调用三只读 validator，必须补授其传递纯函数及相关关系 SELECT，不能将 deferred wrapper 改成 definer。

| 分类 | 精确签名与返回 |
| --- | --- |
| pure | pp_ac_uuid_v1(text,boolean) → boolean |
| pure | pp_ac_text_v1(text) → boolean |
| pure | pp_ac_command_digest_v1(text,text,text,bigint,text,text,text,bigint) → text |
| pure | pp_ac_time_v1(timestamptz) → text |
| pure | pp_ac_snapshot_v1(text,text,text,text,bigint,numeric,numeric,bigint,timestamptz) → jsonb |
| private | pp_ac_require_writer_v1() → void |
| private | pp_ac_require_root_v1() → void |
| private | pp_ac_mutate_v1(text,text,text,text,bigint,text,text,text,bigint) → 与公开根相同 TABLE |
| readonly | pp_ac_validate_command_v1(text) → void |
| readonly | pp_ac_validate_account_v1(text) → void |
| readonly | pp_ac_validate_root_v1(text) → void |
| trigger | pp_ac_guard_v1(), pp_ac_account_write_v1(), pp_ac_account_result_v1(), pp_ac_command_write_v1(), pp_ac_audit_write_v1(), pp_ac_parent_write_v1(), pp_ac_deferred_v1() → trigger |

validator 只读、无锁、无补写、无权限发行；它们不是未完成全域 pp_validate_* 的 success stub。本材料仅展开账户命令域闭包。

## 状态机与顺序

校验输入 → operation advisory xact lock → 读取原 command → actor/digest 冲突 PP002 `procurement_operation_conflict`（先于角色检查）→ users FOR SHARE 核 admin/active（否则 PP003）→ 匹配重放并完整认证历史。新配置按 account→head 行锁；等待之后新 statement 重读 account/head、预算与 blocker。旧无 tracked origin 的账户返回 PP906，不登记旧账户、不回填。

新命令预分配非零 root/source/journal/audit 身份，INSERT 完整 source **先于效果**；create 使用 account 原 DEFAULT created_at、revision1/H0/S0，INSERT RETURNING 得真实时间后创建 origin；configure 沿原 revision trigger，只有 config tuple 变化才递增（同配置在 max revision 可成功）。journal 接旧 tail 的准确 before、ordinal+1（包括 revision_conflict）；head 逐次推进。随后保存 command、audit、step，最后一次完整 INSERT seal root。not_found 只保存 source/command/audit/step/root。source command_created_at 是独立 DB clock；command BEFORE 强制采用该字段，W 不获得 INSERT(created_at)。

root、step、source、origin、journal 不可 UPDATE/DELETE/TRUNCATE。head 只允许当前 xid 的准确单步，禁止 DELETE/TRUNCATE。aa invoker guard 在原 revision/command/payment guard 前执行；zz account result 在原 revision 后核结果。APP 的 account UPDATE(created_at=created_at) 和 W 对 users 的真正 UPDATE 均被拒；SELECT 行锁不会触发 UPDATE guard。command 保留 credential_bind/balance_observe 的原路径且必须所有新 pointers NULL；共享 audit 普通业务仍沿旧路径，账户命令审计必须精确 source 和 G 六列。

新 snapshot created_at 固定 UTC RFC3339Nano（PG 实际微秒精度，裁尾零、Z）。历史快照比较将 created_at 按同一 instant 比较，其他八字段为 JSON 严格相等；这明确不同于旧 Go 进程时区的 JSON 字节表示。digest 仍逐字节兼容，不能对 JSONB::text 哈希。

## 双向核验、错误与验收

每个命令核 source↔原 command↔审计精确 slot、step↔最后 seal root、journal 与 branch/snapshot/digest；账户核真实 origin、从 ordinal1 到唯一 head 的完整链、source 的每个命令及当前 account 配置/金额、原 reservation/event sums 与 blocker。不可用“存在 root”替代枚举；缺来源、错字段、额外来源、断链或 claimed tracked 原件部分 NULL 都是 PP902 `procurement_history_invariant`。带账户来源证据的 command/root 即使被改成非账户 kind/scope 也按 PP902 处理，不能降为未知域。完全没有 tracked 标记的旧对象，以及本片尚未重放的非 command account journal 是 PP906 `procurement_prefix_untracked_domain`，不会冒充完整认证。非 command journal 在返回 PP906 前，先核 root/step/xid/实际 APP OID、transition 或 dispatch delivery 的精确身份、account/attempt/journal 及原 PE/dispatch pointer；必要来源缺失或错绑返回 PP902。这些必要身份边的核验不是相应域的数值或状态机认证。PP901 `procurement_execution_forbidden`，PP903 `procurement_execution_protocol`，PP905 `procurement_history_exhausted`；保留 42501/235xx/55P03/57014 等真实执行错误。

为使未知域分类在实际 APP COMMIT 下仍能读到必要身份，额外 SELECT 闭包精确增加：`pp_procurement_transitions(id,event_id,account_id,attempt_id,root_id,step_no,step_kind,created_xid,journal_id)`、`pp_attempt_delivery_sources(id,root_id,step_no,step_kind,created_xid,attempt_id,mode,dispatch_account_id,dispatch_journal_id,dispatch_xid,pb_dispatch_checked_at)`；原 `procurement_budget_events` 的 id/attempt_id 与五个 pp pointer，`upstream_attempts` 的 id、六个 pp_dispatch pointer 与 pb_dispatch_checked_at。它们不新增 W DML，也不授权任何模型密钥或原业务秘密。

专项验收由集成人在专用库串行执行 actual APP LOGIN；至少覆盖 create/configure/noop/revision_conflict/not_found/replay，冲突先于失权、失权重放拒绝，来源先于效果/根最后、真实时间与摘要差分，raw SQL/SET ROLE/owner/worker/peer/私有 EXECUTE/父锁写、partial pointer、缺/错/多来源、commit deferred 与专项撤权回滚，锁等待、ordinal/revision 溢出和未知域关闭。SQL/contract 静态存在不能记作实际 PG PASS；统一架构、权限与验证记录待全部并行结果集成后同步。

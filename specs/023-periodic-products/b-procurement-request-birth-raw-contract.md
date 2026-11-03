# B-P4-A1：raw SQL、误授与真实 guard 验收合同

2026-11-04；本页先冻结新增 `internal/procurementrequestbirth/request_birth_raw_postgres_test.go` 的真实测试边界。承接[request writer](b-procurement-request-birth-writer-contract.md)、[最小 DDL/exact-link](b-procurement-birth-exact-link-integration-contract.md)与[完整 ACL](b-procurement-full-acl-contract.md)。只新增本合同及根审定后的独立测试文件，不修改已有 SQL/摘要、fixture、原测试、架构、Git 或数据库；PG 由集成人串行运行。测试源码完成不等于实际 PG 已通过。

## 1. 身份、装配和 oracle

复用本包现有 `newFixture/fixture.snapshot/fixture.checkBirth`，只连接专用 PG16 `127.0.0.1:55439/moni_test`、安装 LOGIN `moni_test`；受测 SQL 使用 fixture 的 actual APP LOGIN，W 仍 NOLOGIN。不得 owner+SET ROLE 代替实际 APP。owner 只准备管理父数据、精确临时误授、安装/移除故障 trigger，以及独立读回；不手插 RequestOrigin/head/root/step 作为成功证据。

正控制先由原 `billing.Store.Reserve` 真实成功产生参考 request。raw INSERT 的列集与原语句完全相同，仅 fresh request_id、其余 20 列取该合法原行；原零售价/tenant domain、bounds、nullable IP、PB stamp 和出生仍由实际 guard/018/020/AFTER hook处理。测试不会临时禁用任何原/新 guard、FK、deferred 或 native CHECK。

每个失败比较 fixture 全业务快照，包括原 request/reservation/幂等/Key held/账本/PB/recovery/audit/outbox 与四张来源表；额外查询自然 ID、root 与 source 的逐槽关系，不只看计数。误授前后读取有效权限，恢复后必须等于原值；错误事务回滚后才由 owner恢复权限/故障材料。所有 SQL 标识使用 pgx.Identifier，不把测试字符串当 shell 代码。

## 2. 等价 raw INSERT 的准确接受集

固定 APP 21 列：

```text
id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,
input_upper_bound,output_limit,profile_version,policy_version,request_hmac,
request_deadline,instance_id,streaming,route_revision,client_ip,
pricing_group_id,tenant_pricing_revision,retail_priced_at
```

在 RC、lock_timeout=3s、statement_timeout=5s 的真实 APP 事务中，完全等价 raw INSERT 必须成功生成真实 Origin、Head、Step、Root。新增独立 oracle 在**该 APP 事务**观察未提交图并逐字段比较；不可使用另一 owner 连接看不到未提交行来猜成功，也不依赖 INSERT RETURNING 的旧 NEW 含内部 UPDATE 后的 pointers。

两种结束方式必须分清：

1. 不写 reservation：原 request INSERT/自动 birth 本身成功，APP 可读出完整图；外层 COMMIT 必须由原020以 SQLSTATE `23514`、Message `periodic source reservation missing` 拒绝，全部本次行回滚。这不是 A1 guard 的 PP902，也不能把 INSERT 成功说成成功商业 Reserve。
2. 本 fixture 无 customer PB control；APP 按原列写入真实 reserved reservation companion 后，可 COMMIT，独立 oracle核真实出生。这个原始写面正是 A1 保留的信任边界：它没有执行原 Go Reserve 的完整资金/Key/PB/审计流程，不能据此认证商业硬预占或扩大 A1 能力状态。原 Go Reserve 的成功证据仍来自正控制。不得伪造 source 或为缺 reservation 关闭020。

同一 APP 事务连续两条合法 raw request，各用自己的 companion，必须得到不同 root、同一实际 xid、精确各自来源，无互借 source/head/step。其余已提交根保持。

## 3. 隔离与正常权限拒绝

| 用例 | 预期且必须定位的拒绝层 |
| --- | --- |
| RC 等价 raw INSERT + 原 companion | 成功 COMMIT；只认证本图和明确原约束 |
| RR / Serializable / Read Uncommitted 下等价 raw INSERT | BEFORE `PP903 procurement_execution_protocol`；无新 request/source |
| 同三种隔离下 tracked request `UPDATE status=status` | APP 本来有该列 UPDATE，必须是 BEFORE PP903，不能用42501冒充隔离证据 |
| RR readonly 调实际两只读 validator | 原合同允许，不能因 tracked 写限制阻断只读 |
| 原最小 ACL 下写冻结列、出生列或四表 DML | 准确42501权限拒绝；不能把它报告为 guard 已执行 |

初态/派生 INSERT 的误授另用准确列权：异常 status、billing_status、latest，任一非 NULL PP/PB 出生槽，必须在早期真实 guard PP902，不能等 NOT NULL/FK/CHECK 或018才失败。不能宣称 guard 能识别“显式传入有限 created_at 与 DEFAULT 得到同值”的 SQL 来源：该 INSERT 列当前靠最小 ACL 拒绝，不能拿误授后的观察夸大 DB 默认时钟证明。

## 4. 误授后逐冻结字段、回改与 pointer 攻击

对一个已由真实 Reserve 产生的 tracked request，owner 每次只给 APP 当前用例所需列 UPDATE，先核有效权限 false→true；原来已允许的 status/billing不撤其正常权。以有效不同值修改以下完整 26 槽：

```text
id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,
input_upper_bound,output_limit,profile_version,policy_version,request_hmac,
route_revision,request_deadline,instance_id,streaming,client_ip,
pricing_group_id,tenant_pricing_revision,retail_priced_at,created_at,
pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at
```

每项首条 UPDATE 必须真实 `PP902 procurement_history_invariant`，然后同一失败事务的“再改回”语句必须 `25P02`，说明第一次修改已令事务失败；不能先允许改写再以终态改回掩盖历史。nullable字段用 NULL↔非NULL，两种时间用不同 instant，xid用明确不同合法xid8，其他值保持可解析类型，使失败定位于新 guard。

另覆盖原三列 birth pointer 分别清空、换别 root/step/xid、部分联合、跨已提交 request 的 root，pp_last/latest 非 NULL，以及无真实财务依据的 succeeded/settled；预期 PP902。对 legacy 行，在同一事务先产生真实新 raw request/source，再把其同 xid/root 装到旧行，仍 PP902，不能用“已有同xid来源”补旧证。

当前 request guard 认证的是**值不可变**；误授后仅 `col=col` 的无变化赋值可能沿合法 no-op 分支成功，不能写成该 guard 已实现 UPDATE OF 禁写。source/head/root/step则对任何 UPDATE（包括同值）一律拒绝。若未来要求 request 在误授下连同值赋写都拒绝，须另冻结不可绕过的列事件合同并实现，不能用本测试虚构该能力。

## 5. 四表 raw DML 和实际出生中错边

先测当前 ACL 的42501，再临时误授精确 INSERT/UPDATE/DELETE；actual APP 对 Origin/Head/Root/Step 的真实 raw INSERT、同值 UPDATE、DELETE 必须 `PP901 procurement_execution_forbidden`。最小 INSERT 只给普通 locator 列即可，guard须先于缺字段的 NOT NULL 拒绝。TRUNCATE测试必须先计算并误授随机 schema 内准确 FK 级联闭包，避免因其他关系缺 TRUNCATE权或缺CASCADE先失败；真正 statement guard须PP901，全部权限精确恢复。

仅 raw APP auth 拒绝不能替代 payload exact-link 验收。owner另安装排序早于 `aa_pp_rb_*` 的小型 BEFORE fault trigger，**在原 Reserve 的真实 capture 内**改变即将出生的 NEW，随后由原有真实 guard拒绝：

| 实际受测行 | 故障变化与预期 |
| --- | --- |
| Origin | wrong request/tenant/root、冻结 bound不同、nullable IP不同、NULL必要payload或wrong xid；PP902，不能由原FK/NOT NULL抢先拒绝 |
| Head | wrong request、非零ordinal、非NULL tail或wrong updated_xid；PP902 |
| Step | wrong root/source/kind/step/xid；PP902，不能借已提交root追加 |
| Root seal | wrong request/tenant/result/step_count/xid或已提交root ID；PP902，已创建的本次source/head/pointer/step一起回滚 |

fault只改真实 NEW，不禁用guard、不持久修改旧来源、不以owner插入一套假来源后称成功。每个用例先保存全快照，调用实际 APP原Reserve，精确核PP902，移除fault，再比完整快照；后续正常Reserve仍成功，证明fixture未被损坏。source/root同xid但错对象不构成许可。

## 6. 交付与退出

根审本页后独占新增 `request_birth_raw_postgres_test.go`，同包复用fixture，不修改原测试或SQL；先仅 `TEST_DATABASE_URL` 未设置的编译/专项静态检查，PG只由根串行执行新顶层测试，保留实际SQLSTATE/Message与最终零残留证据。实际PG通过后才由集成人更新架构/验证记录与状态。本页不覆盖全域A2账户/attempt/成本关系、真并发锁/撤权全集、生产供应商、支付、1Panel、HA或性能，也不把既有toy exact-link实验作为本矩阵通过。

新增测试已实现六个 `TestPostgresA1Raw*` 顶层；`env -u TEST_DATABASE_URL go test -race ./internal/procurementrequestbirth -run TestPostgresA1Raw -count=1` 已通过编译/无PG运行，六组数据库测试因缺环境变量跳过。尚未运行实际PG，不能据此记录raw商业/guard验收完成。

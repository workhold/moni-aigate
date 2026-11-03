# B-P4-A1：真实 COMMIT 已完成但回执丢失的窄验收合同

2026-11-04；承接 [A1 writer](b-procurement-request-birth-writer-contract.md)、[预算收尾](b-procurement-request-birth-budget-contract.md)、[真实钱包等待](b-procurement-request-birth-wait-contract.md)与[PB等待](b-procurement-request-birth-pb-wait-contract.md)，并按 [HLD](../../docs/design/HLD.md) / [LLD](../../docs/design/LLD.md)保留未知结果不重放。**状态：先冻结后实施；根已完成真实PG1顶层/3子项与纯wire6子项，精确结果见[验证记录](../../docs/verification/procurement-request-birth.md)。**

本片只新增本合同和根审定后的 `internal/procurementrequestbirth/request_birth_commit_unknown_postgres_test.go`。不改原 Store/SQL/摘要/fixture/ACL/001–022或生产hook，不把库接口错误改成成功。PG仅根在专库串行执行，代理只做显式清除数据库开关后的纯验证/编译。

## 1. 精确问题与原实现依据

本片只制造这一真实窗口：**原 APP Store.Reserve 的全部写入及真实 PostgreSQL COMMIT已经成功，但客户端没有收到该COMMIT的成功响应，原Go调用返回传输失败。** 它不是断言COMMIT前网络断开、server拒绝COMMIT、所有未知窗口、恢复系统或生产网络已经验收。

源码依据是[原事务](../../internal/billing/store.go)和[原Reserve](../../internal/billing/reserve.go)：transactionOnce最后直接返回 `tx.Commit(ctx)`；transaction只对真实PostgreSQL `40P01/40001`做原有有界重试，不重试传输失败。Reserve在任何error时删除本Store的active标志并返回零Reservation，不把未知提交暴露成已知成功。

已核锁定的 [go.mod](../../go.mod) 为pgx v5.7.6。该版本 [tx.Commit](https://github.com/jackc/pgx/blob/v5.7.6/tx.go)调用无参 `conn.Exec(ctx, "commit")`；[conn.exec](https://github.com/jackc/pgx/blob/v5.7.6/conn.go)明确无参使用simple protocol。因此前端单条 `Q` 消息的SQL为 `commit`，后端真实成功为 `CommandComplete` 消息 `C`、tag=`COMMIT\0`，随后为ReadyForQuery。发送Q只是意图，**不能**作为server已提交的oracle；本片必须同时观察真正C帧与独立owner连接的已提交可见性。

## 2. 唯一测试传输接缝与实际身份

复用现有 `newFixture`，仍只允许PG16专库 `127.0.0.1:55439/moni_test`、安装LOGIN `moni_test`、随机schema及受限actual APP LOGIN。先在原APP连接通过原Release关闭合法legacy预占、核钱包10000/0和Key0，使用原Commander配置tenant/Key两个scope（enabled大限额/disabled追踪），保留真实receipt/source与原时钟；不手写control、来源或资金事实。

新pool只能克隆 `f.app.Config()`，使用其中原DialFunc实际拨号，仅将返回的真实loopback `net.Conn` 包成测试内部观察连接；不换DSN/user/schema，不SET ROLE，不用owner承担业务写入，也不启动外部数据库代理。校验配置仍为原字面loopback/端口/数据库/APP、无fallback且TLS关闭；不手动关掉原本开启的TLS来制造可观察路径。实际连接peer必须是该专库loopback。

arming前获取该pool的真实连接，核 `session_user=current_user=原APP`、实际backend PID；底层必须是本片包装器，不能误接其他连接。先正常完成身份查询并把连接交还pool，再只arm这个已认证连接的一次COMMIT丢回执。该pool没有其他并发任务；若原Reserve用了其他连接、没有观察到唯一指定COMMIT或故障未命中，测试失败，不能当成功跳过。重连后的连接不重新arm。

包装器只观察该合成fixture协议，不能输出DSN、认证消息、SQL/参数/正文、秘密或完整wire流。保留必要计数与阶段布尔值、PID及安全错误，不增加生产可配置开关或hook。

## 3. Wire状态机与有界资源

### 3.1 前端

在已完成startup的指定连接被arm后，按PostgreSQL typed message（type字节＋大端length，length含四字节自身）增量观察Write。不能在任意payload中搜索字符串COMMIT；只识别完整 `Q`、唯一终止NUL及规范化后精确 `commit` 的SQL，不接受多语句或把参数字节当提交。

可在向真实net.Conn写入前记录完整Q意图，避免并行网络读取先见响应；这不是已发送/已提交证明。部分Write、多个帧合并、帧跨Write均按消息边界处理。除指定丢响应外，原bytes照常交真实连接，不伪造server消息或Store回执。保留唯一Q计数，实际后端C未到不能推进故障完成态。

### 3.2 后端

从连接建立开始按typed帧增量读取真实后端流，保持原bytes/顺序；认证、查询、RowDescription/DataRow及其他CommandComplete正常传给pgx。无TLS的专库startup后端同为typed帧，不解析或记录认证内容。缓存跨Read/短读和已有部分帧，不把网络分段当消息边界；超时/EOF保留已读状态，不丢半帧后继续冒充正常流。

只有指定连接已观察到目标Q，且完整读得真实 `C/COMMIT\0` 时，才记录故障命中，**整帧不交给pgx**，关闭该真实连接并返回EOF/原传输错误。该C的任何header/payload字节以及后续ReadyForQuery都不能提前泄漏给pgx。不能丢另一个普通CommandComplete、ROLLBACK或ErrorResponse后声称已提交。

parser使用固定有限消息/累计缓存界（覆盖本片小型fixture），越界、畸形长度/终止符或非预期协议使测试失败并关闭本片连接；这是测试harness失败，不是产品业务拒绝或验收通过。读/写状态分别同步，故障arming/计数使用互斥或原子保护，支持 `-race`。纯传输控制可用内存分段流/net.Pipe检查完整帧透传、分段Q/C识别、非目标消息不触发和目标C零泄漏；这类控制不能代替真实PG提交证据。

### 3.3 期限与清理

原Store RC、lock_timeout3s、statement_timeout5s均保持；被测Reserve使用有限context（不延长产品期限），dial/read/write/deadline仍委托真实net.Conn。故障在server完成后立即关闭指定连接，不能靠固定sleep选择窗口。失败/取消时关闭本片连接、取消原调用并有界收取结果；pool/连接清理先于fixture schema/角色清理。没有重新安装、重发生成或把未知提交改成回滚的清理动作。

## 4. 实际调用、独立提交与唯一效果oracle

冻结一个明确带非空IdempotencyKey/RequestHMAC的新ReserveInput，输入100、输出20，原两价1000000，R120。用新pool创建的**同一个原billing.Store**调用Reserve，wire故障只arm一次。

必须同时满足：

1. 观察器准确命中一个完整前端Q和一个真实成功C，drop次数恰1；原调用返回非nil传输error和零Reservation，不能返回业务成功、request_exists或伪造可恢复回执。不得把context预先到期/SQL失败当作命中。
2. owner在独立连接、原调用返回后读取，能看到指定request、reservation和完整Origin/Head/Step/Root；原checkBirth逐槽相等，root实际session_role_oid=APP OID。观察独立已提交行才证明真实COMMIT完成，不靠Q或本连接未提交读。
3. owner独立核request_idempotency的tenant/operation/key/HMAC/request_id精确等于原输入；只有一条该身份记录、一条指定request/reservation、一个本次birth root/step/head/origin。reservation状态reserved、金额120；钱包available9880/held120、Key held120/spent0。
4. 双scope原PB各一条new allocation/reservation effect，marker/control/window/admission、revision1、held120/spent0准确，原ledger与pb_check_customer通过；audit/outbox/recovery各为原一次效果。将这些事实连同旧合法基线保留为完整提交后快照。

本片引用已有 `birthBudgetSnapshot/checkBirth/birthBudgetEffects/birthBudgetWallet` 并另核本次自然身份与唯一业务伴随行，不从新source反推金额期待，也不只数root。所有资金及来源来自原Store实际写入；owner不补source或手工确认“成功”。

## 5. 原幂等观察与active缓存边界

丢回执之后不恢复/重放第一次写入，不将未知结果转业务成功。后续只用原已提交的同一tenant、IdempotencyKey与RequestHMAC进行原入口的**已存在身份观察**；这些调用在request_idempotency冲突分支结束，不能再次到达财务预占或模型派发：

| 调用 | 预期 |
| --- | --- |
| 原Store、完全相同原输入与RequestID | 原DB幂等分支 `request_exists`、Message=`inference idempotency key already used`、ResourceID=原RequestID；不能是active缓存的 `request already active`，据此核第一次error确实清除了本Storeactive标志 |
| 新Store、完全相同原输入 | 相同已存在身份错误与ResourceID |
| 新Store、只换fresh RequestID而保留原幂等身份/HMAC | 仍指原RequestID；新RequestID及任何新预占/来源不出现 |

每次之后完整提交后快照逐字不变，原request/reservation/root和所有资金/PB/ledger/audit/outbox/recovery效果保持唯一；观察器不重新丢任何回执。不得把 `request_exists`包装成第二次成功Reservation，也不自动改IdempotencyKey重新预占。明确仅在非空幂等身份的原API拒绝分支验证，不推断无幂等Key、换Key/HMAC、其他Store API或进程重启场景已认证。

不创建attempt、不调用模型provider，不新增最终用量/finalization/settlement；此测试不为调用方授予未知消费可重试的通用策略。

## 6. 交付与保留范围

根审定后仅新增指定test，helper使用独有前缀，不改共享fixture或原SQL。代理可运行纯wire控制及显式清除 `TEST_DATABASE_URL`、`PB_PREFIX_DATABASE_URL`、`PROCUREMENT_IDENTITY_DATABASE_URL`、`PROCUREMENT_ATTEMPT_RULES_DATABASE_URL` 后的编译/race；必须分别记录纯验证与未运行PG，不能写为商业验收通过。

根专库串行运行本片 `TestPostgresA1CommitUnknown*`，实际零SKIP/FAIL并确认本片资源清理后才更新架构/验证。根此前PB control等待race2顶层/6子项、零SKIP/FAIL、12.812秒是另一专项，不计作本片COMMIT未知证据，也不重复加总。

只关闭“server确认成功且owner独立可见、客户端丢提交回执”这一个窄窗口；COMMIT前/执行中断网、server拒绝、取消并发、全部网络错误、跨进程恢复/HA、active预算cap/窗口跨期、完整catalog、A2/A3/settled链及正式迁移/生产仍按原门禁另验。

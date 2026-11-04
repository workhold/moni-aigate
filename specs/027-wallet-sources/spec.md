# 027：现行钱包资金来源与开账

2026-11-04，范围与数据库/事务合同冻结；现行实现已汇合，限定本地验收见[记录](../../docs/verification/wallet-sources.md)。依据用户要求，贯通资金来源、钱包开账、预占、结算、释放、消费退款和可见核对；不再把离线证明实验作为前置。本规格替代022-C0中尚未接入服务的生产接线方案；已有研究保持记录身份，不自动安装。

## 目标与当前版本边界

一个现行钱包模型、一份客户预占、一份唯一结算。人工核款、运营券、支付capture与显式开账都生成不可变来源；消费沿来源FIFO，结算与退款精确回原来源。采购账保持独立，第二次上游安全尝试不重复分配客户资金。

MVP不支持旧内部资金历史无损升级。正式023迁移在发现既有journal、funding receipt、reservation、settlement、退款事实、非零钱包/平台科目、或Key非零spent/held时明确拒绝，事务回滚，不改余额或伪造历史。开发验收使用独立全新当前数据库；已有开发库保留，不删除、不改校验和。正式发布后再单独承诺升级合同。

“开账”是管理员在**停止服务的新钱包**上显式导入一个已确认初始余额，记为非现金、不可原路退款的opening来源，同时产生平衡journal。不是猜测旧充值来源，不是生成现金退款资格。空钱包自然从0开始，不需要开账实验或维护proof。

## 当前数据合同

追加 `migrations/023_wallet_sources.sql`，六核心表，另有开账原件表：

| 表 | 冻结列（除注明外TEXT；金额BIGINT，累计比较用NUMERIC/big.Int） |
| --- | --- |
| funding_lots | id PK（等于funding receipt ID），tenant_id，funding_receipt_id UNIQUE，source_kind，source_id，refund_class(nonrefundable/sandbox_original)，issued_amount>0，fifo_at TIMESTAMPTZ；UNIQUE(tenant_id,id)，来源/金额/时间不可变 |
| funding_lot_balances | lot_id PK，tenant_id，available_amount>=0，held_amount>=0，consumed_amount>=0，revision BIGINT>=1；三桶之和按NUMERIC必须等于lot本金；FK(tenant_id,lot_id) |
| funding_lot_events | id PK，tenant_id，lot_id，operation_id，journal_id，kind(funding/reserve/finish/refund)，revision>=1，delta_available/delta_held/delta_consumed BIGINT，created_xid xid8（DB生成，不接受伪造），created_at TIMESTAMPTZ；UNIQUE(lot_id,revision)，UNIQUE(operation_id,lot_id)，不可变 |
| funding_reservation_lots | tenant_id，reservation_id，request_id，lot_id，allocation_index BIGINT>0，amount>0；PK(reservation_id,allocation_index)，UNIQUE(reservation_id,lot_id)，复合tenant FK；不可变 |
| funding_settlement_lots | tenant_id，settlement_id，reservation_id，allocation_index，lot_id，charged_amount>=0，released_amount>=0；PK(settlement_id,allocation_index)，FK原reservation边，charged+released=原分配；不可变 |
| funding_consumption_refund_lots | tenant_id，refund_order_id，settlement_id，allocation_index，lot_id，refund_index>0，amount>0；PK(refund_order_id,refund_index)，UNIQUE(refund_order_id,allocation_index)，FK原settlement边；不可变 |
| wallet_openings | id PK，tenant_id，external_ref UNIQUE，amount>0，actor，created_at，created_xid；不可变原件，提交时必须有唯一wallet_opening receipt和journal；不可由APP创建 |

所有关联通过tenant复合FK约束，不以URL授权代替数据隔离。来源、事件、分配边不授UPDATE/DELETE；余额仅授三个金额列和revision的UPDATE，不授DELETE；projector对新表无写权。余额变更必须与同事务、同revision、同operation事件精确匹配；递延约束核对来源/分配/事件/钱包journal的完整方向，失败整笔回滚。不能只加Go缓存而不约束资金原件。

资金receipt新增source_kind=`wallet_opening`，source_id指wallet_openings。新增平台科目`WALLET_OPENING_CLEARING`（非正余额），入账journal kind=`wallet_opening`、reference/operation_id=opening.id、actor=原件actor。`funding_journal_valid`和现行VerifyFunding必须认识该来源；开账也经过ApplyFundingTx、lot genesis与唯一credited事件，不另造钱包写入函数。报表明确此为初始余额导入，不能称现金收款。

manual_credit/voucher_grant/wallet_opening均nonrefundable；sandbox_capture仅sandbox_original（不能当真实支付退款资格）。lot ID等于receipt ID，fifo_at取receipt.created_at；相同时按lot_id字节序。零额预占/结算合法但不建零本金lot或虚构分配边。

## 唯一资金路径和锁顺序

保持既有来源/授权/request/Key/模型/周期controls的顺序，再锁**本次全部ledger_accounts按ID**，再锁**本次lot balances按lot_id**。helper只消费原pgx.Tx，不Begin/Commit、不联网、不重试生成，不新增tenant UPDATE gate。

1. ApplyFundingTx：原来源确认→全部账户锁→唯一平衡journal/receipt→lot+初始A=本金/H=C=0及revision1 genesis事件→原审计/outbox/外层CAS；savepoint回滚覆盖全部来源效果。receipt重读不重复发lot。
2. Reserve：授权/价格/周期/账户锁→读取并锁当前可用lot→现有私有FIFO planner→等待完全部锁后重验Key到期、request deadline→INSERT request/reservation→原journal→连续分配边与事件，A-=R/H+=R。lot等待必须在request INSERT之前，因为INSERT固定周期归属时刻。金额为0时分配为空。
3. finish：原request/Key/周期/账户→锁原分配lot→最终DB时刻检查24h截止→唯一settlement/journal→按原分配正序收费C、剩余R-C逐来源释放，H-=原分配、C+=收费、A+=释放。到期C=0，不延长资格；Settle/Release/Recover/Reconcile共用它。
4. ApproveRefund：原双审/订单/request/settlement→全部账户→原收费lot→按原收费边逆序退回未退部分，C-=refund/A+=refund；不改Key.spent或周期spent，不变更来源资格。重复审批返回原结果。

每个新request的分配合计必须等于reservation金额；每次finish所有边合计charged/released必须对应唯一settlement；逐边累计refund不得超过charged。既有lot_plan.go的精确FIFO、finish和逆序退款算法直接接入，不另写不同数学。上游RecordAttempt不重新分配客户lot；客户unknown继续保留H，采购unknown依019独立保留，恢复永不重发生成。

## 开账命令和维护

`moni-aigate open-wallet --tenant <UUID> --reference <受控外部业务号> --amount <微元整数> --actor <管理员UUID>`：需MAINTENANCE_MODE=true、使用migration/owner连接、获得现有服务单实例锁；服务运行时拒绝。确认tenant/admin存在且active，钱包无历史journal/receipt/request、所有余额0且Key无spent/held。每tenant只允许一次非零opening；reference全局唯一；同tenant/ref/amount/actor重复读取原receipt，无新资金，异意图冲突。SQL原件仅owner可INSERT，APP不能伪造开账。CLI错误不输出DSN/SQL/秘密，未知提交仅按原reference只读核查，不盲重试。

开账后即可使用普通人工入账、券与调用。`check-wallet-sources`只读核对来源/receipt/本金、事件序列、三桶、原分配、settlement/refund、钱包A/H与lot汇总；任何不完整输出失败，不自动修余额或发行运行权限。限定当前数据模型，无全历史profile或catalog原字节认证。

开账生产入口为`Store.OpenWallet(ctx,WalletOpeningInput)`：显式maintenance标志、规范非零UUID与正微元金额及受控reference缺一即拒绝；入口自身为获取连接及全部业务工作设置至多10秒总预算，不能只依赖CLI期限。入口自行验证current_user=session_user且为wallet_openings表owner，用一条专用连接获取现有服务session advisory lock并在同连接执行唯一事务，不能由CLI调用者绕过这些检查。锁的响应未知或COMMIT结果不明时主动关闭该底层连接；退出时先ROLLBACK未完成事务，再确认unlock，unlock失败关闭连接后才归池。重复意图只验证原receipt/lot并返回，发现不完整不补写。CLI两命令在serving配置之前分流，仅需数据库连接；open-wallet优先MIGRATION_DATABASE_URL，check-wallet-sources只读使用DATABASE_URL；均先验证完整当前迁移，不启动监听器、后台worker或读取服务加密密钥。结果输出安全JSON；核对不平衡退出非零，所有执行错误不回显DSN或SQL。

## 读接口和控制台

- 客户 `GET /api/wallet/sources`：沿当前tenant/member账单授权，返回分页安全来源：lot_id/source_kind/refund_class、issued/available/held/consumed（微元字符串）、created_at。客户不读外部业务号、付款凭据、采购或其它tenant。
- 客户 `GET /api/requests/:id/funding`：复用当前请求详情租户与owner/member权限，返回原预占分配及已结算/退款金额；不能据此允许其它成员看请求。
- 平台 `GET /api/platform/wallet-sources?tenant_id=...` 与只读核对：仅admin/finance_operator/finance_approver；明确微元、开账非现金及退款类别。
- 钱包页提供来源分桶列表，请求下钻提供来源分配，平台可查看核对结果。统一目录和现有账单/经营报表保持一套API，媒体接028后仍进入同一lot分配。

## 一体化实现与验收

共同类型/SQL冻结后并行：资金集成人独占四写入口/helper；另一人持正式迁移/权限/开账CLI/核对；HTTP与控制台按固定DTO独立实现。全部结果汇合后串行专库验证、刷新架构、check/build、提交推送。

必须覆盖：人工/券/payment/opening四来源完整入账；跨lot FIFO、零价、部分收费/释放、两次安全attempt客户只分配一次、多次逆序退款；并发预占与双审/幂等、额度溢出、savepoint/审计故障回滚；等待lot跨Key/请求/24h截止；实际APP权限、数据库负控及复合FK；开账服务锁/一次性/冲突/非现金/旧库拒迁且原件不变；UI筛选/身份切换、真实HTTP商业链路与核对。

本地供应商fixture不代表正式支付或模型账单认证。没有完成四入口、CLI、读取/控制台和专项前，不将027标记完成；同步媒体/异步任务/Realtime按028及整体设计继续实现。

## 现行数据库角色边界

角色配置与启动只读检查使用同一现行矩阵，不在运行时探测或兼容旧资金 schema。APP 对 `wallet_openings` 仅 SELECT；对六张 source 表 SELECT/INSERT，其中 `funding_lot_balances` 只允许更新 available_amount、held_amount、consumed_amount、revision，其余新表不可 UPDATE/DELETE。`ledger_accounts` 保留 SELECT/INSERT，仅允许 UPDATE(balance)，禁止更新账户身份、归属、kind 和 DELETE。列级残余授权必须在重复 provision 时清除，不能只撤表级权限。

支付 worker 只新增 `funding_lots`、`funding_lot_balances`、`funding_lot_events` 的 SELECT/INSERT 与余额四列 UPDATE；不授 opening、人工/券来源、客户预占/结算/退款或其分配表的读取和写入。既有 ledger 权限仍为 SELECT、INSERT(tenant_id,kind)、UPDATE(balance)。projector 对上述七张新表及 ledger_accounts 均无权限。任何 PUBLIC、grant option、可达角色或 SECURITY DEFINER 绕过都由现行角色验证拒绝。

钱包十二个函数按固定 search_path、SECURITY INVOKER、无 PUBLIC EXECUTE 管理。APP 可执行现行受审函数；worker 仅执行 `wallet_lot_balance_guard`、`wallet_lot_event_stamp`、`wallet_lot_valid`、`wallet_event_valid`、`wallet_journal_valid`、`wallet_account_valid`、`wallet_source_fact_guard`，不得执行 opening、FIFO reservation、reservation、settlement、refund 的五个函数。`funding_journal_valid` 和 `wallet_event_valid` 使用 PL/pgSQL 分支派发，让合法 sandbox 入账不查询 worker 无权读取的消费/旧来源关系；这不赋予其它分支执行权。

现行启动核对完整表/列权限及55个 payment/funding/wallet 函数；正文摘要从受审021与023 SQL源文件静态固定，023替换的 funding_journal_valid 使用新正文。检查数据库时不得从待验数据库生成期望 hash。保留离线历史研究测试不等于当前运行支持旧 schema，其 fixture 若需要旧 provision，须显式固定历史测试材料而非向现行服务新增 fallback。实际权限、DML拒绝、函数漂移及重复 provision 测试须在专用 PostgreSQL 串行运行后才计入验收。


## 现行读端与控制台合同补充

标准来源/请求分配沿现有 `s.page`：50+1，created_at/id倒序，next_cursor必为字符串、空串结束。请求行id=lot_id，allocation_index为规范正整数字符串，reserved_amount/refunded_amount为微元字符串；charged_amount/released_amount在未结算时为null，结算后为精确字符串，不能把null改0。列表展示顺序不代表FIFO执行顺序，原allocation_index给出实际分配次序。

平台核对GET `/api/platform/wallet-sources/check?tenant_id=<UUID>` 需显式单tenant和三个平台角色之一，返回tenant_id/as_of/balanced及source/event/allocation/wallet_mismatches四计数。钱包来源不接受客户自选tenant扩大scope，请求分配复用原请求授权；非本人/跨tenant404。平台 `/platform/wallet-sources` 页面输入tenant并手动核对，请求详情挂来源分配，钱包展示当前来源三桶。所有读端对身份/目标/路由和卸载的晚响应失效；无修复、无生成权限。

原入账回执与摘要同样使用四来源合同：`FundingSourceKind` 包含 `wallet_opening`，回执列表允许显式筛选该来源；摘要新增必填微元字符串 `wallet_opening_amount`，`total_amount` 恰等于人工、券、沙箱capture和开账四项之和，不保留三项旧DTO回退。回执是入账原件，来源三桶是当前余额，两者不能混为累计收入。控制台标注初始余额导入为非现金，累计钱包入账可含开账，模型销售收入仍只来自唯一settlement。

通用查询DTO装配将数据库 `time.Time` 统一为UTC再序列化，输出RFC3339的`Z`形式，保留微秒/纳秒精度及原instant。分页仍使用`time.Time`和原(created_at,id)顺序；前端不得为了接纳服务端本地时区而放宽既定UTC合同。

实施与验收入口：[来源架构](../../docs/architecture/wallet-sources.md)、[验证](../../docs/verification/wallet-sources.md)。028媒体仍按独立阶段交付，不因来源片通过自动完成。

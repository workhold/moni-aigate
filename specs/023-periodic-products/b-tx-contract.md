# 023-B 独立事务 helper 冻结切片

2026-11-01，根冻结本文件的**独立 Tx helper 实现与隔离 PG 测试**。承接[B主合同§4–7](b-contract.md)和[核心SQL](b-core.sql)，不授权追加应用迁移、修改billing、Reader/Commander、HTTP/main或前端接线。三份SQL仍作为设计fixture；其后变动必须先校准本合同和测试。此切片可与022-B1并行，不代表平台已提供周期预算。

## 1. 精确导出 API

在既有 `internal/periodicbudget` 增加主合同§7的 `ScopeKind/Scope/Subject/Amount/CustomerLocks/SupplierLocks/Admission/Effect` 及八个精确函数：`LockCustomerTx`、`AdmitCustomerTx`、`RecordCustomerReserveTx`、`FinishCustomerTx`、`LockSupplierTx`、`AdmitSupplierTx`、`ApplySupplierEffectTx`、`PermitDispatchTx`。签名逐字沿主合同，无pool、clock、provider或trusted参数。Amount.Decimal必须是既有规范Int64AmountString，并与数据库原R/U完全相等，不接受任意delta。

仅 `Lock*` 可创建有效的不透明令牌；零值、不同pgx.Tx对象、已结束事务、不同tenant/key/account/subject的交叉使用均拒绝。令牌保存私有原事务身份及范围/已锁control列表，公开Admission.At/WindowIDs只是结果：Record必须验证它与私有源/原事务/原时刻/原窗口完全一致，外部改字段不能伪造准入。列表复制，不能共享可修改的公开slice。事务身份比较须对不支持安全比较的Tx实现返回受控错误，不能panic。不得仅以同一连接/PID认同前后两个事务。

`LockSupplierTx(ctx,tx,"")` 是唯一显式“无采购账户”令牌构造方式；它只可给PermitDispatch使用，后者须在数据库确认attempt没有procurement reservation且当前credential没有binding。Admit/Apply拒绝此令牌；不能用零值代表不存在。

所有方法拒nil/零Tx；方法不Begin/Commit/Rollback/Savepoint、不重试、不访问环境/网络。调用者始终负责整体回滚与提交，不承诺helper失败后事务仍可提交。读取/扫描必须关闭Rows并传播错误。

## 2. 锁和权威来源

LockCustomer前置是主合同全部tenant、membership/user、Key、model/售价group根已由调用者按原序取得，不能由helper在后层补取。先以固定tenant→key顺序调用 `pb_sync_unconfigured` 加入首配屏障，再按control UUID排序取本tenant/Key对应control `FOR NO KEY UPDATE`，最多两份；保留准确scope/ID映射。LockSupplier前置已有本路径要求的全部客户control（仅派发路径；普通采购收尾不新增客户control）及实际procurement account NO KEY UPDATE，先参加其未配置scope屏障，再锁唯一采购control。无配置仍返回有效、空列表令牌，不创建control。

AdmitCustomer前置所有后续钱包账户等待已结束，request/reservation已在当前tx创建。读取实际request/reservation及 `pb_current_tuple(pb_created_xid,xmin)`，验证规范身份、tenant/Key、同tx、金额、源未决状态及at-creation controller marker与令牌准确对应。以数据库BEFORE INSERT写入的同一个pb_admitted_at对全部已锁control调用 `pb_gate(control,At,R)`，由数据库创建/返回原窗口。不得以Go时钟、客户端At、当前查询时刻或retail_priced_at替代；At须是可表示UTC6，gate失败回滚由调用者负责。

AdmitSupplier同理，验证同txprocurement源、对应account/control marker、prepared/held=U/spent0/anomaly=false；以其自动pb_admitted_at对唯一control调用gate(U)。无control仍返回合法Admission；不得绕过019累计/quote/时限复验（后者属于未来billing接线）。

RecordCustomerReserve重新验证Admission与源证明，按每control插入唯一普通new allocation和原reservation effect。allocation原upper/baseline/metadata由SQL guard派生；helper不写可变金额投影。精确effect已存在则验证其allocation/control/subject/origin/window/源绑定后返回，不重复追加；异体为invariant。重复调用只返回原事实，不能获得新的准入期。

FinishCustomer从指定的真实settlement和request/reservation派生唯一最终效果，对已锁control原allocation追加settlement事件。已完成相同effect只读原事实；已闭合旧源在首配前完成且没有应有allocation的情况保持无事实，不创建catchup。其余漏allocation/来源错绑拒绝。无当前cap/clock门禁、不重分窗口，不回滚钱包或重做结算。

ApplySupplierEffect只接受Effect.SourceKind=`procurement_event`及同attempt/account真实019事件ID，要求其为当前tx新事实（精确已应用原effect可幂等重读）。已有allocation沿原窗；缺失时，marker=当前control只能创建new且执行完整U首事件规则，marker为空只允许SQL guard严格证明legacy_catchup；不得导入任意旧closed为普通new。普通new的窗口仅由源pb_admitted_at取得，legacy永远null；首次全U准入由SQL event guard再证明。后续可信绝对成本可超U，不能截断。无control时确认真实源绑定后无B写入。

PermitDispatch先核令牌范围与真实request/attempt/采购源身份，不取任何前层锁，只执行实际 `prepared→sending UPDATE ... RETURNING pb_dispatch_checked_at`。返回时刻完全由SQL BEFORE guard对全部scope在全部等待后取一次DB At；零行、空At、非prepared或已sending均不能当新许可。无采购令牌须通过本节显式无binding证明。调用者随后用返回At复验全部request/Key/reservation/quote截止；失败整体回滚。helper不发网络。

## 3. 错误与实施证据

输入语法错误`invalid_request`；失配令牌/来源/金额/窗口/缺事实`periodic_budget_invariant`；SQL精确clock regression→`periodic_clock_regression`；客户cap→`periodic_budget_exceeded`，采购cap→`procurement_periodic_budget_exceeded`；其他SQL错误输出安全`periodic_budget_unavailable`。内部可保留Unwrap供调用方识别真实40P01/40001，Error()不得包含SQL、参数、schema或DB原错误正文；不把任意同名error字符串认作受控错误。SQL预算拒绝精确使用SQLSTATE=23514、message=`periodic budget exceeded`、constraint=`pb_budget_tenant|pb_budget_key|pb_budget_procurement`；helper据此区分客户/采购，PermitDispatch也不猜scope，不对外输出constraint或金额。未知constraint即安全unavailable，不把任意23514误判成限额。

测试用真实001–019+三份设计SQL的随机schema、真实ProvisionFor+受限APP/投影身份；不修改预览或保留产品预算表。至少覆盖客户双scope单钱包完整初占/唯一收尾、零token/跨tx/跨subject/Admission篡改、采购完整U与catchup/幂等、无配置及显式无采购、真实sending全scope/不可重放许可、SQL错误脱敏、角色负权限和精确清理。原型时钟fixture必须明确owner仅替换随机schema的pb_now，不称生产时间验收。

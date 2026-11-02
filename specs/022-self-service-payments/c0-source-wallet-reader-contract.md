# 022-C0：同 session 84 表来源与钱包读取（根冻结）

2026-11-02。继续[68表来源读取](c0-complete-source-reader-contract.md)与[V0-WL原行重放](c0-wallet-lots-rows-contract.md)。本片真实读取68业务表与16张C0事实表的固定列并集，在同一owned raw session中执行七项来源算法和实际lot重放；不拼旧Facts、Report或ScanProof，不增加资金writer、生产迁移或HTTP/main。

## 接口、权限与 phase

`fundinglotscan.ReadWallet(ctx, Query, schema, Mode) (WalletFacts,error)`独立于原`Read`。WalletFacts只私有保存本次session及其描述性报告，没有原行/tx出口或外部完成报告构造器。`WalletReport.Scope=complete_source_wallet_graphs_v1`，保留Mode、Status、PhysicalEOFComplete、ScannedTables、SourceRows、SourceBytes、InputMismatchFields、JournalMetadataMismatches、OrdinaryBoundaryMismatches、CoverageComplete，固定Algorithms[8]为ledger/links/funding/customer/procurement/PB/sandbox/wallet，并保留这八项纯结果。旧68表/7算法/原scope及旧Facts不改变，不把84表计数填到旧68表报告中。

维护新增第七`ScanSourceWallet`，旧六值不变，uint8 once仍足够；完整pre+basis可占后续第八位，writer不通过scan或开放SQL取得。`ReadLease.SourceMode`仅对第六或第七kind开放，仍取原authority predecessor。`maintenancescan.VerifySourceWalletMaintenance(*ReadLease)`唯一可信构造：Require成功后接管失败清理，取原schema/mode、私有Query读取同raw、八算法全部返回、唯一Finish后保存私有WalletFacts与真实ScanProof；Verify/Summary只接受原Permit。Resource优先保留，driver与真实取消/超时按既有封闭规则区分，错误整个零结果。

现有Installation/Attest发行只允许16表为空；Verify/BeginScan/Finish不重复空表检查，但仍保持原Permit证据、busy与停服认证。此片不删空表门禁、不发明post-epoch Permit。开账前调用本reader会如实报告缺epoch等数据不符，物理扫描proof不赋开账资格。真正同Init原RR写后读取必须由后续protected init writer/phase状态机驱动；已提交epoch环境的新Plan/post安装授权、严格v2 codec及basis仍另冻。本片真实PG的非空C0验证使用独立owner RR，只证明固定SQL/原值/八算法读取，不冒充上述维护phase或生产writer验收。

## 固定原列与分页

原68表只新增共享物理列`tenants.fl_created_xid`，其余九组WL业务投影均已在原列并集中。16表全部按DDL真实PK；epoch/attestation按singleton枚举false/true，legacy sources按(kind,source_id)两项C序，其余按各自id、receipt_id、settlement_id、refund_order_id、tenant_id或lot_id。operation按id分页，seq仍完整原TEXT由core排序；不以kind/action/tenant/join过滤事实。每页128，满页续查，显式C序、逐项tuple、真实EOF/Err/Close与严格递增游标保持，NULL键属无法分页的Execution。

绑定只来自编译期固定typed声明。四种WS嵌入DTO按匿名struct的声明顺序递归展开字段路径；原生时间envelope是叶子，不展开time.Time。bind/project一一对应固定路径，字段数/类型/重复列/共享kind/限额冲突均为实现错误。不得借反射从DB catalog或调用方内容生成列。

Wallet25组分别为Tenants/Accounts/Journals/Lines/Receipts/Requests/Reservations/Settlements/Refunds与16表完整原列，准确映射见实现固定绑定；尤其voucher source_id=券ID而journal.operation_id=redemptionID、event HeldDelta映射inference_held_delta、attestation ProjectorRoleOID在DTO中先于nullable WorkerRoleOID，不按DDL位置猜字段。

## 原生值、预算与报告

全84表共享100000行/16MiB，行/公共物理列只计一次。TEXT/JSONB原UTF8 bytes，bytea原件owned clone，时间固定timestamptz_send传8字节/NULL再用binary plan还原；每时间32B，bool1B。单字段默认65536B，唯一固定特例为funding_lot_runtime_attestations.proof_canonical=262144B，SQL资格CASE与Go累计两侧一致，仍计总16MiB；旧68入口不因此放宽任何字段。

WalletLotsTime是原生envelope，NULL/±infinity/公元1年/五位年份原样交WL核验，不造zero哨兵。required TEXT/bool原NULL等无法表示的值按物理字段去重记InputMismatchFields，该core明确input_mismatch且不调用；其它core与全部物理表仍扫描/执行。nullable TEXT区分nil与空串，proof bytea保留NULL与空。其它七core各保留旧时间资格。不得以一个core失配跳过余下算法或用独立报告补齐。

所有84EOF后运行七来源core，再从同raw投影WL并实际调用CheckWalletLotsRows，scope必须匹配。总体确定mismatch优先，随后indeterminate，再consistent；CoverageComplete仅表示本固定84表/八算法覆盖：全EOF、各core executed或合法ordinary sandbox N/A、无输入资格缺口、来源原Unproven与WL ReplayPrefixUnproven为零。明确mismatch可同时具有完整执行覆盖；它不是basis/epoch/RuntimeProof或资金权限。没有epoch不是执行错误，也不能把钱包标N/A。

## 验收与交付

独立核84表/25组固定列与PK，旧Read恰68表且不查fl_created_xid/C0；跨页128/129/256/257，singleton false/true与legacy tuple，9共享表只读一次。核proof 65537/262144/262145与普通字段65536/65537、跨首末表共享总预算、NULL/空bytea/driver别名、原生时间、NUMERIC/xid保真、生命周期/typed-nil/中途取消全零；坏输入仍全EOF且其它算法实际执行。

fake从独立手算正向lot图、缺多/错边/伪cache验证确实运行WL；真实PG专库使用原001–022加候选DDL的隔离schema、真实非空C0与同RR旧快照/权限错误/资源界，严格清理。维护专项证明第七kind真实Require/mode/读取/唯一Finish与跨Permit/重复/撤销，仍空C0时如实mismatch；非空postphase未由此认证。普通billing/PB/main及领域测试不导入maintenance/catalog。

根汇合后更新README、HLD/LLD、统一架构、计划与验证，reviewed refresh/check、相关race/真实PG与make check/verify，通过后提交推送。后续优先冻结protected pre+basis/init/post/epoch及全部资金接线，再按原计划退款/拒付、商品、扩展、生产/HA，最后性能。

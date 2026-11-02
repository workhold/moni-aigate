# 022-C0-V0-R：caller-owned RR 原始账本 reader

2026-11-01，**15个顶层专项（5纯、10真实 PostgreSQL）通过隔离验收**。依据[冻结合同](../../specs/022-self-service-payments/c0-ledger-reader-contract.md)，新增[`funding_ledger_read.go`](../../internal/billing/funding_ledger_read.go)及两份测试。没有public wrapper、HTTP/CLI或普通main调用；旧VerifyLedger、VerifyFunding、Rebuild、迁移及provision源码保持原样。

## 实现边界

私有`readFundingLedgerSnapshotTx`在caller原RR内读取三表全部原行，返回算术投影、同页journal额外facts及`supplied_ledger_rows`纯检查。它保留caller事务/wrapper、Conn/PgConn、PID、top xid、DB/schema和实际LOGIN身份，入口与返回重核；只执行SELECT，不Begin/Commit/Rollback、修改GUC或取得业务/advisory锁。30秒总deadline继承更短caller期限，失败只返回四个固定私有sentinel与整个零snapshot。

读取前逐表核schema_migrations与三业务表的permanent heap/共同owner、无RLS/继承/分区、所用列的精确attnum/内建type/nullable/生成属性，以及原单列非延迟PK与有效唯一btree text_ops backing index及其heap绑定。完整latest history仍调用既有`VerifySnapshotTx`；先核其未限定history表的解析OID，并封闭两个旧helper函数的path解析，含default/variadic重载。两处旧helper实现没有改动，这些窄guard不能当完整catalog认证。

三表静态全限定投影，各固定128行，C collation及比较operator/cast限定pg_catalog。第一页以私有bool区分，空TEXT主键不充当下界；每页严格前进、完整消费/检查Rows.Err并关闭，恰128尾页继续到实际空EOF。全部表收完后一次独立纯检查，不做逐页AND、JOIN过滤或SQL金额聚合。总原行≤100000、原字符串≤16MiB，journal复用字段仅计一次，超界不返回截尾结果。

## 最终命令与真实范围

专库`TEST_DATABASE_URL`只通过进程环境提供：

```sh
go test -race -count=1 -run '^(TestFundingLedgerRead|TestPostgresFundingLedgerRead)' -v ./internal/billing -timeout 240s
go vet ./internal/billing
```

最终race退出0、8.548s，15顶层全部通过，无数据库SKIP；vet退出0。三份源码完整SHA、精确验收项与执行计划见[脱敏证据](funding-ledger-reader.json)。最终版本测试源码稳定后重跑，未借用之前失败/旧版测试作为验收。

真实业务正例复用既有人工核款、Reserve、settle及消费退款fixture生成账本，然后由`current_user=session_user=Config.User`的实际受限APP LOGIN在RR RO/RW读取。reader不提交caller未提交sentinel，调用后caller仍可查询和回滚；同RR保持旧视图，新RR观察后来人工入账。nonRR、Serializable、closed、aborted及SET ROLE伪装均拒绝，projector及APP实际撤销SELECT后不能借reader读取。

三表分别实测0/1/128/129/256/300行并计真实page query，页数严格为`floor(n/128)+1`，包含恰128/256行后的空页、空TEXT与Unicode ID、NULL tenant；另300个journal跨页共享两个平台账户，统一算术一致。受控owner坏数据fixture故意停用写侧约束后，六条line完整保留，独立计出缺account=1、缺journal=1、跨tenant=2、重复operation组=1、重复journal/account组=1、不平衡journal=1和account余额不符=1；这些负例不冒充正常写入路径。

真实负例覆盖RLS/forced-RLS、view、inheritance、domain替换列、缺/坏history、pg_temp同名history、缺PK和deferrable PK。四种同名helper函数（精确签名、default、variadic）携带sequence/sentinel/RAISE；预拒后sequence未调用、sentinel未改、原tx仍可SELECT，证明没有先执行再吞错误。目标schema同名text domain、C collation、>与< operator的RAISE探针也不影响限定SQL，额外以真实false cursor页完整消费确认比较谓词被执行。

额外journal事实允许原定义中的空Kind/Reference/Actor，非UTC driver时区转UTC并保留微秒；0/负created_txid、零/越界/infinite时刻拒绝。纯预算精确100000行/16MiB允许，实际100001行和单条额外journal引用超过总字节界返回Resource。资源行界fixture**仅在测试事务临时加C排序索引**，避免782页重复全排序使资源测试变成负载测试；没有新增生产索引。

查询取消通过owner实际ACCESS EXCLUSIVE屏障，使APP reader进入物理Lock等待后取消；返回Canceled与零snapshot，caller负责回滚。纯测试验证late Rows.Err及顺序重复/倒退；实际PostgreSQL infinity时刻验证driver扫描失败，均关闭Rows且不发布部分结果。每个fixture精确核角色backend归零、角色和schema删除，受限pool关闭，caller tx全部收尾；另一次只读末审reader角色/backend均为0，不用前缀清扫删除其他资源。

## 执行计划与后续边界

无辅助C索引的300行fixture，对实际ledger_accounts keyset查询`EXPLAIN`观察为：

```text
Limit  (cost=26.42..26.74 rows=128 width=160)
  -> Sort  (cost=26.42..26.93 rows=203 width=160)
       Sort Key: id COLLATE "C"
       -> Seq Scan on ledger_accounts  (cost=0.00..18.64 rows=203 width=160)
            Filter: ((id)::text > '000127'::text COLLATE "C")
```

该小fixture采用Seq Scan后Sort，未证明当前生产索引支持大历史C排序，也不代表网关p95或生产长历史核验预算通过。后续索引/DDL与量级预算另审。

本片只认证同RR三表读取及传入行的结构与算术一致，不返回Balanced/CoverageComplete、不授Permit/RuntimeProof或资金写权限，不验证完整source、Key、采购、PB/B、C0 lot/epoch或外部支付真实性。统一架构/HLD/LLD与manifest由根汇合集成。


2026-11-02复用改动：为[资金21表同session](funding-financial-graph.md)将原唯一三表reader拆为私有raw读取与原CheckLedgerRows阶段；旧入口保留原SQL、字段、第一页bool与错误身份。新入口以私有预算/context adapter共用同一次账本原始行；新验证范围独立记录，不沿用本文真实PG历史计数宣称本轮已验。

# 022-C0 正式静态目录核验与事务证明

2026-11-02。本片实现`internal/fundinglotcatalog`三个公开发行API、同事务opaque证明与两套受审静态根。最终完整leaf race通过217.140秒；整仓集成检查在下方单列。模块尚未接入普通main，不发行维护Permit、epoch或完整资金RuntimeProof。

## 冻结输入与根审

[合同§10](../../specs/022-self-service-payments/c0-schema-check-contract.md#10-根冻结完整静态检查器的实现与正式期望启用)固定API、身份、权限与资源边界。[五变体记录](funding-lot-variants.md)证明真实三角色ordinary、named/public及两种实际LOGIN owner安装形态；[peer记录](funding-lot-catalog-peers.md)证明完整三schema/八角色sandbox及十组跨域负权；[collector记录](funding-lot-catalog-collector.md)记录封闭格式、raw pre-deparse和固定registry。原离线导出器及原SQL/provision pins保持不变。

| 固定根 | bytes | SHA-256 |
| --- | ---: | --- |
| ordinary-v1.json | 44854845 | a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2 |
| sandbox-v1.json | 13533215 | a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f |

ordinary固定五个完整variant；sandbox固定full-sandbox。根审交叉核原候选完整app schema、原ACL/provision顺序、M/Q的22表187列、37函数原文与flags、52用户trigger及212约束identity/flags。sandbox与原app-only导出样本的函数ACL差异按真实v1安装来源逐项说明，不在运行时过滤差异。所有unknown对象或未认证profile失败关闭，runtime不能从live库更新expected。

## 身份与证明边界

Snapshot继承调用方RR RO/RW；Runtime额外要求指定实际APP/worker/projector LOGIN及RR RO；Installation在同次五秒预算增加16表发行时全空。配置User=current_user=session_user，配置/实际TCP/SQL endpoint、DB/schema/owner/role OID、具体wrapper/Conn/PgConn、PID/backend_start及top xid前后重核。错误只返回ErrCatalog及nil证明。

`InstallationProof`直接保存私有原identity，`Catalog()`每次返回新wrapper；通过返回指针赋值不能替换原empty证据。写后原证明仅保留发行时点事实，不反复要求空表；它不授权新init或资金写入。跨tx/连接/nested wrapper、提交/回滚/abort、取消/断连均不能继续使用旧证明。

runtime数据库CREATE和CREATE/CONNECT/TEMPORARY grant option必须为false；默认PUBLIC CONNECT/TEMPORARY不被误拒。结构、原prosrc与SHA、TOAST/RI、依赖multiset、raw/default/effective ACL、完整角色闭包都参与完整variant比较。

## 执行记录与当前出口

五ordinary公开API非race回归通过，package46.672秒；完整sandbox Snapshot/Installation/实际APP、worker、projector Runtime非race及peer drift通过，package36.583秒。两项为各自执行时源码证据，后续collector优化需最终复验。

首次完整leaf race为失败，package165.277秒，原始日志保存在`.local/c0-catalog-verification/final-race.jsonl`。多个正例触及原五秒预算；未发现data race，但不能据此标为race通过。一处`function_source`负例fixture的参数名不一致已修复，专项非race6.631秒通过。保留真实失败记录，不能改clock、提高预算或跳正例通过检查。

第二轮完整leaf race仍为失败，package225.665秒，日志`.local/c0-catalog-verification/final-race-optimized.jsonl`；177项终态中仅完整sandbox第二次Installation发行触及五秒，其余含五ordinary完整公开API、peer drift基线/恢复和权限反例均通过。单独sandbox公开API曾在race47.674秒中全部通过，但整包余量仍不足，不能用单项代替出口。随后根与独立审阅者确认并冻结：公开链的完整expected精确比较同时承担actual封闭DTO约束，metadata observer仍保留完整DTO验证；权限向量展开仅允许同RR完整字节等值。

最终串行完整leaf race实际退出0，**217.140秒，47个顶层测试、179个终态场景，其中176 PASS、3 SKIP、0 FAIL**。三个skip为独立metadata capture、时序诊断及双轮export；它们已按各自opt-in专项执行，本命令没有重新生成候选或冒称重跑。五ordinary完整公开API、完整sandbox五次发行、peer drift前后成功基线、正文/结构/ACL/type/shadow/endpoint/authentication反例、事务失效/空集时点、数据库最小权限、旧候选投影及两类查询同RR等价测试均实际运行。原五秒预算未放宽；没有data race报告。

```sh
C0_CATALOG_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
C0_CATALOG_LEGACY_REFERENCE='.local/c0-descriptor-candidates/qa-one-c8dec693633e6e5d3e6bf49995a7d0df' \
go test -race ./internal/fundinglotcatalog -count=1 -timeout 600s -json
```

原始最终日志`.local/c0-catalog-verification/final-race-closed-compare.jsonl` SHA-256为`635d4562cedafbd01b685d6a28f813b078c2d2748eefb24b934daab2dc526056`。两次失败保留。当前源码、固定根、旧输入pins、逐项命令与清理审计见[机器记录](funding-lot-catalog.json)。独立只读审计确认flcv/flcp角色、schema、随机DB、backend全部为0；共享public仍由pg_database_owner持有、原ACL、0 relation，database ACL仍NULL。旧导出器七文件及六份provision/migrations源码pin、C0原SQL/两套ACL pin均核相同。

依赖SQL的同RR执行计划从1407.933毫秒降至29.828毫秒，完整raw multiset及真实重复次数相同；五类权限向量在完整三schema同RR比较也逐字节相同。最新公开沙箱专项race46.436秒通过，单次私有诊断4.233秒只用于开销定位，不是HTTP首字节/首token或生产性能数据。整仓`make check`7.546秒、`make verify-mvp`340.981秒均退出0，286项前端、strict TS/Vite、明确专库全Go race/vet及嵌入build全部通过。整仓命令不重开C0独立opt-in，不能将独立capture/export/diagnostic或真实进程运行器冒称在此重跑。

## 未验范围

本轮环境为专用PG16.15（Homebrew）initdb catalog-profile，UTF8/C/C。language OID14038不是通用PG16常量；其他集群、发行版、locale、1Panel及生产安装需要独立profile或新受审符号绑定。未调用付费模型/PSP、未改生产DB，未升级58123旧预览。

后续仍须真实停服Gate/Permit、同RR全源scanner/basis、封闭init、startup全源证明、全资金路径、退款/拒付、订阅、扩展、HA和最后性能验收。静态安装Proof不能替代这些工作。

测试入口架构快照为1114文件/`529d53dc048b`；Go源码在入口后没有改动，后续仅补验证状态及校正细化图中Runtime角色检查先于collector的顺序。全部并行结果汇合、根语义复核后再次显式刷新并只读检查。最终binary摘要仅为本地构建证据，没有部署或升级58123。

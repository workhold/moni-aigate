# B-P2 固定历史与候选安装验证

2026-11-03，基线 `6bed3a98052b26d42a3e5dd5e447968da39331ce`。[合同](../../specs/023-periodic-products/b-prefix-install-contract.md)先于实现冻结；[安装架构](../architecture/periodic-budget-prefix-install.md)说明实际模块边界，精确源 SHA、日志摘要和命令见[机器记录](periodic-budget-prefix-install.json)。

## 已执行专项

明确使用 `127.0.0.1:55439/moni_test` 专库；owner 只负责随机 schema/DDL，原商业语义仍用 restricted actual APP LOGIN。原 001–022 明确 ApplyThrough；候选历史核验、DDL、最小授权和权限复核在 caller 原 RR RW 的同一 savepoint。没有正式迁移或普通服务安装。

最终 reviewed race 专项四包全部通过，没有 SKIP/FAIL：

| 包 | 顶层 PASS | 父子合计 PASS | 时间 |
| --- | ---: | ---: | ---: |
| migrations（本片固定历史） | 9 | 27 | 3.046s |
| periodicbudgetprefixschema | 3 | 3 | 1.243s |
| testsupport/periodicprefix | 2 | 8 | 1.267s |
| periodicbudget（原 B-P1 + 本片安装） | 24 | 101 | 18.173s |

固定历史验证包含 future FakeFS 不扩历史、原 SQL 变更/缺失拒绝、自有副本、RR RO/RW 与 caller 所有权、未提交坏历史、MVCC、明确 schema/temp shadow、重复替换缺项、NULL/超长/额外项及 ONLY。取消测试实际阻塞读取，并在 fresh 5 秒内要求池 acquired 归零、被销毁原 PID 消失、替换连接 idle。

安装测试实际检查成功前对其他连接不可见、caller 可回滚整个候选、重复/部分候选在 DDL 调用前拒绝、错误模式/身份/原历史拒绝、明确路径忽略临时 history/source/catalog 同名表。真实中途 DDL 名冲突与 DDL/ACL 后错误、DDL 后取消均恢复原 catalog/ACL；故障包装器必须实际到达预定阶段才算通过。

保存点创建/释放/回滚的回执不明和回滚失败由 driver wrapper 注入，实际数据库中执行相应 savepoint 操作，要求 `ErrInstallCleanup` 后 caller 整体回滚且候选未提交。这证明库级保守分支，不称为真实网络丢包或顶层 COMMIT 丢回复验收。

权限测试覆盖 PUBLIC、projection、type/列/函数、grant option、新 source UPDATE、invoker/path/额外 overload。默认 ACL 的外部 grantee 与三种角色可达关系共同测试；另独立在候选对象 ACL 不变时仅增加高权角色关系，验证 SET/ADMIN 上下文闭包。旧 broad provision 再跑后确实被局部 verifier 拒绝。原 B-P1 三 scope、reserve/settle/release/采购更正、并发与故障专项继续执行。

## 初次失败与证据边界

首轮正例因 `inet_server_addr()::text` 带 `/32` 被过严文本比较拒绝，改为 `host(...)` 后仍只接受原 loopback；权限矩阵无需放宽。取消测试最初把 pgxpool 异步销毁的瞬时 acquired=1 误判为泄漏，现要求有界完成且核原 backend。中途 DDL 初始使用自动命名主键索引冲突，PostgreSQL 会改名避让；现改为显式 unique 约束名冲突，并检查实际 SQLSTATE/阶段。

这些初始失败日志均保留本地摘要；早期拒绝产生的负例通过未作为最终验收。固定候选 SQL/权限没有修改。原 B-P1 记录保留其提交时源码和结果，新的 fixture 字节及复跑结果以本片记录为准。

前一提交 `6bed3a9` 的 [Linux CI 41553185722](https://github.com/workhold/moni-aigate/actions/runs/41553185722)已完成，10/10 分区通过。下载的 other JSONL 证实原 PB 候选 15/46 与纯核验 19/227 全 PASS、零 SKIP/FAIL，名称与该提交原记录一致；其他独立 opt-in 的明确 SKIP 保留，不能写成通过。这是上一提交的 Linux 证据，不代替本片尚未推送的 CI。

## 回归与后续门禁

初次 `make check` 及明确专库 `make verify-mvp` 均通过：286 项前端测试、strict 构建、50 个 Go 包 race、vet 与最终构建，另 4 包无测试文件。默认整仓命令未启用独立 C0/PB 开关；实际候选与固定历史专项仍以上表单独记账。

整仓命令完成后仅追加了一份旧 catalog 精确拒绝测试，没有改变实现。显式 `C0_CATALOG_DATABASE_URL` 的 supplemental race 测试通过（1 顶层/3 父子，包总计 37.135s）：named ordinary 的 LegacyV1 与 WalletReadV2 均先从真实基线发行 snapshot/runtime 证明，再在同原事务安装完整候选并拒绝旧 snapshot，提交后实际 APP 新 RR 拒绝旧 runtime；LegacyV1 还核原 installation 拒绝。两份原静态材料、缓存及 roots 未变。WalletReadV2 没有 installation profile API，不将其原本不匹配旧安装接口的失败计作候选证据。该新测试由独立日志记录，未伪称包含在此前整仓命令内。

本片不发行 catalog/runtime authority，不安装正式迁移，不打开 R1-B 完整经营认证或 R2 在线资金 ready。新完整 profile 的物理读取、共享预算、basis/catalog/report、protected pre/post/observer/runtime 及历史兼容仍是下一依赖；生产互斥、真实网络未知提交、正式供应商/支付、容器/1Panel 与 HA 均未由本片验收。

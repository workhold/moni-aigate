# 022-C0-T1：维护 Snapshot 验证

2026-11-02。按[根冻结合同](../../specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md#10-根冻结范围与尚未开放的能力)完成库级 T1，根与独立审阅者逐行复核后执行真实专库验证。[机器记录](maintenance-snapshot.json)保存当前源码、日志 SHA、首次失败、最终结果和独立清理观察；[T0旧记录](maintenance-gate.md)保持原当期范围。

## 结果

| 验证 | 实际结果 |
| --- | --- |
| 最终完整 maintenancetrust 真 PostgreSQL / race | 115.412秒，46个顶层、124个终态全部PASS，0SKIP、0FAIL |
| writer锁等待与首次RR观察专项 / race | 包20.034秒，用例3.08秒，PASS；最终全包也覆盖 |
| go vet ./internal/maintenancetrust | 退出0 |
| 独立清理 | 本轮schema、role、backend、当前测试DB advisory全部0；public owner/关系及每fixture共享ACL保持 |
| 运行环境 | PG16.15 Homebrew，专库；fsync/synchronous_commit/full_page_writes均on，max_connections200 |

调用显式 opt-in `C0_MAINTENANCE_DATABASE_URL`；没有改动长期58123预览、生产库、迁移/ACL/expected pins或连接其它模型/支付商。默认整仓检查不自动设置此变量，因此专项单独运行，不以SKIP视为通过。

## 有意义的物理证据

测试经原001–022、原provision及固定C0候选建立随机named-minimal安装，使用实际LOGIN；不伪造InstallationProof。真实wire记录唯一BEGIN与三个SET，Init首次SELECT前一条完整ONLY125全限定全局C序SRE；独立pg_locks逐OID核全集。普通APP SELECT通过，写入和冲突DDL受阻，未知继承不递归加锁且随后被完整catalog拒绝。

writer在排序靠后的voucher_policy合法更新但不提交；独立观察Init正在等待SRE后才提交writer，Init第一次读取必须看见新的enabled/revision。它证明首次快照时序，不冒称尚未实现的basis比对。证据发行保留原真实顶层wrapper；返回proof赋零、换连接、nested wrapper、重复发行及终结后复用均拒绝。

父级与调用级取消在idle、CPU间隙和SQL回包阻塞期间真实关闭socket；无人使用的默认30秒期限、较早期限与Background不得延长均已验。busy时Commit/Rollback/Close/第二stage直接拒绝，独立wire确认没有额外SQL。

隔离QA schema的私有测试sentinel仅证明事务结论：先由另一连接看见真实COMMIT已提交，再只丢driver回复，生产返回Unknown并物理关闭，不重发或补偿。另验COMMIT之后取消、deferred约束回滚、aborted事务ROLLBACK tag、明确Rollback、丢Rollback回复及取消caller仍执行原5秒有界清理。QA SQL不进入生产API，不冒称资金epoch验收。

## 首次失败与修正

首轮112.273秒有123 PASS、1 FAIL：测试用子串SELECT判断语句，误命中静态表名payment_order_capture_selections，尚未走到MVCC断言。仅改测试为固定前导关键字分类；保留原日志及源码pins。随后writer专项和完整最终race均通过。没有放宽锁/期限、少锁表或改产品SQL。

## 尚未交付

T1没有Tx/Conn/context出口、任意回调、Permit/ReadLease stub、账户行锁、history/manifest或前驱reader实现。T2真实维护身份与[完整前驱候选](../../specs/022-self-service-payments/c0-predecessor-contract.md)、T3独立来源、T4 basis/epoch及普通main接线继续后续；没有正式PG/1Panel部署或真实支付验收。

本轮GitHub初始CI另发现测试环境与跨平台断言问题，按[独立CI合同](ci-profile-contract.md)处理，不把本地T1专项当作远端修复已通过。

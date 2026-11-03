# 采购账户行锁的最小列授权实验

2026-11-03；基线 `2223e40a2947846541e34451b6b2465082c18bbe`。为[B-P4共同来源合同](../../specs/023-periodic-products/b-procurement-source-contract.md#8-权限锁权与设计验证)及[账户合同](../../specs/023-periodic-products/b-procurement-account-contract.md)选择APP行锁权限方案，单独验证PostgreSQL权限原语。没有安装完整采购writer、修改旧迁移、provision或普通main，也没有认证完整新profile。

## 实际执行与结果

[诊断程序](testdata/procurement-account-lock-acl/main.go)严格限定专用 `127.0.0.1:55439/moni_test` 的 `moni_test` 连接，要求PG16且无fallback。以随机schema、随机受限APP LOGIN和私有随机密码运行；真实业务连接使用session_user=APP，没有SET ROLE。程序总期限45秒，清理独立5秒。

```sh
# 从私有环境提供 PROCUREMENT_LOCK_ACL_DATABASE_URL，必须是上述专库。
go run ./docs/verification/testdata/procurement-account-lock-acl
```

最终源码实际执行退出0，结果包含cleanup_confirmed=true：

- 只有SELECT权限时，UPDATE、NO KEY UPDATE、SHARE、KEY SHARE四种行锁全部42501。
- 只追加UPDATE(created_at)后，四种行锁全部成功；table UPDATE与held UPDATE仍为false。
- SECURITY INVOKER guard拒绝created_at=created_at及实际改动时间两种UPDATE；四种其他DML（改held、DELETE、INSERT、TRUNCATE）均42501。
- APP事务持有NO KEY UPDATE后，另一owner事务竞争同一行锁得到55P03；150毫秒是lock_timeout配置值，未记录实际elapsed，不把它写成性能结果。
- 行数、身份、时间和held值保持不变；退出清理后查询随机schema和角色均零残留。

最终源码经gofmt及`go vet ./docs/verification/testdata/procurement-account-lock-acl`通过。源文件与实际输出SHA见[机器记录](procurement-account-lock-acl.json)。该程序位于testdata，不会被普通`go test ./...`或线上main自动执行。

## 结论与限制

列级UPDATE可满足SELECT行锁要求，且行锁不执行UPDATE trigger。因此候选可保留APP对原procurement_accounts.created_at单列授权，以维持原PB Commander/绑定入口的账户锁，而不恢复金额或配置写权，也无需新增可写noop列。

**实验guard无条件拒绝全部UPDATE**。它只证明行锁与写guard可并存，未验证真实writer允许分支、原014身份/投影触发器组合、完整共享PB/绑定链路、角色继承/default ACL/PUBLIC闭包或业务负面矩阵。这些仍须在新可安装DDL/权限profile下以actual LOGIN另验；不能以本实验关闭原采购十一项Unproven。

清理复用原owner连接，若连接断开会报告失败；没有断连故障注入或重连清理验收。实际成功清理只说明本次终态。旧整仓本机失败及随后精确源码远端CI分别见[身份叶子记录](procurement-identity-leaf.md#本轮整仓集成与未通过项)和[CI记录](procurement-identity-ci.md)，本实验不替代它们。

# 022-C0-S：caller-owned RR 迁移清单核验（窄片冻结）

2026-11-01，根冻结，允许实现 `migrations.VerifySnapshotTx`。此片是离线维护的依赖，只验证完整嵌入迁移清单，不发行 maintenance permit、epoch 或 proof，不验证对象正文，不接入资金入口；[完整维护合同](c0-maintenance-contract.md)仍为候选。

```go
func VerifySnapshotTx(ctx context.Context, tx pgx.Tx) error
var ErrVerifySnapshot error // 固定安全错误 migration_snapshot_verification_failed
```

调用方必须提供有效的已开始 Repeatable Read 事务；允许 READ ONLY 和 READ WRITE，拒绝 Read Committed、Serializable、nil/关闭事务及失效 context。函数只在该事务执行静态 SELECT，核当前 isolation/access mode、schema_migrations 存在、完整当前 embed 文件集合及逐文件 SHA-256，无 missing/unknown/不同 checksum。没有历史前缀参数、跳过检查参数或运行时默认迁移目标。

它不 Begin、Commit、Rollback、安装 SQL、写数据或取得 advisory/业务行锁；成功和业务核验错误后，原事务仍归调用方。全部失败只返回固定`ErrVerifySnapshot`，不包装或输出history name、行值或driver错误；不改变旧核验入口的错误形状。调用前已经失效的context在DB访问前拒绝；执行中连接/权限/取消造成的 PostgreSQL 事务失败仍按 PostgreSQL 规则由调用方回滚，函数不偷偷恢复。`VerifyReadOnlyTx` 保持只接受 RR READ ONLY；普通 main 继续既有完整 latest 验证，不能用本函数代替运行角色、对象、许可或源图证明。

同一 RR 快照保留原 history 的一致视图；其他连接后提交坏 history，原快照仍按其可见内容判断，新快照拒绝。RR READ WRITE 可看见自己未提交的 history 修改并据此拒绝，不根据 pool 另开事务。完整内容、函数/trigger/列/ACL 与来访 LOGIN 的授权仍归后续独立 schema checker / maintenance permit，本函数不对这些作出承诺。

验收：专用 PG 随机 schema 的完整 latest 安装，RR 两种 mode 成功；旧 read-only 入口仍拒写事务；缺清单/未知/checksum/空 schema 拒绝；原事务未提交 sentinel 对别的连接不可见，核验后可继续 SQL 并回滚；同快照/新快照分歧符合 MVCC。nil、closed、已取消和非 RR 拒绝。测试不创建 epoch、不启动 main、不改运行角色或资金。

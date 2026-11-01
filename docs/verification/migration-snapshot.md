# Caller-owned RR 迁移清单核验

2026-11-01。依据[C0-S窄片合同](../../specs/022-self-service-payments/c0-snapshot-contract.md)，实现[migrations.VerifySnapshotTx](../../migrations/verify_snapshot.go)及[真实PG测试](../../migrations/verify_snapshot_test.go)。只核完整latest history；未实现C0生产lot、维护许可、proof v2、epoch或资金接线。

专用`127.0.0.1:55439/moni_test`随机schema实际执行：

```sh
TEST_DATABASE_URL=专用测试库 go test -race ./migrations -count=1
```

首次完整migrations专项3.231s通过；独立审阅指出错误正文与已取消context验证不足后，合同/实现统一固定`ErrVerifySnapshot`，补活跃事务预取消且仍能由调用方SELECT验证，最终完整专项3.005s通过。测试包含完整latest安装、RR READ ONLY/READ WRITE、原只读入口仍拒写事务、Read Committed/Serializable拒绝、未提交sentinel对其他连接不可见且可回滚、同RR/新RR可见清单分歧、自己改坏checksum、缺latest、unknown和缺history表拒绝。

helper仅用原tx的SELECT，不Begin/Commit/Rollback、写数据、取advisory或业务行锁。无效context在DB访问前拒绝，全部新入口失败固定安全错误，不返回history name或driver详情。既有VerifyReadOnlyTx未修改。执行期间数据库错误仍可能使调用方事务失败，恢复/回滚由调用方负责。

它不证明函数/trigger/列/ACL、LOGIN授权、全源或同事务资金图；这些仍是[C0完整维护候选](../../specs/022-self-service-payments/c0-maintenance-contract.md)的独立关口。未启动main或操作长期预览，未修改运行角色、资金或应用迁移；随机schema由finally清理。

# 017 实施计划

1. 固定五字段转换、历史兼容、独立版本、预算与有效期同步点，补充HLD/LLD。
2. identity/012迁移、server/API/HTTP、frontend三套配置状态机并行；root负责billing准入与到期复查和资金测试。
3. 专用PG/确定性状态机与锁等待验证，独立交叉复核并修复。
4. 前端测试/strict build、全Go真实PG race/vet/嵌入build；隔离程序浏览器验证编辑/拒绝/恢复/账单和移动布局。
5. 清理隔离资源，确认预览进程归属后排空，以owner迁移/provision和受限serve升级，核对ready与全部资源；更新验收与路线图。

2026-10-31：以上五步已在限定本地环境完成，完整证据见[017验收报告](../../docs/verification/key-configuration.md)。Docker/1Panel目标部署、正式供应商与性能等原门禁保持。

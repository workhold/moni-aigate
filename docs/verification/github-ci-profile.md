# GitHub 首轮 CI 与环境修订

2026-11-02。[首轮真实运行](https://github.com/workhold/moni-aigate/actions/runs/41334156647)失败，源码为197eb35；前端和架构通过，Go失败不能写成全CI成功。根和两名独立审阅者核对完整失败日志及源码，按[修订合同](ci-profile-contract.md)收口。[机器记录](github-ci-profile.json)保存日志SHA、源码SHA及实际结果。

## 修正及依据

- PB/HTTP/CLI fixture创建无密码LOGIN，CI镜像默认SCRAM报28P01；新临时PG显式测试trust。只为实际角色/权限实验，不冒称验证密码认证。
- 支付runtime要求实际SQL服务端127.0.0.1:55439，Docker bridge不满足；改Linux host network、PG仅监听loopback，并查询真实endpoint/DB就绪。产品拒绝规则保持。
- billing/server在各包600秒到期时，当前用例仅4秒/0秒；大量fixture全量迁移且共用401。将billing、identity、server与其它包置于四台独立runner/数据库，保留每包10分钟及完整race集合，互斥go-list校验覆盖42包（1/1/1/39）。不延长金融/SQL期限、不关闭持久化。
- 时间Location内部表示不再冒充预算差异；仍以Equal核原时刻并UTC副本严格比较所有字段。backend终止/pool.Close后以原期限独立观察精确PID/start/随机key或本次角色退出，不终止别人、不更改生产重试。

## 实际验证

相关operations两测试真实PG/race2.228秒通过；完整ledger reader专项7.903秒通过；额外TZ=UTC采购测试通过。工作流YAML解析、全部bash脚本语法、真实go-list全分区及embed占位检查通过。所有SQL及产品认证/预算未改；T1另有独立全包证据。

本机未执行Linux Docker环境；推送后的新远端CI仍需看实际运行结果，不能用本地测试或YAML解析替代。默认C0专项不因使用PG16镜像自动取得新catalog profile认证。

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

后续41339443048已实际运行并失败；本页保留首轮修订来源和专项范围，第二轮真实结果、动态顶层分区及数据库时区修订见[后续记录](github-ci-second-run.md)。

## B-P1 候选的后续测试接线

2026-11-03新增预算观察来源候选后，原十分区划分保持；仅在`other`的最终Go执行处通过`env`传入`PB_PREFIX_DATABASE_URL=$TEST_DATABASE_URL`，复用本job独立PG16/loopback/trust，测试各自创建随机schema与实际受限APP。原JSONL、pipefail、包/顶层完整分区校验及期限保持，其他九个分区与生产运行不安装候选。Homebrew专库已验，首次Linux CI按新提交另验；精确范围与后续结果见[本片记录](periodic-budget-prefix.md)，不改写本页历史失败或旧SHA。

## B-P4 纯身份材料的后续测试接线

2026-11-03按[独立合同](../../specs/023-periodic-products/b-procurement-identity-contract.md)在同一`other`命令级环境追加`PROCUREMENT_IDENTITY_DATABASE_URL=$TEST_DATABASE_URL`，原PB开关、十分区、期限、race与JSONL保持。测试仅在随机schema安装六个纯身份函数，以独立受限APP LOGIN与原Go ABI差分；没有完整采购writer、正式迁移或启动接线。namespace设计实验和其他C0专项不自动开启。实际远端须按新提交与JSONL另验，当前本机专项证据见[记录](procurement-identity-leaf.md)，本节不改写此前CI失败或原SHA的结果。

# GitHub T2 发布 CI：十组完整通过

2026-11-02，独立核验[运行41347278123](https://github.com/workhold/moni-aigate/actions/runs/41347278123)，精确源码 `46f25e01fa6522c6962321aa71718ee755c931c8`，分支 `codex/initial-mvp`，push attempt 1。运行于2026-11-01 20:31:23–20:49:03 UTC完成，十个 job 全部 success；只读 GitHub API 再查仍为 completed/success，未触发重跑。完整原日志 SHA-256 为 `1e3609a86f0cb3fccee583e08e33d932734ca075884d68bb94506283a007d285`。这份记录保留旧第三轮报告，**只覆盖上述已提交 SHA，不覆盖本轮尚未提交的 T3 ReadLease、两个维护 reader 或取消窗口修复**。

[机器证据](github-ci-authority-run.json)从已下载的十份 JSONL、各 artifact 的包/顶层分区文件、运行与 job 元数据以及原始日志重新核算。逐一检查每个启动父/子用例只有一次 run 和唯一终态、没有先终态后启动、没有悬挂或重复终态；包 start 与唯一包终态同样匹配。GitHub artifact API 的十个名称、所属 run/head、ID 与 archive digest 均已记录；文件清单逐项保存本地 SHA-256。API 的 archive digest 与本地解压后单文件 SHA 是两个不同对象，未声称重算了未保留的 ZIP 摘要。

42个包按 billing/identity/server/other 归属为1/1/1/39，四类互斥穷尽；十份完整包清单一致。billing 157个顶层按40/39/39/39分片，server 205个顶层按52/51/51/51分片；两类均独立核对 discovery、四份计划并集及实际顶层 run，无重漏。identity 57个顶层 PASS；other 649顶层 PASS、67顶层 SKIP。

| 组 | 顶层 PASS | 顶层 SKIP | 父子 PASS | 父子 SKIP |
|---|---:|---:|---:|---:|
| billing-0 | 40 | 0 | 98 | 0 |
| billing-1 | 39 | 0 | 94 | 0 |
| billing-2 | 38 | 1 | 110 | 1 |
| billing-3 | 39 | 0 | 97 | 0 |
| identity | 57 | 0 | 90 | 0 |
| other | 649 | 67 | 2602 | 107 |
| server-0 | 52 | 0 | 110 | 0 |
| server-1 | 50 | 1 | 90 | 1 |
| server-2 | 51 | 0 | 126 | 0 |
| server-3 | 51 | 0 | 138 | 0 |
| 合计 | 1066 | 69 | 3555 | 109 |

全部1135个顶层启动和3664个父子启动均有唯一 PASS/SKIP，零 FAIL；表中不把 `[no test files]` 的包级 SKIP混入用例计数。billing 的 helper process 与 server 容量 opt-in 各有一个合法顶层 SKIP；other 的107个父子 SKIP及源文件行号/理由完整保存在机器证据中，其中包括未开启的 C0 catalog/maintenance/candidate、描述符导出、真实 main/mail 生命周期专项。包 success 不能把包内这些 SKIP转写为专项通过。

前端286项全部 PASS，失败/取消/跳过均0；other job 的架构只读检查、`go vet ./...`、`go build -trimpath ./cmd/moni-aigate` 均 success。原日志确认架构检查针对该 SHA 的1181文件快照，摘要 `6dbccf9ebe60`。这些公共检查按 workflow 只在 other 运行，其他九组相应步骤的条件性 skipped 不表示失败，也不重复计为十次通过。

十组 PostgreSQL readiness 步骤均 success。对该 SHA 的 workflow 原文复核可见，readiness 只有 SQL CASE核对 loopback:55439、moni_test实际库/角色、Asia/Shanghai、max_connections=200及fsync/synchronous_commit/full_page_writes=on后才退出成功；因此这是临时专用 PG 环境证据。CI 使用 disposable trust fixture，不能据此声称生产密码认证通过。

本轮没有设置专用 `C0_CATALOG_DATABASE_URL`、`C0_MAINTENANCE_DATABASE_URL`、`C0_CANDIDATE_DATABASE_URL`，未进行对应 C0真实专项、真实 main/capacity、正式供应商、正式支付或生产部署验收。T2专库专项仍见[维护授权验证](maintenance-authority.md)，后续 T3须另以其精确代码版本及独立证据验收。

本次仅补已提交版本的验证记录，不修改实现、权限、迁移、部署或 CI 合同；统一架构由集成人复核并同步本记录的来源与能力状态。

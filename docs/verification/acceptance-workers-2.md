# 040 PR-D workers=2 候选与未通过验收记录

对应[040 设计](../../specs/040-acceptance-parallelism/spec.md)和[实施计划](../../specs/040-acceptance-parallelism/plan.md)。**当前只交付默认关闭的 workers=2 候选。没有任何一次完整 14 片/17 part 全绿并生成可复用 manifest，也没有完成两轮完整 029；不得把本页解释为 PR-D 验收完成、默认化或整单提速。**

## 已实现边界

- 两个 worker 按 PR-A 计划提交每片恰好一次，最多两个 Go race part、两个 PostgreSQL 和四个活动测试容器；结果按计划顺序合并，片间及 identity part 间 `fail-fast=false`，不重试、不拼单，不拆 `other`。
- 首个并发对在测试前完成真实双 cluster 证明：不同 `pg_system_identifier`、同名一次性 owner/grantee 与 default ACL 互不影响，A 持锁时 B 在同一会话取得并释放同值 advisory lock。探针前后逐字比较 `moni_test` default ACL 原文，避免污染业务测试初态。
- 每片使用独立 cluster/volume/netns、无 alternates 的独立 bare Git common 和 detached worktree；nested-worktree 测试可以写本片 Git 元数据，但不能修改主仓或其他片。每片只挂载自己的 worktree/Git common/home/tmp/GOPATH、只读共享模块缓存和同 attempt 的 Go build cache；`-count=1` 禁止测试结果复用。
- PostgreSQL/runner/cidfile/label/协调器所有权、PostgreSQL OOM 与运行终态、identity 清理不明时停止本片、host netns 探针、容器/volume/netns/private/worktree 清理均失败关闭。资源样本必须同时包含所属 PostgreSQL 和 Go runner 的非空记录。
- v2 schema 未改：候选 manifest 经 100 个 current-MVP 与 77 个矩阵唯一测试的合并检查后才原子改名；workers=2 还要求真实双 part 重叠、最多两个活动 part、identity 不重叠和 14 个数据库身份。
- `phase1-acceptance.py` 只有显式 `PHASE1_ACCEPTANCE_PARALLEL_WORKERS=2` 才启动本轮 runner，要求新的仓库外 `--artifacts` 与 `--output`，并校验 run_id、report/proof、manifest SHA-256、17 part、资源曲线和清理。未设置时保留 serial v1；外部 workers=2 manifest 同样必须提供 report，不能绕过证明。
- 独立 identity/mail real-main、支付、Compose、备份恢复、浏览器与 mock deployment 串行尾部未改。由于本轮 Go 分片没有全绿，这些完整 029 阶段没有被启动为 PR-D 验收证据。

## 专项测试

最终代码的专项测试包括：v1 5 项、v2 17 项、runner 28 项、协调器 5 项、029 Python 16 项、浏览器 Python 12 项，以及 41 IDs/82 bindings 矩阵检查。另完成：

- 真实空 part 双 cluster SQL 探针：`default_acl_independent=true`、`default_acl_restored=true`、`advisory_lock_independent=true`，临时 role/table/lock 清理成功。
- 真实 nested-worktree 预检：`scripts/acceptance-shard-plan` 6 个顶层 PASS、0 FAIL。
- 单独完整 billing-1、修正后的 billing-0/1 双片诊断均曾通过；这些只用于定位，不与失败整单拼接。

这些结果不能替代完整 14 片合并、完整 029、浏览器或生产验收。对 `e3b5600` 的独立聚焦复核未发现剩余 false-PASS、manifest 或清理实现阻断；该结论仅覆盖失败关闭合同，不是完整 workers=2 通过结论。

## 完整 workers=2 实跑与失败

serial 基线仍是同一 040 规格记录的 **2727.018 秒**（约 45 分 27 秒）。下列时间来自 runner report，不是设计估算；每次均使用新的 run_id、cluster/volume/worktree/artifact，失败时也没有可复用 manifest。

| run_id | 实现范围 | runner 墙钟（秒） | 结果与主要阻断 |
|---|---|---:|---|
| `1f85cd0005c644c188d897921db3000d` | 初版双 worker | 3511.480 | 探针 psql 会话错误；other 的只读主 Git common 使 nested-worktree 测试失败。 |
| `d201d8f782d84f458a38e899b4bb1bbe` | 独立 Git common、共享构建材料 | 2834.395 | 仅 default-ACL 断言文本错误；其余已执行测试通过，但 billing-0/1 未执行，不完整。 |
| `ad5a80b2a07b4ae295bc275dacdd12e6` | 首次逻辑源码路径实验 | 2560.024 | server-1 与 other 多项业务断言失败；不作为干净候选结果。 |
| `e11ecc7253604e94a5214bbdc9fe4e60` | 保留逻辑/物理双挂载 | 2921.235 | billing-0/1 与 other 失败，包括 vector reserve、PostgresLifecycle、payments。 |
| `4ec9d3ac549e47f982275c6deef3220c` | 干净探针、逻辑路径 | 3509.833 | server-0、other 失败；运行中编辑源码又触发 clean-HEAD 失败，整场无效。 |
| `ad38d2ebfc124cfabc7ff42e9a7676b3` | 恢复物理路径、干净探针 | 3287.923 | billing-1 与 other 失败；不是全绿或提速证据。 |
| `ec96f3c1016545a8bcc170936f392326` | 同上，VM 时钟同步 | 2656.807 | 只有 billing-0 的 `TestPostgresCashRefundLifecycleAndReplay` 失败；虽比 serial 少 70.211 秒，仍无 manifest，**不能宣称提速**。 |
| `b21ffe6f742e4c16bf89a46335fac502` | 最终候选 `e3b5600` | 3178.887 | server-0 `TestCostVectorApplicationQuoteChain` 与 other `TestPostgresAlertConcurrentCommandsAndWorkerSlots` 失败。 |

失败位置和类型在各轮间变化，包括 `cost_unknown`、钱包来源一致性、settlement/报价快照、任务提交竞争和告警并发。单片和双片诊断可以通过，但完整 workers=2 尚不可重复。VM 时钟同步也没有消除失败。没有删除测试、增加 SKIP、放宽业务/测试超时、关闭 fsync、提高 workers>=3 或拆 other。

各轮原始证据保留在 `/Users/vchive/.cache/moni-aigate-acceptance/<run_id>/evidence/`。最终一轮 report 的 `parallel_shards_seconds=2699.952`、`wall_seconds=3178.887`、`merge_seconds=0`、`manifest=null`，所以不能把更早的 44 分 17 秒或 42 分 40 秒失败运行当作最终候选结果。

## 资源与清理

- 所有实跑均保持 OOMKilled=false 的 runner/PG 终态检查；没有把 OOM、超时或资源错误当作通过。失败的业务断言仍逐项保留。
- 最终候选及此前诊断结束后均独立检查 run-labelled 容器、volume、协调器为空；runner report 的 containers/volume/netns 清理为 true，private 与 detached worktree/Git common 已删除。
- 没有启动完整 029 的串行尾部、桌面/390px 浏览器、支付、Compose、备份恢复或独立 identity/mail 验收，因为 Go 分片前置门禁未通过。
- 既有 `moni-pr3-exp-e5d6bc-*` / 18084 预览和 macOS 55439 串行尾部专库未停止、重启或修改。未使用 prune，未删除其他镜像、volume 或容器。

## 推广结论

`workers=2` 继续 **opt-in，默认 serial v1 不变**。在相同干净候选连续两次完整 029 均达到 41/41、0 FAIL、0 必需 SKIP、100/100 current-MVP 复用、完整桌面/390px 浏览器和独立生命周期证据之前，不进入 `parallel_default`，也不宣称完整回归墙钟已降低。后续需要基于这些失败进一步分析资源/时序敏感测试；不能用选择性重试或只重跑失败片拼出结果。

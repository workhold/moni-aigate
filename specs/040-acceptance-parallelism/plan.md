# 040 分阶段实施计划

本文只展开[040 设计](spec.md)的实施顺序，不表示任何阶段已经实现。每阶段独立 PR，后续阶段不得用局部结果冒充完整 029。

## PR-A：CI/本地共享分片器

**范围**

- 从 `.github/workflows/ci.yml` 提取可调用的 Go/CLI 分片实现，覆盖 go list、顶层 Test/Fuzz/Example 发现、C 序轮转、锚定 `-run` 和完整分区校验。
- 输出不可变 `shard-plan.json`：schema、commit/tree、14 个逻辑片、精确包/测试清单、命令和 digest。
- 新包自动进入 `other`；billing/server/attempt/identity 的父子测试不拆；identity 四份顺序执行。

**出口**：新 CI 14/14 与旧名单完全等价；缺包、重复包、重复测试、无效标识和无效 `-run` 负例通过。

**PR-A 当前实现**：`scripts/acceptance-shard-plan` 已提供共享 CLI、精确 argv/digest、原始 discovery 和 legacy artifact；`.github/workflows/ci.yml` 已改为调用该 CLI。独立 detached-worktree 等价测试、真实 attempt-0/identity-0 分片执行和静态检查已通过。PR-B/C/D 仍未实施。

## PR-B：v2 证据合并器

**范围**

- 扩展 `scripts/check-go-test-evidence.py`，保留 v1，新增 schema v2 索引和原始 shard JSONL 验证。
- 验证 14 片完整覆盖、77 个唯一测试到 82 个绑定、current-MVP 100 个唯一测试、SKIP multiset 和清理证明。
- 不接并行 runner；使用合成证据和只读测试验证 `verify-current-mvp.sh` 的 opt-in 兼容。

**出口**：缺片、跨 attempt、错误 digest/数据库身份、FAIL/SKIP、重复 PASS、损坏日志、错误清理和 v1/v2 混用全部失败关闭；039 standalone 行为不变。

**PR-B 当前实现**：`check-acceptance-evidence-v2.py` 提供 v2 manifest/plan/日志/cleanup 严格合并器，重新发现当前 HEAD 的计划和 other 测试，支持 identity 共享 cluster、跨包同名、完整终态与两轮真实 CI 共同确认的 211 项版本化 SKIP 基线；`phase1-acceptance.py` 提供显式 v2 的 77→82 矩阵路径，`verify-current-mvp.sh` 提供 `VERIFY_CURRENT_MVP_GO_EVIDENCE_V2` opt-in，v1 和 standalone 不变。真实源码合成证据覆盖 100 个 current-MVP 与 77→82 矩阵，但实际多库 runner 仍未实施，合成证据不代表并行验收通过。

## PR-C：容器 runner 与 workers=1 canary

**范围**

- 实现每片 PostgreSQL+Go runner 的一次性 cluster/volume/netns 生命周期、私有凭据、镜像摘要和清理证明。
- 保持片内 `127.0.0.1:55439/moni_test`、角色/ACL/锁隔离、原始日志、资源采样和 `fail-fast=false`。
- workers=1 验证工具链、真实 loopback 和零新增宿主监听，不用于双 cluster 互见证明，也不作为默认拓扑。

**出口**：单分片工具链和清理成立；workers=2 再证明同名角色/default ACL/advisory lock 互不影响。数据库身份全部不同，进程/DB/日志/清理失败均无可复用证据。

**PR-C 当前实现**：`acceptance-parallel-runner.py` 已提供固定 digest 的 PostgreSQL+Go 容器、每片独立 cluster/volume/netns 与 detached worktree、私有 env 文件、完整 run_id/cidfile/label 所有权、资源采样以及实际 SandboxKey 消失证明。PR-A 精确 argv、PR-B 原始日志/parser/211 项 SKIP 基线保持不变；workers 只接受 1，partial 不生成 manifest，完整 v2 manifest 仍受 14 片/17 part 与 100/77 合并门禁约束。`83eeb73` 的 `attempt-0` 真实 canary 已通过：6 PASS、12 个基线内 SKIP、0 FAIL、零新增宿主监听，容器/volume/netns/worktree/private 清理均独立核验；单片不生成可复用 manifest。`80ed12d` 又封闭 cidfile 空/半写启动竞态，运行中允许 pending、退出后仍严格校验，最终复审无剩余阻断。详见[验证记录](../../docs/verification/acceptance-container-runner.md)。workers=2、双 cluster 互见、完整 029 与默认化仍未实施。

## PR-D：workers=2 完整 029 与默认化

**范围**

- workers=2 通过显式 `PHASE1_ACCEPTANCE_PARALLEL_WORKERS=2` 接入 `phase1-acceptance.py`，与直接传入既有 manifest 的 `PHASE1_ACCEPTANCE_GO_EVIDENCE_V2` 互斥；未设置时仍是 serial v1。保留前端、独立 identity/mail real-main、支付、邮件、备份恢复、Compose、浏览器和 mock deployment 的原串行链；server 包内 actual-main 及其子进程只能使用本分片数据库/netns。
- 首个并发对的两个 PostgreSQL 同时存活后、任何测试 part 启动前，完成同名 role/default ACL/advisory lock 互见证明。A 撤销默认权限并删除同名 role 不得改变 B；A 持锁期间 B 必须能取得并释放同值 advisory lock。证明失败不阻断其他安全分片的诊断执行，但整单和 manifest 失败关闭。
- 协调器在专用 Linux Docker VM 的 host netns 内运行固定 Go 镜像，使用 VM 的 Docker CLI/socket；它只观察 VM 宿主监听，不能将 macOS 55439 的串行尾部专库误判为并行片资源，也不向测试容器暴露 Docker socket。协调器名称/cidfile 绑定新 run_id，`--rm` 和精确 ID 清理均须核验。
- 每轮使用新的 detached git worktree、run_id、artifact 目录和全套资源，保留 HEAD/tree/status 校验。每片先复制无 alternates 的独立 Git common，支持 nested-worktree 测试而不写主仓或其他片。PR-A `-list` 已产生的同 attempt build/module cache 可以共享以减少重复冷编译，但模块缓存只读、`-count=1` 禁止测试结果复用。集成模式必须显式提供仓库外 `--artifacts` 与 `--output`，禁止默认 artifact 路径或前一轮生成文档阻断下一轮。两轮间不做选择性复用。
- 同一干净候选连续两次完整 029，记录各片、双库证明、合并、串行尾部、整单墙钟和带时间戳的容器资源曲线。
- 独立复核后才默认 v2；serial v1 只作为下一场完整诊断/回退。

**出口**：连续两轮 41/41、0 FAIL、0 必需 SKIP、100/100 复用，无 OOM/超时/残留；浏览器与独立生命周期证据齐全，CI 和架构检查全绿。

**PR-D 去重补充**：第一轮完整 029 已 41/41 PASS，但 `wall_seconds=3554.532`，慢于 serial 基线 827.514 秒。runner 内部合并只使用预检生成的内存 `source_override=(plan,other_tests)`，逐字段匹配而不重复 `fresh_source`，并显式绑定本次 run ID。独立复核否定了仅以可变 report 证明测试已执行的浅层 attested 方案；修正后，phase1 不再单独运行矩阵合并，current-MVP 启动时对 100+77 必需唯一测试执行一次完整严格 v2 校验和一次 fresh source，随后同一 shell 内各组才可复用并报告实际 spec 数。phase1 只从绑定 run_id/commit/完整计数的原子批处理输出展开矩阵结果；report/proof 不能替代原始日志、freshness、干净源码或指定 spec 校验。standalone v2、外部无 report 的 workers=1 manifest 与 serial v1 保持原检查。详见[去重优化与整单墙钟](../../docs/verification/acceptance-workers-2-performance.md)。严格候选 `d9b55f0` 已连续两轮完整 029 达到 41/41，整单 2668.525/2636.994 秒，均低于 serial 基线；默认仍未改变。

**PR-D 当前实现与推广边界**：双 worker 调度、真实双 cluster 互见证明、独立 Git common、同 attempt 构建材料复用、严格 v2 调度/资源/清理验证和 029 显式集成已实现，默认仍为 serial v1。严格候选 `d9b55f0025bcfe806e5e420128858975cfaa3be6` 的 run `703d1b8ac30142b5b9c01d08a627ada1` 与 `0fd4416271584b3b8829a91c57f50902` 连续两轮完整 029 均为 41/41、0 FAIL、0 必需 SKIP、100/100 current-MVP 复用，浏览器与独立生命周期证据齐全，整单 2668.525/2636.994 秒，无 OOM、超时或残留。历史失败、VM 时钟根因、磁盘耗尽和已中止的浅层 attested attempt 均保留在[候选记录](../../docs/verification/acceptance-workers-2.md)、[VM 时钟分析](../../docs/verification/acceptance-workers-2-clock-analysis.md)与[去重性能记录](../../docs/verification/acceptance-workers-2-performance.md)，不得与成功轮次拼接。两轮成功只满足 workers=2 候选出口；默认化仍需独立复核真实证据与推广决策，serial v1 保留为下一场完整诊断/回退。

## 后续另行立项

- workers>=3、拆 `other`、并行真实 main/支付/邮件/Compose、跨机器调度。
- 这些工作必须依据 PR-D 的资源测量和新的 SDD，不在 040 首批承诺范围。

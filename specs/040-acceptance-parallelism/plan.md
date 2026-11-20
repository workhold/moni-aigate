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

- workers=2 接入 `phase1-acceptance.py`，保留前端、独立 identity/mail real-main、支付、邮件、备份恢复、Compose、浏览器和 mock deployment 的原串行链；server 包内 actual-main 及其子进程只能使用本分片数据库/netns。
- 每轮使用新的 detached git worktree、run_id、artifact 目录和全套资源，保留 HEAD/tree/status 校验，禁止默认 artifact 路径或前一轮生成文档阻断下一轮。两轮间不做选择性复用。
- 同一干净候选连续两次完整 029，记录各片、合并、串行尾部、整单墙钟和资源曲线。
- 独立复核后才默认 v2；serial v1 只作为下一场完整诊断/回退。

**出口**：连续两轮 41/41、0 FAIL、0 必需 SKIP、100/100 复用，无 OOM/超时/残留；浏览器与独立生命周期证据齐全，CI 和架构检查全绿。

## 后续另行立项

- workers>=3、拆 `other`、并行真实 main/支付/邮件/Compose、跨机器调度。
- 这些工作必须依据 PR-D 的资源测量和新的 SDD，不在 040 首批承诺范围。

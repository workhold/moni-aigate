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

## PR-C：容器 runner 与 workers=1 canary

**范围**

- 实现每片 PostgreSQL+Go runner 的一次性 cluster/volume/netns 生命周期、私有凭据、镜像摘要和清理证明。
- 保持片内 `127.0.0.1:55439/moni_test`、角色/ACL/锁隔离、原始日志、资源采样和 `fail-fast=false`。
- workers=1 验证工具链、真实 loopback 和零新增宿主监听，不用于双 cluster 互见证明，也不作为默认拓扑。

**出口**：单分片工具链和清理成立；workers=2 再证明同名角色/default ACL/advisory lock 互不影响。数据库身份全部不同，进程/DB/日志/清理失败均无可复用证据。

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

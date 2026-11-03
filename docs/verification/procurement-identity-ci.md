# 采购历史身份 SQL 叶子的精确提交 CI

2026-11-03；验收对象为已推送提交 `2223e40a2947846541e34451b6b2465082c18bbe`，分支 `codex/initial-mvp`。[CI 41594394743](https://github.com/workhold/moni-aigate/actions/runs/41594394743) 终态 `completed/success`，10/10 jobs 成功，07:09:00Z 创建、07:27:48Z 更新。本记录只描述该精确提交，不代表承载本记录的后续提交已经通过 CI。

## 独立核验方式与结果

读取 run 的准确 head SHA、全部 job 终态、十份 artifact 元数据及 `other` job 日志；下载十份 artifact，从其原始 `go test -json` JSONL 独立重算包、顶层测试及含子项结果。没有重启 workflow，也没有为本次记录重跑本机 PostgreSQL 或整仓测试。原始下载与分析保存在忽略目录 `.local/b-p4-identity-ci/`；准确 job ID、artifact ID、JSONL SHA-256、计数和证据摘要见[机器记录](procurement-identity-ci.json)。

| 检查 | 本次精确 CI 结果 |
| --- | --- |
| Go 包 | 56 包：52 PASS，4 无测试，无 FAIL |
| 顶层测试 | 1679 PASS / 114 SKIP / 0 FAIL |
| 包含子项 | 9471 PASS / 220 SKIP / 0 FAIL |
| 前端 | 286 PASS / 0 SKIP / 0 FAIL，TypeScript/Vite 构建成功 |
| Go 静态检查与构建 | `go vet ./...`、`go build -trimpath ./cmd/moni-aigate` 成功 |
| 只读架构检查 | 1648 文件，snapshot `79f2682b7a32`，成功 |

包集合与 artifact 保存的发现结果一致，billing/identity/server 各 1 包，other 53 包；billing 和 server 各按原四个顶层分区执行，另有 identity/other 两个 job。各 job 保持原 `-race -count=1 -timeout=10m`，billing/server 保留准确顶层筛选及其子项。`contracts (identity)` 是已有账号 identity 包的分区；本次新增 `procurementidentity` 由 `contracts (other)` 实际执行。

相对上一[精确 CI](github-readme-design-ci.json)的提交 `d93f6096b471bd365bf1708dfbda98b847f5ca7a`，仅对两个运行时钟产生的子项名称做已明确的归一后，原 9683 个测试结果逐项相同；新增 8 个结果全部是采购身份叶子的 PASS。该比较仅说明结果集合一致，不证明原有行为的所有故障已消除。

## 新 identity 的实际 PostgreSQL 范围

`other` job 在其独立 PostgreSQL 16 Alpine fixture 中显式提供 `PROCUREMENT_IDENTITY_DATABASE_URL=$TEST_DATABASE_URL`。原 [identity 测试](../../internal/procurementidentity/identity_postgres_test.go)确实执行，没有 SKIP：`TestPostgresOriginalProcurementIdentityABI` 及六个子项全部 PASS，另一个顶层 `TestFrozenIdentityMaterial` PASS，总计 2 顶层 / 8 含子项 PASS，包耗时 3.085 秒。

固定 SQL 材料 SHA-256 为 `05f4476b25158624ed08d1e6497ac41580bb3c50550ff7164f7ea08c8be3eacc`。实际测试使用随机独立 schema 和受限 APP LOGIN，以原 `billing.FinalizationInput/Usage`、Go `encoding/json` 与 SHA-256 为独立 oracle；验证原结构与 raw delivery、Unicode/转义、完整 int64、固定 seed 差分、逐参数 STRICT NULL、session 设置与非法 TEXT 域。函数属性、未授权 APP 的 42501、准确签名授权、无额外业务关系及退出零残留断言也随该测试执行。

这只验收[身份字节合同](../../specs/023-periodic-products/b-procurement-identity-contract.md)及纯 SQL 叶子。CI fixture 使用 trust；真实 LOGIN 不等于密码认证已经验证。六个纯函数没有采购业务 writer 或默认启动安装入口，不能据此宣布完整 D1、B-P3、B-P4、采购历史十一项缺证或权限攻击矩阵验收完成。[writer namespace 程序](testdata/procurement-writer-namespace/main.go)不进入普通包发现，本次 CI 未执行该设计诊断。

## 原 opt-in 与跳过边界

`other` 保留 `PB_PREFIX_DATABASE_URL`，原 PB_PREFIX PostgreSQL 范围 24 顶层 / 101 含子项全部 PASS；新增 identity 开关不扩大其他专项范围。CI 没有提供 `MONI_TEST_DATABASE_URL`、`C0_CATALOG_DATABASE_URL`、`C0_MAINTENANCE_DATABASE_URL` 或 `C0_CATALOG_TIMING_DIAGNOSTIC`。

原 19 个测试文件 / 67 个顶层的清单仍是原范围：原始顶层 58 PASS / 9 SKIP，其中 5 个 PASS 父项的 PostgreSQL 子项全部 SKIP，实际执行通过的顶层为 53 个。共 13 个 PostgreSQL 套件和 1 个诊断未获本次专项验收；14 项跳过详情及原因与上一 CI 完全一致。机器记录逐项保留五个父项及子项数，不能把父 PASS 转记为这些 PostgreSQL 能力通过。

## 本机整仓失败继续保留

先前[本机集成记录](procurement-identity-leaf.md#本轮整仓集成与未通过项)的 `make verify-mvp` **仍记失败，退出 2**：56 包中 50 PASS / 2 FAIL / 4 无测试，前端 286 PASS，新 identity 包 PASS；原日志是非 JSON，不能补造其完整逐测试 SKIP 计数。其准确日志 SHA-256 为 `834037fdac31e6d45bb49301940fba53581380ddd62aefe0532ecfb9e0108ace`。

本次远程对应结果单独记账：

| 本机原失败位置 | 本次远程结果 |
| --- | --- |
| `TestPostgresBindingIsolationSharesChannelLock/prepare_dispatch` 原得到 57014 statement timeout | 子项 PASS，2.41 秒；完整父项 PASS，4.82 秒 |
| `TestMVPPostgresActualMainAlertsDefaultOffAndCrashRecovery` 原临时库/角色清理超时 | 完整用例 PASS，13.96 秒 |
| `TestMVPPostgresActualMainSIGTERMDrainsSSEAndRestartDoesNotReplay` 原临时库/角色清理超时 | 完整用例 PASS，18.21 秒 |

这些远程 PASS 和先前定向复现 PASS 均不能替换本机整仓失败，也未确定其超时根因。本机 Go 失败后 Make 没有执行后续 vet/build；本次 CI 的独立成功步骤、先前 `make check` 与独立 build 各按自身运行记账。

## 证据与仍未覆盖的验收

机器记录保留全部十个 job、artifact 的 GitHub API 元数据、每份解包 JSONL 的独立 SHA-256 和分区统计，以及 run/artifact API 响应、日志、准确 workflow 与分析脚本的本地证据 SHA。artifact 的 `digest` 是 GitHub API 报告的 ZIP 摘要；本次工具下载后直接解包，没有独立复算 ZIP 文件摘要，不能与已复算的 JSONL SHA 混用。忽略目录原始文件不会随源码提交；远端 artifact 有保留期限，公开记录固定其身份与统计。

此次仅增加精确 CI 证据，不变更运行模块、请求链路、数据关系、权限或部署结构，未执行新的本机测试。统一架构应由集成人汇合本轮工作后更新验证状态并复核。正式供应商、支付、Docker/1Panel 目标环境、HA 和性能对标仍须各自验收；catalog 专项 race 时限、B-P3/R2 门禁及采购历史缺证继续保留。

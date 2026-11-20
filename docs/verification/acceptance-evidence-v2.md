# 040 PR-B v2 证据合并验证记录

对应[040 设计](../../specs/040-acceptance-parallelism/spec.md)与[实施计划](../../specs/040-acceptance-parallelism/plan.md)。本片只实现证据索引、合并器和显式接入，不启动多库 runner。

## 实现边界

- v2 使用 14 个逻辑 shard、17 个执行 part；identity 的四个 part 可以共享一个 cluster，其他任何 class 组合复用 system identifier 都拒绝，且与 manifest 顺序无关。
- manifest 固定 run/source/image 身份、精确 argv、日志摘要和清理证明。checker 在当前 HEAD 的干净 detached worktree 重新生成 PR-A 计划，并通过一次 JSON list 发现 other 各包测试；证据计划与当前源码不一致时拒绝。
- 原始日志必须有匹配的 run_id、精确 command、started_at、JSON events、completed_at 和最后的 `exit_status: 0`。截断、exit 后记录、跨 attempt 或窗口外时间戳均失败关闭。
- 每个发现测试必须恰有一个终态，每个 package 必须恰有一个 compile 终态。有发现测试的 package 必须 PASS；无测试文件包的 package-level SKIP 不算测试 SKIP。额外/跨片 PASS、重复终态、FAIL、必需 SKIP、错误清理和摘要均拒绝。
- `scripts/acceptance-skip-baseline.json` 是仓库内版本化基线，manifest 不能自行批准新 SKIP。真实 Go 终态没有 Reason 字段，因此只比较 package:Test 多重集，reason 固定为空，不宣称检测原因变化。
- `phase1-acceptance.py` 只有 `PHASE1_ACCEPTANCE_GO_EVIDENCE_V2` 才走 77 个唯一测试到 82 个绑定的 v2 路径；`verify-current-mvp.sh` 只有 `VERIFY_CURRENT_MVP_GO_EVIDENCE_V2` 才走 v2。v1 和 standalone 的 inventory/实跑不变。

## 专项测试

```bash
./scripts/phase1-acceptance.test.sh
```

该入口依次运行 v1/v2 证据测试、029/浏览器 Python 测试和冻结矩阵清单检查；CI 的 other 任务调用同一入口。

v2 测试覆盖合法完整证据、跨包同名、identity 共享，以及缺片、重复分配、跨片 PASS、错误 argv、计划外删测、identity/非 identity 数据库碰撞、缺 compile 终态、截断、exit 后事件、旧 run_id/时间、错误清理、必需 SKIP、自批准基线、重复 JSON key 和 malformed UTF-8/object 的受控 FAIL。

其中一个集成测试在真实 HEAD 上运行 PR-A 发现和 other 清单发现，再生成完整合成终态证据，验证：

- current-MVP 的 100 个唯一 `package:Test` 全部能从 17 个 part 精确复用；
- 029 矩阵包含 77 个唯一 Go 测试，v2 合并后展开为 82 个 ID 绑定，全部为 PASS。

该集成测试使用**真实源码清单和合成证据**，没有执行这 100/77 个测试，也不证明真实 PostgreSQL、多库 runner、workers=2、完整 029、真实供应商或生产验收通过。后续 PR-C 只能以本 v2 格式产出真实运行证据，并另行验证容器、cluster、netns 和清理生命周期。

## 本次本地验证

- `./scripts/phase1-acceptance.test.sh`：v1 证据 5 项、v2 证据 15 项、029 集成 13 项、浏览器 Python 12 项全部 OK；矩阵清单为 41 IDs、82 bindings、7 packages。
- `go test ./scripts/acceptance-shard-plan`：PASS，CI 分片合同未回归。
- `make check`：架构只读检查、采购布局、上游来源、Go vet 和前端严格构建全部 PASS。
- `go run ./scripts/check-architecture`：最终快照只读校验 PASS。

当前 `acceptance-skip-baseline.json` 固定 211 个非必需顶层 SKIP。来源是 PR-A 源码树 `ef1ea0feac66506956f08348e49229c74a6d83db` 的两轮成功 CI：[run 44338064958](https://github.com/workhold/moni-aigate/actions/runs/44338064958)（pull_request merge checkout）与 [run 44337906066](https://github.com/workhold/moni-aigate/actions/runs/44337906066)（push checkout，commit `a927404b9a887d3e98b5ef2afcfd968ca712de94`）。每轮内 14 个分片计划一致；两轮均有 2298 个唯一顶层 PASS、211 个唯一顶层 SKIP、0 FAIL，SKIP 集合 SHA-256 均为 `f3248696f23321f80bf72ece51f18caa0b6c521fe40b9fe0e6ca9182dc47482a`。这 211 项与 current-MVP/029 必需清单没有交集。

基线只适用于 CI-compatible 的本地 029 环境；PR-C 必须保留 procurement identity/attempt rules 的独立 DSN 和其他研究/诊断 opt-in 的默认关闭边界。环境、工具链或测试清单变化后必须重新审查真实输出，manifest 自行增加条目不能通过。对第二轮真实 Output 的逐项归类得到 26 类原因、0 个缺失原因，全部属于 subprocess helper、明确关闭的专用研究数据库、历史冻结夹具或诊断/导出/独立生命周期 opt-in；没有必需项或异常跳过。真实源码集成测试在合成证据中保留这 211 个非必需 SKIP，同时要求 100/77 必需项全部 PASS，不再用“所有发现测试都 PASS”的不真实夹具。

另一次只读适配验证直接使用 push run `44337906066` 的真实 Go JSON 事件：将 identity 的四个真实进程段拆开，只为 17 个 part 补上测试用 run_id/时间/数据库/清理索引，未生成或替换测试 PASS/SKIP。合并器接受 17 个 part、211 个真实 SKIP，并验证 current-MVP 100 与矩阵 77 的 152 个唯一必需绑定。该适配 manifest 仅存在于临时目录，资源与清理字段是合成的，因此只证明真实事件格式兼容，不是多库或完整 029 证据。

## 独立复审结论

首轮独立复审指出的 run_id 合并顺序和真实 SKIP 基线两个阻断均已修复。聚焦复审确认：合并前 run_id 顺序有静态回归；基线为 211 个排序唯一项，与 152 个唯一必需项零交集；真实源码测试保留 211 个非必需 SKIP；package 终态符合真实 Go 输出。复审结论为没有剩余具体阻断。复审者只读检查，未重复运行测试、下载 CI 产物或重放临时真实事件适配；这些执行结果以本记录中的本地验证为准。

本次没有运行完整 029、`make verify-mvp`、真实多库 runner 或 workers=2，也没有据此声明商业链路、真实供应商或生产验收完成。

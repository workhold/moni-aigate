# acceptance-shard-plan

`go run ./scripts/acceptance-shard-plan --emit-legacy <directory>` 在同一 commit/tree 上发现完整 Go 包和顶层测试，生成 14 个 CI 逻辑分片：

- `billing-0..3`、`server-0..3`、`attempt-0..3`：对应大包按 C 序轮转的四个分片；
- `identity`：同一逻辑片中的四个顺序测试进程；
- `other`：其余全部包，不加测试过滤。

输出包括 `shard-plan.json` 和 CI 继续使用的 `ci-packages-*.txt`、`ci-tests-*.txt`。生成器要求干净工作树、完整包覆盖、唯一测试、非空分片和合法标识符；新包自动进入 `other`，不会静默漏跑。

本工具只生成计划，不启动数据库、worker 或并行测试。CI 的实际执行环境、race、`-p 1`、`-count=1` 和原进程超时保持不变。多库 runner 属于 040 后续阶段。

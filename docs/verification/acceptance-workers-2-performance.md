# 040 PR-D 去重优化与整单墙钟

本文记录 workers=2 去重优化的合同与实测边界。第一轮完整 029 已 41/41 PASS，但整单明显慢于 serial 基线，因此不能把 runner 局部提速直接写成整单提速。

## 基线与第一轮完整 029

serial 基线仍为 `2727.018s`（约 45 分 27 秒）。

第一轮完整 029（commit `938f7b7112504efe5629ad24c73560bd73be2dd3`、run `34e47231db684cb3a84f1286525872ad`）：

- `overall_status=PASS`，41/41、0 FAIL、0 必需 SKIP。
- 整单 `wall_seconds=3554.532`（约 59 分 15 秒），比 serial 慢 827.514 秒。
- runner `wall_seconds=2293.869`；其中 14 片调度 `parallel_shards_seconds=2091.369`，runner 内部 v2 合并 `merge_seconds=147.683`。
- phase1 再次 v2 合并 `v2_merge_seconds=73.767`。
- current-MVP `683.849` 秒，独立 identity/mail `25.648/134.842` 秒，支付 `155.014` 秒，mock deployment `176.031` 秒，串行尾部 `1175.856` 秒。
- 浏览器 10/10、v2 manifest、双库证明、容器/volume/netns 清理与零新增监听均通过。

主要额外成本不是 workers=2 本身，而是 runner 合并后又在 phase1/current-MVP 多次重新发现同一源码计划。重复检查没有增加任何测试覆盖。

## 去重合同

1. **runner 内部合并只发现一次。** PR-A 预检已经生成同一 commit/tree 的完整 plan 和 other 清单。`merge_complete_evidence` 现在把这份计划作为 `source_override` 交给原 v2 `verify`，不再调用第二次 `fresh_source`。计划必须逐字段等于 evidence plan，other 测试清单、原始 JSONL、run_id、commit/tree/status、SKIP、数据库身份、并发窗口和清理仍按原合同校验；不一致即失败关闭。外部 standalone v2 仍调用 `fresh_source`。
2. **phase1 只复用 runner-attested 结果。** 有效 `runner-report.json` 必须满足 reusable、workers_2_proven、14 shard/17 part、run_id/commit/tree、manifest SHA-256、双库证明、零新增监听和清理。`verify_current_mvp_attested.py` 只验证这些绑定，不再重新执行 v2 `fresh_source`。`phase1-acceptance.py` 在该 attested 路径不再单独运行 `v2_merge_command`。
3. **current-MVP 不重新执行重复检查。** `verify-current-mvp.sh` 只有显式 `VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED=1` 和 `VERIFY_CURRENT_MVP_RUNNER_REPORT` 时才进入 attested 复用，逐组输出 `reused`；report 和 manifest 缺失、run_id/commit/tree/digest 不符或 proof 失败均拒绝。serial v1、standalone v2 和未提供 runner report 的外部 workers=1 manifest 保持原检查。
4. 不删除测试、增加 SKIP、放宽断言或业务/测试超时，不改 workers>=3，不拆 other，不关闭 fsync，不改变 serial v1 回退。去重只消除重复的源码发现，不消除任何测试或必需证据。

## 当前状态

去重代码、v2/runner/attestation 测试已完成；下一轮完整 029 尚未执行。第一轮 59 分 15 秒是优化前实测，不能用它宣称优化后的速度。只有新 run_id 的完整 workers=2 和连续两轮完整 029 才能证明真实整单收益。

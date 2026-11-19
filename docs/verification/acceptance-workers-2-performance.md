# 040 PR-D 去重优化与整单墙钟

本文记录 workers=2 去重优化的合同与实测边界。去重前第一轮完整 029 已 41/41 PASS，但整单明显慢于 serial 基线，因此不能把 runner 局部提速直接写成整单提速。严格批处理修正后的候选尚未完成连续两轮完整 029，当前没有优化后提速结论。

## 基线与去重前第一轮完整 029

serial 基线仍为 `2727.018s`（约 45 分 27 秒）。

去重前第一轮完整 029（commit `938f7b7112504efe5629ad24c73560bd73be2dd3`、run `34e47231db684cb3a84f1286525872ad`）：

- `overall_status=PASS`，41/41、0 FAIL、0 必需 SKIP。
- 整单 `wall_seconds=3554.532`（约 59 分 15 秒），比 serial 慢 827.514 秒。
- runner `wall_seconds=2293.869`；其中 14 片调度 `parallel_shards_seconds=2091.369`，runner 内部 v2 合并 `merge_seconds=147.683`。
- phase1 再次 v2 合并 `v2_merge_seconds=73.767`。
- current-MVP `683.849` 秒，独立 identity/mail `25.648/134.842` 秒，支付 `155.014` 秒，mock deployment `176.031` 秒，串行尾部 `1175.856` 秒。
- 浏览器 10/10、v2 manifest、双库证明、容器/volume/netns 清理与零新增监听均通过。

主要额外成本是 runner 合并后又在 phase1/current-MVP 多次重新发现同一源码计划。去重只能减少这些重复发现，不能减少任何测试或证据校验。

## 严格去重合同

1. **runner 内部合并只发现一次。** PR-A 预检已经生成同一 commit/tree 的完整 plan 和 other 清单。`merge_complete_evidence` 把这份内存计划作为 `source_override` 交给原 v2 `verify`，不再调用第二次 `fresh_source`。计划必须逐字段等于 evidence plan，other 测试清单、原始 JSONL、run_id、commit/tree/status、SKIP、数据库身份、并发窗口和清理仍按原合同校验。合并进程显式设置并恢复本次 `PHASE1_ACCEPTANCE_RUN_ID`；candidate 创建、discovery 解析或校验失败都删除候选文件。外部 standalone v2 仍调用 `fresh_source`。
2. **report 不是测试执行的替代品。** 独立复核确认，浅层检查 report/manifest 会把十七个空 shard 当成 PASS，且 shell 可能掩盖逐组 checker 的非零状态；该方案已废弃。有效 runner report/proof 仍要求 reusable、workers_2_proven、14 shard/17 part、run_id/commit/tree、manifest SHA-256、双库证明、零新增监听和清理，但这些只是附加绑定。
3. **current-MVP 一次严格批处理。** `verify-current-mvp.sh` 只有显式 `VERIFY_CURRENT_MVP_GO_EVIDENCE_V2_ATTESTED=1` 和 `VERIFY_CURRENT_MVP_RUNNER_REPORT` 时才进入批处理。`verify_current_mvp_attested.py` 对 100 个 current-MVP 与 77 个矩阵唯一测试的并集调用一次完整 v2 `verify` 和一次 `fresh_source`，保留干净源码、1 小时/未来 5 分钟新鲜度、全部原始日志、指定 spec、SKIP、数据库身份、并发与清理检查。批处理成功前没有任何 reused；失败直接终止 shell，后续状态或 printf 不得掩盖。成功后同一 shell 内各组按实际 spec 数输出 reused。
4. **phase1 绑定严格批处理输出。** phase1 不再单独运行 `v2_merge_command` 或浅层 attestation。批处理通过 `--output` 原子发布 run_id/commit/完整计数；phase1 验证该输出后才把 77 个唯一矩阵测试展开为 82 个 PASS 绑定，否则全部 MISSING。`go-race` suite 同时追加严格的批处理 command/log，外部 manifest 的空命令列表不得因 `all([])` 显示 PASS。没有可跨 attempt 或跨进程信任的 discovery 缓存，CLI 输入/输出的符号链接在 resolve 前拒绝。
5. 不删除测试、增加 SKIP、放宽断言或业务/测试超时，不改 workers>=3，不拆 other，不关闭 fsync，不改变 serial v1 回退。

## 已失败或失效的优化 attempt

- run `077323dda96e41eb901866ccf5768399`：旧专用 VM 的 `/var/lib/docker` 只剩 692MiB，PostgreSQL 明确报告 SQLSTATE 53100/57P03；runner 902.330 秒后 FAIL，无 manifest、无串行尾部。停止所属剩余测试后，所有 run-labelled 容器/卷/netns 均清理，零新增监听，已执行 part 均 OOMKilled=false。大量断言失败是磁盘耗尽的环境结果，不计为产品回归或性能数据。
- 为避免 Docker prune、删除其他镜像或触碰既有环境，新建 `colima-moni-acceptance-040`：4 CPU、8GiB RAM、60GiB 数据盘，原 `colima-moni-acceptance` 与 18084 预览不改。新 VM 仅停用重复的 `systemd-timesyncd` 并保留 lima-guestagent；558 个数据库样本 0 回拨，诊断容器/卷/netns 清理完成。
- run `2d88fa9e569a43799510527e2e52209d`：独立复核在运行期间确认浅层 attested 与 runner 合并 run ID 缺陷，候选立即失效并中止，不作为验收或性能证据。精确清理该 run 的容器、卷、SandboxKey、worktree/private 后，开始严格批处理修复。

## 当前状态

严格批处理实现及 runner 30、phase1 22、v2 18、协调器 5、浏览器 12 项专项测试已通过；独立复审对六项原问题及后续 suite/CLI 边界均无剩余可执行阻断，修正后候选的完整 029 仍待执行。第一轮 59 分 15 秒是去重前实测，磁盘失败轮和被中止轮都不能作为优化结果。只有同一干净候选的新 run_id 连续两轮完整 029 才能证明真实整单收益；workers=2 继续 opt-in。

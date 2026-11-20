# 040 PR-D 去重优化与整单墙钟

本文记录 workers=2 去重优化的合同与实测。严格候选 `d9b55f0025bcfe806e5e420128858975cfaa3be6` 已连续两轮完整 029 达到 41/41 PASS，且两轮整单都快于 serial 基线。**workers=2 仍为 opt-in；这里的通过结果不等于已切换默认，也不代表生产或供应商资格。**

## 基线与去重前完整 029

serial 基线为 `2727.018s`（约 45 分 27 秒）。

去重前完整 029（commit `938f7b7112504efe5629ad24c73560bd73be2dd3`、run `34e47231db684cb3a84f1286525872ad`）虽然 41/41 PASS，但整单 `3554.532s`，比 serial 慢 827.514 秒：runner `2293.869s`，phase1 重复 v2 合并 `73.767s`，current-MVP `683.849s`，串行尾部 `1175.856s`。主要额外成本是 runner 合并后又在 phase1/current-MVP 多次重新发现同一源码计划。

## 严格去重合同

1. **runner 内部合并只发现一次。** PR-A 预检已经生成同一 commit/tree 的完整 plan 和 other 清单。`merge_complete_evidence` 把这份内存计划作为 `source_override` 交给原 v2 `verify`，不再调用第二次 `fresh_source`。全部原始 JSONL、run_id、commit/tree/status、SKIP、数据库身份、并发窗口和清理仍按原合同校验。合并进程显式设置并恢复本次 `PHASE1_ACCEPTANCE_RUN_ID`；candidate 创建、discovery 解析或校验失败都删除候选文件。standalone v2 仍调用 `fresh_source`。
2. **report 不是测试执行的替代品。** 独立复核曾确认浅层 attested 会接受十七个空 shard，且 shell 可能掩盖逐组 checker 的非零状态；该方案已废弃。runner report/proof、manifest digest、双库证明与清理只是附加绑定。
3. **current-MVP 一次严格批处理。** `verify_current_mvp_attested.py` 对 100 个 current-MVP 与 77 个矩阵唯一测试的并集调用一次完整 v2 `verify` 和一次 `fresh_source`，保留干净源码、1 小时/未来 5 分钟新鲜度、全部原始日志、指定 spec、SKIP、数据库身份、并发与清理检查。批处理成功前没有任何 reused，失败直接终止 shell；成功后同一 shell 内各组按实际 spec 数输出 reused。
4. **phase1 绑定严格批处理输出。** 不再单独运行 `v2_merge_command` 或浅层 attestation。批处理原子发布 run_id/commit/完整计数，phase1 验证后才把 77 个唯一矩阵测试展开为 82 个 PASS 绑定，否则全部 MISSING。`go-race` suite 追加真实批处理 command/log，空命令列表不得显示 PASS；CLI 输入/输出符号链接在解引用前拒绝。没有跨 attempt 或跨进程信任的 discovery 缓存。
5. 不删除测试、增加 SKIP、放宽断言或业务/测试超时，不改 workers>=3，不拆 other，不关闭 fsync，不改变 serial v1 回退。

独立复审对六项原问题及后续 suite/CLI 边界均无剩余可执行阻断。专项测试：v1 5、v2 18、runner 30、协调器 5、phase1 22、浏览器 12，矩阵 41 IDs/82 bindings；`make check` 通过。

## 连续两轮完整 029 实测

两轮使用同一干净提交 `d9b55f0025bcfe806e5e420128858975cfaa3be6`，但 run_id、浏览器账号/schema、 detached worktree、14 个 cluster/volume/netns 和 artifact 全部独立。两轮 shard plan 逐字段相同，digest 均为 `71c76ca9b251c9a82f4a405173f946bda77457632153702c5794ad8e2a334414`。

| 阶段（秒） | 第一轮 `703d1b8ac30142b5b9c01d08a627ada1` | 第二轮 `0fd4416271584b3b8829a91c57f50902` |
|---|---:|---:|
| 整单墙钟 | **2668.525** | **2636.994** |
| 相对 serial `2727.018` | **-58.493（2.14%）** | **-90.024（3.30%）** |
| 相对去重前 `3554.532` | -886.007 | -917.538 |
| phase1 测得 runner 墙钟 | 2013.815 | 2006.858 |
| runner 并行分片阶段 | 1960.518 | 1954.029 |
| runner 内部合并 | 0.181 | 0.158 |
| current-MVP（含 preview stop） | 151.920 | 130.645 |
| identity 独立生命周期 | 26.813 | 25.208 |
| mail 独立生命周期 | 134.922 | 134.724 |
| 支付流程 | 154.686 | 154.554 |
| mock deployment | 176.231 | 176.141 |
| 串行尾部 | 645.066 | 621.705 |

`v2_merge_seconds=null` 表示 phase1 不再另跑一次合并；实际 runner 内部合并时间仍是上表的 0.181/0.158 秒。两轮 runner report 的墙钟分别为 2013.458/2006.538 秒，与 phase1 测量边界略有差异，不能混用字段。

两轮均满足：

- 41/41 PASS、0 FAIL、0 必需 SKIP，桌面与 390px 浏览器各 10/10；独立 identity/mail、支付、Compose、备份恢复与 mock deployment 全部 PASS。
- 2300 个唯一顶层 PASS、211 个基线内 SKIP、0 FAIL、无重复终态；两轮的完整终态 multiset 完全一致。
- 严格批处理 `verified_specs=152`、`current_mvp_reused=100`、`matrix_reused=77`；九组数量 `[2,10,21,14,7,17,5,13,11]` 合计 100，77 个矩阵唯一测试展开为 82 个绑定。
- 每轮 14 个不同 system identifier，跨 attempt 无任何复用；双 cluster 同名 role/default ACL/advisory lock 证明通过。
- `fail_fast=false`，无自动重试、无选择性拼单，manifest 可复用且 report/run_id/commit/tree/digest 绑定。

## 分片墙钟与资源曲线

下表峰值是该分片单个时间样本中 PostgreSQL+Go runner 的合计内存，不是同时刻全 VM 总和；原始时间戳、CPU、内存、PID、网络与块 IO 曲线保存在各轮 runner report。

| shard | 第一轮墙钟 s | 第二轮墙钟 s | 第一轮峰值 MiB | 第二轮峰值 MiB | 样本数 R1/R2 |
|---|---:|---:|---:|---:|---:|
| billing-0 | 235.029 | 248.692 | 509.6 | 509.9 | 54/56 |
| billing-1 | 182.535 | 192.526 | 499.9 | 515.2 | 41/42 |
| billing-2 | 155.093 | 167.080 | 501.3 | 486.8 | 37/39 |
| billing-3 | 145.545 | 151.107 | 474.0 | 496.8 | 34/35 |
| server-0 | 196.312 | 200.275 | 574.1 | 574.2 | 47/48 |
| server-1 | 190.470 | 193.823 | 804.1 | 795.8 | 46/46 |
| server-2 | 210.012 | 211.265 | 532.5 | 537.4 | 50/51 |
| server-3 | 154.426 | 153.635 | 641.5 | 634.8 | 37/36 |
| attempt-0 | 15.855 | 17.837 | 100.2 | 113.7 | 2/2 |
| attempt-1 | 18.093 | 17.979 | 130.6 | 160.1 | 2/2 |
| attempt-2 | 15.464 | 17.707 | 106.7 | 133.4 | 2/2 |
| attempt-3 | 15.220 | 18.014 | 148.8 | 139.0 | 2/2 |
| identity（四段合计） | 337.938 | 332.300 | 438.8 | 448.8 | 81/81 |
| other | 1185.879 | 1152.933 | 6129.7 | 5620.1 | 260/252 |

总计 695/694 个资源样本，所有 part `OOMKilled=false`，无超时、零新增宿主监听。两轮结束后 run-labelled 容器、volume、netns、worktree/private、预览容器和专用 schema 均已清理；新 VM 最终无容器/卷。既有 18084 预览与 macOS 55439 专库未被停止或修改。

## 环境与失败历史边界

- run `077323dda96e41eb901866ccf5768399`：旧 VM 磁盘耗尽，PostgreSQL SQLSTATE 53100/57P03，902.330 秒后 FAIL，无 manifest，不能作为业务或性能结果。未使用 Docker prune 或删除其他环境资源。
- 替代环境 `colima-moni-acceptance-040` 为 4 CPU、8GiB RAM、60GiB 数据盘；原 VM 不改。仅停用重复的 `systemd-timesyncd` 并保留 lima-guestagent，558 个数据库样本 0 回拨且诊断资源清理完成。
- run `2d88fa9e569a43799510527e2e52209d`：独立复核发现浅层 attested 与 runner run ID 缺陷后中止并精确清理，不计入连续通过轮次。

两轮原始证据分别在 `/Users/vchive/.cache/moni-aigate-acceptance/029-strict-d9b55f0-r1/` 与 `029-strict-d9b55f0-r2/`。推广默认仍需独立复核上述真实证据与默认化决策；当前默认 serial v1 不变。

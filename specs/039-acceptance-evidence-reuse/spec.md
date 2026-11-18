# 039：同次验收运行的 Go 测试证据复用

2026-11-18；状态：实施合同。目标是去掉 `phase1-acceptance.py` 外层全包 race 与 `verify-current-mvp.sh` 之间的重复执行，不改变任何测试、断言、数据库、权限或最终 41 项门禁。

## 1. 范围

外层 runner 已以 `-race -p 1 -count=1` 在专用 PostgreSQL 上执行完整 `./...`，current-MVP 随后又对同一批显式 current-only 测试逐项点名执行。复用只针对后者已经在外层 JSON 日志中取得严格 PASS 的 Go 测试，不扩展到 Compose、支付、身份、邮件、浏览器、前端、备份恢复或其他独立证据。

`verify-current-mvp.sh` 未提供证据清单时保持原行为，继续逐项 inventory 并执行测试。

## 2. 证据身份

外层 runner 在 go-race 命令结束后立即生成 manifest，至少包含：

- `schema_version=1`、`suite=go-race`、完整 40 位 HEAD commit；
- 本次执行的唯一 `PHASE1_ACCEPTANCE_RUN_ID` 和创建时刻；复用进程的同名环境变量必须一致，创建超过 1 小时或未来超过 5 分钟的证据拒绝；
- 专用数据库 `postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`；
- 同一暂存 `go-race.jsonl` 的 SHA-256；
- `-count=1` 所要求的单次运行事实。

current-MVP 只接受该 manifest，不接受任意旧日志路径。每次复用都重新计算日志摘要，拒绝被替换或后续修改的文件。manifest commit 必须等于当前 HEAD，checker 要求匹配的运行 ID，并拒绝除备份恢复组自身生成的 `docs/verification/backup-restore-local.json` 之外的工作树变更；该文件只是验收输出，不是测试源码。最终发布仍由外层同提交和镜像检查约束。数据库变更不能被 JSON 摘要证明，因此复用只允许在外层 runner 刚生成 manifest 的同一受控执行链中，不能把该文件当成跨时间通用凭证。

## 3. 逐项核验

`checked_go_race_test` 在证据模式下不再执行 `go test -list` 或 `go test`，改为调用严格 checker：

1. 规格中的每个 `package:TestName` 必须映射到精确顶层 Go JSON `Action=pass`；package PASS、子测试 PASS 或名称前缀不能替代。checker 只豁免 `run_logged` 的 command/started_at/completed_at/exit_status 框架和空行，其他非 JSON 内容、重复 JSON key、布尔 schema_version 和非零 exit_status 均拒绝。
2. 每个必需测试恰好一个 PASS；重复、缺失、FAIL、必需 SKIP 均为失败。
3. 日志中任何 package/test FAIL 都拒绝复用。非本清单的环境型 SKIP 不冒充必需通过，也不扩展 current-only 范围。
4. checker 输出复用数量、manifest digest 和 PASS，不写原始测试正文或秘密。
5. 失败必须使 current-MVP 和外层整单失败，不能因为其他组通过而掩盖。

第一片保留 compile-only 和全部非 Go 组；前端去重另行评估，避免一次混入过多变化。

## 4. 验收与负例

- 单元测试覆盖：有效证据、缺失测试、重复 PASS、必需 FAIL/SKIP、其他 FAIL、错误 manifest commit/database/digest/schema、陈旧日志和错误 spec。
- 外层 runner 测试确认 manifest 在 current-MVP 前生成并传入环境，未设置时 standalone 命令保持原样。
- 同一候选先比较去重前后必需 100 项集合，再运行完整 029；必须 41/41、0 FAIL、0 必需 SKIP。
- 对比 current-MVP 墙钟，记录实测节省，不预设固定提速值。禁止删除测试、放宽断言、增加 SKIP、缩短业务超时或降低 race/数据库要求。

## 5. 非目标

- 同库提高 `-p`、删除 race、复用跨提交/跨数据库旧结果。
- 用 package 汇总或旧 CI 结果替代本次 current-MVP 必需测试。
- 自然 lease、首事件超时、真实 main、Compose 或浏览器证据复用。

# 039 验收证据复用验证记录

对应[039 合同](../../specs/039-acceptance-evidence-reuse/spec.md)。本片只优化同一次 029 的测试编排，不扩大或缩小产品、供应商、支付或生产认证范围。

## 已实施的边界

- `phase1-acceptance.py` 在外层 go-race 后生成绑定唯一 run ID、创建时效、HEAD、专用数据库和日志 SHA-256 的 manifest；checker 要求匹配的运行环境，并只豁免备份恢复组自身生成的 `docs/verification/backup-restore-local.json`，其他源码/测试变更均拒绝。
- `verify-current-mvp.sh` 仅在 `VERIFY_CURRENT_MVP_GO_EVIDENCE` 存在时复用；standalone 保持原 inventory 和 `go test` 行为。
- checker 逐项验证精确顶层 Go PASS，拒绝缺失、重复、必需 FAIL/SKIP、任意其他 FAIL、错误 commit/database/digest/schema、被修改日志和非法 spec。
- 第一片不复用前端、Compose、支付、邮件、身份、备份恢复或浏览器结果，不缩短任何业务等待。

## 专项验证

- `python3 -I scripts/check_go_test_evidence_test.py`：checker 正反例和 manifest/log 边界。
- `python3 -I scripts/phase1_acceptance_test.py`：11 项，包含 manifest 接线、standalone 兼容和 100 个 current-MVP 必需测试从单一 race JSON 逐项复用。
- `bash -n scripts/verify-current-mvp.sh`：shell 语法。

## 首轮实测修正

首轮去重版 current-MVP 在备份恢复组之后失败：`backup-restore-drill.go` 按设计更新 `docs/verification/backup-restore-local.json`，一刀切 clean 检查误将其视为源码变更。checker 现只豁免这个明确的生成证据文件，其他 dirty path 仍失败；独立复核确认该 JSON 不是 Go 源码、测试数据或构建输入。首轮其他全局套件均通过，但不计为整单通过，必须在修正提交上重跑。

## 最终验收要求

1. 在同一候选提交运行完整 029，必须 41/41、0 FAIL、0 必需 SKIP。
2. 对比 current-MVP 日志，100 个必需测试均显示 `status=PASS` 和 `reused=<数量>`；不得以未执行 checker 的空输出冒充复用。
3. 记录 current-MVP 与整单墙钟，和历史同机结果比较；不预先承诺固定秒数。
4. 独立审查 checker 的安全性、current-only 清单穷尽性和失败路径；架构 reviewed refresh/check 与 CI 全绿。
5. 任一去重路径失败都整单重跑，不能选择性执行复用组来证明通过。

## 后续非目标

同库 `-p 4`、跨提交/数据库旧结果复用、自然 lease/首事件超时缩短、真实 main/Compose/浏览器结果复用，均需另行立项。

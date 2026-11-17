# 038-PR2 发布资格与证据门禁验证

状态：实现与专项验证已执行；真实供应商资格、生产可售和下游 commercial 不在范围内。最终完整 029 与同提交桌面/390px 结果须由最终候选证据确认，不用本页中途专项替代。

## 实施范围

- `current_011_release_qualification.sql` 追加证据、凭据覆盖、撤回、route release intent 和 binding 证据引用；历史 route 保持 test-only。
- catalog 提供登记、撤回、分页/详情读取，以及 direct commercial 发布与重激活的共同校验；deferred INSERT/UPDATE guard 阻止直接 SQL 绕过。
- admin HTTP 提供严格登记/撤回和只读管理查询；channels/catalog 使用同一个只读资格证据对话框。
- `third_party/provenance.json`、严格 checker、third_party 架构覆盖和 CI/current-MVP/029 实际执行已接线。

## 已执行的专项

以下命令均在当前工作分支执行，数据库测试显式使用 `postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`：

```sh
go test -race -count=1 -run '^(TestCurrentInstallerRequiresEmptyDatabaseAndVerifiesCompleteBaseline|TestEmbeddedPrefixIsExactClosedSet)' ./migrations -timeout 900s
```

结果：PASS。current_011 安装、完整 current-only 计划和嵌入闭集通过。

```sh
go test -race -count=1 -run '^(TestReleaseEvidence)' ./internal/catalog -timeout 900s
```

结果：PASS。覆盖 test-only 不变、无证据/mock 拒绝、direct 全凭据覆盖发布、错版本/范围、撤回、回滚、external commercial 拒绝、APP/projector 权限、直接 SQL UPDATE 与新建 model INSERT 防绕过、历史 stale 一致性、Go/SQL 仓库规则和分页读取。

```sh
go test -race -count=1 -run '^(TestReleaseEvidenceHTTP|TestExternalRelay)' ./internal/server -timeout 900s
```

结果：PASS。覆盖 admin 登记/读取/撤回、CSRF、恢复模式、finance 拒绝、commercial/test-only HTTP 发布、秘密 canary、零供应商 I/O，以及 035 外部 relay 非回归。

```sh
go test -race -count=1 ./scripts/check-architecture ./scripts/check-upstream-provenance
python3 -I scripts/phase1_acceptance_test.py
python3 -I scripts/phase1_browser_qa_test.py
npm --prefix web test
npm --prefix web run build
```

结果：PASS。覆盖 provenance 固定身份、SOURCE 漂移、目录闭集、reference/vendored inventory、精确父路径、大小写变体/重复/未知 JSON key、digest、symlink 和只读性；执行器自测、严格 DTO、防晚回和失败分页状态均通过。

在实现中途还执行过 catalog 与 server 全包真实 PostgreSQL/race，均通过；最终修改后的完整 `make verify-mvp` 已再次执行并以退出码 0 完成，全部 Go 包、前端、vet/build 通过。

## 首轮同提交浏览器与 029

提交 `fa6336b1414c1c40106eb5a9cd8ffc55a60ff7c0` 上，最终浏览器为 `PASS/completed=true`，10 个 ID 均覆盖桌面和 390px；source commit/tree 与不可变镜像 ID 一致。随后完整 029 为 **41/41 PASS、0 FAIL、0 必需 SKIP**，static/frontend/go-race/current-MVP/identity/mail/payment/mock-deployment 全部 PASS。逐项、原始日志和浏览器证据保存在 `.local/phase2-pr2-fa6336b/`。

该首轮 PASS 发生于侧栏收口之前。导航变更必须在新的提交上重新执行浏览器与完整 029，不能把首轮结果当作最终导航验收。

## 导航收口实施与检查

[导航子规格](../../specs/038-new-api-capability-migration/navigation-refinement.md)保持客户工作空间、权限和路由不变，平台侧栏按客户与权限、渠道与定价、资金与结算、运营与审计四组生成；当前组展开、当前项滚动定位、会话内偏好、独立导航滚动和移动抽屉键盘/焦点/滚动锁均已实现。

导航单测使用独立冻结的 21 条授权路径闭集，不以待测分组自证。前端全量测试、strict 构建和浏览器 harness 自测通过。独立实现与 UX 审查发现的断点滚动锁、同组路由/重开定位和测试闭集问题已修正，并新增同组跳转、390px↔桌面断点、焦点圈定/返回和重开定位的真实浏览器断言；最终结果待新提交复验。

## 后续 029 运行时诊断与修正

侧栏最终候选的完整 029 曾失败于 Compose task-submit 503 和支付 Recovery 重启，不能用较早提交的 PASS 覆盖。独立只读分析将 Compose 定位为 HTTP/worker 同时提交 prepared 任务的竞争；原实现把输家 `ErrNoResubmit` 误映射为 503。

现按[运行时修正合同](../../specs/038-new-api-capability-migration/release-evidence-gate.md#10-029-发现的运行时修正)改为原身份 `ReplaySubmit` 返回同一任务，不新增供应商调用。真实 PostgreSQL 竞争测试已通过：HTTP 202、worker 无业务错误或合法无工作、一个 submission_accepted 事件和一个 attempt；既有 unknown-submit 测试仍要求不重新提交，adapter 调用总数保持 1。

支付私有日志确认 Recovery 子进程在 `OpenProcessor` 退出，未把清理错误当主因。新增 `payments.SafeErrorCode` 封闭分类和主进程安全日志，不输出原始 cause/秘密。分类单测已覆盖 Guard、领域错误、context、SQLSTATE、网络和未知含秘密错误；真实 Recovery 复跑与最终整单结果仍待确认，不提前宣称通过。

## 环境根因与独立复测

支付分类曾捕获 `payment_deadline_exceeded`。只读资源核查发现专用测试库残留大量历史 meter fixture：约 245 个 schema、15 万 relation 和 5.5 万函数。核对其创建/清理代码并确认无其他 active 测试、无未授予锁后，仅对精确 `meter_gap/u/login/roles` 测试前缀按独立小事务清理，避免单事务超过锁表空间。清理后约为 71 个 schema、1 万 relation 和 6 千函数；其他业务/用户对象未纳入删除范围。

随后支付真实进程 QA 完整通过，包括 checkout、capture、唯一入账、Recovery、冻结查询重放、attested off、SIGTERM/SIGKILL、Guard-loss、自然 lease 恢复及八角色清理。Compose 业务 QA 也独立通过，覆盖修复后的 task submit。以上结果证明本次失败的环境与竞争根因，但最终 PR-2 仍须在新的最终提交上整单重跑 029；预存在的 `database_commit_reply_loss_no_wire_fault_facility` 显式 SKIP 没有被改写或计作必需通过。

## CI 旧断言收口

远端 server-2 在任务语义变更后精确发现旧断言仍期待 `ErrNoResubmit`。没有恢复 HTTP/worker 竞争产生的 503，也没有删除或跳过测试。`TestVideoTaskUnknownSubmissionReleasesMediaAndPreventsResubmit` 现在要求同身份 Submit 返回原 unknown 任务，并同时断言 settlement=1、submission_unknown 事件=1；与真实 worker/HTTP 竞争测试一起运行通过。随后须在包含该断言修正的新提交上重跑完整 029。

## 独立审查修正

三路代码审查及逐项反驳已汇总。修复包括：精确 `..` 与大小写 key 绕过、manifest/SOURCE 漂移、未登记组件、测试浅复制、model INSERT 门禁、Go/SQL 输入差异、登记历史证据 stale 不一致，以及失败分页提前修改游标历史。修正后相关专项均重跑通过。

## 明确边界

- 所有测试证据均为 fixture/deterministic mock，不授予 `supplier_profile_qualified`，更不是生产资格。
- New API/Sub2API commercial 在本片显式拒绝；没有推断下游账号池持续资格。
- test-only 不是网络隔离；真实部署的普通租户隔离仍需独立准入合同。
- 浏览器与完整 029 的最终状态必须在同一最终候选提交上确认，不能以本页专项提前宣称 PR-2 全部完成。

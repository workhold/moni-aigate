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

在实现中途还执行过 catalog 与 server 全包真实 PostgreSQL/race，均通过；最终修改后的完整结果由后续 `make verify-mvp` 重新确认，不沿用中途输出。

## 独立审查修正

三路代码审查及逐项反驳已汇总。修复包括：精确 `..` 与大小写 key 绕过、manifest/SOURCE 漂移、未登记组件、测试浅复制、model INSERT 门禁、Go/SQL 输入差异、登记历史证据 stale 不一致，以及失败分页提前修改游标历史。修正后相关专项均重跑通过。

## 明确边界

- 所有测试证据均为 fixture/deterministic mock，不授予 `supplier_profile_qualified`，更不是生产资格。
- New API/Sub2API commercial 在本片显式拒绝；没有推断下游账号池持续资格。
- test-only 不是网络隔离；真实部署的普通租户隔离仍需独立准入合同。
- 浏览器与完整 029 的最终状态必须在同一最终候选提交上确认，不能以本页专项提前宣称 PR-2 全部完成。

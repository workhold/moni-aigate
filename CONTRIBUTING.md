# 参与 moni-aigate 开发

项目仍在持续开发，能力与验收边界以 [README](README.md)、[统一架构](docs/architecture/README.md)和[交付计划](docs/design/delivery-plan.md)为准。提交前先阅读 [AGENTS.md](AGENTS.md)。源码发布许可证尚未决定，来源与复用边界见 [ADR-001](docs/decisions/ADR-001-source-strategy.md)。

## 报告问题与提出功能

请在 [GitHub Issues](https://github.com/workhold/moni-aigate/issues)中说明使用的提交版本、部署方式、预期行为、实际行为和最小复现步骤。涉及协议时补充接口、模型能力 profile 和经过脱敏的错误码或请求 ID。

不要在 Issue、PR、测试 fixture 或日志附件中提交真实密钥、密码、恢复码、数据库连接秘密、个人信息及模型请求/回复正文。上游请求可能产生费用，复现优先使用本地模拟器。

## 按 SDD 修改行为

1. 查阅对应 `specs/<编号>/` 的规格、合同和任务，以及 [HLD](docs/design/HLD.md) / [LLD](docs/design/LLD.md)。新增行为先补状态机、接口与数据合同、失败处理和验收范围，再修改实现。
2. 保持租户隔离、不可变发布、资金硬预占、唯一结算和未知消费不重放。恢复任务不得重新发送模型生成请求；数据库迁移只追加。
3. 为行为变化运行相关的有意义验证。说明实际执行的范围、未执行或跳过的内容；本地模拟结果不能替代供应商或生产环境验收。
4. 复核统一架构的模块、链路、数据关系、权限和能力状态，将受影响正文、设计和验证记录一起更新。并行任务汇合后再由集成人刷新架构清单。

## 本地环境

需要 Go ≥1.26、Node 22.12+（22 系列）、PostgreSQL 16、Git 和 Make。建库、角色分离和首次启动见[开发手册](docs/runbooks/development.md)，容器与 1Panel 见[部署手册](docs/runbooks/deployment.md)。没有固定默认管理员或测试密码；MFA 默认可选，个人已启用的 MFA 仍须验证。

`make build` 会安装前端锁定依赖、构建前端，并生成内嵌控制台的 `bin/moni-aigate`。单独运行前端测试、`make check` 或 `make verify-mvp` 前，先准备依赖：

```sh
npm --prefix web ci
```

## 架构同步与检查

每次代码、测试、依赖、迁移、部署、CI 或设计合同变化，都要复核[统一架构](docs/architecture/README.md)的模块图、请求链路、数据关系、权限边界、能力状态和源码链接。只改说明文字时，也要确认其能力声明和使用步骤与实现一致；无结构变化须在刷新理由中说明。

先运行与变更相关的 Go 包或前端验证，再更新受影响的架构正文与设计/验证记录。并行任务必须汇总全部结果，由集成人完成语义复核后，在仓库根目录执行：

```sh
go run ./scripts/check-architecture --refresh --reviewed --reason '具体变更及架构影响，或无结构变化的复核结论'
go run ./scripts/check-architecture
make check
```

`--reviewed` 表示已完成正文和源码的语义复核。清单用 SHA-256 检测增加、修改与删除，并检查本地链接；它不能代替审阅，也不能证明业务验收通过。默认检查及 CI 都不自动刷新，禁止为了通过检查盲目刷新或缩小覆盖范围。

`make check` 包含只读架构与候选采购列布局检查、Go vet 和前端构建，不运行完整测试套件。`make test` 运行 Go race 和前端测试，但未提供数据库配置时部分测试会跳过。

修改[采购字段布局合同](specs/023-periodic-products/b-procurement-schema-contract.md)时，先复核宏、列、类型与 generated 表达式，再显式运行 `go run ./scripts/check-procurement-layout --write` 更新候选 JSON，随后用 `make procurement-layout-check` 只读比对。该物料只覆盖新表列，不是安装 SQL 或完整 catalog/reader 注册表；不得用它替代数据库与权限验收。

## 商业链路验证

涉及商业链路时，按[专用测试库要求](docs/runbooks/development.md#开发验证)准备隔离 PostgreSQL 16 环境，通过私有环境提供 `TEST_DATABASE_URL`，再执行：

```sh
make verify-mvp
```

当前专库要求 `127.0.0.1:55439/moni_test`，测试 LOGIN 为 `moni_test`；完整权限与隔离要求以开发手册为准。测试会创建临时 schema 和角色，不要指向预览或生产数据库。

`make verify-mvp` 串行运行使用同一专库的 Go 包，并执行只读架构与候选采购列布局检查、前端测试/构建、Go race、vet 与构建。C0 等独立 opt-in 专项按各自合同另行运行，普通命令不会自动启用这些专项。记录精确提交、命令及 PASS / SKIP / 未运行结果；跳过的数据库测试不能计为商业验收通过，CI 绿色也不代表供应商、支付或部署认证完成。

## 提交 PR

PR 描述应说明具体问题、修改后的行为、相关 SDD、实际验证结果及仍存在的限制。范围小的修改保持简洁；涉及资金、权限或恢复时写清状态与故障边界。

提交源码时同时包含受影响的设计/验证记录、统一架构正文、`docs/architecture/changes.md` 和 `architecture-manifest.json`。不要提交 `.env`、构建产物、数据库文件或真实凭据。避免夹带无关格式化与重构，便于审阅和回滚。

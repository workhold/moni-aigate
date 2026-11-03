# B-P1：周期预算观察前缀候选验证

2026-11-03，基线 `83734076223ffe80d460dd255b45c4f9362e972f`。先冻结 [B-P1 合同](../../specs/023-periodic-products/b-prefix-contract.md)，再并行实现候选 SQL、独立纯核验与 actual APP 专库矩阵；根汇合并复核架构、并发语义及最终交付。模块与边界见[架构细图](../architecture/periodic-budget-prefix.md)。

候选纯层、专用 PostgreSQL 最终汇合专项及整仓默认验收已通过；推送后 CI 按精确提交另验。原始本地日志保存在忽略的 `.local/`，摘要与源码身份见[机器记录](periodic-budget-prefix.json)。

## 范围

候选在专库 `127.0.0.1:55439/moni_test`、LOGIN `moni_test` 下创建随机 schema 与独立 APP LOGIN，先核精确原 001–022，再追加候选 SQL/权限。DDL 与清理由 owner 执行；业务命令、资金与候选读取使用实际 APP，不用 SET ROLE 替代。

纯层保留原 Base 核验器和历史缺证，在新 scope 核 origin、source、观察链、精确资金 cut 与窗口出生次序。专库矩阵覆盖原 Commander、真实资金、旧 control、直接 SQL/不可变/权限反例、事务回滚，以及 RC 锁等待后的新鲜快照和单语句副作用。

测试读取器只用于候选语义验证，没有生产 catalog、预算或运行授权资格。旧迁移、旧 reader/basis/catalog/root、普通 main、Compose 与默认运行权限均保持；候选未接入 APP RuntimeProof，不标记 R1-B 或 R2 完成。

GitHub CI 在既有 `other` 分区的 Go 测试命令单独传入 `PB_PREFIX_DATABASE_URL=$TEST_DATABASE_URL`，复用该 job 独享的 PostgreSQL 16。候选仍由各测试在随机 schema 安装，原 JSONL 与 pipefail 保留；其他九个分区、生产启动和本地默认 `make verify-mvp` 不因此开启该专项。首次 Linux runner 结果按推送后的精确提交另验，不能提前沿用 Homebrew 结果。

## 最终专项结果

环境为 PostgreSQL 16.15 Homebrew、UTF8、C/C、Asia/Shanghai、max_connections=200，fsync/synchronous_commit/full_page_writes 均 on。其他发行版与目标部署环境未由本片认证。

| 验证 | 实际结果与边界 |
| --- | --- |
| 纯层最终 race | 1.765 秒，19 个顶层、227 个父子终态 PASS，0 FAIL/SKIP；含手算、字段变异、严格 JSON、资源/取消与 legacy 缺证保留 |
| snapshot fuzz | 子任务实际执行10秒，55,604次、包11.615秒，PASS；独立日志未保存，只保留工具输出来源，不将race JSON冒充fuzz日志 |
| actual APP 最终合组 race | 9.271 秒，15 个顶层、46 个父子终态 PASS，0 FAIL/SKIP；只选择新 `TestPBPrefix*`，不冒充旧全包验收 |
| 原业务与资金 | 三 scope 首配/no-op/changed/原回执；实际客户预占/结算/释放、供应商 finalization/释放与绝对成本更正；旧 control 继续业务且不补造 witness |
| 时间与并发 | 相同资金 cut 下两种窗口顺序、RETURNING/CTE 副作用、多行 INSERT；RC 语句早于另一事务提交，等待实际 blocker 后回执/窗口/cut 共同读取新值 |
| 跨日 carry | 三 scope 两笔旧日未决→换日 NULL 当前窗与 carry→独立新窗→旧笔结算/释放；18 条历史命令认证，旧窗收尾不改变新窗金额 |
| 权限与原子性 | actual APP 直接 SQL 拒绝、source 假指针被覆盖、origin 不能预造收编、projection 原权限保持拒绝、savepoint 与完整回滚 |
| 真实死锁 | 捕获 journal/head 后才形成真实锁环；40P01 一次后恰两次 BEGIN、仅一份来源；三次耗尽恰三次 BEGIN，全部事实回滚 |
| COMMIT 中断 | deferred trigger 内终止 backend，unknown、恰一次 BEGIN、无重发、连接销毁；只读原操作为 not_found，全部事实回滚 |
| 图形与文档 | 三份来源共23张 Mermaid 解析/渲染通过；已查看本片数据流图。README 11 个页内引用、1121 个本地文件目标和两份 Issue YAML 解析通过 |

根最终命令为 `PB_PREFIX_DATABASE_URL=<专用本地库> go test -json -race -p 1 ./internal/periodicbudget -run '^TestPBPrefix' -count=1 -timeout 180s` 和 `go test -race -json ./internal/periodicbudgetprefix -count=1`。12 份候选代码/测试/SQL/合同固定 SHA，后续整仓检查按这些源码执行。

COMMIT 故障在提交完成前终止，**不证明已成功提交但丢失成功回包**。本轮没有独立 40001 故障注入；部分错误 kind/跨 control SQL 同时违反 ordinal 条件，证明拒绝但不声称逐字段隔离的唯一约束 oracle。正式发布前仍需完成合同余下的安装、材料、读取 profile 与运行组合门禁。

## 初次失败与修订

- 首次合同组的 projection 反例用了 generated 列的 `INSERT SELECT *`，先返回428C9；仅改测试为 `DEFAULT VALUES` 后验证实际权限拒绝，保留原失败和后续日志。
- 根首次并发/语句组测试遗漏规范 target 的 `/periodic-budget` 后缀，被原 guard 正确拒绝；只补齐测试输入，第二轮及最终整组通过，原失败日志保留。
- 新 COMMIT 故障测试初次 Reader pepper 长度错误，修正为32字节后通过；不是修改生产 Reader 或降低验证。初次失败文件被同路径重跑覆盖，原失败仅保留在子任务工具输出，不重构原始日志。
- 纯层取消边界测试发现 `ctx.Err()` 双读可误分类，候选预算层改为单次读取，并在原 Base 返回错误后复核 caller。旧算法与源材料不改。

专项没有修改候选业务 SQL 来绕过测试；旧资金核验器、正式迁移和生产装配范围保持原状。

## GitHub 项目入口

README 补充快速导航、首次经营步骤、常用配置与供应商认证入口，精简重复维护内部细节，补齐原清单的ChatAPI/Moni-AI及旧New API地址归一，保留未验证来源、开发版、支付、部署、性能和许可证状态。新增缺陷/功能 Issue 表单与 PR 模板，配合已有 CONTRIBUTING 和 AGENTS；不选择尚未由用户决定的源码许可证。

这些协作入口没有运行时结构或权限影响；本轮代码与设计影响由统一架构模块节点、细图和能力状态同步表达。

## 整仓与交付复核

- `make check` 退出0，6.204秒：当时只读架构、Go vet与前端构建通过。其后仅追加CI候选执行环境与文档收口，末轮 `make check` 再次退出0、6.124秒，最终记录同步后再执行只读快照检查。
- 显式专库 `make verify-mvp` 退出0，1072.572秒：286项前端测试、前端构建、48个Go包race、vet与嵌入式构建通过，另4包无测试文件。默认未设置PB/C0等独立opt-in；候选实际PG验收以本页15个专项为准，不能将默认SKIP当成通过。
- 全程12份候选代码/测试/SQL/合同SHA未漂移。CI工作流只改变测试命令环境，不改本地Makefile、生产源码、旧资金核验器、迁移或权限材料；全部旧材料相对基线保持。
- 最终交付包含统一架构、细图、HLD/LLD、交付计划、开发手册、GitHub协作入口和验证记录；快照在汇合语义复核后刷新，默认检查及CI不自动刷新。

正式新profile、完整读取/运行组合、追加迁移与启动接线，C1/C2/C3、商品、媒体扩展、真实供应商、正式支付、容器/1Panel、生产/HA和性能对照均未由本片完成。远端代码、README字节和CI状态按推送后的精确提交核对，见GitHub Actions及本轮交付回复；本记录不预填自身提交SHA。

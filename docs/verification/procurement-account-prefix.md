# 采购账户命令、纯核基础与精确关联实验

2026-11-03；实施基线 `cff5bef966c990cfea5d85f0b4ea4ba164ae146e`。本片按 SDD 将三条工作线独立汇合，不关闭整个 D1、I1/A0、B-P3 或 R2，也不把隔离材料装配成正式迁移或线上权限。

## 实现范围

| 工作线 | 已交付 | 保留边界 |
| --- | --- | --- |
| [纯核 DTO 合同](../../specs/023-periodic-products/b-procurement-core-dto-contract.md) | [40 类原件](../../internal/procurementprefix/original_types.go)482 列、[32 类前缀](../../internal/procurementprefix/types.go)715 列；全 72 数组共享资源预检、标量资格和旧 23 数组 173 字段无损 Base 投影 | 没有导出 `Check`；四域重放、十一命题逐对象义务、最终 aggregate、scanner/catalog/runtime 尚未实现 |
| [账户 writer 合同](../../specs/023-periodic-products/b-procurement-account-writer-contract.md) | [独立材料包](../../internal/procurementaccountwriter/materials.go)、20 函数、20 普通及 9 deferred 触发器、两个真实 create/configure 根、精确账户列 ACL | 只认证新账户的命令历史；其他八类根、完整原 APP baseline、全域 validator、完整 writer/profile 及在线接线未完成 |
| [精确关联合同](../../specs/023-periodic-products/b-procurement-exact-link-contract.md) | 268 个 PK/UK 与 291 个 FK 的[设计清单](../../specs/023-periodic-products/b-procurement-exact-link-inventory-v1.json)、静态对应测试及 RC 双端锁/精确比较实验 | 原 685 项关系约束未替换；166 个复合键仍需宽度审查。实验不认证全域 RI、权限或业务根 |

常规安装仍是完整 001–022 迁移。未修改已应用迁移、原 constructor/main、预览进程、部署角色或任何客户与上游资金；本片没有模型网络调用、真实收款或供应商认证。

## 实际 PostgreSQL 账户专项

根串行执行 `go test -race -p 1 ./internal/procurementaccountwriter -count=1 -v`，三个实际 PG 顶层测试及材料单元测试全部通过，无 PG SKIP。

[测试](../../internal/procurementaccountwriter/account_postgres_test.go)只接受显式专用 `127.0.0.1:55439/moni_test`、`moni_test` owner LOGIN 与 PG16，无 fallback。每个顶层测试创建自己的随机 schema、actual APP/peer LOGIN 和 W NOLOGIN。业务调用由真正 APP 连接发起；owner `SET ROLE APP` 只用于身份冒充负例，不充当通过证据。测试证明角色和实际连接身份，不声称本机 HBA 已验证密码认证。

在随机 schema 安装原 001–022、B-P1、identity、关系材料，再安装本 writer 与列 ACL。原 revision、PB、payment 和 deferred 约束始终保留；不禁用 trigger，不以 owner 执行真实业务根。W 拥有新函数，不拥有表/schema；装配期间的 schema CREATE 权限在运行前撤销。

实际 APP 已通过以下范围：

- 创建、配置、同值配置、持久 `revision_conflict` 和 `not_found` 回执；not_found 无账户 journal，业务拒绝也保存准确来源与最后 seal root。
- create/configure 摘要逐字节对照独立 Go `encoding/json` 前像；create 快照对照原 Go `Account` ABI。新时间固定 UTC，同一时刻的旧时区表示按合同兼容，不声称 JSON 字节相同。
- 匹配重放返回原回执/root，不增加历史；actor 当前失权拒绝重放，同 operation 不同输入先于失权返回冲突。真正旧无证账户返回 PP906，不补历史。
- 真实双 APP 会话争用 operation advisory lock，等待提交后只返回原 replay；双配置争用账户行锁，等待后的新语句读取最新 revision，只有一项配置生效，另一项保存 CAS 拒绝。两组均以 `pg_blocking_pids` 确认实际阻塞。
- APP raw 配置、锁专用列的真实 UPDATE、私有 EXECUTE、SET writer、schema CREATE、source TRUNCATE、peer 调用和 owner+SET ROLE 身份冒充被拒；RR 调用返回协议错误。
- 同一顶层事务内两个独立 root，显式 `SET CONSTRAINTS ALL IMMEDIATE` 后 rollback，来源和效果一起消失。
- root 返回后撤销 APP 只读 validator EXECUTE，APP COMMIT 失败且原账户、command、root 全部回滚；测试以独立 owner 恢复准确 grant。
- 前端 statement/lock timeout 为 0 或大于合同值时拒绝；SQL NULL 必填输入拒绝且不留 root。没有函数内 SET 来延长已开始的前端计时。
- 原普通审计包含显式 `created_at` 的 APP 插入正例通过。ACL 保留该列及 payment guard 的最小 environment 读取依赖；这不替代完整 PB、绑定、观察和全部 APP baseline 的经营回归。

材料 SHA 在[包内固定常量](../../internal/procurementaccountwriter/materials.go)校验。渲染器对 schema/角色安全引用，先选不出现在绑定名中的 dollarquote，再一次替换；单元测试覆盖 `$$`、候选 delimiter 和包含模板字符的合法名称。安装参数只渲染物料，不认证实际 catalog 或发行运行许可。

PP906 明确表示未受证旧对象或尚未实现的历史域。声称 tracked 却缺来源、错绑定或部分 pointer 使用 PP902；非命令 journal 先核必要 identity 边再拒绝未实施域，不宣称该域完整通过。

完整故障矩阵仍待后续：其他八类根、全域双向坏来源/孤儿/额外来源、所有必要 grant 逐项撤销、锁超时与取消窗口、ordinal/revision 极限、未知 COMMIT 观察及完整 PB/资金接线。当前专项通过不等于 I1/A0 完成。

## 纯核与 Base 投影

`go test -race ./internal/procurementprefix -count=1` 通过，根最后独立复核为 2.163s，无数据库依赖。

测试逐列核对新 32 表布局及原 40 表 union，同时独立对照旧 V3 readset 与全部 110 扩列，避免新 JSON 与 DTO 互相自证。Base 投影使用原 scanner 的固定 23 类/列序作为独立 oracle，验证 173 字段及 nullable 深拷贝。

验证规范数值、UTF8/NUL、NULL/infinity/有限微秒时间、int32 无损边界、100000 行/16MiB 共享预算、65536/272144 字段界、取消与绝对 deadline。无法无损投影的原字段返回错误与零结果，不填零或截断；能够无损表示的业务矛盾留旧 Base 与未来域重放判断。没有调用方提交的 Base/Proof/Passed，也没有部分 `Check` 返回一致结论。

## 精确关联实验与原关系回归

`TestPostgresProcurementExactLinkRCExperiment` 使用独立 parent/child 测试关系及 actual 低权 LOGIN 的两个连接。它不安装生产 writer，不替换 685 项约束。六组场景全部通过：

1. 8192 字节长 TEXT、精确相等/单字节差异、NULL 与空串。
2. RR 下 tracked 父 UPDATE/DELETE、子 INSERT/UPDATE/DELETE 全拒绝，普通 legacy RR 写保持可用。
3. A→B→A 最终状态、双端一致更新、子单端错误、父删除后同定位键 legacy 重建拒绝、双端完整删除。
4. child 先持 SHARE，parent 实际等待，提交后反向检查拒绝错误修改。
5. parent 先改，child 实际等待，等待后仍提交旧 payload 被拒。
6. parent 先改，child 等待后的新语句读取最新匹配 payload 并提交。

两种竞争使用真实锁与 `pg_blocking_pids`，不以固定 sleep 代替阻塞观察。RR 限制是该候选原语成立的前提；不能将普通 deferred 比较器等同 native RI 的跨快照检测。

根串行执行整个 `procurementprefixschema` 包 race，2.773s 通过，无 PG SKIP。除上述实验外，覆盖清单与四份 SQL 的 SHA/键/列序/目标/反向关联、原 32 表 715 列（68 generated）、18 原表 110 扩列、268 键/126 CHECK/291 deferred FK 和 12 个命名声明边正负例。原 owner 声明边与 actual APP writer 的证据分别记录，不混为一项。

每个 PG fixture 使用独立清理 context，关闭本次连接池，精确删除随机 schema/角色并查询零残留。LOGIN 身份校验失败的 pool 也登记清理；身份冒充负例 reset 失败时销毁连接，避免污染 owner pool。

## 运行方式与其余边界

在独占专库上串行运行，连接串只在本机环境提供：

```sh
go test -race ./internal/procurementprefix -count=1
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -race -p 1 ./internal/procurementaccountwriter -count=1
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -race -p 1 ./internal/procurementprefixschema -count=1
```

未设置 `TEST_DATABASE_URL` 时数据库测试会明确 SKIP，不能据此标记账户商业链路或关联实验通过。

前置提交 `cff5bef` 的 [CI run 41616788662](https://github.com/workhold/moni-aigate/actions/runs/41616788662) 已只读核验 10/10 success；它只覆盖前置提交。本片新提交 CI 以对应精确 SHA 的远端结果为准，不继承该结论。

README 已核对本地构建、首次经营配置和 API 示例，补齐 001–022 与候选材料边界、Compose 本机 HTTP 试用、一次性任务状态/日志及 1Panel 容器环回网络前提。Docker/1Panel 实际运行、正式供应商、生产支付和性能对标仍未验，不把文档核对或数据库专项算作这些阶段交付。

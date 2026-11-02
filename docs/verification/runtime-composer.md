# C0-R1-B：独立 APP 完整资金运行核验

2026-11-03。基线提交 `1844c447d5b7d5ff0f0ac7ef2e6d685c3f435dc6`。本片先冻结[合同](../../specs/022-self-service-payments/c0-runtime-composer-contract.md)，随后并行实现纯 V1 接缝、APP 私有文件加载和同次原件投影，再集成 composer、静态兼容、history guard 与查询适配器。架构入口见[完整流程](../architecture/runtime-composer.md)。

当前状态：独立库实现、下列限定本地专项与最终整仓检查已完成；精确提交与推送后 CI 见 GitHub Actions 和本轮交付回复。完整经营认证仍受非空周期预算历史来源门禁限制，不能标记 R1-B 全部完成。

## 实现与授权边界

- `fundinglotruntime` 由实际 APP LOGIN 在调用方既有 RR READ ONLY 中核验完整当前 catalog/history、同次 84 表与八算法、持久 epoch/V2 和六条固定历史映射。没有接入 main、HTTP、billing、worker、projector 或 ready。
- sandbox 加载原真实 APP manifest，完整核验目录/文件、严格 JSON、nonce 与全部 signing key 材料；只保留私有不可变 V1 原件和 APP nonce 摘要，不保留秘密、keyring、manifest 原文或 FD。数据库 APP V1、本机 V1 与 V2 内嵌前驱必须完整逐字节相同。
- `expected` V2 只是明确比较值，不是文件来源认证。测试准备阶段由 owner 读取持久原件并与原 Plan.Expected 摘要核对；正式 V2 发行/持有/加载保留给 R2。
- 发行总窗口 30 秒，catalog 与后续窄身份检查各受 5 秒界；文件单独 5 秒。同时检查派生 context 和绝对时刻。proof 不保存已取消的工作 context，只对原活跃事务 wrapper、物理连接与事务身份有效。
- 本片没有生产迁移、GRANT/provision 接线或资金 writer。固定四列 SELECT 候选仅由隔离 fixture owner 安装。原 LegacyV1 API、旧 canonical golden、operations 权限检查与在线依赖门禁保持。

## 专用环境与测试来源

实际测试端点固定 `127.0.0.1:55439/moni_test`，准备 LOGIN 为 `moni_test`；环境为 PostgreSQL **16.15 Homebrew**、UTF8、C/C、Asia/Shanghai。普通 public 变体各自创建随机新数据库，绝不在父测试库 public schema 安装。其他 PostgreSQL 发行版、locale 或部署环境未由本片认证。

owner 只执行原迁移/固定候选、原商业 API、受保护 Plan/Init 和测试比较值准备。核验阶段使用新建 actual APP 连接及 RR RO，禁止用 owner、SET ROLE、维护 Permit 或 caller 自报 Facts 替代。genuine sandbox 使用原 QA installer 的三 schema、八角色、真实私有文件和原支付资金，不用自造 canonical 冒充真实安装。

## 验证矩阵

| 验证层 | 覆盖范围 | 限定结果 |
| --- | --- | --- |
| V1 / 旧路径差分 | 原 1786B golden/SHA、19 字段严格递归拒绝、旧合法边界、owned 状态与原私有 oracle | 纯层及旧 oracle race 通过 |
| APP 文件 | 真实 0700/0600、严格 JSON、1/128 keys、nonce/key 绑定、不复用、路径/大小/类型/权限、预算、FD 清理 | race 与最终 10 秒 fuzz 通过；错 owner 子项因无 chown 权限跳过 |
| 同 raw 投影 | 完整 84 表资格与深复制、ordinary 两空/sandbox 两单例、无额外 SQL、坏时间/资源/EOF/取消 | 纯层及 actual APP 专项通过 |
| 静态兼容 | 六组完整历史/当前 tuple、22 条迁移逐项 SHA、未知/交叉拼接拒绝 | 两项独立 Python 核对与 Go race 通过 |
| actual APP / 寿命 | 两轮 named/public、真实 sandbox 非空图；结束/换 wrapper/后续事务拒绝，窄检查不重扫资金 | 指定专项通过；组命令单项断言失败及重跑见下文 |
| 经营与重放 | 当前 basis 不同于历史、原生租户 genesis、拆分消费退款、129 租户跨 128 分页、各域坏历史拒绝 | 指定专项通过；活动 PB 仅验证缺证拒绝 |
| 采购成本 | 原 API 实际派发、逐 attempt 成本与客户结算、成本篡改拒绝 | 修正测试状态断言后定向 race 通过，不含 PB 活动历史 |
| 历史与权限 | 缺多/损坏/超界 history、原缺四列 profile、列撤权/表宽权/PUBLIC 权、LOGIN 与 RR RO 约束 | 指定 actual APP 专项通过 |
| 故障与清理 | 未读 Rows、Scan/EOF 故障、取消/断 backend、SQL 锁等待、fresh bounded cleanup 与 backend 消失 | 纯层及 actual APP 专项通过 |
| 图形 | 六份来源共 31 张 Mermaid 图，模块图与运行细图 | 全部解析渲染通过，根已查看两张 PNG |
| 整仓 / 交付 | 架构、前端、默认 Go race、vet、嵌入构建、精确远端与 CI | 本轮 make check / verify-mvp 通过；推送后按精确提交核对，CI 见 GitHub Actions 与交付回复 |

## 最终专项执行记录

下列计数区分顶层和父子终态，不将不同命令的重复覆盖相加为独立用例。原始日志保留于忽略的 `.local/`；精确命令、日志 SHA-256、实际顶层测试名和最终源码 SHA 见[机器记录](runtime-composer.json)。

| 日志 | 命令结果与耗时 | 顶层终态 | 父子终态 |
| --- | --- | --- | --- |
| `r1b-pure-final.json` | 三包 race 通过：proof 2.532 秒、scan 7.190 秒、runtime 22.355 秒 | 111 PASS、3 SKIP | 1381 PASS、4 SKIP、0 FAIL |
| `r1b-codec-final.json` | 两条原私有 codec oracle race 通过：payments 2.841 秒、maintenancetrust 25.071 秒 | 20 PASS | 1864 PASS、0 SKIP/FAIL |
| `r1b-sandbox.json` | genuine sandbox race 通过，包 355.421 秒、测试 335.980 秒 | 1 PASS | 63 PASS、0 SKIP/FAIL |
| `r1b-runtime-qualified.json` | **组命令退出 1**，包 370.183 秒；采购项断言失败，其余九项通过 | 9 PASS、1 FAIL | 53 PASS、1 FAIL、0 SKIP |
| `r1b-active-procurement-final.json` | 只修正测试状态断言后定向 race 通过，包 44.490 秒、测试 25.010 秒 | 1 PASS | 1 PASS、0 SKIP/FAIL |

纯层三个顶层 SKIP 是 scanner 的显式 PostgreSQL opt-in：`TestPostgresCompleteSourceReader`、`TestPostgresSourceWalletReaderNonemptyC0`、`TestPostgresLegacySeedProjection`；另一个子项为 `TestApplicationLoaderFileBoundaries/wrong_owner`，非特权环境无法更换 owner。这些跳过不计为通过，实际 APP 数据库专项另行记录。

组命令原始 `-run` 完整过滤式未保留，机器记录从日志列出十个实际顶层测试，不重构成“原命令”。采购项 verifier 已正确返回 `balanced`、complete 且 mismatch/unproven 全零，测试误期望 `consistent`。之后只把断言改为合同规定的 `balanced`，没有修改 verifier 或放宽金额/图验证。原失败日志保留，定向成功不把原组命令改写为通过。

采购验收沿原 API 执行双审入账 1000、采购账户与绑定、R120/U240、实际 dispatch permit、确认上游成本 24 与客户结算 12；actual APP 完整 composer 读取 84 表，在 5.13842175 秒内通过。随后更改成本必须拒绝。该用例没有周期预算活动，不能替代 PB 缺证修复。

最终 `FuzzApplicationManifestStrict` 实际执行 281184 次、208 个新 interesting，包 15.314 秒、退出 0；此命令未加 race，race 覆盖来自纯层命令。两个独立 Python 工具分别确认六个完整兼容 tuple/22 份迁移，以及两模式/六变体的精确四列权限 delta，均退出 0。

静态材料：兼容表 7374B / SHA-256 `5add8e6ffde8f792547aff8b205b0c34b6ea6ec13ac19f2ab823902e8a987f4b`；22 份迁移 manifest 为 2459B / `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`。原 V1 canonical 1786B / `1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c` 不变。读取范围仍为 84 表、776 唯一物理列，原 29 份 readset source pins 保持，仅追加新投影源 pin。

专项日志完成后，`runtime_materials.go` 与 `runtime_window.go` 仅执行最终 gofmt，PB gap 文档补充只读设计复核结论。机器记录保留这三项相对预先冻结 61 份库源码/材料清单的变化，并记录最终源码 SHA；整仓检查已针对最终格式化源码执行，不声称专项前后全部文件零漂移。

31 张图的完整来源/图像 SHA 和渲染报告嵌入机器记录。根已查看[系统模块图](assets/architecture-runtime-composer-application.png)与[运行细图](assets/architecture-runtime-composer-detail.png)；图形成功不替代业务或部署验收。

## 初次测试发现与修订

原始失败证据保留；修订没有降低 catalog、旧 negative、八算法或预算。

1. 查询适配器在驱动 Err 返回、参数复制和发布边界补充绝对截止复核，覆盖 `ctx.Err()==nil` 而绝对界已到的情形。
2. 既有 held 历史首次启用周期预算时，原算法保留历史来源 `Unproven`，Plan 因而拒绝。正例改为原 API 配置新空 scope；旧历史不足另保留拒绝，不伪造来源证明。
3. pgx 的 Next/fatal 自身可能关闭并消费结果；断 backend 时完整 rowset 也可能已在本地缓冲至 EOF。测试限制适配器额外关闭未完成结果，允许真正 EOF/Err/Close，不承诺驱动完全不 drain。
4. 客户端关闭与服务端 backend 消失不是同一瞬间。清理使用新的有界观察窗口确认原 backend 结束，closed 连接缓存 `TxStatus=T` 不被误认为可归还连接。
5. public fixture 以短生命 owner typed-protocol pool 在 C0 DDL 之前准备原业务，与原 snapshot fixture 一致；后续维护及 actual APP 保持 simple protocol。表级 REVOKE 恢复后按固定候选补齐四列权限。
6. 采购测试错误状态断言改为 `balanced` 后定向通过；原组命令仍记录退出 1。

## 最终整仓与交付检查

- `make check` 退出 0，7.035 秒：只读架构、Go vet、前端构建通过。
- 显式专库 `make verify-mvp` 退出 0，1089.860 秒：286 项前端测试、前端构建、47 个 Go 测试包 race、vet 与嵌入式构建通过；另有 4 个无测试包。`C0_MAINTENANCE_DATABASE_URL` 未设置，其他可选测试环境也未预置，默认包通过不替代本页单独记录的 C0 专项。
- 最终 73 份关键源码/合同 SHA 复核一致；整仓运行期间代码和测试均未改变。之后仅补本验证记录并重新同步架构清单，未修改被测代码。
- README、统一架构、HLD/LLD、交付计划与本验证页共 1052 个本地文件链接目标均存在；该统计不声称检查全部 Markdown 锚点。

精确命令、日志摘要及图形来源见[机器记录](runtime-composer.json)。推送后的当前提交、远端 README 字节与 CI 单独按实际提交核对，结果见 GitHub Actions 与交付回复；本版本化记录不预填尚未生成的自身 commit hash，也不沿用基线 CI。

## 非空周期预算门禁与未完成范围

[来源缺口](../../specs/022-self-service-payments/c0-runtime-pb-evidence-gap.md)依据现有代码和旧真实 PG 预期单独记录。任意非零 held 历史或配置周期 window 均可能留下 `OperationSnapshotPrefixUnproven`；合法链不因终态金额平衡便获得历史认证。空 control 正例及缺证拒绝不等于活动 PB 完整验收。下一片先冻结不可变 window creation cut、command snapshot 前缀及共同观察顺序，再实现并保留旧历史缺证边界；此前 R2 不得据本轮报告打开在线资金写入或 ready。

本片只覆盖独立 APP 组合库和实际执行的限定本地测试，不等于 C0 全部完成。R1 worker 窄证明、projector 认证，R2 正式迁移、V2 发行/持有/加载、维护 CLI、启动结束确认/ready/guard 和全部在线资金路径仍按[交付计划](../design/delivery-plan.md)继续。真实供应商、正式 PSP、容器实际运行、1Panel、HA 和竞品性能对照均未由本片验收。

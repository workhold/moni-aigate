# C0 完整 sandbox catalog：peer 验证记录

2026-11-01–02。对应[完整 sandbox 合同](../../specs/022-self-service-payments/c0-sandbox-catalog-contract.md)、[统一 checker 合同](../../specs/022-self-service-payments/c0-schema-check-contract.md)。本记录覆盖完整三 schema fixture、八角色/十组跨域负权、peer 元数据来源和模板位置审阅；最终正式 expected 与三公开 API 的集成验证由下方状态单独记录。

## 固定来源与真实安装

使用显式 `C0_CATALOG_DATABASE_URL` 的专用 PostgreSQL 16：TCP `127.0.0.1:55439/moni_test`。连接配置与实际 `moni_test` LOGIN、superuser、数据库 owner、server endpoint/major 均校验；使用随机 128-bit 前缀的三 schema、八角色，不改共享 public、长期预览或真实资金。

应用 schema 真实执行 001–022，再按旧 sandbox 实际顺序撤原函数 PUBLIC EXECUTE、授原 APP/projector、ProvisionFor、显著合成且仅用于 worker provision 的 marker、ProvisionPaymentWorkerFor、worker funding_uuid leaf，最后安装固定 C0 DDL/ACL。没有在 C0 之后重新运行宽授权。Mock/Receiver 由各自 NOLOGIN owner 安装原固定 SQL，仅按冻结 Permissions 矩阵授各自实际 LOGIN；无 peer 环境、key、业务历史 seed，不调用支付服务或收费供应商。

fixture 固定校验六项 peer 输入及 C0 DDL/ACL SHA。源码未更改旧 v1、离线 descriptor exporter 或已有原 SQL/ACL 信任根。

## 专库结果

- 三个独立 NOLOGIN owner、五个实际独立 runtime LOGIN。APP/worker/projector 的同 caller RR 角色与负权检查成功，snapshot 在调用前后相同；真实 APP 对 Mock/Receiver 业务表 SELECT 返回 SQLSTATE 42501。
- 十个跨域方向分别授 schema USAGE 都被拒绝。另逐项注入 schema CREATE/grant option、table 七类权限及 grant option、column SELECT/INSERT/UPDATE/REFERENCES 及 grant option、function EXECUTE/grant option、四类 membership/SET/ADMIN 边、runtime relation/function/namespace/type ownership、source-proof type USAGE 及角色 flags/limit/config/expiry，共 41 项负例均拒绝。拒绝后原事务仍可 SELECT 1；每项 rollback 后实际 APP 重新验证成功。
- ordinary APP/projector 对受保护 schema 以外新 schema 的 ownership 均拒绝；rollback 后 baseline 恢复。该检查是全局无 owner 路径边界，不能仅检查受保护 schema 的 owner。
- 补充数据库 CREATE/授权提升边界：ordinary APP/projector、sandbox 五 runtime 分别注入 DB CREATE、CREATE-GO、CONNECT-GO、TEMPORARY-GO，两个 mode 分别注入 PUBLIC CREATE，合计 30 个真实 PG 负例全部拒绝；所有 ACL 只在同 RR 未提交事务中改变。每项后 rollback，实际 LOGIN 新 RR 重验成功，原 PUBLIC 默认 CONNECT/TEMPORARY 仍为 true。两顶层专项 race PASS（fixture 段 0.60/1.49 秒；含 embedded roots 预热的进程 13.966 秒），没有向共享数据库提交授权。
- C0 的 16 个自动 row type 及 16 个依赖数组，对 projector 的有效 PUBLIC USAGE 为 true、grant option 为 false；named fl_source_proof 及其数组对 projector/Mock/Receiver 的有效 USAGE 均为 false。类型使用权不代表表读取权。
- 每次结束使用独立最多 10 秒 cleanup，关闭精确自有连接、核 PID/backend_start/database/application 后仅清理自有 backend，删除自有三 schema/八角色并确认不存在；测试通过包含该清理确认。

专项命令（公开 API 集成测试在正式 expected 冻结后另跑）：

```sh
C0_CATALOG_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
C0_CATALOG_CAPTURE_PEER=1 go test -race ./internal/fundinglotcatalog \
  -run '^TestCatalogPeerCaptureReviewMetadata$|^TestCatalogPeerBaselineAndActualLogins$|^TestCatalogPeerRejectsForeignPrivilegesMembershipAndOwnership$|^TestCatalogPeerRejectsInvalidPinRegistry$|^TestCatalogOrdinaryRuntimeOwnerOutsideProtectedSchemaRejected$' \
  -count=1 -v -timeout 180s
go vet ./internal/fundinglotcatalog
```

真实 race 结果 PASS，17.058 秒；vet PASS。其后仅移除临时诊断 wrapper，capture 使用普通 observer 接口。

## 三 schema 待审元数据与来源交叉检查

两个完全新建的随机实例产出完整候选均为 **13,533,178 bytes**，逐 byte 相同，SHA-256 `b6f8a72579c099e72f10e2cf846c24d0ede5036c67cfb03cd458d6e52166aba4`。第三次 race 生成内容相同。候选为三 schema 与八角色 envelope，运行时不会从测试输出/live 库加载它作为 expected。

| scope | 表 | 列 | type | 函数 | 全索引（含 TOAST） | 全 trigger（含内部 RI） | constraint |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| application | 125 | 1145 | 252 | 197 | 453 | 1331 | 1142 |
| mock | 16 | 145 | 32 | 27 | 50 | 105 | 166 |
| receiver | 6 | 42 | 12 | 10 | 18 | 35 | 46 |

Mock 的 50 索引包括 34 原业务索引与 16 TOAST 索引，105 trigger 包括 37 原用户 trigger 与 68 内部 RI；Receiver 对应 12+6、15+20。自动对象以其受审 owner 对象与固定角色规范化，不保留自建 raw OID/内部自动名。

独立复核候选有效权限矩阵：Mock runtime 为 schema USAGE、16 表 SELECT、13 表 INSERT、27 函数 EXECUTE、四张表合计 15 列 UPDATE；Receiver runtime 为 schema USAGE、6 表 SELECT、3 表 INSERT、10 函数 EXECUTE，无列 UPDATE。两者均无 schema CREATE、表级 UPDATE 或任何 grant option；全部其他保护角色在该 peer 的 schema/table/column/function 有效正权限项为 0。自动 type 的 PUBLIC USAGE 按前述例外另列，不归入数据访问权。

37 个完整 prosrc 从冻结 SQL 的原 `$$` 正文独立提取，与观察值逐 byte 相同，再各自计算 SHA 与旧 inventory 对照，全部相同。旧 inventory 仍只是来源交叉检查，不代替完整 descriptor。

43 个 SQL SchemaIdentifier 模板位置全部在 CHECK constraint expression：Mock 32，Receiver 11，application 0。token lexer 只识别紧接点的实际 schema 标识符，quoted literal 不改写；actual SQL 没有 ReplaceAll。模板按自有 scope 去除受审限定符后，106 个 Mock 与 26 个 Receiver CHECK 表达式与冻结 own-schema inventory 定义逐 byte 相同；无跨 schema token。

[完整位置审计](funding-lot-catalog-peer-template-positions.json)为 3,323 bytes，SHA-256 `533cfc6c8cb465f641e251e44ee57111f79d5e81bb0f51b43d2937e829c6810d`。两个随机实例该审计亦逐 byte 相同。字段之外的 literal、prosrc、type/role/reference 不套模板。

## 集成状态与验收边界

根已独立总装并嵌入完整 sandbox 正式 root，SHA-256 `a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f`。三公开 API 的非 race 专库测试通过：Snapshot、16 表空 Installation、实际 APP/worker/projector Runtime 及 Commit 后失效共 15.38 秒；同 RR 未提交的 peer 列、constraint、prosrc、新函数、列 ACL、函数 ACL、membership、owner 八项漂移均被公开 API 拒绝，每项 2.17–2.49 秒，原事务保持可用，合计 19.47 秒。完整命令进程 36.583 秒。该结果已走正式 embedded root，未用 live 期望替代。

首次正式 race 发行测试受原 5 秒总预算限制而拒绝（fixture 加首次 Snapshot 为 6.14 秒），没有放宽生产预算。负例测试已增加自身前后的实际 APP Snapshot 基线，避免早期身份或预算失败造成负例空转；该新基线在首次 race 中同样安全失败，未继续把负例当成功。当前由集成人完成 collector 优化后串行重跑整个正式 API/race 专项；在此之前不能把上述非 race 通过写成最终 race 验收。

仅测试用 `C0_CATALOG_PEER_TIMINGS=1 TestCatalogPeerReadTimings` 记录旧实现的只读 metadata 总时间 6.489 秒（race）：Query 至 Rows.Close 含逐条 JSON 解码为 2.920 秒，normalization/canonical/comparison 余量 3.569 秒；其中 dependencies 为 1.743 秒。该诊断直接调用不能发行 proof 的私有只读 collector，用于定位开销，不是放宽公开 API 的五秒预算。

本片不发行维护 Permit、资金 epoch、业务 RuntimeProof，也不代表支付供应商、生产维护/启动或真钱退款验收。架构与全部并行结果由根统一语义复核、刷新清单并完成最终只读检查。

根后续最终收口：保留上述失败记录，经同RR等值依赖/权限查询及完整expected比较去冗余后，最终整个leaf race退出0，217.140秒、176场景PASS/3独立opt-in SKIP/0 FAIL；本节完整sandbox公开发行21.91秒、八项peer drift含前后actual APP成功基线32.23秒，原每次五秒预算保留。源码、原始日志SHA与独立清理审计见[最终汇总](funding-lot-catalog.md)及[机器记录](funding-lot-catalog.json)。本轮全部flcp/flcv对象与backend归零，共享public和DB ACL未变；这些新证据不改写旧轮的失败/未验范围。

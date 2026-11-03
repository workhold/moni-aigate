# 采购历史身份 SQL 叶子与权限设计实验

2026-11-03；基线 `d93f6096b471bd365bf1708dfbda98b847f5ca7a`。先冻结[字节合同](../../specs/023-periodic-products/b-procurement-identity-contract.md)，再并行实施静态SQL材料及独立原Go ABI oracle。根汇合源码审查、实际专库测试和架构记录。完整B-P4经营能力、逐字段writer/来源DDL及新profile尚未实现，不以本叶子关闭采购历史缺证。

## 已实现及材料

[procurementidentity](../../internal/procurementidentity/materials.go)仅嵌入固定SQL模板，Version=1，SHA-256为`05f4476b25158624ed08d1e6497ac41580bb3c50550ff7164f7ea08c8be3eacc`。六函数负责Go JSON字符串、Usage、finalization/evidence前像及其摘要；STRICT/IMMUTABLE/PARALLEL SAFE/SECURITY INVOKER，无业务表查询、财务写入、安装器或角色授予，PUBLIC EXECUTE全部撤销。

函数与连接运算符均固定schema/pg_catalog限定。ObservedAt始终使用Go零值，raw stage/content在sticky前编码；空AttemptID保留空串。身份转换涵盖完整int64，不承担业务usage/UUID/金额资格校验。默认billing/迁移/main无import；`go list -deps ./cmd/moni-aigate`独立确认新包不在普通运行依赖中。

## 实际 PostgreSQL/race 差分

在指定`127.0.0.1:55439/moni_test`专库，由集成人串行执行，PG16.15。测试随机独立schema与受限APP LOGIN，无SET ROLE；仅安装这六函数，无业务关系。先验证未授权APP调用得到42501，再按六个准确签名授予测试调用权，核函数属性、PUBLIC无EXECUTE及无额外关系，退出时删除schema/角色并查询零残留。

最终命令：

```sh
# 从私有环境设置 PROCUREMENT_IDENTITY_DATABASE_URL，必须指向指定专库。
go test -race -json ./internal/procurementidentity -count=1 -timeout 120s
```

结果为2个顶层/8项含子项PASS，零SKIP/FAIL，包2.579秒。其中实际PG顶层0.79秒；另一个顶层独立固定材料SHA。首次专项1顶层/7项含子项PASS、包3.448秒，后续仅增强session设置下转义及非法文本SQLSTATE断言，最终再次执行全叶子包；首次结果保留，不转记为最终源码测试。

[独立测试](../../internal/procurementidentity/identity_postgres_test.go)用实际`billing.FinalizationInput/Usage`、标准`encoding/json`和Go SHA-256产生expected，不从SQL回填。覆盖768组固定seed Unicode/整数差分，参数顺序哨兵、raw stage/content组合、非零及超JSON日期ObservedAt先清零、全部PG可表示ASCII控制符、HTML/U+2028/U+2029与字面反斜杠u的区别、中文/emoji/组合字符、每个token位置的Min/MaxInt64、逐参数NULL，以及on/off标准字符串和无关search_path下的ASCII/转义前像。NUL和非法UTF8获得PG22021，属于TEXT域拒绝，不证明未来业务根显式校验已实现。

纯材料race测试独立PASS；新包和命名空间诊断程序`go vet`通过。准确日志SHA、源码SHA、包终态与边界保存在[机器记录](procurement-identity-leaf.json)。本轮按先行合同将现有CI的other分区命令级环境显式接入`PROCUREMENT_IDENTITY_DATABASE_URL=$TEST_DATABASE_URL`，复用该job独立PG与JSONL；分区、PB开关、期限保持。namespace诊断及其他C0专项未因此开启；推送后须核准确SHA与JSONL，不能以配置存在视为已通过。普通本机命令未提供该开关时仍SKIP，最终完整回归与此叶子专项分别计账。

## writer 命名空间的实际设计实验

[受控写入合同](../../specs/023-periodic-products/b-procurement-writer-authority-contract.md)选择独立NOLOGIN最小writer。原020 `pb_scope_sync`使用`current_schema()`生成namespace锁键，故definer的schema顺序影响新旧路径是否同步。

[可复现程序](testdata/procurement-writer-namespace/main.go)只提取并SHA固定原020的`pb_uuid/pb_require_read_committed/pb_scope_sync`，在随机schema和真实APP LOGIN/NOLOGIN writer下比较两函数。受信schema首位时，在另一连接持原函数的namespace排他锁后，APP在150毫秒收到精确55P03；pg_catalog首位的反例则返回pg_catalog并绕过同一锁。最终源码实际执行PASS，函数owner/definer与writer属性已核，成功结束清理确认零残留。

```sh
# 独立显式开关；只在指定专库运行，不进入普通 go test ./...。
go run ./docs/verification/testdata/procurement-writer-namespace
```

该程序要求私有环境`PROCUREMENT_WRITER_PROBE_DATABASE_URL`，严格限定同一专库，并为执行、事务与清理设置有界上下文。它只验证namespace决策，不覆盖完整root/source/role/default ACL攻击矩阵，原三helper的PUBLIC EXECUTE未在本实验撤销。清理复用原owner连接，若该连接失联则报告失败，不能保证重连清理；没有断连故障验收。本次实际成功清理不能外推失败注入已通过。

## 后续依赖与远端基线

账户和成本/request的[D1工作提案](../../specs/023-periodic-products/b-procurement-account-d1-proposal.md)及[来源提案](../../specs/023-periodic-products/b-procurement-cost-request-d1-proposal.md)已保留。共同root/typed source、完整DDL/权限/锁图、PB写闭包、独立纯重放与新完整profile仍待冻结实现。旧checker及V3材料不改签；B-P3采购缺证、catalog专项race和R2门禁保持。

上一文档交付提交的[CI 41585242075](https://github.com/workhold/moni-aigate/actions/runs/41585242075)已10/10成功，51测试包PASS、4无测试包，前端286项PASS。它属于`d93f609`，不属于本叶子；精确终态和原opt-in边界见[记录](github-readme-design-ci.json)。本轮最终整仓检查与推送后的CI须按本轮身份另记。

## 本轮整仓集成与未通过项

`make check`终态0：只读架构检查、全Go vet、TypeScript与Vite构建通过。独立`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate`终态0。开发手册17段Shell语法、CI YAML及7段run脚本语法通过，未执行部署示例。

在同一专库串行执行`make verify-mvp`，显式提供TEST_DATABASE_URL和PROCUREMENT_IDENTITY_DATABASE_URL；其他C0/PB_PREFIX专项未启用。结果**未通过，退出2**：前端286项PASS、无FAIL/SKIP；Go共56包，50包PASS、2包FAIL、4包无测试。此非JSON整仓日志不能给出完整逐测试SKIP计数，不把包PASS外推为所有opt-in已执行。新identity包在这次整仓执行中也PASS，3.751秒。

失败分别为：

- `TestPostgresBindingIsolationSharesChannelLock/prepare_dispatch`得到SQLSTATE57014 statement timeout，原期望binding_unavailable。0.80秒是Go报告的子例耗时，288.548秒是整billing包耗时；原业务锁/语句期限仍为3/5秒。现有日志没有精确SQL、后端和阻塞阶段，不能归因新identity或机器休眠。
- `TestMVPPostgresActualMainAlertsDefaultOffAndCrashRecovery`与`TestMVPPostgresActualMainSIGTERMDrainsSSEAndRestartDoesNotReplay`在临时数据库/角色清理时超时，server包514.855秒。不能将仅业务正文未报其他错误写成整个用例通过。

原billing/procurement/迁移/default main及go.mod/go.sum相对d93f609无变动，新identity没有进入普通main依赖；这不证明超时的具体原因。整仓Go失败后Make未执行其后的vet/build，前述make check与独立build分别计账，不补写成verify-mvp成功。

整仓终态后按本次PG错误DROP语句的时间和准确随机名称识别两个自有临时库，查询均为moni_test owner且零连接；逐个非FORCE删除，再删四个自有角色并查询零残留。清理仅作用于本轮两个失败fixture，没有按前缀批量处理其他资源。准确范围与日志SHA见[机器记录](procurement-identity-leaf.json)。

同DSN、原race/业务期限的隔离用例单独复现PASS：1顶层/3含子项，零SKIP/FAIL，包4.586秒；这不替换整仓失败。两个服务端原用例（含完整清理）定向复现也PASS：2顶层、零SKIP/FAIL、包33.259秒，原race及全部业务/context期限不变。两次定向复现均不能抹去整仓失败，也不能证明具体超时原因；没有放宽期限或重跑整个仓库掩盖失败。完整B-P3、catalog专项时限、十一项历史缺证及生产门禁继续保留。

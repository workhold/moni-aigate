# 采购更正、周期预算关系与账户提交故障验收

2026-11-04；基线 `e73d9e9`。承接[来源内容与效果](procurement-source-relations.md)，本轮先冻结[成本更正原件](../../specs/023-periodic-products/b-procurement-correction-original-relations-contract.md)、[PB操作与观察](../../specs/023-periodic-products/b-procurement-pb-operation-relations-contract.md)、[PB事件](../../specs/023-periodic-products/b-procurement-pb-event-relations-contract.md)及[账户故障矩阵](../../specs/023-periodic-products/b-procurement-account-writer-fault-matrix.md)。模块依赖与保留项见[架构细图](../architecture/procurement-replay.md)。

## 实施与关系边界

- [更正原件关系](../../internal/procurementprefix/source_closure_correction_relations.go)从两类明确cause核correction物理ID、联合全局operation、金额/basis/evidence及六pointer；原attempt绑定tenant/request，approval独立消费原proposal和birth。先登记已知槽，缺依赖或指定输出时仍拒绝同operation额外全NULL原件，不凭字符串前缀推managed。
- [PB配置与观察](../../internal/procurementprefix/source_closure_pb_operation_relations.go)独立核三scope原件、完整配置版本及末配置、真实changed/no-op、原审计内容、origin/head/journal和两类source双向槽；按ordinal/event_seq核完整观察链、cut与窗口出生先后。复用严格JSON解码但不借其他域报告或writer构expected。
- [PB事件关系](../../internal/procurementprefix/source_closure_pb_event_relations.go)按control seq独立重放allocation/window/control三层金额、revision及tail，从真实客户reserve/settlement和供应商PE核正反集合与当前投影；保留new/import/catchup、零delta、NUMERIC100和自然UTC日/月。legacy残余只证明数额相容，不推历史cut。

全部叶子共享原replayWork、context和绝对deadline，多值身份仅消费唯一原件，按实际物理表/自然PK计错；缺失对象不造假键，执行错误由完整入口返回零结果。Tenants/Users仅ID投影，不据此推历史角色或权限。来源仍Complete=false、Qualified为空，无公共Check。

## 独立复核与纯核验证

更正关系7顶层专项race PASS（1.618s），覆盖跨causeoperation冲突、物理主键多值、缺输出/依赖时的反向槽、basis-only、旧operation和32次数组乱序。PB配置与观察13顶层专项race PASS（1.766s），包括同audit物理ID不同target、全部业务索引多值、完整changed/no-op、同事务window_birth先于配置receipt、闰年2月/disabled/100位carry及共享排序取消。专项耗时仅归各自范围，不重复加总为最终整包。

根独立复核发现审计物理ID复用不能因target集合各唯一而消费；已补各原件遍历的PK门禁与持久反例。排序采用既有replaySort，比较/拷贝共用work并可中止。根新增真实私有入口测试，合法create/change/no-op/legacy全零finding，合法形状的错误audit精确标记原audit和operation；无事件控制也必须为零桶。这些不替代未实现的动态历史快照。

PB事件11顶层专项race PASS（1.752s）。独立审查以双tenant/key字面基线复现foreign-window污染：仅将tenant allocation指向Key窗口，旧guard会误推进另一scope的计数/tail；补同control唯一window门禁后64轮原反例通过。还补齐镜像seq唯一门禁、真实PE/settlement-journal错误对象归属及缺依赖不得认证聚合。合法catchup降到baseline以下、legacy未镜像零残余、new漏零delta原PE的拒绝均独立核验。根在最终文件冻结后原样运行固定外部overlay，三组race全部PASS（1.834s），对应持久反例也已进入仓库测试。

全部三叶子已接入同一次私有checkSourceClosure，根最终执行：

```sh
env -u TEST_DATABASE_URL go test -race -json -count=1 ./internal/procurementprefix
```

**205顶层/1053子例全部PASS，0 SKIP/FAIL，4.687s**；含真实私有入口的四类合法配置、错误audit与空event前缀投影。专项与外部overlay不重复计入整包。旧采购checker回归 `env -u TEST_DATABASE_URL go test -race -count=1 ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph'` PASS（1.566s），旧实现未修改。该纯核整包没有PG操作；真实专库证据按下节单独计账。

## 真实 PostgreSQL 账户矩阵

根串行使用显式专用 PostgreSQL 16.15 测试库，先独立只读确认 `127.0.0.1:55439/moni_test`、session/current user 均为 `moni_test`、服务版本160015。连接没有密码，不将其写成密码认证验收。fixture随机schema、actual APP/peer LOGIN、W NOLOGIN，保留原001–022、B-P1、identity、关系材料及全部trigger；owner仅安装、精确撤/还测试权限和独立观察。

[新增测试](../../internal/procurementaccountwriter/account_fault_postgres_test.go)包含：

- 203项有效权限逐项撤销：9个函数EXECUTE、八张全行读取表156列、step/聚合/outbox14列、其他24个前缀族root_id列。APP先完成合法create但不提交，再撤一项；有效权限true→false→恢复true，COMMIT保留原42501并全回滚。每项单独恢复，不扩权或重新provision。
- 每个子例对账户、命令、审计、root、step、source、origin、head、journal九表做确定性完整JSONB快照，前后逐值相等；失败operation的原command及typed source均不存在。只看root数量不计通过。
- 操作advisory lock、账户行锁各做实际3秒lock_timeout（55P03）和准确测试连接CancelRequest（57014）；先以pg_blocking_pids确认准确blocker。保留frontend statement_timeout=5s/lock_timeout=3s，不缩短协议，两个事务最终回滚且完整历史恢复baseline。

首次专项race实际 **2顶层/207子例PASS、0 SKIP/FAIL，9.922s**。自审去除重复快照断言并改进失权诊断后，根运行最终整个账户包：

```sh
go test -race -json -count=1 -timeout=180s ./internal/procurementaccountwriter
```

命令环境显式设置上述 `TEST_DATABASE_URL`；**7顶层/221子例全部PASS，0 SKIP/FAIL，11.351s**，包含已有成功、并发、权限与失败边界。随机schema/角色清理及零残留断言均执行。专项与整包结果不重复加总；耗时不代表网关性能。

## 保留门禁

账户候选仍仅create/configure，未接线上或完整profile；全部W必要DML撤权、坏/额外source、极限计数、未知COMMIT、其他根和完整APP/PB经营路径仍须完成。SourceClosure继续Complete=false、Qualified为空，无公共Check；PB命令动态金额前缀、业务历史absence与B-HO1、全部资格/逐对象闭包、完整Base与aggregate仍未完成。

32新表715列/68 generated、40原件482列、旧23数组173字段、001–022与旧profile不改签。此次专库故障证据不替代真实供应商、生产支付、容器/1Panel或生产性能验收，普通启动不安装本轮候选。

## 架构复核与仓库交付

根已复核统一架构的模块图、来源关系、执行路径、权限和能力边界，新增三个纯核模块及账户故障矩阵，在线请求、迁移、部署与权限无结构变更。README保留项目定位、使用/部署和能力状态，将内部候选细节收敛到架构细图/验证记录。独立文档复核的1281个本地相对链接目标均存在。

全代码冻结后，根刷新1791文件架构快照并通过只读检查；`make check`全部PASS，包括32表715列/68 generated布局、`go vet ./...`、Vue strict及Vite构建。前端完成后 `go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` PASS。行为代码不再修改；归档本段后再次刷新并只读核验。未运行本轮整仓`make verify-mvp`，所列专项、实际PG账户矩阵和旧checker回归分别记账，不代替完整商业/生产验收。

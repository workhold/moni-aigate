# 012 不可变凭据池验证

2026-10-31，工作树无提交SHA；[规格](../../specs/012-credential-pools/spec.md)、[计划](../../specs/012-credential-pools/plan.md)、[清单](../../specs/012-credential-pools/acceptance.md)。状态：限定范围本地验收通过，008迁移/最小角色后的最终预览就绪、资源匹配。

## 交付范围

管理员可维护一个渠道的1..32成员完整凭据池，按expected_version比较并创建新渠道版本，查看历史，独立永久撤销一个身份。元数据编辑保留成员身份；移出新池不会撤销旧路由。新池须显式重新发布路由才用于新准入。永久撤销覆盖历史版本，但无法撤回已经发出的请求，也不保证供应商处同一密钥全局失效。

渠道与成员分两次加权选择，渠道总容量及credential ID跨模型/版本共享容量同时生效，健康保持credential+model隔离。同渠道不换Key重试；只有可信无消费失败可切换下一渠道，每请求总计最多两次。每渠道仍只记录一条安全决策，实际credential_id独立保存在attempt中。资金预占、最终量、唯一结算/释放和未知消费不重放合同保持原样。

008新增不可变成员表、旧primary回填、primary/渠道/凭据复合约束，发现、推理attempt和平台probe均引用完整成员身份。池成员必须与渠道版本同事务创建，提交后也禁止追加；创建事务标记不能由调用者伪造。恢复后的事务编号碰撞及SAVEPOINT属于额外验证边界。应用角色可读/追加，不能修改/删除成员；projection角色无权限。具体恢复约束见[手册](../runbooks/backup-restore.md)。

Probe使用已发布池和同一Routing容量，记录实际选中身份，只代表该成员本次校验；发现按position取第一个未撤销成员，仅GET目录。二者均不动客户钱包。控制台支持排序、primary、成员参数、移出/新增/历史及单独确认永久撤销；秘密只存在本次编辑内存，保存成功/关闭/切换后清除，晚响应不能覆盖新选择。平台attempt、目录结果与探测结果展示实际身份。

## 直接证据

| 层 | 测试 | 主要判据 |
|---|---|---|
| Catalog/迁移 | [credential_pool_integration_test.go](../../internal/catalog/credential_pool_integration_test.go) | CAS/旧池冻结、元数据保留ID、32成员、非法/重复/跨渠道/历史同秘密拒绝、防撤销复活、primary撤销后secondary发布、全撤销拒发布、安全view、审计原子回滚、001–007旧历史升级008 |
| Routing | [pool_test.go](../../internal/routing/pool_test.go) | 池大小不增加渠道权重、成员权重、渠道/成员双容量、跨模型/版本并发、健康/半开隔离、同渠道不重试、panic/取消/旧代次释放、1000渠道/32成员与溢出边界 |
| 资金/派发PG | [credential_pool_integration_test.go](../../internal/billing/credential_pool_integration_test.go) | RecordAttempt冻结成员校验、失败batch排空及单连接复用、primary撤销后secondary派发、真实pg_blocking_pids观测实际次凭据锁及撤销后拒绝、旧预占revision不被新发布替换、原硬预占和零误收费 |
| 管理/客户HTTP+PG | [credential_pool_integration_test.go](../../internal/server/credential_pool_integration_test.go) | 精确JSON字段/重复及大小写别名拒绝、CAS/历史/秘密不回显、管理员/CSRF/再认证/恢复/ready/可选MFA；旧发布用旧池、实际主/次header与attempt一致、每次单笔收费、全撤销在资金准入前拒绝；缺量保留预占且不重放、可信拒绝只切下一渠道；发现实际secondary及其撤销stale |
| Probe HTTP+PG | [probe_pool_integration_test.go](../../internal/server/probe_pool_integration_test.go) | 单连接读取冻结完整池、实际secondary/audit、未发布变更及移出不替换旧池、主撤销后次成员、主失败不换Key、不污染次成员健康、共享双容量、次成员锁等待后撤销拒绝且零HTTP/记录/审计、钱包不变 |
| 最小角色/维护 | [credential_pool_integration_test.go](../../internal/operations/credential_pool_integration_test.go) | app追加/不可更新删除、owner不可变、projection拒绝、身份/1..32/字节/权重/并发/主成员约束、提交后禁止追加及防伪事务标记、四个当前/历史/撤销身份重包保持原ID/AAD/路由与撤销状态 |
| 前端 | [credential-pool.test.mjs](../../web/tests/credential-pool.test.mjs) | 安全view解析、完整CAS/边界、历史配置只读及单独撤销、排序/primary、秘密清理、渠道/版本/关闭和晚响应隔离；与其它前端契约一起回归 |

所有供应商HTTP均为本机mock，数据库为专用moni_test随机隔离schema/角色。没有付费供应商调用、评审账号/MFA变更、真实客户资金变更、提交/推送或性能压测。

复核已修正JSON输入歧义：Go的struct字段匹配忽略大小写，单纯按原字符串去重不能拦截`weight`与`WEIGHT`覆盖。现在仅接受精确canonical字段，额外覆盖顶层/member大小写及Unicode别名。未通过删除资金或撤权检查换取可用性。

## 可复现验证

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
go test -race ./internal/catalog ./internal/routing ./internal/billing ./internal/operations ./internal/server -run 'CredentialPool|TestPool|ProbePool|RewrapPreservesHistorical' -count=1 -timeout 120s
npm --prefix web test
npm --prefix web run build
go test -race ./... -count=1 -timeout 240s
go vet ./...
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
```

最终50项前端测试、strict TypeScript/Vite构建、全Go race、`go vet ./...`及最终dist的嵌入build均通过。全Go race：billing28.550s、catalog2.766s、config2.189s、identity44.663s、operations6.048s、provider5.481s、routing3.101s、security3.445s、server96.936s、telemetry4.142s；其余无测试包编译通过。秒数是测试包执行时间，不能当作响应延迟。定向资金批次/凭据池race回归与既有probe/检索probe回归亦通过，没有放宽原计费策略。

预览升级实际执行：确认58123旧进程属于本工作区→SIGTERM排空退出→新程序使用owner连接`migrate`→owner `provision`→受限应用角色`serve`。`GET /health/ready`返回200、ready且recovery_mode=false。首页与dist逐字节相同，SHA256=`cef391fae2c306a822ccc0c3f2a34dba5a5b2cedcbef76bae52c9cfe2cd08d7e`；全部27个assets文件亦与dist逐字节相同。

实际预览数据库只读检查确认008已应用、所有历史版本均有成员、三个封口/不可变触发器存在；app具SELECT/INSERT且无UPDATE/DELETE，projection无SELECT。未通过预览账号制造运营/资金数据来完成验证。浏览器访问渠道入口正常显示登录页并保留返回地址；本轮未登录评审账号或操作池表单。控制台通过契约测试、strict构建、独立静态复核及后端真实HTTP验证，不冒称浏览器表单端到端验收。

## 未执行边界

正式供应商全池认证、各Key的供应商侧额度/全局撤销、后台自动轮转/探测、批量渠道运营、自动目录同步、长时间多实例容量均未交付。本切片没有性能结论，007旧指标不代表新增池路径。001/002的生产门禁、真实供应商合同、容器/异地灾备、目标资源及竞品对比继续保留；前端契约与后端HTTP验证不等同登录评审账号后的浏览器端到端验收。

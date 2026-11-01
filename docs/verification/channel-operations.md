# 013 跨渠道批量启停与回执验证

2026-10-31，工作树无提交SHA；[规格](../../specs/013-channel-batch-operations/spec.md)、[计划](../../specs/013-channel-batch-operations/plan.md)、[清单](../../specs/013-channel-batch-operations/acceptance.md)。状态：限定范围本地验收通过，009迁移/最小角色后的最终预览就绪、资源匹配。

## 交付范围

管理员在渠道页勾选当前页最多50条，预检后一次批量启用或停用；API最多100条。所有目标都以current_version及字符串management_revision比较，任一阻断则整批不改，留下rejected持久回执。预检单SQL一致快照只读，不访问供应商、不产生审计/回执/资金写入。启用要求当前池有未撤销成员且渠道未隔离；停用允许零可用凭据并保持隔离。

启停是历史发布绑定的实时渠道门禁：停用阻止新准入和未派发尝试，重新启用可恢复原有route。它不创建渠道版本或替换凭据池，不解除渠道/binding隔离或永久撤销，不修改路由、售价、授权和钱包。已发上游无法撤回。新池仍须按012显式重新发布后用于新准入。

009迁移给所有渠道UPDATE增加单调管理revision，INSERT强制1、UPDATE强制old+1，检测状态ABA及原单渠道入口的并发变化；原入口未因此变为CAS编辑。完整items与操作摘要单行封口，不可更新/删除；数据库严格验证固定JSON结构、1..100、256KiB、状态与计数、before/after及revision。app仅SELECT/INSERT，projection无权限，owner普通SQL仍受不可变触发器约束。

操作ID永久幂等，同actor与规范输入（目标次序无关）返回原决定，异内容/actor为409。状态变更、完整回执及唯一审计同事务。HTTP200还可能是rejected；网络/5xx/非法响应只查原ID，GET404不证明另一事务没有执行。关闭或切页后当前账号内存保留提交ID；浏览器刷新后可从历史或复制的ID找回。恢复到旧备份不承诺跨恢复epoch去重，不能自动重播旧预检/未知命令。

## 直接证据

| 层 | 测试 | 主要判据 |
|---|---|---|
| Catalog/迁移 | [channel_operations_integration_test.go](../../internal/catalog/channel_operations_integration_test.go) | 8主测：预检零写/旧绑定计数、全部应用/no-op、重排重放与actor/摘要冲突、整批拒绝及历史、启用门禁/ABA、同ID并发唯一执行与反序目标锁、实际channel/credential锁等待后新快照、审计/actor失败全回滚和单连接、100项边界 |
| 最小角色/约束 | [channel_operations_integration_test.go](../../internal/operations/channel_operations_integration_test.go) | app可读/追加且不可改删、owner不可变、projection拒绝、revision防伪、40余种非法字段/状态/计数/边界组合、合法100项与101拒绝、审计失败全回滚且原ID可重试 |
| 资金/派发PG | [channel_operations_integration_test.go](../../internal/billing/channel_operations_integration_test.go) | 真实pg_blocking_pids观测批停用与PrepareDispatch竞争、批启用不清隔离、当前池可用不复活旧冻结路由的已撤销身份、钱包及资金事实保持 |
| 管理/客户HTTP+PG | [channel_operations_integration_test.go](../../internal/server/channel_operations_integration_test.go) | 4主测：严格字段/重复大小写/数值revision/边界、角色/CSRF/再认证/可选MFA/ready/恢复、原子停启/旧CAS/同ID/历史/审计、混合quarantine/revoked/version/missing拒绝；实际客户停用零POST/扣费，启用原route/pool唯一收费，50+1安全分页 |
| 前端 | [channel-operations.test.mjs](../../web/tests/channel-operations.test.mjs) | 当前页字符串revision目标与冻结命令、预检与拒绝回执解析、未知结果/原ID只读查询/actor隔离、晚回与选择世代、严格安全投影及历史 |

数据库为专用moni_test的随机隔离schema/角色，供应商为本机HTTP mock。未访问付费供应商、修改评审账号/MFA或真实客户资金，未提交/推送、做性能压测或容器验收。

## 可复现验证

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
go test -race ./internal/catalog -run '^TestChannelOperation' -count=1
go test -race ./internal/operations ./internal/billing -run '^(TestProvisionedChannel(Operation|Management)|TestPostgresChannelOperation)' -count=1
go test -race ./internal/server -run '^TestChannelOperationsHTTP|^TestCredentialPoolHTTP' -count=1 -timeout 90s
npm --prefix web test
npm --prefix web run build
go test -race ./... -count=1 -timeout 240s
go vet ./...
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
```

专项race已通过：catalog2.925s、operations2.166s、billing2.075s、server2.859s（含既有凭据池HTTP回归）。最终66项前端测试（新增16项）、strict TypeScript/Vite、全Go race、`go vet ./...`及最终dist嵌入build全部通过。全Go race：billing34.213s、catalog4.576s、config2.951s、identity43.468s、operations7.391s、provider5.629s、routing4.476s、security3.531s、server104.381s、telemetry4.867s；其余无测试包编译通过。这些秒数是测试包时间，不是响应延迟。

预览升级实际执行：确认58123旧进程属于本工作区→SIGTERM排空退出→新程序owner migrate→owner provision→受限应用角色serve。新进程ready200且recovery_mode=false。首页与dist逐字节相同，SHA256=`64fba53a971524ed4357b155e13dda0d4715f2b1f9631bc79938abc3747a10f1`，全部27个assets亦匹配。只读元数据检查009已应用、管理revision有效、两项触发器及唯一审计索引存在；app对回执具SELECT/INSERT且无UPDATE/DELETE，projection四项权限全无。没有通过评审账号创建运营/资金数据，未执行登录后的浏览器交互冒烟。

## 复核与边界

事务显式READ COMMITTED，先按operation ID advisory锁再新语句读回执；所有channel升序锁定，再全局credential升序SHARE，之后新语句重读撤销/CAS。不锁model，避免反转发布锁序。不存在渠道即使后来并发插入也不在lockedIDs里，保持not_found，不能无锁更新。审计、回执或commit出错不自动重试未知结果。

独立复核修复两项前端问题并补回归：提交A晚回不能覆盖后来选择的历史B，统一展示世代保证ID/明细一致而A仍写入自己的内存journal；applied所有项必须含合法after且unchanged等于before，rejected所有项不得有after（含null），残缺响应不能解除unknown。后端/SQL复核与前端最终只读复核均无剩余明确问题。前端测试为helper/transport模拟与异步合同验证，不冒称完整挂载Vue的端到端测试。

控制台和HTTP契约验证不等于登录评审账号后的浏览器表单端到端验收。正式供应商认证、自动同步/标签/余额、批量密钥导入/route发布/付费探测、自助注册/支付/订阅、HA、原001/002生产与性能门禁继续独立推进。本轮没有性能结论。

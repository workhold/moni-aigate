# 011 模型发现验证

2026-10-31，工作树无提交SHA；[spec](../../specs/011-model-discovery/spec.md)、[plan](../../specs/011-model-discovery/plan.md)、[清单](../../specs/011-model-discovery/acceptance.md)。状态：限定范围本地验收通过，迁移/最小角色后的最终预览就绪、资源匹配。功能优先，002/008性能后移。

## 实现范围

管理员在渠道页选择OpenAI/Anthropic/Gemini目录格式，以session/CSRF、近期再认证及expected_version发起模型发现。固定渠道/凭据版本与开始时的已发布绑定，受控GET读取目录，保存不可变开始、唯一结果及各自同事务审计。支持历史、未绑定模型、目录存在/缺失绑定（含暂停模型）、动态stale。目录不自动创建/删除/授权/定价/发布，不调用生成或客户计费。

仅保存排序去重的安全ID；10s、10页、1000原始条目、每页1MiB/合计4MiB（多读一个字节检测超界）。三格式鉴权分开，复用SSRF/DNS/端口/TLS；分页仅内部构造且同源，禁止重定向、代理和重试。后页失败整体失败，错误不变更推理健康。目录存在/缺失并不证明可调用性或供应商计费认证。

007新增两表、复合版本/凭据身份外键、索引与不可变触发器；ProvisionFor纳入应用角色追加权限，projection角色无目录权限。不修改旧迁移、资金规则或历史profile。

## 直接验证

| 层 | 证据 | 主要判据 |
|---|---|---|
| Provider | [discovery_test.go](../../internal/provider/discovery_test.go) | 三格式、分页/header、严格JSON/ID、1000条边界、后页失败不返回早页、游标/页/解压字节上限、SSRF/每页DNS、重定向/代理、真实取消与短deadline |
| 最小角色PG | [discovery_integration_test.go](../../internal/operations/discovery_integration_test.go) | app开始/收尾/读取及重放幂等/冲突；app及owner不可变、projector无权限；五种跨channel/version/credential组合外键拒绝；开始/终态审计失败与事实同回滚 |
| HTTP+PG | [discovery_integration_test.go](../../internal/server/discovery_integration_test.go) | 三格式真实GET、净化/差异/多公开绑定与暂停模型、历史跨渠道拒绝/分页；管理员/CSRF/再认证/恢复/ready/旧版本/撤销先拒；分钟限制和共享并发；HTTP/后页失败无部分差异/健康变更；读取中轮转不持tx且结果stale；快照不被新revision改写；中断/取消与许可释放；审计失败discovery_pending不交付目录；未发布渠道可先发现 |
| 前端 | [discovery.test.mjs](../../web/tests/discovery.test.mjs) | 格式/请求、元数据与完整结果解析、缺失不作删除、stale/多binding、无部分成功差异、渠道关闭/切换/旧响应隔离、刷新旧A晚回不覆盖新B；共39项前端回归通过 |

所有HTTP供应商均为本机mock，PG使用专用moni_test随机独立schema/角色；不调用付费模型、不改评审账号/MFA或真实客户资金。独立静态复核已完成，未发现新的设计/实现阻断。

命令：

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
go test -race ./internal/provider ./internal/operations ./internal/server -run Discovery -count=1 -timeout 120s
npm --prefix web test
npm --prefix web run build
go test -race ./... -count=1 -timeout 240s
go vet ./...
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
```

Provider最终13个专项测试函数通过，2.019s；server/operations专项通过，3.395s/1.786s。前端39项测试、strict TypeScript/Vite构建通过。全Go race通过：billing25.585s、config2.044s、identity42.976s、operations5.142s、provider3.792s、routing3.711s、security4.059s、server93.695s、telemetry4.317s；其余无测试包正常编译，手动容量按原开关跳过。全库vet和最终dist的嵌入build通过。上述秒数为包测试执行时间，不能当作响应延迟。

最终011预览升级实际执行：确认旧本项目进程与cwd→SIGTERM排空退出→新程序`migrate`成功→owner连接`provision`成功→受限app角色`serve`通过迁移Verify。`GET /health/ready`返回200与`{"recovery_mode":false,"status":"ready"}`，首页字节与最终dist一致，SHA256=`56f79e75d7d7e72d9e779c0a2d6b44f8257677e9d04826cd92d9e46fa735d13f`。本机预览实际权限读检确认007已应用、app可读/追加两表且无UPDATE/DELETE、projection无目录SELECT权限。

浏览器访问`/platform/channels`正常显示登录页并保留返回地址；本轮没有登录评审账号或操作新表单。新组件与管理业务通过前端契约/strict构建、独立静态复核和后端真实HTTP+PG验证，不将其写成已完成浏览器账号端到端验收。

复核推动两项收紧：总字节预算按剩余量限制实际读取，避免先多读整页才判失败；历史刷新捕获detail选择世代，且读取历史时禁用切选，防止旧刷新覆盖新选择。都保留对应回归，不以界外结果作为成功。

## 未执行边界

正式供应商目录/SDK兼容性、未支持厂商分页、自动同步/定时、凭据池/批量经营及余额查询均未在本切片完成。目录ID不推断协议profile或供应商计费硬界。真实供应商、容器/灾备、目标资源/竞品/50ms等生产门禁保持原状态；本轮未做性能压测。

# 采购来源值、消息格式与跨链连续性

2026-11-04；基线 `32fe22cbad734d160110f239a9affe9cd3754cde`。按[实施合同](../../specs/023-periodic-products/b-procurement-replay-implementation-contract.md)先冻结接受集、事件联合和连续性义务，再并行实施与交叉复核。承接[人工顺序续片](procurement-reconciliation-order.md)，当前模块关系见[架构细图](../architecture/procurement-replay.md)。

## 实际范围

- [原件值](../../internal/procurementprefix/source_closure_original_values.go)逐类核40种安全投影的金额、枚举、NULL、余额桶和PB本地联合。保留旧TEXT身份与允许nil的采购UUID，PB明确非零UUID单独处理；平台REVENUE/FUNDING_CLEARING允许有符号余额。所选unmanaged报价允许未知或部分单价，managed仍要求完整已知报价。
- [严格payload](../../internal/procurementprefix/source_closure_payloads.go)核四类采购命令before/after快照、固定采购审计及相关outbox的实际JSON ABI。拒绝重复decoded键、未知/缺失字段、错误类型、多根JSON和非法Unicode；NULL与字符串、SQL NULL与JSON null分开。自动cost_recorded与waived保留NUMERIC(100,0)非负金额，人工Money仍为非负int64，不经浮点数转换。共享的其他普通审计/消息族不被前缀规则擅自收紧。
- [业务联合](../../internal/procurementprefix/source_closure_business_unions.go)核输入Usage/digest、报价与成本分支、账户gate/桶/revision、八种采购event目标与合并ceil，以及有符号ledger余额行内算术。unknown消费只能来自prepared/sending；confirmed成本超U必须异常，但token越界可在金额未超U时同样异常。dispute键精确绑定attempt与evidence_ref。
- [连续性](../../internal/procurementprefix/source_closure_continuity.go)按显式attempt node与account journal推进delivery、cost与p；dispatch以其真实journal推进p。观察cut须在该attempt相邻p动作之间，成员只能出生一次并在实际历史前缀内复用。不得用墙钟、XID、UUID或其他域Stats补造顺序或有效性。

全部叶子共享原work、绝对deadline和资源预算。错误按物理表与自然PK去重，跨表同ID分别计数；取消或执行错误返回零结果。没有新增数据库访问、网络请求或资金写权限。

## 交叉复核与修正

独立审查发现并修复6处PB optional UUID漏判；原反例与历史接受集race复验通过。业务事件外部overlay复验原两处漏判与三正例，并补dispute精确键正反例，race通过。原报价接受集、人工Money与自动宽金额、signed REVENUE分别保留回归。

连续性独立overlay复现错误账户journal污染正确账户动作序的问题：map遍历顺序可导致同一raw输入计数不稳定。修复先验证唯一origin与永久account/binding/source/node桥，坏journal仍报错但不进入正确attempt动作序。仓库保留128次、两个输入数组顺序的回归；另一代理原样复验256次外部反例，错误计数始终为1且正确reserve未被误标。该复验race通过，2.277s。

## 根集成验证

全部并行文件冻结后，根实际执行：

```sh
env -u TEST_DATABASE_URL go test -race -json ./internal/procurementprefix -count=1
env -u TEST_DATABASE_URL go test -race ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1
npm --prefix web test
```

完整新包 **144顶层、845子例全部PASS，0 SKIP/FAIL，3.461s**；旧采购证据checker回归PASS，1.553s；前端286项全部PASS，0 SKIP/FAIL。最终以根完整包为本片集成结果，不将并行专项重复加总；专项耗时不是首字节、首token或吞吐性能。冻结后仅更新未完成义务说明，保留PB JSON、跨行语义与精确效果集合、历史观察和逐对象Qualified五项，行为代码未改。

README、贡献与安全说明、Dockerfile、Compose、Makefile及开发/部署手册已经独立只读交叉核对，未发现确定的安装使用阻塞。README补充三种启动方式对照与本片验证入口，明确无固定测试账号、MFA默认可选、普通001–022迁移与候选模块范围；未将文档审查表述为实际容器或1Panel部署通过。

根已完成统一架构语义复核并刷新1767文件快照；只读架构、32表715列/68 generated布局检查与 `make check` 全部PASS，包括 `go vet ./...`、Vue strict及Vite构建。归档本段后再次刷新并只读核验，代码、测试和依赖不再修改。本轮未运行依赖专用PostgreSQL的 `make verify-mvp`，不引用历史整仓结果替代当前执行。

## 剩余门禁

来源域仍 `Complete=false`、`Qualified` 为空，无公共 `Check`。PB操作的 `safe_input` / `result_snapshot` 两列严格JSON、完整calendar/configuration/window关系、来源与原件内容及业务cause的跨行精确对应、ledger/客户与供应商PB效果的完整归属和双向集合、逐对象依赖资格尚未收口。finish仍缺未派生decision的历史latest/sent观察与历史PB配置/window/absence闭包；完整原Base调用与aggregate继续保留。

32新表715列/68 generated、40原件482列、旧23数组173字段及历史十一项Unproven未改签。没有正式迁移、reader/profile/ACL安装或线上资金接线变化。本片未执行PostgreSQL、真实供应商、支付、Docker或1Panel验收；相关专项与R2、商品/原路退款/拒付、生产和最终性能门禁分别推进。

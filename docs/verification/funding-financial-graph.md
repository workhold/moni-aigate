# 资金21表同session与人工/券、CustomerKey关系核验

日期：2026-11-02。基于已推送 `fb4c9a3` 继续增量。状态：库级实现与定向本地测试；不是完整资金来源组合、维护切换或生产验收。

## 冻结合同与实现

- [同session读取合同](../../specs/022-self-service-payments/c0-financial-read-session-contract.md)：固定21表、同ctx/query/schema、128行C序keyset、完整EOF/Err/Close及私有完成记录；全局100000实际源行与16MiB原始投影文本，多core共享行不重复计源预算，原lease最多30秒。
- [人工/券来源图](../../specs/022-self-service-payments/c0-funding-source-graph-contract.md)：来源/receipt、双审批次与撤销、兑换资格、commerce终态、精确两条资金分录与唯一audit/outbox；四类payment kind仅通用检查。
- [CustomerKey资金图](../../specs/022-self-service-payments/c0-customer-key-graph-contract.md)：独立ceil计算R、预占/终态、原始收费C、消费退款、Key held/spent/limit；退款不减spent，多pending退款不误判。

[billing读取入口](../../internal/billing/funding_financial_read.go)拥有同一原始图，复用[唯一ledger raw算法](../../internal/billing/funding_ledger_query.go)，调用四个独立core；facts仅公开固定scope、计数与一致性摘要。旧ledger/source reader及维护wrapper的SQL、字段、第一页游标布尔语义与错误身份保持兼容。组合保留旧ledger journal xid/time与旧SourceLinks规范数字的执行资格。

[维护入口](../../internal/maintenancescan/funding_financial.go)仍消费原ScanFunding，与兼容三表入口在每个Permit中二选一。Require一次、四core正常完成、Finish一次；结果核四scope并绑定同一原Permit，Snapshot终结撤销全部副本。Input/Resource/Canceled/Timeout/Execution为封闭错误，无driver、SQL或原文cause出口。零值、JSON与假query不能制造维护proof。

## 测试范围

[reader测试](../../internal/billing/funding_financial_read_test.go)覆盖：

- 21表各自0/1/127/128/129行；显式固定投影与C序keyset、满页继续到真实EOF，共享表只读一次。
- 每表Query/nil Rows/Next/Scan/Err/Close/重复/倒序/超页失败，错误全零且关闭Rows；终态同时观察Err与Close。
- 可为空TEXT身份、SQL NULL与非NULL空值、typed时间/版本/金额列映射、共享owned用户/账本记录。
- 正式人工入账图四摘要一致；错误分录不跳过最后reader与Key core；全部21表EOF后ledger或SourceLinks的core执行错误仍返回零facts。
- 从第一表累积到最后表的100000/100001行与16MiB/多1字节边界；多表/多core不得重置预算。
- 读取中及末表EOF/Close取消、父超时、nil/typed-nil query/schema输入资格、旧ledger元数据前置及脱敏。

[来源core测试](../../internal/fundinglotverify/funding_source_graph_test.go)和[Key core测试](../../internal/fundinglotverify/customer_key_graph_test.go)独立验证正反关系、状态/拒绝理由、闭合JSON、规范数值、重复typed唯一键、零金额必需分录、回拨时间、共享资源和取消/超时。独立审阅对照真实writer，未用写侧SQL predicate作为oracle。

[维护封装测试](../../internal/maintenancescan/funding_financial_test.go)核假lease/零值/JSON/缺scope无authority、四summary拒绝无proof、脱敏及schema/Finish等错误封闭映射。

## PostgreSQL QA与未执行边界

[真实消费者QA](../../scripts/maintenance-scan-qa/maintenance_integration_test.go)改为新ScanFunding入口，Plan检查四摘要与独立ledger一致，Init额外损坏来源引用和Key.held，核相应确定mismatch；既有采购/sandbox局部摘要保持自身范围。全部结果检验原Permit绑定、禁止重复kind、错误Permit与原Snapshot终结撤销，并比较扫描前后资金、legacy、采购与Key事实。

扩大读取时通过独立静态复核修正了旧fixture的四处问题：

1. 正式Store.Reserve需要owner的member_model_grants，除tenant模型授权外补此明确成员授权。
2. 客户request/reservation改由Store.Reserve生成真实Key累计及journal；APP人工入账219，加券17和sandbox23，共259，覆盖R120；采购upper仍240。
3. 所有真实业务写入放在候选C0 DDL安装之前，避免无epoch的保护trigger拒绝正式writer；只读维护扫描不关闭业务trigger。
4. legacy只读基线在获准fixture与隔离损坏完成后取，避免把后续合法核款改变共享清算账户误判为扫描写入；完整tenants计数为两个现存租户。

本轮仅核验环境变量是否存在，未输出DSN/秘密：`TEST_DATABASE_URL`与`C0_MAINTENANCE_DATABASE_URL`均未配置。真实PG Plan/Init明确SKIP；没有执行`make verify-mvp`，不能把Go包成功当作数据库商业验收通过。上述QA修正仍需专库实跑。

## 当前本地验证

最终汇合后执行 `go test -race -count=1 ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa`，四包通过（2.229s / 16.410s / 15.577s / 14.774s）；对应四包 `go vet` 通过。纯core各自的先前专项记录保留原命令与范围，不替代此集成运行。

19张现有Mermaid图经官方Mermaid 12.2.0语法解析全部通过，逐图内容SHA见[机器记录](architecture-funding-financial-graph.json)。本轮未进行浏览器布局渲染；历史PNG不追溯证明修改后的图。

`go test -race -count=1 ./...` 在全部并行实现汇合后通过。真实PG opt-in未配置，明确跳过；该全仓运行没有代替专库验收。`make check`通过，包含只读架构检查、全仓Go vet与Vue/TypeScript strict生产构建。根汇合复核后更新架构快照（1299文件）并通过只读检查，最终验证文字同步后再核快照。未运行`make verify-mvp`，数据库跳过边界保持。

## 下一步

21表session只是完整phase组合的基础。采购evidence与完整事件、PB独立原始图、完整sandbox、跨域关系、完整七域coverage和单次phase Finish仍待实现；随后同pre快照读取basis，才推进protected init/post replay、epoch与APP资金接线。没有新增迁移、在线权限、生产维护CLI、RuntimeProof、原路退款或拒付能力。部署、供应商认证、商品、HA与性能继续按[交付计划](../design/delivery-plan.md)推进。

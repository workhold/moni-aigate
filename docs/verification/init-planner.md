# 最小旧数据投影与纯初始化规划验证

2026-11-03，基于5089f7b，按[冻结合同](../../specs/022-self-service-payments/c0-init-planner-contract.md)先设计后并行实施。新`LegacySeedRows`仅九组普通原件；`ProjectLegacySeed`从原68表完整一致Facts深复制；新`fundinglotinit.Build`独立核业务/账本并生成描述性INSERT计划。没有迁移、ACL、普通serve或资金写入变更，第八ScanKind未消费。

## 独立验算与边界

三tenant静态golden由独立手算金额、集合与UUID帧形成，测试不调用planner生成expected。令M=9223372036854775807：A租户收100+70+50，旧预占60收费30、已批准退款10+5、pending7、未结40与0，A=165/H=40；B租户收M−100及M−70，未结M−20，A=M−150/H=M−20；第三旧tenant为空TEXT ID、无账户或历史。平台共享clearing/revenue和每条原分录同时合法，不靠余额反推开账。

全局available=9223372036854775822、held=9223372036854775827；5receipt、1sandbox、4reserve、1settlement、2approved、3unfinished、6opening、4lot/event/cache、2allocation、3seq0/head、12legacy source，数据写集47行，含未来epoch/attestation两行预留G=49。terminal无held、R0仍保留opening/source且无lot，approved和历史receipt不重复发行本金。

外部测试独立核固定UUIDv5、UTF8字节帧、拼接歧义、Unicode不归一、跨epoch、金额/时间不入ID、乱序稳定及输入/输出隔离。源负例覆盖缺失/重复/跨tenant、状态/币种/金额、精确journal、原退款累计、未知wallet journal及孤儿；R0和finish0仍要求两条零值分录。资源检查包含九组输入、生成行与输出字节/metadata预留，错误零结果；这些测试不构成数据库授权或真实资金提交。

## 集成执行

最终相关四包race以明确MONI专库运行33.983秒通过：223个顶层、1432个测试终态，无失败；精确源码SHA前后相同。真实PG实际运行原68 reader、非空C0的84 reader、原生金额形式以及本轮两个旧数据组合场景；本轮新PG父子3终态全部PASS，旧Read ctx取消后新ctx投影、再纯Build成功且原资金不变。新规划包不依赖SQL/维护模块，普通在线依赖隔离检查通过。完整命令、所有SKIP及SHA见[机器记录](init-planner.json)。`make check` 8.057秒、明确TEST专库`make verify-mvp` 1007.389秒通过，包含286项前端及46个Go包的默认race、vet和构建。unit与full源码SHA全程相同。整仓未打开C0维护或MONI独立PG opt-in，新投影真实PG以以上MONI专项为准，不把默认SKIP升级为数据库验收。最终测试后仅更新文档与架构快照。

统一架构已补模块依赖、普通数据流、未来phase边界、HLD/LLD与交付顺序；所有Mermaid由官方12.2.0解析并在独立临时浏览器渲染，交付时记录最终正文身份。README复核特性、启动/管理员、API、Compose/1Panel、MFA默认可选及验收边界；部署文件和命令本轮未改，不新增容器或目标环境验收结论。

仍未交付：完整protected pre/basis/write/strict v2与实际84post/commit-ready、非空epoch observer/runtime、生产维护CLI、全部资金接线、原路退款/拒付、商品与生产/HA；真实供应商、正式支付和性能对照另验。普通startup不能用经营后109表当前basis强等于历史cutover basis，外部Plan/Facts/Report不能授写。

当前23张Mermaid已解析/实际渲染，模块总览与本片详细图已视检，精确四份正文和两PNG身份见[图证据](architecture-init-planner.json)。图为文档关系，不把未来phase虚线当生产装配。上一批5089f7b的GitHub CI 41485087128十组success，仅证明原批；本轮将另推送。

最终README已由独立代理对照CLI、开发/部署手册、Makefile、Dockerfile与Compose复核，无需阻断交付的实质差异；容器/1Panel实装、正式供应商、在线收款和性能对照仍按原门禁。

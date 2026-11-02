# 采购、周期预算与支付来源：独立纯核验核心

日期：2026-11-02。承接 `a874aab` 的21表资金基础，按 SDD 先冻结三份合同，再分工实现。状态为**库级纯算法**；没有将新核心接入完整数据库 reader 或全来源维护入口。

## 合同与实现范围

| 核心 | 固定输入与检查 | 历史可证性 |
| --- | --- | --- |
| [采购证据](../../internal/fundinglotverify/procurement_evidence_graph.go) / [合同](../../specs/022-self-service-payments/c0-procurement-evidence-graph-contract.md) | 23组原行；同图内部重跑预算、绑定、双审、命令、成本审计五core，补meter出生/Usage、finalization/identity摘要、八种预算事件、成本与账户来源 | 缺incoming归属、sticky前驱、复杂事件/成本/账户历史按精确行级规则报告Unproven；不以时间排序猜真实先后 |
| [周期预算](../../internal/fundinglotverify/periodic_budget_graph.go) / [合同](../../specs/022-self-service-payments/c0-periodic-budget-graph-contract.md) | 18组业务行；从来源起点独立重放control序列、allocation/window revision与三层投影；配置版本、指纹、审计与历史snapshot | legacy成员、legacy效果边界、operation snapshot prefix分别计缺证；保留原owner公共Verifier |
| [支付来源](../../internal/fundinglotverify/sandbox_source_graph.go) / [合同](../../specs/022-self-service-payments/c0-sandbox-source-graph-contract.md) | 33组原行；独立strict canonical/digest，SKU链、人工命令/query、stored-verified原件、processing/review、identity/selection/capture到完整资金效果 | 不认证真实安装/HMAC/外部收款；保留旧enum中语义未定义的exhaustion review原行缺证，正常无review耗尽不误报 |

输入是未受信原始数据。局部空图完成、调用者构造DTO、摘要相同或Balanced都不能制造数据库读取完成位、Permit、RuntimeProof或资金写入权。确定矛盾优先mismatch，真实历史缺证另计；资源、取消、超时或固定依赖执行失败返回零结果。

## 关键审查修正

- 采购保留实际 `contract/invoice/unknown` 成本状态；不由latest finalization反推当前request.status，因为下一次合法派发可改变该状态。
- 正常采购预算事件必须绑定产生该事件的首次语义identity及原meter，语义重放的新meter不能随意替换原事件引用；成本消息独立核其正常证据来源。
- legacy先人工更正、后正常证据可合法没有cost_recorded；资格缺证仅接受真实更正原件等有限前缀，不能单凭当前状态豁免缺记录。
- PB以big.Int保留NUMERIC100、负delta和baseline下降后回升；不从当前cache或事件before列初始化独立期望。原生时间保留NULL/finite/±infinity，非法组合明确mismatch。
- 支付允许迟到原件、跨claim复用query child、review后不重开、同fact不同Evidence和永久overflow；当前停售/off或处理晚于checkout期限不推翻合法历史capture。
- P1与v1 proof各自保留真实canonical转义约定；正式语义review对每个Evidence恰一，额外review不能借宽DDL取得成功资格。
- 对照原Processor/021收紧mutation unknown、query retry和两类review的错误码/代次矩阵；无child且无可信snapshot不能以空错误码伪造可达retry。已保存的迟到completed快照仍可保留在retry/review，不倒推重开。
- 原v1只要求key ID唯一，允许不同ID的摘要相同；native/P1业务时刻允许有限year1零值，keyTime与v1窗口继续沿原非零约束，避免把不同时间合同混用。
- meter的主键去重独立于source唯一键，即使某行因源冲突被排除，其主键仍不能复活；legacy无冲突且已有更正时，当前成本不能退回旧cost_recorded。
- 共享源行先累计资源再建索引；typed索引与排序保持有界，数据mismatch继续完成其余反向关系，不以错值0补部分累计。

## 实际测试与限制

各代理的纯fixture定向测试均已通过；根汇合运行全 `fundinglotverify` race（2.694秒）及四相关包vet通过。随后新增的最小采购复杂历史例覆盖“确认成本→发票更正→无消费争议”，明确验证EventOrder缺证与确定delta矛盾同时保留。最后原仓库 `make check` 与指定真实专库下的 `make verify-mvp` 均通过，后者墙钟959.665秒，fundinglotverify包2.526秒，覆盖286项前端、strict构建、全默认Go race、vet与嵌入程序构建；实际失败、商业命令并发修复和复验见[集成记录](commerce-concurrency.md)与[机器结果](source-graph-integration.json)。

测试包含采购正常/未知/恢复/冲突出生/跨attempt来源碰撞/发票降低与旧成本消息、PB双scope/legacy与catchup/事件与三层投影/严格JSON/typed重复/时间和数值、支付通知与query完整capture/四类命令/SKU多版回退与no-op/未知订单后来出现/永久overflow/stale原件/资金反向损坏。资源覆盖100000行、16MiB及相邻边界、NULL与context错误全零。

仍未穷举每个refund/dispute通知union、全部review/provider偏序组合、所有复杂采购历史与排序期间取消；没有23/18/33组新reader的真实PG证据。上面测试不是全历史补证，也不是正式供应商、支付或生产验收。

旧五类维护reader与21表资金入口另有[真实PG补验](maintenance-financial-postgres.md)，不能将其结果转移给本次未接线的新算法。新完整phase仍须在同一受审session取固定列并集、共享预算、记录全部EOF与反向sweep、唯一Finish；basis、epoch和资金writer继续后续。

接线前的只读审查另明确两项必须先冻结的设计：采购与共用旧DTO的`time.Time`无法保留SQL非有限时刻，不能用零时间或NULL替换；各旧reader的文本预算与PB原生时间/bool预算不同，不能把多次局部预算重置或各自Finish当作完整读取。新合同须明确native时间保真、物理表固定列并集、一次共享预算和内部完成矩阵；ordinary模式须由已认证前驱决定，不能以空DTO或disabled policy推断。

19张现有Mermaid图经官方12.2.0语法解析通过，逐图SHA见[机器记录](architecture-source-graphs.json)。本轮未执行浏览器布局渲染，不沿用历史图片证明当前图。

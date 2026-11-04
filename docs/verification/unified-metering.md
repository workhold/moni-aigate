# 028-U共同纯域验证

2026-11-04。先冻结[共同合同](../../specs/028-unified-metering-media/u-domain-contract.md)，再实现`inference`，与原`metering`复用抽出的唯一`strictjson`语法层。当前模块/依赖/信任边界见[架构](../architecture/unified-metering.md)，准确原件、命令及源码摘要见[机器记录](unified-metering.json)。这不是完整U生产验收。

## 实际实现与审查

新增六文本不可变Profile、公共request/收紧attempt计划、原known/unknown报价、分维度观察及分侧FinalQuantities。input-only明确0输出价/界/量；采购缺版本和显式unknown版本分别保留，unknown读金额/费率返回错误。人工客户核对即使提供共享最终量也不能确认supplier。对象及输出slice/bytes为copy，零值无效；金额仍调用metering，未写第二套价格计算器或协议parser。

metering新增schema/费率只读描述，防止consumer反解析canonical或获取可写内部slice。通用JSON语法从原包移动到strictjson，metering仍映射原sentinel并保持原领域错误顺序/canonical/摘要；独立审查发现原depth计数漏拒九层全空容器，现明确容器depth>=8拒绝，八层空容器及深度8标量仍接受。此项收紧到原合同要求，不能声称语法接受集逐字未变。

交叉审查同时修正外壳字段/值与身份比较的错误顺序、手工supplier冲突优先级，以及持久唯一槽沿原tenant/source/event而非缩小到request范围。存储草案明确FinalQuantities外层摘要与纯核Quantities摘要的区别；这些是合同对齐，不表示SQL已经安装。

## 限定本地结果

根汇合后执行`go test -json -race ./internal/metering ./internal/strictjson ./internal/inference -count=1`，最终原件`.local/028-common-final.jsonl`，188658字节，SHA-256 `0e0b65ce955f9bc76e9a0b1a549b82da64d099d1bd26ae3c749ffcb1b149eeee`。

| 包 | PASS顶层 / 子项 | 包报告时间 |
| --- | --- | --- |
| inference | 13 / 106 | 2.611秒 |
| metering | 14 / 49 | 1.725秒 |
| strictjson | 3 / 18 | 2.027秒 |

三包均0 FAIL/SKIP。顶层包含默认执行的fuzz seed入口（inference1、metering2），不是全部顶层均为普通Test，也不是持续fuzz执行次数。作者及审查者先前专项与本轮重叠，不相加。测试涵盖7个Python独立framing/SHA黄金值、六协议与stream、候选完整绑定/最大界/收紧、same UUID异schema、known零价/unknown/int64溢出、非零retrieval输出价拒绝、不同mask/共享缺量/异常归属、超界actual保留、人工supplier隔离、观察时间不变事实摘要、严格JSON组合错误、大小及并发copy。

公开API纵向专项独立断言：公共input120、所选attempt input100、output20；retail率1/2和1/3得R=67，supplier率1/4和1/5得U=29；实际量3/2得retail3、supplier2微元。交换两侧率拒绝。supplier原价unknown时，adapter量仍可确认，retail按已知原价得3；人工客户量不能代替采购证据。这些只是纯对象与数学组合，不声称发生过钱包预占或正式扣费。

根另执行共享语法短程fuzz（2 workers、请求4秒）：`FuzzStrictParsers`实际174750次、5.640秒PASS。共同外壳`FuzzInferenceEnvelopes`同样2 workers/请求4秒，实际188967次、5.525秒PASS。没有将默认seed测试当作持续fuzz。共同三包`go vet`、最终`make check`（只读架构、采购布局、整仓vet、前端strict/Vite生产构建）及嵌入Go构建通过；原件摘要由机器记录列出。最终汇合后按AGENTS完成架构语义复核和显式refresh/只读check；没有执行完整当前全仓PG或make verify-mvp。

## 未完成范围

未新增正式迁移，未修改当前provider/billing/pricing/routing/HTTP/UI资金路径，未运行本片PostgreSQL商业链。028-U生产DDL、六文本原子切换、原权限/采购/PB/027来源/退款/恢复和026元数据/CSV/UI扩展仍未完成，按[任务清单](../../specs/028-unified-metering-media/tasks.md)继续。当前只有每模型唯一profile发行，多候选纯测试不证明生产异构路由。媒体/任务/Realtime、真实供应商/支付/1Panel及性能均未因此验收。

026经营报表和027资金来源的既有本地证据保留各自范围，本片不声称重新执行其PG验收。GitHub上一提交的Actions因账户支付/额度未启动，见[027记录](wallet-sources.md)；本地通过不等于远端CI通过。

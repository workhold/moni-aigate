# 统一计量与共同值域（028-U）

2026-11-04。共同纯领域已接入当前文本模型的报价、预占、结算和终态投影。`024` 只安装空库，不改 001–023，也不转换已有历史。UUID 档案在 HMAC 就绪时写入计划 canonical，观察必须绑定已有尝试，请求和尝试各有四行用量界。计划就绪时请求和尝试快照写入 inference 报价外壳。纯核报价摘要、六协议 HTTP/UI 和媒体仍未完成。

规格入口：[总体](../../specs/028-unified-metering-media/spec.md)、[共同域精确合同](../../specs/028-unified-metering-media/u-domain-contract.md)、[存储草案](../../specs/028-unified-metering-media/u-storage-contract.md)、[消费者清单](../../specs/028-unified-metering-media/u-consumer-inventory.md)、[阶段任务](../../specs/028-unified-metering-media/tasks.md)。当前经营/资金链分别见[026报表](operating-reports.md)与[027来源](wallet-sources.md)。

## 已实现的纯依赖

```mermaid
flowchart LR
    Inference[inference\nProfile / BoundPlan\nQuote / Observation / FinalQuantities]
    Metering[metering\n完整向量 / 精确有理费用\n末尾一次ceil / schema绑定]
    JSON[strictjson\n字节和深度界 / 唯一member\n无损Unicode / 完整EOF]
    Inference --> Metering
    Inference --> JSON
    Metering --> JSON
```

这三个包无数据库、网络、环境、时钟或资金写入。图中没有serve调用边，不代表已完成U生产接线。纯值解析成功不授模型派发、租户权限、供应商认证或资金许可。

| 源码 | 责任与边界 |
| --- | --- |
| [inference](../../internal/inference) | opaque不可变profile、request/attempt计划、known/unknown原报价及分维度观察；只接受已验证的六文本材料，协议parser仍归provider |
| [metering](../../internal/metering) | 完整schema/side绑定、数量与原界、规范有理率、精确金额和精确成本比较；不判断供应商真实性或业务收费资格 |
| [只读描述](../../internal/metering/describe.go) | schema身份/维度/mask与已验证费率的copy；input-only可拒非零输出价，不用反解析canonical或暴露内部slice |
| [strictjson](../../internal/strictjson/parse.go) | 从原metering提取唯一通用语法实现；拒重复键、非法UTF8/孤立surrogate、深度>8、尾JSON；各领域仍独占字段/值/绑定规则 |

## 关键合同

Profile和所有vector零值无效；canonical返回copy，digest绑定完整schema定义。共同Profile以摘要绑定完整provider合同；同一UUID但不同schema内容不相容。U只发行default variant及同步json/events，Embeddings/Rerank明确0输出界、0输出价和0最终量，不用missing代替0。

request Plan绑定排序去重后的完整合法profile集合、有效输入HMAC、所选output和stream；公共输入界取候选最大值，attempt只能命中原集合并收紧。**实际U路由仍只有每模型唯一capability**，跨渠道/凭据先去重且由billing重新核闭包；纯算法的多profile支持不表示已安装异构路由的持久身份或参数兼容规则。

known报价保留原版本、完整rates/bounds并由metering算R/U；unknown只允许supplier，区分version_missing与basis_unknown，后者保留已选版本。unknown不能读取伪0金额或空rates。媒体及managed采购准入另要求known；纯对象不替代账务政策。

Observation必须绑定原tenant/request/attempt/profile/schema/variant/plan。每维保留confirmed/provisional/missing/conflicting；只有某一侧mask完整时才能获得该侧FinalQuantities。supplier独有量缺失不抹掉已确认retail，共享量冲突不能被隐藏。人工approved_reconciliation只确认客户侧，永不能据此确认供应商成本；无attempt准入失败直接Release，不伪造观察。真实超界量保留，客户R与供应商U异常在未来账务接线处理。

## 生产集成与经营报表

`024_unified_text_metering.sql` 在同一当前模型中安装两维文本 schema、有理费率、请求/尝试快照、事件数量行和双侧终态投影；沿现有一份 finalization、一个客户 settlement 及每 attempt 采购事实，不建媒体钱包或第二结算器。提交时核对事件行、已知采购报价、请求价格归属、尝试与请求一致，以及终态投影等于事件行按 `moni-aigate.inference.final.v1` 重建的结果。已知供应商金额必须等于原费率对终态用量的一次 ceil；授权更正等于更正原件；未发送或认证无消费只能为 0。人工核对不能确认供应商侧。应用角色只能 SELECT/INSERT 计量表。001–023 不改写，旧历史库不自动转换。受管采购的单次预占必须等于该尝试的预算事件合计；已批准提案必须等于预占消费，结算时还要等于供应商金额。批准时证据条数必须仍是提案记下的条数，修订号必须是当时的值或批准这一步加一。用量事件必须指向同一次请求上已经存在的尝试，不能改挂到别的请求，也不能在没有尝试时补造观察。没有尝试的恢复仍按原政策释放。档案版本是 UUID 且请求 HMAC 为 64 位十六进制时，请求快照保存共同 request 计划，尝试快照保存由它派生的 attempt 计划。自由版本号仍不能生成计划。请求和尝试快照各有四行用量界，输入等于档案硬界，输出等于本次选定值。计划就绪时，报价外壳与 inference 的 `moni-aigate.inference.quote.v1` 一致；计划未就绪时报价列为空。纯核报价摘要仍未落库。完整九组 guard 尚未齐。

026继续只读唯一settlement收入/消费退款和全部attempt采购金额；opening不计销售。U计划从原request不可变快照补protocol/execution/schema/variant与筛选/CSV/UI，不从当前模型回填，也不从vector重算金额。未知采购仍使毛利待核对。任务poll/下载、实时heartbeat不产生第二笔收入。该元数据扩展尚未接线。

当前生产消费者与冻结研究逐个列在[清单](../../specs/028-unified-metering-media/u-consumer-inventory.md)。旧proof/scan/prospective候选只保持其原身份、编译及拒绝current边界，不升级兼容矩阵，不成为U前置。后续验收必须覆盖实际受限APP的六文本、采购/周期预算/027来源/退款/恢复、026 JSON/CSV与控制台，纯包测试不能替代这些出口。

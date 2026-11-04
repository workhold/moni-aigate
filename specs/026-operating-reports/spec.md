# 026：经营报表与对账导出 V1

2026-11-04；状态：已实现并完成限定本地专项，最终集成结果见[验证记录](../../docs/verification/operating-reports.md)。依据[ADR-005](../../docs/decisions/ADR-005-pre-mvp-single-path.md)，直接扩展当前正式业务模型，不依赖历史proof候选或旧版本兼容。

## 场景与边界

平台运营希望知道某段时间受理的模型请求赚取多少消费收入、退回多少、上游各渠道花费多少，哪些成本尚待核对，并下载请求级对账明细。只读能力，不改变资金、采购、路由或模型调用。V1仅平台角色使用；客户原钱包累计报表继续表达不同业务口径，不是新旧兼容双路径。

- 以 `request_records.created_at ∈ [from,to)` 为请求队列，统计截至同一数据库快照的结算、消费退款及所有attempt采购成本；不是按资金发生日期的现金流报表。
- 必填from/to为带时区RFC3339，统一UTC；非空窗口，最长31×24小时。可选tenant_id/model_id按当前UUID规则检查。至多5000个请求；超限返回422，要求缩短窗口，不截断或抽样后展示总额。
- 平台探测、钱包充值/赠额、支付手续费、税费与其它经营费用不纳入。收入是客户已结算消费，不能标称现金收款；渠道只汇总采购成本，不分摊客户收入。
- 一个request的销售额只计一次，所有attempt成本均计入。成本仅 `cost_status IN ('contract','invoice') AND cost_amount IS NOT NULL` 为已知；其它项计未知，不按零推定。尚无settlement的请求为未结算。
- `net_revenue = charged - refunded`。仅该范围未结算请求=0且未知attempt成本=0时提供 `gross_margin = net_revenue - known_provider_cost`；否则null。此为模型调用毛利，不是全平台净利润。
- 金额以CNY微元规范十进制字符串传输，支持NUMERIC(100,0)，Go汇总使用big.Int；有限计数用JSON整数。模型/渠道名称使用当前可见标签，ID作为准确关联身份。

## 数据与模块

新增 `internal/reporting` 负责Filter校验、只读快照读取、精确聚合和CSV；server负责平台授权/HTTP映射；Vue独立组件负责筛选/展示/下载。只读取当前request_records、settlements、upstream_attempts、models、channels、tenants，复用APP现有SELECT权限，无候选SQL、角色或历史回填。

`Read(ctx,*pgxpool.Pool,Filter) (Report,error)`：工作总预算至多5秒，RR READ ONLY，数据库时刻as_of与所有原件来自同一快照；无行锁、DML、网络调用或报表投影依赖。先将cohort限制为5001个request，再读取其最多两次attempt；遇超限返回零结果。关闭rows后ROLLBACK，成功清理后返回；错误不返回部分报表。默认不增加迁移；若实际查询显示现有索引不足，另加明确的当前索引，不能引入兼容profile。

Go/JSON合同如下（字段名为准确JSON名）：

```text
Filter{from,to time.Time; tenant_id,model_id string}
Totals{request_count,unsettled_requests,attempt_count,unknown_provider_costs int64;
       charged,refunded,net_revenue,known_provider_cost string; gross_margin *string}
ModelReport{model_id,model_name string; 嵌入Totals}
ChannelReport{channel_id,channel_name string; request_count,attempt_count,unknown_provider_costs int64;
              known_provider_cost string}
RequestRow{request_id,tenant_id,tenant_name,model_id,model_name,status,billing_status string;
           created_at time.Time; 嵌入Totals} // request_count始终1
Report{from,to,as_of time.Time; currency="CNY"; amount_unit="micro_cny";
       scope="request_created_window"; summary Totals;
       models []ModelReport; channels []ChannelReport; requests []RequestRow}
```

空结果数组为[]，金额为"0"；空队列毛利为"0"。requests按(created_at DESC,request_id DESC)排列；models/channels按ID稳定排列。平台可以在页面本地分页，不增加游标、跨快照拼页或持久报表任务。

## HTTP与导出

- `GET /api/platform/reports/operations?from=...&to=...&tenant_id=...&model_id=...` →标准 `{data:Report,request_id}`。
- `GET /api/platform/reports/operations.csv` 接受相同参数 → UTF-8 CSV，请求级一行，固定列顺序：`request_id,tenant_id,tenant_name,model_id,model_name,created_at,status,billing_status,charged,refunded,net_revenue,attempt_count,known_provider_cost,unknown_provider_costs,gross_margin`。金额保留微元整数；未知毛利为空串（空单元格），不写0。列名单位在页面下载说明明确。
- 仅现有平台admin/finance_operator/finance_approver，客户owner/billing_viewer/member均403；认证/恢复/单活Guard沿当前中间件。
- 重复、未知query参数或非法时间/ID返回400 `invalid_report_filter`；窗口超过31天也400；超过5000请求为422 `report_range_too_large`；数据库/超时失败为503 `report_unavailable`，不泄露SQL/cause。无自动重试。
- CSV完整生成成功后再写HTTP，固定安全文件名，`text/csv; charset=utf-8`、`Cache-Control:no-store`、`X-Content-Type-Options:nosniff`。所有文本单元格防电子表格公式注入（含前导空白/control后=,+,-,@）；合法数值列不走文本公式转义；encoding/csv处理引号/逗号/换行。不输出credential、密钥、prompt或响应正文。
- 每次查询/导出独立新快照，页面明确as_of；下载期间新结算/更正可使导出与旧屏幕不同，CSV不伪称屏幕快照持久化。

## 控制台

在平台报表页新增经营报表区域，默认最近7天，明确请求创建窗口/消费收入/微元导出口径。时间、租户ID与模型ID筛选，汇总卡、模型表、渠道表和请求明细；请求ID复用RouteAttempts下钻。输入可用当前tenant/model选项或明确ID，不把当前页选项误称全部目录。未知成本/未结算时毛利显示“待核对”。CSV下载走同源Cookie和现有401事件处理，不用localStorage保存数据。

编辑筛选立即废弃旧展示/下载状态并递增generation；新响应只对当前筛选和组件生命周期有效，组件卸载及路由/身份切换后旧响应不能覆盖。加载、错误、空数据和超限均有明确提示，失败不保留冒充当前范围的旧金额。CSV下载失败不生成错误内容文件，object URL用后回收。

## 验收与分工

1. 真实当前模型fixture：一个request两attempt收入一次/成本两次、退款净额、同模型多请求、多渠道、跨租户及窗口左右界、未知与未结算毛利null、大整数、空结果和5001请求上限。
2. HTTP平台角色正控/客户403、严格参数、超限/错误安全映射，CSV解析/公式转义与无秘密。
3. 前端筛选/陈旧响应/卸载、金额精度、空与错误、下载失败/URL清理的有意义测试与严格构建。
4. 更新README特点与使用方式、HLD/LLD/统一架构和验证记录；专项真实PG串行执行，集成check/build通过后提交并推送。

范围以本页为准；不加入旧数据格式适配、异步报表平台、渠道收入分摊、全现金利润、订阅、媒体或HA。先形成一个可用纵向片，再按实际需求扩展。

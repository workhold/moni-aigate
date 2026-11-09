# 026 经营报表与对账导出验证

2026-11-04。先冻结[规格](../../specs/026-operating-reports/spec.md)，再并行实现当前模型的reporting领域、平台HTTP与控制台。模块、读链、数据及权限见[架构](../architecture/operating-reports.md)；准确命令、测试名单和原件摘要见[机器记录](operating-reports.json)。

## 实现及本地专项

平台`/platform/reports`新增请求创建窗口报表、模型与渠道汇总、请求下钻及CSV。收入在request侧只计一次，采购成本汇总所有attempt；退款使用当前settlement累计值，unknown和未结算明确使毛利待核对。金额为精确微元字符串，渠道不分摊客户收入。Read直接使用当前六表的一个有界RR只读快照，不依赖累计钱包异步投影；默认HTTP与控制台已经接线，无迁移、新角色或后台任务。

| 专项 | PASS顶层 / 子项 | 实际范围 |
| --- | --- | --- |
| reporting纯race | 6 / 22 | 1.676s；精确聚合、金额、unknown/未结算、空结果与上限、CSV、微秒边界 |
| reporting当前源码整合race | 6 / 13 | 1.999s；3项纯聚合与3项专库查询，含5001拒绝/5000接受、NUMERIC100、筛选和并发RR快照 |
| HTTP当前源码整合race | 3 / 5 | 9.856s；2项纯参数/错误及1项正式001–022业务，实际gateway两attempt、结算、更正/退款、角色与CSV |

上述原件均零FAIL/SKIP，每个run有唯一且有序PASS。完整纯包与当前整合的3项纯聚合有重叠，不相加；首轮结果保留在机器记录。最终整合原件`.local/026-integrated-final.jsonl`为28899字节，SHA-256 `4ea03f68be355a762975c05850539f33327c80e4e4d4e91dfdea0fe76221e345`。前端全部299项通过，其中新增13项覆盖筛选/身份/卸载旧响应、金额与毛利、CSV认证事件、流读取失败和URL回收；TypeScript严格检查及Vite生产构建通过。`go vet ./internal/reporting`通过。

真实业务测试通过现有gateway fixture生成一个客户请求、两个不同渠道attempt和一次收费20微元，初始未知成本使毛利null。经现有成本更正及双人钱包退款流程后，采购成本18、退款5、净收入15、毛利-3；JSON及CSV保持准确。左含右不含时间边界、tenant/model无匹配空结果、admin/finance_operator/finance_approver正控与客户owner/billing_viewer/member的JSON/CSV 403均执行。报表前后模型POST始终两次，报表没有额外生成；响应不含credential、prompt或模型正文。

## 028 媒体只读接缝

`internal/reporting` 现将当前 `media_requests`、`media_settlements` 和 `media_attempts` 纳入同一 RR 只读快照。媒体结算在请求侧只计一次 `charged_amount`，释放和未结算请求不生成收入；每条媒体 attempt 都计入 attempt 总数。supplier cost 从 `media_settlements` 的不可变 known/unknown 快照读取，`media_settlements.raw_amount` 不被解释为供应商成本。失败、取消、缺失或未知消费继续进入未知成本桶，使毛利为 `null`。

55439 专项 `TestPostgresReadOperatingReportIncludesMediaOnceAndLeavesCostUnknown` 覆盖 settled/released/pending 媒体请求、同一请求多 attempt、video 的 asynchronous 元数据、operation 筛选、请求收入一次和 raw amount 不进成本。该接缝只扩展报表读取，不接入任务/实时写入，不改变账务或迁移，也不代表媒体供应商成本已经认证。

查询PG另用明确的最小六表fixture测试完整100位成本、跨租户/模型队列、5000上限，以及读取请求队列被锁时并发提交结算与成本改变：旧报告读到100/10，新独立报告读到200/20，没有拼快照。它不代表正式商业writer或角色验收，后者由正式001–022 HTTP专项承担。纯CSV测试覆盖文本公式、前导空白/control、引号/逗号/换行、大整数、负毛利、未知空单元格和writer失败。

集成人另启动临时真实001–022页面fixture，在IAB观察到收入20、采购成本7、毛利13；输入不存在的模型UUID后旧结果立即清空，查询为空，再清空筛选查询后恢复，console error/warn均为空。截图及fixture优雅停止PASS日志见机器记录。临时测试、私有状态与QA页已清理；此浏览器轮未操作下载、移动端和请求下钻，不将其写成已验。CSV和权限仍以HTTP/前端专项为证。仓库最终检查、图表、资源清理由集成人统一记录，提交推送随后收口。

集成人清理核对：其它backend为0，reporting/auth临时schema为0，私有fixture文件已删除。一个无法确认本片所有权的既存gateway schema予以保留，本片browser用户不在其中；不宣称全测试库schema归零。026细页图已由Mermaid12.2.0渲染并视觉复核；统一模块图首次隔离渲染超时，未改源码重试后解析/渲染通过。原图较大，渲染成功不等同于总览可读性或业务验收；最终`make check`（只读架构检查、采购布局检查、整仓vet、前端严格生产构建）与CLI构建均已通过；该结果不代表整仓PostgreSQL回归或生产验收。

## 边界

这是当前请求队列的消费经营报表，非资金发生日现金流、法定账单或全平台净利润；不含充值/赠额、平台探测、手续费、税费及其它费用。CSV每次读取新快照，不能当作先前屏幕快照的持久导出。正式供应商账单同步、异步大报表与生产容量/性能不属于本片；没有将本地fixture视作真实供应商或生产环境认证。

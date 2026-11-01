# 024-B0-P 纯向量计量验证

2026-11-01。依据根冻结的[精确纯层合同](../../specs/024-extension-platform/b0-pure-contract.md)新增 [internal/metering](../../internal/metering)，只使用 Go 标准库；未接入 provider/billing/routing/catalog/DB，未注册媒体端点，不表示任何媒体供应商或模型可售。

模块提供最多八个闭合收费维度、完整 schema/side 绑定、严格数量/上界/有理费率、规范编码及域分离摘要。费用按精确有理数求和后仅向上取整一次；actual 可保留超过 int64 的费用，预占溢出返回错误。旧 input/output token 适配与原百万 token 公式等价，不改旧价格或账务数据。subset/diagnostic、unknown 证据及供应商上界认证不在本纯层。

实际专项：

| 检查 | 结果 |
| --- | --- |
| `go test -race -count=1 -cover ./internal/metering` | PASS，1.744s，94.0%覆盖率 |
| `go vet ./internal/metering` | PASS |
| 严格解析短程 fuzz | PASS，309443 次，6.495s |
| 金额单调/Reservation 等价短程 fuzz | PASS，137489 次，5.266s |

测试包含合法八维/两侧不同集合、缺维/null/乱序/未知单位、UTF8/重复键/非法surrogate/深度/字节限制、固定错误优先级、MAX乘积与零费率、1/2+1/2仅收1、同ceil但精确成本不同、same UUID 不同schema拒绝、输入/输出slice与big.Int不变性、并发只读和独立 framing/SHA256 golden。旧公式由独立大整数表达式对照512组固定seed随机值与双side；不以新Amount作为自己的oracle。

根已审六个源码文件的严格解析、私有不可变结构、金额/绑定和摘要，未发现运行路径接线。B0生产SQL/报价/资金/目录/明细、B1官方语义正文和真实凭据/扣费认证仍未通过；[通用设计候选](../../specs/024-extension-platform/b0-metering-contract.md)记录明确边界。纯层测试不代替真实PG商业链路、实际媒体交付、生产或性能验收。

# 源码校准：Chat、SSE、usage 与工具调用

状态：DRAFT / 设计输入；2026-10-30。固定 New API `e449cc700eb569cdfc2199cb73e97de46944f43c` 与 Higress `bde990b4bfc7ecb581338e2d0ae858aaedb2fd69`。复用已有研究快照，补读实际调用者、状态分类和测试源码；未运行上游或测试，未发起模型调用。

New API 为本地固定提交完整源码；Higress 的补读文件从同提交官方 codeload 归档取得。1Panel-Gateway 仍只有产品材料，不能补出不存在的实现证据；其公开特性与 moni 的首版取舍继续见[原分析](1panel-gateway-analysis.md)。

## 1. 公开 usage 偏好与内部计量必须分开

New API 的 `TextHelper` 在模型映射后读取客户 `StreamOptions.IncludeUsage`，另外按渠道能力/全局配置处理上游的 stream options，最后单独保存 `ShouldIncludeUsage`。[compatible_handler.go:42-68](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/compatible_handler.go#L42-L68) `handleLastResponse` 处理客户不需要usage时是否继续下发末帧；`HandleFinalResponse` 可补usage并写DONE。[helper.go:165-204](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/helper.go#L165-L204)

这支持“内部拿到usage”和“下游显示usage”是两个决策，但其默认值与moni不必相同。**校准：** moni上游始终申请可靠计量；客户省略/false不附加usage-only chunk，true正常结束时只附一次。客户不能靠关闭展示关闭收费计量。`choices=[] + usage` 仍是有效计量帧。

## 2. 不能把非零合并或最后一帧当作通用计费规则

New API 的Chat流会保留最后与倒数第二条事件，尝试补取usage；仍缺失时用收集的文本估算并加工具计数，最后返回usage。[relay-openai.go:119-197](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay-openai.go#L119-L197) `ValidUsage` 判定为非nil且prompt/completion至少一个非零；`ResponseText2Usage` 明确调用估算器。[usage_helpr.go:23-34](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/service/usage_helpr.go#L23-L34) 这是其具体兼容/计费选择，不是所有供应商最终量的证明。

Higress观测代码明确按字段存在性维护标量/明细：后值覆盖，缺失保留，明细出现则整图替换，而非跨事件相加。[main.go:957-1016](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/main.go#L957-L1016) 测试用累计 `5+3`、`5+8` 断言最终 `5+8=13`，不是21；另有细节保留/替换测试。[main_test.go:2764-2823](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/main_test.go#L2764-L2823)、[同文件:2831-2915](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/main_test.go#L2831-L2915)。

**校准：** moni区分missing/null/显式0，增加最终/临时/估算/冲突状态；profile决定累计、终态和字段关系。估算不收费，互相冲突的最终快照进入核对；缓存输入和reasoning作为子集校验，不重复加价。财务不直接读观测插件的“最终指标”。

## 3. 分帧、终态和观测容错是三个合同

New API通用scanner按行读取并提取 `data:`，有DONE判定、客户取消、关闭body和等待协程的完整清理路径。[stream_scanner.go:287-350](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L287-L350)、[同文件:173-190](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L173-L190)。其 `IsNormalEnd` 包括EOF，但 `StreamOutcome` 另存是否期待终态、协议结果和错误；不能把某一层“normal”布尔值当成全链路成功。[stream_status.go:60-70](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/common/stream_status.go#L60-L70)、[同文件:209-215](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/common/stream_status.go#L209-L215)。

Higress使用字节framer处理分隔符跨回调，限制的是**未完成尾部**≤1MiB；已经完整的event不受同一上限约束。超限进入RESYNC，丢弃直到下一个分隔符，再继续观测；源码自述它是只读观察者，不阻塞下游。[sse_framer.go:21-53](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/sse_framer.go#L21-L53)、[同文件:80-129](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/sse_framer.go#L80-L129)。分隔符支持LF/CRLF/裸CR及混合边界。[同文件:149-178](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/sse_framer.go#L149-L178)。

对应测试覆盖每个CRLF边界、逐字节回调、完整大回调、溢出恢复和尾部丢弃。[sse_framer_test.go:113-151](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/sse_framer_test.go#L113-L151)、[同文件:186-239](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/sse_framer_test.go#L186-L239)、[同文件:280-328](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/sse_framer_test.go#L280-L328)。panic测试还特意保护观测fail-open，后续事件仍能计量。[main_test.go:3083-3109](https://github.com/higress-group/higress/blob/bde990b4bfc7ecb581338e2d0ae858aaedb2fd69/plugins/wasm-go/extensions/ai-statistics/main_test.go#L3083-L3109)。

**校准：** moni采用完整event级解析，单完整/未完整event都受1MiB限制；超限属于协议/计量失败，不照抄“丢弃再继续”用于客户账单。裸EOF、残缺JSON和流内错误分开记录，DONE不能补造usage或覆盖错误。观测可以容错，资金解释器不能把丢字段变成零费/成功。

## 4. 工具输出不是文本，但仍是本次生成

New API按choice/index/callID去重函数调用名称，遇到finish_reason记录终态。测试构造多段arguments，断言不能按chunk数量重复计数；`tool_calls`结尾标记completed，`content_filter`另外标记业务拒绝。[relay-openai.go:200-235](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/relay-openai.go#L200-L235)、[stream_tool_billing_test.go:11-38](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/channel/openai/stream_tool_billing_test.go#L11-L38)。

**校准：** moni首版只转发客户端函数工具，不执行工具、不自动迭代；托管搜索/文件/代码等额外计费功能拒绝。tool arguments是计费输出，有效内容也包括tool delta；不把“没有自然语言”当成无输出或免费失败。`length`/`tool_calls`是正常终止原因，业务拒绝与渠道故障分开。

## 5. 关闭连接与释放许可必须跟真实资源生命周期一致

New API取消测试通过 `io.Pipe` 模拟静止上游，断言取消后handler返回、上游reader被关闭、后续内容不转发。[stream_scanner_test.go:315-379](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner_test.go#L315-L379)。写预算则按累计字节和最低速率计算deadline；图像测试允许长事件获得数百秒预算。[stream_scanner.go:76-117](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner.go#L76-L117)、[stream_scanner_test.go:695-725](https://github.com/QuantumNous/new-api/blob/e449cc700eb569cdfc2199cb73e97de46944f43c/relay/helper/stream_scanner_test.go#L695-L725)。

**校准：** moni文本首版使用固定有界event/写等待，所有写预算受总deadline约束，不照搬多模态的长预算。取消先cancel/close，再等待reader/writer退出才释放对应许可；结算使用独立有界context，不能因客户context取消而跳过T2。关闭本地body只表示已尝试取消，不保证供应商已经停止收费。

## 6. 验证深度与实施落点

| 层级 | 本轮确认了什么 | 尚未确认 |
|---|---|---|
| 实现 | 上述固定路径的调用顺序、状态、分帧和合并机制 | 全供应商/所有配置路径一致 |
| 测试源码 | 上游写下了边界、取消、工具、累计usage的断言 | 本轮执行通过、生产性能、moni兼容 |
| moni规格 | LLD新增profile、usage状态、交付/收费矩阵、终结证据与清理边界 | 实现、真实PG故障验收、正式供应商合同 |

以上使协议合同更精确，不扩大首版到Responses、原生Claude/Gemini、多模态或网关执行工具。完整校准清单见[设计校准汇总](design-calibration.md)。

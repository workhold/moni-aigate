# 钱包全序声明纯检查验证

2026-11-02，依据 [根冻结合同](../../specs/022-self-service-payments/c0-wallet-sequence-rows-contract.md)。本片在 `internal/fundinglotverify` 新增独立原行检查；不是完整 lot/FIFO 重放或维护准入。源码与最终集成命令身份见 [机器记录](wallet-sequence-rows.json)。

## 已实现与审查

七组投影核 epoch 单行/引用、tenant birth 与 seq0、连续前驱和唯一完整 head、全局键、legacy/new源和journal互斥、原 journal/top-xid 与 event 绑定、event_count 及四项 `big.Int` 合计。两种新退款 kind 归一到同一个 source key；receipt ID 不误当 credit/redemption/capture ID。重复组不选任意行作关系真值，继续枚举各组原行；结果不输出行身份或金额。

输入先做全局 100000 行/16MiB/字段65536B预算，再分配索引和大整数；四种安全错误均返回零结果。新 UUID 严格，旧 TEXT（含空串和空白）、NULL、小数部分全零的 NUMERIC 保留原语义。无数据库、网络、ReadLease/ScanKind/once位、proof、HTTP/main、资金writer或迁移变化。

三路并行工作为实现、独立手算测试及合同/实现语义复核，由根集成。复核新增两个精确边界：新 tenant 不可有 legacy source；context 状态只读取一次再区分取消/超时。后者用受控 context 的第二次 Err 到期确定性复现原错误分类，再修正实现。

## 验证结果及原失败

最终纯包 `go test -race ./internal/fundinglotverify -count=1` 通过，3.899秒。钱包新增16个顶层测试，连同fuzz种子共168个PASS终态；纯包总计899个测试终态PASS。覆盖26个mismatch方向、空业务库、旧/新genesis、所有kind、零额推进、同top-xid多操作、文本原值、BIGINT/UINT64与大整数、scale-zero、全局去重、冲突组置换、资源精确界/超界、中途取消/超时、脱敏和输入不变。

原失败保留：新 tenant legacy 回归先于实现补丁运行时失败，补齐后定向race通过；`ctx.Err()`双读的受控deadline回归先失败，再改为单次读取。该修复使测试context调用数减半，原20000行fixture不再达到第400次取消阈值，首次纯包汇合失败；仅将该测试工作量改为40000行（仍在原100000硬界内），最终纯包完整race通过。没有放宽资源、期限或更改业务准入以通过测试。

短fuzz与最终 `make check` / 明确专库 `make verify-mvp` 的结果以机器记录列出的精确本轮执行为准；先前业务来源版本 `b9da240` 的 [CI run 41442551097](https://github.com/workhold/moni-aigate/actions/runs/41442551097) 已全部成功，不能替代本片精确提交的CI。

最终短fuzz通过，12.757秒、73385次执行、两worker；19张当前Mermaid图均以12.2.0解析/渲染通过，模块图单列无运行调用方的纯序列节点。正文、各图源码与PNG摘要见[架构渲染记录](architecture-wallet-sequence.json)，图像见[模块图](assets/architecture-wallet-sequence-application.png)。

## 有限证明与后续

用例明确展示：共同改写 grant 的 operation/event 金额，或共同给 reserve 增加自洽 issued 声明，都可能通过本结构检查。没有真实业务源、完整分录、opening成员、lot链/桶/分配作为输入，不能从声明相符推出资金正确；删除整个tenant或全部合法尾部也不一定能发现。结果只有 `supplied_wallet_sequence_rows`、RowsConsistent、Scanned/Mismatches，无 Balanced/CoverageComplete/ScanProof/RuntimeProof。

后续完整重放须从真实来源独立推导初态和 FIFO/charge/refund，不从本片声明初始化金额。旧held opening按legacy reserve减legacy terminal集合推导，旧未结后来finish仍按原allocated charged边退款。然后单独冻结pre+basis、protected init/post、epoch与APP接线。没有以本纯片替代C0 opt-in真实维护验证、供应商、容器、1Panel、生产或性能认证；长期58123预览不因本片变更而重启。

最终集成通过：`make check` 7.623秒；明确专库 `make verify-mvp` 977.773秒，前端286项、43个Go测试包race、vet与嵌入构建全部通过。该默认整仓不启用C0 opt-in，不替代真实维护专项。完整验证后仅更新验证正文/机器记录/架构快照，Go实现和测试身份保持。

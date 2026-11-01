# C0-V0-L：输入账本行独立纯检查

2026-11-01。按[根冻结合同§12](../../specs/022-self-service-payments/c0-verifier-contract.md#12-首个有界实施片-v0-l原始账本行独立纯重放根冻结)实现标准库纯包；专项通过。**只证明传入三类账本行的结构与算术一致，不证明数据库已读全、完整来源、lot、资金授权或运行就绪。** 静态reader、同RR全源组合与C0生产接线尚未在本片实施。

实现为[ledger.go](../../internal/fundinglotverify/ledger.go)，测试为[固定边界用例](../../internal/fundinglotverify/ledger_test.go)与[短fuzz](../../internal/fundinglotverify/ledger_fuzz_test.go)。`CheckLedgerRows`直接接收账户、journal和line原始算术投影，返回固定`Scope=supplied_ledger_rows`、`RowsConsistent`和安全计数；无Balanced、CoverageComplete、phase、AsOf或proof。空输入一致不表示数据库为空；后续reader必须自己证明三张表全部EOF，不能分页调用后AND结果。

所有string保持原TEXT语义且核UTF8/NUL，account tenant区分SQL NULL与空TEXT；Balance/SignedDelta为canonical signed int64，全部求和用独立math/big.Int。检查完整平台账户余额、账户形状、自然键重复、缺失父、跨tenant、无line/不平journal及全部实际line；dangling/cross-tenant line仍进入能够归属的算术，不用join丢行。重复主键/坏字面值、超界和取消分别返回固定错误与整个零结果。

硬界为整份输入最多100000行、16MiB输入string字节，不是Go堆/RSS保证。超过后整体拒绝；不提供截断、分页面通过、跳过平台账户或信任调用方已读全的选项。输入不排序、不修改、不保留引用；输出只含值。

## 实际验证

以下均在仓库根运行，退出码0：

| 命令 | 结果 |
| --- | --- |
| `go test -race ./internal/fundinglotverify -count=1` | 最终源码通过，包输出1.615s；含固定测试和fuzz seeds |
| `go vet ./internal/fundinglotverify` | 通过，无输出 |
| `go test ./internal/fundinglotverify -run '^$' -fuzz '^FuzzLedgerRowsClosedFailures$' -fuzztime=3s -parallel=2` | PASS，381041次，40个新interesting输入，包输出4.440s |
| `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./internal/fundinglotverify` | 仅输出本包，依赖为标准库 |
| `rg -n 'github.com/moni-ai/moni-aigate/internal/fundinglotverify' --glob '*.go' cmd internal scripts` | 仅本包两个外部测试文件import；无生产import |

短fuzz后只增加了一个验证“字面值/主键检查完成后再取消”的确定性测试，纯实现及fuzz源码未变；之后重新执行最终race/vet如上。未把短fuzz执行次数当性能基准。

固定测试覆盖：

- 手算三种clearing入账、reserve、finish和消费退款的6账户/6journal/13line算术；这不意味着这些fixture已证明业务来源。
- 空输入、零账户、零/单/两条零金额journal、自然键三次重复只记一个关系组；全部十方向精确计数。
- line单缺/双缺parent、跨tenant、NULL与空TEXT、原空/Unicode/非UUID/大小写身份、平台余额更改，以及孤儿仍参与算术。
- MIN/MAX、非法前导零/负零/指数/小数/越界和全部string字段UTF8/NUL；重复主键即使相同内容也拒绝；错误无部分结果。
- 多次MIN/MAX的中间和超int64后回归0；两个MIN在int64回绕会假平的输入仍判两个账户不符；journal大整数非零和同样拒绝。
- 恰100000行和+1、恰16MiB和+1（含多字节UTF8）、非nil tenant每次出现的字节计入上限；无截断。
- 开始取消、已过deadline、预算检查中取消、字面值/主键完成后的算术阶段取消和最终返回边界取消，全部零结果。
- 原slice/tenant指针不变、输入顺序重排结果相同、返回后输入变化不影响旧结果，16个并发稳定读取goroutine通过race。

## 源码指纹与后续

| 输入 | SHA-256 |
| --- | --- |
| `internal/fundinglotverify/ledger.go` | `dcccbb5f02e07f685429140461e5d6ff32df5eed08a9c44556e89892cb74f74a` |
| `internal/fundinglotverify/ledger_test.go` | `67adf812a9b3f5dc600855e02e9576fff0b013354a392499b44f981d11846197` |
| `internal/fundinglotverify/ledger_fuzz_test.go` | `2d540d2c2979111307a423817e241ee0dfe29b6971064a6c3f7b3577ffcbdacb` |
| `specs/022-self-service-payments/c0-verifier-contract.md`（本片冻结时） | `6c8084928f04300f41a35334b61b4ca06a12e7d885a3009c0d7f8cc03a0c046d` |

本片没有DB/PG、角色/catalog、source生命周期、同tx/EOF、普通main或浏览器验收；没有修改旧VerifyLedger/VerifyFunding/Rebuild、迁移、生产import或用户预览。下一片需另冻结caller-owned RR静态ledger reader并真实PG验证全行/128行keyset/EOF/权限和错误边界；其成功仍只证明ledger行层。完整独立source/Key/采购/PB/B、lot重放、Permit/epoch/runtime proof与C0资金接线继续按各自合同收口。统一架构正文、变更记录和manifest由根汇总审阅后同步。

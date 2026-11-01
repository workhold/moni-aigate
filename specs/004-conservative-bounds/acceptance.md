# 004 验收

| ID | 判据 |
|---|---|
| CB-01 | 三协议input=context、output独立上限能发布/解析；input>context或output>context拒绝。 |
| CB-02 | 省略输入上界时创建结果为完整context，不因默认或最大输出减少。 |
| CB-03 | PG context120、input120、output20、各1微元/token：R140，实际115+3=118，收费118/释放22，无残留冻结，账本平衡。 |
| CB-04 | 原授权/撤权/硬预算/并发/输出约束与协议回归通过；不改旧profile或资金历史。 |

直接证据：`internal/provider/bounds_test.go`、`internal/billing/bounds_integration_test.go`、`TestModelDefaultInputBoundUsesWholeContext`；实际执行记录汇入[MVP状态](../../docs/verification/mvp-status.md)。

2026-10-30，CB-01至04在最终`make verify-mvp`中通过：12项前端测试、strict TS/Vite、真实PG全Go race、vet及最终嵌入编译均成功。provider 3.588s、billing 23.830s、server 128.890s为包测试执行时间，不是请求延迟。旧测试对输入/输出之和的断言已按004改为检查单独输入越界，新三协议测试同时验证独立上界与单独输出越界；不修改旧profile或资金历史。供应商认证不在此结论内。

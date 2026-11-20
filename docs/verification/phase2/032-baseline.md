# 032 slice 2.1 baseline implementation status

2026-11-20。本文只记录测量入口和局部验证；不是固定资源性能报告。

## 已实现入口

- `internal/testsupport/mockupstream` 的 receipt 增加 `first_event_at`、`first_content_at`、`completed_at` 以及进程内单调时钟产生的 `first_event_after_us`、`first_content_after_us`。runner 只使用数值微秒间隔做 PERF-03 逐请求减法，不对反序列化 wall-clock 时间戳做差。这些时间只通过既有 `--allow-directives` 保护的接收记录暴露；候选 secret 不进入报告。
- mock 的 `slow_ttfb` 与 `event_interval_ms` 通过可注入 wait hook 做行为测试，测试不依赖真实睡眠或 wall-clock 延迟阈值。
- `scripts/phase2-baseline` 提供部署态 OpenAI Chat SSE 负载入口。它只调用正常鉴权网关，不提供绕过授权、预占、派发、最终证据、结算或审计的开关。
- 配置固定记录精确 commit/tree/build、四类资源及连接配额、无秘密 mock 摘要、并发/活动流、连接/TLS/HTTP、单时钟源、预热/采样和至少三轮顺序。
- `samples.jsonl` 保留全部预热与采样请求（含失败）、客户端 TTFB/TTFC/full、receipt、逐请求 mock 等待及 extra 延迟；报告记录文件 SHA-256。
- 报告沿用 `measurement_version=2` 的 nearest-rank 分位数与有效内容边界，新增 `phase2_baseline_version=1`。额外延迟先在同一样本内扣除 mock 等待，再聚合。
- 活动流峰值由 mock 实际接收/完成区间计算；不完整 receipt、连接身份不符、缺原始样本、缺 CPU/RSS 或 PG correctness 证据都会使报告保持 `not_accepted`。

配置、资源样本和 correctness 文件格式见 `scripts/phase2-baseline/README.md` 及同目录示例。示例不是运行证据。

## 局部验证

执行范围仅为：

```sh
go test -race ./internal/testsupport/mockupstream ./scripts/phase2-baseline
go vet ./internal/testsupport/mockupstream ./scripts/phase2-baseline
```

这些测试覆盖确定性等待与 emission 时间、六协议既有 mock 合同、TTFC 边界、逐请求减法、活动流区间、runner HTTP 样本、报告独占创建及商业链路 bypass 拒绝。它们不启动 VM、Compose、真实 PostgreSQL 或持续负载。

## 未执行验收边界

- 未运行固定 4 vCPU/8GB 应用、固定配额 PostgreSQL、100 实际活动流、60 秒预热/5 分钟采样及三次正式重复。
- 未生成真实 `report.json` 或逐请求验收样本；模板和单元测试不能替代它们。
- 未在同条件下运行 Bifrost 或 Rust LiteLLM，未比较、未宣称接近或领先。
- 未运行直接上游、六协议性能矩阵、工具、长短流、冷/热连接、TLS 变体或慢客户端矩阵。
- 未执行完整 029 商业回归、真实供应商或生产认证。性能结论仍为 `not_adjudicated`。

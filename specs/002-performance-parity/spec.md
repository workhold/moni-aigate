# 性能对齐规格

2026-10-30。来源：开发者要求性能接近 Rust 版 LiteLLM 与 Bifrost。本迭代继承 [001 商业正确性规格](../001-commercial-mvp/spec.md)，不以取消鉴权、持久资金或派发检查换取通过。

状态：已校准计时口径并实现本地客户端观测；竞品运行、固定资源持续负载和性能对齐仍未验收。HLD 原有 4 vCPU / 8GB、100 并发、额外首字节 p95≤50ms 目标继续有效；它不足以单独证明与竞品接近。

## 1. 固定参考

| 目标 | 固定源码 | 实际入口与范围 |
|---|---|---|
| LiteLLM Rust native gateway | `BerriAI/litellm@feda51453a3c468d04b608da4a9a972d3b365ce8` | `litellm-rust` workspace 的 `litellm-gateway`，Axum/Tokio；release 构建。不能以 Python Proxy 或 Python Rust bridge 结果替代 |
| Bifrost HTTP gateway | `maximhq/bifrost@dc28f7011d978f0cdaef68758b8aad33a7612a5f` | Go HTTP gateway 的 release 构建；固定插件与配置清单 |
| moni-aigate | 当前独立实现 | 正常 release 构建；正式比较必须保存源码归档 SHA256 / Git commit、构建参数和运行配置摘要，不能只有 `dev` 版本号 |

源码与宣传口径核查见 [参考证据](../../docs/research/performance-references.md)。Rust gateway 的用户/团队/预算和持久 spend accounting 在该版本并非完整可用的商业链路；Bifrost 宣传的 11µs 排除 JSON marshalling 与 HTTP calls，且是 average。两者都不能直接作为完整商业链路 p95 验收值。

## 2. 指标合同

| ID | MUST 行为 |
|---|---|
| PERF-01 | 同时报告客户端首响应正文 byte（TTFB）、首个有效内容事件（TTFC）、完整结束时间，不能把 headers、heartbeat、role-only、空 delta、usage-only 或 DONE 当作首有效内容 |
| PERF-02 | TTFC 按完整 SSE 事件含非空文本/拒绝说明或工具函数名/参数增量识别；工具 ID/index 单独出现不算。它是客户端可观察代理指标，不是模型内部 tokenizer 的首 token 时刻。真实模型端到端 TTFT 可用该事件作为操作性口径，必须说明协议与供应商生成时间仍包含其中 |
| PERF-03 | 对每个请求保留客户端耗时和 mock 从接收到内容发出的间隔；额外内容延迟逐请求减去该 mock 间隔再计算分位数。直接上游另测；不能以两个 p95 相减作为逐请求额外开销，也不能把多个阶段 p95 相加 |
| PERF-04 | 保留 p50/p95/p99、有效样本数、错误/拒绝、完成 goodput、上游实际 POST 数、CPU/RSS、资源配额、连接池/TLS/HTTP版本、重试/插件/缓存配置与逐请求原始样本；失败请求不能静默丢弃以美化延迟 |
| PERF-05 | 直接上游、Rust native、Bifrost、moni 使用同一 mock、请求/流形状、资源、负载器、连接条件；分别测冷/热连接、HTTP/TLS、文本/工具、短/长流、慢客户端。只支持的共同协议可直接比较，缺失功能标明 |
| PERF-06 | moni 完整商业链路始终包含权限、硬余额/Key限额预占、持久派发、最终证据/结算、审计。可另建基础转发实验，但不能把该结果标为完整产品性能；不为基准提供生产绕过资金开关 |
| PERF-07 | 正确性门禁先于性能：无超扣、残留冻结/许可、无授权派发、隐式 POST 重放；DB/实例失锁仍关闭准入。性能优化须通过对应 001 并发/故障回归 |
| PERF-08 | 不覆盖旧报告。报告显式标明测量版本；单次突发、内核微基准和持续负载分开，缺少竞品运行结果不得宣称接近或领先 |

## 3. “相近”的工程判据

先采用以下工作阈值，作为下一轮优化和报告的明确目标；不是已验证结果或对外 SLA。若同口径参考显示该阈值不合理，须修改本规格并给出原始证据，不能修改历史数据。

- 共同基础代理实验：同负载、同功能配置下，moni 的逐请求额外 TTFC 的 p95/p99 分别不超过每个参考的 `max(1.25 × reference, reference + 1ms)`；稳定成功 goodput 至少为参考的 90%。为微小时间设置 1ms 容差，避免用微秒宣传数字作不可能复现的比例判断。
- 完整商业链路实验：保留全部商业门禁，与上述参考在各自已实现治理配置下分别出表，公开功能差异；同样报告前述比值和 goodput。只有完整 moni 也满足相近阈值时，才能称“完整产品性能接近”；基础实验通过不能替代。
- 固定 4 vCPU / 8GB app、同地域独立且固定配额 PostgreSQL，100 个实际活动流；moni 完整链路额外 TTFB p95≤50ms 仍是最低门槛。并发客户端数不能代替上游实际活动流数。
- 每项预热至少 60s、稳定采样至少 5min，至少 3 次独立重复并轮换运行顺序；每次分别裁决，报告跨轮波动，不能只取最快一轮。goodput只计合法终结与成功状态；错误/拒绝率须为0才参与该档性能通过，另测过载响应。
- 小于100个样本的p99只能作功能诊断，不能作为稳定性能结论。容量、内存和慢客户端边界必须分别验收。

首个目标是测量可信与消除派发前无效串行等待，然后逐项逼近竞品；不能仅按 Go/Rust 语言名称推断结果。

## 4. 当前证据与边界

- 旧测量完整链路额外 TTFB p95=122.57ms，未达50ms。旧程序只读第一个正文 byte，未单独观测有效内容。
- 本次 `measurement_version=2` 增加独立客户端 SSE 识别、role-only 前帧、可选25ms内容等待，分别记录 TTFB/TTFC 与额外延迟；只是本机 HTTP 短流突发。最新原始文件和解释见 [容量记录](../../docs/verification/capacity.md)。
- 正式三方持续比较、目标资源配额、生产 TLS 连接成本、长流/慢客户端性能尚未执行；不得声称通过。

设计和任务见 [plan.md](plan.md)，裁决见 [acceptance.md](acceptance.md)。

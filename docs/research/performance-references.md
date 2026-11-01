# Rust LiteLLM / Bifrost 性能参考核实

访问与源码核查日：2026-10-30。只读源码/文档，没有构建或运行竞品，没有真实付费模型请求。用于 [SDD 性能迭代](../../specs/002-performance-parity/spec.md)。

## LiteLLM Rust native

固定 `BerriAI/litellm@feda51453a3c468d04b608da4a9a972d3b365ce8`。

- [原生 gateway README](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm-rust/crates/gateway/README.md)：公开 `litellm-rust` Cargo workspace，入口 `litellm-gateway`，配置 `LITELLM_CONFIG/HOST/PORT`。本版本 Cargo package `0.1.0`，使用 Axum/Tokio。
- [Dockerfile](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm-rust/crates/gateway/Dockerfile)：公开源码 release 构建。正式比较应用 `cargo build --release --locked --manifest-path litellm-rust/Cargo.toml -p litellm-gateway`；不能以 README 方便开发的 debug `cargo run` 比较。
- [inference routes](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm-rust/crates/gateway-inference/src/lib.rs)：有 `/chat/completions` 与 `/v1/chat/completions`，可对接同一文本/SSE mock。
- [management README](https://github.com/BerriAI/litellm/blob/feda51453a3c468d04b608da4a9a972d3b365ce8/litellm-rust/crates/gateway-management/README.md)：virtual-key create/get/revoke 是 scaffold，没有 HTTP routes、DB adapter、生产 in-memory store或CLI wiring；用户/团队/模型/预算策略尚未表示。gateway README也明确保留Python配置字段不代表已实现对应服务，列出spend accounting等未实现范围。

因此“LiteLLM只有Python”和“Rust代码不公开”均不符合该固定版本。也不能把其Python Proxy的商业能力默认算进原生Rust入口。此次将原生gateway作为用户所述Rust LiteLLM的默认参考，不混用bridge/SDK/原生不同入口的性能数字。

## Bifrost

固定 `maximhq/bifrost@dc28f7011d978f0cdaef68758b8aad33a7612a5f`，已审阅核心与HTTP gateway使用Go，不能称Rust Bifrost。

- [benchmark总览](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/docs/benchmarking/getting-started.mdx)：小于15µs说的是average overhead，使用mock OpenAI，并非真实模型首token时间。
- [t3.xlarge详情](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/docs/benchmarking/t3.xl.mdx)：4vCPU/16GB，5000RPS；11µs total overhead明确**排除JSON marshalling与HTTP calls**。文中的平均端到端延迟1.61s也是其特定实验结果，不是本项目的参考p95。
- [自测指南](https://github.com/maximhq/bifrost/blob/dc28f7011d978f0cdaef68758b8aad33a7612a5f/docs/benchmarking/run-your-own-benchmarks.mdx)：提供独立 [bifrost-benchmarking](https://github.com/maximhq/bifrost-benchmarking) mocker/hitter。若后续复用工具，须另固定其commit并显式纯mock配置；默认未指定provider的路径可能包含真实OpenAI，不能直接启动默认负载。

这些数字是上游自己的文档声明，本轮没有独立复现实验。不得与moni本机完整商业链路的客户端额外TTFB p95=122.57ms直接比较。后续统一测试网络、JSON解析、鉴权、路由与持久成本的计入范围，再讨论差距。

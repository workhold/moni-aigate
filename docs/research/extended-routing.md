# 扩展研究：配置快照、语义路由与会话切换成本

状态：DRAFT / 独立实现的设计输入；访问日：2026-10-30（Asia/Shanghai）。本轮从 GitHub Repository/Search API 发现并核验项目，固定默认分支 SHA，再读取 README、LICENSE、目录树与少量关键源文件。未运行第三方程序、模型、测试或性能基准，也未将源码迁入产品。

## 1. 项目身份、维护状态与许可

| 项目 | 固定源码快照 / 提交时间 UTC | 本次访问的维护状态 | 许可与适用定位 |
|---|---|---|---|
| [TensorZero](https://github.com/tensorzero/tensorzero) | `62eb8f63e8ec62018d70420dbf1a8c5d1c026315`；2026-06-04T14:44:10Z | Repository API `archived=true`，`pushed_at=2026-06-11T01:48:44Z`；不能按仍持续维护的新组件推荐 | 根 [LICENSE](https://github.com/tensorzero/tensorzero/blob/62eb8f63e8ec62018d70420dbf1a8c5d1c026315/LICENSE) 为 Apache-2.0；仅作为历史设计参考，不因存量 README 的性能/路线声明推断当前服务状态 |
| [Plano](https://github.com/katanemo/plano)（旧地址 ArchGW） | `b13ddd632bde1fea7678386c345665ea5fa671af`；2026-10-24T18:59:43Z | `katanemo/archgw` Repository API 实际返回 canonical `katanemo/plano`；`archived=false`，`pushed_at=2026-10-24T19:03:25Z` | 根 [LICENSE](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/LICENSE) 为 Apache-2.0；开源代理与默认托管路由模型的使用条件须分开 |
| [RouteLLM](https://github.com/lm-sys/RouteLLM) | `0b64fdafe049e596a3f5657c219329f24af24198`；2024-08-10T19:10:15Z | `archived=false`，但 Repository API `pushed_at` 仍为 2024-08-10；只据本次可见字段说明长期未推送，不断言停止维护 | 根 [LICENSE](https://github.com/lm-sys/RouteLLM/blob/0b64fdafe049e596a3f5657c219329f24af24198/LICENSE) 为 Apache-2.0；研究型模型路由/评估框架，不是商业钱包平台 |

维护状态是访问当日 API 元数据，不属于固定源码提交内容，可复查：[TensorZero API](https://api.github.com/repos/tensorzero/tensorzero)、[ArchGW 旧地址 API](https://api.github.com/repos/katanemo/archgw)、[RouteLLM API](https://api.github.com/repos/lm-sys/RouteLLM)。`updated_at` 不等于代码推送日期；默认分支快照也不等于推荐部署版本。模型权重、数据集及托管服务条件不能仅由仓库代码许可证推定。

## 2. TensorZero：配置可追溯和实验边界，而非直接加入依赖

README 将其定位为 Gateway、观测、评估、优化和实验平台，宣传延迟/效果等指标。本轮没有复测这些指标，且仓库已经归档，故不把它当作新项目的运行依赖推荐。

### 真实源码机制

1. **配置快照与请求结果关联。** `ConfigSnapshot` 将配置和模板保存为可序列化快照，以排序后的配置/模板计算确定性哈希，供历史配置追踪和结果关联。实现 `new` 转换 StoredConfig、生成排序 TOML，再计算 hash。[config_snapshot.rs:9–44](https://github.com/tensorzero/tensorzero/blob/62eb8f63e8ec62018d70420dbf1a8c5d1c026315/crates/tensorzero-core/src/config/snapshot/config_snapshot.rs#L9-L44)、[同文件:83–105](https://github.com/tensorzero/tensorzero/blob/62eb8f63e8ec62018d70420dbf1a8c5d1c026315/crates/tensorzero-core/src/config/snapshot/config_snapshot.rs#L83-L105)。这说明了如何把“当时运行的策略”变成可查事实；不代表照存完整配置/模板就符合 moni 的客户隐私边界。
2. **配置加载时阻止无效路由，候选与 fallback 分开。** 模型配置检查候选非空、provider 去重且真实存在；静态实验只在 active variants 与显式候选的交集中加权，交集无可用项再按顺序找 fallback。[model.rs:3665–3702](https://github.com/tensorzero/tensorzero/blob/62eb8f63e8ec62018d70420dbf1a8c5d1c026315/crates/tensorzero-core/src/model.rs#L3665-L3702)、[static_experimentation.rs:147–189](https://github.com/tensorzero/tensorzero/blob/62eb8f63e8ec62018d70420dbf1a8c5d1c026315/crates/tensorzero-core/src/experimentation/static_experimentation.rs#L147-L189)。正常候选权重和故障备用顺序不是同一个概念。

另读 `model.rs:906–1003`：模型依 routing 顺序尝试供应商，保留 provider 错误，存在单供应商/模型总时限和失败响应汇总。这是可解释性的启发，不能直接照用“所有供应商错误都换下一个”的语义；moni 仍要求可信未消费才透明重试。

### moni-aigate 的有限采纳

- **首版**：策略/能力/价格分别版本化，发布前拒绝空池、重复候选和悬空引用；请求保存版本 ID 或不含秘密的配置摘要。新配置校验失败时旧配置继续生效，不能发布半成品。
- **后续**：独立的评估数据集、策略版本与实验结果关联；先离线回放再小流量验证。默认不存正文，不引入其 ClickHouse、模板/优化与实验全栈，也不因归档仓库仍可读取就当作可维护的新依赖。

## 3. ArchGW → Plano：质量选路与切换成本分开

本次实际读取 README、`common/routing.rs`、`handlers/llm/session_router.rs`、路由 API 文档、模型别名及偏好配置示例。README 说明代理基于 Envoy，默认 Plano 路由/编排模型提供美国中部托管体验；生产可本地运行模型或取得相应 API Key。[README:32–40](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/README.md#L32-L40)。开源代码不等于不需要额外路由模型、计算资源或外发请求。

### 真实源码机制

1. **语义意图和实际执行模型之间还有一层决策。** `session_router` 明确质量 router 先给候选，之后根据会话缓存暖态和累计切换开销决定是否保留已使用模型；读取 binding 时同时传 session 与 tenant，避免把 session 标识直接当全局身份。[session_router.rs:1–23](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/crates/brightstaff/src/handlers/llm/session_router.rs#L1-L23)、[同文件:328–374](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/crates/brightstaff/src/handlers/llm/session_router.rs#L328-L374)。
2. **切模型不只比较每百万 Token 单价。** 实现估计上下文重读的切换成本，比较累计开销与会话 baseline 上限，超出则保留 warm anchor，并记录 over-cap 理由和可选反事实候选。[session_router.rs:454–526](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/crates/brightstaff/src/handlers/llm/session_router.rs#L454-L526)。该段也明确缺价格时 fail-open 允许切换，这是它的质量/成本策略，不能作为 moni 严格收费准入规则照用；其 `f64` USD 切换预算不是客户钱包。

产品配置还区分固定模型、模型别名和路由偏好。模型别名为稳定名称映射目标；偏好示例用自然语言区分“理解代码”和“生成代码”。[别名配置:58–91](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/demos/llm_routing/model_alias_routing/config_with_aliases.yaml#L58-L91)、[偏好配置:8–24](https://github.com/katanemo/plano/blob/b13ddd632bde1fea7678386c345665ea5fa671af/demos/llm_routing/preference_based_routing/config.yaml#L8-L24)。这些配置说明不能替代对语义分类准确率的验证。

### moni-aigate 的有限采纳

- **首版**：固定逻辑模型/能力、配置版本和明确别名。相同逻辑模型内切账号，不暗中把客户购买的模型换成更弱模型；继续实行保守的资金预占和失败分类。
- **后续**：若推出显式 `auto` 模型套餐，语义 selector 只提出候选，授权、能力、地域、价格和成本准入再决定可否执行；保存提案/最终选择/否决原因。会话绑定必须包含 tenant，评估切换时的完整上下文重送、缓存损失、工具/推理状态兼容和额外路由模型成本。
- 不默认调用美国托管路由服务，不把“更便宜的模型单价”宣传为整个会话一定更便宜；也不把路由文档中普通 429/5xx fallback 建议当成无消费证明。

## 4. RouteLLM：语义路由要用自己的业务数据校准

本次读取 README、`controller.py`、`routers/routers.py`、`calibrate_threshold.py` 与 `evals/evaluate.py`。README 的“最多节省85%、保留95%效果”来自其模型/基准声明，不是本项目实测，也不能外推至现在的模型组合。

### 真实源码机制

1. **质量阈值决定强/弱模型，路由会改变实际模型身份。** Router 按预测分数与 threshold 比较，返回 strong 或 weak；Controller 校验 threshold 在0到1，解析 `router-名称-阈值`，替换请求 model 后调用底层 completion。[routers.py:32–45](https://github.com/lm-sys/RouteLLM/blob/0b64fdafe049e596a3f5657c219329f24af24198/routellm/routers/routers.py#L32-L45)、[controller.py:79–111](https://github.com/lm-sys/RouteLLM/blob/0b64fdafe049e596a3f5657c219329f24af24198/routellm/controller.py#L79-L111)、[controller.py:137–170](https://github.com/lm-sys/RouteLLM/blob/0b64fdafe049e596a3f5657c219329f24af24198/routellm/controller.py#L137-L170)。它不等同于“同模型故障备用渠道”。
2. **阈值依赖样本分布，代码也暴露适用边界。** calibration 按目标 strong-model 占比取分数分位数；Controller 实际只读最后一条 message，注释承认当前 routers 在首轮数据训练，多轮仍需研究。[calibrate_threshold.py:50–58](https://github.com/lm-sys/RouteLLM/blob/0b64fdafe049e596a3f5657c219329f24af24198/routellm/calibrate_threshold.py#L50-L58)、[controller.py:105–111](https://github.com/lm-sys/RouteLLM/blob/0b64fdafe049e596a3f5657c219329f24af24198/routellm/controller.py#L105-L111)。README 同样提醒实际流量分布不同会改变模型调用比例；MF/相似度路由还可能需要额外 embedding 调用，不能忽略成本、隐私和时延。

### moni-aigate 的有限采纳

- **首版**：把固定模型和未来 `auto` SKU 在产品与计价上分开；保留路由原因和实际模型，不宣称已经拥有语义调度或质量保证。
- **后续实验门槛**：独立批准的业务样本、任务类型/多轮/工具调用分层，比较质量、全链路延迟、客户可见费用、平台全部尝试与路由器成本；记录模型对、路由器/阈值版本和数据集版本。训练/校准与验证样本分离，达不到质量门槛或路由服务不可用时退回明确的固定模型策略，而不是静默降低质量。
- 维护活动较旧、示例依赖/接口需要重新核实，不将该旧快照直接加入首版生产依赖，也不把研究性阈值照搬到客户流量。

## 5. 建议合入 HLD/LLD 的最小变更

| 设计点 | 可立即加强的 v0.1 合同 | 后续独立规格 |
|---|---|---|
| 配置发布 | 先完整校验、版本不可变、原子切换；请求绑定实际配置版本；失败保留旧版 | 配置效果回放、灰度实验与回滚评估 |
| 调度边界 | 逻辑模型授权和售价先明确，同模型账号池切换不改变客户购买的质量档 | 显式auto套餐、语义提案与商业准入分开 |
| 观测与隐私 | 保存候选/过滤/最终选择原因，不默认存正文；财务事实与调度估计分开 | 可审计的采样数据、离线质量与会话成本评估 |
| 预算 | 客户收费用既定整数账本、可靠usage、预占与唯一结算 | 单列路由模型费用、上下文切换与缓存损失预算，不把反事实节省当真金白银 |

本研究不会把 TensorZero、Plano 或 RouteLLM 的全部功能带进首版。收益主要是让既有设计的配置可复现、调度可解释，并为未来动态路由建立可验证门槛。

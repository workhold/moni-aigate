# 041：盈利商品与混合执行平台总体设计（R0）

2026-11-23；状态：**设计候选，未实施**。本文把“精准把控成本、在质量约束下动态选择供应商/物理模型并赚取可审计差价”确立为后续产品目标，把 035/038 的“逐步全部内收”修订为“核心自研、长尾复用、选择性内收、允许永久混合”。

实现基线为 main `d51bd241da61e06890c44f2f143caf0e2df2b627`，其中 032/033 已合入。R0 只冻结产品、数据、运行、权限、故障和验收边界，不新增 SQL、运行 API、前端页面、部署拓扑或商业资格。New API/Sub2API external commercial 继续由现有 `release_external_commercial_unsupported` 拒绝。

## 1. 商业目标与范围

目标不是再做一个 New API，而是提供可公开解释的服务商品，在授权、隐私、质量、可靠性和客户合同约束内选择成本更低的供应商或物理模型，使客户节省与平台贡献毛利同时可观察、可核对、可回退。

首版利润公式：

```text
账单确认贡献毛利 = 净实际付款基准 - 全部账单确认供应商成本
```

只有合同价成本时只能显示**暂估贡献毛利（provisional，as-of 成本版本）**，不能称已实现实际利润。消费退款按原结算分配边冲减付款基准；退款币再次消费不得重复确认同一笔现金收入。支付手续费、税费、基础设施、客服和其他运营费用未分摊前，只能称贡献毛利或毛利，不能称净利润。路由前的最低毛利是保守准入指标，不等于结算或账单确认后的真实现金利润。

范围：

- 文本固定模型、同模型多供应商、品牌动态、任务优化和通用 Auto 商品；
- direct/New API/Sub2API 三类叶子执行器；
- 一个 aigate Vue SPA 统一商品、执行、路由、请求、利润和审计管理；
- 逐供应商/模型/协议/profile 的 mock、真实资格和生产资格分离；
- 选择性内收，不设置整体删除 New API/Sub2API 的目标。

非目标：

- R0 开放 external commercial、真实供应商、真实支付或生产流量；
- 将固定 K3 等原厂模型名称用作未披露的动态替代品；
- 用额外 LLM 读取客户正文做任务分类、质量评估或影子双生成；
- 媒体、异步任务、Realtime 的多候选动态路由；
- 通用插件/脚本/MCP、签到/邀请/分佣、下游客户/钱包/quota/订单导入；
- 通用多币种/自动 FX、订阅账号成本分摊、HA、自动学习/付费流量探索的同期实现；**USD/CNY 付款、供应商成本和保守估值是商业首发必需范围，不属于无限后置项**。

## 2. 当前事实与必须修复的缺口

1. 032/033 已进入当前代码：版本化熔断、首事件、精确缓存、物理模型、候选决策、auto、统计排序、粘性、最低毛利字段和只读回放均有本地完整 029 证据；固定资源性能、真实供应商和生产仍未完成。
2. 2026-11-25 前的 `dynamic-v1` 把 micro-COIN 预占与 micro-CNY 供应商成本直接相减。`dynamic-v2` 把已付 CNY 收据的实付金额按本次分摊向下取整，再与供应商 CNY 上限比较。赠送不按牌价记成现金，也不抹掉同笔里的已付部分。第二次尝试只保留尚未证明无消费的供应商上限。付款基准唯一权威：带 P0 `request_economics_snapshots` 的请求只消费 Reserve 冻结快照（含 USD/CNY FX）；无快照的普通动态路由只消费网关请求时的已付 CNY 分摊查询，两条路径互斥。`quality_score` 不能抬高质量档，场景质量证据仍未实施。USD 付款在普通动态路由失败关闭，交叉估值仍只在 P0 快照路径。
3. 逐候选最低毛利检查没有覆盖两次 outer attempt 的累计采购成本。两个候选分别通过，不证明“第一次实际/保留成本 + 第二次保守成本”仍满足请求级利润下限。2026-11-25 起 `dynamic-v2` 普通动态路由在第二次尝试带上尚未证明无消费的第一次供应商上限（缺失时 `cumulative_cost_unavailable` 失败关闭），P0 商品由服务端在 dispatch 按冻结快照复核累计门禁；实际成本更正与账单确认口径仍属 041 后续。
4. `quality_tier` 是经营档位，`quality_score` 未参与完整场景质量约束；HTTP 200、成功率和延迟不是答案质量。场景质量证据尚不存在。
5. 现有 thin external relay 主要覆盖 OpenAI-compatible deterministic fixture；没有不可变 inner pool 版本、最终账号安全伪名、逐 inner dispatch 摘要、实际派发次数或生成前版本匹配拒绝。
6. `test_only` 是发布意图，不是客户请求准入或网络隔离。真实凭据测试必须另用独立环境或服务端 verification subject。
7. 038 台账、032/033 任务和架构部分历史行仍保留旧状态；后续实施必须按当前源码和验证记录修正，不能重复开发已合入能力。

## 3. 权威职责与复用边界

| 能力 | 权威/实现位置 | 边界 |
| --- | --- | --- |
| 公网入口、租户、用户、成员、客户 Key、MFA | aigate | 客户 Key/Cookie 永不发送给下游；New API/Sub2API 不建立客户账号体系 |
| 商品、公开披露、逻辑目录、零售 profile、售价 | aigate | 下游模型列表只生成发现草稿，不自动上架或授权 |
| 最终路由、质量/隐私/成本/毛利过滤、粘性与回放 | aigate 033 | 下游不得再次选择供应商、物理模型或地域 |
| 高流量、低延迟、精细控本 profile | aigate direct | 官方 API、已知成本和账号级控制优先；direct 不自动取得真实资格 |
| 长尾供应商协议、模型映射、多 Key/渠道、发现 | New API | 固定为内部执行叶子；不复用客户、quota、钱包、支付、订阅和 React 后台 |
| 授权订阅账号、OAuth/session 刷新、账号亲和、专用编码服务 | Sub2API（条件启用） | 仅在 direct/New API 无增量能力且条款允许时启用；普通 API Key 转发不部署 |
| 供应商账号秘密 | direct 由 aigate；外部由对应下游 | aigate 外部只保存机器 Token、不透明账号 ID 和安全摘要，不导入明文秘密 |
| 钱包、硬预占、唯一结算、采购成本、退款、审计 | aigate | 下游 quota、余额、订单和日志不是客户商业事实 |
| 原始 usage/成本/任务状态 | direct 或外部执行器产生证据 | aigate 校验、归属和持久化；字段完整不自动等于资格 |

Sub2API 固定参考为 `Wei-Shaw/sub2api@10ba8f9b4dc2e6023d4a9879ab77b21841babd10`。Claude Code、Codex 等自购或获授权订阅账号的接入不代表允许多人共享、自动化转售或规避供应商限制；必须逐服务核对条款。固定月费、共享额度和超额费在没有单请求可审计成本分摊合同前一律 `cost_status=unknown`，不得进入利润敏感商业池。

## 4. 商品体系

### 4.1 不可变商品版本

逻辑 `ServiceOfferVersion` 绑定：商品类型、公开说明版本、固定/动态实际模型规则、任务政策、质量政策、披露政策、经济版本、粘性/延迟/隐私约束、节省参照、route revision、零售 profile 和 price version。

商品发布在同一 CAS 事务中创建新商品版本和 route revision，校验完整候选矩阵与公开解释；在途请求永远使用原快照。复用现有 model/profile/price/route/binding/channel/version/physical model，不新建包含重复费率、绑定或成本的大 Provider 表。`payment_skus` 只表示充值套餐，与推理 Service SKU 分域。

### 4.2 五类商品

| 类型 | 合同 |
| --- | --- |
| `exact_model` | 固定 canonical actual model；只能失败，不得跨模型替换 |
| `same_model_multi_provider` | 实际模型与质量档不变，只切换合格供应商/账号 |
| `branded_dynamic` | 独立动态商品；公开候选家族、任务映射、质量和降档规则 |
| `task_optimized` | 对明确任务集合执行逐任务质量、隐私、延迟和成本选择 |
| `generic_auto` | 多任务、多模型 Auto；只有各任务样本与未知类安全默认齐备后开放 |

### 4.3 K3-Dynamic 示例

K3-Dynamic 是独立逻辑商品，不是固定 K3 别名。公开页和购买前说明必须写明“多模型动态服务、并非固定 K3、可能由已公布候选如 DeepSeek 承载”，并公开候选范围、质量下限、数据政策、降档策略和单一零售价。

首片固定 `coding_non_reasoning`，不允许降档，不支持的 reasoning 参数直接拒绝。响应 `model` 回显 `k3-dynamic`；客户请求详情显示实际服务物理模型、服务档位和 `tier_changed`。是否增加响应 header 另行冻结，不把扩展字段塞入严格 SSE 事件。

品牌/商标、候选、质量阈值、售价、最低毛利和披露方式需产品/法务/质量裁决。该示例不是现有可售商品。

## 5. 任务与质量合同

### 5.1 任务分类

封闭初始类别：`coding_non_reasoning`、`coding_reasoning`、`reasoning`、`tool_use`、`extraction`、`summarization`、`translation`、`general_chat`、`unknown`。

优先级：

1. 商品 SKU 固定任务；
2. 客户显式受控 hint；
3. 后续有界本地确定性分类器。

分类只能缩小已发布候选池。冲突或低置信度进入 `unknown` 安全默认，不进入更宽、更便宜的池。首片不读取 prompt 猜测任务，不增加在线 LLM 分类器，不记录正文，只保存 class/source/classifier version/confidence 和请求形状摘要。

### 5.2 场景质量证据

逻辑 `QualityEvidence` 绑定 canonical actual model、供应商身份、不可变 execution profile/deployment/model revision，以及 direct channel version 或 external inner pool version；另含 task、suite/rubric/judge version、协议/工具/语言/reasoning scope、指标、样本量、分数/不确定性、有效期、观察时刻和审批。跨供应商/部署复用必须另有明确等价证据和审批，不能因 upstream_model 同名而共享质量结论。

QualityPolicy 冻结允许 suite/metric、分数方向、公开阈值、最小样本、最低置信下界和最大年龄。通过要求证据精确匹配 scope、未撤回/未过期、样本达标且保守置信下界达到公开阈值；“有证据”与“证据通过下限”分开，任何不可比较或低于阈值均失败关闭。

路由版本冻结逐 candidate 的证据绑定，撤回/漂移阻止新派发但不改写历史或阻断原请求财务收尾。direct 与 external 都必须在 sending/PrepareDispatch 事务内以 DB 时钟复核商品活动状态、证据撤回/有效期和绑定版本，关闭过滤后到发送前的撤回竞态。

## 6. 经济合同

### 6.1 准入与实际利润分离

- **保守准入门禁**：在 Reserve 冻结束零售价值、付款基准、经济版本和候选全量保守成本；所有比较先转为明确估值币种并保留原币事实，精度、方向和费用缓冲可追溯。首片 test-only 利润敏感商品采用 `fixed_full_reservation`：固定请求价、最终全额消费原 reservation，不允许按 usage 部分结算；工程夹具先用完整单 CNY 已付基准收窄验证。**USD/CNY 付款基准、USD/CNY 供应商成本及交叉组合是商业首发必需能力**；缺失/陈旧 FX、未知付款或成本失败关闭。部分 usage 的动态计价必须另冻所有允许 charged 前缀和付费/赠送 lot 组合下的付款基准下界、取整和余数合同。
- **服务端 ExecutionPolicy**：不可变 `max_outer_attempts`、inner retry、relay depth、settlement mode 和成本政策同时冻结进商品/route revision、route decision 与 FrozenExecution；服务端与客户偏好取更小 attempt 预算，不能依赖客户端 `allow_fallbacks=false` 实现首片单 attempt。
- **请求级累计成本**：第一次实际/保留成本必须计入第二次候选的保守上限。首片由服务端 ExecutionPolicy 固定 `max_outer_attempts=1`，只有累计成本门禁实现后才可发布大于 1 的商品版本。
- **贡献毛利投影**：复用并扩展现有 034.5 reporting gross_margin 管线，不另建付款基准或利润聚合权威。消费退款按原 funding-settlement-lot 分配边和 funding-consumption-refund-lot 事实冲减付款基准，冻结部分/全额退款的比例、取整和余数规则；同一退款 lot 再次消费不能重复确认现金。独立现金退款与消费退款分域。
- **成本时点**：contract quote 只产生 provisional/as-of 暂估值；只有全部相关供应商成本达到预冻账单确认资格，才显示账单确认贡献毛利。成本更正携带 revision/as-of，可令历史暂估值下降或为负，不能继续显示静态正利润。

未来如需 COIN→CNY 的保守路由估值，必须独立版本化并明确只用于准入，不能替代真实付款基准或会计 FX。

### 6.2 成功任务成本与节省（后续阶段）

语义成功来自版本化离线评估或明确生产反馈。Verified CPST 是 cohort 内全部已知供应商成本之和除以语义成功数；HTTP 完成数另报，不能混称任务成功。未知成本、评估缺失和 cohort 覆盖不足显示 unknown。

期望 CPST 排序只有达到预冻样本和置信阈值后才可启用，使用成本上界/成功率下界；样本不足回退保守 `cost_first`，付费流量探索默认关闭。

客户节省只与同任务、同质量/语义成功口径、同数量、同 schema、已公开并冻结的兼容参照商品比较；语义失败或质量不可比时为 null。其他同价 schema 参照只能显示“零售价差额”，不能称已验证客户节省；负节省和 null 均保留。贡献毛利投影复用 034.5 reporting，不创建第二财务读权威。

## 7. 执行器、池与证据

### 7.1 叶子执行器

逻辑 `ExecutionPolicy` 与 `ExecutionAdapter` 同属不可变发布合同。ExecutionPolicy 至少固定 `max_outer_attempts`、inner_max_attempts、max_relay_depth、settlement_mode 和累计成本政策，并复制进 route decision/FrozenExecution 供服务端强制和回放；首片分别为 1、1、0、fixed_full_reservation，且 `cache_mode=disabled`；direct 的 max_relay_depth 必须为 0，external 才可发布为 1。ExecutionAdapter 是现有 provider.Client 外的薄一次执行接缝，接收 FrozenExecution，返回 Response/Stream 与 ExecutionEvidence；不持有钱包写权限，不改变商品、物理模型或客户售价。DownstreamAdminConnector 是独立控制面，不得与执行 adapter 嵌套。

一个 outer attempt 最多经过一层 external relay。发布配置 read-back、部署网络、出口 allowlist 和漂移检查共同拒绝 aigate/New API/Sub2API 互连成链；不能仅信 relay-depth header。

### 7.2 外部 inner pool

每个 outer binding 固定不可变 inner pool version/digest，dispatch 还携带 expected pool/model/mapping/retry generation。外部执行器必须在选账号和任何供应商 I/O 前原子校验该 generation 并拒绝失配；read-back 后到生成前的变化不能靠周期巡检消除。实际模型/池身份必须在客户 committed 前复核，失配失败关闭。stock 无法提供原子 fencing 时保持 test-only。commercial 池必须均质：同一实际物理模型、协议/usage 语义、质量档、地域/数据条款、成本版本和重试政策。成员、映射、成本、区域或重试变化产生新池版本和新 route revision；旧请求继续使用旧快照。

池内只允许局部账号选择。最终账号安全伪名、实际模型和成本归属无法返回时，不得进入利润敏感商业池。Outer machine credential 的覆盖不能证明 New API/Sub2API 全池账号合格。

### 7.3 ExecutionEvidence

至少包含 adapter kind/version、outer request/attempt、inner pool version/digest、下游 request ID、最终账号伪名、实际模型、协议终态、取消/首事件/committed、usage/consumption/cost 状态、有界 inner dispatch 摘要和证据引用。

stock 下游缺字段时保持 missing/unknown 和 test-only，connector 不得伪造签名报告。结构校验通过的下游自报字段仍是受控观察，只有绑定认证机器身份并与真实供应商账单核对后才可支持资格裁决。

## 8. 故障、重试与恢复不变量

1. Reserve、RecordAttempt、PrepareDispatch 提交前禁止供应商生成 I/O；授权、数据库、报价、凭据或 Guard 失败 fail closed。
2. 一个客户请求只有一个 reservation 和一个 settlement；inner dispatch 不新增客户资金事实，所有实际消耗进入采购。
3. 全局最多两次 outer attempt；第二次须同时满足 SafeToRetry、Plan.CanRetry、前一 attempt 终结、未 committed、原 deadline 有效及请求级累计成本门禁。
4. 外部 inner generation retry 默认关闭。未来开放必须逐 child 证明可信无消费、限制次数和累计成本；普通 429/5xx 不构成无消费证据。
5. 首完整事件验证前不得向客户写 headers/heartbeat/body；写出任何字节后 committed，不再跨 adapter/channel/account 切换。
6. 取消沿 aigate→外部执行器→供应商传播；已发送后的取消不证明无消费，不得 Detach/WithoutCancel 续生成追 usage。
7. sending、unknown、可能已消费、取消、超时、缺可靠终态/usage 或已 committed 均不得重放；同渠道换 Key 不增加预算。
8. 恢复、回放、投影、报表、通知、回调、迁移和 shadow 比较只能处理原事实，绝不再次 Execute/Submit。
9. usage/成本/结果缺失、冲突、provisional、estimated 或 unknown 不得按 0/成功覆盖历史。
10. 缓存命中走现有无 attempt、供应商成本已知 0 的 finalization/settlement，不调用执行器。利润敏感/质量门禁首片固定 `cache_mode=disabled`；未来启用必须在 CacheFinalization 前以 DB 时钟复核商品、逐候选质量证据和绑定有效期/撤回，避免旧缓存绕过新质量门禁。

## 9. 统一控制台与管理写

一个 aigate Vue SPA，一个同源 API；不 iframe、不跳转、不加载 New API/Sub2API 前端资源，浏览器不接触其 Cookie、Token、数据库或管理端口。原生后台只用于受审计的私网 break-glass。

首发工作台：

1. **供应与执行**：Provider/Executor/Binding、物理模型、渠道/池版本、资格、成本、desired/observed/drift、发现和探测。
2. **商品与路由**：商品/披露/售价、候选、质量、任务、auto/degradation、粘性、熔断/缓存、发布与回退、公开页预览。
3. **请求与利润**：route decision、attempt/evidence、实际服务模型、usage/cost/finalization/settlement、节省和分币种贡献毛利。

AdminConnector 先提供 Capabilities、ReadSnapshot、BuildChangePlan 和差异草稿。资源默认 unmanaged；显式 adoption 后每资源只有一个日常写者。只有认证、最小权限、幂等写、版本/ETag、operation 查询、read-back 和删除语义均证明后才开放窄 typed write。

所有写操作使用持久 operation_id、基线摘要、RBAC/CSRF/Origin/再认证/审批和不可变审计。结果未知锁定原操作，只读核查；禁止浏览器自动化、直连下游数据库、自动补偿删除或“一键同步成功”。partial/unknown 不得显示全绿。

## 10. 资格与发布状态

实现位置、`mock_verified`、`external_flow_verified`、`supplier_profile_qualified`、commercial 和 `production_qualified` 是不同状态。证据登记本身只是元数据声明，不自动授予真实资格。

当前 external commercial 保持拒绝。后续 external 资格 SDD 必须追加实际 build/config、全池账号与权限、模型映射、最终账号/模型、逐 child consumption/usage/cost、取消、隐藏重试、漂移、隐私/转售条款、真实供应商和账单证据，以及服务端+deferred DB 双门禁。

真实凭据验证使用独立环境或服务端 verification tenant/key、非公开目录、独立采购预算和受控出口。普通生产 Key、常规租户或客户端测试标志不得进入资格流量。

## 11. 分阶段交付与完成定义

1. **R0（本文）**：冻结上述合同，修订 035/038、HLD/LLD 和统一架构；无运行变化，现有 commercial 门禁不变。
2. **经济基础**：付款基准、请求级累计成本和实际贡献毛利读模型。
3. **direct-only K3-Dynamic test-only**：coding_non_reasoning、单 attempt、max_relay_depth=0、无缓存、无降档、成本未知拒绝、direct 质量/成本证据和实际模型回执。
4. **统一控制台首片**：供应与执行、商品与路由、请求与利润三个工作流，不重建整个平台。
5. **首个真实 direct profile**：DeepSeek Chat JSON/SSE、工具、取消、最终 usage、成本与账单核对。
6. **外部统一读面/test-only 演练**：New API 先行；Sub2API 只在专用账号价值和成本合同明确后试点。
7. **external commercial 与后续产品**：独立 SDD 后逐 profile 开放；generic auto、期望 CPST、媒体/Realtime、HA 和 USD/CNY 之外的通用多币种后置。

每个实现阶段必须有专用 PostgreSQL、故障/权限/金额单位、真实 main 桌面与 390px、相关 race/前端测试和适用的完整 029；架构正文、changes 和 manifest 由集成人语义复核后刷新。任何单次 probe、管理 API 200、截图或三服务拓扑都不能替代真实供应商/生产验收。

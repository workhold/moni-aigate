# 041 实施设计：商品、利润、执行器与统一控制台（R0）

状态：设计候选，未实施。本文只定义逻辑合同和代码接缝，不批准具体 SQL 列、HTTP 路径、迁移号或商业 profile。实现前需按任务拆出聚焦子规格。

## 1. 模块映射

| 域 | 复用现状 | 041 目标增量 |
| --- | --- | --- |
| 商品/发布 | `internal/catalog`、models/profile/price/route/binding/channel/version/physical | 不可变 ServiceOfferVersion、ExecutionPolicy、披露/任务/质量/经济/节省参照引用；CAS 同事务发布 |
| 文本执行 | `internal/provider.Client`、032 首事件/取消、033 Plan | 薄 ExecutionAdapter 与 FrozenExecution/ExecutionEvidence；不另建执行核心 |
| 外部管理 | 035 channel kind 与只读拓扑 | DownstreamAdminConnector、desired/observed/drift、operation journal；与数据面分离 |
| 账务 | Reserve/Attempt/Finalization/Settle、034.5 funding-lot payment basis 与 reporting gross_margin | 请求级保守准入和累计成本；贡献毛利扩展现有报表管线并处理退款/成本时点 |
| 质量 | 033 physical quality_tier、统计窗口 | 独立场景 QualityEvidence/QualityPolicy；统计不冒充语义质量 |
| 控制台 | App/router/navigation/session、PlatformView、RouteEditor/Revisions/Attempts | 供应与执行、商品与路由、请求与利润三个聚焦工作台 |
| 报表 | `internal/reporting`、026 快照/导出 | 商品/任务/实际模型/执行器/成本覆盖/节省/贡献毛利切片；可重建读模型 |

禁止同时复制 Service SKU、Supply Offer、大 Provider 三张重复事实表。新实体优先只保存对现有权威行的引用和披露/证据，不复制费率、绑定、成本或凭据。

## 2. 逻辑数据合同

### 2.1 ServiceOfferVersion 与 ExecutionPolicy

必需语义字段：

- `offer_version_id, logical_model_id, kind`；
- `profile_version_id, price_version_id, route_policy_version_id`；
- `disclosure_version_id, task_policy_version_id, quality_policy_version_id`；
- `economics_version_id, transparency_policy_version_id, execution_policy_version_id`；
- `allowed_candidate_digest, savings_reference_version_id`；
- `created_at, created_by, approval_evidence_id`。

生命周期闭集：`draft, test_candidate, test_active, commercial_pending, commercial_active, rejected, withdrawn, retired`。

```text
draft → test_candidate → test_active → commercial_pending → commercial_active → retired

draft/test_candidate/commercial_pending → rejected
test_active/commercial_active → withdrawn
```

`rejected` 是未通过审批的终态；`withdrawn` 是曾可被选择版本的主动撤回，另保存撤回事实/时间/操作者。活动指针使用 expected_revision CAS，只影响新请求；商业发布还受 038 资格/凭据/撤回/恢复门禁。历史版本不可 UPDATE 业务字段，撤回不改写历史，不阻断原请求财务收尾。

不可变 ExecutionPolicy 至少包含：

- `max_outer_attempts`：首片固定 1，服务端与客户 route 偏好取更小值；
- `inner_max_attempts=1`、`max_relay_depth`：direct 为 0，external 为 1；
- `settlement_mode`：首片为 `fixed_full_reservation`；
- `cache_mode`：利润敏感/质量门禁首片为 `disabled`；后续启用必须在 CacheFinalization 前以 DB 时钟复核商品、逐候选质量证据和绑定有效期/撤回；
- `cumulative_cost_policy` 与 `unknown_cost_policy=deny`。

该政策同时进入商品、route revision、route decision 和 FrozenExecution，供服务端强制及回放。不得依赖客户端 `allow_fallbacks=false` 实现商品级单 attempt。

验证矩阵：exact 唯一 canonical actual model；same-model 所有候选 actual model 相同；branded/task/generic 公开候选矩阵覆盖 task/unknown 默认、质量和披露，未声明候选不得进入 route publication。

### 2.2 TaskPolicyVersion

字段语义：taxonomy version、SKU default class、允许的 hint、class→candidate set、unknown action、classifier version、CPU/memory bounds、shape digest algorithm。

分类结果只保存 `{class, source=sku|hint|deterministic, version, confidence, shape_digest}`。hint 与推断冲突时取更窄集合；无法证明时 unknown。分类器无外部网络、无 LLM、无正文日志、无在线学习。

### 2.3 QualityEvidence 与 QualityPolicy

QualityEvidence 不可变绑定：

- canonical actual model、供应商身份；
- execution profile、部署/模型 revision；
- direct channel version，或 external inner pool version；
- task、suite/rubric/judge version、protocol/tools/language/reasoning scope；
- metric、score/uncertainty、sample count、observed_at/valid_until、evidence ref、approval/withdrawal。

同名 upstream_model 不足以跨供应商、量化部署或映射复用证据。跨 scope 复用需要独立等价证据和审批。证据只追加和撤回，不能原处分值改写。

QualityPolicy 冻结允许 suite/metric、分数方向、公开阈值、最小样本、最低置信下界和最大年龄。通过谓词要求：scope 精确匹配、未撤回、在 DB 时钟有效期内、样本达标、保守置信下界达到公开阈值。证据存在与证据通过分开，任何不可比较或低于阈值均失败关闭。

路由版本保存逐 candidate 的证据绑定。direct 和 external 都在 sending/PrepareDispatch 事务内复核商品活动状态、证据撤回/有效期和绑定版本，关闭过滤后到发送前的竞态。

### 2.4 EconomicsSnapshot 与贡献毛利

请求出生时冻结经济版本、COIN 预占、零售计价量/价格版本、付款基准类型/币种/数值/覆盖状态、节省参照、供应商成本版本和请求级累计成本上限。

首片 test-only 采用 `fixed_full_reservation`：固定请求价，最终全额消费原 reservation；工程夹具先使用完整单 CNY 已付基准以收窄验证范围。**USD/CNY 是商业首发必需能力，不是最终产品只收 CNY。**后续部分 usage 必须另冻所有允许 charged 前缀及付费/赠送 lot 组合下的付款基准下界、取整和余数，不能把全额预占基准错误当作保守下界。

USD/CNY 合同统一使用 `Money{currency, amount_micro}`：内部数值固定为对应法币的 **10^-6 微法币整数**，与现有 034/034.5 `internal/billing/fiat.go` 的微法币事实无损一致；这里的 micro 不是分/cent，也不使用各货币通常两位小数的 minor unit。外部十进制解析必须显式指定 scale，超过六位不得静默丢失：收入保守侧向下、成本保守侧向上，所有取整/余数进入经济版本和证据。USD/CNY 报表同时保留原币净收入/成本向量，禁止直接跨币种相减。商业发布前必须支持 USD 与 CNY 付款基准、USD/CNY 供应商成本及二者交叉组合。

需要共同币种比较时使用不可变 FxPolicyVersion：base currency、来源、报价时刻/有效期、买卖方向、保守侧、取整和费用缓冲。准入门禁采用收入保守下界与全部 attempt 成本上界，缺失/陈旧/不覆盖所需货币对则失败关闭。报表同时保留原币净收入/成本向量和折算值：使用 FX 快照的结果标为“折算贡献毛利”，只有实际换汇及费用事实齐备时才称实收现金毛利；FX 不回写历史钱包或会计价格。

`margin_admission_status=pass` 只说明保守准入，不代表 realized margin。第二次 attempt 必须将已发生/保留成本加入新候选保守上限；首片由 ExecutionPolicy 禁止第二次。

**付款基准双路径边界（2026-11-25，`dynamic-v2`）**：`request_economics_snapshots` 只覆盖显式 P0 offer（test_active、`fixed_full_reservation`、单 attempt）的请求；普通 033 动态路由在 Reserve 不产生快照。为避免同一请求出现两个付款基准事实源，现行裁决是：快照存在时它是唯一权威，网关不执行已付 CNY 查询，路由引擎的 `marginExclusion` 直接走 `evaluateEconomicAdmission`（快照收入下界 + FX + 服务端累计成本复核）；快照不存在时唯一权威是网关请求时的 `ReservationCNYBasis` 查询（CNY-only、只读资金冻结事实、`floor(amount×fiat/issued)` 与快照写入公式一致、未知/USD/开账/赠送之外的 basis 失败关闭），引擎内按冻结路由决策的供应商上界做第二次尝试累计。两条路径按 `route_shape.economics` 是否为空互斥选择，不互为回退；`margin_below_floor` 仍表示快照路径的准入拒绝，`margin_basis_unavailable`/`cumulative_cost_unavailable` 表示普通路径的基准不可证。USD/CNY 交叉估值只在快照路径实现；普通路径的 USD 付款整笔失败关闭，等后续冻结通用估值合同时再收口。

贡献毛利**复用并扩展现有 034.5 reporting gross_margin 管线**，不新建付款基准或利润聚合权威：

1. 原 funding-settlement-lot 分配边提供 charged payment basis；消费退款按原分配边及 funding-consumption-refund-lot 事实冲减，冻结比例、取整和余数规则。
2. 同一退款 lot 再次消费不得重复确认现金；跨请求聚合以原资金收款为上限，独立现金退款与消费退款分域。
3. 所有实际 outer/inner 成本进入原采购事实和报表；缺付款基准、成本或分配不完整返回 null。
4. contract quote 只产生 `provisional`、带 as-of/cost revision 的暂估贡献毛利；全部成本达到预冻账单确认资格后才显示账单确认贡献毛利。成本更正可令暂估值下降或为负。

### 2.5 FrozenExecution 与 ExecutionEvidence

FrozenExecution：request/outer attempt ID、binding/channel version、physical model、execution policy/profile version、inner pool version/digest、机器凭据版本、成本/质量证据引用、亲和键、剩余 deadline。

ExecutionEvidence：上述身份的安全投影，加下游 request ID、最终账号伪名、实际模型、终态观察、首事件/取消/committed 观察、usage/consumption/cost 观察、child dispatch count/摘要、observed_at 和证据 digest。

原始证据以 `(outer_attempt_id, source, source_event_id)` 和 canonical digest 判重；同键同摘要幂等，同键异摘要或终态冲突标记 conflict/unknown 并阻止自动结算。重复/乱序合并规则在子规格冻结。

**ExecutionEvidence 只是现有 metering_events/finalization/attempt 采购事实的输入或追加扩展，不是独立终态权威。**客户交付读 finalization，用量读原计量事实，成本读原采购事实，控制台使用同一投影；原始证据仅供审计。恢复不得用一个新证据覆盖另一个权威状态。

### 2.6 DownstreamResource 与 AdminOperation

资源逻辑身份：executor、resource type、stable downstream ID、managed/unmanaged、desired ref、observed safe snapshot/digest、observed_at、drift state、credential ref。

外部写 I/O 前必须持久化 operation_id、baseline digest、resource/desired、操作者和 `prepared/dispatching` 状态，再进入 submitted。崩溃恢复遇到非终态 prepared/dispatching/submitted，一律先按 unknown 锁定并用原 operation_id 查询；本地 prepared 不是可信 not_sent。

状态机：

```text
prepared → dispatching → submitted → applied
                              ├→ rejected
                              ├→ partial_needs_review
                              └→ unknown
unknown + 原 operation read-back/证据/审批 → applied | partial_needs_review | rejected(not_applied_confirmed)
```

reconcile 必须保存原 operation_id、read-back digest、操作者、证据和审计。确认未应用才终结 rejected；不得换 operation_id 重发。新 desired 只有在原事实明确后创建。break-glass 修改默认为 drift，必须显式 adoption/reapproval。

## 3. 请求时序

### 3.1 direct/外部共同前缀

```text
AuthorizedModel
  → ServiceOffer/Profile/Price/ExecutionPolicy/Task snapshot
  → Reserve（含经济快照）
  → 质量/隐私/能力/资格/成本/累计毛利硬过滤
  → sticky/priority/order/cost/statistics 排序
  → RecordAttempt + RouteDecision（含 ExecutionPolicy）
  → PrepareDispatch
  → FrozenExecution
```

任何提交前失败不触网。direct 与 external 的 sending 事务都复核商品、质量证据和绑定版本；第二次 attempt 还重核原请求累计成本和原 deadline，首片不允许第二次。

### 3.2 direct

`provider.Client` 保持一次 Do、禁重定向和取消传播。direct 账号选择发生在 binding 选定后，是本地不可变凭据池成员选择，不新增候选或客户资金身份。

### 3.3 New API/Sub2API

`aigate → 专用机器凭据 → 固定 inner pool → 一个供应商出口`。池内账号选择不改变公开商品、物理模型、地域、质量、成本类别或资金身份。

发布和 PrepareDispatch 校验 desired/observed digest、账号资格、live 状态和成本合同还不足以关闭生成前竞态。dispatch 必须携带 expected pool/model/mapping/retry generation；外部执行器在选账号及任何供应商 I/O 前原子校验并拒绝失配，随后按同一快照执行。实际模型/池身份必须在客户 committed 前复核。stock 无法提供原子 fencing 时保持 test-only，不能依赖定时巡检或事后证据。

### 3.4 收尾

`Response/Stream → ExecutionEvidence → Finalization/Observe → Settle → 034.5 贡献毛利/节省读模型`。客户零售量来自最终可信响应；采购覆盖全部可能消费的 outer/child execution。迟到账单只追加成本异常/更正并更新 provisional/as-of，不改写客户历史售价或自动补扣。

## 4. 故障矩阵

| 场景 | 执行/重试 | 财务/证据 |
| --- | --- | --- |
| 预占/授权/报价/池版本失败 | 不派发 | fail closed，不伪造 attempt 成功 |
| 明确 not_sent 且未 committed | 可按原预算选择下一候选 | 原 attempt 保留，累计成本重核 |
| frozen profile 的可信 rejected_no_consumption | 同上 | 保留机器码和证据，普通状态码不够 |
| sending/unknown/可能已消费 | 禁止内外层重放 | 原 attempt 观察/核对，成本未知不归零 |
| 已写客户 header/heartbeat/body | committed，禁止切换 | 原响应与原资金身份收尾 |
| 取消/总 deadline | 传播并关闭资源 | 不证明无消费，不为追量续生成 |
| 缺/冲突/provisional/estimated usage | 不自动补发 | pending/unknown，不伪造零 |
| 外部池漂移/隐藏重试 | 阻止新派发，必要时 quarantine | 历史不变，原请求继续财务收尾 |
| AdminOperation unknown | 不换 ID 重发 | 原 operation reconcile 后解锁 |
| 无效签名/未知任务回调 | 零下游 I/O，安全拒绝 | 不创建任务、不结算、不泄露状态 |
| 已验签且映射本租户原 task 的回调 | 幂等去重后有界 Query 原引用 | 防请求放大；不直接结算；重复复用原回执 |
| 后台恢复/回放/报表/通知 | 无 Execute/Submit 权限 | 只读原事实、补证据和推进财务终态 |

媒体/任务/Realtime 的完整回调、Resolve、制品和恢复合同不在本片，表内仅规定安全外边界，具体实现另立 SDD。

## 5. 控制台权限与 DTO

- admin：供应、执行、商品、路由、资格、利润全量授权读；写操作按资源和审批拆分。
- finance operator/approver：付款基准、成本/对账、退款和贡献毛利；无执行池秘密或模型正文。
- owner/member：公开商品、实际服务模型/档位、用量、售价、兼容参照节省和结算；无内部 endpoint、账号、采购价或其他租户信息。
- billing_viewer：授权账务读面，无路由/凭据写权限。
- anonymous：只读公开商品与披露，无平台内部读面。

DTO 使用严格字段闭集、统一 envelope/request ID、keyset 分页和安全错误码。拓扑、资格、迁移、健康和账务分别显示，不能用一个绿色“成功”合并。unknown/unsupported 余额和成本不显示为 0，provisional 与账单确认贡献毛利使用不同标签。

## 6. 首个实现纵切片

1. 子规格先冻结经济/质量/商品/ExecutionPolicy/执行证据/AdminConnector 状态机和数据库/接口/ACL。
2. 复用并扩展 034.5 reporting gross_margin，补净付款基准、inner 成本、商品/任务维度和 provisional/as-of；Reserve 经济快照不建立第二报表管线。
3. 新建独立 K3-Dynamic test-only 商品：fixed_full_reservation、max_outer_attempts=1、max_relay_depth=0、cache_mode=disabled、coding_non_reasoning、公开候选、无降档、实际模型回执、成本未知拒绝。
4. 首批仅 direct 候选，DeepSeek 可作为首选；每个候选具有同场景质量和已知 CNY 成本证据。
5. 使用 033 cost_first/sticky/replay，不新增 profit 路由器或第二 RouteSelector。
6. 增加薄 ExecutionAdapter，但不改变当前选择结果和流式 committed 行为；ExecutionEvidence 关联原 attempt。
7. 控制台交付三个聚焦工作流；external inner pool 无证据时显示“未接入/不可证明”。
8. New API/Sub2API commercial 继续拒绝，Sub2API 不进入首个必需部署集。

## 7. 验收边界

R0：只读文档语义复核、链接/架构检查和 `make check`；不声明业务或生产验收。

实现片：专用 PostgreSQL 覆盖租户隔离、金额单位、fixed_full_reservation、付费/赠送/未知基准、消费退款及退款币再次消费、累计成本、缺 usage、取消、unknown 不重放、唯一结算、池原子 fencing、AdminOperation 崩溃/reconcile、质量撤回竞态和 provisional/账单确认成本时点。客户节省必须覆盖任务/质量不可比和语义失败 null。

真实 main 桌面与 390px 完成商品发布、候选/质量查看、路由回退、冲突/未知核查和利润查看。最终共享商业链候选按仓库要求重跑完整 029，CI 全绿；真实供应商和生产资格另验。mock、拓扑、单次 probe、管理 API 200 或截图均不能替代资格。

## 8. 后续独立 SDD

- 动态部分 usage 的保守付款基准下界；
- external New API/Sub2API commercial、inner pool 原子 fencing 证据和服务端+DB 门禁；
- 订阅/OAuth 账号单请求成本分摊；
- 期望 CPST、置信阈值和离线策略回放；
- USD/CNY 之外的通用多币种、自动 FX、税费、支付手续费和营销补贴；
- 媒体/任务/Realtime 外部执行与 Resolve/回调/制品安全；
- generic auto、租户级 canary、HA、自动扩池和选择性源码内收。

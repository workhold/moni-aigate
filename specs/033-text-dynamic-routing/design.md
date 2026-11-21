# 033 实施设计与状态机

2026-11-20。本文把 `spec.md` 与 `implementation-freeze.md` 落实到当前代码接缝。当前分支已合并 032 的 `312fa5f`（含 `034e862`）；其熔断参数、cache-hit finalization 和首事件边界已进入组合代码，032 的性能基线仍以该规格自己的报告和验收为准。

## 1. 单一执行链与依赖边界

文本请求继续只有一条 `AuthorizedModel → Parse → Reserve → Plan.Start/RecordAttempt → PrepareDispatch → Provider → Finalization/Settle` 链。动态路由扩展现有 `routing.Plan/Engine`、`catalog.CandidateSnapshot` 和 `billing.Store`，不增加第二个钱包、计量、结算、采购成本或供应商 adapter。

032 合入后，同一不可变 route publication 同时携带其 `failure_threshold/open_ms/half_open_tokens/cache_ttl_seconds` 和 033 策略字段。cache hit 在动态计划之前完成原 finalization，不创建 attempt 或动态决策；cache miss 才进入本设计的选择链。健康 key 为 `channel_id+upstream_model`，物理 binding 仍是选择单元。033 的统计不借用 032 的性能结论。

## 2. 物理模型、零售 profile 与退役

- D-033-0=A：`models.profile_version` 仍是唯一客户零售合同、schema、预占界和输出上限。physical capability 不创建第二个 profile，不改变 request retail bounds 或 reservation。
- `physical_models.capability` 是唯一能力事实，严格键为 `context_limit,input_upper_bound,output_limit,supports_tools,supports_parallel_tool_calls,usage_semantics,modalities,quality_tier,quality_score,max_concurrency,supported_parameters`。`quality_score` 只能为有限数值或 null；缺数据保持 null。`modalities` 当前只能为 `["text"]`，usage 只能是与逻辑 profile 一致的 `final/cumulative`，质量档位只能是 `economy/standard/premium`。
- 物理身份、能力和 revision 不可变，DELETE 拒绝。唯一允许的状态变化是同事务、同 revision 的 `active → retired`；retired 不可复活，其余列不可改。新能力必须新建 ID，旧行通过退役和新 route revision 退出新候选。
- 既有 binding 迁移为与逻辑 profile 完全等价的 physical row。发布时 old-style binding 可经 store 明确查找/创建该等价行，但落库的 `physical_model_id` 与 `upstream_model` 始终同时存在且由数据库 guard 核一致；这不是第二套 binding 事实。
- binding 去重键为 `(channel_id,physical_model_id)`。同 channel 的不同物理模型保留各自 priority/weight/cost quote；同 binding 的多个 credential 仍只是成员，不产生多个选择候选。

## 3. 不可变 route policy 与 CAS

route publication 继续在 model 锁内检查 `expected_revision == active_revision`，从历史 `MAX(revision)+1` 分配新版本并 CAS 更新活动指针。新版本一次性写入：

- `scoring`: `{"auto":bool,"allow_degradation":bool}`；缺省两者 false。只有显式 auto route 可跨质量档位；非 auto 发布拒绝混合档位。
- `quality_floor`: null 或 economy/standard/premium。
- `min_margin_rate`: null 或 0..10000 的万分比整数。
- `stickiness`: null 或 `{"enabled":bool,"max_age_s":1..86400,"break_on":["quality","cost","health"]}`；去重、顺序固定，enabled=false 时不得携带 break_on。

旧列 NULL 表示关闭。auto 仍只有一个逻辑模型、一个 default 价格版本和一份客户 reservation/settlement。`allow_degradation=false` 时只保留最高可用质量档；true 时允许低档候选，实际选中非最高档必须写 `degradation=tier_changed`。`allow_fallbacks=false` 只保留最高可用档并只允许一次 attempt。

## 4. Chat `route` 请求合同

只有 `POST /v1/chat/completions` 在 provider parser 前剥离并严格解析顶层 `route`；原请求 bytes 仍用于 idempotency/HMAC，剥离后的对象交给现有 Chat parser，向上游永不发送 `route`。Responses、Messages、Gemini、Embeddings、Rerank、媒体、任务和 Realtime 不剥字段，继续由原严格信封拒绝。

`route` 是对象，未知键、重复键、null、尾数据均 400。字段闭集：

- `order/only/ignore`: 1..32 个非空、去重的受控字符串。选择器精确匹配当前授权 route 中的 `channel_id`、`physical_model_id` 或 `upstream_model`；不模糊匹配，不接受表达式。only/ignore 的未知选择器、only 空交集均 400；order 的未知选择器同样 400。only 后再 ignore。
- `allow_fallbacks`: bool，缺省 true；false 时 `maxAttempts=1`。
- `require_parameters`: bool，缺省 false；true 时把请求实际使用的可选参数与 physical `supported_parameters` 求交，不满足为 `capability_missing`。tools 与 parallel tools 无论该开关都执行硬能力过滤。
- `sort`: `price/latency/throughput`。显式 order 在同 priority 内优先于 sort；无 order 时 cost_first 先比较精确成本，sort 作为后续次级键。延迟/吞吐只在样本数≥50 时有值，未知统计排在已知值之后，不用 0 估算。
- `max_price`: 对象，键只能为 `input/output`，值是微 CNY/百万 token 的规范非负十进制整数字符串；至少一个键。与冻结 supplier Rates 用有理数精确比较，超界为 `cost_limit_exceeded`；设置该字段时 unknown quote 一律以 `cost_unknown` 排除，未设置时才由 unknown_cost_policy 决定是否保留。
- `data_collection`: `allow/deny`。当前平台没有客户正文训练或供应商 data-collection 开关；deny 不加宽、不发送新 header，allow 也不改变既有禁日志/禁正文默认。将来接入供应商收集政策前不得把 allow 解释为授权。

会话标识只取 `X-Moni-Session`，1..128 字节的 `[A-Za-z0-9_.:-]+`。其他大小写、空白、正文、IP 或登录 cookie 不作为路由会话。DB 键为 `(tenant_id,session_id)`，不跨租户。

## 5. 两阶段选择与故障

候选以 binding 为选择单元，credential 是选中后的组内成员。固定顺序：

1. 已授权 model 之外的候选根本不能进入快照；route only/ignore 和地域以 `not_authorized/region_forbidden` 过滤。
2. physical 缺失/retired、context/input/output、工具、参数能力依次为 `capability_missing/context_too_small/output_limit_too_small/tools_unsupported`。
3. quality floor 为 `quality_below_floor`；非 auto 混档在发布时拒绝。
4. 既有 channel/binding/credential 状态、容量/熔断和采购过滤保持原最终否决权，原因映射到本规格闭集。
5. unknown cost → max_price/逐维 cap → 最低毛利。保守成本是所选 physical supplier bounds 与冻结 cost Rates 的单次 ceil U；客户冻结售价是原 reservation.amount R。精确判定 `(R-U)*10000 >= R*min_margin_rate`。R=0 时只有 U=0 且下限为 0 才通过，正下限返回 `margin_below_floor`。

通过硬过滤后才执行 sticky → priority → order/cost/sort → 可记录权重 draw → credential draw。被排除项保留 `filter_reason`，排序项另写 `reason`；高评分不能复活排除项。already_tried 按 binding 而非 channel，允许同渠道换物理模型，仍最多两次 attempt。

无候选、容量/熔断耗尽或准入失败不派发。已有 request 时在 Release 同一事务写 `attempt_id=NULL` 的决策再释放；完全没有候选时 selected 字段为空，准入失败则保留已经选中的 binding/physical 与 draw，使回放仍得到同一路径。没有 request 的 400 授权/信封拒绝不伪造 request 或决策。任何 `Consumption=unknown` 不改变 `SafeToRetry`，后台恢复和 route replay 都没有 provider 调用权。

## 6. 决策、粘性与统计快照

`route_decisions` 每次选择一行，包含规格字段以及 `policy_snapshot`、`request_shape`、`algorithm_version`。两个快照是安全、有界、规范 JSON：不含 secret、URL、正文、IP、cookie。候选项包含 binding/channel/version/physical、priority/weight、quality、capability、filter_reason/final reason、score、cost_snapshot 和 stats；数组≤1000、JSON≤256KiB，digest 是 canonical bytes 的 SHA-256。

- `draw` 是实际 uint64 权重抽签的十进制整数；credential draw 也保存在 `score` 的受控字段中。
- sticky 命中且未过期、未被 already_tried 或 break_on 排除时直接选中，`sticky=inherit`、reason=`sticky_override`；首次建立为 establish。质量/成本/健康打破分别写 break_quality/break_cost/break_health。建立/延续只在对应 attempt 准入事务中 upsert route session，last_seen/expires 使用 DB 时间。
- `request_shape` 固化 route 偏好、输入/输出界、工具/参数、sticky 目标与统计窗口；`policy_snapshot` 固化排序、质量、毛利和 auto 规则。回放不读取当前 policy/model/stats/session。

统计键为 `(physical_model_id,channel_id)`，窗口 15 分钟。真实样本来自本请求已持久化的 attempt 与 finalization：completed_at 是 finalization 持久化时间，first_content_at 是首次已写下游内容的时间；非流式完成同时作为首内容时间。样本不足 50 时所有统计值为 null。吞吐是窗口内成功完成数/900 秒，以微次/秒整数保存；延迟为真实首内容减 attempt created_at 的微秒 p50/p95。`route_stats_windows` 只是可重建读模型，不是资金事实；刷新只读原事实，失败不触发模型请求。

## 7. attempt 计量 guard 修订

request 的 retail/supplier bounds 仍按逻辑 profile 出生；attempt retail bounds 等于 request retail，attempt supplier input/output bounds 分别取所选 physical `input_upper_bound` 与本次请求 output limit。physical 必须满足 request 界，且 attempt plan/quote/digest 与实际 supplier bounds 一致。`upstream_attempts` 保持原始身份和列形状，不在 attempt 冗余存 physical/binding；`meter_insert_attempt_snapshot` 从原 request route revision、attempt channel/version/upstream 和不可变 binding 派生能力，不能从当前 model/profile 或另一 binding 猜测。迁移生成的等价 physical 使旧 attempt 数值不变。

## 8. 回放与读取

`routing.ReplayRoute` 只消费一条不可变 DecisionRecord 内的 policy/request/draw/candidates，重跑同一纯选择算法并逐字段比较 selected 与原因序列。迁移的旧记录标记 `algorithm_version=legacy-v1`，只回放已冻结的候选/结果，不伪称能重建当时未保存的随机或统计输入。当前记录为 `dynamic-v1`，同一输入逐字节一致。

`moni-aigate route-replay --request <id>` 使用只读 DB 查询，输出每行 sequence、选中路径和候选原因；不写 route decision、不创建 attempt、不派发、不触碰资金。平台 attempts 接口和 RouteAttempts.vue 改读新表；无 attempt 的失败池也按请求展示。客户响应 model 始终回显原始请求名；physical ID 默认仅平台可见，客户详情只显示服务档位标签，physical 的显式展示开关才允许显示物理名。

## 9. 验收边界

确定性纯域覆盖全部原因、三类切换、抽签/回放、auto 降档、only/order/ignore 和 max_price。专用 PostgreSQL 覆盖不可变性、角色、迁移回填、决策与 attempt/无候选释放同事务、供应商界、唯一结算及 unknown 单接收。032 的基线与故障矩阵合入后必须重跑对应组合验收；在此之前不能宣称性能达标、完整 032+033 商业验收或真实供应商/生产认证。

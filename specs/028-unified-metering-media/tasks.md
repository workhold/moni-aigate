# 028 实施任务与阶段出口

当前运行时迁移入口已切到 `ApplyCurrent`/`VerifyCurrent`，只执行单一 `migrations/current_mvp.sql` 基线。它在业务 DDL 前拒绝已有历史或非空 schema，并只允许完整当前基线。历史 `ApplyThrough` 仅留给隔离 fixture，不能作为服务启动路径。

2026-11-04。本页是当前执行清单，不用设计完成替代运行验收。总体范围见[规格](spec.md)，当前可用能力见[统一架构](../../docs/architecture/README.md)。经营报表026和来源027已经接线，后续阶段需持续回归。

## U：六文本单一向量路径

- [x] 盘点当前serve消费者，区分冻结研究及其固定历史fixture：[消费者清单](u-consumer-inventory.md)。
- [x] 冻结共同纯域工厂、完整绑定、known/unknown联合、两侧用量确认及错误/资源合同：[共同域](u-domain-contract.md)。
- [x] 明确存储列/键/双向完整性、原来源与采购接缝、拒迁和角色边界：[存储草案](u-storage-contract.md)。这是实施输入，正式SQL全文与PG原型仍在下一项。
- [x] 完成共同域实现、独立审查及纯专项；[验证记录](../../docs/verification/unified-metering.md)与[机器记录](../../docs/verification/unified-metering.json)记录准确源码/命令/结果。纯专项不能勾选下方生产项。
- [ ] 集成人追加正式迁移全文、canonical/整数有理金额/guard/发布指纹/最小权限，实际PG原型验证后冻结生产DDL；不修改001–023原件。候选024已覆盖两维文本schema、有理率、请求/尝试快照及四条界行、请求/尝试计划与inference报价、纯核bounds/rates/QuoteDigest、独立parent_plan_digest、双侧终态与成本来源指针、受管采购证据及角色边界。6c 已补客户R、结算金额、capability完整性、route/binding出生、route complete 与非零route_revision guard，并在55439专库通过相关迁移/catalog/billing/server回归；本轮又补了 payment worker 对计量表和 `meter_%` 函数的独立拒绝负控、attempt quote/parent plan 与跨租户 snapshot 反例、实际 LOGIN 权限负控，以及 api key/procurement held-spent、周期预算、funding lot child、payment capture/identity/effect 和 procurement evidence identity 的候选拒迁负控。九组完整guard、11.2全部旧金融事实正反例及生产级权限全矩阵仍未全部验收，本项保持未勾。
- [ ] 并行改造provider、catalog/pricing/routing、billing/procurement与HTTP/UI，最终原子切换；移除在线scalar公式及旧内部DTO，不启用双运行。6a 已将默认/组价/采购发布接入完整向量 operation、指纹和不可变回执，6b 已切报表与采购读端，6c 已切当前准入/路由/账务复验，6d 已覆盖受限APP的局部商业链；本轮又将人工核对提交切为绑定原请求 schema 的完整 `retail_quantities`，删除 HTTP/UI 的 `input_tokens/output_tokens` 输入并为 proposal 增加 canonical/digest guard，补齐前端对图片/TTS/video/realtime 报表的协议和同步/异步解析；仍有完整guard、其余公开/私有出口、浏览器和生产部署验收待完成。
- [ ] 受限APP下六文本完整商业链：当前已通过六协议 paid/known-zero、unknown政策、组价HTTP回放、默认/组价六协议组合、等待期间原价/不重发、标量读取禁用、一次退款审批/重复审批和027来源一致性、过期未知用量恢复不重生成，以及本轮六协议超界/客户人工核对/供应商 unknown 反例专项；真实 supplier late、完整出口矩阵、浏览器和生产 LOGIN 仍未形成验收闭包。
- [x] 026经营报表按原请求快照增加协议/执行种类/schema/variant元数据及对应筛选/CSV/UI；金额沿原结算/attempt事实，不从向量重算。2026-11-04 请求明细从不可变请求行读取 protocol/execution_kind，从 `request_meter_snapshots` 读取 profile/schema/variant；缺失留空，不回填当前模型，也不默认成 Chat。筛选、JSON、CSV 末尾六列和报表页同步。金额仍是原结算一次、全部尝试成本。
- [ ] 汇合架构语义复核、专项/当前PG回归、前端与构建，提交推送；2026-11-09 已完成当前代码的架构语义复核、架构快照刷新、迁移/operations/billing/server/task/realtime 顺序 race 专项、整仓编译、vet、前端336项测试和构建，并完成本机 Compose 五条业务链实跑。完整U guard/商业出口、全仓同专库 race、外部供应商/目标部署仍未闭合，提交推送待用户授权。

U当前发行只用每模型唯一capability，同一profile跨渠道/凭据先去重；共同算法支持候选最大界和收紧不表示已发布异构profile路由。多profile路由的绑定身份与参数兼容须另行设计。人工核对绑定原attempt、只确认客户用量，不能据此确认供应商采购。

2026-11-05 第1片追加请求/尝试两侧 bounds canonical/digest、known rates canonical/digest及纯核 `metering.QuoteDigest`。计划未就绪时这些列与计划、外层报价必须同为NULL；supplier unknown且计划就绪时保留bounds/外层unknown，supplier rates与纯核quote为空。历史前缀夹具继续保留原合同。

2026-11-05 第2片追加 `attempt_meter_snapshots.parent_plan_digest`，绑定同租户原请求计划；第3片追加互斥的finalization/correction成本来源指针，unknown时金额与两指针同空。迁移canonical/缺口、billing生命周期/证据/结算及角色边界局部专项已在PostgreSQL 16.15通过。
2026-11-05 第4片将 `observation_canonical` 从旧 Usage JSON 改为 inference Observation canonical：新增 observation digest、协议/交付/finish 元数据、来源闭集和数据库重建 guard；相同来源正文重放保留原事实，异体进入 conflict。迁移 canonical/缺口、billing evidence/settlement/procurement、operations meter role boundary 专项已通过；028-U存储项保持未勾选，后续 HTTP/UI 和六文本完整商业链路仍待完成。

2026-11-05 第5片继续收口：路由发布和公共目录发布校验改读 `meter_model_profile`；当前成本/价格/模型夹具改用meter入口，attempt报价改读独立快照。公共目录反例在profile出生时创建；历史013/018夹具只核原SQL合同，并验证024拒迁。周期预算HTTP修正模型身份，只编译。新增计划未就绪×known/unknown组合、非法纯核快照负控及SQL纯核摘要与Go的一致性测试。具体验证结果见[验证记录](../../docs/verification/unified-metering.md)，完整HTTP/UI切换与六文本商业链尚未收口。

本片继续对齐Gemini/Responses/Messages/检索的当前财务夹具：只从quantity行读取归一化量，cache/reasoning仍在provider/原生wire断言；移除无调用方的旧 `meter_event_usage` JSON函数。当前采购报价等待、来源零价种子改用meter函数。`maintenance-scan-qa` 实际是固定源码pins和C0原件的历史组合，本片保持其冻结身份，只编译；不把改写其旧列或签新pins当成当前商业验收。

第5片剩余：采购/cost routing内部DTO与精确向量发布指纹、公开/私有HTTP与TS报价/数量合同、当前请求查询的legacy标记、九组完整guard；然后才能进行第6片受限APP六文本完整商业链。管理端每百万token整数输入继续保留。

2026-11-05 第5片续：采购发布不再从最后创建的capability猜schema，SQL入口显式定位schema，HTTP/UI必须提交并回显schema_id/digest/default。原route新增profile绑定及出生stamp，非零active route与请求route须匹配profile。候选、attempt、采购锁后与派发前选价只读取各自原schema/variant域；unknown和计划未就绪快照也独立核真实attempt的channel/upstream。新增跨域、同刻异schema、known零价/unknown、未发布schema与input-only非零输出负控。前端329项测试、strict/Vite构建已通过，不代表浏览器验收；存储项及六文本商业链继续未勾。

2026-11-05 第5片续：先冻结存储合同6.2.1，客户实际收费、attempt成本及受管采购实际消费直接读取原不可变价格/成本完整费率canonical与对应侧finalization数量canonical，共用metering.Amount。移除financialRequest两价；缺失/非规范/错绑定/摘要错拒绝本次资金事务，计划未就绪仍只沿原版本、不补快照。新增禁用scalar反算函数的ready/unready矩阵，验证原价、单次ceil、known零价/unknown、超界、明确无消费和人工核对不确认供应商；完整准入DTO、guard及受限APP六文本商业出口仍待完成，存储项保持未勾。

2026-11-05 第5片客户预占续：先冻结存储合同4.1.1，SelectEffectiveTx按当前唯一capability/schema/digest/default及价格组，同SQL读取原完整Rates与DB时刻并核规范字节/摘要；Selection与Reservation删除两价镜像，Reserve直接ReservationAmount，不走标量反算。新增受限APP禁标量预占13个场景（默认/组价、零价、单ceil、input-only、缺价/未来价、溢出回滚后同幂等键重试）及当前选价schema隔离/9类坏原件负控；原model/group/wallet等待回归保持。历史前缀只改必要断言到原旧列、不升级024。采购准入/发布指纹、完整guard与受限APP六文本商业链未完成，U存储项继续未勾。

2026-11-05 采购U续：按存储合同7.1以原attempt供应商界和原同域费率计算U，并新增024独立数值guard；专项覆盖受限APP、原价、零价、单ceil、缺失/伪造和溢出回滚。当时发现的采购预占列表旧列503已在下一片修复，原失败记录保留。CostQuote及路由仍有标量接缝；九组完整guard和商业出口仍未完成，存储项不勾。

2026-11-05 采购列表续：先冻结存储合同7.2，024新增只读SECURITY INVOKER原supplier界投影，固定搜索路径且撤PUBLIC执行。管理端列表从原attempt快照和完整界行返回纯核Bounds wire、原profile/schema/default，不返回旧两界字段；TS严格解析、保留大整数，UI分列显示原用量界与原金额。缺/错绑定原件使整页安全503，不隐藏预占或回填未就绪canonical。新增受限APP列表、共享账户跨租户/两账户、50+1分页、计划未就绪、input-only零输出、原价/模型停用、缺失/错误原件及权限专项；仍不代表浏览器或受限APP六文本完整商业链通过，存储项继续未勾。

2026-11-05完整候选报价值纯层续片：先冻结存储合同4.2.2，新增routecost不可变完整schema/default/channel/upstream/version/basis及supplier Rates选择值、原存储canonical/digest核验与可选精确有理cap。known零价/unknown/坏原件及同UUID异schema严格区分，unknown不含伪零率，不增加报价身份或金额计算。该包只有纯测试消费，未在线装配；现行routing.CostQuote、候选与采购/派发复验及冻结022原型本片未切换，不启用双运行或fallback。纯race/fuzz及当前PG回归结果见验证记录；下一片仍须完整输入隔离和受限APP禁标量链验收，存储/生产切换/商业出口复选框保持未勾。

2026-11-05完整候选报价在线续片：按存储4.2.2原子接入catalog→routing→RecordAttempt→采购account/PB锁后→PrepareDispatch。原canonical/digest读取与完整报价相等性、同schema有理cap、CompareExactCost精确排序及ReservationAmount初筛替代旧两价DTO/CostScore，坏原件不降级成unknown；复用原锁后决策时刻、不回填未就绪canonical、不改变026金额/原U/客户价。NewProspective独立Store与FrozenAttemptInput保留原13参ABI；原冻结研究SQL/pins/旧列不升级。受限SET ROLE APP禁两标量函数的六协议paid/known-zero、unknown策略、排期跨凭据等待及唯一收费/原采购局部测试通过；这不是独立LOGIN、周期预算专库、退款/来源/恢复全组合或完整商业出口。历史fixture中原依赖旧current运行时的组合现只编译并明确拒绝，不把编译或拒绝边界单元测试当历史业务oracle验收。6a–6d 当前片已在 codex/initial-mvp 汇合并通过55439的catalog/billing/server/operations/migrations回归；完整guard与六文本总矩阵仍未完成，继续保持上述复选框未勾，未commit/push。

2026-11-07 阶段1收口复核：6c 的独立金额/结构 guard 与非零路由边界、6d 的受限 APP 六协议局部链和周期预算HTTP夹具已完成当前域适配；默认协议在唯一模型写入口规范化到不可变 profile。PostgreSQL 16.15 专库的 migrations、catalog、billing、server、operations race 回归通过；这不替代九组完整guard、浏览器、正式供应商、部署/HA/性能或 S/A/R 后续阶段验收。架构快照在本片语义复核后刷新，提交/推送仍待用户明确授权。

2026-11-07 阶段1续：新增 deferred `route complete` guard，空 route、错误 channel/version 关系和出生时没有有效 credential 的 binding 不得提交；合法 route 必须把 route、binding、可选 cap 和 active pointer 在同一发布事务内完成。55439 migrations、catalog、billing、server race 回归通过；这只补结构边界，不勾选完整九组 guard 或六文本商业总矩阵。

2026-11-07 阶段1续片：受限 APP 新增默认价/组价六协议组合（Chat、Responses、Messages、Embeddings、Rerank、Gemini），逐项核客户收入、采购成本、唯一结算、原组价快照及禁用标量读取；新增退款审批、重复审批、原 settlement/refund lot 边和 `CheckWalletSources` 一致性专项。两项均使用55439专库并通过，完整超界、恢复不重生成、生产角色/迁移全矩阵和外部出口仍未验收，复选框保持未勾。

2026-11-07 阶段1续片：新增过期未知用量恢复专项。受限 APP 请求只产生一次上游 POST 和一次 attempt，恢复从持久 reservation/recovery job 释放冻结；重复 recovery 不生成第二次 attempt、结算或网络请求。该测试只证明当前文本恢复不重生成，超界、完整权限矩阵和外部出口仍未验收，复选框保持未勾。

2026-11-08 028-U人工核对向量片：按存储合同11.2.1，`POST /api/platform/reconciliations/:id/resolve` 的收费提案只接受完整 `retail_quantities` QuantitiesWire；服务端从原请求不可变 schema 校验 canonical、schema/side、维度顺序、单位、int64和请求界，proposal 保存 canonical/digest，批准继续沿 `approved_reconciliation` 观察与原结算链且 supplier 保持 missing。旧 `input_tokens/output_tokens` HTTP/UI入口已删除，未知字段、重复键和旧标量请求有单测负控。024空库迁移、billing evidence/vector actual/人工核对和server输入边界在55439专库或纯单测通过；U存储与六文本完整商业链复选框仍未勾。

2026-11-08 028-U guard/账务续片：024 的 `meter_route_complete` 现在按每个 binding 拒绝半套 supplier cap；cap 要么为零条，要么同时包含 `text_input_token` 与 `text_output_token` 两条完整维度，单条或跨 binding 拼接均在提交时回滚。Reserve/finish 的 funding lot reservation/settlement 写集先完成纯余额投影，再按原顺序一次批量发送；已有单行退款路径仍复用同一投影校验。迁移 route-cap 矩阵、billing lifecycle/rollback/finish-tail/financial-boundary 专项在55439通过；九组完整 guard、全量商业出口和008其余正式验收仍未完成。

2026-11-08 028-U/008商业与账务续片：超预占的 `billing_profile_blocks` 与 `billing.profile_blocked` audit 已纳入同一 batch，profile/audit 任一失败都会回滚结算、周期预算、wallet lot 和审计，重试保持唯一 settlement/profile block。受限 APP 六协议新增过期 unknown 恢复不重生成、重复恢复和幂等收费一次专项；周期预算超界也验证了原 allocation/window、回滚和 profile 拒绝。上述均为 55439 本地 fixture 证据，真实 supplier late、独立 LOGIN、浏览器、全量 guard 和部署仍未形成闭包，U/008复选框保持未勾。

2026-11-08 028-U/008周期预算续片：现行 024 fixture 的完整 route/catalog 种子、非零 route revision、额外 channel 与成本 operation 已接通，`internal/periodicbudget` 整包 race 在 55439 专库通过；冻结历史/prefix fixture 未升级。008 固定 binary 运行器随后用不同 SHA-256 的 before/after binary 串行跑完三对样本，六次均通过请求、结算、账本和资源断言。该结果补齐 008 的 SIGKILL 与固定 binary 局部证据，不替代独立评审、最终预览或 U 的完整存储与商业出口。

2026-11-08 028-U入口守卫续片：024 请求记录 guard 纳入 request HMAC、streaming、route revision 等报价身份字段，防止快照出生后改变请求报价身份；人工核对 HTTP 的 `settle` 动作明确映射为账务 `propose`。迁移与 server 定向 race 在 55439 通过。attempt quote 的发送态完整不可变守卫、supplier quote 绑定和 U 完整九组 guard 仍未完成，复选框保持未勾。

2026-11-08 028-S/A/R 纵向接口续片：S 新增 profile registry、显式发布授权、provider adapter、原 request/attempt/observation/settlement 引用和未知消费禁止重发；A 新增持久化 Store/TaskTx 合同、内存实现、worker lease、provider task adapter、幂等提交与制品 grant；R 新增 SessionStore/TurnStore/EventStore/Persistence 合同、内存实现、provider adapter、收费轮次引用和 pending 禁止重发。三片仍未接入 HTTP/WS、PostgreSQL writer、wallet/billing 结算和 worker 运行入口，复选框保持未勾。

2026-11-08 028-S/A/R 当前存储片：024 追加 `media_tasks`、任务事件/制品引用、`realtime_sessions`、`realtime_turns`、`realtime_events`，加入同租户原 request/reservation/attempt/source 绑定、状态闭集、事件去重、终态和 session 单 inflight guard。`ProvisionFor` 为这些关系增加 APP SELECT/INSERT/UPDATE、禁止 DELETE，projector/PUBLIC/payment worker 无权限。迁移与角色边界专项通过 55439。HTTP/WS、PostgreSQL service writer、mock provider、媒体金额/预占/结算接线仍未完成。

2026-11-08 异步任务 HTTP 续片：`/v1/tasks`、`/v1/tasks/:id`、`/submit`、`/cancel`、`/result-grant` 已接 API Key 鉴权、当前 Postgres task store、任务状态机和 mock video adapter。请求要求原 request/reservation/attempt/source 绑定并保留幂等摘要，提交沿 `submission_unknown` 禁止重发边界。worker、结果正文持久化、账务预占/结算和端到端任务恢复仍未完成。

2026-11-08 异步 worker 续片：`media_tasks` 增加 lease owner/kind/expiry，Postgres Store 支持 `Claim/RenewLease/ReleaseLease`，`tasks.Worker` 按 submit/observe/cancel/expire 调用现有状态机。租约过期可被下一 worker 接管，生成提交仍受 task unknown 禁止重发约束。worker 进程入口、重启专项、统一账务收尾和对象存储仍未完成。

2026-11-08 worker 部署续片：新增 `moni-aigate worker` 命令和 Compose 独立 worker 服务。worker 只使用当前 task lease 与 mock adapter，未获得计量表、wallet 或 payment worker 权限。`docker-compose config` 通过，真实容器启动、worker 重启恢复和统一账务收尾仍待验收。

2026-11-08 媒体商业入口续片：024 追加当前 `media_model_profiles`、request/attempt/observation、reservation/settlement 和两侧 funding-lot edge。`billing.Store` 复用既有账户锁、journal、FIFO lot 和 wallet event validator，支持媒体预占、confirmed usage 收费、unknown/failed 释放、幂等和未知 attempt 禁止重发。图片 HTTP 与 TTS HTTP 在有数据库的 server fixture 已各完成一次真实 media reservation、attempt、observation、settlement 和 wallet 守恒核对；Realtime 有数据库路径接入单 turn media reservation/settlement，纯 WS fixture 保持 pending。异步 video task、媒体结果持久化/对象存储、数据库 session/turn writer、真实供应商、Compose 容器运行和完整部署验收仍未完成，S/A/R 纵向复选框保持未勾。

2026-11-08 当前 MVP 纵向接线续片：异步 `video_generation` 的创建、提交、观察、取消与过期路径现在通过现有 `media_model_profiles` 和 `billing.Store` 复用 Reserve→Attempt→Observe→Settle。HTTP 创建按原 request/reservation/attempt/source 绑定预占，提交前进入 sending，终态成功按固定 mock task usage 收费，失败、取消、过期和 submission unknown 释放且不重发；worker 通过租户 scoped adapter 和幂等 expiry hook 使用同一路径。PostgreSQL task store 同时修正 nullable artifact/provider 读取、事件 raw SHA-256 canonical 和结果窗口边界。55439 race 已通过任务成功/unknown 释放/重复观察/唯一结算专项。独立 worker 重启、对象存储、真实供应商和 full task acceptance 仍未验收。

2026-11-08 当前 MVP 结果与实时持久化续片：新增不可变 `media_result_artifacts`，图片与 TTS 在结算后保存受保护 inline bytes、media type 和 SHA-256，重置进程内缓存后按 tenant/user/key/request 或原 idempotency key读取，重复读取不创建第二 settlement。Realtime 有 DB 时使用租户绑定的 PostgreSQL persistence 写入 `realtime_sessions`、`realtime_turns`、`realtime_events`，保留事件幂等、状态迁移、单 inflight 和原商业引用；纯 WS fixture 仍保留内存路径。该片的 55439 migration/role/media HTTP/realtime/reporting 定向 race 已通过，provider session 重启恢复、真实 socket 故障和完整 commercial matrix 仍未验收。

2026-11-08 当前 MVP 报表续片：经营报表把 `media_requests`、唯一 `media_settlements` 和全部 `media_attempts` 纳入同一只读快照。媒体收入只按 request 计一次，当前没有供应商成本字段时保留 unknown，不把 `raw_amount`解释为成本；支持 media operation、同步/异步 execution_kind 和安全模型筛选。报表接缝已通过 55439 race，但任务/实时完整写入、供应商成本和浏览器验收仍未完成。

2026-11-09 异步任务重启续片：`media_task_artifacts` 现在在成功 metadata 后支持一次性写入并校验正文 SHA-256，Postgres Store 读取已保存正文时不再调用 provider。新的 Store、新 grant 和拒绝下载 provider 已验证正文跨进程读取；新的 worker/provider adapter 可从持久 task 状态观察并结算一次，不重新提交生成。该片仍使用 deterministic mock provider，真实供应商 task handle、对象存储和容器重启验收仍未完成。

2026-11-09 Realtime 重启与 Compose 续片：`realtime_sessions` 持久化 provider session identity，`realtime_turns` 持久化 provider handle、dispatch unknown 和 cancel 状态；新的 persistence/adapter 已验证 Receive→Settle 不重复 Dispatch。`scripts/deployment-qa.sh` 在 Docker daemon、PostgreSQL 16.15、本地构建镜像和临时 Compose 项目中通过空库迁移两次、provision 两次、bootstrap、防重复 bootstrap、app/worker 启动、live/ready、app/worker/PostgreSQL 重启。目标 1Panel、真实供应商、浏览器和真实业务调用仍未验收。

2026-11-09 Realtime HTTP/媒体绑定续片：新增 55439 真实 HTTP/WebSocket 专项，覆盖 API Key、单 session/单 turn、输入/响应事件、UUID event、重复 charge event 幂等、provider 调用次数、Realtime 持久化和 media settlement。修正 024 的 turn binding guard，使文本 turn 严格绑定 `request_records/reservations/upstream_attempts`，Realtime 媒体 turn 严格绑定 `media_requests/media_reservations/media_attempts`，禁止两套事实跨接；复用 `ReserveMedia` 返回的真实 reservation/attempt identity。该专项通过，真实供应商、浏览器和完整六文本/媒体商业出口仍未验收。新增 `scripts/deployment-business-qa.sh` 只做已部署环境的 health/auth/model/text smoke，并明确不伪造 wallet、任务商业引用或 WebSocket 结果。

2026-11-09 Realtime guard 反例续片：在 55439 真实 PostgreSQL 上验证同一 request ID 下，media reservation + text attempt、text reservation + media attempt 两种混接均在 turn INSERT 时回滚，session/turn 无残留。该证据补强双来源绑定边界，不替代完整 028-U guard matrix、六协议商业出口或部署业务验收。

2026-11-09 异步任务引用可发现性续片：客户请求详情现在返回原不可变 `profile_version`、`reservation_id` 和首个 `attempt_id`，资金详情继续返回可用于 task `source_id` 的 funding lot identity。新增 55439 HTTP 专项验证文本请求后这些事实按租户 owner 规则可读取，未暴露凭据或供应商正文。task API 仍要求沿该原事实提交，未知引用不会被服务端伪造。

2026-11-09 异步任务公开链续片：`TestVideoTask*` 集成夹具已改为通过 owner-scoped `/api/requests/:id` 和 `/api/requests/:id/funding` 获取 task 引用，再调用 `/v1/tasks`、submit、observe、cancel 和重启恢复路径。55439 race 通过，任务仍沿原 media wallet settlement，不新增第二钱包或内部 SQL 旁路。

2026-11-09 U guard 矩阵续片：新增 55439 PostgreSQL race 专项，覆盖跨租户和跨 request/attempt 绑定、snapshot/bounds/quantity/observation/finalization 不可变、观察来源与错误 attempt、成本来源指针互斥、资金边界、失败根事务回滚，以及计量表和 `meter_%` 函数的 app/projector/payment-worker/PUBLIC 权限。该矩阵补的是当前 024 的负控证据，完整九组 guard 和旧金融事实拒迁仍保持未勾。

2026-11-09 Compose 业务 profile 续片：新增显式 `business-qa` mock provider、HTTP provisioning 脚本和部署 smoke 自测入口。profile 默认关闭，不改变生产 Compose；provision 通过管理 HTTP API 创建当前模型、mock 渠道、路由、默认价、租户、双人充值和客户 Key。`DEPLOY_QA_BUSINESS=true scripts/deployment-qa.sh` 现在可以在同一临时 Compose 项目中执行 provisioning 和文本 smoke；图片/TTS/任务/Realtime 的 Compose 实跑仍未勾选。

2026-11-09 Compose 业务 profile 实跑续片：在同一临时 Compose 项目中实际通过文本、图片、TTS、异步 video task 创建/幂等提交/worker 完成/结果下载和 Realtime WebSocket session/turn，随后 app/worker 与 PostgreSQL 重启恢复通过。该证据使用 deterministic mock provider，只闭合本机开发 Compose 主链；正式供应商、浏览器、1Panel、HA、性能和支付生产验收仍未完成。修复了 APP SELECT-only 媒体档案的锁读、媒体约束函数执行权限和 task store 对 LATERAL 外连接的错误锁目标。

2026-11-10 当前主线收口复验：`DEPLOY_QA_BUSINESS=true DEPLOY_QA_PORT=18090 scripts/deployment-qa.sh` 在更新后的 `codex/initial-mvp` 通过，覆盖空库 migrate、重复 migrate/provision/bootstrap 拒绝、文本、图片、TTS、异步 video task、Realtime WebSocket 和 app/worker/PostgreSQL 重启恢复。该证据仍是 deterministic mock provider 的开发环境内测，完整 U guard/六协议商业全矩阵、真实供应商、浏览器和生产部署不因此勾选。

2026-11-09 图片专项复核：图片 POST 在数据库中先重放已完成 `media_result_artifacts`，进程缓存清空或进程重启不会重新 Reserve、提交 provider 或创建第二 settlement；图片 GET 在数据库配置下不再使用过期或缺失 artifact 的内存完成缓存。新增跨租户/用户/Key 访问拒绝和未发送失败终态测试，验证命令与 mock/真实 PostgreSQL 边界见[028-U验证记录](../../docs/verification/unified-metering.md)。

## S / A / R / 交付

- [ ] S：冻结一个当前可认证、完整inline图片或TTS profile，端点→有界结果→同一结算→账单/报表纵向验收；再补图片编辑/STT专有合同及实现。
- [ ] A：明确供应商任务合同、持久任务/观察、未知submit不重发、结果所有权/过期/有界下载与唯一结算；poll/下载不计第二笔收入。
- [ ] R：会话与收费轮次分离，首收费帧前预占，禁止自动生成旁路；断线/取消/未知量与轮次账单/采购/来源闭合。
- [ ] 正式模型渠道、选定支付商、Docker/1Panel、备份恢复与部署验收；本地fixture不代替这些结果。
- [ ] 在完整商业检查保留的条件下建立新性能基线，分别度量首字节与首有效内容，再做同环境竞品对比。

客户/渠道经营、商品/套餐、原路退款等其余MVP事项仍按[总交付计划](../../docs/design/delivery-plan.md)逐片推进，不因U或某个媒体端点通过自动视为完成。

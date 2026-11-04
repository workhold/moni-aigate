# 业务能力与并行交付计划

2026-11-02。目标是对外API商业运营：六协议统一接入、渠道经营、客户权限/钱包/账单，以及可解释的上游采购成本和调配。业务完整性优先，性能专项后置。当前系统视图见[统一架构](../architecture/README.md)，逐项产品差距见[New API路线图](new-api-roadmap.md)。本文件给出依赖与工作边界，不把规划当成交付。

## 当前执行顺序（2026-11-04 用户调整）

本节与[ADR-005](../decisions/ADR-005-pre-mvp-single-path.md)优先于下文历史排期：MVP 前以架构完整、设计合理和用户可用能力为重点，不承诺未发布版本兼容。新功能只使用现行正式模型与领域入口；开发库可明确重建，不新增双写、自动版本探测、旧格式分支或失败回退。历史记录中的未完成项保持事实，不再全部作为业务功能的串行门禁。

| 优先级 | 下一工作 | 完成出口 |
| --- | --- | --- |
| 当前收口 | 已实现采购回执观察器的专项、文档与推送 | 交付准确范围后冻结隔离候选，不继续扩展 legacy/历史兼容矩阵 |
| 1 | 经营报表：请求时间窗口、模型维度收入/退款/成本、渠道采购成本、对账导出 | 平台控制台与实际数据查询接通；未知成本不当零，客户收入不因多attempt重复，金额精确，权限隔离 |
| 2 | 客户与渠道经营、商品/套餐及选定支付服务 | 每片按场景补必要合同，领域→HTTP→控制台纵向交付；不依赖离线全历史proof或旧profile兼容 |
| 3 | 当前版本单实例部署、首次配置、备份恢复与实际供应商使用 | Docker/1Panel可复现手册及实际环境验证；缺目标环境/密钥的门单列，不阻塞本地能力 |
| 后续 | 原路退款/拒付、媒体/异步/长连接、HA与完整历史认证 | 按明确产品范围独立排期，未完成不写成支持；性能在主要能力稳定后优化 |

资金正确性、租户隔离、硬预占、唯一结算、报价快照和未知消费不重发模型请求继续是现行架构不变量。此前C0/B-P3/B-P4候选、兼容profile及全历史证明暂缓扩展；普通serve路径没有因本次排期切换为候选，也不因暂缓而被标作验收通过。

## 018–020 已交付工作面

| 工作面 | 先补什么设计 | 实现边界 | 汇合点 |
| --- | --- | --- | --- |
| A 渠道采购配置与选路 | [018规格](../../specs/018-cost-aware-routing/spec.md)：不可变路由策略、未知成本、两维单价上限、比较分数 | 013迁移、catalog、routing、管理API；金额与完整路由CAS | 与B固定CostQuote、策略及失败码后并行 |
| B 派发和资金安全 | 候选→attempt→sending前报价一致；当前时刻与锁序；已发收尾 | billing、gateway、真实PG竞态与六协议HTTP验证 | A的报价/策略类型和SQL字段；禁止共用收尾门禁 |
| C 控制台经营 | 完整编辑、冲突/不明结果状态机、无损金额、身份/对话epoch | web独立路由与成本编辑器、前端测试 | API固定后可与A/B并行；最终真实main浏览器联调 |
| D 架构持续同步 | 统一视图、源码/SDD映射、内容快照与复核记录合同 | docs/architecture、AGENTS、检查器、Make/CI | 全部源码稳定后集成人复核正文并刷新；CI始终只读 |
| E 采购账户实施 | [019规格](../../specs/019-procurement-accounts/spec.md)与[详细设计](procurement-accounts.md) | 014迁移、永久归属、累计预算、逐attempt预占、独立unknown/恢复、双审与人工观察 | 019已完成限定本地验收；保持与018单价门禁独立 |
| F 公开模型与售价 | [020规格](../../specs/020-public-catalog/spec.md)与[合同](../../specs/020-public-catalog/contracts.md) | 015迁移、默认私有、匿名安全投影、单DB时刻选价、独立公开CAS、价格页 | 020已完成限定本地验收；公开展示不授予调用权限 |

所有工作面共享协议profile、客户收费和恢复合同。修改这些公共合同须先回到规格，由集成人统一决策；各工作面独立改文件，最终做交叉审查和集成验收。并行不代表同时升级预览、执行数据库角色维护或占用同一真实main单实例锁。

本轮A–D已完成实现及限定本地集成验收：[018记录](../verification/cost-aware-routing.md)包含真实PG竞态、122项前端测试、隔离浏览器CAS/两渠道调用/资金核对、QA清理与013迁移后的预览核对，以及最终重建的目标补验；统一架构已由集成人建立源码、设计与证据复核基线，make check与只读内容检查通过。E依据[019冻结合同](../../specs/019-procurement-accounts/contract.md)已实现，并通过[019限定本地验收](../verification/procurement-accounts.md)；141项前端/真实PG并发与故障/隔离浏览器及014预览升级通过。

F依据[020验收](../verification/public-catalog.md)完成匿名/管理员HTTP、156项前端及strict构建、最终相关PG race、隔离真实main浏览器与015预览升级。此前全Go race早于最后SQL安全修订，最后改动由catalog/server相关专项覆盖，不把旧全量通过当作最终源码全量复测。运行器记录零模型调用、零客户资金事实并验证资源清理；58123首页与35资源和最终包一致。现有公开布局可供021接线，021接续该布局完成注册/恢复，支付沙箱APP闭环现由022-B3接线并验收，正式支付另有门禁。

## 接下来可并行与必须串行的工作

| 工作流 | 可并行部分 | 必须先完成的依赖与验收 |
| --- | --- | --- |
| 采购账户/共享预算 | 账户配置与余额观测适配器、控制台、对账查询可在合同冻结后并行 | 账户/凭据不可变绑定→逐attempt持久采购预占/未知消费收尾→预算过滤；先确定统一锁序、故障恢复，不能把供应商余额观测当强限额 |
| 自助经营 | 020公开价格已交付；021注册/邮箱恢复已交付，022-A共同入账/双审兑换券已完成限定本地验收 | 021已通过限定本地验收；订单/收款主体/幂等事件和权益模型先冻结，支付沙箱验证后再接正式支付商；订阅依赖周期权益、预占来源与退款合同 |
| 渠道经营与报表 | 标签/筛选、只读导出、渠道告警可与采购资金实现分工 | 先明确导出权限/隐私和unknown统计口径；不能把未知成本算0后展示“完整利润” |
| 新供应商与媒体 | 供应商适配可按独立profile并行；媒体/异步任务先写独立状态机 | 各家硬计量上界、最终usage与无消费证据认证；任务幂等、取消/回调/结果期限不能照搬文本调用 |
| 生产交付 | Docker/1Panel演练、备份恢复、供应商SDK认证可分人执行 | 使用隔离资源和固定版本，升级排空/恢复门禁共同验收；许可证决策保留，不能以本地mock替代生产结果 |
| 性能与HA | 业务稳定后再按002复测；HA可先设计 | 保留完整商业检查、先完成共享限流/租约/恢复设计，不取消单活锁换取吞吐；竞品实跑之前不宣称接近 |

每个可编码切片都走“spec → HLD/LLD/状态机 → 接口与迁移合同 → 实现与测试 → 验证记录 → 架构正文/演化记录同步”。发现缺失合同先补设计；依赖尚未冻结的功能保持planned，避免边写资金代码边猜业务规则。

## 持续执行阶段与当前顺序

| 阶段 | 当前状态 | 下一步与依赖 |
| --- | --- | --- |
| 019 采购账户 | 已实现，限定本地验收通过 | 周期/正式余额适配后续；不阻塞自助经营 |
| 020 公开目录 | 已实现，限定本地验收通过 | 默认私有、独立公开授权/匿名DTO、确定售价、CAS与015预览；不授予调用、不锁定未来请求价格 |
| 021 自助身份 | 已实现，限定本地验收通过 | [证据](../verification/self-service-identity.md)；注册/邮件默认关、最小权限开户/当前MFA、全会话撤销、独立worker/outbox，正式收件仍待外部 |
| 022 自助支付 | A共同入账/双审券已完成限定本地验收；B0-P及M/Q独立沙箱已编码，专项真实PG/race通过，真实进程短程通过、旧版本20代长程已完成，最新补丁/精确故障继续收口；B1核心设计/SQL已冻结，B2/Go第三来源已实现并通过专项，B3订单/worker/HTTP/UI/main已接线，真实PG专项及两轮真实主进程/最终浏览器整链通过限定本地验收，真实在途SIGTERM与Guard失权补验已通过；C0纯分配/history/同RR账本reader及SQL候选专项已通过，完整静态checker三API与ordinary/sandbox两套正式root已实现，五ordinary变体和完整sandbox的最终leaf真实PG race已通过并保留原五秒预算，整仓make check及verify-mvp已通过；T2库级维护Permit已实现，[专库race专项](../verification/maintenance-authority.md)已通过；库级候选[protected phase与epoch](../verification/protected-init-phase.md)、[独立停服epoch观察](../verification/epoch-observer.md)和[APP runtime](../verification/runtime-composer.md)已实现，新预算证据组合按023-B-P3继续；正式迁移/CLI与全资金路径仍后续，C1退款、C2拒付/债务及C3交叉/恢复已有候选，C尚未接入资金事务 | [B0运行证据](../verification/payment-sandbox.md)→应用支付资金→来源lot/分配后才开放原路退款；正式支付商另门禁 |
| 023 周期与商品 | A完成限定本地验收；B核心/HTTP/UI、浏览器、020最终相关PG race及预览升级通过；独立三态Verifier/CLI专项通过；B-P3预算观察证据的完整V3库级组合已实现，验收按[本片记录](../verification/periodic-budget-prefix-profile.md)继续；[C-V纯价值/锚点月合同](../../specs/023-periodic-products/c-value-contract.md)已冻结实现，无资金或生产接线；C商品购买仍未实现 | [B核验合同](../../specs/023-periodic-products/b-verifier-contract.md)、[验证](../verification/periodic-budgets.md)；完整预算证据/资金来源验收后发布正式迁移与在线接线，再订阅/套餐 |
| 024 扩展平台 | [独立草案](../../specs/024-extension-platform/spec.md)与[A0精确告警合同](../../specs/024-extension-platform/a-contract.md)已冻结019，生产代码及专项/最终全量race、浏览器和019预览通过，架构检查通过 | 真实PG/HTTP、双槽worker与UI逐项验证；真实main故障/浏览器已通过，见[记录](../verification/operations-alerts.md)。B0-P纯计量层已冻结实现并通过race/vet/fuzz，尚未接入provider/billing；媒体/异步任务/长连接、外部身份按认证与计量能力逐片交付 |
| 025 HA与生产 | [独立草案](../../specs/025-production-ha/spec.md)已完成；仅A0配置透传及实际CLI渲染、A1-M邮件真实进程窄片已完成限定本地验证，A1-G模型真实main运行器按[精确合同](../../specs/025-production-ha/a1-model-process-contract.md)实施，真实两窗口仍未验；完整HA未冻结未实现 | [共享执行候选](../../specs/025-production-ha/b0-shared-execution-contract.md)已补成员epoch、完整资源边、保守unknown及精确整数桶；生产SQL/角色/接线未冻结。容器/1Panel和灾备；021指定claim/SMTP接受至ACK窗口真实SIGKILL及原60秒lease重启已通过限定本地验收；不得直接移除单活保护 |
| 最后阶段 性能 | 002/008后置 | 完整商业检查保留，重新建立基线并优化；同机同协议同用量条件才能与Bifrost/LiteLLM比较 |

本次持续目标覆盖后续阶段；阶段验收不是整个目标完成。缺正式凭据或运行环境时如实保留该门禁，继续其余可执行工作。

022-B3当前接线与分层执行范围见[应用支付记录](../verification/payment-application.md)。真实main验收在专用测试库串行占用InstanceLock；独立前端/文档/C0设计可以并行。最终汇合后才更新架构清单并跑完整检查，之后继续C0→C1/C2→C3、023-C、024余下切片、025和最后性能阶段。

后续022的资金候选集中于[C0生产验证](../verification/funding-lot-candidate.md)、[独立重放](../../specs/022-self-service-payments/c0-verifier-contract.md)、[C1退款](../../specs/022-self-service-payments/c1-refund-contract.md)、[C2拒付/债务](../../specs/022-self-service-payments/c2-dispute-contract.md)和[C3交叉/恢复](../../specs/022-self-service-payments/c3-cross-recovery-contract.md)。025邮件真实进程按[A1-M合同](../../specs/025-production-ha/a1-mail-process-contract.md)单独验收，不能从SMTP本地持久写入推断已命中生产ACK之前的窗口。

用户已确认正式模型渠道范围为OpenAI/Codex、Claude、DeepSeek、Gemini、GLM及硅基流动等第三方平台；逐渠道协议/计量/采购/验收矩阵见[渠道认证计划](provider-qualification-plan.md)。它们分别登记profile与报价，不因同名模型或兼容接口继承官方计费认证。支付商和1Panel目标主机仍未确定，本地实现继续。

可并行的C1-P纯退款协议已完成，见[合同](../../specs/022-self-service-payments/c1-protocol-v2-contract.md)与[专项证据](../verification/payment-refund-protocol-v2.md)；独立Python两条v1桥、四摘要域及九个签名向量与Go交叉通过，race/vet/短fuzz通过。它未被普通main调用；C1-M/S/I、退款留置/资金、永久拒绝墓碑、C2/C3及生产验收仍在后续。当前C0静态checker已实现，最终集成验证与后续[Permit](../../specs/022-self-service-payments/c0-permit-contract.md)、[运行helper](../../specs/022-self-service-payments/c0-runtime-helper-contract.md)仍分别交付，核心串行关口继续保留。

C0的[离线元数据导出窄片](../../specs/022-self-service-payments/c0-schema-check-contract.md#9-根冻结窄片metadata-only-离线-descriptor-生成器)已实现并通过[专用PG验证](../verification/funding-lot-descriptor.md)：ordinary/sandbox分别before/after，两轮十文件同bytes，失败DDL后精确清理通过。该工具的原导出物保留未审候选身份；新checker的两套正式root经独立实装、来源交叉核对与根审后固定嵌入。精确类型权限已校准：32个自动row/array的默认USAGE不授表读取，显式proof类型权限单独核。

完整静态叶子模块`internal/fundinglotcatalog`已实现`VerifySnapshotTx`、`VerifyInstallationTx`、`VerifyRuntimeTx`及同事务opaque证据：ordinary为实际owner/APP/projector三角色，固定五种named/public与owner属性变体；sandbox覆盖application/Mock/Receiver三schema、八角色与十组跨域负权，另核runtime全局无owner及目标数据库无CREATE/授权提升路径。两套SHA固定root、封闭DTO、原函数正文、结构/ACL/依赖与资源界已落代码，见[collector记录](../verification/funding-lot-catalog-collector.md)及[peer记录](../verification/funding-lot-catalog-peers.md)。最终leaf串行真实PG race已通过（217.140秒），三个发行API保留原五秒总预算；47个顶层测试、179个终态中176 PASS，3个独立capture/timing/export为显式opt-in跳过且另有专项证据。精确清理确认本轮schema/role/DB/backend归零，public与DB ACL保持原样，见[最终leaf验证](../verification/funding-lot-catalog.md)及[机器记录](../verification/funding-lot-catalog.json)。本轮整仓`make check`/`make verify-mvp`已由集成人执行通过。

当前正式root仅覆盖**PG16.15/Homebrew initdb catalog-profile、UTF8与C/C环境**，其中PL/pgSQL language OID 14038为该initdb分配值；任意PG16、其他locale/发行版及1Panel部署仍需独立profile验收，未知profile失败关闭，详见[正式root边界](../../specs/022-self-service-payments/c0-schema-check-contract.md#104-首次正式root冻结与已验环境)。该leaf尚未接普通main；T2已由原Snapshot消费其owned证明发行维护Permit，资金epoch或独立全源RuntimeProof仍未实现；维护初始化、来源/lot全图核验和全资金路径接线继续串行推进，022-C及全部后续阶段仍未完成。

维护T0按[根冻结§10](../../specs/022-self-service-payments/c0-permit-contract.md#10-根冻结t0专用连接双session-gate与清理)新增`internal/maintenancetrust`：专用物理连接、非权威身份预检、402→401双session锁与独立有界逆序清理；失败立即关捕获socket，副本共态及晚到dial封闭。生产代码已独立读审，专项范围见[验证](../verification/maintenance-gate.md)与[细化架构](../architecture/maintenance-gate.md)。本片没有Snapshot/Permit/main或资金权；T1–T4须先冻结首次RR/账户锁顺序、全程scanner lease/deadline、可信manifest及完整v1前驱字段矩阵，之后再继续维护与全源组合。

本轮GitHub入口交付汇合T0最终专项（54.205秒、53 PASS）、README/部署入口和19图；根最终make check 6.767秒、make verify-mvp 339.394秒通过，286项前端、全Go默认race/vet/build及明确测试库已执行。期间B0测试将合法deferred lease误判为HTTP槽数，按根冻结§10.3仅修测试并补独立多连接回归，原失败与最终验证分别保留，见[交付记录](../verification/github-initial-delivery.md)。无普通main、迁移、权限、资金或生产验收范围扩张。

C0-V0-L的[纯账本行检查](../verification/funding-ledger-rows.md)已实现并通过race/vet及短fuzz；只核输入行，不读数据库，不发完整coverage或proof。后续[caller-owned RR ledger reader](../../specs/022-self-service-payments/c0-ledger-reader-contract.md)已实现并通过[真实PG/race专项](../verification/funding-ledger-reader.md)，按三表全部行/128keyset/EOF及原journal业务列读取；独立来源/Key/采购/PB/B和lot序列仍须同snapshot完成，不能用旧VerifyLedger中的写侧SQL谓词作为新oracle。

2026-11-02后续维护片：根已冻结[T1 Snapshot 生命周期](../../specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md#10-根冻结范围与尚未开放的能力)，已实现私有单次RR、ONLY125/首次快照、固定catalog stage、全程busy/绝对期限及提交结果分类；最终[真实PG/race](../verification/maintenance-snapshot.md)115.412秒、124终态全PASS，最终整仓make check与指定专库make verify-mvp已通过，见[集成记录](../verification/maintenance-snapshot-integration.md)；新四分区远端CI另验。T2完整history/维护身份/[前驱矩阵](../../specs/022-self-service-payments/c0-predecessor-contract.md)已按[真实授权合同](../../specs/022-self-service-payments/c0-authority-contract.md)实现，新增[专库race专项](../verification/maintenance-authority.md)已通过；T3独立来源、T4 basis/epoch继续后续，无资金或生产能力开放。

GitHub第二轮41339443048已失败并保留完整JSONL：身份通过，两个大包仍因累计600秒超时，支付模拟器被时区相关静态文本校验拒绝。只调整CI为十个完整动态分区与显式数据库时区，原始M/Q Open隔离复现已通过；新远端结果单独核验，见[第二轮证据](../verification/github-ci-second-run.md)。未新增产品/测试源码、迁移、金融或生产权限。

2026-11-02第三轮[GitHub CI](../verification/github-ci-third-run.md)已对精确f64b56f源码十组全部通过，1036顶层PASS/54明确SKIP、web286全PASS；T2新增代码不沿用该结果。T2并行实现已汇合真实manifest/history、LOGIN与新鲜停服采样、完整前驱、Init全账户锁及私有Permit，保持无生产CLI/资金写入/全来源/epoch的边界；专项与最终回归单独记录。


2026-11-02并行片：采购ScanProcurement按[成本双审合同](../../specs/022-self-service-payments/c0-procurement-approval-contract.md)扩为十五表/三个局部摘要；023-C-V仅独立精确价值/期限算法；025-A1-G仅私有fixture与实际主进程故障运行器。前两项无新增资金写入，后者无生产fault hook。当前TEST_DATABASE_URL、C0_MAINTENANCE_DATABASE_URL及模型主进程opt-in均未配置，真实PG/模型SIGKILL不记通过；本地验证与最终集成结果见各自验证记录。完整全来源、basis/epoch、退款/拒付、商品购买、正式部署与性能仍按原依赖继续。


2026-11-02采购历史增量：按[十九表读取合同](../../specs/022-self-service-payments/c0-procurement-history-read-contract.md)在原ScanProcurement内增加commands、balance observations、audit与projection outbox，新增[命令/观察](../../specs/022-self-service-payments/c0-procurement-command-contract.md)与[成本审计/消息](../../specs/022-self-service-payments/c0-procurement-cost-journal-contract.md)两个局部core。audit只读一次，十九表共用原预算/期限且Finish一次，无迁移/在线资金权限变化。evidence历史保存的缺口和不可重建范围先存入[研究记录](../research/procurement-evidence-lifecycle.md)，不从摘要或最终行猜回历史。

同session owned raw graph、全局共享资源和私有reader完成记录现已按[21表合同](../../specs/022-self-service-payments/c0-financial-read-session-contract.md)冻结实现，人工/券及CustomerKey独立图已纳入原ScanFunding四局部摘要；下一串行关口是同session继续完整sandbox、采购evidence/事件与PB独立图及跨域关系，最后提供固定pre/post phase组合及唯一Finish；现五个分别Finish的局部reader不能拼成全来源proof。随后同pre snapshot核basis，再冻protected init writer/post replay/epoch及实际APP接线。原退款/拒付、商品、扩展、生产/HA和性能阶段继续保留，当前没有标记全目标完成。本轮测试状态见[采购验证](../verification/procurement-budget-maintenance.md)，历史验证不追溯升级。


本轮[资金核验记录](../verification/funding-financial-graph.md)区分纯core、固定21表reader/维护封装及真实PG验收。真实Plan/Init fixture已改用正式Store.Reserve并补成员模型授权，全部业务历史先于候选C0触发器安装；专库未配置时明确跳过，不能据Go测试包成功宣称资金维护切换通过。README继续按当前dev状态说明使用、部署和能力边界，交付代码与统一架构同步推送。


2026-11-02完整来源依赖增量：已冻结并实现[采购证据图](../../specs/022-self-service-payments/c0-procurement-evidence-graph-contract.md)、[PB原始图](../../specs/022-self-service-payments/c0-periodic-budget-graph-contract.md)与[支付stored-verified来源图](../../specs/022-self-service-payments/c0-sandbox-source-graph-contract.md)三个纯Go核心，分别包含23/18/33组原行与明确历史缺证。它们尚未接入完整同session reader，不将五个旧局部ScanProof拼成全来源证明。下一串行关口仍是固定列并集、模式/phase、完整EOF与唯一Finish，再继续basis/epoch和APP资金接线。

原21表资金/五类维护reader的[真实PG补验](../verification/maintenance-financial-postgres.md)在专用PG16.15环境执行：修正fixture连接池关闭、双审后的采购余额预期，以及合法核款后的legacy基线；原仓库最终完整race专项210.188秒全PASS。历史缺环境而SKIP的记录保持，补验不扩大供应商、正式支付、部署或全目标验收范围。README保留GitHub首页特性/使用/部署/许可说明并精简维护内部细节，新增贡献指南；代码、设计和统一架构在根汇合检查后共同推送。

本轮整仓发现并修复支付/兑换券共用commerce命令的冗余唯一索引并发冲突；保留完整输入核对与原期限，不新增23505重试。两既有八并发用例以共同屏障复验，结果及main测试数据库清理诊断见[并发集成记录](../verification/commerce-concurrency.md)。全仓结果按最终实际执行更新，不将专项成功当作整轮通过。

最终原仓库`make check`、指定真实专库下`make verify-mvp`均PASS（后者959.665秒，286项前端/全默认Go race/vet/build）；两商业命令并发专项各30轮、billing/payments全包与两main原例另行通过。默认全仓不打开C0独立opt-in，210.188秒维护专项保留独立归属。下一完整reader合同须先解决native时间与共用旧DTO的保真转换、物理列并集和统一资源计数，再实施同session完成矩阵与唯一Finish；不以旧局部proof或空支付DTO代替完整范围。

## 68表完整业务来源读取切片

先冻结[完整读取合同](../../specs/022-self-service-payments/c0-complete-source-reader-contract.md)，再实现fundinglotscan固定68表/七算法、ScanCompleteSources可信模式和唯一Finish。旧五种局部proof不相加，物理EOF与coverage分开；原生坏值不伪造DTO/算法执行。根汇合三路列/游标审查、fake边界、真实PG SQL与真实Plan/Init后同步README/HLD/LLD/统一架构并提交推送。

下一依赖为lot全序及protected pre/post组合，随后basis/epoch与全部APP资金路径；C1/C2/C3、商品、扩展、生产/HA及性能继续保留，不把本业务来源范围标为全目标完成。

下一切片先冻结独立纯Go lot全钱包重放，使用业务源与16张C0表原行、独立big.Int按seq推导FIFO/结算/逆序退款；不接writer或维护authority。随后另冻pre（含109表basis）与post（重扫source+lot）封闭入口。根须先裁决once位图容量、来源全局10万行/16MiB与候选较大重放预算、basis计数/分页/超长值界、未知init提交和v2严格codec，不能用68表摘要代替basis原件。该计划仍是设计工作，不标为已实现。


## V0-WS前置全序切片与剩余依赖

根先按[七投影合同](../../specs/022-self-service-payments/c0-wallet-sequence-rows-contract.md)冻结纯序列声明检查，实施与手算测试并行，根汇合后复核、更新架构与[验证记录](../verification/wallet-sequence-rows.md)。该片只解决完整重放的结构前置，不包含FIFO/分配/退款/16表reader，也不升级68表来源结果。

原业务源+16表的独立lot全钱包纯重放已按下节V0-WL冻结实施，复用结构检查但不从声明自我初始化金额。之后封闭pre+basis与init/post/epoch。既有六kind的uint8 once容量、统一源+lot预算、109表record_send basis计数/分页/超长/取消界、未知init提交及严格v2 codec仍须在组合实施前明确冻结；本片不消耗新kind或声称较大候选预算已支持。全资金接线后继续C1/C2/C3、商品、扩展、生产/HA，最后性能；整体目标仍未完成。

## V0-WL实际 lot 域重放与下一依赖

已按[原行重放合同](../../specs/022-self-service-payments/c0-wallet-lots-rows-contract.md)实现独立 `CheckWalletLotsRows`：25组输入覆盖九组业务原行与16张C0事实表，从真实金额按seq推导旧开账、新来源、FIFO reserve、正序finish和逆原charged边分次refund，比较全部事件/边/cache。实现、独立硬编码手算测试和来源/legacy审查并行，由根集成；本轮与历史失败、修复及最终命令身份见[验证记录](../verification/wallet-lots-rows.md)。本纯片尚无数据库reader或维护/资金调用方，不作为C0生产完成标志。

接下来先冻结并实现同owned session的来源+lot原行读取、共享预算与EOF记录，保留旧六种reader的准确范围；再冻结pre+basis（109表原始record_send、计数/分页/超长/取消界）、一次init消费、同RR写后source+lot重扫和未知COMMIT只查询原epoch/proof的规则。明确封闭ScanKind/once容量及严格v2 codec和APP真实身份/运行组合，不能拼旧报告批准开账。完整资金接线后，按依赖继续C1/C2/C3、商品/订阅、扩展、生产/HA与最后性能，不省略真实供应商、目标容器/1Panel等外部验收。


## 84表来源与钱包读取及下一串行关口

已先冻结[ReadWallet合同](../../specs/022-self-service-payments/c0-source-wallet-reader-contract.md)，再并行实现固定25组投影/独立列审查、84表reader与边界测试、第七kind维护封装与真实Plan/Init验证。新入口在同raw执行七来源算法与实际lot重放，平铺八结果；原68表/七算法入口与旧六kind保持，uint8 once未扩位。根汇合后复核状态/coverage、更新README/HLD/LLD/统一架构并按[本片证据](../verification/source-wallet-reader.md)完成验证、提交与推送。

维护发行仍要求16表空，当前维护正例如实缺epoch mismatch；独立owner RR的非空C0验证只属reader。下一关口先冻结完整protected pre+basis（未来第八once位）、109表record_send分页/预算/取消，再实现同Init原RR的protected writer→post重扫→epoch及未知提交只查原身份。已经提交epoch的新维护授权、严格v2 codec与全部资金路径须分别完成；不删除空门禁或借lease SQL写入伪造phase。C1退款、C2拒付/债务、C3恢复、商品/订阅、媒体扩展、生产/HA及性能仍保留，整体持续目标未完成。


## 109表basis与strict v2叶子及下一关口

先冻结[basis读取合同](../../specs/022-self-service-payments/c0-basis-reader-contract.md)与[v2 codec合同](../../specs/022-self-service-payments/c0-proof-v2-codec-contract.md)，再并行实施：固定元数据与109表原二进制reader、stdlib-only v2 codec/独立golden，以及旧v1单向兼容差分和真实PG审查。离线send profile已由独立专库706字段逐项复核零差异；不从live目录生成运行时expected。根汇合复核、同步README/统一架构、执行专项和整仓验证后提交推送，精确结果见[本片记录](../verification/basis-proof-v2.md)。

两叶子不消费未来第八kind，也不接入资金/启动。下一步先冻结完整protected pre+basis：私有phase owner保持原busy，来自同session的来源原件与basis共同验收，不能拼旧Report/ScanProof；明确组合共享预算、一次init消费和取消清理。随后同Init原RR protected writer→严格v2与84表post→epoch→commit-ready，ready拒绝新lease，Commit受phase约束；未知COMMIT只查原身份，不补发或重新初始化。已提交epoch的observer/runtime另冻，旧空安装门禁不可删除。全部资金路径接线后，继续C1退款/C2拒付债务/C3恢复、商品/订阅、媒体扩展、生产/HA与最后性能；整体目标未完成。

## 最小seed与纯初始化计划

按[冻结合同](../../specs/022-self-service-payments/c0-init-planner-contract.md)先并行完成原68表Facts的最小九组非授权投影、独立纯init planner与外部手算golden。planner用原业务金额计算开账，与ledger净额及cache同时核对；生成准确旧成员集合、零opening、seq0/head及正额lot/event/cache/held allocation，不生成数据库stamp、epoch/proof或资金权限。预算和确定ID均先冻结。实现与执行证据见[本片记录](../verification/init-planner.md)。

下一依赖仍是完整protected phase：自己真实读原68+完整tenant birth=NULL+16空表及109 basis，持有自己的seed/plan；第八once位贯穿原busy，私有writer不接收外部Facts/Plan/Report。strict v2与实际84post、deferred flush、commit-ready和unknown原epoch观察必须一起闭合，然后另冻已提交epoch的observer/runtime及正式迁移，才推进全资金路径。旧安装空表门禁不可删除，普通startup不比较经营后当前basis与历史cutover basis。C1/C2/C3、商品、扩展、生产/HA与性能仍在其后，整体目标保持进行中。


## 受保护初始化事务与下一依赖

按[冻结合同](../../specs/022-self-service-payments/c0-protected-init-phase-contract.md)并行完成私有phase/rows/唯一提交、固定writer与资源计数、strict issuer/post proof；根串行实际PG集成、故障与架构收口。库级候选已装配pre68+birth+16空/109basis→自持draft→固定writer→actual84/strictv2/freshcatalog及post109→唯一COMMIT。结果与执行范围见[验证记录](../verification/protected-init-phase.md)。Unknown保留且不自动重做，当前没有生产观察器。

下一片先设计并实现独立停服只读的原epoch/原proof观察、非空epoch runtime认证及最小权限，保留旧空安装发行门禁。然后另审正式C0迁移（固定manifest/current roots不可静默跟随latest）、维护CLI与全部资金路径接线，才进入C1原路退款/C2拒付债务/C3恢复、商品/订阅、媒体扩展、目标容器/1Panel与生产/HA，最后完整能力下性能对照。整体目标仍进行中，不能以候选库级开账代替在线交付或生产验收。上述窄片的“下一依赖”按各自历史记录保留，以本节为当前路线。


## 独立停服 epoch 观察与下一依赖

2026-11-03先冻结[O1合同](../../specs/022-self-service-payments/c0-epoch-observer-contract.md)，再并行实现私有观察生命周期、不授权限的快照 facts reader、实际84表原件投影和strict-v2绑定。新的 Gate 重新取得双锁；当前图完整且cleanup完成后才返回普通观察，不修改原Unknown、不重试开账。经营后不重算当前basis强等历史cutover摘要。实现与真实执行范围以[本片验证](../verification/epoch-observer.md)为准。

下一依赖是R1：审全84表/列实际APP权限，明确 payment_query_conflicts 四列最小读取与cutover/current roots兼容，再实现 actual APP 非空epoch完整RuntimeProof、worker窄证明和projector catalog。R2另审正式迁移、维护CLI、普通/沙箱/off/Recovery启动及全部资金helper；之后继续C1/C2/C3、商品/订阅、媒体、生产部署/HA及性能，不缩减整体目标。


2026-11-03 C0-R1-A：[显式读取profile合同](../../specs/022-self-service-payments/c0-runtime-read-profile-contract.md)先于实现冻结，新增WalletReadV2及84表776列（history另2列）受审读取清单。原API/旧root/operations/provision源SHA保持，新增四列仅SELECT，完整新root由静态base+delta固定，专库验收状态见[记录](../verification/runtime-read-profile.md)。当前已实现非空RuntimeProof的权限前置，并通过限定专库验收；最终make check与明确专库make verify-mvp也已通过（286前端、46 Go包race/vet/build），准确记录见同页。不替代R1-B历史/current根兼容、APP自有v1/文件认证及完整同RR组合；R2正式迁移/CLI/全资金接线和全部后续阶段继续。

2026-11-03后续先冻结[R1-B完整APP运行合同](../../specs/022-self-service-payments/c0-runtime-composer-contract.md)：根串行组合当前完整catalog/history、六条静态历史映射与同RR原件/八域图；V1纯格式、原件投影和APP文件loader可独立并行。caller-owned失败清理、30秒发行与5秒窄检查先于实现明确，组合库已实现，限定真实APP/genuine sandbox结果与整仓/架构/推送分别见[记录](../verification/runtime-composer.md)；完整PB历史认证继续下述证据门禁。R2完整V2交付/正式迁移/维护CLI/全部在线资金及后续阶段保持原范围。

2026-11-03运行验收发现：[非空PB历史来源缺口](../../specs/022-self-service-payments/c0-runtime-pb-evidence-gap.md)是原算法有意保留的 `OperationSnapshotPrefixUnproven`，不因APP组合库落地而消失。空control配置不视为活动预算认证；需先冻结command前缀/window创建cut及必要append-only证据，保留旧历史缺证，再验完整经营图。此项仍是R1-B完整验收前置，不能据本轮限定PASS推进在线资金ready。

2026-11-03 B-P1先冻结[命令前缀与窗口出生合同](../../specs/023-periodic-products/b-prefix-contract.md)，并行实现独立纯包、候选SQL/最小APP权限与restricted actual APP专库语义矩阵，根负责并发快照/语句副作用与架构/交付汇合。候选不改旧迁移、旧核验器或正式运行入口，实际执行范围见[本片记录](../verification/periodic-budget-prefix.md)。README补导航、首次经营与配置，新增Issue/PR模板，代码与设计一起推送。

下一依赖仍按合同§1：精确001–022安装/材料隔离→新完整scanner/basis/catalog/report profile与同snapshot组合→正式append-only迁移/provision/startup。旧control不补造origin，原Base与legacy缺证保留，完整R1-B和R2线上门禁持续关闭；C1/C2/C3、商品、媒体扩展、供应商与容器/1Panel、生产/HA和最后性能阶段继续，不以本候选替代最后阶段交付。

本片根最终专项15顶层/46父子终态PG race与19顶层/227父子纯race全部通过；`make verify-mvp`指定专库1072.572秒、286前端/48Go包race/vet/build通过，默认独立opt-in仍分别记账。CI仅other分区命令级显式运行PB候选，保留精确22与随机schema/actual APP隔离；最终静态/架构与推送后CI按[验证记录](../verification/periodic-budget-prefix.md)核对，不将本地成功当Linux、正式迁移或生产能力。

2026-11-03 B-P2 按[精确合同](../../specs/023-periodic-products/b-prefix-install-contract.md)新增固定 original22 材料及明确 schema 的同事务 verifier，纯候选材料与局部角色核验并行实施，根接入仅测试支持的 savepoint 安装及显式历史 fixture。候选不跟随 latest；失败不留下可提交半片，旧 provision 后跑扩权要被检出。实现与实际结果见[架构](../architecture/periodic-budget-prefix-install.md)和[验证](../verification/periodic-budget-prefix-install.md)。B-P3承接全来源共享扫描/basis/catalog/report profile与protected pre/post/observer/runtime；正式迁移/启动及完整经营验收仍为后续依赖。

## B-P3 完整版本组合与当前串行关口

2026-11-03按[冻结合同](../../specs/023-periodic-products/b-prefix-profile-contract.md)并行完成source/wallet scanner、basis和静态catalog材料，再由根汇合V3维护/observer/runtime及固定兼容映射。新固定读集为71表/635列、87表/804列、112表/1004列，全catalog为128张普通表；显式V3入口不替换旧68/84表、109表与LegacyV1/WalletReadV2，也不删除旧Base或legacy历史缺证。受保护phase保持同原RR的pre→basis→writer→post→basis复读与fresh授权→唯一提交；observer只读终结，actual APP组合仍绑定原受限LOGIN/事务，sandbox保留原本地manifest/nonce和v1。

实现已落地，scanner、basis和ordinary全阶段已有专项PG证据；catalog/sandbox矩阵、最终回归和精确远端CI以[组合验证](../verification/periodic-budget-prefix-profile.md)为准。B-P3整体未完成：[采购历史证据缺口](../../specs/023-periodic-products/b-prefix-procurement-barrier-gap.md)仍要求Plan拒绝，下一独立片先设计不可变审批消费的revision、完整evidence与唯一绝对成本事件绑定，再实现完整成功路径；[catalog CPU专项](../verification/periodic-prefix-catalog-cpu.md)的2026-11-04正式catalog双轮/负例race矩阵已在原5秒界通过，旧失败仍保留，局部性能结果不抵消该缺口。

当前候选仍是exact original22之后的隔离安装，无正式追加迁移/provision/生产维护CLI/startup或在线资金接线。完成上述合同门禁及本片验收后，再另冻R2正式交付和全资金路径；之后继续C1/C2/C3、商品/订阅、媒体扩展、实际供应商与支付、容器/1Panel、生产/HA，最后在完整商业检查下性能对照。历史片中的“下一依赖”保留其当期记录，本节作为当前路线入口。


## B-P4 设计前置与当前下一步

[采购历史设计稿 v0.1](../../specs/023-periodic-products/b-procurement-prefix-design.md)已完成D0汇合，权限/事务方向和身份SQL叶子已先行冻结；后者真实PG/race通过。当前新增[共同来源](../../specs/023-periodic-products/b-procurement-source-contract.md)、[账户](../../specs/023-periodic-products/b-procurement-account-contract.md)、[成本/request](../../specs/023-periodic-products/b-procurement-cost-request-contract.md)和[PB最小闭包](../../specs/023-periodic-products/b-procurement-pb-acl-contract.md)合同，统一两scope root、canonical child/companion、单attempt链、来源先出生后效果闭合及audit/outbox来源槽。Reconcile包含完整三action和分层资金时点，保留pending实际更新及原API未消费字段；原工作提案保留历史状态。

[行锁ACL实验](../verification/procurement-account-lock-acl.md)以实际APP LOGIN确认created_at单列UPDATE可取得四种行锁且实际UPDATE受guard拒绝；它与namespace实验均只证权限/锁原语，未验完整writer/014/PB/角色组合。没有生产模块、业务数据库、普通权限或部署结构变化，统一架构已增加明确标为候选的数据关系图。

本轮进一步汇合[32表物理字段](../../specs/023-periodic-products/b-procurement-schema-contract.md)、[入口锁/错误/恢复](../../specs/023-periodic-products/b-procurement-execution-contract.md)、[全部角色列ACL](../../specs/023-periodic-products/b-procurement-full-acl-contract.md)及[纯核/读取/保护阶段](../../specs/023-periodic-products/b-procurement-replay-read-contract.md)合同。完整pp_record_attempt接管DB选价与INSERT，私有三参helper最后seal；BYTEA保持被忽略预期报价的原接受集，未消费字段不转TEXT。32新表物料从711校准为715列（647可INSERT、68 generated），补四个条件关系列；已有只读CLI和Make/CI检查。新增[固定关系SQL](../../internal/procurementprefixschema/materials.go)与[原件合同](../../specs/023-periodic-products/b-procurement-original-ddl-contract.md)，覆盖32新表columns、原18表110新增列和source/account/cost/original逐边约束，按全部keys→checks→FK装配，不包含最终profile、函数/guard/ACL或默认业务接线。

本轮在上述关系材料之上拆出三条互不覆盖的工作线：[纯核DTO合同](../../specs/023-periodic-products/b-procurement-core-dto-contract.md)对应[procurementprefix基础](../../internal/procurementprefix/types.go)，40类原件482列与32类前缀715列、72数组共享100000行/16MiB预检及旧23数组173字段Base无损投影已实现，定向race通过，但未导出Check；[账户writer合同](../../specs/023-periodic-products/b-procurement-account-writer-contract.md)对应[20函数/29触发器隔离候选](../../internal/procurementaccountwriter/materials.go)，actual APP LOGIN限定专项已通过，完整故障矩阵仍待完成；[宽TEXT精确链接合同](../../specs/023-periodic-products/b-procurement-exact-link-contract.md)与[268 PK/UK、291 FK清单](../../specs/023-periodic-products/b-procurement-exact-link-inventory-v1.json)先交RC双端锁/比较原语实验，6组PG race场景通过，现有685项关系未改。执行合同当前为81个PP错误，PP906明确表示旧未受证或尚未实现的数据域。三线的实际范围与最终验收见[本片记录](../verification/procurement-account-prefix.md)。

下一串行关口仍是D1：剩余受控根/全域validator/guard全文、触发顺序与完整ACL、宽TEXT全域接受集方案、四域独立重放/十一命题逐对象枚举与最终固定profile；已有DTO和账户候选不替代完整实现。I1完整写证与独立oracle、I2全量reader/basis/catalog和保护链按所有权推进，维护/runtime由根集成，PG严格串行。A0必须实际新历史完整成功、旧历史继续缺证和actual APP攻击/旧接口回归，不能用窄例或缺证拒绝代替；D1、A0和B-P3整体仍未完成。原十一项Unproven和R2正式迁移/CLI/启动/在线资金接线不解除；C1/C2/C3、商品/扩展、真实供应商/支付、容器/1Panel、生产/HA及最终性能范围保持。

GitHub首页与贡献指南已复核特点、本地/API使用、Docker/1Panel和许可/验证边界。精确2223e40的[远端CI](../verification/procurement-identity-ci.md)10/10成功：Go56包中52PASS/4无测试，前端286PASS，新identity为2顶层/8含子项PASS且无SKIP；原13套PG和1诊断未验收。此前[本机整仓失败](../verification/procurement-identity-leaf.md#本轮整仓集成与未通过项)及三个原用例的定向PASS分账保留，远端成功不确定本机超时根因。本轮合同与诊断的最终检查、架构快照及提交推送独立收口。

本次关系材料的独立catalog与命名声明边验收、GitHub上一提交精确CI、最终静态检查分别记录于[关系材料验证](../verification/procurement-relation-materials.md)。README及SECURITY入口同步；不把候选SQL关系安装当成完整写证或商业验收。


## 2026-11-04 续片交付与下一根

covered PB命令的精确动态前缀与账户必要W DML故障矩阵已实施；最终纯核220顶层/1123子项、账户真实PG10顶层/478子项race全PASS，零SKIP/FAIL。独立catalog双轮安装/负例/旧profile拒绝候选3顶层/37子项race在原5秒界通过，关闭该专项复验缺口，历史失败保留。证据与实际边界见[本片记录](../verification/procurement-pb-snapshot-dml.md)和[catalog记录](../verification/periodic-prefix-catalog-cpu.md)。README特点、API/源码/Compose/1Panel与账号说明已独立复核并补仓库导览。

下一真实受控路径按[attempt出生根A0–A6](../../specs/023-periodic-products/b-procurement-attempt-birth-writer-contract.md#8-实施切片和实际尚缺正文)推进：先原规则叶子、真实request自动birth与保护，再完整attempt/managed时序、reserved效果和command+reserve新guard/validator、最小ACL、显式adapter/actual LOGIN及集成。末序p hook取得原020真实admitted_at后一次完整生source，旧account v1与715/482物料不改签。该根仍仅设计，不能绕旧guard或给APP原件DML旁路。

legacy cut/absence/完整首配、B-HO1、全部业务资格/逐对象Q与Base/aggregate、其余根/profile/宽TEXT/R2和所有后续经营/扩展/生产阶段继续原范围；B-P3整体未完成。Docker daemon当前不可用，容器/1Panel实际安装仍保留，其他可执行工作继续。

## B-P4 attempt A0 与独立 A1 续片

本轮[A0原规则](../../specs/023-periodic-products/b-procurement-attempt-rules-contract.md)已实现五函数固定材料，并由真实原Go规则与actual APP专库完成2顶层/224子项race差分，零SKIP/FAIL；CI billing分区显式开启。该切片是完整attempt根的一个前置，不等于上文全域验收A0或完整writer完成。[A1真实父请求](../../specs/023-periodic-products/b-procurement-request-birth-writer-contract.md)与[窄定位集成](../../specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md)已经冻结独立四表切片，不以A2账户闭包阻塞A1；旧685关系、账户v1和正式001–022不改签。

独立A1 writer/guard/只读validator/冻结ACL已实现。本轮新增[budget](../../specs/023-periodic-products/b-procurement-request-birth-budget-contract.md)、[grant-fault](../../specs/023-periodic-products/b-procurement-request-birth-grant-fault-contract.md)、[raw](../../specs/023-periodic-products/b-procurement-request-birth-raw-contract.md)、[wait](../../specs/023-periodic-products/b-procurement-request-birth-wait-contract.md)四份先行合同与四份测试；actual APP PG race整包最终23顶层/423子项、零SKIP/FAIL、36.078秒，包含旧5顶层/22子项，不重复相加。新增预算九配置、zero/input-only/分组价、pending/Release/recovery与三故障；钱包真实等待跨价、request/Key期限及55P03/57014；raw6顶层/92子项含26冻结字段与22错边；逐项grant291必要fault、两个未用allocation读权正控制及PUBLIC重叠正控制。首轮失败日志保留；原020的AND短路说明allocation两读在当前pointer路径不使用，SQL/摘要不改，不把冻结最小ACL宣称为每条grant必需。准确范围见[A1验证](../verification/procurement-request-birth.md)。

2026-11-04续片补齐父行八项资格重读/两种提前约束、原PB控制行跨价/过期、真实server成功后丢COMMIT回执和独立catalog/全部grantee。最终actual APP原Reserve整包32顶层/528子项race全PASS、零SKIP/FAIL、62.292s，含旧23/423；五顶层静态/纯wire，其余27真实PG。catalog五关系/七函数/20trigger及全ACL认证、78项同事务漂移均实跑；只在测试显式调用，不据此发行Qualified/profile。原SQL/摘要/fixture/001–022不改，首轮提前约束与OID表示失败分别保留。README经独立复核源码/Compose/1Panel/首次管理员流程，补Gemini认证限制与race工具链依赖；已核GitHub默认分支为codex/initial-mvp，clone会取得交付分支。

独立 [A2材料](../../internal/procurementattemptbirth/materials.go)已实现38函数/68trigger、12表262列/七原表38扩列/137约束及精确ACL。首轮12顶层/25子项后补齐原规则/Reserve兼容、逐grant失权与catalog漂移，最终actual APP专库race为47顶层/1648子项PASS、零SKIP/FAIL、535.086s（41顶层真实PG，六静态）；详细[验证及保留失败](../verification/procurement-attempt-birth.md)与[机器记录](../verification/procurement-attempt-birth-hardening.json)分别给出范围和原件。1339项grant实验包含1334必要失权与五项真实撤权后提交成功的控制；110项漂移属于13类、15个隔离fixture，不把保守授权清单解释为每项都必需。账户/供应商PB配置、15处DML故障、共享硬限额与等待跨scheduled报价均实跑；每次安装在COMMIT前核固定catalog，另核提交后。birth→p复用原双列UK保持NULL激活，未新增宽索引。A4剩余组合/同步/期限/未知提交、A5 executor、全域profile与R2正式接线仍开放；普通billing.New与001–022不变。模块、时序和角色责任见[细图](../architecture/procurement-attempt-birth.md)。

## 当前下一切片：A4 收口与 A5 显式 executor

2026-11-04先设计再实现[同步](../../specs/023-periodic-products/b-procurement-attempt-synchronization-contract.md)与[期限](../../specs/023-periodic-products/b-procurement-attempt-fault-deadline-contract.md)专项：原BindCredential/Commander双向等待、真实双租户共享采购预算、锁后paused、原生超时/取消、Key/request过期和单SQL累计5秒；最终结果单独见[A4记录](../verification/procurement-attempt-synchronization.md)，不把前轮47/1648与新专项重复相加。真实24小时财务期限、active周期cap及跨UTC窗口继续保留。

当前完整A2＋A4四分片准确并集56顶层/1652子项全部PASS、0 FAIL/SKIP；前三片保留PASS，第四片跨低电量休眠的失败保留并在唤醒后按原名单/源码/期限整片复验通过。所有临时schema/role/backend归零。S1根复核20顶层/56子项纯race全PASS；没有数据库、事务或普通服务接线。前一精确提交9688929的[14组远端CI](../verification/ci-package-isolation.md#精确提交-9688929-的最终远端审计)也已全部通过，220项测试SKIP明确列出；该远端结果不冒充当前新代码CI。

[A5合同](../../specs/023-periodic-products/b-procurement-prospective-executor-contract.md)已冻结；首片[S1无连接协议](../verification/procurement-execution-protocol.md)交付固定三根ABI、参数快照、恰一行/NULL union与固定PP cause分类，不接默认服务。随后依序完成S2显式事务/确认回滚重试/unknown只读观察、S3原Go API显式构造器及HTTP错误优先级、S4实际APP差分与故障。A4可与无连接叶子并行补验，完整A5验收仍依赖A4；PB错误域的精确兼容另先设计，不能通过开放PG文本猜测完成。

之后继续其余D1/I1根与独立全域重放/profile、B-P3历史缺证、R2正式迁移/启动/全资金接线，再到C1退款/C2拒付/C3恢复、商品订阅、扩展、生产/HA和最终性能。阶段验证及提交不等于整个持续目标完成，正式供应商/支付/目标1Panel等外部验收边界保持。

本轮S2事务运行器和S3显式适配已实现，按先行[事务](../../specs/023-periodic-products/b-procurement-executor-transaction-contract.md)/[适配](../../specs/023-periodic-products/b-procurement-executor-adapter-contract.md)/[验收](../../specs/023-periodic-products/b-procurement-executor-transaction-acceptance-contract.md)合同核实际APP三根与提交未知，准确计数/失败历史见[记录](../verification/procurement-execution-runner.md)。正常New/main/provision与001–022不变；没有完整A5/R2上线。[只读观察合同](../../specs/023-periodic-products/b-procurement-executor-observation-contract.md)对应的生产Observe已实现：新RR RO精准回执、三根真实提交ACK丢失与未发送COMMIT正反控、账户分支及旧回执、managed/unmanaged和已有PB三形态、必要失权/恢复均有本片限定证据。最终纯race65顶层/1427子项、实际APP6顶层/21子项均PASS、零FAIL/SKIP；准确范围、两轮结果、codec修复与失败历史见[观察器记录](../verification/procurement-execution-observer.md)。真实legacy当前在捕获handle前安全拒绝，完整legacy Observe、坏关系/额外反向边PG和并发snapshot交错仍未实证；这些历史专项按[ADR-005](../decisions/ADR-005-pre-mvp-single-path.md)暂缓扩展，后续执行顶部MVP能力排期，不将本片扩大为完整S4/A5/R2验收。

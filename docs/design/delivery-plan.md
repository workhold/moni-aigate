# 业务能力与并行交付计划

2026-11-02。目标是对外API商业运营：六协议统一接入、渠道经营、客户权限/钱包/账单，以及可解释的上游采购成本和调配。业务完整性优先，性能专项后置。当前系统视图见[统一架构](../architecture/README.md)，逐项产品差距见[New API路线图](new-api-roadmap.md)。本文件给出依赖与工作边界，不把规划当成交付。

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
| 022 自助支付 | A共同入账/双审券已完成限定本地验收；B0-P及M/Q独立沙箱已编码，专项真实PG/race通过，真实进程短程通过、旧版本20代长程已完成，最新补丁/精确故障继续收口；B1核心设计/SQL已冻结，B2/Go第三来源已实现并通过专项，B3订单/worker/HTTP/UI/main已接线，真实PG专项及两轮真实主进程/最终浏览器整链通过限定本地验收，真实在途SIGTERM与Guard失权补验已通过；C0纯分配/history/同RR账本reader及SQL候选专项已通过，完整静态checker三API与ordinary/sandbox两套正式root已实现，五ordinary变体和完整sandbox的最终leaf真实PG race已通过并保留原五秒预算，整仓make check及verify-mvp已通过；维护Permit/epoch、全源proof与全资金路径仍后续，C1退款、C2拒付/债务及C3交叉/恢复已有候选，C尚未接入资金事务 | [B0运行证据](../verification/payment-sandbox.md)→应用支付资金→来源lot/分配后才开放原路退款；正式支付商另门禁 |
| 023 周期与商品 | A完成限定本地验收；B核心/HTTP/UI、浏览器、020最终相关PG race及预览升级通过；独立三态Verifier/CLI专项通过；C商品价值条带合同已补候选，未实现 | [B核验合同](../../specs/023-periodic-products/b-verifier-contract.md)、[验证](../verification/periodic-budgets.md)；先完成周期硬预算与资金来源，再订阅/套餐 |
| 024 扩展平台 | [独立草案](../../specs/024-extension-platform/spec.md)与[A0精确告警合同](../../specs/024-extension-platform/a-contract.md)已冻结019，生产代码及专项/最终全量race、浏览器和019预览通过，架构检查通过 | 真实PG/HTTP、双槽worker与UI逐项验证；真实main故障/浏览器已通过，见[记录](../verification/operations-alerts.md)。B0-P纯计量层已冻结实现并通过race/vet/fuzz，尚未接入provider/billing；媒体/异步任务/长连接、外部身份按认证与计量能力逐片交付 |
| 025 HA与生产 | [独立草案](../../specs/025-production-ha/spec.md)已完成；仅A0配置透传及实际CLI渲染、A1-M邮件真实进程窄片已完成限定本地验证，完整HA未冻结未实现 | [共享执行候选](../../specs/025-production-ha/b0-shared-execution-contract.md)已补成员epoch、完整资源边、保守unknown及精确整数桶；生产SQL/角色/接线未冻结。容器/1Panel和灾备；021指定claim/SMTP接受至ACK窗口真实SIGKILL及原60秒lease重启已通过限定本地验收；不得直接移除单活保护 |
| 最后阶段 性能 | 002/008后置 | 完整商业检查保留，重新建立基线并优化；同机同协议同用量条件才能与Bifrost/LiteLLM比较 |

本次持续目标覆盖后续阶段；阶段验收不是整个目标完成。缺正式凭据或运行环境时如实保留该门禁，继续其余可执行工作。

022-B3当前接线与分层执行范围见[应用支付记录](../verification/payment-application.md)。真实main验收在专用测试库串行占用InstanceLock；独立前端/文档/C0设计可以并行。最终汇合后才更新架构清单并跑完整检查，之后继续C0→C1/C2→C3、023-C、024余下切片、025和最后性能阶段。

后续022的资金候选集中于[C0生产验证](../verification/funding-lot-candidate.md)、[独立重放](../../specs/022-self-service-payments/c0-verifier-contract.md)、[C1退款](../../specs/022-self-service-payments/c1-refund-contract.md)、[C2拒付/债务](../../specs/022-self-service-payments/c2-dispute-contract.md)和[C3交叉/恢复](../../specs/022-self-service-payments/c3-cross-recovery-contract.md)。025邮件真实进程按[A1-M合同](../../specs/025-production-ha/a1-mail-process-contract.md)单独验收，不能从SMTP本地持久写入推断已命中生产ACK之前的窗口。

用户已确认正式模型渠道范围为OpenAI/Codex、Claude、DeepSeek、Gemini、GLM及硅基流动等第三方平台；逐渠道协议/计量/采购/验收矩阵见[渠道认证计划](provider-qualification-plan.md)。它们分别登记profile与报价，不因同名模型或兼容接口继承官方计费认证。支付商和1Panel目标主机仍未确定，本地实现继续。

可并行的C1-P纯退款协议已完成，见[合同](../../specs/022-self-service-payments/c1-protocol-v2-contract.md)与[专项证据](../verification/payment-refund-protocol-v2.md)；独立Python两条v1桥、四摘要域及九个签名向量与Go交叉通过，race/vet/短fuzz通过。它未被普通main调用；C1-M/S/I、退款留置/资金、永久拒绝墓碑、C2/C3及生产验收仍在后续。当前C0静态checker已实现，最终集成验证与后续[Permit](../../specs/022-self-service-payments/c0-permit-contract.md)、[运行helper](../../specs/022-self-service-payments/c0-runtime-helper-contract.md)仍分别交付，核心串行关口继续保留。

C0的[离线元数据导出窄片](../../specs/022-self-service-payments/c0-schema-check-contract.md#9-根冻结窄片metadata-only-离线-descriptor-生成器)已实现并通过[专用PG验证](../verification/funding-lot-descriptor.md)：ordinary/sandbox分别before/after，两轮十文件同bytes，失败DDL后精确清理通过。该工具的原导出物保留未审候选身份；新checker的两套正式root经独立实装、来源交叉核对与根审后固定嵌入。精确类型权限已校准：32个自动row/array的默认USAGE不授表读取，显式proof类型权限单独核。

完整静态叶子模块`internal/fundinglotcatalog`已实现`VerifySnapshotTx`、`VerifyInstallationTx`、`VerifyRuntimeTx`及同事务opaque证据：ordinary为实际owner/APP/projector三角色，固定五种named/public与owner属性变体；sandbox覆盖application/Mock/Receiver三schema、八角色与十组跨域负权，另核runtime全局无owner及目标数据库无CREATE/授权提升路径。两套SHA固定root、封闭DTO、原函数正文、结构/ACL/依赖与资源界已落代码，见[collector记录](../verification/funding-lot-catalog-collector.md)及[peer记录](../verification/funding-lot-catalog-peers.md)。最终leaf串行真实PG race已通过（217.140秒），三个发行API保留原五秒总预算；47个顶层测试、179个终态中176 PASS，3个独立capture/timing/export为显式opt-in跳过且另有专项证据。精确清理确认本轮schema/role/DB/backend归零，public与DB ACL保持原样，见[最终leaf验证](../verification/funding-lot-catalog.md)及[机器记录](../verification/funding-lot-catalog.json)。本轮整仓`make check`/`make verify-mvp`已由集成人执行通过。

当前正式root仅覆盖**PG16.15/Homebrew initdb catalog-profile、UTF8与C/C环境**，其中PL/pgSQL language OID 14038为该initdb分配值；任意PG16、其他locale/发行版及1Panel部署仍需独立profile验收，未知profile失败关闭，详见[正式root边界](../../specs/022-self-service-payments/c0-schema-check-contract.md#104-首次正式root冻结与已验环境)。该leaf尚未接普通main，也未发行维护Permit、资金epoch或独立全源RuntimeProof；维护初始化、来源/lot全图核验和全资金路径接线继续串行推进，022-C及全部后续阶段仍未完成。

维护T0按[根冻结§10](../../specs/022-self-service-payments/c0-permit-contract.md#10-根冻结t0专用连接双session-gate与清理)新增`internal/maintenancetrust`：专用物理连接、非权威身份预检、402→401双session锁与独立有界逆序清理；失败立即关捕获socket，副本共态及晚到dial封闭。生产代码已独立读审，专项范围见[验证](../verification/maintenance-gate.md)与[细化架构](../architecture/maintenance-gate.md)。本片没有Snapshot/Permit/main或资金权；T1–T4须先冻结首次RR/账户锁顺序、全程scanner lease/deadline、可信manifest及完整v1前驱字段矩阵，之后再继续维护与全源组合。

本轮GitHub入口交付汇合T0最终专项（54.205秒、53 PASS）、README/部署入口和19图；根最终make check 6.767秒、make verify-mvp 339.394秒通过，286项前端、全Go默认race/vet/build及明确测试库已执行。期间B0测试将合法deferred lease误判为HTTP槽数，按根冻结§10.3仅修测试并补独立多连接回归，原失败与最终验证分别保留，见[交付记录](../verification/github-initial-delivery.md)。无普通main、迁移、权限、资金或生产验收范围扩张。

C0-V0-L的[纯账本行检查](../verification/funding-ledger-rows.md)已实现并通过race/vet及短fuzz；只核输入行，不读数据库，不发完整coverage或proof。后续[caller-owned RR ledger reader](../../specs/022-self-service-payments/c0-ledger-reader-contract.md)已实现并通过[真实PG/race专项](../verification/funding-ledger-reader.md)，按三表全部行/128keyset/EOF及原journal业务列读取；独立来源/Key/采购/PB/B和lot序列仍须同snapshot完成，不能用旧VerifyLedger中的写侧SQL谓词作为新oracle。

2026-11-02后续维护片：根已冻结[T1 Snapshot 生命周期](../../specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md#10-根冻结范围与尚未开放的能力)，已实现私有单次RR、ONLY125/首次快照、固定catalog stage、全程busy/绝对期限及提交结果分类；最终[真实PG/race](../verification/maintenance-snapshot.md)115.412秒、124终态全PASS，最终整仓make check与指定专库make verify-mvp已通过，见[集成记录](../verification/maintenance-snapshot-integration.md)；新四分区远端CI另验。T2完整history/维护身份/[前驱矩阵](../../specs/022-self-service-payments/c0-predecessor-contract.md)、T3独立来源、T4 basis/epoch继续后续，无资金或生产能力开放。

GitHub第二轮41339443048已失败并保留完整JSONL：身份通过，两个大包仍因累计600秒超时，支付模拟器被时区相关静态文本校验拒绝。只调整CI为十个完整动态分区与显式数据库时区，原始M/Q Open隔离复现已通过；新远端结果单独核验，见[第二轮证据](../verification/github-ci-second-run.md)。未新增产品/测试源码、迁移、金融或生产权限。

# 统一架构与内容同步检查

2026-11-01。本记录覆盖统一系统架构及其持续更新机制；018 成本路由的运行行为与验收边界另见[018 验证记录](cost-aware-routing.md)。

统一入口为 [系统架构](../architecture/README.md)：系统上下文、控制/数据平面、模块源码地图、六协议范围、请求与资金生命周期、成本隔离、核心数据关系、身份与三条 Key CAS、单活部署/worker/恢复、已实现与规划边界，以及演化流程。018 已补充最高优先级内采购比价、未知/两维上限、候选报价到 attempt/sending 的锁内同步、成本发布的渠道 CAS/UPDATE 屏障和前端编辑器源码映射，当前标为已实现、限定本地验收通过；019 明确链接[规划规格](../../specs/019-procurement-accounts/spec.md)与[设计讨论稿](../design/procurement-accounts.md)，保持未冻结、未实现。文档包含 Mermaid 上下文、模块、时序、ER、部署五图。HLD/LLD 和 SDD 继续承载详细合同。

`scripts/check-architecture` 为 Go 标准库独立命令。默认只读比较 SHA-256 清单，包含源码/测试、构建依赖/部署/CI、SDD 与设计/验证合同，以及架构正文和演化记录摘要；新文件、删除文件和保留 mtime 的内容变化都会使检查失败。显式 `--refresh --reviewed --reason` 才能追加演化记录并刷新快照，不依赖初始 Git 提交。默认 CI 不刷新。人工/agent 的语义复核要求写入根 [AGENTS.md](../../AGENTS.md)，由 `make check`、`make verify-mvp` 和 GitHub Actions 接入只读检查。

已执行：

```sh
go test -race ./scripts/check-architecture -count=1
go vet ./scripts/check-architecture
go run ./scripts/check-architecture
```

前两项通过。临时目录夹具覆盖初建与无副作用 check、无操作 refresh、文件增加/修改/删除、保持 mtime 的内容变更、源码/设计/证据覆盖、新目录代码、架构后续支持文档与 PNG 图片的增改删、产物/私密 env 排除、显式复核/理由、正文与历史变更、损坏清单、排序/摘要一致性、缺失章节/图、本地断链、路径越界及符号链接越界。

五图使用 npm 官方 registry 的 Mermaid 12.2.0 在 `.local` 临时依赖中核验，未修改产品依赖：5/5 解析成功、5/5 实际 SVG 渲染成功，并查看整页截图；标签换行正确显示。最终时序图补齐 catalog/PostgreSQL 参与者，明确 AuthorizedModel/profile → CandidateSnapshot/CostQuote → NewPlan 冻结 → Key runtime/准入队列 → T0 Reserve → plan.Start 实际选择渠道/凭据 → RecordAttempt/PrepareDispatch → Guard/Provider.Do 的顺序；修订后再次完成全部五图解析与渲染核验，保留[五图渲染截图](assets/architecture-018.png)。使用独立 headless Chrome 与专属临时配置，未操作既有浏览器、QA 或预览实例。模块连线、ER 实体/外键与可选关系、实例 advisory lock、10 秒 worker、5 秒恢复/投影限时、100 个恢复任务、50×200 投影批次及 310 秒排空已对照源码复核。文档特别区分迁移串行锁与服务维护锁，并说明恢复模式保留恢复授权接口。

本轮集成已实际通过真实 PostgreSQL 全 Go race（count=1、timeout=240s）、122 项前端测试、strict 构建、vet/build。隔离真实 main 两次 SSE 完整结束，客户收费 20+20 微元，采购成本分别为 8/22 微元，客户 held=0、账本平衡；旧 CAS 2 被当前 3 拒绝后进入只读核查，再显式新编辑成功到 4。390×844 无水平溢出，采购 cap 与底部按钮可达。随机 QA schema、role、listener 和秘密清理完成；opt-in 真实 main 生命周期与失败清理专项通过（10.739s）。58123 已由 owner 应用 013 迁移/provision，再以受限角色运行，ready 且非恢复模式，首页与 29 项 assets 一致；评审账号未修改。[浏览器证据](cost-routing-018-browser.json)、[隔离生命周期与清理](cost-routing-018-harness.json)、[预览核对](cost-routing-018-preview.json)及[完整 018 记录](cost-aware-routing.md)限定上述结论，不替代供应商、生产或性能认证。

本轮集成人已完成架构正文语义、能力状态、五图与源码映射复核，并实际执行显式 `make architecture-refresh REASON=...` 建立442文件基线。随后 `make check` 通过：只读架构/链接/内容检查、全仓库 vet、前端 strict TS/Vite 构建均成功。初建前缺少清单的预期失败已由本次基线及最终检查收口。本记录与018状态更新后再次显式刷新并执行只读 `make architecture-check`，变更理由与具体路径保留在[演化记录](../architecture/changes.md)。工具验证内容同步与链接，不能证明自然语言语义、后续图形渲染或业务正确性，后续变更仍须显式复核。

最终重建检查发现首轮包未包含构建交叉期间的两项前端校准，已单独补验并保留两个版本的摘要：历史不完整报价只读核查、新写两维必填、完整路由发布；直接HTTP SSE完成后收费20/采购22、冻结0且账平。当前源码122项前端再次通过，运行器真实main/失败清理再次通过11.094s，随机资源与秘密再次清理。最终包与58123首页/29资源逐字节匹配且ready非恢复；[018完整记录](cost-aware-routing.md)保留原因、范围与新旧证据。架构语义无结构变化，增加这批证据与对应能力说明后再次显式复核刷新；不把首轮浏览器覆盖范围改写成最终产物的全量重复验收。

## 2026-11-01：B3 生命周期、纯算法与后续设计同步

本节追加后续切片的复核范围，保留上文018历史结论；019至024的既有集成记录仍按[架构演化记录](../architecture/changes.md)及各专项证据读取。本轮按来源事实、进程、浏览器、纯算法、静态部署配置和未接线设计分别记账，不把其中一层的通过推导成另一层已验收。统一架构正文、HLD/LLD、演化记录和机器清单由根集成人在并行工作汇合后统一复核与刷新；本节的追加本身不表示已经执行最终清单刷新。

| 切片 | 本轮有证据支持的状态 | 证据与保留边界 |
| --- | --- | --- |
| 022-B3 APP 支付与真实生命周期 | 已接入普通 main、HTTP、支付页面与独立受限 worker；限定本地模拟闭环、在途 SIGTERM、真实 InstanceLock 失权取消和原命令恢复通过 | [应用验证](payment-application.md)、[原进程证据](payment-application-022-process.json)、[浏览器证据](payment-application-022-browser.json)、[原生 IndexedDB 证据](payment-application-022-indexeddb.json)、[新增生命周期证据](payment-application-022-lifecycle.json)。没有真实 PSP 收款、原路退款或生产认证。 |
| 022-C0 package-private 纯分配算法 | 仅 FIFO 预占、原边正序收费与逆 charged 边消费退款的纯算法及其专项已实现 | [纯算法验证](funding-lot-planners.md)。不读写数据库，不产生资金来源或发送许可；生产来源证明、钱包全序、维护 epoch、实际角色和业务事务接线仍为候选。 |
| 024-B0-P 纯计量向量 | 最多八维、双侧完整绑定、严格规范编码与摘要、精确有理费用末尾一次 ceil、旧 token 公式 adapter 的纯层已实现 | [纯计量验证](metering-vectors.md)与[精确合同](../../specs/024-extension-platform/b0-pure-contract.md)。未接入 provider/billing/routing/catalog/数据库，未注册媒体端点，不证明媒体上界或供应商扣费。 |
| 025-A0 Compose 身份配置 | 已补 app 的注册、重置与 SMTP 配置透传；静态检查及隔离Compose 5.6.1真实config渲染/环境隔离通过 | 初次[静态证据](compose-config-025.json)保留当时NOT RUN；后续[实际CLI渲染](compose-render-025.json)与[环境记录](compose-runtime-025.json)单独记录。镜像构建、容器运行、1Panel和真实SMTP仍未验收；当前单活锁保持，范围见[合同](../../specs/025-production-ha/a0-compose-config-contract.md)。 |
| 022-C0 生产来源 lot 与全钱包序列 | SQL、最小权限、维护切换与 proof v2 已有待审设计，尚未分配生产迁移、未进入资金接线 | [lot 合同](../../specs/022-self-service-payments/c0-lot-contract.md)、[全序合同](../../specs/022-self-service-payments/c0-sequence-contract.md)、[生产 DDL 候选](../../specs/022-self-service-payments/c0-production-schema.sql)、[ACL 候选](../../specs/022-self-service-payments/c0-production-permissions.sql)、[维护与 proof v2](../../specs/022-self-service-payments/c0-maintenance-contract.md)。设计审阅和随机 schema 的局部机制检查不替代全业务路径验收。 |
| 025-B0 共享执行与容量 | 已补共享状态、成员 epoch、准入/执行/退出状态机、整数桶与保守 unknown 容量规则的审查候选 | [共享执行合同](../../specs/025-production-ha/b0-shared-execution-contract.md)。未冻结生产 DDL/权限/接线，未开放多活；租约过期不证明旧网络退出，sending/running 不据 TTL 自动释放。 |

B3 第三轮在隔离专用库中使用当前源码分别重建 ordinary main 与 mock。真实 worker 调用已到达 P 的证据来自实际 mock LOGIN 的等待状态与原 command/merchant gate；维护屏障仅用于本轮物理在途观察，不进入普通服务。SIGTERM 和精确终止本环境唯一 APP InstanceLock 后，P 取消耗时分别为2ms和4ms，包含查询年龄及观察后全部检查的总 age 分别为16ms和51ms，均在原5秒超时之前。APP 退出并 join、APP/worker/projector 三实际 LOGIN 连接归零、P 原 command/order/transition/capture 均无效果后才解除维护 gate。随后保持同 manifest 和原命令，等待原60秒 lease 自然过期，经生产 expiry、签名 query not_seen 和同 canonical/digest 的原命令重试恢复，各形成唯一 ready、无新 capture；原四笔资金和非空 A 指纹保持不变。第三轮22项 PASS、脚本 exit 0，三个 schema、八角色与私密根完整清理，未触58123。

旧 process/browser JSON保留各自原轮的二进制摘要与未验范围；新增生命周期使用独立 JSON，不将旧轮未执行改成通过。真实浏览器的 GET404 与两次 reload 保留原操作编号，以及独立原生 IndexedDB 并发/原子性证据，各按其实际层次读取；它们不证明浏览器经历真实数据库提交回包丢失。P 的 HTTP 提交后回包丢失已验；数据库 COMMIT 成功仅丢回复仍明确 SKIP。生命周期测试未耗尽整个310秒 HTTP 排空期限，也不证明025多活。B3 记录中的全仓真实 PG race 属于新增纯算法和随后生命周期脚本之前的基线，新文件依各自专项证明；最终汇合检查不能由此提前声称完成。

C0 本轮设计一致性审阅沿真实001–022来源、零额两行 journal、SAVEPOINT 的 top-xid stamp、实际 runtime 权限和全钱包 prefix 核对候选。发现旧零余额 tenant 的 genesis/开账 membership 完整性需要补齐，已交作者修订；该局部检查未产生资金效果，随机 schema 与三个 LOGIN 已清理。正常消费退款的数据库不变量也已交作者：每个 settlement 的已批准退款 NUMERIC 合计必须精确等于 `refunded_amount` 且不超过 `charged_amount`；refund source/lot/journal 的检查入口须独立触达，不能只依赖业务代码额外 UPDATE settlement。旧 terminal 的 cutover 已退基线与后续新批准退款须精确相接，旧 approved 重读不发行新 lot；正常分次退款仍沿原 charged 边逆序，不恢复 Key/周期 spent。这些是候选冻结前审查项，不是运行时已接线或新验收通过的声明。

纯 lot 与 metering 各有独立专项和清晰的无 IO 边界，可以并行交付；C0 来源/helper/维护迁移及共享资金文件仍须单一负责人串行，独立 verifier 与读侧可在合同冻结后并行。架构同步继续按根 `AGENTS.md`：先补设计、再实现与专项，最后汇合复核系统图、数据和权限边界、源码映射、能力状态与证据，再显式 refresh 并执行只读检查。

根后续补验：caller-owned RR清单核验已按[C0-S](../../specs/022-self-service-payments/c0-snapshot-contract.md)实现并通过完整migrations真实PG/race专项（最终3.005s），固定安全错误及活跃事务预取消补验见[记录](migration-snapshot.md)。C0候选的birth/退款累计及journal同top-xid已补齐并通过隔离专项，见[候选验证](funding-lot-candidate.md)；仍无生产资金接线。另已由官方SHA验证的隔离Compose 5.6.1完成默认/合成覆盖的真实config CLI渲染，见[后续证据](compose-render-025.json)，原静态JSON的NOT RUN未改写；该结果不证明容器、1Panel或真实SMTP。

025-A1-M后续收口：两个真实SIGKILL/原60秒lease自然恢复窗口最终132.811s通过。根与独立读审核过SMTP查询前计时加lease age全程小于9秒、首250确认，以及FinishMail release至kill/零角色小于4.5秒；失败与最终清理及8份源码hash匹配，见[记录](mail-lifecycle-025.md)与[脱敏JSON](mail-lifecycle-025.json)。只有QA运行器/private seam变化，021产品语义、运行角色与数据模型不变；统一架构邮件时序已补同proof可重复的原租约恢复说明，无外部SMTP或多活结论。

后续候选自动回归：四份仅测试源码位于`internal/qa/c0candidate`，最终五项真实PG/race全部实际运行通过（4.133s），候选DDL/ACL固定摘要、实际LOGIN、错误SQLSTATE和完整失败前后快照见[记录](funding-lot-candidate.md)。最终随机schema及三类LOGIN均归零；APP tenant INSERT八列/UPDATE六列、worker原锁权限已核。该结果只证明冻结的直接SQL层，正式worker来源链仍待helper接线；没有生产migration、epoch或启动许可。Compose隔离引擎尝试因官方镜像下载超时未启动，本轮profile删除及既有VM未变见[记录](compose-runtime-025.json)，容器运行保持未验。

022-C1-P已新增独立`paymentrefundproto`，根与另一审阅者分别读审八份实现；独立Python手写canonical/frame/SHA/HMAC并与Go固定向量交叉核验。根集成复跑新包/v1共同race（1.870s/1.945s）、vet及Python默认离线比较全部退出0，覆盖两v1桥/四摘要域/九签名向量；作者短fuzz及完整行为边界见[记录](payment-refund-protocol-v2.md)。模块图增加只依赖v1已验证值的纯退款层，HLD/LLD、SDD、交付计划与MVP状态已同步；现请求、资金、数据库、权限和支付v1进程链无变化。退款端点、P永久拒绝墓碑、原路资金及M/S/I仍未实现，不能把纯层成功写成退款已开放。最终架构render/refresh与整仓检查仍在并行结果汇合后执行。

C0-V0-L新增`fundinglotverify`标准库纯包；根全文复核实现/固定及fuzz测试，再独立复跑race（1.412s）和vet退出0。它不import生产资金模块，当前也无生产import；图中独立纯区新增十方向账本行检查，既有资金/数据/角色/进程边界不变。独立big.Int、平台账户全额、NULL/空TEXT、孤儿行保留、重复组、100000行/16MiB逻辑界与取消见[验证](funding-ledger-rows.md)。下一RR reader尚属候选，不能用RowsConsistent代替数据库EOF或全源CoverageComplete。

## 2026-11-01：离线descriptor与同RR账本读取汇合

本节接续上文历史状态。C0离线metadata导出器已按冻结§9交付，双轮ordinary/sandbox安装的十文件逐字节一致，注入失败DDL后的精确清理通过，专库13项race和vet通过，见[证据](funding-lot-descriptor.md)。根与独立审阅者核对固定源SQL/ACL、原provision顺序、prosrc保真、依赖multiset、双轮产物/SHA及权限；正式expected、运行checker、Permit、epoch和RuntimeProof均未由此发行。校准了自动row/array默认USAGE与显式proof类型权限，未改候选SQL/ACL或其固定摘要。

C0-V0-R已按冻结合同新增私有reader及两份测试。根全文复核实现，独立审阅者交叉核入口/出口事务身份、heap/PK绑定、history表解析、两函数名全部path重载预拒、keyset EOF、字节/行界、Rows.Err/关闭和安全错误；最终15项（5纯、10实际PG）race通过8.548s，vet通过，见[证据](funding-ledger-reader.md)。它是`fundinglotverify`的首个产品代码import，但无普通运行入口；原纯包验证中“无import”保留当期范围，当前图与HLD/LLD已更新。source/Key/采购/PB/B/lot完整核验仍后续，不能把supplied rows升级成完整来源证明。

模块图增加离线生成器/专用测试资源/候选产物及私有reader→原三表/独立纯检查，当前请求、资金ER、迁移、角色与单活运行链不变；能力表、后续依赖、源码链接、HLD/LLD、交付计划和MVP状态同步。正式渠道范围另链接[供应商认证计划](../design/provider-qualification-plan.md)，不把兼容profile或同名模型当真实供应商认证。

架构检查器仅补排除精确`.vite`生成缓存组件，与既有node_modules/产物规则一致；专项race1.564s及vet通过，新增/修改/删除缓存不改清单，`.vite-source`真实源码与SDD变化仍被检测。没有扩大其他排除范围。根补跑前端286项全部通过、strict TS/Vite构建通过；新读取器最终代码的整仓集成结果在下段追加，不把此前全量结果冒充本轮。

本轮16张Mermaid图均由官方npm Mermaid12.2.0在独立临时Chrome中解析/实际渲染，根查看模块图截图；[渲染证据](architecture-c0-reader.json)、[模块截图](assets/architecture-c0-reader-application.png)保留正文SHA与图摘要。所有临时浏览器profile清理，不操作用户原浏览器或58123预览。正文、演化记录与机器清单在全部并行改动汇合后由根显式reviewed刷新，默认CI继续只读。

根最终实际执行`make verify-mvp`退出0，359.526s：入口只读架构检查、286项前端测试、strict TS/Vite、使用明确专库的全Go `-race -count=1 -timeout 600s`、全仓vet及嵌入build全部通过。billing237.832s、identity270.946s、server334.379s是包测试耗时，不是请求延迟。独立opt-in的descriptor双安装、候选SQL、邮件真实进程和浏览器运行器没有在此命令重开，仍按各自原专项证据读取；不把skip隐藏在“整仓通过”中。测试入口快照为1064文件/`8fcfb390d20b588f0358f8e8e70d77eb47b177ce39e3c20f4e87bfda98e76358`，源码未再修改，之后只补证据/SDD状态并再次显式复核刷新及只读检查，见[整仓证据](c0-integration.json)。没有升级58123、发付费模型或真实PSP请求、性能压测或生产验收。

## 2026-11-02：正式静态checker与固定expected

新增`fundinglotcatalog`叶子与两套受审固定根，模块图明确caller-owned RR→catalog/proof及静态expected输入，没有连入main或现资金链。ordinary五种完整变体、完整sandbox三schema/八角色、原文/依赖/ACL/raw类型I/O/角色数据库负权、opaque证明身份与发行空集均由根与独立审阅者交叉核；HLD/LLD、022合同、交付计划、MVP状态和[细化架构](../architecture/funding-lot-catalog.md)已同步。维护候选另记录四个冻结前缺口，未把候选写为Gate/Permit/epoch/全源proof已实现。

正式两root与旧导出器/SQL/ACL/provision pins保持受审摘要。公开链完整精确比较与metadata observer的DTO校验分型已按根冻结，依赖双EXISTS、权限ARRAY以同RR完整raw multiset逐字相等验证，保留五秒预算。两次整leaf真实失败保留；最终整个leaf race217.140秒退出0，47个顶层/179终态中176 PASS、3个独立capture/timing/export opt-in SKIP，专用资源及backend零遗留，共享public/DB ACL未变，见[完整证据](funding-lot-catalog.md)及[机器记录](funding-lot-catalog.json)。这些结果只认证PG16.15/Homebrew initdb UTF8/C/C profile，不代表其他PG16/1Panel/生产或正式供应商认证。

主架构16图与细化1图共17张，用官方Mermaid12.2.0在独立临时Chrome实际解析和渲染成功；根查看[模块截图](assets/architecture-c0-catalog-application.png)及[细化截图](assets/architecture-c0-catalog-detail.png)，两份正文SHA/图尺寸/图片SHA见[渲染记录](architecture-c0-catalog.json)。临时浏览器及profile已清理，未操作用户浏览器或58123。本轮整仓结果见下段，不沿用上轮359.526秒作为本轮结果。

根最终`make check`7.546秒、`make verify-mvp`340.981秒均退出0：286项前端、strict TS/Vite、明确专库全Go race/vet及嵌入build通过，server321.035秒、billing217.715秒为包测试耗时。C0的完整真实PG opt-in leaf另以217.140秒通过，默认整仓未重开其capture/export/diagnostic或旧真实进程运行器。测试入口快照1114文件/`529d53dc048b`；入口后Go源码未改，后续只补验证/状态及将细化图Runtime角色检查校正为collector之前，再显式复核刷新及只读检查。[完整记录](funding-lot-catalog.json)保留命令退出、日志/SHA、源码、固定root、旧pins与清理；当前main/资金/预览和生产验收边界不变。

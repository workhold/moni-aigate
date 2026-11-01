# v0.1.0-dev 实施与验证记录

2026-10-30，macOS arm64本地工作树。开发者已同意使用SDD实施r3首版范围；独立代码、UI与测试已经交付，当前没有Git提交SHA或远程发布。设计细化记录为r4，见[ADR-002](../decisions/ADR-002-implementation-refinements.md)。

2026-10-31本轮017实现Key名称、模型/地域、累计额度、有效期完整GET/PUT和控制台编辑，012迁移追加独立`configuration_revision`，当前schema为001–012。额度按锁后最新spent+held精确检查，旧字段未改时不清洗；期限保留RFC3339微秒并规范UTC，新期限转换后的年份越界在写入前拒绝，过期Key可明确续期/移除期限，撤销不可恢复。三种配置分别CAS、不重置秘密或016运行态，模型/地域只限制外层授权；变化与审计同事务，no-op无审计，旧CAS同值仍冲突。017专项/全量真实PG race、107项前端测试/strict构建、vet/build、隔离浏览器与58123升级均通过限定本地验收，结果统一见[017记录](key-configuration.md)。周期预算、分组、自助商业、正式供应商、容器/灾备、许可和性能门禁继续保留。

2026-10-31阶段016增加Key动态RPM/burst/concurrency创建、列表、只读核查及完整CAS编辑，011迁移当时扩展schema至001–011。默认60/20/16，三元组与来源分别维护`limit_revision`和`access_policy_revision`，消费及另一种配置不改变本域版本；显式null或非法整数拒绝。身份/最小角色真实PG完整race与vet及运行态专项已通过，最终全量、构建、隔离浏览器和58123升级状态统一见[016报告](key-runtime-limits.md)。下方015及更早内容保留各轮原始验证范围，不作为017最终通过证明。

2026-10-31本轮015增加Key来源IP/CIDR创建、完整展示、只读GET核查及CAS编辑。010迁移追加规范白名单、只随来源实际变化递增的`access_policy_revision`及不可变`request_records.client_ip`，迁移范围为001–010；不修改Key秘密、scopes、预算、期限或地域。共享规范化/身份/最小角色完整race与vet、资金专项race已通过；最终全量、前端构建、隔离浏览器与58123升级执行状态以[015报告](key-access-policy.md)为准，不从历史验收继承。下段起保留014及更早原始记录。

最新更新至2026-10-31的[014收口](mvp-release-qualification.md)：默认渠道global与客户/Key授权一致、可信反代登录限流/metrics传输身份已修复并回归；隔离真实main浏览器完成配置→双人核款→客户Key→JSON/SSE→账单→双人退款、发现/双成员池/批停启及历史查询。只读账本核对可用1,234,527微元、held0、3次唯一收费共60/退款20。最终66项前端测试/strict构建、全Go race（server127.173s）、vet和嵌入build通过；最终QA两轮真实main/失败清理专项race10.632s通过，资源无残留。真实main正常SIGTERM保留在途SSE/唯一结算/重启不重放也通过，58123已升级并与27assets匹配。下文保留历次验证记录；发布门禁仍见[MVP状态](mvp-status.md)。

## 当前可运行闭环

平台首次引导（MFA默认可选） → 创建模型/协议profile/渠道 → 发布完整revision和售价 → 邀请客户与授权 → 财务制单/另一账号核款 → 客户创建受限Key → `/v1/models`与Chat/Responses/Messages文本、基础工具、JSON/SSE，Gemini原生纯文本JSON/SSE，或Embeddings/Jina Rerank非流检索 → 同事务收费/释放 → 客户钱包/请求/流水 → 成本核对、报表、双人退款与审计。基础函数工具只转发，不执行工具；Playground支持实际同源请求、取消及request_id排障。

人工资金金额以微元十进制字符串传输和整数计算；`1000输入×¥2/M + 500输出×¥8/M = 6000微元 = ¥0.006`。请求先按硬上界冻结R，最终可信usage结算A≤R；unknown不造量收费。缺失量且可能消费时待核对，24h未结算释放，后到供应商成本不能自动再扣客户。

当前实际模块在[LLD](../design/LLD.md)及`internal/`，前端在`web/`。所有数据库测试独立schema/角色，本地供应商均为HTTP/TCP Mock，没有真实付费模型或支付调用。

## 已执行验证

环境：Go 1.27.2（go.mod最低1.26）、Node 22.23.1、npm 10.9.8、PostgreSQL 16.15。测试PG为仅本任务创建的127.0.0.1:55439/moni_test，非生产数据。

| 检查 | 命令 / 证据 | 结果与边界 |
|---|---|---|
| 全Go race回归 | `TEST_DATABASE_URL=... go test -race -count=1 ./...` | PASS；真实PG资金、身份、网关、角色/重包/实例、真实HTTP/TCP协议与路由均运行。手动容量开关默认跳过，另行执行 |
| 最后路由发布回归 | `... go test -race -count=1 ./internal/server -run TestRoutePublish` | PASS；拒绝同channel不同upstream重复绑定，失败整体回滚且旧revision仍可实际调用；之后重新vet/编译/前端构建 |
| 静态检查与编译 | `go vet ./...`；`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` | PASS |
| 实际main恢复 | [备份演练](backup-restore.md) | PASS；真实受限角色、未来deadline/held保留、env=false启动仍recovery=true，收费/核款503，SIGTERM正常退出 |
| 收尾HTTP回归 | `... go test -race -count=1 ./internal/server -run TestHTTPRecovery` | PASS；DBgate锁存、角色/再认证、不同账号审批，审批后仍不自动重开进程 |
| 前端 | `npm --prefix web test`；`npm --prefix web run build` | 本轮12 tests PASS；strict TypeScript及生产构建通过，含三协议Playground与取消 |
| 真实Chrome | [控制台记录](console.md) | MFA、授权、精确核款/钱包、Key/成员、成本版本/更正、客户报表、密码修改及390px布局 |
| 数据库最小角色/密钥 | [operations](operations.md) | 实际受限角色完成资金闭环；禁止篡改历史/迁移；AAD交换/密文篡改拒绝；新旧keyring重包/回滚 |
| 备份恢复 | [演练](backup-restore.md)、[原始JSON](backup-restore-local.json) | 独立新DB恢复账本和凭据；备份后资金事实不会凭空恢复，需外部回执核对；持久gate防env漏配 |
| 容量 | [capacity](capacity.md)、[007完整对照](dispatch-read-batches.md) | 007六轮各100/100接受、DONE、POST、结算，账本平衡、held/许可归零；完整Guard/资金路径每轮额外TTFB p95=61.56–69.74ms，未达50ms；保留基线异常与一对完成回退及全部旧结果 |
| 容器 | `docker compose --env-file .local/review.env config --quiet`尝试 | 本机Docker CLI缺少Compose子命令，报unknown flag；未完成Compose解析/镜像构建/启动，不算通过 |

完整业务判据与证据缺口见[52项执行范围](acceptance-execution.md)。上表PASS只对实际命令与环境成立，不能覆盖未执行的生产步骤。

## 安全和故障收尾

- 会话家族退出不能被旧切租户请求复活；MFA绑定后撤销旧家族，改密需原密码/已启用MFA现验并撤销全会话。owner部分更新和Key创建锁内重读权限，防止旧会话扩权。
- 供应商秘密采用v2 AES-GCM，AAD绑定用途/记录ID/key版本；复制密文到另一credential不能解开。主密钥重包保持业务ID和上游秘密不变。
- 计费只共享必要的父行锁并锁实际分录账户；明确中止的40P01/40001最多重试2次，结果未知不盲重试；永久资金幂等与唯一流水避免重扣。
- 独占锁会话失效会永久fence旧进程。新业务入口和T1.5后派发前即时检查；失败不发上游且停止ready。接管等待旧持久请求执行窗口，避免内存限额立即重叠；网络边界不承诺远端exactly-once。
- 明确模型配置错误会持久隔离精确channel_version/upstream_model，其他模型/版本不受影响。重建Server仍拒绝，单独改渠道未发布不能解除；显式新版本发布后恢复，旧隔离保留。
- Probe采用已发布版本、4并发上限，共享global100和渠道许可；操作有审计与独立平台成本，不动客户钱包。
- 恢复在数据库事务中latch，启动按DBgate与RECOVERY_MODE取OR；付费/核款/退款关闭。外部回执核对后财务maker/checker双人确认，账本必须平衡，仍需运维重启退出恢复。
- 公开错误和异步日志仅使用白名单元数据。成功敏感变更审计与业务同tx，日志丢弃不会成为收费依据。不可把HTTP503或写失败理解为上游一定没有执行。慢writer/并发队列测试通过：1024条、8MiB排队字节、16KiB含换行单条；另有最多一条在writer中。sink失败也计Dropped；不支持deadline的sink可阻塞唯一writer，生产者仍有界不等待。

## 交付与运行

[本地运行](../runbooks/development.md)、[Compose / 1Panel](../runbooks/deployment.md)、[备份/恢复](../runbooks/backup-restore.md)。私有运行环境、测试PG数据、预览凭证、编译产物和node_modules均被Git忽略。

本地预览使用独立moni_review数据库与localhost:58123；review@moni.local是否使用MFA由评审者自行选择，已启用时仍须验证，初始密码仅在`.local/REVIEW-CREDENTIALS.md`。演示资金/渠道为假数据，演示Key已撤销、模型禁用。

003已新增原生Responses/Claude文本与基础工具、平台probe和Playground，见[原生协议验收](native-protocols.md)。006迁移增加不可变选路解释；另补[五个真实SIGKILL边界与结算后交付失败](mvp-failure-recovery.md)，修复Gin吞掉FlushError的交付观测缺陷。完整race与最终定向集成通过，资金/身份父锁批处理优化另有16个真实撤权等待屏障验证。

该收敛版本实际执行`make verify-mvp`成功：12项前端测试、strict TS/Vite、真实PG全Go race、全库vet及嵌入编译。billing23.830s、identity64.535s、operations5.333s、provider3.588s、server128.890s均为包测试时间。004验证独立输入/输出界、缺省完整上下文与R140/实际118/释放22；fresh Guard检查批次、Claude上下文终止与表单清空null也包含在当前版本。容量在该命令结束后单独运行，见[容量页](capacity.md)的v4历史原始报告。

正式发布前仍需目标资源性能、真实渠道协议/计量合同、容器首装升级和长时故障演练、异地灾备及最终许可选择。本版没有自动支付、HA或媒体，不能借用上游功能清单把这些描述为已实现。

后续005/006已实现按Key短准入/完成调度，并修复准入panic未转交渠道许可的泄漏。全Go race最后通过（server92.625s、billing16.991s），vet和编译通过；完成队列另有真实客户取消与5s超时/8s后worker恢复。新一轮三版轮换对照保留005的完成回退和006的修复，组合版首byte p95=69.80–77.76ms、全部完成162.29–174.62ms，详见[Key调度证据](key-scheduling.md)。前端未改动，仍使用上次已验证的dist；不把局部突发结果认作50ms或生产验收通过。

007保留原COMMIT、锁序及新快照，合并发送原派发只读查询和账户初始化/加锁语句，正常首次准入少9次应用等待。真实PG测试发现并修复pgx v5.7.6最后Rows扫描错误后首次Close未收尾pipeline的问题。专项race12.081s、全Go race（billing40.537s、server158.600s）、vet与嵌入构建通过，前端无变化。六对普通binary对照保留第一轮before异常，正常五对TTFB p95改善4%–9%，007全部六轮61.56–69.74ms；一对完成回退3.29%，不宣称吞吐一致改善，详见[全部原始证据](dispatch-read-batches.md)。

2026-10-31改为功能完善优先，008性能方案后移。009新增Embeddings/Jina Rerank的严格协议、聚合输入硬界/零输出价格与预占、最终usage计费、probe与五协议Playground。最新专项与全量执行状态见[009验证](retrieval-protocols.md)，不引用旧007成绩代表新协议性能，也不替代真实供应商与生产门禁。

同日010新增Gemini原生文本JSON/SSE，复用原商业生命周期、300s/Guard/恢复门禁，使用URL模型与不可变映射、上游x-goog-api-key、含模型/action的幂等身份。thinking计入输出，cache作为输入子集，未知计费维度/托管工具证据待核对；SSE保留finish直至尾量、完整EOF、最终证据及结算提交。平台发布/probe与六协议Playground一起交付。专项race、全Go race（billing26.922s、server93.066s）、30项前端测试、strict TS/Vite、vet及最终嵌入build通过；58123预览ready、recovery_mode=false，嵌入首页与dist一致。本轮无浏览器账号操作、付费调用或性能压测；[010完整证据](gemini-native.md)区分本地通过与正式供应商/SDK/工具媒体缺口。

011新增模型发现：三格式供应商目录、受控GET/分页与实际字节预算，固定渠道/凭据/已发布绑定快照，展示未绑定及目录存在/缺失绑定、历史和动态stale。新007迁移追加不可变开始/唯一结果、完整身份复合外键与最小角色；开始/终态审计原子提交，读取中不持事务、失败不保留部分成功目录、不影响客户资金或推理健康。专项、全Go race（operations5.142s、provider3.792s、server93.695s）、39项前端测试、strict TS/Vite、vet及最终build通过。本机预览已依次排空、migrate、owner provision与受限serve，ready非恢复模式且资源匹配；浏览器只检查登录页。细节、边界与两项复核修正见[011证据](model-discovery.md)。


012新增不可变凭据池：1..32成员、完整CAS/历史、元数据保留身份、独立永久撤销、渠道/成员两阶段权重与双层容量。新池必须显式重新发布路由，旧revision不被替换；数据库同事务封口阻止提交后晚追加，attempt/probe/发现均引用完整冻结成员身份。RecordAttempt/PrepareDispatch保留撤销屏障，未知消费不换Key，同渠道不重试；平台probe/发现记录实际成员且钱包不变。50项前端测试/strict build、真实PG全Go race（billing28.550s、catalog2.766s、operations6.048s、server96.936s）、vet及最终嵌入build通过。008迁移、owner角色配置与受限serve已更新58123预览，ready且非恢复模式，首页和全部27个assets与dist一致。浏览器仅验证登录页，未修改评审账号/MFA或真实客户资金，未做性能压测。详见[012证据](credential-pools.md)，后续继续跨渠道批量运营与自助经营；这不等于New API全功能或生产门禁通过。

013增加跨渠道批量启停：当前页最多50项（API100项）、无网络预检、版本+字符串management revision校验、整批应用/拒绝、永久幂等回执与历史。启停实时影响历史发布绑定，新池仍须显式发布；不清隔离/撤销、不改路由/资金。009为全部渠道写入口维护单调revision，完整回执/唯一审计同事务且不可变。专项、66项前端测试/strict构建、全Go race（billing34.213s、catalog4.576s、operations7.391s、server104.381s）、vet及最终嵌入build通过。009迁移、owner角色配置与受限serve已更新58123，ready非恢复模式，首页与全部27个assets匹配；实际只读权限验证通过。独立复核修复回执晚回串线与after合同；未执行登录后的浏览器交互、付费调用、评审账号/MFA或真实资金修改，未做性能压测。详见[013证据](channel-operations.md)，其余渠道运营、Key扩展、自助经营及原生产门禁继续推进。

015来源策略由服务端按显式可信代理规则固定来源；所有API Key入口先筛选，Reserve/RecordAttempt/PrepareDispatch在现有锁序下检查最新策略。`PrepareDispatch`提交`sending`是发送授权同步点，随后收紧不撤回已准入请求，也不阻断可信量结算、释放、退款或恢复；未知来源的历史请求在受限策略下不得创建新attempt/派发。CAS旧版本即使同内容也拒绝，当前版本同规范内容no-op不新增审计；变化与唯一策略审计同事务，失败回滚。GET只返回安全策略与版本，网络失败/5xx后的当前观察不等于持久命令回执，不自动重发。015仅是NA-07来源切片，动态RPM/burst/concurrency、其他Key属性编辑及原生产/性能门禁均未因此完成，详见[015执行边界](key-access-policy.md)。

016运行准入由`WithKeyRuntimeLimits`按tenant/membership/user/Key锁读取当前有效身份及三元组，再执行纯内存令牌/容量检查，与PUT互斥；回调后事务提交失败归还已授许可，已消耗令牌不回退。桶只在下一次检查观察新版本时按旧速率推进后切换；提高burst不赠令牌、降低burst截断余额，不按revision新建桶。并发降低不清零或撤回已有许可，队列及整个调用收尾都持有许可，safe retry不重复扣令牌。单活动实例最多4096桶、运行中无TTL驱逐，满时新Key503，重启重新满桶；平台6000RPM/burst200、租户300RPM/burst40及全局100/租户32并发继续生效。模型目录不扣运行令牌，容量拒绝不产生预占/attempt/收费。Key运行准入与015的`sending`授权同步点不同，来源、撤销、模型和资金复查及后续结算/释放/退款/恢复继续生效；其余Key属性编辑、周期预算、平台/租户动态限额、分布式计数和原生产/性能门禁不在016完成范围，详见[规格](../../specs/016-key-runtime-limits/spec.md)与[验收](key-runtime-limits.md)。

017专项覆盖完整JSON存在性/null/数组元素类型、UUID/版本/金额、UTC微秒与年份边界，历史名称/scopes/地域原样保留，独立触发器和最小角色，CAS/并发/审计失败回滚，以及锁等待后的当前身份、撤销、最新冻结金额和期限。资金与HTTP专项按六协议检验最新限制和在途收尾，编辑器分别保留三域较高revision，不用配置快照覆盖spent/held；未知结果只读核查且不能据相同值归因。最终全量、构建、隔离浏览器和预览迁移/角色/资源核对也已通过限定本地验收，见[017记录](key-configuration.md)。

017最终执行107/107前端测试、strict构建、专用真实PG全Go race、vet及嵌入build全部通过。隔离真实main浏览器完成基本配置/来源/运行编辑与2次SSE、1次JSON调用，3次各20微元、合计60；配置预算409、调用预算402、地域503拒绝前后只读快照逐字段一致，configuration/source/runtime版本为5/2/2。390px无横向溢出，schema/角色/listener/秘密均已清理。58123已完成012迁移与owner provision，以非owner/superuser的受限app启动，projector不可读Key新增字段，ready非恢复模式，首页及28assets与最终dist逐字节一致；正式供应商、容器/灾备、许可及性能门禁保持原范围。

2026-11-02：022-C0-T2按[根冻结](../../specs/022-self-service-payments/c0-authority-contract.md)实现唯一二参Attest和Permit.Verify，复用T1私有Snapshot与原busy/总期限。新增固定manifest+完整history guard/helper、实际维护身份、新鲜clear→runtime采样、16表重空、完整ordinary/v1前驱、Init全账户C序锁、出口与共享撤销；原v1、迁移/ACL/source pins保持。并行纯codec golden与真实PG公开链/私有判定层分别验收，最终[专库race专项](maintenance-authority.md)333.879秒、89顶层/832父子终态全部PASS，整仓回归与源码/文档快照另记。T3全来源/ReadLease/basis/epoch及普通main/资金调用者仍未实现。

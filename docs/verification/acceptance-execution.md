# SDD 验收执行范围

2026-10-31更新至017 Key基本配置切片，v0.1.0-dev本地工作树；尚无Git提交SHA。原始52项完整判据见[acceptance.md](../../specs/001-commercial-mvp/acceptance.md)。本页区分直接测试范围与缺口，**不是52项全部通过的发布证明**。017专项/全量真实PG race、107项前端测试/strict构建、vet/build、隔离浏览器和预览升级已通过限定本地验收，状态集中见[基本配置记录](key-configuration.md)。015/016分别见[来源报告](key-access-policy.md)和[运行限额报告](key-runtime-limits.md)，下方原AC表及历次结果保留其原验证范围。

“已验证”仅指所列受控环境/具体场景；“部分”表示原判据尚有未执行部分；“未达标”保留原门槛。代码可运行与生产验收是两个阶段。测试索引：

- [B：资金PG](../../internal/billing/store_test.go)：并发冻结、金额/版本、同tx回滚、事件重放、恢复/期限、退款/双人审批、投影/重建。
- [A：身份PG](../../internal/identity/identity_integration_test.go)、[H：鉴权HTTP](../../internal/server/auth_integration_test.go)、[C：收尾HTTP](../../internal/server/completion_integration_test.go)。
- [G：Chat真实HTTP+PG](../../internal/server/gateway_integration_test.go)、[P：Provider真实HTTP/TCP](../../internal/provider/network_test.go)、[S：SSE](../../internal/provider/response_test.go)、[R：路由](../../internal/routing/routing_test.go)。
- [O：受限角色/主密钥](../../internal/operations/operations_integration_test.go)、[I：实例保护](../../internal/operations/instance_test.go)、[D：恢复演练](backup-restore.md)、[U：真Chrome](console.md)。
- [015来源规范化](../../internal/security/client_ip_test.go)、[身份CAS/审计](../../internal/identity/key_policy_integration_test.go)、[资金锁屏障](../../internal/billing/key_access_policy_integration_test.go)及[最小角色/来源不可变](../../internal/operations/key_policy_integration_test.go)：补充Key来源切片，不扩大原AC为生产全通过。
- [016身份/最新锁内准入](../../internal/identity/key_limits_integration_test.go)、[数据库约束/独立版本/最小角色](../../internal/operations/key_limits_integration_test.go)、[确定性运行桶](../../internal/server/key_runtime_limiter_test.go)及[HTTP运行限额](../../internal/server/key_runtime_limits_integration_test.go)：补充NA-07动态限额范围；最终集成证据以016报告为准。
- [017身份CAS/金额/期限/旧数据](../../internal/identity/key_configuration_integration_test.go)、[独立版本/最小角色](../../internal/operations/key_configuration_integration_test.go)及[规格](../../specs/017-key-configuration/spec.md)：补充NA-07基本配置编辑；严格HTTP/资金锁屏障、最终全量/构建、隔离浏览器和预览核对均已通过限定本地验收，不扩大001原AC范围。

| AC | 执行状态 | 直接证据与未覆盖部分 |
|---|---|---|
| 01 | 部分 | H/B跨租户列表/ID访问与角色隔离；首版没有导出入口，未对所有未来接口作全量ID枚举 |
| 02 | 已验证 | A/H/U一次性Key、摘要存储、撤销新准入；在途不强制杀流语义由G/B覆盖 |
| 03 | 已验证 | A/H/P默认空授权拒绝、模型目录与Key/成员交集 |
| 04 | 已验证 | provider/request_test.go拒绝n=2、媒体、未知/越界参数，在网络前失败 |
| 05 | 已验证 | S/G任意拆分、CRLF/多data、重复最终量、完整event上限 |
| 06 | 部分 | R优先级/权重/容量、revision及attempt版本；006补齐每个已准入attempt的全部候选选择/排除理由，真实PG验证不可变、平台可查/客户不可访问。未产生attempt的完整失败池解释仍未持久化，旧记录显示缺失 |
| 07 | 已验证 | R/P/G统一预算、无消费证明、普通500/未知429不重试、已提交SSE不重放 |
| 08 | 已验证 | B整数/大数取整与1000/500示例=6000微元；cached/reasoning子集不重复 |
| 09 | 已验证 | B售价快照、不可变历史、切渠道不改客户售价 |
| 10 | 已验证 | B真实PG多Key并发，余额仅够一笔R时唯一成功且不负数 |
| 11 | 已验证 | B预算/held/spent与钱包同tx、竞争/释放/退款边界 |
| 12 | 已验证 | B/O平衡约束、不可修改已提交分录、回滚与余额校验 |
| 13 | 已验证 | B唯一结算/释放、幂等重放、提交后按业务事实查询；未声称外网exactly-once |
| 14 | 已验证 | B封顶R、豁免独立记录、profile暂停，成本不加豁免 |
| 15 | 已验证 | B/U同业务号/摘要冲突、不同制单审批、重复审批不重复加款 |
| 16 | 已验证 | B并发退款上限、原消费保留、补偿流水；U文案为退回钱包 |
| 17 | 部分 | B真实事务失败注入与恢复扫描，G实际TCP取消；本轮五个真实子进程在预占、派发及最终量提交后SIGKILL并扫描恢复，无追加POST/收费，见[记录](mvp-failure-recovery.md)。完整main/容器/所有阶段矩阵仍未完成 |
| 18 | 已验证 | B持久截止、重复worker、到期释放且后到成本不追扣 |
| 19 | 已验证 | G/P真实取消关闭上游、未知量待核对、执行资源退出后许可回收 |
| 20 | 已验证 | C/U报表/钱包/账本准确、unknown不作0；财务角色投影隐藏客户授权/邮箱 |
| 21 | 部分 | I实际终止PG锁会话、第二实例拒绝、C派发前失锁无网络、启动窗口排空；014真实main正常SIGTERM保留在途SSE/唯一结算，重启不重放。真实DB网络分区及容器升级排空未演练 |
| 22 | 部分 | A/H/C/U会话/CSRF/MFA/恢复码/再认证/改密；014可信反代IP/歧义头/伪造头限流与metrics transport peer专项通过，未做真实多来源暴力压测 |
| 23 | 已验证 | P真实解析/拨号/重绑定/重定向/元数据/授权私网；probe共用相同Provider安全栈 |
| 24 | 已验证 | security及O的v2目的/记录ID绑定、篡改/AAD交换拒绝、旧新密钥重包与备份keyring |
| 25 | 未达标 | [容量](capacity.md)及[007对照](dispatch-read-batches.md)保留旧结果；007六轮各100并发成功且资金/资源归零，每轮额外首字节p95=61.56–69.74ms；首轮before异常、一对完成回退均公开，仍高于50ms，非4vCPU/8GB，长时慢客户端/持续吞吐未认证 |
| 26 | 部分 | D本地全库+keyring恢复/持久gate/HTTP关闭收费、双人解除；实际异地灾备/生产RPO-RTO未验 |
| 27 | 部分 | [来源清单](../research/dependency-provenance.md)与ADR一致；项目许可证未选，17个未下载Go图节点许可未实核，最终发行审核未做 |
| 28 | 已验证 | B/C/U失败尝试成本独立、后到账单更正不改客户消费、unknown显式；平台probe成本独立待核对 |
| 29 | 已验证 | A/H/B成员降权/停用/模型撤回再准入、旧历史保留 |
| 30 | 已验证 | B跨24h迟到证据与行锁屏障、release/settle竞争、人工入口同样检查DB时钟 |
| 31 | 部分 | R/G/probe并发容量/取消回收与no_capacity释放；RPM默认值已实现，未对全部平台/租户/Key组合做持续速率压测 |
| 32 | 已验证 | B成本版本快照/每attempt独立、后到成本附加更正；C/U成本修正不改客户价 |
| 33 | 已验证 | B提案冻结、不同角色审批、可信量/期限/R约束，不能resolve绕退款 |
| 34 | 部分 | catalog发布完整版本+CAS校验，B PublishedRouteSnapshot验证旧revision在途与stale新准入；C验证同channel不同模型绑定发布失败且旧策略继续服务；并发发布所有非法组合HTTP枚举未齐 |
| 35 | 已验证 | H注入header不扩权，P严格参数，B派发撤权屏障/硬约束/未发送释放 |
| 36 | 已验证 | B事件source命名空间、同身份内容冲突、最终累计量不相加、attempt追溯 |
| 37 | 部分 | B/O投影事务失败/重放/重建/受限权限，事实与账本不改；全阶段进程强杀和30日真实时间保留演练未做 |
| 38 | 部分 | P/S统一公开错误不转发上游原文、配置URL严格校验、元数据固定结构；受控证据引用拒绝URL/查询串/JSON/明显凭据前缀；未将所有header/嵌套标记排列跑成全出口扫描 |
| 39 | 部分 | 有界异步元数据日志，资金/审计写失败B回滚；慢writer/32并发producer的1024条/8MiB/16KiB/丢弃计数与退出已race验证；真实磁盘/日志代理阻塞和持续流未压测 |
| 40 | 已验证 | H/C/G与HTTP映射：401/403/402/429/503，数据库失败不降级放行 |
| 41 | 已验证 | provider合同+request/S tests，工具只转发、tool-only/length/stop终态、预占上界；正式渠道未验证前不得启售 |
| 42 | 已验证 | P/S/G missing/null/0/冲突、include_usage双偏好；缺量billing_pending且无成功DONE |
| 43 | 已验证 | S/P/G字节分帧/UTF8/CR/LF、错误后DONE/裸EOF不伪成功、先有效事件再提交、提交后不重试 |
| 44 | 部分 | B收费矩阵、G最终量/取消/交付观察；本轮受控ResponseWriter对非流正文、SSE终态Write/Flush失败验证收费已提交且保留，修复Gin吞FlushError。真实客户端kernel/network全部故障组合未齐 |
| 45 | 部分 | T1.9后真实SIGKILL子进程验证无内容释放、有内容可信量收费、截止释放，重复恢复幂等；24h采用明确时间压缩fixture。014另补真实main正常SIGTERM/重启不重放及投影worker；网络分区/完整故障矩阵仍未验 |
| 46 | 已验证 | B逐资金写边界失败注入、审计/outbox同成败、已中止SQLSTATE重试不重复收费；无另取pool事务helper |
| 47 | 已验证 | P/R/G真实TCP POST计数、禁重放/重定向/环境代理、拨号与生成分开；部署外层代理重试由runbook要求关闭，容器实测未做 |
| 48 | 部分 | P/S取消/heartbeat进展/总deadline/解压与event限额；G真实关闭socket，清理ctx有界；长时慢writer压力未验 |
| 49 | 已验证 | B真实READ COMMITTED父行SHARE/撤权UPDATE与新语句、grant增删同步、权限/锁错误不能派发 |
| 50 | 部分 | P/S/G错误白名单、provider原文不进入公开错误；敏感标记全出口排列扫描与长时堆画像未齐 |
| 51 | 已验证 | O/B不可变credential/channel/route与紧急撤销、AAD重包不换秘密、旧备份需旧key；真正异地密钥归档恢复待上线演练 |
| 52 | 部分 | R半开中性/旧generation、裸401/403不误隔离、明确credential持久撤销，429冷却；明确机器码的模型绑定隔离及重启/新版本恢复已PG纵向验证，裸404不隔离；长期真实错误样本未核实 |

发布门禁仍含性能目标与目标资源验证、选定真实供应商profile的usage/无消费拒绝/成本口径、容器首装/升级/排空、进程强杀与网络分区、异地备份和密钥恢复及许可发布选择。2026-10-31开发优先级改为先补齐业务能力，再推进性能专项；门禁不会因优先级调整或开发版页面可操作而自动通过。

003三协议与Playground为额外功能切片，独立验收见[原生协议记录](native-protocols.md)，不据此把001剩余发布门禁勾绿。最后集成定向race（provider/server，含空header歧义、原生取消、路由解释与交付失败）通过：2.880s/4.235s。

收敛版本最终`make verify-mvp`通过：12项前端测试、strict TS/Vite、真实PG全Go race、vet及嵌入编译。包含004独立界与真实预占140/收费118/释放22样例、fresh batch Guard及006；provider3.588s、billing23.830s、identity64.535s、operations5.333s、server128.890s均为包测试时间。仅有旧provider测试仍断言输入/输出之和的首轮失败已按004校正并全量重跑通过，未删除越界拒绝验证。

009的Embeddings/Jina Rerank为后续功能切片，专项证据和最新全量结果见[独立验收](retrieval-protocols.md)。输入计费仅对白名单协议放开零输出；未知usage仍待核对、阻止结果交付；不将009通过等同于001全部52项或New API全功能通过。

010 Gemini原生文本为后续功能切片，见[独立验收](gemini-native.md)与[清单](../../specs/010-gemini-native/acceptance.md)。专项、真实PG/HTTP及全Go race、30项前端测试/strict build、vet/嵌入编译和预览资源匹配均通过；覆盖thinking/cache计费、终态晚于结算、尾usage与完整EOF、未知量pending、模型/action幂等及原权限/恢复门禁。该窄范围本地验收不补齐001剩余生产门禁；正式供应商聚合硬界、完整SDK、Gemini工具/媒体和性能未认证。

011模型发现为后续渠道运营切片，见[独立验收](model-discovery.md)与[清单](../../specs/011-model-discovery/acceptance.md)。专项/全Go race、39项前端测试/strict build、vet/嵌入编译、007迁移/最小角色及预览资源匹配通过；覆盖三目录格式/安全分页、版本/凭据与绑定快照、动态stale、历史/权限、唯一结果/同事务审计与失败/取消/并发清理。只作人工配置参考，不自动发布或影响客户资金；正式供应商兼容、凭据池/批量运营与原生产门禁仍后续验收。


012新增不可变凭据池：1..32成员、完整CAS/历史、元数据保留身份、独立永久撤销、渠道/成员两阶段权重与双层容量。新池必须显式重新发布路由，旧revision不被替换；数据库同事务封口阻止提交后晚追加，attempt/probe/发现均引用完整冻结成员身份。RecordAttempt/PrepareDispatch保留撤销屏障，未知消费不换Key，同渠道不重试；平台probe/发现记录实际成员且钱包不变。50项前端测试/strict build、真实PG全Go race（billing28.550s、catalog2.766s、operations6.048s、server96.936s）、vet及最终嵌入build通过。008迁移、owner角色配置与受限serve已更新58123预览，ready且非恢复模式，首页和全部27个assets与dist一致。浏览器仅验证登录页，未修改评审账号/MFA或真实客户资金，未做性能压测。详见[012证据](credential-pools.md)，后续继续跨渠道批量运营与自助经营；这不等于New API全功能或生产门禁通过。

013增加跨渠道批量启停：当前页最多50项（API100项）、无网络预检、版本+字符串management revision校验、整批应用/拒绝、永久幂等回执与历史。启停实时影响历史发布绑定，新池仍须显式发布；不清隔离/撤销、不改路由/资金。009为全部渠道写入口维护单调revision，完整回执/唯一审计同事务且不可变。专项、66项前端测试/strict构建、全Go race（billing34.213s、catalog4.576s、operations7.391s、server104.381s）、vet及最终嵌入build通过。009迁移、owner角色配置与受限serve已更新58123，ready非恢复模式，首页与全部27个assets匹配；实际只读权限验证通过。独立复核修复回执晚回串线与after合同；未执行登录后的浏览器交互、付费调用、评审账号/MFA或真实资金修改，未做性能压测。详见[013证据](channel-operations.md)，其余渠道运营、Key扩展、自助经营及原生产门禁继续推进。

014完成MVP本地闭环收口，详见[完整证据](mvp-release-qualification.md)：隔离浏览器实际配置渠道/双成员池/模型路由/价格/客户授权，双人模拟核款、客户受限Key、非流与SSE、账单、双人退款、目录发现及批停启/预检/applied回执/刷新后历史与ID查询。3次成功各20微元，退款20，最终可用1,234,527/held0，投影与账本一致；停用失败没有上游或资金事实。修复渠道默认地域及可信反代登录限流，新增真实main正常SIGTERM在途SSE/唯一结算/重启不重放专项。前端66项/strict构建、真实PG全Go race（server127.173s）、vet与最终嵌入build通过；QA最终源码两轮真实main及失败清理专项race10.632s通过，旧业务实例与专项资源均清理。58123已按owner迁移/配置权限、受限serve更新，ready非恢复模式、首页与27assets逐字节匹配。本轮没有性能压测、正式供应商/容器/网络分区或异地灾备认证，也未修改评审账号/MFA/真实资金；不将014本地通过等同001全部发布门禁通过。

015增加Key来源IP/CIDR创建/展示、只读核查及CAS编辑；最多32项、原始总4096字节，空列表兼容旧Key。010数据库约束保证规范来源、版本防伪及请求来源不可变，消费计数不改配置版本。共享规范化、身份/权限/审计与最小角色完整race/vet、资金专项race已通过，包括Reserve/attempt/dispatch真实锁等待后的最新策略拒绝、冻结释放、旧NULL来源、已sending后唯一结算/恢复/退款。`sending`提交后已获发送授权，收紧不撤回请求。409与网络未知结果只能先GET当前策略、人工复核，没有命令回执或自动重发；完整验收边界以[015报告](key-access-policy.md)与[清单](../../specs/015-key-access-policy/acceptance.md)为准。动态RPM/burst/concurrency、周期预算和其他Key属性编辑仍未实现；本轮不把历史浏览器/预览结果当作015已执行，也不补齐001原生产门禁。

016补充动态RPM/burst/concurrency：创建默认60/20/16、严格整数及null处理，011独立版本/约束与安全GET/PUT；当前版本同tuple no-op，旧CAS仍冲突，变化与审计同事务。身份/最小角色完整真实PG race与vet、运行态专项已通过；浏览器、全量构建和预览最终执行范围只以[016报告](key-runtime-limits.md)与[清单](../../specs/016-key-runtime-limits/acceptance.md)为准。运行检查在Key锁下取最新限额，保留旧桶和在途计数，4096桶无TTL、重启满桶；平台/租户固定速率/容量仍共同生效，不是分布式或持久速率控制。限额下调不撤回已获运行许可的排队/在途调用，来源等后续复查与资金收尾不绕过；429容量拒绝不产生资金事实。未知提交必须只读核查，GET不提供命令归因。其余Key属性编辑、周期预算与分组商品仍未做，不将016等同New API全能力或001生产发布门禁全部通过。

2026-10-31，017基本配置编辑已实现名称、模型/地域、累计额度及期限五元组，012迁移使017时schema为001–012，并独立维护`configuration_revision`。专项真实PG覆盖CAS/no-op/审计原子性、当前权限/最小角色、三版本互不干扰、旧字段未改保留、spent+held超int64之和无溢出、预算下调与锁等待，以及UTC微秒/归一化年份边界无写入拒绝。过期Key可明确续期或移除期限，撤销Key不能恢复；模型/地域只限制外层授权，已sending后的SSE和资金收尾继续。严格API与前端编辑器实现完成，未知结果先只读核查、不自动重发；107项前端测试/strict构建、最终真实PG全Go race、vet/build、隔离浏览器及预览升级核对均通过限定本地验收，见[017记录](key-configuration.md)。周期预算、分组商品、自助商业、正式供应商、容器/灾备、许可与性能门禁保持原缺口。

017隔离浏览器资金核对为2SSE+1JSON，各20微元、总计60；配置预算409、调用预算402和地域503拒绝前后快照逐字段一致。三域版本为configuration5/source2/runtime2，390px布局无横向溢出，测试schema/角色/listener/秘密清理已验证。58123完成012迁移/provision并以受限serve运行，ready且非恢复模式，首页和28项assets与dist逐字节匹配，实际app非owner/superuser、projector不可读Key新字段。这些本地证据不改变上方001原AC及生产/性能缺口。

## 018 成本感知路由与统一架构（2026-11-01）

018设计、实现及限定本地验收已完成，当前schema为001–013。真实PG全Go race、122项前端/strict build、vet/build、独立复核通过；隔离真实main浏览器完成采购报价、完整成本路由、并发冲突只读核查、移动端与两次SSE。客户收费20+20、采购成本8+22，冻结0、账本平衡；mock两次POST与两次唯一结算相符。运行器正常/失败清理专项通过，本次测试资源/秘密清理已核验。58123排空旧实例、owner迁移013/provision后受限启动，ready非恢复，首页及29assets一致。完整证据及明确边界见[018记录](cost-aware-routing.md)。

统一架构五图/源码与设计映射、AGENTS复核要求、显式refresh及Make/CI只读增删改检查已接入，见[架构专项](architecture-sync.md)。019采购账户/共享预算已有[未冻结设计讨论稿](../design/procurement-accounts.md)，尚未实现。上述证据不替代001原AC中的正式供应商、容器/1Panel、灾备、许可或002性能/竞品门禁。

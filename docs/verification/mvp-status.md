# MVP 交付状态

2026-11-03，`v0.1.0-dev`，本地可运行的商业网关MVP候选。人工入账、授权、多协议调用与结算的基础闭环已实现；正式支付扩展、001/002中的生产发布与性能门禁尚未全部通过，不能标成生产试运营验收完成。

当前扩展状态以此表及[统一架构](../architecture/README.md)为准；下文各轮测试、预览PID与二进制摘要保留当期范围，不表示所有轮次已部署到当前预览。

| 工作面 | 当前已验范围 | 仍待完成 |
| --- | --- | --- |
| 自助经营 | 021邮箱证明/恢复、022-A双审券、023-A分组售价与023-B周期预算，均限定本地验收 | 正式SMTP、023-C商品/订阅 |
| 应用支付 | 022-B3普通main/受限worker/HTTP/UI及本地模拟整链；在途SIGTERM/Guard失权恢复已验 | 正式PSP、main数据库COMMIT回包丢失及外部事实灾备 |
| 来源与退款 | 完整68表来源、84表/八算法来源与lot重放、109表basis/strict v2、受保护开账事务及独立停服epoch观察均已进入库级候选实现；独立观察的真实PG范围见[本轮记录](epoch-observer.md)，旧phase及各纯层保留各自证据；C1-P纯退款协议专项通过 | R1-A显式读取profile已通过限定专库验收，见[记录](runtime-read-profile.md)；R1-B非空RuntimeProof与历史/current root兼容、R2正式迁移/CLI/main及全部在线资金路径；C1原路退款、C2拒付/债务、C3交叉恢复 |
| 扩展和生产 | 024-A站内告警、024-B0-P纯计量；025-A0隔离Compose实际渲染、025-A1-M两邮件真实SIGKILL窗口已验 | 媒体/异步/长连接接线、容器/1Panel、HA、正式渠道及最后性能 |

[来源候选回归](funding-lot-candidate.md)、[纯退款协议](payment-refund-protocol-v2.md)、[计量](metering-vectors.md)、[Compose渲染](compose-render-025.json)和[邮件进程](mail-lifecycle-025.md)分别说明各自边界；纯层和QA工具不产生生产资金能力。

最新来源纯图、维护真实PG补验和商业命令并发修复分别见[三核心](source-graph-cores.md)、[C0补验](maintenance-financial-postgres.md)与[并发集成记录](commerce-concurrency.md)。本轮观察器及整仓执行以[O1记录](epoch-observer.md)和[机器结果](epoch-observer.json)为准；并发集成及以下历次PASS保留其当期身份，不转移到未完成的新接线或生产环境。

2026-11-01本轮[离线descriptor](funding-lot-descriptor.md)与[同RR账本reader](funding-ledger-reader.md)汇合后，根实际`make verify-mvp`全部通过：286项前端/strict、明确专库下全Go race、vet与嵌入build，见[整仓证据](c0-integration.json)。无生产入口、完整C0 proof或原路退款开放；本轮未升级长期预览、未执行正式供应商/支付商或性能验收。

2026-11-02新增完整静态叶子包`internal/fundinglotcatalog`：`VerifySnapshotTx`、`VerifyInstallationTx`、`VerifyRuntimeTx`只在caller-owned RR内核验，发行同事务opaque Catalog/Installation证据。ordinary正式root包含五个named/public与owner属性变体，sandbox正式root完整覆盖application/Mock/Receiver三schema与八角色；原函数正文、结构/ACL/依赖、十组跨域负权、runtime全局owner及数据库CREATE/授权提升边界已实施。两套root经独立来源审阅后固定SHA嵌入，见[collector](funding-lot-catalog-collector.md)、[peer验证](funding-lot-catalog-peers.md)与[正式root合同](../../specs/022-self-service-payments/c0-schema-check-contract.md#104-首次正式root冻结与已验环境)。最终leaf串行真实PG race已通过，217.140秒、47个顶层/179个终态（176 PASS；3个独立capture/timing/export显式opt-in跳过且另有专项证据），保留原五秒发行预算。精确清理后本轮schema/role/DB/backend归零，public owner/ACL/零关系与数据库NULL ACL保持原样，见[最终leaf验证](funding-lot-catalog.md)及[机器记录](funding-lot-catalog.json)。本轮整仓`make check`/`make verify-mvp`已由集成人执行通过；前段整仓通过记录覆盖当期导出器/reader源码。

该静态expected仅实证**PG16.15/Homebrew initdb catalog-profile、UTF8与C/C环境**，PL/pgSQL language OID 14038属于该集群初始化结果；其他PG16集群、locale/发行版与1Panel仍需独立profile验收，未知profile失败关闭。checker尚未接普通main，不发行维护Permit、资金epoch或独立全源RuntimeProof；完整022-C、后续退款/订阅/扩展/HA及最后性能阶段继续保留未完成状态。

022-C0-T0新增[维护Gate](../architecture/maintenance-gate.md)库级实现：专用物理连接、身份/endpoint非权威预检、固定双session锁、unknown立即物理关闭与独立有界清理。实现与真实PG测试由独立读审交叉核对，运行范围见[专项证据](maintenance-gate.md)。本片不提供Snapshot/Permit/epoch或main/资金接线，完整C阶段仍未完成；后续T1–T4合同缺口保留。

2026-11-02 GitHub首次交付候选的最终make check 6.767秒及make verify-mvp 339.394秒均退出0，覆盖286项前端、strict构建、明确专库全Go默认race/vet/build；T0独立真实PG/race54.205秒、53 PASS。B0旧双槽测试的lease/HTTP oracle已按合同只修测试，另补实际多连接gate竞争；首轮失败与修订后结果完整保留，见[交付验证](github-initial-delivery.md)及[机器记录](github-initial-delivery.json)。代码、README、HLD/LLD、架构与来源记录共同交付，不代表后续阶段或生产门禁完成。

015补齐Key来源访问策略，016增加动态RPM/burst/concurrency及锁内最新配置准入。2026-10-31，017进一步实现名称、模型/地域、累计额度和期限完整编辑，第三个独立CAS版本及012迁移使017时schema为001–012；三种配置与消费计数互不干扰。017专项/全量真实PG race、107项前端测试/strict构建、vet/build、隔离浏览器和58123预览升级均通过限定本地验收；状态见[017记录](key-configuration.md)。周期预算、分组商品、Key换发/恢复和自助商业继续后续规格；单实例、正式供应商、容器/灾备、许可及性能门禁仍保留。此前015/016状态分别见[来源报告](key-access-policy.md)及[运行限额报告](key-runtime-limits.md)，下文历次结果保留原验证范围。

017隔离QA完成2次SSE及1次JSON调用，各收费20微元、合计60；配置预算409、调用预算402与地域503三次拒绝前后只读快照逐字段一致。配置/来源/运行版本分别为5/2/2，390px移动布局无横向溢出；随机schema、测试角色、listener及秘密清理已核验。58123完成012迁移/provision后以受限serve运行，ready且非恢复模式，首页及28项assets匹配；app非owner/superuser且projector不可读取Key新字段。本轮未运行正式供应商或性能对比，完整证据见[017记录](key-configuration.md)。

018成本感知路由已通过限定本地验收，018时schema为001–013：逐渠道模型采购报价、最高priority内比价、未知/单价上限硬过滤及派发前报价同步；客户收费独立。122项前端测试、真实PG全Go race、strict build/vet/build、浏览器CAS与两次SSE/资金核对、运行器清理均通过，58123已升级013，ready非恢复、首页及29assets匹配。见[018记录](cost-aware-routing.md)。[统一架构](../architecture/README.md)和内容检查机制同步交付；[019采购账户](procurement-accounts.md)已通过限定本地验收，019时schema为001–014。跨渠道共享预算/逐attempt预占/独立unknown/双审更正及人工观察已实现；141项前端测试、真实PG race/故障、两轮隔离QA和当期最终包58123升级核对通过。周期、真实余额同步和外部发布门禁仍保留。

[020公开目录](public-catalog.md)已完成限定本地验收，020时schema为001–015。未登录可访问`/pricing`与匿名模型列表/详情，旧/新模型默认私有，管理员用独立revision CAS显式公开；只展示协议、产品限制和当前客户售价，不授予调用权限。156项前端测试/strict构建、最终catalog/server真实PG专项race（2.347s/16.171s）通过；全Go race记录早于最后SQL安全修订，最后变更由相关专项重测，未声称再次全量。运行器opt-in测试10.296s、隔离真实main浏览器默认私有/冲突只读核查/大整数/0与缺价/390px布局均通过；无模型调用和客户资金事实，临时schema/角色/listener已清理，见[运行器](public-catalog-020-harness.json)及[浏览器](public-catalog-020-browser.json)。

58123完成015 owner迁移/provision及受限serve升级，ready且非恢复模式，PID85387，二进制SHA-256为`5530671707b03ab90f355a073676c7405b106bc2be944e0c8cac0ac8f97127a4`；嵌入首页及35项assets与最终dist一致，权限核查表明app非owner/superuser、projector不可读公开配置，详见[预览证据](public-catalog-020-preview.json)。以上为020当期升级记录。当前[021自助身份](self-service-identity.md)已完成限定本地验收，schema001–016、186项前端/strict、最终身份/HTTP代码全Go真实PG race及vet通过，身份包223.619s、server215.029s。运行器身份fixture生命周期/失败清理修订后专项11.792s通过。隔离本地SMTP和浏览器验证邮件请求/证明GET不变更/URL清理/最小权益/登录；注册与改密提交由HTTP验证，未冒称浏览器输入新密码。旧密码/旧会话失效，proof至多消费一次，模型/客户资金事实为0，资源秘密清理。当前58123 PID4350，ready非恢复、016迁移及受限权限/40assets匹配；注册默认关、邮件未配置，真实收件另门禁。[浏览器与HTTP](self-service-identity-021-browser.json)、[清理](self-service-identity-021-harness.json)、[预览](self-service-identity-021-preview.json)。022未实现。

## 可用的业务闭环

平台配置模型协议/渠道/版本与售价 → 邀请客户、租户/成员/模型授权 → 财务制单、另一账号审批人工入账 → 客户创建受限Key → Chat/Responses/Messages文本或基础函数工具、JSON/SSE，Gemini原生纯文本JSON/SSE，或Embeddings/Jina Rerank非流检索 → 硬预占、可信最终量与唯一结算/释放 → 钱包/请求/账单/成本与退款/审计。

客户可在Playground测试实际同源端点、停止读取、查看用量与request_id；平台可查看版本与每次已准入attempt的候选筛选理由。没有租户的管理员先看到接入步骤，不会被迫绑定MFA或伪装成已有模型授权。已主动启用MFA的账号仍按自己的安全设置验证。

访客可先在公开价格目录查看已发布模型和当前售价；实际客户调用需邀请或已开放邮件证明开户、模型授权、资金及Key。目录的as_of是当页观察时刻，不锁定未来请求价格，缺价不按免费处理。

融合的是New API/One API的统一模型接入、渠道经营与客户自助控制台，以及1Panel公开展示的自托管治理/部署流程。实现独立，额外坚持租户隔离、不可变版本、持久硬预占、未知消费不重放、双人财务与可核对账本；未继承竞品全功能或复制其代码。

## 验证入口

- [003协议与联调](native-protocols.md)：真实PG/HTTP/SSE与客户端SDK的限定范围验证。
- [009检索协议](retrieval-protocols.md)：Embeddings与Jina Rerank、输入独立计费、五协议Playground与本地验收状态。
- [010 Gemini文本](gemini-native.md)：原生路径/认证、JSON/SSE、thinking/cache计量、六协议Playground与正式供应商认证边界。
- [011模型发现](model-discovery.md)：三格式目录、绑定差异/历史/stale及最小角色与审计验证。
- [012凭据池](credential-pools.md)：版本封口/完整CAS/历史/永久撤销、两阶段调度/双容量、实际成员身份与计费验证。
- [013批量启停](channel-operations.md)：无网络预检、整批应用/拒绝、并发CAS/派发屏障与幂等不可变回执。
- [014闭环收口](mvp-release-qualification.md)：隔离浏览器经营闭环/账本、首次接入修正、运行器可重复清理与真实main正常SIGTERM排空。
- [015来源策略](key-access-policy.md)：IP/CIDR规范化、配置CAS/只读核查、数据库约束与真实锁屏障；全量、浏览器及预览执行范围单列。
- [016运行限额](key-runtime-limits.md)：默认/输入/独立CAS、旧桶保留与在途计数、最新锁内准入和4096桶容量边界；最终浏览器/预览状态单列。
- [017基本配置](key-configuration.md)：历史字段保留、精确额度下调、期限续期/移除、三版本独立、最新限制锁屏障和严格API；专项/全量、隔离浏览器和预览迁移/权限/资源核对已通过限定本地验收。
- [018成本感知路由](cost-aware-routing.md)：采购价/上限/成本优先、锁内报价复核与已发收尾，含全量、隔离浏览器资金与预览升级证据。
- [019采购账户](procurement-accounts.md)：跨渠道共享累计预算、逐attempt预占、独立未知消费/恢复、双审更正与人工观察。
- [020公开目录](public-catalog.md)：匿名白名单/当前售价、管理员公开CAS、单DB时刻/C游标、Guard前资源门禁、隔离浏览器与015预览核对。
- [统一架构同步](architecture-sync.md)：系统图、源码映射、显式语义复核和Make/CI只读内容检查。
- [故障恢复](mvp-failure-recovery.md)：五个SIGKILL边界、结算后Write/Flush故障与资金不重扣。
- [计费优化](performance-optimization.md)：同一事务批处理与实际PG撤权屏障，保留资金与权限正确性。
- [独立输入/输出界](../../specs/004-conservative-bounds/spec.md)与[实例检查批次](../decisions/ADR-004-fresh-guard-batches.md)：修正硬预占依据，保持每个检查点之后的新数据库证据。
- [容量及原始数据](capacity.md)：完整商业链路100并发，首byte与首有效内容分别计时。
- [逐AC执行边界](acceptance-execution.md)：保留部分/未达标项，不用功能列表替代验收。

安装Go/Node依赖并准备专用PostgreSQL测试库后，可一次执行本地回归（只访问本地fixture，不调用付费模型）：

```sh
npm --prefix web ci
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
make verify-mvp
```

目标会在缺少测试库配置时明确失败，避免把跳过所有PG测试误认成商业闭环验证。SDK合同与容量另行显式执行，不作为默认测试中的外部网络步骤。预览和六协议curl见[本地运行](../runbooks/development.md)。

Python SDK可复现工具见[零费用SDK验证](native-sdk.md)，只读取本地fixture，不接受上游URL；首次装开发依赖需要PyPI网络，产品运行时不依赖Python。

2026-10-30最终`make verify-mvp`已实际通过：12项前端测试、strict TS/Vite构建、真实PG全Go race、`go vet ./...`及嵌入程序编译。覆盖003/004、006路由解释、fresh batch Guard、并发幂等与SIGKILL恢复；server包128.890s为测试执行时间。数字表单同时修复清空必填0值字段后`null`被误当0的问题。该通过记录不替代下列发布门禁。

最终程序已替换本机58123预览，ready=true且recovery_mode=false，主页嵌入资源与最终dist一致。浏览器检查三协议选项、可选上界留空及管理员接入指南，console无应用error/warn。可直接访问[API联调](http://localhost:58123/playground)；平台账号按接入步骤准备客户工作空间，实际推理须使用客户Key。本轮没有修改评审账号的MFA或真实客户资金。

随后005/006性能迭代完成按Key短阶段调度，保留全部财务事务与检查。最后全Go race（server92.625s）、vet与嵌入编译通过；新增真实取消、5s清理超时和原deadline后的worker恢复验证，前端无修改。三版轮换9轮各100成功，组合版首byte p95为69.80–77.76ms，同时收回005单独实施的完成回退；[完整前后对照](key-scheduling.md)公开中间代价及最终数据，50ms仍未通过。005+006最终程序随后已重启58123预览，就绪检查通过且recovery_mode=false，首页嵌入资源与最终dist一致。

007进一步保留原事务/锁与逐语句新快照，批量发送派发与账户读取；补齐解码错误后的pgx pipeline清理。专项race12.081s、全Go race（server158.600s、billing40.537s）、vet及嵌入构建通过。十二轮前后对照各100成功，007六轮额外首byte p95为61.56–69.74ms，正常五对改善4%–9%；首轮基线异常及一对完成回退3.29%均在[007记录](dispatch-read-batches.md)公开，不能宣称持续吞吐改善。最终007程序已重启58123预览，就绪、非恢复模式，嵌入首页与已验证dist一致。

## 尚未通过的发布门禁

1. 100并发完整链路50ms门槛、4vCPU/8GB目标环境和Rust LiteLLM/Bifrost正式持续对比；007本机六轮每轮额外首字节p95为61.56–69.74ms、额外首有效内容p95为61.66–69.72ms。
2. 正式供应商的输入硬边界、输出上限、最终usage/缓存/无消费拒绝与成本证据；合成fixture不是供应商认证。
3. 容器首装/升级/排空。本机尚无可用Docker引擎；隔离Compose CLI已完成真实配置渲染，容器及1Panel运行仍未验，详见[环境记录](compose-runtime-025.json)。
4. 异地灾备、网络分区与长时慢客户端演练，及项目最终发行许可证选择。014已补真实main正常业务SIGTERM排空/重启不重放，但不覆盖这些环境。

2026-10-31按用户优先级，先补[能力路线图](../design/new-api-roadmap.md)全部业务能力，再推进性能专项。009新增Embeddings/Jina Rerank，010新增Gemini原生文本，最新验收状态分别见[009记录](retrieval-protocols.md)和[010记录](gemini-native.md)；此前007性能数字仅为历史。周期预算、分组商品、在线支付、开放注册、订阅、其余批量经营/自动同步、Gemini工具/多模态、媒体/长连接和HA仍未实现，按SDD切片继续。源码未提交、推送或外部发布。

009限定范围本地验收已通过：两协议Provider/真实PG/HTTP专项、全Go race、22项前端契约（含3项协议/模型切换回归）、strict TS/Vite、vet与最终嵌入build。最终009程序已替换58123预览，ready且非恢复模式，嵌入首页与已验证dist一致；浏览器本次仅验证登录页，未登录评审账号操作新协议。正式渠道聚合计费上界与新协议性能尚未认证。

010限定范围本地验收已通过：Gemini原生文本JSON/SSE、thinking计入输出/cache为输入子集、尾usage与完整EOF、成功终态晚于最终证据和结算、未知量待核对；平台probe/发布及六协议Playground同步交付。专项、全Go race（server93.066s）、30项前端测试、strict TS/Vite、vet和最终嵌入build全部通过，详细命令见[010记录](gemini-native.md)。最终010程序已替换58123预览，ready且非恢复模式，主页字节与已验证dist一致。本轮未做浏览器账号操作或性能压测；真实供应商聚合输出硬界、完整SDK、工具/多模态认证仍单列，后续继续凭据池/模型发现。

011限定范围本地验收已通过：OpenAI/Anthropic/Gemini三种目录格式、受控GET分页、未绑定/目录存在及缺失绑定、不可变历史与动态stale；开始/结果分别同事务审计，不改客户资金/路由/价格/授权/推理健康。Provider、真实PG/HTTP与最小角色专项、全Go race（server93.695s）、39项前端测试、strict TS/Vite、vet及最终嵌入build通过。新增007迁移与owner最小角色配置已应用到本机预览，受限角色启动Verify及ready通过、嵌入首页与dist一致；浏览器仅验证登录页。详情见[011证据](model-discovery.md)，后续继续凭据池/批量运营与自助经营；本轮没有性能压测、付费调用或评审账号/MFA变更。


012新增不可变凭据池：1..32成员、完整CAS/历史、元数据保留身份、独立永久撤销、渠道/成员两阶段权重与双层容量。新池必须显式重新发布路由，旧revision不被替换；数据库同事务封口阻止提交后晚追加，attempt/probe/发现均引用完整冻结成员身份。RecordAttempt/PrepareDispatch保留撤销屏障，未知消费不换Key，同渠道不重试；平台probe/发现记录实际成员且钱包不变。50项前端测试/strict build、真实PG全Go race（billing28.550s、catalog2.766s、operations6.048s、server96.936s）、vet及最终嵌入build通过。008迁移、owner角色配置与受限serve已更新58123预览，ready且非恢复模式，首页和全部27个assets与dist一致。浏览器仅验证登录页，未修改评审账号/MFA或真实客户资金，未做性能压测。详见[012证据](credential-pools.md)，后续继续跨渠道批量运营与自助经营；这不等于New API全功能或生产门禁通过。

013增加跨渠道批量启停：当前页最多50项（API100项）、无网络预检、版本+字符串management revision校验、整批应用/拒绝、永久幂等回执与历史。启停实时影响历史发布绑定，新池仍须显式发布；不清隔离/撤销、不改路由/资金。009为全部渠道写入口维护单调revision，完整回执/唯一审计同事务且不可变。专项、66项前端测试/strict构建、全Go race（billing34.213s、catalog4.576s、operations7.391s、server104.381s）、vet及最终嵌入build通过。009迁移、owner角色配置与受限serve已更新58123，ready非恢复模式，首页与全部27个assets匹配；实际只读权限验证通过。独立复核修复回执晚回串线与after合同；未执行登录后的浏览器交互、付费调用、评审账号/MFA或真实资金修改，未做性能压测。详见[013证据](channel-operations.md)，其余渠道运营、Key扩展、自助经营及原生产门禁继续推进。

014完成MVP本地闭环收口，详见[完整证据](mvp-release-qualification.md)：隔离浏览器实际配置渠道/双成员池/模型路由/价格/客户授权，双人模拟核款、客户受限Key、非流与SSE、账单、双人退款、目录发现及批停启/预检/applied回执/刷新后历史与ID查询。3次成功各20微元，退款20，最终可用1,234,527/held0，投影与账本一致；停用失败没有上游或资金事实。修复渠道默认地域及可信反代登录限流，新增真实main正常SIGTERM在途SSE/唯一结算/重启不重放专项。前端66项/strict构建、真实PG全Go race（server127.173s）、vet与最终嵌入build通过；QA最终源码两轮真实main及失败清理专项race10.632s通过，旧业务实例与专项资源均清理。58123已按owner迁移/配置权限、受限serve更新，ready非恢复模式、首页与27assets逐字节匹配。本轮没有性能压测、正式供应商/容器/网络分区或异地灾备认证，也未修改评审账号/MFA/真实资金；不将014本地通过等同001全部发布门禁通过。

022-B3应用支付闭环已接入普通main、独立受限worker、订单/通知HTTP和支付页面，双manifest/双nonce/022运行证明默认不安装；完整payments真实PG race与config/HTTP专项通过，两轮真实主进程/最终浏览器整链已通过限定本地验收，真实Guard失权和在途SIGTERM独立补验已通过，见[当前分层记录](payment-application.md)。长期58123仍为020已验包，此项不等于已升级预览、正式收款或整体后续计划完成；来源lot/退款、订阅、其余扩展、HA与最后性能仍继续。

2026-11-02本轮静态checker收口后的整仓`make check`7.546秒、`make verify-mvp`340.981秒均退出0：286项前端、strict TS/Vite、明确专库全Go race/vet及嵌入build通过。C0专用opt-in的真实PG完整leaf race单独217.140秒通过，整仓命令不重开capture/export/诊断或真实进程运行器；精确范围、两次旧失败、源码hash与清理见[最终证据](funding-lot-catalog.md)及[机器记录](funding-lot-catalog.json)。源码未因此接入main、未升级58123、未作生产或正式供应商验收。


2026-11-02新增 [V0-WS钱包序列纯检查](wallet-sequence-rows.md)：七组supplied原行的全序、genesis/head、journal/event声明、全局唯一与legacy互斥。它不是完整lot/FIFO重放，没有数据库/维护/资金装配；原路退款、epoch和生产验收继续后续。README同步保持当前使用、部署和能力边界。

V0-WS最终集成：`make check` 7.623秒、明确专库 `make verify-mvp` 977.773秒通过（286项前端、43个Go测试包race、vet/build）。本片纯包race与73385次短fuzz独立通过；默认整仓未启用C0 opt-in。原实现与验证边界见[记录](wallet-sequence-rows.md)，不等于完整来源lot或生产验收完成。


2026-11-02新增[V0-WL原行lot纯重放](wallet-lots-rows.md)：25组原始行独立核旧开账、FIFO预占、正序结算、逆原收费分次退款、事件/分配/cache与原始父关系。它不接数据库reader、维护授权或在线资金；同session源+lot、pre/basis/init/post/epoch仍按后续计划推进。最终本片验证以该记录为准，不沿用旧WS通过结果，也不升级58123或生产状态。

V0-WL最终集成：纯包race 9.027秒、32508次短fuzz、`make check` 9.702秒，以及明确专库`make verify-mvp` 1018.811秒全部通过（286项前端、43个Go包race、vet/build）。最终Go源码与测试摘要全程一致，默认整仓未启用C0 opt-in；详见[本片记录](wallet-lots-rows.md)。

84表来源与钱包读取最终集成：相关四包明确MONI专库race 39.531秒、115顶层/960终态通过（69独立opt-in跳过）；另行真实维护Plan/Init race 93.656秒、零跳过。make check 11.714秒与明确TEST专库make verify-mvp 1034.525秒通过（286前端、43 Go包race/vet/build）。原68范围不升级，postphase、basis/epoch和资金writer仍后续；详见[本片记录](source-wallet-reader.md)。


2026-11-03新增[109表basis与strict v2叶子](basis-proof-v2.md)：固定原二进制基线摘要、metadata前置与严格owned canonical；无维护phase/epoch/资金或运行授权装配。最终相关四包race39.657秒、113顶层/1528终态通过，保留69历史opt-in SKIP；其中新basis真实专库18子向量/19终态零跳过。proof fuzz41578次、basis envelope fuzz186770次通过。make check 6.888秒及明确TEST专库make verify-mvp 1003.198秒通过（286前端、45 Go包race/vet/build），unit/全仓源码身份一致。protected pre+basis/init/post/epoch与全资金接线仍后续。


2026-11-03新增[最小旧数据投影与纯初始化规划](init-planner.md)：原68表完成一致Facts深复制九组业务原件，独立big.Int/ledger/来源核对后生成准确legacy/opening/seq0与正额lot写集，无数据库stamp、epoch/proof或写权。三tenant47行手算golden/G49及确定UUID、资源/取消/深复制已验；明确MONI专库四包race33.983秒、223顶层/1432终态零跳过，实际普通/沙箱Read→Project→Build成功且原资金不变。make check 8.057秒与明确TEST专库make verify-mvp 1007.389秒通过（286前端、46 Go包race/vet/build），源码SHA一致；23张图已渲染视检。protected phase/writer、strict v2/84post提交、非空epoch observer/runtime和全资金路径仍后续。

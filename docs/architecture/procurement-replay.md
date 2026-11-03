# 采购历史独立重放

本页对应 [B-P4 读取合同](../../specs/023-periodic-products/b-procurement-replay-read-contract.md)与[本轮实施合同](../../specs/023-periodic-products/b-procurement-replay-implementation-contract.md)。它展示开发中的纯核，正式运行入口与剩余门禁见[统一架构](README.md)、[首轮叶子验证](../verification/procurement-replay-leaves.md)、[成本与结算续片](../verification/procurement-cost-finish-replay.md)、[当前指针与时钟续片](../verification/procurement-current-source-clocks.md)、[人工顺序续片](../verification/procurement-reconciliation-order.md)、[来源值与连续性](../verification/procurement-source-values-continuity.md)、[来源内容与真实效果](../verification/procurement-source-relations.md)和[PB关系及账户故障](../verification/procurement-pb-relations.md)。后续逐对象依赖与观察缺口见[收口计划](../../specs/023-periodic-products/b-procurement-replay-closure-plan.md)。

## 模块与数据流

```mermaid
flowchart TD
    Raw[本次完整原始行\n40 原件表 + 32 前缀表] --> Budget[共享预检\n100000 行 / 16 MiB / 原绝对 deadline]
    Budget --> Source[来源闭合\n标量、Usage、NULL 联合、68 generated\n根语言、companion、四条链及当前指针\n人工P→A→R、finish行内联合]
    Source --> OriginalLocal[40类原件本地值\n金额、NULL、桶与原标识接受集]
    Source --> PayloadLocal[采购命令 / audit / outbox\n严格JSON与原始ABI]
    Source --> PBJSON[PB操作两列严格JSON\n原生bool、UTC6日历、八段指纹]
    Source --> PayloadRelations[命令快照与audit/outbox真实内容\n归属、cause→mutation→link、双向集合]
    Source --> Effects[真实账本 / 客户与供应商PB效果\n原件与声明双向精确集合]
    Source --> Corrections[成本更正原件\n两类cause / 全局operation / scope与proposal桥]
    Source --> PBConfig[PB配置与观察\n版本 / changed-noop / audit / origin-head-journal]
    Source --> PBEvents[PB事件独立重放\n真实source镜像 / allocation-window-control三层桶]
    Source -. 后续独立版本草案 .-> HO[历史观察B-HO1\nrequest序 + PB scope序 / 六表未实现]
    Source --> BusinessLocal[补充来源行内联合\nquote / gate / revision / effect算术]
    Source --> Continuity[跨链状态连续\nattempt node / account p动作 / member cut]
    Budget --> Request[请求重放\n原始输入摘要、meter 归属与完整 cut\nsticky/latest、conflict、身份绑定]
    Budget --> Account[账户重放\n命令、journal、预占与派发 gate\n证据成员、采购状态、审批与恢复]
    Budget --> Project[无损投影\n旧 23 数组 / 173 字段]
    Project --> Required[Required 自然对象集合\n11 类命题，独立兼容枚举]
    Budget --> Cost[成本历史子域\nquote、delivery、normal/reset/mutation\n更正、恢复、outbox、当前成本]
    Budget --> Finish[Reconcile / finish 子域\n三 action、派生decision前驱桥\n独立结算时钟、账本与两 scope PB 效果]
    Project -. 后续实际调用 .-> Base[原 CheckProcurementEvidenceGraph\n保留完整 Base]
    Source -. 完整后 Qualified .-> Aggregate[最终逐对象聚合\nRequired / Proven / Unproven]
    Request -. 局部 Candidates .-> Aggregate
    Account -. 局部 Candidates .-> Aggregate
    Cost -. 局部 Candidates .-> Aggregate
    Finish -. 历史 cut 完整后才能聚合 .-> Aggregate
    Required -. 对象集合 .-> Aggregate
    Base -. 原值与逐项计数核对 .-> Aggregate
```

实线是本轮已有私有执行路径，虚线表示尚未接通的聚合路径或标注的独立版本设计草案。当前没有导出的 `Check`，不发行新 scope 的一致或覆盖结论。普通服务、数据库迁移、SQL writer、固定读取 profile 与维护权限均没有接入此包。

## 职责与源码

| 部分 | 实际职责 | 源码入口 |
| --- | --- | --- |
| 资源与对象身份 | 全 72 数组先预检；工作循环共享取消与原绝对 deadline；复合键使用逐分量字节长度，保留空串及 UTF8 身份 | [scalar.go](../../internal/procurementprefix/scalar.go)、[replay_support.go](../../internal/procurementprefix/replay_support.go) |
| 来源闭合 | 核物理标量和 NULL envelope、真实 PK 重复、generated、canonical/root、companion 与 request/attempt/account/member 链；未完整时 `Complete=false`、`Qualified` 为空 | [source_closure.go](../../internal/procurementprefix/source_closure.go) |
| 来源续片 | 两列 route_decisions 严格五字段 JSON，64 位 priority/uint64 weight、selected、1000 项与 Go 业务字节界；原18表追踪联合及 request26/attempt15 冻结列双向核验 | [路由](../../internal/procurementprefix/source_closure_routes.go)、[原件联合](../../internal/procurementprefix/source_closure_original_unions.go)、[出生快照](../../internal/procurementprefix/source_closure_birth_snapshots.go) |
| 当前指针与原件 Usage | request latest/last decision、attempt最后mutation/delivery及当前值、永久dispatch六列/真实PB时点与managed journal、p出生及自身最后journal双向核验；三处六字段Usage严格JSON | [当前指针](../../internal/procurementprefix/source_closure_current_pointers.go)、[Usage](../../internal/procurementprefix/source_closure_usage_payloads.go) |
| 人工顺序与行内联合 | P→A→R固定偏序、唯一action、proposal/actor消费、真实pending承接与case六列last；行内NULL/角色/result/金额/profile与mode/reason/deadline独立校验 | [人工偏序](../../internal/procurementprefix/source_closure_reconciliation_order.go)、[行内联合](../../internal/procurementprefix/source_closure_finish_unions.go) |
| 原件本地值 | 40类安全投影逐表核金额/枚举/NULL/余额桶/PB本地组；旧TEXT身份不新增UUID约束，原procurement nil UUID与PB非零UUID分别保留 | [原件值](../../internal/procurementprefix/source_closure_original_values.go) |
| 命令与消息payload | 四kind命令快照、固定采购audit和cost/本次Reconcile outbox严格对象与原金额ABI；其他普通共享族保持原范围 | [payload](../../internal/procurementprefix/source_closure_payloads.go) |
| PB操作JSON | safe_input四字段、result十二字段/窗口五字段；原生bool、UTC6自然日/月、changed/no-op候选、路径/时间/八段长度framing指纹行内绑定；未认证真实配置与absence | [PB payload](../../internal/procurementprefix/source_closure_pb_payloads.go) |
| 命令与消息内容 | 四类command快照、typed audit及outbox内容/真实归属；cause→mutation→link、正反向与已知业务槽集合，保留legacy动态历史缺证 | [内容关系](../../internal/procurementprefix/source_closure_payload_relations.go) |
| 来源效果关系 | 新settlement/journal、HELD/AVAILABLE及正charge时REVENUE的完整line集合；客户tenant/Key PB和supplier PB真实event/allocation/origin/window及delta；不以当前配置证明absence | [效果关系](../../internal/procurementprefix/source_closure_effect_relations.go) |
| 更正原件关系 | approval/unmanaged两cause联合核全局operation、物理ID与真实内容，attempt/request及proposal/birth桥；已知槽旁全NULL原件不能降legacy | [更正关系](../../internal/procurementprefix/source_closure_correction_relations.go) |
| PB配置与观察 | 三scope原件、完整配置版本、真实changed/no-op与审计，origin/head/journal及command/window_birth双向槽、完整ordinal/cut；命令动态金额前缀仍后续 | [操作关系](../../internal/procurementprefix/source_closure_pb_operation_relations.go) |
| PB事件与当前投影 | 客户reserve/settlement及供应商PE真实镜像，new/import/catchup、三层独立金额重放/seq/revision/tail和当前投影；legacy残余仅核数额相容 | [事件关系](../../internal/procurementprefix/source_closure_pb_event_relations.go) |
| 补充业务联合 | 选中部分未知quote、managed完整价、Usage/digest、成本分支、账户gate/桶/revision、八PE目标与合并ceil、signed收入余额行内算术 | [业务联合](../../internal/procurementprefix/source_closure_business_unions.go) |
| 来源连续性 | 按attempt node推进delivery/cost/p，dispatch经真实journal推进p；account筛同attempt动作同序，观察cut区间、member出生/复用与历史前缀相等 | [连续性](../../internal/procurementprefix/source_closure_continuity.go) |
| 请求 | 独立复算原 Go JSON/SHA ABI；保留 meter 首次出生归属，按 ordinal 核完整竞争集合和 witness；重放跨 attempt 或无 attempt 的 sticky/latest 与 conflict | [request_replay.go](../../internal/procurementprefix/request_replay.go)、[输入 ABI](../../internal/procurementprefix/request_replay_input.go) |
| 账户 | 从 create 开始重放全账户 journal、配置与 reservation revision、H/S/C、reserve/dispatch gate；按真实 cut 核 evidence/member、effect/skip、proposal/approval 与恢复 | [account_replay.go](../../internal/procurementprefix/account_replay.go)、[证据](../../internal/procurementprefix/account_replay_evidence.go)、[屏障](../../internal/procurementprefix/account_replay_barriers.go) |
| 成本历史 | 从 prepared、unknown/NULL 按唯一 attempt node 顺序核冻结报价、交付、正常资格、reset/mutation、绝对更正、恢复和自身 canonical outbox；历史目录可见全集不在本证明范围 | [attempt_cost.go](../../internal/procurementprefix/attempt_cost.go)、[事件](../../internal/procurementprefix/attempt_cost_events.go)、[原件](../../internal/procurementprefix/attempt_cost_objects.go) |
| 人工对账与客户结算 | 核真正消费输入及角色、无 attempt 派生 incoming及唯一normal decision真实前驱、三时钟用途、new/existing/pending、金额、Key 局部桶、账本和 PB 双向效果；历史 cut 未齐，`Complete=false` | [finish_replay.go](../../internal/procurementprefix/finish_replay.go)、[账本与 PB](../../internal/procurementprefix/finish_replay_effects.go)、[严格 JSON](../../internal/procurementprefix/finish_replay_json.go) |
| Required | 从本次无损原件投影构独立索引，沿原算法实际条件记录自然对象；不读取调用方 Report 或接受调用方计数，不证明对象 | [required.go](../../internal/procurementprefix/required.go)、[严格解析](../../internal/procurementprefix/required_parse.go) |

`Complete` 只说明本域算法实施并执行的范围，不代表输入无矛盾。业务域 `Candidates` 只表示本域的局部候选，必须再与完整来源及其他依赖逐对象结合，才可能计入 `Proven`。一个域的通过不能抵消另一个域的错误，也不能拼接其他调用的历史。

账户全局 ordinal 与各 attempt node 必须描述一致的顺序。逐 attempt 的账户动作 node 严格递增；证据/提案/批准/恢复的观察 cut 位于其前后动作之间，并按该 attempt 的 source node 前进，不允许同额更正或无 effect 行掩盖未来/回退 cut。不同 attempt 与账户配置仍可合法交错。

Required 的十一类对象依次是 finalization、request、conflict、两个 attempt/digest 身份集合、三个 attempt 集合、proposal、两个 account 集合。实际条件保持原算法的合法标量、主键去重、complex 事件数量、不同值更正、tenant conflict、配置与审批屏障分支。测试用手算精确键集合及同次原 checker 的逐项 Unproven 长度做双重核对；旧 mismatch 和全部 Base 字段不改写。

上游成本和客户收费分别重放。成本使用 attempt 冻结的采购单价，客户使用 request 冻结的销售单价，均在大整数求和后单次 ceil。审批时钟只决定能否产生带原真实 delivery 的 finalization；结算决定时钟和资金锁后的最终时钟各自判定截止，墙钟允许回退，执行顺序不由时间比较决定。已选择的deadline释放不能因后续时间回退恢复收费；最终过期同时清零 raw/charged/waived，不产生 REVENUE 或 profile-block 效果。零价收费仍可 settled，HELD/AVAILABLE 的真实零 delta 行不能省略。

当前原件的last与immutable出生快照分别核验。request末decision可以是conflict而latest仍为旧normal；attempt最新delivery可以是收尾而永久permit仍是初次dispatch。p.last按账户全序筛选本attempt，允许其他配置和attempt穿插；新journal不能借全NULL追踪列伪装旧p，也不能指向另一账户的同ID attempt。真正全NULL且无新来源的旧行保留缺证，不倒造历史。

来源续片独立核原件值、采购payload、补充业务联合及跨链before/after；不调用writer或其他域的Stats来决定通过。unmanaged可冻结选中但部分未知的报价，managed仍须完整known价；cost_recorded金额可达NUMERIC100，人工Money仍为int64，REVENUE余额保留signed。坏序非空历史不能降成空链，跨表同ID分开计数，执行错误返回零结果。后续三个来源叶子已补PB两列严格JSON、采购命令与消息内容、以及真实ledger/客户与供应商PB正反向效果集合；同业务身份的额外全NULL原件不能降成旧件。本轮另补完整配置/观察及事件三层重放，见下文；历史命令动态快照、absence、实际业务资格与逐对象Qualified仍未收口。

finish 只核本次历史局部 before/after 与真实输出，不把今天 Key/ledger 余额、已退款金额或旧 profile-block.request 倒填到过去。租户/Key PB 使用原 admission window、真实 reserve/import event 和本次 settlement event，客户不允许 legacy catchup。人工source按P→A→R固定语言核proposal/actor与case最后人工pointer，不对自动reason/完整客户状态历史发明总序；新A已结算不能再新R，A后新R必须由准确pending outcome承接。派生incoming唯一normal decision精确绑定source.prior_latest，同内容不能换ID；meter与完整request序仍由RequestIncoming负责。旧缺P/A或全NULL last的case保留私有LegacyCases，不作完整uncovered集合或资格。finish现保留两项缺口：未派生decision的历史latest/sent观察，以及历史control absence和命令动态快照前缀；本轮原配置/窗口关系与事件重放不替代这两项观察。两个成本私有子域不等于完整 AttemptCost；来源和最终 aggregate 也继续未完成。

## 权限、失败与当前边界

纯核不连接数据库，不读取供应商密钥，不发模型请求，不修改钱包、采购余额或历史。执行失败返回错误与零结果；数据矛盾保留固定分类计数，不能包装成执行错误后忽略，也不能因缺证而隐藏确定矛盾。

完整来源闭合、AttemptCost 的剩余历史 cut、完整 Base 调用及 aggregate 仍是交付前置；当前局部算法不解除旧十一项历史 Unproven。最终还需要完整固定 scanner/readset、V4 profile、basis/catalog、protected pre/post、actual APP 全路径与独立验收；正式迁移/启动和在线资金接线继续由 R2 合同控制。已完成与未完成算法的逐项范围以[成本与结算验证](../verification/procurement-cost-finish-replay.md)为准。

历史观察另见[B-HO1独立版本设计草案](../../specs/023-periodic-products/b-procurement-historical-observation-design.md)。六表候选未实施、未安装、未验收；现有正常decision前驱与真实PB event桥继续复用，未派生decision/latest/sent与scope absence/首次配置完整导入须由全writer同步和新版本材料补证，不能在现715列上回填。

## 本轮关系与真实提交故障边界

[最新关系合同与验收](../verification/procurement-pb-relations.md)继续扩充同一私有SourceClosure，不新增线上依赖。真实配置版本与观察链、事件/窗口及当前桶已分别独立核验；命令快照的历史carry/window held/spent及revision/latest-tail仍缺完整前缀，历史scope absence、完整首配成员和B-HO1不因此自动成立。legacy_import/catchup残余守恒不认证哪些PE处于历史cut之前。两个typed correction cause的原件内容通过也不认证批准资格、历史角色或当时可更正状态。

账户候选新增actual APP的203项逐一有效撤权与4个真实锁等待/取消场景；九表全值快照和原命令/source缺席证明事务完整回滚，fixture独立清理。它只增加测试与证据，不修改固定SQL、ACL、迁移或线上入口；W全部DML/未知COMMIT/其他根仍后续。来源Complete=false、Qualified为空，无公共Check，完整Base/aggregate与R2边界保持。

# B-P4 D1-A-DDL：账户域声明约束合同

2026-11-03。承接[统一布局](b-procurement-schema-contract.md)、[账户](b-procurement-account-contract.md)、[成本/request](b-procurement-cost-request-contract.md)、[共同来源](b-procurement-source-contract.md)、[执行](b-procurement-execution-contract.md)及[完整ACL](b-procurement-full-acl-contract.md)。本页先冻结下述声明约束，再由[account-constraints-v1.sql](../../internal/procurementprefixschema/materials/account-constraints-v1.sql)实现；它是候选ALTER材料，**未执行PG安装，不包含业务writer、guard、validator或授权，不关闭D1/I1/R2**。

## 1. 所有权、安装与键

账户文件只处理account_origins/heads/journal/command_sources、procurement_transitions、proposal_births、approval_consumptions、evidence_choices、recovery_sources九表的领域关系，以及这些来源与原件的精确关联。32表715列/68 generated/647 ordinary（含delivery两列、reset一列封闭cause discriminator及finish.d_new_settlement）及原表nullable扩列由集成材料创建，不在此新增或改名；原001–022保持原样。所有关系以`{{MONI_SCHEMA}}`标识符占位限定，执行人必须安全标识符替换，业务调用方不提供schema。

材料分`keys`、`checks`、`foreign-keys`三phase，由集成人先合并全部keys，再checks，再FK；单文件不可脱离共同材料任意安装。所有本文件约束名以`pp_ad_`开头，长度≤63。PK/UNIQUE均非deferred，FK均DEFERRABLE INITIALLY DEFERRED、默认NO ACTION；普通唯一身份冲突不推迟到COMMIT。所有CHECK的可选分支显式核NULL组，不以SQL UNKNOWN作为满足条件。

共同文件提供18 canonical PK、`(pk,root_id,step_no,step_kind,created_xid)`、`(pk,root_id,created_xid)`以及完整R/A身份唯一键及root/step双向FK；本文件不重复。自然AttemptOrigin身份键去重为`(attempt_id,root_id,created_xid,tenant_id,request_id)`。本文件拥有account origin/head/journal的PK与领域额外键：

| 对象 | 精确领域键 |
| --- | --- |
| account origin | PK(account_id)，UNIQUE(create_operation_id)、UNIQUE(command_source_id)、UNIQUE(journal_id)，及原件/创建输出所引用的完整五元组 |
| account head | PK(account_id)，完整当前配置/桶投影目标键 |
| account journal | PK(id)，UNIQUE(account_id,ordinal)、UNIQUE(account_id,ordinal,id)、UNIQUE(account_id,ordinal,id,created_xid)、UNIQUE(root_id,step_no)、UNIQUE(source_kind,source_id)；另有原p origin/current、命令、PE、dispatch的精确目标元组 |
| command source | operation_id、audit_id各UNIQUE；原command全部输入/回执/时间/来源及可选journal的目标元组 |
| transition | event_id、journal_id各UNIQUE；PE全字段输出元组、cause与四类来源future输出的精确目标元组 |
| proposal/approval | proposal出生proposal_id/audit_id各UNIQUE；批准proposal_id/approval_operation_id/provider_correction_id/cost_mutation_id/outbox_id/audit_id各UNIQUE；完整屏障、角色身份与输出元组 |
| evidence/recovery | 各自非NULL的transition/event/journal输出ID各UNIQUE；recovery另有delivery_mutation_id唯一，evidence的skip输出全NULL |

原表新增复合UNIQUE由集成人统一提供，不在本文件重复创建。准确列集与本文件FK目标相同：账户出生/当前配置桶；command operation/actor/kind/target/digest/status/reason/created_at/来源；PE id/account/attempt/key/type/deltas/evidence/来源/journal；proposal出生及批准来源；correction身份与来源；audit id及canonical来源；identity(attempt,digest,event)；upstream dispatch完整许可/账户/journal/checked_at。原p.upper_amount为NUMERIC、新来源upper_amount为BIGINT，不用异类型FK假证相等，由真实validator独立比较整数值。

## 2. 行内决定与NULL联合

账户origin固定version=1，创建source/command/journal与真实原account出生复合绑定。head ordinal>0、configuration revision>0、非负H/S/blocking，绑定同账户journal末项的xid及完整after；head→真实账户当前配置/桶由精确FK绑定。旧账户没有head，不能用无条件原account.id→head FK强制全部旧账户补出生；tracked原account必须有唯一head的反向集合及blocking由真正validator核。

journal明确ordinal1=create且previous=NULL，其他前驱非NULL且引用同账户ordinal-1。create的before配置/桶全NULL，after零桶/revision1；其余before完整。command/budget_event/dispatch决定唯一canonical指针与step_kind。配置类的全部p/quote/gate/pause列NULL；p类完整，reserve的p.before全NULL，其他完整。gate仅reserve/dispatch，extra=U/0；dispatch账户前后完全相等且p prepared→sending。所有金额/计数有界非负；账户配置revision只在配置四元组改变时+1，p revision只在状态/桶/anomaly四元组改变时+1，使用NUMERIC比较避免MaxInt64算术溢出。

command source固定admin/active、两种kind及applied/rejected/reason联合。create input_target=''、expected=NULL、有journal；configure input_target=target且expected>0，not_found无journal，revision_conflict有journal。label/provider/state/limit采用冻结合同；原Go Unicode文本完整接受集及摘要由真正codec/validator验证。command_created_at是独立DB采样并由新BEFORE guard派生原command.created_at；不以诊断created_at代替，材料无此guard假实现。

transition四cause严格限定八种PE、event_key拼接、evidence与状态目标。reserved before全NULL且after prepared/rev1/U/0/false；其他before完整。delta精确等after-before，revision按真实四元组变化。reserved/release/unknown/confirmed/disputed/recovery/corrected各有行内可判的状态、桶与anomaly联合。费用计算、token超界和approval金额只能由相应真实来源与validator核。

proposal出生固定finance_operator/active、非inflight p屏障与member count/tail；approval固定maker/operator、checker/approver、均active且不同人，消费同proposal出生的完整p/member prefix；tail=NULL恰count=0。新批准输出始终完整，after=settled/H0，其金额/quote/anomaly/实际PE/更正由来源精确闭合。用户ID存在可FK，但历史role/status不得永久FK到今天users的可变角色。

evidence固定new_member/existing_member及九decision。existing_member立即skip、effective/cause NULL且member cut不变；new_member count+1、完整effective/cause。所有skip恰对应固定skip_reason且无三个效果输出；effect三输出完整、skip_reason NULL。duplicate-dispute才有existing_dispute_event_id。member两端tail和身份均有自然键关联；选择的实际新增member root/step/xid与分支对应由validator核，不能给existing分支强加“identity member等当前tail”。

recovery仅prepared/sending、checked_at≥request_deadline；无permit且p/upstream prepared才expired_without_permit，其他eligible组合为recovered_unknown。它绑定真实delivery/transition/PE/journal；无动作没有来源。不会派发模型。

## 3. 精确关联与历史边界

来源到原件和原件到来源均以真实身份/root/step/kind/xid关联。audit通过预分配id绑定本canonical来源，固定action/role/actor/request/payload仍由真实guard/validator检查。source→output严格使用完整A身份；account journal没有tenant/request列时仍按同step公共五元组及account/attempt输出元组闭合，不凭一个id或同xid替代。

managed AttemptOrigin绑定真实account/binding、reserved transition/event/journal、admission；unmanaged为空的联合由共同文件负责。p的origin与当前last只从原p指向对应journal；不将历史journal.after永久FK到未来p.current。proposal/approval/evidence/recovery观察的account cut指历史journal(account,ordinal,id)，不要求永远等今天head。新成员、后续配置、更正、退款不能使旧合法历史失效。

固定布局的ApprovalConsumption不保存account_journal_id，以完整A身份/root/xid/cause及event_id绑定transition，再由transition的必填journal_id绑定真实journal；不虚构本地字段。RecoverySource不保存account_id，其before快照以transition id、attempt_id和完整p四元组绑定，账户由已精确绑定的transition与AttemptOrigin冻结quote闭合。相应transition额外唯一键仅为这两个精确外键提供目标，不宣称新增跨行validator。

## 4. 声明DDL不能完成的义务

下列义务必须由完整、不可绕过的实际guard/只读validator、独立重放及actual LOGIN验收承担，本文件没有stub替代：

- 全链无分叉/跳号/漏尾及root模板、cause严格更早、scope与所有canonical node适用集。
- head真实末项、完整账户桶=所有p=所有PE累计、blocking集合与账户gate观察时点；ordinal/check/FK只证明局部关联。
- 初生实际top-xid、DB clock、真实session_user/APP OID、锁后before、当前授权与maker/checker重试优先级。
- 原p四元组/quote/input/output/U、managed admission时刻、首次DB选价和post-wait重核、PB全部原窗效果及absence。
- source分类条件、EvidenceChoice所有优先级、semantic hash与完整成员prefix、skip/noop及零delta输出完整性。
- 原command JSON摘要和before/after快照、created_at新guard派生；原proposal/correction金额/basis/evidence及业务actor/audit/outbox逐值匹配。
- 新原件不得伪装legacy、不可变字段及UPDATE/DELETE/TRUNCATE保护、列ACL/负attnum sidecar/默认ACL与角色可达闭包。
- COMMIT/取消/资源/未知结果、幂等回执、原业务回归与所有跨锁期限/报价测试。

本材料仅提供声明约束；没有来源写根与上述守卫时，即使DDL能安装也不能启用候选或发行资金权限。结构静态核验与PG安装、商业链路、生产供应商/部署分别报告，不能互相替代。

集成后真实PG/race的整组结构与命名声明边结果另见[关系材料验证](../../docs/verification/procurement-relation-materials.md)。上文“未运行PG”只描述领域agent静态交付时点，不覆盖后续集成人的记录；结构通过不等于本页列出的业务guard/writer/actual APP验收。

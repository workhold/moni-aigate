# 012：不可变渠道凭据池

2026-10-31，沿[能力路线图](../../docs/design/new-api-roadmap.md)NA-06/A3继续，状态：限定范围本地验收通过，见[直接证据](../../docs/verification/credential-pools.md)。功能优先，002/008性能专项后移。

## 用户出口

管理员为一个渠道配置1..32个供应商凭据，分别设置名称、成员权重与并发上限。完整池随不可变渠道版本发布，客户调用在原渠道优先级/权重选择之后选择可用成员。平台可查看当前及历史池、实际attempt/探测/发现使用的凭据身份，并永久撤销单个凭据。客户售价、授权、预占、最终证据和结算合同保持原样。

## 版本、管理与安全

1. 新008迁移增加`channel_version_credentials`，包含channel_version_id/channel_id/credential_id、position1..32、label≤64、weight1..1000000、max_inflight1..1000。成员身份/位置唯一、版本及凭据的channel复合外键、不可变触发器与应用角色只追加。历史version按原primary凭据回填一个成员（weight=1、max_inflight=1000）；`channel_versions.credential_id`保留为primary兼容字段，以延迟复合外键保证primary必须是该版本成员。版本INSERT触发器无条件写数据库事务stamp `credential_pool_creation_xid`；成员INSERT要求stamp等于当前顶层事务，并证明版本tuple属于其创建事务：普通/active子事务检查当前backend的xmin事务ExclusiveLock；released savepoint则结合当前stamp、MVCC可见tuple与完整子xid仍in-progress，支持wrap并拒绝committed/future/无效xid，阻断恢复stamp偶合。版本及完整成员必须同事务创建，提交后连owner普通SQL也不能晚追加；成员UPDATE/DELETE同样拒绝。attempt、probe与目录发现都以version/channel/credential复合外键引用池成员。既有迁移checksum与历史记录不改写。
2. `GET /api/platform/channels/:id/credential-pool[?version_id=...]`仅admin可读，返回当前/历史版本、current_version、stale及成员ID/position/label/weight/并发/primary/revoked_at，不返回secret/密文/秘密前缀。history version须属于该channel。
3. `PUT .../credential-pool`仅接收expected_version与完整members数组；每项是existing credential_id或new secret，两者互斥，并有label/weight/max_inflight。第一项为primary，不能空池或超过32。渠道UPDATE锁内比较expected_version；现有成员按ID稳定锁序检查同channel、未撤销，新秘密用原AAD加密保存；重复ID/重复秘密、历史同secret另起ID或已撤销secret重录均拒绝。秘密≤4096字节、无空白/控制字符；名称为有界纯文本。pool/version/审计同事务，失败无残留。旧PUT重放409，不覆盖新配置。
4. PUT创建新channel_version并复制base/provider/region/CIDRs，返回新池；原完整route+expected_revision发布后才用于新准入。移出池只影响该新版本，已经发布的旧版本和在途快照保持原成员。不能将“移出池”描述为紧急撤销。
5. `DELETE /api/platform/channels/:id/credentials/:credentialID`永久撤销同channel的该身份，覆盖全部历史版本，幂等且只记录一次成功撤销审计，不提供恢复。沿原channel→credential锁序和派发屏障；已发上游无法撤回。相同供应商秘密若过去跨channel录入，撤销一个身份不保证供应商全局Key失效。
6. 沿原session、CSRF、可选MFA、管理员、近期再认证、Guard/恢复门禁。普通channel元数据/状态更新保留整个池与credential ID，不克隆秘密绕过撤销；单secret更新只允许单成员池，多成员走专用完整池接口。新secret不能以新ID复活历史撤销秘密。主密钥重包继续原ID/AAD，不改变池身份。

## 调度与资金

7. Catalog按固定route/channel_version展开pool成员，撤销/停用/隔离成员不解密，不因primary撤销而禁用仍有其他可用成员的整池；发布锁定版本并按ID稳定锁成员，至少有一个live成员。新请求授权与资金预占仍锁定原模型/售价/revision；池不改变报价。
8. Routing两阶段选择：过滤静态状态、region、成员健康和成员容量，渠道继续原总MaxInflight；先按渠道最高优先级/原Weight选一个渠道，再按成员CredentialWeight选择凭据。池大小不能倍增渠道权重。凭据并发以持久credential ID为key跨模型/版本共享；渠道总容量与凭据容量都在同锁内取得、真实上游退出后归还。健康仍按credential+model，半开/取消/panic/旧generation保护不放宽。
9. 同channel的候选必须有不同非空credential ID，且version/model/region/priority/channel weight/channel cap一致；≤1000渠道/每池32，权重不得溢出。默认单成员参数兼容既有routing fixture。route_decisions继续每channel一条、原1000条/256KiB上限；实际选中credential_id已随attempt独立持久化并仅平台显示，不扩成32倍诊断数组。本轮不提供完整逐成员失败池解释。
10. 每请求最多两次生成尝试，tried仍按channel；不会在同渠道失败后继续换Key耗尽整池。无消费证明才允许下一渠道，消费未知/已提交输出不重放。池容量、熔断或健康不授予额外重试权。
11. RecordAttempt和PrepareDispatch都验证attempt的冻结version成员身份，原有按序读取批次及失败排空规则不变；派发在原channel/实际credential锁后读取当前撤销状态，不能用当前新池替换旧attempt的凭据。数据库复合成员外键另行阻止未匹配身份持久化。撤权、独立硬预占、同步最终证据/唯一收费、未知量pending、到期释放及恢复保持原合同；不让纯内存池余额或轮询取代资金事务。

## 诊断与控制台

12. 平台probe从一个完整已发布绑定的池中按同一Routing机制选择一个成员，持有同一渠道/成员容量并记录实际credential_id；只代表该成员本次协议/用量成功，不代表全池认证。成本仍独立，不动客户钱包。发现仅GET目录，按position选择第一个未撤销成员，记录实际credential_id；011结果改为membership外键，原stale比较实际身份与版本，不称轮询或全池目录。
13. 控制台能查看当前/指定历史池、调整顺序/primary、批量添加/移除并一次提交完整池；新secret仅当前组件内存，提交成功/关闭/切渠道后清除，不放localStorage或日志。历史只读、过时结果不能覆盖当前编辑；PUT成功刷新channel当前版本并提示需发布路由。永久撤销有明确确认与不可恢复提示，和移出池分开。
14. 目录详情、平台attempt与probe展示实际credential ID，普通客户看不到池信息或供应商秘密。元数据编辑不会隐式轮换池；无后台自动轮换、定时探测、供应商余额/全局账号额度或多活动实例承诺。完整dump恢复按数据后post-data触发器顺序；已建schema的data-only导入必须同事务且先version后membership，不能逐表提交、给既有version补成员或常规禁用封口触发器，详见[恢复手册](../../docs/runbooks/backup-restore.md)。

## 验收

Routing确定性/并发fixture验证两阶段不偏置、成员权重、跨模型/版本容量、熔断半开/取消/panic、同channel不重试；真实PG/HTTP验证管理CAS/历史/撤销防复活、membership派发屏障、主/次成员真实header与单笔结算、未发布更新不影响旧revision、未知消费不换Key、probe/发现实际身份且钱包不变。应用最小角色和owner不可变、审计失败回滚、主密钥重包/恢复均回归。前端契约/strict build、全Go race/vet/最终嵌入build、008迁移/角色后的本机预览分别记录。

本切片不运行性能压测、付费供应商调用或修改评审账号/MFA及真实客户资金；完整供应商/SDK认证、全渠道批量运营、自动同步、注册/支付/套餐与扩展平台继续后续独立规格。

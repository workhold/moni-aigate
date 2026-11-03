# B-P4-D1：采购历史写入的权限与事务合同

2026-11-03；基线 `d93f6096b471bd365bf1708dfbda98b847f5ca7a`。本合同承接[B-P4设计稿](b-procurement-prefix-design.md)。本页冻结受控写入方向、权限边界、命名空间和事务规则；完整逐字段DDL、typed来源与固定新profile仍须配套合同，不据本页直接启用业务写入。

## 1. 选择闭合 mutation

采用固定数据库 mutation 与独立 NOLOGIN 最小 writer。数据库在实际写入时持有业务锁，读取 before，执行 gate，生成真实业务原件与历史证据，再更新投影；Go 只提交业务输入与源身份。

不采用“APP任意改reservation/account/event后补见证”的路径。仅为journal撤销写权限不能阻止此绕过；也不采用读取当前余额或在任意AFTER trigger复制终态来认证早前gate。低级变动全量采集的invoker路线需要另建暂态重放、真实gate与锁反转约束，本次不选。

这项选择新增受审信任边界，不能在原V3 catalog中默默接受SECURITY DEFINER、额外角色或ACL。原Go构造器、现有正式迁移/provision/main及旧profile保持原行为。

## 2. 身份与所有权

独立writer必须为NOLOGIN、NOINHERIT、非superuser、无BYPASSRLS/CREATEDB/CREATEROLE/REPLICATION，且运行时无schema/database CREATE。APP、worker、projector和peer不得经INHERIT、SET或ADMIN闭包取得writer或其owner能力。

迁移owner拥有表与触发器。writer只拥有受审固定函数，并获得函数实现需要的精确表/列权限；它不成为业务表owner，不能关闭触发器或改schema。安装过程中临时授予的CREATE或membership必须在受审终态撤回。所有函数默认PUBLIC EXECUTE须撤销；只有封闭业务根对准确APP开放EXECUTE，内部子函数不对APP/其他运行角色开放。

不能以`current_user`在definer内部等于writer推定调用方正确。根入口须绑定实际`session_user`与受审APP身份；owner通过SET ROLE、其他LOGIN继承APP、可SET高权路径均不得替代实际APP。actor/user字段仍须按原操作核当前角色与状态，数据库本地见证不认证人的外部会话或供应商账单真实性。

## 3. 精确 search_path 与 PB 锁域

definer固定 `search_path = <精确受信业务schema>, pg_catalog, pg_temp`。受信schema禁止APP、worker、projector、peer和PUBLIC CREATE；新函数的关系及私有函数调用均使用schema限定，不拼接调用方SQL、标识符或任意路径。schema及角色标识符只由受审安装材料安全引用，不是业务请求参数。

schema必须排第一。原020的`pb_scope_sync`按 `hashtext(current_schema() || ':' || scope || ':' || target)` 取得namespace锁；将pg_catalog排第一会把锁域变为pg_catalog，使新旧路径不能互相等待。显式pg_temp排最后，不能让临时对象抢占未限定的关系。原PB函数及其ABI不重写，所有相关函数proconfig/owner/依赖和有效权限进入新静态catalog。

根已用专用PG16.15、实际APP LOGIN及NOLOGIN writer进行隔离实验：受信schema首位受到原namespace排他锁阻塞，pg_catalog首位则绕开；这只证明该权限设计细节，不是采购新行为验收。复现材料与清理结果见后续验证记录。

## 4. APP 与 writer 的写面

APP撤销对新增origin/head/journal/decision的直接DML及TRUNCATE，也撤销对受保护原源/投影字段的raw INSERT/UPDATE/DELETE路径。撤销时必须同时检查表级与列级ACL、默认ACL及所有可达角色；只撤销列级grant而保留表级UPDATE不构成限制。

受保护面至少包括：采购account配置和金额、reservation状态/桶/追踪字段、budget events、evidence identities、cost proposal及消费、provider corrections、attempt成本及真实dispatch、meter/conflict/finalization、request.latest及对应出生信息。仍需由普通客户资金路径更新的request/attempt字段以精确列清单重新授权，不能因原宽表grant方便而保留受保护列旁路。

候选必须接完所有写入入口后才能开放受限APP：包括公开finalization、Reconcile中无attempt的`approved_reconciliation`、采购恢复、managed提案/批准和legacy-unmanaged更正。某入口尚未接入时继续关闭候选资格，不放宽ACL绕过它。

## 5. 闭合根与来源关联

业务根至少分为账户创建/配置、采购reserve、实际dispatch、输入finalization/conflict、managed提案/批准、legacy-unmanaged更正及恢复。它们可调用同writer的私有子函数，共用唯一业务状态机与证据发行逻辑，不能复制多套可漂移的before捕获。

每次真实变动有精确source ID和独立decision/ordinal关联，同一事务允许多个合法来源时逐一闭合。不能使用“当前事务最后一条decision”、caller GUC、trigger_depth或自报xid作为权限或来源选择。原始incoming输入可由Go提供；历史before、被消费成员集合、序号、gate结果、实际前驱及after必须由数据库当场推导。

request、attempt和account各自的出生/顺序域完整保留。无attempt finalization仍影响request.latest与sticky；unmanaged没有account/reservation，走独立成本链。正常finalization相同ID+digest快返不增源；重复conflict可产生新conflict原件而不增加语义成员；不同同额批准仍是新decision。当前失权的重试不能因已有回执获准。

## 6. PB 与真正的发送许可

新writer执行的原PB invoker函数会以writer身份访问关系，必须逐调用图授予真实需要的列级闭包。不能默认继承APP全权限；不能只给PB SELECT却声称reserve/finalize/dispatch已闭合。

需要的PB effect/window/allocation及其observation写面由精确SQL合同确定。writer不得借effect权限创建/启用预算control、修改配置限额/周期或发行配置命令。自动窗口观察更新与控制配置更新须分列区分，并纳入实际权限负例。

actual dispatch必须在同一闭合根内完成采购gate、采购sending和真实upstream prepared→sending；原PB guard在真正变更点执行、固定PB decision time。所有等待后重新核期限与报价，返回的许可只属于这一次实际变更；不能给外部DTO发一份可在另一事务使用的sending权限。

supplier effect不得离开原事务。若后续精确合同选择由Go在根返回后执行固定PB helper，则必须证明原source与PB完整deferred闭合、锁与before不变且遗漏无法提交；在该证明完成前不接受“稍后再补PB”作为已闭合实现。

## 7. 锁、快照与回滚

保留原各入口的前层锁顺序：身份/角色、客户与渠道等前层、request/attempt、共享采购account、供应商PB、采购reservation、proposal及新head按完整入口矩阵审定。不能从account/head回头锁前层对象，不能在已锁reservation的AFTER阶段才补account锁来宣称无锁反转。

经营写根要求READ COMMITTED，与原PB一致；等待账户/control后以VOLATILE路径取得新鲜值，不复用等待前CTE/STABLE快照。before既是gate实际使用值，也是本次证据的被消费值。RR只用于既有只读核验或另定维护阶段，不通过切换隔离级别放宽经营规则。

序号、业务原件、账户/成本投影、PB、audit/outbox与来源见证在同一事务闭合。statement/savepoint/事务回滚不留下已提交缺口。保留现有锁3秒/statement5秒及既有上层绝对期限；不得因新函数增加工作而直接放宽。

## 8. 语义错误与提交结果

闭合根采用封闭的typed结果协议：区分业务成功、允许保存原件后提交的语义冲突、必须回滚的业务拒绝，以及基础执行错误。精确返回列/枚举在来源合同冻结，不能将任意error字符串变成可提交状态。

公开RecordFinalization的真实metering conflict可提交冲突证据并向调用方返回原语义错误；Reconcile内部同一子流程的语义冲突按原行为回滚整个reconcile。二者使用相同事实逻辑、不同明确的外层事务决策，不能把所有semantic error都提交或都吞掉。

仅已知40P01/40001按原有界规则重试已回滚事务；COMMIT回复未知不是可重试失败。按原操作身份只读查结果，不重发模型生成、不自动重做批准或财务effect。未知提交与供应商消费未知分别记录。

## 9. 候选选择、安装及核验

候选executor由显式受审profile装配，旧构造器继续原路径。不能探测表/函数存在、调用成功、caller布尔或配置开关来认证权限与完整历史；这些最多是选择器，实际数据库与完整性仍须固定catalog/history和原事务核验。

只有精确安装前驱、完整候选DDL/函数/角色ACL、全来源列集、独立纯重放及显式新profile共同冻结后才能接入完整开账/observer/runtime。原V3接口、固定读集、basis/catalog、旧proof与Base计数保持。正式migration/provision/CLI/startup/ready和在线来源资金仍按R2另行交付。

## 10. 权限与事务验收门禁

actual APP必须不能raw改原成本/桶/源或新证据，不能调用内部helper、SET writer、改函数/search_path/trigger或利用默认ACL扩权。精确APP根调用须能生成真实新历史；owner或SET ROLE不得替代业务LOGIN。

至少验证schema/pg_catalog/pg_temp顺序及临时对象遮蔽、PB新旧namespace并发等待、同事务多根精确关联、回滚/deferred遗漏、同额批准、重复conflict、无attempt Reconcile、unmanaged更正、失权重试、未知提交只读观察及完整十一项命题。未通过的数据域继续缺证，不以本页或隔离实验PASS关闭B-P3。

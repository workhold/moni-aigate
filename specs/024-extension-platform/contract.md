# 024 合同草案

2026-11-01，**A0精确合同已冻结019，生产实现待执行；B–F未冻结**。本文件A部分由[a-contract](a-contract.md)细化并冻结；B–F只有进入详细设计的门禁，不是媒体/身份/长连接API授权。配套[规格](spec.md)、[计划](plan.md)、[验收](acceptance.md)与[设计](../../docs/design/extension-platform.md)。

## 1. A的严格值、权限与开关

- ID和operation_id为非nil规范小写UUID，revision为正BIGINT十进制字符串。金额单位CNY整数微元；阈值为`0..9223372036854775807`规范字符串，禁止JSON number、小数、指数、前导零和`-0`。计数和秒也用下述有界十进制字符串。时间输出UTC微秒RFC3339，`null`明确缺失。
- label为1..80个Unicode字符且UTF-8不超过160字节，拒控制字符、纯空白和首尾空白；不是HTML。reason不是任意客户端输入，只用合同固定枚举。所有DTO拒绝未知、重复、缺失、null替代必填、尾随JSON和重复query；body上限8KiB。
- 所有接口在Console session、实例Guard、当前MFA要求之后。读取/确认要求当前active用户的platform_role为`admin|finance_operator|finance_approver`；规则创建/配置/退役要求admin。写还要有效CSRF、同源、近期再认证和非恢复模式，事务内重读并锁当前用户核验状态/角色。客户tenant角色不带来任何权限，API Key不是Console凭据。
- `OPERATIONS_ALERTS_ENABLED`为严格boolean，默认false，仅控制评估worker；有效配置在排空重启后生效。关闭时仍可读取旧发生记录、管理规则和确认，能力/新鲜度明确显示暂停；不做隐式恢复、回补或补发。恢复模式不评估且禁止mutation，既有只读入口可用。无外部通知配置，无通知默认收件人。
- 实例硬上限：最多256条非retired规则、其中最多64条enabled；同`kind+scope_id`最多一条非retired规则。达到上限为持久业务拒绝，不静默覆盖旧规则。retired不占上限且不可恢复；其历史不删除。
- HTTP读每用户60/min、burst20；写每用户20/min、burst5；使用独立key前缀和有界既有limiter，无可用桶503、速率429。查询最多2秒、每页50+1且响应上限256KiB。写数据库事务预算3秒、锁等待最多500ms。无自动HTTP重试。

## 2. A的规则和评估函数

规则的`kind,scope_type,scope_id`创建后不可变；换目标/种类须退役后新建。`state=enabled|disabled|retired`。创建和配置显式提供完整`label,state,parameters`，创建/普通配置只允许enabled或disabled，退役走独立命令。有效配置实际改变才增加configuration_revision；完全相同的已知配置可返回applied且revision不变，不结束当前发生记录。

| kind / scope_type | parameters精确字段 | 评估和恢复 |
| --- | --- | --- |
| `channel_quarantined` / `channel` | `{}` | 成功快照quarantined=true为breach，false为clear；enabled=false不改变该判断 |
| `procurement_budget_low` / `procurement_account` | `{threshold_amount,recovery_amount}`，两者非负int64且recovery>=threshold | `available=limit_amount-spent_amount-held_amount`，无open记录且available<=threshold则breach；有open记录时available>recovery才clear，其余hold |
| `customer_reconciliation_aged` / `tenant` | `{minimum_age_seconds,trigger_count,recovery_count}` | age为600..604800秒；trigger为1..1000、recovery为0..999且recovery<trigger；合格行count>=trigger为breach，有open时count<=recovery为clear，其余hold |
| `procurement_unresolved_aged` / `procurement_account` | 同上 | 同一计数/滞回，采购状态筛选不同；不按客户24h释放清除 |

创建时非锁定SELECT确认资源存在；停用tenant、paused采购账户和disabled渠道仍可被观察，不要求它们可接流量。规则表保存逻辑资源ID，不对业务表加FK/触发器，也不反向锁业务根；运行时资源缺失是`source_missing`评估失败，不能clear。kind/scope匹配由CHECK保护，不接受通配、列表或任意额外参数。

每次成功评估在规则锁等待结束后执行一个SQL语句，物化唯一`as_of=clock_timestamp()`并取得源表同一语句MVCC快照。无源业务行的FOR SHARE/UPDATE；样本只说明这个观察时刻。DB时刻早于该规则上次成功时刻报`clock_regression`，保留原状态，不能用进程时刻补齐。

- 客户积压：`reservations.tenant_id=scope_id AND status='pending_reconciliation' AND created_at<=as_of-age`，不把全部在途reserved视为未知。
- 采购积压：`procurement_reservations.account_id=scope_id AND state IN ('unknown','disputed') AND created_at<=as_of-age`。年龄由创建时间起算，不能展示成unknown持续时间。
- 两种积压按`created_at ASC,request_id/attempt_id ASC`最多取1001条。observed_count<=1001；恰取到1001时count_is_lower_bound=true，即使实际正好1001也保守显示“至少1001”。阈值最大1000，足以确定触发/恢复；不提供截断行的资金合计。取到的首行给oldest_created_at和oldest_reference_id，空集合两者null。
- 预算样本直接读019账户投影，held/spent保持NUMERIC精度。available允许负数，不截成0；这是登记累计预算余量，绝不是供应商当前现金余额或023周期预算。
- 渠道样本只读持久quarantined和enabled。内存熔断、凭据健康、profile block及binding isolation不等于该kind，后续另加种类须独立规格。

成功评估breach且无未结束记录时创建episode；已有open/acknowledged时更新最后观察/样本，hold保持原episode，clear结束为recovered。无episode且未达到触发阈值即保持无episode。首次开启时已经成立的条件在首次成功评估创建记录，opened_at是本系统发现时间，不回填历史开始时间。

## 3. A的生命周期与并发

```text
规则 disabled --显式CAS启用--> enabled --显式CAS关闭--> disabled
规则 enabled/disabled --退役--> retired（终态）
发生记录 无 --breach--> open --ack--> acknowledged
发生记录 open/acknowledged --clear--> resolved(reason=recovered)
发生记录 open/acknowledged --配置实际变化/关闭/退役--> resolved(相应reason)
发生记录 resolved --以后再次breach--> 新ID的open
```

确认是全平台共同记录，不是每个用户的私有已读；第一位成功确认者的actor/time永久保留。确认不使检测静音、不阻止样本更新、不清资金/隔离、不调用既有对账接口。已确认或已结束的episode不能再次确认；同operation重读原回执。后台不能冒充用户确认。

规则任何有效配置实际变化都原子结束当前episode（reason=`rule_changed|rule_disabled|rule_retired`，依最终动作），清空当前runtime成功样本/错误/last_success并设置下一评估时间；单纯label变化也如此，界面保存前明确提示。当前规则改动不能重写旧episode冻结的rule snapshot。无变化的配置不关闭episode。disabled/retired不评估；部署开关关或恢复模式只暂停，不结束episode。

configuration_revision只随规则配置变化增长；episode.action_revision初始1，只随ack或结束增长；周期样本、last_seen和runtime错误不增长上述revision。ACK必须expected_action_revision，规则配置必须expected_revision。确认与恢复/改规则竞争由规则锁及episode锁串行，成功恢复或改规则后旧ACK拒绝；新的episode不会继承旧ACK。

告警锁序：当前actor user SHARE（用户写）→alert command→singleton alert_control（规则写）→alert rule→alert runtime→episode，最后写事件/审计/终态回执。ACK不取control；worker从rule开始，不取actor/control/command。多行同层按ID排序。禁止持告警锁再进入billing、procurement写、channel/tenant锁或网络。来源仅MVCC读取且逻辑ID无业务FK，避免引入业务根反向锁。

每个变更及相应事件、审计、命令终态同事务。worker每次成功决策和runtime/episode/event同事务；失败不得部分close/open。评估错误回滚后用新的短事务按相同rule revision记录错误/下一次时间，配置已变则丢弃该晚回错误。DB不可用、上下文取消或Guard失效无法记错时，只读新鲜度自然变旧，不伪造最后成功。

## 4. A的数据合同（拟追加迁移）

迁移编号由集成时分配下一个顺序，不占用022/023并行编号、不修改已应用SQL。以下表均新建；不改财务表状态机和现有API。

| 表 | 字段与必要约束 |
| --- | --- |
| `alert_control` | singleton bool PK/check true；规则写锁的稳定根，迁移插入一行；不保存资金或动态开关 |
| `alert_rules` | id UUID文本PK；kind/scope_type/scope_id；label；state；parameters JSONB；configuration_revision BIGINT>0；created_by/updated_by actor文本；created_at/updated_at/retired_at。identity不可变，revision触发器只按有效配置变化递增；retired不能逆转；部分唯一(kind,scope_id) WHERE state<>'retired' |
| `alert_rule_runtime` | rule_id PK/FK；episode_generation BIGINT>=0初始0；next_evaluation_at nullable；last_attempt_at/last_success_at nullable；evaluated_revision nullable；last_sample nullable JSONB；last_error_code nullable；current_episode_id nullable。复合FK(rule_id,current_episode_id)指向同规则episode；配置重置后evaluated_revision/sample/success均null，generation不重置；rule锁内新episode才递增generation，溢出报counter_exhausted且不创建 |
| `alert_episodes` | id PK，rule_id FK，generation BIGINT>0，rule_revision，rule_snapshot JSONB，opened_at，initial_sample JSONB；last_seen_at/latest_sample；action_revision；acknowledged_by/at nullable；resolved_at/resolve_reason nullable。UNIQUE(rule_id,generation)及(rule_id,id)，部分唯一rule_id WHERE resolved_at IS NULL；初始身份/样本不可变，ack不可替换/清除，resolved不可重开 |
| `alert_episode_events` | id PK；episode_id FK；action_revision；type；actor文本（worker为system）；observed_at；safe_sample nullable；created_at；UNIQUE(episode_id,action_revision)。type仅opened/acknowledged/recovered/rule_changed/rule_disabled/rule_retired；追加不可变 |
| `alert_commands` | operation_id PK；actor；kind；target_id nullable；canonical_digest SHA256；status=claimed/applied/rejected；reason；resource_id nullable；result_revision nullable；created_at。claimed仅允许事务内临时claim，reason为空且result为空，deferred检查禁止claimed COMMIT；只许claimed→一个终态，终态不可变；HTTP只见applied/rejected，请求正文不存储，不保存秘密 |

JSONB不是任意文档存储。rule parameters、rule snapshot及四类sample均按本文件字段白名单、类型/大小和kind匹配校验；单样本<=2KiB、规则快照<=1KiB。DB CHECK/触发器保护不可变身份、枚举、规范参数、合法单向状态、revision自动派生及每个action_revision的对应事件；deferred校验在COMMIT时要求发生记录最后action revision有匹配事件、runtime当前指针指向本规则未结束记录。普通SQL不能只改关闭状态而不落事件。

必要索引：runtime(next_evaluation_at,rule_id) WHERE next_evaluation_at IS NOT NULL；episodes(opened_at DESC,id DESC)、episodes(rule_id,opened_at DESC,id DESC)；events(episode_id,created_at,id)；commands(actor,created_at,operation_id)。业务源表仅追加读索引：客户reservations(tenant_id,created_at,request_id) WHERE status='pending_reconciliation'；采购reservations(account_id,created_at,attempt_id) WHERE state IN ('unknown','disputed')。如现有索引已等价覆盖则不重复创建，并记录EXPLAIN证据。

app允许规则/运行态及受保护episode的必需列写、历史event INSERT、command claim/终态写；所有告警表禁止app DELETE，event与终态command禁止UPDATE；identity/历史不可变由trigger补防。projection角色全部REVOKE，migration/owner离线角色管理DDL。A无在线purge；样本更新不逐轮追加历史，无外部payload/收件人；日后历史保留/清理须另立合同。

## 5. A的worker与失败边界

- 仅在部署开关开启、非恢复模式、实例Guard有效且主生命周期未退出时评估。独立worker、最多2个固定执行槽、最多2个专用pool连接；不占财务恢复/报表串行slot，不用邮件队列。没有生成/HTTP/SMTP客户端依赖。
- 每1秒调度一次，只为当前空闲槽选择到期rule，按next_evaluation_at/rule_id，单条`FOR UPDATE OF rule SKIP LOCKED`后锁runtime并重新核对state/revision/due。不预claim长队列；源查询与落告警保持同一有界事务，无外部IO，无持久lease（仍受单活，非HA认证）。
- 单评估事务<=2秒、源SQL<=1500ms、锁等待<=250ms；成功next_due=as_of+60秒，已知失败退避30秒。无可提交错误时进程槽有1..30秒有界退避，不能热循环。上一轮未结束不为同一rule起第二轮。
- runtime错误码白名单为`source_missing|source_query_timeout|source_unavailable|clock_regression|counter_exhausted|evaluation_failed`；不存在供应商原始错误文本。Guard失效和退出取消不伪造source故障；无法可靠分类时只用evaluation_failed。
- 每次认领前以独立<=1秒Guard核验，执行绑定主worker取消上下文；Guard真实失败沿既有fence停止进程，不能把评估SQL超时自行解释成所有权丢失。SIGTERM先停止新评估并取消运行槽，全部worker退出后才关闭pool；普通评估失败只降级告警，不停止收费业务。
- 正常目标为每60秒观察，**不承诺60秒内一定检测**。健康容量、锁争用、故障和暂停都可能延迟。每条规则上次成功样本超过180秒显示stale；API读取用当前DB时刻计算，不能用浏览器时钟宣称healthy。没有成功样本为never_evaluated；关闭规则、退役规则或部署暂停分别明确显示。
- 进程在决策COMMIT前退出全部回滚；COMMIT后退出保留episode和due，重启只重评估原source，不重复发生记录。未知COMMIT允许worker下一次重新读源/规则进行幂等决策，因为没有外部效果；不得重试生成或财务操作。真实进程故障验收与PG状态模拟分开记录。

## 6. A的HTTP合同

统一单体成功`{data:SafeView,request_id}`；列表`{data:[SafeView],next_cursor:string|null,request_id}`，空数组与null明确出现；所有响应no-store。错误沿`{error:{code,message},request_id}`，固定安全message，不回显body/SQL。以下路径均为新设计，当前不可用。

| 方法/路径 | 精确输入 / 输出 |
| --- | --- |
| GET `/api/platform/alerts/capabilities` | 无query/body；Capabilities |
| GET `/api/platform/alert-rules` | 可选state/kind/scope_id/cursor；RuleView列表 |
| POST `/api/platform/alert-rules` | `{operation_id,kind,scope_type,scope_id,label,state,parameters}`；CommandReceipt |
| GET `/api/platform/alert-rules/{id}` | 无query/body；RuleView |
| PUT `/api/platform/alert-rules/{id}` | `{operation_id,expected_revision,label,state,parameters}`；CommandReceipt |
| POST `/api/platform/alert-rules/{id}/retire` | `{operation_id,expected_revision}`；CommandReceipt |
| GET `/api/platform/alerts` | 可选status(open/acknowledged/resolved)/rule_id/kind/cursor；EpisodeView列表 |
| GET `/api/platform/alerts/{id}` | 无query/body；EpisodeView |
| GET `/api/platform/alerts/{id}/events` | 可选cursor；EventView列表 |
| POST `/api/platform/alerts/{id}/acknowledge` | `{operation_id,expected_action_revision}`；CommandReceipt |
| GET `/api/platform/alert-operations/{operation_id}` | 当前原actor且当前仍有原kind权限；CommandReceipt；其他actor或不存在404 |

query枚举逐项校验；scope_id须与kind同时提供且语义匹配，禁止未知query。列表固定created_at语义：rules按created_at、episodes按opened_at、events按created_at，均时间DESC/ID DESC，50+1 keyset。cursor为规范RawURL base64的`{v:1,t,id,filter_digest}`，无padding、<=512字节；t是对应时间UTC微秒，filter_digest为规范过滤对象SHA256，防止换筛选继续旧页；cursor不授权也不冻结全页快照，每页重验当前读权。响应next_cursor无下一页时null。

| SafeView | 精确字段 |
| --- | --- |
| Capabilities | `{deployment_enabled:bool,recovery_mode:bool,evaluation_available:bool,reason:available|deployment_disabled|recovery_mode,max_enabled_rules:"64",evaluation_interval_seconds:"60",stale_after_seconds:"180",observed_at:time}`；reason恢复优先于部署关闭，available不是健康认证 |
| RuleView | `{id,kind,scope_type,scope_id,label,state,parameters,configuration_revision,created_by,updated_by,created_at,updated_at,retired_at:time|null,runtime:RuntimeView}` |
| RuntimeView | `{next_evaluation_at:time|null,last_attempt_at:time|null,last_success_at:time|null,evaluated_revision:string|null,last_sample:Sample|null,last_error_code:string|null,current_episode_id:id|null,freshness:disabled|retired|paused|never_evaluated|fresh|stale,observed_at:time}` |
| EpisodeView | `{id,rule_id,generation:string,rule_revision:string,rule_snapshot:{kind,scope_type,scope_id,label,parameters},status:open|acknowledged|resolved,action_revision:string,opened_at:time,initial_sample:Sample,last_seen_at:time,latest_sample:Sample,acknowledged_by:string|null,acknowledged_at:time|null,resolved_at:time|null,resolve_reason:recovered|rule_changed|rule_disabled|rule_retired|null,evaluation_freshness:同Runtime枚举,observed_at:time}` |
| EventView | `{id,episode_id,action_revision:string,type,actor:string,observed_at:time,safe_sample:Sample|null,created_at:time}`；opened/recovered带当时sample，ack/config结束事件safe_sample为null |
| CommandReceipt | `{operation_id,kind:rule_create|rule_configure|rule_retire|acknowledge,status:applied|rejected,reason:string,resource_id:id|null,result_revision:string|null,created_at:time}`；applied reason=""且resource/revision非null；rejected revision=null，create resource=null，其余resource为原path目标 |

freshness优先级：rule retired→retired，disabled→disabled，部署关闭或恢复→paused，无当前revision成功样本→never_evaluated，`observed_at-last_success_at>180s`或DB时间回退→stale，其余fresh。last_error_code不清掉已有成功样本，只在下一次成功或配置重置时清空；UI同时显示“上次评估失败”，不能只看freshness隐藏错误。历史episode的evaluation_freshness来自当前规则，不能据此推断旧初始样本仍为当前事实。

Sample精确类型：

- channel：`{kind:"channel_quarantined",as_of,enabled:bool,quarantined:bool}`。
- budget：`{kind:"procurement_budget_low",as_of,account_state:active|paused,limit_amount,held_amount,spent_amount,available_amount,currency:"CNY"}`；held/spent为非负至100位字符串，available为规范有符号至101位字符串，不省略负值。
- aging：`{kind:customer_reconciliation_aged|procurement_unresolved_aged,as_of,minimum_age_seconds:string,observed_count:string,count_is_lower_bound:bool,oldest_created_at:time|null,oldest_reference_id:id|null}`；observed_count为0..1001，1001必lower_bound，0时oldest两字段null。

无供应商endpoint、凭据/摘要、prompt/response、邮箱、MFA、完整账务证据或自由文本日志。引用只用于既有受权页面，不接受任意链接。新DTO不能直接复用通用`SELECT *`或报表JSON。

### 6.1 命令、回执和结果不明

所有mutation仅返回HTTP200的CommandReceipt，前端按status判断；GET operation也只返回原不可变回执，资源当前视图另查。规范摘要对象固定为`{v:1,actor,kind,target_id,body}`，target_id来自path，create为null；body排除operation_id，其余字段按上表且参数字段按固定顺序规范编码。金额/revision/枚举已校验规范化；不把request_id和进程时间计入摘要。

先核验当前权限，再claim命令；同operation+同actor+同规范摘要返回原终态，相同ID异体或他人ID返回409 `alert_operation_conflict`。可判定业务拒绝在savepoint内回滚效果后持久rejected：`alert_revision_stale|alert_rule_limit|alert_rule_exists|alert_rule_retired|alert_source_missing|alert_not_open|alert_already_acknowledged`。不明资源的configure/retire/ack也持久`alert_not_found`拒绝，未返回来源信息。语法400、当前权限403、认证401、Guard/恢复503、限流429在claim前拒绝；source/DB/审计/事件非预期错误整个事务回滚，503不表示未提交。

仅驱动明确证明已回滚且仍在事务预算内可重试原DB closure；COMMIT结果不明不自动发新命令。页面保留原operation_id和安全期望值，只GET原operation；404仅表示该读快照尚未找到，不能证明旧事务绝不提交。用户明确核查后才可按当前revision发起新的操作。UI关闭/换账号/换筛选丢弃晚回，不跨身份复用operation；无业务秘密入浏览器存储。

## 7. B–F的后续冻结出口

| 切片 | 编码前必须独立冻结的合同 | 本地与外部门禁 |
| --- | --- | --- |
| B0媒体计量 | 维度向量/单位、最大请求资源、聚合上界、售价和采购费率版本/舍入、未知非零维度、模型目录/quote/账单、原token兼容、采购与来源分配；不能把图片数量强塞input_tokens | 真实PG上下界/溢出/唯一结算/unknown；profile官方定价、请求上界和usage证据逐家认证 |
| B1同步媒体 | 选择一种图片生成或一种音频端点和一个模型版本；只接受受界限的输入/结果类型，明确是否二进制/base64及大小、资源退出、内容保存与净化；客户URL不默认抓取 | JSON/二进制、断流、取消、结果不交付、缺usage、真实供应商扣费对账；本地fixture不等价正式供应商 |
| C0/C1异步任务 | tenant/Key所有权、客户端幂等与上游task identity、提交未知但可能扣费、只读轮询与受验签回调、乱序/重复、取消能否停费、held期限/终态、结果访问/保留期；明确后台可做的状态查询但永不重发生成 | 进程终止各窗口、回调伪造/重放、轮询限额、租户隔离、未知长期占用；真实任务和供应商取消/费用语义认证 |
| D0/D1长连接 | 选定协议和事件版本；握手/续期/轮次授权、每轮与session预算、持续硬预占、计费维度、最大连接/缓冲/时长、断线/重连/未知消费；现有300秒与一次settlement不能直接复用为无限会话 | 慢客户端/背压/断线/多事件并发、资金耗尽不超支、进程退出、真实SDK/供应商usage和扣费；025前仍单活 |
| E0/E1外部身份 | 首片一个固定OIDC issuer与Client、PKCE/state/nonce、严格issuer/aud/sub、回调/发现/JWKS出口、账号显式绑定和解绑、邮箱冲突、禁止自动合并、现有MFA/reauth/family撤销及不可登录恢复边界；Passkey另冻challenge/origin/RP/user-verification/counter/恢复 | 本地伪issuer故障/密钥轮换/重放与账号接管反例；真实IdP注册客户端、重定向、回收/退出能力及浏览器Passkey认证分别验收 |
| F运营 | 品牌资源安全与匿名缓存、公告租户/公开域、多语言回退与固定错误码；外发通知独立收件授权、限流、摘要/秘密、可靠投递/重复/不确定窗口/SSRF，不能复用身份令牌outbox装任意通知 | 真实浏览器/可访问性；外发邮件真实收件或webhook外部端点另门禁 |

024-A不依赖022支付完成或023周期预算落地，且不改它们合同。B/C/D若加入新的资金维度与期限，必须与当时已实现的022来源lot、023预算/商品事实逐路径交叉审查；它们不是本稿已解决的问题。E不要求商业资金表改变，但涉及现有登录锁序与MFA，须单独身份集成人统筹。

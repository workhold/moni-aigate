# 021 接口、状态与数据合同（冻结）

2026-11-01，**已实现，限定本地验收通过**。本合同只定义021；真实SMTP收件仍待外部验证，执行证据见[验收记录](../../docs/verification/self-service-identity.md)。设计说明见[详细设计](../../docs/design/self-service-identity.md)，完成条件见[验收](acceptance.md)。

## 1. 配置与有效能力

| 配置 | 默认与约束 |
| --- | --- |
| `AUTH_REGISTRATION_ENABLED` | `false`；仅显式 true 允许自助注册 |
| `AUTH_PASSWORD_RESET_ENABLED` | `true`；仍依赖邮件 transport 和非恢复模式 |
| `MAIL_TRANSPORT` | `disabled`；允许 `disabled`、`smtp` |
| `SMTP_HOST` / `SMTP_PORT` | smtp 模式必须配置单个主机和 1–65535 端口，不能是 URL 或含控制字符 |
| `SMTP_FROM` | smtp 模式必须为单个有效邮箱，不允许显示名、多个地址或头注入 |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | 同时设置或同时省略；不得输出到日志、公开配置或客户端 |
| `SMTP_TLS_MODE` | 默认 `starttls`；允许 `starttls`、`implicit`、`plain_loopback` |

`smtp` 配置不全或非法必须启动失败。`disabled` 是有效配置，即使业务 flag 为 true，邮件相关有效能力仍为 false。STARTTLS 必须协商成功，implicit 从连接起使用 TLS；两者校验证书链与主机，最低 TLS 1.2，不提供跳过校验或降级选项。`plain_loopback` 仅允许主机字面量 `127.0.0.1` 或 `::1`，用于本地 fixture，不接受依赖 DNS 的 `localhost`。

邮件启用时，`PUBLIC_ORIGIN` 必须为可信、无 userinfo/path/query/fragment 的 HTTPS origin；loopback fixture 可使用 loopback HTTP origin。链接永远不从 `Host`、`Origin`、`Referer` 或转发头拼接。

`GET /api/auth/capabilities` 不要求登录，沿用实例 ready/guard 与 no-store。返回标准 `data` envelope：

```json
{
  "email_delivery_configured": false,
  "registration": {"available": false, "reason": "feature_disabled"},
  "password_reset": {"available": false, "reason": "mail_not_configured"}
}
```

`reason` 为 `available|feature_disabled|mail_not_configured|setup_required|recovery_mode`，按恢复模式、feature flag、transport、初始化标志顺序决定；初始化标志仅约束 registration。`email_delivery_configured=true` 仅表示配置有效，不能解释为 SMTP 健康或已收件。状态对所有邮箱一致，不输出 SMTP 详情。新 mutation 在当前有效能力 false 时统一 503 `identity_unavailable`，不按输入邮箱改变结果；恢复模式同时暂停新投递。

公开注册还必须依赖持久的“平台初始化完成”标志，不能仅用 `count(users)>0` 判断。空库先执行 Bootstrap，管理员与初始化标志在现有 bootstrap advisory lock `782193403` 下同事务提交；注册不能成为第一条 user，避免普通账号使现有 Bootstrap 永久拒绝。迁移仅对确有既有 admin 的库补初始化标志；不把普通账号、未验证注册或 serve 启动当初始化。标志只表示曾合法完成初始化，不随管理员临时停用而抖动，也不赋予其注册用户管理权限。

这些配置当前是启动时读取，021 不增加热更新 API；变更需要按既有排空/重启流程生效。关闭能力是暂停签发、消费和新投递，**不是批量撤销旧证明**；重新开启时，仍在 TTL 内且未被替换/改密/消费的证明可继续使用，TTL 不延期。若需要永久撤销所有证明，必须另行设计受审计的撤销操作，不能宣称关闭开关已完成撤销。已经进入网络的邮件无法撤回。

## 2. 公开 HTTP 接口

新增入口不挂到已有 `session()` 中间件下；它们全部经过 ready/guard。POST 必须通过已有 `validOrigin` 同源检查，且 JSON content type、严格字段/重复字段/单对象/尾部校验，body 上限 4096 字节。未登录不要求不存在的会话 CSRF token；邮件令牌不接受 Cookie 或查询参数传递。

| 方法与路径 | 严格输入 | 成功 |
| --- | --- | --- |
| POST `/api/auth/registration/request` | `email` | 202 `{ "accepted": true }` |
| POST `/api/auth/registration/complete` | `token,password,tenant_name` | 200 `{ "completed": true, "login_required": true }` |
| POST `/api/auth/password-reset/request` | `email` | 202 `{ "accepted": true }` |
| POST `/api/auth/password-reset/complete` | `token,password,mfa_code`；`mfa_code` 必填字符串，未启用时可为 `""` | 200 `{ "completed": true, "login_required": true }` |

全部沿用 `{data,request_id}` / `{error:{code,message},request_id}` envelope。请求邮件的统一文案为“请求已接受；如果该邮箱符合条件，稍后会收到邮件”。已注册邮箱的注册请求、不存在或停用邮箱的恢复请求、不符合条件或处于邮箱冷却期的请求，都返回完全相同的 202 及正文，不返回 challenge ID、用户 ID、MFA 状态、是否排队或发送结果。

邮箱规范为现有 `lower(trim(email))`，随后严格校验单个 ASCII 邮箱、最长 254 字节；不接受显示名/控制字符/内部空白，不折叠 `+tag` 或点。密码沿用 12–72 字节、bcrypt cost 12；MFA输入是至多128字节且无控制字符的字符串，可为 `""`；租户名去首尾空白后 1–200 字节且不含控制字符。非法字段格式统一 400 `invalid_request`，与账号是否存在无关。

无效/过期/撤销/已消费/跨用途证明、注册邮箱冲突、停用账号、密码版本不符、MFA 缺失/错误均统一 400 `identity_proof_invalid`，不细分有效 token 或 MFA 状态。前端始终显示“若账号已启用验证器，请填写动态验证码或恢复码”。校验拒绝事务回滚，不消费任何有效证明、恢复码或 TOTP step；COMMIT 结果不明则按第 8 节处理，不能声称一定回滚。系统错误统一 503，不返回数据库/SMTP 原文。请求邮件的 DB 提交结果不明仍不能显示“已发送”。

## 3. 令牌、频控与失效

- token 为 `ic1_` 加 32 随机字节的 base64url 无 padding 文本（共 47 字符）。校验精确格式；不接受整条链接。摘要使用 `Secrets.Digest("identity-challenge:v1:" + purpose + ":" + token)`；用途为 `registration|password_reset`。
- 邮箱主体索引为 HMAC `Secrets.Digest("identity-mail-subject:v1:" + purpose + "\x00" + normalized_email)`；不在普通索引或审计存原始邮箱。
- 注册 TTL 30 分钟，密码恢复 TTL 15 分钟，均使用 DB `clock_timestamp()`，`now >= expires_at` 即失效；不能以事务开始时间或浏览器时钟判断。
- 每 purpose/邮箱至少间隔 60 秒，固定一小时窗口最多签发 5 份；窗口、计数和下次允许时间持久化。签发新证明时撤销该主体以前未消费证明，旧邮件可能仍到达但不能使用。
- 匿名请求先执行基于现有可信传输地址捕获的 IP 桶（burst 5，每 12 秒补 1）；再执行按 HMAC 邮箱或 token 索引的桶（burst 5，每 60 秒补 1）。邮箱/token 桶拒绝：邮件请求静默 202，完成接口统一 429；IP 桶拒绝均 429。检查在账号查询前执行，结果不依赖账号存在。复用已有最多 4096 条、过期清理的有界限流存储，容量不足统一 503。
- eligible 邮箱的持久计数不能因进程重启或内存桶清理而绕过；不存在/不合条件邮箱只经过相同内存桶，不落无限量假令牌。请求端不承诺严格时间等价，不能声称已解决所有统计计时侧信道；必须保证公开状态/正文/错误不枚举。
- 完成接口的 bcrypt 在事务锁外进行，且经上述限流；额外设置进程内最多 4 个并行身份密码 hash，容量不足 429。不能用登录或 MFA 的既有桶偷偷替代这项边界。
- 所有签发/完成都检查当前 feature flag、恢复模式及账号状态。关闭能力后不继续消费已有证明；TTL 照常流逝。

## 4. 建议数据模型与写保护

迁移必须追加；具体迁移编号在集成时确定，避免与并行 020 冲突。

| 实体 | 关键字段与约束 |
| --- | --- |
| `identity_initialization` | singleton、initialized_at、initialized_by（既有 admin 的 user ID）；一次性初始化，不由公开注册写入 |
| `users` 新字段 | `email_verified_at timestamptz NULL`；`password_revision bigint NOT NULL DEFAULT 1 CHECK (>0)` |
| `identity_mail_subjects` | `id UUID`、purpose、email_digest、window_started_at、window_count、next_issue_at、last_seen_at；`UNIQUE(purpose,email_digest)`；计数非负；无明文邮箱 |
| `identity_challenges` | id、subject_id、purpose、token_digest UNIQUE、user_id NULL、password_revision NULL、registration_email_ciphertext NULL、created_at、expires_at、consumed_at、revoked_at |
| `identity_mail_outbox` | id、challenge_id UNIQUE、payload_ciphertext、state、attempt_count、next_attempt_at、claim_generation、lease_until、sent_at、last_error_code、created_at |

subject 与 challenge 的 purpose 使用复合外键约束一致。注册 challenge 只有加密邮箱、无 user/version；reset challenge 只有 user/version、无注册邮箱。注册邮箱密文可按秘密清理规则单向清空，不得因此令已终结行违反目标类型约束。时间满足 `expires_at > created_at`；consumed_at 与 revoked_at 不能同时非空。同主体至多一条 `consumed_at IS NULL AND revoked_at IS NULL` 证明；签发新 token 前先撤销旧 token，包括已过期但尚未清理的 token。

`password_revision` 由数据库触发器在 `password_hash` 实际变化时恰好递增，普通改密及将来任何改密路径都不能漏掉；禁止业务直接伪造或降低 revision。初始化用户为 1。成功重置只写新 hash，通过同一触发器得到新 revision，不再手工加一次。

challenge 的归属、用途、摘要、密码版本、创建/过期时间不可修改。消费与撤销只能单向写入，不能复活；注册邮箱密文允许在消费/撤销/过期后单向清空，不能换成另一邮箱。outbox 的 challenge、message identity 和创建时间不可改；payload 仅在终态单向清空，不能替换收件人或令牌。使用受限角色权限和触发器共同约束；应用角色不能删除活跃证明或重置发送次数/代际，投影角色无这些表的写权限。

审计只记录 challenge/outbox/user/tenant 等安全 ID 与动作，不记原始邮箱、token、password、mfa_code、SMTP 回复。注册完成记录新用户、租户、主体证明的关联；恢复完成记录目标 user 和会话撤销事实。新增流程的关键审计与身份变更在同一事务，审计失败回滚。

## 5. 注册事务与兼容

请求只接收邮箱，不接收密码，防止为他人预设注册密码。满足当前注册能力、已初始化、邮箱尚不存在且主体频控允许时，在同一事务内签发证明、密封注册邮箱、创建 outbox 及安全审计；不创建 user、tenant、session、Key 或资金记录。存在邮箱只给统一 202，不发送“可接管”的注册证明。

完成前预读 challenge 只用于定位主体，不能视作证明。bcrypt 在锁外；事务锁主体、锁 challenge 后重新验证用途/摘要/当前 DB 时钟/状态，然后解密邮箱。插入新 user 依赖规范化邮箱唯一约束，遇冲突统一失败并回滚，**不得把已有 user 关联到新租户或覆盖其密码/角色**。

仅成功插入的新 user 可在本事务创建全新 tenant 及 membership：user=`active/none`，邮箱验证时刻为当前 DB 时间；tenant=`active`、默认地域沿用现有 global；membership=`owner/active/allow_all_models=false`。不创建任何 tenant/member model grants、API Key、credit/赠额、订单或采购余额。无账本入金即余额零，不能另写一条伪资金流水。在所有插入/唯一冲突等待结束后、消费证明前再次读取 DB 时间与初始化标志；越过 TTL 或尚未初始化则整笔回滚。事务最后消费证明、撤销同主体其他证明及写审计，一起提交，不签发会话。

现有 `CreateTenant` 使用 `allow_all_models=true`，不能直接调用它作为自助开户实现。既有用户的 `email_verified_at=NULL` 保持原登录行为，不静默回填验证成功、不强迫 MFA。

## 6. 恢复事务与实际锁图

请求 reset 先只读定位 user；eligible 时事务先锁当前 `users FOR UPDATE`，复核邮箱与 active 状态，再锁/创建 reset 主体、读取新 DB 时间、检查持久频控，以当前 password_revision 签发证明/outbox。同一 subject 的签发串行；不把旧查询到的密码版本当作锁内事实。

完成 reset 的固定顺序：

```text
只读预定位 challenge → 锁外 HashPassword
→ users FOR UPDATE
→ identity_mail_subjects FOR UPDATE
→ identity_challenges FOR UPDATE
→ 当前 MFA 校验（可锁 mfa_recovery_codes）
→ session_families（该用户全部）
→ 审计 → COMMIT
```

拿锁后重新校验 user active、当前邮箱索引、purpose/摘要、最新未消费证明、`clock_timestamp()` TTL 及当前 password_revision。当前 `mfa_enabled=true` 时调用现有事务内验证逻辑，使用当前 secret、TOTP last_step 或单次 recovery code；不能信任邮箱请求时、旧 session 中或前端传入的 MFA 状态。错误 MFA 必须使整个事务回滚，包括 step/恢复码变更。

证明及 MFA 成功后，先按 family ID 稳定顺序锁住该 user 全部 families，再读 DB 时间重验 TTL；特别覆盖 Logout 或其他 family 锁等待跨过期界的情况，过期则包含 TOTP step/恢复码使用在内全部回滚。该最后校验是证明消费的授权时点，不能用锁 family 之前的时刻放行。随后同事务更新 hash（触发 revision）、`email_verified_at=COALESCE(old,now)`、消费当前/撤销同主体其余证明、撤销该 user **所有** session families 并审计。所有旧密码版本 token 自动失效，不自动登录，不改变 MFA secret/enabled 或未使用恢复码，不撤销 API Key。已启用 MFA 而丢失所有因素时，本接口无法恢复；人工恢复须另立双人/证据合同。

当前 `verifyMFATx` 在成功 TOTP 路径清空 pending secret；对已启用 MFA 的账号保留这一既有行为，不把 pending secret 恢复或替换成新因素。不能把未完成 enrollment 的 pending secret 当作 reset 要求的已启用因素。

与现有代码的关系：Login、SetupMFA、VerifyMFA、Reauthenticate、ChangePassword、Switch 都先锁 user，再处理 family/MFA；新 reset 必须沿用该顺序。Logout 只锁 family、不反向等 user。注册只操作初始化标志的只读事实、主体、证明和**新插入**的用户/租户，不能锁已有 tenant 或已有 user；与管理端创建/加成员的邮箱竞争由唯一约束结束，冲突不尝试绑定。初始化标志不可公开修改或删除，因此注册不需要从主体锁反向取得 bootstrap/admin 行锁。

会话撤销的生效边界是 reset 提交后发起的会话认证；在 reset 前已经通过 session 中间件的普通业务请求可能继续并提交，021 不承诺取消或回滚所有在途业务写。密码/MFA/会话轮换路径仍按其事务内 user/family 复查拒绝已失效证明。若产品要求所有敏感业务写以 reset 为撤权屏障，须另行补全其事务内 family 复查及统一锁图，不能仅凭更新 revoked_at 宣称已实现。既有 API Key 调用和已发送推理请求继续按原资金收尾合同执行。

Login 与 reset 串行后，reset 先提交则旧密码 proof 被 re-read 拒绝；Login 先提交则其新 family 被 reset 撤销。MFA enable 先提交则 reset 要求因素；reset 先提交则被撤销会话不能继续启用 MFA。锁等待中越过 TTL 必须拒绝，不使用事务早期时间。

## 7. Outbox 状态与恢复

邮件 payload 为严格版本化加密JSON `{v:1,to,purpose,link}`：`to`是收件邮箱，`purpose`选择固定模板，`link`是可信origin生成的完整fragment链接；拒未知/重复/大小写别名键和非1版本，不含任意template正文。`Secrets.SealFor(payload,"identity-mail-outbox",outbox_id)`；注册邮箱使用独立 `SealFor(email,"identity-registration",challenge_id)`。不同用途/记录的密文不能互换。邮件不含密码、MFA secret、恢复码、用户余额或 API Key。

```text
pending → sending → sent
               ↘ pending（可重试且未过期）
               ↘ dead（永久错误或尝试耗尽）
pending/sending → cancelled（证明失效/过期、已消费）
sending 租约过期 → sending（新 generation claim 后重试）
```

1. 独立 worker 每秒检查；最多 2 个并行 SMTP IO，**只按当前空闲发送槽 claim，单次最多 2 条**，不预 claim 10 条再在内存排队。没有可用槽就不 claim，避免最后几封尚未发送租约已过期。发送不共用已有资金恢复的串行 10 秒任务槽，不让 SMTP 阻塞账务恢复。
2. 短事务按可用时间/id 排序，`FOR UPDATE OF o SKIP LOCKED` 只锁 outbox 别名 o，增加 generation 和 attempt_count，设置 60 秒租约后提交。JOIN challenge 只作为普通读取条件，不能省略 OF 而隐式锁身份行。每封最多 5 次 claim；临时失败在第 1–4 次后分别退避 5 秒、30 秒、2 分钟、5 分钟。过期/撤销/消费证明和次数耗尽者不再发送。
3. claim 前及网络发送前检查实例 guard、RecoveryMode 与对应有效能力；失效停止新发送。发送前普通只读复查证明、当前 state/generation 和 DB 时间下至少剩余 25 秒的 lease；不满足则不启动 IO，使用原 generation CAS 交还/终结，不延长已过期旧 claim。网络 context 还须受剩余 lease 减 5 秒约束。检查不持有 subject/user/token 锁，也与网络不原子；进程长暂停等情况仍可能产生旧尝试，不能声称 lease/CAS 可以在 SMTP 外部 fence 发送。撤销后可能仍收到旧邮件，但旧证明不能消费。关闭能力/恢复模式时保留队列并暂停，TTL 不延期。
4. main的邮件生命周期在正常SIGTERM时取消SMTP和新工作，并等待worker退出；已经开始的单活Guard检查使用独立最长1秒上下文完成，不能将正常邮件退出的取消当作ownership失败而误取消仍在排空的HTTP。真实检查失败仍按原规则fence实例，不缓存或复用旧guard结果。SMTP 单次总超时 20 秒，含 10 秒连接/IO 上限；必须可取消、退出关闭连接。网络调用在事务外，成功仅指 SMTP 接受（DATA 最终成功响应），不是进入收件箱。
5. ACK 使用 `(id,state=sending,claim_generation)` CAS；旧 generation 不能改写新 claim。outbox ID 生成稳定 `Message-ID`，重试同一条记录及同一 token，不能靠重发生成新证明。
6. SMTP 已接受与数据库 ACK 之间没有原子提交。进程崩溃或超时后可能重复投递，同一 Message-ID 也不保证下游去重；承诺是可恢复的至少一次 SMTP 尝试、令牌至多消费一次，**不承诺邮件 exactly-once 或最终一定送达**。
7. 临时网络/SMTP 4xx 可在限额内重试；永久 5xx、非法配置/密文或不可用解密 key 进入 dead；错误只存固定枚举，不存服务器原文。密钥轮换需保留仍待处理 ciphertext 所用旧 key；移除 key 前清空或完成队列，不能静默跳过解密失败。
8. 第 5 次 claim 后崩溃而未 ACK 的 sending，在 lease 到期后由有界清理转 dead 并擦除 payload，不能因 claim 查询筛掉 attempt_count=5 而永远悬挂。dead 仅表示不再尝试，不代表确定未投递；最后一次可能已被 SMTP 接受。sent/dead/cancelled 即清空 payload_ciphertext；仍可重试的 sending 崩溃后保持密文供恢复。
9. Guard有效时每分钟有界清理；功能off或Recovery只暂停签发/消费/claim/发送，仍可擦除自然失效或终态秘密及过保留期元数据，不能主动消费/撤销活跃proof；Guard失败停止全部DB与网络工作。固定按 **subject → challenge → outbox** 顺序获取行锁，多个同类 ID 稳定排序；可以先无锁取候选，不得先锁 outbox 再回头锁 challenge/subject，也不锁 user/tenant。清空过期注册邮箱及终态 payload，保留脱敏元数据 7 天后受控清理。主体连续 24 小时未请求且无有效证明可清理；固定窗口计数不在有效小时内丢失。worker 仅锁 outbox，ACK 不再锁上游身份行。
10. offline `rewrap-secrets` 必须覆盖两种新密文，并保留 purpose/id 绑定、token、收件人及其他明文内容完全一致；在原维护单实例锁内逐行完成，不与 SMTP worker 并行。新密文重包使用既有 owner/迁移角色的维护权限，命令必须在开始写入前校验权限并在不满足时安全退出；不得给线上应用角色通用的 payload 替换例外。数据库约束限制维护权限，工具负责校验解密后重包内容等义，不声称数据库能在没有主密钥时证明明文相等。旧 key 移除前验清所有新旧表密文，保留备份所需 key。AUTH_SECRET 轮换不迁移旧摘要，按身份恢复运行手册明确使未完成 proof 失效；不能把它描述为透明无损轮换。

发送和清理不会调用 supplier/model endpoint、签发新 token、补发资金或重跑模型请求。单活动实例仍保留；outbox lease/CAS 只解决恢复与防旧 ACK，不等于 025 HA 已验收。

## 8. 浏览器与未知结果

邮件分别指向 `/register/complete#token=<raw>`、`/password/reset#token=<raw>`。GET 只打开页面，不进行证明交换或自动 POST。应用在创建 Vue Router/history **之前**从 fragment 一次读入独立内存并立即用干净 URL 与不含该 fragment 的历史状态执行 `history.replaceState`；只清地址栏而保留 `history.state.current` 不合格，后续导航不得重新写回原 token。仅匹配两条完成路径及严格 `#token=<canonical>` 格式；错误/非目标路径也清除邮件 fragment，不把其存入 router query。页面随后只能一次领取当前路径的内存 proof；不写 storage、analytics、console 或第三方资源。丢失内存后需重新打开邮件，不能从浏览器持久缓存恢复秘密。

注册/恢复只有用户显式提交才 mutation。成功/结果不明的“去登录核查”是显式操作：先清客户端缓存会话及旧loadMe epoch，再导航登录，不自动POST logout、不自动登录，旧响应不得把用户送回工作台。成功显示需要登录；恢复页同步说明所有设备会话已退出、API Key 仍需单独管理。无效只给统一证明错误，不显示是否有账号/是否曾消费。网络、5xx 或非法响应进入结果不明，禁止自动重放或换 token；用户可显式去登录验证新密码或重新请求邮件，不能把再次 400 当作前一次一定失败。

页面 route/关闭/账号切换使用独立 epoch，晚回不覆盖新表单、不自动登录。表单清理密码/MFA/token，提交时冻结本次 DTO；不把敏感输入放进 URL、全局长期 store 或错误对象。

## 9. 后续接口依赖

| 后续编号 | 依赖 021 | 本切片明确不做 |
| --- | --- | --- |
| 022 支付/兑换 | 已登录 user、当前 tenant、active membership 和服务端权限；邮件证明只辅助身份 | 支付商密钥、订单、收款主体、验签/回调、入金或兑换权益 |
| 023 周期预算/订阅 | 稳定 tenant/user，未来支付的独立权益来源 | 周期重置、续费、赠额、现金/套餐消费顺序或预占迁移 |
| 024 媒体/异步 | 现有会话/Key 认证、tenant 所有权 | 任务提交/回调、取消、结果保存和媒体计量 |
| 025 HA | durable challenge/outbox 可作为输入 | 多活、分布式 IP 桶、跨节点 SMTP fencing 和分区一致性承诺 |

真实 SMTP 收件、域名配置、SPF/DKIM/DMARC、投递信誉与生产反滥用是独立外部门禁。本地 SMTP fixture 仅证明协议、加密、状态机及恢复，不是生产邮件认证。

## 10. 登录入口初始化与返回路径（2026-11-01补充冻结）

022-B3真实浏览器联验发现首次 /me 未认证响应与App全局事件可能竞争初始路由：进入 /login?redirect=本地路径 时不得因初始化401将它改成无query的/login。入口先安装router并等待router.isReady()再挂载App；初始会话和路由判定在全局UI事件监听器安装前完成。已在/login时收到unauthenticated，只清会话、保留当前登录入口及redirect，不重复导航；私有页面失权沿既有登录跳转。成功登录仍只使用现有单斜杠本地redirect校验，不扩大外部跳转、MFA或邮件证明权限。验证用真实未登录入口与可延迟/me的浏览器检查，确认登录前query保持、密码登录后进入原支付页；前端构建和原身份测试继续通过。

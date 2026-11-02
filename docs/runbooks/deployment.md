# Compose / 1Panel 部署与升级

交付的是可运行开发版，尚未在当前机器实际构建/启动容器。Docker daemon不可用，下面是待目标环境验收的部署路径；不触碰本机其他Docker/Colima环境。正式发布前须完成[剩余验收](../verification/acceptance-execution.md)，特别是目标容量与选定真实渠道的usage契约。

当前源码要求完整 **001–022** 迁移，并默认 `PAYMENTS_MODE=off`。下文标注旧规格编号的升级记录是历史说明；不能把旧迁移上限用于当前版本。Compose 配置渲染已验证，容器、1Panel 和正式供应商仍按目标环境分别验收。

022-C0 的停服维护已有库级 `Attest(ctx, snapshot)`、`Permit.Verify(ctx)` 与受控 ReadLease，绑定原 Gate/Snapshot、实际维护身份和同事务前驱；真实 PostgreSQL 专项与历史 CI 结果分别记录。维护读取集中在独立 `internal/maintenancescan`，普通billing/PB/main及领域测试不依赖维护或catalog；真正维护消费者仍完整认证固定catalog。单租约68表业务来源读取已实现，钱包全序声明另有纯检查，实际 lot 重放、生产维护CLI、basis与epoch开账仍在后续。普通main、Compose和1Panel没有这些维护装配或新增正式迁移；部署继续使用001–022，C0规格SQL不属于安装或升级步骤。

维护依赖隔离的旧 `f572bbd` / CI run 41359784155 观察期限失败保留原结论；后续 `824e4e5` 精确实现提交十组CI全部成功，进程、真实reader与整仓重新验收见[隔离记录](../verification/maintenance-integration-isolation.md)。新68表范围另见[读取记录](../verification/complete-source-reader.md)。这些本地及CI证据不扩大为容器、1Panel、供应商或生产验收；权限与接线边界见[维护架构](../architecture/maintenance-gate.md)。

## 首次安装

使用专用服务器/目录检出完整仓库，安装Docker Compose v2，以下命令均在仓库根目录执行。`migrate`使用 `build: .` 构建本地镜像，`provision`和app复用该镜像，因此需要根 `Dockerfile`、Go/Web源码及 `deploy/init-roles.sh`，仅保存Compose文件和脚本不足以首次构建；当前流程不假定存在已发布的预构建镜像。生成环境后配置HTTPS域名，文件权限保持0600；运行密钥与日常数据库访问权限分开保管。备份脚本会生成包含明文keyring的受保护完整包，必须存放于加密介质，详见恢复手册。

```sh
go run ./cmd/moni-aigate init-env --out .env
# 编辑PUBLIC_ORIGIN=https://your-domain，COOKIE_SECURE=true，PORT=8080
# MFA默认可选；若要求平台/财务账号先绑定，添加MFA_ENFORCE_PLATFORM=true
# .env由Compose读取，不要提交到Git。
docker compose config --quiet
docker compose build
# bootstrap 会等待迁移与受限角色授权依赖完成；此时尚未启动对外app。
docker compose run --rm -T app bootstrap --email admin@example.com --password-stdin
# 首个管理员创建成功后，再启动单活动app。
docker compose up -d app
```

`MFA_ENFORCE_PLATFORM=false`（未设置也为false）允许未绑定账号密码登录；`compose.yaml`将该变量传给app。设为true并重建app容器后，未绑定的平台管理员/财务账号必须先设置验证器；已经启用MFA的账号始终须第二因子，关闭强制策略不会取消个人绑定。敏感操作近期密码验证、权限、双人审批仍生效；本地二维码和手动绑定入口均保留。

不带数据库URL的Compose配置会在容器内部建立owner/app/projection连接。`deploy/init-roles.sh`仅在全新PG卷初始化运行；已有卷改密码须通过数据库角色维护同步，不能只改.env。数据库不映射公网端口，app仅映射宿主机127.0.0.1。若机器不安装Go，可在有Go的开发机构建/生成私有env后安全传入；本项目不把示例MASTER_KEY当作可用秘密。

1Panel可使用“容器→编排”导入根 `compose.yaml`，编排项目目录与 `build: .` 的构建上下文均须指向完整仓库根目录；若导入界面把YAML保存到其他目录，须先将完整检出放入实际项目目录或按目标1Panel版本正确指定源目录，不能只复制YAML和初始化脚本。确认相对挂载路径 `deploy/init-roles.sh`、根Dockerfile/源码及私有 `.env` 均可被解析，先在测试目录验证变量注入、构建和依赖条件，再使用正式卷。当前容器运行与1Panel实装仍待目标环境验收，没有声称已上架1Panel应用商店或修改1Panel本体。

在1Panel网站/OpenResty建立HTTPS反向代理到127.0.0.1:8080，合并[nginx.conf](../../deploy/nginx.conf)。SSE需`proxy_buffering off`，`proxy_next_upstream off`，超时覆盖300s总deadline并留排空余量。其他CDN/负载均衡也应关闭生成POST自动重试，避免网关之外重复生成。安全Cookie和Origin必须与最终访问域名一致。

反代部署还须设置`TRUSTED_PROXY_CIDRS`为应用实际看到的边缘代理源CIDR。默认空表示不信任任何转发头；单机直接反代可显式设`127.0.0.1/32,::1/128`。容器端口转发后源地址可能为bridge/NAT地址，须在目标环境核实并尽可能使用单地址CIDR，不能照抄环回或设全网。该配置只影响客户端身份解析，不授予模型、资金或管理权限；Compose会传给app。

随附nginx示例用`proxy_set_header X-Real-IP $remote_addr`覆盖客户端输入，并清除X-Forwarded-For。应用仅接受受信peer的单一合法X-Real-IP，重复/链/非法头退回peer，忽略XFF。登录限流和015 Key来源白名单共同使用这一来源规则；IPv4-mapped传输地址转普通IPv4，zone/非法地址视为未知，受限Key遇到未知来源拒绝。若边缘前还有CDN，先独立核实CDN来源/真实IP还原配置；不要将未经验证的公网header直接透传为身份。

启用Key来源限制前，核实应用看到的peer、代理覆盖header和目标客户端出口地址；`TRUSTED_PROXY_CIDRS`与客户白名单是两个不同边界，信任某个代理不等于自动允许它后面的所有客户端。客户策略为空表示不限来源，受限策略最多32项IP/CIDR且不接受全网`/0`；配置、GET只读核查和CAS编辑见[开发手册](development.md#客户key来源策略015)。请求在鉴权时固定来源，预占/尝试/派发锁内复查当前策略；`sending`提交后已获发送授权，后续收紧不撤回请求或阻断资金收尾。

`/health/live`只说明进程存活；`/health/ready`检查实例与数据库、返回recovery_mode；recovery_mode=true仍可登录只读/核对，但收费关闭。metrics仅按实际传输peer接受环回来源，不用转发头授予访问；反向代理必须保留`/metrics`的403规则，因为代理到应用的连接可能来自本机，单靠应用peer检查不能区分代理后的公网用户。

## 升级与回滚

1. 先确认上一版没有需要保留的未推送代码，备份数据库及对应keyring/AUTH_SECRET；记录镜像摘要与迁移checksum。
2. `docker compose stop app`，等待最长320s排空；不同时启动新旧app。若进程强杀/失锁，新版启动先等待旧请求持久执行窗口结束，再接新请求，旧请求由恢复worker裁决，不能重放生成。
3. 构建新镜像；`docker compose run --rm migrate`、`docker compose run --rm provision`，完成后启动app。迁移以owner运行，serve只读校验checksum且不自动改表。
4. 检查ready、账本平衡、只读目录与控制台、一个预算内的受控真实渠道样例，监控未知usage/冻结/成本待核对。真实样例预算由经营者自己确定，本次未发生。
5. 只在数据库schema仍兼容时回退旧镜像。涉及不兼容数据恢复时走[恢复手册](backup-restore.md)，不能覆盖数据库后立刻开放充值/收费。

本版没有自动down migration。当前完整schema为001–022，后续升级只能追加新迁移，不编辑已应用文件。以下010–013为对应阶段的历史变更说明。010为Key新增规范来源列表和独立`access_policy_revision`，并为请求新增不可更新的`client_ip INET`；旧Key来源默认不限、旧请求来源为NULL，不伪造历史来源。011为Key增加默认60RPM/burst20/并发16的三元组及独立`limit_revision`，数据库限制范围并防止直接指定版本绕过。012追加独立`configuration_revision`，只随名称/模型/地域/累计额度/期限实际变化递增，不改写历史字段，也不为旧字段增加会阻断资金更新的严格CHECK。013增加不可变路由成本策略、binding两维采购单价上限及成本INSERT的渠道同步触发器；旧路由manual/allow，历史同刻报价保留并确定性选择，新同刻报价拒绝。成本发布与已有渠道management_revision/CAS共同生效，调价不追溯改变sending后的采购快照。三版本及spent/held互不干扰，app/projector不因此扩大权限。先排空旧实例，再以owner完成迁移及provision，受限serve只读Verify。财务历史不会因应用回滚删除；015及016最终执行状态分别见[来源报告](../verification/key-access-policy.md)与[运行限额报告](../verification/key-runtime-limits.md)，017专项/全量及隔离浏览器、58123迁移/角色/资源核对已通过限定本地验收，见[基本配置记录](../verification/key-configuration.md)。018实现、013迁移后的受限预览/29项静态资源与权限核对见[成本路由记录](../verification/cost-aware-routing.md)；本地通过不等同目标容器/1Panel验收。

## Key运行限额与进程生命周期（016）

Key可配置RPM 1..6000、burst 1..200、并发1..16，平台固定6000RPM/burst200、租户300RPM/burst40及全局100/租户32并发继续生效。运行限额API/控制台使用独立CAS，配置下次被新调用准入观察到时才切换，修改不会重新补满旧桶；已获运行许可的排队及在途请求继续，来源与`sending`发送授权边界保持独立。用法见[开发手册](development.md#客户key动态运行限额016)。

运行态仅在单活动进程内，最多4096份Key桶，进程运行期间无TTL驱逐。满时新Key返回503 `limiter_unavailable`，已有Key继续；不实现分布式计数或持久速率历史。重启会清空内存计数并使桶重新满，数据库配置仍保留。升级/故障重启须遵守前面的单实例排空、持久执行窗口与恢复流程，不能通过并行启动实例扩大限额。限流不是资金预算，原硬预占、未知消费不重放、结算/释放/退款与恢复仍必须完成；016浏览器和预览最终验收状态只以[独立记录](../verification/key-runtime-limits.md)为准。

## Key基本配置与生效边界（017）

基本配置使用第三个独立CAS版本，编辑不会换发秘密、清运行桶或重置消费。模型/地域/期限在预占、attempt、派发前按当前配置复查，`sending`后不撤回已获发送授权的调用；结算、释放、退款和恢复继续。下调累计额度须覆盖锁内最新spent+held，0是零预算、null是无累计上限；期限null会移除期限，已过期Key可因此明确重新启用，撤销Key不能恢复。操作、历史字段兼容及未知结果核查见[开发手册](development.md#客户key基本配置017)。周期预算、分组商品、自助商业、正式供应商、容器/灾备、许可与性能门禁仍分别待验。

2026-10-31本机58123已完成012迁移和owner provision后以受限serve启动，ready且非恢复模式；首页及28项assets与最终dist逐字节一致，实际app不是owner/superuser，projector不能读取Key新增字段。这是本地升级核对，不代表容器/1Panel目标环境或生产部署已认证，证据见[017记录](../verification/key-configuration.md)。

## 密钥轮换与故障

022-A兑换券默认关闭：`VOUCHERS_ENABLED=false`与数据库voucher policy共同控制新发行/兑换。启用前配置独立`VOUCHER_HMAC_KEYS`（JSON版本→规范raw-base64url的32随机字节）和`VOUCHER_HMAC_ACTIVE_KEY`，通过私密部署配置注入；不复用MASTER_KEY/AUTH_SECRET，不把真实值放进命令输出或仓库。Compose已透传三项。开启硬上限时启动会检查全部历史券摘要和命令指纹引用的版本，缺任一版本直接拒绝监听，即使DB policy仍off。

轮换采用排空重启并保留全部被引用的旧版本，不能仅切active后删除旧key。关闭硬上限不要求配置HMAC，可GET旧回执并继续已有审批/作废/撤销；所有create/redeem POST（含旧operation重放）409，使用原operation GET核查。Recovery仍拒全部写。券码只有首次确认COMMIT的201显示一次，丢失不能从DB恢复；作废/双审撤销后才明确新建。017本地预览升级与限定验收见[记录](../verification/funding-vouchers.md)，不代表容器或正式支付验收。

- 上游秘密变更：渠道更新创建新credential/channel版本，再发布binding。旧版本在途保留；明确凭据失效会撤销并隔离，不因重启解除；明确model_not_found等机器码仅隔离对应channel_version/upstream_model，新建版本并重新发布才恢复，其他模型不受影响。
- 主密钥变更：保留所有旧key，设置MASTER_KEYS_JSON及新的MASTER_KEY_ID；停app，`MAINTENANCE_MODE=true`以owner执行`rewrap-secrets`，再启动并验证；对应旧备份需要的旧key必须保留。重包保持秘密本身和身份ID不变。
- AUTH_SECRET参与Key/会话/HMAC身份，不能按主密钥重包方式随意更换；丢失将不能验证原Key/幂等摘要，须完整恢复或按专门迁移计划换发。
- DB不可用、专用锁会话失效、权限查询/解密失败均拒绝新派发。HTTP503不证明此前请求未执行，使用request_id核对；不能让客户端循环重发生成。
- 对不明usage只做核对或到期释放，禁止按R/估算量直接扣款。对已结算消费退款走双人订单，不改余额或删分录。

## 024-A经营告警与角色核验

024-A验收版本要求001–019；当前源码已追加至001–022，启动始终校验完整当前迁移，不能用旧版本清单启动新程序。019追加站内告警与两类未决来源部分索引；排空旧实例后，离线owner执行migrate/provision，完成后启动APP及独立PROJECTOR。`OPERATIONS_ALERTS_ENABLED`默认false，Compose已透传；开关只控制评估，恢复模式只读，不开启也须满足新表最小权限。

provision在同一事务配置并核验六表有效权限；serve在worker/HTTP前核验两条真实连接。禁止表/列越权、GRANT OPTION、schemaCREATE、owner、高权属性及SET/ADMIN可提升路径；无害group允许存在。外部group越权由离线运维修正，不自动撤销共享group授权。owner登录后SET ROLE不能视为受限服务连接。失败在监听前终止，不以授予owner绕过。

独立双槽/双连接评估在SIGTERM取消并join后关pool，不占财务恢复或邮件槽。确认、停用和退役不改变来源或资金，不提供外发通知与HA。语义及本地边界见[验证记录](../verification/operations-alerts.md)；正式容器/1Panel和供应商认证仍需目标环境。

## 自助身份配置与支付部署边界

Compose 已透传 `AUTH_REGISTRATION_ENABLED`（默认 false）、`AUTH_PASSWORD_RESET_ENABLED`（默认 true）、`MAIL_TRANSPORT`（默认 disabled），以及 `SMTP_HOST`、`SMTP_PORT`、`SMTP_FROM`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`SMTP_TLS_MODE`（默认 starttls）。启用邮件须同时配置正确 HTTPS `PUBLIC_ORIGIN`、实际 SMTP 地址/端口/发件人、配对用户名密码及供应商要求的 TLS 模式；使用受保护部署环境保存秘密。开关及邮件配置变更须重建 app 容器。默认部署不发送邮件，单独启用注册开关不能绕过有效证明和邮件流程。验证结果见[静态配置记录](../verification/compose-config-025.json)；全局Docker CLI仍缺compose插件；补验使用SHA验证的Homebrew官方隔离Compose 5.6.1，在清空继承环境、显式合成env文件、空DOCKER_CONFIG和不存在的daemon socket下，实际config --quiet/JSON默认及覆盖检查通过，见[渲染证据](../verification/compose-render-025.json)。未读取用户.env或更改全局配置；该结果不代表容器运行、1Panel或正式收件。

`PAYMENTS_MODE=off` 为常规部署默认。022-B3目前仅为 literal `127.0.0.1:55439/moni_test` 隔离本地模拟，要求成对私密 APP/worker manifest、精确 listener 与环境证明；常规 Compose 不透传这些文件或 worker DSN。不能用容器内 postgres 服务名替换目标后绕过证明。live 在连接前拒绝；带沙箱历史而缺原证明也拒绝启动。支付入口的已验范围见[应用支付证据](../verification/payment-application.md)，不是生产收款能力。来源 lot 维护、原路退款和后续生产方案仍按各自 SDD 门禁推进。

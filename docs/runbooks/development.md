# 本地开发与首次运行

本版要求 Go ≥1.26、Node 22、PostgreSQL 16。使用独立数据库，应用仅一个活动实例；不使用SQLite或Redis代替资金事实。

当前源码迁移范围为 **001–022**。下文带规格编号的升级与测试记录保留各阶段的历史范围；新部署始终使用当前完整迁移及 `provision`，不按历史清单裁剪。

```sh
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
./bin/moni-aigate init-env --out .env
```

生成文件权限0600，不覆盖已有文件。`init-env`只生成配置和随机秘密，不创建外部数据库或LOGIN角色。先按下节初始化独立空库，再补充三个数据库URL。自定义schema/角色可用operations.ProvisionFor嵌入集成，标准CLI使用public/moni_app/moni_projection。

## 建立本地数据库与角色

以下步骤仅用于 PostgreSQL 16 的专用空环境，要求 `moni` 数据库和三个同名角色尚不存在。若名称已存在，先核实环境及所有权，不覆盖、删除或复用现有业务库。使用安装 PostgreSQL 时已有的初始化管理身份连接；下例 `postgres`、主机和端口须按实际安装替换。该身份负责建库/建角色，运行应用时不使用它；三个项目角色均不授予 `CREATEDB`、`CREATEROLE` 或其他高权属性。

```sh
psql -X -W -h 127.0.0.1 -p 5432 -U postgres -d postgres
```

`-W`提示输入管理密码，不将密码写入命令参数或shell历史。进入psql后逐行执行；`\password`会交互提示并确认密码，不把明文写入SQL或psql历史。分别保存三个角色的独立强密码。

```sql
\set ON_ERROR_STOP on
CREATE ROLE moni_owner LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE moni_app LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE moni_projection LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
\password moni_owner
\password moni_app
\password moni_projection
CREATE DATABASE moni OWNER moni_owner;
\connect moni
SELECT datname, pg_get_userbyid(datdba) AS owner FROM pg_database WHERE datname = current_database();
SELECT nspname, pg_get_userbyid(nspowner) AS owner, nspacl FROM pg_namespace WHERE nspname = 'public';
\quit
```

使用具有创建角色、创建数据库及指定新数据库owner所需权限的管理身份，例如新安装时的数据库超级用户；不要为通过初始化而提高 `moni_owner`、app或projection的属性，也不授予三者相互成员关系。标准新建 PostgreSQL 16 数据库的 `public` schema由 `pg_database_owner`拥有，数据库owner `moni_owner`通过其隐含成员身份管理该schema并创建迁移对象；保留这一所有权关系。默认PUBLIC有USAGE、没有CREATE。若实例模板被定制或来自旧版升级，先核实schema所有权与ACL；不能以向app/projection授予schema CREATE或改为它们所有来绕过核验。

用安全编辑器将下面三行补入私有 `.env`，把占位符换成对应角色密码的URL百分号编码值；文件权限保持0600，不提交到Git，也不在shell命令中输入真实URL。这里的 `sslmode=disable`仅用于本机开发，远程数据库按环境配置TLS。

```dotenv
DATABASE_URL=postgres://moni_app:APP_PASSWORD_URL_ENCODED@127.0.0.1:5432/moni?sslmode=disable
PROJECTION_DATABASE_URL=postgres://moni_projection:PROJECTION_PASSWORD_URL_ENCODED@127.0.0.1:5432/moni?sslmode=disable
MIGRATION_DATABASE_URL=postgres://moni_owner:OWNER_PASSWORD_URL_ENCODED@127.0.0.1:5432/moni?sslmode=disable
```

`DATABASE_URL`仅使用受限app角色，`PROJECTION_DATABASE_URL`使用独立projection角色；owner连接仅用于离线迁移及 `provision`。`migrate`建立当前完整schema后，`provision`再授予并核验表/列等所需权限，同时撤销PUBLIC的schema CREATE。不要手工给app或projection广泛授权替代这一步。

## 迁移、初始化管理员与启动

完成数据库初始化与私有配置后，在仓库根目录执行：

```sh
set -a
. ./.env
set +a
./bin/moni-aigate migrate
DATABASE_URL="$MIGRATION_DATABASE_URL" ./bin/moni-aigate provision
./bin/moni-aigate bootstrap --email admin@example.com --password-stdin
./bin/moni-aigate serve
```

`bootstrap`从stdin读一行密码，长度12–72字节；只允许空用户库创建首次管理员。交互运行时输入不会被此程序隐藏，建议通过密码管理器/权限0600临时文件的stdin提供，不在命令参数或shell历史写密码。数据库角色须先按[建立本地数据库与角色](#建立本地数据库与角色)由初始化管理身份创建；应用不具有建表/改迁移权限。Compose/1Panel使用独立安装流程，见[部署](deployment.md#首次安装)。

升级已有数据库时，先停止接单、排空并停止旧实例，再使用新程序执行`migrate`及`provision`，完成后才启动`serve`。011新增007模型发现迁移，012新增008凭据池迁移及版本创建封口，013新增009渠道管理revision/批量回执迁移，015新增010 Key来源策略/配置版本及不可变请求来源，016新增011运行限额/独立版本，017新增012基本配置独立版本，018新增013成本策略/两维上限与报价发布同步，019新增014共享采购账户与预算，020新增015公开目录标记/独立版本，021新增016自助身份/初始化/证明与邮件outbox；022追加017共同入账/双审券，023-A追加018单租户价格组/精确域唯一性/不可变请求快照；023-A阶段迁移范围为001–018。`migrate`从`MIGRATION_DATABASE_URL`选择owner连接，`provision`也须显式使用owner连接为新表/字段配置最小权限：

```sh
./bin/moni-aigate migrate
DATABASE_URL="$MIGRATION_DATABASE_URL" ./bin/moni-aigate provision
./bin/moni-aigate serve
```

上述命令使用已加载的私有环境配置。024-A追加019经营告警、完整事件链和来源索引；随后020周期预算、021本地支付资金和022运行证明将当前完整范围追加至001–022。`serve`核对版本与checksum，并在worker/HTTP启动前只读核验APP/PROJECTOR的告警有效权限，包括继承、SET/ADMIN可提升路径、列授权和owner/高权。它不会自动迁移或修复授权；缺少迁移应先完成升级，不能给应用角色增加owner权限绕过。

本机HTTP预览设置`PUBLIC_ORIGIN=http://localhost:8080`、`COOKIE_SECURE=false`、`LISTEN_ADDR=127.0.0.1:8080`；生产HTTPS设置Secure=true。`MFA_ENFORCE_PLATFORM`缺省false，未绑定的管理员/财务账号可密码登录并测试业务；无需为联调先设置验证器。需要时在“账号安全”点击开始设置，使用验证器扫描本地二维码或手动密钥，保存恢复码并输入6位动态码后启用。设为true并重启可要求未绑定的平台/财务账号先完成绑定；已启用者始终须验证码，不能通过改false跳过。控制台仍需分别创建财务制单员和审批员；近期密码再认证及双人审批规则不变。

首次经营顺序：创建有明确usage契约的模型profile → 创建上游渠道 → 发布完整binding/revision → 追加售价 → 创建客户并授权模型 → 财务制单/另一账号核款 → 客户创建Key → 调用。渠道创建/改密后须重新发布binding才能用于新版本；未知成本先保留待核对，不填0。

```sh
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer $MONI_API_KEY"
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $MONI_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"your-public-model","messages":[{"role":"user","content":"Hello"}],"max_tokens":128,"stream":true}'
```

示例变量通过私有环境注入；真实渠道会产生供应商费用。本次验证只调用本地Mock，没有用这些示例访问真实供应商。

## 开发验证

全仓数据库测试要求独立 PostgreSQL 16 测试集群：实际服务端与 URL 均为 `127.0.0.1:55439/moni_test`，实际 LOGIN 为 `moni_test`，且为测试专用超级用户。部分 fixture 会创建无密码的受限 LOGIN；临时测试集群需允许这些角色的 loopback 连接，当前 CI 显式使用 trust。此配置只用于隔离测试，不用于应用部署。

已验测试 profile 的 `max_connections=200`，`fsync`、`synchronous_commit`、`full_page_writes` 均为 `on`。当前M/Q固定v1结构库存要求数据库会话默认 `TimeZone=Asia/Shanghai`，用于严格反解析文本核对；单设Go进程TZ不能代替服务端设置，复现及CI修订见[时区验证](../verification/github-ci-second-run.md)。以下 URL 适用于已经按上述规则准备的无密码本地专库；有测试管理员密码时从私有环境加载相应 URL，不将秘密写入脚本。测试会创建并清理随机 schema 和角色，不要指向日常开发、预览或生产库。

```sh
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
make check
make verify-mvp
```

`make verify-mvp` 的Go阶段以 `-p 1` 按包串行使用同一专用数据库，完整执行所有包、race与包内显式并发，保留原测试期限和skip条件。它会比跨包并发执行更久；通过不代表并发启动余量或容量验收。`make test` 与CI独立runner继续使用各自执行合同，不能由本地串行结果推断远端状态。

直接运行 `go test` 而未设置 `TEST_DATABASE_URL` 会跳过数据库测试；`make verify-mvp` 则在变量缺失时直接失败。默认测试不启用 C0 等独立 opt-in 专项，这些专项及固定 catalog profile 按各自验证合同执行。这样的跳过结果不能替代完整验收。CI 环境及四类包归属、十组执行合同见[CI 验证](../verification/github-ci-profile.md)，第三轮与T2发布精确源码结果分别见[第三轮记录](../verification/github-ci-third-run.md)、[T2发布记录](../verification/github-ci-authority-run.md)；容量测试需显式开关，复现见[容量报告](../verification/capacity.md)。

项目没有内置管理员账号、默认密码或自动演示余额。各开发环境应使用自己的私有配置和独立测试数据库；不要将账号、恢复码、Key 或数据库文件提交到仓库。

### 资金来源维护库（022-C0）

`internal/maintenancetrust` 已实现 Gate、私有单次 RR Snapshot，以及库级二参 `Attest(ctx, snapshot)` 和 `Permit.Verify(ctx)`。T2 从 Snapshot 内部持有的安装证明出发，在同一原始事务内核对固定迁移 manifest/history、实际维护 LOGIN、全体 runtime 的新鲜停服采样、空安装与完整前驱；Init 最后按账户 ID 的 C 序完整锁行。Permit 复制共享撤销，取消、失败或开始终结后失效，不暴露原连接、事务或任意 SQL 回调。具体合同见[真实维护授权](../../specs/022-self-service-payments/c0-authority-contract.md)。

T0/T1 的历史专项结果见[Gate](../verification/maintenance-gate.md)和[Snapshot](../verification/maintenance-snapshot.md)；本轮 T2 的[真实 PostgreSQL race 专项](../verification/maintenance-authority.md)已通过，333.879秒、89顶层/832父子终态全部PASS；第三轮CI仍只证明其精确旧源码。专项使用独立 `C0_MAINTENANCE_DATABASE_URL` 显式选择上述专库；ordinary public profile 会创建并精确清理随机数据库，不使用父库的 public 作为安装目标。

维护装配位于独立 `internal/maintenancescan`，单向依赖billing、periodicbudget及maintenancetrust→fundinglotcatalog；普通billing/PB/main及领域测试不依赖维护或catalog，依赖门禁同时检查生产与测试图。真正维护消费者仍在初始化时完整认证固定catalog，普通服务不为未调用的维护能力支付初始化成本。Compose与1Panel没有维护装配，也没有生产维护CLI、受控全来源+lot重放、basis或epoch开账入口；独立CheckWalletLotsRows只接受所给原行，不发行数据库或资金权限；规格目录中的C0 SQL尚非已追加的应用迁移，常规安装继续执行完整001–022与`provision`。

T3首片提供`Permit.BeginScan`、封闭`ReadLease`与账本/PB维护reader。领域包保留原public入口及唯一固定读取算法；窄raw-facts接缝只返回不可信局部事实，不接受或发行authority。maintenancescan仅接受真实`*ReadLease`，私有adapter执行Require→固定schema SELECT与算法→Finish后才构造绑定原Permit的私有结果，不接收外部provider、报告或通过位。两个摘要仍仅为`supplied_ledger_rows`和`periodic_budget_rows`，不发行全来源或开账许可。独立专项使用同一专库的`C0_MAINTENANCE_DATABASE_URL`，重PG测试必须串行；默认`make verify-mvp`不自动打开此专项。实际命令及取消/资源边界见[T3专项](../verification/maintenance-read-lease.md)，整仓结果见[集成记录](../verification/maintenance-read-lease-integration.md)。当前没有可调用的生产维护CLI，请勿据这些库接口自行跳过完整来源/basis/epoch门禁。

依赖隔离按[SDD§7](../../specs/022-self-service-payments/c0-read-lease-contract.md#7-ci-启动回归后的维护集成隔离根冻结)修复远端启动回归。此前本地串行整仓1172.881秒成功保留原版本；精确f572bbd/CI run 41359784155在billing-0原12秒helper、server-2/3原10秒main和other周期核验CLI原60秒观察失败，不能用本地通过替代。原进程、真实双reader/资源界、无权威core、整仓和新精确提交CI须重新验收；本次不修改固定root、SQL/ACL、五秒issuer预算、原测试期限或skip条件。

## 客户Key来源策略（015）

客户在“API密钥”创建或查看Key时，可设置来源白名单，每行一个纯IPv4、IPv6或CIDR。最多32项、原始字符串总和4096字节；服务端将地址转为`/32`或`/128`，CIDR屏蔽主机位后排序去重。留空表示不限来源；旧Key保持空列表。域名、URL、端口、zone、IPv4-mapped配置、空项和全网`/0`会被拒绝。这是客户请求来源限制，与上游渠道`allowed_cidrs`的私网出口授权分开配置。

owner可编辑本租户全部Key，member仅可编辑自己的Key，billing_viewer仅可查看自己的策略。已过期Key仍可编辑来源，但不会恢复期限；已撤销Key可读、不可编辑。保存仍需当前权限、CSRF、MFA策略、近期再认证和实例/恢复门禁；MFA默认可选。

`GET /api/keys/:id/access-policy`返回`id`、`allowed_client_cidrs`及字符串`access_policy_revision`，供打开编辑器、冲突和未知结果时只读核查，无需近期再认证。`PUT`同路径完整替换来源列表，须带从GET取得的`expected_revision`和明确数组；`[]`清空，null/遗漏非法。示例中的`"1"`须替换为刚读取并复核的实际版本：

```json
{"expected_revision":"1","allowed_client_cidrs":["192.0.2.0/24","2001:db8::1"]}
```

新建`POST /api/keys`增加同名数组，遗漏或null等同不限来源；明文Key仍仅创建时展示一次。GET/列表不会返回秘密、摘要或请求来源。同规范列表且版本仍匹配是no-op，不增加版本或策略审计；旧版本即使提交相同内容也返回409。配置版本只随来源变化递增，消费spent/held更新不会让编辑失效；015不提供动态RPM/burst/concurrency或其他Key属性编辑。

409后先只读刷新并核对当前策略，再明确开始新编辑。网络失败或5xx是提交结果未知，不自动重发；只读观察当前值不能证明一定由自己的写入产生，本接口没有持久命令回执。关闭窗口、切换账号/租户或离开页面后，表单和一次性秘密应清除。

来源在API Key鉴权时捕获一次，默认使用连接peer；反代须按[部署手册](deployment.md)配置`TRUSTED_PROXY_CIDRS`，只有受信peer的单一合法`X-Real-IP`参与，XFF不参与。客户端正文或任意自报header不能指定持久来源。所有Key协议入口和模型目录先筛选；预占、创建attempt、派发前再按最新策略复查。历史请求来源为空时，后来收紧会拒绝新attempt/派发；未发送请求按原规则释放冻结。

`PrepareDispatch`提交`sending`是发送授权同步点。之后收紧不撤回已获准发送的请求、不强杀SSE，已有可信量结算、退款、释放和恢复继续完成。完整规格及实际全量、浏览器和预览执行状态见[015验收](../verification/key-access-policy.md)，不能沿用014通过记录。

## 客户Key动态运行限额（016）

在“API密钥”创建表单或现有Key的“运行限额”中设置三个整数。来源策略和运行限额分别编辑，互不替代；累计消费额度与运行限流也是不同约束。

| 字段 | 默认值 | 可配置范围 | 含义 |
|---|---|---|---|
| `rpm_limit` | 60 | 1..6000 | 每分钟补充的Key令牌数，不是固定分钟窗口 |
| `burst_limit` | 20 | 1..200 | 桶容量，不与RPM相加，不要求小于等于RPM |
| `concurrency_limit` | 16 | 1..16 | 此Key同时持有的运行许可上限 |

创建API允许遗漏，分别采用默认值；显式null、0、负数、小数、字符串或越界值均拒绝。控制台清空数字不得当作0或默认值提交。模型目录GET不消耗Key运行令牌；六种收费协议共用Key桶，在协议/候选准备后、预占前进行一次运行准入。容量拒绝不扣Key令牌，不产生预占、attempt或收费；获运行准入后令牌不回退，安全跨渠道回退继续使用原许可，不重复扣令牌。

`GET /api/keys/:id/runtime-limits`返回`id`、三个配置值及字符串`limit_revision`，不返回实时余额/在途数、秘密或来源。`PUT`同路径完整替换三个数字和`expected_revision`，全部必填；示例版本`"1"`须替换为刚读取并复核的实际值：

```json
{"expected_revision":"1","rpm_limit":120,"burst_limit":20,"concurrency_limit":8}
```

GET与015保持相同可见性：当前owner可读本租户全部，member/billing_viewer只读自己的Key，撤销/过期仍可读，无需近期再认证。PUT仅owner/member且需当前权限、CSRF、MFA策略、近期再认证、ready/Guard和非恢复模式；撤销Key不能编辑，过期Key编辑也不会延长期限。当前版本同三元组no-op不新增版本/审计，旧版本即使同内容仍409；运行限额版本与来源`access_policy_revision`独立，spent/held不改变任一配置版本。

409、网络失败或5xx后先只读刷新核查，再明确开始新编辑，不自动重发。GET只是当前观察，没有命令回执，不能证明某次未知提交由自己成功完成。读取不能用默认值掩盖字段缺失；关闭或切换账号/租户/Key后清理表单与秘密，晚回不能覆盖另一种配置。

服务端锁定当前有效身份与Key后读取最新限额，再执行内存准入。修改在下一次新调用准入观察到时切换：先按旧速率补充到观察时刻，再调整原桶；降低burst截断余额，提高burst不赠令牌，不能通过反复编辑获得新满桶。并发下调不清零在途计数，当前许可少于新上限才允许新增；已获运行许可的排队或在途SSE继续，原来源/撤销/模型/资金复查及结算、释放、退款、恢复保持有效。

平台速率仍固定6000RPM/burst200，租户固定300RPM/burst40；全局并发100、租户32和渠道/凭据容量共同约束调用。Key配置较高不保证可突破这些上限，平台/租户动态配置不属于016。

单活动进程最多4096份Key桶，运行中不按TTL驱逐或按revision重建；桶满时新Key返回503 `limiter_unavailable`，已有Key继续。速率/并发拒绝返回429 `rate_limited`/`concurrency_limited`。计数只在内存，重启后桶重新满，持久配置保留；继续遵守单活动实例和排空/恢复门禁，不同时运行多个实例。基本配置编辑见017；周期预算及原生产与性能门禁仍后续推进。016合同及最终浏览器/预览状态见[规格](../../specs/016-key-runtime-limits/spec.md)与[验收](../verification/key-runtime-limits.md)。

## 客户Key基本配置（017）

2026-10-31，017限定本地验收已通过：专项/全量真实PG race、107项前端测试/strict构建、vet/build、隔离浏览器及58123的012迁移/受限角色升级与资源核对，见[017规格](../../specs/017-key-configuration/spec.md)与[记录](../verification/key-configuration.md)。在“API密钥”打开“基本配置”，可编辑名称、模型范围、地域、累计消费上限和期限。模型/地域始终与租户及所属成员授权取交集，`global`是精确地域标签；`*`须单独一项且不授予额外权限。

`GET /api/keys/:id/configuration`仅返回`id/name/scopes/allowed_regions/lifetime_limit/expires_at/configuration_revision/spent/held`九字段。金额和版本均为十进制字符串；`spent/held`只是可变化快照，不属于配置版本。`PUT`同路径完整提交以下六字段，任何遗漏均拒绝；null仅用于明确移除累计上限或期限：

```json
{"expected_revision":"1","name":"client-key","scopes":["*"],"allowed_regions":["global"],"lifetime_limit":"1000000","expires_at":null}
```

示例金额单位为微元，`"1000000"`是¥1；`"0"`是零预算，null表示无累计上限。控制台以CNY输入、最多6位小数并无损转换；额度下调在Key锁内保护最新`spent+held`，不足返回409 `key_budget_below_usage`，不会重置计数或改变钱包。当前版本同五元组no-op不新增版本/审计，旧版本即使同值仍409；`configuration_revision`只随五元组实际变化，与来源/运行版本分别维护，资金更新不使编辑失效。

只有实际修改的字段按新规则规范化：名称trim后1..100 UTF-8字节且无控制字符；模型为单独`["*"]`或1..1000个规范UUID，地域1..32项、每项trim后1..80字节且无控制字符；普通数组去重排序，混合/重复通配符拒绝。旧名称、旧格式/空模型范围或地域未改时原样保留，包括原顺序；目录外值可见，不在打开编辑器时自动清洗。创建/列表补充地域及基本配置版本，明文Key仍仅创建时显示一次。

期限提供“保留原期限 / 设置新期限 / 移除期限”三态；原始RFC3339秒与微秒保留，明确设置才转换为UTC微秒，新值须晚于取得全部编辑锁后的数据库时刻，转UTC后超出0000..9999年拒绝。已过期值可原样保留以编辑其他字段，也可明确续期或移除期限重新启用；已撤销Key只读，不能恢复。GET可见性与015/016相同；PUT只允许当前owner管理本租户Key、member管理自己，billing_viewer只读，并沿用CSRF、MFA策略、近期再认证和实例/恢复门禁。

冲突或未知保存先只读刷新、复核后明确开始新编辑，不自动重试或仅凭相同配置判断写入归因。三种编辑器分别保留较高版本，配置响应不会覆盖列表的当前消费/冻结快照；关闭/离页清理输入和秘密，账号/租户/角色变化屏蔽晚回。收紧模型/地域/期限会阻止尚未获`sending`授权的调用，已预占沿原释放路径归还；已获发送授权的在途SSE、结算/释放/退款/恢复继续完成。017不重建运行桶、不清并发，也未实现周期预算、分组商品或Key换发/恢复。

## 隔离浏览器QA（014）

`scripts/browser-qa`启动已构建的真实程序，并仅在显式指定的本机`moni_test`中创建随机schema及受限app/projector LOGIN角色。测试owner须有创建schema/角色与配置权限的能力；这些能力不会授予应用。不要加载评审/正式环境文件，运行器拒绝moni_review和远程库，应用和mock端口均排除58123。同一个测试数据库只运行一个QA主进程，以免触发正式单活动实例锁。

```sh
npm --prefix web run build
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
export TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
go run ./scripts/browser-qa -binary bin/moni-aigate
```

stdout只给应用origin、私有manifest路径及安全状态，端口每次分配。manifest位于`.local/browser-qa/run-*/manifest.json`，目录0700、文件0600；在本机受控编辑器读取本次合成账号和mock接入资料，不复制整个文件到日志、报告或Git。它只准备管理员、财务制单/审批三种身份和本机供应商，不预置租户、模型、钱包或调用事实。

按[014浏览器路径](../verification/mvp-release-qualification.md)在应用中创建渠道、完整池、Chat模型/路由/价格和客户授权，再用两个独立财务账号核准模拟资金；客户创建Key后在Playground验证非流/SSE、请求详情、钱包和退款。测试出口只授权manifest列出的本机CIDR，不填真实供应商。一次性Key只留当前页面，完成后清空并退出；截图应选择账单/钱包/回执，不包含秘密表单。

完成浏览器操作、保存安全截图和只读业务证据后，先结束在途测试请求，再在运行器终端按Ctrl+C。运行器等待子serve退出，关闭mock，记录计数，删除本次schema/角色并清除manifest密码/供应商秘密；保留安全清理记录。若显示cleanup_failed，按manifest的remaining_resources核查本次资源，不能当作成功清理或误删其他实例。QA的退出等待有12秒上限，不能替代正式部署最长320秒的排空流程。

独立验证运行器隔离、两轮真实main生命周期和失败bootstrap清理（使用上面已构建的程序及专用测试库）：

```sh
BROWSER_QA_REAL_MAIN_TEST=1 BROWSER_QA_ERROR_TEST=1 go test -race ./scripts/browser-qa -count=1 -v
```

这两项显式opt-in不会由默认全量Go测试自动运行。真实main在途SSE排空用例在`internal/server/main_shutdown_integration_test.go`，需要专用测试owner具CREATEDB权限，否则明确skip；它在独立临时数据库中运行，不碰评审库。正式供应商、容器与目标性能仍按[剩余门禁](../verification/mvp-status.md)分别验收。

## 生成与检索协议联调

登录后可进入“API联调”；无租户账号先看到接入步骤。平台为模型选择协议、配置带版本前缀的渠道base URL（如`https://api.openai.com/v1`、`https://api.anthropic.com/v1`或Gemini的`https://generativelanguage.googleapis.com/v1beta`）、发布绑定与售价，再授权客户。新模型本身不会自动给客户权限或余额。

客户完成邀请开户、人工核款审批后创建受限Key；Key只显示一次。在Playground内存输入Key、选择获授权模型、文本/输出上限/流式后显式发送。Key和文本不会写入localStorage；关闭页面即清除。取消会关闭读取，不自动重试，请凭request_id检查最终账单。

CLI示例（自行在安全终端设置`MONI_API_KEY`与已授权公开模型名，示例不包含真实Key）：

```sh
curl http://localhost:58123/v1/responses \
  -H "Authorization: Bearer $MONI_API_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"your-responses-model","input":"hello","max_output_tokens":32,"store":false}'

curl http://localhost:58123/v1/messages \
  -H "x-api-key: $MONI_API_KEY" -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"your-messages-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}'
```

Messages不可同时发送Bearer和x-api-key；协议须与模型profile一致。原生接口限无状态文本/基础函数工具，完整边界见[003规格](../../specs/003-native-protocols/spec.md)。正式渠道必须先验证输入硬上界、输出约束和usage；本地fixture与一次成功请求不足以证明已可收费上线。

Embeddings/Rerank选择相应模型后，Playground显示文本/文本列表或query/documents、top_n等字段，隐藏生成输出与流式选项。API还支持Embeddings token数组；详细范围见[009](../../specs/009-retrieval-protocols/spec.md)。平台须设置整个请求的聚合输入硬界、输出上限/默认输出0及输出价0，并核实供应商对聚合计费量的保证。不要将单文档context当整批计费上界。

```sh
curl http://localhost:58123/v1/embeddings \
  -H "Authorization: Bearer $MONI_API_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"your-embedding-model","input":["hello","world"],"encoding_format":"float"}'

curl http://localhost:58123/v1/rerank \
  -H "Authorization: Bearer $MONI_API_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"your-jina-rerank-model","query":"hello","documents":["hello world","another text"],"top_n":1,"return_documents":true}'
```

两种检索请求不能携带stream（false也不允许）、max_tokens或工具字段。Rerank仅支持Jina token用量，Cohere search_units未支持。缺少可信最终usage返回503 billing_pending并保留预占，不返回向量/排名；凭request_id查账，不自动重发。平台探测也须可信usage才显示成功，其成本独立核对，不扣客户钱包。

## Gemini 原生文本联调

平台选择`gemini_generate_content`协议，渠道基础URL填写含版本的路径（通常为`https://generativelanguage.googleapis.com/v1beta`），路由中填写上游模型名称。公开模型和上游模型都限1至100个ASCII字符，以字母或数字开头，后续允许字母、数字、点、下划线或短横线，不能含连续点、斜杠、百分号或冒号。模型映射发生在URL路径，不能把`models/`前缀或完整端点填入模型名。创建模型时保留正的输入/输出上界与默认输出，函数工具固定关闭。

客户请求只使用`Authorization: Bearer`中的moni API Key。不要同时发送`x-api-key`或`x-goog-api-key`，也不能把密钥放在query中；重复或混合凭据会拒绝。供应商密钥在平台渠道中保存，由网关转为上游`x-goog-api-key`，客户无需提供供应商密钥。

JSON示例中`your-gemini-model`应替换为已授权的公开模型名。请求体保持原生`contents`、可选`systemInstruction`和`generationConfig`，不含`model`或`stream`：

```sh
curl 'http://localhost:58123/v1beta/models/your-gemini-model:generateContent' \
  -H "Authorization: Bearer $MONI_API_KEY" -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"systemInstruction":{"parts":[{"text":"Be concise."}]},"generationConfig":{"maxOutputTokens":32,"candidateCount":1}}'

curl -N 'http://localhost:58123/v1beta/models/your-gemini-model:streamGenerateContent?alt=sse' \
  -H "Authorization: Bearer $MONI_API_KEY" -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":32,"candidateCount":1}}'
```

非流端点不接受query；流端点只允许一个`alt=sse`。流使用原生`data: JSON`，没有Chat的`[DONE]`；网关等待合法`STOP`或`MAX_TOKENS`、完整帧边界的EOF与最终usage后结算，再发送成功终态。没有合法finish的EOF、残缺尾帧、流内错误和缺少可信最终量都不能当作成功。取消会停止读取，不自动重发；凭`request_id`检查最终账单。Playground会生成同样的URL和原生JSON，并支持可选纯文本系统指令。

010范围为无状态纯文本JSON/SSE：不支持媒体、函数/托管工具、`cachedContent`引用、thinking配置或有状态会话；官方SDK完整认证仍需单独进行。可选生成参数与详细边界见[010规格](../../specs/010-gemini-native/spec.md)，协议资料见[合同校准](../research/gemini-contract-notes.md)。

最终`usageMetadata`按`input=promptTokenCount`、`output=candidatesTokenCount+thoughtsTokenCount`计费，`totalTokenCount`须等于两者之和；缺省思考计数为0，思考文本不交付。`cachedContentTokenCount`属于prompt输入子量，不重复加量，当前仍按统一输入单价收费。缺失、冲突或未知收费维度会进入`billing_pending`并保留预占：JSON不交付结果，SSE不发成功终态，不按估算补量。

发布正式渠道前，须核实输入硬界覆盖全部contents、systemInstruction和协议开销，且`maxOutputTokens`能约束候选与思考的**聚合收费输出**。不能把仅限制可见文本的上限当作全部计费输出的保证；schema、mock或单次200不足以完成该认证。真实供应商的模型上界、最终usage、错误/无消费证据及成本合同均须逐模型核实，本手册示例不表示正式供应商已经验收通过。

## 渠道模型发现与差异预览

平台管理员进入“上游渠道”，在渠道行点击“发现模型”，选择`openai`、`anthropic`或`gemini`目录格式，再点击“读取模型目录”。基础URL沿用渠道配置，须包含供应商的版本路径：OpenAI兼容与Anthropic通常为`/v1`，Gemini通常为`/v1beta`；服务端只在其下读取固定`/models`，分页由适配器处理。目录格式由管理员明确选择，不根据模型名字推断调用协议。

该操作只发送供应商目录GET，不调用生成接口、不预占或扣除客户钱包。供应商凭据仍保存在平台并由服务端使用，浏览器不直连供应商；会话、CSRF、管理员权限和近期再认证仍需满足。每个渠道每分钟最多发起一次发现，与平台探测共享4个运行许可，并占用全局100预算中的一个；许可覆盖分页和持久收尾。目录读取总计最多10s，单页解压后最多1MiB、全部最多4MiB、最多10页及1000个原始条目。超过任一上限、分页未完成或某页失败，都不会把已读部分保存为成功目录；开始时绑定快照和最终模型ID数组还分别受1000条/256KiB持久化边界约束。

成功记录提供排序去重的模型目录及三类差异：`unbound`是目录列出而开始快照尚未绑定的上游模型；`present`是已绑定且目录列出的绑定；`missing`是已绑定但本次目录未列出的绑定。绑定快照包含暂停公开模型的当前路由版本，一个上游模型可以对应多个公开模型，界面保留每条绑定。暂停不等于未配置，目录缺失也不等于模型不能调用，更不会自动删除、停用或修改路由。

历史记录关联本次开始时的渠道、凭据版本及已发布绑定快照。渠道版本或可用状态变化、原凭据撤销、绑定或公开模型启停变化时，读取详情会动态标记`stale`。过时记录用于查阅，准备配置时应重新发现；渠道已更新导致版本冲突时，先关闭窗口、刷新渠道列表，再重新打开。发现失败可使用响应中的`discovery_id`查看历史，界面也会刷新记录并定位该次失败；若进程中断或未持久化终态，无结果记录30s后显示`interrupted`。页面只在操作时读取，不自动轮询或重试；可手动刷新历史或重新发起发现。

CLI示例使用已有管理员会话、CSRF和渠道标识环境变量。`MONI_ORIGIN`应与部署的`PUBLIC_ORIGIN`一致，`MONI_SESSION`为会话Cookie值，`MONI_CSRF`为该会话的CSRF值；`MONI_CHANNEL_ID`及`MONI_CHANNEL_VERSION`取自当前渠道记录，`MONI_DISCOVERY_ID`取自本次响应或历史。示例不包含真实凭据：

```sh
curl "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/discover-models" \
  --cookie "moni_session=$MONI_SESSION" \
  -H "Origin: $MONI_ORIGIN" -H "X-CSRF-Token: $MONI_CSRF" \
  -H 'Content-Type: application/json' \
  -d "{\"format\":\"openai\",\"expected_version\":\"$MONI_CHANNEL_VERSION\"}"

curl "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/model-discoveries" \
  --cookie "moni_session=$MONI_SESSION"

curl "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/model-discoveries/$MONI_DISCOVERY_ID" \
  --cookie "moni_session=$MONI_SESSION"
```

第一条请求的`format`可改为`anthropic`或`gemini`，应与该渠道实际目录接口一致；`expected_version`是当前渠道版本，不是公开模型的路由revision。目录历史使用响应的`next_cursor`分页，详情必须使用同一渠道的记录ID。

发现只辅助人工接入：核实具体模型协议、整个请求的输入/输出计费硬界、最终usage及成本合同后，再通过现有模型配置创建profile、追加售价、授予客户权限，最后按完整绑定及`expected_revision`发布路由。新目录不会自动生成profile、价格、授权或发布；成功目录、已有绑定或一次探测成功也不能代替正式供应商认证。完整合同见[011规格](../../specs/011-model-discovery/spec.md)。

## 渠道凭据池与永久撤销

管理员在“上游渠道”打开“凭据池”，读取当前版本后编辑完整成员列表；每池1..32项，每项名称1..64字节、权重1..1000000、并发1..1000。第一项为primary，可以调整顺序，保留原credential ID或添加新secret，提交时一次保存完整池。历史版本只读。新secret只放当前组件内存，成功提交、关闭或切换渠道后清除，不进localStorage或日志；需要重新输入时从受控密钥管理器取值。

接口仅限admin，读取需要有效会话并满足当前MFA策略及实例Guard；PUT/DELETE还要求CSRF、近期再认证并通过恢复门禁。MFA默认可选。`GET /api/platform/channels/:id/credential-pool`返回`channel_version_id`、`current_version`、`stale`及安全成员元数据（ID、position、label、weight、max_inflight、primary、revoked_at）；添加`?version_id=...`读取同渠道历史。接口不返回secret、密文或秘密前缀。

`PUT .../credential-pool`接收`expected_version`及**完整**members数组，未列入的成员会从新版本移出；每项只能填现有`credential_id`或新`secret`之一，不能同时填写。请求字段示例：

```json
{
  "expected_version": "<刚读取的 channel_version_id>",
  "members": [
    {"credential_id": "<保留的原 ID>", "label": "主凭据", "weight": 1, "max_inflight": 8},
    {"secret": "<受控提供的新供应商密钥>", "label": "备用凭据", "weight": 3, "max_inflight": 4}
  ]
}
```

秘密最多4096字节，不能含空白或控制字符；重复成员/秘密、把历史同秘密作为新身份录入、重录已撤销秘密都会拒绝。秘密已有有效ID时应保留原ID。未知/重复JSON字段也会拒绝。成功创建一个新渠道版本并返回新池；CAS过期返回409，先刷新比较再重新编辑，不能只换expected_version后盲目重放旧列表覆盖他人的变更。普通渠道元数据/状态更新会保留整个池和原ID；多成员不能通过旧单secret字段修改，须使用完整池接口。

以下命令使用上一节的管理员环境变量；`MONI_POOL_FILE`指向操作者准备的权限0600完整JSON文件，`MONI_HISTORY_VERSION`及`MONI_CREDENTIAL_ID`来自对应渠道的安全元数据。文件中的新秘密不要提交Git，完成后按密钥管理要求清理：

```sh
curl "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/credential-pool" \
  --cookie "moni_session=$MONI_SESSION"

curl "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/credential-pool?version_id=$MONI_HISTORY_VERSION" \
  --cookie "moni_session=$MONI_SESSION"

curl -X PUT "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/credential-pool" \
  --cookie "moni_session=$MONI_SESSION" \
  -H "Origin: $MONI_ORIGIN" -H "X-CSRF-Token: $MONI_CSRF" \
  -H 'Content-Type: application/json' --data-binary "@$MONI_POOL_FILE"

curl -X DELETE "$MONI_ORIGIN/api/platform/channels/$MONI_CHANNEL_ID/credentials/$MONI_CREDENTIAL_ID" \
  --cookie "moni_session=$MONI_SESSION" \
  -H "Origin: $MONI_ORIGIN" -H "X-CSRF-Token: $MONI_CSRF"
```

**保存池后必须重新发布完整路由。** 成功PUT只更新渠道current_version；为相关公开模型按当前`expected_revision`发布完整bindings，之后新准入才使用新池。不要把池版本当路由revision。已经发布的旧版本和在途请求保持旧成员；移出成员不等于立即停用。紧急停止某ID时执行上面的DELETE永久撤销：影响其全部历史版本、幂等且无恢复入口；已发上游无法撤回。若供应商Key曾跨渠道录为多个身份，还需处理其他身份或在供应商侧吊销，不能假定一个ID撤销代表全局Key失效。

调度先按渠道最高优先级与渠道权重选渠道，再按该池可用成员权重选凭据，池大小不增加渠道选中概率。渠道总并发和成员并发同时生效，成员以credential ID跨模型/版本共享，健康按credential+model区分。每个请求仍最多两次生成尝试，不在同渠道失败后换Key继续；未知消费或已有下游输出不会重放。池配置不改变客户模型授权、已固定售价、硬预占和最终结算政策。

平台attempt、probe和目录详情显示实际credential ID，每渠道路由解释保持一条。Probe从完整已发布池选一个可用成员，只代表该成员本次协议/可信usage，成本单独核对且不动客户钱包；目录发现按position选当前池首个未撤销成员，只发目录GET。它们都不能证明全池认证。本切片没有后台自动轮换、定时探测、供应商余额/全局账号额度、自动同步或跨渠道批量运营，详见[012规格](../../specs/012-credential-pools/spec.md)及[LLD §19](../design/LLD.md#19-012-不可变渠道凭据池)。

## 跨渠道批量启用与停用

013增加渠道页当前页多选、批量启用/停用、逐项预检、回执历史与按ID查询。最多选当前页50条，刷新、翻页、筛选或切换资源会清除选择并要求重新预检。预检只读数据库，不付费探测、不访问供应商目录。它显示可用凭据及已发布/旧版本绑定数量，不能保证实际供应商可用性或全池认证。

**启停立即影响全部历史发布绑定的渠道门禁。** 停用后阻止新准入与尚未派发的尝试，已发上游不能撤回；启用可能恢复旧路由服务，不需要重新发布。不改变current_version、凭据池、永久撤销、隔离、模型绑定/价格/授权和客户资金。保存新池仍遵循上一节显式重新发布的流程。隔离渠道或当前池零可用成员不能批量启用；停用不清除隔离。

预检接口`POST /api/platform/channel-operations/preflight`接收以下固定字段，API每批1..100条。渠道列表提供current_version和字符串management_revision；不要把管理revision、模型route revision和渠道版本混用：

```json
{
  "action": "disable",
  "targets": [
    {
      "channel_id": "<渠道 UUID>",
      "expected_version": "<列表中的 current_version>",
      "expected_revision": "<列表中的 management_revision 字符串>"
    }
  ]
}
```

确认后`POST /api/platform/channel-operations`在相同内容上增加一次生成的UUID字段`operation_id`。必须保留这个ID：HTTP200返回`applied`或`rejected`持久回执，并不总是成功修改状态。任何目标版本/revision过期、缺失或启用条件不满足都会整批拒绝，其他渠道零修改；刷新后重新审核方可发起新操作。no-op仍有回执，但不增加渠道revision。普通单渠道编辑也会递增management_revision，原入口未因此变为CAS编辑。

网络中断、5xx或无效响应视为结果待确认，使用`GET /api/platform/channel-operations/<原ID>`查询，不能自动换ID重发。404只表示这次查询未读到已提交回执，另一事务仍可能执行；继续保留原ID人工核查。关闭弹窗或切页后控制台在当前账号内存中保留已提交ID，刷新浏览器会丢失内存，应复制ID或用历史入口找回；无需把秘密保存到浏览器。

`GET /api/platform/channel-operations`提供每页50条摘要，详情保持执行时before/after，不会混入后来渠道状态。同ID同actor与同规范内容重放返回原回执（含原拒绝），异内容或actor为409。读取历史仅admin，POST预检/执行/重放沿用CSRF、近期再认证、可选MFA及恢复门禁。恢复后禁止直接重播旧预检或备份点后可能丢失的命令，先按[恢复手册](backup-restore.md)核对。范围和本地证据见[013规格](../../specs/013-channel-batch-operations/spec.md)与[验收](../verification/channel-operations.md)。

## 渠道采购价与成本路由018

平台管理员先在“渠道成本版本”按渠道与精确上游模型登记采购价，再到“模型与路由”完整发布策略和绑定。上游可以是原厂，也可以是另一家兼容平台；接口、模型映射、计量和网络策略仍须满足该模型profile。客户售价在独立售价页配置，不因更换采购渠道改变。

所有界面采购价单位为CNY/百万计费Tokens；API为微元整数的十进制字符串。例如输入¥2/百万Tokens传`"2000000"`。unknown必须两维null，contract/invoice必须两维显式非负金额，已知免费填`"0"`。生效时间留空/null使用锁后的数据库当前时刻；未来时间是计划调价。历史不可覆盖，新版本不能复用同渠道/上游名/生效时刻，历史差异走成本更正。

成本发布示意：

```json
{
  "channel_id": "<渠道 UUID>",
  "expected_channel_revision": "<渠道列表 management_revision 字符串>",
  "upstream_model": "exact-upstream-model",
  "input_rate": "2000000",
  "output_rate": "6000000",
  "basis": "contract",
  "effective_at": null
}
```

POST `/api/platform/cost-versions`成功返回id及新channel_revision；成本发布也会递增渠道management_revision，先前的批量启停预检可能因此冲突。若409，重新读取成本和渠道，审核后新编辑；网络/5xx/无效响应保留不明状态，只读核查，不能自动重发或仅凭当前值相同证明原请求成功。

PUT `/api/platform/models/:id/bindings`一次提交expected_revision、cost_policy和完整bindings：

```json
{
  "expected_revision": 1,
  "cost_policy": { "mode": "cost_first", "unknown_cost_policy": "deny" },
  "bindings": [
    {
      "channel_id": "<渠道 UUID>",
      "upstream_model": "exact-upstream-model",
      "priority": 0,
      "weight": 1,
      "max_inflight": 4,
      "max_input_cost_rate": "2000000",
      "max_output_cost_rate": "6000000"
    }
  ]
}
```

manual按原优先级和权重选路；cost_first在健康、授权、容量与采购门禁合格的最高priority层内比较输入硬界×输入采购价＋输出上限×输出采购价，同分保留权重。需要在全部渠道之间比价时使用相同priority。cost_first必须deny未知报价；manual/deny或设置任一cap也会排除未知。null上限表示不限制该维，0要求已知免费。

候选报价、attempt快照和sending前锁内当前报价必须一致；等待期间调价或定时版本生效会503 cost_quote_stale，不发上游、不自动重规划，客户冻结走原释放。报价检查获准后继续原attempt合同，即使后续调价也不改其结算快照。门禁只约束库中已登记单价，不保证供应商真实账单/可用余额，也不是累计采购预算。[019采购账户](../../specs/019-procurement-accounts/spec.md)已实现累计预算，详见下节。

每次修改需按[统一架构](../architecture/README.md)和根AGENTS复核代码/设计映射，再执行`make architecture-refresh REASON='实际变更和架构影响'`及`make architecture-check`；CI只做只读检查。

## 共享采购账户与预算019

管理员在“采购账户与对账”创建供应商身份与累计CNY预算，先为渠道的精确上游模型登记完整已知采购价，再将每个credential永久绑定到采购账户。同一供应商账号的多个Key/渠道应绑定同一账户；绑定不可撤销或改属，错误归属需停用旧凭据并创建正确的新凭据。未绑定成员明确标为legacy_unmanaged。绑定后包括旧渠道版本在内的新attempt均受控，不能通过manual/allow绕过。

预算是本平台控制的累计采购限额，不是供应商实时余额。每个attempt按冻结报价和保守用量界先占用，最终可信量转成本；unknown保持占用，即使客户退款或24小时释放也不释放。收紧预算/暂停阻止新的sending，已经发送的账务继续；异常未核清时不能只改active恢复发送。余额人工观察另记来源/时刻/语义，unknown不等于0，不影响预算。

配置、绑定和观察提交均生成一次operation_id，HTTP200仍须看applied/rejected回执。提交不明只查询原ID，不换ID重试；revision冲突先只读核查，明确开始新编辑。采购更正由finance_operator制单、独立finance_approver审批；减少成本必须有invoice证据，新的原始证据会使旧待审失效。客户接口不返回采购信息。测试与限制见[019验收](../verification/procurement-accounts.md)。

## 公开模型与售价目录020

直接访问`/pricing`或本机[58123价格页](http://localhost:58123/pricing)，无需账号、会话、MFA或API Key。它使用独立公开布局，不进入客户工作台，也不请求个人资料。模型默认`public_visible=false`，升级不会自动公开既有模型；空目录是默认状态。

管理员在平台模型列表中打开公开配置，读取当前公开状态后显式保存。`public_revision`为独立正整数字符串，只保护公开标记；启停、路由revision及售价版本各有自己的含义。公开前须有合法逻辑名/profile，隐藏不受损坏profile阻碍。停用模型允许预设公开标记，但目录仅展示公开且启用的模型；重新启用会恢复展示。

管理接口为`GET /api/platform/models/:id/publication`与`PUT /api/platform/models/:id/publication`，只有当前有效admin可用；PUT沿原会话、CSRF、MFA、近期再认证和恢复模式门禁。提交完整JSON，例如：

```json
{"expected_revision":"1","public_visible":true}
```

旧revision即使值与当前相同仍冲突；当前revision下同值保存为no-op。冲突先重读后开始新编辑；网络/5xx/非法响应只GET核查，不自动重试PUT，也不把“当前值相同”当成原提交成功的证明。

匿名API只读，支持逻辑名称前缀`q`、精确`protocol`及固定50项游标分页。例如：

```sh
curl --get http://localhost:8080/api/public/models \
  --data-urlencode 'q=chat-' \
  --data-urlencode 'protocol=openai_chat'
```

详情为`GET /api/public/models/:id`；私有、停用和不存在统一404。使用响应`next_cursor`继续分页时保留原筛选，改变筛选需从首页开始。名称前缀区分大小写，`%`、`_`和反斜杠均按字面量匹配。每页有自己的数据库`as_of`，不是跨页一致快照；新公开且排在旧游标之前的模型需刷新才能看到。

响应只含逻辑模型、已实现协议能力/限制及当前客户售价，不含渠道、秘密、上游模型、采购成本或客户资料。价格为CNY微元/百万计费Token的规范整数字符串；已知0与`pricing.status=unavailable`不同，缺价显示“暂无有效售价”。未来价格不提前生效。公开目录是当前观察，**不是报价锁定或调用授权**：实际调用仍需租户/成员/Key授权、有效路由和足额预占，并在其事务内冻结售价；后来调价或下架不重算既有请求。

匿名GET使用独立限流和最多4个在途许可，2秒总超时包含Guard/连接池等待；限额满返回429、不排队。它不访问供应商或创建客户请求/资金事实；恢复模式也须满足原ready/Guard才能只读展示，不绕过管理写门禁。完整边界见[020规格](../../specs/020-public-catalog/spec.md)、[接口合同](../../specs/020-public-catalog/contracts.md)与[详细设计](../design/public-catalog.md)。

020已完成限定本地验收：156项前端测试与strict构建、最终catalog/server专用PG race（2.347s/16.171s）、QA运行器opt-in（10.296s）通过。此前全Go测试通过早于最后SQL安全小修，不能代表最终源码全量回归。隔离浏览器覆盖默认私有、发布/下架、CAS冲突、精确大金额、零价/缺价、前缀筛选及390px无横向溢出；供应商目录、模型生成、客户请求/结算均为0，运行器正常退出并清理随机schema/角色/监听。

020当期58123预览已应用015并配置受限角色，记录PID为85387，二进制SHA-256为`5530671707b03ab90f355a073676c7405b106bc2be944e0c8cac0ac8f97127a4`；ready且非恢复模式，首页及35项assets与构建一致。证据见[020验收](../verification/public-catalog.md)、[浏览器记录](../verification/public-catalog-020-browser.json)、[隔离运行器与清理](../verification/public-catalog-020-harness.json)、[015预览升级](../verification/public-catalog-020-preview.json)。这些结果不替代正式供应商、生产部署或性能对比验收。

## 自助注册与邮箱恢复（021）

已实现并完成[限定本地验收](../verification/self-service-identity.md)，当前001–016。58123以受限角色运行021包，默认注册关闭、邮件disabled，原评审账号/MFA不变。测试账号继续按本地私密凭据文件使用；无邮件环境可使用原管理员创建账号与登录。

正式配置时先按排空流程停旧进程、追加迁移/provision，再在私密环境中设置`AUTH_REGISTRATION_ENABLED=true`、`MAIL_TRANSPORT=smtp`及SMTP_HOST/PORT/FROM；认证用户名/密码同时设置或同时省略。默认`SMTP_TLS_MODE=starttls`，也支持implicit，均验证证书与主机；`plain_loopback`仅用于字面127.0.0.1/::1隔离fixture。邮件启用时PUBLIC_ORIGIN必须是精确HTTPS origin，无path/query/fragment；本地fixture允许字面loopback HTTP。不要使用生产SMTP做自动化QA。

`/register`先请求证明，邮件`/register/complete`链接30分钟；`/password/forgot`及`/password/reset`为15分钟。页面移除fragment，刷新后须重新打开邮件，不自动消费证明。新账号零余额/无模型授权/Key；需运营配置后才能调用。已启用验证器的账号重置仍需TOTP或未用恢复码，邮件不能绕过MFA；成功退出全部会话但不撤销API Key。

配置启动读取，改动须排空重启；关闭只暂停，不批量撤销，TTL不延期。SMTP接受不保证实际收件，ACK丢失可重复同一封邮件；proof只用一次。默认disabled仍清理自然失效秘密，不发送邮件。待发密文需要保留对应keyring；rewrap-secrets使用owner/维护单活锁，AUTH_SECRET变更会使旧proof摘要失效，不能当透明迁移。

隔离QA可在明确TEST_DATABASE_URL=本机moni_test并最终构建后运行`go run ./scripts/browser-qa --identity-fixture`；运行器独立SMTP、随机schema/角色、合成账号，正文只写0600私密邮箱且退出清除，不写公开manifest。正式域名、SPF/DKIM/DMARC、收件与生产反滥用仍待外部验收。022-A合同另行冻结后实施，不把本地SMTP成功当支付或生产完成。


### 023-A 分组售价维护升级

按既有排空→owner migrate/provision→受限check-ledger/serve流程应用018。最低PG15（NULLS NOT DISTINCT）；原旧价格唯一约束形状须匹配，异常历史或未知结构拒绝升级。匿名和旧平台价入口仍默认域，客户模型价与调用按当前tenant域；新建价格组不自动分配客户或授予模型。组退休只禁止新分配/发布，原客户继续；缺组价不回落默认。023-A限定本地验收、48资源与受限权限/默认off预览记录见[验证](../verification/tenant-pricing.md)。

## 站内经营告警（024-A）

`OPERATIONS_ALERTS_ENABLED=false`默认暂停评估；管理员仍可管理规则，三种平台角色可读取历史与共同确认。开启后重启，独立双槽/双连接worker观察渠道持久隔离、采购登记累计预算和客户/采购待核对积压。确认只记录第一确认者，不解除隔离或裁决资金；登记余量不代表供应商现金余额。恢复模式只读。

同目标/种类仅一条非退役规则，最多64条启用、256条非退役。正常目标60秒观察，超过180秒标陈旧；锁等待或故障可能延迟。停用、退役和实际配置变化会结束当前发生记录；部署暂停不代表恢复。未知保存保留原operation，只读查原回执；404不证明没有提交，禁止自动换ID重放。

019须排空升级、owner迁移及provision后启动受限serve。provision原子配置并核验权限，外部group越权导致拒绝并整体回滚；需离线运维修正角色关系。详见[验收记录](../verification/operations-alerts.md)。

`make verify-mvp`的完整Go race套件每包预算为600秒。真实进程、密码哈希与商业用例增加后，240秒旧预算不足；仅延长测试期限，生产密码哈希成本、请求超时和测试断言保持原值。必须显式传专用TEST_DATABASE_URL，跳过数据库测试不算验收。

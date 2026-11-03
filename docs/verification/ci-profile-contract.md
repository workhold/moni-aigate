# GitHub CI 测试环境与断言修订合同

2026-11-02，根冻结后实施，仅限一次性 GitHub runner、独立 PostgreSQL 和测试断言。依据首次远端运行 [41334156647](https://github.com/workhold/moni-aigate/actions/runs/41334156647) 的实际失败日志；本合同不是修复已通过声明。

## 已证实的问题与修订

1. 旧 PB/HTTP/CLI fixture 创建无密码 LOGIN；本地专库使用 trust，镜像默认 SCRAM 导致 SQLSTATE 28P01。只为新建的 CI 测试 PostgreSQL 显式配置 trust，监听 loopback；不更改产品登录、数据库部署认证或现有本机实例。这些测试验证实际 LOGIN/OID 和权限，不宣称验证密码认证。
2. Docker bridge 的端口映射只保证客户端端口相同，服务端 `inet_server_addr()` 仍为容器地址，支付 runtime 按固定地址规则拒绝。Linux runner 使用 host network，PostgreSQL 只监听 127.0.0.1:55439；就绪检查必须实际查询 SQL endpoint/DB。保留真实端点校验，不加容器地址白名单。
3. 采购测试对 JSON UTC 与 pgx Local 的 time.Time 使用 DeepEqual，可能把同一时刻误报为预算改变。只规范化比较副本的 CreatedAt 为 UTC，并用 Equal 验证原时刻；所有金额、状态、版本及其它字段仍严格比较。
4. pg_terminate_backend(pid) 的默认返回只确认信号已发送；pool.Close 也不证明远端 backend 已退出。实例测试先保存 PID/backend_start，原有时间界内由独立连接观察该实例与随机 advisory key 消失，再只调用一次 AcquireInstance。账本 reader fixture 在原10秒清理期限内等待本次精确随机角色的连接数为0，再清理；不终止别人、不扩大权限、不改产品 retry。

## 执行与覆盖

首轮 billing/server 包累计600秒时，正在执行的测试仅4秒/0秒；堆栈处于 fixture 清理/完整迁移，不证明单个测试死锁。多个测试包重复执行全套迁移，并共享同库固定401锁。本轮将 billing、identity、server 和其余包分到四台独立 runner，各有独立数据库，保留每包原10分钟 harness 和所有业务/SQL期限、race、全测试集合。调度只改变 CI 隔离方式，不是生产并行或 HA。运行前从 `go list ./...` 构造互斥完整分区，任何包仅运行一次，空分区失败；新增包默认进入其余组。

PostgreSQL连接数显式200与本地测试配置对齐；日志没有连接耗尽证据，不把它当已证实根因。fsync、synchronous_commit、full_page_writes保持开启；不使用跳过失败测试、降低安全检查、关闭持久化或延长金融预算来通过CI。前端测试/构建、架构检查、vet及嵌入构建保留在其余组；默认C0 opt-in专项未设置，不冒称新Linux profile已通过正式C0 expected认证。

先执行修改断言相关的真实专库/race测试，再全量本地集成；推送后以新 GitHub run 的实际结果核验环境修复。保留首次失败日志摘要/SHA与新证据；未知或未完成的远端任务不得标成功。

## 第二轮累计时限与顶层测试分区（根冻结）

2026-11-02，第二轮真实运行 [41339443048](https://github.com/workhold/moni-aigate/actions/runs/41339443048)，源码 `1bcc106`。身份包已通过（414.754秒）；billing/server分别在600.047/600.035秒触发包累计上限，当时用例只运行约1.317/12秒。billing已有111个完成的顶层PASS，合计579.58秒，仍有44个顶层测试未开始。当前证据不支持把这两个用例或旧取消watcher称作长期SQL死锁，不修改产品期限、重试或数据库认证。其余组当时尚在运行，最终结果另记。

仅对billing/server各拆为四个独立runner/数据库，矩阵为 `billing-0..3`、`server-0..3`、`identity`、`other`。保留完整包归属检查；两个大包通过 `go test -list .` 发现当前全部顶层Test/Fuzz/Example，固定C序排序后轮转分成四份，每份非空，拼接排序必须与原名单完全一致且无重复。测试名限定Go标识符，构造精确锚定的 `-run ^(A|B)$`，保持每个父测试及其全部子测试在同一进程中。新增顶层测试自动进入集合，新增包自动进入other；不按历史失败用例白名单筛选。

`-json -race -count=1 -timeout=10m` 保持；10分钟适用于各次测试进程，原业务/SQL/锁预算不变。完整测试集合中的每个顶层用例执行一次；Fuzz仅执行默认种子语料，和原普通go test语义一致，不宣称持续fuzz。上传完整包名单、完整顶层名单、四份分区名单及JSONL，以便独立核对覆盖。当前两个大包没有TestMain；后续若增加跨顶层共享执行前提，必须另行复核分区合同。

先校验YAML/bash语法、真实动态名单的互斥穷尽及锚定匹配，再以实际远端运行验证。未改生产或测试源码，既有本地整仓证据保持当期范围；配置解析和名单检查不冒充新Linux整轮通过。

第二轮其它组最终也失败：M/Q组件均被原catalog检查拒绝。固定v1 inventory保存Asia/Shanghai反解析的timestamptz字面量，UTC服务端会将同一常量显示为不同字符串；原检查按完整文本严格比较。专库单事务临时表、相同CHECK分别SET LOCAL UTC/Asia/Shanghai，已独立复现year1的00:00:00+00与08:05:43+08:05:43，以及year10000的00:00:00+00与08:00:00+08差异，事务已回滚。修订只在临时CI PostgreSQL启动参数中增加 `timezone=Asia/Shanghai`，就绪查询必须核对实际值；Go进程TZ不能代替数据库设置。不改SQL、固定inventory、产品DSN或校验器，未知profile继续失败；不宣称C0 expected或生产认证。

## 同库创建者默认权限与包串行合同

2026-11-04，实际运行 [41718896150](https://github.com/workhold/moni-aigate/actions/runs/41718896150) 的源码为 `20907f33ea214f35c69ffa664d6a92455c98351b`。九组通过，`contracts (other)` 失败；其中四个周期预算顶层测试在A2候选包同时运行期间返回42501。A2材料在安装期撤销同一installer的全局默认FUNCTION/PUBLIC EXECUTE，随机schema不能隔离该创建者级状态。日志事实、源码推断及无法确定的具体拒权对象见[包隔离记录](ci-package-isolation.md)；本节不宣称修订后的新CI已通过。

CI实际执行命令增加 `-p 1`，成为 `go test -json -race -p 1 -count=1 -timeout=10m`，保留完整包参数和精确 `-run`。同一runner/专库内的包依次执行。新增A2反例后的累计执行范围较大，`internal/procurementattemptbirth`独立分配 `attempt-0..3`；当前为billing/server/attempt各四份及identity/other，共14组独立runner/数据库。三大包均按完整顶层名单C序轮转，保留互斥穷尽检查和JSONL上传。其它新增包自动进入other，新增顶层测试自动进入对应分区，没有新增排除名单。

`-p 1` 不关闭race，不取消包内显式并发或测试内部的多连接、双进程、锁等待和提交争用；10分钟仍按每个测试进程计算，原业务、SQL、锁及fixture期限不变。单包发现步骤只列测试，不运行数据库fixture。`make test`、`make verify-mvp` 的全仓Go命令采用相同的包串行约束，前者保留默认期限/skip语义，后者保留数据库变量前置检查与600秒期限。

此约束只管一条Go命令调度的包，不能协调另一个shell、并行任务或独立进程对同一专库/installer的全局默认权限变更；这些调用必须串行，或使用独立专库和创建者身份。不能据本次调度修订改变生产ACL、补授历史fixture宽权限、跳过数据库测试或延长金融预算。修订须在包含全部集成变更的精确提交上重新运行CI，以该run的实际终态记录验收。

A2的110项catalog反例分四个连续名称顶层，metadata与ACL固定拆分后共15个fixture，继续逐项同事务拒绝/回滚复核及各fixture末尾actual APP业务正控。静态库存强制全部攻击互斥、穷尽、无重复；fixture原180秒与进程原10分钟均不变。这是测试调度调整，不改变请求链路、业务权限、候选SQL或真实并发验收条件；详细来源与已知限制见[包隔离记录](ci-package-isolation.md)。

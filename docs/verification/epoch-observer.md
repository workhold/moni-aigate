# C0-O1 独立停服 epoch 观察验证

2026-11-03。**库级候选范围已完成限定本地验收。** 基于 `cceee0232cd6a2781b92dcf12515813776be292f`；本记录绑定本轮观察源码，旧 protected-init 或 CI 结果不代替本轮。精确源码与命令结果见[机器记录](epoch-observer.json)。

先冻结[合同](../../specs/022-self-service-payments/c0-epoch-observer-contract.md)，并行实现生命周期、私有事实读取、无Query原件投影与strict绑定，再由根串行专库验证。没有正式迁移、ACL、CLI、普通main或在线资金路径变化。

## 实现与独立审查

新的 `Gate.ObserveFundingLots` 认领唯一 Begin，在新物理连接已取得双 session 锁之后开始 RR READ ONLY。旧 authority 的身份、完整迁移 history、fresh stopped 和前驱读取被提取为私有描述性 reader；原 wrapper 仍先验证原安装证明与 issuer，不向观察路径发行 Permit。全部 16 张 C0 表确实为空才返回 Uninitialized，非空必须通过完整 84 表/八算法及 strict v2 原件绑定。

`ProjectEpochProof` 只从本次完整钱包读取的私有原行提取并深复制两个单例；它不新增 SQL、不修改原 rejected map，不提供数据库或资金权限。观察器不复用初始化专属计数，不把经营后的当前 basis 强等于历史开账摘要。不同 epoch 也须通过实际完整图；同 ID 异摘要、部分表或损坏原件一律错误零值。

独立审查发现回滚前后存在硬时钟检查窗口：即使绝对期限已过，context timer 也可能尚未调度。现于认领 ROLLBACK 前与确认其完成后的生命周期锁内明确比较绝对期限。新的 net.Pipe 测试只构造 pgx idle 句柄，在 ctx.Err 仍为 nil 时确定模拟两个窗口，不冒充真实 PostgreSQL。30秒预算覆盖只读事务终结，随后独立 cleanup 最多5秒；清理成功前不发布结论，调用者取消仍返回零值。

## 真实 PostgreSQL 场景

专项显式同时配置 `C0_MAINTENANCE_DATABASE_URL` 与 `MONI_TEST_DATABASE_URL`，使用固定专用 `127.0.0.1:55439/moni_test`、PG16.15。本地受审 catalog profile、随机 ordinary schema/角色与 genuine sandbox installer 沿原合同，不修改预览、全局数据库配置或无关连接。

- 空 C0 的 Uninitialized、空 TEXT/Unicode 旧 tenant 开账后的 Matched、另一有效 epoch 的 DifferentEpoch，以及同 ID 四项摘要分别变化时拒绝。
- 原初始化 COMMIT 已在数据库完成但回包丢失：旧 session 仍持锁时新 Acquire 超时；旧连接关闭后新 observer Matched，原 OutcomeUnknown 保留。真实已回滚事务则观察到全16表空。
- 混合历史及大 NUMERIC：独立手算 available=`9223372036854775872`、held=`9223372036854775727`，经真实 Plan→Init 后独立观察。
- genuine sandbox 保留真实 v1 原件与 SHA，实际开账、再观察，复核旧支付资金和原件未变。
- 新 tenant 原 INSERT 触发真实 genesis；入账200、预占120、结算80、消费退款30+20后，独立算得 available170、held0、consumed30，Key spent80、held0，wallet tail seq5。历史 epoch 计数与余额合计仍为零，实际当前109表 basis 已变化，但 observer 仍 Matched。
- 实际 APP/projector 活连接、完整 history 漂移、epoch 缺失、损坏 strict 原件、历史计数与 catalog ACL 漂移均拒绝。取消、ROLLBACK 回包及 cleanup 回包故障零结果；Gate 别名/公开 Begin/Close 在 busy 期间拒绝，清理前不发布，实际 backend 和双锁释放。

经营正例保留所有原 FK/普通与 deferred trigger，不靠禁用约束制造合法数据；除原 tenant/genesis trigger 与 billing finalization API 外，其余 lot/资金写入为明确的隔离 owner fixture，**不是已经完成在线 APP 资金接线**。坏数据负例仅在各自隔离 schema 内制造并恢复 catalog 后交给观察器拒绝。

固定 wire 记录核对 RR READ ONLY、零 COMMIT、唯一成功 ROLLBACK 与受审只读基础设施命令；只读数据库事务进一步约束写入。原 Gate/Plan/Init/Installation/Permit/predecessor、84表字段/资源 SQL、basis/codec/纯重放及普通服务依赖隔离由最终八包回归共同覆盖。fake 错误分支、纯字段突变与真实数据库场景分别保留归属，不声称每个组合都已构造为真实环境。

## 探索性结果与最终检查

首轮真实专库 `-race` 135.503秒失败。新测试的只读命令 oracle 只接纳 SELECT/SET/BEGIN/ROLLBACK，遗漏既有 catalog 的 WITH 只读查询。首次只增加 WITH 后，第二轮198.465秒仍失败：固定 SQL 使用 SELECT 换行，词法 oracle 应按首个 SQL token 判断，不能要求关键词后一定是空格。两次均为断言修正，不放宽数据库 READ ONLY 或生产能力。

第三轮观察专项通过281.861秒、零失败/零SKIP，包含新增经营/大金额/sandbox场景；当时尚未汇合最后的回滚硬时钟修正，因此不作为最终源码验收。该修正的无数据库专项 race 9.324秒及依赖隔离0.587秒通过，真实数据库与整仓最终结果单独记录。

最终八包真实专库 race 分两条串行记录完成：维护包894.881秒、169个顶层全部PASS；根曾向Go调度器发出SIGINT，它等待该包完成后以exit1退出，未启动另外七包，故该命令不记为八包成功。使用同race配置的 `go test -list` 独立发现169项，与实际PASS集合完全相等，无缺失/重复/额外项；另七包补跑60.654秒、exit0。两次相关源码SHA完全相同，合计457个顶层/3455个父子PASS终态，零SKIP、零测试失败。中断命令、完整发现清单与补跑结果分别保留，不覆盖原exit1。

`make check` 已通过；指定 `TEST_DATABASE_URL` 的 `make verify-mvp` 通过968.534秒，前端286/286、46个Go包默认race、vet与trimpath二进制构建均通过。执行前后相关源码SHA相同；默认整仓不打开C0/MONI独立opt-in，真实维护覆盖由上述完整八包专项承担。文档和结果收口后再次执行reviewed refresh与只读检查，未改变已验代码。25张 Mermaid 12.2.0 实际解析/渲染通过，根已检查完整模块图与独立观察细图，精确身份见[图验证](architecture-epoch-observer.json)。README 与 runbook 的首次启动命令、Compose依赖、132个本地路径和14个锚点已复核；JSON 环境示例以占位符在 sh/zsh 中实际解析通过。未读取用户私有 `.env`。

## 尚未交付的边界

本片是停服维护库中的候选观察能力，没有生产维护 CLI、非空 epoch APP RuntimeProof、正式迁移、ACL 或在线资金路径变化。后续 R1 仍须审核完整84表/列的实际APP权限（包括 `payment_query_conflicts` 四列）及 cutover/current roots 兼容，再推进正式迁移、启动门禁与全部资金 helper。原路退款、拒付/债务、商品、媒体、正式支付、真实供应商、目标容器/1Panel、HA与性能对照仍按各自阶段验收。

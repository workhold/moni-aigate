# C0-T0 专用维护双 Gate 验证

2026-11-02。对应[根冻结 T0 合同](../../specs/022-self-service-payments/c0-permit-contract.md#10-根冻结t0专用连接双session-gate与清理)。实现位于 [`internal/maintenancetrust`](../../internal/maintenancetrust)，仅有 `Acquire`、`Gate.Close`、固定安全错误与 redacted 编码。没有 Snapshot/Permit stub、普通 main/CLI/HTTP 接线、迁移/权限变更或资金动作。T0 成功不证明安装 catalog、维护身份、业务来源完整性或运行许可。

## 已实现边界

- 入口深拷贝解析后的 pgx 配置（RuntimeParams、fallback/TLS）及完整 Pins；拒绝 nil fallback 和不闭合的 Pins。仅新建专用物理 TCP 连接，入口至返回共享父期限与原 30 秒的较早值。
- 只读 catalog preflight 实际核 Config/current/session LOGIN、DB/schema/OID/raw namespace owner 与真实 TCP/SQL endpoint。ordinary 允许 DNS，但实际 socket 与 SQL server 必须一致；public 内建例外额外绑定真实 datdba 的 OID 及角色名。sandbox 保持字面端点及 moni_test LOGIN 配置限制；super/datdba 的维护认证、v1 和业务状态仍留 T2。
- 固定 SQL 先 782193402、再 782193401。只对明确 false 每 50ms 等待；每把明确 true 后核本 PID/DB 的完整 advisory 集合：ExclusiveLock、granted、classid=0、objsubid=1、无额外锁。每把只取得一次。
- Close 使用独立 background 总计最多 5 秒，即使传入 nil 或已取消 context。正常 idle 且锁集合精确时先解 401 再 402，每次要求明确 true，末尾核无 advisory 锁，再关闭专用连接。复制 Gate 共享私有 busy/关闭状态；并发直接安全拒绝，幂等返回第一次 cleanup 结果。
- SQL 错误、取消、丢回复或异常事务/锁集合使连接永久失效，不重试未知 lock、不补偿 unlock、不操作未知事务。先强制关闭拨号时捕获的原 net.Conn，再做有界 pgx 清理；不依赖 pgx 已 closed 标志。socket registry 单独加锁并先封闭，覆盖 async CancelRequest 可能晚到的额外 dial，封闭后新 socket 立即物理关闭。
- 所有公开错误为固定 maintenance 码，无 driver unwrap；String/GoString/JSON 固定 redacted。不公开连接、SQL hook、skip、已有锁或授权声明。

## 专项测试与证据

真实数据库测试只由独立 `C0_MAINTENANCE_DATABASE_URL` opt-in，解析后仍要求本机专库 127.0.0.1:55439/moni_test、moni_test LOGIN、无 TLS/其他查询项。值在本地从既有维护配置仅于子进程环境注入，未写入本文、机器记录或测试输出。fixture 创建自己的随机 schema 与三个实际 LOGIN，独立 admin 观察会话/锁；不安装候选 DDL，不修改共享 public/数据库 ACL，不终止其他进程。

```sh
# 仅在专库环境中注入上述独立变量，不将凭据写进命令或日志。
go test -json -race ./internal/maintenancetrust -count=1 -timeout=150s
go vet ./internal/maintenancetrust
```

专项覆盖真实新连接、402→401/各一次、第二把被占时仅第一把可见且无半 Gate、false 后正常取得、父 deadline/主动取消/原 30 秒、逆序解锁、nil/取消 Close、原 5 秒 cleanup 超时、复制/零值/并发/幂等/redaction，以及额外/丢失/shared/two-int advisory 锁和非 idle 事务。身份负例覆盖 DB/schema/owner 名称及 OID、错误/NULL/系统 current_schema、SET ROLE/SET SESSION AUTHORIZATION 造成的真实登录映射漂移、已有 advisory 锁、真实 socket IP/端口与配置不符。ordinary DNS、非 owner LOGIN 及 public 内建有效 owner 的正例确保 T0 没有提前冒充 T2 维护认证。

401 丢回复与 unlock401 丢回复使用**仅测试文件**的标准 DialFunc wrapper；RemoteAddr 如实转发真实 TCP。固定 simple-protocol SQL 的响应已经从原 socket 读到，但尚未交给 pgx时，独立 admin 以原 PID/backend_start 及 pg_locks 分别证明 401 已 granted 或 401 已释放而 402 仍持有。随后 fixture 仅返回 read error，不主动关闭原 socket；生产 unknown 路径必须强制物理关闭。原 backend/锁在测试 cleanup 关闭 wrapper 之前独立有界归零，避免把未执行请求、Describe 失败或 fixture 主动断连当成 unknown 清理通过。5 秒 cleanup 测试同样不由 fixture 关闭原 socket。

每个 fixture 精确清理自身 schema/角色/会话，并比较共享 public owner、原 ACL、关系数和数据库 ACL 与初始值相同。最终 race、源码/日志 SHA、计数及独立清理结果见[机器记录](maintenance-gate.json)。最终精确源码整包真实 PG/race **退出 0，54.205 秒，17 个顶层测试、53 个终态 PASS，0 FAIL、0 SKIP**；`go vet` 退出 0。独立只读审计确认本轮 `mtg_` schema/role/backend/advisory 锁全为 0，public 仍为内建 owner、0 关系，数据库 ACL 仍为 NULL。原 30 秒入口与 5 秒 cleanup 均真实等待验证，未改预算。此前通过轮次保留各自源码范围，不代替本次最终结果。

## 后续及验收限制

此片仅 Gate。T1 顶层 RR/125 表锁与 stage deadline、T2 opaque InstallationProof/真实维护身份/v1、T3 全源 scanner、T4 plan/init/epoch/RuntimeProof、全资金接线、退款及正式生产验收仍未实现。没有执行模型/支付网络请求，没有升级 58123。统一架构语义复核与清单刷新、整仓检查由根集成人在并行结果汇合后完成；本文不自行刷新清单。

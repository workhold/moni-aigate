# 025-A1-G：模型真实主进程强杀与恢复（窄片冻结）

2026-11-02，按根任务冻结。仅扩展 `scripts/browser-qa` 私有测试接缝和独立模型 fixture，不加生产 fault hook，不改变正常 fixture 默认行为、main、provider、billing、期限或角色权限。本片只证明当前单活动进程在指定窗口的本地恢复，不证明 HA、外部供应商或完整 025。

## 隔离与装配

唯一 opt-in 为 `MODEL_LIFECYCLE_REAL_MAIN_TEST=1`；要求专用 literal `127.0.0.1:55439/moni_test`、预构建真实 main（二进制由 `MODEL_LIFECYCLE_BINARY` 指定，默认 `bin/moni-aigate`）。无显式 opt-in、专库或制品时明确 SKIP，不计已验。随机 schema、实际 APP/projector LOGIN、独立 loopback listener、0600 文件与原 browser-qa 清理约束保持；不触 58123、评审账号/MFA、真实渠道、邮件或资金。每个窗口独立 run，串行独占数据库级 InstanceLock。建任何schema/role前须只读确认实际LOGIN具有superuser或继承可用的pg_read_all_stats观察权；不由运行器增加权限。

私有 `options.modelLifecycle` 不注册 CLI/env 注入。只接收本 run 的 owner、合成 secrets/账户/上游配置、受控模型 fixture 及现有 kill/restart 生命周期；禁止任意启动器或生产暂停接口。setup 使用真实 identity/catalog/procurement/billing API 建立一个合成客户、模型、采购报价/共享账户、Key 与双人核款；这不是 HTTP/浏览器经营验收。推理必须由客户 Key 经真实 HTTP 进入 main。

独立模型 fixture 仅接受固定 loopback Chat 请求、合成凭据和有界正文。两个一次性屏障分别在已读完唯一 POST、未返回 headers，以及 SSE 首内容 flush 后、最终用量前。等待受 run context 和 Close 取消，Close join 所有 handler；普通 mock 的五秒 WriteTimeout 不变。独立生命周期模式允许最多 320 秒有界 fixture 等待，不能用于非测试入口。

## 窗口与物理 oracle

1. **sending 已提交、上游响应前**：独立 fixture 收到唯一已认证 POST 并停在 headers 前；同一 run owner 读取唯一 request/attempt，确认 `delivery_state=sending`、客户预占与采购 reservation 均为原正上界、最终证据/结算均为零，并以DB时刻确认原request_deadline尚余大于290且不超过300秒；超出窗口失败，不修改期限补偿。此时精确 SIGKILL 本 run main，确认 OS 信号、Wait 以及 APP/projector 连接归零，再关闭/释放 fixture。不能把“正在 HTTP”或 sleep 当作 committed sending 证据。
2. **最终计量证据已提交、客户结算前**：SSE 首内容已由 fixture flush，客户实际读到内容；在放行最终 usage/EOF 前，owner 独立事务锁本客户 `ledger_accounts(kind='AVAILABLE') FOR UPDATE`。放行 fixture；等待真实 main 的 `RecordFinalization` COMMIT 已对独立查询可见（confirmed_final、content_written），且 settlements=0，同时 `pg_stat_activity` + `pg_blocking_pids` 确认本实际 APP LOGIN 正等待该屏障 backend 的 ledger_accounts 锁。`Settle` 与 `RecordFinalization` 是两次事务；不能锁住后者而误称命中窗口。

第二窗口从放行最终响应前的 monotonic 点起，到发出 SIGKILL，必须严格小于 1.5 秒；DB query_age 必须小于 1.5 秒，覆盖 billing事务实际三秒 lock_timeout（覆盖连接默认两秒）与五秒 cleanup 总界。实现采用更强的保守证明：该点至 kill/Wait/零角色连接全部完成的总时长也必须小于1.5秒，以覆盖信号调用前的调度暂停。未获得完整物理等待与可见 COMMIT 证据则失败。kill/Wait/零角色连接完成后才 rollback 屏障；不修改资金、lease、generation、request_deadline 或原计费事实来制造窗口。

## 原期限与恢复裁决

真实 inference middleware 固定 300 秒，main 重启 `DrainPredecessor` 必须自然等待原 request_deadline+5 秒；本片不缩短/重写该期限。每个 run 允许 380 秒，两个窗口整体至少需要约 11 分钟，使用预构建同 SHA 制品重启、相同 DB/secret/origin，无重新 setup、bootstrap 或生成 POST。记录重启 ready 时 DB 时刻已超过原 deadline+5 秒。

窗口1：原模型 POST 始终为1；恢复进入客户 pending_reconciliation 且原 held 保留；采购独立 unknown 且原 held 保留、spent=0；无新 finalization、settlement、journal 或采购重发。重复至少两轮正常 recovery 观察不会增加原事实。客户24h到期释放另属既有验收，本片不声称实等24h。

窗口2：原 finalization/计量证据只有一份，采购按该可信量已唯一收尾；客户由正常恢复使用原证据收费一次，held=0，余额/Key/账本与预期精确相等。再次等待两轮恢复及原幂等请求 HTTP 重放，不增加模型POST、attempt、finalization、settlement、journal或采购预算事件；重放必须返回HTTP 409、error.code=request_exists及X-Original-Request-ID等于原request.id，不能将任意4xx/5xx当作幂等证明；不重建已丢失正文。最终数据库关系只读核验并保留本片计数；不声称来源 lot 全域已接线。

## 证据与清理

安全报告仅包含窗口、二进制 SHA、用量/金额/计数、阶段时长、物理 oracle 布尔值、恢复裁决与清理结果；不输出用户/凭据/邮箱、token、DSN、正文、原SQL错误或秘密环境。最终正常 SIGTERM/Wait、fixture handler join、所有 listener/APP/projector 连接关闭、随机 schema/roles 删除、私密文件删除都须独立核查。失败也执行原生命周期清理，不得删除其他进程或 schema。

先验证独立 fixture 的取消/Close 和两屏障行为，再编译/默认 race，最后仅本 opt-in 串行执行真实 main。记录实际命令、制品 SHA、PASS/SKIP/失败及清理；不得把编译或本地 fixture 视为正式供应商、生产或 HA 通过。

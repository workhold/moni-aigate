# 024 验收草案

2026-11-01，**024-A生产源码已实现，真实PG、HTTP、浏览器与主进程故障专项通过；已完成限定本地验收，权限/全量race/019预览与最终架构检查通过，B–F未冻结**。已通过项依据[验证记录](../../docs/verification/operations-alerts.md)勾选；最终集成门禁已通过。本地fixture、PG状态模拟、真实进程故障和外部认证分别记录。A完成不代表B–F或024整体完成。精确判断见[合同](contract.md)。

## A0：冻结出口

- [x] 集成人接受范围/权限/开关、四kind/有界计数、滞回、状态机和安全DTO，冻结[精确合同](a-contract.md)与019；隔离原型与真实源码专项通过，主进程故障和浏览器已执行。
- [x] 模块设计与HLD/LLD/统一架构区分A的实际实现和B–F规划；最终内容快照已汇总复核并通过只读检查。

## A：来源、计算与显示

- [x] 持久channel quarantine触发/恢复；人工disabled、内存熔断、单个错误、credential状态或binding isolation不被误当该kind；无供应商网络。
- [x] 预算按limit-spent-held，0、负值、精确等于trigger、等于recovery、大于recovery、100位累计与int64阈值无损；paused账户仍可观察；明确登记预算而非真实供应商余额/023周期额。
- [x] 客户只数pending_reconciliation；采购只数unknown/disputed；各自600秒/7天和精确年龄边界、0/1/1000/1001条、重复时间稳定排序，1001显示至少且无虚假金额合计。
- [x] 年龄按reservation创建时间显示，不称unknown持续时间；客户24h释放不清采购unknown；源缺失/SQL超时/DB时钟回退保持原episode，不冒充clear。
- [x] 一个SQL观察时刻与MVCC源快照；源行并发变动不混合不同查询结果；不持业务行锁，不依赖报表投影和stdout日志，源索引EXPLAIN证明有界读取。

## A：生命周期与命令

- [x] 两worker并发首次breach最多一个episode；持续breach/hold不重复；clear结束后再breach新ID/generation，旧ack不继承。
- [x] 确认仅保留第一actor/time，不改源表、路由或资金；确认与恢复/配置竞争符合锁序，旧revision拒绝，已解决记录不能复活。
- [x] 配置实际变化（含label）原子结束当前episode、保留旧snapshot并重置runtime；完全相同配置不增长revision、不结束；关闭/退役/部署暂停/恢复模式的不同语义可观察。
- [x] 相同operation原请求重取原回执，异体/他人ID409，业务拒绝HTTP200+rejected；revision与样本更新独立；事件/审计/回执任一失败全回滚。
- [x] COMMIT不明仅查原operation；404不被解释成绝对未提交；前端不换ID自动重放。worker不明提交重读源只幂等决定告警，无生成/财务外部效果。
- [x] PostgreSQL屏障覆盖rule配置与检测、ACK与clear、两actor相同operation、退役与新同域规则、64/256上限并发、锁等待/取消和时钟边界；无反向业务锁。

## A：权限、数据库与秘密

- [x] 当前admin管理；三种平台角色读取/确认；用户停用、角色降级、MFA未完成、会话撤销、CSRF/Origin/近期再认证/恢复门禁均执行；客户角色与推理Key无法越权。
- [x] body/query重复/未知/null/超大、非法UUID/金额/revision/参数组合/游标全部拒绝；过滤变更不能复用旧cursor；每页重新校验当前读权。
- [x] 受限app不能DELETE、改历史event/终态command、替换旧ACK、改episode身份或不落事件直接关闭；projector无告警表权限，owner/migration独立。
- [x] 响应、日志、审计与浏览器无上游密钥/endpoint、prompt/response、邮箱/MFA、自由文本证据；精确DTO与no-store，样本/页面/响应/限流均有界。

## A：worker、资源与真实程序

- [x] 默认开关false；规则配置与读取可用但明确paused；开启后双槽/双连接、不预claim长队列、60秒正常间隔、失败30秒退避/退避防热循环、180秒stale按DB时刻计算。
- [x] 评估超时不使收费服务停机，真实Guard失效沿既有fence；恢复模式/关闭/退出停止新评估，不删除原告警、不造成恢复假象。
- [x] PG状态模拟分别覆盖COMMIT前后失败；真实main SIGTERM停止/取消/退出、专用pool关闭；真实SIGKILL并重启覆盖episode提交前/后，不把模拟当实际杀进程。
- [x] 独立随机schema/受限角色、无真实客户资金/付费调用的浏览器链路：创建规则→触发→确认→修改来源后恢复→再次触发→关闭/退役；核对当前/历史和清理；前端仅fixture不能替代真实HTTP/PG。
- [x] 新鲜度paused/never_evaluated/fresh/stale与last_error同时显示；至少计数、登记预算、ACK不等于修复、配置会结束episode均可理解；换身份/筛选/关闭页面晚回不污染新上下文。
- [x] 相关race/vet、前端strict及有意义专项通过；集成人汇总并复核统一架构模块/链路/数据/权限/状态/链接，显式refresh后只读架构检查和最终make检查通过。

## B–F：独立门禁

- [ ] B0/B1分别冻结计量基础及一个同步媒体profile；硬上界/两侧费率/来源分配/最终usage/未知计费与真实供应商账单认证通过后才发布收费支持。
- [ ] C0/C1冻结任务/幂等/回调/取消/结果期限/资金状态；提交未知不重放、跨租户/乱序回调/进程终止/长期unknown及真实任务验证通过。
- [ ] D0/D1冻结session/turn/event与持续预占/结算；资源耗尽、背压、断线/恢复、授权收紧及正式供应商/SDK用量认证通过。
- [ ] E0/E1冻结OIDC绑定/冲突/解绑/恢复与MFA；禁止同邮箱自动合并，真实IdP另验收；Passkey有独立冻结和浏览器认证，不借OIDC结果。
- [ ] F各站点运营/通知切片独立冻结/验收；外发通知不滥用021身份outbox，不把本地SMTP或回调fixture当真实收件/交付保证。

A不做性能竞品、支付、容器/1Panel或HA认证；对应外部门禁保留。没有这些证据时不得宣传完整New API替代、所有媒体支持或024平台整体完成。

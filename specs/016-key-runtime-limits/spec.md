# 016：Key 动态运行限额

2026-10-31，状态：实现与限定范围本地验收完成。承接NA-07，按SDD实现Key的RPM、突发容量和并发配置；来源策略015保持独立。其他Key属性、周期预算、平台/租户动态限额与分布式限流不在本轮范围。证据见[验收报告](../../docs/verification/key-runtime-limits.md)。

## 配置与权限

1. api_keys增加rpm_limit INTEGER（1..6000，默认60）、burst_limit INTEGER（1..200，默认20）、concurrency_limit INTEGER（1..16，默认16）、limit_revision BIGINT正int64默认1。RPM表示每分钟补充的令牌数，不是固定分钟窗口；burst是桶容量，不与RPM相加，也不要求burst≤RPM。0/null/负数不是无限。
2. 011追加迁移，不修改001–010。数据库约束保证范围，触发器强制INSERT revision1，仅三元组实际变化时递增，直接指定revision无效。来源、spent/held、撤销和其他字段变化不递增限额版本；限额修改不递增来源版本。
3. 创建Key原字段加三个可选数字；遗漏分别默认60/20/16，显式null/小数/字符串/越界拒绝。创建/列表返回安全三元组及limit_revision，仍只创建时展示一次秘密。GET响应不能用默认值掩盖字段缺失。
4. GET/PUT /api/keys/:id/runtime-limits。GET返回{id,rpm_limit,burst_limit,concurrency_limit,limit_revision}，可见性与015列表一致：当前active owner本租户全部，member/billing_viewer仅自己；撤销/过期仍可读，不需近期再认证。PUT完整替换{expected_revision:string,rpm_limit:number,burst_limit:number,concurrency_limit:number}，全部必填，严格有界JSON；规范UUID及正int64字符串，拒重复/未知/大小写别名/尾JSON。
5. PUT需session/CSRF、个人MFA及部署策略、近期再认证、ready/Guard、非恢复模式。锁序tenant SHARE→actor membership SHARE→actor user SHARE→Key UPDATE，锁后当前身份/角色/归属/撤销/CAS重判。owner本租户、member自己；viewer/撤销禁止编辑，过期可编辑但不延长期限。不可见/不存在/跨租户统一403。
6. 同tuple且当前revision为no-op，无新审计；旧CAS即使同值仍409。变更和key.runtime_limits.update审计同事务，只存before/after三元组和版本，不动秘密、来源、模型/地域/累计额度、财务历史。无持久命令回执，未知提交不能自动重放或只靠GET归因成功。

## 运行时准入和生效边界

7. 六种收费协议共用同一Key运行态。模型目录GET不消耗运行令牌；平台/租户速率仍在原入口检查，容量和授权约束继续共同生效。合法协议请求及候选路由准备后、Reserve前申请一次Key令牌+Key/租户/全局并发许可；本轮Key速率统计这种准入，预解析失败/无候选不会消耗Key令牌，平台/租户速率统计保持原有入口行为。
8. 不能用鉴权时旧principal的配置直接重设桶。WithKeyRuntimeLimits在tenant SHARE→owner membership SHARE→owner user SHARE→Key SHARE锁下读最新有效身份/Key和限额，在同事务提交前完成纯内存准入。PUT与该Key锁互斥：变更先提交则新准入看到新配置；已获锁且获运行许可的调用可按旧准入继续。
9. 单活动实例内用同一把运行态锁原子检查Key令牌与Key/租户/全局并发。容量拒绝不扣Key令牌，不产生预占、attempt或收费；429 rate_limited/concurrency_limited，运行桶状态不可用503 limiter_unavailable。数据库/权限/提交失败不得进入Reserve；已取得许可须归还，已消耗令牌不退款，防并发回滚覆盖新状态。
10. 每Key只有一份桶与在途计数，不能按配置revision建新桶。新桶初始为完整burst；观察更高revision时，以旧速率推进到当前观察时刻，再在原桶切换速率/容量，降低容量截断余额，提高容量不赠令牌。锁内保存单调观察时刻，反序到达的旧采样钳制到上一时刻，不能回退桶时钟或重复累计令牌。配置提交到下次运行检查观察之前按原速率，不能声称提交时刻即重算全部未观察期间；多次未观察修改按最终观察tuple切换。旧revision或同revision不同tuple必须fail closed，不能覆盖较新运行状态。
11. Key桶与通用登录/平台/租户桶分离，最多4096份且运行中不TTL驱逐/重建；满时新Key503，已有Key继续。低RPM/大burst不能沿用10分钟TTL重新补满。此为单实例开发版的显式容量边界；不实现分布式计数或持久速率历史。
12. 降低并发不清零计数，不中断已有许可；当前计数低于新上限才允许新增。提高上限也不得突破租户32/全局100/渠道及成员容量。运行许可包括后续短准入排队和整个调用收尾，真实资源退出后归还一次；safe retry继续使用原许可，不再扣Key令牌。
13. 已获运行许可的排队、已发送及在途SSE不因本轮限额修改撤回；来源/撤销/模型/资金的Reserve与派发复查仍按原规则。限额不是015的派发授权屏障，运行态准入与sending提交是不同同步点。修改不能阻断结算、释放、退款、恢复或完成调度。
14. 运行计数仅在活动进程内，重启后重新满桶；持久配置保留。原单实例锁、持久执行窗口、恢复/不重放门禁保持，不能通过并行启动多个实例扩大额度。RPM不是账务额度，完整资金硬预占继续保留。

## 控制台与验收

15. 创建表单显式60/20/16，严格整数输入，清空不得变成0/default。列表展示配置值及独立revision；来源与运行限额各自CAS/编辑器/unknown结果，账号/租户/角色与弹窗epoch屏蔽晚回，旧列表/GET不能回滚另一策略。关闭/卸载清理表单和明文，unknown关闭重开仍要求只读核查并显式新编辑，不自动重发。
16. UI说明令牌补充与容量、平台/租户上限、收紧不撤回已有运行许可、下次检查观察及重启满桶。GET只是当前观察，不能证明某次写入的归因。
17. 专用PG随机schema验证默认/输入/版本独立/CAS/权限/审计/约束/最小角色与真实锁等待；确定性时钟验证桶保持/上下调/旧revision/容量上限；HTTP六协议429零资金事实、并发收紧/排队和在途完成/许可归零。前端合同与strict build、全Go race/vet/build、隔离真实main浏览器代表操作及预览升级后交付。本轮不调用付费渠道、不改评审账号/MFA或真实资金，原生产与性能门禁保留。

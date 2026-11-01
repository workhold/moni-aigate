# T3 首片集成与架构验证

本记录保留精确提交 `f572bbd` 发布前的源码与执行身份；其远端 CI 后来失败，不能据本地串行结果声明该提交 CI 通过。当前维护集成隔离及重新验收见[隔离修复记录](maintenance-integration-isolation.md)。

2026-11-02。本记录将 ReadLease、账本/PB 两个维护 reader 的专项与整仓验证分开；本片不包含完整全来源组合、basis、epoch、生产维护 CLI 或资金写入。生产能力和精确专项证据见[读取租约验证](maintenance-read-lease.md)。

根串行执行原 reader 回归：设置专用 TEST_DATABASE_URL 后，`go test -race -p 1 ./internal/billing ./internal/periodicbudget -run '^(TestPostgresFundingLedgerRead|TestFundingLedger(Read|Maintenance)|TestVerifier)' -count=1 -timeout 600s -json` 退出0；34顶层、85父子终态全部PASS，零SKIP/FAIL。billing 23.050秒、periodicbudget 20.924秒；完整日志SHA-256为 `a6ef85d362f4d1cd9500db9f3ef805862521b69c8df36766cef95282a530f82f`。这包括原实际LOGIN/owner/RRRO前置、history/解析、账本完整分页/资源/取消，以及PB非空资金与历史缺证回归。

根与独立审阅汇合后同步模块依赖、维护生命周期、权限与局部结果边界，HLD/LLD/SDD/README及开发手册共同更新。十九张Mermaid图使用官方12.2.0和独立临时浏览器实际解析/渲染，四份正文及两张新PNG的SHA见[本轮图证据](architecture-maintenance-read-lease.json)；根实际查看模块图和维护细化图。T2旧图和旧CI证据保持原版本身份。

独立[只读清理审计](maintenance-read-lease-cleanup.json)在上述PG结束后执行：本轮fixture命名族的schema、roles、临时DB、backend和advisory均0，父public关系0且owner为pg_database_owner/默认ACL，DB ACL为null。PG16.15/UTF8/C/C、Asia/Shanghai、max_connections200及三个持久化开关on，client_connection_check_interval=0。首次审计将lc_collate/lc_ctype误作GUC而得到SQLSTATE42704，改读pg_database.datcollate/datctype后通过；始终只读且未清理资源或终止连接。该观察不替代测试内精确ID/shared-ACL检查，也不虚称独立前后配置基线。

首次整仓 `make verify-mvp` 退出2，354.342秒，日志SHA为 `f6ca0c3a0c060bc00e76023d4eee2b883d521fcd4d66c5ec7537fe722e422cf2`。四个顶层失败涉及CLI共同60秒、五个SIGKILL helper的12秒屏障及真实main启动/角色拒绝观察；没有DATA RACE或panic。前端286项通过，Go失败后尾部vet/build没有执行。原失败证据保留。新增依赖将固定catalog初始化带入所有child；按SDD保持固定root、完整认证及原期限，改用私有canonical span缓存、固定字段表预编译与expected专用等价严格parser。独立差分/边界/fuzz及测量见[启动记录](catalog-startup.md)，不作为网关首字节证据。

修复后串行执行下列真实专库回归；精确命令、日志SHA、每个run唯一终态及逐项skip名单见[机器记录](maintenance-read-lease-integration.json)。


| 回归范围 | 顶层 PASS / SKIP | 含父子 PASS / SKIP | 结果 |
| --- | ---: | ---: | --- |
| process-regressions-final | 4 / 0 | 9 / 0 | PASS |
| catalog-regression-final | 56 / 5 | 211 / 5 | PASS |
| lease-regression-final | 15 / 0 | 44 / 0 | PASS |
| consumers-regression-final | 1 / 0 | 3 / 0 | PASS |

四项原失败进程父测试与五个崩溃子例全部通过，原10/12/60秒期限和五秒catalog预算均未调整。catalog全包还包含完整普通/沙盒issuer与漂移负例；独立opt-in跳过仍逐项记为SKIP。lease回归仅选定ReadLease，不能称为重跑全部T0–T2；两个真实消费者经genuine Plan/Init使用新parser与缓存。

默认整仓与CI未启用的opt-in不计专项通过；下文单列最终串行整仓与后置审计，不覆盖两轮失败或历史源码身份。

第二轮默认跨包并发整仓仍退出2，359.245秒，SHA `20891063d96cd97dd5e57a707ad9b73bcc3581df306abe2f52f67390382891a6`；只剩真实main的10秒权限拒绝观察未及时完成，其余包均通过。独立审阅确认本地整仓应按同专库串行约定使用 `-p 1` 调度Go包，保留所有测试、race、包内显式并发和原10/12/60/600秒期限；不据串行通过声称并发启动余量。未落地的strip/key-intern探索仅留.local，不作为本版实现或发布验证。最终串行整仓实际结果另追加。

最终按包串行 `make verify-mvp` 退出0，1172.881秒，日志SHA `bceddd2f291e59c8711a4bb558dc0de93be6f746c3a4419ebf8b62ee87496b31`：前端286项及构建、39个有测试Go包race、4个无测试包、vet和可执行文件构建全部完成。Go包调度使用 `-p 1`；包内并发、所有既有测试和原期限不变，未打开的C0 opt-in仍仅按独立专项计证据。串行通过不证明跨包并发启动余量或网关首字节性能。

所有PG工作结束后，根在固定专库独立执行[后置只读审计](maintenance-read-lease-post-serial-audit.json)，SHA `4c675b02bf303f4c27cc0d2d74ca017a8bc26bd9f2cf24a14123560b40e335e1`：所观察fixture命名族的schema/role/DB/backend/advisory，以及父库其他backend均0；public关系0、owner/ACL和PG配置详列原始记录。没有清理、终止连接或连接moni_review，也不据此推断缺失的独立前基线。

独立最终审阅核对README启动CLI、三角色授权、Compose完整源码上下文及能力边界，并重算五份回归日志终态和SHA。开发/部署手册明确main未调用维护API但已包含传递初始化；LLD保留T1历史并指向当前§39。catalog-startup机器记录中的SDD合同SHA保留为新增串行执行策略前的历史身份，生产parser/cache/root源码SHA不变。提交后的远端commit/ref/tree与README字节由发布步骤另行核实，避免在待提交文件中递归记录自身commit。

最终文档澄清、后置审计和证据汇合后，再执行 `make check` 退出0，6.178秒，SHA `4d4f8f11c7baee9b8f62b83424496c106edb78d474afbd1d49fc70525a79bd32`。测试后只追加文档/证据并显式复核refresh；最后只读架构检查与diff检查另由提交步骤确认，生产源码不再变更。

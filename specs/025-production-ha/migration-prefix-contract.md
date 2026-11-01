# 离线历史迁移前缀安装与核验合同

2026-11-01，合同冻结，按本文实施。此能力只服务明确固定历史基线的隔离 fixture/离线工具，解决后续020/021加入embed后001–019实验被常规Apply静默升级的问题；不新增应用迁移，不是服务启动的降级/跳过迁移模式。

## API 与输入

```go
func ApplyThrough(ctx context.Context, pool *pgxpool.Pool, through string) error
func VerifyThrough(ctx context.Context, pool *pgxpool.Pool, through string) error
```

`through` 必须非空，且与当前 `//go:embed *.sql` 中某一文件名逐字节完全相等，如 `019_operations_alerts.sql`。拒绝序号、目录路径、大小写变化、空白、glob或不存在文件；不Trim、不猜最近版本、不接受任意文件/目录。先从embed解析完整有序清单并验证目标，未知输入在任何pool Acquire、DB访问或DDL前返回错误。合法目标选择按文件名排序的完整闭合前缀，包括目标自身；不能选择文件子集。

`Apply/Verify` 保持固定面向当前embed的**完整最新清单**，没有through参数、配置或环境变量可改变其目标。生产main继续使用常规API，服务role不获得迁移或DDL权限。前缀API不接受skip-checksum/allow-future等逃生参数。

## 安装与拒绝顺序

ApplyThrough沿既有session advisory锁`782193401`串行化，取得锁后先检查目标schema中**已有**schema_migrations全表：

1. 每个已有name必须在当前embed中；未知记录拒绝。
2. 每个已有name必须落在请求前缀内；即使是正确的未来embed记录，也拒绝，不能向已升级schema冒充历史基线。
3. 所有已有记录checksum必须与当前embed的SHA-256相同；先核完整历史，不能边检查边执行缺失的较早SQL。

这些预检拒绝不得新建表、追加迁移记录或执行任何migration body。schema_migrations不存在视作空历史，预检通过后才能创建metadata并安装前缀。不会从记录删除/改写、降级或自动清理未来事实。预检不修复非法SQL表结构，也不承诺抵抗owner在锁外恶意改schema；所有使用此installer的迁移者遵守同一锁。

预检成功后按完整文件名前缀执行缺失文件，每个文件仍是一个事务，body与schema_migrations记录同提交；已应用合法文件跳过。SQL执行失败保留先前已成功提交文件，沿既有前进迁移行为；“拒绝前无新事实”保证针对输入/历史完整性预检，不把多文件安装伪装成一个全局事务。正常取消/报错回滚当前文件，释放advisory锁。清理在发出锁请求前安装：锁请求返回错误意味着是否取得session锁未知，直接从pool Hijack并有界关闭物理连接；确认成功后正常有界解锁，解锁失败同样Hijack+Close，不把可能持锁的连接交回pool。

## 只读核验

VerifyThrough使用只读Repeatable Read事务检查同一快照：schema_migrations存在、前缀各文件完整、SHA-256全部匹配、无未知或前缀外记录。没有DDL、INSERT、UPDATE、DELETE、advisory写锁或自动安装行为；空schema核验失败，仍保持空。常规Verify对应完整最新清单，同样不因提供前缀API而容忍缺失最新迁移。

原schema角色、资金、业务、main/worker门禁都不变；migrationowner仅用于离线schema操作。此功能不新增生产运行选项，也不占020/021迁移号。

## 历史fixture固定点

- paymentsandboxqa application installer显式ApplyThrough(...,"019_operations_alerts.sql")，其资金fixture显式VerifyThrough同基线。
- 023-B实际Tx fixture显式安装同一019基线，再应用自己的设计SQL；不得由当前最新embed推导截止。
- 022-B1 `.local`原型在API就绪后也改同一显式截止。普通Apply/Verify调用保持最新语义，不能全库批量替换。

## 验证出口

未知/空目标传nil pool仍能正常返回错误，证明无pool/DB访问。专用PG随机schema覆盖空装、截止、重复幂等、只读Verify、checksum破坏、已知未来/未知记录预检拒绝，并比较拒绝前后schema对象与metadata/业务种子指纹；利用短前缀验证不能先安装缺失文件再发现后续坏记录。补充锁等待取消、该backend销毁、独立连接可再取锁及pool无lease泄漏；实际001–019安装/核验和相关fixture测试；所有资源finally清理。未运行测试不能标通过。

## 已实施的限定验证

2026-11-01 已实现并通过专用PG随机schema的 `go test -race -count=1 ./migrations`：空/非法目标nil pool前拒、精确闭合前缀/幂等、完整最新Apply/Verify、后续坏checksum在缺失002前拒、已知未来/unknown记录拒绝、拒绝前后catalog/metadata/tenant种子指纹相同；实际advisory等待后才读历史；取消等待后原backend不再回pool、释放原阻塞者后独立物理连接可取得同锁、pool无lease泄漏。最新专项2.088s，联回归1.965s，均退出0。

固定019调用已更新 `internal/paymentsandboxqa/{install,funding}.go`、`internal/periodicbudget/tx_fixture_test.go` 以及B1 `.local`原型。完整周期helper包在新前缀调用下PG/race通过23.474s；M/Q实际attestation/非空资金fixture与旧Alert角色+新周期角色联回归分别2.656s/5.908s通过。过滤联跑周期包无命中不能当再次完整运行；最终全量由根统一执行。未新增或改写SQL迁移，未启动main或外部PSP，架构由根汇总后复核refresh。

## 021之后的固定019资金QA核验（根冻结，待实现）

新版生产`billing.VerifyFunding/VerifyLedger`必须核对021的全部三来源，不能探表失败后退回旧核验。历史M/Q和手工020原型仍固定019，显式使用仅供QA的`internal/testsupport/funding019`，不挂到serve、restore或Rebuild，也没有运行时自动版本选择。

接口固定`const Through="019_operations_alerts.sql"`和`Verify(ctx context.Context,pool *pgxpool.Pool,tenant string)(Check,error)`；Check含Balanced、A原八计数MissingReceipts/OrphanReceipts/SourceMismatches/JournalMismatches/OutboxMismatches/RedemptionMismatches/BatchCountMismatches/ClearingMismatches，以及AccountMismatches/UnbalancedJournals。包不依赖billing；tenant为空或规范非零UUID，非法输入/nil/cancel/COMMIT失败返回零Check和安全error。

一个RR READ ONLY事务在同快照核对恰19份嵌入文件名与SHA，拒缺项/未知/020或更新的记录；拒当前schema任何`payment_` relation或function，允许历史prototype的`periodic_budget_*`。另外检查旧`funding_journal_valid(text)`的017静态prosrc摘要、参数/返回/sql/STABLE/invoker属性，拒伪造旧manifest的部分升级。不得以live目录作为expected。资金SQL使用静态017原谓词构成valid_funding CTE，保留完整A双向八计数；账户余额/分录和journal至少两条且NUMERIC SUM=0同statement核对，不调用生产新核验或任意live资金函数。规范schema引用、参数化值、COMMIT确认后才返回完整结果，全部扫描只读。

验收使用随机schema及完整非空人工资金链，核平台/tenant、NUMERIC、事实摘要不变；拒缺项/未知/未来/hash异常、B表/函数、替换旧函数、非法scope/nil/cancel，验证附加periodic原型仍可核。历史fixture使用此固定核验不构成生产降级能力，也不将它作为021新支付的验收。

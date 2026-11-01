# 022-C0：离线 metadata descriptor 生成器

2026-11-01，**冻结§9的导出器通过专用 PostgreSQL 16.15 限定验收**。依据[窄片合同](../../specs/022-self-service-payments/c0-schema-check-contract.md#9-根冻结窄片metadata-only-离线-descriptor-生成器)，本次只实现[`scripts/funding-lot-descriptor`](../../scripts/funding-lot-descriptor/main.go)，产物全部是 `unreviewed_descriptor_candidate`。未生成正式 expected，未实现 catalog checker、资金 epoch 或 RuntimeProof，也未改变生产迁移、main或资金行为。

## 实现与输入边界

生成器固定核验001–022规范 manifest、C0 DDL/ACL、六份原provision/migration源码和本工具全部Go/test源码；嵌入与工作树文件集合及逐字节内容必须相同。只接受环境中的专库连接，先核 literal endpoint、实际LOGIN/DB owner和PG major16，再创建独立随机schema/四角色。原migrations.Apply及ProvisionFor不被替换；sandbox在原ProvisionFor成功后插入固定合成marker，再运行原ProvisionPaymentWorkerFor。

每个profile先拍完整before，再安装候选C0并拍after；全部ACL/default ACL归RoleDescriptor，结构/owner/原函数正文和结构依赖归SchemaDescriptor。自动TOAST和内部RI以唯一结构符号映射；prosrc原文与SHA保持，禁止名字/数字替换。集合按稳定identity排序，有语义的列/参数顺序保留；差异文件中修改归changed，RoleDelta绑定前后规范descriptor摘要。

每profile先关闭自身Rows/事务/owner pool，按精确自有backend身份排空，再删除精确schema/角色；rollback、物理close、排空和清理共享10秒deadline。两profile清理成功并关闭最终admin pool后才写盘；输出目录须新建、0600文件、无覆盖/越界/静态symlink，失败删除自己的partial目录。未知CREATE/COMMIT不会重放，预登记精确清理意图；本次没有注入网络丢回包故障，不能据此宣称该故障已实测通过。

## 最终验证

- 无专库：`go test -race -count=1 ./scripts/funding-lot-descriptor` 退出0，2.292s，11项纯测试通过；两项真实数据库测试明确SKIP，这次不算数据库验收。
- 最终专库：仅通过进程环境提供 `C0_DESCRIPTOR_DATABASE_URL`，运行 `go test -race -count=1 -v ./scripts/funding-lot-descriptor -timeout 300s`；13项通过，退出0，115.415s。两轮独立随机安装/导出101.33s，中途注入无效DDL的清理12.07s。
- 最终 `go vet ./scripts/funding-lot-descriptor` 退出0。全部输入、七份工具源码、十文件完整SHA/大小和具体测试结果见[脱敏证据](funding-lot-descriptor.json)。测试完成后只读查询确认本工具schema、role、backend均为0。

两轮各包含ordinary/sandbox两个profile，最终同版本tool_sources稳定，十文件逐字节一致，summary列出的九项SHA/bytes逐项匹配，RoleDelta前后SHA匹配四份原RoleDescriptor；全部产物0600，不含随机schema/role名、DSN或业务行。固定manifest为 `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`，DDL为 `e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047`，ACL为 `9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae`。旧ACL版本不作为本次输入。

| 结构 | 原001–022 | C0后 | 增量 |
| --- | ---: | ---: | ---: |
| 业务表 | 109 | 125 | 16 |
| 业务列 | 976 | 1145 | 169 |
| 函数 | 156 | 197 | 41 |
| type | 218 | 252 | 34 |
| 用户trigger | 206 | 255 | 49 |
| 业务index | 274 | 331 | 57 |
| constraint | 924 | 1142 | 218 |
| 自动TOAST relation/index | 212 | 244 | 32 |

schema差异无删除，changed只有tenants一条：原八列不变，新增第9列nullable `fl_created_xid xid8`，无default/identity/generated/collation；其余变化为声明的对象新增。schema_migrations与全部业务对象的owner正确；原函数OID/prosrc及原table/type owner均不变。

## 权限与依赖的实际观察

APP在tenants保留八列INSERT、六列UPDATE；不能写第9列，UPDATE不含id或created_at。worker原有id列SELECT/UPDATE被保留。ordinary的worker是无base显式grant、无schema USAGE的placeholder，仍继承PUBLIC对113个原函数及218个原type的有效权；不能把它称为所有effective权限为零，更不能把这份四角色观测当作正式三角色ordinary expected。

新增34个type中，projector对16个自动table row type及16个数组的effective USAGE为true（默认PUBLIC）；对显式 `fl_source_proof` 及其数组为false。自动type的metadata/USAGE不等于table SELECT或function EXECUTE，后续正式checker必须逐类核精确矩阵，不能统一套用“全部type为零”。

原结构依赖有5,640个符号edge、5,652条raw edge；C0后为6,848与6,860。两者各12组 `pg_depend` 同raw edge、每组multiplicity=2；base/c0的 `pg_shdepend` 结构依赖分别266/324条且均单条。此处依据导出器本次成功执行的raw全字段identity守卫及normalized内容守卫，不冒称另跑过独立raw GROUP BY SQL审计。直接反例测试故意将两个不同raw ref映射到同一symbol，要求归并前拒绝；相同raw重复仍保留multiplicity。ordinary ACL依赖220→437，sandbox 298→515，均无重复组。

纯测试还覆盖临时源码镜像中的新增/缺失/修改SQL/Go/DDL/ACL/provision输入拒绝，固定endpoint、literal保持、identity变化归类、子对象闭合、符号双向唯一和已有/symlink输出拒绝。失败DDL测试只比较失败前后资源并按本轮精确拥有清单清理，不执行前缀清扫。

本片只证明固定输入的metadata提取忠实、可复现和限定失败清理。未通过actual APP LOGIN做资金/业务验收，未验证public namespace owner分支、正式ordinary角色变换、运行时schema门禁、维护Permit、完整资金scanner或外部支付/供应商认证。后续正式expected仍须人工审阅冻结，再实施checker；集成方统一更新架构正文、设计和变化记录并显式reviewed刷新manifest。

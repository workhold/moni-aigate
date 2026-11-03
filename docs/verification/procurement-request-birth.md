# A1 真实 request 自动出生：隔离候选续片

2026-11-04；初片基线 `1d45726`，矩阵续片基线 `71de18a`，等待/未知提交/catalog 续验基线 `3b528fb`。先冻结[真实父请求合同](../../specs/023-periodic-products/b-procurement-request-birth-writer-contract.md)与[窄定位集成合同](../../specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md)，再实现[独立 DDL](../../internal/procurementbirthschema/materials.go)、[writer / guard / 只读 validator](../../internal/procurementrequestbirth/materials/request-birth-writer-v1.sql)及[列 ACL](../../internal/procurementrequestbirth/materials/permissions-v1.sql)。下文区分初片和新增矩阵；完整 A1 矩阵尚未关闭。

## 已实现与真实执行

新 DDL 固定四表 71 列、request 四列、5 键、8 CHECK、10 条真 deferred FK；没有新宽 payload UK，也没有调用旧完整 685 项材料再删除约束。旧材料与迁移摘要不改。writer 为七函数、16 triggers、五个 deferred trigger；唯一 W-owned definer 为真实 AFTER capture，其他函数 INVOKER。W 仅通过 capture 依次生成 source→head→三 pointer UPDATE→step→最后 root seal。

APP 保留原 Reserve 的 21 列 request INSERT 与 status / billing_status 的窄 UPDATE，不授来源 DML 或私有 EXECUTE。W 对 origin、head、step 的三项锁列 UPDATE 由实际拒写 guard 保护；独立审查确认 root 的 UPDATE(id) 没有对应行锁路径，已删除该多余授权。APP 最终 COMMIT 的只读普通函数闭包、request 普通列与独立 xmin 读权实际参与测试。

[PG 测试](../../internal/procurementrequestbirth/request_birth_postgres_test.go)严格限制 PG16 专库、安装 LOGIN `moni_test`、`127.0.0.1:55439/moni_test`。被测来源由实际非 owner APP LOGIN 调原 `billing.Store.Reserve` 的真实 INSERT 自动产生，未使用 owner 手插来源替代。安装者仅准备父管理数据、原客户核款、合法历史、SQL / ACL 与故障触发器；W NOLOGIN、无 membership，测试没有使用 SET ROLE 充当成功身份。

```sh
TEST_DATABASE_URL="$PRIVATE_TEST_DATABASE_URL" \
  go test -race -p=1 -json ./internal/procurementrequestbirth \
  -count=1 -timeout=180s
```

根实际命令另用 `caffeinate -di`，原 RC、statement_timeout=5s / lock_timeout=3s 不放宽。最终整包 **5 顶层 / 22 子项全部 PASS，0 SKIP / FAIL，2.756s**，包括两个静态顶层和三个真实 PG 顶层；未设数据库时的 SKIP 不计本结果。本地专库无密码，不宣称已验证密码认证。

本轮实际范围：

- 原 APP Reserve 的 NULL / IPv4 / IPv6 来源，两种 streaming 值，手算预占 120，与原行逐字段对照完整来源 / head / root / step；幂等重放不变更已提交图或资金。
- 安装前真实 Reserve 的长 policy TEXT 保持未追踪，不被补来源；旧 018→022 历史中 UPDATE 后 DELETE 的原 profile 与 A1 行为对照；RR 只读认证继续通过。
- 十项 APP 保护写、五表 TRUNCATE、peer、owner 与 owner SET ROLE 拒绝，逐次比较完整行快照。
- origin、head、request pointer、step、root、reservation、journal line、audit、outbox 九处真实故障注入；原 PZ991 保留、18 张来源及业务表全行恢复 baseline，每次移除故障后以新请求正控制。

## 独立审查与失败修正

审查发现旧未追踪 request 同事务 UPDATE→DELETE 的延迟检查会误报最终缺行。现仅在原事件四 pp 全 NULL 且最终 origin / head / root / step 均无任何声明时保留旧行为，tracked 或任意来源声明仍拒；另补真实历史迁移边界的差分回归。

首轮三个 PG 顶层均在安装 writer 时因未给 CASE 表达式加括号而返回原 `42601 syntax error at end of input`，尚未产生有效出生证据。修正语法、同步固定 SHA，移除上述无用 root 锁权后重新运行整包，得到最终通过；未更改原 018 / 020 或禁用约束。静态检查不能替代这次实际装配发现的问题。

| 忽略的本地原件 | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/moni-request-birth-initial.json`，安装 FAIL | 4820 | `9856864006687c1d233b27cf6e4fe55360d489b86882a1d24f74bb114a9943ec` |
| `.local/moni-request-birth-reviewed.json` | 32302 | `07bed17e412ad0dfeccb5c7597ecd8f8feac132eb4422f902c9dd9937ae12653` |

## 预算、等待、误授与有效撤权续验

续片先冻结[预算/资金兼容](../../specs/023-periodic-products/b-procurement-request-birth-budget-contract.md)、[真实钱包等待](../../specs/023-periodic-products/b-procurement-request-birth-wait-contract.md)、[raw/误授/exact-link](../../specs/023-periodic-products/b-procurement-request-birth-raw-contract.md)和[必要授权失权](../../specs/023-periodic-products/b-procurement-request-birth-grant-fault-contract.md)四合同，再新增对应独立测试。原 fixture、writer/ACL SQL、固定摘要、001–022 和在线入口均未修改。根串行运行同一专库，APP 原 RC / 3s / 5s 保持。

| 专项 | 真实结果 | 已执行的准确范围 |
| --- | --- | --- |
| [预算/资金](../../internal/procurementrequestbirth/request_birth_budget_postgres_test.go) | 5 顶层 / 11 子项 PASS，7.764s | tenant×Key none/enabled/disabled 九配置；零售价、input-only、分组价格及缺组价不回退；pending、Release、无 attempt recovery 和幂等；PB reservation、pending case、PB settlement 三处故障全回滚 |
| [钱包等待](../../internal/procurementrequestbirth/request_birth_wait_postgres_test.go) | 首两顶层 / 两子项 PASS，7.550s；新增超时/取消一顶层 / 两子项 PASS，5.619s | `pg_blocking_pids` 证明真实 AVAILABLE 行锁等待；跨 scheduled price 仍冻结早报价；request/Key 过期全回滚；原55P03和精确backend取消57014，释放锁后新请求成功 |
| [raw/误授](../../internal/procurementrequestbirth/request_birth_raw_postgres_test.go) | 6 顶层 / 92 子项 PASS，4.208s | 等价21列raw与同xid双请求；缺reservation由原020 COMMIT 23514；三种非RC写隔离PP903；26冻结字段/五pointer误授、11派生INSERT、legacy补证、四表真实DML/TRUNCATE guard及22处capture内错边 |
| [有效撤权](../../internal/procurementrequestbirth/request_birth_grant_fault_postgres_test.go) | 修订后3 顶层 / 294 子项 PASS，15.926s | 291项必要权限真实true→false→true、原42501、18表全行回滚及完整canonical ACL恢复；两项未使用读取和PUBLIC重叠另作准确正控制，不计必要失权 |

随后根对**整个包**重新执行上方 `go test -race -p=1 -json ... -count=1 -timeout=180s`：最终 **23 顶层 / 423 子项全部 PASS，0 SKIP / FAIL，36.078s**，其中三顶层为静态检查，其余为真实PG。该总数已含初片5/22和各专项，不再相加。独立查询随机 `rb_*` schema / role 均为 **0 / 0**。真实模型、支付或网络供应商未参与本验收。

重要接受边界仍保留：fresh enabled PB 由原020在下一UTC边界生效，此片证明真实配置/追踪而非active cap拦截；双scope不重复扣钱包，zero价仍有唯一零额效果。input-only非零输出上界由原Reserve拒绝，非零输出价格由原catalog拒绝。没有finalization的live Settle只能pending；正向settled依赖尚未接通的dispatch/decision链，不能用owner手造最终证据补过。

等价raw已实测自动出生，原reservation companion可满足无PB场景原020关系，但**不证明完整Reserve的资金/Key/账本执行**。request guard按值不可变，误授后同值no-op允许；四张来源表任何UPDATE（含同值）由PP901拒绝。显式created_at INSERT依靠ACL，误授后无法区分合法值与DEFAULT；不得将其升级为独立DB采时证明。

首轮grant专项两项失败揭示了必要性假设错误：W的 `periodic_budget_allocations(subject_type,subject_id)` 有效失权后Reserve仍成功。原020 pointer UPDATE未改变 `(id,tenant_id,key_id)`，`AND EXISTS` 不执行；两读属于候选保留的保守权限，**当前路径并非必需**。按事先合同修订固定inventory为291必要项，加两个失权成功正控制；没有新增人为读取、删改原SQL或静默SKIP。原 `pb_require_read_committed()` 同时有PUBLIC/W授权，单撤W仍有效；只有明确保留APP并隔离PUBLIC后再撤W，才计真实W失权。owner隐含权与自动trigger直接EXECUTE不伪计失权。

| 本地原件（忽略，不含生产秘密） | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/moni-request-birth-budget-initial.json`，共享raw文件中间态尚缺helper，编译FAIL，未触达PG | 1517 | `1e4bfd9297e8dae10af0ce4b38597d11f51fb38ce73dba6055d48e328a597350` |
| `.local/moni-request-birth-grant-first-pg.json`，两项必要性假设FAIL | 349594 | `6399429af23da2f3b7c99c2e9bef111dc2b93bd9b4113abc6cc2c6b63b787e08` |
| `.local/moni-request-birth-matrix-final.json`，最终整包 | 513914 | `add5ca91bd221b3d250eeec1b7a05e4ed0052314d361260b385821db593c17f3` |

## 父行、PB 等待、提交回执与 catalog 续验

先冻结[父锁与提前约束](../../specs/023-periodic-products/b-procurement-request-birth-parent-wait-contract.md)、[PB 控制行等待](../../specs/023-periodic-products/b-procurement-request-birth-pb-wait-contract.md)、[提交回执丢失](../../specs/023-periodic-products/b-procurement-request-birth-commit-unknown-contract.md)与[实际 catalog/全 grantee](../../specs/023-periodic-products/b-procurement-request-birth-catalog-contract.md)四合同，再新增四个独立测试；没有修改原 SQL、摘要、fixture、001–022 或生产入口。实际 PG 仍由根串行运行，保留原 RC / 3s / 5s。

| 专项 | 实际结果 | 准确范围 |
| --- | --- | --- |
| [父行与约束](../../internal/procurementrequestbirth/request_birth_parent_wait_postgres_test.go) | 2 顶层 / 10 子项 PASS，5.212s | `pg_blocking_pids` 确认原 Reserve 等待选定父行；解锁前改 tenant/member/user 状态、Key 撤销/scopes、member 模型授权或 model enabled/profile，共八项。owner 变更后未解锁时独立采 26 表 baseline，APP 拒绝后逐行不变；恢复后新请求成功。另验 ALL IMMEDIATE 原020的23514，以及只提前 origin_complete 的A1 PP902 |
| [PB 控制行](../../internal/procurementrequestbirth/request_birth_pb_wait_postgres_test.go) | 2 顶层 / 6 子项 PASS，12.812s | tenant-only / Key-only 的原 LockCustomerTx 精确查询与 backend 阻塞证据；两项等待跨未来售价仍冻结原120，四项 request/Key 到期原错误且全图/资金回滚。不是 active 预算窗口切换 |
| [提交回执](../../internal/procurementrequestbirth/request_birth_commit_unknown_postgres_test.go) | 真实 PG 1 顶层 / 3 子项 PASS，2.011s；纯 wire 另有1顶层/6子项 | 包装实际 APP loopback socket，识别原 pgx simple Q commit 与真实 C/COMMIT 成功帧，只整帧丢弃该成功回执。原 Store 返回传输失败/零结果，独立 owner 可见完整已提交图、钱包9880/held120、Key/PB 与唯一业务伴随效果。同Store、新Store及换fresh RequestID的原幂等分支均指向原请求，快照不变 |
| [catalog/ACL](../../internal/procurementrequestbirth/request_birth_catalog_postgres_test.go) | 正控1顶层/2子项 PASS，2.173s；漂移1顶层/78子项 PASS，10.156s；另有静态inventory | 五关系104普通列、19 generated、38关系约束、8索引、七完整函数、20用户trigger及六constraint-trigger元数据；schema/role/OID、全部 grantee 原始 ACL/有效权及系统列。独立固定 SHA 原DDL参考取得PG16表达式规范形，未抄actual定义。78种漂移在其自身事务内准确拒绝，回滚后完整复核，最后actual APP正控制 |

COMMIT 证据同时要求真实成功 C 帧和另一连接的已提交可见性，发送 Q 或遇 EOF 均不能单独证明提交。纯 wire 控制只验证分段、非目标透传、目标零泄漏、畸形帧拒绝及 timeout 后保留半帧续读，不当成 PostgreSQL 验收。该场景没有 attempt 或模型派发，不扩称所有未知提交、跨进程恢复或生产网络均已验证。

catalog inspector 只在测试显式调用；原 fixture 安装、数据库只读 row validator、普通 serve 并没有自动获得这一认证，更没有发行 Qualified/profile。检查现有保守 allocation 两读及 PUBLIC 重叠权限时保留其原样，不把它们变为必要权限。

两项首轮失败保留：ALL IMMEDIATE 在原020的 AFTER 检查早于 A1 capture 时先报23514，初期期待PP902错误；已按真实顺序分开原020和A1的提前约束场景，未放宽成“任意错误”。catalog 初次正控误把 PG16 JSON 的 OID 当数值，原生 OID 实为 JSON 字符串；独立查询确认后对 inspector 查询的数值 OID 显式 `::bigint`（数组同理），固定2279/2278/25、源摘要和业务SQL均不改。

根最终重新运行**整个包**：**32 顶层 / 528 子项 race 全 PASS，0 SKIP / FAIL，62.292s**，其中五顶层为静态或纯 wire 控制，其余27顶层使用真实 PG。该总数包含上轮23/423和全部新专项，不重复相加。最终独立查询 `rb_*` schema/role 残留 **0/0**。

README与实际CLI/Makefile/Compose/env/runbooks已独立对照，GitHub默认分支确认为`codex/initial-mvp`，普通clone会取得交付分支；补明Gemini只接受Bearer及race需要CGO/C编译器。最终13份相关文本的1510个本地链接目标均存在，并修正新合同的一个附录锚点。架构细图使用官方Mermaid12.2.0与隔离Chrome156完成一图解析、真实渲染及截图检查；精确文档SHA `fd99c297134e0d15826c975dbc75d0065c6f7d2cb6c3130a02539d77a8f7944e`，图源码SHA `8060bfbc095be3d4e6d516a2d27c1cbcae4012de2c96278537c7aeb8def59990`。本地报告为`.local/architecture-mermaid-check/procurement-attempt-birth-20261104/report.json`，PNG为864×752、SHA `3088373fe3adae4c2bacb63468819acfbf0547c99cb57bab140eab65edf50a27`；不覆盖旧图证据，也不把静态部署说明称为容器实装通过。

| 本地原件（忽略，不含生产秘密） | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/moni-request-birth-parent-wait-initial.json`，提前约束期待错误 FAIL | 11584 | `7462fb8893370ed59a03e0c7ab95124749341138561e6a8b380158be0c17f3fb` |
| `.local/moni-request-birth-parent-wait-reviewed.json` | 13619 | `d79ce458698b47ea8ff356cd7993367dd088e5f20c373e84e900e1fd771f589c` |
| `.local/moni-request-birth-pb-wait-first-pg.json` | 9272 | `a79c82d1043db54c1836dcaf5a0c9232a96150a41c335bc0774f97cc00bf3c23` |
| `.local/moni-request-birth-commit-unknown-first-pg.json` | 5195 | `be3ca93c8572f99eaa323369f5566208e4351b8bfdcdac8c965780d29ec74f38` |
| `.local/moni-request-birth-catalog-positive-first-pg.json`，OID JSON表示 FAIL | 2084 | `e34ee9e86625c35662d7517eb209321860be91d288430222e92778990b9a2005` |
| `.local/moni-request-birth-catalog-positive-reviewed-pg.json` | 3773 | `a72a516fcd57bbb6060efe7f3764eb9b95259e2c980c65f86b6e70ecfdf1c725` |
| `.local/moni-request-birth-catalog-drift-first-pg.json` | 85273 | `55548fff40e3f568ec848ffb458b10003c42493bdb92dd079000997bbc6bfbb6` |
| `.local/moni-request-birth-expanded-final.json`，最终整包 | 635609 | `97befb8bfdedea0e529be0747246e2d6a34b075b789454cbe75c83185e893a8e` |

## 尚未验收与下一步

仍需active预算上限/争用、真实PB准入窗口跨UTC周期、原Reserve全体旧经营父锁权限撤还及其他未实测的源/root攻击。八项资格锁后重读、实际PB control等待、两种提前约束、成功后丢COMMIT回执和本页精确catalog已按上节关闭各自窄缺口；不能泛化到全管理权限或所有网络窗口，完整A1未关闭。

fresh control 的 tracking/effective 周期受原020真实当前/下一UTC周期约束，Commander不能提前生效；本次短程无法合法构造active cap或真实窗口跨期。后续需在真实前周期提交并保留的隔离fixture或先设计完整历史导入，不能改控制时间、mock时钟或把 scheduled 追踪成功称为active验收。这一时间条件不阻止未启用的A2源码实施。

[A2/A3 装配合同](../../specs/023-periodic-products/b-procurement-attempt-installation-contract.md)已经根审定完整 command＋request＋managed/unmanaged 接受集、12表262列、38原表扩列、137约束、68trigger及逐锁最小权限。它是下一实施合同，尚无可执行新包/SQL；不叠装旧account v1或A1封闭writer，不将旧PASS重新解释为新候选通过。

来源只认证真实初态和不可变快照，不认证完整 Reserve 资金来源或 retail_priced_at 的独立采时。A2/A3 attempt 与 reserved 效果、账户组合、全域 exact-link / ACL / profile、R2 与后续经营/生产阶段继续开放。

没有正式迁移、普通 `serve` 安装或在线权限变更。模块、实际时序、数据与权限见[架构细图](../architecture/procurement-attempt-birth.md)，原规则差分另见[A0 记录](procurement-attempt-rules.md)，两套结果不重复加总为完整业务验收。

初片汇合时，根独立观察 `rb_*` / `pp_at_*` 随机 schema 与角色均为零残留；新规则/关系材料 race 分别3顶层（2.105s）/4顶层（2.452s）全部PASS。初片统一架构1818文件快照和`make check`已通过。续片根语义复核模块、时序、关系与权限边界，1826文件架构快照刷新/只读检查、`make check`与最终Go二进制构建均PASS；检查含715列设计布局、全仓Go vet和Vue strict/Vite构建。README/贡献/安全/开发/部署文档已独立对照实际CLI、Compose和Nginx，链接与步骤通过静态核对；不替代容器/1Panel实际部署。本片没有重新执行整仓`make verify-mvp`，不据专项通过宣称完整商业/生产验收。

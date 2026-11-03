# A1 真实 request 自动出生：隔离候选续片

2026-11-04；初片基线 `1d45726`，矩阵续片基线 `71de18a`。先冻结[真实父请求合同](../../specs/023-periodic-products/b-procurement-request-birth-writer-contract.md)与[窄定位集成合同](../../specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md)，再实现[独立 DDL](../../internal/procurementbirthschema/materials.go)、[writer / guard / 只读 validator](../../internal/procurementrequestbirth/materials/request-birth-writer-v1.sql)及[列 ACL](../../internal/procurementrequestbirth/materials/permissions-v1.sql)。下文区分初片和新增矩阵；完整 A1 矩阵尚未关闭。

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

## 尚未验收与下一步

仍需active预算上限/争用、真实PB control等待及准入窗口跨期、原Reserve全体父锁授权/锁后资格重读、提前SET CONSTRAINTS、COMMIT结果未知和完整catalog/ACL等独立矩阵。本片已执行的钱包等待不能代替PB等待；原权限闭包的新增项逐撤不能代替所有旧经营权限。未获实测的源/root攻击仍按上位合同保留，完整A1未关闭。

来源只认证真实初态和不可变快照，不认证完整 Reserve 资金来源或 retail_priced_at 的独立采时。A2/A3 attempt 与 reserved 效果、账户组合、全域 exact-link / ACL / profile、R2 与后续经营/生产阶段继续开放。

没有正式迁移、普通 `serve` 安装或在线权限变更。模块、实际时序、数据与权限见[架构细图](../architecture/procurement-attempt-birth.md)，原规则差分另见[A0 记录](procurement-attempt-rules.md)，两套结果不重复加总为完整业务验收。

初片汇合时，根独立观察 `rb_*` / `pp_at_*` 随机 schema 与角色均为零残留；新规则/关系材料 race 分别3顶层（2.105s）/4顶层（2.452s）全部PASS。初片统一架构1818文件快照和`make check`已通过。续片根语义复核模块、时序、关系与权限边界，1826文件架构快照刷新/只读检查、`make check`与最终Go二进制构建均PASS；检查含715列设计布局、全仓Go vet和Vue strict/Vite构建。README/贡献/安全/开发/部署文档已独立对照实际CLI、Compose和Nginx，链接与步骤通过静态核对；不替代容器/1Panel实际部署。本片没有重新执行整仓`make verify-mvp`，不据专项通过宣称完整商业/生产验收。

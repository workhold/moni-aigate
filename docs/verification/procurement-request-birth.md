# A1 真实 request 自动出生：隔离候选续片

2026-11-04；基线 `1d45726`。先冻结[真实父请求合同](../../specs/023-periodic-products/b-procurement-request-birth-writer-contract.md)与[窄定位集成合同](../../specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md)，再实现[独立 DDL](../../internal/procurementbirthschema/materials.go)、[writer / guard / 只读 validator](../../internal/procurementrequestbirth/materials/request-birth-writer-v1.sql)及[列 ACL](../../internal/procurementrequestbirth/materials/permissions-v1.sql)。当前只验证本页列明范围，完整 A1 矩阵尚未关闭。

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

## 尚未验收与下一步

本轮尚未运行 enabled / disabled tenant / Key PB 及预算争用、零金额 / input-only / 价格组、scheduled 报价跨钱包/PB等待与过期、等价 raw INSERT / 缺 reservation 的 23514、初态 / 派生列 / 部分输入、逐冻结字段误授后篡改、缺多错来源 / 跨 root / 同事务多请求、RC 与其他写隔离、真实 SHARE 等待与锁后重读、超时取消 / 未知提交、逐项必要 grant 撤还与完整 catalog / ACL 恢复、pending / Release / settled 兼容。实现已有相应 guard 不等于这些矩阵已经通过；不能把十项无权写的 42501 当作误授后逐字段 guard 验证。

APP 同一权限下手写的完全等价 INSERT 会自动出生，这是保留原 21 列接口的信任边界；本轮还没有执行该专门用例。来源只认证真实初态和不可变快照，不认证完整 Reserve 资金来源或 retail_priced_at 的独立采时。完整 A1、A2/A3 attempt 与 reserved 效果、账户组合、全域 exact-link / ACL / profile、R2 与后续经营/生产阶段继续开放。

没有正式迁移、普通 `serve` 安装或在线权限变更。模块、实际时序、数据与权限见[架构细图](../architecture/procurement-attempt-birth.md)，原规则差分另见[A0 记录](procurement-attempt-rules.md)，两套结果不重复加总为完整业务验收。

汇合后根独立观察本轮 `rb_*` / `pp_at_*` 随机 schema 与角色均为零残留；新规则/关系材料最终 race 分别3顶层（2.105s）/4顶层（2.452s）全部PASS。统一架构已完成语义复核与1818文件快照刷新、只读核验；`make check` PASS，含715列设计布局、`go vet ./...`、Vue strict/Vite构建。本轮没有重新执行整仓`make verify-mvp`，不据专项通过宣称完整商业/生产验收；最终归档后再刷新并只读核验。

# 采购 attempt 出生：原规则与真实父请求

本页承接[完整 attempt 根](../../specs/023-periodic-products/b-procurement-attempt-birth-writer-contract.md)，按 A0 原规则、A1 真实父请求、A2/A3 attempt 与预占效果推进。它描述显式隔离候选；当前正式安装仍为 001–022，普通 `serve` 不安装这些材料。

## 模块、调用与状态

```mermaid
flowchart TD
    Original[原 billing / routing\n已运行的业务规则] --> Oracle[仅测试：原 Go 独立 oracle]
    Identity[procurementidentity\n既定六函数材料] --> Rules[procurementattemptrules\n五函数材料：A0 已实现]
    Rules --> PGTest[专库 actual APP\n路由字节 / UUID / Equal / 真实选价差分]
    Oracle --> PGTest
    Reserve[测试 actual APP\n原 billing.Store.Reserve] --> Birth[已实现的 A1 隔离候选\nsource → head → pointer → step → seal]
    Schema[A1 四表与窄定位关系\n独立材料及 exact-link] --> Birth
    Birth --> Matrix[真实 PG 验收\n预算 / 钱包与父行及PB等待 / 权限\n丢COMMIT回执 / catalog漂移]
    Rules -. A2 / A3 待接通 .-> Attempt[完整 pp_record_attempt\n真实 attempt / p / PE / account / PB]
    Birth -. 真实父快照 .-> Attempt
```

实线为已实现的材料依赖和隔离测试；虚线为后续依赖。A1 的实际执行限于显式专库装配，普通服务不安装它。规则包没有数据库连接、安装器或运行时业务调用，测试中的 `billing` 导入不会使其进入生产路径。

## A0 的实际规则边界

[新材料包](../../internal/procurementattemptrules/materials.go)只提供固定 SQL 字节、版本和 SHA-256；[规则正文](../../internal/procurementattemptrules/materials/attempt-rules-v1.sql)包含以下五个函数，全部 SECURITY INVOKER，撤销 PUBLIC EXECUTE，不授予角色或 DML。

| 函数 | 实际责任 |
| --- | --- |
| `pp_at_uuid_parse_v1` | 保留原 `google/uuid.Parse` 接受集，包括 38 字节任意 ASCII 包裹；不把原文字身份归一化 |
| `pp_at_route_json_v1` | 五字段路由、1000 项、完整 int64 / uint64、20 reason 与原错误分支；输出与 Go JSON 逐字节一致 |
| `pp_at_expected_shape_v1` | 检查 expected BYTEA 的存在性联合；保留空、NUL、非法 UTF8、负或 NULL rate |
| `pp_at_quote_equal_v1` | 消费时比较原字节与 nullable rate，沿真实 `CostQuote.Equal`；不代替 Known 或政策检查 |
| `pp_at_current_quote_v1` | 单条 SQL 用一个真实 DB 时钟稳定选价；无报价仍返回一行采时及 NULL 成本维度 |

前四个函数 IMMUTABLE / PARALLEL SAFE；只读选价函数 VOLATILE / PARALLEL UNSAFE，不自行取渠道锁。完整根仍需在真实锁等待后以新语句重读资格，保留原错误顺序与 5 秒 statement 界。

[独立差分](../../internal/billing/procurement_attempt_rules_postgres_test.go)直接调用原私有 `validID`、`validatedRouteDecisions`、`currentAttemptCostSQL` 和公开 `CostQuote.Equal` 作 oracle。它在随机 schema 中安装原 001–022、原 identity 和新材料，再由实际受限 APP LOGIN 执行。报价同生效时刻的旧数据在真实 012→013 迁移边界前准备，不禁用原 013 的新发布约束。CI 的 billing 分区显式开启专库变量；本地未设置变量时明确 SKIP。

## A1 与后续的设计责任

[A1 合同](../../specs/023-periodic-products/b-procurement-request-birth-writer-contract.md)与[exact-link 集成合同](../../specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md)冻结独立四表、request 四列和窄 locator FK，不先安装旧 685 项关系再删除宽 UK。原件与来源的完整 payload 由双端 guard 和最终只读核验逐列比较；单列原 TEXT 身份保留，不用 hash 或新长度上限替代相等。

原 Reserve 保留 APP 的 21 列 INSERT，故同一 actual APP 手写的完全等价 INSERT 也会自动出生。该来源只认证真实初态与冻结事实，不能证明完整 Reserve 已经受控执行；客户资金来源、零售选价时刻的完整资格、后续 request decision 和 attempt 根仍有各自责任。原 018/020 触发器、资金硬预占与最终 COMMIT 保持真实运行。

[独立 DDL](../../internal/procurementbirthschema/materials.go)与[request writer](../../internal/procurementrequestbirth/materials.go)均已实现；四表 71 列、5 键、8 CHECK、10 条 native deferred FK 配套七函数、16 trigger、五个 deferred trigger。W 为无登录角色，仅 capture 为 definer；APP 无来源写权或私有 EXECUTE，W 对 origin/head/step 三项行锁列的实际 UPDATE 由 guard 拒绝。原候选 SQL/摘要保持；在原预算/钱包/raw/授权四份专项之上，再增加父锁、PB等待、提交回执和catalog四份先行合同/独立测试。最终整包 **32 顶层 / 528 子项 race PASS，0 SKIP/FAIL，62.292s**，含五个静态或纯wire顶层，其余27个为真实PG；总数已含上轮23/423及初片，详见[真实 PG 记录](../verification/procurement-request-birth.md)。

| 新增验收面 | 当前证明与保留边界 |
| --- | --- |
| [预算与资金](../../internal/procurementrequestbirth/request_birth_budget_postgres_test.go) | tenant/Key九种配置、零价/input-only/分组价、pending/Release/recovery及三处故障；fresh enabled只证明配置与追踪，active cap和正向settled另验 |
| [真实钱包等待](../../internal/procurementrequestbirth/request_birth_wait_postgres_test.go) | 真实锁证据下保留较早零售价、过期回滚、55P03与精确57014；不代替PB锁等待或COMMIT未知 |
| [raw与误授](../../internal/procurementrequestbirth/request_birth_raw_postgres_test.go) | 等价raw/同xid双请求、原020缺reservation、写隔离、冻结逐槽、真实guard与capture内错边；request允许同值no-op，来源表任意UPDATE拒绝 |
| [有效授权](../../internal/procurementrequestbirth/request_birth_grant_fault_postgres_test.go) | 291必要失权、全行与canonical ACL恢复；原PUBLIC重叠单独控制。W两allocation SELECT因原020身份不变的AND短路当前不使用，另以有效撤权成功验证，不能称所有现有grant均必需 |
| [父行与提前约束](../../internal/procurementrequestbirth/request_birth_parent_wait_postgres_test.go) | 八项真实等待后的资格变更及26表解锁前baseline；ALL IMMEDIATE先由原020报23514，单提前origin_complete由A1报PP902，回滚/恢复正控完整 |
| [PB控制行](../../internal/procurementrequestbirth/request_birth_pb_wait_postgres_test.go) | 精确原LockCustomerTx和blocking PID，tenant/Key两种控制行跨价保留120与四种期限回滚；不证明active cap或窗口跨期 |
| [提交回执丢失](../../internal/procurementrequestbirth/request_birth_commit_unknown_postgres_test.go) | 实际APP socket的真实C/COMMIT帧零泄漏与独立已提交可见性；原Store仍传输失败，三种原幂等观察无重复资金/出生。纯wire六控制另列，不泛化所有未知窗口 |
| [catalog/全部grantee](../../internal/procurementrequestbirth/request_birth_catalog_postgres_test.go) | 独立固定DDL参考与正文pins，五关系104列/38约束/8索引、七函数、20trigger、角色/OID/普通和系统ACL；78项同事务漂移准确拒绝，回滚复核。仅测试显式调用，未自动发行profile/Qualified |

先行合同分别为[预算](../../specs/023-periodic-products/b-procurement-request-birth-budget-contract.md)、[钱包等待](../../specs/023-periodic-products/b-procurement-request-birth-wait-contract.md)、[raw](../../specs/023-periodic-products/b-procurement-request-birth-raw-contract.md)、[授权](../../specs/023-periodic-products/b-procurement-request-birth-grant-fault-contract.md)、[父行](../../specs/023-periodic-products/b-procurement-request-birth-parent-wait-contract.md)、[PB等待](../../specs/023-periodic-products/b-procurement-request-birth-pb-wait-contract.md)、[回执](../../specs/023-periodic-products/b-procurement-request-birth-commit-unknown-contract.md)和[catalog](../../specs/023-periodic-products/b-procurement-request-birth-catalog-contract.md)。仍需active预算争用、真实UTC窗口跨期、全体旧经营父锁授权撤还及其余未实测攻击，完整A1尚未关闭。fresh控制必须遵守原020下一周期生效，不能改时钟或手改control冒充active。原SQL/权限未变，验证修正不靠人为增加读取。

A2/A3 的[装配合同](../../specs/023-periodic-products/b-procurement-attempt-installation-contract.md)已审定独立 `birth-exact-link-a2-v1`，覆盖完整 command＋request＋managed/unmanaged attempt：12表262列、七原表38扩列、137关系约束、68trigger，固定leaf无递归图和每项显式锁用授权；尚未实现新材料。它将复用同源关系定义、identity/A0与账户纯函数，在干净前驱一次装配新writer/guard/validator，不叠旧account v1或A1封闭writer。原LR的Key/reservation锁只授created_at单列UPDATE并拒W真正写，APP原管理/资金面保留；未用credential/root锁权不授。真实p末序hook采原admitted_at，完整PB/reserved效果之后由private closure唯一最后seal。完整实现、catalog和actual APP矩阵仍是后续，不把本合同计作通过。

实际测试、失败修正和当前保留边界分别见[A0 原规则](../verification/procurement-attempt-rules.md)与[A1 真实父请求](../verification/procurement-request-birth.md)。来源纯核仍 `Complete=false`、Qualified 为空，无公共 Check；D1/I1、B-P3、R2、生产支付、真实供应商、目标容器/1Panel 与最终性能阶段没有因本轮局部通过而关闭。

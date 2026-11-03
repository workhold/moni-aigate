# A2 真实 attempt 出生与 reserved 效果：独立候选验证记录

2026-11-04；候选标识为 `birth-exact-link-a2-v1`。先冻结[安装合同](../../specs/023-periodic-products/b-procurement-attempt-installation-contract.md)、[完整出生根合同](../../specs/023-periodic-products/b-procurement-attempt-birth-writer-contract.md)和[exact-link 集成合同](../../specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md)，再形成[材料装配包](../../internal/procurementattemptbirth/materials.go)。本页区分源码交付、测试验收目标和实际执行结果；新增测试源码不代表 PostgreSQL 已通过。

## 候选材料与装配边界

新包独立渲染固定摘要的 SQL/ACL，不连接数据库、不安装 serving profile，不改变普通 `billing.New`、默认启动或 provision，也不追加正式迁移。原 001–022、旧 account v1、旧 A1 与旧完整关系材料及其摘要保持各自历史，不将旧 A1 的 PASS 计入新 A2。

安装基线是原 001–022＋B-P1，另安装独立 identity/A0 前置；受审安装者在同一事务执行新候选 SQL 与 Permissions。新候选不能混装旧 account v1 mutation/validator/trigger 或 A1 封闭 writer/validator/trigger。它仅按同源定义独立创建五个 `pp_ac_*` 纯函数，不通过先安装旧封闭候选再删除拒绝分支来扩展接受集。直接升级有业务数据的旧 A1 schema 不在本片范围内。

| 物理材料 | 固定候选范围 |
| --- | --- |
| 新关系 | 12 表、262 列；七张原表新增 38 个 nullable 列，无 backfill |
| 新约束 | 137 项＝22 PK/UK＋46 CHECK＋69 FK；不含原 001–022/B-P1 约束 |
| A2 的 59 条 FK | 12 条保留原 native；1 条复用既有双列 UK 并保持 NULL 激活条件的替换边；46 条 locator。另有 A1 的 10 条，合计 69 条 |
| 新函数 | 38 个：五纯函数、两权限检查、四只读 leaf＋五固定只读接口、账户 mutation＋两公开根、attempt 三私有 helper＋一公开根、capture＋其余 14 个 trigger 函数 |
| 实际 hooks | 68 个普通或 constraint trigger，按固定对象/事件/时机安装 |
| 公开业务根 | `pp_create_account`、`pp_configure_account`、`pp_record_attempt` |

关系与列序见[表](../../internal/procurementattemptbirth/materials/a2-tables-v1.sql)、[原表扩列](../../internal/procurementattemptbirth/materials/a2-original-columns-v1.sql)、[约束](../../internal/procurementattemptbirth/materials/a2-constraints-v1.sql)及[逐边 descriptor](../../internal/procurementattemptbirth/materials/relation-descriptor-v1.json)。函数/hook 的静态期望见[catalog descriptor](../../internal/procurementattemptbirth/materials/catalog-v1.json)，材料字节以[固定摘要](../../internal/procurementattemptbirth/material_pins.go)为准。这些静态 descriptor 不是实际 catalog 检查结果，也不发行 Qualified 或运行授权。

## 源码实现的真实范围

[request capture](../../internal/procurementattemptbirth/materials/request-capture-v1.sql)接在 actual APP 原 Reserve 的真实 request INSERT 之后，冻结完整快照，依次产生 origin、head0、pointer、step，最后 seal root。request head 保持 ordinal0/last NULL；此片没有 RequestDecision/latest 推进。旧未追踪原件不补来源，部分 pointer 或已有来源声明不能降级为 legacy。

[账户 writer](../../internal/procurementattemptbirth/materials/account-writer-v1.sql)支持 create/configure、同值 noop、revision_conflict、not_found 及匹配 replay；账户只读/guard 接受 command 与 reserve journal 交错，不把旧 command-only 接受集当成新完整前缀。账户合同见[账户设计](../../specs/023-periodic-products/b-procurement-account-contract.md)。

[attempt writer](../../internal/procurementattemptbirth/materials/attempt-writer-v1.sql)由公开完整 `pp_record_attempt` 执行原 checks、初次数据库选价及真实 upstream attempt INSERT，再按 managed/unmanaged 分支闭合来源：

- unmanaged 保留首次 quote；closure 不因跨 schedule 再选价。它有 attempt origin/head0、一个 attempt_birth step 和最后 root，无采购 p、PE、账户 journal 或 reserved transition。
- managed 按 account→supplier scope/control→head 锁序执行；等待后使用新 statement 重读并复核报价和 gate。在 p 的末序真实 BEFORE hook 中一次完整生成 origin/transition，使用原 p 的真实 PB admitted_at，不预采、不造 pending 来源。p RETURNING 后复核 gate、Key/期限与 quote，再闭合 PE、账户桶、PB、journal、head、node/step，私有 closure 最后唯一 seal。
- replay 仍先执行原检查。序号冲突后只比较四项原身份，返回旧五事实字段；`root_id`、`source_id` 为 SQL NULL，不重做准入，不补来源或 DML。

来源、指针、效果和最终反向关系由[只读 validator](../../internal/procurementattemptbirth/materials/validators-v1.sql)、[写 guard](../../internal/procurementattemptbirth/materials/guards-v1.sql)与[真实 triggers](../../internal/procurementattemptbirth/materials/triggers-v1.sql)共同闭合。deferred 暂态仅允许本 root 的固定顺序，不能提交成缺 p/PE/journal/PB/step/root 的状态；root 最后插入后不允许追加本根来源。

本片没有 dispatch/delivery、finalization/incoming、后续 cost/member/correction、proposal/approval、case/recovery/finish 或 outbox。表内保留未来 discriminator 或 pointer 列不表示分支已实现；合法未来域与损坏来源须分别拒绝。

## 身份、权限与时间条件

三公开根及 request capture 为 W-owned SECURITY DEFINER，其余函数 SECURITY INVOKER。W 为 NOLOGIN/NOINHERIT，不能成为表/schema owner；业务调用必须来自真实 APP LOGIN，owner 或 owner `SET ROLE APP` 不代替 session 身份。九个只读入口不加锁、不写入、不递归进入 writer；其 actual session 仍是 APP，current 可为 APP 或合法 W，允许 RR/read-only，不附加写根 RC/timeout 要求。

业务根要求 READ COMMITTED、lock_timeout=3s、statement_timeout=5s，内部 SPI 不重置时钟。APP 不获私有 helper EXECUTE 或原 attempt/来源直接写权；W 新表列权、原父锁列与 xmin 读取按[固定 ACL](../../internal/procurementattemptbirth/materials/permissions-v1.sql)精确授予。七原表父 guard 保留 APP 原管理/客户资金能力，同时拒 W 借锁用列权实际写父。详细授权责任见[完整 ACL](../../specs/023-periodic-products/b-procurement-full-acl-contract.md)和[PB ACL](../../specs/023-periodic-products/b-procurement-pb-acl-contract.md)。

## 审查中修正的语义

`pp_account_effect` 的四参 canonical 语义是 `(root_id,attempt_id,source_kind,source_id)`；四个 TEXT 的物理签名与 ACL 不变。出生调用为 `pp_account_effect(v_root,a.id,'attempt_birth',a.id)`，不能把第二参当作 source kind 或把第四参当作外部 effect。source kind 封闭为 attempt_birth/evidence_choice/approval_consumed/recovery；本片只消费 attempt_birth，attempt 与自然 source ID 相同且必须属于同 root 的真实 origin。合法未来域返回 PP906；损坏或陌生 discriminator 返回 PP902；NULL 协议错误返回 PP903。

managed PB proof 边界补回原 `AdmitSupplierTx→readSupplier` 的 UUID 接受集：p RETURNING 后、gate 前对 attempt/account/request/tenant/credential/channel/binding ID 调用原 `pb_uuid`，要求非零规范小写 UUID。该检查只作用于 managed PB 证明边界；unmanaged 保留原 TEXT 接受集，不能统一收紧原输入。actor 与 channel 的 SHARE 锁等待后也改用独立新 statement 重读，避免消费同 statement 等待前快照。

首次实际 PG 运行发现 unmanaged 被出生→p 的可空 FK 错误激活：单列非 NULL attempt_id locator 强迫本应没有 p 的分支存在 p。修复约定是 `pp_ad_birth_p` 复用原 014 的 `(attempt_id,account_id)` 既有 UK，保留 MATCH SIMPLE/DEFERRABLE INITIALLY DEFERRED；unmanaged 的 account_id=NULL 时关闭该边，managed 两列非 NULL 时真实激活。旧六列完整关系仍由 exact guard/validator 核对，不删除 FK，也不把此替换误称旧 native 六列原样保留。逐边 NULL 激活 registry 与修订后实际结果须由集成人最终核对。

## 已有 PostgreSQL 测试的验收目标

四份 PostgreSQL 测试使用专用 PG16、`127.0.0.1:55439/moni_test`、安装 LOGIN `moni_test`，拒非专库或 fallback。每个 fixture 创建隔离 schema、真实 APP/peer LOGIN 与 W NOLOGIN；不以 owner/SET ROLE 充当成功业务身份。owner 仅准备父管理数据、原核款、候选材料和故障/阻塞条件；request 由 actual APP 的原 `billing.Store.Reserve` 出生，attempt 由 actual APP 的完整 `pp_record_attempt` 出生。没有 `TEST_DATABASE_URL` 时会 SKIP，SKIP 不能记为商业验收通过。

| 测试源码 / 用例 | 已执行的限定范围，结果见下节 |
| --- | --- |
| [基础整链](../../internal/procurementattemptbirth/attempt_postgres_test.go)：`TestPostgresA2CommandRequestManagedAndUnmanaged` | 真实 unmanaged 与两次 managed；command→reserve→configure→reserve 交错；attempt replay 的 NULL root/source 和全快照不变；APP 验证 request/attempt/account/root；独立手算累计 held=480、spent=0、revision=2、account ordinal=4；noop journal、命令 replay、revision conflict 与 not_found |
| 同文件：`TestPostgresA2SupplierPBAndLaterImport` | `configured_before` 的新 allocation 与 `configured_after` 的真实 Commander 后配导入；分别核 origin=new/legacy_import、held=240、spent=0 与 attempt/account 完整前缀。它不是 active cap 或真实跨期验收 |
| [故障与权限](../../internal/procurementattemptbirth/attempt_fault_postgres_test.go)：`TestPostgresA2ActualBirthEveryDMLBoundaryRollsBack` | 已提交 managed 前缀保留后，在 attempt、p、origin/head/transition、PE、账户 UPDATE、PB allocation/event、journal/account head、step/node/attempt head 和 root 共 15 个 DML 边界注入 PZ992；逐次对 34 张原件/资金/PB/来源表全行快照，失败无部分出生，旧账户前缀仍能认证 |
| 同文件：`TestPostgresA2BudgetExhaustionHasNoPartialBirth` | 采购账户 limit=300，首次 U=240 成功，下一次 PP115；失败前后全快照一致，无额外 p/PE/source/head 或资金效果 |
| 同文件：`TestPostgresA2NoPrivateOrRawAPPBypass` | 八项原件/来源 raw 写及私有 helper 调用拒绝且快照不变；peer 普通调用 42501，owner 及 owner SET ROLE APP 的只读调用 PP901 |
| [真实锁等待](../../internal/procurementattemptbirth/attempt_wait_postgres_test.go)：`TestPostgresA2ConcurrentAccountBudgetHasOneWinner` | 两 request 共享 limit=300 的账户并发争用，恰一笔 U=240 成功，另一笔 PP115；独立核账户桶、head ordinal 与 p/PE/origin 基数，防止超额及部分来源 |
| 同文件：`TestPostgresA2AccountWaitRechecksQuoteAndRollsBack` | 先按原 channel 屏障发布 1500ms 后生效的真实报价，再持账户锁并观察 APP Lock wait，等待跨生效；解锁后 stale quote 返回 PP412，34 表快照不变。该用例未声称精确 blocker 身份、全部父锁或单根累计超时已验 |

[catalog 测试](../../internal/procurementattemptbirth/catalog_postgres_test.go)从固定原001–022/B-P1/descriptor和函数字节独立重建reference，在回滚子事务中比较12表262列/38扩列/137约束、38函数完整正文与安全属性、68新hook及全部原hook、APP/W普通与系统列effective权限、默认ACL和旧writer混装。每个fixture安装COMMIT前调用，另在提交后重复检查；不是生产Check、Qualified或profile。完整漂移与逐grant攻击矩阵仍未交付。

[静态测试](../../internal/procurementattemptbirth/materials_test.go)另检查 15 份材料固定摘要、38 函数/68 hooks 的装配、SQL 与 Permissions 分离、descriptor 的调用方拷贝隔离、恶意标识符单次 quoting 与无效绑定拒绝。静态装配不能代替实际数据库函数、trigger、ACL 或 COMMIT 执行。

## 首轮实现的历史执行结果

2026-11-04，最终15项材料摘要、关系descriptor设计来源及全部并行结果汇合后，在专用PG16串行执行：

```sh
TEST_DATABASE_URL="$PRIVATE_TEST_DATABASE_URL" \
  go test -race ./internal/procurementattemptbirth -count=1 -json
```

最终 **12顶层 / 25子项 PASS，零SKIP/FAIL，18.045s**。其中四顶层为静态材料/renderer/catalog inventory，八顶层实际连接PG；25子项包含15个真实DML故障、八项APP raw/private拒绝和两种PB配置顺序，未将安装catalog检查次数另算测试数。

原件为仓库忽略目录 `.local/moni-attempt-birth-reviewed-final.json`，42481字节，SHA-256 `e0f423018f78252c7c160595d9e314696ff5386fb09543bc0d9108aefa15e1e6`。最终`ab_*` schema/role均零残留；fixture保存并恢复安装者global FUNCTION默认PUBLIC权限，精确DROP OWNED清除临时W的global defaultACL，避免污染其他候选回归。普通应用的RC/3s/5s不放宽。

另串行保留旧材料回归，不能将它们与新候选测试加总成完整A2验收：

| 回归范围 | 实际结果 |
| --- | --- |
| 旧A1 request出生整包 | 32顶层/528子项race PASS，0 SKIP/FAIL，61.620s |
| 旧account writer整包 | 10顶层/478子项race PASS，0 SKIP/FAIL，15.074s |
| 旧A1关系材料 / A0静态包 | 分别4顶层、3顶层PASS，0 SKIP/FAIL |
| identity真实PG与静态包 | 首次广回归未设独立`PROCUREMENT_IDENTITY_DATABASE_URL`有1 SKIP；随后显式设置专库重跑，最终2顶层/6子项race PASS，0 SKIP/FAIL，2.383s |
| 原Go A0真实规则差分 | 显式`PROCUREMENT_ATTEMPT_RULES_DATABASE_URL`，只运行两项`TestPostgresProcurementAttemptOriginalRules/QuoteSampling`；2顶层/224子项race PASS，0 SKIP/FAIL，2.057s |

旧广回归原件`.local/moni-attempt-birth-prior-regression.json`，1238879字节，SHA-256 `3fcbb77af615c956efecc85cd14e9432e5e29e8792a6fc5a38759f95e17bf305`；identity补验原件9237字节，SHA-256 `ab948fa89909e840919e69972a3bcdcc37306794fb97b2565653252f784b15ff`；A0原件252491字节，SHA-256 `c29075ef4e6dc1e92a2aecc57517045bd7ef8bd196f6a987b0eac06c376f7dde`。文件均留本机忽略目录，未将工具输出或凭据提交Git。

初次PG装配除上述NULL激活错误外，还定位并修复shared guard嵌套SQL别名`ao`与外层rowvar同名的42702、原PB Commander审计写`created_at`缺少APP准确INSERT列权的42501。等待用例首版从已持账户的blocker发布报价，违反原channel→account锁序并真实40P01；最终改为先发布真实future-effective报价，再持账户跨生效，不改业务锁序或错误处理。首轮完整输出仍在`.local/moni-attempt-birth-first-full.json`；最终通过不是忽略失败或禁用原guard。

架构细图两块Mermaid均用官方12.2.0与隔离Chrome156解析、渲染和看图；证据位于`.local/architecture-mermaid-check/procurement-attempt-birth-a2-20261104/`。未操作用户浏览器，不把语法解析当作数据库验收。

## 扩展规则、请求兼容、授权与 catalog 反例

本轮只修改测试、安装验收合同、CI调度与文档，不改业务SQL、固定摘要、正式001–022迁移、应用权限或默认启动路径。

| 源码 | 验证责任 |
| --- | --- |
| [attempt_rules_postgres_test.go](../../internal/procurementattemptbirth/attempt_rules_postgres_test.go) | actual APP输入及错误优先序、忽略的expected BYTEA、宽TEXT/route JSON、replay及采购报价/未知报价/零额分支；失败逐次核34表全行回滚 |
| [request_compat_postgres_test.go](../../internal/procurementattemptbirth/request_compat_postgres_test.go) | A2下原Reserve与等价raw出生、冻结/派生字段、tenant/Key九种预算配置、零价/input-only/分组价、pending/Release/recovery、资金与内部DML故障、IP/幂等错误优先序、真实legacy不回填及提前约束 |
| [attempt_grant_postgres_test.go](../../internal/procurementattemptbirth/attempt_grant_postgres_test.go) | 固定1339项授权：1334项必要失权预期、5项有效失权但managed COMMIT成功正控制；另有21项W锁用原值UPDATE与1项APP账户锁用UPDATE拒绝控制 |
| [catalog_drift_postgres_test.go](../../internal/procurementattemptbirth/catalog_drift_postgres_test.go) | 110项固定对象/函数/触发器/角色/权限反例；同一事务改变并准确拒绝，回滚后完整接受，每个隔离fixture以actual APP真实业务再次正控 |

catalog inspector扩至权限材料涉及的50张关系，并核对固定依赖函数的正文、安全属性和固定owner；38个候选函数仍逐字节比较认证renderer输出。APP/W有效权限之外增加全部grantee原始ACL检查，避免PUBLIC/peer获得两业务角色本已拥有的权限而漏检。范围仍是test-only inspector，不发行生产Check、Qualified或profile。

逐grant实验先证明权限确实true→false，再由actual APP执行完整公开根及外层deferred COMMIT；恢复时精确比较all-grantee canonical ACL，复核已提交旧前缀和新根正控。失败需准确42501和34表全行不变，不能把owner、PUBLIC、继承或automatic trigger的直接EXECUTE误算作必要权限。

五项成功正控制为APP的 `pp_ac_text_v1`、`pp_ac_command_digest_v1`、`pp_ac_time_v1`、`pp_ac_snapshot_v1` 和 `pp_json_string_v1`，准确签名留在冻结矩阵。当前managed根先在W上下文初始化/执行相关纯表达式，PG16同事务simple-expression状态可被外层APP deferred复用。实测成功不能解释成函数从未运行或其它APP路径不需要授权；固定grant全部保留，不为测试增加人为调用。正控制确实提交第二笔managed预占，独立手算H=480/S=0/revision=1/account ordinal=3，随后恢复权限并以全新request核验。

首轮扩展整包为43顶层/1638子项PASS、1顶层/8子项FAIL，487.047s；失败均在旧APP deferred EXECUTE预期中。首个成功提交后共用request使后续发生replay级联；独立fixture重跑进一步证实上述另外四项COMMIT也成功。最终改为17项EXECUTE各用独立fixture，对五项记录真实成功，余项保留必要拒权。修订后该专项2顶层/17子项race PASS、0 SKIP/FAIL，54.044s；本段不把首次失败计作通过。首轮JSONL为`.local/moni-a2-hardening-final.json`（1974580字节，SHA-256 `6a894fcda8c9a65afce0b227fe42ec88cecf78b9ec3469e1e50c5fd4e3dc0ff7`），专项修订原件为`.local/moni-a2-grant-deferred-final.json`。

catalog拒绝只接受预期语义分类；SQL执行、reference重建、取消和超时错误不能充作漂移检出。四个顶层执行组保留全部110反例，metadata与ACL固定拆分后共15个隔离fixture，各自180秒、业务RC/3s/5s及每进程10分钟均保持。静态清单核对四组互斥、穷尽、无重复；CI对A2沿用完整顶层发现与C序轮转四分片。该调度只提供执行隔离，不是网关性能指标。CI同库包串行的故障依据见[包隔离记录](ci-package-isolation.md)。

最终串行整包使用`go test -race -p 1 ./internal/procurementattemptbirth -count=1 -timeout 600s -json`：**47顶层/1648子项全部PASS、0 FAIL/SKIP，535.086秒**，其中41顶层真实连接PG，6顶层静态；包含本页首轮业务及全部新增矩阵，不与此前专项重复相加。结束后专库`ab_*` schema/role均为0。原件`.local/moni-a2-release-final.json`为1981464字节，SHA-256 `4cdd53e79ac6a90a1d2fe663f8501986a45867ed0d9eba7af7c7d7fc9da71fe5`；[机器记录](procurement-attempt-birth-hardening.json)保存源码摘要、实际Go版本与范围。该结果仍不替代远端CI、全域来源认证或供应商/生产验收。

更新后的细图两张Mermaid均以官方12.2.0和隔离Chrome156实际解析/渲染，已查看变更的模块概览；证据在`.local/architecture-mermaid-check/procurement-attempt-birth-a2-hardening-20261104/`，不覆盖首轮图原件，也未操作用户浏览器。页面仅变化测试覆盖标签，真实请求时序与生产模块边没有改变。

## 仍未关闭的边界

上述扩展仍不关闭安装合同的完整矩阵：全部原checks与组合错误优先序、所有路由/重试分支及幂等冲突、绑定/首次PB配置双向等待、资格/expiry/真实UTC预算窗口跨期、active cap、原Reserve/A1与command-only逐grant、全部grantee提升路径、完整来源篡改/额外来源、真实取消/锁超时/整体5s、死锁重试和未知COMMIT继续逐项验收。当前1339授权仅覆盖冻结managed first-window场景，110漂移仅覆盖固定test-only inspector，不能泛化全域生产认证；DML故障也不等于全部来源攻击已验。

D1/I1、B-P3 历史命题、全域认证/完整 profile、A5 prospective executor、R2 正式迁移/权限/运行接线继续开放。没有接通真实模型生成或后台重发；供应商、正式支付、容器/1Panel、HA、生产验收和最后性能指标必须独立完成，不能由本地 fixture PASS 代替。

本片影响统一架构的模块、request→attempt→reserved 时序、来源/账户/PB 关系、身份/列权限及能力状态。集成人须在汇总代码与实际证据后更新[统一架构](../architecture/README.md)及[细图](../architecture/procurement-attempt-birth.md)，执行 reviewed refresh 与只读检查；固定catalog检查只用于测试；本片不自动启用普通运行profile。

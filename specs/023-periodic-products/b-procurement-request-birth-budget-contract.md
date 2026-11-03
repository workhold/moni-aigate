# B-P4-A1：真实 Reserve 的客户预算与资金收尾验收合同

2026-11-04；承接 [A1 writer](b-procurement-request-birth-writer-contract.md)、[最小 exact-link](b-procurement-birth-exact-link-integration-contract.md)、[原执行合同](b-procurement-execution-contract.md)和 [HLD](../../docs/design/HLD.md) / [LLD](../../docs/design/LLD.md)。**状态：已按本合同范围实施并通过专库验收；准确结果与尚缺边界见[验证记录](../../docs/verification/procurement-request-birth.md)。**

## 1. 目标、所有权与真实依据

本片只增加 `internal/procurementrequestbirth/request_birth_budget_postgres_test.go`，与现有 `procurementrequestbirth_test` 共用 `newFixture/input/snapshot/checkBirth`，不修改原 fixture、SQL/ACL/固定摘要、001–022、A0、A2、架构或运行入口。根统一串行执行专用 PostgreSQL；代理只做显式清空数据库开关后的编译。发现真实实现缺陷先报告，由对应 owner 修复，不能在本测试中补授权、绕过 guard 或降低断言。

被测写入必须来自真正 APP LOGIN 的原 `billing.New(f.app).Reserve`、`Settle`、`MarkPending`、`Release`；PB 配置使用同 APP 连接的 `periodicbudget.Commander`，价格组使用原 `catalog` 命令。owner 只准备合法管理身份/模型授权等父数据、注入/移除故障和独立读 oracle，禁止手写 request/origin/head/step/root、reservation/settlement/PB 效果冒充业务成功。

真实代码依据：[Reserve](../../internal/billing/reserve.go)、[Settle/MarkPending/Release](../../internal/billing/settle.go)、[PB Commander](../../internal/periodicbudget/commander.go)、[PB 客户效果](../../internal/periodicbudget/tx_customer.go)、[选价](../../internal/pricing/pricing.go)、[价格组命令](../../internal/catalog/pricing_commands.go)、原 [018](../../migrations/018_tenant_pricing.sql) / [020](../../migrations/020_periodic_budgets.sql)。普通产品原 API、资金/PB 次序与权限保留。

## 2. 公共基线与权限

每个独立 fixture 都保留现有合法 legacy request。为使新增预算金额有独立且简单的 oracle，可先通过 APP 原 `Release(not_sent)` 关闭该旧预占，核无本片来源补生、钱包可用恢复为10000/held0、Key held0；不 DELETE 或改写 legacy。owner 新建一个 active 平台 admin 用户供真实配置授权；Key Commander 使用现有 active owner 成员。不能把 finance_operator 改成 admin，破坏原双人核款基线。

A1 安装后不再运行全表 `ProvisionFor`，不加 TABLE SELECT/UPDATE，不增加 W/PUBLIC/peer 权限。原 APP21列 request INSERT 与 status/billing 两列 UPDATE、APP `SELECT(xmin)`、原 PB Commander/资金读取与写权以及 W 三列 pointer UPDATE继续按冻结材料执行。018/020 真实运行，不能替换 `pb_now`、配置时钟或禁用原 trigger。

## 3. tenant × Key 配置矩阵

tenant 与 Key 各取三态，完整执行九种组合；每组合独立 fixture，避免前例的旧 control、window 或资金计数污染：

| 状态 | 构造 | 对新请求的准确预期 |
| --- | --- | --- |
| `none` | 从不配置此 scope | 对应 request/source 的 PB marker 为 SQL NULL；该 scope 不生成 control/window/allocation/event |
| `enabled` | 通过 Commander 从 revision0 新建 enabled=true、`utc_day` 控制 | marker 是真实 control ID；新请求有一条 origin=new 的 allocation、真实 admission/window 和 reservation event |
| `disabled` | 通过 Commander 新建 enabled=false、`utc_day` 控制，limit0 | 同样保留真实 marker、allocation/window/event；禁用只关闭准入上限，不关闭追踪 |

**真实时间边界：** 新控制的 `effective_from` 由原020固定为下一 UTC 周期边界；fresh enabled 控制在本次正常短程验收中通常为 scheduled。测试核 actual receipt/control 与 DB 时钟，不改写 effective_from，不把 scheduled 通过写成 active cap 拦截验收。若测试恰好跨自然边界，使用足以覆盖本例的 enabled limit，仍按实际状态核验；active 超限、跨日/月、carry 与历史导入属于后续真实时间专项，不靠 mock clock 宣称完成。本片 `enabled` 是配置态，不承诺已证明 active enforcement。

九组合均 Reserve 标准文本模型：input100/output20、两价1000000，原 `billing.Calculate` 独立得 R=120。钱包只预占一次：available减120/held加120，Key held加120。每个已配置 scope 各一条 new allocation/event；两个 scope不把钱包或Key金额乘二。控制/window 的held增量恰120、spent保持0；allocation upper/source_held/held=120、source_spent/spent=0、revision1、state=held、origin_source_id=实际reservation.id。

request/source 的 tenant/key marker逐槽等于对应 actual control ID，`pb_admitted_at`与allocation.admitted_at精确相等；allocation.window_id必须指向同control且覆盖该实际 admission的原窗口。未配置scope不能用另一个scope的control或空串填槽。独立查原020 `pb_check_customer`可执行结果及真实ledger平衡；不以新validator返回成功替代金额检查。

## 4. 可真实到达的 pending、释放与 settled 边界

九组合在成功 Reserve 后调用原 `Settle(mode=live)`，因为没有 finalization，必须返回 `pending_reconciliation/delivery_unknown`，原次序 reservation pending → request billing pending → case INSERT在APP COMMIT成功。请求status仍accepted；source.initial_status/initial_billing_status始终accepted/reserved，冻结图不随当前态改变。钱包、Key held和PB held/window保持120，且不新增reservation effect或来源/root。可再调用原 `MarkPending`验证已有case的原更新路径，无重复case或资金效果。

随后调用原 `Release(reason=not_sent)`：唯一settlement为released、charged/raw/waived均0，原reservation/request billing为released，accepted请求转failed，case closed、recovery job移除；钱包/Key held与新allocation held归0，两个PB scope各只有一个对应settlement effect，allocation revision2/state released，spent仍0且原window不换。再次Release及Settle应读取同一settlement，不新增journal/事件/根；比较完整快照，并核source/head/step/root字节未变。

另外覆盖新请求直接 `Settle(mode=recovery)` 的真实无attempt分支：只产生released_no_delivery，不重发模型。A1未接attempt、dispatch与finalization/RequestDecision root，不为了制造positive `settled` 手插finalization、赋latest、临时授列权或owner写settlement。原真正charged_usage/settled（包括trusted zero usage）需要原finalization链，而当前A1故意禁止APP推进latest，因此**本片不计正向settled验收完成**；后续A2/dispatch/finalization切片按其合同交付。没有证据的live Settle只能pending，不能伪写settled。

## 5. 零金额、input-only 与分组售价

以下为独立的模型/售价变体，不把九种PB配置与所有变体机械做全组合：

1. **零售价**：通过原catalog发布真实有效的双0价格，新APP Reserve的R准确0，仍真实出生/唯一reservation/原journal路径；在双scope已配置时记录upper/held/spent均0但revision1的真实reservation effect，pending/Release后revision2，不省略零金额来源或伪造余额变化。
2. **input-only**：通过原catalog创建合法Embeddings profile（input100、context≥100、output/default_output0、无output field/工具），准备真实租户/Key模型授权并发布output价0。APP原Reserve以OutputLimit0成功，独立计算仅输入R；不调用供应商或把此案算协议认证。非零output上界由原Reserve以 `invalid_bounds` 拒绝；非零output价格由原catalog的 `PublishPrice` 以 `catalog.ErrInvalid` 拒绝发布。两个负例均核完整快照无变化，不能把catalog发布校验写成Reserve拒价验收。
3. **价格组**：用原CreatePriceGroup/PublishGroupPrice/AssignTenantPricing命令创建并赋予真实组，读取命令返回的immutable price ID与revision作独立oracle。Reserve必须冻结对应group、tenant revision、精确两价/price ID与真实retail_priced_at，不能退到默认价格。其后以原命令改组价或解除分组不改变已出生request/source，释放仍成功；验证旧快照而不是把当前配置倒填成期待值。缺有效组价负例应原price_unavailable且全回滚。

模型profile版本及授权只通过合法原管理父数据设置；不改原已经出生request的profile/quote/硬界。所有source冻结映射复用独立 `checkBirth`，另从原表/命令回执核业务期待值，不从新source反推价格oracle。

## 6. 时间、失败与完整快照

Reserve调用前后各读取真实DB `clock_timestamp()`；核原retail_priced_at与pb_admitted_at在该调用的真实采样范围内、两个source时点分别逐值等原request。它们是不同阶段的两个样本，不要求相等，也不以今天新采时间代替旧时点；原created_at、xid与三pointer保留准确出生值。跨等待重选Q2的专门并发测试不由本片普通成功样例代替。

失败快照基于现有f.snapshot，并追加它未覆盖而本片实际涉及的 `settlements/reconciliation_cases/billing_profile_blocks/periodic_budget_operations/periodic_budget_configuration_versions/price_groups/pricing_operations/tenant_pricing_assignments/price_versions`；资金/PB/source原全行排序保留。已完成的管理配置是失败前基线，不把正常配置变化算失败副作用。

至少在双scope配置后向真实 `periodic_budget_events` reservation effect、pending case INSERT、Release的PB settlement effect各注入固定PZ异常；仍由APP原API经过真实内部次序触发。失败后新birth/资金/PB/幂等/audit/outbox/recovery/case/settlement全部与基线逐字相同，已有提交request/root保留。移除故障后同类真实API成功；SQLSTATE保留，不把SQL错误当成功或返回伪回执。调用均不触发任何上游网络请求。

## 7. 实施与记录

根审定本合同后，代理仅新增指定test文件；不调用PG、不改共享fixture或候选SQL。编译使用显式移除 `TEST_DATABASE_URL` 及所有相关独立DB开关的 `go test ./internal/procurementrequestbirth -run '^$'`，只说明编译成功。根统一实际PG、集成架构复核和验证记录；失败/skip均不能标商业矩阵通过，整个A1也不因此升级为完整attempt、全域历史证明、正式迁移或生产验收。

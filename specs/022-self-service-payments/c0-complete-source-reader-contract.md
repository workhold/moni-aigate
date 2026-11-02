# 022-C0：同一租约的完整业务来源读取（根冻结）

2026-11-02。根依据现有21表financial reader、19表采购reader、PB公共Verifier、三份纯图合同及三路独立列/游标审查冻结本合同。目标是从同一原始快照完整取得七项来源算法的物理列并集，不把旧五类ScanProof相加。它是basis/epoch之前的实际读取关口，不增加线上writer、迁移、生产维护CLI或退款能力。

## 1. 范围与封闭接缝

新增无权威叶子包`internal/fundinglotscan`，只依赖标准库、pgx/pgtype与fundinglotverify。公开`Read(ctx, Query, schema, Mode) (Facts,error)`只返回私有owned session所生成的脱敏报告；假的Query或Mode只能生成无权威事实。Query/Rows仅为固定SELECT所需的Next/Scan/Err/Close窄接口，无任意callback、可填完成位、外部报告转proof或原始数据出口。

maintenancetrust追加第六个封闭`ScanCompleteSources`，旧五个枚举值、once规则及scope保持。新增`ReadLease.SourceMode()`只在有效完整来源lease内返回原Permit authority所持owned predecessor的ordinary/sandbox枚举，核其原身份绑定；不接受模式参数，不查当前policy推测模式，不发行新authority。维护集成`VerifyCompleteSourcesMaintenance(*ReadLease)`自行Require、取模式/current_schema、构造私有Query adapter、Read，并在成功后唯一Finish。私有结果绑定该Permit/kind/lease；外部不能提交Facts或provider换取可信结果。

原30秒lease总deadline、Snapshot更早期限、busy/取消物理关闭、安装/history/真实LOGIN/新鲜停服前置、Finish出口认证保持。新读取器不得调用旧wrapper、它们的Finish或其SQL业务判定；普通main与billing/PB测试依赖继续不包含maintenance/catalog。

维护wrapper由受审调用方独占同一lease，不提供任意并发构造的claim协议。Require尚未成功的busy拒绝不得由defer Abort扰动当前owner；Require成功后才接管失败清理。读取器已确定的Resource错误优先保留，不能被提前Close未EOF lease时产生的撤销context覆盖；原driver错误仍归Execution，真实到期归Timeout，不能把清理副作用当成caller取消。

## 2. 固定68张业务表及列并集

| 组 | 物理表 |
| --- | --- |
| Financial 21 | users, tenants, ledger_accounts, journals, journal_lines, credit_orders, funding_receipts, voucher_batches, vouchers, voucher_revocations, voucher_revocation_decisions, voucher_redemptions, commerce_commands, commerce_operation_receipts, audit_events, projection_outbox, api_keys, request_records, reservations, settlements, refund_orders |
| Procurement新增18 | channels, channel_credentials, channel_versions, channel_version_credentials, cost_versions, procurement_accounts, credential_procurement_bindings, upstream_attempts, procurement_reservations, procurement_budget_events, provider_cost_corrections, procurement_cost_proposals, procurement_commands, procurement_balance_observations, procurement_evidence_identities, metering_events, metering_event_conflicts, finalization_records |
| PB新增6 | periodic_budget_controls, periodic_budget_windows, periodic_budget_allocations, periodic_budget_events, periodic_budget_operations, periodic_budget_configuration_versions |
| Payment新增23 | payment_sandbox_environment, payment_runtime_attestations, payment_policy, payment_skus, payment_sku_versions, payment_sku_changes, payment_orders, payment_cancel_intents, payment_command_outbox, payment_command_observations, payment_query_attempts, payment_inbox, payment_event_conflicts, payment_verified_queries, payment_query_conflicts, payment_verified_evidence, payment_evidence_processing, payment_provider_revisions, payment_transaction_identities, payment_order_capture_selections, payment_captures, payment_effects, payment_review_facts |

字段集合严格取[financial合同](c0-financial-read-session-contract.md)、[采购23组](c0-procurement-evidence-graph-contract.md)、[PB18组](c0-periodic-budget-graph-contract.md)、[Sandbox33组](c0-sandbox-source-graph-contract.md)的逐列并集；不取SELECT *、JOIN过滤、关联父子子集、SQL SUM或按action/kind筛表。每张表只扫描一次，投影出多个算法的内存视图。schema_migrations和peer前驱/安装catalog继续外层认证，不计第69业务表。

实现使用包内静态typed binding声明：每个DTO字段依声明顺序映射到明确的物理列，JSONB转text，六个payment canonical原件bytea明确标注。根只从编译期声明构建固定SELECT列并集，不从live catalog或caller输入生成。共享表、键或字段类型冲突、DTO字段数不符、重复列及物理表数量不是68均是实现错误；验收逐列核注册清单与原合同。新reader的SELECT顺序取这些固定声明首次出现顺序，不改变旧reader顺序。

## 3. 资源、原生值与完成状态

同session仅有一份100000行/16MiB源预算，跨全部68表累加，shared列/行只计一次。TEXT/JSONB按实际UTF8 bytes，bytea按原字节，bool每字段1B，timestamptz envelope每字段32B（含NULL/infinity）；null TEXT/bytea为0B。单TEXT/bytea字段最多65536B。SQL逐行资格位和CASE有界投影防止超长字段直接传输；资格位不算业务列。该界不宣称PG排序、进程RSS或Go堆精确上界。

全部TEXT及整数/NUMERIC/OID/xid8读取为pg_catalog.text，保留NULL/空字符串与100位数值、负delta，不经float/int64汇总。bool用可空typed值；timestamp统一先收pgtype.Timestamptz，保留Valid、InfinityModifier和Time；bytea的NULL=nil、空值=非nil零长，owned clone。nullable canonical_parameters不新增lease codec能力，以局部[]byte映射其*[]byte DTO。

真实PG补验确认pgx在原强制simple protocol下以四位年份文本layout扫描timestamptz，合法year10000会在领域资格前失败。因此本第六reader的固定SELECT以`pg_catalog.timestamptz_send`传输原生8字节或NULL，包内只用pgtype固定binary scan plan解码到上述Timestamptz；保留finite/±infinity/NULL，不经文本、epoch浮点或格式化哨兵。SQL资格CASE仍对整行生效，时间预算仍每字段32B；不修改ReadLease的simple protocol或exact codec允许集，不开放调用方结果格式/自定义scanner。损坏的非NULL非8字节wire是Execution，不能当领域mismatch。此处为源时间的原生二进制传输，不把它当payment canonical bytea字段计数。

PB的时间envelope原样映射，包含公元1年Go零时间；其finite/NULL/infinity资格由PB纯算法按原合同判定。旧time.Time DTO只能在原生finite时原样转换，nullable时间的SQL NULL合法；必填NULL、infinity或不可表示的其它typed值不填zero、不造哨兵、不丢坏行、不降为SQL执行失败。

此类输入失配仍完成全部68表物理扫描，原native行留在私有session。每个受影响算法明确`input_mismatch`且不调用其旧DTO core、不伪造其scope/报告；其它输入可表示的算法继续执行。报告独立列出PhysicalEOFComplete、各算法执行状态、按物理字段去重的InputMismatchFields、CoverageComplete=false及确定mismatch。它不能声称坏值所在域的全部业务关系已核验，更不能授basis/epoch资格。finite但超出业务年份/精度的时刻仍能用time.Time表示，原样交core报告InvalidTimeRows；不把合法Go零时间当NULL。

各core保留自己的既有时间资格，不人为统一范围：例如PB原合同允许PG有限年份范围，year10000保持native且不新增PB InvalidTimeRows；旧业务域按各自1..9999等规则判定。传输保真不等于领域时间合法。

私有session保存68表EOF/行数和七算法状态矩阵。缺表/权限、Query/Next/Scan/Err/Close、游标、资源、context、core执行或Finish失败均固定error+零结果并Abort，不能留部分mismatch或proof。数据mismatch/indeterminate与上述执行错误分开；只有正常固定扫描及唯一Finish才可返回绑定结果，Finish本身不证明领域健康。

## 4. 分页与投影

每表128行keyset，首次状态独立bool。TEXT显式pg_catalog."C"与pg_catalog比较operator，不拼接复合键；空TEXT键不作首次哨兵。三张singleton以原bool的text C序枚举false/true，不能WHERE singleton。以下四类复合键逐项比较：pool(channel_version_id,credential_id)、identity(attempt_id,evidence_digest)、PB版本(control_id,revision)、payment版本(environment_id,merchant_id,order_id,provider_order_revision)；后两revision按BIGINT数值序及typed int64 cursor，不按十进制文本序。

其它按真实主键：如operation_id、attempt_id、query_id、evidence_id、capture_id、order_id及event_id，不发明id列。整128页必须续查，所有行Scan后才允许Next；真实Next=false、Err、Close和context均成功后，短页才能登记EOF。空表仍实际发SELECT。每页键严格递增；不通过重复/倒退游标形成假EOF或无限扫描。

## 5. 七算法与模式

所有物理EOF后依次运行ledger、manual/voucher receipt links、manual/voucher funding graph、customer Key financial graph、procurement evidence graph（内部仍同raw重跑原五core）、PB raw replay及sandbox stored-verified graph。每个算法仅从同owned原行投影；禁止外部摘要、PB SQL、写侧helper或旧ScanProof作oracle。原journal业务元数据的正int64 created_txid、finite非zero UTC微秒时刻资格额外保留为确定mismatch，不能因ledger四列摘要未包含它们而忽略。

普通模式仍全扫23张payment表：除policy外22表必须为空（包含query_conflicts本身），policy恰singleton=true/enabled=false/revision=1；另检查六共享旁路（commerce command/receipt kind、audit action的payment.前缀，sandbox_capture receipt/journal，SANDBOX_FUNDING_CLEARING account）。外层原mandatory FK和predecessor认证保持。精确满足时sandbox算法标记not_applicable；不向需要marker的Sandbox core填空图，不伪造consistent scope。sandbox历史存在而policy disabled仍按sandbox处理。

报告总体mismatch优先，随后indeterminate，再consistent。CoverageComplete要求全EOF、全部算法executed或合法ordinary not_applicable、无输入资格缺口，以及采购/PB/Sandbox各自封闭Unproven均为零；确定mismatch可以与完整执行并存。manual核心的UncoveredSandboxReceipts只有本次sandbox完整算法实际覆盖才可由组合解释，ordinary不豁免。该CoverageComplete仅指本固定业务来源图，仍非lot重放、protected init/post phase、basis/epoch或RuntimeProof。

## 6. 验收与交付

固定注册清单逐表/列核对；空/1/127/128/129/256/257行、空键/Unicode、bool false、四种tuple跨页及revision2/10；同字段/跨首末表的65536/16MiB/100000边界；NULL/空bytea、driver内存别名、nullable时间、±infinity、公元1年、超业务范围finite与NUMERIC100保真；所有行生命周期错误、typed-nil、CPU/SQL取消及错误全零。native资格失败必须证明其余表仍EOF、其它算法执行、坏域未执行且无完整覆盖。

真实Plan/Init均经原Acquire→Snapshot→VerifyInstallation→Attest→BeginScan(ScanCompleteSources)，验同RR旧快照、真实模式、68表完成、输入损坏与正常mismatch、ordinary精确排除、唯一Finish/跨Permit/取消/终结撤销。使用正式API生成资金/采购/PB/沙箱事实；专库串行，精确清理，不碰预览、PUBLIC ACL或别人会话。默认回归不替代opt-in专项；保留真实未执行项。

根汇合后交叉复核模块依赖、权限、能力状态和源码链接，更新统一架构、HLD/LLD、计划、README与验证记录，再reviewed refresh/check、make check/verify及提交推送。未知供应商、正式支付、容器/1Panel、HA与性能门禁继续原范围。

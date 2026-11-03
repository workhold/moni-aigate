# B-P3 完整预算证据版本组合验证

2026-11-03；基线 `ff812a3f983c2285a7d7fff973004f6f66c449fb`。本记录区分实现、真实 PostgreSQL 专项、默认回归及外部验收。合同见 [B-P3](../../specs/023-periodic-products/b-prefix-profile-contract.md)，模块和源码见[架构](../architecture/periodic-budget-prefix-profile.md)。候选只在 exact original22 后的隔离 schema 中安装；普通迁移、启动和在线来源钱包尚未接通。

## 材料与独立核对

| 材料 | 固定值 |
| --- | --- |
| source / wallet | 71表635列 / 87表804列，同session100000行16MiB |
| basis | 112表1004列131个PK字段，100000行64MiB |
| basis descriptor | 40655B，SHA `104914e6176e3d02a289e4e0acf0d476a69dcfb2e8772cf29336b520a7ec82bc` |
| catalog delta | 2739318B，SHA `2db8c8fc92e3cf56b2a5b335dd643818ed43d8b3c3e80a8bdb021a047be5bcb2` |
| ordinary root | `404c3b9d699a1a9ff7afcb62e2a21c7836a5e7217f20bf428ef1834c50903beb` |
| sandbox root | `486b753d8dbbd831fad4f78a6a8a969e6aeed77bc038f53eb2bdb88776f32c26` |
| 共用兼容材料 | 9711B，12条，SHA `9e35abdac16b9a1c9df1e644f89f9c0cf52d3f686b83c90a1623b600fbd1dd9c` |

`check-prefix-profile-delta.py`、`check-prefix-profile-roots.py`、`check-periodic-prefix-compatibility.py` 均以固定源码和原材料只读重算通过，不读取 live expected。[静态审定](periodic-prefix-catalog-static.md)保留准确对象/权限与PG16 builtin依据。[新读集](../../specs/023-periodic-products/b-prefix-readset.json)与原30项scanner材料分别认证，新旧manifest显式不重叠全集覆盖；旧catalog roots、basis descriptor/manifest/send、兼容材料与codec/golden不改签。

统一架构、README和本片细图共24个Mermaid图已实际解析/渲染通过；复核[总模块图PNG](assets/architecture-periodic-budget-prefix-profile-application.png)与[组合细图PNG](assets/architecture-periodic-budget-prefix-profile-detail.png)，后续仅修改缺证说明文字，图源逐字一致。关联文档1221个本地链接与27段Shell示例通过目标存在/`bash -n`检查；这不代表部署执行。准确图源与文件SHA见机器记录。

## 已执行分项

纯/race集成验证覆盖12条旧/fresh映射、跨mode/variant/root/descriptor拒绝、固定128/旧125锁目标、唯一Begin/安装/scan/phase机会、basis边界、伪造权限及脱敏结果。root consumer纯与race均14顶层/104含子项PASS，wrapper纯与race均4顶层PASS，零SKIP/FAIL；两包vet与格式检查通过。证据保留于本地结构化记录与最终汇总JSON。

专库实际端点为 `127.0.0.1:55439/moni_test`，LOGIN `moni_test`；全部PG专项由集成人串行执行，actual APP业务使用独立受限LOGIN，不以SET ROLE或owner代替。

- scanner：`TestPostgresPrefixSourceWalletReader`，1顶层/6含子项PASS，零SKIP/FAIL；包含同session物理读取与新generated字段，日志 `.local/b-p3-scanner-postgres-first.jsonl`。本项无维护authority，不冒充APP运行认证。
- basis：`TestIndependentPostgresPrefixBasis`，1顶层/9含子项PASS，零SKIP/FAIL，日志 `.local/b-p3-basis-postgres-first.jsonl`；原framing与静态descriptor独立回归。
- ordinary全阶段：实际restricted APP在首次reserve前配置tenant/key/procurement三个scope，充值1000、零售扣12、采购成本24，改变及no-op后，V3 Plan→fresh Init→post87→独立observer→actual APP RuntimeProof完成。原Base gap仍非零，新prefix完整；原事务wrapper替换和终结后拒绝，C0 writer不增加PB journal。首次完整专项该顶层31.58秒PASS，日志 `.local/b-p3-maintenance-postgres-full-first.jsonl`。
- 资源边界校正后：`TestPeriodicPrefixMaintenancePostgresSourceBudgetAbandonsInit`，1顶层PASS，零SKIP/FAIL，测试6.85秒；520条各32KiB合法audit事实越过共享16MiB，返回ErrResource、撤销原Permit/phase并关闭原backend，16张C0输出全部仍空。日志 `.local/b-p3-maintenance-resource-second.jsonl`；包72.048秒含静态材料初始化，不是请求或phase延迟。

## 首轮失败与校正

失败日志保留，不将失败父项或未运行子项计入成功：

- catalog首轮完整427.679秒失败：多数ordinary安装通过，但第一轮named-isolated的种子在空C0 epoch后创建tenant，被原 `fl_require_epoch` 正确拒绝（23514）；改用独立采购账户由实际APP产生PB历史。部分sandbox/旧基线正例亦失败，须进一步区分deadline、driver和结构差异；静态roots不从live回填。
- maintenance首轮94.356秒失败：ordinary与profile交叉选择通过；资源fixture漏装WalletReadV2四列权限，在进入预算测试之前被V3完整root拒绝，已补固定四列delta；sandbox安装核验仍待诊断。日志 `.local/b-p3-maintenance-postgres-full-first.jsonl`。
- 新wrapper首轮的V3fixture同样漏装四列delta；已在新fixture的同一安装事务追加认证原件。旧consumerFixture不修改，生产权限要求不放宽。

进一步定位确认sandbox catalog测试使用的admin pool保持默认search_path，未满足真实事务身份；新fixture显式创建正确schema的实际LOGIN连接，并在每组反例前用同一事务跑完整正例，防止因身份错误造成伪PASS。另实际APP正例已捕获5秒耗尽；driver以外的时间包含Go解码/规范化/排序、调度及环境暂停，不能全部计为CPU。

11:27–11:29的机器电源日志确认发生Maintenance Sleep/DarkWake，最长一次29秒；对应回归的墙钟与Go elapsed也明显不同。随后尝试临时保醒并串行重测，`.local/b-p3-maintenance-postgres-awake.jsonl`仍失败：真实报价fixture与成本缺证断言需校正，原/新catalog安装也有5秒拒绝。因此不能将全部失败归因系统睡眠。

独立串行race诊断 `.local/b-p3-catalog-timing-awake.jsonl` 在原LegacyV1第98个固定查询（application/type_acl，sentinel253）耗尽5秒，尚未达到权限快解码路径：总5014ms、driver587ms。CPU采样5.12秒中有4.39秒样本，通用`decodeCatalogJSON`累计1.73秒，race的`tsan_read`自身flat0.76秒。此证据定位到受instrumentation影响的旧公共解码/检查路径；未到root比较，不能报告为已经证实的ACL或root差异。局部优化范围、差分测试与准确性能含义见[CPU诊断](periodic-prefix-catalog-cpu.md)。后续系统日志确认该保醒命令仍未阻止Maintenance Sleep，诊断窗口11:39:35–11:39:40也休眠5秒；本记录撤回“完全醒时”的环境假设。CPU采样可以定位热点，不能排除环境暂停或将全部差异归因instrumentation。不延长产品5秒或30秒预算，不削减核验；标准PG与race分别记录。

当前记录将在校正复跑、旧接口回归、整仓检查和远端CI核对完成后更新。分项PASS不能关闭B-P3完整门禁。

## 标准 PostgreSQL 复跑

保持原catalog 5秒及APP组合30秒限制，单独运行不含race instrumentation的真实专库专项。它与之前race结果分别计账，不替代尚未通过的专项race门禁。

- catalog完整矩阵：2顶层/37含子项PASS，零SKIP/FAIL，包174.588秒。两轮独立名称/OID的五ordinary变体及完整sandbox均覆盖真实LOGIN、旧新布局互拒、固定root与完整APP读集；受控权限/正文/generated/依赖漂移均先在同事务通过正例。日志 `.local/b-p3-catalog-standard-final.jsonl`。
- wrapper：2顶层/8含子项PASS，零SKIP/FAIL，包21.108秒。实际维护连接完成source71与wallet87，核同kind唯一机会、profile替换拒绝和终结撤销；其中预开账完整图仍按原业务证据判断，wrapper扫描成功不等于开账成功。日志 `.local/b-p3-wrapper-standard-final.jsonl`。
- 第一轮标准maintenance包33.386秒，ordinary全阶段4.93秒、资源1.40秒、唯一机会0.39秒、genuine sandbox全阶段8.99秒及绝对更正缺证拒绝1.38秒通过；跨窗共享采购账户与旧epoch正例仍失败，不把失败父项计为PASS。前者额外暴露`AccountAdmissionOrderUnproven=1`，后者对已终态旧请求首次配置tenant范围控制，原checker正确保留`LegacyMembershipUnproven`；正例须使用与旧请求无关的新key scope，不能消除该gap。日志 `.local/b-p3-maintenance-standard-final.jsonl`，后续仅修新fixture与准确缺证断言。

标准单次诊断在实际APP完成194个固定query，V3发行1938ms；这是catalog定位结果，尚非完整组合、网关请求或生产性能。权限解码器真实差分fuzz已完成baseline5/5与1000次执行；首次5秒启动未执行到输入的旧fuzz不计有效通过。原件SHA、最终源码身份、全部保留失败和最终命令结果见[机器记录](periodic-budget-prefix-profile.json)。

修订后标准maintenance全量：7顶层/13含子项PASS，零SKIP/FAIL，包76.741秒，日志 `.local/b-p3-maintenance-standard-reviewed.jsonl`。普通与genuine sandbox均完成protected pre/basis/Plan/Init/post、独立observer和原actual APP RuntimeProof；资源界、安装机会互斥及终结撤销继续通过。旧109表basis发行的epoch保留全部不可变原件，新候选后只为此前没有请求的key建立新scope，完整87表图、历史tuple映射、observer与APP认证通过；另一个真实缺origin负例继续拒绝。

跨窗用例分开验收：17条命令、23条观察、6个window、12条allocation、21条event及真实锁阻塞的PB prefix完整；同一共享采购账户的4个来源含终态事件，原core仍保留唯一`AccountAdmissionOrderUnproven=1`，owned Plan完成71/635物理来源读取后拒绝、未进入basis、无COMMIT或16表输出，PB原件未变。绝对成本24→25更正准确保留两项原缺证，各1且确定mismatch全零。它们证明缺证失败关闭，不作为完整成功门禁。

## 采购历史的未完成门禁

真实双人绝对采购成本更正会保留审批前的proposal revision及审批后的reservation revision；当前独立重放还缺少审批时点的完整不可变屏障证据。原checker保留缺证，完整source扫描及预算prefix通过也不能覆盖它，受保护Plan必须拒绝且不写入C0输出。准确代码依据、缺证集合与下一份SDD的设计问题见[采购屏障缺口](../../specs/023-periodic-products/b-prefix-procurement-barrier-gap.md)。共享账户多来源及终态事件还会保留`AccountAdmissionOrderUnproven`；跨窗与并发观察测试分别证明PB prefix和原采购缺证拒绝。拒绝用例通过仅证明失败关闭，不能代替合同§6的完整经营成功门禁；B-P3整体保持未完成。

## 尚需独立验收

正式migration/provision/维护CLI、历史材料升级、startup/ready与在线来源资金写入仍为下一依赖。原路退款、拒付、商品订阅、媒体协议、真实供应商、正式支付、Docker/1Panel、HA与竞品性能不是本片验收范围。默认整仓/CI的独立opt-in SKIP必须另记，不能以包PASS代替真实PG专项。

## 整仓检查与环境限制

`make check`退出0，报告执行时间10.188秒、墙钟15.983秒，完成只读架构检查、全Go vet和前端strict构建。随后`make verify-mvp`启用专用`TEST_DATABASE_URL`，286项前端通过；默认Go race全仓已执行：50个测试包PASS、资金包1个FAIL、4个包无测试，命令退出2；报告执行时间2173.090秒、墙钟3220.200秒。已有资金包的`TestPostgresKeyConfigurationExpiryAfterUnchangedLockWait`与`TestPostgresRetailWalletWaitKeepsAlreadyFrozenPrice`出现statement/lock timeout，该包268.439秒FAIL，不能标为整仓通过。

12:01开始的电源日志确认测试期间反复Maintenance Sleep/DarkWake，临时caffeinate未能阻止电池供电下的休眠；本轮采集区间内最长56秒。不能仅据此证明每项失败的唯一原因，也不能延长产品资金期限来获得PASS。标准PG专项、默认race、完整命令结果及其未执行后续步骤分别记录；远端CI只按精确提交确认，不转移旧提交结果。`make verify-mvp`在测试失败后没有执行其末尾vet/build；全Go vet已由`make check`通过，另独立`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate`退出0，1.642秒。本轮提交时远端CI尚待推送触发，结果按精确commit与Actions运行核对。

# C0 受保护初始化事务验证

日期：2026-11-03。基于 `6497f43cf2985a2991801a8e098766f2c3784b5f`；精确源码、命令、日志摘要与最终结果见[机器记录](protected-init-phase.json)。本片按[冻结合同](../../specs/022-self-service-payments/c0-protected-init-phase-contract.md)交付库级候选初始化，不修改原001–022或固定候选DDL/ACL。

## 变化与独立复核

三个并行任务分别负责私有phase/封闭rows/唯一提交、固定writer与资源计数、strict v2真实发行与写后核验。根汇合复核并串行执行真实PostgreSQL；新增public接口仅原Permit上的两个同步方法，Expected是五个普通字符串，不接外部Plan/Facts/SQL/callback。原七ScanKind保持，第八once专用于完整phase。

交叉审查补强了三处状态机：writer仅原Init Writing阶段；issuer仅Draft、postproof仅Post；advance按purpose白名单限制合法边。写前重新求和实际12组行数加两行metadata，与DataRows/GeneratedRows一致后才按全68预算，而非只信Metadata。它们均在最终专项重新验证，不把早期编译或探索性运行当最终身份。

## 真实专库与业务正例

显式同时开启 `C0_MAINTENANCE_DATABASE_URL` 与 `MONI_TEST_DATABASE_URL`，均指向专用 `127.0.0.1:55439/moni_test`，PG16.15；配置由原fixture严格核对。ordinary使用随机隔离schema、独立最小LOGIN及原001–022+固定候选材料；sandbox使用genuine immutable v1 QA installer及真实已付款来源。没有停止共享集群或预览、修改集群设置或终止无关连接。

根新增测试实际经过Acquire→Snapshot→VerifyInstallation→Attest→PlanFundingLots成功只读提交；新Init事务重新读取全部来源和basis、写入14组固定表、flush、实际84表及strict v2/freshcatalog/post109核对后唯一提交。然后由独立连接RR读取真实已提交84表、严格v2和109basis，不用纯Plan虚构数据库post。

- 空安装与三个旧零钱包：含合法空TEXT、Unicode旧身份、无账户及已有零AVAILABLE账户。每旧tenant含0 opening、seq0/head都保留；没有发行零额lot。原空安装发行器继续拒绝已开账环境。
- 混合业务全部由C0安装前的原billing API产生：manual/voucher各两条、两结算、两个approved分次退款及一个pending、正未结预占、R0未结和R0终态。手算T3/F4/S2/Q2/U3、6openings/13legacy sources、4lots/2allocations、Data47/G49；零值legacy membership恰3。
- M=9223372036854775807。独立手算available总额=M+65=`9223372036854775872`，held总额=M−80=`9223372036854775727`；第二tenant A=M−50、H=M−120，各桶合法，operation issuance=2M−170=`18446744073709551444`保留NUMERIC。两个平台清算账户各恰−M，不借账户溢出制造测试数据。
- 旧空TEXT及Unicode有资金场景另用原SQL历史fixture，在C0安装前保持全部原触发器/FK/deferred约束开启，构造两笔manual入账100与40；真实NULL平台clearing=-140与非NULL空tenant账户分离。手算T2/F2/A140/H0、Data16/G18；完整Plan→Init后，独立84/strictv2/109及逐tenant opening/lot/seq0/来源ID区别检查。此历史SQL测试不声称原在线Go入口接受空tenant。
- genuine sandbox：保留原v1 canonical完整bytes/SHA；原资金事实在初始化前后相同。新v2从真实owned catalog/pins/predecessor/manifest/basis构造，非手填维护证据。

## 故障、状态与资源

实际SQL故障oracle覆盖原出生事务列非NULL、删除旧零opening、SHA格式合法但epoch基线错误、修改84未覆盖的recovery_gate业务事实。测试仅在自己持有的原事务故意改一项，并恢复原trigger/canonical catalog，再调用同一个生产private post方法。缺opening由实际84重放拒绝；后两者的84保持consistent，分别被strict证明绑定及第二次109basis比较拒绝。均未发送COMMIT，无持久化epoch或测试篡改。

wire屏障不修改实际SQL：等待服务器第一笔metadata写入回复后并发尝试Commit/Close/新scan/Plan均busy且无额外SQL；caller取消关闭原socket，结果零、状态Abandoned且数据库无epoch。另一屏障等待COMMIT真实完成并以独立连接证明持久化，再丢弃驱动回复或取消caller；两者均ErrCommitUnknown/零结果/OutcomeUnknown，只发一次COMMIT，不回滚、不重做初始化、不换epoch。

七core的旧tenant纯测试分别覆盖manual来源+ledger、Customer四种请求/退款状态、采购binding/approval/cost-journal/完整metering+finalization、周期tenant/key预算和Sandbox capture/cancel全图；同时验证错FK、重复空tenant、NULL混同、非法对象ID/UTF8/NUL、空reference与voucher UUID scope仍被拒绝。真实PG的采购/PB/支付历史旧TEXT全组合尚未独立构造，这些扩展旧TEXT关系的证明以纯图范围记录，不把其升级成真实在线admission。

纯与封闭生命周期测试覆盖nonce/state/once128/latch、错误mode/输入、所有错误零结果、callback完成前不释放busy、EOF/Err/Close全观察、错误stage无SQL、12组实际预算、固定14表列/类型与完整echo、DB stamp/原生BC及五位年、NUMERIC、不变深复制、每个v2嵌套字段和35个存储标量的错绑定。生成投影6555B使用独立手算golden；全68预算与basis预算分别检查，不声称RSS上界。

## 探索性失败与校准

早期仅设置 `MONI_TEST_DATABASE_URL` 的维护命令会跳过 `C0_MAINTENANCE_DATABASE_URL` 专项；初次相关8包报告虽exit 0，实际81项SKIP，不能算真实维护验收。根曾误报为真实事务通过，核对开关后已明确更正。首次同时启用两开关的真实执行为169.897秒、exit 1、零SKIP，确实暴露了旧空tenant来源core与planner合同不一致、业务fixture错误沿用SimpleProtocol、wire仅等5秒过短等问题，保留原失败日志摘要，不将其覆盖为成功。

先在[冻结合同第6节](../../specs/022-self-service-payments/c0-protected-init-phase-contract.md#6-验收与交付)统一legacy tenant TEXT语义，再修复七core的tenant身份/外键/索引边界；保持新业务ID、UUID及在线admission不变。业务准备改为单独短生命周期的正常pgx协议pool，原维护/DDL SimpleProtocol保持不变；这仅修复audit JSON fixture写入方式。wire等待增至35秒只用于等待服务器执行到屏障，产品phase累计30秒不变。后续探索还发现voucher fixture恰设一小时期限，进入事务时已短于原一小时下界；改为24小时，保留真实服务期限判断，未放宽产品规则。proof错绑定按原Schema封闭错误断言，其余实际图/基线不符按Transaction；均必须失败且不得COMMIT。

交叉只读评审另发现epoch规范校验前复制任意大字符串的输入预算问题，现改为认领once/latch后先校验固定36字节，再保存输入；无效输入仍永久消费phase，回归断言不保留超长原值。

## 最终检查

最终8包 `go test -race -p 1 -json ... -count=1 -timeout 900s` 明确同时启用C0/MONI专库，通过680.197秒：431顶层、3317父子PASS终态，零SKIP/零失败，执行前后完整相关源码SHA不变。含实际维护Gate/Permit/phase、basis与seed来源SQL，普通服务依赖隔离通过。统一架构/维护/目录/README共24张Mermaid 12.2.0实际parse/render通过；根已视检完整模块图和phase细图，源文档及PNG身份进入机器记录。

`make check` 通过7.825秒；明确TEST专库 `make verify-mvp` 通过978.116秒：前端286/286、46个Go包race、vet及trimpath构建通过，执行前后相关源码SHA不变。默认整仓不打开C0/MONI独立opt-in，维护真实覆盖由上述零SKIP专项承担。记录完成后由根再次reviewed refresh与只读check，同步最终验证材料而不改变已验源码；没有为结果文字变化重复运行资金测试。

## 尚未交付的边界

没有生产unknown observer；测试的独立持久化查询只是故障oracle。非空epoch独立观察/runtime认证、正式C0迁移、生产维护CLI、在线全资金路径、原路退款/拒付仍需后续交付。旧Installation/Attest空16表门禁保留，普通服务及其race测试依赖不反向引入离线维护。不能把经营后的当前109basis强等历史cutover摘要。候选事务通过不表示正式支付、真实供应商、容器/1Panel目标部署、HA或性能竞品对照验收完成。

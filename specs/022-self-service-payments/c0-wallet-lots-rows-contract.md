# 022-C0-V0-WL：原始行全钱包 lot 重放（根冻结）

2026-11-02。按[完整核验候选](c0-verifier-contract.md)、[全序](c0-sequence-contract.md)、[lot](c0-lot-contract.md)与[16表DDL](c0-production-schema.sql)冻结实际FIFO、结算、退款与事件/cache的独立纯核验。它继续[V0-WS](c0-wallet-sequence-rows-contract.md)，本片直接实现实际重放，不再只比较声明。

## 原行、输出与权限

`CheckWalletLotsRows(ctx, WalletLotsRows) (WalletLotsRowsCheck,error)`接受九组业务原行（tenants/accounts/journals/lines/receipts/requests/reservations/settlements/refunds）与全部16张C0事实表原列。新增opaque ID规范非nil小写UUID；既有TEXT保留原值、NULL与空串不同。所有数值保留原十进制；金额运算独立big.Int，不调用billing planner/SQL业务谓词。operation/event/legacy source嵌入WS投影再追加原列；时间用原生NULL/finite/infinity envelope，允许PG finite年范围-4712..294276、微秒精度，不缩窄为四位年份；需要时间的行必须finite。proof_canonical保留bytea。

输出只含`Scope=supplied_wallet_lots_rows`、Status（consistent/mismatch/indeterminate）、RowsConsistent、25组Scanned、WS结构计数、本域固定Mismatches与ReplayPrefixUnproven。所有计数脱敏，不输出身份、金额、时间或正文。只证明所提供行的lot域关系，无DB EOF、真实OID/catalog、basis/epoch初始化真实性或任何proof/资金权限。不接受外部报告、已验证bool或分配结果作真值。

原收据的人工双审、券资格、capture完整证据以及Key/PB/采购/audit/outbox由已有独立来源域在未来同owned session中验证；本纯lot域从receipt/reservation/settlement/approved refund的原金额推导，不宣称单凭九业务投影证明全部来源授权。它独立核钱包journal的tenant/kind/CNY/原operation、准确分录及反向钱包journal归属；request owner、refund approver的可用原列逐项比。funding更深的actor/命令/签名来源不从header自我认证。epoch/attestation只核给定内容的内部关系、版本、计数及canonical SHA，不认证实际角色、OID或严格运行codec。

## 精确重放

1. 先共享全输入预算，再内部实际运行WS结构检查；不得仅凭公共RowsConsistent布尔决定所有tenant失效。私有索引按原tenant核seq0/连续前驱/head；声明delta/event_count错误不阻止仍可独立推导的金额重放。
2. 单例epoch/attestation精确绑定、SHA/版本/ordinary-sandbox与角色OID互斥；旧tenant birth NULL与每tenant一个available opening（含0）双向覆盖。epoch counts是旧集合数，总A/H是旧opening合计，不是当前业务总数。旧非NULL birth无legacy事实。
3. 各真实钱包源恰属于legacy sources或唯一新operation；funding钱包source_id=receipt.id，legacy_receipts.source_id=原credit.id / voucher.id / capture.id。legacy receipt/窄sandbox、terminal、approved refund、source与opening集合逐键一致。旧held集合=legacy reserve减legacy terminal对应reservation，不能使用当前unfinished。旧available以全部legacy journal AVAILABLE净和核，held也核原HELD净和；不能拿当前cache倒推开账。
4. opening正A或H各一nonrefundable lot，FIFO=epoch时间；零额无lot/event/allocation。旧未结正R恰一个index1原边、旧terminal没有虚构分配。opening.account_id=NULL且amount0不要求当前账户也不存在，账户可以后来创建。旧approved F0逐行求和等于legacy settlement基线。
5. 按tenant seq重放。新receipt只取唯一origin lot行的opaque ID作locator，P/资格/FIFO/A从原receipt推导。可用正A按(fifo_at,lot_id C序)选择最短正额前缀；先形成整笔expected向量再apply，R不足不部分扣。R0仍有operation/journal但无边/event。
6. finish从独立原reserve边按index升序charge，0<=C<=R；每原正边都必须有一条terminal，即使charge0。桶变化A+=R-C/H-=R/S+=C，不重选lot。旧未结后批finish仍沿opening的原边。
7. 新来源消费退款只取原expected charged边，逆index扣此前合法seq累计已退，每笔正额边重编号1..n；S-=F/A+=F，原FIFO/资格不变。pending没有任何钱包效果。旧terminal后新批退款先验证F0+此前新F+F<=C，再新发nonrefundable lot，FIFO=当前wallet op.CreatedAt；旧pending的CreatedAt不能决定新lot时间。cutover旧approved不重发。退款不恢复Key/PB spent。
8. expected每次桶/delta/revision从独立状态计算，比较全部event身份、kind、原journal/operation、新operation/seq/top-xid、前驱、before/after/revision/四delta与完整集合，operation count与delta也与expected比较。真实边行只提供唯一opaque ID作后续FK locator，不能初始化q/c/refunded。
9. 最后比较每lot cache/尾/守恒、tenant AVAILABLE/HELD与unfinished held，以及全部approved退款SUM=current settlement.refunded<=C；全表反向扫尾发现缺多、跨tenant/epoch和未知钱包journal（零line也算）。非钱包采购journal不误报。所有实际行计Scanned；重复组不选首行。

## 损坏、预算、验收

源金额/序列/必要origin身份歧义导致无法推导时，只停止该tenant数值后缀、记ReplayPrefixUnproven，仍枚举全部实际事实并继续其他tenant。坏event/cache金额但expected仍可算时继续expected状态；缺event locator仅让链身份未知，不能从后续before/previous恢复。孤儿必须可确定无合法父/来源，受坏前缀影响的已声明后缀记deferred，不能猜上千orphan。确定不符优先mismatch，真实后缀缺证另保留。

全25组固定合计100000行/16MiB，字符串/普通bytea单字段65536B，proof_canonical独立上限262144B仍计总预算，原生时间每字段32B、bool1B。先预算再索引/大整数，内部WS投影不重复扣原输入预算；界定逻辑输入而非RSS。每至多256工作步及大整数解析/排序前后检查ctx。Input/Resource/Canceled/Timeout四固定错误一律整个零结果，数据坏行mismatch正常返回。最初直接FIFO每reserve排序，复杂度按完整候选O(V+ΣLlogL+A)，不得宣称网关p95。

独立手算：L1/L2/L3本金40/50/60、R120、C75、F20/F30→最终A15/50/60、H0、S25/0/0，revision4/5/3，含charge0的第三terminal/event；legacy fund200/C100/F0=20/R30开A90/H30，后C12退5沿allocated，再旧terminal退30/50新发两lot→A193/H0/S7，P200。另测零额、旧pending、分次等价、同刻C序、同xid多op、clock倒退、跨tenant、错序/漏尾/分叉、共改声明/FIFO错边/漏charge0尾、篡改origin/cache/events、坏前缀不猜后缀、资源与中途取消全零、输入不变与置换。预期不调用写侧算法。

本片无SQL reader、ScanKind/once扩展、维护pre/post/basis、epoch writer、RuntimeProof、迁移/ACL/HTTP/main或生产资金接线。实现后同步README、HLD/LLD、统一架构与验证，再按原依赖实现同session源+lot读取及protected pre+basis/init/post/epoch，之后全资金写路径。

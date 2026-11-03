# B-P4：来源与真实资金/PB效果的精确关系

2026-11-04，基线 `b3fd5cb6c29367ea65bda92340f51df13ea000c3`。承接[成本/request合同§9.1](b-procurement-cost-request-contract.md)、[账户合同§9](b-procurement-account-contract.md)和[来源实施合同](b-procurement-replay-implementation-contract.md)。本片冻结纯来源域的效果关系；不改变715列物料、原迁移、writer或在线授权。

## 独立责任

`source_closure_effect_relations.go` 只读取本次完整raw，通过唯一物理PK/业务身份索引核 source、companion、immutable原件与真实效果集合。expected从原合同及本source声明输出独立计算，不调用writer、其他重放域或其Stats/Candidates。匹配本source声明不等于证明其业务decision正确，后者继续由对应业务域与最终逐对象依赖证明。

所有循环沿原work/deadline；多值索引不任选。没有source的真正旧原件保留原legacy边界，不据此补Q。source存在但原件指针全NULL仍不能豁免。任何执行错误返回零结果；使用大整数，不能经float64或int64中间溢出。

## 客户结算与账本集合

new_settlement唯一输出settlement与journal：精确tenant/request、finalization、charged/raw/waived/status/reason和journal ID；journal精确operation=settlement、kind=result_status、reference=request、actor=system、currency=CNY及真实created_txid。来源六列继续按真实slot核验。当前refunded_amount可由后续退款增加，不倒填为历史0；created_at不由另一个clock样本猜测。

ledger companion与原journal_lines按实际ID、journal/account/delta双向一一对应，account的kind/tenant与companion精确相等；不比较今天account.balance与历史before/after。new_settlement完整kind集合恰为HELD、AVAILABLE及charge>0时REVENUE；delta分别为-R、R-charge、charge，零delta也保留真实行，平台REVENUE的tenant为NULL。完整集合按实际journal枚举，未列出或复用同一line均报错；existing/pending不产生新效果行组，原件其他业务journal不被纳入。

## 客户PB效果集合

每个实际companion的tenant/key scope、source/root、control/allocation/event与本次settlement逐值匹配；control的tenant/Key身份取本次request，allocation必须customer_request、本request、upper=R、baseline0，origin仅new或legacy_import。event真实source为settlement、本settlementID、同created_xid；before/after为(R,0)→(0,charge)，source_spent=charge、state=result_status、delta=(-R,charge)、allocation_before_revision=1。原admission marker存在时必须与本control相同；其allocation新生源/窗口/时间与request出生相同。legacy_import使用request ID、无窗口/入场时间，不允许customer catchup。

companion不是唯一观察：反向枚举所有source_kind=settlement且source_id=本settlement的原event，每一条恰消费一次。原admission marker或本次captured control下真实allocation存在时不得漏该scope效果。captured control可存在而source在其跟踪前已关闭、marker=NULL且无allocation，保留合法无效果分支；control=NULL且没有event不发行历史absence资格。不用今天配置/窗口倒填本次历史观察。

## 供应商PB效果集合

每个transition先精确绑定原PE的event_key/account/attempt/type/evidence_ref/delta及PB/来源xid。新settlement按request、journal按(tenant,operation)、原PE按event_key的业务集合均须唯一；其旁额外全NULL追踪原件不能冒充legacy。按真实source_kind=procurement_event、source_id=该PE枚举PB event：同attempt至多一个procurement scope效果；p出生marker非NULL必须恰有一个且control相同。marker=NULL时允许配置前无event，不能因今天已有allocation反向要求全部旧transition都有PB效果，该absence仍缺观察证据。

有PB event时核control所属account、allocation所属attempt/upper及origin。new要求baseline0、原reserved PE源、admission/window精确；legacy_import源为attempt且无窗口/admission；legacy_catchup源为真实同attempt/account PE且无窗口/admission，若本transition即catchup源，beforeHeld=0且baseline=beforeSpent。每个event的before/after held取真实transition，spent取max(sourceSpent-baseline,0)，after_source_spent取afterSpent，delta独立计算；state按prepared/sending/unknown→held、disputed/settled/released映射，created_xid精确相等。零delta不能省略有证据要求的实际effect。原PB配置、window/calendar和全event前缀仍由其完整域核验。

## 计数、集成与验收

跨行不匹配记OriginalPointerRows，按实际(物理表,自然PK)去重；已有双方但内容错误分别标source/companion与原件。缺/多效果的完整集合义务记CompanionRows，按outcome/transition或孤立companion自然键；孤立真实line/event记其真实物理对象。缺原件时不能制造虚假original行键。跨表同ID不互抵。

验收包括：正确正价/零价/释放/有符号REVENUE、后续余额与退款合法变化；漏/多/复用/跨租户line，错误journal/settlement字段；tenant/key双scope、new/import、marker和实际allocation强制效果、旧closed无效果；供应商new/import/catchup、超100位边界由原资源/金额层拒绝、零delta、marker=NULL的旧无效果与今天新配置并存；重复物理PK/业务身份、同ID跨表、排列顺序不变、预算内取消零结果。专项/独立复核与最终根集成后记录结果，来源Complete=false、Qualified为空，历史absence、业务cause和逐对象闭包继续保留。

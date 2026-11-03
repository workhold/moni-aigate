# B-P4：来源资格与历史观察的收口计划

2026-11-04，审查基线 `028b5a50db212546833b4724a483e8ab2ead9b65`。承接[读取合同](b-procurement-replay-read-contract.md)与[实施合同](b-procurement-replay-implementation-contract.md)。本页区分已经足够的材料、可从业务语言导出的顺序和真正缺少的观察依据，防止重复扩表或把局部通过当作完整证明。

本片不修改32表715列、40原件482列、旧V3及任何已应用迁移；当前仍没有公共 `Check`。后续若确需新增物理证据，必须先形成独立版本的字段、writer/guard/ACL、reader/basis/catalog及兼容合同，不就地改签V1历史材料。

## 1. 已落实的关系

本片落实原件当前指针与永久dispatch身份、p的出生/最后journal、三处原件Usage JSON，并纠正finish对墙钟单调性的错误假设；实现与实际测试以[续片验证](../../docs/verification/procurement-current-source-clocks.md)为准。

RequestHead认证latest/sticky序；last decision可以是conflict，此时latest仍是先前normal finalization。它不认证完整request.status/billing_status生命周期。attempt最后delivery来源与永久dispatch许可是两个不同指针，后续finalization/recovery不覆盖许可。p.last取所属账户全序中该attempt的最后p动作，不能取全账户最后配置或其他attempt动作。

## 2. 不扩字段即可继续实施的顺序

### 2.1 批准实际派生incoming

当Reconcile确实派生incoming和RequestDecision时，必须精确消费该同root decision的before_latest；`PriorLatestID`与它逐值相等，再沿完整request序证明sticky与新latest。现有decision提供了可信顺序，应先把此依赖接齐，不能把所有Reconcile都笼统写成缺cut。

### 2.2 最近人工来源

按真实Reconcile状态机，每case的新人工来源最多各一个propose、approve、release。新propose后proposal不可变，重复同输入快返；新approve后状态approved，后续approve快返；release终结reservation，其后操作快返或拒绝。因而合法人工来源只能是 `P → A → R` 的允许子路径，也可直接R。若A本身产生新settlement，不能再出现新R；A后仍能出现新R时，A必须留下pending客户预占。

下一片应基于这套固定语言核重复action、角色/提案消费、允许的before状态以及原case `pp_last_*` 的精确最后人工来源。自动pending/finish可修改reason、关闭非approved case，但不会更换last-manual指针；不要要求当前case所有业务字段永久等于最后人工source.after，也不要把完整自动状态历史混作人工pointer总序缺口。这个算法尚未实现，必须用交叉自动finish/pending的正反例复核后才能删除相应Unimplemented。

## 3. 仍须补足的历史观察依据

### 3.1 不派生decision的latest/发送观察

新Reconcile root无attempt/node，且没有request head的观察ordinal/tail。未派生decision的分支不能仅凭当前latest或其他attempt当前delivery推断历史观察。

一个必须保留的合法路径：reservation deadline=100，approval clock=100，因此不派生incoming；后续decision clock回退为90，仍会执行recovery的无latest分支。此时真实查询的sent决定released_no_delivery或pending。最终某attempt已sending，既可能是在此查询前派发，也可能是在此后派发；各attempt序与Reconcile root目前缺少能区别两者的跨序边。XID、UUID及墙钟都不能补这条边。

下一份最小证据合同须明确：在原request/预占同步点、与真实业务查询同一观察阶段记录request-head cut；对no-latest的发送查询提供完整attempt成员与delivery/node cuts，或受封闭writer与guard保护的精确typed观察并明确其历史证明责任。不能只加一个调用方可填写的sent布尔、count或当前hash。需要先证明所有birth/dispatch与此观察使用同一同步域，再冻结字段与失效/legacy边界；本页不预选一个未验证的DDL方案。

### 3.2 PB absence

当前没有allocation/event，不能证明该历史观察时尚未配置control；birth marker只证明出生时点，今天control存在也不能定位过去无效果动作。已有原PB与PBprefix继续证明配置、窗口、分配和效果；新增来源还须与配置/观察cut桥接。

后续先核真实namespace/control锁与首次配置legacy导入能否产生唯一偏序，再决定采用已有事件关系还是新的受控观察cut。需同时覆盖tenant/Key和supplier scope、首配前后、禁用期间记账、旧held导入与零delta，禁止以缺event自动授予absence资格。

### 3.3 报价目录不是本次额外缺口

所选immutable quote可精确核对；历史可见全集/noquote absence不能由今天cost_versions加时间过滤证明。读取合同已经明确将目录发行全序排除在十一命题范围之外，本片不因此另扩目录前缀，也不把quote_selected_at包装成历史目录认证。

## 4. Qualified依赖与实施顺序

仍需完成原件业务标量/其余JSON、语义NULL联合、ledger/PB精确槽、attempt/member跨序连续性，以及下面的逐对象依赖认证。业务叶子已实现的计价、p变动和member核验可以提供私有有效对象集合，但必须先冻结集合的身份与责任；不能用某域Stats全0推定全部来源有效。

共同资格Q至少包含对象所属root/全部step、唯一canonical child、精确companion/原件、origin、相关完整链与当前投影。复合身份按(物理表,自然主键)，跨表同ID不能合并。建议先以本地合法对象建立候选，沿缺失/坏依赖反向剔除至稳定，或等价处理强连通分量；双向约束不能采用从空集只加已全qualified父对象的算法，否则合法环永远无法进入。

| 命题对象 | Q以外必需的业务依赖 |
| --- | --- |
| finalization前驱、request顺序、conflict输入 | RequestIncoming的准确输入、meter归属/完整cut、request序 |
| identity原因及输入绑定 | RequestIncoming与AccountPrefix的member出生/复用、原incoming/meter和完整member cut |
| PE顺序 | AttemptCostHistory与AccountPrefix，全attempt node、账户journal桥接、原PE与真实supplier PB效果 |
| normal成本资格 | 冻结quote/request界、真实delivery/cost/p/member/account cut及其RequestIncoming输入 |
| 当前成本 | 全mutation/reset/correction/outbox及current-last；引用证据/审批时继续依赖对应请求/账户域 |
| proposal屏障 | AccountPrefix与AttemptCostHistory，出生/批准cut、角色快照及准确correction/transition/audit/outbox |
| account pause及admission | 全账户配置与p动作、真实blocker/gate、永久permit与PB效果；其cause递归依赖对应域 |

凡incoming带ReconcileSourceID，额外依赖真实Reconcile语义及适用finish，不因source字符串相同就授资格。Q使用物理行身份，Required使用命题对象身份，二者不能直接按同字符串求交。每个命题i先定义 `EligibleQ_i={k ∈ Required_i | deps_i(k)全部位于Q}`，再以 `Proven_i=Required_i ∩ Candidate_i ∩ EligibleQ_i` 计数。Candidate_i必须按上表逐对象映射其全部必需业务依赖，例如attempt通过origin.account_id定位账户候选，不直接将attempt键集合与account键集合相交。保留同次旧Base逐值与十一计数交叉核验。来源、四域、Base和aggregate未齐前继续不开放公共Check。

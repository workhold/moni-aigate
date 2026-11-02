# C0 最小旧数据投影与纯初始化规划合同

状态：2026-11-03 冻结，本片仅实现描述性投影和纯规划。依赖原68表完整来源合同、V0-WL、basis v1与strict proof v2；不发行维护权限，不实现writer/phase/epoch提交。

## 1. 边界与接口

`fundinglotverify.LegacySeedRows` 只含九组普通原件：Tenants（仅ID）、Accounts、Journals、Lines、Receipts、Requests、Reservations、Settlements、Refunds。后八组复用现有WL/ledger DTO；tenant不含birth。旧68没有读取birth，不得用缺省NULL冒充实际读过NULL。所有旧TEXT保持原UTF8字节（含合法空串），拒NUL，不trim、不要求旧身份为UUID。

`fundinglotscan.ProjectLegacySeed(ctx, Facts) (LegacySeedRows,error)` 仅接受原Read的68表结果：scope=complete_business_source_graphs_v1、原registry/列/键完整、68个EOF、有效原mode、Status=consistent、CoverageComplete、零input/journal/ordinary mismatch；前六算法executed，第七sandbox executed或ordinary not_applicable。核所有子结果与最终状态一致，重算raw行/字节与报告相同。不接受零Facts、84表Facts、外部report或自报budget。资格不满足返回ErrInput；私有结构损坏返回ErrExecution。沿用原68的100000行/16MiB/字段65536B、time32B、bool1B，资源越界ErrResource。

出口不执行Query、不改原session/report/rejected，使用本次ctx（不继承旧Read已结束的ctx）。slice、指针、string均深复制，原生时间Value/Valid/Infinity原样保留。逐行检查ctx，结束再查；nil ctx→ErrInput，取消/超时→原封闭错误，所有失败零DTO。已经识别的Resource不被后续取消覆盖。返回数据无schema、query、mode、coverage、basis、Permit或snapshot绑定；fake Query仍可制造描述性Facts，所以投影绝无授写能力。

`fundinglotinit.Build(ctx, Input{EpochID, EpochAt, Mode, Rows}) (Plan,error)` 是纯叶子，生产依赖仅stdlib与fundinglotverify。Mode仅ordinary/sandbox；ordinary拒sandbox receipt。EpochID为小写规范非零UUID；EpochAt为显式有限、有效、微秒精度PG时间（与WL的有限范围一致）。不读系统时钟/SQL/网络，不接受Verified/Balanced/ScanProof。四错误ErrInput/ErrResource/ErrCanceled/ErrTimeout，失败Plan全零。先入口ctx，再预算，再语义；已经识别的资源故障保留。每行、索引/算术循环、排序比较及结束观察ctx。

## 2. 输入独立校验

预算后独立检查九组字段和主键/自然键、tenant关系、账户kind/唯一性、全ledger的平衡与账户缓存；每source的journal header和精确分录、同tenant/request/reservation/settlement/refund关系、金额/状态/时间均须符合WL原业务来源合同。每request恰一reservation；终态恰一settlement，未结无settlement；approved有原审批和journal，pending两者NULL。原金额规范十进制，桶金额非负BIGINT、receipt/refund正BIGINT，journal delta/账户余额为各自原有signed BIGINT语义。每settlement的全部approved总额恰等refunded_amount且不大于charged_amount；released收费0。

反向核全部wallet-kind journal及触及AVAILABLE/HELD账户的journal必须恰好被业务源解释，包括零额reserve/finish仍含两条零值分录的journal；不得藏孤儿或重复journal。ledger的非wallet平台清算原件仍校验，不强行按钱包业务解析。九组不足以证明人工双审、券和capture来源资格；未来phase必须自己运行完整68表七算法，再使用自己的seed。纯Build成功不能替代该资格。

## 3. 集合与金额

F=全receipts，V=全reservations（含零/terminal），S=全settlements，Q=仅approved refunds，U=V减S（按唯一request精确关联）。每旧tenant：

```
A = sum(F.amount) + sum(Q.amount) - sum(V.amount) + sum(S.reserved - S.charged)
H = sum(V.amount) - sum(S.reserved) = sum(U.amount)
```

全部用big.Int；A/H分别非负且<=9223372036854775807。A+H及跨tenant合计可超BIGINT，按规范NUMERIC积分文本输出。A/H同时等于全部旧journal AVAILABLE/HELD净和及原账户balance，不从cache自初始化。

全F进入LegacyReceipts，sandbox精确子集另入LegacySandboxReceipts；LegacyReceipts.SourceID保留credit/voucher/capture原ID。全S进入LegacySettlements保留C/F0及原request/reservation/journal。全Q进入LegacyApprovedRefunds。F+V+S+Q进入LegacySources；其中funding source_id=receipt.ID，绝非receipt.SourceID。pending不进入membership。

每tenant恰1 available opening（含0）；每U恰1 reservation opening（含0）。available存在唯一原AVAILABLE账户就绑定，即使A0；仅A0允许没有账户且AccountID=NULL。held opening始终绑定原HELD账户，含R0。terminal不建held opening/allocation。

每正额opening恰1 nonrefundable lot，origin=legacy_available或legacy_reservation，issued=P=opening.amount，FIFOAt=EpochAt；历史receipt不逐笔另发lot，approved已包含A不另发退款lot。正held各1 allocation_index=1。每tenant恰1seq0 epoch_opening与head：source_id=tenant、previous/journal均NULL，delta=(A,H,0,A+H)，event_count=本tenant正额opening数。每正opening恰1legacy_opening event：before三桶0/revision0，after revision1，previous NULL；available after=(A,0,0)，held after=(0,R,0)，issued_delta=P。cache等于after/revision/tail。SettlementLots/RefundLots恒空。

## 4. 输出、ID和顺序

Plan包含上述12组数据写集、显式恒空SettlementLots/RefundLots，以及Metadata描述性draft（EpochID/EpochAt、T/F/S/Q/opening/legacySource计数、available/held总额、DataRows/GeneratedRows）。不输出伪epoch、attestation、strict proof、角色/OID、basis或actor，不伪造CreatedXID/CreatedAt。Operations/Lots/Events/ReservationLots使用专门INSERT DTO省略数据库stamp；其他无生成stamp的DTO可复用verify。返回切片/指针不与输入共享。调用者可修改普通Plan，故不能作为授权载体。

UUIDv5 namespace=epoch的16原字节。name=`moni-aigate/funding-lots-init/id/v1`+NUL，接逐字段U64BE字节长度及UTF8原件；包括以下tag及严格顺序的字段：

| tag | 后续字段 |
|---|---|
| available-opening | tenantID |
| held-opening | tenantID,reservationID |
| epoch-operation | tenantID |
| legacy-lot | tenantID,openingID |
| legacy-event | tenantID,lotID |
| held-allocation | tenantID,reservationID,lotID |

不拼分隔符、不规范化Unicode、不加入金额/时间/PID/xid。生成ID跨全部生成实体碰撞时整体拒绝。输入置换不改变任何输出。每组按原表PK字段tuple的UTF8字节序排序：ID类按ID，receipt/settlement/refund membership按源ID，LegacySources按(kind,source_id)，Heads按tenantID，Balances按lotID。没有map迭代序输出。

## 5. 资源与未来事务接线

纯Build先独立计九组100000行/16MiB/字段65536B（原生时间固定32B）；生成前估算行数，生成后核投影字节。T旧tenant、B sandbox子集、a正A、h正U、L=a+h：

`G = 3T + U + 2F + B + V + 2S + 2Q + 3L + h + 2`。

DataRows=G-2；末2为未来epoch与attestation预留。要求九组输入行+G<=100000，九组输入字节+生成C0投影字节+1MiB固定metadata预留<=16MiB。投影字节沿reader标量计数，省略的DB stamp按每XID20B/每时间32B保守计；metadata预留包括proof262144B但不发行proof。预算只界定逻辑数据，不称RSS上界。真实68其他59表不在纯输入中，不能据此承诺实际84post一定够；未来phase另用自己读到的全68行/字节重算同一上界，且真实84post仍为提交必要条件。basis独立10万行/64MiB不变。

未来写拓扑：legacy membership/openings→operations→heads→lots→events→balances→allocations→strict v2 attestation→epoch最后→flush deferred→实际84post→ready。仅16C0表allowlist与epoch.actor；不额外写audit_events（会改变109 basis）。c0-lot-contract旧§8.1的初始化audit意图由未来epoch.actor及提交证据表达；如增加业务audit必须另冻其basis delta，不能临场加写。

本片不消费第八ScanKind，不修改通用Commit/空安装门禁，不增加迁移/ACL/HTTP/CLI/在线资金写入。未来phase必须自己读取并持有seed，私有writer只收同phase state，不能接受外部Plan/Facts/Report/callback/Tx；pre另完整读tenant birth=NULL及16表空。busy覆盖pre+basis→write→strict v2/84post→ready，不能先Finish再授写。普通startup不能把经营后109表当前basis强等于历史cutover basis。

## 6. 验收

投影需覆盖原68 ordinary/sandbox真实PG、零/不完整/84拒绝、无Query、原ctx结束、新ctx取消、深复制、预算及所有错误零结果。Planner用独立手算三tenant多来源golden（含M级合法NUMERIC、pending、R0、terminal、空钱包）、固定UUID帧向量、输入乱序与不变性，以及缺失/重复/跨tenant/错误状态、金额、journal、refund总额、资源/取消负例。纯结果可在测试中显式构造虚拟DB stamps再交WL checker作补充交叉核验，但不把这种测试适配器或虚拟epoch放进生产包或视作真实授权。集成后运行专项race、必要真实专库、make check与verify-mvp，同步README/HLD/LLD/统一架构并推送。

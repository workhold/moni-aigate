# 022-C3：退款、拒付与恢复交叉（验收设计候选）

2026-11-01，仅设计候选，未冻结外部协议/SQL或实现资金入口。它补[C1原路退款](c1-refund-contract.md)、[C2拒付/债务](c2-dispute-contract.md)之间的裁决与恢复出口，不替代[C0完整来源](c0-lot-contract.md)、[维护/proof](c0-maintenance-contract.md)与[独立核验](c0-verifier-contract.md)。本地mock通过不能替代正式PSP认证；C1/C2各自通过也不能代替本片。

## 1. 事实与授权的最小集合

每次判断限于一个不可变原order/capture/environment/payee/transaction/CNY。输入必须包括：原本金P、原lot全序及桶；全部本地退款提案/hold和稳定命令；逐笔权威退款事实/最终拒绝墓碑；唯一可应用case的debit/reversal事实及完整交易revision。不能只用Q/F余额、供应商total、单event或当前AVAILABLE推导关系。

新协议必须逐笔定义本地refund UUID、provider refund reference、command digest、capture引用及金额，并证明snapshot是同一revision的完整交易视图。有分页时，所有页必须相同revision、稳定cursor、完整终结标志；分页期间交易改变则保留原Q/风险，重新查询原对象。未给出完整性/最终性合同的PSP不进入自动资金裁决。

网络事实不是财务操作授权：合法原hold才允许Q收尾，合法C2源及风险屏障才允许debit/reversal。外部事件没有对应本地hold时只能保留review，不临时冻结客户别的资金以凑出“退款成功”。所有资金作用仍须完整source/journal/seq/event/cache/effect/audit/outbox同提交。

## 2. 同order交叉裁决

| 原状态与新事实 | 必须先成立 | 允许的资金作用与保留项 |
| --- | --- | --- |
| C1 proposed与dispute同时到达 | 同order根锁裁决、批准前最新风险复验 | 风险先提交则不批准；hold先提交则Q保留，不能撤销已授权命令 |
| C1 held/unknown且dispute.opened | 风险阶段成功提交tenant屏障 | 不动Q/H/S，不造debt；仍可接收原命令证据与query |
| C1 held与权威refund.confirmed | exact原命令/原capture/金额、无互斥终态 | Q→F一次；原case保持风险，之后重新核完整外部累计 |
| C1 held与final_no_effect | 永久同command/body拒绝墓碑，晚到mutation也不生效 | Q→原A一次；不能据此自动清除其他case/冲突/债务 |
| C1执行预算耗尽后到完整终态 | 仍为held，耗尽仅是调度review | 同原授权收尾；不重开原mutation，不因停止query永久拒绝终态 |
| C1仍可能生效且收到debit | 无法证明退款与扣回的最终关系 | 保留Q和原钱、先风险/review；即使Q+B≤P也不自动应用debit |
| 全部退款已有一致最终事实、无在途命令，debit B | 完整逐笔snapshot与本地全部操作一致且F_ext+B≤P | 按C2只回收原lot A，其余独立debt；不占其他来源/H/Q |
| F_ext+B>P、错绑或同revision异体 | 原始证据持久、可映射原交易 | 风险/review，已有资金不回写；不能截B、忽略外部损失或多扣客户 |
| C1已no_effect后到相反成功，或成功后到相反终局 | 原order风险阶段独立提交 | 不复用已释放Q、不再次扣客户；完整冲突交事故处理合同，无自动赢家 |
| reversal先于debit被本地观察 | 完整权威debit+同额reversal均在同原交易 | 依C2同tx两份连续钱包操作；缺任一事实仅pending/review，不先加钱 |
| debit后原H释放或消费退款 | 原消费source/原边完整 | 正常恢复原A；不在finish里自动偿债，显式双审偿付才改变claim |
| 反转与明确偿付竞争 | 同order根串行，重验原claim/lot与原operation | 偿付先则反转逐原偿付边返还；反转先则新偿付拒绝，无重复返还 |

外部事实矛盾后，哪怕之后一个新revision显示净额恢复，也不能单凭“看起来一致”清除review；需协议认证其取代/纠正关系和最终性，保留原证据链。首片没有这类认证便维持核查，不能新增万能管理员改钱/关风险接口。

## 3. 锁和阶段不得因交叉改变

所有制单/批准、证据selection、claim/query/expiry、debit/reversal/偿付都从原order锁开始，再子根及需要的当前财务身份；禁止outbox/processing/refund子行锁后回锁order。先无锁选定位键不授权，持父锁后重新核generation。风险阶段到tenant UPDATE屏障即提交；后续资金阶段失败不能回滚已落风险。

资金阶段继续原order→子根/身份→tenant→账户C序→lot C序→最终DB时刻/源复验。Reserve/finish/消费退款不反向取得原order，以原immutable来源和tenant准入读门禁同步；已sending可完成唯一结算/释放。新的共享执行容量若未来接线，不从客户/采购/退款财务终态自动释放，保持025独立执行退出合同。

该锁图仍须以最终SQL的隐式FK/触发器锁补全；只有文字顺序不足以开放C3。专项应固定屏障制造两个真实连接争锁，验证合法赢家与无中间资金状态；不以随机压测没遇到死锁当完整证明。

## 4. 提交未知和进程故障

DB成功COMMIT但回包丢失：销毁原连接、返回零结果；新连接只读**原operation**完整回执及资金图，不换ID、不补缺边。GET404保留unknown，直到可确认原backend/事务及恢复状态；余额接近预期不是原操作证明。资金图缺边属于完整性错误，不进入“再执行一次修复”。

外部成功丢HTTP回包：同一稳定原command/body，只通过原持久query/通知收集证据；no_effect必须最终拒绝，not_seen只是查见性。恢复worker不重发模型生成；C1退款mutation是否可按同ID重放仅由新版幂等/墓碑合同授权，不能将checkout的旧规则自动套过来。

真实SIGKILL/SIGTERM/Guard失权分别在claim前后、sending提交后、外部接受后、证据入箱后、资金COMMIT前后执行。每个窗口必须有物理等待或提交见证，不靠睡眠猜命中。记录原ID/generation、canonical digest、Q/F/C/T/D、全部账户/seq/边/effect及资源join；重启只延续原事实。耗尽执行预算与冲突资金review分别验证，生产期限不得为测试直接改短。

## 5. 旧备份与离库事实

恢复旧备份保持既有Recovery latch，不开放新充值/退款/偿付/模型准入，不删除旧proof/epoch/member以冒充新库。不能再次执行C0 init、把当前钱包重新开成legacy，或更换environment/merchant逃避原外部历史。

必须取得经认证的原PSP完整交易范围及离库checkpoint，覆盖备份之后可能发生的capture/refund/debit/reversal和仍在途的原command；checkpoint绑定原environment、交易revision/范围及本地已承诺操作身份。具体存储、完整性/权限、失败恢复与双审释放在生产恢复合同另冻。本地当前库的最大event ID不证明备份后的外部事实不存在。

恢复期间允许的网络仅原对象查询/证据收集，资金应用由恢复模式合同显式约束；当前Recovery只读证据而不资金，不能为补齐差额偷偷打开普通worker。须在受控离线恢复流程中按原operation唯一语义导入/核验外部事实，所有source/seq/lot和旧回执一致且独立全源核验完成，再沿既有双审恢复授权重启。该离线导入协议未冻结或信息不完整时保持Recovery，不造调平流水或把缺外部历史记成0。

## 6. 独立验收出口

使用固定新版mock协议、实际APP/worker/projector LOGIN、独立checker、非空旧历史及多tenant。组合矩阵至少包含：原支付消费后券补余额、两笔部分退款、Q未知+opened/debit、退款永久拒绝晚mutation、矛盾终态风险、H释放/消费退款/偿付/reversal各顺序、通知/query多event竞争、同order多审批、真实COMMIT丢包/外部丢回包、失权与重启、旧备份后外部新事实。

每种组合先手算P=A+H+S+Q+F+C+T及journal；未反转时核B=R+E+D。全额反转则按原B=返还(R+E)+被抵消的反转前D核分录，并要求该case当前净C/T/D=0；历史B/R/E不回写，也不把反转后D=0代入未反转公式。再用独立Go全钱包seq重放与原source/外部事实双向验证，不能从运行planner生成expected。缺权限/查询取消/预算不足返回零结果和固定错误；确定不符与coverage不足分别展示，任何一个都阻断发行。客户看账/登录继续，未知成本或外部损失不计0或完整利润。

完整C3验收前还缺：C0全路径、C1/C2新版外部协议与mock、生产SQL/角色和opaque helpers、风险/资金分阶段恢复、独立全源Verifier、离库checkpoint和导入合同。此文不标记这些已实现。最终性能必须基于届时完整商业路径重新测量，本片不证明首字节/首token或竞品性能相近。

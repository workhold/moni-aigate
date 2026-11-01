# 022 验收矩阵（A已限定本地验收，B/C待执行）

2026-11-01，**A0/A1已实现并通过限定本地验收；B0-P与M/Q独立沙箱已实现并通过限定专项/真实进程短程；旧版本20代长程完成、最新补丁/精确故障另验，B1核心SQL与B2资金接线合同已冻结，B2尚未追加021或接应用，B3与C后续**。A依[A执行合同](a-contract.md)、[领域合同](a-domain-contract.md)完成共同入账、双审券、HTTP和控制台，证据见[验证记录](../../docs/verification/funding-vouchers.md)。在线支付沙箱、来源lot与原路退款继续独立审查，不能将A验收视作022整体完成。

A0与A1的组合证据、真实PG/HTTP/前端/隔离浏览器及017预览升级见[阶段验证](../../docs/verification/funding-vouchers.md)。下表A由这些证据覆盖；B/C继续待执行，不以本地fixture替代正式支付/生产验收。

## A：共同入账和兑换券

| ID | 真实边界 | 预期 |
| --- | --- | --- |
| A01 | 默认配置、admin、member/API Key尝试发行/审批/兑换 | 默认off；权限拒绝，admin不能造额；只owner可兑换 |
| A02 | maker自审、被撤销角色maker/approver、过期再认证 | 原子拒绝，无激活/入账；MFA仍可选但已绑定照常验证 |
| A03 | 100张/正金额/金额乘积/钱包溢出/非法日期与byte长度 | 静态金额/乘积等格式上限400且不claim；实际钱包/清算账户溢出HTTP200+funding_amount_overflow rejected，不核销；全程不用浮点 |
| A04 | 创建首次响应、同ID重读、并发同ID、响应丢失 | 一批摘要，首次一次codes；GET/重读无codes，无第二批秘密 |
| A05 | DB/history、审计/outbox、HTTP错误、日志/trace、浏览器存储与URL | 不含code/secret/signature；只有安全ID/金额/用途；关闭与身份切换清内存 |
| A06 | 同券100个并发兑换、同租户及跨租户竞争 | 全局仅一个redemption/receipt/journal/credited；失败不泄漏获胜租户 |
| A07 | 原operation重读、异体/同body改path目标、跨tenant、GET404与未知COMMIT | 返回原安全回执或operation冲突；指纹绑定target；不自动换ID/重发；404不解除未知状态/新operation锁 |
| A08 | 锁等待跨到期、批次作废/兑换竞争、证据计数陈旧 | 锁后DB时刻；审批证据陈旧HTTP200+rejected voucher_evidence_stale，并追加不可变rejected decision；作废只影响未兑，不撤余额 |
| A09 | 核销后、journal/receipt/audit/outbox边界注入回滚 | 全有或全无，回滚券可兑，未知先查原命令 |
| A10 | 历史人工credit回填、旧outbox重放、新grant、报表重建 | 原journal/旧累计不变，无重复credited；全部来源和ledger一致，grant不显示现金收款 |
| A11 | app/projector/migration角色，篡改历史/状态/归属 | 历史、摘要、source不可改；projector不能动任何资金或验证事实 |
| A12 | 隔离浏览器finance批次/独立审批/owner兑换/查看账单 | 精确金额/一次展示/安全回执，权限与API一致；不复制真实秘密进报告 |
| A13 | 旧credit缺journal/重复journal/错tenant/错金额/错credited/pending污染；历史回填重跑 | 任一异常中止迁移、无调平/静默忽略；合法历史仅一份receipt，无新钱/新事件 |
| A14 | approved credit字段篡改、receipt绑定其他tenant或refund journal、同额source交换 | DB终态/归属/指定科目约束拒绝；VerifyFunding逐项失败，不能被SUM抵消 |
| A15 | AVAILABLE锁等待跨券到期，再释放；account溢出 | 最后账户锁后DB时刻决定，券未核销；语义拒绝持久，金额/业务audit/outbox无变化 |
| A16 | redeem与disable、owner/tenant撤权、batch revoke、Reserve/credit/refund交叉 | 按A真实锁图线性化；无父锁反序、死锁已知回滚可重试、COMMIT未知不重试 |
| A17 | DB policy off/过期/已兑/旧CAS/stale/溢出与audit/DB失败 | 前者HTTP200+rejected终态且四关联null，无资金/核销/状态变更/业务audit/outbox，stale仅保留rejected decision；后者整体回滚/未知查询；env硬off另在claim前409，UI不把200视为accepted |
| A18 | HMAC active轮换、旧券/旧accepted和rejected命令重读、缺key版本 | 旧版本识别同body，不异体误判；缺key503不核销/不伪装已兑，日志/DTO无版本秘密 |
| A19 | batch状态rev、兑数、多个revocation proposal/旧decision直接改写 | rev仅state变，兑数与唯一redemption一致；旧证据stale，proposal/decision不可重批/篡改 |
| A20 | env与policy双off、admin同值/旧rev/伪造revision、off后approve/void/revoke | env不能被admin突破；同值no-op/旧rev409；off仅挡新提案/兑换，恢复仍挡全部写 |
| A21 | 旧credited重放、券新增、原reports-v1、同步来源摘要、Rebuild失败切换 | 无第二份入账分类事件，累计一次；source摘要精确并有DB时刻，失败保持旧active |
| A22 | app/projector真实角色新表/新账户、case别名/重复body/超限/非canonical code/cursor | 最小列权/trigger生效；projector仍无source读权，严格语法不claim，安全响应无原输入 |
| A23 | 4096actor桶/第5在途/限流、poolwait/取消/rollback归还 | 有界独立准入、不污染旧桶，5秒包含池等；许可唯一归还，429不建立command |
| A24 | 40P01/40001闭包重试、失败attempt secrets与未知COMMIT | 只最终提交获胜attempt显示一次codes；回滚/未知不回显、不重发，安全command journal不含券 |
| A25 | 六种mutation首次/重读/accepted/rejected、缺字段/额外codes及GET operation | 严格匹配总合同§3.4–3.5；accepted reason=accepted，resource指向正确种类，仅redeem带金额/receipt/journal；所有rejected四关联全null，GET从不返回secret |
| A26 | BatchView四态/到期/激活maker后来离职、policy PUT同值/变更、RedemptionView | 四态actor/time null严格匹配；到期不改历史，active授权不因maker后离职撤回；policy GET/PUT同DTO，redemption路径ID及当前tenant正确 |
| A27 | active admin/finance平台capabilities、非active角色、finance访问policy、DB故障 | capabilities只给有效admin/finance，同钱包安全DTO且payments_mode=off；policy仍仅admin，DB故障503而非假off |
| A28 | 批次下revocation列表/单体详情、approve额外证据、stale/已有decision | 精确RevocationView/DecisionView；approve仅operation；stale生成一份不可改decision，已有decision的新operation仅state_conflict rejected；无secret |
| A29 | 创建关闭/卸载/身份切换/unknown后旧201迟到、GET404、刷新/切页 | 晚回codes丢弃且不缓存；unknown保持原operation安全journal，不以404/刷新自动解锁、重发或换ID |
| A30 | wallet/platform各列表空页/末页/换scope/撤权、receipt source筛选/unknown query | 固定50且data数组/next_cursor null形状不省略；只收约定cursor/source_kind，换scope/filter无效400，逐页重验当前读权 |
| A31 | 钱包来源摘要、赠额入账、人工核款及异步credited滞后 | 标注累计钱包入账/人工核款/运营赠额/as_of，不将赠额称为收款或可退现金；投影滞后不触发自动补账，不展示A未实现在线支付/原路退款 |
| A32 | ApplyFundingTx手工/券scope、Applied重读、缺父锁接入审查、唯一emit | manual无CommandID且voucher绑定本tx已claim命令；helper不Begin/Commit/取父锁，账户/最终时刻/资金/emit唯一归属；Applied不作HTTP成功证明，外层提交后才返回 |
| A33 | 多tenant共用平台clearing账户、tenant VerifyFunding、旧非UUID credit source、decision列 | 各tenant只比自身source/journal clearing lines，全库账户核验仍保留；合法历史ID原样保留而不强转；唯一funding_receipts表，decision使用status/reason/decided_at且无别名分叉 |

## B：隔离支付沙箱

| ID | 边界 | 预期 |
| --- | --- | --- |
| B01 | 普通main、普通DB仅设sandbox env、live、非loopback | 拒绝启用；无simulate/sign/forge入口；off不开放新订单 |
| B02 | mock端控制权限、无DB权限、外网/重定向、跨环境签名 | 只有QA CLI控制、无外部调用，错env/merchant/key拒绝 |
| B03 | SKU改价/下架与已有订单、客户端amount/payee/paid注入 | 旧订单快照不变；客户端造额拒绝；下架不丢合法晚通知 |
| B04 | 签名raw-body变化/时间偏移/过期key/未知JSON/超限body | 固定边界拒绝，不形成可信inbox/资金效果，无秘密泄漏 |
| B05 | inbox提交失败、ACK后进程终止、worker lease到期 | 未提交不2xx；已ACK事实恢复；唯一资金效果 |
| B06 | 同event等价重复/异体；不同event同transaction；query与callback并发 | 等价回原receipt、异体保冲突、每笔capture仅一次入账 |
| B07 | checkout失败/过期先到、合法时间capture晚到、失效后capture | 失败不覆盖收款；合法晚到一次到账；失效后或冲突事实review不盲目入账 |
| B08 | 未知order、金额/币种/收款人错、同order第二transaction | 安全证据可核查；不假tenant、不能靠admin“置成功”入账 |
| B09 | 外部提交成功后本地超时/崩溃、mock暂不可查 | 原command进入unknown，只查原ID；不新建外部支付或退款 |
| B10 | 功能off、tenant暂停、商品停售、正常重启 | 已承诺财务事实仍完成；新的准入被拒；无模型生成调用 |
| B11 | 真实main正常SIGTERM和kill窗口、受限角色、投影重建 | durable inbox/outbox可恢复，账本/source/投影一致；不声称真实支付商通过 |
| B12 | 隔离浏览器展示订单，QA CLI产生mock事实 | 前端仅以本地入账receipt显示到账；明确本地模拟，无客户端签名能力 |

## C：来源、退款与拒付

| ID | 边界 | 预期 |
| --- | --- | --- |
| C01 | AVAILABLE/HELD legacy迁移，有旧未结reservation和旧settlement退款 | 原ledger不变，所有持有有来源；历史退款非现金资格，无猜测归因 |
| C02 | 所有Reserve/安全第二attempt/settle/release/unknown/超时/profile超界 | 原客户只预占一次；lot与账户/settlement守恒，硬24h不延长，不重发生成 |
| C03 | 充值100→消费100→兑券100→申请原支付退100 | **拒绝原路退款**；钱包100不能转换来源 |
| C04 | 多lot FIFO、并发推理/现金退款、消费退款返原lot | 同一额度不双用，全部精确；消费退款不提高其他lot可提现额，不恢复Key spent |
| C05 | C0未落地、没来源分配或lot与ledger不一致 | 原路refund/debt effect入口不启用或停止写，不靠总AVAILABLE绕过 |
| C06 | 双审退款、原支付可退额/本地lot不足、累计退款并发 | maker/checker有效且不同，双上限，独立payment refund held，不动推理held |
| C07 | refund提交timeout、回调与query竞争、明确失败/未知失败 | 成功只一次；确定无effect退回同lot；未知持续原hold/原command不另起 |
| C08 | dispute仅通知、confirmed chargeback部分/全额、余额不足 | 不把通知当扣回；仅原lot可用回收，剩余独立债务，钱包/held不负 |
| C09 | tenant门禁写与Reserve/PrepareSending锁等待交错 | 屏障前sending不撤回；屏障后新Reserve/sending拒绝；既有结算/释放继续 |
| C10 | 券/legacy/新现金与债务、原lot后来释放/消费退款 | 不吞赠额偿现金；只有冻结的显式偿债来源与分配可抵债，门禁解释清楚 |
| C11 | case重复、reversed先到、拒付逆转含部分已偿付 | 无凭空credit，等权威核查；恢复确切来源/偿付，余额与剩余债务合计=A |
| C12 | refund unknown与chargeback交叉、外部累计超capture | 所有证据保留review；不盲目释放、不双扣、不伪造调平事实 |
| C13 | lot桶/allocation mutation、越tenant归属、排序死锁竞争 | DB拒绝非法历史/负桶；真实PG最终锁图无反序；已知回滚重试与未知提交分开 |
| C14 | 恢复旧备份，外部已有新收款/退款/发券；恢复模式收通知 | 只收集证据不自动资金应用；离库事实与双人核对后解锁，不盲重播 |
| C15 | 全量账本/source/lot/hold/debt/report verify与rebuild | 独立精确核验全部一致；projector重放不能造资金或改变退款资格 |

## 审查出口与非结论

- 设计阶段：检查本地链接、角色与状态闭合、资金分录平衡、来源套利与恢复边界；集成人冻结前不编码。
- 实施阶段：先专项有意义的真实PG race测试，再合并前后端/构建/受限角色/隔离浏览器与`make check`、`make verify-mvp`。最终由集成人更新统一架构正文、changes和manifest并只读检查。
- 真实支付接入、真实商户收款/对账/退款/拒付、税务、支付商生产密钥、外部故障和生产容量均未在本提案验收。没有实际运行证据的项保留待验收，不能复用模型供应商fixture或历史性能记录。

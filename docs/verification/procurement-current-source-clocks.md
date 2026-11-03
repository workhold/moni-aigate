# 采购当前原件、Usage 与独立墙钟校验

2026-11-04；基线 `028b5a50db212546833b4724a483e8ab2ead9b65`。本片依照[实施合同](../../specs/023-periodic-products/b-procurement-replay-implementation-contract.md)补来源关系与严格JSON，同时按已有非单调墙钟合同修正finish。前片结果见[成本/finish历史记录](procurement-cost-finish-replay.md)，当前关系见[架构细图](../architecture/procurement-replay.md)。

## 实际实施

[当前指针](../../internal/procurementprefix/source_closure_current_pointers.go)在四条显式链结构核验后执行，继续共享原work/deadline。request的last decision及latest分别对照完整decision序，允许最后decision为conflict、latest仍指旧normal；不以最后decision永久固定status/billing_status。

attempt按唯一node序核last mutation、last delivery与其真实当前值。空链只能保留origin初态，已存在但结构坏掉的链不能视作空历史。mutation/delivery原件与node双向核身份和scope；孤立、重复来源不因当前值恰好正确而豁免。永久dispatch与最新delivery分别核对：首次prepared→sending的唯一permit，其source/root/step/xid、PB checked_at以及managed账户/journal必须精确相等，后续delivery收尾不覆盖许可。unmanaged的账户/journal保持NULL。

p出生精确绑定同根reserve journal及原reserved event/transition，before-p为NULL；current p与该attempt在所属账户全序中的最后p journal逐值对应。配置、其他attempt可合法穿插；全账户末journal不等于p.last。source/original使用物理表与自然PK去重，多个错误字段不重复计同一原件。

[Usage](../../internal/procurementprefix/source_closure_usage_payloads.go)独立核两类meter原件的normalized_usage及case非NULL proposed_usage。只接受六字段对象，拒绝缺失/未知/重复decoded字段、错误类型、指数/小数/非规范或溢出整数、非法Unicode与多根JSON。五种原status全部保留；所有计数非负int64、cached≤input、reasoning≤output，仅confirmed_final要求input+output=total，独立大整数求和防溢出。nullable proposal仅SQL NULL跳过，JSON null不是Usage；非NULL proposal须confirmed_final。

[finish时钟修正](../../internal/procurementprefix/finish_replay.go)删除两个错误的墙钟先后比较。approval、settlement decision与financial采样各控制自己的原分支，不假定clock_timestamp单调。测试保留审批已到期但decision回退时无latest的两种原recovery分支；它们仍缺历史sent/absence cut，因此只核局部结果，不因此发候选。decision已选deadline释放后，即使financial回退也不能恢复收费；financial前进跨deadline则最终raw/charged/waived均归零。

## 独立复核与回归

独立reviewer的外部overlay复现三处本片初稿的反向漏核，均已修复并在仓库保留变异回归：有origin/original但没有node的额外mutation、移除origin并将p追踪列清NULL以掩盖新journal、另一账户journal复用同attempt。原overlay独立race复验全部PASS，1.321s；它只用于复核本私有关系叶子，不冒称已完整运行SourceClosure或新scope。

手工fixture覆盖乱序输入、非单调XID、conflict末项、零成本与NULL、prepared空链、managed/unmanaged permit、精确dispatch journal、p与账户配置交错、旧全NULL边界、新来源不能伪装legacy，以及预算内取消/错误零结果。旧birth快照测试继续允许later current值；其完整入口fixture在没有node时改用真实birth当前态，避免把省略全部后续来源当作有效完整历史。

根在所有并行文件冻结后执行：

```sh
env -u TEST_DATABASE_URL go test -race -json ./internal/procurementprefix -count=1
env -u TEST_DATABASE_URL go test -race ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1
```

完整新包 **98顶层、576子例全部PASS，0 SKIP/FAIL，2.769s**；旧采购证据checker回归PASS，1.618s。Usage独立5组race PASS1.380s；finish专项race PASS1.844s；根current专项race PASS1.549s。最终以根完整包为集成结果，不将各并行专项耗时相加，也不把这些耗时当作首字节/首token/吞吐性能。

根完成统一架构语义复核后刷新1752文件快照，只读架构检查与 `make check` 全部PASS：候选32表715列/68 generated布局、`go vet ./...`、Vue strict与Vite构建。归档本段后再次刷新并只读核验清单；代码、测试与依赖没有再修改。

## 收口依据与范围

[收口计划](../../specs/023-periodic-products/b-procurement-replay-closure-plan.md)已区分：人工P→A→R偏序、派生decision.before_latest绑定可由现材料继续实施；未派生decision的request/attempt观察，以及PB scope首次配置前的absence仍需明确跨序证据。后两者不能用当前投影、UUID、墙钟或XID补序。报价目录可见全集明确在本轮十一命题外，不额外扩大此范围。

SourceClosure继续Complete=false、Qualified为空；finish继续保留三项Unimplemented。其他原件标量与payload、剩余语义NULL、ledger/PB效果槽、跨链连续性、逐对象资格、同次Base和aggregate尚未完成。没有公共Check、正式reader/profile、schema升级、writer授权或线上资金接线；32表715列、旧checker/V3和历史十一项Unproven不改签。

本片未访问PostgreSQL、真实供应商、支付、Docker或1Panel环境；B-P3、完整writer/ACL/guard、宽TEXT切换、R2正式迁移/CLI/启动/在线资金、后续商品/原路退款/拒付、生产与性能门禁继续独立验收。

后续人工来源偏序、派生decision前驱桥和完整行内联合已另见[顺序续片](procurement-reconciliation-order.md)。本页98/576与三项Unimplemented描述保留本片当时范围，不改签后续结果。

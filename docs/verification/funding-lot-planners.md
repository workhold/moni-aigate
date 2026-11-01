# 022-C0 纯分配算法验证

2026-11-01。本记录只覆盖[合同 §12](../../specs/022-self-service-payments/c0-lot-contract.md#12-根冻结纯分配算法切片2026-11-01)冻结的 package-private [纯算法](../../internal/billing/lot_plan.go)及其[测试](../../internal/billing/lot_plan_test.go)，不代表 C0 资金事务、生产 DDL、维护初始化或退款已实现。

`go test -race ./internal/billing -run '^TestFundingLotPlan' -count=1` 通过（1.699s）；UTC年界复核后再次通过（1.736s）。无需数据库；无资金写入、网络请求、来源发行或权限能力。覆盖同刻 UUID/C 序 FIFO、最短正额 prefix、零额/不足额、MAX 单笔及候选总和超 MAX、原 allocation 连续性、部分/零收费、逆原 charged 边退款、分次与一次退款等价、1–129 碎片守恒、非法/重复/超退输入及输入不变。

运行时后续必须在原账户/lot 锁内独立证明完整候选、source/journal、事务和钱包操作序列；纯函数输入不是可信资金凭证。历史只读核验器不得复用这些运行时算法作为自身正确性证据。生产候选的全部钱包路径、维护 epoch、实际 LOGIN 权限和双向破坏验收继续按 [C0 合同](../../specs/022-self-service-payments/c0-lot-contract.md)与[序列候选](../../specs/022-self-service-payments/c0-sequence-contract.md)审查。

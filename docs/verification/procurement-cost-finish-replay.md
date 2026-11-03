# 采购成本与客户结算的私有历史校验

2026-11-03；基线 `5a49e3359b9fa40ce4d93a786c940dd44f60aeee`。按[私有实施合同](../../specs/023-periodic-products/b-procurement-replay-implementation-contract.md)先固定成本历史与客户 finish 两个子域、固定计数和失败边界，再并行实施、独立复核。前片范围见[历史记录](procurement-replay-leaves.md)，当前调用关系见[架构细图](../architecture/procurement-replay.md)。

没有公共 `Check`，不发行新 scope/profile，也不改变旧 checker 的十一项 Unproven。这里的 `Complete` 仅描述指定私有子域的实施范围；真实完整来源、跨域依赖交集和最终 aggregate 尚未完成。普通服务、SQL writer、迁移、启动、钱包资金和维护权限均没有接入这些新增纯核。

## 成本历史

[checkAttemptCostHistory](../../internal/procurementprefix/attempt_cost.go)从 prepared、unknown/NULL 开始，按 attempt node ordinal 消费 delivery、normal eligibility、reset/mutation、evidence/p、approval、unmanaged correction、recovery 和 cost outbox。它独立按冻结采购两维费率大整数求和后单次 ceil，更正采用绝对金额，保留同额及 basis-only 历史；current 必须等最后 mutation。自身 cost_outbox canonical 身份不能跳到 cause。

本私有子域 `Complete=true` 只覆盖固定前九计数与三个 attempt 候选；完整 scalar/generated/root/原件闭包由来源域认证，账户 gate、PE/member/proposal barrier/角色由账户域认证，客户 finish 与完整 PB 仍须联合。它没有为这些依赖发出完整证明。

quote 仅核所选 immutable cost_version、origin/原attempt/managed冻结值的一致性和 noquote NULL 联合。今天 cost_versions 的时间过滤不能证明过去 statement 可见的完整目录；该历史目录证明不在本轮十一命题范围，也不能拿 quote_selected_at 冒称已认证。

正常成本的 known 优先于 disputed/usage 分支，managed/unmanaged 分开；未知到未知 reset 和同根两个 reset 不合并。unmanaged 已 recorded、reset 后再次正常写成本的真实 outbox 唯一冲突不能变成成功 skip。局部 attempt 候选必须再与完整 SourceClosure、RequestIncoming、AccountPrefix 和 PB 依赖合并，不直接成为 Proven。

## Reconcile 与客户 finish

[checkReconciliationFinish](../../internal/procurementprefix/finish_replay.go)实际核三种 action 消费的字段、maker/checker 角色快照、before/after、原件身份和严格 audit。approve/release 原 API 忽略的输入在 typed source 必须为 NULL；propose 的 Case* 保存真实旧 case，不能倒填 Input*。每种 action 都将捕获的 reservation deadline 与真实不可变预占绑定，包括没有 finish 的 propose。

approve 以 approval_checked_at 决定是否派生无 attempt incoming，保留原 latest 的 delivery。settlement_decision_checked_at 决定原 recovery 分支，financial_checked_at 是资金锁后最终检查；最终到期将 raw、charged、waived 同时置0，并释放预占。不能用审批时刻延长收费期限。

new_settlement 独立核冻结销售费率、单次 ceil、charge≤R、waived、Key 局部桶及原 settlement/journal/audit/outbox。零价仍可以 settled；HELD/AVAILABLE 即使零 delta 也各有真实 line，REVENUE 仅最终 charge>0。profile block 使用 `(model_id,profile_version)`，created/reused 与本次 audit 精确对应；reused 允许旧 block.request 指向其他请求。今天 Key/ledger 余额及后来 refunded_amount 不是本次历史 before。

pending 不生成新资金原件，但非终态 reservation 与 request billing status 进入 pending，case.reason 保留已有 metering_conflict 优先；case.state 可以仍为 approved。existing settlement 两个结算 clock 都为 NULL，Key 与局部状态不变，不能伪造新 ledger/PB/billing 输出。release 的公开 closed 与原 approved case 保持 approved 分别核对。

[账本/PB叶子](../../internal/procurementprefix/finish_replay_effects.go)逐对象双向核真实 line/event。customer PB 两 scope 分别绑定 control/allocation/source/xid、原 reserve/import event 和本次 settlement event；new 使用真实 admission window，legacy_import 的 baseline 固定0且 window/admitted NULL，拒绝客户 catchup。完整配置/窗口链仍是原独立 PB checker 的职责。

finish 明确保持 `Complete=false`，准确保留三项未实施义务：

1. Reconcile 的历史 request.latest/attempt delivery cut，以及无 latest 时没有发送的完整 absence。
2. 包含中途自动客户流程的 reconciliation 最新人工来源总序。
3. 历史 customer control absence 与完整 PB 配置/窗口闭包。

这些缺口没有用当前投影、时间或 UUID 排序填补。无 latest 的两个原分支仅核局部结果一致性，不产生证明候选。

## 来源续片与金额 ABI 修正

来源入口新增[两列 route_decisions](../../internal/procurementprefix/source_closure_routes.go)严格五字段 JSON、重复 decoded key、int64 priority/uint64 weight、原 UUID 接受集、selected/1000项与 Go canonical 262144B 业务界。允许 PG 空白格式的物理串超过该业务界，只按独立 Go 编码后的长度判断。

[原18表追踪联合](../../internal/procurementprefix/source_closure_original_unions.go)核 legacy/tracked、G角色、dispatch/approval及action基础组；[出生快照](../../internal/procurementprefix/source_closure_birth_snapshots.go)双向核 request26项与attempt15项冻结列及路由规范值，不永久固定当前 delivery/cost/status。

SourceClosure 继续 `Complete=false`、Qualified空，余项为：其他原件业务标量/Usage与payload严格JSON；剩余语义NULL/value与时钟分支；current/永久dispatch/p origin-last及完整效果槽；reconcile最新源顺序；attempt/member连续性；全部完成后的逐对象依赖闭包。成本和finish局部重放不是完整来源认证。

独立复核发现[账户提案/批准审计](../../internal/procurementprefix/account_replay_barriers.go)误将 `Money` 金额期待成 JSON number，已改为真实规范十进制 JSON string。固定字节另核真实 Money ABI，但不使用 writer 生成重放 expected；两个 audit 各覆盖 number/小数/指数/非规范字符串/NULL反例。

## 计数与独立复核

固定计数按物理类型和自然身份去重：finish/outcome、source、`(outcome,account)`、`(outcome,scope)`；缺原件使用所属义务，孤立原line/event有独立类型。错误原因不是新对象。独立 overlay 复现的错误 AccountKind 双计数已修：missing kind 通过真实账户种类定位同一 expected account，与其 effect 同键；一个对象多错计1、跨类型同ID分别计数。另一独立 overlay 复现的 propose deadline 漏绑定已修并保留回归。

成本独立复核另修正缺失 mutation 的空键、dispatch source 的实际 xid、EvidenceChoice 引用 incoming/meter 的同根与前驱检查。反向缺 node 与正常分支使用同一物理对象键，不能将 `cost_mutation`/`mutation` 等错误路径别名当作两个对象；专项覆盖删除 node、保留原件的同对象计数及候选撤销。normal 的真实 RequestDecision 必须有唯一 cost/delivery 输出，managed 冻结 reservation 与最终 p 也作独立比较。

测试使用手工原始行和金额，不连接 PostgreSQL、不调用计价/写入状态机生成 expected。覆盖正常与缺失来源、同额更正、资格优先级、当前顺序、typed NULL、重复/额外 JSON、非法 Unicode、财务过期、零价/零行、pending/existing、两 scope PB、资源与解析中取消。错误/取消/超时返回零结果，不返回部分报告；扫描与排序沿原共享 context/deadline。

## 验证边界

根在所有并行文件冻结、独立审查修复与语义复核后执行：

```sh
env -u TEST_DATABASE_URL go test -race -json ./internal/procurementprefix -count=1
go test -race ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1
```

新包完整 race：87 个顶层测试、434 个子例全部 PASS，0 SKIP、0 FAIL，包耗时 2.695s。原采购证据 checker 回归 PASS，1.898s；旧 checker 源码没有变动。成本专项9组 final race 另通过1.618s，独立 reviewer 的原始 finish 两反例 overlay 修复后均通过；最终采用根完整包结果，不用并行中间编译/专项替代集成。

README、本片设计/架构/验证的本地链接与代码围栏已核对。按 AGENTS 显式语义复核后刷新1745文件清单，只读架构检查与 `make check` 全部 PASS：32表715列/68 generated布局、`go vet ./...`、Vue strict与Vite构建。归档此结果后再次刷新并只读核清单；代码、测试和依赖没有再改变。这些测试耗时不是网关首字节、首 token 或吞吐指标。

前置 [CI run 41646819359](https://github.com/workhold/moni-aigate/actions/runs/41646819359)已只读核对 `headSha=5a49e3359b9fa40ce4d93a786c940dd44f60aeee`、10/10 jobs success，不能自动覆盖本次新提交。

本片没有访问 PostgreSQL、真实供应商、支付或生产环境。完整四域/聚合、固定 scanner/readset、V4/basis/catalog、完整 writer/validator/guard/ACL、宽TEXT全域切换、actual APP 完整矩阵、B-P3/R2以及生产/性能门禁继续保留。

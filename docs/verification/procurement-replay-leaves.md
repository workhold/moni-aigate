# 采购历史私有重放叶子与 Required 对象集合

2026-11-03；实施基线 `5e5dcdf6867b58a1107abe889895e08320b66cfc`。先冻结[实施合同](../../specs/023-periodic-products/b-procurement-replay-implementation-contract.md)，再并行实现来源、请求与账户，根独立实现 Required。模块关系、真实调用与后续接线见[架构细图](../architecture/procurement-replay.md)。

本页保留首轮验收范围和当期余项。后续成本/finish、来源严格路由与冻结列、账户金额审计 ABI 修正及当前剩余义务见[续片记录](procurement-cost-finish-replay.md)，不能用本页旧余项推定新增部分尚未实现，也不能把首轮测试结果当成续片验收。

本片仍无公共 `Check`，没有新 profile 或线上调用。所有结果均为包内叶子；旧采购 checker、V3、数据库候选材料与迁移均不改动。

## Required 与资源合同

从本次原件无损投影独立枚举十一类自然对象，不读取旧报告或调用方计数，不按表总行数推定证明。字节长度编码保留任意 TEXT 分量，单 ID 和 attempt/digest 复合身份均没有分隔符歧义。

[手算测试](../../internal/procurementprefix/required_test.go)逐个比较全部十一项具体键集合，并在相同 raw 输入上真实调用原 `CheckProcurementEvidenceGraph`，逐项核对集合长度与原 Unproven。稀疏 fixture 中的原结构 mismatch 保持原样；这些测试验证兼容枚举，不能证明稀疏数据有效。300 组固定种子差分覆盖输入乱序、无管理 reservation、错误 usage、不同 tenant、更正同值/异值、proposal 屏障与账户版本。单独覆盖 duplicate PK/identity、complex>1、未知成本、明确 configure 与自动 pause、原始 DTO 入口等边界。

[支持层测试](../../internal/procurementprefix/replay_support_test.go)验证 tuple 碰撞/空分量/UTF8、跨数组总行数/字段界、取消和绝对 deadline、处理中途取消返回零集合、Unicode surrogate、重复 decoded JSON key、规范数值与可中断排序。所有索引、嵌套枚举、Unicode 扫描与排序使用同一工作预算；不逐域重开 deadline。

## 来源、请求与账户

[请求域](../../internal/procurementprefix/request_replay.go)独立重算 raw input/digest，核 created/reused meter 的不可迁移归属、全部 attempt cut 成员、竞争 witness 与 absence，保留 source payload mismatch 优先级。request ordinal 重放 sticky/latest，覆盖跨 attempt 与无 attempt 输入、normal/conflict 双向原件及首 identity 的真实 reason/目标；合法幂等快返不能伪造 conflict，未来 meter 不能提前复用。

请求域 `Complete=true` 仅表示本域算法已实施；标量/NULL 联合、generated、完整 root/node 防伪由来源域承担，member/effect 状态由账户域承担。正例确认五类局部候选非空，额外来源/缺原件撤销对应候选；真实旧 meter 无 origin 时传播缺证，保留可直接反驳的矛盾。排序逐比较/拷贝 tick，可中断；JSON 解析前后核原 context。域专项 race 通过、无 SKIP，实际最终命令由根统一重跑记录。

[来源域](../../internal/procurementprefix/source_closure.go)实际调用 physical → semantic 基础联合 → generated → canonical/root 语言 → companion 双向 → 原件 pointer → 四链。72 表布局与真实 PK、NULL envelope、68 generated 逐值、嵌套 reset/root 语言、各链 ordinal/前驱/head、原件指针双向及入口实际执行均有独立正例和变异。request latest 与账户 Config/Buckets 连续性已核。68 generated 使用独立手算 baseline 与逐列变异，未用 SQL writer 计算 expected。

来源域明确 `Complete=false`、`Qualified` 为空，六条实际余项留在结果中：

1. 原件业务标量与严格 JSON（包括 route_decisions 和 payload）。
2. 其余语义 NULL/值联合、reconciliation 角色/提案、management/quote 与时钟分支。
3. 完整原件 payload/current 槽：request/attempt 冻结与末指针、永久 dispatch、p origin/last、精确 ledger/PB 效果集合。
4. 人工 reconciliation 最新来源的序：目前只证明精确历史目标。
5. attempt node 顺序中的 delivery/cost/p before/after，以及 evidence member cut 连续性。
6. 全部义务完成后的逐对象依赖闭合与 Qualified 构造。

[账户域](../../internal/procurementprefix/account_replay.go)从 create 开始独立重放全局账户 journal、配置 revision、逐 attempt reservation revision、H/S/C、reserve/dispatch gate、冻结 quote、evidence/member 前缀及 effect/skip、异常 pause、proposal/approval 屏障和 recovery。核当前账户/p/head 投影、自身 supplier PB marker/source/slot、allocation/window 与 target/delta；完整 PB 配置和窗口序仍由原独立 PB checker 核。

根复核修正了证据字段混淆：Usage+Reason 摘要及 no-consumption 分支使用 `TransportEndReason`，供应商 `FinishReason` 保持独立。测试用固定 SHA golden、`FinishReason=not_sent` 但 transport=done、真实 transport=not_sent 验证，避免 fixture 填错字段而自证。恢复采用 checked_at 严格大于 deadline，等于与未到均拒绝；非法极大 ordinal 不得触发 slice panic。严格快照/audit JSON 拒绝重复 decoded key、非法 UTF16/UTF8 和额外 root；Unicode 扫描及 flat object 循环共享工作预算。

新增共享账户专项使用两条渠道/凭据、两个 attempt 的交错 reserve/dispatch/settle/configure/unknown/release/dispute/approval，按手算核账户 H/S/C、限额/paused gate 和每个 p.last 不串账。零费率情况下分别超过 input/output 硬界仍须 anomaly+pause，不因金额为零放行。

独立审查还发现：同额两次 correction 可以交换 account journal 而保持金额一致。实施合同补充账户动作与 attempt node 的共同顺序，以及观察 cut 的前后动作区间和同 attempt 不回退要求；回归须覆盖完整反序更正、未来/过去 proposal cut 与无 effect evidence cut。所有新反例已由专项通过，独立 reviewer 保持原始重排步骤另建临时 overlay 重测，原漏检现产生 Journal/Proposal/Approval mismatch，相关三类候选全部撤销；正式集成结果另列于下。

账户域 `Complete=true` 只表示本域算法实施并执行，不认证全来源、AttemptCost/delivery 或完整 PB 历史；三个本域候选仍需最终跨域依赖交集。恢复按 attempt 一次排序建前缀、逐 source 二分，不逐次扫描全部历史。测试覆盖 create/config/noop/CAS/not_found、快照和原 Go 摘要、派发 gate、确认/未知/争议、重复/terminal skip、双审屏障、恢复 permit/deadline 与取消。

完整来源与最终聚合未交付前，不把局部 Candidates 转成 Proven，不向调用方提供部分 Check。

## 固定计数的自然对象

按读取合同§3.3复核所有局部诊断：同一 journal 的 before/after/hold、同一 EvidenceChoice 的 member/choice、同一 transition 的 target/delta/原 PE/PB 等多个错误原因，在同一字段只计一次。原始命令按 operation、账户/持仓按各自自然键；真正孤立 journal/PE/member/identity/head 或缺失 approval 义务使用显式类型与自然键，不能靠跨表相同 ID 互相抵消。

请求域另覆盖同 ID 的 incoming/choice、同一 identity 下多条错误 EvidenceChoice、多个 NULL finalization/conflict 输出，以及同一 decision 多项 sticky 缺陷；计数采用物理类型与自然 PK，NULL 输出使用所属 decision 的身份。两域测试都校验具体数字，不只判断大于零。精确键表见[账户测试](../../internal/procurementprefix/account_replay_test.go)和[请求计数测试](../../internal/procurementprefix/request_replay_counters_test.go)。

## 验证与未交付事项

根在全部并行文件冻结、语义复核及计数修正后统一执行：

```sh
go test -race ./internal/procurementprefix -count=1
go test -race ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1
```

新包完整 race 为 60 个顶层测试、280 个子例全部 PASS，0 SKIP、0 FAIL，包耗时 2.562s；原采购证据 checker 回归 PASS，1.833s。Required/support 已另由独立 reviewer 逐分支对照旧算法并运行 race；账户顺序漏洞已由原始独立反例重测拒绝。以上时间是测试包耗时，不是网关首字节、首 token 或吞吐指标。

README、架构细图、验证和合同的本地文件链接与代码围栏已检查。最终仓库门禁按 AGENTS 在语义复核后刷新 architecture 清单，再只读检查并执行 `make check`；它含 Go vet、布局检查和前端 strict 构建，不执行本片未涉及的真实数据库/供应商验收。

前置提交的 [CI run 41623606587](https://github.com/workhold/moni-aigate/actions/runs/41623606587)已只读确认 `headSha=5e5dcdf6867b58a1107abe889895e08320b66cfc`、10/10 jobs success。此结果不自动覆盖本片新提交。

本片为纯 Go，没有访问 PostgreSQL、供应商、支付或生产环境。来源完整闭合、AttemptCost、完整 Base 调用/aggregate、新固定读集/profile、全域 writer/guard/ACL、宽 TEXT 全域切换、actual APP 完整矩阵、B-P3 与 R2 仍未完成。旧 catalog 5 秒 race、其他资金维护与生产/性能门禁保持。

# 033 实施任务与验收台账

状态：2026-11-20 实施中；实现勾选只表示本分支已有对应代码和专项，不代表 032、真实供应商、生产或完整 029 验收。

- [x] 3.0 冻结物理/profile、route 请求、CAS、决策/回放和无候选合同（`design.md`）。
- [x] 3.1 追加 current 迁移：physical、binding、策略、session、decision、统计与 attempt 时间/物理身份；回填并删除旧 attempt 决策列。
- [x] 3.1 调整 028 attempt supplier bounds/Plan/quote guard，保持零售 reservation 与 settlement 不变。
- [x] 3.2 catalog 发布/快照使用物理候选，决策与 attempt 同事务写入；无候选与 Release 同事务。
- [x] 3.3 实现严格 Chat route envelope、完整硬过滤、max_price 和精确最低毛利。
- [x] 3.4 实现 priority/order/cost/price/latency/throughput、可记录 draw 和真实统计快照。
- [x] 3.5 实现 X-Moni-Session 租户隔离粘性、建立/延续/打破原因。
- [x] 3.6 实现显式 auto 单售价、质量 floor 和可见降档；非 auto 不混档。
- [x] 3.7 实现只读 ReplayRoute 与 route-replay CLI，切换平台 API/前端候选解释。
- [ ] 完成 routing/provider/catalog/billing/server/migrations 相关 race、专用 PostgreSQL、前端和真实 main 专项；逐项结果见 `docs/verification/phase3-dynamic-routing.md`。
- [ ] 语义复核并刷新统一架构；记录与 032 rebase 后需重跑的验收。
- [ ] 干净提交后运行完整 029；失败或未运行不得标记终验。
- [ ] 创建堆叠 PR，明确 base、依赖、已验证和未验收范围；不自动 merge。

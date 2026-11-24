# 033 实施任务与验收台账

状态：2026-11-23 基线校正。实现与本地完整验收已完成，验证代码提交 `e7b0de825232b7f10b81f941aa1b5576c845066a`；PR workhold/moni-aigate#31 已在 CI 28/28 全绿后合入 main（merge `d51bd24`）。真实供应商、生产、HA、固定资源性能，以及 041 的场景质量和现金利润资格不在完成声明内。

- [x] 3.0 冻结物理/profile、route 请求、CAS、决策/回放和无候选合同（`design.md`）。
- [x] 3.1 追加 current 迁移：physical、binding、策略、session、decision、统计与 attempt 时间/物理身份；回填并删除旧 attempt 决策列。
- [x] 3.1 调整 028 attempt supplier bounds/Plan/quote guard，保持零售 reservation 与 settlement 不变。
- [x] 3.2 catalog 发布/快照使用物理候选，决策与 attempt 同事务写入；无候选与 Release 同事务。
- [x] 3.3 实现严格 Chat route envelope、完整硬过滤、max_price 和精确最低毛利。
- [x] 3.4 实现 priority/order/cost/price/latency/throughput、可记录 draw 和真实统计快照。
- [x] 3.5 实现 X-Moni-Session 租户隔离粘性、建立/延续/打破原因。
- [x] 3.6 实现显式 auto 单售价、质量 floor 和可见降档；非 auto 不混档。
- [x] 3.7 实现只读 ReplayRoute 与 route-replay CLI，切换平台 API/前端候选解释。
- [x] 完成 routing/provider/catalog/billing/server/migrations 相关 race、专用 PostgreSQL、前端和真实 main 专项；逐项结果见 `docs/verification/phase3-dynamic-routing.md`。
- [x] 语义复核并刷新统一架构；记录与 032 合并后重跑的验收及历史失败边界。
- [x] 干净提交完成完整 029：r21 run `78a97ecd77d34a348ddffadb6e3c64da`，41/41、0 必需 SKIP、current-MVP 100/100、桌面/390px 浏览器 10/10。
- [x] 创建 PR workhold/moni-aigate#31，明确 base、依赖、已验证和未验收范围；不自动 merge，审查与 CI 状态另以 PR 为准。

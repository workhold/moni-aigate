# 041-P0 经济基础验证记录

2026-11-24；实现合同见[聚焦子规格](../../specs/041-profit-hybrid-platform/p0-economic-foundation.md)。基线为 main `907f4697464372316b29f885629a8204b79028de`。本记录区分纯函数、专用 PostgreSQL、真实 HTTP/主进程、前端和完整 029，不授予真实供应商、正式支付、external commercial 或生产资格。

## 环境与边界

- 专用 `colima-moni-acceptance-041`：4 vCPU、8GiB、60GiB；独立 PostgreSQL 16.15 容器 `moni-041-pg`，仅本机 `127.0.0.1:55439/moni_test`。
- 测试按随机 schema/租户隔离；供应商均为本地 deterministic HTTP fixture，不产生真实供应商请求或费用。
- 不修改 032/033/040 环境、长期预览、默认 serial-v1/workers=2 决策或验收断言来规避失败。
- P0 没有商品/质量/FX 配置管理 HTTP。内部发布与数据库 fixture 只证明 test-only 接缝，不是管理面或商业发布验收。

## 已实现专项

| 范围 | 测试/结果 | 当前状态 |
| --- | --- | --- |
| 金额与 FX | `internal/economics` race：微法币 JSON、CNY/USD 向量、双向保守 FX、费用/取整、缺失/过期/溢出、累计成本和零价反例 | PASS |
| 不可变迁移 | `TestProfitEconomicsCurrentMigration`、`TestProfitEconomicsFxVersionSealed`：current/Verify/reapply、USD 成本约束、版本封口和不可变性 | PASS，专用 PG |
| CNY fixed-full | `TestProfitOfferFixedFullReservation`：Reserve 基准、单 attempt、独立成本上界重算、PrepareDispatch、confirmed final、固定全额及唯一结算 | PASS，专用 PG |
| USD 原币 | `TestProfitOfferUSDOriginalCurrency`：独立 USD 租户/付款/供应商成本、固定全额与 USD 快照 | PASS，专用 PG |
| 质量与撤回 | `TestProfitOfferReleaseAndQualityWithdrawal`：证据撤回后 PrepareDispatch 拒绝、释放不变、offer 撤回后新 Reserve 拒绝 | PASS，专用 PG |
| 真实 HTTP 数据面 | `TestProfitOfferActualGatewayFixedFull`：真实 Server/HTTP/socket/provider fixture，fixed-full、统一报表、部分消费退款与退款币再次消费；两次 POST，净付款与贡献毛利只计当前净消费 | PASS，本地 mock，非供应商资格 |
| 定价 HTTP | `TestRestrictedAPPCostPricePublishHTTP`：现有 CNY/unknown/null/CAS/回滚语义及新增 USD currency | PASS，专用 PG/受限 APP |
| 路由与回放 | FX 反例中 CNY 70→USD 10 优于 USD 30；FrozenExecution 携带服务端单 attempt、成本/质量/物理引用；既有 route race 通过 | PASS |
| 统一报表 | `internal/reporting` 全包 race + 专用 PG：净退款、取整、再消费、lot 上界、inner 修订、outer 更正、provisional/confirmed、交叉 FX、offer/task、CSV | PASS |
| 前端 | 397 项前端测试、TypeScript typecheck 与 production build；严格 DTO 和暂估/账单确认/FX 标签 | PASS |

早期失败及修复均保留在会话工具记录：reservation FK、pricing operation 列名/币种、旧序列化兼容、报价生效顺序、净退款零额币种、历史 ACL 和迁移顺序。任何中间失败都不是最终通过证据。

独立审查后新增并通过真实 PG 反例：摘要正确但 JSON/政策不一致的快照拒绝、Reserve/撤回状态锁、无效 task policy 发布拒绝、fixed-full 实际报表与退款币再消费。快照 guard 对 request/reservation、政策、FX、执行、任务候选、字段闭集和规范字节逐项交叉核对；数据库执行错误不会冒充业务拒绝通过。

## 汇总回归与完整验收

- 实现提交 `7d60bb34738b9a07b48dbf621bb46db4dedf00d8` 的完整 `go test -race -p 1 -count=1 -timeout 1200s -json ./...`：PASS，3326.4 秒；覆盖 economics/routecost/routing/catalog/billing/reporting、operations/migrations 及全部其他 Go 包。
- `make check`：PASS；包括架构只读校验、采购布局、上游来源、`go vet ./...`、前端 TypeScript 检查和 production build。
- 完整 029 第三轮运行 `84b4aa26f9e145b5a646b93f79badeda`：**overall PASS，41/41，fail=0，skip=0**。使用默认 serial-v1，未启用 workers=2；汇总为 `/Users/vchive/.cache/moni-aigate-acceptance/041-p0-7d60bb3/full029-r3/summary.json`，原始输出在同目录旁的 `raw-r3`。
- 本轮 static/frontend、全仓 race、current-MVP、identity lifecycle、mail lifecycle、payment process、preview 启停和 mock deployment 均为 PASS；真实 main 的独立部署及完整 payment Guard-loss 生命周期也在专用环境中通过。
- 本轮浏览器证据 `/Users/vchive/.cache/moni-aigate-acceptance/041-p0-7d60bb3/browser-final-r3/browser-evidence.json`：PASS、completed=true，桌面与 390px 共 10/10 项通过。
- 前两轮完整运行未获得全套通过，失败产物分别保留；第三轮从 static 开始完整重跑，未跨轮拼接任何阶段结果。备份恢复脚本产生的本地 JSON 运行产物未纳入本次交付。

上述原始验收产物保留在本机缓存，不作为仓库内伪造的供应商或生产证明。最终文档更新后再次执行语义架构 refresh/只读检查及 `make check`，结果以最终提交和 PR 检查为准。

## 资格结论

即使所有本地测试通过，P0 仅表示 direct test-only 经济基础。`margin_admission_status=pass` 不是 realized margin；contract 只产生暂估贡献毛利，FX 快照折算不是实收现金毛利，全部相关成本账单确认后才可显示账单确认贡献毛利。没有真实供应商、生产、外部 inner fencing、自动换汇或外部商业开放声明。

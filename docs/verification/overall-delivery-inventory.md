# 001–028 整体交付盘点与并行计划

更新时间：2026-11-10。当前工作基线是 `codex/initial-mvp`；以下状态以当前源码、SDD、验证记录和当前专库证据交叉判断，不能把历史记录直接当作生产验收。

工作目录核对：本轮唯一开发目录是 `/Users/vchive/Documents/ChatGPT/aigate/moni-aigate`。它与 `/Users/vchive/Documents/aigate` 处于同一分支、同一基线提交；对两棵工作树做文件集合、内容和三方合并复核后，旧目录没有独有源码路径，当前目录包含全部旧目录已存在的非冲突内容。两者差异主要是当前目录已经推进的向量计量、单一路径和验证改动；旧目录仍保留若干较早的标量/兼容实现。旧目录中的旧标量 bypass、fallback 和历史 fixture 变更没有回拷，避免把 ADR-005 已淘汰的兼容路径带回当前实现。

当前状态补充：运行时迁移已经收口到单一 `migrations/current_mvp.sql`，serve/worker/payment/periodic-budget 当前入口使用 `VerifyCurrent` 族只读校验；历史迁移和研究 fixture 不进入当前服务。只服务历史维护扫描的 billing raw reader、`internal/maintenancescan` 和 maintenance QA 已由 `historical` build tag 从默认当前构建图隔离。图片、TTS、video task 和单 turn Realtime 已接当前 media wallet/settlement，结果 artifact、task 正文和 session/turn/event 已持久化。2026-11-10 更新后的 Compose 业务 smoke 在新基线通过。完整 028-U guard/六文本商业出口、正式供应商、浏览器和目标环境仍保持未完成状态。

## 判断标准

“已实现”表示源码入口存在；“本地已验”表示隔离 PostgreSQL/HTTP/race 或本地 fixture 有证据；“浏览器已验”表示隔离真实 main 和浏览器实际操作；“正式已验”表示真实供应商、支付商、部署或生产环境有证据。四者不相互替代。

## 001–027 当前状态

| 范围 | 当前已完成 | 仍缺的能力或验收 | 对 028 的关系 |
| --- | --- | --- | --- |
| 001–006 商业基线 | 身份、租户、Key、模型/渠道、预占、结算、取消、恢复和三文本基础链路已有实现与本地闭环；008 的批次写集、尾写回滚、恢复和固定 binary 已完成 55439 专库与集成复核 | 正式供应商、生产部署、灾备、持续性能和外部支付仍未完成 | 作为不变量和入口，不能另起兼容路径 |
| 007–010 协议与调度 | 六文本入口、SSE、检索、Gemini、usage 门禁和完成调度已有本地证据 | 正式供应商硬界、账单、SDK、生产/性能验收 | 直接提供 028 的协议、usage、零价和未知语义 |
| 011–017 目录、凭据和 Key | 目录发现、凭据池、批量启停、来源策略、运行限额、基本配置已通过限定本地验收 | 正式渠道、分布式限流、完整浏览器和生产验收 | 028 复用快照、撤权和派发屏障 |
| 018–020 成本、采购、公开目录 | 成本感知路由、采购账户、公开目录和分组售价已有本地/部分浏览器证据 | 正式供应商账单、采购余额适配、生产部署和性能 | 是 028 价格/成本/route 快照的直接依赖 |
| 021 自助身份 | 注册、邮箱证明、密码恢复、MFA、outbox、worker 和本地 SMTP 已实现 | 指定 claim/SMTP 接受前窗口的真实进程故障、正式收件域名和反滥用 | 不阻 028 内部开发，阻生产发布 |
| 022 自助支付 | 兑换券、支付沙箱、订单/通知 worker、HTTP/UI 和本地 main 闭环已有限定证据 | 正式 PSP、真实收款、COMMIT 回包丢失、来源 lot、原路退款、拒付、交叉恢复 | 不阻 028-U 内部开发；不能替代 027 来源和退款事实 |
| 023 周期与商品 | A 分组售价已验；B 周期预算核心、HTTP/UI、Reader/Commander/Verifier 有本地实现 | B 完整产品/故障矩阵；C 商品购买、订阅、权益和来源闭环未实现 | 023-A 是 028 组价依赖；C 不阻 U |
| 024 扩展平台 | A 告警/来源观察已验；当前 MVP 基线已接入 028-U 文本计量、028-S 图片/TTS、028-A video task 和 028-R 单 turn Realtime 的数据库、HTTP/WS、mock provider、wallet/settlement 与重启恢复路径 | E 身份扩展、F通知、真实供应商认证、浏览器和正式目标环境仍未完成；完整 028-U guard/商业全矩阵单独保留 | 当前媒体链只支持本地 mock 内测，继续复用唯一 `Profile → Plan → Reserve → Attempt → Observe → Settle` 路径 |
| 025 生产/HA | Compose 配置渲染和邮件窄片通过限定验证 | 容器/1Panel 实跑、模型/支付故障点、共享限流/许可、数据库故障、备份恢复、RPO/RTO、正式外部服务 | 不阻 U 开发，阻商用发布 |
| 026 经营报表 | 请求元数据、唯一收入、退款、全部 attempt 成本、unknown 毛利、CSV/UI 已接线 | 大规模/正式部署/正式账单验收 | 028 必须继续只读唯一 settlement 和全部 attempt 成本 |
| 027 钱包来源 | 四来源、FIFO 预占/结算/释放、退款来源图、HTTP/UI、只读核对已接线 | 真实原路现金退款、拒付/债务、外部事实灾备；旧历史明确拒迁 | 028 只复用来源写入口和不变量，不改第二套账务 |

001–020 的主要缺口集中在正式供应商、生产部署、浏览器完整操作和性能；021–025 的未勾项主要是外部故障、支付/商品/媒体/HA；这些不能伪装成代码完成，也不应阻塞 028-U 的内部单一路径开发。

## 028-U 当前状态

028-U 的主链已经形成：`provider → inference → metering → routecost → catalog/routing → billing/procurement → reporting`。费率是约分有理数，金额由统一向量计算最后一次 `ceil`；请求和 attempt 保存不可变计划、报价、界行和来源指针；恢复不重新生成模型请求。S/A/R 当前 MVP 也已复用同一媒体 Reserve/Attempt/Observe/Settle、wallet、settlement、audit 和 recovery 路径，详见[纯域验证记录](028-media-pure-candidates.md)及媒体验证记录。

已落地并有 55439 专库局部证据的内容包括：

- 024 候选单文件的文本 schema、canonical/digest、bounds/rates/QuoteDigest、request/attempt plan、parent plan、观察和双侧终态；
- 默认价、组价、采购价的完整向量 operation、指纹、回执和审计局部闭环；
- route/binding/cap 出生关系、非零 route revision、客户 R、结算金额和能力完整性 guard；
- 当前六协议受限 APP 的 paid/known-zero、unknown 策略、等待期间原价、默认/组价组合、退款来源、过期未知用量恢复不重生成；
- 026 报表和采购读端继续读不可变快照、唯一结算和全部 attempt 成本。

仍未达到完整出口的内容：

1. 九组/十类完整 guard 的正反例、跨租户/跨 route/并发/失败回滚闭包；其中 route_revision=0 的 SQL 旁路已移除，当前已有 55439 负控矩阵，剩余完整正反例闭包仍未形成全矩阵。
2. 空库拒迁、旧金融历史拒迁、ACL、owner、APP/projector/PUBLIC/payment worker 的生产级全矩阵。
3. 六协议超界、supplier late、conflict、人工核对、unknown 和退款/来源/恢复的统一受限 APP 总矩阵。
4. 全部 HTTP/TS/UI 读端的向量合同和真实浏览器验收；管理端每百万 token 输入与人工核对标量必须保留，历史 fixture/冻结研究代码不能误删。
5. 正式供应商计量/账单认证、浏览器完整验收、1Panel、HA、备份恢复和性能基线。Docker Compose 单机的空库安装、provision/bootstrap、文本/图片/TTS/video task/Realtime smoke 与 app/worker/PostgreSQL 重启已通过，但不等同正式部署验收。

本轮盘点后的新增证据：支付 worker 的计量表/函数负权限已有独立 55439 负控；六协议超界、客户人工核对保持供应商 unknown、以及 supplier unknown 禁止派发已有受限 APP 反例专项；前端审查确认当前在线报价/界/用量出口使用完整向量 DTO。上述证据仍属于本地专项，不能把对应任务项改为完成。

随后补充的候选 024 拒迁负控覆盖 api key/procurement held-spent、周期预算 allocation/event、funding lot child 以及 payment transaction identity/capture/effect；每个案例都确认迁移失败且 schema snapshot 未变化。该矩阵扩大了旧金融事实的拒迁证据，但没有把候选迁移变成已发行生产 DDL。

## 依赖图与执行顺序

```text
001/003/004/009/010 协议与硬界
              │
018/023-A 价格、采购与 route 快照 ──┐
026 报表金额口径 ───────────────────┼─> 028-U 完整 guard 与六文本出口
027 钱包来源/退款/恢复不变量 ───────┘              │
                                                   ├─> 028-S 同步媒体
                                                   ├─> 028-A 持久任务
                                                   └─> 028-R 实时轮次
```

必须串行的主线只有共享事实边界：

- 先冻结失败边界和数据合同，再修改 024；
- 迁移/guard/权限与账务资金整合由主集成人统一落盘；
- 共享 PostgreSQL 安装、迁移测试、账务集成和最终架构快照串行执行；
- S/A/R 不在 U 未闭合时改共享 settlement、wallet 或 recovery 核心。

可以并行的分片及写集合：

| 分片 | 写集合 | 禁止触碰 | 交付物 |
| --- | --- | --- | --- |
| DB/权限 | `migrations/024_unified_text_metering.sql`、`migrations/unified_text_metering_postgres_test.go`、`internal/operations/meter_roles*` | billing 资金算法、001–023 原迁移 | guard/拒迁/权限矩阵与专项测试 |
| 账务/采购 | `internal/billing`、`internal/procurement` 的非迁移文件及专项测试 | 024 SQL、server 共享装配 | 成本/采购/退款/恢复边界，待主线集成 |
| 协议/商业测试 | provider 文件和独立 server 集成测试 | billing 核心、024 SQL | 六协议超界/unknown/late/conflict 的证据 |
| HTTP/UI | `web/src`、`web/tests`、浏览器 QA 脚本 | server 共享装配、账务/迁移 | 向量 DTO、标量出口清单和前端验证 |
| S/A/R 设计 | 新增独立媒体/任务/实时包及合同 | 当前 U settlement/wallet 共享表 | 只有合同和纯域原型，U 完成后再接主链 |
| 主集成 | `internal/server`、`cmd`、架构 docs、迁移集成、最终检查 | 并行分片未审代码 | 原子汇合、语义复核、架构 refresh/check |

并行代理必须在独立 worktree 或明确不重叠目录中工作；每个分片先报告合同和失败边界，再编码。没有用户另行授权，不提交、不推送、不合并。

## 结论

当前不需要推倒重做。最快且风险可控的路线是：主线先收口 028-U 的迁移/guard/权限，旁路并行补六协议商业反例和 HTTP/UI 向量出口；022/023/025 的外部门禁和 S/A/R 保持独立，不把它们混进 U 的资金核心。所有结果最终由主集成人统一做 PostgreSQL 专库回归、架构复核和未完成项更新。

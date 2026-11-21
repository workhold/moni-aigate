# 033 文本动态路由验证记录

2026-11-22。最终实现与完整本地验收提交为 `e7b0de825232b7f10b81f941aa1b5576c845066a`，基于已合入 032 的 `main`（`9947ec418b7716affe6e6d562d5d898cb0e23531`）。堆叠 PR：workhold/moni-aigate#31；未自动合并。

## 最终实现边界

- 物理模型、能力与 channel+physical binding 不可变；route publication 继续使用 CAS 和不可变版本。
- 同一 routing Plan/Engine 完成 binding 级候选硬过滤、priority/order/cost/price/latency/throughput、真实统计快照、抽签、sticky、auto 单售价与可见降档。
- Chat `route` 严格解析并剥离；only/order/ignore/allow_fallbacks/require_parameters/sort/max_price 均已实现。媒体、任务和 Realtime 不进入动态路由。
- `route_decisions` 保存完整候选、策略/请求/统计快照和 draw；无候选及准入失败与 Release 同事务。`route-replay` 只读，不重新生成模型请求。
- 原 Reserve → RecordAttempt → PrepareDispatch → Observe → Settle、SafeToRetry、唯一结算、采购成本和未知消费不重放保持不变。
- current 015 为追加迁移，覆盖非空历史 attempt/decision 回填、物理供应商界与角色分离。

## 最终完整证据

### Go race

- run_id `78a97ecd77d34a348ddffadb6e3c64da`，workers=2，14 shards / 17 parts，原始 Go JSON 事件失败数为 0。
- 全部 152 个 required specs 通过绑定 run_id、HEAD/tree、freshness 和原始事件的 strict-v2 只读校验。
- 双活动 PostgreSQL cluster、14 个 database identifier、default ACL/advisory lock 独立性、container/netns/volume 清理及 host listeners zero_new 均通过。
- runner wall 2284.340 秒；证据：`/Users/vchive/.cache/moni-aigate-acceptance/phase3-go-r21/evidence/`。

### 桌面与 390px 浏览器

- final evidence：`/Users/vchive/.cache/phase3-browser/browser-r21-final-observed/browser-evidence.json`。
- `status=PASS`、`completed=true`，10/10 项的 desktop/mobile390 全部 PASS；不是 PREVIEW 或 PARTIAL。
- app/worker/UI mock 共同镜像 `sha256:ed23f186614c4a021e40b0e2fe835afe07bd66f2e9d9a55f7b46334bc5b30ccd`，source commit/tree/clean 与最终提交一致。
- 覆盖公开目录、管理页、MFA、改密、新密码 reauth、Key/Playground/撤销、核款/退款/券、CSV、告警及 390px 导航。

### 完整 029

- 机器汇总：`/Users/vchive/.cache/phase3-browser/full029-r21/summary/summary.json`；原始日志与同轮证据：`/Users/vchive/.cache/phase3-browser/full029-r21/raw/`。
- **41/41 PASS，0 FAIL，0 必需 SKIP**；41 个矩阵项及每个必需 Go binding 均为 PASS。
- current-MVP **100/100**；matrix 77 个唯一测试、82 个 bindings；严格复用同 run_id 的 workers=2 evidence。
- 整单 wall 867.989 秒，serial tail 847.796 秒；不拼接任何旧失败 run 的部分 PASS。
- static architecture/procurement/upstream/vet/build、frontend test/build、current-only migration/guard、wallet/settlement、backup/restore/recovery lock、Compose migration/provision/restart/business smoke、真实 main identity/mail、支付进程/恢复、mock-deployment 全部 PASS。
- Compose 业务实跑包括 text、image、TTS、异步任务及 Realtime WebSocket；PostgreSQL/app/worker 重启恢复通过。

## 专项与修复回归

- routing/provider、catalog、完整 migrations（含 current 015 非空回填）、动态 order/no-candidate/auto、真实 50 样本统计、billing 成本域/Gemini/采购 guard、准入失败后 replay 均已通过。
- 032 cache hit、首事件、版本化熔断策略与 033 动态路由组合目标通过；前端完整测试和生产构建通过。
- 任务 HTTP/worker 提交竞态增加确定性“竞争者先完成”回归，修复 Snapshot 与 BeginSubmit 窗口内按原 key/digest 重放；50 轮真实 race 通过且 adapter Submit 始终为 1。
- 浏览器行操作使用既有有界 wait 等待表格渲染，13 个 browser QA 单元测试通过。
- deployment QA 将受保护 provisioning stdout 与 business.env 分离，避免 GNU cat 同文件读写；deployment business shell/WebSocket 自测通过。

## 失败历史与环境边界

以下均为最终成功 run 之前的诊断，未删除失败证据、未用轮询或延长超时替代修复：

- r12 的 unknown recovery `{0 0 0 0}` 与仓库既有双校时源时钟回拨模式一致。033 专用 VM 停用重复 `systemd-timesyncd`、保留 guestagent 后，560 个 guest/PG 样本无负时间步；原一次性断言保留。
- r17 的 `task_no_resubmit` 暴露真实提交竞态，已在 tasks.Service 修复，不是通过允许额外测试错误规避。
- 隔离浏览器曾遇到 Chromium `/dev/shm` 资源耗尽、Docker client/daemon 路径不一致和 VM/macOS host-gateway 分裂；最终统一 VM-local PG、单校时源及 Chromium 启动参数。
- current-MVP 环境最终使用有界编译（`GOFLAGS=-p=1`、`GOMAXPROCS=2`）、Asia/Shanghai PG、匹配 PG16 客户端和仅 Docker bridge 可达的模块 HTTP 代理。备份恢复、全模块下载和 Compose 业务实跑均单独验证后进入最终整单。
- r14/r15/r16/r18/r19/r20 的 early failure 或 freshness 失败均不作为终验证据；最终只采用上述 r21 整单。

## 尚未认证或不得外推

- 真实供应商、正式支付商、生产部署、HA、供应商资格和性能对比未认证；deterministic mock 与专用 PostgreSQL 不能替代这些验收。
- 规格全部原因枚举的逐条 PostgreSQL 矩阵、质量/成本粘性打破的全组合和性能基线不是本次完整 029 的覆盖声明。
- 本记录不表示 PR 已审查、CI 已全绿或已合并；这些状态以 workhold/moni-aigate#31 为准。

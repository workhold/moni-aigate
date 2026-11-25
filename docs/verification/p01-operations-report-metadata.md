# 042 经营报表媒体空三元组解析修复验证（P0.1）

2026-11-25；状态：修复与验证已完成。规格见[042 状态合同](../../specs/042-operations-report-metadata-states/spec.md)。本片只校准经营报表前端严格解析的元数据三态联合，不表示真实用户内测、第一阶段 41 项新结论、正式供应商或生产验收完成。只读参考本地 `worktree-beta-experience-review` 提交 `4772a05` 的三态思路，未引入该分支其他工作；服务端 SQL、聚合、CSV 编码与权限零改动。

## 基线与根因

- main `41f66e4`（041 P0 已合入）上，未筛选经营报表同时含文本与媒体请求行。`mediaRequestsSQL` 按线路合同输出 `profile_version`（`media_uuid` 非零小写 UUID）和空 `schema_id/schema_digest/variant_key` 三元组；前端解析器要求每行 `variant_key='default'` 且 profile/schema 全部非空，一条合法媒体行即抛"经营报表响应含未定义或旧标量字段"，API 200 但整页报错。筛选 Chat 恰好只剩完整快照文本行，掩盖了未筛选缺陷。
- `request_records.protocol` 可空、计量快照 LEFT JOIN 未命中时四元组同时为空，均为既有防御性线路事实；`media_provider_runtime.go mediaFinalizationValues` 规定 mock 媒体一律 released、零收费、unknown 供应商成本（业务 QA 不得花费真实钱包），因此媒体行的合法展示本就包含零/待核对状态。

## 实施内容

- `web/src/operating-report.ts`：响应行类型新增 `OperatingResponseProtocol/OperatingResponseExecutionKind`（闭集 ∪ `''`）与 `variant_key: 'default' | ''`；新增 `reportMetadata()` 只允许三种组合状态（完整文本快照 / 媒体空三元组 / 无快照文本），`protocol/execution_kind` 各自独立允许缺失标记或既有闭集值；`reportUUID` 与数据库 `media_uuid` 一致（非零小写 canonical）。查询侧 `OperatingFilter`/`Filter.Validate` 保持非空闭集不变。
- 失败关闭不变：任何部分三元组、伪造媒体快照（媒体协议配完整三元组）、半快照（文本协议配有效 profile + 空三元组）、非法/大写/nil UUID、非 64 位小写 digest、未知 variant、未知非空枚举、null/错误类型、缺字段/多字段，均整表拒绝，不跳行、不补造 `default`、不从当前模型推断 schema。
- 不变量：041 P0 全部金额字段（payment_basis/provider_cost/暂估与账单确认毛利/FX/商品维度）、键闭集、generation/sequence 失效、CSV 下载状态与错误文案保持原样。

## 已执行结果

| 检查 | 结果 | 边界 |
| --- | --- | --- |
| 目标运营报表 Node 套件 | 21/21 PASS | 四类媒体空三元组、完整快照、无快照文本、五路混合正例；六种部分三元组、逐字段非法值/缺失、伪造媒体快照、文本半快照、未知枚举负例；P0 金额字段在媒体路径原样保留 |
| 独立 042 状态矩阵 | 37,632 种组合 PASS | `web/tests/operating-report-matrix.test.mjs` 逐一对照三态、空值、非法类型与枚举 |
| 完整前端 | 401/401 PASS，0 FAIL/SKIP | 全部 `web/tests/*.test.mjs`；`vue-tsc --noEmit` 与 Vite 生产构建通过 |
| reporting 纯+专库 race | PASS | `go test -race -p 1 -count=1 ./internal/reporting` 于 127.0.0.1:55439 专库；CSV 36 列完整/空元数据逐列保留；媒体行 profile 保留且空三元组原样；无快照线路六元组全空 |
| server 报表集成（含 race） | PASS | 既有商业链五子项；新增 `TestOperatingReportMixedMetadataRealLifecycle`：同一响应并存真实文本结算行（完整快照）与真实 Realtime 媒体行（空三元组），JSON 逐字段断言、CSV 逐列断言、P0 字段与权限不变；报表读取前后 gateway POST 计数不变 |
| `make check` | PASS | 只读架构检查、procurement/provenance、`go vet`、前端构建 |
| 架构快照 | `e72294a7665b`（2657 文件） | 语义复核后 `--refresh --reviewed`；统一架构能力行与 operating-reports 正文补充三态合同 |

## 隔离预览与浏览器证据

- 预览：`phase1-browser-preview.sh` 从干净提交 `fbec003` 构建镜像 `moni-aigate:phase1-browser-fbec003e5b2f`，独立 schema `phase1qa_1795591794_78460`（55439 专库），app/worker 容器部署于 `colima-moni-acceptance-041`，公网入口仅 `127.0.0.1:18081`。数据库、渠道、私有配置未修改；未触碰 jdyvchive 或任何远程服务。
- 数据：`phase1-mock-acceptance.sh` 全链通过（run `phase1-acceptance-7038c63ce62d`，17 请求 × 6 文本协议）；随后以 owner 会话创建客户 Key（文本模型 scope + `mock-tts`），经部署应用驱动确定性 mock 图片与 TTS 生成，产生真实媒体请求/尝试/结算行。媒体结算按设计 released、零收费、unknown 成本（`mediaFinalizationValues`），聚合字段非空（attempt=1、unknown=1）。
- [六场景证据 JSON](after-001/p01-browser-evidence.json)：**6/6 PASS**，脚本 `scripts/p01_report_metadata_browser.py`（登录与 GET/观察为主，仅注入场景改写网络响应）：
  1. 未筛选混合报表（桌面 1440）：21 行全部渲染，无解析错误；P0 指标卡全部有值（客户消费币数 0.000042 币、客户付款基准 ¥0.000042、采购成本 ¥0.000074、暂估/账单确认贡献毛利"待核对"原义展示、21/32 请求/尝试、3 未结算、10 未知成本），无横向溢出（scrollWidth=1440=viewport）。
  2. 媒体空三元组可见：mock-image-generation/mock-tts 行正常展示（聚合非空、金额为零/待核对的原义展示）。
  3. 纯 Chat 协议筛选：9 行渲染、无错误——旧证据路径继续成立。
  4. 未筛选 390px：21 行、scrollWidth=390=viewport，无溢出无错误。
  5. 非法结构注入·文本部分三元组（客户端 fetch shim 在网络边界注入 `schema_id=''` 且删除 `variant_key` 的行）：整页失败关闭，仅显示严格解析错误，经营报表金额指标全部消失（同页其他面板计数不受影响）。
  6. 非法结构注入·媒体伪造完整三元组（媒体行配 UUID+digest+default）：同样整页拒绝。
  - 截图：[混合桌面](after-001/report-mixed-desktop.png)、[混合 390](after-001/report-mixed-390.png)、[纯 Chat](after-001/report-pure-chat-desktop.png)、[注入拒绝×2](after-001/report-illegal-text-partial-triple.png)。
- 浏览器只执行登录、GET、筛选观察与确定性 mock 媒体生成；无真实供应商、支付或其他业务写。注入仅改写页面网络响应，不改数据库、不改服务端。

## 边界

- 029 共享商业链候选按仓库要求在干净最终提交 `fbec003` 重跑完整默认 serial-v1 029：**r2 整单 PASS 41/41、fail=0、skip=0**（run `d0888993608b4a80b9b51cc212292cd7`，wall 4307.4s，go-race 3604.6s，matrix 82 绑定、current-MVP 100 必需测试，summary 于 `~/.cache/moni-aigate-acceptance/p01-fbec003/full029-r2/summary.json`，原始日志同目录 `full029-raw-r2/`）。r1 因两个 migrations 测试在 `CREATE ROLE` 上 3 秒锁超时（55P03，guard fixture `lock_timeout='3s'` 的环境性瞬时角色目录争用）失败，其余套件全部 PASS；两项测试隔离重跑与整个 migrations 包 `-race` 重跑（243.9s）均 PASS，确认与本片 diff 无关（本片不含 migrations/角色/SQL 改动）。r1 原始输出保留于 `full029-raw/`，不作为终验复用。浏览器 gate 复用同提交同镜像 10/10 PASS 隔离证据（033 r15 先例）。本机 55439 专库上 6 个 10 天前遗留的空闲 moni_app/moni_projection 会话已在重跑前清理。
- SKIP 不计入数据库或浏览器验收。真实供应商、生产认证、workers=2 变更、external commercial 均不在本片范围。

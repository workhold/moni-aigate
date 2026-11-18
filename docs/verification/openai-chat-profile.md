# 038-PR3 deterministic Chat profile 验证记录

本记录对应[PR-3 实施合同](../../specs/038-new-api-capability-migration/openai-chat-profile.md)。范围仅为本地 deterministic/mock、专用 PostgreSQL、真实 main 和隔离浏览器；不授予真实供应商、commercial、生产、032/033 或正式支付资格。

## 1. 实现与不变量复核

- Chat JSON/SSE 继续由既有 `provider.Client`、`Response` 和 gateway 统一处理；PR-3 没有新增执行核心、钱包、计量、结算、成本或路由事实表。
- deterministic mock 的 `chat_tool_calls` 产生两个完整/分片 function calls、tool_calls 终态和最终 usage；外部 relay 与 direct embedded 使用同一商业链。
- Playground 只按 index 重组并展示工具，不执行函数或自动续写；实际 finish reason 与“接收完成不等于已结算”同时可见。
- route history 是 admin-only 只读安全投影，没有供应商 I/O；activation 继续使用既有 expected-revision CAS、live credential、发布证据、CSRF/reauth/recovery 和审计边界。回退后再发布在模型锁内从历史 MAX+1 分配新 revision，绝不复用不可变历史。
- activation journal 不假设 revision 单调递增，未知/冲突结果禁止自动重发，只允许只读观察和显式新操作。
- 迁移只追加：本片没有新增或修改 SQL。租户隔离、不可变历史、硬预占、唯一结算和 unknown consumption 不重放不变。

## 2. 专项证据矩阵

| 范围 | 测试/检查 | 证明边界 |
| --- | --- | --- |
| mock fixture | `go test -race ./internal/testsupport/mockupstream` | 双工具 JSON、SSE 分片顺序、终态、usage 和 fault 白名单 |
| 统一商业链 | `TestOpenAIChatProfileToolJSONAndSSEAcrossImplementations`、`TestOpenAIChatProfileLengthAndContentFilterSettleWithoutReplay`、`TestOpenAIChatProfilePublishesFreshRevisionAfterRollback` | external/embedded × JSON/SSE；模型映射、parallel 请求、工具与 usage、单 attempt/单 settlement、精确 20 微币收费；length/content_filter 不续写或重放；回退后发布获得新 revision |
| 既有 035 非回归 | `go test -race -run '^TestExternalRelay.*' ./internal/server` | direct/New API/Sub2API、安全切换、未知消费 no-replay 与原 revision 回退 |
| route HTTP | `TestRouteRevisionHTTPHistoryAndNonMonotonicRollback` | admin 权限、秘密 canary、CSRF/reauth/recovery、stale CAS、数值下降回退和无供应商 I/O |
| route 分页 | `TestRouteRevisionHTTPPagination` | 52 个真实 revision、50+2 keyset 页面、无重复/遗漏和唯一 active |
| provider/catalog | 两包显式专库 race | 既有协议、不可变发布、commercial/test-only 与成本门禁不回归 |
| Web | `npm --prefix web test`、`npm --prefix web run build` | 工具重组/冲突、状态清理、真实 SSE reader、activation journal、分页、strict TypeScript |
| harness | 三个 `scripts/phase1_*test.py` | 验收矩阵、账户/秘密边界和浏览器/mock 静态合同 |

数据库命令必须显式使用 `TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`；缺变量产生的 SKIP 不是上述商业链证据。

## 3. 真实 main 与浏览器验收

`scripts/phase1_browser_qa.py` 在既有 029 ID 内增加实际操作，不新增伪造通过路径：

1. admin 通过 RouteEditor 发布下一 revision，打开历史并明确回退到较小 revision；DOM 与同一会话 API 均确认唯一 active，保存桌面/390px 截图。
2. owner 使用一次性 Key 先完成普通文本调用，再通过隔离 mock 的显式 fault 指令完成双工具 SSE；检查两个 index、完整参数、tool_calls、未执行和账务提示。
3. 两个工具响应在同一 request 上切换桌面与 390px，不重发生成；分别核查 settlement，随后进入调用记录确认已结算并撤销 Key。
4. 既有侧栏、资格证据、财务审批、下载和其他浏览器步骤保持执行，不能用新增步骤替代它们。

最终证据必须来自干净 HEAD 的同镜像 app/worker/mock，并通过 `scripts/phase1-acceptance.py` 的同提交/镜像检查。预览结果只标 PREVIEW。

## 4. 发布候选整单门禁

在最终候选提交上执行：

```bash
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' make verify-mvp
```

```bash
make check
```

```bash
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' python3 scripts/phase1-acceptance.py --execute --artifacts .local/phase2-pr3-final --browser-evidence <同提交最终浏览器证据>
```

共享 provider、route 和控制台变更不得以局部通过替代完整 029；任何 FAIL、缺失测试、无效 `-run` 或必需 SKIP 都要求修复后整单重跑。CI 必须全绿。架构 refresh 只允许集成人语义复核后执行，CI 保持只读。

## 5. 明确未覆盖

- 真实 OpenAI/DeepSeek/其他供应商的字段兼容、账号权限、usage、采购账单或生产 SLA。
- `supplier_profile_qualified`、`commercial`、`production_qualified` 和 New API/Sub2API 退役。
- Responses/Messages/Gemini/Embeddings/Rerank 新 profile，032 数据面、033 动态决策、媒体/Realtime 和正式支付商。
- 多活动实例 HA、租户级灰度、影子重复生成和自动路由切换。

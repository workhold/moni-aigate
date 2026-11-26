# 043 实施设计：路由健康归因

2026-11-26。本文把 [043 修正案](spec.md) 落到 `internal/routing`、`internal/provider`、`internal/server` 的具体状态与接口。资金链路、attempt 边界与结算完全不变；本文只描述健康动作层。

## 1. 失败分类（internal/provider）

1. `types.go` 新增 `ClassOverloaded FailureClass = "overloaded"`。`Error` 结构不变（`UpstreamStatus` 字段已存在且不外泄）。
2. `classification.go`：`case 529: class = ClassOverloaded`。≥500 不解析错误正文机器码的现状不变；其余状态码归类不变。
3. 客户可见映射不变：`Error.Code="upstream_rejected"`、`HTTPStatus` 仍按现行规则（仅 429 映射 429）。`transportReason` 对新类落默认 `provider_error`，与 `upstream` 类现状一致。

## 2. Engine 健康状态（internal/routing/routing.go）

### 2.1 状态扩展

```go
const (
    attributionWindow    = 60 * time.Second  // A1 配对窗口
    successRecencyWindow = 5 * time.Minute   // A1 渠道成功近因
    overloadedRest       = 30 * time.Second  // A3 固定短休
    ledgerLimit          = 64                // 每 key 账本上限，超出丢最旧
)

type attribEntry struct {
    class       provider.FailureClass
    status      int
    credentialID string
    at          time.Time
}

type health struct {
    state      State
    failures   int
    until      time.Time
    probes     int
    generation uint64
    successAt  time.Time      // A1：最后一次 Success
    ledger     []attribEntry  // A1：可归因失败账本（仅 upstream 类且带状态码）
}

// 凭据冷却（A2）：独立于 health 状态机，成员级准入过滤
type cooldownKey struct{ channelID, model, credentialID string }
// Engine 增加 credentialCooldown map[cooldownKey]time.Time
```

### 2.2 结局与归因

- 新增结局 `Overloaded`。`Attempt` 新增 `FinishWithFailure(outcome, detail FailureDetail)`；`Finish(outcome)` 委托它并传空 detail。`FailureDetail{Class provider.FailureClass; UpstreamStatus int}` 只在结局为 `Failure` 且 `Class == ClassUpstream && UpstreamStatus > 0` 时参与归因。
- `Success`：现逻辑外记录 `successAt = now`（不动账本）。
- `Failure`（带可归因 detail）：先按 `attributionWindow` 修剪账本；存在配对账目（同 class+status、不同 credential）且 `now - successAt ≤ successRecencyWindow` 时归因——消耗配对账目、不增加本次计数、`failures--`（下限 0）。打开状态不可被归因触碰：所有 open/隔离/短休路径都递增 `generation`，旧 generation 完成在入口即被丢弃，故归因只作用于同代的 closed/half_open 状态；半开时归因只归还令牌保持半开。未配对则记入账本（修剪+限长）并走现行爬梯。
- `Failure`（无 detail 或类不是 `upstream`）：现行爬梯，账本不动。
- `Overloaded`：`state=Open`、`until=now+overloadedRest`、`probes=0`、`generation++`；`failures`、账本、`successAt` 全部不动。管理隔离（`Quarantined`）与旧 generation 保护照常优先。
- `Cooldown(healthKey, d)` 保留（测试与运维用），网关生产路径不再调用它做 429。

### 2.3 凭据冷却（A2）

```go
func (e *Engine) CredentialCooldown(channelID, model, credentialID string, d time.Duration) // 持锁写 until
func (e *Engine) credentialCooling(channelID, model, credentialID string, now time.Time) bool
```

独立 map，不与 `health` 状态机互相覆盖；`SetQuarantined`/熔断状态变更不影响它。成员选择在 `acquirePlan` 成员循环中、健康状态检查之后追加：

```go
if reason == "" && e.credentialCooling(candidate.ChannelID, candidate.Model, candidate.CredentialID, now) {
    reason = "credential_cooldown"
}
```

成员级排除：池内尚有可用成员时 `group.explanation.FilterReason` 被现行防污染逻辑清空，binding 整体可选；全部成员冷却时 binding 以 `credential_cooldown` 排除。理由 `credential_cooldown` 必须同时登记进两处白名单——`routing.exclusionReason()`（含只读回放归一化）与 `billing.validRouteReason()`（路由决策持久校验，未登记会让 503 无候选释放整体失败），并加入 `healthBreak()`（selection.go，供 033 粘性 `break_on: health`）。

## 3. 网关动作（internal/server/gateway.go）

1. `outcome()`：`case provider.ClassOverloaded: return routing.Overloaded`；其余类不变。
2. 失败路径（HTTP 层错误与首事件错误共用）：构造 `routing.FailureDetail{Class: pe.Class, UpstreamStatus: pe.UpstreamStatus}`（无 `*provider.Error` 时为空 detail），`attempt.FinishWithFailure(...)` 替代 `attempt.Finish(...)`。流中失败、非流解码失败等无上游状态码的路径传空 detail。
3. `applyProviderHealth`：`ClassRateLimit` 分支从 `s.Routing.Cooldown(candidate.HealthKey, duration)` 改为 `s.Routing.CredentialCooldown(candidate.ChannelID, candidate.Model, candidate.CredentialID, duration)`；时长计算（`RetryAfter` → 缺省 30s → 上限 5min）不变。`ClassCredential/ClassQuota`/`BindingIsolation` 分支不变；`ClassOverloaded` 不在 `applyProviderHealth` 施加动作（短休在 Finish）。

## 4. 不变量复核清单

- `SafeToRetry`、`Consumption`、`plan.CanRetry`、attempt 预算、`CommitDownstream` 首事件边界：零改动。
- 结算、finalization、预占、审计、041 准入排除（cost/margin/procurement）：零改动。
- 对外错误码：`providerError`/`streamError`/`fail` 输出不变；新增失败子类只存在于内部分类与健康动作。
- 健康全部内存态：`successAt`、账本、凭据冷却、`Overloaded` 短休随重启清空；持久隔离仍只由凭据/binding/quota 的数据库状态承载。

## 5. 测试矩阵

单元（`internal/routing`，注入 `Now`/`RandN`）：

| 用例 | 断言 |
|---|---|
| 配对撤回 | 两次同 class+status、不同凭据、窗口内、有近期成功 → `failures` 回落，阈值内不开 |
| 无近期成功 | 同配对但 `successAt` 过期 → 照常爬梯并 open |
| 状态码不配对 | 500 与 502 不互撤 |
| 同凭据不配对 | 同凭据两次同码 → 照常爬梯 |
| 窗口过期 | 账目超 60 秒不配对 |
| 账本限长 | 超 64 条丢最旧，语义不 panic |
| 半开归因 | 探针失败被归因 → 保持 half_open、令牌归还、不重新 open，后续探针成功关闭 |
| 旧 generation | 已在途 attempt 的延迟完成在熔断打开后到达 → 整体丢弃，不撤回也不计数 |
| overloaded | 连续 N 次 529：`failures=0`、`until≈now+30s`、不进爬梯；过期后半开恢复；不覆盖隔离 |
| 凭据冷却 | 冷却只排除该成员；到期恢复；`RetryAfter` 由网关侧换算后写入；全成员冷却时 binding 理由为 `credential_cooldown` |

集成（`internal/server`，真实 PG 随机 schema + httptest 上游）：

| 用例 | 断言 |
|---|---|
| A1 归因矩阵 | 单 binding 双凭据（`CredentialMaxInflight=1` 强制两个并发请求落不同凭据）连续裸 500、此前有成功 → 熔断不打开、第三请求 200；无成功前置的同场景 → `circuit_open`/503（反例） |
| A2 作用域 | 上游按 Bearer secret 区分凭据：A 429（带 Retry-After）后 B 仍可用并成功；路由决策含 `credential_cooldown`；结算/attempt 计数不变 |
| A3 短休 | 上游恒 529：重复请求全部失败但 `failures` 为 0、状态 open 且 `OpenUntil≈+30s`；客户响应体与 032 基线一致（`upstream_rejected`、released、零收费、恰一次上游接收） |
| 回归 | 版本化熔断参数隔离、首事件边界、unknown 不重放、quota 持久隔离不触发渠道 quarantine（现有用例全部保持通过） |

相关包 `go test -race`；`make check`；最终干净提交完整 serial-v1 029。

# 028-A：异步任务第一片合同

2026-11-08；状态：**纯域合同与状态机第一片冻结，尚未接入数据库、HTTP、账务或供应商**。

本合同只冻结异步任务的身份、提交幂等、观察、取消、结果所有权和制品读取边界。它不发布任何媒体模型，不声明供应商可用，也不安装没有 writer 的 `media_tasks` 表。后续集成必须沿本合同把一个任务接到现有 request、reservation、attempt、supplier usage、唯一 settlement 和 027 来源事务；不能为任务另建钱包或第二次生成路径。

## 1. 术语和信任边界

- **Task ID** 是网关生成的非零、小写、标准 UUID；客户端永远只看到它。供应商 task ID、凭据、URL 和签名下载 URL 不外泄。
- **Owner** 是 `(tenant_id, subject_id)`。任务所有权不从当前模型、当前租户成员关系或提交者显示名称推导。
- **Submit fingerprint** 是严格输入摘要。相同 `Idempotency-Key` 必须同时匹配同一 tenant、owner、模型/profile、受理参数摘要和请求身份；这里只保存摘要，不保存 prompt、视频正文或长期密钥。
- **Provider reference** 是受限不透明引用，不是 URL。它只能由受信 adapter/观察器写入；不得进入通用 HTTP 下载器。
- **Observation** 是受信 worker、签名回调或原提交查询得到的事实。查询任务状态、查询结果和下载制品都是只读动作，不产生新的 generation attempt。
- **Result** 是原任务生成的受控制品引用、媒体类型、字节长度、SHA-256 和过期时刻；业务表不保存媒体正文。

纯包只验证这些值和状态转移，不能证明调用者真的拥有数据库中的租户权限、供应商签名或账务资格。集成层必须在调用纯包前后完成相应认证与原 request/attempt 绑定。

## 2. 身份、提交和幂等

### 2.1 严格值

Task ID、event ID、tenant ID、subject ID、grant ID 都必须是非零、小写、标准 UUID。不能接受大写、无连字符、全零或带空白的值。幂等键为 1–128 个 ASCII 字符，只允许 `[A-Za-z0-9._:-]`；原始请求摘要和制品摘要必须是 32 字节的小写十六进制表示。所有时间使用规范 UTC RFC3339Nano；状态机内部不得从系统时钟补值。

任务创建输入必须同时给出：Task ID、Owner、幂等键、请求摘要、创建时刻、提交截止、观察截止、结果截止和制品最大字节数。截止时间严格递增，制品最大字节数为正且由 profile 的资源上限约束。任务初始为 `prepared`，还没有供应商提交事实。

### 2.2 一次提交

`prepared -> submitting` 是唯一允许取得生成许可并调用供应商的转移。它只可成功一次。提交成功、提交未知和提交前失败必须分别记录；HTTP 超时、进程退出、空供应商 ID 或数据库提交未知都不能解释成 `not_sent`。

同幂等键和同完整摘要的重放只返回原任务及其当前状态，不再调用供应商；同键异摘要返回 `idempotency_conflict`。任务进入 `submitting` 后，任何普通重试都不能再次进入 `submitting`。

`submitting -> submission_unknown` 是保守终态事实（对生成动作而言），表示平台不能证明供应商是否已受理。该状态可以由后续**只读**原提交查询、签名观察或人工核对解析为已有任务状态，但永远不能获得第二次生成许可。没有供应商 task ID 且没有原提交查询能力时，必须保持该状态并转人工，不能以“未找到”重发 POST。

## 3. 状态机

### 3.1 协议状态

协议状态闭集为：

```text
prepared -> submitting -> queued -> running -> succeeded
                       \-> submission_unknown
queued/running -> cancel_requested -> cancelled | running | succeeded | failed
queued/running -> failed
submission_unknown -> queued | running | succeeded | failed | cancelled
```

`succeeded`、`failed`、`cancelled` 是协议终态。终态只能产生一次；同一事件重放是幂等读取，另一终态事件必须进入冲突，不得覆盖原终态或创建第二次结算。`submission_unknown` 不是“失败且未扣费”，也不是生成许可；`cancel_requested` 不释放原 U，直到供应商事实和用量/无消费证据完成后才由账务决定收尾。

### 3.2 独立状态轴

协议状态、观察新鲜度、取消命令和制品状态不能拼成一个含义模糊的字符串：

- 观察新鲜度：`unobserved | fresh | blocked | expired`。`blocked/expired` 只停止本地观察，不证明供应商停止，也不改变已提交事实。
- 取消命令：`none | requested | accepted | rejected`。重复取消返回同一命令状态；取消竞态由后续观察确定。
- 制品状态：`none | available | expired`。制品过期保留原 `succeeded`、原计量和财务事实。

任务状态响应必须分别返回这些轴和最后观察时刻；不能把 `submission_unknown`、`observation_blocked` 或 `observation_expired` 显示成“失败且未扣费”。

## 4. 观察事件合同

纯 parser 接受上限 16 KiB、严格 JSON、无重复键、无未知字段、无尾 JSON、无非法 UTF-8/孤立 surrogate。事件 canonical 字段固定为：

```json
{
  "version": "1",
  "event_id": "uuid",
  "task_id": "uuid",
  "kind": "submission_accepted|submission_unknown|status_queued|status_running|status_succeeded|status_failed|status_cancelled|cancel_rejected|artifact_expired",
  "sequence": "positive decimal",
  "observed_at": "UTC RFC3339Nano",
  "provider_reference": "string|null",
  "artifact": {
    "reference": "safe opaque reference",
    "media_type": "canonical media type",
    "size": "positive decimal",
    "sha256": "lowercase 64 hex",
    "expires_at": "UTC RFC3339Nano"
  },
  "failure_code": "safe code|null"
}
```

字段始终齐全；无关字段必须为 `null`。提交未知不得带 provider reference；成功必须带完整 artifact；失败必须带 failure code；其它事件不得带 artifact 或 failure code。`provider_reference`、artifact reference 和 failure code 只允许有限 ASCII 字符，不能是 URL、路径跳转或自由诊断 JSON。事件 sequence 单调递增；旧事件只读忽略或拒绝，异体同 ID 必须冲突。

事件的 canonical JSON 使用固定字段顺序和规范时间/十进制字符串，digest 使用 `moni-aigate.tasks.event.v1` 域分帧摘要。对象键输入顺序不影响解析，但 canonical 输出只有一个表示。事件观察时间是诊断元数据，不决定价格、周期预算或客户截止。

## 5. 查询、取消和结果所有权

- `status` 只读取任务快照和最后观察，不即时 poll，不创建 attempt，不改变 reservation/settlement，也不把空结果显示为健康。
- `observe` 只允许受信任务观察权限和同租户边界；它只能应用绑定原 Task ID 的事件，不能创建任务、Reserve、attempt 或生成调用。
- `cancel` 只改变取消命令状态并发出后续受控观察请求；它不改变 Task ID、不重建 request、不释放 U。供应商取消协议未明确幂等前，不允许重送取消。
- 结果读取必须同时满足原 tenant、授权 subject/租户读权限、有效下载授权、原 task ID、原 artifact reference、原 SHA-256、未过期和 profile 字节上限。首片不支持 Range、redirect、任意 URL 或断点续传；下载是原制品的只读读取。
- 结果授权必须绑定 grant ID、task/tenant/subject、artifact reference/digest、最大字节数和短期过期时刻。授权过期、制品过期或摘要不一致分别返回固定失败，不生成新制品。

普通成员只能读取自己拥有的任务；平台现行 tenant owner/billing viewer 等更高读权限由外层授权层映射为明确 capability，纯包不会猜测角色。观察 worker 的权限也不能转化为客户下载权限。

## 6. 失败、过期和唯一终结

本片至少区分：`invalid_request`、`idempotency_conflict`、`task_not_found`、`task_forbidden`、`task_submission_unknown`、`task_observation_unavailable`、`task_observation_expired`、`task_cancel_conflict`、`task_already_terminal`、`artifact_expired`、`artifact_not_ready`、`artifact_digest_mismatch`、`artifact_too_large`。不得通过 provider body、SQL 错误或状态文案泄露内部细节。

观察截止到达后只可把新鲜度置为 `expired`；恢复 worker 只能查询原提交、读取受信回调或读取原制品，不能重新 POST 生成。制品截止到达后只把制品轴置为 `expired`，保留成功和财务证据。客户原 24 小时截止、客户释放、供应商 unknown 和采购成本收尾由统一账务合同决定，本片不伪造结算结果。

## 7. 后续集成的验收边界

本片纯 Go 必须覆盖：严格 ID 与事件 parser、canonical/digest 稳定性、提交一次与同意图重放、同键异体冲突、提交未知后禁止重发、乱序/重复/异体事件、唯一终态、取消与成功竞态、观察/制品过期、租户/主体/下载 grant 隔离和制品大小/摘要边界，并运行普通测试、race 和 vet。

本片**不能**勾选 A 完成。后续必须在全新拒迁数据库中补任务持久表和 writer、request/attempt/027 来源/账务原子接缝、正式 provider submit/query/cancel/content 合同、签名回调去重、worker lease、真实模型调用、HTTP/UI、受限 APP、026 报表、部署/恢复和外部供应商验收。没有这些证据，不能声称异步视频可售、unknown submit 已生产收口或下载已完成。

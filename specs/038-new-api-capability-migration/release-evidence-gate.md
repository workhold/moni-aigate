# 038-PR2：发布资格与证据门禁物理合同

2026-11-17；状态：PR-2 实施合同。本片实现[038 总合同](spec.md) §5 的首个闭环：追加式证据、显式 test-only/commercial 发布意图、商业发布与回滚校验、只读管理 UI 和仓库来源检查。不表示任何真实供应商已取得资格。

## 1. 范围与默认边界

1. 现有 `Publish`/`PublishWithCostPolicy`、现有 HTTP 发布请求和迁移前 route revision 全部为 `test_only`。它们继续用于隔离 029/deterministic mock 商业链验证，不回填或伪造供应商资格。
2. `commercial` 只能由显式发布请求产生。缺省、null、未知值或只有证据 ID 都不能提升为 commercial。
3. PR-2 只允许 **direct channel** 的 commercial 发布。New API/Sub2API 可登记 mock/external-flow 证据，但保持 test-only；下游账号池的持续身份与变更证据另行冻结。
4. 本片不新增真实供应商资格签发流程。`supplier_profile_qualified` 的登记只表示管理员提交了绑定到具体证据的声明；真正授予资格仍属第四阶段，当前 mock/probe 不得转成该标签。
5. test-only 不是网络隔离机制。若真实部署需要禁止普通租户调用，必须另冻 verification tenant/key 或 harness-only 准入合同，不能由 release intent、loopback URL 或客户端参数推断。
6. 不改变客户钱包、计量、结算、采购成本、公开目录授权、路由候选结构或 unknown-consumption 不重放规则。

## 2. 证据身份与标签

证据精确绑定：

`(channel_id, channel_version_id, model_id, profile_version, upstream_model, variant_key)`

并额外固定 `supplier` 与 `protocol`。供应商不能从 channel 名称、URL 或 `provider=openai` 猜测。

本片持久标签：

| 标签 | 含义 | commercial 是否接受 |
| --- | --- | --- |
| `mock_verified` | deterministic/mock 合同证据 | 否 |
| `external_flow_verified` | 真实外部服务拓扑与统一商业链证据 | 否 |
| `supplier_profile_qualified` | 指定直接供应商/profile 的资格证据记录 | 是，且须满足 §4 全部条件 |

`declared` 是无证据默认状态，不写入合格标签；`production_qualified` 不在本片开放，也不能替代 supplier profile 资格。不同标签分别建记录，不由一个标签推导另一个。

每条证据必须携带：

- 不可变 ID、actor、created_at、observed_at；
- evidence commit（40 位提交）、受控不透明 evidence ref 和该证据制品的 SHA-256；
- 运行边界、usage/终态、安全重试和成本证据引用；
- cost status（`known|unknown`）和受控 open-issue codes；
- `source_mode=reference_only|vendored`、来源仓库/40 位提交、LICENSE/NOTICE 与本地修改记录引用。

`reference_only` 的修改记录必须明确表示未 vendor 源码；`vendored` 必须有实际修改记录和 source offer。证据 ref 不得是签名 URL、带凭据 URL、原始请求/响应正文或本地绝对路径。服务端只登记元数据，不读取或认证外部制品内容。

## 3. 追加迁移与权限

追加 `migrations/current_011_release_qualification.sql`，不修改已应用 SQL。

### 3.1 表与列

- `channel_profile_release_evidence`：保存 §2 的身份、标签、证据、来源、成本和未关闭问题；insert-only。
- `channel_profile_release_evidence_credentials`：保存一条 direct 证据覆盖的 `(channel_version_id, credential_id)` 集合；insert-only。
- `channel_profile_release_withdrawals`：保存证据撤回、actor、reason code 和 created_at；每个证据最多一次，insert-only。
- `route_policy_versions.release_intent`：`test_only|commercial`，默认 `test_only`；历史行保持 test-only。
- `model_bindings.release_evidence_id`：可空 FK；test-only 必须为 NULL，commercial 必须非 NULL。

证据与覆盖集合通过复合外键对齐 `channel_versions(id,channel_id)`、`model_capability_versions(id,model_id)` 和 `channel_version_credentials(channel_version_id,credential_id)`。历史 channel/version、route/binding 不原地改写。

### 3.2 数据库门禁

- 三张新证据历史表禁止 UPDATE/DELETE；APP 仅 SELECT/INSERT，projector/PUBLIC 无新增读取权。
- binding insert guard 强制 release intent 与 evidence ID 的 NULL 关系。
- deferred commercial-active guard 在 `models.active_revision` 写入/提交前复核：所有 binding 证据的完整作用域、supplier 标签、未撤回、无 open issue、已知成本、direct 拓扑及凭据覆盖。
- guard 对 test-only 不增加供应商证据要求；不能由直接 SQL 插入未合格 commercial route 后激活。
- `operations.ProvisionFor` 只在 current_011 表存在时收紧其 ACL，保持历史隔离 fixture 可运行。

## 4. 登记、发布、撤回与回滚状态机

### 4.1 登记证据

`RecordChannelProfileEvidence` 在事务内：

1. 校验精确输入、引用格式、数组去重和有界长度。
2. 显式读取输入的 channel version、model/profile 与凭据池；不隐式采用“当前版本”。
3. 校验 credential coverage 全部属于该 channel version；direct 至少一项，external 可登记 external-flow 但不获得 commercial 资格。
4. 同事务写 evidence、coverage 和 audit；任一项失败全部回滚。

新 channel version 不继承旧证据。同一作用域可有多条独立证据，不按“最新一条”自动选资格。

### 4.2 发布 route revision

现有方法固定 test-only；新增显式 commercial 方法共享同一事务实现，不建立第二条路由或资金路径。

- test-only：所有 binding 的 evidence ID 必须为空，现有 CAS、live credential、isolation 和成本规则不变。
- commercial：每个 binding 显式提交 evidence ID。服务端固定当前 channel version 后，同事务核对证据六元组、protocol、supplier 标签、direct kind、未撤回、无 open issue、cost known。
- 对 direct 每个当前未撤销凭据，必须在证据 coverage 中；`live=0` 或任一未覆盖即拒绝。多 binding 任一失败，整个 route policy、binding、active_revision 和 audit 全部不变。
- route policy 冻结 release intent，binding 冻结实际采用的 evidence ID；不得在发布后自动换用“更新证据”。

### 4.3 撤回

`WithdrawChannelProfileEvidence` 只追加 withdrawal 和 audit，不修改证据正文。撤回不修改历史 route/binding，也不影响在途请求；它阻止该证据支持的 commercial 新发布与后续重激活。

### 4.4 回滚/重激活

`ActivateRouteRevision` 读取目标 revision 已冻结的 intent：

- test-only 继续现有 live/dead route 检查；
- commercial 对每个历史 binding 重新执行 §4.2 的证据、撤回和当前 live credential coverage 校验。

activation 不接受新 intent/evidence，不提升 test-only 历史 revision。目标证据撤回、范围错配或覆盖不足时失败关闭，活动 revision 不变。

## 5. HTTP 合同

仅平台 `admin`；写接口继续经过 session、CSRF、Origin、敏感操作再认证和 recovery-mode 写门禁，读接口不需要 CSRF/reauth。

| API | 语义 |
| --- | --- |
| `POST /api/platform/release-evidence` | 登记 §2 证据与 credential coverage；严格未知/重复字段拒绝 |
| `POST /api/platform/release-evidence/{id}/withdraw` | 撤回指定证据；重复撤回返回 conflict |
| `GET /api/platform/release-evidence` | 50 条 keyset 分页，可选精确 channel_id/model_id |
| `GET /api/platform/release-evidence/{id}` | 单条不可变详情与 coverage/withdrawal/stale |
| 现有 route publish | 可选 `release_intent`，binding 可选 `release_evidence_id`；缺省 test-only |
| 现有 route activate | 输入不变，按目标 intent 重核 |

安全错误码至少包括 `release_evidence_invalid`、`release_evidence_not_found`、`release_evidence_withdrawn`、`release_qualification_required`、`release_credential_coverage`、`release_external_commercial_unsupported` 和既有 conflict/reauth/recovery 错误。错误不返回 SQL、上游正文或秘密。

管理 DTO 可返回安全 ID、作用域、标签、观察/创建时刻、commit、受控引用、digest、来源/许可证路径、open-issue codes、coverage ID 和 withdrawal/stale。不得返回 `base_url`、CIDR、secret/ciphertext、签名 URL、原始错误、模型正文或证据 payload。

`stale` 只表示证据引用的 channel/profile 已不是当前管理版本，不回写历史，也不自动使合法历史 route 失效。

## 6. 只读管理 UI

在现有 channels/catalog 行打开同一个 `ReleaseQualification` dialog，不新增公开页面或客户路由。

- 使用 exact-key TypeScript parser 和独立 list/detail epoch，隔离晚回、关闭、分页和身份切换。
- 展示来源模式、资格标签、精确范围、观察时刻、commit、受控引用、未关闭问题、凭据覆盖、withdrawal/stale。
- 区分 loading、loaded-empty、错误、forbidden、not-found、stale；错误不能伪装为空资格，旧详情不能套到新行。
- 不提供登记、撤回、发布、下载或“一键认证”按钮。
- 桌面与 390px 实际打开 channels/catalog 的证据视图并做 DOM 断言；长 ID/安全引用可换行，无整页横向溢出，秘密 canary 不出现在 DOM。

## 7. 仓库 provenance 检查

新增版本化 `third_party/provenance.json`，与现有 SOURCE.md 共同记录组件；`source_mode` 与 channel kind/integration mode 独立。

- `reference_only` 要求固定仓库/提交、参考源码路径、LICENSE/NOTICE、SOURCE 与 source offer，vendored inventory 必须为空。
- `vendored` 要求精确文件闭集、local/upstream 摘要、修改记录和 source offer；额外、缺失、漂移、symlink 或路径逃逸均失败。
- checker 改为可测试的只读 `run --root`，严格 JSON、拒绝未知字段/浮动 ref，保留现有固定镜像与 supplier_authenticated=false 检查。
- CI other、verify-current-mvp 和 029 static-build 实际执行 checker；Go 编译 `[no test files]` 不算执行证据。
- `third_party/` 和 dependency-provenance 记录进入架构内容清单。
- PASS 只表示仓库来源/引用完整性，不证明供应商质量、法律合规、完整 SBOM 或生产资格。

## 8. 验收

1. current_010→current_011 专库升级：历史 route 全 test-only、无资格回填；新表不可变、ACL/角色正确。
2. test-only 现有 catalog/server/external-relay 回归不变；commercial 无证据、mock/external、错 scope、多 binding 单项失败、覆盖不足、撤回和 external commercial 全部原子拒绝。
3. direct 全 live credential 覆盖的 supplier 证据可发布 commercial；后续撤回阻止重激活，test-only 回退不受影响。直接 SQL 未合格 commercial active 也被 deferred guard 拒绝。
4. HTTP 权限、严格输入、安全错误、分页、stale、空态和秘密 canary；公开/客户 DTO 不出现证据内部字段。
5. provenance checker 正负例与无写入测试；CI/current-mvp/029 执行器保留实际日志。
6. 前端 parser/state 测试、strict 构建和真实 main 桌面+390px DOM 验收。
7. 最终运行专用 PostgreSQL `make verify-mvp`、`make check`、CI；共享迁移的发布候选按 038 §9.2 在最终 merge commit 整单重跑 029，零必需 SKIP。

## 9. 非目标

- 真实供应商资格签发、生产可售声明或下游 New API/Sub2API commercial。
- 第二套 route decision、钱包、结算、采购成本或公开目录授权。
- 证据正文在线下载、自动供应商探测推断资格、自动修改历史 route。
- 多实例 HA、032/033、媒体/Realtime 路由和正式支付。

## 10. 029 发现的运行时修正

### 10.1 HTTP 与 worker 同任务提交竞争

`tasks.Service.Submit` 在任务已不是 prepared 时，必须使用该任务持久化的原幂等键和请求 digest 调用 `ReplaySubmit`：身份一致则返回同一任务，不调用 adapter、不新增媒体 attempt 或供应商生成；身份不一致仍返回冲突，未知 submission 仍不得重发。真实 PostgreSQL 测试必须让 worker 与 HTTP 同时提交同一任务，验证 HTTP 202、worker 无业务错误/合法无工作、恰好一个 submission_accepted 事件和一个 attempt。

该规则不是放宽重试，而是消除“worker 已完成唯一提交，HTTP 却向客户返回 503”的错误。原冻结的 unknown/no-resubmit 资金边界不变。

### 10.2 支付启动诊断

支付主进程初始化失败可以附加 `payments.SafeErrorCode` 的封闭错误身份；原始 cause、SQL、DSN、凭据、Cookie、供应商正文和内部密钥不得进入日志。未知错误固定归为 `payments_unavailable`；context 截止/取消、SQLSTATE 和网络错误只能映射为对应的固定诊断类别，不得包含原始文本。该诊断不改变启动失败关闭、实例锁、恢复或支付资金行为。

这些运行时修正由完整 029 触发，不能因局部测试或较早提交通过而免除同提交整单重跑。

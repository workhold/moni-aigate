# 034.2 角色、独立申请、付款基准与成对来源验证

日期：2026-11-14。范围只含 034.2；034.3 的 `refund_held/retired`、034.3b 币价版本、034.4 现金退款、034.5 报表展示和 034.6 约合/显示名不在本记录内。

状态：**034.2 实现与本地验收通过（2026-11-14）。** 本记录不证明真实服务器/1Panel 部署、真实供应商、正式支付、生产对账、生产高可用或后续 034 分片已完成。

## 规格纠偏

实现复核发现，早期工作曾把客户自助申请直接放入 `credit_orders.requested`，违反 034 §5.9“申请不放进 credit_orders”的明确约束。已先在规格补齐独立 `wallet_requests` 的状态机、接口、并发、故障和验收边界，再移除该路径：

- `credit_orders` 只接受 `pending/approved`，没有旧 requested 状态兼容分支。
- `wallet_requests` 使用 `open/linked/closed`；open 和 linked 均无钱包效果。
- 财务制单与同租户 `topup/open` 申请关联在同一事务；审批入账与 `linked → closed` 在同一事务。
- 每租户最多 20 个 open+linked 申请，直接 SQL 也受 guard；cash_refund 在 034.4 前不能关联。
- `financial_idempotency` 只追加 `wallet_request` 和 `wallet_request_close` 两种受控 operation，没有复用充值业务号或增加第二付款事实源。

## 已执行的专项断言

| 范围 | 实际断言 |
| --- | --- |
| 付款基准 | paid 同时保存 COIN 与 `FiatMoney`/币种；grant/unknown 法币字段为 NULL；USD 在 034.3b 前失败关闭；amount 与 bonus 分别执行 1e12 微币硬上限及溢出拒绝 |
| 成对来源 | paid+bonus 两个 receipt、两个 journal operation、主 journal 与 parent_ref；纯 grant/unknown 形态；直接 SQL 缺 bonus 在 COMMIT 前整体回滚；重复审批不重复来源 |
| 确定性 FIFO | 固定时间构造断言 `bonus=paid+1µs`；两个并发文本预占分别得到 51/0 与 49/2 的 paid/bonus 分配；释放第二张后，TTS 60 单位分配为 49 paid + 11 bonus |
| 独立申请 | 创建/重放/异内容冲突、跨租户拒绝、open 取消、财务拒绝、20 条直接 SQL 上限、同一申请并发关联只成功一次、审批关闭及不可重开 |
| 真实 APP | `CreateWalletRequest → CreateCredit(request_id) → ApproveCredit` 使用独立受限 LOGIN，不是 owner/SET ROLE；linked 前无钱包效果，审批后来源核对平衡 |
| 单人运营 | 默认 genesis、追加版本、幂等重放、完整意图冲突、expected_revision CAS、两个并发写只成功一个、审计同事务、不可 UPDATE/DELETE、受限 APP 无额外表权限 |
| HTTP | admin 在同一账号制单/审批并写 `single_operator_exception`；非 admin 财务仍不可自批；设置要求近期再认证、CSRF、二次确认；客户申请到独立审批关闭全链路通过 |
| 角色边界 | current 与离线历史 provisioning、worker/projector 权限、表/列/函数指纹、周期预算和 meter 角色矩阵均通过专项；历史只跳过不存在的 034 对象 |
| 价格回归 | `internal/pricing` 全包通过；current fixture 使用 `ApplyCurrent`，raw price 外层为 COIN，canonical 内嵌费率保持 CNY |

上述重点测试包括：

- `TestPostgresCreditFixedClockConcurrentTextAndMediaFIFO`
- `TestPostgresCreditPaidBonusGrantUnknownAndReplay`
- `TestPostgresCreditDirectSQLMissingBonusRollsBack`
- `TestPostgresWalletRequestsLifecycleIsolationAndDirectSQLLimit`
- `TestPostgresWalletRequestConcurrentLinkAndApprovalClose`
- `TestPostgresWalletSourcesApplicationSelfServiceCompletion`
- `TestPostgresWalletOperatorSettingsAppendReplayCASAndAudit`
- `TestPostgresWalletOperatorSettingsActualApplication`
- `TestHTTPWalletRequestLinkApprovalAndIsolation`
- `TestHTTPWalletOperatorSettingsAndAdminFinance`
- `TestPaymentRoles*`、`TestWalletSourcesRoles*`、`TestMeterRoleBoundary`、周期角色矩阵

## 前端与静态检查

已执行并通过：

```sh
npm --prefix web test
npm --prefix web run build
go vet ./...
```

前端覆盖六类来源摘要、现金/沙箱/不可退组合、运营设置历史与命令、申请 DTO/状态/分页、完整测试和生产构建。`go vet` 全仓无输出。

## 全仓 PostgreSQL 回归

专用库固定为 `postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`，每次均显式设置 `TEST_DATABASE_URL`。并行 `go test ./...` 曾触发共享迁移 advisory lock 的锁超时，未记为通过；改为按包串行分组，不增加超时、不删除测试、不增加 SKIP。

已完成的分组：

- `internal/billing`：全包 race 通过，287.796s；最终申请错误码与固定时钟/媒体专项另于 3.559s 通过。
- `internal/server`：全包 race 通过，438.001s。
- `internal/operations`：全包 race 通过，74.927s。
- `migrations`：全包 race 通过，67.555s。
- `internal/pricing`：全包 race 通过，10.105s。
- `internal/periodicbudget`：全包 race 通过，68.633s。
- `internal/procurementprefixschema`：全包 race 通过，3.732s。

全仓并行运行中，其余包均已通过；上述曾失败的包随后全部在串行模式完整通过。并行锁竞争和每次失败均保留在本记录中，没有把失败运行改写成通过。034.2 对应 PostgreSQL 用例均在显式专用 `TEST_DATABASE_URL` 下执行，未新增 SKIP；本片验收完成。

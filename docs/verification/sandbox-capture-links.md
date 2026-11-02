# T3 sandbox capture 直接关联核验

日期：2026-11-02。实现和范围依据 [sandbox capture 直接关联合同](../../specs/022-self-service-payments/c0-sandbox-capture-link-contract.md)。此记录只覆盖`ScanSandboxSources`窄reader；它不扩展此前manual/voucher source-link结果，也不是全来源、完整SandboxFacts或生产支付验收。

## 实现范围

`maintenancescan.VerifySandboxCaptureReceiptLinksMaintenance`要求真实ReadLease和独立`ScanSandboxSources` kind。固定reader无过滤地完整分页读取`funding_receipts`和`payment_captures`，使用lease内`current_schema()`与固定列投影，每表128行C序keyset；两表共用100000行、16MiB所选UTF-8字符串预算和最多30秒原lease期限。成功必须到真实EOF并观察Rows.Err及显式Close；预算、SQL、decode、取消、期限或行生命周期失败返回零facts。

纯core只检查sandbox receipt与capture间的直接TEXT ID引用，以及tenant、amount对wallet credit amount和currency的一致性。结果scope为`sandbox_capture_receipt_reference_v1`。它逐capture计数`UnvalidatedCaptureGraphs`与`UnvalidatedLedgerEffects`，不读取capture订单/身份/evidence/processing/review/effect图、journal、ledger、credited outbox或audit，也不验证PSP真实性。直接关联一致不等于完整capture资金图有效，不递减全来源coverage缺口。

reader只由库级维护包接入，不在普通main、HTTP、支付worker或资金writer中使用；不发行full-source proof，不授权basis、epoch或资金写入。`ScanFunding`人工核款/兑换券reader仍为独立kind和结果，不与本reader互相升级。

## 验证状态

集成人已运行并通过以下定向检查：

```sh
go test ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa
go test -race ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa
go vet ./internal/fundinglotverify ./internal/billing ./internal/maintenancescan ./scripts/maintenance-scan-qa
```

这些测试覆盖本reader的纯core、分页边界和rows生命周期模拟，以及维护result的proof绑定/脱敏。QA包的真实PostgreSQL Plan/Init用例本轮没有运行：环境未提供`C0_MAINTENANCE_DATABASE_URL`。没有以空capture表或本地fixture冒充真实capture PG正例，也没有完成受控孤儿/金额/租户不匹配的真实PG测试，扫描前后真实资金快照不变尚待验收。

`TEST_DATABASE_URL`未配置，所以本轮没有运行`make verify-mvp`。全部文档汇合后，集成人已显式refresh统一架构并通过只读架构检查；`make check`亦通过（全仓vet与前端strict构建）。上述待验项不影响已通过的定向纯Go检查，但不能据此声称真实PostgreSQL Plan/Init、整仓商业验收或生产环境通过。

## 后续验收

- [ ] 配置专用`C0_MAINTENANCE_DATABASE_URL`，执行Plan/Init真实ReadLease扫描。
- [ ] 在专用schema中建立正常capture/receipt正例，运行controlled orphan、tenant、amount、currency和重复receipt关联负例；fixture清理不触碰外部数据库对象。
- [ ] 比较扫描前后资金快照，确认维护reader没有资金写入；验证同Permit各kind绑定、同kind一次性及回滚后proof失效。
- [ ] 配置专用`TEST_DATABASE_URL`后运行`make verify-mvp`；完成后更新本记录，不将本地模拟器结果写成外部PSP验收。

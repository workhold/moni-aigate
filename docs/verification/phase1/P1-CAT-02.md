# P1-CAT-02：模型别名及六协议实际调用

- 结论：**FAIL**（本次同一提交终验，非历史证据）。
- 提交：`7afe425dbcd04f51b9a32a63b2231a1064247754`。
- 数据库：`127.0.0.1:55439/moni_test`，专用 PostgreSQL；凭据不写入证据。
- deterministic mock：是；仅为本地内测证据，不是供应商或生产认证。
- 必需 Go 测试：PASS=2，FAIL/MISSING=0，SKIP=0。

## 本次执行套件与日志

- `static-build`：**PASS**；命令见 `summary.json`，日志：docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/static-architecture.log, docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/static-procurement.log, docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/static-vet.log, docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/static-build.log。
- `frontend`：**PASS**；命令见 `summary.json`，日志：docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/frontend-test.log, docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/frontend-build.log。
- `go-race`：**FAIL**；命令见 `summary.json`，日志：docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/go-race.jsonl。
- `current-mvp`：**FAIL**；命令见 `summary.json`，日志：docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/preview-stop.log, docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/current-mvp.log。
- `mock-deployment`：**PASS**；命令见 `summary.json`，日志：docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/preview-start.log, docs/verification/phase1/evidence/7afe425dbcd04f51b9a32a63b2231a1064247754/mock-deployment.log。

## 绑定的 Go 测试

- `internal/catalog:TestPhase1ModelAliasOperationLifecycle`：PASS。
- `internal/server:TestModelAliasHTTPGatewayAuthorizationAndOriginalName`：PASS。

## 隔离浏览器

- desktop（1440px）：PASS；检查：/models: 模型与价格，无页面横向溢出或错误告警; 六个模型别名均在客户目录显示；部署 harness 已断言六协议别名实际调用；证据：P1-CAT-02/desktop-01.png。
- mobile390（390px）：PASS；检查：/models: 模型与价格，无页面横向溢出或错误告警; 六个模型别名均在客户目录显示；部署 harness 已断言六协议别名实际调用；证据：P1-CAT-02/mobile390-01.png。

## 未覆盖范围

- 本记录不证明真实供应商、真实支付商、生产凭据、生产 SLA、多实例 HA 或 032 第二阶段能力；这些均不在第一阶段本项验收范围。
- 任何必需套件失败、测试缺失/SKIP 或浏览器视口缺失都会使本项 FAIL；修复后必须整单重跑，不能只复跑本项。

# 第一阶段浏览器与部署预览（非最终验收）

日期：2026-11-15。状态：**完整 PREVIEW 通过；最终 029 同提交终验尚未执行。**

本轮基线提交为 `e87cdaf40c7981bb46acc10a0f064480359d9948`，之后仍有 DTO 与验收工具变更需要单独提交。预览不能替代最终干净提交上的 41 项结果，也不证明真实供应商、正式支付商、生产 SLA 或多实例 HA。

## 部署 harness

在专用 `colima-moni-acceptance` VM、专用 PostgreSQL `127.0.0.1:55439/moni_test` 的随机隔离 schema 中，使用 `PHASE1_MOCK_CONFIG_TRANSPORT=copy` 执行 `scripts/phase1-mock-acceptance.sh`。结果：

- `status=passed`，`requests=17`，`alias_protocols=6`，`compiled_group_price=true`；
- 六协议别名、编译价格实际计费、可信 a→b、未知消费单次接收和故障分类全部满足断言；
- `drop_after_headers=502`、`missing_usage=503`、`drop_after_events:2=200`、`error_after_usage=200`、`conflicting_usage=200`、`stall_first_event=504`。

全部为 deterministic mock 证据，不是供应商认证。

## 完整浏览器

通过 `scripts/phase1_browser_qa.py` 在独立 Chrome 中执行，证据为 PREVIEW，包含 10 个浏览器 ID，全部同时覆盖桌面与 390px：

- P1-CAT-01/02：公开模型/价格与六模型别名；
- P1-UI-01/02：客户与运营页面，Key 创建、实际 Playground 调用及撤销；
- P1-ID-02：TOTP 启用、不同 step 的 MFA 登录、密码修改与敏感再认证；
- P1-FN-02：人工核款与消费退款的独立制单和审批；
- P1-FN-03：数据库策略显式开启、券发行、独立审批与客户兑换；
- P1-RP-01：报表页面及请求级 CSV 实际下载与 SHA-256；
- P1-OP-01：预算阈值触发站内告警、事件读取、共同确认和审计；
- P1-PR-02：价格组页面及 harness 的编译价格实际计费。

所有截图均在秘密清除后保存；证据不含密码、Cookie、API Key、券码或 MFA 秘密。原始证据：[browser-evidence.json](browser-preview/browser-evidence.json)，各 ID 目录包含桌面/移动截图与观察 JSON，CSV 位于 `downloads/`。

## 相关回归

- `go test -race ./internal/server -run '^TestPricingHTTPBusinessAndCurrentDomainIsolation$'`：admin 与租户价格列表固定六位微秒。
- `go test -race ./internal/server -run '^TestCustomerRequestDetailExposesTaskReferenceFacts$'`：`confirmed_final` 数量行保留状态与原始数组形状。
- `go test -race ./internal/server -run '^(TestAdminAndTenantPricesPreserveCursorPagination|TestPricingHTTPBusinessAndCurrentDomainIsolation)$'`：51 个真实价格版本的 admin/租户游标与第二页读取均通过，时间仍为六位微秒。
- `go test -race ./internal/billing -run '^(TestHTTPCommerceIssuanceDualApprovalUniqueFundingAndOffReadback|TestVoucher.*)$'`：券工作流与时间投影。
- `go test ./internal/server -run '^(TestCommerceWireUsesVoucherTimeMarshaler|TestCommerceWirePreservesExactMoneyNullAndSixMicroseconds)$'`：反射映射器尊重 `json.Marshaler`，不再把 `VoucherTime` 展开为对象。
- `scripts/phase1-acceptance.test.sh`：执行器、浏览器协议、TOTP step、登录 pacing 和 41 ID/82 测试绑定检查全部通过。

## 未完成

最终 41 项同提交终验、生产/真实供应商、正式支付商、生产凭据与 SLA、HA、032 后续实现均未完成或不在本轮范围。最终运行仍需在浏览器/DTO PR 合入后的同一干净提交上重建环境并整单执行。

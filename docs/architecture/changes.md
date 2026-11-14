# 架构演化记录

本记录由 `scripts/check-architecture --refresh --reviewed --reason ...` 追加，与统一架构正文及机器清单一起交付。记录表示执行人已复核本次变化的架构影响；SHA-256 不能代替语义与验收复核。首次基线由集成人在并行实现稳定后建立，后续记录包含增量源文件路径。

## 2026-10-31T16:50:11Z · 481769819a46

018成本感知路由与013迁移落地：复核候选报价冻结、最高优先级比价、attempt与sending锁内校验、控制台CAS和资金收尾；建立五图源码映射与持续同步基线，019明确为未冻结设计，校准本地验收和生产边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 442 个源文件；首次建立基线，完整路径与摘要见机器清单。

## 2026-10-31T16:51:45Z · 9862f017efa7

归档018最终验收与检查通过记录：make check已验证442文件基线、vet和strict构建；复核源码及五图无结构变化，019仍未冻结未实现

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 442 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-sync.md`
- modified `docs/verification/cost-aware-routing.md`
- modified `specs/018-cost-aware-routing/acceptance.md`
- modified `specs/018-cost-aware-routing/plan.md`

</details>

## 2026-10-31T17:02:09Z · 81aef18e4558

最终构建与源码一致性收口：归档历史报价只读和分页校验补验、最终SSE资金与清理、预览摘要；复核模块时序及资金边界无结构变化，保留首轮与最终产物各自证据

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 447 个源文件；本次 9 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-sync.md`
- added `docs/verification/assets/cost-legacy-read-final-018.png`
- added `docs/verification/assets/cost-versions-final-018.png`
- modified `docs/verification/cost-aware-routing.md`
- added `docs/verification/cost-routing-018-final-browser.json`
- added `docs/verification/cost-routing-018-final-harness.json`
- added `docs/verification/cost-routing-018-preview-initial.json`
- modified `docs/verification/cost-routing-018-preview.json`

</details>

## 2026-10-31T18:05:44Z · c0289713ec9f

019共享采购账户累计预算、永久凭据归属、逐attempt预占与独立unknown恢复/证据双审、人工余额观察及控制台；已复核五图/锁序/权限/源码映射，并同步真实PG与两轮隔离QA、014预览最终产物证据；020仅冻结设计，021/022仅待审设计，生产与性能门禁仍独立

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 493 个源文件；本次 73 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/procurement-accounts.md`
- added `docs/design/public-catalog.md`
- added `docs/design/self-service-identity.md`
- added `docs/design/self-service-payments.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-019.png`
- added `docs/verification/assets/procurement-cas-019.png`
- added `docs/verification/assets/procurement-mobile-019.png`
- added `docs/verification/assets/procurement-observation-final-019.png`
- added `docs/verification/assets/procurement-settled-019.png`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/procurement-019-browser.json`
- added `docs/verification/procurement-019-final-browser.json`
- added `docs/verification/procurement-019-final-harness.json`
- added `docs/verification/procurement-019-harness.json`
- added `docs/verification/procurement-019-preview.json`
- added `docs/verification/procurement-accounts.md`
- modified `internal/billing/cost_policy.go`
- modified `internal/billing/evidence.go`
- added `internal/billing/procurement.go`
- added `internal/billing/procurement_integration_test.go`
- added `internal/billing/procurement_review_integration_test.go`
- modified `internal/billing/recovery.go`
- modified `internal/billing/route_decisions.go`
- modified `internal/catalog/cost_policy_integration_test.go`
- modified `internal/catalog/credential_pool.go`
- modified `internal/catalog/store.go`
- modified `internal/operations/operations.go`
- added `internal/operations/procurement_integration_test.go`
- added `internal/procurement/store.go`
- added `internal/procurement/types.go`
- added `internal/procurement/types_test.go`
- added `internal/routing/procurement.go`
- added `internal/routing/procurement_test.go`
- modified `internal/routing/routing.go`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/gateway.go`
- added `internal/server/procurement_handlers.go`
- added `internal/server/procurement_handlers_test.go`
- added `internal/server/procurement_integration_test.go`
- modified `internal/server/server.go`
- added `migrations/014_procurement_accounts.sql`
- modified `specs/019-procurement-accounts/acceptance.md`
- added `specs/019-procurement-accounts/contract.md`
- modified `specs/019-procurement-accounts/plan.md`
- modified `specs/019-procurement-accounts/spec.md`
- added `specs/020-public-catalog/acceptance.md`
- added `specs/020-public-catalog/contracts.md`
- added `specs/020-public-catalog/plan.md`
- added `specs/020-public-catalog/spec.md`
- added `specs/021-self-service-identity/acceptance.md`
- added `specs/021-self-service-identity/contract.md`
- added `specs/021-self-service-identity/plan.md`
- added `specs/021-self-service-identity/spec.md`
- added `specs/022-self-service-payments/acceptance.md`
- added `specs/022-self-service-payments/contract.md`
- added `specs/022-self-service-payments/plan.md`
- added `specs/022-self-service-payments/spec.md`
- modified `web/src/App.vue`
- added `web/src/components/ProcurementWriter.vue`
- modified `web/src/components/RouteAttempts.vue`
- added `web/src/procurement-state.ts`
- added `web/src/procurement.ts`
- modified `web/src/router.ts`
- added `web/src/views/ProcurementView.vue`
- added `web/tests/procurement-state.test.mjs`
- added `web/tests/procurement.test.mjs`

</details>

## 2026-10-31T18:39:50Z · 4496279f07cb

020公开目录完成：同步匿名访客/安全投影、独立准入、确定售价、公开CAS、015数据权限与浏览器/零资金/预览验收；五图渲染复核通过。021仅SMTP配置传输基础已实现，身份/outbox尚未接线；022-A补齐源码校准合同仍待冻结，未写成实现。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 522 个源文件；本次 60 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/public-catalog.md`
- modified `docs/design/self-service-identity.md`
- modified `docs/design/self-service-payments.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-020.png`
- added `docs/verification/assets/public-catalog-conflict-020.png`
- added `docs/verification/assets/public-catalog-desktop-020.png`
- added `docs/verification/assets/public-catalog-mobile-020.png`
- added `docs/verification/assets/public-catalog-private-020.png`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/public-catalog-020-browser.json`
- added `docs/verification/public-catalog-020-harness.json`
- added `docs/verification/public-catalog-020-preview.json`
- added `docs/verification/public-catalog.md`
- modified `internal/billing/reserve.go`
- added `internal/billing/retail_price_integration_test.go`
- added `internal/catalog/public_catalog.go`
- added `internal/catalog/public_catalog_integration_test.go`
- added `internal/catalog/public_catalog_test.go`
- modified `internal/config/config.go`
- added `internal/config/mail.go`
- added `internal/config/mail_test.go`
- added `internal/maildeliver/smtp.go`
- added `internal/maildeliver/smtp_test.go`
- added `internal/server/public_catalog.go`
- added `internal/server/public_catalog_admission.go`
- added `internal/server/public_catalog_integration_test.go`
- added `internal/server/public_catalog_test.go`
- modified `internal/server/queries.go`
- modified `internal/server/server.go`
- added `migrations/015_public_catalog.sql`
- modified `specs/020-public-catalog/acceptance.md`
- modified `specs/020-public-catalog/contracts.md`
- modified `specs/020-public-catalog/plan.md`
- modified `specs/020-public-catalog/spec.md`
- modified `specs/021-self-service-identity/acceptance.md`
- modified `specs/021-self-service-identity/contract.md`
- modified `specs/021-self-service-identity/plan.md`
- modified `specs/021-self-service-identity/spec.md`
- added `specs/022-self-service-payments/a-contract.md`
- modified `specs/022-self-service-payments/acceptance.md`
- modified `specs/022-self-service-payments/contract.md`
- modified `specs/022-self-service-payments/plan.md`
- modified `specs/022-self-service-payments/spec.md`
- modified `web/src/App.vue`
- added `web/src/components/ModelPublicationEditor.vue`
- added `web/src/public-catalog-state.ts`
- added `web/src/public-catalog.ts`
- modified `web/src/router.ts`
- modified `web/src/views/LoginView.vue`
- modified `web/src/views/PlatformView.vue`
- added `web/src/views/PricingView.vue`
- added `web/tests/public-catalog-state.test.mjs`
- added `web/tests/public-catalog.test.mjs`

</details>

## 2026-10-31T19:16:30Z · f53504f4da05

021自助身份实现及限定本地验收：同步公开身份链路、初始化与密码版本、加密邮件outbox和独立worker、角色/退出语义与016预览证据；022-A/A0设计冻结及023未冻结草案明确列为规划，未宣称支付或生产完成

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 567 个源文件；本次 80 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- added `cmd/moni-aigate/main_test.go`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- added `docs/design/periodic-products.md`
- modified `docs/design/self-service-identity.md`
- modified `docs/design/self-service-payments.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-021.png`
- added `docs/verification/assets/identity-disabled-021.png`
- added `docs/verification/assets/identity-mobile-021.png`
- added `docs/verification/assets/identity-password-proof-021.png`
- added `docs/verification/assets/identity-registration-proof-021.png`
- added `docs/verification/assets/identity-zero-entitlements-021.png`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/self-service-identity-021-browser.json`
- added `docs/verification/self-service-identity-021-harness.json`
- added `docs/verification/self-service-identity-021-preview.json`
- added `docs/verification/self-service-identity.md`
- modified `internal/config/mail.go`
- modified `internal/config/mail_test.go`
- added `internal/identity/mail_outbox.go`
- added `internal/identity/mail_outbox_cleanup.go`
- added `internal/identity/mail_outbox_integration_test.go`
- added `internal/identity/mail_outbox_types.go`
- added `internal/identity/self_service.go`
- added `internal/identity/self_service_integration_test.go`
- added `internal/identity/self_service_issuance_integration_test.go`
- added `internal/identity/self_service_test.go`
- modified `internal/identity/store.go`
- added `internal/mailworker/worker.go`
- added `internal/mailworker/worker_test.go`
- modified `internal/operations/credential_pool_integration_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/operations_integration_test.go`
- added `internal/operations/self_service_identity_integration_test.go`
- added `internal/server/self_service_handlers.go`
- added `internal/server/self_service_integration_test.go`
- added `internal/server/self_service_test.go`
- modified `internal/server/server.go`
- added `migrations/016_self_service_identity.sql`
- added `scripts/browser-qa/mail_fixture.go`
- added `scripts/browser-qa/mail_fixture_test.go`
- modified `scripts/browser-qa/main.go`
- modified `scripts/browser-qa/main_test.go`
- modified `specs/021-self-service-identity/acceptance.md`
- modified `specs/021-self-service-identity/contract.md`
- modified `specs/021-self-service-identity/plan.md`
- modified `specs/021-self-service-identity/spec.md`
- modified `specs/022-self-service-payments/a-contract.md`
- added `specs/022-self-service-payments/a-domain-contract.md`
- modified `specs/022-self-service-payments/acceptance.md`
- modified `specs/022-self-service-payments/contract.md`
- modified `specs/022-self-service-payments/plan.md`
- modified `specs/022-self-service-payments/spec.md`
- added `specs/023-periodic-products/acceptance.md`
- added `specs/023-periodic-products/contract.md`
- added `specs/023-periodic-products/plan.md`
- added `specs/023-periodic-products/spec.md`
- modified `web/src/App.vue`
- modified `web/src/api.ts`
- added `web/src/components/IdentityLayout.vue`
- added `web/src/identity-proof.ts`
- added `web/src/identity-public-state.ts`
- added `web/src/identity-public.ts`
- added `web/src/public-layout.ts`
- modified `web/src/router.ts`
- modified `web/src/session.ts`
- added `web/src/views/IdentityCompleteView.vue`
- added `web/src/views/IdentityRequestView.vue`
- modified `web/src/views/LoginView.vue`
- added `web/tests/identity-proof.test.mjs`
- added `web/tests/identity-public-state.test.mjs`
- added `web/tests/identity-public.test.mjs`
- modified `web/tests/public-catalog.test.mjs`
- added `web/tests/session.test.mjs`

</details>

## 2026-10-31T19:17:36Z · a7c1c0e49ab1

021收口补记最终架构六图/只读检查证据，并纳入已审查的022-A1内部接口（待A0专项出口）；正文保持023仅草案、正式SMTP/支付/生产与性能外部门禁未完成

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 567 个源文件；本次 2 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/self-service-identity.md`
- modified `specs/022-self-service-payments/a-domain-contract.md`

</details>

## 2026-10-31T19:34:59Z · 87079a5ebea3

022-A0共同入账017九表、严格历史回填与source-journal-receipt-credited双向保护、人工钱包与reports兼容和最小角色；同步021审计验收边界、023根复核、022-B及024/025未冻结草案，A1入口仍待实施

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 585 个源文件；本次 38 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/design/extension-platform.md`
- added `docs/verification/funding-vouchers.md`
- modified `docs/verification/self-service-identity.md`
- modified `internal/billing/account_batch_integration_test.go`
- added `internal/billing/funding.go`
- added `internal/billing/funding_integration_test.go`
- added `internal/billing/funding_verify.go`
- modified `internal/billing/orders.go`
- modified `internal/billing/query.go`
- modified `internal/billing/rebuild.go`
- modified `internal/billing/store.go`
- added `internal/operations/funding_integration_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/self_service_identity_integration_test.go`
- modified `internal/server/billing_handlers.go`
- added `internal/server/funding_integration_test.go`
- added `migrations/017_funding_vouchers.sql`
- modified `specs/021-self-service-identity/acceptance.md`
- modified `specs/022-self-service-payments/a-contract.md`
- modified `specs/022-self-service-payments/a-domain-contract.md`
- modified `specs/022-self-service-payments/acceptance.md`
- added `specs/022-self-service-payments/b-contract.md`
- modified `specs/022-self-service-payments/contract.md`
- modified `specs/022-self-service-payments/plan.md`
- modified `specs/022-self-service-payments/spec.md`
- added `specs/023-periodic-products/review.md`
- added `specs/024-extension-platform/acceptance.md`
- added `specs/024-extension-platform/contract.md`
- added `specs/024-extension-platform/plan.md`
- added `specs/024-extension-platform/spec.md`
- added `specs/025-production-ha/acceptance.md`
- added `specs/025-production-ha/contract.md`
- added `specs/025-production-ha/plan.md`
- added `specs/025-production-ha/spec.md`

</details>

## 2026-10-31T19:35:10Z · 96190b67b486

补记022-A0最终完整PG race：billing74.705s、operations17.656s；旧021分阶段fixture仅追加017以匹配当前provision，未改身份实现或架构结构

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 585 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/funding-vouchers.md`

</details>

## 2026-10-31T20:08:26Z · 0197b6fad96c

022-A0/A1最终限定本地验收与017预览交付：复核共同入账、双审券六命令、HMAC、未知提交、来源查询、权限/生命周期和源码证据；023-A0与022-B0-P仅冻结合同，024-A0仅DDL应用检查，M/Q及其余后续仍待冻结实现

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 621 个源文件；本次 68 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- modified `compose.yaml`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/periodic-products.md`
- modified `docs/design/self-service-payments.md`
- modified `docs/runbooks/deployment.md`
- added `docs/verification/assets/voucher-active-022.png`
- added `docs/verification/assets/voucher-mobile-022.png`
- added `docs/verification/assets/voucher-mobile-sources-022.png`
- added `docs/verification/assets/voucher-rejected-022.png`
- added `docs/verification/assets/voucher-wallet-022.png`
- added `docs/verification/funding-vouchers-022-browser.json`
- added `docs/verification/funding-vouchers-022-harness.json`
- added `docs/verification/funding-vouchers-022-preview.json`
- modified `docs/verification/funding-vouchers.md`
- added `internal/billing/voucher_commands.go`
- added `internal/billing/voucher_fault_integration_test.go`
- added `internal/billing/voucher_integration_test.go`
- added `internal/billing/voucher_query.go`
- added `internal/billing/voucher_query_integration_test.go`
- added `internal/billing/voucher_roles_integration_test.go`
- added `internal/billing/voucher_types.go`
- modified `internal/config/config.go`
- added `internal/config/voucher_test.go`
- added `internal/security/voucher.go`
- added `internal/security/voucher_test.go`
- added `internal/server/commerce_admission.go`
- added `internal/server/commerce_handlers.go`
- added `internal/server/commerce_input.go`
- added `internal/server/commerce_input_test.go`
- added `internal/server/commerce_integration_test.go`
- modified `internal/server/server.go`
- modified `scripts/browser-qa/main.go`
- modified `scripts/browser-qa/main_test.go`
- modified `specs/022-self-service-payments/a-contract.md`
- modified `specs/022-self-service-payments/a-domain-contract.md`
- modified `specs/022-self-service-payments/acceptance.md`
- added `specs/022-self-service-payments/b-domain-contract.md`
- modified `specs/022-self-service-payments/contract.md`
- modified `specs/022-self-service-payments/plan.md`
- modified `specs/022-self-service-payments/spec.md`
- added `specs/023-periodic-products/a-contract.md`
- modified `specs/023-periodic-products/acceptance.md`
- modified `specs/023-periodic-products/contract.md`
- modified `specs/023-periodic-products/plan.md`
- modified `specs/023-periodic-products/review.md`
- modified `specs/023-periodic-products/spec.md`
- added `specs/024-extension-platform/a-contract.md`
- modified `web/src/App.vue`
- added `web/src/commerce-state.ts`
- added `web/src/commerce.ts`
- added `web/src/components/CommerceCommands.vue`
- added `web/src/components/CommerceWriter.vue`
- added `web/src/components/FundingPanel.vue`
- added `web/src/components/VoucherPolicy.vue`
- modified `web/src/format.ts`
- modified `web/src/router.ts`
- modified `web/src/views/ReportsView.vue`
- added `web/src/views/VouchersView.vue`
- modified `web/src/views/WalletView.vue`
- added `web/tests/commerce-state.test.mjs`
- added `web/tests/commerce-transport.test.mjs`
- added `web/tests/commerce.test.mjs`

</details>

## 2026-10-31T20:42:48Z · 77a4fcaee595

023-A实现与限定本地验收：018精确价格域、catalog原回执与当前权限、Reserve不可变快照、客户与匿名投影及UI；真实PG分包/并发/COMMIT故障、227前端、真实main/浏览器与48资源预览核对。022-B0-P纯协议已实现；M独立SQL和024告警仅隔离设计原型，后续资金/周期/生产门禁保持未完成。已复核模块/链路/数据关系/锁/角色和HLD/LLD/验证状态，所有并行写入停稳后刷新。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 672 个源文件；本次 95 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/extension-platform.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/periodic-products.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/tenant-pricing-023-desktop.png`
- added `docs/verification/assets/tenant-pricing-023-mobile.png`
- modified `docs/verification/funding-vouchers.md`
- added `docs/verification/tenant-pricing-023-browser.json`
- added `docs/verification/tenant-pricing-023-harness.json`
- added `docs/verification/tenant-pricing-023-preview.json`
- added `docs/verification/tenant-pricing.md`
- modified `internal/billing/reserve.go`
- modified `internal/billing/retrieval_integration_test.go`
- added `internal/billing/retrieval_legacy_integration_test.go`
- modified `internal/billing/store.go`
- added `internal/billing/tenant_pricing_concurrency_integration_test.go`
- added `internal/billing/tenant_pricing_integration_test.go`
- modified `internal/billing/types.go`
- added `internal/catalog/pricing_commands.go`
- added `internal/catalog/pricing_concurrency_integration_test.go`
- added `internal/catalog/pricing_integration_test.go`
- added `internal/catalog/pricing_queries.go`
- added `internal/catalog/pricing_types.go`
- modified `internal/catalog/public_catalog.go`
- modified `internal/catalog/public_catalog_integration_test.go`
- added `internal/catalog/public_catalog_legacy_integration_test.go`
- modified `internal/catalog/store.go`
- modified `internal/operations/key_policy_integration_test.go`
- modified `internal/operations/operations.go`
- added `internal/operations/pricing_roles_integration_test.go`
- modified `internal/operations/procurement_integration_test.go`
- modified `internal/operations/self_service_identity_integration_test.go`
- added `internal/paymentproto/fuzz_test.go`
- added `internal/paymentproto/json.go`
- added `internal/paymentproto/notification.go`
- added `internal/paymentproto/protocol_test.go`
- added `internal/paymentproto/replies.go`
- added `internal/paymentproto/requests.go`
- added `internal/paymentproto/signing.go`
- added `internal/paymentproto/signing_test.go`
- added `internal/paymentproto/types.go`
- added `internal/paymentproto/values.go`
- added `internal/pricing/pricing.go`
- added `internal/pricing/pricing_integration_test.go`
- modified `internal/server/billing_handlers.go`
- added `internal/server/pricing_admission.go`
- added `internal/server/pricing_handlers.go`
- added `internal/server/pricing_input.go`
- added `internal/server/pricing_integration_test.go`
- added `internal/server/pricing_test.go`
- modified `internal/server/queries.go`
- modified `internal/server/server.go`
- added `migrations/018_tenant_pricing.sql`
- modified `specs/022-self-service-payments/acceptance.md`
- modified `specs/022-self-service-payments/b-domain-contract.md`
- added `specs/022-self-service-payments/b-mock-schema-prototype.sql`
- added `specs/022-self-service-payments/b-mock-sql-contract.md`
- added `specs/022-self-service-payments/b-receiver-schema-prototype.sql`
- added `specs/022-self-service-payments/b-sandbox-grants-prototype.sql`
- added `specs/022-self-service-payments/b-sandbox-roles-prototype.sql`
- added `specs/022-self-service-payments/b-sql-prototype-check.py`
- added `specs/022-self-service-payments/b-sql-prototype-evidence.json`
- modified `specs/022-self-service-payments/contract.md`
- modified `specs/022-self-service-payments/plan.md`
- modified `specs/022-self-service-payments/spec.md`
- modified `specs/023-periodic-products/a-contract.md`
- modified `specs/023-periodic-products/acceptance.md`
- modified `specs/023-periodic-products/contract.md`
- modified `specs/023-periodic-products/plan.md`
- modified `specs/023-periodic-products/review.md`
- modified `specs/023-periodic-products/spec.md`
- modified `specs/024-extension-platform/a-contract.md`
- modified `specs/024-extension-platform/acceptance.md`
- modified `specs/024-extension-platform/contract.md`
- modified `specs/024-extension-platform/plan.md`
- modified `specs/024-extension-platform/spec.md`
- modified `web/src/App.vue`
- added `web/src/components/PricingCommands.vue`
- added `web/src/components/PricingWriter.vue`
- added `web/src/components/TenantPricing.vue`
- added `web/src/customer-pricing.ts`
- added `web/src/pricing-state.ts`
- added `web/src/pricing.ts`
- modified `web/src/router.ts`
- modified `web/src/views/ModelsView.vue`
- modified `web/src/views/PlatformView.vue`
- added `web/src/views/PriceGroupsView.vue`
- modified `web/src/views/PricingView.vue`
- added `web/tests/pricing-state.test.mjs`
- added `web/tests/pricing-transport.test.mjs`
- added `web/tests/pricing.test.mjs`

</details>

## 2026-10-31T21:45:49Z · ecc47ee07aab

024-A站内告警完整实现与019迁移、有效角色核验、独立双槽及HTTP/UI，真实PG/浏览器/主进程故障和最终全量race/019预览证据收口；023-B精确SQL和HTTP设计仍未冻结，022-M/Q精确SQL与运行合同冻结待实施；复核模块、链路、内部ER、权限、默认开关和能力状态，保留外部门禁

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 724 个源文件；本次 83 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `Makefile`
- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- modified `compose.yaml`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/extension-platform.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/operations-alerts-024-budget.png`
- added `docs/verification/assets/operations-alerts-024-desktop.png`
- added `docs/verification/assets/operations-alerts-024-final-ui.png`
- added `docs/verification/assets/operations-alerts-024-mobile.png`
- added `docs/verification/operations-alerts-024-browser.json`
- added `docs/verification/operations-alerts-024-final-ui-harness.json`
- added `docs/verification/operations-alerts-024-final-ui.json`
- added `docs/verification/operations-alerts-024-harness.json`
- added `docs/verification/operations-alerts-024-preview.json`
- added `docs/verification/operations-alerts.md`
- added `internal/alerts/alerts_integration_test.go`
- added `internal/alerts/commands.go`
- added `internal/alerts/concurrency_integration_test.go`
- added `internal/alerts/evaluate.go`
- added `internal/alerts/fault_integration_test.go`
- added `internal/alerts/guards_integration_test.go`
- added `internal/alerts/queries.go`
- added `internal/alerts/source_integration_test.go`
- added `internal/alerts/source_sql.go`
- added `internal/alerts/store.go`
- added `internal/alerts/types.go`
- added `internal/alerts/values.go`
- added `internal/alerts/values_test.go`
- added `internal/alertworker/worker.go`
- added `internal/alertworker/worker_test.go`
- added `internal/config/alerts_test.go`
- modified `internal/config/config.go`
- added `internal/operations/alert_roles.go`
- added `internal/operations/alert_roles_integration_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/self_service_identity_integration_test.go`
- added `internal/server/alert_admission.go`
- added `internal/server/alert_handlers.go`
- added `internal/server/alert_input.go`
- added `internal/server/alert_integration_test.go`
- added `internal/server/alert_main_integration_test.go`
- added `internal/server/alert_test.go`
- modified `internal/server/main_shutdown_integration_test.go`
- modified `internal/server/server.go`
- added `migrations/019_operations_alerts.sql`
- modified `scripts/browser-qa/main.go`
- modified `scripts/browser-qa/main_test.go`
- modified `specs/022-self-service-payments/b-domain-contract.md`
- modified `specs/022-self-service-payments/b-mock-schema-prototype.sql`
- modified `specs/022-self-service-payments/b-mock-sql-contract.md`
- added `specs/022-self-service-payments/b-runtime-contract.md`
- modified `specs/022-self-service-payments/b-sql-prototype-check.py`
- modified `specs/022-self-service-payments/b-sql-prototype-evidence.json`
- modified `specs/022-self-service-payments/plan.md`
- added `specs/023-periodic-products/b-commands.sql`
- added `specs/023-periodic-products/b-contract.md`
- added `specs/023-periodic-products/b-core.sql`
- added `specs/023-periodic-products/b-http-contract.md`
- added `specs/023-periodic-products/b-permissions.sql`
- added `specs/023-periodic-products/b-verification.md`
- modified `specs/024-extension-platform/a-contract.md`
- modified `specs/024-extension-platform/acceptance.md`
- modified `specs/024-extension-platform/plan.md`
- modified `specs/024-extension-platform/spec.md`
- modified `web/src/App.vue`
- added `web/src/alerts-json.ts`
- added `web/src/alerts-state.ts`
- added `web/src/alerts-transport.ts`
- added `web/src/alerts.ts`
- added `web/src/components/AlertCommands.vue`
- added `web/src/components/AlertRuleEditor.vue`
- added `web/src/components/AlertSample.vue`
- modified `web/src/router.ts`
- added `web/src/views/AlertsView.vue`
- added `web/tests/alerts-state.test.mjs`
- added `web/tests/alerts.test.mjs`

</details>

## 2026-10-31T21:46:30Z · c6bd2869f3d4

024-A限定本地验收收口：实际全量race/vet、251项前端、真实main故障/浏览器、019排空预览与make check均通过；仅同步最终能力状态和证据，模块/链路/ER/权限无新增结构；022-M/Q独立运行合同冻结待实施、023-B保持未冻结

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 724 个源文件；本次 9 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/extension-platform.md`
- modified `docs/verification/operations-alerts.md`
- modified `specs/024-extension-platform/a-contract.md`
- modified `specs/024-extension-platform/acceptance.md`
- modified `specs/024-extension-platform/plan.md`
- modified `specs/024-extension-platform/spec.md`

</details>

## 2026-11-01T15:00:55Z · 8fcfb390d20b

汇合022-B3生命周期、023周期核验、024纯计量及025配置/邮件证据；新增C0离线descriptor与同RR账本reader，精确校准type权限，复核16图/HLD/LLD/源码状态；仅排除.vite生成缓存，正式checker/全资金接线/HA/性能仍后续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1064 个源文件；本次 407 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `.github/workflows/ci.yml`
- modified `.gitignore`
- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- added `cmd/moni-aigate/payment_startup.go`
- added `cmd/moni-aigate/payment_startup_test.go`
- added `cmd/moni-aigate/periodic_verify.go`
- added `cmd/moni-aigate/periodic_verify_integration_test.go`
- added `cmd/moni-aigate/periodic_verify_test.go`
- added `cmd/moni-payment-sandbox/main.go`
- modified `compose.yaml`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/extension-platform.md`
- modified `docs/design/new-api-roadmap.md`
- added `docs/design/provider-qualification-plan.md`
- modified `docs/runbooks/deployment.md`
- added `docs/verification/architecture-c0-reader.json`
- modified `docs/verification/architecture-sync.md`
- added `docs/verification/assets/architecture-b2-verifier.png`
- added `docs/verification/assets/architecture-b3-application.png`
- added `docs/verification/assets/architecture-c0-reader-application.png`
- added `docs/verification/assets/architecture-payment-proof.png`
- added `docs/verification/assets/payment-application-022-mobile.png`
- added `docs/verification/assets/payment-application-022-owner.png`
- added `docs/verification/assets/payment-application-022-platform.png`
- added `docs/verification/assets/payment-sandbox-architecture.png`
- added `docs/verification/assets/periodic-budget-023-desktop.png`
- added `docs/verification/assets/periodic-budget-023-mobile.png`
- added `docs/verification/compose-config-025.json`
- added `docs/verification/compose-render-025.json`
- added `docs/verification/compose-runtime-025.json`
- added `docs/verification/funding-ledger-reader.json`
- added `docs/verification/funding-ledger-reader.md`
- added `docs/verification/funding-ledger-rows.md`
- added `docs/verification/funding-lot-candidate.md`
- added `docs/verification/funding-lot-descriptor.json`
- added `docs/verification/funding-lot-descriptor.md`
- added `docs/verification/funding-lot-planners.md`
- added `docs/verification/mail-lifecycle-025.json`
- added `docs/verification/mail-lifecycle-025.md`
- added `docs/verification/metering-vectors.md`
- added `docs/verification/migration-snapshot.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/payment-application-022-browser.json`
- added `docs/verification/payment-application-022-indexeddb.json`
- added `docs/verification/payment-application-022-lifecycle.json`
- added `docs/verification/payment-application-022-process.json`
- added `docs/verification/payment-application.md`
- added `docs/verification/payment-funding.md`
- added `docs/verification/payment-refund-protocol-v2.md`
- added `docs/verification/payment-refund-v2-golden.json`
- added `docs/verification/payment-sandbox.md`
- added `docs/verification/periodic-budget-023-preview.json`
- added `docs/verification/periodic-budgets.md`
- modified `internal/billing/cost_policy.go`
- added `internal/billing/deadline_fixture_test.go`
- modified `internal/billing/evidence.go`
- modified `internal/billing/funding.go`
- added `internal/billing/funding_ledger_read.go`
- added `internal/billing/funding_ledger_read_integration_test.go`
- added `internal/billing/funding_ledger_read_test.go`
- added `internal/billing/funding_sandbox.go`
- added `internal/billing/funding_sandbox_integration_test.go`
- modified `internal/billing/funding_verify.go`
- added `internal/billing/lot_plan.go`
- added `internal/billing/lot_plan_test.go`
- added `internal/billing/periodic_budget.go`
- modified `internal/billing/process_failure_integration_test.go`
- modified `internal/billing/procurement.go`
- modified `internal/billing/procurement_integration_test.go`
- modified `internal/billing/reserve.go`
- modified `internal/billing/settle.go`
- modified `internal/billing/store.go`
- modified `internal/billing/store_test.go`
- modified `internal/billing/voucher_query.go`
- modified `internal/billing/voucher_types.go`
- modified `internal/config/config.go`
- added `internal/config/payments.go`
- added `internal/config/payments_test.go`
- added `internal/fundinglotverify/ledger.go`
- added `internal/fundinglotverify/ledger_fuzz_test.go`
- added `internal/fundinglotverify/ledger_test.go`
- added `internal/metering/amount.go`
- added `internal/metering/digest.go`
- added `internal/metering/fuzz_test.go`
- added `internal/metering/json.go`
- added `internal/metering/legacy.go`
- added `internal/metering/metering_test.go`
- added `internal/metering/parse.go`
- added `internal/metering/types.go`
- added `internal/mockpsp/config.go`
- added `internal/mockpsp/http.go`
- added `internal/mockpsp/input.go`
- added `internal/mockpsp/input_test.go`
- added `internal/mockpsp/repo_base.go`
- added `internal/mockpsp/repo_base_test.go`
- added `internal/mockpsp/repo_commands.go`
- added `internal/mockpsp/repo_controls.go`
- added `internal/mockpsp/repo_delivery.go`
- added `internal/mockpsp/repo_delivery_integration_test.go`
- added `internal/mockpsp/repo_delivery_test.go`
- added `internal/mockpsp/repo_events.go`
- added `internal/mockpsp/repo_events_test.go`
- added `internal/mockpsp/repo_faults.go`
- added `internal/mockpsp/repo_integration_test.go`
- added `internal/mockpsp/repo_locks.go`
- added `internal/mockpsp/repo_locks_test.go`
- added `internal/mockpsp/repo_observations.go`
- added `internal/mockpsp/repo_observations_test.go`
- added `internal/mockpsp/repo_queries.go`
- added `internal/mockpsp/runner.go`
- added `internal/mockpsp/runner_integration_test.go`
- added `internal/mockpsp/server.go`
- added `internal/mockpsp/types.go`
- added `internal/mockpsp/types_test.go`
- modified `internal/operations/key_policy_integration_test.go`
- added `internal/operations/metadata_fixture_test.go`
- modified `internal/operations/operations.go`
- added `internal/operations/payment_attestation_test.go`
- added `internal/operations/payment_roles.go`
- added `internal/operations/payment_roles_functions.go`
- added `internal/operations/payment_roles_test.go`
- added `internal/operations/periodic_roles.go`
- added `internal/operations/periodic_roles_test.go`
- modified `internal/operations/procurement_integration_test.go`
- added `internal/paymentreceiver/http.go`
- added `internal/paymentreceiver/http_test.go`
- added `internal/paymentreceiver/repository.go`
- added `internal/paymentreceiver/repository_integration_test.go`
- added `internal/paymentreceiver/server.go`
- added `internal/paymentreceiver/types.go`
- added `internal/paymentreceiver/types_test.go`
- added `internal/paymentrefundproto/capture.go`
- added `internal/paymentrefundproto/capture_test.go`
- added `internal/paymentrefundproto/codec.go`
- added `internal/paymentrefundproto/facts.go`
- added `internal/paymentrefundproto/fuzz_test.go`
- added `internal/paymentrefundproto/golden_test.go`
- added `internal/paymentrefundproto/protocol_test.go`
- added `internal/paymentrefundproto/replies.go`
- added `internal/paymentrefundproto/requests.go`
- added `internal/paymentrefundproto/signing.go`
- added `internal/paymentrefundproto/signing_test.go`
- added `internal/paymentrefundproto/types.go`
- added `internal/paymentrefundproto/values.go`
- added `internal/paymentruntime/catalog.go`
- added `internal/paymentruntime/dsn.go`
- added `internal/paymentruntime/files.go`
- added `internal/paymentruntime/json.go`
- added `internal/paymentruntime/load.go`
- added `internal/paymentruntime/load_test.go`
- added `internal/paymentruntime/open.go`
- added `internal/paymentruntime/ownership.go`
- added `internal/paymentruntime/runtime_integration_test.go`
- added `internal/paymentruntime/state.go`
- added `internal/paymentruntime/types.go`
- added `internal/payments/assembly.go`
- added `internal/payments/assembly_test.go`
- added `internal/payments/processor_adapter.go`
- added `internal/payments/processor_adapter_test.go`
- added `internal/payments/processor_claim.go`
- added `internal/payments/processor_evidence.go`
- added `internal/payments/processor_expiry_test.go`
- added `internal/payments/processor_mutation.go`
- added `internal/payments/processor_query.go`
- added `internal/payments/processor_query_safety_test.go`
- added `internal/payments/processor_safety_test.go`
- added `internal/payments/processor_test.go`
- added `internal/payments/processor_types.go`
- added `internal/payments/runner.go`
- added `internal/payments/runner_test.go`
- added `internal/payments/runtime_catalog.go`
- added `internal/payments/runtime_check.go`
- added `internal/payments/runtime_codec.go`
- added `internal/payments/runtime_dsn.go`
- added `internal/payments/runtime_files.go`
- added `internal/payments/runtime_integration_test.go`
- added `internal/payments/runtime_load.go`
- added `internal/payments/runtime_test.go`
- added `internal/payments/runtime_transport.go`
- added `internal/payments/runtime_types.go`
- added `internal/payments/service_base.go`
- added `internal/payments/service_commands.go`
- added `internal/payments/service_fault_test.go`
- added `internal/payments/service_fixture_test.go`
- added `internal/payments/service_funding_read_test.go`
- added `internal/payments/service_notification.go`
- added `internal/payments/service_query.go`
- added `internal/payments/service_test.go`
- added `internal/payments/service_types.go`
- added `internal/paymentsandboxqa/application.go`
- added `internal/paymentsandboxqa/application_funding.go`
- added `internal/paymentsandboxqa/application_manifest.go`
- added `internal/paymentsandboxqa/application_test.go`
- added `internal/paymentsandboxqa/application_users.go`
- added `internal/paymentsandboxqa/application_wire.go`
- added `internal/paymentsandboxqa/environment.go`
- added `internal/paymentsandboxqa/environment_test.go`
- added `internal/paymentsandboxqa/funding.go`
- added `internal/paymentsandboxqa/install.go`
- added `internal/paymentsandboxqa/manifest.go`
- added `internal/paymentsandboxschema/mock_inventory.json`
- added `internal/paymentsandboxschema/mock_v1.sql`
- added `internal/paymentsandboxschema/receiver_inventory.json`
- added `internal/paymentsandboxschema/receiver_v1.sql`
- added `internal/paymentsandboxschema/schema.go`
- added `internal/paymentsandboxschema/schema_test.go`
- added `internal/periodicbudget/billing_dispatch_integration_test.go`
- added `internal/periodicbudget/billing_integration_test.go`
- added `internal/periodicbudget/billing_procurement_integration_test.go`
- added `internal/periodicbudget/billing_recovery_integration_test.go`
- added `internal/periodicbudget/commander.go`
- added `internal/periodicbudget/commander_auth.go`
- added `internal/periodicbudget/commander_configure.go`
- added `internal/periodicbudget/commander_fault_test.go`
- added `internal/periodicbudget/commander_integration_test.go`
- added `internal/periodicbudget/commander_limits_test.go`
- added `internal/periodicbudget/commander_receipt.go`
- added `internal/periodicbudget/cursor.go`
- added `internal/periodicbudget/cursor_test.go`
- added `internal/periodicbudget/fingerprint.go`
- added `internal/periodicbudget/fingerprint_test.go`
- added `internal/periodicbudget/input.go`
- added `internal/periodicbudget/input_test.go`
- added `internal/periodicbudget/reader.go`
- added `internal/periodicbudget/reader_auth.go`
- added `internal/periodicbudget/reader_integration_test.go`
- added `internal/periodicbudget/reader_json.go`
- added `internal/periodicbudget/reader_operations.go`
- added `internal/periodicbudget/reader_pages.go`
- added `internal/periodicbudget/reader_test.go`
- added `internal/periodicbudget/tx_customer.go`
- added `internal/periodicbudget/tx_customer_test.go`
- added `internal/periodicbudget/tx_dispatch.go`
- added `internal/periodicbudget/tx_effects.go`
- added `internal/periodicbudget/tx_fixture_test.go`
- added `internal/periodicbudget/tx_identity_integration_test.go`
- added `internal/periodicbudget/tx_locks.go`
- added `internal/periodicbudget/tx_sources.go`
- added `internal/periodicbudget/tx_supplier.go`
- added `internal/periodicbudget/tx_supplier_test.go`
- added `internal/periodicbudget/tx_types.go`
- added `internal/periodicbudget/tx_unit_test.go`
- added `internal/periodicbudget/types.go`
- added `internal/periodicbudget/types_test.go`
- added `internal/periodicbudget/values.go`
- added `internal/periodicbudget/values_test.go`
- added `internal/periodicbudget/verifier.go`
- added `internal/periodicbudget/verifier_configuration.go`
- added `internal/periodicbudget/verifier_events.go`
- added `internal/periodicbudget/verifier_integration_test.go`
- added `internal/periodicbudget/verifier_operations.go`
- added `internal/periodicbudget/verifier_projections.go`
- added `internal/periodicbudget/verifier_sources.go`
- added `internal/periodicbudget/verifier_test.go`
- modified `internal/pricing/pricing_integration_test.go`
- added `internal/qa/c0candidate/candidate_test.go`
- added `internal/qa/c0candidate/fixture_test.go`
- added `internal/qa/c0candidate/legacy_fixture_test.go`
- added `internal/qa/c0candidate/wallet_fixture_test.go`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/commerce_handlers.go`
- added `internal/server/payment_admission.go`
- added `internal/server/payment_handlers.go`
- added `internal/server/payment_handlers_test.go`
- added `internal/server/payment_input.go`
- added `internal/server/periodic_budget_admission.go`
- added `internal/server/periodic_budget_handlers.go`
- added `internal/server/periodic_budget_input.go`
- added `internal/server/periodic_budget_integration_test.go`
- added `internal/server/periodic_budget_test.go`
- modified `internal/server/server.go`
- added `internal/testsupport/funding019/query.go`
- added `internal/testsupport/funding019/verify.go`
- added `internal/testsupport/funding019/verify_test.go`
- added `migrations/020_periodic_budgets.sql`
- added `migrations/021_payment_sandbox.sql`
- added `migrations/022_payment_runtime_attestation.sql`
- modified `migrations/embed.go`
- added `migrations/embed_test.go`
- added `migrations/verify_readonly.go`
- added `migrations/verify_readonly_test.go`
- added `migrations/verify_snapshot.go`
- added `migrations/verify_snapshot_test.go`
- added `scripts/browser-qa/lifecycle_seams.go`
- added `scripts/browser-qa/mail_barrier.go`
- added `scripts/browser-qa/mail_barrier_test.go`
- modified `scripts/browser-qa/mail_fixture.go`
- added `scripts/browser-qa/mail_lifecycle_test.go`
- modified `scripts/browser-qa/main.go`
- modified `scripts/check-architecture/main.go`
- modified `scripts/check-architecture/main_test.go`
- added `scripts/funding-lot-descriptor/canonical.go`
- added `scripts/funding-lot-descriptor/catalog.go`
- added `scripts/funding-lot-descriptor/dto.go`
- added `scripts/funding-lot-descriptor/inputs.go`
- added `scripts/funding-lot-descriptor/install.go`
- added `scripts/funding-lot-descriptor/main.go`
- added `scripts/funding-lot-descriptor/main_test.go`
- added `scripts/payment-application-qa/README.md`
- added `scripts/payment-application-qa/exercise.go`
- added `scripts/payment-application-qa/exercise_lifecycle.go`
- added `scripts/payment-application-qa/exercise_lifecycle_test.go`
- added `scripts/payment-application-qa/http.go`
- added `scripts/payment-application-qa/main.go`
- added `scripts/payment-application-qa/main_test.go`
- added `scripts/payment-application-qa/process.go`
- added `scripts/payment-sandbox-qa/http.go`
- added `scripts/payment-sandbox-qa/long.go`
- added `scripts/payment-sandbox-qa/main.go`
- added `scripts/payment-sandbox-qa/main_test.go`
- added `scripts/payment-sandbox-qa/process.go`
- added `scripts/payment-sandbox-qa/short.go`
- added `scripts/verification/payment-refund-v2-golden.py`
- modified `specs/021-self-service-identity/contract.md`
- modified `specs/022-self-service-payments/acceptance.md`
- added `specs/022-self-service-payments/b-processor-contract.md`
- modified `specs/022-self-service-payments/b-runtime-contract.md`
- added `specs/022-self-service-payments/b1-contract.md`
- added `specs/022-self-service-payments/b1-core.sql`
- added `specs/022-self-service-payments/b1-permissions.sql`
- added `specs/022-self-service-payments/b1-verification.md`
- added `specs/022-self-service-payments/b2-funding-contract.md`
- added `specs/022-self-service-payments/b3-application-qa-contract.md`
- added `specs/022-self-service-payments/b3-http-ui-contract.md`
- added `specs/022-self-service-payments/b3-runtime-contract.md`
- added `specs/022-self-service-payments/b3-web-contract.md`
- added `specs/022-self-service-payments/c0-candidate-regression-contract.md`
- added `specs/022-self-service-payments/c0-core-prototype.sql`
- added `specs/022-self-service-payments/c0-ledger-reader-contract.md`
- added `specs/022-self-service-payments/c0-lot-contract.md`
- added `specs/022-self-service-payments/c0-maintenance-contract.md`
- added `specs/022-self-service-payments/c0-permissions-prototype.sql`
- added `specs/022-self-service-payments/c0-permit-contract.md`
- added `specs/022-self-service-payments/c0-production-permissions.sql`
- added `specs/022-self-service-payments/c0-production-schema.sql`
- added `specs/022-self-service-payments/c0-runtime-helper-contract.md`
- added `specs/022-self-service-payments/c0-schema-check-contract.md`
- added `specs/022-self-service-payments/c0-sequence-contract.md`
- added `specs/022-self-service-payments/c0-snapshot-contract.md`
- added `specs/022-self-service-payments/c0-verifier-contract.md`
- added `specs/022-self-service-payments/c1-protocol-v2-contract.md`
- added `specs/022-self-service-payments/c1-refund-contract.md`
- added `specs/022-self-service-payments/c2-dispute-contract.md`
- added `specs/022-self-service-payments/c3-cross-recovery-contract.md`
- modified `specs/022-self-service-payments/contract.md`
- modified `specs/022-self-service-payments/plan.md`
- modified `specs/022-self-service-payments/spec.md`
- added `specs/023-periodic-products/b-commander-contract.md`
- modified `specs/023-periodic-products/b-contract.md`
- modified `specs/023-periodic-products/b-core.sql`
- modified `specs/023-periodic-products/b-http-contract.md`
- added `specs/023-periodic-products/b-integration-contract.md`
- added `specs/023-periodic-products/b-reader-contract.md`
- added `specs/023-periodic-products/b-tx-contract.md`
- modified `specs/023-periodic-products/b-verification.md`
- added `specs/023-periodic-products/b-verifier-contract.md`
- added `specs/023-periodic-products/c-contract.md`
- modified `specs/023-periodic-products/plan.md`
- added `specs/024-extension-platform/b0-metering-contract.md`
- added `specs/024-extension-platform/b0-pure-contract.md`
- modified `specs/024-extension-platform/plan.md`
- added `specs/025-production-ha/a0-compose-config-contract.md`
- added `specs/025-production-ha/a1-mail-process-contract.md`
- modified `specs/025-production-ha/acceptance.md`
- added `specs/025-production-ha/b0-shared-execution-contract.md`
- modified `specs/025-production-ha/contract.md`
- added `specs/025-production-ha/migration-prefix-contract.md`
- modified `specs/025-production-ha/plan.md`
- modified `specs/025-production-ha/spec.md`
- modified `web/src/App.vue`
- modified `web/src/commerce.ts`
- modified `web/src/components/FundingPanel.vue`
- added `web/src/components/PaymentsPage.vue`
- added `web/src/components/PaymentsPanel.vue`
- added `web/src/components/PeriodicBudgetPanel.vue`
- added `web/src/components/PeriodicBudgetWriter.vue`
- modified `web/src/format.ts`
- modified `web/src/main.ts`
- added `web/src/payments-journal.ts`
- added `web/src/payments-json.ts`
- added `web/src/payments-state.ts`
- added `web/src/payments-transport.ts`
- added `web/src/payments.ts`
- added `web/src/periodic-budget-journal.ts`
- added `web/src/periodic-budget-json.ts`
- added `web/src/periodic-budget-state.ts`
- added `web/src/periodic-budget-transport.ts`
- added `web/src/periodic-budget.ts`
- modified `web/src/router.ts`
- modified `web/src/views/KeysView.vue`
- added `web/src/views/PeriodicBudgetView.vue`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/ProcurementView.vue`
- modified `web/src/views/WalletView.vue`
- modified `web/tests/commerce.test.mjs`
- added `web/tests/payments-indexeddb.browser.mjs`
- added `web/tests/payments-state.test.mjs`
- added `web/tests/payments-transport.test.mjs`
- added `web/tests/payments.test.mjs`
- added `web/tests/periodic-budget-indexeddb.browser.mjs`
- added `web/tests/periodic-budget-state.test.mjs`
- added `web/tests/periodic-budget-transport.test.mjs`
- added `web/tests/periodic-budget.test.mjs`

</details>

## 2026-11-01T15:09:21Z · 25a8c9e3361e

补记最终make verify-mvp通过与36包race/286前端证据，更新C0/reader父合同和交付状态；源码不变，16图/类型权限/源码链接复核完成，正式checker及全部后续阶段继续保留

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1065 个源文件；本次 6 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-sync.md`
- added `docs/verification/c0-integration.json`
- modified `docs/verification/mvp-status.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T17:04:48Z · 529d53dc048b

022-C0正式静态catalog叶子及两套固定profile expected完成；复核五ordinary/完整sandbox、同RR opaque证明、类型I/O与完整权限边界、SQL等值优化和五秒预算；同步HLD/LLD/SDD/MVP及17图证据，维护Permit/epoch/全资金链仍未接线

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1114 个源文件；本次 57 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `docs/architecture/funding-lot-catalog.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-c0-catalog.json`
- modified `docs/verification/architecture-sync.md`
- added `docs/verification/assets/architecture-c0-catalog-application.png`
- added `docs/verification/assets/architecture-c0-catalog-detail.png`
- added `docs/verification/funding-lot-catalog-collector.json`
- added `docs/verification/funding-lot-catalog-collector.md`
- added `docs/verification/funding-lot-catalog-peer-template-positions.json`
- added `docs/verification/funding-lot-catalog-peers.md`
- added `docs/verification/funding-lot-catalog.json`
- added `docs/verification/funding-lot-catalog.md`
- added `docs/verification/funding-lot-variants.json`
- added `docs/verification/funding-lot-variants.md`
- modified `docs/verification/mvp-status.md`
- added `internal/fundinglotcatalog/canonical.go`
- added `internal/fundinglotcatalog/collector.go`
- added `internal/fundinglotcatalog/collector_bounds.go`
- added `internal/fundinglotcatalog/collector_bounds_test.go`
- added `internal/fundinglotcatalog/collector_builtins.go`
- added `internal/fundinglotcatalog/collector_decode.go`
- added `internal/fundinglotcatalog/collector_normal.go`
- added `internal/fundinglotcatalog/collector_preflight.go`
- added `internal/fundinglotcatalog/collector_preflight_test.go`
- added `internal/fundinglotcatalog/collector_privilege_plan_test.go`
- added `internal/fundinglotcatalog/collector_pure_test.go`
- added `internal/fundinglotcatalog/collector_queries.go`
- added `internal/fundinglotcatalog/collector_query_plan_test.go`
- added `internal/fundinglotcatalog/collector_resolve.go`
- added `internal/fundinglotcatalog/descriptor.go`
- added `internal/fundinglotcatalog/descriptor_compare.go`
- added `internal/fundinglotcatalog/descriptor_compare_test.go`
- added `internal/fundinglotcatalog/descriptor_fields.go`
- added `internal/fundinglotcatalog/descriptor_types.go`
- added `internal/fundinglotcatalog/descriptor_validate.go`
- added `internal/fundinglotcatalog/expected.go`
- added `internal/fundinglotcatalog/expected/ordinary-v1.json`
- added `internal/fundinglotcatalog/expected/sandbox-v1.json`
- added `internal/fundinglotcatalog/expected_embedded.go`
- added `internal/fundinglotcatalog/identity.go`
- added `internal/fundinglotcatalog/peer_capture_test.go`
- added `internal/fundinglotcatalog/peer_fixture_test.go`
- added `internal/fundinglotcatalog/peer_timing_test.go`
- added `internal/fundinglotcatalog/peers.go`
- added `internal/fundinglotcatalog/proof.go`
- added `internal/fundinglotcatalog/proof_integration_test.go`
- added `internal/fundinglotcatalog/proof_test.go`
- added `internal/fundinglotcatalog/types.go`
- added `internal/fundinglotcatalog/variants_fixture_test.go`
- added `specs/022-self-service-payments/c0-ordinary-permissions.sql`
- added `specs/022-self-service-payments/c0-ordinary-public-contract.md`
- modified `specs/022-self-service-payments/c0-permit-contract.md`
- added `specs/022-self-service-payments/c0-sandbox-catalog-contract.md`
- modified `specs/022-self-service-payments/c0-schema-check-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T17:14:31Z · 648d1ff60c77

022-C0最终整leaf真实PG race217.140秒及make check/verify-mvp340.981秒通过；源码未改，补齐两次失败与最终日志/SHA、清理审计、阶段状态和17图Runtime前置角色顺序证据，复核main/Permit/epoch/全资金链及PG生产认证边界仍未开放

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1114 个源文件；本次 9 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/architecture/funding-lot-catalog.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-c0-catalog.json`
- modified `docs/verification/architecture-sync.md`
- modified `docs/verification/assets/architecture-c0-catalog-detail.png`
- modified `docs/verification/funding-lot-catalog.json`
- modified `docs/verification/funding-lot-catalog.md`
- modified `docs/verification/mvp-status.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T17:35:47Z · 64e465981e80

022-C0-T0新增独立维护Gate、专用连接身份预检与固定双session锁/unknown物理清理，已复核无Snapshot/Permit/main或资金授权；同步模块/阶段图、细化架构、HLD/LLD与专项证据；GitHub README和通用部署入口整理、忽略构建产物，无线上行为或迁移变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1124 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.gitignore`
- modified `README.md`
- added `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-maintenance-gate.json`
- added `docs/verification/assets/architecture-maintenance-gate-application.png`
- added `docs/verification/assets/architecture-maintenance-gate-detail.png`
- added `docs/verification/maintenance-gate.json`
- added `docs/verification/maintenance-gate.md`
- modified `docs/verification/mvp-status.md`
- added `internal/maintenancetrust/gate.go`
- added `internal/maintenancetrust/gate_integration_test.go`
- added `internal/maintenancetrust/gate_test.go`
- added `internal/maintenancetrust/identity.go`
- modified `specs/022-self-service-payments/c0-permit-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T17:50:19Z · 22e62acdf9ff

汇合T0最终真实PG/race与代码审阅、GitHub README/通用部署入口和19图渲染；B0按冻结§10.3仅校准网络槽测试与独立多连接Prepare竞争测试，已复核生产pool/调度/权限/资金/预算均无变化；保留首轮整仓失败并准备最终复验

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1126 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/architecture-maintenance-gate.json`
- added `docs/verification/github-initial-delivery.json`
- added `docs/verification/github-initial-delivery.md`
- modified `internal/mockpsp/repo_delivery_integration_test.go`
- modified `internal/mockpsp/runner_integration_test.go`
- modified `specs/022-self-service-payments/b-runtime-contract.md`

</details>

## 2026-11-01T17:57:17Z · 93d6c0a4821e

最终集成收口：T0专用Gate真实PG/race53场景通过，B0仅测试oracle修正与多连接竞争专项通过；根make check及verify-mvp全通过，补齐可追溯原失败/最终证据和交付状态。已语义复核19图、模块/数据/权限/资金边界及README部署入口；本轮不接main、不改生产SQL/资金/预算，后续阶段保持未完成

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1126 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/github-initial-delivery.json`
- modified `docs/verification/github-initial-delivery.md`
- modified `docs/verification/mvp-status.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T18:43:06Z · 164d96c5a823

根汇合T1私有单次RR、ONLY125首次快照、真实catalog stage、busy/取消/提交未知及115.412秒真实PG race；同步T2前驱候选和HLD/LLD/细化图；修订GitHub临时loopback测试profile、完整独立分区和跨平台测试观察，生产预算/权限保持，未开放Permit或资金接线

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1142 个源文件；本次 29 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-maintenance-snapshot.json`
- added `docs/verification/assets/architecture-maintenance-snapshot-application.png`
- added `docs/verification/assets/architecture-maintenance-snapshot-detail.png`
- added `docs/verification/ci-profile-contract.md`
- added `docs/verification/github-ci-profile.json`
- added `docs/verification/github-ci-profile.md`
- added `docs/verification/maintenance-snapshot.json`
- added `docs/verification/maintenance-snapshot.md`
- modified `internal/billing/funding_ledger_read_integration_test.go`
- modified `internal/maintenancetrust/gate.go`
- added `internal/maintenancetrust/snapshot.go`
- added `internal/maintenancetrust/snapshot_fixture_test.go`
- added `internal/maintenancetrust/snapshot_integration_test.go`
- added `internal/maintenancetrust/snapshot_tables.go`
- added `internal/maintenancetrust/snapshot_tables_test.go`
- added `internal/maintenancetrust/snapshot_test.go`
- modified `internal/operations/instance_test.go`
- modified `internal/operations/procurement_integration_test.go`
- modified `specs/022-self-service-payments/c0-maintenance-contract.md`
- modified `specs/022-self-service-payments/c0-permit-contract.md`
- added `specs/022-self-service-payments/c0-predecessor-contract.md`
- added `specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T18:52:38Z · b8d729e70236

汇合T1私有单次RR/ONLY125首次快照与真实PG专项、最终整仓证据、CI隔离及断言修订、README和19图；已复核模块/时序/数据/权限/能力边界，T2及资金接线仍后续，新远端CI待实际运行

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1144 个源文件；本次 7 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-maintenance-snapshot.json`
- added `docs/verification/maintenance-snapshot-integration.json`
- added `docs/verification/maintenance-snapshot-integration.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T18:54:09Z · 622958485758

最终交付复核T1私有RR/ONLY125首次快照、真实PG与整仓证据、四分区CI修订及19图；README和开发验证已对齐固定专库与测试身份。模块/数据/权限/时序边界一致，T2资金接线和新远端CI验收仍后续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1144 个源文件；本次 4 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/architecture-maintenance-snapshot.json`
- modified `docs/verification/maintenance-snapshot-integration.md`

</details>

## 2026-11-01T19:28:43Z · b222a0e489b3

第二轮真实GitHub失败证据及CI修订：完整race顶层名单互斥分片、独立runner/PG、显式Shanghai时区与ready核验；已复核原始M/Q Open隔离复现、19图、HLD/LLD和测试部署边界。产品SQL/权限/资金/期限均不变，新远端实际结果另验

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1146 个源文件；本次 12 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/architecture-maintenance-snapshot.json`
- modified `docs/verification/ci-profile-contract.md`
- modified `docs/verification/github-ci-profile.json`
- modified `docs/verification/github-ci-profile.md`
- added `docs/verification/github-ci-second-run.json`
- added `docs/verification/github-ci-second-run.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T20:21:33Z · a866a4e98917

022-C0-T2汇合真实维护授权、manifest与身份叶子、完整前驱和Init账户锁；根与独立审阅已复核原Snapshot共态撤销、公开与私有PG证据层级、源码依赖图及README部署边界，T3全来源/basis/epoch继续后续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1180 个源文件；本次 54 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/funding-lot-catalog.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-maintenance-authority.json`
- added `docs/verification/assets/architecture-maintenance-authority-application.png`
- added `docs/verification/assets/architecture-maintenance-authority-detail.png`
- added `docs/verification/github-ci-third-run.json`
- added `docs/verification/github-ci-third-run.md`
- modified `docs/verification/implementation.md`
- added `docs/verification/maintenance-authority-integration.md`
- added `docs/verification/maintenance-authority.json`
- added `docs/verification/maintenance-authority.md`
- added `internal/maintenancetrust/authority_accounts.go`
- added `internal/maintenancetrust/authority_accounts_test.go`
- added `internal/maintenancetrust/authority_budget_integration_test.go`
- added `internal/maintenancetrust/authority_identity.go`
- added `internal/maintenancetrust/authority_identity_test.go`
- added `internal/maintenancetrust/authority_manifest.go`
- added `internal/maintenancetrust/authority_manifest_test.go`
- added `internal/maintenancetrust/authority_profiles_integration_test.go`
- added `internal/maintenancetrust/authority_sandbox_fixture_test.go`
- added `internal/maintenancetrust/authority_sandbox_integration_test.go`
- modified `internal/maintenancetrust/gate.go`
- added `internal/maintenancetrust/permit.go`
- added `internal/maintenancetrust/permit_integration_test.go`
- added `internal/maintenancetrust/permit_test.go`
- added `internal/maintenancetrust/predecessor.go`
- added `internal/maintenancetrust/predecessor_codec.go`
- added `internal/maintenancetrust/predecessor_codec_test.go`
- added `internal/maintenancetrust/predecessor_postgres_test.go`
- added `internal/maintenancetrust/predecessor_rows.go`
- added `internal/maintenancetrust/predecessor_rows_test.go`
- added `internal/maintenancetrust/predecessor_sql.go`
- added `internal/maintenancetrust/predecessor_sql_test.go`
- modified `internal/maintenancetrust/snapshot.go`
- modified `internal/maintenancetrust/snapshot_fixture_test.go`
- added `internal/maintenancetrust/testdata/predecessor_v1.json`
- added `internal/maintenancetrust/testdata/predecessor_v1.sha256`
- added `internal/payments/runtime_predecessor_golden_test.go`
- added `internal/paymentsandboxqa/application_predecessor_golden_test.go`
- added `specs/022-self-service-payments/c0-authority-contract.md`
- modified `specs/022-self-service-payments/c0-maintenance-contract.md`
- modified `specs/022-self-service-payments/c0-permit-contract.md`
- modified `specs/022-self-service-payments/c0-predecessor-contract.md`
- modified `specs/022-self-service-payments/c0-schema-check-contract.md`
- modified `specs/022-self-service-payments/c0-sequence-contract.md`
- modified `specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T20:28:04Z · 6dbccf9ebe60

T2最终专库race与整仓check/verify-mvp均通过；独立复核修正HLD账户锁在Attest内且Permit发行前的措辞，收口分层验证、19图、README部署和SDD完成状态，测试后无源码变动，生产和T3边界保留

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1181 个源文件；本次 4 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- added `docs/verification/maintenance-authority-integration.json`
- modified `docs/verification/maintenance-authority-integration.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T21:32:20Z · ae71944e6849

汇合T3真实ReadLease、ledger/PB同快照局部reader、封闭codec与取消清理；根及独立审阅已校准模块依赖/EOF生命周期/proof后继取消和长SQL边界，专项与旧入口真实PG通过，完整全源/basis/epoch及生产CLI仍后续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1207 个源文件；本次 38 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-maintenance-read-lease.json`
- added `docs/verification/assets/architecture-maintenance-read-lease-application.png`
- added `docs/verification/assets/architecture-maintenance-read-lease-detail.png`
- added `docs/verification/github-ci-authority-run.json`
- added `docs/verification/github-ci-authority-run.md`
- added `docs/verification/maintenance-read-lease-cleanup.json`
- added `docs/verification/maintenance-read-lease-integration.md`
- added `docs/verification/maintenance-read-lease.json`
- added `docs/verification/maintenance-read-lease.md`
- added `internal/billing/funding_ledger_maintenance.go`
- added `internal/billing/funding_ledger_maintenance_test.go`
- added `internal/billing/funding_ledger_query.go`
- modified `internal/billing/funding_ledger_read.go`
- modified `internal/maintenancetrust/authority_sandbox_fixture_test.go`
- added `internal/maintenancetrust/lease.go`
- added `internal/maintenancetrust/lease_codec.go`
- added `internal/maintenancetrust/lease_codec_test.go`
- added `internal/maintenancetrust/lease_integration_test.go`
- added `internal/maintenancetrust/lease_rows.go`
- added `internal/maintenancetrust/lease_rows_test.go`
- added `internal/maintenancetrust/lease_test.go`
- modified `internal/maintenancetrust/permit.go`
- added `internal/maintenancetrust/sandbox_export_test.go`
- added `internal/maintenancetrust/sandbox_external_test.go`
- modified `internal/maintenancetrust/snapshot.go`
- modified `internal/periodicbudget/verifier.go`
- added `internal/periodicbudget/verifier_maintenance.go`
- added `internal/periodicbudget/verifier_maintenance_test.go`
- modified `internal/periodicbudget/verifier_operations.go`
- added `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `scripts/maintenance-scan-qa/periodic_resources_test.go`
- added `specs/022-self-service-payments/c0-read-lease-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T22:20:15Z · 6724078ee9c7

T3真实ReadLease与ledger/PB维护读取器、私有静态catalog启动优化；根汇合独立审阅、完整PG回归、原进程超时回归与19图，保留局部结果及全来源后续边界；补齐README源码与1Panel部署说明

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1214 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/funding-lot-catalog.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/architecture-maintenance-read-lease.json`
- added `docs/verification/catalog-startup.json`
- added `docs/verification/catalog-startup.md`
- added `docs/verification/maintenance-read-lease-integration.json`
- modified `docs/verification/maintenance-read-lease-integration.md`
- modified `docs/verification/maintenance-read-lease.json`
- modified `internal/fundinglotcatalog/descriptor_types.go`
- modified `internal/fundinglotcatalog/expected.go`
- added `internal/fundinglotcatalog/expected_raw_parser.go`
- added `internal/fundinglotcatalog/expected_raw_parser_test.go`
- added `internal/fundinglotcatalog/expected_spans.go`
- added `internal/fundinglotcatalog/expected_spans_test.go`
- modified `specs/022-self-service-payments/c0-read-lease-contract.md`

</details>

## 2026-11-01T22:36:04Z · 9e7869b58366

根汇合T3/静态启动优化及两轮失败证据；专库整仓按包串行保留所有测试race与原期限，统一架构及19图已重新复核，独立审阅通过

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1214 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `Makefile`
- modified `docs/runbooks/development.md`
- modified `docs/verification/architecture-maintenance-read-lease.json`
- modified `docs/verification/maintenance-read-lease-integration.json`
- modified `docs/verification/maintenance-read-lease-integration.md`
- modified `specs/022-self-service-payments/c0-read-lease-contract.md`

</details>

## 2026-11-01T22:57:22Z · 5a0817e5dcb1

T3最终独立语义复核：维护API未接入main但传递初始化已生效；LLD历史与当前首片边界一致；按包串行全仓race/vet/build通过，追加后置只读PG审计；未增加生产入口或资金权限

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1215 个源文件；本次 6 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/LLD.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/maintenance-read-lease-integration.json`
- modified `docs/verification/maintenance-read-lease-integration.md`
- added `docs/verification/maintenance-read-lease-post-serial-audit.json`

</details>

## 2026-11-01T22:57:44Z · a1366cff4007

追加最终make check成功日志身份与1172.881秒串行全仓证据，所有并行审阅已汇合；仅文档证据收口，生产代码与19图正文未再变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1215 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/maintenance-read-lease-integration.json`
- modified `docs/verification/maintenance-read-lease-integration.md`

</details>

## 2026-11-01T23:25:16Z · faadb01a7849

SDD§7隔离维护集成依赖；独立审阅确认原reader/SQL/guard和授权无变化；原失败进程及真实双reader/九项资源界通过，19图重渲染，完整整仓待本轮执行

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1225 个源文件；本次 29 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/funding-lot-catalog.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-maintenance-integration-isolation.json`
- added `docs/verification/assets/architecture-maintenance-integration-isolation-application.png`
- added `docs/verification/assets/architecture-maintenance-integration-isolation-detail.png`
- added `docs/verification/maintenance-integration-isolation.json`
- added `docs/verification/maintenance-integration-isolation.md`
- modified `docs/verification/maintenance-read-lease-integration.md`
- added `internal/billing/funding_ledger_facts.go`
- deleted `internal/billing/funding_ledger_maintenance.go`
- modified `internal/billing/funding_ledger_maintenance_test.go`
- modified `internal/billing/funding_ledger_query.go`
- added `internal/maintenancescan/ledger.go`
- added `internal/maintenancescan/ledger_test.go`
- added `internal/maintenancescan/periodic_budget.go`
- added `internal/maintenancescan/periodic_budget_test.go`
- modified `internal/periodicbudget/verifier.go`
- modified `internal/periodicbudget/verifier_maintenance.go`
- modified `internal/periodicbudget/verifier_maintenance_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- modified `scripts/maintenance-scan-qa/periodic_resources_test.go`
- added `scripts/online-dependency-qa/dependencies_test.go`
- modified `specs/022-self-service-payments/c0-read-lease-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-01T23:42:24Z · 15e899eb9658

T3隔离修复完成：依赖门禁race、原失败进程、真实双reader九边界及934秒整仓/vet/build通过，更新post-audit/证据并重渲染19图；无能力权限扩张

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1226 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- added `docs/verification/maintenance-integration-isolation-post-audit.json`
- modified `docs/verification/maintenance-integration-isolation.json`
- modified `docs/verification/maintenance-integration-isolation.md`

</details>

## 2026-11-01T23:43:17Z · ac1bfa065df5

追加最终make check验证日志身份及集成文档，所有代码语义审阅已完成；清单保持覆盖1226个源/设计文件

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1226 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/maintenance-integration-isolation.json`
- modified `docs/verification/maintenance-integration-isolation.md`

</details>

## 2026-11-02T00:01:35Z · d6723e6e357d

记录精确实现提交824e4e5远端tree、README字节一致及十组GitHub Actions全部成功；仅追加发布验证，不改变源码或能力架构

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1226 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/maintenance-integration-isolation.json`
- modified `docs/verification/maintenance-integration-isolation.md`

</details>

## 2026-11-02T01:20:43Z · ed2fdec2eea5

新增C0人工核款/兑换券收据关联维护reader；冻结局部来源合同、固定C序三表分页及双向核验；补齐中断边界回归与真实PG未执行状态，保持完整来源覆盖和T3/T4未完成

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1236 个源文件；本次 16 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/funding-source-links.md`
- added `internal/billing/funding_source_facts.go`
- added `internal/billing/funding_source_query.go`
- added `internal/billing/funding_source_read.go`
- added `internal/billing/funding_source_read_test.go`
- added `internal/fundinglotverify/funding_sources.go`
- added `internal/fundinglotverify/funding_sources_test.go`
- added `internal/maintenancescan/funding_sources.go`
- added `internal/maintenancescan/funding_sources_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-source-link-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T02:13:42Z · 9219daeda4e1

022-C0-T3新增独立sandbox capture直接关联reader，复核并同步scope、依赖图、资金关系、验证边界与全源未覆盖状态；仅库级ScanSandboxSources，无生产接线或完整资金证明

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1244 个源文件；本次 16 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/sandbox-capture-links.md`
- added `internal/billing/funding_source_capture_facts.go`
- added `internal/billing/funding_source_capture_read.go`
- modified `internal/billing/funding_source_facts.go`
- modified `internal/billing/funding_source_read_test.go`
- added `internal/fundinglotverify/sandbox_capture_links.go`
- added `internal/fundinglotverify/sandbox_capture_links_test.go`
- added `internal/maintenancescan/sandbox_capture_links.go`
- added `internal/maintenancescan/sandbox_capture_links_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-sandbox-capture-link-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T02:15:33Z · 9d26248ad144

022-C0-T3 sandbox capture窄片文档补记make check与最终架构核验结果；真实PostgreSQL Plan/Init和TEST_DATABASE_URL整仓验收仍未执行

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1244 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/sandbox-capture-links.md`

</details>

## 2026-11-02T02:16:52Z · edd55ec3694a

移除新验证记录末尾空白并保持sandbox capture窄片架构快照一致；无行为变化，真实PG验收仍待专库

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1244 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/sandbox-capture-links.md`

</details>

## 2026-11-02T02:49:14Z · 7f578ec37a22

022-C0-T3增加采购预算投影维护reader；复核固定扫描SQL、独立核验、ReadLease权限、QA损坏场景、架构图和未验PG边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1253 个源文件；本次 16 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/runbooks/deployment.md`
- added `docs/verification/procurement-budget-maintenance.md`
- added `internal/billing/procurement_budget_facts.go`
- added `internal/billing/procurement_budget_read.go`
- added `internal/billing/procurement_budget_read_test.go`
- added `internal/fundinglotverify/procurement_budget.go`
- added `internal/fundinglotverify/procurement_budget_test.go`
- added `internal/maintenancescan/procurement.go`
- added `internal/maintenancescan/procurement_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-procurement-budget-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T02:50:52Z · 7e87fbcc8d2b

补记采购预算reader专项race、vet、make check与Plan/Init真实PG跳过状态；同步核验记录至架构快照

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1253 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-budget-maintenance.md`

</details>

## 2026-11-02T04:40:32Z · 51f6cd49345f

022-C0-T3扩展ScanProcurement为同一ReadLease内十二表采购budget与attempt binding双摘要；新增固定复合游标、纯big.Int关系核验、维护adapter与真实PG QA边界，更新README/HLD/LLD/架构图/计划/验证记录；成本更正证据命令审计余额观察及PB交叉关系仍未覆盖

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1259 个源文件；本次 16 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/procurement-budget-maintenance.md`
- added `internal/billing/procurement_attempt_facts.go`
- added `internal/billing/procurement_attempt_read.go`
- added `internal/billing/procurement_attempt_read_test.go`
- added `internal/fundinglotverify/procurement_attempt.go`
- added `internal/fundinglotverify/procurement_attempt_test.go`
- modified `internal/maintenancescan/procurement.go`
- modified `internal/maintenancescan/procurement_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-procurement-attempt-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-budget-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T05:10:43Z · e048ba6da730

补记采购十二表reader扩展的全仓Go race、make check与专用PG缺失跳过状态；无实现范围变化，继续明确真实Plan/Init和TEST_DATABASE_URL未执行

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1259 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-budget-maintenance.md`

</details>

## 2026-11-02T06:05:08Z · 793769f6bb49

022-C0采购同lease十五表与成本双审第三局部摘要；023-C-V独立精确价值条带和UTC锚点月；025-A1-G私有模型故障运行器及严格重放/期限oracle，真实PG和主进程未执行；已复核模块图、请求链、数据关系、权限、README和HLD/LLD，无新增迁移或在线资金接线

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1276 个源文件；本次 36 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/periodic-products.md`
- added `docs/verification/model-lifecycle-025.json`
- added `docs/verification/model-lifecycle-025.md`
- modified `docs/verification/procurement-budget-maintenance.md`
- added `internal/billing/procurement_approval_read.go`
- added `internal/billing/procurement_approval_read_test.go`
- modified `internal/billing/procurement_attempt_facts.go`
- modified `internal/billing/procurement_attempt_read.go`
- modified `internal/billing/procurement_attempt_read_test.go`
- added `internal/fundinglotverify/procurement_approval.go`
- added `internal/fundinglotverify/procurement_approval_test.go`
- modified `internal/maintenancescan/procurement.go`
- modified `internal/maintenancescan/procurement_test.go`
- added `internal/productvalue/fuzz_test.go`
- added `internal/productvalue/month.go`
- added `internal/productvalue/month_test.go`
- added `internal/productvalue/value.go`
- added `internal/productvalue/value_test.go`
- modified `scripts/browser-qa/main.go`
- added `scripts/browser-qa/model_fixture.go`
- added `scripts/browser-qa/model_fixture_test.go`
- added `scripts/browser-qa/model_lifecycle_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-procurement-approval-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-attempt-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-budget-contract.md`
- modified `specs/022-self-service-payments/plan.md`
- added `specs/023-periodic-products/c-value-contract.md`
- modified `specs/023-periodic-products/plan.md`
- added `specs/025-production-ha/a1-model-process-contract.md`
- modified `specs/025-production-ha/plan.md`

</details>

## 2026-11-02T06:06:06Z · b739416e9833

收口本批1276文件实现的专项和整仓race、make check通过记录；独立审查后的模型QA断言由最终包race/vet覆盖，真实数据库与模型main窗口仍未执行；仅验证/架构文本更新，无新增结构变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1276 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-budget-maintenance.md`

</details>

## 2026-11-02T06:36:43Z · fca4df94cf1f

采购维护核验扩为十九表五个局部摘要，新增命令/余额观察与成本审计/outbox独立core，复核模块图、单lease预算/唯一Finish、历史快照与权限边界；同步README部署贡献说明、HLD/LLD和验证范围，无迁移或在线资金权限变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1284 个源文件；本次 21 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/procurement-budget-maintenance.md`
- modified `internal/billing/procurement_attempt_facts.go`
- modified `internal/billing/procurement_attempt_read.go`
- modified `internal/billing/procurement_attempt_read_test.go`
- added `internal/billing/procurement_command_read.go`
- added `internal/fundinglotverify/procurement_command.go`
- added `internal/fundinglotverify/procurement_command_test.go`
- added `internal/fundinglotverify/procurement_cost_journal.go`
- added `internal/fundinglotverify/procurement_cost_journal_test.go`
- modified `internal/maintenancescan/procurement.go`
- modified `internal/maintenancescan/procurement_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-procurement-command-contract.md`
- added `specs/022-self-service-payments/c0-procurement-cost-journal-contract.md`
- added `specs/022-self-service-payments/c0-procurement-history-read-contract.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`

</details>

## 2026-11-02T06:37:14Z · 39e8eee04ea6

采购十九表五摘要最终验收记录收口；根已复核架构正文、README启动部署与局部证明边界，make check和补丁后四包race/vet通过，无新增结构或权限变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1284 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-budget-maintenance.md`

</details>

## 2026-11-02T07:22:57Z · 9fcd1c1f1a13

冻结并实现21表资金同session原始图，新增人工券及CustomerKey独立核验与原ScanFunding四摘要入口，保持旧reader语义和无资金写权边界；同步README、HLD/LLD、维护链路图、QA及验证范围

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1299 个源文件；本次 25 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-funding-financial-graph.json`
- added `docs/verification/funding-financial-graph.md`
- modified `docs/verification/funding-ledger-reader.md`
- modified `docs/verification/funding-source-links.md`
- added `internal/billing/funding_financial_read.go`
- added `internal/billing/funding_financial_read_test.go`
- modified `internal/billing/funding_ledger_query.go`
- added `internal/fundinglotverify/customer_key_graph.go`
- added `internal/fundinglotverify/customer_key_graph_test.go`
- added `internal/fundinglotverify/funding_graph_rows.go`
- added `internal/fundinglotverify/funding_source_graph.go`
- added `internal/fundinglotverify/funding_source_graph_checks.go`
- added `internal/fundinglotverify/funding_source_graph_test.go`
- added `internal/maintenancescan/funding_financial.go`
- added `internal/maintenancescan/funding_financial_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-customer-key-graph-contract.md`
- added `specs/022-self-service-payments/c0-financial-read-session-contract.md`
- added `specs/022-self-service-payments/c0-funding-source-graph-contract.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`

</details>

## 2026-11-02T07:24:39Z · f72d97fb2991

记录资金21表最终四包及全仓race、vet、make check和19图语法检查结果；复核模块、链路、权限与未验边界无进一步结构变化，真实PG保持明确SKIP

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1299 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/funding-financial-graph.md`

</details>

## 2026-11-02T08:21:37Z · 9c45ba4040a4

新增采购证据、周期预算及支付stored-verified纯图核验并完成交叉审查；保留完整reader/phase/basis/epoch未接线边界；真实PG维护QA修正与补验、README及贡献指南同步，无迁移/角色/在线资金装配变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1324 个源文件；本次 32 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-source-graphs.json`
- added `docs/verification/maintenance-financial-postgres.md`
- added `docs/verification/source-graph-cores.md`
- added `internal/fundinglotverify/periodic_budget_graph.go`
- added `internal/fundinglotverify/periodic_budget_graph_commands.go`
- added `internal/fundinglotverify/periodic_budget_graph_history_test.go`
- added `internal/fundinglotverify/periodic_budget_graph_index.go`
- added `internal/fundinglotverify/periodic_budget_graph_replay.go`
- added `internal/fundinglotverify/periodic_budget_graph_sources.go`
- added `internal/fundinglotverify/periodic_budget_graph_test.go`
- added `internal/fundinglotverify/periodic_budget_graph_values.go`
- added `internal/fundinglotverify/procurement_evidence_graph.go`
- added `internal/fundinglotverify/procurement_evidence_graph_accounts.go`
- added `internal/fundinglotverify/procurement_evidence_graph_events.go`
- added `internal/fundinglotverify/procurement_evidence_graph_evidence.go`
- added `internal/fundinglotverify/procurement_evidence_graph_test.go`
- added `internal/fundinglotverify/sandbox_source_graph.go`
- added `internal/fundinglotverify/sandbox_source_graph_commerce.go`
- added `internal/fundinglotverify/sandbox_source_graph_evidence.go`
- added `internal/fundinglotverify/sandbox_source_graph_funding.go`
- added `internal/fundinglotverify/sandbox_source_graph_test.go`
- added `internal/fundinglotverify/sandbox_source_graph_wire.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- modified `scripts/maintenance-scan-qa/periodic_resources_test.go`
- added `specs/022-self-service-payments/c0-periodic-budget-graph-contract.md`
- added `specs/022-self-service-payments/c0-procurement-evidence-graph-contract.md`
- added `specs/022-self-service-payments/c0-sandbox-source-graph-contract.md`

</details>

## 2026-11-02T08:57:34Z · c350b1cb2dd9

实现采购23组、PB18组、支付33组独立来源纯图，保持完整reader/phase/basis/epoch未接线；复核模块依赖与能力状态，补真实PG维护QA证据，修复支付/券命令冗余唯一索引并发占位并保留原资金与期限，完善GitHub README/贡献和验证说明

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1325 个源文件；本次 11 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/commerce-concurrency.md`
- modified `docs/verification/mvp-status.md`
- modified `internal/billing/voucher_commands.go`
- modified `internal/billing/voucher_integration_test.go`
- modified `internal/payments/service_commands.go`
- modified `internal/payments/service_test.go`
- modified `specs/022-self-service-payments/a-domain-contract.md`
- modified `specs/022-self-service-payments/b-domain-contract.md`

</details>

## 2026-11-02T09:17:44Z · 8d34ce9abd3c

根汇合复核三来源纯核验core与商业命令并发占位修复，默认完整真实PG回归959.665秒及C0独立专项210.188秒分别记录；验证后仅补README/架构/HLD/LLD/计划/验证文档，明确完整reader/native保真与共享预算仍待下一合同，无新迁移、权限、CI或生产装配

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1326 个源文件；本次 4 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/commerce-concurrency.md`
- modified `docs/verification/source-graph-cores.md`
- added `docs/verification/source-graph-integration.json`

</details>

## 2026-11-02T11:41:38Z · 9272bc72aa57

新增fundinglotscan固定68表列并集、同租约七业务来源算法与第六ScanCompleteSources；原生时间经timestamptz_send保真、Resource原因保留与Require忙拒绝清理；旧五scope/在线依赖/权限不变。已汇总三路实现与独立审查，同步模块图、维护链、能力表、README、HLD/LLD、计划及验证边界；lot/pre-post/basis/epoch与生产仍后续。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1350 个源文件；本次 34 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-complete-sources.json`
- added `docs/verification/assets/architecture-complete-sources-application.png`
- added `docs/verification/assets/architecture-complete-sources-detail.png`
- added `docs/verification/complete-source-reader.md`
- modified `docs/verification/mvp-status.md`
- added `internal/fundinglotscan/binding.go`
- added `internal/fundinglotscan/checks.go`
- added `internal/fundinglotscan/projection_funding.go`
- added `internal/fundinglotscan/projection_periodic.go`
- added `internal/fundinglotscan/projection_periodic_test.go`
- added `internal/fundinglotscan/projection_procurement.go`
- added `internal/fundinglotscan/projection_procurement_test.go`
- added `internal/fundinglotscan/projection_sandbox.go`
- added `internal/fundinglotscan/projection_sandbox_test.go`
- added `internal/fundinglotscan/reader.go`
- added `internal/fundinglotscan/reader_postgres_test.go`
- added `internal/fundinglotscan/reader_test.go`
- added `internal/fundinglotscan/types.go`
- added `internal/maintenancescan/complete_sources.go`
- added `internal/maintenancescan/complete_sources_test.go`
- modified `internal/maintenancetrust/lease.go`
- added `internal/maintenancetrust/lease_complete_source_integration_test.go`
- added `internal/maintenancetrust/lease_source_mode.go`
- added `internal/maintenancetrust/lease_source_mode_test.go`
- modified `internal/maintenancetrust/lease_test.go`
- added `scripts/maintenance-scan-qa/complete_source_edges_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- added `specs/022-self-service-payments/c0-complete-source-reader-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T11:48:47Z · 6f6a97a508e9

汇合最终68表实现及原生时间修复，三路复核与真实PG分层验证已收口；校准PB balanced测试预期、安装失败安全诊断及旧候选合同状态。源码与19图/能力/SDD/README同步，lot/pre-post/basis/epoch与生产边界保留；最终整仓回归结果单独记录。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1350 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`

</details>

## 2026-11-02T11:49:49Z · b150512855aa

完成最终候选合同状态与下一切片设计缺口校准；记录完整维护批次中的QA预期修正、安装前置未知失败及Plan成功复验。已逐项复核68表/七算法、原生wire、权限与在线依赖边界，README/HLD/LLD/19图及验证入口一致；整仓回归结果另记。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1350 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/complete-source-reader.md`
- modified `specs/022-self-service-payments/c0-maintenance-contract.md`
- modified `specs/022-self-service-payments/c0-sequence-contract.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`

</details>

## 2026-11-02T12:08:37Z · 4ec301e5a2d1

最终make check 6.33秒、指定专库make verify-mvp 966.88秒通过，286前端与默认Go race/vet/build完成；C0 opt-in按真实SQL/ordinary授权/维护各场景独立记证，保留原整批失败及Plan复验身份。仅补最终验证文档/机器记录与计划勾选，源码、权限、模块/请求/数据图无进一步变化，19图正文身份保持。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1351 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- added `docs/verification/complete-source-integration.json`
- modified `docs/verification/complete-source-reader.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T12:43:39Z · 906cc2485157

冻结并实现V0-WS七投影全钱包序列声明纯检查；核全序与journal/event绑定，无FIFO或维护权威；同步README、HLD/LLD、模块图、legacy开账边界与验证记录，运行依赖及001–022迁移不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1362 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- added `docs/verification/architecture-wallet-sequence.json`
- added `docs/verification/assets/architecture-wallet-sequence-application.png`
- added `docs/verification/assets/architecture-wallet-sequence-detail.png`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/wallet-sequence-rows.json`
- added `docs/verification/wallet-sequence-rows.md`
- added `internal/fundinglotverify/wallet_sequence.go`
- added `internal/fundinglotverify/wallet_sequence_check.go`
- added `internal/fundinglotverify/wallet_sequence_fuzz_test.go`
- added `internal/fundinglotverify/wallet_sequence_test.go`
- added `internal/fundinglotverify/wallet_sequence_values.go`
- modified `specs/022-self-service-payments/c0-sequence-contract.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`
- added `specs/022-self-service-payments/c0-wallet-sequence-rows-contract.md`

</details>

## 2026-11-02T13:02:55Z · deaafaae628c

V0-WS最终验收与文档收口：专库全仓race/vet/build和前端286项通过；校准维护文档中已实现68表读取与历史隔离CI状态，更新验证证据，代码身份不变且无运行权限或迁移变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1362 个源文件；本次 6 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/architecture/maintenance-gate.md`
- modified `docs/verification/architecture-wallet-sequence.json`
- modified `docs/verification/mvp-status.md`
- modified `docs/verification/wallet-sequence-rows.json`
- modified `docs/verification/wallet-sequence-rows.md`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`

</details>

## 2026-11-02T13:04:04Z · 9c34dbb3dbf6

补齐最终机器验收记录的Node TAP 286/286与0失败；复核代码身份、README及架构图未变，仅证据元数据更新

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1362 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/wallet-sequence-rows.json`

</details>

## 2026-11-02T14:05:55Z · 3240afab8984

V0-WL从25组原行独立重放lot/FIFO/结算/退款并核事件缓存和复合父关系；增加纯层节点与内部WS调用，同步README/HLD/LLD/计划和维护边界；无DB reader、迁移、六kind或在线资金变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1383 个源文件；本次 30 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-wallet-lots.json`
- added `docs/verification/assets/architecture-wallet-lots-application.png`
- added `docs/verification/assets/architecture-wallet-lots-detail.png`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/wallet-lots-rows.json`
- added `docs/verification/wallet-lots-rows.md`
- added `internal/fundinglotverify/wallet_lots.go`
- added `internal/fundinglotverify/wallet_lots_check.go`
- added `internal/fundinglotverify/wallet_lots_events.go`
- added `internal/fundinglotverify/wallet_lots_fuzz_test.go`
- added `internal/fundinglotverify/wallet_lots_index.go`
- added `internal/fundinglotverify/wallet_lots_internal.go`
- added `internal/fundinglotverify/wallet_lots_legacy.go`
- added `internal/fundinglotverify/wallet_lots_refund.go`
- added `internal/fundinglotverify/wallet_lots_replay.go`
- added `internal/fundinglotverify/wallet_lots_root_test.go`
- added `internal/fundinglotverify/wallet_lots_shape.go`
- added `internal/fundinglotverify/wallet_lots_sources.go`
- added `internal/fundinglotverify/wallet_lots_sweep.go`
- added `internal/fundinglotverify/wallet_lots_test.go`
- added `internal/fundinglotverify/wallet_lots_values.go`
- modified `specs/022-self-service-payments/c0-verifier-contract.md`
- added `specs/022-self-service-payments/c0-wallet-lots-rows-contract.md`

</details>

## 2026-11-02T14:25:41Z · 0d282cf66dd6

V0-WL最终纯包race、32508次fuzz、make check和专库make verify-mvp全部通过；仅补真实验证记录与三态/权限边界，Go源码测试摘要保持一致，20图已渲染复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1383 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/mvp-status.md`
- modified `docs/verification/wallet-lots-rows.json`
- modified `docs/verification/wallet-lots-rows.md`

</details>

## 2026-11-02T15:05:12Z · a323a2767cd9

完成第七ScanSourceWallet与同session84表八算法来源钱包读取；复核旧68表兼容、固定列预算、原Permit唯一Finish及未交付protected phase边界，同步README/启动部署手册/HLD/LLD和21图验证

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1398 个源文件；本次 29 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-source-wallet.json`
- added `docs/verification/assets/architecture-source-wallet-application.png`
- added `docs/verification/assets/architecture-source-wallet-detail.png`
- added `docs/verification/source-wallet-reader.json`
- added `docs/verification/source-wallet-reader.md`
- modified `internal/fundinglotscan/binding.go`
- added `internal/fundinglotscan/projection_wallet.go`
- added `internal/fundinglotscan/projection_wallet_test.go`
- modified `internal/fundinglotscan/reader.go`
- modified `internal/fundinglotscan/types.go`
- added `internal/fundinglotscan/wallet.go`
- added `internal/fundinglotscan/wallet_reader_postgres_test.go`
- added `internal/fundinglotscan/wallet_reader_test.go`
- added `internal/maintenancescan/source_wallet.go`
- added `internal/maintenancescan/source_wallet_test.go`
- modified `internal/maintenancetrust/lease.go`
- modified `internal/maintenancetrust/lease_source_mode.go`
- modified `internal/maintenancetrust/lease_source_mode_test.go`
- added `internal/maintenancetrust/lease_source_wallet_test.go`
- modified `internal/maintenancetrust/lease_test.go`
- added `scripts/maintenance-scan-qa/source_wallet_integration_test.go`
- added `specs/022-self-service-payments/c0-source-wallet-reader-contract.md`

</details>

## 2026-11-02T15:23:36Z · 09651f308e39

84表八算法来源钱包读取最终验收归档：相关真实PG/race、真实维护Plan/Init、make check及明确专库verify-mvp通过；只补验证记录与架构清单，Go源码测试身份保持，未开放protected phase或资金写权限

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1398 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/mvp-status.md`
- modified `docs/verification/source-wallet-reader.json`
- modified `docs/verification/source-wallet-reader.md`

</details>

## 2026-11-02T16:23:59Z · 7fc4eaee97a0

新增固定109表976列原二进制basis只读叶子和stdlib严格v2 codec；复核静态send前置、资源/EOF/帧、旧v1单向差分与真实专库，维护第八phase/epoch与在线资金仍后续；同步README/HLD/LLD/统一架构和维护图及验收边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1432 个源文件；本次 41 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-basis-proof-v2.json`
- added `docs/verification/assets/architecture-basis-proof-v2-application.png`
- added `docs/verification/assets/architecture-basis-proof-v2-detail.png`
- added `docs/verification/basis-proof-v2.json`
- added `docs/verification/basis-proof-v2.md`
- added `docs/verification/basis-send-metadata-audit.json`
- added `internal/fundinglotbasis/basis_test.go`
- added `internal/fundinglotbasis/independent_postgres_test.go`
- added `internal/fundinglotbasis/materials.go`
- added `internal/fundinglotbasis/materials/descriptor-v1.json`
- added `internal/fundinglotbasis/materials/manifest-v1.json`
- added `internal/fundinglotbasis/materials/send-v1.json`
- added `internal/fundinglotbasis/metadata.go`
- added `internal/fundinglotbasis/queries.go`
- added `internal/fundinglotbasis/read.go`
- added `internal/fundinglotbasis/record.go`
- added `internal/fundinglotbasis/types.go`
- added `internal/fundinglotproof/codec.go`
- added `internal/fundinglotproof/codec_test.go`
- added `internal/fundinglotproof/strict_test.go`
- added `internal/fundinglotproof/testdata/generate_golden.py`
- added `internal/fundinglotproof/testdata/ordinary.json`
- added `internal/fundinglotproof/testdata/predecessor_128keys.json`
- added `internal/fundinglotproof/testdata/predecessor_1keys.json`
- added `internal/fundinglotproof/testdata/predecessor_v1.json`
- added `internal/fundinglotproof/testdata/sandbox.json`
- added `internal/fundinglotproof/testdata/sandbox_128keys.json`
- added `internal/fundinglotproof/testdata/sandbox_1keys.json`
- added `internal/fundinglotproof/types.go`
- added `internal/fundinglotproof/v1.go`
- added `internal/maintenancetrust/proof_v2_compat_test.go`
- added `specs/022-self-service-payments/c0-basis-reader-contract.md`
- added `specs/022-self-service-payments/c0-basis-send-metadata-candidate.json`
- added `specs/022-self-service-payments/c0-proof-v2-codec-contract.md`

</details>

## 2026-11-02T16:24:54Z · fb0bf5ead439

补录根最终四包race与真实basis专库19终态、两叶子fuzz精确源码身份；正文模块与权限边界已复核，尚待整仓回归

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1432 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/basis-proof-v2.json`
- modified `docs/verification/basis-proof-v2.md`

</details>

## 2026-11-02T16:47:46Z · f7b328ef695f

汇合全部并行实施与独立审查后完成根语义复核；相关race含真实basis PG与v1差分、两leaf fuzz、make check及专库verify-mvp全部通过，补最终证据和README部署说明；澄清cutover basis只约束停服init，普通startup历史绑定与phase/epoch/runtime仍后续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1432 个源文件；本次 7 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/LLD.md`
- modified `docs/verification/architecture-basis-proof-v2.json`
- modified `docs/verification/basis-proof-v2.json`
- modified `docs/verification/basis-proof-v2.md`
- modified `docs/verification/mvp-status.md`
- modified `specs/022-self-service-payments/c0-proof-v2-codec-contract.md`
- modified `specs/022-self-service-payments/c0-schema-check-contract.md`

</details>

## 2026-11-02T17:12:11Z · d27da56634c1

新增原68表最小旧数据投影和独立纯开账规划；复核模块依赖、资金集合、资源界及未来phase权限边界，同步README/HLD/LLD/验证与23图，未新增数据库或在线写入

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1452 个源文件；本次 27 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-init-planner.json`
- added `docs/verification/assets/architecture-init-planner-application.png`
- added `docs/verification/assets/architecture-init-planner-detail.png`
- added `docs/verification/init-planner.json`
- added `docs/verification/init-planner.md`
- added `internal/fundinglotinit/build.go`
- added `internal/fundinglotinit/generate.go`
- added `internal/fundinglotinit/ledger.go`
- added `internal/fundinglotinit/planner_core_test.go`
- added `internal/fundinglotinit/planner_external_test.go`
- added `internal/fundinglotinit/sort.go`
- added `internal/fundinglotinit/sources.go`
- added `internal/fundinglotinit/testdata/README.md`
- added `internal/fundinglotinit/testdata/mixed_golden.json`
- added `internal/fundinglotinit/types.go`
- added `internal/fundinglotscan/seed_projection.go`
- added `internal/fundinglotscan/seed_projection_postgres_test.go`
- added `internal/fundinglotscan/seed_projection_test.go`
- added `internal/fundinglotverify/legacy_seed.go`
- added `specs/022-self-service-payments/c0-init-planner-contract.md`
- modified `specs/022-self-service-payments/c0-lot-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T17:30:00Z · 481e522cf0bb

纯开账规划与最小旧数据出口最终验证收口；相关专库race和46包整仓通过且源码身份一致，复核README/HLD/LLD、23图与未来phase边界，仅更新验证记录和架构快照

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1452 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/init-planner.json`
- modified `docs/verification/init-planner.md`
- modified `docs/verification/mvp-status.md`

</details>

## 2026-11-02T18:25:52Z · ce342ef86bfc

SDD冻结并实现原Permit受保护开账phase、固定writer与strict-v2/实际84表及109表post、永久busy和唯一提交；校准七core旧tenant TEXT且保留在线admission；真实专项431顶层零SKIP通过，同步README使用部署、HLD/LLD及24张架构图和失败修正记录，正式迁移/观察器/CLI/在线接线仍后续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1472 个源文件；本次 49 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-protected-init-application.png`
- added `docs/verification/assets/architecture-protected-init-detail.png`
- added `docs/verification/protected-init-phase.json`
- added `docs/verification/protected-init-phase.md`
- modified `internal/fundinglotverify/funding_source_graph.go`
- modified `internal/fundinglotverify/funding_source_graph_checks.go`
- modified `internal/fundinglotverify/funding_sources.go`
- added `internal/fundinglotverify/legacy_tenant.go`
- added `internal/fundinglotverify/legacy_tenant_domain_test.go`
- added `internal/fundinglotverify/legacy_tenant_scope_test.go`
- modified `internal/fundinglotverify/periodic_budget_graph_index.go`
- modified `internal/fundinglotverify/procurement_approval.go`
- modified `internal/fundinglotverify/procurement_attempt.go`
- modified `internal/fundinglotverify/procurement_cost_journal.go`
- modified `internal/fundinglotverify/procurement_evidence_graph_evidence.go`
- modified `internal/fundinglotverify/sandbox_source_graph_commerce.go`
- added `internal/maintenancetrust/funding_lots_commit.go`
- added `internal/maintenancetrust/funding_lots_legacy_integration_test.go`
- added `internal/maintenancetrust/funding_lots_phase.go`
- added `internal/maintenancetrust/funding_lots_phase_integration_test.go`
- added `internal/maintenancetrust/funding_lots_phase_test.go`
- added `internal/maintenancetrust/funding_lots_post.go`
- added `internal/maintenancetrust/funding_lots_proof.go`
- added `internal/maintenancetrust/funding_lots_proof_test.go`
- added `internal/maintenancetrust/funding_lots_rows.go`
- added `internal/maintenancetrust/funding_lots_run.go`
- added `internal/maintenancetrust/funding_lots_writer.go`
- added `internal/maintenancetrust/funding_lots_writer_test.go`
- modified `internal/maintenancetrust/gate.go`
- modified `internal/maintenancetrust/permit.go`
- modified `internal/maintenancetrust/snapshot.go`
- modified `internal/maintenancetrust/snapshot_fixture_test.go`
- modified `specs/022-self-service-payments/c0-funding-source-graph-contract.md`
- modified `specs/022-self-service-payments/c0-periodic-budget-graph-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-approval-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-attempt-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-cost-journal-contract.md`
- modified `specs/022-self-service-payments/c0-procurement-evidence-graph-contract.md`
- added `specs/022-self-service-payments/c0-protected-init-phase-contract.md`
- modified `specs/022-self-service-payments/c0-sandbox-source-graph-contract.md`
- modified `specs/022-self-service-payments/c0-source-link-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T18:43:05Z · a4d989eda790

受保护开账与旧tenant TEXT的实现保持专项和整仓所验身份；补齐最终验证记录：8包680.197秒/431顶层/3317父子终态零SKIP，make check及专库verify-mvp通过；24图与README使用部署边界已复核，无新增运行结构或生产接线

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1472 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/protected-init-phase.json`
- modified `docs/verification/protected-init-phase.md`

</details>

## 2026-11-02T18:56:04Z · 8d8efb5da28b

CI a9a5b59财务分片暴露既有pg_stat_activity文本与真实锁边采样错配；先冻023-A验收合同，再以精确tenant tuple/XID锁与blocking PID验证屏障，隔离可选幂等FK前锁；真实PG场景20次及全8场景race零SKIP通过。仅测试和文档变更，生产模块/锁序/图/迁移/权限不变，同步架构HLD/LLD及失败记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1474 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/pricing-lock-ci.json`
- added `docs/verification/pricing-lock-ci.md`
- modified `internal/billing/tenant_pricing_concurrency_integration_test.go`
- modified `specs/023-periodic-products/a-contract.md`

</details>

## 2026-11-02T18:57:24Z · 2b130ac94b05

CI并发观察测试补丁收口：真实PG目标20次及定价8场景race零SKIP通过，make check通过；仅补最终失败/修正证据，生产源码及架构图结构未变。保留a9a5b59整仓验证范围与旧CI失败，不将后续源码冒称旧run成功

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1474 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/pricing-lock-ci.json`
- modified `docs/verification/pricing-lock-ci.md`

</details>

## 2026-11-02T19:29:14Z · f9fc5dfd539e

C0-O1新增独立停服epoch只读观察、同次84表原件投影和私有事实读取；复核唯一Begin、期限、旧空安装权限及普通服务依赖隔离，更新模块图/流程/HLD/LLD/验证范围；README精简并校准默认端口、JSON环境示例与部署状态，正式迁移/在线资金路径未变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1489 个源文件；本次 31 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `README.md`
- modified `docs/architecture/maintenance-gate.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-epoch-observer-application.png`
- added `docs/verification/assets/architecture-epoch-observer-detail.png`
- added `docs/verification/epoch-observer.md`
- added `internal/fundinglotscan/epoch_proof_projection.go`
- added `internal/fundinglotscan/epoch_proof_projection_test.go`
- modified `internal/maintenancetrust/authority_identity.go`
- modified `internal/maintenancetrust/authority_manifest.go`
- added `internal/maintenancetrust/funding_lots_observe.go`
- added `internal/maintenancetrust/funding_lots_observe_business_integration_test.go`
- added `internal/maintenancetrust/funding_lots_observe_integration_test.go`
- added `internal/maintenancetrust/funding_lots_observe_rows.go`
- added `internal/maintenancetrust/funding_lots_observe_test.go`
- added `internal/maintenancetrust/funding_lots_observed_proof.go`
- added `internal/maintenancetrust/funding_lots_observed_proof_test.go`
- modified `internal/maintenancetrust/gate.go`
- added `internal/maintenancetrust/maintenance_facts.go`
- added `internal/maintenancetrust/maintenance_facts_test.go`
- modified `internal/maintenancetrust/predecessor.go`
- modified `internal/maintenancetrust/predecessor_postgres_test.go`
- modified `internal/maintenancetrust/predecessor_rows.go`
- modified `internal/maintenancetrust/snapshot.go`
- added `specs/022-self-service-payments/c0-epoch-observer-contract.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T19:42:50Z · febd5ac58dc9

汇合O1独立审查、完整169项维护测试发现/PASS及七包真实PG补跑证据；保留调度器中断exit1与历史失败，不扩大生产范围；同步手动专项累计预算建议、下一阶段权限与root设计关口，架构模块/流程无额外变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1491 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/runbooks/development.md`
- added `docs/verification/architecture-epoch-observer.json`
- added `docs/verification/epoch-observer.json`
- modified `docs/verification/epoch-observer.md`
- modified `specs/022-self-service-payments/c0-epoch-observer-contract.md`

</details>

## 2026-11-02T20:00:52Z · d7958fe75ebc

完成O1真实PG与整仓验收：457顶层/3455终态、46默认Go包及286前端项；保留中断与补跑证据，复核当前README/MVP状态/阶段表及25图，资金/权限/生产边界不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1491 个源文件；本次 8 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `README.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-epoch-observer.json`
- modified `docs/verification/epoch-observer.json`
- modified `docs/verification/epoch-observer.md`
- modified `docs/verification/mvp-status.md`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T20:02:05Z · e12ac2f31713

仅补录最后一次make check的成功结果与原始日志摘要；25图、全部已验源码及架构结构未变，复核GitHub交付文件与生产边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1491 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/epoch-observer.json`

</details>

## 2026-11-02T21:07:05Z · 1a76b04e2e8f

C0-R1-A冻结显式WalletReadV2与84表776列清单，旧root/API及生产接线保持；复核四列只读ACL与sandbox依赖镜像、同RR完整比较和新旧拒绝边界，同步模块图/权限细图/HLD/LLD/README与限定专库证据，26图已渲染视检，最终整仓检查待执行

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1507 个源文件；本次 26 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/funding-lot-catalog.md`
- added `docs/architecture/runtime-read-profile.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/architecture-runtime-read-profile.json`
- added `docs/verification/assets/architecture-runtime-read-profile-application.png`
- added `docs/verification/assets/architecture-runtime-read-profile-detail.png`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/runtime-read-profile-roots.json`
- added `docs/verification/runtime-read-profile.json`
- added `docs/verification/runtime-read-profile.md`
- modified `internal/fundinglotcatalog/collector.go`
- added `internal/fundinglotcatalog/profile.go`
- modified `internal/fundinglotcatalog/proof.go`
- modified `internal/fundinglotcatalog/types.go`
- added `internal/fundinglotcatalog/wallet_expected.go`
- added `internal/fundinglotcatalog/wallet_profile_integration_test.go`
- added `internal/fundinglotcatalog/wallet_profile_test.go`
- added `internal/fundinglotscan/wallet_readset_test.go`
- added `scripts/check-wallet-profile-roots.py`
- added `specs/022-self-service-payments/c0-runtime-read-permissions.sql`
- added `specs/022-self-service-payments/c0-runtime-read-profile-contract.md`
- added `specs/022-self-service-payments/c0-wallet-readset.json`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T21:26:08Z · dfb66e6692df

汇合R1-A全部审查与真实PG记录，复核137份源码身份、26图与550链接及README使用/部署边界；整仓286前端与46Go包race/vet/build通过，修正文档Gemini SSE必需查询参数；当前结构仍为显式catalog读取权限候选，R1-B/R2和生产状态不扩展

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1507 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/architecture-runtime-read-profile.json`
- modified `docs/verification/runtime-read-profile.json`
- modified `docs/verification/runtime-read-profile.md`

</details>

## 2026-11-02T21:26:54Z · 3e801abb4df3

仅补录最终make check再次成功6.062秒及其日志SHA；已验源码/静态材料、26图与模块/权限结构不变，复核最终README、SDD和交付文件，所有未验生产及R1-B/R2边界保持

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1507 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/runtime-read-profile.json`
- modified `docs/verification/runtime-read-profile.md`

</details>

## 2026-11-02T22:49:28Z · cd3d3785b266

C0-R1-B独立APP核验库：严格V1与私有APP材料、同84表投影、六条历史兼容、完整catalog/history和原事务证明；同步模块/流程/权限/寿命图及限定专库证据，保留活动PB来源门禁与未在线装配边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1542 个源文件；本次 49 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- added `docs/architecture/runtime-composer.md`
- modified `docs/architecture/runtime-read-profile.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- added `docs/verification/assets/architecture-runtime-composer-application.png`
- added `docs/verification/assets/architecture-runtime-composer-detail.png`
- added `docs/verification/runtime-composer.json`
- added `docs/verification/runtime-composer.md`
- modified `internal/fundinglotcatalog/wallet_profile_integration_test.go`
- added `internal/fundinglotproof/v1_public.go`
- added `internal/fundinglotproof/v1_public_test.go`
- added `internal/fundinglotruntime/application_codec.go`
- added `internal/fundinglotruntime/application_codec_test.go`
- added `internal/fundinglotruntime/application_files.go`
- added `internal/fundinglotruntime/application_state.go`
- added `internal/fundinglotruntime/application_state_test.go`
- added `internal/fundinglotruntime/expected/compatibility-v1.json`
- added `internal/fundinglotruntime/runtime_bindings.go`
- added `internal/fundinglotruntime/runtime_bindings_test.go`
- added `internal/fundinglotruntime/runtime_compose.go`
- added `internal/fundinglotruntime/runtime_history.go`
- added `internal/fundinglotruntime/runtime_history_test.go`
- added `internal/fundinglotruntime/runtime_materials.go`
- added `internal/fundinglotruntime/runtime_materials_test.go`
- added `internal/fundinglotruntime/runtime_rows.go`
- added `internal/fundinglotruntime/runtime_rows_test.go`
- added `internal/fundinglotruntime/runtime_types.go`
- added `internal/fundinglotruntime/runtime_window.go`
- added `internal/fundinglotscan/application_payment_proof_projection.go`
- added `internal/fundinglotscan/application_payment_proof_projection_test.go`
- modified `internal/fundinglotscan/wallet_readset_test.go`
- modified `internal/maintenancetrust/authority_profiles_integration_test.go`
- added `internal/maintenancetrust/funding_lots_runtime_integration_test.go`
- added `internal/maintenancetrust/funding_lots_runtime_procurement_integration_test.go`
- added `internal/maintenancetrust/funding_lots_runtime_profiles_integration_test.go`
- added `internal/maintenancetrust/funding_lots_runtime_resource_integration_test.go`
- modified `internal/maintenancetrust/sandbox_export_test.go`
- modified `internal/maintenancetrust/sandbox_external_test.go`
- added `internal/maintenancetrust/v1_codec_compat_test.go`
- added `internal/payments/v1_codec_compat_test.go`
- added `scripts/check-runtime-compatibility.py`
- modified `specs/022-self-service-payments/c0-proof-v2-codec-contract.md`
- added `specs/022-self-service-payments/c0-runtime-composer-contract.md`
- added `specs/022-self-service-payments/c0-runtime-pb-evidence-gap.md`
- modified `specs/022-self-service-payments/c0-wallet-readset.json`
- modified `specs/022-self-service-payments/plan.md`

</details>

## 2026-11-02T23:08:17Z · cefc8e9d5034

R1-B最终整仓验收记录：286前端测试、47 Go包race、vet与嵌入构建通过，关键73份源码未变；补充验证与README链接复核，架构/运行图和PB未解门禁语义保持

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1542 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/runtime-composer.json`
- modified `docs/verification/runtime-composer.md`

</details>

## 2026-11-03T00:00:00Z · 604e6799ab64

B-P1先冻结命令前缀与窗口出生合同，汇合纯核验与隔离SQL及actual APP候选专项；保留旧Base与legacy缺证及正式迁移/reader/runtime门禁；同步模块细图、HLD/LLD、README和GitHub协作入口

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1562 个源文件；本次 24 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `.github/ISSUE_TEMPLATE/bug_report.yml`
- added `.github/ISSUE_TEMPLATE/feature_request.yml`
- added `.github/pull_request_template.md`
- modified `README.md`
- added `docs/architecture/periodic-budget-prefix.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/assets/architecture-periodic-budget-prefix-application.png`
- added `docs/verification/assets/architecture-periodic-budget-prefix-detail.png`
- added `docs/verification/periodic-budget-prefix.json`
- added `docs/verification/periodic-budget-prefix.md`
- added `internal/periodicbudget/prefix_concurrency_integration_test.go`
- added `internal/periodicbudget/prefix_contract_integration_test.go`
- added `internal/periodicbudget/prefix_fault_integration_test.go`
- added `internal/periodicbudget/prefix_fixture_integration_test.go`
- added `internal/periodicbudgetprefix/check.go`
- added `internal/periodicbudgetprefix/check_test.go`
- added `internal/periodicbudgetprefix/snapshot.go`
- added `internal/periodicbudgetprefix/types.go`
- added `internal/periodicbudgetprefix/values.go`
- added `specs/023-periodic-products/b-prefix-candidate.sql`
- added `specs/023-periodic-products/b-prefix-contract.md`
- added `specs/023-periodic-products/b-prefix-permissions.sql`

</details>

## 2026-11-03T00:20:33Z · 9db30609fa74

B-P1最终15项actual APP候选race及19项纯race和全仓专库验收通过，12份候选源未漂移；补other分区显式PB测试环境、完整README竞品与运行说明和复现/验证记录；无正式迁移、在线资金、旧profile或启动接线变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1562 个源文件；本次 7 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `README.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/github-ci-profile.md`
- modified `docs/verification/periodic-budget-prefix.json`
- modified `docs/verification/periodic-budget-prefix.md`

</details>

## 2026-11-03T00:21:23Z · 479f6503186b

归档B-P1末轮make check 6.124秒与全仓1072.572秒成功记录；已复核候选代码/测试/SQL/合同固定及CI、README、架构来源链接；本次仅验证记录变更，无模块、数据、权限或运行时结构变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1562 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/periodic-budget-prefix.json`
- modified `docs/verification/periodic-budget-prefix.md`

</details>

## 2026-11-03T00:51:47Z · 551bf375b61e

B-P2新增固定original22同事务历史核验、纯候选材料及仅测试支持的savepoint安装和局部ACL复核；普通运行与正式迁移及旧catalog/runtime边界保持；同步HLD/LLD与安装架构和专项证据

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1578 个源文件；本次 24 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- added `docs/architecture/periodic-budget-prefix-install.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/periodic-budget-prefix-install.json`
- added `docs/verification/periodic-budget-prefix-install.md`
- modified `internal/periodicbudget/prefix_fixture_integration_test.go`
- added `internal/periodicbudget/prefix_install_integration_test.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- added `internal/periodicbudgetprefixschema/materials.go`
- added `internal/periodicbudgetprefixschema/materials/candidate-v1.sql`
- added `internal/periodicbudgetprefixschema/materials/permissions-v1.sql`
- added `internal/periodicbudgetprefixschema/materials_test.go`
- added `internal/testsupport/periodicprefix/install.go`
- added `internal/testsupport/periodicprefix/roles.go`
- added `internal/testsupport/periodicprefix/roles_test.go`
- added `migrations/original22.go`
- added `migrations/original22_manifest.json`
- added `migrations/original22_postgres_test.go`
- added `migrations/original22_test.go`
- added `specs/023-periodic-products/b-prefix-install-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T01:13:16Z · 07e8771c860a

B-P2汇合完成：固定历史/候选savepoint与ACL专项、完整商业回归及旧LegacyV1/WalletReadV2精确拒绝补测通过；源码边界、README、HLD/LLD、安装图与证据已复核，正式迁移/runtime仍未接入

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1580 个源文件；本次 6 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/architecture/periodic-budget-prefix-install.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-periodic-budget-prefix-install-detail.png`
- modified `docs/verification/periodic-budget-prefix-install.json`
- modified `docs/verification/periodic-budget-prefix-install.md`
- added `internal/fundinglotcatalog/prefix_rejection_integration_test.go`

</details>

## 2026-11-03T01:13:49Z · d4866433f34a

B-P2最终make check结果与精确旧catalog补验日志摘要已收口；仅验证元数据更新，已复核安装图/源码链接与普通运行不变的边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1580 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/periodic-budget-prefix-install.json`

</details>

## 2026-11-03T04:01:06Z · 224b44572775

B-P3显式prefix读集、basis/catalog、受保护开账与observer/APP组合；保留旧接口、原件与证据缺口，补共享采购准入及更正缺证和race预算未达边界；同步README部署、HLD/LLD、统一架构和真实专项结果

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1633 个源文件；本次 90 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/periodic-budget-prefix-install.md`
- added `docs/architecture/periodic-budget-prefix-profile.md`
- modified `docs/architecture/periodic-budget-prefix.md`
- modified `docs/architecture/runtime-composer.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/assets/architecture-periodic-budget-prefix-profile-application.png`
- added `docs/verification/assets/architecture-periodic-budget-prefix-profile-detail.png`
- modified `docs/verification/mvp-status.md`
- modified `docs/verification/periodic-budget-prefix-install.json`
- modified `docs/verification/periodic-budget-prefix-install.md`
- added `docs/verification/periodic-budget-prefix-profile.json`
- added `docs/verification/periodic-budget-prefix-profile.md`
- added `docs/verification/periodic-prefix-catalog-cpu.md`
- added `docs/verification/periodic-prefix-catalog-static.md`
- added `docs/verification/periodic-prefix-profile-roots.json`
- modified `internal/fundinglotbasis/materials.go`
- added `internal/fundinglotbasis/materials/descriptor-prefix-v1.json`
- modified `internal/fundinglotbasis/metadata.go`
- added `internal/fundinglotbasis/prefix.go`
- added `internal/fundinglotbasis/prefix_postgres_test.go`
- added `internal/fundinglotbasis/prefix_test.go`
- modified `internal/fundinglotbasis/queries.go`
- modified `internal/fundinglotbasis/read.go`
- modified `internal/fundinglotcatalog/collector.go`
- modified `internal/fundinglotcatalog/collector_decode.go`
- added `internal/fundinglotcatalog/collector_privilege_decode.go`
- added `internal/fundinglotcatalog/collector_privilege_decode_test.go`
- modified `internal/fundinglotcatalog/descriptor.go`
- added `internal/fundinglotcatalog/expected/periodic-prefix-v3-delta.json`
- added `internal/fundinglotcatalog/prefix_expected.go`
- added `internal/fundinglotcatalog/prefix_profile.go`
- added `internal/fundinglotcatalog/prefix_profile_integration_test.go`
- added `internal/fundinglotcatalog/prefix_profile_test.go`
- modified `internal/fundinglotcatalog/proof.go`
- added `internal/fundinglotprefixcompat/expected/compatibility-v1.json`
- added `internal/fundinglotprefixcompat/materials.go`
- added `internal/fundinglotprefixcompat/materials_test.go`
- added `internal/fundinglotruntime/prefix_bindings_test.go`
- added `internal/fundinglotruntime/prefix_compose.go`
- added `internal/fundinglotruntime/prefix_materials.go`
- modified `internal/fundinglotruntime/runtime_bindings.go`
- modified `internal/fundinglotruntime/runtime_compose.go`
- modified `internal/fundinglotruntime/runtime_types.go`
- added `internal/fundinglotscan/prefix_checks.go`
- added `internal/fundinglotscan/prefix_projection.go`
- added `internal/fundinglotscan/prefix_projection_test.go`
- added `internal/fundinglotscan/prefix_qualification.go`
- added `internal/fundinglotscan/prefix_reader.go`
- added `internal/fundinglotscan/prefix_reader_postgres_test.go`
- added `internal/fundinglotscan/prefix_reader_test.go`
- added `internal/fundinglotscan/prefix_readset_test.go`
- added `internal/fundinglotscan/prefix_types.go`
- added `internal/fundinglotscan/projection_prefix.go`
- modified `internal/fundinglotscan/wallet_readset_test.go`
- modified `internal/maintenancescan/complete_sources.go`
- added `internal/maintenancescan/periodic_prefix.go`
- added `internal/maintenancescan/periodic_prefix_test.go`
- modified `internal/maintenancescan/source_wallet.go`
- modified `internal/maintenancetrust/authority_manifest.go`
- modified `internal/maintenancetrust/funding_lots_observe.go`
- modified `internal/maintenancetrust/funding_lots_observed_proof.go`
- modified `internal/maintenancetrust/funding_lots_phase.go`
- modified `internal/maintenancetrust/funding_lots_post.go`
- added `internal/maintenancetrust/funding_lots_prefix.go`
- added `internal/maintenancetrust/funding_lots_prefix_business_integration_test.go`
- added `internal/maintenancetrust/funding_lots_prefix_integration_test.go`
- added `internal/maintenancetrust/funding_lots_prefix_legacy_integration_test.go`
- added `internal/maintenancetrust/funding_lots_prefix_observe.go`
- added `internal/maintenancetrust/funding_lots_prefix_observed_proof_test.go`
- added `internal/maintenancetrust/funding_lots_prefix_sandbox_integration_test.go`
- modified `internal/maintenancetrust/funding_lots_proof.go`
- modified `internal/maintenancetrust/funding_lots_run.go`
- modified `internal/maintenancetrust/funding_lots_runtime_procurement_integration_test.go`
- added `internal/maintenancetrust/periodic_prefix_profile.go`
- added `internal/maintenancetrust/periodic_prefix_profile_test.go`
- modified `internal/maintenancetrust/snapshot.go`
- modified `internal/maintenancetrust/snapshot_fixture_test.go`
- added `scripts/check-periodic-prefix-compatibility.py`
- added `scripts/check-prefix-profile-delta.py`
- added `scripts/check-prefix-profile-roots.py`
- added `scripts/maintenance-scan-qa/periodic_prefix_integration_test.go`
- added `specs/023-periodic-products/b-prefix-procurement-barrier-gap.md`
- added `specs/023-periodic-products/b-prefix-profile-contract.md`
- added `specs/023-periodic-products/b-prefix-readset.json`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T04:58:41Z · 65e85bfe971f

最终复核B-P3候选及README部署范围；追加标准专库7/13维护与2/37 catalog通过、整仓50包通过和资金短时限失败、保醒未阻止维护休眠的环境证据及独立构建，明确采购历史缺证和专项race未关闭；仅补交付文档与验证事实

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1633 个源文件；本次 4 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/verification/periodic-budget-prefix-profile.json`
- modified `docs/verification/periodic-budget-prefix-profile.md`
- modified `docs/verification/periodic-prefix-catalog-cpu.md`

</details>

## 2026-11-03T05:38:08Z · e41892451ee8

整理GitHub首页及贡献入口，记录0b150c5精确CI和SKIP边界；汇合B-P4采购历史设计稿及十一项证据命题，DDL权限与新profile未冻结。复核现有模块图、请求、数据、权限、部署和源码链接，无运行结构变化，保留B-P3与R2未完成门禁。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1635 个源文件；本次 9 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/periodic-budget-prefix-profile-ci.json`
- modified `docs/verification/periodic-budget-prefix-profile.md`
- modified `specs/023-periodic-products/b-prefix-procurement-barrier-gap.md`
- added `specs/023-periodic-products/b-procurement-prefix-design.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T05:39:39Z · 8eb92f0764cb

补齐GitHub首页与B-P4 D0文档交付验证记录：make check、链接和Shell语法已通过。并行审查已汇合，源码与图源未变；只追加准确验证范围，保留旧资金timeout、catalog race及采购缺证，无运行结构变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1636 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `docs/verification/github-readme-design-delivery.md`

</details>

## 2026-11-03T06:20:54Z · d047e658d54b

B-P4先冻结writer权限事务方向与纯identity ABI并交付六函数固定SQL材料和独立PG差分；模块图仅新增无默认调用的纯材料节点，请求/数据库关系/运行权限/部署未变，D1提案与完整writer及11项缺证门禁保留；复核HLD/LLD/计划和精确CI范围

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1648 个源文件；本次 19 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/github-readme-design-ci.json`
- modified `docs/verification/github-readme-design-delivery.md`
- added `docs/verification/procurement-identity-leaf.json`
- added `docs/verification/procurement-identity-leaf.md`
- added `docs/verification/testdata/procurement-writer-namespace/main.go`
- added `internal/procurementidentity/identity_postgres_test.go`
- added `internal/procurementidentity/materials.go`
- added `internal/procurementidentity/materials/identity-v1.sql`
- added `internal/procurementidentity/materials_test.go`
- modified `specs/023-periodic-products/b-prefix-procurement-barrier-gap.md`
- added `specs/023-periodic-products/b-procurement-account-d1-proposal.md`
- added `specs/023-periodic-products/b-procurement-cost-request-d1-proposal.md`
- added `specs/023-periodic-products/b-procurement-identity-contract.md`
- modified `specs/023-periodic-products/b-procurement-prefix-design.md`
- added `specs/023-periodic-products/b-procurement-writer-authority-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T06:23:28Z · 81491fee5ec7

汇合独立复核后为纯identity叶子追加other命令级PG回归开关及开发复现入口；保持原PB/分区/期限和其他C0边界，未改变业务writer、运行权限或部署结构

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1648 个源文件；本次 8 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/github-ci-profile.md`
- modified `docs/verification/procurement-identity-leaf.json`
- modified `docs/verification/procurement-identity-leaf.md`
- modified `specs/023-periodic-products/b-procurement-identity-contract.md`

</details>

## 2026-11-03T07:07:40Z · 79f2682b7a32

B-P4纯identity最终集成复核：同步完整回归50包通过2失败4无测试、三超时用例定向成功及准确自有资源清理，保留整仓失败和D1/B-P3/R2门禁；只有纯材料及other专项CI新增，运行链/业务关系/权限/部署不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1648 个源文件；本次 3 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/delivery-plan.md`
- modified `docs/verification/procurement-identity-leaf.json`
- modified `docs/verification/procurement-identity-leaf.md`

</details>

## 2026-11-03T08:05:01Z · a55ba1ebbc9c

汇合B-P4共同来源、账户、成本request和PB最小权限逻辑合同；复核候选关系、source先出生/单attempt链及Reconcile资金时点，新增独立锁权诊断与准确2223e40 CI证据；无线上调用、实际业务表、正式权限或部署结构变化，D1整体及旧缺证门禁保留

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1657 个源文件；本次 14 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/procurement-account-lock-acl.json`
- added `docs/verification/procurement-account-lock-acl.md`
- added `docs/verification/procurement-identity-ci.json`
- added `docs/verification/procurement-identity-ci.md`
- added `docs/verification/testdata/procurement-account-lock-acl/main.go`
- added `specs/023-periodic-products/b-procurement-account-contract.md`
- added `specs/023-periodic-products/b-procurement-cost-request-contract.md`
- added `specs/023-periodic-products/b-procurement-pb-acl-contract.md`
- modified `specs/023-periodic-products/b-procurement-prefix-design.md`
- added `specs/023-periodic-products/b-procurement-source-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T09:16:44Z · 77f2625983b3

汇合采购32表物理/执行/ACL/重放合同，校准DB选价与命令采时、status/OID和xmin边界；新增只读列布局工具及Make/CI检查，同步README/HLD/LLD与交付计划，无线上业务SQL或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1665 个源文件；本次 18 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `Makefile`
- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/procurement-prefix-layout.md`
- added `scripts/check-procurement-layout/main.go`
- added `scripts/check-procurement-layout/main_test.go`
- modified `specs/023-periodic-products/b-procurement-account-contract.md`
- added `specs/023-periodic-products/b-procurement-column-layout-v1.json`
- modified `specs/023-periodic-products/b-procurement-cost-request-contract.md`
- added `specs/023-periodic-products/b-procurement-execution-contract.md`
- added `specs/023-periodic-products/b-procurement-full-acl-contract.md`
- added `specs/023-periodic-products/b-procurement-replay-read-contract.md`
- added `specs/023-periodic-products/b-procurement-schema-contract.md`
- modified `specs/023-periodic-products/b-procurement-source-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T09:18:05Z · 0054cedc6548

收口采购列布局专项与最终make check实际结果，补齐统一架构验证链接；仅更新验证说明，无新增运行结构或权限

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1665 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-prefix-layout.md`

</details>

## 2026-11-03T10:06:57Z · 671ea0c852d3

按SDD落实采购关系SQL：32表715列/68 generated与原18表110列，685约束固定摘要及phase装配，专库PG结构和12声明边race通过；同步架构模块图、HLD/LLD/计划、README/SECURITY与验证边界；无正式迁移/业务调用/权限部署变化，保留宽TEXT与完整writer/profile门禁

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1680 个源文件；本次 25 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/procurement-relation-materials.md`
- added `internal/procurementprefixschema/materials.go`
- added `internal/procurementprefixschema/materials/account-constraints-v1.sql`
- added `internal/procurementprefixschema/materials/cost-constraints-v1.sql`
- added `internal/procurementprefixschema/materials/original-columns-v1.sql`
- added `internal/procurementprefixschema/materials/original-constraints-v1.sql`
- added `internal/procurementprefixschema/materials/source-constraints-v1.sql`
- added `internal/procurementprefixschema/materials/tables-v1.sql`
- added `internal/procurementprefixschema/materials_postgres_test.go`
- added `internal/procurementprefixschema/materials_test.go`
- modified `scripts/check-procurement-layout/main.go`
- modified `scripts/check-procurement-layout/main_test.go`
- added `specs/023-periodic-products/b-procurement-account-ddl-contract.md`
- modified `specs/023-periodic-products/b-procurement-column-layout-v1.json`
- added `specs/023-periodic-products/b-procurement-cost-ddl-contract.md`
- modified `specs/023-periodic-products/b-procurement-cost-request-contract.md`
- added `specs/023-periodic-products/b-procurement-original-column-layout-v1.json`
- added `specs/023-periodic-products/b-procurement-original-ddl-contract.md`
- modified `specs/023-periodic-products/b-procurement-schema-contract.md`
- added `specs/023-periodic-products/b-procurement-source-ddl-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T10:07:48Z · 44cd0435044a

补记采购关系材料最终验证：685约束PG/race和12命名声明边通过，Make静态检查通过；已再次复核README使用/部署、安全报告、模块图和所有候选边界，无线上架构变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1680 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-relation-materials.md`

</details>

## 2026-11-03T10:56:17Z · ce7fd63c099e

按SDD交付采购账户create/configure隔离writer与精确ACL，actual APP三组专库race及并发/CAS/撤权提交回滚通过；增加40原件482列与32前缀715列DTO、72数组预算和旧Base无损投影，无Check；精确关联RC六场景实验与268键/291FK清单静态核对通过但原685约束未改；同步HLD/LLD/计划/验证、README安装与Compose排障和1Panel网络前提。无正式迁移、默认main、在线资金或部署权限变化，保留完整writer/profile/宽TEXT及生产验收边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1700 个源文件；本次 28 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/runbooks/deployment.md`
- added `docs/verification/procurement-account-prefix.md`
- added `internal/procurementaccountwriter/account_postgres_test.go`
- added `internal/procurementaccountwriter/materials.go`
- added `internal/procurementaccountwriter/materials/account-writer-v1.sql`
- added `internal/procurementaccountwriter/materials/permissions-v1.sql`
- added `internal/procurementaccountwriter/materials_test.go`
- added `internal/procurementprefix/base_projection.go`
- added `internal/procurementprefix/base_projection_test.go`
- added `internal/procurementprefix/original_types.go`
- added `internal/procurementprefix/scalar.go`
- added `internal/procurementprefix/scalar_test.go`
- added `internal/procurementprefix/types.go`
- added `internal/procurementprefix/types_test.go`
- added `internal/procurementprefixschema/exact_link_experiment_postgres_test.go`
- added `internal/procurementprefixschema/exact_link_inventory_test.go`
- added `specs/023-periodic-products/b-procurement-account-writer-contract.md`
- added `specs/023-periodic-products/b-procurement-core-dto-contract.md`
- added `specs/023-periodic-products/b-procurement-exact-link-contract.md`
- added `specs/023-periodic-products/b-procurement-exact-link-inventory-v1.json`
- modified `specs/023-periodic-products/b-procurement-execution-contract.md`
- added `specs/023-periodic-products/b-procurement-original-core-layout-v1.json`
- modified `specs/023-periodic-products/b-procurement-replay-read-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T13:23:20Z · db2ecf8b56f9

新增采购历史私有来源/请求/账户重放、共享资源排序和十一Required自然对象集合；独立复核修复transport reason、账户/attempt顺序及计数身份，更新README/HLD/LLD/计划与模块细图。来源仍不完整且Qualified为空，AttemptCost/Base聚合未接通，无公共Check、迁移、权限或线上调用变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1729 个源文件；本次 34 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- added `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/procurement-replay-leaves.md`
- added `internal/procurementprefix/account_replay.go`
- added `internal/procurementprefix/account_replay_barriers.go`
- added `internal/procurementprefix/account_replay_evidence.go`
- added `internal/procurementprefix/account_replay_json.go`
- added `internal/procurementprefix/account_replay_test.go`
- added `internal/procurementprefix/replay_support.go`
- added `internal/procurementprefix/replay_support_test.go`
- added `internal/procurementprefix/request_replay.go`
- added `internal/procurementprefix/request_replay_counters_test.go`
- added `internal/procurementprefix/request_replay_input.go`
- added `internal/procurementprefix/request_replay_test.go`
- added `internal/procurementprefix/required.go`
- added `internal/procurementprefix/required_parse.go`
- added `internal/procurementprefix/required_test.go`
- added `internal/procurementprefix/source_closure.go`
- added `internal/procurementprefix/source_closure_canonical.go`
- added `internal/procurementprefix/source_closure_chains.go`
- added `internal/procurementprefix/source_closure_chains_test.go`
- added `internal/procurementprefix/source_closure_companions.go`
- added `internal/procurementprefix/source_closure_generated.go`
- added `internal/procurementprefix/source_closure_generated_test.go`
- added `internal/procurementprefix/source_closure_layout.go`
- added `internal/procurementprefix/source_closure_pointers.go`
- added `internal/procurementprefix/source_closure_templates_test.go`
- added `internal/procurementprefix/source_closure_test.go`
- added `internal/procurementprefix/source_closure_unions.go`
- added `specs/023-periodic-products/b-procurement-replay-implementation-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-read-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T15:42:53Z · 6bbf5566284f

采购成本历史与Reconcile客户结算私有核验、严格路由及冻结快照续片；修正Money审计ABI和自然计数，复核README/HLD/LLD/调用图与未完成历史cut，无正式迁移或线上接线

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1745 个源文件；本次 27 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/procurement-cost-finish-replay.md`
- modified `docs/verification/procurement-replay-leaves.md`
- modified `internal/procurementprefix/account_replay_barriers.go`
- modified `internal/procurementprefix/account_replay_test.go`
- added `internal/procurementprefix/attempt_cost.go`
- added `internal/procurementprefix/attempt_cost_events.go`
- added `internal/procurementprefix/attempt_cost_objects.go`
- added `internal/procurementprefix/attempt_cost_test.go`
- added `internal/procurementprefix/finish_replay.go`
- added `internal/procurementprefix/finish_replay_edges_test.go`
- added `internal/procurementprefix/finish_replay_effects.go`
- added `internal/procurementprefix/finish_replay_json.go`
- added `internal/procurementprefix/finish_replay_test.go`
- modified `internal/procurementprefix/source_closure.go`
- added `internal/procurementprefix/source_closure_birth_snapshots.go`
- added `internal/procurementprefix/source_closure_birth_snapshots_test.go`
- added `internal/procurementprefix/source_closure_original_unions.go`
- added `internal/procurementprefix/source_closure_original_unions_test.go`
- added `internal/procurementprefix/source_closure_routes.go`
- added `internal/procurementprefix/source_closure_routes_test.go`
- modified `specs/023-periodic-products/b-procurement-replay-implementation-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-read-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T15:43:44Z · a8ee19bce24b

归档成本与finish续片最终87顶层434子例race及make check通过；复核1745文件、README部署说明和私有子域未完成边界，代码与依赖无后续改变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1745 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-cost-finish-replay.md`

</details>

## 2026-11-03T16:10:53Z · 6299c2eb784c

采购来源续片：核当前request/attempt末项、永久dispatch和p origin/last双向关系及严格Usage，修复finish墙钟回退误拒；独立反例复验，补逐对象依赖与观察收口设计，同步README/HLD/LLD/架构与验证；无715列、旧checker/profile、正式迁移或在线权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1752 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/procurement-cost-finish-replay.md`
- added `docs/verification/procurement-current-source-clocks.md`
- modified `internal/procurementprefix/finish_replay.go`
- added `internal/procurementprefix/finish_replay_clocks_test.go`
- modified `internal/procurementprefix/finish_replay_test.go`
- modified `internal/procurementprefix/source_closure.go`
- modified `internal/procurementprefix/source_closure_birth_snapshots_test.go`
- modified `internal/procurementprefix/source_closure_chains.go`
- added `internal/procurementprefix/source_closure_current_pointers.go`
- added `internal/procurementprefix/source_closure_current_pointers_test.go`
- added `internal/procurementprefix/source_closure_usage_payloads.go`
- added `internal/procurementprefix/source_closure_usage_payloads_test.go`
- added `specs/023-periodic-products/b-procurement-replay-closure-plan.md`
- modified `specs/023-periodic-products/b-procurement-replay-implementation-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-read-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T16:11:43Z · d18c15f69049

归档来源当前指针与独立墙钟续片：98顶层576子例race、旧checker回归及make check通过；复核1752文件和README使用部署边界，代码测试依赖无后续修改

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1752 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-current-source-clocks.md`

</details>

## 2026-11-03T16:36:10Z · 64771a166af7

采购历史纯核续片：补齐人工P→A→R及case最后人工来源、派生normal decision真实before_latest桥、Reconciliation/finish行内NULL角色结果与独立deadline联合；同步README/HLD/LLD/收口计划和验证。复核旧人工缺证、无派生latest/sent与历史PB两项边界；无正式DDL、HTTP、writer权限、默认main或部署结构变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1758 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/procurement-current-source-clocks.md`
- added `docs/verification/procurement-reconciliation-order.md`
- modified `internal/procurementprefix/finish_replay.go`
- added `internal/procurementprefix/finish_replay_binding_test.go`
- modified `internal/procurementprefix/finish_replay_clocks_test.go`
- modified `internal/procurementprefix/finish_replay_test.go`
- modified `internal/procurementprefix/source_closure.go`
- added `internal/procurementprefix/source_closure_finish_unions.go`
- added `internal/procurementprefix/source_closure_finish_unions_test.go`
- added `internal/procurementprefix/source_closure_reconciliation_order.go`
- added `internal/procurementprefix/source_closure_reconciliation_order_test.go`
- modified `specs/023-periodic-products/b-procurement-cost-ddl-contract.md`
- modified `specs/023-periodic-products/b-procurement-cost-request-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-closure-plan.md`
- modified `specs/023-periodic-products/b-procurement-replay-implementation-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-read-contract.md`

</details>

## 2026-11-03T16:37:03Z · 932dc21f982e

归档人工对账顺序续片的根集成证据：113顶层/706子例race与原checker回归、make check及README使用部署只读审查均通过；复核未派生cut与历史PB两项缺口、旧人工LegacyCases和未开放Check边界。仅验证记录变化，无代码、依赖、权限或部署结构变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1758 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-reconciliation-order.md`

</details>

## 2026-11-03T17:14:38Z · f22631eb6b39

采购来源纯核续片：40类原件本地值、命令与采购audit/outbox严格JSON、补充业务联合和attempt/account/member连续性；交叉修正PB可空UUID、unknown与超额异常及dispute精确键、错账户map顺序。更新README启动方式和验证入口、HLD/LLD与统一架构；明确PB JSON、跨行精确效果/历史观察/逐对象资格未齐。无正式迁移、HTTP、ACL或线上资金接线变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1767 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/procurement-source-values-continuity.md`
- modified `internal/procurementprefix/source_closure.go`
- added `internal/procurementprefix/source_closure_business_unions.go`
- added `internal/procurementprefix/source_closure_business_unions_test.go`
- added `internal/procurementprefix/source_closure_continuity.go`
- added `internal/procurementprefix/source_closure_continuity_test.go`
- added `internal/procurementprefix/source_closure_original_values.go`
- added `internal/procurementprefix/source_closure_original_values_test.go`
- added `internal/procurementprefix/source_closure_payloads.go`
- added `internal/procurementprefix/source_closure_payloads_test.go`
- modified `internal/procurementprefix/source_closure_routes_test.go`
- modified `internal/procurementprefix/source_closure_unions.go`
- modified `specs/023-periodic-products/b-procurement-replay-closure-plan.md`
- modified `specs/023-periodic-products/b-procurement-replay-implementation-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T17:15:19Z · 9d52ef061931

归档来源值与连续性根集成：144顶层/845子例race、旧checker回归、前端286项及make check通过，README使用部署独立核对完成；补准确跨行和PB五类剩余义务，代码测试依赖无后续修改。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1767 个源文件；本次 3 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/procurement-source-values-continuity.md`

</details>

## 2026-11-03T17:55:27Z · fc70a145af86

采购来源续片按三个合同实现PB操作严格JSON/指纹、命令审计消息内容归属与真实ledger/客户及供应商PB效果双向集合，补业务身份和物理owner唯一门禁；已语义复核模块调用图、HLD/LLD、能力状态和README；新增B-HO1仅为历史观察设计草案，普通finish来源、legacy分支和全writer锁图待独立冻结，正式迁移/权限/部署及在线资金不变。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1778 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/procurement-source-relations.md`
- modified `internal/procurementprefix/source_closure.go`
- added `internal/procurementprefix/source_closure_effect_relations.go`
- added `internal/procurementprefix/source_closure_effect_relations_test.go`
- added `internal/procurementprefix/source_closure_payload_relations.go`
- added `internal/procurementprefix/source_closure_payload_relations_test.go`
- added `internal/procurementprefix/source_closure_pb_payloads.go`
- added `internal/procurementprefix/source_closure_pb_payloads_test.go`
- modified `internal/procurementprefix/source_closure_test.go`
- added `specs/023-periodic-products/b-procurement-effect-relations-contract.md`
- added `specs/023-periodic-products/b-procurement-historical-observation-design.md`
- added `specs/023-periodic-products/b-procurement-payload-relations-contract.md`
- added `specs/023-periodic-products/b-procurement-pb-json-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-closure-plan.md`
- modified `specs/023-periodic-products/b-procurement-replay-implementation-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T17:58:29Z · 8d2b50822af4

独立复核补齐effect直接枚举的owner及admission/supplier event物理唯一门禁并持久回归；最终完整新包173顶层905子例race全通过。再次语义复核来源域职责、错误归属、未完成观察/Q边界及B-HO1冻结前门禁，部署和资金授权不变。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1778 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-source-relations.md`
- modified `internal/procurementprefix/source_closure_effect_relations.go`
- modified `internal/procurementprefix/source_closure_effect_relations_test.go`

</details>

## 2026-11-03T17:59:24Z · 04cc63a976ce

归档冻结后173顶层905子例完整race、旧checker回归、最终make check及Go构建的实际结果；只更新验证记录，已复核模块图、来源/Q与B-HO1草案边界，行为代码、部署结构及权限未再变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1778 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-source-relations.md`

</details>

## 2026-11-03T18:27:46Z · d5bfff58c94a

按冻结合同接入更正原件、PB配置观察与事件三层重放，新增actual APP 203项提交撤权及4锁故障；同步模块图、HLD/LLD、README和验证边界，线上迁移权限不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1791 个源文件；本次 21 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/procurement-account-prefix.md`
- added `docs/verification/procurement-pb-relations.md`
- added `internal/procurementaccountwriter/account_fault_postgres_test.go`
- modified `internal/procurementprefix/source_closure.go`
- added `internal/procurementprefix/source_closure_correction_relations.go`
- added `internal/procurementprefix/source_closure_correction_relations_test.go`
- added `internal/procurementprefix/source_closure_pb_event_relations.go`
- added `internal/procurementprefix/source_closure_pb_event_relations_test.go`
- added `internal/procurementprefix/source_closure_pb_operation_relations.go`
- added `internal/procurementprefix/source_closure_pb_operation_relations_test.go`
- added `internal/procurementprefix/source_closure_pb_relations_integration_test.go`
- added `specs/023-periodic-products/b-procurement-account-writer-fault-matrix.md`
- added `specs/023-periodic-products/b-procurement-correction-original-relations-contract.md`
- added `specs/023-periodic-products/b-procurement-pb-event-relations-contract.md`
- added `specs/023-periodic-products/b-procurement-pb-operation-relations-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-closure-plan.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T18:28:28Z · 69bae45fac04

归档最终205顶层1053子例纯核race、7顶层221子例实际PG账户回归与独立反例；make check和二进制构建通过，复核能力边界不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1791 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-pb-relations.md`

</details>

## 2026-11-03T18:52:16Z · 062965662dcc

接入私有PB命令精确动态前缀并复核多值/资金cut/出生ordinal与线性错误范围；新增138必要W授权232次失权及50正控制，实际PG账户与纯核最终race通过；冻结下一attempt出生根真实admitted_at hook与A0–A6设计；归档原5秒catalog正式双轮及drift复验，保留原失败与采购缺证；同步HLD/LLD、统一架构/细图、计划、验证及README仓库导览；无正式迁移、线上调用、权限或部署变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1798 个源文件；本次 22 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/periodic-budget-prefix-profile.md`
- modified `docs/architecture/procurement-replay.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/periodic-prefix-catalog-cpu.md`
- added `docs/verification/procurement-pb-snapshot-dml.md`
- added `internal/procurementaccountwriter/account_dml_fault_postgres_test.go`
- modified `internal/procurementprefix/source_closure.go`
- modified `internal/procurementprefix/source_closure_pb_relations_integration_test.go`
- added `internal/procurementprefix/source_closure_pb_snapshot_prefix.go`
- added `internal/procurementprefix/source_closure_pb_snapshot_prefix_test.go`
- added `specs/023-periodic-products/b-procurement-account-writer-dml-fault-contract.md`
- added `specs/023-periodic-products/b-procurement-attempt-birth-writer-contract.md`
- modified `specs/023-periodic-products/b-procurement-execution-contract.md`
- modified `specs/023-periodic-products/b-procurement-full-acl-contract.md`
- added `specs/023-periodic-products/b-procurement-pb-snapshot-prefix-contract.md`
- modified `specs/023-periodic-products/b-procurement-replay-closure-plan.md`
- modified `specs/023-periodic-products/b-procurement-schema-contract.md`
- modified `specs/023-periodic-products/b-procurement-source-contract.md`
- modified `specs/023-periodic-products/plan.md`

</details>

## 2026-11-03T18:53:22Z · 6194f2ce2f37

归档本片最终纯核220/1123、实际PG账户10/478、catalog3/37全部race通过与原失败；make check及最终二进制构建已通过；复核1798文件模块/数据/权限/能力边界与README使用部署说明，无后续行为代码变动

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1798 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-pb-snapshot-dml.md`

</details>

## 2026-11-03T19:22:49Z · 80be5e0776ed

新增attempt五函数原规则及原Go/actual APP差分、独立A1四表窄关系与真实Reserve自动来源/guard/最小ACL和限定故障验证；复核候选模块图、真实时序、NULL与TEXT关系、角色边界和未完成矩阵，README补SDK并校准catalog已过专项，普通迁移及线上接线不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1818 个源文件；本次 26 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `README.md`
- added `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/procurement-attempt-rules.md`
- added `docs/verification/procurement-request-birth.md`
- added `internal/billing/procurement_attempt_rules_postgres_test.go`
- added `internal/procurementattemptrules/materials.go`
- added `internal/procurementattemptrules/materials/attempt-rules-v1.sql`
- added `internal/procurementattemptrules/materials_test.go`
- added `internal/procurementbirthschema/materials.go`
- added `internal/procurementbirthschema/materials/a1-constraints-v1.sql`
- added `internal/procurementbirthschema/materials/a1-original-columns-v1.sql`
- added `internal/procurementbirthschema/materials/a1-tables-v1.sql`
- added `internal/procurementbirthschema/materials_test.go`
- added `internal/procurementrequestbirth/materials.go`
- added `internal/procurementrequestbirth/materials/permissions-v1.sql`
- added `internal/procurementrequestbirth/materials/request-birth-writer-v1.sql`
- added `internal/procurementrequestbirth/materials_test.go`
- added `internal/procurementrequestbirth/request_birth_postgres_test.go`
- added `specs/023-periodic-products/b-procurement-attempt-rules-contract.md`
- added `specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-writer-contract.md`
- modified `specs/023-periodic-products/b-procurement-schema-contract.md`

</details>

## 2026-11-03T19:23:36Z · 35a22ee74500

归档原规则与A1真实Reserve限定PG、两次fixture及SQL安装失败修正、三锁列最小授权和剩余预算/并发/撤权矩阵；统一模块/时序/关系/权限/README已语义复核，补最终make check与零残留观察，无线上结构或普通迁移变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1818 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-request-birth.md`

</details>

## 2026-11-03T19:41:22Z · 0bad2a97b346

A1新增预算、钱包真实等待、raw误授和有效撤权四合同及四测试；23顶层423子项真实专库race通过，校准W两项未用读权/PUBLIC重叠并保留失败记录；同步架构图、权限与未验边界、HLD/LLD/计划和README，生产模块/SQL/迁移与运行接线不变。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1826 个源文件；本次 15 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/procurement-request-birth.md`
- added `internal/procurementrequestbirth/request_birth_budget_postgres_test.go`
- added `internal/procurementrequestbirth/request_birth_grant_fault_postgres_test.go`
- added `internal/procurementrequestbirth/request_birth_raw_postgres_test.go`
- added `internal/procurementrequestbirth/request_birth_wait_postgres_test.go`
- added `specs/023-periodic-products/b-procurement-request-birth-budget-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-grant-fault-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-raw-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-wait-contract.md`
- modified `specs/023-periodic-products/b-procurement-request-birth-writer-contract.md`

</details>

## 2026-11-03T19:42:40Z · 1a783035ddc8

归档A1整包23/423真实PG、1826文件架构只读检查、make check及最终Go构建的实际结果；已审README/CLI/Compose/1Panel文档一致性，保持active预算/PB等待/未知提交/完整profile与生产未验边界，代码和SQL不再变更。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1826 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/procurement-request-birth.md`

</details>

## 2026-11-03T20:17:25Z · 617dbaae6034

A1新增父资格/PB真实等待、提前约束、提交回执丢失与全catalog漂移验收，整包32/528通过；审定A2完整装配及逐锁权限合同，同步README使用部署、架构/HLD/LLD/验证边界，原SQL和线上入口不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1835 个源文件；本次 16 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/procurement-request-birth.md`
- added `internal/procurementrequestbirth/request_birth_catalog_postgres_test.go`
- added `internal/procurementrequestbirth/request_birth_commit_unknown_postgres_test.go`
- added `internal/procurementrequestbirth/request_birth_parent_wait_postgres_test.go`
- added `internal/procurementrequestbirth/request_birth_pb_wait_postgres_test.go`
- added `specs/023-periodic-products/b-procurement-attempt-installation-contract.md`
- modified `specs/023-periodic-products/b-procurement-full-acl-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-catalog-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-commit-unknown-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-parent-wait-contract.md`
- added `specs/023-periodic-products/b-procurement-request-birth-pb-wait-contract.md`

</details>

## 2026-11-03T21:01:25Z · 41ca2d206187

实现独立A2采购出生候选：12表262列与38原扩列、NULL激活保持FK、38函数68触发器、精确ACL及test-only安装catalog；actual APP整链/PB导入/故障/争用限定race通过，同步统一模块图、managed时序、权限和未启用线上边界，更新README使用与候选状态

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1858 个源文件；本次 31 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/procurement-attempt-birth.md`
- added `internal/procurementattemptbirth/attempt_fault_postgres_test.go`
- added `internal/procurementattemptbirth/attempt_postgres_test.go`
- added `internal/procurementattemptbirth/attempt_wait_postgres_test.go`
- added `internal/procurementattemptbirth/catalog_postgres_test.go`
- added `internal/procurementattemptbirth/material_pins.go`
- added `internal/procurementattemptbirth/materials.go`
- added `internal/procurementattemptbirth/materials/a2-constraints-v1.sql`
- added `internal/procurementattemptbirth/materials/a2-original-columns-v1.sql`
- added `internal/procurementattemptbirth/materials/a2-tables-v1.sql`
- added `internal/procurementattemptbirth/materials/account-pure-v1.sql`
- added `internal/procurementattemptbirth/materials/account-writer-v1.sql`
- added `internal/procurementattemptbirth/materials/attempt-writer-v1.sql`
- added `internal/procurementattemptbirth/materials/authority-v1.sql`
- added `internal/procurementattemptbirth/materials/catalog-v1.json`
- added `internal/procurementattemptbirth/materials/guards-v1.sql`
- added `internal/procurementattemptbirth/materials/permissions-v1.sql`
- added `internal/procurementattemptbirth/materials/preflight-v1.sql`
- added `internal/procurementattemptbirth/materials/relation-descriptor-v1.json`
- added `internal/procurementattemptbirth/materials/request-capture-v1.sql`
- added `internal/procurementattemptbirth/materials/triggers-v1.sql`
- added `internal/procurementattemptbirth/materials/validators-v1.sql`
- added `internal/procurementattemptbirth/materials_test.go`
- modified `specs/023-periodic-products/b-procurement-attempt-installation-contract.md`
- modified `specs/023-periodic-products/b-procurement-birth-exact-link-integration-contract.md`

</details>

## 2026-11-03T21:03:59Z · be4ea4b994b7

汇合A2最终材料与验证：规范化SQL末尾空白并同步固定摘要，复核NULL激活FK/38函数68hook/权限边界；最终actual APP race12顶层25子项PASS，同步18.045s原件与架构/HLD/LLD/README，普通运行profile不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1858 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/procurement-attempt-birth.md`
- modified `internal/procurementattemptbirth/material_pins.go`
- modified `internal/procurementattemptbirth/materials/a2-constraints-v1.sql`
- modified `internal/procurementattemptbirth/materials/catalog-v1.json`
- modified `internal/procurementattemptbirth/materials/relation-descriptor-v1.json`
- modified `internal/procurementattemptbirth/materials/request-capture-v1.sql`

</details>

## 2026-11-03T22:03:34Z · 24c3d9996b4e

扩展A2规则/请求兼容、1339授权与110漂移验收并保留5项真实成功控制；47顶层1648子项PG/race及失败CI相关回归通过；CI同库包串行与14组完整分片、README使用部署和架构状态同步，生产模块/SQL/权限与001–022启动链路未变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1864 个源文件；本次 18 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `Makefile`
- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/runbooks/development.md`
- added `docs/verification/ci-package-isolation.md`
- modified `docs/verification/ci-profile-contract.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/procurement-attempt-birth-hardening.json`
- modified `docs/verification/procurement-attempt-birth.md`
- added `internal/procurementattemptbirth/attempt_grant_postgres_test.go`
- modified `internal/procurementattemptbirth/attempt_postgres_test.go`
- added `internal/procurementattemptbirth/attempt_rules_postgres_test.go`
- added `internal/procurementattemptbirth/catalog_drift_postgres_test.go`
- modified `internal/procurementattemptbirth/catalog_postgres_test.go`
- added `internal/procurementattemptbirth/request_compat_postgres_test.go`
- modified `specs/023-periodic-products/b-procurement-attempt-installation-contract.md`
- modified `web/README.md`

</details>

## 2026-11-04T01:00:34Z · 9c54bc31923d

A4新增真实绑定/PB首配双向同步、双租户共享采购硬界与精确PID/累计5秒期限验收；A5-S1新增无连接三根ABI/参数快照/列型与回执/PP cause协议叶子，S2-S4及普通运行接线仍未开放。同步HLD/LLD/交付计划、README、细图和真实失败/复验边界；审定旧9688929远端14组CI与明确跳过项，未改变SQL材料、角色或生产部署。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1880 个源文件；本次 24 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/verification/ci-package-isolation.md`
- added `docs/verification/github-ci-9688929.json`
- modified `docs/verification/procurement-attempt-birth.md`
- added `docs/verification/procurement-attempt-synchronization.json`
- added `docs/verification/procurement-attempt-synchronization.md`
- added `docs/verification/procurement-execution-protocol.json`
- added `docs/verification/procurement-execution-protocol.md`
- added `internal/procurementattemptbirth/attempt_deadline_postgres_test.go`
- added `internal/procurementattemptbirth/attempt_sync_postgres_test.go`
- added `internal/procurementexecution/errors.go`
- added `internal/procurementexecution/errors_test.go`
- added `internal/procurementexecution/query.go`
- added `internal/procurementexecution/query_test.go`
- added `internal/procurementexecution/receipt.go`
- added `internal/procurementexecution/receipt_test.go`
- modified `specs/023-periodic-products/b-procurement-attempt-birth-writer-contract.md`
- added `specs/023-periodic-products/b-procurement-attempt-fault-deadline-contract.md`
- added `specs/023-periodic-products/b-procurement-attempt-synchronization-contract.md`
- added `specs/023-periodic-products/b-procurement-prospective-executor-contract.md`

</details>

## 2026-11-04T02:54:12Z · 36603856b098

A5 S2固定三根事务Runner与S3显式业务/HTTP适配，确认COMMIT后发布与unknown不重放；同步候选模块/权限/14组只读观察设计和限定实际APP/wire验证；普通New/main/provision及001–022不变，完整A4/A5/PB异常域/R2继续开放

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1898 个源文件；本次 32 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/github-ci-43f3801.json`
- added `docs/verification/procurement-execution-runner.json`
- added `docs/verification/procurement-execution-runner.md`
- modified `internal/billing/evidence.go`
- added `internal/billing/prospective.go`
- added `internal/billing/prospective_test.go`
- modified `internal/billing/types.go`
- added `internal/procurement/prospective.go`
- added `internal/procurement/prospective_test.go`
- modified `internal/procurement/store.go`
- modified `internal/procurement/types.go`
- added `internal/procurementattemptbirth/executor_adapter_postgres_test.go`
- added `internal/procurementattemptbirth/executor_postgres_test.go`
- added `internal/procurementattemptbirth/executor_wire_test.go`
- modified `internal/procurementexecution/query.go`
- added `internal/procurementexecution/runner.go`
- added `internal/procurementexecution/runner_test.go`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/gateway.go`
- added `internal/server/procurement_execution_errors.go`
- added `internal/server/procurement_execution_errors_test.go`
- modified `internal/server/server.go`
- added `specs/023-periodic-products/b-procurement-executor-adapter-contract.md`
- added `specs/023-periodic-products/b-procurement-executor-observation-contract.md`
- added `specs/023-periodic-products/b-procurement-executor-transaction-acceptance-contract.md`
- added `specs/023-periodic-products/b-procurement-executor-transaction-contract.md`
- modified `specs/023-periodic-products/b-procurement-prospective-executor-contract.md`

</details>

## 2026-11-04T03:23:17Z · aec37a375a32

实现库级采购回执只读Observe及专项；同步来源/root/步骤/validator与RR只读清理边界、源码和验证。依据用户要求新增ADR-005，MVP采用现行单路径、冻结历史兼容候选，优先经营能力；README使用部署步骤经源码复核。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1913 个源文件；本次 24 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/procurement-attempt-birth.md`
- added `docs/decisions/ADR-005-pre-mvp-single-path.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/procurement-execution-observer.json`
- added `docs/verification/procurement-execution-observer.md`
- modified `docs/verification/procurement-execution-runner.md`
- added `internal/procurementattemptbirth/observer_postgres_test.go`
- added `internal/procurementattemptbirth/observer_wire_test.go`
- added `internal/procurementexecution/observe.go`
- added `internal/procurementexecution/observe_account.go`
- added `internal/procurementexecution/observe_account_test.go`
- added `internal/procurementexecution/observe_attempt.go`
- added `internal/procurementexecution/observe_attempt_test.go`
- added `internal/procurementexecution/observe_rows.go`
- added `internal/procurementexecution/observe_rows_test.go`
- added `internal/procurementexecution/observe_shared.go`
- added `internal/procurementexecution/observe_test.go`
- modified `specs/023-periodic-products/b-procurement-executor-observation-contract.md`
- added `specs/023-periodic-products/b-procurement-observer-acceptance-contract.md`

</details>

## 2026-11-04T03:47:24Z · 84281fd5dd4e

026现行经营报表纵向交付：新增只读reporting与平台JSON/CSV、控制台筛选和精确成本汇总；同步模块图/源码/权限/能力口径及HLD/LLD/SDD/README与专项边界；ADR-005用白话明确不维护未发布内部旧结构与旧格式组合，保留外部协议支持。已完成并行汇合和语义复核，无迁移或资金写入口变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1929 个源文件；本次 23 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- added `docs/architecture/operating-reports.md`
- modified `docs/decisions/ADR-005-pre-mvp-single-path.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/verification/operating-reports.json`
- added `docs/verification/operating-reports.md`
- added `internal/reporting/csv.go`
- added `internal/reporting/read.go`
- added `internal/reporting/read_postgres_test.go`
- added `internal/reporting/report.go`
- added `internal/reporting/report_test.go`
- added `internal/reporting/types.go`
- added `internal/server/operating_reports.go`
- added `internal/server/operating_reports_integration_test.go`
- added `internal/server/operating_reports_test.go`
- modified `internal/server/server.go`
- added `specs/026-operating-reports/spec.md`
- added `web/src/components/OperatingReport.vue`
- added `web/src/operating-report.ts`
- modified `web/src/views/ReportsView.vue`
- added `web/tests/operating-report.test.mjs`

</details>

## 2026-11-04T03:49:17Z · 71e2af35925d

026最终验证记录收口：专项PG/HTTP、299前端、浏览器筛选、Mermaid渲染、make check和CLI build已执行；仅更新证据与准确边界，复核模块/数据/权限/能力正文，无新增实现或架构结构变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1929 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/operating-reports.json`
- modified `docs/verification/operating-reports.md`

</details>

## 2026-11-04T05:28:27Z · 1299eb4ff3fb

027单一现行来源模型：正式023、四资金入口FIFO/退款、非现金停服开账、受限角色与双向核对、HTTP及控制台四来源摘要/余额/分配；通用DTO UTC规范化。026经营收入/全部attempt成本与CSV在023下复验。028补统一计量/媒体/任务/实时总体设计及D0缺口，仍未实现。冻结原001–022研究材料与历史helper身份修正、旧descriptor当前源码拒绝及覆盖缩减；不新增023 proof/profile或在线兼容依赖。已语义复核模块图、资金链路、数据权限、能力边界、源码与验证链接。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1960 个源文件；本次 74 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- added `cmd/moni-aigate/wallet_commands_test.go`
- added `docs/architecture/wallet-sources.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- added `docs/design/integrated-wallet-media.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/wallet-sources.json`
- added `docs/verification/wallet-sources.md`
- modified `internal/billing/funding.go`
- modified `internal/billing/funding_sandbox_integration_test.go`
- modified `internal/billing/funding_verify.go`
- added `internal/billing/lot_runtime.go`
- added `internal/billing/lot_runtime_integration_test.go`
- modified `internal/billing/orders.go`
- modified `internal/billing/reserve.go`
- modified `internal/billing/settle.go`
- modified `internal/billing/store.go`
- modified `internal/billing/voucher_query.go`
- modified `internal/billing/voucher_types.go`
- added `internal/billing/wallet_opening.go`
- added `internal/billing/wallet_opening_integration_test.go`
- added `internal/billing/wallet_opening_test.go`
- added `internal/billing/wallet_sources_application_test.go`
- added `internal/billing/wallet_sources_constraints_test.go`
- added `internal/billing/wallet_sources_read.go`
- added `internal/billing/wallet_sources_read_test.go`
- added `internal/billing/wallet_sources_summary_test.go`
- modified `internal/fundinglotbasis/basis_test.go`
- modified `internal/fundinglotruntime/runtime_history.go`
- modified `internal/fundinglotruntime/runtime_history_test.go`
- modified `internal/fundinglotruntime/runtime_materials.go`
- modified `internal/fundinglotruntime/runtime_materials_test.go`
- modified `internal/maintenancetrust/authority_manifest_test.go`
- modified `internal/maintenancetrust/funding_lots_observed_proof_test.go`
- modified `internal/maintenancetrust/funding_lots_proof_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/payment_roles_test.go`
- added `internal/operations/wallet_sources_roles_test.go`
- modified `internal/qa/c0candidate/fixture_test.go`
- modified `internal/server/commerce_handlers.go`
- modified `internal/server/queries.go`
- added `internal/server/queries_test.go`
- modified `internal/server/server.go`
- added `internal/server/wallet_sources.go`
- added `internal/server/wallet_sources_integration_test.go`
- added `migrations/023_wallet_sources.sql`
- added `migrations/wallet_sources_postgres_test.go`
- modified `scripts/funding-lot-descriptor/main_test.go`
- added `specs/027-wallet-sources/spec.md`
- added `specs/028-unified-metering-media/spec.md`
- modified `web/src/App.vue`
- modified `web/src/commerce.ts`
- modified `web/src/components/FundingPanel.vue`
- added `web/src/components/RequestFunding.vue`
- added `web/src/components/WalletSourceAudit.vue`
- added `web/src/components/WalletSources.vue`
- modified `web/src/format.ts`
- modified `web/src/router.ts`
- modified `web/src/views/ReportsView.vue`
- modified `web/src/views/RequestsView.vue`
- added `web/src/views/WalletSourcesView.vue`
- modified `web/src/views/WalletView.vue`
- added `web/src/wallet-source-details.ts`
- added `web/src/wallet-sources.ts`
- modified `web/tests/commerce.test.mjs`
- added `web/tests/wallet-source-details.test.mjs`
- added `web/tests/wallet-sources.test.mjs`

</details>

## 2026-11-04T06:00:57Z · 209b55a0670c

028-U共同纯域与共享strictjson实施，明确无serve调用边；同步计划/报价/分侧观察与只读描述、生产存储草案/消费者和阶段出口，026保留唯一收入及全部采购金额并规划原请求元数据，027远端CI未启动边界；正式DDL与媒体仍未实现

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1979 个源文件；本次 32 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/operating-reports.md`
- added `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/integrated-wallet-media.md`
- modified `docs/verification/mvp-status.md`
- added `docs/verification/unified-metering.json`
- added `docs/verification/unified-metering.md`
- modified `docs/verification/wallet-sources.md`
- added `internal/inference/inference_test.go`
- added `internal/inference/json.go`
- added `internal/inference/observation.go`
- added `internal/inference/pipeline_test.go`
- added `internal/inference/plan.go`
- added `internal/inference/profile.go`
- added `internal/inference/quote.go`
- added `internal/inference/types.go`
- added `internal/metering/describe.go`
- added `internal/metering/describe_test.go`
- modified `internal/metering/json.go`
- modified `internal/metering/parse.go`
- modified `internal/metering/types.go`
- added `internal/strictjson/parse.go`
- added `internal/strictjson/parse_test.go`
- modified `specs/024-extension-platform/b0-pure-contract.md`
- modified `specs/028-unified-metering-media/spec.md`
- added `specs/028-unified-metering-media/tasks.md`
- added `specs/028-unified-metering-media/u-consumer-inventory.md`
- added `specs/028-unified-metering-media/u-domain-contract.md`
- added `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-04T06:01:58Z · 4408153655c6

028-U纯域最终race、双fuzz与make check/构建证据收口；复核模块、当前无资金/媒体接线、026/027口径及未验边界，仅验证记录补充无新增运行权限

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1979 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.json`
- modified `docs/verification/unified-metering.md`

</details>

## 2026-11-04T13:10:35Z · 2a366dc184b8

新增C1-M本地退款模拟的独立协议输入、schema资源与命令/查询仓储骨架；复核确认尚未接入沙箱schema版本/catalog、终局fact控制、签名HTTP或钱包Q/F资金效果

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1987 个源文件；本次 9 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `internal/mockpsp/refund_input.go`
- added `internal/mockpsp/refund_input_test.go`
- added `internal/mockpsp/repo_refunds.go`
- modified `internal/mockpsp/types.go`
- added `internal/paymentrefundschema/c1m.sql`
- added `internal/paymentrefundschema/schema.go`
- added `internal/paymentrefundschema/schema_test.go`
- added `specs/022-self-service-payments/c1-m-mock-contract.md`
- added `specs/022-self-service-payments/c1-m-mock-schema.sql`

</details>

## 2026-11-04T13:19:33Z · 3b1f4393cc60

修正C1-M候选SQL：parameter_digest与fact_digest按C1-P domain-framing摘要校验，避免合法协议摘要与普通SHA-256 CHECK不一致；仍未接入运行schema/catalog或资金入口

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 1987 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `internal/paymentrefundschema/c1m.sql`
- modified `specs/022-self-service-payments/c1-m-mock-schema.sql`

</details>

## 2026-11-07T05:13:50Z · e0fe954dad51

028-U阶段1 6c–6e当前域收口：统一RatesWire读写、非零路由与绑定证据、能力/界/结算守卫、现行受限APP验证；完整商业链、供应商、部署和浏览器验收仍未完成

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2022 个源文件；本次 186 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `cmd/moni-aigate/main.go`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/alerts/source_integration_test.go`
- modified `internal/billing/batch_integration_test.go`
- modified `internal/billing/bounds_integration_test.go`
- added `internal/billing/cost_domain_integration_test.go`
- modified `internal/billing/cost_policy.go`
- modified `internal/billing/cost_policy_integration_test.go`
- modified `internal/billing/credential_pool_integration_test.go`
- modified `internal/billing/dispatch_batch_integration_test.go`
- modified `internal/billing/evidence.go`
- modified `internal/billing/funding_integration_test.go`
- modified `internal/billing/gemini_integration_test.go`
- modified `internal/billing/key_configuration_integration_test.go`
- modified `internal/billing/lot_runtime_integration_test.go`
- added `internal/billing/meter_snapshot_integration_test.go`
- modified `internal/billing/process_failure_integration_test.go`
- modified `internal/billing/procurement.go`
- modified `internal/billing/procurement_attempt_rules_postgres_test.go`
- modified `internal/billing/procurement_integration_test.go`
- modified `internal/billing/prospective.go`
- modified `internal/billing/prospective_test.go`
- modified `internal/billing/recovery.go`
- modified `internal/billing/reserve.go`
- modified `internal/billing/retail_price_integration_test.go`
- modified `internal/billing/retrieval_integration_test.go`
- modified `internal/billing/retrieval_legacy_integration_test.go`
- modified `internal/billing/route_decisions.go`
- modified `internal/billing/settle.go`
- modified `internal/billing/store.go`
- modified `internal/billing/store_test.go`
- modified `internal/billing/tenant_pricing_concurrency_integration_test.go`
- modified `internal/billing/tenant_pricing_integration_test.go`
- added `internal/billing/test_cost_helpers_test.go`
- modified `internal/billing/types.go`
- added `internal/billing/vector_actual.go`
- added `internal/billing/vector_actual_integration_test.go`
- added `internal/billing/vector_actual_test.go`
- added `internal/billing/vector_procurement.go`
- added `internal/billing/vector_procurement_integration_test.go`
- added `internal/billing/vector_procurement_test.go`
- added `internal/billing/vector_rates.go`
- added `internal/billing/vector_reserve_integration_test.go`
- modified `internal/billing/wallet_sources_application_test.go`
- modified `internal/billing/wallet_sources_read_test.go`
- added `internal/catalog/cost_domain_integration_test.go`
- modified `internal/catalog/cost_policy.go`
- modified `internal/catalog/cost_policy_integration_test.go`
- modified `internal/catalog/cost_policy_test.go`
- modified `internal/catalog/credential_pool_integration_test.go`
- added `internal/catalog/group_price_vector_integration_test.go`
- modified `internal/catalog/pricing_commands.go`
- modified `internal/catalog/pricing_concurrency_integration_test.go`
- modified `internal/catalog/pricing_integration_test.go`
- added `internal/catalog/pricing_publish.go`
- modified `internal/catalog/pricing_queries.go`
- modified `internal/catalog/pricing_types.go`
- added `internal/catalog/pricing_vector.go`
- added `internal/catalog/pricing_vector_test.go`
- modified `internal/catalog/public_catalog.go`
- modified `internal/catalog/public_catalog_integration_test.go`
- modified `internal/catalog/public_catalog_legacy_integration_test.go`
- modified `internal/catalog/public_catalog_test.go`
- modified `internal/catalog/store.go`
- modified `internal/identity/identity_integration_test.go`
- modified `internal/maintenancetrust/funding_lots_prefix_business_integration_test.go`
- modified `internal/maintenancetrust/funding_lots_runtime_procurement_integration_test.go`
- modified `internal/operations/key_policy_integration_test.go`
- modified `internal/operations/metadata_fixture_test.go`
- added `internal/operations/meter_roles.go`
- added `internal/operations/meter_roles_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/operations_integration_test.go`
- modified `internal/operations/pricing_roles_integration_test.go`
- modified `internal/operations/procurement_integration_test.go`
- modified `internal/payments/processor_safety_test.go`
- modified `internal/paymentsandboxqa/funding.go`
- modified `internal/paymentsandboxqa/install.go`
- modified `internal/periodicbudget/billing_dispatch_integration_test.go`
- modified `internal/periodicbudget/billing_integration_test.go`
- modified `internal/periodicbudget/billing_procurement_integration_test.go`
- modified `internal/periodicbudget/commander_integration_test.go`
- modified `internal/periodicbudget/commander_limits_test.go`
- modified `internal/periodicbudget/reader_integration_test.go`
- modified `internal/periodicbudget/tx_customer_test.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/periodicbudget/tx_supplier_test.go`
- modified `internal/periodicbudget/verifier_integration_test.go`
- modified `internal/pricing/pricing.go`
- modified `internal/pricing/pricing_integration_test.go`
- added `internal/pricing/vector_integration_test.go`
- modified `internal/procurement/store.go`
- modified `internal/procurementattemptbirth/attempt_postgres_test.go`
- modified `internal/procurementattemptbirth/attempt_rules_postgres_test.go`
- modified `internal/procurementattemptbirth/executor_adapter_postgres_test.go`
- modified `internal/procurementattemptbirth/observer_postgres_test.go`
- modified `internal/procurementattemptbirth/request_compat_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_budget_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_grant_fault_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_pb_wait_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_wait_postgres_test.go`
- modified `internal/reporting/csv.go`
- modified `internal/reporting/read.go`
- modified `internal/reporting/read_postgres_test.go`
- modified `internal/reporting/report_test.go`
- modified `internal/reporting/types.go`
- added `internal/routecost/quote.go`
- added `internal/routecost/quote_test.go`
- modified `internal/routing/cost.go`
- modified `internal/routing/cost_test.go`
- modified `internal/routing/decisions_test.go`
- modified `internal/routing/pool_test.go`
- modified `internal/routing/procurement.go`
- modified `internal/routing/procurement_test.go`
- modified `internal/routing/routing.go`
- modified `internal/routing/routing_test.go`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/binding_isolation_integration_test.go`
- modified `internal/server/catalog_handlers.go`
- modified `internal/server/cost_routing_integration_test.go`
- added `internal/server/cost_vector_application_integration_test.go`
- modified `internal/server/gateway.go`
- modified `internal/server/gateway_integration_test.go`
- modified `internal/server/gemini_integration_test.go`
- added `internal/server/group_price_vector_integration_test.go`
- modified `internal/server/main_shutdown_integration_test.go`
- modified `internal/server/native_protocols_integration_test.go`
- modified `internal/server/operating_reports.go`
- modified `internal/server/operating_reports_integration_test.go`
- modified `internal/server/periodic_budget_integration_test.go`
- modified `internal/server/pricing_handlers.go`
- modified `internal/server/pricing_integration_test.go`
- added `internal/server/pricing_publish_http_integration_test.go`
- modified `internal/server/probe.go`
- modified `internal/server/probe_integration_test.go`
- modified `internal/server/probe_pool_integration_test.go`
- modified `internal/server/procurement_integration_test.go`
- added `internal/server/procurement_meter_integration_test.go`
- modified `internal/server/public_catalog_integration_test.go`
- modified `internal/server/queries.go`
- modified `internal/server/reports.go`
- modified `internal/server/retrieval_integration_test.go`
- modified `internal/server/server.go`
- added `internal/server/test_cost_helpers_test.go`
- added `internal/testsupport/frozenattempt/attempt.go`
- added `internal/testsupport/frozenattempt/attempt_test.go`
- modified `internal/testsupport/funding019/verify_test.go`
- added `internal/testsupport/meterquote/group.go`
- added `internal/testsupport/meterquote/publish.go`
- added `internal/testsupport/meterquote/quote.go`
- added `migrations/024_unified_text_metering.sql`
- added `migrations/unified_text_metering_postgres_test.go`
- modified `scripts/backup-restore-drill.go`
- modified `scripts/browser-qa/model_lifecycle_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-consumer-inventory.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`
- modified `web/src/components/CostEditor.vue`
- modified `web/src/components/OperatingReport.vue`
- modified `web/src/components/PricingWriter.vue`
- modified `web/src/components/TenantPricing.vue`
- modified `web/src/cost-aware-routing.ts`
- modified `web/src/customer-pricing.ts`
- added `web/src/metering-wire.ts`
- modified `web/src/operating-report.ts`
- modified `web/src/pricing.ts`
- modified `web/src/procurement.ts`
- modified `web/src/public-catalog.ts`
- modified `web/src/views/ModelsView.vue`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/PriceGroupsView.vue`
- modified `web/src/views/PricingView.vue`
- modified `web/src/views/ProcurementView.vue`
- modified `web/src/views/RequestsView.vue`
- modified `web/tests/cost-aware-routing.test.mjs`
- added `web/tests/metering-wire.test.mjs`
- modified `web/tests/operating-report.test.mjs`
- modified `web/tests/pricing.test.mjs`
- modified `web/tests/procurement.test.mjs`
- modified `web/tests/public-catalog-state.test.mjs`
- modified `web/tests/public-catalog.test.mjs`

</details>

## 2026-11-07T06:55:47Z · 561f97f5a253

028-U route complete 结构守卫：空 route、错误 channel/version 与无有效 credential 的 binding 在提交时拒绝；route/binding/cap/active pointer 原子出生，55439 migrations/catalog/billing/server/operations 回归通过

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2022 个源文件；本次 9 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/store_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/unified_text_metering_postgres_test.go`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-07T07:05:37Z · e5f108ec127e

补充 route complete 错 channel/version 负控后的语义复核：空 route 与 supplier contract 脱离的 binding 均在 COMMIT 拒绝；当前PG专项复跑通过

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2022 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `migrations/unified_text_metering_postgres_test.go`

</details>

## 2026-11-07T10:42:46Z · a287b583ff79

028-U受限APP默认/组价六协议组合与退款来源专项验证，仍保留完整guard和商业出口未验收边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2022 个源文件；本次 4 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/server/cost_vector_application_integration_test.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10 · current MVP verification entry

新增 `make verify-current-mvp` 和显式当前专项脚本，固定使用 55439 `moni_test`，逐组输出 current-only 数据库、028-U、账务、六文本、媒体/Realtime、provider、前端、vet 与 Compose smoke 的实际结果和耗时。入口不调用整仓历史 maintenance fixture；初始演练中 timeout wiring 在 provider 合入前失败、Compose task smoke 有一次瞬时 503，均保留为失败证据，主线合入 provider 后会重新执行。

## 2026-11-07T14:32:45Z · 0c5250705416

028-U受限APP默认/组价、退款来源和恢复不重生成续片，保持完整guard与商业出口未验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2022 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/server/cost_vector_application_integration_test.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-07T15:00:08Z · 5e24c625cae9

新增001–028整体交付盘点，修正026元数据消费者清单过期描述；确认主链合理并划分互斥并行写集合

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2023 个源文件；本次 2 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `docs/verification/overall-delivery-inventory.md`
- modified `specs/028-unified-metering-media/u-consumer-inventory.md`

</details>

## 2026-11-07T15:57:21Z · 42cf10fd1e97

完成001–028整体盘点，补充028-U角色/纯域/六协议商业反例证据并同步未完成边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2024 个源文件；本次 9 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/metering/metering_test.go`
- modified `internal/operations/meter_roles_test.go`
- added `internal/server/parallel_commercial_gap_test.go`
- modified `internal/strictjson/parse_test.go`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `web/tests/operating-report.test.mjs`

</details>

## 2026-11-07T18:11:34Z · 2e3492334f47

核对两个工作目录并汇合028拒迁负控、021邮件故障窗口、022-C1-M隔离退款模拟证据

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2029 个源文件；本次 16 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/overall-delivery-inventory.md`
- added `docs/verification/payment-refund-c1-m.md`
- modified `docs/verification/self-service-identity.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/dispatch_batch_integration_test.go`
- added `internal/mailworker/worker_process_integration_test.go`
- modified `internal/mockpsp/input.go`
- added `internal/mockpsp/refund_controls.go`
- added `internal/mockpsp/refund_integration_test.go`
- modified `internal/mockpsp/repo_controls.go`
- modified `internal/mockpsp/repo_refunds.go`
- modified `migrations/024_unified_text_metering.sql`
- added `migrations/unified_text_metering_rejection_postgres_test.go`
- modified `specs/022-self-service-payments/c1-m-mock-schema.sql`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-07T19:04:59Z · 826ff1f54558

收口008 journal纯核、024 route bypass removal、028 S/A/R纯域候选合同与状态机，生产媒体仍未接线

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2049 个源文件；本次 30 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/028-media-pure-candidates.md`
- added `docs/verification/financial-write-batches.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- added `internal/billing/journal_batch.go`
- added `internal/billing/journal_batch_test.go`
- modified `internal/billing/store.go`
- added `internal/media/input.go`
- added `internal/media/json.go`
- added `internal/media/media_test.go`
- added `internal/media/profile.go`
- added `internal/media/result.go`
- added `internal/media/state.go`
- added `internal/media/types.go`
- added `internal/realtime/realtime.go`
- added `internal/realtime/realtime_test.go`
- added `internal/tasks/event.go`
- added `internal/tasks/state.go`
- added `internal/tasks/tasks_test.go`
- added `internal/tasks/types.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/unified_text_metering_postgres_test.go`
- modified `specs/008-financial-write-batches/acceptance.md`
- added `specs/028-unified-metering-media/a-contract.md`
- added `specs/028-unified-metering-media/r-contract.md`
- added `specs/028-unified-metering-media/s-contract.md`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-07T19:14:47Z · 9f8f27c9a92b

补强008 journal写集拒绝同一ledger account重复kind并同步验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2049 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/financial-write-batches.md`
- modified `internal/billing/journal_batch.go`
- modified `internal/billing/journal_batch_test.go`
- modified `specs/008-financial-write-batches/spec.md`
- modified `specs/028-unified-metering-media/r-contract.md`

</details>

## 2026-11-07T19:45:32Z · 7930b2761181

补齐008 finish尾写点故障回滚与幂等重试专项及验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2050 个源文件；本次 4 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/financial-write-batches.md`
- added `internal/billing/finish_tail_integration_test.go`
- modified `specs/008-financial-write-batches/acceptance.md`
- modified `specs/008-financial-write-batches/spec.md`

</details>

## 2026-11-08T01:24:14Z · 559573b31708

028-U人工核对改为绑定原请求schema的完整retail数量向量，新增proposal canonical/digest guard并移除HTTP旧标量入口

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2053 个源文件；本次 21 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/evidence.go`
- added `internal/billing/reconciliation_vector.go`
- added `internal/billing/reconciliation_vector_test.go`
- modified `internal/billing/recovery.go`
- modified `internal/billing/store_test.go`
- modified `internal/billing/types.go`
- modified `internal/billing/vector_actual_integration_test.go`
- modified `internal/metering/describe.go`
- modified `internal/metering/parse.go`
- modified `internal/metering/types.go`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/parallel_commercial_gap_test.go`
- added `internal/server/reconciliation_input_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`
- modified `web/src/views/PlatformView.vue`

</details>

## 2026-11-08T01:25:57Z · 7c764445cdde

028-U人工核对proposal列明确保存绑定请求的retail QuantitiesWire及纯核摘要，批准时才生成approved_reconciliation Observation；HTTP release/settle字段边界已收紧

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2053 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T02:53:17Z · 36b727233c3e

008 funding-lot写批次与028-U route cap完整性guard及对应专项/设计记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2053 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/financial-write-batches.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/lot_plan_test.go`
- modified `internal/billing/lot_runtime.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/unified_text_metering_postgres_test.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T04:10:31Z · ab1c44bf7ff6

008 profile block与周期预算超界原子批次、028-U六协议unknown恢复/幂等收费矩阵及冻结历史fixture边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2054 个源文件；本次 12 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/financial-write-batches.md`
- modified `docs/verification/unified-metering.md`
- added `internal/billing/profile_block_batch_integration_test.go`
- modified `internal/billing/settle.go`
- modified `internal/periodicbudget/billing_integration_test.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/server/cost_vector_application_integration_test.go`
- modified `specs/008-financial-write-batches/acceptance.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T04:54:33Z · 58691a000795

收口024成本来源互斥guard、现行周期预算完整route/catalog fixture与008/028-U验证记录；冻结历史fixture未升级；已完成语义复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2054 个源文件；本次 13 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/financial-write-batches.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/store_test.go`
- modified `internal/periodicbudget/billing_dispatch_integration_test.go`
- modified `internal/periodicbudget/billing_procurement_integration_test.go`
- modified `internal/periodicbudget/billing_recovery_integration_test.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/periodicbudget/tx_supplier_test.go`
- modified `internal/periodicbudget/verifier_integration_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `specs/008-financial-write-batches/acceptance.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T05:01:17Z · 76c277fdcbc9

补充008周期预算全包race与SIGKILL recovery验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2054 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/financial-write-batches.md`
- modified `specs/008-financial-write-batches/acceptance.md`

</details>

## 2026-11-08T05:29:04Z · 70d3bbcf0cf4

028-U请求报价身份guard、人工核对HTTP动作映射、008固定binary运行器与现行server夹具收口

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2056 个源文件；本次 11 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/financial-write-batches.md`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/cost_routing_integration_test.go`
- modified `internal/server/reconciliation_input_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/unified_text_metering_postgres_test.go`
- added `scripts/financial-write-batch-qa/main.go`
- added `scripts/financial-write-batch-qa/main_test.go`
- modified `specs/008-financial-write-batches/acceptance.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T06:16:23Z · 700e4f1ffee0

028-S/A/R服务接口、当前存储表、状态guard与媒体运行时权限接入

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2068 个源文件；本次 20 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- added `internal/media/service.go`
- added `internal/media/service_test.go`
- modified `internal/operations/meter_roles.go`
- modified `internal/operations/meter_roles_test.go`
- modified `internal/operations/operations.go`
- added `internal/provider/media_adapter.go`
- added `internal/provider/realtime_adapter.go`
- added `internal/provider/task_adapter.go`
- added `internal/realtime/service.go`
- added `internal/realtime/service_test.go`
- added `internal/realtime/store.go`
- added `internal/tasks/postgres_store.go`
- added `internal/tasks/service.go`
- added `internal/tasks/service_test.go`
- modified `internal/tasks/state.go`
- added `internal/tasks/store.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T06:37:18Z · 744e8453bb5a

新增同步媒体mock provider并复核当前MVP边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2069 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- added `internal/provider/mock_media.go`

</details>

## 2026-11-08T06:48:26Z · f479c6905145

接入video_generation任务HTTP入口与当前任务状态机

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2071 个源文件；本次 5 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- added `internal/provider/mock_task.go`
- modified `internal/server/server.go`
- added `internal/server/task_handlers.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T06:49:39Z · 02afd7cfe7ab

复用单进程mock task adapter保证任务提交与观察状态连续

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2071 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `internal/server/server.go`
- modified `internal/server/task_handlers.go`

</details>

## 2026-11-08T06:51:44Z · 9f1ceaf6ec3c

任务HTTP改用标准Idempotency-Key并保持请求摘要不可变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2071 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `internal/server/task_handlers.go`

</details>

## 2026-11-08T06:54:59Z · 31683400be58

补齐video_generation结果授权与开发环境结果交付路径

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2071 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `internal/server/server.go`
- modified `internal/server/task_handlers.go`
- modified `internal/tasks/postgres_store.go`

</details>

## 2026-11-08T07:02:26Z · 0dda8d34c856

补齐media_tasks持久租约与tasks worker状态机

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2072 个源文件；本次 5 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `internal/tasks/postgres_store.go`
- added `internal/tasks/worker.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T07:12:28Z · d5659b01d061

新增moni-aigate worker命令与Compose独立worker服务

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2072 个源文件；本次 4 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `cmd/moni-aigate/main.go`
- modified `compose.yaml`
- modified `docs/runbooks/deployment.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T07:20:30Z · 25c3b7570e89

补充task worker lease与开发环境worker回归测试

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2073 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- added `internal/tasks/worker_test.go`

</details>

## 2026-11-08T08:01:12Z · 253053e741dd

新增限定同步图片 HTTP MVP，复核 server→media→provider mock 的调用链与内存幂等/结果授权边界，未接入持久化、计量、账务、wallet、settlement、tasks、realtime、migrations 或 compose

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2076 个源文件；本次 4 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `docs/design/image-http-mvp.md`
- added `internal/server/image_handlers.go`
- added `internal/server/image_handlers_test.go`
- modified `internal/server/server.go`

</details>

## 2026-11-08T10:58:00Z · fcb3b10c7c56

完成当前媒体 profile、media wallet Reserve Attempt Observe Settle 入口、图片/TTS/Realtime 账务接线、任务状态出口和 payment wallet validator 基线复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2087 个源文件；本次 32 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `Dockerfile`
- modified `README.md`
- modified `compose.yaml`
- modified `deploy/init-roles.sh`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/integrated-wallet-media.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/unified-metering.md`
- added `internal/billing/media.go`
- added `internal/billing/media_integration_test.go`
- modified `internal/operations/meter_roles.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/payment_roles_test.go`
- added `internal/provider/mock_realtime.go`
- added `internal/provider/realtime_test.go`
- modified `internal/server/image_handlers.go`
- added `internal/server/image_handlers_integration_test.go`
- added `internal/server/media_billing.go`
- added `internal/server/media_handlers.go`
- added `internal/server/realtime.go`
- added `internal/server/realtime_test.go`
- modified `internal/server/server.go`
- modified `internal/server/task_handlers.go`
- added `internal/server/tts_handlers_test.go`
- modified `migrations/024_unified_text_metering.sql`
- added `scripts/deployment-qa.sh`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T11:36:34Z · b2f67bb3c205

补齐历史 schema 条件权限、payment wallet dispatcher 当前 024 审查基线并完成媒体专项后最终语义复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2087 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `internal/operations/meter_roles.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/wallet_sources_roles_test.go`

</details>

## 2026-11-08T12:05:29Z · c6de349f62d0

新增 reporting 对当前 media_requests、media_settlements、media_attempts 的只读接缝；收入按请求唯一 settlement，媒体 attempt 成本未知且不使用 raw_amount，更新架构与验证说明；未修改 server、task、realtime、billing/media.go 或 migrations

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2090 个源文件；本次 21 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/operating-reports.md`
- modified `docs/verification/operating-reports.md`
- modified `internal/operations/meter_roles.go`
- modified `internal/operations/meter_roles_test.go`
- modified `internal/operations/operations.go`
- modified `internal/reporting/read.go`
- modified `internal/reporting/read_postgres_test.go`
- modified `internal/reporting/report.go`
- modified `internal/reporting/report_test.go`
- modified `internal/reporting/types.go`
- modified `internal/server/image_handlers.go`
- modified `internal/server/image_handlers_integration_test.go`
- added `internal/server/media_artifacts.go`
- modified `internal/server/media_handlers.go`
- modified `internal/server/task_handlers.go`
- added `internal/server/task_media_billing.go`
- added `internal/server/task_media_billing_integration_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `specs/028-unified-metering-media/u-consumer-inventory.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T16:04:08Z · eb145cf97303

当前 MVP 媒体结果持久化、异步 video 统一账务、Realtime PostgreSQL 持久化、媒体报表只读接缝、任务事件与角色边界修正；保留真实供应商、容器重启、浏览器和完整 028/S/A/R 未验收边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2092 个源文件；本次 19 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/integrated-wallet-media.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/provider/mock_task.go`
- modified `internal/server/realtime.go`
- added `internal/server/realtime_persistence.go`
- added `internal/server/realtime_persistence_test.go`
- modified `internal/server/server.go`
- modified `internal/server/task_handlers.go`
- modified `internal/server/task_media_billing.go`
- modified `internal/server/task_media_billing_integration_test.go`
- modified `internal/tasks/postgres_store.go`
- modified `internal/tasks/service.go`
- modified `internal/tasks/worker.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T16:47:21Z · 9bc80beec7e2

异步任务制品正文一次性持久化、Store/短期 grant 跨进程读取、deterministic mock worker/provider 重启观察不重复提交；更新任务合同、架构与验证记录；真实供应商、对象存储和容器重启仍未验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2092 个源文件；本次 13 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/integrated-wallet-media.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/provider/mock_task.go`
- modified `internal/server/task_media_billing_integration_test.go`
- modified `internal/tasks/postgres_store.go`
- modified `internal/tasks/service.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T17:34:03Z · 38253cd78a56

补齐 Realtime provider identity 持久化、异步制品正文重启读取和本机 Compose QA 的当前状态，校正历史中间状态；未扩大旧研究覆盖

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2093 个源文件；本次 14 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/provider/mock_realtime.go`
- added `internal/server/realtime_http_postgres_integration_test.go`
- modified `internal/server/realtime_persistence.go`
- modified `internal/server/realtime_persistence_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `scripts/deployment-qa.sh`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T17:44:22Z · e79518950509

补齐 Realtime HTTP/WS media-backed turn 真实商业绑定、UUID event 合同与部署业务 smoke 边界；校正 028-S/A/R 中间状态文档，不扩大 U/历史研究覆盖

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2094 个源文件；本次 8 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `internal/server/media_billing.go`
- modified `internal/server/realtime.go`
- modified `internal/server/realtime_http_postgres_integration_test.go`
- modified `migrations/024_unified_text_metering.sql`
- added `scripts/deployment-business-qa.sh`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-08T17:46:01Z · 7474d19691fb

收紧 Realtime turn 文本/media 来源类型不可混接，并完成 55439 migration/HTTP-WebSocket 重启回归与部署 smoke 脚本安全清理复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2094 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `migrations/024_unified_text_metering.sql`

</details>

## 2026-11-08T18:03:21Z · ea1608374ee3

补充 55439 Realtime 文本/media commercial fact source-type 混接回滚反例，并同步 U、架构、HLD/LLD 验证边界；未扩大 U guard matrix 或部署外部能力

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2095 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- added `internal/server/realtime_binding_guard_integration_test.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-08T18:09:36Z · eee0d43e32d8

补齐 owner-scoped 请求详情的 task 原 profile/reservation/attempt 与资金 source 可发现性，并通过 55439 HTTP/Go/前端构建回归；未伪造任务商业事实

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2096 个源文件；本次 8 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/server/billing_handlers.go`
- added `internal/server/task_request_reference_integration_test.go`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `web/src/views/RequestsView.vue`

</details>

## 2026-11-09T01:44:45Z · 95bf5b60c02c

028-U与028媒体当前MVP收口：补55439 guard矩阵、任务引用HTTP-first验收、严格部署业务smoke与任务适配器无mock回退；同步当前架构/验证/部署文档。保留完整U guard、真实供应商、浏览器、1Panel、S/A/R外部出口未完成边界。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2099 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/server/task_handlers.go`
- modified `internal/server/task_media_billing_integration_test.go`
- added `migrations/unified_text_metering_guard_matrix_postgres_test.go`
- added `scripts/deployment-business-qa-ws.py`
- modified `scripts/deployment-business-qa.sh`
- added `scripts/deployment-business-qa.test.sh`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T05:47:12Z · e4dd618078dd

028-U媒体权限、任务锁目标、Compose五条业务链和部署验收记录已完成语义复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2101 个源文件；本次 12 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `compose.yaml`
- modified `docs/runbooks/deployment.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/media.go`
- modified `internal/operations/meter_roles.go`
- modified `internal/tasks/postgres_store.go`
- added `scripts/deployment-business-provision.sh`
- modified `scripts/deployment-business-qa.sh`
- modified `scripts/deployment-business-qa.test.sh`
- added `scripts/deployment-qa-provider.py`
- modified `scripts/deployment-qa.sh`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T05:55:04Z · 0c2f719baa2a

028-U当前媒体、任务、Realtime、Compose业务验收及架构文档已再次语义复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2101 个源文件；本次 4 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T07:20:13Z · c6cfbe22bdd9

调整 make verify-mvp 包级超时预算至1200秒并同步开发运行手册；无运行架构变化，仅修正完整回归验收时间边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2101 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `Makefile`
- modified `docs/runbooks/development.md`

</details>

## 2026-11-09T12:23:24Z · 605917e8efe0

切换当前服务到单一 current_mvp.sql 空库基线并补齐 VerifyCurrent 运行入口；TTS provider usage/WAV 校验、管理端 token 一次性向量转换、路由凭据身份收口、媒体重启验收和相关设计验证同步更新

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2104 个源文件；本次 61 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- modified `cmd/moni-aigate/periodic_verify.go`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/image-http-mvp.md`
- modified `docs/design/integrated-wallet-media.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/cost_policy_integration_test.go`
- modified `internal/billing/reserve.go`
- modified `internal/billing/retail_price_integration_test.go`
- modified `internal/catalog/cost_policy_integration_test.go`
- modified `internal/catalog/pricing_commands.go`
- modified `internal/catalog/pricing_concurrency_integration_test.go`
- modified `internal/catalog/pricing_publish.go`
- modified `internal/catalog/pricing_vector.go`
- modified `internal/catalog/pricing_vector_test.go`
- modified `internal/inference/plan.go`
- modified `internal/metering/describe_test.go`
- modified `internal/metering/fuzz_test.go`
- deleted `internal/metering/legacy.go`
- added `internal/metering/management_token.go`
- modified `internal/metering/metering_test.go`
- modified `internal/payments/runtime_check.go`
- modified `internal/payments/runtime_integration_test.go`
- modified `internal/payments/service_fixture_test.go`
- modified `internal/paymentsandboxqa/application.go`
- modified `internal/paymentsandboxqa/application_test.go`
- modified `internal/paymentsandboxqa/funding.go`
- modified `internal/paymentsandboxqa/install.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/periodicbudget/verifier.go`
- modified `internal/periodicbudget/verifier_test.go`
- modified `internal/pricing/vector_integration_test.go`
- modified `internal/provider/mock_media.go`
- modified `internal/routecost/quote_test.go`
- modified `internal/routing/cost_test.go`
- modified `internal/routing/pool_test.go`
- modified `internal/routing/procurement.go`
- modified `internal/routing/routing.go`
- modified `internal/routing/routing_test.go`
- modified `internal/server/image_handlers.go`
- modified `internal/server/image_handlers_integration_test.go`
- modified `internal/server/image_handlers_test.go`
- modified `internal/server/media_handlers.go`
- modified `internal/server/server.go`
- modified `internal/server/tts_handlers_test.go`
- added `internal/server/tts_wav.go`
- modified `internal/testsupport/meterquote/publish.go`
- added `migrations/current.go`
- added `migrations/current_mvp.sql`
- modified `migrations/embed.go`
- modified `migrations/embed_test.go`
- modified `migrations/unified_text_metering_postgres_test.go`
- modified `migrations/verify_readonly.go`
- modified `scripts/backup-restore-drill.go`
- modified `scripts/browser-qa/main.go`
- modified `scripts/deployment-qa.sh`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T13:08:04Z · 55a40989daaf

补齐实际 main/CLI 当前基线夹具与历史 scanner source pin 审查，保持历史 fixture 和当前运行入口分离

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2104 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `cmd/moni-aigate/periodic_verify_integration_test.go`
- modified `internal/fundinglotcatalog/variants_fixture_test.go`
- modified `internal/server/alert_main_integration_test.go`
- modified `internal/server/main_shutdown_integration_test.go`
- modified `specs/022-self-service-payments/c0-wallet-readset.json`

</details>

## 2026-11-09T16:24:51Z · 682d10557dfc

补齐采购 evidence identity 拒迁、028-U guard/实际 LOGIN 权限负控和前端媒体报表向量解析，复核当前 Profile-Plan-Reserve-Attempt-Observe-Settle 链路；完整九组 guard、正式供应商和目标环境仍未验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 23 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- added `internal/operations/meter_roles_login_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`
- added `migrations/unified_text_metering_guard_gaps_postgres_test.go`
- modified `migrations/unified_text_metering_rejection_postgres_test.go`
- modified `specs/008-financial-write-batches/acceptance.md`
- modified `specs/008-financial-write-batches/spec.md`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `web/src/components/OperatingReport.vue`
- modified `web/src/metering-wire.ts`
- modified `web/src/operating-report.ts`
- modified `web/src/reporting.ts`
- added `web/src/request-wire.ts`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/ReportsView.vue`
- modified `web/src/views/RequestsView.vue`
- modified `web/tests/metering-wire.test.mjs`
- modified `web/tests/operating-report.test.mjs`
- modified `web/tests/reporting.test.mjs`
- added `web/tests/request-wire.test.mjs`

</details>

## 2026-11-09T17:11:58Z · d608154f72dd

记录 2026-11-10 更新后 Compose 单机五条业务链与 app/worker/PostgreSQL 重启 smoke；mock provider 边界保持，完整 guard/供应商/生产环境仍未验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T17:14:07Z · b91c9e632a7a

澄清经营报表媒体模型筛选输入说明；无数据流或权限结构变化，当前向量报表链路复核通过

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `web/src/components/OperatingReport.vue`

</details>

## 2026-11-09T17:16:32Z · c0c884facf38

更新 028-S/A/R 与当前 Compose MVP 完成矩阵，区分 mock 内测已验能力和真实供应商/图片编辑/STT/1Panel/HA/性能后置项

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T17:17:17Z · 5223d56d95e3

确认 55439 mock APP 六文本商业矩阵已通过，并把真实 supplier late/conflict、独立浏览器和生产 LOGIN 商业认证明确后置

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T17:17:56Z · 4c6a00e8980f

收口当前 MVP 向量 provider/catalog/routing/billing/procurement/HTTP/UI 单一路径状态，独立 SQL guard 和正式环境仍分项验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T17:36:54Z · c5c77a2c96e6

补齐 028-U 四个 trigger-only 完整性函数的 SECURITY INVOKER、固定 search_path 和最小权限，并通过实际 LOGIN 断言

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `internal/operations/meter_roles_login_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T17:54:30Z · c1120fefe3ba

补齐全部 meter_% 函数的 SECURITY INVOKER、固定 search_path、PUBLIC 执行撤销和实际 LOGIN 角色定义核验，并同步028-U验证边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 7 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/operations/meter_roles.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T17:59:34Z · 4f40bdcbdd18

校正001–028整体盘点与MVP状态中的当前媒体接线、Compose证据和028-U未完成边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/mvp-status.md`
- modified `docs/verification/overall-delivery-inventory.md`

</details>

## 2026-11-09T22:57:41Z · e597626fadeb

记录权限收口后的整仓回归抖动与两个专项重跑结果，并校正001–028整体状态

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-09T23:20:04Z · 511aa11df4ef

补齐当前媒体 retail/supplier cost snapshot、唯一 settlement guard、reporting 成本读取，并汇总运行时 legacy 路径审计

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2108 个源文件；本次 11 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/media.go`
- modified `internal/billing/media_integration_test.go`
- modified `internal/reporting/read.go`
- modified `internal/reporting/read_postgres_test.go`
- modified `internal/reporting/report.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-09T23:33:48Z · 7d7256e75f0e

补充028-U拒绝矩阵后，复核媒体supplier cost从不可变结算快照读取，并校正README、报表设计、验证记录和消费者清单；无运行时模块边界变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 7 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/operating-reports.md`
- modified `docs/verification/operating-reports.md`
- modified `docs/verification/unified-metering.md`
- added `migrations/unified_text_metering_guard_extension_postgres_test.go`
- added `migrations/unified_text_metering_rejection_matrix_postgres_test.go`
- modified `specs/028-unified-metering-media/u-consumer-inventory.md`

</details>

## 2026-11-10T00:04:41Z · 6710649da736

补充028-U拒绝矩阵和媒体supplier cost报表口径后的最终架构语义复核；仅同步当前状态、验证记录与任务边界，无新增运行时路径

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T00:06:45Z · b9de1ca4cc83

补充028-U拒绝矩阵和定向回归的实际命令、结果及整包advisory lock竞争超时边界；不改变运行时模块和覆盖声明

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`

</details>

## 2026-11-10T00:20:44Z · ec6a0eae6e57

按ADR-005将历史维护raw reader、ProspectiveStore和维护扫描从默认当前构建图隔离；复核默认cmd依赖、当前billing、历史tag编译、默认build/vet/空测试编译及55439定向回归

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 54 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/decisions/ADR-005-pre-mvp-single-path.md`
- modified `docs/design/LLD.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/funding_financial_read.go`
- modified `internal/billing/funding_financial_read_test.go`
- modified `internal/billing/funding_ledger_facts.go`
- modified `internal/billing/funding_ledger_maintenance_test.go`
- modified `internal/billing/funding_ledger_query.go`
- modified `internal/billing/funding_ledger_read.go`
- modified `internal/billing/funding_ledger_read_integration_test.go`
- modified `internal/billing/funding_ledger_read_test.go`
- modified `internal/billing/funding_source_capture_facts.go`
- modified `internal/billing/funding_source_capture_read.go`
- modified `internal/billing/funding_source_facts.go`
- modified `internal/billing/funding_source_read.go`
- modified `internal/billing/funding_source_read_test.go`
- modified `internal/billing/procurement_approval_read.go`
- modified `internal/billing/procurement_approval_read_test.go`
- modified `internal/billing/procurement_attempt_facts.go`
- modified `internal/billing/procurement_attempt_read.go`
- modified `internal/billing/procurement_attempt_read_test.go`
- modified `internal/billing/procurement_budget_facts.go`
- modified `internal/billing/procurement_budget_read.go`
- modified `internal/billing/procurement_budget_read_test.go`
- modified `internal/billing/procurement_command_read.go`
- modified `internal/billing/prospective.go`
- modified `internal/billing/prospective_test.go`
- modified `internal/billing/retrieval_legacy_integration_test.go`
- modified `internal/maintenancescan/complete_sources.go`
- modified `internal/maintenancescan/complete_sources_test.go`
- modified `internal/maintenancescan/funding_financial.go`
- modified `internal/maintenancescan/funding_financial_test.go`
- modified `internal/maintenancescan/funding_sources.go`
- modified `internal/maintenancescan/funding_sources_test.go`
- modified `internal/maintenancescan/ledger.go`
- modified `internal/maintenancescan/ledger_test.go`
- modified `internal/maintenancescan/periodic_budget.go`
- modified `internal/maintenancescan/periodic_budget_test.go`
- modified `internal/maintenancescan/periodic_prefix.go`
- modified `internal/maintenancescan/periodic_prefix_test.go`
- modified `internal/maintenancescan/procurement.go`
- modified `internal/maintenancescan/procurement_test.go`
- modified `internal/maintenancescan/sandbox_capture_links.go`
- modified `internal/maintenancescan/sandbox_capture_links_test.go`
- modified `internal/maintenancescan/source_wallet.go`
- modified `internal/maintenancescan/source_wallet_test.go`
- modified `internal/procurementattemptbirth/executor_adapter_postgres_test.go`
- modified `scripts/maintenance-scan-qa/complete_source_edges_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- modified `scripts/maintenance-scan-qa/periodic_prefix_integration_test.go`
- modified `scripts/maintenance-scan-qa/periodic_resources_test.go`
- modified `scripts/maintenance-scan-qa/source_wallet_integration_test.go`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T00:31:47Z · 706305ce6ec6

补齐024/current MVP在第一条业务DDL前的schema/relation owner与ACL形状拒迁，并记录021/022 payment ACL合法例外；复核迁移、权限、验证和当前构建图

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 7 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`
- modified `migrations/unified_text_metering_rejection_matrix_postgres_test.go`
- modified `specs/028-unified-metering-media/tasks.md`
- modified `specs/028-unified-metering-media/u-storage-contract.md`

</details>

## 2026-11-10T02:29:05Z · f3ee729d3939

收口当前 wallet funding 校验器身份：024/current_mvp 创建 payment_funding_journal_valid、替换当前 funding dispatcher、删除旧函数并同步角色 ACL、架构正文和验证记录；不改变 021–023 历史材料与资金事实

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 8 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/decisions/ADR-005-pre-mvp-single-path.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/payment_roles_test.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T02:59:20Z · b1ddde0461fc

028-U 角色权限闭包新增表级 TRUNCATE、REFERENCES、TRIGGER PostgreSQL 负控；无运行时数据关系、请求链路或权限策略变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `migrations/unified_text_metering_guard_gaps_postgres_test.go`

</details>

## 2026-11-10T03:46:42Z · 4234a5f102a8

集成028-U wallet funding 校验器当前身份收口、保留 wallet_opening 分支、补计量角色写权限负控，并修复 Compose 新卷 public schema owner 与当前迁移身份门禁的一致性；同步部署手册和验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `deploy/init-roles.sh`
- modified `docs/runbooks/deployment.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/operations/payment_roles_functions.go`
- modified `migrations/024_unified_text_metering.sql`
- modified `migrations/current_mvp.sql`

</details>

## 2026-11-10T04:52:32Z · 8ca1e60c8996

修复实际主进程 PostgreSQL 临时库 fixture 与 Compose 新卷 public schema owner 初始化边界的一致性；alert-main 和 shutdown/restart 专项通过，模块图、迁移拒绝和权限语义不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 3 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `internal/server/alert_main_integration_test.go`
- modified `internal/server/main_shutdown_integration_test.go`

</details>

## 2026-11-10T04:55:50Z · f2b6c962c5e0

记录最终主线 Compose 五条业务链与重启恢复 smoke 复跑结果；仍明确 deterministic mock provider、本机内测和后置真实供应商/生产部署边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`

</details>

## 2026-11-10T06:49:47Z · cc40d0f9b6c1

重建 current-only 空库迁移基线；同步迁移入口、当前对象集合、ACL、安装拒迁边界和验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 3 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/unified-metering.md`
- modified `migrations/current_mvp.sql`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T07:06:31Z · a1794a4f0150

P0-2：将上游明确 binding 级模型错误与请求级 context_length_exceeded 分离，只有显式 binding 级错误可持久化 route_binding_isolations；补充跨租户 PostgreSQL 回归，未改变迁移、账务接口或媒体模块

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2110 个源文件；本次 7 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/verification/mvp-failure-recovery.md`
- modified `internal/provider/classification.go`
- modified `internal/provider/classification_test.go`
- modified `internal/provider/client.go`
- modified `internal/provider/types.go`
- modified `internal/server/binding_isolation_integration_test.go`
- modified `internal/server/gateway.go`

</details>

## 2026-11-10 · current-only recovery and append path

备份恢复演练改用 `VerifyCurrent` 并完成专用 PostgreSQL 备份、恢复、checksum 和恢复锁验证；增加 current-only 追加迁移计划、幂等安装和完整当前校验，不修改钱包、billing 或 settlement 业务路径。

已显式复核统一架构正文、能力状态和源码链接；当前追加迁移只在已验证的 current baseline 上执行，历史迁移和旧库转换仍被拒绝。

## 2026-11-10T08:53:32Z · 8c771589e1a6

P1-7/P1-8 provider 契约：Responses reasoning profile/catalog 拒绝边界与可配置阶段 timeout、上下限、Compose/config/server 接线；未改 migration/billing/wallet/media

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2113 个源文件；本次 31 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `compose.yaml`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/provider-qualification-plan.md`
- modified `docs/runbooks/backup-restore.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- modified `docs/verification/backup-restore-local.json`
- modified `docs/verification/backup-restore.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/catalog/public_catalog_test.go`
- modified `internal/config/config.go`
- modified `internal/config/config_test.go`
- modified `internal/provider/native_test.go`
- modified `internal/provider/network.go`
- added `internal/provider/profile_contract_test.go`
- modified `internal/provider/request_test.go`
- modified `internal/provider/response_test.go`
- added `internal/provider/timeout_contract_test.go`
- modified `internal/provider/types.go`
- modified `internal/provider/usage.go`
- modified `internal/server/server.go`
- modified `migrations/current.go`
- added `migrations/current_001_append_path.sql`
- modified `migrations/embed.go`
- modified `migrations/embed_test.go`
- modified `scripts/backup-restore-drill.go`
- modified `scripts/deployment-qa.sh`
- modified `specs/001-commercial-mvp/contracts/provider.md`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T09:39:21Z · 26314048cf7c

增加媒体 provider mock 安全边界：显式 profile、app/worker 同步配置、inner-test provider_unavailable、mock 零收费与未知不重试，并更新部署与验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2116 个源文件；本次 27 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.env.example`
- modified `cmd/moni-aigate/main.go`
- modified `compose.yaml`
- added `docs/design/media-provider-safety.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/config/config.go`
- modified `internal/config/config_test.go`
- modified `internal/provider/mock_media.go`
- modified `internal/provider/mock_realtime.go`
- modified `internal/provider/mock_task.go`
- modified `internal/provider/task_adapter.go`
- modified `internal/provider/types.go`
- modified `internal/realtime/service.go`
- modified `internal/server/gateway_integration_test.go`
- modified `internal/server/image_handlers.go`
- modified `internal/server/image_handlers_integration_test.go`
- modified `internal/server/media_handlers.go`
- added `internal/server/media_provider_runtime.go`
- added `internal/server/media_provider_safety_integration_test.go`
- modified `internal/server/realtime.go`
- modified `internal/server/realtime_http_postgres_integration_test.go`
- modified `internal/server/realtime_persistence.go`
- modified `internal/server/server.go`
- modified `internal/server/task_handlers.go`
- modified `internal/server/task_media_billing.go`
- modified `internal/server/task_media_billing_integration_test.go`
- modified `scripts/deployment-qa.sh`

</details>

## 2026-11-10T09:47:40Z · 8b8f2d03407c

当前 MVP 集成复核：current-only migration、provider 契约、028-U guard、wallet/billing/settlement、六文本、媒体 mock 安全、Compose 单机和验收文档已同步；完整 U guard 正反闭包、真实供应商、浏览器、正式支付、HA 与性能仍未完成

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2121 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `Makefile`
- added `docs/runbooks/current-mvp-verification.md`
- added `docs/verification/028-u-guard-matrix.md`
- added `docs/verification/current-mvp.md`
- modified `docs/verification/unified-metering.md`
- added `migrations/unified_text_metering_guard_current_postgres_test.go`
- modified `migrations/unified_text_metering_guard_matrix_postgres_test.go`
- modified `migrations/unified_text_metering_postgres_test.go`
- added `scripts/verify-current-mvp.sh`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T09:59:43Z · cc68243748de

补记主分支 current MVP 17 组验收全部通过；验证入口、媒体 mock/inner-test、当前 guard 与未完成的真实供应商/完整商业边界保持一致

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2121 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/current-mvp.md`
- modified `docs/verification/unified-metering.md`

</details>

## 2026-11-10T10:56:45Z · 608294bf1df9

新增 current-only 默认运行时路径审计脚本，并补充真实二租户 HTTP/WS 等价内测 smoke 证据；只更新架构入口、验收记录与清单，不改变业务运行模块

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2124 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- added `docs/verification/browser-qa.md`
- modified `docs/verification/current-mvp.md`
- modified `docs/verification/overall-delivery-inventory.md`
- added `scripts/browser-qa/http_security_smoke_test.go`
- added `scripts/current-runtime-path-audit.sh`
- modified `scripts/deployment-business-qa.sh`

</details>

## 2026-11-10T11:16:39Z · d9dba4b39717

汇合 028-U guard closure、默认运行时兼容路径审计和只消费已有引用的 HTTP/WS 等价内测 sidecar；新增仅测试与验证入口，复核 current-only 模块图、请求/计量/资金链路、权限边界和明确后置验收范围

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2128 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/runbooks/deployment.md`
- modified `docs/verification/028-u-guard-matrix.md`
- modified `docs/verification/current-mvp.md`
- modified `docs/verification/overall-delivery-inventory.md`
- modified `docs/verification/unified-metering.md`
- added `migrations/unified_text_metering_guard_closure_postgres_test.go`
- added `scripts/browser-qa/README.md`
- added `scripts/browser-qa/current-mvp-smoke.sh`
- added `scripts/browser-qa/current-mvp-smoke.test.sh`
- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T11:18:20Z · 930d0d67fc7b

将 028-U guard closure 测试加入 verify-current-mvp，并把 18 组 current MVP 验收与 HTTP/WS sidecar 证据同步到架构/验证记录；模块图与当前唯一计量、wallet、settlement、媒体链路不变

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2128 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/current-mvp.md`
- modified `docs/verification/unified-metering.md`
- modified `scripts/verify-current-mvp.sh`

</details>

## 2026-11-10T11:31:03Z · 8adc6baa477f

校正最终 current MVP 验收分组统计；028-U guard closure 已纳入既有 guard 组，不新增运行模块

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2128 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/current-mvp.md`
- modified `docs/verification/unified-metering.md`

</details>

## 2026-11-10T12:15:40Z · 82f8d0184d26

current MVP guard audit、provider timeout、备份恢复业务事实和18组验收门禁已汇合；当前架构与验证记录同步

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2130 个源文件；本次 13 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/unified-metering.md`
- modified `docs/runbooks/current-mvp-verification.md`
- modified `docs/verification/028-u-guard-matrix.md`
- modified `docs/verification/backup-restore-local.json`
- modified `docs/verification/current-mvp.md`
- added `docs/verification/provider-usage.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/provider/client.go`
- modified `internal/provider/response_test.go`
- modified `internal/server/cost_vector_application_integration_test.go`
- added `migrations/unified_text_metering_guard_audit_postgres_test.go`
- modified `scripts/backup-restore-drill.go`
- modified `scripts/verify-current-mvp.sh`

</details>

## 2026-11-10T12:26:00Z · 33991d66d873

最终18组 current MVP 门禁更新备份恢复随机证据；复核架构正文、验证记录、权限边界和当前运行路径后同步快照

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2130 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/backup-restore-local.json`

</details>

## 2026-11-10T12:31:43Z · ddc082437b2b

更新028-U任务清单的当前MVP收口状态与正式Release Qualification剩余边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2130 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `specs/028-unified-metering-media/tasks.md`

</details>

## 2026-11-10T14:08:17Z · 8af175b7decc

新增四阶段路线单一事实源并同步HLD/LLD/路线图/交付计划/README：真实供应商认证移至第四阶段，前三阶段渠道与动态路由只覆盖文本，媒体保持单profile单adapter不参与路由，修正文本与媒体共享钱包但分属两族业务表的描述，补第三阶段路由决策合同缺口与第一阶段别名/分组倍率约束；纯设计变更，无源码、迁移或权限结构变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2131 个源文件；本次 9 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/media-provider-safety.md`
- modified `docs/design/new-api-roadmap.md`
- added `docs/design/phase-roadmap.md`
- modified `docs/design/provider-qualification-plan.md`

</details>

## 2026-11-10T14:48:18Z · fae99f479782

补充四阶段后续能力的设计规格029–033（第一阶段验收清单、模型别名与分组倍率、多协议多候选mock上游、第二阶段文本数据面、第三阶段文本动态路由），并在HLD/LLD/路线文档链接；记录对既有约束（同池channel唯一、028单profile、route_decisions JSONB、price variant_key）的修订需求与决策点；纯设计草案，无源码、迁移或权限变化，能力表标注为设计草案未实现

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2136 个源文件；本次 8 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/phase-roadmap.md`
- added `specs/029-phase1-acceptance/spec.md`
- added `specs/030-model-aliases-group-multipliers/spec.md`
- added `specs/031-deterministic-mock-upstream/spec.md`
- added `specs/032-phase2-text-data-plane/spec.md`
- added `specs/033-text-dynamic-routing/spec.md`

</details>

## 2026-11-10T15:20:12Z · e1329d112b87

合入 main 的四阶段路线与规格 029–033，并保留当前 baiyang 供应商资格探测、CI race/历史候选门禁修复；复核统一架构、源码链接和能力状态

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2140 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `.github/workflows/ci.yml`
- modified `docs/architecture/unified-metering.md`
- modified `docs/design/provider-qualification-plan.md`
- added `docs/verification/provider-qualification-baiyang.md`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/server/pricing_publish_http_integration_test.go`
- added `scripts/provider-qualification/README.md`
- added `scripts/provider-qualification/main.go`
- added `scripts/provider-qualification/main_test.go`
- modified `specs/022-self-service-payments/c0-wallet-readset.json`

</details>

## 2026-11-11T04:56:29Z · f3c7ed54ee35

新增034虚拟币记账单位设计草案（参考New API quota与充值源码，采纳统一整数单位与展示分离，不采纳可变全局换算/回调时重算/浮点金额/运行时倍率相乘），并在四阶段路线与HLD价格成本节登记；纯设计草案，现行合同仍为人民币单币种，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/phase-roadmap.md`
- added `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T06:24:17Z · acc41a40d77c

修订034：确认对外只暴露虚拟币且手动充值/手动现金退款优先；明确钱包侧与付款侧字段单位而不改名；补充付费与赠送两个来源的数据关系、第四桶retired及需同步修改的guard、现金退款整数规则与审批流程、journal适用范围、付款基准改称；路线图与HLD措辞同步；纯设计草案，现行合同仍为人民币单币种，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T07:11:33Z · c5061f59a5e0

按评审修订034：现金退款改为先冻结后退出的状态机（避免转账失败丢币）；拆分recipient/requested_by/maker/approver/transfer_executor并给出权限矩阵；钱包按租户；单人运营仅作内测例外并加审计标记；source_kind与basis_kind允许组合由CHECK枚举并新增manual_unknown；两来源的parent_ref、成对性约束及journal operation_id全局唯一的处理；第四桶retired与账户余额检查的关系及需同步的journal kind校验；新增冻结前阻断项与评审逐条状态；路线图状态改为设计草案；纯设计变更，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T07:12:38Z · 414805b4f63e

034补充已核对的wallet_event_valid封闭事件类型与wallet_lot_balance_guard不要求held对应预占的事实，并细化需同步修改的guard清单；纯设计变更，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T07:27:30Z · 8d19337ca772

对照现状校验033与034并修订：034的FIFO排序由SQL guard与Go两处强制、CNY字面量还在函数体、退款冻结改用独立refund_held桶与REFUND_HELD账户而不复用held/HELD、迁移DDL语法与回填、报表补媒体结算分配、移除财务法务冻结条件；033的同池channel唯一实为Go校验而非SQL约束、预占界由逻辑模型profile决定、决策记录改为每次决策一行加有界JSONB；HLD/LLD/路线图同步并新增设计完整度清单；纯设计变更，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/phase-roadmap.md`
- modified `specs/033-text-dynamic-routing/spec.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T07:57:31Z · 7796ca76e1fe

按评审修订034与路线图：路线图5.2的auto预占界统一为逻辑模型profile、能力不足候选在硬过滤排除、路由切换不改变预占上界；034的三元组约束改为receipt与lot各自单表CHECK加wallet_lot_valid（PostgreSQL普通CHECK不能跨表）；统一basis_ref/parent_ref/proof_ref/transfer_ref/fx_note字段职责；D-034-4改为实施阻断并给出A改FIFO合同/B保持严格FIFO/C控制赠送来源时间戳三个方案及已核对的四处排序位置；更正journals.kind白名单实为funding_fact_guard触发器；保留设计可冻结但不代表生产财务验收的边界；纯设计变更

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T08:15:27Z · 1f0da1702362

034裁决D-034-4：同一充值单付费来源先消耗、赠送来源后消耗，沿用严格FIFO不改任何guard，删除原方案A与C；用本机PostgreSQL 16.15实测clock_timestamp在同语句内连续调用有约90%相等、隔一次往返3000对无相等，因此以赠送来源created_at取max(时钟,付费created_at+1微秒)提供确定性保证并在注释、延迟校验触发器和并发测试三处固定；新增客户余额构成展示（付费/赠送/其他之和等于可用余额）及前端来源类型校验放开；D-034-10待确认；路线图同步；纯设计变更，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T08:52:07Z · e5cd342d721c

034按产品裁决修订：现金退款额取min(按当前币价,按原付款比例)并用20万用例对拍精确分数验证、方案A存在退款再充值套利而B净收益为0；新增追加式币价版本表(按币种、审批、不得回溯、调价预览)并让paid来源记录买入币价版本；全额退款由系统填币数且同一来源一张未完成单(部分唯一索引)；弃用累计取整公式与productvalue；单人运营开关默认开启并逐处核对需修改的role()精确匹配、自审拒绝、funding.go授权SQL、funding_journal_valid及两个CHECK；赠送币到期后置且不预留字段；新增客户自助申请表；路线图与HLD同步；纯设计变更，无源码、迁移或权限变化

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 3 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-11T11:54:01Z · 5b69cecc91af

update 034 virtual coin decisions: paid-first FIFO, admin-controlled single-operator switch, B refund rule with MVP rate-adjustment deferral

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2141 个源文件；本次 4 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-12T11:45:15Z · e02d8d0df092

merge external upstream integration roadmap while preserving the merged virtual-coin architecture history

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2143 个源文件；本次 10 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/decisions/ADR-001-source-strategy.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/phase-roadmap.md`
- modified `docs/design/provider-qualification-plan.md`
- added `docs/design/upstream-integration-plan.md`
- added `specs/035-external-upstream-integration/spec.md`

</details>

## 2026-11-12T11:56:39Z · 3018c5a08242

rename external upstream contract to 035 to preserve the existing 034 virtual-coin specification

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2143 个源文件；本次 1 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/provider-qualification-plan.md`

</details>

## 2026-11-12T12:03:19Z · dae22220d29a

align the HLD link label with the renumbered external upstream contract

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2143 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`

</details>

## 2026-11-13T09:11:05Z · 3278d1a7e352

迁移模型别名、分组倍率编译与确定性多协议 mock，并同步当前迁移、目录、billing、Compose 与验收文档

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2171 个源文件；本次 67 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `Dockerfile`
- modified `compose.yaml`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/phase1/031-mock-harness.md`
- added `docs/verification/phase1/P1-CAT-02.md`
- added `docs/verification/phase1/P1-PR-02.md`
- modified `docs/verification/unified-metering.md`
- modified `internal/billing/reserve.go`
- modified `internal/billing/store_test.go`
- modified `internal/billing/types.go`
- modified `internal/billing/voucher_roles_integration_test.go`
- added `internal/catalog/alias_reader_contract_test.go`
- modified `internal/catalog/credential_pool_integration_test.go`
- added `internal/catalog/group_price_compile.go`
- added `internal/catalog/group_price_compile_test.go`
- added `internal/catalog/model_alias.go`
- added `internal/catalog/model_alias_test.go`
- added `internal/catalog/phase1_alignment_integration_test.go`
- modified `internal/catalog/pricing_commands.go`
- modified `internal/catalog/pricing_types.go`
- modified `internal/catalog/public_catalog.go`
- modified `internal/catalog/public_catalog_integration_test.go`
- modified `internal/catalog/store.go`
- modified `internal/operations/operations.go`
- added `internal/server/alias_reader_contract_test.go`
- modified `internal/server/auth_integration_test.go`
- modified `internal/server/gateway.go`
- modified `internal/server/gateway_integration_test.go`
- added `internal/server/model_alias_handlers.go`
- added `internal/server/model_alias_handlers_test.go`
- modified `internal/server/pricing_handlers.go`
- modified `internal/server/pricing_input.go`
- modified `internal/server/pricing_test.go`
- modified `internal/server/queries.go`
- modified `internal/server/server.go`
- added `internal/testsupport/mockupstream/config.go`
- added `internal/testsupport/mockupstream/config_test.go`
- added `internal/testsupport/mockupstream/provider_contract_test.go`
- added `internal/testsupport/mockupstream/server.go`
- added `internal/testsupport/mockupstream/server_test.go`
- modified `migrations/current.go`
- added `migrations/current_002_phase1_aliases.sql`
- modified `scripts/deployment-business-provision.sh`
- modified `scripts/deployment-business-qa.test.sh`
- deleted `scripts/deployment-qa-provider.py`
- added `scripts/mock-upstream/business-qa.json`
- added `scripts/mock-upstream/main.go`
- added `scripts/phase1-mock-acceptance.py`
- added `scripts/phase1-mock-acceptance.sh`
- added `scripts/phase1-mock-acceptance.test.sh`
- modified `specs/029-phase1-acceptance/spec.md`
- added `specs/030-model-aliases-group-multipliers/a-alias-periphery-contract.md`
- added `specs/030-model-aliases-group-multipliers/integration-contract.md`
- modified `specs/031-deterministic-mock-upstream/spec.md`
- added `web/src/components/GroupMultiplierCompiler.vue`
- added `web/src/components/ModelAliases.vue`
- modified `web/src/components/PricingWriter.vue`
- modified `web/src/customer-pricing.ts`
- added `web/src/model-aliases.ts`
- modified `web/src/pricing.ts`
- modified `web/src/public-catalog.ts`
- modified `web/src/views/ModelsView.vue`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/PriceGroupsView.vue`
- added `web/tests/model-aliases.test.mjs`
- modified `web/tests/pricing.test.mjs`

</details>

## 2026-11-13T16:15:11Z · 6286f7ec4ffd

收口 0 阶段外部上游：current-only channel kind/mode、relay/probe、route revision 回退、current fixture 修复及 030/031/Compose 验证记录；真实上游与三库拓扑仍标为未验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2176 个源文件；本次 25 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/phase0-external-upstream.md`
- modified `docs/verification/phase1/031-mock-harness.md`
- modified `docs/verification/phase1/P1-CAT-02.md`
- modified `docs/verification/phase1/P1-PR-02.md`
- modified `internal/catalog/cost_policy_integration_test.go`
- added `internal/catalog/route_activate.go`
- modified `internal/catalog/store.go`
- modified `internal/operations/operations_integration_test.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/periodicbudget/verifier_integration_test.go`
- modified `internal/server/catalog_handlers.go`
- added `internal/server/external_relay_integration_test.go`
- added `internal/server/model_alias_gateway_integration_test.go`
- modified `internal/server/probe.go`
- modified `internal/server/public_catalog_integration_test.go`
- modified `internal/server/queries.go`
- modified `internal/server/server.go`
- modified `internal/testsupport/mockupstream/server.go`
- modified `internal/testsupport/mockupstream/server_test.go`
- modified `migrations/current.go`
- added `migrations/current_003_external_channels.sql`
- modified `scripts/phase1-mock-acceptance.py`
- modified `specs/035-external-upstream-integration/spec.md`

</details>

## 2026-11-13T16:27:44Z · 34dae2a5c461

补强 035 route revision 回退：目标 channel/credential 固定锁序，拒绝整池撤销及并发撤销竞态；补专用 PostgreSQL 负向测试并同步验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2176 个源文件；本次 4 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/LLD.md`
- modified `docs/verification/phase0-external-upstream.md`
- modified `internal/catalog/route_activate.go`
- modified `internal/server/external_relay_integration_test.go`

</details>

## 2026-11-13T17:08:21Z · 474a9be974b0

修正实际主进程 fixture 清理边界：当前基线删库与角色清理由共享 10 秒改为有界 30 秒；业务状态机不变，支付失败确认为专用库时区环境恢复，不修改冻结清单

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2176 个源文件；本次 2 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `internal/server/alert_main_integration_test.go`
- modified `internal/server/main_shutdown_integration_test.go`

</details>

## 2026-11-13T18:37:27Z · 87d8e6809697

创建 local-mvp 基线并冻结 032 缓存/熔断/预算、033 动态路由/物理计量/回放、034 虚拟币五桶/现金退款实施合同；仅设计冻结，未把规划能力写成已实现

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2179 个源文件；本次 6 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- added `specs/032-phase2-text-data-plane/implementation-freeze.md`
- modified `specs/032-phase2-text-data-plane/spec.md`
- added `specs/033-text-dynamic-routing/implementation-freeze.md`
- modified `specs/033-text-dynamic-routing/spec.md`
- added `specs/034-virtual-coin/implementation-freeze.md`
- modified `specs/034-virtual-coin/spec.md`

</details>
## 2026-11-13T19:18:07Z · 67815182e87b

部署定位修订：产品定位从“自托管/1Panel 体验”改为仓库自带 Compose/脚本部署，1Panel 等面板降为可选部署环境、非验收门禁；同步 README、统一架构、HLD、overview、四阶段路线、交付计划、new-api 路线图、上游接入路线、ADR-001 及 001/014/025/028/029/035 规格；附带修复四阶段路线 0 阶段插入后小节未重编号导致的断链（小节归位 4.x/5.x/6.x，更新 029–034 与 new-api-roadmap 的锚点）

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2143 个源文件；本次 23 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `docs/decisions/ADR-001-source-strategy.md`
- modified `docs/design/HLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/overview.md`
- modified `docs/design/phase-roadmap.md`
- modified `docs/design/upstream-integration-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `specs/001-commercial-mvp/spec.md`
- modified `specs/014-mvp-release-qualification/spec.md`
- modified `specs/025-production-ha/a0-compose-config-contract.md`
- modified `specs/025-production-ha/acceptance.md`
- modified `specs/025-production-ha/contract.md`
- modified `specs/025-production-ha/spec.md`
- modified `specs/028-unified-metering-media/spec.md`
- modified `specs/029-phase1-acceptance/spec.md`
- modified `specs/030-model-aliases-group-multipliers/spec.md`
- modified `specs/031-deterministic-mock-upstream/spec.md`
- modified `specs/032-phase2-text-data-plane/spec.md`
- modified `specs/033-text-dynamic-routing/spec.md`
- modified `specs/034-virtual-coin/spec.md`
- modified `specs/035-external-upstream-integration/spec.md`

</details>
## 2026-11-13T19:23:11Z · cdbc6ffd9ed6

4 个 beta 加固提交回流 main：Chat usage 白名单（DeepSeek 分区与 cost/cost_details/is_byok 元数据键，未知键仍冲突）与 reasoning 增量计入 stream progress（不转发不计费）；备份演练 PG15+ schema 属主修复；运营账号恢复 CLI（user set-password/reset-mfa/suspend/activate，经既有 app 身份链，无新角色/模块/表）。迁移幂等机制（46c42d1）因与上游 current_*.sql 追加方案语义重复而跳过。模块图、请求链路、数据关系与权限边界无结构变化；specs/001 provider 合同已同步 usage 白名单与 progress 语义。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2147 个源文件；本次 11 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `cmd/moni-aigate/main.go`
- added `cmd/moni-aigate/user_command.go`
- added `cmd/moni-aigate/user_command_test.go`
- added `internal/identity/operator.go`
- added `internal/identity/operator_integration_test.go`
- modified `internal/provider/request_test.go`
- modified `internal/provider/response.go`
- modified `internal/provider/response_test.go`
- modified `internal/provider/usage.go`
- modified `scripts/backup-restore-drill.go`
- modified `specs/001-commercial-mvp/contracts/provider.md`

</details>

## 2026-11-13T20:12:16Z · d85d3f896ac8

合入 PR #6/#7：部署定位修订与四阶段路线断链修复；beta usage/reasoning、备份 schema owner 与运营恢复 CLI 回流；保留双方架构历史并补恢复 runbook，已复核实际 app/projection 角色边界

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2148 个源文件；本次 13 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `cmd/moni-aigate/main.go`
- added `cmd/moni-aigate/user_command.go`
- added `cmd/moni-aigate/user_command_test.go`
- modified `docs/runbooks/development.md`
- added `docs/runbooks/operator-account-recovery.md`
- added `internal/identity/operator.go`
- added `internal/identity/operator_integration_test.go`
- modified `internal/provider/request_test.go`
- modified `internal/provider/response.go`
- modified `internal/provider/response_test.go`
- modified `internal/provider/usage.go`
- modified `scripts/backup-restore-drill.go`
- modified `specs/001-commercial-mvp/contracts/provider.md`

</details>

## 2026-11-13T20:15:21Z · 1e5660faedaf

将合入 PR #6/#7 的 main 同步到 local-mvp：保留 032/033/034 实施冻结与真实上游 checkpoint，合并部署定位、运营恢复与 beta 加固，保留双方架构历史并复核冲突

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2186 个源文件；本次 38 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `README.md`
- modified `cmd/moni-aigate/main.go`
- added `cmd/moni-aigate/user_command.go`
- added `cmd/moni-aigate/user_command_test.go`
- added `deploy/external-upstreams.compose.yaml`
- modified `docs/decisions/ADR-001-source-strategy.md`
- modified `docs/design/HLD.md`
- modified `docs/design/delivery-plan.md`
- modified `docs/design/new-api-roadmap.md`
- modified `docs/design/overview.md`
- modified `docs/design/phase-roadmap.md`
- modified `docs/design/upstream-integration-plan.md`
- modified `docs/runbooks/deployment.md`
- modified `docs/runbooks/development.md`
- added `docs/runbooks/operator-account-recovery.md`
- added `internal/identity/operator.go`
- added `internal/identity/operator_integration_test.go`
- modified `internal/provider/request_test.go`
- modified `internal/provider/response.go`
- modified `internal/provider/response_test.go`
- modified `internal/provider/usage.go`
- modified `scripts/backup-restore-drill.go`
- added `scripts/external-upstream-qa.py`
- modified `specs/001-commercial-mvp/contracts/provider.md`
- modified `specs/001-commercial-mvp/spec.md`
- modified `specs/014-mvp-release-qualification/spec.md`
- modified `specs/025-production-ha/a0-compose-config-contract.md`
- modified `specs/025-production-ha/acceptance.md`
- modified `specs/025-production-ha/contract.md`
- modified `specs/025-production-ha/spec.md`
- modified `specs/028-unified-metering-media/spec.md`
- modified `specs/029-phase1-acceptance/spec.md`
- modified `specs/030-model-aliases-group-multipliers/spec.md`
- modified `specs/031-deterministic-mock-upstream/spec.md`
- modified `specs/032-phase2-text-data-plane/spec.md`
- modified `specs/033-text-dynamic-routing/spec.md`
- modified `specs/034-virtual-coin/spec.md`
- modified `specs/035-external-upstream-integration/spec.md`

</details>

## 2026-11-13T20:29:54Z · 8c6fc3f6d948

真实本地 New API/Sub2API 三服务三库拓扑验收：固定 New API e449cc7 与 Sub2API 10ba8f9/digest，三库与内部网络、JSON/SSE、唯一结算、receipt 和 route revision 回退通过；供应商资格仍标未验收

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2186 个源文件；本次 3 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/verification/phase0-external-upstream.md`
- modified `scripts/external-upstream-qa.py`

</details>

## 2026-11-14T04:08:13Z · 97838356cf2b

034.1 COIN 单位、FiatMoney 类型边界与历史定价回执迁移；计量 canonical 与供应商/支付 CNY 保持不变；026 报表留到 034.5

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2191 个源文件；本次 68 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/wallet-sources.md`
- added `docs/verification/034-coin-units.md`
- added `internal/billing/fiat.go`
- added `internal/billing/fiat_test.go`
- modified `internal/billing/funding.go`
- modified `internal/billing/funding_integration_test.go`
- modified `internal/billing/funding_sandbox.go`
- modified `internal/billing/voucher_integration_test.go`
- modified `internal/billing/voucher_query.go`
- modified `internal/billing/wallet_sources_read.go`
- modified `internal/billing/wallet_sources_summary_test.go`
- modified `internal/catalog/group_price_vector_integration_test.go`
- modified `internal/catalog/pricing_commands.go`
- modified `internal/catalog/pricing_publish.go`
- modified `internal/catalog/pricing_queries.go`
- modified `internal/catalog/pricing_types.go`
- modified `internal/catalog/pricing_vector.go`
- modified `internal/catalog/pricing_vector_test.go`
- modified `internal/catalog/public_catalog.go`
- modified `internal/catalog/public_catalog_test.go`
- modified `internal/metering/types.go`
- modified `internal/operations/funding_integration_test.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/payments/processor_evidence.go`
- modified `internal/payments/processor_test.go`
- modified `internal/payments/processor_types.go`
- modified `internal/payments/service_funding_read_test.go`
- modified `internal/payments/service_query.go`
- modified `internal/payments/service_types.go`
- modified `internal/server/payment_handlers.go`
- modified `internal/server/queries.go`
- modified `internal/testsupport/meterquote/publish.go`
- added `migrations/current_004_virtual_coin_units.sql`
- added `migrations/virtual_coin_postgres_test.go`
- modified `web/src/commerce.ts`
- modified `web/src/components/CommerceCommands.vue`
- modified `web/src/components/CommerceWriter.vue`
- modified `web/src/components/FundingPanel.vue`
- modified `web/src/components/MetricMoney.vue`
- modified `web/src/components/PaymentsPanel.vue`
- modified `web/src/components/PeriodicBudgetPanel.vue`
- modified `web/src/components/PeriodicBudgetWriter.vue`
- modified `web/src/components/PricingWriter.vue`
- modified `web/src/components/RequestFunding.vue`
- modified `web/src/components/WalletSources.vue`
- modified `web/src/format.ts`
- modified `web/src/key-configuration.ts`
- modified `web/src/metering-wire.ts`
- modified `web/src/pricing.ts`
- modified `web/src/public-catalog.ts`
- modified `web/src/views/KeysView.vue`
- modified `web/src/views/ModelsView.vue`
- modified `web/src/views/OverviewView.vue`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/PriceGroupsView.vue`
- modified `web/src/views/PricingView.vue`
- modified `web/src/views/ReportsView.vue`
- modified `web/src/views/RequestsView.vue`
- modified `web/src/views/VouchersView.vue`
- modified `web/src/views/WalletView.vue`
- modified `web/tests/commerce.test.mjs`
- modified `web/tests/key-configuration.test.mjs`
- modified `web/tests/metering-wire.test.mjs`
- modified `web/tests/pricing-state.test.mjs`
- modified `web/tests/pricing.test.mjs`
- modified `web/tests/public-catalog-state.test.mjs`
- modified `web/tests/public-catalog.test.mjs`

</details>

## 2026-11-14T04:12:19Z · dd16169f0c16

034.1 完整本地验收：存量 COIN 迁移、历史定价回执重放、FiatMoney、前端单位与权限边界；完整 server/PG race、前端和文档已复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2191 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/034-coin-units.md`

</details>

## 2026-11-14T03:32:58Z · 9608d9b45941

034 冻结与开发方向审查：034 规格 D-034-1…13 全部裁决并标记已冻结；HLD §6.3、LLD §5.2.1、phase-roadmap 三处同步冻结状态；新增 docs/design/direction-review-2026-11-14.md 方向审查记录（含在途实现对照与不变量复核）；修正 034 §4 兑换券校验行号误差。纯文档变更，无代码/迁移影响

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2149 个源文件；本次 5 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/design/direction-review-2026-11-14.md`
- modified `docs/design/phase-roadmap.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-14T04:17:14Z · f6d69e8e7485

PR #8 合入后回合 main 到 local-mvp；034 已冻结

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2192 个源文件；本次 146 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `Dockerfile`
- modified `compose.yaml`
- added `deploy/external-upstreams.compose.yaml`
- modified `docs/architecture/wallet-sources.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- added `docs/verification/034-coin-units.md`
- added `docs/verification/phase0-external-upstream.md`
- added `docs/verification/phase1/031-mock-harness.md`
- added `docs/verification/phase1/P1-CAT-02.md`
- added `docs/verification/phase1/P1-PR-02.md`
- modified `docs/verification/unified-metering.md`
- added `internal/billing/fiat.go`
- added `internal/billing/fiat_test.go`
- modified `internal/billing/funding.go`
- modified `internal/billing/funding_integration_test.go`
- modified `internal/billing/funding_sandbox.go`
- modified `internal/billing/reserve.go`
- modified `internal/billing/store_test.go`
- modified `internal/billing/types.go`
- modified `internal/billing/voucher_integration_test.go`
- modified `internal/billing/voucher_query.go`
- modified `internal/billing/voucher_roles_integration_test.go`
- modified `internal/billing/wallet_sources_read.go`
- modified `internal/billing/wallet_sources_summary_test.go`
- added `internal/catalog/alias_reader_contract_test.go`
- modified `internal/catalog/cost_policy_integration_test.go`
- modified `internal/catalog/credential_pool_integration_test.go`
- added `internal/catalog/group_price_compile.go`
- added `internal/catalog/group_price_compile_test.go`
- modified `internal/catalog/group_price_vector_integration_test.go`
- added `internal/catalog/model_alias.go`
- added `internal/catalog/model_alias_test.go`
- added `internal/catalog/phase1_alignment_integration_test.go`
- modified `internal/catalog/pricing_commands.go`
- modified `internal/catalog/pricing_publish.go`
- modified `internal/catalog/pricing_queries.go`
- modified `internal/catalog/pricing_types.go`
- modified `internal/catalog/pricing_vector.go`
- modified `internal/catalog/pricing_vector_test.go`
- modified `internal/catalog/public_catalog.go`
- modified `internal/catalog/public_catalog_integration_test.go`
- modified `internal/catalog/public_catalog_test.go`
- added `internal/catalog/route_activate.go`
- modified `internal/catalog/store.go`
- modified `internal/metering/types.go`
- modified `internal/operations/funding_integration_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/operations_integration_test.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/payments/processor_evidence.go`
- modified `internal/payments/processor_test.go`
- modified `internal/payments/processor_types.go`
- modified `internal/payments/service_funding_read_test.go`
- modified `internal/payments/service_query.go`
- modified `internal/payments/service_types.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/periodicbudget/verifier_integration_test.go`
- modified `internal/server/alert_main_integration_test.go`
- added `internal/server/alias_reader_contract_test.go`
- modified `internal/server/auth_integration_test.go`
- modified `internal/server/catalog_handlers.go`
- added `internal/server/external_relay_integration_test.go`
- modified `internal/server/gateway.go`
- modified `internal/server/gateway_integration_test.go`
- modified `internal/server/main_shutdown_integration_test.go`
- added `internal/server/model_alias_gateway_integration_test.go`
- added `internal/server/model_alias_handlers.go`
- added `internal/server/model_alias_handlers_test.go`
- modified `internal/server/payment_handlers.go`
- modified `internal/server/pricing_handlers.go`
- modified `internal/server/pricing_input.go`
- modified `internal/server/pricing_test.go`
- modified `internal/server/probe.go`
- modified `internal/server/public_catalog_integration_test.go`
- modified `internal/server/queries.go`
- modified `internal/server/server.go`
- modified `internal/testsupport/meterquote/publish.go`
- added `internal/testsupport/mockupstream/config.go`
- added `internal/testsupport/mockupstream/config_test.go`
- added `internal/testsupport/mockupstream/provider_contract_test.go`
- added `internal/testsupport/mockupstream/server.go`
- added `internal/testsupport/mockupstream/server_test.go`
- modified `migrations/current.go`
- added `migrations/current_002_phase1_aliases.sql`
- added `migrations/current_003_external_channels.sql`
- added `migrations/current_004_virtual_coin_units.sql`
- added `migrations/virtual_coin_postgres_test.go`
- modified `scripts/deployment-business-provision.sh`
- modified `scripts/deployment-business-qa.test.sh`
- deleted `scripts/deployment-qa-provider.py`
- added `scripts/external-upstream-qa.py`
- added `scripts/mock-upstream/business-qa.json`
- added `scripts/mock-upstream/main.go`
- added `scripts/phase1-mock-acceptance.py`
- added `scripts/phase1-mock-acceptance.sh`
- added `scripts/phase1-mock-acceptance.test.sh`
- modified `specs/029-phase1-acceptance/spec.md`
- added `specs/030-model-aliases-group-multipliers/a-alias-periphery-contract.md`
- added `specs/030-model-aliases-group-multipliers/integration-contract.md`
- modified `specs/031-deterministic-mock-upstream/spec.md`
- added `specs/032-phase2-text-data-plane/implementation-freeze.md`
- modified `specs/032-phase2-text-data-plane/spec.md`
- added `specs/033-text-dynamic-routing/implementation-freeze.md`
- modified `specs/033-text-dynamic-routing/spec.md`
- added `specs/034-virtual-coin/implementation-freeze.md`
- modified `specs/035-external-upstream-integration/spec.md`
- modified `web/src/commerce.ts`
- modified `web/src/components/CommerceCommands.vue`
- modified `web/src/components/CommerceWriter.vue`
- modified `web/src/components/FundingPanel.vue`
- added `web/src/components/GroupMultiplierCompiler.vue`
- modified `web/src/components/MetricMoney.vue`
- added `web/src/components/ModelAliases.vue`
- modified `web/src/components/PaymentsPanel.vue`
- modified `web/src/components/PeriodicBudgetPanel.vue`
- modified `web/src/components/PeriodicBudgetWriter.vue`
- modified `web/src/components/PricingWriter.vue`
- modified `web/src/components/RequestFunding.vue`
- modified `web/src/components/WalletSources.vue`
- modified `web/src/customer-pricing.ts`
- modified `web/src/format.ts`
- modified `web/src/key-configuration.ts`
- modified `web/src/metering-wire.ts`
- added `web/src/model-aliases.ts`
- modified `web/src/pricing.ts`
- modified `web/src/public-catalog.ts`
- modified `web/src/views/KeysView.vue`
- modified `web/src/views/ModelsView.vue`
- modified `web/src/views/OverviewView.vue`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/PriceGroupsView.vue`
- modified `web/src/views/PricingView.vue`
- modified `web/src/views/ReportsView.vue`
- modified `web/src/views/RequestsView.vue`
- modified `web/src/views/VouchersView.vue`
- modified `web/src/views/WalletView.vue`
- modified `web/tests/commerce.test.mjs`
- modified `web/tests/key-configuration.test.mjs`
- modified `web/tests/metering-wire.test.mjs`
- added `web/tests/model-aliases.test.mjs`
- modified `web/tests/pricing-state.test.mjs`
- modified `web/tests/pricing.test.mjs`
- modified `web/tests/public-catalog-state.test.mjs`
- modified `web/tests/public-catalog.test.mjs`

</details>

## 2026-11-14T04:18:34Z · e21cd00b61e8

PR #8 合入后回合 main 到 local-mvp；034.1 验证记录与冻结规格最终同步

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2192 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/034-coin-units.md`

</details>

## 2026-11-14T04:37:29Z · 76f5a885b37d

034.1 不可变历史修复：追加 current_005 恢复原 operation/audit 字节，读取时投影 COIN；D-034-11 与 §15 冻结状态一致性修订；迁移、catalog、operations 与定价 HTTP 已复核

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2193 个源文件；本次 6 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/design/direction-review-2026-11-14.md`
- modified `docs/verification/034-coin-units.md`
- modified `internal/catalog/pricing_commands.go`
- added `migrations/current_005_immutable_pricing_history.sql`
- modified `migrations/virtual_coin_postgres_test.go`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-14T12:44:02Z · baa198d3c35c

034.2：角色字段、独立wallet_requests、单人运营设置CAS与审计、付款基准、成对来源和确定性FIFO；同步当前六来源、应用/worker/projection权限、HTTP/UI和串行验证记录；034.3起仍标为后续能力

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2212 个源文件；本次 95 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/operating-reports.md`
- modified `docs/architecture/wallet-sources.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/phase-roadmap.md`
- added `docs/verification/034-2-paired-wallet-sources.md`
- added `internal/billing/credit_funding.go`
- added `internal/billing/credit_input.go`
- added `internal/billing/credit_sources_integration_test.go`
- modified `internal/billing/funding.go`
- modified `internal/billing/funding_integration_test.go`
- modified `internal/billing/funding_sandbox.go`
- modified `internal/billing/funding_sandbox_integration_test.go`
- modified `internal/billing/funding_verify.go`
- modified `internal/billing/lot_runtime.go`
- added `internal/billing/operator_settings.go`
- added `internal/billing/operator_settings_integration_test.go`
- modified `internal/billing/orders.go`
- modified `internal/billing/store.go`
- modified `internal/billing/store_test.go`
- modified `internal/billing/types.go`
- modified `internal/billing/voucher_integration_test.go`
- modified `internal/billing/voucher_query.go`
- modified `internal/billing/voucher_query_integration_test.go`
- modified `internal/billing/voucher_types.go`
- added `internal/billing/wallet_requests.go`
- added `internal/billing/wallet_requests_integration_test.go`
- modified `internal/billing/wallet_sources_application_test.go`
- modified `internal/billing/wallet_sources_read.go`
- modified `internal/billing/wallet_sources_summary_test.go`
- modified `internal/fundinglotscan/wallet_reader_postgres_test.go`
- modified `internal/maintenancetrust/funding_lots_phase_integration_test.go`
- modified `internal/maintenancetrust/funding_lots_prefix_business_integration_test.go`
- modified `internal/maintenancetrust/funding_lots_runtime_procurement_integration_test.go`
- modified `internal/operations/funding_integration_test.go`
- modified `internal/operations/instance_test.go`
- modified `internal/operations/operations.go`
- modified `internal/operations/operations_integration_test.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/payment_roles_test.go`
- modified `internal/operations/wallet_sources_roles_test.go`
- modified `internal/payments/processor_safety_test.go`
- modified `internal/paymentsandboxqa/funding.go`
- modified `internal/periodicbudget/commander_limits_test.go`
- modified `internal/periodicbudget/tx_fixture_test.go`
- modified `internal/pricing/vector_integration_test.go`
- modified `internal/procurementattemptbirth/attempt_postgres_test.go`
- modified `internal/procurementattemptbirth/attempt_sync_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_grant_fault_postgres_test.go`
- modified `internal/procurementrequestbirth/request_birth_postgres_test.go`
- modified `internal/server/auth_integration_test.go`
- modified `internal/server/billing_handlers.go`
- modified `internal/server/binding_isolation_integration_test.go`
- modified `internal/server/capacity_test.go`
- modified `internal/server/channel_defaults_integration_test.go`
- modified `internal/server/external_relay_integration_test.go`
- modified `internal/server/funding_integration_test.go`
- modified `internal/server/gateway_integration_test.go`
- modified `internal/server/main_shutdown_integration_test.go`
- added `internal/server/operator_settings_handlers.go`
- added `internal/server/operator_settings_integration_test.go`
- modified `internal/server/periodic_budget_integration_test.go`
- modified `internal/server/procurement_meter_integration_test.go`
- modified `internal/server/server.go`
- added `internal/server/wallet_request_handlers.go`
- added `internal/server/wallet_request_integration_test.go`
- modified `internal/server/wallet_sources_integration_test.go`
- modified `internal/testsupport/funding019/verify_test.go`
- added `migrations/current_006_paired_wallet_sources.sql`
- modified `migrations/unified_text_metering_guard_matrix_postgres_test.go`
- modified `scripts/backup-restore-drill.go`
- modified `scripts/browser-qa/model_lifecycle_test.go`
- modified `scripts/maintenance-scan-qa/maintenance_integration_test.go`
- modified `specs/034-virtual-coin/implementation-freeze.md`
- modified `specs/034-virtual-coin/spec.md`
- modified `web/src/App.vue`
- modified `web/src/commerce.ts`
- modified `web/src/components/FundingPanel.vue`
- added `web/src/components/WalletOperatorSettings.vue`
- added `web/src/components/WalletRequests.vue`
- modified `web/src/views/OverviewView.vue`
- modified `web/src/views/PlatformView.vue`
- modified `web/src/views/PlaygroundView.vue`
- modified `web/src/views/WalletSourcesView.vue`
- modified `web/src/views/WalletView.vue`
- added `web/src/wallet-operator-settings.ts`
- added `web/src/wallet-requests.ts`
- modified `web/src/wallet-source-details.ts`
- modified `web/src/wallet-sources.ts`
- modified `web/tests/commerce.test.mjs`
- added `web/tests/wallet-operator-settings.test.mjs`
- added `web/tests/wallet-requests.test.mjs`
- modified `web/tests/wallet-source-details.test.mjs`
- modified `web/tests/wallet-sources.test.mjs`

</details>

## 2026-11-14T16:51:51Z · 9637224702c2

034.3：refund_held/retired五桶、静态cash_refund_orders及独立operation、deferred订单/journal/事件guard、REFUND_HELD账户与checker；现有路径保留新桶且不授生产UPDATE，034.4工作流仍未实现；同步全仓验证记录

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2215 个源文件；本次 28 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/wallet-sources.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/phase-roadmap.md`
- added `docs/verification/034-3-refund-buckets.md`
- modified `internal/billing/journal_batch.go`
- modified `internal/billing/lot_plan_test.go`
- modified `internal/billing/lot_runtime.go`
- modified `internal/billing/lot_runtime_integration_test.go`
- modified `internal/billing/media.go`
- added `internal/billing/refund_buckets_integration_test.go`
- modified `internal/billing/store.go`
- modified `internal/billing/wallet_opening_integration_test.go`
- modified `internal/billing/wallet_sources_application_test.go`
- modified `internal/billing/wallet_sources_read.go`
- modified `internal/billing/wallet_sources_read_test.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/payment_roles_test.go`
- modified `internal/operations/wallet_sources_roles_test.go`
- modified `internal/server/wallet_sources.go`
- added `migrations/current_007_refund_buckets.sql`
- modified `migrations/virtual_coin_postgres_test.go`
- modified `specs/034-virtual-coin/implementation-freeze.md`
- modified `specs/034-virtual-coin/spec.md`
- modified `web/src/components/WalletSources.vue`
- modified `web/src/wallet-sources.ts`
- modified `web/tests/wallet-sources.test.mjs`

</details>

## 2026-11-14T18:28:41Z · 79ee964f0003

034.3b 新增不可变币价版本、CNY 创世、paid receipt 买入版本冻结与存量 manual/sandbox 回填；补齐 USD 失败关闭、三角色 SELECT-only 边界，并消除 Reserve/结算/消费退款的间接币价读取。同步五桶现状、HLD/LLD 与验证记录；034.4 现金退款仍未开放。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2220 个源文件；本次 21 个源文件变化，架构正文变化：true。

<details><summary>源文件变化</summary>

- modified `docs/architecture/wallet-sources.md`
- modified `docs/design/HLD.md`
- modified `docs/design/LLD.md`
- modified `docs/design/integrated-wallet-media.md`
- modified `docs/design/phase-roadmap.md`
- added `docs/verification/034-3b-coin-rates.md`
- added `internal/billing/coin_rates.go`
- added `internal/billing/coin_rates_integration_test.go`
- modified `internal/billing/credit_funding.go`
- modified `internal/billing/credit_input.go`
- modified `internal/billing/funding.go`
- modified `internal/billing/funding_integration_test.go`
- modified `internal/operations/payment_roles.go`
- modified `internal/operations/payment_roles_functions.go`
- modified `internal/operations/payment_roles_test.go`
- modified `internal/operations/wallet_sources_roles_test.go`
- added `migrations/coin_rate_postgres_test.go`
- added `migrations/current_008_coin_rate_versions.sql`
- modified `migrations/virtual_coin_postgres_test.go`
- modified `specs/034-virtual-coin/implementation-freeze.md`
- modified `specs/034-virtual-coin/spec.md`

</details>

## 2026-11-14T18:29:33Z · 297d979f5221

记录 034.3b 全仓 77 包 race、前端、构建与统一架构检查结果；本次仅定稿验证状态，无模块、链路、数据关系或权限结构变化。

已显式复核统一架构正文、能力状态和源码链接。快照覆盖 2220 个源文件；本次 1 个源文件变化，架构正文变化：false。

<details><summary>源文件变化</summary>

- modified `docs/verification/034-3b-coin-rates.md`

</details>

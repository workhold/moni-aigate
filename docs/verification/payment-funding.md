# 022-B2 支付资金接缝验证

2026-11-01。本记录覆盖正式021迁移、共同入账第三来源、十方向核验/投影重建、三角色权限与六字段资金摘要兼容。本记录时尚不覆盖订单Service、APP通知、Processor/Runner或支付运营页面，它们现由[B3独立接线验收](payment-application.md)；来源lot/原路退款与正式PSP继续后续；本地SQL/helper通过不代表应用支付运营或生产收款验收。

## 迁移与资金合同

正式[021](../../migrations/021_payment_sandbox.sql) SHA256为`757721a8559ce6710972759aaa1eb4bbd1a4af6d0b1a058d45f69c121643323d`，999行。它从根完整审读、独立审查与隔离原型收口后追加，默认policy off/revision1、无marker/B业务；不改写001–020。原`funding_journal_valid/funding_fact_guard`保留OID/owner/签名/依赖，A静态分支与sandbox分支分离。

根审定的最终离线候选在随机schema、三独立LOGIN验证45项通过、24项预期拒绝；非空A人工/券/消费/退款/unknown资金摘要升级前后相等，原函数身份不变。三类历史错绑（selection首evidence与identity不同、identity原处理代次坏、identity原evidence非终态）能被实际APP/worker只读重验检出。该候选证据属于迁移设计/SQL原型，不替代下列正式Go测试。

冻结语义见[B2合同](../../specs/022-self-service-payments/b2-funding-contract.md)。[ApplyFundingTx](../../internal/billing/funding.go)三处使用显式三分支；sandbox locator不带任何验签权限，实际RC worker事务从持久完整图派生租户/金额，账户锁后再次核代次/租约和源。journal operation=capture、reference=order，固定system actor和独立sandbox clearing。只有完整原receipt可幂等返回，缺审计/最终CAS不能提交。overflow仅由checkedAdd确认，不能将任意SQL错误转为可提交review。

## 正式代码验证

专用PG为本机55439/moni_test，随机schema/实际独立APP、worker、projector LOGIN；owner只安装/seed、精确历史损坏与清理。不使用真实资金、评审账号或收费上游。

| 范围 | 已执行结果 |
| --- | --- |
| 新sandbox资金专项 | race 8.886s通过；真实P query验签绑定原request/snapshot/evidence，实际worker唯一入账/原receipt重读、三来源NUMERIC摘要/核验/投影/Rebuild、角色/事务拒绝、AVAILABLE正溢出与共享clearing负溢出、漏audit/CAS回滚、历史错绑逐方向检出 |
| 完整billing回归 | 专用PG完整race 149.776s通过，138个顶层测试；人工/券、预占/结算/释放/消费退款/恢复和采购路径仍通过；vet通过 |
| 支付角色专项 | 专用PG race 7.554s通过；完整operations回归36.517s通过，vet通过。覆盖空marker与重复Provision、三实际LOGIN、单列/PUBLIC/表列grant option、继承/SET/ADMIN多跳、跨身份提升、危险属性、schema CREATE、DB/schema OID、部分表/view、函数体/属性/path/未知函数与可执行definer反例；随机资源精确清理 |
| 根联合专项 | 同时运行billing/operations/periodicbudget/cmd/migrations，限定新资金/支付角色/Verifier/CLI/只读迁移测试的真实PG race均通过：11.106s/6.308s/7.241s/5.760s/3.913s；不等同最终全仓回归 |
| 前端兼容 | funding parser严格六字段、BigInt三来源合计，拒旧五字段/未知来源；模拟capture要求规范UUID，人工来源保持旧TEXT。显式模拟标签与第三筛选，14项commerce专项和全部271项web测试、typecheck/Vite构建通过；该新资金显示尚未做真实main浏览器验收 |

支付函数expected来自审定021静态SQL，共43份函数定义；worker仅31项明示EXECUTE，不含audit两个触发器函数，projector本族零EXECUTE/B表零SELECT。投影runtime仅用pg_catalog验证权限边界；marker中的环境归属由离线owner三角色核验及未来APP启动证明负责，不能扩大其源表权限。

## 固定历史QA边界

生产VerifyFunding/VerifyLedger严格要求最新三来源，没有探表降级。旧M/Q及周期Tx原型显式固定001–019，使用仅QA的[funding019](../../internal/testsupport/funding019/verify.go)：同RR核精确19份manifest、拒payment表/函数和半升级谓词、静态017原谓词CTE加完整NUMERIC账本核验。专项race2.985s/vet通过；调用点已明确区分历史与最新fixture，交叉回归另记。此支持包不挂serve、restore或Rebuild。

## 仍待验收

B3运行证明、领域订单/通知、Processor/Runner和HTTP/UI/main现已接线，专项通过；真实main隔离运行、跨tab原操作恢复及精确故障按[独立B3记录](payment-application.md)验收。B3 D1–D6已由根冻结。现有长期58123预览仍运行020已验包，021及新六字段资金页面尚未发布到该预览。成功数据库COMMIT仅丢回包、完整进程故障/旧备份恢复和正式PSP另列门禁，不使用旧原型或旧二进制结果替代。

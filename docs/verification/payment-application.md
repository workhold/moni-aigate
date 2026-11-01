# 022-B3 应用支付闭环验证

2026-11-01。订单、APP 通知、独立受限 worker、HTTP、前端及普通 main 已接线，真实主进程与浏览器整链、在途 SIGTERM 和真实 Guard 丢失均已通过限定本地验收。本记录将库级测试、真实数据库、真实主进程和浏览器证据分别列出；仅支持本地模拟，无真实收款。来源 lot、原路退款、拒付与正式 PSP 仍依赖后续阶段。

## 已实现范围

- [Service / Processor / Runner](../../internal/payments)：不可变 SKU、激活 CAS、订单/取消、policy、安全视图、raw 签名通知 durable ACK；原 ID/代数绑定、唯一 capture/effect/journal/receipt/audit 与终态 CAS。
- [启动装配](../../cmd/moni-aigate/payment_startup.go)：双私密 manifest、双 nonce、完整 keyset、typed OID/canonical proof、APP/projector/worker 三条 DSN 连接前白名单；listener 必须匹配证明。默认 off 无秘密、支付 worker 或通知；带历史却缺证明启动失败。
- [HTTP](../../internal/server/payment_handlers.go)：会话/CSRF/当前角色/近期再认证、独立有界准入、5 秒含 body、128KiB 完整响应与 8KiB ACK。通知保留原始字节，只在数据库 COMMIT 确认后 ACK。
- [支付页面](../../web/src/components/PaymentsPage.vue)：平台运营与租户订单分开，独立 IndexedDB 原操作 journal，事务 complete 后才发送；未知结果保留原 ID，仅有限次 GET 核查。无客户模拟付款按钮或真实付款链接。
- [离线应用 QA](../../internal/paymentsandboxqa/application.go)：非空 A 历史升级、随机隔离租户/账号、八个独立角色、双 manifest、旧 A 指纹和精确清理；不把 builder 当真实 APP 验收。

## 已执行的分层验证

专用 PostgreSQL 为 literal `127.0.0.1:55439/moni_test`，与长期预览隔离。仅 owner 安装/制造负例；业务测试使用实际 LOGIN，不以 SET ROLE 冒充在线角色。

| 层次 | 结果与范围 |
| --- | --- |
| 完整 payments 包 | 正式目录集成后真实 PG race 30.027s 通过；资金完整性错误分类、取消后不记录及 transport 取消修订后全包 race 29.725s 再次通过。含实际 Service/Processor、权限与并发、wire fault、运行证明及调度。 |
| 装配与调度专项 | race 1.601s 通过；两个共享网络槽、四证据槽、完整 1 秒间隔、Close join/超时、正常取消与 Guard 失败分开。库级测试不等于实际 SIGTERM。 |
| HTTP 合同 | pure/mock race 2.490s、vet 通过；联合 payment/commerce 专项 2.274s 通过。严格 body/query、UTC6、错误白名单、准入、慢 body 与 keepalive deadline 清理；此层不证明真实数据库 HTTP。 |
| 前端与构建 | 全部 286 项 Node 测试、strict TypeScript/Vite 构建、全仓 vet 通过。原生 IndexedDB 与真实两个独立 tab 在隔离 Chrome 中验证原子 admission、事务 complete、原编号恢复、实际 reload、旧编号不能删除、身份隔离、取消回滚、128 上限、损坏键全命名空间失败关闭和跨 tab 提示；见[浏览器存储证据](payment-application-022-indexeddb.json)。这不替代真实 APP 页面联验。 |
| config/main 纯装配 | race 1.844s / 1.831s 通过；默认 off、live/半配置拒绝及首次连接前配置验证。 |
| APP QA builder | 真实 PG race 25.889s 通过，脚本 race 1.578s、vet 通过；双 manifest 经生产 loader/构造、M/Q 原身份自证、worker/projector 负权限、随机六类账号及完整八角色/三 schema/秘密清理。prepare-only 脚本实际通过，明确跳过 main。 |
| 真实普通 main 与 mock | 两轮完整进程运行通过；最终包嵌入已构建前端，真实 cookie/CSRF 登录、SKU/策略发布激活、P 通知→APP→worker 唯一资金、重复 ACK/原 operation 重放、Recovery 只 query、P 提交后 HTTP 回包丢失的原 command 恢复、证明 off 历史收尾、无证明历史拒启、第二实例固定锁原因拒绝、正常 SIGTERM 与确认提交后 SIGKILL 重启通过。四笔各 250 的模拟入账均有唯一资金图；见[结构化进程证据](payment-application-022-process.json)。 |
| 真实浏览器 | 最终包实际密码登录无需绑定 MFA；延迟 `/me` 仍保留登录 redirect，平台 UI 发布/激活、owner 建单/取消/回执和钱包来源、finance/viewer 只读、1440px/390px 无横溢通过。原生 IDB 原子性与两个 tab 并发、GET404/两次实际 reload 保留原 ID 通过；见[页面证据](payment-application-022-browser.json)及[存储证据](payment-application-022-indexeddb.json)。 |
| 精确资源清理 | 两轮所有自有子进程 join、运行时连接归零、旧非空 A 资金指纹不变、三 schema/八角色清理通过；外部确认含随机凭据、manifest、日志和二进制的整个私密根目录已删除。 |
| B3 全仓回归基线 | `TEST_DATABASE_URL=专用库 go test -race -p 2 ./... -count=1 -timeout 600s` exit 0，server 277.004s、billing 137.624s、identity 233.854s、payments 30.276s。此命令启动早于新增 C0 纯算法与随后生命周期脚本，不把此基线扩写成后续新增文件的全量证明；新增部分各自专项，最终汇合后复核。 |
| 真实在途退出/失锁 | 第三轮完整普通 main/mock 进程 exit 0，22 阶段 PASS；通过物理 merchant gate 证明真实 worker 网络已到 P，再 SIGTERM 或精确终止本 APP 的唯一实例锁连接。P 取消分别 2ms/4ms，贯穿观察总 age 16ms/51ms，均早于原 5 秒超时。APP join、三 LOGIN 连接归零及 P 无效果后才解 QA gate；原 60 秒 lease 自然到期后，以同 command/digest/canonical query 恢复 ready，P 各唯一且无新 capture，原四笔资金与非空 A 不变。见[独立生命周期证据](payment-application-022-lifecycle.json)。 |

专项准确入口及运行合同见 [B3 runtime](../../specs/022-self-service-payments/b3-runtime-contract.md)、[HTTP/UI](../../specs/022-self-service-payments/b3-http-ui-contract.md)、[web](../../specs/022-self-service-payments/b3-web-contract.md) 与 [APP QA](../../specs/022-self-service-payments/b3-application-qa-contract.md)。先前 B2 的第三来源/十方向资金核验保留在[独立记录](payment-funding.md)。

## 正在执行与保留门禁

真实 Guard 丢失和实际在途网络调用期间的 SIGTERM 已按 [APP QA §9](../../specs/022-self-service-payments/b3-application-qa-contract.md)补充合同独立验收；测试未耗尽整个 310 秒 HTTP 排空期限，不证明025多活。第三轮同样完整清理三 schema、八角色、维护/管理员连接及所有秘密，未操作58123。旧 process/browser JSON保留原轮未验范围和二进制摘要，新的补验结论仅存独立 lifecycle JSON。B3 全仓真实 PG race 已通过上述基线；最终架构清单及检查在并行结果汇合、语义复核后执行。

成功数据库 COMMIT 仅丢回包仍无真实 wire 注入设施，明确 SKIP；P 的 HTTP 回包丢失是另一已验边界。旧备份恢复继续独立门禁。当前未替换 58123 已验收的 020 预览、修改评审账号或 MFA，也未接触真实资金。容器 daemon、正式 PSP/供应商凭据、生产环境及最终性能对比仍各自验收。

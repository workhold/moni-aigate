# 周期预算023-B核心实施与验证

2026-11-01。范围为020追加迁移、纯协议层、独立Reader/Commander、同事务helper、billing预占/采购/派发/收尾、应用/投影有效角色检查及管理HTTP/控制台。核心全Go与HTTP专项、前端、隔离真实main浏览器、最后HTTP相关全包回归及020评审预览升级已通过；独立只读核验器和完整产品验收继续，不将模块验证称为周期商品或商业订阅交付。

## 实施与设计依据

- [核心接线合同](../../specs/023-periodic-products/b-integration-contract.md)、[完整语义/锁图](../../specs/023-periodic-products/b-contract.md)、[HTTP/DTO/cursor](../../specs/023-periodic-products/b-http-contract.md)。
- [Tx helper](../../specs/023-periodic-products/b-tx-contract.md)、[Reader](../../specs/023-periodic-products/b-reader-contract.md)、[Commander](../../specs/023-periodic-products/b-commander-contract.md)分片先冻结后编码；[设计反例与SQL证据](../../specs/023-periodic-products/b-verification.md)保留历史，不覆盖成新的产品验收。
- [020迁移](../../migrations/020_periodic_budgets.sql)按已审核心/命令SQL与固定namespace追加，不创建默认control或伪造旧admitted_at。只有已配置scope记账；未配置返回unconfigured。
- [billing接线](../../internal/billing/periodic_budget.go)：客户R按tenant/Key各记录，单次钱包预占；采购每attempt完整U；最后派发同At检查；结算/释放/采购更正回原窗。退款不降低周期spent，关闭cap仍跟踪，占用不随自然换期消失。
- [角色配置与核验](../../internal/operations/periodic_roles.go)收回表及单列旧ACL，再逐列授权，检查PUBLIC/继承/SET/ADMIN闭包与owner/schema权限；main在listener前核验实际APP/projector LOGIN。历史019隔离fixture仅在六表全不存在时可跳过此扩展。
- [显式历史前缀安装器](../../specs/025-production-ha/migration-prefix-contract.md)固定独立M/Q版本1与SQL原型的001–019基线，常规Apply/Verify仍要求最新。新业务迁移不混入旧沙箱attestation。

## 已执行的独立分片验证

专用PostgreSQL为本机55439的moni_test；全部随机schema及精确角色finally清理，不修改评审账号/真实资金，不访问收费上游。

| 分片 | 证据与边界 |
| --- | --- |
| 纯协议 | 金额/revision/UTC6规范值、strict五字段PUT、重复/转义键与surrogate拒绝；cursor独立HMAC、canonical字节、路由/作用域绑定，PG/Python/Node golden与纯race/vet通过 |
| Tx helper | 实际APP/projector LOGIN、原A双审非空资金基线，客户双scope/原effect幂等、跨tx/令牌与Admission篡改拒绝、采购完整U/legacy/catchup、同At许可、cap安全分类、源/投影守恒及负权限；专项和整包真实PG race通过 |
| Reader | 当前角色/归属/撤权、revoked历史、四类unconfigured无写、原receipt与入口隔离、时钟退行仅挡当前view、101窗50/50/1及51条同time C序分页、cursor第50行与父取消有界；真实PG race通过 |
| Commander | 三scope first/replay/no-op/change、当前权限/锁等待撤权、全局operation异体冲突、1001零事实拒绝/1000精确导入、源不回锁、MaxInt64、真实死锁有限重试、audit原子回滚、body/COMMIT期间断连销毁与unknown分类；最终分片PG race12.825s及vet通过。未模拟成功COMMIT仅回包丢失或真实40001 |
| 迁移前缀与角色 | 前缀完整预检、指纹/未知/未来记录拒绝前无写、真实锁等待和取消；实际LOGIN精确有效ACL/重复Provision/PUBLIC/列授权与角色提升反例、019兼容和部分表拒绝，专项真实PG race通过 |

## 核心接线汇合

本轮以真实020和真实APP LOGIN新增四份billing交叉测试，最终15组联合race通过（12.687s），vet通过。客户测试覆盖双预算单钱包、退款不减spent、禁用仍跟踪、跨日原窗收尾、首次配置与Reserve双向真实等待；采购测试覆盖完整U、累计和周期预算交集、第二attempt、unknown/conflict、双审绝对更正和legacy catchup；派发测试覆盖三scope限额、并发唯一许可、最后账户等待后的跨日/Key过期/报价生效；恢复测试真实等待短request_deadline，核对prepared→released及sending→unknown的019事件与周期mirror精确对应、客户R/钱包不变及重复恢复零新事实。

旧账务回归首次因020禁止修改reservation身份/截止而暴露四处旧测试夹具；修订仅允许随机schema的owner在单事务暂关该一个身份guard、设置压缩截止、执行全部deferred检查并恢复guard。该fixture不是生产修改期限能力，也不声称实际等待24小时。修后相关客户策略、钱包锁等待、真实SIGKILL恢复和采购提案专项race通过（14.009s）。

旧operations元数据测试的request-only夹具改为实际APP同事务request+零金额reservation，无资金操作，不以此替代Reserve商业测试。旧pricing元数据夹具同样补零金额来源，原018快照负例绑定精确P0001原因，避免020 guard掩盖回归；最终pricing专项race通过（4.622s）及vet通过。main保留既有alert角色失败分类，新增独立periodic角色分类；原真实main告警拒绝/崩溃恢复专项race通过（14.217s），不冒充新周期HTTP/main产品验收。

迁移取消测试以精确取消backend PID的pg_locks/pg_stat_activity及连接drain证明清理，独立连接正常有界等待生产锁，避免把其他包合法迁移排队误判为泄锁；专项race（2.078s）与取消/普通完整迁移各8轮并行通过。生产锁键和安装器实现未因测试修改。

020核心接线的最终全Go真实PG race全部通过（server 283.620s、billing 154.523s、identity 241.777s、periodicbudget 61.547s、pricing 21.743s、migrations 20.457s）；251项前端测试、strict/Vite构建、全仓vet及真实main编译均通过。此基线早于新增B HTTP/UI代码，不能冒充后续产品接线最终验收。上述交叉未覆盖HTTP/真实main、真实供应商、request及24h期限的最后锁等待、多采购账户隔离、并发采购周期竞争、成功COMMIT仅回包丢失与旧备份恢复。

## HTTP、控制台与隔离真实main

已按冻结HTTP合同接入四类资源入口、当前预算/窗口/占用列表及原操作核查；main在listener前初始化Reader/Commander。独立有界准入保留总读32、操作核查8、写8，单actor读4/核查1/写1；父工作期限分别5/10秒，不在重试时重置。严格body/query与128KiB完整预编码避免部分响应；Guard沿原实例所有权边界。

新增HTTP专项使用真实020和实际APP LOGIN，8组真实PG HTTP及13组纯单元顶层测试联合race通过（11.808s），相关vet通过。覆盖当前权限/撤权、四类资源与原回执、51条分页、strict输入/CSRF/恢复/Guard、有界准入、原子事实快照、连接断开及ResponseWriter失败。未模拟成功COMMIT仅回包丢失。

控制台增加独立预算页、精确金额/DTO/transport、身份epoch和IndexedDB journal。专项19项与全部270项web测试、typecheck和Vite构建通过。真实Chrome原生IndexedDB验证唯一pending、事务complete后才发送、两个字面标签页竞争及第三标签恢复、原ID条件删除和损坏元数据拒绝；独立端口、profile和测试记录清理，脱敏结果保存于本地验证目录。

最终真实main和内嵌bundle采用SHA256 `95d14e337c35347085bddc7041a3c5d62a7b36c3f50f01697565d9a0a43d5913`，在随机schema/独立受限角色与本地mock上运行。浏览器通过tenant首次配置及修改、原receipt不可变、以真实receipt.actor查询的终态journal删除、采购配置、wallet只读、owner Key配置；390px移动布局无页面横向溢出。截图等待弹窗和侧栏动画结束后另行核对，人工检查[桌面](assets/periodic-budget-023-desktop.png)及[移动](assets/periodic-budget-023-mobile.png)通过，不把过渡帧作为最终视觉证据。

同一隔离main通过HTTP将合成渠道凭据绑定至采购账户，再发出一次本地mock模型请求：一个客户request、一个attempt和一个settlement；tenant/Key各有一条同request的周期allocation，spent均为8微元，采购spent为12微元，三个held均为0。初次配置从下一完整UTC周期限额生效；本次证明scheduled阶段持续跟踪与结算，不声称已实测下一自然日的限额触发。无收费上游调用。最终manifest确认main优雅退出、schema/角色无残留、账号/供应商秘密已清空；附加私密fixture文件已移除。

最终HTTP相关全包真实PG race通过：server 256.630s、cmd/moni-aigate 2.273s。该回归对应020 HTTP/UI版本，早于后续独立核验器与021支付迁移，不作为后续源码的最终回归证据。

020预览升级已完成：[脱敏记录](periodic-budget-023-preview.json)。旧进程准确识别后优雅排空，迁移与角色配置完成，29张身份/资金相关表的事实摘要升级前后相等；没有修改评审账号、密码或MFA。当前58123运行上述已验020二进制，ready且非恢复模式；首页及52项静态资源与验收bundle逐字节相等，实际受限APP/projector启动检查通过，configured controls为0。后续021支付代码仍在独立验证，尚未替换该预览。

## 独立只读守恒核验器

依据[冻结合同](../../specs/023-periodic-products/b-verifier-contract.md)实现独立Verifier与`verify-periodic-budgets`CLI。完整001–021迁移、实际LOGIN/六表owner、固定来源列及无RLS在同一RR READ ONLY连接验证；不调用在线预算helper，不取业务行锁，也不按当前clock/权限/价格重判历史。19类异常与3类缺证分别报告，未完成扫描不返回部分成功。

新增Verifier/VerifyReadOnlyTx专项真实PG race通过（8.802s/3.703s），相关vet通过。实际APP双人充值、客户tenant/Key与managed采购U、成本结算/绝对更正/消费退款均核验异常为0；历史actor与Key撤权不影响合法旧事实。旧未决导入、legacy catchup冻结baseline30及后续成本25归零保留正确语义；缺证时indeterminate。整份零额allocation或零delta镜像被删、即使调平所有SUM/revision仍能查漏；coherent NaN跨源/PE/预算SUM相等仍mismatch。坏snapshot按异常计数，缺最新/坏checksum返回零报告；业务行锁不阻塞、DDL阻塞受父100ms期限约束，超时清理后pool可继续使用。

CLI通过真实main/退出路径子进程：空库和无事件配置balanced退出0；合法空窗但原snapshot缺创建见证indeterminate退出3；孤立audit触发mismatch退出2；实际APP拒绝和坏checksum退出1、stdout为空且stderr固定安全码。该分支没有serve密钥/配置、实例锁、worker或listener。专项race4.517s通过；根五包联合专项全部通过。完整相关回归在后续修改历史fixture接缝后重新执行，不把上述专项当最终全仓验收。

## 继续保留的集成门禁

独立全量只读守恒核验器、大样本分页EXPLAIN、完整恢复/旧备份/COMMIT精确丢包故障、商品/订阅和最终性能仍分开完成。生产时钟/真实上游月账单与外部部署认证不由owner时钟fixture替代。

020核心新增的事务检查与往返尚未进行性能复测；旧TTFB/p95基线不代表当前实现。按既定顺序先完成产品与资金验收，再进行002/008最终性能阶段，保留全部商业检查。

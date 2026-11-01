# B3 支付前端实施附录

2026-11-01，细化已冻结的 [B3 §3–5、§9](b3-http-ui-contract.md)。本附录是实现约束，不代表真实 APP HTTP、跨 tab 浏览器或商业支付验收。支付页独立于 A 的 sessionStorage 协议，沿现有 Vue / Element Plus 布局。

- 新入口 `/payments`（当前 tenant owner/billing_viewer）和 `/platform/payments`（admin/finance_operator/finance_approver），路由、导航和共享资金兼容由根集成。新增组件自行重新读取当前身份，actor、tenant、精确角色、CSRF、再认证/MFA 上下文或路由变化即销毁旧页面实例。每个请求另有代数；离页、身份改变和 deadline 取消旧请求，迟到结果不能写快照或删除 journal。
- 新模块 `payments*.ts` 自有严格 raw JSON parser、DTO、请求白名单和受控错误词典。响应最多131072 bytes，流式有界读取、fatal UTF-8、重复字段/坏 surrogate/非规范数字/未知键拒；人类请求最多8192 bytes；只发 GET/POST/PUT，credentials=include/cache=no-store/redirect=error。浏览器 deadline 为12秒，不重试写。支付字段中的金额、版本和时间遵守 B3，不经 Number 处理金额，不将 checkout_ref 渲染成链接。
- IndexedDB 固定独立数据库 `moni-aigate-payments-v1`、version1、单 store `pending`，out-of-line 主键 `[actor,scope,kind,target_id]`；value 精确为 `{operation_id,kind,scope,target_id,created_at}`。actor 只在 key 命名空间，scope 与 value 相等。原子 readwrite transaction 中核全部当前 actor+scope 记录、128上限、唯一目标并 add；只在 transaction complete 后允许发送。已有目标恢复原编号，不发送。任何损坏/abort/存储不可用/满额都阻断新写，无降级存储或清空按钮。
- 删除仅用于当前 epoch 严格绑定的 terminal receipt 或 B3 §9 提交前安全拒绝。finish 逐字段比对原 journal，且监听当前 ticket 的 AbortSignal；身份/路由变化发生在事务完成前则 abort 删除。GET404/403/离线/失真均不清锁；跨身份不枚举其他人的 pending。BroadcastChannel 只通知重新只读读取，不作为锁。
- 操作状态 `idle → journaled → submitting → settled|unknown`；reload 一律恢复 unknown。手动查询仅 GET 原操作。当前可见且在线时最多5次自动查询，退避1/2/5/10/20秒，之后保留原编号和显式只读核查按钮；离页/隐藏/离线取消或停止，不能发出新 POST。所有列表固定服务端50项，用 null 结束并检测重复游标，避免无穷翻页。
- policy 独立 CAS，无伪 operation ID 或资金命令 journal。写结果未知时停用编辑，GET仅展示当前配置，保持“原请求结果未知”提示；读取成功后用户显式“基于当前版本重新编辑”才开始新意图，使用新读 revision，不自动提交。policy 不在普通 pending journal 中存请求或正文。
- 平台读安全 SKU/订单/指定 logical order UUID 的观察；unknown_order 允许无订单行的观察查询。admin 才有发布/上下架/policy；钱包 owner 才有下单/取消，billing_viewer 只读。所有支付区域持续显示“本地模拟，无真实收款”，credited 为“模拟钱包到账”，没有模拟成功/强制到账/支付URL/回调URL输入或真实付款二维码。

验证分层：Node 专项验证 strict DTO/raw/有界传输/epoch/未知和上限算法；真实 IndexedDB helper 验跨独立连接与 reload/事务完成/abort/128上限，由根在真实浏览器执行并记录。fixture、类型检查、静态组件检查均不能替代真实 APP HTTP/浏览器验收。

# moni-aigate 控制台

Vue 3 + TypeScript + Element Plus 的独立控制台。接口先定义在 [console contract](../specs/001-commercial-mvp/contracts/console.md)，再按客户与运营场景实现。

## 开发与构建

要求 Node.js 22.12+（测试脚本使用 Node 22.23 的类型剥离能力）。

```sh
npm ci
npm run dev
```

Vite 在 `http://127.0.0.1:5173` 提供页面，将 `/api`、`/v1`（含 `/v1beta`）与 `/health` 请求代理至 `http://127.0.0.1:8080`。联调时为服务端设置 `PUBLIC_ORIGIN=http://127.0.0.1:5173` 与 `COOKIE_SECURE=false`，并从该地址访问前端，保留 CSRF/Origin 校验。生产环境使用同源 HTTPS，恢复安全 Cookie。

```sh
npm test
npm run build
```

生产构建输出 `dist/`，Go 的 `web.FS` 嵌入这些资源。源码未构建时，`embed.go` 提供明确的构建说明页，避免 `go test` 因缺少前端资源失败。`dist/.gitkeep` 是源码占位符，构建插件会保留它；哈希资源不纳入源码版本。

## 已实现交互

- Cookie 会话、租户切换、MFA 绑定/验证与恢复码展示、敏感操作再认证、验证当前密码与 MFA 后修改登录密码。
- 客户成员与权限、模型与售价、API Key 创建/撤销及一次性明文、调用明细、钱包与流水。
- Chat / Responses / Messages 模型协议配置与目录展示；API 联调页使用真实 Key 发送单轮文本 JSON/SSE，支持取消、request_id 跳转账单、原生用量和单次浏览器端耗时。
- 平台客户/成员/授权、平台财务账号、渠道轮换、模型/路由 CAS 发布、未来售价版本。
- 人工核款、不同账号审批、原消费钱包退款、费用核对与操作审计。
- 客户累计资金/请求报表、平台按客户汇总与尝试成本分页、财务成本更正、管理员追加渠道成本版本。
- 平台恢复状态查看、独立财务角色提交/审批恢复提案，以及批准后交由运维重启的明确提示。
- 平台专属路由与尝试明细：路由版本、候选权重/优先级和中文筛选原因；普通租户不调用平台尝试接口。
- 未加入客户工作空间时，API 联调显示接入指南；加载、空数据、错误及游标分页，小屏侧栏和布局。

权限与资金规则由服务端执行。前端隐藏无权限入口不会替代服务端鉴权；财务制单使用会话内稳定幂等标识，不自动重试写请求。

## 数据与安全边界

金额使用十进制字符串和 BigInt，支持超出 JavaScript 安全整数范围的微元数值；未知金额显示破折号或“待核对”。供应商密钥和客户 Key 不写入 localStorage、URL 或观测服务。密钥弹窗关闭时立即清除内存展示值。前端不引用外部字体、统计脚本或 CDN 运行资源。

## 验证记录

2026-10-30：`npm test` 的 12 项测试覆盖资金精度、未知成本、HTTP 合同与三协议联调客户端。新增流测试覆盖 UTF-8/CRLF 分片、usage、成功终态、缺少终态、billing_pending、HTTP 拒绝、主动取消和无自动重试；`npm run build` 包含 TypeScript 严格检查并通过。真实 PostgreSQL + Go 后端 + Chrome 验证 MFA、客户/成员授权、Key 创建与撤销、双人核款和精确钱包余额。390px 断点无横向整页溢出，浏览器无 error/warn。详细链路及新增模块的验收状态见 [控制台验证记录](../docs/verification/console.md)；未使用真实供应商或真实收款。

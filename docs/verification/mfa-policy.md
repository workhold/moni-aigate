# MFA 默认可选的实施验证

2026-10-30，依据开发者明确产品指示和 [ADR-003](../decisions/ADR-003-optional-mfa.md)。本记录承接早先强制MFA基线，历史验证不改写。

## 实施

- `MFA_ENFORCE_PLATFORM`缺省false，Config→Server→Identity统一传递；Login和每次Session读取均据当前策略计算标志，未绑定的平台/财务账号不再被引导强制绑定。配置改变后重启应用生效，无需迁移或修改账号。
- true保留平台未绑定账号强制绑定；旧密码会话即使`mfa_verified=true`也不能绕过`enrollment_required`。普通租户账号不受平台绑定策略强制。
- 已主动启用MFA的账号始终要求动态码/恢复码，关闭强制不删除绑定或伪造验证。绑定、恢复码单次使用和会话撤销沿用原合同。
- 前端可选状态显示说明与“返回工作台”；强制状态仍有警告。扫码二维码继续由浏览器本地生成，保留手动备用入口。登录页不再宣称平台/财务一律必须MFA。
- 示例环境、`init-env`产物和Compose默认一致；近期密码再认证、角色分工、CSRF和财务双人审批保留。

## 执行证据

真实PostgreSQL16.15，隔离schema；沿用本地专用测试DSN，不修改review或其他开发库。测试中所有账号、OTP和恢复码均为临时fixture，不是用户绑定信息。

| 检查 | 命令/范围 | 结果 |
|---|---|---|
| 配置与MFA专项 | `go test -race -count=1 -timeout 120s -run 'TestHTTPMFA\|TestMFAEnrollmentPolicyConfiguration' ./internal/config ./internal/server`，设置测试DSN | config 2.127s、server 52.796s，通过 |
| 全身份回归 | `go test -race -count=1 -timeout 120s ./internal/identity`，设置测试DSN | 67.322s，通过 |
| 全服务端回归 | `go test -race -count=1 -timeout 180s ./internal/server`，设置测试DSN；容量默认跳过 | 110.757s，通过 |
| 静态与嵌入构建 | `go vet ./...`；`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` | 通过 |
| 前端 | `npm run test`；`npm run build` | 5项测试、严格TS与Vite构建通过 |
| 运行预览 | 重启本工作区58123服务；`/health/ready` | HTTP200，ready；HTTP实际返回的入口和SecurityView资源与最终dist逐字一致 |
| 浏览器 | 隐藏临时IAB页读取安全页，点击“返回工作台” | 现有未绑定review会话显示“MFA为可选”，成功进入“平台工作台”；error/warn日志为空。临时页已关闭，用户原页保留 |

专项覆盖：管理员/财务制单/财务审批默认密码登录可用；旧待绑定会话不再受阻；平台显式强制仍拦；租户不被平台策略强制；打开强制能拦旧已验证密码会话；主动绑定返回标准otpauth、TOTP启用与会话轮换成功，默认策略下后续登录仍须第二因素；恢复码完成验证；关闭策略不抹除MFA；近期密码过期仍拒写，再认证后admin可写，财务不能越权创建平台账号。

本机预览仅保存`MFA_ENFORCE_PLATFORM=false`配置，未代用户生成/启用/关闭MFA或修改用户因素。真实手机扫码仍由用户自行完成；此前合成二维码独立解码证据见 [控制台记录](console.md#mfa-绑定引导增补)。容器实际启动未在本轮执行，Compose透传属于配置校准。

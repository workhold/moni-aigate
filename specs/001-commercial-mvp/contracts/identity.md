# 身份与授权实施合同

基线：2026-10-30 用户同意 r3 后实施；FR-01/02、NFR-02。同日开发者明确要求MFA默认不开启、方便测试联调；以下可选策略覆盖早先强制MFA基线，见[ADR-003](../../../docs/decisions/ADR-003-optional-mfa.md)。

- 管理身份只接受高熵 Cookie 会话，推理只接受 Bearer Key；token使用独立 AUTH_SECRET 的 HMAC-SHA256摘要，密钥不写日志。密码为 bcrypt cost12，12–72字节。
- 平台角色互斥为 admin / finance_operator / finance_approver / none；admin不能冒充财务审批。`MFA_ENFORCE_PLATFORM`缺省false：未启用MFA的账号可密码登录并访问获授权业务，不要求绑定；true时未绑定平台/财务账号返回`enrollment_required=true`，仅可访问me/设置或验证MFA/密码再认证/登出。已主动启用MFA的账号在任意配置下都须验证第二因子，密码后`mfa_required=true`；未验证前不可访问业务。切换配置不清除绑定，不绕过已启用因素；管理写操作仍需10分钟内再认证，已启用时须同时验证MFA。
- TOTP 30秒、6位、允许前后1窗口，持久 last_step 阻止重放；恢复码一次使用。秘密 AES-256-GCM版本加密。开始设置返回待绑定秘密、otpauth URI与恢复码；本地二维码扫码和手动添加均支持，提交有效动态码后才启用。重新生成仅限未启用的待绑定阶段，使旧待绑定资料失效；未确认设置不会因默认策略自动启用。
- 会话12h有效，登录/完成MFA时轮换；安全Cookie，SameSite=Lax，写请求校验同源Origin和CSRF。开发HTTP需显式 COOKIE_SECURE=false；生产默认true。
- 租户上下文由服务端会话/Key确定；空授权拒绝。Key scopes为model ID数组，`*`仅显式全部，仍受租户和成员授权交集限制。
- 授权的父行同步顺序及READ COMMITTED依LLD，撤销先提交则下一attempt不得派发；推理不支持URL Key、供应商后缀或任意身份header。
- 身份/权限修改与审计同一事务；登录失败可有界丢弃。安全错误不包含SQL/密码/凭据。

# 运营账号恢复 CLI

`moni-aigate user` 用于控制台无法恢复账号时的主机侧应急操作，例如管理员密码或验证器丢失、邮件找回关闭。它不是公开 HTTP 接口，也不要求启动 app/worker。

## 权限与安全边界

- 需要当前应用的 `DATABASE_URL`、`MASTER_KEY`/`MASTER_KEY_ID` 和 `AUTH_SECRET`。数据库连接必须先通过 `VerifyCurrent`，不能指向投影角色或绕过迁移检查。
- 操作者必须已经持有应用数据库凭据和主机访问权。命令不会额外验证控制台用户，也不会把 owner/superuser 当作必要条件；实际 `moni_app` 受限 LOGIN 可执行这些操作。
- `set-password` 只从 stdin 读取一行 12–72 字节密码，不接受命令行密码。不要把密码写进 shell 历史、日志或仓库。
- 每次变更撤销该用户全部会话并写 `audit_events`（actor=`operator-cli`）。`reset-mfa` 同时清除 MFA secret、pending secret、last step 和全部恢复码。
- `suspend` 不会允许停用最后一个 active admin；需要保留至少一个可登录的管理员。

## 命令

```bash
./bin/moni-aigate user list
./bin/moni-aigate user set-password --email user@example.com --password-stdin < /absolute/private/new-password
./bin/moni-aigate user reset-mfa --email user@example.com
./bin/moni-aigate user suspend --email user@example.com
./bin/moni-aigate user activate --email user@example.com
```

`list` 输出用户 ID、邮箱、平台角色、状态和 MFA 是否启用。其他命令成功时明确提示该账号的全部会话已结束。用户不存在、输入无效或最后一个管理员将被停用时命令返回非零状态；不要把失败当作完成。

## 复核顺序

1. 在预览环境用实际受限 app LOGIN 执行 `user list` 和一次目标账号恢复，确认 audit 行、旧会话失效以及新密码可登录。
2. 用 projection 角色重试，必须在迁移/权限校验阶段被拒绝，不能用 owner 绕过形成“通过”记录。
3. 对 MFA 强制开启的部署，重置后通知用户在下一次登录重新绑定验证器。
4. 删除临时密码文件；保留审计与变更原因，不记录秘密。

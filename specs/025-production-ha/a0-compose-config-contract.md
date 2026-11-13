# 025-A0：Compose 自助身份配置透传

2026-11-01，根冻结的静态配置窄切片。只修复已实现 021 在 Compose 中缺失的配置透传，不开放 HA，不改变身份、邮件或支付合同，不代表容器运行已验收；1Panel 等面板部署为可选项，不属于本合同。

## 范围与默认值

仅 `app.environment` 增加 `AUTH_REGISTRATION_ENABLED=false`、`AUTH_PASSWORD_RESET_ENABLED=true`、`MAIL_TRANSPORT=disabled`，以及 SMTP_HOST/PORT/FROM/USERNAME/PASSWORD/TLS_MODE；SMTP 字段默认空，TLS_MODE 默认 starttls。默认部署仍不发送邮件、不开放注册；恢复能力仍需有效邮件配置和当前身份合同才能使用。

迁移/provision 服务不获得 SMTP 配置、APP/PROJECTOR 之外的新运行角色或支付秘密。变量来自受保护的部署环境，不包含在镜像、公开能力、示例明文值或验证日志中。`MAIL_TRANSPORT=disabled` 时沿现有 loader 丢弃未使用 SMTP 秘密；smtp 模式沿现有严格 origin/TLS/地址/账号密码校验，不增加弱化开关。

022 本地沙箱要求 literal 专用测试库与私密证明，常规 Compose 不传 manifest 或 worker DSN，不提供绕过容器目标限制的部署路径；PAYMENTS_MODE 默认 off，误设 sandbox 无证明必须拒启，live 仍拒启。注册、邮件、券和告警开关各自独立，不从一个总开关推导。

## 验证与交付

先审 `internal/config/config.go` 和 `mail.go` 的真实变量及默认值，再修改 compose。静态检查 app 的默认值/显式覆盖、迁移与 provision 的环境隔离，以及原 app/projector 分离、loopback 端口、320 秒排空与只读 rootfs 保持。使用合成环境，禁止读取/输出用户 `.env` 或真实 SMTP 凭据。

优先 `docker compose --env-file <仅合成值的临时文件> config --quiet`；插件/daemon 缺失须如实保留，YAML 与插值静态检查不能冒称容器运行。Docker build、真实 Compose 启动、正式 SMTP 收件、备份恢复和发行仍按 025 独立验收；1Panel 等面板部署可选，不作验收条件。同步部署手册、统一架构和验证记录，待并行汇合后由根统一 refresh。

## 后续实际CLI补验

2026-11-01补验已通过：未安装全局插件，使用Homebrew官方bottle SHA验证的隔离Compose 5.6.1；默认与显式合成覆盖均实际config --quiet/JSON通过。清空继承环境、显式私密合成env、空DOCKER_CONFIG、指定不存在的daemon socket，不读取用户.env/凭据或操作daemon；合成文件已清理，见[独立新证据](../../docs/verification/compose-render-025.json)。原静态JSON的NOT RUN保留其历史轮结论，构建/容器/1Panel/SMTP继续独立验证。

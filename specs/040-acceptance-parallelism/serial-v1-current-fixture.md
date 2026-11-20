# 040 后续：serial-v1 identity 夹具使用 current baseline

2026-11-20；状态：小优化候选。本文只调整身份测试的数据库夹具初始化，不改变默认 serial-v1、workers=2 opt-in、产品代码或验收覆盖。

## 1. 观测与选择

精确 main 提交 `c0d02b14ba711330ff432aa126573f2f1179190a` 的 serial-v1 完整 029 原始 Go JSON 中，`internal/identity` 包耗时 311.097 秒，61 个顶层测试合计 309.400 秒。`newIdentityFixture` 被 48 个顶层测试调用，`newSelfServiceEmptyFixture` 被 3 个调用；两者都在新的隔离 schema 中通过 `migrations.Apply` 逐个执行历史 001–024，再运行身份断言。生产 current-only 入口以及 server、billing 的现行夹具已经使用 `ApplyCurrent`。

在同一工作树和同一 55439 专用 PostgreSQL 上，用 Go overlay 而不修改源码做交错定向实验：

- `TestIdentitySessionRotationAndTenantBoundary`：历史路径三次为 6.781、6.656、6.386 秒；current baseline 三次为 6.213、6.265、6.296 秒。
- `TestSelfServiceRegistrationInitializationMinimumPrivilegesAndSingleUse`：历史路径三次为 8.508、8.312、8.134 秒；current baseline 三次为 7.914、7.755、7.756 秒。

这些短实验只支持“current baseline 可减少每个新夹具的初始化成本”这一可证伪假设，不外推整单收益。只有同一干净提交、同一专用环境的完整 029 才能作为最终收益证据。

## 2. 接口与状态机

公开接口、Go 函数签名、SQL、迁移文件、数据库角色和测试清单均不变。每个身份测试继续保持独立随机 schema 和独立连接池：

```text
empty isolated schema
  → ApplyCurrent / apply all current-only files
  → current schema ready
  → unchanged identity fixture and assertions
  → close pool / drop only the owned schema
```

`ApplyCurrent` 仍先取得既有 migration advisory lock，检查空 schema 或合法 current history，并按不可变 checksum 执行 current baseline 与全部追加迁移。任何安装失败都使测试失败，不能进入身份断言；清理只删除本测试创建的 schema。数据库连接、提交或清理失败不得被转换为 PASS 或 SKIP。

## 3. 覆盖与故障边界

- 两个夹具只把安装器从 `migrations.Apply` 改为 `migrations.ApplyCurrent`；密码散列成本、账户/角色数据、MFA、邮件、Key、权限、锁等待、超时和全部断言保持不变。
- 身份包不是历史升级矩阵。`migrations.Apply`、`ApplyThrough`、历史拒绝和 legacy fixture 的专门测试继续存在并执行，不删除测试、不增加 SKIP、不修改版本化 SKIP baseline。
- current baseline 与历史安装器仍是两个明确入口；本变更不授权旧库升级，不让生产服务调用 `Apply`，也不改变 current-only 拒迁规则。
- 不改 race、`-count=1`、`-p 1`、包/进程超时，不在同库并发测试，不接触预览、支付、邮件、Compose、浏览器或 workers=2 调度。

## 4. 验收

1. 两个定向测试和完整 `internal/identity` race 在 55439 专库通过；顶层测试数量与必需身份覆盖不减少。
2. 相关 migration current/历史负控、`phase1-acceptance.test.sh`、`make check` 通过。
3. 在干净提交上用既有隔离验收 VM 完成默认 serial-v1 的完整 029：41/41、0 FAIL、0 必需 SKIP、100/100 current-MVP 复用及完整串行尾部/浏览器证据。
4. 与精确 main 的同 VM serial 控制比较整单、Go race、identity 和串行尾部；只有实测差值才可记录为收益。环境差异、其他包波动或单次短实验不得冒充整单结论。

# 0 阶段外部上游接入验证（035）

状态：current-only deterministic relay 与 Compose mock 验收通过（2026-11-14）。真实 New API/Sub2API 服务、真实凭据、三服务三库拓扑和逐 profile 供应商认证未完成，不得以本记录替代。

## 已实现合同

- `current_003_external_channels.sql`：`channel_versions.channel_kind` 为 `direct`、`new_api`、`sub2api`；`integration_mode` 为 `external_relay`、`embedded_adapter`、`migrated_capability`，并约束 direct 不能伪装成 external relay。
- `catalog.SaveChannel` 固定渠道拓扑，后续不能原地改写 kind；能力迁移通过不同 channel 和 route revision 完成。`CandidateSnapshot` 提供 kind/mode，但 RouteSelector 仍只选择 channel。
- 外部 relay 复用 `provider.Client` 的 OpenAI-compatible 单请求路径、usage 解析、错误分类、网络限制和 `SafeToRetry`。模型绑定的 `upstream_model` 是发往下游的模型映射，客户响应仍回显逻辑模型名。
- new_api 和 sub2api 使用同一 Reserve→Attempt→Observe→Settle 商业链。下游账号池不创建客户 reservation、wallet 或 settlement；成本未知不记零。
- `ActivateRouteRevision` 及 admin HTTP `POST /api/platform/models/:id/route-revisions/:revision/activate` 支持 expected-revision CAS、已发布版本校验、死路由拒绝和审计；在途请求保持冻结 revision。
- probe 分别报告 channel kind、integration mode、协议成功、usage、成本 basis、错误类别与 consumption，不把下游模型列表或 HTTP 200 当作可售认证。

## 专用 PostgreSQL 证据

数据库：`127.0.0.1:55439/moni_test`，不记录凭据。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go test -race -count=1 -run '^TestExternalRelay' ./internal/server -timeout 900s
```

结果：PASS。覆盖：

1. new_api 端到端切片：下游模型映射、Bearer 凭据、客户模型名回显、一个 attempt、唯一 settlement、显式采购成本。
2. sub2api 复用同一合同。
3. new_api 429 无消费后切换 direct，共两个 attempt、一次收费。
4. `http_500` 与 `drop_after_headers` 均只有一个上游接收、一个 attempt，不向 direct 重放。
5. external relay → embedded direct adapter 切片，再按 route revision 回退；历史请求 revision 不漂移。
6. channel kind 生命周期、非法 kind、拓扑原地改写拒绝、activation CAS/未知版本/死路由/整池凭据撤销拒绝，以及并发撤销与 activation 的锁序负向测试。
7. probe 成功和失败的证据维度与 consumption 分类。

## Compose business-qa

执行：

```sh
DEPLOY_QA_PORT=18082 DEPLOY_QA_BUSINESS=true scripts/deployment-qa.sh
```

结果：`deployment QA passed`（日志 `/tmp/phase0-compose-business-qa.log`）。覆盖空库迁移及幂等重跑、两次 provision、bootstrap 及重复拒绝、live/ready、app/worker/qa-provider 进程、文本与媒体 business-qa、app/worker 重启、PostgreSQL 重启和恢复。临时 Compose 项目、数据卷和凭据由脚本清理。

另一次连接专用 55439 数据库的六协议部署验收见[031 记录](phase1/031-mock-harness.md)，最终结果为 12 个请求 PASS，未知消费和缺 usage 均不重放。

## 明确缺口

- 未启动真实 New API 或 Sub2API，未验证三服务、三独立数据库的内部网络拓扑；本分支 Compose 只使用仓库内 deterministic mock upstream。
- 没有真实外部 channel 凭据、真实 usage/成本对账或逐 profile 供应商资格结论。
- `qualification_status`、source/license/modification ref 和下游 retry/cost evidence 的持久化发布门禁尚未实现；源码复用组件的 LICENSE/NOTICE 验收仍需单独执行。
- 1Panel、生产部署、HA、真实供应商和完整 029 浏览器清单不在本次 PASS 范围。

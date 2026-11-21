# 032 数据面分片实现与专项验证

2026-11-20。本文记录当前分支的实际实现和局部/专项验证，不是第二阶段整体验收、真实供应商认证或生产资格。最终干净提交的完整 029 结果由后续验收入口单独发布。

## 实现范围

- 2.1：部署态 Chat 短 SSE 测量入口、031 mock 单调微秒间隔、三轮/原始样本/资源与正确性报告合同；固定资源实测未执行。
- 2.2：不可变路由熔断参数、channel+上游模型内存健康、多半开令牌、credential/quota/binding 持久隔离、首事件预读与显式无消费事件码白名单。
- 2.3：复用 023-B 客户周期预算，新增安全 402 `budget_exceeded` 字段和 Playground/请求详情的严格投影；没有新增余额或拒绝预占。
- 2.4/2.5：同租户精确 JSON 缓存、同价 cache-hit finalization/settlement、无 attempt/event/采购事实、已知零供应商成本、TTL 清理和角色/报表隔离。

合同见 [032 设计](../../specs/032-phase2-text-data-plane/design.md) 与 [028-U §6.2.2](../../specs/028-unified-metering-media/u-storage-contract.md)。

## 已执行验证

以下均在实际工作树执行；数据库命令使用专用 `127.0.0.1:55439` PostgreSQL 和每测试独立 schema，不使用生产库：

```sh
go test -race ./internal/provider ./internal/routing ./internal/catalog
go test -race ./internal/server -count=1
go test -race ./internal/testsupport/mockupstream ./scripts/phase2-baseline
go vet ./internal/testsupport/mockupstream ./scripts/phase2-baseline
npm --prefix web test
npm --prefix web run build
```

真实 PostgreSQL 专项：

```sh
go test -race -p 1 -count=1 ./internal/server \
  -run '^(TestGatewayPostgresTrustedFirstEventErrorFailsOverBeforeCommit|TestGatewayPostgresUnknownFirstEventErrorDoesNotReplay|TestPostgresQuotaIsolationPersistsWithoutChannelCircuitQuarantine|TestPostgresContextLengthExceededIsolatesBindingAcrossTenants)$'
go test -race -p 1 -count=1 ./internal/server -run '^TestPostgresRouteDataPlanePolicyIsImmutableAndVersioned$'
go test -race -p 1 -count=1 ./internal/server -run '^TestGatewayPostgresExactCacheHitSameChargeAndZeroUpstream$'
go test -race -p 1 -count=1 ./internal/billing -run '^TestPostgresPeriodicBudgetRejectionMetadataAndRollback$'
go test -race -p 1 -count=1 ./internal/periodicbudget
go test -race -p 1 -count=1 ./internal/billing -run '^TestPostgresCache'
go test -race -p 1 -count=1 ./internal/reporting ./internal/operations
```

结果均为 PASS。覆盖首事件可信换渠道与 unknown 单次接收、quota 内存重置后的持久隔离、路由旧/新 revision、缓存零新增上游 POST、同价唯一结算、TTL 删除、防篡改 guard、已知零成本与 projection 无缓存正文权限。预算测试通过正式 Commander 和独立测试时钟建立完整 UTC 窗口，不修改生产窗口算法。

## 未覆盖/待最终门禁

- 2.1 固定 4 vCPU/8GB、固定 PG 配额、100 实际活动流、60 秒预热/5 分钟采样、三次正式重复尚未执行；未生成正式报告。
- 未运行同条件 Bifrost/Rust LiteLLM，不宣称接近或领先；其他协议、工具、长流、慢客户端和冷/热 TLS 性能矩阵未做。
- 本文不是完整 029 结果；最终共享商业链路必须在干净提交运行完整 029。
- deterministic mock 和本地 PostgreSQL 通过不代表真实供应商、正式支付商、生产 SLA、多实例 HA 或生产隐私认证。

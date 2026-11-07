# 022-C1-M 本地退款模拟验证

2026-11-08。C1-M 只在 `internal/mockpsp` 的隔离 overlay 中实现本地退款命令、终局控制和冻结查询；候选 SQL 不由普通 paymentsandbox runtime 安装，不开放普通 `serve`、客户 HTTP 或真实资金效果。它依赖 C1-P 的退款 v2 协议，不能作为 C1-S 生产钱包退款验收。

本片新增命令/查询 canonical 输入、capture 原件重建、同 command/local refund identity 冲突记录、confirmed 与 `final_no_effect` 不可变 fact、capture 累计退款上限、同 capture 并发串行、`not_seen`/`in_progress` 不造终局墓碑，以及 query ID 的 immutable snapshot。QA 控制动作只写独立 `mock_refund_controls` 回执；外部控制不是支付事实，超时/未见/进程重启不会自动生成终局。

验证使用 PostgreSQL 16.15 专库 `127.0.0.1:55439/moni_test`，在现有 mock schema 上只为测试连接安装 `specs/022-self-service-payments/c1-m-mock-schema.sql`，随后撤除 PUBLIC 权限并授予 mock runtime 的精确表/函数权限。通过：

```sh
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL="postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable" \
go test -count=1 -race -timeout 300s ./internal/mockpsp
PATH="/opt/homebrew/bin:$PATH" go vet ./internal/mockpsp ./internal/paymentrefundschema
```

结果为 PASS（mockpsp race 11.498s）和 PASS。现有 v1 mock checkout/cancel/query 回归仍在同包内执行。C1-M 仍缺正式退款 SQL、C1-S/Q/F 钱包资金效果、签名 HTTP/UI、真实 PSP、生产角色/部署/HA；这些边界不能由本地 overlay 证据替代。

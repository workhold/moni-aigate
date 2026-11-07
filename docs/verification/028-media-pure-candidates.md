# 028-S/A/R 纯域候选第一片验证

更新时间：2026-11-08。该记录只证明三个尚未接线的纯 Go 候选包，不代表 028-S/A/R 或 024 媒体生产能力完成。

## 已验证

- `internal/media`：未发布同步图片 profile、严格输入和 PNG inline 结果边界；结果、坏结果、取消、传输失败和缺失用量不会被解释为零消费。
- `internal/tasks`：严格任务/事件 canonical 与 digest、提交一次、同意图幂等重放、同键异体冲突、`submission_unknown` 禁止再次提交、终态/取消竞态、结果过期、租户授权和有界制品读取。
- `internal/realtime`：session/turn 状态机、首收费 request/reservation/attempt 引用门禁、单用 Origin ticket、事件去重、heartbeat/status 不造 turn、断线/取消/期限/背压以及 settled/released/pending 边界。

统一执行结果：

```text
PATH="/opt/homebrew/bin:$PATH" GOCACHE=/tmp/moni-aigate-gocache \
go test -count=1 -race -timeout 180s ./internal/media ./internal/tasks ./internal/realtime
PATH="/opt/homebrew/bin:$PATH" GOCACHE=/tmp/moni-aigate-gocache \
go vet ./internal/media ./internal/tasks ./internal/realtime
```

两条命令均通过。之后又收紧了媒体 UUID、任务制品引用/媒体类型和实时 retail usage digest 的纯输入合同，并重新通过同一组 race/vet。

## 仍然不能勾选

三个包没有数据库 writer、迁移、HTTP/WS endpoint、生产 ticket 存储、provider adapter、签名回调、任务 lease、真实模型调用、钱包/账务接缝、026 报表接线、受限 APP 商业链、部署/恢复或性能证据。图片本地 PNG fixture 不能代替正式供应商；异步任务纯状态机不能代替供应商 submit/query/cancel；实时内存 ticket/session 不能代替 HTTP handshake、CSRF、Origin allowlist、跨进程存储或真实收费。

因此 `specs/028-unified-metering-media/tasks.md` 中 S、A、R 复选框继续保持未勾选，U 存储和商业出口也继续保持未勾选。

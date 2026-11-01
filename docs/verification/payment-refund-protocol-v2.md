# 022-C1-P 本地退款 v2 纯协议验证

2026-11-01。按[冻结合同](../../specs/022-self-service-payments/c1-protocol-v2-contract.md)新增独立 `internal/paymentrefundproto`；**纯协议算法及专项验证通过，原路退款资金、端点、P 墓碑和运行接线尚未实现。** 本记录不代表 C0/C1 完整验收、真实 PSP 认证、生产部署或性能验收。

## 实现与边界

- [类型与 opaque 值](../../internal/paymentrefundproto/types.go)、[规范值/分帧](../../internal/paymentrefundproto/values.go)、[封闭 codec](../../internal/paymentrefundproto/codec.go)：六种 DTO 的精确字段和 null 联合；数字字面量 `2`、完整 UTF8/UTF16、重复 member、大小/深度/数组限制、规范 UUID/正 int64/UTC6。资源界在解码分配前检查。
- [v1 capture 只读桥](../../internal/paymentrefundproto/capture.go)：仅接收 v1 已验签 opaque capture，重新严格解析 canonical 副本；通知原 order UUID 与 query 原 MPS1 映射为同一 capture 身份。桥保留原环境，不选择 APP 环境或来源 lot 资格。
- [退款与冻结查询请求](../../internal/paymentrefundproto/requests.go)、[终局 fact/通知](../../internal/paymentrefundproto/facts.go)、[回复](../../internal/paymentrefundproto/replies.go)：command 与 query 的完整原绑定；四种独立摘要域；confirmed/final_no_effect 联合与非终局零 Fact。
- [独立 keyring 和三签名用途](../../internal/paymentrefundproto/signing.go)：32 字节复制密钥、历史认证/仅 active 签名、长度帧 HMAC、精确 raw-body SHA、完整原 query digest context、时间和可信环境/请求核对。错误仅四种固定 sentinel，失败不返回部分可信对象。

源码 import 检查结果仅 Go 标准库与 `internal/paymentproto`。未修改 v1 包、P/mock/receiver、迁移、billing/payments/operations 或普通 main；没有新 DB/HTTP client，没有注册退款路由，没有向现有进程授予新权限。opaque 证据不具有财务或网络授权。

## 独立固定向量

另一实施者独立编写[Python 生成/校验器](../../scripts/verification/payment-refund-v2-golden.py)，以手写有序 JSON、UTF8 字节、uint64 big-endian 帧、SHA-256 与 HMAC 产生[固定合成向量](payment-refund-v2-golden.json)。脚本不调用 Go 实现；Go [golden 测试](../../internal/paymentrefundproto/golden_test.go)直接消费 artifact 的 expected canonical、四域摘要、frame bytes 与签名。

实际通过两条 v1 authenticated bridge、两份 command/query/fact，以及九种 signed vector：confirmed 与 final_no_effect 各 command/query/notification，command in_progress、query not_seen、Unicode escaped/pretty notification redelivery。相同终局在不同包装与 raw 编码下保持相同 fact digest。测试用密钥带 `SYNTHETIC` 标识，仅为公开固定合成材料。

## 已执行验证

```sh
go test -race ./internal/paymentrefundproto ./internal/paymentproto -count=1 -v
go vet ./internal/paymentrefundproto ./internal/paymentproto
go test ./internal/paymentrefundproto -run '^$' -fuzz '^FuzzWireRoundTrips$' -fuzztime=3s -parallel=2
go test ./internal/paymentrefundproto -run '^$' -fuzz '^FuzzAuthenticatedNotification$' -fuzztime=3s -parallel=2
go test ./internal/paymentrefundproto -run '^$' -fuzz '^FuzzSignedHeaders$' -fuzztime=3s -parallel=2
go test -race ./internal/paymentrefundproto -run '^TestNestedObjectsRemainClosedAndBodyLimits$' -count=1
```

以上均退出 0。共同 race：新包 1.914 秒、v1 包 1.969 秒；11 项行为测试、9 个签名子向量与 3 个 fuzz target 的种子全部通过，未 Skip。短 fuzz 实际执行分别为 5,825、132,486、181,587 次，无失败。最后一项是在修正测试自身的嵌套 token 定位后重新执行，1.530 秒通过；未因该测试调整修改生产实现。

[codec/值测试](../../internal/paymentrefundproto/protocol_test.go)覆盖六 parser 缺失/未知/大小写字段、escaped duplicate key、嵌套 `schema_version:2.0/2e0`、非法 surrogate、合法非 BMP 字母、UTF8/数组/深度与精确 8KiB/64KiB 字节边界。值域覆盖非 nil 任意 UUID variant、MAX int64、原 capture 上限、v1 Unicode 与新退款 ASCII 引用差别、UTC6 逗号/offset/年界及 nullable 联合。

[认证测试](../../internal/paymentrefundproto/signing_test.go)覆盖三用途互拒、合法 raw 乱序后 canonical、逐 command 字段与 query ID/generation/context、可信环境异体、有效 HMAC 下非法正文、大小写重复 header、key 复制/重复/轮换/闭区间、±300 秒、completed_at +5 秒、receivedAt UTC 跨年界/零值、历史终局与任意合法接收时区/纳秒。所有 opaque 零值、not_seen/in_progress 的零 Fact、raw/DTO/返回 slice 防修改与 16 goroutine 并发只读通过 race。[桥测试](../../internal/paymentrefundproto/capture_test.go)明确旧 v1 refund.failed 不可成为 v2 终局或 capture。

## 仍待独立验收

本片无需数据库，也未运行新的 PG 或普通 main/浏览器验收；共同 v1 race 只证明其纯协议回归。C0 独立全源核验/静态 checker/维护切换及全资金路径仍为前置；C1-M 的真实幂等效果/拒绝墓碑/冻结 query、C1-S 的追加 SQL/角色/运行证明、C1-I 的双审原 lot 冻结/唯一终结、HTTP/UI 与普通 main 故障，以及 C2/C3 风险/恢复关口仍未交付。不能由 signed fact 或纯包测试推断真实退款已发生或钱包可释放。

集成人汇总并行结果后更新统一架构、HLD/LLD 与阶段状态，再执行 reviewed refresh 和只读检查；本专项不自行刷新架构清单。

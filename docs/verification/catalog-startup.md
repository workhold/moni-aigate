# 固定 Catalog 初始化优化验证

2026-11-02。本记录只覆盖固定 expected catalog 的包初始化与无 PostgreSQL 解析验证。正式 expected 专用 parser 已接入，当前尚不能据此认定原 CLI、SIGKILL 与 main 的真实进程回归或整仓验收通过。精确命令、日志 SHA-256、测量样本和源码身份见 [JSON 证据](catalog-startup.json)。原 T0–T3 专项身份和通过范围保持各自记录。

## 问题与变更边界

此前整仓 `make verify-mvp` 退出 2，失败涉及 CLI 的 60 秒共同期限、SIGKILL helper 的 12 秒到达边界期限，以及 main 的 10 秒启动/角色拒绝观察期限。`billing → maintenancetrust → fundinglotcatalog` 的新依赖使这些 race 子进程在进入 main 或 helper 前都执行固定根的 eager 认证。独立启动和 inittrace 复现了明显的包初始化开销；这解释了需要优化的路径，但仍须通过原真实进程用例判断最终修复结果。原失败日志 SHA 和范围保留在 JSON，不将本记录写成原整仓通过。

按 [SDD §6](../../specs/022-self-service-payments/c0-read-lease-contract.md#6-集成启动开销与静态缓存合同)，本轮集成三项静态优化：完整 canonical 验证后复用私有 Schema/Roles/category 的规范片段；预编译固定字段类型清单，保留原顺序和校验规则；仅 `decodeExpected` 改用 [严格 raw parser](../../internal/fundinglotcatalog/expected_raw_parser.go)。原 actual/candidate 元数据的 `strictJSONNodes` 保留。

固定根及 SHA、启动时 eager 认证、完整 DTO/模板检查、whole-root canonical 与原 raw 逐字节相等，以及 digest 命中后的完整 bytes.Equal 均保留。没有 lazy 认证、外部 expected 输入或跳过开关，不修改 SQL、ACL、冻结 root 或发行预算。新 parser 的树持有自己的字符串；escaped string 使用 encoding/json.Unmarshal 保持原 surrogate/转义行为，不使用 unsafe。

严格 parser 保留全 raw UTF8、64 MiB、根 depth 0/最大 depth 64、最多 4,000,000 个值节点、每数组最多 100,000 项、decoded 字符串值最多 65,536 UTF8 bytes 且无 NUL、decoded key 查重、原 canonical number 与 64 字节界，以及完整 EOF。key 不计值节点，key 保留原 parser 的大小/NUL 语义，闭合 DTO 继续负责拒绝不属于合同的字段。

## 冷启动测量

使用事先构建的 fundinglotcatalog race test 二进制，以 `-test.run=^$` 每次启动新进程。baseline 已包含 span cache 和固定字段预编译，candidate 在相同冻结源码上只切 expected parser。所有数据库 URL 环境均 unset；没有 pprof、数据库连接或监听器。其他 agent 暂停 CPU 验证后，按 baseline → candidate 顺序串行执行三轮。设置 `GORACE=atexit_sleep_ms=0` 去掉双方共同的 race 退出等待，不改变初始化代码。

| 指标 | baseline：原 Token expected parser | candidate：严格 raw expected parser |
| --- | ---: | ---: |
| 第 1 轮新进程 wall | 9.5250 s | 7.0642 s |
| 第 2 轮新进程 wall | 9.2746 s | 6.9879 s |
| 第 3 轮新进程 wall | 9.2294 s | 7.1537 s |
| wall 中位数 | 9.2746 s | 7.0642 s |
| 独立 inittrace：catalog init | 9,241 ms | 7,099 ms |
| 独立 inittrace：累计分配次数 | 17,089,971 | 14,347,812 |
| 独立 inittrace：累计分配字节 | 1,332,116,856 | 1,331,472,080 |

本机 race 新进程 wall 中位数下降约 23.83%，累计分配次数下降约 16.05%；累计分配字节基本不变，也不是峰值 RSS。较早一组存在其他构建/验证竞争可能的样本保留在本地，未纳入上表。上述二进制早于正式测试命名整理，候选 parser 与正式实现仅注释不同；正式源码的 race 结果另列。

这些数值是本机包初始化观察，不是网关首字节、模型首 token、吞吐或目标机器容量结果，也不承诺在并发整仓测试下具有固定启动余量。

## 正确性与已执行结果

原 Token parser 是差分 oracle：同时比较成功/失败与完整 owned 树类型和值。固定语料覆盖 decoded 重复 key、孤立 surrogate/替换字符、无效转义、raw 控制字节/UTF8、canonical number/EOF、`1true`/`-`/leading zero/exponent、trailing comma、depth 63/64/65、值节点小边界、字符串值与 key 的原有差异。字符串测试还覆盖 ASCII、UTF8 及 escaped UTF8 的 65,535/65,536/65,537 bytes，数组覆盖 99,999/100,000/100,001 项，并验证修改 caller raw 不会修改返回树。

| 执行范围 | 结果 |
| --- | --- |
| overlay 小语料、文本/数组、ownership 与确定性随机差分 | 4 顶层 PASS；20,000 个生成合法输入及 20,000 个变异输入；2.323 s |
| overlay race：原纯测及候选完整根/门禁 | 23 顶层、25 父子终态全部 PASS，零 skip/fail；42.503 s |
| overlay 显式大资源差分 | 1 顶层 PASS；64 MiB/64 MiB+1，3,999,999/4,000,000/4,000,001 值节点；4.236 s |
| overlay 单 worker fuzz | 请求 15 s，实际 16.01 s；37,147 次执行，无差异，PASS；含启动总计 18.571 s |
| 正式源码、正式测试名的必要 race 纯测 | 14 顶层、16 父子终态全部 PASS，零 skip/fail；42.637 s |

完整 Ordinary/Sandbox root 均与原 Token 树 DeepEqual，并再次通过完整 decodeExpected。独立负例确认 SHA 不匹配、错误模式、重新计算 SHA 后的非 canonical 字节和尾随值仍拒绝。fuzz 在 6 秒之后执行计数没有继续增加，最终在有界时间内正常 PASS；不把它描述为 15 秒持续同速或穷尽证明。

正式大边界与 fuzz 测试是已验 overlay 测试的命名/helper/env 整理，逻辑未变，本轮没有再次以正式名执行它们，以便集成人独占 CPU 串行回归原 PostgreSQL 进程测试。历史 overlay 的精确命令、源码 SHA 和实际结果在 JSON 中；不得把未执行命令计作另一轮验收。

## 正式源码复现命令

必要纯测的完整精确 selector 见 JSON 的 `production_reproduction_commands.targeted_race`。仅 parser 专项可使用以下命令；均不设置 PostgreSQL URL。大内存边界明确 opt-in，不由默认整仓验收自动计入：

```sh
env -u C0_MAINTENANCE_DATABASE_URL -u C0_CATALOG_DATABASE_URL -u TEST_DATABASE_URL -u DATABASE_URL \
  go test -race ./internal/fundinglotcatalog \
  -run '^TestExpectedRawJSON(Corpus|TextAndArrayBounds|OwnedStrings|RandomDifferential|FullRoots|CompleteGates)$' -count=1 -json

env -u C0_MAINTENANCE_DATABASE_URL -u C0_CATALOG_DATABASE_URL -u TEST_DATABASE_URL -u DATABASE_URL \
  C0_EXPECTED_RAW_LARGE_TEST=1 go test ./internal/fundinglotcatalog \
  -run '^TestExpectedRawJSONLargeBounds$' -count=1 -json

env -u C0_MAINTENANCE_DATABASE_URL -u C0_CATALOG_DATABASE_URL -u TEST_DATABASE_URL -u DATABASE_URL \
  go test ./internal/fundinglotcatalog -run '^$' \
  -fuzz '^FuzzExpectedRawJSONDifferential$' -fuzztime=15s -parallel=1 -json
```

本记录写入时，原 CLI/SIGKILL/main 的 PostgreSQL 进程回归、最终整仓验证和统一架构快照由集成人继续串行收口。容器/1Panel、正式供应商、目标性能与生产发布门禁分别验收；本轮不改变这些状态。

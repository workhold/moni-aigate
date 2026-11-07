# 028-S：同步媒体第一片——受限 inline 图片候选合同

状态：**纯域候选，未发布、未接线、不可售**。本片只冻结一个供应商无关、可执行的同步媒体边界，并提供 `internal/media` 的纯 Go 校验器和局部状态机。它不证明任何供应商支持这些字段、格式、尺寸、usage 或价格，也不开放 HTTP 端点、模型目录、路由、预占、结算、钱包、报表或真实供应商认证。

本片选择“同步图片生成、单张 PNG inline 结果”作为最小资源闭包。图片编辑、TTS、STT、图片输入、异步视频和实时媒体另行冻结合同；不能因为本包存在而把它们路由到图片生成 profile。当前 profile 的发布状态固定为 `unpublished`，任何派发检查都返回明确的 `media_profile_not_published`。

## 1. 信任边界和单一路径

`internal/media` 只依赖标准库和已有的严格 JSON 语法层。它不访问数据库、HTTP、文件系统、环境变量、时钟、随机数、钱包、billing、provider 或凭据，也不调用 `internal/inference`。它不计算 token、金额、供应商成本或客户消费量。

包内对象只说明：

1. 输入是否符合候选 profile 的有限 wire 合同；
2. inline PNG 是否在固定字节、尺寸、像素和摘要边界内且完整可解码；
3. 一个外部集成已经发生的 dispatch/result/usage 事实在有限状态机中的位置。

`ParseInput` 和 `ParseResult` 不授予调用权。`ObserveDispatch` 只记录调用方已经发生的外部事实；它不发送请求，也不证明供应商已经接受请求。`InlineResult` 的存在只证明收到并验证了一个制品，不能推出供应商消费、客户成功、零消费或应收金额。只有受后续供应商认证合同约束的显式 `UsageEvidence` 才能让局部状态机进入 `completed`；本片没有真实 usage 适配器。

## 2. 候选 profile

profile 的 canonical 身份是以下固定合同：

```json
{
  "version":"1",
  "profile_version":"<non-zero lower-case UUID>",
  "operation":"image_generation",
  "execution_kind":"synchronous",
  "result_kind":"inline",
  "variant_key":"default",
  "publication":"unpublished",
  "input_format":"json",
  "result_format":"png",
  "max_prompt_bytes":"4096",
  "max_outputs":"1",
  "allowed_sizes":["256x256","512x512","1024x1024"],
  "max_result_json_bytes":"33554432",
  "max_decoded_bytes":"16777216"
}
```

profile version 必须是非零、小写、标准 36 字节 UUID；输入端传入的 profile version 必须逐字匹配。`variant_key` 和 operation 等枚举不接受空值、未知值或“当前默认”推断。所有限制是硬上限，解析器拒绝超限输入，不截断、不降级、不改用另一个尺寸。

尺寸是有限 profile 参数而不是可相加的计量维度。结果最多一张；本片不定义图片 token、像素收费、请求次数收费、压缩率换算或任何价格。不得把 `width`、`height`、文件字节数或 PNG 成功解码转换成 token 或金额。

## 3. 输入合同

`ParseInput(raw, profile)` 只接受不超过 64 KiB 的严格 JSON 对象，字段集合必须恰为以下顺序语义；输入键的排列不影响解析，但 canonical 输出固定为此顺序：

```json
{
  "version":"1",
  "profile_version":"<UUID>",
  "variant_key":"default",
  "prompt":"<1..4096 UTF-8 bytes>",
  "size":"256x256|512x512|1024x1024",
  "format":"png",
  "n":"1"
}
```

prompt 保留调用方原文，不 trim、不 Unicode normalize、不替换字符；空串、控制字符、无效 UTF-8、过长文本拒绝。`n` 是规范十进制字符串且只能为 `"1"`。输入不含远程 URL、文件路径、base64 图片、multipart、任意 header、供应商参数或自由 metadata。未知字段、重复键、BOM、尾随 JSON、非法转义、浮点/指数/带正号数字都拒绝。

输入解析成功只生成不可变 `Input` 和摘要；不计算 prompt token，不检查供应商可用性，不开始网络调用。profile 未发布时输入仍可被本地校验，以便测试边界，但派发检查必须失败。

## 4. inline 结果合同

`ParseResult(raw, profile, input)` 只接受不超过 32 MiB 的严格 JSON 对象，并要求结果的 profile、variant、格式和尺寸与已验证的 `input` 完全一致。结果 wire 字段集合必须恰为：

```json
{
  "version":"1",
  "profile_version":"<UUID>",
  "variant_key":"default",
  "format":"png",
  "data_base64":"<canonical padded standard base64>",
  "width":"<decimal>",
  "height":"<decimal>",
  "byte_length":"<decimal>",
  "sha256":"<64 lower-case hex>"
}
```

`data_base64` 必须是标准 padded base64 的唯一编码，不能有空白、URL-safe 字符、额外 padding 或替代编码。解码后的 PNG：

- 非空且不超过 16 MiB；
- 必须有 PNG signature，能由固定 Go PNG 解码器完整解码；
- 实际宽高必须与 wire 的规范十进制字段相同，且必须等于 input 请求的尺寸（该尺寸也必须属于 profile 允许集合）；
- 解码像素内存预算按 `width*height*4` 检查且不溢出；
- `byte_length` 和 `sha256` 必须分别与解码字节长度和原始字节摘要相同。

解析器不写临时文件、不跟随 URL、不转码、不解压任意归档、不执行元数据。结果对象只在内存中保存有界原始 PNG；getter 返回 copy。canonical 结果描述只含 profile、格式、尺寸、字节长度和摘要，不把二进制复制进数据库或审计对象；结果 digest 另把原始 PNG 纳入固定 framing。结果校验成功仍不是 usage 证据。

## 5. 失败分类和状态机

解析失败返回零值对象和受控 sentinel；不得返回部分合法结果。错误分类至少保持以下边界：

| 错误 | 含义 | 消费结论 |
| --- | --- | --- |
| `media_too_large` | wire、prompt、decoded resource 或像素预算超限 | 尚未 dispatch 时为 `no_dispatch` |
| `media_invalid_json` / `media_invalid_input` | 严格 JSON 或输入字段失败 | `no_dispatch` |
| `media_invalid_result` | base64、摘要、PNG、尺寸或声明不一致 | 若已 dispatch，消费为 `unknown` |
| `media_profile_not_published` | 当前 profile 没有真实供应商认证和发布授权 | `no_dispatch` |
| `media_invalid_state` | 事件顺序、重复终结或跨状态操作错误 | 不改变原状态 |
| `media_usage_unconfirmed` | 仅有 inline 文件，没有受信 usage 事实 | 不得进入 `completed` |
| `media_consumption_unknown` | 已 dispatch 但无法安全证明消费结果 | 进入终态 `consumption_unknown` |

记录状态是：

```text
prepared -> sending -> result_received -> completed
    |          |              |
    |          |              +-> consumption_unknown
    |          +-> failed (consumption_unknown)
    |          +-> cancelled (consumption_unknown)
    +-> failed (no_dispatch)
    +-> cancelled (no_dispatch)
```

`prepared` 只能由输入和 profile 创建；本片 `DispatchCheck` 在此状态也返回 `media_profile_not_published`。集成层若已经实际派发，可调用 `ObserveDispatch` 记录 `sending`，这不是授权入口。`ObserveResult` 只能从 `sending` 进入 `result_received`；它不能直接进入 `completed`。`ConfirmUsage` 只能从 `result_received` 进入 `completed`，且必须提供非空、格式受限的外部事件身份。没有 usage 时调用 `ConfirmUsage` 必须返回 `media_usage_unconfirmed` 并保持原状态。已派发后的错误、取消、连接终止或坏结果不能被本包解释成零消费；只能进入 `consumption_unknown` 或带该结论的失败/取消状态。终态不可重开、不可重发、不可覆盖。

本状态机不决定客户收费、供应商成本、钱包释放或退款。集成时必须把它接到 028 的原 request/attempt/observation/settlement 事实，且供应商认证、受限 APP、唯一结算、报表和恢复必须单独验收。

## 6. 本片验收

纯包验收必须实际覆盖：

1. profile canonical、非零小写 UUID、未发布拒绝和 getter 深拷贝；
2. 输入的精确字段、枚举、prompt 字节界、尺寸、`n=1`、重复/未知键、尾 JSON、BOM、Unicode/数字异常和 raw 上限；
3. PNG 结果的 canonical base64、完整解码、尺寸/字节/摘要一致性、截断/伪造/超限/像素溢出和 getter 深拷贝；
4. `prepared → sending → result_received` 后没有 usage 不能完成；显式 usage 才能完成；已 dispatch 的坏结果/取消/未知消费不变成 no-consumption；重复和非法状态迁移被拒；
5. `go test`, `go test -race`, `go vet` 均只针对 `internal/media`；不以本地 PNG fixture 代替真实供应商、HTTP、PostgreSQL、钱包、账务、报表、浏览器、部署或性能验收。

本片不修改 001–024 迁移、已有 Go 文件、共享架构文档、HTTP/UI、provider、billing、wallet 或报表，也不提交/推送。完成本片不勾选 028-S 总任务。

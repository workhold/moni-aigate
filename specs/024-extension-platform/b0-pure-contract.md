# 024-B0-P：纯计量层精确合同（仅纯层已冻结）

2026-11-01。根已接受[B0方向](b0-metering-contract.md)中的最多8收费维度、有理率末尾单次ceil、两侧完整硬界与旧token等价adapter，并已审查冻结**本文纯层精确合同，授权仅新增`internal/metering/`及纯测试**。B0生产数据/资金接线和B1仍为候选，不在本片授权内。不修改provider、billing、routing、catalog、DB、配置或运行行为，不注册媒体端点。subset/diagnostic尚无精确registry，本片一律不解析、不猜字段。

## 1. 范围和信任边界

纯层只验证规范值/集合/绑定、计算非负有理费用和上界关系。解析成功不证明provider usage已确认、上界可由供应商强制或actor已授权；这些是后续B0-I/B1的合同。不得把`Quantities`当作可以调用上游的许可。模块只用Go标准库，无DB、网络、时钟、环境、全局可变registry或用户秘密；零值对象拒绝。

schema最多8维，**全部为billable**；retail与supplier mask均非空、无重复、为dimensions子集，二者并集精确等于dimensions。名称及单位固定如下，增加registry另经设计/测试；首片没有可忽略的未知维度：

| dimension | unit |
| --- | --- |
| `audio_input_millisecond` | `millisecond` |
| `audio_output_millisecond` | `millisecond` |
| `image_output_count` | `image` |
| `request_count` | `request` |
| `text_input_token` | `token` |
| `text_input_unicode_scalar` | `unicode_scalar` |
| `text_input_utf8_byte` | `byte` |
| `text_output_token` | `token` |

每个维度有schema `maximum_quantity`，范围0..MaxInt64；0是明确上界，不表示缺失或不限。actual quantity解析可超过该maximum以保留真实超界；Bounds解析不得超过它。模型能否提供这些单位不在本片断言。

## 2. 严格wire与canonical编码

所有对象均为精确字段集合，全部必有，无nullable/omitempty。JSON必须单个对象、有效UTF8、无BOM/重复member/未知field/尾JSON；允许JSON标准空白和对象字段任意顺序，但数组**必须已按dimension的ASCII字节C序严格递增**，不替客户端排序。所有十进制字段是string，`0|[1-9][0-9]*`，≤9223372036854775807；revision在本片没有业务含义，wire version恒字符串`"1"`。

`schema_id`为小写带连字符非nil UUID；允许任意非nil128位值，不额外限制UUID version/variant，不trim或改写大小写。unit与registry精确相同。side仅`retail|supplier`；currency恒`CNY`。

```json
{
  "schema_id":"11111111-1111-1111-1111-111111111111",
  "version":"1",
  "dimensions":[
    {"dimension":"text_input_token","unit":"token","maximum_quantity":"100"},
    {"dimension":"text_output_token","unit":"token","maximum_quantity":"20"}
  ],
  "retail_dimensions":["text_input_token","text_output_token"],
  "supplier_dimensions":["text_input_token","text_output_token"]
}
```

其余wire的精确形状如下；示意省略的元素只能在真实schema mask中不存在，不能省掉必需维度：

```text
BoundsWire = {
 schema_id:UUID,version:"1",side:"retail"|"supplier",
 bounds:[{dimension:Dimension,unit:Unit,quantity:Decimal}]
}
QuantitiesWire = {
 schema_id:UUID,version:"1",side:"retail"|"supplier",
 quantities:[{dimension:Dimension,unit:Unit,quantity:Decimal}]
}
RatesWire = {
 schema_id:UUID,version:"1",side:"retail"|"supplier",currency:"CNY",
 rates:[{dimension:Dimension,unit:Unit,numerator:Decimal,denominator:Decimal}]
}
```

三种vector数组精确等于该side的mask，既不缺维也不含另一side独有维度。quantity/rate若未知、missing、null，**不能表达为本片的完整已知vector**，应留给后续证据层处理；拒绝用0凑齐。BoundsWire和QuantitiesWire用不同字段名和不同opaque类型，不能互换parser绕开global maximum。

denominator是1..1,000,000，numerator非负int64，gcd必须1；0价唯一为`"0"/"1"`。`1/2`合法，`2/4`和`0/1000000`拒绝。输入不进行静默约分；Legacy adapter由已知旧rate主动构造约分后的新对象，旧DB/DTO不改。

schema原始body≤16,384字节，其余每份≤8,192字节；超限在JSON解析前拒绝。每个数组≤8，嵌套深度≤8；控制/无效UTF8、escaped未知member、重复键即使值相同也拒绝。必须消费到EOF并关闭decoder引用；不能用encoding/json默认map覆盖重复键。标准合法字符串escape可接受，canonical会重新编码；拒绝非法UTF16 surrogate escape，不能让Go decoder静默替成U+FFFD。

canonical JSON是无空白UTF8，object按上列字段顺序；schema dimension成员顺序`dimension,unit,maximum_quantity`，bound/quantity成员`dimension,unit,quantity`，rate成员`dimension,unit,numerator,denominator`；数组仍为已验证C序。字符串用encoding/json标准转义，所有当前合法内容均ASCII。导出的是新byte slice，调用者修改不影响对象或后续digest。

## 3. Opaque API、schema与side绑定

公开类型仅`Schema, Bounds, Quantities, Rates, Side`；前三类vector及Schema内部字段不export，持私有不可变值，构造时复制所有slice。Side为自定义string类型：

```go
type Side string
const Retail Side = "retail"
const Supplier Side = "supplier"
type Schema struct { /* private */ }
type Bounds struct { /* private */ }
type Quantities struct { /* private */ }
type Rates struct { /* private */ }

func ParseSchema(raw []byte) (Schema, error)
func ParseBounds(schema Schema, side Side, raw []byte) (Bounds, error)
func ParseQuantities(schema Schema, side Side, raw []byte) (Quantities, error)
func ParseRates(schema Schema, side Side, raw []byte) (Rates, error)
func (s Schema) CanonicalJSON() ([]byte, error)
func (b Bounds) CanonicalJSON() ([]byte, error)
func (q Quantities) CanonicalJSON() ([]byte, error)
func (r Rates) CanonicalJSON() ([]byte, error)
func (s Schema) Digest() ([32]byte, error)
func (b Bounds) Digest() ([32]byte, error)
func (q Quantities) Digest() ([32]byte, error)
func (r Rates) Digest() ([32]byte, error)
func QuoteDigest(bounds Bounds, rates Rates) ([32]byte, error)

func Amount(quantities Quantities, rates Rates) (*big.Int, error)
func ReservationAmount(bounds Bounds, rates Rates) (int64, error)
func CompareExactCost(left Bounds, leftRates Rates, right Bounds, rightRates Rates) (int, error)
func WithinBounds(actual Quantities, upper Bounds) (bool, error)

func LegacyTokenRates(schema Schema, side Side, inputRate, outputRate int64) (Rates, error)
func LegacyTokenQuantities(schema Schema, side Side, input, output int64) (Quantities, error)
func LegacyTokenBounds(schema Schema, side Side, input, output int64) (Bounds, error)
```

Parse三vector要求wire.schema_id与传入schema完全一致、wire.side与传入side相同。每个对象内部保留**完整canonical schema摘要**和side，不只保存UUID。任何运算要求参与对象schema ID、摘要、side全部一致；相同UUID但不同maximum/mask/unit定义同样ErrBindingMismatch。schema不是指针相等判定，相同canonical内容分别Parse可互操作；所有对象零值均ErrInvalidValue。

CompareExactCost进一步要求四个对象全为Supplier；retail误传ErrSide，不比较不同schema的两个金额来暗示它们是同一请求兼容报价。未来跨schema路由若需要，必须新冻结请求等价证明/接口，不放宽本片绑定。Amount/ReservationAmount可用于两side，但只能同side；纯层没有自动转换side函数。

Amount返回**新分配**big.Int，不暴露内部共享整数；修改结果不能改变Rates/Quantities或后续计算。其他API不输出内部slice、map、big.Rat或big.Int指针。私有字段不提供Set/MutableView；复制Go值仍引用不可变内部内容，不开放反射/unsafe或JSON Unmarshal方法绕parser。

Legacy三个adapter要求schema.dimensions及两side mask均恰为`text_input_token,text_output_token`，unit均token；选择side必须合法。negative rate/quantity拒绝；rates按旧rate/1e6主动约分（0→0/1），quantity允许actual超schema maximum，bounds必须≤schema maximum。input-only用明确output bound/rate/quantity=0，不能省output维度。adapter不创建schema UUID、model/price版本或新资金来源；调用者显式提供局部已校验schema，不把它写回旧DB。它只是金额兼容工具，不为旧数据伪造已认证media合同。

## 4. 算法、上界与失败结果

Amount精确计算`ceil(SUM(q_i*n_i/d_i))`，只在总和最后ceil一次；使用big.Int/big.Rat，不用float或每维ceil。输入全部非负，结果非负；允许actual费用超int64以供后续NUMERIC100/超界处理。max8维、每quantity/numerator≤MaxInt64、分母≤1e6，合计整数费用<10^40，完整有理中间量也在100位内；实现可始终用big.Int，不靠int64中间乘法。

ReservationAmount用相同算法处理已校验Bounds，只有结果≤MaxInt64返回int64，否则ErrAmountOverflow；不是将结果截为上限。WithinBounds在完整相同schema/side下逐维比较，超任一维为`false,nil`；vector格式/绑定错误返回error，不能将错误冒充“正常超界”。实际quantity大于schema maximum仍可Amount，WithinBounds会识别；只有Bounds要求全局最大界。

CompareExactCost精确比较两个未ceil有理总和，返回-1/0/1；例如1/3和1/2都ceil为1，但前者应更便宜。不得通过每个rate先约成float、按JSON字符串或已ceil值评分。schema各mask相同、供应商rate和本次bound均绑定，不能用retail rate意外做采购score。

所有失败返回对应类型**零值**或nil big.Int、int结果0、digest全0、canonical nil；不提供部分向量/截断摘要。WithinBounds失败返回false+error；合法超过上界也是false但error=nil。调用者必须检查error，不能将零结果当免费报价。

## 5. Canonical digest framing

哈希仅作身份/绑定/重验，无秘密输入，不宣称它证明授权或供应商事实。使用SHA-256。framing函数对每个frame追加`uint32大端字节长度 || 原始frame bytes`；所有frame顺序固定，包含domain本身，禁止普通字符串拼接或省掉空frame。当前长度远小于uint32上限。

| API | frames按顺序 |
| --- | --- |
| Schema.Digest | UTF8`moni-aigate.metering.schema.v1`，canonical SchemaWire |
| Bounds.Digest | UTF8`moni-aigate.metering.bounds.v1`，原始32byte schema digest，UTF8 side，canonical BoundsWire |
| Quantities.Digest | UTF8`moni-aigate.metering.quantities.v1`，schema digest，side，canonical QuantitiesWire |
| Rates.Digest | UTF8`moni-aigate.metering.rates.v1`，schema digest，side，canonical RatesWire |
| QuoteDigest | UTF8`moni-aigate.metering.quote.v1`，schema digest，side，canonical BoundsWire，canonical RatesWire |

schema摘要是32原始字节，不是hex字符串；side是枚举完整ASCII；wire中的schema_id/side仍保留，不能因为外层frame已有而省字段。quote任一bound/rate/schema/side改变均改变摘要；相同semantic内容不同object字段顺序/标准escape得到相同摘要。bounds/quantities即使数值一样也domain分离。测试固定至少一份独立手写frame+SHA256的golden向量，禁止仅调用同一helper生成expected。

## 6. 受控错误

导出sentinel，Error()仅固定下列code，不反射raw输入、字段值或底层decoder消息；可用errors.Is识别：

| sentinel | code | 精确场景 |
| --- | --- | --- |
| ErrTooLarge | `meter_too_large` | 原始byte长度超过该parser上限，先于JSON解析 |
| ErrInvalidJSON | `meter_invalid_json` | UTF8/BOM/语法/非法surrogate/重复member/尾对象或depth>8 |
| ErrInvalidSchema | `meter_invalid_schema` | schema精确字段/UUID/version/registry/unit/集合/maximum错误；subset/diagnostic字段也在此拒绝 |
| ErrInvalidVector | `meter_invalid_vector` | bounds/quantity/rate字段形状、十进制/null/集合/unit/分母/GCD错误，或bounds超过schema maximum |
| ErrInvalidValue | `meter_invalid_value` | 零值Schema/Bounds/Quantities/Rates参与调用，或adapter negative输入 |
| ErrSide | `meter_invalid_side` | 外部side不是两常量，或Compare用了Retail |
| ErrBindingMismatch | `meter_binding_mismatch` | wire与参数schema_id/side不同；合法对象运算的schema摘要/side不一致 |
| ErrAmountOverflow | `meter_amount_overflow` | ReservationAmount超过int64；Amount不会因结果超int64报此错 |

确定优先级：原始超限→JSON安全→schema/side参数有效→wire形状和值→wire身份/side绑定→集合/量界。wire.side为未知枚举是ErrInvalidVector，wire.side为合法另一side是ErrBindingMismatch；所有schema解析shape错误ErrInvalidSchema。schema ID格式错误是shape错误而非绑定错误。Parse vector若schema零值但body非法JSON，先按上述次序报ErrInvalidJSON；不得依赖调用map遍历顺序决定错误。

## 7. 有意义测试与交付边界

1. 精确wire合法/非法表：0/MaxInt64、UUID/nil/大小写、rate 0/1/GCD/分母上限、所有8registry，空/重复/乱序/缺/另一side/未知维度、null/number/指数、重复深层member、surrogate/非法UTF8/BOM/尾JSON/size边界；subset/diagnostic明确拒绝。
2. 金额反例：两项1/2合计1；不同分母1/3+1/6=1/2→1；零价完整向量；最大q*n不经int64溢出；实际超界仍可算、预占溢出失败；精确评分同ceil不同顺序。
3. 旧token兼容：输入-only0输出、0率、两个独立最大、1e6舍入边界和MaxInt64；固定seed多组随机值与独立旧公式`(I*Pi+O*Po+999999)/1000000`对照。该测试保护旧收费合同，不重复新函数作为oracle。
4. 绑定/不变性：相同UUID不同schema maximum/mask拒绝；不同side和零值拒绝；独立解析同内容可运算；修改原raw/返回canonical slice/返回big.Int不改变对象、后续金额或摘要。
5. 固定golden digest手写frame oracle，字段顺序/escape等价同摘要，schema/side/数量/费率/类型domain变更摘要变化；规范roundtrip必须相同。
6. 有界fuzz：任意byte串各parser不panic、错误不反射输入、成功必canonical roundtrip且再算/摘要一致；生成合法rate/quantity作单调性与Reserve/Amount一致性，不进行外部IO。

只跑`go test -race ./internal/metering`、相关fuzz短程及`go vet ./internal/metering`。无生产路径/依赖/迁移改动，不运行DB或main，不把纯测试当商业验收。根已冻结本精确合同，实施与验证记录由实际结果补齐；集成人最终将纯模块标为“已实现但未接线”，复核统一架构并统一refresh，不能写成媒体已支持。

## 8. 纯层实施与验证记录

2026-11-01已仅新增[`internal/metering`](../../internal/metering)：六份生产源码与两份纯测试，无新增依赖，无现有生产包import该模块，无迁移/配置/HTTP/资金路径改动。它是**已实现但未接线的纯层**，没有供应商认证权限，也不使任何媒体profile可售。

- `go test -race -count=1 -cover ./internal/metering`：退出0，最终1.744s，语句覆盖94.0%。保护严格wire/完整向量/错误优先级，8维两侧不同mask，实际超界保留/预占溢出拒绝，旧公式512组固定seed随机值及边界的双side等价，schema摘要绑定、零值/不变性和共享对象并发使用。
- `go vet ./internal/metering`：退出0。
- `go test ./internal/metering -run '^$' -fuzz '^FuzzStrictParsers$' -fuzztime=5s -parallel=2`：退出0，6.495s，309,443次；成功解析的canonical roundtrip和受控错误通过。
- `go test ./internal/metering -run '^$' -fuzz '^FuzzAmountMonotonicity$' -fuzztime=5s -parallel=2`：退出0，5.266s，137,489次；非负/单调、Amount与预占一致性、超int64拒绝通过。
- schema固定黄金摘要为`cb5ec303ae366738e83d617e68ca415a9eba025a806a1eda307d0bdd3d67fa82`，由独立Python `hashlib/struct`计算，测试另用手写length frame核每种摘要；没有以生产helper生成expected。

没有数据库、真实main/浏览器或外部供应商测试；未刷新统一架构清单，由集成人汇总并独立复核后执行。B0-S/B0-I/B0-Q/W/B1仍待各自精确冻结与实施，不能用本节的纯测试结果替代商业验收。

# 023-C-V 纯价值条带与 UTC 锚点月合同

2026-11-02，**根已授权冻结并实施的独立纯算法窄片**。依据 [C 候选 §4–5](c-contract.md#4-订阅周期和购买状态机) 的锚点月和原本金条带语义，实施 [internal/productvalue](../../internal/productvalue/value.go)。本合同只冻结下述数学输入、输出、资源和失败边界；C 候选的商品账、购买、订阅、到期任务、退款、来源证明及生产接线仍未冻结或未实现。没有迁移、数据库、HTTP、worker、网络调用或生产 import；纯函数返回值不具有支付、来源资格、退款许可、维护 coverage 或资金写入权。

## 1. 目的和不变量

购买价 `P` 和权益量 `G` 各为 `1..MaxInt64`，允许 `P != G`。只处理 `cny_credit` 对应的一维整数坐标，不处理 token、汇率或当前模型售价。对原始单位坐标 `0 <= k <= G`：

```text
F(k) = floor(P*k/G)
权益半开区间 [a,b) 的价值 = F(b)-F(a)
映射原本金半开区间 = [F(a),F(b))
```

乘积使用 `big.Int`，不经过 `int64` 乘法或浮点。先映射绝对端点再相减，不能对每一段独立计算 `floor(P*(b-a)/G)`。任意无重叠分拆后价值相加保持原区间价值；整份 `[0,G)` 恰为 `P`。当 `P < G` 时合法非空权益可能映射为空本金区间，价值为零，不能向上取整补一微元。

来源归因只接收**已按原购买次序排列的本金区间分区**，以输入的零基 `edge` 索引返回金额。函数不接收 source kind/ID/refund_class，不选择或重排资金来源，也不能验证来源存在、租户、FIFO、现金资格、可退状态或这份输入是否已经消费/退回。后续资金集成人必须从已验证的不可变原购买边建立分区，再以同一索引绑定原来源。两次独立调用可以重复计算同一范围；本模块不提供去重、余额或防重复退款状态机。

## 2. 固定 Go API

```go
const MaxRanges = 4096
const MaxFundingEdges = 4096

type Range struct { Start, End string }
type Value struct {
    Units string
    Amount string
    MoneyRanges []Range
}
type Attribution struct {
    Value Value
    ByEdge []string
}
type Terms struct { /* private validated P/G; zero value invalid */ }

func New(purchaseAmount, allowanceAmount string) (Terms, error)
func (Terms) Value(ranges []Range) (Value, error)
func (Terms) Attribute(ranges, fundingEdges []Range) (Attribution, error)

type Term struct { StartsAt, EndsAt string }
type TermState string // scheduled | active | expired
func MonthAt(anchorUTC6, monthOffset string) (string, error)
func TermAt(anchorUTC6, monthOffset string) (Term, error)
func TermStateAt(anchorUTC6, monthOffset, asOfUTC6 string) (TermState, error)
```

模块只依赖 Go 标准库，全部调用无 I/O、系统时钟、全局可变状态或 goroutine。`Terms` 是可值拷贝的只读参数，不是权威 proof；输出是普通可修改数据。新结果与输入和其它结果没有可修改 slice 别名；返回后修改结果不影响 Terms 或后续计算。模块不提供 JSON 解析或 DTO 兼容承诺，HTTP strict JSON 仍由未来独立合同负责。

### 2.1 规范整数和区间

金额、坐标、offset 全部只接受 ASCII 十进制字符串：`0` 或 `[1-9][0-9]*`，最多 19 字节，值不大于 `9223372036854775807`。拒空、正负号、空格、换行、前导零、小数、指数、Unicode 数字、NUL 和溢出。P/G 禁止 0；坐标和 offset 允许 0。

`Value` 的 ranges 最多 4096 条；nil/空集合表示零权益，返回 `Units="0"`、`Amount="0"` 和非 nil 的空 `MoneyRanges`。每条须 `0 <= Start < End <= G`，按 Start 递增、不重叠；**允许相邻，保留原分段**，不排序、不合并、不静默去重。反序、重复、重叠、空范围或越界全部失败。不要求这些选中区间覆盖整个 G，也不把它们解释为任何权益桶。要验证五桶完整覆盖仍需后续独立 source/event verifier。

`Value.Units` 为选中区间长度之和；`Amount` 为每段端点价值之和。`MoneyRanges` 与输入等长同序，每段保存其 `[F(Start),F(End))`，包括 `Start==End` 的零价值结果。输出规范十进制，无浮点或截断；两项总额分别不超过 G、P。

`Attribute` 的 fundingEdges 数量为 `1..4096`。每段为规范非空本金范围，首段 Start=0、每段 Start=前段 End、末段 End=P；不允许缺头/缺尾/空洞/重叠/反序。它保留原本金条带，不根据选中范围修改分区。`ByEdge` 与 fundingEdges 等长同序，包含零金额项；第 i 项为全部 MoneyRanges 与第 i 个原本金区间交集长度之和。`sum(ByEdge)=Value.Amount`，任一 edge 的金额不超过该 edge 长度。即使 ranges 为空也先验证完整本金分区，不能用零选中量跳过坏来源分区。

### 2.2 UTC 锚点月和半开期限

时间只收精确 27 字节 `YYYY-MM-DDTHH:mm:ss.ffffffZ`，公历年份 0001..9999，精确 UTC6，拒年份0/10000、无效日历、闰秒、offset 时区、非六位小数及所有变体。不依赖 `time.Local`、进程 TZ 或当前时间。

`MonthAt(anchor,n)` 从**原 anchor 的年月**直接增加 n 月，保留原 anchor 日、UTC 时分秒微秒；当目标月不足原日数时夹至目标月末。1月31日→2月末→3月31日，不能用上一次夹末结果递推。n=0 返回原 anchor。offset 虽按 int64 解析，目标年月超过9999即失败，不让整数加法溢出。函数不会倒退月份，不提供负 offset。

`TermAt(anchor,n)` 只计算 `[MonthAt(anchor,n), MonthAt(anchor,n+1))`，两端必须都能表示；n 是**零基数学月偏移**，不决定未来数据库 term_index 从0还是1开始。9999年12月的起点本身可由 MonthAt 返回，但不能建立越过10000年的 Term。`TermStateAt` 先生成同一 Term，再对显式 asOf 判定：asOf<start 为 scheduled，start<=asOf<end 为 active，asOf>=end 为 expired。asOf 是调用方提供的数学输入，纯函数不证明它来自数据库权威时钟，也不改变任何期限或权益桶。

## 3. 资源、错误与失败原子性

Value 为 O(r)，Attribute 采用有序双指针为 O(r+e)，不创建 r×e 交集矩阵或逐权益单位展开；输出最多4096个范围和4096个归因金额。月运算 O(1)，不按 offset 循环。解析前先以长度/数量判界，拒超限而不截断；本纯层不接受 context/deadline，其有限操作不承担数据库或HTTP准入期限。

公开错误为以下固定 sentinel，直接返回且支持 `errors.Is`，不拼接原输入、ID、金额或时间：

| 错误 | 范围 |
| --- | --- |
| `ErrInvalidValue` | P/G 非法、零值 Terms 或非法 offset |
| `ErrInvalidRanges` | 选中权益范围格式、顺序、重叠、空或边界非法 |
| `ErrInvalidPartition` | 本金分区为空或格式、连续覆盖非法 |
| `ErrResourceLimit` | 任一输入 slice 超过对应条数上限 |
| `ErrInvalidTime` | anchor/asOf 非规范 UTC6 或无效日期 |
| `ErrMonthOverflow` | 合法 offset 使目标年月/term 末端超过9999年 |

Value/Attribute 先拒零值 Terms，再检查两个 slice 条数上限，再验证选中 ranges，最后验证本金分区。月函数先验证 anchor、再 offset；TermStateAt 先验证完整 term、再 asOf。多种错误同时出现时遵循该顺序。任何失败只返回零值结果（包括 nil slices、空时间/状态），不返回部分合计或部分归因。所有输入保持原样。

## 4. 验证与交付边界

必须覆盖：P=100/G=150、P=3/G=10、P>G、P/G=MaxInt64及乘积超过int64、整份/分拆可加、零价值范围不生钱；混源分区/跨边交集/零edge金额/空选中/坏分区和限额；输入/结果别名、固定安全错误和零值Terms；1月31日/非闰与闰2月/3月31日、1900与2000年、原锚多月、微秒、0001/9999边界、offset上界、term end半开以及严格时间拒绝。数学 oracle 采用独立小整数逐单位枚举和确定性随机分区，不只重复被测端点公式；额外 fuzz 验证合法分段可加、原本金归因守恒与资源判界。

定向 `go test -race ./internal/productvalue`、`go vet ./internal/productvalue` 必须通过；短程 fuzz 另记实际命令和持续时间。没有数据库/供应商/浏览器/正式收款验收，本片不需要或声称这些证据。集成人汇总后同步架构、HLD/LLD、README能力状态与清单；本并行实现者只更新本合同、新package及[023计划](plan.md)，不单独 refresh。

## 5. 实施结果

2026-11-02完成本纯片实现。源码为 [value.go](../../internal/productvalue/value.go)、[month.go](../../internal/productvalue/month.go)，验证为 [value_test.go](../../internal/productvalue/value_test.go)、[month_test.go](../../internal/productvalue/month_test.go) 和 [fuzz_test.go](../../internal/productvalue/fuzz_test.go)。只依赖标准库 `errors/math/big/strconv/time`。当前 `go list -deps ./cmd/moni-aigate` 的366项依赖不含 productvalue；没有新增迁移、持久写入或生产导入。

| 实际执行 | 结果 |
| --- | --- |
| `go test -race ./internal/productvalue` | PASS，包执行1.647s；八个单元顶层测试及全部fuzz seeds，包含4900组独立小整数逐单位/本金edge枚举oracle和不可变Terms并发读取 |
| `go vet ./internal/productvalue` | PASS |
| `go test -coverprofile=/tmp/moni-productvalue-cover.out ./internal/productvalue` | PASS，statement覆盖100.0%；覆盖率不等于生产或资金验收 |
| `go test ./internal/productvalue -run '^$' -fuzz '^FuzzUnitAndOriginalEdgeOracle$' -fuzztime=3s -parallel=2` | PASS，403186次执行，包3.878s |
| `go test ./internal/productvalue -run '^$' -fuzz '^FuzzLargeAbsoluteSplitIsAdditive$' -fuzztime=3s -parallel=2` | PASS，441958次执行，包4.007s |
| `go test ./internal/productvalue -run '^$' -fuzz '^FuzzStrictInputsFailAtomically$' -fuzztime=3s -parallel=2` | PASS，453636次执行，包3.694s |

以上是同一实现的定向本地数学验证，未运行数据库、真实支付、供应商、浏览器或生产测试，也不将它们列为本片已通过。整仓与架构同步由根集成人在并行结果汇总后执行；C-F来源证明、账务/权益状态机、迁移、购买接口、退款拒付及完整C商业验收仍后续。

# 022-C0-V0-WS：纯全钱包序列声明检查（根冻结）

2026-11-02。依据 [全序候选](c0-sequence-contract.md)、[完整 verifier 候选](c0-verifier-contract.md)、[生产 DDL 候选](c0-production-schema.sql)。本片先冻结独立、无数据库权威的结构检查；完整 lot/FIFO/退款重放仍按原候选后续交付。实施前已复核 HLD/LLD 与维护读取边界。

## 1. 接口和输入投影

```go
func CheckWalletSequenceRows(context.Context, WalletSequenceRows) (WalletSequenceRowsCheck, error)
```

七组原始列投影，全部由 caller 提供；不接受旧 report、Balanced、coverage、分配 planner 的输出或 SQL validator 结果。

| 组 | 原始列投影 |
| --- | --- |
| Epochs | funding_lot_epoch.id |
| Tenants | tenants.id, fl_created_xid（可 NULL） |
| Operations | funding_wallet_operations 的 id,tenant_id,epoch_id,seq,previous_seq?,previous_id?,kind,source_id,journal_id?,journal_operation_id?,event_count,available_delta,held_delta,consumed_delta,issued_delta,created_xid |
| Heads | funding_wallet_heads 的 tenant_id,epoch_id,tail_seq,tail_operation_id |
| LegacySources | funding_wallet_legacy_sources 的 epoch_id,tenant_id,kind,source_id,journal_id,journal_operation_id,amount |
| Journals | journals 的 id,tenant_id,operation_id,kind,currency,created_txid |
| Events | funding_lot_events 的 id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,operation_id?,journal_id?,created_xid,available_delta,inference_held_delta,consumed_delta,issued_delta |

SQL NULL 以 *string 保留；空串不等于 NULL。新 epoch/operation/event/lot/head-tail/previous ID 是非零小写规范 UUID；既有 tenant/source/journal/原operation 身份保留原 TEXT（含空串和空白），不强转 UUID、不 trim。字符串须 UTF8、无 NUL；operation/legacy/event kind 仅接受固定值，未被引用的 journal.kind 保持原自由 TEXT。原时间完全不参与本片投影：不以 clock 或 xid 给 operation 排序，不对合法 PG finite 时间范围增加约束。

seq/previous_seq/event_count/tail_seq 为规范非负 BIGINT 十进制；operation/event/tenant birth xid 为规范正 uint64；原 journal.created_txid 为规范正 BIGINT，再作大整数数值比较。amount 为非负 BIGINT，funding/refund legacy amount 必须正。NUMERIC delta 独立使用 math/big.Int：允许十进制整数和小数部分全零的积分值（1、1.0、1.00 数值相同，允许 +/- 与前导0，负零归零）；拒绝非零小数、指数、NaN、Infinity 和不合法语法。不得 int64 累加四 delta 或跨事件金额。

## 2. 检查范围

1. Epoch 恰一行（空业务库也须有一行）；本片仅核 ID 及引用，不验证 epoch/attestation、OID、manifest、basis、安装或维护真实性。空 epoch 计 mismatch，不能推断已初始化。
2. 每 tenant 恰一条从 seq0 开始的连续序列、一个 head。逐条前驱精确指向同 tenant/epoch 的 seq−1 operation；无洞、无分叉、无遗留未推进尾。head 必须指全部提供 operation 的唯一末尾。BIGINT MAX 不做加一或 wrap；与提供行序无关。
3. seq0 的 source_id=tenant.id，previous_seq/previous_id/journal_id/journal_operation_id 均 NULL。birth NULL 只能 epoch_opening；birth 非 NULL 只能 tenant_genesis 且与其 created_xid 相等。tenant_genesis 四 delta、event_count、实际 events 均为零；epoch_opening 只允许 legacy_opening event，可以零额。本片不证明 opening 集合完整。
4. 非 seq0 只有七个业务 kind（含两种退款）；原 journal 和原 operation ID 非 NULL，精确核 journal 的 tenant、operation ID、currency=CNY、kind 和正 top-xid。funding 三类 journal operation 不强行等于 receipt ID；reserve/finish/两种refund的 journal operation 等于 source ID。finish 的原 kind 可 settled/released，其余对应 credit/voucher_grant/sandbox_capture/reserve/refund。每个 event 的 tenant/seq/原journal/原operation/top-xid 与所属 wallet operation 精确相同。同一 top-xid 的多个 operation 各自合法。
5. 全局自然键独立去重：主键、journal.operation_id、(tenant,seq)、非空 previous_id、非genesis (kind,source_id)、非空 operation.journal_id、legacy (kind,source_id)/journal_id，以及 event (wallet_operation_id,lot_id)。两个新 refund kind 的 source key 均归一到 consumption_refund；旧表只允许六种 kind，且必须属于 birth=NULL 的旧 tenant，新 tenant 出现 legacy source 计 SourceBoundaryRows。任一新 source 与 legacy 同 key 或 journal 冲突都不符，即使金额相同。funding source_id 指 receipt.id，不能替换为 receipt.source_id。legacy 的原 journal 同样核 tenant/operation/currency/kind；legacy reserve/finish/consumption_refund 的原 operation ID 也须等于 source_id，但其历史 xid 不要求等于新 operation。
6. event kind 映射为 epoch_opening→legacy_opening、三 funding→funding、其它同 wallet kind；tenant_genesis 不允许 event。每 operation 的实际 event_count 与声明相同，四 delta 分别等于全部 event 的独立大整数合计。每 operation/event 的 A+H+S=I，I>=0；不以总和守恒代替四项等值。
7. 本片不从 kind 猜真实 R/C/F，也不证明 kind 对应的四 delta 形状、对应业务源存在、金额资格、完整 legacy 成员、任一 journal 的完整分录、单 lot 前驱/revision/桶、三类 allocation、FIFO 或退款顺序。自洽改写一组 operation/event 的金额仍可能通过本片；它们必须由后续完整原源与 lot 重放验证。未被引用的 journal 可属于非钱包业务，不在此处猜其是否为孤儿。

重复键不得任选一行作为真值。重复组每一自然键只计一次；确定受损的关系按各方向每物理行最多计一次，继续枚举其余事实。非法字段是数据 mismatch；nil context、资源或取消失败才返回封闭错误与整个零结果。所有检查都不修改 caller 的 slice、string 或指针目标。

## 3. 结果与资源

结果仅 Scope=`supplied_wallet_sequence_rows`、RowsConsistent、七组 Scanned 和固定 Mismatches 计数。不输出任何行身份、金额、xid 或正文；没有 Balanced/CoverageComplete/ScanProof/RuntimeProof。不提供完成记录或摘要转权限的构造器。即使 RowsConsistent=true，也不能证明 caller 没有省略全部尾部或整个 tenant，不能用于 init/epoch 或资金准入。

固定合计 100000 行、16MiB 原始字符串，单字段最大 65536B；NULL 无文本开销。预算覆盖七组输入、所有指针文本，并在索引和大整数分配之前检查；这是输入界，不承诺整个进程 RSS。无可放大的公共配置。不沿用完整候选的百万行/256MiB预算。循环每至多256工作步检查 context，排序及大整数解析前后检查；不后台重试。四种固定错误 Input/Resource/Canceled/Timeout 均返回零结果，错误不带 SQL、身份或输入正文。

## 4. 验收与架构

手算正常：空业务库、旧零钱包 seq0、新 tenant genesis、同 top-xid 多操作、乱序输入、零额reserve/finish、超过 BIGINT 的 delta 合计、scale-zero NUMERIC。损坏：缺epoch/tenant/genesis/head、漏中间/尾未推进/分叉、跨 tenant/epoch 前驱、两种 refund source 重用、legacy/new source/journal重叠、funding receipt ID 与原journal ID不同、坏event kind/seq/top-xid/四项合计、duplicate组不依赖输入顺序、非法TEXT/数值、UINT64/BIGINT边界、取消/超时/超长全零、输入不变。预期由手算给出，不调用 billing planner 或写侧SQL。

实现只在 internal/fundinglotverify 新增纯函数，不读数据库、不新增 ScanKind/once 位、不改 ReadLease/CompleteSourcesResult 私有 facts、schema/角色/迁移/main/HTTP/资金 writer。普通运行依赖隔离保持。统一架构、HLD/LLD、README、计划与验证记录同步注明本片仅为完整重放的前置检查。

# 兼容代码与候选代码盘点

盘点基线：2026-11-04，提交 `e0c398d`。

本文件是代码整理阶段的盘点。目录名带有 `legacy`、`compat`、`prefix` 或 `candidate` 不等于可以删除；最终是否保留必须以正式启动、迁移、恢复和专项验证的引用关系为准。只服务历史维护扫描的 raw reader 已通过 `historical` build tag 从默认当前构建图隔离，仍保留给显式历史专项。

## 规模统计

| 目录 | 文件数 | 代码行数 | 当前判断 |
| --- | ---: | ---: | --- |
| `internal/fundinglotprefixcompat` | 3 | 494 | 历史兼容材料，被 runtime/维护验证引用 |
| `internal/fundinglotruntime` | 20 | 4,134 | 资金 runtime/历史组合验证，需确认正式接线 |
| `internal/fundinglotscan` | 36 | 8,709 | 资金来源读取和验证，部分进入正式账单读取 |
| `internal/fundinglotverify` | 66 | 25,543 | 钱包、采购、周期预算和历史边界核验 |
| `internal/maintenancescan` | 18 | 1,797 | `historical` tag 下的维护扫描，默认当前构建不编译 |
| `internal/maintenancetrust` | 81 | 22,031 | 维护权限、快照、lease 和恢复证明 |
| `internal/metering` | 10 | 1,476 | 当前推理和结算依赖；含旧 token 边界适配 |
| `internal/periodicbudgetprefix` | 5 | 1,730 | 周期预算候选前缀核验 |
| `internal/periodicbudgetprefixschema` | 4 | 650 | 候选 SQL/权限材料 |
| `internal/procurementaccountwriter` | 7 | 1,954 | 采购账户候选 writer，主要由专项测试使用 |
| `internal/procurementattemptbirth` | 33 | 25,616 | 采购 attempt 候选 birth/observer/executor |
| `internal/procurementexecution` | 17 | 5,857 | 采购观察与执行结果，存在业务错误映射引用 |
| `internal/procurementprefix` | 81 | 31,775 | 采购历史重放和来源连续性候选实现 |
| `internal/procurementprefixschema` | 11 | 4,919 | 采购候选关系和 SQL 材料 |
| `internal/qa/c0candidate` | 4 | 945 | 纯候选 QA 测试，无生产源码 |
| `migrations` | 34 | 6,097 | 正式迁移和历史 001–022 验证材料混合 |

## 按正式非测试源码引用统计

下表统计该包名被 `cmd/`、`internal/`、`migrations/` 中非测试 Go 源码引用的文件数。数字为引用文件数，不是调用次数。

| 包 | 非测试引用文件数 | 初步分类 |
| --- | ---: | --- |
| `fundinglotprefixcompat` | 2 | 验证/runtime 材料依赖 |
| `fundinglotprefixschema` | 0 | 未发现该目录/正式引用 |
| `fundinglotruntime` | 0 | 当前未发现直接正式导入，需继续追踪间接路径 |
| `fundinglotscan` | 18 | 正式资金读取与维护验证混合 |
| `fundinglotverify` | 49 | 正式账单/资金读取依赖，不能直接删除 |
| `maintenancescan` | 0 | 仅 `historical` tag 下的专项/维护内部路径 |
| `maintenancetrust` | 9 | 启动/维护相关依赖，需保留边界审计 |
| `metering` | 5 | 当前推理、结算和报表依赖 |
| `periodicbudgetprefix` | 4 | 候选周期预算路径，尚未确认正式在线接线 |
| `periodicbudgetprefixschema` | 1 | 测试支持安装材料引用 |
| `procurementaccountwriter` | 0 | 未发现正式非测试导入 |
| `procurementattemptbirth` | 0 | 未发现正式非测试导入 |
| `procurementexecution` | 3 | 采购业务错误/观察路径引用 |
| `procurementprefix` | 0 | 未发现正式非测试导入 |
| `procurementprefixschema` | 0 | 未发现正式非测试导入 |

## 当前确认的兼容代码

- `internal/metering/legacy.go` 被 `internal/inference/plan.go` 使用，当前仍是正式请求计量边界的一部分。
- `internal/fundinglotverify` 中的 `Legacy*` 结构和 `legacy_*` 事件用于旧资金来源、钱包重放和历史边界核验。
- `internal/fundinglotcatalog` 的 `LegacyV1` 与 `WalletReadV2` 是资金 profile 判定的一部分，不能只按命名删除。
- `migrations/original22.go` 验证 001–022 的历史迁移快照，当前被候选安装/历史验证使用。
- `internal/periodicbudget/reader_pages.go` 将旧来源映射为 `legacy_customer` / `legacy_supplier`，这仍影响预算读取 DTO。
- `internal/billing` 中仍存在 `legacy` 价格快照、资金和采购兼容语义。只服务历史维护的 reader 已由 `historical` tag 隔离，当前 wallet/billing 主链文件保持默认编译。
- 支付沙箱和 runtime 代码中仍保留旧资金摘要，用于验证测试环境事实未被篡改。

## 当前确认的候选/验证代码

- `internal/qa/c0candidate` 只有测试文件，没有生产实现文件。
- `internal/procurementprefix` 主要是采购历史重放、来源闭包、连续性和候选合同实现；没有发现正式非测试导入。
- `internal/procurementprefixschema` 主要是候选关系、约束和 SQL 材料；没有发现正式非测试导入。
- `internal/procurementaccountwriter` 没有发现正式非测试导入，当前更接近专项 writer 材料和测试支持包。
- `internal/procurementattemptbirth` 没有发现正式非测试导入，当前更接近候选 attempt birth/observer/executor 实现。
- `internal/periodicbudgetprefixschema/materials/` 包含候选 SQL 和权限材料，尚未发现正式迁移安装入口。
- `internal/fundinglotprefixcompat` 和 `internal/fundinglotruntime` 虽然具有候选/历史性质，但仍被维护验证代码间接引用，不能按“无正式导入”直接删除。

## 暂不归类为无用代码的部分

以下内容虽然包含 `legacy`、`compat` 或 `candidate` 字样，目前不能视为无用：

- 资金账本和来源关系的历史边界检查。
- `migrations/original22.go` 及其 PostgreSQL 测试。
- `fundinglotverify` 的旧来源、旧退款和旧钱包重放逻辑。
- `metering.LegacyTokenBounds`，因为当前推理计划仍调用它。
- `procurementexecution` 的观察结果和错误映射。
- 维护 Gate、Snapshot、ReadLease 及其恢复状态验证。

## 后续整理顺序

1. 继续沿 `cmd/moni-aigate`、`serve`、`ready`、worker、迁移和账单读取入口追踪间接引用。
2. 给候选包补充明确的包级边界说明，区分“只允许测试调用”和“可被维护路径调用”。
3. 找出没有生产、维护或专项测试调用方的孤立文件，再单独评估是否属于无用代码。
4. 对仍需保留的兼容代码集中命名和注释，避免与当前业务实现混淆。
5. 在完成盘点前不删除兼容代码、不修改迁移、不改变旧数据读取语义。

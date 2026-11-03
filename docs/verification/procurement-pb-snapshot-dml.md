# PB 命令动态前缀与账户 DML 失权续片

2026-11-04；源码基线 `6c03c15`。先冻结[动态快照合同](../../specs/023-periodic-products/b-procurement-pb-snapshot-prefix-contract.md)和[必要 DML 失权合同](../../specs/023-periodic-products/b-procurement-account-writer-dml-fault-contract.md)，分别实施纯核和真实角色故障测试。此前关系与 APP 提交故障的证据保留在[上一片](procurement-pb-relations.md)，不与本片重复计数。

## 实施范围

[动态前缀叶子](../../internal/procurementprefix/source_closure_pb_snapshot_prefix.go)已接入同次私有 `checkSourceClosure`。它从 raw 原件独立核多值身份、完整观察链与来源、配置绑定及事件 before/after/delta，按每条命令自己的 event_seq 重放 allocation/window/control 桶；窗口出生与命令的 ordinal 决定当时是否存在窗口。同资金 cut 的先出生和后出生可以产生不同回执，今天的金额、revision 和 tail 不作历史 expected。

covered command 的 carry、窗口 held/spent、window revision 与最新真实 tail 均逐值比较。零 delta 仍推进计数和 tail，NUMERIC100 与负 spent 更正保持原接受集；可靠定位的坏金额后缀不污染更早完整 cut，无法定位的 seq 或跨 control 物理重复不被任选。legacy 完整 NULL 只保留缺证，有覆盖声明的缺边不能降为 legacy。所有阶段共享原 work/context/绝对期限，不新给预算或修改输入。

根与独立审查发现并修复了三类问题：重复 control/scope bucket 可能反复遍历同一历史；错误 event 引用外部合法 allocation 时可能误标其窗口；同control/同规范seq的重复后缀可能误拒更早完整cut。现按 distinct control 一次处理链、缓存不可变索引 bucket 的标记，并先核 allocation 实际归属；可定位重复仅从该seq起失效，跨control/跨seq/非法seq仍不能任选。持久反例包含三 control、64 种事件排列、跨 control 重复 PK，以及输入从128增至256时的工作量约束。根另补全入口 cut0 错 carry 精确只标 operation、legacy 同金额保留缺证的回归。

[账户 DML 测试](../../internal/procurementaccountwriter/account_dml_fault_postgres_test.go)固定138个不同必要 grant，按 create123、changed-configure109 展开232次真实根故障调用。每项先确认 W 有效权限 true，再准确单项撤权至 false；actual APP 调原根必须保留原42501，独立 rollback、精确单项恢复至 true，并逐项比较完整 ACL 集合及九表全行快照。恢复探针用新 operation，正常执行后显式回滚；不把探针算作提交证据。25个当前两根未使用的 journal 授权另做50次成功回滚正控制，不虚计为必要失权。

## 实际验证

纯核独占专项14顶层组race PASS（2.074s），其后根在最终文件冻结并接线后执行 `env -u TEST_DATABASE_URL go test -race -json -count=1 ./internal/procurementprefix`：**220顶层/1123子项全部PASS，0 SKIP/FAIL，4.183s**。包括新叶子、真实私有入口和原包回归；专项不与整包重复加总。旧 `CheckProcurementEvidenceGraph` 定向race PASS（1.646s），原实现没有修改。

真实PG命令显式使用专用 `TEST_DATABASE_URL`、`/usr/bin/caffeinate -di` 与 `go test -race -p=1 -json ./internal/procurementaccountwriter -count=1 -timeout=180s`。首轮新观测查询把PG内部char类型`attgenerated`直接扫描至string，在257个新子项进入撤权前失败；8个原/静态顶层、221个原子项通过，整包12.300s FAIL。这不是有效失权证据。只将测试查询改为`attgenerated::text`，业务SQL、固定ACL与断言不变后，最终整包**10顶层/478子项全部PASS，0 SKIP/FAIL，14.894s**；包括232失权、25组/50次正控制以及此前203项APP提交撤权与4锁故障。真实APP身份、原42501、精确有效权限/ACL恢复、九表回滚、fixture清理均实际执行。

| 忽略的本地原件 | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/moni-account-dml-6c03c15.json`（首轮FAIL） | 709968 | `3893846c94de8ee6dc40f516c0f713453ef7279edc417be47ac7765115d6d79b` |
| `.local/moni-account-dml-6c03c15-reviewed.json` | 592332 | `eb55c9d65082f6e293ca7b91a165abb2c9885f2e558caccfcd4285d2dec7d2df` |
| `.local/moni-pb-snapshot-prefix-final.json` | 1441287 | `cfef4e3d4669850e77b041e38585de22e6a7cb2f0bd154d57176ac93ffcd851b` |

catalog 正式矩阵与本纯核/账户矩阵分账，见[catalog记录](periodic-prefix-catalog-cpu.md)。设计、无库编译或 SKIP 不计 PostgreSQL 通过；这些耗时不是网关性能指标。

## 下一真实根的设计

[完整 attempt 出生合同](../../specs/023-periodic-products/b-procurement-attempt-birth-writer-contract.md)已审定，并同步来源/schema/执行/ACL上位合同。原 upstream INSERT 明确允许先于来源；managed p 的 BEFORE 按 `pb_procurement_stamp` → `procurement_reservation_identity` → 拟新增 `zz_pp_attempt_birth_p`，末序 hook 从真实 NEW.pb_admitted_at 一次完整生成 origin/reserved transition，派生 p pointers 后 p 本体才写入；closure 完成 PE/A/PB/journal/node/head 后唯一 seal。replay 返回原五字段，root/source均NULL，不补旧证据。

这条根尚未实现。真实 request 自动出生、command+reserve 新版 guard/validator、exact-link、最小权限及实际 LOGIN 验收是显式前置，不能叠上函数绕过原账户 v1 对非命令的 PP906。A0–A6 顺序与缺失正文已列入合同。

## 保留边界与架构复核

来源仍 `Complete=false`、Qualified 为空，无公共 `Check`。legacy精确cut、历史scope absence、首配完整成员、B-HO1、实际业务资格与逐对象依赖、完整Base/aggregate、其余writer/profile及R2仍未完成。715/482/173列边界、已应用001–022与旧profile不改签；正式迁移、线上入口、部署与生产权限没有变化。

README 的项目特点、API/源码启动、Compose/1Panel、初始管理员和许可/验证范围已独立静态复核；167个本地相对目标存在，另补仓库导览。此项不替代实际容器部署；本机 Docker daemon 尚未运行。统一架构的模块图、数据与权限边界、能力状态及源码链接随本片更新，汇合后再 reviewed refresh、只读检查和仓库检查。

最终汇合后已完成统一架构语义复核并刷新1798文件快照，随后只读检查及`make check`全部PASS，包含32表715列/68 generated布局、`go vet ./...`和Vue strict/Vite构建；前端完成后`go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` PASS。归档本段后再刷新并只读核验，无行为代码继续修改。本轮未运行整仓`make verify-mvp`，所列纯核/真实PG/catalog专项和旧checker回归分别记账，不据专项通过宣称完整商业或生产验收。

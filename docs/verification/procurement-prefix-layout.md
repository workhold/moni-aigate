# 采购历史列布局与合同交叉核对

2026-11-03；源码基线为`ed1badcf455f4a6f43d072de400198d43c2cfe07`。本片交付[物理布局](../../specs/023-periodic-products/b-procurement-schema-contract.md)、[执行锁图/错误](../../specs/023-periodic-products/b-procurement-execution-contract.md)、[完整ACL](../../specs/023-periodic-products/b-procurement-full-acl-contract.md)和[重放读取](../../specs/023-periodic-products/b-procurement-replay-read-contract.md)合同，以及开发期列布局检查；没有运行PostgreSQL、安装候选SQL或启用业务writer。

## 已执行的本片检查

`go test -race ./scripts/check-procurement-layout`通过（2.992秒）。独立每表计数核对32张新表、711列，其中647个非generated列和64个generated列；测试同时核对宏的前缀/NULL语义、列顺序与表达式。负例覆盖未知宏/类型/后缀、重复表列、超过63字节的标识符、缺少/重复/引用不存在列的generated表达式，以及默认检查失败时不改写文件、显式写入失败时不留半份物料。

以下命令已执行成功：

```sh
go run ./scripts/check-procurement-layout --write
go run ./scripts/check-procurement-layout
go run ./scripts/check-procurement-layout --root /Users/liminghan/Documents/moni-aigate
```

[静态JSON](../../specs/023-periodic-products/b-procurement-column-layout-v1.json)明确标为`design-only-new-table-columns`。它不包括原表扩列、主键/唯一/FK/CHECK、函数/trigger/ACL或最终reader/basis/catalog材料，不可拿来安装数据库或发行权限。Make/CI新增默认只读检查，更新必须显式`--write`。

## 交叉核对实际修正

- 去除ACL中的`credential_procurement_bindings.channel_revision`：它是查询中从`channels.management_revision`取得的DTO值，不是binding物理列。
- 统一Reconcile输入列为`input_usage_status/input_input_tokens/...`；reservations的Key由request关联，billing_profile_blocks不增加来源列。
- 完整`pp_record_attempt`拥有首次DB选价与INSERT；三参出生helper固定私有并最后seal，外层只重建返回。expected报价文字使用BYTEA，原未消费的任意bytes不提前转TEXT。
- RequestHead仅覆盖latest/sticky；dispatch与普通客户finish可另外改变request状态。head0的updated_xid绑定origin出生xid，INET client_ip采用明确的PG16文本投影。
- 根当时的quote选择不被当成历史目录完整性证明；pure也不能从一个role OID整数自行认定APP授权，保护阶段须绑定owned exact catalog/pins。
- cause discriminator引用`cause_source_id`，不引用该表不存在的`source_id`。命令原件时间通过受审DB根采时、完整source及BEFORE stamp闭合，明确该两kind与旧DEFAULT的采样点差异。
- 原PB实时tuple检查采用八表系统列权限sidecar，W/APP各五表SELECT(xmin)，V4另需负attnum ACL采集；不把xmin加入历史读集或basis，也不以普通列SELECT冒充该授权。

## 上一提交的CI单独记账

读取[run 41600915864](https://github.com/workhold/moni-aigate/actions/runs/41600915864)的run/jobs元数据，确认精确head为`ed1badcf455f4a6f43d072de400198d43c2cfe07`，终态`completed/success`，10/10 jobs成功；创建08:07:08Z、更新08:37:23Z。未重启workflow，未下载并重算这次artifact的测试计数，因此不复制旧CI计数来冒充本次统计。此结果不代表承载本文的后续提交已通过CI。

## 保留的验收边界

本片只验收设计列布局工具和静态交叉核对。完整可执行DDL/函数正文、逐边约束/guard、实际权限闭包、独立业务重放、全量固定读集与V4保护链仍未实施。D1整体、I1 writer、原十一项采购缺证、B-P3及catalog原5秒race门禁不因此关闭。

此前本机整仓失败、定向PASS和远端CI各按原[记录](procurement-identity-ci.md)分别保留；未重跑本机整仓或新增数据库测试。正式迁移、生产支付、真实供应商、Docker/1Panel目标环境、HA与最终网关性能均不属于本片通过范围。汇合后已执行reviewed架构刷新与只读检查；`make check`完整通过，包含架构/候选列布局只读核验、`go vet ./...`、TypeScript检查与Vite构建。未将这些静态检查计为商业数据库验收。

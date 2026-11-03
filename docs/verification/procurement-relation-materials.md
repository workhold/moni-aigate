# B-P4 D1采购关系材料验证

2026-11-03；源码基线`7f48fecb1e567a016af2f51f80754085aab70121`。本片按[source](../../specs/023-periodic-products/b-procurement-source-ddl-contract.md)、[account](../../specs/023-periodic-products/b-procurement-account-ddl-contract.md)、[cost](../../specs/023-periodic-products/b-procurement-cost-ddl-contract.md)、[原件](../../specs/023-periodic-products/b-procurement-original-ddl-contract.md)合同交付[固定材料包](../../internal/procurementprefixschema/materials.go)。不是正式迁移或业务授权安装器。

## 交付内容

六份原始SQL分别固定SHA-256，公开API只有`SQL(schema)`与`Digests()`；单个schema按UTF8、非NUL及63字节界校验并quote一次，不递归替换输入。新表/原件列在前，全域keys先于checks和FK，避免单域提前引用另一域尚未建立的键。

32新表715列，包括647非generated和68 generated；原18表110个nullable列，无默认和回填。逐边落DDL时补四个条件字段：delivery的finalization/recovery、reset反向mutation类型、finish新settlement槽。前版[711列验证](procurement-prefix-layout.md)是上一提交实际物料，保留历史结果。

材料包含PK/UNIQUE/CHECK及validated、初始延迟的FK；不包含函数、user-trigger、GRANT、受控根或stub。纯身份SQL叶子是独立材料，没有为了让关系安装成功而加入假validator。

## 本片执行记录

专用PG最终专项命令已执行成功：

```sh
TEST_DATABASE_URL=postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable \
  go test -race -p 1 ./internal/procurementprefixschema ./scripts/check-procurement-layout -count=1 -v
```

两包均PASS，无本片PG跳过。整组catalog核对 **268个PK/UNIQUE、126个CHECK、291个FK，共685个新约束**；32新表/715列/68 generated、原18表110新增列一致。代表性legacy audit在安装前后原值一致、新组保持NULL；命名关系边12个正负场景通过。材料摘要漂移、非法schema/单次引用、三phase缺失/重复/乱序拒绝均通过；生成式oracle保留SQL字面量大小写、空白和转义，不会将不同分支错误归一。列布局含新finish分支/结果列拒绝测试。

首轮结构测试在补11个合同明确的UNIQUE前已安装成功（257 keys）；最终685约束结果覆盖这11个新键，旧首轮结果不替代终版。复核时发现表达式oracle原会忽略字符串字面量大小写，已修复并新增独立回归，再运行最终PG/race。汇合全部领域结果后，已复核架构模块/数据关系/能力边界并执行reviewed刷新与只读检查；`make check`完整通过，含架构和列布局只读检查、`go vet ./...`、TypeScript检查及Vite构建。这些静态检查不计为商业写根或生产验收。

结构测试源码：[materials_postgres_test.go](../../internal/procurementprefixschema/materials_postgres_test.go)。使用严格限定的专用PG16 `127.0.0.1:55439/moni_test`、`moni_test` LOGIN，无备用地址；随机schema按original001–022+B-P1候选安装，再执行完整phase材料。固定90秒测试总上下文、8秒fixture声明语句与3秒锁等待不是受控业务根实现，也未放宽原业务5秒或catalog原5秒门禁。

结构oracle独立读PG catalog并对比候选列布局、原件列清单和执行前原列/default；另核generated表达式、全部新FK validated/deferred、唯一键非deferred，以及历史迁移、原函数、user-trigger与显式ACL未改变。所有临时schema按精确名删除并确认零残留。

命名声明边正负例由fixture owner在事务内构造最小图，分别核root/xid、typed source、tenant复合身份、重复step、非法head联合及原件G的完整/部分NULL/错xid/错role。每例只将目标约束切为IMMEDIATE并最终回滚，其他边可能仍pending；**正例仅证明该命名声明边，不是完整root提交、实际APP writer或商业验收**。

## 上一提交远端CI

只读核对[run 41610300122](https://github.com/workhold/moni-aigate/actions/runs/41610300122)的run/jobs：精确head为`7f48fecb1e567a016af2f51f80754085aab70121`，终态`completed/success`，10/10 jobs成功。未重启workflow、未下载artifact重算测试数，不把上一提交CI成功归给本次尚未推送的代码。

## 保留边界

宽TEXT复合UNIQUE有btree索引项页宽限制，可能缩小旧接口接受集或阻止含长值的存量安装。此片空schema结构通过不能证明存量兼容；正式启用前须补合法长度边界数据，必要时先重设计精确关系与validator分工。不能截断旧数据、hash替代精确身份或跳过约束。

NULL敏感精确等值、step完整/无漏尾、各root模板、head最大项、真实时点/角色/计量/gate、旧行不得补历史及新受保护行不得伪装legacy，都仍需真实只读validator、写guard与受控根。当前没有该完整实现，D1整体、I1/A0、十一项Unproven和B-P3未完成。

本片不安装既有13套opt-in PG或1项诊断，不重跑本机全仓race或消除其原失败记录；不解除catalog原5秒race、R2正式迁移/provision/维护CLI/启动/在线资金、C1/C2/C3、商品/扩展、真实供应商/生产支付、容器/1Panel、生产/HA与性能门禁。README/贡献/SECURITY与统一架构/HLD/LLD/计划同步交付，部署流程仍使用正式迁移。

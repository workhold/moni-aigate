# 固定 109 表 basis 与严格 proof v2 验证

2026-11-03（2026-11-02开始冻结与实现）。基线提交 `19cae0638f254eded76d57d5c8fdf13323915595`；上一批 GitHub CI [41471261680](https://github.com/workhold/moni-aigate/actions/runs/41471261680) 已全部成功。本批先冻结[basis合同](../../specs/022-self-service-payments/c0-basis-reader-contract.md)与[v2合同](../../specs/022-self-service-payments/c0-proof-v2-codec-contract.md)，再并行实现、独立差分与目录审查。以下仅描述本片范围，不把历史通过结果当作本片验收。

## 本片范围

- `fundinglotbasis`：固定109表/976列原二进制basis v1，环境/原结构/send profile前置、精确帧、ONLY count/128页/完整EOF、PK与row信封绑定、逻辑预算、脱敏错误和owned summary。
- `fundinglotproof`：stdlib-only strict21字段v2，independent ordinary/sandbox golden、深复制、资源与canonical约束，嵌入旧v1原件完整语义与外层pins绑定。
- `maintenancetrust`测试单向引用纯proof叶子，与未改的v1进行兼容差分；普通领域生产与测试不增加维护依赖。

两叶子没有资金、维护phase或启动调用方。不增加ScanKind/once位、迁移/权限、HTTP/main/worker、epoch、RuntimeProof或protected writer；原Installation/Attest发行仍要求16表空。不能先Finish释放busy再拿Facts/Report授写权，未来phase须贯穿pre+basis→write→strict v2与84表post→commit-ready，且约束Commit与ready后新lease。

## 静态材料与实际目录独立审查

descriptor原39415B、109表/976列/128 PK字段，SHA `54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5`；manifest原2459B/22项，SHA `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`。追加迁移必须重审材料，不能live采样作为expected。

send profile源于离线PG16 postgres.bki，15type/16send，原12360B，SHA `1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b`。独立只读审查在专用127.0.0.1:55439/moni_test、PG16.15/Homebrew/aarch64、UTF8/C/C、NAMEDATALEN64/float8-by-value下，RR READ ONLY读取固定白名单pg_type/pg_proc，706字段逐项0差异并显式ROLLBACK。未读业务、改数据库或集群设置，详见[目录审查JSON](basis-send-metadata-audit.json)。sorted compact审计JSON SHA `bd7bd8139b1d0983e196b1f8bcbf6b8afd23fb4016ff0874bdd0eb4f349c18b5`不是wire digest；仅证明该本机目录相等，不认证C二进制或任意PG16生产环境。

## 集成验证

真实SimpleProtocol的非UTF8负例有驱动前置边界：pgx在参数插值时先拒绝client_encoding，叶子得到Query错误并返回ErrExecution/零Facts/零业务查询；这不证明叶子已读到环境。实际环境返回非UTF8的fake路径单独验证ErrMetadata。测试不能为满足预期而绕过SimpleProtocol、改编码或放宽拒绝。

根最终相关四包race 39.657秒通过，113个顶层/1528个父子PASS终态；69项历史维护C0 opt-in明确SKIP，不追溯升级。明确MONI专库运行的新basis真实PG为1顶层/18子场景/19终态，0skip；原生值、分页与全帧独立计算、RR稳定、metadata drift/birth delta、pg_proc负例rollback及临时schema清理均通过。源码与命令身份另存[basis-proof-v2.json](basis-proof-v2.json)。

最终proof fuzz 41578次/7.461秒通过，9个baseline，2worker；basis record-envelope fuzz 5秒/186770次通过，包源码帧SHA与最终root核对一致。旧v1兼容差分5顶层/511终态包含在最终race，独立3秒fuzz只有5次执行，仅作smoke。

`make check` 6.888秒通过；明确TEST专库`make verify-mvp` 1003.198秒通过：286项前端、45个Go包race、vet和构建。unit与全仓源码SHA全程相同；默认整仓没有打开C0维护或MONI basis独立PG opt-in，新basis真实PG以本页上面的明确MONI专项为准。未以历史SKIP冒充实际PG验收。

## 后续门禁

下一切片为完整protected pre+basis与私有phase owner，继而同Init原RR写入/post/epoch与未知提交查原身份、已提交epoch observer/runtime，再接全部资金路径。C1/C2/C3、正式支付、商品/订阅、媒体/扩展、生产/1Panel/HA与最后性能仍在交付计划中。本地模拟、纯codec或描述性SHA均不代表这些能力完成。

后续只读设计审查澄清：当前109表包含正常经营会改变的事实，cutover basis只适合停服plan/init即时比较；普通startup不能拿当前数据强等于历史basis。v2合同中的同事务实际basis比较明确限定未来init issuer。已有epoch的observer、历史绑定与当前图核验/最小权限仍须独立冻结，未以本片交付替代。

架构图使用Mermaid12.2.0解析渲染并视检，见[渲染证据](architecture-basis-proof-v2.json)、[模块图](assets/architecture-basis-proof-v2-application.png)与[本片细图](assets/architecture-basis-proof-v2-detail.png)。

README已再次对照CLI、角色初始化、Makefile、Compose/Dockerfile和部署手册只读复核；本地启动与首次管理员顺序一致。容器实际运行、1Panel实装、正式供应商、生产收款与性能对比仍待对应验收。

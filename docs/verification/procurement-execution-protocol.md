# A5-S1：三根无连接执行协议

2026-11-04；承接先行冻结的[prospective executor 合同](../../specs/023-periodic-products/b-procurement-prospective-executor-contract.md)。本片仅交付 `internal/procurementexecution` 的无连接叶子；不取得连接、不运行 SQL、不提交事务、不提供观察器或 `NewProspective`，也不接普通 main/provision。

## 实现责任

| 源码 | 责任与边界 |
| --- | --- |
| [query.go](../../internal/procurementexecution/query.go) | 固定 create/configure/attempt 三根 ABI，准确参数顺序与显式 pg_catalog 类型；受信 schema 用 pgx.Identifier 限定，参数 bind；私有参数快照，读取参数时再次复制 mutable bytes |
| [receipt.go](../../internal/procurementexecution/receipt.go) | 扫描前认证固定列数/列名/顺序/OID，恰一行、完整扫描/排空与 NULL union；applied/replay 账户与 attempt 的不同回执约束；返回中间 typed 数据，不是已提交业务结果 |
| [errors.go](../../internal/procurementexecution/errors.go) | 固定入口域 PP 分类、本地安全 Code/Message、原 PgError cause；未知 PP、非 PP、其它根的业务 state 保持原错误；不按开放 PG 文本猜 PB 域，不在 mapper 重试 |

quote 字符串直接复制为 BYTEA，nil quote 四项 SQL NULL，存在 quote 的空字符串保留非 NULL 空 bytes；rate 的 NULL/负值和 ignored bytes 原样保留。route JSON 不经 float64 解码，不改变完整 uint64。真正业务外层 validator 继续由后续 S3 原位调用，本包不另写更窄接受集。

applied attempt 严格核新 UUID、root/source 与 prepared/unknown；replay 保留原五字段，包括旧 TEXT attempt ID 和原 TEXT NOT NULL 的非标准 cost_status，不把新建规则套到旧快返。账户 JSON null 与 SQL NULL 分开，not_found/revision_conflict/noop 分别核对，NUMERIC(100,0) held/spent 不截成 int64。账户旧回执使用不可变 snapshot，不读取今天账户覆盖历史。

Rows 的列数/codec 失败在真实 pgx 中也会进入 Rows.Err，不能因此绕过 PP903。解码须保留同一 scan cause；独立的后续服务器/流异常又不能被零行或本地形状错误掩盖。所有失败返回零内部回执。实际事务的 rollback/COMMIT、结果发布时间和未知提交分类属于后续 S2，并非本片已实现。

## 验证范围

集成复核发现两项需要补强，均在最终测试之前修订：真实pgx Scan错误也进入Rows.Err，仍须本地PP903；以及driver允许部分跨型自动转换，单靠Scan成功不能证明固定ABI。因此新增实际pgx codec正控制，再核错误OID在decode前拒绝，包含INT8旧ID→string、BOOL成本→string和INT8 sequence→int32；TEXT sequence在当前codec自身即拒绝，不虚称其可转换。

根最终纯race为 **20顶层／56子项PASS，0 FAIL/SKIP，1.297秒**。命令为`env -u TEST_DATABASE_URL go test -json -race -p 1 -count=1 ./internal/procurementexecution`；原件`.local/a5-s1-root-final.jsonl`为77378字节，SHA-256 `e5dcdcd37e7b8d9b3f03892ef99471190981525e9b9c95a462816ca0349c4ca2`。[机器记录](procurement-execution-protocol.json)绑定最终六个Go文件及原件摘要。这没有 PostgreSQL 测试结果；模拟Rows不能证明实际APP、SQL权限、guard、锁或提交行为。

采购细图两张Mermaid由官方12.2.0与隔离Chrome156实际解析渲染，已查看新增模块图；原件在`.local/architecture-mermaid-check/procurement-attempt-a4-s1-20261104/`。图明确将S1画为没有数据库调用边的叶子，不把S2/S3规划画为线上链路。

## 下一依赖与保留差异

S2 显式连接/单根事务、原三次 billing／零次管理重试、unknown 与只读观察仍未实现。S3 显式构造器、原 API/DTO 适配和保留 cause 的 HTTP 优先级仍未实现，普通构造器保持原路径。

PP101 的安全固定 Message 只适用于原 Go 外层校验已成功的候选调用；直接 SQL 输入攻击不能要求 mapper 解析 PG Message。A2 固定根没有可信 PB 错误域 tag，因此非 PP 错误仍透明传回，PB 精确分类兼容门须另先设计，不能用 Message/Context 或某个 23514 猜测。S1 通过不关闭完整 A4/A5、S4、全域 profile 或 R2。

本片新增候选协议模块，没有数据库、角色、部署或在线请求边；统一视图与源码入口见[采购架构细图](../architecture/procurement-attempt-birth.md)。

# T2 真实维护授权与完整前驱验证

2026-11-02，按[根冻结合同](../../specs/022-self-service-payments/c0-authority-contract.md)完成库级二参 `Attest(ctx, snapshot)` 与 `Permit.Verify(ctx)`。最终显式专库 PostgreSQL race 验证退出0，耗时333.879秒：89个顶层测试、含子测试832项全部PASS，零FAIL、零SKIP；832个run各有唯一终态。[机器记录](maintenance-authority.json)保存源码摘要、全部尝试及最终日志SHA。它覆盖本轮T2及T0/T1回归，不改写历史验证记录。整仓检查、文档和发布另见[集成记录](maintenance-authority-integration.md)。

原Snapshot内部owned installation是唯一发证基础。固定链在同一rawTx与busy范围内执行编译manifest/完整history、实际维护LOGIN与owner、新鲜全runtime停服采样、16表重空、完整前驱、仅Init全部账户C序锁和出口重核；失败、取消、期限或终结开始撤销全部Permit副本并关闭原socket。没有开放Tx/Conn/context、任意SQL回调或资金写入入口。

## 三层证据范围

- **公开维护链**：ordinary named-minimal/image、public-minimal/image的实际LOGIN，以及原QA安装的genuine sandbox，均真实执行Plan/Init、Attest与Permit.Verify。NOLOGIN的静态安装成功与维护身份拒绝分开验证；SET ROLE替代身份同样拒绝。history缺失/错误/过长、原RR快照、完整账户锁/Plan无锁、新鲜runtime连接、busy零SQL、取消和终结撤销均有真实证据。sandbox七个代表异常在T1成功后实际执行CASE投影，再由公开Attest拒绝。
- **私有PostgreSQL判定层**：ordinary21表、mandatory FK、policy与六旁路，以及sandbox全部持久列、有限时间、CASE安全投影、哨兵/EOF和资源边界。部分坏行在owner回滚事务中临时放宽fixture后注入；这些不是合法公开安装，不能说全部逐列反例均走过公开Attest。五秒CPU gap用例是从真实Gate/RR/owned installation建立的私有stage watcher验证。
- **规范编码与纯判定层**：独立Python向量、原payments私有codec、原QA writer与新私有codec交叉比较。向量1786bytes，SHA-256为`1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c`。纯测试不替代真实SQL执行。

完整前驱在传输前用资格位和CASE限制长字段，单例LIMIT2、keys LIMIT129，成功业务行最多262、累计安全投影1MiB；累计五秒子预算覆盖CPU与SQL。原v1 canonical bytes、重编码bytes和原SHA三向一致，peer本地值独立保存。Init账户ID沿用无长度约束TEXT语义，按uint64大端长度和原UTF8 bytes流式摘要，没有单个DataRow额外内存硬上界的承诺。

## 原始失败与修正

保留首轮失败日志。失败涉及fixture：过长history名称先撞PostgreSQL主键索引硬界、C0安装后追加账户被epoch guard拒绝、Plan UPDATE被epoch guard拒绝、SET ROLE导致pg_stat_activity的backend_start遮NULL、原QA owner观察方式、私有坏行fixture误删trigger约束以及五位年份的文本扫描。修正仅调整合法数据准备、独立观察和测试判定，不改冻结SQL/ACL、原v1、catalog expected、生产身份或预算。最终整包已重新真实执行通过；各专项计数与原失败均保存在机器记录中。

边界观察：恰逢子预算到期而watcher尚未记录超时，Scan或纯判定可先返回安全的execution/identity类别；仍返回nil evidence、永久撤销并物理关连接。未观察到授权绕过或期限延长，不把错误分类优先级写成已保证的合同。

## 清理与环境

独立只读审计确认本轮fixture命名族schema/role/database/backend/advisory均为零；父库public由pg_database_owner持有且relations为零。PG16.15、UTF8/C/C及三项持久化配置为on，max_connections=200。测试内部另核精确随机名称、PID/backend_start/datid及每fixture的shared baseline。外部审计没有本轮运行前baseline，不将最终目录观察扩大为全配置前后不变；完整安全结果嵌入机器记录。

## 后续边界

新鲜零连接采样只证明观察时点；停服、join及禁止重连仍需未来生产协调器。T3 ReadLease与独立全来源、basis/epoch、封闭writer、全资金路径、C1/C2/C3和普通main/CLI仍后续。当前profile只认证既定PG16.15专库，不代表任意PostgreSQL或1Panel环境通过。未修改长期58123预览，未调用真实模型或支付商；本片通过不代表整个后续交付goal完成。

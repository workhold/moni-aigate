# 022-C0-T2：真实维护授权与前驱的根冻结合同

2026-11-02，根冻结后实施。本文优先于 permit/predecessor 候选中的旧三参 Attest、maintenanceStage、裸 Permit.VerifyTx 表述；不追溯扩大 T0/T1 历史证据。目标是实际发行同 Snapshot 的维护 Permit，不是来源覆盖、basis、资金写入、epoch 或 startup RuntimeProof。

## 1. 唯一公开入口与状态

新增唯一 `Attest(ctx context.Context, snapshot *Snapshot) (*Permit,error)` 和 `(*Permit).Verify(ctx context.Context) error`。Permit 所有字段私有，零值无效，复制共享撤销，String/GoString/JSON固定redacted；不导出Tx/Conn/Context、任意回调、FromDigest或兼容三参入口。Verify只核自身绑定，不发行来源或资金权限。T3 ReadLease和全部writer/CLI继续后续，不提供stub。

Snapshot增加一次authorityAttempted、唯一activeAuthority及permit指针。Attest从入口到全部CPU/SQL/出口保持原gate.busy；第二stage/Commit/Rollback/Close直接busy且不发SQL。缺原owned installation、输入/身份/SQL/内容/期限失败均消费本次尝试，永久invalid、撤销证据并物理关原socket；已成功后的重复Attest只拒绝，不再发行。每次Verify同样受原总期限/caller取消与busy约束，失败永久撤销。所有终结开始、Close、Snapshot失效均撤销Permit，不能靠复制复活。

私有authorityStage固定含snapshot、非零大小nonce、call、installed、manifest、identity、predecessor和active；purpose由Snapshot plan/init派生，caller不可提交。installed必须是Snapshot内部owned wrapper；返回给调用方的T1 wrapper不参与发证。manifest/identity等事实仅在真实固定调用成功后构造，不能从caller布尔或摘要制造。

## 2. 固定发行顺序

T1已完成双gate、唯一顶层RR、Init首次SELECT前完整ONLY125表SRE及原catalog issuer。T2依次执行：原binding/owned proof完整pins → 编译期manifest与同rawTx完整history → 真实维护身份/新鲜停服采样 → 16表重新全空 → 私有完整前驱reader → Init全部accounts C序FOR UPDATE → 出口复核 → 原子发行一次Permit。Plan不锁accounts。失败返回nil，不降级ordinary、不另Begin/连接补读、不重试。

出口重核原wrapper/Conn/PgConn/PID/backend_start/xid/RR/access、原deadline、双gate、真实身份/新鲜停服、owned proof/pins以及本authority的manifest/predecessor归属；Init须有完整账户锁EOF事实。成功只停止本次call取消监视并释放busy，Snapshot lifetime watcher继续。

## 3. 可信manifest与history资源界

允许新增migrations与paymentsandboxschema两项生产叶子依赖；禁止payments/paymentruntime/billing/operations/paymentsandboxqa适配生产入口。编译期migrations.Files按文件名C序形成name/sha256键序的紧凑JSON数组，无LF/BOM；固定22项、2459bytes、SHA `ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de`。任何增删改必须重审，不从工作目录或live rows生成expected。

先在同rawTx对受审schema_migrations执行服务端scalar资格guard：恰22行，各name非空且长度不超过固定manifest最大名字节，各checksum恰64bytes；只返回资格bool。通过后仍必须调用原migrations.VerifySnapshotTx完整核所有name/SHA，不能用count或manifest摘要代替history。此guard约束旧helper分配，不改变任何能匹配固定manifest的合法历史。

## 4. 实际身份与新鲜停服观察

配置及所有fallback、实际TCP remote/SQL endpoint、DB/schema/raw-owner/有效owner、实际Config.User=current_user=session_user及非零LOGIN OID均精确绑定原pins。ordinary本人必须是实际object owner及PB六表relowner；public仅按既定pg_database_owner→当前DB datdba解析。sandbox仅字面127.0.0.1:55439/moni_test/moni_test实际super且DBowner例外；三NOLOGIN owner、五LOGIN runtime及对象/ACL仍必须通过原完整catalog证明，不SET ROLE，不改旧PB Verifier规则。

在Attest入口、出口和后续Permit.Verify中，每次用独立SQL先 `SELECT pg_catalog.pg_stat_clear_snapshot()`，完成后再以同rawTx查询pg_stat_activity。禁止在同SELECT依赖函数求值顺序。目标DB OID及runtime OID全集精确匹配：ordinary App/Projector；sandbox再加Worker、MockRuntime、ReceiverRuntime。所有状态连接均计入，不按query/application_name过滤，不terminate任何会话。存在性字段无需pg_read_all_stats；统计刷新不改变RR商业快照。

零连接仅证明本次采样。进程先停服、取消/join、禁止重连和并发owner写是协调器前置边界，数据库采样不能伪称进程永远退出。T2不实现这些生产协调器或开放serve。

## 5. 前驱私有接缝、时间和资源

采用前驱合同全部逐字段语义，唯一 `readPredecessorAuthority(stage *authorityStage) (*predecessorEvidence,error)` 与 `(*predecessorEvidence).verifyAuthority(stage *authorityStage) error`。root提供 `(*authorityStage).verify(ctx)`（原binding/proof/pins及活动状态）和 `(*authorityStage).beginPredecessor() (*snapshotCall,context.CancelFunc,error)`。后者从父call派生累计最多5秒，经原Snapshot.stageContext安装原raw-close/stop-ack监视；reader始终不释放父busy。成功出口先短锁核父/子有效并stop子call，随后close/ack，最后cancel派生context；CPU间隙到期也必须撤销。

evidence固定含authority、nonce、v1SHA256及完整owned规范值；ordinary v1SHA256恰空字符串。App原canonical bytes、重编码bytes、原SHA三向相等，私有codec不添加原validator不存在的digest唯一限制；peer行按其UNIQUE核digest。所有时间typed pgtype.Timestamptz：Valid、Finite、非zero、UTC year1..9999、微秒精度；created/applied有限性是此次新增边界，异常历史失败关闭，不修改旧runtime。历史过期key允许，不推断active key或秘密原件。

客户端收大字段前必须SQL资格位+CASE有界投影；不WHERE隐藏坏行、不Scan后len冒充传输限制。固定上界按前驱合同：名字63、UUID36、hash64、keyID32、label/ref100、loopbackURL128、canonical65536。单例LIMIT2，keys C序LIMIT129；成功业务行最多262，越界哨兵只用于拒绝。累计1MiB按返回安全投影TEXT/bytea真实字节、bool1/OID4/int4/int8或timestamptz8计；哨兵已返回字段也计入，不重复计owned副本/JSON AST。catalog/history身份查询不计入262，仍受各自及Snapshot预算。

ordinary完整21表空集、mandatory query-conflicts FK结构、唯一false/revision1 policy及六旁路全部检查；其他商业历史允许。sandbox完整App12/14、Mock27、Receiver13、keys5/4、两migration3列逐项读取，peer本地字段分别保存不假绑v1；叶子原SQL/version/SHA保持固定。原schema/ACL/provision/expected不改。

## 6. 完整accounts锁

仅Init在身份/catalog/history/前驱全部通过后，以原rawTx执行一条固定全限定 `SELECT id FROM ledger_accounts ORDER BY id COLLATE pg_catalog."C" FOR UPDATE`，无WHERE/LIMIT/SKIP LOCKED/NOWAIT。完整消费Rows，检查EOF/Err/Close，保存count及按每个UTF8 ID的uint64大端字节长度+原bytes顺序形成的SHA256；不保留全ID数组、不假设UUID/64bytes或跳过异常行，零账户合法。

现有id为无长度约束TEXT；本片明确不宣称单个DataRow有额外内存硬上界，总执行受Snapshot/SQL/锁期限约束。若以后增加服务端资格投影或历史ID限制，必须另审，不能悄改本条全账户锁语义。count溢出、顺序/Rows错误、取消均无Permit。

## 7. 验收和交付

真实普通维护LOGIN与sandbox安装者正例；全部五ordinary profile及public有效owner解析按原catalog边界测试；原v1 golden由独立编码器、原payments私有codec及QA writer、新codec交叉比对。允许新增对应包内仅测试桥，不新增生产导出API。前驱逐列/21表/六旁路/FK/finite/超长安全投影分别有私有判定层与真实公开链证据，避免所有负例被static前置遮蔽。

重点覆盖history增删/巨字段/固定manifest漂移、原proof副本赋零不影响owned证据、runtime在T1统计缓存后新连/断连仍被新鲜采样识别、父子CPU/SQL取消、并发busy零SQL、重复/终结撤销、Init完整account真实行锁/零集、Plan无行锁、出口失败零Permit。真实PG/race仅专库，精确清理本次schema/role/backend/锁，不触58123/public ACL/他人会话。

实现后独立读审、相关专项、整仓验证、架构/HLD/LLD/SDD/证据同步并推送。T3全源、basis、epoch、原路退款/拒付、正式供应商/支付与生产验收仍须后续，不以T2或CI通过替代。

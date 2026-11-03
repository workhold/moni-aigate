# B-P3 catalog CPU 诊断与受限解码优化

2026-11-03；对应[B-P3 合同第8节](../../specs/023-periodic-products/b-prefix-profile-contract.md)。这是完整catalog核验的局部CPU诊断，不是网关首字节/首token基准，也不代表B-P3或生产验收完成。

## 先定位再修改

首次完整专库race运行发生不同正例失败。先修复两个明确的测试前置问题：C0空epoch不能新建tenant，非空PB经营证据改为owner创建采购账户、实际APP执行ConfigureProcurement与pb_observe；sandbox原审计池不具有固定APP search_path，新测试单独建立实际维护LOGIN审计池，不改变旧fixture。每个受控drift事务先通过完整正例，再施加变更，避免错误身份导致负例伪通过。

后续受限实测准确定位原LegacyV1 sandbox正例也达到总5秒：elapsed5004ms、driver631ms、driver之间4373ms；固定query101为application effective_privileges。独立CPU采样只覆盖fixture建好后的核验，排除eager expected材料解析：duration5.20s、全部CPU采样4.20s，其中decodeCatalogJSON累计1.67s（39.76%），通用Decoder累计1.61s。失败保留，没有修改root或放宽5秒。

driver之间的时间包括调度、CPU与环境暂停，不能全算作CPU。另一次根集成回归出现JSONL墙钟和Go elapsed明显不同，系统唤醒信息也表明必须排查测试执行期的睡眠。CPU采样仍能识别解码热点，但单次超时不能直接归因为纯CPU不足或root不匹配；实际验收应保持机器在测试执行期唤醒、串行运行专库，并保留先前失败与环境证据。

本地诊断文件不提交，供复核其完整性：

| 文件 | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/b-p3-catalog-timing-profile-before.jsonl` | 6185 | `5f45ce7294678ba1aafaa62093c423383c90da8eb5d8af1ccb4c9ea4881dd7b1` |
| `.local/b-p3-catalog-cpu-before.pprof` | 17566 | `288b40b9e3ef8f4428bedca1199701d205c6d74bdf4cb32b098579b53b3954eb` |

## 优化与等价边界

[固定形状解码器](../../internal/fundinglotcatalog/collector_privilege_decode.go)只处理kind首位的五字段权限对象：准确两个三元uint32引用、无转义ASCII、最多7项布尔向量、字段各出现一次与完整EOF。返回普通map、[]any与json.Number，字符串及数字不借用driver字节。其他输入全部回退原通用解析和原检查；记录大小与UTF8先检不变。SQL、raw/effective权限面、grant option、依赖计数、逐记录与全调用预算、规范化、排序、全tuple比较、前后原事务身份及总5秒期限均保持。

独立通用decoder差分覆盖全部五类权限向量、uint32边界、输入所有截断点和逐字节结构变异，以及未知/重复键、转义、非ASCII、非规范数字、NULL、向量超长、错误引用、UTF8和资源界。所有原catalog可运行纯测试通过；实际PG仍单独串行验收，明确opt-in未启用时的SKIP不计通过。

同机局部单行基准（darwin/arm64，Apple M5 Pro，300ms）为original3644ns/op、3160B/op、85allocs/op；optimized691.7ns/op、1632B/op、33allocs/op。这只证明固定权限行解码的改进，完整catalog的实际耗时与稳定性以之后专库结果为准。

可复现命令（专库连接由环境提供，不在记录中写凭据）：

```sh
go test ./internal/fundinglotcatalog -run '^TestCatalogPrivilegeDecoder' -count=1 -bench '^BenchmarkCatalogPrivilegeJSON$' -benchtime=300ms
go test ./internal/fundinglotcatalog -run '^$' -fuzz '^FuzzCatalogPrivilegeDecoderDifferential$' -fuzztime=1000x -parallel=1
go test ./internal/fundinglotcatalog -count=1
C0_CATALOG_TIMING_DIAGNOSTIC=1 C0_CATALOG_CPU_PROFILE="$PWD/.local/catalog.pprof" go test -race -p=1 ./internal/fundinglotcatalog -run '^TestPeriodicPrefixCatalogTimingDiagnostic$' -count=1 -timeout=5m
```

TimingDiagnostic是独立显式启用的定位工具；profile文件亦须额外显式指定。它不计入正常catalog验收清单。旧验证记录中的源码SHA保留历史，新变更由本次统一架构清单与最终验证记录记账。

第一次5秒fuzz进程虽返回PASS，但worker初始化导致baseline为0/5、没有实际迭代，故不计有效fuzz。后续改用准确迭代数以避免把初始化耗尽时间预算当成输入差分通过；有效执行结果须以baseline完成与实际exec计数单独记账。

固定1000x补验已完成：baseline5/5、实际execs1000、new interesting1、零失败，含worker初始化总27.431s；日志`.local/b-p3-catalog-decoder-fuzz-1000.txt`。这是解码器差分证据，仍不替代PG门禁。

设置临时保醒命令并串行运行的race复核仍在原LegacyV1 baseline拒绝：elapsed5014ms、driver587ms、query98精确对应application/type_acl（sentinel253），尚未到query101的有效权限快路径。独立CPU采样duration5.12s、CPU4.39s，通用解码累计1.68s；该采样确认instrumented通用解码/检查仍是CPU热点，核验未到全root比较，不足以判定root或权限不符。后续电源日志证实临时保醒命令未能阻止电池供电下的Maintenance Sleep，11:39诊断窗口亦发生5秒休眠；不能把全部driver间隔算作CPU，也不能把标准/race差异全部归于instrumentation。本轮保留race门禁未通过的状态，不进一步猜测性修改生产代码；标准无race的相同固定界诊断另行记账。

随后只运行一次串行、标准无race的相同诊断，通过原LegacyV1 baseline、WalletReadV2 baseline、候选安装与实际APP完整V3发行。selected V3 issuer为1938ms、driver1123ms、194次query；application/effective_privileges query101为169ms（driver47ms），规范化排序阶段约246ms。该结果证明标准执行完整root匹配；单次诊断仍不计双轮安装、全部负例或维护主链的验收。实际日志`.local/b-p3-catalog-timing-standard.jsonl`，总14.026s（测试9.60s，另含eager材料），profile`.local/b-p3-catalog-cpu-standard.pprof`。

补验原件校验值如下。该本机race的5秒时限仍未达，本catalog专项race门禁保持未关闭；标准诊断和差分fuzz通过不抵消这项缺口，B-P3整体也不能据此关闭。

| 本地原件 | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/b-p3-catalog-timing-awake.jsonl` | 5514 | `828f6784905ad6c27105ab9544db3b3f064e7c8421bec052775fa784dbbeecc1` |
| `.local/b-p3-catalog-cpu-awake.pprof` | 17380 | `87ee090b509618b7e4d369e30c1ef5aaad98c31a9259620a83539c1bdffd8b12` |
| `.local/b-p3-catalog-decoder-fuzz-1000.txt` | 557 | `77ddfb063705f2c2e3bf720d0a2cd883128c3bf6f15c526bbd3710c9259227d0` |
| `.local/b-p3-catalog-timing-standard.jsonl` | 3585 | `82ad8ee4d8d1246b3e34c160eddcddfff42de1be43a4b1044e37a1a81db0fc10` |
| `.local/b-p3-catalog-cpu-standard.pprof` | 19966 | `fa30ffd2423775f1bfb88eef0e4a6710093f04564175502fedb8cda5773d1b9b` |

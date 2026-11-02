# C0-R1-A 版本化读取权限验证

2026-11-03。基线提交 `ed95cb8048bbeb10b31d1a91c794f85303df57cd`。按[先冻结合同](../../specs/022-self-service-payments/c0-runtime-read-profile-contract.md)实现显式 WalletReadV2；当前为限定库级候选，不是非空 RuntimeProof、生产迁移、在线资金接线或正式收款验收。

## 实现与静态独立核对

- 原两套v1 expected bytes与SHA、旧operations/provision源SHA及拒绝条件不变。新增两项显式profile发行API，保留原5秒；collector类别界、完整比较和proof共用本次descriptor。
- 固定base仅派生四列ACL、四个APP SELECT位和四条真实列级ACL依赖；sandbox三scope各镜像四条，共12条metadata记录，不增加peer内容权。完整派生envelope与全部variant摘要由Go和[独立Python](../../scripts/check-wallet-profile-roots.py)交叉复算，固定结果见[roots](runtime-read-profile-roots.json)。不从live metadata填expected，不复制58MiB旧文件。
- [读取清单](../../specs/022-self-service-payments/c0-wallet-readset.json)冻结84表776唯一物理列，history另两列、共85表778列；124 bindings有985字段引用，去重按实际registry。scanner侧双向集合和29份源SHA测试已实现。

## 执行记录

完整catalog专库回归已通过：

```sh
C0_CATALOG_DATABASE_URL='<专用测试库>' go test -race -json ./internal/fundinglotcatalog -count=1 -timeout=1200s
```

包耗时653.146秒，63顶层PASS、5显式SKIP；父子终态合计283 PASS、5 SKIP、0 FAIL。五ordinary/完整sandbox各两个独立安装，actual APP全部776列与history两列均可读，显式投影和ReadWallet84物理EOF完成；APP/worker对peer内容仍被拒绝。完整live canonical独立序列化在一个named-isolated和一个sandbox fixture执行，其余变体经同一完整collector逐节点精确比较，未声称每变体另做序列化。

5个SKIP分别是大内存parser差分、metadata候选capture、性能诊断、双轮候选导出与需要旧四角色artifact的精确投影，均为独立显式opt-in，不计本轮通过。准确测试名与源码SHA见[机器记录](runtime-read-profile.json)。测试运行期间监控的137份源文件及静态材料SHA均保持不变。

旧O1真实PG/race窄回归也已通过，包110.417秒、两项顶层PASS、零SKIP/FAIL：`TestFundingLotsObservationPostgresUninitializedAndMatched` 与 `TestFundingLotsObservationPostgresSandbox`。它实际覆盖原空安装/开账后观察与genuine sandbox前驱，不冒称重跑维护包全部169项历史专项。最终整仓结果见下节，不沿用基线提交的通过状态。

根于2026-11-03 05:06:39 +08:00对专库执行只读catalog清理复核，`^flc[vp]_`命名范围的schema、role、database均为0。此观察仅覆盖catalog fixture命名范围，不扩大为全部维护资源的外部审计。

[架构图报告](architecture-runtime-read-profile.json)记录五份源SHA：26张Mermaid图均经12.2.0实际解析与渲染通过。根已查看[系统模块图](assets/architecture-runtime-read-profile-application.png)与[权限细图](assets/architecture-runtime-read-profile-detail.png)，模块状态和权限边界与正文一致；渲染检查不替代业务或部署验收。

探索历史保留：

1. 首轮纯层测试因新ACL依赖误用了结构依赖字段`referenced`，被封闭DTO拒绝；改为原角色DTO规定的`role`后，Go/Python完整派生根一致，纯层通过。
2. 第一轮真实PG/race权限漂移专项exit1：ordinary逐项负例通过；sandbox独立元数据审计拒绝。定位后确认原collector的每scope ACL dependency查询都会闭合全部八角色，因此同四条APP依赖也出现在mock/receiver文档；修订合同后仅补各四条镜像metadata，不改peer内容权或collector查询。第二次定位日志 `.local/r1-profile-sandbox-debug.jsonl` 同样保留。原日志 `.local/r1-profile-drift-first.jsonl` 保留，之后先增加安全的阶段定位再核原因，不降低产品门禁。
3. 单个named-isolated真实APP/race探索成功：84表776列及history两列、真实显式投影与ReadWallet完整EOF，包44.086秒。它早于最终test汇合，不能替代完整两轮六变体验收。

## 最终整仓与交付检查

本轮 `make check` 7.082秒、显式专库 `make verify-mvp` 1056.181秒均退出0：286项前端测试、strict TypeScript/Vite构建、46个Go测试包race、vet及最终嵌入build全部通过；另外4个Go包没有测试。137份受审源码与静态材料的SHA在整仓前后完全一致。本轮执行期间仅修正README中Gemini SSE必需的`?alt=sse`，重新渲染并记录当前README SHA；最终make check再次退出0（6.062秒），随后只读架构核对通过；准确结果在[机器记录](runtime-read-profile.json)。

默认整仓清除`C0_*`、`MONI_TEST_DATABASE_URL`和`GO_TEST_SHARD`独立opt-in，仅设置专用`TEST_DATABASE_URL`。因此默认包成功不代替前述catalog完整专项/O1窄回归，也不将其他未启用的真实进程、capture/export或性能诊断计为通过。README与关联启动/部署及架构语义经独立只读复核，550个本地链接与锚点无误；未选择项目许可证。

最终提交和GitHub CI按推送后的精确commit另行核对，不能从本地通过推定远端已经成功。本片仍只是R1-A，全部后续阶段继续。

## 明确边界

专库为 `127.0.0.1:55439/moni_test`、PostgreSQL16.15/Homebrew、UTF8、C/C、Asia/Shanghai。沿旧固定initdb/catalog profile；其他发行版、PG版本、locale、容器和1Panel未认证。

本片PG fixture独立随机schema/角色，仅证明catalog和actual APP可读性，沙箱marker为显著合成fixture、无genuine支付v1/资金epoch。不把ReadWallet执行完等同于全部业务图一致，图mismatch/unproven不由新profile授权跳过。APP/worker peer内容权仍为空，worker/projector不扩资金源访问。

当前main仍用原支付runtime路径；没有自动GRANT、listener、worker或资金writer改变。O1仍按旧维护root认证。R1-B须另冻历史/current兼容、APP自有v1/文件认证和同RR全源RuntimeProof；R2再实施正式迁移、维护CLI、启动/guard及全部在线资金路径。全计划仍继续。

# 同 session 84 表来源与钱包读取验证

2026-11-02。[冻结合同](../../specs/022-self-service-payments/c0-source-wallet-reader-contract.md)与[机器记录](source-wallet-reader.json)。新ReadWallet与第七ScanSourceWallet在同owned raw session执行原七来源算法与实际钱包lot重放；原68表入口与旧六kind保持范围。

## 实际覆盖

- 独立逐列/PK清单覆盖25组投影、四个匿名嵌入路径，68→84物理并集、共享业务表只增加tenants.fl_created_xid；9共享表只读一次。
- 手算fund40→R30→C20/release10→F5，最终A25/H0/S15、revision4；坏退款边、缺终态与守恒伪cache仍被实际钱包算法识别，来源core不因钱包mismatch跳过。
- 128/129/256/257行C序分页、singleton false/true与legacy双键；全84表统一行/字节预算、proof 65537/262144/262145例外及普通字段上界。
- native NULL/finite/±infinity/Go零/五位年份、原TEXT整数与xid8、nullable TEXT和NULL/空bytea、driver owned复制、typed nil、失败/取消/超时全零与资源优先。
- 真实owner RR先通过正式人工入账API生成40，再在随机隔离schema安装原候选DDL并手写legacy opening/lot/event/cache；原约束IMMEDIATE后真实提交。fixture canonical明确不是runtime proof；正图consistent、坏cache/缺event、RR旧快照、超长proof与末C0表权限拒绝均实际验证。
- 真实维护Plan/Init保持16表空：原Permit、第七kind、八算法、唯一Finish、独立once、旧68结果、跨Permit拒绝、副本/终结撤销、Resource零结果及backend/锁清理。缺epoch如实mismatch，不伪造开账。

## 复核与中间失败

三路实现汇合后根核status/coverage、原68行为、限额与phase边界；另行独立审查未发现P0–P2合同缺陷。README本地、Compose和1Panel命令按实际CLI/config核对，旧范围手册同步。

初期Go构建因磁盘不足在编译/链接阶段中止，未执行的测试不作通过；根只清理可重建Go缓存后串行验证。首个fake例把NULL singleton主键误当输入mismatch，已改为非主键必填TEXT；NULL键仍无法分页且返回Execution。没有为通过测试放宽主键或产品逻辑。

## 最终命令与身份

| 检查 | 实际结果 |
| --- | --- |
| 相关四包 `go test -race -p 1 -json`，明确MONI专库 | PASS；39.531秒；115顶层、960 PASS终态；原业务reader与新非空C0真实PG均执行 |
| `TestMaintenanceSourceWalletPostgres`，独立C0 opt-in专库race | PASS；wall 93.656秒；2顶层/4父子终态、零SKIP；Plan与Init仍空C0 |
| `make check` | PASS；11.714秒 |
| 明确TEST专库 `make verify-mvp` | PASS；1034.525秒；286项前端、43个Go包race、vet/build |
| Mermaid 12.2.0 | 21张解析渲染通过，根视检模块与84表细图 |

相关四包包含fundinglotscan、maintenancetrust、maintenancescan及普通依赖隔离门禁；69项独立opt-in未启用的历史测试明确SKIP，不与另行真实维护专项混计。根包命令的前后源码摘要一致，维护专项精确源码另存机器记录；随后wallet.go只补固定算法顺序的注释，未改行为。默认整仓没有打开C0安装/维护opt-in。专库为本机PostgreSQL16 moni_test；没有变更集群配置、重启评审环境或升级预览。

[图渲染记录](architecture-source-wallet.json)保留四份来源SHA、21图列表与图片摘要：[模块图](assets/architecture-source-wallet-application.png)、[84表细图](assets/architecture-source-wallet-detail.png)。图中来源与钱包reader共享检查设施，无重复68表读取，只有维护scanner实际调用lot核验，普通在线资金链仍未接线。

命令日志SHA与精确Go源码/测试摘要均在机器记录。前提交ecfd148的[CI run 41462091823](https://github.com/workhold/moni-aigate/actions/runs/41462091823)已确认success，仅证明前批；本批推送后的CI单独核对，不借用旧版本通过。

## 交付边界

Coverage只表示本固定84表/八算法域执行完整；确定mismatch也可完整coverage，缺epoch不是N/A。WalletFacts与维护结果不公开原行/tx，不接受外部报告/proof拼装；维护结果只绑定原Permit，不授资金写入、basis、epoch或RuntimeProof。

原Installation与Attest发行仍要求16表全空，Verify/BeginScan/Finish不重空；未来同Init原RR可在Attest后由protected writer写入再post读取，当前writer/phase尚未实现。已提交epoch环境的新授权须另冻，不能用原空安装入口。ReadLease没有SQL只读解析器，本固定scanner只发送受审SELECT，不借Init RW写SQL seed冒充phase。非空ownerRR只证明reader，不证明维护postphase、严格v2或生产初始化。

本批没有迁移、ACL、普通main/HTTP或在线资金装配变化。正式供应商、真实支付、容器/1Panel、HA和性能仍按原独立门禁验收。下一串行依赖是protected pre+basis、init/post/epoch及全部资金路径，然后继续C1/C2/C3、商品、扩展、生产/HA与最后性能；整体持续目标仍active。

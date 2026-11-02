# 同租约68表完整业务来源读取验证

日期：2026-11-02。依据[根冻结合同](../../specs/022-self-service-payments/c0-complete-source-reader-contract.md)，本轮新增fundinglotscan和第六ScanCompleteSources。读取范围是固定业务来源图，不能替代lot全序、protected pre/post、basis/epoch或RuntimeProof。

## 实现与独立复核

- 三路逐列审查覆盖Financial21、采购23、PB18、Sandbox33的真实DDL、DTO顺序、nullable、bytea/JSONB和主键；重叠后固定68物理表，各行/字段只计一次。
- 根修正采购scope字面，实际为`procurement_domain_evidence_graph_v1`，并补完整Read测试，避免只测投影遗漏接线失败。
- 维护入口只从真实lease取得原authenticated predecessor模式和schema；私有结果绑定原Permit与第六kind。旧五scope、权限、codec允许集、simple protocol、30秒lease及Snapshot更早期限均保持。
- 独立审查发现Require忙拒绝时不应提前注册Abort，已修正并用真实PgSleep在途屏障验证原owner仍可完成。wrapper在Require成功后要求受审调用方独占lease，不声称提供任意并发构造的claim协议。
- 资源已超限后，提前Close未EOF lease会撤销context；修正为保留原Resource原因，并保留私有adapter观察到的首个固定驱动错误，避免清理副作用把Execution或Resource伪装为caller取消。

没有迁移、新表、数据库关系、角色授权、HTTP/main或在线资金写入变更。普通main/billing/PB的生产与测试依赖隔离门禁通过。

## 原生时间的真实失败与修复

初次补测year10000时，真实PG插入成功，但pgx强制simple protocol的timestamptz文本decoder只支持四位年份，导致原Read在领域资格之前返回Execution。没有将此失败改成跳过或放宽年份要求。

先补合同，再将第六reader的固定SELECT时间列改用`pg_catalog.timestamptz_send`传原生8字节或NULL，以包内固定pgtype binary plan还原Timestamptz。没有开放caller扫描器、结果格式或改变lease协议；时间逻辑预算仍为每字段32B。NULL、±infinity、公元1年、year10000均保真；非NULL非8字节wire是Execution。

新增测试的PB预期也按原合同校准：PB本来允许PG有限年份范围，因此year10000不新增PB InvalidTimeRows；Funding/Procurement/Sandbox按各自原业务时间资格报告异常。不修改任何旧core的范围或语义。

## 已执行专项

| 检查 | 结果与范围 |
| --- | --- |
| 完整Read fake race | 通过；68表、0/1/127/128/129/256/257页、四tuple、BIGINT数值cursor、false/空键、共享预算、原生坏值、bytea所有权、各阶段错误全零；新native wire专项5.068秒 |
| 最终相关四包race | fundinglotscan 6.882秒、maintenancetrust 20.144秒、maintenancescan 16.179秒、online-dependency-qa 7.354秒；opt-in未配置项明确跳过 |
| 原生时间修复后真实SQL race | 12子项全部通过，包2.797秒；保持simple protocol，包括全部68表SQL、四tuple各129行、原生资格、year1/year10000、NUMERIC100、bytea与CASE及同RR快照 |
| ordinary真实授权链 | Plan/Init通过，trust包14.721秒；SourceMode零SQL、原PID/xid/RR、复制、唯一Finish、once、错Permit/kind及终结撤销；不冒充68表读取测试 |
| 沙箱真实Plan/Init初验 | 包43.957秒通过；真实API生成核款/券/客户预占/采购/沙箱capture，旧五reader与第六组合共享同快照。该初验早于时间wire修复，最终原生路径另行回归 |
| 三个真实维护边界初验 | 包62.496秒通过：超限Resource、generic audit infinity的完整EOF/分域未执行，以及PgSleep忙拒绝不扰owner。该初验早于时间wire修复，最终回归另记 |

正常资金fixture会报告`indeterminate`、CoverageComplete=false：七算法全部执行，但采购历史存在恰一条ProposalBarrierUnproven。PB与Sandbox来源图一致；不能凭最终proposal猜回此前barrier。Init注入的真实资金/采购损坏报告mismatch。报告中的物理完成与业务覆盖并非同一个位。

原生SQL专项使用`MONI_TEST_DATABASE_URL`，真实授权/维护使用`C0_MAINTENANCE_DATABASE_URL`，均严格指向专用`127.0.0.1:55439/moni_test`。普通整仓默认回归不自动开启这两个opt-in；不能用默认包成功冒充专项。所有测试仅处理各自随机fixture，按准确名称清理并确认无遗留；不改共享实例配置、不操作预览库、不输出真实凭据。

## 最终集成

原生时间修复后，完整维护脚本实际运行261.557秒：三个新边界、Init及九个旧资源子例全部通过；Plan因根新增断言将PB既有成功状态balanced误写为consistent失败。仅校正QA预期，保留原core语义。首次定向Plan重验在安装前置阶段失败，原诊断未保留错误类别，不能据此确定原因；随后仅补安全错误类别输出，保持五秒catalog和原全部期限，再次Plan定向race通过（28.030秒，主体17.02秒）。不把前一个失败整批命令改写成成功；最终新代码的各目标场景分别有通过证据。

最终`make check`通过（6.33秒），明确指定专库的`make verify-mvp`通过（966.88秒）：286项前端测试、strict构建、全部默认Go race/vet/build完成。默认回归不启用两个C0 opt-in，专项仍按上文单独归属。[机器记录](complete-source-integration.json)绑定最终源码SHA与本轮各日志摘要；最终验证后仅更新文档、记录和架构清单。

19张图已用Mermaid12.2.0独立浏览器解析及渲染，见[图及正文身份](architecture-complete-sources.json)；没有将旧PNG身份转移到本轮。

本轮仍不证明lot全序、protected pre/post、basis/epoch、线上writer、生产维护CLI、真实供应商、正式支付、容器/1Panel或HA；后续按[交付计划](../design/delivery-plan.md)继续。

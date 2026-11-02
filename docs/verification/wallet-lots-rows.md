# V0-WL：原行钱包 lot 独立重放验证

2026-11-02。[冻结合同](../../specs/022-self-service-payments/c0-wallet-lots-rows-contract.md)与[机器记录](wallet-lots-rows.json)。本记录对应CheckWalletLotsRows纯层，所给九组业务原行与16张C0表原列；它不证明数据库读取完整、安装真实性或资金权限。

## 实际核验范围

- 独立big.Int从来源金额推导，FIFO预占、全部正序终态边与逆原收费分次退款；legacy开账和后批退款分开处理。
- 25组Scanned、WS结构计数与本域18方向；原生时间、UTF8/NULL/UUID/整数资格、共享预算和取消全零。
- 独立手算三lot40/50/60，R120/C75/F20+30，最终A15/50/60、S25/0/0，revision4/5/3；legacy开A90/H30，旧未结C12退5、旧terminal退30/50，最终A193/H0/S7。
- 事件/cache/分配声明联动篡改、零额、同刻C序、clock倒退、置换与重复、跨租户、坏前缀保留可算前缀且不伪造后缀orphan。
- 受损后缀仍核本地算式、枚举和复合父关系；已确定的早期额外事件不会被后期损坏降为deferred。

## 复核与缺陷回归

首轮定向检查发现未知钱包kind零分录漏源，以及未来坏receipt使整个tenant过早停止；修复后分别补反向wallet header归属与按唯一operation的延迟屏障。根独立复核继续补充缺journal header的零wallet line、无tenant钱包账户、重复seq0逐原行资格、原始同lot/业务根，以及早期确定extra保留。重复组不选首行，重复父/坏长journal与退款SUM按唯一父或预建索引处理。

测试构造曾因clone helper递归time.Time私有字段在调用核验器前panic；已改为不可变time.Time值拷贝。该辅助错误和早期版本通过均不替代以下最终源码的验证。

## 最终执行

| 检查 | 结果 |
| --- | --- |
| `go test -race -json ./internal/fundinglotverify -count=1` | PASS；9.027秒；钱包25顶层、152 PASS终态（含fuzz seeds），纯包总1051终态 |
| `FuzzWalletLotsRowsBoundedAndPermutationInvariant`，12s、2 workers | PASS；32508次，命令共16.776秒 |
| `make check` | PASS；9.702秒 |
| 明确专库 `make verify-mvp` | PASS；1018.811秒；286项前端、43个Go测试包race、vet与嵌入build |
| 统一架构/README/维护图 | Mermaid 12.2.0共20张解析渲染通过；根已检查模块图和lot细图 |

专库为本机PostgreSQL 16的moni_test。默认整仓没有启用C0专用opt-in，也没有重开候选DDL安装/导出或真实支付进程运行器；本片没有这些实现变化。每条命令日志SHA、最终源码SHA和历史前提交CI身份存于机器记录。所有最终命令期间wallet Go源码/测试摘要保持不变。

[图渲染记录](architecture-wallet-lots.json)包含四份图来源SHA、20张图清单及图片摘要：[模块图](assets/architecture-wallet-lots-application.png)、[原行lot细图](assets/architecture-wallet-lots-detail.png)。图中的lot节点只连接内部WS纯检查，没有reader、维护或在线资金调用箭头。

## 交付边界

没有迁移、SQLreader、ScanKind/once扩展、HTTP/main或资金writer变化。普通预览未升级；本地fixture通过不等于真实供应商、正式支付、容器/1Panel、HA、生产或性能验收。来源深层资格和真实epoch/角色/OID/严格proof codec由未来同session受控组合负责；本报告没有CoverageComplete或authority。

# 资金同租约核验：真实 PostgreSQL 补验

日期：2026-11-02。承接已推送 `a874aab` 的[资金图实现记录](funding-financial-graph.md)。本记录单独保留真实数据库执行结果，不改写上一轮明确跳过的历史。

## 环境与范围

本轮以只读探测确认本机专用 `127.0.0.1:55439/moni_test` 可用：PG 16.15，实际 LOGIN 为测试专用超级用户 `moni_test`；`max_connections=200`，`fsync/synchronous_commit/full_page_writes=on`，会话时区 `Asia/Shanghai`。没有读取或输出真实业务凭据，未连接预览或生产数据库。

专项显式设置 `C0_MAINTENANCE_DATABASE_URL`，按包串行运行 `scripts/maintenance-scan-qa` 的 race 测试。保持原维护 Snapshot、ReadLease、五秒 catalog 发行预算及全部资源上限，不通过延长期限、跳过门禁或减少扫描范围取得通过。

## 原始失败与修正

1. 首轮真实专项耗时 224.508 秒，Plan/Init 与九个资源子例均在“已停服”检查处失败。业务 fixture 使用多个独立连接池，旧代码只关闭最后的 owner 池；新增正式核款/采购 fixture 的 runtime 池仍处于 idle，维护前提正确拒绝。修正为在进入维护前关闭所有 fixture 生产者池。
2. 修正关闭顺序后，Plan/Init 均完成所有扫描，但仍被一条旧结尾断言误判；该次包执行 46.570 秒。fixture 已经双审确认采购成本 12，原 upper=240 应释放；结尾误期望 held=240/spent=0。修正为 held=0/spent=12，保留完整采购事实前后快照比较。
3. 后续完整运行 247.788 秒，Plan/Init 均通过。资源子例暴露旧 legacy 基线仍取自业务 fixture 扩展前，合法核款改变共享 clearing 后被误当作扫描写入。该轮另有四个资源子例在安装前置阶段失败，旧诊断未输出类别，不能据此确定原因。资源 helper 改为在全部合法数据和资源审计准备后，重新捕获 funding、legacy 与采购基线，并逐一比较扫描后值；安装前置失败时输出安全封闭错误类别，不回显 SQL、原文或秘密。后续完整运行未复现该前置失败，不将其归因为已确认的生产缺陷或性能结论。

上述仅修改 QA 生命周期、预期值和观察基线；没有新增业务写入、放松生产权限或改变核验算法。

## 完整执行结果

为避免并行实现中的中间编译状态影响诊断，先将 `a874aab` 的 Git 归档与上述两个 QA 修正文件复制到独立临时源码目录。该副本的 UTF-8 below 定向子例通过（包 27.921 秒），随后完整专项通过（204.801 秒）。它只证明旧生产源码加 QA 修正，不将尚未汇合的新纯算法计入此副本结果。

随后在**原工作仓库**执行同一完整专项，包耗时 **210.188 秒，全部 PASS**：

```sh
C0_MAINTENANCE_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go test -race -count=1 -p 1 -timeout 30m -v ./scripts/maintenance-scan-qa
```

- Plan / Init：34.83 秒（17.54 / 17.29）。
- UTF-8 单字段 below / at / over：53.23 秒。
- 全投影 16MiB below / at / over：52.83 秒。
- 总行数 100000 below / at / over：59.31 秒。

所有子例使用真实迁移、原角色分离、正式业务 API 生成的核款/券/客户预占/采购/沙箱资金事实，以及原维护授权入口。Init 的隔离损坏仍按合同产生确定 mismatch；全部结果在原 Snapshot 终结后失效。没有触发供应商调用或真实支付。

首次失败中保留的一份随机 fixture 在确认原创建时刻、精确三 schema / 八角色、ownership、业务形状与无活动 backend 后按原 QA 清理顺序移除，精确 suffix 的 schema / role 均为0。后续完整子例各自执行原清理断言，未放宽其退出观察期限。临时源码副本不是项目分支或部署目录。

## 交付边界

Plan/Init 专项检查原 Permit、Snapshot 终结撤销、禁止重复 ScanKind、五类 reader、四个资金摘要以及扫描前后事实不变。资源子例分别检查 UTF-8 单字段、全投影字节与总行数的 below/at/over；测试对完整 19 关系/146 字段和迁移历史的独立预算仍保留。

本专项不证明完整七域来源组合、basis、epoch 开账、线上资金 writer、原路退款、真实供应商或生产环境。纯采购证据、周期预算和支付来源 core 的新实现另行记录；后续同一 session 的完整读取仍须独立接线和验收。

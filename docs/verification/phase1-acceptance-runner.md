# 029 第一阶段 41 项终验执行入口

状态：**执行工具已具备，尚未据此宣称第一阶段通过。** 本入口只执行冻结清单，不改变 `specs/029-phase1-acceptance/spec.md` 的技术内容，也不沿用历史局部证据作为本次通过结论。

## 防漏项边界

`scripts/phase1-acceptance-matrix.json` 固定 41 个唯一 ID 和 81 个必需 Go 测试绑定。`scripts/phase1-acceptance.py --check` 逐包检查测试名存在；正式执行使用 `go test -json` 验证每个必需测试实际得到 PASS，缺失、FAIL 或 SKIP 均使对应项失败。

全局门禁包括静态架构/采购布局、vet/build、前端测试与严格构建、全 Go race、current-only PostgreSQL/Compose 验证。身份与邮件 real-main 使用独立串行套件；支付使用本地沙箱真实进程；文本部署场景使用 031 deterministic mock。控制台项另需同一提交的隔离浏览器桌面与 390px 证据。任何必需门禁失败都不能产生 MVP 完成结论；修复后必须在新干净提交上整单重跑 41 项，不能只复跑失败项。

## 自测

```sh
scripts/phase1-acceptance.test.sh
```

这只验证执行器和测试清单，不是 029 验收。

## 正式执行前置

1. 所有实现与验收工具已合入，当前工作树位于最终干净提交；`TEST_DATABASE_URL` 必须逐字为 `postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`。
2. Docker Compose 可用；从同一干净提交构建并启动隔离 app/worker 与 deterministic mock。部署端口与 `current-mvp` 自己创建的 Compose 端口分开，例如分别使用 18081 与 18082。
3. 在隔离浏览器完成所有 `browser=true` 项的桌面与 390px 检查，形成下述 JSON；不能用前端单元测试或 HTTP smoke 替代。
4. 设置 `PHASE1_BASE_URL`、`PHASE1_APP_CONTAINER`、`PHASE1_WORKER_CONTAINER`、`PHASE1_ADMIN_EMAIL`、`PHASE1_ADMIN_PASSWORD` 以及部署 harness 需要的镜像/schema 环境。密码只经环境传入，不写入命令、日志或证据。

浏览器证据格式（示例结构不是实际 PASS 证据）：

```json
{
  "schema_version": 1,
  "commit": "<与最终提交相同的 40 位 SHA>",
  "isolated": true,
  "base_url": "http://127.0.0.1:18081",
  "items": {
    "P1-UI-01": {
      "desktop": {"status": "PASS", "width": 1440, "checks": ["实际执行的检查"], "evidence": "原始记录路径"},
      "mobile390": {"status": "PASS", "width": 390, "checks": ["无横向溢出", "实际执行的检查"], "evidence": "原始记录路径"}
    }
  }
}
```

矩阵中所有浏览器项都必须出现；每个视口必须有非空实际检查记录，390px 宽度必须精确为 390。缺少任一项或视口即为 FAIL，不记为“通过但待补充”。

## 整单执行

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
DEPLOY_QA_PORT=18082 \
PHASE1_BASE_URL='http://127.0.0.1:18081' \
PHASE1_APP_CONTAINER='<隔离 app 容器>' \
PHASE1_WORKER_CONTAINER='<隔离 worker 容器>' \
PHASE1_ADMIN_EMAIL='admin@example.com' \
PHASE1_ADMIN_PASSWORD='<仅注入环境，不写入命令历史>' \
scripts/phase1-acceptance.sh --execute \
  --browser-evidence '<同一提交的 browser-evidence.json>'
```

执行器依次运行全局静态/前端/race/current-mvp、独立 real-main、支付沙箱与部署 mock，再生成：

- `docs/verification/phase1/<ID>.md`：41 个逐项结论、命令套件、日志、测试计数、mock 与未覆盖范围；
- `docs/verification/phase1/summary.json`：机器可读总状态与 41 项计数；
- `docs/verification/phase1/evidence/<提交>/`：原始测试与 harness 日志。

只有 `summary.json` 的 `overall_status=PASS`、`counts={total:41,pass:41,fail:0,skip:0}` 才表示本地第一阶段清单通过。该结论仍不覆盖真实供应商、正式支付商、生产凭据、生产 SLA、多实例 HA 或第二阶段能力。

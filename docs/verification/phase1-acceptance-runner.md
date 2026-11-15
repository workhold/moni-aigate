# 029 第一阶段 41 项终验执行入口

状态：**执行工具已具备，尚未据此宣称第一阶段通过。** 本入口只执行冻结清单，不改变 `specs/029-phase1-acceptance/spec.md` 的技术内容，也不沿用历史局部证据作为本次通过结论。已完成的工具/流程预检见[浏览器与部署预览](phase1-browser-preview.md)，该记录不是最终验收。

## 防漏项边界

`scripts/phase1-acceptance-matrix.json` 固定 41 个唯一 ID 和 81 个必需 Go 测试绑定。`scripts/phase1-acceptance.py --check` 逐包检查测试名存在；正式执行使用 `go test -json` 验证每个必需测试实际得到 PASS，缺失、FAIL 或 SKIP 均使对应项失败。

全局门禁包括静态架构/采购布局、vet/build、前端测试与严格构建、全 Go race、current-only PostgreSQL/Compose 验证。身份与邮件 real-main 使用独立串行套件；支付使用本地沙箱真实进程；文本部署场景使用 031 deterministic mock。控制台项另需同一提交的隔离浏览器桌面与 390px 证据。任何必需门禁失败都不能产生 MVP 完成结论；修复后必须在新干净提交上整单重跑 41 项，不能只复跑失败项。

## 自测

```sh
scripts/phase1-acceptance.test.sh
```

这只验证执行器和测试清单，不是 029 验收。

## 隔离浏览器/部署预览

```bash
scripts/phase1-browser-preview.sh
```

该前台进程使用当前源码构建镜像，在 55439 专库创建随机 `phase1qa_*` schema，执行 current-only 迁移和显式 `operations.ProvisionFor(schema)`，再启动 `moni-phase1-browser-app` / `moni-phase1-browser-worker`。`scripts/phase1-provision` 只用于这个隔离测试目标；生产 `provision` 仍只处理 public，不改变默认部署行为。预览退出时删除自己的容器、网络和随机 schema。

随机管理员密码与运行时密钥只写入 `/Users/vchive/.cache/moni-phase1-browser/` 的 0600 文件，不应打印到终端或复制进证据。浏览器驱动直接读取受保护文件。`Dockerfile`/`compose.yaml` 的 `GOPROXY` 构建参数保持默认代理；本地预览显式使用可访问的公共镜像并继续执行 `go.sum` 校验。

扩展 031 harness 设置 `PHASE1_UI_ACCOUNTS_FILE` 后，浏览器驱动通过 Chrome DevTools Protocol 启动独立 Chrome，执行公开目录、客户/运营页面、Key 创建/Playground/撤销、TOTP 与密码变更、人工核款及退款双审、券发行/审批/兑换、报表 CSV 和站内告警共同确认。所有截图都在一次性秘密清除后保存；证据不含密码、Cookie、API Key、券码或 MFA 秘密。

```bash
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PHASE1_DATABASE_SCHEMA="$(cat /Users/vchive/.cache/moni-phase1-browser/schema)" \
DOCKER_CONTEXT=colima \
python3 scripts/phase1_browser_qa.py \
  --admin-password-file /Users/vchive/.cache/moni-phase1-browser/admin-password \
  --accounts-file /Users/vchive/.cache/moni-phase1-browser/ui-accounts.json \
  --ui-mock-image "$(docker inspect --format '{{.Config.Image}}' moni-phase1-browser-app)" \
  --output /Users/vchive/.cache/moni-phase1-browser/browser-final \
  --final
```

不带 `--final` 的预检全部标为 `PREVIEW`，终验执行器不会接受。最终模式要求干净提交、全部必需账号和独立 UI mock；任一实际操作、页面断言、390px 溢出检查或下载失败都会使命令失败，不能输出部分 PASS。若流程在 TOTP 启用或密码修改后失败，必须重建隔离 schema、harness 账号和浏览器 profile，不能复用已变化的账号文件、禁用 MFA 或恢复旧密码。UI mock 配置通过 `docker create`、`docker cp` 和 `docker start` 注入，不依赖 Colima 的宿主 bind-mount；宿主私有文件保持 0600，容器内固定测试配置对非 root mock 用户可读，启动后仍必须通过健康检查。

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

执行器先停止预览 app/worker 并确认专用实例锁释放，再依次运行全局静态/前端/race/current-mvp、独立 real-main、支付沙箱；随后启动同一组容器并执行部署 mock，避免多个真实主进程同时持有服务锁。最后生成：

- `docs/verification/phase1/<ID>.md`：41 个逐项结论、命令套件、日志、测试计数、mock 与未覆盖范围；
- `docs/verification/phase1/summary.json`：机器可读总状态与 41 项计数；
- `docs/verification/phase1/evidence/<提交>/`：原始测试与 harness 日志。

只有 `summary.json` 的 `overall_status=PASS`、`counts={total:41,pass:41,fail:0,skip:0}` 才表示本地第一阶段清单通过。该结论仍不覆盖真实供应商、正式支付商、生产凭据、生产 SLA、多实例 HA 或第二阶段能力。

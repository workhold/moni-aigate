# 029 第一阶段 41 项终验执行入口

状态：**2026-11-16 已在提交 `2634329bb3befd52749a2c8afb08b3260b6180d1` 上完成 41/41 PASS、0 FAIL、0 必需 SKIP。** 工具/流程预检见[浏览器与部署预览](phase1-browser-preview.md)。最终逐项结论、[机器汇总](phase1/summary.json)、原始测试/部署日志及桌面/390px 浏览器证据均已发布；这不构成真实供应商、正式支付商或生产认证。

## 防漏项边界

`scripts/phase1-acceptance-matrix.json` 固定 41 个唯一 ID 和 82 个必需 Go 测试绑定。`scripts/phase1-acceptance.py --check` 逐包检查测试名存在；正式执行使用 `go test -json` 验证每个必需测试实际得到 PASS，缺失、FAIL 或 SKIP 均使对应项失败。

全局门禁包括静态架构/采购布局、vet/build、前端测试与严格构建、全 Go race、current-only PostgreSQL/Compose 验证。采购 attempt 原始规则差异测试所需的 `PROCUREMENT_ATTEMPT_RULES_DATABASE_URL` 由执行器显式设置为已核验的专用 `TEST_DATABASE_URL`，不能因缺变量产生必需 SKIP。身份与邮件 real-main 使用独立串行套件；支付使用本地沙箱真实进程；文本部署场景使用 031 deterministic mock。控制台项另需同一提交的隔离浏览器桌面与 390px 证据。任何必需门禁失败都不能产生 MVP 完成结论；修复后必须在新干净提交上整单重跑 41 项，不能只复跑失败项。

## 自测

```sh
scripts/phase1-acceptance.test.sh
```

这只验证执行器和测试清单，不是 029 验收。

## 隔离浏览器/部署预览

```bash
DOCKER_CONTEXT=colima-moni-acceptance scripts/phase1-browser-preview.sh
```

该前台进程要求干净工作树，从 HEAD 的私有 `git archive` 快照执行 current-only 迁移、provision、bootstrap 和镜像构建，在 55439 专库创建随机 `phase1qa_*` schema，经显式 `operations.ProvisionFor(schema)` 授权后启动 `moni-phase1-browser-app` / `moni-phase1-browser-worker`。`scripts/phase1-provision` 只用于这个隔离测试目标；生产 `provision` 仍只处理 public，不改变默认部署行为。镜像构建标签记录 source commit、source tree 和干净构建状态，标签对应实际归档内容而非构建前的一次工作树采样。终验主动停止 app/worker 时，预览启动器仍保持运行，不会因日志跟随退出而提前删除同一组容器和 schema；只有明确返回 NotFound 或操作者退出启动器时才执行清理，Docker daemon/context 临时失败会保留资源并重试。

随机管理员密码与运行时密钥只写入 `/Users/vchive/.cache/moni-phase1-browser/` 的 0600 文件，不应打印到终端或复制进证据。浏览器驱动直接读取受保护文件。`Dockerfile`/`compose.yaml` 的 `GOPROXY` 构建参数保持默认代理；本地预览显式使用可访问的公共镜像并继续执行 `go.sum` 校验。直连 npm 不稳定时，可为预览启动命令设置 `PHASE1_BUILD_HTTPS_PROXY=http://host.docker.internal:<代理端口>`，也可分别设置 `PHASE1_BUILD_HTTP_PROXY` 和 `PHASE1_BUILD_NO_PROXY`；这些值只注入 Dockerfile 构建阶段，默认空，不进入最终运行配置。Compose/current-mvp 构建可分别设置 `MONI_BUILD_HTTPS_PROXY`、`MONI_BUILD_HTTP_PROXY` 和 `MONI_BUILD_NO_PROXY`，不要使用通用 `HTTPS_PROXY` 影响宿主机测试；这些变量同样默认空且只影响构建。npm tarball 直连不稳定时，还可分别设置 `PHASE1_BUILD_NPM_REGISTRY` 或 `MONI_BUILD_NPM_REGISTRY` 选择可访问的 registry；这只改变包下载源，`package-lock.json` 的版本与 integrity 校验不变。Alpine 官方索引不可达时，可分别设置 `PHASE1_BUILD_ALPINE_REPOSITORY` 或 `MONI_BUILD_ALPINE_REPOSITORY` 为可访问的 Alpine 镜像根地址；默认仍使用官方源。

扩展 031 harness 设置 `PHASE1_UI_ACCOUNTS_FILE` 后，浏览器驱动通过 Chrome DevTools Protocol 启动独立 Chrome，执行公开目录、客户/运营页面、Key 创建/Playground/撤销、TOTP 与密码变更、人工核款及退款双审、券发行/审批/兑换、报表 CSV 和站内告警共同确认。所有截图都在一次性秘密清除后保存；证据不含密码、Cookie、API Key、券码或 MFA 秘密。

```bash
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PHASE1_DATABASE_SCHEMA="$(cat /Users/vchive/.cache/moni-phase1-browser/schema)" \
DOCKER_CONTEXT=colima-moni-acceptance \
python3 scripts/phase1_browser_qa.py \
  --admin-password-file /Users/vchive/.cache/moni-phase1-browser/admin-password \
  --accounts-file /Users/vchive/.cache/moni-phase1-browser/ui-accounts.json \
  --ui-mock-image "$(docker --context colima-moni-acceptance inspect --format '{{.Image}}' moni-phase1-browser-app)" \
  --output /Users/vchive/.cache/moni-phase1-browser/browser-final \
  --final
```

不带 `--final` 的预检全部标为 `PREVIEW`，终验执行器不会接受。最终模式要求干净提交、全部必需账号、同一 source commit/tree 的干净构建镜像，以及 app/worker/UI mock 的实际镜像 ID 一致；终验开始前还会重新检查运行中的 app/worker，防止浏览器完成后替换部署。最终浏览器在全部操作和清理成功前只发布 `status=PARTIAL`、`completed=false` 的诊断证据，完整成功后才原子改为 `PASS/true`；任一实际操作、页面断言、390px 溢出检查或下载失败都会使命令失败，部分结果不能计入 PASS。重复记录使用递增编号保存截图和观察 JSON，不覆盖制单、审批、兑换等不同阶段。若流程在 TOTP 启用或密码修改后失败，必须重建隔离 schema、harness 账号和浏览器 profile，不能复用已变化的账号文件、禁用 MFA 或恢复旧密码。UI mock 配置通过 `docker create`、`docker cp` 和 `docker start` 注入，不依赖 Colima 的宿主 bind-mount；宿主私有文件保持 0600，容器内固定测试配置对非 root mock 用户可读，启动后仍必须通过健康检查。

## 正式执行前置

1. 所有实现与验收工具已合入，当前工作树位于最终干净提交；`TEST_DATABASE_URL` 必须逐字为 `postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable`。
2. Docker Compose 可用；从同一干净提交构建并启动隔离 app/worker 与 deterministic mock。部署端口与 `current-mvp` 自己创建的 Compose 端口分开，例如分别使用 18081 与 18082。
3. 在隔离浏览器完成所有 `browser=true` 项的桌面与 390px 检查，形成下述 JSON；不能用前端单元测试或 HTTP smoke 替代。
4. 设置 `PHASE1_BASE_URL`、`PHASE1_APP_CONTAINER`、`PHASE1_WORKER_CONTAINER`、`PHASE1_ADMIN_EMAIL`、`PHASE1_ADMIN_PASSWORD` 以及部署 harness 需要的镜像/schema 环境。密码只经环境传入，不写入命令、日志或证据。

浏览器证据格式（示例结构不是实际 PASS 证据）：

```json
{
  "schema_version": 1,
  "status": "PASS",
  "completed": true,
  "commit": "<与最终提交相同的 40 位 SHA>",
  "isolated": true,
  "base_url": "http://127.0.0.1:18081",
  "runtime": {
    "source_commit": "<与最终提交相同的 40 位 SHA>",
    "source_tree": "<最终提交对应的 40 位 tree SHA>",
    "source_clean": true,
    "image_id": "sha256:<app/worker/UI mock 共同的 64 位镜像 ID>"
  },
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
DOCKER_CONTEXT=colima-moni-acceptance \
DEPLOY_QA_PORT=18082 \
PHASE1_BASE_URL='http://127.0.0.1:18081' \
PHASE1_APP_CONTAINER='<隔离 app 容器>' \
PHASE1_WORKER_CONTAINER='<隔离 worker 容器>' \
PHASE1_ADMIN_EMAIL='admin@example.com' \
PHASE1_ADMIN_PASSWORD='<仅注入环境，不写入命令历史>' \
scripts/phase1-acceptance.sh --execute \
  --browser-evidence '<同一提交的 browser-evidence.json>'
```

执行器先停止预览 app/worker 并确认专用实例锁释放，再依次运行全局静态/前端/race/current-mvp、独立 real-main、支付沙箱；随后启动同一组容器，就绪后重新核对 app/worker 的实际镜像 ID 与标签，并强制以浏览器证据中已复核的镜像 ID 执行部署 mock，避免多个真实主进程同时持有服务锁、长跑期间替换容器或依赖可变的本地镜像标签。全部原始日志在运行期间暂存仓库外，避免新增证据文件使只读架构清单失效；套件结束后一次性发布到最终证据目录并把汇总中的日志路径及日志正文中的暂存路径映射到发布位置。最后生成：

- `docs/verification/phase1/<ID>.md`：41 个逐项结论、命令套件、日志、测试计数、mock 与未覆盖范围；
- `docs/verification/phase1/summary.json`：机器可读总状态与 41 项计数；
- `docs/verification/phase1/evidence/<提交>/`：原始测试与 harness 日志。

只有 `summary.json` 的 `overall_status=PASS`、`counts={total:41,pass:41,fail:0,skip:0}` 才表示本地第一阶段清单通过。该结论仍不覆盖真实供应商、正式支付商、生产凭据、生产 SLA、多实例 HA 或第二阶段能力。

## 040 workers=2 候选（默认关闭，当前未通过）

040 PR-D 可显式设置 `PHASE1_ACCEPTANCE_PARALLEL_WORKERS=2`，让本入口生成新 run_id、调用专用 VM 协调器执行 14 片/17 part，再进入相同的 current-MVP 与串行尾部。该变量与 `PHASE1_ACCEPTANCE_GO_EVIDENCE_V2` 互斥；未设置时仍执行上文的 serial v1。候选模式必须同时提供新的仓库外 `--artifacts` 和 `--output`，以便同一干净提交连续运行；外部 workers=2 manifest 也必须附同 attempt 的 runner report/proof。具体命令、隔离与缓存边界见[runner 手册](../../scripts/acceptance-parallel-runner.md)。

当前多轮完整 workers=2 均出现业务断言失败，没有全绿 manifest，未进入本页完整 029 串行尾部，更没有连续两轮或默认化。失败与清理证据见[workers=2 候选与未通过验收记录](acceptance-workers-2.md)。不得把局部分片、canary 或失败但较快的运行写成本页 41/41 通过。

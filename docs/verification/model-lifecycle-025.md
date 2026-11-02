# 025-A1-G 模型真实主进程恢复运行器

2026-11-02。**运行器已编码；两个真实 main SIGKILL 窗口均未执行，尚未验收。** 当前继承环境没有显式 `TEST_DATABASE_URL`，未读取历史路径中的凭据、未猜测连接、未操作任何数据库或评审预览。依据[A1-G窄合同](../../specs/025-production-ha/a1-model-process-contract.md)，默认检查和显式 opt-in 缺库检查都明确 SKIP 真实进程用例。

## 已执行的限定范围

- `go test -race ./scripts/browser-qa -count=1 -v`：退出0，最终包耗时1.659秒；15个顶层测试PASS、4个既有/新增真实进程opt-in顶层测试SKIP。新增四项纯测试验证两个屏障的Close取消与handler join、实际provider对JSON/SSE最终用量的接收、私有接缝互斥和精确专库拒绝规则。它们没有PostgreSQL、资金或真实main。
- `MODEL_LIFECYCLE_REAL_MAIN_TEST=1 go test -race ./scripts/browser-qa -run '^TestModelLifecycleRealMainRecovery$' -count=1 -v`：退出0但该用例明确SKIP，原因是缺专用测试库；包耗时1.583秒。PASS进程退出不代表窗口PASS。
- `go vet ./scripts/browser-qa`、`go build -o /tmp/moni-model-lifecycle-qa-check ./scripts/browser-qa` 和 `git diff --check`：退出0。临时编译产物已删除。

独立复核后又收紧原期限与幂等oracle：初次sending须证明DB时刻距原deadline为290..300秒；重放须精确409/request_exists和原请求ID；在建库内临时对象前只读检查跨role统计观察权。新增纯反例覆盖401/503、错ID、错误码、坏JSON及超限body。最终`go test -race ./scripts/browser-qa -count=1`与`go vet ./scripts/browser-qa`通过（包1.773s）；这是断言校准后的专项，本轮真实main仍未执行。

安全机器记录和源码摘要见[JSON](model-lifecycle-025.json)。本次没有模型生成POST、真实main启动、SIGKILL、恢复、真实PG资金核对或临时schema/role创建，不能从纯fixture推出这些行为已经通过。

## 待执行的运行器合同

[独立fixture](../../scripts/browser-qa/model_fixture.go)只有私有 `options.modelLifecycle` 才装配，正常CLI没有启用入口，普通mock五秒写期限保持。[opt-in运行器](../../scripts/browser-qa/model_lifecycle_test.go)使用既有随机schema/实际APP和projector LOGIN、0600制品与受控kill/restart；domain API只准备合成经营事实，实际推理经客户HTTP进入真实main，不声称setup是HTTP或浏览器验收。

sending窗口在fixture收到唯一已认证POST、尚未返回headers时，交叉核对原sending、客户及采购各116微元预占和零最终证据/结算。最终证据窗口先由客户实际读取SSE内容，再以owner锁本客户AVAILABLE、放行最终usage；要求已提交证据可见且实际APP在该backend上有物理锁等待，客户结算0，采购可信成本已唯一收尾。放行至kill/Wait/角色连接归零的保守总界为1.5秒，低于billing事务实际3秒lock_timeout；独立复核已校准原合同误写的连接默认2秒，运行器阈值无需改变。

每窗口使用同SHA二进制、原secret/DB/origin重启，原middleware 300秒与predecessor额外5秒自然等待；不改数据库期限。窗口1须证明客户pending与采购unknown分别保留原held；窗口2须证明原证据客户唯一收费6微元、采购成本6微元。再观察两轮正常恢复及同幂等HTTP重放，核对POST/attempt/证据/客户结算/账本/采购事件没有重复。成功和失败均走受控生命周期清理，最后核schema/roles/连接/listeners/私密文件清理。

取得显式专库配置并预构建最终main后，可在仓库根目录串行执行（会独占库级InstanceLock，禁止同时运行其他真实main/QA）：

```sh
go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
MODEL_LIFECYCLE_REAL_MAIN_TEST=1 \
  go test -race ./scripts/browser-qa \
  -run '^TestModelLifecycleRealMainRecovery$' -count=1 -v -timeout=15m
```

命令继承部署者显式配置的 `TEST_DATABASE_URL`，目标必须为literal `127.0.0.1:55439/moni_test`。预计两个窗口约11分钟；未运行前不勾选025模型故障验收，不据此宣布HA、供应商或生产已通过。

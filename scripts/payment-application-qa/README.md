# APP 支付离线 QA

本命令为 022-B3 的离线环境、构造与普通 main 真实进程验收入口。`--prepare-only` 从非空 019 双人核款基线升级当前迁移，创建八角色、APP/worker 双证明文件和随机 QA 用户，执行生产 loader、角色检查、APP/worker/projector 构造与原 M/Q 身份自证，最后验证原 A 资金并清理本次资源。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go run ./scripts/payment-application-qa --require-database --prepare-only
```

不设置数据库时输出 SKIP；`--require-database` 将其变成失败。输出只含固定阶段码与环境 UUID，不输出凭据、manifest 路径、签名或数据库原始错误。原 Receiver listener 不启动；APP 的回调端口已经在离线证明内固定。

`--process` 会按当前源码重构建普通 main 和 mock，启动真正的子进程并通过真实 cookie/CSRF 完成 SKU、策略、订单、P 通知及受限 worker 唯一入账；还覆盖重复 ACK/原操作重放、Recovery 只查询、P 提交后 HTTP 回包丢失经独立 query 恢复、带证明 off 历史收尾、无证明 off 历史拒启、第二实例锁拒启、SIGTERM 排空与 SIGKILL 后已确认资金身份不变。

在途生命周期还会以隔离 mock 现有 merchant gate 持锁，观察真实 worker 网络请求在P数据库等待，分别执行实际SIGTERM与精确终止本次APP InstanceLock连接。必须在P既有5秒超时之前证明取消、APP/worker/projector连接0且P无effect，才释放QA gate；接着真实等待原60秒lease自然过期，以生产Expiry/query和原command恢复，验证资金不重复。两场各需约一分钟，不能用改时间/状态或逻辑hold命令加速。管理员控制连接仅离线QA持有，绝不回收到业务池；不增加普通serve测试hook或业务角色权限。

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
  go run ./scripts/payment-application-qa --require-database --process
```

运行 process 前须取得专库普通 main 的 InstanceLock 独占测试窗口；不能与其他 main 集成专项并发，不得夺锁或停止用户预览。子进程仅收到白名单环境和本组件所需私密配置；日志封存于临时 0700 目录的 0600 文件且不默认回显。脚本只在明确 admission 429/Retry-After=1 时重试原样请求和原 operation。

添加 `--browser-hold` 可在 API 验收全部通过后保留最后一个 sandbox 进程，输出安全 origin、0600 `browser-state.json` 和 stop 文件位置。state 仅供本机测试工具读取随机 QA 登录凭据与安全业务 ID，禁止打印其内容、放入命令参数或上传。写入所示 stop 文件或向脚本发送 SIGTERM 后，会等待全部子进程退出、关闭观察连接、验证原 A 并精确删除本环境；hold 期间仍占专库测试窗口。

两个模式互斥，`--browser-hold` 仅适用于 process。`--prepare-only` 不代表普通 main 进程验收；数据库 COMMIT 回包丢失在缺少真实故障设施时始终明确 SKIP，P 提交后 HTTP 回包丢失不可替代这一验收。

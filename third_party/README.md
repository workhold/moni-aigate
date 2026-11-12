# 上游源码集成区

本目录用于存放按许可证纳入同一仓库开发的上游组件。当前修正版只提交接入合同和来源规则，没有把未固定提交的 New API 或 Sub2API 源码伪装成已集成代码。

## 纳入步骤

1. 固定上游仓库、提交 SHA、许可证和 NOTICE 文件，写入 `docs/research/dependency-provenance.md`。
2. 将源码放在 `third_party/<component>/`，保留上游版权头、LICENSE、NOTICE 和原始目录边界。
3. 写 `SOURCE.md`，记录来源 URL、commit、拉取日期、本地修改和对应的源码提供方式。
4. 通过 `internal/provider` 或独立 adapter 接入；不能让上游模块直接写 aigate 钱包、结算或客户身份表。
5. 为 external relay 与 embedded adapter 使用同一协议、usage、错误、成本和未知消费 fixture，再按 route revision 小流量切流。
6. 运行架构清单刷新和发布前的许可证检查；未完成来源记录的代码不能进入 release 镜像。

网络可用时，建议先浅克隆并固定提交，再开始修改：

```sh
git clone --filter=blob:none https://github.com/QuantumNous/new-api.git third_party/new-api
git clone --filter=blob:none https://github.com/Wei-Shaw/sub2api.git third_party/sub2api
git -C third_party/new-api checkout <NEW_API_COMMIT>
git -C third_party/sub2api checkout <SUB2API_COMMIT>
```

两个 `<..._COMMIT>` 必须替换为经过审查的固定 SHA，不能用 `main`、`latest` 或浮动镜像标签。当前环境无法解析 `github.com`，因此本分支没有执行克隆，也没有提交未核实的上游代码。

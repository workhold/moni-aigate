# 上游源码集成区

本目录用于存放按许可证纳入同一仓库开发的上游组件及来源证明。当前没有 vendor 修改版 New API 或 Sub2API 源码；`new-api/` 与 `sub2api/` 保存固定提交的 LICENSE/NOTICE 和 SOURCE 记录，执行形态仍为 `external_relay`。

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

本次通过 GitHub API 按固定提交取得 LICENSE/NOTICE 并记录来源；未执行源码克隆，也未提交未核实的上游代码。`make check` 的 `upstream-provenance-check` 校验这些文件、固定提交、镜像 digest 和本地修改记录。

2026-11-17 起，`third_party/provenance.json` 是组件、固定提交、参考路径、许可证/NOTICE、source offer 与 vendored 文件闭集的机器可读索引。检查器同时核对 SOURCE 记录、组件目录闭集、实际文件摘要和路径/符号链接安全；CI、current-MVP 与 029 静态门实际执行它。该 PASS 只表示仓库元数据和引用完整性，不是供应商、法律、完整 SBOM 或生产资格证明。

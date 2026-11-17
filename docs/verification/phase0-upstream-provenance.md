# 0 阶段上游来源与许可证验收（035 第七项）

日期：2026-11-15。范围只含源码来源、固定提交/镜像、LICENSE/NOTICE 和本地修改记录；不是完整法律意见、容器 SBOM、供应商资格或生产发布认证。

## 固定组件与执行形态

| 组件 | 固定来源 | 本地修改 | 许可证与 NOTICE |
| --- | --- | --- | --- |
| New API | `QuantumNous/new-api@e449cc700eb569cdfc2199cb73e97de46944f43c`；本地 `local/new-api:e449cc7` 构建 | **上游源码未修改**；仅 aigate/Compose/channel 与 QA 编排 | `third_party/new-api/LICENSE`、`NOTICE`、`THIRD-PARTY-LICENSES.md`、`SOURCE.md` |
| Sub2API | `Wei-Shaw/sub2api@10ba8f9b4dc2e6023d4a9879ab77b21841babd10`；官方镜像 `sha256:868c15f3c729af05896c94b3456d91a68bc81dce50c4d7b4557682df7ca92c13` | **上游源码未修改**；仅 aigate/Compose/channel 与 QA 编排 | `third_party/sub2api/LICENSE`、`SOURCE.md`；该提交无单独 NOTICE 文件 |

两个组件当前均为 `external_relay`，没有 vendor 修改版源码。New API 使用 AGPLv3，保留项目链接、作者与第三方 NOTICE；Sub2API 使用 LGPLv3。`third_party/new-api/SOURCE.md` 和 `third_party/sub2api/SOURCE.md` 分别记录上游 URL、固定提交、镜像/构建方式、修改范围和源码提供位置。执行拓扑、真实本地三服务三库和 deterministic supplier 证据见[0 阶段记录](phase0-external-upstream.md)；该 PASS 不是供应商认证。

## 发布检查

`make check` 与 `make verify-mvp` 中的 `upstream-provenance-check` 执行 `go run ./scripts/check-upstream-provenance`，无需网络。它校验：

- LICENSE/NOTICE 文件与下列固定提交原件 SHA-256 完全一致；
- `SOURCE.md` 的固定提交、镜像 digest、未修改声明和本记录路径；
- Compose 的 New API/Sub2API 固定镜像与三个独立数据库标识；
- phase0 记录仍明确 `supplier_authenticated=false`，不把 deterministic 出口冒充真实供应商；
- New API NOTICE 保留原始项目链接、作者归属及 `THIRD-PARTY-LICENSES.md`。

| 文件 | SHA-256 |
| --- | --- |
| `third_party/new-api/LICENSE` | `8486a10c4393cee1c25392769ddd3b2d6c242d6ec7928e1414efff7dfb2f07ef` |
| `third_party/new-api/NOTICE` | `528067fcdf4f9d7e3fdb489d02cbdd36a0efa63fc2eb1686340612c26beb9f33` |
| `third_party/new-api/THIRD-PARTY-LICENSES.md` | `3848811f99c7b7ad802eea23775ccd262228c980f81eef64027f587689b0841d` |
| `third_party/sub2api/LICENSE` | `a5681bf9b05db14d86776930017c647ad9e6e56ff6bbcfdf21e5848288dfaf1b` |

修改上游版本时必须重新获取并审查来源文件、更新 SOURCE 与预期摘要，不能用浮动 `main/latest` 或只改检查常量通过门禁。发布镜像还需另行审查该镜像内完整第三方 notice bundle；本项不宣称完成该完整 SBOM 或生产法律审查。

## 038-PR2 机器清单与执行边界

2026-11-17 新增 `third_party/provenance.json`，把组件、固定仓库/提交、参考源码路径、LICENSE/NOTICE、SOURCE、本地修改和 source offer 纳入严格 JSON 与文件摘要检查；reference-only 组件不得携带 vendored inventory，未来 vendored 组件必须声明精确文件闭集。checker 同时核对固定组件与 SOURCE 的一致性、third_party 目录闭集、路径穿越和符号链接，且在实际 CI、current-MVP 与 029 静态门中执行。

这些增强只证明仓库来源元数据及引用完整性，不读取或认证外部供应商制品，不授予 supplier/profile 或生产资格。原有 035 固定拓扑与本地验收结论保持不变。

# GitHub 首页与 B-P4 设计稿交付

2026-11-03；基线为已推送提交 `0b150c58ebf630f984770bd0fe37c07591b13c51`。本次仅修改文档与设计，不改 Go、SQL、前端、依赖、CI、部署配置或运行权限。

## 交付范围

- [README](../../README.md)突出产品特点、能力状态、首次管理员与模型配置、API调用、本地构建、Docker/1Panel部署和许可边界；MFA默认可选，没有固定默认密码。
- [贡献指南](../../CONTRIBUTING.md)集中说明依赖准备、SDD、架构同步、专用测试库与独立opt-in规则；首页减少维护内部版本细节。
- [B-P4设计稿](../../specs/023-periodic-products/b-procurement-prefix-design.md)完成D0源码审查与交叉复核，逐项对应原采购图十一种历史缺证；精确DDL/权限/锁图/读集与新profile仍待D1冻结，尚未实施。
- [统一架构](../architecture/README.md)、HLD、LLD、交付计划及任务状态同步。现有模块图、请求链路、数据库关系和部署结构没有变化；B-P3与R2门禁保留。

## 本轮实际检查

`make check`退出0，完成只读架构检查、全Go vet及前端TypeScript/Vite构建。并行任务已全部汇合，由集成人完成架构语义复核，再执行显式reviewed refresh与只读检查；最终内容身份由提交中的架构清单固定。

10份受影响文档的1143个本地Markdown路径/锚点检查通过，9段Shell示例通过`bash -n`，没有执行示例中的部署命令。统一架构的Mermaid图源与基线逐字一致；检查共识别31个图块，这个数量不表示本轮重新渲染31图。`git diff --check`通过。

本次没有可执行行为变更，未重跑`make verify-mvp`或真实PG专项，也没有供应商、正式支付、容器、1Panel、HA或性能新增验收。

## 已推送代码的精确 CI

基线提交的[远端CI](https://github.com/workhold/moni-aigate/actions/runs/41581210176)10/10任务成功，已独立核对SHA及artifact。实际PASS/SKIP和父PASS/子SKIP边界保存在[机器记录](periodic-budget-prefix-profile-ci.json)，说明见[组合验证](periodic-budget-prefix-profile.md#精确提交的远端-ci)。它只属于该精确提交；本次文档提交的后续CI须按自己的SHA核对，不能借用此结果。

原本机整仓两项资金timeout、catalog专项race时限及采购历史缺证未因文档交付关闭。仓库配置了部署路径不等于目标环境已验收；发布许可证仍未决定。

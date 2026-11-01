# T1 与 CI 修订的最终整仓验证

2026-11-02。根汇合所有并行结果、独立读审及[SDD冻结合同](../../specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md#10-根冻结范围与尚未开放的能力)后，最终 `make check` 与指定专库 `make verify-mvp` 均退出0。后者实际完成286项前端测试、strict/Vite构建、全默认Go race、全仓vet及嵌入binary构建；billing包209.338秒、server包310.402秒通过。日志、包耗时与binary摘要见[机器记录](maintenance-snapshot-integration.json)。

[维护Snapshot专项](maintenance-snapshot.md)另以显式opt-in执行真实PG/race，115.412秒、124终态全部PASS。默认整仓不设置C0专项变量，不能将这些SKIP当作新profile认证。原静态checker、旧T0及其它阶段证据保留当期范围。

[CI环境修订](github-ci-profile.md)保留首轮真实失败；新增四独立runner/数据库和准确loopback环境还需推送后实际验证，未提前写成远端成功。采购UTC及终止/清理观察的相关真实专库专项已通过，没有修改产品身份、SQL/权限、持久化或金融期限。

提交前独立复核确认 README 的特点、使用和部署顺序与实现一致，并发现开发手册仍使用旧 `localhost:5432/test_user` 测试示例；现已与全仓 fixture 的固定专库、实际 LOGIN、临时 trust 及持久化 profile 对齐，README 链接到该准备步骤。最终整仓通过后仅有文档和证据收口，没有新增产品或测试源码改动。

统一架构、HLD/LLD、SDD状态和README已语义复核。十九张Mermaid由独立临时Chrome实际解析/渲染，当前图示与四份正文SHA见[渲染记录](architecture-maintenance-snapshot.json)；根随后刷新清单并执行只读架构检查。没有触碰长期预览或调用付费供应商/支付商。维护权限、来源全图、basis/epoch及后续商业阶段保持未完成，不把本轮视为整体goal完成。

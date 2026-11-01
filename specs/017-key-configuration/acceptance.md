# 017 验收

2026-10-31，限定范围本地验收通过。证据见 [017验收报告](../../docs/verification/key-configuration.md)；不代表001全项或生产/性能门禁已完成。

- [x] 012独立版本防伪、三版本互不干扰、消费不改版本及最小角色通过。
- [x] 完整strict GET/PUT/创建列表投影、当前权限/CAS/no-op/审计回滚通过。
- [x] 锁内最新spent+held下限、精确金额、过期续期/撤销只读及历史兼容通过。
- [x] Reserve/RecordAttempt/PrepareDispatch最新配置和真实锁等待跨到期复查通过。
- [x] 已sending/在途和最终结算/释放/退款/恢复不受新配置阻断通过。
- [x] 前端三配置CAS/unknown/晚回、日期微秒/金额/历史scope和地域不丢失通过。
- [x] 全量测试、strict build、真实PG race、vet、最终嵌入build及独立复核通过。
- [x] 隔离浏览器代表路径、资金核对/截图/清理及预览升级/资源校验通过。

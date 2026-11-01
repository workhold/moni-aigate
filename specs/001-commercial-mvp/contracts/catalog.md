# 目录与配置实施合同

2026-10-30；FR-03/04/05、NFR-02。先固定以下行为，再实现管理 API 与准入。

- 所有渠道、profile、售价和路由绑定版本不可修改；model/channel可变状态只负责停用/隔离。新渠道秘密产生新credential和channel_version，旧版本在在途结束前保留。
- 发布时锁model，再按ID锁channel/credential；expected_revision不匹配409。空/重复候选、非正权重/容量、悬空/撤销引用拒绝；失败不切换活动版本。
- 供应商base_url不含用户信息/query/fragment，HTTPS默认；私网或HTTP只能通过显式允许的CIDR。每次实际dial校验真实IP，禁止重定向和环境代理。平台供应商秘密单独加密，任何列表或错误不返回秘密。
- 模型profile包含独立的输入硬上界和全部计费输出上界，各自不超过context_limit；两个最大值不必同时可达，允许其和超过共享上下文以保守过预占。缺省输入界为完整上下文，不自动减输出。更小输入界必须有供应商独立强制保证，不能用近似token计数降低资金上界。修正与验收见[004](../../004-conservative-bounds/spec.md)。
- 售价微元/百万Token、CNY、整数；新建版本，事务按DB时钟取最新生效版本。路由版本/成本版本分别持久化，回退不变客户售价。
- 每次attempt查主库重新检查所有撤回权限/停用/凭据与地域交集；排序不能扩大授权，空授权集合拒绝。

首版同一个逻辑模型的发布池每个channel只能出现一次，即使upstream_model不同也拒绝；与运行时每channel最多一次尝试合同一致。非法发布整体回滚并保留原revision。

2026-10-30补齐AC-06：每个准入attempt同一INSERT保存有界的`route_decisions`，候选理由在实际选路锁内生成，不用事后当前状态伪造历史理由。最多1000项/256KiB，只允许channel/version/priority/weight和受控枚举；数据库触发器禁止改写。平台attempt明细可查，客户视图隐藏。未产生attempt的空池失败保留公开错误与资金释放事实，完整失败池解释后续补齐。

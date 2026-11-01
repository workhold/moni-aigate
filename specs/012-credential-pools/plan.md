# 012 实施计划

1. 固定不可变成员/primary、管理CAS、移出与撤销区别、两阶段调度/容量及旧快照合同。
2. 并行实现Catalog+008迁移、Routing成员选择、控制台；主任务接入管理API、billing成员验证、probe/发现与真实HTTP验收。
3. 先专项验证资金/成员/锁序与管理事务，再收取独立静态复核，处理失败并运行完整回归。
4. 前端strict build后嵌入，完成HLD/LLD、手册/路线图与实际验收报告；排空旧预览、owner migrate/provision、受限serve和资源比对。
5. 保留002/008性能专项后移，后续继续批量渠道运营和自助经营，不将凭据池等同完整New API能力。

## 实施合同与文档落点

- 008迁移以数据库生成的`credential_pool_creation_xid`及版本tuple创建事务归属封口：版本和全部成员同事务创建，savepoint保持兼容，已提交版本不得晚追加，恢复历史stamp偶合也不授予追加权。primary延迟成员外键、成员不可变触发器与attempt/probe/发现复合成员外键共同约束身份；角色只追加与owner触发器检查同时保留。
- Catalog负责完整CAS、历史池、秘密身份防复活、元数据保池、永久撤销和发布时live成员检查；Routing负责渠道→成员两阶段权重及原子双层容量。Billing检查冻结成员和实际凭据派发屏障；probe在原事务连接读完整发布池，发现按position取首个live成员。两者均记录实际ID，不授予同渠道生成重试或全池认证。
- 管理接口与控制台只提交完整池，成功后需重新发布完整route；历史只读，移出和永久撤销分开。设计见[HLD §19](../../docs/design/HLD.md#19-012-不可变渠道凭据池)及[LLD §19](../../docs/design/LLD.md#19-012-不可变渠道凭据池)，API操作见[开发手册](../../docs/runbooks/development.md#渠道凭据池与永久撤销)，NA-06与A3范围见[路线图](../../docs/design/new-api-roadmap.md)。
- 备份使用现有完整custom dump→完整SQL→单事务空库恢复，保留post-data触发器顺序。已建schema的data-only属于另行受控运维流程，必须同事务、version先于membership及满足其余依赖，不由现有restore.sh提供。stamp不能被当成跨库业务版本；详细步骤见[恢复手册](../../docs/runbooks/backup-restore.md)。

上述为实施合同说明。各项验收、全量回归、备份演练与预览升级的实际状态由独立验收记录给出，不以本计划代替。

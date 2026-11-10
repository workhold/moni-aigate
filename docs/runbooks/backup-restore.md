# 备份、恢复与重新开放收费

适用于当前 PostgreSQL 16 单实例部署。命令使用 PostgreSQL 客户端、Bash 和 libpq 环境配置；不会接收包含密码的连接 URL 参数。使用匹配服务器主版本的 pg_dump/pg_restore，凭据放受保护的 PGPASSFILE 或 libpq service 配置。本文中的数据库名、主机、路径均需由操作者选择，脚本不删除数据库、不覆盖已有备份或密钥文件。

## 备份

1. 停止入口并排空应用，例如 `docker compose stop -t 320 app`。确认没有独立资金写入进程。脚本还会取得与服务相同的独占维护锁，服务未停止时拒绝备份。
2. 准备权限 0600 的 `SECRET_MATERIAL_FILE`，包含当前 MASTER_KEY_ID、MASTER_KEYS_JSON（仍受保留期备份引用的全部主密钥）及 AUTH_SECRET。后者用于 Key/会话/幂等摘要，不能只备加密主密钥。不要把这个文件交给 shell 执行，也不要写入 Git。当前 Compose 可以通过受保护 env 文件传递这些字段。
3. 在加密、权限受控的存储上执行：

```bash
export PGHOST=127.0.0.1 PGPORT=5432 PGUSER=moni_owner PGDATABASE=moni
export MAINTENANCE_MODE=true
export SECRET_MATERIAL_FILE=/secure/moni-key-material.env
scripts/backup.sh /encrypted-backups/moni-20261030T052600Z
```

可选 `PG_BINDIR` 指向 PostgreSQL 客户端目录。每次使用新目录。产物为 `database.dump`、`key-material.env`、不含密码的 manifest 和 SHA256SUMS；完整结束才删除 INCOMPLETE 标记。目录 0700，密钥文件 0600。SHA256 用于完整性检查，备份来源本身仍需可信。

整个 bundle 含明文主密钥和认证密钥，必须进入加密且访问受控的备份存储；不要把它上传到普通日志、工单或公共对象桶。数据库 dump 虽不含上游明文密钥，仍含业务账本、身份和认证摘要。记录备份时间、版本/镜像来源和异地副本；定期按下节实做恢复。

备份成功后可以按正常步骤重启原服务；停机窗口与备份频率决定实际 RPO，脚本没有 WAL 持续归档能力。

## 恢复到新数据库

1. 在隔离目标建立新的空数据库，停应用、worker 和其他写入者，限制外部 API 入口。使用可信且完整的 bundle。此脚本恢复当前 public schema 布局；不会自动迁移任意旧版备份。
2. 明确目标数据库名，执行：

```bash
export PGHOST=127.0.0.1 PGPORT=5432 PGUSER=moni_owner PGDATABASE=moni_recovered
export MAINTENANCE_MODE=true
export CONFIRM_RESTORE_DATABASE=moni_recovered
scripts/restore.sh /encrypted-backups/moni-20261030T052600Z /secure/recovered-key-material.env
```

目标存在业务关系时直接拒绝，不能用此命令就地覆盖在线库。先校验 bundle；完整提取 dump 的 SQL 后，把恢复、`recovery_gate.required=true`、新 epoch、清除旧提案及恢复审计放在同一 PostgreSQL 事务提交，失败不留下半个恢复库。只有完成后才生成新密钥文件及 `.recovery.env`（RECOVERY_MODE=true）。即使环境文件后续处理失败，数据库中的持久恢复锁仍生效。

012的008迁移增加完整不可变凭据池及数据库创建封口。`channel_versions.credential_pool_creation_xid`由版本INSERT触发器无条件写当前事务stamp；成员INSERT同时检查当前顶层stamp及版本tuple创建事务归属。普通/active子事务检查当前backend持有的xmin事务锁，released savepoint另用MVCC可见tuple及完整子xid仍in-progress证明，不假定释放savepoint后仍持子锁。封口不能向已提交版本晚追加，即使恢复后历史stamp恰好与新事务号相等也不能借此追加；完整机制见[LLD §19.1](../design/LLD.md#191-数据身份与封口)。版本与成员的UPDATE/DELETE仍受不可变触发器保护；attempt、probe、目录发现的实际credential ID均以复合外键指向其冻结成员关系。

现有`backup.sh`生成完整custom dump，`restore.sh`提取完整SQL到空库，并用`psql --single-transaction`执行。完整dump按pre-data→data→post-data恢复，池的INSERT封口及不可变触发器在post-data阶段创建，因此先装载历史stamp、版本与成员数据，再恢复约束/触发器。不要把脚本改成先在目标运行全部迁移、再逐表提交数据的流程，也不要为绕过封口而常规禁用触发器。

若独立运维流程向**已建schema的空表**做data-only恢复，须另行明确编排数据依赖，在一个数据库事务内先插入`channel_versions`，再插入其`channel_version_credentials`；相关channels/credentials依赖也须先满足，primary的延迟外键在提交时检查。使用`psql --single-transaction -v ON_ERROR_STOP=1`或等效单事务执行，不能逐表autocommit或并行拆成不同事务。版本INSERT触发器会重写导入stamp，成员随后在同一创建事务内通过检查。已有版本行不能以data-only补成员。当前`restore.sh`不提供此模式，仍只接受空数据库的完整恢复；不能把任意`pg_restore --data-only`默认顺序当作已验证的导入方案。

3. 以恢复目标的数据库 owner 重新执行角色 provision；dump 特意不恢复所有权/ACL，也不备角色登录密码。使用已有受控部署配置为 app / projection 角色设置各自的新密码。运行当前应用的迁移校验和 `check-ledger`。历史 SQL checksum 不一致时停止，不修改校验记录来绕过。
4. 将恢复文件的主密钥集和 AUTH_SECRET 装入目标服务。使用 `.recovery.env` 额外覆盖，先启动恢复模式。只读恢复界面可以立即启动；退出恢复后的付费启动仍会等待前一个进程的剩余 request deadline 加5秒执行窗口。服务启动还读取持久 `recovery_gate`：环境写 false 也不能跳过数据库锁存。主密钥缺失、迁移缺失或 gate 无法读取时不要开放服务。
5. 验证钱包可用/冻结、Key spent/held、结算/退款/幂等记录、账本平衡及历史版本凭据解密。012还须核对每个当前/历史版本的完整成员/primary、权重/并发、永久撤销状态，以及attempt/probe/发现的实际ID关系；恢复不能复活撤销身份或把旧attempt改指当前池。确认 `/v1/chat/completions` 和入账/退款返回 503，授权钱包/账本查询可读。恢复模式停 worker，不自动处理未知冻结。

013的009迁移增加渠道管理revision与不可变批量回执。备份需同时保留`channels.management_revision`、`channel_batch_operations`及其唯一审计；恢复后核对enabled/隔离/撤销、回执before/after和审计一致性。恢复到旧备份会使revision及回执历史回退，备份点后已执行的操作可能查不到，旧预检也可能与恢复数据巧合匹配。因此恢复门禁期间禁止重播旧批量命令；404不作为未执行的证据，先核对备份后的管理操作及上游影响，废弃旧预检，重新读取/审核后再创建新操作。本版没有跨恢复epoch的命令去重保证，内存中的待确认ID不能替代恢复核对。

## 双人核对后解除

账本平衡不能发现备份点之后已经发生、但不在备份中的收款、上游消费或交付。必须独立对照银行/收款回执、供应商账单及外部业务记录，逐项登记遗漏/不确定项目，不能以“恢复成功”替代核对，也不能让恢复流程重新发送生成请求。

1. 已完成最近再认证的finance_operator读取 `GET /api/recovery/status` 的 epoch，再提交 `POST /api/recovery/propose`，字段为 `epoch`、受控 `evidence_ref` 和 `reason`。证据引用指向独立受控的核对材料，不上传原始密钥或推理正文。提案一旦提出即冻结。
2. 不同的、当前有效的finance_approver同样通过最近再认证，提交 `POST /api/recovery/approve`，带相同 epoch 和 proposal_id。服务内再次验证两人的角色/状态及账本平衡；旧 epoch、自己审批、材料变更或审计写入失败都不能解除。
3. 审批只清恢复 gate 并持久审计，不追扣客户、不释放或结算旧请求。当前进程仍保持恢复模式。确认外部核对记录已经完成后，撤掉环境中的 RECOVERY_MODE=true 覆盖并重启服务。核对健康状态、只进行已批准的小额验证，再逐步恢复入口。

新的恢复操作会生成新epoch，不能拿旧审批重放解锁。不能让管理员直接代替财务双人批准。MFA默认可选；部署开启`MFA_ENFORCE_PLATFORM=true`时未绑定财务账号须先绑定，已启用MFA的账号无论配置如何均须在登录和再认证时验证该因素。丢失已启用的因素时执行独立身份恢复流程，不通过改配置/数据库跳过验证或伪造审批状态。

## 主密钥轮换与保留

先部署能解 old/new 的 MASTER_KEYS_JSON，把 active 指到 new，再停机并运行 `MAINTENANCE_MODE=true moni-aigate rewrap-secrets`。它只重包同一个 credential/user 记录的密文；供应商秘密轮换则必须创建新 credential/channel 版本并重新发布 route，二者不同。

确认所有在线记录可由 new 解密后，才讨论在线退役 old；尚在保留期内的旧备份仍需要旧主密钥，归档不得一起销毁。AUTH_SECRET 也须随对应备份保留；本版没有“随意轮换 AUTH_SECRET 后旧 Key 自动兼容”的功能。

## 可重复演练

从仓库根目录执行以下命令。演练使用当前唯一 current-only 计划，先对源库执行 `migrations.VerifyCurrent`，恢复后再次执行同一校验；它不会调用历史 `migrations.Verify`，也不会使用 5432 或生产数据库：

```bash
PATH="/opt/homebrew/bin:$PATH" \
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
PG_BINDIR=/opt/homebrew/opt/postgresql@16/bin \
go run ./scripts/backup-restore-drill.go
```

仅使用专门测试实例和有建库权限的测试账号。程序只创建/删除本次生成的 `moni_drill_<random>_source/_restore` 数据库，使用伪造资金和密钥，验证 current baseline、snapshot、持久恢复锁、HTTP 关闭收费、旧主密钥、非空目标拒绝及 checksum 损坏拒绝，并写 `docs/verification/backup-restore-local.json`。Docker Compose 没有暴露 PostgreSQL 端口时，应从专用 backup service 或等价的 Compose 内网环境运行同一脚本，并把 `PGHOST`、`PGPORT`、`PGUSER`、`PGDATABASE` 传给目标数据库。不要用生产 DATABASE_URL 替代测试连接。
